//go:build windows

package main

import (
	"unsafe"

	"github.com/egoist/mygo"
)

var (
	procGetWindowRect       = iconUser32.NewProc("GetWindowRect")
	procIsWindowVisible     = iconUser32.NewProc("IsWindowVisible")
	procSetWindowPos        = iconUser32.NewProc("SetWindowPos")
	procSetForegroundWindow = iconUser32.NewProc("SetForegroundWindow")
)

const (
	winSWPNomove     = 0x0002
	winSWPNosize     = 0x0001
	winSWPShowWindow = 0x0040
)

type winRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

// bringColabOneWindowToFront surfaces an already-visible main window above an
// external browser tab (such as after DriveFS browser consent finishes) without
// keeping it pinned AlwaysOnTop.
func bringColabOneWindowToFront(win *mygo.Window) {
	if win == nil || win.IsDestroyed() {
		return
	}
	hwnd := win.NativeHandle()
	if hwnd == 0 {
		return
	}
	if procIsWindowVisible.Find() == nil {
		visible, _, _ := procIsWindowVisible.Call(hwnd)
		if visible == 0 {
			return
		}
	}
	if procSetWindowPos.Find() == nil {
		flags := uintptr(winSWPNomove | winSWPNosize | winSWPShowWindow)
		hwndTopmost := ^uintptr(0)   // HWND_TOPMOST (-1)
		hwndNoTopmost := ^uintptr(1) // HWND_NOTOPMOST (-2)
		_, _, _ = procSetWindowPos.Call(hwnd, hwndTopmost, 0, 0, 0, 0, flags)
		_, _, _ = procSetWindowPos.Call(hwnd, hwndNoTopmost, 0, 0, 0, 0, flags)
	}
	if procSetForegroundWindow.Find() == nil {
		_, _, _ = procSetForegroundWindow.Call(hwnd)
	}
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
