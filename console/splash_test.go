package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestColabOneAppVersionFromManifest(t *testing.T) {
	v := colabOneAppVersion()
	if !strings.HasPrefix(v, "v") || len(v) < 2 {
		t.Fatalf("unexpected version string %q", v)
	}
}

func TestColabOneTrimIconImageCropsTransparentGutter(t *testing.T) {
	cropped, err := colabOneTrimIconImage(trayIcon)
	if err != nil {
		t.Fatalf("colabOneTrimIconImage failed: %v", err)
	}
	b := cropped.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		t.Fatalf("expected non-empty cropped icon, got %v", b)
	}
	if b.Dx() >= 1024 || b.Dy() >= 1024 {
		t.Fatalf("expected transparent desktop gutter to be trimmed, got %dx%d", b.Dx(), b.Dy())
	}
	if b.Dx() != b.Dy() {
		t.Fatalf("expected square tile after trimming, got %dx%d", b.Dx(), b.Dy())
	}
}

func TestColabOneAppearanceStoreRoundTripAndRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "appearance.json")
	store := newColabOneAppearanceStore(func() (string, error) { return path, nil })

	if got := store.Load(); got != "system" {
		t.Fatalf("initial appearance = %q, want system", got)
	}
	if err := store.Save("dark"); err != nil {
		t.Fatalf("Save(dark) error: %v", err)
	}
	if got := store.Load(); got != "dark" {
		t.Fatalf("reloaded appearance = %q, want dark", got)
	}
	if err := store.Save("light"); err != nil {
		t.Fatalf("Save(light) error: %v", err)
	}
	if got := store.Load(); got != "light" {
		t.Fatalf("reloaded appearance = %q, want light", got)
	}
	if err := store.Save("invalid-theme"); err == nil {
		t.Fatal("expected Save to reject unsupported appearance")
	}

	// Corrupt JSON falls back cleanly to system.
	if err := os.WriteFile(path, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := store.Load(); got != "system" {
		t.Fatalf("corrupt appearance = %q, want system", got)
	}
}

func TestColabOneSplashCopyCoversAllSupportedLocales(t *testing.T) {
	locales := []string{
		colabOneLocaleZhCN,
		colabOneLocaleEnUS,
		colabOneLocaleZhTW,
		colabOneLocaleZhHK,
		colabOneLocaleJaJP,
		colabOneLocaleKoKR,
	}
	for _, loc := range locales {
		cp := colabOneSplashCopy(loc)
		if cp.Brand != "ColabOne" {
			t.Fatalf("locale %s: Brand = %q, want ColabOne", loc, cp.Brand)
		}
		if cp.Tagline == "" || cp.Status == "" || cp.Pillars == "" || cp.Version == "" || cp.A11yBusy == "" {
			t.Fatalf("locale %s: incomplete splash copy %+v", loc, cp)
		}
	}
}

type fakeSplashTimerScheduler struct {
	mu      sync.Mutex
	entries []struct {
		delay time.Duration
		fn    func()
		fired bool
	}
}

func (s *fakeSplashTimerScheduler) after(d time.Duration, fn func()) *time.Timer {
	s.mu.Lock()
	idx := len(s.entries)
	s.entries = append(s.entries, struct {
		delay time.Duration
		fn    func()
		fired bool
	}{delay: d, fn: fn})
	s.mu.Unlock()
	// Return a stopped real timer so Stop() is always safe.
	tm := time.NewTimer(time.Hour)
	tm.Stop()
	_ = idx
	return tm
}

func (s *fakeSplashTimerScheduler) fireDelay(d time.Duration) int {
	s.mu.Lock()
	var toRun []func()
	for i := range s.entries {
		if !s.entries[i].fired && s.entries[i].delay == d {
			s.entries[i].fired = true
			toRun = append(toRun, s.entries[i].fn)
		}
	}
	s.mu.Unlock()
	for _, fn := range toRun {
		fn()
	}
	return len(toRun)
}

func TestColabOneStartupCoordinatorWaitsForReadyAndMinDisplay(t *testing.T) {
	sched := &fakeSplashTimerScheduler{}
	var reveals atomic.Int32
	coord := newColabOneStartupCoordinator(colabOneStartupConfig{
		MinDisplay:   1200 * time.Millisecond,
		PaintGrace:   180 * time.Millisecond,
		PageFailSafe: 4500 * time.Millisecond,
		HardFailSafe: 7500 * time.Millisecond,
		AfterFunc:    sched.after,
		OnReveal:     func() { reveals.Add(1) },
	})

	coord.Start()
	if !coord.IsActive() {
		t.Fatal("expected coordinator active after Start")
	}

	// Fast page + fast snapshot settle must NOT flash off before MinDisplay.
	coord.MarkPageReady()
	coord.MarkSnapshotSettled()
	if sched.fireDelay(180*time.Millisecond) != 1 {
		t.Fatal("expected paint grace timer")
	}
	if reveals.Load() != 0 {
		t.Fatalf("revealed prematurely before MinDisplay: %d", reveals.Load())
	}

	// Once MinDisplay elapses, main window reveals exactly once.
	if sched.fireDelay(1200*time.Millisecond) != 1 {
		t.Fatal("expected min display timer")
	}
	if reveals.Load() != 1 {
		t.Fatalf("expected 1 reveal after MinDisplay, got %d", reveals.Load())
	}
	if coord.IsActive() {
		t.Fatal("expected coordinator inactive after reveal")
	}

	// Later failsafe timers are idempotent no-ops.
	sched.fireDelay(4500 * time.Millisecond)
	sched.fireDelay(7500 * time.Millisecond)
	if reveals.Load() != 1 {
		t.Fatalf("expected idempotent reveal count 1, got %d", reveals.Load())
	}
}

func TestColabOneStartupCoordinatorWaitsForSlowSnapshot(t *testing.T) {
	sched := &fakeSplashTimerScheduler{}
	var reveals atomic.Int32
	coord := newColabOneStartupCoordinator(colabOneStartupConfig{
		MinDisplay:   1200 * time.Millisecond,
		PaintGrace:   180 * time.Millisecond,
		PageFailSafe: 4500 * time.Millisecond,
		HardFailSafe: 7500 * time.Millisecond,
		AfterFunc:    sched.after,
		OnReveal:     func() { reveals.Add(1) },
	})

	coord.Start()
	coord.MarkPageReady()
	sched.fireDelay(1200 * time.Millisecond)
	if reveals.Load() != 0 {
		t.Fatalf("should still show splash while initial snapshot is pending, got %d reveals", reveals.Load())
	}

	coord.MarkSnapshotSettled()
	if reveals.Load() != 0 {
		t.Fatalf("should wait for paint grace before reveal, got %d reveals", reveals.Load())
	}
	sched.fireDelay(180 * time.Millisecond)
	if reveals.Load() != 1 {
		t.Fatalf("expected reveal after slow snapshot + paint grace, got %d", reveals.Load())
	}
}

func TestColabOneStartupCoordinatorFailSafeGuaranteesReveal(t *testing.T) {
	sched := &fakeSplashTimerScheduler{}
	var reveals atomic.Int32
	coord := newColabOneStartupCoordinator(colabOneStartupConfig{
		MinDisplay:   1200 * time.Millisecond,
		PaintGrace:   180 * time.Millisecond,
		PageFailSafe: 4500 * time.Millisecond,
		HardFailSafe: 7500 * time.Millisecond,
		AfterFunc:    sched.after,
		OnReveal:     func() { reveals.Add(1) },
	})

	coord.Start()
	sched.fireDelay(7500 * time.Millisecond)
	if reveals.Load() != 1 {
		t.Fatalf("expected hard failsafe to guarantee reveal, got %d", reveals.Load())
	}
}

func TestColabOneStartupCoordinatorCancelPreventsReveal(t *testing.T) {
	sched := &fakeSplashTimerScheduler{}
	var reveals atomic.Int32
	coord := newColabOneStartupCoordinator(colabOneStartupConfig{
		MinDisplay:   1200 * time.Millisecond,
		PaintGrace:   180 * time.Millisecond,
		PageFailSafe: 4500 * time.Millisecond,
		HardFailSafe: 7500 * time.Millisecond,
		AfterFunc:    sched.after,
		OnReveal:     func() { reveals.Add(1) },
	})

	coord.Start()
	coord.Cancel()
	if coord.IsActive() {
		t.Fatal("expected coordinator inactive after Cancel")
	}
	coord.MarkPageReady()
	coord.MarkSnapshotSettled()
	coord.ForceReveal()
	sched.fireDelay(1200 * time.Millisecond)
	sched.fireDelay(7500 * time.Millisecond)
	if reveals.Load() != 0 {
		t.Fatalf("expected 0 reveals after Cancel, got %d", reveals.Load())
	}
}

func TestColabOneSplashRenderLightAndDarkSnapshots(t *testing.T) {
	renderTheme := func(dark bool, locale string, filename string) (centerLum float64, cornerAlpha uint8) {
		flag := dark
		state := &colabOneSplashViewState{
			forceDark:   &flag,
			forceLocale: locale,
			staticFrame: true,
		}
		img := ui.Render(state.render, colabOneSplashWindowWidth, colabOneSplashWindowHeight, 2)
		if img == nil {
			t.Fatal("ui.Render returned nil")
		}
		b := img.Bounds()
		if b.Dx() != colabOneSplashWindowWidth*2 || b.Dy() != colabOneSplashWindowHeight*2 {
			t.Fatalf("unexpected rendered dimensions %dx%d", b.Dx(), b.Dy())
		}

		// Optional developer preview export when COLABONE_SPLASH_PREVIEW_DIR is set.
		if dir := strings.TrimSpace(os.Getenv("COLABONE_SPLASH_PREVIEW_DIR")); dir != "" {
			if f, err := os.Create(filepath.Join(dir, filename)); err == nil {
				_ = png.Encode(f, img)
				_ = f.Close()
			}
		}

		corner := img.RGBAAt(2, 2)
		// Sample background area inside the card near bottom-left of body (away from text/logo).
		sample := img.RGBAAt(b.Dx()/4, b.Dy()*3/4)
		if sample.A < 250 {
			t.Fatalf("dark=%v: expected opaque card interior at sample point, got alpha=%d", dark, sample.A)
		}
		lum := 0.2126*float64(sample.R) + 0.7152*float64(sample.G) + 0.0722*float64(sample.B)
		return lum, corner.A
	}

	lightLum, lightCornerA := renderTheme(false, colabOneLocaleEnUS, "splash-light.png")
	darkLum, darkCornerA := renderTheme(true, colabOneLocaleEnUS, "splash-dark.png")
	_, _ = renderTheme(false, colabOneLocaleZhCN, "splash-zh-light.png")
	_, _ = renderTheme(true, colabOneLocaleZhCN, "splash-zh-dark.png")

	if lightCornerA != 0 || darkCornerA != 0 {
		t.Fatalf("expected transparent outer corners, got light=%d dark=%d", lightCornerA, darkCornerA)
	}
	if lightLum <= darkLum+120 {
		t.Fatalf("expected clear luminance separation between light (%.1f) and dark (%.1f) splash cards", lightLum, darkLum)
	}
}
