// Package outboundlog provides a thread-safe, credential-redacted, size-bounded
// persistent JSONL logger for every outbound network request made by ColabOne.
//
// Total disk usage across the active log file and its single rotated backup
// (<path>.1) is strictly bounded to MaxTotalBytes (default 20 MiB).
package outboundlog

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/luolongfei/ColabOne/console/internal/activity"
	"github.com/luolongfei/ColabOne/console/internal/fsutil"
)

const (
	// COLABONE_OUTBOUND_LOG_FILENAME is the default filename stored under <configDir>/logs/.
	COLABONE_OUTBOUND_LOG_FILENAME = "outbound-requests.log"

	// COLABONE_OUTBOUND_LOG_MAX_BYTES is the default strict maximum total disk size
	// across active + rotated outbound request logs (20 MiB).
	COLABONE_OUTBOUND_LOG_MAX_BYTES int64 = 20 * 1024 * 1024

	// COLABONE_OUTBOUND_LOG_MIN_BYTES is the minimum supported total size cap (16 KiB).
	COLABONE_OUTBOUND_LOG_MIN_BYTES int64 = 16 * 1024

	maxRecordLineBytes  = 4096
	maxComponentRunes   = 48
	maxProtocolRunes    = 16
	maxMethodRunes      = 16
	maxSchemeRunes      = 16
	maxHostRunes        = 180
	maxPathRunes        = 300
	maxQueryRunes       = 300
	maxQueryValRunes    = 100
	maxProxyRunes       = 120
	maxEndpointRunes    = 120
	maxDetailRunes      = 320
	maxErrorRunes       = 400
	redactedPlaceholder = "[REDACTED]"
)

var (
	outboundLineReplacer = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ")

	outboundSecretTokenRe = regexp.MustCompile(
		`(?i)(?:Bearer\s+[^\s,'"\x60;}\]]+|Basic\s+[^\s,'"\x60;}\]]+|ya29\.[\w.-]+|1//[\w./+-]{12,}|hf_[\w-]+|co_web_[\w-]{20,}|co_(?:gal|wf)_[A-Za-z0-9._-]{16,}|co_[\w-]{40,}|\bsk-[A-Za-z0-9._-]{10,}\b|eyJ[A-Za-z0-9_+/=-]+\.eyJ[A-Za-z0-9_+/=-]+\.[A-Za-z0-9_+/=-]+|eyJ[A-Za-z0-9_+/=-]{20,}|4/[\w./+%-]{12,}|GOCSPX-[A-Za-z0-9_-]{12,})`,
	)

	outboundUserInfoURLRe = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)([^\s/@"'<>]+@)`)

	sensitiveQueryKeyFragments = []string{
		"token",
		"secret",
		"password",
		"passwd",
		"pwd",
		"apikey",
		"api_key",
		"api-key",
		"auth",
		"credential",
		"cookie",
		"session",
		"xsrf",
		"csrf",
		"signature",
		"verifier",
		"nonce",
		"jwt",
		"bearer",
		"private",
		"passcode",
		"ticket",
	}

	sensitiveQueryExactKeys = map[string]bool{
		"key":   true,
		"code":  true,
		"sig":   true,
		"pass":  true,
		"state": true,
		"pw":    true,
	}
)

// ColabOneOutboundRecord is a single structured, credential-redacted outbound request entry.
type ColabOneOutboundRecord struct {
	Timestamp  string `json:"ts"`
	Seq        uint64 `json:"seq"`
	Component  string `json:"component"`
	Protocol   string `json:"protocol,omitempty"`
	Method     string `json:"method"`
	Scheme     string `json:"scheme,omitempty"`
	Host       string `json:"host,omitempty"`
	Path       string `json:"path,omitempty"`
	Query      string `json:"query,omitempty"`
	StatusCode int    `json:"status_code,omitempty"`
	OK         bool   `json:"ok"`
	DurationMs int64  `json:"duration_ms"`
	ReqBytes   int64  `json:"req_bytes,omitempty"`
	RespBytes  int64  `json:"resp_bytes,omitempty"`
	Proxy      string `json:"proxy,omitempty"`
	Endpoint   string `json:"endpoint,omitempty"`
	Detail     string `json:"detail,omitempty"`
	Error      string `json:"error,omitempty"`
}

// ColabOneOutboundLogger appends structured JSONL outbound request records to a
// rotating disk log strictly capped at MaxTotalBytes across active and backup files.
type ColabOneOutboundLogger struct {
	mu            sync.Mutex
	logPath       string
	maxTotalBytes int64
	seq           uint64
	closed        bool
	nowFn         func() time.Time
}

// NewColabOneOutboundLogger creates a bounded outbound request logger.
// When maxTotalBytes <= 0, COLABONE_OUTBOUND_LOG_MAX_BYTES (20 MiB) is used.
// Positive values below COLABONE_OUTBOUND_LOG_MIN_BYTES (4 KiB) are clamped to 4 KiB.
// When logPath is empty, Record is a safe no-op.
func NewColabOneOutboundLogger(logPath string, maxTotalBytes int64) *ColabOneOutboundLogger {
	trimmed := strings.TrimSpace(logPath)
	if maxTotalBytes <= 0 {
		maxTotalBytes = COLABONE_OUTBOUND_LOG_MAX_BYTES
	} else if maxTotalBytes < COLABONE_OUTBOUND_LOG_MIN_BYTES {
		maxTotalBytes = COLABONE_OUTBOUND_LOG_MIN_BYTES
	}
	return &ColabOneOutboundLogger{
		logPath:       trimmed,
		maxTotalBytes: maxTotalBytes,
		nowFn:         time.Now,
	}
}

// Path returns the configured active log file path.
func (l *ColabOneOutboundLogger) Path() string {
	if l == nil {
		return ""
	}
	return l.logPath
}

// MaxTotalBytes returns the configured total size ceiling across active and rotated files.
func (l *ColabOneOutboundLogger) MaxTotalBytes() int64 {
	if l == nil {
		return 0
	}
	return l.maxTotalBytes
}

// Close disables subsequent disk writes. Safe to call multiple times or on nil.
func (l *ColabOneOutboundLogger) Close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
}

// Record sanitizes and persists a single outbound request record.
// It is thread-safe, never panics, and never lets active + backup exceed maxTotalBytes.
func (l *ColabOneOutboundLogger) Record(rec ColabOneOutboundRecord) {
	if l == nil || l.logPath == "" {
		return
	}

	sanitized := sanitizeOutboundRecord(rec)

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return
	}
	nowFn := l.nowFn
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn().UTC()
	l.seq++
	sanitized.Seq = l.seq
	if sanitized.Timestamp == "" {
		sanitized.Timestamp = now.Format("2006-01-02T15:04:05.000Z")
	}

	line := marshalBoundedJSONLine(sanitized)
	if len(line) == 0 {
		return
	}
	l.appendDiskLocked(line)
}

// RecordURL parses rawURL cleanly into sanitized scheme/host/path/query fields and records rec.
func (l *ColabOneOutboundLogger) RecordURL(rec ColabOneOutboundRecord, rawURL string) {
	if l == nil || l.logPath == "" {
		return
	}
	if u, err := url.Parse(strings.TrimSpace(rawURL)); err == nil && u != nil {
		applySanitizedURL(&rec, u)
	}
	l.Record(rec)
}

func (l *ColabOneOutboundLogger) appendDiskLocked(line []byte) {
	if l.logPath == "" || l.closed || len(line) == 0 {
		return
	}
	maxTotal := l.maxTotalBytes
	if maxTotal <= 0 {
		maxTotal = COLABONE_OUTBOUND_LOG_MAX_BYTES
	} else if maxTotal < COLABONE_OUTBOUND_LOG_MIN_BYTES {
		maxTotal = COLABONE_OUTBOUND_LOG_MIN_BYTES
	}
	halfLimit := maxTotal / 2
	if int64(len(line)) > halfLimit {
		// Defensive guard: never allow a single line to breach halfLimit.
		return
	}

	dir := filepath.Dir(l.logPath)
	if dirInfo, err := os.Lstat(dir); err == nil {
		if dirInfo.Mode()&os.ModeSymlink != 0 || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeIrregular != 0 {
			return
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	if dirInfo, err := os.Lstat(dir); err != nil || dirInfo.Mode()&os.ModeSymlink != 0 || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeIrregular != 0 {
		return
	}

	backup := l.logPath + ".1"
	if binfo, berr := os.Lstat(backup); berr == nil {
		if binfo.Mode()&os.ModeSymlink != 0 || !binfo.Mode().IsRegular() {
			return
		}
		if binfo.Size() > halfLimit {
			// Guard against externally oversized backup files so total stays <= maxTotal.
			if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
				return
			}
		}
	} else if !errors.Is(berr, os.ErrNotExist) {
		return
	}

	linfo, lerr := os.Lstat(l.logPath)
	if lerr == nil {
		if linfo.Mode()&os.ModeSymlink != 0 || !linfo.Mode().IsRegular() {
			return
		}
		if linfo.Size()+int64(len(line)) > halfLimit {
			if linfo.Size() <= halfLimit {
				if err := fsutil.RenameReplace(l.logPath, backup); err != nil {
					_ = os.Remove(backup)
					if err := fsutil.RenameReplace(l.logPath, backup); err != nil {
						return
					}
				}
			} else {
				// Active file was externally oversized (> halfLimit): drop it so backup never breaches halfLimit.
				if err := os.Remove(l.logPath); err != nil && !errors.Is(err, os.ErrNotExist) {
					return
				}
			}
			linfo, lerr = os.Lstat(l.logPath)
			if lerr == nil {
				if linfo.Mode()&os.ModeSymlink != 0 || !linfo.Mode().IsRegular() {
					return
				}
			} else if !errors.Is(lerr, os.ErrNotExist) {
				return
			}
		}
	} else if !errors.Is(lerr, os.ErrNotExist) {
		return
	}

	openFlags := os.O_APPEND | os.O_WRONLY
	if lerr != nil {
		openFlags = os.O_CREATE | os.O_EXCL | os.O_WRONLY
	}
	f, err := os.OpenFile(l.logPath, openFlags, 0o600)
	if err != nil {
		return
	}
	finfo, fstatErr := f.Stat()
	if fstatErr != nil || !finfo.Mode().IsRegular() {
		_ = f.Close()
		return
	}
	if lerr == nil {
		if !os.SameFile(linfo, finfo) {
			_ = f.Close()
			return
		}
	} else {
		postInfo, postErr := os.Lstat(l.logPath)
		if postErr != nil || postInfo.Mode()&os.ModeSymlink != 0 || !postInfo.Mode().IsRegular() || !os.SameFile(postInfo, finfo) {
			_ = f.Close()
			return
		}
	}
	_ = f.Chmod(0o600)
	_, _ = f.Write(line)
	_ = f.Close()
}

// ColabOneOutboundTransport wraps an http.RoundTripper to record every outbound
// HTTP/HTTPS/WebSocket-upgrade request into a ColabOneOutboundLogger without
// mutating requests or consuming response bodies.
type ColabOneOutboundTransport struct {
	Base      http.RoundTripper
	Logger    *ColabOneOutboundLogger
	Component string
	Proxy     string
}

var _ http.RoundTripper = (*ColabOneOutboundTransport)(nil)

// WrapTransport returns an http.RoundTripper that logs every request executed through base.
// If logger is nil, base is returned unchanged.
func WrapTransport(
	base http.RoundTripper,
	logger *ColabOneOutboundLogger,
	component string,
	proxyLabel string,
) http.RoundTripper {
	if logger == nil {
		return base
	}
	if base == nil {
		base = http.DefaultTransport
	}
	if existing, ok := base.(*ColabOneOutboundTransport); ok && existing != nil && existing.Logger == logger {
		return base
	}
	return &ColabOneOutboundTransport{
		Base:      base,
		Logger:    logger,
		Component: strings.TrimSpace(component),
		Proxy:     strings.TrimSpace(proxyLabel),
	}
}

// WrapHTTPClient returns a shallow copy of client whose Transport logs outbound requests.
// The original client struct is never mutated. When logger is nil, client is returned as-is.
func WrapHTTPClient(
	client *http.Client,
	logger *ColabOneOutboundLogger,
	component string,
	proxyLabel string,
) *http.Client {
	if logger == nil {
		return client
	}
	var copied http.Client
	if client != nil {
		copied = *client
	}
	copied.Transport = WrapTransport(copied.Transport, logger, component, proxyLabel)
	return &copied
}

// CloseIdleConnections forwards idle connection cleanup to the underlying transport.
func (t *ColabOneOutboundTransport) CloseIdleConnections() {
	if t == nil || t.Base == nil {
		return
	}
	type idleCloser interface {
		CloseIdleConnections()
	}
	if c, ok := t.Base.(idleCloser); ok {
		c.CloseIdleConnections()
	}
}

// RoundTrip executes req on Base and logs sanitized timing, status, and error details.
func (t *ColabOneOutboundTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t == nil {
		return http.DefaultTransport.RoundTrip(req)
	}
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if t.Logger == nil || req == nil {
		return base.RoundTrip(req)
	}

	started := time.Now()
	resp, err := base.RoundTrip(req)
	elapsedMs := time.Since(started).Milliseconds()
	if elapsedMs < 0 {
		elapsedMs = 0
	}

	protocol := "http"
	if strings.EqualFold(strings.TrimSpace(req.Header.Get("Upgrade")), "websocket") {
		protocol = "ws"
	}
	method := strings.TrimSpace(req.Method)
	if method == "" {
		method = http.MethodGet
	}

	rec := ColabOneOutboundRecord{
		Component:  t.Component,
		Protocol:   protocol,
		Method:     method,
		DurationMs: elapsedMs,
		Proxy:      t.Proxy,
	}
	if req.ContentLength > 0 {
		rec.ReqBytes = req.ContentLength
	}
	if req.URL != nil {
		applySanitizedURL(&rec, req.URL)
	}
	var details []string
	if rng := strings.TrimSpace(req.Header.Get("Range")); rng != "" {
		details = append(details, "range="+truncateRunes(stripControl(rng), 64))
	}
	if resp != nil {
		rec.StatusCode = resp.StatusCode
		if resp.ContentLength > 0 {
			rec.RespBytes = resp.ContentLength
		}
		if resp.StatusCode >= 100 && resp.StatusCode < 400 && err == nil {
			rec.OK = true
		}
		if resp.StatusCode >= 300 {
			if ct := strings.TrimSpace(resp.Header.Get("Content-Type")); ct != "" {
				if semi := strings.IndexByte(ct, ';'); semi >= 0 {
					ct = strings.TrimSpace(ct[:semi])
				}
				if ct != "" {
					details = append(details, "ct="+truncateRunes(stripControl(ct), 48))
				}
			}
			if retryAfter := strings.TrimSpace(resp.Header.Get("Retry-After")); retryAfter != "" {
				details = append(details, "retry_after="+truncateRunes(stripControl(retryAfter), 32))
			}
		}
	}
	if err != nil {
		rec.OK = false
		rec.Error = err.Error()
	}
	if len(details) > 0 {
		rec.Detail = strings.Join(details, " ")
	}

	t.Logger.Record(rec)
	return resp, err
}

// SanitizeURL returns sanitized scheme, host, path, and query components from u.
// Userinfo, fragments, sensitive query parameters, and embedded credentials are stripped.
func SanitizeURL(u *url.URL) (scheme, host, path, query string) {
	if u == nil {
		return "", "", "", ""
	}
	scheme = strings.ToLower(cleanSimpleField(u.Scheme, maxSchemeRunes))
	host = cleanSimpleField(scrubTokens(u.Host), maxHostRunes)
	path = cleanPathField(u.Path)
	query = SanitizeQuery(u.RawQuery)
	return scheme, host, path, query
}

// SanitizeQuery redacts sensitive query parameter values and scrubs credential tokens.
func SanitizeQuery(rawQuery string) string {
	trimmed := strings.TrimSpace(rawQuery)
	if trimmed == "" {
		return ""
	}
	vals, err := url.ParseQuery(trimmed)
	if err != nil {
		return redactedPlaceholder
	}
	if len(vals) == 0 {
		return ""
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		cleanKey := cleanSimpleField(scrubTokens(k), 64)
		if cleanKey == "" {
			continue
		}
		if isSensitiveQueryKey(k) {
			parts = append(parts, cleanKey+"="+redactedPlaceholder)
			continue
		}
		vList := vals[k]
		if len(vList) == 0 {
			parts = append(parts, cleanKey+"=")
			continue
		}
		for _, v := range vList {
			cleanVal := truncateRunes(stripControl(scrubTokens(v)), maxQueryValRunes)
			parts = append(parts, cleanKey+"="+cleanVal)
		}
	}
	return truncateRunes(strings.Join(parts, "&"), maxQueryRunes)
}

func applySanitizedURL(rec *ColabOneOutboundRecord, u *url.URL) {
	if rec == nil || u == nil {
		return
	}
	scheme, host, path, query := SanitizeURL(u)
	if rec.Scheme == "" {
		rec.Scheme = scheme
	}
	if rec.Host == "" {
		rec.Host = host
	}
	if rec.Path == "" {
		rec.Path = path
	}
	if rec.Query == "" {
		rec.Query = query
	}
}

func isSensitiveQueryKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	if lower == "" || lower == "authuser" {
		return false
	}
	if sensitiveQueryExactKeys[lower] {
		return true
	}
	for _, frag := range sensitiveQueryKeyFragments {
		if strings.Contains(lower, frag) {
			return true
		}
	}
	return false
}

func sanitizeOutboundRecord(rec ColabOneOutboundRecord) ColabOneOutboundRecord {
	comp := cleanSimpleField(scrubTokens(rec.Component), maxComponentRunes)
	if comp == "" {
		comp = "outbound"
	}
	method := strings.ToUpper(cleanSimpleField(rec.Method, maxMethodRunes))
	if method == "" {
		method = "REQUEST"
	}
	out := ColabOneOutboundRecord{
		Timestamp:  cleanSimpleField(rec.Timestamp, 40),
		Seq:        rec.Seq,
		Component:  comp,
		Protocol:   strings.ToLower(cleanSimpleField(rec.Protocol, maxProtocolRunes)),
		Method:     method,
		Scheme:     strings.ToLower(cleanSimpleField(rec.Scheme, maxSchemeRunes)),
		Host:       cleanSimpleField(scrubTokens(rec.Host), maxHostRunes),
		Path:       cleanPathField(rec.Path),
		Query:      truncateRunes(stripControl(scrubTokens(rec.Query)), maxQueryRunes),
		StatusCode: rec.StatusCode,
		OK:         rec.OK,
		DurationMs: rec.DurationMs,
		ReqBytes:   rec.ReqBytes,
		RespBytes:  rec.RespBytes,
		Proxy:      cleanSimpleField(scrubTokens(rec.Proxy), maxProxyRunes),
		Endpoint:   cleanSimpleField(scrubTokens(rec.Endpoint), maxEndpointRunes),
	}
	if out.DurationMs < 0 {
		out.DurationMs = 0
	}
	if out.ReqBytes < 0 {
		out.ReqBytes = 0
	}
	if out.RespBytes < 0 {
		out.RespBytes = 0
	}
	if strings.TrimSpace(rec.Detail) != "" {
		out.Detail = truncateRunes(stripControl(activity.SafeMessage(rec.Detail)), maxDetailRunes)
	}
	if strings.TrimSpace(rec.Error) != "" {
		out.Error = truncateRunes(stripControl(activity.SafeMessage(rec.Error)), maxErrorRunes)
	}
	return out
}

func cleanPathField(p string) string {
	cleaned := stripControl(strings.TrimSpace(p))
	if cleaned == "" {
		return ""
	}
	cleaned = scrubTokens(cleaned)
	// Strip any accidentally embedded query or fragment from callers passing raw paths.
	if idx := strings.IndexAny(cleaned, "?#"); idx >= 0 {
		cleaned = cleaned[:idx]
	}
	return truncateRunes(cleaned, maxPathRunes)
}

func cleanSimpleField(s string, maxRunes int) string {
	return truncateRunes(stripControl(strings.TrimSpace(s)), maxRunes)
}

func stripControl(s string) string {
	if s == "" {
		return ""
	}
	s = outboundLineReplacer.Replace(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func scrubTokens(s string) string {
	if s == "" {
		return ""
	}
	out := outboundUserInfoURLRe.ReplaceAllString(s, "${1}"+redactedPlaceholder+"@")
	out = outboundSecretTokenRe.ReplaceAllString(out, redactedPlaceholder)
	return out
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 || s == "" {
		return ""
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	count := 0
	for i := range s {
		if count == maxRunes {
			return s[:i]
		}
		count++
	}
	return s
}

func marshalBoundedJSONLine(rec ColabOneOutboundRecord) []byte {
	encode := func(r ColabOneOutboundRecord) []byte {
		raw, err := json.Marshal(r)
		if err != nil {
			return nil
		}
		return append(raw, '\n')
	}
	line := encode(rec)
	if len(line) <= maxRecordLineBytes {
		return line
	}
	rec.Detail = truncateRunes(rec.Detail, 80)
	rec.Error = truncateRunes(rec.Error, 120)
	rec.Query = truncateRunes(rec.Query, 80)
	rec.Path = truncateRunes(rec.Path, 120)
	line = encode(rec)
	if len(line) <= maxRecordLineBytes {
		return line
	}
	rec.Detail = ""
	rec.Query = ""
	rec.Error = truncateRunes(rec.Error, 60)
	line = encode(rec)
	if len(line) <= maxRecordLineBytes {
		return line
	}
	// Ultimate fallback so line length never exceeds maxRecordLineBytes.
	fallback := fmt.Sprintf(
		`{"ts":%q,"seq":%d,"component":%q,"method":%q,"status_code":%d,"ok":%t,"duration_ms":%d}`+"\n",
		rec.Timestamp, rec.Seq, rec.Component, rec.Method, rec.StatusCode, rec.OK, rec.DurationMs,
	)
	return []byte(fallback)
}
