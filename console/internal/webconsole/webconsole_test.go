package webconsole

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/luolongfei/ColabOne/console/internal/brandicon"
)

type memoryVault struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemoryVault() *memoryVault {
	return &memoryVault{data: make(map[string][]byte)}
}

func (m *memoryVault) GetJSON(name string, dest any) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, ok := m.data[name]
	if !ok {
		return false, nil
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return false, err
	}
	return true, nil
}

func (m *memoryVault) Set(name string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.data[name] = raw
	m.mu.Unlock()
	return nil
}

func (m *memoryVault) Delete(name string) error {
	m.mu.Lock()
	delete(m.data, name)
	m.mu.Unlock()
	return nil
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock {
	return &fakeClock{now: t}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func reserveFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func boolPtr(v bool) *bool    { return &v }
func intPtr(v int) *int       { return &v }
func strPtr(v string) *string { return &v }

func sampleFrontendFS() fs.FS {
	return fstest.MapFS{
		"index.html":        &fstest.MapFile{Data: []byte("<!doctype html><html><body>ColabOne App</body></html>")},
		"assets/app.js":     &fstest.MapFile{Data: []byte("console.log('colabone');")},
		"assets/font.woff2": &fstest.MapFile{Data: []byte("wOF2fakefontdata")},
		".env":              &fstest.MapFile{Data: []byte("SECRET=1")},
		"assets/.hidden":    &fstest.MapFile{Data: []byte("hidden")},
	}
}

func loginForCookie(t *testing.T, h http.Handler, host, passcode string, extraHeaders map[string]string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"passcode": passcode})
	req := httptest.NewRequest(http.MethodPost, "http://"+host+"/_colabone_web/login", bytes.NewReader(body))
	req.Host = host
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(ClientHeaderName, ClientHeaderValue)
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName {
			return c
		}
	}
	t.Fatalf("expected %s cookie in login response", SessionCookieName)
	return nil
}

func TestConfigDefaultsPersistenceAndReload(t *testing.T) {
	dir := t.TempDir()
	vault := newMemoryVault()

	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  dir,
		Vault:      vault,
		AppVersion: "1.2.3",
	})
	defer wc.Close()

	st := wc.State()
	if st.Enabled || st.Running {
		t.Fatalf("expected disabled/stopped by default, got %+v", st)
	}
	if st.BindMode != BindModeLoopback || st.Port != DefaultPort || st.PublicOrigin != "" {
		t.Fatalf("unexpected default config: %+v", st)
	}
	if st.LocalURL != fmt.Sprintf("http://127.0.0.1:%d", DefaultPort) {
		t.Fatalf("unexpected LocalURL: %q", st.LocalURL)
	}
	if st.HasPasscode || st.PasscodePreview != "" {
		t.Fatalf("expected no passcode initially, got HasPasscode=%v preview=%q", st.HasPasscode, st.PasscodePreview)
	}

	port := reserveFreePort(t)
	st, err := wc.Update(ColabOneWebConsoleUpdateRequest{
		Enabled:      boolPtr(true),
		BindMode:     strPtr(BindModeLAN),
		Port:         intPtr(port),
		PublicOrigin: strPtr("Console.Example.COM/"),
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if !st.Enabled || !st.Running || st.BindMode != BindModeLAN || st.Port != port {
		t.Fatalf("unexpected running state: %+v", st)
	}
	if st.PublicOrigin != "https://console.example.com" {
		t.Fatalf("expected normalized PublicOrigin, got %q", st.PublicOrigin)
	}
	if !st.HasPasscode || !strings.Contains(st.PasscodePreview, "••••") {
		t.Fatalf("expected auto-generated masked passcode, got %+v", st)
	}
	wc.Close()

	// Verify persisted web-console.json reloads cleanly on a new instance.
	wc2 := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir: dir,
		Vault:     vault,
	})
	defer wc2.Close()

	st2 := wc2.State()
	if !st2.Enabled || st2.Running {
		t.Fatalf("constructor must load Enabled=true without auto-starting, got %+v", st2)
	}
	if st2.BindMode != BindModeLAN || st2.Port != port || st2.PublicOrigin != "https://console.example.com" {
		t.Fatalf("reloaded config mismatch: %+v", st2)
	}
	if !st2.HasPasscode {
		t.Fatalf("expected persisted passcode detected on reload")
	}

	if err := wc2.StartIfEnabled(); err != nil {
		t.Fatalf("StartIfEnabled failed: %v", err)
	}
	if !wc2.State().Running {
		t.Fatalf("expected running after StartIfEnabled")
	}
}

func TestCorruptConfigFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ConfigFilename), []byte("{invalid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{ConfigDir: dir})
	defer wc.Close()
	st := wc.State()
	if st.Enabled || st.BindMode != BindModeLoopback || st.Port != DefaultPort || st.PublicOrigin != "" {
		t.Fatalf("expected default fallback on corrupt JSON, got %+v", st)
	}
}

func TestStartIfEnabledOccupiedPortRecordsLastError(t *testing.T) {
	dir := t.TempDir()
	vault := newMemoryVault()

	blocker, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	occupiedPort := blocker.Addr().(*net.TCPAddr).Port

	cfgBytes, _ := json.Marshal(ColabOneWebConsoleConfig{
		Enabled:  true,
		BindMode: BindModeLoopback,
		Port:     occupiedPort,
	})
	if err := os.WriteFile(filepath.Join(dir, ConfigFilename), cfgBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	var stateChanges []ColabOneWebConsoleState
	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir: dir,
		Vault:     vault,
		OnStateChange: func(s ColabOneWebConsoleState) {
			stateChanges = append(stateChanges, s)
		},
	})
	defer wc.Close()

	err = wc.StartIfEnabled()
	if err == nil {
		t.Fatalf("expected StartIfEnabled to fail on occupied port")
	}
	if !strings.Contains(err.Error(), "端口已被占用") {
		t.Fatalf("expected occupied-port Chinese error, got %q", err.Error())
	}
	st := wc.State()
	if st.Running || st.LastError == "" {
		t.Fatalf("expected Running=false and LastError populated, got %+v", st)
	}
	if len(stateChanges) == 0 {
		t.Fatalf("expected OnStateChange to fire on startup error")
	}
}

func TestUpdateValidationAndHotApply(t *testing.T) {
	dir := t.TempDir()
	vault := newMemoryVault()
	var logs []string
	var mu sync.Mutex

	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir: dir,
		Vault:     vault,
		OnLog: func(msg, src, level string) {
			mu.Lock()
			logs = append(logs, fmt.Sprintf("%s|%s|%s", src, level, msg))
			mu.Unlock()
		},
	})
	defer wc.Close()

	// Invalid bind_mode
	if _, err := wc.Update(ColabOneWebConsoleUpdateRequest{BindMode: strPtr("wan")}); err == nil {
		t.Fatalf("expected invalid bind_mode error")
	}
	// Invalid ports
	for _, badPort := range []int{0, 80, 1023, 65536, -1} {
		if _, err := wc.Update(ColabOneWebConsoleUpdateRequest{Port: intPtr(badPort)}); err == nil {
			t.Fatalf("expected invalid port %d to fail", badPort)
		}
	}
	// Invalid PublicOrigin values
	invalidOrigins := []string{
		"http://example.com",
		"ftp://example.com",
		"https://user:pass@example.com",
		"https://example.com:8443",
		"https://example.com/sub",
		"https://example.com?foo=bar",
		"https://example.com#frag",
		"https://localhost",
		"https://127.0.0.1",
		"https://[::1]",
		"https://box.local",
		"https://svc.internal",
		"not-a-domain",
		"https://example.com//",
	}
	for _, bad := range invalidOrigins {
		if _, err := wc.Update(ColabOneWebConsoleUpdateRequest{PublicOrigin: strPtr(bad)}); err == nil {
			t.Fatalf("expected invalid PublicOrigin %q to be rejected", bad)
		}
	}
	// Valid PublicOrigin values (including trycloudflare.com)
	validCases := map[string]string{
		"Example.COM":                      "https://example.com",
		"https://Demo.TryCloudflare.com/":  "https://demo.trycloudflare.com",
		"https://sub.console.colabone.dev": "https://sub.console.colabone.dev",
		"":                                 "",
	}
	for in, want := range validCases {
		st, err := wc.Update(ColabOneWebConsoleUpdateRequest{PublicOrigin: strPtr(in)})
		if err != nil {
			t.Fatalf("PublicOrigin %q unexpectedly failed: %v", in, err)
		}
		if st.PublicOrigin != want {
			t.Fatalf("PublicOrigin %q normalized to %q, want %q", in, st.PublicOrigin, want)
		}
	}

	// Enabling on occupied port rolls back Enabled=false
	blocker, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	occupiedPort := blocker.Addr().(*net.TCPAddr).Port

	st, err := wc.Update(ColabOneWebConsoleUpdateRequest{
		Enabled: boolPtr(true),
		Port:    intPtr(occupiedPort),
	})
	if err == nil || !strings.Contains(err.Error(), "端口已被占用") {
		t.Fatalf("expected occupied-port error, got %v", err)
	}
	if st.Enabled || st.Running {
		t.Fatalf("expected rollback on occupied port, got %+v", st)
	}

	// Enable on free port1
	port1 := reserveFreePort(t)
	st, err = wc.Update(ColabOneWebConsoleUpdateRequest{
		Enabled: boolPtr(true),
		Port:    intPtr(port1),
	})
	if err != nil {
		t.Fatalf("enable on port1 failed: %v", err)
	}
	if !st.Enabled || !st.Running || st.ListenAddr != fmt.Sprintf("127.0.0.1:%d", port1) {
		t.Fatalf("unexpected enabled state: %+v", st)
	}

	// Attempt to swap to occupied port -> old listener stays alive and healthy without poisoning LastError!
	st, err = wc.Update(ColabOneWebConsoleUpdateRequest{Port: intPtr(occupiedPort)})
	if err == nil {
		t.Fatalf("expected port swap to occupied port to fail")
	}
	if !st.Running || st.Port != port1 || st.LastError != "" {
		t.Fatalf("expected old listener on port1 to stay alive with empty LastError, got %+v", st)
	}

	// Update only PublicOrigin while running (in-place without dropping listener)
	st, err = wc.Update(ColabOneWebConsoleUpdateRequest{PublicOrigin: strPtr("https://console.example.com")})
	if err != nil {
		t.Fatalf("in-place PublicOrigin update failed: %v", err)
	}
	if !st.Running || st.Port != port1 || st.PublicOrigin != "https://console.example.com" {
		t.Fatalf("unexpected in-place update state: %+v", st)
	}

	// Swap to free port2 and LAN mode
	wc.listInterfaceIPs = func() []netip.Addr {
		return []netip.Addr{netip.MustParseAddr("192.168.1.42"), netip.MustParseAddr("10.0.0.5")}
	}
	port2 := reserveFreePort(t)
	st, err = wc.Update(ColabOneWebConsoleUpdateRequest{
		BindMode: strPtr(BindModeLAN),
		Port:     intPtr(port2),
	})
	if err != nil {
		t.Fatalf("swap to port2/LAN failed: %v", err)
	}
	if !st.Running || st.Port != port2 || st.ListenAddr != fmt.Sprintf("0.0.0.0:%d", port2) {
		t.Fatalf("unexpected swapped state: %+v", st)
	}
	if len(st.LanURLs) != 2 || st.LanURLs[0] != fmt.Sprintf("http://10.0.0.5:%d", port2) || st.LanURLs[1] != fmt.Sprintf("http://192.168.1.42:%d", port2) {
		t.Fatalf("unexpected LanURLs: %v", st.LanURLs)
	}

	// Disable clears sessions and stops listener
	passcode, err := wc.Passcode()
	if err != nil {
		t.Fatal(err)
	}
	_ = loginForCookie(t, wc.Handler(), fmt.Sprintf("127.0.0.1:%d", port2), passcode, nil)
	if wc.State().ActiveSessions != 1 {
		t.Fatalf("expected 1 active session")
	}
	st, err = wc.Update(ColabOneWebConsoleUpdateRequest{Enabled: boolPtr(false)})
	if err != nil {
		t.Fatalf("disable failed: %v", err)
	}
	if st.Enabled || st.Running || st.ActiveSessions != 0 {
		t.Fatalf("expected stopped state with 0 sessions, got %+v", st)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(logs) == 0 {
		t.Fatalf("expected activity logs under %q", webConsoleLogSource)
	}
	for _, entry := range logs {
		if !strings.HasPrefix(entry, webConsoleLogSource+"|") {
			t.Fatalf("unexpected log source: %q", entry)
		}
		if strings.Contains(entry, passcode) {
			t.Fatalf("passcode leaked in log entry: %q", entry)
		}
	}
}

func TestPasscodeGenerationValidationAndSessionRevocation(t *testing.T) {
	vault := newMemoryVault()
	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir: t.TempDir(),
		Vault:     vault,
	})
	defer wc.Close()

	// Passcode() generates and persists even while disabled
	p1, err := wc.Passcode()
	if err != nil {
		t.Fatalf("Passcode() failed: %v", err)
	}
	if !strings.HasPrefix(p1, "co_web_") || len(p1) != len("co_web_")+32 {
		t.Fatalf("unexpected generated passcode format: %q (len=%d)", p1, len(p1))
	}
	st := wc.State()
	if !st.HasPasscode || st.PasscodePreview != p1[:4]+"••••"+p1[len(p1)-4:] {
		t.Fatalf("unexpected passcode preview: %+v", st)
	}

	// Invalid custom passcodes
	for _, bad := range []string{"short77", " leading123", "trailing123 ", "has\nnewline1", "has\x00nul123", strings.Repeat("a", 129)} {
		if _, err := wc.SetPasscode(bad); err == nil {
			t.Fatalf("expected invalid custom passcode %q to fail", bad)
		}
	}

	// Short valid 8-char passcode uses full masked preview
	st, err = wc.SetPasscode("12345678")
	if err != nil {
		t.Fatalf("SetPasscode 8-char failed: %v", err)
	}
	if st.PasscodePreview != "••••••••" {
		t.Fatalf("expected full mask for <10 rune passcode, got %q", st.PasscodePreview)
	}

	// Active session and ticket are revoked when passcode changes
	port := reserveFreePort(t)
	if _, err := wc.Update(ColabOneWebConsoleUpdateRequest{Enabled: boolPtr(true), Port: intPtr(port)}); err != nil {
		t.Fatal(err)
	}
	cookie := loginForCookie(t, wc.Handler(), fmt.Sprintf("127.0.0.1:%d", port), "12345678", nil)
	ticket, err := wc.CreateLoginTicket(false)
	if err != nil {
		t.Fatal(err)
	}

	// Empty SetPasscode generates a fresh random passcode and revokes sessions/tickets
	st, err = wc.SetPasscode("   ")
	if err != nil {
		t.Fatalf("SetPasscode regenerate failed: %v", err)
	}
	if st.ActiveSessions != 0 {
		t.Fatalf("expected sessions revoked after SetPasscode, got %d", st.ActiveSessions)
	}
	p2, err := wc.Passcode()
	if err != nil || p2 == "12345678" || !strings.HasPrefix(p2, "co_web_") {
		t.Fatalf("expected new random passcode, got %q err=%v", p2, err)
	}

	// Old session cookie must now be rejected
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/web/capabilities", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected revoked cookie to return 401, got %d", rec.Code)
	}

	// Old ticket must now be rejected
	u, _ := url.Parse(ticket.LocalURL)
	tReq := httptest.NewRequest(http.MethodGet, u.RequestURI(), nil)
	tReq.Host = u.Host
	tRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(tRec, tReq)
	if tRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected revoked ticket to return 401, got %d", tRec.Code)
	}

	// RevokeSessions clears active sessions
	c2 := loginForCookie(t, wc.Handler(), fmt.Sprintf("127.0.0.1:%d", port), p2, nil)
	if wc.State().ActiveSessions != 1 {
		t.Fatalf("expected 1 active session before revoke")
	}
	st = wc.RevokeSessions()
	if st.ActiveSessions != 0 {
		t.Fatalf("expected 0 active sessions after RevokeSessions")
	}
	req2 := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/web/capabilities", nil)
	req2.AddCookie(c2)
	rec2 := httptest.NewRecorder()
	wc.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after RevokeSessions, got %d", rec2.Code)
	}
}

func TestHostHeaderValidation(t *testing.T) {
	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir: t.TempDir(),
		Vault:     newMemoryVault(),
	})
	defer wc.Close()
	wc.listInterfaceIPs = func() []netip.Addr {
		return []netip.Addr{netip.MustParseAddr("192.168.50.10")}
	}

	// Malformed Host rejected with 400
	for _, badHost := range []string{"", "bad host", "evil.com/path", "evil.com:99999", "[::1"} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/_colabone_web/login", nil)
		req.Host = badHost
		rec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("host %q expected 400, got %d", badHost, rec.Code)
		}
	}

	// When PublicOrigin is empty in loopback mode: ONLY loopback hosts are permitted (DNS rebinding protection).
	for _, h := range []string{"127.0.0.1:3232", "localhost:3232", "[::1]:3232"} {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/_colabone_web/login", nil)
		r.Host = h
		w := httptest.NewRecorder()
		wc.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("expected loopback host %q allowed when PublicOrigin empty, got %d", h, w.Code)
		}
	}
	for _, blocked := range []string{"random-tunnel.trycloudflare.com", "evil.example.com", "192.168.50.10:3232"} {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/_colabone_web/login", nil)
		r.Host = blocked
		w := httptest.NewRecorder()
		wc.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected non-loopback host %q blocked (403) in loopback mode when PublicOrigin empty, got %d", blocked, w.Code)
		}
	}

	// When PublicOrigin is empty in LAN mode: loopback + active local interface IPs allowed; arbitrary hostnames blocked.
	if _, err := wc.Update(ColabOneWebConsoleUpdateRequest{BindMode: strPtr(BindModeLAN)}); err != nil {
		t.Fatal(err)
	}
	for _, allowed := range []string{"127.0.0.1:3232", "localhost:3232", "[::1]:3232", "192.168.50.10:3232"} {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/_colabone_web/login", nil)
		r.Host = allowed
		w := httptest.NewRecorder()
		wc.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("expected host %q allowed in LAN mode with empty PublicOrigin, got %d", allowed, w.Code)
		}
	}
	for _, blocked := range []string{"random-tunnel.trycloudflare.com", "evil.example.com", "192.168.50.99:3232"} {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/_colabone_web/login", nil)
		r.Host = blocked
		w := httptest.NewRecorder()
		wc.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected host %q blocked (403) in LAN mode with empty PublicOrigin, got %d", blocked, w.Code)
		}
	}

	// When PublicOrigin is configured in loopback mode:
	if _, err := wc.Update(ColabOneWebConsoleUpdateRequest{
		BindMode:     strPtr(BindModeLoopback),
		PublicOrigin: strPtr("https://console.example.com"),
	}); err != nil {
		t.Fatal(err)
	}

	allowedLoopback := []string{"127.0.0.1:3232", "localhost:3232", "[::1]:3232", "console.example.com", "CONSOLE.EXAMPLE.COM:443"}
	for _, h := range allowedLoopback {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/_colabone_web/login", nil)
		r.Host = h
		w := httptest.NewRecorder()
		wc.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("expected host %q allowed, got %d", h, w.Code)
		}
	}
	for _, blocked := range []string{"evil.example.com", "192.168.50.10:3232"} {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/_colabone_web/login", nil)
		r.Host = blocked
		w := httptest.NewRecorder()
		wc.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected host %q blocked (403) in loopback mode, got %d", blocked, w.Code)
		}
	}

	// In LAN mode, local interface IP is also allowed while unknown hosts remain blocked
	if _, err := wc.Update(ColabOneWebConsoleUpdateRequest{BindMode: strPtr(BindModeLAN)}); err != nil {
		t.Fatal(err)
	}
	lanReq := httptest.NewRequest(http.MethodGet, "http://192.168.50.10:3232/_colabone_web/login", nil)
	lanReq.Host = "192.168.50.10:3232"
	lanRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(lanRec, lanReq)
	if lanRec.Code != http.StatusOK {
		t.Fatalf("expected LAN interface IP allowed in LAN mode, got %d", lanRec.Code)
	}
	otherReq := httptest.NewRequest(http.MethodGet, "http://192.168.50.99:3232/_colabone_web/login", nil)
	otherReq.Host = "192.168.50.99:3232"
	otherRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(otherRec, otherReq)
	if otherRec.Code != http.StatusForbidden {
		t.Fatalf("expected non-local IP rejected in LAN mode, got %d", otherRec.Code)
	}
}

func TestLoginPageRateLimitingCookiesTicketsAndLogout(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC))
	vault := newMemoryVault()
	locale := "zh-CN"

	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  t.TempDir(),
		Vault:      vault,
		Now:        clk.Now,
		GetLocale:  func() string { return locale },
		FrontendFS: sampleFrontendFS,
	})
	defer wc.Close()

	port := reserveFreePort(t)
	if _, err := wc.Update(ColabOneWebConsoleUpdateRequest{
		Enabled:      boolPtr(true),
		Port:         intPtr(port),
		PublicOrigin: strPtr("https://console.example.com"),
	}); err != nil {
		t.Fatal(err)
	}
	passcode, err := wc.Passcode()
	if err != nil {
		t.Fatal(err)
	}

	// 1. Login page headers and localized content
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/_colabone_web/login", nil)
	rec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login page expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Security-Policy") == "" ||
		rec.Header().Get("Cache-Control") != "no-store" ||
		rec.Header().Get("X-Frame-Options") != "DENY" ||
		rec.Header().Get("X-Content-Type-Options") != "nosniff" ||
		rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("missing required security headers on login page: %v", rec.Header())
	}
	if !strings.Contains(rec.Body.String(), "ColabOne") || !strings.Contains(rec.Body.String(), "X-ColabOne-Client") {
		t.Fatalf("login page missing expected brand/fetch client header")
	}

	locale = "en-US"
	enRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(enRec, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/_colabone_web/login", nil))
	if !strings.Contains(enRec.Body.String(), "Unlock ColabOne Console") {
		t.Fatalf("expected en-US localized login page")
	}

	// Verify Accept-Language precedence (zh-Hant-HK/zh-MO/yue -> zh-HK before zh-Hant -> zh-TW) and formal HK wording
	if got := localeFromAcceptLanguage("zh-Hant-HK,zh-TW;q=0.9,en;q=0.8"); got != "zh-HK" {
		t.Fatalf("expected zh-Hant-HK -> zh-HK, got %q", got)
	}
	if got := localeFromAcceptLanguage("zh-MO,en;q=0.8"); got != "zh-HK" {
		t.Fatalf("expected zh-MO -> zh-HK, got %q", got)
	}
	if got := localeFromAcceptLanguage("yue-Hant-HK,zh;q=0.8"); got != "zh-HK" {
		t.Fatalf("expected yue-Hant-HK -> zh-HK, got %q", got)
	}
	if got := localeFromAcceptLanguage("zh-Hant-TW,en;q=0.8"); got != "zh-TW" {
		t.Fatalf("expected zh-Hant-TW -> zh-TW, got %q", got)
	}
	hkCopy := colabOneWebLoginCopyByLocale["zh-HK"]
	if strings.Contains(hkCopy.description, "嘅") {
		t.Fatalf("zh-HK login copy must use formal written Chinese, got %q", hkCopy.description)
	}

	// Login POST without anti-CSRF client header must be rejected with 403
	noHeaderBody, _ := json.Marshal(map[string]string{"passcode": passcode})
	noHeaderReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/_colabone_web/login", bytes.NewReader(noHeaderBody))
	noHeaderRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(noHeaderRec, noHeaderReq)
	if noHeaderRec.Code != http.StatusForbidden {
		t.Fatalf("login POST without %s expected 403, got %d", ClientHeaderName, noHeaderRec.Code)
	}

	// 2. Per-IP login rate limiter (5 failures in 5 min -> 429) with localized errors
	attempt := func(remoteAddr, cfIP, pw string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]string{"passcode": pw})
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/_colabone_web/login", bytes.NewReader(b))
		r.RemoteAddr = remoteAddr
		r.Header.Set(ClientHeaderName, ClientHeaderValue)
		if cfIP != "" {
			r.Header.Set("CF-Connecting-IP", cfIP)
		}
		w := httptest.NewRecorder()
		wc.Handler().ServeHTTP(w, r)
		return w
	}

	// Loopback remoteAddr honors CF-Connecting-IP and returns en-US error copy
	for i := 0; i < 5; i++ {
		w := attempt("127.0.0.1:1234", "203.0.113.10", "wrong-passcode")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d expected 401, got %d", i+1, w.Code)
		}
		if !strings.Contains(w.Body.String(), "Incorrect access passcode") {
			t.Fatalf("expected en-US invalid passcode error, got %q", w.Body.String())
		}
	}
	blocked := attempt("127.0.0.1:1234", "203.0.113.10", passcode)
	if blocked.Code != http.StatusTooManyRequests || blocked.Header().Get("Retry-After") == "" {
		t.Fatalf("expected 429 with Retry-After after 5 failures, got %d header=%v", blocked.Code, blocked.Header())
	}
	if !strings.Contains(blocked.Body.String(), "Too many attempts") {
		t.Fatalf("expected en-US rate-limit error, got %q", blocked.Body.String())
	}

	// Different CF-Connecting-IP from loopback is NOT blocked
	otherCF := attempt("127.0.0.1:1234", "203.0.113.11", passcode)
	if otherCF.Code != http.StatusOK {
		t.Fatalf("different CF-Connecting-IP should succeed, got %d", otherCF.Code)
	}

	// Non-loopback remoteAddr ignores spoofed CF-Connecting-IP
	for i := 0; i < 5; i++ {
		w := attempt("198.51.100.7:4444", fmt.Sprintf("198.51.100.%d", i+20), "wrong")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("non-loopback attempt %d expected 401, got %d", i+1, w.Code)
		}
	}
	spoofed := attempt("198.51.100.7:4444", "198.51.100.99", passcode)
	if spoofed.Code != http.StatusTooManyRequests {
		t.Fatalf("expected spoofed CF-Connecting-IP on non-loopback to still be rate-limited, got %d", spoofed.Code)
	}

	// Advance past 5-minute window: rate limit resets and login succeeds with Secure cookie under HTTPS proxy
	clk.Advance(5*time.Minute + time.Second)
	httpsCookie := loginForCookie(t, wc.Handler(), "console.example.com", passcode, map[string]string{
		"X-Forwarded-Proto": "https",
		"CF-Connecting-IP":  "203.0.113.10",
	})
	if !httpsCookie.HttpOnly || httpsCookie.SameSite != http.SameSiteLaxMode || !httpsCookie.Secure || httpsCookie.MaxAge != int(SessionTTL.Seconds()) {
		t.Fatalf("unexpected HTTPS session cookie flags: %+v", httpsCookie)
	}

	// CF-Visitor HTTPS detection also sets Secure=true
	cfCookie := loginForCookie(t, wc.Handler(), "console.example.com", passcode, map[string]string{
		"CF-Visitor": `{"scheme":"https"}`,
	})
	if !cfCookie.Secure {
		t.Fatalf("expected Secure cookie when CF-Visitor indicates https")
	}

	// Plain HTTP loopback sets Secure=false
	httpCookie := loginForCookie(t, wc.Handler(), fmt.Sprintf("127.0.0.1:%d", port), passcode, nil)
	if httpCookie.Secure {
		t.Fatalf("expected Secure=false on plain HTTP loopback")
	}

	// Already-authenticated GET /_colabone_web/login redirects 302 to /
	authedLoginReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/_colabone_web/login", nil)
	authedLoginReq.AddCookie(httpCookie)
	authedLoginRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(authedLoginRec, authedLoginReq)
	if authedLoginRec.Code != http.StatusFound || authedLoginRec.Header().Get("Location") != "/" {
		t.Fatalf("authenticated GET /_colabone_web/login expected 302 to /, got %d loc=%q", authedLoginRec.Code, authedLoginRec.Header().Get("Location"))
	}

	// 3. Login tickets: reusable within 60s (default), optional single-use, preview-bot immunity, and expiration
	reusableTicket, err := wc.CreateLoginTicket(false)
	if err != nil {
		t.Fatalf("CreateLoginTicket(false) failed: %v", err)
	}
	if reusableTicket.SingleUse || !reusableTicket.UsesPublicHost || !strings.HasPrefix(reusableTicket.URL, "https://console.example.com/_colabone_web/redeem?ticket=") {
		t.Fatalf("unexpected reusable ticket URL: %+v", reusableTicket)
	}
	if reusableTicket.TTLSeconds != int(LoginTicketTTL.Seconds()) {
		t.Fatalf("unexpected TTLSeconds: %d", reusableTicket.TTLSeconds)
	}

	ru, _ := url.Parse(reusableTicket.URL)
	reusableReq1 := httptest.NewRequest(http.MethodGet, ru.RequestURI(), nil)
	reusableReq1.Host = ru.Host
	reusableReq1.Header.Set("X-Forwarded-Proto", "https")
	reusableRec1 := httptest.NewRecorder()
	wc.Handler().ServeHTTP(reusableRec1, reusableReq1)
	if reusableRec1.Code != http.StatusFound || reusableRec1.Header().Get("Location") != "/" {
		t.Fatalf("reusable redeem #1 expected 302 to /, got %d location=%q", reusableRec1.Code, reusableRec1.Header().Get("Location"))
	}
	if reusableRec1.Header().Get("Cache-Control") != "no-store" || len(reusableRec1.Result().Cookies()) == 0 {
		t.Fatalf("reusable redeem #1 missing no-store or session cookie")
	}
	// A second unauthenticated device/browser can redeem the same reusable link within 60s
	reusableRec2 := httptest.NewRecorder()
	wc.Handler().ServeHTTP(reusableRec2, reusableReq1)
	if reusableRec2.Code != http.StatusFound || len(reusableRec2.Result().Cookies()) == 0 {
		t.Fatalf("reusable redeem #2 expected 302 with session cookie, got %d", reusableRec2.Code)
	}
	// An already-authenticated browser tapping the link again redirects cleanly without minting a duplicate session
	sessionsBefore := wc.State().ActiveSessions
	authedRedeemReq := httptest.NewRequest(http.MethodGet, ru.RequestURI(), nil)
	authedRedeemReq.Host = ru.Host
	authedRedeemReq.AddCookie(reusableRec1.Result().Cookies()[0])
	authedRedeemRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(authedRedeemRec, authedRedeemReq)
	if authedRedeemRec.Code != http.StatusFound || wc.State().ActiveSessions != sessionsBefore {
		t.Fatalf("already-authenticated redeem should redirect without adding session: code=%d before=%d after=%d", authedRedeemRec.Code, sessionsBefore, wc.State().ActiveSessions)
	}

	// Single-use ticket: preview crawlers (TelegramBot / prefetch) must NOT consume it or mint sessions
	singleTicket, err := wc.CreateLoginTicket(true)
	if err != nil {
		t.Fatalf("CreateLoginTicket(true) failed: %v", err)
	}
	if !singleTicket.SingleUse {
		t.Fatalf("expected SingleUse=true")
	}
	u, _ := url.Parse(singleTicket.URL)
	sessionsBeforeBot := wc.State().ActiveSessions
	botReq := httptest.NewRequest(http.MethodGet, u.RequestURI(), nil)
	botReq.Host = u.Host
	botReq.Header.Set("User-Agent", "TelegramBot (like TwitterBot)")
	botRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(botRec, botReq)
	if botRec.Code != http.StatusOK || len(botRec.Result().Cookies()) != 0 || wc.State().ActiveSessions != sessionsBeforeBot {
		t.Fatalf("TelegramBot preview should return 200 without cookie/session: code=%d cookies=%d", botRec.Code, len(botRec.Result().Cookies()))
	}

	prefetchReq := httptest.NewRequest(http.MethodGet, u.RequestURI(), nil)
	prefetchReq.Host = u.Host
	prefetchReq.Header.Set("Sec-Purpose", "prefetch")
	prefetchRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(prefetchRec, prefetchReq)
	if prefetchRec.Code != http.StatusOK || len(prefetchRec.Result().Cookies()) != 0 {
		t.Fatalf("prefetch request should not consume single-use ticket: code=%d", prefetchRec.Code)
	}

	// First real browser redemption succeeds after bots/prefetch
	redeemReq := httptest.NewRequest(http.MethodGet, u.RequestURI(), nil)
	redeemReq.Host = u.Host
	redeemReq.Header.Set("X-Forwarded-Proto", "https")
	redeemRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(redeemRec, redeemReq)
	if redeemRec.Code != http.StatusFound || redeemRec.Header().Get("Location") != "/" {
		t.Fatalf("single-use redeem expected 302 to /, got %d location=%q", redeemRec.Code, redeemRec.Header().Get("Location"))
	}
	if len(redeemRec.Result().Cookies()) == 0 {
		t.Fatalf("single-use redeem expected session cookie")
	}

	// Reusing the exact same single-use ticket must fail with 401 + expired notice
	reuseRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(reuseRec, redeemReq)
	if reuseRec.Code != http.StatusUnauthorized || !strings.Contains(reuseRec.Body.String(), "expired") {
		t.Fatalf("reused single-use ticket expected 401 with expired notice, got %d", reuseRec.Code)
	}

	// Reusable ticket also expires cleanly after 60s
	ticket2, err := wc.CreateLoginTicket(false)
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(LoginTicketTTL + time.Second)
	u2, _ := url.Parse(ticket2.LocalURL)
	expReq := httptest.NewRequest(http.MethodGet, u2.RequestURI(), nil)
	expReq.Host = u2.Host
	expRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(expRec, expReq)
	if expRec.Code != http.StatusUnauthorized {
		t.Fatalf("expired reusable ticket expected 401, got %d", expRec.Code)
	}

	// 4. Logout clears session and expires cookie (with CSRF check)
	badLogout := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/_colabone_web/logout", nil)
	badLogout.AddCookie(httpCookie)
	badLogoutRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(badLogoutRec, badLogout)
	if badLogoutRec.Code != http.StatusForbidden {
		t.Fatalf("logout without client header expected 403, got %d", badLogoutRec.Code)
	}

	goodLogout := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/_colabone_web/logout", nil)
	goodLogout.Header.Set(ClientHeaderName, ClientHeaderValue)
	goodLogout.AddCookie(httpCookie)
	goodLogoutRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(goodLogoutRec, goodLogout)
	if goodLogoutRec.Code != http.StatusOK {
		t.Fatalf("logout expected 200, got %d", goodLogoutRec.Code)
	}
	cleared := false
	for _, c := range goodLogoutRec.Result().Cookies() {
		if c.Name == SessionCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatalf("expected logout to expire %s cookie", SessionCookieName)
	}

	// 5. Idle session expires after SessionTTL (400 days without renewal)
	clk.Advance(SessionTTL + time.Second)
	expSessReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/web/capabilities", nil)
	expSessReq.AddCookie(httpsCookie)
	expSessRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(expSessRec, expSessReq)
	if expSessRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected idle session expired after SessionTTL (401), got %d", expSessRec.Code)
	}
}

func TestPersistedSessionsSurviveRestartSlidingRenewalAndRevocation(t *testing.T) {
	dir := t.TempDir()
	vault := newMemoryVault()
	clk := newFakeClock(time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC))

	newInstance := func() *ColabOneWebConsole {
		return NewColabOneWebConsole(ColabOneWebConsoleOptions{
			ConfigDir: dir,
			Vault:     vault,
			Now:       clk.Now,
			CallBridge: func(ctx context.Context, method string, payload map[string]any) (string, error) {
				return `{"ok":true}`, nil
			},
		})
	}

	wc1 := newInstance()
	port := reserveFreePort(t)
	if _, err := wc1.Update(ColabOneWebConsoleUpdateRequest{
		Enabled: boolPtr(true),
		Port:    intPtr(port),
	}); err != nil {
		t.Fatalf("enable wc1 failed: %v", err)
	}
	passcode, err := wc1.Passcode()
	if err != nil {
		t.Fatal(err)
	}

	// Session 1 via passcode login (including trimmed whitespace around pasted passcode)
	cookie1 := loginForCookie(t, wc1.Handler(), fmt.Sprintf("127.0.0.1:%d", port), "  "+passcode+"\n", nil)
	if cookie1.MaxAge != int(SessionTTL.Seconds()) {
		t.Fatalf("expected long-lived cookie MaxAge=%d, got %d", int(SessionTTL.Seconds()), cookie1.MaxAge)
	}

	// Session 2 via quick login ticket
	ticket, err := wc1.CreateLoginTicket(false)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(ticket.LocalURL)
	redeemReq := httptest.NewRequest(http.MethodGet, u.RequestURI(), nil)
	redeemReq.Host = u.Host
	redeemRec := httptest.NewRecorder()
	wc1.Handler().ServeHTTP(redeemRec, redeemReq)
	if redeemRec.Code != http.StatusFound || len(redeemRec.Result().Cookies()) == 0 {
		t.Fatalf("redeem expected 302 with session cookie, got %d", redeemRec.Code)
	}
	cookie2 := redeemRec.Result().Cookies()[0]

	if got := wc1.State().ActiveSessions; got != 2 {
		t.Fatalf("expected 2 active sessions before restart, got %d", got)
	}

	// Simulate app shutdown and restart: Close() must NOT wipe persisted sessions
	wc1.Close()

	wc2 := newInstance()
	defer wc2.Close()
	if got := wc2.State().ActiveSessions; got != 2 {
		t.Fatalf("expected 2 persisted sessions restored on restart, got %d", got)
	}
	if err := wc2.StartIfEnabled(); err != nil {
		t.Fatalf("StartIfEnabled after restart failed: %v", err)
	}

	// Both cookies authenticate immediately after restart without re-login
	for idx, c := range []*http.Cookie{cookie1, cookie2} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/snapshot", nil)
		req.AddCookie(c)
		rec := httptest.NewRecorder()
		wc2.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("cookie%d expected 200 after restart, got %d", idx+1, rec.Code)
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatalf("cookie%d should not emit redundant Set-Cookie within renewal interval", idx+1)
		}
	}

	// Sliding renewal: advance 200 days, use cookie1 -> renews cookie + persisted expiry
	clk.Advance(200 * 24 * time.Hour)
	renewReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/web/capabilities", nil)
	renewReq.AddCookie(cookie1)
	renewRec := httptest.NewRecorder()
	wc2.Handler().ServeHTTP(renewRec, renewReq)
	if renewRec.Code != http.StatusOK {
		t.Fatalf("cookie1 after 200d expected 200, got %d", renewRec.Code)
	}
	var refreshedCookie *http.Cookie
	for _, c := range renewRec.Result().Cookies() {
		if c.Name == SessionCookieName {
			refreshedCookie = c
		}
	}
	if refreshedCookie == nil || refreshedCookie.Value != cookie1.Value || refreshedCookie.MaxAge != int(SessionTTL.Seconds()) {
		t.Fatalf("expected refreshed session cookie after 200d, got %+v", refreshedCookie)
	}

	// Advance another 250 days (450d total > 400d SessionTTL) and restart again:
	// cookie1 stayed active so it survives; idle cookie2 expires.
	clk.Advance(250 * 24 * time.Hour)
	wc2.Close()

	wc3 := newInstance()
	defer wc3.Close()
	if got := wc3.State().ActiveSessions; got != 1 {
		t.Fatalf("expected only renewed cookie1 to survive 450d across restart, got %d", got)
	}
	req1 := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/web/capabilities", nil)
	req1.AddCookie(cookie1)
	rec1 := httptest.NewRecorder()
	wc3.Handler().ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("renewed cookie1 expected 200 at 450d after restart, got %d", rec1.Code)
	}
	req2 := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/web/capabilities", nil)
	req2.AddCookie(cookie2)
	rec2 := httptest.NewRecorder()
	wc3.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("idle cookie2 expected 401 at 450d, got %d", rec2.Code)
	}

	// Explicit logout removes session from vault across restarts
	logoutReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/_colabone_web/logout", nil)
	logoutReq.Header.Set(ClientHeaderName, ClientHeaderValue)
	logoutReq.AddCookie(cookie1)
	logoutRec := httptest.NewRecorder()
	wc3.Handler().ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout expected 200, got %d", logoutRec.Code)
	}
	wc3.Close()

	wc4 := newInstance()
	defer wc4.Close()
	if got := wc4.State().ActiveSessions; got != 0 {
		t.Fatalf("expected 0 sessions after logout + restart, got %d", got)
	}

	// RevokeSessions clears persisted sessions across restarts
	cRevoke := loginForCookie(t, wc4.Handler(), fmt.Sprintf("127.0.0.1:%d", port), passcode, nil)
	wc4.RevokeSessions()
	wc4.Close()

	wc5 := newInstance()
	defer wc5.Close()
	if wc5.State().ActiveSessions != 0 {
		t.Fatalf("expected 0 sessions after RevokeSessions + restart")
	}
	rRevoked := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/web/capabilities", nil)
	rRevoked.AddCookie(cRevoke)
	wRevoked := httptest.NewRecorder()
	wc5.Handler().ServeHTTP(wRevoked, rRevoked)
	if wRevoked.Code != http.StatusUnauthorized {
		t.Fatalf("expected revoked session to remain 401 after restart, got %d", wRevoked.Code)
	}

	// Out-of-band passcode replacement in vault invalidates previously persisted sessions
	_ = loginForCookie(t, wc5.Handler(), fmt.Sprintf("127.0.0.1:%d", port), passcode, nil)
	// Calling persistSessions() after Close() must NEVER wipe VaultSessionsKey
	wc5.Close()
	wc5.persistSessions()
	wc5Check := newInstance()
	if wc5Check.State().ActiveSessions != 1 {
		wc5Check.Close()
		t.Fatalf("expected post-Close persistSessions() to preserve vault sessions, got %d", wc5Check.State().ActiveSessions)
	}
	// Explicitly disabling Web Console clears persisted sessions across restarts
	if _, err := wc5Check.Update(ColabOneWebConsoleUpdateRequest{Enabled: boolPtr(false)}); err != nil {
		wc5Check.Close()
		t.Fatalf("disable wc5Check failed: %v", err)
	}
	wc5Check.Close()
	wcDisabled := newInstance()
	if wcDisabled.State().ActiveSessions != 0 {
		wcDisabled.Close()
		t.Fatalf("expected 0 sessions after disable + restart, got %d", wcDisabled.State().ActiveSessions)
	}
	// Re-enable, create a session, replace passcode out-of-band, and confirm rejection
	if _, err := wcDisabled.Update(ColabOneWebConsoleUpdateRequest{Enabled: boolPtr(true), Port: intPtr(port)}); err != nil {
		wcDisabled.Close()
		t.Fatalf("re-enable failed: %v", err)
	}
	_ = loginForCookie(t, wcDisabled.Handler(), fmt.Sprintf("127.0.0.1:%d", port), passcode, nil)
	wcDisabled.Close()
	if err := vault.Set(VaultPasscodeKey, "brand_new_out_of_band_passcode"); err != nil {
		t.Fatal(err)
	}
	wc6 := newInstance()
	defer wc6.Close()
	if wc6.State().ActiveSessions != 0 {
		t.Fatalf("expected mismatched passcode fingerprint to reject stale persisted sessions, got %d", wc6.State().ActiveSessions)
	}
}

func TestCSRFProtectionAndAPIEndpoints(t *testing.T) {
	largeSnapshot := `{"status":"ready","padding":"` + strings.Repeat("colabone-", 300) + `"}`
	var bridgeCalls []string
	var bridgeMu sync.Mutex

	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  t.TempDir(),
		Vault:      newMemoryVault(),
		AppVersion: "0.9.5",
		CallBridge: func(ctx context.Context, method string, payload map[string]any) (string, error) {
			bridgeMu.Lock()
			bridgeCalls = append(bridgeCalls, method)
			bridgeMu.Unlock()
			switch method {
			case "snapshot":
				return largeSnapshot, nil
			case "models.list":
				return `{"models":["qwen"]}`, nil
			case "slow.op":
				return "", context.DeadlineExceeded
			case "bad.op":
				return "", errors.New("boom")
			default:
				return `{"ok":true}`, nil
			}
		},
	})
	defer wc.Close()

	passcode, err := wc.Passcode()
	if err != nil {
		t.Fatal(err)
	}
	cookie := loginForCookie(t, wc.Handler(), "127.0.0.1:3232", passcode, nil)

	// Unauthenticated API endpoints return 401
	for _, ep := range []struct{ method, path string }{
		{http.MethodGet, "/api/web/capabilities"},
		{http.MethodGet, "/api/snapshot"},
		{http.MethodPost, "/api/command"},
		{http.MethodGet, "/api/outputs/0123456789abcdef0123456789abcdef/download"},
	} {
		req := httptest.NewRequest(ep.method, "http://127.0.0.1:3232"+ep.path, nil)
		rec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s %s expected 401, got %d", ep.method, ep.path, rec.Code)
		}
		if rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("must never emit Access-Control-Allow-Origin")
		}
	}

	// Capabilities payload
	capReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3232/api/web/capabilities", nil)
	capReq.AddCookie(cookie)
	capRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(capRec, capReq)
	if capRec.Code != http.StatusOK {
		t.Fatalf("capabilities expected 200, got %d", capRec.Code)
	}
	var caps ColabOneWebCapabilities
	if err := json.Unmarshal(capRec.Body.Bytes(), &caps); err != nil {
		t.Fatal(err)
	}
	if caps.Runtime != "web" || caps.Version != "0.9.5" || !caps.CanDownloadOutput ||
		caps.CanOpenNativeCanvasWindow || caps.CanStandaloneLogWindow || caps.CanManageWebConsole ||
		caps.CanManageCloseBehavior || caps.CanManageShortcuts || caps.CanSelfUpdate ||
		caps.CanMigrate || caps.CanRevealHostSecrets || caps.CanLoopbackOAuth {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}

	// Snapshot: ETag + gzip + 304 Not Modified
	snapReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3232/api/snapshot", nil)
	snapReq.AddCookie(cookie)
	snapReq.Header.Set("Accept-Encoding", "gzip, deflate")
	snapRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(snapRec, snapReq)
	if snapRec.Code != http.StatusOK {
		t.Fatalf("snapshot expected 200, got %d", snapRec.Code)
	}
	etag := snapRec.Header().Get("ETag")
	if !strings.HasPrefix(etag, `"sha256-`) {
		t.Fatalf("expected sha256 ETag, got %q", etag)
	}
	if snapRec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected gzip-compressed large snapshot")
	}
	gzr, err := gzip.NewReader(bytes.NewReader(snapRec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	unzipped, err := io.ReadAll(gzr)
	_ = gzr.Close()
	if err != nil || string(unzipped) != largeSnapshot {
		t.Fatalf("gzipped snapshot mismatch: err=%v len=%d", err, len(unzipped))
	}

	// If-None-Match returns 304
	snap304Req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3232/api/snapshot", nil)
	snap304Req.AddCookie(cookie)
	snap304Req.Header.Set("If-None-Match", etag)
	snap304Rec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(snap304Rec, snap304Req)
	if snap304Rec.Code != http.StatusNotModified || snap304Rec.Body.Len() != 0 {
		t.Fatalf("expected 304 empty body, got %d len=%d", snap304Rec.Code, snap304Rec.Body.Len())
	}

	// CSRF checks on /api/command
	if _, err := wc.Update(ColabOneWebConsoleUpdateRequest{
		PublicOrigin: strPtr("https://console.example.com"),
	}); err != nil {
		t.Fatal(err)
	}
	cmdPost := func(host, body string, headers map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "http://"+host+"/api/command", strings.NewReader(body))
		r.Host = host
		r.RemoteAddr = "127.0.0.1:54321"
		r.AddCookie(cookie)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		wc.Handler().ServeHTTP(w, r)
		return w
	}

	// Missing X-ColabOne-Client -> 403
	if w := cmdPost("127.0.0.1:3232", `{"action":"models.list"}`, nil); w.Code != http.StatusForbidden {
		t.Fatalf("missing client header expected 403, got %d", w.Code)
	}
	// Mismatched Origin -> 403
	if w := cmdPost("127.0.0.1:3232", `{"action":"models.list"}`, map[string]string{
		ClientHeaderName: ClientHeaderValue,
		"Origin":         "https://evil.example.com",
	}); w.Code != http.StatusForbidden {
		t.Fatalf("mismatched Origin expected 403, got %d", w.Code)
	}
	// Sec-Fetch-Site: cross-site -> 403
	if w := cmdPost("127.0.0.1:3232", `{"action":"models.list"}`, map[string]string{
		ClientHeaderName: ClientHeaderValue,
		"Sec-Fetch-Site": "cross-site",
	}); w.Code != http.StatusForbidden {
		t.Fatalf("cross-site Sec-Fetch-Site expected 403, got %d", w.Code)
	}
	// Mismatched scheme Origin (http Origin on https forwarded request) -> 403
	if w := cmdPost("console.example.com", `{"action":"models.list"}`, map[string]string{
		ClientHeaderName:    ClientHeaderValue,
		"Origin":            "http://console.example.com",
		"X-Forwarded-Proto": "https",
	}); w.Code != http.StatusForbidden {
		t.Fatalf("scheme-mismatched Origin expected 403, got %d", w.Code)
	}
	// Default HTTPS port normalization: Origin https://console.example.com matches Host console.example.com:443
	if w := cmdPost("console.example.com:443", `{"action":"models.list"}`, map[string]string{
		ClientHeaderName:    ClientHeaderValue,
		"Origin":            "https://console.example.com",
		"Sec-Fetch-Site":    "same-origin",
		"X-Forwarded-Proto": "https",
	}); w.Code != http.StatusOK || w.Body.String() != `{"models":["qwen"]}` {
		t.Fatalf("normalized HTTPS Origin expected 200, got %d body=%q", w.Code, w.Body.String())
	}

	// Command blocklist enforcement (none of these should hit CallBridge)
	bridgeMu.Lock()
	callsBeforeBlocklist := len(bridgeCalls)
	bridgeMu.Unlock()

	blockedCases := map[string]string{
		"desktop.shortcuts":           "该桌面专属操作仅支持在宿主机客户端执行",
		"api.tunnel_token":            "为保护本机凭据安全，网页控制台禁止读取明文密钥或代理配置",
		"comfyui.canvas.tunnel_token": "为保护本机凭据安全，网页控制台禁止读取明文密钥或代理配置",
		"settings.hf_token":           "为保护本机凭据安全，网页控制台禁止读取明文密钥或代理配置",
		"settings.assign_proxy":       "为保护本机凭据安全，网页控制台禁止读取明文密钥或代理配置",
		"settings.assign_proxy.test":  "为保护本机凭据安全，网页控制台禁止读取明文密钥或代理配置",
		"auth.colab.auto":             "网页控制台不支持本机回环自动登录，请使用手动授权登录",
	}
	for action, wantMsg := range blockedCases {
		w := cmdPost("127.0.0.1:3232", fmt.Sprintf(`{"action":%q}`, action), map[string]string{
			ClientHeaderName: ClientHeaderValue,
		})
		if w.Code != http.StatusForbidden {
			t.Fatalf("blocked action %q expected 403, got %d", action, w.Code)
		}
		var errBody map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &errBody); err != nil || errBody["error"] != wantMsg {
			t.Fatalf("blocked action %q expected error %q, got %q", action, wantMsg, w.Body.String())
		}
	}

	bridgeMu.Lock()
	if len(bridgeCalls) != callsBeforeBlocklist {
		t.Fatalf("blocked commands must never invoke CallBridge")
	}
	bridgeMu.Unlock()

	// Bridge errors map to 400 and timeouts to 504
	if w := cmdPost("127.0.0.1:3232", `{"action":"bad.op"}`, map[string]string{ClientHeaderName: ClientHeaderValue}); w.Code != http.StatusBadRequest {
		t.Fatalf("bridge error expected 400, got %d", w.Code)
	}
	if w := cmdPost("127.0.0.1:3232", `{"action":"slow.op"}`, map[string]string{ClientHeaderName: ClientHeaderValue}); w.Code != http.StatusGatewayTimeout {
		t.Fatalf("timeout expected 504, got %d", w.Code)
	}
}

func TestOutputDownloadChunkingAndIntegrity(t *testing.T) {
	validID := "0123456789abcdef0123456789abcdef"
	payload := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("pixeldata!"), 200)...)
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])

	mode := "ok"
	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir: t.TempDir(),
		Vault:     newMemoryVault(),
		CallBridge: func(ctx context.Context, method string, params map[string]any) (string, error) {
			if method != "comfyui.output.asset" {
				t.Fatalf("unexpected method %q", method)
			}
			offset := int(params["offset"].(int64))
			chunkSize := 512
			end := min(offset+chunkSize, len(payload))
			part := payload[offset:end]

			chunk := colabOneWebMediaChunk{
				Data:      base64.StdEncoding.EncodeToString(part),
				Size:      int64(len(payload)),
				Offset:    int64(offset),
				SHA256:    digest,
				Extension: "png",
			}
			switch mode {
			case "bad_ext":
				chunk.Extension = "svg"
			case "bad_hash":
				chunk.SHA256 = strings.Repeat("0", 64)
			case "drive_empty_hash":
				chunk.Source = "drive"
				chunk.SHA256 = ""
			case "tamper_offset":
				if offset > 0 {
					chunk.Offset = int64(offset + 1)
				}
			}
			b, _ := json.Marshal(chunk)
			return string(b), nil
		},
	})
	defer wc.Close()

	passcode, _ := wc.Passcode()
	cookie := loginForCookie(t, wc.Handler(), "127.0.0.1:3232", passcode, nil)

	doDownload := func(id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3232/api/outputs/"+id+"/download", nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		wc.Handler().ServeHTTP(w, r)
		return w
	}

	// Invalid IDs
	for _, badID := range []string{"short", "ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ", "0123456789abcdef0123456789abcdef/extra"} {
		if w := doDownload(badID); w.Code != http.StatusBadRequest {
			t.Fatalf("bad id %q expected 400, got %d", badID, w.Code)
		}
	}

	// HEAD preflight returns validated headers with empty body (enables zero-RAM browser streaming)
	headReq := httptest.NewRequest(http.MethodHead, "http://127.0.0.1:3232/api/outputs/"+validID+"/download", nil)
	headReq.AddCookie(cookie)
	headRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(headRec, headReq)
	if headRec.Code != http.StatusOK {
		t.Fatalf("HEAD download expected 200, got %d", headRec.Code)
	}
	if headRec.Body.Len() != 0 {
		t.Fatalf("HEAD download expected empty body, got %d bytes", headRec.Body.Len())
	}
	if headRec.Header().Get("Content-Disposition") != `attachment; filename="ColabOne-01234567.png"` ||
		headRec.Header().Get("Content-Type") != "image/png" ||
		headRec.Header().Get("Content-Length") != fmt.Sprintf("%d", len(payload)) {
		t.Fatalf("unexpected HEAD download headers: %v", headRec.Header())
	}

	// HEAD invalid ID returns 400 with percent-encoded X-ColabOne-Error and empty body
	badHeadReq := httptest.NewRequest(http.MethodHead, "http://127.0.0.1:3232/api/outputs/short/download", nil)
	badHeadReq.AddCookie(cookie)
	badHeadRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(badHeadRec, badHeadReq)
	if badHeadRec.Code != http.StatusBadRequest || badHeadRec.Body.Len() != 0 {
		t.Fatalf("bad HEAD expected 400 empty body, got %d len=%d", badHeadRec.Code, badHeadRec.Body.Len())
	}
	decodedHeadErr, decErr := url.PathUnescape(badHeadRec.Header().Get("X-ColabOne-Error"))
	if decErr != nil || decodedHeadErr != "输出文件 ID 无效" {
		t.Fatalf("unexpected X-ColabOne-Error header: %q (decoded %q, err %v)", badHeadRec.Header().Get("X-ColabOne-Error"), decodedHeadErr, decErr)
	}

	// Multi-chunk happy path
	w := doDownload(validID)
	if w.Code != http.StatusOK {
		t.Fatalf("valid download expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("unexpected Content-Type: %q", w.Header().Get("Content-Type"))
	}
	wantDisp := `attachment; filename="ColabOne-01234567.png"`
	if w.Header().Get("Content-Disposition") != wantDisp {
		t.Fatalf("unexpected Content-Disposition: %q, want %q", w.Header().Get("Content-Disposition"), wantDisp)
	}
	if !bytes.Equal(w.Body.Bytes(), payload) {
		t.Fatalf("downloaded bytes mismatch: got %d want %d", w.Body.Len(), len(payload))
	}

	// Drive export with empty SHA-256 allowed
	mode = "drive_empty_hash"
	w = doDownload(validID)
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), payload) {
		t.Fatalf("drive empty hash expected 200, got %d", w.Code)
	}

	// Disallowed extension -> 502
	mode = "bad_ext"
	if w = doDownload(validID); w.Code != http.StatusBadGateway {
		t.Fatalf("bad extension expected 502, got %d", w.Code)
	}

	// Hash mismatch -> 502
	mode = "bad_hash"
	if w = doDownload(validID); w.Code != http.StatusBadGateway {
		t.Fatalf("hash mismatch expected 502, got %d", w.Code)
	}

	// Offset tamper mid-stream -> 502
	mode = "tamper_offset"
	if w = doDownload(validID); w.Code != http.StatusBadGateway {
		t.Fatalf("offset tamper expected 502, got %d", w.Code)
	}
}

func TestOutputMediaStreamingEndpointAuthAndRange(t *testing.T) {
	validID := "0123456789abcdef0123456789abcdef"
	payload := []byte("WEBCONSOLE-VIDEO-STREAM-PAYLOAD-0123456789")

	var gotRange string
	var gotMethod string
	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  t.TempDir(),
		Vault:      newMemoryVault(),
		FrontendFS: sampleFrontendFS,
		ServeOutputMedia: func(ctx context.Context, rw http.ResponseWriter, r *http.Request, id string) {
			gotRange = r.Header.Get("Range")
			gotMethod = r.Method
			rw.Header().Set("Content-Type", "video/mp4")
			rw.Header().Set("Accept-Ranges", "bytes")
			if r.Method == http.MethodHead {
				rw.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
				rw.WriteHeader(http.StatusOK)
				return
			}
			rw.Header().Set("Content-Range", fmt.Sprintf("bytes 0-15/%d", len(payload)))
			rw.Header().Set("Content-Length", "16")
			rw.WriteHeader(http.StatusPartialContent)
			_, _ = rw.Write(payload[:16])
		},
	})
	defer wc.Close()

	// Unauthenticated GET/HEAD must be rejected with 401.
	unauthReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3232/api/outputs/"+validID+"/media", nil)
	unauthRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated media expected 401, got %d", unauthRec.Code)
	}

	passcode, _ := wc.Passcode()
	cookie := loginForCookie(t, wc.Handler(), "127.0.0.1:3232", passcode, nil)

	// Invalid ID -> 400
	badReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3232/api/outputs/bad-id/media", nil)
	badReq.AddCookie(cookie)
	badRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("invalid media id expected 400, got %d", badRec.Code)
	}

	// Authenticated HEAD
	headReq := httptest.NewRequest(http.MethodHead, "http://127.0.0.1:3232/api/outputs/"+validID+"/media", nil)
	headReq.AddCookie(cookie)
	headRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(headRec, headReq)
	if headRec.Code != http.StatusOK || gotMethod != http.MethodHead || headRec.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("HEAD media failed: code=%d method=%q headers=%#v", headRec.Code, gotMethod, headRec.Header())
	}

	// Authenticated Range GET -> 206 Partial Content
	getReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3232/api/outputs/"+validID+"/media", nil)
	getReq.Header.Set("Range", "bytes=0-15")
	getReq.AddCookie(cookie)
	getRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusPartialContent || gotRange != "bytes=0-15" || getRec.Body.String() != string(payload[:16]) {
		t.Fatalf("GET Range media failed: code=%d range=%q body=%q", getRec.Code, gotRange, getRec.Body.String())
	}
}

func TestFrontendStaticServingSPADotfilesAndAuthRedirect(t *testing.T) {
	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  t.TempDir(),
		Vault:      newMemoryVault(),
		FrontendFS: sampleFrontendFS,
	})
	defer wc.Close()

	// Unauthenticated HTML / extensionless routes redirect to login
	for _, p := range []string{"/", "/settings", "/workflows/run"} {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3232"+p, nil)
		w := httptest.NewRecorder()
		wc.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusFound || w.Header().Get("Location") != "/_colabone_web/login" {
			t.Fatalf("unauthenticated %s expected 302 to login, got %d loc=%q", p, w.Code, w.Header().Get("Location"))
		}
	}

	// Unauthenticated asset fetch returns 401
	assetReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3232/assets/app.js", nil)
	assetReq.Header.Set("Accept", "*/*")
	assetRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(assetRec, assetReq)
	if assetRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated asset expected 401, got %d", assetRec.Code)
	}

	passcode, _ := wc.Passcode()
	cookie := loginForCookie(t, wc.Handler(), "127.0.0.1:3232", passcode, nil)

	getAuthed := func(p string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3232"+p, nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		wc.Handler().ServeHTTP(w, r)
		return w
	}

	// Index served with no-cache
	idx := getAuthed("/")
	if idx.Code != http.StatusOK || idx.Header().Get("Cache-Control") != "no-cache" || !strings.Contains(idx.Body.String(), "ColabOne App") {
		t.Fatalf("unexpected index response: %d cc=%q body=%q", idx.Code, idx.Header().Get("Cache-Control"), idx.Body.String())
	}

	// SPA fallback for extensionless missing route
	spa := getAuthed("/workflows/123")
	if spa.Code != http.StatusOK || !strings.Contains(spa.Body.String(), "ColabOne App") || spa.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("SPA fallback failed: %d cc=%q", spa.Code, spa.Header().Get("Cache-Control"))
	}

	// Font woff2 served with proper MIME and immutable cache
	font := getAuthed("/assets/font.woff2")
	if font.Code != http.StatusOK || font.Header().Get("Content-Type") != "font/woff2" || !strings.Contains(font.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("unexpected font response: %d ct=%q cc=%q", font.Code, font.Header().Get("Content-Type"), font.Header().Get("Cache-Control"))
	}

	// Missing file with extension returns 404 (not SPA fallback)
	if missing := getAuthed("/assets/missing.js"); missing.Code != http.StatusNotFound {
		t.Fatalf("missing asset expected 404, got %d", missing.Code)
	}

	// Dotfiles and traversal paths are blocked
	for _, unsafePath := range []string{"/.env", "/assets/.hidden", "/../secret", "/assets/%2e%2e/index.html"} {
		if w := getAuthed(unsafePath); w.Code != http.StatusNotFound {
			t.Fatalf("unsafe path %q expected 404, got %d", unsafePath, w.Code)
		}
	}

	// Nil FrontendFS returns friendly 503
	wcNil := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir: t.TempDir(),
		Vault:     newMemoryVault(),
	})
	defer wcNil.Close()
	pNil, _ := wcNil.Passcode()
	cNil := loginForCookie(t, wcNil.Handler(), "127.0.0.1:3232", pNil, nil)
	rNil := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3232/", nil)
	rNil.AddCookie(cNil)
	wNil := httptest.NewRecorder()
	wcNil.Handler().ServeHTTP(wNil, rNil)
	if wNil.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil FrontendFS expected 503, got %d", wNil.Code)
	}
}

func TestLiveHTTPServerEndToEnd(t *testing.T) {
	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  t.TempDir(),
		Vault:      newMemoryVault(),
		AppVersion: "2.0.0",
		FrontendFS: sampleFrontendFS,
		CallBridge: func(ctx context.Context, method string, payload map[string]any) (string, error) {
			return `{"status":"ok"}`, nil
		},
	})
	defer wc.Close()

	port := reserveFreePort(t)
	st, err := wc.Update(ColabOneWebConsoleUpdateRequest{
		Enabled: boolPtr(true),
		Port:    intPtr(port),
	})
	if err != nil {
		t.Fatalf("enable live server failed: %v", err)
	}

	ticket, err := wc.CreateLoginTicket(false)
	if err != nil {
		t.Fatalf("CreateLoginTicket failed: %v", err)
	}

	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Get(ticket.LocalURL)
	if err != nil {
		t.Fatalf("GET redeem ticket failed: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("live redeem expected 302, got %d", resp.StatusCode)
	}
	var sessionCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == SessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatalf("missing session cookie from live redeem")
	}

	// Fetch snapshot over live TCP socket with the issued cookie
	snapReq, _ := http.NewRequest(http.MethodGet, st.LocalURL+"/api/snapshot", nil)
	snapReq.AddCookie(sessionCookie)
	snapResp, err := client.Do(snapReq)
	if err != nil {
		t.Fatalf("GET /api/snapshot failed: %v", err)
	}
	snapBody, _ := io.ReadAll(snapResp.Body)
	_ = snapResp.Body.Close()
	if snapResp.StatusCode != http.StatusOK || string(snapBody) != `{"status":"ok"}` {
		t.Fatalf("unexpected live snapshot response: %d %q", snapResp.StatusCode, string(snapBody))
	}

	// If IPv6 loopback is active on this host, verify [::1]:<port> is also reachable.
	if probe6, probeErr := net.DialTimeout("tcp6", fmt.Sprintf("[::1]:%d", port), 500*time.Millisecond); probeErr == nil {
		_ = probe6.Close()
		v6Req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://[::1]:%d/api/snapshot", port), nil)
		v6Req.AddCookie(sessionCookie)
		v6Resp, v6Err := client.Do(v6Req)
		if v6Err != nil {
			t.Fatalf("IPv6 loopback GET /api/snapshot failed: %v", v6Err)
		}
		_, _ = io.Copy(io.Discard, v6Resp.Body)
		_ = v6Resp.Body.Close()
		if v6Resp.StatusCode != http.StatusOK {
			t.Fatalf("IPv6 loopback expected 200, got %d", v6Resp.StatusCode)
		}
	}
}

func TestSamePortBindModeSwapAndBoundsAndConcurrency(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 11, 25, 14, 0, 0, 0, time.UTC))
	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir: t.TempDir(),
		Vault:     newMemoryVault(),
		Now:       clk.Now,
		CallBridge: func(ctx context.Context, method string, payload map[string]any) (string, error) {
			return `{"ok":true}`, nil
		},
	})
	defer wc.Close()

	// CreateLoginTicket requires running server
	if _, err := wc.CreateLoginTicket(false); err == nil {
		t.Fatalf("expected CreateLoginTicket to fail when server is stopped")
	}

	port := reserveFreePort(t)
	st, err := wc.Update(ColabOneWebConsoleUpdateRequest{
		Enabled:  boolPtr(true),
		BindMode: strPtr(BindModeLoopback),
		Port:     intPtr(port),
	})
	if err != nil || !st.Running || st.ListenAddr != fmt.Sprintf("127.0.0.1:%d", port) {
		t.Fatalf("expected loopback running, got %+v err=%v", st, err)
	}

	// Ticket without PublicOrigin uses LocalURL
	tk, err := wc.CreateLoginTicket(false)
	if err != nil || tk.UsesPublicHost || tk.URL != tk.LocalURL {
		t.Fatalf("expected local ticket URL, got %+v err=%v", tk, err)
	}

	// Swap bind_mode on the SAME port: loopback -> lan -> loopback
	st, err = wc.Update(ColabOneWebConsoleUpdateRequest{BindMode: strPtr(BindModeLAN)})
	if err != nil || !st.Running || st.ListenAddr != fmt.Sprintf("0.0.0.0:%d", port) {
		t.Fatalf("expected same-port swap to LAN, got %+v err=%v", st, err)
	}
	st, err = wc.Update(ColabOneWebConsoleUpdateRequest{BindMode: strPtr(BindModeLoopback)})
	if err != nil || !st.Running || st.ListenAddr != fmt.Sprintf("127.0.0.1:%d", port) {
		t.Fatalf("expected same-port swap back to loopback, got %+v err=%v", st, err)
	}

	// Bounded session cap (max 64) evicts oldest
	passcode, err := wc.Passcode()
	if err != nil {
		t.Fatal(err)
	}
	var firstCookie *http.Cookie
	for i := 0; i < maxActiveSessions+5; i++ {
		clk.Advance(time.Second)
		c := loginForCookie(t, wc.Handler(), fmt.Sprintf("127.0.0.1:%d", port), passcode, nil)
		if i == 0 {
			firstCookie = c
		}
	}
	if got := wc.State().ActiveSessions; got != maxActiveSessions {
		t.Fatalf("expected active sessions capped at %d, got %d", maxActiveSessions, got)
	}
	reqOld := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/web/capabilities", nil)
	reqOld.AddCookie(firstCookie)
	recOld := httptest.NewRecorder()
	wc.Handler().ServeHTTP(recOld, reqOld)
	if recOld.Code != http.StatusUnauthorized {
		t.Fatalf("expected oldest evicted session to return 401, got %d", recOld.Code)
	}

	// Bounded pending ticket cap (max 32) evicts oldest
	var firstTicket ColabOneWebConsoleTicket
	for i := 0; i < maxPendingTickets+4; i++ {
		clk.Advance(time.Millisecond)
		cur, err := wc.CreateLoginTicket(false)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			firstTicket = cur
		}
	}
	uOld, _ := url.Parse(firstTicket.LocalURL)
	reqOldTk := httptest.NewRequest(http.MethodGet, uOld.RequestURI(), nil)
	reqOldTk.Host = uOld.Host
	recOldTk := httptest.NewRecorder()
	wc.Handler().ServeHTTP(recOldTk, reqOldTk)
	if recOldTk.Code != http.StatusUnauthorized {
		t.Fatalf("expected evicted oldest ticket to return 401, got %d", recOldTk.Code)
	}

	// Concurrent requests + state reads + ticket creation stay deadlock-free
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				_ = wc.State()
				_, _ = wc.CreateLoginTicket(false)
				rec := httptest.NewRecorder()
				wc.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/_colabone_web/login", nil))
			}
		}()
	}
	wg.Wait()
}

func TestProxyHeaderSpoofingGlobalRateLimitAndAtomicity(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 11, 25, 16, 0, 0, 0, time.UTC))
	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir: t.TempDir(),
		Vault:     newMemoryVault(),
		Now:       clk.Now,
	})
	defer wc.Close()
	wc.listInterfaceIPs = func() []netip.Addr {
		return []netip.Addr{netip.MustParseAddr("192.168.50.10")}
	}
	if _, err := wc.Update(ColabOneWebConsoleUpdateRequest{BindMode: strPtr(BindModeLAN)}); err != nil {
		t.Fatal(err)
	}
	passcode, err := wc.Passcode()
	if err != nil {
		t.Fatal(err)
	}

	// 1. Non-loopback RemoteAddr must ignore X-Forwarded-Proto and CF-Visitor (Secure stays false).
	nonLoopbackLogin := func(hdrs map[string]string) *http.Cookie {
		b, _ := json.Marshal(map[string]string{"passcode": passcode})
		r := httptest.NewRequest(http.MethodPost, "http://192.168.50.10:3232/_colabone_web/login", bytes.NewReader(b))
		r.Host = "192.168.50.10:3232"
		r.RemoteAddr = "192.168.50.99:4444"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(ClientHeaderName, ClientHeaderValue)
		for k, v := range hdrs {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		wc.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK || len(w.Result().Cookies()) == 0 {
			t.Fatalf("expected 200 login, got %d body=%s", w.Code, w.Body.String())
		}
		return w.Result().Cookies()[0]
	}
	if c := nonLoopbackLogin(map[string]string{"X-Forwarded-Proto": "https"}); c.Secure {
		t.Fatal("non-loopback RemoteAddr must ignore spoofed X-Forwarded-Proto")
	}
	if c := nonLoopbackLogin(map[string]string{"CF-Visitor": `{"scheme":"https"}`}); c.Secure {
		t.Fatal("non-loopback RemoteAddr must ignore spoofed CF-Visitor")
	}

	// 2. Loopback RemoteAddr with PublicOrigin == "" must ignore CF-Visitor and CF-Connecting-IP.
	cNoPub := loginForCookie(t, wc.Handler(), "127.0.0.1:3232", passcode, map[string]string{
		"CF-Visitor": `{"scheme":"https"}`,
	})
	if cNoPub.Secure {
		t.Fatal("CF-Visitor must be ignored when PublicOrigin is empty")
	}
	for i := 0; i < loginRateLimitMaxFailures; i++ {
		b, _ := json.Marshal(map[string]string{"passcode": "wrong"})
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:3232/_colabone_web/login", bytes.NewReader(b))
		r.RemoteAddr = "127.0.0.1:54321"
		r.Header.Set(ClientHeaderName, ClientHeaderValue)
		r.Header.Set("CF-Connecting-IP", fmt.Sprintf("203.0.113.%d", i+1))
		w := httptest.NewRecorder()
		wc.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d expected 401, got %d", i+1, w.Code)
		}
	}
	// Rotating CF-Connecting-IP without PublicOrigin must STILL be throttled on 127.0.0.1.
	{
		b, _ := json.Marshal(map[string]string{"passcode": passcode})
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:3232/_colabone_web/login", bytes.NewReader(b))
		r.RemoteAddr = "127.0.0.1:54321"
		r.Header.Set(ClientHeaderName, ClientHeaderValue)
		r.Header.Set("CF-Connecting-IP", "203.0.113.200")
		w := httptest.NewRecorder()
		wc.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("expected 429 when CF-Connecting-IP rotated without PublicOrigin, got %d", w.Code)
		}
	}

	// 3. Global sliding failure cap (25 failures / 5m across distinct IPs) blocks attempt 26.
	clk.Advance(loginRateLimitWindow + time.Second)
	if _, err := wc.Update(ColabOneWebConsoleUpdateRequest{
		PublicOrigin: strPtr("https://console.example.com"),
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < loginRateLimitGlobalMaxFailures; i++ {
		b, _ := json.Marshal(map[string]string{"passcode": "wrong"})
		r := httptest.NewRequest(http.MethodPost, "http://console.example.com/_colabone_web/login", bytes.NewReader(b))
		r.Host = "console.example.com"
		r.RemoteAddr = "127.0.0.1:54321"
		r.Header.Set(ClientHeaderName, ClientHeaderValue)
		r.Header.Set("CF-Connecting-IP", fmt.Sprintf("198.51.100.%d", i+1))
		w := httptest.NewRecorder()
		wc.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("global failure %d expected 401, got %d", i+1, w.Code)
		}
	}
	{
		b, _ := json.Marshal(map[string]string{"passcode": passcode})
		r := httptest.NewRequest(http.MethodPost, "http://console.example.com/_colabone_web/login", bytes.NewReader(b))
		r.Host = "console.example.com"
		r.RemoteAddr = "127.0.0.1:54321"
		r.Header.Set(ClientHeaderName, ClientHeaderValue)
		r.Header.Set("CF-Connecting-IP", "198.51.100.250")
		w := httptest.NewRecorder()
		wc.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("expected 429 after %d global failures across rotating IPs, got %d", loginRateLimitGlobalMaxFailures, w.Code)
		}
	}

	// 4. Concurrent login burst from the same IP is atomic: at most loginRateLimitMaxFailures get 401.
	clk.Advance(loginRateLimitWindow + time.Second)
	const burst = 16
	codes := make(chan int, burst)
	var wg sync.WaitGroup
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, _ := json.Marshal(map[string]string{"passcode": "wrong-concurrent"})
			r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:3232/_colabone_web/login", bytes.NewReader(b))
			r.RemoteAddr = "127.0.0.1:54321"
			r.Header.Set(ClientHeaderName, ClientHeaderValue)
			r.Header.Set("CF-Connecting-IP", "203.0.113.77")
			w := httptest.NewRecorder()
			wc.Handler().ServeHTTP(w, r)
			codes <- w.Code
		}()
	}
	wg.Wait()
	close(codes)
	unauthCount, limitCount := 0, 0
	for code := range codes {
		switch code {
		case http.StatusUnauthorized:
			unauthCount++
		case http.StatusTooManyRequests:
			limitCount++
		default:
			t.Fatalf("unexpected concurrent login status %d", code)
		}
	}
	if unauthCount != loginRateLimitMaxFailures || limitCount != burst-loginRateLimitMaxFailures {
		t.Fatalf("atomic rate limit broken: 401=%d 429=%d (want %d and %d)", unauthCount, limitCount, loginRateLimitMaxFailures, burst-loginRateLimitMaxFailures)
	}
}

func TestCommandBodyLimitsAndMediaUploadExemption(t *testing.T) {
	if maxCommandBodyBytes != 64<<20 {
		t.Fatalf("maxCommandBodyBytes = %d, want 64 MiB", maxCommandBodyBytes)
	}
	if maxMediaUploadCommandBodyBytes != 240<<20 {
		t.Fatalf("maxMediaUploadCommandBodyBytes = %d, want 240 MiB", maxMediaUploadCommandBodyBytes)
	}

	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir: t.TempDir(),
		Vault:     newMemoryVault(),
		CallBridge: func(ctx context.Context, method string, payload map[string]any) (string, error) {
			return fmt.Sprintf(`{"action":%q}`, method), nil
		},
	})
	defer wc.Close()

	// Use compact limits for fast, deterministic boundary checks while preserving exact production logic.
	wc.maxCommandBodyBytes = 8 << 10            // 8 KiB normal limit
	wc.maxMediaUploadCommandBodyBytes = 32 << 10 // 32 KiB media upload limit

	passcode, err := wc.Passcode()
	if err != nil {
		t.Fatal(err)
	}
	cookie := loginForCookie(t, wc.Handler(), "127.0.0.1:3232", passcode, nil)

	postCmd := func(rawBody string, chunked bool) *httptest.ResponseRecorder {
		var body io.Reader = strings.NewReader(rawBody)
		if chunked {
			body = io.NopCloser(strings.NewReader(rawBody))
		}
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:3232/api/command", body)
		if chunked {
			r.ContentLength = -1
		}
		r.RemoteAddr = "127.0.0.1:54321"
		r.AddCookie(cookie)
		r.Header.Set(ClientHeaderName, ClientHeaderValue)
		w := httptest.NewRecorder()
		wc.Handler().ServeHTTP(w, r)
		return w
	}

	// Normal command exceeding normal limit -> 413 (both Content-Length and chunked).
	normalTooLarge := `{"action":"models.list","payload":{"blob":"` + strings.Repeat("a", 12<<10) + `"}}`
	if w := postCmd(normalTooLarge, false); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("normal command > limit (Content-Length) expected 413, got %d", w.Code)
	}
	if w := postCmd(normalTooLarge, true); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("normal command > limit (chunked) expected 413, got %d", w.Code)
	}

	// All 4 allowed media upload commands are permitted between normalLimit and mediaLimit.
	for _, action := range []string{
		"comfyui.input.upload",
		"comfyui.draft.media.save",
		"comfyui.live.avatars.clip.upload",
		"comfyui.live.driving.upload",
	} {
		mediaOK := fmt.Sprintf(`{"action":%q,"payload":{"data":%q}}`, action, strings.Repeat("b", 20<<10))
		if w := postCmd(mediaOK, false); w.Code != http.StatusOK {
			t.Fatalf("media upload %q expected 200, got %d body=%s", action, w.Code, w.Body.String())
		}
		if w := postCmd(mediaOK, true); w.Code != http.StatusOK {
			t.Fatalf("chunked media upload %q expected 200, got %d body=%s", action, w.Code, w.Body.String())
		}
	}

	// Media upload exceeding mediaLimit -> 413.
	mediaTooLarge := `{"action":"comfyui.input.upload","payload":{"data":"` + strings.Repeat("c", 36<<10) + `"}}`
	if w := postCmd(mediaTooLarge, true); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("media upload > mediaLimit expected 413, got %d", w.Code)
	}

	// Duplicate-action JSON bypass attempt: leading media action followed by non-media action -> 413!
	dupBypass := `{"action":"comfyui.input.upload","payload":{"blob":"` + strings.Repeat("d", 16<<10) + `"},"action":"models.list"}`
	if w := postCmd(dupBypass, true); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("duplicate-action body limit bypass expected 413, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestWebConsolePostCloseOperationsAndCallbackSuppression(t *testing.T) {
	var closedFlag bool
	var callbackMu sync.Mutex
	callbacksAfterClose := 0

	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir: t.TempDir(),
		Vault:     newMemoryVault(),
		OnStateChange: func(ColabOneWebConsoleState) {
			callbackMu.Lock()
			if closedFlag {
				callbacksAfterClose++
			}
			callbackMu.Unlock()
		},
	})

	port := reserveFreePort(t)
	if _, err := wc.Update(ColabOneWebConsoleUpdateRequest{
		Enabled: boolPtr(true),
		Port:    intPtr(port),
	}); err != nil {
		t.Fatalf("Update enable failed: %v", err)
	}

	callbackMu.Lock()
	closedFlag = true
	callbackMu.Unlock()
	wc.Close()

	if err := wc.StartIfEnabled(); err != nil {
		t.Fatalf("StartIfEnabled after Close expected nil no-op, got %v", err)
	}
	if _, err := wc.Update(ColabOneWebConsoleUpdateRequest{Enabled: boolPtr(true)}); err == nil {
		t.Fatal("expected Update after Close to return error")
	}
	if _, err := wc.SetPasscode("new-passcode-123"); err == nil {
		t.Fatal("expected SetPasscode after Close to return error")
	}
	if _, err := wc.CreateLoginTicket(false); err == nil {
		t.Fatal("expected CreateLoginTicket after Close to return error")
	}
	_ = wc.RevokeSessions()
	wc.emitStateChange(wc.State())

	callbackMu.Lock()
	gotAfter := callbacksAfterClose
	callbackMu.Unlock()
	if gotAfter != 0 {
		t.Fatalf("expected 0 OnStateChange callbacks after Close(), got %d", gotAfter)
	}
}

func TestWebConsoleBrandLogoAndFaviconRoutes(t *testing.T) {
	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  t.TempDir(),
		Vault:      newMemoryVault(),
		FrontendFS: sampleFrontendFS,
	})
	defer wc.Close()

	// 1. Login page embeds the real ColabOne software logo for both favicon and brand logo.
	loginReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3232/_colabone_web/login", nil)
	loginRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login page expected 200, got %d", loginRec.Code)
	}
	body := loginRec.Body.String()
	wantURI := brandicon.DataURI()
	if !strings.Contains(body, `<link rel="icon" type="image/svg+xml" href="`+wantURI+`">`) {
		t.Fatalf("login page favicon link does not embed ColabOne brandicon data URI")
	}
	if !strings.Contains(body, `<img class="co-brand-logo" src="`+wantURI+`" alt="ColabOne" width="32" height="32">`) {
		t.Fatalf("login page brand logo img does not embed ColabOne brandicon data URI")
	}
	if !strings.Contains(body, `<link rel="alternate icon" type="image/x-icon" href="/favicon.ico">`) {
		t.Fatalf("login page missing /favicon.ico alternate icon link")
	}
	if !strings.Contains(body, `<link rel="apple-touch-icon" sizes="180x180" href="/apple-touch-icon.png">`) {
		t.Fatalf("login page missing /apple-touch-icon.png link")
	}
	if strings.Contains(body, brandicon.FallbackDataURI()) {
		t.Fatalf("login page must not render the placeholder fallback icon")
	}
	if csp := loginRec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "img-src 'self' data:;") {
		t.Fatalf("login page CSP missing img-src 'self' data:; got %q", csp)
	}

	// Verify decoded data URI matches the trimmed ColabOne portrait SVG.
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(wantURI, "data:image/svg+xml;base64,"))
	if err != nil {
		t.Fatalf("decode brandicon data URI: %v", err)
	}
	if !strings.Contains(string(decoded), "<title>ColabOne</title>") || !strings.Contains(string(decoded), `viewBox="120 120 1014 1014"`) {
		t.Fatalf("decoded login page icon does not look like trimmed ColabOne icon.svg")
	}

	// 2. Public favicon/icon routes work for GET and HEAD before login and match embedded assets.
	assetCases := []struct {
		path        string
		contentType string
		wantBytes   []byte
	}{
		{"/favicon.ico", "image/x-icon", brandicon.FaviconICOBytes()},
		{"/apple-touch-icon.png", "image/png", brandicon.AppleTouchIconPNGBytes()},
		{"/apple-touch-icon-precomposed.png", "image/png", brandicon.AppleTouchIconPNGBytes()},
		{"/_colabone_web/icon.svg", "image/svg+xml", brandicon.SVGBytes()},
	}
	for _, tc := range assetCases {
		getReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3232"+tc.path, nil)
		getRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(getRec, getReq)
		if getRec.Code != http.StatusOK {
			t.Fatalf("GET %s expected 200, got %d", tc.path, getRec.Code)
		}
		if gotCT := getRec.Header().Get("Content-Type"); gotCT != tc.contentType {
			t.Fatalf("GET %s Content-Type = %q, want %q", tc.path, gotCT, tc.contentType)
		}
		if !bytes.Equal(getRec.Body.Bytes(), tc.wantBytes) {
			t.Fatalf("GET %s bytes do not match embedded brandicon asset", tc.path)
		}

		headReq := httptest.NewRequest(http.MethodHead, "http://127.0.0.1:3232"+tc.path, nil)
		headRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(headRec, headReq)
		if headRec.Code != http.StatusOK || headRec.Body.Len() != 0 {
			t.Fatalf("HEAD %s expected 200 empty body, got %d len=%d", tc.path, headRec.Code, headRec.Body.Len())
		}

		postReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:3232"+tc.path, nil)
		postRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(postRec, postReq)
		if postRec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST %s expected 405, got %d", tc.path, postRec.Code)
		}

		badHostReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3232"+tc.path, nil)
		badHostReq.Host = "evil.example.com"
		badHostRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(badHostRec, badHostReq)
		if badHostRec.Code != http.StatusForbidden {
			t.Fatalf("disallowed Host on %s expected 403, got %d", tc.path, badHostRec.Code)
		}
	}
}
