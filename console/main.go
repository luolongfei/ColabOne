package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/luolongfei/ColabOne/console/internal/migration"
	"github.com/luolongfei/ColabOne/console/internal/updater"
	"log"
	"mime"
	"net/url"
	"os"
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
	trayMu           sync.Mutex
	tray             *mygo.Tray
	splashMu         sync.Mutex
	splashWin        *mygo.Window
	splashState      *colabOneSplashViewState
	splashFadeTimer  *time.Timer
	startup          *colabOneStartupCoordinator
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
}

func (d *ColabOneDesktop) invalidateSplash() {
	d.splashMu.Lock()
	win := d.splashWin
	d.splashMu.Unlock()
	if win == nil || win.IsDestroyed() {
		return
	}
	mygo.RunOnMain(func() {
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
	d.splashMu.Lock()
	splash := d.splashWin
	state := d.splashState
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
		mygo.RunOnMain(func() {
			d.splashMu.Lock()
			cur := d.splashWin
			if cur == splash {
				d.splashWin = nil
				d.splashState = nil
				d.splashFadeTimer = nil
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

func (d *ColabOneDesktop) focusMainWindow() {
	if !mygo.App.IsReady() || d.appQuitting.Load() {
		return
	}
	mygo.RunOnMain(func() {
		if d.appQuitting.Load() {
			return
		}
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
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: colabOneT("tray.open"), Click: func(*mygo.MenuItem, *mygo.Window) {
			if d.showMain != nil {
				d.showMain()
			}
		}},
		{Label: d.trayUpdateLabel(), Click: func(*mygo.MenuItem, *mygo.Window) {
			d.handleTrayUpdateClick()
		}},
		mygo.Separator(),
		{Label: colabOneT("tray.quit"), Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
	})
}

func (d *ColabOneDesktop) Snapshot(ctx context.Context) (string, error) {
	res, err := d.bridge.call(ctx, "snapshot", nil)
	d.notifyInitialSnapshotSettled()
	return res, err
}
func (d *ColabOneDesktop) Command(ctx context.Context, request ColabOneCommandRequest) (string, error) {
	return d.bridge.call(ctx, request.Action, request.Payload)
}
func (d *ColabOneDesktop) SystemAppearance() string {
	return colabOneDetectSystemAppearance()
}

func (d *ColabOneDesktop) SetAppearance(value string) error {
	if value != "system" && value != "light" && value != "dark" {
		return errors.New(colabOneT("err.unsupportedAppearance"))
	}
	if mygo.App.IsReady() {
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
	mygo.RunOnMain(func() {
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

func colabOneWindowBackground() string {
	return "light-dark(#fafbfc, #0b0c0e)"
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
	d.bridge = &colabOneBridge{
		onFocusWindow: d.focusMainWindow,
	}
	d.initCloseBehavior(nil)
	d.initUpdater(colabOneConfigDir(), nil)
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
				mygo.RunOnMain(func() {
					if mainWin != nil && !mainWin.IsDestroyed() {
						revealColabOneWindow(mainWin)
					}
					d.beginSplashFadeOutOnMain(func() {
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
	})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		d.appQuitting.Store(true)
		d.cancelStartup()
	})
	mygo.App.OnQuitCanceled(func() {
		d.appQuitting.Store(false)
	})
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		d.cancelStartup()
		d.clearMigrationSessions()
		d.closeUpdater()
		d.destroyTray()
		d.flushAppearance()
		d.bridge.close()
	})
	if err := mygo.App.Run(); err != nil && !strings.Contains(err.Error(), "generate") {
		log.Fatal(err)
	}
}
