package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

const (
	colabOneCloseBehaviorAsk        = "ask"
	colabOneCloseBehaviorQuit       = "quit"
	colabOneCloseBehaviorBackground = "background"

	colabOneCloseBehaviorFileName = "close-behavior.json"
)

// ColabOneCloseBehaviorState reports the persisted main-window close policy
// and whether hiding to the system tray / background is available.
type ColabOneCloseBehaviorState struct {
	Behavior      string `json:"behavior"`
	DefaultChoice string `json:"default_choice"`
	CanBackground bool   `json:"can_background"`
}

// ColabOneCloseRequest asks the main window UI to prompt the user to choose
// between quitting immediately and running in the background.
type ColabOneCloseRequest struct {
	DefaultChoice string `json:"default_choice"`
	CanBackground bool   `json:"can_background"`
}

// ColabOneCloseRequestEvent notifies the main window frontend to display the
// quit-vs-background close confirmation dialog.
var ColabOneCloseRequestEvent = mygo.NewEvent[ColabOneCloseRequest]("colabone-close-request")

// ColabOneCloseBehaviorEvent pushes persisted close behavior updates to open
// desktop pages so Settings stays synchronized immediately.
var ColabOneCloseBehaviorEvent = mygo.NewEvent[ColabOneCloseBehaviorState]("colabone-close-behavior")

type colabOneCloseDecision int

const (
	colabOneCloseDecisionQuit colabOneCloseDecision = iota
	colabOneCloseDecisionBackground
	colabOneCloseDecisionPrompt
)

func normalizeColabOneCloseBehavior(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case colabOneCloseBehaviorAsk:
		return colabOneCloseBehaviorAsk, true
	case colabOneCloseBehaviorQuit:
		return colabOneCloseBehaviorQuit, true
	case colabOneCloseBehaviorBackground:
		return colabOneCloseBehaviorBackground, true
	default:
		return "", false
	}
}

func normalizeColabOneCloseAction(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case colabOneCloseBehaviorQuit:
		return colabOneCloseBehaviorQuit, true
	case colabOneCloseBehaviorBackground:
		return colabOneCloseBehaviorBackground, true
	default:
		return "", false
	}
}

func decideColabOneMainWindowClose(behavior string, canBackground, promptReady bool) colabOneCloseDecision {
	norm, ok := normalizeColabOneCloseBehavior(behavior)
	if !ok {
		norm = colabOneCloseBehaviorAsk
	}
	switch norm {
	case colabOneCloseBehaviorQuit:
		return colabOneCloseDecisionQuit
	case colabOneCloseBehaviorBackground:
		if canBackground {
			return colabOneCloseDecisionBackground
		}
		return colabOneCloseDecisionQuit
	default:
		if canBackground && promptReady {
			return colabOneCloseDecisionPrompt
		}
		return colabOneCloseDecisionQuit
	}
}

// colabOneCloseBehaviorStore persists the user's chosen main-window close
// behavior in <configDir>/close-behavior.json so it is immediately available
// to native window-close handlers before WebView or controller startup finishes.
type colabOneCloseBehaviorStore struct {
	mu     sync.Mutex
	pathFn func() (string, error)
}

func defaultColabOneCloseBehaviorPath() (string, error) {
	dir := strings.TrimSpace(colabOneConfigDir())
	if dir == "" {
		var err error
		dir, err = mygo.App.Path(mygo.PathUserData)
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(dir, colabOneCloseBehaviorFileName), nil
}

func newColabOneCloseBehaviorStore(pathFn func() (string, error)) *colabOneCloseBehaviorStore {
	if pathFn == nil {
		pathFn = defaultColabOneCloseBehaviorPath
	}
	return &colabOneCloseBehaviorStore{pathFn: pathFn}
}

func (s *colabOneCloseBehaviorStore) Load() string {
	if s == nil {
		return colabOneCloseBehaviorAsk
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.pathFn()
	if err != nil || strings.TrimSpace(path) == "" {
		return colabOneCloseBehaviorAsk
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) || err != nil {
		return colabOneCloseBehaviorAsk
	}
	var payload struct {
		Behavior string `json:"behavior"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return colabOneCloseBehaviorAsk
	}
	if norm, ok := normalizeColabOneCloseBehavior(payload.Behavior); ok {
		return norm
	}
	return colabOneCloseBehaviorAsk
}

func (s *colabOneCloseBehaviorStore) Save(value string) error {
	if s == nil {
		return nil
	}
	norm, ok := normalizeColabOneCloseBehavior(value)
	if !ok {
		return errors.New(colabOneT("err.unsupportedCloseBehavior"))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.pathFn()
	if err != nil || strings.TrimSpace(path) == "" {
		if err != nil {
			return err
		}
		return errors.New("close behavior store path is empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(struct {
		Behavior string `json:"behavior"`
	}{Behavior: norm}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmpFile, err := os.CreateTemp(dir, ".close-behavior-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	closed := false
	renamed := false
	defer func() {
		if !closed {
			_ = tmpFile.Close()
		}
		if !renamed {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmpFile.Write(data); err != nil {
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		return err
	}
	if err := tmpFile.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	var renameErr error
	for range 4 {
		renameErr = os.Rename(tmpPath, path)
		if renameErr == nil {
			renamed = true
			return nil
		}
		time.Sleep(15 * time.Millisecond)
	}
	_ = os.Remove(tmpPath)
	renamed = true
	return os.WriteFile(path, data, 0o644)
}

func (d *ColabOneDesktop) initCloseBehavior(pathFn func() (string, error)) {
	store := newColabOneCloseBehaviorStore(pathFn)
	loaded := store.Load()
	d.closeBehaviorMu.Lock()
	d.closeBehaviorStore = store
	d.closeBehavior = loaded
	d.closeBehaviorMu.Unlock()
}

func (d *ColabOneDesktop) currentCloseBehavior() string {
	d.closeBehaviorMu.RLock()
	behavior := d.closeBehavior
	d.closeBehaviorMu.RUnlock()
	if norm, ok := normalizeColabOneCloseBehavior(behavior); ok {
		return norm
	}
	return colabOneCloseBehaviorAsk
}

func (d *ColabOneDesktop) canRunInBackground() bool {
	if d.canBackgroundFn != nil {
		return d.canBackgroundFn()
	}
	if runtime.GOOS == "darwin" {
		return true
	}
	d.trayMu.Lock()
	hasTray := d.tray != nil
	d.trayMu.Unlock()
	return hasTray
}

func (d *ColabOneDesktop) closeBehaviorState() ColabOneCloseBehaviorState {
	return ColabOneCloseBehaviorState{
		Behavior:      d.currentCloseBehavior(),
		DefaultChoice: colabOneCloseBehaviorQuit,
		CanBackground: d.canRunInBackground(),
	}
}

func (d *ColabOneDesktop) isStartupActive() bool {
	d.splashMu.Lock()
	startup := d.startup
	d.splashMu.Unlock()
	return startup != nil && startup.IsActive()
}

func (d *ColabOneDesktop) hideMainWindow() {
	if d.hideMainFn != nil {
		d.hideMainFn()
		return
	}
	mygo.RunOnMain(func() {
		win := d.mainWindow()
		if win != nil && !win.IsDestroyed() {
			win.Hide()
		}
	})
}

func (d *ColabOneDesktop) quitApplication() {
	if d.quitAppFn != nil {
		d.quitAppFn()
		return
	}
	mygo.App.Quit()
}

func (d *ColabOneDesktop) emitCloseRequest(win *mygo.Window, req ColabOneCloseRequest) error {
	if d.emitCloseReqFn != nil {
		return d.emitCloseReqFn(win, req)
	}
	if win == nil || win.IsDestroyed() {
		return errors.New("main window unavailable")
	}
	return ColabOneCloseRequestEvent.Emit(win, req)
}

// handleMainWindowClose intercepts user-initiated main window close attempts
// (titlebar close button, Alt+F4, Windows taskbar "Close window", macOS Cmd+W)
// and enforces the configured close behavior. App-level quits (tray Quit,
// Cmd+Q, updater relaunch, OS shutdown) bypass this handler via appQuitting.
func (d *ColabOneDesktop) handleMainWindowClose(win *mygo.Window, e *mygo.CloseEvent) {
	if e == nil || d.appQuitting.Load() {
		return
	}
	canBg := d.canRunInBackground()
	promptReady := d.closePromptReady.Load() && !d.isStartupActive()
	decision := decideColabOneMainWindowClose(d.currentCloseBehavior(), canBg, promptReady)
	switch decision {
	case colabOneCloseDecisionBackground:
		e.PreventDefault()
		if d.hideMainFn != nil {
			d.hideMainFn()
		} else if win != nil && !win.IsDestroyed() {
			win.Hide()
		}
	case colabOneCloseDecisionPrompt:
		e.PreventDefault()
		if win != nil && !win.IsDestroyed() {
			revealColabOneWindow(win)
		}
		err := d.emitCloseRequest(win, ColabOneCloseRequest{
			DefaultChoice: colabOneCloseBehaviorQuit,
			CanBackground: canBg,
		})
		if err != nil {
			d.quitApplication()
		}
	default:
		e.PreventDefault()
		d.quitApplication()
	}
}

// GetCloseBehavior returns the current main-window close policy and registers
// the calling main window as ready to display the close confirmation modal.
func (d *ColabOneDesktop) GetCloseBehavior(ctx context.Context) ColabOneCloseBehaviorState {
	if ctx != nil {
		if caller := mygo.CallerWindow(ctx); caller != nil && caller == d.mainWindow() {
			d.closePromptReady.Store(true)
		}
	}
	return d.closeBehaviorState()
}

// SetCloseBehavior updates and persists the main-window close policy ("ask",
// "quit", or "background").
func (d *ColabOneDesktop) SetCloseBehavior(value string) (ColabOneCloseBehaviorState, error) {
	norm, ok := normalizeColabOneCloseBehavior(value)
	if !ok {
		return d.closeBehaviorState(), errors.New(colabOneT("err.unsupportedCloseBehavior"))
	}
	if norm == colabOneCloseBehaviorBackground && !d.canRunInBackground() {
		return d.closeBehaviorState(), errors.New(colabOneT("err.backgroundUnavailable"))
	}
	d.closeBehaviorWriteMu.Lock()
	defer d.closeBehaviorWriteMu.Unlock()
	if norm == colabOneCloseBehaviorBackground && !d.canRunInBackground() {
		return d.closeBehaviorState(), errors.New(colabOneT("err.backgroundUnavailable"))
	}
	d.closeBehaviorMu.RLock()
	store := d.closeBehaviorStore
	d.closeBehaviorMu.RUnlock()
	if store != nil {
		if err := store.Save(norm); err != nil {
			return d.closeBehaviorState(), err
		}
	}
	d.closeBehaviorMu.Lock()
	d.closeBehavior = norm
	d.closeBehaviorMu.Unlock()
	state := d.closeBehaviorState()
	if mygo.App.IsReady() {
		_ = ColabOneCloseBehaviorEvent.Broadcast(state)
	}
	return state, nil
}

// ResolveCloseRequest executes the user's decision from the close prompt
// ("quit" or "background") and optionally persists it as the future default.
func (d *ColabOneDesktop) ResolveCloseRequest(action string, remember bool) error {
	norm, ok := normalizeColabOneCloseAction(action)
	if !ok {
		return errors.New(colabOneT("err.unsupportedCloseAction"))
	}
	if norm == colabOneCloseBehaviorBackground && !d.canRunInBackground() {
		return errors.New(colabOneT("err.backgroundUnavailable"))
	}
	if remember {
		if _, err := d.SetCloseBehavior(norm); err != nil {
			if norm != colabOneCloseBehaviorQuit {
				return err
			}
		}
	}
	if norm == colabOneCloseBehaviorBackground {
		d.hideMainWindow()
		return nil
	}
	d.quitApplication()
	return nil
}
