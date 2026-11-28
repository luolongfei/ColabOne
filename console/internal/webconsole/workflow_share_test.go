package webconsole

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func extractShareKeyFromURL(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse share url %q: %v", rawURL, err)
	}
	k := u.Query().Get("k")
	if k == "" {
		t.Fatalf("missing k query param in %q", rawURL)
	}
	return k
}

func activateShareCookie(t *testing.T, h http.Handler, host, shareKey string) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k="+url.QueryEscape(shareKey), nil)
	req.Host = host
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != WorkflowShareRoutePath {
		t.Fatalf("activate share expected 302 to %s, got %d loc=%q body=%s", WorkflowShareRoutePath, rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == WorkflowShareCookieName {
			return c
		}
	}
	t.Fatalf("expected %s cookie in share activation response", WorkflowShareCookieName)
	return nil
}

func doShareCommand(
	t *testing.T,
	h http.Handler,
	host string,
	cookie *http.Cookie,
	action string,
	payload map[string]any,
	extraHeaders map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"action":  action,
		"payload": payload,
	})
	req := httptest.NewRequest(http.MethodPost, "http://"+host+"/api/share/command", bytes.NewReader(body))
	req.Host = host
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(ClientHeaderName, WorkflowShareClientHeaderValue)
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestWorkflowShareKeyFormatAndAuthIsolation(t *testing.T) {
	dir := t.TempDir()
	vault := newMemoryVault()
	clock := newFakeClock(time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC))

	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  dir,
		Vault:      vault,
		FrontendFS: sampleFrontendFS,
		AppVersion: "1.5.0",
		Now:        clock.Now,
		CallBridge: func(ctx context.Context, method string, payload map[string]any) (string, error) {
			if method == "snapshot" {
				return `{"runtime":{"state":"ready"},"workflows":[{"id":"fashion-tryon-h3","name":"Fashion"}],"comfy_outputs":[]}`, nil
			}
			return `{"ok":true}`, nil
		},
	})
	defer wc.Close()

	passcode, err := wc.Passcode()
	if err != nil {
		t.Fatalf("Passcode failed: %v", err)
	}
	port := reserveFreePort(t)
	if _, err := wc.Update(ColabOneWebConsoleUpdateRequest{
		Enabled: boolPtr(true),
		Port:    intPtr(port),
	}); err != nil {
		t.Fatalf("enable web console failed: %v", err)
	}
	ticket, err := wc.CreateLoginTicket(false)
	if err != nil {
		t.Fatalf("CreateLoginTicket failed: %v", err)
	}
	ticketURL, _ := url.Parse(ticket.URL)
	rawTicket := ticketURL.Query().Get("ticket")

	created, err := wc.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
		Label:       "VIP Owner Note",
		Title:       "Guest Studio",
		WorkflowIDs: []string{"fashion-tryon-h3"},
		TTLMode:     WorkflowShareTTL24h,
	})
	if err != nil {
		t.Fatalf("CreateWorkflowShare failed: %v", err)
	}
	if !workflowShareIDRe.MatchString(created.ID) {
		t.Fatalf("share ID must be 12-char lowercase Crockford Base32, got %q", created.ID)
	}
	if !created.CanCopyLink || created.URL == "" || created.LocalURL == "" || created.Status != "active" {
		t.Fatalf("unexpected created share summary: %+v", created)
	}

	shareKey := extractShareKeyFromURL(t, created.URL)
	if !strings.HasPrefix(shareKey, "co_wf_"+created.ID+".") {
		t.Fatalf("unexpected share key format: %q", shareKey)
	}
	if len(strings.Split(strings.TrimPrefix(shareKey, "co_wf_"), ".")) != 3 {
		t.Fatalf("expected 3 dot-separated segments in share key, got %q", shareKey)
	}

	// Vault stores dedicated HMAC key separate from web_console_passcode.
	var storedHMAC string
	if found, err := vault.GetJSON(VaultWorkflowShareHMACKey, &storedHMAC); err != nil || !found || storedHMAC == "" {
		t.Fatalf("expected %s in vault, found=%v err=%v", VaultWorkflowShareHMACKey, found, err)
	}

	host := "127.0.0.1:3232"

	// Full console passcode and login ticket MUST NOT unlock /share.
	for _, bogusKey := range []string{passcode, rawTicket, "co_wf_" + created.ID + ".invalid.tag"} {
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k="+url.QueryEscape(bogusKey), nil)
		req.Host = host
		req.RemoteAddr = "127.0.0.1:40001"
		rec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(rec, req)
		if rec.Code == http.StatusFound || rec.Code == http.StatusOK {
			t.Fatalf("bogus key %q unexpectedly accepted on /share: %d", bogusKey, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "/_colabone_web/login") || strings.Contains(rec.Body.String(), `name="passcode"`) {
			t.Fatalf("share error page must never link to login or show passcode form")
		}
	}

	// Share key MUST NOT unlock full console login or ticket redeem.
	body, _ := json.Marshal(map[string]string{"passcode": shareKey})
	loginReq := httptest.NewRequest(http.MethodPost, "http://"+host+"/_colabone_web/login", bytes.NewReader(body))
	loginReq.Host = host
	loginReq.RemoteAddr = "127.0.0.1:40002"
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set(ClientHeaderName, ClientHeaderValue)
	loginRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusUnauthorized {
		t.Fatalf("share key must be rejected on /_colabone_web/login, got %d", loginRec.Code)
	}

	// Mint both full-console cookie and share cookie from separate clients.
	webCookie := loginForCookie(t, wc.Handler(), host, passcode, nil)
	shareCookie := activateShareCookie(t, wc.Handler(), host, shareKey)

	// Guest share cookie MUST NOT unlock full console routes or APIs.
	for _, path := range []string{"/api/snapshot", "/api/web/capabilities", "/api/outputs/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/media", "/api/outputs/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/download"} {
		r := httptest.NewRequest(http.MethodGet, "http://"+host+path, nil)
		r.AddCookie(shareCookie)
		w := httptest.NewRecorder()
		wc.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("share cookie on %s expected 401, got %d", path, w.Code)
		}
	}
	// Guest share cookie on "/" redirects to full console login (never serves full console).
	rootReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil)
	rootReq.AddCookie(shareCookie)
	rootRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(rootRec, rootReq)
	if rootRec.Code != http.StatusFound || rootRec.Header().Get("Location") != "/_colabone_web/login" {
		t.Fatalf("share cookie on / expected 302 to login, got %d loc=%q", rootRec.Code, rootRec.Header().Get("Location"))
	}
	// Guest share cookie on POST /api/command rejected with 401.
	cmdReq := httptest.NewRequest(http.MethodPost, "http://"+host+"/api/command", strings.NewReader(`{"action":"snapshot","payload":{}}`))
	cmdReq.Header.Set("Content-Type", "application/json")
	cmdReq.Header.Set(ClientHeaderName, ClientHeaderValue)
	cmdReq.AddCookie(shareCookie)
	cmdRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(cmdRec, cmdReq)
	if cmdRec.Code != http.StatusUnauthorized {
		t.Fatalf("share cookie on /api/command expected 401, got %d", cmdRec.Code)
	}

	// Full console cookie MUST NOT unlock /api/share/* routes.
	for _, path := range []string{"/api/share/capabilities", "/api/share/snapshot"} {
		r := httptest.NewRequest(http.MethodGet, "http://"+host+path, nil)
		r.AddCookie(webCookie)
		w := httptest.NewRecorder()
		wc.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("web cookie on %s expected 401, got %d", path, w.Code)
		}
	}

	// Static assets under /assets/* MUST work with guest share cookie, while dotfiles/missing fail with 404.
	assetReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/assets/app.js", nil)
	assetReq.AddCookie(shareCookie)
	assetRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(assetRec, assetReq)
	if assetRec.Code != http.StatusOK || !strings.Contains(assetRec.Body.String(), "colabone") {
		t.Fatalf("guest asset fetch failed: %d body=%q", assetRec.Code, assetRec.Body.String())
	}
	dotReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/assets/.hidden", nil)
	dotReq.AddCookie(shareCookie)
	dotRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(dotRec, dotReq)
	if dotRec.Code != http.StatusNotFound {
		t.Fatalf("guest dotfile asset expected 404, got %d", dotRec.Code)
	}

	// Guest GET /api/share/capabilities returns Title (not private Label) and expanded workflows.
	capReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/share/capabilities", nil)
	capReq.AddCookie(shareCookie)
	capRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(capRec, capReq)
	if capRec.Code != http.StatusOK {
		t.Fatalf("capabilities expected 200, got %d", capRec.Code)
	}
	var caps ColabOneWorkflowShareCapabilities
	if err := json.Unmarshal(capRec.Body.Bytes(), &caps); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if caps.Runtime != WorkflowShareClientHeaderValue || caps.ShareID != created.ID || caps.Title != "Guest Studio" ||
		!caps.CanDownloadOutput || !caps.CanDeleteOutput || !caps.CanExportToHostDrive {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
	if strings.Contains(capRec.Body.String(), "VIP Owner Note") {
		t.Fatalf("owner private label leaked into capabilities: %s", capRec.Body.String())
	}
}

func TestWorkflowShareCookieFirstAuthSwitchingAndLinkPreview(t *testing.T) {
	dir := t.TempDir()
	vault := newMemoryVault()
	clock := newFakeClock(time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))

	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  dir,
		Vault:      vault,
		FrontendFS: sampleFrontendFS,
		Now:        clock.Now,
	})
	defer wc.Close()

	host := "127.0.0.1:3232"

	// 1. Single-use share ignores link preview bots & prefetchers, then consumes on first real visit.
	onceShare, err := wc.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
		Title:       "Once Share",
		WorkflowIDs: []string{"fashion-tryon-h3"},
		TTLMode:     WorkflowShareTTLOneTime,
	})
	if err != nil {
		t.Fatalf("create once share: %v", err)
	}
	onceKey := extractShareKeyFromURL(t, onceShare.URL)

	for _, tc := range []struct {
		name   string
		header string
		value  string
	}{
		{"telegram", "User-Agent", "TelegramBot (like TwitterBot)"},
		{"feishu", "User-Agent", "Feishu/7.0"},
		{"prefetch", "Sec-Purpose", "prefetch"},
		{"xmoz_prefetch", "X-Moz", "prefetch"},
	} {
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k="+url.QueryEscape(onceKey), nil)
		req.Header.Set(tc.header, tc.value)
		rec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("X-Robots-Tag"), "noindex") {
			t.Fatalf("%s preview expected 200 noindex stub, got %d hdr=%q", tc.name, rec.Code, rec.Header().Get("X-Robots-Tag"))
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatalf("%s preview must not mint session cookie", tc.name)
		}
	}
	// Still active & unconsumed.
	st := wc.WorkflowSharesState()
	if st.Shares[0].Status != "active" || st.Shares[0].ConsumedAt != 0 || !st.Shares[0].CanCopyLink {
		t.Fatalf("preview bot unexpectedly consumed single-use share: %+v", st.Shares[0])
	}

	// First real browser visit activates and consumes single-use share.
	onceCookie := activateShareCookie(t, wc.Handler(), host, onceKey)

	stAfter := wc.WorkflowSharesState()
	if stAfter.Shares[0].Status != "used" || stAfter.Shares[0].ConsumedAt == 0 || stAfter.Shares[0].CanCopyLink || stAfter.Shares[0].URL != "" || stAfter.Shares[0].ActiveSessions != 1 {
		t.Fatalf("unexpected once share state after consumption: %+v", stAfter.Shares[0])
	}

	// A second visitor without cookie gets error page ("used").
	secondReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k="+url.QueryEscape(onceKey), nil)
	secondReq.RemoteAddr = "127.0.0.1:50002"
	secondRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(secondRec, secondReq)
	if secondRec.Code != http.StatusGone || !strings.Contains(secondRec.Body.String(), `data-share-error="used"`) {
		t.Fatalf("second visitor to consumed once share expected 410 used error page, got %d body=%s", secondRec.Code, secondRec.Body.String())
	}

	// Cookie-first auth: original visitor reopening the ALREADY CONSUMED URL still redirects to /share and loads SPA!
	reopenReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k="+url.QueryEscape(onceKey), nil)
	reopenReq.AddCookie(onceCookie)
	reopenRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(reopenRec, reopenReq)
	if reopenRec.Code != http.StatusFound || reopenRec.Header().Get("Location") != WorkflowShareRoutePath {
		t.Fatalf("existing cookie reopening consumed URL expected 302 /share, got %d loc=%q", reopenRec.Code, reopenRec.Header().Get("Location"))
	}

	// Clean /share serves index.html.
	spaReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/share", nil)
	spaReq.AddCookie(onceCookie)
	spaRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(spaRec, spaReq)
	if spaRec.Code != http.StatusOK || !strings.Contains(spaRec.Body.String(), "ColabOne App") {
		t.Fatalf("GET /share with valid cookie expected index.html, got %d body=%q", spaRec.Code, spaRec.Body.String())
	}

	// 2. Cookie-first auth also bypasses an EXPIRED 24h share URL!
	dayShare, err := wc.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
		Title:       "24h Share",
		WorkflowIDs: []string{BuiltinDigitalHumanGroupKey},
		TTLMode:     WorkflowShareTTL24h,
	})
	if err != nil {
		t.Fatalf("create 24h share: %v", err)
	}
	dayKey := extractShareKeyFromURL(t, dayShare.URL)
	dayCookie := activateShareCookie(t, wc.Handler(), host, dayKey)

	// Advance clock by 25 hours so the 24h share URL expires.
	clock.Advance(25 * time.Hour)

	// New visitor gets expired error page.
	expReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k="+url.QueryEscape(dayKey), nil)
	expReq.RemoteAddr = "127.0.0.1:50003"
	expRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(expRec, expReq)
	if expRec.Code != http.StatusGone || !strings.Contains(expRec.Body.String(), `data-share-error="expired"`) {
		t.Fatalf("new visitor to expired 24h share expected 410 expired, got %d", expRec.Code)
	}

	// Existing visitor with dayCookie STILL bypasses expired ?k= AND keeps full API access!
	reopenDayReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k="+url.QueryEscape(dayKey), nil)
	reopenDayReq.AddCookie(dayCookie)
	reopenDayRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(reopenDayRec, reopenDayReq)
	if reopenDayRec.Code != http.StatusFound || reopenDayRec.Header().Get("Location") != WorkflowShareRoutePath {
		t.Fatalf("existing cookie reopening expired URL expected 302 /share, got %d", reopenDayRec.Code)
	}
	capDayReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/share/capabilities", nil)
	capDayReq.AddCookie(dayCookie)
	capDayRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(capDayRec, capDayReq)
	if capDayRec.Code != http.StatusOK {
		t.Fatalf("existing session must stay valid after share URL expiry, got %d", capDayRec.Code)
	}

	// 3. Switching to a DIFFERENT valid active share renders a confirmation interstitial on GET
	// (no silent session overwrite) and switches only on explicit same-origin POST /share/switch!
	foreverShare, err := wc.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
		Title:       "Forever Share",
		WorkflowIDs: []string{"fashion-tryon-h3"},
		TTLMode:     WorkflowShareTTLForever,
	})
	if err != nil {
		t.Fatalf("create forever share: %v", err)
	}
	foreverKey := extractShareKeyFromURL(t, foreverShare.URL)

	switchReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k="+url.QueryEscape(foreverKey), nil)
	switchReq.AddCookie(dayCookie)
	switchRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(switchRec, switchReq)
	if switchRec.Code != http.StatusOK || !strings.Contains(switchRec.Body.String(), `data-share-switch="confirm"`) {
		t.Fatalf("switch share GET expected 200 confirmation interstitial, got %d body=%s", switchRec.Code, switchRec.Body.String())
	}
	if len(switchRec.Result().Cookies()) != 0 {
		t.Fatalf("switch confirmation GET must not emit Set-Cookie, got %+v", switchRec.Result().Cookies())
	}
	if !strings.Contains(switchRec.Header().Get("Content-Security-Policy"), "form-action 'self'") {
		t.Fatalf("switch confirmation page missing form-action 'self' CSP: %q", switchRec.Header().Get("Content-Security-Policy"))
	}
	if !strings.Contains(switchRec.Body.String(), "24h Share") || !strings.Contains(switchRec.Body.String(), "Forever Share") {
		t.Fatalf("switch confirmation page should display current and target share titles")
	}
	switchToken := extractHiddenInputValue(t, switchRec.Body.String(), "token")

	// Confirm switch via same-origin POST /share/switch.
	formBody := url.Values{"token": {switchToken}}.Encode()
	confirmReq := httptest.NewRequest(http.MethodPost, "http://"+host+"/share/switch", strings.NewReader(formBody))
	confirmReq.Host = host
	confirmReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	confirmReq.Header.Set("Origin", "http://"+host)
	confirmReq.AddCookie(dayCookie)
	confirmRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(confirmRec, confirmReq)
	if confirmRec.Code != http.StatusSeeOther || confirmRec.Header().Get("Location") != WorkflowShareRoutePath {
		t.Fatalf("POST /share/switch expected 303 /share, got %d loc=%q body=%s", confirmRec.Code, confirmRec.Header().Get("Location"), confirmRec.Body.String())
	}
	var switchedCookie *http.Cookie
	for _, c := range confirmRec.Result().Cookies() {
		if c.Name == WorkflowShareCookieName {
			switchedCookie = c
		}
	}
	if switchedCookie == nil || switchedCookie.Value == dayCookie.Value {
		t.Fatalf("expected new session cookie after confirming share switch")
	}

	capSwitchedReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/share/capabilities", nil)
	capSwitchedReq.AddCookie(switchedCookie)
	capSwitchedRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(capSwitchedRec, capSwitchedReq)
	var switchedCaps ColabOneWorkflowShareCapabilities
	_ = json.Unmarshal(capSwitchedRec.Body.Bytes(), &switchedCaps)
	if switchedCaps.ShareID != foreverShare.ID || switchedCaps.Title != "Forever Share" {
		t.Fatalf("expected switched capabilities for foreverShare %s, got %+v", foreverShare.ID, switchedCaps)
	}

	// 4. Missing ?k without cookie shows "missing" error page (never redirects to login).
	missingReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/share", nil)
	missingRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(missingRec, missingReq)
	if missingRec.Code != http.StatusUnauthorized || !strings.Contains(missingRec.Body.String(), `data-share-error="missing"`) {
		t.Fatalf("missing key expected 401 missing error page, got %d", missingRec.Code)
	}
}

func TestWorkflowShareTTLModesRevocationAndPersistenceAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	vault := newMemoryVault()
	clock := newFakeClock(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))

	var shareEvents []ColabOneWorkflowShareListState
	wc1 := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  dir,
		Vault:      vault,
		FrontendFS: sampleFrontendFS,
		Now:        clock.Now,
		OnWorkflowSharesChange: func(s ColabOneWorkflowShareListState) {
			shareEvents = append(shareEvents, s)
		},
	})

	sOnce, err := wc1.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{WorkflowIDs: []string{"wf-a"}, TTLMode: WorkflowShareTTLOneTime})
	if err != nil || !sOnce.SingleUse || sOnce.ExpiresAt != clock.Now().Add(24*time.Hour).Unix() {
		t.Fatalf("unexpected once share: %+v err=%v", sOnce, err)
	}
	s24h, err := wc1.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{WorkflowIDs: []string{"wf-a"}, TTLMode: ""})
	if err != nil || s24h.TTLMode != WorkflowShareTTL24h || s24h.SingleUse || s24h.ExpiresAt != clock.Now().Add(24*time.Hour).Unix() {
		t.Fatalf("unexpected default 24h share: %+v err=%v", s24h, err)
	}
	s7d, err := wc1.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{WorkflowIDs: []string{"wf-a"}, TTLMode: WorkflowShareTTL7d})
	if err != nil || s7d.ExpiresAt != clock.Now().Add(7*24*time.Hour).Unix() {
		t.Fatalf("unexpected 7d share: %+v err=%v", s7d, err)
	}
	sForever, err := wc1.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{Label: "Permanent", WorkflowIDs: []string{"wf-a"}, TTLMode: WorkflowShareTTLForever})
	if err != nil || sForever.ExpiresAt != 0 {
		t.Fatalf("unexpected forever share: %+v err=%v", sForever, err)
	}

	if _, err := os.Stat(filepath.Join(dir, WorkflowSharesFilename)); err != nil {
		t.Fatalf("expected persisted %s: %v", WorkflowSharesFilename, err)
	}

	host := "127.0.0.1:3232"
	cookie7d := activateShareCookie(t, wc1.Handler(), host, extractShareKeyFromURL(t, s7d.URL))
	cookieForever := activateShareCookie(t, wc1.Handler(), host, extractShareKeyFromURL(t, sForever.URL))

	if len(shareEvents) == 0 {
		t.Fatalf("expected OnWorkflowSharesChange callbacks to fire")
	}

	// Simulate cold restart.
	wc1.Close()

	wc2 := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  dir,
		Vault:      vault,
		FrontendFS: sampleFrontendFS,
		Now:        clock.Now,
	})
	defer wc2.Close()

	st2 := wc2.WorkflowSharesState()
	if len(st2.Shares) != 4 || st2.TotalActiveSessions != 2 {
		t.Fatalf("expected 4 persisted shares & 2 active sessions after restart, got %+v", st2)
	}

	// Persisted cookies keep working after restart.
	for _, c := range []*http.Cookie{cookie7d, cookieForever} {
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/share/capabilities", nil)
		req.AddCookie(c)
		rec := httptest.NewRecorder()
		wc2.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("persisted share cookie expected 200 after restart, got %d", rec.Code)
		}
	}

	// Advance 8 days: once (unused), 24h, and 7d URLs all expire, while forever stays active.
	clock.Advance(8 * 24 * time.Hour)
	st8d := wc2.WorkflowSharesState()
	statusByID := map[string]string{}
	for _, s := range st8d.Shares {
		statusByID[s.ID] = s.Status
	}
	if statusByID[sOnce.ID] != "expired" || statusByID[s24h.ID] != "expired" || statusByID[s7d.ID] != "expired" || statusByID[sForever.ID] != "active" {
		t.Fatalf("unexpected statuses after 8 days: %+v", statusByID)
	}

	// Manual revocation IMMEDIATELY invalidates all minted sessions for that share.
	afterRevoke, err := wc2.RevokeWorkflowShare(sForever.ID)
	if err != nil {
		t.Fatalf("RevokeWorkflowShare failed: %v", err)
	}
	if afterRevoke.ActiveCount != 0 || afterRevoke.TotalActiveSessions != 1 {
		t.Fatalf("unexpected counts after revoking forever share: %+v", afterRevoke)
	}
	reqRevoked := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/share/capabilities", nil)
	reqRevoked.AddCookie(cookieForever)
	recRevoked := httptest.NewRecorder()
	wc2.Handler().ServeHTTP(recRevoked, reqRevoked)
	if recRevoked.Code != http.StatusUnauthorized {
		t.Fatalf("revoked share cookie expected 401 immediately, got %d", recRevoked.Code)
	}
	// Visiting revoked URL shows revoked error page.
	urlRevokedReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k="+url.QueryEscape(extractShareKeyFromURL(t, sForever.URL)), nil)
	urlRevokedReq.RemoteAddr = "127.0.0.1:50009"
	urlRevokedRec := httptest.NewRecorder()
	wc2.Handler().ServeHTTP(urlRevokedRec, urlRevokedReq)
	if urlRevokedRec.Code != http.StatusGone || !strings.Contains(urlRevokedRec.Body.String(), `data-share-error="revoked"`) {
		t.Fatalf("revoked URL expected 410 revoked page, got %d", urlRevokedRec.Code)
	}

	// Deleting s7d immediately invalidates cookie7d.
	if _, err := wc2.DeleteWorkflowShare(s7d.ID); err != nil {
		t.Fatalf("DeleteWorkflowShare failed: %v", err)
	}
	reqDeleted := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/share/capabilities", nil)
	reqDeleted.AddCookie(cookie7d)
	recDeleted := httptest.NewRecorder()
	wc2.Handler().ServeHTTP(recDeleted, reqDeleted)
	if recDeleted.Code != http.StatusUnauthorized {
		t.Fatalf("deleted share cookie expected 401 immediately, got %d", recDeleted.Code)
	}
}

func TestWorkflowShareCommandWhitelistOwnershipAndSanitization(t *testing.T) {
	dir := t.TempDir()
	vault := newMemoryVault()
	clock := newFakeClock(time.Date(2026, 6, 1, 15, 0, 0, 0, time.UTC))

	const (
		guestInputID   = "11111111111111111111111111111111"
		foreignInputID = "22222222222222222222222222222222"
		guestTaskID    = "33333333333333333333333333333333"
		foreignTaskID  = "44444444444444444444444444444444"
		guestOutID     = "55555555555555555555555555555555"
		fallbackOutID  = "66666666666666666666666666666666"
		foreignOutID   = "77777777777777777777777777777777"
		preExistOutID  = "88888888888888888888888888888888"
		ttsTaskID      = "99999999999999999999999999999999"
	)

	var (
		mu                sync.Mutex
		runtimeState      = "ready"
		driveConnected    = true
		foreignTaskActive = true
		outputs           = []map[string]any{
			{
				"id":                  preExistOutID,
				"workflow_id":         "digital-human-longcat",
				"workflow_name":       "LongCat",
				"kind":                "video",
				"filename":            "old.mp4",
				"extension":           "mp4",
				"mime":                "video/mp4",
				"size":                1234,
				"sha256":              strings.Repeat("a", 64),
				"created_at":          float64(clock.Now().Add(-10 * time.Minute).Unix()),
				"elapsed":             12.5,
				"cu_consumed":         1.2,
				"drive_path":          "/MyDrive/secret/old.mp4",
				"drive_web_view_link": "https://drive.google.com/secret",
			},
		}
		lastRunPayload map[string]any
		lastTTSPayload map[string]any
		mediaServedIDs []string
	)

	outputBytes := []byte("fake-mp4-video-content")
	outputSum := sha256.Sum256(outputBytes)
	outputSHA := hex.EncodeToString(outputSum[:])

	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  dir,
		Vault:      vault,
		FrontendFS: sampleFrontendFS,
		Now:        clock.Now,
		ServeOutputMedia: func(ctx context.Context, rw http.ResponseWriter, r *http.Request, id string) {
			mu.Lock()
			mediaServedIDs = append(mediaServedIDs, id)
			mu.Unlock()
			rw.Header().Set("Content-Type", "video/mp4")
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write(outputBytes)
		},
		CallBridge: func(ctx context.Context, method string, payload map[string]any) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			switch method {
			case "snapshot":
				tasks := []map[string]any{
					{
						"id":         guestTaskID,
						"kind":       "comfyui_run",
						"model_id":   "digital-human-longcat",
						"state":      "running",
						"progress":   0.4,
						"message":    "Running",
						"error":      "",
						"created_at": float64(clock.Now().Unix()),
					},
				}
				if foreignTaskActive {
					tasks = append(tasks, map[string]any{
						"id":       foreignTaskID,
						"kind":     "download",
						"model_id": "secret-host-model",
						"state":    "running",
						"progress": 0.7,
						"message":  "Host private model download",
						"error":    "",
					})
				}
				snap := map[string]any{
					"runtime": map[string]any{
						"state":        runtimeState,
						"capabilities": []string{"comfyui", "comfyui_drive_export", "comfyui_share_guard"},
						"gpu": map[string]any{
							"name":         "NVIDIA L4",
							"total_mib":    23034,
							"used_mib":     12000,
							"util_percent": 91,
						},
						"drive": map[string]any{
							"connected":     driveConnected,
							"mount_path":    "/content/drive/MyDrive/secret",
							"remote_folder": "ColabOne_Drive/outputs",
						},
						"comfyui": map[string]any{
							"installed":          true,
							"running":            true,
							"state":              "ready",
							"tag":                "v0.3",
							"cached_tts_engines": []string{"fish_drama3"},
							"queue_running":      1,
							"queue_pending":      0,
							"error":              "",
							"canvas":             map[string]any{"url": "https://secret-canvas.example.com"},
							"auto_release":       map[string]any{"enabled": true, "state": "armed"},
						},
						"tasks": tasks,
						"idle": map[string]any{
							"server_time":       float64(clock.Now().Unix()),
							"remaining_seconds": 1200,
						},
					},
					"workflows": []map[string]any{
						{"id": "digital-human-infinitetalk", "name": "InfiniteTalk"},
						{"id": "digital-human-longcat", "name": "LongCat"},
						{"id": "fashion-tryon-h3", "name": "Fashion TryOn"},
					},
					"tts_engines": []map[string]any{
						{"id": "fish_drama3", "name": "Fish Drama"},
					},
					"comfy_outputs": outputs,
					"models":        []map[string]any{{"id": "secret-llm", "name": "Secret LLM"}},
					"logs":          []map[string]any{{"id": 1, "message": "secret host log"}},
					"account":       map[string]any{"logged_in": true, "email": "owner@example.com", "balance": 99.5},
					"setup":         map[string]any{"colab": true, "drive": true, "hf": true},
					"settings":      map[string]any{"gpu": "L4", "idle_minutes": 30},
				}
				b, _ := json.Marshal(snap)
				return string(b), nil

			case "comfyui.start":
				return `{"id":"` + guestTaskID + `","kind":"comfyui_start","state":"running"}`, nil

			case "comfyui.input.upload":
				return `{"id":"` + guestInputID + `","filename":"portrait.png"}`, nil

			case "comfyui.run":
				lastRunPayload = cloneShallowMap(payload)
				return fmt.Sprintf(`{"id":%q,"kind":"comfyui_run","model_id":"digital-human-longcat","state":"running","created_at":%.0f,"internal_secret":"strip-me"}`, guestTaskID, float64(clock.Now().Unix())), nil

			case "comfyui.tts.preview":
				lastTTSPayload = cloneShallowMap(payload)
				return `{"cached":false,"preview_id":"p1","task":{"id":"` + ttsTaskID + `","kind":"comfyui_tts_preview","state":"running"}}`, nil

			case "task.cancel":
				return `{"ok":true}`, nil

			case "comfyui.output.asset":
				return `{"data":"` + base64.StdEncoding.EncodeToString(outputBytes) + `","size":` + jsonNumberStr(len(outputBytes)) + `,"offset":0,"sha256":"` + outputSHA + `","extension":"mp4","filename":"demo.mp4","source":"runtime"}`, nil

			case "comfyui.output.export_drive":
				return `{"ok":true,"already_exists":true,"drive_path":"/MyDrive/ColabOne_Drive/outputs/demo.mp4","remote_folder":"ColabOne_Drive/outputs","drive_file_id":"drive_id_123","drive_web_view_link":"https://drive.google.com/file/d/drive_id_123"}`, nil

			case "comfyui.output.delete":
				return `{"ok":true}`, nil

			default:
				return `{"ok":true}`, nil
			}
		},
	})
	defer wc.Close()

	host := "127.0.0.1:3232"

	// Create a Digital Human group share (expands to digital-human-infinitetalk + digital-human-longcat).
	dhShare, err := wc.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
		Label:       "客户A",
		Title:       "数字人体验",
		WorkflowIDs: []string{BuiltinDigitalHumanGroupKey},
		TTLMode:     WorkflowShareTTL24h,
	})
	if err != nil {
		t.Fatalf("create DH share: %v", err)
	}
	dhCookie := activateShareCookie(t, wc.Handler(), host, extractShareKeyFromURL(t, dhShare.URL))

	// Create a non-DH share to verify TTS preview is blocked there.
	fashionShare, err := wc.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
		Label:       "服装分享",
		WorkflowIDs: []string{"fashion-tryon-h3"},
		TTLMode:     WorkflowShareTTL24h,
	})
	if err != nil {
		t.Fatalf("create fashion share: %v", err)
	}
	fashionCookie := activateShareCookie(t, wc.Handler(), host, extractShareKeyFromURL(t, fashionShare.URL))

	// Wrong X-ColabOne-Client header rejected with 403.
	if rec := doShareCommand(t, wc.Handler(), host, dhCookie, "comfyui.start", map[string]any{}, map[string]string{ClientHeaderName: ClientHeaderValue}); rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when X-ColabOne-Client=web instead of web-share, got %d", rec.Code)
	}

	// Non-whitelisted host commands MUST be blocked with 403.
	for _, blocked := range []string{
		"runtime.start", "runtime.release", "settings.save", "settings.hf_token",
		"notifications.save", "notifications.test", "notifications.secret",
		"comfyui.workflow.delete", "comfyui.auto_release", "api.key", "drive.connect",
	} {
		if rec := doShareCommand(t, wc.Handler(), host, dhCookie, blocked, map[string]any{}, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("command %q expected 403 in share mode, got %d", blocked, rec.Code)
		}
	}

	// comfyui.start fails with 400 when host runtime is not ready.
	mu.Lock()
	runtimeState = "stopped"
	mu.Unlock()
	if rec := doShareCommand(t, wc.Handler(), host, dhCookie, "comfyui.start", map[string]any{}, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("comfyui.start while runtime stopped expected 400, got %d", rec.Code)
	}
	mu.Lock()
	runtimeState = "ready"
	mu.Unlock()
	if rec := doShareCommand(t, wc.Handler(), host, dhCookie, "comfyui.start", map[string]any{}, nil); rec.Code != http.StatusOK {
		t.Fatalf("comfyui.start while runtime ready expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Upload input for dhShare -> recorded under dhShare.
	if rec := doShareCommand(t, wc.Handler(), host, dhCookie, "comfyui.input.upload", map[string]any{"filename": "a.png", "data": "AA=="}, nil); rec.Code != http.StatusOK {
		t.Fatalf("input upload expected 200, got %d", rec.Code)
	}

	// Unauthorized workflow_id -> 403.
	if rec := doShareCommand(t, wc.Handler(), host, dhCookie, "comfyui.run", map[string]any{"workflow_id": "fashion-tryon-h3"}, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("unauthorized workflow_id expected 403, got %d", rec.Code)
	}

	// Foreign input ID in comfyui.run -> 403.
	if rec := doShareCommand(t, wc.Handler(), host, dhCookie, "comfyui.run", map[string]any{
		"workflow_id": "digital-human-longcat",
		"inputs":      map[string]any{"portrait": foreignInputID},
	}, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("foreign input ID in comfyui.run expected 403, got %d", rec.Code)
	}

	// Valid comfyui.run on expanded DH workflow strips auto_release_after_sync and hf_token, sanitizes response.
	runRec := doShareCommand(t, wc.Handler(), host, dhCookie, "comfyui.run", map[string]any{
		"workflow_id":             "digital-human-longcat",
		"inputs":                  map[string]any{"portrait": guestInputID},
		"auto_release_after_sync": true,
		"hf_token":                "hf_should_be_stripped",
	}, nil)
	if runRec.Code != http.StatusOK {
		t.Fatalf("valid comfyui.run expected 200, got %d: %s", runRec.Code, runRec.Body.String())
	}
	if strings.Contains(runRec.Body.String(), "internal_secret") {
		t.Fatalf("comfyui.run response was not sanitized: %s", runRec.Body.String())
	}
	mu.Lock()
	if _, hasAR := lastRunPayload["auto_release_after_sync"]; hasAR {
		t.Fatalf("auto_release_after_sync was not stripped from comfyui.run")
	}
	if _, hasHF := lastRunPayload["hf_token"]; hasHF {
		t.Fatalf("hf_token was not stripped from comfyui.run")
	}
	mu.Unlock()

	// TTS preview: blocked on non-DH share, allowed on DH share (with ref audio ownership check + hf_token stripped).
	if rec := doShareCommand(t, wc.Handler(), host, fashionCookie, "comfyui.tts.preview", map[string]any{"engine_id": "fish_drama3"}, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("TTS preview on non-DH share expected 403, got %d", rec.Code)
	}
	if rec := doShareCommand(t, wc.Handler(), host, dhCookie, "comfyui.tts.preview", map[string]any{
		"engine_id":          "fish_drama3",
		"ref_audio_input_id": foreignInputID,
	}, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("TTS preview with foreign ref_audio_input_id expected 403, got %d", rec.Code)
	}
	if rec := doShareCommand(t, wc.Handler(), host, dhCookie, "comfyui.tts.preview", map[string]any{
		"engine_id":          "fish_drama3",
		"ref_audio_input_id": guestInputID,
		"hf_token":           "hf_strip_me",
	}, nil); rec.Code != http.StatusOK {
		t.Fatalf("valid TTS preview expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	mu.Lock()
	if _, hasHF := lastTTSPayload["hf_token"]; hasHF {
		t.Fatalf("hf_token was not stripped from comfyui.tts.preview")
	}
	mu.Unlock()

	// task.cancel: foreign task -> 403, own workflow task and own TTS task -> 200.
	if rec := doShareCommand(t, wc.Handler(), host, dhCookie, "task.cancel", map[string]any{"id": foreignTaskID}, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("canceling foreign task expected 403, got %d", rec.Code)
	}
	if rec := doShareCommand(t, wc.Handler(), host, dhCookie, "task.cancel", map[string]any{"id": guestTaskID}, nil); rec.Code != http.StatusOK {
		t.Fatalf("canceling own task expected 200, got %d", rec.Code)
	}
	if rec := doShareCommand(t, wc.Handler(), host, dhCookie, "task.cancel", map[string]any{"id": ttsTaskID}, nil); rec.Code != http.StatusOK {
		t.Fatalf("canceling own TTS task expected 200, got %d", rec.Code)
	}

	// Simulate 3 new outputs appearing on the remote server:
	// 1) guestOutID with explicit task_id == guestTaskID
	// 2) fallbackOutID without task_id, matching workflow_id and created_at >= StartedAt - 5
	// 3) foreignOutID with explicit task_id == foreignTaskID (must NOT be misattributed!)
	clock.Advance(3 * time.Second)
	nowTs := float64(clock.Now().Unix())
	mu.Lock()
	outputs = append(outputs,
		map[string]any{
			"id":                  guestOutID,
			"task_id":             guestTaskID,
			"workflow_id":         "digital-human-longcat",
			"workflow_name":       "LongCat",
			"kind":                "video",
			"filename":            "guest1.mp4",
			"extension":           "mp4",
			"mime":                "video/mp4",
			"size":                len(outputBytes),
			"sha256":              outputSHA,
			"created_at":          nowTs,
			"elapsed":             8.2,
			"storage":             "runtime",
			"drive_previewable":   false,
			"drive_exported":      false,
			"cu_consumed":         0.42,
			"cu_rate":             1.96,
			"cu_estimated":        false,
			"gpu_name":            "NVIDIA L4",
			"drive_path":          "/MyDrive/secret/guest1.mp4",
			"drive_folder":        "ColabOne_Drive/outputs",
			"drive_file_id":       "file_secret",
			"drive_web_view_link": "https://drive.google.com/secret",
			"drive_mode":          "colab_drivefs",
			"drive_synced":        true,
		},
		map[string]any{
			"id":            fallbackOutID,
			"workflow_id":   "digital-human-longcat",
			"workflow_name": "LongCat",
			"kind":          "video",
			"filename":      "guest2.mp4",
			"extension":     "mp4",
			"mime":          "video/mp4",
			"size":          len(outputBytes),
			"sha256":        outputSHA,
			"created_at":    nowTs,
			"elapsed":       9.0,
			"storage":       "runtime",
		},
		map[string]any{
			"id":            foreignOutID,
			"task_id":       foreignTaskID,
			"workflow_id":   "digital-human-longcat",
			"workflow_name": "LongCat",
			"kind":          "video",
			"filename":      "host.mp4",
			"extension":     "mp4",
			"mime":          "video/mp4",
			"size":          len(outputBytes),
			"sha256":        outputSHA,
			"created_at":    nowTs,
			"elapsed":       5.0,
			"storage":       "runtime",
		},
	)
	mu.Unlock()

	// Fetch guest snapshot (also verifies ETag + gzip!).
	snapReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/share/snapshot", nil)
	snapReq.AddCookie(dhCookie)
	snapReq.Header.Set("Accept-Encoding", "gzip")
	snapRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(snapRec, snapReq)
	if snapRec.Code != http.StatusOK {
		t.Fatalf("guest snapshot expected 200, got %d: %s", snapRec.Code, snapRec.Body.String())
	}
	etag := snapRec.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("expected ETag header on guest snapshot")
	}

	var snapRaw []byte
	if snapRec.Header().Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(bytes.NewReader(snapRec.Body.Bytes()))
		if err != nil {
			t.Fatalf("gzip reader: %v", err)
		}
		snapRaw, _ = io.ReadAll(gz)
		_ = gz.Close()
	} else {
		snapRaw = snapRec.Body.Bytes()
	}

	// Verify If-None-Match returns 304.
	snapReq304 := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/share/snapshot", nil)
	snapReq304.AddCookie(dhCookie)
	snapReq304.Header.Set("If-None-Match", etag)
	snapRec304 := httptest.NewRecorder()
	wc.Handler().ServeHTTP(snapRec304, snapReq304)
	if snapRec304.Code != http.StatusNotModified {
		t.Fatalf("expected 304 Not Modified on matching ETag, got %d", snapRec304.Code)
	}

	var guestSnap map[string]any
	if err := json.Unmarshal(snapRaw, &guestSnap); err != nil {
		t.Fatalf("decode guest snapshot: %v", err)
	}

	// Verify strict sanitization of guest snapshot.
	rawStr := string(snapRaw)
	for _, forbidden := range []string{
		"owner@example.com",
		"secret host log",
		"secret-llm",
		"secret-canvas.example.com",
		"auto_release",
		"cu_consumed",
		"cu_rate",
		"cu_estimated",
		"drive_path",
		"drive_folder",
		"drive_file_id",
		"drive_web_view_link",
		"drive_mode",
		"drive_synced",
		"share_label",
		"客户A",
		foreignTaskID,
		foreignOutID,
		preExistOutID,
		"fashion-tryon-h3",
	} {
		if strings.Contains(rawStr, forbidden) {
			t.Fatalf("forbidden sensitive data %q leaked into guest snapshot: %s", forbidden, rawStr)
		}
	}

	shareMeta := shareAsMap(guestSnap["share"])
	if shareAsString(shareMeta["id"]) != dhShare.ID ||
		shareAsString(shareMeta["title"]) != "数字人体验" ||
		!shareAsBool(shareMeta["busy_other"]) ||
		!shareAsBool(shareMeta["drive_export_ready"]) {
		t.Fatalf("unexpected guest share metadata: %+v", shareMeta)
	}
	guestRT := shareAsMap(guestSnap["runtime"])
	if !shareAsBool(guestRT["drive_export_ready"]) {
		t.Fatalf("expected runtime.drive_export_ready=true")
	}
	guestGPU := shareAsMap(guestRT["gpu"])
	if _, hasUsed := guestGPU["used_mib"]; hasUsed || shareAsString(guestGPU["name"]) != "NVIDIA L4" {
		t.Fatalf("unexpected sanitized guest gpu: %+v", guestGPU)
	}
	guestIdle := shareAsMap(guestRT["idle"])
	if _, hasRem := guestIdle["remaining_seconds"]; hasRem || shareAsFloat64(guestIdle["server_time"]) <= 0 {
		t.Fatalf("unexpected sanitized guest idle: %+v", guestIdle)
	}
	if len(shareAsSlice(guestSnap["workflows"])) != 2 {
		t.Fatalf("expected 2 expanded DH workflows, got %d", len(shareAsSlice(guestSnap["workflows"])))
	}
	if len(shareAsSlice(guestSnap["tts_engines"])) != 1 {
		t.Fatalf("expected tts_engines included for DH share")
	}
	guestOuts := shareAsSlice(guestSnap["comfy_outputs"])
	if len(guestOuts) != 2 {
		t.Fatalf("expected 2 owned outputs (task_id + fallback), got %d: %+v", len(guestOuts), guestOuts)
	}

	// Non-DH share snapshot gets empty tts_engines and only fashion-tryon-h3 workflow.
	fashionSnapReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/share/snapshot", nil)
	fashionSnapReq.AddCookie(fashionCookie)
	fashionSnapRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(fashionSnapRec, fashionSnapReq)
	var fashionSnap map[string]any
	_ = json.Unmarshal(fashionSnapRec.Body.Bytes(), &fashionSnap)
	if len(shareAsSlice(fashionSnap["tts_engines"])) != 0 || len(shareAsSlice(fashionSnap["workflows"])) != 1 {
		t.Fatalf("unexpected fashion share snapshot: tts=%v wf=%v", fashionSnap["tts_engines"], fashionSnap["workflows"])
	}

	// Host snapshot enrichment stamps share_id and share_label onto attributed outputs only!
	passcode, _ := wc.Passcode()
	webCookie := loginForCookie(t, wc.Handler(), host, passcode, nil)
	hostSnapReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/snapshot", nil)
	hostSnapReq.AddCookie(webCookie)
	hostSnapRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(hostSnapRec, hostSnapReq)
	if hostSnapRec.Code != http.StatusOK {
		t.Fatalf("host snapshot expected 200, got %d", hostSnapRec.Code)
	}
	var hostSnap map[string]any
	_ = json.Unmarshal(hostSnapRec.Body.Bytes(), &hostSnap)
	stamped := map[string]string{}
	for _, rawOut := range shareAsSlice(hostSnap["comfy_outputs"]) {
		om := shareAsMap(rawOut)
		if sid := shareAsString(om["share_id"]); sid != "" {
			stamped[shareAsString(om["id"])] = sid + ":" + shareAsString(om["share_label"])
		}
	}
	if stamped[guestOutID] != dhShare.ID+":客户A" || stamped[fallbackOutID] != dhShare.ID+":客户A" {
		t.Fatalf("expected guest outputs stamped in host snapshot, got %+v", stamped)
	}
	if _, ok := stamped[foreignOutID]; ok {
		t.Fatalf("foreign host output must NOT be stamped with share_id")
	}
	if _, ok := stamped[preExistOutID]; ok {
		t.Fatalf("pre-existing output must NOT be stamped with share_id")
	}

	// Foreign output access (media, download, asset, delete, export_drive) -> 403!
	for _, p := range []string{"/api/share/outputs/" + foreignOutID + "/media", "/api/share/outputs/" + foreignOutID + "/download"} {
		r := httptest.NewRequest(http.MethodGet, "http://"+host+p, nil)
		r.AddCookie(dhCookie)
		w := httptest.NewRecorder()
		wc.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("foreign output %s expected 403, got %d", p, w.Code)
		}
	}
	for _, action := range []string{"comfyui.output.asset", "comfyui.output.delete", "comfyui.output.export_drive"} {
		if rec := doShareCommand(t, wc.Handler(), host, dhCookie, action, map[string]any{"id": foreignOutID}, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("foreign %s expected 403, got %d", action, rec.Code)
		}
	}

	// Owned output media & download succeed!
	mediaReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/share/outputs/"+guestOutID+"/media", nil)
	mediaReq.AddCookie(dhCookie)
	mediaRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(mediaRec, mediaReq)
	if mediaRec.Code != http.StatusOK || !bytes.Equal(mediaRec.Body.Bytes(), outputBytes) {
		t.Fatalf("owned output media failed: %d", mediaRec.Code)
	}
	dlReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/share/outputs/"+guestOutID+"/download", nil)
	dlReq.AddCookie(dhCookie)
	dlRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(dlRec, dlReq)
	if dlRec.Code != http.StatusOK || !bytes.Equal(dlRec.Body.Bytes(), outputBytes) {
		t.Fatalf("owned output download failed: %d body=%s", dlRec.Code, dlRec.Body.String())
	}

	// Owned output export_drive fails with 400 when Drive disconnected, succeeds with SANITIZED payload when connected!
	mu.Lock()
	driveConnected = false
	mu.Unlock()
	if rec := doShareCommand(t, wc.Handler(), host, dhCookie, "comfyui.output.export_drive", map[string]any{"id": guestOutID}, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("export_drive when Drive disconnected expected 400, got %d", rec.Code)
	}
	mu.Lock()
	driveConnected = true
	mu.Unlock()
	expRec := doShareCommand(t, wc.Handler(), host, dhCookie, "comfyui.output.export_drive", map[string]any{"id": guestOutID}, nil)
	if expRec.Code != http.StatusOK {
		t.Fatalf("export_drive expected 200, got %d: %s", expRec.Code, expRec.Body.String())
	}
	var expMap map[string]any
	_ = json.Unmarshal(expRec.Body.Bytes(), &expMap)
	if len(expMap) != 2 || !shareAsBool(expMap["ok"]) || !shareAsBool(expMap["already_exists"]) {
		t.Fatalf("expected strictly sanitized export_drive response {ok, already_exists}, got %s", expRec.Body.String())
	}
	for _, secret := range []string{"drive_path", "remote_folder", "drive_file_id", "drive_web_view_link"} {
		if strings.Contains(expRec.Body.String(), secret) {
			t.Fatalf("export_drive leaked %q: %s", secret, expRec.Body.String())
		}
	}

	// Owned output delete removes ownership and decrements share OutputCount!
	if rec := doShareCommand(t, wc.Handler(), host, dhCookie, "comfyui.output.delete", map[string]any{"id": guestOutID}, nil); rec.Code != http.StatusOK {
		t.Fatalf("owned output delete expected 200, got %d", rec.Code)
	}
	// Host full-console command deleting fallbackOutID also forgets ownership!
	hostDelReq := httptest.NewRequest(http.MethodPost, "http://"+host+"/api/command", strings.NewReader(`{"action":"comfyui.output.delete","payload":{"id":"`+fallbackOutID+`"}}`))
	hostDelReq.Header.Set("Content-Type", "application/json")
	hostDelReq.Header.Set(ClientHeaderName, ClientHeaderValue)
	hostDelReq.AddCookie(webCookie)
	hostDelRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(hostDelRec, hostDelReq)
	if hostDelRec.Code != http.StatusOK {
		t.Fatalf("host output delete expected 200, got %d", hostDelRec.Code)
	}
	for _, s := range wc.WorkflowSharesState().Shares {
		if s.ID == dhShare.ID && s.OutputCount != 0 {
			t.Fatalf("expected OutputCount 0 after both outputs deleted, got %d", s.OutputCount)
		}
	}
}

func TestWorkflowShareRateLimitingAndDisableReenableRetention(t *testing.T) {
	dir := t.TempDir()
	vault := newMemoryVault()
	clock := newFakeClock(time.Date(2026, 6, 1, 18, 0, 0, 0, time.UTC))

	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  dir,
		Vault:      vault,
		FrontendFS: sampleFrontendFS,
		Now:        clock.Now,
	})
	defer wc.Close()

	port := reserveFreePort(t)
	if _, err := wc.Update(ColabOneWebConsoleUpdateRequest{
		Enabled: boolPtr(true),
		Port:    intPtr(port),
	}); err != nil {
		t.Fatalf("enable failed: %v", err)
	}

	created, err := wc.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
		Title:       "Persistent Share",
		WorkflowIDs: []string{"fashion-tryon-h3"},
		TTLMode:     WorkflowShareTTLForever,
	})
	if err != nil {
		t.Fatalf("create share failed: %v", err)
	}
	host := "127.0.0.1:3232"
	cookie := activateShareCookie(t, wc.Handler(), host, extractShareKeyFromURL(t, created.URL))

	// Disabling and re-enabling Web Console preserves workflow shares and their active guest sessions.
	if _, err := wc.Update(ColabOneWebConsoleUpdateRequest{Enabled: boolPtr(false)}); err != nil {
		t.Fatalf("disable failed: %v", err)
	}
	if _, err := wc.Update(ColabOneWebConsoleUpdateRequest{Enabled: boolPtr(true)}); err != nil {
		t.Fatalf("re-enable failed: %v", err)
	}
	st := wc.WorkflowSharesState()
	if len(st.Shares) != 1 || st.Shares[0].ActiveSessions != 1 {
		t.Fatalf("expected workflow share and session retained across web console toggle, got %+v", st)
	}
	capReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/share/capabilities", nil)
	capReq.AddCookie(cookie)
	capRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(capRec, capReq)
	if capRec.Code != http.StatusOK {
		t.Fatalf("guest session expected to work after web console re-enable, got %d", capRec.Code)
	}

	// Rate limit brute-force share key attempts per IP.
	for i := 0; i < workflowShareRateLimitMaxPerIP; i++ {
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k=co_wf_aaaaaaaaaaaa.bbbb.cccc", nil)
		req.RemoteAddr = "198.51.100.42:12345"
		rec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d expected 401, got %d", i+1, rec.Code)
		}
	}
	blockedReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k=co_wf_aaaaaaaaaaaa.bbbb.cccc", nil)
	blockedReq.RemoteAddr = "198.51.100.42:12345"
	blockedRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(blockedRec, blockedReq)
	if blockedRec.Code != http.StatusTooManyRequests || blockedRec.Header().Get("Retry-After") == "" {
		t.Fatalf("expected 429 rate limit after repeated failed share keys, got %d retry=%q", blockedRec.Code, blockedRec.Header().Get("Retry-After"))
	}
}

func TestWorkflowShareReviewHardening(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 11, 26, 16, 0, 0, 0, time.UTC))
	var mu sync.Mutex
	runtimeState := "ready"
	hostRuntimeID := "colab-vm-session-secret-001"
	activePack := "fashion-tryon-h3"
	queueRunning := 0
	queuePending := 0
	tasks := []map[string]any{}
	outputs := []map[string]any{}

	guestTaskID := "77777777777777777777777777777777"
	guestOutID := "88888888888888888888888888888888"
	hostOutAfterID := "99999999999999999999999999999999"
	driveOutID := "abababababababababababababababab"
	ttsTaskID := "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
	remoteClockSkew := -120.0 // Remote Colab VM clock is 120s behind local host clock.

	dir := t.TempDir()
	vault := newMemoryVault()
	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  dir,
		Vault:      vault,
		FrontendFS: sampleFrontendFS,
		Now:        clock.Now,
		CallBridge: func(ctx context.Context, action string, payload map[string]any) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			switch action {
			case "snapshot":
				snap := map[string]any{
					"runtime": map[string]any{
						"id":    hostRuntimeID,
						"state": runtimeState,
						"drive": map[string]any{"connected": true},
						"comfyui": map[string]any{
							"installed":            true,
							"running":              true,
							"state":                "ready",
							"active_pack":          activePack,
							"tts_preview_prefetch": map[string]any{"job_id": "host-pf-1", "active": true},
							"queue_running":        queueRunning,
							"queue_pending":        queuePending,
						},
						"tasks": tasks,
					},
					"workflows": []map[string]any{
						{"id": "digital-human-longcat", "name": "LongCat"},
						{"id": "fashion-tryon-h3", "name": "Fashion TryOn"},
					},
					"tts_engines":   []map[string]any{{"id": "fish_drama3", "name": "Fish Drama"}},
					"comfy_outputs": outputs,
				}
				b, _ := json.Marshal(snap)
				return string(b), nil

			case "comfyui.run":
				remoteNow := float64(clock.Now().Unix()) + remoteClockSkew
				tasks = append(tasks, map[string]any{
					"id":         guestTaskID,
					"kind":       "comfyui_run",
					"model_id":   "digital-human-longcat",
					"state":      "running",
					"created_at": remoteNow,
					"logs":       []string{"/content/secret/internal.py: traceback"},
				})
				return fmt.Sprintf(`{"id":%q,"kind":"comfyui_run","model_id":"digital-human-longcat","state":"running","created_at":%.0f,"logs":["secret-log"]}`, guestTaskID, remoteNow), nil

			case "comfyui.tts.preview":
				return fmt.Sprintf(`{"cached":false,"preview_id":"p1","task":{"id":%q,"kind":"comfyui_tts_preview","state":"running","logs":["secret-tts-log"]}}`, ttsTaskID), nil

			case "comfyui.output.asset":
				return `{"data":"AA==","size":1,"offset":0,"filename":"clip.mp4","extension":"mp4","source":"runtime"}`, nil

			case "comfyui.output.delete":
				return `{"ok":true}`, nil

			case "comfyui.output.export_drive":
				return `{"ok":true,"already_exists":false}`, nil

			default:
				return `{"ok":true}`, nil
			}
		},
	})
	defer wc.Close()
	port := reserveFreePort(t)
	if _, err := wc.Update(ColabOneWebConsoleUpdateRequest{
		Enabled: boolPtr(true),
		Port:    intPtr(port),
	}); err != nil {
		t.Fatalf("enable failed: %v", err)
	}
	host := fmt.Sprintf("127.0.0.1:%d", port)

	// 1. HEAD /share?k=<single-use> must NOT consume the single-use key or mint a cookie.
	onceShare, err := wc.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
		Label:       "一次性测试",
		WorkflowIDs: []string{BuiltinDigitalHumanGroupKey},
		TTLMode:     WorkflowShareTTLOneTime,
	})
	if err != nil {
		t.Fatalf("create once share: %v", err)
	}
	onceKey := extractShareKeyFromURL(t, onceShare.URL)
	headReq := httptest.NewRequest(http.MethodHead, "http://"+host+"/share?k="+onceKey, nil)
	headRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(headRec, headReq)
	if headRec.Code != http.StatusOK {
		t.Fatalf("HEAD /share?k=<once> expected 200 stub, got %d", headRec.Code)
	}
	if len(headRec.Result().Cookies()) != 0 {
		t.Fatalf("HEAD /share?k=<once> must not set cookies")
	}
	dhCookie := activateShareCookie(t, wc.Handler(), host, onceKey)

	// 2. Re-visiting own consumed share key while authenticated never hits rate limit;
	// probing OTHER forged share keys while authenticated DOES hit rate limit (429).
	for i := 0; i < workflowShareRateLimitMaxPerIP+3; i++ {
		ownReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k="+onceKey, nil)
		ownReq.RemoteAddr = "198.51.100.77:1111"
		ownReq.AddCookie(dhCookie)
		ownRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(ownRec, ownReq)
		if ownRec.Code != http.StatusFound || ownRec.Header().Get("Location") != "/share" {
			t.Fatalf("own consumed key revisit %d expected 302 /share, got %d", i+1, ownRec.Code)
		}
	}
	for i := 0; i < workflowShareRateLimitMaxPerIP; i++ {
		probeReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k=co_wf_zzzzzzzzzzzz.aaaa.bbbb", nil)
		probeReq.RemoteAddr = "198.51.100.77:1111"
		probeReq.AddCookie(dhCookie)
		probeRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(probeRec, probeReq)
		if probeRec.Code != http.StatusFound {
			t.Fatalf("foreign key probe %d expected 302 back to /share, got %d", i+1, probeRec.Code)
		}
	}
	throttledReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k=co_wf_zzzzzzzzzzzz.aaaa.bbbb", nil)
	throttledReq.RemoteAddr = "198.51.100.77:1111"
	throttledReq.AddCookie(dhCookie)
	throttledRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(throttledRec, throttledReq)
	if throttledRec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on repeated foreign key probes while holding valid share session, got %d", throttledRec.Code)
	}

	// 3. Opaque runtime.id, active_pack scoping, foreign Canvas queue busy_other, and prefetch blocking.
	mu.Lock()
	queueRunning = 1
	mu.Unlock()
	snapReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/share/snapshot", nil)
	snapReq.AddCookie(dhCookie)
	snapRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(snapRec, snapReq)
	if snapRec.Code != http.StatusOK {
		t.Fatalf("share snapshot expected 200, got %d", snapRec.Code)
	}
	var snap1 map[string]any
	_ = json.Unmarshal(snapRec.Body.Bytes(), &snap1)
	rt1 := shareAsMap(snap1["runtime"])
	rtID1 := shareAsString(rt1["id"])
	if len(rtID1) != 16 || rtID1 == hostRuntimeID || strings.Contains(snapRec.Body.String(), hostRuntimeID) {
		t.Fatalf("expected opaque 16-char runtime.id hiding raw host ID, got %q", rtID1)
	}
	if !shareAsBool(shareAsMap(snap1["share"])["busy_other"]) {
		t.Fatalf("expected foreign ComfyUI Canvas queue to mark busy_other=true")
	}
	comfy1 := shareAsMap(rt1["comfyui"])
	if int(shareAsFloat64(comfy1["queue_running"])) != 0 {
		t.Fatalf("expected foreign Canvas queue_running zeroed to 0, got %v", comfy1["queue_running"])
	}
	if _, hasPack := comfy1["active_pack"]; hasPack {
		t.Fatalf("non-shared active_pack fashion-tryon-h3 must not be exposed to DH share")
	}
	if _, hasPrefetch := comfy1["tts_preview_prefetch"]; hasPrefetch {
		t.Fatalf("tts_preview_prefetch must not be exposed in guest snapshot")
	}

	// Switching host runtime ID and setting active_pack to a shared workflow updates both safely.
	mu.Lock()
	hostRuntimeID = "colab-vm-session-secret-002"
	activePack = "digital-human-longcat"
	queueRunning = 0
	mu.Unlock()
	snapRec2 := httptest.NewRecorder()
	wc.Handler().ServeHTTP(snapRec2, snapReq)
	var snap2 map[string]any
	_ = json.Unmarshal(snapRec2.Body.Bytes(), &snap2)
	rtID2 := shareAsString(shareAsMap(snap2["runtime"])["id"])
	if rtID2 == "" || rtID2 == rtID1 {
		t.Fatalf("expected opaque runtime.id to change when host runtime restarts: %q vs %q", rtID1, rtID2)
	}
	if shareAsString(shareAsMap(shareAsMap(snap2["runtime"])["comfyui"])["active_pack"]) != "digital-human-longcat" {
		t.Fatalf("expected allowed active_pack to be exposed")
	}

	// Prefetch endpoints must be blocked (403) in share mode; tts.preview response strips task.logs.
	for _, blockedAction := range []string{"comfyui.tts.preview.prefetch", "comfyui.tts.preview.prefetch.cancel"} {
		if rec := doShareCommand(t, wc.Handler(), host, dhCookie, blockedAction, map[string]any{}, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("%s expected 403 in share mode, got %d", blockedAction, rec.Code)
		}
	}
	ttsRec := doShareCommand(t, wc.Handler(), host, dhCookie, "comfyui.tts.preview", map[string]any{"engine_id": "fish_drama3"}, nil)
	if ttsRec.Code != http.StatusOK || strings.Contains(ttsRec.Body.String(), "secret-tts-log") {
		t.Fatalf("tts.preview expected 200 without task.logs, got %d body=%s", ttsRec.Code, ttsRec.Body.String())
	}

	// 4. Legacy fallback attribution under -120s remote clock drift succeeds for guest output,
	// AND does NOT mis-attribute a subsequent host run's output after NotifyHostWorkflowRunStarted().
	runRec := doShareCommand(t, wc.Handler(), host, dhCookie, "comfyui.run", map[string]any{"workflow_id": "digital-human-longcat"}, nil)
	if runRec.Code != http.StatusOK || strings.Contains(runRec.Body.String(), "secret-log") {
		t.Fatalf("comfyui.run expected 200 without task.logs, got %d body=%s", runRec.Code, runRec.Body.String())
	}
	clock.Advance(15 * time.Second)
	mu.Lock()
	remoteFin := float64(clock.Now().Unix()) + remoteClockSkew
	tasks[0]["state"] = "completed"
	tasks[0]["finished_at"] = remoteFin
	outputs = append(outputs, map[string]any{
		"id":           guestOutID,
		"workflow_id":  "digital-human-longcat",
		"filename":     "guest_skewed.mp4",
		"kind":         "video",
		"created_at":   remoteFin - 2,
		"drive_synced": true,
		"drive_path":   "/MyDrive/ColabOne_Drive/outputs/guest_skewed.mp4",
	})
	mu.Unlock()

	snapRec3 := httptest.NewRecorder()
	wc.Handler().ServeHTTP(snapRec3, snapReq)
	var snap3 map[string]any
	_ = json.Unmarshal(snapRec3.Body.Bytes(), &snap3)
	outs3 := shareAsSlice(snap3["comfy_outputs"])
	if len(outs3) != 1 || shareAsString(shareAsMap(outs3[0])["id"]) != guestOutID {
		t.Fatalf("expected skewed-clock legacy guest output attributed, got %v", outs3)
	}
	if shareAsBool(shareAsMap(outs3[0])["drive_exported"]) || strings.Contains(snapRec3.Body.String(), "drive_path") {
		t.Fatalf("drive_synced alone must NOT be folded into drive_exported=true and must not leak drive_path, got %s", snapRec3.Body.String())
	}
	// When drive_exported is explicitly true, guest snapshot surfaces drive_exported=true without drive_path.
	mu.Lock()
	outputs[0]["drive_exported"] = true
	mu.Unlock()
	snapRec3b := httptest.NewRecorder()
	wc.Handler().ServeHTTP(snapRec3b, snapReq)
	var snap3b map[string]any
	_ = json.Unmarshal(snapRec3b.Body.Bytes(), &snap3b)
	outs3b := shareAsSlice(snap3b["comfy_outputs"])
	if len(outs3b) != 1 || !shareAsBool(shareAsMap(outs3b[0])["drive_exported"]) || strings.Contains(snapRec3b.Body.String(), "drive_path") {
		t.Fatalf("explicit drive_exported=true should be preserved without drive_path, got %s", snapRec3b.Body.String())
	}
	if strings.Contains(snapRec3.Body.String(), "secret/internal.py") {
		t.Fatalf("task.logs leaked in guest snapshot: %s", snapRec3.Body.String())
	}

	// Host now runs the same workflow within 30s (within workflowShareTaskRetention); legacy output has no task_id.
	clock.Advance(20 * time.Second)
	wc.NotifyHostWorkflowRunStarted()
	mu.Lock()
	outputs = append(outputs, map[string]any{
		"id":          hostOutAfterID,
		"workflow_id": "digital-human-longcat",
		"filename":    "host_private.mp4",
		"kind":        "video",
		"created_at":  float64(clock.Now().Unix()) + remoteClockSkew,
	})
	mu.Unlock()
	snapRec4 := httptest.NewRecorder()
	wc.Handler().ServeHTTP(snapRec4, snapReq)
	if strings.Contains(snapRec4.Body.String(), hostOutAfterID) {
		t.Fatalf("host private output generated after guest task was leaked to guest: %s", snapRec4.Body.String())
	}

	// 5. Drive-only outputs and stopped runtime cannot be read or deleted by guests.
	mu.Lock()
	outputs = append(outputs, map[string]any{
		"id":          driveOutID,
		"workflow_id": "digital-human-longcat",
		"filename":    "archived_drive.mp4",
		"kind":        "video",
		"storage":     "drive",
		"created_at":  remoteFin,
	})
	mu.Unlock()
	wc.mu.Lock()
	wc.shareOutputOwners[driveOutID] = colabOneShareOutputOwner{ShareID: onceShare.ID, CreatedAt: clock.Now().Unix()}
	wc.mu.Unlock()

	snapRec5 := httptest.NewRecorder()
	wc.Handler().ServeHTTP(snapRec5, snapReq)
	if strings.Contains(snapRec5.Body.String(), driveOutID) {
		t.Fatalf("storage=drive output must not appear in guest snapshot")
	}
	for _, action := range []string{"comfyui.output.asset", "comfyui.output.delete", "comfyui.output.export_drive"} {
		if rec := doShareCommand(t, wc.Handler(), host, dhCookie, action, map[string]any{"id": driveOutID}, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("%s on storage=drive output expected 403, got %d", action, rec.Code)
		}
	}
	for _, p := range []string{"/api/share/outputs/" + driveOutID + "/media", "/api/share/outputs/" + driveOutID + "/download"} {
		r := httptest.NewRequest(http.MethodGet, "http://"+host+p, nil)
		r.AddCookie(dhCookie)
		wRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(wRec, r)
		if wRec.Code != http.StatusForbidden {
			t.Fatalf("%s on storage=drive output expected 403, got %d", p, wRec.Code)
		}
	}

	// Stopped runtime returns empty comfy_outputs, empty runtime.id, and rejects output access with 400.
	mu.Lock()
	runtimeState = "stopped"
	mu.Unlock()
	snapRecStopped := httptest.NewRecorder()
	wc.Handler().ServeHTTP(snapRecStopped, snapReq)
	var snapStopped map[string]any
	_ = json.Unmarshal(snapRecStopped.Body.Bytes(), &snapStopped)
	if len(shareAsSlice(snapStopped["comfy_outputs"])) != 0 || shareAsString(shareAsMap(snapStopped["runtime"])["id"]) != "" {
		t.Fatalf("stopped runtime must return empty comfy_outputs and empty runtime.id, got %s", snapRecStopped.Body.String())
	}
	if rec := doShareCommand(t, wc.Handler(), host, dhCookie, "comfyui.output.delete", map[string]any{"id": guestOutID}, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("comfyui.output.delete while runtime stopped expected 400, got %d", rec.Code)
	}

	// 6. /share/* subpaths never leak unauthenticated guests to /_colabone_web/login,
	// and authenticated guests normalize back to /share via 302.
	subUnauthReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/share/unknown-subpath", nil)
	subUnauthReq.Header.Set("Accept", "text/html")
	subUnauthRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(subUnauthRec, subUnauthReq)
	if subUnauthRec.Code != http.StatusUnauthorized || strings.Contains(subUnauthRec.Header().Get("Location"), "/_colabone_web/login") {
		t.Fatalf("unauthenticated /share/* expected 401 guest error page, got %d loc=%q", subUnauthRec.Code, subUnauthRec.Header().Get("Location"))
	}
	subAuthReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/share/unknown-subpath", nil)
	subAuthReq.AddCookie(dhCookie)
	subAuthRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(subAuthRec, subAuthReq)
	if subAuthRec.Code != http.StatusFound || subAuthRec.Header().Get("Location") != WorkflowShareRoutePath {
		t.Fatalf("authenticated /share/* expected 302 to /share, got %d loc=%q", subAuthRec.Code, subAuthRec.Header().Get("Location"))
	}

	// 7. Outdated remote capabilities lacking comfyui_drive_export hide drive_export_ready and sanitize errors.
	if runtimeSupportsCapability(map[string]any{"capabilities": []any{"comfyui"}}, "comfyui_drive_export") {
		t.Fatalf("expected missing comfyui_drive_export capability to evaluate false")
	}
	if !runtimeSupportsCapability(map[string]any{"capabilities": []any{"comfyui", "comfyui_drive_export"}}, "comfyui_drive_export") {
		t.Fatalf("expected present comfyui_drive_export capability to evaluate true")
	}
	if sanitized := sanitizeGuestDriveExportError("当前远端服务版本暂不支持作品直传 Google 云盘，请先点击顶部「立即热更新」升级远端服务后再试"); strings.Contains(sanitized, "立即热更新") || strings.Contains(sanitized, "Google 云盘") {
		t.Fatalf("expected host-only Drive upgrade wording sanitized for guest, got %q", sanitized)
	}
}

func jsonNumberStr(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func extractHiddenInputValue(t *testing.T, htmlBody, name string) string {
	t.Helper()
	marker := `name="` + name + `" value="`
	idx := strings.Index(htmlBody, marker)
	if idx < 0 {
		t.Fatalf("missing hidden input %q in HTML: %s", name, htmlBody)
	}
	rest := htmlBody[idx+len(marker):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		t.Fatalf("unterminated value attribute for %q", name)
	}
	return rest[:end]
}

func TestWorkflowShareSwitchCSRFAndSingleUseNonConsumption(t *testing.T) {
	dir := t.TempDir()
	vault := newMemoryVault()
	clock := newFakeClock(time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))

	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  dir,
		Vault:      vault,
		FrontendFS: sampleFrontendFS,
		Now:        clock.Now,
	})
	defer wc.Close()

	host := "127.0.0.1:3232"
	shareA, err := wc.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
		Title:       "Share Alpha",
		WorkflowIDs: []string{"fashion-tryon-h3"},
		TTLMode:     WorkflowShareTTLForever,
	})
	if err != nil {
		t.Fatalf("create shareA: %v", err)
	}
	keyA := extractShareKeyFromURL(t, shareA.URL)
	cookieA1 := activateShareCookie(t, wc.Handler(), host, keyA)
	cookieA2 := activateShareCookie(t, wc.Handler(), host, keyA)

	// Advance past sliding renewal interval so any duplicate Set-Cookie bug would surface,
	// THEN create shareBOnce so it is active & unconsumed.
	clock.Advance(sessionRenewalInterval + time.Minute)

	shareBOnce, err := wc.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
		Title:       "Share Beta Once",
		WorkflowIDs: []string{BuiltinDigitalHumanGroupKey},
		TTLMode:     WorkflowShareTTLOneTime,
	})
	if err != nil {
		t.Fatalf("create shareBOnce: %v", err)
	}
	keyB := extractShareKeyFromURL(t, shareBOnce.URL)

	// Cross-site subresource request must be blocked (403) without consuming single-use key.
	for _, tc := range []struct {
		dest string
		mode string
	}{
		{"image", "no-cors"},
		{"script", "cors"},
		{"", "no-cors"},
	} {
		subReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k="+url.QueryEscape(keyB), nil)
		subReq.Header.Set("Sec-Fetch-Site", "cross-site")
		if tc.dest != "" {
			subReq.Header.Set("Sec-Fetch-Dest", tc.dest)
		}
		if tc.mode != "" {
			subReq.Header.Set("Sec-Fetch-Mode", tc.mode)
		}
		subRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(subRec, subReq)
		if subRec.Code != http.StatusForbidden {
			t.Fatalf("cross-site subresource dest=%q mode=%q expected 403, got %d", tc.dest, tc.mode, subRec.Code)
		}
	}
	// Verify shareBOnce is still active & unconsumed.
	for _, s := range wc.WorkflowSharesState().Shares {
		if s.ID == shareBOnce.ID && (s.Status != "active" || s.ConsumedAt != 0) {
			t.Fatalf("cross-site subresource consumed shareBOnce: %+v", s)
		}
	}

	// GET /share?k=<keyB> while signed into shareA renders confirmation page, emits ZERO Set-Cookie,
	// and does NOT consume shareBOnce!
	getSwitch := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k="+url.QueryEscape(keyB), nil)
	getSwitch.AddCookie(cookieA1)
	getSwitchRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(getSwitchRec, getSwitch)
	if getSwitchRec.Code != http.StatusOK || !strings.Contains(getSwitchRec.Body.String(), `data-share-switch="confirm"`) {
		t.Fatalf("expected 200 switch confirmation page, got %d", getSwitchRec.Code)
	}
	if len(getSwitchRec.Result().Cookies()) != 0 {
		t.Fatalf("switch interstitial GET must not emit Set-Cookie, got %d", len(getSwitchRec.Result().Cookies()))
	}
	for _, s := range wc.WorkflowSharesState().Shares {
		if s.ID == shareBOnce.ID && (s.Status != "active" || s.ConsumedAt != 0) {
			t.Fatalf("GET switch confirmation must not consume single-use target share: %+v", s)
		}
	}

	switchToken := extractHiddenInputValue(t, getSwitchRec.Body.String(), "token")

	// Cross-origin POST /share/switch is rejected with 403.
	crossPost := httptest.NewRequest(http.MethodPost, "http://"+host+"/share/switch", strings.NewReader(url.Values{"token": {switchToken}}.Encode()))
	crossPost.Host = host
	crossPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	crossPost.Header.Set("Origin", "https://evil.example.com")
	crossPost.AddCookie(cookieA1)
	crossRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(crossRec, crossPost)
	if crossRec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST /share/switch expected 403, got %d", crossRec.Code)
	}

	// Token bound to cookieA1 is rejected when submitted with cookieA2 (another session).
	otherSessPost := httptest.NewRequest(http.MethodPost, "http://"+host+"/share/switch", strings.NewReader(url.Values{"token": {switchToken}}.Encode()))
	otherSessPost.Host = host
	otherSessPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	otherSessPost.Header.Set("Origin", "http://"+host)
	otherSessPost.AddCookie(cookieA2)
	otherSessRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(otherSessRec, otherSessPost)
	if otherSessRec.Code != http.StatusForbidden {
		t.Fatalf("switch token from another session expected 403, got %d", otherSessRec.Code)
	}

	// Tampered token is rejected with 403.
	tamperedPost := httptest.NewRequest(http.MethodPost, "http://"+host+"/share/switch", strings.NewReader(url.Values{"token": {switchToken + "x"}}.Encode()))
	tamperedPost.Host = host
	tamperedPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tamperedPost.Header.Set("Origin", "http://"+host)
	tamperedPost.AddCookie(cookieA1)
	tamperedRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(tamperedRec, tamperedPost)
	if tamperedRec.Code != http.StatusForbidden {
		t.Fatalf("tampered switch token expected 403, got %d", tamperedRec.Code)
	}

	// Valid same-origin POST /share/switch swaps session with exactly ONE Set-Cookie and consumes shareBOnce.
	okPost := httptest.NewRequest(http.MethodPost, "http://"+host+"/share/switch", strings.NewReader(url.Values{"token": {switchToken}}.Encode()))
	okPost.Host = host
	okPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	okPost.Header.Set("Origin", "http://"+host)
	okPost.AddCookie(cookieA1)
	okRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(okRec, okPost)
	if okRec.Code != http.StatusSeeOther || okRec.Header().Get("Location") != WorkflowShareRoutePath {
		t.Fatalf("valid POST /share/switch expected 303 /share, got %d loc=%q", okRec.Code, okRec.Header().Get("Location"))
	}
	if len(okRec.Result().Cookies()) != 1 || okRec.Result().Cookies()[0].Name != WorkflowShareCookieName {
		t.Fatalf("expected exactly 1 Set-Cookie on switch, got %+v", okRec.Result().Cookies())
	}
	for _, s := range wc.WorkflowSharesState().Shares {
		if s.ID == shareBOnce.ID && (s.Status != "used" || s.ConsumedAt == 0) {
			t.Fatalf("expected shareBOnce consumed after POST /share/switch, got %+v", s)
		}
	}

	// Old cookieA1 is invalidated after switch.
	oldCapReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/share/capabilities", nil)
	oldCapReq.AddCookie(cookieA1)
	oldCapRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(oldCapRec, oldCapReq)
	if oldCapRec.Code != http.StatusUnauthorized {
		t.Fatalf("old cookieA1 expected 401 after switch, got %d", oldCapRec.Code)
	}

	// Expired switch token (> 5 min) returns 410 Gone.
	shareC, _ := wc.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
		Title:       "Share Gamma",
		WorkflowIDs: []string{"fashion-tryon-h3"},
		TTLMode:     WorkflowShareTTLForever,
	})
	keyC := extractShareKeyFromURL(t, shareC.URL)
	getExp := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k="+url.QueryEscape(keyC), nil)
	getExp.AddCookie(cookieA2)
	getExpRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(getExpRec, getExp)
	expToken := extractHiddenInputValue(t, getExpRec.Body.String(), "token")
	clock.Advance(workflowShareSwitchTokenTTL + time.Second)
	expPost := httptest.NewRequest(http.MethodPost, "http://"+host+"/share/switch", strings.NewReader(url.Values{"token": {expToken}}.Encode()))
	expPost.Host = host
	expPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	expPost.Header.Set("Origin", "http://"+host)
	expPost.AddCookie(cookieA2)
	expRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(expRec, expPost)
	if expRec.Code != http.StatusGone {
		t.Fatalf("expired switch token expected 410, got %d", expRec.Code)
	}
}

func TestWorkflowShareWebConsoleSecurityHardening(t *testing.T) {
	dir := t.TempDir()
	vault := newMemoryVault()
	clock := newFakeClock(time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC))

	var shareChangeCalls int
	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  dir,
		Vault:      vault,
		FrontendFS: sampleFrontendFS,
		Now:        clock.Now,
		OnWorkflowSharesChange: func(s ColabOneWorkflowShareListState) {
			shareChangeCalls++
		},
	})
	defer wc.Close()

	passcode, err := wc.Passcode()
	if err != nil {
		t.Fatalf("Passcode: %v", err)
	}
	host := "127.0.0.1:3232"
	webCookie := loginForCookie(t, wc.Handler(), host, passcode, nil)

	s1, err := wc.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
		Title:       "Quota Share 1",
		WorkflowIDs: []string{"fashion-tryon-h3"},
		TTLMode:     WorkflowShareTTLForever,
	})
	if err != nil {
		t.Fatalf("create s1: %v", err)
	}
	key1 := extractShareKeyFromURL(t, s1.URL)
	shareCookie := activateShareCookie(t, wc.Handler(), host, key1)

	// 1. /assets/* must return private Cache-Control and NEVER attach sliding Set-Cookie or consume renewal window.
	clock.Advance(sessionRenewalInterval + 2*time.Minute)
	for _, c := range []*http.Cookie{webCookie, shareCookie} {
		assetReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/assets/app.js", nil)
		assetReq.AddCookie(c)
		assetRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(assetRec, assetReq)
		if assetRec.Code != http.StatusOK {
			t.Fatalf("asset fetch failed: %d", assetRec.Code)
		}
		if cc := assetRec.Header().Get("Cache-Control"); cc != "private, max-age=31536000, immutable" {
			t.Fatalf("expected private immutable Cache-Control on /assets/*, got %q", cc)
		}
		if len(assetRec.Result().Cookies()) != 0 {
			t.Fatalf("/assets/* must never emit sliding Set-Cookie, got %+v", assetRec.Result().Cookies())
		}
	}
	// Renewal window must still trigger on the subsequent API request!
	capReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/share/capabilities", nil)
	capReq.AddCookie(shareCookie)
	capRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(capRec, capReq)
	if capRec.Code != http.StatusOK || len(capRec.Result().Cookies()) != 1 {
		t.Fatalf("expected sliding Set-Cookie on /api/share/capabilities after /assets/* check, code=%d cookies=%d", capRec.Code, len(capRec.Result().Cookies()))
	}

	// 2. isHTTPSRequest reads rightmost entry of X-Forwarded-Proto (prevents client-prepended spoofing).
	rHTTP := httptest.NewRequest(http.MethodGet, "http://"+host+"/share", nil)
	rHTTP.RemoteAddr = "127.0.0.1:1234"
	rHTTP.Header.Set("X-Forwarded-Proto", "https, http")
	if wc.isHTTPSRequest(rHTTP) {
		t.Fatalf("X-Forwarded-Proto 'https, http' must evaluate false (rightmost is http)")
	}
	rHTTPS := httptest.NewRequest(http.MethodGet, "http://"+host+"/share", nil)
	rHTTPS.RemoteAddr = "127.0.0.1:1234"
	rHTTPS.Header.Set("X-Forwarded-Proto", "http, https")
	if !wc.isHTTPSRequest(rHTTPS) {
		t.Fatalf("X-Forwarded-Proto 'http, https' must evaluate true (rightmost is https)")
	}

	// 3. Valid share activation must NOT clear shareKeyFailures for that IP.
	attackerIP := "203.0.113.9:5555"
	for i := 0; i < workflowShareRateLimitMaxPerIP-1; i++ {
		badReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k=co_wf_aaaaaaaaaaaa.bbbb.cccc", nil)
		badReq.RemoteAddr = attackerIP
		badRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(badRec, badReq)
		if badRec.Code != http.StatusUnauthorized {
			t.Fatalf("bad attempt %d expected 401, got %d", i+1, badRec.Code)
		}
	}
	// Activate valid share from same IP (must NOT reset failure history).
	validReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k="+url.QueryEscape(key1), nil)
	validReq.RemoteAddr = attackerIP
	validRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(validRec, validReq)
	if validRec.Code != http.StatusFound {
		t.Fatalf("valid activation before threshold expected 302, got %d", validRec.Code)
	}
	// 5th bad attempt hits threshold, 6th triggers 429!
	bad5 := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k=co_wf_aaaaaaaaaaaa.bbbb.cccc", nil)
	bad5.RemoteAddr = attackerIP
	bad5Rec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(bad5Rec, bad5)
	if bad5Rec.Code != http.StatusUnauthorized {
		t.Fatalf("5th bad attempt expected 401, got %d", bad5Rec.Code)
	}
	bad6 := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k=co_wf_aaaaaaaaaaaa.bbbb.cccc", nil)
	bad6.RemoteAddr = attackerIP
	bad6Rec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(bad6Rec, bad6)
	if bad6Rec.Code != http.StatusTooManyRequests {
		t.Fatalf("valid activation must not reset IP failures; 6th bad attempt expected 429, got %d", bad6Rec.Code)
	}

	// 4. Per-share session quota (32) and global 256 isolation (returns 429 without evicting other shares or consuming single-use).
	wcQuota := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  t.TempDir(),
		Vault:      newMemoryVault(),
		FrontendFS: sampleFrontendFS,
		Now:        clock.Now,
	})
	defer wcQuota.Close()

	shareCount := maxWorkflowShareSessions / maxWorkflowShareSessionsPerShare // 8 shares * 32 = 256
	shares := make([]ColabOneWorkflowShareSummary, 0, shareCount)
	var firstCookieOfShare0 *http.Cookie
	for i := 0; i < shareCount; i++ {
		sh, err := wcQuota.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
			Title:       fmt.Sprintf("S%d", i),
			WorkflowIDs: []string{"fashion-tryon-h3"},
			TTLMode:     WorkflowShareTTLForever,
		})
		if err != nil {
			t.Fatalf("create quota share %d: %v", i, err)
		}
		shares = append(shares, sh)
		k := extractShareKeyFromURL(t, sh.URL)
		for j := 0; j < maxWorkflowShareSessionsPerShare; j++ {
			clock.Advance(time.Second)
			c := activateShareCookie(t, wcQuota.Handler(), host, k)
			if i == 0 && j == 0 {
				firstCookieOfShare0 = c
			}
		}
	}
	// 33rd session on share 0 evicts share 0's oldest session (firstCookieOfShare0) and keeps count at 32.
	clock.Advance(time.Second)
	_ = activateShareCookie(t, wcQuota.Handler(), host, extractShareKeyFromURL(t, shares[0].URL))
	oldS0Req := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/share/capabilities", nil)
	oldS0Req.AddCookie(firstCookieOfShare0)
	oldS0Rec := httptest.NewRecorder()
	wcQuota.Handler().ServeHTTP(oldS0Rec, oldS0Req)
	if oldS0Rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected share 0's oldest session evicted on 33rd activation, got %d", oldS0Rec.Code)
	}

	// 9th share (single-use) hits global 256 cap -> 429 session_limit, NEVER evicts other shares, NEVER burns single-use!
	share9Once, err := wcQuota.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
		Title:       "Share 9 Once",
		WorkflowIDs: []string{"fashion-tryon-h3"},
		TTLMode:     WorkflowShareTTLOneTime,
	})
	if err != nil {
		t.Fatalf("create share9Once: %v", err)
	}
	req9 := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k="+url.QueryEscape(extractShareKeyFromURL(t, share9Once.URL)), nil)
	rec9 := httptest.NewRecorder()
	wcQuota.Handler().ServeHTTP(rec9, req9)
	if rec9.Code != http.StatusTooManyRequests || !strings.Contains(rec9.Body.String(), `data-share-error="session_limit"`) {
		t.Fatalf("expected 429 session_limit when global cap full with other shares, got %d body=%s", rec9.Code, rec9.Body.String())
	}
	stQuota := wcQuota.WorkflowSharesState()
	if stQuota.TotalActiveSessions != maxWorkflowShareSessions {
		t.Fatalf("expected all %d existing sessions preserved, got %d", maxWorkflowShareSessions, stQuota.TotalActiveSessions)
	}
	for _, s := range stQuota.Shares {
		if s.ID == share9Once.ID && (s.Status != "active" || s.ConsumedAt != 0) {
			t.Fatalf("429 session_limit must not burn single-use share9Once: %+v", s)
		}
	}

	// 5. Per-share input quota (256) and bounded shareTasks (256).
	otherShare, _ := wcQuota.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
		Title:       "InputShareB",
		WorkflowIDs: []string{"fashion-tryon-h3"},
		TTLMode:     WorkflowShareTTLForever,
	})
	otherInputID := strings.Repeat("f", 32)
	wcQuota.recordShareInput(otherShare.ID, otherInputID)
	for i := 0; i < maxWorkflowShareInputsPerShare+10; i++ {
		clock.Advance(time.Millisecond)
		id := fmt.Sprintf("%032x", i+1)
		wcQuota.recordShareInput(shares[0].ID, id)
	}
	if !wcQuota.ownsInputForShare(otherShare.ID, otherInputID) {
		t.Fatalf("otherShare's input must not be evicted by shares[0]'s per-share input quota")
	}
	wcQuota.mu.RLock()
	share0Inputs := 0
	for _, v := range wcQuota.shareInputs {
		if v.shareID == shares[0].ID {
			share0Inputs++
		}
	}
	wcQuota.mu.RUnlock()
	if share0Inputs != maxWorkflowShareInputsPerShare {
		t.Fatalf("expected shares[0] inputs capped at %d, got %d", maxWorkflowShareInputsPerShare, share0Inputs)
	}
	for i := 0; i < maxWorkflowShareTasksTracked+20; i++ {
		clock.Advance(time.Second)
		wcQuota.recordShareTask(shares[0].ID, fmt.Sprintf("task-%04d", i), "fashion-tryon-h3", "rt-1", nil, float64(clock.Now().Unix()), false)
	}
	wcQuota.mu.RLock()
	taskCount := len(wcQuota.shareTasks)
	wcQuota.mu.RUnlock()
	if taskCount > maxWorkflowShareTasksTracked {
		t.Fatalf("expected shareTasks bounded at %d, got %d", maxWorkflowShareTasksTracked, taskCount)
	}

	// 6. DeleteWorkflowShare persist failure restores record as revoked (blocks access, emits change, retryable!).
	delDir := t.TempDir()
	delEvents := 0
	wcDel := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  delDir,
		Vault:      newMemoryVault(),
		FrontendFS: sampleFrontendFS,
		Now:        clock.Now,
		OnWorkflowSharesChange: func(s ColabOneWorkflowShareListState) {
			delEvents++
		},
	})
	defer wcDel.Close()
	delShare, err := wcDel.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
		Title:       "Delete Durability",
		WorkflowIDs: []string{"fashion-tryon-h3"},
		TTLMode:     WorkflowShareTTLForever,
	})
	if err != nil {
		t.Fatalf("create delShare: %v", err)
	}
	delCookie := activateShareCookie(t, wcDel.Handler(), host, extractShareKeyFromURL(t, delShare.URL))
	// Replace workflow-shares.json with a non-empty directory so RenameReplace fails reliably.
	sharesFile := filepath.Join(delDir, WorkflowSharesFilename)
	_ = os.Remove(sharesFile)
	if err := os.Mkdir(sharesFile, 0o755); err != nil {
		t.Fatalf("mkdir obstacle: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sharesFile, "lock"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write obstacle: %v", err)
	}
	eventsBefore := delEvents
	failState, delErr := wcDel.DeleteWorkflowShare(delShare.ID)
	if delErr == nil {
		t.Fatalf("expected DeleteWorkflowShare to fail when persist target is a non-empty dir")
	}
	if delEvents <= eventsBefore {
		t.Fatalf("expected OnWorkflowSharesChange emitted even when DeleteWorkflowShare persist fails")
	}
	if len(failState.Shares) != 1 || failState.Shares[0].Status != "revoked" || failState.TotalActiveSessions != 0 {
		t.Fatalf("expected failed delete to keep record revoked & purge sessions, got %+v", failState)
	}
	// Active guest cookie must remain immediately rejected.
	chkReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/share/capabilities", nil)
	chkReq.AddCookie(delCookie)
	chkRec := httptest.NewRecorder()
	wcDel.Handler().ServeHTTP(chkRec, chkReq)
	if chkRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for deleted-but-persist-failed share cookie, got %d", chkRec.Code)
	}
	// Remove obstacle and retry DeleteWorkflowShare -> succeeds cleanly!
	_ = os.RemoveAll(sharesFile)
	retryState, retryErr := wcDel.DeleteWorkflowShare(delShare.ID)
	if retryErr != nil || len(retryState.Shares) != 0 {
		t.Fatalf("retry DeleteWorkflowShare expected success & empty list, got state=%+v err=%v", retryState, retryErr)
	}
}

func TestWorkflowShareTaskTrackingFallbackLiveStudioAndAutoRelease(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC))
	var (
		mu             sync.Mutex
		runtimeState   = "ready"
		hostRuntimeID  = "rt-vm-01"
		liveStudioOn   = false
		autoReleaseOn  = false
		capabilities   = []string{"comfyui", "comfyui_drive_export"}
		failPreRunSnap = false
		tasks          = []map[string]any{}
		outputs        = []map[string]any{}
		lastRunPayload map[string]any
		nextTaskSeq    = 0
	)

	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  t.TempDir(),
		Vault:      newMemoryVault(),
		FrontendFS: sampleFrontendFS,
		Now:        clock.Now,
		CallBridge: func(ctx context.Context, action string, payload map[string]any) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			switch action {
			case "snapshot":
				if failPreRunSnap {
					return "", fmt.Errorf("transient snapshot failure")
				}
				snap := map[string]any{
					"runtime": map[string]any{
						"id":           hostRuntimeID,
						"state":        runtimeState,
						"capabilities": capabilities,
						"live_studio":  map[string]any{"running": liveStudioOn},
						"comfyui": map[string]any{
							"installed":    true,
							"running":      true,
							"state":        "ready",
							"auto_release": map[string]any{"enabled": autoReleaseOn},
						},
						"tasks": tasks,
					},
					"workflows":     []map[string]any{{"id": "digital-human-longcat", "name": "LongCat"}},
					"tts_engines":   []map[string]any{{"id": "voxcpm2", "name": "VoxCPM2"}},
					"comfy_outputs": outputs,
				}
				b, _ := json.Marshal(snap)
				return string(b), nil
			case "comfyui.start":
				return `{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","kind":"comfyui_start","state":"running"}`, nil
			case "comfyui.tts.preview":
				return `{"cached":false,"preview_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","task":{"id":"cccccccccccccccccccccccccccccccc","kind":"comfyui_tts_preview","state":"running"}}`, nil
			case "comfyui.run":
				lastRunPayload = cloneShallowMap(payload)
				nextTaskSeq++
				tid := fmt.Sprintf("%032x", nextTaskSeq)
				nowF := float64(clock.Now().Unix())
				tasks = append(tasks, map[string]any{
					"id":         tid,
					"kind":       "comfyui_run",
					"model_id":   "digital-human-longcat",
					"state":      "running",
					"created_at": nowF,
				})
				return fmt.Sprintf(`{"id":%q,"kind":"comfyui_run","model_id":"digital-human-longcat","state":"running","created_at":%.0f}`, tid, nowF), nil
			default:
				return `{"ok":true}`, nil
			}
		},
	})
	defer wc.Close()

	host := "127.0.0.1:3232"
	shareA, _ := wc.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
		Label:       "Guest A",
		WorkflowIDs: []string{BuiltinDigitalHumanGroupKey},
		TTLMode:     WorkflowShareTTLForever,
	})
	shareB, _ := wc.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
		Label:       "Guest B",
		WorkflowIDs: []string{BuiltinDigitalHumanGroupKey},
		TTLMode:     WorkflowShareTTLForever,
	})
	cookieA := activateShareCookie(t, wc.Handler(), host, extractShareKeyFromURL(t, shareA.URL))
	cookieB := activateShareCookie(t, wc.Handler(), host, extractShareKeyFromURL(t, shareB.URL))

	// 1. Live Studio running -> busy_other=true and 409 on comfyui.start, comfyui.run, comfyui.tts.preview.
	mu.Lock()
	liveStudioOn = true
	mu.Unlock()
	snapReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/share/snapshot", nil)
	snapReq.AddCookie(cookieA)
	snapRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(snapRec, snapReq)
	var snapMap map[string]any
	_ = json.Unmarshal(snapRec.Body.Bytes(), &snapMap)
	if !shareAsBool(shareAsMap(snapMap["share"])["busy_other"]) {
		t.Fatalf("expected live_studio.running to set share.busy_other=true")
	}
	for _, act := range []string{"comfyui.start", "comfyui.run", "comfyui.tts.preview"} {
		pl := map[string]any{}
		if act == "comfyui.run" {
			pl["workflow_id"] = "digital-human-longcat"
		} else if act == "comfyui.tts.preview" {
			pl["engine_id"] = "voxcpm2"
		}
		if rec := doShareCommand(t, wc.Handler(), host, cookieA, act, pl, nil); rec.Code != http.StatusConflict {
			t.Fatalf("%s during live_studio.running expected 409, got %d: %s", act, rec.Code, rec.Body.String())
		}
	}
	mu.Lock()
	liveStudioOn = false
	mu.Unlock()

	// 2. auto_release protection: without comfyui_share_guard -> 409; with comfyui_share_guard -> injects skip_auto_release=true.
	mu.Lock()
	autoReleaseOn = true
	mu.Unlock()
	if rec := doShareCommand(t, wc.Handler(), host, cookieA, "comfyui.run", map[string]any{
		"workflow_id":       "digital-human-longcat",
		"skip_auto_release": true, // client-supplied flag must be stripped!
	}, nil); rec.Code != http.StatusConflict {
		t.Fatalf("auto_release without comfyui_share_guard expected 409, got %d", rec.Code)
	}
	mu.Lock()
	capabilities = []string{"comfyui", "comfyui_drive_export", "comfyui_share_guard"}
	mu.Unlock()
	if rec := doShareCommand(t, wc.Handler(), host, cookieA, "comfyui.run", map[string]any{
		"workflow_id": "digital-human-longcat",
	}, nil); rec.Code != http.StatusOK {
		t.Fatalf("auto_release with comfyui_share_guard expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	mu.Lock()
	if !shareAsBool(lastRunPayload["skip_auto_release"]) {
		t.Fatalf("expected skip_auto_release=true injected when auto_release enabled + comfyui_share_guard")
	}
	autoReleaseOn = false
	mu.Unlock()
	// Even when auto_release is off and client sends skip_auto_release=false, server forces skip_auto_release=true
	// so mid-run host auto_release arming can never bind to or be disarmed by a guest workflow task.
	if rec := doShareCommand(t, wc.Handler(), host, cookieA, "comfyui.run", map[string]any{
		"workflow_id":       "digital-human-longcat",
		"skip_auto_release": false,
	}, nil); rec.Code != http.StatusOK {
		t.Fatalf("comfyui.run without auto_release expected 200, got %d", rec.Code)
	}
	mu.Lock()
	if !shareAsBool(lastRunPayload["skip_auto_release"]) {
		t.Fatalf("expected skip_auto_release=true always enforced for guest comfyui.run, got %+v", lastRunPayload)
	}
	mu.Unlock()

	// 3. Long-running task survives transient disconnect / reconnecting state and clears FinishedAt when running again.
	task1ID := fmt.Sprintf("%032x", 1)
	clock.Advance(45 * time.Second)
	mu.Lock()
	savedTasks := tasks
	tasks = nil
	runtimeState = "reconnecting"
	mu.Unlock()
	snapRecDisc := httptest.NewRecorder()
	wc.Handler().ServeHTTP(snapRecDisc, snapReq)
	wc.mu.RLock()
	tt1 := wc.shareTasks[task1ID]
	wc.mu.RUnlock()
	if tt1.FinishedAt != 0 {
		t.Fatalf("missing task during non-ready runtime must NOT be marked FinishedAt, got %d", tt1.FinishedAt)
	}
	// Manually stamp FinishedAt + RemoteFinishedAt, then bring task back as "running" -> must explicitly clear both!
	wc.mu.Lock()
	tt1.FinishedAt = clock.Now().Unix()
	tt1.RemoteFinishedAt = float64(clock.Now().Unix())
	wc.shareTasks[task1ID] = tt1
	wc.mu.Unlock()
	mu.Lock()
	runtimeState = "ready"
	tasks = savedTasks
	mu.Unlock()
	snapRecBack := httptest.NewRecorder()
	wc.Handler().ServeHTTP(snapRecBack, snapReq)
	wc.mu.RLock()
	tt1After := wc.shareTasks[task1ID]
	wc.mu.RUnlock()
	if tt1After.FinishedAt != 0 || tt1After.RemoteFinishedAt != 0 {
		t.Fatalf("running task reappearing in snapshot must clear FinishedAt & RemoteFinishedAt, got %+v", tt1After)
	}

	// 4. Failed task closes fallback immediately; pre-run snapshot failure marks FallbackClosed=true.
	mu.Lock()
	tasks[0]["state"] = "failed"
	tasks[0]["finished_at"] = float64(clock.Now().Unix())
	tasks[1]["state"] = "cancelled"
	tasks[1]["finished_at"] = float64(clock.Now().Unix())
	mu.Unlock()
	wc.Handler().ServeHTTP(httptest.NewRecorder(), snapReq)
	wc.mu.RLock()
	if !wc.shareTasks[task1ID].FallbackClosed || !wc.shareTasks[fmt.Sprintf("%032x", 2)].FallbackClosed {
		t.Fatalf("failed/cancelled tasks must immediately close fallback attribution")
	}
	wc.mu.RUnlock()

	mu.Lock()
	failPreRunSnap = true
	seqBeforeFail := nextTaskSeq
	mu.Unlock()
	if rec := doShareCommand(t, wc.Handler(), host, cookieA, "comfyui.run", map[string]any{"workflow_id": "digital-human-longcat"}, nil); rec.Code == http.StatusOK {
		t.Fatalf("comfyui.run with pre-run snap failure must fail closed (non-200), got %d: %s", rec.Code, rec.Body.String())
	}
	mu.Lock()
	failPreRunSnap = false
	seqAfterFail := nextTaskSeq
	mu.Unlock()
	if seqAfterFail != seqBeforeFail {
		t.Fatalf("pre-run snapshot failure must not invoke comfyui.run, nextTaskSeq changed %d to %d", seqBeforeFail, seqAfterFail)
	}

	// 5. Cross-share fallback isolation + Drive/HostRuntimeID exclusion + strict bracketing preference + NotifyHostWorkflowRunStarting.
	clock.Advance(10 * time.Second)
	if rec := doShareCommand(t, wc.Handler(), host, cookieA, "comfyui.run", map[string]any{"workflow_id": "digital-human-longcat"}, nil); rec.Code != http.StatusOK {
		t.Fatalf("shareA run 3 failed: %d", rec.Code)
	}
	task4ID := fmt.Sprintf("%032x", nextTaskSeq)
	t4Start := float64(clock.Now().Unix())
	clock.Advance(10 * time.Second)
	t4Fin := float64(clock.Now().Unix())
	mu.Lock()
	tasks[len(tasks)-1]["state"] = "completed"
	tasks[len(tasks)-1]["finished_at"] = t4Fin
	mu.Unlock()
	// Reconcile so task4 is marked completed with RemoteFinishedAt = t4Fin.
	wc.Handler().ServeHTTP(httptest.NewRecorder(), snapReq)

	// Start task5 on Share B -> closes fallback on finished task4 (Share A)!
	clock.Advance(2 * time.Second)
	if rec := doShareCommand(t, wc.Handler(), host, cookieB, "comfyui.run", map[string]any{"workflow_id": "digital-human-longcat"}, nil); rec.Code != http.StatusOK {
		t.Fatalf("shareB run 5 failed: %d", rec.Code)
	}
	wc.mu.RLock()
	if !wc.shareTasks[task4ID].FallbackClosed {
		t.Fatalf("starting newer Share B comfyui_run must close fallback on finished Share A task")
	}
	wc.mu.RUnlock()

	// Legacy output produced now belongs to Share B (not Share A), and storage="drive" output is NEVER fallback-attributed!
	outB := "12121212121212121212121212121212"
	driveFakeOut := "34343434343434343434343434343434"
	mu.Lock()
	outputs = append(outputs,
		map[string]any{
			"id":          outB,
			"workflow_id": "digital-human-longcat",
			"created_at":  float64(clock.Now().Unix()),
			"storage":     "runtime",
		},
		map[string]any{
			"id":          driveFakeOut,
			"workflow_id": "digital-human-longcat",
			"created_at":  float64(clock.Now().Unix()),
			"storage":     "drive",
		},
	)
	mu.Unlock()
	wc.Handler().ServeHTTP(httptest.NewRecorder(), snapReq)
	if !wc.ownsOutputForShare(shareB.ID, outB) || wc.ownsOutputForShare(shareA.ID, outB) {
		t.Fatalf("cross-share output outB must belong exclusively to shareB")
	}
	if wc.ownsOutputForShare(shareA.ID, driveFakeOut) || wc.ownsOutputForShare(shareB.ID, driveFakeOut) {
		t.Fatalf("storage=drive output must never be fallback-attributed")
	}

	// Strict bracketing test: finished taskX [t4Start, t4Fin] vs newer still-running taskY [t4Fin+2, 0].
	// Output at t4Fin-1 falls inside taskX's strict [RemoteCreatedAt-5, RemoteFinishedAt+10] AND within taskY's -5s slack.
	// Strict bracketing MUST pick taskX over taskY!
	taskX := colabOneShareTrackedTask{
		TaskID:           "task-bracket-x",
		ShareID:          shareA.ID,
		WorkflowID:       "digital-human-longcat",
		HostRuntimeID:    hostRuntimeID,
		StartedAt:        int64(t4Start),
		FinishedAt:       int64(t4Fin),
		RemoteCreatedAt:  t4Start,
		RemoteFinishedAt: t4Fin,
	}
	taskY := colabOneShareTrackedTask{
		TaskID:          "task-bracket-y",
		ShareID:         shareB.ID,
		WorkflowID:      "digital-human-longcat",
		HostRuntimeID:   hostRuntimeID,
		StartedAt:       int64(t4Fin) + 2,
		RemoteCreatedAt: t4Fin + 2,
	}
	if !isBetterShareFallbackCandidate(taskX, taskY, t4Fin-1) {
		t.Fatalf("strictly bracketed finished taskX must beat non-bracketed newer taskY")
	}
	if isBetterShareFallbackCandidate(taskY, taskX, t4Fin-1) {
		t.Fatalf("non-bracketed taskY must not beat strictly bracketed taskX")
	}

	// NotifyHostWorkflowRunStarting closes fallback on finished tasks ONLY, keeping running tasks open.
	wc.mu.Lock()
	wc.shareTasks["finished-open"] = colabOneShareTrackedTask{
		TaskID:          "finished-open",
		ShareID:         shareA.ID,
		WorkflowID:      "digital-human-longcat",
		HostRuntimeID:   hostRuntimeID,
		StartedAt:       int64(t4Start),
		FinishedAt:      int64(t4Fin),
		RemoteCreatedAt: t4Start,
	}
	wc.shareTasks["running-open"] = colabOneShareTrackedTask{
		TaskID:          "running-open",
		ShareID:         shareA.ID,
		WorkflowID:      "digital-human-longcat",
		HostRuntimeID:   hostRuntimeID,
		StartedAt:       int64(t4Fin) + 2,
		RemoteCreatedAt: t4Fin + 2,
	}
	wc.mu.Unlock()
	wc.NotifyHostWorkflowRunStarting()
	wc.mu.RLock()
	finClosed := wc.shareTasks["finished-open"].FallbackClosed
	runClosed := wc.shareTasks["running-open"].FallbackClosed
	wc.mu.RUnlock()
	if !finClosed || runClosed {
		t.Fatalf("NotifyHostWorkflowRunStarting expected finished=closed(%v) running=open(%v)", finClosed, runClosed)
	}
}

func TestWorkflowShareRuntimeOnlyMediaTTSIsolationErrorSanitizationAndI18n(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 7, 4, 10, 0, 0, 0, time.UTC))
	const (
		outID        = "56565656565656565656565656565656"
		ownedPrevID  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		presetPrevID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		otherPrevID  = "cccccccccccccccccccccccccccccccc"
	)

	var (
		mu                   sync.Mutex
		runtimeState         = "ready"
		shareMediaCalled     bool
		hostMediaCalled      bool
		assetChunkSource     = "runtime"
		lastAssetPayload     map[string]any
		lastDeletePayload    map[string]any
		lastTTSPreviewPl     map[string]any
		lastTTSAssetPl       map[string]any
		cacheLookupCalls     int
		cacheReadCalls       int
		bridgeErrToReturn    error
	)

	outputBytes := []byte("runtime-only-video")
	outputSum := sha256.Sum256(outputBytes)
	outputSHA := hex.EncodeToString(outputSum[:])

	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  t.TempDir(),
		Vault:      newMemoryVault(),
		FrontendFS: sampleFrontendFS,
		Now:        clock.Now,
		ServeOutputMedia: func(ctx context.Context, rw http.ResponseWriter, r *http.Request, id string) {
			mu.Lock()
			hostMediaCalled = true
			mu.Unlock()
			rw.WriteHeader(http.StatusOK)
		},
		ServeShareOutputMedia: func(ctx context.Context, rw http.ResponseWriter, r *http.Request, id string) {
			mu.Lock()
			shareMediaCalled = true
			mu.Unlock()
			rw.Header().Set("Content-Type", "video/mp4")
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write(outputBytes)
		},
		CallBridge: func(ctx context.Context, action string, payload map[string]any) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			if bridgeErrToReturn != nil && action != "snapshot" {
				return "", bridgeErrToReturn
			}
			switch action {
			case "snapshot":
				snap := map[string]any{
					"runtime": map[string]any{"state": runtimeState, "id": "rt-1"},
					"comfy_outputs": []map[string]any{
						{"id": outID, "workflow_id": "digital-human-longcat", "storage": "runtime"},
					},
				}
				b, _ := json.Marshal(snap)
				return string(b), nil
			case "comfyui.output.asset":
				lastAssetPayload = cloneShallowMap(payload)
				return fmt.Sprintf(`{"data":%q,"size":%d,"offset":0,"sha256":%q,"extension":"mp4","filename":"v.mp4","source":%q}`,
					base64.StdEncoding.EncodeToString(outputBytes), len(outputBytes), outputSHA, assetChunkSource), nil
			case "comfyui.output.delete":
				lastDeletePayload = cloneShallowMap(payload)
				return `{"ok":true}`, nil
			case "comfyui.tts.preview":
				lastTTSPreviewPl = cloneShallowMap(payload)
				return fmt.Sprintf(`{"cached":true,"preview_id":%q}`, ownedPrevID), nil
			case "comfyui.tts.preview.asset":
				lastTTSAssetPl = cloneShallowMap(payload)
				return fmt.Sprintf(`{"preview_id":%q,"data":"AA==","size":1,"mime":"audio/wav","local_cached":true,"host_secret_path":"/home/owner/cache.wav"}`, payload["preview_id"]), nil
			case "comfyui.tts.preview.cache.lookup":
				cacheLookupCalls++
				return fmt.Sprintf(`{"engine_id":"voxcpm2","items":[{"voice_preset":"shen_zhiyi","preview_id":%q,"cached":true}]}`, presetPrevID), nil
			case "comfyui.tts.preview.cache.read":
				cacheReadCalls++
				return fmt.Sprintf(`{"cached":true,"preview_id":%q,"data":"AA=="}`, presetPrevID), nil
			default:
				return `{"ok":true}`, nil
			}
		},
	})
	defer wc.Close()

	host := "127.0.0.1:3232"
	dhShare, _ := wc.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
		Title:       "DH Isolation",
		WorkflowIDs: []string{BuiltinDigitalHumanGroupKey},
		TTLMode:     WorkflowShareTTLForever,
	})
	cookie := activateShareCookie(t, wc.Handler(), host, extractShareKeyFromURL(t, dhShare.URL))

	wc.mu.Lock()
	wc.shareOutputOwners[outID] = colabOneShareOutputOwner{ShareID: dhShare.ID, CreatedAt: clock.Now().Unix()}
	wc.mu.Unlock()

	// 1. handleShareOutputMedia MUST invoke ServeShareOutputMedia (NOT ServeOutputMedia).
	mReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/share/outputs/"+outID+"/media", nil)
	mReq.AddCookie(cookie)
	mRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(mRec, mReq)
	if mRec.Code != http.StatusOK || !shareMediaCalled || hostMediaCalled {
		t.Fatalf("expected ServeShareOutputMedia called (not host), code=%d share=%v host=%v", mRec.Code, shareMediaCalled, hostMediaCalled)
	}

	// 2. handleShareOutputDownload passes runtime_only=true and rejects source="drive" with 403.
	dlReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/share/outputs/"+outID+"/download", nil)
	dlReq.AddCookie(cookie)
	dlRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(dlRec, dlReq)
	if dlRec.Code != http.StatusOK || !shareAsBool(lastAssetPayload["runtime_only"]) {
		t.Fatalf("download expected 200 with runtime_only=true, code=%d payload=%+v", dlRec.Code, lastAssetPayload)
	}
	mu.Lock()
	assetChunkSource = "drive"
	mu.Unlock()
	dlDriveRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(dlDriveRec, dlReq)
	if dlDriveRec.Code != http.StatusForbidden {
		t.Fatalf("download with source=drive expected 403, got %d", dlDriveRec.Code)
	}
	if rec := doShareCommand(t, wc.Handler(), host, cookie, "comfyui.output.asset", map[string]any{"id": outID}, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("comfyui.output.asset with source=drive expected 403, got %d", rec.Code)
	}
	mu.Lock()
	assetChunkSource = "runtime"
	mu.Unlock()
	if rec := doShareCommand(t, wc.Handler(), host, cookie, "comfyui.output.asset", map[string]any{"id": outID}, nil); rec.Code != http.StatusOK || !shareAsBool(lastAssetPayload["runtime_only"]) {
		t.Fatalf("comfyui.output.asset expected 200 with runtime_only=true, code=%d payload=%+v", rec.Code, lastAssetPayload)
	}

	// 3. Share upload body cap enforces 96 MiB max (ContentLength > 96 MiB rejected with 413).
	bigUpload := httptest.NewRequest(http.MethodPost, "http://"+host+"/api/share/command", strings.NewReader(`{"action":"comfyui.input.upload","payload":{}}`))
	bigUpload.Host = host
	bigUpload.ContentLength = maxWorkflowShareUploadBodyBytes + 1
	bigUpload.Header.Set("Content-Type", "application/json")
	bigUpload.Header.Set(ClientHeaderName, WorkflowShareClientHeaderValue)
	bigUpload.AddCookie(cookie)
	bigRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(bigRec, bigUpload)
	if bigRec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("share upload > 96 MiB expected 413, got %d", bigRec.Code)
	}

	// 4. Unified guest error sanitization strips all host-only hints.
	for _, hostErr := range []string{
		"请先点击顶部「立即热更新」升级远端服务",
		"请点击「热更新服务」重试",
		"无法读取 Google 云盘 文件",
		"缺少云盘凭据，请前往授权与挂载",
		"导出失败 My Drive / ColabOne_Drive",
		"实时演播占用 GPU 失败",
	} {
		mu.Lock()
		bridgeErrToReturn = fmt.Errorf("%s", hostErr)
		mu.Unlock()
		rec := doShareCommand(t, wc.Handler(), host, cookie, "comfyui.input.upload", map[string]any{"filename": "x.png", "data": "AA=="}, nil)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "操作暂时不可用，请联系分享者检查云端服务状态") {
			t.Fatalf("host error %q was not sanitized: code=%d body=%s", hostErr, rec.Code, rec.Body.String())
		}
	}
	mu.Lock()
	bridgeErrToReturn = nil
	mu.Unlock()

	// 5. TTS preview isolation: skip_local_cache=true, preview_id ownership, asset field sanitization,
	// and cache.lookup / cache.read blocked from touching host local disk cache.
	if rec := doShareCommand(t, wc.Handler(), host, cookie, "comfyui.tts.preview", map[string]any{"engine_id": "voxcpm2"}, nil); rec.Code != http.StatusOK {
		t.Fatalf("tts.preview expected 200, got %d", rec.Code)
	}
	if !shareAsBool(lastTTSPreviewPl["skip_local_cache"]) {
		t.Fatalf("guest tts.preview must set skip_local_cache=true")
	}
	// Owned preview_id allowed on tts.preview.asset with skip_local_cache=true & response sanitized; foreign preview_id -> 403!
	if rec := doShareCommand(t, wc.Handler(), host, cookie, "comfyui.tts.preview.asset", map[string]any{"preview_id": otherPrevID}, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("unowned preview_id on tts.preview.asset expected 403, got %d", rec.Code)
	}
	assetRec := doShareCommand(t, wc.Handler(), host, cookie, "comfyui.tts.preview.asset", map[string]any{"preview_id": ownedPrevID}, nil)
	if assetRec.Code != http.StatusOK || !shareAsBool(lastTTSAssetPl["skip_local_cache"]) {
		t.Fatalf("owned preview_id on tts.preview.asset expected 200 with skip_local_cache=true, code=%d payload=%+v", assetRec.Code, lastTTSAssetPl)
	}
	if strings.Contains(assetRec.Body.String(), "host_secret_path") || strings.Contains(assetRec.Body.String(), "local_cached") {
		t.Fatalf("tts.preview.asset leaked unsanitized fields: %s", assetRec.Body.String())
	}
	// cache.lookup & cache.read MUST NOT invoke host CallBridge (return clean found=false miss so UI falls through).
	beforeLookups := cacheLookupCalls
	lookupRec := doShareCommand(t, wc.Handler(), host, cookie, "comfyui.tts.preview.cache.lookup", map[string]any{
		"engine_id": "voxcpm2",
		"items": []map[string]any{
			{"voice_preset": "shen_zhiyi"},
		},
	}, nil)
	if lookupRec.Code != http.StatusOK || cacheLookupCalls != beforeLookups || !strings.Contains(lookupRec.Body.String(), `"found":false`) {
		t.Fatalf("guest cache.lookup must return found=false without hitting host bridge: code=%d calls=%d body=%s", lookupRec.Code, cacheLookupCalls-beforeLookups, lookupRec.Body.String())
	}
	// presetPrevID was never minted for this share -> still 403 on tts.preview.asset!
	if rec := doShareCommand(t, wc.Handler(), host, cookie, "comfyui.tts.preview.asset", map[string]any{"preview_id": presetPrevID}, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("unminted presetPrevID on tts.preview.asset expected 403, got %d", rec.Code)
	}
	beforeReads := cacheReadCalls
	for _, readPayload := range []map[string]any{
		{"preview_id": otherPrevID},
		{"engine_id": "voxcpm2", "voice_preset": "shen_zhiyi"},
		{"engine_id": "voxcpm2", "voice_preset": "shen_zhiyi", "preview_text": "custom guest text"},
	} {
		missRec := doShareCommand(t, wc.Handler(), host, cookie, "comfyui.tts.preview.cache.read", readPayload, nil)
		if missRec.Code != http.StatusOK || cacheReadCalls != beforeReads || !strings.Contains(missRec.Body.String(), `"found":false`) || !strings.Contains(missRec.Body.String(), `"cached":false`) {
			t.Fatalf("guest cache.read must return found=false/cached=false without hitting host bridge: code=%d calls=%d body=%s", missRec.Code, cacheReadCalls-beforeReads, missRec.Body.String())
		}
	}
	// comfyui.output.delete passes runtime_only=true.
	if rec := doShareCommand(t, wc.Handler(), host, cookie, "comfyui.output.delete", map[string]any{"id": outID}, nil); rec.Code != http.StatusOK || !shareAsBool(lastDeletePayload["runtime_only"]) {
		t.Fatalf("comfyui.output.delete expected 200 with runtime_only=true, code=%d payload=%+v", rec.Code, lastDeletePayload)
	}

	// 6. 6-locale error pages & switch confirmation pages + zh-TW / ko-KR terminology assertions + zero right arrows.
	allLocales := []string{"zh-CN", "en-US", "zh-TW", "zh-HK", "ja-JP", "ko-KR"}
	allReasons := []string{
		workflowShareErrMissing,
		workflowShareErrInvalid,
		workflowShareErrExpired,
		workflowShareErrUsed,
		workflowShareErrRevoked,
		workflowShareErrRateLimited,
		workflowShareErrSessionLimit,
	}
	for _, loc := range allLocales {
		for _, reason := range allReasons {
			doc := renderColabOneWorkflowShareErrorPage(loc, reason, "nonce123")
			if !strings.Contains(doc, `lang="`+loc+`"`) || !strings.Contains(doc, `data-share-error="`+reason+`"`) {
				t.Fatalf("error page loc=%s reason=%s malformed", loc, reason)
			}
			assertSharePageTerminology(t, loc, "error/"+reason, doc)
		}
		swDoc := renderColabOneWorkflowShareSwitchPage(loc, "Alpha", "Beta", "tok.abc", "nonce123")
		if !strings.Contains(swDoc, `lang="`+loc+`"`) || !strings.Contains(swDoc, `data-share-switch="confirm"`) {
			t.Fatalf("switch page loc=%s malformed", loc)
		}
		assertSharePageTerminology(t, loc, "switch", swDoc)
	}
}

func assertSharePageTerminology(t *testing.T, locale, kind, htmlDoc string) {
	t.Helper()
	for _, forbiddenArrow := range []string{string(rune(0x2192)), string(rune(0x21d2)), "&" + "rarr;", "&#" + "8594;"} {
		if strings.Contains(htmlDoc, forbiddenArrow) {
			t.Fatalf("%s (%s) must not contain right-pointing arrow %q", locale, kind, forbiddenArrow)
		}
	}
	switch locale {
	case "zh-TW":
		if !strings.Contains(htmlDoc, "工作流程") {
			t.Fatalf("zh-TW %s must use 工作流程", kind)
		}
		if strings.Contains(strings.ReplaceAll(htmlDoc, "工作流程", ""), "工作流") {
			t.Fatalf("zh-TW %s must not use mainland term 工作流", kind)
		}
		if strings.Contains(htmlDoc, "聯繫") {
			t.Fatalf("zh-TW %s must use 聯絡 instead of 聯繫", kind)
		}
	case "ko-KR":
		if !strings.Contains(htmlDoc, "워크플로") {
			t.Fatalf("ko-KR %s must use 워크플로", kind)
		}
		if strings.Contains(htmlDoc, "워크플로우") {
			t.Fatalf("ko-KR %s must not use 워크플로우", kind)
		}
	}
}

func TestWorkflowShareOverridesAllowlistLoadedLLMBusyUploadConcurrencyAndSingleUseFailClosed(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC))
	var (
		mu              sync.Mutex
		loadedModel     = ""
		uploadStartedCh = make(chan struct{}, 4)
		uploadReleaseCh = make(chan struct{})
		blockUploads    = false
	)

	dir := t.TempDir()
	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  dir,
		Vault:      newMemoryVault(),
		FrontendFS: sampleFrontendFS,
		Now:        clock.Now,
		CallBridge: func(ctx context.Context, action string, payload map[string]any) (string, error) {
			mu.Lock()
			lm := loadedModel
			shouldBlock := blockUploads
			mu.Unlock()
			switch action {
			case "snapshot":
				snap := map[string]any{
					"runtime": map[string]any{
						"id":           "rt-strict-1",
						"state":        "ready",
						"loaded_model": lm,
						"capabilities": []string{"comfyui", "comfyui_share_guard"},
					},
					"workflows": []map[string]any{
						{
							"id":   "digital-human-longcat",
							"name": "LongCat",
							"fields": []map[string]any{
								{"key": "1.image", "field_type": "image"},
								{"key": "2.audio", "field_type": "audio"},
								{"key": "pipeline.script_text", "field_type": "textarea"},
								{"key": "pipeline.voice_preset", "field_type": "select", "options": []string{"shen_zhiyi", "custom"}},
								{"key": "pipeline.quality_preset", "field_type": "select", "options": []string{"cinema_flagship", "balanced_hd"}},
								{"key": "pipeline.num_inference_steps", "field_type": "integer", "min_value": 8, "max_value": 60},
								{"key": "pipeline.audio_guidance_scale", "field_type": "number", "min_value": 1.0, "max_value": 7.0},
								{"key": "pipeline.keep_bg", "field_type": "boolean"},
							},
						},
					},
					"tts_engines":   []map[string]any{{"id": "voxcpm2", "name": "VoxCPM2"}},
					"comfy_outputs": []any{},
				}
				b, _ := json.Marshal(snap)
				return string(b), nil
			case "comfyui.input.upload":
				if shouldBlock {
					uploadStartedCh <- struct{}{}
					<-uploadReleaseCh
				}
				return `{"id":"11111111111111111111111111111111","filename":"ok.png"}`, nil
			case "comfyui.run":
				return `{"id":"22222222222222222222222222222222","kind":"comfyui_run","state":"running"}`, nil
			case "comfyui.start":
				return `{"id":"33333333333333333333333333333333","kind":"comfyui_start","state":"running"}`, nil
			case "comfyui.tts.preview":
				return `{"cached":true,"preview_id":"44444444444444444444444444444444"}`, nil
			default:
				return `{"ok":true}`, nil
			}
		},
	})
	defer wc.Close()

	host := "127.0.0.1:3232"
	share, err := wc.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
		Title:       "Strict Guest",
		WorkflowIDs: []string{BuiltinDigitalHumanGroupKey},
		TTLMode:     WorkflowShareTTLForever,
	})
	if err != nil {
		t.Fatalf("create share: %v", err)
	}
	cookie := activateShareCookie(t, wc.Handler(), host, extractShareKeyFromURL(t, share.URL))

	// 1. Strict guest overrides allowlist checks.
	invalidOverrides := []struct {
		name      string
		overrides any
	}{
		{"non_map", "not-a-map"},
		{"undeclared_key", map[string]any{"99.secret_node": "evil"}},
		{"image_field_in_overrides", map[string]any{"1.image": "/etc/passwd"}},
		{"audio_field_in_overrides", map[string]any{"2.audio": "/content/host.wav"}},
		{"bad_boolean", map[string]any{"pipeline.keep_bg": "true"}},
		{"bad_integer_float", map[string]any{"pipeline.num_inference_steps": 12.5}},
		{"bad_integer_bool", map[string]any{"pipeline.num_inference_steps": true}},
		{"integer_below_min", map[string]any{"pipeline.num_inference_steps": 4}},
		{"integer_above_max", map[string]any{"pipeline.num_inference_steps": 100}},
		{"number_below_min", map[string]any{"pipeline.audio_guidance_scale": 0.5}},
		{"number_above_max", map[string]any{"pipeline.audio_guidance_scale": 9.5}},
		{"number_bool", map[string]any{"pipeline.audio_guidance_scale": false}},
		{"select_not_in_options", map[string]any{"pipeline.quality_preset": "unauthorized_preset"}},
		{"voice_preset_bad_regex", map[string]any{"pipeline.voice_preset": "../escape"}},
		{"text_wrong_type", map[string]any{"pipeline.script_text": 12345}},
	}
	for _, tc := range invalidOverrides {
		rec := doShareCommand(t, wc.Handler(), host, cookie, "comfyui.run", map[string]any{
			"workflow_id": "digital-human-longcat",
			"overrides":   tc.overrides,
		}, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("override case %s expected 400, got %d body=%s", tc.name, rec.Code, rec.Body.String())
		}
	}

	// Valid overrides (including engine-specific regex voice_preset ID) succeed with 200.
	okRec := doShareCommand(t, wc.Handler(), host, cookie, "comfyui.run", map[string]any{
		"workflow_id": "digital-human-longcat",
		"overrides": map[string]any{
			"pipeline.script_text":          "你好，ColabOne！",
			"pipeline.voice_preset":         "qwen_speaker_01",
			"pipeline.quality_preset":       "balanced_hd",
			"pipeline.num_inference_steps":  20,
			"pipeline.audio_guidance_scale": 3.5,
			"pipeline.keep_bg":              true,
		},
	}, nil)
	if okRec.Code != http.StatusOK {
		t.Fatalf("valid guest overrides expected 200, got %d body=%s", okRec.Code, okRec.Body.String())
	}

	// 2. Host loaded LLM marks busy_other=true and returns sanitized 409 on start/run/tts.preview.
	mu.Lock()
	loadedModel = "qwen3-8b-host-private"
	mu.Unlock()
	snapReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/share/snapshot", nil)
	snapReq.AddCookie(cookie)
	snapRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(snapRec, snapReq)
	if snapRec.Code != http.StatusOK {
		t.Fatalf("snapshot expected 200, got %d", snapRec.Code)
	}
	if strings.Contains(snapRec.Body.String(), "qwen3-8b-host-private") {
		t.Fatalf("loaded_model leaked into guest snapshot: %s", snapRec.Body.String())
	}
	var snapMap map[string]any
	_ = json.Unmarshal(snapRec.Body.Bytes(), &snapMap)
	if !shareAsBool(shareAsMap(snapMap["share"])["busy_other"]) {
		t.Fatalf("expected loaded_model to set share.busy_other=true")
	}
	for _, act := range []string{"comfyui.start", "comfyui.run", "comfyui.tts.preview"} {
		pl := map[string]any{}
		if act == "comfyui.run" {
			pl["workflow_id"] = "digital-human-longcat"
		} else if act == "comfyui.tts.preview" {
			pl["engine_id"] = "voxcpm2"
		}
		rec := doShareCommand(t, wc.Handler(), host, cookie, act, pl, nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s while host LLM loaded expected 409, got %d body=%s", act, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "实时演播") || strings.Contains(rec.Body.String(), "qwen3") {
			t.Fatalf("%s 409 message leaked host workload details: %s", act, rec.Body.String())
		}
	}
	mu.Lock()
	loadedModel = ""
	mu.Unlock()

	// 3. Guest upload concurrency limit (max 2 concurrent uploads; 3rd gets 429 immediately).
	mu.Lock()
	blockUploads = true
	mu.Unlock()
	var uploadWG sync.WaitGroup
	uploadCodes := make([]int, 2)
	for i := 0; i < 2; i++ {
		uploadWG.Add(1)
		go func(idx int) {
			defer uploadWG.Done()
			rec := doShareCommand(t, wc.Handler(), host, cookie, "comfyui.input.upload", map[string]any{"filename": "a.png", "data": "AA=="}, nil)
			uploadCodes[idx] = rec.Code
		}(i)
	}
	<-uploadStartedCh
	<-uploadStartedCh
	thirdRec := doShareCommand(t, wc.Handler(), host, cookie, "comfyui.input.upload", map[string]any{"filename": "c.png", "data": "AA=="}, nil)
	if thirdRec.Code != http.StatusTooManyRequests || thirdRec.Header().Get("Retry-After") == "" {
		t.Fatalf("3rd concurrent guest upload expected 429 with Retry-After, got %d hdr=%v", thirdRec.Code, thirdRec.Header())
	}
	close(uploadReleaseCh)
	uploadWG.Wait()
	if uploadCodes[0] != http.StatusOK || uploadCodes[1] != http.StatusOK {
		t.Fatalf("first 2 concurrent uploads expected 200, got %v", uploadCodes)
	}

	// 4. Error sanitization scrubs host paths, tokens, tracebacks, and loopback URLs.
	for _, rawErr := range []string{
		"open /content/drive/secret/model.bin: permission denied",
		`read C:\Users\alice\AppData\Local\ColabOne\vault.json failed`,
		"upstream auth failed with hf_secretToken1234567890",
		"Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9 failed",
		"Traceback (most recent call last): internal error",
		"dial tcp http://127.0.0.1:8188 refused",
	} {
		if got := sanitizeGuestBridgeError(rawErr); got != "操作暂时不可用，请联系分享者检查云端服务状态" {
			t.Fatalf("sanitizeGuestBridgeError(%q) = %q, want sanitized fallback", rawErr, got)
		}
	}

	// 5. SingleUse persistence fail-closed on BOTH entry (/share?k=) and switch (POST /share/switch).
	onceEntry, err := wc.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
		Title:       "Once Entry FailClosed",
		WorkflowIDs: []string{"digital-human-longcat"},
		TTLMode:     WorkflowShareTTLOneTime,
	})
	if err != nil {
		t.Fatalf("create onceEntry: %v", err)
	}
	onceSwitch, err := wc.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
		Title:       "Once Switch FailClosed",
		WorkflowIDs: []string{"digital-human-longcat"},
		TTLMode:     WorkflowShareTTLOneTime,
	})
	if err != nil {
		t.Fatalf("create onceSwitch: %v", err)
	}
	onceEntryKey := extractShareKeyFromURL(t, onceEntry.URL)
	onceSwitchKey := extractShareKeyFromURL(t, onceSwitch.URL)

	// Render switch token while disk is still healthy.
	swGetReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k="+url.QueryEscape(onceSwitchKey), nil)
	swGetReq.AddCookie(cookie)
	swGetRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(swGetRec, swGetReq)
	if swGetRec.Code != http.StatusOK {
		t.Fatalf("switch GET expected 200, got %d", swGetRec.Code)
	}
	swTok := extractHiddenInputValue(t, swGetRec.Body.String(), "token")

	// Break workflow-shares.json persistence with a non-empty directory.
	sharesFile := filepath.Join(dir, WorkflowSharesFilename)
	_ = os.Remove(sharesFile)
	if err := os.Mkdir(sharesFile, 0o755); err != nil {
		t.Fatalf("mkdir obstacle: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sharesFile, "lock"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write obstacle: %v", err)
	}

	// SingleUse entry activation must fail closed with 503, no cookie, and rolled-back ConsumedAt.
	entryReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k="+url.QueryEscape(onceEntryKey), nil)
	entryRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(entryRec, entryReq)
	if entryRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("SingleUse entry on persist failure expected 503, got %d", entryRec.Code)
	}
	if len(entryRec.Result().Cookies()) != 0 {
		t.Fatalf("SingleUse entry fail-closed must not emit Set-Cookie")
	}
	for _, s := range wc.WorkflowSharesState().Shares {
		if s.ID == onceEntry.ID && (s.ConsumedAt != 0 || s.ActiveSessions != 0 || s.Status != "active") {
			t.Fatalf("SingleUse entry fail-closed did not roll back: %+v", s)
		}
	}

	// SingleUse switch must fail closed with 503, no cookie, rolled-back ConsumedAt, and preserved prior session!
	swPost := httptest.NewRequest(http.MethodPost, "http://"+host+"/share/switch", strings.NewReader(url.Values{"token": {swTok}}.Encode()))
	swPost.Host = host
	swPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	swPost.Header.Set("Origin", "http://"+host)
	swPost.AddCookie(cookie)
	swPostRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(swPostRec, swPost)
	if swPostRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("SingleUse switch on persist failure expected 503, got %d", swPostRec.Code)
	}
	if len(swPostRec.Result().Cookies()) != 0 {
		t.Fatalf("SingleUse switch fail-closed must not emit Set-Cookie")
	}
	for _, s := range wc.WorkflowSharesState().Shares {
		if s.ID == onceSwitch.ID && (s.ConsumedAt != 0 || s.ActiveSessions != 0 || s.Status != "active") {
			t.Fatalf("SingleUse switch fail-closed did not roll back: %+v", s)
		}
	}
	// Prior guest cookie remains valid after failed switch!
	priorCapReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/share/capabilities", nil)
	priorCapReq.AddCookie(cookie)
	priorCapRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(priorCapRec, priorCapReq)
	if priorCapRec.Code != http.StatusOK {
		t.Fatalf("prior session cookie should stay valid after fail-closed switch, got %d", priorCapRec.Code)
	}
}

func TestHostWebWorkflowSharesAPIAndLANURLs(t *testing.T) {
	dir := t.TempDir()
	vault := newMemoryVault()
	clock := newFakeClock(time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC))

	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  dir,
		Vault:      vault,
		FrontendFS: sampleFrontendFS,
		AppVersion: "0.1.29",
		Now:        clock.Now,
		CallBridge: func(ctx context.Context, method string, payload map[string]any) (string, error) {
			if method == "snapshot" {
				return `{"runtime":{"state":"ready"},"workflows":[{"id":"fashion-tryon-h3","name":"Fashion"}],"comfy_outputs":[]}`, nil
			}
			return `{"ok":true}`, nil
		},
	})
	defer wc.Close()

	// Stub two LAN interface IPs so we can verify both fallback ordering and request-host matching.
	wc.listInterfaceIPs = func() []netip.Addr {
		return []netip.Addr{
			netip.MustParseAddr("172.17.0.1"),
			netip.MustParseAddr("192.168.1.50"),
		}
	}

	passcode, err := wc.Passcode()
	if err != nil {
		t.Fatalf("Passcode: %v", err)
	}
	port := reserveFreePort(t)
	if _, err := wc.Update(ColabOneWebConsoleUpdateRequest{
		Enabled:  boolPtr(true),
		BindMode: strPtr(BindModeLAN),
		Port:     intPtr(port),
	}); err != nil {
		t.Fatalf("enable LAN web console: %v", err)
	}

	lanHost := fmt.Sprintf("192.168.1.50:%d", port)
	webCookie := loginForCookie(t, wc.Handler(), lanHost, passcode, nil)

	// 1) Unauthenticated GET /api/workflow-shares -> 401.
	unauthReq := httptest.NewRequest(http.MethodGet, "http://"+lanHost+"/api/workflow-shares", nil)
	unauthReq.Host = lanHost
	unauthReq.Header.Set(ClientHeaderName, ClientHeaderValue)
	unauthRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET /api/workflow-shares expected 401, got %d", unauthRec.Code)
	}

	// 2) Missing client header on POST /api/workflow-shares -> 403.
	createBody := `{"label":"Mobile Share","title":"Phone Studio","workflow_ids":["fashion-tryon-h3"],"ttl_mode":"24h"}`
	noHdrReq := httptest.NewRequest(http.MethodPost, "http://"+lanHost+"/api/workflow-shares", strings.NewReader(createBody))
	noHdrReq.Host = lanHost
	noHdrReq.AddCookie(webCookie)
	noHdrRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(noHdrRec, noHdrReq)
	if noHdrRec.Code != http.StatusForbidden {
		t.Fatalf("POST /api/workflow-shares without client header expected 403, got %d", noHdrRec.Code)
	}

	// 3) Cross-origin POST -> 403.
	crossReq := httptest.NewRequest(http.MethodPost, "http://"+lanHost+"/api/workflow-shares", strings.NewReader(createBody))
	crossReq.Host = lanHost
	crossReq.Header.Set("Content-Type", "application/json")
	crossReq.Header.Set(ClientHeaderName, ClientHeaderValue)
	crossReq.Header.Set("Origin", "https://evil.example.com")
	crossReq.AddCookie(webCookie)
	crossRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(crossRec, crossReq)
	if crossRec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST /api/workflow-shares expected 403, got %d", crossRec.Code)
	}

	// 4) Authenticated host web POST /api/workflow-shares over LAN host uses the exact LAN Host IP in URL!
	createReq := httptest.NewRequest(http.MethodPost, "http://"+lanHost+"/api/workflow-shares", strings.NewReader(createBody))
	createReq.Host = lanHost
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set(ClientHeaderName, ClientHeaderValue)
	createReq.Header.Set("Origin", "http://"+lanHost)
	createReq.AddCookie(webCookie)
	createRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("POST /api/workflow-shares expected 200, got %d body=%s", createRec.Code, createRec.Body.String())
	}
	var created ColabOneWorkflowShareSummary
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created workflow share: %v", err)
	}
	if !created.CanCopyLink || created.Status != "active" {
		t.Fatalf("unexpected created workflow share: %+v", created)
	}
	wantLANPrefix := fmt.Sprintf("http://192.168.1.50:%d/share?k=", port)
	if !strings.HasPrefix(created.URL, wantLANPrefix) {
		t.Fatalf("expected created URL prefix %q, got %q", wantLANPrefix, created.URL)
	}
	if !strings.HasPrefix(created.LocalURL, fmt.Sprintf("http://127.0.0.1:%d/share?k=", port)) {
		t.Fatalf("expected loopback LocalURL, got %q", created.LocalURL)
	}

	// Desktop WorkflowSharesState() in LAN mode prefers a LAN IP rather than 127.0.0.1.
	desktopState := wc.WorkflowSharesState()
	if len(desktopState.Shares) != 1 || !strings.HasPrefix(desktopState.Shares[0].URL, fmt.Sprintf("http://172.17.0.1:%d/share?k=", port)) {
		t.Fatalf("expected desktop LAN fallback URL, got %+v", desktopState.Shares)
	}

	// 5) Guest workflow-share session cookie must NEVER be allowed to access /api/workflow-shares*.
	shareKey := extractShareKeyFromURL(t, created.URL)
	guestCookie := activateShareCookie(t, wc.Handler(), lanHost, shareKey)
	guestListReq := httptest.NewRequest(http.MethodGet, "http://"+lanHost+"/api/workflow-shares", nil)
	guestListReq.Host = lanHost
	guestListReq.Header.Set(ClientHeaderName, ClientHeaderValue)
	guestListReq.AddCookie(guestCookie)
	guestListRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(guestListRec, guestListReq)
	if guestListRec.Code != http.StatusUnauthorized {
		t.Fatalf("guest cookie on GET /api/workflow-shares expected 401, got %d", guestListRec.Code)
	}

	// 6) Host GET /api/workflow-shares lists the created share with the matching LAN host URL.
	listReq := httptest.NewRequest(http.MethodGet, "http://"+lanHost+"/api/workflow-shares", nil)
	listReq.Host = lanHost
	listReq.Header.Set(ClientHeaderName, ClientHeaderValue)
	listReq.AddCookie(webCookie)
	listRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET /api/workflow-shares expected 200, got %d body=%s", listRec.Code, listRec.Body.String())
	}
	var listed ColabOneWorkflowShareListState
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode listed workflow shares: %v", err)
	}
	if len(listed.Shares) != 1 || listed.Shares[0].ID != created.ID || !strings.HasPrefix(listed.Shares[0].URL, wantLANPrefix) {
		t.Fatalf("unexpected listed workflow shares: %+v", listed)
	}

	// 7) Configuring PublicOrigin switches share URLs to the public origin even on LAN requests.
	if _, err := wc.Update(ColabOneWebConsoleUpdateRequest{
		PublicOrigin: strPtr("https://studio.example.com"),
	}); err != nil {
		t.Fatalf("set public origin: %v", err)
	}
	pubListReq := httptest.NewRequest(http.MethodGet, "http://"+lanHost+"/api/workflow-shares", nil)
	pubListReq.Host = lanHost
	pubListReq.Header.Set(ClientHeaderName, ClientHeaderValue)
	pubListReq.AddCookie(webCookie)
	pubListRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(pubListRec, pubListReq)
	var pubListed ColabOneWorkflowShareListState
	if err := json.Unmarshal(pubListRec.Body.Bytes(), &pubListed); err != nil {
		t.Fatalf("decode public listed workflow shares: %v", err)
	}
	if len(pubListed.Shares) != 1 || !pubListed.Shares[0].UsesPublicHost || !strings.HasPrefix(pubListed.Shares[0].URL, "https://studio.example.com/share?k=") {
		t.Fatalf("expected public origin URL, got %+v", pubListed.Shares)
	}

	// 8) Host POST /api/workflow-shares/{id}/revoke revokes the share and invalidates guest cookie.
	revReq := httptest.NewRequest(http.MethodPost, "http://"+lanHost+"/api/workflow-shares/"+created.ID+"/revoke", nil)
	revReq.Host = lanHost
	revReq.Header.Set(ClientHeaderName, ClientHeaderValue)
	revReq.AddCookie(webCookie)
	revRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(revRec, revReq)
	if revRec.Code != http.StatusOK || !strings.Contains(revRec.Body.String(), `"status":"revoked"`) {
		t.Fatalf("POST revoke expected 200 revoked, got %d body=%s", revRec.Code, revRec.Body.String())
	}
	guestAfterRevokeReq := httptest.NewRequest(http.MethodGet, "http://"+lanHost+"/api/share/snapshot", nil)
	guestAfterRevokeReq.Host = lanHost
	guestAfterRevokeReq.AddCookie(guestCookie)
	guestAfterRevokeRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(guestAfterRevokeRec, guestAfterRevokeReq)
	if guestAfterRevokeRec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked guest cookie expected 401 on /api/share/snapshot, got %d", guestAfterRevokeRec.Code)
	}

	// 9) Trailing JSON tokens on POST /api/workflow-shares are rejected with 400.
	trailingReq := httptest.NewRequest(http.MethodPost, "http://"+lanHost+"/api/workflow-shares", strings.NewReader(createBody+`{"extra":1}`))
	trailingReq.Host = lanHost
	trailingReq.Header.Set("Content-Type", "application/json")
	trailingReq.Header.Set(ClientHeaderName, ClientHeaderValue)
	trailingReq.Header.Set("Origin", "http://"+lanHost)
	trailingReq.AddCookie(webCookie)
	trailingRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(trailingRec, trailingReq)
	if trailingRec.Code != http.StatusBadRequest {
		t.Fatalf("trailing JSON on POST /api/workflow-shares expected 400, got %d", trailingRec.Code)
	}

	// 10) Defense-in-depth: resolveWorkflowShareLANBase rejects spoofed non-interface IPs and normalizes default port :80.
	lanCfg := ColabOneWebConsoleConfig{BindMode: BindModeLAN, Port: port}
	spoofReq := httptest.NewRequest(http.MethodGet, "http://203.0.113.99:3232/api/workflow-shares", nil)
	spoofReq.Host = "203.0.113.99:3232"
	if got := wc.resolveWorkflowShareLANBase(spoofReq, lanCfg); got != fmt.Sprintf("http://172.17.0.1:%d", port) {
		t.Fatalf("spoofed Host IP must fall back to interface LAN base, got %q", got)
	}
	port80Req := httptest.NewRequest(http.MethodGet, "http://192.168.1.50:80/api/workflow-shares", nil)
	port80Req.Host = "192.168.1.50:80"
	if got := wc.resolveWorkflowShareLANBase(port80Req, lanCfg); got != "http://192.168.1.50" {
		t.Fatalf("explicit :80 HTTP Host expected canonical http://192.168.1.50, got %q", got)
	}

	// 11) Host DELETE /api/workflow-shares/{id} permanently removes it.
	delReq := httptest.NewRequest(http.MethodDelete, "http://"+lanHost+"/api/workflow-shares/"+created.ID, nil)
	delReq.Host = lanHost
	delReq.Header.Set(ClientHeaderName, ClientHeaderValue)
	delReq.AddCookie(webCookie)
	delRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusOK || strings.Contains(delRec.Body.String(), created.ID) {
		t.Fatalf("DELETE expected 200 empty list, got %d body=%s", delRec.Code, delRec.Body.String())
	}
}
