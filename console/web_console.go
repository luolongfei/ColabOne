package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strings"

	"github.com/egoist/mygo"
	"github.com/luolongfei/ColabOne/console/internal/activity"
	"github.com/luolongfei/ColabOne/console/internal/controller"
	"github.com/luolongfei/ColabOne/console/internal/vault"
	"github.com/luolongfei/ColabOne/console/internal/webconsole"
)

// ColabOneWebConsoleEvent pushes live remote web console status and active
// session count updates from Go to open desktop windows.
var ColabOneWebConsoleEvent = mygo.NewEvent[webconsole.ColabOneWebConsoleState]("colabone-web-console")

// ColabOneWorkflowSharesEvent pushes live workflow share list and active guest
// session count updates from Go to open desktop windows.
var ColabOneWorkflowSharesEvent = mygo.NewEvent[webconsole.ColabOneWorkflowShareListState]("colabone-workflow-shares")

// ColabOneOutputSharesEvent pushes live output gallery share list and active
// guest session count updates from Go to open desktop windows.
var ColabOneOutputSharesEvent = mygo.NewEvent[webconsole.ColabOneOutputShareListState]("colabone-output-shares")

func (d *ColabOneDesktop) initWebConsole(configDir string, vaultStore *vault.ColabOneVault) {
	d.initWebConsoleWithFS(configDir, vaultStore, colabOneFrontendFS)
}

func (d *ColabOneDesktop) initWebConsoleWithFS(
	configDir string,
	vaultStore *vault.ColabOneVault,
	frontendFS func() fs.FS,
) {
	if frontendFS == nil {
		frontendFS = colabOneFrontendFS
	}
	var vAdapter webconsole.VaultStore
	if vaultStore != nil {
		vAdapter = &wireVaultAdapter{v: vaultStore}
	}
	wc := webconsole.NewColabOneWebConsole(webconsole.ColabOneWebConsoleOptions{
		ConfigDir: configDir,
		Vault:     vAdapter,
		CallBridge: func(ctx context.Context, method string, payload map[string]any) (string, error) {
			if d.bridge == nil {
				return "", errors.New(colabOneT("err.bridgeNotInitialized"))
			}
			return d.bridge.call(ctx, method, payload)
		},
		FetchSnapshot: func(ctx context.Context) (string, error) {
			if d.bridge == nil {
				return "", errors.New(colabOneT("err.bridgeNotInitialized"))
			}
			return d.bridge.call(ctx, "snapshot", nil)
		},
		ServeOutputMedia: func(ctx context.Context, rw http.ResponseWriter, r *http.Request, id string) {
			if d.bridge == nil {
				writeColabOneMediaError(rw, r, http.StatusServiceUnavailable, colabOneT("err.bridgeNotInitialized"))
				return
			}
			d.bridge.serveOutputMedia(ctx, rw, r, id)
		},
		ServeShareOutputMedia: func(ctx context.Context, rw http.ResponseWriter, r *http.Request, id string) {
			if d.bridge == nil {
				writeColabOneMediaError(rw, r, http.StatusServiceUnavailable, "当前无法访问该作品文件，请稍后再试")
				return
			}
			d.bridge.serveOutputMediaWithOptions(ctx, rw, r, id, controller.ColabOneOutputMediaOptions{
				RuntimeOnly: true,
			})
		},
		ServeOutputShareMedia: func(ctx context.Context, rw http.ResponseWriter, r *http.Request, id string) {
			if d.bridge == nil {
				writeColabOneMediaError(rw, r, http.StatusServiceUnavailable, "作品暂离线或暂不可访问，请稍后再试")
				return
			}
			d.bridge.serveOutputMediaWithOptions(ctx, rw, r, id, controller.ColabOneOutputMediaOptions{
				SanitizeErrors: true,
			})
		},
		StreamOutputMedia: func(ctx context.Context, id string, w io.Writer, sanitizeErrors bool) (webconsole.ColabOneOutputStreamMeta, error) {
			if d.bridge == nil {
				if sanitizeErrors {
					return webconsole.ColabOneOutputStreamMeta{}, webconsole.ErrOutputMediaOffline
				}
				return webconsole.ColabOneOutputStreamMeta{}, errors.New(colabOneT("err.bridgeNotInitialized"))
			}
			meta, err := d.bridge.streamOutputMedia(ctx, id, w, controller.ColabOneOutputMediaOptions{
				SanitizeErrors: sanitizeErrors,
			})
			if err != nil {
				if errors.Is(err, controller.ErrNotFound) {
					return webconsole.ColabOneOutputStreamMeta{}, webconsole.ErrOutputMediaNotFound
				}
				if errors.Is(err, controller.ErrOutputMediaOffline) {
					return webconsole.ColabOneOutputStreamMeta{}, webconsole.ErrOutputMediaOffline
				}
				return webconsole.ColabOneOutputStreamMeta{}, err
			}
			if meta == nil {
				return webconsole.ColabOneOutputStreamMeta{}, nil
			}
			return webconsole.ColabOneOutputStreamMeta{
				ID:        meta.ID,
				Filename:  meta.Filename,
				Extension: meta.Extension,
				Mime:      meta.Mime,
				Size:      meta.Size,
			}, nil
		},
		FrontendFS: frontendFS,
		AppVersion: colabOneAppVersion(),
		GetLocale:  colabOneGetLocale,
		OnLog: func(message, source, level string) {
			if d.bridge != nil {
				d.bridge.logActivity(message, source, level)
			}
		},
		OnStateChange: func(state webconsole.ColabOneWebConsoleState) {
			d.notifyWebConsoleState(state)
		},
		OnWorkflowSharesChange: func(state webconsole.ColabOneWorkflowShareListState) {
			d.notifyWorkflowSharesState(state)
		},
		OnOutputSharesChange: func(state webconsole.ColabOneOutputShareListState) {
			d.notifyOutputSharesState(state)
		},
	})
	if d.outboundLog != nil {
		wc.SetLiveWSOutboundLogger(d.outboundLog)
	}
	d.webConsoleMu.Lock()
	d.webConsole = wc
	d.webConsoleMu.Unlock()
}

func (d *ColabOneDesktop) webConsoleService() *webconsole.ColabOneWebConsole {
	d.webConsoleMu.RLock()
	wc := d.webConsole
	d.webConsoleMu.RUnlock()
	return wc
}

func (d *ColabOneDesktop) startWebConsoleIfEnabled() error {
	wc := d.webConsoleService()
	if wc == nil {
		return nil
	}
	return wc.StartIfEnabled()
}

func (d *ColabOneDesktop) closeWebConsole() {
	d.webConsoleMu.Lock()
	wc := d.webConsole
	d.webConsole = nil
	d.webConsoleMu.Unlock()
	if wc != nil {
		wc.Close()
	}
}

func (d *ColabOneDesktop) notifyWebConsoleState(state webconsole.ColabOneWebConsoleState) {
	if d.appQuitting.Load() || !d.isAppReady() {
		return
	}
	_ = ColabOneWebConsoleEvent.Broadcast(state)
	d.refreshTrayMenu()
}

func (d *ColabOneDesktop) trayWebConsoleItems() []*mygo.MenuItem {
	ws := d.GetWebConsoleState()
	if !ws.Running {
		return nil
	}
	addr := ws.PublicOrigin
	if addr == "" {
		addr = ws.LocalURL
	}
	if addr == "" {
		addr = ws.ListenAddr
	}
	return []*mygo.MenuItem{
		mygo.Separator(),
		{
			Label:    fmt.Sprintf(colabOneT("tray.webConsoleRunning"), addr),
			Disabled: true,
		},
		{
			Label: colabOneT("tray.webConsoleStop"),
			Click: func(*mygo.MenuItem, *mygo.Window) {
				go func() {
					disabled := false
					_, _ = d.UpdateWebConsole(webconsole.ColabOneWebConsoleUpdateRequest{
						Enabled: &disabled,
					})
				}()
			},
		},
	}
}

// GetWebConsoleState returns the live, redacted status of the remote web console server.
func (d *ColabOneDesktop) GetWebConsoleState() webconsole.ColabOneWebConsoleState {
	wc := d.webConsoleService()
	if wc == nil {
		return webconsole.ColabOneWebConsoleState{
			BindMode:   webconsole.BindModeLoopback,
			ListenAddr: fmt.Sprintf("127.0.0.1:%d", webconsole.DefaultPort),
			Port:       webconsole.DefaultPort,
			LocalURL:   fmt.Sprintf("http://127.0.0.1:%d", webconsole.DefaultPort),
		}
	}
	return wc.State()
}

// UpdateWebConsole updates persisted web console settings and hot-applies start,
// stop, bind-mode, port, or public-origin changes without restarting ColabOne.
func (d *ColabOneDesktop) UpdateWebConsole(
	req webconsole.ColabOneWebConsoleUpdateRequest,
) (webconsole.ColabOneWebConsoleState, error) {
	wc := d.webConsoleService()
	if wc == nil {
		return d.GetWebConsoleState(), errors.New(colabOneT("err.webConsoleUnavailable"))
	}
	return wc.Update(req)
}

// GetWebConsolePasscode returns the saved web console passcode for on-demand
// reveal or copying in the desktop Settings UI.
func (d *ColabOneDesktop) GetWebConsolePasscode() (string, error) {
	wc := d.webConsoleService()
	if wc == nil {
		return "", errors.New(colabOneT("err.webConsoleUnavailable"))
	}
	return wc.Passcode()
}

// SetWebConsolePasscode updates or regenerates (when empty) the web console
// access passcode and invalidates all active browser sessions.
func (d *ColabOneDesktop) SetWebConsolePasscode(passcode string) (webconsole.ColabOneWebConsoleState, error) {
	wc := d.webConsoleService()
	if wc == nil {
		return d.GetWebConsoleState(), errors.New(colabOneT("err.webConsoleUnavailable"))
	}
	return wc.SetPasscode(passcode)
}

// CreateWebConsoleLoginTicket issues a short-lived browser unlock link.
// When singleUse is false, the link can be redeemed repeatedly within its TTL;
// when singleUse is true, the first redemption invalidates it immediately.
func (d *ColabOneDesktop) CreateWebConsoleLoginTicket(singleUse bool) (webconsole.ColabOneWebConsoleTicket, error) {
	wc := d.webConsoleService()
	if wc == nil {
		return webconsole.ColabOneWebConsoleTicket{}, errors.New(colabOneT("err.webConsoleUnavailable"))
	}
	return wc.CreateLoginTicket(singleUse)
}

func (d *ColabOneDesktop) notifyWorkflowSharesState(state webconsole.ColabOneWorkflowShareListState) {
	if d.appQuitting.Load() || !d.isAppReady() {
		return
	}
	_ = ColabOneWorkflowSharesEvent.Broadcast(state)
}

func (d *ColabOneDesktop) notifyOutputSharesState(state webconsole.ColabOneOutputShareListState) {
	if d.appQuitting.Load() || !d.isAppReady() {
		return
	}
	_ = ColabOneOutputSharesEvent.Broadcast(state)
}

// RevokeWebConsoleSessions signs out all currently authenticated web browsers.
func (d *ColabOneDesktop) RevokeWebConsoleSessions() (webconsole.ColabOneWebConsoleState, error) {
	wc := d.webConsoleService()
	if wc == nil {
		return d.GetWebConsoleState(), errors.New(colabOneT("err.webConsoleUnavailable"))
	}
	return wc.RevokeSessions(), nil
}

// GetWorkflowSharesState returns all configured workflow share links and live guest session counts.
func (d *ColabOneDesktop) GetWorkflowSharesState() webconsole.ColabOneWorkflowShareListState {
	wc := d.webConsoleService()
	if wc == nil {
		return webconsole.ColabOneWorkflowShareListState{
			Shares: []webconsole.ColabOneWorkflowShareSummary{},
		}
	}
	return wc.WorkflowSharesState()
}

// CreateWorkflowShare generates a new HMAC-signed guest workflow share link.
func (d *ColabOneDesktop) CreateWorkflowShare(
	req webconsole.ColabOneWorkflowShareCreateRequest,
) (webconsole.ColabOneWorkflowShareSummary, error) {
	wc := d.webConsoleService()
	if wc == nil {
		return webconsole.ColabOneWorkflowShareSummary{}, errors.New(colabOneT("err.webConsoleUnavailable"))
	}
	return wc.CreateWorkflowShare(req)
}

// RevokeWorkflowShare revokes a workflow share link and immediately signs out all of its active guests.
func (d *ColabOneDesktop) RevokeWorkflowShare(
	shareID string,
) (webconsole.ColabOneWorkflowShareListState, error) {
	wc := d.webConsoleService()
	if wc == nil {
		return d.GetWorkflowSharesState(), errors.New(colabOneT("err.webConsoleUnavailable"))
	}
	return wc.RevokeWorkflowShare(shareID)
}

// DeleteWorkflowShare permanently removes a workflow share link record and invalidates its active guests.
func (d *ColabOneDesktop) DeleteWorkflowShare(
	shareID string,
) (webconsole.ColabOneWorkflowShareListState, error) {
	wc := d.webConsoleService()
	if wc == nil {
		return d.GetWorkflowSharesState(), errors.New(colabOneT("err.webConsoleUnavailable"))
	}
	return wc.DeleteWorkflowShare(shareID)
}

// GetOutputSharesState returns all configured output gallery share links and live guest session counts.
func (d *ColabOneDesktop) GetOutputSharesState() webconsole.ColabOneOutputShareListState {
	wc := d.webConsoleService()
	if wc == nil {
		return webconsole.ColabOneOutputShareListState{
			Shares: []webconsole.ColabOneOutputShareSummary{},
		}
	}
	return wc.OutputSharesState()
}

// CreateOutputShare generates a new HMAC-signed guest output gallery share link.
func (d *ColabOneDesktop) CreateOutputShare(
	req webconsole.ColabOneOutputShareCreateRequest,
) (webconsole.ColabOneOutputShareSummary, error) {
	wc := d.webConsoleService()
	if wc == nil {
		return webconsole.ColabOneOutputShareSummary{}, errors.New(colabOneT("err.webConsoleUnavailable"))
	}
	return wc.CreateOutputShare(req)
}

// RevokeOutputShare revokes an output gallery share link and immediately signs out its active guests and clears its password.
func (d *ColabOneDesktop) RevokeOutputShare(
	shareID string,
) (webconsole.ColabOneOutputShareListState, error) {
	wc := d.webConsoleService()
	if wc == nil {
		return d.GetOutputSharesState(), errors.New(colabOneT("err.webConsoleUnavailable"))
	}
	return wc.RevokeOutputShare(shareID)
}

// DeleteOutputShare permanently removes an output gallery share link record and invalidates its active guests and password.
func (d *ColabOneDesktop) DeleteOutputShare(
	shareID string,
) (webconsole.ColabOneOutputShareListState, error) {
	wc := d.webConsoleService()
	if wc == nil {
		return d.GetOutputSharesState(), errors.New(colabOneT("err.webConsoleUnavailable"))
	}
	return wc.DeleteOutputShare(shareID)
}

func sanitizeColabOneOutputShareOpenError(err error, shareKey string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if shareKey != "" {
		msg = strings.ReplaceAll(msg, shareKey, "[已隐藏]")
		if escaped := url.QueryEscape(shareKey); escaped != "" && escaped != shareKey {
			msg = strings.ReplaceAll(msg, escaped, "[已隐藏]")
		}
	}
	safe := strings.TrimSpace(activity.SafeMessage(msg))
	if safe == "" {
		return errors.New(colabOneT("err.unsupportedURL"))
	}
	return errors.New(safe)
}

// OpenOutputShare validates shareID, resolves the active server-generated gallery
// share URL from the local web console, verifies http/https without userinfo, and
// opens it in the system browser.
func (d *ColabOneDesktop) OpenOutputShare(ctx context.Context, shareID string) error {
	cleanID := strings.TrimSpace(shareID)
	if cleanID == "" {
		return errors.New("分享编号无效")
	}
	wc := d.webConsoleService()
	if wc == nil {
		return errors.New(colabOneT("err.webConsoleUnavailable"))
	}
	shareURL, err := wc.GetOutputShareURL(cleanID)
	if err != nil {
		return err
	}
	u, err := url.Parse(shareURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" || u.User != nil || u.Host == "" {
		return errors.New(colabOneT("err.unsupportedURL"))
	}
	shareKey := u.Query().Get("k")
	var openErr error
	if d.openExternalFn != nil {
		openErr = d.openExternalFn(shareURL)
	} else {
		openErr = mygo.Shell.OpenExternal(shareURL)
	}
	if openErr != nil {
		return sanitizeColabOneOutputShareOpenError(openErr, shareKey)
	}
	return nil
}
