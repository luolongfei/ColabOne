package provider

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/luolongfei/ColabOne/console/internal/assignproxy"
	"github.com/luolongfei/ColabOne/console/internal/jupyterws"
)

// ErrProviderClosed is returned when managed kernel or runtime operations are
// attempted after ColabOneProvider.Close has been called.
var ErrProviderClosed = errors.New("远端实例管理通道已关闭")

// Service bundle layout matching common/files.py.
var (
	ColabOneServiceBundleFolders  = []string{"server", "common", "catalog"}
	ColabOneServiceBundleSuffixes = []string{".py", ".json", ".txt"}
)

const (
	// COLABONE_DEFAULT_ASSIGN_MAX_ATTEMPTS is the default total number of GPU allocation attempts
	// (1 initial attempt + 19 retries = 20 total attempts).
	COLABONE_DEFAULT_ASSIGN_MAX_ATTEMPTS = 20

	// COLABONE_ASSIGN_BACKOFF_INITIAL is the initial exponential backoff delay after the first failed attempt.
	COLABONE_ASSIGN_BACKOFF_INITIAL = 2 * time.Second

	// COLABONE_ASSIGN_BACKOFF_MAX is the maximum capped exponential backoff delay (32s).
	COLABONE_ASSIGN_BACKOFF_MAX = 32 * time.Second
)

// AssignRetryDelays builds the capped exponential-backoff retry schedule for maxAttempts total attempts:
// 2s -> 4s -> 8s -> 16s -> 32s, staying at 32s for all subsequent retries.
// Returns an empty slice when maxAttempts <= 1 (single attempt, no retries).
func AssignRetryDelays(maxAttempts int) []time.Duration {
	if maxAttempts <= 1 {
		return []time.Duration{}
	}
	retryCount := maxAttempts - 1
	delays := make([]time.Duration, retryCount)
	delay := COLABONE_ASSIGN_BACKOFF_INITIAL
	for i := 0; i < retryCount; i++ {
		if delay > COLABONE_ASSIGN_BACKOFF_MAX {
			delay = COLABONE_ASSIGN_BACKOFF_MAX
		}
		delays[i] = delay
		if delay < COLABONE_ASSIGN_BACKOFF_MAX {
			delay *= 2
			if delay > COLABONE_ASSIGN_BACKOFF_MAX {
				delay = COLABONE_ASSIGN_BACKOFF_MAX
			}
		}
	}
	return delays
}

// DefaultAssignRetryDelays returns the default 19-retry backoff schedule (20 total attempts:
// 1 initial + 19 retries with 2s -> 4s -> 8s -> 16s -> 32s capped delays).
func DefaultAssignRetryDelays() []time.Duration {
	return AssignRetryDelays(COLABONE_DEFAULT_ASSIGN_MAX_ATTEMPTS)
}

var colabOneAltGPUHints = map[string]string{
	"G4":   "L4 / A100 / H100",
	"H100": "L4 / A100 / G4",
	"A100": "L4 / G4 / H100",
	"L4":   "T4 / A100 / G4",
	"T4":   "L4 / A100",
}

var supportedGPUs = map[string]bool{
	"T4":   true,
	"L4":   true,
	"A100": true,
	"G4":   true,
	"H100": true,
}

var transientWSNeedles = []string{
	"connection was lost",
	"websocket",
	"connection closed",
	"broken pipe",
	"connection reset",
	"remote end closed",
	"unexpected eof",
	"handshake",
	"timed out",
}

func alternativeGPUHint(gpuLabel string) string {
	if hint, ok := colabOneAltGPUHints[strings.ToUpper(strings.TrimSpace(gpuLabel))]; ok {
		return hint
	}
	return "L4 / A100"
}

type kernelKey struct {
	endpoint string
	url      string
	token    string
}

type kernelSlot struct {
	mu       sync.Mutex
	rtMu     sync.Mutex  // leaf lock protecting runtime pointer for race-free Abort()
	clearing atomic.Bool // set under ColabOneProvider.kernelMu when detached by Clear(); Dekker-paired with setRuntime/getRuntime
	evicted  bool        // protected by mu; true once removed from ColabOneProvider.kernels
	key      kernelKey
	runtime  KernelClient
}

func (s *kernelSlot) getRuntime() KernelClient {
	s.rtMu.Lock()
	defer s.rtMu.Unlock()
	return s.runtime
}

func (s *kernelSlot) setRuntime(rt KernelClient) {
	s.rtMu.Lock()
	s.runtime = rt
	s.rtMu.Unlock()
}

// ColabOneProvider manages Google Colab GPU allocation, lifecycle, kernel RPC,
// multi-GPU vault state, remote service deployment/upgrade, external read-only
// telemetry observation, and Colab DriveFS mounting.
// Only sessions stored in this profile's vault are operated on automatically.
type ColabOneProvider struct {
	auth                AuthProvider
	vault               VaultStore
	rootDir             string
	clientFactory       ClientFactory
	contents            ContentsClient
	kernelFactory       KernelFactory
	driveMounterFactory DriveMounterFactory
	bundler             ServiceBundler
	location            LocationInferer
	safeMessageFn       SafeMessageFn
	httpClient          *http.Client
	vmBootScript        string
	vmUpdateScript      string
	parseAssignProxyFn  func(string) (*ColabOneAssignProxySpec, error)
	formatProxyErrorFn  func(error) string

	assignRetryDelays   []time.Duration
	releasePollInterval time.Duration
	readyTimeout        time.Duration
	readyPollInterval   time.Duration
	readyLogInterval    time.Duration
	uploadRetryDelay    time.Duration
	nowFn               func() time.Time
	sleepFn             func(ctx context.Context, d time.Duration) error

	stateMu   sync.Mutex
	locProbed map[string]bool

	closed     atomic.Bool
	closedOnce sync.Once
	closedCh   chan struct{}

	kernelMu sync.Mutex
	kernels  map[string]*kernelSlot

	observerExecMu          sync.Mutex
	observerMu              sync.Mutex
	observer                *observerSlot
	observerGen             uint64
	observerPendingEndpoint string

	proxyMu         sync.Mutex
	externalProxies map[string]cachedExternalProxy
}

func colabOneProviderNoRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// NewColabOneProvider constructs a ColabOneProvider with safe defaults for optional fields.
func NewColabOneProvider(cfg ColabOneProviderConfig) *ColabOneProvider {
	delays := cfg.AssignRetryDelays
	if delays == nil {
		delays = DefaultAssignRetryDelays()
	} else {
		cp := make([]time.Duration, len(delays))
		copy(cp, delays)
		delays = cp
	}

	var httpClient *http.Client
	if cfg.HTTPClient == nil {
		var transport http.RoundTripper
		if def, ok := http.DefaultTransport.(*http.Transport); ok {
			cloned := def.Clone()
			cloned.ResponseHeaderTimeout = 30 * time.Second
			transport = cloned
		} else {
			transport = http.DefaultTransport
		}
		httpClient = &http.Client{
			Transport:     transport,
			CheckRedirect: colabOneProviderNoRedirect,
		}
	} else if cfg.HTTPClient.CheckRedirect == nil {
		cloned := *cfg.HTTPClient
		cloned.CheckRedirect = colabOneProviderNoRedirect
		httpClient = &cloned
	} else {
		httpClient = cfg.HTTPClient
	}

	contents := cfg.Contents
	if contents == nil {
		contents = &defaultContentsClient{httpClient: httpClient}
	}

	releasePoll := cfg.ReleasePollInterval
	if releasePoll <= 0 {
		releasePoll = 2 * time.Second
	}
	readyTimeout := cfg.ReadyTimeout
	if readyTimeout <= 0 {
		readyTimeout = 90 * time.Second
	}
	readyPoll := cfg.ReadyPollInterval
	if readyPoll <= 0 {
		readyPoll = 2 * time.Second
	}
	readyLog := cfg.ReadyLogInterval
	if readyLog <= 0 {
		readyLog = 8 * time.Second
	}
	uploadRetry := cfg.UploadRetryDelay
	if uploadRetry <= 0 {
		uploadRetry = 1 * time.Second
	}
	nowFn := cfg.NowFn
	if nowFn == nil {
		nowFn = time.Now
	}
	parseProxy := cfg.ParseAssignProxyFn
	if parseProxy == nil {
		parseProxy = ParseAssignProxy
	}
	formatProxy := cfg.FormatProxyErrorFn
	if formatProxy == nil {
		formatProxy = FormatProxyError
	}

	return &ColabOneProvider{
		auth:                cfg.Auth,
		vault:               cfg.Vault,
		rootDir:             cfg.RootDir,
		clientFactory:       cfg.ClientFactory,
		contents:            contents,
		kernelFactory:       cfg.KernelFactory,
		driveMounterFactory: cfg.DriveMounterFactory,
		bundler:             cfg.Bundler,
		location:            cfg.Location,
		safeMessageFn:       cfg.SafeMessage,
		httpClient:          httpClient,
		vmBootScript:        cfg.VMBootScript,
		vmUpdateScript:      cfg.VMUpdateScript,
		parseAssignProxyFn:  parseProxy,
		formatProxyErrorFn:  formatProxy,
		assignRetryDelays:   delays,
		releasePollInterval: releasePoll,
		readyTimeout:        readyTimeout,
		readyPollInterval:   readyPoll,
		readyLogInterval:    readyLog,
		uploadRetryDelay:    uploadRetry,
		nowFn:               nowFn,
		sleepFn:             cfg.SleepFn,
		locProbed:           make(map[string]bool),
		closedCh:            make(chan struct{}),
		kernels:             make(map[string]*kernelSlot),
		externalProxies:     make(map[string]cachedExternalProxy),
	}
}

func (p *ColabOneProvider) sleep(ctx context.Context, d time.Duration) error {
	if p.sleepFn != nil {
		return p.sleepFn(ctx, d)
	}
	return defaultSleep(ctx, d)
}

func defaultSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
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

func (p *ColabOneProvider) now() time.Time {
	return p.nowFn()
}

func (p *ColabOneProvider) safeMessage(raw string) string {
	if p.safeMessageFn != nil {
		return p.safeMessageFn(raw)
	}
	return raw
}

func (p *ColabOneProvider) regularClient() (ColabClient, error) {
	if p.clientFactory == nil {
		return nil, errors.New("未配置 Google Colab 客户端工厂")
	}
	return p.clientFactory("")
}

func (p *ColabOneProvider) locationFromZone(zone string) map[string]any {
	if p.location == nil || strings.TrimSpace(zone) == "" {
		return nil
	}
	loc := p.location.LocationFromZone(zone)
	if len(loc) == 0 {
		return nil
	}
	return cloneMap(loc)
}

func (p *ColabOneProvider) inferLocationFromEndpoint(ep, rawURL string) map[string]any {
	if p.location == nil {
		return nil
	}
	loc := p.location.InferLocationFromEndpoint(ep, rawURL)
	if len(loc) == 0 {
		return nil
	}
	return cloneMap(loc)
}

func (p *ColabOneProvider) normalizeGeoPayload(country, region, city, zone string) map[string]any {
	if p.location == nil {
		return nil
	}
	loc := p.location.NormalizeGeoPayload(country, region, city, zone)
	if len(loc) == 0 {
		return nil
	}
	return cloneMap(loc)
}

func emitLog(onLog LogFn, message, source, level string) {
	if onLog == nil || message == "" {
		return
	}
	if source == "" {
		source = "GPU"
	}
	if level == "" {
		level = "info"
	}
	onLog(message, source, level)
}

func emitStage(notify StageFn, stage string) {
	if notify == nil || stage == "" {
		return
	}
	notify(stage)
}

// ---------------------------------------------------------------------------
// Multi-GPU vault session & remote persistence
// ---------------------------------------------------------------------------

func (p *ColabOneProvider) vaultGetString(key string) string {
	if p.vault == nil {
		return ""
	}
	var val string
	if ok, err := p.vault.GetJSON(key, &val); err == nil && ok {
		return val
	}
	return ""
}

func (p *ColabOneProvider) vaultGetStringSlice(key string) []string {
	if p.vault == nil {
		return nil
	}
	var vals []string
	if ok, err := p.vault.GetJSON(key, &vals); err == nil && ok {
		return vals
	}
	return nil
}

func (p *ColabOneProvider) hasColabAuth() bool {
	if p.vault == nil || !p.vault.Has("colab") {
		return false
	}
	var raw any
	ok, err := p.vault.GetJSON("colab", &raw)
	if err != nil || !ok || raw == nil {
		return false
	}
	switch v := raw.(type) {
	case map[string]any:
		return len(v) > 0
	case string:
		return strings.TrimSpace(v) != ""
	case bool:
		return v
	default:
		return true
	}
}

func (p *ColabOneProvider) sessionsOrderedLocked() ([]string, map[string]ColabOneSessionState) {
	order := make([]string, 0)
	sessions := make(map[string]ColabOneSessionState)
	if p.vault == nil {
		return order, sessions
	}

	addIfMissing := func(item ColabOneSessionState) {
		if item.Endpoint == "" {
			return
		}
		item.applyDefaults()
		if _, exists := sessions[item.Endpoint]; !exists {
			order = append(order, item.Endpoint)
			sessions[item.Endpoint] = item
		}
	}

	ids := p.vaultGetStringSlice("runtime_session_ids")
	for _, ep := range ids {
		if ep == "" {
			continue
		}
		var item ColabOneSessionState
		if ok, err := p.vault.GetJSON("runtime_session:"+ep, &item); err == nil && ok && item.Endpoint != "" {
			item.applyDefaults()
			if _, exists := sessions[item.Endpoint]; !exists {
				order = append(order, item.Endpoint)
			}
			sessions[item.Endpoint] = item
		}
	}

	var rawMap map[string]ColabOneSessionState
	if ok, err := p.vault.GetJSON("runtime_sessions", &rawMap); err == nil && ok && len(rawMap) > 0 {
		keys := make([]string, 0, len(rawMap))
		for k := range rawMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			addIfMissing(rawMap[k])
		}
	}

	var legacy ColabOneSessionState
	if ok, err := p.vault.GetJSON("runtime_session", &legacy); err == nil && ok && legacy.Endpoint != "" {
		addIfMissing(legacy)
	}

	return order, sessions
}

func (p *ColabOneProvider) remotesOrderedLocked() ([]string, map[string]map[string]any) {
	order := make([]string, 0)
	remotes := make(map[string]map[string]any)
	if p.vault == nil {
		return order, remotes
	}

	addIfMissing := func(item map[string]any) {
		ep := strVal(item["endpoint"])
		if ep == "" {
			return
		}
		if _, exists := remotes[ep]; !exists {
			order = append(order, ep)
			remotes[ep] = item
		}
	}

	ids := p.vaultGetStringSlice("remote_ids")
	for _, ep := range ids {
		if ep == "" {
			continue
		}
		var item map[string]any
		if ok, err := p.vault.GetJSON("remote:"+ep, &item); err == nil && ok && strVal(item["endpoint"]) != "" {
			itemEP := strVal(item["endpoint"])
			if _, exists := remotes[itemEP]; !exists {
				order = append(order, itemEP)
			}
			remotes[itemEP] = item
		}
	}

	var rawMap map[string]map[string]any
	if ok, err := p.vault.GetJSON("remotes", &rawMap); err == nil && ok && len(rawMap) > 0 {
		keys := make([]string, 0, len(rawMap))
		for k := range rawMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if rawMap[k] != nil {
				addIfMissing(rawMap[k])
			}
		}
	}

	var legacy map[string]any
	if ok, err := p.vault.GetJSON("remote", &legacy); err == nil && ok && strVal(legacy["endpoint"]) != "" {
		addIfMissing(legacy)
	}

	return order, remotes
}

func (p *ColabOneProvider) activeEndpointLocked(order []string, sessions map[string]ColabOneSessionState) string {
	if len(sessions) == 0 {
		return ""
	}
	preferred := p.vaultGetString("active_endpoint")
	if preferred != "" {
		if _, ok := sessions[preferred]; ok {
			return preferred
		}
	}
	var legacy ColabOneSessionState
	if p.vault != nil {
		if ok, err := p.vault.GetJSON("runtime_session", &legacy); err == nil && ok && legacy.Endpoint != "" {
			if _, exists := sessions[legacy.Endpoint]; exists {
				return legacy.Endpoint
			}
		}
	}
	if len(order) > 0 {
		return order[0]
	}
	return ""
}

func (p *ColabOneProvider) writeStateLocked(
	sessionOrder []string,
	sessions map[string]ColabOneSessionState,
	remoteOrder []string,
	remotes map[string]map[string]any,
	activeEP string,
) error {
	if p.vault == nil {
		return nil
	}
	prevSessionIDs := p.vaultGetStringSlice("runtime_session_ids")
	prevRemoteIDs := p.vaultGetStringSlice("remote_ids")

	// Store each session and remote under its own per-endpoint key so
	// OS credential store per-entry size limits are never exceeded when
	// running multiple concurrent GPUs.
	chosen := ""
	if len(sessions) > 0 {
		cleanOrder := normalizeOrder(sessionOrder, sessions)
		if activeEP != "" {
			if _, ok := sessions[activeEP]; ok {
				chosen = activeEP
			}
		}
		if chosen == "" {
			if stored := p.vaultGetString("active_endpoint"); stored != "" {
				if _, ok := sessions[stored]; ok {
					chosen = stored
				}
			}
		}
		if chosen == "" && len(cleanOrder) > 0 {
			chosen = cleanOrder[0]
		}
		for _, ep := range cleanOrder {
			if err := p.vault.Set("runtime_session:"+ep, sessions[ep]); err != nil {
				return err
			}
		}
		if err := p.vault.Set("runtime_session_ids", cleanOrder); err != nil {
			return err
		}
		if err := p.vault.Set("active_endpoint", chosen); err != nil {
			return err
		}
		if err := p.vault.Set("runtime_session", sessions[chosen]); err != nil {
			return err
		}
		_ = p.vault.Delete("runtime_sessions")
	} else {
		_ = p.vault.Delete("runtime_session_ids")
		_ = p.vault.Delete("runtime_sessions")
		_ = p.vault.Delete("active_endpoint")
		_ = p.vault.Delete("runtime_session")
	}
	for _, oldEP := range prevSessionIDs {
		if oldEP == "" {
			continue
		}
		if _, ok := sessions[oldEP]; !ok {
			_ = p.vault.Delete("runtime_session:" + oldEP)
		}
	}

	if len(remotes) > 0 {
		cleanRemOrder := normalizeOrderMap(remoteOrder, remotes)
		for _, ep := range cleanRemOrder {
			if err := p.vault.Set("remote:"+ep, remotes[ep]); err != nil {
				return err
			}
		}
		if err := p.vault.Set("remote_ids", cleanRemOrder); err != nil {
			return err
		}
		if chosen != "" {
			if rem, ok := remotes[chosen]; ok {
				if err := p.vault.Set("remote", rem); err != nil {
					return err
				}
			} else {
				_ = p.vault.Delete("remote")
			}
		} else {
			_ = p.vault.Delete("remote")
		}
		_ = p.vault.Delete("remotes")
	} else {
		_ = p.vault.Delete("remote_ids")
		_ = p.vault.Delete("remotes")
		_ = p.vault.Delete("remote")
	}
	for _, oldEP := range prevRemoteIDs {
		if oldEP == "" {
			continue
		}
		if _, ok := remotes[oldEP]; !ok {
			_ = p.vault.Delete("remote:" + oldEP)
		}
	}
	return nil
}

func normalizeOrder[T any](order []string, m map[string]T) []string {
	seen := make(map[string]bool, len(m))
	res := make([]string, 0, len(m))
	for _, ep := range order {
		if ep != "" && !seen[ep] {
			if _, ok := m[ep]; ok {
				seen[ep] = true
				res = append(res, ep)
			}
		}
	}
	if len(res) < len(m) {
		extra := make([]string, 0, len(m)-len(res))
		for ep := range m {
			if !seen[ep] {
				extra = append(extra, ep)
			}
		}
		sort.Strings(extra)
		res = append(res, extra...)
	}
	return res
}

func normalizeOrderMap(order []string, m map[string]map[string]any) []string {
	return normalizeOrder(order, m)
}

// SessionsMap returns a snapshot copy of all managed runtime sessions keyed by endpoint.
func (p *ColabOneProvider) SessionsMap() map[string]ColabOneSessionState {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	_, sessions := p.sessionsOrderedLocked()
	out := make(map[string]ColabOneSessionState, len(sessions))
	for ep, s := range sessions {
		out[ep] = s.clone()
	}
	return out
}

// RemotesMap returns a snapshot copy of all stored remote control metadata keyed by endpoint.
func (p *ColabOneProvider) RemotesMap() map[string]map[string]any {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	_, remotes := p.remotesOrderedLocked()
	out := make(map[string]map[string]any, len(remotes))
	for ep, r := range remotes {
		out[ep] = cloneMap(r)
	}
	return out
}

// ManagedEndpoints returns all managed endpoints in deterministic insertion order.
func (p *ColabOneProvider) ManagedEndpoints() []string {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	order, _ := p.sessionsOrderedLocked()
	out := make([]string, len(order))
	copy(out, order)
	return out
}

// ActiveEndpoint returns the currently active managed endpoint, or "" when none exists.
func (p *ColabOneProvider) ActiveEndpoint() string {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	order, sessions := p.sessionsOrderedLocked()
	return p.activeEndpointLocked(order, sessions)
}

// SetActive switches the active managed endpoint.
func (p *ColabOneProvider) SetActive(endpoint string) (string, error) {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	sOrder, sessions := p.sessionsOrderedLocked()
	if _, ok := sessions[endpoint]; !ok {
		return "", errors.New("所选实例不存在或已释放")
	}
	rOrder, remotes := p.remotesOrderedLocked()
	if err := p.writeStateLocked(sOrder, sessions, rOrder, remotes, endpoint); err != nil {
		return "", err
	}
	return endpoint, nil
}

// RemoteInfo returns stored remote metadata for endpoint (or active endpoint if empty).
func (p *ColabOneProvider) RemoteInfo(endpoint string) map[string]any {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	target := endpoint
	if target == "" {
		order, sessions := p.sessionsOrderedLocked()
		target = p.activeEndpointLocked(order, sessions)
	}
	if target == "" {
		return nil
	}
	_, remotes := p.remotesOrderedLocked()
	rem, ok := remotes[target]
	if !ok {
		return nil
	}
	return cloneMap(rem)
}

// SaveRemote persists remote control metadata for a managed endpoint.
func (p *ColabOneProvider) SaveRemote(remote map[string]any, activate bool) error {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	sOrder, sessions := p.sessionsOrderedLocked()
	rOrder, remotes := p.remotesOrderedLocked()
	ep := strVal(remote["endpoint"])
	if ep == "" {
		return errors.New("缺少远端实例标识")
	}
	if !activate {
		if _, ok := sessions[ep]; !ok {
			return nil
		}
	}
	if _, existed := remotes[ep]; !existed {
		rOrder = append(rOrder, ep)
	}
	remotes[ep] = cloneMap(remote)
	activeEP := p.activeEndpointLocked(sOrder, sessions)
	if activate {
		activeEP = ep
	}
	return p.writeStateLocked(sOrder, sessions, rOrder, remotes, activeEP)
}

// SaveSession persists a managed runtime session in the vault.
func (p *ColabOneProvider) SaveSession(session *ColabOneSessionState, activate bool) error {
	if session == nil || session.Endpoint == "" {
		return errors.New("缺少远端实例标识")
	}
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	return p.saveSessionLocked(session, activate)
}

func (p *ColabOneProvider) saveSessionLocked(session *ColabOneSessionState, activate bool) error {
	sOrder, sessions := p.sessionsOrderedLocked()
	rOrder, remotes := p.remotesOrderedLocked()
	cp := session.clone()
	cp.applyDefaults()
	if _, existed := sessions[cp.Endpoint]; !existed {
		sOrder = append(sOrder, cp.Endpoint)
	}
	sessions[cp.Endpoint] = cp
	chosen := p.activeEndpointLocked(sOrder, sessions)
	if activate {
		chosen = cp.Endpoint
	}
	if chosen == "" {
		chosen = cp.Endpoint
	}
	return p.writeStateLocked(sOrder, sessions, rOrder, remotes, chosen)
}

// ClearOrphanedRemotes atomically removes stale remote metadata keys (remote,
// remotes, remote_ids, and remote:<ep>) when no managed sessions exist,
// without ever touching live sessions or active kernel channels.
func (p *ColabOneProvider) ClearOrphanedRemotes() {
	if p == nil || p.vault == nil {
		return
	}
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	_, sessions := p.sessionsOrderedLocked()
	if len(sessions) > 0 {
		return
	}
	seen := make(map[string]struct{})
	for _, ep := range p.vaultGetStringSlice("remote_ids") {
		if ep != "" {
			seen[ep] = struct{}{}
		}
	}
	_, remotes := p.remotesOrderedLocked()
	for ep := range remotes {
		if ep != "" {
			seen[ep] = struct{}{}
		}
	}
	for ep := range seen {
		_ = p.vault.Delete("remote:" + ep)
	}
	_ = p.vault.Delete("remote_ids")
	_ = p.vault.Delete("remotes")
	_ = p.vault.Delete("remote")
}

// Clear removes a managed endpoint (or active endpoint if empty) from local vault state,
// and closes/evicts the corresponding kernel channel(s) after releasing stateMu.
// When endpoint is empty and no managed session exists, also cleans up any stale remote entries
// and closes all active kernel channels.
func (p *ColabOneProvider) Clear(endpoint string) error {
	return p.clear(endpoint, nil)
}

func (p *ColabOneProvider) clear(endpoint string, heldSlot *kernelSlot) error {
	p.stateMu.Lock()
	sOrder, sessions := p.sessionsOrderedLocked()
	rOrder, remotes := p.remotesOrderedLocked()
	target := endpoint
	if target == "" {
		target = p.activeEndpointLocked(sOrder, sessions)
		if target == "" {
			clear(p.locProbed)
			err := p.writeStateLocked(nil, map[string]ColabOneSessionState{}, nil, map[string]map[string]any{}, "")
			p.stateMu.Unlock()
			if err != nil {
				return err
			}
			p.clearKernelChannels("", heldSlot)
			return nil
		}
	}
	delete(p.locProbed, target)
	_, inSessions := sessions[target]
	_, inRemotes := remotes[target]
	if !inSessions && !inRemotes {
		p.stateMu.Unlock()
		p.clearKernelChannels(target, heldSlot)
		return nil
	}
	delete(sessions, target)
	delete(remotes, target)
	sOrder = removeString(sOrder, target)
	rOrder = removeString(rOrder, target)

	nextActive := ""
	if stored := p.vaultGetString("active_endpoint"); stored != "" {
		if _, ok := sessions[stored]; ok {
			nextActive = stored
		}
	}
	if nextActive == "" && len(sOrder) > 0 {
		nextActive = sOrder[0]
	}
	err := p.writeStateLocked(sOrder, sessions, rOrder, remotes, nextActive)
	p.stateMu.Unlock()
	if err != nil {
		return err
	}
	p.clearKernelChannels(target, heldSlot)
	return nil
}

func removeString(slice []string, target string) []string {
	out := make([]string, 0, len(slice))
	for _, s := range slice {
		if s != target {
			out = append(out, s)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Account & Assignment Description
// ---------------------------------------------------------------------------

// DescribeAssignment formats a Colab assignment into the canonical frontend instance descriptor.
func DescribeAssignment(a ListedAssignment, managedSet map[string]bool, activeEP string, locInferer LocationInferer) map[string]any {
	endpoint := a.Endpoint

	variant := ""
	if strings.TrimSpace(a.Variant) != "" {
		vUpper := strings.ToUpper(strings.TrimSpace(a.Variant))
		switch {
		case strings.Contains(vUpper, "GPU") || vUpper == "1":
			variant = "GPU"
		case strings.Contains(vUpper, "TPU") || vUpper == "2":
			variant = "TPU"
		default:
			variant = "CPU"
		}
	}

	accel := strings.ToUpper(strings.TrimSpace(a.Accelerator))
	if accel == "" || accel == "NONE" {
		if variant == "CPU" {
			accel = "CPU"
		} else {
			accel = variant
		}
	}

	shape := ""
	if strings.TrimSpace(a.MachineShape) != "" {
		sUpper := strings.ToUpper(strings.TrimSpace(a.MachineShape))
		if strings.Contains(sUpper, "HIGH") || sUpper == "1" {
			shape = "HIGH_RAM"
		} else {
			shape = "STANDARD"
		}
	}

	proxyURL := ""
	if a.RuntimeProxyInfo != nil {
		proxyURL = a.RuntimeProxyInfo.URL
	}

	info := map[string]any{
		"id":          endpoint,
		"managed":     managedSet[endpoint],
		"active":      activeEP != "" && endpoint == activeEP,
		"accelerator": accel,
		"variant":     variant,
		"shape":       shape,
	}
	if locInferer != nil {
		if loc := locInferer.InferLocationFromEndpoint(endpoint, proxyURL); len(loc) > 0 {
			info["location"] = cloneMap(loc)
		}
	}
	return info
}

// Account queries Google Colab compute unit balance, hourly rate, and active assignments,
// caching external runtime proxies for read-only observation.
func (p *ColabOneProvider) Account(ctx context.Context) (map[string]any, error) {
	if !p.hasColabAuth() {
		return map[string]any{"logged_in": false}, nil
	}
	client, err := p.regularClient()
	if err != nil {
		return nil, err
	}
	info, err := client.GetConsumptionUserInfo(ctx)
	if err != nil {
		return nil, err
	}
	managedSlice := p.ManagedEndpoints()
	managedSet := make(map[string]bool, len(managedSlice))
	for _, ep := range managedSlice {
		managedSet[ep] = true
	}
	activeEP := p.ActiveEndpoint()
	assignments, err := client.ListAssignments(ctx)
	if err != nil {
		return nil, err
	}
	p.cacheAssignmentProxies(assignments)

	instances := make([]map[string]any, 0, len(assignments))
	for _, a := range assignments {
		instances = append(instances, DescribeAssignment(a, managedSet, activeEP, p.location))
	}

	email := ""
	if p.auth != nil {
		if profile, err := p.auth.Profile(ctx); err == nil && profile != nil {
			email = profile["email"]
		}
	}

	var balance, rate float64
	if info != nil {
		balance = sanitizeFloat(info.PaidComputeUnitsBalance)
		rate = sanitizeFloat(info.ConsumptionRateHourly)
	}

	return map[string]any{
		"logged_in":   true,
		"email":       email,
		"balance":     balance,
		"rate":        rate,
		"assignments": len(instances),
		"instances":   instances,
		"checked_at":  unixFloat(p.now()),
	}, nil
}

// ---------------------------------------------------------------------------
// Session lookup & proxy token rotation
// ---------------------------------------------------------------------------

// Session retrieves the stored ColabOneSessionState for endpoint (or active endpoint if empty).
// When refresh is true, validates that the endpoint is still assigned on Colab (clearing it if gone)
// and rotates its proxy token when forceRotate is set or <= 600s remain before expiry.
func (p *ColabOneProvider) Session(ctx context.Context, refresh bool, endpoint string, forceRotate bool) (*ColabOneSessionState, error) {
	return p.session(ctx, refresh, endpoint, forceRotate, nil)
}

func (p *ColabOneProvider) session(ctx context.Context, refresh bool, endpoint string, forceRotate bool, heldSlot *kernelSlot) (*ColabOneSessionState, error) {
	p.stateMu.Lock()
	order, sessions := p.sessionsOrderedLocked()
	target := endpoint
	if target == "" {
		target = p.activeEndpointLocked(order, sessions)
	}
	var session ColabOneSessionState
	found := false
	if target != "" {
		if s, ok := sessions[target]; ok {
			session = s.clone()
			found = true
		}
	}
	p.stateMu.Unlock()

	if !found {
		return nil, nil
	}

	if refresh {
		client, err := p.regularClient()
		if err != nil {
			return nil, err
		}
		assignments, err := client.ListAssignments(ctx)
		if err != nil {
			return nil, err
		}
		var matched *ListedAssignment
		for i := range assignments {
			if assignments[i].Endpoint == session.Endpoint {
				matched = &assignments[i]
				break
			}
		}
		if matched == nil {
			_ = p.clear(session.Endpoint, heldSlot)
			return nil, nil
		}

		info := matched.RuntimeProxyInfo
		now := p.now()
		p.stateMu.Lock()
		_, curSessions := p.sessionsOrderedLocked()
		current, ok := curSessions[session.Endpoint]
		if !ok || current.Endpoint != session.Endpoint {
			p.stateMu.Unlock()
			// A concurrent release must not be undone by this read.
			return nil, nil
		}
		current = current.clone()
		needsRotate := forceRotate ||
			current.Token == "" ||
			(info != nil && current.URL != info.URL) ||
			current.TokenExpiresAt == nil ||
			!current.TokenExpiresAt.After(now.Add(600*time.Second))
		if needsRotate && info != nil {
			current.URL = info.URL
			current.Token = info.Token
			exp := info.ResolveExpiresAt(now)
			if !exp.IsZero() {
				current.TokenExpiresAt = &exp
			} else {
				current.TokenExpiresAt = nil
			}
			if err := p.saveSessionLocked(&current, false); err != nil {
				p.stateMu.Unlock()
				return nil, err
			}
		}
		p.stateMu.Unlock()
		session = current
	}

	return &session, nil
}

// ---------------------------------------------------------------------------
// Allocation & Retry Backoff
// ---------------------------------------------------------------------------

func isCancelled(ctx context.Context, cancelCh <-chan struct{}) bool {
	if cancelCh != nil {
		select {
		case <-cancelCh:
			return true
		default:
		}
	}
	if ctx != nil && errors.Is(ctx.Err(), context.Canceled) {
		return true
	}
	return false
}

// waitForRetry waits up to delay, waking immediately on cancellation.
// Returns "cancel" or "timeout".
func (p *ColabOneProvider) waitForRetry(
	ctx context.Context,
	delay time.Duration,
	cancelCh <-chan struct{},
) string {
	if delay <= 0 {
		if isCancelled(ctx, cancelCh) {
			return "cancel"
		}
		return "timeout"
	}
	if isCancelled(ctx, cancelCh) {
		return "cancel"
	}

	// Allow deterministic SleepFn tests; fast-path direct call when no cancel select is needed.
	if p.sleepFn != nil {
		if cancelCh == nil && ctx.Done() == nil {
			if err := p.sleepFn(ctx, delay); errors.Is(err, context.Canceled) {
				return "cancel"
			}
			return "timeout"
		}
		sleepCtx, cancelSleep := context.WithCancel(ctx)
		defer cancelSleep()
		doneCh := make(chan error, 1)
		go func() {
			doneCh <- p.sleepFn(sleepCtx, delay)
		}()
		select {
		case <-cancelCh:
			cancelSleep()
			<-doneCh
			return "cancel"
		case <-ctx.Done():
			cancelSleep()
			<-doneCh
			if errors.Is(ctx.Err(), context.Canceled) {
				return "cancel"
			}
			return "timeout"
		case err := <-doneCh:
			if isCancelled(ctx, cancelCh) || errors.Is(err, context.Canceled) {
				return "cancel"
			}
			return "timeout"
		}
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-cancelCh:
		return "cancel"
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.Canceled) {
			return "cancel"
		}
		return "timeout"
	case <-timer.C:
		return "timeout"
	}
}

func (p *ColabOneProvider) formatAssignError(gpuLabel string, err error, retryCount int, usingAssignProxy bool) string {
	var tma *TooManyAssignmentsError
	if errors.As(err, &tma) || errors.Is(err, ErrTooManyAssignments) {
		return fmt.Sprintf("Google Colab 拒绝分配 %s GPU（HTTP 412）：当前账号活跃实例已达上限或受配额限制，请先释放已有实例或切换其他 GPU 型号", gpuLabel)
	}
	if usingAssignProxy && isAssignProxyFastFailError(err) {
		return p.formatProxyErrorFn(err)
	}
	status := httpStatusCode(err)
	if status == 0 && isTimeoutError(err) {
		if retryCount > 0 {
			return fmt.Sprintf("Google Colab 分配 %s GPU 请求超时，已自动重试 %d 次仍未成功，请稍后重试或切换其他 GPU 型号", gpuLabel, retryCount)
		}
		return fmt.Sprintf("Google Colab 分配 %s GPU 请求超时，请稍后重试或切换其他 GPU 型号", gpuLabel)
	}
	if status == 0 && isNetworkOrTransportError(err) {
		if retryCount > 0 {
			return fmt.Sprintf("Google Colab 分配 %s GPU 网络连接中断，已自动重试 %d 次仍未成功，请检查网络或代理后重试", gpuLabel, retryCount)
		}
		return fmt.Sprintf("Google Colab 分配 %s GPU 失败，请检查网络连接后重试", gpuLabel)
	}
	if status > 0 || isColabRequestError(err) {
		switch status {
		case 412:
			return fmt.Sprintf("Google Colab 拒绝分配 %s GPU（HTTP 412）：当前账号活跃实例已达上限或受配额限制，请先释放已有实例或切换其他 GPU 型号", gpuLabel)
		case 400:
			return fmt.Sprintf("Google Colab 拒绝分配 %s GPU（HTTP 400）：当前账号可能暂无该型号配额或权限，请切换其他 GPU 型号", gpuLabel)
		case 401, 403:
			return fmt.Sprintf("Google Colab 授权校验失败（HTTP %d），请在设置中重新登录后重试", status)
		case 429:
			return "Google Colab 分配请求过于频繁（HTTP 429），请稍等片刻后重试"
		case 503:
			if retryCount > 0 {
				return fmt.Sprintf("Google Colab 当前 %s 算力紧张，暂未分配出 GPU（HTTP 503），已自动重试 %d 次仍未排到可用资源，请稍后重试或切换其他 GPU 型号", gpuLabel, retryCount)
			}
			return fmt.Sprintf("Google Colab 当前 %s 算力紧张，暂未分配出 GPU（HTTP 503），请稍后重试或切换其他 GPU 型号", gpuLabel)
		case 502, 504:
			if retryCount > 0 {
				return fmt.Sprintf("Google Colab 当前 %s 分配服务繁忙或超时，暂未分配出 GPU（HTTP %d），已自动重试 %d 次仍未成功，请稍后重试或切换其他 GPU 型号", gpuLabel, status, retryCount)
			}
			return fmt.Sprintf("Google Colab 当前 %s 分配服务繁忙或超时，暂未分配出 GPU（HTTP %d），请稍后重试或切换其他 GPU 型号", gpuLabel, status)
		default:
			if status > 0 {
				return fmt.Sprintf("Google Colab 分配 %s GPU 失败（HTTP %d），请稍后重试", gpuLabel, status)
			}
			return fmt.Sprintf("Google Colab 分配 %s GPU 失败，请检查网络连接后重试", gpuLabel)
		}
	}
	return truncateRunes(p.safeMessage(err.Error()), 300)
}

func (p *ColabOneProvider) assignRuntime(
	ctx context.Context,
	client ColabClient,
	accel string,
	shape string,
	opts AllocateOptions,
	retriesUsed *int,
	usingAssignProxy bool,
) (*Assignment, error) {
	nbh := newUUIDv4()
	delays := p.assignRetryDelays
	if opts.MaxAttempts > 0 {
		delays = AssignRetryDelays(opts.MaxAttempts)
	}
	totalAttempts := 1 + len(delays)
	xsrfToken := ""
	consecutivePoolBusy := 0
	poolBusyHintEmitted := false
	lastRetryKind := ""

	if ctx == nil {
		ctx = context.Background()
	}
	assignCtx, cancelAssign := context.WithCancel(ctx)
	defer cancelAssign()
	if opts.CancelCh != nil || p.closedCh != nil {
		go func() {
			select {
			case <-opts.CancelCh:
				cancelAssign()
			case <-p.closedCh:
				cancelAssign()
			case <-assignCtx.Done():
			}
		}()
	}

	for attempt := 0; attempt < totalAttempts; attempt++ {
		if p.closed.Load() {
			return nil, ErrProviderClosed
		}
		if isCancelled(ctx, opts.CancelCh) {
			return nil, ErrAllocationCancelled
		}
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if opts.OnRetryState != nil {
			opts.OnRetryState(map[string]any{
				"waiting_retry":  false,
				"retry_at":       0.0,
				"attempt":        attempt + 1,
				"next_attempt":   attempt + 1,
				"total_attempts": totalAttempts,
				"retry_kind":     lastRetryKind,
				"gpu":            accel,
			})
		}
		if attempt > 0 && opts.OnStage != nil {
			switch lastRetryKind {
			case "capacity":
				emitStage(opts.OnStage, fmt.Sprintf("Google 算力紧张，正在排队分配 %s（第 %d/%d 次）", accel, attempt+1, totalAttempts))
			case "server_busy":
				emitStage(opts.OnStage, fmt.Sprintf("Google 分配服务繁忙，正在重试分配 %s（第 %d/%d 次）", accel, attempt+1, totalAttempts))
			default:
				emitStage(opts.OnStage, fmt.Sprintf("正在分配 %s（第 %d/%d 次）", accel, attempt+1, totalAttempts))
			}
		}

		attemptStarted := p.now()
		var assigned *Assignment
		var err error
		if xsrfToken == "" {
			var existing *Assignment
			existing, xsrfToken, err = client.GetAssignment(assignCtx, nbh, "GPU", accel, shape)
			if err == nil && existing != nil {
				if attempt > 0 {
					emitLog(opts.OnLog, fmt.Sprintf("已通过状态核验确认 %s GPU 实例分配成功", accel), "GPU", "info")
				}
				return existing, nil
			}
		}
		if err == nil {
			assigned, err = client.PostAssignment(assignCtx, nbh, xsrfToken, "GPU", accel, shape)
			if err == nil {
				return assigned, nil
			}
		}

		elapsed := p.now().Sub(attemptStarted).Seconds()
		if elapsed < 0 {
			elapsed = 0
		}
		if p.closed.Load() {
			return nil, ErrProviderClosed
		}
		if isCancelled(ctx, opts.CancelCh) {
			return nil, ErrAllocationCancelled
		}
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		status := httpStatusCode(err)
		if status == 412 || errors.Is(err, ErrTooManyAssignments) {
			return nil, &TooManyAssignmentsError{Err: err}
		}
		if usingAssignProxy && isAssignProxyFastFailError(err) {
			return nil, err
		}
		isNetErr := status == 0 && isNetworkOrTransportError(err)
		retryable := isNetErr || status == 502 || status == 503 || status == 504
		if !retryable {
			return nil, err
		}

		if attempt >= len(delays) {
			// Final reconciliation: if the last POST hit Google's 30s frontend
			// proxy timeout or a connection drop after the VM was already assigned,
			// adopt it instead of orphaning it.
			if hasPositiveDelay(delays) {
				outcome := p.waitForRetry(assignCtx, 2*time.Second, opts.CancelCh)
				if p.closed.Load() {
					return nil, ErrProviderClosed
				}
				if outcome == "cancel" {
					return nil, ErrAllocationCancelled
				}
				if ctx != nil && ctx.Err() != nil {
					return nil, ctx.Err()
				}
			} else if p.closed.Load() {
				return nil, ErrProviderClosed
			} else if isCancelled(ctx, opts.CancelCh) {
				return nil, ErrAllocationCancelled
			} else if ctx != nil && ctx.Err() != nil {
				return nil, ctx.Err()
			}
			reconciled, _, recErr := client.GetAssignment(assignCtx, nbh, "GPU", accel, shape)
			if recErr == nil && reconciled != nil {
				emitLog(opts.OnLog, fmt.Sprintf("已通过状态核验确认 %s GPU 实例分配成功", accel), "GPU", "info")
				return reconciled, nil
			}
			if p.closed.Load() {
				return nil, ErrProviderClosed
			}
			if isCancelled(ctx, opts.CancelCh) {
				return nil, ErrAllocationCancelled
			}
			return nil, err
		}

		switch status {
		case 503:
			consecutivePoolBusy++
			lastRetryKind = "capacity"
		case 502, 504:
			consecutivePoolBusy++
			lastRetryKind = "server_busy"
		default:
			consecutivePoolBusy = 0
			lastRetryKind = "network"
		}

		delay := delays[attempt]
		delaySec := int(math.Round(delay.Seconds()))
		nextAttempt := attempt + 2
		if retriesUsed != nil {
			*retriesUsed = attempt + 1
		}

		var retryMsg string
		if status == 503 {
			retryMsg = fmt.Sprintf(
				"Google Colab 当前 %s 算力紧张，暂未分配出 GPU（耗时 %.1f 秒 · HTTP 503），%d 秒后自动重试（第 %d/%d 次）",
				accel, elapsed, delaySec, nextAttempt, totalAttempts,
			)
		} else if status == 502 || status == 504 {
			retryMsg = fmt.Sprintf(
				"Google Colab 当前 %s 分配服务繁忙，暂未分配出 GPU（耗时 %.1f 秒 · HTTP %d），%d 秒后自动重试（第 %d/%d 次）",
				accel, elapsed, status, delaySec, nextAttempt, totalAttempts,
			)
		} else {
			reasonTag := "网络异常"
			if isTimeoutError(err) {
				reasonTag = "请求超时"
			}
			retryMsg = fmt.Sprintf(
				"Google Colab 分配 %s GPU 暂时繁忙或超时（耗时 %.1f 秒 · %s），%d 秒后自动重试（第 %d/%d 次）",
				accel, elapsed, reasonTag, delaySec, nextAttempt, totalAttempts,
			)
		}
		emitLog(opts.OnLog, retryMsg, "GPU", "warning")

		if consecutivePoolBusy == 2 && !poolBusyHintEmitted {
			poolBusyHintEmitted = true
			altGPUs := alternativeGPUHint(accel)
			emitLog(
				opts.OnLog,
				fmt.Sprintf("Google Colab 侧 %s 算力持续紧张，暂未放出空闲 GPU，客户端会继续自动排队；也可稍后改选 %s", accel, altGPUs),
				"GPU",
				"warning",
			)
		}

		// Force GetAssignment reconciliation before submitting another POST so
		// an uncertain 503/504/timeout never double-allocates.
		xsrfToken = ""

		if delay > 0 {
			if opts.OnRetryState != nil && !isCancelled(ctx, opts.CancelCh) {
				opts.OnRetryState(map[string]any{
					"waiting_retry":  true,
					"retry_at":       unixFloat(p.now().Add(delay)),
					"attempt":        attempt + 1,
					"next_attempt":   nextAttempt,
					"total_attempts": totalAttempts,
					"retry_kind":     lastRetryKind,
					"gpu":            accel,
				})
			}
			outcome := p.waitForRetry(assignCtx, delay, opts.CancelCh)
			if p.closed.Load() {
				return nil, ErrProviderClosed
			}
			if outcome == "cancel" {
				return nil, ErrAllocationCancelled
			}
			if ctx != nil && ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
	}

	return nil, errors.New("Google Colab 分配 GPU 失败")
}

// Allocate requests a Google Colab GPU assignment with backoff retry, reconciliation,
// optional isolated assign-only proxy, high-memory fallback, and immediate vault persistence.
func (p *ColabOneProvider) Allocate(ctx context.Context, gpu string, opts AllocateOptions) (*ColabOneSessionState, error) {
	accel := strings.ToUpper(strings.TrimSpace(gpu))
	if !supportedGPUs[accel] {
		return nil, ErrUnsupportedGPU
	}

	// G4, H100, L4 omit shape (high-mem-only / default); T4 and A100 request HIGH_RAM first.
	shape := ""
	if accel != "G4" && accel != "H100" && accel != "L4" {
		shape = "HIGH_RAM"
	}
	shapeLabel := "默认规格"
	if shape == "HIGH_RAM" {
		shapeLabel = "高内存规格"
	}
	emitLog(opts.OnLog, fmt.Sprintf("向 Google Colab 请求分配 %s GPU（%s）", accel, shapeLabel), "GPU", "info")

	rawProxy := strings.TrimSpace(opts.AssignProxy)
	var proxySpec *ColabOneAssignProxySpec
	var client ColabClient
	var err error
	if rawProxy != "" {
		proxySpec, err = p.parseAssignProxyFn(rawProxy)
		if err != nil {
			return nil, err
		}
	}
	usingAssignProxy := proxySpec != nil
	if usingAssignProxy {
		if p.clientFactory == nil {
			return nil, errors.New("未配置 Google Colab 客户端工厂")
		}
		client, err = p.clientFactory(proxySpec.NormalizedURL)
		if err != nil {
			return nil, err
		}
		if closer, ok := client.(io.Closer); ok {
			defer func() { _ = closer.Close() }()
		}
		emitLog(
			opts.OnLog,
			fmt.Sprintf("本次 GPU 分配请求将经由代理（%s），后续连接与推理均不走代理", proxySpec.LogLabel),
			"GPU",
			"info",
		)
	} else {
		client, err = p.regularClient()
		if err != nil {
			return nil, err
		}
	}

	retriesUsed := 0
	result, assignErr := p.assignRuntime(ctx, client, accel, shape, opts, &retriesUsed, usingAssignProxy)
	if assignErr != nil && shape != "" && httpStatusCode(assignErr) == 400 && isColabRequestError(assignErr) {
		if isCancelled(ctx, opts.CancelCh) {
			return nil, ErrAllocationCancelled
		}
		emitLog(opts.OnLog, "高内存规格暂不可用，自动切换为默认规格重试分配", "GPU", "warning")
		retriesUsed = 0
		shape = ""
		result, assignErr = p.assignRuntime(ctx, client, accel, "", opts, &retriesUsed, usingAssignProxy)
	}
	if assignErr != nil {
		if errors.Is(assignErr, ErrAllocationCancelled) {
			return nil, ErrAllocationCancelled
		}
		return nil, errors.New(p.formatAssignError(accel, assignErr, retriesUsed, usingAssignProxy))
	}
	if result == nil || result.RuntimeProxyInfo == nil {
		return nil, errors.New("Google Colab 返回了空的实例分配结果")
	}

	info := result.RuntimeProxyInfo
	exp := info.ResolveExpiresAt(p.now())
	var expPtr *time.Time
	if !exp.IsZero() {
		expPtr = &exp
	}
	machineShape := "STANDARD"
	if shape == "HIGH_RAM" {
		machineShape = "HIGH_RAM"
	}
	session := &ColabOneSessionState{
		Name:           "colabone",
		Endpoint:       result.Endpoint,
		Token:          info.Token,
		URL:            info.URL,
		TokenExpiresAt: expPtr,
		Variant:        "GPU",
		Accelerator:    accel,
		MachineShape:   machineShape,
	}

	// Record ownership immediately before readiness/deployment can fail.
	// If local vault persistence fails, immediately unassign the VM via the
	// regular non-proxy client so it is never orphaned while billing.
	if saveErr := p.SaveSession(session, true); saveErr != nil {
		var rollbackClient ColabClient
		if usingAssignProxy {
			rollbackClient, _ = p.regularClient()
		} else {
			rollbackClient = client
		}
		if rollbackClient != nil {
			relCtx, relCancel := context.WithTimeout(context.Background(), 90*time.Second)
			_ = rollbackClient.Unassign(relCtx, session.Endpoint)
			relCancel()
		}
		return nil, fmt.Errorf("保存实例会话失败，已尝试释放刚分配的实例：%s", truncateRunes(p.safeMessage(saveErr.Error()), 200))
	}

	emitLog(opts.OnLog, fmt.Sprintf("已保存实例会话与控制令牌：%s", session.Endpoint), "客户端", "info")
	return session, nil
}

// ---------------------------------------------------------------------------
// Kernel slot management & execution
// ---------------------------------------------------------------------------

// KernelRuntime returns the live KernelClient for the active endpoint, or nil.
func (p *ColabOneProvider) KernelRuntime() KernelClient {
	ep := p.ActiveEndpoint()
	if ep == "" {
		return nil
	}
	p.kernelMu.Lock()
	slot := p.kernels[ep]
	p.kernelMu.Unlock()
	if slot == nil {
		return nil
	}
	return slot.getRuntime()
}

func (p *ColabOneProvider) kernelSlot(endpoint string) (*kernelSlot, error) {
	p.kernelMu.Lock()
	defer p.kernelMu.Unlock()
	if p.closed.Load() {
		return nil, ErrProviderClosed
	}
	slot, ok := p.kernels[endpoint]
	if !ok {
		slot = &kernelSlot{}
		p.kernels[endpoint] = slot
	}
	return slot, nil
}

// lockKernelSlot acquires slot.mu for the current non-evicted kernelSlot of endpoint,
// retrying if the slot was evicted while waiting for slot.mu and returning ErrProviderClosed
// once Close has been invoked.
func (p *ColabOneProvider) lockKernelSlot(endpoint string) (*kernelSlot, error) {
	for {
		slot, err := p.kernelSlot(endpoint)
		if err != nil {
			return nil, err
		}
		slot.mu.Lock()
		if p.closed.Load() {
			slot.mu.Unlock()
			return nil, ErrProviderClosed
		}
		if !slot.evicted && !slot.clearing.Load() {
			return slot, nil
		}
		slot.mu.Unlock()
	}
}

// Execute executes Python code on the managed kernel channel for session.Endpoint.
func (p *ColabOneProvider) Execute(ctx context.Context, session *ColabOneSessionState, code string, opts ExecuteOptions) ([]KernelOutput, error) {
	if p.closed.Load() {
		return nil, ErrProviderClosed
	}
	if session == nil || session.Endpoint == "" {
		return nil, errors.New("请先启动远端 GPU")
	}
	slot, err := p.lockKernelSlot(session.Endpoint)
	if err != nil {
		return nil, err
	}
	defer slot.mu.Unlock()
	return p.executeLocked(ctx, slot, session, code, opts)
}

func (p *ColabOneProvider) executeLocked(
	ctx context.Context,
	slot *kernelSlot,
	session *ColabOneSessionState,
	code string,
	opts ExecuteOptions,
) ([]KernelOutput, error) {
	if p.closed.Load() {
		p.closeKernelSlotLocked(slot)
		return nil, ErrProviderClosed
	}
	if slot.evicted || slot.clearing.Load() {
		p.closeKernelSlotLocked(slot)
		return nil, errors.New("请先启动远端 GPU")
	}
	if p.kernelFactory == nil {
		return nil, errors.New("未配置远端内核客户端工厂")
	}
	ep := session.Endpoint
	saveIdentity := func(field, value string) {
		// Callbacks can outlive a proxy refresh or release. Merge only the
		// kernel identity into the still-owned session, never old credentials.
		if value == "" {
			return
		}
		p.stateMu.Lock()
		defer p.stateMu.Unlock()
		sOrder, sessions := p.sessionsOrderedLocked()
		current, ok := sessions[ep]
		if !ok || current.Endpoint != ep {
			return
		}
		switch field {
		case "kernel_id":
			if current.KernelID == value {
				return
			}
			current.KernelID = value
		case "session_id":
			if current.SessionID == value {
				return
			}
			current.SessionID = value
		}
		sessions[ep] = current
		rOrder, remotes := p.remotesOrderedLocked()
		_ = p.writeStateLocked(sOrder, sessions, rOrder, remotes, "")
	}

	onKID := func(val string) {
		saveIdentity("kernel_id", val)
	}
	onSID := func(val string) {
		saveIdentity("session_id", val)
	}

	key := kernelKey{endpoint: ep, url: session.URL, token: session.Token}
	if slot.key != key {
		p.closeKernelSlotLocked(slot)
	}

	rt := slot.getRuntime()
	needStart := false
	if rt == nil {
		rt = p.kernelFactory(KernelOptions{
			ServerURL:        session.URL,
			ProxyToken:       session.Token,
			SessionName:      "colabone",
			KernelID:         session.KernelID,
			SessionID:        session.SessionID,
			OnKernelStarted:  onKID,
			OnSessionStarted: onSID,
		})
		if rt == nil {
			return nil, errors.New("未配置远端内核客户端工厂")
		}
		slot.setRuntime(rt)
		slot.key = key
		needStart = true
	}

	// Re-check closed and slot.clearing AFTER publishing rt into slot.setRuntime(rt)
	// so Close()/Clear()'s Store(true) -> slot.getRuntime() ordering guarantees either
	// Close()/Clear() aborts rt or we observe the flag here and unwind immediately.
	if p.closed.Load() {
		p.closeKernelSlotLocked(slot)
		return nil, ErrProviderClosed
	}
	if slot.evicted || slot.clearing.Load() {
		p.closeKernelSlotLocked(slot)
		return nil, errors.New("请先启动远端 GPU")
	}

	if needStart {
		if err := rt.Start(ctx); err != nil {
			p.closeKernelSlotLocked(slot)
			if p.closed.Load() {
				return nil, ErrProviderClosed
			}
			if slot.evicted || slot.clearing.Load() {
				return nil, errors.New("请先启动远端 GPU")
			}
			return nil, err
		}
		if p.closed.Load() {
			p.closeKernelSlotLocked(slot)
			return nil, ErrProviderClosed
		}
		if slot.evicted || slot.clearing.Load() {
			p.closeKernelSlotLocked(slot)
			return nil, errors.New("请先启动远端 GPU")
		}
	}

	if opts.Timeout <= 0 {
		opts.Timeout = 300 * time.Second
	}

	outputs, err := rt.ExecuteCode(ctx, code, opts)
	if err != nil {
		// Reconnect on the next request; never replay a mutation whose outcome
		// is unknown. Closing the websocket must not shut down the kernel.
		p.closeKernelSlotLocked(slot)
		if p.closed.Load() {
			return nil, ErrProviderClosed
		}
		if slot.evicted || slot.clearing.Load() {
			return nil, errors.New("请先启动远端 GPU")
		}
		return nil, err
	}
	if p.closed.Load() {
		p.closeKernelSlotLocked(slot)
		return nil, ErrProviderClosed
	}
	if slot.evicted || slot.clearing.Load() {
		p.closeKernelSlotLocked(slot)
		return nil, errors.New("请先启动远端 GPU")
	}

	if kid := rt.KernelID(); kid != "" && kid != session.KernelID {
		session.KernelID = kid
		saveIdentity("kernel_id", kid)
	}
	if sid := rt.SessionID(); sid != "" && sid != session.SessionID {
		session.SessionID = sid
		saveIdentity("session_id", sid)
	}
	return outputs, nil
}

func (p *ColabOneProvider) closeKernelSlotLocked(slot *kernelSlot) {
	rt := slot.getRuntime()
	if rt != nil {
		_ = rt.Stop(context.Background(), false)
	}
	slot.setRuntime(nil)
	slot.key = kernelKey{}
}

// CloseKernelChannel closes the websocket kernel channel for endpoint (or all endpoints if empty)
// without shutting down the remote kernel. When evict is true, removes the endpoint slot from memory.
func (p *ColabOneProvider) CloseKernelChannel(endpoint string, evict bool) {
	p.kernelMu.Lock()
	var targets []string
	if endpoint != "" {
		targets = []string{endpoint}
	} else {
		targets = make([]string, 0, len(p.kernels))
		for ep := range p.kernels {
			targets = append(targets, ep)
		}
	}
	p.kernelMu.Unlock()

	for _, ep := range targets {
		p.kernelMu.Lock()
		slot := p.kernels[ep]
		p.kernelMu.Unlock()
		if slot == nil {
			continue
		}
		slot.mu.Lock()
		p.closeKernelSlotLocked(slot)
		if evict {
			p.kernelMu.Lock()
			if cur, ok := p.kernels[ep]; ok && cur == slot && slot.getRuntime() == nil {
				delete(p.kernels, ep)
				slot.evicted = true
			}
			p.kernelMu.Unlock()
		}
		slot.mu.Unlock()
	}
}

func (p *ColabOneProvider) closeKernelChannelWithHeldSlot(endpoint string, evict bool, heldSlot *kernelSlot) {
	if heldSlot == nil {
		p.CloseKernelChannel(endpoint, evict)
		return
	}
	p.closeKernelSlotLocked(heldSlot)
	if evict {
		p.kernelMu.Lock()
		if endpoint != "" {
			if cur, ok := p.kernels[endpoint]; ok && cur == heldSlot && heldSlot.getRuntime() == nil {
				delete(p.kernels, endpoint)
			}
		} else {
			for ep, cur := range p.kernels {
				if cur == heldSlot && heldSlot.getRuntime() == nil {
					delete(p.kernels, ep)
				}
			}
		}
		if heldSlot.getRuntime() == nil {
			heldSlot.evicted = true
		}
		p.kernelMu.Unlock()
	}
}

func (p *ColabOneProvider) clearKernelChannels(endpoint string, heldSlot *kernelSlot) {
	if heldSlot != nil {
		heldSlot.clearing.Store(true)
		p.closeKernelChannelWithHeldSlot(endpoint, true, heldSlot)
		return
	}
	// Detach target slot(s) from p.kernels and set slot.clearing BEFORE releasing
	// p.kernelMu and BEFORE aborting runtimes so:
	//   - New lockKernelSlot callers never see or busy-spin on a dying slot;
	//   - Any in-flight holder already holding slot.mu (e.g., finishing uploadBytesWithSlot
	//     or calling slot.setRuntime(rt)) is Dekker-paired with clearing.Store(true) ->
	//     slot.getRuntime() and unwinds immediately without starting a 210-300s dial/exec.
	slots := p.detachKernelSlotsForClear(endpoint)
	p.abortKernelSlots(slots)
	p.closeDetachedKernelSlots(slots)
}

func (p *ColabOneProvider) detachKernelSlotsForClear(endpoint string) []*kernelSlot {
	p.kernelMu.Lock()
	defer p.kernelMu.Unlock()
	if endpoint != "" {
		slot := p.kernels[endpoint]
		if slot == nil {
			return nil
		}
		slot.clearing.Store(true)
		delete(p.kernels, endpoint)
		return []*kernelSlot{slot}
	}
	slots := make([]*kernelSlot, 0, len(p.kernels))
	for ep, slot := range p.kernels {
		if slot != nil {
			slot.clearing.Store(true)
			slots = append(slots, slot)
		}
		delete(p.kernels, ep)
	}
	return slots
}

func (p *ColabOneProvider) closeDetachedKernelSlots(slots []*kernelSlot) {
	for _, slot := range slots {
		if slot == nil {
			continue
		}
		slot.mu.Lock()
		p.closeKernelSlotLocked(slot)
		slot.evicted = true
		slot.mu.Unlock()
	}
}

// Close releases all active kernel channels, closes the external read-only observer
// kernel, and closes idle HTTP connections held by the provider.
func (p *ColabOneProvider) Close() {
	if p == nil {
		return
	}
	p.closed.Store(true)
	p.closedOnce.Do(func() {
		if p.closedCh != nil {
			close(p.closedCh)
		}
	})

	// Abort every live managed kernel runtime BEFORE CloseKernelChannel acquires
	// slot.mu so an active 300s ExecuteCode/Start unwinds immediately rather than
	// stalling Close() and starving CloseObserver().
	p.abortKernelExecution("")

	// Abort any live external observer runtime upfront as well so in-flight
	// observer sampling unwinds immediately.
	p.observerMu.Lock()
	var obsRT KernelClient
	if p.observer != nil {
		obsRT = p.observer.runtime
	}
	p.observerMu.Unlock()
	if obsRT != nil {
		obsRT.Abort()
	}

	p.CloseKernelChannel("", true)
	p.CloseObserver("", true)
	if p.httpClient != nil {
		p.httpClient.CloseIdleConnections()
	}
}

func (p *ColabOneProvider) cancelOnClose(cancel context.CancelFunc) func() {
	if p == nil || p.closedCh == nil {
		return func() {}
	}
	if p.closed.Load() {
		cancel()
		return func() {}
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-p.closedCh:
			cancel()
		case <-done:
		}
	}()
	return func() {
		close(done)
		wg.Wait()
	}
}

func (p *ColabOneProvider) abortKernelExecution(endpoint string) {
	p.kernelMu.Lock()
	var slots []*kernelSlot
	if endpoint != "" {
		if slot := p.kernels[endpoint]; slot != nil {
			slots = []*kernelSlot{slot}
		}
	} else {
		slots = make([]*kernelSlot, 0, len(p.kernels))
		for _, slot := range p.kernels {
			if slot != nil {
				slots = append(slots, slot)
			}
		}
	}
	p.kernelMu.Unlock()
	p.abortKernelSlots(slots)
}

func (p *ColabOneProvider) abortKernelSlots(slots []*kernelSlot) {
	for _, slot := range slots {
		if slot == nil {
			continue
		}
		if rt := slot.getRuntime(); rt != nil {
			rt.Abort()
		}
	}
}

// ---------------------------------------------------------------------------
// Contents API upload / delete with bounded retry and 401/403 token rotation
// ---------------------------------------------------------------------------

type defaultContentsClient struct {
	httpClient *http.Client
}

func quoteRemotePath(remotePath string) string {
	trimmed := strings.Trim(remotePath, "/")
	if trimmed == "" {
		return ""
	}
	parts := strings.Split(trimmed, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

const providerContentsUploadChunkBytes = 6 * 1024 * 1024 // 6 MiB raw (exact multiple of 3 -> clean unpadded 8 MiB base64 per PUT)

func (c *defaultContentsClient) UploadBytes(
	ctx context.Context,
	baseURL, proxyToken, remotePath string,
	data []byte,
	timeout time.Duration,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	parts := strings.Split(strings.Trim(remotePath, "/"), "/")
	filename := parts[len(parts)-1]

	quotedPath := quoteRemotePath(remotePath)
	q := url.Values{}
	q.Set("authuser", "0")
	q.Set("colab-runtime-proxy-token", proxyToken)
	fullURL := strings.TrimRight(baseURL, "/") + "/api/contents/" + quotedPath + "?" + q.Encode()

	sendChunk := func(chunkBytes []byte, chunkNum int) error {
		payload := map[string]any{
			"name":    filename,
			"path":    remotePath,
			"type":    "file",
			"format":  "base64",
			"content": base64.StdEncoding.EncodeToString(chunkBytes),
			"chunk":   chunkNum,
		}
		bodyBytes, err := json.Marshal(payload)
		if err != nil {
			return err
		}

		req, err := http.NewRequestWithContext(reqCtx, http.MethodPut, fullURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return redactProviderURLError(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Colab-Client-Agent", "colab-cli")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return redactProviderURLError(err)
		}
		defer func() { _ = resp.Body.Close() }()
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return &ColabRequestError{
				StatusCode:   resp.StatusCode,
				Method:       http.MethodPut,
				URL:          redactProviderProxyTokenURL(fullURL),
				Reason:       resp.Status,
				ResponseBody: string(respBody),
			}
		}
		return nil
	}

	if len(data) <= providerContentsUploadChunkBytes {
		return sendChunk(data, 1)
	}

	chunkIdx := 1
	for offset := 0; offset < len(data); offset += providerContentsUploadChunkBytes {
		end := offset + providerContentsUploadChunkBytes
		chunkNum := chunkIdx
		if end >= len(data) {
			end = len(data)
			chunkNum = -1
		}
		if err := sendChunk(data[offset:end], chunkNum); err != nil {
			return err
		}
		chunkIdx++
	}
	return nil
}

func (c *defaultContentsClient) DeleteFile(ctx context.Context, baseURL, proxyToken, remotePath string) error {
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	quotedPath := quoteRemotePath(remotePath)
	q := url.Values{}
	q.Set("authuser", "0")
	q.Set("colab-runtime-proxy-token", proxyToken)
	fullURL := strings.TrimRight(baseURL, "/") + "/api/contents/" + quotedPath + "?" + q.Encode()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodDelete, fullURL, nil)
	if err != nil {
		return redactProviderURLError(err)
	}
	req.Header.Set("X-Colab-Client-Agent", "colab-cli")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return redactProviderURLError(err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	_ = resp.Body.Close()
	return nil
}

const (
	providerProxyTokenQueryKey = "colab-runtime-proxy-token"
	providerRedactedURLMarker  = "[redacted]"
	providerMaxSanitizeDepth   = 64
)

type sanitizedProviderError struct {
	msg string
	err error
}

func (e *sanitizedProviderError) Error() string { return e.msg }
func (e *sanitizedProviderError) Unwrap() error { return e.err }

type sanitizedProviderMultiError struct {
	msg  string
	errs []error
}

func (e *sanitizedProviderMultiError) Error() string   { return e.msg }
func (e *sanitizedProviderMultiError) Unwrap() []error { return e.errs }

func redactProviderProxyTokenURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return providerRedactedURLMarker
	}
	q := parsed.Query()
	hasTokenQuery := q.Has(providerProxyTokenQueryKey)
	if hasTokenQuery {
		q.Set(providerProxyTokenQueryKey, "REDACTED")
		parsed.RawQuery = q.Encode()
	}
	redacted := parsed.String()
	if containsProviderProxyTokenParam(rawURL) {
		if !hasTokenQuery {
			return providerRedactedURLMarker
		}
		remaining := strings.Replace(redacted, providerProxyTokenQueryKey+"=REDACTED", "", 1)
		if containsProviderProxyTokenParam(remaining) {
			return providerRedactedURLMarker
		}
	}
	return redacted
}

func redactProviderURLError(err error) error {
	sanitized, _ := redactProviderURLErrorWithChanged(err, 0)
	return sanitized
}

func redactProviderURLErrorWithChanged(err error, depth int) (error, bool) {
	if err == nil {
		return nil, false
	}
	if uerr, ok := err.(*url.Error); ok {
		if uerr == nil {
			return nil, false
		}
		if depth < providerMaxSanitizeDepth {
			return sanitizeDirectProviderURLError(uerr, depth+1)
		}
	}
	if depth >= providerMaxSanitizeDepth {
		rawMsg := err.Error()
		redactedMsg := redactProviderProxyTokenInString(rawMsg)
		if redactedMsg != rawMsg {
			return &sanitizedProviderError{msg: redactedMsg}, true
		}
		return err, false
	}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		errs := multi.Unwrap()
		changed := false
		sanitized := make([]error, len(errs))
		rawMsg := err.Error()
		redactedMsg := rawMsg
		for i, sub := range errs {
			s, subChanged := redactProviderURLErrorWithChanged(sub, depth+1)
			sanitized[i] = s
			if subChanged {
				changed = true
				if sub != nil && s != nil {
					if oldMsg := sub.Error(); oldMsg != "" {
						redactedMsg = strings.ReplaceAll(redactedMsg, oldMsg, s.Error())
					}
				}
			}
		}
		redactedMsg = redactProviderProxyTokenInString(redactedMsg)
		if changed || redactedMsg != rawMsg {
			return &sanitizedProviderMultiError{msg: redactedMsg, errs: sanitized}, true
		}
		return err, false
	}
	if single, ok := err.(interface{ Unwrap() error }); ok {
		inner := single.Unwrap()
		sanitizedInner, innerChanged := redactProviderURLErrorWithChanged(inner, depth+1)
		rawMsg := err.Error()
		redactedMsg := rawMsg
		if innerChanged && inner != nil && sanitizedInner != nil {
			if oldMsg := inner.Error(); oldMsg != "" {
				redactedMsg = strings.ReplaceAll(redactedMsg, oldMsg, sanitizedInner.Error())
			}
		}
		redactedMsg = redactProviderProxyTokenInString(redactedMsg)
		if innerChanged || redactedMsg != rawMsg {
			if sanitizedInner != nil && redactedMsg == sanitizedInner.Error() {
				return sanitizedInner, true
			}
			return &sanitizedProviderError{msg: redactedMsg, err: sanitizedInner}, true
		}
		return err, false
	}
	var uerr *url.Error
	if errors.As(err, &uerr) && uerr != nil {
		sanitizedUErr, uChanged := sanitizeDirectProviderURLError(uerr, depth+1)
		rawMsg := err.Error()
		redactedMsg := rawMsg
		if uChanged {
			if oldMsg := uerr.Error(); oldMsg != "" {
				redactedMsg = strings.ReplaceAll(redactedMsg, oldMsg, sanitizedUErr.Error())
			}
		}
		redactedMsg = redactProviderProxyTokenInString(redactedMsg)
		if uChanged || redactedMsg != rawMsg {
			if redactedMsg == sanitizedUErr.Error() {
				return sanitizedUErr, true
			}
			return &sanitizedProviderError{msg: redactedMsg, err: sanitizedUErr}, true
		}
		return err, false
	}
	rawMsg := err.Error()
	redactedMsg := redactProviderProxyTokenInString(rawMsg)
	if redactedMsg != rawMsg {
		return &sanitizedProviderError{msg: redactedMsg}, true
	}
	return err, false
}

func sanitizeDirectProviderURLError(uerr *url.Error, depth int) (*url.Error, bool) {
	newOp := uerr.Op
	if containsProviderProxyTokenParam(newOp) {
		newOp = redactProviderProxyTokenInString(newOp)
	}
	newURL := uerr.URL
	if containsProviderProxyTokenParam(newURL) {
		newURL = redactProviderProxyTokenURL(newURL)
	}
	newErr, errChanged := redactProviderURLErrorWithChanged(uerr.Err, depth)
	if !errChanged && newOp == uerr.Op && newURL == uerr.URL {
		return uerr, false
	}
	cloned := *uerr
	cloned.Op = newOp
	cloned.URL = newURL
	cloned.Err = newErr
	return &cloned, true
}

func containsProviderProxyTokenParam(s string) bool {
	lower := providerASCIILower(s)
	return strings.Contains(lower, providerProxyTokenQueryKey+"=") ||
		strings.Contains(lower, providerProxyTokenQueryKey+"%3d")
}

func redactProviderProxyTokenInString(s string) string {
	if !containsProviderProxyTokenParam(s) {
		return s
	}
	lower := providerASCIILower(s)
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		idxEq := strings.Index(lower[i:], providerProxyTokenQueryKey+"=")
		idxEsc := strings.Index(lower[i:], providerProxyTokenQueryKey+"%3d")

		idx := -1
		prefixLen := 0
		escaped := false
		if idxEq >= 0 && (idxEsc < 0 || idxEq < idxEsc) {
			idx = i + idxEq
			prefixLen = len(providerProxyTokenQueryKey) + 1
		} else if idxEsc >= 0 {
			idx = i + idxEsc
			prefixLen = len(providerProxyTokenQueryKey) + 3
			escaped = true
		}
		if idx < 0 {
			b.WriteString(s[i:])
			break
		}

		valStart := idx + prefixLen
		b.WriteString(s[i:valStart])
		b.WriteString("REDACTED")

		valEnd := valStart
		for valEnd < len(s) {
			ch := s[valEnd]
			if isProviderProxyTokenValueDelimiter(ch) {
				break
			}
			if escaped && ch == '%' && valEnd+2 < len(s) && isProviderEscapedDelimiter(lower[valEnd:valEnd+3]) {
				break
			}
			valEnd++
		}
		i = valEnd
	}
	return b.String()
}

func isProviderProxyTokenValueDelimiter(c byte) bool {
	switch c {
	case '&', '#', '"', '\'', '\\', ' ', '\t', '\r', '\n', ')', ']', '}', '>', ',', ';':
		return true
	default:
		return false
	}
}

func isProviderEscapedDelimiter(seq string) bool {
	switch seq {
	case "%26", "%23", "%20", "%22", "%27", "%09", "%0a", "%0d":
		return true
	default:
		return false
	}
}

func providerASCIILower(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			if b.Len() == 0 {
				b.Grow(len(s))
				b.WriteString(s[:i])
			}
			b.WriteByte(c + ('a' - 'A'))
		} else if b.Len() > 0 {
			b.WriteByte(c)
		}
	}
	if b.Len() == 0 {
		return s
	}
	return b.String()
}

func (p *ColabOneProvider) uploadBytes(
	ctx context.Context,
	session *ColabOneSessionState,
	remotePath string,
	data []byte,
	timeout time.Duration,
) (*ColabOneSessionState, error) {
	return p.uploadBytesWithSlot(ctx, session, remotePath, data, timeout, nil)
}

func (p *ColabOneProvider) uploadBytesWithSlot(
	ctx context.Context,
	session *ColabOneSessionState,
	remotePath string,
	data []byte,
	timeout time.Duration,
	heldSlot *kernelSlot,
) (*ColabOneSessionState, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	effectiveTimeout := timeout
	if effectiveTimeout <= 0 {
		effectiveTimeout = 60 * time.Second
	}
	uploadDeadline := p.now().Add(effectiveTimeout)
	if ctxDeadline, hasDeadline := ctx.Deadline(); hasDeadline && ctxDeadline.Before(uploadDeadline) {
		uploadDeadline = ctxDeadline
	}

	cleanupPartial := func(sess *ColabOneSessionState) {
		if len(data) <= providerContentsUploadChunkBytes || p.closed.Load() || sess == nil || remotePath == "" {
			return
		}
		delCtx, delCancel := context.WithTimeout(context.Background(), 3*time.Second)
		stopDelWatch := p.cancelOnClose(delCancel)
		p.deleteRemoteFile(delCtx, sess, remotePath)
		stopDelWatch()
		delCancel()
	}

	rotated := false
	cur := session
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if ctx.Err() != nil {
			cleanupPartial(cur)
			return cur, ctx.Err()
		}
		remaining := uploadDeadline.Sub(p.now())
		if remaining <= 0 {
			cleanupPartial(cur)
			if lastErr != nil {
				return cur, lastErr
			}
			return cur, context.DeadlineExceeded
		}
		attemptTimeout := remaining
		if timeout > 0 && attemptTimeout > timeout {
			attemptTimeout = timeout
		}
		err := p.contents.UploadBytes(ctx, cur.URL, cur.Token, remotePath, data, attemptTimeout)
		if err == nil {
			return cur, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			cleanupPartial(cur)
			return cur, err
		}
		status := httpStatusCode(err)
		if (status == 401 || status == 403) && !rotated {
			rotated = true
			if refreshed, refErr := p.session(ctx, true, cur.Endpoint, true, heldSlot); refErr == nil && refreshed != nil {
				cur = refreshed
				// Give the rotated token an actual retry even if rotation happened on the final attempt.
				attempt--
				continue
			}
		}
		// Never retry a full-upload context.DeadlineExceeded from chunk 1 (its upload budget is
		// already exhausted) or any transient error when insufficient outer budget remains.
		if errors.Is(err, context.DeadlineExceeded) {
			cleanupPartial(cur)
			return cur, err
		}
		if (status == 502 || status == 503 || status == 504 || (status == 0 && isNetworkOrTransportError(err))) && attempt < 2 {
			retryDelay := p.uploadRetryDelay * time.Duration(attempt+1)
			remAfter := uploadDeadline.Sub(p.now())
			minRetryBudget := retryDelay + time.Second
			if effectiveTimeout < 2*time.Second {
				minRetryBudget = retryDelay
			}
			if remAfter <= minRetryBudget {
				cleanupPartial(cur)
				return cur, err
			}
			if sleepErr := p.sleep(ctx, retryDelay); sleepErr != nil {
				cleanupPartial(cur)
				return cur, sleepErr
			}
			continue
		}
		cleanupPartial(cur)
		return cur, err
	}
	cleanupPartial(cur)
	if lastErr != nil {
		return cur, lastErr
	}
	return cur, errors.New("上传文件到远端实例失败")
}

func (p *ColabOneProvider) deleteRemoteFile(ctx context.Context, session *ColabOneSessionState, remotePath string) {
	if p.contents == nil || session == nil || remotePath == "" {
		return
	}
	_ = p.contents.DeleteFile(ctx, session.URL, session.Token, remotePath)
}

// ---------------------------------------------------------------------------
// Readiness polling, service bundling, Start & UpgradeService
// ---------------------------------------------------------------------------

// Ready polls GET {session.URL}/api/kernels until HTTP 200 or deadline (90s default).
func (p *ColabOneProvider) Ready(ctx context.Context, session *ColabOneSessionState, onLog LogFn, cancelCh <-chan struct{}) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if session == nil || strings.TrimSpace(session.URL) == "" {
		return errors.New("远端会话状态无效")
	}
	started := p.now()
	lastNote := started
	deadline := started.Add(p.readyTimeout)
	kernelsURL := strings.TrimRight(session.URL, "/") + "/api/kernels"

	readyCtx, cancelReady := context.WithCancel(ctx)
	defer cancelReady()
	if cancelCh != nil || p.closedCh != nil {
		go func() {
			select {
			case <-cancelCh:
				cancelReady()
			case <-p.closedCh:
				cancelReady()
			case <-readyCtx.Done():
			}
		}()
	}

	for p.now().Before(deadline) {
		if p.closed.Load() {
			return ErrProviderClosed
		}
		if isCancelled(ctx, cancelCh) {
			return ErrStartCancelled
		}
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		reqCtx, cancelReq := context.WithTimeout(readyCtx, 8*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, kernelsURL, nil)
		if err == nil {
			req.Header.Set("X-Colab-Client-Agent", "colab-cli")
			req.Header.Set("X-Colab-Runtime-Proxy-Token", session.Token)
			req.Header.Set("Authorization", "Bearer "+session.Token)
			resp, doErr := p.httpClient.Do(req)
			if doErr == nil {
				status := resp.StatusCode
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
				_ = resp.Body.Close()
				cancelReq()
				if status == http.StatusOK {
					emitLog(
						onLog,
						fmt.Sprintf("远端 Jupyter 内核已响应（耗时 %.1f 秒）", p.now().Sub(started).Seconds()),
						"GPU",
						"info",
					)
					return nil
				}
			} else {
				cancelReq()
			}
		} else {
			cancelReq()
		}

		if p.closed.Load() {
			return ErrProviderClosed
		}
		if isCancelled(ctx, cancelCh) {
			return ErrStartCancelled
		}
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		now := p.now()
		if now.Sub(lastNote) >= p.readyLogInterval {
			lastNote = now
			emitLog(
				onLog,
				fmt.Sprintf("仍在等待远端 Jupyter 内核响应（已等待 %d 秒）", int(now.Sub(started).Seconds())),
				"GPU",
				"info",
			)
		}
		outcome := p.waitForRetry(readyCtx, p.readyPollInterval, cancelCh)
		if p.closed.Load() {
			return ErrProviderClosed
		}
		if outcome == "cancel" {
			return ErrStartCancelled
		}
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return errors.New("远端内核未能在 90 秒内就绪")
}

func (p *ColabOneProvider) buildServiceZip() ([]byte, int, error) {
	if p.bundler != nil {
		return p.bundler.BuildZip()
	}
	if p.rootDir == "" {
		return nil, 0, errors.New("未配置远端服务打包器或根目录")
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fileCount := 0
	for _, folder := range ColabOneServiceBundleFolders {
		dirPath := filepath.Join(p.rootDir, folder)
		entries, err := os.ReadDir(dirPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			_ = zw.Close()
			return nil, 0, err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			ext := filepath.Ext(entry.Name())
			if !hasSuffixMatch(ext, ColabOneServiceBundleSuffixes) {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dirPath, entry.Name()))
			if err != nil {
				_ = zw.Close()
				return nil, 0, err
			}
			w, err := zw.Create("colabone/" + folder + "/" + entry.Name())
			if err != nil {
				_ = zw.Close()
				return nil, 0, err
			}
			if _, err := w.Write(raw); err != nil {
				_ = zw.Close()
				return nil, 0, err
			}
			fileCount++
		}
	}
	if err := zw.Close(); err != nil {
		return nil, 0, err
	}
	return buf.Bytes(), fileCount, nil
}

func hasSuffixMatch(ext string, allowed []string) bool {
	for _, s := range allowed {
		if ext == s {
			return true
		}
	}
	return false
}

func (p *ColabOneProvider) resolveVMBootScript(zipBytes []byte) (string, error) {
	if p.vmBootScript != "" {
		return p.vmBootScript, nil
	}
	if bsp, ok := p.bundler.(BootScriptProvider); ok {
		if code, err := bsp.VMBootScript(); err == nil && code != "" {
			return code, nil
		}
	}
	if code, ok := readZipEntry(zipBytes, "colabone/server/vm_boot.py", "server/vm_boot.py"); ok {
		return code, nil
	}
	if p.rootDir != "" {
		raw, err := os.ReadFile(filepath.Join(p.rootDir, "server", "vm_boot.py"))
		if err == nil {
			return string(raw), nil
		}
		return "", err
	}
	return "", errors.New("未找到远端引导脚本 server/vm_boot.py")
}

func (p *ColabOneProvider) resolveVMUpdateScript(zipBytes []byte) (string, error) {
	if p.vmUpdateScript != "" {
		return p.vmUpdateScript, nil
	}
	if bsp, ok := p.bundler.(BootScriptProvider); ok {
		if code, err := bsp.VMUpdateScript(); err == nil && code != "" {
			return code, nil
		}
	}
	if code, ok := readZipEntry(zipBytes, "colabone/server/vm_update.py", "server/vm_update.py"); ok {
		return code, nil
	}
	if p.rootDir != "" {
		raw, err := os.ReadFile(filepath.Join(p.rootDir, "server", "vm_update.py"))
		if err == nil {
			return string(raw), nil
		}
		return "", err
	}
	return "", errors.New("未找到远端热更新脚本 server/vm_update.py")
}

func readZipEntry(zipBytes []byte, names ...string) (string, bool) {
	if len(zipBytes) == 0 {
		return "", false
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return "", false
	}
	wanted := make(map[string]bool, len(names))
	for _, n := range names {
		wanted[n] = true
	}
	for _, f := range zr.File {
		if wanted[f.Name] {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			raw, readErr := io.ReadAll(rc)
			_ = rc.Close()
			if readErr == nil {
				return string(raw), true
			}
		}
	}
	return "", false
}

// Start allocates (or reuses) a Colab GPU runtime, waits for Jupyter kernel readiness,
// uploads colabone.zip + colabone-boot.json (+ optional rclone config), and runs vm_boot.py
// with streaming stage/log/telemetry output. On failure after fresh allocation, automatically
// rolls back by releasing the newly allocated VM and restoring the previous active endpoint.
func (p *ColabOneProvider) Start(
	ctx context.Context,
	gpu string,
	notify StageFn,
	opts StartOptions,
) (map[string]any, error) {
	if p.closed.Load() {
		return nil, ErrProviderClosed
	}
	fresh := false
	previousActive := p.ActiveEndpoint()
	allocatedEndpoint := ""

	if !opts.CreateNew {
		session, err := p.Session(ctx, true, "", false)
		if err != nil {
			return nil, err
		}
		if session != nil {
			remote := p.RemoteInfo(session.Endpoint)
			if remote != nil && strVal(remote["endpoint"]) == session.Endpoint {
				emitLog(opts.OnLog, fmt.Sprintf("检测到已连接实例，直接复用：%s", session.Endpoint), "GPU", "info")
				return remote, nil
			}
			return nil, errors.New("实例存在但部署未完成，请先释放后重新启动")
		}
	}

	if isCancelled(ctx, opts.CancelCh) {
		return nil, ErrAllocationCancelled
	}

	emitLog(opts.OnLog, "查询 Google Colab 账号余额与当前运行实例", "计费", "info")
	before, _ := p.Account(ctx)
	if before == nil {
		before = map[string]any{}
	}
	if boolVal(before["logged_in"]) {
		emitLog(
			opts.OnLog,
			fmt.Sprintf("当前余额 %.2f CU · 活跃实例 %d 台", floatVal(before["balance"]), intVal(before["assignments"])),
			"计费",
			"info",
		)
	}
	if isCancelled(ctx, opts.CancelCh) {
		return nil, ErrAllocationCancelled
	}

	rollback := func(origErr error) (map[string]any, error) {
		cancelled := isCancelled(ctx, opts.CancelCh) || errors.Is(origErr, ErrStartCancelled) || errors.Is(origErr, ErrAllocationCancelled)
		if fresh {
			if cancelled {
				emitStage(notify, "已取消启动，正在释放刚分配的实例")
				emitLog(opts.OnLog, fmt.Sprintf("启动已取消，正在释放刚分配的实例：%s", allocatedEndpoint), "GPU", "info")
			} else {
				emitLog(opts.OnLog, fmt.Sprintf("启动过程出错，正在自动回滚释放刚分配的实例：%s", allocatedEndpoint), "GPU", "warning")
			}
			// Always target the freshly allocated endpoint explicitly so a concurrent
			// clear of allocatedEndpoint can never release another healthy GPU.
			targetToRelease := allocatedEndpoint
			if targetToRelease != "" {
				relCtx, relCancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer relCancel()
				if _, relErr := p.Release(relCtx, targetToRelease, opts.OnLog); relErr != nil {
					if cancelled {
						return nil, errors.New("取消启动时未能确认释放刚分配的实例。实例记录已保留，请在「远端设备」页立即重试停止实例")
					}
					return nil, errors.New("启动失败且释放未确认。实例记录已保留，请在「远端设备」页立即重试停止实例")
				}
			}
			if previousActive != "" {
				for _, ep := range p.ManagedEndpoints() {
					if ep == previousActive {
						_, _ = p.SetActive(previousActive)
						break
					}
				}
			}
		}
		if cancelled && fresh {
			return nil, ErrStartCancelled
		}
		return nil, origErr
	}

	emitStage(notify, "正在分配 "+gpu)
	started := unixFloat(p.now())
	session, err := p.Allocate(ctx, gpu, AllocateOptions{
		OnLog:        opts.OnLog,
		OnStage:      notify,
		CancelCh:     opts.CancelCh,
		OnRetryState: opts.OnRetryState,
		AssignProxy:  strings.TrimSpace(opts.AssignProxy),
		MaxAttempts:  opts.MaxAttempts,
	})
	if err != nil {
		return rollback(err)
	}
	allocatedEndpoint = session.Endpoint
	fresh = true

	if opts.OnRetryState != nil {
		opts.OnRetryState(map[string]any{
			"waiting_retry": false,
			"retry_at":      0.0,
			"allocated":     true,
			"gpu":           strings.ToUpper(gpu),
		})
	}

	acct, _ := p.Account(ctx)
	if acct == nil {
		acct = map[string]any{}
	}
	rate := sanitizeFloat(floatVal(acct["rate"]))
	remoteToken, err := randomURLSafeToken(32)
	if err != nil {
		return rollback(err)
	}
	_, hasBeforeBal := before["balance"]
	hasStartBalance := boolVal(before["logged_in"]) && hasBeforeBal
	remote := map[string]any{
		"endpoint":          session.Endpoint,
		"token":             remoteToken,
		"transport":         "colab",
		"started_at":        started,
		"deadline":          0,
		"start_balance":     sanitizeFloat(floatVal(before["balance"])),
		"has_start_balance": hasStartBalance,
		"rate":              rate,
		"gpu":               strings.ToUpper(gpu),
	}
	initialLoc := p.inferLocationFromEndpoint(session.Endpoint, session.URL)
	if len(initialLoc) > 0 {
		remote["location"] = initialLoc
	}
	if err := p.SaveRemote(remote, true); err != nil {
		return rollback(err)
	}

	metaParts := make([]string, 0, 3)
	if locText := formatLocationShort(initialLoc); locText != "" {
		metaParts = append(metaParts, locText)
	}
	if session.MachineShape == "HIGH_RAM" {
		metaParts = append(metaParts, "高内存")
	}
	if rate != 0 {
		metaParts = append(metaParts, fmt.Sprintf("%.2f CU/小时", rate))
	}
	detail := ""
	if len(metaParts) > 0 {
		detail = "（" + strings.Join(metaParts, " · ") + "）"
	}
	emitLog(opts.OnLog, fmt.Sprintf("已分配实例 %s%s", session.Endpoint, detail), "GPU", "info")

	if isCancelled(ctx, opts.CancelCh) {
		return rollback(ErrStartCancelled)
	}

	emitStage(notify, "等待远端内核")
	if err := p.Ready(ctx, session, opts.OnLog, opts.CancelCh); err != nil {
		return rollback(err)
	}
	if isCancelled(ctx, opts.CancelCh) {
		return rollback(ErrStartCancelled)
	}

	emitStage(notify, "部署远端管理服务")
	zipBytes, fileCount, err := p.buildServiceZip()
	if err != nil {
		return rollback(err)
	}
	pkgKB := int(math.Round(float64(len(zipBytes)) / 1024.0))
	if pkgKB < 1 {
		pkgKB = 1
	}
	emitLog(opts.OnLog, fmt.Sprintf("已打包远端管理服务（%d 个文件，%d KB）", fileCount, pkgKB), "客户端", "info")

	bootConfigBytes, err := json.Marshal(map[string]any{
		"token":      remoteToken,
		"runtime_id": session.Endpoint,
		"root":       "/content/colabone-data",
		"deadline":   0,
	})
	if err != nil {
		return rollback(err)
	}

	if isCancelled(ctx, opts.CancelCh) {
		return rollback(ErrStartCancelled)
	}
	emitLog(opts.OnLog, "正在上传管理服务程序包 (colabone.zip)", "GPU", "info")
	session, err = p.uploadBytes(ctx, session, "content/colabone.zip", zipBytes, 60*time.Second)
	if err != nil {
		return rollback(err)
	}

	if isCancelled(ctx, opts.CancelCh) {
		return rollback(ErrStartCancelled)
	}
	emitLog(opts.OnLog, "正在上传远端启动配置 (colabone-boot.json)", "GPU", "info")
	session, err = p.uploadBytes(ctx, session, "content/colabone-boot.json", bootConfigBytes, 60*time.Second)
	if err != nil {
		return rollback(err)
	}

	if opts.RcloneConf != "" {
		if isCancelled(ctx, opts.CancelCh) {
			return rollback(ErrStartCancelled)
		}
		emitLog(opts.OnLog, "正在上传 Google 云盘授权配置", "云盘", "info")
		session, err = p.uploadBytes(ctx, session, "content/.colabone_rclone.conf", []byte(opts.RcloneConf), 60*time.Second)
		if err != nil {
			return rollback(err)
		}
	}

	if isCancelled(ctx, opts.CancelCh) {
		return rollback(ErrStartCancelled)
	}

	bootScript, err := p.resolveVMBootScript(zipBytes)
	if err != nil {
		return rollback(err)
	}

	emitLog(opts.OnLog, "文件上传完成，开始在远端内核执行引导脚本", "远端", "info")

	var hookMu sync.Mutex
	lineBufs := make(map[string]string)
	streamedTelemetry := false
	streamedLogs := false
	remoteLoc, _ := remote["location"].(map[string]any)

	consumeBootLine := func(rawLine string, fromStream bool) {
		stripped := strings.TrimSpace(rawLine)
		if strings.HasPrefix(stripped, "COLABONE_TELEMETRY=") {
			rawJSON := strings.TrimPrefix(stripped, "COLABONE_TELEMETRY=")
			var sample map[string]any
			if json.Unmarshal([]byte(rawJSON), &sample) == nil && sample != nil && opts.OnTelemetry != nil {
				if fromStream {
					streamedTelemetry = true
				}
				opts.OnTelemetry(session.Endpoint, cloneMap(remoteLoc), sample)
			}
			return
		}
		if strings.HasPrefix(stripped, "COLABONE_LOG=") {
			rawJSON := strings.TrimPrefix(stripped, "COLABONE_LOG=")
			var entry map[string]any
			if json.Unmarshal([]byte(rawJSON), &entry) == nil && entry != nil {
				if fromStream {
					streamedLogs = true
				}
				stage := strings.TrimSpace(strVal(entry["stage"]))
				msg := strings.TrimSpace(strVal(entry["message"]))
				src := strings.TrimSpace(strVal(entry["source"]))
				if src == "" {
					src = "远端"
				}
				lvl := strings.TrimSpace(strVal(entry["level"]))
				if lvl == "" {
					lvl = "info"
				}
				if stage != "" && notify != nil {
					notify(stage)
				}
				if msg != "" && msg != stage {
					emitLog(opts.OnLog, msg, src, lvl)
				}
			}
		}
	}

	var outputHook func(KernelOutput)
	if opts.OnTelemetry != nil || opts.OnLog != nil || notify != nil {
		outputHook = func(output KernelOutput) {
			if output.OutputType == "stream" && output.Name == "stderr" {
				return
			}
			if output.Text == "" {
				return
			}
			streamKey := output.Name
			if streamKey == "" {
				streamKey = "stdout"
			}
			hookMu.Lock()
			defer hookMu.Unlock()
			combined := lineBufs[streamKey] + output.Text
			parts := splitLinesKeepEnds(combined)
			lineBufs[streamKey] = ""
			for _, part := range parts {
				if !strings.HasSuffix(part, "\n") && !strings.HasSuffix(part, "\r") {
					lineBufs[streamKey] = part
					continue
				}
				consumeBootLine(part, true)
			}
		}
	}

	execCtx, cancelExec := context.WithCancel(ctx)
	stopWatch := make(chan struct{})
	var watchWG sync.WaitGroup
	if opts.CancelCh != nil || ctx.Done() != nil {
		watchWG.Add(1)
		go func() {
			defer watchWG.Done()
			select {
			case <-stopWatch:
				return
			case <-opts.CancelCh:
				cancelExec()
				p.abortKernelExecution(session.Endpoint)
			case <-ctx.Done():
				cancelExec()
				p.abortKernelExecution(session.Endpoint)
			}
		}()
	}

	outputs, execErr := p.Execute(execCtx, session, bootScript, ExecuteOptions{
		Timeout:    300 * time.Second,
		AllowStdin: false,
		OutputHook: outputHook,
	})
	close(stopWatch)
	watchWG.Wait()
	cancelExec()

	hookMu.Lock()
	for _, leftover := range lineBufs {
		if leftover != "" {
			consumeBootLine(leftover, true)
		}
	}
	clear(lineBufs)
	wasStreamedTelemetry := streamedTelemetry
	wasStreamedLogs := streamedLogs
	hookMu.Unlock()

	if execErr != nil {
		return rollback(execErr)
	}
	if isCancelled(ctx, opts.CancelCh) {
		return rollback(ErrStartCancelled)
	}

	ready := false
	stdoutCombined := combineStdout(outputs)
	for _, line := range splitAllLines(stdoutCombined) {
		stripped := strings.TrimSpace(line)
		if stripped == "COLABONE_READY=1" {
			ready = true
		} else if !wasStreamedTelemetry && opts.OnTelemetry != nil && strings.HasPrefix(stripped, "COLABONE_TELEMETRY=") {
			consumeBootLine(line, false)
		} else if !wasStreamedLogs && (opts.OnLog != nil || notify != nil) && strings.HasPrefix(stripped, "COLABONE_LOG=") {
			consumeBootLine(line, false)
		}
	}

	if !ready {
		bootErr := ""
		for _, out := range outputs {
			if out.OutputType == "error" {
				ename := strings.TrimSpace(out.EName)
				evalue := strings.TrimSpace(out.EValue)
				detail := ""
				if ename != "" && evalue != "" {
					detail = ename + ": " + evalue
				} else if evalue != "" {
					detail = evalue
				} else {
					detail = ename
				}
				if detail != "" {
					bootErr = truncateRunes(p.safeMessage(detail), 240)
					break
				}
			}
		}
		if bootErr != "" {
			emitLog(opts.OnLog, "远端初始化异常："+bootErr, "远端", "error")
			return rollback(errors.New("远端初始化未完成：" + bootErr))
		}
		return rollback(errors.New("远端初始化未完成；可查看 Colab 内核日志"))
	}

	if err := p.SaveRemote(remote, true); err != nil {
		return rollback(err)
	}
	emitLog(opts.OnLog, "远端管理服务引导完成", "远端", "info")
	return cloneMap(remote), nil
}

// UpgradeService hot-updates the remote ColabOne control service in-place via server/vm_update.py,
// preserving cached models and workflows.
func (p *ColabOneProvider) UpgradeService(ctx context.Context, endpoint string, onLog LogFn) error {
	if p.closed.Load() {
		return ErrProviderClosed
	}
	session, err := p.Session(ctx, true, endpoint, false)
	if err != nil {
		return err
	}
	if session == nil {
		return errors.New("请先启动远端 GPU")
	}

	slot, err := p.lockKernelSlot(session.Endpoint)
	if err != nil {
		return err
	}
	defer slot.mu.Unlock()

	zipBytes, fileCount, err := p.buildServiceZip()
	if err != nil {
		return err
	}
	emitLog(onLog, fmt.Sprintf("已打包新版远端服务（%d 个文件），正在上传", fileCount), "客户端", "info")
	session, err = p.uploadBytesWithSlot(ctx, session, "content/colabone-update.zip", zipBytes, 60*time.Second, slot)
	if err != nil {
		return err
	}

	updateScript, err := p.resolveVMUpdateScript(zipBytes)
	if err != nil {
		return err
	}
	emitLog(onLog, "更新包上传完成，开始在远端执行热更新脚本", "远端", "info")

	var hookMu sync.Mutex
	lineBufs := make(map[string]string)
	streamedLogs := false

	consumeUpdateLine := func(rawLine string, fromStream bool) {
		stripped := strings.TrimSpace(rawLine)
		if !strings.HasPrefix(stripped, "COLABONE_LOG=") {
			return
		}
		rawJSON := strings.TrimPrefix(stripped, "COLABONE_LOG=")
		var entry map[string]any
		if json.Unmarshal([]byte(rawJSON), &entry) == nil && entry != nil {
			if fromStream {
				streamedLogs = true
			}
			msg := strings.TrimSpace(strVal(entry["message"]))
			if msg != "" {
				src := strings.TrimSpace(strVal(entry["source"]))
				if src == "" {
					src = "远端"
				}
				lvl := strings.TrimSpace(strVal(entry["level"]))
				if lvl == "" {
					lvl = "info"
				}
				emitLog(onLog, msg, src, lvl)
			}
		}
	}

	var outputHook func(KernelOutput)
	if onLog != nil {
		outputHook = func(output KernelOutput) {
			if output.OutputType == "stream" && output.Name == "stderr" {
				return
			}
			if output.Text == "" {
				return
			}
			streamKey := output.Name
			if streamKey == "" {
				streamKey = "stdout"
			}
			hookMu.Lock()
			defer hookMu.Unlock()
			combined := lineBufs[streamKey] + output.Text
			parts := splitLinesKeepEnds(combined)
			lineBufs[streamKey] = ""
			for _, part := range parts {
				if !strings.HasSuffix(part, "\n") && !strings.HasSuffix(part, "\r") {
					lineBufs[streamKey] = part
					continue
				}
				consumeUpdateLine(part, true)
			}
		}
	}

	outputs, err := p.executeLocked(ctx, slot, session, updateScript, ExecuteOptions{
		Timeout:    210 * time.Second,
		AllowStdin: false,
		OutputHook: outputHook,
	})
	hookMu.Lock()
	for _, leftover := range lineBufs {
		if leftover != "" {
			consumeUpdateLine(leftover, true)
		}
	}
	clear(lineBufs)
	wasStreamedLogs := streamedLogs
	hookMu.Unlock()

	if err != nil {
		return err
	}
	if !wasStreamedLogs && onLog != nil {
		for _, o := range outputs {
			for _, line := range splitAllLines(o.Text) {
				consumeUpdateLine(line, false)
			}
		}
	}
	combined := combineStdout(outputs)
	updated := strings.Contains(combined, "COLABONE_UPDATED=1")
	if !updated {
		for _, o := range outputs {
			if strings.Contains(o.Text, "COLABONE_UPDATED=1") {
				updated = true
				break
			}
		}
	}
	if !updated {
		return errors.New("远端服务更新未完成，缓存文件仍保留，请重试或下次启动时更新")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Kernel RPC tunnel
// ---------------------------------------------------------------------------

func (p *ColabOneProvider) isSlotDetached(endpoint string, slot *kernelSlot) bool {
	if slot == nil || slot.clearing.Load() {
		return true
	}
	p.kernelMu.Lock()
	cur := p.kernels[endpoint]
	p.kernelMu.Unlock()
	return cur != slot
}

// RPC tunnels an authenticated HTTP request to the loopback ColabOne remote service
// (http://127.0.0.1:7860/api/v1/<path>) over Colab's Jupyter kernel channel.
// Non-nil bodies are uploaded as ephemeral files in /content/ before acquiring the
// per-endpoint kernel lock (so multi-minute audio uploads never block Heartbeat,
// Telemetry, Cancel, or Clear) and unlinked after execution.
func (p *ColabOneProvider) RPC(
	ctx context.Context,
	method string,
	path string,
	body any,
	endpoint string,
	timeout time.Duration,
) (any, error) {
	if p.closed.Load() {
		return nil, ErrProviderClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	session, err := p.Session(ctx, false, endpoint, false)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, errors.New("请先启动远端 GPU")
	}

	upperMethod := strings.ToUpper(strings.TrimSpace(method))
	idempotent := upperMethod == http.MethodGet ||
		path == "drive/connect" ||
		path == "drive/sync" ||
		path == "configure"

	if timeout <= 0 {
		timeout = 75 * time.Second
	}
	execSec := int(timeout.Seconds())
	if execSec < 30 {
		execSec = 30
	}
	execTimeout := time.Duration(execSec) * time.Second
	httpTimeoutSec := execSec - 10
	if httpTimeoutSec < 20 {
		httpTimeoutSec = 20
	}

	uploadSec := execSec
	if uploadSec < 45 {
		uploadSec = 45
	}
	if uploadSec > 300 {
		uploadSec = 300
	}
	uploadTimeout := time.Duration(uploadSec) * time.Second

	var bodyBytes []byte
	if body != nil {
		bodyBytes, err = marshalJSONNoEscapeHTML(body)
		if err != nil {
			return nil, err
		}
	}

	targetEP := session.Endpoint
	// Obtain (or register) the slot reference WITHOUT locking slot.mu so large uploads
	// never stall concurrent Heartbeat/Telemetry/Cancel/Clear while still detecting
	// Clear() or eviction racing with the upload or between retries.
	originSlot, err := p.kernelSlot(targetEP)
	if err != nil {
		return nil, err
	}

	maxAttempts := 1
	if idempotent {
		maxAttempts = 2
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if p.closed.Load() {
			return nil, ErrProviderClosed
		}
		if p.isSlotDetached(targetEP, originSlot) {
			return nil, errors.New("请先启动远端 GPU")
		}

		// Phase 1 (unlocked): pre-refresh proxy token when expiring within
		// max(60s, attemptUploadTimeout+30s) and upload ephemeral JSON body BEFORE
		// acquiring slot.mu.
		attemptUploadTimeout := uploadTimeout
		if dl, ok := ctx.Deadline(); ok {
			rem := time.Until(dl)
			if rem <= 0 {
				return nil, context.DeadlineExceeded
			}
			if rem < attemptUploadTimeout {
				attemptUploadTimeout = rem
			}
		}
		attemptPreRefreshMargin := max(60*time.Second, attemptUploadTimeout+30*time.Second)

		session, err = p.session(ctx, false, targetEP, false, nil)
		if err != nil {
			return nil, err
		}
		if session != nil && (session.TokenExpiresAt == nil || !session.TokenExpiresAt.After(p.now().Add(attemptPreRefreshMargin))) {
			// Refresh before uploading or executing. Retrying a failed POST
			// could duplicate a model download, generation or release.
			session, err = p.session(ctx, true, session.Endpoint, true, nil)
			if err != nil {
				return nil, err
			}
		}
		if session == nil {
			return nil, errors.New("请先启动远端 GPU")
		}
		if p.closed.Load() {
			return nil, ErrProviderClosed
		}
		if p.isSlotDetached(targetEP, originSlot) {
			return nil, errors.New("请先启动远端 GPU")
		}

		requestPath := ""
		remoteRel := ""
		if body != nil {
			if dl, ok := ctx.Deadline(); ok {
				rem := time.Until(dl)
				if rem <= 0 {
					return nil, context.DeadlineExceeded
				}
				if rem < attemptUploadTimeout {
					attemptUploadTimeout = rem
				}
			}
			remoteName := "colabone-rpc-" + newUUIDHex() + ".json"
			remoteRel = "content/" + remoteName
			session, err = p.uploadBytes(ctx, session, remoteRel, bodyBytes, attemptUploadTimeout)
			if err != nil {
				return nil, err
			}
			requestPath = "/content/" + remoteName
		}
		prevSession := session
		cleanupRemote := func(sess *ColabOneSessionState) {
			if remoteRel == "" || p.closed.Load() {
				return
			}
			targetSess := sess
			if targetSess == nil {
				targetSess = prevSession
			}
			if targetSess == nil {
				return
			}
			delCtx, delCancel := context.WithTimeout(context.Background(), 3*time.Second)
			stopDelWatch := p.cancelOnClose(delCancel)
			p.deleteRemoteFile(delCtx, targetSess, remoteRel)
			stopDelWatch()
			delCancel()
		}

		if p.closed.Load() {
			return nil, ErrProviderClosed
		}
		if p.isSlotDetached(targetEP, originSlot) {
			cleanupRemote(session)
			if p.closed.Load() {
				return nil, ErrProviderClosed
			}
			return nil, errors.New("请先启动远端 GPU")
		}

		// Phase 2 (locked): acquire slot.mu ONLY around kernel execution & state.
		slot, lockErr := p.lockKernelSlot(targetEP)
		if lockErr != nil {
			cleanupRemote(session)
			if p.closed.Load() {
				return nil, ErrProviderClosed
			}
			return nil, lockErr
		}
		if slot != originSlot || slot.evicted || slot.clearing.Load() {
			slot.mu.Unlock()
			cleanupRemote(session)
			if p.closed.Load() {
				return nil, ErrProviderClosed
			}
			return nil, errors.New("请先启动远端 GPU")
		}

		latestSess, sessErr := p.session(ctx, false, targetEP, false, slot)
		if sessErr != nil {
			slot.mu.Unlock()
			cleanupRemote(latestSess)
			if p.closed.Load() {
				return nil, ErrProviderClosed
			}
			return nil, sessErr
		}
		if latestSess == nil {
			slot.mu.Unlock()
			cleanupRemote(nil)
			if p.closed.Load() {
				return nil, ErrProviderClosed
			}
			return nil, errors.New("请先启动远端 GPU")
		}
		session = latestSess
		if session.TokenExpiresAt == nil || !session.TokenExpiresAt.After(p.now().Add(60*time.Second)) {
			prevSession = session
			session, err = p.session(ctx, true, session.Endpoint, true, slot)
			if err != nil {
				slot.mu.Unlock()
				cleanupRemote(session)
				if p.closed.Load() {
					return nil, ErrProviderClosed
				}
				return nil, err
			}
			if session == nil {
				slot.mu.Unlock()
				cleanupRemote(nil)
				if p.closed.Load() {
					return nil, ErrProviderClosed
				}
				return nil, errors.New("请先启动远端 GPU")
			}
		}

		attemptExecTimeout := execTimeout
		attemptHTTPTimeoutSec := httpTimeoutSec
		if err := ctx.Err(); err != nil {
			slot.mu.Unlock()
			cleanupRemote(session)
			if p.closed.Load() {
				return nil, ErrProviderClosed
			}
			return nil, err
		}
		if dl, ok := ctx.Deadline(); ok {
			rem := time.Until(dl)
			if rem <= 0 {
				slot.mu.Unlock()
				cleanupRemote(session)
				if p.closed.Load() {
					return nil, ErrProviderClosed
				}
				return nil, context.DeadlineExceeded
			}
			if rem < attemptExecTimeout {
				attemptExecTimeout = rem
				remSec := int(rem / time.Second)
				if remSec > 2 {
					remSec--
				}
				if remSec < 1 {
					remSec = 1
				}
				if remSec < attemptHTTPTimeoutSec {
					attemptHTTPTimeoutSec = remSec
				}
			}
		}

		code := buildRPCCode(requestPath, path, method, attemptHTTPTimeoutSec)
		outputs, execErr := p.executeLocked(ctx, slot, session, code, ExecuteOptions{
			Timeout:    attemptExecTimeout,
			AllowStdin: false,
		})
		if execErr != nil {
			if p.closed.Load() {
				slot.mu.Unlock()
				return nil, ErrProviderClosed
			}
			if slot.evicted || slot.clearing.Load() {
				slot.mu.Unlock()
				cleanupRemote(session)
				if p.closed.Load() {
					return nil, ErrProviderClosed
				}
				return nil, errors.New("请先启动远端 GPU")
			}
			status := httpStatusCode(execErr)
			shouldRetry := idempotent && attempt == 0 && ctx.Err() == nil && !errors.Is(execErr, context.DeadlineExceeded) && !slot.evicted && !slot.clearing.Load() && (isTransientWSError(execErr) || status == 401 || status == 403)
			delSess := session
			if shouldRetry {
				p.closeKernelSlotLocked(slot)
				if status == 401 || status == 403 {
					if refreshedSess, refreshErr := p.session(ctx, true, session.Endpoint, true, slot); refreshErr == nil && refreshedSess != nil {
						delSess = refreshedSess
					}
				}
				slot.mu.Unlock()
				cleanupRemote(delSess)
				if p.closed.Load() {
					return nil, ErrProviderClosed
				}
				continue
			}
			slot.mu.Unlock()
			cleanupRemote(delSess)
			if p.closed.Load() {
				return nil, ErrProviderClosed
			}
			return nil, execErr
		}

		stdoutCombined := combineStdout(outputs)
		for _, line := range splitAllLines(stdoutCombined) {
			if strings.HasPrefix(line, "COLABONE_RPC=") {
				rawJSON := strings.TrimPrefix(line, "COLABONE_RPC=")
				var res struct {
					OK    bool `json:"ok"`
					Value any  `json:"value"`
					Error any  `json:"error"`
				}
				dec := json.NewDecoder(strings.NewReader(rawJSON))
				dec.UseNumber()
				if err := dec.Decode(&res); err != nil {
					slot.mu.Unlock()
					return nil, err
				}
				slot.mu.Unlock()
				if !res.OK {
					errText := ""
					switch v := res.Error.(type) {
					case string:
						errText = v
					case nil:
						errText = "远端请求失败"
					default:
						if b, mErr := json.Marshal(v); mErr == nil {
							errText = string(b)
						} else {
							errText = fmt.Sprint(v)
						}
					}
					return nil, errors.New(truncateRunes(p.safeMessage(errText), 500))
				}
				return res.Value, nil
			}
		}

		delSess := session
		if idempotent && attempt == 0 && !slot.evicted && !slot.clearing.Load() && ctx.Err() == nil && !p.closed.Load() {
			p.closeKernelSlotLocked(slot)
			slot.mu.Unlock()
			cleanupRemote(delSess)
			if p.closed.Load() {
				return nil, ErrProviderClosed
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		slot.mu.Unlock()
		cleanupRemote(delSess)
		if p.closed.Load() {
			return nil, ErrProviderClosed
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("Colab 控制连接没有返回结果，请检查网络后重试")
	}

	return nil, errors.New("Colab 控制连接没有返回结果，请检查网络后重试")
}

func buildRPCCode(requestPath, apiPath, method string, httpTimeoutSec int) string {
	return fmt.Sprintf(`
import json as _co_json, threading as _co_thr, urllib.request as _co_http
from pathlib import Path as _co_Path
_co_file = %s
_co_box = {}
try:
    _co_cfg = _co_json.loads(_co_Path('/content/colabone-boot.json').read_text(encoding="utf-8"))
    _co_body = _co_Path(_co_file).read_bytes() if _co_file else None
    _co_req = _co_http.Request('http://127.0.0.1:7860/api/v1/' + %s, data=_co_body, method=%s,
        headers={'Authorization': 'Bearer ' + _co_cfg['token'], 'Content-Type': 'application/json'})
    def _co_do():
        try:
            with _co_http.build_opener(_co_http.ProxyHandler({})).open(_co_req, timeout=%d) as _co_response:
                _co_box['res'] = {'ok':True, 'value':_co_json.loads(_co_response.read())}
        except _co_http.HTTPError as _co_error:
            _co_raw = _co_error.read()
            try:
                _co_detail = _co_json.loads(_co_raw).get('detail', '远端请求失败')
            except Exception:
                _co_detail = f'远端服务返回 HTTP {_co_error.code}'
            _co_box['res'] = {'ok':False, 'error':_co_detail}
        except Exception as _co_exc:
            _co_box['res'] = {'ok':False, 'error':str(_co_exc)[:300]}
    _co_t = _co_thr.Thread(target=_co_do, daemon=True)
    _co_t.start()
    while _co_t.is_alive():
        _co_t.join(5.0)
        if _co_t.is_alive():
            print('COLABONE_RPC_PING=1', flush=True)
    _co_result = _co_box.get('res') or {'ok':False, 'error':'远端服务未返回响应'}
    print('COLABONE_RPC=' + _co_json.dumps(_co_result, ensure_ascii=False), flush=True)
finally:
    if _co_file: _co_Path(_co_file).unlink(missing_ok=True)
`, pyStringLiteral(requestPath), pyStringLiteral(apiPath), pyStringLiteral(method), httpTimeoutSec)
}

// ---------------------------------------------------------------------------
// Heartbeat & one-time VM location probe
// ---------------------------------------------------------------------------

const vmLocationProbeCode = `
import json as _co_j, urllib.request as _co_u
def _co_loc():
    _op = _co_u.build_opener(_co_u.ProxyHandler({}))
    try:
        with _op.open(_co_u.Request('http://metadata.google.internal/computeMetadata/v1/instance/zone', headers={'Metadata-Flavor':'Google'}), timeout=1.5) as _r:
            if _r.status == 200:
                return {'zone': _r.read().decode('utf-8','replace').strip()}
    except Exception:
        pass
    for _url in ('https://ipinfo.io/json', 'https://api.country.is/'):
        try:
            with _op.open(_url, timeout=3) as _r:
                if _r.status == 200:
                    return _co_j.loads(_r.read())
        except Exception:
            pass
    try:
        with _op.open('https://www.cloudflare.com/cdn-cgi/trace', timeout=3) as _r:
            for _ln in _r.read().decode('utf-8','replace').splitlines():
                if _ln.startswith('loc='):
                    return {'country': _ln[4:].strip()}
    except Exception:
        pass
    return {}
print('COLABONE_LOC=' + _co_j.dumps(_co_loc()))
`

// Heartbeat sends the TFE keep-alive ping, performs a one-time VM geographic location probe
// when location is still unknown, and executes a lightweight kernel heartbeat.
func (p *ColabOneProvider) Heartbeat(ctx context.Context, endpoint string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if p.closed.Load() {
		return ErrProviderClosed
	}
	session, err := p.Session(ctx, true, endpoint, false)
	if err != nil {
		return err
	}
	if session == nil {
		return nil
	}

	// Run the external HTTP TFE KeepAlive BEFORE acquiring slot.mu so concurrent
	// RPCs on this endpoint are never stalled behind 10s TFE network latency.
	client, err := p.regularClient()
	if err != nil {
		return err
	}
	kaCtx, kaCancel := context.WithTimeout(ctx, 10*time.Second)
	kaErr := client.KeepAlive(kaCtx, session.Endpoint)
	kaCancel()
	if kaErr != nil {
		// Mirror Python keep-alive semantics: non-2xx HTTP responses on /keep-alive/
		// are non-fatal; the kernel execution below is the authoritative liveness check.
		if !isColabRequestError(kaErr) {
			return kaErr
		}
	}

	slot, err := p.lockKernelSlot(session.Endpoint)
	if err != nil {
		return err
	}

	rem := p.RemoteInfo(session.Endpoint)
	p.stateMu.Lock()
	alreadyProbed := p.locProbed[session.Endpoint]
	p.stateMu.Unlock()

	var discoveredLoc map[string]any
	ranKernelCheck := false
	if len(rem) > 0 && isEmptyLocation(rem["location"]) && !alreadyProbed {
		loc := p.inferLocationFromEndpoint(session.Endpoint, session.URL)
		if len(loc) == 0 {
			outputs, execErr := p.executeLocked(ctx, slot, session, vmLocationProbeCode, ExecuteOptions{
				Timeout:    30 * time.Second,
				AllowStdin: false,
			})
			if execErr != nil {
				slot.mu.Unlock()
				return execErr
			}
			ranKernelCheck = true
			stdoutCombined := combineStdout(outputs)
			for _, line := range splitAllLines(stdoutCombined) {
				if strings.HasPrefix(line, "COLABONE_LOC=") {
					var raw map[string]any
					if json.Unmarshal([]byte(strings.TrimPrefix(line, "COLABONE_LOC=")), &raw) == nil && raw != nil {
						loc = p.locationFromZone(strVal(raw["zone"]))
						if len(loc) == 0 {
							loc = p.normalizeGeoPayload(
								strVal(raw["country"]),
								strVal(raw["region"]),
								strVal(raw["city"]),
								"",
							)
						}
					}
				}
			}
		}
		p.stateMu.Lock()
		p.locProbed[session.Endpoint] = true
		p.stateMu.Unlock()

		if len(loc) > 0 && strVal(rem["endpoint"]) == session.Endpoint {
			discoveredLoc = loc
		}
	}
	if !ranKernelCheck {
		if _, execErr := p.executeLocked(ctx, slot, session, "pass", ExecuteOptions{
			Timeout:    30 * time.Second,
			AllowStdin: false,
		}); execErr != nil {
			slot.mu.Unlock()
			return execErr
		}
	}
	slot.mu.Unlock()

	if len(discoveredLoc) > 0 {
		p.stateMu.Lock()
		sOrder, sessions := p.sessionsOrderedLocked()
		rOrder, remotes := p.remotesOrderedLocked()
		if _, hasSess := sessions[session.Endpoint]; hasSess {
			if curRem, ok := remotes[session.Endpoint]; ok && isEmptyLocation(curRem["location"]) {
				updated := cloneMap(curRem)
				updated["location"] = discoveredLoc
				remotes[session.Endpoint] = updated
				activeEP := p.activeEndpointLocked(sOrder, sessions)
				_ = p.writeStateLocked(sOrder, sessions, rOrder, remotes, activeEP)
			}
		}
		p.stateMu.Unlock()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Release & ReleaseAll
// ---------------------------------------------------------------------------

// Release unassigns a managed Colab runtime and confirms its disappearance from
// ListAssignments for up to 15 polls, retrying unassign at attempts 2 and 6 if needed.
func (p *ColabOneProvider) Release(ctx context.Context, endpoint string, onLog LogFn) (map[string]any, error) {
	session, err := p.Session(ctx, false, endpoint, false)
	if err != nil {
		return nil, err
	}
	if session == nil {
		emitLog(onLog, "本机未找到需要释放的实例会话", "GPU", "info")
		return map[string]any{"ok": true}, nil
	}
	client, err := p.regularClient()
	if err != nil {
		return nil, err
	}
	emitLog(onLog, fmt.Sprintf("查询 Google Colab 实例状态：%s", session.Endpoint), "GPU", "info")
	listed, err := client.ListAssignments(ctx)
	if err != nil {
		return nil, err
	}
	endpoints := assignmentEndpointSet(listed)
	unassignOK := true
	if endpoints[session.Endpoint] {
		emitLog(onLog, fmt.Sprintf("向 Google Colab 发送释放请求：%s", session.Endpoint), "GPU", "info")
		if err := client.Unassign(ctx, session.Endpoint); err != nil {
			// The independent remote guard may already have released it.
			// An HTTP error is not evidence either way: confirm disappearance.
			unassignOK = false
			emitLog(onLog, fmt.Sprintf("释放请求未获直接确认，改为轮询核实停止状态：%s", session.Endpoint), "GPU", "warning")
		}
	} else {
		emitLog(onLog, fmt.Sprintf("实例已不在 Colab 活跃列表中：%s", session.Endpoint), "GPU", "info")
	}

	for attempt := 0; attempt < 15; attempt++ {
		curList, err := client.ListAssignments(ctx)
		if err != nil {
			if isReleaseRetryableError(err) && attempt < 14 {
				if sleepErr := p.sleep(ctx, p.releasePollInterval); sleepErr != nil {
					return nil, sleepErr
				}
				continue
			}
			return nil, err
		}
		curSet := assignmentEndpointSet(curList)
		if !curSet[session.Endpoint] {
			_ = p.Clear(session.Endpoint)
			p.CloseKernelChannel(session.Endpoint, true)
			p.CloseObserver(session.Endpoint, false)
			emitLog(onLog, fmt.Sprintf("已确认实例停止并清理本机会话：%s", session.Endpoint), "GPU", "info")
			return map[string]any{"ok": true}, nil
		}
		if !unassignOK && (attempt == 2 || attempt == 6) {
			if err := client.Unassign(ctx, session.Endpoint); err == nil {
				unassignOK = true
			}
		}
		if attempt == 0 {
			emitLog(onLog, fmt.Sprintf("等待 Google Colab 确认停止计费：%s", session.Endpoint), "GPU", "info")
		} else if attempt == 4 || attempt == 9 {
			emitLog(onLog, fmt.Sprintf("仍在等待释放确认（第 %d/15 次查询）：%s", attempt+1, session.Endpoint), "GPU", "info")
		}
		if attempt < 14 {
			if sleepErr := p.sleep(ctx, p.releasePollInterval); sleepErr != nil {
				return nil, sleepErr
			}
		}
	}
	return nil, errors.New("释放请求已发送，但尚未确认停止计费；请刷新后重试")
}

// ReleaseAll stops an explicit user-reviewed set of Colab endpoints without chasing
// newly created instances, confirming disappearance for up to 15 polls.
func (p *ColabOneProvider) ReleaseAll(ctx context.Context, targets []string, notify StageFn) (map[string]any, error) {
	client, err := p.regularClient()
	if err != nil {
		return nil, err
	}
	targetSet := make(map[string]bool, len(targets))
	for _, t := range targets {
		if t != "" {
			targetSet[t] = true
		}
	}
	emitStage(notify, fmt.Sprintf("正在查询账号运行实例（待停止 %d 个）", len(targetSet)))

	listed, err := client.ListAssignments(ctx)
	if err != nil {
		return nil, err
	}
	current := assignmentEndpointSet(listed)
	toStop := sortedIntersection(targetSet, current)
	if len(toStop) == 0 {
		emitStage(notify, "目标实例均已不在运行列表中")
	}

	unassignFailed := make(map[string]bool)
	for _, ep := range toStop {
		p.CloseObserver(ep, false)
		emitStage(notify, "正在停止实例："+ep)
		if err := client.Unassign(ctx, ep); err != nil {
			unassignFailed[ep] = true
			emitStage(notify, "停止请求未获确认，正在重新查询："+ep)
		}
	}

	confirmed := make(map[string]bool)
	remaining := make(map[string]bool, len(toStop))
	for _, ep := range toStop {
		remaining[ep] = true
	}

	for attempt := 0; attempt < 15; attempt++ {
		curList, err := client.ListAssignments(ctx)
		if err != nil {
			if isReleaseRetryableError(err) && attempt < 14 {
				if sleepErr := p.sleep(ctx, p.releasePollInterval); sleepErr != nil {
					return nil, sleepErr
				}
				continue
			}
			return nil, err
		}
		current = assignmentEndpointSet(curList)

		newlyStopped := make([]string, 0)
		for ep := range targetSet {
			if !current[ep] && !confirmed[ep] {
				newlyStopped = append(newlyStopped, ep)
			}
		}
		sort.Strings(newlyStopped)
		for _, ep := range newlyStopped {
			confirmed[ep] = true
			delete(unassignFailed, ep)
			emitStage(notify, "已确认停止实例："+ep)
		}

		for _, owned := range p.ManagedEndpoints() {
			if targetSet[owned] && !current[owned] {
				_ = p.Clear(owned)
				p.CloseKernelChannel(owned, true)
			}
		}

		remaining = make(map[string]bool)
		for ep := range targetSet {
			if current[ep] {
				remaining[ep] = true
			}
		}
		if len(remaining) == 0 {
			return map[string]any{"ok": true, "remaining": len(current)}, nil
		}

		if len(unassignFailed) > 0 && (attempt == 2 || attempt == 6) {
			for _, ep := range sortedIntersection(unassignFailed, remaining) {
				if err := client.Unassign(ctx, ep); err == nil {
					delete(unassignFailed, ep)
				}
			}
		}
		if attempt == 0 {
			emitStage(notify, fmt.Sprintf("等待 Google Colab 确认停止计费（剩余 %d 个）", len(remaining)))
		}
		if attempt < 14 {
			if sleepErr := p.sleep(ctx, p.releasePollInterval); sleepErr != nil {
				return nil, sleepErr
			}
		}
	}
	return nil, fmt.Errorf("仍有 %d 个实例未确认停止，可能仍在计费，请重试", len(remaining))
}

// ---------------------------------------------------------------------------
// Google Drive config upload & Colab DriveFS mount
// ---------------------------------------------------------------------------

func (p *ColabOneProvider) ensureValidSession(ctx context.Context, endpoint string, minTTL time.Duration) (*ColabOneSessionState, error) {
	session, err := p.Session(ctx, false, endpoint, false)
	if err != nil || session == nil {
		return session, err
	}
	if session.TokenExpiresAt == nil || !session.TokenExpiresAt.After(p.now().Add(minTTL)) {
		return p.Session(ctx, true, session.Endpoint, true)
	}
	return session, nil
}

// UploadDriveConfig uploads a headless rclone configuration file to content/.colabone_rclone.conf.
func (p *ColabOneProvider) UploadDriveConfig(ctx context.Context, rcloneConf string, endpoint string) error {
	session, err := p.ensureValidSession(ctx, endpoint, 60*time.Second)
	if err != nil {
		return err
	}
	if session == nil {
		return errors.New("请先启动远端 GPU")
	}
	_, err = p.uploadBytes(ctx, session, "content/.colabone_rclone.conf", []byte(rcloneConf), 60*time.Second)
	return err
}

// CheckColabDriveFSAuth performs a fast read-only dry-run check of whether Colab DriveFS
// is already authorized for this VM.
func (p *ColabOneProvider) CheckColabDriveFSAuth(ctx context.Context, endpoint string) (bool, string, error) {
	session, err := p.ensureValidSession(ctx, endpoint, 60*time.Second)
	if err != nil {
		return false, "", err
	}
	if session == nil {
		return false, "", nil
	}
	if p.driveMounterFactory == nil {
		return false, "", errors.New("未配置 Google 云盘挂载器")
	}
	client, err := p.regularClient()
	if err != nil {
		return false, "", err
	}
	mount := p.driveMounterFactory(client, session.Endpoint, DriveMountOptions{})
	if mount == nil {
		return false, "", errors.New("未配置 Google 云盘挂载器")
	}
	defer mount.Stop()
	return mount.DryrunCheck(ctx)
}

// MountColabDriveFS mounts Google Drive via official `google.colab.drive.mount` (`dfs_ephemeral`)
// using a dedicated short-lived mount kernel ("colabone-drivefs") so the primary RPC/telemetry
// kernel channel never blocks while awaiting browser confirmation.
func (p *ColabOneProvider) MountColabDriveFS(ctx context.Context, endpoint string, opts DriveMountOptions) (map[string]any, error) {
	if p.closed.Load() {
		return nil, ErrProviderClosed
	}
	totalTimeout := opts.Timeout
	if totalTimeout <= 0 {
		totalTimeout = 300 * time.Second
	}
	if totalTimeout < 60*time.Second {
		totalTimeout = 60 * time.Second
	}

	session, err := p.ensureValidSession(ctx, endpoint, totalTimeout+60*time.Second)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, errors.New("请先启动远端 GPU")
	}
	if p.driveMounterFactory == nil {
		return nil, errors.New("未配置 Google 云盘挂载器")
	}
	if p.kernelFactory == nil {
		return nil, errors.New("未配置远端内核客户端工厂")
	}
	client, err := p.regularClient()
	if err != nil {
		return nil, err
	}

	if ctx == nil {
		ctx = context.Background()
	}
	rt := p.kernelFactory(KernelOptions{
		ServerURL:           session.URL,
		ProxyToken:          session.Token,
		SessionName:         "colabone-drivefs",
		ShutdownOnWSTimeout: true,
	})
	if rt == nil {
		return nil, errors.New("未配置远端内核客户端工厂")
	}

	authTimeout := totalTimeout - 45*time.Second
	if authTimeout < 30*time.Second {
		authTimeout = 30 * time.Second
	}
	if authTimeout > 240*time.Second {
		authTimeout = 240 * time.Second
	}
	mountOpts := opts
	mountOpts.Timeout = authTimeout
	mountHelper := p.driveMounterFactory(client, session.Endpoint, mountOpts)
	if mountHelper == nil {
		return nil, errors.New("未配置 Google 云盘挂载器")
	}
	var stopMountOnce sync.Once
	stopMountHelper := func() {
		stopMountOnce.Do(mountHelper.Stop)
	}
	defer stopMountHelper()

	timeoutMS := int(totalTimeout.Milliseconds())
	mountCode := fmt.Sprintf(
		"import os\n"+
			"from google.colab import drive\n"+
			"drive.mount('/content/drive', timeout_ms=%d)\n"+
			"ok = os.path.isdir('/content/drive/MyDrive') or os.path.isdir('/content/drive/My Drive')\n"+
			"print('\\nCOLABONE_DRIVEFS_OK=' + ('1' if ok else '0'), flush=True)\n",
		timeoutMS,
	)

	execCtx, cancelExec := context.WithCancel(ctx)
	stopWatch := make(chan struct{})
	var watchWG sync.WaitGroup
	closedCh := p.closedCh
	if opts.CancelCh != nil || ctx.Done() != nil || closedCh != nil {
		watchWG.Add(1)
		go func() {
			defer watchWG.Done()
			select {
			case <-stopWatch:
				return
			case <-closedCh:
				cancelExec()
				rt.Abort()
				stopMountHelper()
			case <-opts.CancelCh:
				cancelExec()
				rt.Abort()
				stopMountHelper()
			case <-ctx.Done():
				cancelExec()
				rt.Abort()
				stopMountHelper()
			}
		}()
	}

	var outputs []KernelOutput
	var execErr error
	func() {
		defer func() {
			close(stopWatch)
			watchWG.Wait()
			rt.Abort()
			stopMountHelper()
			cancelExec()
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
			_ = rt.Stop(stopCtx, true)
			stopCancel()
		}()
		if p.closed.Load() {
			execErr = ErrProviderClosed
			return
		}
		if err := rt.Start(execCtx); err != nil {
			execErr = err
			return
		}
		outputs, execErr = rt.ExecuteCode(execCtx, mountCode, ExecuteOptions{
			Timeout:          totalTimeout,
			AllowStdin:       true,
			StdinHook:        func(string) string { return "" },
			ColabRequestHook: mountHelper.Hook,
		})
	}()

	if p.closed.Load() {
		return nil, ErrProviderClosed
	}
	if execErr != nil {
		if errors.Is(execErr, ErrProviderClosed) {
			return nil, ErrProviderClosed
		}
		if mErr := mountHelper.Err(); mErr != nil {
			return nil, mErr
		}
		if isCancelled(ctx, opts.CancelCh) {
			return nil, errors.New("已取消 Google 云盘挂载")
		}
		return nil, errors.New("Google Colab 原生云盘挂载连接中断，请重试：" + truncateRunes(p.safeMessage(execErr.Error()), 200))
	}
	if mErr := mountHelper.Err(); mErr != nil {
		return nil, mErr
	}

	stdoutCombined := combineStdout(outputs)
	mounted := false
	for _, line := range splitAllLines(stdoutCombined) {
		if strings.TrimSpace(line) == "COLABONE_DRIVEFS_OK=1" {
			mounted = true
			break
		}
	}
	errText := ""
	for _, out := range outputs {
		if out.OutputType == "error" {
			evalue := strings.TrimSpace(out.EValue)
			ename := strings.TrimSpace(out.EName)
			if evalue != "" {
				errText = evalue
			} else if ename != "" {
				errText = ename
			}
		}
	}
	if !mounted {
		if errText != "" {
			return nil, errors.New("Google Colab 原生云盘挂载失败：" + truncateRunes(p.safeMessage(errText), 220))
		}
		return nil, errors.New("Google Colab 原生云盘未能完成挂载，请重试")
	}
	return map[string]any{
		"ok":         true,
		"mount_path": "/content/drive/MyDrive",
		"mode":       "colab_drivefs",
	}, nil
}

// ---------------------------------------------------------------------------
// Error classification & pure utility helpers
// ---------------------------------------------------------------------------

func httpStatusCode(err error) int {
	if err == nil {
		return 0
	}
	var coder HTTPStatusCoder
	if errors.As(err, &coder) {
		return coder.HTTPStatusCode()
	}
	var alt interface{ StatusCode() int }
	if errors.As(err, &alt) {
		return alt.StatusCode()
	}
	return 0
}

func isColabRequestError(err error) bool {
	if err == nil {
		return false
	}
	var cre *ColabRequestError
	if errors.As(err, &cre) {
		return true
	}
	return httpStatusCode(err) > 0
}

func isAssignProxyFastFailError(err error) bool {
	if err == nil {
		return false
	}
	var proxyErr interface{ ProxyErrorType() string }
	if errors.As(err, &proxyErr) {
		switch strings.ToLower(strings.TrimSpace(proxyErr.ProxyErrorType())) {
		case "proxy", "ssl", "connect_timeout", "auth", "timeout":
			return true
		case "read_timeout":
			return false
		}
	}
	return assignproxy.IsProxyFailFastError(err)
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var proxyErr interface{ ProxyErrorType() string }
	if errors.As(err, &proxyErr) {
		k := strings.ToLower(proxyErr.ProxyErrorType())
		if k == "timeout" || k == "connect_timeout" || k == "read_timeout" {
			return true
		}
	}
	return false
}

func isNetworkOrTransportError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	var proxyErr interface{ ProxyErrorType() string }
	if errors.As(err, &proxyErr) {
		return true
	}
	var te interface{ IsTransportError() bool }
	if errors.As(err, &te) && te.IsTransportError() {
		return true
	}
	return false
}

func isReleaseRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	return isColabRequestError(err) || isNetworkOrTransportError(err)
}

func isTransientWSError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, jupyterws.ErrKernelAborted) {
		return false
	}
	raw := err.Error()
	// Kernel code execution timeout must NOT be retried (avoids 2x 75s stall and duplicate execution).
	if strings.Contains(raw, "Colab 远端内核执行超时") {
		return false
	}
	msg := strings.ToLower(raw)
	if strings.Contains(msg, "connection aborted") {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	for _, needle := range transientWSNeedles {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// splitAllLines splits s on \r\n, \n, and bare \r (matching Python str.splitlines()).
func splitAllLines(s string) []string {
	if s == "" {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\r' {
			lines = append(lines, s[start:i])
			if i+1 < len(s) && s[i+1] == '\n' {
				i++
			}
			start = i + 1
		} else if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func hasPositiveDelay(delays []time.Duration) bool {
	for _, d := range delays {
		if d > 0 {
			return true
		}
	}
	return false
}

func combineStdout(outputs []KernelOutput) string {
	var b strings.Builder
	for _, out := range outputs {
		if out.OutputType == "stream" && out.Name == "stderr" {
			continue
		}
		if out.Text != "" {
			b.WriteString(out.Text)
		}
	}
	return b.String()
}

func splitLinesKeepEnds(s string) []string {
	if s == "" {
		return nil
	}
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\r' {
			if i+1 < len(s) && s[i+1] == '\n' {
				parts = append(parts, s[start:i+2])
				i++
				start = i + 1
			} else {
				parts = append(parts, s[start:i+1])
				start = i + 1
			}
		} else if s[i] == '\n' {
			parts = append(parts, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		parts = append(parts, s[start:])
	}
	return parts
}

func formatLocationShort(loc map[string]any) string {
	if len(loc) == 0 {
		return ""
	}
	country := strings.TrimSpace(strVal(loc["country_name"]))
	if country == "" {
		country = strings.TrimSpace(strVal(loc["country"]))
	}
	region := strings.TrimSpace(strVal(loc["region"]))
	if country != "" && region != "" && region != country {
		return country + " · " + region
	}
	if country != "" {
		return country
	}
	return region
}

func isEmptyLocation(v any) bool {
	if v == nil {
		return true
	}
	if m, ok := v.(map[string]any); ok {
		return len(m) == 0
	}
	return false
}

func assignmentEndpointSet(list []ListedAssignment) map[string]bool {
	set := make(map[string]bool, len(list))
	for _, a := range list {
		if a.Endpoint != "" {
			set[a.Endpoint] = true
		}
	}
	return set
}

func sortedIntersection(a, b map[string]bool) []string {
	out := make([]string, 0)
	for k := range a {
		if b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	count := 0
	for i := range s {
		if count == max {
			return s[:i]
		}
		count++
	}
	return s
}

func sanitizeFloat(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0.0
	}
	return v
}

func unixFloat(t time.Time) float64 {
	return float64(t.UnixNano()) / 1e9
}

func strVal(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func boolVal(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func floatVal(v any) float64 {
	switch n := v.(type) {
	case float64:
		return sanitizeFloat(n)
	case float32:
		return sanitizeFloat(float64(n))
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return sanitizeFloat(f)
		}
	}
	return 0.0
}

func intVal(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
	}
	return 0
}

func cloneMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		if nested, ok := v.(map[string]any); ok {
			dst[k] = cloneMap(nested)
		} else {
			dst[k] = v
		}
	}
	return dst
}

func marshalJSONNoEscapeHTML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

func pyStringLiteral(s string) string {
	b, err := marshalJSONNoEscapeHTML(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

func newUUIDBytes() [16]byte {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return b
}

func newUUIDv4() string {
	b := newUUIDBytes()
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func newUUIDHex() string {
	b := newUUIDBytes()
	return hex.EncodeToString(b[:])
}

func randomURLSafeToken(nbytes int) (string, error) {
	buf := make([]byte, nbytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
