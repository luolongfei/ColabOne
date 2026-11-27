package colabclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultColabRequestTimeout = 60 * time.Second
	maxColabResponseBytes      = 16 << 20 // 16 MiB
	maxColabErrorBodyBytes     = 1 << 20  // 1 MiB
)

// TokenProvider returns a valid Google OAuth access token for Colab requests.
type TokenProvider func(ctx context.Context) (string, error)

// Option configures a ColabOneColabClient instance.
type Option func(*colabClientConfig)

type colabClientConfig struct {
	colabDomain    string
	colabAPIDomain string
	httpClient     *http.Client
	timeout        time.Duration
	timeoutSet     bool
}

// WithColabDomain overrides the Colab tunnel origin (default DefaultColabDomain).
func WithColabDomain(domain string) Option {
	return func(cfg *colabClientConfig) {
		if trimmed := strings.TrimRight(strings.TrimSpace(domain), "/"); trimmed != "" {
			cfg.colabDomain = trimmed
		}
	}
}

// WithColabAPIDomain overrides the Colab API origin (default DefaultColabAPIDomain).
func WithColabAPIDomain(apiDomain string) Option {
	return func(cfg *colabClientConfig) {
		if trimmed := strings.TrimRight(strings.TrimSpace(apiDomain), "/"); trimmed != "" {
			cfg.colabAPIDomain = trimmed
		}
	}
}

// WithHTTPClient injects a custom *http.Client (e.g. an isolated proxy client
// for /tun/m/assign or an httptest client). The caller's client is not mutated.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(cfg *colabClientConfig) {
		cfg.httpClient = httpClient
	}
}

// WithTimeout configures the HTTP request timeout used by the client.
func WithTimeout(timeout time.Duration) Option {
	return func(cfg *colabClientConfig) {
		cfg.timeout = timeout
		cfg.timeoutSet = true
	}
}

// ColabOneColabClient communicates with Google Colab's /tun/m/* control plane.
// It is immutable after construction and safe for concurrent use.
type ColabOneColabClient struct {
	colabDomain    string
	colabDomainURL *url.URL
	colabAPIDomain string
	httpClient     *http.Client
	tokenProvider  TokenProvider
}

// NewColabOneColabClient creates a Colab control-plane HTTP client using
// tokenProvider to obtain OAuth Bearer tokens.
func NewColabOneColabClient(tokenProvider func(ctx context.Context) (string, error), opts ...Option) *ColabOneColabClient {
	cfg := colabClientConfig{
		colabDomain:    DefaultColabDomain,
		colabAPIDomain: DefaultColabAPIDomain,
		timeout:        defaultColabRequestTimeout,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	var httpClient *http.Client
	if cfg.httpClient != nil {
		if cfg.timeoutSet || cfg.httpClient.CheckRedirect == nil {
			copied := *cfg.httpClient
			if cfg.timeoutSet {
				copied.Timeout = cfg.timeout
			}
			if copied.CheckRedirect == nil {
				copied.CheckRedirect = refuseRedirects
			}
			httpClient = &copied
		} else {
			httpClient = cfg.httpClient
		}
	} else {
		httpClient = &http.Client{
			Timeout:       cfg.timeout,
			CheckRedirect: refuseRedirects,
		}
	}

	parsedDomain, _ := url.Parse(cfg.colabDomain)
	return &ColabOneColabClient{
		colabDomain:    cfg.colabDomain,
		colabDomainURL: parsedDomain,
		colabAPIDomain: cfg.colabAPIDomain,
		httpClient:     httpClient,
		tokenProvider:  tokenProvider,
	}
}

// ColabDomain returns the configured Google Colab origin without trailing slash.
func (c *ColabOneColabClient) ColabDomain() string {
	return c.colabDomain
}

// ColabAPIDomain returns the configured Google Colab API origin without trailing slash.
func (c *ColabOneColabClient) ColabAPIDomain() string {
	return c.colabAPIDomain
}

// DoAuthorized clones req with ctx, injects Authorization, Accept, and
// X-Colab-Client-Agent headers, adds authuser=0 for requests targeting
// ColabDomain, and executes the request via the configured http.Client.
//
// Callers are responsible for closing the returned response body.
func (c *ColabOneColabClient) DoAuthorized(ctx context.Context, req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("colabclient: request is nil")
	}
	if req.URL == nil {
		return nil, errors.New("colabclient: request URL is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if c.tokenProvider == nil {
		return nil, errors.New("colabclient: token provider is nil")
	}

	token, err := c.tokenProvider(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		var te interface{ IsTransportError() bool }
		if errors.As(err, &te) && te.IsTransportError() {
			return nil, err
		}
		var vse interface{ IsVaultStorageError() bool }
		if errors.As(err, &vse) && vse.IsVaultStorageError() {
			return nil, err
		}
		return nil, &ColabOneAuthError{Err: err}
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, &ColabOneAuthError{Err: errors.New("colabclient: empty OAuth access token")}
	}

	outReq := req.Clone(ctx)
	if outReq.Header == nil {
		outReq.Header = make(http.Header)
	}
	outReq.Header.Set("Accept", "application/json")
	outReq.Header.Set(HeaderColabClientAgent, ColabClientAgent)
	outReq.Header.Set("Authorization", "Bearer "+token)

	if sameOriginHost(outReq.URL, c.colabDomainURL) {
		q := outReq.URL.Query()
		q.Set("authuser", "0")
		outReq.URL.RawQuery = q.Encode()
	}

	return c.httpClient.Do(outReq)
}

// GetConsumptionUserInfo fetches account-level compute unit balance, hourly
// consumption rate, and active assignment count from GET /tun/m/ccu-info.
func (c *ColabOneColabClient) GetConsumptionUserInfo(ctx context.Context) (*ColabOneConsumptionInfo, error) {
	endpoint := c.colabDomain + TunEndpoint + "/ccu-info"
	var info ColabOneConsumptionInfo
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// ListAssignments returns all active runtime assignments from GET /tun/m/assignments.
func (c *ColabOneColabClient) ListAssignments(ctx context.Context) ([]ColabOneListedAssignment, error) {
	endpoint := c.colabDomain + TunEndpoint + "/assignments"
	var payload struct {
		Assignments []ColabOneListedAssignment `json:"assignments"`
	}
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &payload); err != nil {
		return nil, err
	}
	if payload.Assignments == nil {
		return []ColabOneListedAssignment{}, nil
	}
	return payload.Assignments, nil
}

// GetAssignment executes phase 1 (GET /tun/m/assign) of Colab's two-phase
// assignment protocol.
//
// If a runtime is already assigned for nbhUUID, alreadyAssigned is non-nil and
// xsrfToken is empty. Otherwise alreadyAssigned is nil and xsrfToken carries
// the XSRF token to submit to PostAssignment.
func (c *ColabOneColabClient) GetAssignment(
	ctx context.Context,
	nbhUUID, variant, accelerator, shape string,
) (alreadyAssigned *ColabOneAssignment, xsrfToken string, err error) {
	endpoint, err := c.buildAssignURL(nbhUUID, variant, accelerator, shape)
	if err != nil {
		return nil, "", err
	}
	var resp ColabOneGetAssignmentResponse
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &resp); err != nil {
		return nil, "", err
	}
	if resp.Endpoint != "" && resp.RuntimeProxyInfo != nil {
		accel := resp.Accelerator
		if accel == "" {
			accel = resp.Acc
		}
		return &ColabOneAssignment{
			Endpoint:         resp.Endpoint,
			Accelerator:      accel,
			Variant:          resp.Variant,
			RuntimeProxyInfo: *resp.RuntimeProxyInfo,
		}, "", nil
	}
	if strings.TrimSpace(resp.Token) == "" {
		return nil, "", errors.New("colabclient: GET /tun/m/assign response missing both assignment and XSRF token")
	}
	return nil, resp.Token, nil
}

// PostAssignment executes phase 2 (POST /tun/m/assign) using the XSRF token
// returned by GetAssignment. HTTP 412 responses are wrapped with ErrTooManyAssignments.
func (c *ColabOneColabClient) PostAssignment(
	ctx context.Context,
	nbhUUID, xsrfToken, variant, accelerator, shape string,
) (*ColabOneAssignment, error) {
	if strings.TrimSpace(xsrfToken) == "" {
		return nil, errors.New("colabclient: xsrfToken is required")
	}
	endpoint, err := c.buildAssignURL(nbhUUID, variant, accelerator, shape)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{
		HeaderColabXSRFToken: xsrfToken,
	}
	var assignment ColabOneAssignment
	if err := c.doJSON(ctx, http.MethodPost, endpoint, headers, &assignment); err != nil {
		var reqErr *ColabOneRequestError
		if errors.As(err, &reqErr) && reqErr.StatusCode == http.StatusPreconditionFailed {
			return nil, fmt.Errorf("%w: %w", ErrTooManyAssignments, reqErr)
		}
		return nil, err
	}
	if strings.TrimSpace(assignment.Endpoint) == "" {
		return nil, errors.New("colabclient: POST /tun/m/assign response missing endpoint")
	}
	return &assignment, nil
}

// Unassign releases a Colab runtime endpoint via the two-step XSRF flow:
// GET /tun/m/unassign/{endpoint} followed by POST with X-Goog-Colab-Token.
func (c *ColabOneColabClient) Unassign(ctx context.Context, endpoint string) error {
	ep := strings.TrimSpace(endpoint)
	if ep == "" {
		return errors.New("colabclient: endpoint is required")
	}
	target := c.colabDomain + TunEndpoint + "/unassign/" + url.PathEscape(ep)
	var tokenResp struct {
		Token string `json:"token"`
	}
	if err := c.doJSON(ctx, http.MethodGet, target, nil, &tokenResp); err != nil {
		return err
	}
	if strings.TrimSpace(tokenResp.Token) == "" {
		return errors.New("colabclient: GET /tun/m/unassign response missing XSRF token")
	}
	headers := map[string]string{
		HeaderColabXSRFToken: tokenResp.Token,
	}
	return c.doJSON(ctx, http.MethodPost, target, headers, nil)
}

// KeepAlive sends a Colab tunnel keep-alive ping to GET /tun/m/{endpoint}/keep-alive/
// with header X-Colab-Tunnel: Google and a bounded 10-second timeout.
func (c *ColabOneColabClient) KeepAlive(ctx context.Context, endpoint string) error {
	ep := strings.TrimSpace(endpoint)
	if ep == "" {
		return errors.New("colabclient: endpoint is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	kaCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	target := c.colabDomain + TunEndpoint + "/" + url.PathEscape(ep) + "/keep-alive/"
	headers := map[string]string{
		HeaderColabTunnel: "Google",
	}
	return c.doJSON(kaCtx, http.MethodGet, target, headers, nil)
}

func (c *ColabOneColabClient) buildAssignURL(nbhUUID, variant, accelerator, shape string) (string, error) {
	if strings.TrimSpace(nbhUUID) == "" {
		return "", errors.New("colabclient: nbhUUID is required")
	}
	base, err := url.Parse(c.colabDomain + TunEndpoint + "/assign")
	if err != nil {
		return "", err
	}
	q := base.Query()
	q.Set("nbh", UUIDToWebSafeBase64(nbhUUID))
	if v := strings.TrimSpace(variant); v != "" {
		q.Set("variant", v)
	}
	if acc := strings.TrimSpace(accelerator); acc != "" {
		q.Set("accelerator", acc)
	}
	if s := normalizeAssignShapeParam(shape); s != "" {
		q.Set("shape", s)
	}
	base.RawQuery = q.Encode()
	return base.String(), nil
}

func normalizeAssignShapeParam(shape string) string {
	trimmed := strings.TrimSpace(shape)
	if trimmed == "" {
		return ""
	}
	switch strings.ToUpper(trimmed) {
	case ShapeStandard, "0", "SHAPE_STANDARD":
		return ""
	case "HM", ShapeHighRAM, "1", "SHAPE_HIGH_RAM", "HIGH-RAM", "HIGHMEM":
		return AssignShapeHighMem
	default:
		return trimmed
	}
}

func (c *ColabOneColabClient) doJSON(
	ctx context.Context,
	method, rawURL string,
	headers map[string]string,
	dest any,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.DoAuthorized(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxColabErrorBodyBytes))
		reqURL := rawURL
		if resp.Request != nil && resp.Request.URL != nil {
			reqURL = resp.Request.URL.String()
		}
		return &ColabOneRequestError{
			StatusCode: resp.StatusCode,
			Method:     method,
			URL:        reqURL,
			Reason:     responseReason(resp),
			Body:       string(bodyBytes),
		}
	}

	if dest == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxColabResponseBytes))
		return nil
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxColabResponseBytes))
	if err != nil {
		return err
	}
	cleaned := bytes.TrimSpace(StripXSSIPrefix(bodyBytes))
	if len(cleaned) == 0 {
		return fmt.Errorf("colabclient: empty JSON response from %s %s", method, rawURL)
	}
	if err := json.Unmarshal(cleaned, dest); err != nil {
		return fmt.Errorf("colabclient: failed to decode response from %s %s: %w", method, rawURL, err)
	}
	return nil
}

func sameOriginHost(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	ah := strings.ToLower(strings.TrimSuffix(a.Hostname(), "."))
	bh := strings.ToLower(strings.TrimSuffix(b.Hostname(), "."))
	if ah == "" || ah != bh {
		return false
	}
	return effectivePort(a) == effectivePort(b)
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	default:
		return ""
	}
}

func refuseRedirects(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func responseReason(resp *http.Response) string {
	if resp == nil {
		return ""
	}
	if _, after, ok := strings.Cut(resp.Status, " "); ok && strings.TrimSpace(after) != "" {
		return strings.TrimSpace(after)
	}
	return http.StatusText(resp.StatusCode)
}
