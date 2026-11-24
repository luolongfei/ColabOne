package controller

import (
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
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/luolongfei/ColabOne/console/internal/activity"
)

const (
	// COLABONE_DEFAULT_COMMON_VERSION tracks one-time seeding of default common repositories.
	COLABONE_DEFAULT_COMMON_VERSION = 1

	// COLABONE_DEFAULT_GPU is the default Colab GPU accelerator tier for new and migrated settings.
	COLABONE_DEFAULT_GPU = "G4"

	// COLABONE_DEFAULT_GPU_POLICY_VERSION tracks one-time migration of the legacy T4 default to G4.
	COLABONE_DEFAULT_GPU_POLICY_VERSION = 1

	colabOneMaxOutputAssetOffset = 512 * 1024 * 1024
)

// ColabOneDefaultCommonRepo describes a default recommended Hugging Face model repository.
type ColabOneDefaultCommonRepo struct {
	Repo string
	Name string
	Note string
}

// COLABONE_DEFAULT_COMMON_REPOS lists the initial common repositories seeded once into library.json.
var COLABONE_DEFAULT_COMMON_REPOS = []ColabOneDefaultCommonRepo{
	{
		Repo: "huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
		Name: "Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
		Note: "默认模型",
	},
	{
		Repo: "huihui-ai/Huihui-DeepSeek-V4.1-Flash-1017-abliterated-GGUF",
		Name: "Huihui-DeepSeek-V4.1-Flash-1017-abliterated-GGUF",
		Note: "默认模型",
	},
	{
		Repo: "JonathanColetti/Qwen3.8-27B-Uncensored",
		Name: "Qwen3.8-27B-Uncensored",
		Note: "默认模型",
	},
}

var (
	reModelID  = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)
	reHex32ID  = regexp.MustCompile(`^[a-f0-9]{32}$`)
	reRepoSlug = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)
)

var validColabOneGPUs = map[string]bool{
	"T4":   true,
	"L4":   true,
	"A100": true,
	"G4":   true,
	"H100": true,
}

var validColabOneLocales = map[string]bool{
	"zh-CN": true,
	"en-US": true,
	"zh-TW": true,
	"zh-HK": true,
	"ja-JP": true,
	"ko-KR": true,
}

var colabOneModelSpecFields = []string{
	"id",
	"api_id",
	"name",
	"description",
	"min_vram_mib",
	"context_length",
	"temperature",
	"cpu_moe_layers",
	"source",
	"kind",
	"files",
}

// DefaultConfigDir returns the platform-appropriate ColabOne configuration directory,
// honoring COLABONE_CONFIG_DIR first.
func DefaultConfigDir() string {
	if dir := strings.TrimSpace(os.Getenv("COLABONE_CONFIG_DIR")); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "ColabOne")
	}
	if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
		return filepath.Join(localAppData, "ColabOne")
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "AppData", "Local", "ColabOne")
	}
	return filepath.Join(home, ".local", "share", "ColabOne")
}

// ErrNotFound marks a missing remote resource (e.g. HTTP 404 or KeyError).
var ErrNotFound = errors.New("not found")

// ErrLookup marks a disappeared external assignment during telemetry observation.
var ErrLookup = errors.New("assignment not found")

// ColabOneOperationCancelled indicates the user voluntarily cancelled a long-running operation.
type ColabOneOperationCancelled struct {
	Message string
	Source  string
}

func (e *ColabOneOperationCancelled) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// ColabOneHTTPError captures an HTTP error response status code without leaking URLs.
type ColabOneHTTPError struct {
	StatusCode int
	Message    string
}

func (e *ColabOneHTTPError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}

// HTTPStatusCode returns the HTTP status code associated with this error.
func (e *ColabOneHTTPError) HTTPStatusCode() int {
	if e == nil {
		return 0
	}
	return e.StatusCode
}

type httpStatusCoder interface {
	HTTPStatusCode() int
}

func httpStatusCode(err error) int {
	if err == nil {
		return 0
	}
	var coder httpStatusCoder
	if errors.As(err, &coder) {
		return coder.HTTPStatusCode()
	}
	return 0
}

func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNotFound) {
		return true
	}
	if httpStatusCode(err) == 404 {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "404") ||
		strings.Contains(msg, "不存在") ||
		strings.Contains(strings.ToLower(msg), "not found")
}

func isExportedTypeName(name string) bool {
	if name == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(r)
}

func errTypeName(err error) string {
	if err == nil {
		return "Error"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return "TimeoutError"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "TimeoutError"
	}
	if httpStatusCode(err) > 0 {
		return "HTTPError"
	}
	var runtimeErr runtime.Error
	if errors.As(err, &runtimeErr) || errors.Is(err, context.Canceled) {
		return "RuntimeError"
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return "ConnectionError"
	}
	var opErr *net.OpError
	var dnsErr *net.DNSError
	if errors.As(err, &opErr) || errors.As(err, &dnsErr) || errors.As(err, &netErr) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "ConnectionError"
	}

	exportedName := ""
	seen := make(map[error]bool)
	cur := err
	for steps := 0; cur != nil && steps < 32; steps++ {
		if rv := reflect.ValueOf(cur); rv.IsValid() && rv.Type().Comparable() {
			if seen[cur] {
				break
			}
			seen[cur] = true
		}
		if ue, ok := cur.(*url.Error); ok && ue.Err != nil {
			cur = ue.Err
			continue
		}
		t := reflect.TypeOf(cur)
		for t != nil && t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t != nil {
			if name := t.Name(); isExportedTypeName(name) {
				exportedName = name
			}
		}
		next := errors.Unwrap(cur)
		if next == nil {
			break
		}
		cur = next
	}
	if exportedName != "" {
		return exportedName
	}
	return "Error"
}

// ColabOneEvent is a thread-safe resettable signal analogous to Python's threading.Event.
type ColabOneEvent struct {
	mu  sync.Mutex
	ch  chan struct{}
	set bool
}

// NewColabOneEvent creates an unset ColabOneEvent.
func NewColabOneEvent() *ColabOneEvent {
	return &ColabOneEvent{ch: make(chan struct{})}
}

// Set marks the event as set and wakes all current waiters.
func (e *ColabOneEvent) Set() {
	e.mu.Lock()
	if !e.set {
		e.set = true
		close(e.ch)
	}
	e.mu.Unlock()
}

// Clear resets the event to unset state.
func (e *ColabOneEvent) Clear() {
	e.mu.Lock()
	if e.set {
		e.set = false
		e.ch = make(chan struct{})
	}
	e.mu.Unlock()
}

// IsSet reports whether the event is currently set.
func (e *ColabOneEvent) IsSet() bool {
	e.mu.Lock()
	v := e.set
	e.mu.Unlock()
	return v
}

// Chan returns a channel closed when the event is set.
func (e *ColabOneEvent) Chan() <-chan struct{} {
	e.mu.Lock()
	ch := e.ch
	e.mu.Unlock()
	return ch
}

// Consume blocks until the event is set (or done is closed), atomically
// resetting the event back to unset so repeatable pulses (such as manual
// retry-now or DriveFS confirm/probe clicks) are delivered cleanly on every
// trigger without busy-polling.
func (e *ColabOneEvent) Consume(done <-chan struct{}) bool {
	for {
		e.mu.Lock()
		select {
		case <-done:
			e.mu.Unlock()
			return false
		default:
		}
		if e.set {
			e.set = false
			e.ch = make(chan struct{})
			e.mu.Unlock()
			return true
		}
		ch := e.ch
		e.mu.Unlock()

		select {
		case <-done:
			return false
		case <-ch:
		}
	}
}

// Wait blocks until the event is set or timeout elapses, returning true if set.
func (e *ColabOneEvent) Wait(timeout time.Duration) bool {
	ch := e.Chan()
	select {
	case <-ch:
		return true
	default:
	}
	if timeout <= 0 {
		return false
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
		return true
	case <-timer.C:
		return false
	}
}

// WaitContext blocks until the event is set or ctx is cancelled, returning true if set.
func (e *ColabOneEvent) WaitContext(ctx context.Context) bool {
	ch := e.Chan()
	select {
	case <-ch:
		return true
	default:
	}
	select {
	case <-ch:
		return true
	case <-ctx.Done():
		return false
	}
}

// LogFunc records a structured activity entry. Empty source defaults to "客户端",
// empty level defaults to "info".
type LogFunc func(message, source, level string)

// ColabOneActivityEntry matches frontend/src/types.ts ActivityEntry.
type ColabOneActivityEntry struct {
	ID      int64   `json:"id"`
	Time    float64 `json:"time"`
	Source  string  `json:"source"`
	Level   string  `json:"level"`
	Message string  `json:"message"`
}

// ColabOneAssignProxySpec describes a parsed allocation-only proxy URL.
type ColabOneAssignProxySpec struct {
	Scheme        string `json:"scheme"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Username      string `json:"-"`
	Password      string `json:"-"`
	NormalizedURL string `json:"-"`
	MaskedURL     string `json:"masked_url"`
	LogLabel      string `json:"log_label"`
	HasAuth       bool   `json:"has_auth"`
}

// String redacts proxy credentials and raw URLs to prevent accidental leakage in logs.
func (s ColabOneAssignProxySpec) String() string {
	return fmt.Sprintf(
		"ColabOneAssignProxySpec{Scheme:%q, Host:%q, Port:%d, MaskedURL:%q, LogLabel:%q, HasAuth:%t}",
		s.Scheme,
		s.Host,
		s.Port,
		s.MaskedURL,
		s.LogLabel,
		s.HasAuth || s.Username != "" || s.Password != "",
	)
}

// GoString redacts proxy credentials and raw URLs when formatted with %#v.
func (s ColabOneAssignProxySpec) GoString() string {
	return s.String()
}

// ColabOneRPCOptions configures a remote kernel RPC invocation.
type ColabOneRPCOptions struct {
	Endpoint string
	Timeout  time.Duration
}

// ColabOneStartOptions configures GPU runtime allocation and startup.
type ColabOneStartOptions struct {
	CreateNew    bool
	RcloneConf   string
	AssignProxy  string
	OnLog        LogFunc
	OnTelemetry  func(endpoint string, location map[string]any, sample map[string]any)
	OnRetryState func(info map[string]any)
	CancelEvent  *ColabOneEvent
	RetryEvent   *ColabOneEvent
}

// ColabOneDriveMountOptions configures Colab native DriveFS mounting.
type ColabOneDriveMountOptions struct {
	Endpoint     string
	AccountEmail string
	OnAuthURL    func(url string)
	OnHint       func(hint string)
	OnLog        LogFunc
	CancelEvent  *ColabOneEvent
	ConfirmEvent *ColabOneEvent
	ProbeEvent   *ColabOneEvent
}

// ---------------------------------------------------------------------------
// Collaborator interfaces (stdlib-only for parallel package compilation)
// ---------------------------------------------------------------------------

// VaultStore persists encrypted credentials and session metadata.
type VaultStore interface {
	Get(key string) (any, bool)
	Set(key string, value any) error
	Delete(key string) error
}

// AuthClient manages Google OAuth credentials, Drive scope checks, and GCP quota projects.
type AuthClient interface {
	Locale() string
	SetLocale(locale string)
	Credentials(ctx context.Context, scope string) (any, error)
	Profile(ctx context.Context) (map[string]any, error)
	ColabBegin(ctx context.Context) (map[string]any, error)
	ColabLogin(ctx context.Context, onAuthURL func(string)) error
	ColabFinish(ctx context.Context, code string) error
	LoopbackActive() bool
	HasDriveScope() bool
	RequiresDriveQuotaProject() bool
	DriveQuotaStatus() map[string]any
	EnsureDriveQuotaProject(ctx context.Context, forceRefresh bool, onLog LogFunc) (projectID string, reason string, err error)
	InvalidateDriveQuotaProject()
	BuildRcloneConf(ctx context.Context, quotaProject string) (string, error)
}

// ProviderClient manages Colab runtime allocation, kernel RPCs, heartbeats, Drive, and telemetry observation.
type ProviderClient interface {
	Account(ctx context.Context) (map[string]any, error)
	RPC(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error)
	Start(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error)
	UpgradeService(ctx context.Context, endpoint string, onLog LogFunc) error
	Release(ctx context.Context, endpoint string, onLog LogFunc) (map[string]any, error)
	ReleaseAll(ctx context.Context, targets []string, notify func(string)) (map[string]any, error)
	Heartbeat(ctx context.Context, endpoint string) error
	Clear(endpoint string)
	SetActive(endpoint string) (string, error)
	SaveRemote(remote map[string]any, activate bool) error
	UploadDriveConfig(ctx context.Context, rcloneConf string, endpoint string) error
	CheckColabDriveFSAuth(ctx context.Context, endpoint string) (authorized bool, authURL string, err error)
	MountColabDriveFS(ctx context.Context, opts ColabOneDriveMountOptions) (map[string]any, error)
	ObserveExternalTelemetry(ctx context.Context, endpoint string) (map[string]any, error)
	CloseObserver(endpoint string, shutdownKernel bool)
}

// ProviderSessionStore is an optional interface that ProviderClient implementations
// may satisfy to expose cached session and remote maps directly.
type ProviderSessionStore interface {
	SessionsMap() map[string]map[string]any
	RemotesMap() map[string]map[string]any
}

// ProviderCloser is an optional interface that ProviderClient implementations
// may satisfy to release underlying resources on controller shutdown.
type ProviderCloser interface {
	Close()
}

// ActivityLogger stores bounded, credential-redacted activity logs.
type ActivityLogger interface {
	Add(message, source, level string)
	Snapshot() []ColabOneActivityEntry
	Clear()
}

// HubClient queries Hugging Face model search and file listings.
type HubClient interface {
	SearchModels(ctx context.Context, query, sort, cursor, token, modality, family string) (map[string]any, error)
	ModelFiles(ctx context.Context, repo, token string) (map[string]any, error)
}

// ModelResolver validates model manifests, resolves API model IDs, and imports HF models.
type ModelResolver interface {
	ValidateModelSpec(raw map[string]any) (map[string]any, error)
	ValidateAPIModelID(value string, allowEmpty bool) (string, error)
	ResolveAPIModelID(model map[string]any) string
	UniqueAPIModelID(baseAPIID string, existingIDs []string, repo, filename string) (string, error)
	ParseHFInput(repoOrURL, filename, revision string) (repo string, file string, rev string)
	ResolveImport(ctx context.Context, repo, filename, revision, token, customName string) (map[string]any, error)
}

// ComfyValidator validates ComfyUI catalogs, workflows, run requests, and canvas configs.
type ComfyValidator interface {
	ValidateCatalog(raw map[string]any) ([]map[string]any, error)
	ValidateWorkflowTemplate(raw map[string]any) (map[string]any, error)
	ValidateWorkflowImport(raw map[string]any) (map[string]any, error)
	ValidateWorkflowRun(raw map[string]any) (map[string]any, error)
	ValidateCanvasConfig(raw map[string]any) (map[string]any, error)
}

// PublicAPIValidator validates named-tunnel Public API gateway configurations.
type PublicAPIValidator interface {
	ValidatePublicConfig(raw map[string]any) (map[string]any, error)
}

// AssignProxyHelper parses, normalizes, formats, and probes allocation-only proxies.
type AssignProxyHelper interface {
	ParseAssignProxy(raw string) (*ColabOneAssignProxySpec, error)
	NormalizeAssignProxy(raw string) (string, error)
	ProxyLogLabel(raw string) string
	TestAssignProxy(ctx context.Context, raw string) (map[string]any, error)
}

// ShortcutManager inspects and creates/removes OS Start Menu and Desktop shortcuts.
type ShortcutManager interface {
	Status(sandboxDir string) map[string]bool
	Manage(root, target string, remove bool, sandboxDir, locale string) (map[string]bool, error)
}

// ServiceBundler provides the remote service bundle revision and embedded catalog JSONs.
type ServiceBundler interface {
	ServiceRevision() string
	ReadPresetsJSON() ([]byte, error)
	ReadComfyCatalogJSON() ([]byte, error)
}

// LocationInferer infers geographic location metadata from Colab endpoints and URLs.
type LocationInferer interface {
	InferLocationFromEndpoint(endpoint, url string) map[string]any
}

// ColabOneControllerConfig holds construction parameters and collaborator dependencies.
type ColabOneControllerConfig struct {
	Root               string
	ConfigDir          string
	ShortcutSandbox    string
	Background         bool
	Vault              VaultStore
	Auth               AuthClient
	Provider           ProviderClient
	Activity           ActivityLogger
	Hub                HubClient
	Models             ModelResolver
	Comfy              ComfyValidator
	PublicAPI          PublicAPIValidator
	AssignProxy        AssignProxyHelper
	Shortcuts          ShortcutManager
	ServiceBundler     ServiceBundler
	Location           LocationInferer
	MonitorInterval    time.Duration
	ObserverInterval   time.Duration
	CanvasWaitInterval time.Duration
	UpgradePollDelay   time.Duration
	StartRetryDelay    time.Duration
}

// ---------------------------------------------------------------------------
// Sanitization & redaction helpers
// ---------------------------------------------------------------------------

// SafeMessage redacts URLs, Bearer headers, OAuth/HF/API tokens, and secret assignments,
// and truncates to at most 600 runes (delegating to internal/activity.SafeMessage).
func SafeMessage(message string) string {
	return activity.SafeMessage(message)
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	n := 0
	for i := range s {
		if n == maxRunes {
			return s[:i]
		}
		n++
	}
	return s
}

// SanitizeJSONValue recursively converts non-finite floats (NaN, +Inf, -Inf) to nil
// so json.Marshal never fails when encoding desktop RPC responses.
func SanitizeJSONValue(v any) any {
	if v == nil {
		return nil
	}
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil
		}
		return x
	case float32:
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return nil
		}
		return x
	case string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
		return x
	case map[string]any:
		if x == nil {
			return nil
		}
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = SanitizeJSONValue(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = SanitizeJSONValue(item)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(x))
		for i, item := range x {
			if item == nil {
				out[i] = nil
				continue
			}
			m := make(map[string]any, len(item))
			for k, val := range item {
				m[k] = SanitizeJSONValue(val)
			}
			out[i] = m
		}
		return out
	case []ColabOneActivityEntry:
		out := make([]ColabOneActivityEntry, len(x))
		for i, entry := range x {
			if math.IsNaN(entry.Time) || math.IsInf(entry.Time, 0) {
				entry.Time = 0
			}
			out[i] = entry
		}
		return out
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice:
		if rv.IsNil() {
			return []any{}
		}
		out := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = SanitizeJSONValue(rv.Index(i).Interface())
		}
		return out
	case reflect.Array:
		out := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = SanitizeJSONValue(rv.Index(i).Interface())
		}
		return out
	case reflect.Map:
		if rv.IsNil() {
			return nil
		}
		if rv.Type().Key().Kind() == reflect.String {
			out := make(map[string]any, rv.Len())
			iter := rv.MapRange()
			for iter.Next() {
				out[iter.Key().String()] = SanitizeJSONValue(iter.Value().Interface())
			}
			return out
		}
	}
	return v
}

// ---------------------------------------------------------------------------
// In-memory VaultStore implementation (used as fallback & in offline tests)
// ---------------------------------------------------------------------------

// ColabOneMemoryVault is a thread-safe in-memory VaultStore.
type ColabOneMemoryVault struct {
	mu   sync.RWMutex
	data map[string]any
}

// NewColabOneMemoryVault returns an empty in-memory VaultStore.
func NewColabOneMemoryVault() *ColabOneMemoryVault {
	return &ColabOneMemoryVault{data: make(map[string]any)}
}

func (v *ColabOneMemoryVault) Get(key string) (any, bool) {
	v.mu.RLock()
	val, ok := v.data[key]
	v.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return deepCopyAny(val), true
}

func (v *ColabOneMemoryVault) Set(key string, value any) error {
	v.mu.Lock()
	v.data[key] = deepCopyAny(value)
	v.mu.Unlock()
	return nil
}

func (v *ColabOneMemoryVault) Delete(key string) error {
	v.mu.Lock()
	delete(v.data, key)
	v.mu.Unlock()
	return nil
}

// ---------------------------------------------------------------------------
// Deep copy, conversion, and token helpers
// ---------------------------------------------------------------------------

func deepCopyAny(v any) any {
	if v == nil {
		return nil
	}
	switch x := v.(type) {
	case map[string]any:
		return deepCopyMap(x)
	case []any:
		return deepCopySlice(x)
	case []map[string]any:
		return deepCopyMapSlice(x)
	case []string:
		if x == nil {
			return []string(nil)
		}
		cp := make([]string, len(x))
		copy(cp, x)
		return cp
	case map[string]string:
		if x == nil {
			return map[string]string(nil)
		}
		cp := make(map[string]string, len(x))
		for k, val := range x {
			cp[k] = val
		}
		return cp
	case map[string]float64:
		if x == nil {
			return map[string]float64(nil)
		}
		cp := make(map[string]float64, len(x))
		for k, val := range x {
			cp[k] = val
		}
		return cp
	case []ColabOneActivityEntry:
		if x == nil {
			return []ColabOneActivityEntry(nil)
		}
		cp := make([]ColabOneActivityEntry, len(x))
		copy(cp, x)
		return cp
	default:
		return deepCopyReflect(v)
	}
}

func isSimpleImmutableKind(k reflect.Kind) bool {
	switch k {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Complex64, reflect.Complex128,
		reflect.String:
		return true
	default:
		return false
	}
}

func deepCopyReflect(v any) any {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return nil
	}
	return deepCopyReflectValue(rv).Interface()
}

func deepCopyReflectValue(rv reflect.Value) reflect.Value {
	if !rv.IsValid() {
		return rv
	}
	switch rv.Kind() {
	case reflect.Interface:
		if rv.IsNil() {
			return rv
		}
		inner := deepCopyReflectValue(rv.Elem())
		out := reflect.New(rv.Type()).Elem()
		out.Set(inner)
		return out
	case reflect.Slice:
		if rv.IsNil() {
			return rv
		}
		n := rv.Len()
		cp := reflect.MakeSlice(rv.Type(), n, n)
		if isSimpleImmutableKind(rv.Type().Elem().Kind()) {
			reflect.Copy(cp, rv)
			return cp
		}
		for i := 0; i < n; i++ {
			elem := rv.Index(i)
			if elem.CanInterface() {
				copied := deepCopyAny(elem.Interface())
				if copied != nil {
					cv := reflect.ValueOf(copied)
					if cv.Type().AssignableTo(cp.Index(i).Type()) {
						cp.Index(i).Set(cv)
						continue
					}
				} else if k := cp.Index(i).Kind(); k == reflect.Interface || k == reflect.Map || k == reflect.Slice || k == reflect.Pointer {
					cp.Index(i).Set(reflect.Zero(cp.Index(i).Type()))
					continue
				}
			}
			cp.Index(i).Set(deepCopyReflectValue(elem))
		}
		return cp
	case reflect.Map:
		if rv.IsNil() {
			return rv
		}
		cp := reflect.MakeMapWithSize(rv.Type(), rv.Len())
		elemType := rv.Type().Elem()
		iter := rv.MapRange()
		for iter.Next() {
			k := iter.Key()
			val := iter.Value()
			if val.CanInterface() {
				copied := deepCopyAny(val.Interface())
				if copied != nil {
					cv := reflect.ValueOf(copied)
					if cv.Type().AssignableTo(elemType) {
						cp.SetMapIndex(k, cv)
						continue
					}
				} else if ek := elemType.Kind(); ek == reflect.Interface || ek == reflect.Map || ek == reflect.Slice || ek == reflect.Pointer {
					cp.SetMapIndex(k, reflect.Zero(elemType))
					continue
				}
			}
			cp.SetMapIndex(k, deepCopyReflectValue(val))
		}
		return cp
	case reflect.Array:
		n := rv.Len()
		cp := reflect.New(rv.Type()).Elem()
		if isSimpleImmutableKind(rv.Type().Elem().Kind()) {
			cp.Set(rv)
			return cp
		}
		for i := 0; i < n; i++ {
			cp.Index(i).Set(deepCopyReflectValue(rv.Index(i)))
		}
		return cp
	default:
		return rv
	}
}

func deepCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, val := range m {
		out[k] = deepCopyAny(val)
	}
	return out
}

func deepCopySlice(s []any) []any {
	if s == nil {
		return nil
	}
	out := make([]any, len(s))
	for i, val := range s {
		out[i] = deepCopyAny(val)
	}
	return out
}

func deepCopyMapSlice(s []map[string]any) []map[string]any {
	if s == nil {
		return []map[string]any{}
	}
	out := make([]map[string]any, len(s))
	for i, item := range s {
		out[i] = deepCopyMap(item)
	}
	return out
}

func asMap(v any) map[string]any {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func asMapSlice(v any) []map[string]any {
	if v == nil {
		return nil
	}
	switch s := v.(type) {
	case []map[string]any:
		return s
	case []any:
		out := make([]map[string]any, 0, len(s))
		for _, item := range s {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

func asAnySlice(v any) []any {
	if v == nil {
		return nil
	}
	switch s := v.(type) {
	case []any:
		return s
	case []map[string]any:
		out := make([]any, len(s))
		for i, m := range s {
			out[i] = m
		}
		return out
	case []string:
		out := make([]any, len(s))
		for i, str := range s {
			out[i] = str
		}
		return out
	default:
		return nil
	}
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case fmt.Stringer:
		return x.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

func asTrimmedString(v any) string {
	return strings.TrimSpace(asString(v))
}

func asBool(v any) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case int:
		return x != 0
	case int8:
		return x != 0
	case int16:
		return x != 0
	case int32:
		return x != 0
	case int64:
		return x != 0
	case uint:
		return x != 0
	case uint8:
		return x != 0
	case uint16:
		return x != 0
	case uint32:
		return x != 0
	case uint64:
		return x != 0
	case float32:
		return x != 0 && !math.IsNaN(float64(x))
	case float64:
		return x != 0 && !math.IsNaN(x)
	case json.Number:
		if n, err := strconv.ParseInt(string(x), 10, 64); err == nil {
			return n != 0
		}
		if u, err := strconv.ParseUint(string(x), 10, 64); err == nil {
			return u != 0
		}
		if f, err := strconv.ParseFloat(string(x), 64); err == nil {
			return f != 0 && !math.IsNaN(f)
		}
		return false
	case string:
		return x != ""
	case map[string]any:
		return len(x) > 0
	case []any:
		return len(x) > 0
	case []map[string]any:
		return len(x) > 0
	case []string:
		return len(x) > 0
	default:
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.Slice, reflect.Map, reflect.Array:
			return rv.Len() > 0
		}
		return true
	}
}

func asBoolDefault(m map[string]any, key string, def bool) bool {
	if m == nil {
		return def
	}
	val, exists := m[key]
	if !exists {
		return def
	}
	return asBool(val)
}

func isStrictBool(v any) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}

func asInt(v any) (int, bool) {
	if v == nil {
		return 0, false
	}
	switch x := v.(type) {
	case bool:
		return 0, false
	case int:
		return x, true
	case int32:
		return int(x), true
	case int64:
		return int(x), true
	case uint:
		return int(x), true
	case uint32:
		return int(x), true
	case uint64:
		return int(x), true
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0, false
		}
		return int(x), true
	case float32:
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return int(f), true
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return int(i), true
		}
		if f, err := x.Float64(); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return int(f), true
		}
		return 0, false
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, false
		}
		if i, err := strconv.Atoi(s); err == nil {
			return i, true
		}
		return 0, false
	default:
		return 0, false
	}
}

func asExactInt(v any) (int, bool) {
	if v == nil {
		return 0, false
	}
	switch x := v.(type) {
	case bool:
		return 0, false
	case int:
		return x, true
	case int32:
		return int(x), true
	case int64:
		return int(x), true
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) || math.Trunc(x) != x {
			return 0, false
		}
		return int(x), true
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return int(i), true
		}
		return 0, false
	default:
		return 0, false
	}
}

func asInt64(v any) (int64, bool) {
	if v == nil {
		return 0, false
	}
	switch x := v.(type) {
	case bool:
		return 0, false
	case int:
		return int64(x), true
	case int32:
		return int64(x), true
	case int64:
		return x, true
	case uint:
		return int64(x), true
	case uint32:
		return int64(x), true
	case uint64:
		return int64(x), true
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0, false
		}
		return int64(x), true
	case float32:
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return int64(f), true
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i, true
		}
		if f, err := x.Float64(); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return int64(f), true
		}
		return 0, false
	default:
		return 0, false
	}
}

func asFloat(v any) (float64, bool) {
	if v == nil {
		return 0, false
	}
	switch x := v.(type) {
	case bool:
		return 0, false
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0, false
		}
		return x, true
	case float32:
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	case int:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

func nowUnixFloat() float64 {
	return float64(time.Now().UnixNano()) / 1e9
}

func generateAPIKey() string {
	var buf [36]byte
	_, _ = rand.Read(buf[:])
	return "co_" + base64.RawURLEncoding.EncodeToString(buf[:])
}

func generateTokenHex(numBytes int) string {
	buf := make([]byte, numBytes)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func sumFileSizes(files []map[string]any) int64 {
	var total int64
	for _, f := range files {
		if sz, ok := asInt64(f["size"]); ok {
			total += sz
		}
	}
	return total
}
