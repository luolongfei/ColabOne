package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/netproxy"
	"github.com/luolongfei/ColabOne/console/internal/updater"
)

//go:embed mygo.json
var colabOneEmbeddedMyGoManifest []byte

// ColabOneUpdateEvent pushes real-time self-update state and download progress
// changes from Go to open desktop pages.
var ColabOneUpdateEvent = mygo.NewEvent[updater.ColabOneUpdateState]("colabone-update")

// ColabOneUpdateOpenEvent requests the frontend to open the update modal (e.g.
// when triggered from the native system tray menu).
var ColabOneUpdateOpenEvent = mygo.NewEvent[bool]("colabone-update-open")

type mygoInstallableAdapter struct {
	up *mygo.Update
}

func (a *mygoInstallableAdapter) Version() string { return a.up.Version }
func (a *mygoInstallableAdapter) Notes() string   { return a.up.Notes }
func (a *mygoInstallableAdapter) Date() time.Time { return a.up.Date }
func (a *mygoInstallableAdapter) Install(ctx context.Context, progress func(downloaded, total int64)) error {
	return a.up.Install(ctx, progress)
}

type mygoUpdateBackend struct {
	repo        string
	readOnly    *updater.ColabOneReadOnlyFeedChecker
	rollback    *updater.ColabOneRollbackInstaller
	fallbackVer string
}

type colabOneMyGoManifestMeta struct {
	Version string `json:"version"`
	Updates struct {
		PublicKey string `json:"publicKey"`
		GitHub    string `json:"github"`
	} `json:"updates"`
}

func readEmbeddedMyGoManifestMeta() colabOneMyGoManifestMeta {
	var meta colabOneMyGoManifestMeta
	_ = json.Unmarshal(colabOneEmbeddedMyGoManifest, &meta)
	meta.Version = updater.NormalizeVersion(meta.Version)
	meta.Updates.PublicKey = strings.TrimSpace(meta.Updates.PublicKey)
	meta.Updates.GitHub = strings.Trim(strings.TrimSpace(meta.Updates.GitHub), "/")
	if meta.Version == "" {
		meta.Version = readFallbackManifestVersion()
	}
	return meta
}

func newMyGoUpdateBackend() *mygoUpdateBackend {
	meta := readEmbeddedMyGoManifestMeta()
	repo := meta.Updates.GitHub
	if repo == "" {
		repo = updater.COLABONE_GITHUB_REPO
	}
	tr := netproxy.Transport().Clone()
	tr.ResponseHeaderTimeout = 20 * time.Second
	metaClient := &http.Client{
		Transport:     tr,
		Timeout:       30 * time.Second,
		CheckRedirect: updater.ColabOneUpdateCheckRedirect,
	}
	downloadTr := netproxy.Transport().Clone()
	downloadTr.ResponseHeaderTimeout = 30 * time.Second
	downloadClient := &http.Client{
		Transport:     downloadTr,
		CheckRedirect: updater.ColabOneUpdateCheckRedirect,
	}
	b := &mygoUpdateBackend{
		repo:        repo,
		fallbackVer: meta.Version,
	}
	b.readOnly = &updater.ColabOneReadOnlyFeedChecker{
		Repo:           repo,
		Target:         updater.TargetPlatform(),
		Client:         metaClient,
		CurrentVersion: b.CurrentVersion,
	}
	b.rollback = &updater.ColabOneRollbackInstaller{
		Repo:      repo,
		Target:    updater.TargetPlatform(),
		PublicKey: meta.Updates.PublicKey,
		Client:    downloadClient,
		OnMacOSQuit: func() {
			mygo.App.Quit()
		},
	}
	return b
}

func readFallbackManifestVersion() string {
	candidates := []string{
		filepath.Join("mygo.json"),
		filepath.Join("console", "mygo.json"),
	}
	for _, path := range candidates {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var doc struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(raw, &doc) == nil {
			if norm := updater.NormalizeVersion(doc.Version); norm != "" {
				return norm
			}
		}
	}
	return ""
}

func (b *mygoUpdateBackend) CurrentVersion() string {
	if v := updater.NormalizeVersion(mygo.App.Version()); v != "" {
		return v
	}
	return b.fallbackVer
}

func (b *mygoUpdateBackend) IsDevBuild() bool {
	return mygo.IsDev() || !mygo.App.IsPackaged()
}

func (b *mygoUpdateBackend) CanSelfUpdate() bool {
	return mygo.Updater.Enabled()
}

func (b *mygoUpdateBackend) SetPrereleases(on bool) {
	mygo.Updater.SetPrereleases(on)
}

func (b *mygoUpdateBackend) CheckSelfUpdate(ctx context.Context) (updater.ColabOneUpdateInstallable, error) {
	up, err := mygo.Updater.Check(ctx)
	if err != nil || up == nil {
		return nil, err
	}
	return &mygoInstallableAdapter{up: up}, nil
}

func (b *mygoUpdateBackend) CheckReadOnly(ctx context.Context, includePrerelease bool) (*updater.ColabOneReadOnlyRelease, error) {
	return b.readOnly.Check(ctx, includePrerelease)
}

func (b *mygoUpdateBackend) CheckReleaseMetadata(ctx context.Context, includePrerelease bool) (*updater.ColabOneReadOnlyRelease, error) {
	return b.readOnly.CheckMetadata(ctx, includePrerelease)
}

func (b *mygoUpdateBackend) ListRollbackVersions(
	ctx context.Context,
	includePrerelease bool,
	currentVersion string,
	canSelfUpdate bool,
	maxCount int,
) ([]updater.ColabOneRollbackVersion, error) {
	return b.readOnly.ListRollbackReleases(ctx, includePrerelease, currentVersion, canSelfUpdate, maxCount)
}

func (b *mygoUpdateBackend) PrepareRollback(ctx context.Context, targetVersion string) (updater.ColabOneUpdateInstallable, error) {
	return b.rollback.PrepareRollback(ctx, targetVersion)
}

func (b *mygoUpdateBackend) RelaunchRollback() (bool, error) {
	return b.rollback.RelaunchRollback()
}

func (b *mygoUpdateBackend) Relaunch() error {
	mygo.App.Relaunch()
	return nil
}

func (d *ColabOneDesktop) initUpdater(cfgDir string, backend updater.ColabOneUpdateBackend) {
	if backend == nil {
		backend = newMyGoUpdateBackend()
	}
	var traySigMu sync.Mutex
	var lastTraySig string
	d.updater = updater.NewColabOneUpdater(updater.ColabOneUpdaterConfig{
		Backend:   backend,
		ConfigDir: cfgDir,
		Repo:      updater.COLABONE_GITHUB_REPO,
		OnStateChange: func(st updater.ColabOneUpdateState) {
			if d.appQuitting.Load() {
				return
			}
			if mygo.App.IsReady() {
				ColabOneUpdateEvent.Broadcast(st)
			}
			sig := fmt.Sprintf("%s|%s|%t", st.Status, st.AvailableVersion, st.IsRollback)
			traySigMu.Lock()
			changed := sig != lastTraySig
			if changed {
				lastTraySig = sig
			}
			traySigMu.Unlock()
			if changed {
				d.refreshTrayMenu()
			}
		},
		OnLog: func(message, source, level string) {
			d.bridge.logActivity(message, source, level)
		},
	})
}

func (d *ColabOneDesktop) startUpdaterBackground() {
	if d.updater != nil {
		d.updater.StartBackground()
	}
}

func (d *ColabOneDesktop) closeUpdater() {
	if d.updater != nil {
		d.updater.Close()
	}
}

func (d *ColabOneDesktop) trayUpdateLabel() string {
	if d.updater == nil {
		return colabOneT("tray.checkUpdates")
	}
	st := d.updater.State()
	switch st.Status {
	case updater.StatusReadyToRestart:
		if st.AvailableVersion != "" {
			if st.IsRollback {
				return fmt.Sprintf(colabOneT("tray.restartToRollback"), st.AvailableVersion)
			}
			return fmt.Sprintf(colabOneT("tray.restartToUpdate"), st.AvailableVersion)
		}
	case updater.StatusAvailable:
		if st.AvailableVersion != "" {
			return fmt.Sprintf(colabOneT("tray.updateAvailable"), st.AvailableVersion)
		}
	}
	return colabOneT("tray.checkUpdates")
}

func (d *ColabOneDesktop) handleTrayUpdateClick() {
	if d.showMain != nil {
		d.showMain()
	}
	if d.updater == nil {
		return
	}
	st := d.updater.State()
	if st.Status == updater.StatusReadyToRestart || st.Status == updater.StatusAvailable ||
		st.Status == updater.StatusDownloading || st.Status == updater.StatusInstalling {
		if mygo.App.IsReady() {
			ColabOneUpdateOpenEvent.Broadcast(true)
		}
		return
	}
	if mygo.App.IsReady() {
		ColabOneUpdateOpenEvent.Broadcast(true)
	}
	d.updater.TriggerAsyncCheck(true)
}

func (d *ColabOneDesktop) refreshTrayMenu() {
	if d.appQuitting.Load() || !d.isAppReady() {
		return
	}
	d.trayMu.Lock()
	hasTray := d.tray != nil
	d.trayMu.Unlock()
	if !hasTray || d.appQuitting.Load() {
		return
	}
	d.scheduleDetachedMainTask(&d.trayRefreshMu, &d.trayRefreshRunning, &d.trayRefreshPending, func() {
		d.trayMu.Lock()
		curTray := d.tray
		d.trayMu.Unlock()
		if curTray != nil {
			curTray.SetMenu(d.buildTrayMenu())
		}
	})
}

// GetUpdateState returns the current desktop self-update status, progress, and preferences.
func (d *ColabOneDesktop) GetUpdateState() updater.ColabOneUpdateState {
	if d.updater == nil {
		return updater.ColabOneUpdateState{
			Status:           updater.StatusDisabled,
			DisabledReason:   updater.DisabledReasonDevBuild,
			ReleaseURL:       updater.COLABONE_RELEASES_URL,
			AutoCheck:        true,
			RollbackVersions: []updater.ColabOneRollbackVersion{},
		}
	}
	return d.updater.State()
}

// ListRollbackVersions queries recent older GitHub Releases available for version rollback.
func (d *ColabOneDesktop) ListRollbackVersions(ctx context.Context, forceRefresh bool) ([]updater.ColabOneRollbackVersion, error) {
	if d.updater == nil {
		return []updater.ColabOneRollbackVersion{}, nil
	}
	versions, err := d.updater.ListRollbackVersions(ctx, forceRefresh)
	if err != nil {
		_, detail := updater.ClassifyUpdateError(err)
		if strings.TrimSpace(detail) != "" {
			return []updater.ColabOneRollbackVersion{}, errors.New(detail)
		}
		return []updater.ColabOneRollbackVersion{}, err
	}
	return versions, nil
}

// StartUpdateRollback downloads, verifies (Ed25519), and installs an older release for rollback.
func (d *ColabOneDesktop) StartUpdateRollback(version string) (updater.ColabOneUpdateState, error) {
	if d.updater == nil {
		return d.GetUpdateState(), errors.New("更新服务未初始化")
	}
	return d.updater.StartRollback(version)
}

// CheckForUpdate queries GitHub Releases for a newer version of ColabOne.
func (d *ColabOneDesktop) CheckForUpdate(ctx context.Context, manual bool) (updater.ColabOneUpdateState, error) {
	if d.updater == nil {
		return d.GetUpdateState(), nil
	}
	st, err := d.updater.Check(ctx, manual)
	if err != nil {
		if strings.TrimSpace(st.ErrorMessage) != "" {
			return st, errors.New(st.ErrorMessage)
		}
		return st, err
	}
	return st, nil
}

// StartUpdateInstall downloads, verifies (Ed25519), and installs the discovered update.
func (d *ColabOneDesktop) StartUpdateInstall() (updater.ColabOneUpdateState, error) {
	if d.updater == nil {
		return d.GetUpdateState(), errors.New("更新服务未初始化")
	}
	return d.updater.StartInstall()
}

// CancelUpdateInstall cancels an in-progress update download.
func (d *ColabOneDesktop) CancelUpdateInstall() updater.ColabOneUpdateState {
	if d.updater == nil {
		return d.GetUpdateState()
	}
	return d.updater.CancelInstall()
}

// SkipUpdateVersion suppresses automatic background notifications for version.
func (d *ColabOneDesktop) SkipUpdateVersion(version string) (updater.ColabOneUpdateState, error) {
	if d.updater == nil {
		return d.GetUpdateState(), errors.New("更新服务未初始化")
	}
	return d.updater.SkipVersion(version)
}

// SetUpdatePreferences updates and persists automatic check and prerelease channel preferences.
func (d *ColabOneDesktop) SetUpdatePreferences(autoCheck bool, includePrerelease bool) (updater.ColabOneUpdateState, error) {
	if d.updater == nil {
		return d.GetUpdateState(), errors.New("更新服务未初始化")
	}
	st, channelChanged, err := d.updater.SetPreferences(autoCheck, includePrerelease)
	if err != nil {
		return st, err
	}
	if channelChanged && st.Status != updater.StatusDisabled &&
		st.Status != updater.StatusDownloading &&
		st.Status != updater.StatusInstalling &&
		st.Status != updater.StatusReadyToRestart {
		d.updater.TriggerAsyncCheck(false)
	}
	return st, nil
}

// RelaunchForUpdate restarts ColabOne to activate an installed update.
func (d *ColabOneDesktop) RelaunchForUpdate() error {
	if d.updater == nil {
		return errors.New("更新服务未初始化")
	}
	return d.updater.Relaunch()
}
