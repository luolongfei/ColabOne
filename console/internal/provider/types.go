package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/luolongfei/ColabOne/console/internal/assignproxy"
)

// Sentinel errors matching ColabOne Python provider semantics.
var (
	ErrTooManyAssignments   = errors.New("too many Colab assignments")
	ErrUnsupportedGPU       = errors.New("不支持的 GPU 型号")
	ErrAllocationCancelled  = errors.New("已取消 GPU 分配")
	ErrStartCancelled       = errors.New("已取消 GPU 启动")
	ErrMissingEndpoint      = errors.New("缺少远端实例标识")
	ErrExternalEndpointGone = errors.New("远端实例已不在运行列表中")
)

// HTTPStatusCoder is optionally implemented by errors carrying an HTTP status code.
type HTTPStatusCoder interface {
	HTTPStatusCode() int
}

// ColabRequestError represents a non-2xx HTTP response from Google Colab endpoints.
type ColabRequestError struct {
	StatusCode   int
	Method       string
	URL          string
	Reason       string
	ResponseBody string
	Err          error
}

func (e *ColabRequestError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	if e.Reason != "" {
		return fmt.Sprintf("Colab request failed (HTTP %d): %s", e.StatusCode, e.Reason)
	}
	return fmt.Sprintf("Colab request failed (HTTP %d)", e.StatusCode)
}

func (e *ColabRequestError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *ColabRequestError) HTTPStatusCode() int {
	if e == nil {
		return 0
	}
	return e.StatusCode
}

// TooManyAssignmentsError represents HTTP 412 quota / concurrency rejection from Colab.
type TooManyAssignmentsError struct {
	Message string
	Err     error
}

func (e *TooManyAssignmentsError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return ErrTooManyAssignments.Error()
}

func (e *TooManyAssignmentsError) Unwrap() error {
	if e == nil {
		return nil
	}
	if e.Err != nil {
		return e.Err
	}
	return ErrTooManyAssignments
}

func (e *TooManyAssignmentsError) Is(target error) bool {
	return target == ErrTooManyAssignments
}

func (e *TooManyAssignmentsError) HTTPStatusCode() int {
	return http.StatusPreconditionFailed
}

// AssignProxyError marks transport/handshake errors originating from the isolated assign proxy.
// Kind may be "proxy", "ssl", "connect_timeout", "auth", or "timeout".
type AssignProxyError struct {
	Kind    string
	Message string
	Err     error
}

func (e *AssignProxyError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "assign proxy error"
}

func (e *AssignProxyError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ProxyErrorType exposes the proxy failure category structurally across packages.
func (e *AssignProxyError) ProxyErrorType() string {
	if e == nil {
		return ""
	}
	return e.Kind
}

// VaultStore persists encrypted per-key JSON values.
type VaultStore interface {
	GetJSON(key string, dest any) (bool, error)
	Set(key string, val any) error
	Delete(key string) error
	Has(key string) bool
}

// AuthProvider supplies Google account metadata and OAuth access tokens.
type AuthProvider interface {
	Profile(ctx context.Context) (map[string]string, error)
	AccessToken(ctx context.Context, name string) (string, error)
}

// ConsumptionInfo holds Colab compute unit balance and burn rate.
type ConsumptionInfo struct {
	PaidComputeUnitsBalance float64 `json:"paid_compute_units_balance"`
	ConsumptionRateHourly   float64 `json:"consumption_rate_hourly"`
}

// RuntimeProxyInfo holds the runtime proxy URL, bearer token, and expiry metadata.
type RuntimeProxyInfo struct {
	URL                   string    `json:"url"`
	Token                 string    `json:"token"`
	TokenExpiresInSeconds int       `json:"token_expires_in_seconds,omitempty"`
	ExpiresAt             time.Time `json:"expires_at,omitempty"`
}

// ResolveExpiresAt resolves an absolute expiry timestamp using ExpiresAt or TokenExpiresInSeconds.
func (r *RuntimeProxyInfo) ResolveExpiresAt(now time.Time) time.Time {
	if r == nil {
		return time.Time{}
	}
	if !r.ExpiresAt.IsZero() {
		return r.ExpiresAt.UTC()
	}
	if r.TokenExpiresInSeconds > 0 {
		return now.UTC().Add(time.Duration(r.TokenExpiresInSeconds) * time.Second)
	}
	return time.Time{}
}

// ListedAssignment describes an active Colab runtime returned by ListAssignments.
type ListedAssignment struct {
	Endpoint         string            `json:"endpoint"`
	Variant          string            `json:"variant"`
	Accelerator      string            `json:"accelerator"`
	MachineShape     string            `json:"machine_shape"`
	RuntimeProxyInfo *RuntimeProxyInfo `json:"runtime_proxy_info,omitempty"`
}

// Assignment describes a Colab runtime returned by GetAssignment/PostAssignment.
type Assignment struct {
	Endpoint         string            `json:"endpoint"`
	Variant          string            `json:"variant"`
	Accelerator      string            `json:"accelerator"`
	MachineShape     string            `json:"machine_shape"`
	RuntimeProxyInfo *RuntimeProxyInfo `json:"runtime_proxy_info,omitempty"`
}

// ColabClient communicates with Google Colab control-plane endpoints.
// An isolated assign-proxy client may optionally also implement io.Closer.
type ColabClient interface {
	ColabDomain() string
	GetConsumptionUserInfo(ctx context.Context) (*ConsumptionInfo, error)
	ListAssignments(ctx context.Context) ([]ListedAssignment, error)
	GetAssignment(ctx context.Context, nbh, variant, accel, shape string) (*Assignment, string, error)
	PostAssignment(ctx context.Context, nbh, token, variant, accel, shape string) (*Assignment, error)
	Unassign(ctx context.Context, endpoint string) error
	KeepAlive(ctx context.Context, endpoint string) error
	DoAuthorized(ctx context.Context, req *http.Request) (*http.Response, error)
}

// ClientFactory constructs a ColabClient; assignProxy is empty for direct/regular calls
// and holds the normalized proxy URL for isolated /tun/m/assign requests.
type ClientFactory func(assignProxy string) (ColabClient, error)

// ContentsClient uploads and deletes files on the remote Colab runtime via Jupyter Contents API.
type ContentsClient interface {
	UploadBytes(ctx context.Context, baseURL, proxyToken, remotePath string, data []byte, timeout time.Duration) error
	DeleteFile(ctx context.Context, baseURL, proxyToken, remotePath string) error
}

// KernelOutput represents a single Jupyter IOPub stream or error message.
type KernelOutput struct {
	OutputType string `json:"output_type"`
	Name       string `json:"name,omitempty"`
	Text       string `json:"text,omitempty"`
	EName      string `json:"ename,omitempty"`
	EValue     string `json:"evalue,omitempty"`
}

// ColabRequestHook handles Colab-specific stdin input_request messages (such as dfs_ephemeral).
type ColabRequestHook func(msg map[string]any, reply func(payload map[string]any) error) bool

// ExecuteOptions configures a single remote kernel execution.
type ExecuteOptions struct {
	Timeout          time.Duration
	AllowStdin       bool
	StdinHook        func(prompt string) string
	OutputHook       func(output KernelOutput)
	ColabRequestHook ColabRequestHook
}

// KernelOptions configures creation of a remote Colab Jupyter kernel channel.
type KernelOptions struct {
	ServerURL           string
	ProxyToken          string
	SessionName         string
	KernelID            string
	SessionID           string
	ShutdownOnWSTimeout bool
	OnKernelStarted     func(kernelID string)
	OnSessionStarted    func(sessionID string)
}

// KernelClient abstracts a live Jupyter WebSocket kernel channel to a Colab runtime.
type KernelClient interface {
	Start(ctx context.Context) error
	ExecuteCode(ctx context.Context, code string, opts ExecuteOptions) ([]KernelOutput, error)
	Abort()
	Stop(ctx context.Context, shutdownKernel bool) error
	KernelID() string
	SessionID() string
}

// KernelFactory creates a KernelClient for a given runtime proxy and session configuration.
type KernelFactory func(opts KernelOptions) KernelClient

// LogFn records structured backend activity logs (source e.g. "GPU", "客户端", "远端", "计费", "云盘").
type LogFn func(message, source, level string)

// StageFn updates the user-visible stage text of a running operation.
type StageFn func(stage string)

// TelemetryFn delivers live hardware telemetry samples streamed during remote boot.
type TelemetryFn func(endpoint string, location map[string]any, sample map[string]any)

// RetryStateFn reports allocation retry/countdown state transitions.
type RetryStateFn func(state map[string]any)

// DriveMountOptions configures Google Colab native DriveFS (`dfs_ephemeral`) mounting.
type DriveMountOptions struct {
	OnAuthURL    func(url string)
	OnLog        LogFn
	CancelCh     <-chan struct{}
	ConfirmCh    <-chan struct{}
	ProbeCh      <-chan struct{}
	OnHint       func(hint string)
	AccountEmail string
	Timeout      time.Duration
}

// DriveMounter handles Colab credentials-propagation for native DriveFS mounting.
type DriveMounter interface {
	DryrunCheck(ctx context.Context) (bool, string, error)
	Hook(msg map[string]any, reply func(payload map[string]any) error) bool
	Stop()
	Err() error
}

// DriveMounterFactory creates a DriveMounter for a specific Colab runtime endpoint.
type DriveMounterFactory func(client ColabClient, endpoint string, opts DriveMountOptions) DriveMounter

// ServiceBundler packages the remote Python control service into colabone.zip.
type ServiceBundler interface {
	BuildZip() ([]byte, int, error)
}

// BootScriptProvider may optionally be implemented by a ServiceBundler to supply
// embedded vm_boot.py and vm_update.py scripts directly.
type BootScriptProvider interface {
	VMBootScript() (string, error)
	VMUpdateScript() (string, error)
}

// LocationInferer resolves geographic metadata from GCP zones, Colab endpoints, or IP geo payloads.
type LocationInferer interface {
	LocationFromZone(zone string) map[string]any
	InferLocationFromEndpoint(ep, url string) map[string]any
	NormalizeGeoPayload(country, region, city, zone string) map[string]any
}

// SafeMessageFn redacts sensitive tokens and URLs from user-facing messages.
type SafeMessageFn func(string) string

// ColabOneSessionState stores local ownership and proxy credentials for a managed Colab runtime.
type ColabOneSessionState struct {
	Name           string     `json:"name"`
	Token          string     `json:"token"`
	URL            string     `json:"url"`
	Endpoint       string     `json:"endpoint"`
	TokenExpiresAt *time.Time `json:"token_expires_at,omitempty"`
	Variant        string     `json:"variant"`
	Accelerator    string     `json:"accelerator"`
	MachineShape   string     `json:"machine_shape"`
	KernelID       string     `json:"kernel_id,omitempty"`
	SessionID      string     `json:"session_id,omitempty"`
}

// UnmarshalJSON decodes ColabOneSessionState while accepting legacy Python/Pydantic ISO timestamps
// and filling default Variant/Accelerator/MachineShape fields.
func (s *ColabOneSessionState) UnmarshalJSON(data []byte) error {
	type rawSession struct {
		Name           string          `json:"name"`
		Token          string          `json:"token"`
		URL            string          `json:"url"`
		Endpoint       string          `json:"endpoint"`
		TokenExpiresAt json.RawMessage `json:"token_expires_at,omitempty"`
		Variant        string          `json:"variant"`
		Accelerator    string          `json:"accelerator"`
		MachineShape   string          `json:"machine_shape"`
		KernelID       string          `json:"kernel_id,omitempty"`
		SessionID      string          `json:"session_id,omitempty"`
	}
	var raw rawSession
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	s.Name = raw.Name
	s.Token = raw.Token
	s.URL = raw.URL
	s.Endpoint = raw.Endpoint
	s.Variant = raw.Variant
	s.Accelerator = raw.Accelerator
	s.MachineShape = raw.MachineShape
	s.KernelID = raw.KernelID
	s.SessionID = raw.SessionID
	s.TokenExpiresAt = parseFlexibleTime(raw.TokenExpiresAt)
	s.applyDefaults()
	return nil
}

func (s *ColabOneSessionState) applyDefaults() {
	if s.Name == "" {
		s.Name = "colabone"
	}
	if s.Variant == "" {
		s.Variant = "DEFAULT"
	}
	if s.Accelerator == "" {
		s.Accelerator = "NONE"
	}
	if s.MachineShape == "" {
		s.MachineShape = "STANDARD"
	}
}

func (s ColabOneSessionState) clone() ColabOneSessionState {
	cp := s
	if s.TokenExpiresAt != nil {
		t := *s.TokenExpiresAt
		cp.TokenExpiresAt = &t
	}
	return cp
}

// String redacts Token (and any proxy token query parameter in URL) so formatting
// a ColabOneSessionState never leaks runtime proxy credentials.
func (s ColabOneSessionState) String() string {
	tokenState := ""
	if s.Token != "" {
		tokenState = "[REDACTED]"
	}
	expires := ""
	if s.TokenExpiresAt != nil {
		expires = s.TokenExpiresAt.UTC().Format(time.RFC3339)
	}
	safeURL := s.URL
	if containsProviderProxyTokenParam(safeURL) {
		safeURL = redactProviderProxyTokenURL(safeURL)
	}
	return fmt.Sprintf(
		"ColabOneSessionState{Name:%q, Endpoint:%q, URL:%q, Token:%q, TokenExpiresAt:%q, Variant:%q, Accelerator:%q, MachineShape:%q, KernelID:%q, SessionID:%q}",
		s.Name,
		s.Endpoint,
		safeURL,
		tokenState,
		expires,
		s.Variant,
		s.Accelerator,
		s.MachineShape,
		s.KernelID,
		s.SessionID,
	)
}

// GoString redacts runtime proxy credentials when formatted with %#v.
func (s ColabOneSessionState) GoString() string {
	return s.String()
}

func parseFlexibleTime(raw json.RawMessage) *time.Time {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		str = strings.TrimSpace(str)
		if str == "" {
			return nil
		}
		layouts := []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02T15:04:05.999999999",
			"2006-01-02T15:04:05",
			"2006-01-02 15:04:05.999999999Z07:00",
			"2006-01-02 15:04:05.999999999",
			"2006-01-02 15:04:05Z07:00",
			"2006-01-02 15:04:05",
		}
		for _, layout := range layouts {
			if parsed, err := time.Parse(layout, str); err == nil {
				utc := parsed.UTC()
				return &utc
			}
		}
		return nil
	}
	var unixSec float64
	if err := json.Unmarshal(raw, &unixSec); err == nil && unixSec > 0 {
		sec := int64(unixSec)
		nsec := int64((unixSec - float64(sec)) * 1e9)
		t := time.Unix(sec, nsec).UTC()
		return &t
	}
	return nil
}

// AllocateOptions configures a single GPU allocation attempt sequence.
type AllocateOptions struct {
	OnLog        LogFn
	OnStage      StageFn
	CancelCh     <-chan struct{}
	OnRetryState RetryStateFn
	AssignProxy  string
	MaxAttempts  int
}

// StartOptions configures end-to-end GPU allocation, readiness check, and remote service deployment.
type StartOptions struct {
	CreateNew    bool
	RcloneConf   string
	OnTelemetry  TelemetryFn
	OnLog        LogFn
	CancelCh     <-chan struct{}
	OnRetryState RetryStateFn
	AssignProxy  string
	MaxAttempts  int
}

// ColabOneAssignProxySpec holds a validated and normalized allocation-only proxy specification.
type ColabOneAssignProxySpec struct {
	Scheme        string
	Host          string
	Port          int
	Username      string
	Password      string
	NormalizedURL string
	MaskedURL     string
	LogLabel      string
}

// IsSOCKS5 returns true if the proxy scheme is socks5 or socks5h.
func (s *ColabOneAssignProxySpec) IsSOCKS5() bool {
	if s == nil {
		return false
	}
	return s.Scheme == "socks5" || s.Scheme == "socks5h"
}

// HasAuth returns true if either username or password is set.
func (s *ColabOneAssignProxySpec) HasAuth() bool {
	if s == nil {
		return false
	}
	return s.Username != "" || s.Password != ""
}

// String redacts Username, Password, and NormalizedURL so formatting a
// ColabOneAssignProxySpec never leaks proxy credentials.
func (s ColabOneAssignProxySpec) String() string {
	return fmt.Sprintf(
		"ColabOneAssignProxySpec{Scheme:%q, Host:%q, Port:%d, HasAuth:%t, MaskedURL:%q, LogLabel:%q}",
		s.Scheme,
		s.Host,
		s.Port,
		s.Username != "" || s.Password != "",
		s.MaskedURL,
		s.LogLabel,
	)
}

// GoString redacts Username, Password, and NormalizedURL when formatted with %#v.
func (s ColabOneAssignProxySpec) GoString() string {
	return s.String()
}

// ColabOneProviderConfig wires all collaborators and optional test seams into ColabOneProvider.
type ColabOneProviderConfig struct {
	Auth                AuthProvider
	Vault               VaultStore
	RootDir             string
	ClientFactory       ClientFactory
	Contents            ContentsClient
	KernelFactory       KernelFactory
	DriveMounterFactory DriveMounterFactory
	Bundler             ServiceBundler
	Location            LocationInferer
	SafeMessage         SafeMessageFn
	HTTPClient          *http.Client

	// Optional overrides for scripts and assign proxy helpers.
	VMBootScript       string
	VMUpdateScript     string
	ParseAssignProxyFn func(raw string) (*ColabOneAssignProxySpec, error)
	FormatProxyErrorFn func(err error) string

	// Optional timing overrides (primarily for deterministic offline unit tests).
	AssignRetryDelays   []time.Duration
	ReleasePollInterval time.Duration
	ReadyTimeout        time.Duration
	ReadyPollInterval   time.Duration
	ReadyLogInterval    time.Duration
	UploadRetryDelay    time.Duration
	NowFn               func() time.Time
	SleepFn             func(ctx context.Context, d time.Duration) error
}

// ParseAssignProxy validates and normalizes an allocation-only proxy URL by
// delegating directly to assignproxy.ParseAssignProxy for exact parity.
// Returns (nil, nil) when raw is empty/whitespace.
func ParseAssignProxy(raw string) (*ColabOneAssignProxySpec, error) {
	spec, err := assignproxy.ParseAssignProxy(raw)
	if err != nil || spec == nil {
		return nil, err
	}
	return &ColabOneAssignProxySpec{
		Scheme:        spec.Scheme,
		Host:          spec.Host,
		Port:          spec.Port,
		Username:      spec.Username,
		Password:      spec.Password,
		NormalizedURL: spec.NormalizedURL,
		MaskedURL:     spec.MaskedURL,
		LogLabel:      spec.LogLabel,
	}, nil
}

// FormatProxyError formats a user-facing Chinese error message for allocation proxy failures
// with 100% parity with assignproxy.FormatProxyError.
func FormatProxyError(err error) string {
	if err == nil {
		return assignproxy.FormatProxyError(nil)
	}
	if errors.Is(err, assignproxy.ErrForbiddenTarget) {
		return assignproxy.ErrForbiddenTarget.Error()
	}

	var proxyErr interface{ ProxyErrorType() string }
	if errors.As(err, &proxyErr) {
		switch strings.ToLower(strings.TrimSpace(proxyErr.ProxyErrorType())) {
		case "ssl":
			return "通过分配代理建立加密连接失败（证书校验未通过），请勿使用会解密 HTTPS 流量的代理"
		case "auth":
			return "GPU 分配代理认证失败，请检查代理用户名与密码后重试"
		case "connect_timeout", "timeout":
			return "连接 GPU 分配代理超时，请检查代理地址、端口或网络连通性"
		}
	}

	return assignproxy.FormatProxyError(err)
}
