package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luolongfei/ColabOne/console/internal/migration"
	"github.com/luolongfei/ColabOne/console/internal/notifier"
)

func makeTinyTestPNGBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 24, 24))
	for y := 0; y < 24; y++ {
		for x := 0; x < 24; x++ {
			img.Set(x, y, color.RGBA{R: 32, G: 148, B: 250, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}

func waitForCondition(t *testing.T, timeout time.Duration, check func() bool, desc string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

func TestControllerNotificationSettingsVaultIsolationTestAndMigration(t *testing.T) {
	var (
		sysMu   sync.Mutex
		sysReqs []notifier.ColabOneSystemNotificationRequest
	)
	var (
		tgMu   sync.Mutex
		tgHits int
	)
	tgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tgMu.Lock()
		tgHits++
		tgMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer tgSrv.Close()

	var (
		barkMu   sync.Mutex
		barkHits int
		barkBody string
	)
	barkSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		barkMu.Lock()
		barkHits++
		barkBody = string(raw)
		barkMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"message":"success"}`))
	}))
	defer barkSrv.Close()

	dir := t.TempDir()
	vault := NewColabOneMemoryVault()
	ctrl := NewColabOneController(ColabOneControllerConfig{
		Root:           dir,
		ConfigDir:      dir,
		Vault:          vault,
		ServiceBundler: defaultMockBundler(),
		SystemNotifyHooks: ColabOneSystemNotificationHooks{
			Supported:       func() bool { return true },
			IsWindowFocused: func() bool { return true }, // focused! test must still bypass
			Send: func(ctx context.Context, req notifier.ColabOneSystemNotificationRequest) error {
				sysMu.Lock()
				sysReqs = append(sysReqs, req)
				sysMu.Unlock()
				return nil
			},
		},
	})
	defer ctrl.Close()

	// Default snapshot has system enabled=true, others disabled=false.
	snap0 := ctrl.Snapshot()
	ns0 := asMap(snap0["notification_settings"])
	if ns0 == nil || !asBool(asMap(ns0["system"])["enabled"]) || !asBool(asMap(ns0["system"])["supported"]) {
		t.Fatalf("unexpected default notification_settings: %#v", ns0)
	}
	if asBool(asMap(ns0["telegram"])["enabled"]) || asBool(asMap(ns0["bark"])["enabled"]) || asBool(asMap(ns0["email"])["enabled"]) {
		t.Fatalf("remote channels must default disabled: %#v", ns0)
	}
	if _, leaked := asMap(snap0["settings"])["notifications"]; leaked {
		t.Fatalf("raw notifications map must not be duplicated under snapshot.settings")
	}

	// Save Telegram + Bark + Email credentials via notifications.save.
	saveRes, err := ctrl.Command(context.Background(), "notifications.save", map[string]any{
		"notify_gpu":   true,
		"notify_task":  true,
		"notify_alert": true,
		"telegram": map[string]any{
			"enabled":            true,
			"bot_token":          "123456789:SUPER_SECRET_TG_TOKEN_XYZ",
			"chat_id":            "99887766",
			"api_base_url":       tgSrv.URL,
			"send_preview_image": true,
		},
		"bark": map[string]any{
			"enabled":      true,
			"endpoint_url": barkSrv.URL + "/SECRET_BARK_KEY_999",
			"group":        "ColabOne",
		},
		"email": map[string]any{
			"enabled":      false,
			"smtp_host":    "smtp.example.com",
			"smtp_port":    465,
			"security":     "tls",
			"username":     "sender@example.com",
			"password":     "SUPER_SECRET_SMTP_PASS",
			"to_addresses": "dest@example.com",
		},
	})
	if err != nil || asMap(saveRes)["ok"] != true {
		t.Fatalf("notifications.save failed: err=%v res=%#v", err, saveRes)
	}

	// Verify secrets NEVER leak into settings.json or Snapshot JSON.
	rawSettingsBytes, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	rawSettingsStr := string(rawSettingsBytes)
	snapJSONBytes, _ := json.Marshal(ctrl.Snapshot())
	snapJSONStr := string(snapJSONBytes)
	for _, secret := range []string{"SUPER_SECRET_TG_TOKEN_XYZ", "SECRET_BARK_KEY_999", "SUPER_SECRET_SMTP_PASS"} {
		if strings.Contains(rawSettingsStr, secret) {
			t.Fatalf("secret %q leaked into settings.json", secret)
		}
		if strings.Contains(snapJSONStr, secret) {
			t.Fatalf("secret %q leaked into Snapshot JSON", secret)
		}
	}

	// Verify notifications.secret reveals allowed secret fields and rejects unknown fields.
	secRes, err := ctrl.Command(context.Background(), "notifications.secret", map[string]any{"field": notifier.SecretTelegramBotToken})
	if err != nil || asString(asMap(secRes)["value"]) != "123456789:SUPER_SECRET_TG_TOKEN_XYZ" {
		t.Fatalf("notifications.secret telegram_bot_token failed: err=%v res=%#v", err, secRes)
	}
	if _, err := ctrl.Command(context.Background(), "notifications.secret", map[string]any{"field": "invalid_key"}); err == nil {
		t.Fatal("expected invalid notification secret field to error")
	}

	// Verify synchronous TestChannel works for system, telegram, and bark (even while window is focused!).
	for _, ch := range []string{"system", "telegram", "bark"} {
		if _, err := ctrl.Command(context.Background(), "notifications.test", map[string]any{"channel": ch}); err != nil {
			t.Fatalf("notifications.test %s failed: %v", ch, err)
		}
	}
	sysMu.Lock()
	gotSys := len(sysReqs)
	sysMu.Unlock()
	if gotSys != 1 {
		t.Fatalf("expected 1 system test notification, got %d", gotSys)
	}
	tgMu.Lock()
	gotTG := tgHits
	tgMu.Unlock()
	if gotTG != 1 {
		t.Fatalf("expected 1 telegram test hit, got %d", gotTG)
	}
	barkMu.Lock()
	gotBark := barkHits
	gotBarkBody := barkBody
	barkMu.Unlock()
	if gotBark != 1 || !strings.Contains(gotBarkBody, "SECRET_BARK_KEY_999") {
		t.Fatalf("expected 1 bark test hit with device_key, hits=%d body=%q", gotBark, gotBarkBody)
	}

	// Export and import migration payload preserves both public notification settings and encrypted vault secrets.
	exported, err := ctrl.ExportMigrationPayload([]string{migration.ModuleCredentials, migration.ModuleSettings}, "0.0.0-test", "linux", nil)
	if err != nil {
		t.Fatalf("ExportMigrationPayload failed: %v", err)
	}
	if exported.Credentials == nil || len(exported.Credentials.Notifications) == 0 {
		t.Fatalf("expected exported credentials to carry notification secrets")
	}

	destDir := t.TempDir()
	destVault := NewColabOneMemoryVault()
	destCtrl := NewColabOneController(ColabOneControllerConfig{
		Root:           destDir,
		ConfigDir:      destDir,
		Vault:          destVault,
		ServiceBundler: defaultMockBundler(),
	})
	defer destCtrl.Close()
	if _, err := destCtrl.ApplyMigrationPayload(context.Background(), exported, []string{migration.ModuleCredentials, migration.ModuleSettings}); err != nil {
		t.Fatalf("ApplyMigrationPayload failed: %v", err)
	}
	destSnap := destCtrl.Snapshot()
	destTG := asMap(asMap(destSnap["notification_settings"])["telegram"])
	if !asBool(destTG["enabled"]) || !asBool(destTG["has_bot_token"]) || asString(destTG["chat_id"]) != "99887766" {
		t.Fatalf("migrated telegram notification settings mismatch: %#v", destTG)
	}
	destBarkSec, err := destCtrl.Command(context.Background(), "notifications.secret", map[string]any{"field": notifier.SecretBarkDeviceKey})
	if err != nil || asString(asMap(destBarkSec)["value"]) != "SECRET_BARK_KEY_999" {
		t.Fatalf("migrated bark_device_key mismatch: err=%v res=%#v", err, destBarkSec)
	}
}

func TestControllerNotificationTriggersGPUOutputTasksAndAuthCooldown(t *testing.T) {
	h := newTestHarness(t, false)
	h.ctrl.mu.Lock()
	h.ctrl.settings["auto_drive"] = false
	h.ctrl.mu.Unlock()

	var (
		mu      sync.Mutex
		sysReqs []notifier.ColabOneSystemNotificationRequest
	)
	h.ctrl.SetSystemNotificationHooks(ColabOneSystemNotificationHooks{
		Supported:       func() bool { return true },
		IsWindowFocused: func() bool { return false },
		Send: func(ctx context.Context, req notifier.ColabOneSystemNotificationRequest) error {
			mu.Lock()
			sysReqs = append(sysReqs, req)
			mu.Unlock()
			return nil
		},
	})
	countByPrefix := func(prefix string) int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, r := range sysReqs {
			if strings.HasPrefix(r.ID, prefix) {
				n++
			}
		}
		return n
	}
	findByPrefix := func(prefix string) *notifier.ColabOneSystemNotificationRequest {
		mu.Lock()
		defer mu.Unlock()
		for i := range sysReqs {
			if strings.HasPrefix(sysReqs[i].ID, prefix) {
				cp := sysReqs[i]
				return &cp
			}
		}
		return nil
	}

	// 1. GPU allocation failure triggers alert notification.
	h.provider.startFn = func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
		if !opts.UnlimitedAttempts {
			t.Errorf("expected default UnlimitedAttempts=true in provider.Start, got false (MaxAttempts=%d)", opts.MaxAttempts)
		}
		return nil, errors.New("Google Colab 返回 HTTP 412：活跃实例达上限")
	}
	if err := h.ctrl.Start(context.Background(), false, "G4"); err == nil {
		t.Fatal("expected start failure")
	}
	waitForCondition(t, 2*time.Second, func() bool { return countByPrefix("gpu-alloc-failed-") == 1 }, "gpu allocation failed notification")

	// 2. GPU allocation success triggers gpu.allocated notification.
	pngBytes := makeTinyTestPNGBytes(t)
	outID := "0123456789abcdef0123456789abcdef"
	h.provider.startFn = func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
		h.provider.addManagedSession("ep-notify-1", gpu, "STANDARD", true)
		if opts.OnRetryState != nil {
			opts.OnRetryState(map[string]any{"attempt": 4, "waiting_retry": false, "unlimited_attempts": true})
		}
		return map[string]any{
			"endpoint": "ep-notify-1",
			"location": map[string]any{"city": "Council Bluffs", "country": "US"},
		}, nil
	}
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if path == "snapshot" || path == "status" {
			return map[string]any{
				"runtime": map[string]any{
					"state": "ready",
					"id":    "ep-notify-1",
					"gpu":   map[string]any{"name": "G4"},
					"tasks": []any{
						// Historical completed task on initial connect MUST NOT trigger stale notification.
						map[string]any{"id": "stale-task-1", "kind": "comfyui_run", "state": "completed", "message": "old"},
					},
				},
				"comfy_outputs": []any{},
			}, nil
		}
		if strings.HasPrefix(path, "comfyui/outputs/"+outID+"/asset") {
			return map[string]any{
				"id":       outID,
				"filename": "artwork.png",
				"mime":     "image/png",
				"size":     len(pngBytes),
				"offset":   0,
				"eof":      true,
				"data":     base64.StdEncoding.EncodeToString(pngBytes),
			}, nil
		}
		return map[string]any{"ok": true}, nil
	}
	if err := h.ctrl.Start(context.Background(), false, "G4"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	waitForCondition(t, 2*time.Second, func() bool { return countByPrefix("gpu-allocated-ep-notify-1") == 1 }, "gpu allocated notification")
	if countByPrefix("output-ready-stale-task-1") != 0 {
		t.Fatal("historical completed task on initial connect must not trigger notification")
	}

	// 3. Active ComfyUI workflow transitions running -> completed and attaches preview image!
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state": "ready",
			"id":    "ep-notify-1",
			"gpu":   map[string]any{"name": "G4"},
			"tasks": []any{
				map[string]any{"id": "wf-task-42", "kind": "comfyui_run", "state": "running", "message": "生成中"},
			},
		},
	})
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state": "ready",
			"id":    "ep-notify-1",
			"gpu":   map[string]any{"name": "G4"},
			"tasks": []any{
				map[string]any{"id": "wf-task-42", "kind": "comfyui_run", "state": "completed", "message": "已完成"},
			},
		},
		"comfy_outputs": []any{
			map[string]any{
				"id":            outID,
				"task_id":       "wf-task-42",
				"workflow_name": "人像精修",
				"filename":      "artwork.png",
				"mime":          "image/png",
				"extension":     "png",
				"size":          len(pngBytes),
			},
		},
	})
	waitForCondition(t, 3*time.Second, func() bool { return countByPrefix("output-ready-wf-task-42") == 1 }, "output ready notification")
	outNotif := findByPrefix("output-ready-wf-task-42")
	if outNotif == nil || !outNotif.OpenOutputs || outNotif.NavigateTab != "comfy" || len(outNotif.ImageBytes) == 0 {
		t.Fatalf("expected output notification with preview image and comfy navigation, got %+v", outNotif)
	}

	// 4. Runtime ready -> disconnected fires warning alert.
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state": "disconnected",
			"id":    "ep-notify-1",
			"gpu":   map[string]any{"name": "G4"},
		},
	})
	waitForCondition(t, 2*time.Second, func() bool { return countByPrefix("gpu-disconnected-ep-notify-1") == 1 }, "gpu disconnected notification")

	// 5. Auth intervention cooldown prevents duplicate alerts.
	h.ctrl.notifyAuthInterventionRequired("google_account", "Google 登录已过期，请重新登录")
	h.ctrl.notifyAuthInterventionRequired("google_account", "Google 登录已过期，请重新登录")
	waitForCondition(t, 2*time.Second, func() bool { return countByPrefix("auth-required-google_account") == 1 }, "auth required notification")
	time.Sleep(80 * time.Millisecond)
	if got := countByPrefix("auth-required-google_account"); got != 1 {
		t.Fatalf("expected auth cooldown to suppress duplicate notification, got %d", got)
	}

	// 6. Successful Account() refresh with logged_in=true clears google_account cooldown;
	// account switch clears all auth cooldown entries too.
	acctGen, acctCreds := h.ctrl.snapshotAccountGuard()
	if !h.ctrl.applyAccountRefreshIfCurrent(acctGen, acctCreds, map[string]any{"logged_in": true, "email": "user@example.com"}, nowUnixFloat()) {
		t.Fatal("expected applyAccountRefreshIfCurrent to succeed")
	}
	h.ctrl.mu.Lock()
	_, stillCooling := h.ctrl.lastAuthNotifyAt["google_account"]
	h.ctrl.mu.Unlock()
	if stillCooling {
		t.Fatal("expected google_account auth cooldown cleared after logged_in=true Account() refresh")
	}
	h.ctrl.notifyAuthInterventionRequired("custom_scope", "需要重新授权")
	h.ctrl.resetAccountStateForSwitch(map[string]any{"logged_in": false}, false)
	h.ctrl.mu.Lock()
	leftAuthCooldown := len(h.ctrl.lastAuthNotifyAt)
	h.ctrl.mu.Unlock()
	if leftAuthCooldown != 0 {
		t.Fatalf("expected resetAccountStateForSwitch to clear lastAuthNotifyAt, got %d", leftAuthCooldown)
	}
}

func TestControllerNotificationMultiInstanceSwitchAndSecondaryEvents(t *testing.T) {
	h := newTestHarness(t, false)
	h.ctrl.mu.Lock()
	h.ctrl.settings["auto_drive"] = false
	h.ctrl.mu.Unlock()

	var (
		mu      sync.Mutex
		sysReqs []notifier.ColabOneSystemNotificationRequest
	)
	h.ctrl.SetSystemNotificationHooks(ColabOneSystemNotificationHooks{
		Supported:       func() bool { return true },
		IsWindowFocused: func() bool { return false },
		Send: func(ctx context.Context, req notifier.ColabOneSystemNotificationRequest) error {
			mu.Lock()
			sysReqs = append(sysReqs, req)
			mu.Unlock()
			return nil
		},
	})
	countByPrefix := func(prefix string) int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, r := range sysReqs {
			if strings.HasPrefix(r.ID, prefix) {
				n++
			}
		}
		return n
	}

	h.provider.addManagedSession("ep-A", "G4", "STANDARD", true)
	h.provider.addManagedSession("ep-B", "L4", "STANDARD", false)

	var rpcMu sync.Mutex
	epBTaskState := "completed"
	epBTaskID := "old-task-b"
	epBRefreshErr := error(nil)
	epBHeartbeatErr := error(nil)

	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		rpcMu.Lock()
		defer rpcMu.Unlock()
		if path == "snapshot" {
			ep := opts.Endpoint
			if ep == "" {
				ep = h.ctrl.activeEndpoint()
			}
			if ep == "ep-B" {
				if epBRefreshErr != nil {
					return nil, epBRefreshErr
				}
				return map[string]any{
					"runtime": map[string]any{
						"state": "ready",
						"id":    "ep-B",
						"gpu":   map[string]any{"name": "L4"},
						"tasks": []any{
							map[string]any{"id": epBTaskID, "kind": "comfyui_run", "state": epBTaskState, "message": "done"},
						},
					},
					"comfy_outputs": []any{},
				}, nil
			}
			return map[string]any{
				"runtime": map[string]any{
					"state": "ready",
					"id":    "ep-A",
					"gpu":   map[string]any{"name": "G4"},
					"tasks": []any{
						map[string]any{"id": "old-task-a", "kind": "comfyui_run", "state": "completed", "message": "done"},
					},
				},
				"comfy_outputs": []any{},
			}, nil
		}
		return map[string]any{"ok": true}, nil
	}
	h.provider.heartbeatFn = func(ctx context.Context, endpoint string) error {
		rpcMu.Lock()
		defer rpcMu.Unlock()
		if endpoint == "ep-B" {
			return epBHeartbeatErr
		}
		return nil
	}

	// Initial refreshes seed ep-A (active) and ep-B (secondary) without replaying historical completed tasks.
	if err := h.ctrl.RefreshRemote(context.Background(), "ep-A"); err != nil {
		t.Fatalf("RefreshRemote ep-A: %v", err)
	}
	if err := h.ctrl.RefreshRemote(context.Background(), "ep-B"); err != nil {
		t.Fatalf("RefreshRemote ep-B (initial seed): %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	if countByPrefix("output-ready-old-task-a") != 0 || countByPrefix("output-ready-old-task-b") != 0 {
		t.Fatalf("initial observation of active/secondary instances must not notify historical completed tasks")
	}

	// Switching active instance from ep-A to ep-B must NOT replay old-task-b!
	if _, err := h.ctrl.SelectInstance(context.Background(), "ep-B"); err != nil {
		t.Fatalf("SelectInstance ep-B: %v", err)
	}
	if err := h.ctrl.RefreshRemote(context.Background(), "ep-B"); err != nil {
		t.Fatalf("RefreshRemote ep-B after SelectInstance: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	if countByPrefix("output-ready-old-task-b") != 0 {
		t.Fatalf("SelectInstance must not replay old completed task on ep-B")
	}

	// Switch back to ep-A so ep-B is secondary again.
	if _, err := h.ctrl.SelectInstance(context.Background(), "ep-A"); err != nil {
		t.Fatalf("SelectInstance ep-A: %v", err)
	}

	// Secondary instance ep-B starts a live task and completes it -> must notify!
	rpcMu.Lock()
	epBTaskID = "live-task-b2"
	epBTaskState = "running"
	rpcMu.Unlock()
	if err := h.ctrl.RefreshRemote(context.Background(), "ep-B"); err != nil {
		t.Fatalf("RefreshRemote ep-B running: %v", err)
	}
	rpcMu.Lock()
	epBTaskState = "completed"
	rpcMu.Unlock()
	if err := h.ctrl.RefreshRemote(context.Background(), "ep-B"); err != nil {
		t.Fatalf("RefreshRemote ep-B completed: %v", err)
	}
	waitForCondition(t, 2*time.Second, func() bool { return countByPrefix("output-ready-live-task-b2") == 1 }, "secondary instance task completion notification")

	// Secondary instance ep-B fails heartbeat in MonitorOnce -> must emit EventGPUDisconnected!
	rpcMu.Lock()
	epBHeartbeatErr = errors.New("connection reset")
	rpcMu.Unlock()
	h.ctrl.mu.Lock()
	h.ctrl.lastAuth = nowUnixFloat()
	h.ctrl.lastHeartbeat = nowUnixFloat()
	h.ctrl.lastConfig = nowUnixFloat()
	if st := h.ctrl.instanceStates["ep-B"]; st != nil {
		st.LastHeartbeat = 0
	}
	h.ctrl.mu.Unlock()
	h.ctrl.MonitorOnce(context.Background())
	waitForCondition(t, 2*time.Second, func() bool { return countByPrefix("gpu-disconnected-ep-B") == 1 }, "secondary instance disconnect notification")

	// Active instance ep-A drops during heartbeat while secondary ep-B remains -> must emit auto-release notification!
	h.ctrl.mu.Lock()
	h.ctrl.lastHeartbeat = 0
	h.ctrl.mu.Unlock()
	h.provider.heartbeatFn = func(ctx context.Context, endpoint string) error {
		if endpoint == "" || endpoint == "ep-A" {
			h.provider.Clear("ep-A")
		}
		return nil
	}
	h.ctrl.MonitorOnce(context.Background())
	waitForCondition(t, 2*time.Second, func() bool { return countByPrefix("gpu-autoreleased-idle-ep-A") == 1 }, "active instance auto-release with remaining secondary session")
}

func TestControllerNotificationStartReuseCancelAndLocalization(t *testing.T) {
	h := newTestHarness(t, false)
	h.ctrl.mu.Lock()
	h.ctrl.settings["auto_drive"] = false
	h.ctrl.settings["locale"] = "ja-JP"
	h.ctrl.mu.Unlock()

	var (
		mu      sync.Mutex
		sysReqs []notifier.ColabOneSystemNotificationRequest
	)
	h.ctrl.SetSystemNotificationHooks(ColabOneSystemNotificationHooks{
		Supported:       func() bool { return true },
		IsWindowFocused: func() bool { return false },
		Send: func(ctx context.Context, req notifier.ColabOneSystemNotificationRequest) error {
			mu.Lock()
			sysReqs = append(sysReqs, req)
			mu.Unlock()
			return nil
		},
	})
	countByPrefix := func(prefix string) int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, r := range sysReqs {
			if strings.HasPrefix(r.ID, prefix) {
				n++
			}
		}
		return n
	}

	// 1. Reused existing session skips EventGPUAllocated.
	h.provider.addManagedSession("ep-reuse", "G4", "STANDARD", true)
	h.provider.startFn = func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
		return map[string]any{"endpoint": "ep-reuse", "reused": true}, nil
	}
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if path == "snapshot" {
			return map[string]any{"runtime": map[string]any{"state": "ready", "id": "ep-reuse"}}, nil
		}
		return map[string]any{"ok": true}, nil
	}
	if err := h.ctrl.Start(context.Background(), false, "G4"); err != nil {
		t.Fatalf("reused Start failed: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	if countByPrefix("gpu-allocated-") != 0 {
		t.Fatal("reused session Start must not emit EventGPUAllocated")
	}

	// 2. Cancelled Start (provider ErrAllocationCancelled / ErrProviderClosed) never emits EventGPUAllocateFailed.
	h.provider.startFn = func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
		return nil, errors.New("已取消 GPU 分配")
	}
	_ = h.ctrl.Start(context.Background(), true, "G4")
	h.provider.startFn = func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
		return nil, errors.New("远端实例管理通道已关闭")
	}
	_ = h.ctrl.Start(context.Background(), true, "G4")
	time.Sleep(80 * time.Millisecond)
	if countByPrefix("gpu-alloc-failed-") != 0 {
		t.Fatal("cancelled or provider-closed Start must never emit EventGPUAllocateFailed")
	}

	// 3. Cancelled Start with leftover instance emits dedicated warning alert instead of EventGPUAllocateFailed.
	h.provider.startFn = func(ctx context.Context, gpu string, notify func(string), opts ColabOneStartOptions) (map[string]any, error) {
		h.provider.addManagedSession("ep-leftover", "G4", "STANDARD", false)
		h.ctrl.startCancel.Set()
		return nil, errors.New("取消启动时未能确认释放刚分配的实例")
	}
	h.provider.releaseFn = func(ctx context.Context, endpoint string, onLog LogFunc) (map[string]any, error) {
		return nil, errors.New("network timeout")
	}
	_ = h.ctrl.Start(context.Background(), true, "G4")
	waitForCondition(t, 2*time.Second, func() bool { return countByPrefix("gpu-cancel-leftover-") == 1 }, "leftover cancel cleanup warning notification")
	if countByPrefix("gpu-alloc-failed-") != 0 {
		t.Fatal("cancelled Start with leftover must not emit EventGPUAllocateFailed")
	}

	// 4. Fallback output matching when remote outputs lack task_id (created_at window + diff).
	outNoTID := "fedcba9876543210fedcba9876543210"
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state": "ready",
			"id":    "ep-reuse",
			"idle":  map[string]any{"server_time": 1000.0},
			"tasks": []any{
				map[string]any{"id": "wf-legacy-1", "kind": "comfyui_run", "state": "running", "started_at": 990.0},
			},
		},
		"comfy_outputs": []any{},
	})
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state": "ready",
			"id":    "ep-reuse",
			"idle":  map[string]any{"server_time": 1002.0},
			"tasks": []any{
				map[string]any{"id": "wf-legacy-1", "kind": "comfyui_run", "state": "completed", "started_at": 990.0, "elapsed_seconds": 12.0},
			},
		},
		"comfy_outputs": []any{
			map[string]any{
				"id":            outNoTID,
				"workflow_name": "LegacyWF",
				"filename":      "legacy_art.png",
				"mime":          "image/png",
				"extension":     "png",
				"created_at":    995.0,
			},
		},
	})
	waitForCondition(t, 2*time.Second, func() bool { return countByPrefix("output-ready-wf-legacy-1") == 1 }, "legacy output matched by created_at window")
	mu.Lock()
	var legacyReq *notifier.ColabOneSystemNotificationRequest
	for i := range sysReqs {
		if sysReqs[i].ID == "output-ready-wf-legacy-1" {
			cp := sysReqs[i]
			legacyReq = &cp
			break
		}
	}
	mu.Unlock()
	if legacyReq == nil || !strings.Contains(legacyReq.Title, "LegacyWF") {
		t.Fatalf("expected legacy output matched with workflow title LegacyWF, got %+v", legacyReq)
	}

	// 5. All 6 locales supported by formatDurationForNotification and notifyGPUAutoReleasedCULimit.
	for _, loc := range []string{"zh-CN", "zh-TW", "zh-HK", "en-US", "ja-JP", "ko-KR"} {
		d1 := formatDurationForNotification(loc, 75)
		d2 := formatDurationForNotification(loc, 3665)
		if d1 == "" || d2 == "" {
			t.Fatalf("formatDurationForNotification returned empty for locale %s", loc)
		}
		if loc == "en-US" && (!strings.Contains(d1, "1m 15s") || !strings.Contains(d2, "1h 1m 5s")) {
			t.Fatalf("unexpected en-US duration: %q / %q", d1, d2)
		}
		if loc == "ko-KR" && (!strings.Contains(d1, "분") || !strings.Contains(d2, "시간")) {
			t.Fatalf("unexpected ko-KR duration: %q / %q", d1, d2)
		}
		if loc == "ja-JP" && (!strings.Contains(d1, "分") || !strings.Contains(d2, "時間")) {
			t.Fatalf("unexpected ja-JP duration: %q / %q", d1, d2)
		}
	}
	h.ctrl.notifyGPUAutoReleasedCULimit(10.5, "10")
	waitForCondition(t, 2*time.Second, func() bool { return countByPrefix("gpu-autoreleased-cu_limit-") == 1 }, "localized cu_limit notification")
	mu.Lock()
	var cuReq *notifier.ColabOneSystemNotificationRequest
	for i := range sysReqs {
		if strings.HasPrefix(sysReqs[i].ID, "gpu-autoreleased-cu_limit-") {
			cp := sysReqs[i]
			cuReq = &cp
			break
		}
	}
	mu.Unlock()
	if cuReq == nil || !strings.Contains(cuReq.Body, "10.50 CU") || !strings.Contains(cuReq.Title, "自動解放") {
		t.Fatalf("expected ja-JP localized cu_limit notification, got %+v", cuReq)
	}
}
