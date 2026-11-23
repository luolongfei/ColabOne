package shortcuts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

func resetWinDesktopCache(t *testing.T) {
	t.Helper()
	winDesktopMu.Lock()
	prev := cachedWinDesktop
	cachedWinDesktop = ""
	winDesktopMu.Unlock()
	t.Cleanup(func() {
		winDesktopMu.Lock()
		cachedWinDesktop = prev
		winDesktopMu.Unlock()
	})
}

func stubHomeDir(t *testing.T, dir string) {
	t.Helper()
	orig := userHomeDirFn
	userHomeDirFn = func() (string, error) { return dir, nil }
	t.Cleanup(func() { userHomeDirFn = orig })
}

func stubExecutable(t *testing.T, path string, err error) {
	t.Helper()
	orig := executableFn
	executableFn = func() (string, error) { return path, err }
	t.Cleanup(func() { executableFn = orig })
}

func stubWinDesktopLookups(t *testing.T, knownFn, shellFn func() string) {
	t.Helper()
	resetWinDesktopCache(t)
	origKnown := winKnownDesktopPathFn
	origShell := winQueryDesktopShellFn
	winKnownDesktopPathFn = knownFn
	winQueryDesktopShellFn = shellFn
	t.Cleanup(func() {
		winKnownDesktopPathFn = origKnown
		winQueryDesktopShellFn = origShell
	})
}

func stubRunCommand(t *testing.T, fn func(ctx context.Context, name string, args []string, env []string) ([]byte, error)) {
	t.Helper()
	orig := runCommandFn
	runCommandFn = fn
	t.Cleanup(func() { runCommandFn = orig })
}

// Port of tests/test_setup.py::test_shortcuts_description_localized_across_locales.
func TestShortcutsDescriptionLocalizedAcrossLocales(t *testing.T) {
	descs := AppDescriptions()
	expectedLocales := []string{"en-US", "ja-JP", "ko-KR", "zh-CN", "zh-HK", "zh-TW"}
	gotLocales := make([]string, 0, len(descs))
	for k := range descs {
		gotLocales = append(gotLocales, k)
	}
	slices.Sort(gotLocales)
	if !reflect.DeepEqual(gotLocales, expectedLocales) {
		t.Fatalf("AppDescriptions locales = %v, want %v", gotLocales, expectedLocales)
	}

	cases := map[string]string{
		"zh-CN": "ColabOne — 远端 GPU 工作空间",
		"en-US": "ColabOne — Remote GPU Workspace",
		"zh-TW": "ColabOne — 遠端 GPU 工作區",
		"zh-HK": "ColabOne — 遠端 GPU 工作區",
		"ja-JP": "ColabOne — リモート GPU ワークスペース",
		"ko-KR": "ColabOne — 원격 GPU 워크스페이스",
		"":      COLABONE_APP_DESCRIPTION,
		"fr-FR": COLABONE_APP_DESCRIPTION,
	}
	for loc, want := range cases {
		if got := AppDescription(loc); got != want {
			t.Errorf("AppDescription(%q) = %q, want %q", loc, got, want)
		}
	}

	// Mutating the returned copy must not mutate internal map state.
	descs["en-US"] = "mutated"
	if got := AppDescription("en-US"); got != "ColabOne — Remote GPU Workspace" {
		t.Fatalf("AppDescriptions did not return defensive copy; got %q", got)
	}

	// Verify Linux .desktop entry uses the requested locale description and 0755 permissions.
	tmp := t.TempDir()
	stubHomeDir(t, tmp)
	root := filepath.Join(tmp, "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(root, "ColabOne")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	status, err := ManageShortcuts(root, "desktop", false, "", "linux", "en-US")
	if err != nil {
		t.Fatalf("ManageShortcuts linux desktop failed: %v", err)
	}
	if status.StartMenu || !status.Desktop {
		t.Fatalf("unexpected status: %+v", status)
	}

	desktopFile := filepath.Join(tmp, "Desktop", "ColabOne.desktop")
	raw, err := os.ReadFile(desktopFile)
	if err != nil {
		t.Fatalf("failed reading desktop entry: %v", err)
	}
	content := string(raw)
	for _, snippet := range []string{
		"[Desktop Entry]\n",
		"Type=Application\n",
		"Name=ColabOne\n",
		"Comment=ColabOne — Remote GPU Workspace\n",
		`Exec="` + binPath + "\"\n",
		"Path=" + root + "\n",
		"Icon=" + filepath.Join(root, "console", "resources", "icon.png") + "\n",
		"Terminal=false\n",
		"Categories=Development;Utility;\n",
	} {
		if !strings.Contains(content, snippet) {
			t.Errorf("desktop entry missing %q\nfull content:\n%s", snippet, content)
		}
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(desktopFile)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o755 {
			t.Errorf("desktop entry mode = %04o, want 0755", perm)
		}
	}
}

func TestLinuxShortcutPrefersRootIconAndLowercaseBinaryFallback(t *testing.T) {
	tmp := t.TempDir()
	stubHomeDir(t, tmp)
	stubExecutable(t, filepath.Join(tmp, "other-test-bin"), nil)

	root := filepath.Join(tmp, "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	lowerBin := filepath.Join(root, "colabone")
	if err := os.WriteFile(lowerBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rootIcon := filepath.Join(root, "icon.png")
	if err := os.WriteFile(rootIcon, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pre-create desktop file with restrictive permissions to verify Chmod(0755) overrides existing perms.
	startPath, desktopPath := ShortcutPaths("", "linux", false)
	if !strings.HasSuffix(startPath, filepath.Join(".local", "share", "applications", "colabone.desktop")) {
		t.Fatalf("unexpected linux startMenuPath: %s", startPath)
	}
	if !strings.HasSuffix(desktopPath, filepath.Join("Desktop", "ColabOne.desktop")) {
		t.Fatalf("unexpected linux desktopPath: %s", desktopPath)
	}
	if err := os.MkdirAll(filepath.Dir(startPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(startPath, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	status, err := ManageShortcuts(root, "both", false, "", "linux", "ja-JP")
	if err != nil {
		t.Fatalf("ManageShortcuts both failed: %v", err)
	}
	if !status.StartMenu || !status.Desktop {
		t.Fatalf("expected both shortcuts active, got %+v", status)
	}

	raw, err := os.ReadFile(startPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	if !strings.Contains(content, "Comment=ColabOne — リモート GPU ワークスペース\n") {
		t.Errorf("missing ja-JP comment: %s", content)
	}
	if !strings.Contains(content, `Exec="`+lowerBin+"\"\n") {
		t.Errorf("expected lowercase binary %s in Exec, got: %s", lowerBin, content)
	}
	if !strings.Contains(content, "Icon="+rootIcon+"\n") {
		t.Errorf("expected root icon %s, got: %s", rootIcon, content)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(startPath)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o755 {
			t.Errorf("updated start menu desktop entry mode = %04o, want 0755", perm)
		}
	}

	// Remove start_menu only, then remove desktop.
	stAfterStartRemove, err := ManageShortcuts(root, "start_menu", true, "", "linux", "ja-JP")
	if err != nil {
		t.Fatalf("remove start_menu failed: %v", err)
	}
	if stAfterStartRemove.StartMenu || !stAfterStartRemove.Desktop {
		t.Fatalf("expected StartMenu=false Desktop=true, got %+v", stAfterStartRemove)
	}
	stAfterAllRemove, err := ManageShortcuts(root, "both", true, "", "linux", "ja-JP")
	if err != nil {
		t.Fatalf("remove both idempotent failed: %v", err)
	}
	if stAfterAllRemove.StartMenu || stAfterAllRemove.Desktop {
		t.Fatalf("expected both false, got %+v", stAfterAllRemove)
	}
}

// Port of tests/test_library_and_equip.py::test_desktop_shortcuts_create_and_remove_in_isolated_sandbox.
func TestDesktopShortcutsCreateAndRemoveInIsolatedSandbox(t *testing.T) {
	tmp := t.TempDir()
	sandbox := filepath.Join(tmp, "shortcuts")
	root := filepath.Join(tmp, "root")

	st0 := ShortcutStatus(sandbox, "")
	if st0.StartMenu || st0.Desktop {
		t.Fatalf("initial status = %+v, want both false", st0)
	}

	st1, err := ManageShortcuts(root, "start_menu", false, sandbox, "", "zh-CN")
	if err != nil {
		t.Fatalf("create start_menu failed: %v", err)
	}
	if !st1.StartMenu || st1.Desktop {
		t.Fatalf("after start_menu create = %+v, want {true, false}", st1)
	}

	st2, err := ManageShortcuts(root, "desktop", false, sandbox, "", "zh-CN")
	if err != nil {
		t.Fatalf("create desktop failed: %v", err)
	}
	if !st2.StartMenu || !st2.Desktop {
		t.Fatalf("after desktop create = %+v, want {true, true}", st2)
	}

	// Verify sandbox shortcut files persist the expected target/workdir key-value format.
	startPath, desktopPath := ShortcutPaths(sandbox, "", false)
	for _, p := range []string{startPath, desktopPath} {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("reading sandbox shortcut %s: %v", p, err)
		}
		text := string(data)
		if !strings.HasPrefix(text, "target=") || !strings.Contains(text, "\nworkdir=") {
			t.Errorf("unexpected sandbox shortcut content in %s: %q", p, text)
		}
	}

	st3, err := ManageShortcuts(root, "both", true, sandbox, "", "zh-CN")
	if err != nil {
		t.Fatalf("remove both failed: %v", err)
	}
	if st3.StartMenu || st3.Desktop {
		t.Fatalf("after remove both = %+v, want {false, false}", st3)
	}

	for _, invalidTarget := range []string{"taskbar-hack", "", "all"} {
		_, err := ManageShortcuts(root, invalidTarget, false, sandbox, "", "zh-CN")
		if !errors.Is(err, ErrInvalidTarget) {
			t.Errorf("target %q: err = %v, want ErrInvalidTarget", invalidTarget, err)
		}
		if err == nil || err.Error() != "快捷方式目标不合法" {
			t.Errorf("target %q: err message = %v, want exact %q", invalidTarget, err, "快捷方式目标不合法")
		}
	}
}

func TestShortcutStatusJSONTags(t *testing.T) {
	raw, err := json.Marshal(ColabOneShortcutStatus{StartMenu: true, Desktop: false})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"start_menu":true,"desktop":false}` {
		t.Fatalf("JSON = %s, want %s", string(raw), `{"start_menu":true,"desktop":false}`)
	}
}

func TestShortcutPathsSandboxAndPlatformNormalization(t *testing.T) {
	sandbox := filepath.Join(t.TempDir(), "sb")

	cases := []struct {
		plat    string
		wantExt string
	}{
		{"win32", ".lnk"},
		{"windows", ".lnk"},
		{"WINDOWS", ".lnk"},
		{"darwin", ".app"},
		{" Darwin ", ".app"},
		{"linux", ".desktop"},
		{"freebsd", ".desktop"},
	}
	for _, tc := range cases {
		startPath, desktopPath := ShortcutPaths(sandbox, tc.plat, false)
		wantStart := filepath.Join(sandbox, "StartMenu", "ColabOne"+tc.wantExt)
		wantDesktop := filepath.Join(sandbox, "Desktop", "ColabOne"+tc.wantExt)
		if startPath != wantStart || desktopPath != wantDesktop {
			t.Errorf("platform %q: got (%q, %q), want (%q, %q)", tc.plat, startPath, desktopPath, wantStart, wantDesktop)
		}
	}
}

func TestWindowsPathsFallbacksAndCaching(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	appdata := filepath.Join(tmp, "AppData", "Roaming")
	onedrive := filepath.Join(tmp, "OneDrive")
	onedriveDesktop := filepath.Join(onedrive, "Desktop")
	userProfile := filepath.Join(tmp, "UserProfile")

	if err := os.MkdirAll(onedriveDesktop, 0o755); err != nil {
		t.Fatal(err)
	}

	stubHomeDir(t, home)
	stubWinDesktopLookups(t, func() string { return "" }, func() string { return "" })

	// 1. APPDATA + OneDrive/Desktop present
	t.Setenv("APPDATA", appdata)
	t.Setenv("OneDrive", onedrive)
	t.Setenv("OneDriveConsumer", "")
	t.Setenv("USERPROFILE", userProfile)

	startPath, desktopPath := ShortcutPaths("", "windows", false)
	wantStart := filepath.Join(appdata, "Microsoft", "Windows", "Start Menu", "Programs", "ColabOne.lnk")
	wantDesktop := filepath.Join(onedriveDesktop, "ColabOne.lnk")
	if startPath != wantStart {
		t.Errorf("startPath = %q, want %q", startPath, wantStart)
	}
	if desktopPath != wantDesktop {
		t.Errorf("desktopPath = %q, want %q", desktopPath, wantDesktop)
	}

	// 2. Fallback to USERPROFILE when OneDrive is unset (env fallback must NOT be cached).
	t.Setenv("OneDrive", "")
	t.Setenv("APPDATA", "")
	startPath, desktopPath = ShortcutPaths("", "win32", false)
	wantStart = filepath.Join(home, "AppData", "Roaming", "Microsoft", "Windows", "Start Menu", "Programs", "ColabOne.lnk")
	wantDesktop = filepath.Join(userProfile, "Desktop", "ColabOne.lnk")
	if startPath != wantStart {
		t.Errorf("fallback startPath = %q, want %q", startPath, wantStart)
	}
	if desktopPath != wantDesktop {
		t.Errorf("fallback desktopPath = %q, want %q", desktopPath, wantDesktop)
	}

	// 3. OneDriveConsumer fallback
	consumer := filepath.Join(tmp, "OneDriveConsumer")
	consumerDesktop := filepath.Join(consumer, "Desktop")
	if err := os.MkdirAll(consumerDesktop, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OneDriveConsumer", consumer)
	_, desktopPath = ShortcutPaths("", "win32", false)
	if desktopPath != filepath.Join(consumerDesktop, "ColabOne.lnk") {
		t.Errorf("OneDriveConsumer desktopPath = %q, want %q", desktopPath, filepath.Join(consumerDesktop, "ColabOne.lnk"))
	}

	// 4. Shell query is only called when queryShell=true and is cached once resolved.
	shellCalls := 0
	shellDesktop := filepath.Join(tmp, "ShellDesktop")
	stubWinDesktopLookups(
		t,
		func() string { return "" },
		func() string {
			shellCalls++
			return shellDesktop
		},
	)
	_, _ = ShortcutPaths("", "win32", false)
	if shellCalls != 0 {
		t.Fatalf("queryShell=false should not invoke shell lookup; calls=%d", shellCalls)
	}
	_, desktopPath = ShortcutPaths("", "win32", true)
	if desktopPath != filepath.Join(shellDesktop, "ColabOne.lnk") || shellCalls != 1 {
		t.Fatalf("first queryShell=true: desktop=%q calls=%d", desktopPath, shellCalls)
	}
	_, desktopPath = ShortcutPaths("", "win32", true)
	if desktopPath != filepath.Join(shellDesktop, "ColabOne.lnk") || shellCalls != 1 {
		t.Fatalf("cached queryShell=true should reuse cache: desktop=%q calls=%d", desktopPath, shellCalls)
	}

	// 5. Known folder API result is cached and takes precedence over shell query.
	knownCalls := 0
	knownDesktop := filepath.Join(tmp, "KnownDesktop")
	stubWinDesktopLookups(
		t,
		func() string {
			knownCalls++
			return knownDesktop
		},
		func() string {
			t.Fatal("shell fallback should not be called when SHGetKnownFolderPath succeeds")
			return ""
		},
	)
	_, desktopPath = ShortcutPaths("", "windows", true)
	if desktopPath != filepath.Join(knownDesktop, "ColabOne.lnk") || knownCalls != 1 {
		t.Fatalf("known folder lookup: desktop=%q calls=%d", desktopPath, knownCalls)
	}
	_, desktopPath = ShortcutPaths("", "windows", false)
	if desktopPath != filepath.Join(knownDesktop, "ColabOne.lnk") || knownCalls != 1 {
		t.Fatalf("cached known folder should not re-query: desktop=%q calls=%d", desktopPath, knownCalls)
	}
}

func TestWindowsManageShortcutsPowerShellAndErrors(t *testing.T) {
	tmp := t.TempDir()
	appdata := filepath.Join(tmp, "AppData")
	desktopDir := filepath.Join(tmp, "Desktop")
	root := filepath.Join(tmp, "portable")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("APPDATA", appdata)
	t.Setenv("OneDrive", "")
	t.Setenv("OneDriveConsumer", "")
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("_CO_DESC", "stale-env-value")
	stubHomeDir(t, tmp)
	stubExecutable(t, filepath.Join(tmp, "unrelated", "test.exe"), nil)
	stubWinDesktopLookups(t, func() string { return desktopDir }, func() string { return "" })

	// 1. Missing ColabOne.exe returns exact localized error message.
	_, err := ManageShortcuts(root, "desktop", false, "", "windows", "en-US")
	if err == nil || err.Error() != "未在当前目录找到 ColabOne.exe，请在完整便携包目录中操作" {
		t.Fatalf("missing exe err = %v, want exact message", err)
	}

	// 2. Create ColabOne.exe and verify PowerShell invocation arguments & env vars.
	exePath := filepath.Join(root, "ColabOne.exe")
	if err := os.WriteFile(exePath, []byte("MZ"), 0o755); err != nil {
		t.Fatal(err)
	}

	type cmdCall struct {
		name string
		args []string
		env  map[string]string
	}
	var calls []cmdCall

	stubRunCommand(t, func(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			t.Error("expected timeout deadline on powershell context")
		}
		envMap := make(map[string]string)
		descCount := 0
		for _, kv := range env {
			if k, v, ok := strings.Cut(kv, "="); ok {
				envMap[k] = v
				if strings.EqualFold(k, "_CO_DESC") {
					descCount++
				}
			}
		}
		if descCount != 1 {
			t.Errorf("expected deduplicated _CO_DESC env var, got %d entries", descCount)
		}
		calls = append(calls, cmdCall{name: name, args: slices.Clone(args), env: envMap})
		// Simulate WScript.Shell creating the .lnk file.
		if err := os.WriteFile(envMap["_CO_LNK"], []byte("LNK"), 0o644); err != nil {
			return nil, err
		}
		return nil, nil
	})

	status, err := ManageShortcuts(root, "both", false, "", "win32", "ko-KR")
	if err != nil {
		t.Fatalf("ManageShortcuts win32 both failed: %v", err)
	}
	if !status.StartMenu || !status.Desktop {
		t.Fatalf("expected both shortcuts created, got %+v", status)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 powershell calls, got %d", len(calls))
	}
	for _, c := range calls {
		if c.name != "powershell.exe" {
			t.Errorf("cmd = %q, want powershell.exe", c.name)
		}
		wantArgs := []string{"-NoProfile", "-NonInteractive", "-Command", winLnkPSScript}
		if !reflect.DeepEqual(c.args, wantArgs) {
			t.Errorf("args = %v, want %v", c.args, wantArgs)
		}
		if c.env["_CO_EXE"] != exePath {
			t.Errorf("_CO_EXE = %q, want %q", c.env["_CO_EXE"], exePath)
		}
		if c.env["_CO_WORKDIR"] != root {
			t.Errorf("_CO_WORKDIR = %q, want %q", c.env["_CO_WORKDIR"], root)
		}
		if c.env["_CO_DESC"] != "ColabOne — 원격 GPU 워크스페이스" {
			t.Errorf("_CO_DESC = %q, want ko-KR description", c.env["_CO_DESC"])
		}
	}

	// 3. Failure when PowerShell succeeds with exit 0 but .lnk file was not written.
	stubRunCommand(t, func(_ context.Context, _ string, _ []string, _ []string) ([]byte, error) {
		return nil, nil
	})
	if err := os.Remove(filepath.Join(desktopDir, "ColabOne.lnk")); err != nil {
		t.Fatal(err)
	}
	_, err = ManageShortcuts(root, "desktop", false, "", "win32", "zh-CN")
	if err == nil || err.Error() != "创建快捷方式失败，请检查系统权限后重试" {
		t.Fatalf("missing .lnk output err = %v, want exact failure message", err)
	}

	// 4. Failure when PowerShell returns an error.
	stubRunCommand(t, func(_ context.Context, _ string, _ []string, _ []string) ([]byte, error) {
		return []byte("access denied"), errors.New("exit status 1")
	})
	_, err = ManageShortcuts(root, "desktop", false, "", "win32", "zh-CN")
	if err == nil || err.Error() != "创建快捷方式失败，请检查系统权限后重试" {
		t.Fatalf("powershell error = %v, want exact failure message", err)
	}
}

func TestDarwinShortcutsSymlinksAndBundleResolution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires developer mode / elevation on Windows")
	}

	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	stubHomeDir(t, home)
	stubExecutable(t, filepath.Join(tmp, "bin", "test-runner"), nil)

	// 1. Missing ColabOne.app returns exact error message.
	emptyRoot := filepath.Join(tmp, "empty")
	if err := os.MkdirAll(emptyRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := ManageShortcuts(emptyRoot, "start_menu", false, "", "darwin", "zh-CN")
	if err == nil || err.Error() != "未找到 ColabOne.app 应用包" {
		t.Fatalf("missing .app err = %v, want exact message", err)
	}

	// 2. Resolve .app bundle from os.Executable() inside ColabOne.app/Contents/MacOS/ColabOne.
	bundleFromExe := filepath.Join(tmp, "Installed", "ColabOne.app")
	exeInsideBundle := filepath.Join(bundleFromExe, "Contents", "MacOS", "ColabOne")
	if err := os.MkdirAll(filepath.Dir(exeInsideBundle), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exeInsideBundle, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	stubExecutable(t, exeInsideBundle, nil)

	status, err := ManageShortcuts(emptyRoot, "both", false, "", "darwin", "zh-TW")
	if err != nil {
		t.Fatalf("ManageShortcuts darwin from executable bundle failed: %v", err)
	}
	if !status.StartMenu || !status.Desktop {
		t.Fatalf("expected both darwin shortcuts true, got %+v", status)
	}

	startPath, desktopPath := ShortcutPaths("", "darwin", false)
	if startPath != filepath.Join(home, "Applications", "ColabOne.app") {
		t.Fatalf("unexpected darwin startPath: %s", startPath)
	}
	if desktopPath != filepath.Join(home, "Desktop", "ColabOne.app") {
		t.Fatalf("unexpected darwin desktopPath: %s", desktopPath)
	}
	for _, p := range []string{startPath, desktopPath} {
		target, err := os.Readlink(p)
		if err != nil {
			t.Fatalf("expected symlink at %s: %v", p, err)
		}
		if target != bundleFromExe {
			t.Errorf("symlink %s -> %q, want %q", p, target, bundleFromExe)
		}
	}

	// 3. Updating existing symlink (even if dangling) retargets to root/ColabOne.app.
	rootWithApp := filepath.Join(tmp, "portable-mac")
	bundleFromRoot := filepath.Join(rootWithApp, "ColabOne.app")
	if err := os.MkdirAll(bundleFromRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	// Replace desktop symlink with a dangling symlink first to verify dangling link status & replacement.
	if err := os.Remove(desktopPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(tmp, "nonexistent.app"), desktopPath); err != nil {
		t.Fatal(err)
	}
	if st := ShortcutStatus("", "darwin"); !st.Desktop {
		t.Fatalf("dangling symlink should still report exists via Lstat, got %+v", st)
	}
	if _, err := ManageShortcuts(rootWithApp, "desktop", false, "", "darwin", "zh-CN"); err != nil {
		t.Fatalf("updating dangling darwin symlink failed: %v", err)
	}
	target, err := os.Readlink(desktopPath)
	if err != nil || target != bundleFromRoot {
		t.Fatalf("updated desktop symlink -> %q (err=%v), want %q", target, err, bundleFromRoot)
	}

	// 4. Real non-symlink .app directory must NOT be overwritten on create or deleted on remove.
	if err := os.Remove(startPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(startPath, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ManageShortcuts(rootWithApp, "start_menu", false, "", "darwin", "zh-CN"); err != nil {
		t.Fatalf("create when real .app dir exists should skip cleanly: %v", err)
	}
	if isSymlink(startPath) || !isDir(startPath) {
		t.Fatalf("real .app directory at %s should remain untouched", startPath)
	}
	stAfterRemove, err := ManageShortcuts(rootWithApp, "both", true, "", "darwin", "zh-CN")
	if err != nil {
		t.Fatalf("remove both on darwin failed: %v", err)
	}
	if !stAfterRemove.StartMenu || stAfterRemove.Desktop {
		t.Fatalf("expected real .app dir preserved (StartMenu=true) and desktop symlink removed (Desktop=false), got %+v", stAfterRemove)
	}
}

func TestDarwinResolvesRootInsideAppBundle(t *testing.T) {
	tmp := t.TempDir()
	stubExecutable(t, filepath.Join(tmp, "other-bin"), nil)
	bundle := filepath.Join(tmp, "ColabOne.app")
	resourcesDir := filepath.Join(bundle, "Contents", "Resources")
	if err := os.MkdirAll(resourcesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	got := resolveTargetBinary(resourcesDir, "darwin")
	if got != bundle {
		t.Fatalf("resolveTargetBinary(%q, darwin) = %q, want %q", resourcesDir, got, bundle)
	}
}

func TestRemoveShortcutErrorPrefix(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod read-only parent directory test requires non-root Unix")
	}

	tmp := t.TempDir()
	sandbox := filepath.Join(tmp, "sb")
	if _, err := ManageShortcuts(tmp, "desktop", false, sandbox, "linux", "zh-CN"); err != nil {
		t.Fatal(err)
	}
	_, desktopPath := ShortcutPaths(sandbox, "linux", false)
	desktopDir := filepath.Dir(desktopPath)
	if err := os.Chmod(desktopDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(desktopDir, 0o755) })

	_, err := ManageShortcuts(tmp, "desktop", true, sandbox, "linux", "zh-CN")
	if err == nil {
		t.Fatal("expected remove error when parent directory is read-only")
	}
	if !strings.HasPrefix(err.Error(), "移除快捷方式失败：") {
		t.Fatalf("err = %q, want prefix %q", err.Error(), "移除快捷方式失败：")
	}
}

func TestShortcutsConcurrentAccessRaceSafe(t *testing.T) {
	base := t.TempDir()
	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			sb := filepath.Join(base, "w", string(rune('a'+i)))
			_ = AppDescription("en-US")
			_ = AppDescriptions()
			_, _ = ShortcutPaths(sb, "win32", false)
			if _, err := ManageShortcuts(base, "both", false, sb, "linux", "zh-CN"); err != nil {
				t.Errorf("worker %d create failed: %v", i, err)
				return
			}
			st := ShortcutStatus(sb, "linux")
			if !st.StartMenu || !st.Desktop {
				t.Errorf("worker %d unexpected status: %+v", i, st)
			}
			if _, err := ManageShortcuts(base, "both", true, sb, "linux", "zh-CN"); err != nil {
				t.Errorf("worker %d remove failed: %v", i, err)
			}
		}()
	}
	wg.Wait()
}

func TestWinDesktopShellScriptAvoidsConsoleOutputEncoding(t *testing.T) {
	const want = "[Convert]::ToBase64String([System.Text.Encoding]::UTF8.GetBytes([Environment]::GetFolderPath('Desktop')))"
	if winDesktopShellScript != want {
		t.Fatalf("winDesktopShellScript = %q, want %q", winDesktopShellScript, want)
	}
	lower := strings.ToLower(winDesktopShellScript)
	if strings.Contains(lower, "console") || strings.Contains(lower, "outputencoding") {
		t.Fatalf("winDesktopShellScript must not touch [Console]::OutputEncoding: %q", winDesktopShellScript)
	}
}

func TestDecodeBase64UTF8Path(t *testing.T) {
	enc := func(s string) []byte {
		return []byte(base64.StdEncoding.EncodeToString([]byte(s)) + "\r\n")
	}

	longPath := `C:\Users\张三\OneDrive - 很长的企业组织名称示例\Personal Workspace\Desktop`
	longB64 := base64.StdEncoding.EncodeToString([]byte(longPath))
	wrappedLongB64 := longB64[:40] + "\r\n" + longB64[40:80] + "\r\n" + longB64[80:] + "\r\n"

	// GBK (CP936) encoded bytes for `C:\Users\张三\Desktop` — invalid UTF-8 even if base64-wrapped.
	gbkBytes := append([]byte(`C:\Users\`), 0xd5, 0xc5, 0xc8, 0xfd)
	gbkBytes = append(gbkBytes, []byte(`\Desktop`)...)

	// UTF-16LE bytes for `C:\Desktop` — contains NUL bytes.
	utf16leBytes := []byte{'C', 0, ':', 0, '\\', 0, 'D', 0, 'e', 0, 's', 0, 'k', 0, 't', 0, 'o', 0, 'p', 0}

	cases := []struct {
		name string
		raw  []byte
		want string
	}{
		{
			name: "ascii_path_with_crlf",
			raw:  enc(`C:\Users\Alice\Desktop`),
			want: `C:\Users\Alice\Desktop`,
		},
		{
			name: "simplified_chinese_path",
			raw:  enc(`C:\Users\张三\Desktop`),
			want: `C:\Users\张三\Desktop`,
		},
		{
			name: "traditional_chinese_onedrive_path",
			raw:  enc(`C:\Users\王小明\OneDrive - 公司\桌面`),
			want: `C:\Users\王小明\OneDrive - 公司\桌面`,
		},
		{
			name: "japanese_path",
			raw:  enc(`C:\Users\太郎\OneDrive\デスクトップ`),
			want: `C:\Users\太郎\OneDrive\デスクトップ`,
		},
		{
			name: "korean_path",
			raw:  enc(`C:\Users\홍길동\Desktop`),
			want: `C:\Users\홍길동\Desktop`,
		},
		{
			name: "accented_latin_and_spaces",
			raw:  enc(`C:\Users\José García\Desktop`),
			want: `C:\Users\José García\Desktop`,
		},
		{
			name: "cyrillic_path",
			raw:  enc(`C:\Users\Иван Петров\Desktop`),
			want: `C:\Users\Иван Петров\Desktop`,
		},
		{
			name: "leading_bom_and_whitespace_around_base64",
			raw:  []byte("  \uFEFF  " + base64.StdEncoding.EncodeToString([]byte(`C:\Users\张三\Desktop`)) + "  \r\n"),
			want: `C:\Users\张三\Desktop`,
		},
		{
			name: "decoded_payload_with_bom_and_whitespace",
			raw:  enc("\uFEFF C:\\Users\\张三\\Desktop \r\n"),
			want: `C:\Users\张三\Desktop`,
		},
		{
			name: "line_wrapped_base64_long_unicode_path",
			raw:  []byte(wrappedLongB64),
			want: longPath,
		},
		{
			name: "nil_input",
			raw:  nil,
			want: "",
		},
		{
			name: "empty_input",
			raw:  []byte(""),
			want: "",
		},
		{
			name: "whitespace_and_bom_only",
			raw:  []byte("\uFEFF \r\n\t "),
			want: "",
		},
		{
			name: "base64_of_whitespace_only",
			raw:  enc("   \r\n"),
			want: "",
		},
		{
			name: "plain_non_base64_path_rejected",
			raw:  []byte("C:\\Users\\Alice\\Desktop\r\n"),
			want: "",
		},
		{
			name: "powershell_error_text_rejected",
			raw:  []byte("Exception setting \"OutputEncoding\": \"The handle is invalid.\"\r\n"),
			want: "",
		},
		{
			name: "invalid_utf8_gbk_bytes_rejected",
			raw:  []byte(base64.StdEncoding.EncodeToString(gbkBytes) + "\r\n"),
			want: "",
		},
		{
			name: "utf16le_nul_bytes_rejected",
			raw:  []byte(base64.StdEncoding.EncodeToString(utf16leBytes) + "\r\n"),
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeBase64UTF8Path(tc.raw)
			if got != tc.want {
				t.Fatalf("decodeBase64UTF8Path(%q) = %q, want %q", string(tc.raw), got, tc.want)
			}
		})
	}
}

// decodeDesktopStringValue simulates freedesktop.org Desktop Entry
// Specification Pass-1 string-value unescaping.
func decodeDesktopStringValue(t *testing.T, rawValue string) string {
	t.Helper()

	var strUnescaped strings.Builder
	for i := 0; i < len(rawValue); i++ {
		if rawValue[i] == '\\' {
			if i+1 >= len(rawValue) {
				t.Fatalf("dangling backslash in desktop string value %q", rawValue)
			}
			i++
			switch rawValue[i] {
			case '\\':
				strUnescaped.WriteByte('\\')
			case 's':
				strUnescaped.WriteByte(' ')
			case 'n':
				strUnescaped.WriteByte('\n')
			case 't':
				strUnescaped.WriteByte('\t')
			case 'r':
				strUnescaped.WriteByte('\r')
			default:
				t.Fatalf("invalid desktop string escape \\%c in %q", rawValue[i], rawValue)
			}
			continue
		}
		strUnescaped.WriteByte(rawValue[i])
	}
	return strUnescaped.String()
}

// decodeDesktopExecSingleQuotedArg simulates a freedesktop.org Desktop Entry
// Specification two-pass parser (string-value unescape followed by Exec quoted
// argument and %% field-code unescaping).
func decodeDesktopExecSingleQuotedArg(t *testing.T, rawValue string) string {
	t.Helper()

	// Pass 1: Desktop Entry string-value unescaping (\\ -> \, \t -> tab, etc.).
	s := decodeDesktopStringValue(t, rawValue)

	// Pass 2: Exec quoted argument unescaping inside "...".
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		t.Fatalf("expected double-quoted Exec arg after string unescape, got %q", s)
	}
	inner := s[1 : len(s)-1]
	var out strings.Builder
	for i := 0; i < len(inner); i++ {
		switch inner[i] {
		case '\\':
			if i+1 >= len(inner) {
				t.Fatalf("dangling backslash in Exec quoted arg %q", s)
			}
			i++
			switch inner[i] {
			case '\\', '"', '`', '$':
				out.WriteByte(inner[i])
			default:
				t.Fatalf("unexpected Exec escape \\%c in %q", inner[i], s)
			}
		case '"', '`', '$':
			t.Fatalf("unescaped %q in Exec quoted arg %q", inner[i], s)
		case '%':
			if i+1 >= len(inner) || inner[i+1] != '%' {
				t.Fatalf("unescaped %% field code in Exec quoted arg %q", s)
			}
			out.WriteByte('%')
			i++
		default:
			out.WriteByte(inner[i])
		}
	}
	return out.String()
}

func TestEscapeDesktopExecArgAndStringValueSpecCompliance(t *testing.T) {
	execCases := []struct {
		name string
		arg  string
		want string
	}{
		{
			name: "plain_path",
			arg:  "/opt/colabone/ColabOne",
			want: `"/opt/colabone/ColabOne"`,
		},
		{
			name: "spaces",
			arg:  "/opt/My Apps/ColabOne",
			want: `"/opt/My Apps/ColabOne"`,
		},
		{
			name: "tab_character",
			arg:  "/opt/a\tb/ColabOne",
			want: `"/opt/a\tb/ColabOne"`,
		},
		{
			name: "backslash_followed_by_tab",
			arg:  "/opt/a\\\tb/ColabOne",
			want: `"/opt/a\\\\\tb/ColabOne"`,
		},
		{
			name: "backslash_followed_by_literal_t",
			arg:  `/opt/a\tb/ColabOne`,
			want: `"/opt/a\\\\tb/ColabOne"`,
		},
		{
			name: "percent_field_codes",
			arg:  "/opt/100%u/%f/ColabOne",
			want: `"/opt/100%%u/%%f/ColabOne"`,
		},
		{
			name: "dollar_sign",
			arg:  "/opt/$HOME/ColabOne",
			want: `"/opt/\\$HOME/ColabOne"`,
		},
		{
			name: "double_quote",
			arg:  `/opt/a"b/ColabOne`,
			want: `"/opt/a\\"b/ColabOne"`,
		},
		{
			name: "backtick",
			arg:  "/opt/a`id`b/ColabOne",
			want: `"/opt/a\\` + "`id\\\\`" + `b/ColabOne"`,
		},
		{
			name: "backslash_four_slashes",
			arg:  `/opt/a\b/ColabOne`,
			want: `"/opt/a\\\\b/ColabOne"`,
		},
		{
			name: "all_special_chars_combined",
			arg:  "/opt/my\tapps/a\\b \"$HOME\" `id` 100%f/ColabOne",
			want: `"/opt/my\tapps/a\\\\b \\"\\$HOME\\" \\` + "`id\\\\`" + ` 100%%f/ColabOne"`,
		},
	}

	for _, tc := range execCases {
		t.Run(tc.name, func(t *testing.T) {
			got := escapeDesktopExecArg(tc.arg)
			if got != tc.want {
				t.Fatalf("escapeDesktopExecArg(%q) =\n  %s\nwant:\n  %s", tc.arg, got, tc.want)
			}
			if strings.ContainsRune(got, '\t') {
				t.Fatalf("escapeDesktopExecArg(%q) left raw tab byte in %q", tc.arg, got)
			}
			roundTrip := decodeDesktopExecSingleQuotedArg(t, got)
			if roundTrip != tc.arg {
				t.Fatalf("two-pass decode(%s) = %q, want %q", got, roundTrip, tc.arg)
			}
		})
	}

	stringCases := []struct {
		in   string
		want string
	}{
		{`/opt/a\b\c`, `/opt/a\\b\\c`},
		{"/opt/a\tb", `/opt/a\tb`},
		{"/opt/a\\\tb", `/opt/a\\\tb`},
		{`/opt/a\tb`, `/opt/a\\tb`},
		{"/opt/\t\t/end", `/opt/\t\t/end`},
	}
	for _, tc := range stringCases {
		got := escapeDesktopStringValue(tc.in)
		if got != tc.want {
			t.Fatalf("escapeDesktopStringValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if strings.ContainsRune(got, '\t') {
			t.Fatalf("escapeDesktopStringValue(%q) left raw tab byte in %q", tc.in, got)
		}
		if decoded := decodeDesktopStringValue(t, got); decoded != tc.in {
			t.Fatalf("decodeDesktopStringValue(%q) = %q, want %q", got, decoded, tc.in)
		}
	}

	for _, bad := range []string{"/opt/a\nb", "/opt/a\rb", "/opt/a\x00b", "/opt/a\r\nb", "/opt/a\x01b", "/opt/a\x1bb", "/opt/a\x7fb"} {
		if !hasInvalidDesktopPathChar(bad) {
			t.Errorf("hasInvalidDesktopPathChar(%q) = false, want true", bad)
		}
	}
	for b := 0; b < 0x20; b++ {
		s := "/opt/a" + string(byte(b)) + "b"
		wantInvalid := byte(b) != '\t'
		if got := hasInvalidDesktopPathChar(s); got != wantInvalid {
			t.Errorf("hasInvalidDesktopPathChar(0x%02x) = %v, want %v", b, got, wantInvalid)
		}
	}
	if !hasInvalidDesktopPathChar("/opt/a\x7fb") {
		t.Error("hasInvalidDesktopPathChar(0x7f) = false, want true")
	}
	if hasInvalidDesktopPathChar("/opt/a\tb\\c \"$HOME\" `id` 100%f/中文/ColabOne") {
		t.Error("hasInvalidDesktopPathChar falsely rejected valid characters including tab")
	}
}

func TestLinuxDesktopEntrySpecialCharsAndControlCharRejection(t *testing.T) {
	t.Run("escapes_special_chars_in_Exec_Path_and_Icon", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX directory names with quotes and backslashes require non-Windows filesystem")
		}

		tmp := t.TempDir()
		stubHomeDir(t, tmp)
		stubExecutable(t, filepath.Join(tmp, "other-test-bin"), nil)

		specialDir := filepath.Join(tmp, "dir with spaces\ttab %u $HOME \"q\" `id` \\slash")
		if err := os.MkdirAll(specialDir, 0o755); err != nil {
			t.Fatal(err)
		}
		binPath := filepath.Join(specialDir, "ColabOne")
		if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		iconPath := filepath.Join(specialDir, "icon.png")
		if err := os.WriteFile(iconPath, []byte("png"), 0o644); err != nil {
			t.Fatal(err)
		}

		status, err := ManageShortcuts(specialDir, "desktop", false, "", "linux", "en-US")
		if err != nil {
			t.Fatalf("ManageShortcuts failed for special-char path: %v", err)
		}
		if !status.Desktop {
			t.Fatalf("expected Desktop=true, got %+v", status)
		}

		_, desktopFile := ShortcutPaths("", "linux", false)
		raw, err := os.ReadFile(desktopFile)
		if err != nil {
			t.Fatalf("read desktop entry: %v", err)
		}
		content := string(raw)
		if strings.ContainsRune(content, '\t') {
			t.Fatalf(".desktop entry must not contain raw tab bytes:\n%q", content)
		}

		wantExecLine := "Exec=" + escapeDesktopExecArg(binPath)
		wantPathLine := "Path=" + escapeDesktopStringValue(specialDir)
		wantIconLine := "Icon=" + escapeDesktopStringValue(iconPath)

		lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
		if len(lines) != 9 {
			t.Fatalf("expected 9 lines in .desktop entry (no injected lines), got %d:\n%s", len(lines), content)
		}
		fields := make(map[string]string, len(lines))
		for _, line := range lines {
			if k, v, ok := strings.Cut(line, "="); ok {
				fields[k] = v
			}
		}

		if "Exec="+fields["Exec"] != wantExecLine {
			t.Errorf("Exec line mismatch:\n got: Exec=%s\nwant: %s", fields["Exec"], wantExecLine)
		}
		if gotBin := decodeDesktopExecSingleQuotedArg(t, fields["Exec"]); gotBin != binPath {
			t.Errorf("decoded Exec arg = %q, want %q", gotBin, binPath)
		}
		if "Path="+fields["Path"] != wantPathLine {
			t.Errorf("Path line mismatch:\n got: Path=%s\nwant: %s", fields["Path"], wantPathLine)
		}
		if gotPath := decodeDesktopStringValue(t, fields["Path"]); gotPath != specialDir {
			t.Errorf("decoded Path = %q, want %q", gotPath, specialDir)
		}
		if "Icon="+fields["Icon"] != wantIconLine {
			t.Errorf("Icon line mismatch:\n got: Icon=%s\nwant: %s", fields["Icon"], wantIconLine)
		}
		if gotIcon := decodeDesktopStringValue(t, fields["Icon"]); gotIcon != iconPath {
			t.Errorf("decoded Icon = %q, want %q", gotIcon, iconPath)
		}
	})

	t.Run("rejects_control_chars_in_paths_without_writing_desktop_file", func(t *testing.T) {
		badSegments := []struct {
			name string
			seg  string
		}{
			{"lf_injection", "app\nExec=/bin/evil"},
			{"cr_injection", "app\rExec=/bin/evil"},
			{"crlf_injection", "app\r\nExec=/bin/evil"},
			{"nul_byte", "app\x00evil"},
			{"soh_control", "app\x01evil"},
			{"esc_control", "app\x1bevil"},
			{"del_control", "app\x7fevil"},
		}
		for _, tc := range badSegments {
			t.Run(tc.name, func(t *testing.T) {
				tmp := t.TempDir()
				stubHomeDir(t, tmp)
				stubExecutable(t, filepath.Join(tmp, "other-test-bin"), nil)

				badRoot := filepath.Join(tmp, tc.seg)
				_, err := ManageShortcuts(badRoot, "both", false, "", "linux", "en-US")
				if err == nil {
					t.Fatalf("ManageShortcuts(%q) succeeded, want error", badRoot)
				}
				if !errors.Is(err, errInvalidDesktopPath) {
					t.Fatalf("ManageShortcuts(%q) err = %v, want wrapped errInvalidDesktopPath", badRoot, err)
				}
				if err.Error() != "创建快捷方式失败，请检查系统权限后重试" {
					t.Fatalf("err.Error() = %q, want exact localized message", err.Error())
				}

				startPath, desktopPath := ShortcutPaths("", "linux", false)
				if existsOrLink(startPath) || existsOrLink(desktopPath) {
					t.Fatalf("rejected path must not write .desktop files: start=%v desktop=%v", existsOrLink(startPath), existsOrLink(desktopPath))
				}
			})
		}
	})

	t.Run("rejects_newline_in_resolved_executable_even_with_clean_root", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("newline in filesystem path requires POSIX filesystem")
		}
		tmp := t.TempDir()
		stubHomeDir(t, tmp)

		cleanRoot := filepath.Join(tmp, "clean-root")
		if err := os.MkdirAll(cleanRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		badExeDir := filepath.Join(tmp, "evil\nExec=id")
		if err := os.MkdirAll(badExeDir, 0o755); err != nil {
			t.Fatal(err)
		}
		badExe := filepath.Join(badExeDir, "ColabOne")
		if err := os.WriteFile(badExe, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		stubExecutable(t, badExe, nil)

		_, err := ManageShortcuts(cleanRoot, "desktop", false, "", "linux", "en-US")
		if err == nil || !errors.Is(err, errInvalidDesktopPath) {
			t.Fatalf("expected errInvalidDesktopPath for newline executable path, got %v", err)
		}
		_, desktopPath := ShortcutPaths("", "linux", false)
		if existsOrLink(desktopPath) {
			t.Fatalf("desktop file must not be written when executable path contains newline")
		}
	})
}
