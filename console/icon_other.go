//go:build !windows

package main

import "github.com/egoist/mygo"

func applyWindowIcon(win *mygo.Window) {
	if win == nil || win.IsDestroyed() {
		return
	}
	_ = win.SetIcon(trayIcon)
}
