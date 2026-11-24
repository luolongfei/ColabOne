package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luolongfei/ColabOne/console/internal/comfyui"
)

// ---------------------------------------------------------------------------
// Offline mock collaborators
// ---------------------------------------------------------------------------

type mockAuth struct {
	mu                   sync.Mutex
	locale               string
	loopbackActive       bool
	driveScope           bool
	requiresQuota        bool
	quotaStatus          map[string]any
	profile              map[string]any
	credErr              error
	beginResult          map[string]any
	loginFn              func(ctx context.Context, onAuthURL func(string)) error
	finishFn             func(ctx context.Context, code string) error
	ensureQuotaFn        func(ctx context.Context, forceRefresh bool, onLog LogFunc) (string, string, error)
	buildRcloneFn        func(ctx context.Context, quotaProject string) (string, error)
	invalidateQuotaCalls int
	credentialsCalls     int
	finishCalls          []string
}

func newMockAuth() *mockAuth {
	return &mockAuth{
		locale:  "zh-CN",
		profile: map[string]any{"sub": "user-sub-1", "email": "dev@example.com"},
		beginResult: map[string]any{
			"url": "https://accounts.google.com/o/oauth2/auth?test=1",
		},
	}
}

func (a *mockAuth) Locale() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.locale
}
func (a *mockAuth) SetLocale(loc string) {
	a.mu.Lock()
	a.locale = loc
	a.mu.Unlock()
}
func (a *mockAuth) Credentials(ctx context.Context, scope string) (any, error) {
	a.mu.Lock()
	a.credentialsCalls++
	err := a.credErr
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return map[string]any{"token": "valid-tok"}, nil
}
func (a *mockAuth) Profile(ctx context.Context) (map[string]any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return deepCopyMap(a.profile), nil
}
func (a *mockAuth) ColabBegin(ctx context.Context) (map[string]any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return deepCopyMap(a.beginResult), nil
}
func (a *mockAuth) ColabLogin(ctx context.Context, onAuthURL func(string)) error {
	a.mu.Lock()
	fn := a.loginFn
	a.mu.Unlock()
	if fn != nil {
		return fn(ctx, onAuthURL)
	}
	if onAuthURL != nil {
		onAuthURL("https://accounts.google.com/o/oauth2/auth?loopback=1")
	}
	return nil
}
func (a *mockAuth) ColabFinish(ctx context.Context, code string) error {
	a.mu.Lock()
	a.finishCalls = append(a.finishCalls, code)
	fn := a.finishFn
	a.mu.Unlock()
	if fn != nil {
		return fn(ctx, code)
	}
	return nil
}
func (a *mockAuth) LoopbackActive() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.loopbackActive
}
func (a *mockAuth) HasDriveScope() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.driveScope
}
func (a *mockAuth) RequiresDriveQuotaProject() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.requiresQuota
}
func (a *mockAuth) DriveQuotaStatus() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return deepCopyMap(a.quotaStatus)
}
func (a *mockAuth) EnsureDriveQuotaProject(ctx context.Context, forceRefresh bool, onLog LogFunc) (string, string, error) {
	a.mu.Lock()
	fn := a.ensureQuotaFn
	a.mu.Unlock()
	if fn != nil {
		return fn(ctx, forceRefresh, onLog)
	}
	return "", "not_required", nil
}
func (a *mockAuth) InvalidateDriveQuotaProject() {
	a.mu.Lock()
	a.invalidateQuotaCalls++
	a.mu.Unlock()
}
func (a *mockAuth) BuildRcloneConf(ctx context.Context, quotaProject string) (string, error) {
	a.mu.Lock()
	fn := a.buildRcloneFn
	a.mu.Unlock()
	if fn != nil {
		return fn(ctx, quotaProject)
	}
	return "", nil
}

type rpcRecord struct {
	Method   string
	Path     string
	Body     any
	Endpoint string
	Timeout  time.Duration
}

type mockProvider struct {
	mu             sync.Mutex
	vault          VaultStore
	accountFn      func(ctx context.Context) (map[string]any, error)
	rpcFn          func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error)
	startFn        func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error)
	upgradeFn      func(ctx context.Context, endpoint string, onLog LogFunc) error
	releaseFn      func(ctx context.Context, endpoint string, onLog LogFunc) (map[string]any, error)
	releaseAllFn   func(ctx context.Context, targets []string, notify func(string)) (map[string]any, error)
	heartbeatFn    func(ctx context.Context, endpoint string) error
	uploadDriveFn  func(ctx context.Context, rcloneConf string, endpoint string) error
	checkDriveFSFn func(ctx context.Context, endpoint string) (bool, string, error)
	mountDriveFSFn func(ctx context.Context, opts ColabOneDriveMountOptions) (map[string]any, error)
	observeFn      func(ctx context.Context, endpoint string) (map[string]any, error)
	setActiveFn    func(endpoint string) (string, error)

	rpcCalls       []rpcRecord
	heartbeatCalls []string
	closeObsCalls  []struct {
		ep       string
		shutdown bool
	}
	clearCalls      []string
	releaseCalls    []string
	releaseAllCalls [][]string
	upgradeCalls    []string
	savedRemotes    []map[string]any
	startOpts       []ColabOneStartOptions
}

func newMockProvider(vault VaultStore) *mockProvider {
	return &mockProvider{vault: vault}
}

func (p *mockProvider) Account(ctx context.Context) (map[string]any, error) {
	p.mu.Lock()
	fn := p.accountFn
	p.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return map[string]any{"logged_in": true, "balance": 100.0, "rate": 1.5, "assignments": 0, "instances": []map[string]any{}}, nil
}

func (p *mockProvider) RPC(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
	p.mu.Lock()
	p.rpcCalls = append(p.rpcCalls, rpcRecord{
		Method:   method,
		Path:     path,
		Body:     deepCopyAny(body),
		Endpoint: opts.Endpoint,
		Timeout:  opts.Timeout,
	})
	fn := p.rpcFn
	p.mu.Unlock()
	if fn != nil {
		return fn(ctx, method, path, body, opts)
	}
	if path == "snapshot" {
		return map[string]any{
			"runtime":       map[string]any{"state": "ready", "id": "ep-1", "protocol": 7, "tasks": []any{}},
			"models":        []any{},
			"workflows":     []any{},
			"comfy_outputs": []any{},
		}, nil
	}
	return map[string]any{"ok": true}, nil
}

func (p *mockProvider) Start(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
	p.mu.Lock()
	p.startOpts = append(p.startOpts, opts)
	fn := p.startFn
	p.mu.Unlock()
	if fn != nil {
		return fn(ctx, gpu, notify, opts)
	}
	if notify != nil {
		notify("部署远端管理服务")
	}
	return map[string]any{"endpoint": "ep-1"}, nil
}

func (p *mockProvider) UpgradeService(ctx context.Context, endpoint string, onLog LogFunc) error {
	p.mu.Lock()
	p.upgradeCalls = append(p.upgradeCalls, endpoint)
	fn := p.upgradeFn
	p.mu.Unlock()
	if fn != nil {
		return fn(ctx, endpoint, onLog)
	}
	return nil
}

func (p *mockProvider) Release(ctx context.Context, endpoint string, onLog LogFunc) (map[string]any, error) {
	p.mu.Lock()
	p.releaseCalls = append(p.releaseCalls, endpoint)
	fn := p.releaseFn
	p.mu.Unlock()
	if fn != nil {
		return fn(ctx, endpoint, onLog)
	}
	p.Clear(endpoint)
	return map[string]any{"ok": true}, nil
}

func (p *mockProvider) ReleaseAll(ctx context.Context, targets []string, notify func(string)) (map[string]any, error) {
	p.mu.Lock()
	cp := append([]string(nil), targets...)
	p.releaseAllCalls = append(p.releaseAllCalls, cp)
	fn := p.releaseAllFn
	p.mu.Unlock()
	if fn != nil {
		return fn(ctx, targets, notify)
	}
	for _, ep := range targets {
		p.Clear(ep)
	}
	return map[string]any{"ok": true, "remaining": 0}, nil
}

func (p *mockProvider) Heartbeat(ctx context.Context, endpoint string) error {
	p.mu.Lock()
	p.heartbeatCalls = append(p.heartbeatCalls, endpoint)
	fn := p.heartbeatFn
	p.mu.Unlock()
	if fn != nil {
		return fn(ctx, endpoint)
	}
	return nil
}

func (p *mockProvider) Clear(endpoint string) {
	p.mu.Lock()
	p.clearCalls = append(p.clearCalls, endpoint)
	v := p.vault
	p.mu.Unlock()
	if v == nil {
		return
	}
	if endpoint == "" {
		_ = v.Delete("runtime_session")
		_ = v.Delete("runtime_sessions")
		_ = v.Delete("runtime_session_ids")
		_ = v.Delete("remote")
		_ = v.Delete("remotes")
		_ = v.Delete("remote_ids")
		_ = v.Delete("active_endpoint")
		return
	}
	_ = v.Delete("runtime_session:" + endpoint)
	_ = v.Delete("remote:" + endpoint)
	if rawIDs, ok := v.Get("runtime_session_ids"); ok {
		var keep []any
		for _, item := range asAnySlice(rawIDs) {
			if asString(item) != endpoint {
				keep = append(keep, item)
			}
		}
		if len(keep) == 0 {
			_ = v.Delete("runtime_session_ids")
			_ = v.Delete("active_endpoint")
		} else {
			_ = v.Set("runtime_session_ids", keep)
			if cur, ok := v.Get("active_endpoint"); ok && asString(cur) == endpoint {
				_ = v.Set("active_endpoint", asString(keep[0]))
			}
		}
	}
	if rawIDs, ok := v.Get("remote_ids"); ok {
		var keep []any
		for _, item := range asAnySlice(rawIDs) {
			if asString(item) != endpoint {
				keep = append(keep, item)
			}
		}
		if len(keep) == 0 {
			_ = v.Delete("remote_ids")
		} else {
			_ = v.Set("remote_ids", keep)
		}
	}
	if leg, ok := v.Get("runtime_session"); ok && asString(asMap(leg)["endpoint"]) == endpoint {
		_ = v.Delete("runtime_session")
	}
	if leg, ok := v.Get("remote"); ok && asString(asMap(leg)["endpoint"]) == endpoint {
		_ = v.Delete("remote")
	}
}

func (p *mockProvider) SetActive(endpoint string) (string, error) {
	p.mu.Lock()
	fn := p.setActiveFn
	p.mu.Unlock()
	if fn != nil {
		return fn(endpoint)
	}
	if p.vault != nil {
		_ = p.vault.Set("active_endpoint", endpoint)
	}
	return endpoint, nil
}

func (p *mockProvider) SaveRemote(remote map[string]any, activate bool) error {
	p.mu.Lock()
	p.savedRemotes = append(p.savedRemotes, deepCopyMap(remote))
	v := p.vault
	p.mu.Unlock()
	if v != nil {
		ep := asString(remote["endpoint"])
		if ep != "" {
			_ = v.Set("remote:"+ep, remote)
			var ids []any
			seen := false
			if rawIDs, ok := v.Get("remote_ids"); ok {
				for _, item := range asAnySlice(rawIDs) {
					if asString(item) == ep {
						seen = true
					}
					ids = append(ids, item)
				}
			}
			if !seen {
				ids = append(ids, ep)
				_ = v.Set("remote_ids", ids)
			}
		}
		if activate && ep != "" {
			_ = v.Set("active_endpoint", ep)
		}
	}
	return nil
}

func (p *mockProvider) UploadDriveConfig(ctx context.Context, rcloneConf string, endpoint string) error {
	p.mu.Lock()
	fn := p.uploadDriveFn
	p.mu.Unlock()
	if fn != nil {
		return fn(ctx, rcloneConf, endpoint)
	}
	return nil
}

func (p *mockProvider) CheckColabDriveFSAuth(ctx context.Context, endpoint string) (bool, string, error) {
	p.mu.Lock()
	fn := p.checkDriveFSFn
	p.mu.Unlock()
	if fn != nil {
		return fn(ctx, endpoint)
	}
	return false, "", nil
}

func (p *mockProvider) MountColabDriveFS(ctx context.Context, opts ColabOneDriveMountOptions) (map[string]any, error) {
	p.mu.Lock()
	fn := p.mountDriveFSFn
	p.mu.Unlock()
	if fn != nil {
		return fn(ctx, opts)
	}
	return nil, nil
}

func (p *mockProvider) ObserveExternalTelemetry(ctx context.Context, endpoint string) (map[string]any, error) {
	p.mu.Lock()
	fn := p.observeFn
	p.mu.Unlock()
	if fn != nil {
		return fn(ctx, endpoint)
	}
	return nil, errors.New("observer not configured")
}

func (p *mockProvider) CloseObserver(endpoint string, shutdownKernel bool) {
	p.mu.Lock()
	p.closeObsCalls = append(p.closeObsCalls, struct {
		ep       string
		shutdown bool
	}{ep: endpoint, shutdown: shutdownKernel})
	p.mu.Unlock()
}

func (p *mockProvider) addManagedSession(ep, gpu, shape string, activate bool) {
	if p.vault == nil {
		return
	}
	_ = p.vault.Set("runtime_session:"+ep, map[string]any{
		"endpoint":      ep,
		"accelerator":   gpu,
		"variant":       "GPU",
		"machine_shape": shape,
		"url":           "https://" + ep + ".invalid",
	})
	var ids []any
	seen := false
	if rawIDs, ok := p.vault.Get("runtime_session_ids"); ok {
		for _, item := range asAnySlice(rawIDs) {
			if asString(item) == ep {
				seen = true
			}
			ids = append(ids, item)
		}
	}
	if !seen {
		ids = append(ids, ep)
		_ = p.vault.Set("runtime_session_ids", ids)
	}
	_ = p.SaveRemote(map[string]any{"endpoint": ep, "gpu": gpu, "deadline": 0}, activate)
}

type mockModels struct {
	resolveImportFn func(ctx context.Context, repo, filename, revision, token, customName string) (map[string]any, error)
}

func (m *mockModels) ValidateModelSpec(raw map[string]any) (map[string]any, error) {
	id := asTrimmedString(raw["id"])
	if !reModelID.MatchString(id) {
		return nil, errors.New("模型 ID 不合法")
	}
	if asTrimmedString(raw["name"]) == "" {
		return nil, errors.New("模型名称不能为空")
	}
	if len(asMapSlice(raw["files"])) == 0 {
		return nil, errors.New("缺少模型文件")
	}
	return deepCopyMap(raw), nil
}

func (m *mockModels) ValidateAPIModelID(value string, allowEmpty bool) (string, error) {
	s := strings.TrimSpace(value)
	if s == "" {
		if allowEmpty {
			return "", nil
		}
		return "", errors.New("模型 API ID 不能为空")
	}
	if strings.EqualFold(s, "colabone/auto") {
		return "", errors.New("colabone/auto 为系统保留别名，请使用其他模型 ID")
	}
	if strings.HasSuffix(s, "/") || strings.Contains(s, "..") {
		return "", errors.New("模型 API ID 格式不合法")
	}
	return s, nil
}

func (m *mockModels) ResolveAPIModelID(model map[string]any) string {
	if explicit := asTrimmedString(model["api_id"]); explicit != "" {
		return explicit
	}
	id := asTrimmedString(model["id"])
	if strings.HasPrefix(id, "hf-") {
		return "colabone/qwen3.8-flash-next"
	}
	return id
}

func (m *mockModels) UniqueAPIModelID(baseAPIID string, existingIDs []string, repo, filename string) (string, error) {
	taken := map[string]bool{"colabone/auto": true}
	for _, id := range existingIDs {
		taken[strings.ToLower(id)] = true
	}
	if !taken[strings.ToLower(baseAPIID)] {
		return baseAPIID, nil
	}
	if strings.Contains(strings.ToLower(filename), "q8") {
		cand := baseAPIID + "-q8"
		if !taken[strings.ToLower(cand)] {
			return cand, nil
		}
	}
	for i := 2; i < 100; i++ {
		cand := fmt.Sprintf("%s-%d", baseAPIID, i)
		if !taken[strings.ToLower(cand)] {
			return cand, nil
		}
	}
	return baseAPIID, nil
}

func (m *mockModels) ParseHFInput(repoOrURL, filename, revision string) (string, string, string) {
	raw := strings.TrimSpace(repoOrURL)
	fn := strings.TrimSpace(filename)
	rev := strings.TrimSpace(revision)
	if rev == "" {
		rev = "main"
	}
	for _, prefix := range []string{"https://huggingface.co/", "https://www.huggingface.co/", "http://huggingface.co/"} {
		if strings.HasPrefix(strings.ToLower(raw), prefix) {
			rest := raw[len(prefix):]
			rest = strings.SplitN(rest, "?", 2)[0]
			rest = strings.SplitN(rest, "#", 2)[0]
			rest = strings.Trim(rest, "/")
			parts := strings.Split(rest, "/")
			if len(parts) >= 2 {
				raw = parts[0] + "/" + parts[1]
				if len(parts) >= 4 && (parts[2] == "blob" || parts[2] == "resolve" || parts[2] == "tree") {
					if rev == "main" {
						rev = parts[3]
					}
					if len(parts) >= 5 && fn == "" {
						cand := strings.Join(parts[4:], "/")
						if strings.HasSuffix(strings.ToLower(cand), ".gguf") {
							fn = cand
						}
					}
				}
			}
			break
		}
	}
	return strings.Trim(raw, "/"), fn, rev
}

func (m *mockModels) ResolveImport(ctx context.Context, repo, filename, revision, token, customName string) (map[string]any, error) {
	if m.resolveImportFn != nil {
		return m.resolveImportFn(ctx, repo, filename, revision, token, customName)
	}
	r, fn, _ := m.ParseHFInput(repo, filename, revision)
	if !reRepoSlug.MatchString(r) || fn == "" {
		return nil, errors.New("无效的仓库或文件名")
	}
	name := strings.TrimSpace(customName)
	if name == "" {
		parts := strings.Split(r, "/")
		name = parts[len(parts)-1]
	}
	id := "hf-" + strings.ReplaceAll(strings.ToLower(fn), ".", "-")
	if len(id) > 40 {
		id = id[:40]
	}
	return map[string]any{
		"id":             id,
		"api_id":         "colabone/qwen3.8-flash-next",
		"name":           name,
		"description":    fn,
		"min_vram_mib":   0,
		"context_length": 8192,
		"temperature":    0.7,
		"cpu_moe_layers": 0,
		"source":         "Hugging Face",
		"kind":           "chat",
		"files": []map[string]any{
			{
				"repo":     r,
				"revision": strings.Repeat("e", 40),
				"path":     fn,
				"size":     4096,
				"sha256":   strings.Repeat("f", 64),
				"role":     "model",
			},
		},
	}, nil
}

type mockPublicAPI struct{}

func validCFToken() string {
	raw, _ := json.Marshal(map[string]string{"a": "account-1", "t": "tunnel-1", "s": "secret-1"})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func extractCFToken(raw string) (string, error) {
	for _, part := range strings.Fields(raw) {
		if strings.HasPrefix(part, "eyJ") && len(part) >= 20 {
			return part, nil
		}
	}
	return "", errors.New("invalid token")
}

func normalizeHTTPSDomain(raw string) (string, error) {
	d := strings.TrimRight(strings.TrimSpace(raw), "/")
	if d == "" {
		return "", errors.New("empty domain")
	}
	if !strings.HasPrefix(d, "https://") {
		d = "https://" + d
	}
	host := strings.TrimPrefix(d, "https://")
	if strings.Contains(host, "/") || !strings.Contains(host, ".") || strings.HasSuffix(host, ".trycloudflare.com") {
		return "", errors.New("invalid domain")
	}
	return "https://" + strings.ToLower(host), nil
}

func (mockPublicAPI) ValidatePublicConfig(raw map[string]any) (map[string]any, error) {
	dom, err := normalizeHTTPSDomain(asString(raw["domain"]))
	if err != nil {
		return nil, err
	}
	tok, err := extractCFToken(asString(raw["tunnel_token"]))
	if err != nil {
		return nil, err
	}
	key := asString(raw["api_key"])
	if !strings.HasPrefix(key, "co_") || len(key) < 43 {
		return nil, errors.New("invalid api key")
	}
	return map[string]any{
		"mode":         "named",
		"domain":       dom,
		"tunnel_token": tok,
		"api_key":      key,
	}, nil
}

type mockComfy struct{}

func (mockComfy) ValidateCatalog(raw map[string]any) ([]map[string]any, error) {
	return deepCopyMapSlice(asMapSlice(raw["templates"])), nil
}

func (mockComfy) ValidateWorkflowTemplate(raw map[string]any) (map[string]any, error) {
	id := asString(raw["id"])
	if !reModelID.MatchString(id) {
		return nil, errors.New("工作流 ID 不合法")
	}
	if asTrimmedString(raw["name"]) == "" {
		return nil, errors.New("工作流名称不能为空")
	}
	wf := asMap(raw["workflow"])
	if len(wf) == 0 {
		return nil, errors.New("工作流不能为空")
	}
	cp := deepCopyMap(raw)
	cp["node_count"] = len(wf)
	if _, ok := cp["fields"]; !ok {
		cp["fields"] = []any{}
	}
	return cp, nil
}

func (mockComfy) ValidateWorkflowImport(raw map[string]any) (map[string]any, error) {
	if asTrimmedString(raw["name"]) == "" {
		return nil, errors.New("工作流名称不能为空")
	}
	wf := asMap(raw["workflow"])
	if len(wf) == 0 {
		return nil, errors.New("工作流不能为空")
	}
	return map[string]any{
		"id":          asString(raw["id"]),
		"name":        asTrimmedString(raw["name"]),
		"description": asString(raw["description"]),
		"workflow":    deepCopyMap(wf),
	}, nil
}

func (mockComfy) ValidateWorkflowRun(raw map[string]any) (map[string]any, error) {
	wid := asString(raw["workflow_id"])
	if !reModelID.MatchString(wid) {
		return nil, errors.New("工作流 ID 不合法")
	}
	overrides := asMap(raw["overrides"])
	if overrides == nil {
		overrides = map[string]any{}
	}
	inputs := asMap(raw["inputs"])
	if inputs == nil {
		inputs = map[string]any{}
	}
	return map[string]any{
		"workflow_id": wid,
		"overrides":   overrides,
		"inputs":      inputs,
	}, nil
}

func (mockComfy) ValidateCanvasConfig(raw map[string]any) (map[string]any, error) {
	dom, err := normalizeHTTPSDomain(asString(raw["domain"]))
	if err != nil {
		return nil, err
	}
	tok, err := extractCFToken(asString(raw["tunnel_token"]))
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"mode":         "named",
		"domain":       dom,
		"tunnel_token": tok,
	}, nil
}

type mockAssignProxy struct {
	testResult map[string]any
	testErr    error
}

func (m *mockAssignProxy) ParseAssignProxy(raw string) (*ColabOneAssignProxySpec, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil, nil
	}
	idx := strings.Index(text, "://")
	if idx <= 0 {
		return nil, errors.New("代理地址请包含协议前缀")
	}
	scheme := strings.ToLower(text[:idx])
	if scheme != "http" && scheme != "https" && scheme != "socks5" && scheme != "socks5h" {
		return nil, errors.New("仅支持 http://、https://、socks5:// 或 socks5h:// 代理协议")
	}
	rest := text[idx+3:]
	var user, pass, hostPort string
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		cred := rest[:at]
		hostPort = rest[at+1:]
		if colon := strings.Index(cred, ":"); colon >= 0 {
			user = cred[:colon]
			pass = cred[colon+1:]
		} else {
			user = cred
		}
	} else {
		hostPort = rest
	}
	host := hostPort
	port := 1080
	if colon := strings.LastIndex(hostPort, ":"); colon >= 0 {
		host = hostPort[:colon]
		fmt.Sscanf(hostPort[colon+1:], "%d", &port)
	}
	schemeTag := strings.ToUpper(scheme)
	if scheme == "socks5" || scheme == "socks5h" {
		schemeTag = "SOCKS5"
	}
	return &ColabOneAssignProxySpec{
		Scheme:        scheme,
		Host:          host,
		Port:          port,
		Username:      user,
		Password:      pass,
		NormalizedURL: text,
		MaskedURL:     fmt.Sprintf("%s://%s:%d", scheme, host, port),
		LogLabel:      fmt.Sprintf("%s %s:%d", schemeTag, host, port),
		HasAuth:       user != "" || pass != "",
	}, nil
}

func (m *mockAssignProxy) NormalizeAssignProxy(raw string) (string, error) {
	spec, err := m.ParseAssignProxy(raw)
	if err != nil || spec == nil {
		return "", err
	}
	return spec.NormalizedURL, nil
}

func (m *mockAssignProxy) ProxyLogLabel(raw string) string {
	spec, err := m.ParseAssignProxy(raw)
	if err != nil || spec == nil {
		return ""
	}
	return spec.LogLabel
}

func (m *mockAssignProxy) TestAssignProxy(ctx context.Context, raw string) (map[string]any, error) {
	if m.testErr != nil {
		return nil, m.testErr
	}
	if m.testResult != nil {
		return deepCopyMap(m.testResult), nil
	}
	spec, err := m.ParseAssignProxy(raw)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "proxy": spec.LogLabel, "latency_ms": 42}, nil
}

type mockHub struct {
	searchFn func(ctx context.Context, query, sort, cursor, token, modality, family string) (map[string]any, error)
	filesFn  func(ctx context.Context, repo, token string) (map[string]any, error)
}

func (h *mockHub) SearchModels(ctx context.Context, query, sortBy, cursor, token, modality, family string) (map[string]any, error) {
	if h.searchFn != nil {
		return h.searchFn(ctx, query, sortBy, cursor, token, modality, family)
	}
	return map[string]any{
		"models":      []map[string]any{{"repo": "org/qwen-gguf", "downloads": 500}},
		"next_cursor": "cur-2",
	}, nil
}

func (h *mockHub) ModelFiles(ctx context.Context, repo, token string) (map[string]any, error) {
	if h.filesFn != nil {
		return h.filesFn(ctx, repo, token)
	}
	return map[string]any{
		"repo":  repo,
		"files": []map[string]any{{"filename": "qwen-q4.gguf", "size": 4096}},
	}, nil
}

type mockShortcuts struct {
	mu        sync.Mutex
	startMenu bool
	desktop   bool
}

func (s *mockShortcuts) Status(sandboxDir string) map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]bool{"start_menu": s.startMenu, "desktop": s.desktop}
}

func (s *mockShortcuts) Manage(root, target string, remove bool, sandboxDir, locale string) (map[string]bool, error) {
	if target != "start_menu" && target != "desktop" && target != "both" {
		return nil, errors.New("快捷方式目标不合法")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if target == "start_menu" || target == "both" {
		s.startMenu = !remove
	}
	if target == "desktop" || target == "both" {
		s.desktop = !remove
	}
	return map[string]bool{"start_menu": s.startMenu, "desktop": s.desktop}, nil
}

type mockLocation struct{}

func (mockLocation) InferLocationFromEndpoint(endpoint, url string) map[string]any {
	if strings.Contains(endpoint, "ass1") {
		return map[string]any{"country": "IN", "country_name": "印度", "region": "孟买", "zone": "asia-south1-a"}
	}
	if strings.Contains(endpoint, "usw1") {
		return map[string]any{"country": "US", "country_name": "美国", "region": "俄勒冈", "zone": "us-west1-b"}
	}
	return nil
}

type mockBundler struct {
	rev         string
	presetsJSON []byte
	comfyJSON   []byte
}

func defaultMockBundler() *mockBundler {
	presets, _ := json.Marshal([]map[string]any{
		{
			"id":             "gemma4-31b",
			"api_id":         "colabone/gemma-4-31b",
			"name":           "Gemma-4-31B",
			"description":    "31B / Q8_0",
			"min_vram_mib":   0,
			"context_length": 8192,
			"files": []map[string]any{
				{
					"repo":     "mradermacher/gemma-4-31B-GGUF",
					"revision": strings.Repeat("1", 40),
					"path":     "gemma.Q8_0.gguf",
					"size":     32635677600,
					"sha256":   strings.Repeat("2", 64),
					"role":     "model",
				},
			},
		},
	})
	comfy, _ := json.Marshal(map[string]any{
		"repo":       "https://github.com/Comfy-Org/ComfyUI.git",
		"tag":        "v0.46.0",
		"commit":     strings.Repeat("a", 40),
		"port":       8188,
		"proxy_port": 8189,
		"templates": []map[string]any{
			{
				"id":          "builtin-t2i",
				"name":        "Builtin T2I",
				"description": "builtin",
				"source":      "builtin",
				"workflow":    map[string]any{"1": map[string]any{"class_type": "CLIPTextEncode", "inputs": map[string]any{"text": "hi"}}},
			},
		},
	})
	return &mockBundler{
		rev:         "1234567890abcdef",
		presetsJSON: presets,
		comfyJSON:   comfy,
	}
}

func (b *mockBundler) ServiceRevision() string               { return b.rev }
func (b *mockBundler) ReadPresetsJSON() ([]byte, error)      { return b.presetsJSON, nil }
func (b *mockBundler) ReadComfyCatalogJSON() ([]byte, error) { return b.comfyJSON, nil }

type testHarness struct {
	ctrl        *ColabOneController
	vault       *ColabOneMemoryVault
	auth        *mockAuth
	provider    *mockProvider
	models      *mockModels
	hub         *mockHub
	assignProxy *mockAssignProxy
	shortcuts   *mockShortcuts
	bundler     *mockBundler
	configDir   string
}

func newTestHarness(t *testing.T, background bool) *testHarness {
	t.Helper()
	cfgDir := t.TempDir()
	vault := NewColabOneMemoryVault()
	auth := newMockAuth()
	provider := newMockProvider(vault)
	models := &mockModels{}
	hub := &mockHub{}
	proxy := &mockAssignProxy{}
	shortcuts := &mockShortcuts{}
	bundler := defaultMockBundler()

	ctrl := NewColabOneController(ColabOneControllerConfig{
		Root:               cfgDir,
		ConfigDir:          cfgDir,
		Background:         background,
		Vault:              vault,
		Auth:               auth,
		Provider:           provider,
		Hub:                hub,
		Models:             models,
		Comfy:              mockComfy{},
		PublicAPI:          mockPublicAPI{},
		AssignProxy:        proxy,
		Shortcuts:          shortcuts,
		ServiceBundler:     bundler,
		Location:           mockLocation{},
		MonitorInterval:    15 * time.Millisecond,
		ObserverInterval:   15 * time.Millisecond,
		CanvasWaitInterval: 10 * time.Millisecond,
		UpgradePollDelay:   10 * time.Millisecond,
		StartRetryDelay:    10 * time.Millisecond,
	})
	t.Cleanup(func() { ctrl.Close() })
	return &testHarness{
		ctrl:        ctrl,
		vault:       vault,
		auth:        auth,
		provider:    provider,
		models:      models,
		hub:         hub,
		assignProxy: proxy,
		shortcuts:   shortcuts,
		bundler:     bundler,
		configDir:   cfgDir,
	}
}

func waitUntil(t *testing.T, timeout time.Duration, pred func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pred() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !pred() {
		t.Fatal("condition not satisfied within timeout")
	}
}

func waitIdle(t *testing.T, ctrl *ColabOneController) {
	t.Helper()
	waitUntil(t, 3*time.Second, func() bool {
		return !ctrl.IsBusy() && ctrl.Snapshot()["operation"] == nil
	})
}

// ---------------------------------------------------------------------------
// 1. Core utilities: DefaultConfigDir, SafeMessage, SanitizeJSONValue, ActivityLog
// ---------------------------------------------------------------------------

func TestDefaultConfigDir(t *testing.T) {
	t.Setenv("COLABONE_CONFIG_DIR", "/tmp/custom-colabone-cfg")
	if got := DefaultConfigDir(); got != "/tmp/custom-colabone-cfg" {
		t.Fatalf("expected env override, got %q", got)
	}
	t.Setenv("COLABONE_CONFIG_DIR", "")
	t.Setenv("LOCALAPPDATA", "")
	got := DefaultConfigDir()
	if !strings.HasSuffix(got, "ColabOne") {
		t.Fatalf("expected ColabOne suffix, got %q", got)
	}
}

func TestSafeMessageRedaction(t *testing.T) {
	got := SafeMessage("Failed to issue request POST https://colab.research.google.com/tun/m/assign?nbh=abc: Service Unavailable")
	want := "Failed to issue request POST [链接已隐藏]: Service Unavailable"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	raw := "POST 'https://colab.example.com/tun/m/assign?authuser=0' failed; ws=\"wss://proxy.example.com/api/kernels/123/channels\" Bearer secret-b hf_secret_abc \"token\": \"ya29.super-secret\" refresh_token=1//09AbCdEfGhIjKlMnOpQrStUvWxYz0123456789 4/secret-authorization-code-long"
	cleaned := SafeMessage(raw)
	for _, secret := range []string{"colab.example.com", "proxy.example.com", "secret-b", "hf_secret_abc", "ya29.super-secret", "09AbCdEfGhIjKlMnOpQrStUvWxYz", "secret-authorization"} {
		if strings.Contains(cleaned, secret) {
			t.Fatalf("secret %q leaked in %q", secret, cleaned)
		}
	}
	if !strings.Contains(cleaned, "[链接已隐藏]") || !strings.Contains(cleaned, "Bearer [已隐藏]") {
		t.Fatalf("expected redaction markers in %q", cleaned)
	}
}

func TestSanitizeJSONValue(t *testing.T) {
	input := map[string]any{
		"ok":     1.5,
		"nan":    math.NaN(),
		"posinf": math.Inf(1),
		"neginf": math.Inf(-1),
		"nested": []any{float32(math.NaN()), map[string]any{"x": math.Inf(1), "y": 42}},
	}
	out := SanitizeJSONValue(input)
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("expected sanitized JSON to marshal cleanly, got error: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded["nan"] != nil || decoded["posinf"] != nil || decoded["neginf"] != nil {
		t.Fatalf("expected non-finite floats to become null, got %#v", decoded)
	}
	if decoded["ok"] != 1.5 {
		t.Fatalf("expected finite float preserved, got %#v", decoded["ok"])
	}
}

func TestActivityLogBoundedDiskAndRotation(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "activity.log")
	log := NewColabOneActivityLog(logPath, 2, 16*1024)
	log.Add("discarded", "", "")
	log.Add("Bearer super-secret https://example.com/?code=mycode hf_secret_abc", "GPU", "error")
	log.Add("refresh_token=secret-ref", "登录", "info")

	items := log.Snapshot()
	if len(items) != 2 || items[0].ID != 2 || items[1].ID != 3 {
		t.Fatalf("unexpected bounded items: %#v", items)
	}
	contentBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("expected activity.log on disk: %v", err)
	}
	content := string(contentBytes)
	if !strings.Contains(content, "[ERROR] [GPU]") || !strings.Contains(content, "[链接已隐藏]") {
		t.Fatalf("unexpected disk log content: %s", content)
	}
	for _, leaked := range []string{"super-secret", "mycode", "hf_secret_abc", "secret-ref"} {
		if strings.Contains(content, leaked) {
			t.Fatalf("secret %q leaked into activity.log", leaked)
		}
	}

	// Trigger rotation past 16 KiB.
	for i := 0; i < 120; i++ {
		log.Add(fmt.Sprintf("entry-%03d %s", i, strings.Repeat("x", 180)), "GPU", "info")
	}
	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Fatalf("expected rotated backup activity.log.1: %v", err)
	}
	log.Clear()
	if len(log.Snapshot()) != 0 {
		t.Fatal("expected cleared activity snapshot")
	}
}

// ---------------------------------------------------------------------------
// 2. Settings persistence, migrations, secrets isolation & assign proxy
// ---------------------------------------------------------------------------

func TestSettingsDefaultsMigrationsAndSecretIsolation(t *testing.T) {
	// Fresh install defaults to G4.
	freshDir := t.TempDir()
	freshCtrl := NewColabOneController(ColabOneControllerConfig{
		Root:           freshDir,
		ConfigDir:      freshDir,
		Vault:          NewColabOneMemoryVault(),
		ServiceBundler: defaultMockBundler(),
	})
	freshSettings := asMap(freshCtrl.Snapshot()["settings"])
	freshCtrl.Close()
	if freshSettings["gpu"] != "G4" {
		t.Fatalf("expected fresh default gpu G4, got %v", freshSettings["gpu"])
	}
	if _, hasPolicy := freshSettings["default_gpu_policy"]; hasPolicy {
		t.Fatalf("internal default_gpu_policy must not be exposed in snapshot settings")
	}

	// Legacy settings.json with explicit non-T4 GPU is preserved across upgrade.
	explicitDir := t.TempDir()
	explicitBytes, _ := json.Marshal(map[string]any{"gpu": "A100"})
	if err := os.WriteFile(filepath.Join(explicitDir, "settings.json"), explicitBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	explicitCtrl := NewColabOneController(ColabOneControllerConfig{
		Root:           explicitDir,
		ConfigDir:      explicitDir,
		Vault:          NewColabOneMemoryVault(),
		ServiceBundler: defaultMockBundler(),
	})
	explicitSettings := asMap(explicitCtrl.Snapshot()["settings"])
	explicitCtrl.Close()
	if explicitSettings["gpu"] != "A100" {
		t.Fatalf("expected explicit legacy gpu A100 preserved, got %v", explicitSettings["gpu"])
	}

	legacyDir := t.TempDir()
	legacySettings := map[string]any{
		"gpu":              "T4",
		"idle_minutes":     3,
		"idle_policy":      2,
		"download_threads": 8,
		"auto_drive":       false,
		"max_cu":           5,
	}
	b, _ := json.Marshal(legacySettings)
	if err := os.WriteFile(filepath.Join(legacyDir, "settings.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}

	vault := NewColabOneMemoryVault()
	ctrl := NewColabOneController(ColabOneControllerConfig{
		Root:           legacyDir,
		ConfigDir:      legacyDir,
		Vault:          vault,
		ServiceBundler: defaultMockBundler(),
	})
	defer ctrl.Close()

	snap := ctrl.Snapshot()
	settings := asMap(snap["settings"])
	if settings["gpu"] != "G4" {
		t.Fatalf("expected legacy default gpu T4 migrated to G4, got %v", settings["gpu"])
	}
	if v, _ := asInt(settings["idle_minutes"]); v != 30 {
		t.Fatalf("expected migrated idle_minutes=30, got %v", settings["idle_minutes"])
	}
	if v, _ := asInt(settings["download_threads"]); v != 16 {
		t.Fatalf("expected migrated download_threads=16, got %v", settings["download_threads"])
	}
	if settings["auto_drive"] != true {
		t.Fatalf("expected rclone_drive_policy migration auto_drive=true, got %v", settings["auto_drive"])
	}
	if _, hasMaxCU := settings["max_cu"]; hasMaxCU {
		t.Fatalf("unexpected legacy max_cu in settings: %#v", settings)
	}
	if _, hasPolicy := settings["idle_policy"]; hasPolicy {
		t.Fatalf("internal idle_policy must not be exposed in snapshot settings")
	}
	if _, hasGPUPolicy := settings["default_gpu_policy"]; hasGPUPolicy {
		t.Fatalf("internal default_gpu_policy must not be exposed in snapshot settings")
	}

	// Validation errors.
	if _, err := ctrl.Command(context.Background(), "settings.save", map[string]any{"download_threads": 8}); err == nil || !strings.Contains(err.Error(), "16 或 32") {
		t.Fatalf("expected 16 或 32 validation error, got %v", err)
	}
	if _, err := ctrl.Command(context.Background(), "settings.save", map[string]any{"idle_minutes": 0}); err == nil {
		t.Fatal("expected idle_minutes=0 to fail")
	}
	if _, err := ctrl.Command(context.Background(), "settings.save", map[string]any{"idle_minutes": true}); err == nil {
		t.Fatal("expected bool idle_minutes to fail")
	}
	if _, err := ctrl.Command(context.Background(), "settings.save", map[string]any{"gpu": "RTX9999"}); err == nil {
		t.Fatal("expected invalid gpu to fail")
	}

	// Save valid settings (including explicit T4 after migration) + HF token isolated from settings.json & snapshot.
	res, err := ctrl.Command(context.Background(), "settings.save", map[string]any{
		"gpu":              "T4",
		"idle_minutes":     45,
		"download_threads": 32,
		"hf_token":         "  hf_super_secret_token  ",
	})
	if err != nil || asMap(res)["ok"] != true {
		t.Fatalf("settings.save failed: %v %#v", err, res)
	}

	rawSettingsFile, _ := os.ReadFile(filepath.Join(legacyDir, "settings.json"))
	if strings.Contains(string(rawSettingsFile), "hf_super_secret_token") {
		t.Fatal("hf_token leaked into settings.json")
	}
	snapJSON, _ := json.Marshal(ctrl.Snapshot())
	if strings.Contains(string(snapJSON), "hf_super_secret_token") {
		t.Fatal("hf_token leaked into snapshot")
	}
	tokRes, err := ctrl.Command(context.Background(), "settings.hf_token", nil)
	if err != nil || asMap(tokRes)["token"] != "hf_super_secret_token" {
		t.Fatalf("unexpected settings.hf_token result: %v %#v", err, tokRes)
	}

	// Reload verifies persistence (including explicit T4 not overwritten back to G4).
	reloaded := NewColabOneController(ColabOneControllerConfig{
		Root:           legacyDir,
		ConfigDir:      legacyDir,
		Vault:          vault,
		ServiceBundler: defaultMockBundler(),
	})
	defer reloaded.Close()
	rSettings := asMap(reloaded.Snapshot()["settings"])
	if rSettings["gpu"] != "T4" {
		t.Fatalf("expected explicit post-migration gpu=T4 persisted, got %v", rSettings["gpu"])
	}
	if v, _ := asInt(rSettings["idle_minutes"]); v != 45 {
		t.Fatalf("expected persisted idle_minutes=45, got %v", rSettings["idle_minutes"])
	}
	if v, _ := asInt(rSettings["download_threads"]); v != 32 {
		t.Fatalf("expected persisted download_threads=32, got %v", rSettings["download_threads"])
	}
}

func TestAssignProxySettingsVaultIsolationTestAndStart(t *testing.T) {
	h := newTestHarness(t, false)
	h.ctrl.mu.Lock()
	h.ctrl.settings["auto_drive"] = false
	h.ctrl.mu.Unlock()

	// Cannot enable before saving proxy.
	if _, err := h.ctrl.Command(context.Background(), "settings.save", map[string]any{"assign_proxy_enabled": true}); err == nil || !strings.Contains(err.Error(), "请先填写并保存分配代理地址") {
		t.Fatalf("expected missing proxy error, got %v", err)
	}

	// Save authenticated proxy: vault-only, auto-enabled, masked in snapshot.
	if _, err := h.ctrl.Command(context.Background(), "settings.save", map[string]any{
		"assign_proxy": "socks5://alice:supersecret@127.0.0.1:1080",
	}); err != nil {
		t.Fatalf("save assign_proxy failed: %v", err)
	}
	settingsText, _ := os.ReadFile(filepath.Join(h.configDir, "settings.json"))
	if strings.Contains(string(settingsText), "supersecret") || strings.Contains(string(settingsText), "alice") {
		t.Fatal("assign_proxy credentials leaked into settings.json")
	}

	snap := h.ctrl.Snapshot()
	ps := asMap(snap["assign_proxy_settings"])
	if ps["enabled"] != true || ps["has_proxy"] != true || ps["masked_url"] != "socks5://127.0.0.1:1080" || ps["has_auth"] != true {
		t.Fatalf("unexpected assign_proxy_settings: %#v", ps)
	}
	snapJSON, _ := json.Marshal(snap)
	if strings.Contains(string(snapJSON), "supersecret") {
		t.Fatal("assign_proxy password leaked into snapshot")
	}

	rawRes, _ := h.ctrl.Command(context.Background(), "settings.assign_proxy", nil)
	if asMap(rawRes)["url"] != "socks5://alice:supersecret@127.0.0.1:1080" {
		t.Fatalf("unexpected settings.assign_proxy: %#v", rawRes)
	}

	testRes, err := h.ctrl.Command(context.Background(), "settings.assign_proxy.test", nil)
	if err != nil || asMap(testRes)["ok"] != true {
		t.Fatalf("assign_proxy.test failed: %v %#v", err, testRes)
	}

	// Start passes assign_proxy when enabled.
	if err := h.ctrl.Start(context.Background(), false, ""); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if len(h.provider.startOpts) == 0 || h.provider.startOpts[len(h.provider.startOpts)-1].AssignProxy != "socks5://alice:supersecret@127.0.0.1:1080" {
		t.Fatalf("expected assign_proxy forwarded to provider.Start, got %#v", h.provider.startOpts)
	}

	// Toggle off preserves vault entry, does not forward on Start.
	if _, err := h.ctrl.Command(context.Background(), "settings.save", map[string]any{"assign_proxy_enabled": false}); err != nil {
		t.Fatal(err)
	}
	if err := h.ctrl.Start(context.Background(), false, ""); err != nil {
		t.Fatal(err)
	}
	if h.provider.startOpts[len(h.provider.startOpts)-1].AssignProxy != "" {
		t.Fatal("expected empty assign_proxy when toggled off")
	}

	// Clear deletes from vault.
	if _, err := h.ctrl.Command(context.Background(), "settings.save", map[string]any{"clear_assign_proxy": true}); err != nil {
		t.Fatal(err)
	}
	if _, exists := h.vault.Get("assign_proxy"); exists {
		t.Fatal("expected assign_proxy deleted from vault")
	}
}

// ---------------------------------------------------------------------------
// 3. Library seeding, migrations, model library, API aliases
// ---------------------------------------------------------------------------

func TestLibrarySeedingLegacyMigrationCommonModelsAndRepos(t *testing.T) {
	h := newTestHarness(t, false)
	repos := asMapSlice(h.ctrl.Snapshot()["common_repos"])
	if len(repos) != 3 || repos[0]["repo"] != COLABONE_DEFAULT_COMMON_REPOS[0].Repo {
		t.Fatalf("expected 3 default common repos, got %#v", repos)
	}

	// Removing one persists across reload.
	if _, err := h.ctrl.Command(context.Background(), "common_repo.remove", map[string]any{"repo": COLABONE_DEFAULT_COMMON_REPOS[1].Repo}); err != nil {
		t.Fatal(err)
	}
	reloaded := NewColabOneController(ColabOneControllerConfig{
		Root:           h.configDir,
		ConfigDir:      h.configDir,
		Vault:          h.vault,
		Models:         h.models,
		ServiceBundler: h.bundler,
	})
	defer reloaded.Close()
	reRepos := asMapSlice(reloaded.Snapshot()["common_repos"])
	if len(reRepos) != 2 || reRepos[1]["repo"] != COLABONE_DEFAULT_COMMON_REPOS[2].Repo {
		t.Fatalf("expected removed default repo to stay removed, got %#v", reRepos)
	}

	// Legacy starred_ids + bookmarks + qwen25-05b migration.
	legacyDir := t.TempDir()
	libBytes, _ := json.Marshal(map[string]any{
		"starred_ids": []string{"qwen25-05b", "gemma4-31b"},
		"bookmarks":   []map[string]any{{"repo": "org/legacy-repo", "name": "Legacy Repo"}},
	})
	_ = os.WriteFile(filepath.Join(legacyDir, "library.json"), libBytes, 0o600)
	cLegacy := NewColabOneController(ColabOneControllerConfig{
		Root:           legacyDir,
		ConfigDir:      legacyDir,
		Models:         &mockModels{},
		ServiceBundler: defaultMockBundler(),
	})
	defer cLegacy.Close()
	snap := cLegacy.Snapshot()
	if cLegacy.commonIDs["qwen25-05b"] {
		t.Fatal("expected legacy qwen25-05b purged from common_ids")
	}
	foundGemma := false
	for _, m := range asMapSlice(snap["models"]) {
		if m["id"] == "gemma4-31b" && m["common"] == true && m["custom"] == false {
			foundGemma = true
		}
	}
	if !foundGemma {
		t.Fatalf("expected gemma4-31b common=true custom=false in %#v", snap["models"])
	}
	legacyRepos := asMapSlice(snap["common_repos"])
	if len(legacyRepos) != 4 || legacyRepos[0]["repo"] != "org/legacy-repo" {
		t.Fatalf("expected migrated bookmark + 3 defaults, got %#v", legacyRepos)
	}

	// Save + toggle common repo via HF URL.
	saveRes, err := h.ctrl.Command(context.Background(), "common_repo.save", map[string]any{
		"repo":      "https://huggingface.co/Qwen/Qwen2.5-7B-GGUF/blob/main/qwen2.5-7b-q4_k_m.gguf",
		"note":      "7B daily",
		"downloads": 1234,
		"likes":     56,
	})
	if err != nil || asMap(saveRes)["repo"] != "Qwen/Qwen2.5-7B-GGUF" || asMap(saveRes)["common"] != true {
		t.Fatalf("unexpected common_repo.save: %v %#v", err, saveRes)
	}
	toggleOff, err := h.ctrl.Command(context.Background(), "common_repo.save", map[string]any{
		"repo":   "https://huggingface.co/qwen/qwen2.5-7b-gguf",
		"toggle": true,
	})
	if err != nil || asMap(toggleOff)["common"] != false {
		t.Fatalf("expected toggle off, got %v %#v", err, toggleOff)
	}

	// Toggle preset common flag + reject invalid model ID.
	if res, err := h.ctrl.Command(context.Background(), "model.common", map[string]any{"id": "gemma4-31b"}); err != nil || asMap(res)["common"] != true {
		t.Fatalf("unexpected model.common toggle on: %v %#v", err, res)
	}
	if _, err := h.ctrl.Command(context.Background(), "model.common", map[string]any{"id": "../bad"}); err == nil {
		t.Fatal("expected invalid model ID error")
	}

	// Remove local model guards downloaded/loaded.
	spec, err := h.ctrl.ImportModel(context.Background(), map[string]any{"repo": "owner/repo", "filename": "qwen-q4.gguf"})
	if err != nil {
		t.Fatal(err)
	}
	specID := asString(spec["id"])
	h.ctrl.Update(map[string]any{
		"models": []map[string]any{
			{"id": specID, "name": "repo", "downloaded": true, "loaded": false, "files": spec["files"]},
		},
	})
	if _, err := h.ctrl.Command(context.Background(), "model.remove_local", map[string]any{"id": specID}); err == nil || !strings.Contains(err.Error(), "远端") {
		t.Fatalf("expected guard error while downloaded, got %v", err)
	}
	h.ctrl.Update(map[string]any{
		"models": []map[string]any{
			{"id": specID, "name": "repo", "downloaded": false, "loaded": false, "files": spec["files"]},
		},
	})
	if res, err := h.ctrl.Command(context.Background(), "model.remove_local", map[string]any{"id": specID}); err != nil || asMap(res)["ok"] != true {
		t.Fatalf("expected remove_local success, got %v %#v", err, res)
	}
}

func TestModelImportAPIIDDeduplicationRenameLegacyRemoteAndSync(t *testing.T) {
	h := newTestHarness(t, false)

	first, err := h.ctrl.ImportModel(context.Background(), map[string]any{
		"repo":     "huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
		"filename": "Qwen3.8-Flash-Next-UD-Q4_K_XL.gguf",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first["api_id"] != "colabone/qwen3.8-flash-next" {
		t.Fatalf("expected base api_id, got %v", first["api_id"])
	}

	second, err := h.ctrl.ImportModel(context.Background(), map[string]any{
		"repo":     "huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
		"filename": "Qwen3.8-Flash-Next-Q8_0.gguf",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second["api_id"] != "colabone/qwen3.8-flash-next-q8" {
		t.Fatalf("expected disambiguated -q8 api_id, got %v", second["api_id"])
	}

	// Duplicate rename and reserved colabone/auto rejected.
	if _, err := h.ctrl.Command(context.Background(), "model.set_api_id", map[string]any{
		"id":     second["id"],
		"api_id": "colabone/qwen3.8-flash-next",
	}); err == nil || !strings.Contains(err.Error(), "已被其他模型使用") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
	if _, err := h.ctrl.Command(context.Background(), "model.set_api_id", map[string]any{
		"id":     second["id"],
		"api_id": "colabone/auto",
	}); err == nil || !strings.Contains(err.Error(), "保留别名") {
		t.Fatalf("expected reserved alias error, got %v", err)
	}

	// Offline custom rename persists.
	renamed, err := h.ctrl.Command(context.Background(), "model.set_api_id", map[string]any{
		"id":     first["id"],
		"api_id": "colabone/qwen-fast",
	})
	if err != nil || asMap(renamed)["api_id"] != "colabone/qwen-fast" {
		t.Fatalf("rename failed: %v %#v", err, renamed)
	}

	// Legacy remote (upgrade_available=true, api_model_aliases=false): live api_id stays internal ID, pending_api_id exposed.
	legacyModel := deepCopyMap(first)
	delete(legacyModel, "api_id")
	legacyModel["downloaded"] = true
	legacyModel["loaded"] = true
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{"state": "ready", "id": "ep-legacy", "protocol": 7, "upgrade_available": true},
		"models":  []map[string]any{legacyModel},
	})
	snapLegacy := h.ctrl.Snapshot()
	for _, m := range asMapSlice(snapLegacy["models"]) {
		if m["id"] == first["id"] {
			if m["api_id"] != first["id"] || m["pending_api_id"] != "colabone/qwen-fast" {
				t.Fatalf("unexpected legacy model decoration: %#v", m)
			}
		}
	}
	if _, err := h.ctrl.Command(context.Background(), "model.set_api_id", map[string]any{
		"id":     first["id"],
		"api_id": "colabone/new",
	}); err == nil || !strings.Contains(err.Error(), "更新远端服务") {
		t.Fatalf("expected legacy upgrade error, got %v", err)
	}

	// Upgraded remote syncs live via POST models/{id}/api-id and reconciles overrides.
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{"state": "ready", "id": "ep-new", "protocol": 7, "api_model_aliases": true, "upgrade_available": false},
		"models":  []map[string]any{deepCopyMap(first)},
	})
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if path == "snapshot" {
			return map[string]any{
				"runtime": map[string]any{"state": "ready", "id": "ep-new", "protocol": 7, "api_model_aliases": true},
				"models":  []any{first},
			}, nil
		}
		return map[string]any{"ok": true}, nil
	}
	res, err := h.ctrl.Command(context.Background(), "model.set_api_id", map[string]any{
		"id":     first["id"],
		"api_id": "colabone/qwen-pro",
	})
	if err != nil || asMap(res)["api_id"] != "colabone/qwen-pro" {
		t.Fatalf("live set_api_id failed: %v %#v", err, res)
	}

	// postRemoteModel retries without api_id on 422 schema reject, but NOT on 409 conflict.
	attempts := 0
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		attempts++
		if attempts == 1 {
			return nil, &ColabOneHTTPError{StatusCode: 422, Message: "extra_forbidden: api_id"}
		}
		if _, hasAPIID := asMap(body)["api_id"]; hasAPIID {
			t.Fatal("expected legacy retry payload without api_id")
		}
		return map[string]any{"ok": true}, nil
	}
	if _, err := h.ctrl.postRemoteModel(context.Background(), second, ""); err != nil {
		t.Fatalf("expected 422 retry to succeed, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts on 422, got %d", attempts)
	}

	attempts = 0
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		attempts++
		return nil, errors.New("同名模型配置不同，请使用新模型 ID")
	}
	if _, err := h.ctrl.postRemoteModel(context.Background(), second, ""); err == nil || attempts != 1 {
		t.Fatalf("expected single attempt on conflict, attempts=%d err=%v", attempts, err)
	}
}

func TestHubSearchFilesInstallAndEnsureRemoteModel(t *testing.T) {
	h := newTestHarness(t, false)
	_ = h.vault.Set("hf", "hf_tok")

	searchRes, err := h.ctrl.Command(context.Background(), "hub.search", map[string]any{"query": "Qwen", "modality": "text"})
	if err != nil || asMap(searchRes)["next_cursor"] != "cur-2" {
		t.Fatalf("hub.search failed: %v %#v", err, searchRes)
	}

	filesRes, err := h.ctrl.Command(context.Background(), "hub.files", map[string]any{"repo": "https://huggingface.co/owner/repo"})
	if err != nil || asMap(filesRes)["repo"] != "owner/repo" {
		t.Fatalf("hub.files failed: %v %#v", err, filesRes)
	}

	// hub.install with download: false saves locally without remote RPC.
	if _, err := h.ctrl.Command(context.Background(), "hub.install", map[string]any{
		"repo":     "owner/repo",
		"filename": "qwen-q4.gguf",
		"name":     "Saved Hub Model",
		"download": false,
	}); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, h.ctrl)
	if len(h.provider.rpcCalls) != 0 {
		t.Fatalf("expected no remote RPCs for offline hub.install, got %#v", h.provider.rpcCalls)
	}

	// Simulate fresh ready runtime lacking the custom model -> model.download & model.load auto-sync POST models.
	var customID string
	for _, m := range asMapSlice(h.ctrl.Snapshot()["models"]) {
		if m["name"] == "Saved Hub Model" {
			customID = asString(m["id"])
		}
	}
	if customID == "" {
		t.Fatal("expected Saved Hub Model in snapshot")
	}
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{"state": "ready", "id": "ep-fresh"},
		"models":  h.ctrl.Presets(),
	})

	if _, err := h.ctrl.Command(context.Background(), "model.download", map[string]any{
		"id":             customID,
		"load":           true,
		"context_length": 16384,
		"temperature":    0.3,
	}); err != nil {
		t.Fatal(err)
	}
	if len(h.provider.rpcCalls) != 2 || h.provider.rpcCalls[0].Path != "models" || h.provider.rpcCalls[1].Path != "models/"+customID+"/download" {
		t.Fatalf("unexpected RPC sequence for model.download: %#v", h.provider.rpcCalls)
	}
	dlBody := asMap(h.provider.rpcCalls[1].Body)
	if dlBody["hf_token"] != "hf_tok" || dlBody["load"] != true {
		t.Fatalf("unexpected download body: %#v", dlBody)
	}

	if _, err := h.ctrl.Command(context.Background(), "model.load", map[string]any{"id": customID, "context_length": 8192}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ctrl.Command(context.Background(), "model.unload", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ctrl.Command(context.Background(), "model.delete", map[string]any{"id": customID}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ctrl.Command(context.Background(), "task.cancel", map[string]any{"id": strings.Repeat("a", 32)}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ctrl.Command(context.Background(), "task.cancel", map[string]any{"id": "bad-id"}); err == nil {
		t.Fatal("expected invalid task ID error")
	}
}

// ---------------------------------------------------------------------------
// 4. Snapshot exact JSON shape & activity deduplication
// ---------------------------------------------------------------------------

func TestSnapshotShapeAndActivityDeduplication(t *testing.T) {
	h := newTestHarness(t, false)
	snap, err := h.ctrl.Command(context.Background(), "snapshot", nil)
	if err != nil {
		t.Fatal(err)
	}
	snapMap := asMap(snap)
	requiredKeys := []string{
		"runtime", "account", "models", "workflows", "comfy_outputs",
		"common_repos", "operation", "error", "auth_url", "drive_auth_hint",
		"active_endpoint", "monitor", "settings", "assign_proxy_settings",
		"api_settings", "comfy_canvas_settings", "logs", "setup",
	}
	for _, k := range requiredKeys {
		if _, ok := snapMap[k]; !ok {
			t.Fatalf("missing required snapshot key %q", k)
		}
	}

	// Verify activity deduplication on repeated polls + incremental logs + idle countdown.
	h.ctrl.Update(map[string]any{"operation": map[string]any{"title": "部署远端管理服务", "running": true}})
	h.ctrl.Update(map[string]any{"operation": map[string]any{"title": "部署远端管理服务", "running": true}})
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state": "ready",
			"public_api": map[string]any{
				"logs": []map[string]any{{"seq": 1, "message": "API tunnel ready", "level": "info"}},
			},
			"comfyui": map[string]any{
				"canvas": map[string]any{
					"logs": []map[string]any{{"seq": 1, "message": "Canvas ready", "level": "info"}},
				},
			},
			"idle": map[string]any{"phase": "countdown", "remaining_seconds": 59.6},
			"tasks": []map[string]any{
				{
					"id":       "t1",
					"kind":     "download",
					"state":    "running",
					"progress": 0.21,
					"message":  "model.gguf",
					"logs":     []map[string]any{{"seq": 1, "message": "8 个连接 · 120 MiB/s", "level": "info"}},
				},
			},
		},
	})
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state": "ready",
			"tasks": []map[string]any{
				{
					"id":       "t1",
					"kind":     "download",
					"state":    "running",
					"progress": 0.22,
					"message":  "model.gguf",
					"logs": []map[string]any{
						{"seq": 1, "message": "8 个连接 · 120 MiB/s", "level": "info"},
						{"seq": 2, "message": "校验 SHA-256", "level": "info"},
					},
				},
			},
		},
	})
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state": "ready",
			"tasks": []map[string]any{
				{
					"id":       "t1",
					"kind":     "download",
					"state":    "failed",
					"progress": 0.22,
					"message":  "model.gguf",
					"error":    "下载校验失败",
				},
			},
		},
	})

	entries := h.ctrl.activity.Snapshot()
	deployCount, pct20Count, speedCount, countdownCount := 0, 0, 0, 0
	for _, e := range entries {
		if e.Message == "部署远端管理服务" {
			deployCount++
		}
		if strings.Contains(e.Message, "20%") && e.Level == "info" {
			pct20Count++
		}
		if e.Message == "8 个连接 · 120 MiB/s" {
			speedCount++
		}
		if strings.Contains(e.Message, "空闲自动释放倒计时剩余 60 秒") && e.Level == "warning" {
			countdownCount++
		}
	}
	if deployCount != 1 || pct20Count != 1 || speedCount != 1 || countdownCount != 1 {
		t.Fatalf("deduplication mismatch: deploy=%d pct20=%d speed=%d countdown=%d", deployCount, pct20Count, speedCount, countdownCount)
	}
	if last := entries[len(entries)-1]; last.Level != "error" || !strings.Contains(last.Message, "校验失败") {
		t.Fatalf("expected final failed task log, got %#v", last)
	}

	// error.clear respects expected value.
	h.ctrl.Update(map[string]any{"error": "new error"})
	_, _ = h.ctrl.Command(context.Background(), "error.clear", map[string]any{"expected": "older error"})
	if h.ctrl.Snapshot()["error"] != "new error" {
		t.Fatal("error.clear should not clear mismatched expected error")
	}
	_, _ = h.ctrl.Command(context.Background(), "error.clear", map[string]any{"expected": "new error"})
	if h.ctrl.Snapshot()["error"] != "" {
		t.Fatal("error.clear should clear matching expected error")
	}

	// logs.clear empties activity log.
	_, _ = h.ctrl.Command(context.Background(), "logs.clear", nil)
	if len(h.ctrl.activity.Snapshot()) != 0 {
		t.Fatal("expected logs cleared")
	}
}

// ---------------------------------------------------------------------------
// 5. Public API & ComfyUI commands
// ---------------------------------------------------------------------------

func TestPublicAPIVaultIsolationRotateStartStop(t *testing.T) {
	h := newTestHarness(t, false)
	tok := validCFToken()

	// Legacy quick mode in vault normalized to named; saving quick rejected.
	_ = h.vault.Set("public_api", map[string]any{"mode": "quick"})
	if asMap(h.ctrl.Snapshot()["api_settings"])["mode"] != "named" {
		t.Fatal("expected legacy quick mode normalized to named")
	}
	if _, err := h.ctrl.Command(context.Background(), "api.save", map[string]any{"mode": "quick"}); err == nil || !strings.Contains(err.Error(), "临时域名") {
		t.Fatalf("expected quick rejection, got %v", err)
	}

	// Save named API config.
	if _, err := h.ctrl.Command(context.Background(), "api.save", map[string]any{
		"domain":       "api.example.com",
		"tunnel_token": "cloudflared service install " + tok,
	}); err != nil {
		t.Fatalf("api.save failed: %v", err)
	}
	keyRes, _ := h.ctrl.Command(context.Background(), "api.key", nil)
	key := asString(asMap(keyRes)["key"])
	if !strings.HasPrefix(key, "co_") {
		t.Fatalf("expected co_ key, got %q", key)
	}
	tokRes, _ := h.ctrl.Command(context.Background(), "api.tunnel_token", nil)
	if asMap(tokRes)["token"] != tok {
		t.Fatalf("expected tunnel token %q, got %#v", tok, tokRes)
	}
	snapJSON, _ := json.Marshal(h.ctrl.Snapshot())
	if strings.Contains(string(snapJSON), key) || strings.Contains(string(snapJSON), tok) {
		t.Fatal("Public API secrets leaked in snapshot")
	}

	// api.rotate fails closed when stopping remote gateway errors.
	h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		return nil, errors.New("offline")
	}
	if _, err := h.ctrl.Command(context.Background(), "api.rotate", nil); err == nil {
		t.Fatal("expected api.rotate to fail when remote stop fails")
	}
	keyAfterFail, _ := h.ctrl.Command(context.Background(), "api.key", nil)
	if asMap(keyAfterFail)["key"] != key {
		t.Fatal("api key must not rotate when remote stop failed")
	}

	// api.rotate succeeds when remote stop succeeds.
	h.provider.rpcFn = nil
	if _, err := h.ctrl.Command(context.Background(), "api.rotate", nil); err != nil {
		t.Fatalf("api.rotate failed: %v", err)
	}
	keyAfterOK, _ := h.ctrl.Command(context.Background(), "api.key", nil)
	if asMap(keyAfterOK)["key"] == key {
		t.Fatal("expected api key to rotate")
	}

	// api.start & api.stop async operations.
	if _, err := h.ctrl.Command(context.Background(), "api.start", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, h.ctrl)
	if _, err := h.ctrl.Command(context.Background(), "api.stop", nil); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, h.ctrl)
}

func TestComfyUIOfflineOnlineWorkflowsOutputsAndCanvas(t *testing.T) {
	h := newTestHarness(t, false)
	tok := validCFToken()
	wfGraph := map[string]any{
		"1": map[string]any{"class_type": "CLIPTextEncode", "inputs": map[string]any{"text": "hello"}},
	}

	// Offline workflow import persists to workflows.json without RPC.
	imported, err := h.ctrl.Command(context.Background(), "comfyui.workflow.import", map[string]any{
		"id":          "wf-offline-1",
		"name":        "离线工作流",
		"description": "无需 GPU",
		"workflow":    wfGraph,
	})
	if err != nil || asMap(imported)["id"] != "wf-offline-1" {
		t.Fatalf("offline workflow import failed: %v %#v", err, imported)
	}
	if len(h.provider.rpcCalls) != 0 {
		t.Fatal("offline workflow import must not call remote RPC")
	}
	if _, err := os.Stat(filepath.Join(h.configDir, "workflows.json")); err != nil {
		t.Fatalf("expected workflows.json persisted: %v", err)
	}

	// Canvas save & vault isolation.
	if _, err := h.ctrl.Command(context.Background(), "comfyui.canvas.save", map[string]any{"mode": "quick"}); err == nil {
		t.Fatal("expected quick canvas mode rejected")
	}
	if _, err := h.ctrl.Command(context.Background(), "comfyui.canvas.save", map[string]any{
		"domain":       "comfy.example.com",
		"tunnel_token": "cloudflared service install " + tok,
	}); err != nil {
		t.Fatal(err)
	}
	snapJSON, _ := json.Marshal(h.ctrl.Snapshot())
	if strings.Contains(string(snapJSON), tok) {
		t.Fatal("canvas tunnel token leaked in snapshot")
	}
	if ct, _ := h.ctrl.Command(context.Background(), "comfyui.canvas.tunnel_token", nil); asMap(ct)["token"] != tok {
		t.Fatalf("unexpected canvas token: %#v", ct)
	}

	// Online comfyui.run auto-syncs offline custom workflow first.
	h.ctrl.Update(map[string]any{"runtime": map[string]any{"state": "ready", "id": "ep-comfy"}})
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if path == "comfyui/workflows" {
			return imported, nil
		}
		if path == "snapshot" {
			return map[string]any{
				"runtime":       map[string]any{"state": "ready", "id": "ep-comfy", "protocol": 7},
				"models":        []any{},
				"workflows":     []any{imported},
				"comfy_outputs": []any{},
			}, nil
		}
		return map[string]any{"ok": true, "id": "task-1"}, nil
	}
	if _, err := h.ctrl.Command(context.Background(), "comfyui.run", map[string]any{
		"workflow_id": "wf-offline-1",
		"overrides":   map[string]any{"1.text": "updated"},
		"inputs":      map[string]any{},
	}); err != nil {
		t.Fatalf("comfyui.run failed: %v", err)
	}
	if len(h.provider.rpcCalls) < 2 || h.provider.rpcCalls[0].Path != "comfyui/workflows" || h.provider.rpcCalls[1].Path != "comfyui/run" {
		t.Fatalf("expected workflow sync before run, got %#v", h.provider.rpcCalls)
	}

	// Other ComfyUI commands: start, free, stop, canvas start/stop/url, input upload, output asset/delete.
	if _, err := h.ctrl.Command(context.Background(), "comfyui.start", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ctrl.Command(context.Background(), "comfyui.free", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ctrl.Command(context.Background(), "comfyui.canvas.start", nil); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, h.ctrl)
	if _, err := h.ctrl.Command(context.Background(), "comfyui.canvas.url", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ctrl.Command(context.Background(), "comfyui.canvas.stop", nil); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, h.ctrl)
	if _, err := h.ctrl.Command(context.Background(), "comfyui.stop", nil); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, h.ctrl)
	if _, err := h.ctrl.Command(context.Background(), "comfyui.input.upload", map[string]any{"filename": "ref.png", "data": "abc"}); err != nil {
		t.Fatal(err)
	}
	oid := strings.Repeat("b", 32)
	if _, err := h.ctrl.Command(context.Background(), "comfyui.output.asset", map[string]any{"id": oid, "offset": 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ctrl.Command(context.Background(), "comfyui.output.asset", map[string]any{"id": oid, "offset": true}); err == nil {
		t.Fatal("expected bool offset rejected")
	}
	if _, err := h.ctrl.Command(context.Background(), "comfyui.output.delete", map[string]any{"id": oid}); err != nil {
		t.Fatal(err)
	}

	// Offline workflow delete.
	h.ctrl.Update(map[string]any{"runtime": map[string]any{"state": "stopped"}})
	if _, err := h.ctrl.Command(context.Background(), "comfyui.workflow.delete", map[string]any{"id": "wf-offline-1"}); err != nil {
		t.Fatal(err)
	}
	rawWFs, _ := os.ReadFile(filepath.Join(h.configDir, "workflows.json"))
	if strings.TrimSpace(string(rawWFs)) != "[]" {
		t.Fatalf("expected empty workflows.json after delete, got %s", string(rawWFs))
	}
}

// ---------------------------------------------------------------------------
// 6. Drive dual-mode, fallback, setup_headless, cancel & sync
// ---------------------------------------------------------------------------

func TestDriveDualModeHeadlessFallbackSetupAndCancel(t *testing.T) {
	h := newTestHarness(t, false)
	_ = h.vault.Set("colab", map[string]any{"token": "tok", "refresh_token": "rtok"})
	h.auth.driveScope = true
	h.auth.requiresQuota = true
	h.auth.quotaStatus = map[string]any{"reason": "gcp_tos_required"}
	var seenForce []bool
	h.auth.ensureQuotaFn = func(ctx context.Context, forceRefresh bool, onLog LogFunc) (string, string, error) {
		seenForce = append(seenForce, forceRefresh)
		return "", "gcp_tos_required", nil
	}

	// 1) drive.setup_headless surfaces actionable ToS error via async operation.
	if _, err := h.ctrl.Command(context.Background(), "drive.setup_headless", nil); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, h.ctrl)
	snap := h.ctrl.Snapshot()
	if !strings.Contains(asString(snap["error"]), "Google Cloud") {
		t.Fatalf("expected ToS error message, got %q", snap["error"])
	}
	if asMap(snap["setup"])["drive_headless"] != false || asMap(snap["setup"])["drive_quota_reason"] != "gcp_tos_required" {
		t.Fatalf("unexpected setup flags: %#v", snap["setup"])
	}

	// 2) Start does NOT stall on browser consent when ToS not accepted.
	mountCalled := 0
	h.provider.mountDriveFSFn = func(ctx context.Context, opts ColabOneDriveMountOptions) (map[string]any, error) {
		mountCalled++
		return map[string]any{"ok": true, "mode": "colab_drivefs"}, nil
	}
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if path == "drive/connect" {
			return map[string]any{"connected": true, "mode": "colab_drivefs"}, nil
		}
		if path == "drive/sync" {
			return map[string]any{"ok": true}, nil
		}
		if path == "snapshot" {
			return map[string]any{"runtime": map[string]any{"state": "ready", "id": "ep-1", "protocol": 7, "idle_minutes": 30}, "models": []any{}}, nil
		}
		return map[string]any{"ok": true}, nil
	}
	if err := h.ctrl.Start(context.Background(), false, ""); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if mountCalled != 0 {
		t.Fatal("Start must not call interactive mount_colab_drivefs when unauthorized")
	}

	// 3) Explicit ConnectDrive falls back to Colab DriveFS.
	info, err := h.ctrl.ConnectDrive(context.Background())
	if err != nil || info["mode"] != "colab_drivefs" || mountCalled != 1 {
		t.Fatalf("expected DriveFS fallback, info=%#v err=%v mountCalled=%d", info, err, mountCalled)
	}

	// 4) drive.confirm / drive.probe / drive.cancel & voluntary cancel produces zero error toast.
	_, _ = h.ctrl.Command(context.Background(), "drive.confirm", nil)
	if !h.ctrl.driveConfirm.IsSet() {
		t.Fatal("expected driveConfirm set")
	}
	_, _ = h.ctrl.Command(context.Background(), "drive.probe", nil)
	if !h.ctrl.driveProbe.IsSet() {
		t.Fatal("expected driveProbe set")
	}
	h.ctrl.Update(map[string]any{"auth_url": "https://accounts.google.com/consent", "drive_auth_hint": "hint"})
	_, _ = h.ctrl.Command(context.Background(), "drive.cancel", nil)
	if !h.ctrl.driveCancel.IsSet() || h.ctrl.Snapshot()["auth_url"] != "" || h.ctrl.Snapshot()["drive_auth_hint"] != "" {
		t.Fatal("expected drive.cancel to set cancel event and clear hints")
	}

	h.provider.mountDriveFSFn = func(ctx context.Context, opts ColabOneDriveMountOptions) (map[string]any, error) {
		opts.CancelEvent.Set()
		return nil, errors.New("已取消 Google 云盘挂载")
	}
	if _, err := h.ctrl.Command(context.Background(), "drive.connect", nil); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, h.ctrl)
	if h.ctrl.Snapshot()["error"] != "" {
		t.Fatalf("voluntary drive cancel must not produce error toast, got %q", h.ctrl.Snapshot()["error"])
	}

	// 5) Quota error during headless rclone connect invalidates quota project and falls back to DriveFS.
	h.auth.ensureQuotaFn = func(ctx context.Context, forceRefresh bool, onLog LogFunc) (string, string, error) {
		return "colabone-drive-proj1", "reused_existing_project", nil
	}
	h.auth.buildRcloneFn = func(ctx context.Context, quotaProject string) (string, error) {
		return "[colabone_gdrive]\ntype = drive\n", nil
	}
	h.provider.uploadDriveFn = func(ctx context.Context, rcloneConf, endpoint string) error {
		return errors.New("googleapi: Error 403: SERVICE_DISABLED accessNotConfigured")
	}
	h.provider.mountDriveFSFn = func(ctx context.Context, opts ColabOneDriveMountOptions) (map[string]any, error) {
		return map[string]any{"ok": true, "mode": "colab_drivefs"}, nil
	}
	if info, err := h.ctrl.ConnectDrive(context.Background()); err != nil || info["mode"] != "colab_drivefs" {
		t.Fatalf("expected quota-error fallback to DriveFS, info=%#v err=%v", info, err)
	}
	if h.auth.invalidateQuotaCalls != 1 {
		t.Fatalf("expected InvalidateDriveQuotaProject called once, got %d", h.auth.invalidateQuotaCalls)
	}

	// 6) drive.sync / sync command.
	h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
	if _, err := h.ctrl.Command(context.Background(), "sync", nil); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, h.ctrl)
	if h.ctrl.Snapshot()["error"] != "" {
		t.Fatalf("unexpected sync error: %q", h.ctrl.Snapshot()["error"])
	}
}

// ---------------------------------------------------------------------------
// 7. Start boot telemetry, retry, cancel & rollback
// ---------------------------------------------------------------------------

func TestStartBootTelemetryRetryCancelAndRollback(t *testing.T) {
	// Case A: Boot telemetry & manual retry trigger during backoff wait.
	h := newTestHarness(t, false)
	h.ctrl.mu.Lock()
	h.ctrl.settings["auto_drive"] = false
	h.ctrl.mu.Unlock()

	var seenBootTelemetry bool
	h.provider.startFn = func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
		notify("等待重试分配 " + gpu)
		opts.OnRetryState(map[string]any{
			"waiting_retry":  true,
			"retryable":      true,
			"retry_at":       nowUnixFloat() + 30.0,
			"next_attempt":   2,
			"total_attempts": 3,
			"gpu":            gpu,
		})
		if !opts.RetryEvent.Wait(2 * time.Second) {
			return nil, errors.New("retry signal not received")
		}
		opts.OnRetryState(map[string]any{"waiting_retry": false, "retryable": false, "allocated": true})
		opts.OnTelemetry(
			"gpu-t4-ass1-boot",
			map[string]any{"country": "IN", "country_name": "印度"},
			map[string]any{
				"gpu":  map[string]any{"name": "Tesla T4", "total_mib": 15360, "used_mib": 128},
				"disk": map[string]any{"free_bytes": 72 * 1024 * 1024 * 1024},
			},
		)
		rtSnap := asMap(h.ctrl.Snapshot()["runtime"])
		if rtSnap["state"] == "starting" && asMap(rtSnap["gpu"])["name"] == "Tesla T4" {
			seenBootTelemetry = true
		}
		h.provider.addManagedSession("gpu-t4-ass1-boot", gpu, "STANDARD", true)
		return map[string]any{"endpoint": "gpu-t4-ass1-boot"}, nil
	}

	if _, err := h.ctrl.Command(context.Background(), "runtime.start", map[string]any{"gpu": "G4"}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		return asBool(asMap(h.ctrl.Snapshot()["operation"])["waiting_retry"])
	})
	retryRes, err := h.ctrl.Command(context.Background(), "runtime.start.retry", nil)
	if err != nil || asMap(retryRes)["triggered"] != true {
		t.Fatalf("expected runtime.start.retry triggered=true, got %v %#v", err, retryRes)
	}
	waitIdle(t, h.ctrl)
	if !seenBootTelemetry || h.ctrl.Snapshot()["error"] != "" {
		t.Fatalf("boot telemetry=%v err=%q", seenBootTelemetry, h.ctrl.Snapshot()["error"])
	}

	// Case B: Cancel after allocation while adding a second instance (new: true) rolls back new VM & preserves active.
	h2 := newTestHarness(t, false)
	h2.ctrl.mu.Lock()
	h2.ctrl.settings["auto_drive"] = false
	h2.ctrl.mu.Unlock()
	h2.provider.addManagedSession("ep-existing", "T4", "STANDARD", true)
	h2.ctrl.Update(map[string]any{"runtime": map[string]any{"state": "ready", "id": "ep-existing"}})

	entered := make(chan struct{})
	h2.provider.startFn = func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
		h2.provider.addManagedSession("ep-new-rollback", gpu, "STANDARD", true)
		opts.OnRetryState(map[string]any{"allocated": true})
		close(entered)
		opts.CancelEvent.Wait(2 * time.Second)
		return nil, errors.New("cancelled during boot")
	}
	if _, err := h2.ctrl.Command(context.Background(), "runtime.start", map[string]any{"new": true, "gpu": "G4"}); err != nil {
		t.Fatal(err)
	}
	<-entered
	cancelRes, err := h2.ctrl.Command(context.Background(), "runtime.start.cancel", nil)
	if err != nil || asMap(cancelRes)["cancelled"] != true {
		t.Fatalf("expected runtime.start.cancel cancelled=true, got %v %#v", err, cancelRes)
	}
	waitIdle(t, h2.ctrl)
	snap2 := h2.ctrl.Snapshot()
	if snap2["error"] != "" {
		t.Fatalf("cancelled start must not set error toast, got %q", snap2["error"])
	}
	if snap2["active_endpoint"] != "ep-existing" || asMap(snap2["runtime"])["id"] != "ep-existing" || asMap(snap2["runtime"])["state"] != "ready" {
		t.Fatalf("expected existing instance preserved after rollback, got active=%v rt=%#v", snap2["active_endpoint"], snap2["runtime"])
	}
}

// ---------------------------------------------------------------------------
// 8. Multi-instance select, release, preview & release_all
// ---------------------------------------------------------------------------

func TestMultiInstanceSelectReleaseAndReleaseAll(t *testing.T) {
	h := newTestHarness(t, false)
	h.provider.addManagedSession("ep-1", "T4", "HIGH_RAM", true)
	h.ctrl.Update(map[string]any{"runtime": map[string]any{"state": "ready", "id": "ep-1"}})
	h.provider.addManagedSession("ep-2", "A100", "STANDARD", false)

	// runtime.select switches active instance.
	selRes, err := h.ctrl.Command(context.Background(), "runtime.select", map[string]any{"endpoint": "ep-2"})
	if err != nil || asMap(selRes)["endpoint"] != "ep-2" {
		t.Fatalf("runtime.select failed: %v %#v", err, selRes)
	}
	if h.ctrl.Snapshot()["active_endpoint"] != "ep-2" {
		t.Fatalf("expected active_endpoint=ep-2, got %v", h.ctrl.Snapshot()["active_endpoint"])
	}

	// Busy guard prevents runtime.select.
	h.ctrl.opBusy.Store(true)
	if _, err := h.ctrl.Command(context.Background(), "runtime.select", map[string]any{"endpoint": "ep-1"}); err == nil || !strings.Contains(err.Error(), "请等待当前操作完成后再切换实例") {
		t.Fatalf("expected busy guard error, got %v", err)
	}
	h.ctrl.opBusy.Store(false)

	// Release blocks on prepare-release failure unless force=true.
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if path == "prepare-release" {
			return nil, errors.New("会话尚未同步")
		}
		return map[string]any{"ok": true}, nil
	}
	if err := h.ctrl.Release(context.Background(), false, ""); err == nil || !strings.Contains(err.Error(), "尚未同步") {
		t.Fatalf("expected unsynced release block, got %v", err)
	}
	if len(h.provider.releaseCalls) != 0 {
		t.Fatal("provider.Release must not be called when prepare-release fails")
	}
	if err := h.ctrl.Release(context.Background(), true, ""); err != nil {
		t.Fatalf("forced release failed: %v", err)
	}
	if len(h.provider.releaseCalls) != 1 || h.ctrl.Snapshot()["active_endpoint"] != "ep-1" {
		t.Fatalf("expected forced release switched back to ep-1, calls=%#v active=%v", h.provider.releaseCalls, h.ctrl.Snapshot()["active_endpoint"])
	}

	// PreviewReleaseAll & ReleaseAll checks.
	h.provider.accountFn = func(ctx context.Context) (map[string]any, error) {
		return map[string]any{
			"logged_in":   true,
			"assignments": 2,
			"rate":        3.5,
			"instances": []map[string]any{
				{"id": "ep-1", "managed": true},
				{"id": "ep-ext", "managed": false},
			},
		}, nil
	}
	planRaw, err := h.ctrl.Command(context.Background(), "runtime.release.preview", nil)
	if err != nil {
		t.Fatal(err)
	}
	planID := asString(asMap(planRaw)["id"])

	// Reject wrong plan ID, account mismatch, empty endpoints, unknown endpoint.
	if err := h.ctrl.ReleaseAll(context.Background(), "bad-plan", true, nil, false); err == nil || !strings.Contains(err.Error(), "清单") {
		t.Fatalf("expected bad plan error, got %v", err)
	}
	if err := h.ctrl.ReleaseAll(context.Background(), planID, true, []string{}, true); err == nil || !strings.Contains(err.Error(), "至少勾选") {
		t.Fatalf("expected empty endpoints error, got %v", err)
	}
	if err := h.ctrl.ReleaseAll(context.Background(), planID, true, []string{"not-in-plan"}, true); err == nil || !strings.Contains(err.Error(), "不在已确认清单中") {
		t.Fatalf("expected out-of-plan error, got %v", err)
	}
	h.auth.profile = map[string]any{"sub": "changed-sub"}
	if err := h.ctrl.ReleaseAll(context.Background(), planID, true, nil, false); err == nil || !strings.Contains(err.Error(), "清单") {
		t.Fatalf("expected account mismatch error, got %v", err)
	}
	h.auth.profile = map[string]any{"sub": "user-sub-1"}

	// Partial selection succeeds even if remaining assignments > 0.
	h.provider.accountFn = func(ctx context.Context) (map[string]any, error) {
		return map[string]any{"logged_in": true, "assignments": 1, "rate": 1.5, "instances": []map[string]any{{"id": "ep-ext", "managed": false}}}, nil
	}
	if err := h.ctrl.ReleaseAll(context.Background(), planID, true, []string{"ep-1"}, true); err != nil {
		t.Fatalf("partial ReleaseAll failed: %v", err)
	}
	if len(h.provider.releaseAllCalls) != 1 || len(h.provider.releaseAllCalls[0]) != 1 || h.provider.releaseAllCalls[0][0] != "ep-1" {
		t.Fatalf("unexpected releaseAll targets: %#v", h.provider.releaseAllCalls)
	}
}

// ---------------------------------------------------------------------------
// 9. UpgradeService (default, force teardown, auto-restore, pinned endpoint, canvas watcher)
// ---------------------------------------------------------------------------

func TestUpgradeServiceDefaultForceRestorePinnedEndpointAndCanvasWatcher(t *testing.T) {
	// A) Default force=false skips teardown RPCs.
	h0 := newTestHarness(t, false)
	if err := h0.ctrl.UpgradeService(context.Background(), false, true); err != nil {
		t.Fatal(err)
	}
	for _, c := range h0.provider.rpcCalls {
		if c.Path == "public-api/stop" || c.Path == "unload" || c.Path == "comfyui/stop" {
			t.Fatalf("force=false must not call teardown RPC %q", c.Path)
		}
	}

	// B) Force=true tears down Public API + chained tasks + ComfyUI + model, restores model + Public API, and pins endpoint!
	h := newTestHarness(t, false)
	tok := validCFToken()
	apiKey := "co_" + strings.Repeat("A", 44)
	_ = h.vault.Set("public_api", map[string]any{
		"mode":         "named",
		"domain":       "https://api.example.com",
		"tunnel_token": tok,
		"api_key":      apiKey,
	})
	h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
	h.provider.addManagedSession("ep-2", "A100", "STANDARD", false)

	tid1 := strings.Repeat("1", 32)
	tid2 := strings.Repeat("2", 32)
	var statusCalls int
	var seenOrder []string
	var seenEndpoints []string
	publicActive := true
	comfyRunning := true
	loadedModel := "gemma4-31b"

	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		seenOrder = append(seenOrder, method+" "+path)
		seenEndpoints = append(seenEndpoints, opts.Endpoint)
		switch {
		case method == "GET" && path == "status":
			statusCalls++
			var tasks []map[string]any
			switch statusCalls {
			case 1, 2:
				tasks = []map[string]any{{"id": tid1, "state": "running", "kind": "public_responses"}}
			case 3:
				tasks = []map[string]any{{"id": tid1, "state": "cancelled"}, {"id": tid2, "state": "running"}}
			default:
				tasks = []map[string]any{{"id": tid1, "state": "cancelled"}, {"id": tid2, "state": "cancelled"}}
			}
			return map[string]any{
				"public_api":     map[string]any{"enabled": publicActive, "state": map[bool]string{true: "ready", false: "stopped"}[publicActive]},
				"tasks":          tasks,
				"comfyui":        map[string]any{"running": comfyRunning},
				"loaded_model":   loadedModel,
				"active_options": map[string]any{"context_length": 131072, "temperature": 0.7},
				"active_engine":  map[bool]any{true: "llama", false: nil}[loadedModel != ""],
			}, nil
		case method == "POST" && path == "public-api/stop":
			publicActive = false
			return map[string]any{"ok": true}, nil
		case method == "DELETE" && strings.HasPrefix(path, "tasks/"):
			return map[string]any{"ok": true}, nil
		case method == "POST" && path == "comfyui/stop":
			comfyRunning = false
			return map[string]any{"ok": true}, nil
		case method == "POST" && path == "unload":
			loadedModel = ""
			return map[string]any{"ok": true}, nil
		case method == "GET" && path == "snapshot":
			return map[string]any{"runtime": map[string]any{"state": "ready", "id": "ep-1", "protocol": 7}, "models": h.ctrl.Presets()}, nil
		default:
			return map[string]any{"ok": true}, nil
		}
	}
	h.provider.upgradeFn = func(ctx context.Context, endpoint string, onLog LogFunc) error {
		seenOrder = append(seenOrder, "PROVIDER upgrade_service")
		seenEndpoints = append(seenEndpoints, endpoint)
		// Switch active endpoint mid-upgrade to verify pinning!
		_ = h.vault.Set("active_endpoint", "ep-2")
		return nil
	}

	if err := h.ctrl.UpgradeService(context.Background(), true, true); err != nil {
		t.Fatalf("UpgradeService force failed: %v", err)
	}
	for _, ep := range seenEndpoints {
		if ep != "ep-1" {
			t.Fatalf("expected all upgrade calls pinned to ep-1, got endpoints=%#v", seenEndpoints)
		}
	}
	idxOf := func(needle string) int {
		for i, s := range seenOrder {
			if s == needle {
				return i
			}
		}
		return -1
	}
	upIdx := idxOf("PROVIDER upgrade_service")
	if upIdx < 0 ||
		idxOf("POST public-api/stop") >= upIdx ||
		idxOf("DELETE tasks/"+tid1) >= upIdx ||
		idxOf("DELETE tasks/"+tid2) >= upIdx ||
		idxOf("POST comfyui/stop") >= upIdx ||
		idxOf("POST unload") >= upIdx ||
		idxOf("POST models/gemma4-31b/load") <= upIdx ||
		idxOf("POST public-api/start") <= upIdx {
		t.Fatalf("unexpected upgrade order: %#v", seenOrder)
	}

	// Verify human-readable spec label ("128K · 温度 0.7") logged.
	foundLabel := false
	for _, e := range h.ctrl.activity.Snapshot() {
		if strings.Contains(e.Message, "128K · 温度 0.7") {
			foundLabel = true
		}
	}
	if !foundLabel {
		t.Fatal("expected 128K · 温度 0.7 restore log")
	}

	// C) RestoreComfyCanvasWhenReady exits cleanly when LLM takes over without starting canvas.
	canvasStarts := 0
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if path == "comfyui/canvas/start" {
			canvasStarts++
		}
		return map[string]any{"loaded_model": "gemma4-31b", "active_engine": "llama"}, nil
	}
	h.ctrl.RestoreComfyCanvasWhenReady(context.Background(), map[string]any{"mode": "named"}, "", "", time.Second)
	if canvasStarts != 0 {
		t.Fatal("canvas watcher must not start canvas after user switched to LLM")
	}
}

// ---------------------------------------------------------------------------
// 10. External telemetry monitor & main background monitor loop + zero leaks
// ---------------------------------------------------------------------------

func TestMonitorAutoManualSwitchPauseRetryAndExternalPolling(t *testing.T) {
	h := newTestHarness(t, false)

	// Local stopped, remote has 1 CPU + 1 G4 -> auto prefers G4.
	h.ctrl.Update(map[string]any{
		"account": map[string]any{
			"logged_in":   true,
			"balance":     2116.33,
			"rate":        10.05,
			"assignments": 2,
			"instances": []map[string]any{
				{"id": "cpu-1", "managed": false, "accelerator": "CPU", "variant": "CPU", "shape": "STANDARD"},
				{"id": "gpu-g4-ext", "managed": false, "accelerator": "G4", "variant": "GPU", "shape": "STANDARD"},
			},
		},
	})
	snap0 := h.ctrl.Snapshot()
	mon0 := asMap(snap0["monitor"])
	if mon0["mode"] != "auto" || mon0["endpoint"] != "gpu-g4-ext" || mon0["managed"] != false || asMap(mon0["runtime"])["state"] != "connecting" {
		t.Fatalf("unexpected initial monitor snapshot: %#v", mon0)
	}

	// Poll populates live telemetry.
	h.provider.observeFn = func(ctx context.Context, endpoint string) (map[string]any, error) {
		return map[string]any{
			"source":            "remote_readonly",
			"loaded_model_name": "",
			"drive_connected":   false,
			"runtime": map[string]any{
				"id":    endpoint,
				"state": "ready",
				"gpu":   map[string]any{"name": "NVIDIA RTX PRO 6000", "total_mib": 98304, "used_mib": 12000},
			},
		}, nil
	}
	if !h.ctrl.PollExternalMonitorOnce(context.Background(), true) {
		t.Fatal("expected external poll success")
	}
	mon1 := asMap(h.ctrl.Snapshot()["monitor"])
	if asMap(mon1["runtime"])["state"] != "ready" || asMap(asMap(mon1["runtime"])["gpu"])["name"] != "NVIDIA RTX PRO 6000" {
		t.Fatalf("unexpected observed monitor snapshot: %#v", mon1)
	}

	// Pause closes observer and stops polling.
	_, _ = h.ctrl.Command(context.Background(), "monitor.pause", map[string]any{"paused": true})
	if h.ctrl.PollExternalMonitorOnce(context.Background(), true) {
		t.Fatal("paused monitor must not poll")
	}

	// Resume + error scoped to monitor.error (not top-level error), retry clears.
	_, _ = h.ctrl.Command(context.Background(), "monitor.pause", map[string]any{"paused": false})
	h.provider.observeFn = func(ctx context.Context, endpoint string) (map[string]any, error) {
		return nil, errors.New("proxy timeout")
	}
	if h.ctrl.PollExternalMonitorOnce(context.Background(), true) {
		t.Fatal("expected failed poll to return false")
	}
	snapErr := h.ctrl.Snapshot()
	if snapErr["error"] != "" || !strings.Contains(asString(asMap(snapErr["monitor"])["error"]), "proxy timeout") {
		t.Fatalf("error should be scoped to monitor.error: top=%q mon=%#v", snapErr["error"], snapErr["monitor"])
	}
	_, _ = h.ctrl.Command(context.Background(), "monitor.retry", nil)
	if asMap(h.ctrl.Snapshot()["monitor"])["error"] != "" {
		t.Fatal("monitor.retry should clear monitor.error")
	}

	// ErrLookup evicts observation and resets lastAuth=0.
	h.provider.observeFn = func(ctx context.Context, endpoint string) (map[string]any, error) {
		return nil, ErrLookup
	}
	h.ctrl.mu.Lock()
	h.ctrl.lastAuth = nowUnixFloat()
	h.ctrl.mu.Unlock()
	h.ctrl.PollExternalMonitorOnce(context.Background(), true)
	h.ctrl.mu.Lock()
	la := h.ctrl.lastAuth
	h.ctrl.mu.Unlock()
	if la != 0 {
		t.Fatalf("expected lastAuth=0 on ErrLookup, got %v", la)
	}

	// Manual switch to CPU works; disappearing remote falls back cleanly to local active.
	if _, err := h.ctrl.Command(context.Background(), "monitor.select", map[string]any{"endpoint": "cpu-1"}); err != nil {
		t.Fatal(err)
	}
	if asMap(h.ctrl.Snapshot()["monitor"])["mode"] != "manual" {
		t.Fatal("expected manual mode")
	}
	h.provider.addManagedSession("ep-local", "T4", "HIGH_RAM", true)
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{"id": "ep-local", "state": "ready"},
		"account": map[string]any{"logged_in": true, "assignments": 1, "instances": []map[string]any{}},
	})
	monFallback := asMap(h.ctrl.Snapshot()["monitor"])
	if monFallback["mode"] != "auto" || monFallback["endpoint"] != "ep-local" || monFallback["managed"] != true {
		t.Fatalf("expected auto fallback to local managed instance, got %#v", monFallback)
	}
}

func TestMainMonitorHeartbeatBeforeConfigureAutoUpgradeSkipAndBackgroundClose(t *testing.T) {
	// 1) Heartbeat runs even if ConfigureRemote fails.
	h := newTestHarness(t, false)
	h.provider.addManagedSession("ep-owned", "T4", "STANDARD", true)
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if path == "configure" {
			return nil, errors.New("stale connection")
		}
		return map[string]any{
			"runtime": map[string]any{
				"id":               "ep-owned",
				"state":            "ready",
				"protocol":         7,
				"service_revision": "stale00000000000",
				"public_api":       map[string]any{"state": "ready", "enabled": true},
				"tasks":            []any{},
			},
			"models": []any{},
		}, nil
	}
	h.ctrl.MonitorOnce(context.Background())
	if len(h.provider.heartbeatCalls) != 1 {
		t.Fatalf("expected heartbeat called even when configure failed, got %d", len(h.provider.heartbeatCalls))
	}

	// 2) Active Public API tunnel prevents unsolicited background hot-upgrade.
	h2 := newTestHarness(t, false)
	h2.provider.addManagedSession("ep-live", "T4", "STANDARD", true)
	h2.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if path == "snapshot" {
			return map[string]any{
				"runtime": map[string]any{
					"id":               "ep-live",
					"state":            "ready",
					"protocol":         7,
					"service_revision": "stale00000000000",
					"public_api":       map[string]any{"state": "ready", "enabled": true},
					"tasks":            []any{},
				},
				"models": []any{},
			}, nil
		}
		return map[string]any{"ok": true}, nil
	}
	h2.ctrl.MonitorOnce(context.Background())
	if len(h2.provider.upgradeCalls) != 0 {
		t.Fatalf("expected no auto-upgrade while Public API is ready, got %#v", h2.provider.upgradeCalls)
	}
	if asMap(h2.ctrl.Snapshot()["runtime"])["upgrade_available"] != true {
		t.Fatal("expected upgrade_available=true flagged on runtime")
	}

	// 3) Background=true starts both loops and Close() shuts down cleanly with zero goroutine leaks.
	beforeGoroutines := runtime.NumGoroutine()
	var accountHits atomic.Int32
	hBg := newTestHarness(t, false)
	hBg.ctrl.Close()

	cfgDir := t.TempDir()
	vault := NewColabOneMemoryVault()
	prov := newMockProvider(vault)
	prov.accountFn = func(ctx context.Context) (map[string]any, error) {
		accountHits.Add(1)
		return map[string]any{"logged_in": true, "balance": 50.0, "rate": 0.0, "assignments": 0, "instances": []map[string]any{}}, nil
	}
	bgCtrl := NewColabOneController(ColabOneControllerConfig{
		Root:             cfgDir,
		ConfigDir:        cfgDir,
		Background:       true,
		Vault:            vault,
		Auth:             newMockAuth(),
		Provider:         prov,
		ServiceBundler:   defaultMockBundler(),
		MonitorInterval:  10 * time.Millisecond,
		ObserverInterval: 10 * time.Millisecond,
	})
	waitUntil(t, 2*time.Second, func() bool {
		return accountHits.Load() >= 1
	})
	bgCtrl.Close()
	// Idempotent second Close() must not panic.
	bgCtrl.Close()
	time.Sleep(20 * time.Millisecond)
	afterGoroutines := runtime.NumGoroutine()
	if afterGoroutines > beforeGoroutines+2 {
		t.Fatalf("possible goroutine leak: before=%d after=%d", beforeGoroutines, afterGoroutines)
	}
}

// ---------------------------------------------------------------------------
// 11. Auth, locale, shortcuts, runtime.activity, unknown action
// ---------------------------------------------------------------------------

func TestAuthLocaleShortcutsActivityTouchAndUnknownAction(t *testing.T) {
	h := newTestHarness(t, false)

	// locale.set non-persisted vs persisted + invalid.
	res1, err := h.ctrl.Command(context.Background(), "locale.set", map[string]any{"locale": "ja-JP", "persist": false})
	if err != nil || asMap(res1)["persisted"] != false || h.auth.Locale() != "ja-JP" {
		t.Fatalf("unexpected non-persisted locale.set: %v %#v", err, res1)
	}
	res2, err := h.ctrl.Command(context.Background(), "locale.set", map[string]any{"locale": "zh-HK", "persist": true})
	if err != nil || asMap(res2)["persisted"] != true || h.auth.Locale() != "zh-HK" {
		t.Fatalf("unexpected persisted locale.set: %v %#v", err, res2)
	}
	if _, err := h.ctrl.Command(context.Background(), "locale.set", map[string]any{"locale": "xx-XX"}); err == nil {
		t.Fatal("expected unsupported locale error")
	}

	// account.refresh
	accRes, err := h.ctrl.Command(context.Background(), "account.refresh", nil)
	if err != nil || asMap(accRes)["ok"] != true {
		t.Fatalf("account.refresh failed: %v %#v", err, accRes)
	}

	// auth.colab.begin, auth.colab.auto, auth.colab.finish (standalone & loopback active).
	beginRes, err := h.ctrl.Command(context.Background(), "auth.colab.begin", nil)
	if err != nil || asString(asMap(beginRes)["url"]) == "" {
		t.Fatalf("auth.colab.begin failed: %v %#v", err, beginRes)
	}
	if _, err := h.ctrl.Command(context.Background(), "auth.colab.auto", nil); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, h.ctrl)
	if _, err := h.ctrl.Command(context.Background(), "auth.colab.finish", map[string]any{"code": "4/code-1"}); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, h.ctrl)
	h.auth.loopbackActive = true
	if res, err := h.ctrl.Command(context.Background(), "auth.colab.finish", map[string]any{"code": "4/code-2"}); err != nil || asMap(res)["ok"] != true {
		t.Fatalf("loopback auth.colab.finish failed: %v %#v", err, res)
	}
	if len(h.auth.finishCalls) != 2 {
		t.Fatalf("expected 2 ColabFinish calls, got %#v", h.auth.finishCalls)
	}

	// desktop.shortcuts
	sc1, err := h.ctrl.Command(context.Background(), "desktop.shortcuts", map[string]any{"target": "start_menu"})
	if err != nil || asMap(sc1)["start_menu"] != true {
		t.Fatalf("desktop.shortcuts create failed: %v %#v", err, sc1)
	}
	sc2, err := h.ctrl.Command(context.Background(), "desktop.shortcuts", map[string]any{"target": "both", "remove": true})
	if err != nil || asMap(sc2)["start_menu"] != false || asMap(sc2)["desktop"] != false {
		t.Fatalf("desktop.shortcuts remove failed: %v %#v", err, sc2)
	}
	if _, err := h.ctrl.Command(context.Background(), "desktop.shortcuts", map[string]any{"target": "invalid"}); err == nil {
		t.Fatal("expected invalid shortcut target error")
	}

	// runtime.activity triggers POST activity when ready & protocol >= 2.
	h.ctrl.Update(map[string]any{"runtime": map[string]any{"state": "ready", "protocol": 7}})
	if _, err := h.ctrl.Command(context.Background(), "runtime.activity", nil); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		h.provider.mu.Lock()
		defer h.provider.mu.Unlock()
		for _, c := range h.provider.rpcCalls {
			if c.Path == "activity" {
				return true
			}
		}
		return false
	})

	// Unknown action.
	if _, err := h.ctrl.Command(context.Background(), "no.such.action", nil); err == nil || err.Error() != "不支持的操作" {
		t.Fatalf("expected 不支持的操作, got %v", err)
	}
}

func TestServiceRevisionMatchesDeterministicBundleHash(t *testing.T) {
	root := t.TempDir()
	for _, sub := range []string{"server", "common", "catalog"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(root, "server", "app.py"), []byte("print('ok')\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "server", "ignore.pyc"), []byte("ignored"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "common", "models.py"), []byte("X = 1\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "catalog", "presets.json"), []byte("[]\n"), 0o644)

	rev1 := ColabOneServiceRevision(root)
	if len(rev1) != 16 {
		t.Fatalf("expected 16-char hex revision, got %q", rev1)
	}
	// Ignored extension must not change hash.
	_ = os.WriteFile(filepath.Join(root, "server", "ignore.pyc"), []byte("changed"), 0o644)
	if rev2 := ColabOneServiceRevision(root); rev2 != rev1 {
		t.Fatalf("ignored file changed revision: %q vs %q", rev1, rev2)
	}
	// Tracked file change must change hash.
	_ = os.WriteFile(filepath.Join(root, "server", "app.py"), []byte("print('v2')\n"), 0o644)
	if rev3 := ColabOneServiceRevision(root); rev3 == rev1 {
		t.Fatal("expected tracked file change to update revision")
	}
	if empty := ColabOneServiceRevision(t.TempDir()); empty != "" {
		t.Fatalf("expected empty revision for empty root, got %q", empty)
	}
}

func TestUpgradeServiceComfyUIAndCanvasAutoRestoreAndPartialFailure(t *testing.T) {
	// A) ComfyUI + Canvas auto-restore when ComfyUI becomes running immediately.
	h := newTestHarness(t, false)
	tok := validCFToken()
	_ = h.vault.Set("comfyui_canvas", map[string]any{
		"mode":         "named",
		"domain":       "https://canvas.example.com",
		"tunnel_token": tok,
	})
	comfyRunning := true
	canvasEnabled := true
	var canvasStartBody map[string]any
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		switch {
		case method == "GET" && path == "status":
			return map[string]any{
				"public_api":    map[string]any{"enabled": false, "state": "stopped"},
				"tasks":         []any{},
				"comfyui":       map[string]any{"running": comfyRunning, "state": map[bool]string{true: "running", false: "stopped"}[comfyRunning], "canvas": map[string]any{"enabled": canvasEnabled, "state": map[bool]string{true: "ready", false: "stopped"}[canvasEnabled]}},
				"loaded_model":  "",
				"active_engine": map[bool]any{true: "comfyui", false: nil}[comfyRunning],
			}, nil
		case method == "POST" && path == "comfyui/stop":
			comfyRunning = false
			canvasEnabled = false
			return map[string]any{"ok": true}, nil
		case method == "POST" && path == "comfyui/start":
			comfyRunning = true
			return map[string]any{"id": strings.Repeat("c", 32), "kind": "comfy_start", "state": "completed"}, nil
		case method == "POST" && path == "comfyui/canvas/start":
			canvasEnabled = true
			canvasStartBody = deepCopyMap(asMap(body))
			return map[string]any{"enabled": true, "state": "starting"}, nil
		case method == "GET" && path == "snapshot":
			return map[string]any{"runtime": map[string]any{"state": "ready", "id": "ep-1", "protocol": 7}, "models": []any{}}, nil
		default:
			return map[string]any{"ok": true}, nil
		}
	}
	if err := h.ctrl.UpgradeService(context.Background(), true, true); err != nil {
		t.Fatalf("ComfyUI+Canvas upgrade restore failed: %v", err)
	}
	if canvasStartBody["domain"] != "https://canvas.example.com" || canvasStartBody["tunnel_token"] != tok {
		t.Fatalf("expected canvas restored with saved config, got %#v", canvasStartBody)
	}

	// B) Partial restore failure (missing public_api vault + model load error) must NOT fail UpgradeService.
	h2 := newTestHarness(t, false)
	h2.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		switch {
		case method == "GET" && path == "status":
			return map[string]any{
				"public_api":     map[string]any{"enabled": true, "state": "ready"},
				"tasks":          []any{},
				"comfyui":        map[string]any{"running": false},
				"loaded_model":   "gemma4-31b",
				"active_options": map[string]any{"context_length": 65536},
				"active_engine":  "llama",
			}, nil
		case method == "POST" && path == "models/gemma4-31b/load":
			return nil, errors.New("transient gpu error")
		case method == "GET" && path == "snapshot":
			return map[string]any{"runtime": map[string]any{"state": "ready", "id": "ep-1", "protocol": 7}, "models": h2.ctrl.Presets()}, nil
		default:
			return map[string]any{"ok": true}, nil
		}
	}
	if err := h2.ctrl.UpgradeService(context.Background(), true, true); err != nil {
		t.Fatalf("upgrade should succeed despite partial restore errors: %v", err)
	}
	var sawLoadWarn, sawAPIWarn bool
	for _, e := range h2.ctrl.activity.Snapshot() {
		if strings.Contains(e.Message, "自动重新装载模型") && strings.Contains(e.Message, "失败") {
			sawLoadWarn = true
		}
		if strings.Contains(e.Message, "缺少完整固定域名或隧道配置") {
			sawAPIWarn = true
		}
	}
	if !sawLoadWarn || !sawAPIWarn {
		t.Fatalf("expected partial restore warnings: loadWarn=%v apiWarn=%v", sawLoadWarn, sawAPIWarn)
	}
}

func TestConcurrentSnapshotCommandsAndMonitorStress(t *testing.T) {
	h := newTestHarness(t, true)
	h.provider.addManagedSession("ep-stress", "T4", "STANDARD", true)
	h.ctrl.Update(map[string]any{"runtime": map[string]any{"state": "ready", "id": "ep-stress", "protocol": 7}})

	var wg sync.WaitGroup
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				snap := h.ctrl.Snapshot()
				if _, err := json.Marshal(snap); err != nil {
					t.Errorf("snapshot marshal failed: %v", err)
				}
				switch (idx + i) % 4 {
				case 0:
					_, _ = h.ctrl.Command(context.Background(), "model.common", map[string]any{"id": "gemma4-31b"})
				case 1:
					_, _ = h.ctrl.Command(context.Background(), "monitor.pause", map[string]any{"paused": i%2 == 0})
				case 2:
					_, _ = h.ctrl.Command(context.Background(), "runtime.activity", nil)
				case 3:
					h.ctrl.MonitorOnce(context.Background())
				}
			}
		}(g)
	}
	wg.Wait()
	h.ctrl.Close()
}

func TestReviewRegressionsShortcutSandboxPersistSerializationAndSecondaryCleanup(t *testing.T) {
	// 1. Default production configDir (empty or equal to DefaultConfigDir()) must NOT force shortcut sandbox.
	defaultDir := t.TempDir()
	t.Setenv("COLABONE_CONFIG_DIR", defaultDir)
	t.Setenv("COLABONE_SHORTCUT_DIR", "")
	cEmpty := NewColabOneController(ColabOneControllerConfig{
		Vault:      NewColabOneMemoryVault(),
		Background: false,
	})
	defer cEmpty.Close()
	if cEmpty.shortcutSandbox != "" {
		t.Fatalf("empty ConfigDir wrongly forced shortcut sandbox: %q", cEmpty.shortcutSandbox)
	}

	cSameAsDefault := NewColabOneController(ColabOneControllerConfig{
		ConfigDir:  defaultDir,
		Vault:      NewColabOneMemoryVault(),
		Background: false,
	})
	defer cSameAsDefault.Close()
	if cSameAsDefault.shortcutSandbox != "" {
		t.Fatalf("DefaultConfigDir() wrongly forced shortcut sandbox: %q", cSameAsDefault.shortcutSandbox)
	}

	// COLABONE_SHORTCUT_DIR env override must work when not using a custom test ConfigDir.
	envSandbox := t.TempDir()
	t.Setenv("COLABONE_SHORTCUT_DIR", envSandbox)
	cEnv := NewColabOneController(ColabOneControllerConfig{
		Vault:      NewColabOneMemoryVault(),
		Background: false,
	})
	defer cEnv.Close()
	if cEnv.shortcutSandbox != envSandbox {
		t.Fatalf("COLABONE_SHORTCUT_DIR ignored: got %q, want %q", cEnv.shortcutSandbox, envSandbox)
	}

	// Custom non-default ConfigDir (used by tests) still auto-sandboxes shortcuts.
	customDir := t.TempDir()
	cCustom := NewColabOneController(ColabOneControllerConfig{
		ConfigDir:  customDir,
		Vault:      NewColabOneMemoryVault(),
		Background: false,
	})
	defer cCustom.Close()
	wantSandbox := filepath.Join(customDir, "shortcuts")
	if cCustom.shortcutSandbox != wantSandbox {
		t.Fatalf("custom test ConfigDir sandbox = %q, want %q", cCustom.shortcutSandbox, wantSandbox)
	}

	// 2. Secondary-instance monitor error after concurrent Release must NOT resurrect zombie instanceStates.
	h := newTestHarness(t, false)
	defer h.ctrl.Close()
	_ = h.vault.Set("runtime_session_ids", []any{"ep-primary", "ep-secondary"})
	_ = h.vault.Set("runtime_session:ep-primary", map[string]any{"endpoint": "ep-primary", "token": "t1", "url": "https://a"})
	_ = h.vault.Set("runtime_session:ep-secondary", map[string]any{"endpoint": "ep-secondary", "token": "t2", "url": "https://b"})
	_ = h.vault.Set("active_endpoint", "ep-primary")
	h.provider.heartbeatFn = func(ctx context.Context, endpoint string) error {
		if endpoint == "ep-secondary" {
			// Simulate concurrent release clearing ep-secondary while Heartbeat was in flight.
			_ = h.vault.Set("runtime_session_ids", []any{"ep-primary"})
			_ = h.vault.Delete("runtime_session:ep-secondary")
			h.ctrl.mu.Lock()
			delete(h.ctrl.instanceStates, "ep-secondary")
			h.ctrl.mu.Unlock()
			return errors.New("connection closed")
		}
		return nil
	}
	h.ctrl.MonitorOnce(context.Background())
	h.ctrl.mu.Lock()
	_, zombie := h.ctrl.instanceStates["ep-secondary"]
	h.ctrl.mu.Unlock()
	if zombie {
		t.Fatal("MonitorOnce resurrected released secondary endpoint into instanceStates")
	}

	// 3. ConnectDrive guards nil provider safely without panicking.
	nilProvCtrl := NewColabOneController(ColabOneControllerConfig{
		ConfigDir:  t.TempDir(),
		Vault:      NewColabOneMemoryVault(),
		Background: false,
	})
	defer nilProvCtrl.Close()
	if _, err := nilProvCtrl.ConnectDrive(context.Background()); err == nil {
		t.Fatal("expected error from ConnectDrive when provider is nil")
	}

	// 4. asBool handles json.Number("0") as false and non-zero json.Number as true.
	if asBool(json.Number("0")) || asBool(json.Number("0.0")) || asBool(uint64(0)) {
		t.Fatal("asBool must treat zero json.Number and zero uint64 as false")
	}
	if !asBool(json.Number("1")) || !asBool(json.Number("1085739284759283741")) {
		t.Fatal("asBool must treat non-zero json.Number as true")
	}

	// 5. Start() rollback must pass an uncancelled context to provider.Release even when caller ctx is cancelled.
	hRoll := newTestHarness(t, false)
	defer hRoll.ctrl.Close()
	var releaseCtxErr error
	var releasedEP string
	hRoll.provider.startFn = func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
		_ = hRoll.vault.Set("runtime_session_ids", []any{"ep-orphan"})
		_ = hRoll.vault.Set("runtime_session:ep-orphan", map[string]any{"endpoint": "ep-orphan", "token": "t", "url": "https://rt"})
		_ = hRoll.vault.Set("active_endpoint", "ep-orphan")
		return map[string]any{"endpoint": "ep-orphan", "gpu": "T4"}, nil
	}
	hRoll.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		return nil, context.Canceled
	}
	hRoll.provider.releaseFn = func(ctx context.Context, endpoint string, onLog LogFunc) (map[string]any, error) {
		releasedEP = endpoint
		releaseCtxErr = ctx.Err()
		_ = hRoll.vault.Delete("runtime_session:ep-orphan")
		_ = hRoll.vault.Set("runtime_session_ids", []any{})
		return map[string]any{"released": true}, nil
	}
	cCtx, cCancel := context.WithCancel(context.Background())
	cCancel()
	if err := hRoll.ctrl.Start(cCtx, false, "T4"); err == nil {
		t.Fatal("expected Start to fail when initial status RPC fails")
	}
	if releasedEP != "ep-orphan" || releaseCtxErr != nil {
		t.Fatalf("rollback Release should use fresh context: ep=%q ctxErr=%v", releasedEP, releaseCtxErr)
	}

	// 6. loadWorkflows preserves 64-bit seeds > 2^53 via json.Number across disk reload.
	wfDir := t.TempDir()
	const seed64 = "1085739284759283741"
	wfJSON := `[{"id":"wf_seed64","name":"Seed64","workflow":{"1":{"class_type":"KSampler","inputs":{"seed":` + seed64 + `}}}}]`
	if err := os.WriteFile(filepath.Join(wfDir, "workflows.json"), []byte(wfJSON), 0o644); err != nil {
		t.Fatalf("write workflows.json: %v", err)
	}
	cReload := NewColabOneController(ColabOneControllerConfig{
		ConfigDir:  wfDir,
		Vault:      NewColabOneMemoryVault(),
		Background: false,
	})
	defer cReload.Close()
	cReload.mu.Lock()
	loadedWF := cReload.customWorkflows["wf_seed64"]
	cReload.mu.Unlock()
	reEncoded, _ := json.Marshal(loadedWF)
	if !strings.Contains(string(reEncoded), seed64) {
		t.Fatalf("loadWorkflows lost 64-bit seed precision across reload: %s", string(reEncoded))
	}
}

type mockClosingProvider struct {
	*mockProvider
	closeCalls atomic.Int32
	onClose    func()
}

func (p *mockClosingProvider) Close() {
	p.closeCalls.Add(1)
	if p.onClose != nil {
		p.onClose()
	}
}

type mockSessionStoreProvider struct {
	*mockProvider
	sessionsCalls atomic.Int32
	sessions      map[string]map[string]any
	remotes       map[string]map[string]any
}

func (p *mockSessionStoreProvider) SessionsMap() map[string]map[string]any {
	p.sessionsCalls.Add(1)
	out := make(map[string]map[string]any, len(p.sessions))
	for k, v := range p.sessions {
		out[k] = deepCopyMap(v)
	}
	return out
}

func (p *mockSessionStoreProvider) RemotesMap() map[string]map[string]any {
	out := make(map[string]map[string]any, len(p.remotes))
	for k, v := range p.remotes {
		out[k] = deepCopyMap(v)
	}
	return out
}

func TestLoadLibraryReadParseErrorPreservesExistingFile(t *testing.T) {
	cfgDir := t.TempDir()
	libPath := filepath.Join(cfgDir, "library.json")
	corrupted := "{invalid json that must not be overwritten"
	if err := os.WriteFile(libPath, []byte(corrupted), 0o600); err != nil {
		t.Fatalf("write library.json: %v", err)
	}

	ctrl := NewColabOneController(ColabOneControllerConfig{
		ConfigDir:      cfgDir,
		Vault:          NewColabOneMemoryVault(),
		ServiceBundler: defaultMockBundler(),
		Background:     false,
	})
	defer ctrl.Close()

	afterBytes, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatalf("read library.json after load: %v", err)
	}
	if string(afterBytes) != corrupted {
		t.Fatalf("loadLibrary overwrote existing corrupted library.json: got %q, want %q", string(afterBytes), corrupted)
	}

	var sawWarn bool
	for _, e := range ctrl.activity.Snapshot() {
		if strings.Contains(e.Message, "读取本地模型库配置失败，已保留原文件未覆盖") && e.Level == "warning" {
			sawWarn = true
		}
	}
	if !sawWarn {
		t.Fatalf("expected warning log when library.json fails to parse, logs=%#v", ctrl.activity.Snapshot())
	}
}

func TestCloseCallsProviderCloserAndShutdownGuardsSuppressBogusDisconnect(t *testing.T) {
	// 1. Close() calls ProviderCloser.Close() after wg.Wait() and is idempotent.
	cfgDir := t.TempDir()
	vault := NewColabOneMemoryVault()
	baseProv := newMockProvider(vault)
	var bgExited atomic.Bool
	bgEntered := make(chan struct{}, 1)
	baseProv.accountFn = func(ctx context.Context) (map[string]any, error) {
		select {
		case bgEntered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		time.Sleep(15 * time.Millisecond)
		bgExited.Store(true)
		return nil, ctx.Err()
	}
	var closedAfterBG atomic.Bool
	closingProv := &mockClosingProvider{
		mockProvider: baseProv,
		onClose: func() {
			closedAfterBG.Store(bgExited.Load())
		},
	}
	ctrl := NewColabOneController(ColabOneControllerConfig{
		ConfigDir:        cfgDir,
		Vault:            vault,
		Auth:             newMockAuth(),
		Provider:         closingProv,
		ServiceBundler:   defaultMockBundler(),
		Background:       true,
		MonitorInterval:  5 * time.Millisecond,
		ObserverInterval: 5 * time.Millisecond,
	})
	select {
	case <-bgEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("background monitor loop did not invoke Account")
	}
	ctrl.Close()
	ctrl.Close() // idempotent

	if closingProv.closeCalls.Load() != 1 {
		t.Fatalf("expected ProviderCloser.Close called exactly once, got %d", closingProv.closeCalls.Load())
	}
	if !closedAfterBG.Load() {
		t.Fatal("expected ProviderCloser.Close to run AFTER background goroutines exited")
	}
	// Closing must NOT have recorded bogus connection failure errors.
	if errMsg := asString(ctrl.Snapshot()["error"]); errMsg != "" {
		t.Fatalf("Close() leaked bogus top-level error: %q", errMsg)
	}
	for _, entry := range ctrl.activity.Snapshot() {
		if strings.Contains(entry.Message, "连接检查失败") || strings.Contains(entry.Message, "连接中断") {
			t.Fatalf("Close() wrote bogus disconnect/error log: %#v", entry)
		}
	}

	// 2. MonitorOnce with cancelled ctx must NOT set disconnected or error, for active OR secondary.
	h := newTestHarness(t, false)
	defer h.ctrl.Close()
	h.provider.addManagedSession("ep-primary", "T4", "STANDARD", true)
	h.provider.addManagedSession("ep-secondary", "L4", "STANDARD", false)
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{"state": "ready", "id": "ep-primary", "protocol": 7},
	})
	h.ctrl.mu.Lock()
	h.ctrl.instanceStates["ep-secondary"] = &instanceCache{
		Runtime: map[string]any{"state": "ready", "id": "ep-secondary", "protocol": 7},
	}
	h.ctrl.mu.Unlock()

	cancelCtx, cancel := context.WithCancel(context.Background())
	h.provider.accountFn = func(ctx context.Context) (map[string]any, error) {
		cancel()
		return nil, context.Canceled
	}
	h.provider.heartbeatFn = func(ctx context.Context, endpoint string) error {
		return context.Canceled
	}
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		return nil, context.Canceled
	}
	h.ctrl.MonitorOnce(cancelCtx)

	if st := asString(asMap(h.ctrl.Snapshot()["runtime"])["state"]); st != "ready" {
		t.Fatalf("cancelled MonitorOnce mutated active runtime state to %q, want ready", st)
	}
	if errMsg := asString(h.ctrl.Snapshot()["error"]); errMsg != "" {
		t.Fatalf("cancelled MonitorOnce wrote error %q", errMsg)
	}
	h.ctrl.mu.Lock()
	secState := asString(h.ctrl.instanceStates["ep-secondary"].Runtime["state"])
	h.ctrl.mu.Unlock()
	if secState != "ready" {
		t.Fatalf("cancelled MonitorOnce mutated secondary runtime state to %q, want ready", secState)
	}

	// 3. Active Heartbeat cancellation mid-flight must NOT mark state disconnected.
	h2 := newTestHarness(t, false)
	defer h2.ctrl.Close()
	h2.provider.addManagedSession("ep-hb", "T4", "STANDARD", true)
	h2.ctrl.Update(map[string]any{
		"runtime": map[string]any{"state": "ready", "id": "ep-hb", "protocol": 7},
	})
	h2.ctrl.mu.Lock()
	h2.ctrl.lastAuth = nowUnixFloat()
	h2.ctrl.mu.Unlock()
	hbCtx, hbCancel := context.WithCancel(context.Background())
	h2.provider.heartbeatFn = func(ctx context.Context, endpoint string) error {
		hbCancel()
		return context.Canceled
	}
	h2.ctrl.MonitorOnce(hbCtx)
	if st := asString(asMap(h2.ctrl.Snapshot()["runtime"])["state"]); st != "ready" {
		t.Fatalf("cancelled Heartbeat marked runtime %q, want ready", st)
	}

	// 4. PollExternalMonitorOnce with cancelled ctx or stop set must NOT record error.
	hExt := newTestHarness(t, false)
	defer hExt.ctrl.Close()
	hExt.ctrl.Update(map[string]any{
		"account": map[string]any{
			"logged_in": true,
			"instances": []map[string]any{
				{"id": "ext-cancel", "managed": false, "accelerator": "T4", "variant": "GPU", "shape": "STANDARD"},
			},
		},
	})
	extCtx, extCancel := context.WithCancel(context.Background())
	hExt.provider.observeFn = func(ctx context.Context, endpoint string) (map[string]any, error) {
		extCancel()
		return nil, context.Canceled
	}
	if hExt.ctrl.PollExternalMonitorOnce(extCtx, true) {
		t.Fatal("expected cancelled external poll to return false")
	}
	hExt.ctrl.monitorMu.Lock()
	obs := hExt.ctrl.externalObservations["ext-cancel"]
	obsErr := asString(obs["error"])
	obsFail, _ := asInt(obs["consecutive_failures"])
	hExt.ctrl.monitorMu.Unlock()
	if obsErr != "" || obsFail != 0 {
		t.Fatalf("cancelled PollExternalMonitorOnce wrote stale error=%q failures=%d", obsErr, obsFail)
	}
}

func TestPollExternalMonitorGenerationGuardsAndRedactBeforeTruncate(t *testing.T) {
	setupExternal := func(t *testing.T) *testHarness {
		t.Helper()
		h := newTestHarness(t, false)
		h.ctrl.Update(map[string]any{
			"account": map[string]any{
				"logged_in": true,
				"instances": []map[string]any{
					{"id": "ext-1", "managed": false, "accelerator": "L4", "variant": "GPU", "shape": "STANDARD"},
					{"id": "ext-2", "managed": false, "accelerator": "T4", "variant": "GPU", "shape": "STANDARD"},
				},
			},
		})
		return h
	}

	// 1. Error returning after SetMonitorPaused must NOT write error or increment failures.
	h1 := setupExternal(t)
	defer h1.ctrl.Close()
	entered := make(chan struct{})
	release := make(chan struct{})
	h1.provider.observeFn = func(ctx context.Context, endpoint string) (map[string]any, error) {
		close(entered)
		<-release
		return nil, errors.New("observer closed by pause")
	}
	done := make(chan bool, 1)
	go func() {
		done <- h1.ctrl.PollExternalMonitorOnce(context.Background(), true)
	}()
	<-entered
	pausedTrue := true
	h1.ctrl.SetMonitorPaused(&pausedTrue)
	close(release)
	if <-done {
		t.Fatal("expected failed poll to return false")
	}
	h1.ctrl.monitorMu.Lock()
	obs1 := h1.ctrl.externalObservations["ext-1"]
	err1 := asString(obs1["error"])
	fail1, _ := asInt(obs1["consecutive_failures"])
	h1.ctrl.monitorMu.Unlock()
	if err1 != "" || fail1 != 0 {
		t.Fatalf("error after SetMonitorPaused mutated state: error=%q failures=%d", err1, fail1)
	}

	// 2. Error returning after RetryMonitor must NOT overwrite cleared error state.
	h2 := setupExternal(t)
	defer h2.ctrl.Close()
	entered2 := make(chan struct{})
	release2 := make(chan struct{})
	h2.provider.observeFn = func(ctx context.Context, endpoint string) (map[string]any, error) {
		close(entered2)
		<-release2
		return nil, errors.New("stale transient failure")
	}
	done2 := make(chan bool, 1)
	go func() {
		done2 <- h2.ctrl.PollExternalMonitorOnce(context.Background(), true)
	}()
	<-entered2
	h2.ctrl.RetryMonitor()
	close(release2)
	if <-done2 {
		t.Fatal("expected stale error poll to return false")
	}
	h2.ctrl.monitorMu.Lock()
	obs2 := h2.ctrl.externalObservations["ext-1"]
	err2 := asString(obs2["error"])
	fail2, _ := asInt(obs2["consecutive_failures"])
	h2.ctrl.monitorMu.Unlock()
	if err2 != "" || fail2 != 0 {
		t.Fatalf("error after RetryMonitor overwrote cleared state: error=%q failures=%d", err2, fail2)
	}

	// 3. Stale success returning after RetryMonitor must ALSO be discarded (monitorGen check).
	h2b := setupExternal(t)
	defer h2b.ctrl.Close()
	entered2b := make(chan struct{})
	release2b := make(chan struct{})
	h2b.provider.observeFn = func(ctx context.Context, endpoint string) (map[string]any, error) {
		close(entered2b)
		<-release2b
		return map[string]any{
			"source":  "remote_readonly",
			"runtime": map[string]any{"id": endpoint, "state": "ready"},
		}, nil
	}
	done2b := make(chan bool, 1)
	go func() {
		done2b <- h2b.ctrl.PollExternalMonitorOnce(context.Background(), true)
	}()
	<-entered2b
	h2b.ctrl.RetryMonitor()
	close(release2b)
	if <-done2b {
		t.Fatal("expected stale success after RetryMonitor to return false")
	}
	h2b.ctrl.monitorMu.Lock()
	_, hasRT := h2b.ctrl.externalObservations["ext-1"]["runtime"]
	h2b.ctrl.monitorMu.Unlock()
	if hasRT {
		t.Fatal("stale success after RetryMonitor should not populate observation runtime")
	}

	// 4. Error returning after SelectMonitor switch must NOT set error on previous target.
	h3 := setupExternal(t)
	defer h3.ctrl.Close()
	entered3 := make(chan struct{})
	release3 := make(chan struct{})
	h3.provider.observeFn = func(ctx context.Context, endpoint string) (map[string]any, error) {
		close(entered3)
		<-release3
		return nil, errors.New("aborted after switch")
	}
	done3 := make(chan bool, 1)
	go func() {
		done3 <- h3.ctrl.PollExternalMonitorOnce(context.Background(), true)
	}()
	<-entered3
	if _, err := h3.ctrl.SelectMonitor("ext-2"); err != nil {
		t.Fatalf("SelectMonitor(ext-2) failed: %v", err)
	}
	close(release3)
	if <-done3 {
		t.Fatal("expected error poll after SelectMonitor to return false")
	}
	h3.ctrl.monitorMu.Lock()
	obs3 := h3.ctrl.externalObservations["ext-1"]
	err3 := asString(obs3["error"])
	fail3, _ := asInt(obs3["consecutive_failures"])
	h3.ctrl.monitorMu.Unlock()
	if err3 != "" || fail3 != 0 {
		t.Fatalf("error after SelectMonitor polluted ext-1: error=%q failures=%d", err3, fail3)
	}

	// 5. Error message redacts tokens BEFORE truncating to 180 runes (no partial secret leak).
	h4 := setupExternal(t)
	defer h4.ctrl.Close()
	prefix := strings.Repeat("x", 168) + " "
	secret := "ya29.SuperSecretTokenCrossingThe180RuneBoundary1234567890"
	h4.provider.observeFn = func(ctx context.Context, endpoint string) (map[string]any, error) {
		return nil, errors.New(prefix + secret)
	}
	h4.ctrl.PollExternalMonitorOnce(context.Background(), true)
	monErr := asString(asMap(h4.ctrl.Snapshot()["monitor"])["error"])
	if strings.Contains(monErr, "ya29.") || strings.Contains(monErr, "SuperSecret") {
		t.Fatalf("monitor.error leaked truncated secret token: %q", monErr)
	}
	if !strings.Contains(monErr, "[凭据已隐藏]") {
		t.Fatalf("expected redacted placeholder in monitor.error, got %q", monErr)
	}
}

func TestErrTypeNameSanitizeNilMapAndNullBooleanOffset(t *testing.T) {
	// 1. errTypeName(errors.New("x")) returns "Error" (never unexported "errorString").
	if got := errTypeName(errors.New("x")); got != "Error" {
		t.Fatalf("errTypeName(errors.New(\"x\")) = %q, want \"Error\"", got)
	}

	// 2. SanitizeJSONValue preserves typed-nil map[string]any(nil) as nil.
	var nilMap map[string]any
	if got := SanitizeJSONValue(nilMap); got != nil {
		t.Fatalf("SanitizeJSONValue(map[string]any(nil)) = %#v, want nil", got)
	}
	wrapped := SanitizeJSONValue(map[string]any{"nested": nilMap}).(map[string]any)
	if wrapped["nested"] != nil {
		t.Fatalf("SanitizeJSONValue nested typed-nil map = %#v, want nil", wrapped["nested"])
	}

	// 3. asBoolDefault treats explicit nil value as false (matching Python bool(None) == False),
	//    while missing key still uses default.
	if asBoolDefault(map[string]any{"restore": nil}, "restore", true) {
		t.Fatal("asBoolDefault({restore: nil}, restore, true) = true, want false")
	}
	if !asBoolDefault(map[string]any{}, "restore", true) {
		t.Fatal("asBoolDefault({}, restore, true) = false, want true")
	}

	// 4. comfyui.output.asset with explicit "offset": null returns error.
	h := newTestHarness(t, false)
	defer h.ctrl.Close()
	_, err := h.ctrl.Command(context.Background(), "comfyui.output.asset", map[string]any{
		"id":     strings.Repeat("a", 32),
		"offset": nil,
	})
	if err == nil || !strings.Contains(err.Error(), "读取范围不合法") {
		t.Fatalf("comfyui.output.asset with offset=nil: got err=%v, want 读取范围不合法", err)
	}
}

func TestMonitorOncePrunesStaleStatesResetsStuckRuntimeAndGuardsUpgradeAttempted(t *testing.T) {
	// 1. Prunes stale instanceStates & upgradeAttempted and resets stuck ready/disconnected -> stopped.
	h := newTestHarness(t, false)
	defer h.ctrl.Close()
	h.ctrl.mu.Lock()
	h.ctrl.lastAuth = nowUnixFloat()
	h.ctrl.instanceStates["ep-stale"] = &instanceCache{
		Runtime: map[string]any{"state": "ready", "id": "ep-stale"},
	}
	h.ctrl.upgradeAttempted["ep-stale"] = true
	h.ctrl.data["runtime"] = map[string]any{"state": "ready", "id": "ep-stale"}
	h.ctrl.mu.Unlock()

	h.ctrl.MonitorOnce(context.Background())

	h.ctrl.mu.Lock()
	_, hasStaleState := h.ctrl.instanceStates["ep-stale"]
	_, hasStaleUpgrade := h.ctrl.upgradeAttempted["ep-stale"]
	rtState := asString(asMap(h.ctrl.data["runtime"])["state"])
	h.ctrl.mu.Unlock()
	if hasStaleState || hasStaleUpgrade {
		t.Fatalf("MonitorOnce failed to prune stale maps: state=%v upgrade=%v", hasStaleState, hasStaleUpgrade)
	}
	if rtState != "stopped" {
		t.Fatalf("MonitorOnce failed to reset stuck ready runtime to stopped: got %q", rtState)
	}

	// Also verify stuck "disconnected" -> "stopped" when sessions empty.
	h.ctrl.mu.Lock()
	h.ctrl.data["runtime"] = map[string]any{"state": "disconnected", "id": "ep-stale"}
	h.ctrl.mu.Unlock()
	h.ctrl.MonitorOnce(context.Background())
	if st := asString(asMap(h.ctrl.Snapshot()["runtime"])["state"]); st != "stopped" {
		t.Fatalf("MonitorOnce failed to reset stuck disconnected runtime to stopped: got %q", st)
	}

	// 2. When opBusy is true while active RefreshRemote fails, MonitorOnce must NOT set disconnected,
	//    and auto-upgrade must only set upgradeAttempted once opBusy is acquired.
	h2 := newTestHarness(t, false)
	defer h2.ctrl.Close()
	h2.provider.addManagedSession("ep-upg", "T4", "STANDARD", true)
	h2.ctrl.Update(map[string]any{
		"runtime": map[string]any{"state": "ready", "id": "ep-upg", "protocol": 7},
	})
	h2.ctrl.mu.Lock()
	h2.ctrl.lastAuth = nowUnixFloat()
	h2.ctrl.lastHeartbeat = nowUnixFloat()
	h2.ctrl.lastConfig = nowUnixFloat()
	h2.ctrl.mu.Unlock()

	// Simulate opBusy becoming true while RefreshRemote is running -> auto-upgrade rejected,
	// so upgradeAttempted["ep-upg"] must stay false!
	holdBusy := true
	h2.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if path == "snapshot" {
			if holdBusy {
				h2.ctrl.opBusy.Store(true)
			}
			return map[string]any{
				"runtime": map[string]any{
					"id":               "ep-upg",
					"state":            "ready",
					"protocol":         7,
					"service_revision": "stale00000000000",
					"tasks":            []any{},
				},
				"models": []any{},
			}, nil
		}
		return map[string]any{"ok": true}, nil
	}
	h2.ctrl.MonitorOnce(context.Background())
	h2.ctrl.opBusy.Store(false)

	h2.ctrl.mu.Lock()
	attemptedWhileBusy := h2.ctrl.upgradeAttempted["ep-upg"]
	h2.ctrl.mu.Unlock()
	if attemptedWhileBusy {
		t.Fatal("upgradeAttempted must NOT be set when asyncOperation failed to acquire opBusy")
	}

	// Next tick with opBusy free should acquire opBusy, set upgradeAttempted, and invoke UpgradeService.
	holdBusy = false
	h2.ctrl.MonitorOnce(context.Background())
	waitUntil(t, 2*time.Second, func() bool {
		h2.provider.mu.Lock()
		n := len(h2.provider.upgradeCalls)
		h2.provider.mu.Unlock()
		return n >= 1 && !h2.ctrl.IsBusy()
	})
	h2.ctrl.mu.Lock()
	attemptedAfter := h2.ctrl.upgradeAttempted["ep-upg"]
	h2.ctrl.mu.Unlock()
	if !attemptedAfter {
		t.Fatal("expected upgradeAttempted set once auto-upgrade acquired opBusy")
	}

	// 3. Active error occurring while opBusy transitions true must NOT clobber runtime to disconnected.
	h3 := newTestHarness(t, false)
	defer h3.ctrl.Close()
	h3.provider.addManagedSession("ep-busy-err", "T4", "STANDARD", true)
	h3.ctrl.Update(map[string]any{
		"runtime": map[string]any{"state": "ready", "id": "ep-busy-err", "protocol": 7},
	})
	h3.ctrl.mu.Lock()
	h3.ctrl.lastAuth = nowUnixFloat()
	h3.ctrl.lastHeartbeat = nowUnixFloat()
	h3.ctrl.mu.Unlock()
	h3.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		// Simulate concurrent Release starting and marking runtime "releasing" while opBusy=true.
		h3.ctrl.opBusy.Store(true)
		h3.ctrl.Update(map[string]any{"runtime": map[string]any{"state": "releasing", "id": "ep-busy-err"}})
		return nil, errors.New("connection closed by release")
	}
	h3.ctrl.MonitorOnce(context.Background())
	h3.ctrl.opBusy.Store(false)
	if st := asString(asMap(h3.ctrl.Snapshot()["runtime"])["state"]); st != "releasing" {
		t.Fatalf("MonitorOnce clobbered releasing state to %q while opBusy was set", st)
	}
}

func TestStartRollback90sTimeoutNoZombieResurrectionAndSinglePassSnapshot(t *testing.T) {
	// 1. Start() rollback uses 90s timeout and deletes orphan from instanceStates AND upgradeAttempted.
	hRoll := newTestHarness(t, false)
	defer hRoll.ctrl.Close()
	var rollbackDeadline time.Duration
	hRoll.provider.startFn = func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
		hRoll.provider.addManagedSession("ep-rb", "T4", "STANDARD", true)
		hRoll.ctrl.mu.Lock()
		hRoll.ctrl.instanceStates["ep-rb"] = &instanceCache{Runtime: map[string]any{"state": "ready", "id": "ep-rb"}}
		hRoll.ctrl.upgradeAttempted["ep-rb"] = true
		hRoll.ctrl.mu.Unlock()
		return map[string]any{"endpoint": "ep-rb", "gpu": "T4"}, nil
	}
	hRoll.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if path == "configure" {
			return nil, errors.New("configure failed")
		}
		return map[string]any{
			"runtime": map[string]any{"state": "ready", "id": "ep-rb", "protocol": 7},
			"models":  []any{},
		}, nil
	}
	hRoll.provider.releaseFn = func(ctx context.Context, endpoint string, onLog LogFunc) (map[string]any, error) {
		if dl, ok := ctx.Deadline(); ok {
			rollbackDeadline = time.Until(dl)
		}
		hRoll.provider.Clear(endpoint)
		return map[string]any{"ok": true}, nil
	}
	if err := hRoll.ctrl.Start(context.Background(), false, "T4"); err == nil {
		t.Fatal("expected Start to fail on configure error")
	}
	if rollbackDeadline < 60*time.Second || rollbackDeadline > 95*time.Second {
		t.Fatalf("expected ~90s rollback Release deadline, got %v", rollbackDeadline)
	}
	hRoll.ctrl.mu.Lock()
	_, hasState := hRoll.ctrl.instanceStates["ep-rb"]
	_, hasUpgrade := hRoll.ctrl.upgradeAttempted["ep-rb"]
	hRoll.ctrl.mu.Unlock()
	if hasState || hasUpgrade {
		t.Fatalf("Start rollback left stale entries: state=%v upgrade=%v", hasState, hasUpgrade)
	}

	// 2. Release & ReleaseAll prune upgradeAttempted alongside instanceStates.
	hRel := newTestHarness(t, false)
	defer hRel.ctrl.Close()
	hRel.provider.addManagedSession("ep-rel-1", "T4", "STANDARD", true)
	hRel.ctrl.mu.Lock()
	hRel.ctrl.instanceStates["ep-rel-1"] = &instanceCache{Runtime: map[string]any{"state": "ready", "id": "ep-rel-1"}}
	hRel.ctrl.upgradeAttempted["ep-rel-1"] = true
	hRel.ctrl.mu.Unlock()
	if err := hRel.ctrl.Release(context.Background(), true, "ep-rel-1"); err != nil {
		t.Fatalf("Release failed: %v", err)
	}
	hRel.ctrl.mu.Lock()
	_, relState := hRel.ctrl.instanceStates["ep-rel-1"]
	_, relUpgrade := hRel.ctrl.upgradeAttempted["ep-rel-1"]
	hRel.ctrl.mu.Unlock()
	if relState || relUpgrade {
		t.Fatalf("Release left stale entries: state=%v upgrade=%v", relState, relUpgrade)
	}

	hRel.provider.addManagedSession("ep-rel-all", "T4", "STANDARD", true)
	hRel.ctrl.mu.Lock()
	hRel.ctrl.instanceStates["ep-rel-all"] = &instanceCache{Runtime: map[string]any{"state": "ready", "id": "ep-rel-all"}}
	hRel.ctrl.upgradeAttempted["ep-rel-all"] = true
	hRel.ctrl.mu.Unlock()
	hRel.provider.accountFn = func(ctx context.Context) (map[string]any, error) {
		sessions := hRel.ctrl.vaultSessions()
		insts := make([]map[string]any, 0, len(sessions))
		for ep := range sessions {
			insts = append(insts, map[string]any{"id": ep, "managed": true, "accelerator": "T4", "variant": "GPU"})
		}
		return map[string]any{"logged_in": true, "assignments": len(insts), "instances": insts}, nil
	}
	planRes, err := hRel.ctrl.PreviewReleaseAll(context.Background())
	if err != nil {
		t.Fatalf("PreviewReleaseAll failed: %v", err)
	}
	if err := hRel.ctrl.ReleaseAll(context.Background(), asString(planRes["id"]), true, nil, false); err != nil {
		t.Fatalf("ReleaseAll failed: %v", err)
	}
	hRel.ctrl.mu.Lock()
	_, relAllState := hRel.ctrl.instanceStates["ep-rel-all"]
	_, relAllUpgrade := hRel.ctrl.upgradeAttempted["ep-rel-all"]
	hRel.ctrl.mu.Unlock()
	if relAllState || relAllUpgrade {
		t.Fatalf("ReleaseAll left stale entries: state=%v upgrade=%v", relAllState, relAllUpgrade)
	}

	// 3. Read/sync paths must NOT resurrect released endpoint into instanceStates.
	hZombie := newTestHarness(t, false)
	defer hZombie.ctrl.Close()
	hZombie.provider.addManagedSession("ep-active", "T4", "STANDARD", true)
	ghostEP := "ep-ghost-released"
	_ = hZombie.ctrl.ConfigureRemote(context.Background(), ghostEP)
	_ = hZombie.ctrl.runtimeSupportsAPIAliases(ghostEP)
	hZombie.ctrl.syncRemoteAPIIDOverrides(context.Background(), ghostEP, false)
	_ = hZombie.ctrl.ensureRemoteModel(context.Background(), "gemma4-31b", ghostEP)
	hZombie.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if path == "status" {
			return map[string]any{"loaded_model": "gemma4-31b"}, nil
		}
		return map[string]any{"ok": true}, nil
	}
	_, _ = hZombie.ctrl.prepareRemoteForUpgrade(context.Background(), ghostEP)
	hZombie.ctrl.mu.Lock()
	_, resurrected := hZombie.ctrl.instanceStates[ghostEP]
	hZombie.ctrl.mu.Unlock()
	if resurrected {
		t.Fatal("read/sync helper resurrected released endpoint into instanceStates")
	}

	// 4. PreviewReleaseAll deep-copies plan.Instances so mutating returned slice cannot affect plan.
	hPlan := newTestHarness(t, false)
	defer hPlan.ctrl.Close()
	hPlan.provider.addManagedSession("ep-plan-1", "T4", "STANDARD", true)
	hPlan.provider.accountFn = func(ctx context.Context) (map[string]any, error) {
		return map[string]any{
			"logged_in":   true,
			"assignments": 1,
			"instances": []map[string]any{
				{"id": "ep-plan-1", "managed": true, "accelerator": "T4", "variant": "GPU"},
			},
		}, nil
	}
	preview, err := hPlan.ctrl.PreviewReleaseAll(context.Background())
	if err != nil {
		t.Fatalf("PreviewReleaseAll failed: %v", err)
	}
	returnedInstances := asMapSlice(preview["instances"])
	returnedInstances[0]["id"] = "mutated-id"
	hPlan.ctrl.mu.Lock()
	storedID := asString(hPlan.ctrl.releasePlan.Instances[0]["id"])
	hPlan.ctrl.mu.Unlock()
	if storedID != "ep-plan-1" {
		t.Fatalf("PreviewReleaseAll did not deep-copy plan.Instances: storedID=%q", storedID)
	}

	// 5. Snapshot() captures sessions/remotes/activeEP ONCE (no torn snapshot, single SessionsMap call).
	vault := NewColabOneMemoryVault()
	_ = vault.Set("active_endpoint", "ep-snap-1")
	storeProv := &mockSessionStoreProvider{
		mockProvider: newMockProvider(vault),
		sessions: map[string]map[string]any{
			"ep-snap-1": {"endpoint": "ep-snap-1", "accelerator": "T4", "variant": "GPU", "machine_shape": "STANDARD"},
		},
		remotes: map[string]map[string]any{
			"ep-snap-1": {"endpoint": "ep-snap-1", "gpu": "T4"},
		},
	}
	snapCtrl := NewColabOneController(ColabOneControllerConfig{
		ConfigDir:      t.TempDir(),
		Vault:          vault,
		Auth:           newMockAuth(),
		Provider:       storeProv,
		ServiceBundler: defaultMockBundler(),
		Background:     false,
	})
	defer snapCtrl.Close()
	storeProv.sessionsCalls.Store(0)
	snap := snapCtrl.Snapshot()
	if calls := storeProv.sessionsCalls.Load(); calls != 1 {
		t.Fatalf("expected Snapshot() to call SessionsMap() exactly once, got %d", calls)
	}
	if snap["active_endpoint"] != "ep-snap-1" {
		t.Fatalf("unexpected active_endpoint: %#v", snap["active_endpoint"])
	}
	insts := asMapSlice(asMap(snap["account"])["instances"])
	if len(insts) != 1 || insts[0]["id"] != "ep-snap-1" || insts[0]["active"] != true {
		t.Fatalf("unexpected enriched instances in Snapshot(): %#v", insts)
	}
}

// ---------------------------------------------------------------------------
// 12. Restored high-value historical regression tests
// ---------------------------------------------------------------------------

func TestUpgradeServiceForceTeardownStartingComfyFailureRefreshAndPayloads(t *testing.T) {
	// (a) Force teardown stops ComfyUI when comfyui.state=="starting", canvas.state=="starting",
	//     or comfyui.running==true, and refreshes remote state when provider.UpgradeService fails.
	comfyCases := []struct {
		name  string
		comfy map[string]any
	}{
		{
			name: "comfyui_starting",
			comfy: map[string]any{
				"running": false,
				"state":   "starting",
				"canvas":  map[string]any{"enabled": false, "state": "stopped"},
			},
		},
		{
			name: "canvas_starting",
			comfy: map[string]any{
				"running": false,
				"state":   "stopped",
				"canvas":  map[string]any{"enabled": false, "state": "starting"},
			},
		},
		{
			name: "comfyui_running",
			comfy: map[string]any{
				"running": true,
				"state":   "running",
				"canvas":  map[string]any{"enabled": false, "state": "stopped"},
			},
		},
	}

	for _, tc := range comfyCases {
		t.Run("a_"+tc.name, func(t *testing.T) {
			h := newTestHarness(t, false)
			h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
			h.ctrl.Update(map[string]any{
				"runtime": map[string]any{
					"state":    "ready",
					"id":       "ep-1",
					"protocol": 7,
					"comfyui":  deepCopyMap(tc.comfy),
				},
			})

			var mu sync.Mutex
			comfyStopped := false
			var events []string
			var stopEPs []string
			var snapshotEPs []string

			h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
				mu.Lock()
				events = append(events, method+" "+path)
				switch {
				case method == "GET" && path == "status":
					stopped := comfyStopped
					mu.Unlock()
					curComfy := deepCopyMap(tc.comfy)
					if stopped {
						curComfy = map[string]any{
							"running": false,
							"state":   "stopped",
							"canvas":  map[string]any{"enabled": false, "state": "stopped"},
						}
					}
					return map[string]any{
						"public_api":    map[string]any{"enabled": false, "state": "stopped"},
						"tasks":         []any{},
						"comfyui":       curComfy,
						"loaded_model":  "",
						"active_engine": nil,
					}, nil
				case method == "POST" && path == "comfyui/stop":
					comfyStopped = true
					stopEPs = append(stopEPs, opts.Endpoint)
					mu.Unlock()
					return map[string]any{"ok": true}, nil
				case method == "GET" && path == "snapshot":
					snapshotEPs = append(snapshotEPs, opts.Endpoint)
					mu.Unlock()
					return map[string]any{
						"runtime": map[string]any{
							"state":    "ready",
							"id":       "ep-1",
							"protocol": 7,
							"comfyui": map[string]any{
								"running": false,
								"state":   "stopped",
								"canvas":  map[string]any{"enabled": false, "state": "stopped"},
							},
						},
						"models": []any{},
					}, nil
				default:
					mu.Unlock()
					return map[string]any{"ok": true}, nil
				}
			}
			h.provider.upgradeFn = func(ctx context.Context, endpoint string, onLog LogFunc) error {
				mu.Lock()
				events = append(events, "PROVIDER upgrade_service")
				mu.Unlock()
				return errors.New("remote bundle upload failed")
			}

			err := h.ctrl.UpgradeService(context.Background(), true, true)
			if err == nil || !strings.Contains(err.Error(), "remote bundle upload failed") {
				t.Fatalf("expected upgrade failure error, got %v", err)
			}

			idxOf := func(needle string) int {
				for i, ev := range events {
					if ev == needle {
						return i
					}
				}
				return -1
			}
			stopIdx := idxOf("POST comfyui/stop")
			upIdx := idxOf("PROVIDER upgrade_service")
			snapIdx := idxOf("GET snapshot")
			if stopIdx < 0 || upIdx < 0 || stopIdx >= upIdx {
				t.Fatalf("expected POST comfyui/stop before provider.UpgradeService, events=%#v", events)
			}
			if len(stopEPs) != 1 || stopEPs[0] != "ep-1" {
				t.Fatalf("expected comfyui/stop pinned to ep-1, got %#v", stopEPs)
			}
			if snapIdx < 0 || snapIdx <= upIdx {
				t.Fatalf("expected RefreshRemote (GET snapshot) after provider.UpgradeService failure, events=%#v", events)
			}
			if len(snapshotEPs) != 1 || snapshotEPs[0] != "ep-1" {
				t.Fatalf("expected RefreshRemote pinned to ep-1, got %#v", snapshotEPs)
			}
			rtComfy := asMap(asMap(h.ctrl.Snapshot()["runtime"])["comfyui"])
			if asBool(rtComfy["running"]) || asString(rtComfy["state"]) != "stopped" {
				t.Fatalf("expected UI runtime refreshed after upgrade failure, got comfyui=%#v", rtComfy)
			}
		})
	}

	// (b) When prepareRemoteForUpgrade fails midway (POST unload fails after POST public-api/stop),
	//     RefreshRemote(ctx, targetEndpoint) is still called and provider.UpgradeService is not.
	t.Run("b_prepare_failure_midway_triggers_refresh", func(t *testing.T) {
		h := newTestHarness(t, false)
		h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
		h.ctrl.Update(map[string]any{
			"runtime": map[string]any{
				"state":        "ready",
				"id":           "ep-1",
				"protocol":     7,
				"loaded_model": "gemma4-31b",
				"public_api":   map[string]any{"enabled": true, "state": "ready"},
			},
		})

		publicActive := true
		var events []string
		var snapshotEPs []string

		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			events = append(events, method+" "+path)
			switch {
			case method == "GET" && path == "status":
				return map[string]any{
					"public_api":    map[string]any{"enabled": publicActive, "state": map[bool]string{true: "ready", false: "stopped"}[publicActive]},
					"tasks":         []any{},
					"comfyui":       map[string]any{"running": false, "state": "stopped"},
					"loaded_model":  "gemma4-31b",
					"active_engine": "llama",
				}, nil
			case method == "POST" && path == "public-api/stop":
				publicActive = false
				return map[string]any{"ok": true}, nil
			case method == "POST" && path == "unload":
				return nil, errors.New("unload failed midway")
			case method == "GET" && path == "snapshot":
				snapshotEPs = append(snapshotEPs, opts.Endpoint)
				return map[string]any{
					"runtime": map[string]any{
						"state":        "ready",
						"id":           "ep-1",
						"protocol":     7,
						"loaded_model": "gemma4-31b",
						"public_api":   map[string]any{"enabled": false, "state": "stopped"},
					},
					"models": []any{},
				}, nil
			default:
				return map[string]any{"ok": true}, nil
			}
		}

		err := h.ctrl.UpgradeService(context.Background(), true, true)
		if err == nil || !strings.Contains(err.Error(), "unload failed midway") {
			t.Fatalf("expected midway unload error, got %v", err)
		}
		if len(h.provider.upgradeCalls) != 0 {
			t.Fatalf("provider.UpgradeService must not be called when prepareRemoteForUpgrade fails, got %#v", h.provider.upgradeCalls)
		}
		if len(snapshotEPs) != 1 || snapshotEPs[0] != "ep-1" {
			t.Fatalf("expected RefreshRemote(ctx, ep-1) on prepare failure, snapshotEPs=%#v events=%#v", snapshotEPs, events)
		}
		if asBool(asMap(asMap(h.ctrl.Snapshot()["runtime"])["public_api"])["enabled"]) {
			t.Fatalf("expected refreshed UI state with public_api disabled, got %#v", h.ctrl.Snapshot()["runtime"])
		}
	})

	// (c) Auto-restore payloads on UpgradeService(ctx, true, true) success preserve model specs
	//     (context_length: 131072, temperature: 0.7) and strip vault "legacy_extra" from public-api/start.
	t.Run("c_auto_restore_payloads", func(t *testing.T) {
		h := newTestHarness(t, false)
		h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
		tok := validCFToken()
		apiKey := "co_" + strings.Repeat("Z", 44)
		_ = h.vault.Set("public_api", map[string]any{
			"mode":         "named",
			"domain":       "https://api.example.com",
			"tunnel_token": tok,
			"api_key":      apiKey,
			"legacy_extra": "ignored",
		})

		publicActive := true
		loadedModel := "gemma4-31b"
		var loadBodyJSON []byte
		var apiStartBodyJSON []byte

		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			switch {
			case method == "GET" && path == "status":
				return map[string]any{
					"public_api":     map[string]any{"enabled": publicActive, "state": map[bool]string{true: "ready", false: "stopped"}[publicActive]},
					"tasks":          []any{},
					"comfyui":        map[string]any{"running": false, "state": "stopped"},
					"loaded_model":   loadedModel,
					"active_options": map[string]any{"context_length": 131072, "temperature": 0.7, "ignored_opt": "drop"},
					"active_engine":  map[bool]any{true: "llama", false: nil}[loadedModel != ""],
				}, nil
			case method == "POST" && path == "public-api/stop":
				publicActive = false
				return map[string]any{"ok": true}, nil
			case method == "POST" && path == "unload":
				loadedModel = ""
				return map[string]any{"ok": true}, nil
			case method == "POST" && path == "models/gemma4-31b/load":
				b, err := json.Marshal(body)
				if err != nil {
					t.Errorf("marshal model load payload: %v", err)
				}
				loadBodyJSON = b
				return map[string]any{"ok": true}, nil
			case method == "POST" && path == "public-api/start":
				b, err := json.Marshal(body)
				if err != nil {
					t.Errorf("marshal public-api/start payload: %v", err)
				}
				apiStartBodyJSON = b
				return map[string]any{"ok": true}, nil
			case method == "GET" && path == "snapshot":
				return map[string]any{
					"runtime": map[string]any{"state": "ready", "id": "ep-1", "protocol": 7},
					"models":  h.ctrl.Presets(),
				}, nil
			default:
				return map[string]any{"ok": true}, nil
			}
		}

		if err := h.ctrl.UpgradeService(context.Background(), true, true); err != nil {
			t.Fatalf("UpgradeService(true, true) failed: %v", err)
		}

		if len(loadBodyJSON) == 0 {
			t.Fatal("expected POST models/gemma4-31b/load to be called")
		}
		var loadBody map[string]any
		if err := decodeJSONWithNumbers(loadBodyJSON, &loadBody); err != nil {
			t.Fatalf("decode model load body: %v", err)
		}
		ctxLen, okCtx := asExactInt(loadBody["context_length"])
		temp, okTemp := asFloat(loadBody["temperature"])
		if !okCtx || ctxLen != 131072 || !okTemp || math.Abs(temp-0.7) > 1e-9 {
			t.Fatalf("unexpected model load restore payload: %s", string(loadBodyJSON))
		}
		if _, hasIgnored := loadBody["ignored_opt"]; hasIgnored || len(loadBody) != 2 {
			t.Fatalf("expected only context_length and temperature in model load payload, got %s", string(loadBodyJSON))
		}

		if len(apiStartBodyJSON) == 0 {
			t.Fatal("expected POST public-api/start to be called")
		}
		var apiBody map[string]any
		if err := decodeJSONWithNumbers(apiStartBodyJSON, &apiBody); err != nil {
			t.Fatalf("decode public-api/start body: %v", err)
		}
		if _, hasLegacy := apiBody["legacy_extra"]; hasLegacy {
			t.Fatalf("expected legacy_extra stripped from public-api/start payload, got %s", string(apiStartBodyJSON))
		}
		if apiBody["mode"] != "named" ||
			apiBody["domain"] != "https://api.example.com" ||
			apiBody["tunnel_token"] != tok ||
			apiBody["api_key"] != apiKey ||
			len(apiBody) != 4 {
			t.Fatalf("unexpected public-api/start restore payload: %s", string(apiStartBodyJSON))
		}
	})
}

func TestRestoreComfyCanvasWatcherAllExitBranches(t *testing.T) {
	assertNoCanvasTimeoutLog := func(t *testing.T, ctrl *ColabOneController) {
		t.Helper()
		for _, entry := range ctrl.activity.Snapshot() {
			if strings.Contains(entry.Message, "等待 ComfyUI 启动超时") {
				t.Fatalf("watcher unexpectedly hit timeout: %#v", entry)
			}
		}
	}

	tok := validCFToken()
	savedCanvasPayload := map[string]any{
		"mode":         "named",
		"domain":       "https://canvas.example.com",
		"tunnel_token": tok,
	}
	taskID := strings.Repeat("d", 32)

	// (a) comfyui.running flips false -> true -> async watcher calls POST comfyui/canvas/start with saved config.
	t.Run("a_running_flips_false_to_true_starts_canvas", func(t *testing.T) {
		h := newTestHarness(t, false)
		h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
		_ = h.vault.Set("comfyui_canvas", deepCopyMap(savedCanvasPayload))

		var mu sync.Mutex
		comfyRunning := true
		canvasEnabled := true
		comfyStartRequested := false
		postStartStatusPolls := 0
		var canvasStartBody map[string]any
		var canvasStartEP string
		postCanvasSnapshot := false

		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			mu.Lock()
			defer mu.Unlock()
			switch {
			case method == "GET" && path == "status":
				if comfyStartRequested && !comfyRunning {
					postStartStatusPolls++
					// Poll 1: immediate check inside restoreRemoteAfterUpgrade -> still starting (spawns watcher).
					// Poll 2: first watcher poll -> still starting.
					// Poll 3: second watcher poll -> flips false -> true!
					if postStartStatusPolls >= 3 {
						comfyRunning = true
					}
				}
				state := "stopped"
				var tasks []map[string]any
				if comfyRunning {
					state = "running"
				} else if comfyStartRequested {
					state = "starting"
					tasks = []map[string]any{{"id": taskID, "kind": "comfy_start", "state": "running"}}
				}
				canvasState := "stopped"
				if canvasEnabled {
					canvasState = "ready"
				}
				return map[string]any{
					"public_api": map[string]any{"enabled": false, "state": "stopped"},
					"tasks":      tasks,
					"comfyui": map[string]any{
						"running": comfyRunning,
						"state":   state,
						"canvas":  map[string]any{"enabled": canvasEnabled, "state": canvasState},
					},
					"loaded_model":  "",
					"active_engine": map[bool]any{true: "comfyui", false: nil}[comfyRunning],
				}, nil
			case method == "POST" && path == "comfyui/stop":
				comfyRunning = false
				canvasEnabled = false
				return map[string]any{"ok": true}, nil
			case method == "POST" && path == "comfyui/start":
				comfyStartRequested = true
				comfyRunning = false
				return map[string]any{"id": taskID, "kind": "comfy_start", "state": "running"}, nil
			case method == "POST" && path == "comfyui/canvas/start":
				canvasEnabled = true
				canvasStartBody = deepCopyMap(asMap(body))
				canvasStartEP = opts.Endpoint
				return map[string]any{"enabled": true, "state": "starting"}, nil
			case method == "GET" && path == "snapshot":
				if canvasStartBody != nil {
					postCanvasSnapshot = true
				}
				return map[string]any{
					"runtime": map[string]any{"state": "ready", "id": "ep-1", "protocol": 7},
					"models":  []any{},
				}, nil
			default:
				return map[string]any{"ok": true}, nil
			}
		}

		if err := h.ctrl.UpgradeService(context.Background(), true, true); err != nil {
			t.Fatalf("UpgradeService failed: %v", err)
		}
		waitUntil(t, 2*time.Second, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return canvasStartBody != nil && postCanvasSnapshot
		})
		mu.Lock()
		gotBody := deepCopyMap(canvasStartBody)
		gotEP := canvasStartEP
		mu.Unlock()
		if gotEP != "ep-1" || gotBody["mode"] != "named" || gotBody["domain"] != "https://canvas.example.com" || gotBody["tunnel_token"] != tok {
			t.Fatalf("unexpected async canvas restore: ep=%q body=%#v", gotEP, gotBody)
		}
		assertNoCanvasTimeoutLog(t, h.ctrl)
		h.ctrl.Close()
	})

	// (b) User stops ComfyUI (state == "stopped", no running start task) -> exits cleanly without starting canvas.
	t.Run("b_user_stops_comfyui", func(t *testing.T) {
		h := newTestHarness(t, false)
		h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
		var polls, canvasStarts int
		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "comfyui/canvas/start" {
				canvasStarts++
				return map[string]any{"ok": true}, nil
			}
			polls++
			if polls == 1 {
				return map[string]any{
					"comfyui": map[string]any{"running": false, "state": "starting", "canvas": map[string]any{"enabled": false, "state": "stopped"}},
					"tasks":   []map[string]any{{"id": taskID, "kind": "comfy_start", "state": "running"}},
				}, nil
			}
			return map[string]any{
				"comfyui": map[string]any{"running": false, "state": "stopped", "canvas": map[string]any{"enabled": false, "state": "stopped"}},
				"tasks":   []map[string]any{},
			}, nil
		}

		started := time.Now()
		h.ctrl.RestoreComfyCanvasWhenReady(context.Background(), savedCanvasPayload, "ep-1", taskID, 5*time.Second)
		if time.Since(started) > time.Second {
			t.Fatalf("watcher exited too slowly: %v", time.Since(started))
		}
		if polls != 2 || canvasStarts != 0 {
			t.Fatalf("expected clean exit on poll 2 without canvas start: polls=%d canvasStarts=%d", polls, canvasStarts)
		}
		assertNoCanvasTimeoutLog(t, h.ctrl)
	})

	// (c) Canvas already enabled or state == "starting" -> exits cleanly without calling POST comfyui/canvas/start.
	for _, tc := range []struct {
		name   string
		canvas map[string]any
	}{
		{name: "enabled", canvas: map[string]any{"enabled": true, "state": "stopped"}},
		{name: "starting", canvas: map[string]any{"enabled": false, "state": "starting"}},
	} {
		t.Run("c_canvas_already_"+tc.name, func(t *testing.T) {
			h := newTestHarness(t, false)
			h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
			var polls, canvasStarts int
			h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
				if path == "comfyui/canvas/start" {
					canvasStarts++
					return map[string]any{"ok": true}, nil
				}
				polls++
				if polls == 1 {
					return map[string]any{
						"comfyui": map[string]any{"running": false, "state": "starting", "canvas": map[string]any{"enabled": false, "state": "stopped"}},
						"tasks":   []map[string]any{{"id": taskID, "kind": "comfy_start", "state": "running"}},
					}, nil
				}
				return map[string]any{
					"comfyui": map[string]any{"running": true, "state": "running", "canvas": deepCopyMap(tc.canvas)},
					"tasks":   []map[string]any{},
				}, nil
			}

			started := time.Now()
			h.ctrl.RestoreComfyCanvasWhenReady(context.Background(), savedCanvasPayload, "ep-1", taskID, 5*time.Second)
			if time.Since(started) > time.Second {
				t.Fatalf("watcher exited too slowly: %v", time.Since(started))
			}
			if polls != 2 || canvasStarts != 0 {
				t.Fatalf("expected clean exit without duplicate canvas start: polls=%d canvasStarts=%d", polls, canvasStarts)
			}
			assertNoCanvasTimeoutLog(t, h.ctrl)
		})
	}

	// (d) ComfyUI start task enters failed or cancelled -> exits cleanly without starting canvas.
	for _, taskState := range []string{"failed", "cancelled"} {
		t.Run("d_task_"+taskState, func(t *testing.T) {
			h := newTestHarness(t, false)
			h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
			var polls, canvasStarts int
			h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
				if path == "comfyui/canvas/start" {
					canvasStarts++
					return map[string]any{"ok": true}, nil
				}
				polls++
				curTaskState := "running"
				if polls >= 2 {
					curTaskState = taskState
				}
				return map[string]any{
					// Keep comfyui.state == "starting" so exit is strictly driven by the task state branch.
					"comfyui": map[string]any{"running": false, "state": "starting", "canvas": map[string]any{"enabled": false, "state": "stopped"}},
					"tasks":   []map[string]any{{"id": taskID, "kind": "comfy_start", "state": curTaskState}},
				}, nil
			}

			started := time.Now()
			h.ctrl.RestoreComfyCanvasWhenReady(context.Background(), savedCanvasPayload, "ep-1", taskID, 5*time.Second)
			if time.Since(started) > time.Second {
				t.Fatalf("watcher exited too slowly: %v", time.Since(started))
			}
			if polls != 2 || canvasStarts != 0 {
				t.Fatalf("expected clean exit on task %s: polls=%d canvasStarts=%d", taskState, polls, canvasStarts)
			}
			assertNoCanvasTimeoutLog(t, h.ctrl)
		})
	}

	// (e) Target endpoint disappears from vault sessions mid-watch -> exits cleanly without starting canvas.
	t.Run("e_endpoint_disappears_mid_watch", func(t *testing.T) {
		h := newTestHarness(t, false)
		h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
		var polls, canvasStarts int
		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "comfyui/canvas/start" {
				canvasStarts++
				return map[string]any{"ok": true}, nil
			}
			polls++
			// Remove ep-1 from vault sessions mid-watch so the next loop iteration exits before any RPC.
			h.provider.Clear("ep-1")
			return map[string]any{
				"comfyui": map[string]any{"running": false, "state": "starting", "canvas": map[string]any{"enabled": false, "state": "stopped"}},
				"tasks":   []map[string]any{{"id": taskID, "kind": "comfy_start", "state": "running"}},
			}, nil
		}

		started := time.Now()
		h.ctrl.RestoreComfyCanvasWhenReady(context.Background(), savedCanvasPayload, "ep-1", taskID, 5*time.Second)
		if time.Since(started) > time.Second {
			t.Fatalf("watcher exited too slowly: %v", time.Since(started))
		}
		if polls != 1 || canvasStarts != 0 {
			t.Fatalf("expected watcher to stop immediately once endpoint left vault: polls=%d canvasStarts=%d", polls, canvasStarts)
		}
		assertNoCanvasTimeoutLog(t, h.ctrl)
	})
}

func TestReleaseAllNonForceSyncGateAndRemainingAssignmentsError(t *testing.T) {
	// (a) force=false calls POST prepare-release on owned instances (skipping unmanaged)
	//     and aborts BEFORE calling provider.ReleaseAll when prepare-release fails.
	t.Run("a_non_force_prepare_release_gate", func(t *testing.T) {
		h := newTestHarness(t, false)
		h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
		h.provider.addManagedSession("ep-2", "A100", "STANDARD", false)
		h.provider.accountFn = func(ctx context.Context) (map[string]any, error) {
			return map[string]any{
				"logged_in":   true,
				"assignments": 3,
				"rate":        5.0,
				"instances": []map[string]any{
					{"id": "ep-1", "managed": true},
					{"id": "ep-2", "managed": true},
					{"id": "ep-ext", "managed": false},
				},
			}, nil
		}
		planRes, err := h.ctrl.PreviewReleaseAll(context.Background())
		if err != nil {
			t.Fatalf("PreviewReleaseAll failed: %v", err)
		}
		planID := asString(planRes["id"])

		var prepareEndpoints []string
		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if method == "POST" && path == "prepare-release" {
				prepareEndpoints = append(prepareEndpoints, opts.Endpoint)
				if opts.Endpoint == "ep-2" {
					return nil, errors.New("远端云盘同步未完成")
				}
			}
			return map[string]any{"ok": true}, nil
		}

		err = h.ctrl.ReleaseAll(context.Background(), planID, false, nil, false)
		if err == nil || !strings.Contains(err.Error(), "远端云盘同步未完成") {
			t.Fatalf("expected prepare-release gate error, got %v", err)
		}
		if len(prepareEndpoints) != 2 || prepareEndpoints[0] != "" || prepareEndpoints[1] != "ep-2" {
			t.Fatalf("expected prepare-release on active (\"\") then ep-2 only, got %#v", prepareEndpoints)
		}
		if len(h.provider.releaseAllCalls) != 0 {
			t.Fatalf("provider.ReleaseAll must not be called when prepare-release fails, got %#v", h.provider.releaseAllCalls)
		}
		sessions := h.ctrl.vaultSessions()
		if _, ok1 := sessions["ep-1"]; !ok1 {
			t.Fatal("ep-1 session should remain intact after aborted ReleaseAll")
		}
		if _, ok2 := sessions["ep-2"]; !ok2 {
			t.Fatal("ep-2 session should remain intact after aborted ReleaseAll")
		}
	})

	// (b) Non-partial ReleaseAll returns an error when assignments > 0 remain after release.
	t.Run("b_non_partial_remaining_assignments_error", func(t *testing.T) {
		h := newTestHarness(t, false)
		h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
		accountCalls := 0
		h.provider.accountFn = func(ctx context.Context) (map[string]any, error) {
			accountCalls++
			if accountCalls == 1 {
				return map[string]any{
					"logged_in":   true,
					"assignments": 2,
					"rate":        3.5,
					"instances": []map[string]any{
						{"id": "ep-1", "managed": true},
						{"id": "ep-ext", "managed": false},
					},
				}, nil
			}
			return map[string]any{
				"logged_in":   true,
				"assignments": 1,
				"rate":        1.5,
				"instances": []map[string]any{
					{"id": "ep-straggler", "managed": false},
				},
			}, nil
		}

		planRes, err := h.ctrl.PreviewReleaseAll(context.Background())
		if err != nil {
			t.Fatalf("PreviewReleaseAll failed: %v", err)
		}
		planID := asString(planRes["id"])

		err = h.ctrl.ReleaseAll(context.Background(), planID, false, nil, false)
		wantErr := "已处理确认的实例；当前账号仍有 1 个实例，请再次停止全部"
		if err == nil || err.Error() != wantErr {
			t.Fatalf("got err=%v, want %q", err, wantErr)
		}
		if len(h.provider.releaseAllCalls) != 1 || len(h.provider.releaseAllCalls[0]) != 2 {
			t.Fatalf("expected provider.ReleaseAll called with both previewed endpoints, got %#v", h.provider.releaseAllCalls)
		}
	})
}

func TestSelectiveSecondaryEndpointReleasePreservesActiveState(t *testing.T) {
	h := newTestHarness(t, false)
	h.provider.addManagedSession("ep-1", "T4", "HIGH_RAM", true)
	h.provider.addManagedSession("ep-2", "A100", "STANDARD", false)

	ep1Outputs := []any{
		map[string]any{"id": strings.Repeat("a", 32), "filename": "ep1-masterpiece.png"},
	}
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state":        "ready",
			"id":           "ep-1",
			"protocol":     7,
			"loaded_model": "gemma4-31b",
		},
		"comfy_outputs": ep1Outputs,
	})
	h.ctrl.mu.Lock()
	h.ctrl.instanceStates["ep-2"] = &instanceCache{
		Runtime:      map[string]any{"state": "ready", "id": "ep-2", "protocol": 7},
		ComfyOutputs: []any{map[string]any{"id": strings.Repeat("b", 32), "filename": "ep2-temp.png"}},
	}
	h.ctrl.upgradeAttempted["ep-2"] = true
	h.ctrl.mu.Unlock()

	var prepareEPs []string
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if method == "POST" && path == "prepare-release" {
			prepareEPs = append(prepareEPs, opts.Endpoint)
		}
		return map[string]any{"ok": true}, nil
	}

	var midReleaseState, midReleaseID, midReleaseActive string
	h.provider.releaseFn = func(ctx context.Context, endpoint string, onLog LogFunc) (map[string]any, error) {
		midSnap := h.ctrl.Snapshot()
		midReleaseState = asString(asMap(midSnap["runtime"])["state"])
		midReleaseID = asString(asMap(midSnap["runtime"])["id"])
		midReleaseActive = asString(midSnap["active_endpoint"])
		h.provider.Clear(endpoint)
		return map[string]any{"ok": true}, nil
	}
	h.provider.accountFn = func(ctx context.Context) (map[string]any, error) {
		return map[string]any{
			"logged_in":   true,
			"assignments": 1,
			"rate":        1.5,
			"instances": []map[string]any{
				{"id": "ep-1", "managed": true, "accelerator": "T4", "variant": "GPU", "shape": "HIGH_RAM"},
			},
		}, nil
	}

	if err := h.ctrl.Release(context.Background(), false, "ep-2"); err != nil {
		t.Fatalf("selective secondary Release(ep-2) failed: %v", err)
	}

	if len(prepareEPs) != 1 || prepareEPs[0] != "ep-2" {
		t.Fatalf("expected prepare-release targeted to ep-2, got %#v", prepareEPs)
	}
	if len(h.provider.releaseCalls) != 1 || h.provider.releaseCalls[0] != "ep-2" {
		t.Fatalf("expected provider.Release called for ep-2, got %#v", h.provider.releaseCalls)
	}
	if midReleaseState != "ready" || midReleaseID != "ep-1" || midReleaseActive != "ep-1" {
		t.Fatalf("active ep-1 transitioned unexpectedly mid-release: state=%q id=%q active=%q", midReleaseState, midReleaseID, midReleaseActive)
	}
	for _, entry := range h.ctrl.activity.Snapshot() {
		if entry.Message == "正在释放远端 GPU" {
			t.Fatalf("active runtime should never have transitioned to releasing state, saw log %#v", entry)
		}
	}

	sessions := h.ctrl.vaultSessions()
	if _, hasEP2 := sessions["ep-2"]; hasEP2 {
		t.Fatal("expected ep-2 removed from vault sessions")
	}
	if _, hasEP1 := sessions["ep-1"]; !hasEP1 {
		t.Fatal("expected ep-1 preserved in vault sessions")
	}
	h.ctrl.mu.Lock()
	_, hasEP2State := h.ctrl.instanceStates["ep-2"]
	_, hasEP2Upgrade := h.ctrl.upgradeAttempted["ep-2"]
	h.ctrl.mu.Unlock()
	if hasEP2State || hasEP2Upgrade {
		t.Fatalf("expected ep-2 pruned from internal caches: state=%v upgrade=%v", hasEP2State, hasEP2Upgrade)
	}

	snap := h.ctrl.Snapshot()
	if snap["active_endpoint"] != "ep-1" {
		t.Fatalf("expected active_endpoint=ep-1, got %v", snap["active_endpoint"])
	}
	rt := asMap(snap["runtime"])
	if rt["state"] != "ready" || rt["id"] != "ep-1" || rt["loaded_model"] != "gemma4-31b" {
		t.Fatalf("expected ep-1 runtime preserved, got %#v", rt)
	}
	outputs := asMapSlice(snap["comfy_outputs"])
	if len(outputs) != 1 || outputs[0]["filename"] != "ep1-masterpiece.png" {
		t.Fatalf("expected ep-1 comfy_outputs preserved, got %#v", snap["comfy_outputs"])
	}
}

func TestSettingsSaveIdleMinutesPushesLiveWhenIdleAndSkipsWhenBusy(t *testing.T) {
	h := newTestHarness(t, false)
	h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
	h.provider.addManagedSession("ep-2", "L4", "STANDARD", false)
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{"state": "ready", "id": "ep-1", "protocol": 7, "idle_minutes": 30},
	})
	h.ctrl.mu.Lock()
	h.ctrl.instanceStates["ep-2"] = &instanceCache{
		Runtime: map[string]any{"state": "ready", "id": "ep-2", "protocol": 7, "idle_minutes": 30},
	}
	h.ctrl.mu.Unlock()

	var mu sync.Mutex
	configuredIdle := make(map[string]int)
	refreshedEPs := make(map[string]int)
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case method == "POST" && path == "configure":
			if mins, ok := asInt(asMap(body)["idle_minutes"]); ok {
				configuredIdle[opts.Endpoint] = mins
			}
			return map[string]any{"ok": true}, nil
		case method == "GET" && path == "snapshot":
			refreshedEPs[opts.Endpoint]++
			ep := opts.Endpoint
			if ep == "" {
				ep = "ep-1"
			}
			return map[string]any{
				"runtime": map[string]any{"state": "ready", "id": ep, "protocol": 7, "idle_minutes": configuredIdle[ep]},
				"models":  []any{},
			}, nil
		default:
			return map[string]any{"ok": true}, nil
		}
	}

	// 1) When idle, changing idle_minutes asynchronously pushes ConfigureRemote + RefreshRemote to owned instances.
	res, err := h.ctrl.Command(context.Background(), "settings.save", map[string]any{"idle_minutes": 45})
	if err != nil || asMap(res)["ok"] != true {
		t.Fatalf("settings.save (idle) failed: %v %#v", err, res)
	}
	waitUntil(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return configuredIdle["ep-1"] == 45 &&
			configuredIdle["ep-2"] == 45 &&
			refreshedEPs[""] >= 1 &&
			refreshedEPs["ep-2"] >= 1
	})
	var sawEP1Log, sawEP2Log bool
	for _, entry := range h.ctrl.activity.Snapshot() {
		if strings.Contains(entry.Message, "已向实例 ep-1 同步空闲释放时长（45 分钟）") {
			sawEP1Log = true
		}
		if strings.Contains(entry.Message, "已向实例 ep-2 同步空闲释放时长（45 分钟）") {
			sawEP2Log = true
		}
	}
	if !sawEP1Log || !sawEP2Log {
		t.Fatalf("expected live idle_minutes sync logs for ep-1 and ep-2: ep1=%v ep2=%v", sawEP1Log, sawEP2Log)
	}

	// 2) When opBusy is true, settings.save persists the new idle_minutes without spawning live ConfigureRemote push.
	h2 := newTestHarness(t, false)
	h2.provider.addManagedSession("ep-busy", "T4", "STANDARD", true)
	h2.ctrl.Update(map[string]any{
		"runtime": map[string]any{"state": "ready", "id": "ep-busy", "protocol": 7, "idle_minutes": 30},
	})
	var busyConfigureCalls, busyRefreshCalls atomic.Int32
	h2.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if method == "POST" && path == "configure" {
			busyConfigureCalls.Add(1)
		}
		if method == "GET" && path == "snapshot" {
			busyRefreshCalls.Add(1)
		}
		return map[string]any{"ok": true}, nil
	}

	h2.ctrl.opBusy.Store(true)
	res2, err := h2.ctrl.Command(context.Background(), "settings.save", map[string]any{"idle_minutes": 75})
	h2.ctrl.opBusy.Store(false)
	if err != nil || asMap(res2)["ok"] != true {
		t.Fatalf("settings.save (busy) failed: %v %#v", err, res2)
	}
	if gotIdle, _ := asInt(asMap(h2.ctrl.Snapshot()["settings"])["idle_minutes"]); gotIdle != 75 {
		t.Fatalf("expected idle_minutes=75 saved in snapshot, got %d", gotIdle)
	}
	rawSettings, err := os.ReadFile(filepath.Join(h2.configDir, "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var diskSettings map[string]any
	if err := decodeJSONWithNumbers(rawSettings, &diskSettings); err != nil {
		t.Fatalf("decode settings.json: %v", err)
	}
	if diskIdle, _ := asInt(diskSettings["idle_minutes"]); diskIdle != 75 {
		t.Fatalf("expected idle_minutes=75 persisted on disk, got %v", diskSettings["idle_minutes"])
	}
	// Close drains wg deterministically; zero configure/refresh RPCs should have been spawned.
	h2.ctrl.Close()
	if busyConfigureCalls.Load() != 0 || busyRefreshCalls.Load() != 0 {
		t.Fatalf("expected no live ConfigureRemote/RefreshRemote push while opBusy=true, got configure=%d refresh=%d",
			busyConfigureCalls.Load(), busyRefreshCalls.Load())
	}
}

func TestDriveForceRefreshContractAndHeadlessStartHappyPath(t *testing.T) {
	// (a) forceRefresh contract: seenForce == []bool{true, true, false}
	//     1. drive.setup_headless forces refresh (true)
	//     2. First ConnectDrive forces refresh while cloud_project is uncached (true)
	//     3. Second ConnectDrive uses cached cloud_project (false)
	t.Run("a_force_refresh_contract", func(t *testing.T) {
		h := newTestHarness(t, false)
		_ = h.vault.Set("colab", map[string]any{"token": "tok", "refresh_token": "rtok"})
		h.auth.driveScope = true
		h.auth.requiresQuota = true

		var mu sync.Mutex
		var seenForce []bool
		h.auth.ensureQuotaFn = func(ctx context.Context, forceRefresh bool, onLog LogFunc) (string, string, error) {
			mu.Lock()
			seenForce = append(seenForce, forceRefresh)
			callNum := len(seenForce)
			mu.Unlock()
			if callNum == 1 {
				return "", "gcp_tos_required", nil
			}
			_ = h.vault.Set("cloud_project", "colabone-proj-1")
			return "colabone-proj-1", "reused_existing_project", nil
		}
		h.auth.buildRcloneFn = func(ctx context.Context, quotaProject string) (string, error) {
			return "[colabone_gdrive]\ntype = drive\n", nil
		}
		var uploadedConf string
		h.provider.uploadDriveFn = func(ctx context.Context, rcloneConf, endpoint string) error {
			uploadedConf = rcloneConf
			return nil
		}
		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "drive/connect" {
				return map[string]any{"connected": true, "mode": "mount"}, nil
			}
			if path == "snapshot" {
				return map[string]any{
					"runtime": map[string]any{"state": "ready", "id": "ep-1", "protocol": 7, "drive": map[string]any{"connected": true, "mode": "mount"}},
					"models":  []any{},
				}, nil
			}
			return map[string]any{"ok": true}, nil
		}

		if _, err := h.ctrl.Command(context.Background(), "drive.setup_headless", nil); err != nil {
			t.Fatal(err)
		}
		waitIdle(t, h.ctrl)

		info1, err := h.ctrl.ConnectDrive(context.Background())
		if err != nil || info1["mode"] != "mount" {
			t.Fatalf("first ConnectDrive failed: info=%#v err=%v", info1, err)
		}
		info2, err := h.ctrl.ConnectDrive(context.Background())
		if err != nil || info2["mode"] != "mount" {
			t.Fatalf("second ConnectDrive failed: info=%#v err=%v", info2, err)
		}

		mu.Lock()
		gotForce := append([]bool(nil), seenForce...)
		mu.Unlock()
		if len(gotForce) != 3 || !gotForce[0] || !gotForce[1] || gotForce[2] {
			t.Fatalf("seenForce = %#v, want []bool{true, true, false}", gotForce)
		}
		if !strings.Contains(uploadedConf, "[colabone_gdrive]") {
			t.Fatalf("expected uploaded rclone conf containing [colabone_gdrive], got %q", uploadedConf)
		}
	})

	// (b) Start with auto_drive=true and valid headless credentials passes RcloneConf containing
	//     [colabone_gdrive] to provider.Start and executes POST drive/connect with empty auth_url.
	t.Run("b_headless_start_happy_path", func(t *testing.T) {
		h := newTestHarness(t, false)
		_ = h.vault.Set("colab", map[string]any{"token": "tok", "refresh_token": "rtok"})
		_ = h.vault.Set("cloud_project", "colabone-proj-happy")
		h.auth.driveScope = true
		h.auth.requiresQuota = true
		h.auth.ensureQuotaFn = func(ctx context.Context, forceRefresh bool, onLog LogFunc) (string, string, error) {
			if forceRefresh {
				t.Errorf("Start should resolve headless rclone with forceRefresh=false")
			}
			return "colabone-proj-happy", "reused_existing_project", nil
		}
		h.auth.buildRcloneFn = func(ctx context.Context, quotaProject string) (string, error) {
			return "[colabone_gdrive]\ntype = drive\nscope = drive\n", nil
		}

		var driveConnectCalls, mountDriveFSCalls int
		authURLAtDriveConnect := "unset"
		h.provider.mountDriveFSFn = func(ctx context.Context, opts ColabOneDriveMountOptions) (map[string]any, error) {
			mountDriveFSCalls++
			return map[string]any{"ok": true}, nil
		}
		h.provider.startFn = func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
			if notify != nil {
				notify("部署远端管理服务")
			}
			h.provider.addManagedSession("ep-headless", gpu, "STANDARD", true)
			return map[string]any{"endpoint": "ep-headless"}, nil
		}
		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			switch {
			case method == "POST" && path == "drive/connect":
				driveConnectCalls++
				authURLAtDriveConnect = asString(h.ctrl.Snapshot()["auth_url"])
				return map[string]any{"connected": true, "mode": "mount"}, nil
			case method == "GET" && path == "snapshot":
				return map[string]any{
					"runtime": map[string]any{
						"state":        "ready",
						"id":           "ep-headless",
						"protocol":     7,
						"idle_minutes": 30,
						"drive":        map[string]any{"connected": driveConnectCalls > 0, "mode": "mount"},
					},
					"models": []any{},
				}, nil
			default:
				return map[string]any{"ok": true}, nil
			}
		}

		if err := h.ctrl.Start(context.Background(), false, "T4"); err != nil {
			t.Fatalf("Start with headless Drive failed: %v", err)
		}
		if len(h.provider.startOpts) != 1 || !strings.Contains(h.provider.startOpts[0].RcloneConf, "[colabone_gdrive]") {
			t.Fatalf("expected RcloneConf containing [colabone_gdrive] passed to provider.Start, got %#v", h.provider.startOpts)
		}
		if driveConnectCalls != 1 {
			t.Fatalf("expected POST drive/connect called once, got %d", driveConnectCalls)
		}
		if mountDriveFSCalls != 0 {
			t.Fatalf("headless Start must not invoke interactive MountColabDriveFS, got %d calls", mountDriveFSCalls)
		}
		if authURLAtDriveConnect != "" || asString(h.ctrl.Snapshot()["auth_url"]) != "" {
			t.Fatalf("expected empty auth_url during and after headless drive/connect, got during=%q after=%q",
				authURLAtDriveConnect, h.ctrl.Snapshot()["auth_url"])
		}
		setup := asMap(h.ctrl.Snapshot()["setup"])
		if setup["drive"] != true || setup["drive_headless"] != true {
			t.Fatalf("expected setup.drive=true and setup.drive_headless=true, got %#v", setup)
		}
	})
}

func TestBootTelemetryPersistsThroughDriveMountAndEnrichesInstanceResources(t *testing.T) {
	const (
		ramUsedBytes   int64 = 24 * 1024 * 1024 * 1024
		ramTotalBytes  int64 = 127 * 1024 * 1024 * 1024
		diskUsedBytes  int64 = 70 * 1024 * 1024 * 1024
		diskTotalBytes int64 = 250 * 1024 * 1024 * 1024
		diskFreeBytes  int64 = 180 * 1024 * 1024 * 1024
	)

	h := newTestHarness(t, false)
	_ = h.vault.Set("colab", map[string]any{"token": "tok", "refresh_token": "rtok"})
	_ = h.vault.Set("cloud_project", "colabone-proj-tele")
	h.auth.driveScope = true
	h.auth.requiresQuota = true
	h.auth.ensureQuotaFn = func(ctx context.Context, forceRefresh bool, onLog LogFunc) (string, string, error) {
		return "colabone-proj-tele", "reused_existing_project", nil
	}
	h.auth.buildRcloneFn = func(ctx context.Context, quotaProject string) (string, error) {
		return "[colabone_gdrive]\ntype = drive\n", nil
	}

	liveResources := map[string]any{
		"memory": map[string]any{
			"used_bytes":  ramUsedBytes,
			"total_bytes": ramTotalBytes,
		},
		"disk": map[string]any{
			"used_bytes":  diskUsedBytes,
			"total_bytes": diskTotalBytes,
			"free_bytes":  diskFreeBytes,
		},
	}

	var savedNotify func(string)
	var seenBootStarting bool
	h.provider.startFn = func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
		savedNotify = notify
		notify("部署远端管理服务")
		opts.OnTelemetry(
			"ep-boot",
			map[string]any{"country": "US", "country_name": "美国", "region": "俄勒冈"},
			map[string]any{
				"gpu":    map[string]any{"name": "NVIDIA RTX PRO 6000", "used_mib": 256, "total_mib": 98304},
				"memory": map[string]any{"used_bytes": ramUsedBytes, "total_bytes": ramTotalBytes},
				"disk":   map[string]any{"used_bytes": diskUsedBytes, "total_bytes": diskTotalBytes, "free_bytes": diskFreeBytes},
			},
		)
		bootRT := asMap(h.ctrl.Snapshot()["runtime"])
		if bootRT["state"] == "starting" && asMap(bootRT["resources"]) != nil {
			seenBootStarting = true
		}
		h.provider.addManagedSession("ep-boot", gpu, "HIGH_RAM", true)
		return map[string]any{"endpoint": "ep-boot"}, nil
	}

	driveConnected := false
	var stateDuringDrive string
	var ramTotalDuringDrive, diskFreeDuringDrive int64

	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		switch {
		case method == "POST" && path == "drive/connect":
			if savedNotify != nil {
				savedNotify("正在挂载云盘")
			}
			midSnap := h.ctrl.Snapshot()
			midRT := asMap(midSnap["runtime"])
			stateDuringDrive = asString(midRT["state"])
			midRes := asMap(midRT["resources"])
			ramTotalDuringDrive, _ = asInt64(asMap(midRes["memory"])["total_bytes"])
			diskFreeDuringDrive, _ = asInt64(asMap(midRes["disk"])["free_bytes"])
			driveConnected = true
			return map[string]any{"connected": true, "mode": "mount"}, nil
		case method == "GET" && path == "snapshot":
			return map[string]any{
				"runtime": map[string]any{
					"state":        "ready",
					"id":           "ep-boot",
					"protocol":     7,
					"idle_minutes": 30,
					"gpu":          map[string]any{"name": "NVIDIA RTX PRO 6000", "used_mib": 512, "total_mib": 98304},
					"resources":    deepCopyMap(liveResources),
					"disk_free":    diskFreeBytes,
					"drive":        map[string]any{"connected": driveConnected, "mode": "mount"},
				},
				"models": []any{},
			}, nil
		default:
			return map[string]any{"ok": true}, nil
		}
	}
	h.provider.accountFn = func(ctx context.Context) (map[string]any, error) {
		return map[string]any{
			"logged_in":   true,
			"balance":     120.0,
			"rate":        2.5,
			"assignments": 1,
			"instances": []map[string]any{
				{"id": "ep-boot", "managed": true, "accelerator": "G4", "variant": "GPU", "shape": "HIGH_RAM"},
			},
		}, nil
	}

	if err := h.ctrl.Start(context.Background(), false, "G4"); err != nil {
		t.Fatalf("Start with auto_drive=true failed: %v", err)
	}
	if !seenBootStarting {
		t.Fatal("expected boot telemetry sample captured while runtime.state == starting")
	}
	if stateDuringDrive != "ready" {
		t.Fatalf("expected runtime.state == ready through POST drive/connect, got %q", stateDuringDrive)
	}
	if ramTotalDuringDrive != ramTotalBytes || diskFreeDuringDrive != diskFreeBytes {
		t.Fatalf("expected live runtime.resources to persist through POST drive/connect, got ram=%d diskFree=%d",
			ramTotalDuringDrive, diskFreeDuringDrive)
	}

	finalSnap := h.ctrl.Snapshot()
	if asMap(finalSnap["runtime"])["state"] != "ready" {
		t.Fatalf("expected final runtime.state == ready, got %#v", finalSnap["runtime"])
	}
	insts := asMapSlice(asMap(finalSnap["account"])["instances"])
	if len(insts) != 1 || insts[0]["id"] != "ep-boot" {
		t.Fatalf("expected 1 enriched instance ep-boot, got %#v", insts)
	}
	inst := insts[0]
	gotRAMTotal, _ := asInt64(inst["ram_total_bytes"])
	gotRAMUsed, _ := asInt64(inst["ram_used_bytes"])
	gotDiskFree, _ := asInt64(inst["disk_free_bytes"])
	gotDiskUsed, _ := asInt64(inst["disk_used_bytes"])
	gotDiskTotal, _ := asInt64(inst["disk_total_bytes"])
	if gotRAMTotal != ramTotalBytes ||
		gotRAMUsed != ramUsedBytes ||
		gotDiskFree != diskFreeBytes ||
		gotDiskUsed != diskUsedBytes ||
		gotDiskTotal != diskTotalBytes {
		t.Fatalf("enrichInstancesWith failed to project nested resource fields: %#v", inst)
	}
	if inst["drive_connected"] != true {
		t.Fatalf("expected enriched instance drive_connected=true, got %#v", inst)
	}
}

func TestMonitorOnceClearsStaleVaultRemoteWhenSessionsEmpty(t *testing.T) {
	cases := []struct {
		name      string
		seedVault func(v *ColabOneMemoryVault)
		checkKey  string
	}{
		{
			name: "legacy_remote",
			seedVault: func(v *ColabOneMemoryVault) {
				_ = v.Set("remote", map[string]any{"endpoint": "ep-stale-legacy", "gpu": "T4"})
			},
			checkKey: "remote",
		},
		{
			name: "remotes_map",
			seedVault: func(v *ColabOneMemoryVault) {
				_ = v.Set("remotes", map[string]any{
					"ep-stale-map": map[string]any{"endpoint": "ep-stale-map", "gpu": "A100"},
				})
			},
			checkKey: "remotes",
		},
		{
			name: "remote_ids",
			seedVault: func(v *ColabOneMemoryVault) {
				_ = v.Set("remote_ids", []any{"ep-stale-id"})
				_ = v.Set("remote:ep-stale-id", map[string]any{"endpoint": "ep-stale-id", "gpu": "L4"})
			},
			checkKey: "remote_ids",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHarness(t, false)
			tc.seedVault(h.vault)

			h.ctrl.MonitorOnce(context.Background())

			if len(h.provider.clearCalls) != 1 || h.provider.clearCalls[0] != "" {
				t.Fatalf("expected provider.Clear(\"\") called once, got %#v", h.provider.clearCalls)
			}
			if _, exists := h.vault.Get(tc.checkKey); exists {
				t.Fatalf("expected stale vault key %q cleared", tc.checkKey)
			}
			if remotes := h.ctrl.vaultRemotes(); len(remotes) != 0 {
				t.Fatalf("expected vaultRemotes empty after Clear, got %#v", remotes)
			}
		})
	}

	t.Run("clean_vault_skips_clear", func(t *testing.T) {
		h := newTestHarness(t, false)
		h.ctrl.MonitorOnce(context.Background())
		if len(h.provider.clearCalls) != 0 {
			t.Fatalf("expected no Clear calls on clean vault, got %#v", h.provider.clearCalls)
		}
	})
}

func TestLocalePersistsAndRestoresAuthLocaleOnRestart(t *testing.T) {
	h := newTestHarness(t, false)

	res, err := h.ctrl.Command(context.Background(), "locale.set", map[string]any{
		"locale":  "zh-HK",
		"persist": true,
	})
	if err != nil || asMap(res)["persisted"] != true || h.auth.Locale() != "zh-HK" {
		t.Fatalf("persisted locale.set failed: err=%v res=%#v locale=%q", err, res, h.auth.Locale())
	}

	// Non-persisted switch changes in-memory auth locale without overwriting settings.json.
	res2, err := h.ctrl.Command(context.Background(), "locale.set", map[string]any{
		"locale":  "ja-JP",
		"persist": false,
	})
	if err != nil || asMap(res2)["persisted"] != false || h.auth.Locale() != "ja-JP" {
		t.Fatalf("non-persisted locale.set failed: err=%v res=%#v locale=%q", err, res2, h.auth.Locale())
	}

	// Instantiating a fresh controller on the same configDir/vault must restore auth.Locale() == "zh-HK".
	freshAuth := newMockAuth()
	if freshAuth.Locale() != "zh-CN" {
		t.Fatalf("expected fresh mockAuth default locale zh-CN, got %q", freshAuth.Locale())
	}
	reloaded := NewColabOneController(ColabOneControllerConfig{
		Root:           h.configDir,
		ConfigDir:      h.configDir,
		Background:     false,
		Vault:          h.vault,
		Auth:           freshAuth,
		Provider:       newMockProvider(h.vault),
		ServiceBundler: h.bundler,
	})
	defer reloaded.Close()

	if freshAuth.Locale() != "zh-HK" {
		t.Fatalf("expected restarted controller to restore auth.Locale()==zh-HK, got %q", freshAuth.Locale())
	}
	if loc := asString(asMap(reloaded.Snapshot()["settings"])["locale"]); loc != "zh-HK" {
		t.Fatalf("expected restarted controller snapshot settings.locale==zh-HK, got %q", loc)
	}
}

// ---------------------------------------------------------------------------
// Regression tests for concurrency & error-handling fixes
// ---------------------------------------------------------------------------

type hookVault struct {
	inner    VaultStore
	mu       sync.Mutex
	getFn    func(key string) (any, bool, bool) // returns (val, ok, handled)
	setFn    func(key string, value any) (error, bool)
	deleteFn func(key string) (error, bool)
}

func newHookVault(inner VaultStore) *hookVault {
	return &hookVault{inner: inner}
}

func (v *hookVault) Get(key string) (any, bool) {
	v.mu.Lock()
	fn := v.getFn
	v.mu.Unlock()
	if fn != nil {
		if val, ok, handled := fn(key); handled {
			return val, ok
		}
	}
	return v.inner.Get(key)
}

func (v *hookVault) Set(key string, value any) error {
	v.mu.Lock()
	fn := v.setFn
	v.mu.Unlock()
	if fn != nil {
		if err, handled := fn(key, value); handled {
			return err
		}
	}
	return v.inner.Set(key, value)
}

func (v *hookVault) Delete(key string) error {
	v.mu.Lock()
	fn := v.deleteFn
	v.mu.Unlock()
	if fn != nil {
		if err, handled := fn(key); handled {
			return err
		}
	}
	return v.inner.Delete(key)
}

type mockOrphanCleanerProvider struct {
	*mockProvider
	orphanClearCalls atomic.Int32
}

func (p *mockOrphanCleanerProvider) ClearOrphanedRemotes() {
	p.orphanClearCalls.Add(1)
	if p.vault != nil {
		// Mirror production atomic behavior: only delete remote keys if sessions are still empty.
		if _, hasIDs := p.vault.Get("runtime_session_ids"); hasIDs {
			return
		}
		if _, hasLegacy := p.vault.Get("runtime_session"); hasLegacy {
			return
		}
		_ = p.vault.Delete("remote")
		_ = p.vault.Delete("remotes")
		_ = p.vault.Delete("remote_ids")
	}
}

func TestMonitorOnceConcurrentStartAndOrphanCleaner(t *testing.T) {
	t.Run("orphan_cleaner_interface_preferred", func(t *testing.T) {
		cfgDir := t.TempDir()
		vault := NewColabOneMemoryVault()
		_ = vault.Set("remote", map[string]any{"endpoint": "ep-orphan", "gpu": "T4"})
		prov := &mockOrphanCleanerProvider{mockProvider: newMockProvider(vault)}
		ctrl := NewColabOneController(ColabOneControllerConfig{
			Root:           cfgDir,
			ConfigDir:      cfgDir,
			Background:     false,
			Vault:          vault,
			Auth:           newMockAuth(),
			Provider:       prov,
			ServiceBundler: defaultMockBundler(),
		})
		defer ctrl.Close()

		ctrl.MonitorOnce(context.Background())

		if prov.orphanClearCalls.Load() != 1 {
			t.Fatalf("expected ClearOrphanedRemotes called once, got %d", prov.orphanClearCalls.Load())
		}
		prov.mu.Lock()
		clearN := len(prov.clearCalls)
		prov.mu.Unlock()
		if clearN != 0 {
			t.Fatalf("expected fallback Clear(\"\") not called when orphanRemoteCleaner is implemented, got %d", clearN)
		}
		if _, exists := vault.Get("remote"); exists {
			t.Fatal("expected orphaned remote key removed by ClearOrphanedRemotes")
		}
	})

	t.Run("opBusy_blocks_orphan_cleanup_and_ui_reset", func(t *testing.T) {
		cfgDir := t.TempDir()
		vault := NewColabOneMemoryVault()
		_ = vault.Set("remote", map[string]any{"endpoint": "ep-orphan", "gpu": "T4"})
		prov := &mockOrphanCleanerProvider{mockProvider: newMockProvider(vault)}
		ctrl := NewColabOneController(ColabOneControllerConfig{
			Root:           cfgDir,
			ConfigDir:      cfgDir,
			Background:     false,
			Vault:          vault,
			Auth:           newMockAuth(),
			Provider:       prov,
			ServiceBundler: defaultMockBundler(),
		})
		defer ctrl.Close()

		ctrl.mu.Lock()
		ctrl.lastAuth = nowUnixFloat()
		ctrl.instanceStates["ep-active"] = &instanceCache{
			Runtime: map[string]any{"state": "ready", "id": "ep-active"},
		}
		ctrl.upgradeAttempted["ep-active"] = true
		ctrl.data["runtime"] = map[string]any{"state": "ready", "id": "ep-active"}
		ctrl.mu.Unlock()

		ctrl.opBusy.Store(true)
		ctrl.MonitorOnce(context.Background())
		ctrl.opBusy.Store(false)

		if prov.orphanClearCalls.Load() != 0 {
			t.Fatalf("expected no ClearOrphanedRemotes while opBusy=true, got %d", prov.orphanClearCalls.Load())
		}
		if _, exists := vault.Get("remote"); !exists {
			t.Fatal("expected remote key untouched while opBusy=true")
		}
		ctrl.mu.Lock()
		_, hasState := ctrl.instanceStates["ep-active"]
		_, hasUpgrade := ctrl.upgradeAttempted["ep-active"]
		rtState := asString(asMap(ctrl.data["runtime"])["state"])
		ctrl.mu.Unlock()
		if !hasState || !hasUpgrade || rtState != "ready" {
			t.Fatalf("MonitorOnce clobbered state while opBusy=true: hasState=%v hasUpgrade=%v rtState=%q",
				hasState, hasUpgrade, rtState)
		}
	})

	t.Run("concurrent_start_during_monitor_does_not_wipe_new_session", func(t *testing.T) {
		cfgDir := t.TempDir()
		memVault := NewColabOneMemoryVault()
		hv := newHookVault(memVault)
		prov := newMockProvider(memVault)
		prov.startFn = func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
			prov.addManagedSession("ep-fresh", gpu, "STANDARD", true)
			return map[string]any{"endpoint": "ep-fresh"}, nil
		}
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "snapshot" {
				return map[string]any{
					"runtime": map[string]any{
						"state":        "ready",
						"id":           "ep-fresh",
						"protocol":     7,
						"idle_minutes": 30,
						"tasks":        []any{},
					},
					"models": []any{},
				}, nil
			}
			return map[string]any{"ok": true}, nil
		}

		ctrl := NewColabOneController(ColabOneControllerConfig{
			Root:           cfgDir,
			ConfigDir:      cfgDir,
			Background:     false,
			Vault:          hv,
			Auth:           newMockAuth(),
			Provider:       prov,
			ServiceBundler: defaultMockBundler(),
		})
		defer ctrl.Close()

		ctrl.mu.Lock()
		ctrl.lastAuth = nowUnixFloat()
		ctrl.mu.Unlock()

		// Simulate Start running and completing right after MonitorOnce's UI-reset
		// vaultSessions() read returns empty (ABA race on opBusy).
		var sessionReads atomic.Int32
		hv.mu.Lock()
		hv.getFn = func(key string) (any, bool, bool) {
			if key == "runtime_session_ids" && sessionReads.Add(1) == 2 {
				// Disable hook before running Start so Start reads real vault.
				hv.mu.Lock()
				hv.getFn = nil
				hv.mu.Unlock()
				if _, err := ctrl.Command(context.Background(), "runtime.start", map[string]any{"gpu": "T4"}); err != nil {
					t.Errorf("concurrent runtime.start failed: %v", err)
				}
				waitIdle(t, ctrl)
				// Return stale empty sessions to MonitorOnce's in-flight vaultSessions() call.
				return nil, false, true
			}
			return nil, false, false
		}
		hv.mu.Unlock()

		ctrl.MonitorOnce(context.Background())

		rt := asMap(ctrl.Snapshot()["runtime"])
		if asString(rt["state"]) != "ready" || asString(rt["id"]) != "ep-fresh" {
			t.Fatalf("MonitorOnce wiped newly started runtime: got %#v", rt)
		}
		if _, exists := ctrl.vaultSessions()["ep-fresh"]; !exists {
			t.Fatal("MonitorOnce wiped newly started vault session ep-fresh")
		}
		ctrl.mu.Lock()
		_, hasCache := ctrl.instanceStates["ep-fresh"]
		ctrl.mu.Unlock()
		if !hasCache {
			t.Fatal("MonitorOnce pruned newly started instanceStates[ep-fresh]")
		}
	})
}

func TestBackgroundRefreshAndMonitorHeartbeatRaces(t *testing.T) {
	t.Run("background_refresh_after_release_does_not_resurrect_ready", func(t *testing.T) {
		h := newTestHarness(t, false)
		h.provider.addManagedSession("ep-rel", "T4", "STANDARD", true)
		h.ctrl.Update(map[string]any{
			"runtime": map[string]any{"state": "ready", "id": "ep-rel", "protocol": 7},
		})
		h.ctrl.mu.Lock()
		h.ctrl.lastAuth = nowUnixFloat()
		h.ctrl.lastHeartbeat = nowUnixFloat()
		h.ctrl.lastConfig = nowUnixFloat()
		h.ctrl.mu.Unlock()

		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "snapshot" {
				// Release clears the last session while MonitorOnce's snapshot RPC is in flight.
				if err := h.ctrl.Release(ctx, true, ""); err != nil {
					t.Errorf("Release during snapshot failed: %v", err)
				}
				return map[string]any{
					"runtime": map[string]any{"state": "ready", "id": "ep-rel", "protocol": 7, "tasks": []any{}},
					"models":  []any{},
				}, nil
			}
			return map[string]any{"ok": true}, nil
		}

		h.ctrl.MonitorOnce(context.Background())

		rt := asMap(h.ctrl.Snapshot()["runtime"])
		if asString(rt["state"]) != "stopped" {
			t.Fatalf("stale background refresh resurrected runtime state=%q after Release", rt["state"])
		}
		h.ctrl.mu.Lock()
		_, resurrectedCache := h.ctrl.instanceStates["ep-rel"]
		h.ctrl.mu.Unlock()
		if resurrectedCache {
			t.Fatal("stale background refresh recreated instanceStates[ep-rel] after Release")
		}
	})

	t.Run("refresh_recomputes_ownership_from_curSessions", func(t *testing.T) {
		cfgDir := t.TempDir()
		memVault := NewColabOneMemoryVault()
		hv := newHookVault(memVault)
		prov := newMockProvider(memVault)
		prov.addManagedSession("ep-mid", "T4", "STANDARD", true)
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "snapshot" {
				return map[string]any{
					"runtime": map[string]any{"state": "ready", "id": "ep-mid", "protocol": 7, "tasks": []any{}},
					"models":  []any{},
				}, nil
			}
			return map[string]any{"ok": true}, nil
		}
		ctrl := NewColabOneController(ColabOneControllerConfig{
			Root:           cfgDir,
			ConfigDir:      cfgDir,
			Background:     false,
			Vault:          hv,
			Auth:           newMockAuth(),
			Provider:       prov,
			ServiceBundler: defaultMockBundler(),
		})
		defer ctrl.Close()

		// RefreshRemote reads vaultSessions() 3 times: hadSessions (1), sessions (2), curSessions (3).
		// Clear the last session right before the 3rd read (curSessions) to simulate Release landing mid-function.
		var reads atomic.Int32
		hv.mu.Lock()
		hv.getFn = func(key string) (any, bool, bool) {
			if key == "runtime_session_ids" && reads.Add(1) == 3 {
				hv.mu.Lock()
				hv.getFn = nil
				hv.mu.Unlock()
				_ = ctrl.Release(context.Background(), true, "")
				return nil, false, true
			}
			return nil, false, false
		}
		hv.mu.Unlock()

		if _, err := ctrl.refreshRemote(context.Background(), "", true); err != nil {
			t.Fatalf("refreshRemote failed: %v", err)
		}
		if st := asString(asMap(ctrl.Snapshot()["runtime"])["state"]); st != "stopped" {
			t.Fatalf("refreshRemote resurrected runtime state=%q after curSessions became empty", st)
		}
	})

	t.Run("background_refresh_during_start_createNew_preserves_starting_and_op", func(t *testing.T) {
		h := newTestHarness(t, false)
		h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
		h.ctrl.Update(map[string]any{
			"runtime": map[string]any{"state": "ready", "id": "ep-1", "protocol": 7},
		})
		h.ctrl.mu.Lock()
		h.ctrl.lastAuth = nowUnixFloat()
		h.ctrl.lastHeartbeat = nowUnixFloat()
		h.ctrl.lastConfig = nowUnixFloat()
		h.ctrl.mu.Unlock()

		bgEntered := make(chan struct{})
		bgRelease := make(chan struct{})
		startEntered := make(chan struct{})
		startProceed := make(chan struct{})
		var closeBGOnce, closeStartOnce sync.Once
		releaseBG := func() { closeBGOnce.Do(func() { close(bgRelease) }) }
		releaseStart := func() { closeStartOnce.Do(func() { close(startProceed) }) }
		defer releaseBG()
		defer releaseStart()
		var savedNotify func(string)
		var bgRefreshOnce atomic.Bool

		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "snapshot" && bgRefreshOnce.CompareAndSwap(false, true) {
				close(bgEntered)
				select {
				case <-bgRelease:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return map[string]any{
					"runtime": map[string]any{
						"state":        "ready",
						"id":           "ep-1",
						"protocol":     7,
						"idle_minutes": 45,
						"tasks":        []any{},
					},
					"models": []any{},
				}, nil
			}
			if path == "snapshot" {
				return map[string]any{
					"runtime": map[string]any{"state": "ready", "id": "ep-2", "protocol": 7, "idle_minutes": 30, "tasks": []any{}},
					"models":  []any{},
				}, nil
			}
			return map[string]any{"ok": true}, nil
		}
		h.provider.startFn = func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
			savedNotify = notify
			notify("正在申请 GPU")
			close(startEntered)
			select {
			case <-startProceed:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			h.provider.addManagedSession("ep-2", gpu, "STANDARD", true)
			return map[string]any{"endpoint": "ep-2"}, nil
		}

		monitorDone := make(chan struct{})
		go func() {
			defer close(monitorDone)
			h.ctrl.MonitorOnce(context.Background())
		}()
		<-bgEntered

		if _, err := h.ctrl.Command(context.Background(), "runtime.start", map[string]any{
			"new": true,
			"gpu": "A100",
		}); err != nil {
			t.Fatalf("runtime.start(new=true) failed: %v", err)
		}
		<-startEntered

		// Unblock background MonitorOnce snapshot for ep-1 while Start(createNew=true) is in "starting".
		releaseBG()
		<-monitorDone

		snap := h.ctrl.Snapshot()
		rt := asMap(snap["runtime"])
		op := asMap(snap["operation"])
		if asString(rt["state"]) != "starting" {
			t.Fatalf("background refresh clobbered starting runtime to %#v", rt)
		}
		if asString(op["kind"]) != "runtime_start" || !asBool(op["cancellable"]) {
			t.Fatalf("background refresh clobbered runtime_start operation: %#v", op)
		}
		// Calling Start's notify callback must also keep runtime_start metadata intact.
		if savedNotify != nil {
			savedNotify("正在启动远端管理服务")
		}
		opAfterNotify := asMap(h.ctrl.Snapshot()["operation"])
		if asString(opAfterNotify["kind"]) != "runtime_start" || !asBool(opAfterNotify["cancellable"]) {
			t.Fatalf("notify after background refresh lost runtime_start metadata: %#v", opAfterNotify)
		}
		// Stale pre-operation background snapshot (idle_minutes=45) must be discarded cleanly
		// without mutating ep-1's instanceStates or c.data.
		h.ctrl.mu.Lock()
		ep1Cache := h.ctrl.instanceStates["ep-1"]
		var cachedIdle int
		if ep1Cache != nil {
			cachedIdle, _ = asInt(ep1Cache.Runtime["idle_minutes"])
		}
		h.ctrl.mu.Unlock()
		if cachedIdle != 0 {
			t.Fatalf("expected stale pre-operation background snapshot discarded without poisoning ep-1 instanceStates, got idle_minutes=%d", cachedIdle)
		}

		releaseStart()
		waitIdle(t, h.ctrl)
	})

	t.Run("select_instance_background_refresh_during_start_createNew_preserves_starting", func(t *testing.T) {
		cfgDir := t.TempDir()
		memVault := NewColabOneMemoryVault()
		hv := newHookVault(memVault)
		prov := newMockProvider(memVault)
		prov.addManagedSession("ep-1", "T4", "STANDARD", true)
		prov.addManagedSession("ep-2", "L4", "STANDARD", false)
		ctrl := NewColabOneController(ColabOneControllerConfig{
			Root:           cfgDir,
			ConfigDir:      cfgDir,
			Background:     false,
			Vault:          hv,
			Auth:           newMockAuth(),
			Provider:       prov,
			ServiceBundler: defaultMockBundler(),
		})
		defer ctrl.Close()

		ctrl.mu.Lock()
		ctrl.instanceStates["ep-2"] = &instanceCache{
			Runtime: map[string]any{"state": "ready", "id": "ep-2", "protocol": 7},
		}
		ctrl.mu.Unlock()

		bgEntered := make(chan struct{})
		bgRelease := make(chan struct{})
		bgDone := make(chan struct{})
		bgReachedLockCheck := make(chan struct{})
		startEntered := make(chan struct{})
		startProceed := make(chan struct{})
		var closeBGOnce, closeStartOnce, closeLockCheckOnce sync.Once
		releaseBG := func() { closeBGOnce.Do(func() { close(bgRelease) }) }
		releaseStart := func() { closeStartOnce.Do(func() { close(startProceed) }) }
		defer releaseBG()
		defer releaseStart()
		var ep2SnapOnce atomic.Bool

		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "snapshot" && opts.Endpoint == "ep-2" && ep2SnapOnce.CompareAndSwap(false, true) {
				close(bgEntered)
				select {
				case <-bgRelease:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				defer close(bgDone)
				return map[string]any{
					"runtime": map[string]any{"state": "ready", "id": "ep-2", "protocol": 7, "idle_minutes": 77, "tasks": []any{}},
					"models":  []any{},
				}, nil
			}
			if path == "snapshot" {
				return map[string]any{
					"runtime": map[string]any{"state": "ready", "id": "ep-3", "protocol": 7, "idle_minutes": 30, "tasks": []any{}},
					"models":  []any{},
				}, nil
			}
			return map[string]any{"ok": true}, nil
		}
		prov.startFn = func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
			notify("正在申请 GPU")
			close(startEntered)
			select {
			case <-startProceed:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			prov.addManagedSession("ep-3", gpu, "STANDARD", true)
			return map[string]any{"endpoint": "ep-3"}, nil
		}

		if _, err := ctrl.SelectInstance(context.Background(), "ep-2"); err != nil {
			t.Fatalf("SelectInstance(ep-2) failed: %v", err)
		}
		<-bgEntered

		if _, err := ctrl.Command(context.Background(), "runtime.start", map[string]any{
			"new": true,
			"gpu": "A100",
		}); err != nil {
			t.Fatalf("runtime.start(new=true) failed: %v", err)
		}
		<-startEntered

		// Watch for SelectInstance's background refreshRemote reading active_endpoint right
		// before its c.mu.Lock() staleness check (while Start is parked in startFn).
		hv.mu.Lock()
		hv.getFn = func(key string) (any, bool, bool) {
			if key == "active_endpoint" {
				closeLockCheckOnce.Do(func() { close(bgReachedLockCheck) })
			}
			return nil, false, false
		}
		hv.mu.Unlock()

		// Unblock SelectInstance's background refresh goroutine while Start(createNew=true) is running.
		releaseBG()
		<-bgDone
		<-bgReachedLockCheck
		hv.mu.Lock()
		hv.getFn = nil
		hv.mu.Unlock()

		snap := ctrl.Snapshot()
		if st := asString(asMap(snap["runtime"])["state"]); st != "starting" {
			t.Fatalf("SelectInstance background refresh clobbered starting state to %q", st)
		}
		op := asMap(snap["operation"])
		if asString(op["kind"]) != "runtime_start" || !asBool(op["cancellable"]) {
			t.Fatalf("SelectInstance background refresh clobbered operation: %#v", op)
		}

		releaseStart()
		waitIdle(t, ctrl)
		ctrl.wg.Wait()

		// After both Start and the background refresh goroutine have completely exited,
		// ep-2's instanceStates must NOT have been poisoned by the stale pre-Start snapshot.
		ctrl.mu.Lock()
		ep2Cache := ctrl.instanceStates["ep-2"]
		var ep2Idle int
		if ep2Cache != nil {
			ep2Idle, _ = asInt(ep2Cache.Runtime["idle_minutes"])
		}
		ctrl.mu.Unlock()
		if ep2Idle != 0 {
			t.Fatalf("expected stale pre-Start SelectInstance background snapshot discarded, got ep-2 idle_minutes=%d", ep2Idle)
		}
		if got := ctrl.activeEndpoint(); got != "ep-3" {
			t.Fatalf("expected active endpoint ep-3 after Start(new=true), got %q", got)
		}
	})

	t.Run("select_instance_during_heartbeat_preserves_healthy_ep1_cache", func(t *testing.T) {
		h := newTestHarness(t, false)
		h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
		h.provider.addManagedSession("ep-2", "A100", "STANDARD", false)
		h.ctrl.mu.Lock()
		h.ctrl.lastAuth = nowUnixFloat()
		h.ctrl.lastHeartbeat = 0 // force Heartbeat
		h.ctrl.instanceStates["ep-1"] = &instanceCache{
			Runtime: map[string]any{"state": "ready", "id": "ep-1", "protocol": 7, "tag": "healthy-ep1"},
		}
		h.ctrl.upgradeAttempted["ep-1"] = true
		h.ctrl.instanceStates["ep-2"] = &instanceCache{
			Runtime: map[string]any{"state": "ready", "id": "ep-2", "protocol": 7, "tag": "healthy-ep2"},
		}
		h.ctrl.data["runtime"] = map[string]any{"state": "ready", "id": "ep-1", "protocol": 7, "tag": "healthy-ep1"}
		h.ctrl.mu.Unlock()

		var rpcAfterSwitch atomic.Int32
		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if opts.Endpoint == "" {
				rpcAfterSwitch.Add(1)
			}
			return map[string]any{
				"runtime": map[string]any{"state": "ready", "id": opts.Endpoint, "protocol": 7, "tasks": []any{}},
				"models":  []any{},
			}, nil
		}
		h.provider.heartbeatFn = func(ctx context.Context, endpoint string) error {
			if endpoint == "" {
				if _, err := h.ctrl.SelectInstance(ctx, "ep-2"); err != nil {
					t.Errorf("SelectInstance(ep-2) during Heartbeat failed: %v", err)
				}
			}
			return nil
		}

		h.ctrl.MonitorOnce(context.Background())

		if got := h.ctrl.activeEndpoint(); got != "ep-2" {
			t.Fatalf("expected active endpoint ep-2, got %q", got)
		}
		h.ctrl.mu.Lock()
		ep1Cache := h.ctrl.instanceStates["ep-1"]
		ep1Upgrade := h.ctrl.upgradeAttempted["ep-1"]
		h.ctrl.mu.Unlock()
		if ep1Cache == nil || asString(ep1Cache.Runtime["tag"]) != "healthy-ep1" {
			t.Fatalf("concurrent SelectInstance during Heartbeat deleted healthy ep-1 cache: %#v", ep1Cache)
		}
		if !ep1Upgrade {
			t.Fatal("concurrent SelectInstance during Heartbeat deleted ep-1 upgradeAttempted flag")
		}
		if rpcAfterSwitch.Load() != 0 {
			t.Fatalf("expected MonitorOnce to abort active tick after switch, got %d default-endpoint RPCs", rpcAfterSwitch.Load())
		}
	})

	t.Run("heartbeat_evicting_dead_ep1_still_cleans_cache_and_activates_ep2", func(t *testing.T) {
		h := newTestHarness(t, false)
		h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
		h.provider.addManagedSession("ep-2", "A100", "STANDARD", false)
		h.ctrl.mu.Lock()
		h.ctrl.lastAuth = nowUnixFloat()
		h.ctrl.lastHeartbeat = 0
		h.ctrl.instanceStates["ep-1"] = &instanceCache{
			Runtime: map[string]any{"state": "ready", "id": "ep-1", "protocol": 7},
		}
		h.ctrl.upgradeAttempted["ep-1"] = true
		h.ctrl.instanceStates["ep-2"] = &instanceCache{
			Runtime: map[string]any{"state": "ready", "id": "ep-2", "protocol": 7, "tag": "from-ep2"},
		}
		h.ctrl.data["runtime"] = map[string]any{"state": "ready", "id": "ep-1", "protocol": 7}
		h.ctrl.mu.Unlock()

		h.provider.heartbeatFn = func(ctx context.Context, endpoint string) error {
			if endpoint == "" {
				// Simulate Heartbeat clearing dead ep-1 from vault.
				h.provider.Clear("ep-1")
			}
			return nil
		}

		h.ctrl.MonitorOnce(context.Background())

		h.ctrl.mu.Lock()
		_, hasEP1 := h.ctrl.instanceStates["ep-1"]
		_, hasEP1Upg := h.ctrl.upgradeAttempted["ep-1"]
		rt := deepCopyMap(asMap(h.ctrl.data["runtime"]))
		h.ctrl.mu.Unlock()
		if hasEP1 || hasEP1Upg {
			t.Fatalf("expected evicted ep-1 pruned from caches: hasState=%v hasUpgrade=%v", hasEP1, hasEP1Upg)
		}
		if asString(rt["id"]) != "ep-2" || asString(rt["tag"]) != "from-ep2" {
			t.Fatalf("expected ep-2 activated after ep-1 eviction, got %#v", rt)
		}
	})
}

func TestRuntimeStartCancelDuringPostStartRefreshAndRetryDelay(t *testing.T) {
	t.Run("cancel_during_successful_post_start_refresh_rolls_back", func(t *testing.T) {
		h := newTestHarness(t, false)
		h.provider.startFn = func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
			if notify != nil {
				notify("部署远端管理服务")
			}
			h.provider.addManagedSession("ep-cancel-post", gpu, "STANDARD", true)
			return map[string]any{"endpoint": "ep-cancel-post"}, nil
		}

		var cancelRes map[string]any
		var configureCalls atomic.Int32
		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "snapshot" {
				res, err := h.ctrl.Command(context.Background(), "runtime.start.cancel", nil)
				if err != nil {
					t.Errorf("runtime.start.cancel failed: %v", err)
				}
				cancelRes = asMap(res)
				return map[string]any{
					"runtime": map[string]any{"state": "ready", "id": "ep-cancel-post", "protocol": 7, "tasks": []any{}},
					"models":  []any{},
				}, nil
			}
			if path == "configure" {
				configureCalls.Add(1)
			}
			return map[string]any{"ok": true}, nil
		}

		if _, err := h.ctrl.Command(context.Background(), "runtime.start", map[string]any{"gpu": "T4"}); err != nil {
			t.Fatalf("runtime.start failed: %v", err)
		}
		waitIdle(t, h.ctrl)

		if !asBool(cancelRes["cancelled"]) {
			t.Fatalf("expected runtime.start.cancel accepted during post-start RefreshRemote, got %#v", cancelRes)
		}
		h.provider.mu.Lock()
		releases := append([]string(nil), h.provider.releaseCalls...)
		h.provider.mu.Unlock()
		if len(releases) != 1 || releases[0] != "ep-cancel-post" {
			t.Fatalf("expected rollback Release(ep-cancel-post), got %#v", releases)
		}
		if configureCalls.Load() != 0 {
			t.Fatalf("expected ConfigureRemote skipped after cancel rollback, got %d calls", configureCalls.Load())
		}
		if len(h.ctrl.vaultSessions()) != 0 {
			t.Fatalf("expected vault sessions empty after cancel rollback, got %#v", h.ctrl.vaultSessions())
		}
		if st := asString(asMap(h.ctrl.Snapshot()["runtime"])["state"]); st != "stopped" {
			t.Fatalf("expected runtime state=stopped after cancel rollback, got %q", st)
		}
		for _, entry := range h.ctrl.activity.Snapshot() {
			if strings.Contains(entry.Message, "启动远端 GPU：完成") {
				t.Fatalf("unexpected completion log after cancelled start: %+v", entry)
			}
		}
	})

	t.Run("cancel_during_retry_delay_aborts_immediately_without_second_refresh", func(t *testing.T) {
		cfgDir := t.TempDir()
		vault := NewColabOneMemoryVault()
		prov := newMockProvider(vault)
		prov.startFn = func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
			prov.addManagedSession("ep-cancel-retry", gpu, "STANDARD", true)
			return map[string]any{"endpoint": "ep-cancel-retry"}, nil
		}
		firstFailed := make(chan struct{})
		var snapCalls atomic.Int32
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "snapshot" {
				if snapCalls.Add(1) == 1 {
					close(firstFailed)
					return nil, errors.New("transient snapshot error")
				}
				return map[string]any{
					"runtime": map[string]any{"state": "ready", "id": "ep-cancel-retry", "protocol": 7},
					"models":  []any{},
				}, nil
			}
			return map[string]any{"ok": true}, nil
		}

		ctrl := NewColabOneController(ColabOneControllerConfig{
			Root:            cfgDir,
			ConfigDir:       cfgDir,
			Background:      false,
			Vault:           vault,
			Auth:            newMockAuth(),
			Provider:        prov,
			ServiceBundler:  defaultMockBundler(),
			StartRetryDelay: 5 * time.Second,
		})
		defer ctrl.Close()

		if _, err := ctrl.Command(context.Background(), "runtime.start", map[string]any{"gpu": "T4"}); err != nil {
			t.Fatalf("runtime.start failed: %v", err)
		}
		<-firstFailed
		// Wait until Start logs the retry warning and enters the 5s timer select.
		waitUntil(t, 2*time.Second, func() bool {
			for _, entry := range ctrl.activity.Snapshot() {
				if strings.Contains(entry.Message, "首次获取远端快照未响应") {
					return true
				}
			}
			return false
		})
		startedWait := time.Now()
		res, err := ctrl.Command(context.Background(), "runtime.start.cancel", nil)
		if err != nil || !asBool(asMap(res)["cancelled"]) {
			t.Fatalf("expected runtime.start.cancel accepted, err=%v res=%#v", err, res)
		}
		waitIdle(t, ctrl)
		elapsed := time.Since(startedWait)
		if elapsed > 1500*time.Millisecond {
			t.Fatalf("cancel waited too long (%v); retry delay was not interrupted by startCancel", elapsed)
		}
		if snapCalls.Load() != 1 {
			t.Fatalf("expected no retry RefreshRemote after cancel, got %d snapshot calls", snapCalls.Load())
		}
		prov.mu.Lock()
		releases := append([]string(nil), prov.releaseCalls...)
		prov.mu.Unlock()
		if len(releases) != 1 || releases[0] != "ep-cancel-retry" {
			t.Fatalf("expected rollback Release(ep-cancel-retry), got %#v", releases)
		}
		if len(ctrl.vaultSessions()) != 0 {
			t.Fatalf("expected empty sessions after cancel rollback, got %#v", ctrl.vaultSessions())
		}
	})
}

func TestMonitorAutoUpgradeSkipsCleanlyOnConcurrentSwitchOrStateChange(t *testing.T) {
	t.Run("select_instance_switch_before_opBusy_skips_upgrade_without_poisoning", func(t *testing.T) {
		h := newTestHarness(t, false)
		h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
		h.provider.addManagedSession("ep-2", "A100", "STANDARD", false)
		h.ctrl.mu.Lock()
		h.ctrl.lastAuth = nowUnixFloat()
		h.ctrl.lastHeartbeat = nowUnixFloat()
		h.ctrl.lastConfig = 0 // trigger ConfigureRemote right before auto-upgrade check
		// Seed ep-2 cache without "id" so the outer auto-upgrade check passes and
		// onStart() must catch c.activeEndpoint() != upgradeEP after acquiring opBusy.
		h.ctrl.instanceStates["ep-2"] = &instanceCache{
			Runtime: map[string]any{
				"state":            "ready",
				"protocol":         7,
				"service_revision": "stale00000000000",
				"tasks":            []any{},
			},
		}
		h.ctrl.mu.Unlock()

		var switched atomic.Bool
		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "snapshot" {
				ep := opts.Endpoint
				if ep == "" {
					ep = h.ctrl.activeEndpoint()
				}
				return map[string]any{
					"runtime": map[string]any{
						"id":               ep,
						"state":            "ready",
						"protocol":         7,
						"service_revision": "stale00000000000",
						"tasks":            []any{},
					},
					"models": []any{},
				}, nil
			}
			if path == "configure" && switched.CompareAndSwap(false, true) {
				if _, err := h.ctrl.SelectInstance(ctx, "ep-2"); err != nil {
					t.Errorf("SelectInstance(ep-2) failed: %v", err)
				}
			}
			return map[string]any{"ok": true}, nil
		}

		h.ctrl.MonitorOnce(context.Background())
		waitIdle(t, h.ctrl)

		h.provider.mu.Lock()
		upgrades := append([]string(nil), h.provider.upgradeCalls...)
		h.provider.mu.Unlock()
		if len(upgrades) != 0 {
			t.Fatalf("expected no UpgradeService call after concurrent switch to ep-2, got %#v", upgrades)
		}
		h.ctrl.mu.Lock()
		att1 := h.ctrl.upgradeAttempted["ep-1"]
		att2 := h.ctrl.upgradeAttempted["ep-2"]
		h.ctrl.mu.Unlock()
		if att1 || att2 {
			t.Fatalf("expected upgradeAttempted clean for both endpoints, got ep-1=%v ep-2=%v", att1, att2)
		}
		if errMsg := asString(h.ctrl.Snapshot()["error"]); errMsg != "" {
			t.Fatalf("expected empty error after skipped auto-upgrade, got %q", errMsg)
		}
		for _, entry := range h.ctrl.activity.Snapshot() {
			if strings.Contains(entry.Message, "更新远端管理服务") {
				t.Fatalf("unexpected upgrade log on skipped precheck: %+v", entry)
			}
		}
	})

	t.Run("runtime_becomes_busy_inside_onStart_skips_cleanly_then_upgrades_when_idle", func(t *testing.T) {
		cfgDir := t.TempDir()
		memVault := NewColabOneMemoryVault()
		hv := newHookVault(memVault)
		prov := newMockProvider(memVault)
		prov.addManagedSession("ep-1", "T4", "STANDARD", true)
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "snapshot" {
				return map[string]any{
					"runtime": map[string]any{
						"id":               "ep-1",
						"state":            "ready",
						"protocol":         7,
						"service_revision": "stale00000000000",
						"tasks":            []any{},
					},
					"models": []any{},
				}, nil
			}
			return map[string]any{"ok": true}, nil
		}

		ctrl := NewColabOneController(ColabOneControllerConfig{
			Root:           cfgDir,
			ConfigDir:      cfgDir,
			Background:     false,
			Vault:          hv,
			Auth:           newMockAuth(),
			Provider:       prov,
			ServiceBundler: defaultMockBundler(),
		})
		defer ctrl.Close()

		ctrl.mu.Lock()
		ctrl.lastAuth = nowUnixFloat()
		ctrl.lastHeartbeat = nowUnixFloat()
		ctrl.lastConfig = nowUnixFloat()
		ctrl.mu.Unlock()

		// Intercept active_endpoint lookup inside onStart (when opBusy is already held)
		// to simulate ComfyUI starting right before the lock-protected precheck.
		var injectedBusy atomic.Bool
		hv.mu.Lock()
		hv.getFn = func(key string) (any, bool, bool) {
			if key == "active_endpoint" && ctrl.opBusy.Load() && injectedBusy.CompareAndSwap(false, true) {
				ctrl.mu.Lock()
				rt := deepCopyMap(asMap(ctrl.data["runtime"]))
				rt["comfyui"] = map[string]any{"running": true, "state": "running"}
				ctrl.data["runtime"] = rt
				ctrl.mu.Unlock()
			}
			return nil, false, false
		}
		hv.mu.Unlock()

		ctrl.MonitorOnce(context.Background())

		if !injectedBusy.Load() {
			t.Fatal("test hook did not fire inside auto-upgrade onStart")
		}
		if ctrl.IsBusy() {
			t.Fatal("expected opBusy released immediately when onStart skips auto-upgrade")
		}
		prov.mu.Lock()
		upgrades1 := append([]string(nil), prov.upgradeCalls...)
		prov.mu.Unlock()
		if len(upgrades1) != 0 {
			t.Fatalf("expected no UpgradeService call when ComfyUI became active, got %#v", upgrades1)
		}
		ctrl.mu.Lock()
		att1 := ctrl.upgradeAttempted["ep-1"]
		ctrl.mu.Unlock()
		if att1 {
			t.Fatal("expected upgradeAttempted[ep-1] NOT set when onStart skipped auto-upgrade")
		}
		for _, entry := range ctrl.activity.Snapshot() {
			if strings.Contains(entry.Message, "更新远端管理服务") {
				t.Fatalf("unexpected upgrade log when onStart skipped: %+v", entry)
			}
		}

		// Next clean tick with ep-1 idle again must succeed in auto-upgrading ep-1.
		ctrl.MonitorOnce(context.Background())
		waitUntil(t, 2*time.Second, func() bool {
			prov.mu.Lock()
			n := len(prov.upgradeCalls)
			prov.mu.Unlock()
			return n == 1 && !ctrl.IsBusy()
		})
		prov.mu.Lock()
		upgrades2 := append([]string(nil), prov.upgradeCalls...)
		prov.mu.Unlock()
		if len(upgrades2) != 1 || upgrades2[0] != "ep-1" {
			t.Fatalf("expected subsequent clean tick to upgrade ep-1, got %#v", upgrades2)
		}
	})
}

func TestSaveSettingsClearAssignProxyReturnsVaultDeleteError(t *testing.T) {
	cfgDir := t.TempDir()
	memVault := NewColabOneMemoryVault()
	hv := newHookVault(memVault)
	ctrl := NewColabOneController(ColabOneControllerConfig{
		Root:           cfgDir,
		ConfigDir:      cfgDir,
		Background:     false,
		Vault:          hv,
		Auth:           newMockAuth(),
		Provider:       newMockProvider(hv),
		AssignProxy:    &mockAssignProxy{},
		ServiceBundler: defaultMockBundler(),
	})
	defer ctrl.Close()

	if _, err := ctrl.SaveSettings(context.Background(), map[string]any{
		"assign_proxy":         "http://127.0.0.1:7890",
		"assign_proxy_enabled": true,
	}); err != nil {
		t.Fatalf("initial SaveSettings failed: %v", err)
	}

	deleteErr := errors.New("keychain delete failed")
	hv.mu.Lock()
	hv.deleteFn = func(key string) (error, bool) {
		if key == "assign_proxy" {
			return deleteErr, true
		}
		return nil, false
	}
	hv.mu.Unlock()

	res, err := ctrl.SaveSettings(context.Background(), map[string]any{
		"clear_assign_proxy": true,
	})
	if !errors.Is(err, deleteErr) {
		t.Fatalf("expected SaveSettings to return vault delete error %v, got res=%#v err=%v", deleteErr, res, err)
	}
	if !asBool(asMap(ctrl.Snapshot()["settings"])["assign_proxy_enabled"]) {
		t.Fatal("expected assign_proxy_enabled to remain true when vault.Delete failed")
	}
	if v, ok := memVault.Get("assign_proxy"); !ok || asString(v) != "http://127.0.0.1:7890" {
		t.Fatalf("expected assign_proxy secret untouched on delete failure, got ok=%v v=%#v", ok, v)
	}
	for _, entry := range ctrl.activity.Snapshot() {
		if strings.Contains(entry.Message, "已清除分配代理") {
			t.Fatalf("unexpected clear-proxy log on delete failure: %+v", entry)
		}
	}
}

func TestConcurrencyAndStateResetFixes(t *testing.T) {
	t.Run("stale_background_refresh_completing_after_async_op_finishes_is_discarded", func(t *testing.T) {
		h := newTestHarness(t, false)
		h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
		h.provider.mu.Lock()
		h.provider.savedRemotes = nil
		h.provider.mu.Unlock()
		h.ctrl.Update(map[string]any{
			"runtime": map[string]any{"state": "ready", "id": "ep-1", "protocol": 7, "idle_minutes": 20},
		})

		bgEntered := make(chan struct{})
		bgRelease := make(chan struct{})
		bgDone := make(chan struct{})
		var closeBGOnce sync.Once
		releaseBG := func() { closeBGOnce.Do(func() { close(bgRelease) }) }
		defer releaseBG()

		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "snapshot" {
				close(bgEntered)
				select {
				case <-bgRelease:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return map[string]any{
					"runtime": map[string]any{
						"state":        "ready",
						"id":           "ep-1",
						"protocol":     7,
						"idle_minutes": 99,
						"location":     map[string]any{"country": "DE", "city": "Frankfurt"},
						"tasks":        []any{},
					},
					"models": []any{
						map[string]any{"id": "stale-model", "name": "Stale Model", "downloaded": true, "loaded": true},
					},
					"comfy_outputs": []any{map[string]any{"filename": "stale.png"}},
				}, nil
			}
			return map[string]any{"ok": true}, nil
		}

		go func() {
			defer close(bgDone)
			_, _ = h.ctrl.refreshRemote(context.Background(), "", true)
		}()
		<-bgEntered

		// Run a complete asyncOperation to completion (opGen bumped, then opBusy returns to false).
		if _, err := h.ctrl.asyncOperation("同步实例状态", func() error {
			h.ctrl.Update(map[string]any{
				"runtime": map[string]any{
					"state":        "ready",
					"id":           "ep-1",
					"protocol":     7,
					"idle_minutes": 60,
				},
			})
			return nil
		}, nil, nil); err != nil {
			t.Fatalf("asyncOperation failed: %v", err)
		}
		waitIdle(t, h.ctrl)
		if h.ctrl.IsBusy() {
			t.Fatal("expected opBusy == false before releasing stale background refresh")
		}

		// Now unblock the pre-operation background refresh while opBusy == false.
		releaseBG()
		<-bgDone

		snap := h.ctrl.Snapshot()
		rt := asMap(snap["runtime"])
		if idle, _ := asInt(rt["idle_minutes"]); idle != 60 {
			t.Fatalf("stale background refresh overwrote c.data runtime idle_minutes to %d (want 60)", idle)
		}
		if outputs := asAnySlice(snap["comfy_outputs"]); len(outputs) != 0 {
			t.Fatalf("stale background refresh polluted c.data comfy_outputs: %#v", outputs)
		}
		for _, m := range asMapSlice(snap["models"]) {
			if asString(m["id"]) == "stale-model" {
				t.Fatalf("stale background refresh polluted c.data models: %#v", m)
			}
		}

		h.ctrl.mu.Lock()
		ep1Cache := h.ctrl.instanceStates["ep-1"]
		var cachedIdle int
		var cachedOutputsLen int
		if ep1Cache != nil {
			cachedIdle, _ = asInt(ep1Cache.Runtime["idle_minutes"])
			cachedOutputsLen = len(ep1Cache.ComfyOutputs)
		}
		h.ctrl.mu.Unlock()
		if cachedIdle != 60 || cachedOutputsLen != 0 {
			t.Fatalf("stale background refresh poisoned instanceStates[ep-1]: idle=%d outputs=%d", cachedIdle, cachedOutputsLen)
		}

		h.provider.mu.Lock()
		savedRemotes := append([]map[string]any(nil), h.provider.savedRemotes...)
		h.provider.mu.Unlock()
		if len(savedRemotes) != 0 {
			t.Fatalf("stale background refresh called SaveRemote after opGen changed: %#v", savedRemotes)
		}
	})

	t.Run("select_instance_during_heartbeat_eviction_and_direct_release_all_bump_opGen", func(t *testing.T) {
		cfgDir := t.TempDir()
		memVault := NewColabOneMemoryVault()
		hv := newHookVault(memVault)
		prov := newMockProvider(memVault)
		prov.addManagedSession("ep-1", "T4", "STANDARD", true)
		prov.addManagedSession("ep-2", "L4", "STANDARD", false)
		prov.addManagedSession("ep-3", "A100", "STANDARD", false)

		ctrl := NewColabOneController(ColabOneControllerConfig{
			Root:           cfgDir,
			ConfigDir:      cfgDir,
			Background:     false,
			Vault:          hv,
			Auth:           newMockAuth(),
			Provider:       prov,
			ServiceBundler: defaultMockBundler(),
		})
		defer ctrl.Close()

		ctrl.mu.Lock()
		ctrl.lastAuth = nowUnixFloat()
		ctrl.lastHeartbeat = 0
		ctrl.instanceStates["ep-1"] = &instanceCache{
			Runtime: map[string]any{"state": "ready", "id": "ep-1", "protocol": 7, "tag": "ep1"},
		}
		ctrl.instanceStates["ep-2"] = &instanceCache{
			Runtime: map[string]any{"state": "ready", "id": "ep-2", "protocol": 7, "tag": "ep2"},
		}
		ctrl.instanceStates["ep-3"] = &instanceCache{
			Runtime: map[string]any{"state": "ready", "id": "ep-3", "protocol": 7, "tag": "ep3"},
		}
		ctrl.data["runtime"] = map[string]any{"state": "ready", "id": "ep-1", "protocol": 7, "tag": "ep1"}
		ctrl.mu.Unlock()

		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "snapshot" {
				return map[string]any{
					"runtime": map[string]any{"state": "ready", "id": "ep-3", "protocol": 7, "tag": "ep3", "tasks": []any{}},
					"models":  []any{},
				}, nil
			}
			return map[string]any{"ok": true}, nil
		}

		var selectRan atomic.Bool
		var genBeforeSelect, genAfterSelect uint64
		prov.heartbeatFn = func(ctx context.Context, endpoint string) error {
			if endpoint == "" {
				// Evict dead ep-1; vault active_endpoint fails over to ep-2.
				prov.Clear("ep-1")
				// Intercept MonitorOnce's activeEndpointFrom(remainingSessions) right after opGen was
				// captured and simulate SelectInstance("ep-3") completing before MonitorOnce locks c.mu.
				hv.mu.Lock()
				hv.getFn = func(key string) (any, bool, bool) {
					if key == "active_endpoint" && selectRan.CompareAndSwap(false, true) {
						hv.mu.Lock()
						hv.getFn = nil
						hv.mu.Unlock()
						genBeforeSelect = ctrl.opGen.Load()
						if _, err := ctrl.SelectInstance(ctx, "ep-3"); err != nil {
							t.Errorf("SelectInstance(ep-3) failed: %v", err)
						}
						genAfterSelect = ctrl.opGen.Load()
						return "ep-2", true, true
					}
					return nil, false, false
				}
				hv.mu.Unlock()
			}
			return nil
		}

		ctrl.MonitorOnce(context.Background())
		ctrl.wg.Wait()

		if !selectRan.Load() {
			t.Fatal("test hook did not fire SelectInstance(ep-3) during MonitorOnce failover")
		}
		if genAfterSelect <= genBeforeSelect {
			t.Fatalf("expected SelectInstance to bump opGen, before=%d after=%d", genBeforeSelect, genAfterSelect)
		}
		if got := ctrl.activeEndpoint(); got != "ep-3" {
			t.Fatalf("expected active endpoint ep-3, got %q", got)
		}
		rt := asMap(ctrl.Snapshot()["runtime"])
		if asString(rt["id"]) != "ep-3" || asString(rt["tag"]) != "ep3" {
			t.Fatalf("MonitorOnce reverted SelectInstance(ep-3) back to %#v", rt)
		}

		// Direct Go call to ReleaseAll (outside asyncOperation) must also bump opGen.
		prov.accountFn = func(ctx context.Context) (map[string]any, error) {
			sess := ctrl.vaultSessions()
			insts := make([]map[string]any, 0, len(sess))
			for ep := range sess {
				insts = append(insts, map[string]any{"id": ep, "managed": true})
			}
			return map[string]any{
				"logged_in":   true,
				"balance":     100.0,
				"rate":        0.0,
				"assignments": len(insts),
				"instances":   insts,
			}, nil
		}
		plan, err := ctrl.PreviewReleaseAll(context.Background())
		if err != nil {
			t.Fatalf("PreviewReleaseAll failed: %v", err)
		}
		genBeforeReleaseAll := ctrl.opGen.Load()
		if err := ctrl.ReleaseAll(context.Background(), asString(plan["id"]), true, nil, false); err != nil {
			t.Fatalf("direct ReleaseAll failed: %v", err)
		}
		if got := ctrl.opGen.Load(); got <= genBeforeReleaseAll {
			t.Fatalf("expected direct ReleaseAll to bump opGen, before=%d after=%d", genBeforeReleaseAll, got)
		}
	})

	t.Run("start_cancel_after_post_start_refresh_rolls_back_remote_models_workflows_and_outputs", func(t *testing.T) {
		h := newTestHarness(t, false)
		h.provider.startFn = func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
			if notify != nil {
				notify("部署远端管理服务")
			}
			h.provider.addManagedSession("ep-cancel-dirty", gpu, "STANDARD", true)
			return map[string]any{"endpoint": "ep-cancel-dirty"}, nil
		}

		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "snapshot" {
				// Cancel during RefreshRemote while still returning a populated remote snapshot so
				// RefreshRemote populates models, workflows, comfy_outputs, and remoteWorkflowVersions
				// before Start enters rollback.
				if _, err := h.ctrl.Command(context.Background(), "runtime.start.cancel", nil); err != nil {
					t.Errorf("runtime.start.cancel failed: %v", err)
				}
				return map[string]any{
					"runtime": map[string]any{
						"state":        "ready",
						"id":           "ep-cancel-dirty",
						"protocol":     7,
						"loaded_model": "remote-ghost-model",
						"tasks":        []any{},
					},
					"models": []any{
						map[string]any{
							"id":         "remote-ghost-model",
							"name":       "Remote Ghost Model",
							"downloaded": true,
							"loaded":     true,
						},
					},
					"workflows": []any{
						map[string]any{
							"id":         "wf-remote-ghost",
							"name":       "Remote Ghost Workflow",
							"updated_at": 1700000000.0,
						},
					},
					"comfy_outputs": []any{
						map[string]any{"filename": "ghost-output.png", "subfolder": ""},
					},
				}, nil
			}
			return map[string]any{"ok": true}, nil
		}

		if _, err := h.ctrl.Command(context.Background(), "runtime.start", map[string]any{"gpu": "T4"}); err != nil {
			t.Fatalf("runtime.start failed: %v", err)
		}
		waitIdle(t, h.ctrl)

		snap := h.ctrl.Snapshot()
		if st := asString(asMap(snap["runtime"])["state"]); st != "stopped" {
			t.Fatalf("expected runtime.state=stopped after rollback, got %q", st)
		}
		models := asMapSlice(snap["models"])
		if want := len(h.ctrl.Presets()); len(models) != want {
			t.Fatalf("expected local Presets() count %d after rollback, got %d (%#v)", want, len(models), models)
		}
		for _, m := range models {
			if asString(m["id"]) == "remote-ghost-model" || asBool(m["downloaded"]) || asBool(m["loaded"]) {
				t.Fatalf("ghost remote model remained after Start rollback: %#v", m)
			}
		}
		for _, wf := range asMapSlice(snap["workflows"]) {
			if asString(wf["id"]) == "wf-remote-ghost" {
				t.Fatalf("ghost remote workflow remained after Start rollback: %#v", wf)
			}
		}
		if outputs := asAnySlice(snap["comfy_outputs"]); len(outputs) != 0 {
			t.Fatalf("ghost comfy_outputs remained after Start rollback: %#v", outputs)
		}
		h.ctrl.mu.Lock()
		wfVerCount := len(h.ctrl.remoteWorkflowVersions)
		h.ctrl.mu.Unlock()
		if wfVerCount != 0 {
			t.Fatalf("expected remoteWorkflowVersions cleared after Start rollback, got %d entries", wfVerCount)
		}
	})

	t.Run("monitor_heartbeat_failover_resets_last_heartbeat", func(t *testing.T) {
		h := newTestHarness(t, false)
		h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
		h.provider.addManagedSession("ep-2", "A100", "STANDARD", false)

		h.ctrl.mu.Lock()
		h.ctrl.lastAuth = nowUnixFloat()
		h.ctrl.lastHeartbeat = nowUnixFloat() - 60 // non-zero & stale so Heartbeat fires
		h.ctrl.lastConfig = nowUnixFloat()
		h.ctrl.instanceStates["ep-1"] = &instanceCache{
			Runtime: map[string]any{"state": "ready", "id": "ep-1", "protocol": 7},
		}
		h.ctrl.instanceStates["ep-2"] = &instanceCache{
			Runtime: map[string]any{"state": "ready", "id": "ep-2", "protocol": 7, "tag": "from-ep2"},
		}
		h.ctrl.data["runtime"] = map[string]any{"state": "ready", "id": "ep-1", "protocol": 7}
		h.ctrl.mu.Unlock()

		var hbCount atomic.Int32
		h.provider.heartbeatFn = func(ctx context.Context, endpoint string) error {
			if endpoint == "" && hbCount.Add(1) == 1 {
				// First heartbeat evicts dead ep-1 -> failover to ep-2.
				h.provider.Clear("ep-1")
			}
			return nil
		}
		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "snapshot" {
				return map[string]any{
					"runtime": map[string]any{
						"state":            "ready",
						"id":               "ep-2",
						"protocol":         7,
						"service_revision": h.ctrl.ExpectedServiceRevision(),
						"tag":              "from-ep2",
						"tasks":            []any{},
					},
					"models": []any{},
				}, nil
			}
			return map[string]any{"ok": true}, nil
		}

		// Tick 1: ep-1 is evicted and MonitorOnce fails over to ep-2.
		h.ctrl.MonitorOnce(context.Background())

		h.ctrl.mu.Lock()
		hbAfterFailover := h.ctrl.lastHeartbeat
		rt := deepCopyMap(asMap(h.ctrl.data["runtime"]))
		h.ctrl.mu.Unlock()
		if asString(rt["id"]) != "ep-2" || asString(rt["tag"]) != "from-ep2" {
			t.Fatalf("expected failover to ep-2, got %#v", rt)
		}
		if hbAfterFailover != 0 {
			t.Fatalf("expected lastHeartbeat reset to 0 on failover to ep-2, got %v", hbAfterFailover)
		}

		// Tick 2 immediately after failover: ep-2 must be heartbeated right away (not skipped for 30s)
		// and stamp lastHeartbeat once ep-2 remains active.
		h.ctrl.MonitorOnce(context.Background())

		if got := hbCount.Load(); got != 2 {
			t.Fatalf("expected immediate Heartbeat for newly active ep-2 on next tick, got %d total calls", got)
		}
		h.ctrl.mu.Lock()
		hbAfterHealthy := h.ctrl.lastHeartbeat
		h.ctrl.mu.Unlock()
		if hbAfterHealthy <= 0 {
			t.Fatalf("expected lastHeartbeat stamped after healthy ep-2 heartbeat, got %v", hbAfterHealthy)
		}

		// Evicting the last remaining session must also reset lastHeartbeat to 0.
		h.ctrl.mu.Lock()
		h.ctrl.lastHeartbeat = nowUnixFloat() - 60
		h.ctrl.mu.Unlock()
		h.provider.heartbeatFn = func(ctx context.Context, endpoint string) error {
			if endpoint == "" {
				h.provider.Clear("ep-2")
			}
			return nil
		}
		h.ctrl.MonitorOnce(context.Background())

		h.ctrl.mu.Lock()
		hbAfterEmpty := h.ctrl.lastHeartbeat
		finalState := asString(asMap(h.ctrl.data["runtime"])["state"])
		h.ctrl.mu.Unlock()
		if finalState != "stopped" || hbAfterEmpty != 0 {
			t.Fatalf("expected stopped state and lastHeartbeat=0 after last session evicted, got state=%q lastHeartbeat=%v", finalState, hbAfterEmpty)
		}
	})
}

func TestBackgroundConfigureRefreshDisconnectAndRollbackGuards(t *testing.T) {
	t.Run("background_configure_override_sync_does_not_clobber_concurrent_start_and_select_instance", func(t *testing.T) {
		// Part A: MonitorOnce -> configureRemote(..., true) -> syncRemoteAPIIDOverrides(..., true)
		// with changed=true while Start(createNew=true) is running must NOT overwrite "starting"
		// or operation.kind=="runtime_start".
		h := newTestHarness(t, false)
		h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
		expectedRev := h.ctrl.ExpectedServiceRevision()
		h.ctrl.mu.Lock()
		h.ctrl.lastAuth = nowUnixFloat()
		h.ctrl.lastHeartbeat = nowUnixFloat()
		h.ctrl.lastConfig = 0
		h.ctrl.apiIDOverrides["gemma4-31b"] = "my-custom-alias"
		h.ctrl.data["runtime"] = map[string]any{
			"state":             "ready",
			"id":                "ep-1",
			"protocol":          7,
			"service_revision":  expectedRev,
			"api_model_aliases": true,
			"tasks":             []any{},
		}
		h.ctrl.data["models"] = []map[string]any{
			{"id": "gemma4-31b", "name": "Gemma 4", "api_id": "gemma4-31b"},
		}
		h.ctrl.mu.Unlock()

		overrideEntered := make(chan struct{})
		overrideRelease := make(chan struct{})
		var releaseOverrideOnce sync.Once
		releaseOverride := func() { releaseOverrideOnce.Do(func() { close(overrideRelease) }) }
		defer releaseOverride()

		startInFlight := make(chan struct{})
		startRelease := make(chan struct{})
		var releaseStartOnce sync.Once
		releaseStart := func() { releaseStartOnce.Do(func() { close(startRelease) }) }
		defer releaseStart()

		var startEntered atomic.Bool
		var snapDuringStart atomic.Int32
		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			switch {
			case method == "GET" && path == "snapshot":
				if startEntered.Load() {
					snapDuringStart.Add(1)
				}
				return map[string]any{
					"runtime": map[string]any{
						"state":             "ready",
						"id":                "ep-1",
						"protocol":          7,
						"service_revision":  expectedRev,
						"api_model_aliases": true,
						"tasks":             []any{},
					},
					"models": []any{
						map[string]any{"id": "gemma4-31b", "name": "Gemma 4", "api_id": "gemma4-31b"},
					},
				}, nil
			case method == "POST" && path == "models/gemma4-31b/api-id":
				close(overrideEntered)
				<-overrideRelease
				return map[string]any{"ok": true}, nil
			default:
				return map[string]any{"ok": true}, nil
			}
		}
		h.provider.startFn = func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
			if notify != nil {
				notify("正在申请 GPU...")
			}
			startEntered.Store(true)
			close(startInFlight)
			<-startRelease
			return nil, errors.New("abort test start")
		}

		monDone := make(chan struct{})
		go func() {
			defer close(monDone)
			h.ctrl.MonitorOnce(context.Background())
		}()
		<-overrideEntered

		if _, err := h.ctrl.Command(context.Background(), "runtime.start", map[string]any{
			"new": true,
			"gpu": "A100",
		}); err != nil {
			t.Fatalf("runtime.start failed: %v", err)
		}
		<-startInFlight

		// Unblock override sync (changed=true) while Start(createNew=true) is actively starting.
		releaseOverride()
		<-monDone

		snap := h.ctrl.Snapshot()
		if st := asString(asMap(snap["runtime"])["state"]); st != "starting" {
			t.Fatalf("background override sync overwrote runtime.state during Start: got %q, want starting", st)
		}
		if kind := asString(asMap(snap["operation"])["kind"]); kind != "runtime_start" {
			t.Fatalf("background override sync clobbered operation.kind during Start: got %q, want runtime_start", kind)
		}
		if got := snapDuringStart.Load(); got != 0 {
			t.Fatalf("expected 0 background snapshot RPCs while Start was running, got %d", got)
		}

		releaseStart()
		waitIdle(t, h.ctrl)

		// Part B: SaveSettings live idle_minutes push goroutine hitting syncRemoteAPIIDOverrides
		// while Start(createNew=true) runs must also NOT clobber "starting" / "runtime_start".
		hSave := newTestHarness(t, false)
		hSave.provider.addManagedSession("ep-save", "T4", "STANDARD", true)
		hSave.ctrl.mu.Lock()
		hSave.ctrl.apiIDOverrides["gemma4-31b"] = "alias-from-save"
		hSave.ctrl.data["runtime"] = map[string]any{
			"state":             "ready",
			"id":                "ep-save",
			"protocol":          7,
			"service_revision":  expectedRev,
			"api_model_aliases": true,
			"tasks":             []any{},
		}
		hSave.ctrl.data["models"] = []map[string]any{
			{"id": "gemma4-31b", "name": "Gemma 4", "api_id": "gemma4-31b"},
		}
		hSave.ctrl.mu.Unlock()

		saveOvrEntered := make(chan struct{})
		saveOvrRelease := make(chan struct{})
		var releaseSaveOvrOnce sync.Once
		releaseSaveOvr := func() { releaseSaveOvrOnce.Do(func() { close(saveOvrRelease) }) }
		defer releaseSaveOvr()

		saveStartEntered := make(chan struct{})
		saveStartRelease := make(chan struct{})
		var releaseSaveStartOnce sync.Once
		releaseSaveStart := func() { releaseSaveStartOnce.Do(func() { close(saveStartRelease) }) }
		defer releaseSaveStart()

		var saveSnapCount atomic.Int32
		hSave.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			switch {
			case method == "POST" && path == "models/gemma4-31b/api-id":
				close(saveOvrEntered)
				<-saveOvrRelease
				return map[string]any{"ok": true}, nil
			case method == "GET" && path == "snapshot":
				saveSnapCount.Add(1)
				return map[string]any{
					"runtime": map[string]any{"state": "ready", "id": "ep-save", "protocol": 7, "tasks": []any{}},
					"models":  []any{},
				}, nil
			default:
				return map[string]any{"ok": true}, nil
			}
		}
		hSave.provider.startFn = func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
			if notify != nil {
				notify("正在申请 GPU...")
			}
			close(saveStartEntered)
			<-saveStartRelease
			return nil, errors.New("abort save test start")
		}

		if _, err := hSave.ctrl.Command(context.Background(), "settings.save", map[string]any{"idle_minutes": 45}); err != nil {
			t.Fatalf("settings.save failed: %v", err)
		}
		<-saveOvrEntered

		if _, err := hSave.ctrl.Command(context.Background(), "runtime.start", map[string]any{
			"new": true,
			"gpu": "L4",
		}); err != nil {
			t.Fatalf("runtime.start during SaveSettings push failed: %v", err)
		}
		<-saveStartEntered

		releaseSaveOvr()
		// Wait until SaveSettings background goroutine settles by letting Start keep opBusy held briefly
		// and verifying no snapshot RPC fired and state remained "starting".
		waitUntil(t, 2*time.Second, func() bool {
			// Ensure no bogus sync log and starting state holds.
			snap := hSave.ctrl.Snapshot()
			return asString(asMap(snap["runtime"])["state"]) == "starting" &&
				asString(asMap(snap["operation"])["kind"]) == "runtime_start"
		})
		if saveSnapCount.Load() != 0 {
			t.Fatalf("SaveSettings push issued snapshot RPC while Start was active: got %d", saveSnapCount.Load())
		}

		releaseSaveStart()
		waitIdle(t, hSave.ctrl)
		hSave.ctrl.Close()
		if saveSnapCount.Load() != 0 {
			t.Fatalf("SaveSettings stale push issued snapshot RPC after opGen changed: got %d", saveSnapCount.Load())
		}
		for _, entry := range hSave.ctrl.activity.Snapshot() {
			if strings.Contains(entry.Message, "已向实例 ep-save 同步空闲释放时长") {
				t.Fatalf("SaveSettings emitted stale idle_minutes sync log after Start intervened: %+v", entry)
			}
		}

		// Part C: Concurrent SelectInstance(ep-2) during ep-1's background configureRemote(ctx, "", true)
		// must NOT let ep-1 stamp c.lastConfig onto ep-2.
		hSel := newTestHarness(t, false)
		hSel.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
		hSel.provider.addManagedSession("ep-2", "A100", "STANDARD", false)
		hSel.ctrl.mu.Lock()
		hSel.ctrl.lastAuth = nowUnixFloat()
		hSel.ctrl.lastHeartbeat = nowUnixFloat()
		hSel.ctrl.lastConfig = 0
		hSel.ctrl.instanceStates["ep-1"] = &instanceCache{
			Runtime:    map[string]any{"state": "ready", "id": "ep-1", "protocol": 7, "service_revision": expectedRev},
			LastConfig: 0,
		}
		hSel.ctrl.instanceStates["ep-2"] = &instanceCache{
			Runtime:    map[string]any{"state": "ready", "id": "ep-2", "protocol": 7, "service_revision": expectedRev},
			LastConfig: 0,
		}
		hSel.ctrl.data["runtime"] = map[string]any{"state": "ready", "id": "ep-1", "protocol": 7, "service_revision": expectedRev}
		hSel.ctrl.mu.Unlock()

		var switched atomic.Bool
		hSel.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "snapshot" {
				ep := opts.Endpoint
				if ep == "" {
					ep = hSel.ctrl.activeEndpoint()
				}
				return map[string]any{
					"runtime": map[string]any{
						"state":            "ready",
						"id":               ep,
						"protocol":         7,
						"service_revision": expectedRev,
						"tasks":            []any{},
					},
					"models": []any{},
				}, nil
			}
			if path == "configure" && opts.Endpoint == "" && switched.CompareAndSwap(false, true) {
				if _, err := hSel.ctrl.SelectInstance(ctx, "ep-2"); err != nil {
					t.Errorf("SelectInstance(ep-2) failed: %v", err)
				}
			}
			return map[string]any{"ok": true}, nil
		}

		hSel.ctrl.MonitorOnce(context.Background())
		hSel.ctrl.wg.Wait()

		if !switched.Load() {
			t.Fatal("expected SelectInstance(ep-2) hook to fire during background configureRemote")
		}
		if got := hSel.ctrl.activeEndpoint(); got != "ep-2" {
			t.Fatalf("expected active endpoint ep-2, got %q", got)
		}
		hSel.ctrl.mu.Lock()
		gotLastCfg := hSel.ctrl.lastConfig
		ep2LastCfg := hSel.ctrl.instanceStates["ep-2"].LastConfig
		hSel.ctrl.mu.Unlock()
		if gotLastCfg != 0 {
			t.Fatalf("ep-1 background configureRemote stamped c.lastConfig=%v onto ep-2 (want 0)", gotLastCfg)
		}
		if ep2LastCfg != 0 {
			t.Fatalf("expected ep-2 LastConfig=0 before ep-2 was configured, got %v", ep2LastCfg)
		}
	})

	t.Run("background_refresh_bails_out_when_opBusy_and_asyncOperation_exit_bumps_opGen", func(t *testing.T) {
		h := newTestHarness(t, false)
		h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
		h.ctrl.Update(map[string]any{
			"runtime": map[string]any{"state": "ready", "id": "ep-1", "protocol": 7, "idle_minutes": 30},
		})

		var snapshotCalls atomic.Int32
		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			switch path {
			case "snapshot":
				snapshotCalls.Add(1)
				return map[string]any{
					"runtime": map[string]any{"state": "ready", "id": "ep-1", "protocol": 7, "idle_minutes": 999, "tasks": []any{}},
					"models":  []any{},
				}, nil
			case "status":
				return map[string]any{
					"comfyui": map[string]any{"running": true, "canvas": map[string]any{"enabled": false, "state": "stopped"}},
				}, nil
			default:
				return map[string]any{"ok": true}, nil
			}
		}

		opEntered := make(chan struct{})
		opRelease := make(chan struct{})
		var releaseOpOnce sync.Once
		releaseOp := func() { releaseOpOnce.Do(func() { close(opRelease) }) }
		defer releaseOp()

		if _, err := h.ctrl.asyncOperation("测试独占操作", func() error {
			close(opEntered)
			<-opRelease
			h.ctrl.Update(map[string]any{
				"runtime": map[string]any{"state": "ready", "id": "ep-1", "protocol": 7, "idle_minutes": 60},
			})
			return nil
		}, nil, nil); err != nil {
			t.Fatalf("asyncOperation failed: %v", err)
		}
		<-opEntered

		genDuringOp := h.ctrl.opGen.Load()
		st, err := h.ctrl.refreshRemote(context.Background(), "", true)
		if err != nil || st != nil {
			t.Fatalf("expected immediate (nil, nil) bail-out from background refreshRemote while opBusy, got state=%#v err=%v", st, err)
		}
		if got := snapshotCalls.Load(); got != 0 {
			t.Fatalf("background refreshRemote issued %d GET snapshot calls while opBusy==true", got)
		}

		// RestoreComfyCanvasWhenReady uses refreshRemote(..., true), so its post-start refresh
		// must also bail out without issuing a GET snapshot while opBusy==true.
		h.ctrl.RestoreComfyCanvasWhenReady(context.Background(), map[string]any{"mode": "named"}, "ep-1", "", time.Second)
		if got := snapshotCalls.Load(); got != 0 {
			t.Fatalf("RestoreComfyCanvasWhenReady issued %d GET snapshot calls while opBusy==true", got)
		}

		releaseOp()
		waitIdle(t, h.ctrl)

		genAfterOp := h.ctrl.opGen.Load()
		if genAfterOp <= genDuringOp {
			t.Fatalf("expected asyncOperation exit defer to bump opGen, during=%d after=%d", genDuringOp, genAfterOp)
		}
		if idle, _ := asInt(asMap(h.ctrl.Snapshot()["runtime"])["idle_minutes"]); idle != 60 {
			t.Fatalf("expected post-op idle_minutes=60 preserved, got %d", idle)
		}
	})

	t.Run("monitor_active_and_secondary_error_fallbacks_guarded_against_concurrent_asyncOperation", func(t *testing.T) {
		// Part A: Active error fallback must NOT clobber "ready" with "disconnected" or log
		// "远端控制连接中断，实例可能仍在计费" when UpgradeService completed while MonitorOnce RPC failed.
		h := newTestHarness(t, false)
		h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
		expectedRev := h.ctrl.ExpectedServiceRevision()
		h.ctrl.mu.Lock()
		h.ctrl.lastAuth = nowUnixFloat()
		h.ctrl.lastHeartbeat = nowUnixFloat()
		h.ctrl.lastConfig = nowUnixFloat()
		h.ctrl.data["runtime"] = map[string]any{
			"state":            "ready",
			"id":               "ep-1",
			"protocol":         7,
			"service_revision": "oldrev0000000000",
		}
		h.ctrl.mu.Unlock()

		monEntered := make(chan struct{})
		monRelease := make(chan struct{})
		var releaseMonOnce sync.Once
		releaseMon := func() { releaseMonOnce.Do(func() { close(monRelease) }) }
		defer releaseMon()

		var firstSnap atomic.Bool
		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if method == "GET" && path == "snapshot" {
				if firstSnap.CompareAndSwap(false, true) {
					close(monEntered)
					<-monRelease
					return nil, errors.New("connection reset while service restarted")
				}
				return map[string]any{
					"runtime": map[string]any{
						"state":            "ready",
						"id":               "ep-1",
						"protocol":         7,
						"service_revision": expectedRev,
						"upgraded":         true,
						"tasks":            []any{},
					},
					"models": []any{},
				}, nil
			}
			return map[string]any{"ok": true}, nil
		}

		monDone := make(chan struct{})
		go func() {
			defer close(monDone)
			h.ctrl.MonitorOnce(context.Background())
		}()
		<-monEntered

		if _, err := h.ctrl.Command(context.Background(), "runtime.upgrade", nil); err != nil {
			t.Fatalf("runtime.upgrade failed: %v", err)
		}
		waitIdle(t, h.ctrl)
		if h.ctrl.IsBusy() {
			t.Fatal("expected opBusy==false after UpgradeService completed")
		}

		// Now unblock MonitorOnce's failing snapshot RPC while opBusy==false.
		releaseMon()
		<-monDone

		rt := asMap(h.ctrl.Snapshot()["runtime"])
		if asString(rt["state"]) != "ready" || !asBool(rt["upgraded"]) {
			t.Fatalf("MonitorOnce active error clobbered post-upgrade runtime: %#v", rt)
		}
		h.ctrl.mu.Lock()
		cachedState := asString(h.ctrl.instanceStates["ep-1"].Runtime["state"])
		h.ctrl.mu.Unlock()
		if cachedState != "ready" {
			t.Fatalf("MonitorOnce active error clobbered instanceStates[ep-1]: state=%q", cachedState)
		}
		for _, entry := range h.ctrl.activity.Snapshot() {
			if strings.Contains(entry.Message, "远端控制连接中断，实例可能仍在计费") {
				t.Fatalf("MonitorOnce emitted bogus disconnected log after concurrent UpgradeService: %+v", entry)
			}
		}

		// Part B: Secondary error fallbacks (heartbeat, refresh, configure) must NOT clobber
		// healthy ep-2 cache with "disconnected" after a concurrent asyncOperation completed.
		type secCase struct {
			name    string
			lastHB  float64
			lastCfg float64
			failAt  string
		}
		now := nowUnixFloat()
		for _, tc := range []secCase{
			{name: "heartbeat_error", lastHB: 0, lastCfg: now, failAt: "heartbeat"},
			{name: "refresh_error", lastHB: now, lastCfg: now, failAt: "snapshot"},
			{name: "configure_error", lastHB: now, lastCfg: 0, failAt: "configure"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h2 := newTestHarness(t, false)
				h2.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
				h2.provider.addManagedSession("ep-2", "L4", "STANDARD", false)
				h2.ctrl.mu.Lock()
				h2.ctrl.lastAuth = now
				h2.ctrl.lastHeartbeat = now
				h2.ctrl.lastConfig = now
				h2.ctrl.data["runtime"] = map[string]any{
					"state":            "ready",
					"id":               "ep-1",
					"protocol":         7,
					"service_revision": expectedRev,
				}
				h2.ctrl.instanceStates["ep-2"] = &instanceCache{
					Runtime:       map[string]any{"state": "ready", "id": "ep-2", "protocol": 7, "tag": "healthy-ep2"},
					LastHeartbeat: tc.lastHB,
					LastConfig:    tc.lastCfg,
				}
				h2.ctrl.mu.Unlock()

				secEntered := make(chan struct{})
				secRelease := make(chan struct{})
				var releaseSecOnce sync.Once
				releaseSec := func() { releaseSecOnce.Do(func() { close(secRelease) }) }
				defer releaseSec()

				h2.provider.heartbeatFn = func(ctx context.Context, endpoint string) error {
					if endpoint == "ep-2" && tc.failAt == "heartbeat" {
						close(secEntered)
						<-secRelease
						return errors.New("secondary hb failed")
					}
					return nil
				}
				h2.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
					if opts.Endpoint == "ep-2" && path == tc.failAt {
						close(secEntered)
						<-secRelease
						return nil, errors.New("secondary " + tc.failAt + " failed")
					}
					if path == "snapshot" {
						ep := opts.Endpoint
						if ep == "" {
							ep = "ep-1"
						}
						return map[string]any{
							"runtime": map[string]any{
								"state":            "ready",
								"id":               ep,
								"protocol":         7,
								"service_revision": expectedRev,
								"tag":              "healthy-" + ep,
								"tasks":            []any{},
							},
							"models": []any{},
						}, nil
					}
					return map[string]any{"ok": true}, nil
				}

				secDone := make(chan struct{})
				go func() {
					defer close(secDone)
					h2.ctrl.MonitorOnce(context.Background())
				}()
				<-secEntered

				if _, err := h2.ctrl.asyncOperation("并发操作", func() error {
					h2.ctrl.mu.Lock()
					h2.ctrl.instanceStates["ep-2"].Runtime = map[string]any{
						"state": "ready",
						"id":    "ep-2",
						"tag":   "post-op-ep2",
					}
					h2.ctrl.mu.Unlock()
					return nil
				}, nil, nil); err != nil {
					t.Fatalf("asyncOperation failed: %v", err)
				}
				waitIdle(t, h2.ctrl)

				releaseSec()
				<-secDone

				h2.ctrl.mu.Lock()
				ep2RT := deepCopyMap(h2.ctrl.instanceStates["ep-2"].Runtime)
				h2.ctrl.mu.Unlock()
				if asString(ep2RT["state"]) != "ready" || asString(ep2RT["tag"]) != "post-op-ep2" {
					t.Fatalf("MonitorOnce secondary %s clobbered ep-2 runtime: %#v", tc.failAt, ep2RT)
				}
			})
		}
	})

	t.Run("start_create_new_rollback_restores_surviving_secondary_and_sets_disconnected_id", func(t *testing.T) {
		// Part A: previousActive (ep-1) vanishes mid-start while pre-existing secondary ep-2 survives.
		h := newTestHarness(t, false)
		h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
		h.provider.addManagedSession("ep-2", "A100", "STANDARD", false)
		h.ctrl.mu.Lock()
		h.ctrl.data["runtime"] = map[string]any{"state": "ready", "id": "ep-1", "protocol": 7}
		h.ctrl.instanceStates["ep-2"] = &instanceCache{
			Runtime: map[string]any{"state": "ready", "id": "ep-2", "protocol": 7, "tag": "ep2-healthy"},
			Models: []map[string]any{
				{"id": "ep2-model", "name": "EP2 Model", "downloaded": true, "loaded": true},
			},
			Workflows: []map[string]any{
				{"id": "wf-ep2", "name": "EP2 Workflow", "updated_at": 1700000123.0},
			},
			ComfyOutputs:           []any{map[string]any{"filename": "ep2-out.png", "subfolder": ""}},
			RemoteWorkflowVersions: map[string]float64{"wf-ep2": 1700000123.0},
		}
		h.ctrl.mu.Unlock()

		h.provider.startFn = func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
			// Simulate ep-1 vanishing mid-start; surviving pre-existing ep-2 becomes remainingActive.
			h.provider.Clear("ep-1")
			return nil, errors.New("GPU quota exhausted")
		}

		if _, err := h.ctrl.Command(context.Background(), "runtime.start", map[string]any{
			"new": true,
			"gpu": "L4",
		}); err != nil {
			t.Fatalf("runtime.start failed: %v", err)
		}
		waitIdle(t, h.ctrl)

		if got := h.ctrl.activeEndpoint(); got != "ep-2" {
			t.Fatalf("expected active endpoint ep-2 after rollback, got %q", got)
		}
		snap := h.ctrl.Snapshot()
		rt := asMap(snap["runtime"])
		if asString(rt["state"]) != "ready" || asString(rt["id"]) != "ep-2" || asString(rt["tag"]) != "ep2-healthy" {
			t.Fatalf("expected surviving ep-2 restored to ready runtime, got %#v", rt)
		}
		foundModel := false
		for _, m := range asMapSlice(snap["models"]) {
			if asString(m["id"]) == "ep2-model" && asBool(m["loaded"]) {
				foundModel = true
				break
			}
		}
		if !foundModel {
			t.Fatalf("expected surviving ep-2 cached models restored, got %#v", snap["models"])
		}
		foundWF := false
		for _, w := range asMapSlice(snap["workflows"]) {
			if asString(w["id"]) == "wf-ep2" {
				foundWF = true
				break
			}
		}
		if !foundWF {
			t.Fatalf("expected surviving ep-2 cached workflows restored, got %#v", snap["workflows"])
		}
		outputs := asAnySlice(snap["comfy_outputs"])
		if len(outputs) != 1 || asString(asMap(outputs[0])["filename"]) != "ep2-out.png" {
			t.Fatalf("expected surviving ep-2 cached comfy_outputs restored, got %#v", outputs)
		}
		h.ctrl.mu.Lock()
		ep2Cache := h.ctrl.instanceStates["ep-2"]
		ep2State := ""
		ep2ModelsLen := 0
		ep2OutputsLen := 0
		if ep2Cache != nil {
			ep2State = asString(ep2Cache.Runtime["state"])
			ep2ModelsLen = len(ep2Cache.Models)
			ep2OutputsLen = len(ep2Cache.ComfyOutputs)
		}
		h.ctrl.mu.Unlock()
		if ep2State != "ready" || ep2ModelsLen != 1 || ep2OutputsLen != 1 {
			t.Fatalf("rollback corrupted surviving ep-2 instanceStates cache: state=%q models=%d outputs=%d", ep2State, ep2ModelsLen, ep2OutputsLen)
		}

		// Part B: Non-existing remainingActive (e.g. newly allocated orphan whose Release failed)
		// must mark runtime disconnected WITH "id": remainingActive.
		h2 := newTestHarness(t, false)
		h2.provider.startFn = func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
			h2.provider.addManagedSession("ep-orphan", gpu, "STANDARD", true)
			return nil, errors.New("boot failed after assign")
		}
		h2.provider.releaseFn = func(ctx context.Context, endpoint string, onLog LogFunc) (map[string]any, error) {
			// Leave ep-orphan in vault to simulate failed orphan cleanup.
			return nil, errors.New("release rpc failed")
		}

		if _, err := h2.ctrl.Command(context.Background(), "runtime.start", map[string]any{
			"new": true,
			"gpu": "T4",
		}); err != nil {
			t.Fatalf("runtime.start (orphan) failed: %v", err)
		}
		waitIdle(t, h2.ctrl)

		rt2 := asMap(h2.ctrl.Snapshot()["runtime"])
		if asString(rt2["state"]) != "disconnected" || asString(rt2["id"]) != "ep-orphan" {
			t.Fatalf("expected runtime {state:disconnected, id:ep-orphan}, got %#v", rt2)
		}
	})
}

func TestMonitorOpGenAndSaveSettingsBailoutFixes(t *testing.T) {
	t.Run("monitor_active_opGen_sampling_and_sessions_refresh_on_concurrent_switch_or_start", func(t *testing.T) {
		// Part A: SelectInstance("ep-2") completing right after MonitorOnce resolves activeEP="ep-1"
		// (between activeEndpointFrom and entering the active block) must prevent BOTH the active block
		// and the secondary loop from running with stale activeEP="ep-1" / touching newly active "ep-2".
		cfgDir := t.TempDir()
		memVault := NewColabOneMemoryVault()
		hv := newHookVault(memVault)
		prov := newMockProvider(memVault)
		prov.addManagedSession("ep-1", "T4", "STANDARD", true)
		prov.addManagedSession("ep-2", "A100", "STANDARD", false)

		ctrl := NewColabOneController(ColabOneControllerConfig{
			Root:           cfgDir,
			ConfigDir:      cfgDir,
			Background:     false,
			Vault:          hv,
			Auth:           newMockAuth(),
			Provider:       prov,
			ServiceBundler: defaultMockBundler(),
		})
		defer ctrl.Close()

		now := nowUnixFloat()
		expectedRev := ctrl.ExpectedServiceRevision()
		ctrl.mu.Lock()
		ctrl.lastAuth = now
		ctrl.lastHeartbeat = now
		ctrl.lastConfig = now
		ctrl.data["runtime"] = map[string]any{"state": "ready", "id": "ep-1", "protocol": 7, "service_revision": expectedRev}
		ctrl.instanceStates["ep-2"] = &instanceCache{
			Runtime:       map[string]any{"state": "ready", "id": "ep-2", "protocol": 7, "service_revision": expectedRev},
			LastHeartbeat: 0,
			LastConfig:    0,
		}
		ctrl.mu.Unlock()

		var activeEmptySnapCalls atomic.Int32
		var ep2HeartbeatCalls atomic.Int32
		var ep2ConfigureCalls atomic.Int32
		prov.heartbeatFn = func(ctx context.Context, endpoint string) error {
			if endpoint == "ep-2" {
				ep2HeartbeatCalls.Add(1)
			}
			return nil
		}
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "snapshot" {
				if opts.Endpoint == "" {
					activeEmptySnapCalls.Add(1)
				}
				ep := opts.Endpoint
				if ep == "" {
					ep = ctrl.activeEndpoint()
				}
				return map[string]any{
					"runtime": map[string]any{
						"state":            "ready",
						"id":               ep,
						"protocol":         7,
						"service_revision": expectedRev,
						"tasks":            []any{},
					},
					"models": []any{},
				}, nil
			}
			if path == "configure" && opts.Endpoint == "ep-2" {
				ep2ConfigureCalls.Add(1)
			}
			return map[string]any{"ok": true}, nil
		}

		var switchHookFired atomic.Bool
		hv.mu.Lock()
		hv.getFn = func(key string) (any, bool, bool) {
			if key == "active_endpoint" && switchHookFired.CompareAndSwap(false, true) {
				hv.mu.Lock()
				hv.getFn = nil
				hv.mu.Unlock()
				if _, err := ctrl.SelectInstance(context.Background(), "ep-2"); err != nil {
					t.Errorf("SelectInstance(ep-2) failed: %v", err)
				}
				return "ep-1", true, true
			}
			return nil, false, false
		}
		hv.mu.Unlock()

		ctrl.MonitorOnce(context.Background())
		ctrl.wg.Wait()

		if !switchHookFired.Load() {
			t.Fatal("expected active_endpoint switch hook to fire in MonitorOnce")
		}
		if got := ctrl.activeEndpoint(); got != "ep-2" {
			t.Fatalf("expected active endpoint ep-2, got %q", got)
		}
		if got := activeEmptySnapCalls.Load(); got != 0 {
			t.Fatalf("MonitorOnce entered active block after concurrent SelectInstance(ep-2): got %d active snapshot calls", got)
		}
		if got := ep2HeartbeatCalls.Load(); got != 0 {
			t.Fatalf("MonitorOnce secondary loop heartbeated newly active ep-2: got %d calls", got)
		}
		if got := ep2ConfigureCalls.Load(); got != 0 {
			t.Fatalf("MonitorOnce secondary loop configured newly active ep-2: got %d calls", got)
		}

		// Part B: SelectInstance("ep-2") completing while MonitorOnce is running active refreshRemote
		// must abort before active configureRemote and must NOT let the secondary loop touch newly active "ep-2".
		hB := newTestHarness(t, false)
		hB.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
		hB.provider.addManagedSession("ep-2", "A100", "STANDARD", false)
		hB.ctrl.mu.Lock()
		hB.ctrl.lastAuth = now
		hB.ctrl.lastHeartbeat = now
		hB.ctrl.lastConfig = 0
		hB.ctrl.data["runtime"] = map[string]any{"state": "ready", "id": "ep-1", "protocol": 7, "service_revision": expectedRev}
		hB.ctrl.instanceStates["ep-2"] = &instanceCache{
			Runtime:       map[string]any{"state": "ready", "id": "ep-2", "protocol": 7, "service_revision": expectedRev},
			LastHeartbeat: 0,
			LastConfig:    0,
		}
		hB.ctrl.mu.Unlock()

		var switchedDuringRefresh atomic.Bool
		var activeCfgCallsB atomic.Int32
		var ep2HBCallsB atomic.Int32
		var ep2CfgCallsB atomic.Int32
		hB.provider.heartbeatFn = func(ctx context.Context, endpoint string) error {
			if endpoint == "ep-2" {
				ep2HBCallsB.Add(1)
			}
			return nil
		}
		hB.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "snapshot" && opts.Endpoint == "" && switchedDuringRefresh.CompareAndSwap(false, true) {
				if _, err := hB.ctrl.SelectInstance(ctx, "ep-2"); err != nil {
					t.Errorf("SelectInstance(ep-2) during refresh failed: %v", err)
				}
			}
			if path == "snapshot" {
				ep := opts.Endpoint
				if ep == "" {
					ep = hB.ctrl.activeEndpoint()
				}
				return map[string]any{
					"runtime": map[string]any{
						"state":            "ready",
						"id":               ep,
						"protocol":         7,
						"service_revision": expectedRev,
						"tasks":            []any{},
					},
					"models": []any{},
				}, nil
			}
			if path == "configure" {
				if opts.Endpoint == "" {
					activeCfgCallsB.Add(1)
				} else if opts.Endpoint == "ep-2" {
					ep2CfgCallsB.Add(1)
				}
			}
			return map[string]any{"ok": true}, nil
		}

		hB.ctrl.MonitorOnce(context.Background())
		hB.ctrl.wg.Wait()

		if !switchedDuringRefresh.Load() {
			t.Fatal("expected SelectInstance(ep-2) hook during active refreshRemote to fire")
		}
		if got := hB.ctrl.activeEndpoint(); got != "ep-2" {
			t.Fatalf("expected active endpoint ep-2, got %q", got)
		}
		if got := activeCfgCallsB.Load(); got != 0 {
			t.Fatalf("expected active configureRemote skipped after SelectInstance during refresh, got %d calls", got)
		}
		if got := ep2HBCallsB.Load(); got != 0 {
			t.Fatalf("expected secondary loop NOT to heartbeat newly active ep-2, got %d calls", got)
		}
		if got := ep2CfgCallsB.Load(); got != 0 {
			t.Fatalf("expected secondary loop NOT to configure newly active ep-2, got %d calls", got)
		}

		// Part C: Start(createNew=true) completing between MonitorOnce's prune vaultSessions() read
		// and active lookup must force a fresh sessions read (activeOpGen != sessionsOpGen) instead
		// of reusing the stale pre-Start sessions map.
		cfgDirC := t.TempDir()
		memVaultC := NewColabOneMemoryVault()
		hvC := newHookVault(memVaultC)
		provC := newMockProvider(memVaultC)
		provC.addManagedSession("ep-1", "T4", "STANDARD", true)

		ctrlC := NewColabOneController(ColabOneControllerConfig{
			Root:           cfgDirC,
			ConfigDir:      cfgDirC,
			Background:     false,
			Vault:          hvC,
			Auth:           newMockAuth(),
			Provider:       provC,
			ServiceBundler: defaultMockBundler(),
		})
		defer ctrlC.Close()

		ctrlC.mu.Lock()
		ctrlC.lastAuth = now
		ctrlC.lastHeartbeat = now
		ctrlC.lastConfig = now
		ctrlC.data["runtime"] = map[string]any{"state": "ready", "id": "ep-1", "protocol": 7, "service_revision": expectedRev}
		ctrlC.instanceStates["ep-1"] = &instanceCache{
			Runtime:       map[string]any{"state": "ready", "id": "ep-1", "protocol": 7, "service_revision": expectedRev},
			LastHeartbeat: 0,
			LastConfig:    now,
		}
		ctrlC.mu.Unlock()

		provC.startFn = func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
			if notify != nil {
				notify("部署远端管理服务")
			}
			provC.addManagedSession("ep-2", gpu, "STANDARD", true)
			return map[string]any{"endpoint": "ep-2"}, nil
		}

		var startFinished atomic.Bool
		var ep1SecHB, ep2SecHB atomic.Int32
		var ep1SecSnap, ep2SecSnap, activeSnapAfterStart atomic.Int32
		provC.heartbeatFn = func(ctx context.Context, endpoint string) error {
			if startFinished.Load() {
				switch endpoint {
				case "ep-1":
					ep1SecHB.Add(1)
				case "ep-2":
					ep2SecHB.Add(1)
				}
			}
			return nil
		}
		provC.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "snapshot" {
				if startFinished.Load() {
					switch opts.Endpoint {
					case "":
						activeSnapAfterStart.Add(1)
					case "ep-1":
						ep1SecSnap.Add(1)
					case "ep-2":
						ep2SecSnap.Add(1)
					}
				}
				ep := opts.Endpoint
				if ep == "" {
					ep = ctrlC.activeEndpoint()
				}
				return map[string]any{
					"runtime": map[string]any{
						"state":            "ready",
						"id":               ep,
						"protocol":         7,
						"service_revision": expectedRev,
						"tasks":            []any{},
					},
					"models": []any{},
				}, nil
			}
			return map[string]any{"ok": true}, nil
		}

		var sessionReads atomic.Int32
		hvC.mu.Lock()
		hvC.getFn = func(key string) (any, bool, bool) {
			if key == "runtime_session_ids" && sessionReads.Add(1) == 2 {
				// Capture the pre-Start session list for the prune read (L832), then run
				// Start(createNew=true) to completion before MonitorOnce reaches L871.
				preVal, preOK := memVaultC.Get("runtime_session_ids")
				hvC.mu.Lock()
				hvC.getFn = nil
				hvC.mu.Unlock()
				if _, err := ctrlC.Command(context.Background(), "runtime.start", map[string]any{
					"new": true,
					"gpu": "A100",
				}); err != nil {
					t.Errorf("runtime.start(new=true) failed: %v", err)
				}
				waitIdle(t, ctrlC)
				// Ensure post-Start heartbeat/config don't mask secondary checks.
				ctrlC.mu.Lock()
				ctrlC.lastHeartbeat = now
				ctrlC.lastConfig = now
				if st := ctrlC.instanceStates["ep-1"]; st != nil {
					st.LastHeartbeat = 0
					st.LastConfig = now
				}
				ctrlC.mu.Unlock()
				startFinished.Store(true)
				return preVal, preOK, true
			}
			return nil, false, false
		}
		hvC.mu.Unlock()

		ctrlC.MonitorOnce(context.Background())
		ctrlC.wg.Wait()

		if !startFinished.Load() {
			t.Fatal("expected Start(createNew=true) hook on 2nd runtime_session_ids read to fire")
		}
		if got := activeSnapAfterStart.Load(); got != 1 {
			t.Fatalf("expected 1 active snapshot for newly started ep-2, got %d", got)
		}
		if got := ep2SecHB.Load(); got != 0 || ep2SecSnap.Load() != 0 {
			t.Fatalf("MonitorOnce reused stale sessionsFresh and treated newly active ep-2 as secondary: hb=%d snap=%d",
				got, ep2SecSnap.Load())
		}
		if got := ep1SecHB.Load(); got != 1 || ep1SecSnap.Load() != 1 {
			t.Fatalf("expected MonitorOnce to treat demoted ep-1 as secondary (hb=1, snap=1), got hb=%d snap=%d",
				got, ep1SecSnap.Load())
		}
	})

	t.Run("secondary_loop_aborts_immediately_on_concurrent_select_instance_to_later_endpoint", func(t *testing.T) {
		now := nowUnixFloat()
		for _, tc := range []struct {
			name         string
			triggerAt    string // "heartbeat_err" or "configure_ok"
			ep2LastHB    float64
			ep2LastCfg   float64
			wantEP2State string
		}{
			{
				name:         "select_during_ep2_heartbeat_error",
				triggerAt:    "heartbeat_err",
				ep2LastHB:    0,
				ep2LastCfg:   now,
				wantEP2State: "ready",
			},
			{
				name:         "select_during_ep2_configure_success",
				triggerAt:    "configure_ok",
				ep2LastHB:    now,
				ep2LastCfg:   0,
				wantEP2State: "ready",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h := newTestHarness(t, false)
				h.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
				h.provider.addManagedSession("ep-2", "L4", "STANDARD", false)
				h.provider.addManagedSession("ep-3", "A100", "STANDARD", false)
				expectedRev := h.ctrl.ExpectedServiceRevision()

				h.ctrl.mu.Lock()
				h.ctrl.lastAuth = now
				h.ctrl.lastHeartbeat = now
				h.ctrl.lastConfig = now
				h.ctrl.data["runtime"] = map[string]any{"state": "ready", "id": "ep-1", "protocol": 7, "service_revision": expectedRev}
				h.ctrl.instanceStates["ep-2"] = &instanceCache{
					Runtime:       map[string]any{"state": "ready", "id": "ep-2", "protocol": 7, "service_revision": expectedRev, "tag": "healthy-ep-2"},
					LastHeartbeat: tc.ep2LastHB,
					LastConfig:    tc.ep2LastCfg,
				}
				h.ctrl.instanceStates["ep-3"] = &instanceCache{
					Runtime:       map[string]any{"state": "ready", "id": "ep-3", "protocol": 7, "service_revision": expectedRev, "tag": "healthy-ep-3"},
					LastHeartbeat: 0,
					LastConfig:    0,
				}
				h.ctrl.mu.Unlock()

				var switched atomic.Bool
				var ep3HBCalls atomic.Int32
				var ep3CfgCalls atomic.Int32
				h.provider.heartbeatFn = func(ctx context.Context, endpoint string) error {
					if endpoint == "ep-2" && tc.triggerAt == "heartbeat_err" && switched.CompareAndSwap(false, true) {
						if _, err := h.ctrl.SelectInstance(ctx, "ep-3"); err != nil {
							t.Errorf("SelectInstance(ep-3) failed: %v", err)
						}
						return errors.New("ep-2 transient heartbeat failure")
					}
					if endpoint == "ep-3" {
						ep3HBCalls.Add(1)
					}
					return nil
				}
				h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
					if path == "configure" && opts.Endpoint == "ep-2" && tc.triggerAt == "configure_ok" && switched.CompareAndSwap(false, true) {
						if _, err := h.ctrl.SelectInstance(ctx, "ep-3"); err != nil {
							t.Errorf("SelectInstance(ep-3) failed: %v", err)
						}
						return map[string]any{"ok": true}, nil
					}
					if path == "configure" && opts.Endpoint == "ep-3" {
						ep3CfgCalls.Add(1)
					}
					if path == "snapshot" {
						ep := opts.Endpoint
						if ep == "" {
							ep = h.ctrl.activeEndpoint()
						}
						return map[string]any{
							"runtime": map[string]any{
								"state":            "ready",
								"id":               ep,
								"protocol":         7,
								"service_revision": expectedRev,
								"tag":              "healthy-" + ep,
								"tasks":            []any{},
							},
							"models": []any{},
						}, nil
					}
					return map[string]any{"ok": true}, nil
				}

				h.ctrl.MonitorOnce(context.Background())
				h.ctrl.wg.Wait()

				if !switched.Load() {
					t.Fatalf("expected SelectInstance(ep-3) hook (%s) to fire", tc.triggerAt)
				}
				if got := h.ctrl.activeEndpoint(); got != "ep-3" {
					t.Fatalf("expected active endpoint ep-3, got %q", got)
				}
				if got := ep3HBCalls.Load(); got != 0 {
					t.Fatalf("secondary loop did not abort and heartbeated newly active ep-3: got %d calls", got)
				}
				if got := ep3CfgCalls.Load(); got != 0 {
					t.Fatalf("secondary loop did not abort and configured newly active ep-3: got %d calls", got)
				}
				h.ctrl.mu.Lock()
				ep2State := asString(h.ctrl.instanceStates["ep-2"].Runtime["state"])
				ep2Tag := asString(h.ctrl.instanceStates["ep-2"].Runtime["tag"])
				h.ctrl.mu.Unlock()
				if ep2State != tc.wantEP2State || ep2Tag != "healthy-ep-2" {
					t.Fatalf("ep-2 cache was corrupted after opGen changed: state=%q tag=%q", ep2State, ep2Tag)
				}
			})
		}
	})

	t.Run("save_settings_skips_sync_log_when_configure_remote_bails_out_on_concurrent_opBusy", func(t *testing.T) {
		// Part A: Direct configureRemote(ctx, ep, true) returns (false, nil) on opBusy bail-out
		// and (true, nil) only after POST configure actually succeeds.
		hDirect := newTestHarness(t, false)
		hDirect.provider.addManagedSession("ep-1", "T4", "STANDARD", true)
		var directCfgCalls atomic.Int32
		hDirect.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "configure" {
				directCfgCalls.Add(1)
			}
			return map[string]any{"ok": true}, nil
		}

		hDirect.ctrl.opBusy.Store(true)
		pushed, err := hDirect.ctrl.configureRemote(context.Background(), "ep-1", true)
		hDirect.ctrl.opBusy.Store(false)
		if err != nil || pushed {
			t.Fatalf("expected (false, nil) when background configureRemote bails out on opBusy, got pushed=%v err=%v", pushed, err)
		}
		if got := directCfgCalls.Load(); got != 0 {
			t.Fatalf("expected 0 POST configure calls during opBusy bail-out, got %d", got)
		}

		pushed, err = hDirect.ctrl.configureRemote(context.Background(), "ep-1", true)
		if err != nil || !pushed {
			t.Fatalf("expected (true, nil) after POST configure succeeded, got pushed=%v err=%v", pushed, err)
		}
		if got := directCfgCalls.Load(); got != 1 {
			t.Fatalf("expected 1 POST configure call when idle, got %d", got)
		}

		// Part B: SaveSettings live idle_minutes push hitting concurrent SelectInstance("ep-2")
		// right before configureRemote(..., true) must bail out cleanly, emit 0 POST configure calls,
		// emit NO "已向实例 ... 同步空闲释放时长" log, and SelectInstance must bump opGen on both entry and exit.
		cfgDir := t.TempDir()
		memVault := NewColabOneMemoryVault()
		hv := newHookVault(memVault)
		prov := newMockProvider(memVault)
		prov.addManagedSession("ep-1", "T4", "STANDARD", true)
		prov.addManagedSession("ep-2", "A100", "STANDARD", false)

		ctrl := NewColabOneController(ColabOneControllerConfig{
			Root:           cfgDir,
			ConfigDir:      cfgDir,
			Background:     false,
			Vault:          hv,
			Auth:           newMockAuth(),
			Provider:       prov,
			ServiceBundler: defaultMockBundler(),
		})
		defer ctrl.Close()

		var cfgCalls atomic.Int32
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "configure" {
				cfgCalls.Add(1)
			}
			if path == "snapshot" {
				ep := opts.Endpoint
				if ep == "" {
					ep = ctrl.activeEndpoint()
				}
				return map[string]any{
					"runtime": map[string]any{"state": "ready", "id": ep, "protocol": 7, "tasks": []any{}},
					"models":  []any{},
				}, nil
			}
			return map[string]any{"ok": true}, nil
		}

		setActiveEntered := make(chan struct{})
		setActiveRelease := make(chan struct{})
		selectDone := make(chan struct{})
		var releaseSelectOnce sync.Once
		releaseSelect := func() { releaseSelectOnce.Do(func() { close(setActiveRelease) }) }
		defer releaseSelect()

		var genBeforeSelect, genAfterSelect atomic.Uint64
		prov.setActiveFn = func(endpoint string) (string, error) {
			close(setActiveEntered)
			<-setActiveRelease
			_ = memVault.Set("active_endpoint", endpoint)
			return endpoint, nil
		}

		var sessionReads atomic.Int32
		hv.mu.Lock()
		hv.getFn = func(key string) (any, bool, bool) {
			// Call 1 is SaveSettings' synchronous session list; Call 2 is the spawned goroutine's
			// ownership check right before calling configureRemote(c.rootCtx, "ep-1", true).
			if key == "runtime_session_ids" && sessionReads.Add(1) == 2 {
				hv.mu.Lock()
				hv.getFn = nil
				hv.mu.Unlock()
				genBeforeSelect.Store(ctrl.opGen.Load())
				go func() {
					defer close(selectDone)
					if _, err := ctrl.SelectInstance(context.Background(), "ep-2"); err != nil {
						t.Errorf("SelectInstance(ep-2) failed: %v", err)
					}
					genAfterSelect.Store(ctrl.opGen.Load())
				}()
				<-setActiveEntered
			}
			return nil, false, false
		}
		hv.mu.Unlock()

		if _, err := ctrl.Command(context.Background(), "settings.save", map[string]any{"idle_minutes": 45}); err != nil {
			t.Fatalf("settings.save failed: %v", err)
		}
		// Wait until SelectInstance holds opBusy inside SetActive, then wait for SaveSettings'
		// background push goroutine to finish bailing out on ep-1 and skipping ep-2.
		<-setActiveEntered
		ctrl.wg.Wait()

		releaseSelect()
		<-selectDone
		ctrl.wg.Wait()

		if got := cfgCalls.Load(); got != 0 {
			t.Fatalf("expected 0 POST configure calls while SelectInstance held opBusy, got %d", got)
		}
		if after, before := genAfterSelect.Load(), genBeforeSelect.Load(); after < before+2 {
			t.Fatalf("expected SelectInstance to bump opGen on both SetActive and exit (before=%d after=%d)", before, after)
		}
		for _, entry := range ctrl.activity.Snapshot() {
			if strings.Contains(entry.Message, "同步空闲释放时长") {
				t.Fatalf("SaveSettings emitted false idle_minutes sync log after configureRemote bailed out: %+v", entry)
			}
		}

		// Part C: Even when concurrent opBusy clears WITHOUT bumping opGen (e.g. a brief failed
		// precheck), configureRemote returning pushed=false must still suppress the false sync log.
		cfgDirC := t.TempDir()
		memVaultC := NewColabOneMemoryVault()
		hvC := newHookVault(memVaultC)
		provC := newMockProvider(memVaultC)
		provC.addManagedSession("ep-only", "T4", "STANDARD", true)

		ctrlC := NewColabOneController(ColabOneControllerConfig{
			Root:           cfgDirC,
			ConfigDir:      cfgDirC,
			Background:     false,
			Vault:          hvC,
			Auth:           newMockAuth(),
			Provider:       provC,
			ServiceBundler: defaultMockBundler(),
		})
		defer ctrlC.Close()

		var cfgCallsC atomic.Int32
		provC.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "configure" {
				cfgCallsC.Add(1)
			}
			return map[string]any{"ok": true}, nil
		}

		var readsC atomic.Int32
		hvC.mu.Lock()
		hvC.getFn = func(key string) (any, bool, bool) {
			if key == "runtime_session_ids" && readsC.Add(1) == 2 {
				hvC.mu.Lock()
				hvC.getFn = nil
				hvC.mu.Unlock()
				// Simulate opBusy held right as configureRemote enters, with zero opGen change.
				ctrlC.opBusy.Store(true)
			}
			return nil, false, false
		}
		hvC.mu.Unlock()

		if _, err := ctrlC.Command(context.Background(), "settings.save", map[string]any{"idle_minutes": 50}); err != nil {
			t.Fatalf("settings.save (Part C) failed: %v", err)
		}
		ctrlC.wg.Wait()
		ctrlC.opBusy.Store(false)

		if got := cfgCallsC.Load(); got != 0 {
			t.Fatalf("expected 0 POST configure calls in Part C, got %d", got)
		}
		for _, entry := range ctrlC.activity.Snapshot() {
			if strings.Contains(entry.Message, "同步空闲释放时长") {
				t.Fatalf("SaveSettings emitted false idle_minutes sync log when pushed=false without opGen bump: %+v", entry)
			}
		}
	})
}

type realComfyTestAdapter struct{}

var (
	_ ComfyValidator            = realComfyTestAdapter{}
	_ ComfyCatalogValidator     = realComfyTestAdapter{}
	_ ComfyInputUploadValidator = realComfyTestAdapter{}
)

func comfyStructToMap(v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := decodeJSONWithNumbers(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (realComfyTestAdapter) ValidateCatalogBundle(raw map[string]any) (ComfyCatalogBundle, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return ComfyCatalogBundle{}, err
	}
	var cat comfyui.ColabOneComfyCatalog
	if err := json.Unmarshal(b, &cat); err != nil {
		return ComfyCatalogBundle{}, err
	}
	if err := cat.Validate(); err != nil {
		return ComfyCatalogBundle{}, err
	}
	templates := make([]map[string]any, 0, len(cat.Templates))
	for _, tpl := range cat.Templates {
		m, err := comfyStructToMap(tpl)
		if err != nil {
			return ComfyCatalogBundle{}, err
		}
		templates = append(templates, m)
	}
	engines := make([]map[string]any, 0, len(cat.TtsEngines))
	for _, eng := range cat.TtsEngines {
		m, err := comfyStructToMap(eng)
		if err != nil {
			return ComfyCatalogBundle{}, err
		}
		engines = append(engines, m)
	}
	return ComfyCatalogBundle{Templates: templates, TtsEngines: engines}, nil
}

func (a realComfyTestAdapter) ValidateCatalog(raw map[string]any) ([]map[string]any, error) {
	bundle, err := a.ValidateCatalogBundle(raw)
	if err != nil {
		return nil, err
	}
	return bundle.Templates, nil
}

func (realComfyTestAdapter) ValidateInputUpload(filename, dataB64 string) error {
	return comfyui.ValidateComfyInputUpload(filename, dataB64)
}

func (realComfyTestAdapter) ValidateWorkflowTemplate(raw map[string]any) (map[string]any, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var tpl comfyui.ColabOneWorkflowTemplate
	if err := json.Unmarshal(b, &tpl); err != nil {
		return nil, err
	}
	if err := tpl.Validate(); err != nil {
		return nil, err
	}
	return comfyStructToMap(tpl)
}

func (realComfyTestAdapter) ValidateWorkflowImport(raw map[string]any) (map[string]any, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var req comfyui.ColabOneWorkflowImportRequest
	if err := json.Unmarshal(b, &req); err != nil {
		return nil, err
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	return comfyStructToMap(req)
}

func (realComfyTestAdapter) ValidateWorkflowRun(raw map[string]any) (map[string]any, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var req comfyui.ColabOneWorkflowRunRequest
	if err := json.Unmarshal(b, &req); err != nil {
		return nil, err
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	return comfyStructToMap(req)
}

func (realComfyTestAdapter) ValidateCanvasConfig(raw map[string]any) (map[string]any, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var cfg comfyui.ColabOneComfyCanvasConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return comfyStructToMap(cfg)
}

func TestComfyUIHardeningUploadRunBuiltinAuthorityAndTTSEngines(t *testing.T) {
	revA := strings.Repeat("a", 40)
	revB := strings.Repeat("b", 40)
	wfGraph := map[string]any{
		"1": map[string]any{
			"class_type": "CLIPTextEncode",
			"inputs":     map[string]any{"text": "studio portrait"},
		},
	}

	makeHardenedBundler := func() *mockBundler {
		b := defaultMockBundler()
		comfyRaw, err := json.Marshal(map[string]any{
			"repo":       "https://github.com/Comfy-Org/ComfyUI.git",
			"tag":        "v0.46.0",
			"commit":     revA,
			"port":       8188,
			"proxy_port": 8189,
			"tts_engines": []map[string]any{
				{
					// Deliberately omit commercial_ok, sample_rate, license, supports_voice_clone
					// to verify ValidateCatalogBundle normalizes defaults.
					"id":       "voxcpm2",
					"name":     "  VoxCPM2  ",
					"repo":     "openbmb/VoxCPM2",
					"revision": revA,
				},
				{
					"id":                   "indextts2",
					"name":                 "IndexTTS-2",
					"repo":                 "IndexTeam/IndexTTS-2",
					"revision":             revB,
					"license":              "IndexTTS Research",
					"commercial_ok":        false,
					"license_notice":       "仅限非商业研究",
					"sample_rate":          24000,
					"supports_emotion_ref": true,
				},
			},
			"templates": []map[string]any{
				{
					"id":          "digital-human-longcat",
					"name":        "Embedded LongCat 1.5",
					"description": "authoritative embedded template",
					"source":      "builtin",
					"workflow":    wfGraph,
				},
			},
		})
		if err != nil {
			t.Fatalf("marshal comfy catalog: %v", err)
		}
		b.comfyJSON = comfyRaw
		return b
	}

	newHardenedController := func(t *testing.T, cfgDir string) (*ColabOneController, *ColabOneMemoryVault, *mockProvider) {
		t.Helper()
		vault := NewColabOneMemoryVault()
		prov := newMockProvider(vault)
		ctrl := NewColabOneController(ColabOneControllerConfig{
			Root:           cfgDir,
			ConfigDir:      cfgDir,
			Background:     false,
			Vault:          vault,
			Auth:           newMockAuth(),
			Provider:       prov,
			Comfy:          realComfyTestAdapter{},
			PublicAPI:      mockPublicAPI{},
			ServiceBundler: makeHardenedBundler(),
		})
		t.Cleanup(func() { ctrl.Close() })
		return ctrl, vault, prov
	}

	t.Run("comfyui_input_upload_rejects_invalid_extension_and_empty_locally_before_remote", func(t *testing.T) {
		ctrl, _, prov := newHardenedController(t, t.TempDir())
		prov.addManagedSession("ep-up", "A100", "STANDARD", true)
		ctrl.Update(map[string]any{"runtime": map[string]any{"state": "ready", "id": "ep-up"}})

		validB64 := base64.StdEncoding.EncodeToString([]byte("valid-media-bytes"))

		for _, tc := range []struct {
			name     string
			filename string
			data     string
			wantSub  string
		}{
			{"forbiddenAAC", "voice.aac", validB64, "不支持的输入文件格式"},
			{"forbiddenEXE", "evil.exe", validB64, "不支持的输入文件格式"},
			{"zeroByteEmpty", "portrait.png", "", "输入文件内容为空"},
			{"zeroByteDataURI", "voice.wav", "data:audio/wav;base64,", "输入文件内容为空"},
			{"pathTraversal", "../portrait.png", validB64, "输入文件名不合法"},
		} {
			if _, err := ctrl.Command(context.Background(), "comfyui.input.upload", map[string]any{
				"filename": tc.filename,
				"data":     tc.data,
			}); err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("%s: err = %v, want substring %q", tc.name, err, tc.wantSub)
			}
		}

		prov.mu.Lock()
		rpcCountBefore := len(prov.rpcCalls)
		prov.mu.Unlock()
		if rpcCountBefore != 0 {
			t.Fatalf("expected 0 remote RPC calls for locally rejected uploads, got %d", rpcCountBefore)
		}

		// Valid PNG upload passes local validation and reaches remote POST comfyui/inputs.
		if _, err := ctrl.Command(context.Background(), "comfyui.input.upload", map[string]any{
			"filename": "portrait.png",
			"data":     validB64,
		}); err != nil {
			t.Fatalf("valid PNG upload failed: %v", err)
		}
		prov.mu.Lock()
		defer prov.mu.Unlock()
		if len(prov.rpcCalls) != 1 || prov.rpcCalls[0].Method != "POST" || prov.rpcCalls[0].Path != "comfyui/inputs" {
			t.Fatalf("expected single POST comfyui/inputs RPC, got %#v", prov.rpcCalls)
		}
	})

	t.Run("comfyui_run_hf_token_injection_override_limit_and_refresh_failure_tolerance", func(t *testing.T) {
		ctrl, vault, prov := newHardenedController(t, t.TempDir())
		prov.addManagedSession("ep-run", "A100", "STANDARD", true)
		runtimeCaps := []string{"public_api", "comfyui", "comfyui_hf_token"}
		ctrl.Update(map[string]any{
			"runtime": map[string]any{
				"state":        "ready",
				"id":           "ep-run",
				"capabilities": runtimeCaps,
			},
		})

		var runBodies []map[string]any
		failSnapshot := false
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "comfyui/run" {
				runBodies = append(runBodies, deepCopyMap(asMap(body)))
				return map[string]any{"id": "task-accepted-1", "state": "running"}, nil
			}
			if path == "snapshot" {
				if failSnapshot {
					return nil, errors.New("transient snapshot timeout")
				}
				rt := map[string]any{"state": "ready", "id": "ep-run", "protocol": 7, "tasks": []any{}}
				if runtimeCaps != nil {
					rt["capabilities"] = runtimeCaps
				}
				return map[string]any{
					"runtime":       rt,
					"models":        []any{},
					"workflows":     []any{},
					"comfy_outputs": []any{},
				}, nil
			}
			return map[string]any{"ok": true}, nil
		}

		// 1. Auto-inject vault hf_token when omitted or blank.
		if err := vault.Set("hf", "  hf_vault_auto_secret_123  "); err != nil {
			t.Fatal(err)
		}
		if _, err := ctrl.Command(context.Background(), "comfyui.run", map[string]any{
			"workflow_id": "digital-human-longcat",
			"overrides":   map[string]any{},
			"inputs":      map[string]any{},
		}); err != nil {
			t.Fatalf("comfyui.run (vault auto-inject) failed: %v", err)
		}
		if _, err := ctrl.Command(context.Background(), "comfyui.run", map[string]any{
			"workflow_id": "digital-human-longcat",
			"hf_token":    "   ",
			"overrides":   map[string]any{},
			"inputs":      map[string]any{},
		}); err != nil {
			t.Fatalf("comfyui.run (whitespace hf_token auto-inject) failed: %v", err)
		}
		if len(runBodies) != 2 ||
			asString(runBodies[0]["hf_token"]) != "hf_vault_auto_secret_123" ||
			asString(runBodies[1]["hf_token"]) != "hf_vault_auto_secret_123" {
			t.Fatalf("expected vault hf_token auto-injected, got %#v", runBodies)
		}

		// 2. Respect explicit non-empty hf_token instead of overwriting with vault token.
		if _, err := ctrl.Command(context.Background(), "comfyui.run", map[string]any{
			"workflow_id": "digital-human-longcat",
			"hf_token":    "hf_explicit_caller_token_999",
			"overrides":   map[string]any{},
			"inputs":      map[string]any{},
		}); err != nil {
			t.Fatalf("comfyui.run (explicit hf_token) failed: %v", err)
		}
		if len(runBodies) != 3 || asString(runBodies[2]["hf_token"]) != "hf_explicit_caller_token_999" {
			t.Fatalf("expected explicit hf_token preserved, got %#v", runBodies)
		}

		// 3. Reject >512-rune hf_token (both explicit and from vault) BEFORE making remote RPCs.
		prov.mu.Lock()
		callsBeforeOverlong := len(prov.rpcCalls)
		prov.mu.Unlock()

		if _, err := ctrl.Command(context.Background(), "comfyui.run", map[string]any{
			"workflow_id": "digital-human-longcat",
			"hf_token":    strings.Repeat("x", 513),
		}); err == nil || !strings.Contains(err.Error(), "hf_token 长度不能超过 512") {
			t.Fatalf("expected >512-rune explicit hf_token rejected, got %v", err)
		}

		if err := vault.Set("hf", strings.Repeat("v", 513)); err != nil {
			t.Fatal(err)
		}
		if _, err := ctrl.Command(context.Background(), "comfyui.run", map[string]any{
			"workflow_id": "digital-human-longcat",
		}); err == nil || !strings.Contains(err.Error(), "hf_token 长度不能超过 512") {
			t.Fatalf("expected >512-rune vault hf_token rejected, got %v", err)
		}

		prov.mu.Lock()
		callsAfterOverlong := len(prov.rpcCalls)
		prov.mu.Unlock()
		if callsAfterOverlong != callsBeforeOverlong {
			t.Fatalf("expected 0 remote calls for >512-rune hf_token, got %d extra calls", callsAfterOverlong-callsBeforeOverlong)
		}

		// 4. Tolerate transient RefreshRemote failure after remote POST comfyui/run succeeded.
		if err := vault.Set("hf", "hf_valid_token_after"); err != nil {
			t.Fatal(err)
		}
		failSnapshot = true
		res, err := ctrl.Command(context.Background(), "comfyui.run", map[string]any{
			"workflow_id": "digital-human-longcat",
			"overrides":   map[string]any{},
			"inputs":      map[string]any{},
		})
		if err != nil {
			t.Fatalf("comfyui.run should tolerate transient RefreshRemote failure, got err=%v", err)
		}
		if asString(asMap(res)["id"]) != "task-accepted-1" {
			t.Fatalf("expected accepted remote task response, got %#v", res)
		}
		foundWarn := false
		for _, entry := range ctrl.activity.Snapshot() {
			if entry.Level == "warning" && strings.Contains(entry.Message, "远端已受理 ComfyUI 工作流任务，但刷新状态失败") {
				foundWarn = true
				break
			}
		}
		if !foundWarn {
			t.Fatalf("expected warning activity log for transient RefreshRemote failure after comfyui.run")
		}

		// 5. On a pre-upgrade v7 remote WITHOUT "comfyui_hf_token" in capabilities, vault hf_token
		// MUST NOT be auto-injected (preventing HTTP 422 on older remote servers with extra="forbid").
		failSnapshot = false
		for _, tc := range []struct {
			name string
			caps []string
		}{
			{"legacy_caps_without_hf_token", []string{"public_api", "comfyui"}},
			{"omitted_capabilities", nil},
		} {
			runtimeCaps = tc.caps
			rtUpdate := map[string]any{"state": "ready", "id": "ep-run", "protocol": 7}
			if tc.caps != nil {
				rtUpdate["capabilities"] = tc.caps
			} else {
				rtUpdate["capabilities"] = nil
			}
			ctrl.Update(map[string]any{"runtime": rtUpdate})

			beforeCount := len(runBodies)
			if _, err := ctrl.Command(context.Background(), "comfyui.run", map[string]any{
				"workflow_id": "digital-human-longcat",
				"overrides":   map[string]any{},
				"inputs":      map[string]any{},
			}); err != nil {
				t.Fatalf("%s: comfyui.run failed: %v", tc.name, err)
			}
			if _, err := ctrl.Command(context.Background(), "comfyui.run", map[string]any{
				"workflow_id": "digital-human-longcat",
				"hf_token":    "   ",
				"overrides":   map[string]any{},
				"inputs":      map[string]any{},
			}); err != nil {
				t.Fatalf("%s: comfyui.run (blank hf_token) failed: %v", tc.name, err)
			}
			if len(runBodies) != beforeCount+2 {
				t.Fatalf("%s: expected 2 run bodies, got %d", tc.name, len(runBodies)-beforeCount)
			}
			for i := beforeCount; i < len(runBodies); i++ {
				if tok, exists := runBodies[i]["hf_token"]; exists && asString(tok) != "" {
					t.Fatalf("%s: pre-upgrade remote must not receive auto-injected hf_token, got %#v", tc.name, runBodies[i])
				}
			}
		}
	})

	t.Run("save_settings_rejects_control_and_whitespace_hf_token", func(t *testing.T) {
		ctrl, vault, _ := newHardenedController(t, t.TempDir())
		if err := vault.Set("hf", "hf_existing_safe_token"); err != nil {
			t.Fatal(err)
		}

		for _, tc := range []struct {
			name  string
			token string
		}{
			{"cr", "hf_bad\rtoken"},
			{"lf", "hf_bad\ntoken"},
			{"crlf", "hf_bad\r\ntoken"},
			{"tab", "hf_bad\ttoken"},
			{"embedded_space", "hf_bad token"},
			{"unicode_space", "hf_bad\u3000token"},
			{"nul_control", "hf_bad\x00token"},
			{"del_control", "hf_bad\x7ftoken"},
		} {
			if _, err := ctrl.SaveSettings(context.Background(), map[string]any{
				"hf_token": tc.token,
			}); err == nil || !strings.Contains(err.Error(), "hf_token 包含非法空白或控制字符") {
				t.Fatalf("SaveSettings(%s): err = %v, want invalid whitespace/control error", tc.name, err)
			}
			gotVault, _ := vault.Get("hf")
			if asString(gotVault) != "hf_existing_safe_token" {
				t.Fatalf("SaveSettings(%s) mutated vault hf token to %q", tc.name, asString(gotVault))
			}
		}

		// Valid ASCII-padded hf_token is trimmed and persisted cleanly.
		if _, err := ctrl.SaveSettings(context.Background(), map[string]any{
			"hf_token": "  hf_clean_saved_token_123  ",
		}); err != nil {
			t.Fatalf("SaveSettings with clean padded hf_token failed: %v", err)
		}
		gotVault, _ := vault.Get("hf")
		if asString(gotVault) != "hf_clean_saved_token_123" {
			t.Fatalf("vault hf token = %q, want %q", asString(gotVault), "hf_clean_saved_token_123")
		}
	})

	t.Run("builtin_template_authority_over_stale_remote_and_local_workflows_json_and_offline_rejections", func(t *testing.T) {
		cfgDir := t.TempDir()
		// Pre-seed workflows.json with a poisoned entry carrying the built-in ID + a valid custom entry.
		poisonedLocal, err := json.Marshal([]map[string]any{
			{
				"id":          "digital-human-longcat",
				"name":        "Poisoned Local Shadow",
				"description": "must never shadow embedded builtin",
				"source":      "custom",
				"updated_at":  9999999999.0,
				"workflow":    wfGraph,
			},
			{
				"id":          "wf-user-custom",
				"name":        "User Custom Flow",
				"description": "kept",
				"source":      "custom",
				"updated_at":  500.0,
				"workflow":    wfGraph,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfgDir, "workflows.json"), poisonedLocal, 0o600); err != nil {
			t.Fatal(err)
		}

		ctrl, _, prov := newHardenedController(t, cfgDir)

		findWF := func(list []map[string]any, id string) map[string]any {
			for _, w := range list {
				if asString(w["id"]) == id {
					return w
				}
			}
			return nil
		}

		// 1. Local workflows.json entry with built-in ID MUST NOT shadow embedded built-in.
		offlineWFs := asMapSlice(ctrl.Snapshot()["workflows"])
		longcatOffline := findWF(offlineWFs, "digital-human-longcat")
		if longcatOffline == nil || asString(longcatOffline["name"]) != "Embedded LongCat 1.5" || asString(longcatOffline["source"]) != "builtin" {
			t.Fatalf("local workflows.json shadowed embedded builtin: %#v", longcatOffline)
		}
		if findWF(offlineWFs, "wf-user-custom") == nil {
			t.Fatalf("legitimate custom workflow missing from offline snapshot: %#v", offlineWFs)
		}
		ctrl.mu.Lock()
		_, hasPoisonedCustom := ctrl.customWorkflows["digital-human-longcat"]
		ctrl.mu.Unlock()
		if hasPoisonedCustom {
			t.Fatal("loadWorkflows must ignore local workflows.json entries colliding with built-in IDs")
		}

		// 2. Offline comfyui.workflow.import and comfyui.workflow.delete reject built-in IDs.
		if _, err := ctrl.Command(context.Background(), "comfyui.workflow.import", map[string]any{
			"id":       "digital-human-longcat",
			"name":     "Attempted Hijack",
			"workflow": wfGraph,
		}); err == nil || !strings.Contains(err.Error(), "不能覆盖内置工作流模板") {
			t.Fatalf("offline import with built-in ID: err = %v", err)
		}
		if _, err := ctrl.Command(context.Background(), "comfyui.workflow.delete", map[string]any{
			"id": "digital-human-longcat",
		}); err == nil || !strings.Contains(err.Error(), "内置工作流模板不可删除") {
			t.Fatalf("offline delete with built-in ID: err = %v", err)
		}
		prov.mu.Lock()
		offlineRPCs := len(prov.rpcCalls)
		prov.mu.Unlock()
		if offlineRPCs != 0 {
			t.Fatalf("expected 0 RPC calls for rejected offline built-in import/delete, got %d", offlineRPCs)
		}

		// 3. Stale remote built-in and retired remote built-in MUST NOT shadow or pollute embedded catalog.
		prov.addManagedSession("ep-auth", "A100", "STANDARD", true)
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "snapshot" {
				return map[string]any{
					"runtime": map[string]any{"state": "ready", "id": "ep-auth", "protocol": 7, "tasks": []any{}},
					"models":  []any{},
					"workflows": []map[string]any{
						{
							"id":          "digital-human-longcat",
							"name":        "Stale Remote LongCat",
							"description": "outdated remote copy",
							"source":      "builtin",
							"updated_at":  9999999999.0,
							"workflow":    wfGraph,
						},
						{
							"id":       "retired-remote-builtin",
							"name":     "Retired Builtin",
							"source":   "builtin",
							"workflow": wfGraph,
						},
						{
							"id":         "wf-remote-custom",
							"name":       "Remote Custom Flow",
							"source":     "custom",
							"updated_at": 600.0,
							"workflow":   wfGraph,
						},
					},
					"comfy_outputs": []any{},
				}, nil
			}
			return map[string]any{"ok": true}, nil
		}
		if err := ctrl.RefreshRemote(context.Background(), ""); err != nil {
			t.Fatalf("RefreshRemote failed: %v", err)
		}

		onlineWFs := asMapSlice(ctrl.Snapshot()["workflows"])
		longcatOnline := findWF(onlineWFs, "digital-human-longcat")
		if longcatOnline == nil || asString(longcatOnline["name"]) != "Embedded LongCat 1.5" || asString(longcatOnline["description"]) != "authoritative embedded template" {
			t.Fatalf("stale remote built-in shadowed embedded built-in: %#v", longcatOnline)
		}
		if findWF(onlineWFs, "retired-remote-builtin") != nil {
			t.Fatalf("retired remote built-in should not appear in snapshot: %#v", onlineWFs)
		}
		if findWF(onlineWFs, "wf-remote-custom") == nil || findWF(onlineWFs, "wf-user-custom") == nil {
			t.Fatalf("expected custom workflows preserved alongside authoritative built-in: %#v", onlineWFs)
		}
	})

	t.Run("snapshot_includes_normalized_tts_engines_offline_and_online", func(t *testing.T) {
		ctrl, _, prov := newHardenedController(t, t.TempDir())

		findEngine := func(list []map[string]any, id string) map[string]any {
			for _, item := range list {
				if asString(item["id"]) == id {
					return item
				}
			}
			return nil
		}

		// 1. Offline Snapshot() includes normalized embedded tts_engines (defaults applied by ValidateCatalogBundle).
		offlineEngines := asMapSlice(ctrl.Snapshot()["tts_engines"])
		if len(offlineEngines) != 2 {
			t.Fatalf("expected 2 offline tts_engines, got %#v", offlineEngines)
		}
		voxOffline := findEngine(offlineEngines, "voxcpm2")
		if voxOffline == nil ||
			asString(voxOffline["name"]) != "VoxCPM2" ||
			!asBool(voxOffline["commercial_ok"]) ||
			asString(voxOffline["license"]) != "Apache-2.0" ||
			!asBool(voxOffline["supports_voice_clone"]) {
			t.Fatalf("offline voxcpm2 not normalized: %#v", voxOffline)
		}
		if sr, ok := asInt(voxOffline["sample_rate"]); !ok || sr != 48000 {
			t.Fatalf("offline voxcpm2 sample_rate = %#v, want 48000", voxOffline["sample_rate"])
		}
		idxOffline := findEngine(offlineEngines, "indextts2")
		if idxOffline == nil || asBool(idxOffline["commercial_ok"]) || !asBool(idxOffline["supports_emotion_ref"]) {
			t.Fatalf("offline indextts2 metadata mismatch: %#v", idxOffline)
		}

		// 2. Online Snapshot() reflects remote tts_engines when remote reports them.
		prov.addManagedSession("ep-tts", "A100", "STANDARD", true)
		var remoteEnginesPayload []map[string]any
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "snapshot" {
				resp := map[string]any{
					"runtime":       map[string]any{"state": "ready", "id": "ep-tts", "protocol": 7, "tasks": []any{}},
					"models":        []any{},
					"workflows":     []any{},
					"comfy_outputs": []any{},
				}
				if remoteEnginesPayload != nil {
					resp["tts_engines"] = remoteEnginesPayload
				}
				return resp, nil
			}
			return map[string]any{"ok": true}, nil
		}

		remoteEnginesPayload = []map[string]any{
			{
				"id":                   "voxcpm2",
				"name":                 "VoxCPM2 Remote",
				"commercial_ok":        true,
				"sample_rate":          48000,
				"supports_voice_clone": true,
			},
			{
				"id":                   "fish_s2_pro",
				"name":                 "Fish Audio S2 Pro",
				"commercial_ok":        false,
				"sample_rate":          44100,
				"supports_inline_tags": true,
			},
		}
		if err := ctrl.RefreshRemote(context.Background(), ""); err != nil {
			t.Fatalf("RefreshRemote (with remote tts_engines) failed: %v", err)
		}
		onlineEngines := asMapSlice(ctrl.Snapshot()["tts_engines"])
		voxOnline := findEngine(onlineEngines, "voxcpm2")
		idxOnline := findEngine(onlineEngines, "indextts2")
		fishOnline := findEngine(onlineEngines, "fish_s2_pro")
		if len(onlineEngines) != 3 ||
			voxOnline == nil || asString(voxOnline["name"]) != "VoxCPM2" ||
			idxOnline == nil ||
			fishOnline == nil || asString(fishOnline["name"]) != "Fish Audio S2 Pro" {
			t.Fatalf("online Snapshot() tts_engines built-in authority merge mismatch: %#v", onlineEngines)
		}

		// 3. Online fallback: when remote snapshot omits tts_engines, Snapshot() falls back
		// cleanly to normalized embedded built-in tts_engines.
		remoteEnginesPayload = nil
		if err := ctrl.RefreshRemote(context.Background(), ""); err != nil {
			t.Fatalf("RefreshRemote (omitted remote tts_engines) failed: %v", err)
		}
		fallbackEngines := asMapSlice(ctrl.Snapshot()["tts_engines"])
		if len(fallbackEngines) != 2 || findEngine(fallbackEngines, "voxcpm2") == nil || findEngine(fallbackEngines, "indextts2") == nil {
			t.Fatalf("online fallback tts_engines mismatch: %#v", fallbackEngines)
		}

		// 4. Transitioning back to stopped restores normalized built-in tts_engines.
		ctrl.Update(map[string]any{"runtime": map[string]any{"state": "stopped"}})
		stoppedEngines := asMapSlice(ctrl.Snapshot()["tts_engines"])
		if len(stoppedEngines) != 2 || findEngine(stoppedEngines, "indextts2") == nil {
			t.Fatalf("stopped Snapshot() tts_engines mismatch: %#v", stoppedEngines)
		}
	})
}
