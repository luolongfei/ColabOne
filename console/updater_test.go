package main

import (
	"context"
	"testing"
	"time"

	"github.com/luolongfei/ColabOne/console/internal/updater"
)

type stubDesktopInstallable struct {
	version string
	notes   string
	date    time.Time
}

func (s *stubDesktopInstallable) Version() string { return s.version }
func (s *stubDesktopInstallable) Notes() string   { return s.notes }
func (s *stubDesktopInstallable) Date() time.Time { return s.date }
func (s *stubDesktopInstallable) Install(_ context.Context, progress func(downloaded, total int64)) error {
	if progress != nil {
		progress(100, 100)
	}
	return nil
}

type stubDesktopUpdateBackend struct {
	current     string
	canSelf     bool
	prereleases bool
	relaunched  bool
}

func (b *stubDesktopUpdateBackend) CurrentVersion() string { return b.current }
func (b *stubDesktopUpdateBackend) IsDevBuild() bool       { return false }
func (b *stubDesktopUpdateBackend) CanSelfUpdate() bool    { return b.canSelf }
func (b *stubDesktopUpdateBackend) SetPrereleases(on bool) { b.prereleases = on }
func (b *stubDesktopUpdateBackend) CheckSelfUpdate(context.Context) (updater.ColabOneUpdateInstallable, error) {
	return &stubDesktopInstallable{
		version: "0.1.7",
		notes:   "ColabOne v0.1.7\n\n## 新增功能\n- 自动更新\n\n---\n\n## New Features\n- Auto-update\n",
		date:    time.Date(2026, 11, 23, 18, 0, 0, 0, time.UTC),
	}, nil
}
func (b *stubDesktopUpdateBackend) CheckReadOnly(context.Context, bool) (*updater.ColabOneReadOnlyRelease, error) {
	return nil, nil
}
func (b *stubDesktopUpdateBackend) Relaunch() error {
	b.relaunched = true
	return nil
}

func TestColabOneDesktopUpdaterMethodsAndTrayLabels(t *testing.T) {
	prevLocale := colabOneGetLocale()
	t.Cleanup(func() { colabOneSetLocaleState(prevLocale) })
	colabOneSetLocaleState(colabOneLocaleZhCN)

	backend := &stubDesktopUpdateBackend{current: "0.1.6", canSelf: true}
	d := &ColabOneDesktop{bridge: &colabOneBridge{}}
	d.initUpdater(t.TempDir(), backend)
	defer d.closeUpdater()

	if label := d.trayUpdateLabel(); label != "检查更新…" {
		t.Fatalf("initial tray update label = %q, want %q", label, "检查更新…")
	}

	st, err := d.CheckForUpdate(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckForUpdate failed: %v", err)
	}
	if st.Status != updater.StatusAvailable || st.AvailableVersion != "0.1.7" {
		t.Fatalf("unexpected update state: %+v", st)
	}
	if label := d.trayUpdateLabel(); label != "发现新版本 v0.1.7…" {
		t.Fatalf("available tray update label = %q, want %q", label, "发现新版本 v0.1.7…")
	}

	colabOneSetLocaleState(colabOneLocaleEnUS)
	if label := d.trayUpdateLabel(); label != "Update Available (v0.1.7)…" {
		t.Fatalf("english tray update label = %q", label)
	}

	if _, err := d.StartUpdateInstall(); err != nil {
		t.Fatalf("StartUpdateInstall failed: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if d.GetUpdateState().Status == updater.StatusReadyToRestart {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := d.GetUpdateState().Status; got != updater.StatusReadyToRestart {
		t.Fatalf("status = %q, want %q", got, updater.StatusReadyToRestart)
	}
	if label := d.trayUpdateLabel(); label != "Restart to Update to v0.1.7" {
		t.Fatalf("restart tray label = %q", label)
	}
	if err := d.RelaunchForUpdate(); err != nil {
		t.Fatalf("RelaunchForUpdate failed: %v", err)
	}
	if !backend.relaunched {
		t.Fatal("expected backend Relaunch to be invoked")
	}
}
