package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testAuthCode = "4/valid-example-code-that-is-not-a-real-secret"

// memoryVault is a thread-safe offline VaultStore mirroring Python's MemoryVault.
type memoryVault struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func newMemoryVault() *memoryVault {
	return &memoryVault{data: make(map[string][]byte)}
}

func (v *memoryVault) GetJSON(name string, dest any) (bool, error) {
	v.mu.RLock()
	raw, ok := v.data[name]
	v.mu.RUnlock()
	if !ok || len(raw) == 0 {
		return false, nil
	}
	if dest == nil {
		return true, nil
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return false, err
	}
	return true, nil
}

func (v *memoryVault) Set(name string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	v.mu.Lock()
	v.data[name] = encoded
	v.mu.Unlock()
	return nil
}

func (v *memoryVault) Delete(name string) error {
	v.mu.Lock()
	delete(v.data, name)
	v.mu.Unlock()
	return nil
}

func (v *memoryVault) rawJSON(name string) string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return string(v.data[name])
}

func (v *memoryVault) setRawJSON(name, raw string) {
	v.mu.Lock()
	v.data[name] = []byte(raw)
	v.mu.Unlock()
}

func noSleep(time.Duration) {}

func TestParseAuthorizationReply(t *testing.T) {
	codeWithPlus := "4/valid+plus-example-code-that-is-not-a-real"

	validCases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "raw code",
			in:   testAuthCode,
			want: testAuthCode,
		},
		{
			name: "complete page text",
			in:   "Authorization complete\nCopy this code:\n" + testAuthCode,
			want: testAuthCode,
		},
		{
			name: "callback URL with escaped code",
			in:   "https://localhost/?state=xyz&code=" + url.QueryEscape(testAuthCode),
			want: testAuthCode,
		},
		{
			name: "raw code with plus sign preserved",
			in:   codeWithPlus,
			want: codeWithPlus,
		},
		{
			name: "callback URL with percent-encoded plus sign",
			in:   "http://localhost:8080/?state=abc&code=" + url.QueryEscape(codeWithPlus),
			want: codeWithPlus,
		},
		{
			name: "duplicate identical code deduplicated",
			in:   testAuthCode + "\n" + testAuthCode,
			want: testAuthCode,
		},
		{
			name: "pasted text containing stray percent sign",
			in:   "100% done! Code: " + testAuthCode,
			want: testAuthCode,
		},
	}

	for _, tc := range validCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseAuthorizationReply(tc.in)
			if err != nil {
				t.Fatalf("ParseAuthorizationReply(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseAuthorizationReply(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	invalidCases := []struct {
		name string
		in   string
	}{
		{name: "empty", in: ""},
		{name: "not a code", in: "not a code"},
		{name: "too short", in: "4/short-code"},
		{name: "ambiguous two codes", in: testAuthCode + "\n4/another-code-long-enough-to-be-ambiguous"},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseAuthorizationReply(tc.in)
			if !errors.Is(err, ErrInvalidAuthReply) {
				t.Fatalf("ParseAuthorizationReply(%q) error = %v, want ErrInvalidAuthReply", tc.in, err)
			}
			wantMsg := "未识别到唯一的 Google 授权码。请粘贴授权页全部文字、回调链接或授权码。"
			if err.Error() != wantMsg {
				t.Fatalf("error text = %q, want %q", err.Error(), wantMsg)
			}
		})
	}
}

func TestPublicScopesAndOAuthConstants(t *testing.T) {
	hasScope := func(target string) bool {
		for _, s := range PUBLIC_SCOPES {
			if s == target {
				return true
			}
		}
		return false
	}

	for _, required := range []string{
		"openid",
		"https://www.googleapis.com/auth/userinfo.profile",
		"https://www.googleapis.com/auth/userinfo.email",
		"https://www.googleapis.com/auth/cloud-platform",
		"https://www.googleapis.com/auth/colaboratory",
		"https://www.googleapis.com/auth/drive.file",
	} {
		if !hasScope(required) {
			t.Fatalf("PUBLIC_SCOPES missing %q", required)
		}
	}

	if !strings.HasPrefix(COLABONE_OAUTH_CLIENT_ID, COLABONE_GCP_ADC_CLIENT_PREFIX) {
		t.Fatalf("unexpected client_id %q", COLABONE_OAUTH_CLIENT_ID)
	}
	if COLABONE_OAUTH_CLIENT_SECRET == "" || COLABONE_OAUTH_AUTH_URI == "" || COLABONE_OAUTH_TOKEN_URI == "" {
		t.Fatal("embedded OAuth config constants must be non-empty")
	}
	if REMOTE_REDIRECT_URI != "https://sdk.cloud.google.com/applicationdefaultauthcode.html" {
		t.Fatalf("unexpected REMOTE_REDIRECT_URI: %q", REMOTE_REDIRECT_URI)
	}

	expectedSnippets := map[string][2]string{
		"zh-CN": {"登录成功", "重试"},
		"en-US": {"Signed in to ColabOne", "try again"},
		"zh-TW": {"返回用戶端", "重試"},
		"zh-HK": {"返回應用程式", "再試"},
		"ja-JP": {"ログインが完了しました", "再試行"},
		"ko-KR": {"로그인을 완료했습니다", "다시 시도"},
	}
	if len(OAuthCallbackTexts) != len(expectedSnippets) {
		t.Fatalf("OAuthCallbackTexts has %d locales, want %d", len(OAuthCallbackTexts), len(expectedSnippets))
	}
	for loc, snippets := range expectedSnippets {
		got, ok := OAuthCallbackTexts[loc]
		if !ok {
			t.Fatalf("missing locale %q in OAuthCallbackTexts", loc)
		}
		if !strings.Contains(got.Success, snippets[0]) {
			t.Fatalf("locale %q success text %q missing %q", loc, got.Success, snippets[0])
		}
		if !strings.Contains(got.Error, snippets[1]) {
			t.Fatalf("locale %q error text %q missing %q", loc, got.Error, snippets[1])
		}
	}
}

func TestColabOneCredentialsPythonJSONParity(t *testing.T) {
	// Exact payload format produced by Python google.oauth2.credentials.Credentials.to_json().
	pythonSaved := `{
		"token": "ya29.python-access-token",
		"refresh_token": "1//python-refresh-token",
		"token_uri": "https://oauth2.googleapis.com/token",
		"client_id": "764086051850-6qr4p6gpi6hn506pt8ejuq83di341hur.apps.googleusercontent.com",
		"client_secret": "d-FL95Q19q7MQmFpd7hHD0Ty",
		"scopes": [
			"openid",
			"https://www.googleapis.com/auth/colaboratory",
			"https://www.googleapis.com/auth/drive.file"
		],
		"universe_domain": "googleapis.com",
		"account": "",
		"expiry": "2099-01-01T00:00:00.123456Z"
	}`

	vault := newMemoryVault()
	vault.setRawJSON("colab", pythonSaved)
	auth := NewColabOneAuth(vault)

	creds, err := auth.Credentials(context.Background(), "colab")
	if err != nil {
		t.Fatalf("Credentials() unexpected error: %v", err)
	}
	if creds.Token != "ya29.python-access-token" || creds.RefreshToken != "1//python-refresh-token" {
		t.Fatalf("unexpected credentials loaded: %+v", creds)
	}
	if creds.UniverseDomain != "googleapis.com" {
		t.Fatalf("UniverseDomain = %q, want googleapis.com", creds.UniverseDomain)
	}
	if len(creds.Scopes) != 3 {
		t.Fatalf("Scopes = %v, want 3 entries", creds.Scopes)
	}

	// Space-delimited string scopes and naive UTC expiry must also unmarshal cleanly.
	var stringScopeCreds ColabOneCredentials
	if err := json.Unmarshal([]byte(`{
		"token": "tok",
		"refresh_token": "ref",
		"scopes": "openid https://www.googleapis.com/auth/drive.file",
		"expiry": "2026-11-23T12:00:00"
	}`), &stringScopeCreds); err != nil {
		t.Fatalf("UnmarshalJSON string scopes error: %v", err)
	}
	if len(stringScopeCreds.Scopes) != 2 || stringScopeCreds.Scopes[1] != "https://www.googleapis.com/auth/drive.file" {
		t.Fatalf("parsed scopes = %v", stringScopeCreds.Scopes)
	}

	// Verify 60-second expiry skew boundary.
	base := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	if stringScopeCreds.ValidAt(base.Add(-59 * time.Second)) {
		t.Fatal("expected invalid when 59s remaining (within 60s skew)")
	}
	if stringScopeCreds.ValidAt(base.Add(-60 * time.Second)) {
		t.Fatal("expected invalid at exact 60s skew boundary")
	}
	if !stringScopeCreds.ValidAt(base.Add(-61 * time.Second)) {
		t.Fatal("expected valid when 61s remaining")
	}
	noExpiry := &ColabOneCredentials{Token: "tok", RefreshToken: "ref"}
	if noExpiry.ValidAt(base) {
		t.Fatal("expected invalid when expiry is missing")
	}
}

func TestCredentialsRefreshOnceAcrossConcurrentRequests(t *testing.T) {
	now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	var refreshCalls atomic.Int32

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCalls.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm failed: %v", err)
		}
		if got := r.FormValue("grant_type"); got != "refresh_token" {
			t.Errorf("grant_type = %q, want refresh_token", got)
		}
		if got := r.FormValue("refresh_token"); got != "saved-refresh" {
			t.Errorf("refresh_token = %q, want saved-refresh", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","expires_in":3600,"token_type":"Bearer"}`))
	}))
	defer tokenSrv.Close()

	vault := newMemoryVault()
	_ = vault.Set("colab", &ColabOneCredentials{
		Token:        "expired-access",
		RefreshToken: "saved-refresh",
		ClientID:     COLABONE_OAUTH_CLIENT_ID,
		ClientSecret: COLABONE_OAUTH_CLIENT_SECRET,
		Expiry:       now.Add(-5 * time.Minute).Format(time.RFC3339),
	})

	auth := NewColabOneAuth(
		vault,
		WithTokenURI(tokenSrv.URL),
		WithNowFn(func() time.Time { return now }),
		WithHTTPClient(tokenSrv.Client()),
	)

	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	tokens := make(chan string, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			creds, err := auth.Credentials(context.Background(), "colab")
			if err != nil {
				errs <- err
				return
			}
			tokens <- creds.Token
		}()
	}
	wg.Wait()
	close(errs)
	close(tokens)

	for err := range errs {
		t.Fatalf("concurrent Credentials() error: %v", err)
	}
	for tok := range tokens {
		if tok != "new-access" {
			t.Fatalf("token = %q, want new-access", tok)
		}
	}
	if got := refreshCalls.Load(); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}

	var stored ColabOneCredentials
	ok, err := vault.GetJSON("colab", &stored)
	if err != nil || !ok {
		t.Fatalf("vault reload failed: ok=%v err=%v", ok, err)
	}
	if stored.RefreshToken != "saved-refresh" {
		t.Fatalf("refresh_token lost after refresh: %q", stored.RefreshToken)
	}
	if !stored.ValidAt(now) {
		t.Fatal("refreshed credentials should be valid")
	}

	// A new client instance restores the same persistent authorization without refreshing.
	auth2 := NewColabOneAuth(
		vault,
		WithTokenURI(tokenSrv.URL),
		WithNowFn(func() time.Time { return now }),
		WithHTTPClient(tokenSrv.Client()),
	)
	creds2, err := auth2.Credentials(context.Background(), "colab")
	if err != nil {
		t.Fatalf("reloaded Credentials() error: %v", err)
	}
	if creds2.Token != "new-access" || refreshCalls.Load() != 1 {
		t.Fatalf("unexpected reload refresh: token=%q calls=%d", creds2.Token, refreshCalls.Load())
	}
}

func TestCredentialsRefreshErrorHandlingPreservesTokens(t *testing.T) {
	now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)

	t.Run("missing login", func(t *testing.T) {
		auth := NewColabOneAuth(newMemoryVault())
		_, err := auth.Credentials(context.Background(), "colab")
		if !errors.Is(err, ErrNotSignedIn) {
			t.Fatalf("err = %v, want ErrNotSignedIn", err)
		}
	})

	t.Run("missing refresh token", func(t *testing.T) {
		vault := newMemoryVault()
		_ = vault.Set("colab", &ColabOneCredentials{
			Token:  "expired",
			Expiry: now.Add(-time.Hour).Format(time.RFC3339),
		})
		auth := NewColabOneAuth(vault, WithNowFn(func() time.Time { return now }))
		_, err := auth.Credentials(context.Background(), "colab")
		if !errors.Is(err, ErrRefreshTokenMissing) {
			t.Fatalf("err = %v, want ErrRefreshTokenMissing", err)
		}
	})

	t.Run("network error preserves saved login", func(t *testing.T) {
		vault := newMemoryVault()
		_ = vault.Set("colab", &ColabOneCredentials{
			Token:        "expired",
			RefreshToken: "saved-ref",
			ClientID:     COLABONE_OAUTH_CLIENT_ID,
			ClientSecret: COLABONE_OAUTH_CLIENT_SECRET,
			Expiry:       now.Add(-time.Hour).Format(time.RFC3339),
		})
		before := vault.rawJSON("colab")

		deadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		deadURL := deadSrv.URL
		deadSrv.Close()

		auth := NewColabOneAuth(
			vault,
			WithTokenURI(deadURL),
			WithNowFn(func() time.Time { return now }),
		)
		_, err := auth.Credentials(context.Background(), "colab")
		if !errors.Is(err, ErrRefreshRetryable) {
			t.Fatalf("err = %v, want ErrRefreshRetryable", err)
		}
		if vault.rawJSON("colab") != before {
			t.Fatal("vault entry must be preserved on network error")
		}
	})

	t.Run("5xx and retryable oauth error return ErrRefreshRetryable", func(t *testing.T) {
		cases := []struct {
			status int
			body   string
		}{
			{http.StatusServiceUnavailable, `{"error":"service_unavailable"}`},
			{http.StatusTooManyRequests, `{"error":"rate_limit"}`},
			{http.StatusBadRequest, `{"error":"temporarily_unavailable"}`},
		}
		for _, tc := range cases {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			vault := newMemoryVault()
			_ = vault.Set("colab", &ColabOneCredentials{
				Token:        "expired",
				RefreshToken: "saved-ref",
				Expiry:       now.Add(-time.Hour).Format(time.RFC3339),
			})
			before := vault.rawJSON("colab")

			auth := NewColabOneAuth(
				vault,
				WithTokenURI(srv.URL),
				WithNowFn(func() time.Time { return now }),
				WithHTTPClient(srv.Client()),
			)
			_, err := auth.Credentials(context.Background(), "colab")
			srv.Close()

			if !errors.Is(err, ErrRefreshRetryable) {
				t.Fatalf("status=%d body=%s: err=%v, want ErrRefreshRetryable", tc.status, tc.body, err)
			}
			if vault.rawJSON("colab") != before {
				t.Fatalf("status=%d: vault modified on retryable error", tc.status)
			}
		}
	})

	t.Run("invalid_grant returns ErrAuthExpired without erasing vault", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`))
		}))
		defer srv.Close()

		vault := newMemoryVault()
		_ = vault.Set("colab", &ColabOneCredentials{
			Token:        "expired",
			RefreshToken: "revoked-ref",
			Expiry:       now.Add(-time.Hour).Format(time.RFC3339),
		})
		before := vault.rawJSON("colab")

		auth := NewColabOneAuth(
			vault,
			WithTokenURI(srv.URL),
			WithNowFn(func() time.Time { return now }),
			WithHTTPClient(srv.Client()),
		)
		_, err := auth.Credentials(context.Background(), "colab")
		if !errors.Is(err, ErrAuthExpired) {
			t.Fatalf("err = %v, want ErrAuthExpired", err)
		}
		if vault.rawJSON("colab") != before {
			t.Fatal("vault entry should not be erased by Credentials()")
		}
	})
}

func TestFetchProfileRetryAndProfileCaching(t *testing.T) {
	var attempts atomic.Int32
	userInfoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if r.Header.Get("Authorization") != "Bearer valid-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"email":"alice@example.com","sub":"sub-alice"}`))
	}))
	defer userInfoSrv.Close()

	vault := newMemoryVault()
	_ = vault.Set("colab", &ColabOneCredentials{
		Token:        "valid-tok",
		RefreshToken: "ref-tok",
		Expiry:       "2099-01-01T00:00:00Z",
	})

	auth := NewColabOneAuth(
		vault,
		WithUserInfoURL(userInfoSrv.URL),
		WithHTTPClient(userInfoSrv.Client()),
		WithSleepFn(noSleep),
	)

	prof, err := auth.Profile(context.Background())
	if err != nil {
		t.Fatalf("Profile() error: %v", err)
	}
	if prof["email"] != "alice@example.com" || prof["sub"] != "sub-alice" {
		t.Fatalf("unexpected profile: %v", prof)
	}
	if attempts.Load() != 2 {
		t.Fatalf("expected 2 attempts (1 retry after 503), got %d", attempts.Load())
	}

	// Second call hits vault cache with zero HTTP calls.
	prof2, err := auth.Profile(context.Background())
	if err != nil {
		t.Fatalf("second Profile() error: %v", err)
	}
	if prof2["sub"] != "sub-alice" || attempts.Load() != 2 {
		t.Fatalf("expected cached profile without network call: prof=%v attempts=%d", prof2, attempts.Load())
	}
}

func TestColabBeginAndColabFinishPKCEAndAccountSwitch(t *testing.T) {
	var seenVerifier string
	var seenRedirectURI string
	var seenCode string
	var currentSub = "sub-account-1"
	var currentEmail = "one@example.com"

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		seenVerifier = r.FormValue("code_verifier")
		seenRedirectURI = r.FormValue("redirect_uri")
		seenCode = r.FormValue("code")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"access_token": "acc-1",
			"refresh_token": "ref-1",
			"expires_in": "3600",
			"token_type": "Bearer"
		}`))
	}))
	defer tokenSrv.Close()

	userInfoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"email": currentEmail,
			"sub":   currentSub,
		})
	}))
	defer userInfoSrv.Close()

	vault := newMemoryVault()
	auth := NewColabOneAuth(
		vault,
		WithTokenURI(tokenSrv.URL),
		WithUserInfoURL(userInfoSrv.URL),
		WithHTTPClient(tokenSrv.Client()),
		WithSleepFn(noSleep),
	)

	// Calling ColabFinish before ColabBegin fails cleanly.
	if _, err := auth.ColabFinish(context.Background(), testAuthCode); !errors.Is(err, ErrLoginNotStarted) {
		t.Fatalf("ColabFinish before ColabBegin err = %v, want ErrLoginNotStarted", err)
	}

	// Active runtime session blocks account switching.
	_ = vault.Set("runtime_session_ids", []string{"sess-1"})
	if _, err := auth.ColabBegin(); !errors.Is(err, ErrActiveSessionsExist) {
		t.Fatalf("ColabBegin with active session err = %v, want ErrActiveSessionsExist", err)
	}
	// Empty slice is not an active session.
	_ = vault.Set("runtime_session_ids", []string{})

	beginRes, err := auth.ColabBegin()
	if err != nil {
		t.Fatalf("ColabBegin() error: %v", err)
	}
	parsedURL, err := url.Parse(beginRes["url"])
	if err != nil {
		t.Fatalf("invalid authorization url %q: %v", beginRes["url"], err)
	}
	q := parsedURL.Query()
	if q.Get("redirect_uri") != REMOTE_REDIRECT_URI {
		t.Fatalf("redirect_uri = %q, want %q", q.Get("redirect_uri"), REMOTE_REDIRECT_URI)
	}
	if q.Get("prompt") != "select_account consent" {
		t.Fatalf("prompt = %q", q.Get("prompt"))
	}
	if q.Get("token_usage") != "remote" {
		t.Fatalf("token_usage = %q", q.Get("token_usage"))
	}
	if q.Get("access_type") != "offline" {
		t.Fatalf("access_type = %q", q.Get("access_type"))
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Fatalf("code_challenge_method = %q", q.Get("code_challenge_method"))
	}
	challenge := q.Get("code_challenge")
	if challenge == "" {
		t.Fatal("missing PKCE code_challenge")
	}

	// Pre-seed cloud_project for an earlier account to verify deletion on account switch.
	_ = vault.Set("google_profile", map[string]string{"sub": "sub-old", "email": "old@example.com"})
	_ = vault.Set("cloud_project", "colabone-drive-oldproj123")

	finishRes, err := auth.ColabFinish(context.Background(), "Paste:\n"+testAuthCode)
	if err != nil {
		t.Fatalf("ColabFinish() error: %v", err)
	}
	if finishRes["ok"] != true {
		t.Fatalf("unexpected finishRes: %v", finishRes)
	}
	if auth.HasPendingFlow() {
		t.Fatal("expected flow cleared after ColabFinish")
	}

	// Verify PKCE S256 verifier matches challenge.
	sum := sha256.Sum256([]byte(seenVerifier))
	wantChallenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if wantChallenge != challenge {
		t.Fatalf("PKCE S256 mismatch: verifier hash %q != challenge %q", wantChallenge, challenge)
	}
	if seenRedirectURI != REMOTE_REDIRECT_URI || seenCode != testAuthCode {
		t.Fatalf("token exchange params: redirect_uri=%q code=%q", seenRedirectURI, seenCode)
	}

	// cloud_project must have been deleted because sub changed.
	if _, ok := func() (string, bool) {
		var s string
		ok, _ := vault.GetJSON("cloud_project", &s)
		return s, ok
	}(); ok {
		t.Fatal("cloud_project must be deleted when Google account sub changes")
	}

	// Re-authenticating the SAME account preserves cloud_project.
	_ = vault.Set("cloud_project", "colabone-drive-sameacct12")
	if _, err := auth.ColabBegin(); err != nil {
		t.Fatalf("second ColabBegin error: %v", err)
	}
	if _, err := auth.ColabFinish(context.Background(), testAuthCode); err != nil {
		t.Fatalf("second ColabFinish error: %v", err)
	}
	var keptProject string
	if ok, _ := vault.GetJSON("cloud_project", &keptProject); !ok || keptProject != "colabone-drive-sameacct12" {
		t.Fatalf("cloud_project should be preserved for same sub, got ok=%v val=%q", ok, keptProject)
	}
}

func TestColabLoginLoopbackStateSecurityHeadersAndFavicon(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("code") != testAuthCode {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.FormValue("code_verifier") == "" {
			t.Error("loopback flow missing PKCE code_verifier")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"loopback-tok","refresh_token":"loopback-ref","expires_in":3600}`))
	}))
	defer tokenSrv.Close()

	userInfoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"email":"demo@example.com","sub":"sub-demo"}`))
	}))
	defer userInfoSrv.Close()

	vault := newMemoryVault()
	var renderedLocale, renderedMsg, renderedEmail string
	var renderedOK bool
	customRenderer := func(locale string, ok bool, message, email string) string {
		renderedLocale = locale
		renderedOK = ok
		renderedMsg = message
		renderedEmail = email
		return fmt.Sprintf(`<html lang="%s"><body class="is-success">%s|%s<button id="co-close-btn"></button></body></html>`, locale, message, email)
	}

	auth := NewColabOneAuth(
		vault,
		WithTokenURI(tokenSrv.URL),
		WithUserInfoURL(userInfoSrv.URL),
		WithHTTPClient(tokenSrv.Client()),
		WithOAuthPageRenderer(customRenderer),
		WithSleepFn(noSleep),
	)
	auth.SetLocale("zh-CN")

	type cbResult struct {
		faviconStatus int
		badStateCode  int
		okStatus      int
		headers       http.Header
		body          string
		wasActive     bool
	}
	resCh := make(chan cbResult, 1)

	err := auth.ColabLogin(context.Background(), func(authURL string) {
		go func() {
			var res cbResult
			res.wasActive = auth.LoopbackActive()
			u, err := url.Parse(authURL)
			if err != nil {
				t.Errorf("parse authURL: %v", err)
				resCh <- res
				return
			}
			redirectURI := u.Query().Get("redirect_uri")
			state := u.Query().Get("state")

			client := &http.Client{Timeout: 3 * time.Second}

			// 1) /favicon.ico returns 204 + Cache-Control: no-store
			favResp, err := client.Get(redirectURI + "favicon.ico")
			if err == nil {
				res.faviconStatus = favResp.StatusCode
				_ = favResp.Body.Close()
			}

			// 2) Wrong state returns 400 Invalid OAuth state
			badResp, err := client.Get(redirectURI + "?state=wrong&code=" + url.QueryEscape(testAuthCode))
			if err == nil {
				res.badStateCode = badResp.StatusCode
				_ = badResp.Body.Close()
			}

			// 3) Valid state + code completes login with 200 and security headers
			okResp, err := client.Get(redirectURI + "?state=" + url.QueryEscape(state) + "&code=" + url.QueryEscape(testAuthCode))
			if err == nil {
				res.okStatus = okResp.StatusCode
				res.headers = okResp.Header.Clone()
				b, _ := io.ReadAll(okResp.Body)
				res.body = string(b)
				_ = okResp.Body.Close()
			}
			resCh <- res
		}()
	})
	if err != nil {
		t.Fatalf("ColabLogin() error: %v", err)
	}
	if auth.LoopbackActive() {
		t.Fatal("LoopbackActive() must be false after ColabLogin returns")
	}
	if auth.HasPendingFlow() {
		t.Fatal("flow must be cleared after successful ColabLogin")
	}

	res := <-resCh
	if !res.wasActive {
		t.Fatal("LoopbackActive() should be true inside readyCallback")
	}
	if res.faviconStatus != http.StatusNoContent {
		t.Fatalf("favicon status = %d, want 204", res.faviconStatus)
	}
	if res.badStateCode != http.StatusBadRequest {
		t.Fatalf("bad state status = %d, want 400", res.badStateCode)
	}
	if res.okStatus != http.StatusOK {
		t.Fatalf("valid callback status = %d, want 200", res.okStatus)
	}
	if got := res.headers.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := res.headers.Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want no-referrer", got)
	}
	if got := res.headers.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := res.headers.Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'none'") {
		t.Fatalf("Content-Security-Policy = %q", got)
	}
	if !renderedOK || renderedLocale != "zh-CN" || renderedEmail != "demo@example.com" {
		t.Fatalf("unexpected renderer args: ok=%v locale=%q email=%q msg=%q", renderedOK, renderedLocale, renderedEmail, renderedMsg)
	}
	if !strings.Contains(res.body, "demo@example.com") || !strings.Contains(res.body, "co-close-btn") {
		t.Fatalf("unexpected callback HTML: %q", res.body)
	}

	var stored ColabOneCredentials
	ok, _ := vault.GetJSON("colab", &stored)
	if !ok || stored.Token != "loopback-tok" || stored.RefreshToken != "loopback-ref" {
		t.Fatalf("stored credentials = %+v", stored)
	}
}

func TestColabLoginCancelledAndFallbackXSSSafeRenderer(t *testing.T) {
	vault := newMemoryVault()
	auth := NewColabOneAuth(vault)
	auth.SetLocale("en-US")

	bodyCh := make(chan string, 1)
	err := auth.ColabLogin(context.Background(), func(authURL string) {
		go func() {
			u, _ := url.Parse(authURL)
			redirectURI := u.Query().Get("redirect_uri")
			state := u.Query().Get("state")
			resp, err := http.Get(redirectURI + "?state=" + url.QueryEscape(state) + "&error=access_denied")
			if err == nil {
				b, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				bodyCh <- string(b)
			} else {
				bodyCh <- ""
			}
		}()
	})
	if !errors.Is(err, ErrAuthCancelled) {
		t.Fatalf("err = %v, want ErrAuthCancelled", err)
	}
	body := <-bodyCh
	if !strings.Contains(body, `lang="en-US"`) || !strings.Contains(body, `class="is-error"`) {
		t.Fatalf("unexpected fallback error HTML: %q", body)
	}

	escaped := defaultRenderOAuthPage("en-US", true, `ok <tag>`, `user+<script>@example.com`)
	if strings.Contains(escaped, "<script>@") || !strings.Contains(escaped, "&lt;script&gt;@example.com") {
		t.Fatalf("fallback renderer did not escape HTML: %q", escaped)
	}
}

func TestColabLoginManualPasteCompletesActiveLoopback(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"manual-tok","refresh_token":"manual-ref","expires_in":3600}`))
	}))
	defer tokenSrv.Close()

	userInfoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"email":"manual@example.com","sub":"sub-manual"}`))
	}))
	defer userInfoSrv.Close()

	vault := newMemoryVault()
	auth := NewColabOneAuth(
		vault,
		WithTokenURI(tokenSrv.URL),
		WithUserInfoURL(userInfoSrv.URL),
		WithHTTPClient(tokenSrv.Client()),
		WithSleepFn(noSleep),
	)

	// Simulate user switching to manual flow (ColabBegin + ColabFinish) while ColabLogin is waiting.
	readyCh := make(chan struct{})
	loginErrCh := make(chan error, 1)
	go func() {
		loginErrCh <- auth.ColabLogin(context.Background(), func(_ string) {
			close(readyCh)
		})
	}()

	<-readyCh
	if !auth.LoopbackActive() {
		t.Fatal("expected LoopbackActive() == true")
	}
	if _, err := auth.ColabBegin(); err != nil {
		t.Fatalf("ColabBegin during loopback failed: %v", err)
	}
	if _, err := auth.ColabFinish(context.Background(), testAuthCode); err != nil {
		t.Fatalf("ColabFinish during loopback failed: %v", err)
	}

	select {
	case err := <-loginErrCh:
		if err != nil {
			t.Fatalf("ColabLogin returned error after manual completion: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ColabLogin did not wake up after manual ColabFinish")
	}

	if auth.LoopbackActive() {
		t.Fatal("LoopbackActive() should be false after completion")
	}
	var creds ColabOneCredentials
	ok, _ := vault.GetJSON("colab", &creds)
	if !ok || creds.Token != "manual-tok" {
		t.Fatalf("expected manual-tok saved, got %+v", creds)
	}
}

func TestColabLoginTimeoutAndContextCancellation(t *testing.T) {
	t.Run("timeout returns ErrLoginTimeout", func(t *testing.T) {
		auth := NewColabOneAuth(
			newMemoryVault(),
			WithLoopbackTimeout(60*time.Millisecond),
		)
		err := auth.ColabLogin(context.Background(), func(_ string) {})
		if !errors.Is(err, ErrLoginTimeout) {
			t.Fatalf("err = %v, want ErrLoginTimeout", err)
		}
		if auth.LoopbackActive() {
			t.Fatal("LoopbackActive() should be false after timeout")
		}
	})

	t.Run("context cancel exits promptly", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		auth := NewColabOneAuth(newMemoryVault())
		err := auth.ColabLogin(ctx, func(_ string) {
			cancel()
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if auth.LoopbackActive() {
			t.Fatal("LoopbackActive() should be false after cancellation")
		}
	})
}

func TestBuildRcloneConfAndQuotaProjectDirective(t *testing.T) {
	vault := newMemoryVault()
	saved := &ColabOneCredentials{
		Token:        "access-tok",
		RefreshToken: "refresh-tok",
		ClientID:     "764086051850-6qr4p.apps.googleusercontent.com",
		ClientSecret: "csecret",
		Expiry:       "2099-01-01T00:00:00Z",
		Scopes: []string{
			"openid",
			"https://www.googleapis.com/auth/colaboratory",
			"https://www.googleapis.com/auth/drive.file",
		},
	}
	_ = vault.Set("colab", saved)
	_ = vault.Set("cloud_project", "colabone-drive-abcdef1234")

	auth := NewColabOneAuth(vault)
	if !auth.HasDriveScope() {
		t.Fatal("expected HasDriveScope() == true")
	}
	if !auth.RequiresDriveQuotaProject() {
		t.Fatal("expected RequiresDriveQuotaProject() == true")
	}

	conf, err := auth.BuildRcloneConf(context.Background(), nil)
	if err != nil {
		t.Fatalf("BuildRcloneConf() error: %v", err)
	}
	for _, want := range []string{
		"# colabone_quota_project = colabone-drive-abcdef1234",
		"[colabone_gdrive]",
		"type = drive",
		"client_id = 764086051850-6qr4p.apps.googleusercontent.com",
		"client_secret = csecret",
		"scope = drive.file",
		`"access_token":"access-tok"`,
		`"token_type":"Bearer"`,
		`"refresh_token":"refresh-tok"`,
		`"expiry":"2099-01-01T00:00:00Z"`,
	} {
		if !strings.Contains(conf, want) {
			t.Fatalf("rclone conf missing %q:\n%s", want, conf)
		}
	}

	// Explicit empty quota project pointer suppresses quota project header.
	empty := ""
	confNoQP, err := auth.BuildRcloneConf(context.Background(), &empty)
	if err != nil {
		t.Fatalf("BuildRcloneConf(empty) error: %v", err)
	}
	if strings.Contains(confNoQP, "colabone_quota_project") {
		t.Fatalf("expected no quota project directive when explicit empty string passed:\n%s", confNoQP)
	}

	// Full /drive scope takes precedence over drive.file.
	savedFull := saved.Clone()
	savedFull.Scopes = append(savedFull.Scopes, "https://www.googleapis.com/auth/drive")
	_ = vault.Set("colab", savedFull)
	confFull, err := auth.BuildRcloneConf(context.Background(), nil)
	if err != nil {
		t.Fatalf("BuildRcloneConf(full drive) error: %v", err)
	}
	if !strings.Contains(confFull, "scope = drive\n") {
		t.Fatalf("expected scope = drive:\n%s", confFull)
	}

	// Token lacking drive scope returns empty config rather than broken remote.
	savedNoDrive := saved.Clone()
	savedNoDrive.Scopes = []string{"openid"}
	_ = vault.Set("colab", savedNoDrive)
	if auth.HasDriveScope() {
		t.Fatal("expected HasDriveScope() == false")
	}
	confEmpty, err := auth.BuildRcloneConf(context.Background(), nil)
	if err != nil || confEmpty != "" {
		t.Fatalf("expected empty conf without drive scope, got conf=%q err=%v", confEmpty, err)
	}
}

func TestEnsureDriveQuotaProjectToSExistingAnd409Retry(t *testing.T) {
	makeADCVault := func(sub string) *memoryVault {
		vault := newMemoryVault()
		_ = vault.Set("colab", &ColabOneCredentials{
			Token:        "access-tok",
			RefreshToken: "refresh-tok",
			ClientID:     "764086051850-6qr4p.apps.googleusercontent.com",
			ClientSecret: "csecret",
			Expiry:       "2099-01-01T00:00:00Z",
			Scopes: []string{
				"https://www.googleapis.com/auth/cloud-platform",
				"https://www.googleapis.com/auth/drive.file",
			},
		})
		_ = vault.Set("google_profile", map[string]string{
			"sub":   sub,
			"email": sub + "@example.com",
		})
		return vault
	}

	t.Run("non-ADC client returns not_required", func(t *testing.T) {
		vault := newMemoryVault()
		_ = vault.Set("colab", &ColabOneCredentials{
			Token:        "access-tok",
			RefreshToken: "refresh-tok",
			ClientID:     "custom-client.apps.googleusercontent.com",
			Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		})
		auth := NewColabOneAuth(vault)
		pid, reason := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
		if pid != "" || reason != "not_required" {
			t.Fatalf("got (%q, %q), want (\"\", \"not_required\")", pid, reason)
		}
		st := auth.DriveQuotaStatus()
		if st["needs_quota"] != false || st["ready"] != true || st["reason"] != "not_required" {
			t.Fatalf("unexpected DriveQuotaStatus: %v", st)
		}
	})

	t.Run("missing cloud-platform scope returns no_cloud_scope", func(t *testing.T) {
		vault := newMemoryVault()
		_ = vault.Set("colab", &ColabOneCredentials{
			Token:        "access-tok",
			RefreshToken: "refresh-tok",
			ClientID:     "764086051850-6qr4p.apps.googleusercontent.com",
			Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		})
		auth := NewColabOneAuth(vault)
		pid, reason := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
		if pid != "" || reason != "no_cloud_scope" {
			t.Fatalf("got (%q, %q), want (\"\", \"no_cloud_scope\")", pid, reason)
		}
	})

	t.Run("ToS not accepted returns gcp_tos_required and caches negative result", func(t *testing.T) {
		now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
		var postCalls atomic.Int32
		crmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/v1/projects":
				_, _ = w.Write([]byte(`{}`))
			case r.Method == http.MethodPost && r.URL.Path == "/v1/projects":
				postCalls.Add(1)
				_, _ = w.Write([]byte(`{"name":"operations/create_project.1"}`))
			case r.Method == http.MethodGet && r.URL.Path == "/v1/operations/create_project.1":
				_, _ = w.Write([]byte(`{"done":true,"error":{"code":9,"message":"Callers must accept Terms of Service"}}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer crmSrv.Close()

		vault := makeADCVault("user-123")
		auth := NewColabOneAuth(
			vault,
			WithCloudResourceManagerBaseURL(crmSrv.URL),
			WithServiceUsageBaseURL(crmSrv.URL),
			WithHTTPClient(crmSrv.Client()),
			WithSleepFn(noSleep),
			WithNowFn(func() time.Time { return now }),
		)

		pid, reason := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
		if pid != "" || reason != "gcp_tos_required" {
			t.Fatalf("got (%q, %q), want (\"\", \"gcp_tos_required\")", pid, reason)
		}

		// Second call hits 600s negative cache without extra HTTP calls.
		pid2, reason2 := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
		if pid2 != "" || reason2 != "gcp_tos_required" {
			t.Fatalf("cached got (%q, %q), want (\"\", \"gcp_tos_required\")", pid2, reason2)
		}
		if postCalls.Load() != 1 {
			t.Fatalf("postCalls = %d, want 1", postCalls.Load())
		}
		st := auth.DriveQuotaStatus()
		if st["ready"] != false || st["reason"] != "gcp_tos_required" {
			t.Fatalf("unexpected DriveQuotaStatus: %v", st)
		}

		// Advancing clock past 600s bypasses negative cache.
		now = now.Add(601 * time.Second)
		_, _ = auth.EnsureDriveQuotaProject(context.Background(), nil, false)
		if postCalls.Load() != 2 {
			t.Fatalf("expected re-probe after negative cache TTL, postCalls = %d", postCalls.Load())
		}
	})

	t.Run("reuses existing active project read-only when drive API enabled", func(t *testing.T) {
		var postCount atomic.Int32
		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				postCount.Add(1)
			}
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/v1/projects":
				_, _ = w.Write([]byte(`{"projects":[{"projectId":"my-existing-proj","lifecycleState":"ACTIVE"}]}`))
			case "/v1/projects/my-existing-proj/services/drive.googleapis.com":
				if r.Header.Get("X-Goog-User-Project") != "my-existing-proj" {
					t.Errorf("missing X-Goog-User-Project header")
				}
				_, _ = w.Write([]byte(`{"state":"ENABLED"}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer apiSrv.Close()

		vault := makeADCVault("user-exist")
		auth := NewColabOneAuth(
			vault,
			WithCloudResourceManagerBaseURL(apiSrv.URL),
			WithServiceUsageBaseURL(apiSrv.URL),
			WithHTTPClient(apiSrv.Client()),
			WithSleepFn(noSleep),
		)

		pid, reason := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
		if pid != "my-existing-proj" || reason != "reused_existing_project" {
			t.Fatalf("got (%q, %q), want (\"my-existing-proj\", \"reused_existing_project\")", pid, reason)
		}
		if postCount.Load() != 0 {
			t.Fatalf("must not mutate unrelated projects, saw %d POST calls", postCount.Load())
		}

		// Subsequent call hits vault cache.
		pid2, reason2 := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
		if pid2 != "my-existing-proj" || reason2 != "cached" {
			t.Fatalf("cached got (%q, %q), want (\"my-existing-proj\", \"cached\")", pid2, reason2)
		}
	})

	t.Run("reuses existing colabone-drive project and enables Drive API if needed", func(t *testing.T) {
		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/v1/projects":
				_, _ = w.Write([]byte(`{"projects":[
					{"projectId":"other-user-proj","lifecycleState":"ACTIVE"},
					{"projectId":"colabone-drive-1234567890","lifecycleState":"ACTIVE"}
				]}`))
			case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/colabone-drive-1234567890/services/drive.googleapis.com":
				_, _ = w.Write([]byte(`{"state":"DISABLED"}`))
			case r.Method == http.MethodPost && r.URL.Path == "/v1/projects/colabone-drive-1234567890/services/drive.googleapis.com:enable":
				_, _ = w.Write([]byte(`{"done":true}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer apiSrv.Close()

		vault := makeADCVault("user-reuse")
		auth := NewColabOneAuth(
			vault,
			WithCloudResourceManagerBaseURL(apiSrv.URL),
			WithServiceUsageBaseURL(apiSrv.URL),
			WithHTTPClient(apiSrv.Client()),
			WithSleepFn(noSleep),
		)

		pid, reason := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
		if pid != "colabone-drive-1234567890" || reason != "reused_colabone_project" {
			t.Fatalf("got (%q, %q), want (\"colabone-drive-1234567890\", \"reused_colabone_project\")", pid, reason)
		}
	})

	t.Run("retries 409 conflict with v2 id and retries 404 enable propagation", func(t *testing.T) {
		var createAttempt atomic.Int32
		var enableAttempt atomic.Int32
		var createdIDs []string
		var mu sync.Mutex

		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/v1/projects":
				_, _ = w.Write([]byte(`{}`))
			case r.Method == http.MethodPost && r.URL.Path == "/v1/projects":
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				mu.Lock()
				createdIDs = append(createdIDs, body["projectId"])
				mu.Unlock()
				if createAttempt.Add(1) == 1 {
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`ALREADY_EXISTS`))
					return
				}
				_, _ = w.Write([]byte(`{"name":"operations/create.v2"}`))
			case r.Method == http.MethodGet && r.URL.Path == "/v1/operations/create.v2":
				_, _ = w.Write([]byte(`{"done":true}`))
			case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/services/drive.googleapis.com:enable"):
				if enableAttempt.Add(1) == 1 {
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`not found`))
					return
				}
				_, _ = w.Write([]byte(`{"name":"operations/enable.1"}`))
			case r.Method == http.MethodGet && r.URL.Path == "/v1/operations/enable.1":
				_, _ = w.Write([]byte(`{"done":true}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer apiSrv.Close()

		vault := makeADCVault("user-409")
		auth := NewColabOneAuth(
			vault,
			WithCloudResourceManagerBaseURL(apiSrv.URL),
			WithServiceUsageBaseURL(apiSrv.URL),
			WithHTTPClient(apiSrv.Client()),
			WithSleepFn(noSleep),
		)

		initialStatus := auth.DriveQuotaStatus()
		if initialStatus["needs_quota"] != true || initialStatus["ready"] != false || initialStatus["project_id"] != "" || initialStatus["reason"] != "" {
			t.Fatalf("unexpected initial DriveQuotaStatus: %v", initialStatus)
		}

		type logEntry struct {
			msg, source, level string
		}
		var logs []logEntry
		pid, reason := auth.EnsureDriveQuotaProject(context.Background(), func(msg, source, level string) {
			logs = append(logs, logEntry{msg, source, level})
		}, true)

		wantV1 := COLABONE_QUOTA_PROJECT_PREFIX + shortSHA256Hex("colabone-drive:user-409", 10)
		wantV2 := COLABONE_QUOTA_PROJECT_PREFIX + shortSHA256Hex("colabone-drive:v2:user-409", 10)
		if pid != wantV2 || reason != "created_colabone_project" {
			t.Fatalf("got (%q, %q), want (%q, \"created_colabone_project\")", pid, reason, wantV2)
		}
		if len(createdIDs) != 2 || createdIDs[0] != wantV1 || createdIDs[1] != wantV2 {
			t.Fatalf("createdIDs = %v, want [%s %s]", createdIDs, wantV1, wantV2)
		}
		if len(logs) != 2 || logs[0].source != "云盘" || logs[1].source != "云盘" {
			t.Fatalf("unexpected logs: %+v", logs)
		}
		if !strings.Contains(logs[1].msg, wantV2) {
			t.Fatalf("completion log %q missing project id %q", logs[1].msg, wantV2)
		}

		finalStatus := auth.DriveQuotaStatus()
		if finalStatus["needs_quota"] != true || finalStatus["ready"] != true || finalStatus["project_id"] != wantV2 || finalStatus["reason"] != "ready" {
			t.Fatalf("unexpected final DriveQuotaStatus: %v", finalStatus)
		}
	})

	t.Run("account switch mid-flight returns account_changed without polluting vault", func(t *testing.T) {
		vault := makeADCVault("sub-A")
		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/v1/projects":
				// Simulate user switching account while GCP list request is in flight.
				_ = vault.Set("google_profile", map[string]string{"sub": "sub-B", "email": "b@example.com"})
				_, _ = w.Write([]byte(`{"projects":[{"projectId":"acct-a-proj","lifecycleState":"ACTIVE"}]}`))
			case "/v1/projects/acct-a-proj/services/drive.googleapis.com":
				_, _ = w.Write([]byte(`{"state":"ENABLED"}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer apiSrv.Close()

		auth := NewColabOneAuth(
			vault,
			WithCloudResourceManagerBaseURL(apiSrv.URL),
			WithServiceUsageBaseURL(apiSrv.URL),
			WithHTTPClient(apiSrv.Client()),
			WithSleepFn(noSleep),
		)

		pid, reason := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
		if pid != "" || reason != "account_changed" {
			t.Fatalf("got (%q, %q), want (\"\", \"account_changed\")", pid, reason)
		}
		var stored string
		if ok, _ := vault.GetJSON("cloud_project", &stored); ok {
			t.Fatalf("cloud_project must not be persisted after account switch, got %q", stored)
		}
	})

	t.Run("transient 503 returns network_error without negative caching", func(t *testing.T) {
		var listCalls atomic.Int32
		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			listCalls.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer apiSrv.Close()

		vault := makeADCVault("user-503")
		auth := NewColabOneAuth(
			vault,
			WithCloudResourceManagerBaseURL(apiSrv.URL),
			WithServiceUsageBaseURL(apiSrv.URL),
			WithHTTPClient(apiSrv.Client()),
			WithSleepFn(noSleep),
		)

		pid1, reason1 := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
		pid2, reason2 := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
		if pid1 != "" || reason1 != "network_error" || pid2 != "" || reason2 != "network_error" {
			t.Fatalf("got (%q,%q) and (%q,%q), want network_error", pid1, reason1, pid2, reason2)
		}
		if listCalls.Load() != 2 {
			t.Fatalf("transient network_error must not be negative-cached; listCalls = %d, want 2", listCalls.Load())
		}
	})

	t.Run("list HTTP 403 ToS error and InvalidateDriveQuotaProject resets caches", func(t *testing.T) {
		var listCalls atomic.Int32
		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/v1/projects":
				if listCalls.Add(1) == 1 {
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"error":{"message":"User must accept Google Cloud Terms of Service"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"projects":[{"projectId":"colabone-drive-abcde12345","lifecycleState":"ACTIVE"}]}`))
			case "/v1/projects/colabone-drive-abcde12345/services/drive.googleapis.com":
				_, _ = w.Write([]byte(`{"state":"ENABLED"}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer apiSrv.Close()

		vault := makeADCVault("user-tos-list")
		auth := NewColabOneAuth(
			vault,
			WithCloudResourceManagerBaseURL(apiSrv.URL),
			WithServiceUsageBaseURL(apiSrv.URL),
			WithHTTPClient(apiSrv.Client()),
			WithSleepFn(noSleep),
		)

		pid, reason := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
		if pid != "" || reason != "gcp_tos_required" {
			t.Fatalf("got (%q, %q), want (\"\", \"gcp_tos_required\")", pid, reason)
		}

		// InvalidateDriveQuotaProject clears negative cache so next call re-probes.
		auth.InvalidateDriveQuotaProject()
		pid2, reason2 := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
		if pid2 != "colabone-drive-abcde12345" || reason2 != "reused_colabone_project" {
			t.Fatalf("after invalidate got (%q, %q), want (\"colabone-drive-abcde12345\", \"reused_colabone_project\")", pid2, reason2)
		}
		if listCalls.Load() != 2 {
			t.Fatalf("listCalls = %d, want 2", listCalls.Load())
		}
	})

	t.Run("falls back without X-Goog-User-Project when Service Usage is disabled on new project", func(t *testing.T) {
		var withQuotaEnableCalls atomic.Int32
		var noQuotaEnableCalls atomic.Int32
		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/v1/projects":
				_, _ = w.Write([]byte(`{"projects":[{"projectId":"colabone-drive-2090a4cd4c","lifecycleState":"ACTIVE"}]}`))
			case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/colabone-drive-2090a4cd4c/services/drive.googleapis.com":
				if r.Header.Get("X-Goog-User-Project") != "" {
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"Service Usage API has not been used in project colabone-drive-2090a4cd4c before or it is disabled.","details":[{"reason":"SERVICE_DISABLED","metadata":{"service":"serviceusage.googleapis.com"}}]}}`))
					return
				}
				_, _ = w.Write([]byte(`{"state":"DISABLED"}`))
			case r.Method == http.MethodPost && r.URL.Path == "/v1/projects/colabone-drive-2090a4cd4c/services/drive.googleapis.com:enable":
				bodyBytes, _ := io.ReadAll(r.Body)
				if strings.TrimSpace(string(bodyBytes)) != "{}" {
					t.Errorf("expected {} JSON body on :enable POST, got %q", string(bodyBytes))
				}
				if r.Header.Get("X-Goog-User-Project") != "" {
					withQuotaEnableCalls.Add(1)
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"Service Usage API has not been used in project colabone-drive-2090a4cd4c before or it is disabled.","details":[{"reason":"SERVICE_DISABLED","metadata":{"service":"serviceusage.googleapis.com"}}]}}`))
					return
				}
				noQuotaEnableCalls.Add(1)
				_, _ = w.Write([]byte(`{"done":true}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer apiSrv.Close()

		vault := makeADCVault("user-su-disabled")
		auth := NewColabOneAuth(
			vault,
			WithCloudResourceManagerBaseURL(apiSrv.URL),
			WithServiceUsageBaseURL(apiSrv.URL),
			WithHTTPClient(apiSrv.Client()),
			WithSleepFn(noSleep),
		)

		pid, reason := auth.EnsureDriveQuotaProject(context.Background(), nil, true)
		if pid != "colabone-drive-2090a4cd4c" || reason != "reused_colabone_project" {
			t.Fatalf("got (%q, %q), want (\"colabone-drive-2090a4cd4c\", \"reused_colabone_project\")", pid, reason)
		}
		if withQuotaEnableCalls.Load() != 1 {
			t.Fatalf("withQuotaEnableCalls = %d, want 1 (immediate bailout on SERVICE_DISABLED)", withQuotaEnableCalls.Load())
		}
		if noQuotaEnableCalls.Load() != 1 {
			t.Fatalf("noQuotaEnableCalls = %d, want 1", noQuotaEnableCalls.Load())
		}
	})

	t.Run("bootstraps cloudapis.googleapis.com and retries propagation before enabling Drive API", func(t *testing.T) {
		var cloudAPIsEnabled atomic.Bool
		var postBootstrapAttempts atomic.Int32
		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/v1/projects":
				_, _ = w.Write([]byte(`{"projects":[{"projectId":"colabone-drive-boot123456","lifecycleState":"ACTIVE"}]}`))
			case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/colabone-drive-boot123456/services/drive.googleapis.com":
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"Service Usage API has not been used in project colabone-drive-boot123456 before or it is disabled."}}`))
			case r.Method == http.MethodPost && r.URL.Path == "/v1/projects/colabone-drive-boot123456/services/cloudapis.googleapis.com:enable":
				cloudAPIsEnabled.Store(true)
				_, _ = w.Write([]byte(`{"done":true}`))
			case r.Method == http.MethodPost && r.URL.Path == "/v1/projects/colabone-drive-boot123456/services/drive.googleapis.com:enable":
				if !cloudAPIsEnabled.Load() {
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"Service Usage API has not been used in project colabone-drive-boot123456 before or it is disabled.","details":[{"reason":"SERVICE_DISABLED","metadata":{"service":"serviceusage.googleapis.com"}}]}}`))
					return
				}
				// Simulate 2 propagation-delay 403 SERVICE_DISABLED responses AFTER
				// cloudapis.googleapis.com succeeded before Service Control catches up.
				if postBootstrapAttempts.Add(1) <= 2 {
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"Service Usage API has not been used in project colabone-drive-boot123456 before or it is disabled.","details":[{"reason":"SERVICE_DISABLED","metadata":{"service":"serviceusage.googleapis.com"}}]}}`))
					return
				}
				_, _ = w.Write([]byte(`{"done":true}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer apiSrv.Close()

		vault := makeADCVault("user-bootstrap")
		auth := NewColabOneAuth(
			vault,
			WithCloudResourceManagerBaseURL(apiSrv.URL),
			WithServiceUsageBaseURL(apiSrv.URL),
			WithHTTPClient(apiSrv.Client()),
			WithSleepFn(noSleep),
		)

		pid, reason := auth.EnsureDriveQuotaProject(context.Background(), nil, true)
		if pid != "colabone-drive-boot123456" || reason != "reused_colabone_project" {
			t.Fatalf("got (%q, %q), want (\"colabone-drive-boot123456\", \"reused_colabone_project\")", pid, reason)
		}
		if !cloudAPIsEnabled.Load() {
			t.Fatal("expected cloudapis.googleapis.com bootstrap call")
		}
		if postBootstrapAttempts.Load() != 3 {
			t.Fatalf("postBootstrapAttempts = %d, want 3 (must retry SERVICE_DISABLED after bootstrap)", postBootstrapAttempts.Load())
		}
	})

	t.Run("existing own colabone-drive project enable failure logs sanitized diagnostics without creating duplicate project", func(t *testing.T) {
		ownPID := COLABONE_QUOTA_PROJECT_PREFIX + shortSHA256Hex("colabone-drive:user-no-dup", 10)
		var createCalls atomic.Int32
		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/v1/projects":
				_, _ = w.Write([]byte(fmt.Sprintf(`{"projects":[{"projectId":%q,"lifecycleState":"ACTIVE"}]}`, ownPID)))
			case r.Method == http.MethodPost && r.URL.Path == "/v1/projects":
				createCalls.Add(1)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"done":true}`))
			case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/drive.googleapis.com"):
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(fmt.Sprintf(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"Service Usage API has not been used in project %s before or it is disabled. Enable it by visiting https://console.developers.google.com/apis/api/serviceusage.googleapis.com/overview?project=%s then retry."}}`, ownPID, ownPID)))
			case r.Method == http.MethodPost && strings.Contains(r.URL.Path, ":enable"):
				w.WriteHeader(http.StatusForbidden)
				if r.Header.Get("X-Goog-User-Project") != "" {
					_, _ = w.Write([]byte(fmt.Sprintf(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"Service Usage API has not been used in project %s before or it is disabled. Enable it by visiting https://console.developers.google.com/apis/api/serviceusage.googleapis.com/overview?project=%s then retry. ya29.fake-token-leak","details":[{"reason":"SERVICE_DISABLED","metadata":{"service":"serviceusage.googleapis.com"}}]}}`, ownPID, ownPID)))
				} else {
					_, _ = w.Write([]byte(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"Service Usage API has not been used in project 764086051850 before or it is disabled.","details":[{"reason":"SERVICE_DISABLED","metadata":{"service":"serviceusage.googleapis.com"}}]}}`))
				}
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer apiSrv.Close()

		vault := makeADCVault("user-no-dup")
		auth := NewColabOneAuth(
			vault,
			WithCloudResourceManagerBaseURL(apiSrv.URL),
			WithServiceUsageBaseURL(apiSrv.URL),
			WithHTTPClient(apiSrv.Client()),
			WithSleepFn(noSleep),
		)

		var logs []string
		pid, reason := auth.EnsureDriveQuotaProject(context.Background(), func(msg, source, level string) {
			logs = append(logs, level+":"+source+":"+msg)
		}, true)
		if pid != "" || reason != "gcp_project_unavailable" {
			t.Fatalf("got (%q, %q), want (\"\", \"gcp_project_unavailable\")", pid, reason)
		}
		if createCalls.Load() != 0 {
			t.Fatalf("must not create duplicate project when own colabone-drive-* already exists; createCalls = %d", createCalls.Load())
		}
		if len(logs) != 1 {
			t.Fatalf("expected 1 diagnostic warning log, got %v", logs)
		}
		diag := logs[0]
		wantPrefix := fmt.Sprintf("warning:云盘:未能为免费 Drive 配额项目 (%s) 启用 Drive API", ownPID)
		if !strings.Contains(diag, wantPrefix) {
			t.Fatalf("unexpected diagnostic log prefix: %q", diag)
		}
		if !strings.Contains(diag, "PERMISSION_DENIED/SERVICE_DISABLED") || !strings.Contains(diag, "764086051850") {
			t.Fatalf("diagnostic log missing structured GCP error detail: %q", diag)
		}
		if strings.Contains(diag, "https://") {
			t.Fatalf("diagnostic log must strip console URLs: %q", diag)
		}
		if strings.Contains(diag, "ya29.fake-token-leak") {
			t.Fatalf("diagnostic log must redact OAuth tokens: %q", diag)
		}
		if strings.Count(diag, ownPID) != 1 {
			t.Fatalf("project ID should appear only once (inside masked slot): %q", diag)
		}
	})

	t.Run("shared unrelated colabone-drive project failure does not block creating own project", func(t *testing.T) {
		ownPID := COLABONE_QUOTA_PROJECT_PREFIX + shortSHA256Hex("colabone-drive:user-team", 10)
		var createCalls atomic.Int32
		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/v1/projects":
				_, _ = w.Write([]byte(`{"projects":[{"projectId":"colabone-drive-colleague1","lifecycleState":"ACTIVE"}]}`))
			case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/colabone-drive-colleague1/services/drive.googleapis.com":
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"The caller does not have permission"}}`))
			case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/projects/colabone-drive-colleague1/"):
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"The caller does not have permission"}}`))
			case r.Method == http.MethodPost && r.URL.Path == "/v1/projects":
				createCalls.Add(1)
				_, _ = w.Write([]byte(`{"done":true}`))
			case r.Method == http.MethodPost && r.URL.Path == "/v1/projects/"+ownPID+"/services/drive.googleapis.com:enable":
				_, _ = w.Write([]byte(`{"done":true}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer apiSrv.Close()

		vault := makeADCVault("user-team")
		auth := NewColabOneAuth(
			vault,
			WithCloudResourceManagerBaseURL(apiSrv.URL),
			WithServiceUsageBaseURL(apiSrv.URL),
			WithHTTPClient(apiSrv.Client()),
			WithSleepFn(noSleep),
		)

		pid, reason := auth.EnsureDriveQuotaProject(context.Background(), nil, true)
		if pid != ownPID || reason != "created_colabone_project" {
			t.Fatalf("got (%q, %q), want (%q, \"created_colabone_project\")", pid, reason, ownPID)
		}
		if createCalls.Load() != 1 {
			t.Fatalf("createCalls = %d, want 1", createCalls.Load())
		}
	})

	t.Run("transient 503 during enableDriveAPI returns network_error without negative caching and IAM 403 avoids redundant GET fallback", func(t *testing.T) {
		ownPID := COLABONE_QUOTA_PROJECT_PREFIX + shortSHA256Hex("colabone-drive:user-503-en", 10)
		var otherGetCalls atomic.Int32
		var enable503Calls atomic.Int32
		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/v1/projects":
				_, _ = w.Write([]byte(fmt.Sprintf(`{"projects":[{"projectId":%q,"lifecycleState":"ACTIVE"},{"projectId":"viewer-only-proj","lifecycleState":"ACTIVE"}]}`, ownPID)))
			case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/viewer-only-proj/services/drive.googleapis.com":
				otherGetCalls.Add(1)
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"The caller does not have permission"}}`))
			case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/"+ownPID+"/services/drive.googleapis.com":
				_, _ = w.Write([]byte(`{"state":"DISABLED"}`))
			case r.Method == http.MethodPost && r.URL.Path == "/v1/projects/"+ownPID+"/services/drive.googleapis.com:enable":
				enable503Calls.Add(1)
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":{"code":503,"status":"UNAVAILABLE","message":"backend unavailable"}}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer apiSrv.Close()

		vault := makeADCVault("user-503-en")
		auth := NewColabOneAuth(
			vault,
			WithCloudResourceManagerBaseURL(apiSrv.URL),
			WithServiceUsageBaseURL(apiSrv.URL),
			WithHTTPClient(apiSrv.Client()),
			WithSleepFn(noSleep),
		)

		pid1, reason1 := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
		pid2, reason2 := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
		if pid1 != "" || reason1 != "network_error" || pid2 != "" || reason2 != "network_error" {
			t.Fatalf("got (%q,%q) and (%q,%q), want network_error", pid1, reason1, pid2, reason2)
		}
		if otherGetCalls.Load() != 2 {
			t.Fatalf("viewer-only IAM 403 should not trigger redundant GET fallback; otherGetCalls = %d, want 2 (1 per invocation)", otherGetCalls.Load())
		}
		if enable503Calls.Load() != 6 {
			t.Fatalf("enable503Calls = %d, want 6 (3 retries x 2 uncached calls)", enable503Calls.Load())
		}
	})

	t.Run("account switch mid-flight during enableDriveAPI suppresses stale diagnostic log", func(t *testing.T) {
		ownPID := COLABONE_QUOTA_PROJECT_PREFIX + shortSHA256Hex("colabone-drive:sub-stale-diag", 10)
		vault := makeADCVault("sub-stale-diag")
		var auth *ColabOneAuth
		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/v1/projects":
				_, _ = w.Write([]byte(fmt.Sprintf(`{"projects":[{"projectId":%q,"lifecycleState":"ACTIVE"}]}`, ownPID)))
			case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/drive.googleapis.com"):
				_, _ = w.Write([]byte(`{"state":"DISABLED"}`))
			case r.Method == http.MethodPost && strings.Contains(r.URL.Path, ":enable"):
				// Simulate account invalidation mid-flight before enableDriveAPI finishes.
				auth.InvalidateDriveQuotaProject()
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"Service Usage API has not been used in project before or it is disabled.","details":[{"reason":"SERVICE_DISABLED","metadata":{"service":"serviceusage.googleapis.com"}}]}}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer apiSrv.Close()

		auth = NewColabOneAuth(
			vault,
			WithCloudResourceManagerBaseURL(apiSrv.URL),
			WithServiceUsageBaseURL(apiSrv.URL),
			WithHTTPClient(apiSrv.Client()),
			WithSleepFn(noSleep),
		)

		var logs []string
		pid, reason := auth.EnsureDriveQuotaProject(context.Background(), func(msg, source, level string) {
			logs = append(logs, msg)
		}, true)
		if pid != "" || reason != "account_changed" {
			t.Fatalf("got (%q, %q), want (\"\", \"account_changed\")", pid, reason)
		}
		if len(logs) != 0 {
			t.Fatalf("stale request after account switch/invalidation must not emit diagnostic logs, got %v", logs)
		}
	})

	t.Run("transient 503 during driveAPIEnabled check returns network_error without creating new project", func(t *testing.T) {
		var createCalls atomic.Int32
		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/v1/projects":
				_, _ = w.Write([]byte(`{"projects":[{"projectId":"existing-user-proj","lifecycleState":"ACTIVE"}]}`))
			case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/existing-user-proj/services/drive.googleapis.com":
				w.WriteHeader(http.StatusServiceUnavailable)
			case r.Method == http.MethodPost && r.URL.Path == "/v1/projects":
				createCalls.Add(1)
				_, _ = w.Write([]byte(`{"done":true}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer apiSrv.Close()

		vault := makeADCVault("user-get-503")
		auth := NewColabOneAuth(
			vault,
			WithCloudResourceManagerBaseURL(apiSrv.URL),
			WithServiceUsageBaseURL(apiSrv.URL),
			WithHTTPClient(apiSrv.Client()),
			WithSleepFn(noSleep),
		)

		pid, reason := auth.EnsureDriveQuotaProject(context.Background(), nil, true)
		if pid != "" || reason != "network_error" {
			t.Fatalf("got (%q, %q), want (\"\", \"network_error\")", pid, reason)
		}
		if createCalls.Load() != 0 {
			t.Fatalf("transient 503 during driveAPIEnabled must not fall through to project creation; createCalls = %d", createCalls.Load())
		}
	})

	t.Run("LRO op.Error logs gRPC error code without misleading HTTP 200", func(t *testing.T) {
		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/v1/projects":
				_, _ = w.Write([]byte(`{}`))
			case r.Method == http.MethodPost && r.URL.Path == "/v1/projects":
				_, _ = w.Write([]byte(`{"name":"operations/create_denied.1"}`))
			case r.Method == http.MethodGet && r.URL.Path == "/v1/operations/create_denied.1":
				_, _ = w.Write([]byte(`{"done":true,"error":{"code":7,"message":"Project creation denied by org policy"}}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer apiSrv.Close()

		vault := makeADCVault("user-lro-err")
		auth := NewColabOneAuth(
			vault,
			WithCloudResourceManagerBaseURL(apiSrv.URL),
			WithServiceUsageBaseURL(apiSrv.URL),
			WithHTTPClient(apiSrv.Client()),
			WithSleepFn(noSleep),
		)

		var logs []string
		pid, reason := auth.EnsureDriveQuotaProject(context.Background(), func(msg, source, level string) {
			logs = append(logs, level+":"+msg)
		}, true)
		if pid != "" || reason != "gcp_project_unavailable" {
			t.Fatalf("got (%q, %q), want (\"\", \"gcp_project_unavailable\")", pid, reason)
		}
		found := false
		for _, entry := range logs {
			if strings.HasPrefix(entry, "warning:等待免费 Drive 配额项目 (") {
				found = true
				if strings.Contains(entry, "HTTP 200") {
					t.Fatalf("LRO op.Error must not log misleading HTTP 200: %q", entry)
				}
				if !strings.Contains(entry, "code 7 · Project creation denied by org policy") {
					t.Fatalf("LRO op.Error log missing gRPC code/message: %q", entry)
				}
			}
		}
		if !found {
			t.Fatalf("expected poll_project diagnostic warning log, got %v", logs)
		}
	})
}

func TestColabLoginDirectPasteAndFinishErrors(t *testing.T) {
	t.Run("direct paste of callback URL completes active loopback without ColabBegin", func(t *testing.T) {
		tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"direct-tok","refresh_token":"direct-ref","expires_in":3600}`))
		}))
		defer tokenSrv.Close()

		userInfoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"email":"direct@example.com","sub":"sub-direct"}`))
		}))
		defer userInfoSrv.Close()

		vault := newMemoryVault()
		auth := NewColabOneAuth(
			vault,
			WithTokenURI(tokenSrv.URL),
			WithUserInfoURL(userInfoSrv.URL),
			WithHTTPClient(tokenSrv.Client()),
			WithSleepFn(noSleep),
		)

		readyURLCh := make(chan string, 1)
		loginErrCh := make(chan error, 1)
		go func() {
			loginErrCh <- auth.ColabLogin(context.Background(), func(u string) {
				readyURLCh <- u
			})
		}()

		authURL := <-readyURLCh
		u, _ := url.Parse(authURL)
		pastedCallback := u.Query().Get("redirect_uri") + "?state=" + u.Query().Get("state") + "&code=" + url.QueryEscape(testAuthCode)

		if _, err := auth.ColabFinish(context.Background(), pastedCallback); err != nil {
			t.Fatalf("direct ColabFinish error: %v", err)
		}
		if err := <-loginErrCh; err != nil {
			t.Fatalf("ColabLogin error after direct paste: %v", err)
		}
		var creds ColabOneCredentials
		if ok, _ := vault.GetJSON("colab", &creds); !ok || creds.Token != "direct-tok" {
			t.Fatalf("stored creds = %+v", creds)
		}
	})

	t.Run("token exchange error returns ErrAuthCodeInvalid and profile error returns ErrProfileFetchFailed", func(t *testing.T) {
		badTokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		}))
		defer badTokenSrv.Close()

		authBadToken := NewColabOneAuth(
			newMemoryVault(),
			WithTokenURI(badTokenSrv.URL),
			WithHTTPClient(badTokenSrv.Client()),
		)
		if _, err := authBadToken.ColabBegin(); err != nil {
			t.Fatalf("ColabBegin: %v", err)
		}
		if _, err := authBadToken.ColabFinish(context.Background(), testAuthCode); !errors.Is(err, ErrAuthCodeInvalid) {
			t.Fatalf("err = %v, want ErrAuthCodeInvalid", err)
		}

		okTokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"tok","refresh_token":"ref","expires_in":3600}`))
		}))
		defer okTokenSrv.Close()

		badUserInfoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer badUserInfoSrv.Close()

		authBadProfile := NewColabOneAuth(
			newMemoryVault(),
			WithTokenURI(okTokenSrv.URL),
			WithUserInfoURL(badUserInfoSrv.URL),
			WithHTTPClient(okTokenSrv.Client()),
			WithSleepFn(noSleep),
		)
		if _, err := authBadProfile.ColabBegin(); err != nil {
			t.Fatalf("ColabBegin: %v", err)
		}
		if _, err := authBadProfile.ColabFinish(context.Background(), testAuthCode); !errors.Is(err, ErrProfileFetchFailed) {
			t.Fatalf("err = %v, want ErrProfileFetchFailed", err)
		}
	})

	t.Run("active session blocks ColabLogin and ColabFinish", func(t *testing.T) {
		vault := newMemoryVault()
		auth := NewColabOneAuth(vault)
		if _, err := auth.ColabBegin(); err != nil {
			t.Fatalf("ColabBegin: %v", err)
		}
		_ = vault.Set("runtime_session", map[string]any{"endpoint": "ep-1"})
		if _, err := auth.ColabFinish(context.Background(), testAuthCode); !errors.Is(err, ErrActiveSessionsExist) {
			t.Fatalf("ColabFinish err = %v, want ErrActiveSessionsExist", err)
		}
		if err := auth.ColabLogin(context.Background(), nil); !errors.Is(err, ErrActiveSessionsExist) {
			t.Fatalf("ColabLogin err = %v, want ErrActiveSessionsExist", err)
		}
	})
}

func TestConcurrentAuthOperationsNoDeadlock(t *testing.T) {
	vault := newMemoryVault()
	_ = vault.Set("colab", &ColabOneCredentials{
		Token:        "tok",
		RefreshToken: "ref",
		ClientID:     COLABONE_OAUTH_CLIENT_ID,
		ClientSecret: COLABONE_OAUTH_CLIENT_SECRET,
		Scopes:       PUBLIC_SCOPES,
		Expiry:       "2099-01-01T00:00:00Z",
	})
	auth := NewColabOneAuth(vault)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				if id%2 == 0 {
					auth.SetLocale("en-US")
				} else {
					auth.SetLocale("ja-JP")
				}
				_ = auth.Locale()
				_ = auth.LoopbackActive()
				_ = auth.HasDriveScope()
				_ = auth.RequiresDriveQuotaProject()
				_ = auth.DriveQuotaStatus()
				auth.InvalidateDriveQuotaProject()
				if _, err := auth.Credentials(context.Background(), "colab"); err != nil {
					t.Errorf("Credentials failed: %v", err)
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestColabLoginDuplicateCallbackAndBuildRcloneConfNaiveExpiry(t *testing.T) {
	var tokenHits atomic.Int32
	oauthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			if tokenHits.Add(1) > 1 {
				// Simulate Google rejecting a single-use authorization code on duplicate callback.
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"tok-once","refresh_token":"ref-once","expires_in":3600}`))
		case "/userinfo":
			_, _ = w.Write([]byte(`{"email":"dup@example.com","sub":"sub-dup"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer oauthSrv.Close()

	vault := newMemoryVault()
	auth := NewColabOneAuth(
		vault,
		WithTokenURI(oauthSrv.URL+"/token"),
		WithUserInfoURL(oauthSrv.URL+"/userinfo"),
		WithHTTPClient(oauthSrv.Client()),
		WithLoopbackTimeout(5*time.Second),
	)

	authURLCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- auth.ColabLogin(context.Background(), func(u string) {
			authURLCh <- u
		})
	}()

	rawAuthURL := <-authURLCh
	u, err := url.Parse(rawAuthURL)
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	cbURL := u.Query().Get("redirect_uri") + "?state=" + url.QueryEscape(u.Query().Get("state")) + "&code=4/valid-dup-test-code-123456"

	// Fire two concurrent duplicate GET callbacks (e.g. browser reload / extension prefetch).
	var cbWG sync.WaitGroup
	for i := 0; i < 2; i++ {
		cbWG.Add(1)
		go func() {
			defer cbWG.Done()
			resp, err := http.Get(cbURL)
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}()
	}
	cbWG.Wait()

	if loginErr := <-errCh; loginErr != nil {
		t.Fatalf("duplicate callback wrongly failed succeeded login: %v", loginErr)
	}
	if tokenHits.Load() != 1 {
		t.Fatalf("expected token endpoint called once, got %d", tokenHits.Load())
	}

	// BuildRcloneConf must normalize naive ISO-8601 expiry timestamps to UTC RFC3339 ("Z").
	_ = vault.Set("colab", map[string]any{
		"token":         "tok-rclone",
		"refresh_token": "ref-rclone",
		"client_id":     "cid",
		"client_secret": "csec",
		"expiry":        "2099-01-01T12:00:00",
		"scopes":        []string{"https://www.googleapis.com/auth/drive.file"},
	})
	conf, err := auth.BuildRcloneConf(context.Background(), nil)
	if err != nil {
		t.Fatalf("BuildRcloneConf failed: %v", err)
	}
	if !strings.Contains(conf, `"expiry":"2099-01-01T12:00:00Z"`) {
		t.Fatalf("expected RFC3339 Z expiry in rclone conf, got:\n%s", conf)
	}
}

func TestColabLoginSemaphoreCancelLateCallbackAndRefreshCancel(t *testing.T) {
	// 1. Second concurrent ColabLogin blocked on loopback semaphore must respect ctx cancellation.
	vault1 := newMemoryVault()
	auth1 := NewColabOneAuth(vault1, WithLoopbackTimeout(5*time.Second))

	ctx1, cancel1 := context.WithCancel(context.Background())
	readyCh := make(chan string, 1)
	done1 := make(chan error, 1)
	go func() {
		done1 <- auth1.ColabLogin(ctx1, func(u string) { readyCh <- u })
	}()
	<-readyCh

	ctx2, cancel2 := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel2()
	if err := auth1.ColabLogin(ctx2, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked ColabLogin should abort on ctx deadline, got %v", err)
	}
	cancel1()
	<-done1

	// 2. Late browser callback arriving AFTER manual ColabFinish succeeds must not clobber success.
	var tokenCalls atomic.Int32
	oauthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			if tokenCalls.Add(1) == 1 {
				_, _ = w.Write([]byte(`{"access_token":"tok-manual","refresh_token":"ref-manual","expires_in":3600,"scope":"https://www.googleapis.com/auth/colab"}`))
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		case "/userinfo":
			_, _ = w.Write([]byte(`{"email":"manual@example.com","sub":"sub-manual","name":"Manual"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer oauthSrv.Close()

	vault2 := newMemoryVault()
	auth2 := NewColabOneAuth(
		vault2,
		WithTokenURI(oauthSrv.URL+"/token"),
		WithUserInfoURL(oauthSrv.URL+"/userinfo"),
		WithHTTPClient(oauthSrv.Client()),
		WithLoopbackTimeout(5*time.Second),
	)
	authURLCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- auth2.ColabLogin(context.Background(), func(u string) { authURLCh <- u })
	}()
	rawURL := <-authURLCh
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	if _, err := auth2.ColabFinish(context.Background(), "4/manual-code-987654321"); err != nil {
		t.Fatalf("manual ColabFinish failed: %v", err)
	}
	// Fire late browser redirect with same state after manual completion.
	lateURL := parsed.Query().Get("redirect_uri") + "?state=" + url.QueryEscape(parsed.Query().Get("state")) + "&code=4/late-code-987654321"
	if resp, err := http.Get(lateURL); err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	if err := <-errCh; err != nil {
		t.Fatalf("ColabLogin failed after manual ColabFinish + late callback: %v", err)
	}

	// 3. Cancelled context during token refresh returns context.Canceled (not ErrRefreshRetryable).
	slowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer slowSrv.Close()
	vault3 := newMemoryVault()
	_ = vault3.Set("colab", map[string]any{
		"token":         "expired-tok",
		"refresh_token": "ref-tok",
		"expiry":        "2000-01-01T00:00:00Z",
	})
	auth3 := NewColabOneAuth(
		vault3,
		WithTokenURI(slowSrv.URL+"/token"),
		WithHTTPClient(slowSrv.Client()),
	)
	cCtx, cCancel := context.WithCancel(context.Background())
	cCancel()
	if _, err := auth3.Credentials(cCtx, "colab"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled on cancelled refresh, got %v", err)
	}
}

type hookVault struct {
	*memoryVault
	onDelete func(name string)
	onSet    func(name string)
}

func (v *hookVault) Delete(name string) error {
	err := v.memoryVault.Delete(name)
	if v.onDelete != nil {
		v.onDelete(name)
	}
	return err
}

func (v *hookVault) Set(name string, value any) error {
	if v.onSet != nil {
		v.onSet(name)
	}
	return v.memoryVault.Set(name, value)
}

func TestEnsureDriveQuotaProjectSignOutDoesNotResurrect(t *testing.T) {
	seedAccount := func(v *memoryVault, sub, tok string) {
		_ = v.Set("colab", &ColabOneCredentials{
			Token:        tok,
			RefreshToken: "ref-" + sub,
			ClientID:     COLABONE_OAUTH_CLIENT_ID,
			ClientSecret: COLABONE_OAUTH_CLIENT_SECRET,
			Expiry:       "2099-01-01T00:00:00Z",
			Scopes:       PUBLIC_SCOPES,
		})
		_ = v.Set("google_profile", map[string]string{
			"sub":   sub,
			"email": sub + "@example.com",
		})
	}

	t.Run("sign-out during in-flight discovery aborts persisting cloud_project", func(t *testing.T) {
		vault := newMemoryVault()
		seedAccount(vault, "sub-signout-a", "tok-a")

		inFlightCh := make(chan struct{})
		releaseCh := make(chan struct{})
		var signalOnce sync.Once

		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/v1/projects":
				signalOnce.Do(func() { close(inFlightCh) })
				<-releaseCh
				_, _ = w.Write([]byte(`{"projects":[{"projectId":"acct-a-proj-01","lifecycleState":"ACTIVE"}]}`))
			case "/v1/projects/acct-a-proj-01/services/drive.googleapis.com":
				_, _ = w.Write([]byte(`{"state":"ENABLED"}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer apiSrv.Close()

		auth := NewColabOneAuth(
			vault,
			WithCloudResourceManagerBaseURL(apiSrv.URL),
			WithServiceUsageBaseURL(apiSrv.URL),
			WithHTTPClient(apiSrv.Client()),
			WithSleepFn(noSleep),
		)

		type probeResult struct {
			pid    string
			reason string
		}
		resCh := make(chan probeResult, 1)
		go func() {
			pid, reason := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
			resCh <- probeResult{pid: pid, reason: reason}
		}()

		<-inFlightCh
		// Simulate signing out while EnsureDriveQuotaProject is in flight:
		// delete profile + credentials, then invalidate quota cache.
		_ = vault.Delete("google_profile")
		_ = vault.Delete("colab")
		auth.InvalidateDriveQuotaProject()
		close(releaseCh)

		res := <-resCh
		if res.pid != "" || res.reason != "account_changed" {
			t.Fatalf("got (%q, %q), want (\"\", \"account_changed\")", res.pid, res.reason)
		}
		var stored string
		if ok, _ := vault.GetJSON("cloud_project", &stored); ok {
			t.Fatalf("sign-out must not resurrect cloud_project, got %q", stored)
		}
		auth.quotaMu.Lock()
		cached := auth.quotaProbeCache
		auth.quotaMu.Unlock()
		if cached != nil {
			t.Fatalf("sign-out must not populate quotaProbeCache, got %+v", cached)
		}
	})

	t.Run("sign-out during in-flight 403 does not poison negative cache", func(t *testing.T) {
		vault := newMemoryVault()
		seedAccount(vault, "sub-signout-tos", "tok-tos")

		inFlightCh := make(chan struct{})
		releaseCh := make(chan struct{})
		var signalOnce sync.Once

		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/v1/projects" {
				signalOnce.Do(func() { close(inFlightCh) })
				<-releaseCh
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"message":"Callers must accept Terms of Service"}}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer apiSrv.Close()

		auth := NewColabOneAuth(
			vault,
			WithCloudResourceManagerBaseURL(apiSrv.URL),
			WithServiceUsageBaseURL(apiSrv.URL),
			WithHTTPClient(apiSrv.Client()),
			WithSleepFn(noSleep),
		)

		type probeResult struct {
			pid    string
			reason string
		}
		resCh := make(chan probeResult, 1)
		go func() {
			pid, reason := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
			resCh <- probeResult{pid: pid, reason: reason}
		}()

		<-inFlightCh
		_ = vault.Delete("google_profile")
		_ = vault.Delete("colab")
		auth.InvalidateDriveQuotaProject()
		close(releaseCh)

		res := <-resCh
		if res.pid != "" || res.reason != "account_changed" {
			t.Fatalf("got (%q, %q), want (\"\", \"account_changed\")", res.pid, res.reason)
		}
		auth.quotaMu.Lock()
		cached := auth.quotaProbeCache
		auth.quotaMu.Unlock()
		if cached != nil {
			t.Fatalf("sign-out must not leave negative quotaProbeCache, got %+v", cached)
		}
	})
}

func TestEnsureDriveQuotaProjectAccountSwitchAtomicNoPoisoning(t *testing.T) {
	seedAccountA := func(v VaultStore) {
		_ = v.Set("colab", &ColabOneCredentials{
			Token:        "tok-acct-a",
			RefreshToken: "ref-acct-a",
			ClientID:     COLABONE_OAUTH_CLIENT_ID,
			ClientSecret: COLABONE_OAUTH_CLIENT_SECRET,
			Expiry:       "2099-01-01T00:00:00Z",
			Scopes:       PUBLIC_SCOPES,
		})
		_ = v.Set("google_profile", map[string]string{
			"sub":   "sub-acct-a",
			"email": "a@example.com",
		})
	}

	newAccountBOAuthServer := func(t *testing.T) *httptest.Server {
		t.Helper()
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/token":
				_, _ = w.Write([]byte(`{
					"access_token": "tok-acct-b",
					"refresh_token": "ref-acct-b",
					"expires_in": 3600,
					"scope": "openid https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/colaboratory https://www.googleapis.com/auth/drive.file"
				}`))
			case "/userinfo":
				_, _ = w.Write([]byte(`{"email":"b@example.com","sub":"sub-acct-b"}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
	}

	t.Run("ColabFinish atomically switches profile and quota state closing persistProject race window", func(t *testing.T) {
		oauthSrv := newAccountBOAuthServer(t)
		defer oauthSrv.Close()

		aInFlight := make(chan struct{})
		releaseA := make(chan struct{})
		var aInFlightOnce sync.Once
		var releaseAOnce sync.Once

		// Pause inside Delete("cloud_project") before Set("google_profile") runs while
		// letting Account A's EnsureDriveQuotaProject race directly into persistProject.
		// Because colabFinishWithFlow holds quotaMu across delete + cache clear + profile write,
		// Account A's persistProject must block until Account B's profile is committed and then abort.
		baseVault := newMemoryVault()
		hv := &hookVault{
			memoryVault: baseVault,
			onDelete: func(name string) {
				if name == "cloud_project" {
					releaseAOnce.Do(func() { close(releaseA) })
					time.Sleep(25 * time.Millisecond)
				}
			},
		}
		seedAccountA(hv)

		gcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			authHeader := r.Header.Get("Authorization")
			switch r.URL.Path {
			case "/v1/projects":
				if authHeader == "Bearer tok-acct-a" {
					aInFlightOnce.Do(func() { close(aInFlight) })
					<-releaseA
					_, _ = w.Write([]byte(`{"projects":[{"projectId":"acct-a-leaked-proj","lifecycleState":"ACTIVE"}]}`))
					return
				}
				if authHeader == "Bearer tok-acct-b" {
					_, _ = w.Write([]byte(`{"projects":[{"projectId":"acct-b-owned-proj","lifecycleState":"ACTIVE"}]}`))
					return
				}
				w.WriteHeader(http.StatusUnauthorized)
			case "/v1/projects/acct-a-leaked-proj/services/drive.googleapis.com":
				_, _ = w.Write([]byte(`{"state":"ENABLED"}`))
			case "/v1/projects/acct-b-owned-proj/services/drive.googleapis.com":
				_, _ = w.Write([]byte(`{"state":"ENABLED"}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer gcpSrv.Close()

		auth := NewColabOneAuth(
			hv,
			WithTokenURI(oauthSrv.URL+"/token"),
			WithUserInfoURL(oauthSrv.URL+"/userinfo"),
			WithCloudResourceManagerBaseURL(gcpSrv.URL),
			WithServiceUsageBaseURL(gcpSrv.URL),
			WithHTTPClient(gcpSrv.Client()),
			WithSleepFn(noSleep),
		)

		type probeResult struct {
			pid    string
			reason string
		}
		aResCh := make(chan probeResult, 1)
		go func() {
			pid, reason := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
			aResCh <- probeResult{pid: pid, reason: reason}
		}()

		<-aInFlight
		if _, err := auth.ColabBegin(); err != nil {
			t.Fatalf("ColabBegin failed: %v", err)
		}
		if _, err := auth.ColabFinish(context.Background(), testAuthCode); err != nil {
			t.Fatalf("ColabFinish failed: %v", err)
		}
		releaseAOnce.Do(func() { close(releaseA) })

		aRes := <-aResCh
		if aRes.pid != "" || aRes.reason != "account_changed" {
			t.Fatalf("Account A probe got (%q, %q), want (\"\", \"account_changed\")", aRes.pid, aRes.reason)
		}

		var storedProj string
		if ok, _ := baseVault.GetJSON("cloud_project", &storedProj); ok {
			t.Fatalf("Account A cloud_project leaked into Account B vault: %q", storedProj)
		}

		// Account B must cleanly discover its own project.
		bPID, bReason := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
		if bPID != "acct-b-owned-proj" || bReason != "reused_existing_project" {
			t.Fatalf("Account B probe got (%q, %q), want (\"acct-b-owned-proj\", \"reused_existing_project\")", bPID, bReason)
		}
	})

	t.Run("Account A failure during account switch does not poison Account B negative cache", func(t *testing.T) {
		oauthSrv := newAccountBOAuthServer(t)
		defer oauthSrv.Close()

		aInFlight := make(chan struct{})
		releaseA := make(chan struct{})
		var aInFlightOnce sync.Once
		var bListCalls atomic.Int32

		vault := newMemoryVault()
		seedAccountA(vault)

		gcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			authHeader := r.Header.Get("Authorization")
			switch r.URL.Path {
			case "/v1/projects":
				if authHeader == "Bearer tok-acct-a" {
					aInFlightOnce.Do(func() { close(aInFlight) })
					<-releaseA
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"error":{"message":"Callers must accept Terms of Service"}}`))
					return
				}
				if authHeader == "Bearer tok-acct-b" {
					bListCalls.Add(1)
					_, _ = w.Write([]byte(`{"projects":[{"projectId":"acct-b-ready-proj","lifecycleState":"ACTIVE"}]}`))
					return
				}
				w.WriteHeader(http.StatusUnauthorized)
			case "/v1/projects/acct-b-ready-proj/services/drive.googleapis.com":
				_, _ = w.Write([]byte(`{"state":"ENABLED"}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer gcpSrv.Close()

		auth := NewColabOneAuth(
			vault,
			WithTokenURI(oauthSrv.URL+"/token"),
			WithUserInfoURL(oauthSrv.URL+"/userinfo"),
			WithCloudResourceManagerBaseURL(gcpSrv.URL),
			WithServiceUsageBaseURL(gcpSrv.URL),
			WithHTTPClient(gcpSrv.Client()),
			WithSleepFn(noSleep),
		)

		type probeResult struct {
			pid    string
			reason string
		}
		aResCh := make(chan probeResult, 1)
		go func() {
			pid, reason := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
			aResCh <- probeResult{pid: pid, reason: reason}
		}()

		<-aInFlight
		if _, err := auth.ColabBegin(); err != nil {
			t.Fatalf("ColabBegin failed: %v", err)
		}
		if _, err := auth.ColabFinish(context.Background(), testAuthCode); err != nil {
			t.Fatalf("ColabFinish failed: %v", err)
		}
		close(releaseA)

		aRes := <-aResCh
		if aRes.pid != "" || aRes.reason != "account_changed" {
			t.Fatalf("Account A failed probe after switch got (%q, %q), want (\"\", \"account_changed\")", aRes.pid, aRes.reason)
		}

		// Account B status must not inherit Account A's gcp_tos_required negative cache.
		st := auth.DriveQuotaStatus()
		if st["reason"] != "" {
			t.Fatalf("Account B DriveQuotaStatus poisoned with reason=%q", st["reason"])
		}

		// forceRefresh=false must still probe GCP for Account B instead of hitting a 10m negative cache.
		bPID, bReason := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
		if bPID != "acct-b-ready-proj" || bReason != "reused_existing_project" {
			t.Fatalf("Account B probe got (%q, %q), want (\"acct-b-ready-proj\", \"reused_existing_project\")", bPID, bReason)
		}
		if bListCalls.Load() != 1 {
			t.Fatalf("expected Account B to query /v1/projects once, got %d", bListCalls.Load())
		}
	})
}

func TestCredentialsRefreshAll5xxReturnRetryable(t *testing.T) {
	now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)

	retryableCodes := []int{
		http.StatusRequestTimeout,      // 408
		http.StatusTooManyRequests,     // 429
		http.StatusInternalServerError, // 500
		http.StatusNotImplemented,      // 501
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout,      // 504
		http.StatusInsufficientStorage, // 507
		520,                            // Non-standard edge/CDN 5xx
		599,                            // Upper 5xx boundary
	}
	for _, code := range retryableCodes {
		t.Run(fmt.Sprintf("HTTP_%d_retryable", code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
				_, _ = w.Write([]byte(`{"error":"upstream_failure"}`))
			}))
			defer srv.Close()

			vault := newMemoryVault()
			_ = vault.Set("colab", &ColabOneCredentials{
				Token:        "expired",
				RefreshToken: "saved-ref",
				Expiry:       now.Add(-time.Hour).Format(time.RFC3339),
			})
			before := vault.rawJSON("colab")

			auth := NewColabOneAuth(
				vault,
				WithTokenURI(srv.URL),
				WithNowFn(func() time.Time { return now }),
				WithHTTPClient(srv.Client()),
			)
			_, err := auth.Credentials(context.Background(), "colab")
			if !errors.Is(err, ErrRefreshRetryable) {
				t.Fatalf("HTTP %d: err = %v, want ErrRefreshRetryable", code, err)
			}
			if errors.Is(err, ErrAuthExpired) {
				t.Fatalf("HTTP %d: must not return ErrAuthExpired", code)
			}
			if vault.rawJSON("colab") != before {
				t.Fatalf("HTTP %d: vault modified on retryable refresh error", code)
			}
		})
	}

	// Non-retryable 4xx (e.g. 400 invalid_grant, 401 unauthorized) must still return ErrAuthExpired.
	for _, code := range []int{http.StatusBadRequest, http.StatusUnauthorized} {
		t.Run(fmt.Sprintf("HTTP_%d_expired", code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			}))
			defer srv.Close()

			vault := newMemoryVault()
			_ = vault.Set("colab", &ColabOneCredentials{
				Token:        "expired",
				RefreshToken: "revoked-ref",
				Expiry:       now.Add(-time.Hour).Format(time.RFC3339),
			})

			auth := NewColabOneAuth(
				vault,
				WithTokenURI(srv.URL),
				WithNowFn(func() time.Time { return now }),
				WithHTTPClient(srv.Client()),
			)
			_, err := auth.Credentials(context.Background(), "colab")
			if !errors.Is(err, ErrAuthExpired) {
				t.Fatalf("HTTP %d: err = %v, want ErrAuthExpired", code, err)
			}
		})
	}
}

func TestDefaultHTTPClientDoesNotFollowRedirectsOrLeakCredentials(t *testing.T) {
	t.Run("FetchProfile rejects 302 redirect without forwarding Authorization Bearer", func(t *testing.T) {
		var targetHits atomic.Int32
		var leakedAuthHeader atomic.Value

		targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			targetHits.Add(1)
			if h := r.Header.Get("Authorization"); h != "" {
				leakedAuthHeader.Store(h)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"email":"attacker@example.com","sub":"sub-attacker"}`))
		}))
		defer targetSrv.Close()

		var redirectHits atomic.Int32
		redirectSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			redirectHits.Add(1)
			if got := r.Header.Get("Authorization"); got != "Bearer secret-oauth-bearer-token" {
				t.Errorf("initial Authorization header = %q, want Bearer secret-oauth-bearer-token", got)
			}
			http.Redirect(w, r, targetSrv.URL+"/userinfo", http.StatusFound)
		}))
		defer redirectSrv.Close()

		// Intentionally omit WithHTTPClient so the default ColabOneAuth httpClient is exercised.
		auth := NewColabOneAuth(
			newMemoryVault(),
			WithUserInfoURL(redirectSrv.URL+"/userinfo"),
			WithSleepFn(noSleep),
		)

		_, err := auth.FetchProfile(context.Background(), "secret-oauth-bearer-token")
		if err == nil {
			t.Fatal("FetchProfile() succeeded across 302 redirect, want error")
		}
		if !strings.Contains(err.Error(), "302") {
			t.Fatalf("FetchProfile() error = %v, want HTTP 302 error", err)
		}
		if redirectHits.Load() != 1 {
			t.Fatalf("redirect endpoint hits = %d, want 1", redirectHits.Load())
		}
		if targetHits.Load() != 0 {
			t.Fatalf("redirect target was hit %d time(s); default httpClient must not follow redirects (leaked auth=%v)", targetHits.Load(), leakedAuthHeader.Load())
		}

		// Also verify raw default httpClient returns 302 directly without following.
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, redirectSrv.URL+"/raw", nil)
		if err != nil {
			t.Fatalf("NewRequest error: %v", err)
		}
		req.Header.Set("Authorization", "Bearer secret-oauth-bearer-token")
		resp, err := auth.httpClient.Do(req)
		if err != nil {
			t.Fatalf("default httpClient.Do error: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("default httpClient status = %d, want 302", resp.StatusCode)
		}
		if targetHits.Load() != 0 {
			t.Fatalf("redirect target hit %d time(s) after raw Do", targetHits.Load())
		}
	})

	t.Run("Credentials refresh rejects 302 and 307 redirects without forwarding form secrets", func(t *testing.T) {
		now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)

		for _, code := range []int{http.StatusFound, http.StatusTemporaryRedirect} {
			t.Run(fmt.Sprintf("HTTP_%d", code), func(t *testing.T) {
				var targetHits atomic.Int32
				targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					targetHits.Add(1)
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"access_token":"hijacked-token","expires_in":3600}`))
				}))
				defer targetSrv.Close()

				redirectSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, targetSrv.URL+"/token", code)
				}))
				defer redirectSrv.Close()

				vault := newMemoryVault()
				_ = vault.Set("colab", &ColabOneCredentials{
					Token:        "expired-access",
					RefreshToken: "secret-refresh-token",
					ClientID:     COLABONE_OAUTH_CLIENT_ID,
					ClientSecret: "secret-client-secret",
					Expiry:       now.Add(-time.Hour).Format(time.RFC3339),
				})

				auth := NewColabOneAuth(
					vault,
					WithTokenURI(redirectSrv.URL+"/token"),
					WithNowFn(func() time.Time { return now }),
				)
				_, err := auth.Credentials(context.Background(), "colab")
				if !errors.Is(err, ErrAuthExpired) {
					t.Fatalf("Credentials() err = %v, want ErrAuthExpired", err)
				}
				if targetHits.Load() != 0 {
					t.Fatalf("token redirect target hit %d time(s), want 0", targetHits.Load())
				}
			})
		}
	})

	t.Run("ColabFinish token exchange rejects 307 redirect without forwarding code_verifier", func(t *testing.T) {
		var targetHits atomic.Int32
		targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			targetHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"hijacked","refresh_token":"hijacked-ref","expires_in":3600}`))
		}))
		defer targetSrv.Close()

		redirectSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, targetSrv.URL+"/token", http.StatusTemporaryRedirect)
		}))
		defer redirectSrv.Close()

		auth := NewColabOneAuth(
			newMemoryVault(),
			WithTokenURI(redirectSrv.URL+"/token"),
			WithSleepFn(noSleep),
		)
		if _, err := auth.ColabBegin(); err != nil {
			t.Fatalf("ColabBegin() error: %v", err)
		}
		_, err := auth.ColabFinish(context.Background(), testAuthCode)
		if !errors.Is(err, ErrAuthCodeInvalid) {
			t.Fatalf("ColabFinish() err = %v, want ErrAuthCodeInvalid", err)
		}
		if targetHits.Load() != 0 {
			t.Fatalf("token exchange redirect target hit %d time(s), want 0", targetHits.Load())
		}
	})

	t.Run("doBearerJSON rejects 302 redirect without forwarding Authorization Bearer", func(t *testing.T) {
		var targetHits atomic.Int32
		targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			targetHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"projects":[{"projectId":"hijacked-proj-123","lifecycleState":"ACTIVE"}]}`))
		}))
		defer targetSrv.Close()

		redirectSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, targetSrv.URL+"/v1/projects", http.StatusFound)
		}))
		defer redirectSrv.Close()

		vault := newMemoryVault()
		_ = vault.Set("colab", &ColabOneCredentials{
			Token:        "secret-crm-bearer-token",
			RefreshToken: "saved-ref",
			ClientID:     COLABONE_OAUTH_CLIENT_ID,
			ClientSecret: COLABONE_OAUTH_CLIENT_SECRET,
			Scopes:       PUBLIC_SCOPES,
			Expiry:       "2099-01-01T00:00:00Z",
		})
		_ = vault.Set("google_profile", map[string]string{
			"sub":   "sub-crm-test",
			"email": "crm@example.com",
		})

		auth := NewColabOneAuth(
			vault,
			WithCloudResourceManagerBaseURL(redirectSrv.URL),
			WithServiceUsageBaseURL(redirectSrv.URL),
			WithSleepFn(noSleep),
		)
		pid, reason := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
		if pid != "" {
			t.Fatalf("EnsureDriveQuotaProject() returned project %q across 302 redirect, want empty", pid)
		}
		if reason != "gcp_project_unavailable" {
			t.Fatalf("EnsureDriveQuotaProject() reason = %q, want gcp_project_unavailable", reason)
		}
		if targetHits.Load() != 0 {
			t.Fatalf("CRM redirect target hit %d time(s), want 0", targetHits.Load())
		}
	})
}

func TestColabOneCredentialsStringAndGoStringRedaction(t *testing.T) {
	var _ fmt.Stringer = ColabOneCredentials{}
	var _ fmt.Stringer = (*ColabOneCredentials)(nil)
	var _ fmt.GoStringer = ColabOneCredentials{}
	var _ fmt.GoStringer = (*ColabOneCredentials)(nil)

	const (
		secretToken        = "ya29.sentinel-access-token-9f8e7d"
		secretRefreshToken = "1//sentinel-refresh-token-6c5b4a"
		secretIDToken      = "eyJhbGciOiJSUzI1NiJ9.sentinel-id-token"
		secretClientSecret = "GOCSPX-sentinel-client-secret-321"
		secretRaptToken    = "sentinel-rapt-token-xyz789"
	)

	creds := ColabOneCredentials{
		Token:          secretToken,
		RefreshToken:   secretRefreshToken,
		IDToken:        secretIDToken,
		TokenURI:       COLABONE_OAUTH_TOKEN_URI,
		ClientID:       COLABONE_OAUTH_CLIENT_ID,
		ClientSecret:   secretClientSecret,
		Scopes:         []string{"openid", "https://www.googleapis.com/auth/colaboratory"},
		QuotaProjectID: "colabone-drive-1234567890",
		RaptToken:      secretRaptToken,
		UniverseDomain: "googleapis.com",
		Account:        "alice@example.com",
		Expiry:         "2099-01-01T00:00:00Z",
	}
	credsPtr := &creds

	secrets := []struct {
		field string
		val   string
	}{
		{"Token", secretToken},
		{"RefreshToken", secretRefreshToken},
		{"IDToken", secretIDToken},
		{"ClientSecret", secretClientSecret},
		{"RaptToken", secretRaptToken},
	}

	outputs := []struct {
		label string
		text  string
	}{
		{"value.String()", creds.String()},
		{"value.GoString()", creds.GoString()},
		{"ptr.String()", credsPtr.String()},
		{"ptr.GoString()", credsPtr.GoString()},
		{"%s value", fmt.Sprintf("%s", creds)},
		{"%v value", fmt.Sprintf("%v", creds)},
		{"%+v value", fmt.Sprintf("%+v", creds)},
		{"%#v value", fmt.Sprintf("%#v", creds)},
		{"%s ptr", fmt.Sprintf("%s", credsPtr)},
		{"%v ptr", fmt.Sprintf("%v", credsPtr)},
		{"%+v ptr", fmt.Sprintf("%+v", credsPtr)},
		{"%#v ptr", fmt.Sprintf("%#v", credsPtr)},
	}

	for _, out := range outputs {
		for _, sec := range secrets {
			if strings.Contains(out.text, sec.val) {
				t.Fatalf("%s leaked %s: %s", out.label, sec.field, out.text)
			}
		}
		if !strings.Contains(out.text, "[REDACTED]") {
			t.Fatalf("%s missing [REDACTED] marker: %s", out.label, out.text)
		}
		for _, safe := range []string{
			creds.TokenURI,
			creds.ClientID,
			creds.QuotaProjectID,
			creds.UniverseDomain,
			creds.Account,
			creds.Expiry,
			"openid",
		} {
			if !strings.Contains(out.text, safe) {
				t.Fatalf("%s missing non-secret metadata %q: %s", out.label, safe, out.text)
			}
		}
	}

	// Nil pointer formatting must not panic.
	var nilCreds *ColabOneCredentials
	if got := fmt.Sprintf("%v", nilCreds); got != "<nil>" {
		t.Fatalf("fmt.Sprintf(%%v, nil) = %q, want <nil>", got)
	}
	if got := fmt.Sprintf("%+v", nilCreds); got != "<nil>" {
		t.Fatalf("fmt.Sprintf(%%+v, nil) = %q, want <nil>", got)
	}
	if got := fmt.Sprintf("%s", nilCreds); got != "<nil>" {
		t.Fatalf("fmt.Sprintf(%%s, nil) = %q, want <nil>", got)
	}
	if got := fmt.Sprintf("%#v", nilCreds); got != "<nil>" {
		t.Fatalf("fmt.Sprintf(%%#v, nil) = %q, want <nil>", got)
	}

	// Zero-value struct must not falsely mark empty fields as [REDACTED].
	var emptyCreds ColabOneCredentials
	if strings.Contains(emptyCreds.String(), "[REDACTED]") || strings.Contains(emptyCreds.GoString(), "[REDACTED]") {
		t.Fatalf("empty credentials should not contain [REDACTED]: %s", emptyCreds.String())
	}

	// JSON serialization for vault persistence must remain completely intact.
	rawJSON, err := json.Marshal(&creds)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}
	var roundTrip ColabOneCredentials
	if err := json.Unmarshal(rawJSON, &roundTrip); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	if roundTrip.Token != secretToken ||
		roundTrip.RefreshToken != secretRefreshToken ||
		roundTrip.IDToken != secretIDToken ||
		roundTrip.ClientSecret != secretClientSecret ||
		roundTrip.RaptToken != secretRaptToken {
		t.Fatal("JSON round-trip must preserve all credential fields for vault persistence")
	}
}

func TestColabLoginLoopbackBlockedOnMutexDoesNotOverwriteManualFinish(t *testing.T) {
	const (
		codeAccountA = "4/account-a-loopback-code-1234567890"
		codeAccountB = "4/account-b-manual-code-0987654321"
	)

	t.Run("loopback callback waiting on auth mutex after manual ColabFinish skips exchange and preserves Account B", func(t *testing.T) {
		manualInToken := make(chan struct{})
		releaseManual := make(chan struct{})
		var manualInTokenOnce sync.Once
		var accountATokenCalls atomic.Int32
		var accountBTokenCalls atomic.Int32

		oauthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/token":
				_ = r.ParseForm()
				code := r.FormValue("code")
				switch code {
				case codeAccountB:
					accountBTokenCalls.Add(1)
					manualInTokenOnce.Do(func() { close(manualInToken) })
					<-releaseManual
					_, _ = w.Write([]byte(`{"access_token":"tok-acct-b","refresh_token":"ref-acct-b","expires_in":3600}`))
				case codeAccountA:
					accountATokenCalls.Add(1)
					_, _ = w.Write([]byte(`{"access_token":"tok-acct-a","refresh_token":"ref-acct-a","expires_in":3600}`))
				default:
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				}
			case "/userinfo":
				switch r.Header.Get("Authorization") {
				case "Bearer tok-acct-b":
					_, _ = w.Write([]byte(`{"email":"b@example.com","sub":"sub-acct-b"}`))
				case "Bearer tok-acct-a":
					_, _ = w.Write([]byte(`{"email":"a@example.com","sub":"sub-acct-a"}`))
				default:
					w.WriteHeader(http.StatusUnauthorized)
				}
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer oauthSrv.Close()

		vault := newMemoryVault()
		auth := NewColabOneAuth(
			vault,
			WithTokenURI(oauthSrv.URL+"/token"),
			WithUserInfoURL(oauthSrv.URL+"/userinfo"),
			WithHTTPClient(oauthSrv.Client()),
			WithLoopbackTimeout(5*time.Second),
			WithSleepFn(noSleep),
		)

		authURLCh := make(chan string, 1)
		loginErrCh := make(chan error, 1)
		go func() {
			loginErrCh <- auth.ColabLogin(context.Background(), func(u string) {
				authURLCh <- u
			})
		}()

		rawAuthURL := <-authURLCh
		parsed, err := url.Parse(rawAuthURL)
		if err != nil {
			t.Fatalf("parse authURL: %v", err)
		}
		cbURL := parsed.Query().Get("redirect_uri") +
			"?state=" + url.QueryEscape(parsed.Query().Get("state")) +
			"&code=" + url.QueryEscape(codeAccountA)

		// User switches to manual flow for Account B while loopback is listening.
		if _, err := auth.ColabBegin(); err != nil {
			t.Fatalf("ColabBegin failed: %v", err)
		}

		manualErrCh := make(chan error, 1)
		go func() {
			_, ferr := auth.ColabFinish(context.Background(), codeAccountB)
			manualErrCh <- ferr
		}()

		// Wait until manual ColabFinish holds a.mu inside /token (loginEvt is still unset).
		<-manualInToken
		if auth.loginEvt.IsSet() {
			t.Fatal("loginEvt must still be unset while manual ColabFinish is in flight")
		}

		// Fire loopback callback for Account A so it checks loginEvt (false) and blocks on a.mu.Lock().
		wroteReqCh := make(chan struct{})
		var wroteOnce sync.Once
		type cbResp struct {
			status int
			body   string
			err    error
		}
		cbRespCh := make(chan cbResp, 1)
		go func() {
			trace := &httptrace.ClientTrace{
				WroteRequest: func(httptrace.WroteRequestInfo) {
					wroteOnce.Do(func() { close(wroteReqCh) })
				},
			}
			req, err := http.NewRequestWithContext(
				httptrace.WithClientTrace(context.Background(), trace),
				http.MethodGet,
				cbURL,
				nil,
			)
			if err != nil {
				cbRespCh <- cbResp{err: err}
				return
			}
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				cbRespCh <- cbResp{err: err}
				return
			}
			b, rerr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			cbRespCh <- cbResp{status: resp.StatusCode, body: string(b), err: rerr}
		}()

		<-wroteReqCh
		// Give the loopback handler goroutine a brief scheduling window to pass loginEvt.IsSet()
		// and park on a.mu.Lock() inside colabFinishWithFlow before Account B finishes.
		time.Sleep(30 * time.Millisecond)

		close(releaseManual)

		if err := <-manualErrCh; err != nil {
			t.Fatalf("manual ColabFinish failed: %v", err)
		}
		if err := <-loginErrCh; err != nil {
			t.Fatalf("ColabLogin failed: %v", err)
		}
		res := <-cbRespCh
		if res.err != nil {
			t.Fatalf("loopback callback HTTP error: %v", res.err)
		}
		if res.status != http.StatusOK {
			t.Fatalf("loopback callback status = %d, want 200", res.status)
		}
		if !strings.Contains(res.body, "b@example.com") || strings.Contains(res.body, "a@example.com") {
			t.Fatalf("loopback callback rendered wrong account HTML: %s", res.body)
		}

		if got := accountBTokenCalls.Load(); got != 1 {
			t.Fatalf("Account B token calls = %d, want 1", got)
		}
		if got := accountATokenCalls.Load(); got != 0 {
			t.Fatalf("stale loopback Account A code must not be exchanged; got %d token call(s)", got)
		}

		var storedCreds ColabOneCredentials
		if ok, _ := vault.GetJSON("colab", &storedCreds); !ok || storedCreds.Token != "tok-acct-b" {
			t.Fatalf("vault credentials overwritten; got ok=%v creds=%+v, want tok-acct-b", ok, storedCreds)
		}
		var storedProfile map[string]string
		if ok, _ := vault.GetJSON("google_profile", &storedProfile); !ok || storedProfile["sub"] != "sub-acct-b" || storedProfile["email"] != "b@example.com" {
			t.Fatalf("vault profile overwritten; got ok=%v profile=%v, want sub-acct-b", ok, storedProfile)
		}
	})

	t.Run("direct explicitFlow guard aborts after loginEvt set and even after subsequent ColabBegin clears loginEvt", func(t *testing.T) {
		var tokenCalls atomic.Int32
		oauthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/token":
				tokenCalls.Add(1)
				_, _ = w.Write([]byte(`{"access_token":"tok-acct-b","refresh_token":"ref-acct-b","expires_in":3600}`))
			case "/userinfo":
				_, _ = w.Write([]byte(`{"email":"b@example.com","sub":"sub-acct-b"}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer oauthSrv.Close()

		vault := newMemoryVault()
		auth := NewColabOneAuth(
			vault,
			WithTokenURI(oauthSrv.URL+"/token"),
			WithUserInfoURL(oauthSrv.URL+"/userinfo"),
			WithHTTPClient(oauthSrv.Client()),
			WithSleepFn(noSleep),
		)

		// Simulate loopback flow started and manual ColabFinish succeeding.
		auth.mu.Lock()
		auth.loginEvt.Clear()
		staleLocalFlow, err := auth.newOAuthFlow("http://localhost:12345/")
		if err != nil {
			auth.mu.Unlock()
			t.Fatalf("newOAuthFlow: %v", err)
		}
		staleLocalFlow.doneCh = auth.loginEvt.WaitCh()
		auth.flow = staleLocalFlow
		auth.mu.Unlock()

		if _, err := auth.ColabBegin(); err != nil {
			t.Fatalf("ColabBegin: %v", err)
		}
		if _, err := auth.ColabFinish(context.Background(), codeAccountB); err != nil {
			t.Fatalf("ColabFinish: %v", err)
		}
		if tokenCalls.Load() != 1 {
			t.Fatalf("tokenCalls = %d, want 1", tokenCalls.Load())
		}

		// 1) loginEvt.IsSet() == true: explicitFlow must no-op without calling /token.
		if res, err := auth.colabFinishWithFlow(context.Background(), codeAccountA, staleLocalFlow); err != nil || res["ok"] != true {
			t.Fatalf("stale colabFinishWithFlow returned (%v, %v), want (ok:true, nil)", res, err)
		}
		if tokenCalls.Load() != 1 {
			t.Fatalf("stale explicitFlow exchanged code when loginEvt was set; tokenCalls=%d", tokenCalls.Load())
		}

		// 2) Even if a new ColabBegin clears loginEvt before stale loopback acquires a.mu,
		// staleLocalFlow.doneCh is already closed and must still no-op.
		if _, err := auth.ColabBegin(); err != nil {
			t.Fatalf("second ColabBegin: %v", err)
		}
		if res, err := auth.colabFinishWithFlow(context.Background(), codeAccountA, staleLocalFlow); err != nil || res["ok"] != true {
			t.Fatalf("stale colabFinishWithFlow after ColabBegin returned (%v, %v), want (ok:true, nil)", res, err)
		}
		if tokenCalls.Load() != 1 {
			t.Fatalf("stale explicitFlow exchanged code after ColabBegin cleared loginEvt; tokenCalls=%d", tokenCalls.Load())
		}
	})
}

func TestEnsureDriveQuotaProjectMissingInitialProfileGuards(t *testing.T) {
	seedAccountACredsOnly := func(v VaultStore) {
		_ = v.Set("colab", &ColabOneCredentials{
			Token:        "tok-acct-a",
			RefreshToken: "ref-acct-a",
			ClientID:     COLABONE_OAUTH_CLIENT_ID,
			ClientSecret: COLABONE_OAUTH_CLIENT_SECRET,
			Expiry:       "2099-01-01T00:00:00Z",
			Scopes:       PUBLIC_SCOPES,
		})
		// Intentionally omit "google_profile" so EnsureDriveQuotaProject starts with initialSub == "".
	}

	newAccountBOAuthServer := func(t *testing.T) *httptest.Server {
		t.Helper()
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/token":
				_, _ = w.Write([]byte(`{
					"access_token": "tok-acct-b",
					"refresh_token": "ref-acct-b",
					"expires_in": 3600,
					"scope": "openid https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/colaboratory https://www.googleapis.com/auth/drive.file"
				}`))
			case "/userinfo":
				_, _ = w.Write([]byte(`{"email":"b@example.com","sub":"sub-acct-b"}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
	}

	t.Run("missing google_profile + concurrent account switch does not persist cloud_project", func(t *testing.T) {
		oauthSrv := newAccountBOAuthServer(t)
		defer oauthSrv.Close()

		aInFlight := make(chan struct{})
		releaseA := make(chan struct{})
		var aInFlightOnce sync.Once

		gcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			authHeader := r.Header.Get("Authorization")
			switch r.URL.Path {
			case "/v1/projects":
				if authHeader == "Bearer tok-acct-a" {
					aInFlightOnce.Do(func() { close(aInFlight) })
					<-releaseA
					_, _ = w.Write([]byte(`{"projects":[{"projectId":"acct-a-leaked-proj","lifecycleState":"ACTIVE"}]}`))
					return
				}
				if authHeader == "Bearer tok-acct-b" {
					_, _ = w.Write([]byte(`{"projects":[{"projectId":"acct-b-owned-proj","lifecycleState":"ACTIVE"}]}`))
					return
				}
				w.WriteHeader(http.StatusUnauthorized)
			case "/v1/projects/acct-a-leaked-proj/services/drive.googleapis.com":
				_, _ = w.Write([]byte(`{"state":"ENABLED"}`))
			case "/v1/projects/acct-b-owned-proj/services/drive.googleapis.com":
				_, _ = w.Write([]byte(`{"state":"ENABLED"}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer gcpSrv.Close()

		vault := newMemoryVault()
		seedAccountACredsOnly(vault)
		auth := NewColabOneAuth(
			vault,
			WithTokenURI(oauthSrv.URL+"/token"),
			WithUserInfoURL(oauthSrv.URL+"/userinfo"),
			WithCloudResourceManagerBaseURL(gcpSrv.URL),
			WithServiceUsageBaseURL(gcpSrv.URL),
			WithHTTPClient(gcpSrv.Client()),
			WithSleepFn(noSleep),
		)

		type probeResult struct {
			pid    string
			reason string
		}
		resCh := make(chan probeResult, 1)
		go func() {
			pid, reason := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
			resCh <- probeResult{pid: pid, reason: reason}
		}()

		<-aInFlight
		if _, err := auth.ColabBegin(); err != nil {
			t.Fatalf("ColabBegin failed: %v", err)
		}
		if _, err := auth.ColabFinish(context.Background(), testAuthCode); err != nil {
			t.Fatalf("ColabFinish failed: %v", err)
		}
		close(releaseA)

		res := <-resCh
		if res.pid != "" || res.reason != "account_changed" {
			t.Fatalf("Account A probe got (%q, %q), want (\"\", \"account_changed\")", res.pid, res.reason)
		}
		var storedProj string
		if ok, _ := vault.GetJSON("cloud_project", &storedProj); ok {
			t.Fatalf("cloud_project leaked into Account B despite missing initial profile: %q", storedProj)
		}
		auth.quotaMu.Lock()
		cached := auth.quotaProbeCache
		auth.quotaMu.Unlock()
		if cached != nil {
			t.Fatalf("quotaProbeCache must remain nil after account switch, got %+v", cached)
		}

		bPID, bReason := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
		if bPID != "acct-b-owned-proj" || bReason != "reused_existing_project" {
			t.Fatalf("Account B probe got (%q, %q), want (\"acct-b-owned-proj\", \"reused_existing_project\")", bPID, bReason)
		}
	})

	t.Run("missing google_profile + concurrent account switch does not poison negative cache", func(t *testing.T) {
		oauthSrv := newAccountBOAuthServer(t)
		defer oauthSrv.Close()

		aInFlight := make(chan struct{})
		releaseA := make(chan struct{})
		var aInFlightOnce sync.Once
		var bListCalls atomic.Int32

		gcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			authHeader := r.Header.Get("Authorization")
			switch r.URL.Path {
			case "/v1/projects":
				if authHeader == "Bearer tok-acct-a" {
					aInFlightOnce.Do(func() { close(aInFlight) })
					<-releaseA
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"error":{"message":"Callers must accept Terms of Service"}}`))
					return
				}
				if authHeader == "Bearer tok-acct-b" {
					bListCalls.Add(1)
					_, _ = w.Write([]byte(`{"projects":[{"projectId":"acct-b-ready-proj","lifecycleState":"ACTIVE"}]}`))
					return
				}
				w.WriteHeader(http.StatusUnauthorized)
			case "/v1/projects/acct-b-ready-proj/services/drive.googleapis.com":
				_, _ = w.Write([]byte(`{"state":"ENABLED"}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer gcpSrv.Close()

		vault := newMemoryVault()
		seedAccountACredsOnly(vault)
		auth := NewColabOneAuth(
			vault,
			WithTokenURI(oauthSrv.URL+"/token"),
			WithUserInfoURL(oauthSrv.URL+"/userinfo"),
			WithCloudResourceManagerBaseURL(gcpSrv.URL),
			WithServiceUsageBaseURL(gcpSrv.URL),
			WithHTTPClient(gcpSrv.Client()),
			WithSleepFn(noSleep),
		)

		type probeResult struct {
			pid    string
			reason string
		}
		resCh := make(chan probeResult, 1)
		go func() {
			pid, reason := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
			resCh <- probeResult{pid: pid, reason: reason}
		}()

		<-aInFlight
		if _, err := auth.ColabBegin(); err != nil {
			t.Fatalf("ColabBegin failed: %v", err)
		}
		if _, err := auth.ColabFinish(context.Background(), testAuthCode); err != nil {
			t.Fatalf("ColabFinish failed: %v", err)
		}
		close(releaseA)

		res := <-resCh
		if res.pid != "" || res.reason != "account_changed" {
			t.Fatalf("Account A failed probe got (%q, %q), want (\"\", \"account_changed\")", res.pid, res.reason)
		}
		if st := auth.DriveQuotaStatus(); st["reason"] != "" {
			t.Fatalf("Account B DriveQuotaStatus poisoned with reason=%q", st["reason"])
		}

		bPID, bReason := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
		if bPID != "acct-b-ready-proj" || bReason != "reused_existing_project" {
			t.Fatalf("Account B probe got (%q, %q), want (\"acct-b-ready-proj\", \"reused_existing_project\")", bPID, bReason)
		}
		if bListCalls.Load() != 1 {
			t.Fatalf("Account B /v1/projects calls = %d, want 1", bListCalls.Load())
		}
	})

	t.Run("missing google_profile + concurrent sign-out/invalidation blocks both cloud_project and negative cache", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			fail403 bool
		}{
			{name: "project discovery", fail403: false},
			{name: "negative cache 403", fail403: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				inFlight := make(chan struct{})
				release := make(chan struct{})
				var once sync.Once

				gcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/v1/projects":
						once.Do(func() { close(inFlight) })
						<-release
						if tc.fail403 {
							w.WriteHeader(http.StatusForbidden)
							_, _ = w.Write([]byte(`{"error":{"message":"Callers must accept Terms of Service"}}`))
							return
						}
						_, _ = w.Write([]byte(`{"projects":[{"projectId":"stale-signout-proj","lifecycleState":"ACTIVE"}]}`))
					case "/v1/projects/stale-signout-proj/services/drive.googleapis.com":
						_, _ = w.Write([]byte(`{"state":"ENABLED"}`))
					default:
						w.WriteHeader(http.StatusNotFound)
					}
				}))
				defer gcpSrv.Close()

				vault := newMemoryVault()
				seedAccountACredsOnly(vault)
				auth := NewColabOneAuth(
					vault,
					WithCloudResourceManagerBaseURL(gcpSrv.URL),
					WithServiceUsageBaseURL(gcpSrv.URL),
					WithHTTPClient(gcpSrv.Client()),
					WithSleepFn(noSleep),
				)

				type probeResult struct {
					pid    string
					reason string
				}
				resCh := make(chan probeResult, 1)
				go func() {
					pid, reason := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
					resCh <- probeResult{pid: pid, reason: reason}
				}()

				<-inFlight
				_ = vault.Delete("colab")
				auth.InvalidateDriveQuotaProject()
				close(release)

				res := <-resCh
				if res.pid != "" || res.reason != "account_changed" {
					t.Fatalf("got (%q, %q), want (\"\", \"account_changed\")", res.pid, res.reason)
				}
				var storedProj string
				if ok, _ := vault.GetJSON("cloud_project", &storedProj); ok {
					t.Fatalf("cloud_project resurrected after sign-out: %q", storedProj)
				}
				auth.quotaMu.Lock()
				cached := auth.quotaProbeCache
				auth.quotaMu.Unlock()
				if cached != nil {
					t.Fatalf("quotaProbeCache populated after sign-out: %+v", cached)
				}
			})
		}
	})

	t.Run("missing google_profile + concurrent account switch aborts before Step 3 project creation", func(t *testing.T) {
		oauthSrv := newAccountBOAuthServer(t)
		defer oauthSrv.Close()

		aInFlight := make(chan struct{})
		releaseA := make(chan struct{})
		var aInFlightOnce sync.Once
		var createCalls atomic.Int32

		gcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/v1/projects":
				aInFlightOnce.Do(func() { close(aInFlight) })
				<-releaseA
				_, _ = w.Write([]byte(`{}`))
			case r.Method == http.MethodPost && r.URL.Path == "/v1/projects":
				createCalls.Add(1)
				_, _ = w.Write([]byte(`{"done":true}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer gcpSrv.Close()

		vault := newMemoryVault()
		seedAccountACredsOnly(vault)
		auth := NewColabOneAuth(
			vault,
			WithTokenURI(oauthSrv.URL+"/token"),
			WithUserInfoURL(oauthSrv.URL+"/userinfo"),
			WithCloudResourceManagerBaseURL(gcpSrv.URL),
			WithServiceUsageBaseURL(gcpSrv.URL),
			WithHTTPClient(gcpSrv.Client()),
			WithSleepFn(noSleep),
		)

		type probeResult struct {
			pid    string
			reason string
		}
		resCh := make(chan probeResult, 1)
		go func() {
			pid, reason := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
			resCh <- probeResult{pid: pid, reason: reason}
		}()

		<-aInFlight
		if _, err := auth.ColabBegin(); err != nil {
			t.Fatalf("ColabBegin failed: %v", err)
		}
		if _, err := auth.ColabFinish(context.Background(), testAuthCode); err != nil {
			t.Fatalf("ColabFinish failed: %v", err)
		}
		close(releaseA)

		res := <-resCh
		if res.pid != "" || res.reason != "account_changed" {
			t.Fatalf("got (%q, %q), want (\"\", \"account_changed\")", res.pid, res.reason)
		}
		if got := createCalls.Load(); got != 0 {
			t.Fatalf("Step 3 must not POST /v1/projects after epoch changed; got %d call(s)", got)
		}
	})
}

func TestEnsureDriveQuotaProjectSameAccountInvalidatePreventsStaleWriteback(t *testing.T) {
	seedSameAccount := func(v VaultStore, expiry string) {
		_ = v.Set("colab", &ColabOneCredentials{
			Token:        "tok-same",
			RefreshToken: "ref-same",
			ClientID:     COLABONE_OAUTH_CLIENT_ID,
			ClientSecret: COLABONE_OAUTH_CLIENT_SECRET,
			Expiry:       expiry,
			Scopes:       PUBLIC_SCOPES,
		})
		_ = v.Set("google_profile", map[string]string{
			"sub":   "sub-same-account",
			"email": "same@example.com",
		})
	}

	t.Run("same-account InvalidateDriveQuotaProject during in-flight discovery blocks stale cloud_project writeback", func(t *testing.T) {
		inFlight := make(chan struct{})
		release := make(chan struct{})
		var listCalls atomic.Int32

		gcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/v1/projects":
				if listCalls.Add(1) == 1 {
					close(inFlight)
					<-release
					_, _ = w.Write([]byte(`{"projects":[{"projectId":"stale-same-proj","lifecycleState":"ACTIVE"}]}`))
					return
				}
				_, _ = w.Write([]byte(`{"projects":[{"projectId":"fresh-same-proj","lifecycleState":"ACTIVE"}]}`))
			case "/v1/projects/stale-same-proj/services/drive.googleapis.com",
				"/v1/projects/fresh-same-proj/services/drive.googleapis.com":
				_, _ = w.Write([]byte(`{"state":"ENABLED"}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer gcpSrv.Close()

		vault := newMemoryVault()
		seedSameAccount(vault, "2099-01-01T00:00:00Z")
		auth := NewColabOneAuth(
			vault,
			WithCloudResourceManagerBaseURL(gcpSrv.URL),
			WithServiceUsageBaseURL(gcpSrv.URL),
			WithHTTPClient(gcpSrv.Client()),
			WithSleepFn(noSleep),
		)

		type probeResult struct {
			pid    string
			reason string
		}
		resCh := make(chan probeResult, 1)
		go func() {
			pid, reason := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
			resCh <- probeResult{pid: pid, reason: reason}
		}()

		<-inFlight
		// Same account stays signed in; only InvalidateDriveQuotaProject() is invoked.
		auth.InvalidateDriveQuotaProject()
		close(release)

		res := <-resCh
		if res.pid != "" || res.reason != "account_changed" {
			t.Fatalf("stale probe got (%q, %q), want (\"\", \"account_changed\")", res.pid, res.reason)
		}
		var storedProj string
		if ok, _ := vault.GetJSON("cloud_project", &storedProj); ok {
			t.Fatalf("same-account invalidate failed to prevent stale cloud_project writeback: %q", storedProj)
		}
		auth.quotaMu.Lock()
		cached := auth.quotaProbeCache
		auth.quotaMu.Unlock()
		if cached != nil {
			t.Fatalf("same-account invalidate left stale quotaProbeCache: %+v", cached)
		}

		// Subsequent probe under the new epoch succeeds and persists the fresh project.
		pid2, reason2 := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
		if pid2 != "fresh-same-proj" || reason2 != "reused_existing_project" {
			t.Fatalf("fresh probe got (%q, %q), want (\"fresh-same-proj\", \"reused_existing_project\")", pid2, reason2)
		}
	})

	t.Run("same-account InvalidateDriveQuotaProject during in-flight 403 blocks stale negative cache writeback", func(t *testing.T) {
		inFlight := make(chan struct{})
		release := make(chan struct{})
		var listCalls atomic.Int32

		gcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/v1/projects":
				if listCalls.Add(1) == 1 {
					close(inFlight)
					<-release
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"error":{"message":"Callers must accept Terms of Service"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"projects":[{"projectId":"post-tos-fresh-proj","lifecycleState":"ACTIVE"}]}`))
			case "/v1/projects/post-tos-fresh-proj/services/drive.googleapis.com":
				_, _ = w.Write([]byte(`{"state":"ENABLED"}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer gcpSrv.Close()

		vault := newMemoryVault()
		seedSameAccount(vault, "2099-01-01T00:00:00Z")
		auth := NewColabOneAuth(
			vault,
			WithCloudResourceManagerBaseURL(gcpSrv.URL),
			WithServiceUsageBaseURL(gcpSrv.URL),
			WithHTTPClient(gcpSrv.Client()),
			WithSleepFn(noSleep),
		)

		type probeResult struct {
			pid    string
			reason string
		}
		resCh := make(chan probeResult, 1)
		go func() {
			pid, reason := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
			resCh <- probeResult{pid: pid, reason: reason}
		}()

		<-inFlight
		auth.InvalidateDriveQuotaProject()
		close(release)

		res := <-resCh
		if res.pid != "" || res.reason != "account_changed" {
			t.Fatalf("stale 403 probe got (%q, %q), want (\"\", \"account_changed\")", res.pid, res.reason)
		}
		auth.quotaMu.Lock()
		cached := auth.quotaProbeCache
		auth.quotaMu.Unlock()
		if cached != nil {
			t.Fatalf("stale 403 poisoned quotaProbeCache after same-account invalidate: %+v", cached)
		}
		if st := auth.DriveQuotaStatus(); st["reason"] != "" {
			t.Fatalf("DriveQuotaStatus reason = %q, want empty", st["reason"])
		}

		// Next call must not hit a negative cache and must discover the new project.
		pid2, reason2 := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
		if pid2 != "post-tos-fresh-proj" || reason2 != "reused_existing_project" {
			t.Fatalf("retry probe got (%q, %q), want (\"post-tos-fresh-proj\", \"reused_existing_project\")", pid2, reason2)
		}
		if listCalls.Load() != 2 {
			t.Fatalf("listCalls = %d, want 2", listCalls.Load())
		}
	})

	t.Run("same-account InvalidateDriveQuotaProject during token refresh aborts before querying GCP", func(t *testing.T) {
		refreshInFlight := make(chan struct{})
		releaseRefresh := make(chan struct{})
		var gcpCalls atomic.Int32

		tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(refreshInFlight)
			<-releaseRefresh
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"refreshed-tok","expires_in":3600,"token_type":"Bearer"}`))
		}))
		defer tokenSrv.Close()

		gcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gcpCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}))
		defer gcpSrv.Close()

		vault := newMemoryVault()
		seedSameAccount(vault, "2000-01-01T00:00:00Z")
		auth := NewColabOneAuth(
			vault,
			WithTokenURI(tokenSrv.URL),
			WithCloudResourceManagerBaseURL(gcpSrv.URL),
			WithServiceUsageBaseURL(gcpSrv.URL),
			WithHTTPClient(tokenSrv.Client()),
			WithSleepFn(noSleep),
		)

		type probeResult struct {
			pid    string
			reason string
		}
		resCh := make(chan probeResult, 1)
		go func() {
			pid, reason := auth.EnsureDriveQuotaProject(context.Background(), nil, false)
			resCh <- probeResult{pid: pid, reason: reason}
		}()

		<-refreshInFlight
		auth.InvalidateDriveQuotaProject()
		close(releaseRefresh)

		res := <-resCh
		if res.pid != "" || res.reason != "account_changed" {
			t.Fatalf("probe after invalidate during refresh got (%q, %q), want (\"\", \"account_changed\")", res.pid, res.reason)
		}
		if got := gcpCalls.Load(); got != 0 {
			t.Fatalf("expected 0 GCP calls after early epoch guard, got %d", got)
		}
	})
}

func TestProfileConcurrentAccountSwitchAndSignOutNoStaleWrite(t *testing.T) {
	t.Run("concurrent ColabFinish account switch prevents stale Account A profile overwrite", func(t *testing.T) {
		aInFlight := make(chan struct{})
		releaseA := make(chan struct{})
		var aOnce sync.Once

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/token":
				_, _ = w.Write([]byte(`{"access_token":"tok-acct-b","refresh_token":"ref-acct-b","expires_in":3600}`))
			case "/userinfo":
				if r.Header.Get("Authorization") == "Bearer tok-acct-a" {
					aOnce.Do(func() { close(aInFlight) })
					<-releaseA
					_, _ = w.Write([]byte(`{"email":"a@example.com","sub":"sub-acct-a"}`))
					return
				}
				if r.Header.Get("Authorization") == "Bearer tok-acct-b" {
					_, _ = w.Write([]byte(`{"email":"b@example.com","sub":"sub-acct-b"}`))
					return
				}
				w.WriteHeader(http.StatusUnauthorized)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		vault := newMemoryVault()
		// Seed Account A credentials WITHOUT cached google_profile so Profile() fetches it.
		_ = vault.Set("colab", &ColabOneCredentials{
			Token:        "tok-acct-a",
			RefreshToken: "ref-acct-a",
			ClientID:     COLABONE_OAUTH_CLIENT_ID,
			ClientSecret: COLABONE_OAUTH_CLIENT_SECRET,
			Expiry:       "2099-01-01T00:00:00Z",
			Scopes:       PUBLIC_SCOPES,
		})

		auth := NewColabOneAuth(
			vault,
			WithTokenURI(srv.URL+"/token"),
			WithUserInfoURL(srv.URL+"/userinfo"),
			WithHTTPClient(srv.Client()),
			WithSleepFn(noSleep),
		)

		type profResult struct {
			prof map[string]string
			err  error
		}
		resCh := make(chan profResult, 1)
		go func() {
			p, err := auth.Profile(context.Background())
			resCh <- profResult{prof: p, err: err}
		}()

		<-aInFlight
		if _, err := auth.ColabBegin(); err != nil {
			t.Fatalf("ColabBegin failed: %v", err)
		}
		if _, err := auth.ColabFinish(context.Background(), testAuthCode); err != nil {
			t.Fatalf("ColabFinish failed: %v", err)
		}
		close(releaseA)

		res := <-resCh
		if res.err != nil {
			t.Fatalf("in-flight Account A Profile() unexpected error: %v", res.err)
		}

		// Vault must still hold Account B's profile, never overwritten by Account A.
		var stored map[string]string
		ok, err := vault.GetJSON("google_profile", &stored)
		if err != nil || !ok {
			t.Fatalf("expected Account B google_profile in vault, ok=%v err=%v", ok, err)
		}
		if stored["sub"] != "sub-acct-b" || stored["email"] != "b@example.com" {
			t.Fatalf("vault google_profile overwritten by stale Account A fetch: %+v", stored)
		}

		// Subsequent Profile() must return Account B's cached profile.
		gotB, err := auth.Profile(context.Background())
		if err != nil {
			t.Fatalf("Profile() after switch failed: %v", err)
		}
		if gotB["sub"] != "sub-acct-b" || gotB["email"] != "b@example.com" {
			t.Fatalf("Profile() after switch = %+v, want Account B", gotB)
		}
	})

	t.Run("concurrent sign-out with InvalidateDriveQuotaProject does not resurrect google_profile", func(t *testing.T) {
		inFlight := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			once.Do(func() { close(inFlight) })
			<-release
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"email":"signedout@example.com","sub":"sub-signedout"}`))
		}))
		defer srv.Close()

		vault := newMemoryVault()
		_ = vault.Set("colab", &ColabOneCredentials{
			Token:        "tok-signout",
			RefreshToken: "ref-signout",
			ClientID:     COLABONE_OAUTH_CLIENT_ID,
			Expiry:       "2099-01-01T00:00:00Z",
		})

		auth := NewColabOneAuth(
			vault,
			WithUserInfoURL(srv.URL),
			WithHTTPClient(srv.Client()),
			WithSleepFn(noSleep),
		)

		errCh := make(chan error, 1)
		go func() {
			_, err := auth.Profile(context.Background())
			errCh <- err
		}()

		<-inFlight
		_ = vault.Delete("google_profile")
		_ = vault.Delete("colab")
		auth.InvalidateDriveQuotaProject()
		close(release)

		if err := <-errCh; err != nil {
			t.Fatalf("Profile() unexpected error: %v", err)
		}
		var stored map[string]string
		if ok, _ := vault.GetJSON("google_profile", &stored); ok {
			t.Fatalf("sign-out must not resurrect google_profile, got %+v", stored)
		}
	})

	t.Run("concurrent vault delete of colab alone prevents resurrecting google_profile", func(t *testing.T) {
		inFlight := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			once.Do(func() { close(inFlight) })
			<-release
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"email":"deleted@example.com","sub":"sub-deleted"}`))
		}))
		defer srv.Close()

		vault := newMemoryVault()
		_ = vault.Set("colab", &ColabOneCredentials{
			Token:        "tok-deleted",
			RefreshToken: "ref-deleted",
			ClientID:     COLABONE_OAUTH_CLIENT_ID,
			Expiry:       "2099-01-01T00:00:00Z",
		})

		auth := NewColabOneAuth(
			vault,
			WithUserInfoURL(srv.URL),
			WithHTTPClient(srv.Client()),
			WithSleepFn(noSleep),
		)

		errCh := make(chan error, 1)
		go func() {
			_, err := auth.Profile(context.Background())
			errCh <- err
		}()

		<-inFlight
		// Delete "colab" even without bumping quotaEpoch yet.
		_ = vault.Delete("colab")
		close(release)

		if err := <-errCh; err != nil {
			t.Fatalf("Profile() unexpected error: %v", err)
		}
		var stored map[string]string
		if ok, _ := vault.GetJSON("google_profile", &stored); ok {
			t.Fatalf("deleted colab creds must not resurrect google_profile, got %+v", stored)
		}
	})

	t.Run("concurrent InvalidateDriveQuotaProject alone prevents stale google_profile writeback", func(t *testing.T) {
		inFlight := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			once.Do(func() { close(inFlight) })
			<-release
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"email":"stale@example.com","sub":"sub-stale"}`))
		}))
		defer srv.Close()

		vault := newMemoryVault()
		_ = vault.Set("colab", &ColabOneCredentials{
			Token:        "tok-stale",
			RefreshToken: "ref-stale",
			ClientID:     COLABONE_OAUTH_CLIENT_ID,
			Expiry:       "2099-01-01T00:00:00Z",
		})

		auth := NewColabOneAuth(
			vault,
			WithUserInfoURL(srv.URL),
			WithHTTPClient(srv.Client()),
			WithSleepFn(noSleep),
		)

		errCh := make(chan error, 1)
		go func() {
			_, err := auth.Profile(context.Background())
			errCh <- err
		}()

		<-inFlight
		auth.InvalidateDriveQuotaProject()
		close(release)

		if err := <-errCh; err != nil {
			t.Fatalf("Profile() unexpected error: %v", err)
		}
		var stored map[string]string
		if ok, _ := vault.GetJSON("google_profile", &stored); ok {
			t.Fatalf("epoch change must block stale google_profile writeback, got %+v", stored)
		}
	})
}

func TestFlexInt64UnmarshalJSONRejectsNaNInfAndOverflow(t *testing.T) {
	validCases := []struct {
		name string
		raw  string
		want int64
	}{
		{"null", `null`, 0},
		{"empty_string", `""`, 0},
		{"whitespace_only_string", `"   "`, 0},
		{"tabs_newlines_only_string", `"\t\n  "`, 0},
		{"int", `3600`, 3600},
		{"quoted_int", `"3600"`, 3600},
		{"quoted_int_padded", `" 3600 "`, 3600},
		{"quoted_int_unicode_escaped", `"\u0033\u0036\u0030\u0030"`, 3600},
		{"quoted_int_unicode_padded", `"\u00203600\u0020"`, 3600},
		{"float", `3600.9`, 3600},
		{"quoted_float", `"3600.9"`, 3600},
		{"quoted_float_exact", `"3600.0"`, 3600},
		{"quoted_float_padded", `" 3600.0 "`, 3600},
		{"sci", `1.5e3`, 1500},
		{"quoted_sci", `"1.5e3"`, 1500},
		{"max_int64", `9223372036854775807`, math.MaxInt64},
		{"quoted_max_int64", `"9223372036854775807"`, math.MaxInt64},
		{"min_int64", `-9223372036854775808`, math.MinInt64},
		{"quoted_min_int64_float", `"-9223372036854775808.0"`, math.MinInt64},
	}
	for _, tc := range validCases {
		t.Run("valid_"+tc.name, func(t *testing.T) {
			got := flexInt64(99)
			if err := json.Unmarshal([]byte(tc.raw), &got); err != nil {
				t.Fatalf("Unmarshal(%s) unexpected error: %v", tc.raw, err)
			}
			if int64(got) != tc.want {
				t.Fatalf("Unmarshal(%s) = %d, want %d", tc.raw, int64(got), tc.want)
			}
		})
	}

	invalidCases := []struct {
		name string
		raw  string
	}{
		{"quoted_nan", `"NaN"`},
		{"quoted_nan_lower", `"nan"`},
		{"quoted_neg_nan", `"-NaN"`},
		{"quoted_inf", `"Inf"`},
		{"quoted_pos_inf", `"+Inf"`},
		{"quoted_neg_inf", `"-Inf"`},
		{"quoted_infinity", `"Infinity"`},
		{"quoted_neg_infinity", `"-Infinity"`},
		{"quoted_go_underscore_int", `"3_600"`},
		{"quoted_go_underscore_float", `"3_600.0"`},
		{"quoted_go_underscore_sci", `"1_000e2"`},
		{"quoted_go_hex_float_lower", `"0x1p4"`},
		{"quoted_go_hex_float_upper", `"0X1P4"`},
		{"quoted_go_hex_float_neg", `"-0x1.8p3"`},
		{"quoted_go_hex_float_pos", `"+0x1p0"`},
		{"max_int64_plus_1", `9223372036854775808`},
		{"quoted_max_int64_plus_1", `"9223372036854775808"`},
		{"max_int64_plus_1_float", `9223372036854775808.0`},
		{"quoted_max_int64_plus_1_float", `"9223372036854775808.0"`},
		{"min_int64_minus_1", `-9223372036854775809`},
		{"quoted_min_int64_minus_1", `"-9223372036854775809"`},
		{"large_pos_sci", `1e20`},
		{"quoted_large_pos_sci", `"1e20"`},
		{"large_neg_sci", `-1e20`},
		{"quoted_large_neg_sci", `"-1e20"`},
		{"float64_overflow", `1e309`},
		{"quoted_float64_overflow", `"1e309"`},
		{"non_numeric", `"abc"`},
	}
	for _, tc := range invalidCases {
		t.Run("invalid_"+tc.name, func(t *testing.T) {
			var got flexInt64
			if err := json.Unmarshal([]byte(tc.raw), &got); err == nil {
				t.Fatalf("Unmarshal(%s) succeeded with %d, want error", tc.raw, int64(got))
			}
		})
	}

	t.Run("direct_unmarshal_rejects_go_only_escapes_and_syntax", func(t *testing.T) {
		for _, raw := range []string{`"\x33\x36"`, `"3_600"`, `"0x1p4"`, `3_600`, `0x1p4`, `"3600`} {
			var got flexInt64
			if err := got.UnmarshalJSON([]byte(raw)); err == nil {
				t.Errorf("flexInt64.UnmarshalJSON(%s) succeeded with %d, want error", raw, int64(got))
			}
		}
	})
}

func TestTokenExpiryDurationClampingPreventsOverflow(t *testing.T) {
	if got := clampTokenExpiryDuration(-10); got != 0 {
		t.Fatalf("clampTokenExpiryDuration(-10) = %v, want 0", got)
	}
	if got := clampTokenExpiryDuration(0); got != 0 {
		t.Fatalf("clampTokenExpiryDuration(0) = %v, want 0", got)
	}
	if got := clampTokenExpiryDuration(3600); got != 3600*time.Second {
		t.Fatalf("clampTokenExpiryDuration(3600) = %v, want 1h", got)
	}
	wantMax := time.Duration(maxTokenExpirySeconds) * time.Second
	if wantMax <= 0 {
		t.Fatalf("wantMax duration must be positive, got %v", wantMax)
	}
	for _, huge := range []int64{maxTokenExpirySeconds, maxTokenExpirySeconds + 1, 10_000_000_000, math.MaxInt64} {
		if got := clampTokenExpiryDuration(huge); got != wantMax {
			t.Fatalf("clampTokenExpiryDuration(%d) = %v, want %v", huge, got, wantMax)
		}
	}

	now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	wantExpiry := now.Add(wantMax).Format(time.RFC3339)

	t.Run("Credentials refresh clamps huge expires_in without wrapping to past", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"refreshed-tok","expires_in":9223372036854775807,"token_type":"Bearer"}`))
		}))
		defer srv.Close()

		vault := newMemoryVault()
		_ = vault.Set("colab", &ColabOneCredentials{
			Token:        "expired",
			RefreshToken: "ref-tok",
			Expiry:       now.Add(-time.Hour).Format(time.RFC3339),
		})
		auth := NewColabOneAuth(
			vault,
			WithTokenURI(srv.URL),
			WithNowFn(func() time.Time { return now }),
			WithHTTPClient(srv.Client()),
		)
		creds, err := auth.Credentials(context.Background(), "colab")
		if err != nil {
			t.Fatalf("Credentials() error: %v", err)
		}
		if creds.Expiry != wantExpiry {
			t.Fatalf("creds.Expiry = %q, want %q", creds.Expiry, wantExpiry)
		}
		if !creds.ValidAt(now) {
			t.Fatalf("refreshed credentials with clamped expiry should be valid at %v (expiry=%q)", now, creds.Expiry)
		}
	})

	t.Run("ColabFinish token exchange clamps huge expires_in without wrapping to past", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/token":
				_, _ = w.Write([]byte(`{"access_token":"exchanged-tok","refresh_token":"exchanged-ref","expires_in":9223372036854775807}`))
			case "/userinfo":
				_, _ = w.Write([]byte(`{"email":"huge@example.com","sub":"sub-huge"}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		vault := newMemoryVault()
		auth := NewColabOneAuth(
			vault,
			WithTokenURI(srv.URL+"/token"),
			WithUserInfoURL(srv.URL+"/userinfo"),
			WithNowFn(func() time.Time { return now }),
			WithHTTPClient(srv.Client()),
			WithSleepFn(noSleep),
		)
		if _, err := auth.ColabBegin(); err != nil {
			t.Fatalf("ColabBegin() error: %v", err)
		}
		if _, err := auth.ColabFinish(context.Background(), testAuthCode); err != nil {
			t.Fatalf("ColabFinish() error: %v", err)
		}
		var saved ColabOneCredentials
		if ok, err := vault.GetJSON("colab", &saved); err != nil || !ok {
			t.Fatalf("vault missing colab credentials: ok=%v err=%v", ok, err)
		}
		if saved.Expiry != wantExpiry {
			t.Fatalf("saved.Expiry = %q, want %q", saved.Expiry, wantExpiry)
		}
		if !saved.ValidAt(now) {
			t.Fatalf("exchanged credentials with clamped expiry should be valid at %v (expiry=%q)", now, saved.Expiry)
		}
	})

	t.Run("Credentials refresh rejects NaN or overflowing float expires_in cleanly", func(t *testing.T) {
		for _, badBody := range []string{
			`{"access_token":"tok","expires_in":"NaN"}`,
			`{"access_token":"tok","expires_in":"Inf"}`,
			`{"access_token":"tok","expires_in":1e20}`,
		} {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(badBody))
			}))
			vault := newMemoryVault()
			_ = vault.Set("colab", &ColabOneCredentials{
				Token:        "expired",
				RefreshToken: "ref-tok",
				Expiry:       now.Add(-time.Hour).Format(time.RFC3339),
			})
			auth := NewColabOneAuth(
				vault,
				WithTokenURI(srv.URL),
				WithNowFn(func() time.Time { return now }),
				WithHTTPClient(srv.Client()),
			)
			_, err := auth.Credentials(context.Background(), "colab")
			srv.Close()
			if !errors.Is(err, ErrAuthExpired) {
				t.Fatalf("body %s: Credentials() err = %v, want ErrAuthExpired", badBody, err)
			}
		}
	})
}

func TestRefreshRejectsUntrustedTokenURIWithoutOverride(t *testing.T) {
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)

	var hits atomic.Int32
	evilSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"stolen","expires_in":3600}`))
	}))
	defer evilSrv.Close()

	untrustedURIs := []string{
		evilSrv.URL,
		evilSrv.URL + "/token",
		"http://oauth2.googleapis.com/token",
		"https://oauth2.googleapis.com/token?extra=1",
		"https://oauth2.googleapis.com/token?",
		"https://oauth2.googleapis.com/token#frag",
		"https://oauth2.googleapis.com/token#",
		"https://user:pass@oauth2.googleapis.com/token",
		"https://oauth2.googleapis.com:8443/token",
		"https://oauth2.googleapis.com.evil.example/token",
		"https://accounts.google.com/o/oauth2/token/",
		" https://oauth2.googleapis.com/token ",
	}

	for _, badURI := range untrustedURIs {
		t.Run(badURI, func(t *testing.T) {
			v := newMemoryVault()
			_ = v.Set("colab", &ColabOneCredentials{
				Token:        "expired",
				RefreshToken: "1//super-secret-refresh-token",
				TokenURI:     badURI,
				Expiry:       now.Add(-time.Hour).Format(time.RFC3339),
			})
			a := NewColabOneAuth(v, WithNowFn(func() time.Time { return now }), WithHTTPClient(evilSrv.Client()))
			if _, err := a.Credentials(context.Background(), "colab"); !errors.Is(err, ErrUntrustedTokenURI) {
				t.Fatalf("TokenURI=%q: err=%v, want ErrUntrustedTokenURI", badURI, err)
			}
		})
	}
	if hits.Load() != 0 {
		t.Fatalf("untrusted token server was contacted %d times, want 0", hits.Load())
	}

	// Verify exact allowlist accepts empty and both official Google OAuth token URIs.
	for _, allowed := range []string{
		"",
		"https://oauth2.googleapis.com/token",
		"https://accounts.google.com/o/oauth2/token",
	} {
		got, err := validateGoogleTokenURI(allowed)
		if err != nil {
			t.Fatalf("validateGoogleTokenURI(%q) unexpected error: %v", allowed, err)
		}
		if got != allowed {
			t.Fatalf("validateGoogleTokenURI(%q) = %q, want %q", allowed, got, allowed)
		}
	}
}

func TestBuildRcloneConfRejectsINIInjectionInClientCredentials(t *testing.T) {
	cases := []struct {
		name         string
		clientID     string
		clientSecret string
	}{
		{"newline in client_id", "cid\n[evil]\ntype = local", "secret"},
		{"carriage return in client_id", "cid\revil = 1", "secret"},
		{"nul in client_id", "cid\x00evil", "secret"},
		{"newline in client_secret", "cid", "sec\n[evil]\ntype = local"},
		{"carriage return in client_secret", "cid", "sec\revil = 1"},
		{"nul in client_secret", "cid", "sec\x00evil"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newMemoryVault()
			_ = v.Set("colab", &ColabOneCredentials{
				Token:        "access-tok",
				RefreshToken: "refresh-tok",
				ClientID:     tc.clientID,
				ClientSecret: tc.clientSecret,
				Expiry:       "2099-01-01T00:00:00Z",
				Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
			})
			a := NewColabOneAuth(v)
			conf, err := a.BuildRcloneConf(context.Background(), nil)
			if err == nil || conf != "" {
				t.Fatalf("expected error and empty conf for %s, got conf=%q err=%v", tc.name, conf, err)
			}
		})
	}
}

func TestColabLoginClearsFlowOnFailureAndBindsIPv6LoopbackBestEffort(t *testing.T) {
	t.Run("timeout clears pending flow", func(t *testing.T) {
		a := NewColabOneAuth(newMemoryVault(), WithLoopbackTimeout(40*time.Millisecond))
		err := a.ColabLogin(context.Background(), func(_ string) {})
		if !errors.Is(err, ErrLoginTimeout) {
			t.Fatalf("err = %v, want ErrLoginTimeout", err)
		}
		if a.HasPendingFlow() {
			t.Fatal("HasPendingFlow() must be false after ColabLogin timeout")
		}
	})

	t.Run("context cancel clears pending flow", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		a := NewColabOneAuth(newMemoryVault())
		err := a.ColabLogin(ctx, func(_ string) { cancel() })
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if a.HasPendingFlow() {
			t.Fatal("HasPendingFlow() must be false after ColabLogin cancel")
		}
	})

	t.Run("access_denied clears pending flow", func(t *testing.T) {
		a := NewColabOneAuth(newMemoryVault())
		err := a.ColabLogin(context.Background(), func(authURL string) {
			go func() {
				u, _ := url.Parse(authURL)
				redirectURI := u.Query().Get("redirect_uri")
				state := u.Query().Get("state")
				resp, rerr := http.Get(redirectURI + "?state=" + url.QueryEscape(state) + "&error=access_denied")
				if rerr == nil {
					_, _ = io.ReadAll(resp.Body)
					_ = resp.Body.Close()
				}
			}()
		})
		if !errors.Is(err, ErrAuthCancelled) {
			t.Fatalf("err = %v, want ErrAuthCancelled", err)
		}
		if a.HasPendingFlow() {
			t.Fatal("HasPendingFlow() must be false after ErrAuthCancelled")
		}
	})

	t.Run("IPv6 companion listener serves callback when IPv6 loopback is available", func(t *testing.T) {
		// Skip only if the test host has no IPv6 loopback support.
		probe, err := net.Listen("tcp6", "[::1]:0")
		if err != nil {
			t.Skipf("IPv6 loopback unavailable in this environment: %v", err)
		}
		_ = probe.Close()

		tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"v6-tok","refresh_token":"v6-ref","expires_in":3600}`))
		}))
		defer tokenSrv.Close()

		userInfoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"email":"v6@example.com","sub":"sub-v6"}`))
		}))
		defer userInfoSrv.Close()

		a := NewColabOneAuth(
			newMemoryVault(),
			WithTokenURI(tokenSrv.URL),
			WithUserInfoURL(userInfoSrv.URL),
			WithHTTPClient(tokenSrv.Client()),
			WithSleepFn(noSleep),
		)

		var gotStatus atomic.Int32
		reqDone := make(chan struct{})
		err = a.ColabLogin(context.Background(), func(authURL string) {
			go func() {
				defer close(reqDone)
				u, _ := url.Parse(authURL)
				redirectURI := u.Query().Get("redirect_uri")
				ru, _ := url.Parse(redirectURI)
				v6URL := fmt.Sprintf("http://[::1]:%s/?state=%s&code=%s",
					ru.Port(),
					url.QueryEscape(u.Query().Get("state")),
					url.QueryEscape(testAuthCode),
				)
				resp, rerr := http.Get(v6URL)
				if rerr == nil {
					gotStatus.Store(int32(resp.StatusCode))
					_, _ = io.ReadAll(resp.Body)
					_ = resp.Body.Close()
				}
			}()
		})
		if err != nil {
			t.Fatalf("ColabLogin via IPv6 loopback failed: %v", err)
		}
		<-reqDone
		if gotStatus.Load() != http.StatusOK {
			t.Fatalf("IPv6 callback status = %d, want 200", gotStatus.Load())
		}
		if a.HasPendingFlow() {
			t.Fatal("HasPendingFlow() must be false after IPv6 login completion")
		}
	})
}
