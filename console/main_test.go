package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/egoist/mygo"
	"github.com/luolongfei/ColabOne/console/internal/controller"
	"github.com/luolongfei/ColabOne/console/internal/updater"
	"github.com/luolongfei/ColabOne/console/internal/vault"
	"github.com/luolongfei/ColabOne/console/internal/webconsole"
)

func TestColabOneIsTrustedMicOriginForEnv(t *testing.T) {
	// Production mode (isDev=false): allow only bundled app origins; ignore MYGO_DEV_URL;
	// deny empty, about:*, loopback HTTP/HTTPS, and external origins.
	prodAllowedOrigins := []string{
		"mygo://localhost",
		"http://mygo.localhost",
		"colabone://",
		"colabone://localhost",
		"app://",
		"app://localhost",
		"wails://",
		"wails://localhost",
		"wails://wails.localhost",
	}
	for _, origin := range prodAllowedOrigins {
		if !colabOneIsTrustedMicOriginForEnv(origin, false, "http://192.168.1.20:5173") {
			t.Fatalf("expected trusted mic origin in prod mode for %q", origin)
		}
	}

	prodDeniedOrigins := []string{
		"",
		"   ",
		"about:blank",
		"about:srcdoc",
		"http://localhost:5173",
		"http://127.0.0.1:5173",
		"http://[::1]:5173",
		"https://localhost:5173",
		"https://evil.example",
		"https://example.com",
		"http://evil.test:5173",
		"https://localhost.evil.com",
		"http://127.0.0.1.evil.com",
		"http://192.168.1.20:5173",
		"https://user:pass@localhost:5173",
		"http://mygo.localhost:8080",
		"https://mygo.localhost",
		"mygo://evil.com",
		"mygo://localhost:8080",
		"file:///etc/passwd",
		"javascript:alert(1)",
		"data:text/html,hello",
	}
	for _, origin := range prodDeniedOrigins {
		if colabOneIsTrustedMicOriginForEnv(origin, false, "http://192.168.1.20:5173") {
			t.Fatalf("expected mic origin DENIED in prod mode for %q", origin)
		}
	}

	// Dev mode (isDev=true): allow bundled origins, loopback HTTP/HTTPS, and exact normalized MYGO_DEV_URL origin.
	devAllowedWithoutCustomURL := []string{
		"mygo://localhost",
		"http://mygo.localhost",
		"colabone://",
		"app://",
		"wails://",
		"http://localhost:5173",
		"http://127.0.0.1:5173",
		"http://[::1]:5173",
		"https://localhost:5173",
	}
	for _, origin := range devAllowedWithoutCustomURL {
		if !colabOneIsTrustedMicOriginForEnv(origin, true, "") {
			t.Fatalf("expected trusted mic origin in dev mode for %q", origin)
		}
	}

	// Custom MYGO_DEV_URL origin matching (case, default port, and path normalized).
	if !colabOneIsTrustedMicOriginForEnv("http://192.168.1.20:5173", true, "http://192.168.1.20:5173/studio") {
		t.Fatal("expected exact normalized MYGO_DEV_URL origin allowed in dev mode")
	}
	if !colabOneIsTrustedMicOriginForEnv("https://dev.lan.example", true, "https://DEV.LAN.EXAMPLE:443/app") {
		t.Fatal("expected default-port normalized MYGO_DEV_URL origin allowed in dev mode")
	}
	// Mismatched port, scheme, or empty devURL must stay denied.
	for _, tc := range []struct {
		origin string
		devURL string
	}{
		{"http://192.168.1.20:5173", ""},
		{"http://192.168.1.20:5174", "http://192.168.1.20:5173"},
		{"https://192.168.1.20:5173", "http://192.168.1.20:5173"},
		{"https://evil.example", "http://192.168.1.20:5173"},
		{"https://evil.example", ""},
		{"", "http://192.168.1.20:5173"},
		{"about:blank", "http://192.168.1.20:5173"},
		{"about:srcdoc", ""},
		{"http://evil.test:5173", ""},
		{"https://localhost.evil.com", ""},
		{"https://user:pass@localhost:5173", ""},
		{"http://mygo.localhost:8080", ""},
		{"https://mygo.localhost", ""},
		{"file:///etc/passwd", ""},
		{"http://localhost:99999", ""},
	} {
		if colabOneIsTrustedMicOriginForEnv(tc.origin, true, tc.devURL) {
			t.Fatalf("expected mic origin DENIED in dev mode for origin=%q devURL=%q", tc.origin, tc.devURL)
		}
	}
}

func TestColabOneHandlePagePermission(t *testing.T) {
	// Production mode via MYGO_ENV=production: mygo.IsDev() is false, ignoring MYGO_DEV_URL.
	t.Setenv("MYGO_ENV", "production")
	t.Setenv("MYGO_DEV", "")
	t.Setenv("MYGO_DEV_URL", "http://192.168.1.20:5173")

	for _, origin := range []string{
		"mygo://localhost",
		"http://mygo.localhost",
		"colabone://",
		"app://",
		"wails://",
	} {
		req := mygo.PermissionRequest{
			Origin:      origin,
			Permissions: []mygo.Permission{mygo.PermissionMicrophone},
		}
		if !colabOneHandlePagePermission(req) {
			t.Fatalf("expected microphone permission granted in prod mode for trusted origin %q", origin)
		}
	}

	for _, origin := range []string{
		"",
		"about:blank",
		"http://localhost:5173",
		"http://192.168.1.20:5173",
		"https://evil.example",
	} {
		req := mygo.PermissionRequest{
			Origin:      origin,
			Permissions: []mygo.Permission{mygo.PermissionMicrophone},
		}
		if colabOneHandlePagePermission(req) {
			t.Fatalf("expected microphone permission DENIED in prod mode for origin %q", origin)
		}
	}

	// Dev mode (MYGO_ENV unset, MYGO_DEV unset as after mygo.init()): mygo.IsDev() is true.
	t.Setenv("MYGO_ENV", "")
	t.Setenv("MYGO_DEV", "")
	t.Setenv("MYGO_DEV_URL", "http://192.168.1.20:5173/index.html")

	for _, origin := range []string{
		"http://localhost:5173",
		"http://127.0.0.1:5173",
		"http://[::1]:5173",
		"https://localhost:5173",
		"http://192.168.1.20:5173",
		"mygo://localhost",
		"http://mygo.localhost",
	} {
		req := mygo.PermissionRequest{
			Origin:      origin,
			Permissions: []mygo.Permission{mygo.PermissionMicrophone},
		}
		if !colabOneHandlePagePermission(req) {
			t.Fatalf("expected microphone permission granted in dev mode for origin %q", origin)
		}
	}
	for _, origin := range []string{
		"",
		"about:blank",
		"https://evil.example",
		"http://192.168.1.20:5174",
	} {
		req := mygo.PermissionRequest{
			Origin:      origin,
			Permissions: []mygo.Permission{mygo.PermissionMicrophone},
		}
		if colabOneHandlePagePermission(req) {
			t.Fatalf("expected microphone permission DENIED in dev mode for untrusted origin %q", origin)
		}
	}

	// Disallowed, non-MyGo string, or empty permission kinds must be denied even on trusted origins.
	deniedPermSets := [][]mygo.Permission{
		nil,
		{},
		{mygo.Permission("audio")},
		{mygo.Permission("media")},
		{mygo.Permission("audio-capture")},
		{mygo.Permission("audio_capture")},
		{mygo.PermissionCamera},
		{mygo.PermissionGeolocation},
		{mygo.PermissionNotifications},
		{mygo.PermissionMicrophone, mygo.PermissionCamera},
	}
	for _, perms := range deniedPermSets {
		req := mygo.PermissionRequest{
			Origin:      "mygo://localhost",
			Permissions: perms,
		}
		if colabOneHandlePagePermission(req) {
			t.Fatalf("expected permissions %v DENIED even on trusted origin", perms)
		}
	}
}

func TestColabOneLiveMethodTimeouts(t *testing.T) {
	for _, m := range []string{
		"comfyui.input.upload",
		"comfyui.live.avatars.clip.upload",
		"comfyui.live.driving.upload",
	} {
		if got := colabOneMethodTimeout(m); got != 330*time.Second {
			t.Fatalf("colabOneMethodTimeout(%q) = %v, want 330s", m, got)
		}
	}
	if got := colabOneMethodTimeout("comfyui.outputs.drive.sync"); got != 240*time.Second {
		t.Fatalf("colabOneMethodTimeout(%q) = %v, want 240s", "comfyui.outputs.drive.sync", got)
	}
	if got := colabOneMethodTimeout("comfyui.live.start"); got != 180*time.Second {
		t.Fatalf("colabOneMethodTimeout(%q) = %v, want 180s", "comfyui.live.start", got)
	}
	for _, m := range []string{
		"comfyui.live.avatars.build_pack",
		"comfyui.live.llm.test",
	} {
		if got := colabOneMethodTimeout(m); got != 150*time.Second {
			t.Fatalf("colabOneMethodTimeout(%q) = %v, want 150s", m, got)
		}
	}
	for _, m := range []string{
		"comfyui.live.avatars.delete",
		"comfyui.live.driving.delete",
	} {
		if got := colabOneMethodTimeout(m); got != 45*time.Second {
			t.Fatalf("colabOneMethodTimeout(%q) = %v, want 45s", m, got)
		}
	}
	if got := colabOneMethodTimeout("comfyui.live.driving.list"); got != 30*time.Second {
		t.Fatalf("colabOneMethodTimeout(%q) = %v, want 30s", "comfyui.live.driving.list", got)
	}
	if got := colabOneMethodTimeout("desktop.shortcuts"); got != 30*time.Second {
		t.Fatalf("colabOneMethodTimeout(%q) = %v, want 30s", "desktop.shortcuts", got)
	}
	for _, phantom := range []string{"shortcuts.status", "shortcuts.create", "shortcuts.remove"} {
		if got := colabOneMethodTimeout(phantom); got != 120*time.Second {
			t.Fatalf("removed phantom method %q timeout = %v, want default 120s", phantom, got)
		}
	}
}

type blockingQuitInstallable struct {
	started chan struct{}
}

func (b *blockingQuitInstallable) Version() string { return "0.1.7" }
func (b *blockingQuitInstallable) Notes() string   { return "notes" }
func (b *blockingQuitInstallable) Date() time.Time { return time.Now() }
func (b *blockingQuitInstallable) Install(ctx context.Context, progress func(downloaded, total int64)) error {
	if progress != nil {
		progress(50, 100)
	}
	close(b.started)
	<-ctx.Done()
	return ctx.Err()
}

type blockingQuitUpdateBackend struct {
	started chan struct{}
}

func (b *blockingQuitUpdateBackend) CurrentVersion() string { return "0.1.6" }
func (b *blockingQuitUpdateBackend) IsDevBuild() bool       { return false }
func (b *blockingQuitUpdateBackend) CanSelfUpdate() bool    { return true }
func (b *blockingQuitUpdateBackend) SetPrereleases(bool)    {}
func (b *blockingQuitUpdateBackend) CheckSelfUpdate(context.Context) (updater.ColabOneUpdateInstallable, error) {
	return &blockingQuitInstallable{started: b.started}, nil
}
func (b *blockingQuitUpdateBackend) CheckReadOnly(context.Context, bool) (*updater.ColabOneReadOnlyRelease, error) {
	return nil, nil
}
func (b *blockingQuitUpdateBackend) Relaunch() error { return nil }

func TestColabOneDesktopQuitDeadlockRegression(t *testing.T) {
	dir := t.TempDir()
	memVault := vault.NewMemoryVault()
	installStarted := make(chan struct{})

	d := &ColabOneDesktop{
		bridge: &colabOneBridge{
			vault: memVault,
			factory: func() *controller.ColabOneController {
				return newColabOneWiredController("", dir, memVault, false)
			},
		},
	}
	d.initUpdater(dir, &blockingQuitUpdateBackend{started: installStarted})
	d.initWebConsoleWithFS(dir, memVault, func() fs.FS {
		return fstest.MapFS{"index.html": {Data: []byte("ok")}}
	})

	// Start an active updater install goroutine and active web console + output media server.
	if _, err := d.CheckForUpdate(context.Background(), true); err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if _, err := d.StartUpdateInstall(); err != nil {
		t.Fatalf("StartUpdateInstall: %v", err)
	}
	<-installStarted

	enabled := true
	for port := 43310; port < 43330; port++ {
		p := port
		if _, err := d.UpdateWebConsole(webconsole.ColabOneWebConsoleUpdateRequest{
			Enabled: &enabled,
			Port:    &p,
		}); err == nil {
			break
		}
	}
	if _, err := d.ensureOutputMediaServer(); err != nil {
		t.Fatalf("ensureOutputMediaServer: %v", err)
	}

	// handleWillQuit must set appQuitting, tear down tray first, and close all subsystems promptly.
	done := make(chan struct{})
	var completed atomic.Bool
	go func() {
		d.handleWillQuit()
		completed.Store(true)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handleWillQuit deadlocked or timed out during shutdown")
	}
	if !completed.Load() || !d.appQuitting.Load() {
		t.Fatalf("expected handleWillQuit to complete with appQuitting=true")
	}

	// Post-quit UI tray/state refresh helpers must return immediately without touching UI thread.
	d.refreshTrayMenu()
	d.notifyWebConsoleState(webconsole.ColabOneWebConsoleState{Running: false})
}

func TestMyGoConfigIncludesMacOSMicrophoneUsageDescription(t *testing.T) {
	raw, err := os.ReadFile("mygo.json")
	if err != nil {
		t.Fatalf("read mygo.json: %v", err)
	}
	var cfg struct {
		MacOS struct {
			InfoPlist map[string]any `json:"infoPlist"`
		} `json:"macos"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal mygo.json: %v", err)
	}
	desc, _ := cfg.MacOS.InfoPlist["NSMicrophoneUsageDescription"].(string)
	if desc == "" {
		t.Fatalf("mygo.json missing macos.infoPlist.NSMicrophoneUsageDescription")
	}
}

func TestDesktopNotificationTargetsDedupeBoundedAndColdRecreateNavigate(t *testing.T) {
	var (
		mu      sync.Mutex
		order   []string
		emitted []ColabOneNavigateRequest
	)
	d := &ColabOneDesktop{
		isAppReadyFn: func() bool { return true },
		runOnMainFn:  func(fn func()) { fn() },
		showMain: func() {
			mu.Lock()
			order = append(order, "show")
			mu.Unlock()
		},
		emitNavigateFn: func(_ *mygo.Window, req ColabOneNavigateRequest) error {
			mu.Lock()
			order = append(order, "nav:"+req.Tab)
			emitted = append(emitted, req)
			mu.Unlock()
			return nil
		},
	}

	// 1. systemNotificationsSupported and isMainWindowFocused read atomic flags with zero main-thread RPCs.
	if d.systemNotificationsSupported() {
		t.Fatal("expected notificationsSupported false before ready")
	}
	d.notificationsSupported.Store(true)
	if !d.systemNotificationsSupported() {
		t.Fatal("expected notificationsSupported true when ready and not quitting")
	}
	d.appQuitting.Store(true)
	if d.systemNotificationsSupported() || d.isMainWindowFocused() {
		t.Fatal("expected false when appQuitting is true")
	}
	d.appQuitting.Store(false)

	// 2. Bounded notifTargets map caps at colabOneMaxNotifTargets (64).
	for i := 0; i < colabOneMaxNotifTargets+10; i++ {
		d.rememberNotificationTarget(fmt.Sprintf("notif-%d", i), ColabOneNavigateRequest{Tab: "device"})
	}
	d.notifMu.Lock()
	gotLen := len(d.notifTargets)
	_, hasEvicted := d.notifTargets["notif-0"]
	_, hasLatest := d.notifTargets[fmt.Sprintf("notif-%d", colabOneMaxNotifTargets+9)]
	d.notifMu.Unlock()
	if gotLen != colabOneMaxNotifTargets || hasEvicted || !hasLatest {
		t.Fatalf("expected bounded notifTargets len=%d evicted=false latest=true, got len=%d evicted=%v latest=%v", colabOneMaxNotifTargets, gotLen, hasEvicted, hasLatest)
	}

	// 3. Cold-recreated window (mainPageReady=false): click stores pendingNavigate and emits on notifyInitialSnapshotSettled.
	d.rememberNotificationTarget("cold-click-1", ColabOneNavigateRequest{Tab: "comfy", OpenOutputs: true})
	if !d.handleNotificationClickByID("cold-click-1") {
		t.Fatal("expected first click for cold-click-1 to be handled")
	}
	// Duplicate click for the same ID (e.g. both n.OnClick and App.OnNotificationClick firing) must be deduped.
	if d.handleNotificationClickByID("cold-click-1") {
		t.Fatal("expected duplicate click for cold-click-1 to be suppressed")
	}
	if d.lastNotifClickAt.Load() == 0 {
		t.Fatal("expected lastNotifClickAt timestamp updated")
	}
	mu.Lock()
	if len(emitted) != 0 {
		mu.Unlock()
		t.Fatalf("expected navigate deferred while mainPageReady=false, got %+v", emitted)
	}
	mu.Unlock()

	d.notifyInitialSnapshotSettled()
	mu.Lock()
	if len(emitted) != 1 || emitted[0].Tab != "comfy" || !emitted[0].OpenOutputs {
		mu.Unlock()
		t.Fatalf("expected deferred navigate emitted on notifyInitialSnapshotSettled, got %+v", emitted)
	}
	mu.Unlock()

	// 4. Ready window (mainPageReady=true): show and navigate execute sequentially in order.
	mu.Lock()
	order = nil
	emitted = nil
	mu.Unlock()
	d.rememberNotificationTarget("live-click-2", ColabOneNavigateRequest{Tab: "models"})
	if !d.handleNotificationClickByID("live-click-2") {
		t.Fatal("expected live-click-2 to be handled")
	}
	mu.Lock()
	gotOrder := append([]string(nil), order...)
	gotEmitted := append([]ColabOneNavigateRequest(nil), emitted...)
	mu.Unlock()
	if len(gotOrder) != 2 || gotOrder[0] != "show" || gotOrder[1] != "nav:models" {
		t.Fatalf("expected sequential [show nav:models], got %v", gotOrder)
	}
	if len(gotEmitted) != 1 || gotEmitted[0].Tab != "models" || gotEmitted[0].OpenOutputs {
		t.Fatalf("unexpected live navigate event: %+v", gotEmitted)
	}
}
