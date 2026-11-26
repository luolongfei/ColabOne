package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/luolongfei/ColabOne/console/internal/controller"
	"github.com/luolongfei/ColabOne/console/internal/vault"
	"github.com/luolongfei/ColabOne/console/internal/webconsole"
)

func TestColabOneDesktopWebConsoleEndToEnd(t *testing.T) {
	dir := t.TempDir()
	sharedVault := vault.NewMemoryVault()

	d := &ColabOneDesktop{
		bridge: &colabOneBridge{
			vault: sharedVault,
			factory: func() *controller.ColabOneController {
				return newColabOneWiredController("", dir, sharedVault, false)
			},
		},
	}
	t.Cleanup(func() {
		d.closeWebConsole()
		d.bridge.close()
	})

	fakeDist := fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><html><body>ColabOne Web OK</body></html>")},
		"assets/app.js": {Data: []byte("console.log('colabone');")},
	}
	d.initWebConsoleWithFS(dir, sharedVault, func() fs.FS { return fakeDist })

	initial := d.GetWebConsoleState()
	if initial.Enabled || initial.Running {
		t.Fatalf("expected web console disabled by default, got %+v", initial)
	}
	if initial.Port != webconsole.DefaultPort {
		t.Fatalf("expected default port %d, got %d", webconsole.DefaultPort, initial.Port)
	}
	if initial.BindMode != webconsole.BindModeLoopback {
		t.Fatalf("expected default bind mode loopback, got %q", initial.BindMode)
	}

	// Enable on an ephemeral free high port? Update validates 1024..65535; pick a deterministic high port
	// or retry in a small range to avoid CI port conflicts.
	enabled := true
	var started webconsole.ColabOneWebConsoleState
	var startErr error
	for port := 43232; port < 43252; port++ {
		p := port
		started, startErr = d.UpdateWebConsole(webconsole.ColabOneWebConsoleUpdateRequest{
			Enabled: &enabled,
			Port:    &p,
		})
		if startErr == nil {
			break
		}
	}
	if startErr != nil {
		t.Fatalf("failed to start web console: %v", startErr)
	}
	if !started.Enabled || !started.Running {
		t.Fatalf("expected web console running, got %+v", started)
	}
	if !started.HasPasscode || started.PasscodePreview == "" {
		t.Fatalf("expected auto-generated passcode preview, got %+v", started)
	}

	passcode, err := d.GetWebConsolePasscode()
	if err != nil || len(passcode) < 16 {
		t.Fatalf("expected valid passcode, got len=%d err=%v", len(passcode), err)
	}

	// Create a one-time login ticket and redeem it against the live HTTP listener.
	ticket, err := d.CreateWebConsoleLoginTicket()
	if err != nil || !strings.Contains(ticket.URL, "/_colabone_web/redeem?ticket=") {
		t.Fatalf("unexpected login ticket: %+v err=%v", ticket, err)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	resp, err := client.Get(ticket.URL)
	if err != nil {
		t.Fatalf("redeem ticket failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after redeem redirect, got %d", resp.StatusCode)
	}

	// Authenticated snapshot call through the live HTTP server -> colabOneBridge -> ColabOneController.
	snapReq, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, started.LocalURL+"/api/snapshot", nil)
	snapReq.Header.Set(webconsole.ClientHeaderName, webconsole.ClientHeaderValue)
	snapResp, err := client.Do(snapReq)
	if err != nil {
		t.Fatalf("GET /api/snapshot failed: %v", err)
	}
	defer snapResp.Body.Close()
	if snapResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /api/snapshot, got %d", snapResp.StatusCode)
	}
	var snap map[string]any
	if err := json.NewDecoder(snapResp.Body).Decode(&snap); err != nil {
		t.Fatalf("decode snapshot failed: %v", err)
	}
	if _, ok := snap["runtime"]; !ok {
		t.Fatalf("expected runtime key in snapshot JSON")
	}

	// Host-only secret reveal command must be blocked with 403 over HTTP.
	cmdReq, _ := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		started.LocalURL+"/api/command",
		strings.NewReader(`{"action":"settings.hf_token","payload":{}}`),
	)
	cmdReq.Header.Set("Content-Type", "application/json")
	cmdReq.Header.Set(webconsole.ClientHeaderName, webconsole.ClientHeaderValue)
	cmdResp, err := client.Do(cmdReq)
	if err != nil {
		t.Fatalf("POST /api/command failed: %v", err)
	}
	_ = cmdResp.Body.Close()
	if cmdResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for settings.hf_token over web console, got %d", cmdResp.StatusCode)
	}

	// Revoke sessions and confirm session cookie is rejected.
	revoked, err := d.RevokeWebConsoleSessions()
	if err != nil {
		t.Fatalf("RevokeWebConsoleSessions failed: %v", err)
	}
	if revoked.ActiveSessions != 0 {
		t.Fatalf("expected 0 active sessions after revoke, got %d", revoked.ActiveSessions)
	}
	snapReq2, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, started.LocalURL+"/api/snapshot", nil)
	snapReq2.Header.Set(webconsole.ClientHeaderName, webconsole.ClientHeaderValue)
	snapResp2, err := client.Do(snapReq2)
	if err != nil {
		t.Fatalf("second GET /api/snapshot failed: %v", err)
	}
	_ = snapResp2.Body.Close()
	if snapResp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 after session revoke, got %d", snapResp2.StatusCode)
	}

	// Custom passcode update succeeds and persists in shared vault.
	updated, err := d.SetWebConsolePasscode("CustomPass-2026!")
	if err != nil || !updated.HasPasscode {
		t.Fatalf("SetWebConsolePasscode failed: %+v err=%v", updated, err)
	}
	gotPass, err := d.GetWebConsolePasscode()
	if err != nil || gotPass != "CustomPass-2026!" {
		t.Fatalf("expected updated passcode, got %q err=%v", gotPass, err)
	}

	// Tray menu items reflect running state.
	trayItems := d.trayWebConsoleItems()
	if len(trayItems) != 3 {
		t.Fatalf("expected 3 tray web console items while running, got %d", len(trayItems))
	}

	// Disable web console.
	disabled := false
	stoppedState, err := d.UpdateWebConsole(webconsole.ColabOneWebConsoleUpdateRequest{
		Enabled: &disabled,
	})
	if err != nil {
		t.Fatalf("failed to disable web console: %v", err)
	}
	if stoppedState.Enabled || stoppedState.Running {
		t.Fatalf("expected web console stopped, got %+v", stoppedState)
	}
	if items := d.trayWebConsoleItems(); len(items) != 0 {
		t.Fatalf("expected no tray web console items while stopped, got %d", len(items))
	}
}
