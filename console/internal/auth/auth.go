// Package auth implements Google OAuth 2.0 authentication, PKCE authorization
// flows (loopback and remote copy-paste), token refresh, user profile lookup,
// GCP Drive quota-project provisioning, and rclone configuration generation
// for ColabOne.
//
// The embedded public OAuth client configuration and scope list originate from
// Google Colab CLI (Copyright Google LLC, licensed under the Apache License,
// Version 2.0).
package auth

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// ColabOne credential store and GCP quota-project constants.
const (
	COLABONE_KEYRING_SERVICE       = "ColabOne"
	COLABONE_GCP_ADC_CLIENT_PREFIX = "764086051850-"
	COLABONE_QUOTA_PROJECT_PREFIX  = "colabone-drive-"

	// Google Colab CLI public OAuth 2.0 installed-app configuration (Apache-2.0).
	COLABONE_OAUTH_CLIENT_ID     = "764086051850-6qr4p6gpi6hn506pt8ejuq83di341hur.apps.googleusercontent.com"
	COLABONE_OAUTH_CLIENT_SECRET = "d-FL95Q19q7MQmFpd7hHD0Ty"
	COLABONE_OAUTH_PROJECT_ID    = "colab-cli"
	COLABONE_OAUTH_AUTH_URI      = "https://accounts.google.com/o/oauth2/auth"
	COLABONE_OAUTH_TOKEN_URI     = "https://oauth2.googleapis.com/token"
	COLABONE_OAUTH_CERT_URL      = "https://www.googleapis.com/oauth2/v1/certs"
	REMOTE_REDIRECT_URI          = "https://sdk.cloud.google.com/applicationdefaultauthcode.html"

	defaultUserInfoURL            = "https://www.googleapis.com/oauth2/v3/userinfo"
	defaultCRMBaseURL             = "https://cloudresourcemanager.googleapis.com"
	defaultServiceUsageBaseURL    = "https://serviceusage.googleapis.com"
	defaultUniverseDomain         = "googleapis.com"
	defaultLocale                 = "zh-CN"
	defaultExpirySkew             = 60 * time.Second
	defaultLoopbackTimeout        = 180 * time.Second
	defaultQuotaNegativeCacheTTL  = 600 * time.Second
	maxHTTPResponseBodyBytes      = 4 << 20 // 4 MiB
	oauthCallbackCSPHeaderContent = "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:;"
)

// PUBLIC_SCOPES lists the Google OAuth 2.0 scopes requested by ColabOne
// (matching Google Colab CLI's Apache-2.0 PUBLIC_SCOPES).
var PUBLIC_SCOPES = []string{
	"openid",
	"https://www.googleapis.com/auth/userinfo.profile",
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/cloud-platform",
	"https://www.googleapis.com/auth/colaboratory",
	"https://www.googleapis.com/auth/drive.file",
}

type retryableAuthTransportError string

func (e retryableAuthTransportError) Error() string          { return string(e) }
func (e retryableAuthTransportError) IsTransportError() bool { return true }

// Sentinel user-facing errors. Their exact Chinese text is matched by the
// frontend i18n dictionary (frontend/src/i18n/backendExactMessages.ts).
var (
	ErrInvalidAuthReply          = errors.New("未识别到唯一的 Google 授权码。请粘贴授权页全部文字、回调链接或授权码。")
	ErrNotSignedIn               = errors.New("请先完成 Google 登录")
	ErrRefreshTokenMissing       = errors.New("Google 授权无法自动续期，请在设置中重新登录")
	ErrRefreshRetryable    error = retryableAuthTransportError("Google 暂时无法续期，已保留本机登录，请稍后重试")
	ErrAuthExpired               = errors.New("Google 授权已失效，请在设置中重新登录")
	ErrActiveSessionsExist       = errors.New("切换账号前请先释放当前实例")
	ErrLoginNotStarted           = errors.New("请先打开登录页面")
	ErrAuthCodeInvalid           = errors.New("授权回显已失效或不属于本次登录，请重新打开登录页")
	ErrProfileFetchFailed        = errors.New("读取 Google 账号信息失败，请检查网络连接后重试")
	ErrAuthCancelled             = errors.New("Google 授权已取消，请重新登录")
	ErrLoginTimeout              = errors.New("等待登录超时。可点击「改用手动授权登录」，粘贴浏览器返回内容自动完成。")
	ErrUntrustedTokenURI         = errors.New("Google 授权令牌地址不受信任，请在设置中重新登录")
)

// OAuthCallbackText holds localized success and error copy shown on the
// loopback OAuth completion page.
type OAuthCallbackText struct {
	Success string
	Error   string
}

// OAuthCallbackTexts provides localized callback copy for all six supported
// ColabOne locales.
var OAuthCallbackTexts = map[string]OAuthCallbackText{
	"zh-CN": {
		Success: "ColabOne 登录成功。你可以关闭此页面，返回客户端。",
		Error:   "登录未完成，请返回 ColabOne 重试。",
	},
	"en-US": {
		Success: "Signed in to ColabOne. You can close this tab and return to the app.",
		Error:   "Sign-in was not completed. Please return to ColabOne and try again.",
	},
	"zh-TW": {
		Success: "已成功登入 ColabOne。你可以關閉此分頁並返回用戶端。",
		Error:   "登入尚未完成，請返回 ColabOne 重試。",
	},
	"zh-HK": {
		Success: "已成功登入 ColabOne。你可以關閉此分頁並返回應用程式。",
		Error:   "登入尚未完成，請返回 ColabOne 再試。",
	},
	"ja-JP": {
		Success: "ColabOne へのログインが完了しました。このタブを閉じてアプリに戻ってください。",
		Error:   "ログインが完了しませんでした。ColabOne に戻って再試行してください。",
	},
	"ko-KR": {
		Success: "ColabOne 로그인을 완료했습니다. 이 탭을 닫고 앱으로 돌아가 주세요.",
		Error:   "로그인이 완료되지 않았습니다. ColabOne으로 돌아가 다시 시도해 주세요.",
	},
}

var (
	authCodeRE     = regexp.MustCompile(`4/[A-Za-z0-9_./+\-]{20,}`)
	gcpProjectIDRE = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
	gcpURLRE       = regexp.MustCompile(`https?://\S+`)
	gcpSecretRE    = regexp.MustCompile(`(?i)\b(?:bearer\s+\S+|ya29\.[A-Za-z0-9._\-]+|1//[A-Za-z0-9._\-]+)`)
)

// VaultStore abstracts encrypted credential storage (OS keyring or in-memory
// test double) so package auth stays decoupled from platform keyrings.
type VaultStore interface {
	GetJSON(name string, dest any) (bool, error)
	Set(name string, value any) error
	Delete(name string) error
}

// OAuthPageRenderer renders the HTML page served by ColabOne's local loopback
// OAuth callback endpoint.
type OAuthPageRenderer func(locale string, ok bool, message, email string) string

// ColabOneCredentials mirrors google.oauth2.credentials.Credentials.to_json()
// so existing vault entries saved by Python load seamlessly and remain
// cross-compatible.
type ColabOneCredentials struct {
	Token          string   `json:"token,omitempty"`
	RefreshToken   string   `json:"refresh_token,omitempty"`
	IDToken        string   `json:"id_token,omitempty"`
	TokenURI       string   `json:"token_uri,omitempty"`
	ClientID       string   `json:"client_id,omitempty"`
	ClientSecret   string   `json:"client_secret,omitempty"`
	Scopes         []string `json:"scopes,omitempty"`
	QuotaProjectID string   `json:"quota_project_id,omitempty"`
	RaptToken      string   `json:"rapt_token,omitempty"`
	UniverseDomain string   `json:"universe_domain,omitempty"`
	Account        string   `json:"account,omitempty"`
	Expiry         string   `json:"expiry,omitempty"`
}

func redactCredentialSecret(val string) string {
	if val == "" {
		return ""
	}
	return "[REDACTED]"
}

// String redacts secret credential fields so %s, %v, and %+v never leak tokens
// or client secrets into logs or error messages.
func (c ColabOneCredentials) String() string {
	return fmt.Sprintf(
		"ColabOneCredentials{Token:%q, RefreshToken:%q, IDToken:%q, TokenURI:%q, ClientID:%q, ClientSecret:%q, Scopes:%q, QuotaProjectID:%q, RaptToken:%q, UniverseDomain:%q, Account:%q, Expiry:%q}",
		redactCredentialSecret(c.Token),
		redactCredentialSecret(c.RefreshToken),
		redactCredentialSecret(c.IDToken),
		c.TokenURI,
		c.ClientID,
		redactCredentialSecret(c.ClientSecret),
		c.Scopes,
		c.QuotaProjectID,
		redactCredentialSecret(c.RaptToken),
		c.UniverseDomain,
		c.Account,
		c.Expiry,
	)
}

// GoString redacts secret credential fields when formatted with %#v.
func (c ColabOneCredentials) GoString() string {
	return c.String()
}

// UnmarshalJSON supports both JSON array scopes and space-delimited string
// scopes (matching google.oauth2.credentials.Credentials.from_authorized_user_info).
func (c *ColabOneCredentials) UnmarshalJSON(data []byte) error {
	type credsAlias struct {
		Token          string          `json:"token,omitempty"`
		RefreshToken   string          `json:"refresh_token,omitempty"`
		IDToken        string          `json:"id_token,omitempty"`
		TokenURI       string          `json:"token_uri,omitempty"`
		ClientID       string          `json:"client_id,omitempty"`
		ClientSecret   string          `json:"client_secret,omitempty"`
		Scopes         json.RawMessage `json:"scopes,omitempty"`
		QuotaProjectID string          `json:"quota_project_id,omitempty"`
		RaptToken      string          `json:"rapt_token,omitempty"`
		UniverseDomain string          `json:"universe_domain,omitempty"`
		Account        string          `json:"account,omitempty"`
		Expiry         string          `json:"expiry,omitempty"`
	}
	var alias credsAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	c.Token = alias.Token
	c.RefreshToken = alias.RefreshToken
	c.IDToken = alias.IDToken
	c.TokenURI = alias.TokenURI
	c.ClientID = alias.ClientID
	c.ClientSecret = alias.ClientSecret
	c.QuotaProjectID = alias.QuotaProjectID
	c.RaptToken = alias.RaptToken
	c.UniverseDomain = alias.UniverseDomain
	c.Account = alias.Account
	c.Expiry = alias.Expiry
	c.Scopes = nil

	trimmed := bytes.TrimSpace(alias.Scopes)
	if len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		var list []string
		if err := json.Unmarshal(trimmed, &list); err == nil {
			c.Scopes = list
		} else {
			var single string
			if err := json.Unmarshal(trimmed, &single); err != nil {
				return err
			}
			c.Scopes = strings.Fields(single)
		}
	}
	return nil
}

// ExpiryTime parses the RFC3339 or naive UTC ISO-8601 expiry timestamp.
func (c *ColabOneCredentials) ExpiryTime() (time.Time, bool) {
	if c == nil {
		return time.Time{}, false
	}
	raw := strings.TrimSpace(c.Expiry)
	if raw == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// ValidAt reports whether the access token is non-empty and has more than
// 60 seconds remaining before expiry relative to now.
func (c *ColabOneCredentials) ValidAt(now time.Time) bool {
	if c == nil || c.Token == "" {
		return false
	}
	exp, ok := c.ExpiryTime()
	if !ok {
		return false
	}
	return now.UTC().Add(defaultExpirySkew).Before(exp)
}

// Valid reports whether the credentials are valid at the current wall time.
func (c *ColabOneCredentials) Valid() bool {
	return c.ValidAt(time.Now())
}

// Clone returns a deep copy of the credentials.
func (c *ColabOneCredentials) Clone() *ColabOneCredentials {
	if c == nil {
		return nil
	}
	cp := *c
	if c.Scopes != nil {
		cp.Scopes = append([]string(nil), c.Scopes...)
	}
	return &cp
}

// ParseAuthorizationReply extracts a unique Google OAuth authorization code
// from pasted page text, a redirect callback URL, or a raw authorization code.
func ParseAuthorizationReply(text string) (string, error) {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "https://") || strings.HasPrefix(trimmed, "http://") {
		if parsed, err := url.Parse(trimmed); err == nil {
			if codes := parsed.Query()["code"]; len(codes) > 0 && codes[0] != "" {
				trimmed = codes[0]
			}
		}
	}
	decoded := percentUnescape(trimmed)
	rawMatches := authCodeRE.FindAllString(decoded, -1)
	seen := make(map[string]struct{}, len(rawMatches))
	unique := make([]string, 0, len(rawMatches))
	for _, m := range rawMatches {
		if _, exists := seen[m]; !exists {
			seen[m] = struct{}{}
			unique = append(unique, m)
		}
	}
	if len(unique) != 1 {
		return "", ErrInvalidAuthReply
	}
	return unique[0], nil
}

// percentUnescape decodes valid %XX sequences like Python's urllib.parse.unquote
// without converting '+' into a space or failing on stray '%' characters.
func percentUnescape(s string) string {
	if !strings.ContainsRune(s, '%') {
		return s
	}
	if unescaped, err := url.PathUnescape(s); err == nil {
		return unescaped
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '%' && i+2 < len(s) {
			hi, ok1 := unhex(s[i+1])
			lo, ok2 := unhex(s[i+2])
			if ok1 && ok2 {
				b.WriteByte(hi<<4 | lo)
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func unhex(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

type oauthFlow struct {
	clientID      string
	clientSecret  string
	authURI       string
	tokenURI      string
	redirectURI   string
	scopes        []string
	codeVerifier  string
	codeChallenge string
	state         string
	doneCh        <-chan struct{}
	resetGen      uint64
}

func isClosed(ch <-chan struct{}) bool {
	if ch == nil {
		return false
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func (f *oauthFlow) authorizationURL(remote bool) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", f.clientID)
	q.Set("redirect_uri", f.redirectURI)
	q.Set("scope", strings.Join(f.scopes, " "))
	q.Set("state", f.state)
	q.Set("code_challenge", f.codeChallenge)
	q.Set("code_challenge_method", "S256")
	q.Set("access_type", "offline")
	q.Set("prompt", "select_account consent")
	if remote {
		q.Set("token_usage", "remote")
	}
	sep := "?"
	if strings.Contains(f.authURI, "?") {
		sep = "&"
	}
	return f.authURI + sep + q.Encode()
}

type colabOneEvent struct {
	mu  sync.Mutex
	set bool
	ch  chan struct{}
}

func newColabOneEvent() *colabOneEvent {
	return &colabOneEvent{ch: make(chan struct{})}
}

func (e *colabOneEvent) Clear() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.set {
		e.ch = make(chan struct{})
		e.set = false
	}
}

func (e *colabOneEvent) Set() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.set {
		e.set = true
		close(e.ch)
	}
}

func (e *colabOneEvent) IsSet() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.set
}

func (e *colabOneEvent) WaitCh() <-chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ch
}

type quotaProbeCacheEntry struct {
	timestamp time.Time
	projectID string
	reason    string
}

// Option configures a ColabOneAuth instance.
type Option func(*ColabOneAuth)

// WithHTTPClient injects a custom HTTP client (e.g., for offline tests).
func WithHTTPClient(client *http.Client) Option {
	return func(a *ColabOneAuth) {
		if client != nil {
			a.httpClient = client
		}
	}
}

// WithOAuthPageRenderer injects the HTML renderer for the loopback OAuth
// completion page.
func WithOAuthPageRenderer(fn OAuthPageRenderer) Option {
	return func(a *ColabOneAuth) {
		a.renderOAuthPage = fn
	}
}

// WithSleepFn injects a custom sleep function (e.g., no-op in offline tests).
func WithSleepFn(fn func(time.Duration)) Option {
	return func(a *ColabOneAuth) {
		a.sleepFn = fn
	}
}

// WithNowFn injects a custom clock function for deterministic tests.
func WithNowFn(fn func() time.Time) Option {
	return func(a *ColabOneAuth) {
		if fn != nil {
			a.nowFn = fn
		}
	}
}

// WithAuthURI overrides the Google OAuth 2.0 authorization endpoint URI.
func WithAuthURI(uri string) Option {
	return func(a *ColabOneAuth) {
		if strings.TrimSpace(uri) != "" {
			a.authURI = strings.TrimSpace(uri)
		}
	}
}

// WithTokenURI overrides the Google OAuth 2.0 token endpoint URI (also
// overriding stored credential token_uri during refresh in offline tests).
func WithTokenURI(uri string) Option {
	return func(a *ColabOneAuth) {
		if strings.TrimSpace(uri) != "" {
			a.tokenURI = strings.TrimSpace(uri)
			a.tokenURIOverride = true
		}
	}
}

// WithUserInfoURL overrides the Google OAuth 2.0 userinfo endpoint URL.
func WithUserInfoURL(uri string) Option {
	return func(a *ColabOneAuth) {
		if strings.TrimSpace(uri) != "" {
			a.userInfoURL = strings.TrimSpace(uri)
		}
	}
}

// WithCloudResourceManagerBaseURL overrides the GCP Cloud Resource Manager API
// base URL (default https://cloudresourcemanager.googleapis.com).
func WithCloudResourceManagerBaseURL(baseURL string) Option {
	return func(a *ColabOneAuth) {
		if trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/"); trimmed != "" {
			a.crmBaseURL = trimmed
		}
	}
}

// WithServiceUsageBaseURL overrides the GCP Service Usage API base URL
// (default https://serviceusage.googleapis.com).
func WithServiceUsageBaseURL(baseURL string) Option {
	return func(a *ColabOneAuth) {
		if trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/"); trimmed != "" {
			a.serviceUsageBaseURL = trimmed
		}
	}
}

// WithLoopbackTimeout overrides the loopback login timeout (default 180s).
func WithLoopbackTimeout(d time.Duration) Option {
	return func(a *ColabOneAuth) {
		if d > 0 {
			a.loopbackTimeout = d
		}
	}
}

// WithQuotaNegativeCacheTTL overrides the negative cache TTL for GCP quota
// project probing (default 600s).
func WithQuotaNegativeCacheTTL(d time.Duration) Option {
	return func(a *ColabOneAuth) {
		if d > 0 {
			a.quotaNegativeCacheTTL = d
		}
	}
}

// ColabOneAuth manages Google OAuth credentials, token refresh, profile
// resolution, loopback/manual PKCE login flows, GCP Drive quota-project
// provisioning, and rclone config generation.
type ColabOneAuth struct {
	vault VaultStore

	// Lock ordering: loopbackSem / quotaProbeSem -> mu -> quotaMu -> leaf locks (localeMu, loginEvt.mu, vault).
	loopbackSem       chan struct{}
	quotaProbeSem     chan struct{}
	quotaProbeSemOnce sync.Once
	mu                sync.Mutex
	quotaMu           sync.Mutex
	localeMu          sync.RWMutex

	flow           *oauthFlow
	authResetGen   uint64
	loginEvt       *colabOneEvent
	loopbackActive atomic.Bool
	locale         string

	quotaProbeCache *quotaProbeCacheEntry
	quotaEpoch      uint64

	httpClient            *http.Client
	renderOAuthPage       OAuthPageRenderer
	sleepFn               func(time.Duration)
	nowFn                 func() time.Time
	listenFn              func(network, address string) (net.Listener, error)
	authURI               string
	tokenURI              string
	tokenURIOverride      bool
	userInfoURL           string
	crmBaseURL            string
	serviceUsageBaseURL   string
	loopbackTimeout       time.Duration
	quotaNegativeCacheTTL time.Duration
}

// NewColabOneAuth constructs a ColabOneAuth backed by vault.
func NewColabOneAuth(vault VaultStore, opts ...Option) *ColabOneAuth {
	a := &ColabOneAuth{
		vault:         vault,
		loopbackSem:   make(chan struct{}, 1),
		quotaProbeSem: make(chan struct{}, 1),
		loginEvt:      newColabOneEvent(),
		locale:      defaultLocale,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		nowFn:                 time.Now,
		authURI:               COLABONE_OAUTH_AUTH_URI,
		tokenURI:              COLABONE_OAUTH_TOKEN_URI,
		userInfoURL:           defaultUserInfoURL,
		crmBaseURL:            defaultCRMBaseURL,
		serviceUsageBaseURL:   defaultServiceUsageBaseURL,
		loopbackTimeout:       defaultLoopbackTimeout,
		quotaNegativeCacheTTL: defaultQuotaNegativeCacheTTL,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(a)
		}
	}
	return a
}

// SetLocale updates the active UI locale used on the loopback OAuth callback page.
func (a *ColabOneAuth) SetLocale(locale string) {
	a.localeMu.Lock()
	defer a.localeMu.Unlock()
	trimmed := strings.TrimSpace(locale)
	if trimmed == "" {
		a.locale = defaultLocale
		return
	}
	a.locale = trimmed
}

// Locale returns the currently active OAuth callback locale.
func (a *ColabOneAuth) Locale() string {
	a.localeMu.RLock()
	defer a.localeMu.RUnlock()
	if a.locale == "" {
		return defaultLocale
	}
	return a.locale
}

// LoopbackActive reports whether an automated loopback OAuth login server is
// currently listening for a callback.
func (a *ColabOneAuth) LoopbackActive() bool {
	return a.loopbackActive.Load()
}

// HasPendingFlow reports whether an OAuth PKCE flow is currently awaiting
// completion via ColabFinish.
func (a *ColabOneAuth) HasPendingFlow() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.flow != nil
}

func (a *ColabOneAuth) now() time.Time {
	return a.nowFn()
}

func (a *ColabOneAuth) sleep(ctx context.Context, d time.Duration) error {
	if a.sleepFn != nil {
		a.sleepFn(d)
		return ctx.Err()
	}
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (a *ColabOneAuth) vaultGetJSON(name string, dest any) (bool, error) {
	if a.vault == nil {
		return false, nil
	}
	return a.vault.GetJSON(name, dest)
}

func (a *ColabOneAuth) vaultSet(name string, value any) error {
	if a.vault == nil {
		return nil
	}
	return a.vault.Set(name, value)
}

func (a *ColabOneAuth) vaultDelete(name string) error {
	if a.vault == nil {
		return nil
	}
	return a.vault.Delete(name)
}

func (a *ColabOneAuth) getVaultString(name string) string {
	var s string
	ok, err := a.vaultGetJSON(name, &s)
	if err != nil || !ok {
		return ""
	}
	return s
}

func (a *ColabOneAuth) getProfileMap() map[string]string {
	var prof map[string]string
	ok, err := a.vaultGetJSON("google_profile", &prof)
	if err != nil || !ok || prof == nil {
		return nil
	}
	return prof
}

func (a *ColabOneAuth) vaultHasTruthy(name string) bool {
	var raw any
	ok, err := a.vaultGetJSON(name, &raw)
	if err != nil || !ok || raw == nil {
		return false
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		return v != ""
	case float64:
		return v != 0
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	default:
		return true
	}
}

func (a *ColabOneAuth) hasActiveSessions() bool {
	return a.vaultHasTruthy("runtime_session_ids") ||
		a.vaultHasTruthy("runtime_session") ||
		a.vaultHasTruthy("runtime_sessions")
}

// Profile returns the cached Google user profile ("email" and "sub"),
// fetching and caching it via the saved "colab" credentials if absent.
func (a *ColabOneAuth) Profile(ctx context.Context) (map[string]string, error) {
	a.quotaMu.Lock()
	var prof map[string]string
	ok, err := a.vaultGetJSON("google_profile", &prof)
	if err != nil {
		a.quotaMu.Unlock()
		return nil, err
	}
	if ok && prof != nil {
		a.quotaMu.Unlock()
		cp := make(map[string]string, len(prof))
		for k, v := range prof {
			cp[k] = v
		}
		return cp, nil
	}
	startEpoch := a.quotaEpoch
	a.quotaMu.Unlock()

	creds, err := a.Credentials(ctx, "colab")
	if err != nil {
		return nil, err
	}
	prof, err = a.FetchProfile(ctx, creds.Token)
	if err != nil {
		return nil, err
	}

	a.quotaMu.Lock()
	var writeErr error
	if a.quotaEpoch == startEpoch {
		var savedCreds ColabOneCredentials
		hasColab, credErr := a.vaultGetJSON("colab", &savedCreds)
		if credErr != nil {
			writeErr = credErr
		} else if hasColab && (savedCreds.Token != "" || savedCreds.RefreshToken != "" || savedCreds.ClientID != "") {
			writeErr = a.vaultSet("google_profile", prof)
		}
	}
	a.quotaMu.Unlock()
	if writeErr != nil {
		return nil, writeErr
	}
	return prof, nil
}

// FetchProfile queries Google's OAuth 2.0 userinfo endpoint with a 2-attempt
// retry on 5xx responses and transient network errors.
func (a *ColabOneAuth) FetchProfile(ctx context.Context, accessToken string) (map[string]string, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, a.userInfoURL, nil)
		if err != nil {
			cancel()
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)

		resp, err := a.httpClient.Do(req)
		if err != nil {
			cancel()
			lastErr = err
			if attempt == 0 {
				if serr := a.sleep(ctx, 500*time.Millisecond); serr != nil {
					return nil, serr
				}
				continue
			}
			return nil, lastErr
		}

		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxHTTPResponseBodyBytes))
		_ = resp.Body.Close()
		cancel()

		if readErr != nil {
			lastErr = readErr
			if attempt == 0 {
				if serr := a.sleep(ctx, 500*time.Millisecond); serr != nil {
					return nil, serr
				}
				continue
			}
			return nil, lastErr
		}

		if isTransient5xx(resp.StatusCode) && attempt == 0 {
			lastErr = fmt.Errorf("userinfo HTTP %d", resp.StatusCode)
			if serr := a.sleep(ctx, 500*time.Millisecond); serr != nil {
				return nil, serr
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("userinfo HTTP %d", resp.StatusCode)
		}

		var payload struct {
			Email string `json:"email"`
			Sub   string `json:"sub"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, err
		}
		if payload.Sub == "" {
			return nil, errors.New("userinfo response missing sub")
		}
		return map[string]string{
			"email": payload.Email,
			"sub":   payload.Sub,
		}, nil
	}
	return nil, lastErr
}

// Credentials loads and validates the saved OAuth credentials under name,
// serializing concurrent refreshes so a stale request cannot overwrite a newer
// login and preserving saved tokens on transient network/5xx errors.
func (a *ColabOneAuth) Credentials(ctx context.Context, name string) (*ColabOneCredentials, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	var creds ColabOneCredentials
	ok, err := a.vaultGetJSON(name, &creds)
	if err != nil {
		return nil, err
	}
	if !ok || (creds.Token == "" && creds.RefreshToken == "" && creds.ClientID == "") {
		return nil, ErrNotSignedIn
	}

	if creds.ValidAt(a.now()) {
		return creds.Clone(), nil
	}

	if creds.RefreshToken == "" {
		return nil, ErrRefreshTokenMissing
	}

	if err := a.refreshCredentialsLocked(ctx, &creds); err != nil {
		return nil, err
	}
	if err := a.vaultSet(name, &creds); err != nil {
		return nil, err
	}
	return creds.Clone(), nil
}

func validateGoogleTokenURI(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if raw != strings.TrimSpace(raw) || strings.ContainsAny(raw, "?#@\r\n") {
		return "", ErrUntrustedTokenURI
	}
	parsed, err := url.Parse(raw)
	if err != nil ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.ForceQuery ||
		parsed.Fragment != "" ||
		parsed.Opaque != "" {
		return "", ErrUntrustedTokenURI
	}
	if !strings.EqualFold(parsed.Scheme, "https") || parsed.Port() != "" {
		return "", ErrUntrustedTokenURI
	}
	canonical := "https://" + strings.ToLower(parsed.Hostname()) + parsed.EscapedPath()
	switch canonical {
	case COLABONE_OAUTH_TOKEN_URI, "https://accounts.google.com/o/oauth2/token":
		if parsed.Path != "/token" && parsed.Path != "/o/oauth2/token" {
			return "", ErrUntrustedTokenURI
		}
		return canonical, nil
	default:
		return "", ErrUntrustedTokenURI
	}
}

func (a *ColabOneAuth) refreshCredentialsLocked(ctx context.Context, creds *ColabOneCredentials) error {
	tokenURI := creds.TokenURI
	if a.tokenURIOverride {
		tokenURI = a.tokenURI
	} else {
		validated, err := validateGoogleTokenURI(tokenURI)
		if err != nil {
			return err
		}
		if validated == "" {
			tokenURI = a.tokenURI
		} else {
			tokenURI = validated
		}
	}
	clientID := creds.ClientID
	if clientID == "" {
		clientID = COLABONE_OAUTH_CLIENT_ID
	}
	clientSecret := creds.ClientSecret
	if clientSecret == "" {
		clientSecret = COLABONE_OAUTH_CLIENT_SECRET
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", creds.RefreshToken)
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)

	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return ErrRefreshRetryable
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return ctx.Err()
		}
		return ErrRefreshRetryable
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxHTTPResponseBodyBytes))
	_ = resp.Body.Close()
	if readErr != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return ctx.Err()
		}
		return ErrRefreshRetryable
	}

	if resp.StatusCode != http.StatusOK {
		if isRetryableRefreshError(resp.StatusCode, body) {
			return ErrRefreshRetryable
		}
		return ErrAuthExpired
	}

	var tokResp oauthTokenResponse
	if err := json.Unmarshal(body, &tokResp); err != nil || tokResp.AccessToken == "" {
		return ErrAuthExpired
	}

	creds.Token = tokResp.AccessToken
	if tokResp.RefreshToken != "" {
		creds.RefreshToken = tokResp.RefreshToken
	}
	if tokResp.IDToken != "" {
		creds.IDToken = tokResp.IDToken
	}
	if tokResp.ExpiresIn > 0 {
		creds.Expiry = a.now().UTC().Add(clampTokenExpiryDuration(int64(tokResp.ExpiresIn))).Format(time.RFC3339)
	}
	if creds.TokenURI == "" {
		creds.TokenURI = COLABONE_OAUTH_TOKEN_URI
	}
	if creds.UniverseDomain == "" {
		creds.UniverseDomain = defaultUniverseDomain
	}
	return nil
}

// HasDriveScope reports whether the saved "colab" OAuth credentials carry a
// refresh token and at least one Google Drive scope.
func (a *ColabOneAuth) HasDriveScope() bool {
	var creds ColabOneCredentials
	ok, err := a.vaultGetJSON("colab", &creds)
	if err != nil || !ok || creds.RefreshToken == "" {
		return false
	}
	for _, s := range creds.Scopes {
		if strings.Contains(strings.ToLower(s), "drive") {
			return true
		}
	}
	return false
}

// RequiresDriveQuotaProject reports whether the saved OAuth token uses
// Google's public ADC client (764086051850-*), which requires an explicit
// X-Goog-User-Project header for drive.googleapis.com calls.
func (a *ColabOneAuth) RequiresDriveQuotaProject() bool {
	var creds ColabOneCredentials
	ok, err := a.vaultGetJSON("colab", &creds)
	if err != nil || !ok {
		return false
	}
	return strings.HasPrefix(creds.ClientID, COLABONE_GCP_ADC_CLIENT_PREFIX)
}

// InvalidateDriveQuotaProject clears the cached cloud_project and resets the
// in-memory quota probe cache.
func (a *ColabOneAuth) InvalidateDriveQuotaProject() {
	a.quotaMu.Lock()
	defer a.quotaMu.Unlock()
	a.quotaEpoch++
	_ = a.vaultDelete("cloud_project")
	a.quotaProbeCache = nil
}

// ResetAccountState clears in-memory OAuth login flows and Drive quota probe caches
// after vault credentials have been replaced externally (such as by migration import).
func (a *ColabOneAuth) ResetAccountState() {
	a.mu.Lock()
	a.authResetGen++
	a.flow = nil
	a.loginEvt.Clear()
	a.quotaMu.Lock()
	a.quotaEpoch++
	a.quotaProbeCache = nil
	a.quotaMu.Unlock()
	a.mu.Unlock()
}

func (a *ColabOneAuth) isFlowReset(f *oauthFlow) bool {
	if f == nil {
		return false
	}
	a.mu.Lock()
	stale := f.resetGen != a.authResetGen
	a.mu.Unlock()
	return stale
}

func (a *ColabOneAuth) acquireQuotaProbeSem(ctx context.Context) (func(), error) {
	a.quotaProbeSemOnce.Do(func() {
		if a.quotaProbeSem == nil {
			a.quotaProbeSem = make(chan struct{}, 1)
		}
	})
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case a.quotaProbeSem <- struct{}{}:
		return func() { <-a.quotaProbeSem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// DriveQuotaStatus returns the offline readiness snapshot for headless Drive
// quota-project mode.
func (a *ColabOneAuth) DriveQuotaStatus() map[string]any {
	needsQuota := a.RequiresDriveQuotaProject()

	a.quotaMu.Lock()
	projectID := a.getVaultString("cloud_project")
	hasProject := isValidProjectID(projectID)
	reason := ""
	if a.quotaProbeCache != nil {
		reason = a.quotaProbeCache.reason
	}
	a.quotaMu.Unlock()

	if !needsQuota {
		return map[string]any{
			"needs_quota": false,
			"ready":       a.HasDriveScope(),
			"project_id":  "",
			"reason":      "not_required",
		}
	}
	if hasProject {
		return map[string]any{
			"needs_quota": true,
			"ready":       true,
			"project_id":  projectID,
			"reason":      "ready",
		}
	}
	return map[string]any{
		"needs_quota": true,
		"ready":       false,
		"project_id":  "",
		"reason":      reason,
	}
}

// EnsureDriveQuotaProject discovers or provisions a free Drive-enabled GCP
// quota project for headless rclone access. It returns (projectID, reason) and
// never returns an error on missing GCP ToS or org restrictions so callers can
// cleanly fall back to native Colab DriveFS.
func (a *ColabOneAuth) EnsureDriveQuotaProject(
	ctx context.Context,
	onLog func(msg, source, level string),
	forceRefresh bool,
) (string, string) {
	if !a.RequiresDriveQuotaProject() {
		return "", "not_required"
	}

	if !forceRefresh {
		a.quotaMu.Lock()
		if cached := a.getVaultString("cloud_project"); isValidProjectID(cached) {
			a.quotaMu.Unlock()
			return cached, "cached"
		}
		if a.quotaProbeCache != nil {
			entry := *a.quotaProbeCache
			if entry.projectID != "" && isValidProjectID(entry.projectID) {
				a.quotaMu.Unlock()
				return entry.projectID, entry.reason
			}
			if a.now().Sub(entry.timestamp) < a.quotaNegativeCacheTTL {
				a.quotaMu.Unlock()
				return "", entry.reason
			}
		}
		a.quotaMu.Unlock()
	}

	releaseSem, semErr := a.acquireQuotaProbeSem(ctx)
	if semErr != nil {
		return "", "network_error"
	}
	defer releaseSem()

	if !a.RequiresDriveQuotaProject() {
		return "", "not_required"
	}

	a.quotaMu.Lock()
	if !forceRefresh {
		if cached := a.getVaultString("cloud_project"); isValidProjectID(cached) {
			a.quotaMu.Unlock()
			return cached, "cached"
		}
		if a.quotaProbeCache != nil {
			entry := *a.quotaProbeCache
			if entry.projectID != "" && isValidProjectID(entry.projectID) {
				a.quotaMu.Unlock()
				return entry.projectID, entry.reason
			}
			if a.now().Sub(entry.timestamp) < a.quotaNegativeCacheTTL {
				a.quotaMu.Unlock()
				return "", entry.reason
			}
		}
	}
	startEpoch := a.quotaEpoch
	initialSub := ""
	if prof := a.getProfileMap(); prof != nil {
		initialSub = prof["sub"]
	}
	var saved ColabOneCredentials
	savedOK, _ := a.vaultGetJSON("colab", &saved)
	a.quotaMu.Unlock()

	if !savedOK || !strings.HasPrefix(saved.ClientID, COLABONE_GCP_ADC_CLIENT_PREFIX) {
		return "", "not_required"
	}

	hasCloudScope := false
	for _, s := range saved.Scopes {
		if strings.Contains(strings.ToLower(s), "cloud-platform") {
			hasCloudScope = true
			break
		}
	}
	if !hasCloudScope {
		if !a.setQuotaNegativeCache(startEpoch, initialSub, "no_cloud_scope") {
			return "", "account_changed"
		}
		return "", "no_cloud_scope"
	}

	quotaStale := func() bool {
		a.quotaMu.Lock()
		defer a.quotaMu.Unlock()
		return a.isQuotaStaleLocked(startEpoch, initialSub)
	}

	persistProject := func(projectID, reasonTag string) (string, string) {
		a.quotaMu.Lock()
		defer a.quotaMu.Unlock()
		if a.isQuotaStaleLocked(startEpoch, initialSub) {
			return "", "account_changed"
		}
		_ = a.vaultSet("cloud_project", projectID)
		a.quotaProbeCache = &quotaProbeCacheEntry{
			timestamp: a.now(),
			projectID: projectID,
			reason:    reasonTag,
		}
		return projectID, reasonTag
	}

	creds, err := a.Credentials(ctx, "colab")
	if err != nil {
		if quotaStale() {
			return "", "account_changed"
		}
		return "", "network_error"
	}
	if quotaStale() {
		return "", "account_changed"
	}
	token := creds.Token

	listURL := a.crmBaseURL + "/v1/projects?filter=" + url.QueryEscape("lifecycleState:ACTIVE") + "&pageSize=50"
	status, body, err := a.doBearerJSON(ctx, http.MethodGet, listURL, token, "", nil, 12*time.Second)
	if err != nil {
		return "", "network_error"
	}
	if status != http.StatusOK {
		if isTransientQuotaHTTPStatus(status) {
			return "", "network_error"
		}
		reason := classifyGCPErrorReason(string(body))
		if !a.setQuotaNegativeCache(startEpoch, initialSub, reason) {
			return "", "account_changed"
		}
		if reason != "gcp_tos_required" {
			a.logQuotaFailure(onLog, "list_projects", "", status, body, 0, nil)
		}
		return "", reason
	}

	var listResp struct {
		Projects []struct {
			ProjectID      string `json:"projectId"`
			LifecycleState string `json:"lifecycleState"`
		} `json:"projects"`
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &listResp); err != nil {
			return "", "network_error"
		}
	}

	activeProjects := make([]string, 0, len(listResp.Projects))
	for _, p := range listResp.Projects {
		if isValidProjectID(p.ProjectID) && strings.ToUpper(p.LifecycleState) == "ACTIVE" {
			activeProjects = append(activeProjects, p.ProjectID)
		}
	}

	// 1) Reuse an existing ColabOne-owned free quota project first.
	failedColabOne := make(map[string]quotaEnableResult)
	for _, pid := range activeProjects {
		if !strings.HasPrefix(pid, COLABONE_QUOTA_PROJECT_PREFIX) {
			continue
		}
		enabled, err := a.driveAPIEnabled(ctx, token, pid)
		if err != nil {
			return "", "network_error"
		}
		if !enabled {
			if quotaStale() {
				return "", "account_changed"
			}
			res, err := a.enableDriveAPI(ctx, token, pid, quotaStale)
			if errors.Is(err, errQuotaStale) {
				return "", "account_changed"
			}
			if err != nil {
				return "", "network_error"
			}
			enabled = res.enabled
			if !enabled {
				failedColabOne[pid] = res
			}
		}
		if enabled {
			return persistProject(pid, "reused_colabone_project")
		}
	}

	// 2) Read-only check: reuse an existing active project that already has
	//    drive.googleapis.com enabled (never mutate unrelated projects).
	limit := len(activeProjects)
	if limit > 10 {
		limit = 10
	}
	for _, pid := range activeProjects[:limit] {
		if strings.HasPrefix(pid, COLABONE_QUOTA_PROJECT_PREFIX) {
			continue
		}
		enabled, err := a.driveAPIEnabled(ctx, token, pid)
		if err != nil {
			return "", "network_error"
		}
		if enabled {
			return persistProject(pid, "reused_existing_project")
		}
	}

	// 3) Attempt once to create a single free dedicated quota project.
	if quotaStale() {
		return "", "account_changed"
	}
	profile, err := a.Profile(ctx)
	if err != nil {
		if quotaStale() {
			return "", "account_changed"
		}
		return "", "network_error"
	}
	sub := profile["sub"]
	if sub == "" {
		return "", "gcp_project_unavailable"
	}
	if initialSub != "" && sub != initialSub {
		return "", "account_changed"
	}
	if quotaStale() {
		return "", "account_changed"
	}
	if initialSub == "" {
		initialSub = sub
	}

	candidates := []string{
		COLABONE_QUOTA_PROJECT_PREFIX + shortSHA256Hex("colabone-drive:"+sub, 10),
		COLABONE_QUOTA_PROJECT_PREFIX + shortSHA256Hex("colabone-drive:v2:"+sub, 10),
	}

	// If one of THIS account's own deterministic colabone-drive-* projects already
	// exists in activeProjects but Drive API could not be enabled on it in Step 1,
	// do not create a second duplicate project; surface the failure cleanly.
	for _, ownCand := range candidates {
		failRes, exists := failedColabOne[ownCand]
		if !exists {
			continue
		}
		if failRes.transient {
			if quotaStale() {
				return "", "account_changed"
			}
			a.logQuotaFailure(onLog, "enable_drive_api", ownCand, failRes.status, failRes.body, failRes.fallbackStatus, failRes.fallbackBody)
			return "", "network_error"
		}
		reason := classifyEnableFailureReason(failRes)
		if !a.setQuotaNegativeCache(startEpoch, initialSub, reason) {
			return "", "account_changed"
		}
		if reason != "gcp_tos_required" {
			a.logQuotaFailure(onLog, "enable_drive_api", ownCand, failRes.status, failRes.body, failRes.fallbackStatus, failRes.fallbackBody)
		}
		return "", reason
	}

	emitAuthLog(onLog, "正在检测或初始化免费 Google Drive API 配额项目（仅首次检测）", "云盘", "info")

	pid := candidates[0]
	var createStatus int
	var createBody []byte
	for idx, cand := range candidates {
		pid = cand
		createStatus, createBody, err = a.doBearerJSON(
			ctx,
			http.MethodPost,
			a.crmBaseURL+"/v1/projects",
			token,
			"",
			map[string]string{
				"projectId": pid,
				"name":      "ColabOne Drive",
			},
			15*time.Second,
		)
		if err != nil {
			return "", "network_error"
		}
		if createStatus == http.StatusConflict && idx+1 < len(candidates) {
			continue
		}
		break
	}

	if createStatus != http.StatusOK {
		if isTransientQuotaHTTPStatus(createStatus) {
			return "", "network_error"
		}
		reason := classifyGCPErrorReason(string(createBody))
		if !a.setQuotaNegativeCache(startEpoch, initialSub, reason) {
			return "", "account_changed"
		}
		if reason != "gcp_tos_required" {
			a.logQuotaFailure(onLog, "create_project", pid, createStatus, createBody, 0, nil)
		}
		return "", reason
	}

	var op gcpOperation
	if len(bytes.TrimSpace(createBody)) > 0 {
		if err := json.Unmarshal(createBody, &op); err != nil {
			return "", "network_error"
		}
	}
	var lastPollStatus int
	var lastPollBody []byte
	const maxProjectCreatePolls = 20
	deadline := a.now().Add(25 * time.Second)
	for polls := 0; !op.Done && op.Name != "" && polls < maxProjectCreatePolls && a.now().Before(deadline); polls++ {
		if quotaStale() {
			return "", "account_changed"
		}
		if err := a.sleep(ctx, 1500*time.Millisecond); err != nil {
			return "", "network_error"
		}
		pollStatus, pollBody, err := a.doBearerJSON(
			ctx,
			http.MethodGet,
			a.crmBaseURL+"/v1/"+op.Name,
			token,
			"",
			nil,
			12*time.Second,
		)
		if err != nil {
			return "", "network_error"
		}
		lastPollStatus = pollStatus
		lastPollBody = pollBody
		if pollStatus != http.StatusOK {
			if isTransientQuotaHTTPStatus(pollStatus) {
				continue
			}
			break
		}
		var nextOp gcpOperation
		if len(bytes.TrimSpace(pollBody)) > 0 {
			if err := json.Unmarshal(pollBody, &nextOp); err != nil {
				return "", "network_error"
			}
		}
		op = nextOp
	}

	if len(op.Error) > 0 || !op.Done {
		if len(op.Error) == 0 && (lastPollStatus == 0 || lastPollStatus == http.StatusOK || isTransientQuotaHTTPStatus(lastPollStatus)) {
			if quotaStale() {
				return "", "account_changed"
			}
			failBody := lastPollBody
			failStatus := lastPollStatus
			if lastPollStatus == 0 || lastPollStatus == http.StatusOK {
				failStatus = 0
				failBody = []byte("operation timed out")
			}
			a.logQuotaFailure(onLog, "poll_project", pid, failStatus, failBody, 0, nil)
			return "", "network_error"
		}
		var failStatus int
		var failBody []byte
		if len(op.Error) > 0 {
			failStatus = 0
			failBody, _ = json.Marshal(op.Error)
		} else {
			failStatus = lastPollStatus
			failBody = lastPollBody
		}
		reason := classifyGCPErrorReason(string(failBody))
		if !a.setQuotaNegativeCache(startEpoch, initialSub, reason) {
			return "", "account_changed"
		}
		if reason != "gcp_tos_required" {
			a.logQuotaFailure(onLog, "poll_project", pid, failStatus, failBody, 0, nil)
		}
		return "", reason
	}

	if quotaStale() {
		return "", "account_changed"
	}
	enableRes, err := a.enableDriveAPI(ctx, token, pid, quotaStale)
	if errors.Is(err, errQuotaStale) {
		return "", "account_changed"
	}
	if err != nil {
		return "", "network_error"
	}
	if enableRes.enabled {
		resPID, resReason := persistProject(pid, "created_colabone_project")
		if resPID != "" {
			emitAuthLog(
				onLog,
				fmt.Sprintf("已自动初始化免费 Drive 配额项目 (%s)，后续所有实例免弹窗自动挂载", pid),
				"云盘",
				"info",
			)
		}
		return resPID, resReason
	}
	if enableRes.transient {
		if quotaStale() {
			return "", "account_changed"
		}
		a.logQuotaFailure(onLog, "enable_drive_api", pid, enableRes.status, enableRes.body, enableRes.fallbackStatus, enableRes.fallbackBody)
		return "", "network_error"
	}

	reason := classifyEnableFailureReason(enableRes)
	if !a.setQuotaNegativeCache(startEpoch, initialSub, reason) {
		return "", "account_changed"
	}
	if reason != "gcp_tos_required" {
		a.logQuotaFailure(onLog, "enable_drive_api", pid, enableRes.status, enableRes.body, enableRes.fallbackStatus, enableRes.fallbackBody)
	}
	return "", reason
}

var errQuotaStale = errors.New("quota check superseded by account change")

type quotaEnableResult struct {
	enabled        bool
	transient      bool
	status         int
	body           []byte
	fallbackStatus int
	fallbackBody   []byte
}

func classifyEnableFailureReason(res quotaEnableResult) string {
	reason := classifyGCPErrorReason(string(res.body))
	if reason == "gcp_project_unavailable" && len(res.fallbackBody) > 0 {
		reason = classifyGCPErrorReason(string(res.fallbackBody))
	}
	return reason
}

func (a *ColabOneAuth) isQuotaStaleLocked(startEpoch uint64, initialSub string) bool {
	if a.quotaEpoch != startEpoch {
		return true
	}
	if initialSub != "" {
		curSub := ""
		if prof := a.getProfileMap(); prof != nil {
			curSub = prof["sub"]
		}
		if curSub != initialSub {
			return true
		}
	}
	return false
}

func (a *ColabOneAuth) setQuotaNegativeCache(startEpoch uint64, initialSub, reason string) bool {
	a.quotaMu.Lock()
	defer a.quotaMu.Unlock()
	if a.isQuotaStaleLocked(startEpoch, initialSub) {
		return false
	}
	a.quotaProbeCache = &quotaProbeCacheEntry{
		timestamp: a.now(),
		projectID: "",
		reason:    reason,
	}
	return true
}

func (a *ColabOneAuth) driveAPIEnabled(ctx context.Context, accessToken, projectID string) (bool, error) {
	return a.serviceEnabled(ctx, accessToken, projectID, "drive.googleapis.com")
}

func (a *ColabOneAuth) serviceEnabled(ctx context.Context, accessToken, projectID, serviceName string) (bool, error) {
	endpoint := fmt.Sprintf("%s/v1/projects/%s/services/%s", a.serviceUsageBaseURL, projectID, serviceName)
	status, body, err := a.doBearerJSON(ctx, http.MethodGet, endpoint, accessToken, projectID, nil, 10*time.Second)
	if err != nil {
		return false, err
	}
	if status == http.StatusForbidden && (isServiceUsageDisabledBody(body) || len(bytes.TrimSpace(body)) == 0) {
		// Newly REST-created projects have serviceusage.googleapis.com disabled by
		// default, so sending X-Goog-User-Project: projectID returns 403 SERVICE_DISABLED.
		// Retry the read-only service check without X-Goog-User-Project, while skipping
		// redundant calls when 403 is an unrelated IAM permission denial.
		status, body, err = a.doBearerJSON(ctx, http.MethodGet, endpoint, accessToken, "", nil, 10*time.Second)
		if err != nil {
			return false, err
		}
	}
	if status != http.StatusOK {
		if isTransientQuotaHTTPStatus(status) {
			return false, fmt.Errorf("transient service usage HTTP %d", status)
		}
		return false, nil
	}
	var payload struct {
		State string `json:"state"`
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			return false, err
		}
	}
	return strings.ToUpper(payload.State) == "ENABLED", nil
}

func (a *ColabOneAuth) enableProjectService(
	ctx context.Context,
	accessToken string,
	projectID string,
	serviceName string,
	quotaHeader string,
	maxAttempts int,
	bailOnServiceUsageDisabled bool,
	quotaStale func() bool,
) (quotaEnableResult, error) {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	endpoint := fmt.Sprintf("%s/v1/projects/%s/services/%s:enable", a.serviceUsageBaseURL, projectID, serviceName)
	var status int
	var body []byte
	var err error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if quotaStale != nil && quotaStale() {
			return quotaEnableResult{}, errQuotaStale
		}
		status, body, err = a.doBearerJSON(ctx, http.MethodPost, endpoint, accessToken, quotaHeader, map[string]any{}, 20*time.Second)
		if err != nil {
			return quotaEnableResult{}, err
		}
		if status == http.StatusOK {
			break
		}
		// Before bootstrapping, bail out immediately on Service Usage SERVICE_DISABLED so
		// the caller can switch headers or bootstrap baseline APIs instead of wasting retries.
		if bailOnServiceUsageDisabled && status == http.StatusForbidden && quotaHeader != "" && isServiceUsageDisabledBody(body) {
			return quotaEnableResult{status: status, body: body}, nil
		}
		// Freshly created or freshly bootstrapped GCP projects can take several seconds to
		// propagate across Service Usage edges; also retry transient 429/5xx statuses.
		if (status == http.StatusForbidden || status == http.StatusNotFound ||
			isTransientQuotaHTTPStatus(status)) && attempt+1 < maxAttempts {
			if serr := a.sleep(ctx, 2*time.Second); serr != nil {
				return quotaEnableResult{}, serr
			}
			continue
		}
		return quotaEnableResult{
			transient: isTransientQuotaHTTPStatus(status),
			status:    status,
			body:      body,
		}, nil
	}
	if status != http.StatusOK {
		return quotaEnableResult{
			transient: isTransientQuotaHTTPStatus(status),
			status:    status,
			body:      body,
		}, nil
	}

	var op gcpOperation
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &op); err != nil {
			return quotaEnableResult{}, err
		}
	}
	var lastPollStatus int
	var lastPollBody []byte
	const maxEnablePolls = 25
	deadline := a.now().Add(30 * time.Second)
	for polls := 0; !op.Done && op.Name != "" && polls < maxEnablePolls && a.now().Before(deadline); polls++ {
		if quotaStale != nil && quotaStale() {
			return quotaEnableResult{}, errQuotaStale
		}
		if serr := a.sleep(ctx, 1200*time.Millisecond); serr != nil {
			return quotaEnableResult{}, serr
		}
		pollStatus, pollBody, err := a.doBearerJSON(
			ctx,
			http.MethodGet,
			a.serviceUsageBaseURL+"/v1/"+op.Name,
			accessToken,
			quotaHeader,
			nil,
			12*time.Second,
		)
		if err != nil {
			return quotaEnableResult{}, err
		}
		lastPollStatus = pollStatus
		lastPollBody = pollBody
		if pollStatus != http.StatusOK {
			if isTransientQuotaHTTPStatus(pollStatus) {
				continue
			}
			break
		}
		var nextOp gcpOperation
		if len(bytes.TrimSpace(pollBody)) > 0 {
			if err := json.Unmarshal(pollBody, &nextOp); err != nil {
				return quotaEnableResult{}, err
			}
		}
		op = nextOp
	}

	if len(op.Error) > 0 {
		errJSON, _ := json.Marshal(op.Error)
		return quotaEnableResult{status: 0, body: errJSON}, nil
	}
	if op.Done {
		return quotaEnableResult{enabled: true, status: http.StatusOK}, nil
	}
	if quotaStale != nil && quotaStale() {
		return quotaEnableResult{}, errQuotaStale
	}
	enabled, err := a.serviceEnabled(ctx, accessToken, projectID, serviceName)
	if err != nil {
		return quotaEnableResult{}, err
	}
	if enabled {
		return quotaEnableResult{enabled: true, status: http.StatusOK}, nil
	}
	if lastPollStatus != 0 && lastPollStatus != http.StatusOK {
		return quotaEnableResult{
			transient: isTransientQuotaHTTPStatus(lastPollStatus),
			status:    lastPollStatus,
			body:      lastPollBody,
		}, nil
	}
	return quotaEnableResult{
		transient: true,
		status:    0,
		body:      []byte("enable operation timed out"),
	}, nil
}

func (a *ColabOneAuth) enableDriveAPI(
	ctx context.Context,
	accessToken string,
	projectID string,
	quotaStale func() bool,
) (quotaEnableResult, error) {
	// 1) Primary attempt with X-Goog-User-Project: projectID (works when Service Usage is active).
	res, err := a.enableProjectService(ctx, accessToken, projectID, "drive.googleapis.com", projectID, 3, true, quotaStale)
	if err != nil || res.enabled || res.transient {
		return res, err
	}
	if res.status != http.StatusForbidden && res.status != http.StatusNotFound && res.status != http.StatusBadRequest {
		return res, nil
	}

	// 2) Fallback without X-Goog-User-Project for bare REST-created projects where
	//    serviceusage.googleapis.com is not yet enabled on projectID.
	fbRes, fbErr := a.enableProjectService(ctx, accessToken, projectID, "drive.googleapis.com", "", 1, false, quotaStale)
	if fbErr != nil {
		return quotaEnableResult{}, fbErr
	}
	if fbRes.enabled {
		return fbRes, nil
	}
	res.fallbackStatus = fbRes.status
	res.fallbackBody = fbRes.body
	anyTransient := fbRes.transient

	// 3) Bootstrap baseline APIs via cloudapis.googleapis.com (matching gcloud projects
	//    create --enable-cloud-apis) or serviceusage.googleapis.com itself, then re-enable Drive API.
	bootstrapped := false
	for _, bootstrapSvc := range []string{"cloudapis.googleapis.com", "serviceusage.googleapis.com"} {
		for _, qp := range []string{"", projectID} {
			bRes, bErr := a.enableProjectService(ctx, accessToken, projectID, bootstrapSvc, qp, 1, false, quotaStale)
			if bErr != nil {
				return quotaEnableResult{}, bErr
			}
			if bRes.enabled {
				bootstrapped = true
				break
			}
			if bRes.transient {
				anyTransient = true
			}
		}
		if bootstrapped {
			break
		}
	}
	if bootstrapped {
		// Allow propagation retries even if initial responses still report Service Usage
		// disabled while the freshly enabled baseline APIs propagate across GCP edges.
		postRes, postErr := a.enableProjectService(ctx, accessToken, projectID, "drive.googleapis.com", projectID, 4, false, quotaStale)
		if postErr != nil || postRes.enabled {
			return postRes, postErr
		}
		if postRes.transient {
			return postRes, nil
		}
		postFBRes, postFBErr := a.enableProjectService(ctx, accessToken, projectID, "drive.googleapis.com", "", 1, false, quotaStale)
		if postFBErr != nil {
			return quotaEnableResult{}, postFBErr
		}
		if postFBRes.enabled {
			return postFBRes, nil
		}
		postRes.fallbackStatus = postFBRes.status
		postRes.fallbackBody = postFBRes.body
		if postFBRes.transient {
			postRes.transient = true
		}
		return postRes, nil
	}

	if anyTransient {
		res.transient = true
	}
	return res, nil
}

func (a *ColabOneAuth) doBearerJSON(
	ctx context.Context,
	method string,
	endpoint string,
	accessToken string,
	quotaProject string,
	payload any,
	timeout time.Duration,
) (int, []byte, error) {
	var bodyReader io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		bodyReader = bytes.NewReader(encoded)
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, method, endpoint, bodyReader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if quotaProject != "" {
		req.Header.Set("X-Goog-User-Project", quotaProject)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHTTPResponseBodyBytes))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

// BuildRcloneConf builds the [colabone_gdrive] rclone INI configuration from
// the saved Google OAuth credentials. When quotaProject is nil, the cached
// "cloud_project" vault entry is used if valid.
func (a *ColabOneAuth) BuildRcloneConf(ctx context.Context, quotaProject *string) (string, error) {
	if !a.HasDriveScope() {
		return "", nil
	}
	creds, err := a.Credentials(ctx, "colab")
	if err != nil {
		return "", err
	}

	scopes := creds.Scopes
	if len(scopes) == 0 {
		var saved ColabOneCredentials
		_, _ = a.vaultGetJSON("colab", &saved)
		scopes = saved.Scopes
	}

	driveScope := ""
	for _, s := range scopes {
		if strings.HasSuffix(s, "/drive") {
			driveScope = "drive"
			break
		}
	}
	if driveScope == "" {
		for _, s := range scopes {
			if strings.Contains(s, "drive.file") {
				driveScope = "drive.file"
				break
			}
		}
	}
	if driveScope == "" || creds.Token == "" || creds.RefreshToken == "" {
		return "", nil
	}
	if strings.ContainsAny(creds.ClientID, "\r\n\x00") || strings.ContainsAny(creds.ClientSecret, "\r\n\x00") {
		return "", errors.New("invalid OAuth client credentials for rclone config")
	}

	type rcloneToken struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		RefreshToken string `json:"refresh_token"`
		Expiry       string `json:"expiry,omitempty"`
	}
	normalizedExpiry := ""
	if exp, ok := creds.ExpiryTime(); ok {
		normalizedExpiry = exp.UTC().Format(time.RFC3339)
	}
	tokenBytes, err := json.Marshal(rcloneToken{
		AccessToken:  creds.Token,
		TokenType:    "Bearer",
		RefreshToken: creds.RefreshToken,
		Expiry:       normalizedExpiry,
	})
	if err != nil {
		return "", err
	}

	qp := ""
	if quotaProject != nil {
		qp = *quotaProject
	} else {
		qp = a.getVaultString("cloud_project")
	}

	lines := make([]string, 0, 8)
	if isValidProjectID(qp) {
		lines = append(lines, "# colabone_quota_project = "+qp)
	}
	lines = append(lines,
		"[colabone_gdrive]",
		"type = drive",
		"client_id = "+creds.ClientID,
		"client_secret = "+creds.ClientSecret,
		"scope = "+driveScope,
		"token = "+string(tokenBytes),
		"",
	)
	return strings.Join(lines, "\n"), nil
}

// ColabBegin starts a manual copy-paste PKCE S256 OAuth 2.0 flow targeting
// REMOTE_REDIRECT_URI and returns {"url": "<authorization-url>"}.
func (a *ColabOneAuth) ColabBegin() (map[string]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.hasActiveSessions() {
		return nil, ErrActiveSessionsExist
	}
	a.loginEvt.Clear()
	flow, err := a.newOAuthFlow(REMOTE_REDIRECT_URI)
	if err != nil {
		return nil, err
	}
	a.flow = flow
	return map[string]string{"url": flow.authorizationURL(true)}, nil
}

// ColabFinish completes the active PKCE OAuth flow using a pasted authorization
// code, callback URL, or full authorization page text.
func (a *ColabOneAuth) ColabFinish(ctx context.Context, code string) (map[string]any, error) {
	return a.colabFinishWithFlow(ctx, code, nil)
}

func (a *ColabOneAuth) colabFinishWithFlow(ctx context.Context, code string, explicitFlow *oauthFlow) (map[string]any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if explicitFlow != nil && explicitFlow.resetGen != a.authResetGen {
		return nil, ErrAuthCodeInvalid
	}

	if explicitFlow != nil && (a.loginEvt.IsSet() || isClosed(explicitFlow.doneCh)) {
		return map[string]any{"ok": true}, nil
	}

	targetFlow := explicitFlow
	if targetFlow == nil {
		targetFlow = a.flow
	}
	if targetFlow == nil {
		return nil, ErrLoginNotStarted
	}
	if targetFlow.resetGen != a.authResetGen {
		return nil, ErrAuthCodeInvalid
	}
	if a.hasActiveSessions() {
		return nil, ErrActiveSessionsExist
	}

	parsed, err := ParseAuthorizationReply(code)
	if err != nil {
		return nil, err
	}

	creds, err := a.exchangeAuthorizationCode(ctx, targetFlow, parsed)
	if err != nil {
		return nil, ErrAuthCodeInvalid
	}

	profile, err := a.FetchProfile(ctx, creds.Token)
	if err != nil {
		return nil, ErrProfileFetchFailed
	}

	a.quotaMu.Lock()
	a.quotaEpoch++
	prev := a.getProfileMap()
	if prev == nil || profile["sub"] != prev["sub"] {
		_ = a.vaultDelete("cloud_project")
	}
	a.quotaProbeCache = nil
	if err := a.vaultSet("google_profile", profile); err != nil {
		a.quotaMu.Unlock()
		return nil, err
	}
	if err := a.vaultSet("colab", creds); err != nil {
		a.quotaMu.Unlock()
		return nil, err
	}
	a.quotaMu.Unlock()
	if explicitFlow == nil || a.flow == targetFlow {
		a.flow = nil
	}
	a.loginEvt.Set()
	return map[string]any{"ok": true}, nil
}

// ColabLogin runs an ephemeral localhost loopback HTTP server to receive the
// Google OAuth 2.0 redirect callback, invoking readyCallback with the
// authorization URL once listening. Manual completion via ColabBegin/ColabFinish
// while ColabLogin is waiting cleanly finishes the login.
func (a *ColabOneAuth) ColabLogin(ctx context.Context, readyCallback func(url string)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case a.loopbackSem <- struct{}{}:
		defer func() { <-a.loopbackSem }()
	case <-ctx.Done():
		return ctx.Err()
	}

	a.mu.Lock()
	if a.hasActiveSessions() {
		a.mu.Unlock()
		return ErrActiveSessionsExist
	}
	a.mu.Unlock()

	const maxLoopbackBindAttempts = 8

	listenFn := a.listenFn
	if listenFn == nil {
		listenFn = net.Listen
	}
	var (
		ln          net.Listener
		ln6         net.Listener
		redirectURI string
	)
	for attempt := 0; attempt < maxLoopbackBindAttempts; attempt++ {
		l4, err := listenFn("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
		tcpAddr, ok := l4.Addr().(*net.TCPAddr)
		if !ok {
			_ = l4.Close()
			return errors.New("unexpected loopback listener address")
		}
		l6, err6 := listenFn("tcp6", fmt.Sprintf("[::1]:%d", tcpAddr.Port))
		if err6 == nil {
			ln = l4
			ln6 = l6
			redirectURI = fmt.Sprintf("http://localhost:%d/", tcpAddr.Port)
			break
		}
		if isLoopbackAddrInUse(err6) && attempt+1 < maxLoopbackBindAttempts {
			_ = l4.Close()
			continue
		}
		ln = l4
		redirectURI = fmt.Sprintf("http://127.0.0.1:%d/", tcpAddr.Port)
		break
	}

	a.loopbackActive.Store(true)
	defer a.loopbackActive.Store(false)

	a.mu.Lock()
	a.loginEvt.Clear()
	localFlow, err := a.newOAuthFlow(redirectURI)
	if err != nil {
		a.mu.Unlock()
		_ = ln.Close()
		if ln6 != nil {
			_ = ln6.Close()
		}
		return err
	}
	a.flow = localFlow
	authURL := localFlow.authorizationURL(false)
	expectedState := localFlow.state
	doneCh := a.loginEvt.WaitCh()
	localFlow.doneCh = doneCh
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		if a.flow == localFlow {
			a.flow = nil
		}
		a.mu.Unlock()
	}()

	loginCtx, cancelLogin := context.WithCancel(ctx)
	defer cancelLogin()

	var (
		cbMu       sync.Mutex
		cbHandled  bool
		cbSuccess  bool
		cbEmail    string
		cbMessage  string
		cbErr      error
		cbDoneCh   = make(chan struct{})
		cbDoneOnce sync.Once
	)
	signalCBDone := func() {
		cbDoneOnce.Do(func() { close(cbDoneCh) })
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/favicon.ico" {
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		params := r.URL.Query()
		gotState := params.Get("state")
		if !hmac.Equal([]byte(gotState), []byte(expectedState)) {
			http.Error(w, "Invalid OAuth state", http.StatusBadRequest)
			return
		}

		locale := a.Locale()
		texts, exists := OAuthCallbackTexts[locale]
		langAttr := locale
		if !exists {
			langAttr = defaultLocale
			texts = OAuthCallbackTexts[defaultLocale]
		}

		// Serialize valid-state callbacks so duplicate browser GETs or reloads
		// never re-submit a single-use authorization code after the first callback
		// or manual ColabFinish has already completed.
		cbMu.Lock()
		if !cbHandled {
			cbHandled = true
			if a.isFlowReset(localFlow) {
				cbMessage = texts.Error
				cbErr = ErrAuthCodeInvalid
				signalCBDone()
			} else if a.loginEvt.IsSet() || isClosed(doneCh) {
				cbSuccess = true
				if prof := a.getProfileMap(); prof != nil {
					cbEmail = prof["email"]
				}
				cbMessage = texts.Success
			} else if params.Get("error") != "" {
				cbMessage = texts.Error
				cbErr = ErrAuthCancelled
				a.loginEvt.Set()
			} else if _, ferr := a.colabFinishWithFlow(loginCtx, params.Get("code"), localFlow); ferr != nil {
				if !a.isFlowReset(localFlow) && (a.loginEvt.IsSet() || isClosed(doneCh)) {
					// Manual ColabFinish succeeded concurrently while this callback was exchanging the code.
					cbSuccess = true
					if prof := a.getProfileMap(); prof != nil {
						cbEmail = prof["email"]
					}
					cbMessage = texts.Success
				} else {
					cbMessage = texts.Error
					recorded := ferr
					if recorded.Error() == "" {
						recorded = errors.New(texts.Error)
					}
					cbErr = recorded
					if a.isFlowReset(localFlow) {
						signalCBDone()
					} else {
						a.loginEvt.Set()
					}
				}
			} else {
				cbSuccess = true
				if prof := a.getProfileMap(); prof != nil {
					cbEmail = prof["email"]
				}
				cbMessage = texts.Success
			}
		}
		succeeded := cbSuccess
		email := cbEmail
		message := cbMessage
		cbMu.Unlock()

		renderer := a.renderOAuthPage
		if renderer == nil {
			renderer = defaultRenderOAuthPage
		}
		content := []byte(renderer(langAttr, succeeded, message, email))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		w.Header().Set("Connection", "close")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", oauthCallbackCSPHeaderContent)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
	})

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	var serveWG sync.WaitGroup
	serveErrCh := make(chan error, 2)
	serveListener := func(l net.Listener, primary bool) {
		defer serveWG.Done()
		err := srv.Serve(l)
		if err != nil && !errors.Is(err, http.ErrServerClosed) && primary {
			serveErrCh <- err
		}
	}
	serveWG.Add(1)
	go serveListener(ln, true)
	if ln6 != nil {
		serveWG.Add(1)
		go serveListener(ln6, false)
	}
	go func() {
		serveWG.Wait()
		close(serveErrCh)
	}()

	var shutdownOnce sync.Once
	shutdownServer := func() {
		shutdownOnce.Do(func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := srv.Shutdown(shutdownCtx); err != nil {
				_ = srv.Close()
			}
			cancel()
			for range serveErrCh {
			}
		})
	}
	defer shutdownServer()

	if readyCallback != nil {
		readyCallback(authURL)
	}

	timer := time.NewTimer(a.loopbackTimeout)
	defer timer.Stop()

	completedViaDone := false
	var waitErr error
	select {
	case <-doneCh:
		completedViaDone = true
	case <-cbDoneCh:
		completedViaDone = true
	case serr, ok := <-serveErrCh:
		if ok && serr != nil {
			waitErr = serr
		}
	case <-timer.C:
		waitErr = ErrLoginTimeout
	case <-ctx.Done():
		waitErr = ctx.Err()
	}

	if !completedViaDone {
		cancelLogin()
	}
	// Gracefully wait for any in-flight callback response to finish writing
	// before inspecting cbErr or returning.
	shutdownServer()

	cbMu.Lock()
	firstErr := cbErr
	cbDone := cbHandled
	cbSucceeded := cbSuccess
	cbMu.Unlock()

	if cbSucceeded {
		return nil
	}
	if completedViaDone {
		if firstErr != nil {
			return firstErr
		}
		return nil
	}
	// If wait timed out / ctx cancelled, check whether manual ColabFinish completed
	// without any browser callback handling the login.
	if !cbDone {
		select {
		case <-doneCh:
			return nil
		default:
		}
		if waitErr != nil {
			return waitErr
		}
		return ErrLoginTimeout
	}
	// A browser callback was in flight when timeout/cancel triggered cancelLogin():
	// preserve explicit browser cancellation, otherwise report the wait error rather
	// than a secondary exchange failure caused by cancelLogin().
	if errors.Is(firstErr, ErrAuthCancelled) {
		return firstErr
	}
	if waitErr != nil {
		return waitErr
	}
	if firstErr != nil {
		return firstErr
	}
	return ErrLoginTimeout
}

func (a *ColabOneAuth) newOAuthFlow(redirectURI string) (*oauthFlow, error) {
	verifier, err := generatePKCEVerifier(128)
	if err != nil {
		return nil, err
	}
	state, err := generateRandomState(32)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	return &oauthFlow{
		clientID:      COLABONE_OAUTH_CLIENT_ID,
		clientSecret:  COLABONE_OAUTH_CLIENT_SECRET,
		authURI:       a.authURI,
		tokenURI:      a.tokenURI,
		redirectURI:   redirectURI,
		scopes:        append([]string(nil), PUBLIC_SCOPES...),
		codeVerifier:  verifier,
		codeChallenge: challenge,
		state:         state,
		resetGen:      a.authResetGen,
	}, nil
}

const winWSAEADDRINUSE = syscall.Errno(10048)

func isLoopbackAddrInUse(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	if runtime.GOOS == "windows" {
		var errno syscall.Errno
		if errors.As(err, &errno) && errno == winWSAEADDRINUSE {
			return true
		}
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "address already in use") ||
		strings.Contains(lower, "only one usage of each socket address")
}

func (a *ColabOneAuth) exchangeAuthorizationCode(
	ctx context.Context,
	flow *oauthFlow,
	code string,
) (*ColabOneCredentials, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", flow.redirectURI)
	form.Set("client_id", flow.clientID)
	form.Set("client_secret", flow.clientSecret)
	form.Set("code_verifier", flow.codeVerifier)

	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, flow.tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHTTPResponseBodyBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange HTTP %d", resp.StatusCode)
	}

	var tokResp oauthTokenResponse
	if err := json.Unmarshal(body, &tokResp); err != nil {
		return nil, err
	}
	if tokResp.AccessToken == "" {
		return nil, errors.New("token exchange response missing access_token")
	}

	scopes := append([]string(nil), flow.scopes...)
	if granted := strings.Fields(tokResp.Scope); len(granted) > 0 {
		scopes = granted
	}

	expiry := ""
	if tokResp.ExpiresIn > 0 {
		expiry = a.now().UTC().Add(clampTokenExpiryDuration(int64(tokResp.ExpiresIn))).Format(time.RFC3339)
	}

	return &ColabOneCredentials{
		Token:          tokResp.AccessToken,
		RefreshToken:   tokResp.RefreshToken,
		IDToken:        tokResp.IDToken,
		TokenURI:       COLABONE_OAUTH_TOKEN_URI,
		ClientID:       flow.clientID,
		ClientSecret:   flow.clientSecret,
		Scopes:         scopes,
		UniverseDomain: defaultUniverseDomain,
		Expiry:         expiry,
	}, nil
}

type oauthTokenResponse struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	IDToken      string    `json:"id_token"`
	TokenType    string    `json:"token_type"`
	Scope        string    `json:"scope"`
	ExpiresIn    flexInt64 `json:"expires_in"`
}

const (
	minInt64Float64       = -9223372036854775808.0 // -2^63 (exact in IEEE 754 float64)
	maxInt64Float64       = 9223372036854775808.0  //  2^63 (exact in IEEE 754 float64; exclusive upper bound)
	maxTokenExpirySeconds = math.MaxInt64 / int64(time.Second)
)

func clampTokenExpiryDuration(expiresIn int64) time.Duration {
	if expiresIn <= 0 {
		return 0
	}
	if expiresIn > maxTokenExpirySeconds {
		expiresIn = maxTokenExpirySeconds
	}
	return time.Duration(expiresIn) * time.Second
}

type flexInt64 int64

func parseFlexInt64(s string) (int64, error) {
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		return v, nil
	} else if errors.Is(err, strconv.ErrRange) {
		return 0, fmt.Errorf("int64 value %q out of range", s)
	}
	if strings.ContainsAny(s, "_xXpP") {
		return 0, fmt.Errorf("unsupported numeric syntax %q", s)
	}
	fv, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(fv) || math.IsInf(fv, 0) {
		return 0, fmt.Errorf("non-finite float value %q cannot convert to int64", s)
	}
	if fv < minInt64Float64 || fv >= maxInt64Float64 {
		return 0, fmt.Errorf("float value %q out of int64 range", s)
	}
	return int64(fv), nil
}

func (f *flexInt64) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*f = 0
		return nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			*f = 0
			return nil
		}
		v, err := parseFlexInt64(s)
		if err != nil {
			return err
		}
		*f = flexInt64(v)
		return nil
	}
	v, err := parseFlexInt64(trimmed)
	if err != nil {
		return err
	}
	*f = flexInt64(v)
	return nil
}

type gcpOperation struct {
	Name  string         `json:"name"`
	Done  bool           `json:"done"`
	Error map[string]any `json:"error"`
}

func emitAuthLog(onLog func(msg, source, level string), msg, source, level string) {
	if onLog == nil || msg == "" {
		return
	}
	if source == "" {
		source = "云盘"
	}
	if level == "" {
		level = "info"
	}
	onLog(msg, source, level)
}

func isValidProjectID(projectID string) bool {
	return gcpProjectIDRE.MatchString(projectID)
}

func isTransient5xx(code int) bool {
	return code == http.StatusInternalServerError ||
		code == http.StatusBadGateway ||
		code == http.StatusServiceUnavailable ||
		code == http.StatusGatewayTimeout
}

func isTransientQuotaHTTPStatus(code int) bool {
	return code == http.StatusTooManyRequests || isTransient5xx(code)
}

func isRetryableRefreshError(statusCode int, body []byte) bool {
	if statusCode == http.StatusRequestTimeout ||
		statusCode == http.StatusTooManyRequests ||
		(statusCode >= 500 && statusCode <= 599) {
		return true
	}
	var payload struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		for _, candidate := range []string{payload.Error, payload.ErrorDescription} {
			switch candidate {
			case "internal_failure", "server_error", "temporarily_unavailable":
				return true
			}
		}
	}
	return false
}

func classifyGCPErrorReason(body string) string {
	lower := strings.ToLower(body)
	if strings.Contains(lower, "terms of service") || strings.Contains(lower, `"tos"`) {
		return "gcp_tos_required"
	}
	return "gcp_project_unavailable"
}

func isServiceUsageDisabledBody(body []byte) bool {
	lower := strings.ToLower(string(body))
	if !strings.Contains(lower, "serviceusage.googleapis.com") && !strings.Contains(lower, "service usage api") {
		return false
	}
	return strings.Contains(lower, "service_disabled") ||
		strings.Contains(lower, "accessnotconfigured") ||
		strings.Contains(lower, "has not been used in project") ||
		strings.Contains(lower, "is disabled")
}

func sanitizeGCPDiagText(raw, projectID string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = gcpURLRE.ReplaceAllString(s, "")
	s = gcpSecretRE.ReplaceAllString(s, "[REDACTED]")
	if projectID != "" {
		s = strings.ReplaceAll(s, projectID, "[project]")
	}
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) > 160 {
		s = strings.TrimSpace(string(runes[:160])) + "..."
	}
	return s
}

func summarizeGCPError(httpStatus int, body []byte, projectID string) string {
	type gcpErrorPayload struct {
		Code    int    `json:"code"`
		Status  string `json:"status"`
		Message string `json:"message"`
		Details []struct {
			Reason   string            `json:"reason"`
			Metadata map[string]string `json:"metadata"`
		} `json:"details"`
		Errors []struct {
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	var wrapper struct {
		Error *gcpErrorPayload `json:"error"`
	}
	var flat gcpErrorPayload
	var errObj *gcpErrorPayload
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		if json.Unmarshal(trimmed, &wrapper) == nil && wrapper.Error != nil {
			errObj = wrapper.Error
		} else if json.Unmarshal(trimmed, &flat) == nil && (flat.Message != "" || flat.Status != "" || flat.Code != 0) {
			errObj = &flat
		}
	}

	parts := make([]string, 0, 4)
	if httpStatus > 0 {
		parts = append(parts, fmt.Sprintf("HTTP %d", httpStatus))
	}
	if errObj != nil {
		codeTag := strings.TrimSpace(errObj.Status)
		reasonTag := ""
		svcTag := ""
		for _, d := range errObj.Details {
			if reasonTag == "" && strings.TrimSpace(d.Reason) != "" {
				reasonTag = strings.TrimSpace(d.Reason)
			}
			if svcTag == "" && d.Metadata != nil && strings.TrimSpace(d.Metadata["service"]) != "" {
				svcTag = strings.TrimSpace(d.Metadata["service"])
			}
		}
		if reasonTag == "" {
			for _, e := range errObj.Errors {
				if strings.TrimSpace(e.Reason) != "" {
					reasonTag = strings.TrimSpace(e.Reason)
					break
				}
			}
		}
		switch {
		case codeTag != "" && reasonTag != "" && !strings.EqualFold(codeTag, reasonTag):
			parts = append(parts, codeTag+"/"+reasonTag)
		case codeTag != "":
			parts = append(parts, codeTag)
		case reasonTag != "":
			parts = append(parts, reasonTag)
		case httpStatus == 0 && errObj.Code != 0:
			parts = append(parts, fmt.Sprintf("code %d", errObj.Code))
		}
		if svcTag != "" {
			parts = append(parts, svcTag)
		}
		if msg := sanitizeGCPDiagText(errObj.Message, projectID); msg != "" {
			parts = append(parts, msg)
		}
	} else if len(trimmed) > 0 && trimmed[0] != '<' {
		if msg := sanitizeGCPDiagText(string(trimmed), projectID); msg != "" {
			parts = append(parts, msg)
		}
	}
	if len(parts) == 0 {
		return "unknown error"
	}
	return strings.Join(parts, " · ")
}

func (a *ColabOneAuth) logQuotaFailure(
	onLog func(msg, source, level string),
	stage string,
	projectID string,
	status int,
	body []byte,
	fallbackStatus int,
	fallbackBody []byte,
) {
	if onLog == nil {
		return
	}
	detail := summarizeGCPError(status, body, projectID)
	if fallbackStatus > 0 || len(bytes.TrimSpace(fallbackBody)) > 0 {
		fbDetail := summarizeGCPError(fallbackStatus, fallbackBody, projectID)
		if fbDetail != "" && fbDetail != detail {
			detail = detail + "；无配额头回退：" + fbDetail
		}
	}
	var msg string
	switch stage {
	case "list_projects":
		msg = fmt.Sprintf("检测免费 Drive 配额项目列表失败（%s）", detail)
	case "create_project":
		msg = fmt.Sprintf("自动创建免费 Drive 配额项目 (%s) 失败（%s）", projectID, detail)
	case "poll_project":
		msg = fmt.Sprintf("等待免费 Drive 配额项目 (%s) 创建完成失败（%s）", projectID, detail)
	case "enable_drive_api":
		msg = fmt.Sprintf("未能为免费 Drive 配额项目 (%s) 启用 Drive API（%s）", projectID, detail)
	default:
		return
	}
	emitAuthLog(onLog, msg, "云盘", "warning")
}

func shortSHA256Hex(input string, n int) string {
	sum := sha256.Sum256([]byte(input))
	encoded := hex.EncodeToString(sum[:])
	if n >= len(encoded) {
		return encoded
	}
	return encoded[:n]
}

func generatePKCEVerifier(length int) (string, error) {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
	max := big.NewInt(int64(len(charset)))
	buf := make([]byte, length)
	for i := 0; i < length; i++ {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = charset[idx.Int64()]
	}
	return string(buf), nil
}

func generateRandomState(numBytes int) (string, error) {
	buf := make([]byte, numBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func defaultRenderOAuthPage(locale string, ok bool, message, email string) string {
	lang := locale
	if _, exists := OAuthCallbackTexts[lang]; !exists {
		lang = defaultLocale
	}
	stateClass := "is-error"
	if ok {
		stateClass = "is-success"
	}
	escapedMsg := html.EscapeString(message)
	emailBlock := ""
	if trimmed := strings.TrimSpace(email); ok && trimmed != "" {
		emailBlock = `<p class="co-email">` + html.EscapeString(trimmed) + `</p>`
	}
	return fmt.Sprintf(
		`<!doctype html><html lang="%s"><head><meta charset="utf-8"><title>ColabOne</title></head><body class="%s"><main><p>%s</p>%s<button id="co-close-btn" type="button">Close</button></main></body></html>`,
		html.EscapeString(lang),
		stateClass,
		escapedMsg,
		emailBlock,
	)
}
