// Package shortcuts manages user-level Start Menu (or Applications) and Desktop
// shortcuts for ColabOne without requiring elevated privileges.
package shortcuts

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	COLABONE_APP_NAME        = "ColabOne"
	COLABONE_APP_DESCRIPTION = "ColabOne — 远端 GPU 工作空间"

	winLnkTimeout = 15 * time.Second

	winLnkPSScript = "$ErrorActionPreference = 'Stop'; " +
		"$ws = New-Object -ComObject WScript.Shell; " +
		"$sc = $ws.CreateShortcut($env:_CO_LNK); " +
		"$sc.TargetPath = $env:_CO_EXE; " +
		"$sc.WorkingDirectory = $env:_CO_WORKDIR; " +
		"$sc.IconLocation = ($env:_CO_EXE + ',0'); " +
		"$sc.Description = $env:_CO_DESC; " +
		"$sc.Save()"
)

// ErrInvalidTarget is returned when ManageShortcuts receives an unsupported target.
var ErrInvalidTarget = errors.New("快捷方式目标不合法")

var errInvalidDesktopPath = errors.New("invalid desktop entry path")

var colabOneAppDescriptions = map[string]string{
	"zh-CN": COLABONE_APP_DESCRIPTION,
	"en-US": "ColabOne — Remote GPU Workspace",
	"zh-TW": "ColabOne — 遠端 GPU 工作區",
	"zh-HK": "ColabOne — 遠端 GPU 工作區",
	"ja-JP": "ColabOne — リモート GPU ワークスペース",
	"ko-KR": "ColabOne — 원격 GPU 워크스페이스",
}

// ColabOneShortcutStatus reports whether the Start Menu (or Applications) and
// Desktop shortcuts currently exist.
type ColabOneShortcutStatus struct {
	StartMenu bool `json:"start_menu"`
	Desktop   bool `json:"desktop"`
}

type shortcutError struct {
	msg string
	err error
}

func (e *shortcutError) Error() string { return e.msg }
func (e *shortcutError) Unwrap() error { return e.err }

var (
	winDesktopMu     sync.Mutex
	cachedWinDesktop string

	// Internal seams for deterministic offline unit tests.
	userHomeDirFn          = os.UserHomeDir
	executableFn           = os.Executable
	winKnownDesktopPathFn  = winKnownDesktopPath
	winQueryDesktopShellFn = winQueryDesktopViaShell
	runCommandFn           = defaultRunCommand
)

// AppDescription returns the localized application description for shortcuts,
// defaulting to Simplified Chinese when locale is empty or unrecognized.
func AppDescription(locale string) string {
	if desc, ok := colabOneAppDescriptions[locale]; ok {
		return desc
	}
	return COLABONE_APP_DESCRIPTION
}

// AppDescriptions returns a copy of all localized application descriptions.
func AppDescriptions() map[string]string {
	return maps.Clone(colabOneAppDescriptions)
}

func normalizePlatform(platformName string) string {
	p := strings.ToLower(strings.TrimSpace(platformName))
	if p == "" {
		p = runtime.GOOS
	}
	if p == "windows" {
		return "win32"
	}
	return p
}

func userHomeDir() string {
	if home, err := userHomeDirFn(); err == nil && home != "" {
		return home
	}
	if home := os.Getenv("HOME"); home != "" {
		return home
	}
	if home := os.Getenv("USERPROFILE"); home != "" {
		return home
	}
	return ""
}

func winDesktopDir(queryShell bool) string {
	winDesktopMu.Lock()
	if cachedWinDesktop != "" {
		cached := cachedWinDesktop
		winDesktopMu.Unlock()
		return cached
	}
	winDesktopMu.Unlock()

	if known := winKnownDesktopPathFn(); known != "" {
		winDesktopMu.Lock()
		cachedWinDesktop = known
		winDesktopMu.Unlock()
		return known
	}

	if queryShell {
		if found := winQueryDesktopShellFn(); found != "" {
			winDesktopMu.Lock()
			cachedWinDesktop = found
			winDesktopMu.Unlock()
			return found
		}
	}

	onedrive := os.Getenv("OneDrive")
	if onedrive == "" {
		onedrive = os.Getenv("OneDriveConsumer")
	}
	if onedrive != "" {
		candidate := filepath.Join(onedrive, "Desktop")
		if isDir(candidate) {
			return candidate
		}
	}

	profile := os.Getenv("USERPROFILE")
	if profile == "" {
		profile = userHomeDir()
	}
	return filepath.Join(profile, "Desktop")
}

// ShortcutPaths resolves the Start Menu (or Applications) and Desktop shortcut
// paths for the given platform, or inside sandboxDir when non-empty.
func ShortcutPaths(sandboxDir, platformName string, queryShell bool) (startMenuPath, desktopPath string) {
	plat := normalizePlatform(platformName)
	if sandboxDir != "" {
		ext := ".desktop"
		switch plat {
		case "win32":
			ext = ".lnk"
		case "darwin":
			ext = ".app"
		}
		return filepath.Join(sandboxDir, "StartMenu", COLABONE_APP_NAME+ext),
			filepath.Join(sandboxDir, "Desktop", COLABONE_APP_NAME+ext)
	}

	home := userHomeDir()
	switch plat {
	case "win32":
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			appdata = filepath.Join(home, "AppData", "Roaming")
		}
		startMenu := filepath.Join(
			appdata,
			"Microsoft",
			"Windows",
			"Start Menu",
			"Programs",
			COLABONE_APP_NAME+".lnk",
		)
		desktop := filepath.Join(winDesktopDir(queryShell), COLABONE_APP_NAME+".lnk")
		return startMenu, desktop
	case "darwin":
		return filepath.Join(home, "Applications", COLABONE_APP_NAME+".app"),
			filepath.Join(home, "Desktop", COLABONE_APP_NAME+".app")
	default:
		return filepath.Join(home, ".local", "share", "applications", "colabone.desktop"),
			filepath.Join(home, "Desktop", COLABONE_APP_NAME+".desktop")
	}
}

// ShortcutStatus inspects whether the Start Menu and Desktop shortcuts exist
// (including symlinks) without invoking interactive shell fallbacks.
func ShortcutStatus(sandboxDir, platformName string) ColabOneShortcutStatus {
	startPath, desktopPath := ShortcutPaths(sandboxDir, platformName, false)
	return ColabOneShortcutStatus{
		StartMenu: existsOrLink(startPath),
		Desktop:   existsOrLink(desktopPath),
	}
}

func resolvedExecutable() string {
	exe, err := executableFn()
	if err != nil || exe == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil && resolved != "" {
		return resolved
	}
	return exe
}

func findAppBundle(path string) string {
	if path == "" {
		return ""
	}
	cur := filepath.Clean(path)
	for {
		if strings.HasSuffix(strings.ToLower(cur), ".app") {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return ""
}

func resolveTargetBinary(root, plat string) string {
	switch plat {
	case "win32":
		exeName := COLABONE_APP_NAME + ".exe"
		candidates := []string{
			filepath.Join(root, exeName),
		}
		if exe := resolvedExecutable(); exe != "" {
			exeDir := filepath.Dir(exe)
			candidates = append(
				candidates,
				filepath.Join(exeDir, exeName),
				filepath.Join(filepath.Dir(exeDir), exeName),
			)
		}
		for _, cand := range candidates {
			if isFile(cand) {
				return cand
			}
		}
		return filepath.Join(root, exeName)
	case "darwin":
		appName := COLABONE_APP_NAME + ".app"
		candidates := []string{
			filepath.Join(root, appName),
		}
		if absRoot, err := filepath.Abs(root); err == nil {
			if bundle := findAppBundle(absRoot); bundle != "" {
				candidates = append(candidates, bundle)
			}
		} else if bundle := findAppBundle(root); bundle != "" {
			candidates = append(candidates, bundle)
		}
		if exe := resolvedExecutable(); exe != "" {
			if bundle := findAppBundle(exe); bundle != "" {
				candidates = append(candidates, bundle)
			}
		}
		for _, cand := range candidates {
			if isDir(cand) {
				return cand
			}
		}
		return filepath.Join(root, appName)
	default:
		candidates := []string{
			filepath.Join(root, COLABONE_APP_NAME),
			filepath.Join(root, "colabone"),
		}
		if exe := resolvedExecutable(); exe != "" {
			base := filepath.Base(exe)
			if base == COLABONE_APP_NAME || base == "colabone" {
				candidates = append(candidates, exe)
			}
		}
		for _, cand := range candidates {
			if isFile(cand) {
				return cand
			}
		}
		return filepath.Join(root, COLABONE_APP_NAME)
	}
}

func defaultRunCommand(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = nil
	if env != nil {
		cmd.Env = env
	}
	applyWindowsProcAttr(cmd)
	return cmd.CombinedOutput()
}

func mergeEnv(base []string, overrides map[string]string) []string {
	out := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key := entry
		if idx := strings.IndexByte(entry, '='); idx >= 0 {
			key = entry[:idx]
		}
		skip := false
		for k := range overrides {
			if strings.EqualFold(key, k) {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, entry)
		}
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}

func writeWindowsLnk(lnkPath, exePath, locale string) error {
	if err := os.MkdirAll(filepath.Dir(lnkPath), 0o755); err != nil {
		return &shortcutError{msg: "创建快捷方式失败，请检查系统权限后重试", err: err}
	}
	env := mergeEnv(os.Environ(), map[string]string{
		"_CO_LNK":     lnkPath,
		"_CO_EXE":     exePath,
		"_CO_WORKDIR": filepath.Dir(exePath),
		"_CO_DESC":    AppDescription(locale),
	})
	ctx, cancel := context.WithTimeout(context.Background(), winLnkTimeout)
	defer cancel()
	_, err := runCommandFn(
		ctx,
		"powershell.exe",
		[]string{"-NoProfile", "-NonInteractive", "-Command", winLnkPSScript},
		env,
	)
	if err != nil || !isFile(lnkPath) {
		return &shortcutError{msg: "创建快捷方式失败，请检查系统权限后重试", err: err}
	}
	return nil
}

// ManageShortcuts creates, updates, or removes user-level shortcuts for target
// ("start_menu", "desktop", or "both") and returns the resulting shortcut status.
func ManageShortcuts(root, target string, remove bool, sandboxDir, platformName, locale string) (ColabOneShortcutStatus, error) {
	if target != "start_menu" && target != "desktop" && target != "both" {
		return ColabOneShortcutStatus{}, ErrInvalidTarget
	}
	plat := normalizePlatform(platformName)
	startPath, desktopPath := ShortcutPaths(sandboxDir, plat, sandboxDir == "" && !remove)

	var selected []string
	if target == "start_menu" || target == "both" {
		selected = append(selected, startPath)
	}
	if target == "desktop" || target == "both" {
		selected = append(selected, desktopPath)
	}

	if remove {
		for _, p := range selected {
			if isSymlink(p) || isFile(p) {
				if err := os.Remove(p); err != nil {
					return ColabOneShortcutStatus{}, &shortcutError{
						msg: "移除快捷方式失败：" + err.Error(),
						err: err,
					}
				}
			}
		}
		return ShortcutStatus(sandboxDir, plat), nil
	}

	binary := resolveTargetBinary(root, plat)
	if sandboxDir != "" {
		content := fmt.Sprintf("target=%s\nworkdir=%s\n", binary, filepath.Dir(binary))
		for _, p := range selected {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return ColabOneShortcutStatus{}, &shortcutError{msg: "创建快捷方式失败，请检查系统权限后重试", err: err}
			}
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				return ColabOneShortcutStatus{}, &shortcutError{msg: "创建快捷方式失败，请检查系统权限后重试", err: err}
			}
		}
		return ShortcutStatus(sandboxDir, plat), nil
	}

	switch plat {
	case "win32":
		if !isFile(binary) {
			return ColabOneShortcutStatus{}, errors.New("未在当前目录找到 ColabOne.exe，请在完整便携包目录中操作")
		}
		absBin := binary
		if !filepath.IsAbs(absBin) {
			if resolved, err := filepath.Abs(absBin); err == nil {
				absBin = resolved
			}
		}
		for _, p := range selected {
			if err := writeWindowsLnk(p, absBin, locale); err != nil {
				return ColabOneShortcutStatus{}, err
			}
		}
	case "darwin":
		if !isDir(binary) {
			return ColabOneShortcutStatus{}, errors.New("未找到 ColabOne.app 应用包")
		}
		linkTarget := binary
		if !filepath.IsAbs(linkTarget) {
			if abs, err := filepath.Abs(linkTarget); err == nil {
				linkTarget = abs
			}
		}
		for _, p := range selected {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return ColabOneShortcutStatus{}, &shortcutError{msg: "创建快捷方式失败，请检查系统权限后重试", err: err}
			}
			if isSymlink(p) {
				if err := os.Remove(p); err != nil {
					return ColabOneShortcutStatus{}, &shortcutError{msg: "创建快捷方式失败，请检查系统权限后重试", err: err}
				}
			} else if exists(p) {
				continue
			}
			if err := os.Symlink(linkTarget, p); err != nil {
				return ColabOneShortcutStatus{}, &shortcutError{msg: "创建快捷方式失败，请检查系统权限后重试", err: err}
			}
		}
	default:
		absBin := binary
		if !filepath.IsAbs(absBin) {
			if resolved, err := filepath.Abs(absBin); err == nil {
				absBin = resolved
			}
		}
		workDir := filepath.Dir(absBin)
		iconPath := filepath.Join(root, "icon.png")
		if !isFile(iconPath) {
			iconPath = filepath.Join(root, "console", "resources", "icon.png")
		}
		if !filepath.IsAbs(iconPath) {
			if resolved, err := filepath.Abs(iconPath); err == nil {
				iconPath = resolved
			}
		}
		if hasInvalidDesktopPathChar(absBin) || hasInvalidDesktopPathChar(workDir) || hasInvalidDesktopPathChar(iconPath) {
			return ColabOneShortcutStatus{}, &shortcutError{
				msg: "创建快捷方式失败，请检查系统权限后重试",
				err: errInvalidDesktopPath,
			}
		}
		desktopEntry := fmt.Sprintf(
			"[Desktop Entry]\n"+
				"Type=Application\n"+
				"Name=%s\n"+
				"Comment=%s\n"+
				"Exec=%s\n"+
				"Path=%s\n"+
				"Icon=%s\n"+
				"Terminal=false\n"+
				"Categories=Development;Utility;\n",
			COLABONE_APP_NAME,
			AppDescription(locale),
			escapeDesktopExecArg(absBin),
			escapeDesktopStringValue(workDir),
			escapeDesktopStringValue(iconPath),
		)
		for _, p := range selected {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return ColabOneShortcutStatus{}, &shortcutError{msg: "创建快捷方式失败，请检查系统权限后重试", err: err}
			}
			if err := os.WriteFile(p, []byte(desktopEntry), 0o755); err != nil {
				return ColabOneShortcutStatus{}, &shortcutError{msg: "创建快捷方式失败，请检查系统权限后重试", err: err}
			}
			if err := os.Chmod(p, 0o755); err != nil {
				return ColabOneShortcutStatus{}, &shortcutError{msg: "创建快捷方式失败，请检查系统权限后重试", err: err}
			}
		}
	}

	return ShortcutStatus(sandboxDir, plat), nil
}

var desktopStringValueEscaper = strings.NewReplacer(`\`, `\\`, "\t", `\t`)

func hasInvalidDesktopPathChar(path string) bool {
	for i := 0; i < len(path); i++ {
		b := path[i]
		if (b < 0x20 && b != '\t') || b == 0x7f {
			return true
		}
	}
	return false
}

// escapeDesktopStringValue applies freedesktop.org Desktop Entry string value
// escaping (\ -> \\ and \t -> \t) for string fields such as Path= and Icon=.
func escapeDesktopStringValue(s string) string {
	return desktopStringValueEscaper.Replace(s)
}

// escapeDesktopExecArg quotes and escapes a single Exec argument according to
// the freedesktop.org Desktop Entry Specification: first escaping argument-level
// characters inside double quotes (\, ", `, $, and % -> %%), then applying
// Desktop Entry string-value escaping (\ -> \\ and \t -> \t).
func escapeDesktopExecArg(arg string) string {
	var b strings.Builder
	b.Grow(len(arg) + 8)
	for i := 0; i < len(arg); i++ {
		switch arg[i] {
		case '\\', '"', '`', '$':
			b.WriteByte('\\')
			b.WriteByte(arg[i])
		case '%':
			b.WriteString("%%")
		default:
			b.WriteByte(arg[i])
		}
	}
	return `"` + escapeDesktopStringValue(b.String()) + `"`
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func existsOrLink(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}
