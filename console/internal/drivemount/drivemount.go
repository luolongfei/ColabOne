// Package drivemount implements official Google Colab native DriveFS
// credentials propagation (`dfs_ephemeral`) for `google.colab.drive.mount`.
//
// Used as the zero-GCP-config automatic fallback whenever headless rclone
// cannot obtain a Drive-enabled GCP quota project (e.g., personal accounts
// that have not accepted GCP Terms of Service or Workspace orgs restricting
// project creation).
package drivemount

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// COLABONE_DRIVEFS_MOUNT_ROOT is the mount root passed to `google.colab.drive.mount`.
	COLABONE_DRIVEFS_MOUNT_ROOT = "/content/drive"
	// COLABONE_DRIVEFS_MYDRIVE is the mounted Google My Drive path inside the remote VM.
	COLABONE_DRIVEFS_MYDRIVE = "/content/drive/MyDrive"

	defaultColabDomain   = "https://colab.research.google.com"
	colabClientAgent     = "colab-cli"
	defaultTimeout       = 240 * time.Second
	minTimeout           = 15 * time.Second
	defaultPollInterval  = 2500 * time.Millisecond
	minPollInterval      = 50 * time.Millisecond
	colabRequestTimeout  = 15 * time.Second
	maxResponseBodyBytes = 4 << 20 // 4 MiB defensive cap
)

// AuthorizedHTTPClient performs authenticated HTTP requests against Google Colab endpoints.
type AuthorizedHTTPClient interface {
	ColabDomain() string
	DoAuthorized(ctx context.Context, req *http.Request) (*http.Response, error)
}

// ColabOneDriveMountOptions configures a ColabOneDriveMount helper.
type ColabOneDriveMountOptions struct {
	Client       AuthorizedHTTPClient
	Endpoint     string
	OnAuthURL    func(string)
	OnLog        func(msg, source, level string)
	OnHint       func(string)
	AccountEmail string
	Timeout      time.Duration
	PollInterval time.Duration
}

// ColabOneDriveMount handles `dfs_ephemeral` credentials propagation for `google.colab.drive.mount`.
type ColabOneDriveMount struct {
	client       AuthorizedHTTPClient
	endpoint     string
	onAuthURL    func(string)
	onLog        func(msg, source, level string)
	onHint       func(string)
	accountEmail string
	timeout      time.Duration
	pollInterval time.Duration

	mu          sync.Mutex
	cancelled   bool
	stopped     bool
	confirm     bool
	probe       bool
	hookStarted bool
	errMsg      string

	notifyCh  chan struct{}
	abortCh   chan struct{}
	abortOnce sync.Once
	wg        sync.WaitGroup

	now func() time.Time
}

// NewColabOneDriveMount constructs a new ColabOneDriveMount with validated defaults.
func NewColabOneDriveMount(opts ColabOneDriveMountOptions) *ColabOneDriveMount {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	} else if timeout < minTimeout {
		timeout = minTimeout
	}

	pollInterval := opts.PollInterval
	if pollInterval == 0 {
		pollInterval = defaultPollInterval
	} else if pollInterval < minPollInterval {
		pollInterval = minPollInterval
	}

	return &ColabOneDriveMount{
		client:       opts.Client,
		endpoint:     opts.Endpoint,
		onAuthURL:    opts.OnAuthURL,
		onLog:        opts.OnLog,
		onHint:       opts.OnHint,
		accountEmail: strings.TrimSpace(opts.AccountEmail),
		timeout:      timeout,
		pollInterval: pollInterval,
		notifyCh:     make(chan struct{}, 1),
		abortCh:      make(chan struct{}),
		now:          time.Now,
	}
}

type transportError struct {
	err error
}

func (e *transportError) Error() string { return e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

func isTransportError(err error) bool {
	var te *transportError
	return errors.As(err, &te)
}

// SignalCancel aborts an in-progress propagation as a user cancellation.
func (m *ColabOneDriveMount) SignalCancel() {
	m.mu.Lock()
	m.cancelled = true
	m.mu.Unlock()
	m.abortOnce.Do(func() { close(m.abortCh) })
	m.poke()
}

// SignalConfirm wakes propagation immediately for an explicit user "I've authorized" confirmation check.
func (m *ColabOneDriveMount) SignalConfirm() {
	m.mu.Lock()
	m.confirm = true
	m.mu.Unlock()
	m.poke()
}

// SignalProbe wakes propagation immediately for a silent background re-check (e.g. window focus).
func (m *ColabOneDriveMount) SignalProbe() {
	m.mu.Lock()
	m.probe = true
	m.mu.Unlock()
	m.poke()
}

// Stop terminates propagation, suppresses further UI callbacks during teardown,
// and waits for any background HandleColabRequest worker to finish.
func (m *ColabOneDriveMount) Stop() {
	m.mu.Lock()
	m.stopped = true
	m.mu.Unlock()
	m.abortOnce.Do(func() { close(m.abortCh) })
	m.poke()
	m.wg.Wait()
}

// Error returns the last recorded propagation error message, if any.
func (m *ColabOneDriveMount) Error() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.errMsg
}

func (m *ColabOneDriveMount) setError(msg string) {
	m.mu.Lock()
	m.errMsg = msg
	m.mu.Unlock()
}

func (m *ColabOneDriveMount) poke() {
	select {
	case m.notifyCh <- struct{}{}:
	default:
	}
}

func (m *ColabOneDriveMount) isStopped() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopped
}

func (m *ColabOneDriveMount) isStopOnly(ctx context.Context) bool {
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	m.mu.Lock()
	stopOnly := m.stopped && !m.cancelled
	m.mu.Unlock()
	return stopOnly
}

func (m *ColabOneDriveMount) isAborted(ctx context.Context) bool {
	if ctx != nil && ctx.Err() != nil {
		return true
	}
	m.mu.Lock()
	aborted := m.cancelled || m.stopped
	m.mu.Unlock()
	return aborted
}

func (m *ColabOneDriveMount) clearWakeEvents() {
	m.mu.Lock()
	m.confirm = false
	m.probe = false
	m.mu.Unlock()
}

func (m *ColabOneDriveMount) consumeWakeEvents() bool {
	m.mu.Lock()
	confirmed := m.confirm
	m.confirm = false
	m.probe = false
	m.mu.Unlock()
	return confirmed
}

func (m *ColabOneDriveMount) emitLog(message, source, level string) {
	if m.onLog == nil || message == "" {
		return
	}
	if source == "" {
		source = "云盘"
	}
	if level == "" {
		level = "info"
	}
	m.onLog(message, source, level)
}

func (m *ColabOneDriveMount) emitHint(text string) {
	if m.onHint == nil {
		return
	}
	m.onHint(text)
}

func (m *ColabOneDriveMount) endpointURL(dryrun bool) string {
	domain := ""
	if m.client != nil {
		domain = strings.TrimSpace(m.client.ColabDomain())
	}
	if domain == "" {
		domain = defaultColabDomain
	}
	base := strings.TrimRight(domain, "/") + "/tun/m/credentials-propagation/" + m.endpoint
	q := url.Values{}
	q.Set("authuser", "0")
	q.Set("authtype", "dfs_ephemeral")
	q.Set("version", "2")
	if dryrun {
		q.Set("dryrun", "true")
	} else {
		q.Set("dryrun", "false")
	}
	q.Set("propagate", "true")
	q.Set("record", "false")
	return base + "?" + q.Encode()
}

func (m *ColabOneDriveMount) notConfirmedHint() string {
	if m.accountEmail != "" {
		return fmt.Sprintf(
			"暂未检测到 %s 的云盘授权确认：请确认浏览器中选择了同一 Google 账号，并在授权页勾选全部云盘权限后重试",
			m.accountEmail,
		)
	}
	return "暂未检测到 Google 云盘授权确认：请确认浏览器中选择了与 ColabOne 相同的 Google 账号，并在授权页勾选全部云盘权限后重试"
}

func (m *ColabOneDriveMount) timeoutMessage() string {
	if m.accountEmail != "" {
		return fmt.Sprintf(
			"等待浏览器确认 %s 的 Google 云盘授权超时。请确认浏览器中登录了同一 Google 账号并在授权页勾选全部云盘权限后重试；本机登录仍保留",
			m.accountEmail,
		)
	}
	return "等待浏览器确认 Google 云盘授权超时。请确认浏览器中选择了与 ColabOne 相同的 Google 账号并勾选全部云盘权限后重试；本机登录仍保留"
}

// parseColabJSONLenient parses XSSI-prefixed or plain JSON objects defensively,
// returning an empty map on HTML, non-object JSON, or empty bodies.
func parseColabJSONLenient(body []byte) map[string]any {
	text := string(body)
	candidates := make([]string, 0, 3)
	candidates = append(candidates, text)
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		candidates = append(candidates, text[idx+1:])
	}
	if idx := strings.LastIndexByte(text, '\n'); idx >= 0 {
		candidates = append(candidates, text[idx+1:])
	}

	for _, candidate := range candidates {
		stripped := strings.TrimSpace(candidate)
		if stripped == "" {
			continue
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(stripped), &parsed); err != nil {
			continue
		}
		if parsed != nil {
			return parsed
		}
	}
	return map[string]any{}
}

func parseColabJSON(status int, body []byte) (map[string]any, error) {
	parsed := parseColabJSONLenient(body)
	if len(parsed) == 0 {
		return nil, fmt.Errorf("Google Colab 云盘授权接口返回异常状态（HTTP %d），请重试", status)
	}
	return parsed, nil
}

func validateConsentURL(rawURL string) (string, error) {
	uri := strings.TrimSpace(rawURL)
	if uri == "" {
		return "", nil
	}
	parsed, err := url.Parse(uri)
	if err != nil ||
		strings.ToLower(parsed.Scheme) != "https" ||
		strings.ToLower(parsed.Hostname()) != "accounts.google.com" ||
		parsed.User != nil {
		return "", errors.New("Google Colab 返回了未受信任的云盘授权地址，已中止")
	}
	return uri, nil
}

func stringField(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	v, ok := data[key]
	if !ok || v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case bool:
		if !val {
			return ""
		}
		return "true"
	case float64:
		if val == 0 {
			return ""
		}
		return fmt.Sprint(val)
	default:
		return fmt.Sprint(val)
	}
}

func boolFieldTrue(data map[string]any, key string) bool {
	if data == nil {
		return false
	}
	v, ok := data[key]
	if !ok {
		return false
	}
	b, isBool := v.(bool)
	return isBool && b
}

func boolFieldFalse(data map[string]any, key string) bool {
	if data == nil {
		return false
	}
	v, ok := data[key]
	if !ok {
		return false
	}
	b, isBool := v.(bool)
	return isBool && !b
}

func isTruthy(v any) bool {
	if v == nil {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return val != ""
	case float64:
		return val != 0
	case int:
		return val != 0
	default:
		return true
	}
}

func isPropagationSuccess(status int, data map[string]any) bool {
	if status != http.StatusOK || len(data) == 0 {
		return false
	}
	if v, ok := data["success"]; ok {
		b, isBool := v.(bool)
		return isBool && b
	}
	// Some responses omit `success` on HTTP 200 when no redirect challenge is present.
	return stringField(data, "unauthorized_redirect_uri") == ""
}

func isRetryableStatus(status int) bool {
	switch status {
	case http.StatusBadRequest,
		http.StatusForbidden,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func wrapClientError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	if ctx != nil && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return ctx.Err()
	}
	var te interface{ IsTransportError() bool }
	if errors.As(err, &te) && te.IsTransportError() {
		return &transportError{err: err}
	}
	var authErr interface{ IsPermanentAuthError() bool }
	if errors.As(err, &authErr) && authErr.IsPermanentAuthError() {
		return err
	}
	msg := err.Error()
	if strings.Contains(msg, "请先完成 Google 登录") ||
		strings.Contains(msg, "Google 授权") ||
		strings.Contains(msg, "请在设置中重新登录") {
		return err
	}
	return &transportError{err: err}
}

func (m *ColabOneDriveMount) doGET(ctx context.Context, dryrun bool) (int, []byte, error) {
	if m.client == nil {
		return 0, nil, &transportError{err: errors.New("authorized HTTP client is nil")}
	}
	reqCtx, cancel := context.WithTimeout(ctx, colabRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, m.endpointURL(dryrun), nil)
	if err != nil {
		return 0, nil, wrapClientError(ctx, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Colab-Client-Agent", colabClientAgent)

	resp, err := m.client.DoAuthorized(reqCtx, req)
	if err != nil {
		return 0, nil, wrapClientError(ctx, err)
	}
	if resp == nil {
		return 0, nil, &transportError{err: errors.New("empty HTTP response")}
	}
	var body []byte
	if resp.Body != nil {
		defer resp.Body.Close()
		body, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
		if err != nil {
			return 0, nil, wrapClientError(ctx, err)
		}
	}
	return resp.StatusCode, body, nil
}

func (m *ColabOneDriveMount) doPOST(ctx context.Context, token string, dryrun bool) (int, []byte, error) {
	if m.client == nil {
		return 0, nil, &transportError{err: errors.New("authorized HTTP client is nil")}
	}
	reqCtx, cancel := context.WithTimeout(ctx, colabRequestTimeout)
	defer cancel()

	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	if err := writer.WriteField("file_id", "empty.ipynb"); err != nil {
		return 0, nil, &transportError{err: err}
	}
	if err := writer.Close(); err != nil {
		return 0, nil, &transportError{err: err}
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, m.endpointURL(dryrun), &payload)
	if err != nil {
		return 0, nil, wrapClientError(ctx, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Colab-Client-Agent", colabClientAgent)
	req.Header.Set("X-Goog-Colab-Token", token)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := m.client.DoAuthorized(reqCtx, req)
	if err != nil {
		return 0, nil, wrapClientError(ctx, err)
	}
	if resp == nil {
		return 0, nil, &transportError{err: errors.New("empty HTTP response")}
	}
	var body []byte
	if resp.Body != nil {
		defer resp.Body.Close()
		body, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
		if err != nil {
			return 0, nil, wrapClientError(ctx, err)
		}
	}
	return resp.StatusCode, body, nil
}

func (m *ColabOneDriveMount) fetchXSRFToken(ctx context.Context, dryrun bool) (string, error) {
	status, body, err := m.doGET(ctx, dryrun)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("未取得 Google Colab 云盘连接许可（HTTP %d），请重试", status)
	}
	data, err := parseColabJSON(status, body)
	if err != nil {
		return "", err
	}
	token := stringField(data, "token")
	if token == "" {
		return "", errors.New("Google Colab 云盘授权握手缺少令牌，请重试")
	}
	return token, nil
}

// requestPropagation fetches a fresh XSRF token and POSTs credentials-propagation once.
func (m *ColabOneDriveMount) requestPropagation(ctx context.Context, dryrun bool) (int, map[string]any, error) {
	getStatus, getBody, err := m.doGET(ctx, dryrun)
	if err != nil {
		return 0, nil, err
	}
	if getStatus != http.StatusOK {
		return getStatus, parseColabJSONLenient(getBody), nil
	}
	getData := parseColabJSONLenient(getBody)
	token := stringField(getData, "token")
	if token == "" {
		return 0, nil, errors.New("Google Colab 云盘授权握手缺少令牌，请重试")
	}
	postStatus, postBody, err := m.doPOST(ctx, token, dryrun)
	if err != nil {
		return 0, nil, err
	}
	return postStatus, parseColabJSONLenient(postBody), nil
}

// waitForWakeup waits up to d, waking immediately on cancel, stop, context done,
// explicit confirm, or window-focus probe.
func (m *ColabOneDriveMount) waitForWakeup(ctx context.Context, d time.Duration) string {
	if d < 0 {
		d = 0
	}
	deadline := m.now().Add(d)
	timer := time.NewTimer(d)
	defer timer.Stop()

	for {
		if ctx != nil && ctx.Err() != nil {
			return "cancel"
		}
		m.mu.Lock()
		aborted := m.cancelled || m.stopped
		confirmed := m.confirm
		probed := m.probe
		m.mu.Unlock()

		if aborted {
			return "cancel"
		}
		if confirmed {
			return "confirm"
		}
		if probed {
			return "probe"
		}
		if !m.now().Before(deadline) {
			return "poll"
		}

		var ctxDone <-chan struct{}
		if ctx != nil {
			ctxDone = ctx.Done()
		}

		select {
		case <-ctxDone:
			return "cancel"
		case <-m.abortCh:
			return "cancel"
		case <-m.notifyCh:
			// Re-check flags under lock at top of loop.
		case <-timer.C:
			if ctx != nil && ctx.Err() != nil {
				return "cancel"
			}
			m.mu.Lock()
			aborted = m.cancelled || m.stopped
			confirmed = m.confirm
			probed = m.probe
			m.mu.Unlock()
			if aborted {
				return "cancel"
			}
			if confirmed {
				return "confirm"
			}
			if probed {
				return "probe"
			}
			return "poll"
		}
	}
}

// DryrunCheck performs a fast read-only check returning (authorized, consentURL, err) for this VM.
func (m *ColabOneDriveMount) DryrunCheck(ctx context.Context) (bool, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	token, err := m.fetchXSRFToken(ctx, true)
	if err != nil {
		if isTransportError(err) {
			return false, "", errors.New("检查 Google 云盘授权状态时网络异常，请检查网络或代理后重试")
		}
		return false, "", err
	}
	status, body, err := m.doPOST(ctx, token, true)
	if err != nil {
		if isTransportError(err) {
			return false, "", errors.New("检查 Google 云盘授权状态时网络异常，请检查网络或代理后重试")
		}
		return false, "", err
	}
	if status != http.StatusOK {
		return false, "", fmt.Errorf("检查 Google 云盘授权状态失败（HTTP %d），请重试", status)
	}
	data, err := parseColabJSON(status, body)
	if err != nil {
		return false, "", err
	}
	if isTruthy(data["success"]) {
		return true, "", nil
	}
	uri, err := validateConsentURL(stringField(data, "unauthorized_redirect_uri"))
	if err != nil {
		return false, "", err
	}
	return false, uri, nil
}

// Propagate ensures Colab DriveFS authorization for this VM and propagates credentials.
func (m *ColabOneDriveMount) Propagate(ctx context.Context) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	m.setError("")

	fail := func(msg string) (bool, error) {
		m.setError(msg)
		return false, errors.New(msg)
	}
	failCancel := func() (bool, error) {
		const msg = "已取消 Google 云盘挂载"
		if !m.isStopOnly(ctx) {
			m.setError(msg)
		}
		return false, errors.New(msg)
	}
	failErr := func(err error) (bool, error) {
		m.setError(err.Error())
		return false, err
	}

	deadline := m.now().Add(m.timeout)
	consentOpened := false
	consentURI := ""

	// Register cleanup defer first so it runs AFTER the abort-watcher cleanup.
	defer func() {
		if !m.isStopped() {
			m.emitHint("")
			if consentOpened && m.onAuthURL != nil {
				m.onAuthURL("")
			}
		}
	}()

	// Derive a cancellable request context wired to SignalCancel/Stop so in-flight
	// HTTP calls abort immediately rather than waiting for the 15s request timeout.
	opCtx, opCancel := context.WithCancel(ctx)
	stopAbortWatch := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-m.abortCh:
			opCancel()
		case <-opCtx.Done():
		case <-stopAbortWatch:
		}
	}()
	defer func() {
		close(stopAbortWatch)
		opCancel()
		<-watchDone
	}()

	// Phase 1: Single read-only dry-run check to see if already authorized or get consent URL.
	dryrunOK := false
	transientErrors := 0
	for {
		if m.isAborted(ctx) {
			return failCancel()
		}
		if !m.now().Before(deadline) {
			return fail(m.timeoutMessage())
		}
		status, data, err := m.requestPropagation(opCtx, true)
		if err != nil {
			if m.isAborted(ctx) {
				return failCancel()
			}
			if isTransportError(err) {
				transientErrors++
				if transientErrors > 4 {
					return fail("连接 Google Colab 云盘授权接口网络异常，请检查网络或代理后重试")
				}
				if m.waitForWakeup(ctx, minDuration(1500*time.Millisecond, m.pollInterval)) == "cancel" {
					return failCancel()
				}
				continue
			}
			return failErr(err)
		}
		if m.isAborted(ctx) {
			return failCancel()
		}

		if isRetryableStatus(status) {
			transientErrors++
			if transientErrors > 4 {
				return fail(fmt.Sprintf("Google 云盘授权确认失败（HTTP %d），请重试", status))
			}
			if m.waitForWakeup(ctx, minDuration(1500*time.Millisecond, m.pollInterval)) == "cancel" {
				return failCancel()
			}
			continue
		}
		if status != http.StatusOK {
			return fail(fmt.Sprintf("Google 云盘连接返回 HTTP %d，请重试", status))
		}

		if boolFieldTrue(data, "success") {
			dryrunOK = true
			break
		}

		validatedURI, err := validateConsentURL(stringField(data, "unauthorized_redirect_uri"))
		if err != nil {
			return failErr(err)
		}
		consentURI = validatedURI
		if consentURI == "" {
			return fail("Google 未接受本次云盘授权请求，请稍后重试；无需重新登录 Google")
		}
		break
	}

	// Phase 1b: Already authorized fast path — immediately propagate with a fresh dryrun=false token.
	if dryrunOK {
		m.emitLog("已收到云盘授权，正在将凭据同步到远端 GPU 实例", "云盘", "info")
		for attempt := 0; attempt < 2; attempt++ {
			if m.isAborted(ctx) {
				return failCancel()
			}
			status, resData, err := m.requestPropagation(opCtx, false)
			if err != nil {
				if m.isAborted(ctx) {
					return failCancel()
				}
				if isTransportError(err) {
					if attempt == 0 {
						if m.waitForWakeup(ctx, minDuration(1*time.Second, m.pollInterval)) == "cancel" {
							return failCancel()
						}
						continue
					}
					return fail("向远端实例同步云盘凭据时网络异常，请检查网络或代理后重试")
				}
				return failErr(err)
			}
			if m.isAborted(ctx) {
				return failCancel()
			}
			if isPropagationSuccess(status, resData) {
				m.emitLog("云盘凭据已同步至远端实例，正在完成 DriveFS 挂载", "云盘", "info")
				return true, nil
			}
			maybeURI, err := validateConsentURL(stringField(resData, "unauthorized_redirect_uri"))
			if err != nil {
				return failErr(err)
			}
			if maybeURI != "" {
				consentURI = maybeURI
				break
			}
			if isRetryableStatus(status) && attempt == 0 {
				continue
			}
			return fail(fmt.Sprintf("向远端实例同步云盘凭据失败（HTTP %d），请重试", status))
		}
		if consentURI == "" {
			return false, nil
		}
	}

	// Phase 2: Open browser consent and actively redeem via fresh dryrun=false calls.
	// Note: On Colab's backend, dryrun=true only checks already-propagated state /
	// returns the OAuth challenge URL; calling dryrun=false with a fresh XSRF token
	// is what actually redeems the browser's ephemeral consent into the VM.
	if m.isAborted(ctx) {
		return failCancel()
	}
	m.clearWakeEvents()
	if m.accountEmail != "" {
		m.emitLog(
			fmt.Sprintf("请在浏览器中使用 %s 勾选全部云盘权限并点击允许（仅本实例确认一次）", m.accountEmail),
			"云盘",
			"info",
		)
	} else {
		m.emitLog(
			"请在浏览器中勾选全部云盘权限并点击允许，以确认当前实例挂载 Google 云盘（仅本实例确认一次）",
			"云盘",
			"info",
		)
	}
	if m.isAborted(ctx) {
		return failCancel()
	}
	if m.onAuthURL != nil && !m.isAborted(ctx) {
		consentOpened = true
		m.onAuthURL(consentURI)
	}

	authErrors := 0
	for {
		if m.isAborted(ctx) {
			return failCancel()
		}
		remaining := deadline.Sub(m.now())
		if remaining <= 0 {
			return fail(m.timeoutMessage())
		}

		wake := m.waitForWakeup(ctx, minDuration(m.pollInterval, remaining))
		if wake == "cancel" {
			return failCancel()
		}
		hadConfirm := m.consumeWakeEvents()
		explicitConfirm := (wake == "confirm") || hadConfirm

		if m.isAborted(ctx) {
			return failCancel()
		}

		status, resData, err := m.requestPropagation(opCtx, false)
		if err != nil {
			if m.isAborted(ctx) {
				return failCancel()
			}
			if isTransportError(err) {
				if explicitConfirm {
					m.emitLog("核验云盘授权时网络暂时不可用，将自动重试", "云盘", "warning")
				}
				continue
			}
			return failErr(err)
		}
		if m.isAborted(ctx) {
			return failCancel()
		}

		if isPropagationSuccess(status, resData) {
			m.emitHint("")
			m.emitLog("云盘凭据已同步至远端实例，正在完成 DriveFS 挂载", "云盘", "info")
			return true, nil
		}

		// HTTP 400 or HTTP 200 with success=false means browser consent is not yet
		// recorded for this account (still reading consent page, unchecked granular
		// Drive scopes, or approved under a different browser Google account).
		if status == http.StatusBadRequest || (status == http.StatusOK && boolFieldFalse(resData, "success")) {
			authErrors = 0
			if explicitConfirm {
				hint := m.notConfirmedHint()
				m.emitHint(hint)
				m.emitLog(hint, "云盘", "warning")
			}
			continue
		}

		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			authErrors++
			if authErrors > 8 {
				return fail(fmt.Sprintf("Google 云盘授权确认失败（HTTP %d），请重试", status))
			}
			continue
		}

		if status == http.StatusInternalServerError ||
			status == http.StatusBadGateway ||
			status == http.StatusServiceUnavailable ||
			status == http.StatusGatewayTimeout {
			continue
		}

		return fail(fmt.Sprintf("向远端实例同步云盘凭据失败（HTTP %d），请重试", status))
	}
}

// HandleColabRequest intercepts Jupyter `colab_request` frames with `authType == "dfs_ephemeral"`,
// runs credentials propagation in a background goroutine so the WebSocket loop stays responsive,
// and replies with `{"type": "colab_reply", "colab_msg_id": msgID, ...}`.
func (m *ColabOneDriveMount) HandleColabRequest(
	ctx context.Context,
	msgType string,
	content map[string]any,
	metadata map[string]any,
	sendReply func(payload map[string]any) error,
) bool {
	if msgType != "" && msgType != "colab_request" {
		return false
	}
	if content == nil {
		return false
	}
	reqMap, ok := content["request"].(map[string]any)
	if !ok || reqMap == nil {
		return false
	}
	authType, _ := reqMap["authType"].(string)
	if authType != "dfs_ephemeral" {
		return false
	}

	var msgID any
	if metadata != nil {
		msgID = metadata["colab_msg_id"]
	}

	send := func(errMsg string) {
		if sendReply == nil {
			return
		}
		payload := map[string]any{
			"type":         "colab_reply",
			"colab_msg_id": msgID,
		}
		if errMsg != "" {
			payload["error"] = errMsg
		}
		_ = sendReply(payload)
	}

	m.mu.Lock()
	if m.hookStarted || m.stopped {
		m.mu.Unlock()
		return true
	}
	m.hookStarted = true
	m.wg.Add(1)
	m.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}

	go func() {
		defer m.wg.Done()
		_, err := m.Propagate(ctx)
		if err != nil {
			if m.isStopOnly(ctx) {
				return
			}
			m.setError(err.Error())
			send(err.Error())
			return
		}
		send("")
	}()

	return true
}
