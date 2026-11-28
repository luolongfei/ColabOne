package controller

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/luolongfei/ColabOne/console/internal/comfyui"
	"github.com/luolongfei/ColabOne/console/internal/outboundlog"
)

type taskActivitySig struct {
	Kind    string
	State   string
	Bucket  int
	Message string
}

type instanceCache struct {
	Runtime                map[string]any
	Models                 []map[string]any
	Workflows              []map[string]any
	TtsEngines             []map[string]any
	ComfyOutputs           []any
	LiveAvatars            []map[string]any
	LiveDrivingMotions     []map[string]any
	LiveCatalog            map[string]any
	RemoteWorkflowVersions map[string]float64
	LastHeartbeat          float64
	LastConfig             float64
	LastLegacyKeepalive    float64
}

type releasePlanState struct {
	ID         string
	Instances  []map[string]any
	AccountSub string
}

// ColabOneController coordinates desktop persistence, multi-instance Colab runtimes,
// Drive dual-mode mounting, model libraries, ComfyUI Studio, Public API, and telemetry monitoring.
type ColabOneController struct {
	root            string
	directory       string
	shortcutSandbox string

	vault       VaultStore
	auth        AuthClient
	provider    ProviderClient
	activity    ActivityLogger
	hub         HubClient
	models      ModelResolver
	comfy       ComfyValidator
	publicAPI   PublicAPIValidator
	assignProxy AssignProxyHelper
	shortcuts     ShortcutManager
	bundler       ServiceBundler
	location      LocationInferer
	onFocusWindow func()

	monitorInterval    time.Duration
	observerInterval   time.Duration
	canvasWaitInterval time.Duration
	upgradePollDelay   time.Duration
	startRetryDelay    time.Duration

	// Lock ordering: vaultConfigMu -> persistMu -> mu -> monitorMu (never invert).
	vaultConfigMu           sync.Mutex
	persistMu               sync.Mutex
	mu                      sync.Mutex
	settings                map[string]any
	data                    map[string]any
	instanceStates          map[string]*instanceCache
	commonIDs               map[string]bool
	customModels            map[string]map[string]any
	apiIDOverrides          map[string]string
	commonRepos             []map[string]any
	defaultCommonVersion    int
	customWorkflows         map[string]map[string]any
	workflowDrafts          map[string]*ColabOneWorkflowDraft
	remoteWorkflowVersions  map[string]float64
	taskActivity            map[string]taskActivitySig
	taskLogCursor           map[string]int64
	lastAuth                float64
	lastConfig              float64
	lastHeartbeat           float64
	lastLegacyKeepalive      float64
	lastCUReleasePoll        float64
	lastCUAutoReleaseAttempt float64
	pendingTouch            bool
	lastTouch               float64
	expectedServiceRev      string
	presetsOnce             sync.Once
	cachedRawPresets        []map[string]any
	builtinWFsOnce          sync.Once
	cachedBuiltinWFs        []map[string]any
	cachedBuiltinTtsEngines []map[string]any
	upgradeAttempted        map[string]bool
	releasePlan             *releasePlanState

	opBusy           atomic.Bool
	opGen            atomic.Uint64
	accountGen       atomic.Uint64
	touchBusy        atomic.Bool
	observerPollBusy atomic.Bool

	stop         *ColabOneEvent
	startCancel  *ColabOneEvent
	driveCancel  *ColabOneEvent
	driveConfirm *ColabOneEvent
	driveProbe   *ColabOneEvent
	observerWake *ColabOneEvent

	monitorMu            sync.Mutex
	monitorMode          string
	monitorEndpoint      string
	monitorPaused        bool
	monitorGen           uint64
	externalObservations map[string]map[string]any

	cuUsage *ColabOneCUUsageStore

	ttsPreviewCacheMu     sync.Mutex
	ttsPreviewIndex       *colabOneLocalTtsPreviewIndex
	pendingTtsPreviewReqs map[string]colabOnePendingTtsEntry
	pendingTtsPrefetchCtx map[string]colabOnePendingTtsEntry
	pendingTtsSeq         uint64

	outputCacheMu           sync.Mutex
	outputCacheIndex        *colabOneOutputCacheIndex
	outputCacheIndexDirty   bool
	outputCacheLastFlushed  time.Time
	outputCacheInflight     map[string]*colabOneOutputCacheFlight
	outputCacheEvictGen     map[string]uint64
	outputCacheResetGen     uint64
	outputCacheMaxBytes     int64
	outputCacheMaxEntries   int
	outputCacheMaxFileBytes int64

	background                              bool
	driveOutputs                            map[string]map[string]*ColabOneDriveOutputRecord
	driveOutputsDeleted                     map[string]map[string]*ColabOneDriveOutputTombstone
	driveOutputsSyncSem                     chan struct{}
	driveOutputsSyncing                     bool
	driveOutputsSyncAccountKey              string
	driveOutputsSyncDeleted                 map[string]map[string]bool
	driveOutputsSyncDeletedFileIDs          map[string]map[string]bool
	driveOutputsSyncClearedWorkspaceIDs     map[string]map[string]bool
	driveOutputsSyncClearedWorkspaceFileIDs map[string]map[string]bool
	driveOutputsSyncAdded                   map[string]map[string]bool
	driveOutputsLastSynced         float64
	driveOutputsLastSyncAttempt    float64
	driveOutputsSyncError          string
	driveAPIBaseURL                string
	driveHTTPClient                *http.Client
	driveHTTPClientOwned           bool
	driveStreamHTTPClient          *http.Client
	outboundLogger                 *outboundlog.ColabOneOutboundLogger
	outboundLoggerOwned            bool

	// Optional overrides for unit tests; zero values use production defaults.
	workflowDraftsMaxCount         int
	workflowDraftsMaxTotalBytes    int64
	livePostMutationRefreshTimeout time.Duration
	shutdownRollbackTimeout        time.Duration

	rootCtx    context.Context
	cancelRoot context.CancelFunc
	spawnMu    sync.Mutex
	closed     bool
	closeOnce  sync.Once
	wg         sync.WaitGroup
}

func sameDirPath(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return a == b
	}
	if absA, err := filepath.Abs(a); err == nil {
		a = absA
	}
	if absB, err := filepath.Abs(b); err == nil {
		b = absB
	}
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// NewColabOneController initializes a ColabOneController with persisted settings, library,
// and workflows, optionally starting background monitor goroutines.
func NewColabOneController(cfg ColabOneControllerConfig) *ColabOneController {
	defaultDir := DefaultConfigDir()
	configDir := strings.TrimSpace(cfg.ConfigDir)
	customConfigDir := configDir != "" && !sameDirPath(configDir, defaultDir)
	if configDir == "" {
		configDir = defaultDir
	}

	shortcutSandbox := strings.TrimSpace(cfg.ShortcutSandbox)
	if shortcutSandbox == "" {
		if customConfigDir {
			shortcutSandbox = filepath.Join(configDir, "shortcuts")
		} else if envSandbox := strings.TrimSpace(os.Getenv("COLABONE_SHORTCUT_DIR")); envSandbox != "" {
			shortcutSandbox = envSandbox
		}
	}

	vault := cfg.Vault
	if vault == nil {
		vault = NewColabOneMemoryVault()
	}
	activity := cfg.Activity
	if activity == nil {
		activity = NewColabOneActivityLog(filepath.Join(configDir, "activity.log"), 300, 256*1024)
	}
	bundler := cfg.ServiceBundler
	if bundler == nil {
		bundler = fsServiceBundler{root: cfg.Root}
	}

	monInterval := cfg.MonitorInterval
	if monInterval <= 0 {
		monInterval = 2 * time.Second
	}
	obsInterval := cfg.ObserverInterval
	if obsInterval <= 0 {
		obsInterval = 1 * time.Second
	}
	canvasWait := cfg.CanvasWaitInterval
	if canvasWait <= 0 {
		canvasWait = 1 * time.Second
	}
	upgradeDelay := cfg.UpgradePollDelay
	if upgradeDelay <= 0 {
		upgradeDelay = 300 * time.Millisecond
	}
	startRetryDelay := cfg.StartRetryDelay
	if startRetryDelay <= 0 {
		startRetryDelay = 1500 * time.Millisecond
	}

	rootCtx, cancelRoot := context.WithCancel(context.Background())

	c := &ColabOneController{
		root:                   cfg.Root,
		directory:              configDir,
		shortcutSandbox:        shortcutSandbox,
		vault:                  vault,
		auth:                   cfg.Auth,
		provider:               cfg.Provider,
		activity:               activity,
		hub:                    cfg.Hub,
		models:                 cfg.Models,
		comfy:                  cfg.Comfy,
		publicAPI:              cfg.PublicAPI,
		assignProxy:            cfg.AssignProxy,
		shortcuts:              cfg.Shortcuts,
		bundler:                bundler,
		location:               cfg.Location,
		onFocusWindow:          cfg.OnFocusWindow,
		monitorInterval:        monInterval,
		observerInterval:       obsInterval,
		canvasWaitInterval:     canvasWait,
		upgradePollDelay:       upgradeDelay,
		startRetryDelay:        startRetryDelay,
		instanceStates:         make(map[string]*instanceCache),
		commonIDs:              make(map[string]bool),
		customModels:           make(map[string]map[string]any),
		apiIDOverrides:         make(map[string]string),
		commonRepos:            make([]map[string]any, 0),
		customWorkflows:        make(map[string]map[string]any),
		workflowDrafts:         make(map[string]*ColabOneWorkflowDraft),
		remoteWorkflowVersions: make(map[string]float64),
		taskActivity:           make(map[string]taskActivitySig),
		taskLogCursor:          make(map[string]int64),
		expectedServiceRev:     bundler.ServiceRevision(),
		upgradeAttempted:       make(map[string]bool),
		stop:                   NewColabOneEvent(),
		startCancel:            NewColabOneEvent(),
		driveCancel:            NewColabOneEvent(),
		driveConfirm:           NewColabOneEvent(),
		driveProbe:             NewColabOneEvent(),
		observerWake:           NewColabOneEvent(),
		monitorMode:            "auto",
		externalObservations:   make(map[string]map[string]any),
		background:                              cfg.Background,
		driveOutputs:                            make(map[string]map[string]*ColabOneDriveOutputRecord),
		driveOutputsDeleted:                     make(map[string]map[string]*ColabOneDriveOutputTombstone),
		driveOutputsSyncSem:                     make(chan struct{}, 1),
		driveOutputsSyncDeleted:                 make(map[string]map[string]bool),
		driveOutputsSyncDeletedFileIDs:          make(map[string]map[string]bool),
		driveOutputsSyncClearedWorkspaceIDs:     make(map[string]map[string]bool),
		driveOutputsSyncClearedWorkspaceFileIDs: make(map[string]map[string]bool),
		driveOutputsSyncAdded:                   make(map[string]map[string]bool),
		driveAPIBaseURL:                strings.TrimSpace(cfg.DriveAPIBaseURL),
		driveHTTPClient:                cfg.DriveHTTPClient,
		driveHTTPClientOwned:           cfg.DriveHTTPClient == nil,
		outboundLogger:                 cfg.OutboundLogger,
		outboundLoggerOwned:            cfg.OutboundLoggerOwned,
		rootCtx:                        rootCtx,
		cancelRoot:                     cancelRoot,
	}
	if c.driveHTTPClient == nil {
		c.driveHTTPClient = &http.Client{
			Transport:     outboundlog.WrapTransport(nil, c.outboundLogger, "drive_api", ""),
			Timeout:       30 * time.Second,
			CheckRedirect: refuseDriveRedirects,
		}
	}

	c.loadSettings()
	c.loadLibrary()
	c.loadWorkflows()
	c.loadWorkflowDrafts()
	c.loadDriveOutputs()
	c.cuUsage = NewColabOneCUUsageStore(configDir, nil)

	c.data = map[string]any{
		"runtime":           map[string]any{"state": "stopped"},
		"account":           map[string]any{"logged_in": false},
		"models":            c.Presets(),
		"workflows":         c.mergeWorkflows(nil),
		"tts_engines":       c.builtinTtsEngines(),
		"comfy_outputs":        []any{},
		"live_avatars":         []map[string]any{},
		"live_driving_motions": []map[string]any{},
		"live_catalog":         map[string]any{},
		"live_external_llm":    c.liveExternalLLMSummary(),
		"operation":         nil,
		"error":             "",
		"auth_url":          "",
		"drive_auth_hint":   "",
	}

	c.log("客户端已启动", "客户端", "info")
	if v, ok := c.vault.Get("colab"); ok && asBool(v) {
		c.log("已恢复本机保存的 Google 登录；到期自动续期", "登录", "info")
	}

	if cfg.Background {
		c.spawn(c.runMonitorLoop)
		c.spawn(c.runExternalMonitorLoop)
		c.triggerBackgroundDriveOutputsSync()
	}
	return c
}

// ConfigDir returns the active configuration directory.
func (c *ColabOneController) ConfigDir() string {
	return c.directory
}

// ExpectedServiceRevision returns the locally packaged remote service revision hash.
func (c *ColabOneController) ExpectedServiceRevision() string {
	return c.expectedServiceRev
}

// IsBusy reports whether a long-running async desktop operation is currently executing.
func (c *ColabOneController) IsBusy() bool {
	return c.opBusy.Load()
}

// Close stops all background loops, cancels pending wait signals, closes any external observer
// and managed provider resources, and waits for tracked goroutines to finish without leaks.
func (c *ColabOneController) Close() {
	c.closeOnce.Do(func() {
		c.spawnMu.Lock()
		c.closed = true
		c.spawnMu.Unlock()

		c.stop.Set()
		c.startCancel.Set()
		c.driveCancel.Set()
		c.driveConfirm.Set()
		c.driveProbe.Set()
		c.observerWake.Set()
		c.cancelRoot()
		if c.cuUsage != nil {
			_ = c.cuUsage.Flush()
		}
		c.closeExternalObserver("", true)
		c.wg.Wait()
		c.mu.Lock()
		var ownedDriveClient *http.Client
		if c.driveHTTPClientOwned && c.driveHTTPClient != nil {
			ownedDriveClient = c.driveHTTPClient
		}
		streamClient := c.driveStreamHTTPClient
		c.driveStreamHTTPClient = nil
		c.mu.Unlock()
		if ownedDriveClient != nil {
			ownedDriveClient.CloseIdleConnections()
		}
		if streamClient != nil {
			streamClient.CloseIdleConnections()
		}
		if c.cuUsage != nil {
			_ = c.cuUsage.Flush()
		}
		c.flushOutputCache()
		if closer, ok := c.provider.(ProviderCloser); ok {
			closer.Close()
		} else {
			c.closeExternalObserver("", true)
		}
		if c.outboundLoggerOwned && c.outboundLogger != nil {
			c.outboundLogger.Close()
		}
	})
}

func (c *ColabOneController) spawn(fn func()) bool {
	c.spawnMu.Lock()
	if c.closed {
		c.spawnMu.Unlock()
		return false
	}
	c.wg.Add(1)
	c.spawnMu.Unlock()
	go func() {
		defer c.wg.Done()
		fn()
	}()
	return true
}

// LogActivity appends a desktop-originated log entry into the shared activity log.
func (c *ColabOneController) LogActivity(message, source, level string) {
	c.log(message, source, level)
}

func (c *ColabOneController) log(message, source, level string) {
	if source == "" {
		source = "客户端"
	}
	if level == "" {
		level = "info"
	}
	c.activity.Add(message, source, level)
}

func (c *ColabOneController) logFunc() LogFunc {
	return func(message, source, level string) {
		c.log(message, source, level)
	}
}

func (c *ColabOneController) inferLocation(endpoint, rawURL string) map[string]any {
	if c.location == nil {
		return nil
	}
	return c.location.InferLocationFromEndpoint(endpoint, rawURL)
}

// ---------------------------------------------------------------------------
// Vault sessions, remotes, and instance state caching
// ---------------------------------------------------------------------------

func (c *ColabOneController) vaultSessions() map[string]map[string]any {
	if ps, ok := c.provider.(ProviderSessionStore); ok {
		if m := ps.SessionsMap(); m != nil {
			out := make(map[string]map[string]any, len(m))
			for k, v := range m {
				out[k] = deepCopyMap(v)
			}
			return out
		}
	}
	sessions := make(map[string]map[string]any)
	if rawIDs, ok := c.vault.Get("runtime_session_ids"); ok {
		for _, item := range asAnySlice(rawIDs) {
			ep := asString(item)
			if ep == "" {
				continue
			}
			if rawItem, ok := c.vault.Get("runtime_session:" + ep); ok {
				if m := asMap(rawItem); m != nil && asString(m["endpoint"]) != "" {
					sessions[asString(m["endpoint"])] = deepCopyMap(m)
				}
			}
		}
	}
	if rawMapVal, ok := c.vault.Get("runtime_sessions"); ok {
		if rawMap := asMap(rawMapVal); rawMap != nil {
			for _, item := range rawMap {
				if m := asMap(item); m != nil {
					if ep := asString(m["endpoint"]); ep != "" {
						if _, exists := sessions[ep]; !exists {
							sessions[ep] = deepCopyMap(m)
						}
					}
				}
			}
		}
	}
	if rawLegacy, ok := c.vault.Get("runtime_session"); ok {
		if m := asMap(rawLegacy); m != nil {
			if ep := asString(m["endpoint"]); ep != "" {
				if _, exists := sessions[ep]; !exists {
					sessions[ep] = deepCopyMap(m)
				}
			}
		}
	}
	return sessions
}

func (c *ColabOneController) vaultRemotes() map[string]map[string]any {
	if ps, ok := c.provider.(ProviderSessionStore); ok {
		if m := ps.RemotesMap(); m != nil {
			out := make(map[string]map[string]any, len(m))
			for k, v := range m {
				out[k] = deepCopyMap(v)
			}
			return out
		}
	}
	remotes := make(map[string]map[string]any)
	if rawIDs, ok := c.vault.Get("remote_ids"); ok {
		for _, item := range asAnySlice(rawIDs) {
			ep := asString(item)
			if ep == "" {
				continue
			}
			if rawItem, ok := c.vault.Get("remote:" + ep); ok {
				if m := asMap(rawItem); m != nil && asString(m["endpoint"]) != "" {
					remotes[asString(m["endpoint"])] = deepCopyMap(m)
				}
			}
		}
	}
	if rawMapVal, ok := c.vault.Get("remotes"); ok {
		if rawMap := asMap(rawMapVal); rawMap != nil {
			for _, item := range rawMap {
				if m := asMap(item); m != nil {
					if ep := asString(m["endpoint"]); ep != "" {
						if _, exists := remotes[ep]; !exists {
							remotes[ep] = deepCopyMap(m)
						}
					}
				}
			}
		}
	}
	if rawLegacy, ok := c.vault.Get("remote"); ok {
		if m := asMap(rawLegacy); m != nil {
			if ep := asString(m["endpoint"]); ep != "" {
				if _, exists := remotes[ep]; !exists {
					remotes[ep] = deepCopyMap(m)
				}
			}
		}
	}
	return remotes
}

func (c *ColabOneController) activeEndpoint() string {
	return c.activeEndpointFrom(c.vaultSessions())
}

func (c *ColabOneController) activeEndpointFrom(sessions map[string]map[string]any) string {
	if len(sessions) == 0 {
		return ""
	}
	if prefVal, ok := c.vault.Get("active_endpoint"); ok {
		if pref := asString(prefVal); pref != "" {
			if _, exists := sessions[pref]; exists {
				return pref
			}
		}
	}
	if legacyVal, ok := c.vault.Get("runtime_session"); ok {
		if m := asMap(legacyVal); m != nil {
			if ep := asString(m["endpoint"]); ep != "" {
				if _, exists := sessions[ep]; exists {
					return ep
				}
			}
		}
	}
	if rawIDs, ok := c.vault.Get("runtime_session_ids"); ok {
		for _, item := range asAnySlice(rawIDs) {
			ep := asString(item)
			if _, exists := sessions[ep]; exists {
				return ep
			}
		}
	}
	keys := make([]string, 0, len(sessions))
	for ep := range sessions {
		keys = append(keys, ep)
	}
	sort.Strings(keys)
	return keys[0]
}

func (c *ColabOneController) ensureInstanceStateLocked(endpoint string) *instanceCache {
	state, ok := c.instanceStates[endpoint]
	if !ok {
		state = &instanceCache{
			Runtime:                map[string]any{"state": "stopped", "id": endpoint},
			Models:                 c.Presets(),
			Workflows:              []map[string]any{},
			TtsEngines:             []map[string]any{},
			ComfyOutputs:           []any{},
			LiveAvatars:            []map[string]any{},
			LiveDrivingMotions:     []map[string]any{},
			LiveCatalog:            map[string]any{},
			RemoteWorkflowVersions: make(map[string]float64),
		}
		c.instanceStates[endpoint] = state
	}
	return state
}

func (c *ColabOneController) saveActiveToCacheLocked() {
	c.saveActiveToCacheWithSessionsLocked(nil, "")
}

func (c *ColabOneController) saveActiveToCacheWithSessionsLocked(sessions map[string]map[string]any, activeEP string) {
	rt := asMap(c.data["runtime"])
	rtState := asString(rt["state"])
	if rtState == "stopped" || rtState == "starting" {
		return
	}
	if sessions == nil {
		sessions = c.vaultSessions()
		activeEP = c.activeEndpointFrom(sessions)
	}
	ep := asString(rt["id"])
	if ep == "" {
		ep = activeEP
	}
	if ep == "" {
		return
	}
	if _, owned := sessions[ep]; !owned {
		return
	}
	entry := c.ensureInstanceStateLocked(ep)
	entry.Runtime = deepCopyMap(rt)
	entry.Models = deepCopyMapSlice(asMapSlice(c.data["models"]))
	entry.Workflows = deepCopyMapSlice(asMapSlice(c.data["workflows"]))
	entry.TtsEngines = deepCopyMapSlice(asMapSlice(c.data["tts_engines"]))
	entry.ComfyOutputs = deepCopySlice(asAnySlice(c.data["comfy_outputs"]))
	if entry.ComfyOutputs == nil {
		entry.ComfyOutputs = []any{}
	}
	entry.LiveAvatars = deepCopyMapSlice(asMapSlice(c.data["live_avatars"]))
	entry.LiveDrivingMotions = deepCopyMapSlice(asMapSlice(c.data["live_driving_motions"]))
	if cat := deepCopyMap(asMap(c.data["live_catalog"])); cat != nil {
		entry.LiveCatalog = cat
	} else {
		entry.LiveCatalog = map[string]any{}
	}
	verCopy := make(map[string]float64, len(c.remoteWorkflowVersions))
	for k, v := range c.remoteWorkflowVersions {
		verCopy[k] = v
	}
	entry.RemoteWorkflowVersions = verCopy
}

func (c *ColabOneController) saveActiveToCache() {
	sessions := c.vaultSessions()
	activeEP := c.activeEndpointFrom(sessions)
	c.mu.Lock()
	c.saveActiveToCacheWithSessionsLocked(sessions, activeEP)
	c.mu.Unlock()
}

func (c *ColabOneController) activateCached(endpoint string) {
	sessions := c.vaultSessions()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.activateCachedWithSessionsLocked(endpoint, sessions)
}

func (c *ColabOneController) activateCachedWithSessionsLocked(endpoint string, sessions map[string]map[string]any) {
	if endpoint != "" {
		if _, ok := sessions[endpoint]; ok {
			entry := c.ensureInstanceStateLocked(endpoint)
			c.data["runtime"] = deepCopyMap(entry.Runtime)
			c.data["models"] = deepCopyMapSlice(entry.Models)
			c.data["workflows"] = c.mergeWorkflowsLocked(entry.Workflows)
			c.data["tts_engines"] = c.mergeTtsEngines(entry.TtsEngines)
			outputs := deepCopySlice(entry.ComfyOutputs)
			if outputs == nil {
				outputs = []any{}
			}
			c.data["comfy_outputs"] = outputs
			c.data["live_avatars"] = deepCopyMapSlice(entry.LiveAvatars)
			c.data["live_driving_motions"] = deepCopyMapSlice(entry.LiveDrivingMotions)
			if cat := deepCopyMap(entry.LiveCatalog); cat != nil {
				c.data["live_catalog"] = cat
			} else {
				c.data["live_catalog"] = map[string]any{}
			}
			verCopy := make(map[string]float64, len(entry.RemoteWorkflowVersions))
			for k, v := range entry.RemoteWorkflowVersions {
				verCopy[k] = v
			}
			c.remoteWorkflowVersions = verCopy
			return
		}
	}
	c.data["runtime"] = map[string]any{"state": "stopped"}
	c.data["models"] = c.Presets()
	c.data["workflows"] = c.mergeWorkflowsLocked(nil)
	c.data["tts_engines"] = c.builtinTtsEngines()
	c.data["comfy_outputs"] = []any{}
	c.data["live_avatars"] = []map[string]any{}
	c.data["live_driving_motions"] = []map[string]any{}
	c.data["live_catalog"] = map[string]any{}
	c.remoteWorkflowVersions = make(map[string]float64)
}

func (c *ColabOneController) enrichInstances(account map[string]any) []map[string]any {
	sessions := c.vaultSessions()
	remotes := c.vaultRemotes()
	activeEP := c.activeEndpointFrom(sessions)
	return c.enrichInstancesWith(account, sessions, remotes, activeEP)
}

func (c *ColabOneController) enrichInstancesWith(
	account map[string]any,
	sessions map[string]map[string]any,
	remotes map[string]map[string]any,
	activeEP string,
) []map[string]any {
	rawList := make([]map[string]any, 0)
	seen := make(map[string]bool)
	for _, item := range asMapSlice(account["instances"]) {
		if item == nil {
			continue
		}
		cp := deepCopyMap(item)
		rawList = append(rawList, cp)
		if id := asString(cp["id"]); id != "" {
			seen[id] = true
		}
	}

	sessionKeys := make([]string, 0, len(sessions))
	for ep := range sessions {
		sessionKeys = append(sessionKeys, ep)
	}
	sort.Strings(sessionKeys)
	for _, ep := range sessionKeys {
		if seen[ep] {
			continue
		}
		sess := sessions[ep]
		accel := asString(remotes[ep]["gpu"])
		if accel == "" {
			accel = asString(sess["accelerator"])
		}
		variant := asString(sess["variant"])
		if variant == "" {
			variant = "GPU"
		}
		shape := asString(sess["machine_shape"])
		if shape == "" {
			shape = "STANDARD"
		}
		rawList = append(rawList, map[string]any{
			"id":          ep,
			"managed":     true,
			"active":      ep == activeEP,
			"accelerator": strings.ToUpper(accel),
			"variant":     strings.ToUpper(variant),
			"shape":       strings.ToUpper(shape),
		})
	}

	// Capture active runtime and cached secondary instance states once under c.mu.
	var activeRT map[string]any
	var activeModels []map[string]any
	var cachedStates map[string]*instanceCache
	hasManaged := false
	for _, item := range rawList {
		ep := asString(item["id"])
		_, inSessions := sessions[ep]
		if asBool(item["managed"]) || inSessions {
			hasManaged = true
			break
		}
	}
	if hasManaged {
		c.mu.Lock()
		activeRT = deepCopyMap(asMap(c.data["runtime"]))
		if asString(activeRT["state"]) != "stopped" {
			activeModels = deepCopyMapSlice(asMapSlice(c.data["models"]))
		}
		if len(c.instanceStates) > 0 {
			cachedStates = make(map[string]*instanceCache, len(c.instanceStates))
			for ep, st := range c.instanceStates {
				if st != nil {
					cachedStates[ep] = &instanceCache{
						Runtime: deepCopyMap(st.Runtime),
						Models:  deepCopyMapSlice(st.Models),
					}
				}
			}
		}
		c.mu.Unlock()
	}

	result := make([]map[string]any, 0, len(rawList))
	for _, item := range rawList {
		ep := asString(item["id"])
		_, inSessions := sessions[ep]
		managed := asBool(item["managed"]) || inSessions
		isActive := managed && (ep == activeEP)
		enriched := deepCopyMap(item)
		enriched["managed"] = managed
		enriched["active"] = isActive

		if managed {
			sess := sessions[ep]
			rem := remotes[ep]
			if asString(enriched["accelerator"]) == "" {
				accel := asString(rem["gpu"])
				if accel == "" {
					accel = asString(sess["accelerator"])
				}
				enriched["accelerator"] = strings.ToUpper(accel)
			}
			if asString(enriched["variant"]) == "" {
				variant := asString(sess["variant"])
				if variant == "" {
					variant = "GPU"
				}
				enriched["variant"] = strings.ToUpper(variant)
			}
			if asString(enriched["shape"]) == "" {
				shape := asString(sess["machine_shape"])
				if shape == "" {
					shape = "STANDARD"
				}
				enriched["shape"] = strings.ToUpper(shape)
			}

			var rt map[string]any
			var models []map[string]any
			if isActive && asString(activeRT["state"]) != "stopped" {
				rt = deepCopyMap(activeRT)
				models = activeModels
			} else if cached := cachedStates[ep]; cached != nil {
				rt = deepCopyMap(cached.Runtime)
				models = cached.Models
			}
			if rt == nil {
				rt = map[string]any{}
			}

			loadedID := asString(rt["loaded_model"])
			loadedName := loadedID
			for _, m := range models {
				if asString(m["id"]) == loadedID {
					if n := asString(m["name"]); n != "" {
						loadedName = n
					}
					break
				}
			}
			activeTask := ""
			for _, t := range asMapSlice(rt["tasks"]) {
				if asString(t["state"]) == "running" {
					activeTask = asString(t["kind"])
					break
				}
			}
			gpu := asMap(rt["gpu"])
			resources := asMap(rt["resources"])
			loc := asMap(rt["location"])
			if len(loc) == 0 {
				loc = asMap(rem["location"])
			}
			if len(loc) == 0 {
				loc = asMap(enriched["location"])
			}
			if len(loc) == 0 {
				loc = c.inferLocation(ep, asString(sess["url"]))
			}

			stateStr := asString(rt["state"])
			if stateStr == "" {
				if _, hasRem := remotes[ep]; hasRem {
					stateStr = "ready"
				} else {
					stateStr = "starting"
				}
			}
			drive := asMap(rt["drive"])
			enriched["state"] = stateStr
			enriched["gpu_name"] = asString(gpu["name"])
			enriched["vram_used_mib"] = gpu["used_mib"]
			enriched["vram_total_mib"] = gpu["total_mib"]
			enriched["disk_free"] = rt["disk_free"]
			enriched["loaded_model"] = loadedName
			enriched["active_task"] = activeTask
			enriched["drive_connected"] = asBool(drive["connected"])

			if len(resources) > 0 {
				enriched["resources"] = deepCopyMap(resources)
				if mem := asMap(resources["memory"]); mem != nil {
					if v, ok := mem["used_bytes"]; ok {
						enriched["ram_used_bytes"] = v
					}
					if v, ok := mem["total_bytes"]; ok {
						enriched["ram_total_bytes"] = v
					}
				}
				if disk := asMap(resources["disk"]); disk != nil {
					if v, ok := disk["used_bytes"]; ok {
						enriched["disk_used_bytes"] = v
					}
					if v, ok := disk["total_bytes"]; ok {
						enriched["disk_total_bytes"] = v
					}
					if v, ok := disk["free_bytes"]; ok {
						enriched["disk_free_bytes"] = v
					}
				}
				for _, rk := range []string{
					"ram_used_bytes",
					"ram_total_bytes",
					"disk_used_bytes",
					"disk_total_bytes",
					"disk_free_bytes",
				} {
					if v, ok := resources[rk]; ok {
						enriched[rk] = v
					}
				}
			}
			if len(loc) > 0 {
				enriched["location"] = deepCopyMap(loc)
			}
		}
		result = append(result, enriched)
	}
	return result
}

// ---------------------------------------------------------------------------
// Snapshot & state updates
// ---------------------------------------------------------------------------

// Snapshot returns the full JSON-serializable state expected by frontend/src/types.ts ColabOneSnapshot.
func (c *ColabOneController) Snapshot() map[string]any {
	sessions := c.vaultSessions()
	remotes := c.vaultRemotes()
	activeEP := c.activeEndpointFrom(sessions)

	c.mu.Lock()
	value := deepCopyMap(c.data)
	commonRepos := deepCopyMapSlice(c.commonRepos)
	c.mu.Unlock()

	rt := asMap(value["runtime"])
	if rt == nil {
		rt = map[string]any{"state": "stopped"}
		value["runtime"] = rt
	}
	decoratedModels := c.decorateModels(asMapSlice(value["models"]), rt)
	value["models"] = decoratedModels
	var remoteWorkflows []map[string]any
	var remoteTtsEngines []map[string]any
	if asString(rt["state"]) != "stopped" {
		remoteWorkflows = asMapSlice(value["workflows"])
		remoteTtsEngines = asMapSlice(value["tts_engines"])
	}
	value["workflows"] = c.mergeWorkflows(remoteWorkflows)
	value["tts_engines"] = c.mergeTtsEngines(remoteTtsEngines)
	outputs := asAnySlice(value["comfy_outputs"])
	if outputs == nil {
		outputs = []any{}
	}
	value["comfy_outputs"] = outputs
	if asString(rt["state"]) != "stopped" {
		value["live_avatars"] = deepCopyMapSlice(asMapSlice(value["live_avatars"]))
		value["live_driving_motions"] = deepCopyMapSlice(asMapSlice(value["live_driving_motions"]))
		if cat := deepCopyMap(asMap(value["live_catalog"])); cat != nil {
			value["live_catalog"] = cat
		} else {
			value["live_catalog"] = map[string]any{}
		}
	} else {
		value["live_avatars"] = []map[string]any{}
		value["live_driving_motions"] = []map[string]any{}
		value["live_catalog"] = map[string]any{}
	}
	value["common_repos"] = commonRepos

	account := asMap(value["account"])
	if account == nil {
		account = map[string]any{"logged_in": false}
		value["account"] = account
	}
	acctCopy := deepCopyMap(account)
	enriched := make([]map[string]any, 0)
	if instVal, exists := account["instances"]; (exists && instVal != nil) || len(sessions) > 0 {
		enriched = c.enrichInstancesWith(account, sessions, remotes, activeEP)
		acctCopy["instances"] = enriched
		if _, hasAssign := acctCopy["assignments"]; !hasAssign && len(enriched) > 0 {
			acctCopy["assignments"] = len(enriched)
		}
	}
	colabCreds, _ := c.vault.Get("colab")
	if c.cuUsage != nil && (asBool(acctCopy["logged_in"]) || asBool(colabCreds)) {
		email := asTrimmedString(acctCopy["email"])
		if email == "" && c.vault != nil {
			if profVal, ok := c.vault.Get("google_profile"); ok {
				email = asTrimmedString(asMap(profVal)["email"])
			}
		}
		if runSnap := c.cuUsage.CurrentRunSnapshot(email); runSnap != nil {
			acctCopy["current_run"] = runSnap
		}
	}
	value["account"] = acctCopy

	value["active_endpoint"] = activeEP
	value["monitor"] = c.buildMonitorSnapshot(enriched, rt, decoratedModels, activeEP)

	rawAssignProxy := ""
	if v, ok := c.vault.Get("assign_proxy"); ok {
		if s, ok := v.(string); ok {
			rawAssignProxy = s
		}
	}
	var assignProxySpec *ColabOneAssignProxySpec
	if rawAssignProxy != "" && c.assignProxy != nil {
		if parsed, err := c.assignProxy.ParseAssignProxy(rawAssignProxy); err == nil {
			assignProxySpec = parsed
		}
	}

	c.mu.Lock()
	assignProxyEnabled := asBool(c.settings["assign_proxy_enabled"]) && assignProxySpec != nil
	publicSettings := make(map[string]any, len(c.settings))
	for k, v := range c.settings {
		if k == "idle_policy" || k == "rclone_drive_policy" || k == "default_gpu_policy" || k == "default_assign_max_attempts_policy" {
			continue
		}
		publicSettings[k] = v
	}
	publicSettings["assign_proxy_enabled"] = assignProxyEnabled
	c.mu.Unlock()

	value["settings"] = publicSettings
	maskedURL := ""
	hasAuth := false
	if assignProxySpec != nil {
		maskedURL = assignProxySpec.MaskedURL
		hasAuth = assignProxySpec.HasAuth
	}
	value["assign_proxy_settings"] = map[string]any{
		"enabled":    assignProxyEnabled,
		"has_proxy":  assignProxySpec != nil,
		"masked_url": maskedURL,
		"has_auth":   hasAuth,
	}

	c.vaultConfigMu.Lock()
	apiMap := map[string]any{}
	if v, ok := c.vault.Get("public_api"); ok {
		if m := asMap(v); m != nil {
			apiMap = m
		}
	}
	value["api_settings"] = map[string]any{
		"mode":      "named",
		"domain":    asString(apiMap["domain"]),
		"has_token": asBool(apiMap["tunnel_token"]),
		"has_key":   asBool(apiMap["api_key"]),
	}

	canvasMap := map[string]any{}
	if v, ok := c.vault.Get("comfyui_canvas"); ok {
		if m := asMap(v); m != nil {
			canvasMap = m
		}
	}
	value["comfy_canvas_settings"] = map[string]any{
		"mode":      "named",
		"domain":    asString(canvasMap["domain"]),
		"has_token": asBool(canvasMap["tunnel_token"]),
	}
	value["live_external_llm"] = c.liveExternalLLMSummaryLocked()
	c.vaultConfigMu.Unlock()

	logs := c.activity.Snapshot()
	if logs == nil {
		logs = []ColabOneActivityEntry{}
	}
	value["logs"] = logs

	driveInfo := asMap(rt["drive"])
	driveAuthorized := false
	needsQuota := false
	driveQuotaReason := ""
	if c.auth != nil {
		driveAuthorized = c.auth.HasDriveScope()
		needsQuota = c.auth.RequiresDriveQuotaProject()
		if qs := c.auth.DriveQuotaStatus(); qs != nil {
			driveQuotaReason = asString(qs["reason"])
		}
	}
	cloudProject, _ := c.vault.Get("cloud_project")
	driveHeadless := driveAuthorized && (!needsQuota || asBool(cloudProject))

	mergedOutputs, driveOutputsStatus := c.mergeComfyOutputsForSnapshot(rt, asAnySlice(value["comfy_outputs"]), driveHeadless)
	value["comfy_outputs"] = mergedOutputs
	value["drive_outputs_status"] = driveOutputsStatus

	var shortcuts map[string]bool
	if c.shortcuts != nil {
		shortcuts = c.shortcuts.Status(c.shortcutSandbox)
	}
	colabCredsMap := asMap(colabCreds)
	hfVal, _ := c.vault.Get("hf")
	driveMode := asString(driveInfo["mode"])
	if driveMode == "" {
		driveMode = "disabled"
	}

	value["setup"] = map[string]any{
		"colab":               asBool(colabCreds),
		"persistent_login":    asBool(colabCredsMap["refresh_token"]),
		"drive_authorized":    driveAuthorized,
		"drive_headless":      driveHeadless,
		"drive_quota_reason":  driveQuotaReason,
		"drive":               asBool(driveInfo["connected"]),
		"drive_mode":          driveMode,
		"hf":                  asBool(hfVal),
		"start_menu_shortcut": shortcuts["start_menu"],
		"desktop_shortcut":    shortcuts["desktop"],
	}

	return SanitizeJSONValue(value).(map[string]any)
}

// Update merges state updates into the controller snapshot, emitting deduplicated activity logs.
func (c *ColabOneController) Update(values map[string]any) {
	var sessions map[string]map[string]any
	var activeEP string
	needSessions := false
	for k := range values {
		if k == "runtime" || k == "models" || k == "workflows" || k == "tts_engines" || k == "comfy_outputs" || k == "live_avatars" || k == "live_driving_motions" || k == "live_catalog" {
			needSessions = true
			break
		}
	}
	if needSessions {
		if rtVal, hasRT := values["runtime"]; hasRT {
			if rtMap := asMap(rtVal); rtMap != nil {
				st := asString(rtMap["state"])
				if st == "stopped" || st == "starting" {
					needSessions = false
				}
			}
		}
	}
	if needSessions {
		sessions = c.vaultSessions()
		activeEP = c.activeEndpointFrom(sessions)
	}
	c.mu.Lock()
	pendingLogs := c.updateWithSessionsLocked(values, sessions, activeEP)
	c.mu.Unlock()
	c.flushActivityLogs(pendingLogs)
	if c.cuUsage != nil {
		if acctVal, hasAcct := values["account"]; hasAcct {
			if acctMap := asMap(acctVal); acctMap != nil {
				if asBool(acctMap["logged_in"]) && asTrimmedString(acctMap["email"]) == "" && c.vault != nil {
					if profVal, ok := c.vault.Get("google_profile"); ok {
						if profEmail := asTrimmedString(asMap(profVal)["email"]); profEmail != "" {
							acctCopy := deepCopyMap(acctMap)
							acctCopy["email"] = profEmail
							acctMap = acctCopy
						}
					}
				}
				c.cuUsage.RecordAccount(acctMap)
			}
		}
	}
}

func (c *ColabOneController) recordAccountCUUsage(acctMap map[string]any) {
	if c.cuUsage == nil || acctMap == nil {
		return
	}
	if asBool(acctMap["logged_in"]) && asTrimmedString(acctMap["email"]) == "" && c.vault != nil {
		if profVal, ok := c.vault.Get("google_profile"); ok {
			if profEmail := asTrimmedString(asMap(profVal)["email"]); profEmail != "" {
				acctCopy := deepCopyMap(acctMap)
				acctCopy["email"] = profEmail
				acctMap = acctCopy
			}
		}
	}
	c.cuUsage.RecordAccount(acctMap)
}

func (c *ColabOneController) snapshotAccountGuard() (uint64, map[string]any) {
	gen := c.accountGen.Load()
	var creds map[string]any
	if c.vault != nil {
		if raw, ok := c.vault.Get("colab"); ok {
			if m := asMap(raw); len(m) > 0 {
				creds = deepCopyMap(m)
			}
		}
	}
	return gen, creds
}

func (c *ColabOneController) accountGuardMatches(gen uint64, beforeCreds map[string]any) bool {
	if c.accountGen.Load() != gen {
		return false
	}
	var curCreds map[string]any
	if c.vault != nil {
		if raw, ok := c.vault.Get("colab"); ok {
			if m := asMap(raw); len(m) > 0 {
				curCreds = m
			}
		}
	}
	if len(beforeCreds) == 0 && len(curCreds) == 0 {
		return true
	}
	return sameColabCredentialIdentity(beforeCreds, curCreds)
}

func (c *ColabOneController) applyAccountRefreshIfCurrent(
	gen uint64,
	beforeCreds map[string]any,
	acct map[string]any,
	lastAuth float64,
) bool {
	if !c.accountGuardMatches(gen, beforeCreds) {
		return false
	}
	if acct == nil {
		acct = map[string]any{"logged_in": false}
	}
	sessions := c.vaultSessions()
	activeEP := c.activeEndpointFrom(sessions)
	c.mu.Lock()
	if c.accountGen.Load() != gen {
		c.mu.Unlock()
		return false
	}
	patch := map[string]any{"account": acct}
	if isMonitorAccountError(asString(c.data["error"])) {
		patch["error"] = ""
	}
	pendingLogs := c.updateWithSessionsLocked(patch, sessions, activeEP)
	if lastAuth > 0 {
		c.lastAuth = lastAuth
	}
	c.mu.Unlock()
	c.flushActivityLogs(pendingLogs)
	if c.accountGen.Load() == gen {
		c.recordAccountCUUsage(acct)
	}
	return true
}

// resetAccountStateForSwitch clears all account-scoped and runtime-scoped cached
// state atomically when logging into a different account or importing account
// credentials via migration.
func (c *ColabOneController) resetAccountStateForSwitch(accountPatch map[string]any, resetAuthState bool) {
	if resetAuthState {
		if resetter, ok := c.auth.(AuthStateResetter); ok {
			resetter.ResetAccountState()
		}
	}
	if c.cuUsage != nil {
		c.cuUsage.ResetActiveAccount()
	}
	if accountPatch == nil {
		hasLogin := false
		email := ""
		if c.vault != nil {
			if v, ok := c.vault.Get("colab"); ok && asBool(v) {
				hasLogin = true
			}
			if profVal, ok := c.vault.Get("google_profile"); ok {
				email = asTrimmedString(asMap(profVal)["email"])
			}
		}
		accountPatch = map[string]any{
			"logged_in": hasLogin,
			"email":     email,
		}
	}

	c.ttsPreviewCacheMu.Lock()
	c.pendingTtsPreviewReqs = nil
	c.pendingTtsPrefetchCtx = nil
	c.ttsPreviewCacheMu.Unlock()

	c.resetOutputCacheForAccountSwitch()

	var pendingLogs []pendingActivityLog
	c.mu.Lock()
	c.accountGen.Add(1)
	c.lastAuth = 0
	c.lastConfig = 0
	c.lastHeartbeat = 0
	c.pendingTouch = false
	c.lastTouch = 0
	c.releasePlan = nil
	clear(c.instanceStates)
	clear(c.upgradeAttempted)
	clear(c.taskActivity)
	clear(c.taskLogCursor)
	c.remoteWorkflowVersions = make(map[string]float64)
	c.resetDriveOutputsSyncStateLocked()

	patch := map[string]any{
		"account":              deepCopyMap(accountPatch),
		"error":                "",
		"auth_url":             "",
		"models":               c.Presets(),
		"workflows":            c.mergeWorkflowsLocked(nil),
		"tts_engines":          c.builtinTtsEngines(),
		"comfy_outputs":        []any{},
		"live_avatars":         []map[string]any{},
		"live_driving_motions": []map[string]any{},
		"live_catalog":         map[string]any{},
	}
	if rtState := asString(asMap(c.data["runtime"])["state"]); rtState != "stopped" {
		patch["runtime"] = map[string]any{"state": "stopped"}
	}
	// Pass empty sessions so saveActiveToCacheWithSessionsLocked does not repopulate instanceStates.
	pendingLogs = c.updateWithSessionsLocked(patch, map[string]map[string]any{}, "")
	c.mu.Unlock()

	// Re-run output cache reset AFTER clearing Account A's runtime/outputs under c.mu
	// so any flight started in the brief window before c.mu.Lock() is cancelled and cannot
	// commit Account A's bytes into Account B's cache namespace.
	c.resetOutputCacheForAccountSwitch()

	c.flushActivityLogs(pendingLogs)
	c.resetMonitorToAuto(true, true)
	c.observerWake.Set()
}

type pendingActivityLog struct {
	message string
	source  string
	level   string
}

func (c *ColabOneController) flushActivityLogs(entries []pendingActivityLog) {
	for _, entry := range entries {
		c.log(entry.message, entry.source, entry.level)
	}
}

func (c *ColabOneController) updateLocked(values map[string]any) []pendingActivityLog {
	return c.updateWithSessionsLocked(values, nil, "")
}

func (c *ColabOneController) updateWithSessionsLocked(values map[string]any, sessions map[string]map[string]any, activeEP string) []pendingActivityLog {
	var pendingLogs []pendingActivityLog
	queueLog := func(message, source, level string) {
		pendingLogs = append(pendingLogs, pendingActivityLog{
			message: message,
			source:  source,
			level:   level,
		})
	}

	if op, ok := values["operation"].(map[string]any); ok && op != nil {
		prevOp := asMap(c.data["operation"])
		prevTitle := asString(prevOp["title"])
		nextTitle := asString(op["title"])
		if nextTitle != "" && nextTitle != prevTitle {
			queueLog(nextTitle, "客户端", "info")
		}
	}

	if errVal, ok := values["error"].(string); ok && errVal != "" && errVal != asString(c.data["error"]) {
		queueLog(errVal, "客户端", "error")
	}

	if rt, ok := values["runtime"].(map[string]any); ok && len(rt) > 0 {
		previous := asMap(c.data["runtime"])

		type sectionCheck struct {
			sec    map[string]any
			oldSec map[string]any
			tag    string
		}
		var newCanvas, oldCanvas map[string]any
		if comfy := asMap(rt["comfyui"]); comfy != nil {
			newCanvas = asMap(comfy["canvas"])
		}
		if prevComfy := asMap(previous["comfyui"]); prevComfy != nil {
			oldCanvas = asMap(prevComfy["canvas"])
		}
		sections := []sectionCheck{
			{sec: asMap(rt["public_api"]), oldSec: asMap(previous["public_api"]), tag: "API"},
			{sec: newCanvas, oldSec: oldCanvas, tag: "ComfyUI"},
		}
		for _, item := range sections {
			if item.sec == nil {
				continue
			}
			var cursor int64
			if item.oldSec != nil {
				oldLogs := asMapSlice(item.oldSec["logs"])
				if len(oldLogs) > 0 {
					cursor, _ = asInt64(oldLogs[len(oldLogs)-1]["seq"])
				}
			}
			for _, entry := range asMapSlice(item.sec["logs"]) {
				if entry == nil {
					continue
				}
				seq, _ := asInt64(entry["seq"])
				if seq > cursor {
					lvl := asString(entry["level"])
					if lvl == "" {
						lvl = "info"
					}
					queueLog(asString(entry["message"]), item.tag, lvl)
				}
			}
		}

		stateLabels := map[string]string{
			"ready":        "远端 GPU 已连接",
			"disconnected": "远端控制连接中断，实例可能仍在计费",
			"stopped":      "当前客户端管理的 GPU 已停止",
			"releasing":    "正在释放远端 GPU",
		}
		newState := asString(rt["state"])
		prevState := asString(previous["state"])
		if newState != prevState {
			if lbl, ok := stateLabels[newState]; ok {
				queueLog(lbl, "GPU", "info")
			}
		}

		taskKindLabels := map[string]string{
			"download":            "模型下载",
			"load":                "模型加载",
			"public_chat":         "外部 API 对话",
			"public_responses":    "外部 API Responses",
			"public_compact":      "外部 API 上下文压缩",
			"comfy_start":         "ComfyUI 启动",
			"comfy_run":           "ComfyUI 工作流",
			"comfyui":             "ComfyUI 任务",
			"comfyui_start":       "ComfyUI 启动",
			"comfyui_run":            "ComfyUI 工作流",
			"comfyui_tts_preview":    "音色试听",
			"live_start":             "实时演播启动",
			"live_build_pack":        "形象动作库构建",
			"comfyui_live_start":     "实时演播启动",
			"comfyui_live_build_pack": "形象动作库构建",
		}
		taskStatusLabels := map[string]string{
			"running":   "进行中",
			"completed": "已完成",
			"failed":    "失败",
			"cancelled": "已取消",
		}

		idle := asMap(rt["idle"])
		serverTime, _ := asFloat(idle["server_time"])

		tasks := asMapSlice(rt["tasks"])
		activeTaskIDs := make(map[string]bool, len(tasks))
		for _, task := range tasks {
			if task == nil {
				continue
			}
			tid := asString(task["id"])
			activeTaskIDs[tid] = true
			cursor := c.taskLogCursor[tid]
			for _, entry := range asMapSlice(task["logs"]) {
				if entry == nil {
					continue
				}
				seq, _ := asInt64(entry["seq"])
				if seq > cursor {
					lvl := asString(entry["level"])
					if lvl == "" {
						lvl = "info"
					}
					queueLog(asString(entry["message"]), "远端", lvl)
					cursor = seq
				}
			}
			c.taskLogCursor[tid] = cursor

			progress, _ := asFloat(task["progress"])
			bucket := int(progress * 10)
			tKind := asString(task["kind"])
			tState := asString(task["state"])
			tMsg := asString(task["message"])
			sig := taskActivitySig{
				Kind:    tKind,
				State:   tState,
				Bucket:  bucket,
				Message: tMsg,
			}
			if prevSig, exists := c.taskActivity[tid]; exists && prevSig == sig {
				continue
			}
			c.taskActivity[tid] = sig

			kindLabel := taskKindLabels[tKind]
			if kindLabel == "" {
				kindLabel = "远端任务"
			}
			statusLabel := taskStatusLabels[tState]
			if statusLabel == "" {
				statusLabel = tState
			}
			detail := ""
			if tKind == "download" {
				detail = fmt.Sprintf(" · %d%%", bucket*10)
			}
			if (tState == "completed" || tState == "failed" || tState == "cancelled") && !strings.Contains(tMsg, "耗时") {
				if elapsedSec, ok := resolveTaskElapsedSeconds(task, serverTime); ok {
					detail += " · 耗时 " + formatColabOneDurationZh(elapsedSec, true)
				}
			}
			if tMsg != "" {
				detail += " · " + tMsg
			}
			if tErr := asString(task["error"]); tErr != "" {
				detail += " · " + tErr
			}
			lvl := "info"
			if tState == "failed" {
				lvl = "error"
			}
			queueLog(kindLabel+"："+statusLabel+detail, "远端", lvl)
		}

		prevIdle := asMap(previous["idle"])
		if asString(idle["phase"]) == "countdown" && asString(prevIdle["phase"]) != "countdown" {
			remFloat, ok := asFloat(idle["remaining_seconds"])
			if !ok {
				remFloat = 60
			}
			remSec := int(math.Round(remFloat))
			queueLog(
				fmt.Sprintf("空闲自动释放倒计时剩余 %d 秒；继续操作可重置倒计时", remSec),
				"GPU",
				"warning",
			)
		}

		nextCursors := make(map[string]int64, len(activeTaskIDs))
		nextActivity := make(map[string]taskActivitySig, len(activeTaskIDs))
		for tid := range activeTaskIDs {
			nextCursors[tid] = c.taskLogCursor[tid]
			if sig, ok := c.taskActivity[tid]; ok {
				nextActivity[tid] = sig
			}
		}
		c.taskLogCursor = nextCursors
		c.taskActivity = nextActivity
	}

	if rt, ok := values["runtime"].(map[string]any); ok && asString(rt["state"]) == "stopped" {
		if _, hasTTS := values["tts_engines"]; !hasTTS {
			c.data["tts_engines"] = c.builtinTtsEngines()
		}
		if _, hasLiveAvatars := values["live_avatars"]; !hasLiveAvatars {
			c.data["live_avatars"] = []map[string]any{}
		}
		if _, hasLiveDriving := values["live_driving_motions"]; !hasLiveDriving {
			c.data["live_driving_motions"] = []map[string]any{}
		}
		if _, hasLiveCatalog := values["live_catalog"]; !hasLiveCatalog {
			c.data["live_catalog"] = map[string]any{}
		}
	}

	cacheDirty := false
	for k, v := range values {
		c.data[k] = deepCopyAny(v)
		if k == "runtime" || k == "models" || k == "workflows" || k == "tts_engines" || k == "comfy_outputs" || k == "live_avatars" || k == "live_driving_motions" || k == "live_catalog" {
			cacheDirty = true
		}
	}
	if cacheDirty {
		c.saveActiveToCacheWithSessionsLocked(sessions, activeEP)
	}
	return pendingLogs
}

func (c *ColabOneController) asyncOperation(title string, fn func() error, meta map[string]any, onStart func() error) (map[string]any, error) {
	if !c.opBusy.CompareAndSwap(false, true) {
		return nil, errors.New("请等待当前操作完成")
	}
	if onStart != nil {
		if err := onStart(); err != nil {
			c.opBusy.Store(false)
			return nil, err
		}
	}
	c.opGen.Add(1)
	op := map[string]any{"title": title, "running": true}
	for k, v := range meta {
		op[k] = v
	}
	c.Update(map[string]any{
		"operation":       op,
		"error":           "",
		"auth_url":        "",
		"drive_auth_hint": "",
	})

	started := c.spawn(func() {
		defer func() {
			c.Update(map[string]any{"operation": nil})
			c.opGen.Add(1)
			c.opBusy.Store(false)
		}()
		err := fn()
		if err == nil {
			c.log(title+"：完成", "客户端", "info")
			return
		}
		var cancelled *ColabOneOperationCancelled
		if errors.As(err, &cancelled) {
			src := cancelled.Source
			if src == "" {
				src = "客户端"
			}
			c.log(truncateRunes(SafeMessage(cancelled.Error()), 300), src, "info")
			return
		}
		var message string
		if code := httpStatusCode(err); code > 0 {
			message = fmt.Sprintf("服务返回 HTTP %d，请检查授权和配置", code)
		} else {
			message = truncateRunes(SafeMessage(err.Error()), 500)
		}
		c.Update(map[string]any{"error": SafeMessage(message)})
	})
	if !started {
		c.Update(map[string]any{"operation": nil})
		c.opGen.Add(1)
		c.opBusy.Store(false)
		return nil, errors.New("客户端正在关闭")
	}
	return map[string]any{"ok": true}, nil
}

// ---------------------------------------------------------------------------
// Remote RPC, configuration, refresh, and sync helpers
// ---------------------------------------------------------------------------

func (c *ColabOneController) remote(ctx context.Context, method, path string, body any, endpoint string, timeout time.Duration) (any, error) {
	if c.provider == nil {
		return nil, errors.New("远端实例未连接")
	}
	if ctx == nil {
		ctx = c.rootCtx
	}
	return c.provider.RPC(ctx, method, path, body, ColabOneRPCOptions{
		Endpoint: endpoint,
		Timeout:  timeout,
	})
}

func (c *ColabOneController) remoteLong(ctx context.Context, method, path string, body any, timeout time.Duration) (any, error) {
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	return c.remote(ctx, method, path, body, "", timeout)
}

// ConfigureRemote pushes the user's idle auto-release policy and API model ID overrides to the remote runtime.
func (c *ColabOneController) ConfigureRemote(ctx context.Context, endpoint string) error {
	_, err := c.configureRemote(ctx, endpoint, false)
	return err
}

func (c *ColabOneController) currentReleasePolicyLocked() (mode string, idleMin int, cuLimit float64, countdownSec int) {
	mode = normalizeColabOneReleaseMode(c.settings["release_mode"])
	idleMin, _ = asInt(c.settings["idle_minutes"])
	if idleMin < 1 || idleMin > 240 {
		idleMin = 30
	}
	cuLimit, _ = asFloat(c.settings["release_cu_limit"])
	if math.IsNaN(cuLimit) || math.IsInf(cuLimit, 0) || cuLimit < COLABONE_MIN_RELEASE_CU_LIMIT || cuLimit > COLABONE_MAX_RELEASE_CU_LIMIT {
		cuLimit = COLABONE_DEFAULT_RELEASE_CU_LIMIT
	} else {
		cuLimit = roundColabOneCULimit(cuLimit)
	}
	countdownSec, _ = asInt(c.settings["idle_countdown_seconds"])
	if countdownSec < 10 || countdownSec > 300 {
		countdownSec = 60
	}
	return mode, idleMin, cuLimit, countdownSec
}

func runtimeSupportsCapability(rt map[string]any, name string) bool {
	if rt == nil || name == "" {
		return false
	}
	for _, raw := range asAnySlice(rt["capabilities"]) {
		if asString(raw) == name {
			return true
		}
	}
	return false
}

func (c *ColabOneController) currentRunCUConsumption(now float64) (consumed float64, rate float64, balance float64, hasBalance bool, active bool) {
	if c.cuUsage == nil {
		return 0, 0, 0, false, false
	}
	if now <= 0 {
		now = nowUnixFloat()
	}
	c.mu.Lock()
	acct := asMap(c.data["account"])
	email := strings.ToLower(strings.TrimSpace(asString(acct["email"])))
	liveRate, _ := asFloat(acct["rate"])
	liveBal, hasLiveBal := asFloat(acct["balance"])
	c.mu.Unlock()
	if email == "" && c.vault != nil {
		if profVal, ok := c.vault.Get("google_profile"); ok {
			email = strings.ToLower(strings.TrimSpace(asString(asMap(profVal)["email"])))
		}
	}
	return c.cuUsage.CurrentRunEffectiveConsumed(email, now, liveRate, liveBal, hasLiveBal)
}

func (c *ColabOneController) computeCUReleaseDeadline(now, cuLimit float64) float64 {
	if cuLimit <= 0 {
		return 0
	}
	consumed, rate, _, _, active := c.currentRunCUConsumption(now)
	if !active {
		return 0
	}
	remCU := cuLimit - consumed
	if remCU <= 0 {
		// Limit already reached: keep a short remote failsafe deadline armed so
		// the VM still self-terminates if the local client disconnects before
		// checkCUAutoRelease completes.
		if rate > colabOneCUEpsilon {
			return math.Floor(now + 30.0)
		}
		return math.Floor(now + 45.0)
	}
	if rate <= colabOneCUEpsilon {
		return 0
	}
	estSec := (remCU / rate) * 3600.0
	const maxDeadlineHorizonSec = 30.0 * 24.0 * 3600.0
	if estSec > maxDeadlineHorizonSec {
		estSec = maxDeadlineHorizonSec
	}
	return math.Floor(now + math.Max(30.0, estSec+15.0))
}

func (c *ColabOneController) endCUUsageRunIfNoExternal(acct map[string]any, releasedTargets ...string) {
	if c.cuUsage == nil || len(c.vaultSessions()) > 0 {
		return
	}
	released := make(map[string]bool, len(releasedTargets))
	for _, ep := range releasedTargets {
		if trimmed := strings.TrimSpace(ep); trimmed != "" {
			released[trimmed] = true
		}
	}
	if acct != nil {
		for _, inst := range asMapSlice(acct["instances"]) {
			ep := strings.TrimSpace(asString(inst["endpoint"]))
			if ep == "" {
				ep = strings.TrimSpace(asString(inst["id"]))
			}
			if ep != "" && !released[ep] {
				// Another live (external) instance is still running on this account.
				return
			}
		}
	}
	email := ""
	if acct != nil {
		email = asTrimmedString(acct["email"])
	}
	if email == "" {
		c.mu.Lock()
		if acctMap := asMap(c.data["account"]); acctMap != nil {
			email = asTrimmedString(acctMap["email"])
		}
		c.mu.Unlock()
	}
	if email == "" && c.vault != nil {
		if profVal, ok := c.vault.Get("google_profile"); ok {
			email = asTrimmedString(asMap(profVal)["email"])
		}
	}
	c.cuUsage.EndRun(email, nowUnixFloat())
}

func (c *ColabOneController) configureRemote(ctx context.Context, endpoint string, background bool) (bool, error) {
	startOpGen := c.opGen.Load()
	if background && c.opBusy.Load() {
		return false, nil
	}
	activeEP := c.activeEndpoint()
	targetEP := endpoint
	if targetEP == "" {
		targetEP = activeEP
	}
	c.mu.Lock()
	var rt map[string]any
	if endpoint == "" || endpoint == activeEP {
		rt = asMap(c.data["runtime"])
	} else if st := c.instanceStates[endpoint]; st != nil {
		rt = st.Runtime
	} else if curRT := asMap(c.data["runtime"]); asString(curRT["id"]) == endpoint {
		rt = curRT
	}
	proto, hasProto := asExactInt(rt["protocol"])
	supportsReleaseModes := runtimeSupportsCapability(rt, "idle_release_modes")
	releaseMode, idleMin, cuLimit, countdownSec := c.currentReleasePolicyLocked()
	c.mu.Unlock()

	now := nowUnixFloat()
	effectiveIdle := idleMin
	effectiveDeadline := 0.0
	switch releaseMode {
	case COLABONE_RELEASE_MODE_NEVER:
		if supportsReleaseModes {
			effectiveIdle = 0
		} else {
			effectiveIdle = 240
		}
	case COLABONE_RELEASE_MODE_CU_LIMIT:
		if supportsReleaseModes {
			effectiveIdle = 0
		} else {
			effectiveIdle = 240
		}
		effectiveDeadline = c.computeCUReleaseDeadline(now, cuLimit)
	default:
		if hasProto && proto == 1 {
			effectiveIdle = 4
		}
	}
	cfgPayload := map[string]any{
		"idle_minutes":           effectiveIdle,
		"idle_countdown_seconds": countdownSec,
		"deadline":               effectiveDeadline,
	}
	if supportsReleaseModes {
		cfgPayload["release_mode"] = releaseMode
	}
	if _, err := c.remote(ctx, "POST", "configure", cfgPayload, endpoint, 0); err != nil {
		return false, err
	}

	now = nowUnixFloat()
	curActiveEP := c.activeEndpoint()
	c.mu.Lock()
	if !background || (!c.opBusy.Load() && c.opGen.Load() == startOpGen) {
		if targetEP == curActiveEP {
			c.lastConfig = now
		}
		if targetEP != "" {
			if st := c.instanceStates[targetEP]; st != nil {
				st.LastConfig = now
			}
		}
	}
	c.mu.Unlock()

	if background && (c.opBusy.Load() || c.opGen.Load() != startOpGen) {
		return true, nil
	}
	c.syncRemoteAPIIDOverrides(ctx, endpoint, background)
	return true, nil
}

func (c *ColabOneController) runtimeSupportsAPIAliases(endpoint string) bool {
	activeEP := c.activeEndpoint()
	c.mu.Lock()
	defer c.mu.Unlock()
	var rt map[string]any
	if endpoint == "" || endpoint == activeEP {
		rt = asMap(c.data["runtime"])
	} else if st := c.instanceStates[endpoint]; st != nil {
		rt = st.Runtime
	} else if curRT := asMap(c.data["runtime"]); asString(curRT["id"]) == endpoint {
		rt = curRT
	}
	if rt == nil {
		return false
	}
	if asBool(rt["api_model_aliases"]) {
		return true
	}
	return !asBool(rt["upgrade_available"])
}

func (c *ColabOneController) hasRemoteCapability(capability string) bool {
	if capability == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	rt := asMap(c.data["runtime"])
	if rt == nil || asString(rt["state"]) != "ready" {
		return false
	}
	for _, item := range asAnySlice(rt["capabilities"]) {
		if asString(item) == capability {
			return true
		}
	}
	return false
}

func validateCleanHFToken(raw string) (string, error) {
	return comfyui.ValidateCleanHFToken(raw)
}

func (c *ColabOneController) syncRemoteAPIIDOverrides(ctx context.Context, endpoint string, background bool) {
	startOpGen := c.opGen.Load()
	if background && c.opBusy.Load() {
		return
	}
	activeEP := c.activeEndpoint()
	c.mu.Lock()
	var rt map[string]any
	var models []map[string]any
	if endpoint == "" || endpoint == activeEP {
		rt = asMap(c.data["runtime"])
		models = deepCopyMapSlice(asMapSlice(c.data["models"]))
	} else if st := c.instanceStates[endpoint]; st != nil {
		rt = st.Runtime
		models = deepCopyMapSlice(st.Models)
	}
	if rt == nil || !asBool(rt["api_model_aliases"]) {
		c.mu.Unlock()
		return
	}
	overrides := make(map[string]string, len(c.apiIDOverrides)+len(c.customModels))
	for mid, alias := range c.apiIDOverrides {
		overrides[mid] = alias
	}
	for mid, spec := range c.customModels {
		if _, exists := overrides[mid]; !exists && spec != nil {
			if customAlias := asTrimmedString(spec["api_id"]); customAlias != "" {
				overrides[mid] = customAlias
			}
		}
	}
	c.mu.Unlock()

	if len(overrides) == 0 {
		return
	}
	remoteByID := make(map[string]map[string]any, len(models))
	for _, m := range models {
		if id := asString(m["id"]); id != "" {
			remoteByID[id] = m
		}
	}
	mids := make([]string, 0, len(overrides))
	for mid := range overrides {
		mids = append(mids, mid)
	}
	sort.Strings(mids)

	changed := false
	for _, mid := range mids {
		if background && (c.opBusy.Load() || c.opGen.Load() != startOpGen) {
			return
		}
		alias := overrides[mid]
		rem, exists := remoteByID[mid]
		if !exists || asTrimmedString(rem["api_id"]) == alias {
			continue
		}
		if _, err := c.remote(ctx, "POST", fmt.Sprintf("models/%s/api-id", mid), map[string]any{"api_id": alias}, endpoint, 0); err != nil {
			continue
		}
		changed = true
	}
	if changed {
		if background && (c.opBusy.Load() || c.opGen.Load() != startOpGen) {
			return
		}
		_, _ = c.refreshRemote(ctx, endpoint, background)
	}
}

func (c *ColabOneController) modelLabel(modelID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, item := range asMapSlice(c.data["models"]) {
		if asString(item["id"]) == modelID {
			if n := asString(item["name"]); n != "" {
				return n
			}
			return modelID
		}
	}
	if custom, ok := c.customModels[modelID]; ok && custom != nil {
		if n := asString(custom["name"]); n != "" {
			return n
		}
	}
	return modelID
}

// RuntimeNeedsUpgrade reports whether a ready remote runtime requires a protocol or bundle update.
func (c *ColabOneController) RuntimeNeedsUpgrade(rt map[string]any) bool {
	if rt == nil || asString(rt["state"]) != "ready" {
		return false
	}
	proto, ok := asExactInt(rt["protocol"])
	if ok && proto > 0 && proto < 7 {
		return true
	}
	if ok && proto >= 7 && c.expectedServiceRev != "" && asString(rt["service_revision"]) != c.expectedServiceRev {
		return true
	}
	return false
}

func extractRestoreModelOptions(raw any) map[string]any {
	m := asMap(raw)
	if m == nil {
		return map[string]any{}
	}
	out := make(map[string]any)
	if ctxLen, ok := asExactInt(m["context_length"]); ok && ctxLen >= 512 && ctxLen <= 262144 {
		out["context_length"] = ctxLen
	}
	if temp, ok := asFloat(m["temperature"]); ok && temp >= 0 && temp <= 2 {
		out["temperature"] = temp
	}
	return out
}

func formatModelOptionsLabel(options map[string]any) string {
	parts := make([]string, 0, 2)
	if ctxLen, ok := asExactInt(options["context_length"]); ok && ctxLen > 0 {
		if ctxLen%1024 == 0 {
			parts = append(parts, fmt.Sprintf("%dK", ctxLen/1024))
		} else {
			parts = append(parts, fmt.Sprintf("%d tokens", ctxLen))
		}
	}
	if temp, ok := asFloat(options["temperature"]); ok {
		parts = append(parts, fmt.Sprintf("温度 %g", temp))
	}
	return strings.Join(parts, " · ")
}

func (c *ColabOneController) prepareRemoteForUpgrade(ctx context.Context, endpoint string) (map[string]any, error) {
	rawStatus, err := c.remote(ctx, "GET", "status", nil, endpoint, 0)
	if err != nil {
		return nil, err
	}
	status := asMap(rawStatus)
	if status == nil {
		status = map[string]any{}
	}
	pub := asMap(status["public_api"])
	comfy := asMap(status["comfyui"])
	comfyCanvas := asMap(comfy["canvas"])
	loadedModel := asTrimmedString(status["loaded_model"])
	modelOptions := extractRestoreModelOptions(status["active_options"])
	if loadedModel != "" && len(modelOptions) == 0 {
		activeEP := c.activeEndpoint()
		c.mu.Lock()
		var cachedRT map[string]any
		if endpoint == "" || endpoint == activeEP {
			cachedRT = asMap(c.data["runtime"])
		} else if st := c.instanceStates[endpoint]; st != nil {
			cachedRT = st.Runtime
		}
		if asTrimmedString(cachedRT["loaded_model"]) == loadedModel {
			modelOptions = extractRestoreModelOptions(cachedRT["active_options"])
		}
		c.mu.Unlock()
	}

	pubState := asString(pub["state"])
	wasPublicAPI := asBool(pub["enabled"]) || pubState == "starting" || pubState == "ready" || pubState == "reconnecting"
	comfyState := asString(comfy["state"])
	wasComfyRunning := asBool(comfy["running"]) || comfyState == "starting" || comfyState == "running" || comfyState == "ready" || asString(status["active_engine"]) == "comfyui"
	canvasState := asString(comfyCanvas["state"])
	wasComfyCanvas := asBool(comfyCanvas["enabled"]) || canvasState == "starting" || canvasState == "ready" || canvasState == "reconnecting"

	restorePlan := map[string]any{
		"loaded_model":   loadedModel,
		"model_options":  modelOptions,
		"public_api":     wasPublicAPI,
		"comfyui":        loadedModel == "" && wasComfyRunning,
		"comfyui_canvas": loadedModel == "" && wasComfyRunning && wasComfyCanvas,
	}

	if wasPublicAPI {
		c.log("更新前正在关闭对外 API 服务", "GPU", "info")
		if _, err := c.remote(ctx, "POST", "public-api/stop", nil, endpoint, 0); err != nil {
			return nil, err
		}
		rawStatus, err = c.remote(ctx, "GET", "status", nil, endpoint, 0)
		if err != nil {
			return nil, err
		}
		status = asMap(rawStatus)
		if status == nil {
			status = map[string]any{}
		}
	}

	runningTasks := filterRunningTasks(asMapSlice(status["tasks"]))
	if len(runningTasks) > 0 {
		c.log("更新前正在取消运行中的远端任务", "GPU", "info")
		cancelledIDs := make(map[string]bool)
		deadline := time.Now().Add(15 * time.Second)
		for {
			for _, t := range runningTasks {
				tid := asString(t["id"])
				if tid != "" && !cancelledIDs[tid] && reHex32ID.MatchString(tid) {
					cancelledIDs[tid] = true
					_, _ = c.remote(ctx, "DELETE", "tasks/"+tid, nil, endpoint, 0)
				}
			}
			rawStatus, err = c.remote(ctx, "GET", "status", nil, endpoint, 0)
			if err != nil {
				return nil, err
			}
			status = asMap(rawStatus)
			if status == nil {
				status = map[string]any{}
			}
			runningTasks = filterRunningTasks(asMapSlice(status["tasks"]))
			if len(runningTasks) == 0 || !time.Now().Before(deadline) {
				break
			}
			if c.stop.Wait(c.upgradePollDelay) {
				break
			}
		}
	}

	liveStudio := asMap(status["live_studio"])
	liveState := asString(liveStudio["state"])
	activeEngine := asString(status["active_engine"])
	if asBool(liveStudio["running"]) ||
		liveState == "starting" || liveState == "ready" || liveState == "stopping" ||
		strings.Contains(activeEngine, "live_studio") || activeEngine == "live" {
		c.log("更新前正在停止实时演播服务", "GPU", "info")
		if _, err := c.remote(ctx, "POST", "live/stop", nil, endpoint, 0); err != nil {
			return nil, err
		}
		rawStatus, err = c.remote(ctx, "GET", "status", nil, endpoint, 0)
		if err != nil {
			return nil, err
		}
		status = asMap(rawStatus)
		if status == nil {
			status = map[string]any{}
		}
	}

	comfy = asMap(status["comfyui"])
	comfyCanvas = asMap(comfy["canvas"])
	comfyState = asString(comfy["state"])
	canvasState = asString(comfyCanvas["state"])
	if asBool(comfy["running"]) ||
		comfyState == "starting" || comfyState == "running" || comfyState == "ready" ||
		asBool(comfyCanvas["enabled"]) ||
		canvasState == "starting" || canvasState == "ready" || canvasState == "reconnecting" ||
		asString(status["active_engine"]) == "comfyui" {
		c.log("更新前正在停止 ComfyUI 服务", "GPU", "info")
		if _, err := c.remote(ctx, "POST", "comfyui/stop", nil, endpoint, 0); err != nil {
			return nil, err
		}
		rawStatus, err = c.remote(ctx, "GET", "status", nil, endpoint, 0)
		if err != nil {
			return nil, err
		}
		status = asMap(rawStatus)
		if status == nil {
			status = map[string]any{}
		}
	}

	if asBool(status["loaded_model"]) || asBool(status["active_engine"]) {
		c.log("更新前正在卸载当前显存模型（保留磁盘缓存）", "GPU", "info")
		if _, err := c.remote(ctx, "POST", "unload", nil, endpoint, 0); err != nil {
			return nil, err
		}
	}
	return restorePlan, nil
}

func filterRunningTasks(tasks []map[string]any) []map[string]any {
	out := make([]map[string]any, 0)
	for _, t := range tasks {
		if t != nil && asString(t["state"]) == "running" {
			out = append(out, t)
		}
	}
	return out
}

// RestoreComfyCanvasWhenReady polls until ComfyUI is running and restores the native canvas tunnel.
func (c *ColabOneController) RestoreComfyCanvasWhenReady(ctx context.Context, canvasPayload map[string]any, endpoint, taskID string, timeout time.Duration) {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) && !c.stop.IsSet() {
		if ctx != nil && ctx.Err() != nil {
			return
		}
		if endpoint != "" {
			if _, ok := c.vaultSessions()[endpoint]; !ok {
				return
			}
		}
		rawStatus, err := c.remote(ctx, "GET", "status", nil, endpoint, 0)
		if err != nil {
			if c.stop.Wait(c.canvasWaitInterval) {
				return
			}
			continue
		}
		status := asMap(rawStatus)
		if asBool(status["loaded_model"]) || asString(status["active_engine"]) == "llama" {
			return
		}
		comfy := asMap(status["comfyui"])
		canvas := asMap(comfy["canvas"])
		if asBool(comfy["running"]) {
			canvasState := asString(canvas["state"])
			if asBool(canvas["enabled"]) || canvasState == "starting" || canvasState == "ready" || canvasState == "reconnecting" {
				return
			}
			c.log("ComfyUI 已就绪，正在按原配置自动恢复原生画布隧道", "ComfyUI", "info")
			if _, err := c.remote(ctx, "POST", "comfyui/canvas/start", canvasPayload, endpoint, 0); err != nil {
				c.log(fmt.Sprintf("自动恢复 ComfyUI 原生画布失败，请手动开启：%v", err), "ComfyUI", "warning")
			} else {
				_, _ = c.refreshRemote(ctx, endpoint, true)
			}
			return
		}
		tasks := asMapSlice(status["tasks"])
		if taskID != "" {
			for _, t := range tasks {
				if asString(t["id"]) == taskID {
					st := asString(t["state"])
					if st == "failed" || st == "cancelled" || st == "completed" {
						return
					}
					break
				}
			}
		}
		hasRunning := false
		for _, t := range tasks {
			if asString(t["state"]) == "running" {
				hasRunning = true
				break
			}
		}
		if asString(comfy["state"]) == "stopped" && !hasRunning {
			return
		}
		if c.stop.Wait(c.canvasWaitInterval) {
			return
		}
	}
	if !c.stop.IsSet() {
		c.log("等待 ComfyUI 启动超时，未能自动恢复原生画布，请手动开启", "ComfyUI", "warning")
	}
}

func (c *ColabOneController) restoreRemoteAfterUpgrade(ctx context.Context, plan map[string]any, endpoint string) {
	if plan == nil {
		return
	}
	loadedModel := asTrimmedString(plan["loaded_model"])
	modelOptions := extractRestoreModelOptions(plan["model_options"])
	wantPublicAPI := asBool(plan["public_api"])
	wantComfyUI := asBool(plan["comfyui"]) && loadedModel == ""
	wantComfyCanvas := asBool(plan["comfyui_canvas"]) && wantComfyUI
	if loadedModel == "" && !wantPublicAPI && !wantComfyUI {
		return
	}

	changed := false
	if loadedModel != "" {
		label := c.modelLabel(loadedModel)
		specLabel := formatModelOptionsLabel(modelOptions)
		suffix := ""
		if specLabel != "" {
			suffix = "（" + specLabel + "）"
		}
		if err := c.ensureRemoteModel(ctx, loadedModel, endpoint); err != nil {
			c.log(fmt.Sprintf("自动重新装载模型 %s 失败，请手动装载：%v", label, err), "模型", "warning")
		} else {
			c.log(fmt.Sprintf("正在按更新前规格自动重新装载模型 %s%s", label, suffix), "模型", "info")
			if _, err := c.remote(ctx, "POST", "models/"+loadedModel+"/load", modelOptions, endpoint, 0); err != nil {
				c.log(fmt.Sprintf("自动重新装载模型 %s 失败，请手动装载：%v", label, err), "模型", "warning")
			} else {
				changed = true
			}
		}
	}

	if wantPublicAPI {
		c.vaultConfigMu.Lock()
		saved := map[string]any{}
		if v, ok := c.vault.Get("public_api"); ok {
			if m := asMap(v); m != nil {
				saved = deepCopyMap(m)
			}
		}
		c.vaultConfigMu.Unlock()
		apiConfig, err := c.validatePublicAPIConfig(map[string]any{
			"mode":         "named",
			"domain":       asString(saved["domain"]),
			"tunnel_token": asString(saved["tunnel_token"]),
			"api_key":      asString(saved["api_key"]),
		})
		if err != nil {
			c.log("更新前对外 API 已开启，但本地缺少完整固定域名或隧道配置，请手动重新开启", "API", "warning")
		} else {
			c.log("正在按更新前配置自动重新开启对外 API 服务与公网隧道", "API", "info")
			if _, err := c.remote(ctx, "POST", "public-api/start", apiConfig, endpoint, 0); err != nil {
				c.log(fmt.Sprintf("自动恢复对外 API 失败，请手动开启：%v", err), "API", "warning")
			} else {
				changed = true
			}
		}
	}

	if wantComfyUI {
		comfyTaskID := ""
		comfyStarted := false
		c.log("正在自动重新启动 ComfyUI 工作流引擎", "ComfyUI", "info")
		res, err := c.remote(ctx, "POST", "comfyui/start", nil, endpoint, 0)
		if err != nil {
			c.log(fmt.Sprintf("自动重新启动 ComfyUI 失败，请手动启动：%v", err), "ComfyUI", "warning")
		} else {
			if m := asMap(res); m != nil {
				comfyTaskID = asString(m["id"])
			}
			comfyStarted = true
			changed = true
		}

		if comfyStarted && wantComfyCanvas {
			c.vaultConfigMu.Lock()
			savedCanvas := map[string]any{}
			if v, ok := c.vault.Get("comfyui_canvas"); ok {
				if m := asMap(v); m != nil {
					savedCanvas = deepCopyMap(m)
				}
			}
			c.vaultConfigMu.Unlock()
			canvasPayload, err := c.validateComfyCanvasConfig(map[string]any{
				"mode":         "named",
				"domain":       asString(savedCanvas["domain"]),
				"tunnel_token": asString(savedCanvas["tunnel_token"]),
			})
			if err != nil {
				c.log("更新前 ComfyUI 原生画布已开启，但本地缺少完整隧道配置，请手动重新开启", "ComfyUI", "warning")
			} else {
				startedNow := false
				if rawCur, err := c.remote(ctx, "GET", "status", nil, endpoint, 0); err == nil {
					curStatus := asMap(rawCur)
					curComfy := asMap(curStatus["comfyui"])
					curCanvas := asMap(curComfy["canvas"])
					if asBool(curComfy["running"]) {
						st := asString(curCanvas["state"])
						if asBool(curCanvas["enabled"]) || st == "starting" || st == "ready" || st == "reconnecting" {
							startedNow = true
						} else {
							c.log("正在按更新前配置自动恢复 ComfyUI 原生画布隧道", "ComfyUI", "info")
							if _, err := c.remote(ctx, "POST", "comfyui/canvas/start", canvasPayload, endpoint, 0); err == nil {
								startedNow = true
								changed = true
							}
						}
					}
				}
				if !startedNow {
					c.log("已预约 ComfyUI 就绪后自动按原配置恢复原生画布隧道", "ComfyUI", "info")
					payloadCopy := deepCopyMap(canvasPayload)
					epCopy := endpoint
					tidCopy := comfyTaskID
					c.spawn(func() {
						c.RestoreComfyCanvasWhenReady(c.rootCtx, payloadCopy, epCopy, tidCopy, 120*time.Second)
					})
				}
			}
		}
	}

	if changed {
		_ = c.RefreshRemote(ctx, endpoint)
	}
}

// UpgradeService updates the remote ColabOne service bundle, optionally tearing down
// and auto-restoring running workloads when force is true.
func (c *ColabOneController) UpgradeService(ctx context.Context, force, restore bool) error {
	if c.provider == nil {
		return errors.New("远端实例未连接")
	}
	targetEndpoint := c.activeEndpoint()
	var restorePlan map[string]any

	if force {
		var err error
		restorePlan, err = c.prepareRemoteForUpgrade(ctx, targetEndpoint)
		if err != nil {
			_ = c.RefreshRemote(ctx, targetEndpoint)
			return err
		}
	}
	c.log("更新远端管理服务，保留模型缓存与工作流", "GPU", "info")
	if err := c.provider.UpgradeService(ctx, targetEndpoint, c.logFunc()); err != nil {
		if force {
			_ = c.RefreshRemote(ctx, targetEndpoint)
		}
		return err
	}

	c.log("正在刷新远端服务状态", "GPU", "info")
	if err := c.RefreshRemote(ctx, targetEndpoint); err != nil {
		return err
	}
	c.log("正在重新同步空闲自动释放策略", "GPU", "info")
	if err := c.ConfigureRemote(ctx, targetEndpoint); err != nil {
		return err
	}
	c.syncRemoteAPIIDOverrides(ctx, targetEndpoint, false)
	if force && restore && restorePlan != nil {
		c.restoreRemoteAfterUpgrade(ctx, restorePlan, targetEndpoint)
	}
	c.log("远端管理服务已更新，支持模型 API、ComfyUI 与闲置保护", "GPU", "info")
	return nil
}

// RefreshRemote fetches the latest snapshot from the remote runtime and updates caches.
func (c *ColabOneController) RefreshRemote(ctx context.Context, endpoint string) error {
	_, err := c.refreshRemote(ctx, endpoint, false)
	return err
}

func (c *ColabOneController) refreshRemote(ctx context.Context, endpoint string, background bool) (map[string]any, error) {
	startOpGen := c.opGen.Load()
	if background && c.opBusy.Load() {
		return nil, nil
	}
	hadSessions := len(c.vaultSessions()) > 0
	rawState, err := c.remote(ctx, "GET", "snapshot", nil, endpoint, 0)
	if err != nil {
		return nil, err
	}
	state := asMap(rawState)
	if state == nil {
		return nil, errors.New("远端快照格式无效")
	}
	rt := asMap(state["runtime"])
	if rt == nil {
		rt = map[string]any{}
		state["runtime"] = rt
	}
	rt["upgrade_available"] = c.RuntimeNeedsUpgrade(rt)

	sessions := c.vaultSessions()
	effective := endpoint
	if effective == "" {
		effective = asString(rt["id"])
	}
	target := effective
	if target == "" {
		target = c.activeEndpointFrom(sessions)
	}
	_, targetOwned := sessions[target]
	stillOwned := (!hadSessions && len(sessions) == 0) || (target != "" && targetOwned)

	rawWorkflows := asMapSlice(state["workflows"])
	mergedWorkflows := c.mergeWorkflows(rawWorkflows)
	mergedTtsEngines := c.mergeTtsEngines(asMapSlice(state["tts_engines"]))
	comfyOutputs := asAnySlice(state["comfy_outputs"])
	if comfyOutputs == nil {
		comfyOutputs = []any{}
	}
	liveAvatars := asMapSlice(state["live_avatars"])
	if liveAvatars == nil {
		liveAvatars = []map[string]any{}
	}
	liveDrivingMotions := asMapSlice(state["live_driving_motions"])
	if liveDrivingMotions == nil {
		liveDrivingMotions = []map[string]any{}
	}
	liveCatalog := deepCopyMap(asMap(state["live_catalog"]))
	if liveCatalog == nil {
		liveCatalog = map[string]any{}
	}
	remoteVersions := make(map[string]float64, len(rawWorkflows))
	for _, w := range rawWorkflows {
		if id := asString(w["id"]); id != "" {
			ts, _ := asFloat(w["updated_at"])
			remoteVersions[id] = ts
		}
	}
	models := asMapSlice(state["models"])
	if models == nil {
		models = []map[string]any{}
	}

	if target != "" && stillOwned {
		remotes := c.vaultRemotes()
		rem := remotes[target]
		loc := asMap(rt["location"])
		if len(loc) == 0 {
			loc = asMap(rem["location"])
		}
		if len(loc) == 0 {
			loc = c.inferLocation(target, asString(sessions[target]["url"]))
		}
		if len(loc) > 0 {
			rt["location"] = deepCopyMap(loc)
			if targetOwned && asString(rem["endpoint"]) == target && !mapsEqual(asMap(rem["location"]), loc) {
				remCopy := deepCopyMap(rem)
				remCopy["location"] = deepCopyMap(loc)
				if c.provider != nil && (!background || (!c.opBusy.Load() && c.opGen.Load() == startOpGen)) {
					_ = c.provider.SaveRemote(remCopy, false)
				}
			}
		}
	}

	curSessions := c.vaultSessions()
	activeEP := c.activeEndpointFrom(curSessions)
	_, curOwned := curSessions[target]
	curStillOwned := (!hadSessions && len(curSessions) == 0) || (target != "" && curOwned)

	var pendingLogs []pendingActivityLog
	c.mu.Lock()
	if background && (c.opBusy.Load() || c.opGen.Load() != startOpGen) {
		c.mu.Unlock()
		return state, nil
	}
	if target != "" && curStillOwned {
		entry := c.ensureInstanceStateLocked(target)
		entry.Runtime = deepCopyMap(rt)
		entry.Models = deepCopyMapSlice(models)
		entry.Workflows = deepCopyMapSlice(mergedWorkflows)
		entry.TtsEngines = deepCopyMapSlice(mergedTtsEngines)
		entry.ComfyOutputs = deepCopySlice(comfyOutputs)
		entry.LiveAvatars = deepCopyMapSlice(liveAvatars)
		entry.LiveDrivingMotions = deepCopyMapSlice(liveDrivingMotions)
		entry.LiveCatalog = deepCopyMap(liveCatalog)
		verCopy := make(map[string]float64, len(remoteVersions))
		for k, v := range remoteVersions {
			verCopy[k] = v
		}
		entry.RemoteWorkflowVersions = verCopy
	}
	if curStillOwned &&
		(endpoint == "" || endpoint == activeEP) &&
		(effective == "" || activeEP == "" || effective == activeEP) {
		c.remoteWorkflowVersions = remoteVersions
		pendingLogs = c.updateWithSessionsLocked(map[string]any{
			"runtime":              rt,
			"models":               models,
			"workflows":            mergedWorkflows,
			"tts_engines":          mergedTtsEngines,
			"comfy_outputs":        comfyOutputs,
			"live_avatars":         liveAvatars,
			"live_driving_motions": liveDrivingMotions,
			"live_catalog":         liveCatalog,
		}, curSessions, activeEP)
	}
	c.mu.Unlock()
	c.flushActivityLogs(pendingLogs)
	if curStillOwned && asString(rt["state"]) == "ready" {
		c.recordSyncedRemoteOutputs(rt, comfyOutputs)
	}
	return state, nil
}

func mapsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || fmt.Sprintf("%v", va) != fmt.Sprintf("%v", vb) {
			return false
		}
	}
	return true
}

// SelectInstance switches the active managed GPU instance.
func (c *ColabOneController) SelectInstance(ctx context.Context, endpoint string) (map[string]any, error) {
	if !c.opBusy.CompareAndSwap(false, true) {
		return nil, errors.New("请等待当前操作完成后再切换实例")
	}
	busyReleased := false
	defer func() {
		if !busyReleased {
			c.opBusy.Store(false)
		}
	}()
	sessions := c.vaultSessions()
	if endpoint == "" {
		return nil, errors.New("所选实例不存在或不属于当前客户端")
	}
	if _, ok := sessions[endpoint]; !ok {
		return nil, errors.New("所选实例不存在或不属于当前客户端")
	}
	c.saveActiveToCache()
	if c.provider != nil {
		if _, err := c.provider.SetActive(endpoint); err != nil {
			return nil, err
		}
	}
	c.opGen.Add(1)
	c.activateCached(endpoint)
	c.resetMonitorToAuto(true, false)
	c.mu.Lock()
	c.lastConfig = 0
	c.mu.Unlock()
	c.opGen.Add(1)
	busyReleased = true
	c.opBusy.Store(false)
	c.log("已切换当前工作实例："+endpoint, "GPU", "info")
	if !c.opBusy.Load() {
		ep := endpoint
		c.spawn(func() {
			_, _ = c.refreshRemote(c.rootCtx, ep, true)
		})
	}
	return map[string]any{"ok": true, "endpoint": endpoint}, nil
}

// ---------------------------------------------------------------------------
// Google Drive dual-mode mounting (Headless OAuth rclone + Colab DriveFS)
// ---------------------------------------------------------------------------

func (c *ColabOneController) resolveHeadlessRcloneConf(ctx context.Context, forceRefresh bool) (string, string) {
	if c.auth == nil {
		return "", "no_drive_scope"
	}
	needsQuota := c.auth.RequiresDriveQuotaProject()
	if needsQuota {
		projectID, reason, err := c.auth.EnsureDriveQuotaProject(ctx, forceRefresh, c.logFunc())
		if err != nil || projectID == "" {
			if reason == "" {
				reason = "gcp_project_unavailable"
			}
			return "", reason
		}
		conf, err := c.auth.BuildRcloneConf(ctx, projectID)
		if err == nil && strings.Contains(conf, "[colabone_gdrive]") {
			return conf, "rclone"
		}
		return "", "no_drive_scope"
	}
	conf, err := c.auth.BuildRcloneConf(ctx, "")
	if err == nil && strings.Contains(conf, "[colabone_gdrive]") {
		return conf, "rclone"
	}
	return "", "no_drive_scope"
}

func isQuotaProjectError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{"accessnotconfigured", "service_disabled", "quota project", "配额项目"} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// SetFocusWindowHandler registers an optional desktop UI callback invoked to
// bring the main window back to the foreground after interactive browser auth.
func (c *ColabOneController) SetFocusWindowHandler(fn func()) {
	c.mu.Lock()
	c.onFocusWindow = fn
	c.mu.Unlock()
}

func (c *ColabOneController) focusWindow() {
	c.mu.Lock()
	fn := c.onFocusWindow
	c.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (c *ColabOneController) connectColabDriveFS(ctx context.Context, silentOnly bool) (map[string]any, error) {
	if c.provider == nil {
		return nil, nil
	}
	if silentOnly {
		authorized, _, err := c.provider.CheckColabDriveFSAuth(ctx, "")
		if err != nil || !authorized {
			return nil, nil
		}
	}
	c.driveCancel.Clear()
	c.driveConfirm.Clear()
	c.driveProbe.Clear()
	c.Update(map[string]any{
		"operation": map[string]any{
			"title":       "正在通过 Colab 原生 DriveFS 挂载 Google 云盘 (ColabOne_Drive_Mount)",
			"running":     true,
			"kind":        "drive_connect",
			"stage":       "checking",
			"mode":        "colab_drivefs",
			"interactive": false,
		},
		"auth_url":        "",
		"drive_auth_hint": "",
	})

	accountEmail := ""
	if profVal, ok := c.vault.Get("google_profile"); ok {
		accountEmail = asTrimmedString(asMap(profVal)["email"])
	}
	if accountEmail == "" {
		c.mu.Lock()
		accountEmail = asTrimmedString(asMap(c.data["account"])["email"])
		c.mu.Unlock()
	}

	var interactiveAuth atomic.Bool
	mountRes, err := c.provider.MountColabDriveFS(ctx, ColabOneDriveMountOptions{
		AccountEmail: accountEmail,
		OnAuthURL: func(u string) {
			if u != "" {
				if c.driveCancel.IsSet() || (ctx != nil && ctx.Err() != nil) {
					return
				}
				interactiveAuth.Store(true)
				c.Update(map[string]any{
					"auth_url": u,
					"operation": map[string]any{
						"title":       "正在通过 Colab 原生 DriveFS 挂载 Google 云盘 (ColabOne_Drive_Mount)",
						"running":     true,
						"kind":        "drive_connect",
						"stage":       "waiting_auth",
						"mode":        "colab_drivefs",
						"interactive": true,
					},
				})
				return
			}
			c.Update(map[string]any{"auth_url": ""})
		},
		OnAuthConfirmed: func(interactive bool) {
			if c.driveCancel.IsSet() || (ctx != nil && ctx.Err() != nil) {
				return
			}
			interactiveAuth.Store(interactive)
			title := "正在通过 Colab 原生 DriveFS 挂载 Google 云盘 (ColabOne_Drive_Mount)"
			if interactive {
				title = "授权已确认，正在远端挂载 Google 云盘 (ColabOne_Drive_Mount)"
			}
			c.Update(map[string]any{
				"operation": map[string]any{
					"title":       title,
					"running":     true,
					"kind":        "drive_connect",
					"stage":       "mounting",
					"mode":        "colab_drivefs",
					"interactive": interactive,
				},
				"auth_url":        "",
				"drive_auth_hint": "",
			})
			if interactive && !silentOnly {
				c.focusWindow()
			}
		},
		OnHint:       func(h string) { c.Update(map[string]any{"drive_auth_hint": h}) },
		OnLog:        c.logFunc(),
		CancelEvent:  c.driveCancel,
		ConfirmEvent: c.driveConfirm,
		ProbeEvent:   c.driveProbe,
	})
	c.Update(map[string]any{"auth_url": "", "drive_auth_hint": ""})
	if err != nil {
		if c.driveCancel.IsSet() {
			return nil, &ColabOneOperationCancelled{Message: "已取消 Google 云盘挂载", Source: "云盘"}
		}
		return nil, err
	}
	if c.driveCancel.IsSet() {
		return nil, &ColabOneOperationCancelled{Message: "已取消 Google 云盘挂载", Source: "云盘"}
	}
	if mountRes == nil {
		return nil, nil
	}
	c.Update(map[string]any{
		"operation": map[string]any{
			"title":       "正在初始化 ColabOne_Drive_Mount 工作区并恢复持久化数据",
			"running":     true,
			"kind":        "drive_connect",
			"stage":       "initializing",
			"mode":        "colab_drivefs",
			"interactive": interactiveAuth.Load(),
		},
		"auth_url":        "",
		"drive_auth_hint": "",
	})
	c.log("正在初始化 ColabOne_Drive_Mount 工作区并恢复持久化数据", "云盘", "info")
	res, err := c.remoteLong(ctx, "POST", "drive/connect", nil, 180*time.Second)
	if err != nil {
		return nil, err
	}
	return asMap(res), nil
}

// ConnectDrive connects Google Drive using headless rclone when available, falling back cleanly to Colab DriveFS.
func (c *ColabOneController) ConnectDrive(ctx context.Context) (map[string]any, error) {
	if c.provider == nil {
		return nil, errors.New("Provider 未配置")
	}
	c.log("正在检查 Google 云盘挂载方式与授权状态", "云盘", "info")
	cloudProj, _ := c.vault.Get("cloud_project")
	rcloneConf, reason := c.resolveHeadlessRcloneConf(ctx, !asBool(cloudProj))
	if rcloneConf != "" {
		c.log("正在上传无感云盘凭据到远端实例", "云盘", "info")
		err := c.provider.UploadDriveConfig(ctx, rcloneConf, "")
		var info map[string]any
		if err == nil {
			c.Update(map[string]any{
				"operation": map[string]any{
					"title":       "正在远端初始化 rclone 并挂载 ColabOne_Drive",
					"running":     true,
					"kind":        "drive_connect",
					"stage":       "mounting",
					"mode":        "rclone",
					"interactive": false,
				},
			})
			c.log("正在远端初始化 rclone 并挂载 ColabOne_Drive", "云盘", "info")
			var raw any
			raw, err = c.remoteLong(ctx, "POST", "drive/connect", nil, 180*time.Second)
			if err == nil {
				info = asMap(raw)
				c.log("正在刷新云盘与远端工作区状态", "云盘", "info")
				err = c.RefreshRemote(ctx, "")
			}
		}
		if err == nil {
			c.syncRemoteAPIIDOverrides(ctx, "", false)
			if asString(info["mode"]) == "mount" {
				c.log("已通过 Google OAuth 自动挂载云盘 (/content/drive/MyDrive/ColabOne_Drive)", "云盘", "info")
			} else {
				c.log("已通过 Google OAuth 连接云盘持久化目录 (ColabOne_Drive)", "云盘", "info")
			}
			return info, nil
		}
		if !isQuotaProjectError(err) {
			return nil, err
		}
		if c.auth != nil {
			c.auth.InvalidateDriveQuotaProject()
		}
		c.log("无感配额项目暂不可用，自动切换为 Google Colab 官方原生 DriveFS 挂载", "云盘", "warning")
	}

	if reason == "gcp_tos_required" {
		c.log("当前账户尚未开通免费 GCP 配额项目，自动切换为 Google Colab 官方原生 DriveFS 挂载", "云盘", "info")
	} else if rcloneConf == "" {
		c.log("正在通过 Google Colab 官方原生 DriveFS 挂载云盘 (ColabOne_Drive_Mount)", "云盘", "info")
	}

	info, err := c.connectColabDriveFS(ctx, false)
	if err != nil {
		return nil, err
	}
	if info == nil {
		return nil, errors.New("当前保存的登录缺少云盘权限，请在设置中点击一次「重新登录」完成授权，后续所有实例永久自动生效")
	}
	c.log("正在刷新云盘与远端工作区状态", "云盘", "info")
	if err := c.RefreshRemote(ctx, ""); err != nil {
		return nil, err
	}
	c.syncRemoteAPIIDOverrides(ctx, "", false)
	c.log("已通过 Google Colab 原生 DriveFS 挂载云盘 (/content/drive/MyDrive/ColabOne_Drive_Mount)", "云盘", "info")
	return info, nil
}

// SetupHeadlessDrive checks or provisions a free GCP quota project for headless Drive mounting.
func (c *ColabOneController) SetupHeadlessDrive(ctx context.Context) (map[string]any, error) {
	if v, ok := c.vault.Get("colab"); !ok || !asBool(v) {
		return nil, errors.New("请先登录 Google 账号")
	}
	if c.auth == nil {
		return nil, errors.New("暂未能自动创建免费 GCP 配额项目（可能受账号或组织策略限制），仍可正常使用 Colab 原生云盘挂载。")
	}
	if !c.auth.HasDriveScope() {
		return nil, errors.New("当前登录凭据缺少云盘免弹窗所需权限，请在设置中点击「重新登录」后再试。")
	}
	c.log("正在检测并开启 Google 云盘永久免弹窗挂载模式", "云盘", "info")
	projectID, reason, err := c.auth.EnsureDriveQuotaProject(ctx, true, c.logFunc())
	if err != nil && reason == "" {
		return nil, errors.New("检测 Google Cloud 配额项目时网络异常，请检查网络或代理后重试。")
	}
	if reason == "not_required" {
		c.log("当前 Google 凭据已支持云盘永久免弹窗自动挂载", "云盘", "info")
		c.triggerBackgroundDriveOutputsSync()
		return map[string]any{"ok": true, "headless": true, "project_id": "", "reason": reason}, nil
	}
	if projectID != "" {
		if reason != "created_colabone_project" {
			c.log(fmt.Sprintf("已确认免弹窗云盘配额项目 (%s)，后续所有实例免弹窗自动挂载", projectID), "云盘", "info")
		}
		c.triggerBackgroundDriveOutputsSync()
		return map[string]any{
			"ok":         true,
			"headless":   true,
			"project_id": projectID,
			"reason":     reason,
		}, nil
	}
	switch reason {
	case "gcp_tos_required":
		return nil, errors.New("尚未检测到 Google Cloud 服务条款同意记录。请先打开 Google Cloud 同意一次服务条款（免费、无需绑卡），再重试检测。")
	case "no_cloud_scope":
		return nil, errors.New("当前登录凭据缺少云盘免弹窗所需权限，请在设置中点击「重新登录」后再试。")
	case "gcp_project_unavailable":
		return nil, errors.New("暂未能自动创建免费 GCP 配额项目（可能受账号或组织策略限制），仍可正常使用 Colab 原生云盘挂载。")
	default:
		return nil, errors.New("检测 Google Cloud 配额项目时网络异常，请检查网络或代理后重试。")
	}
}

// SyncDrive triggers a manual Google Drive workspace sync on the active runtime.
func (c *ColabOneController) SyncDrive(ctx context.Context) (map[string]any, error) {
	if len(c.vaultSessions()) == 0 {
		return nil, errors.New("请先启动远端 GPU 实例")
	}
	c.log("正在将远端配置、工作流与作品同步至 Google 云盘", "云盘", "info")
	raw, err := c.remoteLong(ctx, "POST", "drive/sync", nil, 300*time.Second)
	if err != nil {
		if isQuotaProjectError(err) && c.auth != nil {
			c.auth.InvalidateDriveQuotaProject()
		}
		return nil, err
	}
	result := asMap(raw)
	refreshErr := c.RefreshRemote(ctx, "")
	if !asBool(result["ok"]) {
		msg := strings.TrimSpace(asString(result["error"]))
		if msg == "" {
			msg = "Google 云盘同步未完成，请重试"
		}
		syncErr := errors.New(msg)
		if isQuotaProjectError(syncErr) && c.auth != nil {
			c.auth.InvalidateDriveQuotaProject()
		}
		return nil, syncErr
	}
	if refreshErr != nil {
		return nil, refreshErr
	}
	remoteFolder := strings.TrimSpace(asString(result["remote_folder"]))
	if remoteFolder == "" {
		if strings.TrimSpace(asString(result["mode"])) == "colab_drivefs" {
			remoteFolder = colabOneDriveFSFolderName
		} else {
			remoteFolder = colabOneDriveFolderName
		}
	}
	c.log(fmt.Sprintf("Google 云盘同步已完成 (%s)", remoteFolder), "云盘", "info")
	return result, nil
}

// ---------------------------------------------------------------------------
// GPU Start, Release, PreviewReleaseAll, and ReleaseAll
// ---------------------------------------------------------------------------

// Start allocates and boots a remote GPU runtime with rollback on error or user cancel.
func (c *ColabOneController) Start(ctx context.Context, createNew bool, gpu string) error {
	c.mu.Lock()
	targetGPU := gpu
	if targetGPU == "" {
		targetGPU = asString(c.settings["gpu"])
	}
	autoDrive := asBoolDefault(c.settings, "auto_drive", true)
	proxyEnabled := asBool(c.settings["assign_proxy_enabled"])
	idleMin, _ := asInt(c.settings["idle_minutes"])
	assignMaxAttempts, ok := asExactInt(c.settings["assign_max_attempts"])
	if !ok || assignMaxAttempts < COLABONE_MIN_ASSIGN_MAX_ATTEMPTS || assignMaxAttempts > COLABONE_MAX_ASSIGN_MAX_ATTEMPTS {
		assignMaxAttempts = COLABONE_DEFAULT_ASSIGN_MAX_ATTEMPTS
	}
	c.mu.Unlock()

	if !validColabOneGPUs[targetGPU] {
		return errors.New("GPU 型号不合法")
	}

	initialSessions := c.vaultSessions()
	previousActive := c.activeEndpointFrom(initialSessions)
	existingSessions := make(map[string]bool, len(initialSessions))
	for ep := range initialSessions {
		existingSessions[ep] = true
	}

	defer func() {
		c.startCancel.Clear()
	}()

	baseStartOpLocked := func(title string, hasTitle bool) map[string]any {
		prev := asMap(c.data["operation"])
		op := make(map[string]any)
		for k, v := range prev {
			op[k] = v
		}
		setDefault := func(k string, v any) {
			if _, ok := op[k]; !ok {
				op[k] = v
			}
		}
		setDefault("kind", "runtime_start")
		setDefault("gpu", targetGPU)
		setDefault("cancellable", true)
		setDefault("cancelling", false)
		setDefault("waiting_retry", false)
		setDefault("retry_at", 0.0)
		setDefault("allocated", false)
		op["running"] = true
		if hasTitle {
			op["title"] = title
		}
		return op
	}

	notify := func(message string) {
		activeEP := c.activeEndpoint()
		remotes := c.vaultRemotes()
		c.mu.Lock()
		current := deepCopyMap(asMap(c.data["runtime"]))
		if current == nil {
			current = map[string]any{}
		}
		if asString(current["state"]) == "ready" {
			pendingLogs := c.updateLocked(map[string]any{
				"operation": map[string]any{"title": message, "running": true},
			})
			c.mu.Unlock()
			c.flushActivityLogs(pendingLogs)
			return
		}
		op := baseStartOpLocked("", false)
		if c.startCancel.IsSet() {
			op["cancellable"] = false
			op["cancelling"] = true
			op["waiting_retry"] = false
			op["retry_at"] = 0.0
			if strings.Contains(message, "取消") {
				op["title"] = message
			}
		} else {
			op["title"] = message
		}
		nextRT := deepCopyMap(current)
		nextRT["state"] = "starting"
		if activeEP != "" && (!createNew || !existingSessions[activeEP]) {
			if asString(nextRT["id"]) == "" {
				nextRT["id"] = activeEP
			}
			if rem := remotes[activeEP]; rem != nil {
				if loc := asMap(rem["location"]); len(loc) > 0 && len(asMap(nextRT["location"])) == 0 {
					nextRT["location"] = deepCopyMap(loc)
				}
			}
		}
		pendingLogs := c.updateLocked(map[string]any{
			"runtime":   nextRT,
			"operation": op,
		})
		c.mu.Unlock()
		c.flushActivityLogs(pendingLogs)
	}

	onRetryState := func(info map[string]any) {
		if info == nil {
			return
		}
		c.mu.Lock()
		prev := asMap(c.data["operation"])
		if prev == nil || asString(prev["kind"]) != "runtime_start" {
			c.mu.Unlock()
			return
		}
		op := deepCopyMap(prev)
		if c.startCancel.IsSet() || asBool(op["cancelling"]) {
			op["cancellable"] = false
			op["cancelling"] = true
			op["waiting_retry"] = false
			op["retry_at"] = 0.0
			if asBool(info["allocated"]) {
				op["allocated"] = true
			}
			pendingLogs := c.updateLocked(map[string]any{"operation": op})
			c.mu.Unlock()
			c.flushActivityLogs(pendingLogs)
			return
		}
		for _, k := range []string{
			"waiting_retry",
			"retry_at",
			"attempt",
			"next_attempt",
			"total_attempts",
			"retry_kind",
			"allocated",
			"gpu",
		} {
			if v, exists := info[k]; exists {
				op[k] = v
			}
		}
		pendingLogs := c.updateLocked(map[string]any{"operation": op})
		c.mu.Unlock()
		c.flushActivityLogs(pendingLogs)
	}

	onBootTelemetry := func(endpoint string, location map[string]any, sample map[string]any) {
		if sample == nil {
			return
		}
		c.mu.Lock()
		current := deepCopyMap(asMap(c.data["runtime"]))
		if asString(current["state"]) != "starting" {
			c.mu.Unlock()
			return
		}
		nextRT := deepCopyMap(current)
		nextRT["state"] = "starting"
		if endpoint != "" {
			nextRT["id"] = endpoint
		}
		if len(location) > 0 {
			nextRT["location"] = deepCopyMap(location)
		}
		if gpuMap := asMap(sample["gpu"]); gpuMap != nil {
			nextRT["gpu"] = deepCopyMap(gpuMap)
		}
		nextRT["resources"] = deepCopyMap(sample)
		if disk := asMap(sample["disk"]); disk != nil {
			if freeBytes, ok := asFloat(disk["free_bytes"]); ok {
				nextRT["disk_free"] = freeBytes
			}
		}
		pendingLogs := c.updateLocked(map[string]any{"runtime": nextRT})
		c.mu.Unlock()
		c.flushActivityLogs(pendingLogs)
	}

	rollback := func(origErr error) error {
		currentSessions := c.vaultSessions()
		orphanEPs := make([]string, 0)
		for ep := range currentSessions {
			if !existingSessions[ep] {
				orphanEPs = append(orphanEPs, ep)
			}
		}
		sort.Strings(orphanEPs)
		shutdownBudget := c.shutdownRollbackTimeout
		if shutdownBudget <= 0 {
			shutdownBudget = 6 * time.Second
		}
		var shutdownDeadline time.Time
		if c.stop.IsSet() {
			shutdownDeadline = time.Now().Add(shutdownBudget)
		}
		for _, ep := range orphanEPs {
			c.log("正在清理残留实例："+ep, "GPU", "warning")
			if c.provider != nil {
				var relCtx context.Context
				var relCancel context.CancelFunc
				var watchDone chan struct{}
				if c.stop.IsSet() {
					if shutdownDeadline.IsZero() {
						shutdownDeadline = time.Now().Add(shutdownBudget)
					}
					rem := time.Until(shutdownDeadline)
					if rem <= 0 {
						break
					}
					relCtx, relCancel = context.WithTimeout(context.Background(), rem)
				} else {
					relCtx, relCancel = context.WithTimeout(context.Background(), 90*time.Second)
					done := make(chan struct{})
					watchDone = make(chan struct{})
					go func(cancel context.CancelFunc) {
						defer close(watchDone)
						select {
						case <-done:
							return
						case <-c.stop.Chan():
							timer := time.NewTimer(shutdownBudget)
							defer timer.Stop()
							select {
							case <-done:
							case <-timer.C:
								cancel()
							}
						}
					}(relCancel)
					_, _ = c.provider.Release(relCtx, ep, c.logFunc())
					close(done)
					<-watchDone
					relCancel()
					continue
				}
				_, _ = c.provider.Release(relCtx, ep, c.logFunc())
				relCancel()
			}
		}
		if len(orphanEPs) > 0 {
			c.mu.Lock()
			for _, ep := range orphanEPs {
				delete(c.instanceStates, ep)
				delete(c.upgradeAttempted, ep)
			}
			c.mu.Unlock()
		}
		remainingSessions := c.vaultSessions()
		remainingActive := c.activeEndpointFrom(remainingSessions)
		restoredPrevious := false
		if createNew && previousActive != "" {
			if _, ok := remainingSessions[previousActive]; ok {
				if c.provider != nil {
					_, _ = c.provider.SetActive(previousActive)
				}
				c.activateCached(previousActive)
				restoredPrevious = true
			}
		}
		if createNew && !restoredPrevious && remainingActive != "" && existingSessions[remainingActive] {
			if c.provider != nil {
				_, _ = c.provider.SetActive(remainingActive)
			}
			c.activateCached(remainingActive)
			restoredPrevious = true
		}
		if !restoredPrevious {
			if remainingActive == "" {
				c.mu.Lock()
				c.remoteWorkflowVersions = make(map[string]float64)
				pendingLogs := c.updateLocked(map[string]any{
					"runtime":       map[string]any{"state": "stopped"},
					"models":        c.Presets(),
					"workflows":     c.mergeWorkflowsLocked(nil),
					"tts_engines":   c.builtinTtsEngines(),
					"comfy_outputs":        []any{},
					"live_avatars":         []map[string]any{},
					"live_driving_motions": []map[string]any{},
					"live_catalog":         map[string]any{},
				})
				c.mu.Unlock()
				c.flushActivityLogs(pendingLogs)
			} else {
				c.Update(map[string]any{"runtime": map[string]any{"state": "disconnected", "id": remainingActive}})
			}
		}
		c.observerWake.Set()
		if c.startCancel.IsSet() {
			hasLeftover := false
			for ep := range remainingSessions {
				if !existingSessions[ep] {
					hasLeftover = true
					break
				}
			}
			if !hasLeftover {
				return &ColabOneOperationCancelled{Message: "已取消 GPU 分配", Source: "GPU"}
			}
		}
		return origErr
	}

	c.log("校验 Google 登录凭据", "登录", "info")
	if c.auth != nil {
		if _, err := c.auth.Credentials(ctx, "colab"); err != nil {
			return rollback(err)
		}
	}
	if c.startCancel.IsSet() {
		return rollback(&ColabOneOperationCancelled{Message: "已取消 GPU 分配", Source: "GPU"})
	}

	if createNew {
		c.log(fmt.Sprintf("准备新增远端 %s 实例（保留现有运行实例）", targetGPU), "GPU", "info")
		c.saveActiveToCache()
		c.mu.Lock()
		c.data["models"] = c.Presets()
		c.data["workflows"] = c.mergeWorkflowsLocked(nil)
		c.data["tts_engines"] = c.builtinTtsEngines()
		c.data["comfy_outputs"] = []any{}
		c.data["live_avatars"] = []map[string]any{}
		c.data["live_driving_motions"] = []map[string]any{}
		c.data["live_catalog"] = map[string]any{}
		c.remoteWorkflowVersions = make(map[string]float64)
		c.mu.Unlock()
	}

	rcloneConf := ""
	driveReason := ""
	if autoDrive {
		rcloneConf, driveReason = c.resolveHeadlessRcloneConf(ctx, false)
		if rcloneConf != "" {
			c.log("已准备 Google 云盘自动挂载凭据", "云盘", "info")
		} else if driveReason == "gcp_tos_required" || driveReason == "gcp_project_unavailable" {
			c.log("当前账号未配置免费 GCP 配额项目，GPU 就绪后将使用 Colab 官方原生云盘模式", "云盘", "info")
		} else {
			c.log("未检测到云盘无感凭据，本次启动跳过无感挂载", "云盘", "info")
		}
	} else {
		c.log("未开启自动挂载 Google 云盘，跳过云盘配置", "云盘", "info")
	}

	if c.startCancel.IsSet() {
		return rollback(&ColabOneOperationCancelled{Message: "已取消 GPU 分配", Source: "GPU"})
	}

	c.resetMonitorToAuto(true, false)
	c.Update(map[string]any{"runtime": map[string]any{"state": "starting"}})

	activeAssignProxy := ""
	if proxyEnabled && c.assignProxy != nil {
		if rawVal, ok := c.vault.Get("assign_proxy"); ok {
			if rawStr, ok := rawVal.(string); ok && strings.TrimSpace(rawStr) != "" {
				norm, err := c.assignProxy.NormalizeAssignProxy(rawStr)
				if err != nil {
					return rollback(err)
				}
				activeAssignProxy = norm
			}
		}
	}

	if c.provider == nil {
		return rollback(errors.New("Provider 未配置"))
	}
	remoteInfo, err := c.provider.Start(ctx, targetGPU, notify, ColabOneStartOptions{
		CreateNew:    createNew,
		RcloneConf:   rcloneConf,
		AssignProxy:  activeAssignProxy,
		MaxAttempts:  assignMaxAttempts,
		OnLog:        c.logFunc(),
		OnTelemetry:  onBootTelemetry,
		OnRetryState: onRetryState,
		CancelEvent:  c.startCancel,
	})
	if err != nil {
		return rollback(err)
	}
	if c.startCancel.IsSet() {
		return rollback(&ColabOneOperationCancelled{Message: "已取消 GPU 分配", Source: "GPU"})
	}
	c.opGen.Add(1)

	c.log("正在拉取远端实例硬件与模型快照", "GPU", "info")
	if err := c.RefreshRemote(ctx, ""); err != nil {
		if c.startCancel.IsSet() {
			return rollback(&ColabOneOperationCancelled{Message: "已取消 GPU 分配", Source: "GPU"})
		}
		c.log("首次获取远端快照未响应，1.5 秒后自动重试", "GPU", "warning")
		if c.startRetryDelay > 0 {
			timer := time.NewTimer(c.startRetryDelay)
			select {
			case <-c.startCancel.Chan():
				timer.Stop()
				return rollback(&ColabOneOperationCancelled{Message: "已取消 GPU 分配", Source: "GPU"})
			case <-c.stop.Chan():
				timer.Stop()
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
		if c.startCancel.IsSet() {
			return rollback(&ColabOneOperationCancelled{Message: "已取消 GPU 分配", Source: "GPU"})
		}
		if err2 := c.RefreshRemote(ctx, ""); err2 != nil {
			return rollback(err2)
		}
	}

	var startDoneLogs []pendingActivityLog
	c.mu.Lock()
	if c.startCancel.IsSet() {
		c.mu.Unlock()
		return rollback(&ColabOneOperationCancelled{Message: "已取消 GPU 分配", Source: "GPU"})
	}
	if prevOp := asMap(c.data["operation"]); prevOp != nil && asString(prevOp["kind"]) == "runtime_start" {
		doneOp := deepCopyMap(prevOp)
		doneOp["cancellable"] = false
		doneOp["waiting_retry"] = false
		doneOp["retry_at"] = 0.0
		startDoneLogs = c.updateLocked(map[string]any{"operation": doneOp})
	}
	c.mu.Unlock()
	c.flushActivityLogs(startDoneLogs)

	c.mu.Lock()
	liveReleaseMode, liveIdleMin, liveCULimit, _ := c.currentReleasePolicyLocked()
	if liveIdleMin < 1 {
		liveIdleMin = idleMin
	}
	c.mu.Unlock()

	if liveReleaseMode == COLABONE_RELEASE_MODE_IDLE {
		c.log(fmt.Sprintf("正在同步空闲自动释放策略（%d 分钟）", liveIdleMin), "GPU", "info")
	} else {
		c.log(fmt.Sprintf("正在同步自动释放策略（%s）", formatColabOneReleasePolicyZh(liveReleaseMode, liveIdleMin, liveCULimit)), "GPU", "info")
	}
	if err := c.ConfigureRemote(ctx, ""); err != nil {
		return rollback(err)
	}
	if liveReleaseMode == COLABONE_RELEASE_MODE_IDLE {
		c.log("远端空闲自动释放策略已生效", "GPU", "info")
	} else {
		c.log("远端自动释放策略已生效", "GPU", "info")
	}

	c.mu.Lock()
	activeStartRT := asMap(c.data["runtime"])
	rtIdleMin, _ := asInt(activeStartRT["idle_minutes"])
	rtReleaseMode := normalizeColabOneReleaseMode(activeStartRT["release_mode"])
	startSupportsReleaseModes := runtimeSupportsCapability(activeStartRT, "idle_release_modes")
	liveAutoDrive := asBool(c.settings["auto_drive"])
	c.mu.Unlock()
	expectedRTIdle := liveIdleMin
	if liveReleaseMode != COLABONE_RELEASE_MODE_IDLE {
		if startSupportsReleaseModes {
			expectedRTIdle = 0
		} else {
			expectedRTIdle = 240
		}
	}
	if rcloneConf == "" && (rtIdleMin != expectedRTIdle || (startSupportsReleaseModes && rtReleaseMode != liveReleaseMode)) {
		if err := c.RefreshRemote(ctx, ""); err != nil {
			return rollback(err)
		}
	}

	// Start has passed the point of no return (no rollback beyond this point);
	// anchor the continuous run start timestamp and verified pre-start balance now.
	if c.cuUsage != nil {
		startedAt, hasStarted := asFloat(remoteInfo["started_at"])
		if !hasStarted || startedAt <= 0 {
			startedAt = nowUnixFloat()
		}
		startBal, hasStartBalNum := asFloat(remoteInfo["start_balance"])
		hasStartBal := asBool(remoteInfo["has_start_balance"]) && hasStartBalNum
		email := ""
		c.mu.Lock()
		if acctMap := asMap(c.data["account"]); acctMap != nil {
			email = asTrimmedString(acctMap["email"])
		}
		c.mu.Unlock()
		if email == "" && c.vault != nil {
			if profVal, ok := c.vault.Get("google_profile"); ok {
				email = asTrimmedString(asMap(profVal)["email"])
			}
		}
		c.cuUsage.BeginRun(email, startedAt, startBal, hasStartBal)
	}

	if rcloneConf != "" {
		c.Update(map[string]any{
			"operation": map[string]any{
				"title":   "正在自动挂载 Google 云盘 (ColabOne_Drive)",
				"running": true,
			},
		})
		rawInfo, driveErr := c.remoteLong(ctx, "POST", "drive/connect", nil, 180*time.Second)
		if driveErr != nil {
			if isQuotaProjectError(driveErr) && c.auth != nil {
				c.auth.InvalidateDriveQuotaProject()
			}
			c.log("云盘自动挂载未就绪，实例仍可正常使用："+SafeMessage(driveErr.Error()), "云盘", "warning")
		} else {
			info := asMap(rawInfo)
			if asString(info["mode"]) == "mount" {
				c.log("已自动挂载 Google 云盘 (/content/drive/MyDrive/ColabOne_Drive)", "云盘", "info")
			} else {
				c.log("已自动连接 Google 云盘持久化目录 (ColabOne_Drive)", "云盘", "info")
			}
		}
		if err := c.RefreshRemote(ctx, ""); err == nil {
			c.syncRemoteAPIIDOverrides(ctx, "", false)
			c.log("已刷新云盘工作区与远端状态", "云盘", "info")
		}
	} else if liveAutoDrive && (driveReason == "gcp_tos_required" || driveReason == "gcp_project_unavailable") {
		info, driveErr := c.connectColabDriveFS(ctx, true)
		if driveErr != nil {
			c.log("云盘自动挂载未就绪，实例仍可正常使用："+SafeMessage(driveErr.Error()), "云盘", "warning")
		} else if info != nil && asBool(info["connected"]) {
			_ = c.RefreshRemote(ctx, "")
			c.syncRemoteAPIIDOverrides(ctx, "", false)
			c.log("已通过 Google Colab 原生 DriveFS 自动挂载云盘 (/content/drive/MyDrive/ColabOne_Drive_Mount)", "云盘", "info")
		} else {
			c.log("点击「立即挂载云盘」即可在浏览器一键确认 Colab 原生云盘挂载 (ColabOne_Drive_Mount)", "云盘", "info")
		}
	}

	c.log("正在刷新账号计费与活跃实例列表", "计费", "info")
	acctGen, acctCreds := c.snapshotAccountGuard()
	if acct, err := c.provider.Account(ctx); err == nil && acct != nil {
		if c.applyAccountRefreshIfCurrent(acctGen, acctCreds, acct, nowUnixFloat()) && asBool(acct["logged_in"]) {
			balance, _ := asFloat(acct["balance"])
			rate, _ := asFloat(acct["rate"])
			assignments, _ := asInt(acct["assignments"])
			c.log(
				fmt.Sprintf("计费同步完成 · 余额 %.2f CU · 费率 %.2f CU/小时 · 活跃 %d 台", balance, rate, assignments),
				"计费",
				"info",
			)
		}
	}
	if liveReleaseMode == COLABONE_RELEASE_MODE_CU_LIMIT {
		// Force next monitor tick to push the freshly computed remote failsafe deadline
		// to both active and secondary instances now that BeginRun and post-start Account
		// rate refresh have completed.
		c.mu.Lock()
		c.lastConfig = 0
		c.lastCUAutoReleaseAttempt = 0
		for _, st := range c.instanceStates {
			st.LastConfig = 0
		}
		c.mu.Unlock()
	}
	return nil
}

// Release tears down a single managed runtime instance (gracefully unless force is true).
func (c *ColabOneController) Release(ctx context.Context, force bool, endpoint string) error {
	return c.releaseEndpoint(ctx, force, false, endpoint)
}

func (c *ColabOneController) releaseEndpoint(ctx context.Context, force bool, skipForceLog bool, endpoint string) error {
	activeEP := c.activeEndpoint()
	target := endpoint
	if target == "" {
		target = activeEP
	}
	useExplicit := endpoint != "" && endpoint != activeEP

	if !force {
		c.log("正在通知远端停止任务、卸载模型并同步云盘", "GPU", "info")
		rpcEP := ""
		if useExplicit {
			rpcEP = target
		}
		if _, err := c.remote(ctx, "POST", "prepare-release", nil, rpcEP, 0); err != nil {
			return err
		}
		c.log("远端收尾与云盘同步已完成", "GPU", "info")
	} else if !skipForceLog {
		c.log("已选择强制释放，跳过远端收尾检查", "GPU", "warning")
	}

	var prevRT map[string]any
	if !useExplicit {
		c.mu.Lock()
		prevRT = deepCopyMap(asMap(c.data["runtime"]))
		c.mu.Unlock()
		c.Update(map[string]any{"runtime": map[string]any{"state": "releasing"}})
	}

	if c.provider != nil {
		relEP := ""
		if target != "" && (useExplicit || len(c.vaultSessions()) > 1) {
			relEP = target
		}
		if _, err := c.provider.Release(ctx, relEP, c.logFunc()); err != nil {
			if !useExplicit {
				remainingSessions := c.vaultSessions()
				if len(remainingSessions) == 0 {
					c.mu.Lock()
					c.remoteWorkflowVersions = make(map[string]float64)
					pendingLogs := c.updateWithSessionsLocked(map[string]any{
						"runtime":              map[string]any{"state": "stopped"},
						"models":               c.Presets(),
						"workflows":            c.mergeWorkflowsLocked(nil),
						"tts_engines":          c.builtinTtsEngines(),
						"comfy_outputs":        []any{},
						"live_avatars":         []map[string]any{},
						"live_driving_motions": []map[string]any{},
						"live_catalog":         map[string]any{},
					}, remainingSessions, "")
					c.mu.Unlock()
					c.flushActivityLogs(pendingLogs)
				} else {
					fallbackEP := c.activeEndpointFrom(remainingSessions)
					if fallbackEP == "" {
						fallbackEP = asString(prevRT["id"])
					}
					nextRT := map[string]any{"state": "disconnected"}
					if fallbackEP != "" {
						nextRT["id"] = fallbackEP
					}
					c.Update(map[string]any{"runtime": nextRT})
				}
			}
			return err
		}
	}
	c.opGen.Add(1)
	if target != "" {
		c.mu.Lock()
		delete(c.instanceStates, target)
		delete(c.upgradeAttempted, target)
		c.mu.Unlock()
	}

	nextActive := c.activeEndpoint()
	if nextActive != "" && nextActive != target {
		c.activateCached(nextActive)
		c.log(fmt.Sprintf("已释放 %s，当前切换至 %s", target, nextActive), "GPU", "info")
	} else {
		c.mu.Lock()
		c.remoteWorkflowVersions = make(map[string]float64)
		pendingLogs := c.updateLocked(map[string]any{
			"runtime":       map[string]any{"state": "stopped"},
			"models":        c.Presets(),
			"workflows":     c.mergeWorkflowsLocked(nil),
			"tts_engines":   c.builtinTtsEngines(),
			"comfy_outputs":        []any{},
			"live_avatars":         []map[string]any{},
			"live_driving_motions": []map[string]any{},
			"live_catalog":         map[string]any{},
		})
		c.mu.Unlock()
		c.flushActivityLogs(pendingLogs)
		c.triggerBackgroundDriveOutputsSync()
	}

	if c.provider != nil {
		c.log("正在刷新账号计费与实例清单", "计费", "info")
		acctGen, acctCreds := c.snapshotAccountGuard()
		if acct, err := c.provider.Account(ctx); err == nil && acct != nil {
			if _, hasLoggedIn := acct["logged_in"]; hasLoggedIn {
				if c.applyAccountRefreshIfCurrent(acctGen, acctCreds, acct, nowUnixFloat()) {
					c.observerWake.Set()
					assignments, _ := asInt(acct["assignments"])
					rate, _ := asFloat(acct["rate"])
					c.log(
						fmt.Sprintf("已刷新账号状态 · 活跃实例 %d 台 · 费率 %.2f CU/小时", assignments, rate),
						"计费",
						"info",
					)
				}
			}
			c.endCUUsageRunIfNoExternal(acct, target)
		} else {
			c.endCUUsageRunIfNoExternal(nil, target)
		}
	} else {
		c.endCUUsageRunIfNoExternal(nil, target)
	}
	return nil
}

// PreviewReleaseAll snapshots the account's active instances and issues a confirmation plan ID.
func (c *ColabOneController) PreviewReleaseAll(ctx context.Context) (map[string]any, error) {
	if c.provider == nil {
		return nil, errors.New("Provider 未配置")
	}
	account, err := c.provider.Account(ctx)
	if err != nil {
		return nil, err
	}
	c.Update(map[string]any{"account": account})
	enriched := c.enrichInstances(account)
	sub := ""
	if c.auth != nil {
		prof, err := c.auth.Profile(ctx)
		if err != nil {
			return nil, err
		}
		sub = asString(prof["sub"])
	}
	plan := &releasePlanState{
		ID:         generateTokenHex(16),
		Instances:  deepCopyMapSlice(enriched),
		AccountSub: sub,
	}
	c.mu.Lock()
	c.releasePlan = plan
	c.mu.Unlock()
	return map[string]any{"id": plan.ID, "instances": enriched}, nil
}

// ReleaseAll releases either all previewed instances or a selected subset of them.
func (c *ColabOneController) ReleaseAll(ctx context.Context, planID string, force bool, endpoints []string, hasEndpoints bool) error {
	c.mu.Lock()
	plan := c.releasePlan
	c.mu.Unlock()

	currentSub := ""
	if c.auth != nil {
		prof, err := c.auth.Profile(ctx)
		if err != nil {
			return err
		}
		currentSub = asString(prof["sub"])
	}
	if plan == nil || plan.ID != planID || plan.AccountSub != currentSub {
		return errors.New("实例清单已变化，请重新点击停止")
	}

	reviewed := make([]string, 0, len(plan.Instances))
	reviewedSet := make(map[string]bool, len(plan.Instances))
	for _, item := range plan.Instances {
		id := asString(item["id"])
		reviewed = append(reviewed, id)
		reviewedSet[id] = true
	}

	var targets []string
	var partialSelection bool
	if hasEndpoints {
		if len(endpoints) == 0 {
			return errors.New("请至少勾选一个要停止的实例")
		}
		requested := make(map[string]bool, len(endpoints))
		for _, ep := range endpoints {
			if !reviewedSet[ep] {
				return errors.New("所选实例不在已确认清单中，请重新打开停止窗口")
			}
			requested[ep] = true
		}
		for _, ep := range reviewed {
			if requested[ep] {
				targets = append(targets, ep)
			}
		}
		partialSelection = len(targets) != len(reviewed)
	} else {
		targets = reviewed
		partialSelection = false
	}

	sessions := c.vaultSessions()
	activeEP := c.activeEndpointFrom(sessions)
	c.log(fmt.Sprintf("开始按确认清单停止 %d 个远端实例", len(targets)), "GPU", "info")
	if !force {
		for _, ep := range targets {
			if _, owned := sessions[ep]; owned {
				c.log(fmt.Sprintf("正在停止远端任务并同步云盘（%s）", ep), "GPU", "info")
				rpcEP := ep
				if ep == activeEP || len(sessions) <= 1 {
					rpcEP = ""
				}
				if _, err := c.remote(ctx, "POST", "prepare-release", nil, rpcEP, 0); err != nil {
					return err
				}
				c.log(fmt.Sprintf("远端收尾完成（%s）", ep), "GPU", "info")
			}
		}
	} else {
		c.log("已选择强制释放，跳过远端收尾检查", "GPU", "warning")
	}

	var releaseErr error
	func() {
		defer func() {
			c.opGen.Add(1)
			remainingSessions := c.vaultSessions()
			c.mu.Lock()
			c.lastAuth = 0
			for _, ep := range targets {
				if _, still := remainingSessions[ep]; !still {
					delete(c.instanceStates, ep)
					delete(c.upgradeAttempted, ep)
				}
			}
			c.mu.Unlock()

			targetSet := make(map[string]bool, len(targets))
			for _, ep := range targets {
				targetSet[ep] = true
			}
			c.monitorMu.Lock()
			for _, ep := range targets {
				delete(c.externalObservations, ep)
			}
			if targetSet[c.monitorEndpoint] {
				c.monitorMode = "auto"
				c.monitorEndpoint = ""
				c.monitorPaused = false
				c.monitorGen++
			}
			c.monitorMu.Unlock()

			for _, ep := range targets {
				c.closeExternalObserver(ep, false)
			}

			nextActive := c.activeEndpointFrom(remainingSessions)
			if len(remainingSessions) == 0 || nextActive == "" {
				c.mu.Lock()
				c.remoteWorkflowVersions = make(map[string]float64)
				pendingLogs := c.updateLocked(map[string]any{
					"runtime":       map[string]any{"state": "stopped"},
					"models":        c.Presets(),
					"workflows":     c.mergeWorkflowsLocked(nil),
					"tts_engines":   c.builtinTtsEngines(),
					"comfy_outputs":        []any{},
					"live_avatars":         []map[string]any{},
					"live_driving_motions": []map[string]any{},
					"live_catalog":         map[string]any{},
				})
				c.mu.Unlock()
				c.flushActivityLogs(pendingLogs)
				c.triggerBackgroundDriveOutputsSync()
			} else {
				c.activateCached(nextActive)
			}

			if c.provider != nil {
				acctGen, acctCreds := c.snapshotAccountGuard()
				if acct, err := c.provider.Account(ctx); err == nil && acct != nil {
					c.applyAccountRefreshIfCurrent(acctGen, acctCreds, acct, 0)
					c.endCUUsageRunIfNoExternal(acct, targets...)
				} else {
					c.endCUUsageRunIfNoExternal(nil, targets...)
				}
			} else {
				c.endCUUsageRunIfNoExternal(nil, targets...)
			}
			c.observerWake.Set()
		}()

		if c.provider != nil {
			_, releaseErr = c.provider.ReleaseAll(ctx, targets, func(msg string) {
				c.log(msg, "GPU", "info")
			})
		}
	}()
	if releaseErr != nil {
		return releaseErr
	}

	c.mu.Lock()
	c.releasePlan = nil
	acct := deepCopyMap(asMap(c.data["account"]))
	c.mu.Unlock()

	assignments, _ := asInt(acct["assignments"])
	if !partialSelection && assignments > 0 {
		return fmt.Errorf("已处理确认的实例；当前账号仍有 %d 个实例，请再次停止全部", assignments)
	}
	if partialSelection {
		c.log(fmt.Sprintf("已停止所选的 %d 个实例；当前账号剩余 %d 个运行实例", len(targets), assignments), "计费", "info")
	} else {
		rate, _ := asFloat(acct["rate"])
		suffix := "，等待 Google 更新计费费率"
		if rate == 0 {
			suffix = "，费率为 0 CU/小时"
		}
		c.log("已确认 0 个运行实例"+suffix, "计费", "info")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Model library, API IDs, common repos, and workflow sync helpers
// ---------------------------------------------------------------------------

func (c *ColabOneController) postRemoteModel(ctx context.Context, spec map[string]any, endpoint string) (any, error) {
	payload := deepCopyMap(spec)
	if !c.runtimeSupportsAPIAliases(endpoint) {
		delete(payload, "api_id")
	}
	res, err := c.remote(ctx, "POST", "models", payload, endpoint, 0)
	if err == nil {
		return res, nil
	}
	msg := strings.ToLower(err.Error())
	status := httpStatusCode(err)
	isSchemaReject := status == 400 || status == 422 ||
		strings.Contains(msg, "api_id") ||
		strings.Contains(msg, "extra_forbidden") ||
		strings.Contains(msg, "extra inputs are not permitted") ||
		strings.Contains(msg, "http 400") ||
		strings.Contains(msg, "http 422")
	if _, hasAPIID := payload["api_id"]; hasAPIID && isSchemaReject {
		legacyPayload := make(map[string]any, len(payload))
		for k, v := range payload {
			if k != "api_id" {
				legacyPayload[k] = v
			}
		}
		return c.remote(ctx, "POST", "models", legacyPayload, endpoint, 0)
	}
	return nil, err
}

func (c *ColabOneController) activeModelAPIIDs(excludeID string) []string {
	c.mu.Lock()
	rt := deepCopyMap(asMap(c.data["runtime"]))
	baseModels := deepCopyMapSlice(asMapSlice(c.data["models"]))
	c.mu.Unlock()

	decorated := c.decorateModels(baseModels, rt)
	seen := make(map[string]bool)
	taken := make([]string, 0)
	for _, item := range decorated {
		mid := asString(item["id"])
		if mid == "" || mid == excludeID {
			continue
		}
		if !(asBool(item["custom"]) || asBool(item["common"]) || asBool(item["downloaded"]) || asBool(item["loaded"])) {
			continue
		}
		apiID := asTrimmedString(item["pending_api_id"])
		if apiID == "" {
			apiID = asTrimmedString(item["api_id"])
		}
		if apiID == "" {
			apiID = strings.TrimSpace(c.resolveAPIModelID(item))
		}
		if apiID != "" && !seen[apiID] {
			seen[apiID] = true
			taken = append(taken, apiID)
		}
	}
	return taken
}

func (c *ColabOneController) ensureRemoteModel(ctx context.Context, modelID, endpoint string) error {
	activeEP := c.activeEndpoint()
	c.mu.Lock()
	spec := deepCopyMap(c.customModels[modelID])
	override := c.apiIDOverrides[modelID]
	var models []map[string]any
	if endpoint == "" || endpoint == activeEP {
		models = asMapSlice(c.data["models"])
	} else if st := c.instanceStates[endpoint]; st != nil {
		models = st.Models
	}
	onRemote := false
	for _, m := range models {
		if asString(m["id"]) == modelID {
			onRemote = true
			break
		}
	}
	c.mu.Unlock()

	if spec != nil && !onRemote {
		if override != "" {
			spec["api_id"] = override
		}
		_, err := c.postRemoteModel(ctx, spec, endpoint)
		return err
	}
	return nil
}

func (c *ColabOneController) ensureRemoteWorkflow(ctx context.Context, workflowID string) error {
	c.mu.Lock()
	localWF := deepCopyMap(c.customWorkflows[workflowID])
	remoteVer, hasRemoteVer := c.remoteWorkflowVersions[workflowID]
	c.mu.Unlock()
	if localWF == nil {
		return nil
	}
	localVer, _ := asFloat(localWF["updated_at"])
	if hasRemoteVer && remoteVer >= localVer {
		return nil
	}
	reqPayload := map[string]any{
		"id":          asString(localWF["id"]),
		"name":        asString(localWF["name"]),
		"description": asString(localWF["description"]),
		"workflow":    localWF["workflow"],
	}
	if c.comfy != nil {
		validated, err := c.comfy.ValidateWorkflowImport(reqPayload)
		if err != nil {
			return err
		}
		reqPayload = validated
	}
	rawSynced, err := c.remote(ctx, "POST", "comfyui/workflows", reqPayload, "", 0)
	if err != nil {
		return err
	}
	synced := asMap(rawSynced)
	c.mu.Lock()
	if synced != nil && asString(synced["id"]) != "" {
		ver, ok := asFloat(synced["updated_at"])
		if !ok || ver == 0 {
			ver = localVer
		}
		c.remoteWorkflowVersions[asString(synced["id"])] = ver
	} else {
		c.remoteWorkflowVersions[workflowID] = localVer
	}
	c.mu.Unlock()
	return nil
}

// ImportModel resolves a Hugging Face GGUF model manifest, deduplicates its API ID,
// persists it locally in library.json, and syncs it to the remote runtime when ready.
func (c *ColabOneController) ImportModel(ctx context.Context, payload map[string]any) (map[string]any, error) {
	if c.models == nil {
		return nil, errors.New("ModelResolver 未配置")
	}
	repoInput := asTrimmedString(payload["repo"])
	c.log("正在读取 Hugging Face 模型清单："+repoInput, "模型", "info")
	filename := asTrimmedString(payload["filename"])
	revision := asTrimmedString(payload["revision"])
	if revision == "" {
		revision = "main"
	}
	hfTok := ""
	if v, ok := c.vault.Get("hf"); ok {
		if s, ok := v.(string); ok {
			hfTok = s
		}
	}
	customName := asString(payload["name"])
	spec, err := c.models.ResolveImport(ctx, repoInput, filename, revision, hfTok, customName)
	if err != nil {
		return nil, err
	}
	spec = deepCopyMap(spec)
	specID := asString(spec["id"])

	c.mu.Lock()
	existingOverride := c.apiIDOverrides[specID]
	existingCustom := c.customModels[specID]
	existingAPIID := strings.TrimSpace(existingOverride)
	if existingAPIID == "" && existingCustom != nil {
		existingAPIID = asTrimmedString(existingCustom["api_id"])
	}
	c.mu.Unlock()

	if existingAPIID != "" {
		spec["api_id"] = existingAPIID
	} else {
		existingIDs := c.activeModelAPIIDs(specID)
		baseAPIID := asTrimmedString(spec["api_id"])
		if baseAPIID == "" {
			baseAPIID = c.resolveAPIModelID(spec)
		}
		files := asMapSlice(spec["files"])
		firstRepo, firstPath := "", ""
		if len(files) > 0 && files[0] != nil {
			firstRepo = asString(files[0]["repo"])
			firstPath = asString(files[0]["path"])
		}
		uniqueID, err := c.uniqueAPIModelID(baseAPIID, existingIDs, firstRepo, firstPath)
		if err != nil {
			return nil, err
		}
		spec["api_id"] = uniqueID
	}

	files := asMapSlice(spec["files"])
	totalGiB := float64(sumFileSizes(files)) / float64(1024*1024*1024)
	specName := asString(spec["name"])
	c.log(fmt.Sprintf("已解析模型清单：%s（%d 个文件，%.2f GiB）", specName, len(files), totalGiB), "模型", "info")

	c.mu.Lock()
	c.customModels[specID] = deepCopyMap(spec)
	if asBoolDefault(payload, "common", true) {
		c.commonIDs[specID] = true
	}
	rtState := asString(asMap(c.data["runtime"])["state"])
	c.mu.Unlock()

	if err := c.saveLibrary(); err != nil {
		return nil, err
	}
	c.log("已保存到本地模型库："+specName, "模型", "info")

	if len(c.vaultSessions()) > 0 && rtState == "ready" {
		if _, err := c.postRemoteModel(ctx, spec, ""); err != nil {
			return nil, err
		}
		if err := c.RefreshRemote(ctx, ""); err != nil {
			return nil, err
		}
		c.log("已同步模型配置到远端实例："+specName, "模型", "info")
	} else if len(c.vaultSessions()) == 0 {
		c.Update(map[string]any{"models": c.Presets()})
	}
	return spec, nil
}

// SetModelAPIID sets or resets the OpenAI-compatible public API alias for a model.
func (c *ColabOneController) SetModelAPIID(ctx context.Context, modelID, apiID string) (map[string]any, error) {
	mid, err := ValidateModelID(map[string]any{"id": modelID})
	if err != nil {
		return nil, err
	}
	cleanAPIID, err := c.validateAPIModelID(apiID, true)
	if err != nil {
		return nil, err
	}
	presetsList := c.Presets()
	presetMap := make(map[string]map[string]any, len(presetsList))
	for _, m := range presetsList {
		presetMap[asString(m["id"])] = m
	}

	c.mu.Lock()
	rt := deepCopyMap(asMap(c.data["runtime"]))
	var targetModel map[string]any
	for _, m := range asMapSlice(c.data["models"]) {
		if asString(m["id"]) == mid {
			targetModel = deepCopyMap(m)
			break
		}
	}
	if targetModel == nil {
		if cm := c.customModels[mid]; cm != nil {
			targetModel = deepCopyMap(cm)
		}
	}
	if targetModel == nil {
		if pm := presetMap[mid]; pm != nil {
			targetModel = deepCopyMap(pm)
		}
	}
	c.mu.Unlock()

	if targetModel == nil {
		return nil, errors.New("找不到指定模型")
	}

	var effectiveAPIID, resolved string
	if cleanAPIID != "" {
		taken := make(map[string]bool)
		for _, id := range c.activeModelAPIIDs(mid) {
			taken[strings.ToLower(id)] = true
		}
		if taken[strings.ToLower(cleanAPIID)] {
			return nil, errors.New("该模型 ID 已被其他模型使用，请换一个名称")
		}
		effectiveAPIID = cleanAPIID
		resolved = cleanAPIID
	} else {
		presetDefault := asTrimmedString(presetMap[mid]["api_id"])
		baseDefault := presetDefault
		if baseDefault == "" {
			tmp := deepCopyMap(targetModel)
			tmp["api_id"] = ""
			baseDefault = c.resolveAPIModelID(tmp)
		}
		takenIDs := c.activeModelAPIIDs(mid)
		files := asMapSlice(targetModel["files"])
		firstRepo, firstPath := asString(targetModel["repo"]), ""
		if len(files) > 0 && files[0] != nil {
			if r := asString(files[0]["repo"]); r != "" {
				firstRepo = r
			}
			firstPath = asString(files[0]["path"])
		}
		uniqueDefault, err := c.uniqueAPIModelID(baseDefault, takenIDs, firstRepo, firstPath)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(uniqueDefault, baseDefault) {
			effectiveAPIID = uniqueDefault
		} else {
			effectiveAPIID = ""
		}
		resolved = uniqueDefault
	}

	remoteReady := asString(rt["state"]) == "ready"
	if remoteReady {
		if !asBool(rt["api_model_aliases"]) && asBool(rt["upgrade_available"]) {
			return nil, errors.New("请先更新远端服务后再自定义模型 ID")
		}
		if err := c.ensureRemoteModel(ctx, mid, ""); err != nil {
			return nil, err
		}
		if _, err := c.remote(ctx, "POST", fmt.Sprintf("models/%s/api-id", mid), map[string]any{"api_id": effectiveAPIID}, "", 0); err != nil {
			return nil, err
		}
	}

	c.mu.Lock()
	_, isCustom := c.customModels[mid]
	if effectiveAPIID != "" {
		if cleanAPIID != "" || !isCustom {
			c.apiIDOverrides[mid] = effectiveAPIID
		} else {
			delete(c.apiIDOverrides, mid)
		}
	} else {
		delete(c.apiIDOverrides, mid)
	}
	if isCustom {
		c.customModels[mid]["api_id"] = effectiveAPIID
	}
	c.mu.Unlock()

	if err := c.saveLibrary(); err != nil {
		return nil, err
	}
	if remoteReady {
		if err := c.RefreshRemote(ctx, ""); err != nil {
			return nil, err
		}
	} else if len(c.vaultSessions()) == 0 {
		c.Update(map[string]any{"models": c.Presets()})
	}
	c.log("已更新模型 API ID："+resolved, "模型", "info")
	return map[string]any{"ok": true, "id": mid, "api_id": resolved}, nil
}

// SetCommonModel toggles or explicitly sets a model's membership in "My Models".
func (c *ColabOneController) SetCommonModel(modelID string, common *bool) (map[string]any, error) {
	if !reModelID.MatchString(modelID) {
		return nil, errors.New("模型 ID 不合法")
	}
	presetsList := c.Presets()
	presetIDs := make(map[string]bool, len(presetsList))
	presetByID := make(map[string]map[string]any, len(presetsList))
	for _, m := range presetsList {
		id := asString(m["id"])
		presetIDs[id] = true
		presetByID[id] = m
	}

	c.mu.Lock()
	var existing map[string]any
	for _, m := range asMapSlice(c.data["models"]) {
		if asString(m["id"]) == modelID {
			existing = deepCopyMap(m)
			break
		}
	}
	if existing == nil {
		if cm := c.customModels[modelID]; cm != nil {
			existing = deepCopyMap(cm)
		}
	}
	if existing == nil {
		existing = presetByID[modelID]
	}
	files := asMapSlice(existing["files"])
	currentlyCommon := c.commonIDs[modelID]
	target := !currentlyCommon
	if common != nil {
		target = *common
	}
	if target {
		c.commonIDs[modelID] = true
		if existing != nil && len(files) > 0 && c.customModels[modelID] == nil && !presetIDs[modelID] {
			clean := make(map[string]any)
			for _, field := range colabOneModelSpecFields {
				if val, ok := existing[field]; ok {
					clean[field] = val
				}
			}
			if validated, err := c.validateModelSpec(clean); err == nil && validated != nil {
				c.customModels[modelID] = validated
			}
		}
	} else {
		delete(c.commonIDs, modelID)
	}
	c.mu.Unlock()

	if err := c.saveLibrary(); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "common": target}, nil
}

// SaveCommonRepo saves, updates, or toggles a community Hugging Face repository bookmark.
func (c *ColabOneController) SaveCommonRepo(payload map[string]any) (map[string]any, error) {
	repo, filename, _ := c.parseHFInput(asString(payload["repo"]), asString(payload["filename"]), "main")
	if !reRepoSlug.MatchString(repo) {
		return nil, errors.New("请输入 owner/repository 或 Hugging Face 仓库链接")
	}
	name := truncateRunes(asTrimmedString(payload["name"]), 120)
	if name == "" {
		if idx := strings.LastIndex(repo, "/"); idx >= 0 {
			name = repo[idx+1:]
		} else {
			name = repo
		}
	}
	note := truncateRunes(asTrimmedString(payload["note"]), 200)
	downloads, _ := asInt(payload["downloads"])
	likes, _ := asInt(payload["likes"])
	entry := map[string]any{
		"repo":      repo,
		"name":      name,
		"note":      note,
		"filename":  truncateRunes(filename, 200),
		"downloads": downloads,
		"likes":     likes,
		"added_at":  nowUnixFloat(),
	}

	c.mu.Lock()
	existingIdx := -1
	for i, b := range c.commonRepos {
		if strings.EqualFold(asString(b["repo"]), repo) {
			existingIdx = i
			break
		}
	}
	var saved bool
	if asBool(payload["toggle"]) && existingIdx >= 0 {
		c.commonRepos = append(c.commonRepos[:existingIdx], c.commonRepos[existingIdx+1:]...)
		saved = false
	} else if existingIdx >= 0 {
		if prevAdded, ok := c.commonRepos[existingIdx]["added_at"]; ok {
			entry["added_at"] = prevAdded
		}
		c.commonRepos[existingIdx] = entry
		saved = true
	} else {
		c.commonRepos = append([]map[string]any{entry}, c.commonRepos...)
		saved = true
	}
	c.mu.Unlock()

	if err := c.saveLibrary(); err != nil {
		return nil, err
	}
	if saved {
		c.log("已将社区仓库加入我的模型："+repo, "模型", "info")
	} else {
		c.log("已从我的模型移除仓库："+repo, "模型", "info")
	}
	return map[string]any{"ok": true, "common": saved, "repo": repo}, nil
}

// RemoveCommonRepo removes a saved repository bookmark case-insensitively.
func (c *ColabOneController) RemoveCommonRepo(repo string) (map[string]any, error) {
	trimmed := strings.TrimSpace(repo)
	target := strings.ToLower(trimmed)
	c.mu.Lock()
	filtered := make([]map[string]any, 0, len(c.commonRepos))
	for _, b := range c.commonRepos {
		if strings.ToLower(asString(b["repo"])) != target {
			filtered = append(filtered, b)
		}
	}
	c.commonRepos = filtered
	c.mu.Unlock()

	if err := c.saveLibrary(); err != nil {
		return nil, err
	}
	if target != "" {
		c.log("已从我的模型移除仓库："+trimmed, "模型", "info")
	}
	return map[string]any{"ok": true}, nil
}

// RemoveLocalModel deletes an offline custom model record if it is neither downloaded nor loaded remotely.
func (c *ColabOneController) RemoveLocalModel(modelID string) (map[string]any, error) {
	mid, err := ValidateModelID(map[string]any{"id": modelID})
	if err != nil {
		return nil, err
	}
	sessions := c.vaultSessions()
	activeEP := c.activeEndpointFrom(sessions)
	c.mu.Lock()
	models := asMapSlice(c.data["models"])
	for _, m := range models {
		if asString(m["id"]) == mid {
			if asBool(m["downloaded"]) || asBool(m["loaded"]) {
				c.mu.Unlock()
				return nil, errors.New("该模型已在远端下载或装载，请先删除远端文件后再移除本地记录")
			}
			break
		}
	}
	delete(c.customModels, mid)
	delete(c.apiIDOverrides, mid)
	delete(c.commonIDs, mid)
	filtered := make([]map[string]any, 0, len(models))
	for _, m := range models {
		if asString(m["id"]) != mid {
			filtered = append(filtered, m)
		}
	}
	c.data["models"] = filtered
	for _, st := range c.instanceStates {
		if st == nil || len(st.Models) == 0 {
			continue
		}
		kept := make([]map[string]any, 0, len(st.Models))
		for _, m := range st.Models {
			if asString(m["id"]) != mid {
				kept = append(kept, m)
			}
		}
		st.Models = kept
	}
	c.saveActiveToCacheWithSessionsLocked(sessions, activeEP)
	c.mu.Unlock()

	if err := c.saveLibrary(); err != nil {
		return nil, err
	}
	c.log("已从本地列表移除模型："+mid, "模型", "info")
	return map[string]any{"ok": true}, nil
}

// ---------------------------------------------------------------------------
// Settings saving & activity heartbeat
// ---------------------------------------------------------------------------

// SaveSettings validates and persists settings.json while isolating hf_token and assign_proxy to VaultStore.
func (c *ColabOneController) SaveSettings(ctx context.Context, payload map[string]any) (map[string]any, error) {
	c.mu.Lock()
	curGPU := asString(c.settings["gpu"])
	curReleaseMode, curIdle, curCULimit, _ := c.currentReleasePolicyLocked()
	curThreads, _ := asInt(c.settings["download_threads"])
	curAssignMaxAttempts, ok := asExactInt(c.settings["assign_max_attempts"])
	if !ok || curAssignMaxAttempts < COLABONE_MIN_ASSIGN_MAX_ATTEMPTS || curAssignMaxAttempts > COLABONE_MAX_ASSIGN_MAX_ATTEMPTS {
		curAssignMaxAttempts = COLABONE_DEFAULT_ASSIGN_MAX_ATTEMPTS
	}
	c.mu.Unlock()

	public := make(map[string]any)
	for _, k := range []string{"gpu", "idle_minutes", "release_mode", "release_cu_limit", "auto_drive", "download_threads", "assign_max_attempts", "assign_proxy_enabled"} {
		if v, exists := payload[k]; exists {
			public[k] = v
		}
	}

	gpuVal := curGPU
	if v, exists := public["gpu"]; exists {
		s, ok := v.(string)
		if !ok {
			return nil, errors.New("GPU 型号不合法")
		}
		gpuVal = s
	}
	if !validColabOneGPUs[gpuVal] {
		return nil, errors.New("GPU 型号不合法")
	}

	rawIdle := any(curIdle)
	_, hasIdleInPayload := public["idle_minutes"]
	if v, exists := public["idle_minutes"]; exists {
		rawIdle = v
	}
	idleVal, ok := asInt(rawIdle)
	if !ok || idleVal < 1 || idleVal > 240 {
		return nil, errors.New("空闲时间必须在 1 至 240 分钟之间")
	}
	if hasIdleInPayload {
		public["idle_minutes"] = idleVal
	}

	releaseModeVal := curReleaseMode
	if v, exists := public["release_mode"]; exists {
		s, ok := v.(string)
		if !ok || !validColabOneReleaseModes[strings.TrimSpace(s)] {
			return nil, errors.New("自动释放策略不合法")
		}
		releaseModeVal = strings.TrimSpace(s)
		public["release_mode"] = releaseModeVal
	} else if hasIdleInPayload && curReleaseMode != COLABONE_RELEASE_MODE_IDLE {
		// Backward-compatible callers sending only {idle_minutes} switch back to idle mode.
		releaseModeVal = COLABONE_RELEASE_MODE_IDLE
		public["release_mode"] = releaseModeVal
	}

	cuLimitVal := curCULimit
	if v, exists := public["release_cu_limit"]; exists {
		f, ok := asFloat(v)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f < COLABONE_MIN_RELEASE_CU_LIMIT || f > COLABONE_MAX_RELEASE_CU_LIMIT {
			return nil, errors.New("CU 消耗上限必须在 0.1 至 100000 之间")
		}
		cuLimitVal = roundColabOneCULimit(f)
		public["release_cu_limit"] = cuLimitVal
	}

	rawThreads := any(curThreads)
	if v, exists := public["download_threads"]; exists {
		rawThreads = v
	}
	threadsVal, ok := asExactInt(rawThreads)
	if !ok || (threadsVal != 16 && threadsVal != 32) {
		return nil, errors.New("下载线程数请选择 16 或 32")
	}
	if _, exists := public["download_threads"]; exists {
		public["download_threads"] = threadsVal
	}

	rawAssignMaxAttempts := any(curAssignMaxAttempts)
	if v, exists := public["assign_max_attempts"]; exists {
		rawAssignMaxAttempts = v
	}
	assignMaxAttemptsVal, ok := asExactInt(rawAssignMaxAttempts)
	if !ok || assignMaxAttemptsVal < COLABONE_MIN_ASSIGN_MAX_ATTEMPTS || assignMaxAttemptsVal > COLABONE_MAX_ASSIGN_MAX_ATTEMPTS {
		return nil, fmt.Errorf("GPU 分配最大尝试次数必须在 %d 至 %d 次之间", COLABONE_MIN_ASSIGN_MAX_ATTEMPTS, COLABONE_MAX_ASSIGN_MAX_ATTEMPTS)
	}
	if _, exists := public["assign_max_attempts"]; exists {
		public["assign_max_attempts"] = assignMaxAttemptsVal
	}

	if v, exists := public["auto_drive"]; exists {
		if _, ok := isStrictBool(v); !ok {
			return nil, errors.New("自动挂载云盘设置不合法")
		}
	}
	if v, exists := public["assign_proxy_enabled"]; exists {
		if _, ok := isStrictBool(v); !ok {
			return nil, errors.New("分配代理开关设置不合法")
		}
	}

	clearAssignProxy := asBool(payload["clear_assign_proxy"])
	rawAssignProxy := ""
	if _, exists := payload["assign_proxy"]; exists {
		rawAssignProxy = asTrimmedString(payload["assign_proxy"])
	}
	var newProxySpec *ColabOneAssignProxySpec
	if clearAssignProxy {
		public["assign_proxy_enabled"] = false
	} else if rawAssignProxy != "" {
		if c.assignProxy == nil {
			return nil, errors.New("分配代理组件未配置")
		}
		parsed, err := c.assignProxy.ParseAssignProxy(rawAssignProxy)
		if err != nil {
			return nil, err
		}
		newProxySpec = parsed
		if _, exists := public["assign_proxy_enabled"]; !exists {
			public["assign_proxy_enabled"] = true
		}
	}

	hfToken := ""
	if rawHF, exists := payload["hf_token"]; exists && rawHF != nil {
		rawStr, ok := rawHF.(string)
		if !ok {
			return nil, errors.New("hf_token 格式不合法")
		}
		cleaned, err := validateCleanHFToken(rawStr)
		if err != nil {
			return nil, err
		}
		hfToken = cleaned
	}

	existingProxy := ""
	if err := func() error {
		c.vaultConfigMu.Lock()
		defer c.vaultConfigMu.Unlock()

		if v, ok := c.vault.Get("assign_proxy"); ok {
			if s, ok := v.(string); ok {
				existingProxy = s
			}
		}
		var existingProxySpec *ColabOneAssignProxySpec
		if strings.TrimSpace(existingProxy) != "" && c.assignProxy != nil {
			if parsed, err := c.assignProxy.ParseAssignProxy(existingProxy); err == nil {
				existingProxySpec = parsed
			}
		}
		if enabled, ok := isStrictBool(public["assign_proxy_enabled"]); ok && enabled {
			if newProxySpec == nil && existingProxySpec == nil {
				return errors.New("请先填写并保存分配代理地址")
			}
		}
		if hfToken != "" {
			if err := c.vault.Set("hf", hfToken); err != nil {
				return err
			}
		}
		if clearAssignProxy {
			if err := c.vault.Delete("assign_proxy"); err != nil {
				return err
			}
		} else if newProxySpec != nil {
			if err := c.vault.Set("assign_proxy", newProxySpec.NormalizedURL); err != nil {
				return err
			}
		}
		return nil
	}(); err != nil {
		return nil, err
	}

	c.persistMu.Lock()
	c.mu.Lock()
	prevMode, prevIdle, prevCULimit, _ := c.currentReleasePolicyLocked()
	prevProxyEnabled := asBool(c.settings["assign_proxy_enabled"])
	for k, v := range public {
		c.settings[k] = v
	}
	nextMode, nextIdle, nextCULimit, _ := c.currentReleasePolicyLocked()
	idleChanged := nextMode != prevMode || nextIdle != prevIdle || math.Abs(nextCULimit-prevCULimit) > 1e-6
	snapshotSettings := deepCopyMap(c.settings)
	c.mu.Unlock()

	saveErr := AtomicJSON(filepath.Join(c.directory, "settings.json"), snapshotSettings)
	c.persistMu.Unlock()
	if saveErr != nil {
		return nil, saveErr
	}

	snapIdle, _ := asInt(snapshotSettings["idle_minutes"])
	snapMode := normalizeColabOneReleaseMode(snapshotSettings["release_mode"])
	snapCULimit, _ := asFloat(snapshotSettings["release_cu_limit"])
	if snapCULimit < COLABONE_MIN_RELEASE_CU_LIMIT || snapCULimit > COLABONE_MAX_RELEASE_CU_LIMIT {
		snapCULimit = COLABONE_DEFAULT_RELEASE_CU_LIMIT
	}
	snapThreads, _ := asInt(snapshotSettings["download_threads"])
	snapAssignMaxAttempts, ok := asExactInt(snapshotSettings["assign_max_attempts"])
	if !ok || snapAssignMaxAttempts < COLABONE_MIN_ASSIGN_MAX_ATTEMPTS || snapAssignMaxAttempts > COLABONE_MAX_ASSIGN_MAX_ATTEMPTS {
		snapAssignMaxAttempts = COLABONE_DEFAULT_ASSIGN_MAX_ATTEMPTS
	}
	releaseSummary := fmt.Sprintf("空闲释放: %d 分钟", snapIdle)
	if snapMode == COLABONE_RELEASE_MODE_NEVER {
		releaseSummary = "自动释放: 长期不释放"
	} else if snapMode == COLABONE_RELEASE_MODE_CU_LIMIT {
		releaseSummary = fmt.Sprintf("自动释放: 消耗达 %s CU 释放", formatColabOneCUCompact(snapCULimit))
	}
	summaryParts := []string{
		fmt.Sprintf("默认 GPU: %s", asString(snapshotSettings["gpu"])),
		releaseSummary,
		fmt.Sprintf("下载并发: %d 线程", snapThreads),
		fmt.Sprintf("分配重试上限: %d 次", snapAssignMaxAttempts),
	}
	if hfToken != "" {
		summaryParts = append(summaryParts, "已更新 Hugging Face Token")
	}
	if clearAssignProxy {
		summaryParts = append(summaryParts, "已清除分配代理")
	} else if newProxySpec != nil {
		if asBool(snapshotSettings["assign_proxy_enabled"]) {
			summaryParts = append(summaryParts, fmt.Sprintf("已启用仅分配代理（%s）", newProxySpec.LogLabel))
		} else {
			summaryParts = append(summaryParts, fmt.Sprintf("已保存分配代理（%s）", newProxySpec.LogLabel))
		}
	} else if _, exists := public["assign_proxy_enabled"]; exists && asBool(snapshotSettings["assign_proxy_enabled"]) != prevProxyEnabled {
		if asBool(snapshotSettings["assign_proxy_enabled"]) {
			savedLabel := ""
			if c.assignProxy != nil {
				savedLabel = c.assignProxy.ProxyLogLabel(existingProxy)
			}
			if savedLabel != "" {
				summaryParts = append(summaryParts, fmt.Sprintf("已开启仅分配代理（%s）", savedLabel))
			} else {
				summaryParts = append(summaryParts, "已开启仅分配代理")
			}
		} else {
			summaryParts = append(summaryParts, "已关闭仅分配代理")
		}
	}
	c.log("已保存设置（"+strings.Join(summaryParts, " · ")+"）", "设置", "info")

	c.mu.Lock()
	c.lastConfig = 0
	if idleChanged {
		c.lastCUAutoReleaseAttempt = 0
	}
	for _, st := range c.instanceStates {
		st.LastConfig = 0
	}
	c.mu.Unlock()

	sessionsMap := c.vaultSessions()
	sessions := make([]string, 0, len(sessionsMap))
	for ep := range sessionsMap {
		sessions = append(sessions, ep)
	}
	sort.Strings(sessions)
	if idleChanged && len(sessions) > 0 && !c.opBusy.Load() {
		c.spawn(func() {
			for _, ep := range sessions {
				if c.stop.IsSet() {
					return
				}
				epOpGen := c.opGen.Load()
				if c.opBusy.Load() {
					continue
				}
				if _, owned := c.vaultSessions()[ep]; !owned {
					continue
				}
				pushed, err := c.configureRemote(c.rootCtx, ep, true)
				if err != nil || !pushed {
					continue
				}
				if c.stop.IsSet() {
					return
				}
				if c.opBusy.Load() || c.opGen.Load() != epOpGen {
					continue
				}
				isActive := ep == c.activeEndpoint()
				c.mu.Lock()
				curMode, curIdleMin, curCULimit, _ := c.currentReleasePolicyLocked()
				var rtState string
				if isActive {
					rtState = asString(asMap(c.data["runtime"])["state"])
				} else if st := c.instanceStates[ep]; st != nil {
					rtState = asString(st.Runtime["state"])
				}
				c.mu.Unlock()
				if curMode == COLABONE_RELEASE_MODE_IDLE {
					c.log(fmt.Sprintf("已向实例 %s 同步空闲释放时长（%d 分钟）", ep, curIdleMin), "GPU", "info")
				} else {
					c.log(fmt.Sprintf("已向实例 %s 同步自动释放策略（%s）", ep, formatColabOneReleasePolicyZh(curMode, curIdleMin, curCULimit)), "GPU", "info")
				}
				if isActive && rtState == "ready" {
					_, _ = c.refreshRemote(c.rootCtx, "", true)
				} else if rtState == "ready" {
					_, _ = c.refreshRemote(c.rootCtx, ep, true)
				}
			}
		})
	}
	return map[string]any{"ok": true}, nil
}

// SendActivity flushes a queued user activity touch to the remote runtime (debounced to 5s).
func (c *ColabOneController) SendActivity(ctx context.Context) {
	if !c.touchBusy.CompareAndSwap(false, true) {
		return
	}
	defer c.touchBusy.Store(false)

	now := nowUnixFloat()
	c.mu.Lock()
	proto, _ := asInt(asMap(c.data["runtime"])["protocol"])
	if c.opBusy.Load() || !c.pendingTouch || now-c.lastTouch < 5 || proto < 2 {
		c.mu.Unlock()
		return
	}
	c.pendingTouch = false
	c.mu.Unlock()

	if _, err := c.remote(ctx, "POST", "activity", map[string]any{}, "", 0); err != nil {
		// A failed activity signal must not masquerade as a reset countdown.
		c.mu.Lock()
		c.pendingTouch = true
		c.mu.Unlock()
		return
	}
	c.mu.Lock()
	c.lastTouch = nowUnixFloat()
	c.mu.Unlock()
}

// ValidateModelID extracts and validates payload["id"] against ^[a-zA-Z0-9_-]{1,80}$.
func ValidateModelID(payload map[string]any) (string, error) {
	val, ok := payload["id"].(string)
	if !ok || !reModelID.MatchString(val) {
		return "", errors.New("模型 ID 不合法")
	}
	return val, nil
}

func (c *ColabOneController) validatePublicAPIConfig(raw map[string]any) (map[string]any, error) {
	if c.publicAPI == nil {
		return nil, errors.New("PublicAPIValidator 未配置")
	}
	return c.publicAPI.ValidatePublicConfig(raw)
}

func (c *ColabOneController) validateComfyCanvasConfig(raw map[string]any) (map[string]any, error) {
	if c.comfy == nil {
		return nil, errors.New("ComfyValidator 未配置")
	}
	return c.comfy.ValidateCanvasConfig(raw)
}

func resolveTaskElapsedSeconds(task map[string]any, serverTime float64) (float64, bool) {
	if task == nil {
		return 0, false
	}
	if elapsed, ok := asFloat(task["elapsed_seconds"]); ok && !math.IsNaN(elapsed) && !math.IsInf(elapsed, 0) && elapsed >= 0 {
		return elapsed, true
	}
	createdAt, hasCreated := asFloat(task["created_at"])
	if !hasCreated || math.IsNaN(createdAt) || math.IsInf(createdAt, 0) || createdAt <= 0 {
		return 0, false
	}
	if finishedAt, hasFinished := asFloat(task["finished_at"]); hasFinished && !math.IsNaN(finishedAt) && !math.IsInf(finishedAt, 0) && finishedAt > 0 {
		return math.Max(0, finishedAt-createdAt), true
	}
	if !math.IsNaN(serverTime) && !math.IsInf(serverTime, 0) && serverTime > 0 {
		return math.Max(0, serverTime-createdAt), true
	}
	return 0, false
}

func formatColabOneDurationZh(seconds float64, subsecond bool) string {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
		if subsecond {
			return "0.0 秒"
		}
		return "0 秒"
	}
	if subsecond && seconds < 60 {
		roundedTenths := math.Round(seconds*10) / 10
		if roundedTenths < 60 {
			return fmt.Sprintf("%.1f 秒", roundedTenths)
		}
		return "1 分钟"
	}
	var total int64
	if subsecond {
		total = int64(math.Round(seconds))
	} else {
		total = int64(seconds)
	}
	if total < 60 {
		return fmt.Sprintf("%d 秒", total)
	}
	hours := total / 3600
	mins := (total % 3600) / 60
	secs := total % 60
	if hours == 0 {
		if secs == 0 {
			return fmt.Sprintf("%d 分钟", mins)
		}
		return fmt.Sprintf("%d 分 %d 秒", mins, secs)
	}
	if mins == 0 && secs == 0 {
		return fmt.Sprintf("%d 小时", hours)
	}
	if secs == 0 {
		return fmt.Sprintf("%d 小时 %d 分", hours, mins)
	}
	return fmt.Sprintf("%d 小时 %d 分 %d 秒", hours, mins, secs)
}
