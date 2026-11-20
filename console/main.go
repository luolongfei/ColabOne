package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"log"
	"mime"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	bridge   *colabOneBridge
	winStore *colabOneWindowStateStore
	comfyMu  sync.Mutex
	comfyWin *mygo.Window
	mainMu   sync.Mutex
	mainWin  *mygo.Window
	logMu    sync.Mutex
	logWin   *mygo.Window
}

func (d *ColabOneDesktop) Snapshot(ctx context.Context) (string, error) {
	return d.bridge.call(ctx, "snapshot", nil)
}
func (d *ColabOneDesktop) Command(ctx context.Context, request ColabOneCommandRequest) (string, error) {
	return d.bridge.call(ctx, request.Action, request.Payload)
}
func (d *ColabOneDesktop) SetAppearance(value string) error {
	if value != "system" && value != "light" && value != "dark" {
		return errors.New("不支持的外观模式")
	}
	mygo.Theme.SetSource(mygo.ThemeSource(value))
	return nil
}
func (d *ColabOneDesktop) ReadClipboard() (string, error) {
	data, err := mygo.Clipboard.ReadFormat(transfer.Text)
	if err != nil {
		return "", errors.New("未能读取剪贴板，请直接粘贴到输入框")
	}
	return string(data), nil
}
func (d *ColabOneDesktop) WriteClipboard(value string) error {
	if len(value) > 1024*1024 {
		return errors.New("复制内容超过 1 MB")
	}
	return mygo.Clipboard.Write(transfer.TextData(value))
}
func (d *ColabOneDesktop) OpenURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" {
		return errors.New("只允许 HTTPS 链接")
	}
	allowed := []string{"accounts.google.com", "colab.research.google.com", "developers.google.com", "github.com", "huggingface.co", "one.dash.cloudflare.com", "dash.cloudflare.com", "developers.cloudflare.com"}
	for _, host := range allowed {
		if u.Hostname() == host {
			return mygo.Shell.OpenExternal(value)
		}
	}
	return errors.New("不支持的外部链接")
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
	winStore := newColabOneWindowStateStore(defaultColabOneWindowStatePath)
	mygo.App.SetWindowStateStore(winStore)
	d := &ColabOneDesktop{bridge: &colabOneBridge{}, winStore: winStore}
	mygo.Bind(d)
	if os.Getenv("MYGO_GENERATE") == "" && !mygo.App.RequestSingleInstanceLock() {
		return
	}
	var win *mygo.Window
	mygo.Theme.OnUpdated(func() {
		bg := colabOneWindowBackground()
		if win != nil {
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
	})
	show := func() {
		if win != nil {
			win.Show()
			win.Focus()
			return
		}
		win = mygo.NewWindow(mygo.WindowOptions{
			Title:           "ColabOne",
			URL:             "/",
			Width:           defaultMainWidth,
			Height:          defaultMainHeight,
			MinWidth:        mainMinWidth,
			MinHeight:       mainMinHeight,
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
	}
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.OnActivate(func(bool) { show() })
	mygo.App.OnSecondInstance(func([]string, string) { show() })
	mygo.App.WhenReady(func() {
		menu := mygo.NewMenu([]*mygo.MenuItem{
			{Label: "打开 ColabOne", Click: func(*mygo.MenuItem, *mygo.Window) { show() }},
			mygo.Separator(),
			{Label: "退出客户端（远端空闲与预算保护继续）", Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
		})
		tray, err := mygo.NewTray(mygo.TrayOptions{ID: "colabone", Icon: trayIcon, ToolTip: "ColabOne", Menu: menu})
		if err == nil {
			tray.OnClick(show)
			mygo.App.OnWillQuit(func(*mygo.QuitEvent) { tray.Destroy() })
		}
		show()
	})
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { d.bridge.close() })
	if err := mygo.App.Run(); err != nil && !strings.Contains(err.Error(), "generate") {
		log.Fatal(err)
	}
}
