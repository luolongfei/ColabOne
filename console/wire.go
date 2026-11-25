package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/luolongfei/ColabOne/console/internal/activity"
	"github.com/luolongfei/ColabOne/console/internal/assignproxy"
	"github.com/luolongfei/ColabOne/console/internal/auth"
	"github.com/luolongfei/ColabOne/console/internal/colabclient"
	"github.com/luolongfei/ColabOne/console/internal/comfyui"
	"github.com/luolongfei/ColabOne/console/internal/controller"
	"github.com/luolongfei/ColabOne/console/internal/drivemount"
	"github.com/luolongfei/ColabOne/console/internal/hub"
	"github.com/luolongfei/ColabOne/console/internal/jupyterws"
	"github.com/luolongfei/ColabOne/console/internal/location"
	"github.com/luolongfei/ColabOne/console/internal/models"
	"github.com/luolongfei/ColabOne/console/internal/oauthpage"
	"github.com/luolongfei/ColabOne/console/internal/provider"
	"github.com/luolongfei/ColabOne/console/internal/publicapi"
	"github.com/luolongfei/ColabOne/console/internal/servicebundle"
	"github.com/luolongfei/ColabOne/console/internal/shortcuts"
	"github.com/luolongfei/ColabOne/console/internal/vault"
)

// colabOneRootDir resolves the optional repo/app root directory for shortcuts
// and disk-bundle development overrides. Single-binary installs work cleanly
// even when no sibling source directories exist (falling back to embedded assets).
func colabOneRootDir() string {
	exe, _ := os.Executable()
	cwd, _ := os.Getwd()
	candidates := []string{
		strings.TrimSpace(os.Getenv("COLABONE_ROOT")),
		filepath.Dir(exe),
		filepath.Join(filepath.Dir(exe), "../../.."),
		cwd,
		filepath.Dir(cwd),
	}
	for _, cand := range candidates {
		if cand == "" {
			continue
		}
		abs, err := filepath.Abs(cand)
		if err != nil {
			continue
		}
		if servicebundle.HasDiskBundle(abs) {
			return abs
		}
	}
	if exe != "" {
		return filepath.Dir(exe)
	}
	return cwd
}

// runColabOneSelfTest validates the embedded remote service bundle, catalog
// manifests (`presets.json` and `comfyui.json`), and core pure-Go packages
// completely offline without touching the network or OS credential store.
func runColabOneSelfTest() error {
	bundle := servicebundle.NewEmbeddedBundle()
	rev := bundle.ServiceRevision()
	if len(rev) != 16 {
		return fmt.Errorf("embedded service bundle revision invalid: %q", rev)
	}
	zipBytes, fileCount, err := bundle.BuildZip()
	if err != nil || fileCount == 0 || len(zipBytes) == 0 {
		return fmt.Errorf("embedded service bundle zip invalid (files=%d, bytes=%d): %w", fileCount, len(zipBytes), err)
	}
	presetsRaw, err := bundle.ReadCatalogFile("presets.json")
	if err != nil {
		return fmt.Errorf("read embedded presets.json: %w", err)
	}
	var presets []models.ColabOneModelSpec
	if err := json.Unmarshal(presetsRaw, &presets); err != nil {
		return fmt.Errorf("validate presets.json: %w", err)
	}
	if len(presets) == 0 {
		return errors.New("presets.json is empty")
	}
	for i := range presets {
		if err := presets[i].Validate(); err != nil {
			return fmt.Errorf("validate preset %q: %w", presets[i].ID, err)
		}
	}
	comfyRaw, err := bundle.ReadCatalogFile("comfyui.json")
	if err != nil {
		return fmt.Errorf("read embedded comfyui.json: %w", err)
	}
	var catalog comfyui.ColabOneComfyCatalog
	if err := json.Unmarshal(comfyRaw, &catalog); err != nil {
		return fmt.Errorf("parse comfyui.json: %w", err)
	}
	if err := catalog.Validate(); err != nil {
		return fmt.Errorf("validate comfyui.json: %w", err)
	}
	return nil
}

// newColabOneWiredController wires all pure-Go ColabOne packages into a live
// in-process ColabOneController. Pass nil vaultStore to use the default local
// encrypted vault.enc file in configDir (with silent Windows Credential Manager
// migration when running against the default production config directory).
func newColabOneWiredController(root, configDir string, vaultStore *vault.ColabOneVault, background bool) *controller.ColabOneController {
	if root == "" {
		root = colabOneRootDir()
	}
	defaultCfgDir := controller.DefaultConfigDir()
	activityDir := strings.TrimSpace(configDir)
	isDefaultDir := activityDir == "" || filepath.Clean(activityDir) == filepath.Clean(defaultCfgDir)
	if activityDir == "" {
		activityDir = defaultCfgDir
	}
	if vaultStore == nil {
		if isDefaultDir {
			vaultStore = vault.NewFileVaultWithLegacyMigration(activityDir)
		} else {
			vaultStore = vault.NewFileVault(activityDir)
		}
	}

	bundle := servicebundle.ResolveBundle(root)
	vAdapter := &wireVaultAdapter{v: vaultStore}
	actLog := activity.NewColabOneActivityLog(
		activity.DefaultLimit,
		filepath.Join(activityDir, "activity.log"),
		activity.DefaultMaxBytes,
	)

	authSvc := auth.NewColabOneAuth(
		vAdapter,
		auth.WithOAuthPageRenderer(oauthpage.RenderColabOneOAuthPage),
	)
	if initialLocale := colabOneGetLocale(); initialLocale != "" {
		authSvc.SetLocale(initialLocale)
	}

	contentsClient := colabclient.NewColabOneContentsClient(nil)
	provAuth := &wireProviderAuthAdapter{a: authSvc}
	provSvc := provider.NewColabOneProvider(provider.ColabOneProviderConfig{
		Auth:    provAuth,
		Vault:   vAdapter,
		RootDir: root,
		ClientFactory: func(assignProxy string) (provider.ColabClient, error) {
			tokenFn := func(ctx context.Context) (string, error) {
				return provAuth.AccessToken(ctx, "colab")
			}
			trimmed := strings.TrimSpace(assignProxy)
			if trimmed == "" {
				c := colabclient.NewColabOneColabClient(tokenFn)
				return &wireColabClientAdapter{c: c}, nil
			}
			spec, err := assignproxy.ParseAssignProxy(trimmed)
			if err != nil {
				return nil, err
			}
			tr, err := assignproxy.NewAssignOnlyTransport(spec, "https", "colab.research.google.com")
			if err != nil {
				return nil, err
			}
			httpClient := tr.NewClient(assignproxy.AssignProxyConnectTimeout + assignproxy.AssignProxyReadTimeout)
			c := colabclient.NewColabOneColabClient(tokenFn, colabclient.WithHTTPClient(httpClient))
			return &wireColabClientAdapter{c: c, closer: tr}, nil
		},
		Contents: &wireContentsAdapter{c: contentsClient},
		KernelFactory: func(opts provider.KernelOptions) provider.KernelClient {
			kc := jupyterws.NewColabOneKernelClient(jupyterws.ColabOneKernelOptions{
				ServerURL:           opts.ServerURL,
				ProxyToken:          opts.ProxyToken,
				KernelID:            opts.KernelID,
				SessionID:           opts.SessionID,
				ShutdownOnWSTimeout: opts.ShutdownOnWSTimeout,
				OnKernelStarted:     opts.OnKernelStarted,
				OnSessionStarted:    opts.OnSessionStarted,
			})
			return &wireKernelAdapter{kc: kc}
		},
		DriveMounterFactory: func(client provider.ColabClient, endpoint string, opts provider.DriveMountOptions) provider.DriveMounter {
			return newWireDriveMounterAdapter(client, endpoint, opts)
		},
		Bundler:            &wireProviderBundlerAdapter{b: bundle},
		Location:           wireLocationAdapter{},
		SafeMessage:        activity.SafeMessage,
		ParseAssignProxyFn: wireProviderParseAssignProxy,
		FormatProxyErrorFn: assignproxy.FormatProxyError,
	})

	hubClient := hub.NewColabOneHubClient()
	ctrl := controller.NewColabOneController(controller.ColabOneControllerConfig{
		Root:           root,
		ConfigDir:      configDir,
		Background:     background,
		Vault:          vAdapter,
		Auth:           &wireControllerAuthAdapter{a: authSvc},
		Provider:       &wireControllerProviderAdapter{p: provSvc},
		Activity:       &wireActivityAdapter{log: actLog},
		Hub:            &wireHubAdapter{h: hubClient},
		Models:         wireModelsAdapter{},
		Comfy:          wireComfyAdapter{},
		PublicAPI:      wirePublicAPIAdapter{},
		AssignProxy:    wireAssignProxyAdapter{},
		Shortcuts:      wireShortcutsAdapter{},
		ServiceBundler: &wireControllerBundlerAdapter{b: bundle},
		Location:       wireLocationAdapter{},
	})
	return ctrl
}

// ---------------------------------------------------------------------------
// Vault Adapter
// ---------------------------------------------------------------------------

type wireVaultAdapter struct {
	v *vault.ColabOneVault
}

func (a *wireVaultAdapter) Get(key string) (any, bool) {
	return a.v.GetAny(key)
}

func (a *wireVaultAdapter) GetJSON(key string, dest any) (bool, error) {
	return a.v.GetJSON(key, dest)
}

func (a *wireVaultAdapter) Set(key string, value any) error {
	return a.v.Set(key, value)
}

func (a *wireVaultAdapter) Delete(key string) error {
	return a.v.Delete(key)
}

func (a *wireVaultAdapter) Has(key string) bool {
	_, ok := a.v.GetAny(key)
	return ok
}

// ---------------------------------------------------------------------------
// Activity Adapter
// ---------------------------------------------------------------------------

type wireActivityAdapter struct {
	log *activity.ColabOneActivityLog
}

func (a *wireActivityAdapter) Add(message, source, level string) {
	a.log.Add(message, source, level)
}

func (a *wireActivityAdapter) Snapshot() []controller.ColabOneActivityEntry {
	src := a.log.Snapshot()
	out := make([]controller.ColabOneActivityEntry, len(src))
	for i, item := range src {
		out[i] = controller.ColabOneActivityEntry{
			ID:      int64(item.ID),
			Time:    item.Time,
			Source:  item.Source,
			Level:   item.Level,
			Message: item.Message,
		}
	}
	return out
}

func (a *wireActivityAdapter) Clear() {
	a.log.Clear()
}

// ---------------------------------------------------------------------------
// Location Adapter
// ---------------------------------------------------------------------------

type wireLocationAdapter struct{}

func (wireLocationAdapter) LocationFromZone(zone string) map[string]any {
	return location.LocationFromZone(zone).ToMap()
}

func (wireLocationAdapter) InferLocationFromEndpoint(ep, url string) map[string]any {
	return location.InferLocationFromEndpoint(ep, url).ToMap()
}

func (wireLocationAdapter) NormalizeGeoPayload(country, region, city, zone string) map[string]any {
	return location.NormalizeGeoPayload(country, region, city, zone).ToMap()
}

// ---------------------------------------------------------------------------
// Auth Adapters (Provider + Controller)
// ---------------------------------------------------------------------------

type wireProviderAuthAdapter struct {
	a *auth.ColabOneAuth
}

func (p *wireProviderAuthAdapter) Profile(ctx context.Context) (map[string]string, error) {
	return p.a.Profile(ctx)
}

func (p *wireProviderAuthAdapter) AccessToken(ctx context.Context, name string) (string, error) {
	creds, err := p.a.Credentials(ctx, name)
	if err != nil {
		return "", err
	}
	if creds == nil {
		return "", errors.New("请先完成 Google 登录")
	}
	return creds.Token, nil
}

type wireControllerAuthAdapter struct {
	a *auth.ColabOneAuth
}

func (c *wireControllerAuthAdapter) Locale() string        { return c.a.Locale() }
func (c *wireControllerAuthAdapter) SetLocale(loc string)  { c.a.SetLocale(loc) }
func (c *wireControllerAuthAdapter) LoopbackActive() bool  { return c.a.LoopbackActive() }
func (c *wireControllerAuthAdapter) HasDriveScope() bool   { return c.a.HasDriveScope() }
func (c *wireControllerAuthAdapter) InvalidateDriveQuotaProject() {
	c.a.InvalidateDriveQuotaProject()
}
func (c *wireControllerAuthAdapter) ResetAccountState() {
	c.a.ResetAccountState()
}
func (c *wireControllerAuthAdapter) RequiresDriveQuotaProject() bool {
	return c.a.RequiresDriveQuotaProject()
}
func (c *wireControllerAuthAdapter) DriveQuotaStatus() map[string]any {
	return c.a.DriveQuotaStatus()
}

func (c *wireControllerAuthAdapter) Credentials(ctx context.Context, scope string) (any, error) {
	return c.a.Credentials(ctx, scope)
}

func (c *wireControllerAuthAdapter) Profile(ctx context.Context) (map[string]any, error) {
	p, err := c.a.Profile(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out, nil
}

func (c *wireControllerAuthAdapter) ColabBegin(_ context.Context) (map[string]any, error) {
	res, err := c.a.ColabBegin()
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(res))
	for k, v := range res {
		out[k] = v
	}
	return out, nil
}

func (c *wireControllerAuthAdapter) ColabLogin(ctx context.Context, onAuthURL func(string)) error {
	return c.a.ColabLogin(ctx, onAuthURL)
}

func (c *wireControllerAuthAdapter) ColabFinish(ctx context.Context, code string) error {
	_, err := c.a.ColabFinish(ctx, code)
	return err
}

func (c *wireControllerAuthAdapter) EnsureDriveQuotaProject(ctx context.Context, forceRefresh bool, onLog controller.LogFunc) (string, string, error) {
	var fn func(msg, source, level string)
	if onLog != nil {
		fn = func(msg, source, level string) {
			onLog(msg, source, level)
		}
	}
	pid, reason := c.a.EnsureDriveQuotaProject(ctx, fn, forceRefresh)
	return pid, reason, nil
}

func (c *wireControllerAuthAdapter) BuildRcloneConf(ctx context.Context, quotaProject string) (string, error) {
	if quotaProject != "" {
		qp := quotaProject
		return c.a.BuildRcloneConf(ctx, &qp)
	}
	return c.a.BuildRcloneConf(ctx, nil)
}

// ---------------------------------------------------------------------------
// Colab Client + Assign Proxy Error Bridging
// ---------------------------------------------------------------------------

type wireColabClientAdapter struct {
	c      *colabclient.ColabOneColabClient
	closer interface{ Close() error }
}

func (w *wireColabClientAdapter) Close() error {
	if w.closer != nil {
		return w.closer.Close()
	}
	return nil
}

func (w *wireColabClientAdapter) ColabDomain() string {
	return w.c.ColabDomain()
}

func (w *wireColabClientAdapter) mapErr(err error) error {
	return mapColabClientErr(err, w != nil && w.closer != nil)
}

func mapColabClientErr(err error, isAssignProxy bool) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, colabclient.ErrTooManyAssignments) {
		return &provider.TooManyAssignmentsError{Err: err}
	}
	var reqErr *colabclient.ColabOneRequestError
	if errors.As(err, &reqErr) {
		return &provider.ColabRequestError{
			StatusCode:   reqErr.StatusCode,
			Method:       reqErr.Method,
			URL:          reqErr.URL,
			Reason:       reqErr.Reason,
			ResponseBody: reqErr.Body,
			Err:          err,
		}
	}
	var pErr *assignproxy.ProxyError
	if errors.As(err, &pErr) {
		kind := "proxy"
		switch {
		case errors.Is(pErr, assignproxy.ErrProxyAuth):
			kind = "auth"
		case errors.Is(pErr, assignproxy.ErrProxyTLS):
			kind = "ssl"
		case errors.Is(pErr, assignproxy.ErrProxyTimeout):
			kind = "connect_timeout"
		}
		return &provider.AssignProxyError{
			Kind:    kind,
			Message: pErr.Error(),
			Err:     err,
		}
	}
	if isAssignProxy && assignproxy.IsProxyFailFastError(err) {
		formatted := assignproxy.FormatProxyError(err)
		kind := "proxy"
		switch {
		case strings.Contains(formatted, "认证失败"):
			kind = "auth"
		case strings.Contains(formatted, "证书"):
			kind = "ssl"
		case strings.Contains(formatted, "超时"):
			kind = "connect_timeout"
		}
		return &provider.AssignProxyError{
			Kind:    kind,
			Message: formatted,
			Err:     err,
		}
	}
	return err
}

func mapProxyInfo(info colabclient.ColabOneRuntimeProxyInfo) *provider.RuntimeProxyInfo {
	if info.URL == "" && info.Token == "" && info.TokenExpiresInSeconds == 0 {
		return nil
	}
	var expiresAt time.Time
	if info.TokenExpiresInSeconds > 0 {
		expiresAt = info.ExpiresAt()
	}
	return &provider.RuntimeProxyInfo{
		URL:                   info.URL,
		Token:                 info.Token,
		TokenExpiresInSeconds: info.TokenExpiresInSeconds,
		ExpiresAt:             expiresAt,
	}
}

func (w *wireColabClientAdapter) GetConsumptionUserInfo(ctx context.Context) (*provider.ConsumptionInfo, error) {
	info, err := w.c.GetConsumptionUserInfo(ctx)
	if err != nil {
		return nil, w.mapErr(err)
	}
	return &provider.ConsumptionInfo{
		PaidComputeUnitsBalance: info.PaidComputeUnitsBalance,
		ConsumptionRateHourly:   info.ConsumptionRateHourly,
	}, nil
}

func (w *wireColabClientAdapter) ListAssignments(ctx context.Context) ([]provider.ListedAssignment, error) {
	items, err := w.c.ListAssignments(ctx)
	if err != nil {
		return nil, w.mapErr(err)
	}
	out := make([]provider.ListedAssignment, len(items))
	for i, item := range items {
		out[i] = provider.ListedAssignment{
			Endpoint:         item.Endpoint,
			Variant:          item.Variant,
			Accelerator:      item.Accelerator,
			MachineShape:     item.MachineShape,
			RuntimeProxyInfo: mapProxyInfo(item.RuntimeProxyInfo),
		}
	}
	return out, nil
}

func (w *wireColabClientAdapter) GetAssignment(ctx context.Context, nbh, variant, accel, shape string) (*provider.Assignment, string, error) {
	assigned, xsrf, err := w.c.GetAssignment(ctx, nbh, variant, accel, shape)
	if err != nil {
		return nil, "", w.mapErr(err)
	}
	if assigned == nil {
		return nil, xsrf, nil
	}
	return &provider.Assignment{
		Endpoint:         assigned.Endpoint,
		Variant:          assigned.Variant,
		Accelerator:      assigned.Accelerator,
		RuntimeProxyInfo: mapProxyInfo(assigned.RuntimeProxyInfo),
	}, xsrf, nil
}

func (w *wireColabClientAdapter) PostAssignment(ctx context.Context, nbh, token, variant, accel, shape string) (*provider.Assignment, error) {
	assigned, err := w.c.PostAssignment(ctx, nbh, token, variant, accel, shape)
	if err != nil {
		return nil, w.mapErr(err)
	}
	return &provider.Assignment{
		Endpoint:         assigned.Endpoint,
		Variant:          assigned.Variant,
		Accelerator:      assigned.Accelerator,
		RuntimeProxyInfo: mapProxyInfo(assigned.RuntimeProxyInfo),
	}, nil
}

func (w *wireColabClientAdapter) Unassign(ctx context.Context, endpoint string) error {
	return w.mapErr(w.c.Unassign(ctx, endpoint))
}

func (w *wireColabClientAdapter) KeepAlive(ctx context.Context, endpoint string) error {
	return w.mapErr(w.c.KeepAlive(ctx, endpoint))
}

func (w *wireColabClientAdapter) DoAuthorized(ctx context.Context, req *http.Request) (*http.Response, error) {
	resp, err := w.c.DoAuthorized(ctx, req)
	if err != nil {
		return nil, w.mapErr(err)
	}
	return resp, nil
}

// ---------------------------------------------------------------------------
// Contents Client Adapter
// ---------------------------------------------------------------------------

type wireContentsAdapter struct {
	c *colabclient.ColabOneContentsClient
}

func (w *wireContentsAdapter) UploadBytes(ctx context.Context, baseURL, proxyToken, remotePath string, data []byte, timeout time.Duration) error {
	return mapColabClientErr(w.c.UploadBytes(ctx, baseURL, proxyToken, remotePath, data, timeout), false)
}

func (w *wireContentsAdapter) DeleteFile(ctx context.Context, baseURL, proxyToken, remotePath string) error {
	return mapColabClientErr(w.c.DeleteFile(ctx, baseURL, proxyToken, remotePath), false)
}

// ---------------------------------------------------------------------------
// Jupyter Kernel WebSocket Adapter
// ---------------------------------------------------------------------------

type wireKernelAdapter struct {
	kc *jupyterws.ColabOneKernelClient
}

func mapKernelErr(err error) error {
	if err == nil {
		return nil
	}
	var he *jupyterws.ColabOneHTTPError
	if errors.As(err, &he) {
		return &provider.ColabRequestError{
			StatusCode:   he.StatusCode,
			Method:       he.Method,
			URL:          he.URL,
			ResponseBody: he.Body,
			Err:          err,
		}
	}
	return err
}

func (w *wireKernelAdapter) Start(ctx context.Context) error {
	return mapKernelErr(w.kc.Start(ctx))
}

func (w *wireKernelAdapter) ExecuteCode(ctx context.Context, code string, opts provider.ExecuteOptions) ([]provider.KernelOutput, error) {
	jOpts := jupyterws.ColabOneExecuteOptions{
		AllowStdin: opts.AllowStdin,
		Timeout:    opts.Timeout,
	}
	if opts.StdinHook != nil {
		jOpts.StdinHook = func(prompt string, _ bool) (string, error) {
			return opts.StdinHook(prompt), nil
		}
	}
	if opts.OutputHook != nil {
		jOpts.OutputHook = func(out jupyterws.ColabOneKernelOutput) {
			opts.OutputHook(provider.KernelOutput{
				OutputType: out.OutputType,
				Name:       out.Name,
				Text:       out.Text,
				EName:      out.Ename,
				EValue:     out.Evalue,
			})
		}
	}
	if opts.ColabRequestHook != nil {
		jOpts.RequestHook = func(msg *jupyterws.ColabOneWSMessage, sendInputReply func(parentHeader map[string]any, value any) error) bool {
			if msg == nil {
				return false
			}
			m := map[string]any{
				"msg_type":      msg.MsgType,
				"channel":       msg.Channel,
				"header":        msg.Header,
				"parent_header": msg.ParentHeader,
				"metadata":      msg.Metadata,
				"content":       msg.Content,
			}
			return opts.ColabRequestHook(m, func(payload map[string]any) error {
				return sendInputReply(msg.Header, payload)
			})
		}
	}
	rawOuts, err := w.kc.ExecuteCode(ctx, code, jOpts)
	if err != nil {
		return nil, mapKernelErr(err)
	}
	out := make([]provider.KernelOutput, len(rawOuts))
	for i, item := range rawOuts {
		out[i] = provider.KernelOutput{
			OutputType: item.OutputType,
			Name:       item.Name,
			Text:       item.Text,
			EName:      item.Ename,
			EValue:     item.Evalue,
		}
	}
	return out, nil
}

func (w *wireKernelAdapter) Abort()                                          { w.kc.Abort() }
func (w *wireKernelAdapter) Stop(ctx context.Context, shutdown bool) error   { return w.kc.Stop(ctx, shutdown) }
func (w *wireKernelAdapter) KernelID() string                                { return w.kc.KernelID() }
func (w *wireKernelAdapter) SessionID() string                               { return w.kc.SessionID() }

// ---------------------------------------------------------------------------
// DriveFS Mounter Adapter
// ---------------------------------------------------------------------------

type wireDriveMounterAdapter struct {
	dm       *drivemount.ColabOneDriveMount
	ctx      context.Context
	cancel   context.CancelFunc
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func newWireDriveMounterAdapter(client provider.ColabClient, endpoint string, opts provider.DriveMountOptions) *wireDriveMounterAdapter {
	dmCtx, dmCancel := context.WithCancel(context.Background())
	var onLog func(msg, source, level string)
	if opts.OnLog != nil {
		onLog = func(msg, source, level string) {
			opts.OnLog(msg, source, level)
		}
	}
	dm := drivemount.NewColabOneDriveMount(drivemount.ColabOneDriveMountOptions{
		Client:       client,
		Endpoint:     endpoint,
		OnAuthURL:    opts.OnAuthURL,
		OnLog:        onLog,
		OnHint:       opts.OnHint,
		AccountEmail: opts.AccountEmail,
		Timeout:      opts.Timeout,
	})
	adapter := &wireDriveMounterAdapter{
		dm:     dm,
		ctx:    dmCtx,
		cancel: dmCancel,
	}
	if opts.CancelCh != nil {
		adapter.wg.Add(1)
		go func() {
			defer adapter.wg.Done()
			select {
			case <-opts.CancelCh:
				dm.SignalCancel()
			case <-dmCtx.Done():
			}
		}()
	}
	if opts.ConfirmCh != nil {
		adapter.wg.Add(1)
		go func() {
			defer adapter.wg.Done()
			for {
				select {
				case _, ok := <-opts.ConfirmCh:
					if !ok {
						return
					}
					dm.SignalConfirm()
				case <-dmCtx.Done():
					return
				}
			}
		}()
	}
	if opts.ProbeCh != nil {
		adapter.wg.Add(1)
		go func() {
			defer adapter.wg.Done()
			for {
				select {
				case _, ok := <-opts.ProbeCh:
					if !ok {
						return
					}
					dm.SignalProbe()
				case <-dmCtx.Done():
					return
				}
			}
		}()
	}
	return adapter
}

func (a *wireDriveMounterAdapter) DryrunCheck(ctx context.Context) (bool, string, error) {
	return a.dm.DryrunCheck(ctx)
}

func (a *wireDriveMounterAdapter) Hook(msg map[string]any, reply func(payload map[string]any) error) bool {
	msgType, _ := msg["msg_type"].(string)
	content, _ := msg["content"].(map[string]any)
	metadata, _ := msg["metadata"].(map[string]any)
	return a.dm.HandleColabRequest(a.ctx, msgType, content, metadata, reply)
}

func (a *wireDriveMounterAdapter) Stop() {
	a.stopOnce.Do(func() {
		a.dm.Stop()
		a.cancel()
		a.wg.Wait()
	})
}

func (a *wireDriveMounterAdapter) Err() error {
	if msg := a.dm.Error(); msg != "" {
		return errors.New(msg)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Service Bundle Adapters (Provider + Controller)
// ---------------------------------------------------------------------------

type wireProviderBundlerAdapter struct {
	b *servicebundle.ColabOneServiceBundle
}

func (w *wireProviderBundlerAdapter) BuildZip() ([]byte, int, error) {
	return w.b.BuildZip()
}

func (w *wireProviderBundlerAdapter) VMBootScript() (string, error) {
	raw, err := w.b.ReadServerFile("vm_boot.py")
	return string(raw), err
}

func (w *wireProviderBundlerAdapter) VMUpdateScript() (string, error) {
	raw, err := w.b.ReadServerFile("vm_update.py")
	return string(raw), err
}

type wireControllerBundlerAdapter struct {
	b *servicebundle.ColabOneServiceBundle
}

func (w *wireControllerBundlerAdapter) ServiceRevision() string {
	return w.b.ServiceRevision()
}

func (w *wireControllerBundlerAdapter) ReadPresetsJSON() ([]byte, error) {
	return w.b.ReadCatalogFile("presets.json")
}

func (w *wireControllerBundlerAdapter) ReadComfyCatalogJSON() ([]byte, error) {
	return w.b.ReadCatalogFile("comfyui.json")
}

// ---------------------------------------------------------------------------
// Controller -> Provider Adapter
// ---------------------------------------------------------------------------

type wireControllerProviderAdapter struct {
	p *provider.ColabOneProvider
}

var (
	_ controller.ProviderClient       = (*wireControllerProviderAdapter)(nil)
	_ controller.ProviderSessionStore = (*wireControllerProviderAdapter)(nil)
	_ controller.ProviderCloser       = (*wireControllerProviderAdapter)(nil)
)

func wireProviderParseAssignProxy(raw string) (*provider.ColabOneAssignProxySpec, error) {
	spec, err := assignproxy.ParseAssignProxy(raw)
	if err != nil || spec == nil {
		return nil, err
	}
	return &provider.ColabOneAssignProxySpec{
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

// bridgeRepeatEvent forwards repeated Set pulses on a controller.ColabOneEvent
// into a buffered chan struct{} until done is closed, atomically consuming and
// resetting the event on each pulse without polling.
func bridgeRepeatEvent(ev *controller.ColabOneEvent, done <-chan struct{}, wg *sync.WaitGroup) <-chan struct{} {
	if ev == nil {
		return nil
	}
	ch := make(chan struct{}, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ev.Consume(done) {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}()
	return ch
}

func bridgeCancelEvent(ev *controller.ColabOneEvent, done <-chan struct{}, wg *sync.WaitGroup) <-chan struct{} {
	if ev == nil {
		return nil
	}
	ch := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-ev.Chan():
			close(ch)
		case <-done:
		}
	}()
	return ch
}

func (w *wireControllerProviderAdapter) Account(ctx context.Context) (map[string]any, error) {
	return w.p.Account(ctx)
}

func (w *wireControllerProviderAdapter) RPC(ctx context.Context, method, path string, body any, opts controller.ColabOneRPCOptions) (any, error) {
	res, err := w.p.RPC(ctx, method, path, body, opts.Endpoint, opts.Timeout)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (w *wireControllerProviderAdapter) Start(ctx context.Context, gpu string, notify func(string), opts controller.ColabOneStartOptions) (map[string]any, error) {
	done := make(chan struct{})
	var bridgeWG sync.WaitGroup
	defer func() {
		close(done)
		bridgeWG.Wait()
	}()

	var onLog provider.LogFn
	if opts.OnLog != nil {
		onLog = func(msg, source, level string) { opts.OnLog(msg, source, level) }
	}
	return w.p.Start(ctx, gpu, notify, provider.StartOptions{
		CreateNew:    opts.CreateNew,
		RcloneConf:   opts.RcloneConf,
		OnTelemetry:  opts.OnTelemetry,
		OnLog:        onLog,
		CancelCh:     bridgeCancelEvent(opts.CancelEvent, done, &bridgeWG),
		OnRetryState: opts.OnRetryState,
		AssignProxy:  opts.AssignProxy,
		MaxAttempts:  opts.MaxAttempts,
	})
}

func (w *wireControllerProviderAdapter) UpgradeService(ctx context.Context, endpoint string, onLog controller.LogFunc) error {
	var fn provider.LogFn
	if onLog != nil {
		fn = func(msg, source, level string) { onLog(msg, source, level) }
	}
	return w.p.UpgradeService(ctx, endpoint, fn)
}

func (w *wireControllerProviderAdapter) Release(ctx context.Context, endpoint string, onLog controller.LogFunc) (map[string]any, error) {
	var fn provider.LogFn
	if onLog != nil {
		fn = func(msg, source, level string) { onLog(msg, source, level) }
	}
	return w.p.Release(ctx, endpoint, fn)
}

func (w *wireControllerProviderAdapter) ReleaseAll(ctx context.Context, targets []string, notify func(string)) (map[string]any, error) {
	return w.p.ReleaseAll(ctx, targets, notify)
}

func (w *wireControllerProviderAdapter) Heartbeat(ctx context.Context, endpoint string) error {
	return w.p.Heartbeat(ctx, endpoint)
}

func (w *wireControllerProviderAdapter) Clear(endpoint string) {
	w.p.Clear(endpoint)
}

func (w *wireControllerProviderAdapter) ClearOrphanedRemotes() {
	w.p.ClearOrphanedRemotes()
}

func (w *wireControllerProviderAdapter) SetActive(endpoint string) (string, error) {
	return w.p.SetActive(endpoint)
}

func (w *wireControllerProviderAdapter) SaveRemote(remote map[string]any, activate bool) error {
	return w.p.SaveRemote(remote, activate)
}

func (w *wireControllerProviderAdapter) UploadDriveConfig(ctx context.Context, rcloneConf string, endpoint string) error {
	return w.p.UploadDriveConfig(ctx, rcloneConf, endpoint)
}

func (w *wireControllerProviderAdapter) CheckColabDriveFSAuth(ctx context.Context, endpoint string) (bool, string, error) {
	return w.p.CheckColabDriveFSAuth(ctx, endpoint)
}

func (w *wireControllerProviderAdapter) MountColabDriveFS(ctx context.Context, opts controller.ColabOneDriveMountOptions) (map[string]any, error) {
	done := make(chan struct{})
	var bridgeWG sync.WaitGroup
	defer func() {
		close(done)
		bridgeWG.Wait()
	}()

	var onLog provider.LogFn
	if opts.OnLog != nil {
		onLog = func(msg, source, level string) { opts.OnLog(msg, source, level) }
	}
	return w.p.MountColabDriveFS(ctx, opts.Endpoint, provider.DriveMountOptions{
		OnAuthURL:    opts.OnAuthURL,
		OnLog:        onLog,
		CancelCh:     bridgeCancelEvent(opts.CancelEvent, done, &bridgeWG),
		ConfirmCh:    bridgeRepeatEvent(opts.ConfirmEvent, done, &bridgeWG),
		ProbeCh:      bridgeRepeatEvent(opts.ProbeEvent, done, &bridgeWG),
		OnHint:       opts.OnHint,
		AccountEmail: opts.AccountEmail,
	})
}

func (w *wireControllerProviderAdapter) ObserveExternalTelemetry(ctx context.Context, endpoint string) (map[string]any, error) {
	res, err := w.p.ObserveExternalTelemetry(ctx, endpoint)
	if err != nil {
		if errors.Is(err, provider.ErrExternalEndpointGone) {
			return nil, fmt.Errorf("%w: %w", controller.ErrLookup, err)
		}
		return nil, err
	}
	return res, nil
}

func (w *wireControllerProviderAdapter) CloseObserver(endpoint string, shutdownKernel bool) {
	w.p.CloseObserver(endpoint, shutdownKernel)
}

func (w *wireControllerProviderAdapter) Close() {
	w.p.Close()
}

func (w *wireControllerProviderAdapter) SessionsMap() map[string]map[string]any {
	raw := w.p.SessionsMap()
	out := make(map[string]map[string]any, len(raw))
	for ep, st := range raw {
		m := map[string]any{
			"name":          st.Name,
			"token":         st.Token,
			"url":           st.URL,
			"endpoint":      st.Endpoint,
			"variant":       st.Variant,
			"accelerator":   st.Accelerator,
			"machine_shape": st.MachineShape,
		}
		if st.TokenExpiresAt != nil {
			m["token_expires_at"] = st.TokenExpiresAt.Format(time.RFC3339Nano)
		}
		if st.KernelID != "" {
			m["kernel_id"] = st.KernelID
		}
		if st.SessionID != "" {
			m["session_id"] = st.SessionID
		}
		out[ep] = m
	}
	return out
}

func (w *wireControllerProviderAdapter) RemotesMap() map[string]map[string]any {
	return w.p.RemotesMap()
}

// ---------------------------------------------------------------------------
// Hub, Models, ComfyUI, PublicAPI, AssignProxy, Shortcuts Adapters
// ---------------------------------------------------------------------------

func structToMap(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

type wireHubAdapter struct {
	h *hub.ColabOneHubClient
}

func (w *wireHubAdapter) SearchModels(ctx context.Context, query, sort, cursor, token, modality, family string) (map[string]any, error) {
	res, err := w.h.SearchModels(ctx, hub.ColabOneHubSearchOptions{
		Query:    query,
		Sort:     sort,
		Cursor:   cursor,
		Token:    token,
		Modality: modality,
		Family:   family,
	})
	if err != nil {
		return nil, err
	}
	return structToMap(res)
}

func (w *wireHubAdapter) ModelFiles(ctx context.Context, repo, token string) (map[string]any, error) {
	res, err := w.h.ModelFiles(ctx, repo, token)
	if err != nil {
		return nil, err
	}
	return structToMap(res)
}

type wireModelsAdapter struct{}

func (wireModelsAdapter) ValidateModelSpec(raw map[string]any) (map[string]any, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	spec, err := models.ParseModelSpec(b)
	if err != nil {
		return nil, err
	}
	return structToMap(spec)
}

func (wireModelsAdapter) ValidateAPIModelID(value string, allowEmpty bool) (string, error) {
	return models.ValidateAPIModelID(value, allowEmpty)
}

func (wireModelsAdapter) ResolveAPIModelID(model map[string]any) string {
	return models.ResolveAPIModelIDFromMap(model)
}

func (wireModelsAdapter) UniqueAPIModelID(baseAPIID string, existingIDs []string, repo, filename string) (string, error) {
	return models.UniqueAPIModelID(baseAPIID, existingIDs, repo, filename)
}

func (wireModelsAdapter) ParseHFInput(repoOrURL, filename, revision string) (string, string, string) {
	return models.ParseHFInput(repoOrURL, filename, revision)
}

func (wireModelsAdapter) ResolveImport(ctx context.Context, repo, filename, revision, token, customName string) (map[string]any, error) {
	spec, err := models.ResolveImport(ctx, models.ColabOneResolveImportOptions{
		Repo:                repo,
		Filename:            filename,
		Revision:            revision,
		Token:               token,
		CustomName:          customName,
		ExtractQuantization: hub.ExtractQuantization,
		ClassifyModality:    hub.ClassifyModality,
		IsAuxiliaryGGUF:     hub.IsAuxiliaryGGUF,
	})
	if err != nil {
		var he *models.ColabOneHTTPStatusError
		if errors.As(err, &he) {
			return nil, &controller.ColabOneHTTPError{StatusCode: he.StatusCode, Message: he.Error()}
		}
		return nil, err
	}
	return structToMap(spec)
}

type wireComfyAdapter struct{}

var (
	_ controller.ComfyValidator            = wireComfyAdapter{}
	_ controller.ComfyCatalogValidator     = wireComfyAdapter{}
	_ controller.ComfyInputUploadValidator = wireComfyAdapter{}
	_ controller.ComfyTtsPreviewValidator  = wireComfyAdapter{}
)

func (wireComfyAdapter) ValidateCatalogBundle(raw map[string]any) (controller.ComfyCatalogBundle, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return controller.ComfyCatalogBundle{}, err
	}
	var cat comfyui.ColabOneComfyCatalog
	if err := json.Unmarshal(b, &cat); err != nil {
		return controller.ComfyCatalogBundle{}, err
	}
	if err := cat.Validate(); err != nil {
		return controller.ComfyCatalogBundle{}, err
	}
	templates := make([]map[string]any, 0, len(cat.Templates))
	for _, tpl := range cat.Templates {
		m, err := structToMap(tpl)
		if err != nil {
			return controller.ComfyCatalogBundle{}, err
		}
		templates = append(templates, m)
	}
	engines := make([]map[string]any, 0, len(cat.TtsEngines))
	for _, eng := range cat.TtsEngines {
		m, err := structToMap(eng)
		if err != nil {
			return controller.ComfyCatalogBundle{}, err
		}
		engines = append(engines, m)
	}
	return controller.ComfyCatalogBundle{
		Templates:  templates,
		TtsEngines: engines,
	}, nil
}

func (w wireComfyAdapter) ValidateCatalog(raw map[string]any) ([]map[string]any, error) {
	bundle, err := w.ValidateCatalogBundle(raw)
	if err != nil {
		return nil, err
	}
	return bundle.Templates, nil
}

func (wireComfyAdapter) ValidateInputUpload(filename, dataB64 string) error {
	return comfyui.ValidateComfyInputUpload(filename, dataB64)
}

func (wireComfyAdapter) ValidateTtsPreviewRequest(raw map[string]any) (map[string]any, error) {
	return comfyui.ValidateTtsPreviewRequest(raw)
}

func (wireComfyAdapter) ValidateWorkflowTemplate(raw map[string]any) (map[string]any, error) {
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
	return structToMap(tpl)
}

func (wireComfyAdapter) ValidateWorkflowImport(raw map[string]any) (map[string]any, error) {
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
	return structToMap(req)
}

func (wireComfyAdapter) ValidateWorkflowRun(raw map[string]any) (map[string]any, error) {
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
	return structToMap(req)
}

func (wireComfyAdapter) ValidateCanvasConfig(raw map[string]any) (map[string]any, error) {
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
	return structToMap(cfg)
}

type wirePublicAPIAdapter struct{}

func (wirePublicAPIAdapter) ValidatePublicConfig(raw map[string]any) (map[string]any, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var cfg publicapi.ColabOnePublicConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return structToMap(cfg)
}

type wireAssignProxyAdapter struct{}

func (wireAssignProxyAdapter) ParseAssignProxy(raw string) (*controller.ColabOneAssignProxySpec, error) {
	spec, err := assignproxy.ParseAssignProxy(raw)
	if err != nil || spec == nil {
		return nil, err
	}
	return &controller.ColabOneAssignProxySpec{
		Scheme:        spec.Scheme,
		Host:          spec.Host,
		Port:          spec.Port,
		Username:      spec.Username,
		Password:      spec.Password,
		NormalizedURL: spec.NormalizedURL,
		MaskedURL:     spec.MaskedURL,
		LogLabel:      spec.LogLabel,
		HasAuth:       spec.HasAuth(),
	}, nil
}

func (wireAssignProxyAdapter) NormalizeAssignProxy(raw string) (string, error) {
	return assignproxy.NormalizeAssignProxy(raw)
}

func (wireAssignProxyAdapter) ProxyLogLabel(raw string) string {
	return assignproxy.ProxyLogLabel(raw)
}

func (wireAssignProxyAdapter) TestAssignProxy(ctx context.Context, raw string) (map[string]any, error) {
	return assignproxy.TestAssignProxy(ctx, raw, assignproxy.DefaultProxyProbeURL, assignproxy.DefaultProxyTestTimeout)
}

type wireShortcutsAdapter struct{}

func (wireShortcutsAdapter) Status(sandboxDir string) map[string]bool {
	st := shortcuts.ShortcutStatus(sandboxDir, "")
	return map[string]bool{
		"start_menu": st.StartMenu,
		"desktop":    st.Desktop,
	}
}

func (wireShortcutsAdapter) Manage(root, target string, remove bool, sandboxDir, locale string) (map[string]bool, error) {
	st, err := shortcuts.ManageShortcuts(root, target, remove, sandboxDir, "", locale)
	if err != nil {
		return nil, err
	}
	return map[string]bool{
		"start_menu": st.StartMenu,
		"desktop":    st.Desktop,
	}, nil
}
