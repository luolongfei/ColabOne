package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

func TestColabOneCloseBehaviorStoreRoundTripAndCorrupt(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "nested", colabOneCloseBehaviorFileName)
	store := newColabOneCloseBehaviorStore(func() (string, error) { return storePath, nil })

	// Default behavior before any user choice is "ask".
	if got := store.Load(); got != colabOneCloseBehaviorAsk {
		t.Fatalf("initial Load() = %q, want %q", got, colabOneCloseBehaviorAsk)
	}

	for _, want := range []string{
		colabOneCloseBehaviorQuit,
		colabOneCloseBehaviorBackground,
		colabOneCloseBehaviorAsk,
	} {
		if err := store.Save(want); err != nil {
			t.Fatalf("Save(%q) failed: %v", want, err)
		}
		reloaded := newColabOneCloseBehaviorStore(func() (string, error) { return storePath, nil })
		if got := reloaded.Load(); got != want {
			t.Fatalf("reloaded Load() = %q, want %q", got, want)
		}
	}

	// Unsupported values are rejected without clobbering disk state.
	if err := store.Save("quit"); err != nil {
		t.Fatalf("Save(quit) failed: %v", err)
	}
	if err := store.Save("bogus"); err == nil {
		t.Fatal("expected Save(bogus) to fail")
	}
	if got := store.Load(); got != colabOneCloseBehaviorQuit {
		t.Fatalf("Load() after rejected Save = %q, want %q", got, colabOneCloseBehaviorQuit)
	}

	// Corrupt JSON or unknown persisted values fall back safely to "ask".
	if err := os.WriteFile(storePath, []byte("{bad-json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := store.Load(); got != colabOneCloseBehaviorAsk {
		t.Fatalf("Load() on corrupt JSON = %q, want %q", got, colabOneCloseBehaviorAsk)
	}

	if err := os.WriteFile(storePath, []byte(`{"behavior":"unknown"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := store.Load(); got != colabOneCloseBehaviorAsk {
		t.Fatalf("Load() on unknown behavior = %q, want %q", got, colabOneCloseBehaviorAsk)
	}
}

func TestDecideColabOneMainWindowClose(t *testing.T) {
	cases := []struct {
		name          string
		behavior      string
		canBackground bool
		promptReady   bool
		want          colabOneCloseDecision
	}{
		{"ask prompts when ready and tray available", colabOneCloseBehaviorAsk, true, true, colabOneCloseDecisionPrompt},
		{"ask defaults to quit when prompt not ready", colabOneCloseBehaviorAsk, true, false, colabOneCloseDecisionQuit},
		{"ask defaults to quit when tray unavailable", colabOneCloseBehaviorAsk, false, true, colabOneCloseDecisionQuit},
		{"quit always quits", colabOneCloseBehaviorQuit, true, true, colabOneCloseDecisionQuit},
		{"background hides when tray available", colabOneCloseBehaviorBackground, true, false, colabOneCloseDecisionBackground},
		{"background falls back to quit without tray", colabOneCloseBehaviorBackground, false, true, colabOneCloseDecisionQuit},
		{"unknown defaults to ask prompt when ready", "", true, true, colabOneCloseDecisionPrompt},
		{"unknown defaults to quit when unready", "unexpected", true, false, colabOneCloseDecisionQuit},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideColabOneMainWindowClose(tc.behavior, tc.canBackground, tc.promptReady)
			if got != tc.want {
				t.Fatalf("decideColabOneMainWindowClose(%q, %v, %v) = %v, want %v",
					tc.behavior, tc.canBackground, tc.promptReady, got, tc.want)
			}
		})
	}
}

func TestColabOneDesktopCloseLifecycleAndResolve(t *testing.T) {
	prevLocale := colabOneGetLocale()
	t.Cleanup(func() { colabOneSetLocaleState(prevLocale) })
	colabOneSetLocaleState(colabOneLocaleZhCN)

	storePath := filepath.Join(t.TempDir(), colabOneCloseBehaviorFileName)
	canBg := true
	hideCount := 0
	quitCount := 0
	var emitted []ColabOneCloseRequest
	var emitErr error

	d := &ColabOneDesktop{
		canBackgroundFn: func() bool { return canBg },
		hideMainFn:      func() { hideCount++ },
		quitAppFn:       func() { quitCount++ },
		emitCloseReqFn: func(_ *mygo.Window, req ColabOneCloseRequest) error {
			if emitErr != nil {
				return emitErr
			}
			emitted = append(emitted, req)
			return nil
		},
	}
	d.initCloseBehavior(func() (string, error) { return storePath, nil })

	// Initial state defaults to "ask" with default choice "quit".
	st := d.GetCloseBehavior(context.Background())
	if st.Behavior != colabOneCloseBehaviorAsk || st.DefaultChoice != colabOneCloseBehaviorQuit || !st.CanBackground {
		t.Fatalf("unexpected initial state: %+v", st)
	}

	// 1. User closes window while UI is ready and behavior is "ask": prompts with quit default.
	d.closePromptReady.Store(true)
	ev := &mygo.CloseEvent{}
	d.handleMainWindowClose(nil, ev)
	if !ev.DefaultPrevented() {
		t.Fatal("expected CloseEvent to be prevented when prompting")
	}
	if len(emitted) != 1 || emitted[0].DefaultChoice != colabOneCloseBehaviorQuit || !emitted[0].CanBackground {
		t.Fatalf("unexpected emitted close requests: %+v", emitted)
	}
	if hideCount != 0 || quitCount != 0 {
		t.Fatalf("expected no hide/quit during prompt, got hide=%d quit=%d", hideCount, quitCount)
	}

	// 2. If emitting the prompt fails, fall back cleanly to quitting so user is never stuck.
	emitErr = errors.New("webview detached")
	ev = &mygo.CloseEvent{}
	d.handleMainWindowClose(nil, ev)
	if !ev.DefaultPrevented() || quitCount != 1 {
		t.Fatalf("expected fallback quit on emit failure, prevented=%v quitCount=%d", ev.DefaultPrevented(), quitCount)
	}
	emitErr = nil

	// 3. While startup splash is active, do not prompt — default directly to quit.
	startup := newColabOneStartupCoordinator(colabOneStartupConfig{
		MinDisplay:   10 * time.Second,
		PageFailSafe: 10 * time.Second,
		HardFailSafe: 10 * time.Second,
	})
	startup.Start()
	defer startup.Cancel()
	d.splashMu.Lock()
	d.startup = startup
	d.splashMu.Unlock()

	ev = &mygo.CloseEvent{}
	d.handleMainWindowClose(nil, ev)
	if !ev.DefaultPrevented() || quitCount != 2 || len(emitted) != 1 {
		t.Fatalf("expected direct quit during active startup, prevented=%v quitCount=%d emitted=%d",
			ev.DefaultPrevented(), quitCount, len(emitted))
	}
	d.splashMu.Lock()
	d.startup = nil
	d.splashMu.Unlock()

	// 4. ResolveCloseRequest("background", false) hides without remembering.
	if err := d.ResolveCloseRequest(colabOneCloseBehaviorBackground, false); err != nil {
		t.Fatalf("ResolveCloseRequest(background, false) failed: %v", err)
	}
	if hideCount != 1 || d.GetCloseBehavior(context.Background()).Behavior != colabOneCloseBehaviorAsk {
		t.Fatalf("expected hide=1 and behavior still ask, got hide=%d behavior=%q",
			hideCount, d.GetCloseBehavior(context.Background()).Behavior)
	}

	// 5. ResolveCloseRequest("background", true) persists and hides; subsequent close hides directly.
	if err := d.ResolveCloseRequest(colabOneCloseBehaviorBackground, true); err != nil {
		t.Fatalf("ResolveCloseRequest(background, true) failed: %v", err)
	}
	if hideCount != 2 || d.GetCloseBehavior(context.Background()).Behavior != colabOneCloseBehaviorBackground {
		t.Fatalf("expected hide=2 and persisted background, got hide=%d behavior=%q",
			hideCount, d.GetCloseBehavior(context.Background()).Behavior)
	}
	ev = &mygo.CloseEvent{}
	d.handleMainWindowClose(nil, ev)
	if !ev.DefaultPrevented() || hideCount != 3 || len(emitted) != 1 {
		t.Fatalf("expected remembered background close to hide directly, prevented=%v hide=%d emitted=%d",
			ev.DefaultPrevented(), hideCount, len(emitted))
	}

	// 6. Settings change to "quit" persists across store reload and quits on close.
	updated, err := d.SetCloseBehavior(colabOneCloseBehaviorQuit)
	if err != nil {
		t.Fatalf("SetCloseBehavior(quit) failed: %v", err)
	}
	if updated.Behavior != colabOneCloseBehaviorQuit {
		t.Fatalf("SetCloseBehavior returned %+v", updated)
	}
	ev = &mygo.CloseEvent{}
	d.handleMainWindowClose(nil, ev)
	if !ev.DefaultPrevented() || quitCount != 3 {
		t.Fatalf("expected remembered quit to quit directly, prevented=%v quitCount=%d",
			ev.DefaultPrevented(), quitCount)
	}

	// 7. ResolveCloseRequest("quit", true) from "ask" persists "quit" and quits.
	if _, err := d.SetCloseBehavior(colabOneCloseBehaviorAsk); err != nil {
		t.Fatalf("SetCloseBehavior(ask) failed: %v", err)
	}
	if err := d.ResolveCloseRequest(colabOneCloseBehaviorQuit, true); err != nil {
		t.Fatalf("ResolveCloseRequest(quit, true) failed: %v", err)
	}
	if quitCount != 4 || d.GetCloseBehavior(context.Background()).Behavior != colabOneCloseBehaviorQuit {
		t.Fatalf("expected quit=4 and persisted quit, got quit=%d behavior=%q",
			quitCount, d.GetCloseBehavior(context.Background()).Behavior)
	}
	// Verify persistence on disk with fresh store.
	if diskVal := newColabOneCloseBehaviorStore(func() (string, error) { return storePath, nil }).Load(); diskVal != colabOneCloseBehaviorQuit {
		t.Fatalf("persisted disk behavior = %q, want %q", diskVal, colabOneCloseBehaviorQuit)
	}

	// 8. App-level quit (tray Quit / Cmd+Q / Relaunch / OS shutdown) must bypass OnClose interception.
	if _, err := d.SetCloseBehavior(colabOneCloseBehaviorBackground); err != nil {
		t.Fatalf("SetCloseBehavior(background) failed: %v", err)
	}
	d.appQuitting.Store(true)
	ev = &mygo.CloseEvent{}
	d.handleMainWindowClose(nil, ev)
	if ev.DefaultPrevented() || hideCount != 3 || quitCount != 4 {
		t.Fatalf("expected appQuitting to bypass interception, prevented=%v hide=%d quit=%d",
			ev.DefaultPrevented(), hideCount, quitCount)
	}
	d.appQuitting.Store(false)

	// 9. Validation errors & unavailable background mode.
	if _, err := d.SetCloseBehavior("invalid"); err == nil {
		t.Fatal("expected error for invalid SetCloseBehavior")
	}
	if err := d.ResolveCloseRequest("ask", false); err == nil {
		t.Fatal("expected error for invalid ResolveCloseRequest action")
	}
	canBg = false
	if _, err := d.SetCloseBehavior(colabOneCloseBehaviorBackground); err == nil {
		t.Fatal("expected error when setting background without tray support")
	}
	if err := d.ResolveCloseRequest(colabOneCloseBehaviorBackground, false); err == nil {
		t.Fatal("expected error when resolving background without tray support")
	}
}

func TestColabOneDesktopCloseBehaviorConcurrentDiskMemoryConsistency(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), colabOneCloseBehaviorFileName)
	d := &ColabOneDesktop{
		canBackgroundFn: func() bool { return true },
		hideMainFn:      func() {},
		quitAppFn:       func() {},
		emitCloseReqFn:  func(*mygo.Window, ColabOneCloseRequest) error { return nil },
	}
	d.initCloseBehavior(func() (string, error) { return storePath, nil })
	d.closePromptReady.Store(true)

	values := []string{
		colabOneCloseBehaviorAsk,
		colabOneCloseBehaviorQuit,
		colabOneCloseBehaviorBackground,
	}

	var wg sync.WaitGroup
	for g := range 6 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := range 20 {
				val := values[(id+i)%len(values)]
				if _, err := d.SetCloseBehavior(val); err != nil {
					t.Errorf("SetCloseBehavior(%q) failed: %v", val, err)
					return
				}
				_ = d.GetCloseBehavior(context.Background())
				ev := &mygo.CloseEvent{}
				d.handleMainWindowClose(nil, ev)
			}
		}(g)
	}
	wg.Wait()

	memVal := d.GetCloseBehavior(context.Background()).Behavior
	diskVal := newColabOneCloseBehaviorStore(func() (string, error) { return storePath, nil }).Load()
	if memVal != diskVal {
		t.Fatalf("memory/disk divergence after concurrent updates: mem=%q disk=%q", memVal, diskVal)
	}
}
