package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

const (
	defaultMainWidth        = 1200
	defaultMainHeight       = 820
	mainMinWidth            = 1024
	mainMinHeight           = 660
	comfyMinWidth           = 960
	comfyMinHeight          = 640
	windowStateFileName     = "window-state.json"
	windowStateSaveDebounce = 300 * time.Millisecond
)

type colabOneWindowStateStore struct {
	mu       sync.Mutex
	pathFn   func() (string, error)
	minSizes map[string]mygo.Size
	states   map[string]mygo.WindowState
	loaded   bool
}

func defaultColabOneWindowStatePath() (string, error) {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, windowStateFileName), nil
}

func newColabOneWindowStateStore(pathFn func() (string, error)) *colabOneWindowStateStore {
	if pathFn == nil {
		pathFn = defaultColabOneWindowStatePath
	}
	return &colabOneWindowStateStore{
		pathFn: pathFn,
		minSizes: map[string]mygo.Size{
			"main":           {Width: mainMinWidth, Height: mainMinHeight},
			"comfyui-canvas": {Width: comfyMinWidth, Height: comfyMinHeight},
		},
		states: map[string]mygo.WindowState{},
	}
}

func (s *colabOneWindowStateStore) sanitize(key string, st mygo.WindowState) mygo.WindowState {
	if st.Width <= 0 || st.Height <= 0 {
		return st
	}
	if minSz, ok := s.minSizes[key]; ok {
		if minSz.Width > 0 && st.Width < minSz.Width {
			st.Width = minSz.Width
			if st.PixelWidth > 0 && st.PixelWidth < st.Width {
				st.PixelWidth = st.Width
			}
		}
		if minSz.Height > 0 && st.Height < minSz.Height {
			st.Height = minSz.Height
			if st.PixelHeight > 0 && st.PixelHeight < st.Height {
				st.PixelHeight = st.Height
			}
		}
	}
	return st
}

func (s *colabOneWindowStateStore) loadLocked() (map[string]mygo.WindowState, error) {
	if s.loaded {
		return maps.Clone(s.states), nil
	}
	path, err := s.pathFn()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		s.states = map[string]mygo.WindowState{}
		s.loaded = true
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.states = map[string]mygo.WindowState{}
	s.loaded = true
	var raw map[string]mygo.WindowState
	if err := json.Unmarshal(data, &raw); err != nil {
		// Ignore a damaged state file; the next resize or close will rewrite it cleanly.
		return nil, nil
	}
	for key, st := range raw {
		if st.Width <= 0 || st.Height <= 0 {
			continue
		}
		s.states[key] = s.sanitize(key, st)
	}
	return maps.Clone(s.states), nil
}

// LoadWindowStates implements mygo.WindowStateStore.
func (s *colabOneWindowStateStore) LoadWindowStates() (map[string]mygo.WindowState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

// SaveWindowStates implements mygo.WindowStateStore.
func (s *colabOneWindowStateStore) SaveWindowStates(states map[string]mygo.WindowState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.loaded {
		_, _ = s.loadLocked()
	}
	next := maps.Clone(s.states)
	for key, st := range states {
		if st.Width <= 0 || st.Height <= 0 {
			continue
		}
		next[key] = s.sanitize(key, st)
	}
	s.states = next
	return s.writeLocked()
}

// RecordLive merges an in-flight window geometry/state change and flushes it to
// disk immediately so window size is remembered even after force-restarts or
// sudden shutdowns (not only clean close).
func (s *colabOneWindowStateStore) RecordLive(key string, st mygo.WindowState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.loaded {
		_, _ = s.loadLocked()
	}
	prev, hadPrev := s.states[key]
	var merged mygo.WindowState
	if st.Maximized || st.FullScreen {
		if hadPrev && prev.Width > 0 && prev.Height > 0 {
			merged = prev
		} else {
			merged = s.sanitize(key, st)
		}
		merged.Maximized = st.Maximized
		merged.FullScreen = st.FullScreen
	} else {
		if st.Width <= 0 || st.Height <= 0 {
			return nil
		}
		merged = s.sanitize(key, st)
		merged.Maximized = false
		merged.FullScreen = false
	}
	if merged.Width <= 0 || merged.Height <= 0 {
		return nil
	}
	if hadPrev && prev == merged {
		return nil
	}
	s.states[key] = merged
	return s.writeLocked()
}

func (s *colabOneWindowStateStore) writeLocked() error {
	path, err := s.pathFn()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.states, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeColabOneFileAtomic(path, ".window-state-*.tmp", data, 0o644)
}

func trackColabOneWindowState(store *colabOneWindowStateStore, key string, win *mygo.Window, defaultW, defaultH, minW, minH int) {
	if store == nil || win == nil || key == "" {
		return
	}

	var (
		mu     sync.Mutex
		timer  *time.Timer
		closed bool
	)

	stopTimer := func() {
		mu.Lock()
		if timer != nil {
			timer.Stop()
			timer = nil
		}
		mu.Unlock()
	}

	captureOnMain := func() {
		if win.IsDestroyed() || win.IsMinimized() || !win.IsVisible() {
			return
		}
		maximized := win.IsMaximized()
		fullScreen := win.IsFullScreen()
		st := mygo.WindowState{
			Maximized:  maximized,
			FullScreen: fullScreen,
		}
		if maximized || fullScreen {
			st.Width = max(defaultW, minW)
			st.Height = max(defaultH, minH)
		} else {
			b := win.Bounds()
			if b.Width <= 0 || b.Height <= 0 {
				return
			}
			st.X = b.X
			st.Y = b.Y
			st.Width = max(b.Width, minW)
			st.Height = max(b.Height, minH)
			if px, py, pw, ph, ok := windowPixelBounds(win); ok && pw > 0 && ph > 0 {
				st.PixelX = px
				st.PixelY = py
				st.PixelWidth = pw
				st.PixelHeight = ph
			}
		}
		_ = store.RecordLive(key, st)
	}

	scheduleSave := func() {
		mu.Lock()
		if closed {
			mu.Unlock()
			return
		}
		if timer != nil {
			timer.Reset(windowStateSaveDebounce)
			mu.Unlock()
			return
		}
		timer = time.AfterFunc(windowStateSaveDebounce, func() {
			mu.Lock()
			if closed {
				mu.Unlock()
				return
			}
			mu.Unlock()
			mygo.RunOnMain(captureOnMain)
		})
		mu.Unlock()
	}

	win.OnResize(scheduleSave)
	win.OnMove(scheduleSave)
	win.OnMaximize(scheduleSave)
	win.OnUnmaximize(scheduleSave)
	win.OnEnterFullScreen(scheduleSave)
	win.OnLeaveFullScreen(scheduleSave)
	win.OnClose(func(*mygo.CloseEvent) {
		stopTimer()
		captureOnMain()
	})
	win.OnClosed(func() {
		mu.Lock()
		closed = true
		if timer != nil {
			timer.Stop()
			timer = nil
		}
		mu.Unlock()
	})
}
