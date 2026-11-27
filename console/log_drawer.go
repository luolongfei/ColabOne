package main

import (
	"context"

	"github.com/egoist/mygo"
)

const (
	minLogDrawerWidth     = 320
	maxLogDrawerWidth     = 640
	defaultLogDrawerWidth = 420
	minLogWindowHeight    = 420
)

// ColabOneLogDrawerState reports the current native standalone log window state.
type ColabOneLogDrawerState struct {
	Open       bool `json:"open"`
	Standalone bool `json:"standalone"`
	Expanded   bool `json:"expanded"`
	Delta      int  `json:"delta"`
}

func clampColabOneLogDrawerWidth(width int) int {
	if width <= 0 {
		return defaultLogDrawerWidth
	}
	if width < minLogDrawerWidth {
		return minLogDrawerWidth
	}
	if width > maxLogDrawerWidth {
		return maxLogDrawerWidth
	}
	return width
}

// planColabOneLogWindowBounds computes the position and size of the standalone
// runtime log window so it opens cleanly side-by-side next to the main window
// without modifying or overlapping the main window whenever display space permits.
func planColabOneLogWindowBounds(mainBounds, workArea mygo.Rectangle, requestedWidth int) mygo.Rectangle {
	w := clampColabOneLogDrawerWidth(requestedWidth)
	h := mainBounds.Height
	if h <= 0 {
		h = 820
	}
	y := mainBounds.Y
	if workArea.Height > 0 {
		minH := min(minLogWindowHeight, workArea.Height)
		if h < minH {
			h = minH
		}
		if h > workArea.Height {
			h = workArea.Height
		}
		if y < workArea.Y {
			y = workArea.Y
		}
		workBottom := workArea.Y + workArea.Height
		if y+h > workBottom {
			y = max(workArea.Y, workBottom-h)
		}
	}

	rightEdge := mainBounds.X + mainBounds.Width
	if workArea.Width <= 0 {
		return mygo.Rectangle{X: rightEdge, Y: y, Width: w, Height: h}
	}

	workRight := workArea.X + workArea.Width
	if rightEdge >= workArea.X && rightEdge <= workRight {
		roomRight := workRight - rightEdge
		if roomRight >= w {
			return mygo.Rectangle{X: rightEdge, Y: y, Width: w, Height: h}
		}
		if roomRight >= minLogDrawerWidth {
			return mygo.Rectangle{X: rightEdge, Y: y, Width: roomRight, Height: h}
		}
	}

	if mainBounds.X >= workArea.X && mainBounds.X <= workRight {
		roomLeft := mainBounds.X - workArea.X
		if roomLeft >= minLogDrawerWidth {
			lw := min(w, roomLeft)
			return mygo.Rectangle{X: mainBounds.X - lw, Y: y, Width: lw, Height: h}
		}
	}

	fw := min(w, workArea.Width)
	return mygo.Rectangle{
		X:      max(workArea.X, workRight-fw),
		Y:      y,
		Width:  fw,
		Height: h,
	}
}

func (d *ColabOneDesktop) setMainWindow(win *mygo.Window) {
	d.mainMu.Lock()
	d.mainWin = win
	d.mainMu.Unlock()
}

func (d *ColabOneDesktop) mainWindow() *mygo.Window {
	d.mainMu.Lock()
	win := d.mainWin
	d.mainMu.Unlock()
	return win
}

func (d *ColabOneDesktop) clearMainWindow(win *mygo.Window) {
	d.mainMu.Lock()
	if d.mainWin == win {
		d.mainWin = nil
	}
	d.mainMu.Unlock()
}

func (d *ColabOneDesktop) setLogWindow(win *mygo.Window) {
	d.logMu.Lock()
	d.logWin = win
	d.logMu.Unlock()
}

func (d *ColabOneDesktop) logWindow() *mygo.Window {
	d.logMu.Lock()
	win := d.logWin
	d.logMu.Unlock()
	return win
}

func (d *ColabOneDesktop) clearLogWindow(win *mygo.Window) {
	d.logMu.Lock()
	if d.logWin == win {
		d.logWin = nil
	}
	d.logMu.Unlock()
}

func (d *ColabOneDesktop) notifyMainLogWindowState(open bool) {
	if d.appQuitting.Load() || !d.isAppReady() {
		return
	}
	gen := d.logStateGen.Add(1)
	win := d.mainWindow()
	if win == nil || win.IsDestroyed() {
		return
	}
	page := win.Page()
	if page == nil {
		return
	}
	script := "window.dispatchEvent(new CustomEvent('colabone:log-window-state',{detail:{open:false}}))"
	if open {
		script = "window.dispatchEvent(new CustomEvent('colabone:log-window-state',{detail:{open:true}}))"
	}
	go func() {
		if d.appQuitting.Load() || !d.isAppReady() || d.logStateGen.Load() != gen {
			return
		}
		if win.IsDestroyed() {
			return
		}
		_, _ = page.Eval(script)
	}()
}

// GetLogDrawerState reports whether the standalone runtime log window is open.
func (d *ColabOneDesktop) GetLogDrawerState() ColabOneLogDrawerState {
	win := d.logWindow()
	open := win != nil && !win.IsDestroyed()
	return ColabOneLogDrawerState{Open: open, Standalone: true}
}

// SetLogDrawer opens or closes the standalone native runtime log window
// positioned adjacent to the main window. The log window can be closed,
// resized, moved, and maximized independently of the main window.
func (d *ColabOneDesktop) SetLogDrawer(ctx context.Context, open bool, width int) ColabOneLogDrawerState {
	if d.appQuitting.Load() {
		return ColabOneLogDrawerState{Open: false, Standalone: true}
	}
	mainWin := d.mainWindow()
	if mainWin == nil && ctx != nil {
		mainWin = mygo.CallerWindow(ctx)
	}
	if mainWin == nil && d.logWindow() == nil {
		return ColabOneLogDrawerState{Open: open}
	}
	if !d.isAppReady() {
		return ColabOneLogDrawerState{Open: false, Standalone: true}
	}
	var state ColabOneLogDrawerState
	mygo.RunOnMain(func() {
		if d.appQuitting.Load() || !d.isAppReady() {
			state = ColabOneLogDrawerState{Open: false, Standalone: true}
			return
		}
		state = d.applyLogWindowOnMain(mainWin, open, width)
	})
	return state
}

// applyLogWindowOnMain must be called on the UI main thread.
func (d *ColabOneDesktop) applyLogWindowOnMain(mainWin *mygo.Window, open bool, width int) ColabOneLogDrawerState {
	existing := d.logWindow()
	if !open {
		if existing != nil && !existing.IsDestroyed() {
			existing.Close()
		}
		return ColabOneLogDrawerState{Open: false, Standalone: true}
	}

	if existing != nil && !existing.IsDestroyed() {
		revealColabOneWindow(existing)
		return ColabOneLogDrawerState{Open: true, Standalone: true}
	}

	var mainBounds, workArea mygo.Rectangle
	if mainWin != nil && !mainWin.IsDestroyed() && !mainWin.IsMinimized() && mainWin.IsVisible() {
		mainBounds = mainWin.Bounds()
		workArea = mygo.Screen.DisplayMatching(mainBounds).WorkArea
	} else {
		workArea = mygo.Screen.PrimaryDisplay().WorkArea
		mainBounds = mygo.Rectangle{X: workArea.X, Y: workArea.Y, Width: 0, Height: min(820, workArea.Height)}
	}
	rect := planColabOneLogWindowBounds(mainBounds, workArea, width)

	logWin := mygo.NewWindow(mygo.WindowOptions{
		Title:           colabOneT("window.logs"),
		URL:             "/?view=logs",
		X:               rect.X,
		Y:               rect.Y,
		Width:           rect.Width,
		Height:          rect.Height,
		MinWidth:        minLogDrawerWidth,
		MinHeight:       minLogWindowHeight,
		BackgroundColor: colabOneWindowBackground(),
	})
	applyWindowIcon(logWin)
	logWin.SetBounds(rect)
	d.setLogWindow(logWin)
	d.logStateGen.Add(1)
	logWin.OnClosed(func() {
		d.clearLogWindow(logWin)
		d.notifyMainLogWindowState(false)
	})
	if logWin.IsDestroyed() {
		d.clearLogWindow(logWin)
		return ColabOneLogDrawerState{Open: false, Standalone: true}
	}
	return ColabOneLogDrawerState{Open: true, Standalone: true}
}
