package main

import (
	"archive/zip"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/luolongfei/ColabOne/console/internal/controller"
	"github.com/luolongfei/ColabOne/console/internal/fsutil"
	"github.com/luolongfei/ColabOne/console/internal/live"
	"github.com/luolongfei/ColabOne/console/internal/migration"
	"github.com/luolongfei/ColabOne/console/internal/updater"
	"github.com/luolongfei/ColabOne/console/internal/vault"
	"github.com/luolongfei/ColabOne/console/internal/webconsole"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed resources/icon.png
var trayIcon []byte

func init() {
	// Ensure Go's embedded FileServer always serves bundled WOFF2 UI fonts
	// with a standard font MIME type on clean Windows/macOS installations.
	_ = mime.AddExtensionType(".woff2", "font/woff2")
}

type ColabOneCommandRequest struct {
	Action  string         `json:"action"`
	Payload map[string]any `json:"payload"`
}

type ColabOneDesktop struct {
	bridge           *colabOneBridge
	winStore         *colabOneWindowStateStore
	appearanceStore  *colabOneAppearanceStore
	appearanceMu     sync.Mutex
	appearanceSave   chan string
	appearanceDone   chan struct{}
	appearanceClosed bool
	comfyMu          sync.Mutex
	comfyWin         *mygo.Window
	comfyOrigin      string
	mainMu           sync.Mutex
	mainWin          *mygo.Window
	logMu            sync.Mutex
	logWin           *mygo.Window
	logStateGen      atomic.Uint64
	trayMu             sync.Mutex
	tray               *mygo.Tray
	trayRefreshMu      sync.Mutex
	trayRefreshRunning bool
	trayRefreshPending bool
	focusMainMu        sync.Mutex
	focusMainRunning   bool
	focusMainPending   bool
	runOnMainFn        func(func())
	isAppReadyFn       func() bool
	splashMu              sync.Mutex
	splashWin             *mygo.Window
	splashState           *colabOneSplashViewState
	splashFadeTimer       *time.Timer
	splashRevealDropped   bool
	splashFadeDropped     bool
	mainVisibleBeforeQuit bool
	startup               *colabOneStartupCoordinator
	updater          *updater.ColabOneUpdater
	showMain         func()

	closeBehaviorWriteMu sync.Mutex
	closeBehaviorMu      sync.RWMutex
	closeBehaviorStore   *colabOneCloseBehaviorStore
	closeBehavior        string
	closePromptReady     atomic.Bool
	appQuitting          atomic.Bool
	canBackgroundFn      func() bool
	hideMainFn           func()
	quitAppFn            func()
	emitCloseReqFn       func(*mygo.Window, ColabOneCloseRequest) error

	migrationMu           sync.Mutex
	migrationPendingFile  *colabOnePendingMigrationFile
	migrationPreview      *colabOneMigrationPreviewSession
	migrationSaveDialogFn func(context.Context, string) (string, error)
	migrationOpenDialogFn func(context.Context) (string, error)
	migrationKDFParams    *migration.ColabOneMigrationKDFParams

	outputZipSaveDialogFn func(context.Context, string) (string, error)
	openExternalFn        func(string) error

	webConsoleMu sync.RWMutex
	webConsole   *webconsole.ColabOneWebConsole

	mediaMu      sync.Mutex
	mediaSrv     *http.Server
	mediaLn      net.Listener
	mediaSecret  string
	mediaBaseURL string
	mediaClosed  bool
}

func (d *ColabOneDesktop) invalidateSplash() {
	if d.appQuitting.Load() || !d.isAppReady() {
		return
	}
	d.splashMu.Lock()
	win := d.splashWin
	d.splashMu.Unlock()
	if win == nil || win.IsDestroyed() {
		return
	}
	mygo.RunOnMain(func() {
		if d.appQuitting.Load() || !d.isAppReady() {
			return
		}
		if !win.IsDestroyed() {
			win.Invalidate()
		}
	})
}

func (d *ColabOneDesktop) notifyInitialSnapshotSettled() {
	d.splashMu.Lock()
	startup := d.startup
	d.splashMu.Unlock()
	if startup != nil {
		startup.MarkSnapshotSettled()
	}
}

func (d *ColabOneDesktop) beginSplashFadeOutOnMain(focusMain func()) {
	if d.appQuitting.Load() {
		d.splashMu.Lock()
		if d.splashWin != nil && !d.splashWin.IsDestroyed() {
			d.splashFadeDropped = true
		}
		d.splashMu.Unlock()
		return
	}
	if !d.isAppReady() {
		return
	}
	d.splashMu.Lock()
	splash := d.splashWin
	state := d.splashState
	d.splashFadeDropped = false
	d.splashMu.Unlock()
	if splash == nil || splash.IsDestroyed() {
		return
	}
	if state != nil {
		state.markClosing(time.Now())
	}
	splash.SetIgnoreMouseEvents(true)
	splash.Invalidate()
	timer := time.AfterFunc(colabOneSplashDestroyDelay, func() {
		if d.appQuitting.Load() {
			d.splashMu.Lock()
			if d.splashWin == splash && !splash.IsDestroyed() {
				d.splashFadeDropped = true
			}
			d.splashMu.Unlock()
			return
		}
		if !d.isAppReady() {
			return
		}
		mygo.RunOnMain(func() {
			if d.appQuitting.Load() {
				d.splashMu.Lock()
				if d.splashWin == splash && !splash.IsDestroyed() {
					d.splashFadeDropped = true
				}
				d.splashMu.Unlock()
				return
			}
			if !d.isAppReady() {
				return
			}
			d.splashMu.Lock()
			cur := d.splashWin
			if cur == splash {
				d.splashWin = nil
				d.splashState = nil
				d.splashFadeTimer = nil
				d.splashFadeDropped = false
			}
			d.splashMu.Unlock()
			if !splash.IsDestroyed() {
				splash.Destroy()
			}
			if focusMain != nil {
				focusMain()
			}
		})
	})
	d.splashMu.Lock()
	if d.appQuitting.Load() {
		if d.splashWin == splash && !splash.IsDestroyed() {
			d.splashFadeDropped = true
		}
		d.splashMu.Unlock()
		timer.Stop()
		return
	}
	if d.splashFadeTimer != nil {
		d.splashFadeTimer.Stop()
	}
	d.splashFadeTimer = timer
	d.splashMu.Unlock()
}

func (d *ColabOneDesktop) cancelStartup() {
	d.splashMu.Lock()
	startup := d.startup
	splash := d.splashWin
	fadeTimer := d.splashFadeTimer
	d.startup = nil
	d.splashWin = nil
	d.splashState = nil
	d.splashFadeTimer = nil
	d.splashMu.Unlock()
	if fadeTimer != nil {
		fadeTimer.Stop()
	}
	if startup != nil {
		startup.Cancel()
	}
	if splash != nil && !splash.IsDestroyed() {
		mygo.RunOnMain(func() {
			if !splash.IsDestroyed() {
				splash.Destroy()
			}
		})
	}
}

func (d *ColabOneDesktop) destroyTray() {
	d.trayMu.Lock()
	tray := d.tray
	d.tray = nil
	d.trayMu.Unlock()
	if tray != nil {
		tray.Destroy()
	}
}

func revealColabOneWindow(w *mygo.Window) {
	if w == nil || w.IsDestroyed() {
		return
	}
	if w.IsMinimized() {
		w.Restore()
	}
	w.Show()
	w.Focus()
}

func (d *ColabOneDesktop) isAppReady() bool {
	if d.isAppReadyFn != nil {
		return d.isAppReadyFn()
	}
	return mygo.App.IsReady()
}

func (d *ColabOneDesktop) scheduleDetachedMainTask(
	mu *sync.Mutex,
	running *bool,
	pending *bool,
	task func(),
) {
	if d.appQuitting.Load() || !d.isAppReady() {
		return
	}
	mu.Lock()
	*pending = true
	if *running {
		mu.Unlock()
		return
	}
	*running = true
	mu.Unlock()

	runOnMain := d.runOnMainFn
	if runOnMain == nil {
		runOnMain = mygo.RunOnMain
	}
	go func() {
		completed := false
		defer func() {
			if !completed {
				mu.Lock()
				*pending = false
				*running = false
				mu.Unlock()
			}
		}()
		for {
			mu.Lock()
			if !*pending || d.appQuitting.Load() {
				*pending = false
				*running = false
				completed = true
				mu.Unlock()
				return
			}
			*pending = false
			mu.Unlock()

			if d.appQuitting.Load() || !d.isAppReady() {
				mu.Lock()
				*pending = false
				*running = false
				completed = true
				mu.Unlock()
				return
			}
			runOnMain(func() {
				if d.appQuitting.Load() {
					return
				}
				task()
			})
		}
	}()
}

func (d *ColabOneDesktop) focusMainWindow() {
	if !d.isAppReady() || d.appQuitting.Load() {
		return
	}
	d.scheduleDetachedMainTask(&d.focusMainMu, &d.focusMainRunning, &d.focusMainPending, func() {
		if d.showMain != nil {
			d.showMain()
		}
		d.splashMu.Lock()
		splashActive := d.startup != nil && d.startup.IsActive()
		d.splashMu.Unlock()
		if splashActive {
			return
		}
		if mainWin := d.mainWindow(); mainWin != nil && !mainWin.IsDestroyed() {
			bringColabOneWindowToFront(mainWin)
		}
	})
}

func (d *ColabOneDesktop) buildTrayMenu() *mygo.Menu {
	items := []*mygo.MenuItem{
		{Label: colabOneT("tray.open"), Click: func(*mygo.MenuItem, *mygo.Window) {
			if d.showMain != nil {
				d.showMain()
			}
		}},
	}
	if webItems := d.trayWebConsoleItems(); len(webItems) > 0 {
		items = append(items, webItems...)
		items = append(items, mygo.Separator())
	}
	items = append(items,
		&mygo.MenuItem{Label: d.trayUpdateLabel(), Click: func(*mygo.MenuItem, *mygo.Window) {
			d.handleTrayUpdateClick()
		}},
		mygo.Separator(),
		&mygo.MenuItem{Label: colabOneT("tray.quit"), Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
	)
	return mygo.NewMenu(items)
}

func (d *ColabOneDesktop) Snapshot(ctx context.Context) (string, error) {
	res, err := d.bridge.call(ctx, "snapshot", nil)
	if err == nil {
		if wc := d.webConsoleService(); wc != nil {
			res = wc.EnrichHostSnapshotJSON(res)
		}
	}
	d.notifyInitialSnapshotSettled()
	return res, err
}
func (d *ColabOneDesktop) Command(ctx context.Context, request ColabOneCommandRequest) (string, error) {
	action := strings.TrimSpace(request.Action)
	if action == "comfyui.run" {
		if wc := d.webConsoleService(); wc != nil {
			wc.NotifyHostWorkflowRunStarting()
		}
	}
	res, err := d.bridge.call(ctx, request.Action, request.Payload)
	if err == nil {
		switch action {
		case "comfyui.output.delete":
			if id, ok := request.Payload["id"].(string); ok && strings.TrimSpace(id) != "" {
				if wc := d.webConsoleService(); wc != nil {
					wc.ForgetOutputOwnership(id)
				}
			}
		case "comfyui.run":
			if wc := d.webConsoleService(); wc != nil {
				wc.NotifyHostWorkflowRunStarted()
			}
		}
	}
	return res, err
}
func (d *ColabOneDesktop) SystemAppearance() string {
	return colabOneDetectSystemAppearance()
}

func (d *ColabOneDesktop) SetAppearance(value string) error {
	if value != "system" && value != "light" && value != "dark" {
		return errors.New(colabOneT("err.unsupportedAppearance"))
	}
	if !d.appQuitting.Load() && d.isAppReady() {
		mygo.Theme.SetSource(mygo.ThemeSource(value))
	}
	if d.appearanceStore != nil {
		d.appearanceMu.Lock()
		ch := d.appearanceSave
		closed := d.appearanceClosed
		if ch != nil && !closed {
			// Coalesce rapid updates without blocking the UI animation critical path.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- value:
			default:
			}
			d.appearanceMu.Unlock()
		} else {
			d.appearanceMu.Unlock()
			_ = d.appearanceStore.Save(value)
		}
	}
	d.invalidateSplash()
	return nil
}

func (d *ColabOneDesktop) flushAppearance() {
	d.appearanceMu.Lock()
	if d.appearanceClosed || d.appearanceSave == nil {
		d.appearanceMu.Unlock()
		return
	}
	d.appearanceClosed = true
	close(d.appearanceSave)
	done := d.appearanceDone
	d.appearanceMu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(1 * time.Second):
		}
	}
}

// SetLocale updates the active UI language for native tray menus, window
// titles, and Go-side error messages.
func (d *ColabOneDesktop) SetLocale(value string) error {
	if _, ok := colabOneSetLocaleState(value); !ok {
		return errors.New(colabOneT("err.unsupportedLocale"))
	}
	d.invalidateSplash()
	d.trayMu.Lock()
	tray := d.tray
	d.trayMu.Unlock()
	d.comfyMu.Lock()
	comfyWin := d.comfyWin
	d.comfyMu.Unlock()
	logWin := d.logWindow()
	if tray == nil && comfyWin == nil && logWin == nil {
		return nil
	}
	if d.appQuitting.Load() || !d.isAppReady() {
		return nil
	}
	mygo.RunOnMain(func() {
		if d.appQuitting.Load() || !d.isAppReady() {
			return
		}
		d.trayMu.Lock()
		curTray := d.tray
		d.trayMu.Unlock()
		if curTray != nil {
			curTray.SetMenu(d.buildTrayMenu())
		}
		if logWin != nil && !logWin.IsDestroyed() {
			logWin.SetTitle(colabOneT("window.logs"))
		}
		if comfyWin != nil && !comfyWin.IsDestroyed() {
			comfyWin.SetTitle(colabOneT("window.comfyCanvas"))
		}
	})
	return nil
}
func (d *ColabOneDesktop) ReadClipboard() (string, error) {
	data, err := mygo.Clipboard.ReadFormat(transfer.Text)
	if err != nil {
		return "", errors.New(colabOneT("err.clipboardRead"))
	}
	return string(data), nil
}
func (d *ColabOneDesktop) WriteClipboard(value string) error {
	if len(value) > 1024*1024 {
		return errors.New(colabOneT("err.clipboardLimit"))
	}
	return mygo.Clipboard.Write(transfer.TextData(value))
}
func validateColabOneExternalURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Opaque != "" {
		return errors.New(colabOneT("err.httpsOnly"))
	}
	if u.User != nil || u.Port() != "" {
		return errors.New(colabOneT("err.unsupportedURL"))
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	allowed := []string{
		"accounts.google.com",
		"colab.research.google.com",
		"console.cloud.google.com",
		"developers.google.com",
		"drive.google.com",
		"github.com",
		"huggingface.co",
		"one.dash.cloudflare.com",
		"dash.cloudflare.com",
		"developers.cloudflare.com",
	}
	for _, h := range allowed {
		if host == h {
			return nil
		}
	}
	return errors.New(colabOneT("err.unsupportedURL"))
}

func (d *ColabOneDesktop) OpenURL(value string) error {
	if err := validateColabOneExternalURL(value); err != nil {
		return err
	}
	return mygo.Shell.OpenExternal(value)
}

const (
	colabOneDesktopZipMaxItems       = 256
	colabOneDesktopZipMaxBytes int64 = 1 << 30 // 1 GiB
)

var errColabOneDesktopZipLimit = errors.New("打包导出总大小超出 1 GB 限制")

type colabOneDesktopLimitedWriter struct {
	w        io.Writer
	limit    int64
	written  int64
	exceeded bool
}

func (l *colabOneDesktopLimitedWriter) Write(p []byte) (int, error) {
	if l.exceeded {
		return 0, errColabOneDesktopZipLimit
	}
	if l.limit > 0 && l.written+int64(len(p)) > l.limit {
		l.exceeded = true
		return 0, errColabOneDesktopZipLimit
	}
	n, err := l.w.Write(p)
	l.written += int64(n)
	return n, err
}

var colabOneDesktopOutputMIMEByExt = map[string]string{
	"png":  "image/png",
	"jpg":  "image/jpeg",
	"jpeg": "image/jpeg",
	"webp": "image/webp",
	"gif":  "image/gif",
	"mp4":  "video/mp4",
	"webm": "video/webm",
	"wav":  "audio/wav",
	"mp3":  "audio/mpeg",
	"flac": "audio/flac",
}

func colabOneDesktopExtForMIME(mimeType string) string {
	mt := strings.ToLower(strings.TrimSpace(mimeType))
	if semi := strings.IndexByte(mt, ';'); semi >= 0 {
		mt = strings.TrimSpace(mt[:semi])
	}
	switch mt {
	case "image/png":
		return "png"
	case "image/jpeg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	case "video/mp4":
		return "mp4"
	case "video/webm":
		return "webm"
	case "audio/wav", "audio/x-wav", "audio/wave":
		return "wav"
	case "audio/mpeg":
		return "mp3"
	case "audio/flac", "audio/x-flac":
		return "flac"
	default:
		return ""
	}
}

type colabOneDesktopLazyZipEntryWriter struct {
	zw              *zip.Writer
	usedNames       map[string]bool
	outputID        string
	filename        string
	extension       string
	now             time.Time
	headerAttempted bool
	started         bool
	ew              io.Writer
}

func (e *colabOneDesktopLazyZipEntryWriter) SetColabOneOutputMeta(id, filename, ext, mimeType string, size int64) {
	if e.extension == "" {
		cleanExt := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
		if allowedOutputExtensions[cleanExt] {
			e.extension = cleanExt
		} else if inferred := colabOneDesktopExtForMIME(mimeType); inferred != "" {
			e.extension = inferred
		}
	}
	if e.filename == "" && filename != "" {
		e.filename = filename
	}
}

func (e *colabOneDesktopLazyZipEntryWriter) allocateUniqueName() string {
	const dedupHeadroomBytes = 12 // reserve bytes for " (999)" suffix within maxDownloadNameBytes
	fallback := "ColabOne-" + e.outputID[:8]
	raw := strings.TrimSpace(e.filename)
	safe := sanitizeColabOneDownloadName(raw)
	if raw == "" || safe == "" || safe == "download" {
		safe = fallback
	}
	cleanExt := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(e.extension)), ".")
	if cleanExt != "" && allowedOutputExtensions[cleanExt] {
		existingExt := strings.TrimPrefix(strings.ToLower(filepath.Ext(safe)), ".")
		sameMIME := existingExt != "" && colabOneDesktopOutputMIMEByExt[existingExt] != "" &&
			colabOneDesktopOutputMIMEByExt[existingExt] == colabOneDesktopOutputMIMEByExt[cleanExt]
		if !strings.EqualFold(existingExt, cleanExt) && !sameMIME {
			suffix := "." + cleanExt
			maxStemBytes := maxDownloadNameBytes - dedupHeadroomBytes - len(suffix)
			if len(safe) > maxStemBytes {
				trunc := trimColabOneDownloadName(truncateColabOneUTF8Bytes(safe, maxStemBytes))
				if trunc == "" || trunc == "." || trunc == ".." {
					trunc = fallback
				}
				safe = trunc
			}
			safe += suffix
		}
	}
	maxBaseBytes := maxDownloadNameBytes - dedupHeadroomBytes
	if len(safe) > maxBaseBytes {
		safe = truncateColabOneDownloadName(safe, maxBaseBytes)
		if safe == "" || safe == "." || safe == ".." {
			safe = fallback
		}
	}
	if isWindowsReservedDownloadName(safe) {
		budget := maxBaseBytes - len(reservedDownloadPrefix)
		truncated := truncateColabOneDownloadName(safe, budget)
		if truncated == "" || truncated == "." || truncated == ".." {
			safe = fallback
		} else {
			safe = reservedDownloadPrefix + truncated
		}
	}
	key := strings.ToLower(safe)
	if !e.usedNames[key] {
		e.usedNames[key] = true
		return safe
	}
	ext := filepath.Ext(safe)
	stem := strings.TrimSuffix(safe, ext)
	for i := 2; ; i++ {
		suffix := fmt.Sprintf(" (%d)%s", i, ext)
		curStem := stem
		if len(curStem)+len(suffix) > maxDownloadNameBytes {
			curStem = trimColabOneDownloadName(truncateColabOneUTF8Bytes(curStem, maxDownloadNameBytes-len(suffix)))
			if curStem == "" {
				curStem = fallback
			}
		}
		cand := curStem + suffix
		ck := strings.ToLower(cand)
		if !e.usedNames[ck] {
			e.usedNames[ck] = true
			return cand
		}
	}
}

func (e *colabOneDesktopLazyZipEntryWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if !e.started {
		hdr := &zip.FileHeader{
			Name:     e.allocateUniqueName(),
			Method:   zip.Store,
			Modified: e.now,
		}
		e.headerAttempted = true
		w, err := e.zw.CreateHeader(hdr)
		if err != nil {
			return 0, err
		}
		e.ew = w
		e.started = true
	}
	return e.ew.Write(p)
}

// ExportWorkflowOutputsZip validates a list of workflow output IDs (1..256), opens
// the native save dialog for a .zip archive, streams each artifact directly into a
// sibling temporary file with zip.Store (no full-file RAM buffering), and atomically
// renames it into place. Returns "" without error if the user cancels the dialog.
func (d *ColabOneDesktop) ExportWorkflowOutputsZip(
	ctx context.Context,
	outputIDs []string,
	suggestedName string,
) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(outputIDs) == 0 {
		return "", errors.New("请至少选择一项有效作品")
	}
	seen := make(map[string]bool, len(outputIDs))
	cleanIDs := make([]string, 0, len(outputIDs))
	for _, rawID := range outputIDs {
		cleanID := strings.ToLower(strings.TrimSpace(rawID))
		if !workflowOutputIDPattern.MatchString(cleanID) {
			return "", errors.New(colabOneT("err.invalidOutputID"))
		}
		if seen[cleanID] {
			continue
		}
		seen[cleanID] = true
		cleanIDs = append(cleanIDs, cleanID)
		if len(cleanIDs) > colabOneDesktopZipMaxItems {
			return "", errors.New("单次打包导出作品数量不能超过 256 项")
		}
	}
	if len(cleanIDs) == 0 {
		return "", errors.New("请至少选择一项有效作品")
	}
	if d.bridge == nil {
		return "", errors.New(colabOneT("err.bridgeNotInitialized"))
	}

	rawSuggested := strings.TrimSpace(suggestedName)
	rawSuggested = strings.TrimSuffix(rawSuggested, ".zip")
	rawSuggested = strings.TrimSuffix(rawSuggested, ".ZIP")
	safeBase := sanitizeColabOneDownloadName(rawSuggested)
	if rawSuggested == "" || safeBase == "" || safeBase == "download" {
		safeBase = "ColabOne-Outputs"
	}
	defaultName := safeBase
	if !strings.HasSuffix(strings.ToLower(defaultName), ".zip") {
		defaultName += ".zip"
	}
	defaultPath := defaultName
	if dir, err := mygo.App.Path(mygo.PathDownloads); err == nil && dir != "" {
		defaultPath = filepath.Join(dir, defaultName)
	} else if home, err := mygo.App.Path(mygo.PathHome); err == nil && home != "" {
		defaultPath = filepath.Join(home, defaultName)
	}

	var (
		targetPath string
		dialogErr  error
	)
	if d.outputZipSaveDialogFn != nil {
		targetPath, dialogErr = d.outputZipSaveDialogFn(ctx, defaultPath)
	} else {
		targetPath, dialogErr = mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent:      mygo.CallerWindow(ctx),
			DefaultPath: defaultPath,
			Filters: []mygo.FileFilter{
				{Name: "ZIP (*.zip)", Extensions: []string{"zip"}},
			},
		})
	}
	if dialogErr != nil || targetPath == "" {
		return "", dialogErr
	}

	// Best-effort snapshot lookup for friendly sanitized archive entry names.
	type snapMeta struct {
		filename  string
		extension string
	}
	metaByID := make(map[string]snapMeta, len(cleanIDs))
	if snapRaw, err := d.bridge.call(ctx, "snapshot", nil); err == nil && strings.TrimSpace(snapRaw) != "" {
		var snap struct {
			ComfyOutputs []struct {
				ID        string `json:"id"`
				Filename  string `json:"filename"`
				Extension string `json:"extension"`
			} `json:"comfy_outputs"`
		}
		if json.Unmarshal([]byte(snapRaw), &snap) == nil {
			for _, o := range snap.ComfyOutputs {
				oid := strings.ToLower(strings.TrimSpace(o.ID))
				if seen[oid] {
					ext := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(o.Extension)), ".")
					if !allowedOutputExtensions[ext] {
						ext = ""
					}
					metaByID[oid] = snapMeta{
						filename:  strings.TrimSpace(o.Filename),
						extension: ext,
					}
				}
			}
		}
	}

	out, err := os.CreateTemp(filepath.Dir(targetPath), ".colabone-export-zip-*")
	if err != nil {
		return "", err
	}
	tmpPath := out.Name()
	keepTmp := false
	fileClosed := false
	defer func() {
		if !fileClosed {
			_ = out.Close()
		}
		if !keepTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	limitedW := &colabOneDesktopLimitedWriter{
		w:     out,
		limit: colabOneDesktopZipMaxBytes,
	}
	zw := zip.NewWriter(limitedW)
	usedNames := make(map[string]bool, len(cleanIDs))
	now := time.Now()
	writtenCount := 0
	var lastErr error

	for _, id := range cleanIDs {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		sm := metaByID[id]
		entryW := &colabOneDesktopLazyZipEntryWriter{
			zw:        zw,
			usedNames: usedNames,
			outputID:  id,
			filename:  sm.filename,
			extension: sm.extension,
			now:       now,
		}
		_, streamErr := d.bridge.streamOutputMedia(ctx, id, entryW, controller.ColabOneOutputMediaOptions{})
		if streamErr != nil {
			if entryW.started || entryW.headerAttempted || limitedW.exceeded || errors.Is(streamErr, errColabOneDesktopZipLimit) || ctx.Err() != nil {
				return "", streamErr
			}
			lastErr = streamErr
			continue
		}
		if entryW.started {
			writtenCount++
		}
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}
	if limitedW.exceeded {
		return "", errColabOneDesktopZipLimit
	}
	if writtenCount == 0 {
		if lastErr != nil {
			return "", lastErr
		}
		return "", errors.New("作品不存在")
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	if err := out.Sync(); err != nil {
		return "", err
	}
	if err := out.Close(); err != nil {
		fileClosed = true
		return "", err
	}
	fileClosed = true
	if renameErr := fsutil.RenameReplace(tmpPath, targetPath); renameErr != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("%s%w", colabOneT("err.outputSaveFailedPrefix"), renameErr)
	}
	keepTmp = true
	return targetPath, nil
}

func colabOneWindowBackground() string {
	return "light-dark(#fafbfc, #0b0c0e)"
}

func colabOneIsTrustedMicOrigin(origin string) bool {
	return colabOneIsTrustedMicOriginForEnv(origin, mygo.IsDev(), os.Getenv("MYGO_DEV_URL"))
}

func colabOneIsTrustedMicOriginForEnv(origin string, isDev bool, devURL string) bool {
	trimmed := strings.TrimSpace(origin)
	if trimmed == "" {
		return false
	}
	for _, ch := range trimmed {
		if ch <= 0x20 || ch == 0x7f {
			return false
		}
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.User != nil || u.Opaque != "" {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	switch scheme {
	case "mygo":
		return host == "localhost" && u.Port() == ""
	case "colabone", "app":
		return (host == "" || host == "localhost") && u.Port() == ""
	case "wails":
		return (host == "" || host == "localhost" || host == "wails.localhost") && u.Port() == ""
	case "http", "https":
		originNorm, err := live.ColabOneNormalizedURLOrigin(trimmed)
		if err != nil {
			return false
		}
		if scheme == "http" && host == "mygo.localhost" && u.Port() == "" {
			return true
		}
		if !isDev {
			return false
		}
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return true
		}
		if strings.TrimSpace(devURL) != "" {
			if devNorm, devErr := live.ColabOneNormalizedURLOrigin(devURL); devErr == nil && originNorm == devNorm {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func colabOneHandlePagePermission(req mygo.PermissionRequest) bool {
	if len(req.Permissions) == 0 || !colabOneIsTrustedMicOrigin(req.Origin) {
		return false
	}
	for _, perm := range req.Permissions {
		if perm != mygo.PermissionMicrophone {
			return false
		}
	}
	return true
}

func (d *ColabOneDesktop) handleWillQuit() {
	d.appQuitting.Store(true)
	d.destroyTray()
	d.cancelStartup()
	d.clearMigrationSessions()
	d.closeWebConsole()
	d.closeOutputMediaServer()
	d.closeUpdater()
	d.flushAppearance()
	if d.bridge != nil {
		d.bridge.close()
	}
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--self-test" {
		if err := runColabOneSelfTest(); err != nil {
			log.Fatal(err)
		}
		fmt.Println("ColabOne runtime OK; no network or credentials accessed")
		return
	}
	colabOneSetLocaleState(colabOneResolveInitialLocale())
	winStore := newColabOneWindowStateStore(defaultColabOneWindowStatePath)
	appearanceStore := newColabOneAppearanceStore(defaultColabOneAppearancePath)
	mygo.App.SetWindowStateStore(winStore)
	d := &ColabOneDesktop{
		winStore:        winStore,
		appearanceStore: appearanceStore,
		appearanceSave:  make(chan string, 1),
		appearanceDone:  make(chan struct{}),
	}
	configDir := colabOneConfigDir()
	sharedVault := vault.NewFileVaultWithLegacyMigration(configDir)
	d.bridge = &colabOneBridge{
		vault:         sharedVault,
		onFocusWindow: d.focusMainWindow,
	}
	d.initCloseBehavior(nil)
	d.initUpdater(configDir, nil)
	d.initWebConsole(configDir, sharedVault)
	go func() {
		defer close(d.appearanceDone)
		for value := range d.appearanceSave {
			_ = appearanceStore.Save(value)
		}
	}()
	mygo.Bind(d)
	if os.Getenv("MYGO_GENERATE") == "" && !mygo.App.RequestSingleInstanceLock() {
		return
	}
	var win *mygo.Window
	mygo.Theme.OnUpdated(func() {
		bg := colabOneWindowBackground()
		if win != nil && !win.IsDestroyed() {
			_ = win.SetBackgroundColor(bg)
		}
		d.comfyMu.Lock()
		comfyWin := d.comfyWin
		d.comfyMu.Unlock()
		if comfyWin != nil && !comfyWin.IsDestroyed() {
			_ = comfyWin.SetBackgroundColor(bg)
		}
		if logWin := d.logWindow(); logWin != nil && !logWin.IsDestroyed() {
			_ = logWin.SetBackgroundColor(bg)
		}
		d.splashMu.Lock()
		splashWin := d.splashWin
		d.splashMu.Unlock()
		if splashWin != nil && !splashWin.IsDestroyed() {
			splashWin.Invalidate()
		}
	})
	ensureMainWindow := func(hidden bool) *mygo.Window {
		if win != nil && !win.IsDestroyed() {
			return win
		}
		win = mygo.NewWindow(mygo.WindowOptions{
			Title:           "ColabOne",
			URL:             "/",
			Width:           defaultMainWidth,
			Height:          defaultMainHeight,
			MinWidth:        mainMinWidth,
			MinHeight:       mainMinHeight,
			Hidden:          hidden,
			StateKey:        "main",
			BackgroundColor: colabOneWindowBackground(),
			Page: mygo.PageOptions{
				Autoplay:             mygo.AutoplayAllow,
				BackgroundThrottling: mygo.ThrottlingOff,
			},
		})
		applyWindowIcon(win)
		trackColabOneWindowState(winStore, "main", win, defaultMainWidth, defaultMainHeight, mainMinWidth, mainMinHeight)
		d.setMainWindow(win)
		current := win
		d.closePromptReady.Store(false)
		win.OnClose(func(e *mygo.CloseEvent) {
			d.handleMainWindowClose(current, e)
		})
		if page := win.Page(); page != nil {
			page.SetPermissionHandler(colabOneHandlePagePermission)
			page.OnDidNavigate(func(string) {
				if d.mainWindow() == current {
					d.closePromptReady.Store(false)
				}
			})
			page.OnDidFailLoad(func(*mygo.LoadError) {
				if d.mainWindow() == current {
					d.closePromptReady.Store(false)
				}
			})
			page.OnRenderProcessGone(func(string) {
				if d.mainWindow() == current {
					d.closePromptReady.Store(false)
				}
			})
		}
		win.OnClosed(func() {
			if d.mainWindow() == current {
				d.closePromptReady.Store(false)
			}
			d.clearMainWindow(current)
			if win == current {
				win = nil
			}
		})
		return win
	}
	var show func()
	show = func() {
		if d.appQuitting.Load() {
			return
		}
		if !mygo.App.IsReady() {
			mygo.App.WhenReady(show)
			return
		}
		d.splashMu.Lock()
		startup := d.startup
		splashWin := d.splashWin
		d.splashMu.Unlock()
		if startup != nil && startup.IsActive() {
			if splashWin != nil && !splashWin.IsDestroyed() {
				revealColabOneWindow(splashWin)
			}
			return
		}
		if win != nil && !win.IsDestroyed() {
			revealColabOneWindow(win)
			return
		}
		ensureMainWindow(false)
	}
	startWithSplash := func() {
		if saved := appearanceStore.Load(); saved != "" {
			mygo.Theme.SetSource(mygo.ThemeSource(saved))
		}
		// Pre-warm the in-process Go controller concurrently while WebView initializes behind the splash.
		go func() {
			_ = d.bridge.start()
		}()

		splashState := &colabOneSplashViewState{shownAt: time.Now()}
		sx, sy := colabOneSplashPosition(winStore, colabOneSplashWindowWidth, colabOneSplashWindowHeight)
		splashWin := mygo.NewWindow(mygo.WindowOptions{
			Title:           "ColabOne",
			X:               sx,
			Y:               sy,
			Width:           colabOneSplashWindowWidth,
			Height:          colabOneSplashWindowHeight,
			Frameless:       true,
			Transparent:     true,
			DisableShadow:   true,
			DisableResize:   true,
			DisableMinimize: true,
			DisableMaximize: true,
			SkipTaskbar:     true,
			AlwaysOnTop:     true,
			Content:         ui.View(splashState.render),
		})
		applyWindowIcon(splashWin)

		mainWin := ensureMainWindow(true)
		startup := newColabOneStartupCoordinator(colabOneStartupConfig{
			OnReveal: func() {
				if d.appQuitting.Load() {
					d.splashMu.Lock()
					d.splashRevealDropped = true
					d.splashMu.Unlock()
					return
				}
				if !d.isAppReady() {
					return
				}
				mygo.RunOnMain(func() {
					if d.appQuitting.Load() {
						d.splashMu.Lock()
						d.splashRevealDropped = true
						d.splashMu.Unlock()
						return
					}
					if !d.isAppReady() {
						return
					}
					d.splashMu.Lock()
					d.splashRevealDropped = false
					d.splashMu.Unlock()
					if mainWin != nil && !mainWin.IsDestroyed() {
						revealColabOneWindow(mainWin)
					}
					d.beginSplashFadeOutOnMain(func() {
						if d.appQuitting.Load() || !d.isAppReady() {
							return
						}
						if mainWin != nil && !mainWin.IsDestroyed() {
							mainWin.Focus()
						}
					})
				})
			},
		})

		d.splashMu.Lock()
		d.splashWin = splashWin
		d.splashState = splashState
		d.startup = startup
		d.splashMu.Unlock()

		splashWin.OnClosed(func() {
			d.splashMu.Lock()
			if d.splashWin == splashWin {
				d.splashWin = nil
				d.splashState = nil
			}
			d.splashMu.Unlock()
			startup.ForceReveal()
		})
		mainWin.OnReadyToShow(func() {
			startup.MarkPageReady()
		})
		startup.Start()
	}
	d.showMain = show
	if runtime.GOOS == "darwin" {
		mygo.App.OnWindowAllClosed(func() {})
	}
	mygo.App.OnActivate(func(bool) { show() })
	mygo.App.OnSecondInstance(func([]string, string) { show() })
	mygo.App.WhenReady(func() {
		menu := d.buildTrayMenu()
		tray, err := mygo.NewTray(mygo.TrayOptions{ID: "colabone", Icon: trayIcon, ToolTip: "ColabOne", Menu: menu})
		if err == nil {
			d.trayMu.Lock()
			d.tray = tray
			d.trayMu.Unlock()
			if runtime.GOOS != "darwin" {
				mygo.App.OnWindowAllClosed(func() {})
			}
			if runtime.GOOS == "windows" {
				tray.OnClick(show)
			}
		}
		startWithSplash()
		d.startUpdaterBackground()
		go func() {
			_ = d.startWebConsoleIfEnabled()
		}()
	})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		wasVisible := false
		if w := d.mainWindow(); w != nil && !w.IsDestroyed() && w.IsVisible() {
			wasVisible = true
		}
		d.splashMu.Lock()
		d.mainVisibleBeforeQuit = wasVisible
		d.splashMu.Unlock()
		d.appQuitting.Store(true)
	})
	mygo.App.OnQuitCanceled(func() {
		d.appQuitting.Store(false)
		// If quit was canceled while startup/splash was aborted or main window stayed hidden/closed, restore visibility.
		d.splashMu.Lock()
		startup := d.startup
		revealDropped := d.splashRevealDropped
		fadeDropped := d.splashFadeDropped
		wasVisible := d.mainVisibleBeforeQuit
		d.splashRevealDropped = false
		d.splashFadeDropped = false
		d.mainVisibleBeforeQuit = false
		d.splashMu.Unlock()
		if !d.isAppReady() {
			return
		}
		if revealDropped || fadeDropped {
			mygo.RunOnMain(func() {
				if d.appQuitting.Load() || !d.isAppReady() {
					return
				}
				if w := d.mainWindow(); w != nil && !w.IsDestroyed() {
					revealColabOneWindow(w)
				} else if d.showMain != nil {
					d.showMain()
				}
				d.beginSplashFadeOutOnMain(func() {
					if d.appQuitting.Load() || !d.isAppReady() {
						return
					}
					if w := d.mainWindow(); w != nil && !w.IsDestroyed() {
						w.Focus()
					}
				})
			})
			return
		}
		if wasVisible || startup == nil {
			mygo.RunOnMain(func() {
				if d.appQuitting.Load() || !d.isAppReady() {
					return
				}
				if w := d.mainWindow(); w != nil && !w.IsDestroyed() {
					if !w.IsVisible() {
						revealColabOneWindow(w)
					}
				} else if wasVisible && d.showMain != nil {
					d.showMain()
				}
			})
		}
	})
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		d.handleWillQuit()
	})
	if err := mygo.App.Run(); err != nil && !strings.Contains(err.Error(), "generate") {
		log.Fatal(err)
	}
}
