package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"log"
	"mime"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	bridge          *colabOneBridge
	winStore        *colabOneWindowStateStore
	appearanceStore *colabOneAppearanceStore
	appearanceSave  chan string
	comfyMu         sync.Mutex
	comfyWin        *mygo.Window
	mainMu          sync.Mutex
	mainWin         *mygo.Window
	logMu           sync.Mutex
	logWin          *mygo.Window
	trayMu          sync.Mutex
	tray            *mygo.Tray
	splashMu        sync.Mutex
	splashWin       *mygo.Window
	splashState     *colabOneSplashViewState
	startup         *colabOneStartupCoordinator
	showMain        func()
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
	time.AfterFunc(colabOneSplashDestroyDelay, func() {
		mygo.RunOnMain(func() {
			d.splashMu.Lock()
			cur := d.splashWin
			if cur == splash {
				d.splashWin = nil
				d.splashState = nil
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
}

func (d *ColabOneDesktop) cancelStartup() {
	d.splashMu.Lock()
	startup := d.startup
	splash := d.splashWin
	d.startup = nil
	d.splashWin = nil
	d.splashState = nil
	d.splashMu.Unlock()
	if startup != nil {
		startup.Cancel()
	}
	if splash != nil && !splash.IsDestroyed() {
		splash.Destroy()
	}
}

func (d *ColabOneDesktop) buildTrayMenu() *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: colabOneT("tray.open"), Click: func(*mygo.MenuItem, *mygo.Window) {
			if d.showMain != nil {
				d.showMain()
			}
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
	mygo.Theme.SetSource(mygo.ThemeSource(value))
	if d.appearanceStore != nil {
		if d.appearanceSave != nil {
			// Coalesce rapid updates without blocking the UI animation critical path.
			select {
			case <-d.appearanceSave:
			default:
			}
			select {
			case d.appearanceSave <- value:
			default:
			}
		} else {
			_ = d.appearanceStore.Save(value)
		}
	}
	d.invalidateSplash()
	return nil
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
		if tray != nil {
			tray.SetMenu(d.buildTrayMenu())
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
func (d *ColabOneDesktop) OpenURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" {
		return errors.New(colabOneT("err.httpsOnly"))
	}
	allowed := []string{"accounts.google.com", "colab.research.google.com", "developers.google.com", "github.com", "huggingface.co", "one.dash.cloudflare.com", "dash.cloudflare.com", "developers.cloudflare.com"}
	for _, host := range allowed {
		if u.Hostname() == host {
			return mygo.Shell.OpenExternal(value)
		}
	}
	return errors.New(colabOneT("err.unsupportedURL"))
}

func colabOneWindowBackground() string {
	if mygo.Theme.IsDark() {
		return "#0b0c0e"
	}
	return "#fafbfc"
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--self-test" {
		exe, _ := os.Executable()
		cwd, _ := os.Getwd()
		root, err := colabOneRootAt(exe, cwd)
		if err != nil {
			log.Fatal(err)
		}
		py, err := colabOnePythonAt(root)
		if err != nil {
			log.Fatal(err)
		}
		cmd := exec.Command(py, filepath.Join(root, "bridge", "main.py"), "--self-test")
		cmd.Env = append(os.Environ(), "PYTHONPATH="+filepath.Join(root, "site-packages")+string(os.PathListSeparator)+root)
		out, err := cmd.CombinedOutput()
		fmt.Print(string(out))
		if err != nil {
			log.Fatal(err)
		}
		return
	}
	colabOneSetLocaleState(colabOneResolveInitialLocale())
	winStore := newColabOneWindowStateStore(defaultColabOneWindowStatePath)
	appearanceStore := newColabOneAppearanceStore(defaultColabOneAppearancePath)
	mygo.App.SetWindowStateStore(winStore)
	d := &ColabOneDesktop{
		bridge:          &colabOneBridge{},
		winStore:        winStore,
		appearanceStore: appearanceStore,
		appearanceSave:  make(chan string, 1),
	}
	go func() {
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
		win.OnClosed(func() {
			d.clearMainWindow(current)
			if win == current {
				win = nil
			}
		})
		return win
	}
	show := func() {
		d.splashMu.Lock()
		startup := d.startup
		splashWin := d.splashWin
		d.splashMu.Unlock()
		if startup != nil && startup.IsActive() {
			if splashWin != nil && !splashWin.IsDestroyed() {
				splashWin.Show()
				splashWin.Focus()
			}
			return
		}
		if win != nil && !win.IsDestroyed() {
			win.Show()
			win.Focus()
			return
		}
		ensureMainWindow(false)
	}
	startWithSplash := func() {
		if saved := appearanceStore.Load(); saved != "" {
			mygo.Theme.SetSource(mygo.ThemeSource(saved))
		}
		// Pre-warm the Python bridge concurrently while WebView initializes behind the splash.
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
						mainWin.Show()
						mainWin.Focus()
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
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.OnActivate(func(bool) { show() })
	mygo.App.OnSecondInstance(func([]string, string) { show() })
	mygo.App.WhenReady(func() {
		menu := d.buildTrayMenu()
		tray, err := mygo.NewTray(mygo.TrayOptions{ID: "colabone", Icon: trayIcon, ToolTip: "ColabOne", Menu: menu})
		if err == nil {
			d.trayMu.Lock()
			d.tray = tray
			d.trayMu.Unlock()
			tray.OnClick(show)
			mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
				d.trayMu.Lock()
				d.tray = nil
				d.trayMu.Unlock()
				tray.Destroy()
			})
		}
		startWithSplash()
	})
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		d.cancelStartup()
		d.bridge.close()
	})
	if err := mygo.App.Run(); err != nil && !strings.Contains(err.Error(), "generate") {
		log.Fatal(err)
	}
}
