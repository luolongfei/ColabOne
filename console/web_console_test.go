package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/luolongfei/ColabOne/console/internal/controller"
	"github.com/luolongfei/ColabOne/console/internal/servicebundle"
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

	// Create a short-lived reusable login ticket and redeem it against the live HTTP listener.
	ticket, err := d.CreateWebConsoleLoginTicket(false)
	if err != nil || !strings.Contains(ticket.URL, "/_colabone_web/redeem?ticket=") || ticket.SingleUse {
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

func TestColabOneDesktopWebConsoleSessionSurvivesRestartWithFileVault(t *testing.T) {
	dir := t.TempDir()
	fakeDist := fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><html><body>ColabOne Web OK</body></html>")},
	}

	makeDesktop := func() (*ColabOneDesktop, *vault.ColabOneVault) {
		v := vault.NewFileVault(dir)
		d := &ColabOneDesktop{
			bridge: &colabOneBridge{
				vault: v,
				factory: func() *controller.ColabOneController {
					return newColabOneWiredController("", dir, v, false)
				},
			},
		}
		d.initWebConsoleWithFS(dir, v, func() fs.FS { return fakeDist })
		return d, v
	}

	d1, _ := makeDesktop()
	enabled := true
	var started webconsole.ColabOneWebConsoleState
	var startErr error
	for port := 43260; port < 43280; port++ {
		p := port
		started, startErr = d1.UpdateWebConsole(webconsole.ColabOneWebConsoleUpdateRequest{
			Enabled: &enabled,
			Port:    &p,
		})
		if startErr == nil {
			break
		}
	}
	if startErr != nil || !started.Running {
		t.Fatalf("start d1 web console failed: running=%v err=%v", started.Running, startErr)
	}

	ticket, err := d1.CreateWebConsoleLoginTicket(false)
	if err != nil {
		t.Fatalf("create ticket failed: %v", err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp, err := client.Get(ticket.URL)
	if err != nil {
		t.Fatalf("redeem ticket failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after redeem, got %d", resp.StatusCode)
	}

	// Simulate full desktop app exit and cold restart with a fresh FileVault reader.
	d1.closeWebConsole()
	d1.bridge.close()

	d2, _ := makeDesktop()
	t.Cleanup(func() {
		d2.closeWebConsole()
		d2.bridge.close()
	})
	if err := d2.startWebConsoleIfEnabled(); err != nil {
		t.Fatalf("startWebConsoleIfEnabled after restart failed: %v", err)
	}
	st2 := d2.GetWebConsoleState()
	if !st2.Running || st2.ActiveSessions != 1 {
		t.Fatalf("expected running with 1 persisted session after restart, got %+v", st2)
	}

	// The exact same browser cookie jar authenticates without logging in again.
	snapReq, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, st2.LocalURL+"/api/snapshot", nil)
	snapReq.Header.Set(webconsole.ClientHeaderName, webconsole.ClientHeaderValue)
	snapResp, err := client.Do(snapReq)
	if err != nil {
		t.Fatalf("GET /api/snapshot after restart failed: %v", err)
	}
	_ = snapResp.Body.Close()
	if snapResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after restart with existing cookie, got %d", snapResp.StatusCode)
	}
}

func TestColabOneDesktopWorkflowSharesEndToEnd(t *testing.T) {
	dir := t.TempDir()
	fakeDist := fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><html><body>ColabOne Share OK</body></html>")},
		"assets/app.js": {Data: []byte("console.log('colabone-share');")},
	}

	makeDesktop := func() (*ColabOneDesktop, *vault.ColabOneVault) {
		v := vault.NewFileVault(dir)
		d := &ColabOneDesktop{
			bridge: &colabOneBridge{
				vault: v,
				factory: func() *controller.ColabOneController {
					return newColabOneWiredController("", dir, v, false)
				},
			},
		}
		d.initWebConsoleWithFS(dir, v, func() fs.FS { return fakeDist })
		return d, v
	}

	d1, _ := makeDesktop()
	enabled := true
	var started webconsole.ColabOneWebConsoleState
	var startErr error
	for port := 43290; port < 43310; port++ {
		p := port
		started, startErr = d1.UpdateWebConsole(webconsole.ColabOneWebConsoleUpdateRequest{
			Enabled: &enabled,
			Port:    &p,
		})
		if startErr == nil {
			break
		}
	}
	if startErr != nil || !started.Running {
		t.Fatalf("start d1 web console failed: running=%v err=%v", started.Running, startErr)
	}

	if initShares := d1.GetWorkflowSharesState(); len(initShares.Shares) != 0 {
		t.Fatalf("expected 0 initial shares, got %+v", initShares)
	}

	created, err := d1.CreateWorkflowShare(webconsole.ColabOneWorkflowShareCreateRequest{
		Label:       "Demo Guest",
		Title:       "Digital Human Demo",
		WorkflowIDs: []string{webconsole.BuiltinDigitalHumanGroupKey},
		TTLMode:     webconsole.WorkflowShareTTL24h,
	})
	if err != nil {
		t.Fatalf("CreateWorkflowShare failed: %v", err)
	}
	if !created.CanCopyLink || !strings.Contains(created.URL, "/share?k=co_wf_") {
		t.Fatalf("unexpected created share: %+v", created)
	}

	jar, _ := cookiejar.New(nil)
	guestClient := &http.Client{Jar: jar}

	resp, err := guestClient.Get(created.URL)
	if err != nil {
		t.Fatalf("GET share URL failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after share activation redirect, got %d", resp.StatusCode)
	}

	// Guest fetches /api/share/capabilities and /api/share/snapshot through the live desktop HTTP server.
	capResp, err := guestClient.Get(started.LocalURL + "/api/share/capabilities")
	if err != nil {
		t.Fatalf("GET /api/share/capabilities failed: %v", err)
	}
	defer capResp.Body.Close()
	if capResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /api/share/capabilities, got %d", capResp.StatusCode)
	}
	var caps webconsole.ColabOneWorkflowShareCapabilities
	if err := json.NewDecoder(capResp.Body).Decode(&caps); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if caps.ShareID != created.ID || caps.Runtime != webconsole.WorkflowShareClientHeaderValue || len(caps.AllowedWorkflowIDs) != 2 {
		t.Fatalf("unexpected share capabilities: %+v", caps)
	}

	snapResp, err := guestClient.Get(started.LocalURL + "/api/share/snapshot")
	if err != nil {
		t.Fatalf("GET /api/share/snapshot failed: %v", err)
	}
	defer snapResp.Body.Close()
	if snapResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /api/share/snapshot, got %d", snapResp.StatusCode)
	}

	// Host desktop Snapshot works cleanly with enrichment wired in.
	hostSnapRaw, err := d1.Snapshot(context.Background())
	if err != nil || !strings.Contains(hostSnapRaw, `"runtime"`) {
		t.Fatalf("desktop Snapshot failed: err=%v", err)
	}

	// Simulate cold restart and verify share + guest session survive!
	d1.closeWebConsole()
	d1.bridge.close()

	d2, _ := makeDesktop()
	t.Cleanup(func() {
		d2.closeWebConsole()
		d2.bridge.close()
	})
	if err := d2.startWebConsoleIfEnabled(); err != nil {
		t.Fatalf("startWebConsoleIfEnabled after restart failed: %v", err)
	}
	listAfterRestart := d2.GetWorkflowSharesState()
	if len(listAfterRestart.Shares) != 1 || listAfterRestart.TotalActiveSessions != 1 {
		t.Fatalf("expected 1 persisted share & 1 active session after restart, got %+v", listAfterRestart)
	}

	capResp2, err := guestClient.Get(started.LocalURL + "/api/share/capabilities")
	if err != nil {
		t.Fatalf("GET /api/share/capabilities after restart failed: %v", err)
	}
	_ = capResp2.Body.Close()
	if capResp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after restart with existing guest cookie, got %d", capResp2.StatusCode)
	}

	// Revoke via desktop method -> immediately invalidates guest session.
	revokedList, err := d2.RevokeWorkflowShare(created.ID)
	if err != nil || revokedList.TotalActiveSessions != 0 || revokedList.Shares[0].Status != "revoked" {
		t.Fatalf("RevokeWorkflowShare unexpected result: %+v err=%v", revokedList, err)
	}
	capResp3, err := guestClient.Get(started.LocalURL + "/api/share/capabilities")
	if err != nil {
		t.Fatalf("GET /api/share/capabilities after revoke failed: %v", err)
	}
	_ = capResp3.Body.Close()
	if capResp3.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 after RevokeWorkflowShare, got %d", capResp3.StatusCode)
	}

	// Delete via desktop method removes the entry.
	deletedList, err := d2.DeleteWorkflowShare(created.ID)
	if err != nil || len(deletedList.Shares) != 0 {
		t.Fatalf("DeleteWorkflowShare unexpected result: %+v err=%v", deletedList, err)
	}
}

type desktopTestRPCProvider struct {
	controller.ProviderClient
	rpcFn func(ctx context.Context, method, path string, body any, opts controller.ColabOneRPCOptions) (any, error)
}

func (p *desktopTestRPCProvider) RPC(ctx context.Context, method, path string, body any, opts controller.ColabOneRPCOptions) (any, error) {
	return p.rpcFn(ctx, method, path, body, opts)
}

func (p *desktopTestRPCProvider) Close() {}

func (p *desktopTestRPCProvider) CloseObserver(string, bool) {}

func (p *desktopTestRPCProvider) SessionsMap() map[string]map[string]any {
	return map[string]map[string]any{
		"ep-test": {"endpoint": "ep-test", "name": "ep-test"},
	}
}

func TestColabOneDesktopShareRuntimeOnlyMediaAndControllerGuards(t *testing.T) {
	ctx := context.Background()

	// 1. HEAD error responses on colabOneBridge must never write a text/plain or JSON body;
	// GET error responses must return JSON error.
	nilBridgeDesktop := &ColabOneDesktop{}
	headReq := httptest.NewRequest(http.MethodHead, "/api/share/outputs/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/media", nil)
	headRec := httptest.NewRecorder()
	writeColabOneMediaError(headRec, headReq, http.StatusServiceUnavailable, "控制器桥接未初始化")
	if headRec.Code != http.StatusServiceUnavailable || headRec.Body.Len() != 0 {
		t.Fatalf("HEAD media error expected 503 with empty body, got %d body=%q", headRec.Code, headRec.Body.String())
	}
	getReq := httptest.NewRequest(http.MethodGet, "/api/share/outputs/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/media", nil)
	getRec := httptest.NewRecorder()
	writeColabOneMediaError(getRec, getReq, http.StatusServiceUnavailable, "控制器桥接未初始化")
	if getRec.Code != http.StatusServiceUnavailable || !strings.Contains(getRec.Header().Get("Content-Type"), "application/json") || !strings.Contains(getRec.Body.String(), `"error"`) {
		t.Fatalf("GET media error expected 503 JSON, got %d ct=%q body=%q", getRec.Code, getRec.Header().Get("Content-Type"), getRec.Body.String())
	}
	_ = nilBridgeDesktop

	// 2. Wire a real ColabOneController with mock RPC provider to verify RuntimeOnly media,
	// output_media.go fixes, runtime_only asset/delete, fashion_tryon gating, skip_auto_release,
	// and TTS preview skip_local_cache / >512 KiB has_ref_audio cache poisoning fix.
	cfgDir := t.TempDir()
	memVault := vault.NewMemoryVault()
	_ = memVault.Set("google_profile", map[string]any{"sub": "sub-test", "email": "host@example.com"})
	_ = memVault.Set("colab", map[string]any{"token": "tok"})

	const (
		liveOutID     = "11111111111111111111111111111111"
		driveOnlyID   = "22222222222222222222222222222222"
		exportedOutID = "33333333333333333333333333333333"
		guestPrevID   = "44444444444444444444444444444444"
		refPrevID     = "55555555555555555555555555555555"
	)

	// Pre-seed drive_outputs.json on disk with both a Drive-only output and an exported live output.
	driveOutputsSeed := map[string]any{
		"version": 1,
		"accounts": map[string]any{
			"sub:sub-test": map[string]any{
				driveOnlyID: map[string]any{
					"id":            driveOnlyID,
					"workflow_name": "Drive Only",
					"kind":          "video",
					"filename":      "archived.mp4",
					"extension":     "mp4",
					"mime":          "video/mp4",
					"size":          1024,
					"drive_file_id": "drive-file-only-1",
					"drive_synced":  true,
					"drive_exported": true,
				},
				exportedOutID: map[string]any{
					"id":             exportedOutID,
					"workflow_name":  "Exported Live",
					"kind":           "video",
					"filename":       "exported.mp4",
					"extension":      "mp4",
					"mime":           "video/mp4",
					"size":           256,
					"drive_file_id":  "drive-file-exported-2",
					"drive_synced":   true,
					"drive_exported": true,
				},
			},
		},
	}
	seedBytes, _ := json.Marshal(driveOutputsSeed)
	if err := os.WriteFile(filepath.Join(cfgDir, "drive_outputs.json"), seedBytes, 0o600); err != nil {
		t.Fatalf("write drive_outputs.json: %v", err)
	}

	fullVideo := bytes.Repeat([]byte("0123456789abcdef"), 10) // 160 bytes (> stale meta size 64)
	ttsAudio := []byte("RIFFmockwavdata1234567890")
	ttsSum := sha256.Sum256(ttsAudio)
	ttsSHA := hex.EncodeToString(ttsSum[:])

	var (
		mu             sync.Mutex
		lastRunRPCBody map[string]any
		caps           = []string{"public_api", "comfyui", "comfyui_tts_preview", "comfyui_longcat_native"}
	)
	prov := &desktopTestRPCProvider{
		rpcFn: func(ctx context.Context, method, path string, body any, opts controller.ColabOneRPCOptions) (any, error) {
			mu.Lock()
			curCaps := append([]string(nil), caps...)
			mu.Unlock()
			switch {
			case method == "GET" && path == "snapshot":
				return map[string]any{
					"runtime": map[string]any{
						"state":        "ready",
						"id":           "ep-test",
						"protocol":     7,
						"capabilities": curCaps,
						"tasks":        []any{},
					},
					"models":        []any{},
					"workflows":     []any{},
					"comfy_outputs": []any{},
				}, nil
			case method == "GET" && strings.HasPrefix(path, "comfyui/outputs/"+liveOutID+"/asset"):
				return map[string]any{
					"data":      base64.StdEncoding.EncodeToString(fullVideo),
					"size":      len(fullVideo),
					"offset":    0,
					"extension": "mp4",
					"filename":  "live.mp4",
					"source":    "runtime",
				}, nil
			case method == "GET" && strings.HasPrefix(path, "comfyui/outputs/"+driveOnlyID+"/asset"):
				return nil, controller.ErrNotFound
			case method == "DELETE" && path == "comfyui/outputs/"+exportedOutID:
				return map[string]any{"ok": true}, nil
			case method == "POST" && path == "comfyui/run":
				if m, ok := body.(map[string]any); ok {
					mu.Lock()
					lastRunRPCBody = m
					mu.Unlock()
				}
				return map[string]any{"id": "99999999999999999999999999999999", "kind": "comfyui_run", "state": "running"}, nil
			case method == "POST" && path == "comfyui/tts-preview":
				bMap, _ := body.(map[string]any)
				if _, hasRef := bMap["ref_audio_input_id"]; hasRef {
					// Simulate >512 KiB cached hit (cached=true, data="") with ref_audio.
					return map[string]any{
						"cached":        true,
						"preview_id":    refPrevID,
						"engine_id":     "voxcpm2",
						"voice_preset":  "shen_zhiyi",
						"has_ref_audio": true,
					}, nil
				}
				return map[string]any{
					"cached":       true,
					"preview_id":   guestPrevID,
					"engine_id":    "voxcpm2",
					"voice_preset": "shen_zhiyi",
					"data":         base64.StdEncoding.EncodeToString(ttsAudio),
				}, nil
			case method == "GET" && strings.HasPrefix(path, "comfyui/tts-previews/"+refPrevID):
				return map[string]any{
					"preview_id":    refPrevID,
					"engine_id":     "voxcpm2",
					"voice_preset":  "shen_zhiyi",
					"has_ref_audio": true,
					"data":          base64.StdEncoding.EncodeToString(ttsAudio),
					"size":          len(ttsAudio),
					"sha256":        ttsSHA,
					"offset":        0,
					"chunk_size":    len(ttsAudio),
					"has_more":      false,
				}, nil
			default:
				return map[string]any{"ok": true}, nil
			}
		},
	}

	ctrl := controller.NewColabOneController(controller.ColabOneControllerConfig{
		Root:           colabOneRootDir(),
		ConfigDir:      cfgDir,
		Background:     false,
		Vault:          &wireVaultAdapter{v: memVault},
		Provider:       prov,
		Models:         wireModelsAdapter{},
		Comfy:          wireComfyAdapter{},
		PublicAPI:      wirePublicAPIAdapter{},
		AssignProxy:    wireAssignProxyAdapter{},
		Shortcuts:      wireShortcutsAdapter{},
		ServiceBundler: &wireControllerBundlerAdapter{b: servicebundle.NewEmbeddedBundle()},
		Location:       wireLocationAdapter{},
	})
	t.Cleanup(func() { ctrl.Close() })

	// (a) Stopped runtime + RuntimeOnly: 503 guest-safe (zero Google Drive leak) & runtime_only commands fail cleanly.
	ctrl.Update(map[string]any{"runtime": map[string]any{"state": "stopped"}})
	stoppedReq := httptest.NewRequest(http.MethodGet, "/api/share/outputs/"+driveOnlyID+"/media", nil)
	stoppedRec := httptest.NewRecorder()
	ctrl.ServeOutputMediaHTTPWithOptions(ctx, stoppedRec, stoppedReq, driveOnlyID, controller.ColabOneOutputMediaOptions{RuntimeOnly: true})
	if stoppedRec.Code != http.StatusServiceUnavailable || strings.Contains(stoppedRec.Body.String(), "Google 云盘") {
		t.Fatalf("RuntimeOnly stopped runtime expected 503 guest-safe, got %d body=%s", stoppedRec.Code, stoppedRec.Body.String())
	}
	if _, err := ctrl.Command(ctx, "comfyui.output.asset", map[string]any{"id": driveOnlyID, "runtime_only": true}); err == nil || !strings.Contains(err.Error(), "当前算力服务暂未就绪") {
		t.Fatalf("runtime_only output.asset while stopped expected guest-safe error, got %v", err)
	}
	if _, err := ctrl.Command(ctx, "comfyui.output.delete", map[string]any{"id": driveOnlyID, "runtime_only": true}); err == nil || !strings.Contains(err.Error(), "当前算力服务暂未就绪") {
		t.Fatalf("runtime_only output.delete while stopped expected guest-safe error, got %v", err)
	}

	// (b) Ready runtime: RuntimeOnly serves live output with actualSize > meta.Size completely (non-Range GET fix!),
	// carries discovered actualSize on 416 Range, and returns 404 on Drive-only output!
	ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state":        "ready",
			"id":           "ep-test",
			"protocol":     7,
			"capabilities": caps,
		},
		"comfy_outputs": []any{
			map[string]any{
				"id":        liveOutID,
				"extension": "mp4",
				"size":      64, // intentionally smaller than actual 160-byte remote file!
				"storage":   "runtime",
			},
			map[string]any{
				"id":        exportedOutID,
				"extension": "mp4",
				"size":      256,
				"storage":   "runtime",
			},
		},
	})

	liveReq := httptest.NewRequest(http.MethodGet, "/api/share/outputs/"+liveOutID+"/media", nil)
	liveRec := httptest.NewRecorder()
	ctrl.ServeOutputMediaHTTPWithOptions(ctx, liveRec, liveReq, liveOutID, controller.ColabOneOutputMediaOptions{RuntimeOnly: true})
	if liveRec.Code != http.StatusOK || !bytes.Equal(liveRec.Body.Bytes(), fullVideo) {
		t.Fatalf("non-Range GET with actualSize > meta.Size expected full %d bytes, got code=%d len=%d", len(fullVideo), liveRec.Code, liveRec.Body.Len())
	}

	driveOnlyReq := httptest.NewRequest(http.MethodGet, "/api/share/outputs/"+driveOnlyID+"/media", nil)
	driveOnlyRec := httptest.NewRecorder()
	ctrl.ServeOutputMediaHTTPWithOptions(ctx, driveOnlyRec, driveOnlyReq, driveOnlyID, controller.ColabOneOutputMediaOptions{RuntimeOnly: true})
	if driveOnlyRec.Code != http.StatusNotFound || strings.Contains(driveOnlyRec.Body.String(), "Google 云盘") {
		t.Fatalf("RuntimeOnly on Drive-only output expected 404 guest-safe, got %d body=%s", driveOnlyRec.Code, driveOnlyRec.Body.String())
	}

	// (c) runtime_only comfyui.output.delete preserves host's exported Drive record in drive_outputs.json!
	if _, err := ctrl.Command(ctx, "comfyui.output.delete", map[string]any{"id": exportedOutID, "runtime_only": true}); err != nil {
		t.Fatalf("runtime_only output.delete failed: %v", err)
	}
	diskAfterDel, err := os.ReadFile(filepath.Join(cfgDir, "drive_outputs.json"))
	if err != nil || !strings.Contains(string(diskAfterDel), "drive-file-exported-2") {
		t.Fatalf("runtime_only delete must preserve host exported Drive record, err=%v content=%s", err, string(diskAfterDel))
	}

	// (d) fashion_tryon capability gating + skip_auto_release capability forwarding.
	if _, err := ctrl.Command(ctx, "comfyui.run", map[string]any{"workflow_id": "fashion-tryon-h3"}); err == nil || !strings.Contains(err.Error(), "AI 模特换装棚拍") {
		t.Fatalf("fashion-tryon-h3 without comfyui_fashion_tryon capability expected upgrade hint, got %v", err)
	}
	if _, err := ctrl.Command(ctx, "comfyui.run", map[string]any{
		"workflow_id":       "digital-human-longcat",
		"skip_auto_release": true,
	}); err != nil {
		t.Fatalf("comfyui.run without comfyui_share_guard failed: %v", err)
	}
	mu.Lock()
	if _, forwarded := lastRunRPCBody["skip_auto_release"]; forwarded {
		t.Fatalf("skip_auto_release must NOT be forwarded when remote lacks comfyui_share_guard")
	}
	caps = []string{"public_api", "comfyui", "comfyui_tts_preview", "comfyui_fashion_tryon", "comfyui_share_guard"}
	mu.Unlock()
	ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state":        "ready",
			"id":           "ep-test",
			"protocol":     7,
			"capabilities": caps,
		},
	})
	if _, err := ctrl.Command(ctx, "comfyui.run", map[string]any{
		"workflow_id":       "fashion-tryon-h3",
		"skip_auto_release": true,
	}); err != nil {
		t.Fatalf("fashion-tryon-h3 with capabilities failed: %v", err)
	}
	mu.Lock()
	if lastRunRPCBody["skip_auto_release"] != true {
		t.Fatalf("skip_auto_release should be forwarded when remote advertises comfyui_share_guard, got %+v", lastRunRPCBody)
	}
	mu.Unlock()

	// (e) TTS preview: skip_local_cache=true writes nothing to disk; >512 KiB cached hit with has_ref_audio
	// does NOT write a preset-only requestKey into tts-preview-cache/index.json!
	if _, err := ctrl.Command(ctx, "comfyui.tts.preview", map[string]any{
		"engine_id":        "voxcpm2",
		"voice_preset":     "shen_zhiyi",
		"skip_local_cache": true,
	}); err != nil {
		t.Fatalf("guest skip_local_cache tts.preview failed: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(cfgDir, "tts-preview-cache", guestPrevID+".wav")); !os.IsNotExist(statErr) {
		t.Fatalf("skip_local_cache=true must not write guest preview audio to host disk cache")
	}

	if _, err := ctrl.Command(ctx, "comfyui.tts.preview", map[string]any{
		"engine_id":          "voxcpm2",
		"voice_preset":       "shen_zhiyi",
		"ref_audio_input_id": strings.Repeat("a", 32),
	}); err != nil {
		t.Fatalf("host >512 KiB cached ref_audio tts.preview failed: %v", err)
	}
	if _, err := ctrl.Command(ctx, "comfyui.tts.preview.asset", map[string]any{
		"preview_id": refPrevID,
	}); err != nil {
		t.Fatalf("host tts.preview.asset failed: %v", err)
	}
	idxRaw, err := os.ReadFile(filepath.Join(cfgDir, "tts-preview-cache", "index.json"))
	if err != nil {
		t.Fatalf("read tts-preview-cache/index.json: %v", err)
	}
	var idxDoc struct {
		ByPreviewID  map[string]any    `json:"by_preview_id"`
		ByRequestKey map[string]string `json:"by_request_key"`
	}
	if err := json.Unmarshal(idxRaw, &idxDoc); err != nil {
		t.Fatalf("parse tts index.json: %v", err)
	}
	if _, saved := idxDoc.ByPreviewID[refPrevID]; !saved {
		t.Fatalf("expected refPrevID saved in ByPreviewID, got %+v", idxDoc.ByPreviewID)
	}
	if len(idxDoc.ByRequestKey) != 0 {
		t.Fatalf("has_ref_audio >512 KiB preview must NOT poison preset-only ByRequestKey, got %+v", idxDoc.ByRequestKey)
	}
}

func TestColabOneDesktopSnapshotEnrichmentAndCommandHooks(t *testing.T) {
	dir := t.TempDir()
	memVault := vault.NewMemoryVault()

	const (
		guestTaskID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		guestOutID  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		hostOutID   = "cccccccccccccccccccccccccccccccc"
	)

	var (
		mu                sync.Mutex
		mediaOptsObserved []controller.ColabOneOutputMediaOptions
		outputs           = []map[string]any{
			{
				"id":          guestOutID,
				"task_id":     guestTaskID,
				"workflow_id": "digital-human-longcat",
				"filename":    "guest.mp4",
				"kind":        "video",
				"created_at":  float64(1765000100),
				"storage":     "runtime",
			},
		}
		tasks = []map[string]any{
			{
				"id":          guestTaskID,
				"kind":        "comfyui_run",
				"model_id":    "digital-human-longcat",
				"state":       "completed",
				"created_at":  float64(1765000090),
				"finished_at": float64(1765000105),
			},
		}
	)

	prov := &desktopTestRPCProvider{
		rpcFn: func(ctx context.Context, method, path string, body any, opts controller.ColabOneRPCOptions) (any, error) {
			mu.Lock()
			defer mu.Unlock()
			switch {
			case method == "GET" && path == "snapshot":
				return map[string]any{
					"runtime": map[string]any{
						"state":        "ready",
						"id":           "rt-desktop-1",
						"protocol":     7,
						"capabilities": []string{"public_api", "comfyui", "comfyui_share_guard", "comfyui_longcat_native"},
						"tasks":        tasks,
					},
					"models":        []any{},
					"workflows":     []any{},
					"comfy_outputs": outputs,
				}, nil
			case method == "POST" && path == "comfyui/run":
				return map[string]any{
					"id":         guestTaskID,
					"kind":       "comfyui_run",
					"model_id":   "digital-human-longcat",
					"state":      "running",
					"created_at": float64(1765000090),
				}, nil
			case method == "DELETE" && strings.HasPrefix(path, "comfyui/outputs/"):
				return map[string]any{"ok": true}, nil
			default:
				return map[string]any{"ok": true}, nil
			}
		},
	}

	var ctrl *controller.ColabOneController
	bridge := &colabOneBridge{
		vault: memVault,
		factory: func() *controller.ColabOneController {
			c := controller.NewColabOneController(controller.ColabOneControllerConfig{
				Root:           colabOneRootDir(),
				ConfigDir:      dir,
				Background:     false,
				Vault:          &wireVaultAdapter{v: memVault},
				Provider:       prov,
				Models:         wireModelsAdapter{},
				Comfy:          wireComfyAdapter{},
				PublicAPI:      wirePublicAPIAdapter{},
				AssignProxy:    wireAssignProxyAdapter{},
				Shortcuts:      wireShortcutsAdapter{},
				ServiceBundler: &wireControllerBundlerAdapter{b: servicebundle.NewEmbeddedBundle()},
				Location:       wireLocationAdapter{},
			})
			c.Update(map[string]any{
				"runtime": map[string]any{
					"state":        "ready",
					"id":           "rt-desktop-1",
					"protocol":     7,
					"capabilities": []string{"public_api", "comfyui", "comfyui_share_guard", "comfyui_longcat_native"},
					"tasks":        tasks,
				},
				"comfy_outputs": outputs,
			})
			ctrl = c
			return c
		},
		serveMediaWithOptsFn: func(ctx context.Context, rw http.ResponseWriter, r *http.Request, id string, opts controller.ColabOneOutputMediaOptions) {
			mu.Lock()
			mediaOptsObserved = append(mediaOptsObserved, opts)
			mu.Unlock()
			rw.Header().Set("Content-Type", "video/mp4")
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte("ok"))
		},
	}

	d := &ColabOneDesktop{bridge: bridge}
	fakeDist := fstest.MapFS{"index.html": {Data: []byte("<html>OK</html>")}}
	d.initWebConsoleWithFS(dir, memVault, func() fs.FS { return fakeDist })
	t.Cleanup(func() {
		d.closeWebConsole()
		d.bridge.close()
	})
	_ = ctrl

	enabled := true
	var started webconsole.ColabOneWebConsoleState
	var startErr error
	for port := 43320; port < 43340; port++ {
		p := port
		started, startErr = d.UpdateWebConsole(webconsole.ColabOneWebConsoleUpdateRequest{
			Enabled: &enabled,
			Port:    &p,
		})
		if startErr == nil {
			break
		}
	}
	if startErr != nil || !started.Running {
		t.Fatalf("start web console failed: %v", startErr)
	}

	share, err := d.CreateWorkflowShare(webconsole.ColabOneWorkflowShareCreateRequest{
		Label:       "Desktop Client A",
		Title:       "Desktop Share",
		WorkflowIDs: []string{"digital-human-longcat"},
		TTLMode:     webconsole.WorkflowShareTTLForever,
	})
	if err != nil {
		t.Fatalf("CreateWorkflowShare: %v", err)
	}

	jar, _ := cookiejar.New(nil)
	guestClient := &http.Client{Jar: jar}
	resp, err := guestClient.Get(share.URL)
	if err != nil {
		t.Fatalf("activate share: %v", err)
	}
	_ = resp.Body.Close()

	// Guest runs workflow -> tracked under share.
	runReq, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, started.LocalURL+"/api/share/command",
		strings.NewReader(`{"action":"comfyui.run","payload":{"workflow_id":"digital-human-longcat"}}`))
	runReq.Header.Set("Content-Type", "application/json")
	runReq.Header.Set(webconsole.ClientHeaderName, webconsole.WorkflowShareClientHeaderValue)
	runResp, err := guestClient.Do(runReq)
	if err != nil || runResp.StatusCode != http.StatusOK {
		t.Fatalf("guest comfyui.run failed: err=%v resp=%v", err, runResp)
	}
	_ = runResp.Body.Close()

	// Desktop Snapshot enriches comfy_outputs with share_id and share_label!
	snapRaw, err := d.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("desktop Snapshot failed: %v", err)
	}
	var hostSnap map[string]any
	if err := json.Unmarshal([]byte(snapRaw), &hostSnap); err != nil {
		t.Fatalf("decode desktop Snapshot: %v", err)
	}
	outs := hostSnap["comfy_outputs"].([]any)
	firstOut := outs[0].(map[string]any)
	if firstOut["share_id"] != share.ID || firstOut["share_label"] != "Desktop Client A" {
		t.Fatalf("expected desktop Snapshot share_id=%q share_label=%q, got %+v", share.ID, "Desktop Client A", firstOut)
	}

	// Guest media request routes through serveOutputMediaWithOptions with RuntimeOnly=true!
	mediaResp, err := guestClient.Get(started.LocalURL + "/api/share/outputs/" + guestOutID + "/media")
	if err != nil || mediaResp.StatusCode != http.StatusOK {
		t.Fatalf("guest media request failed: err=%v resp=%v", err, mediaResp)
	}
	_ = mediaResp.Body.Close()
	mu.Lock()
	if len(mediaOptsObserved) != 1 || !mediaOptsObserved[0].RuntimeOnly {
		t.Fatalf("expected guest media request to enforce RuntimeOnly=true, got %+v", mediaOptsObserved)
	}
	mu.Unlock()

	// Desktop Command("comfyui.run") triggers NotifyHostWorkflowRunStarting & NotifyHostWorkflowRunStarted,
	// so a legacy host output without task_id produced immediately after is NOT misattributed to the guest share!
	if _, err := d.Command(context.Background(), ColabOneCommandRequest{
		Action:  "comfyui.run",
		Payload: map[string]any{"workflow_id": "digital-human-longcat"},
	}); err != nil {
		t.Fatalf("desktop Command comfyui.run failed: %v", err)
	}
	mu.Lock()
	outputs = append(outputs, map[string]any{
		"id":          hostOutID,
		"workflow_id": "digital-human-longcat",
		"filename":    "host_after.mp4",
		"kind":        "video",
		"created_at":  float64(1765000106),
		"storage":     "runtime",
	})
	ctrl.Update(map[string]any{"comfy_outputs": outputs})
	mu.Unlock()
	snapRaw2, err := d.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("desktop Snapshot 2 failed: %v", err)
	}
	var hostSnap2 map[string]any
	if err := json.Unmarshal([]byte(snapRaw2), &hostSnap2); err != nil {
		t.Fatalf("decode desktop Snapshot 2: %v", err)
	}
	for _, rawO := range hostSnap2["comfy_outputs"].([]any) {
		om := rawO.(map[string]any)
		if om["id"] == hostOutID && om["share_id"] != nil {
			t.Fatalf("host output after desktop comfyui.run must NOT be attributed to guest share: %+v", om)
		}
	}

	// Desktop Command("comfyui.output.delete") forgets ownership of guestOutID!
	if _, err := d.Command(context.Background(), ColabOneCommandRequest{
		Action:  "comfyui.output.delete",
		Payload: map[string]any{"id": guestOutID},
	}); err != nil {
		t.Fatalf("desktop Command comfyui.output.delete failed: %v", err)
	}
	for _, s := range d.GetWorkflowSharesState().Shares {
		if s.ID == share.ID && s.OutputCount != 0 {
			t.Fatalf("expected OutputCount 0 after desktop output delete, got %d", s.OutputCount)
		}
	}
}
