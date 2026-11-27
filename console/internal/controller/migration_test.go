package controller

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luolongfei/ColabOne/console/internal/assignproxy"
	"github.com/luolongfei/ColabOne/console/internal/migration"
)

type testMigrationAssignProxy struct{}

func (testMigrationAssignProxy) ParseAssignProxy(raw string) (*ColabOneAssignProxySpec, error) {
	spec, err := assignproxy.ParseAssignProxy(raw)
	if err != nil {
		return nil, err
	}
	return &ColabOneAssignProxySpec{
		Scheme:        spec.Scheme,
		Host:          spec.Host,
		Port:          spec.Port,
		Username:      spec.Username,
		Password:      spec.Password,
		NormalizedURL: spec.NormalizedURL,
		MaskedURL:     spec.MaskedURL,
		LogLabel:      spec.LogLabel,
		HasAuth:       spec.HasAuth(),
	}, nil
}

func (testMigrationAssignProxy) NormalizeAssignProxy(raw string) (string, error) {
	return assignproxy.NormalizeAssignProxy(raw)
}

func (testMigrationAssignProxy) ProxyLogLabel(raw string) string {
	return assignproxy.ProxyLogLabel(raw)
}

func (testMigrationAssignProxy) TestAssignProxy(_ context.Context, _ string) (map[string]any, error) {
	return map[string]any{"ok": true}, nil
}

func TestControllerMigrationFullRoundTripAndPersistence(t *testing.T) {
	srcDir := t.TempDir()
	srcVault := NewColabOneMemoryVault()
	src := NewColabOneController(ColabOneControllerConfig{
		ConfigDir:   srcDir,
		Vault:       srcVault,
		AssignProxy: testMigrationAssignProxy{},
	})
	defer src.Close()

	// Populate account & Drive credentials.
	_ = srcVault.Set("colab", map[string]any{
		"token":         "ya29.src-token",
		"refresh_token": "1//0g-src-refresh",
		"client_id":     "764086051850-src.apps.googleusercontent.com",
		"scopes":        []string{"openid", "https://www.googleapis.com/auth/drive.file"},
	})
	_ = srcVault.Set("google_profile", map[string]string{
		"email": "migrated@example.com",
		"sub":   "sub-migrated-1",
	})
	_ = srcVault.Set("cloud_project", "colabone-drive-12345")

	// Populate third-party secrets & tunnel configs.
	_ = srcVault.Set("hf", "hf_migratedSecretToken999")
	_ = srcVault.Set("assign_proxy", "http://u:p@proxy.example.com:8080")
	_ = srcVault.Set("public_api", map[string]any{
		"mode":         "named",
		"domain":       "api.migrated.dev",
		"tunnel_token": "cf-tunnel-api-token",
		"api_key":      "sk-migrated-api-key",
	})
	_ = srcVault.Set("comfyui_canvas", map[string]any{
		"mode":         "named",
		"domain":       "comfy.migrated.dev",
		"tunnel_token": "cf-tunnel-comfy-token",
	})

	// Populate controller settings.
	if _, err := src.SaveSettings(context.Background(), map[string]any{
		"gpu":                  "A100",
		"idle_minutes":         60,
		"download_threads":     32,
		"auto_drive":           false,
		"assign_max_attempts":  42,
		"assign_proxy_enabled": true,
	}); err != nil {
		t.Fatalf("SaveSettings failed: %v", err)
	}
	if _, err := src.Command(context.Background(), "locale.set", map[string]any{
		"locale":  "ja-JP",
		"persist": true,
	}); err != nil {
		t.Fatalf("locale.set failed: %v", err)
	}

	// Populate library + custom workflow.
	src.mu.Lock()
	src.commonIDs["custom-model-1"] = true
	src.customModels["hf-custom1"] = map[string]any{
		"id":    "hf-custom1",
		"name":  "Custom Migrated Model",
		"files": []map[string]any{{"repo": "org/model", "filename": "model.gguf"}},
	}
	src.apiIDOverrides["hf-custom1"] = "migrated/model"
	src.commonRepos = []map[string]any{{"repo": "org/favorite-repo", "name": "Favorite Repo"}}
	src.customWorkflows["wf-migrated-1"] = map[string]any{
		"id":     "wf-migrated-1",
		"name":   "Migrated Workflow",
		"source": "custom",
	}
	src.mu.Unlock()
	if err := src.saveLibrary(); err != nil {
		t.Fatalf("saveLibrary: %v", err)
	}
	if err := src.saveWorkflows(); err != nil {
		t.Fatalf("saveWorkflows: %v", err)
	}

	// Populate CU usage history with recent timestamps within the 365-day retention window.
	recentTS := float64(int64(nowUnixFloat()) - 3600)
	src.cuUsage.RecordSample("migrated@example.com", recentTS, 100.0, true, 2.0, 1)
	src.cuUsage.RecordSample("migrated@example.com", recentTS+600, 99.5, true, 2.0, 1)

	// Populate ComfyUI workflow draft with BOTH text field and media field (media must be stripped!).
	if _, err := src.saveWorkflowDraft(map[string]any{
		"workflow_id": "wf-migrated-1",
		"fields": map[string]any{
			"prompt": "cyberpunk skyline",
			"steps":  28,
		},
	}); err != nil {
		t.Fatalf("saveWorkflowDraft: %v", err)
	}
	pngStub := base64.StdEncoding.EncodeToString([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 1})
	if _, err := src.saveWorkflowDraftMedia(map[string]any{
		"workflow_id": "wf-migrated-1",
		"field_key":   "ref_image",
		"kind":        "image",
		"filename":    "secret-photo.png",
		"data":        pngStub,
	}); err != nil {
		t.Fatalf("saveWorkflowDraftMedia: %v", err)
	}

	// Export all 6 modules, seal with fast KDF, then open.
	maskTrue := true
	payload, err := src.ExportMigrationPayload(migration.AllModules, "v0.9.9", "windows", &migration.ColabOneMigrationSettingsData{
		Appearance:    "dark",
		CloseBehavior: "background",
		UIPrefs: &migration.ColabOneMigrationUIPrefs{
			Appearance:         "dark",
			Locale:             "ja-JP",
			PrivacyMaskDefault: &maskTrue,
		},
	})
	if err != nil {
		t.Fatalf("ExportMigrationPayload failed: %v", err)
	}
	sealed, err := migration.SealPayloadWithParams(payload, "migrate-pass", migration.ColabOneMigrationKDFParams{
		Time: 1, MemoryKiB: 8 * 1024, Threads: 1,
	})
	if err != nil {
		t.Fatalf("SealPayloadWithParams failed: %v", err)
	}
	opened, err := migration.OpenPayload(sealed, "migrate-pass")
	if err != nil {
		t.Fatalf("OpenPayload failed: %v", err)
	}

	// Verify media field was stripped from exported workflow drafts.
	draftMap := asMap(asMap(opened.WorkflowDrafts["drafts"])["wf-migrated-1"])
	draftFields := asMap(draftMap["fields"])
	if asString(draftFields["prompt"]) != "cyberpunk skyline" {
		t.Fatalf("expected prompt preserved in exported draft, got %v", draftFields)
	}
	if _, hasMediaField := draftFields["ref_image"]; hasMediaField {
		t.Fatalf("media-backed field ref_image should be stripped on export, got %v", draftFields)
	}

	// Import into a brand-new target controller.
	dstDir := t.TempDir()
	dstVault := NewColabOneMemoryVault()
	dst := NewColabOneController(ColabOneControllerConfig{
		ConfigDir:   dstDir,
		Vault:       dstVault,
		AssignProxy: testMigrationAssignProxy{},
	})
	defer dst.Close()

	res, err := dst.ApplyMigrationPayload(context.Background(), opened, migration.AllModules)
	if err != nil {
		t.Fatalf("ApplyMigrationPayload failed: %v", err)
	}
	if len(res.AppliedModules) != len(migration.AllModules) || !res.AccountImported || !res.AccountLoggedIn || res.AccountEmail != "migrated@example.com" {
		t.Fatalf("unexpected migration result: %+v", res)
	}

	// Verify vault entries on target.
	if got, ok := dstVault.Get("hf"); !ok || asString(got) != "hf_migratedSecretToken999" {
		t.Fatalf("hf token not migrated: %v, %v", got, ok)
	}
	if got, ok := dstVault.Get("cloud_project"); !ok || asString(got) != "colabone-drive-12345" {
		t.Fatalf("cloud_project not migrated: %v, %v", got, ok)
	}
	if got, ok := dstVault.Get("colab"); !ok || asString(asMap(got)["refresh_token"]) != "1//0g-src-refresh" {
		t.Fatalf("colab credentials not migrated: %v, %v", got, ok)
	}

	// Verify immediate hot-applied snapshot on target.
	snap := dst.Snapshot()
	snapSettings := asMap(snap["settings"])
	if asString(snapSettings["gpu"]) != "A100" || asString(snapSettings["locale"]) != "ja-JP" {
		t.Fatalf("settings not hot-applied: %+v", snapSettings)
	}
	if !asBool(asMap(snap["setup"])["colab"]) || !asBool(asMap(snap["setup"])["persistent_login"]) {
		t.Fatalf("setup flags not updated: %+v", snap["setup"])
	}

	// Verify activity log does not leak secrets.
	for _, entry := range dst.activity.Snapshot() {
		for _, secret := range []string{"ya29.src", "1//0g-src", "hf_migrated", "sk-migrated", "cf-tunnel"} {
			if strings.Contains(entry.Message, secret) {
				t.Fatalf("activity log leaked secret %q: %s", secret, entry.Message)
			}
		}
	}

	// Verify persistence by constructing a fresh controller reading from dstDir + dstVault.
	reloaded := NewColabOneController(ColabOneControllerConfig{
		ConfigDir: dstDir,
		Vault:     dstVault,
	})
	defer reloaded.Close()

	reloadedSnap := reloaded.Snapshot()
	if asString(asMap(reloadedSnap["settings"])["gpu"]) != "A100" {
		t.Fatalf("reloaded GPU setting mismatch: %+v", reloadedSnap["settings"])
	}
	if _, ok := reloaded.customWorkflows["wf-migrated-1"]; !ok {
		t.Fatalf("reloaded custom workflow missing")
	}
	if d := reloaded.workflowDrafts["wf-migrated-1"]; d == nil || asString(d.Fields["prompt"]) != "cyberpunk skyline" || len(d.Media) != 0 {
		t.Fatalf("reloaded workflow draft mismatch: %+v", d)
	}
	cuQuery := reloaded.cuUsage.Query(ColabOneCUUsageQuery{
		AccountEmail: "migrated@example.com",
		LoggedIn:     true,
		Start:        int64(recentTS) - 7200,
		End:          int64(recentTS) + 7200,
	})
	if len(asMapSlice(cuQuery["buckets"])) == 0 {
		t.Fatalf("reloaded CU usage buckets empty: %+v", cuQuery)
	}
}

func TestControllerMigrationPartialOverwriteAndActiveInstanceGuard(t *testing.T) {
	dstDir := t.TempDir()
	dstVault := NewColabOneMemoryVault()
	_ = dstVault.Set("colab", map[string]any{"token": "ya29.keep-me", "refresh_token": "1//keep-me"})
	_ = dstVault.Set("google_profile", map[string]string{"email": "keep@example.com", "sub": "sub-keep"})
	_ = dstVault.Set("hf", "hf_keep_existing_token")
	_ = dstVault.Set("runtime_session_ids", []string{"ep-running-1"})
	_ = dstVault.Set("runtime_session:ep-running-1", map[string]any{"endpoint": "ep-running-1", "token": "rt", "url": "https://rt"})

	dst := NewColabOneController(ColabOneControllerConfig{
		ConfigDir: dstDir,
		Vault:     dstVault,
	})
	defer dst.Close()

	payload := &migration.ColabOneMigrationPayload{
		SchemaVersion:   migration.COLABONE_MIGRATION_SCHEMA_VERSION,
		IncludedModules: []string{migration.ModuleAccount, migration.ModuleSettings},
		Account: &migration.ColabOneMigrationAccountData{
			Colab: map[string]any{"token": "ya29.new", "refresh_token": "1//new"},
		},
		Settings: &migration.ColabOneMigrationSettingsData{
			Controller: map[string]any{
				"gpu":              "H100",
				"idle_minutes":     90,
				"download_threads": 32,
				"auto_drive":       true,
			},
		},
	}

	// 1. Active GPU instance blocks importing account module.
	if _, err := dst.ApplyMigrationPayload(context.Background(), payload, []string{migration.ModuleAccount, migration.ModuleSettings}); err == nil || !strings.Contains(err.Error(), "释放当前实例") {
		t.Fatalf("expected active-session guard error, got %v", err)
	}

	// 2. Unchecking account allows importing settings safely without touching account or credentials!
	res, err := dst.ApplyMigrationPayload(context.Background(), payload, []string{migration.ModuleSettings})
	if err != nil {
		t.Fatalf("partial non-account import failed: %v", err)
	}
	if len(res.AppliedModules) != 1 || res.AppliedModules[0] != migration.ModuleSettings {
		t.Fatalf("unexpected applied modules: %v", res.AppliedModules)
	}
	if got, _ := dstVault.Get("colab"); asString(asMap(got)["refresh_token"]) != "1//keep-me" {
		t.Fatalf("unselected account module was modified: %v", got)
	}
	if got, _ := dstVault.Get("hf"); asString(got) != "hf_keep_existing_token" {
		t.Fatalf("unincluded credentials module was modified: %v", got)
	}
	if got := asString(asMap(dst.Snapshot()["settings"])["gpu"]); got != "H100" {
		t.Fatalf("settings gpu = %q, want H100", got)
	}
}

func TestControllerMigrationValidationFailureDoesNotMutateTarget(t *testing.T) {
	dstDir := t.TempDir()
	dstVault := NewColabOneMemoryVault()
	_ = dstVault.Set("hf", "hf_original_safe")

	dst := NewColabOneController(ColabOneControllerConfig{
		ConfigDir: dstDir,
		Vault:     dstVault,
	})
	defer dst.Close()

	// Invalid custom model (empty files) combined with valid credentials -> entire import must abort atomically.
	badPayload := &migration.ColabOneMigrationPayload{
		SchemaVersion:   migration.COLABONE_MIGRATION_SCHEMA_VERSION,
		IncludedModules: []string{migration.ModuleCredentials, migration.ModuleLibraryWorkflows},
		Credentials: &migration.ColabOneMigrationCredentialsData{
			HFToken: "hf_should_not_be_written",
		},
		LibraryWorkflows: &migration.ColabOneMigrationLibraryWorkflowsData{
			Library: map[string]any{
				"custom_models": []map[string]any{
					{"id": "bad model id with spaces!", "name": ""},
				},
			},
		},
	}

	if _, err := dst.ApplyMigrationPayload(context.Background(), badPayload, []string{migration.ModuleCredentials, migration.ModuleLibraryWorkflows}); err == nil {
		t.Fatal("expected validation failure for bad custom model")
	}

	// Existing HF token must remain untouched.
	if got, _ := dstVault.Get("hf"); asString(got) != "hf_original_safe" {
		t.Fatalf("vault was mutated despite validation failure: got %q", got)
	}
	if _, err := os.Stat(filepath.Join(dstDir, "library.json")); err == nil {
		t.Fatalf("library.json should not have been written on validation failure")
	}
}

func TestControllerMigrationRollbackRestoresWorkflowsAndDraftMedia(t *testing.T) {
	t.Run("workflows write failure restores customWorkflows and remoteWorkflowVersions", func(t *testing.T) {
		dir := t.TempDir()
		vault := NewColabOneMemoryVault()
		c := NewColabOneController(ColabOneControllerConfig{
			ConfigDir: dir,
			Vault:     vault,
		})
		defer c.Close()

		origWF := map[string]any{
			"id":          "custom-orig",
			"name":        "Original WF",
			"category":    "image",
			"source":      "custom",
			"api_prompt":  map[string]any{"1": map[string]any{"class_type": "SaveImage", "inputs": map[string]any{}}},
			"input_specs": []any{},
		}
		c.mu.Lock()
		c.customWorkflows["custom-orig"] = deepCopyMap(origWF)
		c.remoteWorkflowVersions["custom-orig"] = 42.5
		c.mu.Unlock()

		// Make workflows.json a non-empty directory so AtomicJSON("workflows.json") fails deterministically.
		wfPath := filepath.Join(dir, "workflows.json")
		if err := os.MkdirAll(filepath.Join(wfPath, "blocker"), 0o700); err != nil {
			t.Fatal(err)
		}

		payload := &migration.ColabOneMigrationPayload{
			SchemaVersion:   migration.COLABONE_MIGRATION_SCHEMA_VERSION,
			IncludedModules: []string{migration.ModuleLibraryWorkflows},
			LibraryWorkflows: &migration.ColabOneMigrationLibraryWorkflowsData{
				Library: map[string]any{
					"default_common_version": COLABONE_DEFAULT_COMMON_VERSION,
					"common_ids":             []string{},
				},
				Workflows: []map[string]any{
					{
						"id":          "custom-new",
						"name":        "New WF",
						"category":    "image",
						"source":      "custom",
						"api_prompt":  map[string]any{"1": map[string]any{"class_type": "SaveImage", "inputs": map[string]any{}}},
						"input_specs": []any{},
					},
				},
			},
		}

		if _, err := c.ApplyMigrationPayload(context.Background(), payload, []string{migration.ModuleLibraryWorkflows}); err == nil {
			t.Fatal("expected workflows.json write failure")
		}

		c.mu.Lock()
		_, hasOrig := c.customWorkflows["custom-orig"]
		_, hasNew := c.customWorkflows["custom-new"]
		ver := c.remoteWorkflowVersions["custom-orig"]
		c.mu.Unlock()
		if !hasOrig || hasNew || ver != 42.5 {
			t.Fatalf("rollback corrupted workflows state: hasOrig=%v hasNew=%v ver=%v", hasOrig, hasNew, ver)
		}
	})

	t.Run("cu_usage write failure rolls back drafts without deleting prior media files", func(t *testing.T) {
		dir := t.TempDir()
		vault := NewColabOneMemoryVault()
		c := NewColabOneController(ColabOneControllerConfig{
			ConfigDir: dir,
			Vault:     vault,
		})
		defer c.Close()

		pngStub := base64.StdEncoding.EncodeToString([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 1})
		if _, err := c.saveWorkflowDraftMedia(map[string]any{
			"workflow_id": "flux2-klein-t2i",
			"field_key":   "ref_image",
			"kind":        "image",
			"filename":    "keep-me.png",
			"data":        pngStub,
		}); err != nil {
			t.Fatalf("save media failed: %v", err)
		}
		c.mu.Lock()
		origMeta := c.workflowDrafts["flux2-klein-t2i"].Media["ref_image"]
		c.mu.Unlock()
		origMediaPath, _, _, err := c.resolveDraftMediaPath(origMeta)
		if err != nil {
			t.Fatalf("resolveDraftMediaPath failed: %v", err)
		}
		if _, err := os.Stat(origMediaPath); err != nil {
			t.Fatalf("expected original media file on disk: %v", err)
		}

		// Force cu_usage.json write to fail by placing a non-empty directory at its path.
		cuPath := filepath.Join(dir, colabOneCUUsageFilename)
		if err := os.MkdirAll(filepath.Join(cuPath, "blocker"), 0o700); err != nil {
			t.Fatal(err)
		}

		payload := &migration.ColabOneMigrationPayload{
			SchemaVersion:   migration.COLABONE_MIGRATION_SCHEMA_VERSION,
			IncludedModules: []string{migration.ModuleWorkflowDrafts, migration.ModuleCUUsage},
			WorkflowDrafts: map[string]any{
				"version": 1,
				"drafts": map[string]any{
					"flux2-klein-t2i": map[string]any{
						"workflow_id": "flux2-klein-t2i",
						"fields":      map[string]any{"prompt": "replacement"},
						"updated_at":  1000,
					},
				},
			},
			CUUsage: map[string]any{
				"version":        1,
				"bucket_seconds": 60,
				"accounts":       map[string]any{},
			},
		}

		if _, err := c.ApplyMigrationPayload(context.Background(), payload, []string{migration.ModuleWorkflowDrafts, migration.ModuleCUUsage}); err == nil {
			t.Fatal("expected cu_usage write failure")
		}

		// Original media file MUST still exist on disk and metadata MUST be rolled back.
		if _, err := os.Stat(origMediaPath); err != nil {
			t.Fatalf("draft media file was prematurely pruned despite cu_usage rollback: %v", err)
		}
		c.mu.Lock()
		gotMediaID := c.workflowDrafts["flux2-klein-t2i"].Media["ref_image"].ID
		c.mu.Unlock()
		if gotMediaID != origMeta.ID {
			t.Fatalf("draft metadata not rolled back: got %q, want %q", gotMediaID, origMeta.ID)
		}
	})
}

func TestControllerMigrationOrphanProfileAndAutoLocaleSync(t *testing.T) {
	dir := t.TempDir()
	vault := NewColabOneMemoryVault()
	// Seed orphan profile/cloud_project without colab credentials: export must not leak them.
	_ = vault.Set("google_profile", map[string]string{"email": "orphan@example.com", "sub": "sub-orphan"})
	_ = vault.Set("cloud_project", "colabone-drive-orphan1")

	c := NewColabOneController(ColabOneControllerConfig{
		ConfigDir: dir,
		Vault:     vault,
	})
	defer c.Close()

	exported, err := c.ExportMigrationPayload([]string{migration.ModuleAccount}, "v0.1.0", "linux", nil)
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
	if len(exported.Account.GoogleProfile) > 0 || exported.Account.CloudProject != "" {
		t.Fatalf("expected orphan profile/project to be omitted when colab is absent, got %+v", exported.Account)
	}

	// Import with empty colab + orphan profile/project AND empty controller locale with UIPrefs.Locale = "ja-JP".
	payload := &migration.ColabOneMigrationPayload{
		SchemaVersion:   migration.COLABONE_MIGRATION_SCHEMA_VERSION,
		IncludedModules: []string{migration.ModuleAccount, migration.ModuleSettings},
		Account: &migration.ColabOneMigrationAccountData{
			GoogleProfile: map[string]string{"email": "orphan@example.com"},
			CloudProject:  "colabone-drive-orphan1",
		},
		Settings: &migration.ColabOneMigrationSettingsData{
			Controller: map[string]any{"gpu": "G4", "locale": ""},
			UIPrefs:    &migration.ColabOneMigrationUIPrefs{Locale: "ja-JP"},
		},
	}

	res, err := c.ApplyMigrationPayload(context.Background(), payload, []string{migration.ModuleAccount, migration.ModuleSettings})
	if err != nil {
		t.Fatalf("ApplyMigrationPayload failed: %v", err)
	}
	if _, ok := vault.Get("google_profile"); ok {
		t.Fatal("orphan google_profile should not be written when colab credentials are absent")
	}
	if _, ok := vault.Get("cloud_project"); ok {
		t.Fatal("orphan cloud_project should not be written when colab credentials are absent")
	}
	if res.Locale != "ja-JP" {
		t.Fatalf("res.Locale = %q, want ja-JP", res.Locale)
	}
	if got := asString(asMap(c.Snapshot()["settings"])["locale"]); got != "ja-JP" {
		t.Fatalf("settings.locale = %q, want ja-JP", got)
	}
}

func TestSameColabCredentialIdentity(t *testing.T) {
	a := map[string]any{"token": "ya29.old", "refresh_token": "1//same"}
	bRefreshed := map[string]any{"token": "ya29.new", "refresh_token": "1//same"}
	cOther := map[string]any{"token": "ya29.other", "refresh_token": "1//other"}
	if !sameColabCredentialIdentity(a, bRefreshed) {
		t.Fatal("expected matching refresh_token to keep credential identity across token refresh")
	}
	if sameColabCredentialIdentity(a, cOther) {
		t.Fatal("expected different refresh_token to fail credential identity match")
	}
	if sameColabCredentialIdentity(a, nil) {
		t.Fatal("expected nil current creds (logout) to fail credential identity match")
	}
}

func TestControllerMigrationWritesWorkflowsJSONAsTopLevelArray(t *testing.T) {
	dir := t.TempDir()
	vault := NewColabOneMemoryVault()
	c := NewColabOneController(ColabOneControllerConfig{
		ConfigDir: dir,
		Vault:     vault,
	})
	defer c.Close()

	payload := &migration.ColabOneMigrationPayload{
		SchemaVersion:   migration.COLABONE_MIGRATION_SCHEMA_VERSION,
		IncludedModules: []string{migration.ModuleLibraryWorkflows},
		LibraryWorkflows: &migration.ColabOneMigrationLibraryWorkflowsData{
			Library: map[string]any{
				"default_common_version": COLABONE_DEFAULT_COMMON_VERSION,
				"common_ids":             []string{},
			},
			Workflows: []map[string]any{
				{
					"id":          "custom-array-wf",
					"name":        "Array Format WF",
					"category":    "image",
					"source":      "custom",
					"api_prompt":  map[string]any{"1": map[string]any{"class_type": "SaveImage", "inputs": map[string]any{}}},
					"input_specs": []any{},
				},
			},
		},
	}

	if _, err := c.ApplyMigrationPayload(context.Background(), payload, []string{migration.ModuleLibraryWorkflows}); err != nil {
		t.Fatalf("ApplyMigrationPayload failed: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "workflows.json"))
	if err != nil {
		t.Fatalf("read workflows.json: %v", err)
	}
	var arr []map[string]any
	if err := decodeJSONWithNumbers(raw, &arr); err != nil {
		t.Fatalf("workflows.json must be a top-level JSON array matching saveWorkflows(): err=%v raw=%s", err, string(raw))
	}
	if len(arr) != 1 || asString(arr[0]["id"]) != "custom-array-wf" {
		t.Fatalf("unexpected workflows.json contents: %#v", arr)
	}
}

func TestControllerMigrationLiveExternalLLMRoundTripRollbackAndAccountSecurity(t *testing.T) {
	// 1. LiveExternalLLM export, import, snapshot refresh, and opGen bump.
	srcDir := t.TempDir()
	srcVault := NewColabOneMemoryVault()
	_ = srcVault.Set(colabOneVaultLiveExternalLLMKey, map[string]any{
		"base_url": "https://api.example.com/v1",
		"model":    "gpt-4o-mini",
		"api_key":  "sk-migrate-live-llm-1234567890",
	})
	srcCtrl := NewColabOneController(ColabOneControllerConfig{
		ConfigDir: srcDir,
		Vault:     srcVault,
	})
	defer srcCtrl.Close()

	exported, err := srcCtrl.ExportMigrationPayload([]string{migration.ModuleCredentials}, "v0.3.0", "linux", nil)
	if err != nil {
		t.Fatalf("ExportMigrationPayload failed: %v", err)
	}
	if exported.Credentials == nil || asString(exported.Credentials.LiveExternalLLM["model"]) != "gpt-4o-mini" {
		t.Fatalf("expected LiveExternalLLM exported, got %#v", exported.Credentials)
	}

	dstDir := t.TempDir()
	dstVault := NewColabOneMemoryVault()
	dstCtrl := NewColabOneController(ColabOneControllerConfig{
		ConfigDir: dstDir,
		Vault:     dstVault,
	})
	defer dstCtrl.Close()

	beforeGen := dstCtrl.opGen.Load()
	if _, err := dstCtrl.ApplyMigrationPayload(context.Background(), exported, []string{migration.ModuleCredentials}); err != nil {
		t.Fatalf("ApplyMigrationPayload credentials failed: %v", err)
	}
	if afterGen := dstCtrl.opGen.Load(); afterGen < beforeGen+2 {
		t.Fatalf("expected opGen incremented on acquire and release (+2), got %d -> %d", beforeGen, afterGen)
	}
	snapLLM := asMap(dstCtrl.Snapshot()["live_external_llm"])
	if !asBool(snapLLM["configured"]) || asString(snapLLM["model"]) != "gpt-4o-mini" || !asBool(snapLLM["has_api_key"]) {
		t.Fatalf("expected snapshot live_external_llm updated after migration, got %#v", snapLLM)
	}

	// Rollback of live_external_llm when a downstream file write fails (settings.json blocked by dir).
	rbDir := t.TempDir()
	rbVault := NewColabOneMemoryVault()
	_ = rbVault.Set(colabOneVaultLiveExternalLLMKey, map[string]any{
		"base_url": "https://old.example.com/v1",
		"model":    "old-model",
		"api_key":  "sk-old-key-1234567890",
	})
	rbCtrl := NewColabOneController(ColabOneControllerConfig{
		ConfigDir: rbDir,
		Vault:     rbVault,
	})
	defer rbCtrl.Close()
	if err := os.MkdirAll(filepath.Join(rbDir, "settings.json", "blocker"), 0o700); err != nil {
		t.Fatal(err)
	}
	rbPayload := &migration.ColabOneMigrationPayload{
		SchemaVersion:   migration.COLABONE_MIGRATION_SCHEMA_VERSION,
		IncludedModules: []string{migration.ModuleCredentials, migration.ModuleSettings},
		Credentials: &migration.ColabOneMigrationCredentialsData{
			LiveExternalLLM: map[string]any{
				"base_url": "https://new.example.com/v1",
				"model":    "new-model",
				"api_key":  "sk-new-key-1234567890",
			},
		},
		Settings: &migration.ColabOneMigrationSettingsData{
			Controller: map[string]any{"gpu": "L4"},
		},
	}
	if _, err := rbCtrl.ApplyMigrationPayload(context.Background(), rbPayload, []string{migration.ModuleCredentials, migration.ModuleSettings}); err == nil {
		t.Fatal("expected settings write failure to trigger rollback")
	}
	rawRolled, ok := rbVault.Get(colabOneVaultLiveExternalLLMKey)
	if !ok || asString(asMap(rawRolled)["model"]) != "old-model" {
		t.Fatalf("expected live_external_llm rolled back to old-model, got %#v (ok=%v)", rawRolled, ok)
	}

	// Invalid LiveExternalLLM SSRF URL rejected in Phase 1 before touching vault.
	badLLMPayload := &migration.ColabOneMigrationPayload{
		SchemaVersion:   migration.COLABONE_MIGRATION_SCHEMA_VERSION,
		IncludedModules: []string{migration.ModuleCredentials},
		Credentials: &migration.ColabOneMigrationCredentialsData{
			LiveExternalLLM: map[string]any{
				"base_url": "https://169.254.169.254/v1",
				"model":    "evil",
				"api_key":  "sk-evil-1234567890",
			},
		},
	}
	if _, err := dstCtrl.ApplyMigrationPayload(context.Background(), badLLMPayload, []string{migration.ModuleCredentials}); err == nil {
		t.Fatal("expected SSRF LiveExternalLLM base_url to be rejected")
	}

	// 2. validateMigrationAccount SSRF token_uri allowlist.
	for _, goodURI := range []string{
		"",
		"https://oauth2.googleapis.com/token",
		"https://accounts.google.com/o/oauth2/token",
	} {
		if _, err := validateMigrationAccount(&migration.ColabOneMigrationAccountData{
			Colab: map[string]any{
				"refresh_token": "1//valid-refresh",
				"token_uri":     goodURI,
			},
		}); err != nil {
			t.Fatalf("expected trusted token_uri %q to pass, got %v", goodURI, err)
		}
	}
	for _, badURI := range []string{
		"http://oauth2.googleapis.com/token",
		"https://evil.example.com/token",
		"https://user:pass@oauth2.googleapis.com/token",
		"https://oauth2.googleapis.com/token?redirect=https://evil.example",
		"https://oauth2.googleapis.com/token#frag",
		"https://oauth2.googleapis.com:443/token",
		"https://oauth2.googleapis.com/token/",
		" https://oauth2.googleapis.com/token",
	} {
		if _, err := validateMigrationAccount(&migration.ColabOneMigrationAccountData{
			Colab: map[string]any{
				"refresh_token": "1//valid-refresh",
				"token_uri":     badURI,
			},
		}); err == nil {
			t.Fatalf("expected untrusted token_uri %q to be rejected", badURI)
		}
	}

	// 3. INI injection / control characters rejected across ALL OAuth fields, scopes, profile, and cloud_project.
	for _, field := range []string{
		"token", "refresh_token", "id_token", "client_id", "client_secret",
		"quota_project_id", "rapt_token", "universe_domain", "account", "expiry",
	} {
		for _, badVal := range []string{"val\n[evil]", "val\r\nfoo=bar", "val\x00null"} {
			colabMap := map[string]any{"refresh_token": "1//base", field: badVal}
			if _, err := validateMigrationAccount(&migration.ColabOneMigrationAccountData{Colab: colabMap}); err == nil {
				t.Fatalf("expected control char in field %q (%q) to be rejected", field, badVal)
			}
		}
	}
	if _, err := validateMigrationAccount(&migration.ColabOneMigrationAccountData{
		Colab: map[string]any{"refresh_token": "1//ok", "scopes": []string{"openid", "drive\n[evil]"}},
	}); err == nil {
		t.Fatal("expected control char in scopes slice to be rejected")
	}
	if _, err := validateMigrationAccount(&migration.ColabOneMigrationAccountData{
		Colab:         map[string]any{"refresh_token": "1//ok"},
		GoogleProfile: map[string]string{"email": "user@example.com\r\nX: 1"},
	}); err == nil {
		t.Fatal("expected control char in GoogleProfile.email to be rejected")
	}
	if _, err := validateMigrationAccount(&migration.ColabOneMigrationAccountData{
		Colab:        map[string]any{"refresh_token": "1//ok"},
		CloudProject: "colabone-proj1\n",
	}); err == nil {
		t.Fatal("expected control char in CloudProject to be rejected")
	}
}

func TestControllerMigrationAssignMaxAttemptsPolicyUpgradeAndPreservation(t *testing.T) {
	t.Run("legacy_bundle_upgrades_20_to_default_and_hides_policy_marker", func(t *testing.T) {
		dstDir := t.TempDir()
		dst := NewColabOneController(ColabOneControllerConfig{
			ConfigDir: dstDir,
			Vault:     NewColabOneMemoryVault(),
		})
		defer dst.Close()

		legacyPayload := &migration.ColabOneMigrationPayload{
			SchemaVersion:   migration.COLABONE_MIGRATION_SCHEMA_VERSION,
			IncludedModules: []string{migration.ModuleSettings},
			Settings: &migration.ColabOneMigrationSettingsData{
				Controller: map[string]any{
					"assign_max_attempts": 20,
				},
			},
		}

		if _, err := dst.ApplyMigrationPayload(context.Background(), legacyPayload, []string{migration.ModuleSettings}); err != nil {
			t.Fatalf("ApplyMigrationPayload failed: %v", err)
		}

		snapSettings := asMap(dst.Snapshot()["settings"])
		if got, ok := asExactInt(snapSettings["assign_max_attempts"]); !ok || got != COLABONE_DEFAULT_ASSIGN_MAX_ATTEMPTS {
			t.Fatalf("expected legacy assign_max_attempts=20 migrated to %d, got %v (ok=%v)", COLABONE_DEFAULT_ASSIGN_MAX_ATTEMPTS, snapSettings["assign_max_attempts"], ok)
		}
		if _, leaked := snapSettings["default_assign_max_attempts_policy"]; leaked {
			t.Fatalf("default_assign_max_attempts_policy must not be exposed in Snapshot settings: %+v", snapSettings)
		}
	})

	t.Run("post_migration_bundle_preserves_explicit_20_across_reload", func(t *testing.T) {
		dstDir := t.TempDir()
		dstVault := NewColabOneMemoryVault()
		dst := NewColabOneController(ColabOneControllerConfig{
			ConfigDir: dstDir,
			Vault:     dstVault,
		})
		defer dst.Close()

		postMigrationPayload := &migration.ColabOneMigrationPayload{
			SchemaVersion:   migration.COLABONE_MIGRATION_SCHEMA_VERSION,
			IncludedModules: []string{migration.ModuleSettings},
			Settings: &migration.ColabOneMigrationSettingsData{
				Controller: map[string]any{
					"assign_max_attempts":                20,
					"default_assign_max_attempts_policy": COLABONE_DEFAULT_ASSIGN_MAX_ATTEMPTS_POLICY_VERSION,
				},
			},
		}

		if _, err := dst.ApplyMigrationPayload(context.Background(), postMigrationPayload, []string{migration.ModuleSettings}); err != nil {
			t.Fatalf("ApplyMigrationPayload failed: %v", err)
		}

		snapSettings := asMap(dst.Snapshot()["settings"])
		if got, ok := asExactInt(snapSettings["assign_max_attempts"]); !ok || got != 20 {
			t.Fatalf("expected explicit post-migration assign_max_attempts=20 preserved immediately, got %v (ok=%v)", snapSettings["assign_max_attempts"], ok)
		}
		if _, leaked := snapSettings["default_assign_max_attempts_policy"]; leaked {
			t.Fatalf("default_assign_max_attempts_policy must not be exposed in Snapshot settings: %+v", snapSettings)
		}

		reloaded := NewColabOneController(ColabOneControllerConfig{
			ConfigDir: dstDir,
			Vault:     dstVault,
		})
		defer reloaded.Close()

		reloadedSettings := asMap(reloaded.Snapshot()["settings"])
		if got, ok := asExactInt(reloadedSettings["assign_max_attempts"]); !ok || got != 20 {
			t.Fatalf("expected explicit post-migration assign_max_attempts=20 preserved after reload, got %v (ok=%v)", reloadedSettings["assign_max_attempts"], ok)
		}
		if _, leaked := reloadedSettings["default_assign_max_attempts_policy"]; leaked {
			t.Fatalf("default_assign_max_attempts_policy must not be exposed after reload: %+v", reloadedSettings)
		}
	})
}
