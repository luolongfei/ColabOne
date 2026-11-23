// Package colabclient implements the pure-Go Google Colab tunnel, assignment,
// consumption, session-state, and Jupyter Contents HTTP client for ColabOne.
//
// Protocol and data models are ported from google-colab-cli (Apache-2.0) with
// exact wire compatibility for Google Colab's /tun/m/* and runtime /api/contents/*
// endpoints as well as ColabOne's persisted SessionState vault format.
package colabclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultColabDomain is the production Google Colab web/tunnel origin.
	DefaultColabDomain = "https://colab.research.google.com"
	// DefaultColabAPIDomain is the production Google Colab API origin.
	DefaultColabAPIDomain = "https://colab.pa.googleapis.com"
	// TunEndpoint is the base path for Colab tunnel management endpoints.
	TunEndpoint = "/tun/m"
	// XSSIPrefix is Google's XSSI protection prefix on Colab JSON responses.
	XSSIPrefix = ")]}'\n"
	// ColabClientAgent is the X-Colab-Client-Agent header value expected by Colab.
	ColabClientAgent = "colab-cli"

	// Standard Google Colab HTTP headers.
	HeaderColabClientAgent       = "X-Colab-Client-Agent"
	HeaderColabXSRFToken         = "X-Goog-Colab-Token"
	HeaderColabTunnel            = "X-Colab-Tunnel"
	HeaderColabRuntimeProxyToken = "X-Colab-Runtime-Proxy-Token"

	// Known Colab accelerator identifiers.
	AcceleratorNone = "NONE"
	AcceleratorG4   = "G4"
	AcceleratorT4   = "T4"
	AcceleratorL4   = "L4"
	AcceleratorA100 = "A100"
	AcceleratorH100 = "H100"
	AcceleratorV5E1 = "V5E1"
	AcceleratorV6E1 = "V6E1"

	// Known Colab runtime variant identifiers.
	VariantDefault = "DEFAULT"
	VariantGPU     = "GPU"
	VariantTPU     = "TPU"

	// Known Colab machine shape identifiers.
	ShapeStandard      = "STANDARD"
	ShapeHighRAM       = "HIGH_RAM"
	AssignShapeHighMem = "hm"
)

var (
	// ErrTooManyAssignments is returned when Google Colab rejects a runtime
	// assignment with HTTP 412 (active instance limit or quota exceeded).
	ErrTooManyAssignments = errors.New("too many assignments")
	// ErrNotFound is returned by the Contents client on HTTP 404.
	ErrNotFound = errors.New("file or directory not found")
	// ErrIsDirectory is returned when DownloadBytes targets a directory.
	ErrIsDirectory = errors.New("cannot download a directory")

	xssiPrefixLF   = []byte(")]}'\n")
	xssiPrefixCRLF = []byte(")]}'\r\n")
)

// ColabOneRequestError represents a non-2xx HTTP response from Google Colab
// or a Colab runtime proxy endpoint.
type ColabOneRequestError struct {
	StatusCode int
	Method     string
	URL        string
	Reason     string
	Body       string
}

func (e *ColabOneRequestError) Error() string {
	if e == nil {
		return "<nil>"
	}
	reason := strings.TrimSpace(e.Reason)
	if reason == "" && e.StatusCode > 0 {
		reason = http.StatusText(e.StatusCode)
	}
	if reason != "" {
		return fmt.Sprintf("Failed to issue request %s %s: %s (HTTP %d)", e.Method, e.URL, reason, e.StatusCode)
	}
	return fmt.Sprintf("Failed to issue request %s %s: HTTP %d", e.Method, e.URL, e.StatusCode)
}

// StripXSSIPrefix removes Google's XSSI protection prefix (")]}'\n" or
// ")]}'\r\n") from body when present.
func StripXSSIPrefix(body []byte) []byte {
	if bytes.HasPrefix(body, xssiPrefixLF) {
		return body[len(xssiPrefixLF):]
	}
	if bytes.HasPrefix(body, xssiPrefixCRLF) {
		return body[len(xssiPrefixCRLF):]
	}
	return body
}

// UUIDToWebSafeBase64 transforms a UUID string into Google Colab's notebook
// hash (nbh) format by replacing '-' with '_' and right-padding with '.' to
// length 44.
func UUIDToWebSafeBase64(u string) string {
	transformed := strings.ReplaceAll(strings.TrimSpace(u), "-", "_")
	if len(transformed) < 44 {
		transformed += strings.Repeat(".", 44-len(transformed))
	}
	return transformed
}

// ResolveAssignShape maps accelerator and highMem intent to the /tun/m/assign
// "shape" query parameter. L4, V5E1, and V6E1 only exist in a single high-memory
// shape and omit the parameter, returning "".
func ResolveAssignShape(accelerator string, highMem bool) string {
	if !highMem {
		return ""
	}
	switch strings.ToUpper(strings.TrimSpace(accelerator)) {
	case AcceleratorL4, AcceleratorV5E1, AcceleratorV6E1:
		return ""
	default:
		return AssignShapeHighMem
	}
}

// ShapeDisplayLabel returns a human-friendly machine shape label ("High-RAM"
// or "Standard").
func ShapeDisplayLabel(shape string) string {
	switch strings.ToUpper(strings.TrimSpace(shape)) {
	case ShapeHighRAM, "1", "HM", "HIGH-RAM", "HIGHMEM":
		return "High-RAM"
	default:
		return "Standard"
	}
}

// ColabOneRuntimeProxyInfo holds runtime proxy credentials returned by Google
// Colab assignment APIs.
type ColabOneRuntimeProxyInfo struct {
	Token                 string `json:"token"`
	TokenExpiresInSeconds int    `json:"tokenExpiresInSeconds"`
	URL                   string `json:"url"`
}

// String redacts Token so formatting or logging never leaks runtime proxy credentials.
func (r ColabOneRuntimeProxyInfo) String() string {
	return fmt.Sprintf(
		"ColabOneRuntimeProxyInfo{Token:%q, TokenExpiresInSeconds:%d, URL:%q}",
		redactedTokenValue(r.Token),
		r.TokenExpiresInSeconds,
		redactedProxyURL(r.URL),
	)
}

// GoString redacts Token when formatted with %#v.
func (r ColabOneRuntimeProxyInfo) GoString() string {
	return r.String()
}

// ExpiresAt returns the absolute UTC expiry timestamp calculated from now plus
// TokenExpiresInSeconds. Call immediately after receiving the response.
func (r ColabOneRuntimeProxyInfo) ExpiresAt() time.Time {
	return time.Now().UTC().Add(time.Duration(r.TokenExpiresInSeconds) * time.Second)
}

// UnmarshalJSON accepts camelCase (tokenExpiresInSeconds) or snake_case
// (token_expires_in_seconds) and integer or numeric-string expiry durations.
func (r *ColabOneRuntimeProxyInfo) UnmarshalJSON(data []byte) error {
	var raw struct {
		Token                      string          `json:"token"`
		TokenExpiresInSeconds      json.RawMessage `json:"tokenExpiresInSeconds"`
		TokenExpiresInSecondsSnake json.RawMessage `json:"token_expires_in_seconds"`
		URL                        string          `json:"url"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	ttlRaw := raw.TokenExpiresInSeconds
	if len(bytes.TrimSpace(ttlRaw)) == 0 {
		ttlRaw = raw.TokenExpiresInSecondsSnake
	}
	ttl, err := parseFlexibleInt(ttlRaw)
	if err != nil {
		return fmt.Errorf("colabclient: invalid tokenExpiresInSeconds: %w", err)
	}
	r.Token = raw.Token
	r.TokenExpiresInSeconds = ttl
	r.URL = raw.URL
	return nil
}

// ColabOneListedAssignment represents one active assignment returned by
// GET /tun/m/assignments.
type ColabOneListedAssignment struct {
	Accelerator      string                   `json:"accelerator"`
	Endpoint         string                   `json:"endpoint"`
	Variant          string                   `json:"variant"`
	MachineShape     string                   `json:"machineShape"`
	RuntimeProxyInfo ColabOneRuntimeProxyInfo `json:"runtimeProxyInfo"`
}

// String formats the assignment with RuntimeProxyInfo credentials redacted.
func (a ColabOneListedAssignment) String() string {
	return fmt.Sprintf(
		"ColabOneListedAssignment{Accelerator:%q, Endpoint:%q, Variant:%q, MachineShape:%q, RuntimeProxyInfo:%s}",
		a.Accelerator,
		a.Endpoint,
		a.Variant,
		a.MachineShape,
		a.RuntimeProxyInfo.String(),
	)
}

// GoString formats the assignment with RuntimeProxyInfo credentials redacted for %#v.
func (a ColabOneListedAssignment) GoString() string {
	return a.String()
}

// UnmarshalJSON decodes variant (int 0/1/2 or string "DEFAULT"/"GPU"/"TPU")
// and machineShape (int 0/1 or string "STANDARD"/"HIGH_RAM").
func (a *ColabOneListedAssignment) UnmarshalJSON(data []byte) error {
	var raw struct {
		Accelerator           string                    `json:"accelerator"`
		Endpoint              string                    `json:"endpoint"`
		Variant               json.RawMessage           `json:"variant"`
		MachineShape          json.RawMessage           `json:"machineShape"`
		MachineShapeSnake     json.RawMessage           `json:"machine_shape"`
		RuntimeProxyInfo      *ColabOneRuntimeProxyInfo `json:"runtimeProxyInfo"`
		RuntimeProxyInfoSnake *ColabOneRuntimeProxyInfo `json:"runtime_proxy_info"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	variant, err := parseVariant(raw.Variant)
	if err != nil {
		return fmt.Errorf("colabclient: invalid assignment variant: %w", err)
	}
	shapeRaw := raw.MachineShape
	if len(bytes.TrimSpace(shapeRaw)) == 0 {
		shapeRaw = raw.MachineShapeSnake
	}
	shape, err := parseMachineShape(shapeRaw)
	if err != nil {
		return fmt.Errorf("colabclient: invalid assignment machineShape: %w", err)
	}
	a.Accelerator = raw.Accelerator
	a.Endpoint = raw.Endpoint
	a.Variant = variant
	a.MachineShape = shape
	if raw.RuntimeProxyInfo != nil {
		a.RuntimeProxyInfo = *raw.RuntimeProxyInfo
	} else if raw.RuntimeProxyInfoSnake != nil {
		a.RuntimeProxyInfo = *raw.RuntimeProxyInfoSnake
	} else {
		a.RuntimeProxyInfo = ColabOneRuntimeProxyInfo{}
	}
	return nil
}

// ColabOneGetAssignmentResponse represents the response from GET /tun/m/assign.
// When no runtime is assigned yet for the notebook hash, Token carries the XSRF
// token for POST /tun/m/assign. When a runtime is already assigned, Endpoint
// and RuntimeProxyInfo are populated instead.
type ColabOneGetAssignmentResponse struct {
	Acc              string                    `json:"acc,omitempty"`
	Nbh              string                    `json:"nbh,omitempty"`
	Token            string                    `json:"token,omitempty"`
	Variant          string                    `json:"variant,omitempty"`
	Accelerator      string                    `json:"accelerator,omitempty"`
	Endpoint         string                    `json:"endpoint,omitempty"`
	RuntimeProxyInfo *ColabOneRuntimeProxyInfo `json:"runtimeProxyInfo,omitempty"`
}

// String redacts Token and nested RuntimeProxyInfo credentials when formatted.
func (r ColabOneGetAssignmentResponse) String() string {
	proxyInfo := "<nil>"
	if r.RuntimeProxyInfo != nil {
		proxyInfo = r.RuntimeProxyInfo.String()
	}
	return fmt.Sprintf(
		"ColabOneGetAssignmentResponse{Acc:%q, Nbh:%q, Token:%q, Variant:%q, Accelerator:%q, Endpoint:%q, RuntimeProxyInfo:%s}",
		r.Acc,
		r.Nbh,
		redactedTokenValue(r.Token),
		r.Variant,
		r.Accelerator,
		r.Endpoint,
		proxyInfo,
	)
}

// GoString redacts Token and nested RuntimeProxyInfo credentials when formatted with %#v.
func (r ColabOneGetAssignmentResponse) GoString() string {
	return r.String()
}

// UnmarshalJSON decodes variant whether represented as an int (0/1/2) or string.
func (r *ColabOneGetAssignmentResponse) UnmarshalJSON(data []byte) error {
	var raw struct {
		Acc                   string                    `json:"acc"`
		Nbh                   string                    `json:"nbh"`
		Token                 string                    `json:"token"`
		Variant               json.RawMessage           `json:"variant"`
		Accelerator           string                    `json:"accelerator"`
		Endpoint              string                    `json:"endpoint"`
		RuntimeProxyInfo      *ColabOneRuntimeProxyInfo `json:"runtimeProxyInfo"`
		RuntimeProxyInfoSnake *ColabOneRuntimeProxyInfo `json:"runtime_proxy_info"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	variant, err := parseVariant(raw.Variant)
	if err != nil {
		return fmt.Errorf("colabclient: invalid get-assignment variant: %w", err)
	}
	r.Acc = raw.Acc
	r.Nbh = raw.Nbh
	r.Token = raw.Token
	r.Variant = variant
	r.Accelerator = raw.Accelerator
	r.Endpoint = raw.Endpoint
	if raw.RuntimeProxyInfo != nil {
		r.RuntimeProxyInfo = raw.RuntimeProxyInfo
	} else {
		r.RuntimeProxyInfo = raw.RuntimeProxyInfoSnake
	}
	return nil
}

// ColabOneAssignment represents an assigned Colab runtime returned either by
// POST /tun/m/assign or as an already-assigned response from GET /tun/m/assign.
type ColabOneAssignment struct {
	Endpoint         string                   `json:"endpoint"`
	Accelerator      string                   `json:"accelerator,omitempty"`
	Variant          string                   `json:"variant,omitempty"`
	RuntimeProxyInfo ColabOneRuntimeProxyInfo `json:"runtimeProxyInfo"`
}

// String formats the assignment with RuntimeProxyInfo credentials redacted.
func (a ColabOneAssignment) String() string {
	return fmt.Sprintf(
		"ColabOneAssignment{Endpoint:%q, Accelerator:%q, Variant:%q, RuntimeProxyInfo:%s}",
		a.Endpoint,
		a.Accelerator,
		a.Variant,
		a.RuntimeProxyInfo.String(),
	)
}

// GoString formats the assignment with RuntimeProxyInfo credentials redacted for %#v.
func (a ColabOneAssignment) GoString() string {
	return a.String()
}

// UnmarshalJSON decodes variant whether represented as an int (0/1/2) or string.
func (a *ColabOneAssignment) UnmarshalJSON(data []byte) error {
	var raw struct {
		Endpoint              string                    `json:"endpoint"`
		Accelerator           string                    `json:"accelerator"`
		Variant               json.RawMessage           `json:"variant"`
		RuntimeProxyInfo      *ColabOneRuntimeProxyInfo `json:"runtimeProxyInfo"`
		RuntimeProxyInfoSnake *ColabOneRuntimeProxyInfo `json:"runtime_proxy_info"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	variant, err := parseVariant(raw.Variant)
	if err != nil {
		return fmt.Errorf("colabclient: invalid assignment variant: %w", err)
	}
	a.Endpoint = raw.Endpoint
	a.Accelerator = raw.Accelerator
	a.Variant = variant
	if raw.RuntimeProxyInfo != nil {
		a.RuntimeProxyInfo = *raw.RuntimeProxyInfo
	} else if raw.RuntimeProxyInfoSnake != nil {
		a.RuntimeProxyInfo = *raw.RuntimeProxyInfoSnake
	} else {
		a.RuntimeProxyInfo = ColabOneRuntimeProxyInfo{}
	}
	return nil
}

// ColabOneAuthError wraps a non-transient OAuth token acquisition failure from
// the token provider so callers such as DriveFS propagation can fail fast
// instead of retrying as a transient HTTP transport error.
type ColabOneAuthError struct {
	Err error
}

func (e *ColabOneAuthError) Error() string {
	if e == nil || e.Err == nil {
		return "请先完成 Google 登录"
	}
	return e.Err.Error()
}

func (e *ColabOneAuthError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsPermanentAuthError marks this error as a permanent credential/login failure.
func (e *ColabOneAuthError) IsPermanentAuthError() bool {
	return true
}

// ColabOneConsumptionInfo holds account-level Colab compute unit balance,
// hourly burn rate, and active assignment count.
type ColabOneConsumptionInfo struct {
	PaidComputeUnitsBalance float64 `json:"paidComputeUnitsBalance"`
	ConsumptionRateHourly   float64 `json:"consumptionRateHourly"`
	AssignmentsCount        int     `json:"assignmentsCount"`
}

func parseFlexibleFloat(raw json.RawMessage) (float64, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, false, nil
	}
	var f float64
	if err := json.Unmarshal(trimmed, &f); err == nil {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, true, nil
		}
		return f, true, nil
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err == nil {
		s = strings.TrimSpace(s)
		if s == "" {
			return 0, true, nil
		}
		switch strings.ToLower(s) {
		case "nan", "+nan", "-nan", "inf", "+inf", "-inf", "infinity", "+infinity", "-infinity":
			return 0, true, nil
		}
		parsed, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false, err
		}
		if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return 0, true, nil
		}
		return parsed, true, nil
	}
	return 0, false, fmt.Errorf("cannot parse float from %s", string(trimmed))
}

// UnmarshalJSON accepts both GET /tun/m/ccu-info ("currentBalance") and
// ConsumptionUserInfo ("paidComputeUnitsBalance") field names, including
// proto3 string-encoded floats and "NaN"/"Infinity" sentinels.
func (c *ColabOneConsumptionInfo) UnmarshalJSON(data []byte) error {
	var raw struct {
		CurrentBalance             json.RawMessage `json:"currentBalance"`
		PaidComputeUnitsBalance    json.RawMessage `json:"paidComputeUnitsBalance"`
		CurrentBalanceSnake        json.RawMessage `json:"current_balance"`
		PaidComputeUnitsSnake      json.RawMessage `json:"paid_compute_units_balance"`
		ConsumptionRateHourly      json.RawMessage `json:"consumptionRateHourly"`
		ConsumptionRateHourlySnake json.RawMessage `json:"consumption_rate_hourly"`
		AssignmentsCount           json.RawMessage `json:"assignmentsCount"`
		AssignmentsCountSnake      json.RawMessage `json:"assignments_count"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	balance := 0.0
	for _, cand := range []json.RawMessage{
		raw.CurrentBalance,
		raw.PaidComputeUnitsBalance,
		raw.CurrentBalanceSnake,
		raw.PaidComputeUnitsSnake,
	} {
		v, ok, err := parseFlexibleFloat(cand)
		if err != nil {
			return fmt.Errorf("colabclient: invalid balance: %w", err)
		}
		if ok {
			balance = v
			break
		}
	}
	rate := 0.0
	for _, cand := range []json.RawMessage{
		raw.ConsumptionRateHourly,
		raw.ConsumptionRateHourlySnake,
	} {
		v, ok, err := parseFlexibleFloat(cand)
		if err != nil {
			return fmt.Errorf("colabclient: invalid consumptionRateHourly: %w", err)
		}
		if ok {
			rate = v
			break
		}
	}
	countRaw := raw.AssignmentsCount
	if len(bytes.TrimSpace(countRaw)) == 0 {
		countRaw = raw.AssignmentsCountSnake
	}
	count, err := parseFlexibleInt(countRaw)
	if err != nil {
		return fmt.Errorf("colabclient: invalid assignmentsCount: %w", err)
	}
	c.PaidComputeUnitsBalance = balance
	c.ConsumptionRateHourly = rate
	c.AssignmentsCount = count
	return nil
}

// ColabOneSessionState matches ColabOne's persisted runtime session schema
// (identical JSON keys to Python colab_cli.state.SessionState).
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

// String redacts Token so formatting or logging never leaks runtime proxy credentials.
func (s ColabOneSessionState) String() string {
	expiresAt := "<nil>"
	if s.TokenExpiresAt != nil {
		expiresAt = s.TokenExpiresAt.Format(time.RFC3339Nano)
	}
	return fmt.Sprintf(
		"ColabOneSessionState{Name:%q, Token:%q, URL:%q, Endpoint:%q, TokenExpiresAt:%s, Variant:%q, Accelerator:%q, MachineShape:%q, KernelID:%q, SessionID:%q}",
		s.Name,
		redactedTokenValue(s.Token),
		redactedProxyURL(s.URL),
		s.Endpoint,
		expiresAt,
		s.Variant,
		s.Accelerator,
		s.MachineShape,
		s.KernelID,
		s.SessionID,
	)
}

// GoString redacts Token when formatted with %#v.
func (s ColabOneSessionState) GoString() string {
	return s.String()
}

func redactedTokenValue(token string) string {
	if token == "" {
		return ""
	}
	return "[REDACTED]"
}

func redactedProxyURL(rawURL string) string {
	if containsProxyTokenParam(rawURL) {
		return redactProxyTokenURL(rawURL)
	}
	return rawURL
}

// UnmarshalJSON populates Python SessionState defaults (DEFAULT / NONE /
// STANDARD) when omitted and parses RFC3339, naive ISO-8601, or Unix timestamps
// for token_expires_at.
func (s *ColabOneSessionState) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name           string          `json:"name"`
		Token          string          `json:"token"`
		URL            string          `json:"url"`
		Endpoint       string          `json:"endpoint"`
		TokenExpiresAt json.RawMessage `json:"token_expires_at"`
		Variant        json.RawMessage `json:"variant"`
		Accelerator    string          `json:"accelerator"`
		MachineShape   json.RawMessage `json:"machine_shape"`
		KernelID       string          `json:"kernel_id"`
		SessionID      string          `json:"session_id"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	expiresAt, err := parseSessionTimestamp(raw.TokenExpiresAt)
	if err != nil {
		return fmt.Errorf("colabclient: invalid token_expires_at: %w", err)
	}
	variant, err := parseVariant(raw.Variant)
	if err != nil {
		return fmt.Errorf("colabclient: invalid session variant: %w", err)
	}
	if variant == "" {
		variant = VariantDefault
	}
	accel := strings.TrimSpace(raw.Accelerator)
	if accel == "" {
		accel = AcceleratorNone
	}
	shape, err := parseMachineShape(raw.MachineShape)
	if err != nil {
		return fmt.Errorf("colabclient: invalid session machine_shape: %w", err)
	}
	if shape == "" {
		shape = ShapeStandard
	}
	s.Name = raw.Name
	s.Token = raw.Token
	s.URL = raw.URL
	s.Endpoint = raw.Endpoint
	s.TokenExpiresAt = expiresAt
	s.Variant = variant
	s.Accelerator = accel
	s.MachineShape = shape
	s.KernelID = raw.KernelID
	s.SessionID = raw.SessionID
	return nil
}

func parseVariant(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return "", err
		}
		return normalizeVariantString(s), nil
	}
	var n int
	if err := json.Unmarshal(trimmed, &n); err == nil {
		return variantFromInt(n), nil
	}
	return "", fmt.Errorf("unsupported JSON value %s", string(trimmed))
}

func variantFromInt(n int) string {
	switch n {
	case 0:
		return VariantDefault
	case 1:
		return VariantGPU
	case 2:
		return VariantTPU
	default:
		return strconv.Itoa(n)
	}
}

func normalizeVariantString(s string) string {
	upper := strings.ToUpper(strings.TrimSpace(s))
	switch upper {
	case "":
		return ""
	case "0", "DEFAULT", "VARIANT_DEFAULT":
		return VariantDefault
	case "1", "GPU", "VARIANT_GPU":
		return VariantGPU
	case "2", "TPU", "VARIANT_TPU":
		return VariantTPU
	default:
		return upper
	}
}

func parseMachineShape(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return "", err
		}
		return normalizeMachineShapeString(s), nil
	}
	var n int
	if err := json.Unmarshal(trimmed, &n); err == nil {
		return machineShapeFromInt(n), nil
	}
	return "", fmt.Errorf("unsupported JSON value %s", string(trimmed))
}

func machineShapeFromInt(n int) string {
	switch n {
	case 0:
		return ShapeStandard
	case 1:
		return ShapeHighRAM
	default:
		return strconv.Itoa(n)
	}
}

func normalizeMachineShapeString(s string) string {
	upper := strings.ToUpper(strings.TrimSpace(s))
	switch upper {
	case "":
		return ""
	case "0", "STANDARD", "SHAPE_STANDARD":
		return ShapeStandard
	case "1", "HIGH_RAM", "SHAPE_HIGH_RAM", "HM", "HIGH-RAM", "HIGHMEM":
		return ShapeHighRAM
	default:
		return upper
	}
}

func parseFlexibleInt(raw json.RawMessage) (int, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return 0, err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0, err
		}
		return n, nil
	}
	var n int
	if err := json.Unmarshal(trimmed, &n); err == nil {
		return n, nil
	}
	var f float64
	if err := json.Unmarshal(trimmed, &f); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
		return int(f), nil
	}
	return 0, fmt.Errorf("unsupported integer value %s", string(trimmed))
}

func parseSessionTimestamp(raw json.RawMessage) (*time.Time, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, nil
		}
		layouts := []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02T15:04:05.999999999",
			"2006-01-02T15:04:05",
			"2006-01-02 15:04:05.999999999Z07:00",
			"2006-01-02 15:04:05Z07:00",
			"2006-01-02 15:04:05.999999999",
			"2006-01-02 15:04:05",
		}
		for _, layout := range layouts {
			if parsed, err := time.Parse(layout, s); err == nil {
				utc := parsed.UTC()
				return &utc, nil
			}
		}
		return nil, fmt.Errorf("cannot parse timestamp %q", s)
	}
	var sec float64
	if err := json.Unmarshal(trimmed, &sec); err == nil && !math.IsNaN(sec) && !math.IsInf(sec, 0) {
		whole, frac := math.Modf(sec)
		utc := time.Unix(int64(whole), int64(math.Round(frac*1e9))).UTC()
		return &utc, nil
	}
	return nil, fmt.Errorf("unsupported timestamp value %s", string(trimmed))
}
