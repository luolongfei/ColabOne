package controller

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/luolongfei/ColabOne/console/internal/migration"
)

var (
	reMigrationCloudProjectID = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
)

// ColabOneControllerMigrationStatus reports runtime guards relevant to migration UI.
type ColabOneControllerMigrationStatus struct {
	HasActiveOperation  bool `json:"has_active_operation"`
	HasRunningInstances bool `json:"has_running_instances"`
}

// ColabOneControllerMigrationResult describes what modules were applied by the controller.
type ColabOneControllerMigrationResult struct {
	AppliedModules  []string `json:"applied_modules"`
	Warnings        []string `json:"warnings,omitempty"`
	AccountImported bool     `json:"account_imported"`
	AccountLoggedIn bool     `json:"account_logged_in"`
	AccountEmail    string   `json:"account_email,omitempty"`
	Locale          string   `json:"locale,omitempty"`
}

// MigrationStatus reports whether an async operation or remote GPU instance is currently active.
func (c *ColabOneController) MigrationStatus() ColabOneControllerMigrationStatus {
	return ColabOneControllerMigrationStatus{
		HasActiveOperation:  c.opBusy.Load(),
		HasRunningInstances: len(c.vaultSessions()) > 0,
	}
}

// ExportMigrationPayload collects all requested modules from the vault and local
// controller stores into a ColabOneMigrationPayload ready for encryption or summary preview.
func (c *ColabOneController) ExportMigrationPayload(
	modules []string,
	appVersion string,
	sourceOS string,
	desktopSettings *migration.ColabOneMigrationSettingsData,
) (*migration.ColabOneMigrationPayload, error) {
	normModules, err := migration.NormalizeModules(modules)
	if err != nil {
		return nil, err
	}
	want := make(map[string]bool, len(normModules))
	for _, m := range normModules {
		want[m] = true
	}

	payload := &migration.ColabOneMigrationPayload{
		SchemaVersion:   migration.COLABONE_MIGRATION_SCHEMA_VERSION,
		AppVersion:      strings.TrimSpace(appVersion),
		ExportedAt:      time.Now().UTC().Format(time.RFC3339),
		SourceOS:        strings.TrimSpace(sourceOS),
		IncludedModules: normModules,
	}

	if want[migration.ModuleAccount] {
		payload.Account = c.exportMigrationAccount()
	}
	if want[migration.ModuleCredentials] {
		payload.Credentials = c.exportMigrationCredentials()
	}
	if want[migration.ModuleSettings] {
		payload.Settings = c.exportMigrationSettings(desktopSettings)
	}
	if want[migration.ModuleLibraryWorkflows] {
		payload.LibraryWorkflows = c.exportMigrationLibraryWorkflows()
	}
	if want[migration.ModuleCUUsage] {
		if c.cuUsage != nil {
			_ = c.cuUsage.Flush()
			payload.CUUsage = c.cuUsage.ExportDiskMap()
		} else {
			payload.CUUsage = map[string]any{
				"version":        colabOneCUUsageSchemaVersion,
				"bucket_seconds": colabOneCUBucketSeconds,
				"accounts":       map[string]any{},
			}
		}
	}
	if want[migration.ModuleWorkflowDrafts] {
		payload.WorkflowDrafts = c.exportMigrationWorkflowDrafts()
	}

	return payload, nil
}

func (c *ColabOneController) exportMigrationAccount() *migration.ColabOneMigrationAccountData {
	out := &migration.ColabOneMigrationAccountData{}
	if c.vault == nil {
		return out
	}
	if rawColab, ok := c.vault.Get("colab"); ok {
		if m := asMap(rawColab); len(m) > 0 {
			out.Colab = deepCopyMap(m)
		}
	}
	// Never export orphan profile/project entries when no Google login credentials exist.
	if len(out.Colab) == 0 {
		return out
	}
	if rawProf, ok := c.vault.Get("google_profile"); ok {
		prof := make(map[string]string)
		switch m := rawProf.(type) {
		case map[string]string:
			for k, v := range m {
				if s := strings.TrimSpace(v); s != "" {
					prof[k] = s
				}
			}
		default:
			if am := asMap(rawProf); len(am) > 0 {
				for k, v := range am {
					if s := strings.TrimSpace(asString(v)); s != "" {
						prof[k] = s
					}
				}
			}
		}
		if len(prof) > 0 {
			out.GoogleProfile = prof
		}
	}
	if rawProj, ok := c.vault.Get("cloud_project"); ok {
		out.CloudProject = strings.TrimSpace(asString(rawProj))
	}
	return out
}

func (c *ColabOneController) exportMigrationCredentials() *migration.ColabOneMigrationCredentialsData {
	out := &migration.ColabOneMigrationCredentialsData{}
	if c.vault == nil {
		return out
	}
	if v, ok := c.vault.Get("hf"); ok {
		out.HFToken = strings.TrimSpace(asString(v))
	}
	if v, ok := c.vault.Get("assign_proxy"); ok {
		out.AssignProxy = strings.TrimSpace(asString(v))
	}
	if v, ok := c.vault.Get("public_api"); ok {
		if m := asMap(v); len(m) > 0 {
			out.PublicAPI = deepCopyMap(m)
		}
	}
	if v, ok := c.vault.Get("comfyui_canvas"); ok {
		if m := asMap(v); len(m) > 0 {
			out.ComfyUICanvas = deepCopyMap(m)
		}
	}
	return out
}

func (c *ColabOneController) exportMigrationSettings(desktopSettings *migration.ColabOneMigrationSettingsData) *migration.ColabOneMigrationSettingsData {
	c.mu.Lock()
	ctrlSettings := deepCopyMap(c.settings)
	c.mu.Unlock()

	out := &migration.ColabOneMigrationSettingsData{
		Controller: ctrlSettings,
	}
	if desktopSettings != nil {
		out.Appearance = strings.TrimSpace(desktopSettings.Appearance)
		out.CloseBehavior = strings.TrimSpace(desktopSettings.CloseBehavior)
		if desktopSettings.Updater != nil {
			upCopy := *desktopSettings.Updater
			out.Updater = &upCopy
		}
		if desktopSettings.UIPrefs != nil {
			uiCopy := *desktopSettings.UIPrefs
			if desktopSettings.UIPrefs.PrivacyMaskDefault != nil {
				v := *desktopSettings.UIPrefs.PrivacyMaskDefault
				uiCopy.PrivacyMaskDefault = &v
			}
			if desktopSettings.UIPrefs.LogsWidth != nil {
				v := *desktopSettings.UIPrefs.LogsWidth
				uiCopy.LogsWidth = &v
			}
			out.UIPrefs = &uiCopy
		}
	}
	return out
}

func (c *ColabOneController) exportMigrationLibraryWorkflows() *migration.ColabOneMigrationLibraryWorkflowsData {
	c.mu.Lock()
	commonIDs := make([]string, 0, len(c.commonIDs))
	for id := range c.commonIDs {
		commonIDs = append(commonIDs, id)
	}
	sort.Strings(commonIDs)

	customModels := make([]map[string]any, 0, len(c.customModels))
	modelIDs := make([]string, 0, len(c.customModels))
	for id := range c.customModels {
		modelIDs = append(modelIDs, id)
	}
	sort.Strings(modelIDs)
	for _, id := range modelIDs {
		customModels = append(customModels, deepCopyMap(c.customModels[id]))
	}

	overrides := make(map[string]string, len(c.apiIDOverrides))
	for k, v := range c.apiIDOverrides {
		overrides[k] = v
	}

	repos := deepCopyMapSlice(c.commonRepos)
	defaultVer := c.defaultCommonVersion

	wfIDs := make([]string, 0, len(c.customWorkflows))
	for id := range c.customWorkflows {
		wfIDs = append(wfIDs, id)
	}
	sort.Strings(wfIDs)
	workflows := make([]map[string]any, 0, len(wfIDs))
	for _, id := range wfIDs {
		workflows = append(workflows, deepCopyMap(c.customWorkflows[id]))
	}
	c.mu.Unlock()

	return &migration.ColabOneMigrationLibraryWorkflowsData{
		Library: map[string]any{
			"default_common_version": defaultVer,
			"common_ids":             commonIDs,
			"custom_models":          customModels,
			"api_id_overrides":       overrides,
			"common_repos":           repos,
		},
		Workflows: workflows,
	}
}

func (c *ColabOneController) exportMigrationWorkflowDrafts() map[string]any {
	c.mu.Lock()
	snapshot := cloneWorkflowDraftMap(c.workflowDrafts)
	c.mu.Unlock()

	drafts := make(map[string]any)
	for wfID, draft := range snapshot {
		if draft == nil {
			continue
		}
		textFields := make(map[string]any)
		for fk, fv := range draft.Fields {
			// Strip fields bound to local image/audio media files so imported
			// drafts never carry dangling/ghost media filenames across machines.
			if _, isMedia := draft.Media[fk]; isMedia {
				continue
			}
			textFields[fk] = deepCopyAny(fv)
		}
		if len(textFields) == 0 {
			continue
		}
		drafts[wfID] = map[string]any{
			"workflow_id": wfID,
			"fields":      textFields,
			"media":       map[string]any{},
			"updated_at":  draft.UpdatedAt,
		}
	}
	return map[string]any{
		"version": colabOneWorkflowDraftsSchemaVersion,
		"drafts":  drafts,
	}
}

// ---------------------------------------------------------------------------
// Import validation, staging, rollback, and hot-apply
// ---------------------------------------------------------------------------

type preparedMigrationAccount struct {
	colab        map[string]any
	profile      map[string]string
	cloudProject string
}

type preparedMigrationCredentials struct {
	hfToken     string
	assignProxy string
	publicAPI   map[string]any
	comfyCanvas map[string]any
}

type preparedMigrationLibraryWorkflows struct {
	defaultCommonVersion   int
	commonIDs              map[string]bool
	customModels           map[string]map[string]any
	apiIDOverrides         map[string]string
	commonRepos            []map[string]any
	customWorkflows        map[string]map[string]any
	remoteWorkflowVersions map[string]float64
}

type vaultBackupEntry struct {
	existed bool
	value   any
}

// ApplyMigrationPayload validates selected modules in memory first, then applies
// vault and disk updates atomically with best-effort rollback on write failure,
// and refreshes controller runtime state immediately without requiring restart.
func (c *ColabOneController) ApplyMigrationPayload(
	ctx context.Context,
	payload *migration.ColabOneMigrationPayload,
	selectedModules []string,
) (*ColabOneControllerMigrationResult, error) {
	if err := migration.ValidatePayload(payload); err != nil {
		return nil, err
	}
	normSelected, err := migration.NormalizeModules(selectedModules)
	if err != nil {
		return nil, err
	}

	// Intersect user selection with modules actually present in the archive.
	applyList := make([]string, 0, len(normSelected))
	applySet := make(map[string]bool, len(normSelected))
	for _, m := range normSelected {
		if payload.HasModule(m) {
			applyList = append(applyList, m)
			applySet[m] = true
		}
	}
	if len(applyList) == 0 {
		return nil, migration.ErrNoModulesSelected
	}

	if !c.opBusy.CompareAndSwap(false, true) {
		return nil, errors.New("请等待当前操作完成后再导入迁移文件")
	}
	defer c.opBusy.Store(false)

	if applySet[migration.ModuleAccount] && len(c.vaultSessions()) > 0 {
		return nil, errors.New("切换账号前请先释放当前实例")
	}

	// ---- Phase 1: Complete in-memory validation before touching vault or disk ----

	var (
		prepAccount     *preparedMigrationAccount
		prepCreds       *preparedMigrationCredentials
		prepSettings    map[string]any
		prepLibWF       *preparedMigrationLibraryWorkflows
		prepCUUsageRaw  map[string]any
		prepDrafts      map[string]*ColabOneWorkflowDraft
		warnings        []string
	)

	if applySet[migration.ModuleAccount] {
		prepAccount, err = validateMigrationAccount(payload.Account)
		if err != nil {
			return nil, err
		}
	}

	if applySet[migration.ModuleCredentials] {
		prepCreds, err = c.validateMigrationCredentials(payload.Credentials)
		if err != nil {
			return nil, err
		}
	}

	if applySet[migration.ModuleSettings] {
		hasProxyAfter := false
		if prepCreds != nil {
			hasProxyAfter = prepCreds.assignProxy != ""
		} else if c.vault != nil {
			if raw, ok := c.vault.Get("assign_proxy"); ok && strings.TrimSpace(asString(raw)) != "" {
				hasProxyAfter = true
			}
		}
		prepSettings, err = c.validateMigrationSettings(payload.Settings, hasProxyAfter)
		if err != nil {
			return nil, err
		}
	}

	if applySet[migration.ModuleLibraryWorkflows] {
		prepLibWF, err = c.validateMigrationLibraryWorkflows(payload.LibraryWorkflows)
		if err != nil {
			return nil, err
		}
	}

	if applySet[migration.ModuleCUUsage] {
		if _, err := validateCUUsageDiskMap(payload.CUUsage); err != nil {
			return nil, err
		}
		prepCUUsageRaw = payload.CUUsage
	}

	if applySet[migration.ModuleWorkflowDrafts] {
		prepDrafts, err = validateMigrationWorkflowDrafts(payload.WorkflowDrafts)
		if err != nil {
			return nil, err
		}
	}

	// ---- Phase 2: Snapshot rollback state & apply vault + disk mutations ----

	// 2a. Capture vault backup & write vault keys.
	vaultKeysToTouch := make([]string, 0, 7)
	if prepAccount != nil {
		vaultKeysToTouch = append(vaultKeysToTouch, "colab", "google_profile", "cloud_project")
	}
	if prepCreds != nil {
		vaultKeysToTouch = append(vaultKeysToTouch, "hf", "assign_proxy", "public_api", "comfyui_canvas")
	}
	vaultBackups := make(map[string]vaultBackupEntry, len(vaultKeysToTouch))
	if c.vault != nil {
		for _, k := range vaultKeysToTouch {
			val, ok := c.vault.Get(k)
			vaultBackups[k] = vaultBackupEntry{existed: ok, value: deepCopyAny(val)}
		}
	}

	rollbackVault := func() {
		if c.vault == nil {
			return
		}
		for _, k := range vaultKeysToTouch {
			prev := vaultBackups[k]
			if prev.existed {
				_ = c.vault.Set(k, prev.value)
			} else {
				_ = c.vault.Delete(k)
			}
		}
	}

	applyVaultKey := func(key string, val any, hasValue bool) error {
		if c.vault == nil {
			return nil
		}
		if hasValue {
			return c.vault.Set(key, val)
		}
		return c.vault.Delete(key)
	}

	if prepAccount != nil {
		if err := applyVaultKey("colab", prepAccount.colab, len(prepAccount.colab) > 0); err != nil {
			rollbackVault()
			return nil, fmt.Errorf("写入 Google 登录凭据失败：%w", err)
		}
		if err := applyVaultKey("google_profile", prepAccount.profile, len(prepAccount.profile) > 0); err != nil {
			rollbackVault()
			return nil, fmt.Errorf("写入 Google 账号资料失败：%w", err)
		}
		if err := applyVaultKey("cloud_project", prepAccount.cloudProject, prepAccount.cloudProject != ""); err != nil {
			rollbackVault()
			return nil, fmt.Errorf("写入 Google 云盘项目配置失败：%w", err)
		}
	}

	if prepCreds != nil {
		if err := applyVaultKey("hf", prepCreds.hfToken, prepCreds.hfToken != ""); err != nil {
			rollbackVault()
			return nil, fmt.Errorf("写入 Hugging Face Token 失败：%w", err)
		}
		if err := applyVaultKey("assign_proxy", prepCreds.assignProxy, prepCreds.assignProxy != ""); err != nil {
			rollbackVault()
			return nil, fmt.Errorf("写入分配代理配置失败：%w", err)
		}
		if err := applyVaultKey("public_api", prepCreds.publicAPI, len(prepCreds.publicAPI) > 0); err != nil {
			rollbackVault()
			return nil, fmt.Errorf("写入对外 API 配置失败：%w", err)
		}
		if err := applyVaultKey("comfyui_canvas", prepCreds.comfyCanvas, len(prepCreds.comfyCanvas) > 0); err != nil {
			rollbackVault()
			return nil, fmt.Errorf("写入 ComfyUI 画布配置失败：%w", err)
		}
	}

	// If credentials cleared assign_proxy while settings module wasn't imported, disable assign_proxy_enabled cleanly.
	needDisableProxyFlag := prepCreds != nil && prepCreds.assignProxy == "" && prepSettings == nil

	// 2b. Apply controller-persisted files under persistMu -> mu.
	c.persistMu.Lock()

	var (
		prevSettings    map[string]any
		wroteSettings   bool
		prevLib         *preparedMigrationLibraryWorkflows
		wroteLibrary    bool
		wroteWorkflows  bool
		prevDrafts      map[string]*ColabOneWorkflowDraft
		wroteDrafts     bool
	)

	rollbackControllerFilesLocked := func() {
		if wroteDrafts && prevDrafts != nil {
			c.mu.Lock()
			c.workflowDrafts = prevDrafts
			snap := cloneWorkflowDraftMap(c.workflowDrafts)
			c.mu.Unlock()
			_ = c.writeWorkflowDraftsFileLocked(snap)
		}
		if wroteWorkflows && prevLib != nil {
			c.mu.Lock()
			c.customWorkflows = prevLib.customWorkflows
			c.remoteWorkflowVersions = prevLib.remoteWorkflowVersions
			list := make([]map[string]any, 0, len(c.customWorkflows))
			for _, wf := range c.customWorkflows {
				list = append(list, deepCopyMap(wf))
			}
			c.mu.Unlock()
			_ = AtomicJSON(filepath.Join(c.directory, "workflows.json"), list)
		}
		if wroteLibrary && prevLib != nil {
			c.mu.Lock()
			c.defaultCommonVersion = prevLib.defaultCommonVersion
			c.commonIDs = prevLib.commonIDs
			c.customModels = prevLib.customModels
			c.apiIDOverrides = prevLib.apiIDOverrides
			c.commonRepos = prevLib.commonRepos
			libPayload := c.buildLibraryPayloadLocked()
			c.mu.Unlock()
			_ = AtomicJSON(filepath.Join(c.directory, "library.json"), libPayload)
		}
		if wroteSettings && prevSettings != nil {
			c.mu.Lock()
			c.settings = prevSettings
			c.mu.Unlock()
			_ = AtomicJSON(filepath.Join(c.directory, "settings.json"), prevSettings)
		}
	}

	if prepSettings != nil || needDisableProxyFlag {
		c.mu.Lock()
		prevSettings = deepCopyMap(c.settings)
		if prepSettings != nil {
			c.settings = deepCopyMap(prepSettings)
		} else if needDisableProxyFlag {
			c.settings["assign_proxy_enabled"] = false
		}
		nextSettings := deepCopyMap(c.settings)
		c.mu.Unlock()
		if err := AtomicJSON(filepath.Join(c.directory, "settings.json"), nextSettings); err != nil {
			c.mu.Lock()
			c.settings = prevSettings
			c.mu.Unlock()
			c.persistMu.Unlock()
			rollbackVault()
			return nil, fmt.Errorf("写入软件设置失败：%w", err)
		}
		wroteSettings = true
	}

	if prepLibWF != nil {
		c.mu.Lock()
		prevLib = c.snapshotLibraryWorkflowsLocked()
		c.defaultCommonVersion = prepLibWF.defaultCommonVersion
		c.commonIDs = prepLibWF.commonIDs
		c.customModels = prepLibWF.customModels
		c.apiIDOverrides = prepLibWF.apiIDOverrides
		c.commonRepos = prepLibWF.commonRepos
		libPayload := c.buildLibraryPayloadLocked()
		c.mu.Unlock()

		if err := AtomicJSON(filepath.Join(c.directory, "library.json"), libPayload); err != nil {
			c.mu.Lock()
			c.restoreLibraryWorkflowsLocked(prevLib)
			c.mu.Unlock()
			rollbackControllerFilesLocked()
			c.persistMu.Unlock()
			rollbackVault()
			return nil, fmt.Errorf("写入模型库配置失败：%w", err)
		}
		wroteLibrary = true

		c.mu.Lock()
		c.customWorkflows = prepLibWF.customWorkflows
		c.remoteWorkflowVersions = make(map[string]float64)
		wfList := make([]map[string]any, 0, len(c.customWorkflows))
		for _, wf := range c.customWorkflows {
			wfList = append(wfList, deepCopyMap(wf))
		}
		c.mu.Unlock()

		if err := AtomicJSON(filepath.Join(c.directory, "workflows.json"), wfList); err != nil {
			c.mu.Lock()
			c.customWorkflows = prevLib.customWorkflows
			c.remoteWorkflowVersions = prevLib.remoteWorkflowVersions
			c.mu.Unlock()
			rollbackControllerFilesLocked()
			c.persistMu.Unlock()
			rollbackVault()
			return nil, fmt.Errorf("写入自定义工作流失败：%w", err)
		}
		wroteWorkflows = true
	}

	if applySet[migration.ModuleWorkflowDrafts] {
		c.mu.Lock()
		prevDrafts = cloneWorkflowDraftMap(c.workflowDrafts)
		c.workflowDrafts = cloneWorkflowDraftMap(prepDrafts)
		nextDraftsSnap := cloneWorkflowDraftMap(c.workflowDrafts)
		c.mu.Unlock()

		if err := c.writeWorkflowDraftsFileLocked(nextDraftsSnap); err != nil {
			c.mu.Lock()
			c.workflowDrafts = prevDrafts
			c.mu.Unlock()
			rollbackControllerFilesLocked()
			c.persistMu.Unlock()
			rollbackVault()
			return nil, fmt.Errorf("写入 ComfyUI 草稿失败：%w", err)
		}
		wroteDrafts = true
	}

	c.persistMu.Unlock()

	// 2c. Apply CU usage outside controller persistMu (cuUsage has its own lock hierarchy).
	if applySet[migration.ModuleCUUsage] && c.cuUsage != nil {
		if err := c.cuUsage.ReplaceFromDiskMap(prepCUUsageRaw); err != nil {
			c.persistMu.Lock()
			rollbackControllerFilesLocked()
			c.persistMu.Unlock()
			rollbackVault()
			return nil, fmt.Errorf("写入 CU 算力消耗历史失败：%w", err)
		}
	}

	// Only prune unreferenced draft media files once every phase-2 write has succeeded,
	// so a rollback (e.g. cu_usage persistence failure) never destroys prior media files.
	if wroteDrafts {
		c.persistMu.Lock()
		c.pruneUnreferencedDraftMediaLocked()
		c.persistMu.Unlock()
	}

	// ---- Phase 3: Hot-apply in-memory state & trigger background sync ----

	result := &ColabOneControllerMigrationResult{
		AppliedModules: applyList,
		Warnings:       warnings,
	}

	if prepSettings != nil {
		loc := strings.TrimSpace(asString(prepSettings["locale"]))
		result.Locale = loc
		if loc != "" && c.auth != nil {
			c.auth.SetLocale(loc)
		}
		c.mu.Lock()
		c.lastConfig = 0
		for _, st := range c.instanceStates {
			st.LastConfig = 0
		}
		c.mu.Unlock()
		c.observerWake.Set()
	}

	if prepLibWF != nil {
		c.mu.Lock()
		rtState := asString(asMap(c.data["runtime"])["state"])
		c.mu.Unlock()
		if rtState != "ready" {
			c.Update(map[string]any{
				"models":    c.Presets(),
				"workflows": c.mergeWorkflows(nil),
			})
		}
		c.observerWake.Set()
	}

	if prepAccount != nil {
		result.AccountImported = true
		hasLogin := len(prepAccount.colab) > 0
		email := strings.TrimSpace(prepAccount.profile["email"])
		result.AccountLoggedIn = hasLogin
		result.AccountEmail = email

		if resetter, ok := c.auth.(AuthStateResetter); ok {
			resetter.ResetAccountState()
		}
		if c.cuUsage != nil {
			c.cuUsage.ResetActiveAccount()
		}
		c.mu.Lock()
		c.lastAuth = 0
		c.remoteWorkflowVersions = make(map[string]float64)
		c.releasePlan = nil
		c.data["error"] = ""
		c.mu.Unlock()

		c.resetMonitorToAuto(true, true)
		c.Update(map[string]any{
			"account": map[string]any{
				"logged_in": hasLogin,
				"email":     email,
			},
			"models":        c.Presets(),
			"workflows":     c.mergeWorkflows(nil),
			"tts_engines":   c.builtinTtsEngines(),
			"comfy_outputs": []any{},
			"auth_url":      "",
		})
		c.observerWake.Set()

		if hasLogin && c.provider != nil {
			importedCreds := deepCopyMap(prepAccount.colab)
			c.spawn(func() {
				refreshCtx, cancel := context.WithTimeout(c.rootCtx, 25*time.Second)
				defer cancel()
				account, err := c.provider.Account(refreshCtx)
				if err != nil {
					return
				}
				// Guard against stale async completion if user logged out, re-logged in,
				// or imported a different account while provider.Account was in flight.
				if c.vault != nil {
					currentRaw, ok := c.vault.Get("colab")
					if !ok || !sameColabCredentialIdentity(importedCreds, asMap(currentRaw)) {
						return
					}
				}
				if account == nil {
					account = map[string]any{"logged_in": false}
				}
				c.Update(map[string]any{"account": account})
				c.mu.Lock()
				c.lastAuth = nowUnixFloat()
				c.mu.Unlock()
				c.observerWake.Set()
			})
		}
	}

	c.log(formatMigrationImportLog(applyList), "迁移", "info")
	return result, nil
}

func (c *ColabOneController) buildLibraryPayloadLocked() map[string]any {
	commonIDs := make([]string, 0, len(c.commonIDs))
	for id := range c.commonIDs {
		commonIDs = append(commonIDs, id)
	}
	sort.Strings(commonIDs)

	customModels := make([]map[string]any, 0, len(c.customModels))
	for _, spec := range c.customModels {
		customModels = append(customModels, deepCopyMap(spec))
	}
	overrides := make(map[string]string, len(c.apiIDOverrides))
	for k, v := range c.apiIDOverrides {
		overrides[k] = v
	}
	return map[string]any{
		"default_common_version": c.defaultCommonVersion,
		"common_ids":             commonIDs,
		"custom_models":          customModels,
		"api_id_overrides":       overrides,
		"common_repos":           deepCopyMapSlice(c.commonRepos),
	}
}

func (c *ColabOneController) snapshotLibraryWorkflowsLocked() *preparedMigrationLibraryWorkflows {
	commonIDs := make(map[string]bool, len(c.commonIDs))
	for k, v := range c.commonIDs {
		commonIDs[k] = v
	}
	customModels := make(map[string]map[string]any, len(c.customModels))
	for k, v := range c.customModels {
		customModels[k] = deepCopyMap(v)
	}
	overrides := make(map[string]string, len(c.apiIDOverrides))
	for k, v := range c.apiIDOverrides {
		overrides[k] = v
	}
	workflows := make(map[string]map[string]any, len(c.customWorkflows))
	for k, v := range c.customWorkflows {
		workflows[k] = deepCopyMap(v)
	}
	remoteVersions := make(map[string]float64, len(c.remoteWorkflowVersions))
	for k, v := range c.remoteWorkflowVersions {
		remoteVersions[k] = v
	}
	return &preparedMigrationLibraryWorkflows{
		defaultCommonVersion:   c.defaultCommonVersion,
		commonIDs:              commonIDs,
		customModels:           customModels,
		apiIDOverrides:         overrides,
		commonRepos:            deepCopyMapSlice(c.commonRepos),
		customWorkflows:        workflows,
		remoteWorkflowVersions: remoteVersions,
	}
}

func (c *ColabOneController) restoreLibraryWorkflowsLocked(snap *preparedMigrationLibraryWorkflows) {
	if snap == nil {
		return
	}
	c.defaultCommonVersion = snap.defaultCommonVersion
	c.commonIDs = snap.commonIDs
	c.customModels = snap.customModels
	c.apiIDOverrides = snap.apiIDOverrides
	c.commonRepos = snap.commonRepos
	c.customWorkflows = snap.customWorkflows
	if snap.remoteWorkflowVersions != nil {
		c.remoteWorkflowVersions = snap.remoteWorkflowVersions
	} else {
		c.remoteWorkflowVersions = make(map[string]float64)
	}
}

func sameColabCredentialIdentity(a, b map[string]any) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	rtA := strings.TrimSpace(asString(a["refresh_token"]))
	rtB := strings.TrimSpace(asString(b["refresh_token"]))
	if rtA != "" || rtB != "" {
		return rtA == rtB
	}
	tokA := strings.TrimSpace(asString(a["token"]))
	tokB := strings.TrimSpace(asString(b["token"]))
	return tokA != "" && tokA == tokB
}

func validateMigrationAccount(src *migration.ColabOneMigrationAccountData) (*preparedMigrationAccount, error) {
	prep := &preparedMigrationAccount{}
	if src == nil {
		return prep, nil
	}
	if len(src.Colab) > 0 {
		allowedStrFields := []string{
			"token", "refresh_token", "id_token", "token_uri",
			"client_id", "client_secret", "quota_project_id",
			"rapt_token", "universe_domain", "account", "expiry",
		}
		clean := make(map[string]any, len(src.Colab))
		for _, k := range allowedStrFields {
			if raw, exists := src.Colab[k]; exists && raw != nil {
				s, ok := raw.(string)
				if !ok {
					return nil, errors.New("迁移文件中的 Google 凭据字段格式无效")
				}
				if s != "" {
					clean[k] = s
				}
			}
		}
		if rawScopes, exists := src.Colab["scopes"]; exists && rawScopes != nil {
			switch sc := rawScopes.(type) {
			case []any:
				list := make([]string, 0, len(sc))
				for _, item := range sc {
					s, ok := item.(string)
					if !ok {
						return nil, errors.New("迁移文件中的 Google 授权范围格式无效")
					}
					if trimmed := strings.TrimSpace(s); trimmed != "" {
						list = append(list, trimmed)
					}
				}
				if len(list) > 0 {
					clean["scopes"] = list
				}
			case []string:
				list := make([]string, 0, len(sc))
				for _, item := range sc {
					if trimmed := strings.TrimSpace(item); trimmed != "" {
						list = append(list, trimmed)
					}
				}
				if len(list) > 0 {
					clean["scopes"] = list
				}
			case string:
				if fields := strings.Fields(sc); len(fields) > 0 {
					clean["scopes"] = fields
				}
			default:
				return nil, errors.New("迁移文件中的 Google 授权范围格式无效")
			}
		}
		hasIdent := strings.TrimSpace(asString(clean["token"])) != "" ||
			strings.TrimSpace(asString(clean["refresh_token"])) != "" ||
			strings.TrimSpace(asString(clean["client_id"])) != ""
		if !hasIdent {
			return nil, errors.New("迁移文件中的 Google 登录凭据不完整")
		}
		prep.colab = clean
	}

	// Only restore Google profile & Drive quota project when Google OAuth credentials
	// are present; never write orphan profile/project metadata without a login.
	if len(prep.colab) > 0 {
		if len(src.GoogleProfile) > 0 {
			prof := make(map[string]string, 2)
			if email := strings.TrimSpace(src.GoogleProfile["email"]); email != "" {
				prof["email"] = email
			}
			if sub := strings.TrimSpace(src.GoogleProfile["sub"]); sub != "" {
				prof["sub"] = sub
			}
			if len(prof) > 0 {
				prep.profile = prof
			}
		}

		if proj := strings.TrimSpace(src.CloudProject); proj != "" {
			if reMigrationCloudProjectID.MatchString(proj) {
				prep.cloudProject = proj
			}
		}
	}
	return prep, nil
}

func validateCleanPrintableToken(raw string, maxLen int) (string, error) {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return "", nil
	}
	if len(cleaned) > maxLen {
		return "", errors.New("密钥长度超出允许范围")
	}
	for _, r := range cleaned {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", errors.New("密钥包含非法空白或控制字符")
		}
	}
	return cleaned, nil
}

func (c *ColabOneController) validateMigrationCredentials(src *migration.ColabOneMigrationCredentialsData) (*preparedMigrationCredentials, error) {
	prep := &preparedMigrationCredentials{}
	if src == nil {
		return prep, nil
	}
	if strings.TrimSpace(src.HFToken) != "" {
		cleaned, err := validateCleanHFToken(src.HFToken)
		if err != nil {
			return nil, fmt.Errorf("迁移文件中的 Hugging Face Token 无效：%w", err)
		}
		prep.hfToken = cleaned
	}
	if strings.TrimSpace(src.AssignProxy) != "" {
		if c.assignProxy != nil {
			parsed, err := c.assignProxy.ParseAssignProxy(src.AssignProxy)
			if err != nil {
				return nil, fmt.Errorf("迁移文件中的分配代理地址无效：%w", err)
			}
			prep.assignProxy = parsed.NormalizedURL
		} else {
			prep.assignProxy = strings.TrimSpace(src.AssignProxy)
		}
	}
	if len(src.PublicAPI) > 0 {
		domain := strings.TrimSpace(asString(src.PublicAPI["domain"]))
		tunnelTok := strings.TrimSpace(asString(src.PublicAPI["tunnel_token"]))
		apiKey := strings.TrimSpace(asString(src.PublicAPI["api_key"]))
		if domain != "" || tunnelTok != "" {
			if apiKey == "" {
				apiKey = generateAPIKey()
			}
			if c.publicAPI != nil {
				validated, err := c.validatePublicAPIConfig(map[string]any{
					"mode":         "named",
					"domain":       domain,
					"tunnel_token": tunnelTok,
					"api_key":      apiKey,
				})
				if err != nil {
					return nil, errors.New("迁移文件中的对外 API 配置无效")
				}
				prep.publicAPI = map[string]any{
					"mode":         "named",
					"domain":       asString(validated["domain"]),
					"tunnel_token": asString(validated["tunnel_token"]),
					"api_key":      asString(validated["api_key"]),
				}
			} else {
				prep.publicAPI = map[string]any{
					"mode":         "named",
					"domain":       domain,
					"tunnel_token": tunnelTok,
					"api_key":      apiKey,
				}
			}
		} else if apiKey != "" {
			cleanedKey, err := validateCleanPrintableToken(apiKey, 256)
			if err != nil {
				return nil, errors.New("迁移文件中的对外 API Key 格式无效")
			}
			prep.publicAPI = map[string]any{"api_key": cleanedKey}
		}
	}
	if len(src.ComfyUICanvas) > 0 {
		domain := strings.TrimSpace(asString(src.ComfyUICanvas["domain"]))
		tunnelTok := strings.TrimSpace(asString(src.ComfyUICanvas["tunnel_token"]))
		if domain != "" || tunnelTok != "" {
			if c.comfy != nil {
				validated, err := c.validateComfyCanvasConfig(map[string]any{
					"mode":         "named",
					"domain":       domain,
					"tunnel_token": tunnelTok,
				})
				if err != nil {
					return nil, errors.New("迁移文件中的 ComfyUI 画布配置无效")
				}
				prep.comfyCanvas = map[string]any{
					"mode":         "named",
					"domain":       asString(validated["domain"]),
					"tunnel_token": asString(validated["tunnel_token"]),
				}
			} else {
				prep.comfyCanvas = map[string]any{
					"mode":         "named",
					"domain":       domain,
					"tunnel_token": tunnelTok,
				}
			}
		}
	}
	return prep, nil
}

func (c *ColabOneController) validateMigrationSettings(src *migration.ColabOneMigrationSettingsData, hasProxyAfter bool) (map[string]any, error) {
	next := map[string]any{
		"gpu":                    COLABONE_DEFAULT_GPU,
		"idle_minutes":           30,
		"idle_countdown_seconds": 60,
		"auto_drive":             true,
		"download_threads":       16,
		"assign_max_attempts":    COLABONE_DEFAULT_ASSIGN_MAX_ATTEMPTS,
		"assign_proxy_enabled":   false,
		"locale":                 "",
		"idle_policy":            3,
		"rclone_drive_policy":    1,
		"default_gpu_policy":     COLABONE_DEFAULT_GPU_POLICY_VERSION,
	}
	if src == nil {
		return next, nil
	}
	if len(src.Controller) > 0 {
		raw := src.Controller
		if v, ok := raw["gpu"].(string); ok && validColabOneGPUs[v] {
			next["gpu"] = v
		}
		if v, ok := asInt(raw["idle_minutes"]); ok && v >= 1 && v <= 240 {
			next["idle_minutes"] = v
		}
		if v, ok := asInt(raw["idle_countdown_seconds"]); ok && v >= 0 && v <= 600 {
			next["idle_countdown_seconds"] = v
		}
		if v, ok := isStrictBool(raw["auto_drive"]); ok {
			next["auto_drive"] = v
		}
		if v, ok := asExactInt(raw["download_threads"]); ok && (v == 16 || v == 32) {
			next["download_threads"] = v
		}
		if v, ok := asExactInt(raw["assign_max_attempts"]); ok && v >= COLABONE_MIN_ASSIGN_MAX_ATTEMPTS && v <= COLABONE_MAX_ASSIGN_MAX_ATTEMPTS {
			next["assign_max_attempts"] = v
		}
		if v, ok := isStrictBool(raw["assign_proxy_enabled"]); ok {
			next["assign_proxy_enabled"] = v && hasProxyAfter
		}
		if v, ok := raw["locale"].(string); ok && (v == "" || validColabOneLocales[v]) {
			next["locale"] = v
		}
	}
	// If controller settings carried auto-detected locale ("") while UI prefs recorded
	// the effective active locale on the source computer, persist it into settings.json
	// so native splash/tray and WebView stay aligned on subsequent launches.
	if asString(next["locale"]) == "" && src.UIPrefs != nil {
		if uiLoc := strings.TrimSpace(src.UIPrefs.Locale); validColabOneLocales[uiLoc] {
			next["locale"] = uiLoc
		}
	}
	return next, nil
}

func (c *ColabOneController) validateMigrationLibraryWorkflows(src *migration.ColabOneMigrationLibraryWorkflowsData) (*preparedMigrationLibraryWorkflows, error) {
	prep := &preparedMigrationLibraryWorkflows{
		defaultCommonVersion: COLABONE_DEFAULT_COMMON_VERSION,
		commonIDs:            make(map[string]bool),
		customModels:         make(map[string]map[string]any),
		apiIDOverrides:       make(map[string]string),
		commonRepos:          make([]map[string]any, 0),
		customWorkflows:      make(map[string]map[string]any),
	}
	if src == nil {
		return prep, nil
	}

	if raw := src.Library; raw != nil {
		if ver, ok := asInt(raw["default_common_version"]); ok && ver >= COLABONE_DEFAULT_COMMON_VERSION {
			prep.defaultCommonVersion = ver
		}
		for _, item := range asAnySlice(raw["common_ids"]) {
			s := asString(item)
			if reModelID.MatchString(s) {
				prep.commonIDs[s] = true
			}
		}
		for _, item := range asMapSlice(raw["custom_models"]) {
			spec, err := c.validateModelSpec(item)
			if err != nil || spec == nil {
				return nil, fmt.Errorf("迁移文件中的自定义模型配置无效：%v", err)
			}
			mid := asString(spec["id"])
			if strings.HasPrefix(mid, "hf-") {
				spec["min_vram_mib"] = 0
			}
			prep.customModels[mid] = spec
		}
		if rawOverrides := asMap(raw["api_id_overrides"]); rawOverrides != nil {
			for rawMid, rawAlias := range rawOverrides {
				midStr := strings.TrimSpace(rawMid)
				if !reModelID.MatchString(midStr) {
					continue
				}
				cleanAlias, err := c.validateAPIModelID(asString(rawAlias), false)
				if err != nil {
					return nil, fmt.Errorf("迁移文件中的模型 API 别名无效：%w", err)
				}
				prep.apiIDOverrides[midStr] = cleanAlias
			}
		}
		seenRepos := make(map[string]bool)
		for _, item := range asMapSlice(raw["common_repos"]) {
			repo := asTrimmedString(item["repo"])
			repoLow := strings.ToLower(repo)
			if !reRepoSlug.MatchString(repo) || seenRepos[repoLow] {
				continue
			}
			seenRepos[repoLow] = true
			defaultName := repo
			if idx := strings.LastIndex(repo, "/"); idx >= 0 {
				defaultName = repo[idx+1:]
			}
			name := asString(item["name"])
			if name == "" {
				name = defaultName
			}
			dl, _ := asInt(item["downloads"])
			likes, _ := asInt(item["likes"])
			addedAt, ok := asFloat(item["added_at"])
			if !ok || addedAt == 0 {
				addedAt = nowUnixFloat()
			}
			prep.commonRepos = append(prep.commonRepos, map[string]any{
				"repo":      repo,
				"name":      truncateRunes(name, 120),
				"note":      truncateRunes(asString(item["note"]), 200),
				"filename":  truncateRunes(asString(item["filename"]), 200),
				"downloads": dl,
				"likes":     likes,
				"added_at":  addedAt,
			})
		}
	}

	builtinIDs := c.builtinWorkflowIDSet()
	for _, item := range src.Workflows {
		if item == nil {
			continue
		}
		candidate := deepCopyMap(item)
		candidate["source"] = "custom"
		wf, err := c.validateWorkflowTemplate(candidate)
		if err != nil || wf == nil {
			return nil, fmt.Errorf("迁移文件中的自定义工作流无效：%v", err)
		}
		id := asString(wf["id"])
		if id == "" || builtinIDs[id] {
			continue
		}
		prep.customWorkflows[id] = wf
	}
	return prep, nil
}

func validateMigrationWorkflowDrafts(raw map[string]any) (map[string]*ColabOneWorkflowDraft, error) {
	out := make(map[string]*ColabOneWorkflowDraft)
	if len(raw) == 0 {
		return out, nil
	}
	rawDrafts := asMap(raw["drafts"])
	if rawDrafts == nil {
		return out, nil
	}
	for wfID, rawItem := range rawDrafts {
		if !reModelID.MatchString(wfID) {
			return nil, errors.New("迁移文件中的工作流草稿 ID 不合法")
		}
		itemMap := asMap(rawItem)
		if itemMap == nil {
			return nil, errors.New("迁移文件中的工作流草稿格式无效")
		}
		cleanedFields, err := validateWorkflowDraftFields(itemMap["fields"])
		if err != nil {
			return nil, fmt.Errorf("迁移文件中的工作流草稿无效：%w", err)
		}
		if len(cleanedFields) == 0 {
			continue
		}
		updatedAt, _ := asFloat(itemMap["updated_at"])
		if updatedAt < 0 {
			updatedAt = 0
		}
		out[wfID] = &ColabOneWorkflowDraft{
			WorkflowID: wfID,
			Fields:     cleanedFields,
			Media:      make(map[string]ColabOneWorkflowDraftMedia),
			UpdatedAt:  updatedAt,
		}
	}
	return out, nil
}

func formatMigrationImportLog(modules []string) string {
	labels := map[string]string{
		migration.ModuleAccount:          "Google 账号与云盘认证",
		migration.ModuleCredentials:      "第三方密钥与公网隧道",
		migration.ModuleSettings:         "软件与外观设置",
		migration.ModuleLibraryWorkflows: "模型库与自定义工作流",
		migration.ModuleCUUsage:          "CU 算力消耗历史",
		migration.ModuleWorkflowDrafts:   "ComfyUI 文本草稿",
	}
	parts := make([]string, 0, len(modules))
	for _, id := range modules {
		if label, ok := labels[id]; ok {
			parts = append(parts, label)
		}
	}
	return "已导入迁移配置（" + strings.Join(parts, "、") + "）"
}
