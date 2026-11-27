package webconsole

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func extractOutputShareKeyFromURL(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse output share url %q: %v", rawURL, err)
	}
	k := u.Query().Get("k")
	if k == "" {
		t.Fatalf("missing k query param in %q", rawURL)
	}
	return k
}

func activateOutputShareCookie(t *testing.T, h http.Handler, host, shareKey string) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://"+host+"/gallery?k="+url.QueryEscape(shareKey), nil)
	req.Host = host
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != OutputShareRoutePath {
		t.Fatalf("activate output share expected 302 to %s, got %d loc=%q body=%s", OutputShareRoutePath, rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == OutputShareCookieName {
			return c
		}
	}
	t.Fatalf("expected %s cookie in output share activation response", OutputShareCookieName)
	return nil
}

func extractChallengeFromUnlockHTML(t *testing.T, htmlBody string) string {
	t.Helper()
	re := regexp.MustCompile(`name="challenge"\s+value="([^"]+)"`)
	m := re.FindStringSubmatch(htmlBody)
	if len(m) != 2 || m[1] == "" {
		t.Fatalf("could not extract unlock challenge from HTML: %s", htmlBody)
	}
	return m[1]
}

func TestOutputShareValidationPersistenceAndVaultHygiene(t *testing.T) {
	dir := t.TempDir()
	vault := newMemoryVault()
	clock := newFakeClock(time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))

	const (
		out1 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		out2 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)

	var logs []string
	var changeEvents []ColabOneOutputShareListState
	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  dir,
		Vault:      vault,
		FrontendFS: sampleFrontendFS,
		AppVersion: "2.0.0",
		Now:        clock.Now,
		FetchSnapshot: func(ctx context.Context) (string, error) {
			return fmt.Sprintf(`{
				"runtime":{"state":"ready"},
				"comfy_outputs":[
					{
						"id":"%s",
						"filename":"hero_shot.png",
						"extension":"png",
						"kind":"image",
						"mime":"image/png",
						"size":12345,
						"workflow_id":"fashion-tryon-h3",
						"workflow_name":"AI 模特换装",
						"created_at":1765000000,
						"task_id":"task-1",
						"drive_path":"/My Drive/ColabOne_Drive/secret.png",
						"drive_file_id":"drive-fid-secret",
						"drive_web_view_link":"https://drive.google.com/file/d/drive-fid-secret/view",
						"cu_consumed":1.5,
						"share_id":"wfshare12345",
						"prompt_id":"secret-prompt-1"
					},
					{
						"id":"%s",
						"filename":"00_抠衣质检_上装.png",
						"extension":"png",
						"kind":"image",
						"mime":"image/png",
						"size":4567,
						"workflow_id":"fashion-tryon-h3",
						"workflow_name":"AI 模特换装",
						"created_at":1765000010,
						"task_id":"task-1"
					}
				]
			}`, out1, out2), nil
		},
		OnLog: func(message, source, level string) {
			logs = append(logs, message)
		},
		OnOutputSharesChange: func(st ColabOneOutputShareListState) {
			changeEvents = append(changeEvents, st)
		},
	})
	defer wc.Close()

	// Validation failure cases.
	if _, err := wc.CreateOutputShare(ColabOneOutputShareCreateRequest{}); err == nil {
		t.Fatal("expected error for empty groups")
	}
	if _, err := wc.CreateOutputShare(ColabOneOutputShareCreateRequest{
		Groups: []ColabOneOutputShareGroupInput{{Title: "G", OutputIDs: []string{"not-a-valid-hex-id"}}},
	}); err == nil {
		t.Fatal("expected error for invalid output ID")
	}
	if _, err := wc.CreateOutputShare(ColabOneOutputShareCreateRequest{
		TTLMode: "99d",
		Groups:  []ColabOneOutputShareGroupInput{{Title: "G", OutputIDs: []string{out1}}},
	}); err == nil {
		t.Fatal("expected error for invalid TTL mode")
	}
	if _, err := wc.CreateOutputShare(ColabOneOutputShareCreateRequest{
		Title:  strings.Repeat("字", 101),
		Groups: []ColabOneOutputShareGroupInput{{Title: "G", OutputIDs: []string{out1}}},
	}); err == nil {
		t.Fatal("expected error for title > 100 runes")
	}
	if _, err := wc.CreateOutputShare(ColabOneOutputShareCreateRequest{
		Note:   "bad\ncontrol",
		Groups: []ColabOneOutputShareGroupInput{{Title: "G", OutputIDs: []string{out1}}},
	}); err == nil {
		t.Fatal("expected error for control character in note")
	}
	if _, err := wc.CreateOutputShare(ColabOneOutputShareCreateRequest{
		Password: "123",
		Groups:   []ColabOneOutputShareGroupInput{{Title: "G", OutputIDs: []string{out1}}},
	}); err == nil {
		t.Fatal("expected error for password < 4 runes")
	}
	if _, err := wc.CreateOutputShare(ColabOneOutputShareCreateRequest{
		Password: strings.Repeat("a", 33),
		Groups:   []ColabOneOutputShareGroupInput{{Title: "G", OutputIDs: []string{out1}}},
	}); err == nil {
		t.Fatal("expected error for password > 32 runes")
	}
	tooManyGroups := make([]ColabOneOutputShareGroupInput, 65)
	for i := range tooManyGroups {
		tooManyGroups[i] = ColabOneOutputShareGroupInput{Title: "G", OutputIDs: []string{out1}}
	}
	if _, err := wc.CreateOutputShare(ColabOneOutputShareCreateRequest{Groups: tooManyGroups}); err == nil {
		t.Fatal("expected error for > 64 groups")
	}
	tooManyIDs := make([]string, 257)
	for i := range tooManyIDs {
		tooManyIDs[i] = fmt.Sprintf("%032x", i+1)
	}
	if _, err := wc.CreateOutputShare(ColabOneOutputShareCreateRequest{
		Groups: []ColabOneOutputShareGroupInput{{Title: "Big", OutputIDs: tooManyIDs}},
	}); err == nil {
		t.Fatal("expected error for > 256 unique outputs")
	}

	// Valid creation with empty TTL (defaults to 7d) + password.
	secretPw := "AlbumPass#2026"
	created, err := wc.CreateOutputShare(ColabOneOutputShareCreateRequest{
		Title:         "秋季新品样片",
		Note:          "仅限客户审片",
		AllowDownload: true,
		Password:      secretPw,
		Groups: []ColabOneOutputShareGroupInput{
			{
				ID:           "grp-1",
				Title:        "第一套穿搭",
				WorkflowID:   "fashion-tryon-h3",
				WorkflowName: "AI 模特换装",
				TaskID:       "task-1",
				OutputIDs:    []string{out1, out2, out1}, // duplicate out1 deduplicated
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateOutputShare failed: %v", err)
	}
	if !outputShareIDRe.MatchString(created.ID) {
		t.Fatalf("unexpected share ID: %q", created.ID)
	}
	if created.TTLMode != OutputShareTTL7d || !created.AllowDownload || !created.HasPassword || created.Password != secretPw {
		t.Fatalf("unexpected created summary: %+v", created)
	}
	if created.Status != "active" || created.GroupCount != 1 || created.ItemCount != 2 || !created.CanCopyLink || !strings.Contains(created.URL, "/gallery?k=co_gal_") {
		t.Fatalf("unexpected summary counters/URL: %+v", created)
	}
	if len(changeEvents) == 0 {
		t.Fatal("expected OnOutputSharesChange callback fired")
	}
	if len(logs) == 0 || !strings.Contains(logs[len(logs)-1], "已创建成果物分享链接（2 项作品，有效期 7d）") {
		t.Fatalf("unexpected activity log: %v", logs)
	}

	// Verify output-shares.json NEVER contains plaintext password OR sensitive host snapshot fields!
	diskRaw, err := os.ReadFile(filepath.Join(dir, OutputSharesFilename))
	if err != nil {
		t.Fatalf("read output-shares.json: %v", err)
	}
	diskText := string(diskRaw)
	for _, forbidden := range []string{secretPw, "drive-fid-secret", "/My Drive/", "secret-prompt-1", "wfshare12345"} {
		if strings.Contains(diskText, forbidden) {
			t.Fatalf("output-shares.json leaked sensitive string %q: %s", forbidden, diskText)
		}
	}
	if !strings.Contains(diskText, `"password_salt"`) || !strings.Contains(diskText, `"password_hash"`) {
		t.Fatalf("expected scrypt salt/hash in output-shares.json: %s", diskText)
	}

	// Verify plaintext password IS stored in encrypted Vault under VaultOutputSharePasswordsKey.
	var vaultedPws map[string]string
	found, err := vault.GetJSON(VaultOutputSharePasswordsKey, &vaultedPws)
	if err != nil || !found || vaultedPws[created.ID] != secretPw {
		t.Fatalf("expected vaulted password in %s, found=%v err=%v map=%v", VaultOutputSharePasswordsKey, found, err, vaultedPws)
	}

	// Simulate restart: new ColabOneWebConsole reloads share + vaulted password cleanly.
	wc.Close()
	wc2 := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  dir,
		Vault:      vault,
		FrontendFS: sampleFrontendFS,
		Now:        clock.Now,
	})
	defer wc2.Close()

	st2 := wc2.OutputSharesState()
	if len(st2.Shares) != 1 || st2.ActiveCount != 1 || st2.Shares[0].Password != secretPw || st2.Shares[0].ItemCount != 2 {
		t.Fatalf("unexpected state after reload: %+v", st2)
	}

	// Revoke removes password from Vault immediately and updates status.
	revState, err := wc2.RevokeOutputShare(created.ID)
	if err != nil {
		t.Fatalf("RevokeOutputShare failed: %v", err)
	}
	if revState.ActiveCount != 0 || revState.Shares[0].Status != "revoked" || revState.Shares[0].Password != "" || revState.Shares[0].CanCopyLink {
		t.Fatalf("unexpected revoked state: %+v", revState)
	}
	var afterRevPws map[string]string
	if foundAfter, _ := vault.GetJSON(VaultOutputSharePasswordsKey, &afterRevPws); foundAfter && len(afterRevPws) > 0 {
		t.Fatalf("vault password must be deleted immediately on revoke, got %v", afterRevPws)
	}

	// Delete permanently removes the record.
	delState, err := wc2.DeleteOutputShare(created.ID)
	if err != nil || len(delState.Shares) != 0 {
		t.Fatalf("DeleteOutputShare failed: state=%+v err=%v", delState, err)
	}
}

func TestOutputShareAuthIsolationPrefetchAndLocales(t *testing.T) {
	dir := t.TempDir()
	vault := newMemoryVault()
	clock := newFakeClock(time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC))

	const outID = "cccccccccccccccccccccccccccccccc"

	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  dir,
		Vault:      vault,
		FrontendFS: sampleFrontendFS,
		AppVersion: "2.0.0",
		Now:        clock.Now,
		CallBridge: func(ctx context.Context, method string, payload map[string]any) (string, error) {
			return `{"runtime":{"state":"ready"},"workflows":[],"comfy_outputs":[]}`, nil
		},
	})
	defer wc.Close()

	passcode, err := wc.Passcode()
	if err != nil {
		t.Fatalf("Passcode: %v", err)
	}
	host := "127.0.0.1:3232"

	wfShare, err := wc.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
		Title:       "WF Share",
		WorkflowIDs: []string{"fashion-tryon-h3"},
		TTLMode:     WorkflowShareTTL24h,
	})
	if err != nil {
		t.Fatalf("CreateWorkflowShare: %v", err)
	}
	galShare, err := wc.CreateOutputShare(ColabOneOutputShareCreateRequest{
		Title:   "Public Gallery",
		TTLMode: OutputShareTTL24h,
		Groups:  []ColabOneOutputShareGroupInput{{Title: "G1", OutputIDs: []string{outID}}},
	})
	if err != nil {
		t.Fatalf("CreateOutputShare: %v", err)
	}
	galKey := extractOutputShareKeyFromURL(t, galShare.URL)
	wfKey := extractShareKeyFromURL(t, wfShare.URL)

	// Prefetch headers & bots MUST NOT mint a cookie or trip rate limiting.
	for _, hdr := range [][2]string{
		{"Sec-Purpose", "prefetch"},
		{"Purpose", "prefetch"},
		{"X-Purpose", "preview"},
		{"X-Moz", "prefetch"},
		{"User-Agent", "TelegramBot"},
	} {
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/gallery?k="+url.QueryEscape(galKey), nil)
		req.Header.Set(hdr[0], hdr[1])
		rec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || len(rec.Result().Cookies()) != 0 {
			t.Fatalf("prefetch %s=%s expected 200 without cookie, got %d cookies=%d", hdr[0], hdr[1], rec.Code, len(rec.Result().Cookies()))
		}
	}

	webCookie := loginForCookie(t, wc.Handler(), host, passcode, nil)
	wfCookie := activateShareCookie(t, wc.Handler(), host, wfKey)
	galCookie := activateOutputShareCookie(t, wc.Handler(), host, galKey)

	// Gallery cookie unlocks GET /gallery (SPA index.html) and /assets/*, but blocks dotfiles.
	spaReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/gallery", nil)
	spaReq.AddCookie(galCookie)
	spaRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(spaRec, spaReq)
	if spaRec.Code != http.StatusOK || !strings.Contains(spaRec.Body.String(), "ColabOne App") {
		t.Fatalf("GET /gallery with valid cookie expected SPA index.html, got %d body=%q", spaRec.Code, spaRec.Body.String())
	}

	assetReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/assets/app.js", nil)
	assetReq.AddCookie(galCookie)
	assetRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(assetRec, assetReq)
	if assetRec.Code != http.StatusOK || !strings.Contains(assetRec.Body.String(), "colabone") {
		t.Fatalf("gallery cookie expected 200 on /assets/app.js, got %d", assetRec.Code)
	}

	dotReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/assets/.hidden", nil)
	dotReq.AddCookie(galCookie)
	dotRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(dotRec, dotReq)
	if dotRec.Code != http.StatusNotFound {
		t.Fatalf("gallery cookie dotfile expected 404, got %d", dotRec.Code)
	}

	// Strict cookie isolation: gallery cookie MUST NOT unlock host console or workflow share endpoints!
	for _, p := range []string{
		"/api/snapshot",
		"/api/web/capabilities",
		"/api/output-shares",
		"/api/outputs/zip?ids=" + outID,
		"/api/share/capabilities",
		"/api/share/snapshot",
	} {
		req := httptest.NewRequest(http.MethodGet, "http://"+host+p, nil)
		req.Header.Set(ClientHeaderName, ClientHeaderValue)
		req.AddCookie(galCookie)
		rec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("gallery cookie on %s expected 401, got %d", p, rec.Code)
		}
	}

	// Host cookie & workflow share cookie MUST NOT unlock /api/gallery/*!
	for _, c := range []*http.Cookie{webCookie, wfCookie} {
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/gallery/manifest", nil)
		req.Header.Set(ClientHeaderName, OutputShareClientHeaderValue)
		req.AddCookie(c)
		rec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("cookie %s on /api/gallery/manifest expected 401, got %d", c.Name, rec.Code)
		}
	}

	// Verify all 6 locales render cleanly on SSR error and unlock pages with brand icon links.
	for _, loc := range []string{"zh-CN", "en-US", "zh-TW", "zh-HK", "ja-JP", "ko-KR"} {
		errHTML := renderColabOneOutputShareErrorPage(loc, outputShareErrExpired, "nonce123")
		if !strings.Contains(errHTML, `lang="`+loc+`"`) || !strings.Contains(errHTML, `href="/favicon.ico"`) || !strings.Contains(errHTML, `href="/apple-touch-icon.png"`) || !strings.Contains(errHTML, colabOneWebIconDataURI()) {
			t.Fatalf("error page for locale %s missing brand/lang invariants", loc)
		}
		unlockHTML := renderColabOneOutputShareUnlockPage(loc, "chal-1", "invalid_password", "nonce456")
		if !strings.Contains(unlockHTML, `lang="`+loc+`"`) || !strings.Contains(unlockHTML, `href="/favicon.ico"`) || !strings.Contains(unlockHTML, `href="/apple-touch-icon.png"`) || !strings.Contains(unlockHTML, colabOneWebIconDataURI()) {
			t.Fatalf("unlock page for locale %s missing brand/lang invariants", loc)
		}
	}

	// Advance clock past 24h TTL: existing gallery cookie immediately expires and renders 410 expired SSR page.
	clock.Advance(25 * time.Hour)
	expReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/gallery", nil)
	expReq.AddCookie(galCookie)
	expRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(expRec, expReq)
	if expRec.Code != http.StatusGone || !strings.Contains(expRec.Body.String(), `data-gallery-error="expired"`) {
		t.Fatalf("expired gallery expected 410 expired error page, got %d body=%s", expRec.Code, expRec.Body.String())
	}
}

func TestOutputSharePasswordUnlockAndRateLimiting(t *testing.T) {
	dir := t.TempDir()
	vault := newMemoryVault()
	clock := newFakeClock(time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC))

	const outID = "dddddddddddddddddddddddddddddddd"

	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  dir,
		Vault:      vault,
		FrontendFS: sampleFrontendFS,
		Now:        clock.Now,
	})
	defer wc.Close()

	share, err := wc.CreateOutputShare(ColabOneOutputShareCreateRequest{
		Title:    "机密商业样片标题",
		Note:     "机密备注",
		Password: "secret-pass",
		TTLMode:  OutputShareTTL7d,
		Groups:   []ColabOneOutputShareGroupInput{{Title: "秘密分组", OutputIDs: []string{outID}}},
	})
	if err != nil {
		t.Fatalf("CreateOutputShare: %v", err)
	}
	shareKey := extractOutputShareKeyFromURL(t, share.URL)
	host := "127.0.0.1:3232"

	// 1) Opening /gallery?k=... renders password prompt WITHOUT leaking title, note, group, or item IDs!
	req := httptest.NewRequest(http.MethodGet, "http://"+host+"/gallery?k="+url.QueryEscape(shareKey), nil)
	req.Host = host
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `data-gallery-unlock="password"`) {
		t.Fatalf("expected 200 password unlock page, got %d body=%s", rec.Code, rec.Body.String())
	}
	for _, secret := range []string{"机密商业样片标题", "机密备注", "秘密分组", outID} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("pre-auth password page leaked %q", secret)
		}
	}
	challenge := extractChallengeFromUnlockHTML(t, rec.Body.String())

	// 2) Cross-origin POST /gallery/unlock rejected with 403.
	crossReq := httptest.NewRequest(http.MethodPost, "http://"+host+OutputShareUnlockRoutePath, strings.NewReader(`{"challenge":"`+challenge+`","password":"secret-pass"}`))
	crossReq.Host = host
	crossReq.RemoteAddr = "10.0.0.1:1235"
	crossReq.Header.Set("Content-Type", "application/json")
	crossReq.Header.Set("Origin", "https://evil.example.com")
	crossRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(crossRec, crossReq)
	if crossRec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin unlock expected 403, got %d", crossRec.Code)
	}

	// 3) Wrong password via HTML form POST re-renders 401 unlock page with error;
	// wrong password via JSON returns 401 JSON; after 5 total failures per IP, 6th hits 429!
	formData := url.Values{"challenge": {challenge}, "password": {"wrong1"}}.Encode()
	formReq := httptest.NewRequest(http.MethodPost, "http://"+host+OutputShareUnlockRoutePath, strings.NewReader(formData))
	formReq.Host = host
	formReq.RemoteAddr = "10.0.0.2:1111"
	formReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	formRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(formRec, formReq)
	if formRec.Code != http.StatusUnauthorized || !strings.Contains(formRec.Body.String(), `data-gallery-unlock="password"`) {
		t.Fatalf("wrong form password expected 401 unlock page, got %d", formRec.Code)
	}

	// 4 more wrong password failures on the same (ip, shareID) -> hits scoped rate limit!
	for i := 0; i < 4; i++ {
		badJSON := httptest.NewRequest(http.MethodPost, "http://"+host+OutputShareUnlockRoutePath, strings.NewReader(`{"challenge":"`+challenge+`","password":"wrong"}`))
		badJSON.Host = host
		badJSON.RemoteAddr = "10.0.0.2:1112"
		badJSON.Header.Set("Content-Type", "application/json")
		badJSON.Header.Set("Accept", "application/json")
		badRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(badRec, badJSON)
		if badRec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong JSON password #%d expected 401, got %d", i+2, badRec.Code)
		}
	}
	// 6th attempt from same IP on the same share -> 429 Too Many Requests with Retry-After!
	rlReq := httptest.NewRequest(http.MethodPost, "http://"+host+OutputShareUnlockRoutePath, strings.NewReader(`{"challenge":"`+challenge+`","password":"secret-pass"}`))
	rlReq.Host = host
	rlReq.RemoteAddr = "10.0.0.2:1114"
	rlReq.Header.Set("Content-Type", "application/json")
	rlReq.Header.Set("Accept", "application/json")
	rlRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(rlRec, rlReq)
	if rlRec.Code != http.StatusTooManyRequests || rlRec.Header().Get("Retry-After") == "" {
		t.Fatalf("expected 429 with Retry-After after 5 failures, got %d retry=%q", rlRec.Code, rlRec.Header().Get("Retry-After"))
	}

	// 4) Correct password from clean IP unlocks with 302 to /gallery and sets colabone_gal_session cookie!
	okForm := url.Values{"challenge": {challenge}, "password": {"secret-pass"}}.Encode()
	okReq := httptest.NewRequest(http.MethodPost, "http://"+host+OutputShareUnlockRoutePath, strings.NewReader(okForm))
	okReq.Host = host
	okReq.RemoteAddr = "10.0.0.3:2222"
	okReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	okRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusFound || okRec.Header().Get("Location") != OutputShareRoutePath {
		t.Fatalf("valid form unlock expected 302 to /gallery, got %d loc=%q body=%s", okRec.Code, okRec.Header().Get("Location"), okRec.Body.String())
	}
	var galCookie *http.Cookie
	for _, c := range okRec.Result().Cookies() {
		if c.Name == OutputShareCookieName {
			galCookie = c
		}
	}
	if galCookie == nil || !galCookie.HttpOnly {
		t.Fatalf("expected HttpOnly %s cookie, got %+v", OutputShareCookieName, galCookie)
	}

	// JSON unlock returns 200 {"ok":true}.
	okJSONReq := httptest.NewRequest(http.MethodPost, "http://"+host+OutputShareUnlockRoutePath, strings.NewReader(`{"challenge":"`+challenge+`","password":"secret-pass"}`))
	okJSONReq.Host = host
	okJSONReq.RemoteAddr = "10.0.0.4:2222"
	okJSONReq.Header.Set("Content-Type", "application/json")
	okJSONReq.Header.Set("Accept", "application/json")
	okJSONRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(okJSONRec, okJSONReq)
	if okJSONRec.Code != http.StatusOK || !strings.Contains(okJSONRec.Body.String(), `"ok":true`) {
		t.Fatalf("valid JSON unlock expected 200 ok, got %d body=%s", okJSONRec.Code, okJSONRec.Body.String())
	}

	// 5) Challenge expires after 5 minutes.
	clock.Advance(6 * time.Minute)
	expChalReq := httptest.NewRequest(http.MethodPost, "http://"+host+OutputShareUnlockRoutePath, strings.NewReader(`{"challenge":"`+challenge+`","password":"secret-pass"}`))
	expChalReq.Host = host
	expChalReq.RemoteAddr = "10.0.0.5:2222"
	expChalReq.Header.Set("Content-Type", "application/json")
	expChalReq.Header.Set("Accept", "application/json")
	expChalRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(expChalRec, expChalReq)
	if expChalRec.Code != http.StatusGone {
		t.Fatalf("expired unlock challenge expected 410, got %d", expChalRec.Code)
	}
}

func TestOutputShareManifestMediaDownloadAndStreamingZip(t *testing.T) {
	dir := t.TempDir()
	vault := newMemoryVault()
	clock := newFakeClock(time.Date(2026, 7, 4, 15, 0, 0, 0, time.UTC))

	const (
		driveAvailID   = "11111111111111111111111111111111"
		inspectID      = "22222222222222222222222222222222"
		driveFSOffID   = "33333333333333333333333333333333"
		unallowedOutID = "44444444444444444444444444444444"
	)

	payload1 := []byte("PNG-DRIVE-BYTES-ONE")
	payload2 := []byte("PNG-INSPECT-BYTES-TWO")

	// GPU is STOPPED, yet Drive-synced output with drive_previewable=true must report available=true!
	liveSnapshot := fmt.Sprintf(`{
		"runtime":{"state":"stopped"},
		"comfy_outputs":[
			{
				"id":"%s",
				"filename":"lookbook_最终版.png",
				"extension":"png",
				"kind":"image",
				"mime":"image/png",
				"size":%d,
				"workflow_id":"fashion-tryon-h3",
				"workflow_name":"AI 模特换装",
				"created_at":1765100000,
				"task_id":"task-a",
				"storage":"drive",
				"drive_synced":true,
				"drive_previewable":true,
				"drive_path":"/My Drive/ColabOne_Drive/private.png",
				"drive_file_id":"fid-private-1",
				"drive_web_view_link":"https://drive.google.com/file/d/fid-private-1/view",
				"cu_consumed":2.4,
				"share_id":"wf-owner-1",
				"prompt_id":"prompt-secret"
			},
			{
				"id":"%s",
				"filename":"00_抠衣质检_外套.png",
				"extension":"png",
				"kind":"image",
				"mime":"image/png",
				"size":%d,
				"workflow_id":"fashion-tryon-h3",
				"workflow_name":"AI 模特换装",
				"created_at":1765100005,
				"task_id":"task-a",
				"storage":"drive",
				"drive_synced":true,
				"drive_previewable":true
			},
			{
				"id":"%s",
				"filename":"offline_drivefs.mp4",
				"extension":"mp4",
				"kind":"video",
				"mime":"video/mp4",
				"size":9999,
				"storage":"drive",
				"drive_synced":true,
				"drive_mode":"colab_drivefs",
				"drive_previewable":false
			}
		]
	}`, driveAvailID, len(payload1), inspectID, len(payload2), driveFSOffID)

	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  dir,
		Vault:      vault,
		FrontendFS: sampleFrontendFS,
		AppVersion: "2.0.0",
		Now:        clock.Now,
		FetchSnapshot: func(ctx context.Context) (string, error) {
			return liveSnapshot, nil
		},
		ServeOutputShareMedia: func(ctx context.Context, rw http.ResponseWriter, r *http.Request, id string) {
			if id == driveAvailID {
				rw.Header().Set("Content-Type", "image/png")
				rw.Header().Set("Content-Disposition", `inline; filename="ColabOne-11111111.png"`)
				rw.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload1)))
				rw.WriteHeader(http.StatusOK)
				if r.Method != http.MethodHead {
					_, _ = rw.Write(payload1)
				}
				return
			}
			writeHeadAwareJSONError(rw, r, http.StatusNotFound, "作品不存在")
		},
		StreamOutputMedia: func(ctx context.Context, id string, w io.Writer, sanitizeErrors bool) (ColabOneOutputStreamMeta, error) {
			switch id {
			case driveAvailID:
				_, err := w.Write(payload1)
				return ColabOneOutputStreamMeta{ID: id, Filename: "lookbook_最终版.png", Extension: "png", Mime: "image/png", Size: int64(len(payload1))}, err
			case inspectID:
				_, err := w.Write(payload2)
				return ColabOneOutputStreamMeta{ID: id, Filename: "00_抠衣质检_外套.png", Extension: "png", Mime: "image/png", Size: int64(len(payload2))}, err
			case driveFSOffID:
				return ColabOneOutputStreamMeta{}, ErrOutputMediaOffline
			default:
				return ColabOneOutputStreamMeta{}, ErrOutputMediaNotFound
			}
		},
	})
	defer wc.Close()

	host := "127.0.0.1:3232"

	// Share 1: AllowDownload = true, 2 groups (to test multi-group ZIP subfolders + ?group= filter).
	dlShare, err := wc.CreateOutputShare(ColabOneOutputShareCreateRequest{
		Title:         "精选样片",
		Note:          "两套方案",
		AllowDownload: true,
		Groups: []ColabOneOutputShareGroupInput{
			{ID: "g-alpha", Title: "方案 A", OutputIDs: []string{driveAvailID, inspectID}},
			{ID: "g-beta", Title: "方案 B", OutputIDs: []string{driveFSOffID, driveAvailID}},
		},
	})
	if err != nil {
		t.Fatalf("CreateOutputShare dlShare: %v", err)
	}
	dlCookie := activateOutputShareCookie(t, wc.Handler(), host, extractOutputShareKeyFromURL(t, dlShare.URL))

	// Manifest without X-ColabOne-Client: web-gallery rejected with 403.
	badHdrReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/gallery/manifest", nil)
	badHdrReq.AddCookie(dlCookie)
	badHdrRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(badHdrRec, badHdrReq)
	if badHdrRec.Code != http.StatusForbidden {
		t.Fatalf("manifest without web-gallery client header expected 403, got %d", badHdrRec.Code)
	}

	// Manifest with valid header returns sanitized groups, Drive offline availability, is_inspect, and ZERO leaks!
	manReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/gallery/manifest", nil)
	manReq.Header.Set(ClientHeaderName, OutputShareClientHeaderValue)
	manReq.AddCookie(dlCookie)
	manRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(manRec, manReq)
	if manRec.Code != http.StatusOK {
		t.Fatalf("manifest expected 200, got %d body=%s", manRec.Code, manRec.Body.String())
	}
	rawMan := manRec.Body.String()
	for _, leak := range []string{"drive_path", "drive_file_id", "drive_web_view_link", "cu_consumed", "wf-owner-1", "prompt-secret", "/My Drive/"} {
		if strings.Contains(rawMan, leak) {
			t.Fatalf("manifest leaked forbidden field/value %q: %s", leak, rawMan)
		}
	}
	var man ColabOneOutputShareManifest
	if err := json.Unmarshal(manRec.Body.Bytes(), &man); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if man.ShareID != dlShare.ID || !man.AllowDownload || len(man.Groups) != 2 || man.AvailableCount != 3 {
		t.Fatalf("unexpected manifest summary: %+v", man)
	}
	if !man.Groups[0].Items[0].Available || man.Groups[0].Items[0].IsInspect {
		t.Fatalf("expected item 0 available=true is_inspect=false: %+v", man.Groups[0].Items[0])
	}
	if !man.Groups[0].Items[1].Available || !man.Groups[0].Items[1].IsInspect {
		t.Fatalf("expected item 1 available=true is_inspect=true: %+v", man.Groups[0].Items[1])
	}
	if man.Groups[1].Items[0].Available {
		t.Fatalf("expected DriveFS-only item available=false: %+v", man.Groups[1].Items[0])
	}

	// Media allowlist enforcement: allowlisted ID succeeds; non-allowlisted ID rejected with 403.
	mediaReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/gallery/outputs/"+driveAvailID+"/media", nil)
	mediaReq.AddCookie(dlCookie)
	mediaRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(mediaRec, mediaReq)
	if mediaRec.Code != http.StatusOK || !bytes.Equal(mediaRec.Body.Bytes(), payload1) || mediaRec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("gallery media failed: code=%d len=%d hdr=%v", mediaRec.Code, mediaRec.Body.Len(), mediaRec.Header())
	}
	denyMediaReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/gallery/outputs/"+unallowedOutID+"/media", nil)
	denyMediaReq.AddCookie(dlCookie)
	denyMediaRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(denyMediaRec, denyMediaReq)
	if denyMediaRec.Code != http.StatusForbidden {
		t.Fatalf("non-allowlisted media expected 403, got %d", denyMediaRec.Code)
	}

	// Single-item download rewrites Content-Disposition to attachment with sanitized UTF-8 filename!
	dlReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/gallery/outputs/"+driveAvailID+"/download", nil)
	dlReq.AddCookie(dlCookie)
	dlRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(dlRec, dlReq)
	if dlRec.Code != http.StatusOK || !bytes.Equal(dlRec.Body.Bytes(), payload1) {
		t.Fatalf("gallery download failed: code=%d len=%d", dlRec.Code, dlRec.Body.Len())
	}
	cd := dlRec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment;") || !strings.Contains(cd, "filename*=UTF-8''") {
		t.Fatalf("expected RFC 5987 attachment Content-Disposition, got %q", cd)
	}

	// Multi-group ZIP download skips unavailable driveFSOffID cleanly and uses group subfolders + zip.Store!
	zipReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/gallery/download.zip", nil)
	zipReq.AddCookie(dlCookie)
	zipRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(zipRec, zipReq)
	if zipRec.Code != http.StatusOK || zipRec.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("gallery ZIP expected 200 application/zip, got %d ct=%q body=%s", zipRec.Code, zipRec.Header().Get("Content-Type"), zipRec.Body.String())
	}
	zr, err := zip.NewReader(bytes.NewReader(zipRec.Body.Bytes()), int64(zipRec.Body.Len()))
	if err != nil {
		t.Fatalf("parse gallery ZIP failed: %v", err)
	}
	if len(zr.File) != 3 {
		t.Fatalf("expected 3 available files in multi-group ZIP (skipping offline item), got %d", len(zr.File))
	}
	gotEntries := map[string][]byte{}
	for _, f := range zr.File {
		if f.Method != zip.Store {
			t.Fatalf("expected zip.Store for %s, got method %d", f.Name, f.Method)
		}
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		gotEntries[f.Name] = b
	}
	if !bytes.Equal(gotEntries["方案 A/lookbook_最终版.png"], payload1) ||
		!bytes.Equal(gotEntries["方案 A/00_抠衣质检_外套.png"], payload2) ||
		!bytes.Equal(gotEntries["方案 B/lookbook_最终版.png"], payload1) {
		t.Fatalf("unexpected ZIP entries: %v", gotEntries)
	}

	// Single-group ZIP download (?group=g-alpha) writes files at top level!
	gZipReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/gallery/download.zip?group=g-alpha", nil)
	gZipReq.AddCookie(dlCookie)
	gZipRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(gZipRec, gZipReq)
	if gZipRec.Code != http.StatusOK {
		t.Fatalf("group ZIP expected 200, got %d", gZipRec.Code)
	}
	gzr, err := zip.NewReader(bytes.NewReader(gZipRec.Body.Bytes()), int64(gZipRec.Body.Len()))
	if err != nil || len(gzr.File) != 2 {
		t.Fatalf("expected 2 top-level files in group ZIP, got err=%v count=%d", err, len(gzr.File))
	}
	for _, f := range gzr.File {
		if strings.Contains(f.Name, "/") {
			t.Fatalf("single-group ZIP should not nest subfolder, got %q", f.Name)
		}
	}

	// Share 2: AllowDownload = false -> single download AND ZIP return 403 "该分享链接未开启下载权限"!
	noDLShare, err := wc.CreateOutputShare(ColabOneOutputShareCreateRequest{
		Title:         "仅预览相册",
		AllowDownload: false,
		Groups:        []ColabOneOutputShareGroupInput{{Title: "G", OutputIDs: []string{driveAvailID}}},
	})
	if err != nil {
		t.Fatalf("CreateOutputShare noDLShare: %v", err)
	}
	noDLCookie := activateOutputShareCookie(t, wc.Handler(), host, extractOutputShareKeyFromURL(t, noDLShare.URL))

	for _, ep := range []string{"/api/gallery/outputs/" + driveAvailID + "/download", "/api/gallery/download.zip"} {
		r := httptest.NewRequest(http.MethodGet, "http://"+host+ep, nil)
		r.AddCookie(noDLCookie)
		w := httptest.NewRecorder()
		wc.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "该分享链接未开启下载权限") {
			t.Fatalf("%s with AllowDownload=false expected 403, got %d body=%s", ep, w.Code, w.Body.String())
		}
	}

	// Share 3: ALL items offline/missing -> download.zip MUST return 503/404 JSON BEFORE writing any ZIP headers!
	offlineOnlyShare, err := wc.CreateOutputShare(ColabOneOutputShareCreateRequest{
		Title:         "离线测试",
		AllowDownload: true,
		Groups:        []ColabOneOutputShareGroupInput{{Title: "G", OutputIDs: []string{driveFSOffID}}},
	})
	if err != nil {
		t.Fatalf("CreateOutputShare offlineOnlyShare: %v", err)
	}
	offCookie := activateOutputShareCookie(t, wc.Handler(), host, extractOutputShareKeyFromURL(t, offlineOnlyShare.URL))
	offZipReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/gallery/download.zip", nil)
	offZipReq.AddCookie(offCookie)
	offZipRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(offZipRec, offZipReq)
	if offZipRec.Code != http.StatusServiceUnavailable || strings.Contains(offZipRec.Header().Get("Content-Type"), "application/zip") {
		t.Fatalf("all-offline ZIP expected 503 JSON before ZIP headers, got %d ct=%q body=%s", offZipRec.Code, offZipRec.Header().Get("Content-Type"), offZipRec.Body.String())
	}

	missingOnlyShare, err := wc.CreateOutputShare(ColabOneOutputShareCreateRequest{
		Title:         "缺失测试",
		AllowDownload: true,
		Groups:        []ColabOneOutputShareGroupInput{{Title: "G", OutputIDs: []string{"99999999999999999999999999999999"}}},
	})
	if err != nil {
		t.Fatalf("CreateOutputShare missingOnlyShare: %v", err)
	}
	missCookie := activateOutputShareCookie(t, wc.Handler(), host, extractOutputShareKeyFromURL(t, missingOnlyShare.URL))
	missZipReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/gallery/download.zip", nil)
	missZipReq.AddCookie(missCookie)
	missZipRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(missZipRec, missZipReq)
	if missZipRec.Code != http.StatusNotFound || strings.Contains(missZipRec.Header().Get("Content-Type"), "application/zip") {
		t.Fatalf("all-missing ZIP expected 404 JSON before ZIP headers, got %d ct=%q body=%s", missZipRec.Code, missZipRec.Header().Get("Content-Type"), missZipRec.Body.String())
	}
}

func TestHostOutputShareRoutesAndOutputsZip(t *testing.T) {
	dir := t.TempDir()
	vault := newMemoryVault()

	const (
		outA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		outB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)

	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  dir,
		Vault:      vault,
		FrontendFS: sampleFrontendFS,
		FetchSnapshot: func(ctx context.Context) (string, error) {
			return fmt.Sprintf(`{"runtime":{"state":"ready"},"comfy_outputs":[
				{"id":"%s","filename":"Same.png","extension":"png","kind":"image","mime":"image/png","size":5},
				{"id":"%s","filename":"same.png","extension":"png","kind":"image","mime":"image/png","size":5}
			]}`, outA, outB), nil
		},
		StreamOutputMedia: func(ctx context.Context, id string, w io.Writer, sanitizeErrors bool) (ColabOneOutputStreamMeta, error) {
			switch id {
			case outA:
				_, err := w.Write([]byte("AAAAA"))
				return ColabOneOutputStreamMeta{ID: id, Filename: "Same.png", Extension: "png", Mime: "image/png", Size: 5}, err
			case outB:
				_, err := w.Write([]byte("BBBBB"))
				return ColabOneOutputStreamMeta{ID: id, Filename: "same.png", Extension: "png", Mime: "image/png", Size: 5}, err
			default:
				return ColabOneOutputStreamMeta{}, ErrOutputMediaNotFound
			}
		},
	})
	defer wc.Close()

	passcode, err := wc.Passcode()
	if err != nil {
		t.Fatalf("Passcode: %v", err)
	}
	host := "127.0.0.1:3232"
	webCookie := loginForCookie(t, wc.Handler(), host, passcode, nil)

	// 1) POST /api/output-shares requires client header + same-origin.
	createBody := fmt.Sprintf(`{"title":"Host Created","allow_download":true,"groups":[{"title":"G","output_ids":["%s"]}]}`, outA)
	noHdrReq := httptest.NewRequest(http.MethodPost, "http://"+host+"/api/output-shares", strings.NewReader(createBody))
	noHdrReq.Host = host
	noHdrReq.AddCookie(webCookie)
	noHdrRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(noHdrRec, noHdrReq)
	if noHdrRec.Code != http.StatusForbidden {
		t.Fatalf("POST /api/output-shares without client header expected 403, got %d", noHdrRec.Code)
	}

	// Works with both "web-console" and "web" client header!
	createReq := httptest.NewRequest(http.MethodPost, "http://"+host+"/api/output-shares", strings.NewReader(createBody))
	createReq.Host = host
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set(ClientHeaderName, "web-console")
	createReq.AddCookie(webCookie)
	createRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("POST /api/output-shares expected 200, got %d body=%s", createRec.Code, createRec.Body.String())
	}
	var created ColabOneOutputShareSummary
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created share: %v", err)
	}

	// 2) GET /api/output-shares lists the share.
	listReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/output-shares", nil)
	listReq.Host = host
	listReq.Header.Set(ClientHeaderName, ClientHeaderValue)
	listReq.AddCookie(webCookie)
	listRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), created.ID) {
		t.Fatalf("GET /api/output-shares expected 200 with created ID, got %d body=%s", listRec.Code, listRec.Body.String())
	}

	// 3) POST /api/output-shares/{id}/revoke revokes it.
	revReq := httptest.NewRequest(http.MethodPost, "http://"+host+"/api/output-shares/"+created.ID+"/revoke", nil)
	revReq.Host = host
	revReq.Header.Set(ClientHeaderName, ClientHeaderValue)
	revReq.AddCookie(webCookie)
	revRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(revRec, revReq)
	if revRec.Code != http.StatusOK || !strings.Contains(revRec.Body.String(), `"status":"revoked"`) {
		t.Fatalf("POST revoke expected 200 revoked, got %d body=%s", revRec.Code, revRec.Body.String())
	}

	// 4) DELETE /api/output-shares/{id} deletes it.
	delReq := httptest.NewRequest(http.MethodDelete, "http://"+host+"/api/output-shares/"+created.ID, nil)
	delReq.Host = host
	delReq.Header.Set(ClientHeaderName, "web-console")
	delReq.AddCookie(webCookie)
	delRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusOK || strings.Contains(delRec.Body.String(), created.ID) {
		t.Fatalf("DELETE expected 200 empty list, got %d body=%s", delRec.Code, delRec.Body.String())
	}

	// 5) GET /api/outputs/zip deduplicates case-colliding filenames (Same.png + same (2).png)!
	zipReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/outputs/zip?ids="+outA+","+outB+"&name=MyAlbum", nil)
	zipReq.Host = host
	zipReq.AddCookie(webCookie)
	zipRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(zipRec, zipReq)
	if zipRec.Code != http.StatusOK || zipRec.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("GET /api/outputs/zip expected 200 zip, got %d ct=%q body=%s", zipRec.Code, zipRec.Header().Get("Content-Type"), zipRec.Body.String())
	}
	if !strings.Contains(zipRec.Header().Get("Content-Disposition"), "MyAlbum.zip") {
		t.Fatalf("expected MyAlbum.zip in Content-Disposition, got %q", zipRec.Header().Get("Content-Disposition"))
	}
	zr, err := zip.NewReader(bytes.NewReader(zipRec.Body.Bytes()), int64(zipRec.Body.Len()))
	if err != nil || len(zr.File) != 2 {
		t.Fatalf("expected 2 entries in host ZIP, got err=%v count=%d", err, len(zr.File))
	}
	if zr.File[0].Name == zr.File[1].Name || strings.EqualFold(zr.File[0].Name, zr.File[1].Name) {
		t.Fatalf("expected case-insensitive deduplicated ZIP names, got %q and %q", zr.File[0].Name, zr.File[1].Name)
	}
}

func TestOutputShareSecurityZipLocaleAndContractHardening(t *testing.T) {
	dir := t.TempDir()
	vault := newMemoryVault()
	clock := newFakeClock(time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC))

	const (
		out1 = "11111111111111111111111111111111"
		out2 = "22222222222222222222222222222222"
		out3 = "33333333333333333333333333333333"
	)

	var (
		streamMu      sync.Mutex
		streamHandler func(ctx context.Context, id string, w io.Writer, sanitizeErrors bool) (ColabOneOutputStreamMeta, error)
	)
	streamHandler = func(ctx context.Context, id string, w io.Writer, sanitizeErrors bool) (ColabOneOutputStreamMeta, error) {
		switch id {
		case out1:
			_, err := w.Write([]byte("PNG-1"))
			return ColabOneOutputStreamMeta{ID: id, Filename: "render.v1", Extension: "png", Mime: "image/png", Size: 5}, err
		case out2:
			_, err := w.Write([]byte("PNG-2"))
			return ColabOneOutputStreamMeta{ID: id, Filename: "rmbg_inspect_01.png", Extension: "png", Mime: "image/png", Size: 5}, err
		default:
			return ColabOneOutputStreamMeta{}, ErrOutputMediaNotFound
		}
	}

	wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
		ConfigDir:  dir,
		Vault:      vault,
		FrontendFS: sampleFrontendFS,
		Now:        clock.Now,
		GetLocale:  func() string { return "zh-CN" },
		FetchSnapshot: func(ctx context.Context) (string, error) {
			return fmt.Sprintf(`{"runtime":{"state":"ready"},"comfy_outputs":[
				{"id":"%s","filename":"render.v1","extension":"png","kind":"image","mime":"image/png","size":5},
				{"id":"%s","filename":"rmbg_inspect_01.png","extension":"png","kind":"image","mime":"image/png","size":5}
			]}`, out1, out2), nil
		},
		ServeOutputShareMedia: func(ctx context.Context, rw http.ResponseWriter, r *http.Request, id string) {
			rw.Header().Set("Content-Type", "image/jpeg; charset=binary")
			rw.WriteHeader(http.StatusOK)
			if r.Method != http.MethodHead {
				_, _ = rw.Write([]byte("JPEG-BYTES"))
			}
		},
		StreamOutputMedia: func(ctx context.Context, id string, w io.Writer, sanitizeErrors bool) (ColabOneOutputStreamMeta, error) {
			streamMu.Lock()
			fn := streamHandler
			streamMu.Unlock()
			return fn(ctx, id, w, sanitizeErrors)
		},
	})
	defer wc.Close()

	passcode, _ := wc.Passcode()
	host := "127.0.0.1:3232"
	webCookie := loginForCookie(t, wc.Handler(), host, passcode, nil)

	// 1) /api/web/capabilities exposes public_origin and bind_mode.
	wc.mu.Lock()
	wc.cfg.PublicOrigin = "https://share.colabone.example"
	wc.cfg.BindMode = BindModeLAN
	wc.mu.Unlock()
	capReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/web/capabilities", nil)
	capReq.Host = host
	capReq.AddCookie(webCookie)
	capRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(capRec, capReq)
	if capRec.Code != http.StatusOK {
		t.Fatalf("/api/web/capabilities expected 200, got %d", capRec.Code)
	}
	var caps map[string]any
	if err := json.Unmarshal(capRec.Body.Bytes(), &caps); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if caps["public_origin"] != "https://share.colabone.example" || caps["bind_mode"] != BindModeLAN {
		t.Fatalf("expected public_origin & bind_mode in capabilities, got %+v", caps)
	}
	wc.mu.Lock()
	wc.cfg.PublicOrigin = ""
	wc.cfg.BindMode = BindModeLoopback
	wc.mu.Unlock()

	// 2) Host private Note MUST NOT appear in guest /api/gallery/manifest JSON!
	//    Also verify isOutputShareInspectFilename matches rmbg_inspect_.
	shareA, err := wc.CreateOutputShare(ColabOneOutputShareCreateRequest{
		Title:         "Public Title",
		Note:          "SECRET-HOST-ONLY-PRIVATE-NOTE",
		TTLMode:       OutputShareTTL24h,
		AllowDownload: true,
		Password:      "AlphaPass1",
		Groups:        []ColabOneOutputShareGroupInput{{ID: "g1", Title: "G1", OutputIDs: []string{out1, out2}}},
	})
	if err != nil {
		t.Fatalf("CreateOutputShare A: %v", err)
	}
	shareB, err := wc.CreateOutputShare(ColabOneOutputShareCreateRequest{
		Title:         "Share B",
		TTLMode:       OutputShareTTL24h,
		AllowDownload: true,
		Password:      "BetaPass2",
		Groups:        []ColabOneOutputShareGroupInput{{ID: "g2", Title: "G2", OutputIDs: []string{out1}}},
	})
	if err != nil {
		t.Fatalf("CreateOutputShare B: %v", err)
	}
	if !isOutputShareInspectFilename("rmbg_inspect_01.png") {
		t.Fatal("expected rmbg_inspect_ to be recognized as inspect filename")
	}

	// 3) Scoped password rate-limit buckets: 5 failures on shareA from 10.0.1.9 MUST NOT lock out shareB on the same IP!
	chalAReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/gallery?k="+url.QueryEscape(extractOutputShareKeyFromURL(t, shareA.URL)), nil)
	chalAReq.Host = host
	chalAReq.RemoteAddr = "10.0.1.9:1001"
	chalARec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(chalARec, chalAReq)
	chalA := extractChallengeFromUnlockHTML(t, chalARec.Body.String())

	chalBReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/gallery?k="+url.QueryEscape(extractOutputShareKeyFromURL(t, shareB.URL)), nil)
	chalBReq.Host = host
	chalBReq.RemoteAddr = "10.0.1.9:1002"
	chalBRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(chalBRec, chalBReq)
	chalB := extractChallengeFromUnlockHTML(t, chalBRec.Body.String())

	for i := 0; i < 5; i++ {
		r := httptest.NewRequest(http.MethodPost, "http://"+host+OutputShareUnlockRoutePath, strings.NewReader(`{"challenge":"`+chalA+`","password":"wrong"}`))
		r.Host = host
		r.RemoteAddr = "10.0.1.9:2000"
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		wc.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("shareA wrong pass #%d expected 401, got %d", i+1, w.Code)
		}
	}
	// Share A is now rate limited on 10.0.1.9, while Share B unlocks immediately!
	okBReq := httptest.NewRequest(http.MethodPost, "http://"+host+OutputShareUnlockRoutePath, strings.NewReader(`{"challenge":"`+chalB+`","password":"BetaPass2"}`))
	okBReq.Host = host
	okBReq.RemoteAddr = "10.0.1.9:2001"
	okBReq.Header.Set("Content-Type", "application/json")
	okBRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(okBRec, okBReq)
	if okBRec.Code != http.StatusOK {
		t.Fatalf("shareB should NOT be locked out by shareA failures on same IP, got %d body=%s", okBRec.Code, okBRec.Body.String())
	}

	// 4) Scrypt concurrency semaphore cap 4 returns 429 + Retry-After: 2 when saturated.
	for i := 0; i < cap(wc.outputShareUnlockSem); i++ {
		wc.outputShareUnlockSem <- struct{}{}
	}
	satReq := httptest.NewRequest(http.MethodPost, "http://"+host+OutputShareUnlockRoutePath, strings.NewReader(`{"challenge":"`+chalB+`","password":"BetaPass2"}`))
	satReq.Host = host
	satReq.RemoteAddr = "10.0.1.10:3000"
	satReq.Header.Set("Content-Type", "application/json")
	satRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(satRec, satReq)
	for i := 0; i < cap(wc.outputShareUnlockSem); i++ {
		<-wc.outputShareUnlockSem
	}
	if satRec.Code != http.StatusTooManyRequests || satRec.Header().Get("Retry-After") != "2" {
		t.Fatalf("saturated scrypt sem expected 429 Retry-After:2, got %d retry=%q", satRec.Code, satRec.Header().Get("Retry-After"))
	}

	// 5) Unlock shareA from clean IP and verify manifest omits "note" completely!
	okAReq := httptest.NewRequest(http.MethodPost, "http://"+host+OutputShareUnlockRoutePath, strings.NewReader(`{"challenge":"`+chalA+`","password":"AlphaPass1"}`))
	okAReq.Host = host
	okAReq.RemoteAddr = "10.0.1.20:3000"
	okAReq.Header.Set("Content-Type", "application/json")
	okARec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(okARec, okAReq)
	if okARec.Code != http.StatusOK {
		t.Fatalf("shareA unlock failed: %d %s", okARec.Code, okARec.Body.String())
	}
	var cookieA *http.Cookie
	for _, c := range okARec.Result().Cookies() {
		if c.Name == OutputShareCookieName {
			cookieA = c
		}
	}
	manReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/gallery/manifest", nil)
	manReq.Host = host
	manReq.Header.Set(ClientHeaderName, OutputShareClientHeaderValue)
	manReq.AddCookie(cookieA)
	manRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(manRec, manReq)
	if manRec.Code != http.StatusOK {
		t.Fatalf("manifest failed: %d", manRec.Code)
	}
	if strings.Contains(manRec.Body.String(), `"note"`) || strings.Contains(manRec.Body.String(), "SECRET-HOST-ONLY-PRIVATE-NOTE") {
		t.Fatalf("manifest leaked host private note: %s", manRec.Body.String())
	}

	// 6) Guest download: appends allowlisted extension (render.v1 -> render.v1.png), rejects cross-site Sec-Fetch-Site.
	crossDL := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/gallery/outputs/"+out1+"/download", nil)
	crossDL.Host = host
	crossDL.Header.Set("Sec-Fetch-Site", "cross-site")
	crossDL.AddCookie(cookieA)
	crossDLRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(crossDLRec, crossDL)
	if crossDLRec.Code != http.StatusForbidden {
		t.Fatalf("cross-site guest download expected 403, got %d", crossDLRec.Code)
	}
	goodDL := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/gallery/outputs/"+out1+"/download", nil)
	goodDL.Host = host
	goodDL.Header.Set("Sec-Fetch-Site", "same-origin")
	goodDL.AddCookie(cookieA)
	goodDLRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(goodDLRec, goodDL)
	if goodDLRec.Code != http.StatusOK || !strings.Contains(goodDLRec.Header().Get("Content-Disposition"), "render.v1.png") {
		t.Fatalf("expected allowlisted .png appended to render.v1, got code=%d cd=%q", goodDLRec.Code, goodDLRec.Header().Get("Content-Disposition"))
	}

	// Infer extension from Content-Type when baseName is extensionless!
	extlessRec := httptest.NewRecorder()
	attW := &attachmentMediaResponseWriter{rw: extlessRec, baseName: "作品封面"}
	attW.Header().Set("Content-Type", "image/jpeg")
	attW.WriteHeader(http.StatusOK)
	attCD := extlessRec.Header().Get("Content-Disposition")
	if !strings.Contains(attCD, `filename="ColabOne-download.jpg"`) || !strings.Contains(attCD, "filename*=UTF-8''") {
		t.Fatalf("expected ASCII fallback + inferred .jpg + UTF-8 filename*, got %q", attCD)
	}

	// 7) HEAD on /api/gallery/download.zip and /api/outputs/zip!
	for _, tc := range []struct {
		url    string
		cookie *http.Cookie
	}{
		{"http://" + host + "/api/gallery/download.zip", cookieA},
		{"http://" + host + "/api/outputs/zip?ids=" + out1 + "," + out2 + "&name=HeadTest", webCookie},
	} {
		hReq := httptest.NewRequest(http.MethodHead, tc.url, nil)
		hReq.Host = host
		hReq.AddCookie(tc.cookie)
		hRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(hRec, hReq)
		if hRec.Code != http.StatusOK || hRec.Header().Get("Content-Type") != "application/zip" || hRec.Body.Len() != 0 {
			t.Fatalf("HEAD %s expected 200 application/zip empty body, got code=%d ct=%q len=%d", tc.url, hRec.Code, hRec.Header().Get("Content-Type"), hRec.Body.Len())
		}
	}
	// HEAD error returns X-ColabOne-Error with empty body.
	badHead := httptest.NewRequest(http.MethodHead, "http://"+host+"/api/outputs/zip?ids=invalid", nil)
	badHead.Host = host
	badHead.AddCookie(webCookie)
	badHeadRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(badHeadRec, badHead)
	if badHeadRec.Code != http.StatusBadRequest || badHeadRec.Body.Len() != 0 || badHeadRec.Header().Get("X-ColabOne-Error") == "" {
		t.Fatalf("HEAD bad /api/outputs/zip expected 400 with X-ColabOne-Error & empty body, got code=%d len=%d hdr=%q", badHeadRec.Code, badHeadRec.Body.Len(), badHeadRec.Header().Get("X-ColabOne-Error"))
	}

	// 8) Guest ZIP semaphore returns 429 when saturated; cross-site rejected with 403.
	crossZip := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/gallery/download.zip", nil)
	crossZip.Host = host
	crossZip.Header.Set("Sec-Fetch-Site", "cross-site")
	crossZip.AddCookie(cookieA)
	crossZipRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(crossZipRec, crossZip)
	if crossZipRec.Code != http.StatusForbidden {
		t.Fatalf("cross-site guest ZIP expected 403, got %d", crossZipRec.Code)
	}
	for i := 0; i < cap(wc.outputShareZipSem); i++ {
		wc.outputShareZipSem <- struct{}{}
	}
	busyZip := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/gallery/download.zip", nil)
	busyZip.Host = host
	busyZip.AddCookie(cookieA)
	busyZipRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(busyZipRec, busyZip)
	for i := 0; i < cap(wc.outputShareZipSem); i++ {
		<-wc.outputShareZipSem
	}
	if busyZipRec.Code != http.StatusTooManyRequests {
		t.Fatalf("saturated guest ZIP sem expected 429, got %d", busyZipRec.Code)
	}

	// 9) Pre-header total known size > 1 GiB returns HTTP 413 cleanly.
	bigItems := []colabOneZipItemSpec{
		{OutputID: out1, Filename: "a.mp4", Extension: "mp4", Size: (600 << 20)},
		{OutputID: out2, Filename: "b.mp4", Extension: "mp4", Size: (600 << 20)},
	}
	bigRec := httptest.NewRecorder()
	bigReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/outputs/zip", nil)
	wc.streamOutputsZipHTTP(bigRec, bigReq, bigItems, "TooBig", false, nil, 0, "")
	if bigRec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for known total > 1 GiB, got %d body=%s", bigRec.Code, bigRec.Body.String())
	}

	// 10) Mid-stream error panics with http.ErrAbortHandler; lastUnavailableErr is NOT overwritten by later 404;
	//     and cancelled context before headers does NOT write misleading 404.
	streamMu.Lock()
	streamHandler = func(ctx context.Context, id string, w io.Writer, sanitizeErrors bool) (ColabOneOutputStreamMeta, error) {
		if id == out1 {
			_, _ = w.Write([]byte("FIRST-CHUNK"))
			return ColabOneOutputStreamMeta{ID: id}, errors.New("mid-stream disconnect")
		}
		return ColabOneOutputStreamMeta{}, ErrOutputMediaNotFound
	}
	streamMu.Unlock()
	var panickedVal any
	func() {
		defer func() { panickedVal = recover() }()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/outputs/zip", nil)
		wc.streamOutputsZipHTTP(rec, req, []colabOneZipItemSpec{{OutputID: out1, Filename: "a.png", Extension: "png"}}, "Abort", false, nil, 0, "")
	}()
	if panickedVal != http.ErrAbortHandler {
		t.Fatalf("expected panic(http.ErrAbortHandler) on mid-stream ZIP error, got %v", panickedVal)
	}

	// Later 404 does NOT overwrite earlier 503 unavailable error.
	streamMu.Lock()
	streamHandler = func(ctx context.Context, id string, w io.Writer, sanitizeErrors bool) (ColabOneOutputStreamMeta, error) {
		if id == out1 {
			return ColabOneOutputStreamMeta{}, ErrOutputMediaOffline
		}
		return ColabOneOutputStreamMeta{}, ErrOutputMediaNotFound
	}
	streamMu.Unlock()
	orderRec := httptest.NewRecorder()
	orderReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/outputs/zip", nil)
	wc.streamOutputsZipHTTP(orderRec, orderReq, []colabOneZipItemSpec{
		{OutputID: out1, Filename: "a.png"},
		{OutputID: out2, Filename: "b.png"},
	}, "Order", true, nil, 0, "")
	if orderRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected earlier 503 preserved over later 404, got %d body=%s", orderRec.Code, orderRec.Body.String())
	}

	// Cancelled context before headers does NOT write 404.
	cancelCtx, cancelFn := context.WithCancel(context.Background())
	cancelFn()
	cancelRec := httptest.NewRecorder()
	cancelReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/outputs/zip", nil).WithContext(cancelCtx)
	wc.streamOutputsZipHTTP(cancelRec, cancelReq, []colabOneZipItemSpec{{OutputID: out1}}, "Cancel", false, nil, 0, "")
	if cancelRec.Body.Len() != 0 {
		t.Fatalf("expected no misleading 404 body on pre-header cancelled context, got code=%d body=%s", cancelRec.Code, cancelRec.Body.String())
	}

	// Mid-ZIP share revocation aborts via http.ErrAbortHandler.
	revCalls := 0
	streamMu.Lock()
	streamHandler = func(ctx context.Context, id string, w io.Writer, sanitizeErrors bool) (ColabOneOutputStreamMeta, error) {
		_, err := w.Write([]byte("ITEM1"))
		return ColabOneOutputStreamMeta{ID: id, Filename: "a.png", Extension: "png"}, err
	}
	streamMu.Unlock()
	panickedVal = nil
	func() {
		defer func() { panickedVal = recover() }()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/gallery/download.zip", nil)
		wc.streamOutputsZipHTTP(rec, req, []colabOneZipItemSpec{
			{OutputID: out1, Filename: "a.png", Extension: "png"},
			{OutputID: out2, Filename: "b.png", Extension: "png"},
		}, "RevokeMid", true, func() bool {
			revCalls++
			return revCalls == 1
		}, 0, "")
	}()
	if panickedVal != http.ErrAbortHandler {
		t.Fatalf("expected http.ErrAbortHandler when share revoked mid-ZIP, got %v", panickedVal)
	}

	// 11) Expanded group-slot cap <= 256 prevents cross-group duplicate amplification!
	dupGroupIDs := make([]string, 64)
	for i := range dupGroupIDs {
		dupGroupIDs[i] = fmt.Sprintf("%032x", i+1)
	}
	if _, _, err := normalizeOutputShareGroups([]ColabOneOutputShareGroupInput{
		{Title: "G1", OutputIDs: dupGroupIDs},
		{Title: "G2", OutputIDs: dupGroupIDs},
		{Title: "G3", OutputIDs: dupGroupIDs},
		{Title: "G4", OutputIDs: dupGroupIDs},
		{Title: "G5", OutputIDs: []string{dupGroupIDs[0]}}, // 257th expanded slot!
	}); err == nil {
		t.Fatal("expected >256 expanded group slots to be rejected")
	}

	// 12) Zip-Slip extension rejection in allocateUniqueEntryPath & UTF-8 byte budget + post-truncation dot/space trimming in sanitizeArchiveSegment!
	zState := &lazyHTTPZipState{usedEntryPaths: make(map[string]bool)}
	safeEntry := zState.allocateUniqueEntryPath("", "photo", out1, "../../evil")
	if strings.Contains(safeEntry, "..") || strings.Contains(safeEntry, "/") {
		t.Fatalf("Zip-Slip extension leaked into entry path: %q", safeEntry)
	}
	cjkLong := strings.Repeat("样", 100) + ".png"
	segCJK := sanitizeArchiveSegment(cjkLong, "fallback")
	if len(segCJK) > maxArchiveSegmentBytes-archiveSegmentHeadroomBytes {
		t.Fatalf("CJK archive segment exceeded UTF-8 byte budget: %d bytes (%q)", len(segCJK), segCJK)
	}
	if !strings.HasSuffix(segCJK, ".png") {
		t.Fatalf("expected .png suffix preserved on truncated CJK segment, got %q", segCJK)
	}
	// Truncation into trailing dots/spaces on non-extension long string must re-trim cleanly.
	dotLong := "abc" + strings.Repeat(".", 120) + "tail"
	segDot := sanitizeArchiveSegment(dotLong, "fallback")
	if strings.HasSuffix(segDot, ".") || strings.HasSuffix(segDot, " ") {
		t.Fatalf("truncated segment kept trailing dot/space: %q", segDot)
	}

	// 13) Distinct challenge-expired error ("验证页已过期，请刷新页面后重试") & sessionLimitError preservation!
	clock.Advance(6 * time.Minute)
	expChalReq := httptest.NewRequest(http.MethodPost, "http://"+host+OutputShareUnlockRoutePath, strings.NewReader(`{"challenge":"`+chalB+`","password":"BetaPass2"}`))
	expChalReq.Host = host
	expChalReq.RemoteAddr = "10.0.1.30:4000"
	expChalReq.Header.Set("Content-Type", "application/json")
	expChalRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(expChalRec, expChalReq)
	if expChalRec.Code != http.StatusGone || !strings.Contains(expChalRec.Body.String(), "验证页已过期，请刷新页面后重试") {
		t.Fatalf("expected distinct challenge-expired message, got code=%d body=%s", expChalRec.Code, expChalRec.Body.String())
	}

	// 14) Guest SSR locale prefers Accept-Language q= weights over host GetLocale() & ko-KR copy polish!
	koReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/gallery?k=co_gal_invalid", nil)
	koReq.Host = host
	koReq.RemoteAddr = "10.0.1.40:4000"
	koReq.Header.Set("Accept-Language", "en-US;q=0.5, ko-KR;q=0.9")
	koRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(koRec, koReq)
	if !strings.Contains(koRec.Body.String(), "결과물 갤러리를 사용할 수 없습니다") {
		t.Fatalf("expected guest SSR page to honor Accept-Language q=0.9 ko-KR over host zh-CN, body=%s", koRec.Body.String())
	}
	if _, koUnlock := resolveOutputShareUnlockCopy("ko-KR"); koUnlock.passwordLabel != "접근 비밀번호" {
		t.Fatalf("expected polished ko-KR passwordLabel, got %q", koUnlock.passwordLabel)
	}

	// 15) Public /gallery?k=... session mint rate limit (20/min -> 21st gets 429).
	pubShare, err := wc.CreateOutputShare(ColabOneOutputShareCreateRequest{
		Title:         "Public Mint Test",
		TTLMode:       OutputShareTTL24h,
		AllowDownload: true,
		Groups:        []ColabOneOutputShareGroupInput{{Title: "G", OutputIDs: []string{out1}}},
	})
	if err != nil {
		t.Fatalf("CreateOutputShare pub: %v", err)
	}
	pubKey := extractOutputShareKeyFromURL(t, pubShare.URL)
	for i := 0; i < outputShareMintRateLimitMaxPerIP; i++ {
		r := httptest.NewRequest(http.MethodGet, "http://"+host+"/gallery?k="+url.QueryEscape(pubKey), nil)
		r.Host = host
		r.RemoteAddr = "10.0.1.99:5000"
		w := httptest.NewRecorder()
		wc.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusFound {
			t.Fatalf("public mint #%d expected 302, got %d", i+1, w.Code)
		}
	}
	mintRL := httptest.NewRequest(http.MethodGet, "http://"+host+"/gallery?k="+url.QueryEscape(pubKey), nil)
	mintRL.Host = host
	mintRL.RemoteAddr = "10.0.1.99:5001"
	mintRLRec := httptest.NewRecorder()
	wc.Handler().ServeHTTP(mintRLRec, mintRL)
	if mintRLRec.Code != http.StatusTooManyRequests {
		t.Fatalf("21st public /gallery?k= mint expected 429, got %d", mintRLRec.Code)
	}

	// 16) Expired share plaintext password pruning.
	clock.Advance(25 * time.Hour)
	wc.pruneExpiredOutputSharePasswords()
	wc.mu.RLock()
	_, stillHasPW := wc.outputSharePasswords[shareA.ID]
	wc.mu.RUnlock()
	if stillHasPW {
		t.Fatal("expected expired shareA plaintext password pruned from memory and vault")
	}

	// 17) workflow_share guestSensitiveTokenRe flags co_gal_ tokens!
	if !containsGuestSensitiveBridgeHint("leaked gallery key co_gal_abcdefghijkl.nonce123456.tag789012") {
		t.Fatal("expected co_gal_ token to be flagged by workflow_share guestSensitiveTokenRe")
	}
}

func TestWebConsoleOutputShareAuditHardening(t *testing.T) {
	// 1) A2 M5: mapAcceptLanguageTag exact/prefix boundary & strict RFC q-value validation.
	t.Run("AcceptLanguageBoundaryAndQValues", func(t *testing.T) {
		if loc, ok := mapAcceptLanguageTag("jav-id"); ok || loc != "" {
			t.Fatalf("expected jav-id NOT to match ja-JP, got %q", loc)
		}
		if loc, ok := mapAcceptLanguageTag("kok-in"); ok || loc != "" {
			t.Fatalf("expected kok-in NOT to match ko-KR, got %q", loc)
		}
		if loc, ok := mapAcceptLanguageTag("ja-jp"); !ok || loc != "ja-JP" {
			t.Fatalf("expected ja-jp -> ja-JP, got %q ok=%v", loc, ok)
		}
		if loc, ok := mapAcceptLanguageTag("ko"); !ok || loc != "ko-KR" {
			t.Fatalf("expected ko -> ko-KR, got %q ok=%v", loc, ok)
		}
		for _, badQ := range []string{"ja;q=NaN,en;q=0.5", "ja;q=Inf,en;q=0.5", "ja;q=+1,en;q=0.5", "ja;q=1e0,en;q=0.5", "ja;q=0x1,en;q=0.5"} {
			if got, ok := localeFromAcceptLanguageWithQ(badQ); !ok || got != "en-US" {
				t.Fatalf("header %q expected en-US (invalid q ignored), got %q ok=%v", badQ, got, ok)
			}
		}
	})

	// 2) A7 M4: zh-HK output share page uses 相簿 instead of 圖集.
	t.Run("ZhHKGalleryTerminologyAndWorkflowGuestLocale", func(t *testing.T) {
		hkErr := colabOneOutputShareErrorCopyByLocale["zh-HK"]
		if strings.Contains(hkErr.sessionLimitHeading, "圖集") || strings.Contains(hkErr.sessionLimitDesc, "圖集") {
			t.Fatalf("expected zh-HK error copy to use 相簿, got %+v", hkErr)
		}
		hkUnlock := colabOneOutputShareUnlockCopyByLocale["zh-HK"]
		if strings.Contains(hkUnlock.sessionLimitError, "圖集") {
			t.Fatalf("expected zh-HK unlock copy to use 相簿, got %+v", hkUnlock)
		}

		// workflow_share error page uses resolveGuestLocale(r) (Accept-Language overrides host zh-CN).
		wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
			ConfigDir: t.TempDir(),
			GetLocale: func() string { return "zh-CN" },
		})
		t.Cleanup(wc.Close)
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/s", nil)
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		rec := httptest.NewRecorder()
		wc.handleWorkflowShareErrorPage(rec, req, http.StatusUnauthorized, workflowShareErrMissing)
		if !strings.Contains(rec.Body.String(), `lang="en-US"`) {
			t.Fatalf("expected workflow share error page to honor guest Accept-Language en-US, got: %s", rec.Body.String()[:min(len(rec.Body.String()), 250)])
		}
	})

	// 3) A5 H1: /api/web/capabilities returns local_url and lan_urls.
	t.Run("CapabilitiesLocalAndLANURLs", func(t *testing.T) {
		wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
			ConfigDir:  t.TempDir(),
			AppVersion: "0.2.50",
		})
		t.Cleanup(wc.Close)
		wc.listInterfaceIPs = func() []netip.Addr {
			return []netip.Addr{netip.MustParseAddr("192.168.1.42")}
		}
		wc.mu.Lock()
		wc.cfg.BindMode = BindModeLAN
		wc.cfg.Port = 18799
		wc.mu.Unlock()
		rec := httptest.NewRecorder()
		wc.handleCapabilities(rec)
		var caps ColabOneWebCapabilities
		if err := json.Unmarshal(rec.Body.Bytes(), &caps); err != nil {
			t.Fatalf("decode capabilities: %v", err)
		}
		if caps.LocalURL != "http://127.0.0.1:18799" || len(caps.LANURLs) != 1 || caps.LANURLs[0] != "http://192.168.1.42:18799" {
			t.Fatalf("unexpected capabilities local/LAN URLs: %+v", caps)
		}
	})

	// 4) A2 L3 & L4: X-ColabOne-Error encodes '+' as '%2B'; Sec-Fetch-Site enforced on /api/outputs/{id}/download & /api/share/outputs/{id}/download.
	t.Run("ErrorHeaderPlusEscapeAndSameOriginDownloads", func(t *testing.T) {
		headReq := httptest.NewRequest(http.MethodHead, "http://127.0.0.1/test", nil)
		rec := httptest.NewRecorder()
		writeHeadAwareJSONError(rec, headReq, http.StatusBadRequest, "1+1 error")
		hdr := rec.Header().Get("X-ColabOne-Error")
		if strings.Contains(hdr, "+") || !strings.Contains(hdr, "%2B") {
			t.Fatalf("expected '+' escaped as %%2B in X-ColabOne-Error, got %q", hdr)
		}

		wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{ConfigDir: t.TempDir()})
		t.Cleanup(wc.Close)
		crossHostDL := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/outputs/11111111111111111111111111111111/download", nil)
		crossHostDL.Header.Set("Sec-Fetch-Site", "cross-site")
		dlRec := httptest.NewRecorder()
		wc.handleOutputDownload(dlRec, crossHostDL, "11111111111111111111111111111111")
		if dlRec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for cross-site /api/outputs/download, got %d", dlRec.Code)
		}
		shareDLRec := httptest.NewRecorder()
		wc.handleShareOutputDownload(shareDLRec, crossHostDL, "share1234567", "11111111111111111111111111111111")
		if shareDLRec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for cross-site /api/share/outputs/download, got %d", shareDLRec.Code)
		}
	})

	// 5) A1 H4: handleOutputDownload handles Drive multi-chunk sha256 transitions (empty to valid, valid to empty).
	t.Run("DriveMultiChunkSHA256TransitionInWebDownload", func(t *testing.T) {
		outID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		part1 := []byte("HELLO-PART-1-")
		part2 := []byte("WORLD-PART-2!")
		full := append(append([]byte(nil), part1...), part2...)
		sum := sha256.Sum256(full)
		fullSHA := hex.EncodeToString(sum[:])

		for _, tc := range []struct {
			name    string
			sha0    string
			sha1    string
		}{
			{"empty_then_valid", "", fullSHA},
			{"valid_then_empty", fullSHA, ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
					ConfigDir: t.TempDir(),
					CallBridge: func(ctx context.Context, method string, payload map[string]any) (string, error) {
						off := int64(payload["offset"].(int64))
						var chunk colabOneWebMediaChunk
						if off == 0 {
							chunk = colabOneWebMediaChunk{
								Data: base64.StdEncoding.EncodeToString(part1), Size: int64(len(full)), Offset: 0,
								SHA256: tc.sha0, Extension: "png", Source: "drive",
							}
						} else {
							chunk = colabOneWebMediaChunk{
								Data: base64.StdEncoding.EncodeToString(part2), Size: int64(len(full)), Offset: off,
								SHA256: tc.sha1, Extension: "png", Source: "drive",
							}
						}
						raw, _ := json.Marshal(chunk)
						return string(raw), nil
					},
				})
				t.Cleanup(wc.Close)
				req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/outputs/"+outID+"/download", nil)
				rec := httptest.NewRecorder()
				wc.handleOutputDownload(rec, req, outID)
				if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), full) {
					t.Fatalf("expected 200 with full bytes on Drive sha256 transition (%s), got code=%d body=%q", tc.name, rec.Code, rec.Body.String())
				}
			})
		}
	})

	// 6) A2 L1 / M1: Non-canonical nonce skipped on load while valid canonical 16-byte nonce loads cleanly.
	t.Run("LoadSharesRejectsNonCanonicalNonce", func(t *testing.T) {
		dir := t.TempDir()
		badAndGoodOutShares := `{"version":1,"shares":[` +
			`{"id":"0123456789a1","nonce":"short","ttl_mode":"7d","groups":[{"id":"g1","output_ids":["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]}]},` +
			`{"id":"0123456789a2","nonce":"AAAAAAAAAAAAAAAAAAAAAA==","ttl_mode":"7d","groups":[{"id":"g1","output_ids":["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]}]},` +
			`{"id":"0123456789a3","nonce":"AAAAAAAAAAAAAAAAAAAAAB","ttl_mode":"7d","groups":[{"id":"g1","output_ids":["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]}]},` +
			`{"id":"0123456789a4","nonce":"AAAAAAAAAAAAAAAAAAAAAA","ttl_mode":"7d","groups":[{"id":"g1","output_ids":["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]}]}` +
			`]}`
		badAndGoodWFShares := `{"version":1,"shares":[` +
			`{"id":"0123456789b1","nonce":"short","ttl_mode":"24h","workflow_ids":["wf1"]},` +
			`{"id":"0123456789b2","nonce":"AAAAAAAAAAAAAAAAAAAAAA==","ttl_mode":"24h","workflow_ids":["wf1"]},` +
			`{"id":"0123456789b3","nonce":"AAAAAAAAAAAAAAAAAAAAAB","ttl_mode":"24h","workflow_ids":["wf1"]},` +
			`{"id":"0123456789b4","nonce":"AAAAAAAAAAAAAAAAAAAAAA","ttl_mode":"24h","workflow_ids":["wf1"]}` +
			`]}`
		if err := os.WriteFile(filepath.Join(dir, OutputSharesFilename), []byte(badAndGoodOutShares), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, WorkflowSharesFilename), []byte(badAndGoodWFShares), 0o600); err != nil {
			t.Fatal(err)
		}
		wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{ConfigDir: dir})
		t.Cleanup(wc.Close)
		outShares := wc.OutputSharesState().Shares
		if len(outShares) != 1 || outShares[0].ID != "0123456789a4" {
			t.Fatalf("expected only valid canonical output share 0123456789a4 loaded, got %+v", outShares)
		}
		wfShares := wc.WorkflowSharesState().Shares
		if len(wfShares) != 1 || wfShares[0].ID != "0123456789b4" {
			t.Fatalf("expected only valid canonical workflow share 0123456789b4 loaded, got %+v", wfShares)
		}
	})

	// 7) A5 H2, A2 L2, A2 M2, A2 M3, A2 M1:
	//    - HEAD /api/gallery/download.zip & /api/outputs/zip concurrency 429
	//    - Offline/frozen oversized item excluded from pre-header 1 GiB 413 check
	//    - POST /gallery/unlock enforces mint rate limit
	//    - outputShareOpMu avoids AB-BA lock inversion with w.opMu
	t.Run("ZipSemaphoresKnownTotalUnlockMintLimitAndOpMuIsolation", func(t *testing.T) {
		dir := t.TempDir()
		v := newMemoryVault()
		clk := newFakeClock(time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC))
		liveID := "11111111111111111111111111111111"
		offlineID := "22222222222222222222222222222222"
		frozenID := "33333333333333333333333333333333"

		snapJSON := fmt.Sprintf(`{"runtime":{"state":"ready"},"comfy_outputs":[`+
			`{"id":%q,"filename":"live.png","extension":"png","mime":"image/png","size":1024,"storage":"runtime"},`+
			`{"id":%q,"filename":"offline.mp4","extension":"mp4","mime":"video/mp4","size":950000000,"storage":"drive","drive_previewable":false}`+
			`]}`, liveID, offlineID)

		wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
			ConfigDir: dir,
			Vault:     v,
			Now:       clk.Now,
			FetchSnapshot: func(ctx context.Context) (string, error) {
				return snapJSON, nil
			},
			StreamOutputMedia: func(ctx context.Context, id string, w io.Writer, sanitizeErrors bool) (ColabOneOutputStreamMeta, error) {
				if id == liveID {
					_, _ = w.Write([]byte("LIVE-PNG-BYTES"))
					return ColabOneOutputStreamMeta{ID: id, Filename: "live.png", Extension: "png", Mime: "image/png", Size: 14}, nil
				}
				return ColabOneOutputStreamMeta{}, ErrOutputMediaOffline
			},
		})
		t.Cleanup(wc.Close)

		share, err := wc.CreateOutputShare(ColabOneOutputShareCreateRequest{
			Title:         "Audit Gallery",
			TTLMode:       OutputShareTTL24h,
			AllowDownload: true,
			Password:      "secret1234",
			Groups: []ColabOneOutputShareGroupInput{
				{Title: "G1", OutputIDs: []string{liveID, offlineID, frozenID}},
			},
		})
		if err != nil {
			t.Fatalf("CreateOutputShare failed: %v", err)
		}
		// Seed huge frozen size on deleted frozenID (950 MB) to verify A2 M3 excludes offline + frozen sizes!
		wc.mu.Lock()
		rec := wc.outputShares[share.ID]
		rec.Items[frozenID] = colabOneOutputShareItemSnapshot{ID: frozenID, Filename: "gone.mp4", Extension: "mp4", Size: 950_000_000}
		wc.outputShares[share.ID] = rec
		wc.mu.Unlock()

		host := fmt.Sprintf("127.0.0.1:%d", DefaultPort)
		shareKey := extractOutputShareKeyFromURL(t, share.URL)

		// GET /gallery?k=... renders unlock page with challenge token.
		entryReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/gallery?k="+url.QueryEscape(shareKey), nil)
		entryReq.Host = host
		entryReq.RemoteAddr = "10.0.9.9:1000"
		entryRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(entryRec, entryReq)
		if entryRec.Code != http.StatusOK {
			t.Fatalf("expected 200 unlock page, got %d", entryRec.Code)
		}
		reChal := regexp.MustCompile(`name="challenge" value="([^"]+)"`)
		mChal := reChal.FindStringSubmatch(entryRec.Body.String())
		if len(mChal) != 2 {
			t.Fatalf("missing challenge input in unlock HTML")
		}
		challenge := mChal[1]

		// A2 M2: POST /gallery/unlock enforces outputShareMintRateLimitMaxPerIP.
		var guestCookie *http.Cookie
		for i := 0; i < outputShareMintRateLimitMaxPerIP; i++ {
			body, _ := json.Marshal(map[string]string{"challenge": challenge, "password": "secret1234"})
			uReq := httptest.NewRequest(http.MethodPost, "http://"+host+OutputShareUnlockRoutePath, bytes.NewReader(body))
			uReq.Host = host
			uReq.RemoteAddr = "10.0.9.9:2000"
			uReq.Header.Set("Content-Type", "application/json")
			uReq.Header.Set("Origin", "http://"+host)
			uRec := httptest.NewRecorder()
			wc.Handler().ServeHTTP(uRec, uReq)
			if uRec.Code != http.StatusOK {
				t.Fatalf("unlock #%d expected 200, got %d body=%s", i+1, uRec.Code, uRec.Body.String())
			}
			for _, c := range uRec.Result().Cookies() {
				if c.Name == OutputShareCookieName {
					guestCookie = c
				}
			}
		}
		bodyOver, _ := json.Marshal(map[string]string{"challenge": challenge, "password": "secret1234"})
		overReq := httptest.NewRequest(http.MethodPost, "http://"+host+OutputShareUnlockRoutePath, bytes.NewReader(bodyOver))
		overReq.Host = host
		overReq.RemoteAddr = "10.0.9.9:2001"
		overReq.Header.Set("Content-Type", "application/json")
		overReq.Header.Set("Origin", "http://"+host)
		overRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(overRec, overReq)
		if overRec.Code != http.StatusTooManyRequests || overRec.Header().Get("Retry-After") == "" {
			t.Fatalf("21st unlock expected 429 with Retry-After, got %d hdr=%q", overRec.Code, overRec.Header().Get("Retry-After"))
		}

		// A2 M3: Gallery ZIP succeeds (200) instead of false 413 despite 1.9 GB of offline+frozen items!
		zipReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/gallery/download.zip", nil)
		zipReq.Host = host
		zipReq.AddCookie(guestCookie)
		zipRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(zipRec, zipReq)
		if zipRec.Code != http.StatusOK {
			t.Fatalf("expected 200 ZIP excluding offline/frozen item sizes from 1 GiB pre-check, got %d body=%s", zipRec.Code, zipRec.Body.String())
		}

		// A5 H2: HEAD /api/gallery/download.zip returns 429 + Retry-After when semaphore saturated.
		for i := 0; i < cap(wc.outputShareZipSem); i++ {
			wc.outputShareZipSem <- struct{}{}
		}
		headZipReq := httptest.NewRequest(http.MethodHead, "http://"+host+"/api/gallery/download.zip", nil)
		headZipReq.Host = host
		headZipReq.AddCookie(guestCookie)
		headZipRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(headZipRec, headZipReq)
		for i := 0; i < cap(wc.outputShareZipSem); i++ {
			<-wc.outputShareZipSem
		}
		if headZipRec.Code != http.StatusTooManyRequests || headZipRec.Header().Get("Retry-After") != "2" {
			t.Fatalf("saturated HEAD /api/gallery/download.zip expected 429 Retry-After:2, got %d hdr=%q", headZipRec.Code, headZipRec.Header().Get("Retry-After"))
		}

		// A2 L2: HEAD /api/outputs/zip returns 429 + Retry-After when hostOutputZipSem saturated.
		for i := 0; i < cap(wc.hostOutputZipSem); i++ {
			wc.hostOutputZipSem <- struct{}{}
		}
		hostHeadZip := httptest.NewRequest(http.MethodHead, "http://"+host+"/api/outputs/zip?ids="+liveID, nil)
		hostHeadZip.Host = host
		hostHeadRec := httptest.NewRecorder()
		wc.handleHostOutputsZip(hostHeadRec, hostHeadZip)
		for i := 0; i < cap(wc.hostOutputZipSem); i++ {
			<-wc.hostOutputZipSem
		}
		if hostHeadRec.Code != http.StatusTooManyRequests || hostHeadRec.Header().Get("Retry-After") != "2" {
			t.Fatalf("saturated HEAD /api/outputs/zip expected 429 Retry-After:2, got %d hdr=%q", hostHeadRec.Code, hostHeadRec.Header().Get("Retry-After"))
		}

		// A2 M1: Holding w.opMu MUST NOT block RevokeOutputShare (no AB-BA deadlock with shutdownHTTPServer).
		wc.opMu.Lock()
		revokeDone := make(chan error, 1)
		go func() {
			_, rErr := wc.RevokeOutputShare(share.ID)
			revokeDone <- rErr
		}()
		select {
		case rErr := <-revokeDone:
			wc.opMu.Unlock()
			if rErr != nil {
				t.Fatalf("RevokeOutputShare while opMu held failed: %v", rErr)
			}
		case <-time.After(2 * time.Second):
			wc.opMu.Unlock()
			t.Fatal("RevokeOutputShare deadlocked on w.opMu")
		}
	})
}

func TestWebConsoleFinalHardeningCloseRacesMediaCORPStoppingAndUnlockForm(t *testing.T) {
	// 1) H1 & H2: persist* and ensure*HMACKeyOpLocked fail closed with errWebConsoleClosed after Close() without touching Vault.
	t.Run("PersistAndHMACKeyFailClosedAfterClose", func(t *testing.T) {
		dir := t.TempDir()
		v := newMemoryVault()
		wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
			ConfigDir: dir,
			Vault:     v,
		})
		wc.Close()

		if err := wc.persistOutputShares(); !errors.Is(err, errWebConsoleClosed) {
			t.Fatalf("persistOutputShares after Close expected errWebConsoleClosed, got %v", err)
		}
		if err := wc.persistOutputSharePasswords(); !errors.Is(err, errWebConsoleClosed) {
			t.Fatalf("persistOutputSharePasswords after Close expected errWebConsoleClosed, got %v", err)
		}
		if err := wc.persistWorkflowShares(); !errors.Is(err, errWebConsoleClosed) {
			t.Fatalf("persistWorkflowShares after Close expected errWebConsoleClosed, got %v", err)
		}

		wc.outputShareOpMu.Lock()
		_, outKeyErr := wc.ensureOutputShareHMACKeyOpLocked()
		wc.outputShareOpMu.Unlock()
		if !errors.Is(outKeyErr, errWebConsoleClosed) {
			t.Fatalf("ensureOutputShareHMACKeyOpLocked after Close expected errWebConsoleClosed, got %v", outKeyErr)
		}

		wc.workflowShareOpMu.Lock()
		_, wfKeyErr := wc.ensureWorkflowShareHMACKeyOpLocked()
		wc.workflowShareOpMu.Unlock()
		if !errors.Is(wfKeyErr, errWebConsoleClosed) {
			t.Fatalf("ensureWorkflowShareHMACKeyOpLocked after Close expected errWebConsoleClosed, got %v", wfKeyErr)
		}

		var dummy string
		if found, _ := v.GetJSON(VaultOutputShareHMACKey, &dummy); found {
			t.Fatal("closed instance must not write output share HMAC key to Vault")
		}
		if found, _ := v.GetJSON(VaultWorkflowShareHMACKey, &dummy); found {
			t.Fatal("closed instance must not write workflow share HMAC key to Vault")
		}

		// Also verify memory-only instance (empty ConfigDir, nil Vault) still returns errWebConsoleClosed.
		memWC := NewColabOneWebConsole(ColabOneWebConsoleOptions{})
		memWC.Close()
		if err := memWC.persistOutputShares(); !errors.Is(err, errWebConsoleClosed) {
			t.Fatalf("memory persistOutputShares after Close expected errWebConsoleClosed, got %v", err)
		}
		if err := memWC.persistOutputSharePasswords(); !errors.Is(err, errWebConsoleClosed) {
			t.Fatalf("memory persistOutputSharePasswords after Close expected errWebConsoleClosed, got %v", err)
		}
		if err := memWC.persistWorkflowShares(); !errors.Is(err, errWebConsoleClosed) {
			t.Fatalf("memory persistWorkflowShares after Close expected errWebConsoleClosed, got %v", err)
		}
	})

	// 2) M3: /media endpoints enforce Sec-Fetch-Site same-origin and emit Cross-Origin-Resource-Policy: same-origin.
	t.Run("MediaEndpointsCORPAndSecFetchSite", func(t *testing.T) {
		outID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		snapJSON := fmt.Sprintf(`{"runtime":{"state":"ready"},"comfy_outputs":[{"id":%q,"filename":"a.png","extension":"png","mime":"image/png","size":10,"storage":"runtime"}]}`, outID)
		var hostCalls, shareCalls, galCalls int
		wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
			ConfigDir: t.TempDir(),
			Vault:     newMemoryVault(),
			FetchSnapshot: func(ctx context.Context) (string, error) {
				return snapJSON, nil
			},
			CallBridge: func(ctx context.Context, method string, payload map[string]any) (string, error) {
				if method == "snapshot" {
					return snapJSON, nil
				}
				return `{"ok":true}`, nil
			},
			ServeOutputMedia: func(ctx context.Context, rw http.ResponseWriter, r *http.Request, id string) {
				hostCalls++
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write([]byte("host-media"))
			},
			ServeShareOutputMedia: func(ctx context.Context, rw http.ResponseWriter, r *http.Request, id string) {
				shareCalls++
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write([]byte("share-media"))
			},
			ServeOutputShareMedia: func(ctx context.Context, rw http.ResponseWriter, r *http.Request, id string) {
				galCalls++
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write([]byte("gallery-media"))
			},
		})
		t.Cleanup(wc.Close)

		outShare, err := wc.CreateOutputShare(ColabOneOutputShareCreateRequest{
			Title:   "Media Gallery",
			TTLMode: OutputShareTTL24h,
			Groups:  []ColabOneOutputShareGroupInput{{Title: "G", OutputIDs: []string{outID}}},
		})
		if err != nil {
			t.Fatalf("CreateOutputShare failed: %v", err)
		}
		wfShare, err := wc.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
			Title:       "Media Workflow",
			TTLMode:     WorkflowShareTTL24h,
			WorkflowIDs: []string{"wf-1"},
		})
		if err != nil {
			t.Fatalf("CreateWorkflowShare failed: %v", err)
		}
		wc.mu.Lock()
		wc.shareOutputOwners[outID] = colabOneShareOutputOwner{ShareID: wfShare.ID, CreatedAt: time.Now().Unix()}
		wc.mu.Unlock()

		// Cross-site blocked with 403 + CORP: same-origin on all three handlers.
		for _, tc := range []struct {
			name string
			call func(rw http.ResponseWriter, r *http.Request)
		}{
			{"host", func(rw http.ResponseWriter, r *http.Request) { wc.handleOutputMedia(rw, r, outID) }},
			{"workflow_share", func(rw http.ResponseWriter, r *http.Request) { wc.handleShareOutputMedia(rw, r, wfShare.ID, outID) }},
			{"output_share", func(rw http.ResponseWriter, r *http.Request) { wc.handleOutputShareMedia(rw, r, outShare.ID, outID) }},
		} {
			req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/media", nil)
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			rec := httptest.NewRecorder()
			tc.call(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s cross-site media expected 403, got %d body=%s", tc.name, rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Cross-Origin-Resource-Policy"); got != "same-origin" {
				t.Fatalf("%s cross-site media missing CORP header, got %q", tc.name, got)
			}

			sameReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/media", nil)
			sameReq.Header.Set("Sec-Fetch-Site", "same-origin")
			sameRec := httptest.NewRecorder()
			tc.call(sameRec, sameReq)
			if sameRec.Code != http.StatusOK {
				t.Fatalf("%s same-origin media expected 200, got %d body=%s", tc.name, sameRec.Code, sameRec.Body.String())
			}
			if got := sameRec.Header().Get("Cross-Origin-Resource-Policy"); got != "same-origin" {
				t.Fatalf("%s same-origin media missing CORP header, got %q", tc.name, got)
			}
		}
		if hostCalls != 1 || shareCalls != 1 || galCalls != 1 {
			t.Fatalf("expected exactly 1 same-origin call per media handler, got host=%d share=%d gal=%d", hostCalls, shareCalls, galCalls)
		}
	})

	// 3) L1: zh-TW sessionLimit copy uses 相簿 consistently.
	t.Run("ZhTWSessionLimitCopy", func(t *testing.T) {
		zhTW := colabOneOutputShareErrorCopyByLocale["zh-TW"]
		if !strings.Contains(zhTW.sessionLimitHeading, "相簿") || strings.Contains(zhTW.sessionLimitHeading, "圖集") {
			t.Fatalf("unexpected zh-TW sessionLimitHeading: %q", zhTW.sessionLimitHeading)
		}
		if !strings.Contains(zhTW.sessionLimitDesc, "相簿") || strings.Contains(zhTW.sessionLimitDesc, "圖集") {
			t.Fatalf("unexpected zh-TW sessionLimitDesc: %q", zhTW.sessionLimitDesc)
		}
	})

	// 4) L3 & L4: stopping flag rejects share entry/switch with 503; unlock ignores URL query params.
	t.Run("StoppingReturns503AndUnlockRejectsQueryParams", func(t *testing.T) {
		clk := newFakeClock(time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC))
		wc := NewColabOneWebConsole(ColabOneWebConsoleOptions{
			ConfigDir: t.TempDir(),
			Vault:     newMemoryVault(),
			Now:       clk.Now,
		})
		t.Cleanup(wc.Close)

		outShare, err := wc.CreateOutputShare(ColabOneOutputShareCreateRequest{
			Title:    "Locked Gallery",
			TTLMode:  OutputShareTTL24h,
			Password: "pw-secret-99",
			Groups:   []ColabOneOutputShareGroupInput{{Title: "G", OutputIDs: []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}},
		})
		if err != nil {
			t.Fatalf("CreateOutputShare failed: %v", err)
		}
		wf1, err := wc.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
			Title: "WF1", TTLMode: WorkflowShareTTL24h, WorkflowIDs: []string{"wf-1"},
		})
		if err != nil {
			t.Fatalf("CreateWorkflowShare wf1 failed: %v", err)
		}
		wf2, err := wc.CreateWorkflowShare(ColabOneWorkflowShareCreateRequest{
			Title: "WF2", TTLMode: WorkflowShareTTL24h, WorkflowIDs: []string{"wf-2"},
		})
		if err != nil {
			t.Fatalf("CreateWorkflowShare wf2 failed: %v", err)
		}

		host := fmt.Sprintf("127.0.0.1:%d", DefaultPort)
		outKey := extractOutputShareKeyFromURL(t, outShare.URL)
		wf1Key := extractOutputShareKeyFromURL(t, wf1.URL)
		wf2Key := extractOutputShareKeyFromURL(t, wf2.URL)

		// Obtain unlock challenge via GET /gallery?k=...
		entryReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/gallery?k="+url.QueryEscape(outKey), nil)
		entryReq.Host = host
		entryReq.RemoteAddr = "10.1.2.3:1001"
		entryRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(entryRec, entryReq)
		reChal := regexp.MustCompile(`name="challenge" value="([^"]+)"`)
		mChal := reChal.FindStringSubmatch(entryRec.Body.String())
		if len(mChal) != 2 {
			t.Fatalf("expected challenge in unlock form, got status=%d", entryRec.Code)
		}
		challenge := mChal[1]

		// L4: Supplying challenge & password ONLY in URL query string with empty POST body MUST fail.
		qReq := httptest.NewRequest(
			http.MethodPost,
			"http://"+host+OutputShareUnlockRoutePath+"?challenge="+url.QueryEscape(challenge)+"&password="+url.QueryEscape("pw-secret-99"),
			strings.NewReader(""),
		)
		qReq.Host = host
		qReq.RemoteAddr = "10.1.2.3:1002"
		qReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		qReq.Header.Set("Origin", "http://"+host)
		qRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(qRec, qReq)
		if qRec.Code == http.StatusOK || qRec.Code == http.StatusFound || qRec.Code == http.StatusSeeOther {
			t.Fatalf("expected unlock via URL query params rejected, got status %d", qRec.Code)
		}
		for _, c := range qRec.Result().Cookies() {
			if c.Name == OutputShareCookieName {
				t.Fatal("unlock via URL query params must not mint session cookie")
			}
		}

		// L4 control: Same credentials in POST body succeed.
		formVals := url.Values{"challenge": {challenge}, "password": {"pw-secret-99"}}
		pReq := httptest.NewRequest(http.MethodPost, "http://"+host+OutputShareUnlockRoutePath, strings.NewReader(formVals.Encode()))
		pReq.Host = host
		pReq.RemoteAddr = "10.1.2.3:1003"
		pReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		pReq.Header.Set("Origin", "http://"+host)
		pRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(pRec, pReq)
		if pRec.Code != http.StatusSeeOther && pRec.Code != http.StatusFound && pRec.Code != http.StatusOK {
			t.Fatalf("expected unlock via POST form body to succeed, got status %d body=%s", pRec.Code, pRec.Body.String())
		}

		// Establish a workflow share session and obtain a switch token before setting w.stopping.
		wfActReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k="+url.QueryEscape(wf1Key), nil)
		wfActReq.Host = host
		wfActReq.RemoteAddr = "10.1.2.3:1004"
		wfActRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(wfActRec, wfActReq)
		var wfCookie *http.Cookie
		for _, c := range wfActRec.Result().Cookies() {
			if c.Name == WorkflowShareCookieName {
				wfCookie = c
			}
		}
		if wfCookie == nil {
			t.Fatalf("expected workflow share session cookie, got status %d", wfActRec.Code)
		}

		swPageReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k="+url.QueryEscape(wf2Key), nil)
		swPageReq.Host = host
		swPageReq.RemoteAddr = "10.1.2.3:1005"
		swPageReq.AddCookie(wfCookie)
		swPageRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(swPageRec, swPageReq)
		reTok := regexp.MustCompile(`name="token" value="([^"]+)"`)
		mTok := reTok.FindStringSubmatch(swPageRec.Body.String())
		if len(mTok) != 2 {
			t.Fatalf("expected switch token in interstitial, got status %d", swPageRec.Code)
		}
		switchToken := mTok[1]

		// L3: Mark stopping and verify entry & switch handlers return 503.
		wc.mu.Lock()
		wc.stopping = true
		wc.mu.Unlock()

		sOutReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/gallery?k="+url.QueryEscape(outKey), nil)
		sOutReq.Host = host
		sOutRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(sOutRec, sOutReq)
		if sOutRec.Code != http.StatusServiceUnavailable {
			t.Fatalf("handleOutputShareEntry while stopping expected 503, got %d", sOutRec.Code)
		}

		sWfReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k="+url.QueryEscape(wf1Key), nil)
		sWfReq.Host = host
		sWfRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(sWfRec, sWfReq)
		if sWfRec.Code != http.StatusServiceUnavailable {
			t.Fatalf("handleWorkflowShareEntry (fresh) while stopping expected 503, got %d", sWfRec.Code)
		}

		sWfSwitchEntryReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/share?k="+url.QueryEscape(wf2Key), nil)
		sWfSwitchEntryReq.Host = host
		sWfSwitchEntryReq.AddCookie(wfCookie)
		sWfSwitchEntryRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(sWfSwitchEntryRec, sWfSwitchEntryReq)
		if sWfSwitchEntryRec.Code != http.StatusServiceUnavailable {
			t.Fatalf("handleWorkflowShareEntry (switch confirm) while stopping expected 503, got %d", sWfSwitchEntryRec.Code)
		}

		sSwReq := httptest.NewRequest(http.MethodPost, "http://"+host+workflowShareSwitchRoutePath, strings.NewReader(url.Values{"token": {switchToken}}.Encode()))
		sSwReq.Host = host
		sSwReq.AddCookie(wfCookie)
		sSwReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		sSwReq.Header.Set("Origin", "http://"+host)
		sSwRec := httptest.NewRecorder()
		wc.Handler().ServeHTTP(sSwRec, sSwReq)
		if sSwRec.Code != http.StatusServiceUnavailable {
			t.Fatalf("handleWorkflowShareSwitch while stopping expected 503, got %d", sSwRec.Code)
		}
	})
}
