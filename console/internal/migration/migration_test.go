package migration

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func testFastKDF() ColabOneMigrationKDFParams {
	return ColabOneMigrationKDFParams{
		Time:      1,
		MemoryKiB: 8 * 1024,
		Threads:   1,
	}
}

func samplePayload() *ColabOneMigrationPayload {
	maskDefault := true
	logsWidth := 420
	return &ColabOneMigrationPayload{
		SchemaVersion:   COLABONE_MIGRATION_SCHEMA_VERSION,
		AppVersion:      "v0.2.5",
		ExportedAt:      "2026-11-24T12:00:00Z",
		SourceOS:        "windows",
		IncludedModules: append([]string(nil), AllModules...),
		Account: &ColabOneMigrationAccountData{
			Colab: map[string]any{
				"token":         "ya29.super-secret-access-token",
				"refresh_token": "1//0g-super-secret-refresh-token",
				"client_id":     "764086051850-test.apps.googleusercontent.com",
				"scopes":        []string{"openid", "https://www.googleapis.com/auth/drive.file"},
			},
			GoogleProfile: map[string]string{
				"email": "alice@example.com",
				"sub":   "google-sub-12345",
			},
			CloudProject: "colabone-drive-abc123",
		},
		Credentials: &ColabOneMigrationCredentialsData{
			HFToken:     "hf_superSecretTokenValue123456",
			AssignProxy: "http://proxyuser:proxypass@proxy.example.com:8080",
			PublicAPI: map[string]any{
				"mode":         "named",
				"domain":       "api.example.dev",
				"tunnel_token": "eyJhIjoiY2YtdHVubmVsLXRva2VuIn0=",
				"api_key":      "sk-colabone-secret-key-999",
			},
			ComfyUICanvas: map[string]any{
				"mode":         "named",
				"domain":       "comfy.example.dev",
				"tunnel_token": "eyJhIjoiY29tZnktdHVubmVsLXRva2VuIn0=",
			},
		},
		Settings: &ColabOneMigrationSettingsData{
			Controller: map[string]any{
				"gpu":                  "L4",
				"idle_minutes":         45,
				"download_threads":     32,
				"auto_drive":           true,
				"assign_max_attempts":  25,
				"assign_proxy_enabled": true,
				"locale":               "zh-CN",
			},
			Appearance:    "dark",
			CloseBehavior: "background",
			Updater: &ColabOneMigrationUpdaterPrefs{
				AutoCheck:         true,
				IncludePrerelease: true,
				SkippedVersion:    "0.2.4",
			},
			UIPrefs: &ColabOneMigrationUIPrefs{
				Appearance:         "dark",
				Locale:             "zh-CN",
				PrivacyMaskDefault: &maskDefault,
				MonitorLayout:      "grid",
				CUUsageMetric:      "rate",
				LogsWidth:          &logsWidth,
			},
		},
		LibraryWorkflows: &ColabOneMigrationLibraryWorkflowsData{
			Library: map[string]any{
				"common_ids":       []string{"qwen3-8b", "llama3-8b"},
				"custom_models":    []map[string]any{{"id": "hf-custom1", "name": "Custom 1"}},
				"api_id_overrides": map[string]string{"qwen3-8b": "my-qwen"},
				"common_repos":     []map[string]any{{"repo": "org/repo1"}},
			},
			Workflows: []map[string]any{
				{"id": "wf-custom1", "name": "My Workflow"},
			},
		},
		CUUsage: map[string]any{
			"version":        1,
			"bucket_seconds": 1800,
			"accounts": map[string]any{
				"acct:1234567890abcdef": map[string]any{
					"buckets": []any{
						map[string]any{"ts": int64(1700000000), "consumed": 1.25},
						map[string]any{"ts": int64(1700086400), "consumed": 2.5},
					},
				},
			},
		},
		WorkflowDrafts: map[string]any{
			"version": 1,
			"drafts": map[string]any{
				"wf-custom1": map[string]any{
					"workflow_id": "wf-custom1",
					"fields":      map[string]any{"prompt": "hello world", "steps": 20},
					"media":       map[string]any{},
				},
			},
		},
	}
}

func TestSealAndOpenRoundTripProductionParams(t *testing.T) {
	payload := samplePayload()
	raw, err := SealPayload(payload, "s3cr3t-Pass!")
	if err != nil {
		t.Fatalf("SealPayload failed: %v", err)
	}

	// Outer envelope must contain zero plaintext secrets or PII.
	rawStr := string(raw)
	for _, secret := range []string{
		"alice@example.com",
		"ya29.super-secret",
		"1//0g-super-secret",
		"hf_superSecretToken",
		"proxypass",
		"api.example.dev",
		"sk-colabone-secret-key",
		"comfy.example.dev",
		"colabone-drive-abc123",
	} {
		if strings.Contains(rawStr, secret) {
			t.Fatalf("encrypted archive leaked plaintext %q", secret)
		}
	}

	header, err := InspectEnvelope(raw)
	if err != nil {
		t.Fatalf("InspectEnvelope failed: %v", err)
	}
	if header.Format != COLABONE_MIGRATION_FORMAT || header.SchemaVersion != COLABONE_MIGRATION_SCHEMA_VERSION {
		t.Fatalf("unexpected header: %+v", header)
	}

	opened, err := OpenPayload(raw, "s3cr3t-Pass!")
	if err != nil {
		t.Fatalf("OpenPayload failed: %v", err)
	}
	if opened.AppVersion != "v0.2.5" || opened.SourceOS != "windows" {
		t.Fatalf("metadata mismatch: %+v", opened)
	}
	if opened.Account == nil || opened.Account.GoogleProfile["email"] != "alice@example.com" {
		t.Fatalf("account profile mismatch: %+v", opened.Account)
	}
	if opened.Credentials == nil || opened.Credentials.HFToken != "hf_superSecretTokenValue123456" {
		t.Fatalf("credentials mismatch")
	}

	// Wrong password fails cleanly with ErrWrongPassword.
	if _, err := OpenPayload(raw, "wrong-password"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("expected ErrWrongPassword, got %v", err)
	}
}

func TestTamperedEnvelopeAndAADProtection(t *testing.T) {
	raw, err := SealPayloadWithParams(samplePayload(), "my-pass-123", testFastKDF())
	if err != nil {
		t.Fatalf("SealPayloadWithParams failed: %v", err)
	}

	// 1. Flip a byte in ciphertext -> ErrWrongPassword.
	var env ColabOneMigrationEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	ct, _ := base64.StdEncoding.DecodeString(env.Ciphertext)
	ct[0] ^= 0xFF
	env.Ciphertext = base64.StdEncoding.EncodeToString(ct)
	tampered, _ := json.Marshal(env)
	if _, err := OpenPayload(tampered, "my-pass-123"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("tampered ciphertext: expected ErrWrongPassword, got %v", err)
	}

	// 2. Flip KDF threads header -> AAD mismatch -> ErrWrongPassword.
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	env.KDF.Threads = 2
	tamperedAAD, _ := json.Marshal(env)
	if _, err := OpenPayload(tamperedAAD, "my-pass-123"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("tampered AAD: expected ErrWrongPassword, got %v", err)
	}

	// 3. Future schema_version -> ErrUnsupportedVersion on InspectEnvelope.
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	env.SchemaVersion = COLABONE_MIGRATION_SCHEMA_VERSION + 1
	futureRaw, _ := json.Marshal(env)
	if _, err := InspectEnvelope(futureRaw); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("future version: expected ErrUnsupportedVersion, got %v", err)
	}

	// 4. Hostile Argon2id memory parameter -> ErrInvalidEnvelope before KDF executes.
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	env.KDF.MemoryKiB = 1024 * 1024 * 2 // 2 GiB exceeds defensive 256 MiB cap
	dosRaw, _ := json.Marshal(env)
	if _, err := InspectEnvelope(dosRaw); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("DoS KDF params: expected ErrInvalidEnvelope, got %v", err)
	}

	// 5. UTF-8 BOM prefix is tolerated seamlessly.
	withBOM := append([]byte{0xEF, 0xBB, 0xBF}, raw...)
	if _, err := OpenPayload(withBOM, "my-pass-123"); err != nil {
		t.Fatalf("BOM-prefixed archive should open cleanly, got %v", err)
	}
}

func TestValidatePasswordRules(t *testing.T) {
	cases := []struct {
		pw   string
		want error
	}{
		{"", ErrPasswordTooShort},
		{"12345", ErrPasswordTooShort},
		{"      ", ErrPasswordTooShort},
		{"123456", nil},
		{"密码六个字符", nil},
		{strings.Repeat("a", COLABONE_MIGRATION_MAX_PASSWORD_LEN+1), ErrPasswordTooLong},
	}
	for _, tc := range cases {
		err := ValidatePassword(tc.pw)
		if !errors.Is(err, tc.want) {
			t.Fatalf("ValidatePassword(%q) = %v, want %v", tc.pw, err, tc.want)
		}
	}
}

func TestBuildModuleSummariesAndRedaction(t *testing.T) {
	payload := samplePayload()
	// Verify String()/GoString() never leak tokens.
	formatted := fmt.Sprintf("%s %#v %s %#v", payload, payload, payload.Account, payload.Credentials)
	for _, secret := range []string{"ya29.", "1//0g-", "hf_super", "proxypass", "sk-colabone"} {
		if strings.Contains(formatted, secret) {
			t.Fatalf("String/GoString leaked secret %q: %s", secret, formatted)
		}
	}

	// Re-open through JSON roundtrip so numbers become json.Number as in real OpenPayload.
	raw, err := SealPayloadWithParams(payload, "pass1234", testFastKDF())
	if err != nil {
		t.Fatal(err)
	}
	opened, err := OpenPayload(raw, "pass1234")
	if err != nil {
		t.Fatal(err)
	}

	summaries := BuildModuleSummaries(opened)
	if len(summaries) != len(AllModules) {
		t.Fatalf("summary count = %d, want %d", len(summaries), len(AllModules))
	}

	byID := make(map[string]ColabOneMigrationModuleSummary, len(summaries))
	for _, s := range summaries {
		byID[s.ID] = s
	}

	acct := byID[ModuleAccount]
	if !acct.Included || !acct.HasData || acct.Account == nil ||
		!acct.Account.HasLogin || !acct.Account.PersistentLogin ||
		!acct.Account.HasDriveScope || !acct.Account.HasCloudProject ||
		acct.Account.Email != "alice@example.com" {
		t.Fatalf("unexpected account summary: %+v", acct)
	}

	creds := byID[ModuleCredentials]
	if creds.Credentials == nil || creds.Credentials.ConfiguredCount != 4 ||
		creds.Credentials.PublicAPIDomain != "api.example.dev" ||
		creds.Credentials.ComfyCanvasDomain != "comfy.example.dev" {
		t.Fatalf("unexpected credentials summary: %+v", creds)
	}

	settings := byID[ModuleSettings]
	if settings.Settings == nil || settings.Settings.GPU != "L4" ||
		settings.Settings.IdleMinutes != 45 || settings.Settings.DownloadThreads != 32 ||
		settings.Settings.Appearance != "dark" || settings.Settings.CloseBehavior != "background" ||
		!settings.Settings.PrivacyMaskDefault {
		t.Fatalf("unexpected settings summary: %+v", settings)
	}

	lib := byID[ModuleLibraryWorkflows]
	if lib.LibraryWorkflows == nil || lib.LibraryWorkflows.CommonModelCount != 2 ||
		lib.LibraryWorkflows.CustomModelCount != 1 || lib.LibraryWorkflows.CommonRepoCount != 1 ||
		lib.LibraryWorkflows.APIIDOverrideCount != 1 || lib.LibraryWorkflows.CustomWorkflowCount != 1 {
		t.Fatalf("unexpected library summary: %+v", lib)
	}

	cu := byID[ModuleCUUsage]
	if cu.CUUsage == nil || cu.CUUsage.AccountCount != 1 || cu.CUUsage.BucketCount != 2 || cu.CUUsage.DayCount != 2 {
		t.Fatalf("unexpected cu_usage summary: %+v", cu)
	}

	drafts := byID[ModuleWorkflowDrafts]
	if drafts.WorkflowDrafts == nil || drafts.WorkflowDrafts.DraftCount != 1 || drafts.WorkflowDrafts.FieldCount != 2 {
		t.Fatalf("unexpected drafts summary: %+v", drafts)
	}

	// Check JSON serialization of summaries contains zero secrets.
	sumBytes, _ := json.Marshal(summaries)
	for _, secret := range []string{"ya29.", "1//0g-", "hf_super", "proxypass", "sk-colabone", "tunnel-token"} {
		if bytes.Contains(sumBytes, []byte(secret)) {
			t.Fatalf("summary JSON leaked secret %q", secret)
		}
	}
}

func TestNormalizeModules(t *testing.T) {
	got, err := NormalizeModules([]string{ModuleWorkflowDrafts, ModuleAccount, ModuleAccount})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != ModuleAccount || got[1] != ModuleWorkflowDrafts {
		t.Fatalf("got %v, want [account workflow_drafts]", got)
	}
	if _, err := NormalizeModules(nil); !errors.Is(err, ErrNoModulesSelected) {
		t.Fatalf("expected ErrNoModulesSelected, got %v", err)
	}
	if _, err := NormalizeModules([]string{"unknown"}); err == nil {
		t.Fatal("expected error for unknown module")
	}
}

func TestSchemaVersionZeroAndBOMWithLeadingWhitespace(t *testing.T) {
	raw, err := SealPayloadWithParams(samplePayload(), "pw123456", testFastKDF())
	if err != nil {
		t.Fatalf("SealPayloadWithParams failed: %v", err)
	}

	// UTF-8 BOM followed by leading newline/spaces must still open cleanly.
	withBOMAndSpace := append([]byte{0xEF, 0xBB, 0xBF, '\n', ' ', '\t'}, raw...)
	if _, err := OpenPayload(withBOMAndSpace, "pw123456"); err != nil {
		t.Fatalf("expected BOM + whitespace envelope to open, got: %v", err)
	}

	// Missing / zero schema_version must return ErrInvalidEnvelope (not ErrUnsupportedVersion).
	var env ColabOneMigrationEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	env.SchemaVersion = 0
	zeroRaw, _ := json.Marshal(env)
	if _, err := InspectEnvelope(zeroRaw); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("schema_version=0 InspectEnvelope got %v, want ErrInvalidEnvelope", err)
	}

	// ValidatePayload with SchemaVersion < 1 must return ErrCorruptPayload, and > current returns ErrUnsupportedVersion.
	pZero := samplePayload()
	pZero.SchemaVersion = 0
	if err := ValidatePayload(pZero); !errors.Is(err, ErrCorruptPayload) {
		t.Fatalf("ValidatePayload(schema=0) got %v, want ErrCorruptPayload", err)
	}
	pFuture := samplePayload()
	pFuture.SchemaVersion = COLABONE_MIGRATION_SCHEMA_VERSION + 1
	if err := ValidatePayload(pFuture); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("ValidatePayload(schema=future) got %v, want ErrUnsupportedVersion", err)
	}
}

func TestLiveExternalLLMMigrationSummaryRoundTripAndRedaction(t *testing.T) {
	p := samplePayload()
	secretKey := "sk-live-ext-llm-super-secret-9999"
	p.Credentials.LiveExternalLLM = map[string]any{
		"base_url": "https://llm.example.com/v1",
		"model":    "gpt-4o-mini",
		"api_key":  secretKey,
	}

	formatted := fmt.Sprintf("%s %#v", p.Credentials, p.Credentials)
	if strings.Contains(formatted, secretKey) || strings.Contains(formatted, "llm.example.com") {
		t.Fatalf("Credentials String/GoString leaked live_external_llm secret: %s", formatted)
	}
	if !strings.Contains(formatted, "HasLiveExternalLLM:true") {
		t.Fatalf("expected HasLiveExternalLLM:true in formatted credentials, got %s", formatted)
	}

	raw, err := SealPayloadWithParams(p, "pass123456", testFastKDF())
	if err != nil {
		t.Fatalf("SealPayloadWithParams failed: %v", err)
	}
	if strings.Contains(string(raw), secretKey) {
		t.Fatal("sealed envelope leaked live_external_llm api_key")
	}

	opened, err := OpenPayload(raw, "pass123456")
	if err != nil {
		t.Fatalf("OpenPayload failed: %v", err)
	}
	if opened.Credentials == nil || opened.Credentials.LiveExternalLLM["api_key"] != secretKey {
		t.Fatalf("LiveExternalLLM did not round-trip: %#v", opened.Credentials)
	}

	summaries := BuildModuleSummaries(opened)
	var credSum *ColabOneMigrationCredentialsSummary
	for _, s := range summaries {
		if s.ID == ModuleCredentials {
			credSum = s.Credentials
			break
		}
	}
	if credSum == nil || !credSum.HasLiveExternalLLM || credSum.ConfiguredCount != 5 {
		t.Fatalf("unexpected credentials summary with live_external_llm: %+v", credSum)
	}
	sumJSON, _ := json.Marshal(summaries)
	if bytes.Contains(sumJSON, []byte(secretKey)) {
		t.Fatal("summary JSON leaked live_external_llm api_key")
	}
}

func TestNotificationsMigrationSummaryRoundTripAndRedaction(t *testing.T) {
	p := samplePayload()
	secrets := map[string]string{
		"telegram_bot_token": "987654321:AAH-super-secret-telegram-token",
		"telegram_proxy_url": "socks5://tguser:tgpass-secret@proxy.internal:1080",
		"bark_device_key":    "bark-device-secret-key-xyz789",
		"email_password":     "smtp-app-secret-password-456",
	}
	p.Credentials.Notifications = map[string]any{
		"telegram_bot_token": secrets["telegram_bot_token"],
		"telegram_proxy_url": secrets["telegram_proxy_url"],
		"bark_device_key":    secrets["bark_device_key"],
		"email_password":     secrets["email_password"],
	}
	// Also verify assign_max_attempts: 0 (unlimited) round-trips cleanly.
	p.Settings.Controller["assign_max_attempts"] = 0

	formatted := fmt.Sprintf("%s %#v", p.Credentials, p.Credentials)
	if !strings.Contains(formatted, "HasNotifications:true") {
		t.Fatalf("expected HasNotifications:true in formatted credentials, got %s", formatted)
	}
	for key, val := range secrets {
		if strings.Contains(formatted, val) {
			t.Fatalf("Credentials String/GoString leaked notifications %s: %s", key, formatted)
		}
	}

	raw, err := SealPayloadWithParams(p, "pass123456", testFastKDF())
	if err != nil {
		t.Fatalf("SealPayloadWithParams failed: %v", err)
	}
	rawStr := string(raw)
	for key, val := range secrets {
		if strings.Contains(rawStr, val) {
			t.Fatalf("sealed archive leaked notifications %s", key)
		}
	}

	opened, err := OpenPayload(raw, "pass123456")
	if err != nil {
		t.Fatalf("OpenPayload failed: %v", err)
	}
	if opened.Credentials == nil || len(opened.Credentials.Notifications) != len(secrets) {
		t.Fatalf("Notifications did not round-trip: %#v", opened.Credentials)
	}
	for key, wantVal := range secrets {
		if gotVal, _ := opened.Credentials.Notifications[key].(string); gotVal != wantVal {
			t.Fatalf("Notifications[%q] = %q, want %q", key, gotVal, wantVal)
		}
	}

	// Verify assign_max_attempts: 0 (unlimited) is preserved cleanly on round-trip.
	if opened.Settings == nil || opened.Settings.Controller == nil {
		t.Fatalf("Settings.Controller missing after round-trip")
	}
	rawMaxAttempts, exists := opened.Settings.Controller["assign_max_attempts"]
	if !exists || asInt(rawMaxAttempts) != 0 {
		t.Fatalf("assign_max_attempts did not round-trip as 0: exists=%v val=%#v", exists, rawMaxAttempts)
	}

	summaries := BuildModuleSummaries(opened)
	var credSum *ColabOneMigrationCredentialsSummary
	var settingsMod *ColabOneMigrationModuleSummary
	for i := range summaries {
		switch summaries[i].ID {
		case ModuleCredentials:
			credSum = summaries[i].Credentials
		case ModuleSettings:
			settingsMod = &summaries[i]
		}
	}
	if credSum == nil || !credSum.HasNotifications || credSum.ConfiguredCount != 5 {
		t.Fatalf("unexpected credentials summary with notifications: %+v", credSum)
	}
	if settingsMod == nil || !settingsMod.HasData || settingsMod.Settings == nil || settingsMod.Settings.GPU != "L4" {
		t.Fatalf("unexpected settings summary with assign_max_attempts=0: %+v", settingsMod)
	}

	sumJSON, _ := json.Marshal(summaries)
	for key, val := range secrets {
		if bytes.Contains(sumJSON, []byte(val)) {
			t.Fatalf("summary JSON leaked notifications %s", key)
		}
	}

	// Empty/whitespace-only Notifications must not count as configured.
	emptyCreds := &ColabOneMigrationCredentialsData{
		Notifications: map[string]any{
			"telegram_bot_token": "   ",
			"bark_device_key":    "",
			"email_password":     "\t\n",
		},
	}
	if strings.Contains(emptyCreds.String(), "HasNotifications:true") {
		t.Fatalf("expected HasNotifications:false for whitespace-only notifications, got %s", emptyCreds.String())
	}
	emptySum := summarizeCredentials(emptyCreds)
	if emptySum.HasNotifications || emptySum.ConfiguredCount != 0 {
		t.Fatalf("expected empty notifications summary not configured, got %+v", emptySum)
	}

	// Credentials module with only Notifications configured reports HasData=true and ConfiguredCount=1.
	onlyNotifPayload := &ColabOneMigrationPayload{
		SchemaVersion:   COLABONE_MIGRATION_SCHEMA_VERSION,
		IncludedModules: []string{ModuleCredentials},
		Credentials: &ColabOneMigrationCredentialsData{
			Notifications: map[string]any{"bark_device_key": "bark-only-secret"},
		},
	}
	onlySummaries := BuildModuleSummaries(onlyNotifPayload)
	for _, s := range onlySummaries {
		if s.ID == ModuleCredentials {
			if !s.HasData || s.Credentials == nil || !s.Credentials.HasNotifications || s.Credentials.ConfiguredCount != 1 {
				t.Fatalf("unexpected notifications-only credentials module summary: %+v", s)
			}
		}
	}
}
