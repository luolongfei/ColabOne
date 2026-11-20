//go:build windows

package main

import (
	"unsafe"

	"github.com/egoist/mygo"
)

var procGetWindowRect = iconUser32.NewProc("GetWindowRect")

type winRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

// windowPixelBounds reads the exact screen-pixel window rectangle on Windows so
// mygo's multi-DPI RestoreWindowState places the window back on the same display.
func windowPixelBounds(win *mygo.Window) (x, y, w, h int, ok bool) {
	if win == nil || win.IsDestroyed() {
		return 0, 0, 0, 0, false
	}
	hwnd := win.NativeHandle()
	if hwnd == 0 || procGetWindowRect.Find() != nil {
		return 0, 0, 0, 0, false
	}
	var r winRect
	ret, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	if ret == 0 {
		return 0, 0, 0, 0, false
	}
	w = int(r.Right - r.Left)
	h = int(r.Bottom - r.Top)
	if w <= 0 || h <= 0 {
		return 0, 0, 0, 0, false
	}
	return int(r.Left), int(r.Top), w, h, true
}
