// Package webconsole implements ColabOne's pure-Go remote web console HTTP
// server, session/ticket authentication, CSRF protection, output download
// proxy, and static frontend serving.
package webconsole

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/luolongfei/ColabOne/console/internal/brandicon"
	"github.com/luolongfei/ColabOne/console/internal/fsutil"
)

const (
	// DefaultPort is the default TCP listen port for the remote web console.
	DefaultPort = 3232
	// MinPort is the minimum allowed non-privileged TCP port.
	MinPort = 1024
	// MaxPort is the maximum allowed TCP port.
	MaxPort = 65535

	// BindModeLoopback binds the web console strictly to 127.0.0.1.
	BindModeLoopback = "loopback"
	// BindModeLAN binds the web console to 0.0.0.0 for LAN access.
	BindModeLAN = "lan"

	// SessionCookieName is the HttpOnly session cookie issued to web console clients.
	SessionCookieName = "colabone_web_session"
	// VaultPasscodeKey is the vault entry key storing the web console passcode.
	VaultPasscodeKey = "web_console_passcode"
	// VaultSessionsKey is the vault entry key storing persisted web console session digests.
	VaultSessionsKey = "web_console_sessions"
	// ConfigFilename is the JSON configuration file persisted in ConfigDir.
	ConfigFilename = "web-console.json"

	// LoginTicketTTL is the lifetime of a short-lived browser unlock ticket.
	LoginTicketTTL = 60 * time.Second
	// SessionTTL is the sliding lifetime of an authenticated web console session
	// (400 days matches the RFC 6265bis browser cookie Max-Age ceiling and is
	// automatically renewed on active visits so users stay signed in across restarts).
	SessionTTL = 400 * 24 * time.Hour

	// ClientHeaderName is the custom anti-CSRF header required on mutating web API calls.
	ClientHeaderName = "X-ColabOne-Client"
	// ClientHeaderValue is the expected value of ClientHeaderName.
	ClientHeaderValue = "web"
)

const (
	webConsoleLogSource             = "网页控制台"
	maxActiveSessions               = 64
	maxPendingTickets               = 32
	maxRateLimitIPs                 = 1024
	loginRateLimitMaxFailures       = 5
	loginRateLimitGlobalMaxFailures = 25
	loginRateLimitWindow            = 5 * time.Minute
	maxLoginBodyBytes               = 4 << 10
	maxCommandBodyBytes             = 64 << 20
	maxMediaUploadCommandBodyBytes  = 240 << 20
	commandActionPeekBytes          = 4 << 10
	maxOutputSizeBytes              = 512 << 20
	maxOutputBufferBytes            = 32 << 20
	maxOutputChunkBytes             = 512 << 10
	gzipMinBytes                    = 1024
	serverShutdownTimeout           = 2 * time.Second
	passcodeMinRunes                = 8
	passcodeMaxRunes                = 128
	maxPublicOriginRunes            = 300
	sessionRenewalInterval          = 24 * time.Hour
	persistedSessionsVersion        = 1
)

// VaultStore abstracts encrypted credential persistence for the web console passcode.
type VaultStore interface {
	GetJSON(name string, dest any) (bool, error)
	Set(name string, value any) error
	Delete(name string) error
}

// BridgeCaller dispatches a ColabOne controller method call and returns its raw JSON result.
type BridgeCaller func(ctx context.Context, method string, payload map[string]any) (string, error)

// LogFunc records a sanitized activity entry.
type LogFunc func(message, source, level string)

// LocaleFunc returns the desktop host's active UI locale (e.g. "zh-CN", "en-US").
type LocaleFunc func() string

// FrontendFSFunc returns the embedded or disk filesystem serving the compiled frontend.
type FrontendFSFunc func() fs.FS

// ColabOneWebConsoleConfig is the persisted JSON configuration for the web console.
type ColabOneWebConsoleConfig struct {
	Enabled      bool   `json:"enabled"`
	BindMode     string `json:"bind_mode"`
	Port         int    `json:"port"`
	PublicOrigin string `json:"public_origin"`
}

// ColabOneWebConsoleState is the live, secret-redacted status snapshot exposed to the desktop UI.
type ColabOneWebConsoleState struct {
	Enabled         bool     `json:"enabled"`
	Running         bool     `json:"running"`
	BindMode        string   `json:"bind_mode"`
	ListenAddr      string   `json:"listen_addr"`
	Port            int      `json:"port"`
	LocalURL        string   `json:"local_url"`
	LanURLs         []string `json:"lan_urls,omitempty"`
	PublicOrigin    string   `json:"public_origin"`
	HasPasscode     bool     `json:"has_passcode"`
	PasscodePreview string   `json:"passcode_preview"`
	ActiveSessions  int      `json:"active_sessions"`
	LastError       string   `json:"last_error,omitempty"`
}

// ColabOneWebConsoleUpdateRequest carries partial configuration updates for hot-apply.
type ColabOneWebConsoleUpdateRequest struct {
	Enabled      *bool   `json:"enabled,omitempty"`
	BindMode     *string `json:"bind_mode,omitempty"`
	Port         *int    `json:"port,omitempty"`
	PublicOrigin *string `json:"public_origin,omitempty"`
}

// ColabOneWebConsoleTicket describes a short-lived browser unlock URL.
type ColabOneWebConsoleTicket struct {
	URL            string `json:"url"`
	LocalURL       string `json:"local_url"`
	ExpiresAt      int64  `json:"expires_at"`
	TTLSeconds     int    `json:"ttl_seconds"`
	UsesPublicHost bool   `json:"uses_public_host"`
	SingleUse      bool   `json:"single_use"`
}

// ColabOneWebCapabilities advertises which desktop/web features are available in web mode.
type ColabOneWebCapabilities struct {
	Runtime                   string   `json:"runtime"`
	Version                   string   `json:"version"`
	BindMode                  string   `json:"bind_mode,omitempty"`
	LocalURL                  string   `json:"local_url,omitempty"`
	LANURLs                   []string `json:"lan_urls,omitempty"`
	PublicOrigin              string   `json:"public_origin,omitempty"`
	CanOpenNativeCanvasWindow bool     `json:"can_open_native_canvas_window"`
	CanStandaloneLogWindow    bool   `json:"can_standalone_log_window"`
	CanManageWebConsole       bool   `json:"can_manage_web_console"`
	CanManageCloseBehavior    bool   `json:"can_manage_close_behavior"`
	CanManageShortcuts        bool   `json:"can_manage_shortcuts"`
	CanSelfUpdate             bool   `json:"can_self_update"`
	CanMigrate                bool   `json:"can_migrate"`
	CanRevealHostSecrets      bool   `json:"can_reveal_host_secrets"`
	CanLoopbackOAuth          bool   `json:"can_loopback_oauth"`
	CanDownloadOutput         bool   `json:"can_download_output"`
}

// ColabOneWebConsoleOptions configures a ColabOneWebConsole instance.
type ColabOneWebConsoleOptions struct {
	ConfigDir              string
	Vault                  VaultStore
	CallBridge             BridgeCaller
	FetchSnapshot          func(ctx context.Context) (string, error)
	ServeOutputMedia       func(ctx context.Context, rw http.ResponseWriter, r *http.Request, id string)
	ServeShareOutputMedia  func(ctx context.Context, rw http.ResponseWriter, r *http.Request, id string)
	ServeOutputShareMedia  func(ctx context.Context, rw http.ResponseWriter, r *http.Request, id string)
	StreamOutputMedia      func(ctx context.Context, id string, w io.Writer, sanitizeErrors bool) (ColabOneOutputStreamMeta, error)
	FrontendFS             FrontendFSFunc
	AppVersion             string
	GetLocale              LocaleFunc
	OnLog                  LogFunc
	OnStateChange          func(ColabOneWebConsoleState)
	OnWorkflowSharesChange func(ColabOneWorkflowShareListState)
	OnOutputSharesChange   func(ColabOneOutputShareListState)
	Now                    func() time.Time
}

type colabOneWebSession struct {
	createdAt         time.Time
	lastSeen          time.Time
	expiresAt         time.Time
	cookieRefreshedAt time.Time
}

type colabOnePersistedWebSession struct {
	DigestHex string `json:"d"`
	CreatedAt int64  `json:"c"`
	LastSeen  int64  `json:"l"`
	ExpiresAt int64  `json:"e"`
}

type colabOnePersistedWebSessionStore struct {
	Version      int                           `json:"v"`
	PasscodeHash string                        `json:"p"`
	Sessions     []colabOnePersistedWebSession `json:"s"`
}

type colabOneWebTicket struct {
	createdAt time.Time
	expiresAt time.Time
	singleUse bool
}

type colabOneWebMediaChunk struct {
	Data      string `json:"data"`
	Size      int64  `json:"size"`
	Offset    int64  `json:"offset"`
	SHA256    string `json:"sha256"`
	Extension string `json:"extension"`
	Filename  string `json:"filename"`
	Source    string `json:"source"`
}

// ColabOneWebConsole manages the remote HTTP web console lifecycle, sessions,
// authentication, command filtering, and static asset serving.
type ColabOneWebConsole struct {
	opts ColabOneWebConsoleOptions

	opMu                 sync.Mutex // serializes StartIfEnabled, Update, Passcode, SetPasscode, Close
	workflowShareOpMu    sync.Mutex // serializes CreateWorkflowShare, RevokeWorkflowShare, DeleteWorkflowShare (never blocks on shutdownHTTPServer)
	outputShareOpMu      sync.Mutex // serializes CreateOutputShare, RevokeOutputShare, DeleteOutputShare (never blocks on shutdownHTTPServer)
	persistMu            sync.Mutex // serializes persisted session snapshots written to Vault
	sharePersistMu       sync.Mutex // serializes workflow-shares.json and VaultWorkflowShareSessionsKey writes
	outputSharePersistMu sync.Mutex // serializes output-shares.json, VaultOutputShareSessionsKey, and VaultOutputSharePasswordsKey writes
	mu                   sync.RWMutex

	cfg           ColabOneWebConsoleConfig
	running       bool
	stopping      bool
	closed        bool
	listenAddr    string
	srv           *http.Server
	listeners     []net.Listener
	srvDone       chan struct{}
	passcode      string
	passcodeReady bool
	lastError     string

	sessions            map[[32]byte]colabOneWebSession
	tickets             map[[32]byte]colabOneWebTicket
	loginFailures       map[string][]time.Time
	globalLoginFailures []time.Time

	shareHMACKey           []byte
	workflowShares         map[string]colabOneWorkflowShareRecord
	shareSessions          map[[32]byte]colabOneWorkflowShareSession
	shareOutputOwners      map[string]colabOneShareOutputOwner
	shareTasks             map[string]colabOneShareTrackedTask
	shareInputs            map[string]colabOneShareInputOwner
	sharePreviewOwners     map[string]map[string]time.Time
	shareKeyFailures       map[string][]time.Time
	globalShareKeyFailures []time.Time
	shareUploadSem         chan struct{}

	outputShareHMACKey              []byte
	outputShares                    map[string]colabOneOutputShareRecord
	outputShareSessions             map[[32]byte]colabOneOutputShareSession
	outputSharePasswords            map[string]string
	outputShareAuthFailures         map[string][]time.Time
	globalOutputShareAuthFailures   []time.Time
	outputShareUnlockSem            chan struct{}
	outputShareZipSem               chan struct{}
	hostOutputZipSem                chan struct{}
	outputShareUnlockInflightIP     map[string]int
	outputShareUnlockInflightGlobal int
	outputShareMintAttempts         map[string][]time.Time

	liveWSSem    chan struct{}
	liveWSNextID uint64
	liveWSConns  map[uint64]colabOneLiveWSConn

	handler http.Handler

	// Test-injectable hooks/limits.
	listInterfaceIPs               func() []netip.Addr
	maxCommandBodyBytes            int64
	maxMediaUploadCommandBodyBytes int64
	liveWSHTTPClient               *http.Client
	liveWSAllowLocal               bool
}

var (
	errWebConsoleClosed = errors.New("网页控制台服务已关闭")

	publicFQDNRe = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+(?:[a-z]{2,63}|xn--[a-z0-9-]{2,59})$`)
	dnsHostRe    = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$`)
	outputIDRe   = regexp.MustCompile(`^[a-f0-9]{32}$`)
	sha256HexRe  = regexp.MustCompile(`^[a-f0-9]{64}$`)

	blockedPublicSuffixes = []string{
		".localhost",
		".local",
		".internal",
		".invalid",
		".home.arpa",
	}

	blockedWebCommands = map[string]string{
		"desktop.shortcuts":           "该桌面专属操作仅支持在宿主机客户端执行",
		"api.tunnel_token":            "为保护本机凭据安全，网页控制台禁止读取明文密钥或代理配置",
		"comfyui.canvas.tunnel_token": "为保护本机凭据安全，网页控制台禁止读取明文密钥或代理配置",
		"settings.hf_token":           "为保护本机凭据安全，网页控制台禁止读取明文密钥或代理配置",
		"settings.assign_proxy":       "为保护本机凭据安全，网页控制台禁止读取明文密钥或代理配置",
		"settings.assign_proxy.test":  "为保护本机凭据安全，网页控制台禁止读取明文密钥或代理配置",
		"auth.colab.auto":             "网页控制台不支持本机回环自动登录，请使用手动授权登录",
	}

	mediaUploadWebCommands = map[string]bool{
		"comfyui.input.upload":             true,
		"comfyui.draft.media.save":         true,
		"comfyui.live.avatars.clip.upload": true,
		"comfyui.live.driving.upload":      true,
	}

	allowedOutputMIME = map[string]string{
		"png":  "image/png",
		"jpg":  "image/jpeg",
		"jpeg": "image/jpeg",
		"webp": "image/webp",
		"gif":  "image/gif",
		"mp4":  "video/mp4",
		"webm": "video/webm",
		"wav":  "audio/wav",
		"mp3":  "audio/mpeg",
		"flac": "audio/flac",
	}

	staticMIMEByExt = map[string]string{
		".html":  "text/html; charset=utf-8",
		".htm":   "text/html; charset=utf-8",
		".js":    "application/javascript; charset=utf-8",
		".mjs":   "application/javascript; charset=utf-8",
		".css":   "text/css; charset=utf-8",
		".json":  "application/json; charset=utf-8",
		".svg":   "image/svg+xml",
		".png":   "image/png",
		".jpg":   "image/jpeg",
		".jpeg":  "image/jpeg",
		".webp":  "image/webp",
		".gif":   "image/gif",
		".ico":   "image/x-icon",
		".woff2": "font/woff2",
		".woff":  "font/woff",
		".ttf":   "font/ttf",
		".otf":   "font/otf",
		".wasm":  "application/wasm",
		".txt":   "text/plain; charset=utf-8",
		".map":   "application/json; charset=utf-8",
	}
)

// NewColabOneWebConsole constructs a ColabOneWebConsole, loading persisted
// configuration from <ConfigDir>/web-console.json without starting the HTTP server.
// Call StartIfEnabled() when the host is ready to accept connections.
func NewColabOneWebConsole(opts ColabOneWebConsoleOptions) *ColabOneWebConsole {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	w := &ColabOneWebConsole{
		opts:                    opts,
		cfg:                     defaultWebConsoleConfig(),
		sessions:                make(map[[32]byte]colabOneWebSession),
		tickets:                 make(map[[32]byte]colabOneWebTicket),
		loginFailures:           make(map[string][]time.Time),
		workflowShares:          make(map[string]colabOneWorkflowShareRecord),
		shareSessions:           make(map[[32]byte]colabOneWorkflowShareSession),
		shareOutputOwners:       make(map[string]colabOneShareOutputOwner),
		shareTasks:              make(map[string]colabOneShareTrackedTask),
		shareInputs:             make(map[string]colabOneShareInputOwner),
		sharePreviewOwners:      make(map[string]map[string]time.Time),
		shareKeyFailures:        make(map[string][]time.Time),
		shareUploadSem:          make(chan struct{}, maxWorkflowShareConcurrentUploads),
		outputShares:                make(map[string]colabOneOutputShareRecord),
		outputShareSessions:         make(map[[32]byte]colabOneOutputShareSession),
		outputSharePasswords:        make(map[string]string),
		outputShareAuthFailures:     make(map[string][]time.Time),
		outputShareUnlockSem:        make(chan struct{}, maxOutputShareConcurrentUnlocks),
		outputShareZipSem:           make(chan struct{}, maxOutputShareConcurrentZips),
		hostOutputZipSem:            make(chan struct{}, maxOutputShareConcurrentZips),
		outputShareUnlockInflightIP: make(map[string]int),
		outputShareMintAttempts:     make(map[string][]time.Time),
		liveWSSem:                   make(chan struct{}, colabOneLiveWSMaxConcurrent),
		liveWSConns:                 make(map[uint64]colabOneLiveWSConn),
		listInterfaceIPs:            defaultListInterfaceIPs,
	}
	w.handler = http.HandlerFunc(w.serveHTTP)
	w.loadConfig()
	w.loadWorkflowShares()
	w.loadOutputShares()
	if opts.Vault != nil {
		var stored string
		if found, err := opts.Vault.GetJSON(VaultPasscodeKey, &stored); err == nil && found && strings.TrimSpace(stored) != "" {
			w.passcode = stored
			w.passcodeReady = true
			if w.cfg.Enabled {
				w.loadPersistedSessions(stored)
			}
		}
		w.loadWorkflowShareHMACKey()
		w.loadPersistedWorkflowShareSessions()
		w.loadOutputShareHMACKey()
		w.loadOutputSharePasswords()
		w.loadPersistedOutputShareSessions()
	}
	return w
}

func defaultWebConsoleConfig() ColabOneWebConsoleConfig {
	return ColabOneWebConsoleConfig{
		Enabled:      false,
		BindMode:     BindModeLoopback,
		Port:         DefaultPort,
		PublicOrigin: "",
	}
}

func (w *ColabOneWebConsole) now() time.Time {
	if w.opts.Now != nil {
		return w.opts.Now()
	}
	return time.Now()
}

func (w *ColabOneWebConsole) configPath() string {
	dir := strings.TrimSpace(w.opts.ConfigDir)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, ConfigFilename)
}

func (w *ColabOneWebConsole) loadConfig() {
	cfgPath := w.configPath()
	if cfgPath == "" {
		return
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return
	}
	var loaded ColabOneWebConsoleConfig
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return
	}
	cfg := defaultWebConsoleConfig()
	cfg.Enabled = loaded.Enabled
	if normMode, ok := normalizeBindMode(loaded.BindMode); ok {
		cfg.BindMode = normMode
	}
	if loaded.Port >= MinPort && loaded.Port <= MaxPort {
		cfg.Port = loaded.Port
	}
	if normOrigin, err := normalizePublicOrigin(loaded.PublicOrigin); err == nil {
		cfg.PublicOrigin = normOrigin
	}
	w.cfg = cfg
}

func (w *ColabOneWebConsole) saveConfig(cfg ColabOneWebConsoleConfig) error {
	cfgPath := w.configPath()
	if cfgPath == "" {
		return nil
	}
	dir := filepath.Dir(cfgPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("保存网页控制台配置失败：%w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("保存网页控制台配置失败：%w", err)
	}
	data = append(data, '\n')

	tmpFile, err := os.CreateTemp(dir, ".web-console-*.tmp")
	if err != nil {
		return fmt.Errorf("保存网页控制台配置失败：%w", err)
	}
	tmpPath := tmpFile.Name()
	closed := false
	renamed := false
	defer func() {
		if !closed {
			_ = tmpFile.Close()
		}
		if !renamed {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmpFile.Write(data); err != nil {
		return fmt.Errorf("保存网页控制台配置失败：%w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("保存网页控制台配置失败：%w", err)
	}
	if err := tmpFile.Close(); err != nil {
		closed = true
		return fmt.Errorf("保存网页控制台配置失败：%w", err)
	}
	closed = true
	if err := fsutil.RenameReplace(tmpPath, cfgPath); err != nil {
		return fmt.Errorf("保存网页控制台配置失败：%w", err)
	}
	renamed = true
	return nil
}

func (w *ColabOneWebConsole) logActivity(message, level string) {
	if w.opts.OnLog == nil {
		return
	}
	w.opts.OnLog(message, webConsoleLogSource, level)
}

func (w *ColabOneWebConsole) emitStateChange(state ColabOneWebConsoleState) {
	if w.opts.OnStateChange == nil {
		return
	}
	w.mu.RLock()
	closed := w.closed
	w.mu.RUnlock()
	if closed {
		return
	}
	w.opts.OnStateChange(state)
}

// Handler returns the HTTP handler serving the web console routes.
func (w *ColabOneWebConsole) Handler() http.Handler {
	return w.handler
}

// State returns the live, redacted status snapshot of the web console.
func (w *ColabOneWebConsole) State() ColabOneWebConsoleState {
	w.mu.RLock()
	cfg := w.cfg
	running := w.running
	listenAddr := w.listenAddr
	passcode := w.passcode
	lastErr := w.lastError
	now := w.now()
	activeSessions := 0
	for _, s := range w.sessions {
		if now.Before(s.expiresAt) {
			activeSessions++
		}
	}
	w.mu.RUnlock()

	var lanURLs []string
	if cfg.BindMode == BindModeLAN {
		lanURLs = w.lanURLs(cfg.Port)
	}

	return ColabOneWebConsoleState{
		Enabled:         cfg.Enabled,
		Running:         running,
		BindMode:        cfg.BindMode,
		ListenAddr:      listenAddr,
		Port:            cfg.Port,
		LocalURL:        fmt.Sprintf("http://127.0.0.1:%d", cfg.Port),
		LanURLs:         lanURLs,
		PublicOrigin:    cfg.PublicOrigin,
		HasPasscode:     passcode != "",
		PasscodePreview: maskPasscode(passcode),
		ActiveSessions:  activeSessions,
		LastError:       lastErr,
	}
}

// StartIfEnabled starts the HTTP server when the persisted configuration has Enabled=true.
// Startup bind failures are recorded in LastError rather than crashing the host process.
func (w *ColabOneWebConsole) StartIfEnabled() error {
	state, changed, err := w.startIfEnabledLocked()
	if changed {
		w.emitStateChange(state)
		if err == nil && w.hasWorkflowShares() {
			w.emitWorkflowSharesChange(w.WorkflowSharesState())
		}
		if err == nil && w.hasOutputShares() {
			w.emitOutputSharesChange(w.OutputSharesState())
		}
	}
	return err
}

func (w *ColabOneWebConsole) startIfEnabledLocked() (ColabOneWebConsoleState, bool, error) {
	w.opMu.Lock()
	defer w.opMu.Unlock()

	w.mu.RLock()
	closed := w.closed
	cfg := w.cfg
	running := w.running
	w.mu.RUnlock()

	if closed || !cfg.Enabled || running {
		return w.State(), false, nil
	}

	if _, _, err := w.ensurePasscodeOpLocked(); err != nil {
		w.mu.Lock()
		w.lastError = err.Error()
		w.mu.Unlock()
		w.logActivity(fmt.Sprintf("网页控制台启动失败：%s", err.Error()), "error")
		return w.State(), true, err
	}
	w.mu.RLock()
	needShareSessionLoad := len(w.shareSessions) == 0 && len(w.workflowShares) > 0
	needOutputShareLoad := len(w.outputShareSessions) == 0 && len(w.outputShares) > 0
	w.mu.RUnlock()
	if needShareSessionLoad {
		w.loadWorkflowShareHMACKey()
		w.loadPersistedWorkflowShareSessions()
	}
	if needOutputShareLoad {
		w.loadOutputShareHMACKey()
		w.loadOutputSharePasswords()
		w.loadPersistedOutputShareSessions()
	}

	listeners, addr, err := listenTCP(cfg.BindMode, cfg.Port)
	if err != nil {
		w.mu.Lock()
		w.lastError = err.Error()
		w.running = false
		w.listenAddr = ""
		w.mu.Unlock()
		w.logActivity(fmt.Sprintf("网页控制台启动失败：%s", err.Error()), "error")
		return w.State(), true, err
	}

	srv := w.newHTTPServer()
	done := make(chan struct{})
	w.mu.Lock()
	w.srv = srv
	w.listeners = listeners
	w.srvDone = done
	w.running = true
	w.listenAddr = addr
	w.lastError = ""
	w.mu.Unlock()
	w.serveInBackground(srv, listeners, done)

	w.logActivity(fmt.Sprintf("网页控制台已启动，监听 %s", addr), "info")
	return w.State(), true, nil
}

// Update validates, persists, and hot-applies web console configuration changes.
func (w *ColabOneWebConsole) Update(req ColabOneWebConsoleUpdateRequest) (ColabOneWebConsoleState, error) {
	state, changed, err := w.updateOpLocked(req)
	if changed {
		w.emitStateChange(state)
		if err == nil && w.hasWorkflowShares() {
			w.emitWorkflowSharesChange(w.WorkflowSharesState())
		}
		if err == nil && w.hasOutputShares() {
			w.emitOutputSharesChange(w.OutputSharesState())
		}
	}
	return state, err
}

func (w *ColabOneWebConsole) updateOpLocked(req ColabOneWebConsoleUpdateRequest) (ColabOneWebConsoleState, bool, error) {
	w.opMu.Lock()
	defer w.opMu.Unlock()

	w.mu.RLock()
	closed := w.closed
	oldCfg := w.cfg
	wasRunning := w.running
	oldSrv := w.srv
	oldListeners := w.listeners
	oldDone := w.srvDone
	w.mu.RUnlock()
	if closed {
		return w.State(), false, errors.New("网页控制台服务已关闭")
	}

	nextCfg := oldCfg
	if req.Enabled != nil {
		nextCfg.Enabled = *req.Enabled
	}
	if req.BindMode != nil {
		normMode, ok := normalizeBindMode(*req.BindMode)
		if !ok {
			return w.State(), false, errors.New("不支持的监听范围设置")
		}
		nextCfg.BindMode = normMode
	}
	if req.Port != nil {
		if *req.Port < MinPort || *req.Port > MaxPort {
			return w.State(), false, fmt.Errorf("网页控制台端口必须在 %d 至 %d 之间", MinPort, MaxPort)
		}
		nextCfg.Port = *req.Port
	}
	if req.PublicOrigin != nil {
		normOrigin, err := normalizePublicOrigin(*req.PublicOrigin)
		if err != nil {
			return w.State(), false, err
		}
		nextCfg.PublicOrigin = normOrigin
	}

	// Case 1: Turning enabled off (or staying disabled).
	if !nextCfg.Enabled {
		if err := w.saveConfig(nextCfg); err != nil {
			return w.State(), false, err
		}
		if wasRunning {
			w.mu.Lock()
			w.stopping = true
			w.mu.Unlock()
			w.cancelAllLiveWSConns(1001)
			shutdownHTTPServer(oldSrv, oldListeners, oldDone)
		}
		w.mu.Lock()
		w.cfg = nextCfg
		w.running = false
		w.stopping = false
		w.listenAddr = ""
		w.srv = nil
		w.listeners = nil
		w.srvDone = nil
		w.lastError = ""
		clearSessions := wasRunning || oldCfg.Enabled
		if clearSessions {
			clear(w.sessions)
			clear(w.tickets)
		}
		w.mu.Unlock()
		if clearSessions {
			w.cancelAllLiveWSConns(4401)
			w.persistSessions()
		}
		if wasRunning {
			w.logActivity("已关闭网页控制台服务", "info")
		}
		return w.State(), true, nil
	}

	// nextCfg.Enabled == true: ensure a passcode exists before serving traffic.
	if _, _, err := w.ensurePasscodeOpLocked(); err != nil {
		return w.State(), false, err
	}
	w.mu.RLock()
	needShareSessionLoad := len(w.shareSessions) == 0 && len(w.workflowShares) > 0
	needOutputShareLoad := len(w.outputShareSessions) == 0 && len(w.outputShares) > 0
	w.mu.RUnlock()
	if needShareSessionLoad {
		w.loadWorkflowShareHMACKey()
		w.loadPersistedWorkflowShareSessions()
	}
	if needOutputShareLoad {
		w.loadOutputShareHMACKey()
		w.loadOutputSharePasswords()
		w.loadPersistedOutputShareSessions()
	}

	// Case 2: Turning on from stopped state.
	if !wasRunning {
		listeners, addr, err := listenTCP(nextCfg.BindMode, nextCfg.Port)
		if err != nil {
			w.mu.Lock()
			w.lastError = err.Error()
			w.mu.Unlock()
			w.logActivity(fmt.Sprintf("网页控制台启动失败：%s", err.Error()), "error")
			return w.State(), true, err
		}
		if err := w.saveConfig(nextCfg); err != nil {
			closeListeners(listeners)
			return w.State(), false, err
		}
		srv := w.newHTTPServer()
		done := make(chan struct{})
		w.mu.Lock()
		w.cfg = nextCfg
		w.srv = srv
		w.listeners = listeners
		w.srvDone = done
		w.running = true
		w.listenAddr = addr
		w.lastError = ""
		w.mu.Unlock()
		w.serveInBackground(srv, listeners, done)
		w.logActivity(fmt.Sprintf("网页控制台已启动，监听 %s", addr), "info")
		return w.State(), true, nil
	}

	// Case 3: Already running and only PublicOrigin (or nothing) changed.
	if nextCfg.BindMode == oldCfg.BindMode && nextCfg.Port == oldCfg.Port {
		if err := w.saveConfig(nextCfg); err != nil {
			return w.State(), false, err
		}
		w.mu.Lock()
		w.cfg = nextCfg
		w.lastError = ""
		w.mu.Unlock()
		if nextCfg.PublicOrigin != oldCfg.PublicOrigin {
			w.logActivity("已更新网页控制台公网域名配置", "info")
		}
		return w.State(), true, nil
	}

	// Case 4: Already running and port changed — bind the new port first so the
	// old listener stays alive if the new port is unavailable.
	if nextCfg.Port != oldCfg.Port {
		newListeners, newAddr, err := listenTCP(nextCfg.BindMode, nextCfg.Port)
		if err != nil {
			// Keep the existing healthy listener running without poisoning LastError.
			w.logActivity(fmt.Sprintf("切换网页控制台端口失败：%s", err.Error()), "error")
			return w.State(), false, err
		}
		if err := w.saveConfig(nextCfg); err != nil {
			closeListeners(newListeners)
			return w.State(), false, err
		}
		newSrv := w.newHTTPServer()
		newDone := make(chan struct{})
		w.mu.Lock()
		w.cfg = nextCfg
		w.srv = newSrv
		w.listeners = newListeners
		w.srvDone = newDone
		w.running = true
		w.listenAddr = newAddr
		w.lastError = ""
		w.mu.Unlock()
		w.serveInBackground(newSrv, newListeners, newDone)
		shutdownHTTPServer(oldSrv, oldListeners, oldDone)
		w.logActivity(fmt.Sprintf("网页控制台已切换监听至 %s", newAddr), "info")
		return w.State(), true, nil
	}

	// Case 5: Already running and only BindMode changed on the same port — stop
	// old listener first, bind new listener, and restore old listener on failure.
	shutdownHTTPServer(oldSrv, oldListeners, oldDone)
	newListeners, newAddr, err := listenTCP(nextCfg.BindMode, nextCfg.Port)
	if err != nil {
		restoredListeners, restoredAddr, restoreErr := listenTCP(oldCfg.BindMode, oldCfg.Port)
		if restoreErr == nil {
			restoredSrv := w.newHTTPServer()
			restoredDone := make(chan struct{})
			w.mu.Lock()
			w.cfg = oldCfg
			w.srv = restoredSrv
			w.listeners = restoredListeners
			w.srvDone = restoredDone
			w.running = true
			w.listenAddr = restoredAddr
			w.lastError = ""
			w.mu.Unlock()
			w.serveInBackground(restoredSrv, restoredListeners, restoredDone)
			w.logActivity(fmt.Sprintf("切换网页控制台监听模式失败：%s", err.Error()), "error")
			return w.State(), false, err
		}
		w.mu.Lock()
		w.cfg = oldCfg
		w.srv = nil
		w.listeners = nil
		w.srvDone = nil
		w.running = false
		w.listenAddr = ""
		w.lastError = err.Error()
		w.mu.Unlock()
		w.logActivity(fmt.Sprintf("切换网页控制台监听模式失败：%s", err.Error()), "error")
		return w.State(), true, err
	}

	if err := w.saveConfig(nextCfg); err != nil {
		closeListeners(newListeners)
		restoredListeners, restoredAddr, restoreErr := listenTCP(oldCfg.BindMode, oldCfg.Port)
		if restoreErr == nil {
			restoredSrv := w.newHTTPServer()
			restoredDone := make(chan struct{})
			w.mu.Lock()
			w.cfg = oldCfg
			w.srv = restoredSrv
			w.listeners = restoredListeners
			w.srvDone = restoredDone
			w.running = true
			w.listenAddr = restoredAddr
			w.lastError = ""
			w.mu.Unlock()
			w.serveInBackground(restoredSrv, restoredListeners, restoredDone)
			return w.State(), false, err
		}
		w.mu.Lock()
		w.cfg = oldCfg
		w.srv = nil
		w.listeners = nil
		w.srvDone = nil
		w.running = false
		w.listenAddr = ""
		w.lastError = restoreErr.Error()
		w.mu.Unlock()
		return w.State(), true, err
	}

	newSrv := w.newHTTPServer()
	newDone := make(chan struct{})
	w.mu.Lock()
	w.cfg = nextCfg
	w.srv = newSrv
	w.listeners = newListeners
	w.srvDone = newDone
	w.running = true
	w.listenAddr = newAddr
	w.lastError = ""
	w.mu.Unlock()
	w.serveInBackground(newSrv, newListeners, newDone)
	w.logActivity(fmt.Sprintf("网页控制台已切换监听至 %s", newAddr), "info")
	return w.State(), true, nil
}

// Passcode returns the raw host access passcode from the vault, generating and
// persisting a fresh high-entropy passcode if none exists yet.
func (w *ColabOneWebConsole) Passcode() (string, error) {
	w.opMu.Lock()
	passcode, generated, err := w.ensurePasscodeOpLocked()
	var state ColabOneWebConsoleState
	if generated && err == nil {
		state = w.State()
	}
	w.opMu.Unlock()

	if generated && err == nil {
		w.emitStateChange(state)
	}
	return passcode, err
}

func (w *ColabOneWebConsole) ensurePasscodeOpLocked() (string, bool, error) {
	w.mu.RLock()
	cached := w.passcode
	ready := w.passcodeReady
	w.mu.RUnlock()

	if ready && cached != "" {
		return cached, false, nil
	}

	if w.opts.Vault != nil {
		var stored string
		found, err := w.opts.Vault.GetJSON(VaultPasscodeKey, &stored)
		if err != nil {
			return "", false, fmt.Errorf("读取网页控制台访问口令失败：%w", err)
		}
		if found && strings.TrimSpace(stored) != "" {
			w.mu.Lock()
			w.passcode = stored
			w.passcodeReady = true
			shouldLoadSessions := w.cfg.Enabled && len(w.sessions) == 0 && !w.closed
			w.mu.Unlock()
			if shouldLoadSessions {
				w.loadPersistedSessions(stored)
			}
			return stored, false, nil
		}
	}

	generated, err := generatePasscode()
	if err != nil {
		return "", false, err
	}
	if w.opts.Vault != nil {
		if err := w.opts.Vault.Set(VaultPasscodeKey, generated); err != nil {
			return "", false, fmt.Errorf("保存网页控制台访问口令失败：%w", err)
		}
	}
	w.mu.Lock()
	w.passcode = generated
	w.passcodeReady = true
	w.mu.Unlock()
	return generated, true, nil
}

// SetPasscode updates the host access passcode (or generates a fresh random one
// when trimmed raw is empty), revoking all active sessions and pending tickets.
func (w *ColabOneWebConsole) SetPasscode(raw string) (ColabOneWebConsoleState, error) {
	state, changed, err := w.setPasscodeOpLocked(raw)
	if changed {
		w.emitStateChange(state)
	}
	return state, err
}

func (w *ColabOneWebConsole) setPasscodeOpLocked(raw string) (ColabOneWebConsoleState, bool, error) {
	w.opMu.Lock()
	defer w.opMu.Unlock()

	w.mu.RLock()
	closed := w.closed
	w.mu.RUnlock()
	if closed {
		return w.State(), false, errors.New("网页控制台服务已关闭")
	}

	var nextPasscode string
	var regenerated bool
	if strings.TrimSpace(raw) == "" {
		gen, err := generatePasscode()
		if err != nil {
			return w.State(), false, err
		}
		nextPasscode = gen
		regenerated = true
	} else {
		if err := validateCustomPasscode(raw); err != nil {
			return w.State(), false, err
		}
		nextPasscode = raw
	}

	if w.opts.Vault != nil {
		if err := w.opts.Vault.Set(VaultPasscodeKey, nextPasscode); err != nil {
			return w.State(), false, fmt.Errorf("保存网页控制台访问口令失败：%w", err)
		}
	}

	w.mu.Lock()
	w.passcode = nextPasscode
	w.passcodeReady = true
	clear(w.sessions)
	clear(w.tickets)
	w.mu.Unlock()
	w.cancelAllLiveWSConns(4401)
	w.persistSessions()

	if regenerated {
		w.logActivity("已重置网页控制台随机访问口令并注销全部远程会话", "info")
	} else {
		w.logActivity("已更新网页控制台访问口令并注销旧会话", "info")
	}
	return w.State(), true, nil
}

// CreateLoginTicket creates a short-lived browser unlock ticket URL.
// When singleUse is false (default), the link can be redeemed repeatedly within
// LoginTicketTTL so messaging link-preview crawlers or multi-device taps do not
// prematurely invalidate it. When singleUse is true, the first non-crawler
// redemption consumes the ticket immediately.
func (w *ColabOneWebConsole) CreateLoginTicket(singleUse bool) (ColabOneWebConsoleTicket, error) {
	rawToken, err := generateHexToken(32)
	if err != nil {
		return ColabOneWebConsoleTicket{}, err
	}
	digest := sha256.Sum256([]byte(rawToken))

	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return ColabOneWebConsoleTicket{}, errors.New("网页控制台服务已关闭")
	}
	if !w.running {
		w.mu.Unlock()
		return ColabOneWebConsoleTicket{}, errors.New("请先开启网页控制台后再生成登录链接")
	}
	now := w.now()
	w.pruneExpiredTicketsLocked(now)
	for len(w.tickets) >= maxPendingTickets {
		evictOldestTicketLocked(w.tickets)
	}
	expiresAt := now.Add(LoginTicketTTL)
	w.tickets[digest] = colabOneWebTicket{
		createdAt: now,
		expiresAt: expiresAt,
		singleUse: singleUse,
	}
	port := w.cfg.Port
	publicOrigin := w.cfg.PublicOrigin
	w.mu.Unlock()

	localURL := fmt.Sprintf("http://127.0.0.1:%d/_colabone_web/redeem?ticket=%s", port, rawToken)
	ticketURL := localURL
	usesPublic := false
	if publicOrigin != "" {
		ticketURL = fmt.Sprintf("%s/_colabone_web/redeem?ticket=%s", publicOrigin, rawToken)
		usesPublic = true
	}

	return ColabOneWebConsoleTicket{
		URL:            ticketURL,
		LocalURL:       localURL,
		ExpiresAt:      expiresAt.Unix(),
		TTLSeconds:     int(LoginTicketTTL.Seconds()),
		UsesPublicHost: usesPublic,
		SingleUse:      singleUse,
	}, nil
}

// RevokeSessions clears all active browser sessions and pending login tickets,
// including persisted sessions in the credential vault.
func (w *ColabOneWebConsole) RevokeSessions() ColabOneWebConsoleState {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return w.State()
	}
	clear(w.sessions)
	clear(w.tickets)
	w.mu.Unlock()
	w.cancelAllLiveWSConns(4401)
	w.persistSessions()

	w.logActivity("已注销全部网页控制台在线会话", "info")
	state := w.State()
	w.emitStateChange(state)
	return state
}

// Close gracefully stops the HTTP server and clears in-memory state without
// modifying persisted configuration or persisted browser sessions, so signed-in
// browsers remain authenticated across desktop app restarts.
func (w *ColabOneWebConsole) Close() {
	w.opMu.Lock()
	// Hold persistMu, sharePersistMu, and outputSharePersistMu while marking closed so any
	// straggling handler cannot overwrite Vault or share files or emit UI callbacks while Close() waits on shutdown.
	w.persistMu.Lock()
	w.sharePersistMu.Lock()
	w.outputSharePersistMu.Lock()
	w.mu.Lock()
	w.closed = true
	srv := w.srv
	listeners := w.listeners
	done := w.srvDone
	w.srv = nil
	w.listeners = nil
	w.srvDone = nil
	w.running = false
	w.listenAddr = ""
	w.mu.Unlock()
	w.outputSharePersistMu.Unlock()
	w.sharePersistMu.Unlock()
	w.persistMu.Unlock()

	w.cancelAllLiveWSConns(1001)
	shutdownHTTPServer(srv, listeners, done)

	w.persistMu.Lock()
	w.sharePersistMu.Lock()
	w.outputSharePersistMu.Lock()
	w.mu.Lock()
	clear(w.sessions)
	clear(w.tickets)
	clear(w.shareSessions)
	clear(w.shareInputs)
	clear(w.sharePreviewOwners)
	clear(w.outputShareSessions)
	clear(w.outputSharePasswords)
	w.mu.Unlock()
	w.outputSharePersistMu.Unlock()
	w.sharePersistMu.Unlock()
	w.persistMu.Unlock()
	w.opMu.Unlock()
}

func (w *ColabOneWebConsole) newHTTPServer() *http.Server {
	return &http.Server{
		Handler:           w.handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
}

func (w *ColabOneWebConsole) serveInBackground(srv *http.Server, listeners []net.Listener, done chan struct{}) {
	go func() {
		doneClosed := false
		closeDone := func() {
			if !doneClosed {
				doneClosed = true
				close(done)
			}
		}
		defer closeDone()

		var wg sync.WaitGroup
		errCh := make(chan error, len(listeners))
		for idx, ln := range listeners {
			if ln == nil {
				continue
			}
			isPrimary := idx == 0
			wg.Add(1)
			go func(l net.Listener, primary bool) {
				defer wg.Done()
				err := srv.Serve(l)
				if err == nil || errors.Is(err, http.ErrServerClosed) {
					return
				}
				if primary {
					errCh <- err
				}
			}(ln, isPrimary)
		}
		wg.Wait()
		close(errCh)
		var primaryErr error
		for err := range errCh {
			if err != nil {
				primaryErr = err
				break
			}
		}
		if primaryErr == nil {
			return
		}
		w.mu.Lock()
		shouldNotify := !w.closed && w.srv == srv
		if shouldNotify {
			w.running = false
			w.listenAddr = ""
			w.srv = nil
			w.listeners = nil
			w.srvDone = nil
			w.lastError = primaryErr.Error()
		}
		w.mu.Unlock()

		// Always signal server goroutine completion before invoking UI/log callbacks
		// so Close() on the main UI thread can never deadlock waiting on <-done.
		closeDone()
		if shouldNotify {
			w.logActivity(fmt.Sprintf("网页控制台服务异常退出：%s", primaryErr.Error()), "error")
			w.emitStateChange(w.State())
		}
	}()
}

func closeListeners(listeners []net.Listener) {
	for _, ln := range listeners {
		if ln != nil {
			_ = ln.Close()
		}
	}
}

func shutdownHTTPServer(srv *http.Server, listeners []net.Listener, done <-chan struct{}) {
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), serverShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			_ = srv.Close()
		}
	}
	closeListeners(listeners)
	if done != nil {
		<-done
	}
}

func listenTCP(bindMode string, port int) ([]net.Listener, string, error) {
	portStr := strconv.Itoa(port)
	if bindMode == BindModeLAN {
		displayAddr := net.JoinHostPort("0.0.0.0", portStr)
		// Prefer dual-stack wildcard bind (serves both IPv4 and IPv6 on modern OSes).
		ln, err := net.Listen("tcp", ":"+portStr)
		if err != nil {
			if isAddrInUseErr(err) {
				return nil, "", errors.New("端口已被占用，请更换其他端口")
			}
			// Fallback to IPv4-only on hosts without IPv6 support.
			ln4, err4 := net.Listen("tcp4", displayAddr)
			if err4 != nil {
				if isAddrInUseErr(err4) {
					return nil, "", errors.New("端口已被占用，请更换其他端口")
				}
				return nil, "", fmt.Errorf("监听 %s 失败：%w", displayAddr, err4)
			}
			return []net.Listener{ln4}, displayAddr, nil
		}
		return []net.Listener{ln}, displayAddr, nil
	}

	displayAddr := net.JoinHostPort("127.0.0.1", portStr)
	ln4, err := net.Listen("tcp4", displayAddr)
	if err != nil {
		if isAddrInUseErr(err) {
			return nil, "", errors.New("端口已被占用，请更换其他端口")
		}
		return nil, "", fmt.Errorf("监听 %s 失败：%w", displayAddr, err)
	}
	listeners := []net.Listener{ln4}
	// Best-effort secondary IPv6 loopback listener so http://localhost:<port>
	// and cloudflared targets resolving localhost to [::1] work seamlessly.
	if ln6, err6 := net.Listen("tcp6", net.JoinHostPort("::1", portStr)); err6 == nil {
		listeners = append(listeners, ln6)
	}
	return listeners, displayAddr, nil
}

func isAddrInUseErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "only one usage of each socket address")
}

func normalizeBindMode(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case BindModeLoopback:
		return BindModeLoopback, true
	case BindModeLAN:
		return BindModeLAN, true
	default:
		return "", false
	}
}

const (
	errInvalidPublicOriginMsg = "公网域名格式无效，请填写公网 HTTPS 固定域名（例如 https://colab.example.com）"
	errInvalidPasscodeMsg     = "访问口令长度需在 8 至 128 个字符之间，且不能包含控制字符"
)

func normalizePublicOrigin(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	if utf8.RuneCountInString(trimmed) > maxPublicOriginRunes {
		return "", errors.New(errInvalidPublicOriginMsg)
	}
	if strings.ContainsAny(trimmed, "%?# \t\r\n") {
		return "", errors.New(errInvalidPublicOriginMsg)
	}

	candidate := trimmed
	if idx := strings.Index(candidate, "://"); idx >= 0 {
		scheme := strings.ToLower(candidate[:idx])
		if scheme != "https" {
			return "", errors.New(errInvalidPublicOriginMsg)
		}
		candidate = "https://" + candidate[idx+3:]
	} else {
		candidate = "https://" + candidate
	}

	// Permit a single trailing slash from browser copy/paste while rejecting any real path.
	if strings.HasSuffix(candidate, "/") && !strings.HasSuffix(candidate, "//") {
		candidate = strings.TrimSuffix(candidate, "/")
	}

	parsed, err := url.Parse(candidate)
	if err != nil {
		return "", errors.New(errInvalidPublicOriginMsg)
	}
	if parsed.Scheme != "https" ||
		parsed.Opaque != "" ||
		parsed.User != nil ||
		parsed.Port() != "" ||
		strings.Contains(parsed.Host, ":") ||
		parsed.Path != "" ||
		parsed.RawPath != "" ||
		parsed.RawQuery != "" ||
		parsed.ForceQuery ||
		parsed.Fragment != "" ||
		parsed.RawFragment != "" {
		return "", errors.New(errInvalidPublicOriginMsg)
	}

	host := strings.ToLower(parsed.Hostname())
	if host == "" || strings.ToLower(parsed.Host) != host || len(host) > 253 {
		return "", errors.New(errInvalidPublicOriginMsg)
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return "", errors.New(errInvalidPublicOriginMsg)
	}
	if host == "localhost" || hasBlockedPublicSuffix(host) || !publicFQDNRe.MatchString(host) {
		return "", errors.New(errInvalidPublicOriginMsg)
	}
	return "https://" + host, nil
}

func hasBlockedPublicSuffix(host string) bool {
	for _, suffix := range blockedPublicSuffixes {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

func validateCustomPasscode(raw string) error {
	if !utf8.ValidString(raw) {
		return errors.New(errInvalidPasscodeMsg)
	}
	if strings.TrimSpace(raw) != raw {
		return errors.New("访问口令首尾不能包含空白字符")
	}
	count := utf8.RuneCountInString(raw)
	if count < passcodeMinRunes || count > passcodeMaxRunes {
		return errors.New(errInvalidPasscodeMsg)
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return errors.New(errInvalidPasscodeMsg)
		}
	}
	return nil
}

func generatePasscode() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成随机访问口令失败：%w", err)
	}
	return "co_web_" + base64.RawURLEncoding.EncodeToString(buf), nil
}

func generateHexToken(numBytes int) (string, error) {
	buf := make([]byte, numBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成随机令牌失败：%w", err)
	}
	return hex.EncodeToString(buf), nil
}

func generateBase64Token(numBytes int) (string, error) {
	buf := make([]byte, numBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成随机令牌失败：%w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func maskPasscode(passcode string) string {
	if passcode == "" {
		return ""
	}
	runes := []rune(passcode)
	if len(runes) >= 10 {
		return string(runes[:4]) + "••••" + string(runes[len(runes)-4:])
	}
	return "••••••••"
}

func defaultListInterfaceIPs() []netip.Addr {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var addrs []netip.Addr
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		ifAddrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range ifAddrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil {
				continue
			}
			addr, ok := netip.AddrFromSlice(ip)
			if !ok {
				continue
			}
			addr = addr.Unmap()
			if addr.IsLoopback() || addr.IsUnspecified() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() {
				continue
			}
			addrs = append(addrs, addr)
		}
	}
	return addrs
}

func (w *ColabOneWebConsole) lanURLs(port int) []string {
	listFn := w.listInterfaceIPs
	if listFn == nil {
		listFn = defaultListInterfaceIPs
	}
	raw := listFn()
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[netip.Addr]struct{}, len(raw))
	var v4 []netip.Addr
	for _, addr := range raw {
		addr = addr.Unmap()
		if !addr.Is4() || addr.IsLoopback() || addr.IsUnspecified() || addr.IsLinkLocalUnicast() || addr.IsMulticast() {
			continue
		}
		if _, dup := seen[addr]; dup {
			continue
		}
		seen[addr] = struct{}{}
		v4 = append(v4, addr)
	}
	if len(v4) == 0 {
		return nil
	}
	sort.Slice(v4, func(i, j int) bool {
		pi := v4[i].IsPrivate()
		pj := v4[j].IsPrivate()
		if pi != pj {
			return pi
		}
		return v4[i].String() < v4[j].String()
	})
	urls := make([]string, 0, len(v4))
	for _, addr := range v4 {
		urls = append(urls, fmt.Sprintf("http://%s:%d", addr.String(), port))
	}
	return urls
}

// --- HTTP handling ---

func (w *ColabOneWebConsole) serveHTTP(rw http.ResponseWriter, r *http.Request) {
	setBaseSecurityHeaders(rw)

	host, _, ok := parseRequestHost(r.Host)
	if !ok {
		http.Error(rw, "Bad Request", http.StatusBadRequest)
		return
	}
	if !w.isHostAllowed(host) {
		http.Error(rw, "Forbidden", http.StatusForbidden)
		return
	}

	reqPath := r.URL.Path
	switch {
	case reqPath == "/favicon.ico":
		serveBrandIconAsset(rw, r, "image/x-icon", brandicon.FaviconICOBytes())
		return

	case reqPath == "/apple-touch-icon.png" || reqPath == "/apple-touch-icon-precomposed.png":
		serveBrandIconAsset(rw, r, "image/png", brandicon.AppleTouchIconPNGBytes())
		return

	case reqPath == "/_colabone_web/icon.svg":
		serveBrandIconAsset(rw, r, "image/svg+xml", brandicon.SVGBytes())
		return

	case reqPath == "/_colabone_web/login":
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			errCode := r.URL.Query().Get("err")
			if errCode == "" && w.authenticateRequest(rw, r) {
				rw.Header().Set("Cache-Control", "no-store")
				http.Redirect(rw, r, "/", http.StatusFound)
				return
			}
			w.handleLoginPage(rw, r, http.StatusOK, errCode)
		case http.MethodPost:
			w.handleLoginSubmit(rw, r)
		default:
			writeMethodNotAllowed(rw, "GET, HEAD, POST")
		}
		return

	case reqPath == "/_colabone_web/redeem":
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(rw, "GET")
			return
		}
		w.handleRedeem(rw, r)
		return

	case reqPath == "/_colabone_web/logout":
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(rw, "POST")
			return
		}
		w.handleLogout(rw, r)
		return

	case strings.HasPrefix(reqPath, "/_colabone_web/"):
		http.NotFound(rw, r)
		return

	case reqPath == workflowShareSwitchRoutePath:
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(rw, "POST")
			return
		}
		w.handleWorkflowShareSwitch(rw, r)
		return

	case reqPath == WorkflowShareRoutePath || strings.HasPrefix(reqPath, WorkflowShareRoutePath+"/"):
		w.handleWorkflowShareEntry(rw, r)
		return

	case reqPath == "/api/share/capabilities":
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(rw, "GET")
			return
		}
		shareID, ok := w.authenticateShareRequest(rw, r)
		if !ok {
			writeJSONError(rw, http.StatusUnauthorized, "未登录或分享会话已失效")
			return
		}
		w.handleShareCapabilities(rw, shareID)
		return

	case reqPath == "/api/share/snapshot":
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(rw, "GET")
			return
		}
		shareID, ok := w.authenticateShareRequest(rw, r)
		if !ok {
			writeJSONError(rw, http.StatusUnauthorized, "未登录或分享会话已失效")
			return
		}
		w.handleShareSnapshot(rw, r, shareID)
		return

	case reqPath == "/api/share/command":
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(rw, "POST")
			return
		}
		shareID, ok := w.authenticateShareRequest(rw, r)
		if !ok {
			writeJSONError(rw, http.StatusUnauthorized, "未登录或分享会话已失效")
			return
		}
		if !w.validateSameOriginShareRequest(r) {
			writeJSONError(rw, http.StatusForbidden, "跨源请求已被拒绝")
			return
		}
		w.handleShareCommand(rw, r, shareID)
		return

	case strings.HasPrefix(reqPath, "/api/share/outputs/") && strings.HasSuffix(reqPath, "/download"):
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeMethodNotAllowed(rw, "GET, HEAD")
			return
		}
		shareID, ok := w.authenticateShareRequest(rw, r)
		if !ok {
			writeHeadAwareJSONError(rw, r, http.StatusUnauthorized, "未登录或分享会话已失效")
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(reqPath, "/api/share/outputs/"), "/download")
		w.handleShareOutputDownload(rw, r, shareID, id)
		return

	case strings.HasPrefix(reqPath, "/api/share/outputs/") && strings.HasSuffix(reqPath, "/media"):
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeMethodNotAllowed(rw, "GET, HEAD")
			return
		}
		shareID, ok := w.authenticateShareRequest(rw, r)
		if !ok {
			writeHeadAwareJSONError(rw, r, http.StatusUnauthorized, "未登录或分享会话已失效")
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(reqPath, "/api/share/outputs/"), "/media")
		w.handleShareOutputMedia(rw, r, shareID, id)
		return

	case reqPath == "/api/share" || strings.HasPrefix(reqPath, "/api/share/"):
		if _, ok := w.authenticateShareRequest(rw, r); !ok {
			writeJSONError(rw, http.StatusUnauthorized, "未登录或分享会话已失效")
			return
		}
		writeJSONError(rw, http.StatusNotFound, "接口不存在")
		return

	case reqPath == OutputShareUnlockRoutePath:
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(rw, "POST")
			return
		}
		w.handleOutputShareUnlock(rw, r)
		return

	case reqPath == OutputShareRoutePath || strings.HasPrefix(reqPath, OutputShareRoutePath+"/"):
		w.handleOutputShareEntry(rw, r)
		return

	case reqPath == "/api/gallery/manifest":
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(rw, "GET")
			return
		}
		shareID, ok := w.authenticateOutputShareRequest(rw, r)
		if !ok {
			writeJSONError(rw, http.StatusUnauthorized, "未登录或分享会话已失效")
			return
		}
		w.handleOutputShareManifest(rw, r, shareID)
		return

	case reqPath == "/api/gallery/download.zip":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeMethodNotAllowed(rw, "GET, HEAD")
			return
		}
		shareID, ok := w.authenticateOutputShareRequest(rw, r)
		if !ok {
			writeHeadAwareJSONError(rw, r, http.StatusUnauthorized, "未登录或分享会话已失效")
			return
		}
		w.handleOutputShareZipDownload(rw, r, shareID)
		return

	case strings.HasPrefix(reqPath, "/api/gallery/outputs/") && strings.HasSuffix(reqPath, "/download"):
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeMethodNotAllowed(rw, "GET, HEAD")
			return
		}
		shareID, ok := w.authenticateOutputShareRequest(rw, r)
		if !ok {
			writeHeadAwareJSONError(rw, r, http.StatusUnauthorized, "未登录或分享会话已失效")
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(reqPath, "/api/gallery/outputs/"), "/download")
		w.handleOutputShareDownload(rw, r, shareID, id)
		return

	case strings.HasPrefix(reqPath, "/api/gallery/outputs/") && strings.HasSuffix(reqPath, "/media"):
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeMethodNotAllowed(rw, "GET, HEAD")
			return
		}
		shareID, ok := w.authenticateOutputShareRequest(rw, r)
		if !ok {
			writeHeadAwareJSONError(rw, r, http.StatusUnauthorized, "未登录或分享会话已失效")
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(reqPath, "/api/gallery/outputs/"), "/media")
		w.handleOutputShareMedia(rw, r, shareID, id)
		return

	case reqPath == "/api/gallery" || strings.HasPrefix(reqPath, "/api/gallery/"):
		if _, ok := w.authenticateOutputShareRequest(rw, r); !ok {
			writeJSONError(rw, http.StatusUnauthorized, "未登录或分享会话已失效")
			return
		}
		writeJSONError(rw, http.StatusNotFound, "接口不存在")
		return

	case reqPath == "/api/output-shares":
		switch r.Method {
		case http.MethodGet:
			if !w.authenticateRequest(rw, r) {
				writeJSONError(rw, http.StatusUnauthorized, "未登录或会话已过期")
				return
			}
			if !isHostWebClientHeader(r) {
				writeJSONError(rw, http.StatusForbidden, "跨源请求已被拒绝")
				return
			}
			w.handleHostOutputSharesList(rw)
		case http.MethodPost:
			if !w.authenticateRequest(rw, r) {
				writeJSONError(rw, http.StatusUnauthorized, "未登录或会话已过期")
				return
			}
			if !w.validateHostWebSameOrigin(r) {
				writeJSONError(rw, http.StatusForbidden, "跨源请求已被拒绝")
				return
			}
			w.handleHostOutputShareCreate(rw, r)
		default:
			writeMethodNotAllowed(rw, "GET, POST")
		}
		return

	case strings.HasPrefix(reqPath, "/api/output-shares/") && strings.HasSuffix(reqPath, "/revoke"):
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(rw, "POST")
			return
		}
		if !w.authenticateRequest(rw, r) {
			writeJSONError(rw, http.StatusUnauthorized, "未登录或会话已过期")
			return
		}
		if !w.validateHostWebSameOrigin(r) {
			writeJSONError(rw, http.StatusForbidden, "跨源请求已被拒绝")
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(reqPath, "/api/output-shares/"), "/revoke")
		w.handleHostOutputShareRevoke(rw, r, id)
		return

	case strings.HasPrefix(reqPath, "/api/output-shares/") && !strings.Contains(strings.TrimPrefix(reqPath, "/api/output-shares/"), "/"):
		if r.Method != http.MethodDelete {
			writeMethodNotAllowed(rw, "DELETE")
			return
		}
		if !w.authenticateRequest(rw, r) {
			writeJSONError(rw, http.StatusUnauthorized, "未登录或会话已过期")
			return
		}
		if !w.validateHostWebSameOrigin(r) {
			writeJSONError(rw, http.StatusForbidden, "跨源请求已被拒绝")
			return
		}
		id := strings.TrimPrefix(reqPath, "/api/output-shares/")
		w.handleHostOutputShareDelete(rw, r, id)
		return

	case reqPath == "/api/outputs/zip":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeMethodNotAllowed(rw, "GET, HEAD")
			return
		}
		if !w.authenticateRequest(rw, r) {
			writeHeadAwareJSONError(rw, r, http.StatusUnauthorized, "未登录或会话已过期")
			return
		}
		w.handleHostOutputsZip(rw, r)
		return

	case reqPath == "/api/web/capabilities":
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(rw, "GET")
			return
		}
		if !w.authenticateRequest(rw, r) {
			writeJSONError(rw, http.StatusUnauthorized, "未登录或会话已过期")
			return
		}
		w.handleCapabilities(rw)
		return

	case reqPath == "/api/snapshot":
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(rw, "GET")
			return
		}
		if !w.authenticateRequest(rw, r) {
			writeJSONError(rw, http.StatusUnauthorized, "未登录或会话已过期")
			return
		}
		w.handleSnapshot(rw, r)
		return

	case reqPath == "/api/command":
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(rw, "POST")
			return
		}
		if !w.authenticateRequest(rw, r) {
			writeJSONError(rw, http.StatusUnauthorized, "未登录或会话已过期")
			return
		}
		if !w.validateSameOriginRequest(r, true) {
			writeJSONError(rw, http.StatusForbidden, "跨源请求已被拒绝")
			return
		}
		w.handleCommand(rw, r)
		return

	case reqPath == colabOneLiveWSProxyPath:
		w.handleLiveStudioWSProxy(rw, r)
		return

	case strings.HasPrefix(reqPath, "/api/outputs/") && strings.HasSuffix(reqPath, "/download"):
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeMethodNotAllowed(rw, "GET, HEAD")
			return
		}
		if !w.authenticateRequest(rw, r) {
			writeHeadAwareJSONError(rw, r, http.StatusUnauthorized, "未登录或会话已过期")
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(reqPath, "/api/outputs/"), "/download")
		w.handleOutputDownload(rw, r, id)
		return

	case strings.HasPrefix(reqPath, "/api/outputs/") && strings.HasSuffix(reqPath, "/media"):
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeMethodNotAllowed(rw, "GET, HEAD")
			return
		}
		if !w.authenticateRequest(rw, r) {
			writeHeadAwareJSONError(rw, r, http.StatusUnauthorized, "未登录或会话已过期")
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(reqPath, "/api/outputs/"), "/media")
		w.handleOutputMedia(rw, r, id)
		return

	case reqPath == "/api" || strings.HasPrefix(reqPath, "/api/"):
		if !w.authenticateRequest(rw, r) {
			writeJSONError(rw, http.StatusUnauthorized, "未登录或会话已过期")
			return
		}
		writeJSONError(rw, http.StatusNotFound, "接口不存在")
		return

	case strings.HasPrefix(reqPath, "/assets/"):
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeMethodNotAllowed(rw, "GET, HEAD")
			return
		}
		authed := w.authenticateRequest(nil, r)
		if !authed {
			_, authed = w.authenticateShareRequest(nil, r)
		}
		if !authed {
			_, authed = w.authenticateOutputShareRequest(nil, r)
		}
		if !authed {
			writeJSONError(rw, http.StatusUnauthorized, "未登录或会话已过期")
			return
		}
		w.handleFrontendStaticAssetOnly(rw, r)
		return
	}

	// All remaining paths serve the authenticated frontend SPA.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeMethodNotAllowed(rw, "GET, HEAD")
		return
	}
	if !w.authenticateRequest(rw, r) {
		if shouldRedirectToLogin(r) {
			rw.Header().Set("Cache-Control", "no-store")
			http.Redirect(rw, r, "/_colabone_web/login", http.StatusFound)
			return
		}
		writeJSONError(rw, http.StatusUnauthorized, "未登录或会话已过期")
		return
	}
	w.handleFrontendStatic(rw, r)
}

func setBaseSecurityHeaders(rw http.ResponseWriter) {
	h := rw.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
}

func writeMethodNotAllowed(rw http.ResponseWriter, allow string) {
	rw.Header().Set("Allow", allow)
	http.Error(rw, "Method Not Allowed", http.StatusMethodNotAllowed)
}

func writeJSON(rw http.ResponseWriter, status int, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		http.Error(rw, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	rw.Header().Set("Cache-Control", "no-store")
	rw.WriteHeader(status)
	_, _ = rw.Write(data)
}

func writeJSONError(rw http.ResponseWriter, status int, message string) {
	writeJSON(rw, status, map[string]string{"error": message})
}

func writeHeadAwareJSONError(rw http.ResponseWriter, r *http.Request, status int, message string) {
	if r != nil && r.Method == http.MethodHead {
		rw.Header().Set("Cache-Control", "no-store")
		if message != "" {
			rw.Header().Set("X-ColabOne-Error", strings.ReplaceAll(url.PathEscape(message), "+", "%2B"))
		}
		rw.WriteHeader(status)
		return
	}
	writeJSONError(rw, status, message)
}

func isSameOriginDownloadRequest(r *http.Request) bool {
	if r == nil {
		return true
	}
	sfs := strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))
	if sfs == "" {
		return true
	}
	return strings.EqualFold(sfs, "same-origin") || strings.EqualFold(sfs, "none")
}

func parseRequestHost(rawHost string) (string, string, bool) {
	if rawHost == "" || strings.TrimSpace(rawHost) != rawHost {
		return "", "", false
	}
	for i := 0; i < len(rawHost); i++ {
		c := rawHost[i]
		if c <= 0x20 || c >= 0x7f {
			return "", "", false
		}
	}
	if strings.ContainsAny(rawHost, `/\@?#%`) {
		return "", "", false
	}

	var host, port string
	if strings.HasPrefix(rawHost, "[") {
		if strings.HasSuffix(rawHost, "]") {
			host = rawHost[1 : len(rawHost)-1]
		} else {
			var err error
			host, port, err = net.SplitHostPort(rawHost)
			if err != nil {
				return "", "", false
			}
		}
		addr, err := netip.ParseAddr(host)
		if err != nil || !addr.Is6() || addr.Zone() != "" {
			return "", "", false
		}
		host = addr.String()
	} else if strings.Contains(rawHost, ":") {
		var err error
		host, port, err = net.SplitHostPort(rawHost)
		if err != nil {
			return "", "", false
		}
	} else {
		host = rawHost
	}

	if port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 || strconv.Itoa(p) != port {
			return "", "", false
		}
	}

	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" || len(host) > 253 {
		return "", "", false
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Zone() != "" {
			return "", "", false
		}
		return addr.Unmap().String(), port, true
	}
	if !dnsHostRe.MatchString(host) {
		return "", "", false
	}
	return host, port, true
}

func (w *ColabOneWebConsole) hasPublicOrigin() bool {
	w.mu.RLock()
	has := w.cfg.PublicOrigin != ""
	w.mu.RUnlock()
	return has
}

func remoteAddrIP(r *http.Request) (netip.Addr, bool) {
	if r == nil {
		return netip.Addr{}, false
	}
	remoteHost := strings.TrimSpace(r.RemoteAddr)
	if h, _, err := net.SplitHostPort(remoteHost); err == nil {
		remoteHost = h
	}
	remoteHost = strings.Trim(remoteHost, "[]")
	addr, err := netip.ParseAddr(remoteHost)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func isLoopbackRemoteAddr(r *http.Request) bool {
	addr, ok := remoteAddrIP(r)
	return ok && addr.IsLoopback()
}

func (w *ColabOneWebConsole) isHostAllowed(host string) bool {
	w.mu.RLock()
	publicOrigin := w.cfg.PublicOrigin
	bindMode := w.cfg.BindMode
	w.mu.RUnlock()

	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}

	if publicOrigin != "" {
		if u, err := url.Parse(publicOrigin); err == nil {
			if strings.EqualFold(host, u.Hostname()) {
				return true
			}
		}
	}

	if bindMode == BindModeLAN {
		if addr, err := netip.ParseAddr(host); err == nil {
			unmapped := addr.Unmap()
			if !unmapped.IsLoopback() {
				listFn := w.listInterfaceIPs
				if listFn == nil {
					listFn = defaultListInterfaceIPs
				}
				for _, ifaceAddr := range listFn() {
					if ifaceAddr.Unmap() == unmapped {
						return true
					}
				}
			}
		}
	}

	return false
}

func (w *ColabOneWebConsole) isHTTPSRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	if !isLoopbackRemoteAddr(r) {
		return false
	}
	if protoVals := r.Header.Values("X-Forwarded-Proto"); len(protoVals) > 0 {
		for i := len(protoVals) - 1; i >= 0; i-- {
			parts := strings.Split(protoVals[i], ",")
			for j := len(parts) - 1; j >= 0; j-- {
				if last := strings.TrimSpace(parts[j]); last != "" {
					if strings.EqualFold(last, "https") {
						return true
					}
					if strings.EqualFold(last, "http") {
						return false
					}
					goto checkCFVisitor
				}
			}
		}
	}
checkCFVisitor:
	if w.hasPublicOrigin() {
		if cfVals := r.Header.Values("CF-Visitor"); len(cfVals) > 0 {
			for i := len(cfVals) - 1; i >= 0; i-- {
				if rawVisitor := strings.TrimSpace(cfVals[i]); rawVisitor != "" {
					var parsed struct {
						Scheme string `json:"scheme"`
					}
					if json.Unmarshal([]byte(rawVisitor), &parsed) == nil && strings.EqualFold(strings.TrimSpace(parsed.Scheme), "https") {
						return true
					}
					break
				}
			}
		}
	}
	return false
}

func normalizePortForScheme(scheme, port string) string {
	switch strings.ToLower(scheme) {
	case "https":
		if port == "443" {
			return ""
		}
	case "http":
		if port == "80" {
			return ""
		}
	}
	return port
}

func (w *ColabOneWebConsole) validateSameOriginRequest(r *http.Request, requireClientHeader bool) bool {
	if requireClientHeader {
		return w.validateSameOriginWithClientHeader(r, ClientHeaderValue)
	}
	return w.validateSameOriginWithClientHeader(r, "")
}

func (w *ColabOneWebConsole) validateSameOriginWithClientHeader(r *http.Request, expectedClientHeader string) bool {
	if expectedClientHeader != "" && r.Header.Get(ClientHeaderName) != expectedClientHeader {
		return false
	}

	for _, rawSFS := range r.Header.Values("Sec-Fetch-Site") {
		if sfs := strings.TrimSpace(rawSFS); sfs != "" {
			if !strings.EqualFold(sfs, "same-origin") && !strings.EqualFold(sfs, "none") {
				return false
			}
		}
	}

	var origin string
	for _, rawOrig := range r.Header.Values("Origin") {
		trimmed := strings.TrimSpace(rawOrig)
		if trimmed == "" {
			continue
		}
		if origin != "" {
			// Reject duplicate Origin headers to prevent proxy/parser desynchronization.
			return false
		}
		origin = trimmed
	}
	if origin != "" {
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
			u.Opaque != "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return false
		}
		originHost, originPort, ok := parseRequestHost(u.Host)
		if !ok {
			return false
		}
		reqHost, reqPort, ok := parseRequestHost(r.Host)
		if !ok {
			return false
		}
		reqScheme := "http"
		if w.isHTTPSRequest(r) {
			reqScheme = "https"
		}
		originScheme := strings.ToLower(u.Scheme)
		originPort = normalizePortForScheme(originScheme, originPort)
		reqPort = normalizePortForScheme(reqScheme, reqPort)
		if originScheme != reqScheme || originHost != reqHost || originPort != reqPort {
			return false
		}
	}

	return true
}

func (w *ColabOneWebConsole) resolveLocale(r *http.Request) string {
	if w.opts.GetLocale != nil {
		if loc := strings.TrimSpace(w.opts.GetLocale()); loc != "" {
			return loc
		}
	}
	return localeFromAcceptLanguage(r.Header.Get("Accept-Language"))
}

func (w *ColabOneWebConsole) resolveGuestLocale(r *http.Request) string {
	if r != nil {
		if loc, ok := localeFromAcceptLanguageWithQ(r.Header.Get("Accept-Language")); ok {
			return loc
		}
	}
	if w.opts.GetLocale != nil {
		if loc := strings.TrimSpace(w.opts.GetLocale()); loc != "" {
			return loc
		}
	}
	return colabOneWebDefaultLocale
}

func serveBrandIconAsset(rw http.ResponseWriter, r *http.Request, contentType string, data []byte) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeMethodNotAllowed(rw, "GET, HEAD")
		return
	}
	rw.Header().Set("Content-Type", contentType)
	rw.Header().Set("Cache-Control", "public, max-age=86400")
	rw.Header().Set("Content-Length", strconv.Itoa(len(data)))
	rw.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = rw.Write(data)
	}
}

func (w *ColabOneWebConsole) handleLoginPage(rw http.ResponseWriter, r *http.Request, status int, errCode string) {
	nonce, err := generateBase64Token(16)
	if err != nil {
		nonce = "colabone"
	}
	locale := w.resolveLocale(r)
	htmlDoc := renderColabOneWebLoginPage(locale, errCode, nonce)

	csp := fmt.Sprintf(
		"default-src 'none'; style-src 'nonce-%s'; script-src 'nonce-%s'; img-src 'self' data:; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'",
		nonce, nonce,
	)
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.Header().Set("Cache-Control", "no-store")
	rw.Header().Set("X-Robots-Tag", "noindex, nofollow")
	rw.Header().Set("Content-Security-Policy", csp)
	rw.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(rw, htmlDoc)
	}
}

func (w *ColabOneWebConsole) handleLoginSubmit(rw http.ResponseWriter, r *http.Request) {
	_, copySet := resolveWebLoginCopy(w.resolveLocale(r))
	if !w.validateSameOriginRequest(r, true) {
		writeJSONError(rw, http.StatusForbidden, copySet.forbiddenError)
		return
	}

	r.Body = http.MaxBytesReader(rw, r.Body, maxLoginBodyBytes)
	var body struct {
		Passcode string `json:"passcode"`
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&body); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSONError(rw, http.StatusRequestEntityTooLarge, copySet.bodyTooLargeError)
			return
		}
		writeJSONError(rw, http.StatusBadRequest, copySet.badRequestError)
		return
	}

	submittedPasscode := strings.TrimSpace(body.Passcode)
	ip := w.clientIPForRateLimit(r)
	now := w.now()

	token, err := generateBase64Token(32)
	if err != nil {
		writeJSONError(rw, http.StatusInternalServerError, copySet.sessionError)
		return
	}

	w.mu.Lock()
	if w.closed || w.stopping {
		w.mu.Unlock()
		writeJSONError(rw, http.StatusServiceUnavailable, copySet.sessionError)
		return
	}
	retryAfter := w.checkRateLimitLocked(ip, now)
	if retryAfter > 0 {
		w.mu.Unlock()
		rw.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		writeJSON(rw, http.StatusTooManyRequests, map[string]any{
			"error":       copySet.rateLimitError,
			"retry_after": retryAfter,
		})
		return
	}
	expected := w.passcode
	matched := expected != "" && subtle.ConstantTimeCompare([]byte(submittedPasscode), []byte(expected)) == 1
	if !matched {
		w.recordLoginFailureLocked(ip, now)
		w.mu.Unlock()
		writeJSONError(rw, http.StatusUnauthorized, copySet.invalidPasscodeError)
		return
	}
	delete(w.loginFailures, ip)
	w.storeSessionLocked(token, now)
	w.mu.Unlock()
	w.persistSessions()

	w.setSessionCookie(rw, r, token, now)
	w.emitStateChange(w.State())
	writeJSON(rw, http.StatusOK, map[string]bool{"ok": true})
}

var linkPreviewBotTokens = []string{
	"telegrambot",
	"whatsapp",
	"slackbot",
	"discordbot",
	"twitterbot",
	"facebookexternalhit",
	"facebot",
	"linkedinbot",
	"skypeuripreview",
	"bingpreview",
	"embedly",
	"quora link preview",
	"vkshare",
	"line-poker",
	"kakaotalk-scrap",
	"feishu",
	"larkurlpreview",
}

func isLinkPreviewRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	for _, hdr := range []string{"Sec-Purpose", "Purpose", "X-Purpose", "X-Moz"} {
		val := strings.ToLower(strings.TrimSpace(r.Header.Get(hdr)))
		if strings.Contains(val, "prefetch") || strings.Contains(val, "preview") {
			return true
		}
	}
	ua := strings.ToLower(strings.TrimSpace(r.Header.Get("User-Agent")))
	if ua == "" {
		return false
	}
	for _, token := range linkPreviewBotTokens {
		if strings.Contains(ua, token) {
			return true
		}
	}
	return false
}

func (w *ColabOneWebConsole) handleRedeem(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Cache-Control", "no-store")
	rw.Header().Set("Referrer-Policy", "no-referrer")

	// Messaging apps (Telegram, Slack, Discord, WhatsApp, etc.) and browser
	// link-prefetchers routinely pre-fetch shared URLs. Return a minimal
	// noindex stub without minting a session or consuming a single-use ticket.
	if isLinkPreviewRequest(r) {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		rw.Header().Set("X-Robots-Tag", "noindex, nofollow")
		rw.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(rw, `<!doctype html><html><head><meta charset="utf-8"><meta name="robots" content="noindex,nofollow"><title>ColabOne</title></head><body>ColabOne Remote Web Console</body></html>`)
		return
	}

	rawTicket := strings.TrimSpace(r.URL.Query().Get("ticket"))
	if rawTicket == "" {
		w.handleLoginPage(rw, r, http.StatusUnauthorized, "ticket_expired")
		return
	}
	digest := sha256.Sum256([]byte(rawTicket))
	now := w.now()

	existingCookieVal := ""
	var existingDigest [32]byte
	if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
		existingCookieVal = c.Value
		existingDigest = sha256.Sum256([]byte(c.Value))
	}

	token, tokenErr := generateBase64Token(32)

	w.mu.Lock()
	if w.closed || w.stopping || !w.running {
		w.mu.Unlock()
		w.handleLoginPage(rw, r, http.StatusUnauthorized, "ticket_expired")
		return
	}
	w.pruneExpiredTicketsLocked(now)
	ticket, exists := w.tickets[digest]
	valid := exists && now.Before(ticket.expiresAt)
	if !valid {
		w.mu.Unlock()
		w.handleLoginPage(rw, r, http.StatusUnauthorized, "ticket_expired")
		return
	}

	// Check whether this browser already holds a valid session atomically under the same lock.
	alreadyAuthed := false
	needsRefresh := false
	if existingCookieVal != "" {
		if s, ok := w.sessions[existingDigest]; ok && now.Before(s.expiresAt) {
			alreadyAuthed = true
			s.lastSeen = now
			s.expiresAt = now.Add(SessionTTL)
			if now.Sub(s.cookieRefreshedAt) >= sessionRenewalInterval {
				s.cookieRefreshedAt = now
				needsRefresh = true
			}
			w.sessions[existingDigest] = s
		}
	}

	if !alreadyAuthed && tokenErr != nil {
		w.mu.Unlock()
		_, copySet := resolveWebLoginCopy(w.resolveLocale(r))
		writeJSONError(rw, http.StatusInternalServerError, copySet.sessionError)
		return
	}
	if ticket.singleUse {
		delete(w.tickets, digest)
	}
	if !alreadyAuthed {
		w.storeSessionLocked(token, now)
	}
	w.mu.Unlock()

	if alreadyAuthed {
		if needsRefresh {
			w.setSessionCookie(rw, r, existingCookieVal, now)
			w.persistSessions()
		}
		http.Redirect(rw, r, "/", http.StatusFound)
		return
	}

	w.persistSessions()
	w.setSessionCookie(rw, r, token, now)
	w.emitStateChange(w.State())
	http.Redirect(rw, r, "/", http.StatusFound)
}

func (w *ColabOneWebConsole) handleLogout(rw http.ResponseWriter, r *http.Request) {
	if !w.validateSameOriginRequest(r, true) {
		writeJSONError(rw, http.StatusForbidden, "跨源请求已被拒绝")
		return
	}

	removed := false
	var logoutDigest [32]byte
	if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
		digest := sha256.Sum256([]byte(c.Value))
		logoutDigest = digest
		w.mu.Lock()
		if _, ok := w.sessions[digest]; ok {
			delete(w.sessions, digest)
			removed = true
		}
		w.mu.Unlock()
	}

	w.clearSessionCookie(rw, r)
	if removed {
		w.cancelLiveWSConnsForSession(logoutDigest, 4401)
		w.persistSessions()
		w.emitStateChange(w.State())
	}
	writeJSON(rw, http.StatusOK, map[string]bool{"ok": true})
}

func (w *ColabOneWebConsole) handleCapabilities(rw http.ResponseWriter) {
	w.mu.RLock()
	bindMode := w.cfg.BindMode
	port := w.cfg.Port
	publicOrigin := w.cfg.PublicOrigin
	w.mu.RUnlock()
	localURL := ""
	if port > 0 {
		localURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	}
	var lanURLs []string
	if bindMode == BindModeLAN && port > 0 {
		lanURLs = w.lanURLs(port)
	}
	writeJSON(rw, http.StatusOK, ColabOneWebCapabilities{
		Runtime:                   "web",
		Version:                   w.opts.AppVersion,
		BindMode:                  bindMode,
		LocalURL:                  localURL,
		LANURLs:                   lanURLs,
		PublicOrigin:              publicOrigin,
		CanOpenNativeCanvasWindow: false,
		CanStandaloneLogWindow:    false,
		CanManageWebConsole:       false,
		CanManageCloseBehavior:    false,
		CanManageShortcuts:        false,
		CanSelfUpdate:             false,
		CanMigrate:                false,
		CanRevealHostSecrets:      false,
		CanLoopbackOAuth:          false,
		CanDownloadOutput:         true,
	})
}

func (w *ColabOneWebConsole) handleSnapshot(rw http.ResponseWriter, r *http.Request) {
	if w.opts.CallBridge == nil {
		writeJSONError(rw, http.StatusServiceUnavailable, "控制器桥接未初始化")
		return
	}
	raw, err := w.opts.CallBridge(r.Context(), "snapshot", nil)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			writeJSONError(rw, http.StatusGatewayTimeout, err.Error())
			return
		}
		writeJSONError(rw, http.StatusInternalServerError, err.Error())
		return
	}

	raw = w.EnrichHostSnapshotJSON(raw)
	writeSnapshotBytes(rw, r, []byte(raw))
}

func writeSnapshotBytes(rw http.ResponseWriter, r *http.Request, rawBytes []byte) {
	sum := sha256.Sum256(rawBytes)
	etag := fmt.Sprintf(`"sha256-%x"`, sum)

	rw.Header().Set("ETag", etag)
	rw.Header().Set("Cache-Control", "no-store")
	rw.Header().Set("Vary", "Accept-Encoding")

	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		rw.WriteHeader(http.StatusNotModified)
		return
	}

	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	if len(rawBytes) > gzipMinBytes && acceptsGzip(r.Header.Get("Accept-Encoding")) {
		var compressed bytes.Buffer
		gz := gzip.NewWriter(&compressed)
		if _, wErr := gz.Write(rawBytes); wErr == nil && gz.Close() == nil {
			rw.Header().Set("Content-Encoding", "gzip")
			rw.Header().Set("Content-Length", strconv.Itoa(compressed.Len()))
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write(compressed.Bytes())
			return
		}
	}

	rw.Header().Set("Content-Length", strconv.Itoa(len(rawBytes)))
	rw.WriteHeader(http.StatusOK)
	_, _ = rw.Write(rawBytes)
}

type countingReadCloser struct {
	r io.Reader
	c io.Closer
	n int64
}

func (cr *countingReadCloser) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	cr.n += int64(n)
	return n, err
}

func (cr *countingReadCloser) Close() error {
	if cr.c != nil {
		return cr.c.Close()
	}
	return nil
}

func skipJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if _, ok := tok.(json.Delim); !ok {
		return nil
	}
	depth := 1
	for depth > 0 {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := t.(json.Delim); ok {
			switch d {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
	}
	return nil
}

// peekCommandAction inspects the leading JSON bytes for a top-level "action"
// string so media-upload commands can opt into the larger body cap without
// allowing arbitrary commands past maxCommandBodyBytes.
func peekCommandAction(prefix []byte) string {
	dec := json.NewDecoder(bytes.NewReader(prefix))
	tok, err := dec.Token()
	if err != nil {
		return ""
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return ""
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return ""
		}
		key, ok := keyTok.(string)
		if !ok {
			return ""
		}
		if strings.EqualFold(key, "action") {
			valTok, err := dec.Token()
			if err != nil {
				return ""
			}
			if action, ok := valTok.(string); ok {
				return strings.TrimSpace(action)
			}
			return ""
		}
		if err := skipJSONValue(dec); err != nil {
			return ""
		}
	}
	return ""
}

func (w *ColabOneWebConsole) commandBodyLimits() (normalLimit, mediaLimit int64) {
	normalLimit = w.maxCommandBodyBytes
	if normalLimit <= 0 {
		normalLimit = maxCommandBodyBytes
	}
	mediaLimit = w.maxMediaUploadCommandBodyBytes
	if mediaLimit <= 0 {
		mediaLimit = maxMediaUploadCommandBodyBytes
	}
	if mediaLimit < normalLimit {
		mediaLimit = normalLimit
	}
	return normalLimit, mediaLimit
}

func (w *ColabOneWebConsole) handleCommand(rw http.ResponseWriter, r *http.Request) {
	if w.opts.CallBridge == nil {
		writeJSONError(rw, http.StatusServiceUnavailable, "控制器桥接未初始化")
		return
	}

	normalLimit, mediaLimit := w.commandBodyLimits()
	if r.ContentLength > mediaLimit {
		writeJSONError(rw, http.StatusRequestEntityTooLarge, "请求体超出限制")
		return
	}

	peekSize := int64(commandActionPeekBytes)
	if normalLimit+1 < peekSize {
		peekSize = normalLimit + 1
	}
	prefixBuf := make([]byte, peekSize)
	prefixN, prefixErr := io.ReadFull(r.Body, prefixBuf)
	if prefixErr != nil && !errors.Is(prefixErr, io.EOF) && !errors.Is(prefixErr, io.ErrUnexpectedEOF) {
		writeJSONError(rw, http.StatusBadRequest, "命令请求格式无效")
		return
	}
	prefix := prefixBuf[:prefixN]

	bodyLimit := normalLimit
	if mediaUploadWebCommands[peekCommandAction(prefix)] {
		bodyLimit = mediaLimit
	}
	if r.ContentLength > bodyLimit {
		writeJSONError(rw, http.StatusRequestEntityTooLarge, "请求体超出限制")
		return
	}

	cr := &countingReadCloser{
		r: io.MultiReader(bytes.NewReader(prefix), r.Body),
		c: r.Body,
	}
	r.Body = http.MaxBytesReader(rw, cr, bodyLimit)
	var req struct {
		Action  string         `json:"action"`
		Payload map[string]any `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSONError(rw, http.StatusRequestEntityTooLarge, "请求体超出限制")
			return
		}
		writeJSONError(rw, http.StatusBadRequest, "命令请求格式无效")
		return
	}

	action := strings.TrimSpace(req.Action)
	if cr.n > normalLimit && !mediaUploadWebCommands[action] {
		writeJSONError(rw, http.StatusRequestEntityTooLarge, "请求体超出限制")
		return
	}
	if action == "" {
		writeJSONError(rw, http.StatusBadRequest, "缺少命令名称")
		return
	}

	if reason, blocked := blockedWebCommands[action]; blocked {
		writeJSONError(rw, http.StatusForbidden, reason)
		return
	}

	if action == "comfyui.run" {
		w.NotifyHostWorkflowRunStarting()
	}

	res, err := w.opts.CallBridge(r.Context(), action, req.Payload)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			writeJSONError(rw, http.StatusGatewayTimeout, err.Error())
			return
		}
		writeJSONError(rw, http.StatusBadRequest, err.Error())
		return
	}
	if action == "comfyui.output.delete" {
		if id, ok := req.Payload["id"].(string); ok && strings.TrimSpace(id) != "" {
			w.ForgetOutputOwnership(id)
		}
	} else if action == "comfyui.run" {
		w.NotifyHostWorkflowRunStarted()
	}

	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	rw.Header().Set("Cache-Control", "no-store")
	rw.WriteHeader(http.StatusOK)
	if strings.TrimSpace(res) == "" {
		_, _ = io.WriteString(rw, "{}")
		return
	}
	_, _ = io.WriteString(rw, res)
}

func (w *ColabOneWebConsole) handleOutputDownload(rw http.ResponseWriter, r *http.Request, id string) {
	w.handleOutputDownloadWithOptions(rw, r, id, false)
}

func (w *ColabOneWebConsole) handleOutputDownloadWithOptions(rw http.ResponseWriter, r *http.Request, id string, runtimeOnly bool) {
	if !isSameOriginDownloadRequest(r) {
		writeHeadAwareJSONError(rw, r, http.StatusForbidden, "跨源请求已被拒绝")
		return
	}
	if !outputIDRe.MatchString(id) {
		writeHeadAwareJSONError(rw, r, http.StatusBadRequest, "输出文件 ID 无效")
		return
	}
	if w.opts.CallBridge == nil {
		writeHeadAwareJSONError(rw, r, http.StatusServiceUnavailable, "控制器桥接未初始化")
		return
	}

	makeChunkPayload := func(off int64) map[string]any {
		p := map[string]any{"id": id, "offset": off}
		if runtimeOnly {
			p["runtime_only"] = true
		}
		return p
	}
	formatBridgeErr := func(err error) string {
		if runtimeOnly {
			return sanitizeGuestBridgeError(err.Error())
		}
		return err.Error()
	}

	raw, err := w.opts.CallBridge(r.Context(), "comfyui.output.asset", makeChunkPayload(0))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			writeHeadAwareJSONError(rw, r, http.StatusGatewayTimeout, formatBridgeErr(err))
			return
		}
		writeHeadAwareJSONError(rw, r, http.StatusBadGateway, formatBridgeErr(err))
		return
	}

	var chunk colabOneWebMediaChunk
	if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
		writeHeadAwareJSONError(rw, r, http.StatusBadGateway, "输出文件元数据无效")
		return
	}
	if runtimeOnly && strings.EqualFold(strings.TrimSpace(chunk.Source), "drive") {
		writeHeadAwareJSONError(rw, r, http.StatusForbidden, "该作品已归档至宿主云盘，分享模式下不可访问")
		return
	}

	ext := strings.ToLower(strings.TrimSpace(chunk.Extension))
	mimeType, extOK := allowedOutputMIME[ext]
	digest := strings.ToLower(strings.TrimSpace(chunk.SHA256))
	allowEmptyDigest := !runtimeOnly && chunk.Source == "drive" && digest == ""
	if chunk.Size <= 0 || chunk.Size > maxOutputSizeBytes || !extOK || (!allowEmptyDigest && !sha256HexRe.MatchString(digest)) {
		writeHeadAwareJSONError(rw, r, http.StatusBadGateway, "输出文件元数据未通过安全校验")
		return
	}

	totalSize := chunk.Size
	filename := fmt.Sprintf("ColabOne-%s.%s", id[:8], ext)
	if r.Method == http.MethodHead {
		writeOutputDownloadHeaders(rw, mimeType, filename, totalSize)
		rw.WriteHeader(http.StatusOK)
		return
	}
	bufferInMemory := totalSize <= maxOutputBufferBytes

	var buf bytes.Buffer
	if bufferInMemory {
		buf.Grow(int(totalSize))
	}

	hasher := sha256.New()
	var offset int64
	headersWritten := false
	flusher, _ := rw.(http.Flusher)

	for {
		if runtimeOnly && strings.EqualFold(strings.TrimSpace(chunk.Source), "drive") {
			if !headersWritten {
				writeJSONError(rw, http.StatusForbidden, "该作品已归档至宿主云盘，分享模式下不可访问")
			} else {
				w.logActivity("分享模式输出文件下载检测到非运行时来源，已中止传输", "warn")
			}
			return
		}
		chunkDigest := strings.ToLower(strings.TrimSpace(chunk.SHA256))
		isDriveChunk := !runtimeOnly && chunk.Source == "drive"
		digestMismatch := false
		if digest == "" {
			if chunkDigest != "" {
				if isDriveChunk && sha256HexRe.MatchString(chunkDigest) {
					digest = chunkDigest
				} else {
					digestMismatch = true
				}
			}
		} else if chunkDigest != digest {
			if !(isDriveChunk && chunkDigest == "") {
				digestMismatch = true
			}
		}
		if chunk.Offset != offset || chunk.Size != totalSize || digestMismatch {
			if !headersWritten {
				writeJSONError(rw, http.StatusBadGateway, "输出文件传输元数据发生变化")
			} else {
				w.logActivity("输出文件流式下载元数据不一致，已中止传输", "warn")
			}
			return
		}

		data, decErr := base64.StdEncoding.DecodeString(chunk.Data)
		if decErr != nil || len(data) == 0 || len(data) > maxOutputChunkBytes || offset+int64(len(data)) > totalSize {
			if !headersWritten {
				writeJSONError(rw, http.StatusBadGateway, "输出文件分片数据不完整")
			} else {
				w.logActivity("输出文件流式下载分片校验失败，已中止传输", "warn")
			}
			return
		}

		hasher.Write(data)
		nextOffset := offset + int64(len(data))

		if bufferInMemory {
			_, _ = buf.Write(data)
		} else {
			if !headersWritten {
				writeOutputDownloadHeaders(rw, mimeType, filename, totalSize)
				rw.WriteHeader(http.StatusOK)
				headersWritten = true
			}
			// Verify SHA-256 before writing the final chunk so a mismatch results
			// in an incomplete body rather than silently completing Content-Length.
			if nextOffset == totalSize && digest != "" && hex.EncodeToString(hasher.Sum(nil)) != digest {
				w.logActivity("输出文件流式下载 SHA-256 校验失败，已中止传输", "warn")
				return
			}
			if _, wErr := rw.Write(data); wErr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}

		offset = nextOffset
		if offset == totalSize {
			break
		}

		nextRaw, callErr := w.opts.CallBridge(r.Context(), "comfyui.output.asset", makeChunkPayload(offset))
		if callErr != nil {
			if !headersWritten {
				if errors.Is(callErr, context.DeadlineExceeded) {
					writeJSONError(rw, http.StatusGatewayTimeout, formatBridgeErr(callErr))
				} else {
					writeJSONError(rw, http.StatusBadGateway, formatBridgeErr(callErr))
				}
			}
			return
		}
		var nextChunk colabOneWebMediaChunk
		if err := json.Unmarshal([]byte(nextRaw), &nextChunk); err != nil {
			if !headersWritten {
				writeJSONError(rw, http.StatusBadGateway, "输出文件后续分片格式无效")
			}
			return
		}
		chunk = nextChunk
	}

	if bufferInMemory {
		if digest != "" && hex.EncodeToString(hasher.Sum(nil)) != digest {
			writeJSONError(rw, http.StatusBadGateway, "输出文件完整性校验失败")
			return
		}
		writeOutputDownloadHeaders(rw, mimeType, filename, totalSize)
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write(buf.Bytes())
	}
}

func (w *ColabOneWebConsole) handleOutputMedia(rw http.ResponseWriter, r *http.Request, id string) {
	rw.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	if !isSameOriginDownloadRequest(r) {
		writeHeadAwareJSONError(rw, r, http.StatusForbidden, "跨源请求已被拒绝")
		return
	}
	if !outputIDRe.MatchString(id) {
		writeHeadAwareJSONError(rw, r, http.StatusBadRequest, "输出文件 ID 无效")
		return
	}
	if w.opts.ServeOutputMedia == nil {
		writeHeadAwareJSONError(rw, r, http.StatusServiceUnavailable, "控制器桥接未初始化")
		return
	}
	w.opts.ServeOutputMedia(r.Context(), rw, r, id)
}

func writeOutputDownloadHeaders(rw http.ResponseWriter, mimeType, filename string, size int64) {
	h := rw.Header()
	h.Set("Content-Type", mimeType)
	h.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	h.Set("Content-Length", strconv.FormatInt(size, 10))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
}

func (w *ColabOneWebConsole) serveFrontendIndex(rw http.ResponseWriter, r *http.Request) {
	var fsys fs.FS
	if w.opts.FrontendFS != nil {
		fsys = w.opts.FrontendFS()
	}
	if fsys == nil {
		rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
		rw.Header().Set("Cache-Control", "no-store")
		rw.WriteHeader(http.StatusServiceUnavailable)
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(rw, "前端静态资源尚未就绪，请稍后再试。")
		}
		return
	}

	f, info, found := openStaticFile(fsys, "index.html")
	if !found {
		http.NotFound(rw, r)
		return
	}
	defer f.Close()

	if ct := detectStaticMIME("index.html"); ct != "" {
		rw.Header().Set("Content-Type", ct)
	}
	rw.Header().Set("Cache-Control", "no-cache")
	rw.Header().Set("X-Robots-Tag", "noindex, nofollow")
	rw.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; media-src 'self' blob:; font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(rw, r, info.Name(), info.ModTime(), rs)
		return
	}
	data, err := io.ReadAll(f)
	if err != nil {
		http.Error(rw, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Length", strconv.Itoa(len(data)))
	rw.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = rw.Write(data)
	}
}

func (w *ColabOneWebConsole) handleFrontendStaticAssetOnly(rw http.ResponseWriter, r *http.Request) {
	if hasUnsafeStaticPath(r.URL.Path, r.URL.RawPath) {
		http.NotFound(rw, r)
		return
	}
	var fsys fs.FS
	if w.opts.FrontendFS != nil {
		fsys = w.opts.FrontendFS()
	}
	if fsys == nil {
		rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
		rw.Header().Set("Cache-Control", "no-store")
		rw.WriteHeader(http.StatusServiceUnavailable)
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(rw, "前端静态资源尚未就绪，请稍后再试。")
		}
		return
	}
	cleanPath := path.Clean("/" + r.URL.Path)
	rel := strings.TrimPrefix(cleanPath, "/")
	f, info, found := openStaticFile(fsys, rel)
	if !found {
		http.NotFound(rw, r)
		return
	}
	defer f.Close()

	if ct := detectStaticMIME(rel); ct != "" {
		rw.Header().Set("Content-Type", ct)
	}
	if cleanPath == "/assets/colabone-bootstrap.js" {
		rw.Header().Set("Cache-Control", "no-cache")
	} else {
		rw.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	}
	if strings.EqualFold(path.Ext(rel), ".svg") {
		rw.Header().Set("X-Robots-Tag", "noindex, nofollow")
		rw.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	}
	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(rw, r, info.Name(), info.ModTime(), rs)
		return
	}
	data, err := io.ReadAll(f)
	if err != nil {
		http.Error(rw, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Length", strconv.Itoa(len(data)))
	rw.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = rw.Write(data)
	}
}

func (w *ColabOneWebConsole) handleFrontendStatic(rw http.ResponseWriter, r *http.Request) {
	if hasUnsafeStaticPath(r.URL.Path, r.URL.RawPath) {
		http.NotFound(rw, r)
		return
	}

	var fsys fs.FS
	if w.opts.FrontendFS != nil {
		fsys = w.opts.FrontendFS()
	}
	if fsys == nil {
		rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
		rw.Header().Set("Cache-Control", "no-store")
		rw.WriteHeader(http.StatusServiceUnavailable)
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(rw, "前端静态资源尚未就绪，请稍后再试。")
		}
		return
	}

	cleanPath := path.Clean("/" + r.URL.Path)
	rel := strings.TrimPrefix(cleanPath, "/")
	if rel == "" {
		rel = "index.html"
	}

	f, info, found := openStaticFile(fsys, rel)
	isIndexFallback := false
	if !found {
		if path.Ext(cleanPath) != "" {
			http.NotFound(rw, r)
			return
		}
		f, info, found = openStaticFile(fsys, "index.html")
		if !found {
			http.NotFound(rw, r)
			return
		}
		rel = "index.html"
		isIndexFallback = true
	}
	defer f.Close()

	if ct := detectStaticMIME(rel); ct != "" {
		rw.Header().Set("Content-Type", ct)
	}
	extLower := strings.ToLower(path.Ext(rel))
	if rel == "index.html" || extLower == ".html" || extLower == ".htm" {
		rw.Header().Set("X-Robots-Tag", "noindex, nofollow")
		rw.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; media-src 'self' blob:; font-src 'self' data:; connect-src 'self' ws: wss:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	} else if extLower == ".svg" {
		rw.Header().Set("X-Robots-Tag", "noindex, nofollow")
		rw.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	}
	if !isIndexFallback && strings.HasPrefix(cleanPath, "/assets/") && cleanPath != "/assets/colabone-bootstrap.js" {
		rw.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		rw.Header().Set("Cache-Control", "no-cache")
	}

	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(rw, r, info.Name(), info.ModTime(), rs)
		return
	}
	data, err := io.ReadAll(f)
	if err != nil {
		http.Error(rw, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Length", strconv.Itoa(len(data)))
	rw.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = rw.Write(data)
	}
}

func openStaticFile(fsys fs.FS, rel string) (fs.File, fs.FileInfo, bool) {
	if rel == "" || strings.HasPrefix(rel, "/") || strings.Contains(rel, "..") {
		return nil, nil, false
	}
	f, err := fsys.Open(rel)
	if err != nil {
		return nil, nil, false
	}
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		_ = f.Close()
		return nil, nil, false
	}
	return f, info, true
}

func hasUnsafeStaticPath(urlPath, rawPath string) bool {
	check := func(p string) bool {
		if strings.ContainsRune(p, 0) || strings.Contains(p, `\`) {
			return true
		}
		for _, seg := range strings.Split(p, "/") {
			if seg == "" {
				continue
			}
			if seg == "." || seg == ".." || strings.HasPrefix(seg, ".") {
				return true
			}
		}
		return false
	}
	if check(urlPath) {
		return true
	}
	if rawPath != "" {
		if check(rawPath) {
			return true
		}
		if decoded, err := url.PathUnescape(rawPath); err == nil && check(decoded) {
			return true
		}
	}
	return false
}

func detectStaticMIME(rel string) string {
	ext := strings.ToLower(path.Ext(rel))
	if ct, ok := staticMIMEByExt[ext]; ok {
		return ct
	}
	return mime.TypeByExtension(ext)
}

func shouldRedirectToLogin(r *http.Request) bool {
	if r.URL.Path == "/" || path.Ext(r.URL.Path) == "" {
		return true
	}
	accept := strings.ToLower(r.Header.Get("Accept"))
	return strings.Contains(accept, "text/html")
}

// --- Session, ticket, and rate-limiter helpers ---

func hashPasscodeFingerprint(passcode string) string {
	if passcode == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("colabone:web_session_bind:v1:" + passcode))
	return hex.EncodeToString(sum[:])
}

func (w *ColabOneWebConsole) loadPersistedSessions(passcode string) {
	if w.opts.Vault == nil || passcode == "" {
		return
	}
	var store colabOnePersistedWebSessionStore
	found, err := w.opts.Vault.GetJSON(VaultSessionsKey, &store)
	if err != nil || !found {
		return
	}
	if store.Version != persistedSessionsVersion {
		return
	}
	expectedHash := hashPasscodeFingerprint(passcode)
	if subtle.ConstantTimeCompare([]byte(store.PasscodeHash), []byte(expectedHash)) != 1 {
		return
	}
	now := w.now()

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.passcode != passcode {
		return
	}
	for _, rec := range store.Sessions {
		if rec.ExpiresAt <= 0 {
			continue
		}
		expiresAt := time.Unix(rec.ExpiresAt, 0)
		if !now.Before(expiresAt) {
			continue
		}
		rawDigest, err := hex.DecodeString(strings.TrimSpace(rec.DigestHex))
		if err != nil || len(rawDigest) != 32 {
			continue
		}
		var digest [32]byte
		copy(digest[:], rawDigest)

		createdAt := now
		if rec.CreatedAt > 0 {
			createdAt = time.Unix(rec.CreatedAt, 0)
		}
		lastSeen := createdAt
		if rec.LastSeen > 0 {
			lastSeen = time.Unix(rec.LastSeen, 0)
		}
		for len(w.sessions) >= maxActiveSessions {
			evictOldestSessionLocked(w.sessions)
		}
		w.sessions[digest] = colabOneWebSession{
			createdAt:         createdAt,
			lastSeen:          lastSeen,
			expiresAt:         expiresAt,
			cookieRefreshedAt: lastSeen,
		}
	}
}

func (w *ColabOneWebConsole) persistSessions() {
	if w.opts.Vault == nil {
		return
	}
	w.persistMu.Lock()
	defer w.persistMu.Unlock()

	now := w.now()
	w.mu.RLock()
	if w.closed {
		w.mu.RUnlock()
		return
	}
	passcode := w.passcode
	entries := make([]colabOnePersistedWebSession, 0, len(w.sessions))
	for digest, s := range w.sessions {
		if !now.Before(s.expiresAt) {
			continue
		}
		entries = append(entries, colabOnePersistedWebSession{
			DigestHex: hex.EncodeToString(digest[:]),
			CreatedAt: s.createdAt.Unix(),
			LastSeen:  s.lastSeen.Unix(),
			ExpiresAt: s.expiresAt.Unix(),
		})
	}
	w.mu.RUnlock()

	if passcode == "" || len(entries) == 0 {
		if err := w.opts.Vault.Delete(VaultSessionsKey); err != nil {
			w.logActivity(fmt.Sprintf("清理网页控制台持久化会话失败：%s", err.Error()), "warn")
		}
		return
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].LastSeen != entries[j].LastSeen {
			return entries[i].LastSeen < entries[j].LastSeen
		}
		return entries[i].DigestHex < entries[j].DigestHex
	})

	store := colabOnePersistedWebSessionStore{
		Version:      persistedSessionsVersion,
		PasscodeHash: hashPasscodeFingerprint(passcode),
		Sessions:     entries,
	}
	if err := w.opts.Vault.Set(VaultSessionsKey, store); err != nil {
		w.logActivity(fmt.Sprintf("保存网页控制台持久化会话失败：%s", err.Error()), "warn")
	}
}

func (w *ColabOneWebConsole) authenticateRequest(rw http.ResponseWriter, r *http.Request) bool {
	_, ok := w.authenticateRequestWithDigest(rw, r)
	return ok
}

func (w *ColabOneWebConsole) authenticateRequestWithDigest(rw http.ResponseWriter, r *http.Request) ([32]byte, bool) {
	var zero [32]byte
	c, err := r.Cookie(SessionCookieName)
	if err != nil || c.Value == "" {
		return zero, false
	}
	digest := sha256.Sum256([]byte(c.Value))
	now := w.now()

	w.mu.Lock()
	s, ok := w.sessions[digest]
	if !ok {
		w.mu.Unlock()
		return zero, false
	}
	if !now.Before(s.expiresAt) {
		delete(w.sessions, digest)
		w.mu.Unlock()
		w.persistSessions()
		w.emitStateChange(w.State())
		return zero, false
	}
	s.lastSeen = now
	s.expiresAt = now.Add(SessionTTL)
	needsRefresh := rw != nil && now.Sub(s.cookieRefreshedAt) >= sessionRenewalInterval
	if needsRefresh {
		s.cookieRefreshedAt = now
	}
	w.sessions[digest] = s
	w.mu.Unlock()

	if needsRefresh {
		w.setSessionCookie(rw, r, c.Value, now)
		w.persistSessions()
	}
	return digest, true
}

func (w *ColabOneWebConsole) storeSessionLocked(rawToken string, now time.Time) {
	w.pruneExpiredSessionsLocked(now)
	for len(w.sessions) >= maxActiveSessions {
		evictOldestSessionLocked(w.sessions)
	}
	digest := sha256.Sum256([]byte(rawToken))
	w.sessions[digest] = colabOneWebSession{
		createdAt:         now,
		lastSeen:          now,
		expiresAt:         now.Add(SessionTTL),
		cookieRefreshedAt: now,
	}
}

func (w *ColabOneWebConsole) pruneExpiredSessionsLocked(now time.Time) {
	for k, s := range w.sessions {
		if !now.Before(s.expiresAt) {
			delete(w.sessions, k)
		}
	}
}

func evictOldestSessionLocked(sessions map[[32]byte]colabOneWebSession) {
	var oldestKey [32]byte
	var oldest colabOneWebSession
	first := true
	for k, s := range sessions {
		if first || s.lastSeen.Before(oldest.lastSeen) || (s.lastSeen.Equal(oldest.lastSeen) && s.createdAt.Before(oldest.createdAt)) {
			oldestKey = k
			oldest = s
			first = false
		}
	}
	if !first {
		delete(sessions, oldestKey)
	}
}

func (w *ColabOneWebConsole) pruneExpiredTicketsLocked(now time.Time) {
	for k, t := range w.tickets {
		if !now.Before(t.expiresAt) {
			delete(w.tickets, k)
		}
	}
}

func evictOldestTicketLocked(tickets map[[32]byte]colabOneWebTicket) {
	var oldestKey [32]byte
	var oldest colabOneWebTicket
	first := true
	for k, t := range tickets {
		if first || t.createdAt.Before(oldest.createdAt) {
			oldestKey = k
			oldest = t
			first = false
		}
	}
	if !first {
		delete(tickets, oldestKey)
	}
}

func (w *ColabOneWebConsole) clientIPForRateLimit(r *http.Request) string {
	return clientIPForRateLimitWithTrust(r, w.hasPublicOrigin())
}

func clientIPForRateLimitWithTrust(r *http.Request, trustCF bool) string {
	if addr, ok := remoteAddrIP(r); ok {
		if addr.IsLoopback() && trustCF {
			if cfVals := r.Header.Values("CF-Connecting-IP"); len(cfVals) > 0 {
				for i := len(cfVals) - 1; i >= 0; i-- {
					parts := strings.Split(cfVals[i], ",")
					for j := len(parts) - 1; j >= 0; j-- {
						if cfIP := strings.TrimSpace(parts[j]); cfIP != "" {
							if parsedCF, cfErr := netip.ParseAddr(cfIP); cfErr == nil && parsedCF.Zone() == "" {
								return parsedCF.Unmap().String()
							}
							goto fallbackRemoteAddr
						}
					}
				}
			}
		}
	fallbackRemoteAddr:
		return addr.String()
	}
	remoteHost := strings.TrimSpace(r.RemoteAddr)
	if h, _, err := net.SplitHostPort(remoteHost); err == nil {
		remoteHost = h
	}
	remoteHost = strings.Trim(remoteHost, "[]")
	if remoteHost == "" {
		return "unknown"
	}
	return remoteHost
}

func (w *ColabOneWebConsole) checkRateLimitLocked(ip string, now time.Time) int {
	cutoff := now.Add(-loginRateLimitWindow)
	maxSecs := 0

	w.globalLoginFailures = pruneTimestamps(w.globalLoginFailures, cutoff)
	if len(w.globalLoginFailures) >= loginRateLimitGlobalMaxFailures {
		wait := w.globalLoginFailures[0].Add(loginRateLimitWindow).Sub(now)
		secs := int(math.Ceil(wait.Seconds()))
		if secs < 1 {
			secs = 1
		}
		if secs > maxSecs {
			maxSecs = secs
		}
	}

	times := pruneTimestamps(w.loginFailures[ip], cutoff)
	if len(times) == 0 {
		delete(w.loginFailures, ip)
	} else {
		w.loginFailures[ip] = times
		if len(times) >= loginRateLimitMaxFailures {
			wait := times[0].Add(loginRateLimitWindow).Sub(now)
			secs := int(math.Ceil(wait.Seconds()))
			if secs < 1 {
				secs = 1
			}
			if secs > maxSecs {
				maxSecs = secs
			}
		}
	}
	return maxSecs
}

func (w *ColabOneWebConsole) recordLoginFailureLocked(ip string, now time.Time) {
	cutoff := now.Add(-loginRateLimitWindow)
	w.globalLoginFailures = append(pruneTimestamps(w.globalLoginFailures, cutoff), now)
	times := pruneTimestamps(w.loginFailures[ip], cutoff)
	times = append(times, now)
	if len(w.loginFailures) >= maxRateLimitIPs {
		for k, v := range w.loginFailures {
			pruned := pruneTimestamps(v, cutoff)
			if len(pruned) == 0 {
				delete(w.loginFailures, k)
			} else {
				w.loginFailures[k] = pruned
			}
		}
		// If still full, evict an arbitrary stale-oldest entry so map stays bounded.
		if len(w.loginFailures) >= maxRateLimitIPs {
			var oldestIP string
			var oldestTime time.Time
			first := true
			for k, v := range w.loginFailures {
				last := v[len(v)-1]
				if first || last.Before(oldestTime) {
					oldestIP = k
					oldestTime = last
					first = false
				}
			}
			if !first {
				delete(w.loginFailures, oldestIP)
			}
		}
	}
	w.loginFailures[ip] = times
}

func pruneTimestamps(times []time.Time, cutoff time.Time) []time.Time {
	if len(times) == 0 {
		return nil
	}
	dst := times[:0]
	for _, ts := range times {
		if ts.After(cutoff) {
			dst = append(dst, ts)
		}
	}
	return dst
}

func (w *ColabOneWebConsole) setSessionCookie(rw http.ResponseWriter, r *http.Request, token string, now time.Time) {
	http.SetCookie(rw, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  now.Add(SessionTTL),
		MaxAge:   int(SessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   w.isHTTPSRequest(r),
	})
}

func (w *ColabOneWebConsole) clearSessionCookie(rw http.ResponseWriter, r *http.Request) {
	http.SetCookie(rw, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   w.isHTTPSRequest(r),
	})
}

func etagMatches(ifNoneMatch, etag string) bool {
	if strings.TrimSpace(ifNoneMatch) == "" {
		return false
	}
	for _, part := range strings.Split(ifNoneMatch, ",") {
		cand := strings.TrimSpace(part)
		if cand == "*" {
			return true
		}
		cand = strings.TrimPrefix(cand, "W/")
		if cand == etag {
			return true
		}
	}
	return false
}

func acceptsGzip(acceptEncoding string) bool {
	if acceptEncoding == "" {
		return false
	}
	for _, item := range strings.Split(acceptEncoding, ",") {
		parts := strings.Split(strings.TrimSpace(item), ";")
		enc := strings.ToLower(strings.TrimSpace(parts[0]))
		if enc != "gzip" {
			continue
		}
		rejected := false
		for _, param := range parts[1:] {
			kv := strings.SplitN(strings.TrimSpace(param), "=", 2)
			if len(kv) == 2 && strings.EqualFold(strings.TrimSpace(kv[0]), "q") {
				if q, err := strconv.ParseFloat(strings.TrimSpace(kv[1]), 64); err == nil && q <= 0 {
					rejected = true
				}
			}
		}
		if !rejected {
			return true
		}
	}
	return false
}
