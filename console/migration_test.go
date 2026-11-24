package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luolongfei/ColabOne/console/internal/controller"
	"github.com/luolongfei/ColabOne/console/internal/migration"
	"github.com/luolongfei/ColabOne/console/internal/servicebundle"
	"github.com/luolongfei/ColabOne/console/internal/vault"
)

func newTestMigrationDesktop(t *testing.T, canBackground bool) (*ColabOneDesktop, *vault.ColabOneVault, string) {
	t.Helper()
	dir := t.TempDir()
	memVault := vault.NewMemoryVault()
	vAdapter := &wireVaultAdapter{v: memVault}
	bundle := servicebundle.NewEmbeddedBundle()
	kdf := migration.ColabOneMigrationKDFParams{Time: 1, MemoryKiB: 8 * 1024, Threads: 1}
	appStore := newColabOneAppearanceStore(func() (string, error) {
		return filepath.Join(dir, colabOneAppearanceFileName), nil
	})
	bridge := &colabOneBridge{
		factory: func() *controller.ColabOneController {
			return controller.NewColabOneController(controller.ColabOneControllerConfig{
				Root:           colabOneRootDir(),
				ConfigDir:      dir,
				Vault:          vAdapter,
				Models:         wireModelsAdapter{},
				Comfy:          wireComfyAdapter{},
				PublicAPI:      wirePublicAPIAdapter{},
				AssignProxy:    wireAssignProxyAdapter{},
				ServiceBundler: &wireControllerBundlerAdapter{b: bundle},
			})
		},
	}
	d := &ColabOneDesktop{
		bridge:             bridge,
		appearanceStore:    appStore,
		canBackgroundFn:    func() bool { return canBackground },
		migrationKDFParams: &kdf,
	}
	d.initCloseBehavior(func() (string, error) {
		return filepath.Join(dir, colabOneCloseBehaviorFileName), nil
	})
	d.initUpdater(dir, &stubDesktopUpdateBackend{current: "0.1.6", canSelf: true})
	t.Cleanup(func() {
		d.clearMigrationSessions()
		d.closeUpdater()
		d.bridge.close()
	})
	return d, memVault, dir
}

func TestColabOneDesktopMigrationEndToEnd(t *testing.T) {
	prevLoc := colabOneGetLocale()
	t.Cleanup(func() { colabOneSetLocaleState(prevLoc) })

	// Source desktop with full state.
	src, srcVault, _ := newTestMigrationDesktop(t, true)
	if err := srcVault.Set("colab", map[string]any{
		"token":         "ya29.desktop-src-token",
		"refresh_token": "1//0g-desktop-refresh",
		"client_id":     "764086051850-desktop.apps.googleusercontent.com",
		"scopes":        []string{"openid", "https://www.googleapis.com/auth/drive.file"},
	}); err != nil {
		t.Fatal(err)
	}
	_ = srcVault.Set("google_profile", map[string]string{"email": "user@example.com", "sub": "sub-desktop"})
	_ = srcVault.Set("cloud_project", "colabone-drive-99999")
	_ = srcVault.Set("hf", "hf_desktopSecretToken123")
	_ = srcVault.Set("public_api", map[string]any{
		"mode":         "named",
		"domain":       "api.desktop.dev",
		"tunnel_token": "eyJhIjoiYWNjb3VudC0xMjM0NTYiLCJ0IjoidHVubmVsLTEyMzQ1NiIsInMiOiJzZWNyZXQtMTIzNDU2In0=",
		"api_key":      "co_desktopSecretKey0123456789abcdefghijklmnopqrstuvwxyz",
	})
	if _, err := src.SetCloseBehavior(colabOneCloseBehaviorBackground); err != nil {
		t.Fatal(err)
	}
	if err := src.appearanceStore.Save("dark"); err != nil {
		t.Fatal(err)
	}
	if _, err := src.SetUpdatePreferences(false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Command(context.Background(), ColabOneCommandRequest{
		Action:  "settings.save",
		Payload: map[string]any{"gpu": "L4", "idle_minutes": 45, "download_threads": 32},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Command(context.Background(), ColabOneCommandRequest{
		Action:  "locale.set",
		Payload: map[string]any{"locale": "ko-KR", "persist": true},
	}); err != nil {
		t.Fatal(err)
	}

	maskTrue := true
	logsWidth := 460
	uiPrefs := &migration.ColabOneMigrationUIPrefs{
		Appearance:         "dark",
		Locale:             "ko-KR",
		PrivacyMaskDefault: &maskTrue,
		MonitorLayout:      "grid",
		CUUsageMetric:      "rate",
		LogsWidth:          &logsWidth,
	}

	// 1. Export summary contains redacted info and zero secrets.
	summary, err := src.GetMigrationExportSummary(context.Background(), uiPrefs)
	if err != nil {
		t.Fatalf("GetMigrationExportSummary failed: %v", err)
	}
	summaryJSON, _ := json.Marshal(summary)
	for _, secret := range []string{"ya29.desktop", "1//0g-desktop", "hf_desktopSecret", "co_desktopSecretKey"} {
		if strings.Contains(string(summaryJSON), secret) {
			t.Fatalf("export summary leaked secret %q", secret)
		}
	}

	// 2. Export to a .colabone archive via injected save dialog.
	archivePath := filepath.Join(t.TempDir(), "my-backup")
	src.migrationSaveDialogFn = func(context.Context, string) (string, error) {
		return archivePath, nil
	}
	exportRes, err := src.ExportMigrationFile(context.Background(), ColabOneMigrationExportRequest{
		Modules:  migration.AllModules,
		Password: "secret-password",
		UIPrefs:  uiPrefs,
	})
	if err != nil {
		t.Fatalf("ExportMigrationFile failed: %v", err)
	}
	if exportRes.Cancelled || exportRes.Filename != "my-backup.colabone" || exportRes.SizeBytes <= 0 {
		t.Fatalf("unexpected export result: %+v", exportRes)
	}
	finalArchivePath := archivePath + ".colabone"
	if _, err := os.Stat(finalArchivePath); err != nil {
		t.Fatalf("exported archive missing at %s: %v", finalArchivePath, err)
	}

	// 3. Import onto a fresh target desktop WITHOUT system tray (tests closeBehavior fallback!).
	dst, dstVault, _ := newTestMigrationDesktop(t, false)
	dst.migrationOpenDialogFn = func(context.Context) (string, error) {
		return finalArchivePath, nil
	}

	pickRes, err := dst.PickMigrationFile(context.Background())
	if err != nil {
		t.Fatalf("PickMigrationFile failed: %v", err)
	}
	if pickRes.Cancelled || pickRes.FileToken == "" || pickRes.Filename != "my-backup.colabone" {
		t.Fatalf("unexpected pick result: %+v", pickRes)
	}

	// 4. Wrong password fails cleanly AND keeps fileToken alive for immediate retry.
	if _, err := dst.DecryptMigrationPreview(context.Background(), pickRes.FileToken, "wrong-pw"); !errors.Is(err, migration.ErrWrongPassword) {
		t.Fatalf("expected ErrWrongPassword, got %v", err)
	}

	// 5. Correct password returns redacted preview with close_behavior fallback warning.
	preview, err := dst.DecryptMigrationPreview(context.Background(), pickRes.FileToken, "secret-password")
	if err != nil {
		t.Fatalf("DecryptMigrationPreview failed: %v", err)
	}
	if preview.PreviewID == "" || preview.CanRunInBackground {
		t.Fatalf("unexpected preview metadata: %+v", preview)
	}
	previewJSON, _ := json.Marshal(preview)
	for _, secret := range []string{"ya29.desktop", "1//0g-desktop", "hf_desktopSecret", "co_desktopSecretKey"} {
		if strings.Contains(string(previewJSON), secret) {
			t.Fatalf("import preview leaked secret %q", secret)
		}
	}
	hasFallbackWarn := false
	for _, w := range preview.Warnings {
		if w == "close_behavior_fallback_quit" {
			hasFallbackWarn = true
		}
	}
	if !hasFallbackWarn {
		t.Fatalf("expected close_behavior_fallback_quit warning, got %v", preview.Warnings)
	}

	// 6. Apply all modules on target desktop.
	applyRes, err := dst.ApplyMigrationPreview(context.Background(), ColabOneMigrationApplyRequest{
		PreviewID: preview.PreviewID,
		Modules:   migration.AllModules,
	})
	if err != nil {
		t.Fatalf("ApplyMigrationPreview failed: %v", err)
	}
	if !applyRes.AccountImported || !applyRes.AccountLoggedIn || applyRes.AccountEmail != "user@example.com" {
		t.Fatalf("unexpected apply result: %+v", applyRes)
	}
	if applyRes.CloseBehavior != colabOneCloseBehaviorQuit {
		t.Fatalf("expected close behavior fallback to quit, got %q", applyRes.CloseBehavior)
	}
	if applyRes.AppliedUIPrefs == nil || applyRes.AppliedUIPrefs.Appearance != "dark" ||
		applyRes.AppliedUIPrefs.Locale != "ko-KR" || applyRes.AppliedUIPrefs.PrivacyMaskDefault == nil ||
		!*applyRes.AppliedUIPrefs.PrivacyMaskDefault || applyRes.AppliedUIPrefs.LogsWidth == nil ||
		*applyRes.AppliedUIPrefs.LogsWidth != 460 {
		t.Fatalf("unexpected applied UI prefs: %+v", applyRes.AppliedUIPrefs)
	}
	if got := dst.appearanceStore.Load(); got != "dark" {
		t.Fatalf("target appearanceStore = %q, want dark", got)
	}
	if got := dst.currentCloseBehavior(); got != colabOneCloseBehaviorQuit {
		t.Fatalf("target closeBehavior = %q, want quit", got)
	}
	if got := colabOneGetLocale(); got != "ko-KR" {
		t.Fatalf("target native locale = %q, want ko-KR", got)
	}
	if upPrefs := dst.updater.Preferences(); upPrefs.AutoCheck || !upPrefs.IncludePrerelease {
		t.Fatalf("target updater prefs mismatch: %+v", upPrefs)
	}
	if got := dstVault.GetString("hf", ""); got != "hf_desktopSecretToken123" {
		t.Fatalf("target vault hf = %q", got)
	}

	// 7. Preview session is single-use and wiped after apply.
	if _, err := dst.ApplyMigrationPreview(context.Background(), ColabOneMigrationApplyRequest{
		PreviewID: preview.PreviewID,
		Modules:   []string{migration.ModuleSettings},
	}); err == nil {
		t.Fatal("expected consumed preview session to be rejected")
	}
}

func TestColabOneDesktopMigrationCancelCleansMemory(t *testing.T) {
	dst, _, _ := newTestMigrationDesktop(t, true)

	// Export dialog cancellation returns Cancelled=true with nil error.
	dst.migrationSaveDialogFn = func(context.Context, string) (string, error) {
		return "", nil
	}
	res, err := dst.ExportMigrationFile(context.Background(), ColabOneMigrationExportRequest{
		Modules:  []string{migration.ModuleSettings},
		Password: "password123",
	})
	if err != nil || !res.Cancelled {
		t.Fatalf("expected cancelled export, got res=%+v err=%v", res, err)
	}

	// Open dialog cancellation returns Cancelled=true with nil error.
	dst.migrationOpenDialogFn = func(context.Context) (string, error) {
		return "", nil
	}
	pickRes, err := dst.PickMigrationFile(context.Background())
	if err != nil || !pickRes.Cancelled {
		t.Fatalf("expected cancelled pick, got res=%+v err=%v", pickRes, err)
	}

	if err := dst.CancelMigrationSession(context.Background(), ""); err != nil {
		t.Fatalf("CancelMigrationSession failed: %v", err)
	}
}

func TestColabOneDesktopMigrationFileAndConcurrencySafety(t *testing.T) {
	d, _, dir := newTestMigrationDesktop(t, true)
	defer d.closeUpdater()
	if ctrl, err := d.bridge.ensureController(context.Background()); err == nil {
		defer ctrl.Close()
	}

	// 1. PickMigrationFile must reject directories (non-regular files).
	subDir := filepath.Join(dir, "not-a-file")
	if err := os.MkdirAll(subDir, 0o700); err != nil {
		t.Fatal(err)
	}
	d.migrationOpenDialogFn = func(context.Context) (string, error) {
		return subDir, nil
	}
	if _, err := d.PickMigrationFile(context.Background()); !errors.Is(err, migration.ErrInvalidEnvelope) {
		t.Fatalf("PickMigrationFile(dir) got %v, want ErrInvalidEnvelope", err)
	}

	// 2. writeColabOneMigrationFileAtomic must refuse to overwrite a directory or symlink.
	if err := writeColabOneMigrationFileAtomic(subDir, []byte("data")); err == nil {
		t.Fatal("expected writeColabOneMigrationFileAtomic to refuse directory target")
	}
	realTarget := filepath.Join(dir, "real-secret.txt")
	if err := os.WriteFile(realTarget, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkTarget := filepath.Join(dir, "link.colabone")
	if err := os.Symlink(realTarget, linkTarget); err == nil {
		if err := writeColabOneMigrationFileAtomic(linkTarget, []byte("overwrite")); err == nil {
			t.Fatal("expected writeColabOneMigrationFileAtomic to refuse symlink target")
		}
		got, _ := os.ReadFile(realTarget)
		if string(got) != "untouched" {
			t.Fatalf("symlink target was overwritten: %q", string(got))
		}
	}

	// 3. In-flight applying preview session must reject concurrent duplicate ApplyMigrationPreview
	// and ignore CancelMigrationSession until apply completes.
	d.migrationMu.Lock()
	d.migrationPreview = &colabOneMigrationPreviewSession{
		id:        "preview-in-flight",
		filename:  "test.colabone",
		payload:   &migration.ColabOneMigrationPayload{SchemaVersion: 1, IncludedModules: []string{migration.ModuleSettings}},
		createdAt: time.Now(),
		applying:  true,
	}
	d.migrationMu.Unlock()

	if _, err := d.ApplyMigrationPreview(context.Background(), ColabOneMigrationApplyRequest{
		PreviewID: "preview-in-flight",
		Modules:   []string{migration.ModuleSettings},
	}); err == nil || !strings.Contains(err.Error(), "请等待当前操作完成后再导入迁移文件") {
		t.Fatalf("expected busy rejection for concurrent apply, got: %v", err)
	}
	if err := d.CancelMigrationSession(context.Background(), "preview-in-flight"); err != nil {
		t.Fatal(err)
	}
	d.migrationMu.Lock()
	stillPresent := d.migrationPreview != nil && d.migrationPreview.id == "preview-in-flight"
	d.migrationPreview = nil
	d.migrationMu.Unlock()
	if !stillPresent {
		t.Fatal("CancelMigrationSession should not clear an in-flight applying preview session")
	}
}
