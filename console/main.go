package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed resources/tray.png
var trayIcon []byte

type CommandRequest struct {
	Action  string         `json:"action"`
	Payload map[string]any `json:"payload"`
}
type Desktop struct{ bridge *bridge }

func (d *Desktop) Snapshot(ctx context.Context) (string, error) {
	return d.bridge.call(ctx, "snapshot", nil)
}
func (d *Desktop) Command(ctx context.Context, request CommandRequest) (string, error) {
	return d.bridge.call(ctx, request.Action, request.Payload)
}
func (d *Desktop) ReadClipboard() (string, error) {
	data, err := mygo.Clipboard.ReadFormat(transfer.Text)
	if err != nil {
		return "", errors.New("未能读取剪贴板，请直接粘贴到输入框")
	}
	return string(data), nil
}
func (d *Desktop) OpenURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" {
		return errors.New("只允许 HTTPS 链接")
	}
	allowed := []string{"accounts.google.com", "colab.research.google.com", "developers.google.com", "github.com", "huggingface.co"}
	for _, host := range allowed {
		if u.Hostname() == host {
			return mygo.Shell.OpenExternal(value)
		}
	}
	return errors.New("不支持的外部链接")
}
func (d *Desktop) ExportConversation(ctx context.Context, content string) (string, error) {
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: mygo.CallerWindow(ctx), DefaultPath: "ColabOne-conversation.md"})
	if err != nil || path == "" {
		return "", err
	}
	return path, os.WriteFile(path, []byte(content), 0600)
}
func main() {
	if len(os.Args) > 1 && os.Args[1] == "--self-test" {
		exe, _ := os.Executable()
		cwd, _ := os.Getwd()
		root, err := rootAt(exe, cwd)
		if err != nil {
			log.Fatal(err)
		}
		py, err := pythonAt(root)
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
	d := &Desktop{bridge: &bridge{}}
	mygo.Bind(d)
	if os.Getenv("MYGO_GENERATE") == "" && !mygo.App.RequestSingleInstanceLock() {
		return
	}
	var win *mygo.Window
	show := func() {
		if win != nil {
			win.Show()
			win.Focus()
			return
		}
		win = mygo.NewWindow(mygo.WindowOptions{Title: "ColabOne", URL: "/", Width: 1200, Height: 820, MinWidth: 900, MinHeight: 620, StateKey: "main", BackgroundColor: "#f7f8fa"})
		win.OnClosed(func() { win = nil })
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
