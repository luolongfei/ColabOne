package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/luolongfei/ColabOne/console/internal/activity"
	"github.com/luolongfei/ColabOne/console/internal/fsutil"
	"github.com/luolongfei/ColabOne/console/internal/migration"
)

const colabOneMigrationSessionTTL = 10 * time.Minute

type colabOnePendingMigrationFile struct {
	token         string
	filename      string
	raw           []byte
	schemaVersion int
	createdAt     time.Time
}

type colabOneMigrationPreviewSession struct {
	id        string
	filename  string
	payload   *migration.ColabOneMigrationPayload
	createdAt time.Time
	applying  bool
}

// ColabOneMigrationExportSummary describes live redacted module summaries shown
// in the Export Migration modal before the user chooses a password and saves.
type ColabOneMigrationExportSummary struct {
	AppVersion          string                                     `json:"app_version"`
	SourceOS            string                                     `json:"source_os"`
	HasActiveOperation  bool                                       `json:"has_active_operation"`
	HasRunningInstances bool                                       `json:"has_running_instances"`
	Modules             []migration.ColabOneMigrationModuleSummary `json:"modules"`
}

// ColabOneMigrationExportRequest carries user selections from the Export modal.
// Raw secrets are read strictly inside Go and never cross the WebView boundary.
type ColabOneMigrationExportRequest struct {
	Modules  []string                            `json:"modules"`
	Password string                              `json:"password"`
	UIPrefs  *migration.ColabOneMigrationUIPrefs `json:"ui_prefs,omitempty"`
}

// ColabOneMigrationExportResult reports the outcome of ExportMigrationFile.
type ColabOneMigrationExportResult struct {
	Cancelled bool     `json:"cancelled"`
	Filename  string   `json:"filename,omitempty"`
	SizeBytes int64    `json:"size_bytes,omitempty"`
	Modules   []string `json:"modules,omitempty"`
}

// ColabOneMigrationPickResult reports outer-envelope metadata for a chosen .colabone file.
type ColabOneMigrationPickResult struct {
	Cancelled     bool   `json:"cancelled"`
	FileToken     string `json:"file_token,omitempty"`
	Filename      string `json:"filename,omitempty"`
	FileSize      int64  `json:"file_size,omitempty"`
	SchemaVersion int    `json:"schema_version,omitempty"`
}

// ColabOneMigrationPreview reports strictly redacted archive metadata after
// password decryption so the user can review and select modules before applying.
type ColabOneMigrationPreview struct {
	PreviewID           string                                     `json:"preview_id"`
	Filename            string                                     `json:"filename"`
	AppVersion          string                                     `json:"app_version"`
	ExportedAt          string                                     `json:"exported_at"`
	SourceOS            string                                     `json:"source_os"`
	SchemaVersion       int                                        `json:"schema_version"`
	HasActiveOperation  bool                                       `json:"has_active_operation"`
	HasRunningInstances bool                                       `json:"has_running_instances"`
	CanRunInBackground  bool                                       `json:"can_run_in_background"`
	Modules             []migration.ColabOneMigrationModuleSummary `json:"modules"`
	Warnings            []string                                   `json:"warnings,omitempty"`
}

// ColabOneMigrationApplyRequest selects which decrypted modules to apply on this machine.
type ColabOneMigrationApplyRequest struct {
	PreviewID string   `json:"preview_id"`
	Modules   []string `json:"modules"`
}

// ColabOneMigrationApplyResult reports applied modules and UI preferences to hot-sync in WebView.
type ColabOneMigrationApplyResult struct {
	AppliedModules  []string                            `json:"applied_modules"`
	AccountImported bool                                `json:"account_imported"`
	AccountLoggedIn bool                                `json:"account_logged_in"`
	AccountEmail    string                              `json:"account_email,omitempty"`
	AppliedUIPrefs  *migration.ColabOneMigrationUIPrefs `json:"applied_ui_prefs,omitempty"`
	CloseBehavior   string                              `json:"close_behavior,omitempty"`
	Warnings        []string                            `json:"warnings,omitempty"`
}

func randomColabOneSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (d *ColabOneDesktop) clearMigrationSessions() {
	d.migrationMu.Lock()
	if d.migrationPendingFile != nil {
		clear(d.migrationPendingFile.raw)
		d.migrationPendingFile = nil
	}
	d.migrationPreview = nil
	d.migrationMu.Unlock()
}

func (d *ColabOneDesktop) currentDesktopMigrationSettings(uiPrefs *migration.ColabOneMigrationUIPrefs) *migration.ColabOneMigrationSettingsData {
	appearance := "system"
	if d.appearanceStore != nil {
		appearance = d.appearanceStore.Load()
	}
	closeBehavior := d.currentCloseBehavior()
	var updaterPrefs *migration.ColabOneMigrationUpdaterPrefs
	if d.updater != nil {
		p := d.updater.Preferences()
		updaterPrefs = &migration.ColabOneMigrationUpdaterPrefs{
			AutoCheck:         p.AutoCheck,
			IncludePrerelease: p.IncludePrerelease,
			SkippedVersion:    p.SkippedVersion,
		}
	}
	cleanUI := sanitizeColabOneUIPrefs(uiPrefs)
	if cleanUI != nil {
		if normApp, ok := normalizeColabOneAppearance(cleanUI.Appearance); ok {
			appearance = normApp
		}
	}
	return &migration.ColabOneMigrationSettingsData{
		Appearance:    appearance,
		CloseBehavior: closeBehavior,
		Updater:       updaterPrefs,
		UIPrefs:       cleanUI,
	}
}

func sanitizeColabOneUIPrefs(src *migration.ColabOneMigrationUIPrefs) *migration.ColabOneMigrationUIPrefs {
	if src == nil {
		return nil
	}
	out := &migration.ColabOneMigrationUIPrefs{}
	hasAny := false
	if normApp, ok := normalizeColabOneAppearance(src.Appearance); ok {
		out.Appearance = normApp
		hasAny = true
	}
	if normLoc, ok := colabOneNormalizeLocale(src.Locale); ok {
		out.Locale = normLoc
		hasAny = true
	}
	if src.PrivacyMaskDefault != nil {
		v := *src.PrivacyMaskDefault
		out.PrivacyMaskDefault = &v
		hasAny = true
	}
	switch strings.ToLower(strings.TrimSpace(src.MonitorLayout)) {
	case "grid", "rows", "list", "compact", "split", "cards", "sidebar":
		out.MonitorLayout = strings.ToLower(strings.TrimSpace(src.MonitorLayout))
		hasAny = true
	}
	switch strings.ToLower(strings.TrimSpace(src.CUUsageMetric)) {
	case "consumed", "balance", "rate":
		out.CUUsageMetric = strings.ToLower(strings.TrimSpace(src.CUUsageMetric))
		hasAny = true
	}
	if src.LogsWidth != nil && *src.LogsWidth >= 240 && *src.LogsWidth <= 1200 {
		v := *src.LogsWidth
		out.LogsWidth = &v
		hasAny = true
	}
	if !hasAny {
		return nil
	}
	return out
}

func (d *ColabOneDesktop) openMigrationSaveDialog(ctx context.Context, defaultName string) (string, error) {
	if d.migrationSaveDialogFn != nil {
		return d.migrationSaveDialogFn(ctx, defaultName)
	}
	defaultPath := defaultName
	if dir, err := mygo.App.Path(mygo.PathDownloads); err == nil && dir != "" {
		defaultPath = filepath.Join(dir, defaultName)
	} else if home, err := mygo.App.Path(mygo.PathHome); err == nil && home != "" {
		defaultPath = filepath.Join(home, defaultName)
	}
	return mygo.Dialog.Save(mygo.SaveDialogOptions{
		Parent:      mygo.CallerWindow(ctx),
		Title:       colabOneT("dialog.migrationSaveTitle"),
		DefaultPath: defaultPath,
		Filters: []mygo.FileFilter{
			{
				Name:       colabOneT("dialog.migrationFilterName"),
				Extensions: []string{migration.COLABONE_MIGRATION_FILE_EXT},
			},
		},
	})
}

func (d *ColabOneDesktop) openMigrationOpenDialog(ctx context.Context) (string, error) {
	if d.migrationOpenDialogFn != nil {
		return d.migrationOpenDialogFn(ctx)
	}
	defaultDir := ""
	if dir, err := mygo.App.Path(mygo.PathDownloads); err == nil && dir != "" {
		defaultDir = dir
	} else if home, err := mygo.App.Path(mygo.PathHome); err == nil && home != "" {
		defaultDir = home
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:      mygo.CallerWindow(ctx),
		Title:       colabOneT("dialog.migrationOpenTitle"),
		DefaultPath: defaultDir,
		Filters: []mygo.FileFilter{
			{
				Name:       colabOneT("dialog.migrationFilterName"),
				Extensions: []string{migration.COLABONE_MIGRATION_FILE_EXT},
			},
		},
	})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

func writeColabOneFileAtomic(targetPath, tmpPattern string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, tmpPattern)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	closed := false
	renamed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		if !renamed {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(perm); err != nil && runtime.GOOS != "windows" {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	if err := fsutil.RenameReplace(tmpName, targetPath); err != nil {
		return err
	}
	renamed = true
	return nil
}

func writeColabOneMigrationFileAtomic(targetPath string, data []byte) error {
	if fi, err := os.Lstat(targetPath); err == nil {
		if fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return errors.New("refusing to overwrite directory or symlink")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeColabOneFileAtomic(targetPath, ".colabone-migration-*.tmp", data, 0o600)
}

// GetMigrationExportSummary returns live redacted summaries for all 6 migration
// modules on this machine so the Export modal can preview what will be packaged.
func (d *ColabOneDesktop) GetMigrationExportSummary(
	ctx context.Context,
	uiPrefs *migration.ColabOneMigrationUIPrefs,
) (ColabOneMigrationExportSummary, error) {
	if d.bridge == nil {
		return ColabOneMigrationExportSummary{}, errors.New(colabOneT("err.bridgeNotInitialized"))
	}
	ctrl, err := d.bridge.ensureController(ctx)
	if err != nil {
		return ColabOneMigrationExportSummary{}, err
	}
	desktopSettings := d.currentDesktopMigrationSettings(uiPrefs)
	payload, err := ctrl.ExportMigrationPayload(migration.AllModules, colabOneAppVersion(), runtime.GOOS, desktopSettings)
	if err != nil {
		return ColabOneMigrationExportSummary{}, err
	}
	st := ctrl.MigrationStatus()
	return ColabOneMigrationExportSummary{
		AppVersion:          colabOneAppVersion(),
		SourceOS:            runtime.GOOS,
		HasActiveOperation:  st.HasActiveOperation,
		HasRunningInstances: st.HasRunningInstances,
		Modules:             migration.BuildModuleSummaries(payload),
	}, nil
}

// ExportMigrationFile validates selections and password, opens the native Save
// dialog, encrypts the selected modules in-process with Argon2id + AES-256-GCM,
// and writes the resulting .colabone archive atomically to disk.
func (d *ColabOneDesktop) ExportMigrationFile(
	ctx context.Context,
	req ColabOneMigrationExportRequest,
) (ColabOneMigrationExportResult, error) {
	if d.bridge == nil {
		return ColabOneMigrationExportResult{}, errors.New(colabOneT("err.bridgeNotInitialized"))
	}
	normModules, err := migration.NormalizeModules(req.Modules)
	if err != nil {
		return ColabOneMigrationExportResult{}, err
	}
	if err := migration.ValidatePassword(req.Password); err != nil {
		return ColabOneMigrationExportResult{}, err
	}
	ctrl, err := d.bridge.ensureController(ctx)
	if err != nil {
		return ColabOneMigrationExportResult{}, err
	}

	defaultName := fmt.Sprintf(
		"ColabOne-Backup-%s.%s",
		time.Now().Format("20060102-1504"),
		migration.COLABONE_MIGRATION_FILE_EXT,
	)
	savePath, err := d.openMigrationSaveDialog(ctx, defaultName)
	if err != nil {
		return ColabOneMigrationExportResult{}, err
	}
	savePath = strings.TrimSpace(savePath)
	if savePath == "" {
		return ColabOneMigrationExportResult{Cancelled: true}, nil
	}
	if !strings.HasSuffix(strings.ToLower(savePath), "."+migration.COLABONE_MIGRATION_FILE_EXT) {
		savePath += "." + migration.COLABONE_MIGRATION_FILE_EXT
	}

	desktopSettings := d.currentDesktopMigrationSettings(req.UIPrefs)
	payload, err := ctrl.ExportMigrationPayload(normModules, colabOneAppVersion(), runtime.GOOS, desktopSettings)
	if err != nil {
		return ColabOneMigrationExportResult{}, err
	}

	var sealed []byte
	if d.migrationKDFParams != nil {
		sealed, err = migration.SealPayloadWithParams(payload, req.Password, *d.migrationKDFParams)
	} else {
		sealed, err = migration.SealPayload(payload, req.Password)
	}
	if err != nil {
		return ColabOneMigrationExportResult{}, err
	}

	if err := writeColabOneMigrationFileAtomic(savePath, sealed); err != nil {
		return ColabOneMigrationExportResult{}, errors.New(colabOneT("err.migrationSaveFailed") + activity.SafeMessage(err.Error()))
	}

	ctrl.LogActivity(
		fmt.Sprintf("已导出加密迁移文件（%d 项模块）", len(normModules)),
		"迁移",
		"info",
	)
	return ColabOneMigrationExportResult{
		Cancelled: false,
		Filename:  filepath.Base(savePath),
		SizeBytes: int64(len(sealed)),
		Modules:   normModules,
	}, nil
}

// PickMigrationFile opens the native Open dialog, validates the outer .colabone
// archive header without exposing any file path or contents to the WebView, and
// caches the raw archive under a short-lived FileToken for decryption.
func (d *ColabOneDesktop) PickMigrationFile(ctx context.Context) (ColabOneMigrationPickResult, error) {
	if d.bridge == nil {
		return ColabOneMigrationPickResult{}, errors.New(colabOneT("err.bridgeNotInitialized"))
	}
	ctrl, err := d.bridge.ensureController(ctx)
	if err != nil {
		return ColabOneMigrationPickResult{}, err
	}
	if ctrl.MigrationStatus().HasActiveOperation {
		return ColabOneMigrationPickResult{}, errors.New("请等待当前操作完成后再导入迁移文件")
	}

	pickedPath, err := d.openMigrationOpenDialog(ctx)
	if err != nil {
		return ColabOneMigrationPickResult{}, err
	}
	pickedPath = strings.TrimSpace(pickedPath)
	if pickedPath == "" {
		return ColabOneMigrationPickResult{Cancelled: true}, nil
	}

	f, err := os.Open(pickedPath)
	if err != nil {
		return ColabOneMigrationPickResult{}, errors.New(colabOneT("err.migrationReadFailed") + activity.SafeMessage(err.Error()))
	}
	info, statErr := f.Stat()
	if statErr != nil {
		_ = f.Close()
		return ColabOneMigrationPickResult{}, errors.New(colabOneT("err.migrationReadFailed") + activity.SafeMessage(statErr.Error()))
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return ColabOneMigrationPickResult{}, migration.ErrInvalidEnvelope
	}
	if info.Size() > migration.COLABONE_MIGRATION_MAX_FILE_BYTES {
		_ = f.Close()
		return ColabOneMigrationPickResult{}, migration.ErrFileTooLarge
	}
	raw, readErr := io.ReadAll(io.LimitReader(f, migration.COLABONE_MIGRATION_MAX_FILE_BYTES+1))
	_ = f.Close()
	if readErr != nil {
		return ColabOneMigrationPickResult{}, errors.New(colabOneT("err.migrationReadFailed") + activity.SafeMessage(readErr.Error()))
	}
	if int64(len(raw)) > migration.COLABONE_MIGRATION_MAX_FILE_BYTES {
		return ColabOneMigrationPickResult{}, migration.ErrFileTooLarge
	}

	header, err := migration.InspectEnvelope(raw)
	if err != nil {
		return ColabOneMigrationPickResult{}, err
	}

	token, err := randomColabOneSessionID()
	if err != nil {
		return ColabOneMigrationPickResult{}, err
	}

	d.migrationMu.Lock()
	if d.migrationPreview != nil && d.migrationPreview.applying {
		d.migrationMu.Unlock()
		clear(raw)
		return ColabOneMigrationPickResult{}, errors.New("请等待当前操作完成后再导入迁移文件")
	}
	if d.migrationPendingFile != nil {
		clear(d.migrationPendingFile.raw)
	}
	d.migrationPendingFile = &colabOnePendingMigrationFile{
		token:         token,
		filename:      filepath.Base(pickedPath),
		raw:           raw,
		schemaVersion: header.SchemaVersion,
		createdAt:     time.Now(),
	}
	d.migrationPreview = nil
	d.migrationMu.Unlock()

	return ColabOneMigrationPickResult{
		Cancelled:     false,
		FileToken:     token,
		Filename:      filepath.Base(pickedPath),
		FileSize:      header.FileSize,
		SchemaVersion: header.SchemaVersion,
	}, nil
}

// DecryptMigrationPreview decrypts the pending .colabone archive identified by
// fileToken inside Go, caches the validated payload under a short-lived PreviewID,
// and returns strictly redacted summaries to the WebView.
func (d *ColabOneDesktop) DecryptMigrationPreview(
	ctx context.Context,
	fileToken string,
	password string,
) (ColabOneMigrationPreview, error) {
	if d.bridge == nil {
		return ColabOneMigrationPreview{}, errors.New(colabOneT("err.bridgeNotInitialized"))
	}
	ctrl, err := d.bridge.ensureController(ctx)
	if err != nil {
		return ColabOneMigrationPreview{}, err
	}

	trimmedToken := strings.TrimSpace(fileToken)
	d.migrationMu.Lock()
	pending := d.migrationPendingFile
	if pending == nil || pending.token != trimmedToken || time.Since(pending.createdAt) > colabOneMigrationSessionTTL {
		if pending != nil && time.Since(pending.createdAt) > colabOneMigrationSessionTTL {
			clear(pending.raw)
			d.migrationPendingFile = nil
		}
		d.migrationMu.Unlock()
		return ColabOneMigrationPreview{}, errors.New(colabOneT("err.migrationSessionExpired"))
	}
	rawCopy := append([]byte(nil), pending.raw...)
	filename := pending.filename
	d.migrationMu.Unlock()
	defer clear(rawCopy)

	payload, err := migration.OpenPayload(rawCopy, password)
	if err != nil {
		// On wrong password, keep pending file alive so user can immediately retry.
		return ColabOneMigrationPreview{}, err
	}

	previewID, err := randomColabOneSessionID()
	if err != nil {
		return ColabOneMigrationPreview{}, err
	}

	d.migrationMu.Lock()
	if d.migrationPreview != nil && d.migrationPreview.applying {
		d.migrationMu.Unlock()
		return ColabOneMigrationPreview{}, errors.New("请等待当前操作完成后再导入迁移文件")
	}
	if d.migrationPendingFile != nil && d.migrationPendingFile.token == trimmedToken {
		clear(d.migrationPendingFile.raw)
		d.migrationPendingFile = nil
	}
	d.migrationPreview = &colabOneMigrationPreviewSession{
		id:        previewID,
		filename:  filename,
		payload:   payload,
		createdAt: time.Now(),
	}
	d.migrationMu.Unlock()

	st := ctrl.MigrationStatus()
	canBg := d.canRunInBackground()
	warnings := make([]string, 0, 2)
	if st.HasRunningInstances && payload.HasModule(migration.ModuleAccount) {
		warnings = append(warnings, "active_instances_block_account")
	}
	if payload.HasModule(migration.ModuleSettings) && payload.Settings != nil &&
		strings.EqualFold(strings.TrimSpace(payload.Settings.CloseBehavior), colabOneCloseBehaviorBackground) &&
		!canBg {
		warnings = append(warnings, "close_behavior_fallback_quit")
	}

	return ColabOneMigrationPreview{
		PreviewID:           previewID,
		Filename:            filename,
		AppVersion:          payload.AppVersion,
		ExportedAt:          payload.ExportedAt,
		SourceOS:            payload.SourceOS,
		SchemaVersion:       payload.SchemaVersion,
		HasActiveOperation:  st.HasActiveOperation,
		HasRunningInstances: st.HasRunningInstances,
		CanRunInBackground:  canBg,
		Modules:             migration.BuildModuleSummaries(payload),
		Warnings:            warnings,
	}, nil
}

// ApplyMigrationPreview atomically applies user-selected modules from the
// decrypted preview session across controller stores, local encrypted credential
// vault (vault.enc), and native desktop settings, then immediately wipes the
// cached preview from memory.
func (d *ColabOneDesktop) ApplyMigrationPreview(
	ctx context.Context,
	req ColabOneMigrationApplyRequest,
) (ColabOneMigrationApplyResult, error) {
	if d.bridge == nil {
		return ColabOneMigrationApplyResult{}, errors.New(colabOneT("err.bridgeNotInitialized"))
	}
	ctrl, err := d.bridge.ensureController(ctx)
	if err != nil {
		return ColabOneMigrationApplyResult{}, err
	}

	trimmedID := strings.TrimSpace(req.PreviewID)
	d.migrationMu.Lock()
	session := d.migrationPreview
	if session == nil || session.id != trimmedID || time.Since(session.createdAt) > colabOneMigrationSessionTTL {
		if session != nil && !session.applying && time.Since(session.createdAt) > colabOneMigrationSessionTTL {
			d.migrationPreview = nil
		}
		d.migrationMu.Unlock()
		return ColabOneMigrationApplyResult{}, errors.New(colabOneT("err.migrationSessionExpired"))
	}
	if session.applying {
		d.migrationMu.Unlock()
		return ColabOneMigrationApplyResult{}, errors.New("请等待当前操作完成后再导入迁移文件")
	}
	session.applying = true
	payload := session.payload
	d.migrationMu.Unlock()

	ctrlRes, err := ctrl.ApplyMigrationPayload(ctx, payload, req.Modules)
	if err != nil {
		d.migrationMu.Lock()
		if d.migrationPreview == session {
			session.applying = false
		}
		d.migrationMu.Unlock()
		return ColabOneMigrationApplyResult{}, err
	}

	appliedSet := make(map[string]bool, len(ctrlRes.AppliedModules))
	for _, m := range ctrlRes.AppliedModules {
		appliedSet[m] = true
	}

	warnings := append([]string(nil), ctrlRes.Warnings...)
	var appliedUIPrefs *migration.ColabOneMigrationUIPrefs
	appliedCloseBehavior := ""

	if appliedSet[migration.ModuleSettings] && payload.Settings != nil {
		st := payload.Settings

		// 1. Native Appearance + Theme source
		targetAppearance := strings.TrimSpace(st.Appearance)
		if targetAppearance == "" && st.UIPrefs != nil {
			targetAppearance = strings.TrimSpace(st.UIPrefs.Appearance)
		}
		if normApp, ok := normalizeColabOneAppearance(targetAppearance); ok {
			if err := d.SetAppearance(normApp); err == nil {
				targetAppearance = normApp
			}
		}

		// 2. Native Locale (tray menu + window titles + Go i18n)
		targetLocale := strings.TrimSpace(ctrlRes.Locale)
		if targetLocale == "" && st.UIPrefs != nil {
			targetLocale = strings.TrimSpace(st.UIPrefs.Locale)
		}
		if normLoc, ok := colabOneNormalizeLocale(targetLocale); ok {
			_ = d.SetLocale(normLoc)
			targetLocale = normLoc
		}

		// 3. Main-window Close Behavior (graceful fallback when target lacks system tray)
		if normClose, ok := normalizeColabOneCloseBehavior(st.CloseBehavior); ok {
			if normClose == colabOneCloseBehaviorBackground && !d.canRunInBackground() {
				normClose = colabOneCloseBehaviorQuit
				warnings = append(warnings, "close_behavior_fallback_quit")
			}
			if state, err := d.SetCloseBehavior(normClose); err == nil {
				appliedCloseBehavior = state.Behavior
			}
		}

		// 4. Updater Preferences
		if st.Updater != nil && d.updater != nil {
			upState, channelChanged, err := d.updater.ApplyImportedPreferences(
				st.Updater.AutoCheck,
				st.Updater.IncludePrerelease,
				st.Updater.SkippedVersion,
			)
			if err == nil && channelChanged &&
				upState.Status != "disabled" &&
				upState.Status != "downloading" &&
				upState.Status != "installing" &&
				upState.Status != "ready_to_restart" {
				d.updater.TriggerAsyncCheck(false)
			}
		}

		// 5. Consolidated WebView UI preferences for immediate hot-apply
		cleanUI := sanitizeColabOneUIPrefs(st.UIPrefs)
		if cleanUI == nil {
			cleanUI = &migration.ColabOneMigrationUIPrefs{}
		}
		if normApp, ok := normalizeColabOneAppearance(targetAppearance); ok {
			cleanUI.Appearance = normApp
		}
		if normLoc, ok := colabOneNormalizeLocale(targetLocale); ok {
			cleanUI.Locale = normLoc
		}
		appliedUIPrefs = cleanUI
	}

	// Consume and wipe the decrypted preview session after successful apply.
	d.migrationMu.Lock()
	if d.migrationPreview == session {
		d.migrationPreview = nil
	}
	d.migrationMu.Unlock()

	return ColabOneMigrationApplyResult{
		AppliedModules:  ctrlRes.AppliedModules,
		AccountImported: ctrlRes.AccountImported,
		AccountLoggedIn: ctrlRes.AccountLoggedIn,
		AccountEmail:    ctrlRes.AccountEmail,
		AppliedUIPrefs:  appliedUIPrefs,
		CloseBehavior:   appliedCloseBehavior,
		Warnings:        warnings,
	}, nil
}

// CancelMigrationSession discards any pending picked file or decrypted preview
// held in desktop memory when the user closes or cancels the Migration modal.
func (d *ColabOneDesktop) CancelMigrationSession(_ context.Context, sessionID string) error {
	trimmed := strings.TrimSpace(sessionID)
	d.migrationMu.Lock()
	if trimmed == "" {
		if d.migrationPendingFile != nil {
			clear(d.migrationPendingFile.raw)
			d.migrationPendingFile = nil
		}
		if d.migrationPreview != nil && !d.migrationPreview.applying {
			d.migrationPreview = nil
		}
	} else {
		if d.migrationPendingFile != nil && d.migrationPendingFile.token == trimmed {
			clear(d.migrationPendingFile.raw)
			d.migrationPendingFile = nil
		}
		if d.migrationPreview != nil && d.migrationPreview.id == trimmed && !d.migrationPreview.applying {
			d.migrationPreview = nil
		}
	}
	d.migrationMu.Unlock()
	return nil
}
