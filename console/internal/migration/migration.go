package migration

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Canonical migration module identifiers.
const (
	ModuleAccount          = "account"
	ModuleCredentials      = "credentials"
	ModuleSettings         = "settings"
	ModuleLibraryWorkflows = "library_workflows"
	ModuleCUUsage          = "cu_usage"
	ModuleWorkflowDrafts   = "workflow_drafts"
)

// AllModules lists every supported migration module in canonical UI display order.
var AllModules = []string{
	ModuleAccount,
	ModuleCredentials,
	ModuleSettings,
	ModuleLibraryWorkflows,
	ModuleCUUsage,
	ModuleWorkflowDrafts,
}

var validModuleSet = map[string]bool{
	ModuleAccount:          true,
	ModuleCredentials:      true,
	ModuleSettings:         true,
	ModuleLibraryWorkflows: true,
	ModuleCUUsage:          true,
	ModuleWorkflowDrafts:   true,
}

// ErrNoModulesSelected is returned when an export or import request selects zero valid modules.
var ErrNoModulesSelected = errors.New("请至少选择一个要迁移的模块")

// ColabOneMigrationAccountData holds portable Google OAuth credentials, profile,
// and headless Drive quota project metadata.
type ColabOneMigrationAccountData struct {
	Colab         map[string]any    `json:"colab,omitempty"`
	GoogleProfile map[string]string `json:"google_profile,omitempty"`
	CloudProject  string            `json:"cloud_project,omitempty"`
}

// String redacts OAuth secrets so formatting never leaks tokens.
func (a ColabOneMigrationAccountData) String() string {
	return fmt.Sprintf(
		"ColabOneMigrationAccountData{HasColab:%t, HasProfile:%t, HasCloudProject:%t}",
		len(a.Colab) > 0,
		len(a.GoogleProfile) > 0,
		strings.TrimSpace(a.CloudProject) != "",
	)
}

// GoString redacts OAuth secrets when formatted with %#v.
func (a ColabOneMigrationAccountData) GoString() string { return a.String() }

// ColabOneMigrationCredentialsData holds third-party tokens and public tunnel settings.
type ColabOneMigrationCredentialsData struct {
	HFToken       string         `json:"hf_token,omitempty"`
	AssignProxy   string         `json:"assign_proxy,omitempty"`
	PublicAPI     map[string]any `json:"public_api,omitempty"`
	ComfyUICanvas map[string]any `json:"comfyui_canvas,omitempty"`
}

// String redacts third-party secrets so formatting never leaks tokens or proxy credentials.
func (c ColabOneMigrationCredentialsData) String() string {
	return fmt.Sprintf(
		"ColabOneMigrationCredentialsData{HasHF:%t, HasAssignProxy:%t, HasPublicAPI:%t, HasComfyCanvas:%t}",
		strings.TrimSpace(c.HFToken) != "",
		strings.TrimSpace(c.AssignProxy) != "",
		len(c.PublicAPI) > 0,
		len(c.ComfyUICanvas) > 0,
	)
}

// GoString redacts third-party secrets when formatted with %#v.
func (c ColabOneMigrationCredentialsData) GoString() string { return c.String() }

// ColabOneMigrationUpdaterPrefs captures portable self-update preferences.
type ColabOneMigrationUpdaterPrefs struct {
	AutoCheck         bool   `json:"auto_check"`
	IncludePrerelease bool   `json:"include_prerelease"`
	SkippedVersion    string `json:"skipped_version,omitempty"`
}

// ColabOneMigrationUIPrefs captures WebView localStorage preferences synced across machines.
type ColabOneMigrationUIPrefs struct {
	Appearance         string `json:"appearance,omitempty"`
	Locale             string `json:"locale,omitempty"`
	PrivacyMaskDefault *bool  `json:"privacy_mask_default,omitempty"`
	MonitorLayout      string `json:"monitor_layout,omitempty"`
	CUUsageMetric      string `json:"cu_usage_metric,omitempty"`
	LogsWidth          *int   `json:"logs_width,omitempty"`
}

// ColabOneMigrationSettingsData holds controller, native desktop, and WebView preferences.
type ColabOneMigrationSettingsData struct {
	Controller    map[string]any                 `json:"controller,omitempty"`
	Appearance    string                         `json:"appearance,omitempty"`
	CloseBehavior string                         `json:"close_behavior,omitempty"`
	Updater       *ColabOneMigrationUpdaterPrefs `json:"updater,omitempty"`
	UIPrefs       *ColabOneMigrationUIPrefs      `json:"ui_prefs,omitempty"`
}

// ColabOneMigrationLibraryWorkflowsData holds local model library and custom ComfyUI workflows.
type ColabOneMigrationLibraryWorkflowsData struct {
	Library   map[string]any   `json:"library,omitempty"`
	Workflows []map[string]any `json:"workflows,omitempty"`
}

// ColabOneMigrationPayload is the plaintext JSON object encrypted inside .colabone files.
type ColabOneMigrationPayload struct {
	SchemaVersion    int                                    `json:"schema_version"`
	AppVersion       string                                 `json:"app_version"`
	ExportedAt       string                                 `json:"exported_at"`
	SourceOS         string                                 `json:"source_os"`
	IncludedModules  []string                               `json:"included_modules"`
	Account          *ColabOneMigrationAccountData          `json:"account,omitempty"`
	Credentials      *ColabOneMigrationCredentialsData      `json:"credentials,omitempty"`
	Settings         *ColabOneMigrationSettingsData         `json:"settings,omitempty"`
	LibraryWorkflows *ColabOneMigrationLibraryWorkflowsData `json:"library_workflows,omitempty"`
	CUUsage          map[string]any                         `json:"cu_usage,omitempty"`
	WorkflowDrafts   map[string]any                         `json:"workflow_drafts,omitempty"`
}

// String redacts secret sub-structures so logging never leaks credentials.
func (p ColabOneMigrationPayload) String() string {
	return fmt.Sprintf(
		"ColabOneMigrationPayload{SchemaVersion:%d, AppVersion:%q, ExportedAt:%q, SourceOS:%q, IncludedModules:%v}",
		p.SchemaVersion,
		p.AppVersion,
		p.ExportedAt,
		p.SourceOS,
		p.IncludedModules,
	)
}

// GoString redacts secret sub-structures when formatted with %#v.
func (p ColabOneMigrationPayload) GoString() string { return p.String() }

// HasModule reports whether moduleID is listed in IncludedModules.
func (p *ColabOneMigrationPayload) HasModule(moduleID string) bool {
	if p == nil {
		return false
	}
	for _, m := range p.IncludedModules {
		if m == moduleID {
			return true
		}
	}
	return false
}

// NormalizeModules validates, deduplicates, and sorts module IDs into canonical order.
func NormalizeModules(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, ErrNoModulesSelected
	}
	seen := make(map[string]bool, len(raw))
	for _, item := range raw {
		id := strings.TrimSpace(item)
		if !validModuleSet[id] {
			return nil, fmt.Errorf("不支持的迁移模块：%s", id)
		}
		seen[id] = true
	}
	out := make([]string, 0, len(seen))
	for _, id := range AllModules {
		if seen[id] {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return nil, ErrNoModulesSelected
	}
	return out, nil
}

// ValidatePayload checks structural invariants of an unsealed migration payload.
func ValidatePayload(p *ColabOneMigrationPayload) error {
	if p == nil {
		return ErrCorruptPayload
	}
	if p.SchemaVersion < 1 {
		return ErrCorruptPayload
	}
	if p.SchemaVersion > COLABONE_MIGRATION_SCHEMA_VERSION {
		return ErrUnsupportedVersion
	}
	norm, err := NormalizeModules(p.IncludedModules)
	if err != nil {
		return ErrCorruptPayload
	}
	p.IncludedModules = norm
	return nil
}

// ---------------------------------------------------------------------------
// Redacted summary types (safe to send across Go <-> WebView IPC)
// ---------------------------------------------------------------------------

// ColabOneMigrationAccountSummary summarizes Google account status without any tokens.
type ColabOneMigrationAccountSummary struct {
	HasLogin        bool   `json:"has_login"`
	PersistentLogin bool   `json:"persistent_login"`
	Email           string `json:"email"`
	HasDriveScope   bool   `json:"has_drive_scope"`
	HasCloudProject bool   `json:"has_cloud_project"`
}

// ColabOneMigrationCredentialsSummary summarizes configured secrets without raw keys/tokens.
type ColabOneMigrationCredentialsSummary struct {
	HasHFToken        bool   `json:"has_hf_token"`
	HasAssignProxy    bool   `json:"has_assign_proxy"`
	HasPublicAPI      bool   `json:"has_public_api"`
	PublicAPIDomain   string `json:"public_api_domain"`
	HasComfyCanvas    bool   `json:"has_comfy_canvas"`
	ComfyCanvasDomain string `json:"comfy_canvas_domain"`
	ConfiguredCount   int    `json:"configured_count"`
}

// ColabOneMigrationSettingsSummary summarizes key software and desktop preferences.
type ColabOneMigrationSettingsSummary struct {
	GPU                string `json:"gpu"`
	IdleMinutes        int    `json:"idle_minutes"`
	DownloadThreads    int    `json:"download_threads"`
	AutoDrive          bool   `json:"auto_drive"`
	AssignProxyEnabled bool   `json:"assign_proxy_enabled"`
	Locale             string `json:"locale"`
	Appearance         string `json:"appearance"`
	CloseBehavior      string `json:"close_behavior"`
	PrivacyMaskDefault bool   `json:"privacy_mask_default"`
}

// ColabOneMigrationLibrarySummary summarizes local model library and custom workflow counts.
type ColabOneMigrationLibrarySummary struct {
	CommonModelCount    int `json:"common_model_count"`
	CustomModelCount    int `json:"custom_model_count"`
	CommonRepoCount     int `json:"common_repo_count"`
	APIIDOverrideCount  int `json:"api_id_override_count"`
	CustomWorkflowCount int `json:"custom_workflow_count"`
}

// ColabOneMigrationCUUsageSummary summarizes persisted Compute Unit consumption records.
type ColabOneMigrationCUUsageSummary struct {
	AccountCount int `json:"account_count"`
	BucketCount  int `json:"bucket_count"`
	DayCount     int `json:"day_count"`
}

// ColabOneMigrationDraftsSummary summarizes ComfyUI text-only workflow drafts.
type ColabOneMigrationDraftsSummary struct {
	DraftCount int `json:"draft_count"`
	FieldCount int `json:"field_count"`
}

// ColabOneMigrationModuleSummary describes one module row in Export / Import UI modals.
type ColabOneMigrationModuleSummary struct {
	ID               string                               `json:"id"`
	Included         bool                                 `json:"included"`
	HasData          bool                                 `json:"has_data"`
	Account          *ColabOneMigrationAccountSummary     `json:"account,omitempty"`
	Credentials      *ColabOneMigrationCredentialsSummary `json:"credentials,omitempty"`
	Settings         *ColabOneMigrationSettingsSummary    `json:"settings,omitempty"`
	LibraryWorkflows *ColabOneMigrationLibrarySummary     `json:"library_workflows,omitempty"`
	CUUsage          *ColabOneMigrationCUUsageSummary     `json:"cu_usage,omitempty"`
	WorkflowDrafts   *ColabOneMigrationDraftsSummary      `json:"workflow_drafts,omitempty"`
}

// BuildModuleSummaries builds strictly redacted UI summaries for all 6 canonical modules.
func BuildModuleSummaries(p *ColabOneMigrationPayload) []ColabOneMigrationModuleSummary {
	out := make([]ColabOneMigrationModuleSummary, 0, len(AllModules))
	for _, id := range AllModules {
		included := p != nil && p.HasModule(id)
		item := ColabOneMigrationModuleSummary{
			ID:       id,
			Included: included,
		}
		if !included {
			out = append(out, item)
			continue
		}
		switch id {
		case ModuleAccount:
			s := summarizeAccount(p.Account)
			item.Account = s
			item.HasData = s.HasLogin || s.Email != "" || s.HasCloudProject
		case ModuleCredentials:
			s := summarizeCredentials(p.Credentials)
			item.Credentials = s
			item.HasData = s.ConfiguredCount > 0
		case ModuleSettings:
			s := summarizeSettings(p.Settings)
			item.Settings = s
			item.HasData = true
		case ModuleLibraryWorkflows:
			s := summarizeLibraryWorkflows(p.LibraryWorkflows)
			item.LibraryWorkflows = s
			item.HasData = s.CommonModelCount > 0 || s.CustomModelCount > 0 || s.CommonRepoCount > 0 || s.APIIDOverrideCount > 0 || s.CustomWorkflowCount > 0
		case ModuleCUUsage:
			s := summarizeCUUsage(p.CUUsage)
			item.CUUsage = s
			item.HasData = s.BucketCount > 0
		case ModuleWorkflowDrafts:
			s := summarizeWorkflowDrafts(p.WorkflowDrafts)
			item.WorkflowDrafts = s
			item.HasData = s.DraftCount > 0
		}
		out = append(out, item)
	}
	return out
}

func summarizeAccount(a *ColabOneMigrationAccountData) *ColabOneMigrationAccountSummary {
	s := &ColabOneMigrationAccountSummary{}
	if a == nil {
		return s
	}
	if a.Colab != nil {
		tok := strings.TrimSpace(asString(a.Colab["token"]))
		ref := strings.TrimSpace(asString(a.Colab["refresh_token"]))
		cid := strings.TrimSpace(asString(a.Colab["client_id"]))
		s.HasLogin = tok != "" || ref != "" || cid != ""
		s.PersistentLogin = ref != ""
		if s.PersistentLogin {
			for _, sc := range extractScopes(a.Colab["scopes"]) {
				if strings.Contains(strings.ToLower(sc), "drive") {
					s.HasDriveScope = true
					break
				}
			}
		}
	}
	if a.GoogleProfile != nil {
		s.Email = strings.TrimSpace(a.GoogleProfile["email"])
	}
	s.HasCloudProject = strings.TrimSpace(a.CloudProject) != ""
	return s
}

func summarizeCredentials(c *ColabOneMigrationCredentialsData) *ColabOneMigrationCredentialsSummary {
	s := &ColabOneMigrationCredentialsSummary{}
	if c == nil {
		return s
	}
	if strings.TrimSpace(c.HFToken) != "" {
		s.HasHFToken = true
		s.ConfiguredCount++
	}
	if strings.TrimSpace(c.AssignProxy) != "" {
		s.HasAssignProxy = true
		s.ConfiguredCount++
	}
	if c.PublicAPI != nil {
		domain := strings.TrimSpace(asString(c.PublicAPI["domain"]))
		hasToken := strings.TrimSpace(asString(c.PublicAPI["tunnel_token"])) != ""
		hasKey := strings.TrimSpace(asString(c.PublicAPI["api_key"])) != ""
		if domain != "" || hasToken || hasKey {
			s.HasPublicAPI = true
			s.PublicAPIDomain = domain
			s.ConfiguredCount++
		}
	}
	if c.ComfyUICanvas != nil {
		domain := strings.TrimSpace(asString(c.ComfyUICanvas["domain"]))
		hasToken := strings.TrimSpace(asString(c.ComfyUICanvas["tunnel_token"])) != ""
		if domain != "" || hasToken {
			s.HasComfyCanvas = true
			s.ComfyCanvasDomain = domain
			s.ConfiguredCount++
		}
	}
	return s
}

func summarizeSettings(st *ColabOneMigrationSettingsData) *ColabOneMigrationSettingsSummary {
	s := &ColabOneMigrationSettingsSummary{}
	if st == nil {
		return s
	}
	if st.Controller != nil {
		s.GPU = strings.TrimSpace(asString(st.Controller["gpu"]))
		s.IdleMinutes = asInt(st.Controller["idle_minutes"])
		s.DownloadThreads = asInt(st.Controller["download_threads"])
		s.AutoDrive = asBool(st.Controller["auto_drive"])
		s.AssignProxyEnabled = asBool(st.Controller["assign_proxy_enabled"])
		s.Locale = strings.TrimSpace(asString(st.Controller["locale"]))
	}
	s.Appearance = strings.TrimSpace(st.Appearance)
	s.CloseBehavior = strings.TrimSpace(st.CloseBehavior)
	if st.UIPrefs != nil {
		if s.Appearance == "" {
			s.Appearance = strings.TrimSpace(st.UIPrefs.Appearance)
		}
		if s.Locale == "" {
			s.Locale = strings.TrimSpace(st.UIPrefs.Locale)
		}
		if st.UIPrefs.PrivacyMaskDefault != nil {
			s.PrivacyMaskDefault = *st.UIPrefs.PrivacyMaskDefault
		}
	}
	return s
}

func summarizeLibraryWorkflows(lw *ColabOneMigrationLibraryWorkflowsData) *ColabOneMigrationLibrarySummary {
	s := &ColabOneMigrationLibrarySummary{}
	if lw == nil {
		return s
	}
	if lw.Library != nil {
		s.CommonModelCount = sliceLen(lw.Library["common_ids"])
		s.CustomModelCount = sliceLen(lw.Library["custom_models"])
		s.CommonRepoCount = sliceLen(lw.Library["common_repos"])
		if m, ok := lw.Library["api_id_overrides"].(map[string]any); ok {
			s.APIIDOverrideCount = len(m)
		} else if ms, ok := lw.Library["api_id_overrides"].(map[string]string); ok {
			s.APIIDOverrideCount = len(ms)
		}
	}
	s.CustomWorkflowCount = len(lw.Workflows)
	return s
}

func summarizeCUUsage(raw map[string]any) *ColabOneMigrationCUUsageSummary {
	s := &ColabOneMigrationCUUsageSummary{}
	if raw == nil {
		return s
	}
	accounts, ok := raw["accounts"].(map[string]any)
	if !ok || len(accounts) == 0 {
		return s
	}
	daySet := make(map[string]bool)
	for _, acctVal := range accounts {
		acctMap, ok := acctVal.(map[string]any)
		if !ok || acctMap == nil {
			continue
		}
		buckets := anySlice(acctMap["buckets"])
		if len(buckets) == 0 {
			continue
		}
		s.AccountCount++
		for _, bVal := range buckets {
			bm, ok := bVal.(map[string]any)
			if !ok || bm == nil {
				continue
			}
			ts := asInt64(bm["ts"])
			if ts <= 0 {
				continue
			}
			s.BucketCount++
			daySet[time.Unix(ts, 0).UTC().Format("2006-01-02")] = true
		}
	}
	s.DayCount = len(daySet)
	return s
}

func summarizeWorkflowDrafts(raw map[string]any) *ColabOneMigrationDraftsSummary {
	s := &ColabOneMigrationDraftsSummary{}
	if raw == nil {
		return s
	}
	drafts, ok := raw["drafts"].(map[string]any)
	if !ok || len(drafts) == 0 {
		return s
	}
	for _, dv := range drafts {
		dm, ok := dv.(map[string]any)
		if !ok || dm == nil {
			continue
		}
		fields, _ := dm["fields"].(map[string]any)
		if len(fields) == 0 {
			continue
		}
		s.DraftCount++
		s.FieldCount += len(fields)
	}
	return s
}

func extractScopes(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		return strings.Fields(v)
	default:
		return nil
	}
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func asInt(v any) int {
	return int(asInt64(v))
}

func asInt64(v any) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int64:
		return x
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0
		}
		return int64(x)
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n
		}
		if f, err := x.Float64(); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return int64(f)
		}
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64); err == nil {
			return n
		}
	}
	return 0
}

func anySlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

func sliceLen(v any) int {
	switch s := v.(type) {
	case []any:
		return len(s)
	case []string:
		return len(s)
	case []map[string]any:
		return len(s)
	default:
		return 0
	}
}

// SortedCopy returns a sorted copy of a string slice without mutating the input.
func SortedCopy(items []string) []string {
	cp := append([]string(nil), items...)
	sort.Strings(cp)
	return cp
}
