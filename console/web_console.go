package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/egoist/mygo"
	"github.com/luolongfei/ColabOne/console/internal/vault"
	"github.com/luolongfei/ColabOne/console/internal/webconsole"
)

// ColabOneWebConsoleEvent pushes live remote web console status and active
// session count updates from Go to open desktop windows.
var ColabOneWebConsoleEvent = mygo.NewEvent[webconsole.ColabOneWebConsoleState]("colabone-web-console")

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
	})
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
	if !mygo.App.IsReady() {
		return
	}
	_ = ColabOneWebConsoleEvent.Broadcast(state)
	d.trayMu.Lock()
	hasTray := d.tray != nil
	d.trayMu.Unlock()
	if !hasTray {
		return
	}
	mygo.RunOnMain(func() {
		d.trayMu.Lock()
		curTray := d.tray
		d.trayMu.Unlock()
		if curTray != nil {
			curTray.SetMenu(d.buildTrayMenu())
		}
	})
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

// RevokeWebConsoleSessions signs out all currently authenticated web browsers.
func (d *ColabOneDesktop) RevokeWebConsoleSessions() (webconsole.ColabOneWebConsoleState, error) {
	wc := d.webConsoleService()
	if wc == nil {
		return d.GetWebConsoleState(), errors.New(colabOneT("err.webConsoleUnavailable"))
	}
	return wc.RevokeSessions(), nil
}
