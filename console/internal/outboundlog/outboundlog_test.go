package outboundlog

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func readJSONLRecords(t *testing.T, path string) []ColabOneOutboundRecord {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open log %s: %v", path, err)
	}
	defer f.Close()
	var records []ColabOneOutboundRecord
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec ColabOneOutboundRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("invalid jsonl line %q: %v", line, err)
		}
		records = append(records, rec)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan log: %v", err)
	}
	return records
}

func totalLogSize(t *testing.T, logPath string) int64 {
	t.Helper()
	var total int64
	for _, p := range []string{logPath, logPath + ".1"} {
		if info, err := os.Stat(p); err == nil {
			total += info.Size()
		}
	}
	return total
}

func TestDefaultMaxBytesIs20MiB(t *testing.T) {
	if COLABONE_OUTBOUND_LOG_MAX_BYTES != 20*1024*1024 {
		t.Fatalf("COLABONE_OUTBOUND_LOG_MAX_BYTES = %d, want %d", COLABONE_OUTBOUND_LOG_MAX_BYTES, 20*1024*1024)
	}
	logger := NewColabOneOutboundLogger(filepath.Join(t.TempDir(), "logs", COLABONE_OUTBOUND_LOG_FILENAME), 0)
	if logger.MaxTotalBytes() != 20*1024*1024 {
		t.Fatalf("default MaxTotalBytes = %d, want 20 MiB", logger.MaxTotalBytes())
	}
}

func TestOutboundTransportLogsAndRedactsSecrets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/drive/v3/files":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"files":[]}`))
		case "/fail":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "15")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate_limited"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	logPath := filepath.Join(t.TempDir(), "logs", COLABONE_OUTBOUND_LOG_FILENAME)
	logger := NewColabOneOutboundLogger(logPath, 64*1024)
	defer logger.Close()

	client := WrapHTTPClient(srv.Client(), logger, "drive_api", "")

	reqURL := srv.URL + "/drive/v3/files?authuser=0&fields=files(id,name)&access_token=ya29.super-secret-access&colab-runtime-proxy-token=proxy-secret-999&key=AIzaSecretKey&code=4/0AbCdEfGhIjKlMnOpQr&oauth_token=secret-oauth"
	req, err := http.NewRequest("", reqURL, nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	req.Header.Set("Authorization", "Bearer ya29.header-bearer-secret")
	req.Header.Set("Range", "bytes=0-1023")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do req: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != `{"files":[]}` {
		t.Fatalf("body modified: %q", string(body))
	}

	req2, _ := http.NewRequest(http.MethodPost, srv.URL+"/fail?client_secret=GOCSPX-secretValue12345&refresh_token=1//09abcdef123456789", strings.NewReader(`{"hello":"world"}`))
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("do req2: %v", err)
	}
	_ = resp2.Body.Close()

	rawBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	raw := string(rawBytes)
	for _, secret := range []string{
		"ya29.super-secret-access",
		"ya29.header-bearer-secret",
		"proxy-secret-999",
		"AIzaSecretKey",
		"4/0AbCdEfGhIjKlMnOpQr",
		"GOCSPX-secretValue12345",
		"1//09abcdef123456789",
		"secret-oauth",
	} {
		if strings.Contains(raw, secret) {
			t.Fatalf("secret %q leaked into outbound log:\n%s", secret, raw)
		}
	}

	records := readJSONLRecords(t, logPath)
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}

	r0 := records[0]
	if r0.Component != "drive_api" || r0.Method != "GET" || r0.Path != "/drive/v3/files" || r0.StatusCode != 200 || !r0.OK {
		t.Fatalf("unexpected record[0]: %+v", r0)
	}
	if !strings.Contains(r0.Query, "fields=files(id,name)") || !strings.Contains(r0.Query, "authuser=0") {
		t.Fatalf("expected non-sensitive query params (fields, authuser) preserved, got %q", r0.Query)
	}
	if !strings.Contains(r0.Query, "access_token=[REDACTED]") || !strings.Contains(r0.Query, "colab-runtime-proxy-token=[REDACTED]") || !strings.Contains(r0.Query, "oauth_token=[REDACTED]") {
		t.Fatalf("expected sensitive query params redacted, got %q", r0.Query)
	}
	if !strings.Contains(r0.Detail, "range=bytes=0-1023") {
		t.Fatalf("expected range detail, got %q", r0.Detail)
	}

	r1 := records[1]
	if r1.StatusCode != 429 || r1.OK || r1.ReqBytes != int64(len(`{"hello":"world"}`)) {
		t.Fatalf("unexpected record[1]: %+v", r1)
	}
	if !strings.Contains(r1.Detail, "retry_after=15") {
		t.Fatalf("expected retry_after detail on 429, got %q", r1.Detail)
	}
}

func TestStrictMaxTotalBytesRotationNeverExceedsCap(t *testing.T) {
	const capBytes int64 = 16 * 1024 // 16 KiB total => 8 KiB active + 8 KiB backup
	logPath := filepath.Join(t.TempDir(), "logs", COLABONE_OUTBOUND_LOG_FILENAME)
	logger := NewColabOneOutboundLogger(logPath, capBytes)
	defer logger.Close()

	for i := 0; i < 260; i++ {
		logger.Record(ColabOneOutboundRecord{
			Component:  "remote_rpc",
			Protocol:   "rpc",
			Method:     "POST",
			Scheme:     "remote",
			Host:       "m-s-test-endpoint",
			Path:       "/api/v1/drive/sync",
			StatusCode: 200,
			OK:         true,
			DurationMs: int64(i + 10),
			Detail:     strings.Repeat("sync-status-ok-", 8),
		})
		total := totalLogSize(t, logPath)
		if total > capBytes {
			t.Fatalf("iteration %d: total log size %d exceeded cap %d", i, total, capBytes)
		}
	}

	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("active log missing after rotations: %v", err)
	}
	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Fatalf("rotated backup log missing after rotations: %v", err)
	}
	activeInfo, _ := os.Stat(logPath)
	backupInfo, _ := os.Stat(logPath + ".1")
	if activeInfo.Size() > capBytes/2 || backupInfo.Size() > capBytes/2 {
		t.Fatalf("file exceeded half limit: active=%d backup=%d half=%d", activeInfo.Size(), backupInfo.Size(), capBytes/2)
	}
	recs := readJSONLRecords(t, logPath)
	if len(recs) == 0 || recs[len(recs)-1].Seq != 260 {
		t.Fatalf("expected latest seq 260 in active log, got %+v", recs)
	}
}

func TestConcurrentWritesAndClose(t *testing.T) {
	const capBytes int64 = 16 * 1024
	logPath := filepath.Join(t.TempDir(), "logs", COLABONE_OUTBOUND_LOG_FILENAME)
	logger := NewColabOneOutboundLogger(logPath, capBytes)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				logger.Record(ColabOneOutboundRecord{
					Component:  "colab",
					Protocol:   "http",
					Method:     "GET",
					Scheme:     "https",
					Host:       "colab.research.google.com",
					Path:       "/tun/m/assignments",
					StatusCode: 200,
					OK:         true,
					DurationMs: int64(id*10 + j),
				})
			}
		}(g)
	}
	wg.Wait()

	if total := totalLogSize(t, logPath); total > capBytes {
		t.Fatalf("concurrent total size %d exceeded cap %d", total, capBytes)
	}

	logger.Close()
	sizeAfterClose := totalLogSize(t, logPath)
	logger.Record(ColabOneOutboundRecord{
		Component: "colab",
		Method:    "GET",
		Host:      "colab.research.google.com",
		Path:      "/after-close",
	})
	if totalLogSize(t, logPath) != sizeAfterClose {
		t.Fatalf("expected no writes after Close()")
	}
}

func TestNetworkErrorAndSymlinkSafety(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "logs", COLABONE_OUTBOUND_LOG_FILENAME)
	logger := NewColabOneOutboundLogger(logPath, 32*1024)
	defer logger.Close()

	// Network error via custom failing transport should redact credentials in error text.
	failRT := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New(`dial tcp failed for https://user:superpass@proxy.example.com:8080/path?token=ya29.leakme`)
	})
	tr := WrapTransport(failRT, logger, "colab_assign_proxy", "proxy.example.com:8080")
	req, _ := http.NewRequest(http.MethodPost, "https://colab.research.google.com/tun/m/assign?authuser=0", nil)
	_, err := tr.RoundTrip(req)
	if err == nil {
		t.Fatalf("expected transport error")
	}
	raw, _ := os.ReadFile(logPath)
	if strings.Contains(string(raw), "superpass") || strings.Contains(string(raw), "ya29.leakme") {
		t.Fatalf("credentials leaked in error log: %s", string(raw))
	}
	recs := readJSONLRecords(t, logPath)
	if len(recs) != 1 || recs[0].OK || recs[0].Proxy != "proxy.example.com:8080" || recs[0].Error == "" {
		t.Fatalf("unexpected error record: %+v", recs)
	}

	// Refuses symlink log target on POSIX systems.
	if runtime.GOOS != "windows" {
		target := filepath.Join(tmp, "victim.txt")
		if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
			t.Fatalf("write target: %v", err)
		}
		symLog := filepath.Join(tmp, "sym-outbound.log")
		if err := os.Symlink(target, symLog); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		symLogger := NewColabOneOutboundLogger(symLog, 32*1024)
		symLogger.Record(ColabOneOutboundRecord{Component: "test", Method: "GET", Host: "example.com"})
		got, _ := os.ReadFile(target)
		if string(got) != "untouched" {
			t.Fatalf("symlink target was modified: %q", string(got))
		}
	}
}

func TestTelegramBotTokenRedactionQuerySanitizationWrapOverrideAndFallbackJSON(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "logs", COLABONE_OUTBOUND_LOG_FILENAME)
	logger := NewColabOneOutboundLogger(logPath, 64*1024)
	defer logger.Close()

	// 1. Direct Record() sanitizes sensitive query keys and redacts Telegram bot tokens (colon & %3A, 8+ chars) across Path, Query, Detail, Error.
	logger.Record(ColabOneOutboundRecord{
		Component: "notifier_telegram",
		Method:    "POST",
		Scheme:    "https",
		Host:      "api.telegram.org",
		Path:      "/bot123456:ShortTok8/sendMessage",
		Query:     "authuser=0&access_token=my_direct_secret&key=my_api_key&note=bot777888%3aEncodedTok9",
		Detail:    "relay failed for bot555666:DetailTok8 and bot999000%3AUpperTok9",
		Error:     "upstream error at /bot111222:ErrorTok8 and bot333444%3aLowerTok9",
	})

	// 2. WrapTransport overrides component/proxy on an already-wrapped transport without double logging.
	var rtHits int
	baseRT := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rtHits++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("ok")),
		}, nil
	})
	wrapped1 := WrapTransport(baseRT, logger, "base_comp", "")
	wrapped2 := WrapTransport(wrapped1, logger, "telegram_override", "socks5://127.0.0.1:1080")

	req, err := http.NewRequest(http.MethodGet, "https://example.com/test?q=100%25&token=secret_val&authuser=0", nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	resp, err := wrapped2.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip failed: %v", err)
	}
	_ = resp.Body.Close()

	if rtHits != 1 {
		t.Fatalf("expected 1 underlying RoundTrip, got %d", rtHits)
	}

	rawBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	raw := string(rawBytes)
	for _, secret := range []string{
		"ShortTok8",
		"my_direct_secret",
		"my_api_key",
		"EncodedTok9",
		"DetailTok8",
		"UpperTok9",
		"ErrorTok8",
		"LowerTok9",
		"secret_val",
	} {
		if strings.Contains(raw, secret) {
			t.Fatalf("secret %q leaked into outbound log:\n%s", secret, raw)
		}
	}

	recs := readJSONLRecords(t, logPath)
	if len(recs) != 2 {
		t.Fatalf("expected exactly 2 records (no double logging on WrapTransport override), got %d: %+v", len(recs), recs)
	}
	if !strings.Contains(recs[0].Query, "access_token=[REDACTED]") || !strings.Contains(recs[0].Query, "key=[REDACTED]") || !strings.Contains(recs[0].Query, "authuser=0") {
		t.Fatalf("expected direct Record() Query sanitized cleanly, got %q", recs[0].Query)
	}
	if recs[1].Component != "telegram_override" || recs[1].Proxy != "socks5://127.0.0.1:1080" {
		t.Fatalf("expected WrapTransport override component/proxy, got %+v", recs[1])
	}
	if !strings.Contains(recs[1].Query, "q=100%") || !strings.Contains(recs[1].Query, "token=[REDACTED]") {
		t.Fatalf("expected RoundTrip query with percent sign preserved and token redacted, got %q", recs[1].Query)
	}

	// 3. marshalBoundedJSONLine fallback always produces valid JSON even with special characters and huge fields.
	huge := ColabOneOutboundRecord{
		Timestamp:  "2026-11-28T10:00:00.000Z",
		Seq:        42,
		Component:  `comp"quote\slash`,
		Method:     "POST",
		Host:       strings.Repeat("h", 5000),
		Path:       strings.Repeat("p", 5000),
		Endpoint:   strings.Repeat("e", 5000),
		Proxy:      strings.Repeat("x", 5000),
		StatusCode: 500,
		OK:         false,
		DurationMs: 99,
	}
	line := marshalBoundedJSONLine(huge)
	if len(line) == 0 || len(line) > maxRecordLineBytes {
		t.Fatalf("expected bounded fallback line <= %d bytes, got %d", maxRecordLineBytes, len(line))
	}
	var parsed ColabOneOutboundRecord
	if err := json.Unmarshal(line, &parsed); err != nil {
		t.Fatalf("fallback line is not valid JSON: %v (line=%q)", err, string(line))
	}
	if parsed.Component != `comp"quote\slash` || parsed.Seq != 42 {
		t.Fatalf("unexpected parsed fallback record: %+v", parsed)
	}

	// 4. Positive maxTotalBytes below 16 KiB clamps cleanly to 16 KiB.
	clamped := NewColabOneOutboundLogger(filepath.Join(t.TempDir(), "clamped.log"), 1024)
	if clamped.MaxTotalBytes() != COLABONE_OUTBOUND_LOG_MIN_BYTES || clamped.MaxTotalBytes() != 16*1024 {
		t.Fatalf("expected clamped MaxTotalBytes 16 KiB, got %d", clamped.MaxTotalBytes())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
