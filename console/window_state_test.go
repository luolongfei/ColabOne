package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"
)

func TestColabOneWindowStateStoreRoundTripAndRestart(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "nested", "window-state.json")
	store := newColabOneWindowStateStore(func() (string, error) { return statePath, nil })

	// First launch has no saved state yet.
	initial, err := store.LoadWindowStates()
	if err != nil {
		t.Fatalf("LoadWindowStates initial error: %v", err)
	}
	if len(initial) != 0 {
		t.Fatalf("expected empty initial state, got %+v", initial)
	}

	// Resizing/moving while open flushes immediately to disk.
	want := mygo.WindowState{
		X:           140,
		Y:           90,
		Width:       1460,
		Height:      920,
		PixelX:      140,
		PixelY:      90,
		PixelWidth:  1460,
		PixelHeight: 920,
	}
	if err := store.RecordLive("main", want); err != nil {
		t.Fatalf("RecordLive error: %v", err)
	}

	// Simulate app restart with a brand new store instance reading the same file.
	restarted := newColabOneWindowStateStore(func() (string, error) { return statePath, nil })
	loaded, err := restarted.LoadWindowStates()
	if err != nil {
		t.Fatalf("LoadWindowStates after restart error: %v", err)
	}
	if got := loaded["main"]; got != want {
		t.Fatalf("restarted main state = %+v, want %+v", got, want)
	}
}

func TestColabOneWindowStateStorePreservesNormalBoundsAcrossMaximize(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "window-state.json")
	store := newColabOneWindowStateStore(func() (string, error) { return statePath, nil })

	normal := mygo.WindowState{X: 200, Y: 100, Width: 1380, Height: 860}
	if err := store.RecordLive("main", normal); err != nil {
		t.Fatalf("RecordLive normal error: %v", err)
	}
	if err := store.RecordLive("main", mygo.WindowState{
		Width:     defaultMainWidth,
		Height:    defaultMainHeight,
		Maximized: true,
	}); err != nil {
		t.Fatalf("RecordLive maximized error: %v", err)
	}

	restarted := newColabOneWindowStateStore(func() (string, error) { return statePath, nil })
	loaded, err := restarted.LoadWindowStates()
	if err != nil {
		t.Fatalf("LoadWindowStates error: %v", err)
	}
	got := loaded["main"]
	if !got.Maximized {
		t.Fatalf("expected Maximized=true after restart, got %+v", got)
	}
	if got.Width != 1380 || got.Height != 860 || got.X != 200 || got.Y != 100 {
		t.Fatalf("expected normal bounds preserved across maximize, got %+v", got)
	}

	// Unmaximizing with a new resized normal geometry clears Maximized and updates size.
	unmax := mygo.WindowState{X: 160, Y: 70, Width: 1520, Height: 900, Maximized: false}
	if err := restarted.RecordLive("main", unmax); err != nil {
		t.Fatalf("RecordLive unmaximized error: %v", err)
	}
	third := newColabOneWindowStateStore(func() (string, error) { return statePath, nil })
	loaded2, err := third.LoadWindowStates()
	if err != nil {
		t.Fatalf("LoadWindowStates third error: %v", err)
	}
	if got2 := loaded2["main"]; got2 != unmax {
		t.Fatalf("state after unmaximize = %+v, want %+v", got2, unmax)
	}
}

func TestColabOneWindowStateStoreClampsMinimumDimensions(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "window-state.json")
	// Simulate an older persisted file saved before minimum dimensions were raised.
	legacy := `{"main":{"x":50,"y":60,"width":800,"height":500,"pixelX":50,"pixelY":60,"pixelWidth":800,"pixelHeight":500}}`
	if err := os.WriteFile(statePath, []byte(legacy), 0o644); err != nil {
		t.Fatalf("WriteFile legacy error: %v", err)
	}

	store := newColabOneWindowStateStore(func() (string, error) { return statePath, nil })
	loaded, err := store.LoadWindowStates()
	if err != nil {
		t.Fatalf("LoadWindowStates error: %v", err)
	}
	got := loaded["main"]
	if got.Width != mainMinWidth || got.Height != mainMinHeight {
		t.Fatalf("clamped main size = %dx%d, want %dx%d", got.Width, got.Height, mainMinWidth, mainMinHeight)
	}
	if got.PixelWidth < mainMinWidth || got.PixelHeight < mainMinHeight {
		t.Fatalf("clamped pixel size = %dx%d, want >= %dx%d", got.PixelWidth, got.PixelHeight, mainMinWidth, mainMinHeight)
	}
}

func TestColabOneWindowStateStoreHandlesCorruptFileGracefully(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "window-state.json")
	if err := os.WriteFile(statePath, []byte("{not-json"), 0o644); err != nil {
		t.Fatalf("WriteFile corrupt error: %v", err)
	}

	store := newColabOneWindowStateStore(func() (string, error) { return statePath, nil })
	loaded, err := store.LoadWindowStates()
	if err != nil {
		t.Fatalf("expected corrupt file to be ignored gracefully, got err: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected empty map for corrupt file, got %+v", loaded)
	}

	// Next write should recover and rewrite cleanly.
	if err := store.RecordLive("main", mygo.WindowState{X: 10, Y: 20, Width: 1280, Height: 840}); err != nil {
		t.Fatalf("RecordLive recovery error: %v", err)
	}
	restarted := newColabOneWindowStateStore(func() (string, error) { return statePath, nil })
	loaded2, err := restarted.LoadWindowStates()
	if err != nil {
		t.Fatalf("LoadWindowStates recovery error: %v", err)
	}
	if loaded2["main"].Width != 1280 || loaded2["main"].Height != 840 {
		t.Fatalf("unexpected recovered state: %+v", loaded2["main"])
	}
}
