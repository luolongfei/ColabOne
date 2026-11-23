package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func makeTestSpec() ColabOneModelSpec {
	sum := sha256.Sum256([]byte("12345678"))
	spec := NewColabOneModelSpec()
	spec.ID = "test-model"
	spec.Name = "Test model"
	spec.Files = []ColabOneModelFile{
		{
			Repo:     "org/model",
			Revision: strings.Repeat("a", 40),
			Path:     "model.gguf",
			Size:     8,
			SHA256:   hex.EncodeToString(sum[:]),
			Role:     "model",
		},
	}
	spec.ApplyDefaults()
	return spec
}

func specToMap(t *testing.T, spec ColabOneModelSpec) map[string]any {
	t.Helper()
	b, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal spec map: %v", err)
	}
	return m
}

func validateFromMap(t *testing.T, m map[string]any) (*ColabOneModelSpec, error) {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal map: %v", err)
	}
	return ParseModelSpec(b)
}

func TestValidatePresetsCatalog(t *testing.T) {
	presetsPath := filepath.Join("..", "..", "..", "catalog", "presets.json")
	raw, err := os.ReadFile(presetsPath)
	if err != nil {
		t.Fatalf("read catalog/presets.json: %v", err)
	}
	var specs []ColabOneModelSpec
	if err := json.Unmarshal(raw, &specs); err != nil {
		t.Fatalf("unmarshal catalog/presets.json: %v", err)
	}
	if len(specs) != 4 {
		t.Fatalf("expected 4 presets, got %d", len(specs))
	}

	expectedIDs := []string{
		"qwen38-huihui-v2",
		"gemma4-31b",
		"qwen38-27b-rvn",
		"glm53-flash",
	}
	expectedAPIIDs := []string{
		"colabone/qwen3.8-flash-next",
		"colabone/gemma-4-31b",
		"colabone/qwen3.8-27b",
		"colabone/glm-5.3-flash",
	}
	expectedOwners := []string{
		"huihui-ai",
		"mradermacher",
		"0bserverx",
		"GCSA-AiLab",
	}

	for i, spec := range specs {
		if spec.ID != expectedIDs[i] {
			t.Errorf("preset[%d].ID = %q, want %q", i, spec.ID, expectedIDs[i])
		}
		if spec.APIID != expectedAPIIDs[i] {
			t.Errorf("preset[%d].APIID = %q, want %q", i, spec.APIID, expectedAPIIDs[i])
		}
		if ResolveAPIModelID(spec) != expectedAPIIDs[i] {
			t.Errorf("ResolveAPIModelID(preset[%d]) = %q, want %q", i, ResolveAPIModelID(spec), expectedAPIIDs[i])
		}
		if ModelOwner(spec) != expectedOwners[i] {
			t.Errorf("ModelOwner(preset[%d]) = %q, want %q", i, ModelOwner(spec), expectedOwners[i])
		}
		if spec.Kind != "chat" {
			t.Errorf("preset[%d].Kind = %q, want chat", i, spec.Kind)
		}
		if err := spec.Validate(); err != nil {
			t.Errorf("preset[%d].Validate() failed: %v", i, err)
		}
		// Ensure round-trip JSON marshaling and unmarshaling preserves full fidelity.
		encoded, err := json.Marshal(spec)
		if err != nil {
			t.Fatalf("marshal preset[%d]: %v", i, err)
		}
		decoded, err := ParseModelSpec(encoded)
		if err != nil {
			t.Fatalf("round-trip ParseModelSpec preset[%d]: %v", i, err)
		}
		if decoded.ID != spec.ID || decoded.APIID != spec.APIID || len(decoded.Files) != len(spec.Files) {
			t.Fatalf("round-trip mismatch for preset[%d]", i)
		}
	}

	// Verify 64-bit size (137 GB) and cpu_moe_layers on glm53-flash.
	glm := specs[3]
	if glm.CPUMoELayers != 19 {
		t.Errorf("glm53-flash CPUMoELayers = %d, want 19", glm.CPUMoELayers)
	}
	if glm.Files[0].Size != 137073273280 {
		t.Errorf("glm53-flash Files[0].Size = %d, want 137073273280", glm.Files[0].Size)
	}
	if len(specs[0].Files) != 4 {
		t.Errorf("qwen38-huihui-v2 file count = %d, want 4", len(specs[0].Files))
	}
}

func TestRejectUnsafeManifest(t *testing.T) {
	base := makeTestSpec()
	cases := []struct {
		name      string
		field     string
		value     any
		wantMatch string
	}{
		{"parent traversal path", "path", "../evil.gguf", "模型文件路径不合法"},
		{"nested traversal path", "path", "sub/../evil.gguf", "模型文件路径不合法"},
		{"dot segment path", "path", "./evil.gguf", "模型文件路径不合法"},
		{"absolute path", "path", "/evil.gguf", "模型文件路径不合法"},
		{"backslash path", "path", `x\evil.gguf`, "模型文件路径不合法"},
		{"colon path", "path", "C:evil.gguf", "模型文件路径不合法"},
		{"overlong path", "path", strings.Repeat("a", 508) + ".gguf", "模型文件路径不合法"},
		{"non-pinned revision", "revision", "main", "模型必须固定到完整 commit SHA"},
		{"url repo", "repo", "https://evil.test/secret", "需要 Hugging Face 的 owner/repository"},
		{"dotdot repo", "repo", "../secret", "需要 Hugging Face 的 owner/repository"},
		{"dot repo", "repo", "org/.", "需要 Hugging Face 的 owner/repository"},
		{"bad sha256", "sha256", "bad", "文件需要可信的 SHA-256"},
		{"zero size", "size", 0, "文件大小必须大于 0"},
		{"negative size", "size", -1, "文件大小必须大于 0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := specToMap(t, base)
			files := data["files"].([]any)
			files[0].(map[string]any)[tc.field] = tc.value
			_, err := validateFromMap(t, data)
			if err == nil {
				t.Fatalf("expected validation error for %s=%v, got nil", tc.field, tc.value)
			}
			if !strings.Contains(err.Error(), tc.wantMatch) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantMatch)
			}
		})
	}
}

func TestSourceCodeIsNotAModelField(t *testing.T) {
	data := specToMap(t, makeTestSpec())
	data["code"] = `print("unsafe")`
	if _, err := validateFromMap(t, data); err == nil {
		t.Fatal("expected unknown field 'code' to be rejected")
	}

	// Unknown field inside nested file object must also be rejected.
	data2 := specToMap(t, makeTestSpec())
	data2["files"].([]any)[0].(map[string]any)["exec"] = "rm -rf /"
	if _, err := validateFromMap(t, data2); err == nil {
		t.Fatal("expected unknown field inside file to be rejected")
	}

	// Wrong-case field key must be rejected (case-sensitive like Pydantic).
	rawWrongCase := `{"ID":"test-model","id":"test-model","name":"Test","files":[{"repo":"org/m","revision":"` +
		strings.Repeat("a", 40) + `","path":"m.gguf","size":8,"sha256":"` + strings.Repeat("1", 64) + `"}]}`
	if _, err := ParseModelSpec([]byte(rawWrongCase)); err == nil {
		t.Fatal("expected wrong-case key 'ID' to be rejected")
	}
}

func TestRejectRemovedCreativeModelKindsAndRecipes(t *testing.T) {
	overrides := []map[string]any{
		{"kind": "image"},
		{"kind": "video"},
		{"kind": ""},
		{"recipe": "z-image-turbo"},
	}
	for _, ov := range overrides {
		data := specToMap(t, makeTestSpec())
		for k, v := range ov {
			data[k] = v
		}
		if _, err := validateFromMap(t, data); err == nil {
			t.Fatalf("expected override %v to be rejected", ov)
		}
	}
}

func TestRejectNonGGUFAndNonLLMRolesAndFileSetInvariants(t *testing.T) {
	base := makeTestSpec()

	badExt := specToMap(t, base)
	badExt["files"].([]any)[0].(map[string]any)["path"] = "weights.safetensors"
	if _, err := validateFromMap(t, badExt); err == nil {
		t.Fatal("expected non-gguf extension to be rejected")
	}

	badRole := specToMap(t, base)
	badRole["files"].([]any)[0].(map[string]any)["role"] = "vae"
	if _, err := validateFromMap(t, badRole); err == nil {
		t.Fatal("expected non-llm role 'vae' to be rejected")
	}

	// Zero entry models (all shards) -> "需要且只能有一个入口模型文件"
	zeroModel := base.Clone()
	zeroModel.Files[0].Role = "shard"
	if err := zeroModel.Validate(); err == nil || err.Error() != "需要且只能有一个入口模型文件" {
		t.Fatalf("expected single-entry error for 0 models, got %v", err)
	}

	// Two entry models -> "需要且只能有一个入口模型文件"
	twoModels := base.Clone()
	f2 := twoModels.Files[0]
	f2.Path = "model2.gguf"
	twoModels.Files = append(twoModels.Files, f2)
	if err := twoModels.Validate(); err == nil || err.Error() != "需要且只能有一个入口模型文件" {
		t.Fatalf("expected single-entry error for 2 models, got %v", err)
	}

	// Duplicate paths -> "文件路径重复"
	dupPath := base.Clone()
	shard := dupPath.Files[0]
	shard.Role = "shard"
	dupPath.Files = append(dupPath.Files, shard)
	if err := dupPath.Validate(); err == nil || err.Error() != "文件路径重复" {
		t.Fatalf("expected duplicate path error, got %v", err)
	}

	// Uppercase SHA-256 normalizes to lowercase.
	upperSHA := base.Clone()
	upperSHA.Files[0].SHA256 = strings.Repeat("A", 64)
	if err := upperSHA.Validate(); err != nil {
		t.Fatalf("expected uppercase sha256 to validate, got %v", err)
	}
	if upperSHA.Files[0].SHA256 != strings.Repeat("a", 64) {
		t.Fatalf("expected sha256 lowercased, got %q", upperSHA.Files[0].SHA256)
	}
}

func TestModelSpecDefaultsAndStrictJSONSemantics(t *testing.T) {
	rev := strings.Repeat("a", 40)
	sha := strings.Repeat("b", 64)

	// Minimal valid JSON omitting api_id, description, min_vram_mib, context_length,
	// temperature, cpu_moe_layers, source, kind, and file role.
	minimal := fmt.Sprintf(`{
		"id": "minimal_1",
		"name": "Minimal",
		"files": [{"repo": "org/repo", "revision": %q, "path": "m.gguf", "size": 100, "sha256": %q}]
	}`, rev, sha)
	spec, err := ParseModelSpec([]byte(minimal))
	if err != nil {
		t.Fatalf("minimal ParseModelSpec failed: %v", err)
	}
	if spec.ContextLength != 8192 {
		t.Errorf("ContextLength = %d, want 8192", spec.ContextLength)
	}
	if spec.Temperature != 0.7 {
		t.Errorf("Temperature = %v, want 0.7", spec.Temperature)
	}
	if spec.Source != "Hugging Face" {
		t.Errorf("Source = %q, want Hugging Face", spec.Source)
	}
	if spec.Kind != "chat" {
		t.Errorf("Kind = %q, want chat", spec.Kind)
	}
	if spec.Files[0].Role != "model" {
		t.Errorf("Files[0].Role = %q, want model", spec.Files[0].Role)
	}

	// Explicit temperature=0.0 must be preserved (NOT overwritten to 0.7).
	zeroTemp := fmt.Sprintf(`{
		"id": "zero-temp",
		"name": "Deterministic",
		"temperature": 0,
		"api_id": null,
		"files": [{"repo": "org/repo", "revision": %q, "path": "m.gguf", "size": 100, "sha256": %q}]
	}`, rev, sha)
	specZeroTemp, err := ParseModelSpec([]byte(zeroTemp))
	if err != nil {
		t.Fatalf("zero temperature ParseModelSpec failed: %v", err)
	}
	if specZeroTemp.Temperature != 0.0 {
		t.Errorf("Temperature = %v, want 0.0", specZeroTemp.Temperature)
	}
	if specZeroTemp.APIID != "" {
		t.Errorf("APIID = %q, want empty", specZeroTemp.APIID)
	}

	// Explicit context_length=0 must be rejected (< 512).
	zeroCtx := fmt.Sprintf(`{
		"id": "bad-ctx",
		"name": "BadCtx",
		"context_length": 0,
		"files": [{"repo": "org/repo", "revision": %q, "path": "m.gguf", "size": 100, "sha256": %q}]
	}`, rev, sha)
	if _, err := ParseModelSpec([]byte(zeroCtx)); err == nil {
		t.Fatal("expected context_length=0 to be rejected")
	}

	// Non-string api_id must return exact Chinese message.
	badAPIIDType := fmt.Sprintf(`{
		"id": "bad-api",
		"name": "BadAPI",
		"api_id": 123,
		"files": [{"repo": "org/repo", "revision": %q, "path": "m.gguf", "size": 100, "sha256": %q}]
	}`, rev, sha)
	if _, err := ParseModelSpec([]byte(badAPIIDType)); err == nil || err.Error() != "模型 API ID 必须为字符串" {
		t.Fatalf("expected non-string api_id error, got %v", err)
	}

	// Null non-nullable fields and null top-level object must be rejected.
	for _, raw := range []string{
		`null`,
		`[]`,
		fmt.Sprintf(`{"id":"ok","name":null,"files":[{"repo":"org/r","revision":%q,"path":"m.gguf","size":1,"sha256":%q}]}`, rev, sha),
		fmt.Sprintf(`{"id":"ok","name":"ok","temperature":null,"files":[{"repo":"org/r","revision":%q,"path":"m.gguf","size":1,"sha256":%q}]}`, rev, sha),
		fmt.Sprintf(`{"id":"ok","name":"ok","files":[null]}`),
		fmt.Sprintf(`{"id":"ok","name":"ok","files":[{"repo":"org/r","revision":%q,"path":"m.gguf","size":1,"sha256":%q,"role":null}]}`, rev, sha),
		fmt.Sprintf(`{"id":"ok","name":"ok","files":[{"repo":"org/r","revision":%q,"path":"m.gguf","size":1,"sha256":%q,"role":""}]}`, rev, sha),
	} {
		if _, err := ParseModelSpec([]byte(raw)); err == nil {
			t.Fatalf("expected error for JSON %s", raw)
		}
	}

	// NaN and Inf temperatures must be rejected by Validate().
	for _, badTemp := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -0.01, 2.01} {
		s := makeTestSpec()
		s.Temperature = badTemp
		if err := s.Validate(); err == nil {
			t.Fatalf("expected error for temperature=%v", badTemp)
		}
	}
}

func TestDefaultAPIModelIDIsCleanAndReadable(t *testing.T) {
	cases := []struct {
		repo     string
		filename string
		expected string
	}{
		{
			"huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
			"unsloth-UD-Q4_K_XL-V2/Qwen3.8-Flash-Next-UD-Q4_K_XL-00001-of-00004.gguf",
			"colabone/qwen3.8-flash-next",
		},
		{
			"huihui-ai/Huihui-DeepSeek-V4.1-Flash-1017-abliterated-GGUF",
			"DeepSeek-V4.1-Flash-Q4_K_M.gguf",
			"colabone/deepseek-v4.1-flash",
		},
		{
			"mradermacher/gemma-4-31B-it-ARA-heretic-GGUF",
			"gemma-4-31B-it-ARA-heretic.Q8_0.gguf",
			"colabone/gemma-4-31b",
		},
		{
			"0bserverx/Qwen3.8-27B-Heretic-Abliterated-Uncensored-GGUF",
			"RVN-BF16.gguf",
			"colabone/qwen3.8-27b",
		},
		{
			"GCSA-AiLab/GLM-5.3-Flash-Uncensored-RCO-GSQ-GGUF",
			"Q4/GLM-5.3-Flash-Uncensored-GSQ-RCO-Q4.gguf",
			"colabone/glm-5.3-flash",
		},
		{
			"unsloth/Qwen3-30B-A3B-Instruct-2507-GGUF",
			"Qwen3-30B-A3B-UD-Q4_K_XL.gguf",
			"colabone/qwen3-30b-a3b",
		},
		{
			"bartowski/Llama-3.3-70B-Instruct-GGUF",
			"Llama-3.3-70B-Instruct-Q4_K_M.gguf",
			"colabone/llama-3.3-70b",
		},
		{
			"org/Qwen3_32B_Q4_K_M_GGUF",
			"Qwen3_32B_Q4_K_M.gguf",
			"colabone/qwen3-32b",
		},
		{
			"org/gguf",
			"model.gguf",
			"colabone/gguf",
		},
	}

	for _, tc := range cases {
		got := DefaultAPIModelID(tc.repo, tc.filename, "")
		if got != tc.expected {
			t.Errorf("DefaultAPIModelID(%q, %q) = %q, want %q", tc.repo, tc.filename, got, tc.expected)
		}
	}

	// Fallbacks when repo is empty: name first, then filename basename, then "model".
	if got := DefaultAPIModelID("", "dir/Qwen3-8B-Q4_K_M.gguf", ""); got != "colabone/qwen3-8b" {
		t.Errorf("filename fallback = %q, want colabone/qwen3-8b", got)
	}
	if got := DefaultAPIModelID("", "dir/ignored.gguf", "My Model Instruct"); got != "colabone/my-model" {
		t.Errorf("name fallback = %q, want colabone/my-model", got)
	}
	if got := DefaultAPIModelID("", "", ""); got != "colabone/model" {
		t.Errorf("empty fallback = %q, want colabone/model", got)
	}
	// Adjacent embedded quants must both be stripped (non-consuming lookahead parity).
	if got := DefaultAPIModelID("org/Foo-Q4_K_M-Q8_0-GGUF", "", ""); got != "colabone/foo" {
		t.Errorf("adjacent embedded quants = %q, want colabone/foo", got)
	}
}

func TestDefaultAPIIDGuardsReservedAutoAndDotsAndDotSegments(t *testing.T) {
	autoID := DefaultAPIModelID("org/Auto-GGUF", "Auto-Q4_K_M.gguf", "")
	if autoID == COLABONE_AUTO_API_MODEL_ID {
		t.Fatalf("DefaultAPIModelID must never return %s", COLABONE_AUTO_API_MODEL_ID)
	}
	if valid, err := ValidateAPIModelID(autoID, false); err != nil || valid != autoID {
		t.Fatalf("ValidateAPIModelID(%q) = (%q, %v)", autoID, valid, err)
	}

	dotsID := DefaultAPIModelID("org/foo..bar-GGUF", "foo..bar-Q4_K_M.gguf", "")
	if strings.Contains(dotsID, "..") {
		t.Fatalf("expected consecutive dots collapsed, got %q", dotsID)
	}
	if valid, err := ValidateAPIModelID(dotsID, false); err != nil || valid != dotsID {
		t.Fatalf("ValidateAPIModelID(%q) = (%q, %v)", dotsID, valid, err)
	}

	for _, bad := range []string{"colabone/./foo", "a/./b"} {
		if _, err := ValidateAPIModelID(bad, false); err == nil {
			t.Errorf("expected ValidateAPIModelID(%q) to fail", bad)
		}
	}
}

func TestUniqueAPIModelIDDisambiguatesOnlyOnCollision(t *testing.T) {
	base := "colabone/qwen3.8-flash-next"
	repo := "huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF"

	got1, err := UniqueAPIModelID(base, nil, repo, "Qwen3.8-Flash-Next-UD-Q4_K_XL.gguf")
	if err != nil || got1 != base {
		t.Fatalf("first = (%q, %v), want %q", got1, err, base)
	}

	got2, err := UniqueAPIModelID(base, []string{base}, repo, "Qwen3.8-Flash-Next-Q8_0.gguf")
	if err != nil || got2 != "colabone/qwen3.8-flash-next-q8" {
		t.Fatalf("second = (%q, %v), want colabone/qwen3.8-flash-next-q8", got2, err)
	}

	got3, err := UniqueAPIModelID(
		base,
		[]string{base, "colabone/qwen3.8-flash-next-q4"},
		repo,
		"Qwen3.8-Flash-Next-UD-Q4_K_XL.gguf",
	)
	if err != nil || got3 != "colabone/qwen3.8-flash-next-ud-q4_k_xl" {
		t.Fatalf("third = (%q, %v), want colabone/qwen3.8-flash-next-ud-q4_k_xl", got3, err)
	}

	got4, err := UniqueAPIModelID(
		base,
		[]string{base, "colabone/qwen3.8-flash-next-q4", "colabone/qwen3.8-flash-next-ud-q4_k_xl"},
		repo,
		"Qwen3.8-Flash-Next-UD-Q4_K_XL.gguf",
	)
	if err != nil || got4 != "colabone/qwen3.8-flash-next-huihui" {
		t.Fatalf("fourth = (%q, %v), want colabone/qwen3.8-flash-next-huihui", got4, err)
	}

	// Numeric suffix fallback when quant + full quant + owner are all taken (case-insensitive).
	got5, err := UniqueAPIModelID(
		base,
		[]string{
			strings.ToUpper(base),
			"colabone/qwen3.8-flash-next-q4",
			"colabone/qwen3.8-flash-next-ud-q4_k_xl",
			"colabone/qwen3.8-flash-next-huihui",
		},
		repo,
		"Qwen3.8-Flash-Next-UD-Q4_K_XL.gguf",
	)
	if err != nil || got5 != "colabone/qwen3.8-flash-next-2" {
		t.Fatalf("fifth = (%q, %v), want colabone/qwen3.8-flash-next-2", got5, err)
	}

	// Resetting a disambiguated second variant (from test_library_and_equip.py).
	resetGot, err := UniqueAPIModelID(
		base,
		[]string{"colabone/qwen3.8-flash-next"},
		repo,
		"Qwen3.8-Flash-Next-Q8_0.gguf",
	)
	if err != nil || resetGot != "colabone/qwen3.8-flash-next-q8" {
		t.Fatalf("reset disambiguated = (%q, %v), want colabone/qwen3.8-flash-next-q8", resetGot, err)
	}

	// Invalid base_api_id returns validation error.
	if _, err := UniqueAPIModelID(COLABONE_AUTO_API_MODEL_ID, nil, repo, "a.gguf"); err == nil {
		t.Fatal("expected reserved colabone/auto base to fail")
	}
}

func TestCompactQuantTag(t *testing.T) {
	cases := []struct {
		filename string
		want     string
	}{
		{"Qwen3.8-Flash-Next-UD-Q4_K_XL.gguf", "q4"},
		{"Qwen3.8-Flash-Next-Q8_0.gguf", "q8"},
		{"Qwen3-30B-A3B-IQ4_XS.gguf", "iq4"},
		{"BitNet-TQ1_0.gguf", "tq1"},
		{"RVN-BF16.gguf", "bf16"},
		{"model-F16.gguf", "f16"},
		{"model-F32.gguf", "f32"},
		{"model-FP8_E4M3FN.gguf", "fp8"},
		{"model-MXFP4_MOE.gguf", "mxfp4"},
		{"Q4/GLM-5.3-Flash-Uncensored-GSQ-RCO-Q4.gguf", "q4"},
		{"Q4_K_M/model.gguf", "q4"},
		{"Q4-Q8.gguf", "q8"},
		{"model.gguf", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := CompactQuantTag(tc.filename); got != tc.want {
			t.Errorf("CompactQuantTag(%q) = %q, want %q", tc.filename, got, tc.want)
		}
	}
}

func TestValidateAndResolveAPIModelIDAndOwner(t *testing.T) {
	if got, err := ValidateAPIModelID("colabone/qwen3.8-flash-next", false); err != nil || got != "colabone/qwen3.8-flash-next" {
		t.Fatalf("valid api_id = (%q, %v)", got, err)
	}
	if got, err := ValidateAPIModelID("  my-coder  ", false); err != nil || got != "my-coder" {
		t.Fatalf("trimmed api_id = (%q, %v)", got, err)
	}
	if got, err := ValidateAPIModelID("", true); err != nil || got != "" {
		t.Fatalf("allowEmpty api_id = (%q, %v)", got, err)
	}
	if _, err := ValidateAPIModelID("   ", false); err == nil || err.Error() != "模型 API ID 不能为空" {
		t.Fatalf("empty disallow error = %v", err)
	}

	for _, auto := range []string{COLABONE_AUTO_API_MODEL_ID, "COLABONE/AUTO", " colabone/Auto "} {
		_, err := ValidateAPIModelID(auto, false)
		if err == nil || err.Error() != "colabone/auto 为系统保留别名，请使用其他模型 ID" {
			t.Fatalf("reserved %q error = %v", auto, err)
		}
	}

	badList := []string{
		"../evil",
		"a//b",
		"colabone/",
		"colabone.",
		"bad id",
		"colabone/./foo",
		"a/./b",
		"-leading-dash",
		strings.Repeat("a", 121),
	}
	const wantBadMsg = "模型 API ID 仅支持字母、数字、点、下划线、连字符和斜杠，且不能以斜杠或点结尾"
	for _, bad := range badList {
		_, err := ValidateAPIModelID(bad, false)
		if err == nil || err.Error() != wantBadMsg {
			t.Errorf("ValidateAPIModelID(%q) error = %v, want %q", bad, err, wantBadMsg)
		}
	}

	spec := makeTestSpec()
	// Non-hf spec without explicit api_id falls back to its id.
	if got := ResolveAPIModelID(spec); got != "test-model" {
		t.Errorf("ResolveAPIModelID(spec) = %q, want test-model", got)
	}
	if got := ModelOwner(spec); got != "org" {
		t.Errorf("ModelOwner(spec) = %q, want org", got)
	}

	// Legacy hf-* spec without saved api_id auto-derives a readable colabone/* ID.
	legacyHF := spec.Clone()
	legacyHF.ID = "hf-82b42fca20f78bbf"
	legacyHF.APIID = ""
	legacyHF.Name = "Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF"
	legacyHF.Files[0].Repo = "huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF"
	legacyHF.Files[0].Path = "Qwen3.8-Flash-Next-UD-Q4_K_XL.gguf"

	if got := ResolveAPIModelID(legacyHF); got != "colabone/qwen3.8-flash-next" {
		t.Errorf("ResolveAPIModelID(legacyHF) = %q, want colabone/qwen3.8-flash-next", got)
	}
	if got := ResolveAPIModelIDFromMap(specToMap(t, legacyHF)); got != "colabone/qwen3.8-flash-next" {
		t.Errorf("ResolveAPIModelIDFromMap(legacyHF) = %q, want colabone/qwen3.8-flash-next", got)
	}
	if got := ModelOwner(legacyHF); got != "huihui-ai" {
		t.Errorf("ModelOwner(legacyHF) = %q, want huihui-ai", got)
	}
	if got := ModelOwnerFromMap(specToMap(t, legacyHF)); got != "huihui-ai" {
		t.Errorf("ModelOwnerFromMap(legacyHF) = %q, want huihui-ai", got)
	}

	// Explicit custom api_id always wins.
	custom := legacyHF.Clone()
	custom.APIID = "colabone/my-qwen"
	if got := ResolveAPIModelID(custom); got != "colabone/my-qwen" {
		t.Errorf("ResolveAPIModelID(custom) = %q, want colabone/my-qwen", got)
	}
	if got := ResolveAPIModelIDFromMap(specToMap(t, custom)); got != "colabone/my-qwen" {
		t.Errorf("ResolveAPIModelIDFromMap(custom) = %q, want colabone/my-qwen", got)
	}

	// Fallback repo field directly on map & missing owner defaults to "colabone".
	if got := ModelOwnerFromMap(map[string]any{"repo": "bartowski/Llama-GGUF"}); got != "bartowski" {
		t.Errorf("ModelOwnerFromMap top-level repo = %q, want bartowski", got)
	}
	if got := ModelOwnerFromMap(map[string]any{"repo": "noslash"}); got != "colabone" {
		t.Errorf("ModelOwnerFromMap noslash = %q, want colabone", got)
	}
	if got := ModelOwnerFromMap(nil); got != "colabone" {
		t.Errorf("ModelOwnerFromMap(nil) = %q, want colabone", got)
	}
	if got := ResolveAPIModelIDFromMap(map[string]any{
		"id":   "hf-1234567890abcdef",
		"repo": "bartowski/Llama-3.3-70B-Instruct-GGUF",
	}); got != "colabone/llama-3.3-70b" {
		t.Errorf("ResolveAPIModelIDFromMap top-level repo = %q, want colabone/llama-3.3-70b", got)
	}
}

func TestParseHFInputVariants(t *testing.T) {
	cases := []struct {
		raw      string
		filename string
		revision string
		wantRepo string
		wantFile string
		wantRev  string
	}{
		{"owner/repo", "", "main", "owner/repo", "", "main"},
		{"  owner/repo  ", " qwen-q4_k_m.gguf ", "v1.0", "owner/repo", "qwen-q4_k_m.gguf", "v1.0"},
		{"owner/repo", "", "", "owner/repo", "", "main"},
		{"https://huggingface.co/owner/repo", "", "main", "owner/repo", "", "main"},
		{"https://www.huggingface.co/owner/repo/", "", "main", "owner/repo", "", "main"},
		{"https://huggingface.co/owner/repo/tree/dev", "", "main", "owner/repo", "", "dev"},
		{"https://huggingface.co/owner/repo/tree/dev", "", "explicit-branch", "owner/repo", "", "explicit-branch"},
		{"https://huggingface.co/owner/repo/blob/main/qwen-q4_k_m.gguf", "", "main", "owner/repo", "qwen-q4_k_m.gguf", "main"},
		{"https://huggingface.co/owner/repo/resolve/main/qwen-q4_k_m.gguf", "", "main", "owner/repo", "qwen-q4_k_m.gguf", "main"},
		{"https://huggingface.co/owner/repo/resolve/dev/sub/qwen-q4_k_m.gguf", "", "main", "owner/repo", "sub/qwen-q4_k_m.gguf", "dev"},
		{"https://huggingface.co/owner/repo/blob/main/README.md", "", "main", "owner/repo", "", "main"},
		{"https://huggingface.co/owner/repo/resolve/main/my%20model+v1.gguf?download=true#frag", "", "main", "owner/repo", "my model+v1.gguf", "main"},
	}

	for _, tc := range cases {
		repo, fn, rev := ParseHFInput(tc.raw, tc.filename, tc.revision)
		if repo != tc.wantRepo || fn != tc.wantFile || rev != tc.wantRev {
			t.Errorf("ParseHFInput(%q, %q, %q) = (%q, %q, %q), want (%q, %q, %q)",
				tc.raw, tc.filename, tc.revision, repo, fn, rev, tc.wantRepo, tc.wantFile, tc.wantRev)
		}
	}
}

func TestResolveImportPinsCommitAndAllShards(t *testing.T) {
	var hits atomic.Int32
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sha": strings.Repeat("b", 40),
			"siblings": []map[string]any{
				{
					"rfilename": "model-00001-of-00002.gguf",
					"lfs":       map[string]any{"sha256": strings.Repeat("1", 64), "size": 16},
				},
				{
					"rfilename": "model-00002-of-00002.gguf",
					"lfs":       map[string]any{"sha256": strings.Repeat("2", 64), "size": 16},
				},
			},
		})
	}))
	defer srv.Close()

	spec, err := ResolveImport(context.Background(), ColabOneResolveImportOptions{
		Repo:       "org/model",
		Filename:   "model-00002-of-00002.gguf",
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
	})
	if err != nil {
		t.Fatalf("ResolveImport failed: %v", err)
	}
	if len(spec.Files) != 2 || spec.Files[0].Role != "model" || spec.Files[1].Role != "shard" {
		t.Fatalf("unexpected shard files: %+v", spec.Files)
	}
	for _, f := range spec.Files {
		if f.Revision != strings.Repeat("b", 40) {
			t.Errorf("file revision = %q, want %q", f.Revision, strings.Repeat("b", 40))
		}
	}
	if hits.Load() != 1 {
		t.Errorf("HTTP hits = %d, want 1", hits.Load())
	}
	if gotPath != "/api/models/org/model/revision/main" || gotQuery != "blobs=true" {
		t.Errorf("request = %s?%s, want /api/models/org/model/revision/main?blobs=true", gotPath, gotQuery)
	}
}

func TestResolveImportEstimatesVRAMAndBlocksAuxiliaryOrComfyModels(t *testing.T) {
	var currentPayload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(currentPayload)
	}))
	defer srv.Close()

	currentPayload = map[string]any{
		"sha":          strings.Repeat("c", 40),
		"pipeline_tag": "text-generation",
		"siblings": []map[string]any{
			{
				"rfilename": "Qwen3-32B-Q8_0.gguf",
				"lfs":       map[string]any{"sha256": strings.Repeat("a", 64), "size": int64(32) << 30},
			},
			{
				"rfilename": "imatrix_micro3.gguf",
				"lfs":       map[string]any{"sha256": strings.Repeat("b", 64), "size": int64(1) << 30},
			},
		},
	}

	spec, err := ResolveImport(context.Background(), ColabOneResolveImportOptions{
		Repo:       "org/Qwen3-32B-GGUF",
		Filename:   "Qwen3-32B-Q8_0.gguf",
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
	})
	if err != nil {
		t.Fatalf("ResolveImport failed: %v", err)
	}
	if spec.MinVRAMMib != 0 {
		t.Errorf("MinVRAMMib = %d, want 0", spec.MinVRAMMib)
	}
	if !strings.Contains(spec.Description, "Q8_0") {
		t.Errorf("Description = %q, want Q8_0", spec.Description)
	}

	_, err = ResolveImport(context.Background(), ColabOneResolveImportOptions{
		Repo:       "org/Qwen3-32B-GGUF",
		Filename:   "imatrix_micro3.gguf",
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
	})
	if err == nil || !strings.Contains(err.Error(), "辅助文件") {
		t.Fatalf("expected auxiliary error, got %v", err)
	}

	currentPayload = map[string]any{
		"sha":          strings.Repeat("d", 40),
		"pipeline_tag": "text-to-video",
		"siblings": []map[string]any{
			{
				"rfilename": "wan2.1-Q4_K_M.gguf",
				"lfs":       map[string]any{"sha256": strings.Repeat("e", 64), "size": int64(8) << 30},
			},
		},
	}
	_, err = ResolveImport(context.Background(), ColabOneResolveImportOptions{
		Repo:       "city96/Wan2.1-T2V-14B-gguf",
		Filename:   "wan2.1-Q4_K_M.gguf",
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
	})
	if err == nil || !strings.Contains(err.Error(), "ComfyUI") {
		t.Fatalf("expected ComfyUI error for video model, got %v", err)
	}

	// Image repo detected by repo name even when pipeline_tag is empty.
	currentPayload = map[string]any{
		"sha": strings.Repeat("d", 40),
		"siblings": []map[string]any{
			{
				"rfilename": "flux-Q4_K_M.gguf",
				"lfs":       map[string]any{"sha256": strings.Repeat("e", 64), "size": int64(8) << 30},
			},
		},
	}
	_, err = ResolveImport(context.Background(), ColabOneResolveImportOptions{
		Repo:       "city96/FLUX.1-dev-gguf",
		Filename:   "flux-Q4_K_M.gguf",
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
	})
	if err == nil || !strings.Contains(err.Error(), "ComfyUI") {
		t.Fatalf("expected ComfyUI error for image model, got %v", err)
	}
}

func TestResolveImportPopulatesReadableAPIID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sha":          strings.Repeat("e", 40),
			"pipeline_tag": "text-generation",
			"siblings": []map[string]any{
				{
					"rfilename": "unsloth-UD-Q4_K_XL-V2/Qwen3.8-Flash-Next-UD-Q4_K_XL-00001-of-00002.gguf",
					"lfs":       map[string]any{"sha256": strings.Repeat("1", 64), "size": 1024},
				},
				{
					"rfilename": "unsloth-UD-Q4_K_XL-V2/Qwen3.8-Flash-Next-UD-Q4_K_XL-00002-of-00002.gguf",
					"lfs":       map[string]any{"sha256": strings.Repeat("2", 64), "size": 1024},
				},
			},
		})
	}))
	defer srv.Close()

	imported, err := ResolveImport(context.Background(), ColabOneResolveImportOptions{
		Repo:       "huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
		Filename:   "unsloth-UD-Q4_K_XL-V2/Qwen3.8-Flash-Next-UD-Q4_K_XL-00001-of-00002.gguf",
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
	})
	if err != nil {
		t.Fatalf("ResolveImport failed: %v", err)
	}
	if !strings.HasPrefix(imported.ID, "hf-") {
		t.Errorf("ID = %q, want hf- prefix", imported.ID)
	}
	if imported.APIID != "colabone/qwen3.8-flash-next" {
		t.Errorf("APIID = %q, want colabone/qwen3.8-flash-next", imported.APIID)
	}
}

func TestResolveImportURLVariantsAndCustomName(t *testing.T) {
	cases := []struct {
		repoInput     string
		filenameInput string
		expectedRev   string
		customName    string
		expectedName  string
	}{
		{"owner/repo", "qwen-q4_k_m.gguf", "main", "", "repo"},
		{"https://huggingface.co/owner/repo", "qwen-q4_k_m.gguf", "main", "", "repo"},
		{"https://huggingface.co/owner/repo/tree/dev", "qwen-q4_k_m.gguf", "dev", "", "repo"},
		{"https://huggingface.co/owner/repo/blob/main/qwen-q4_k_m.gguf", "", "main", "", "repo"},
		{"https://huggingface.co/owner/repo/resolve/main/qwen-q4_k_m.gguf", "", "main", "My Custom Qwen", "My Custom Qwen"},
	}

	for _, tc := range cases {
		t.Run(tc.repoInput, func(t *testing.T) {
			var gotPath, gotQuery, gotAuth string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotQuery = r.URL.RawQuery
				gotAuth = r.Header.Get("Authorization")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"sha": strings.Repeat("e", 40),
					"siblings": []map[string]any{
						{
							"rfilename": "qwen-q4_k_m.gguf",
							"lfs":       map[string]any{"sha256": strings.Repeat("f", 64), "size": 8192},
						},
					},
				})
			}))
			defer srv.Close()

			spec, err := ResolveImport(context.Background(), ColabOneResolveImportOptions{
				Repo:       tc.repoInput,
				Filename:   tc.filenameInput,
				Token:      "hf_dummy",
				CustomName: tc.customName,
				HTTPClient: srv.Client(),
				BaseURL:    srv.URL,
			})
			if err != nil {
				t.Fatalf("ResolveImport failed: %v", err)
			}
			if spec.Name != tc.expectedName {
				t.Errorf("Name = %q, want %q", spec.Name, tc.expectedName)
			}
			if !strings.HasPrefix(spec.ID, "hf-") {
				t.Errorf("ID = %q, want hf- prefix", spec.ID)
			}
			if len(spec.Files) != 1 {
				t.Fatalf("len(Files) = %d, want 1", len(spec.Files))
			}
			f := spec.Files[0]
			if f.Repo != "owner/repo" || f.Revision != strings.Repeat("e", 40) || f.Path != "qwen-q4_k_m.gguf" || f.Size != 8192 || f.SHA256 != strings.Repeat("f", 64) {
				t.Errorf("unexpected file: %+v", f)
			}
			wantPath := "/api/models/owner/repo/revision/" + tc.expectedRev
			if gotPath != wantPath || gotQuery != "blobs=true" || gotAuth != "Bearer hf_dummy" {
				t.Errorf("request = (%q, %q, %q), want (%q, blobs=true, Bearer hf_dummy)", gotPath, gotQuery, gotAuth, wantPath)
			}
		})
	}
}

func TestResolveImportRejectsNonHFURLsAndMissingFileWithoutNetwork(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cases := []struct {
		badRepo  string
		filename string
		revision string
	}{
		{"https://evil.example/a/b", "qwen-q4_k_m.gguf", "main"},
		{"https://evil.example/owner/repo/blob/main/qwen-q4_k_m.gguf", "", "main"},
		{"https://huggingface.co.evil.example/owner/repo", "qwen-q4_k_m.gguf", "main"},
		{"ftp://huggingface.co/owner/repo", "qwen-q4_k_m.gguf", "main"},
		{"just-one-segment", "qwen-q4_k_m.gguf", "main"},
		{"https://huggingface.co/owner/repo", "", "main"},
		{"owner/repo", "qwen-q4_k_m.gguf", "../bad"},
		{"owner/repo", "qwen-q4_k_m.gguf", "."},
	}

	for _, tc := range cases {
		_, err := ResolveImport(context.Background(), ColabOneResolveImportOptions{
			Repo:       tc.badRepo,
			Filename:   tc.filename,
			Revision:   tc.revision,
			HTTPClient: srv.Client(),
			BaseURL:    srv.URL,
		})
		if err == nil {
			t.Errorf("expected error for repo=%q filename=%q rev=%q", tc.badRepo, tc.filename, tc.revision)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("expected 0 HTTP requests for rejected inputs, got %d", hits.Load())
	}
}

func TestResolveImportEdgeCasesAndErrors(t *testing.T) {
	// 1. Non-2xx HTTP status returns ColabOneHTTPStatusError.
	srv401 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv401.Close()

	_, err := ResolveImport(context.Background(), ColabOneResolveImportOptions{
		Repo:       "org/private",
		Filename:   "m.gguf",
		HTTPClient: srv401.Client(),
		BaseURL:    srv401.URL,
	})
	var httpErr *ColabOneHTTPStatusError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 401 {
		t.Fatalf("expected ColabOneHTTPStatusError(401), got %v", err)
	}
	if httpErr.Error() != "服务返回 HTTP 401，请检查授权和配置" {
		t.Errorf("httpErr.Error() = %q", httpErr.Error())
	}

	// 2. Non-JSON or JSON array/null response returns exact Chinese error.
	for _, body := range []string{"not-json", "[]", "null", `"hello"`} {
		srvBad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		_, err := ResolveImport(context.Background(), ColabOneResolveImportOptions{
			Repo:       "org/repo",
			Filename:   "m.gguf",
			HTTPClient: srvBad.Client(),
			BaseURL:    srvBad.URL,
		})
		srvBad.Close()
		if err == nil || err.Error() != "模型社区返回了无效结果" {
			t.Errorf("body=%s: error = %v, want 模型社区返回了无效结果", body, err)
		}
	}

	// 3. Invalid shard counts (0 or 129) return exact error.
	srvOK := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sha":      strings.Repeat("a", 40),
			"siblings": []any{},
		})
	}))
	defer srvOK.Close()

	for _, badShard := range []string{"m-00001-of-00000.gguf", "m-00001-of-00129.gguf"} {
		_, err := ResolveImport(context.Background(), ColabOneResolveImportOptions{
			Repo:       "org/repo",
			Filename:   badShard,
			HTTPClient: srvOK.Client(),
			BaseURL:    srvOK.URL,
		})
		if err == nil || err.Error() != "分片数量需在 1 到 128 之间" {
			t.Errorf("shard=%s: error = %v, want 分片数量需在 1 到 128 之间", badShard, err)
		}
	}

	// 4. Context cancellation aborts cleanly.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ResolveImport(ctx, ColabOneResolveImportOptions{
		Repo:       "org/repo",
		Filename:   "m.gguf",
		HTTPClient: srvOK.Client(),
		BaseURL:    srvOK.URL,
	}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled context error = %v, want context.Canceled", err)
	}

	// 5. Unicode CustomName > 200 runes truncates safely by runes (not bytes).
	srvSingle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sha": strings.Repeat("a", 40),
			"siblings": []map[string]any{
				{
					"rfilename": "model.gguf",
					"lfs":       map[string]any{"sha256": strings.Repeat("1", 64), "size": 42},
				},
			},
		})
	}))
	defer srvSingle.Close()

	longCN := strings.Repeat("模", 250)
	spec, err := ResolveImport(context.Background(), ColabOneResolveImportOptions{
		Repo:       "org/repo",
		Filename:   "model.gguf",
		CustomName: longCN,
		HTTPClient: srvSingle.Client(),
		BaseURL:    srvSingle.URL,
	})
	if err != nil {
		t.Fatalf("long unicode custom name failed: %v", err)
	}
	if len([]rune(spec.Name)) != 200 {
		t.Errorf("len([]rune(Name)) = %d, want 200", len([]rune(spec.Name)))
	}
	if spec.Description != "model.gguf" {
		t.Errorf("Description without quant = %q, want model.gguf", spec.Description)
	}
}

func TestAuxiliaryGGUFAndModalityHelpers(t *testing.T) {
	auxTrue := []string{
		"mmproj-model-f16.gguf",
		"sub/Qwen-mmproj-BF16.gguf",
		"projector.gguf",
		"vision_proj-f16.gguf",
		"vision-proj.gguf",
		"imatrix_micro3.gguf",
		"imatrix.gguf",
		"model-imatrix.gguf",
		"model-imatrix-v2.gguf",
		"vocab-llama.gguf",
		"tokenizer.gguf",
		"adapter_model.gguf",
		"my-lora.gguf",
	}
	for _, p := range auxTrue {
		if !isAuxiliaryGGUF(p) {
			t.Errorf("isAuxiliaryGGUF(%q) = false, want true", p)
		}
	}

	auxFalse := []string{
		"Qwen3-32B-Q8_0.gguf",
		"model-Q4_K_M-imatrix.gguf",
		"gemma-4-31B-it-ARA-heretic.Q8_0.gguf",
	}
	for _, p := range auxFalse {
		if isAuxiliaryGGUF(p) {
			t.Errorf("isAuxiliaryGGUF(%q) = true, want false", p)
		}
	}

	if got := classifyModality("text-to-video", nil, "org/qwen"); got != "video" {
		t.Errorf("classifyModality video pipeline = %q", got)
	}
	if got := classifyModality("text-to-image", nil, "org/qwen"); got != "image" {
		t.Errorf("classifyModality image pipeline = %q", got)
	}
	if got := classifyModality("text-generation", []string{"text-to-video"}, "org/wan2.1"); got != "text" {
		t.Errorf("classifyModality text pipeline override = %q", got)
	}
	if got := classifyModality("", []string{"image-to-image"}, "org/foo"); got != "image" {
		t.Errorf("classifyModality image tag = %q", got)
	}
	if got := classifyModality("", nil, "city96/Wan2.1-T2V-14B-gguf"); got != "video" {
		t.Errorf("classifyModality video repo = %q", got)
	}
	if got := classifyModality("", nil, "city96/FLUX.1-dev-gguf"); got != "image" {
		t.Errorf("classifyModality image repo = %q", got)
	}
	if got := classifyModality("", nil, "org/Qwen3-32B-GGUF"); got != "text" {
		t.Errorf("classifyModality text repo = %q", got)
	}
}

func TestConcurrencySafety(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sha":          strings.Repeat("a", 40),
			"pipeline_tag": "text-generation",
			"siblings": []map[string]any{
				{
					"rfilename": "Qwen3-8B-Q4_K_M.gguf",
					"lfs":       map[string]any{"sha256": strings.Repeat("1", 64), "size": 4096},
				},
			},
		})
	}))
	defer srv.Close()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := DefaultAPIModelID("unsloth/Qwen3-30B-A3B-Instruct-2507-GGUF", "Qwen3-30B-A3B-UD-Q4_K_XL.gguf", "")
			if id != "colabone/qwen3-30b-a3b" {
				t.Errorf("goroutine %d: got %q", idx, id)
			}
			uniq, err := UniqueAPIModelID(id, []string{id}, "unsloth/Qwen3-30B-A3B-Instruct-2507-GGUF", "Qwen3-30B-A3B-Q8_0.gguf")
			if err != nil || uniq != "colabone/qwen3-30b-a3b-q8" {
				t.Errorf("goroutine %d: uniq=(%q, %v)", idx, uniq, err)
			}
			spec, err := ResolveImport(context.Background(), ColabOneResolveImportOptions{
				Repo:       "org/Qwen3-8B-GGUF",
				Filename:   "Qwen3-8B-Q4_K_M.gguf",
				HTTPClient: srv.Client(),
				BaseURL:    srv.URL,
			})
			if err != nil || spec.APIID != "colabone/qwen3-8b" {
				t.Errorf("goroutine %d: resolve=(%v, %v)", idx, spec, err)
			}
		}(i)
	}
	wg.Wait()
}

func TestPythonOracleParity(t *testing.T) {
	defaultCases := []struct {
		repo, filename, name, want string
	}{
		{"org/Foo-Q4_K_M-Q8_0-GGUF", "", "", "colabone/foo"},
		{"org/Qwen3-20250428-Instruct-Q4_K_M-GGUF", "", "", "colabone/qwen3"},
		{"org/20250428-Model-GGUF", "", "", "colabone/20250428-model"},
		{"org/MyModel-v2-GGUF", "", "", "colabone/mymodel-v2"},
		{"org/MyModel-abliterated-v2.1-GGUF", "", "", "colabone/mymodel"},
		{"unsloth/unsloth-DeepSeek-R1-Distill-Qwen-32B-GGUF", "", "", "colabone/deepseek-r1-distill-qwen-32b"},
		{"org/___", "", "", "colabone/model"},
		{"org/...", "", "", "colabone/model"},
		{"org/Q4_K_M", "", "", "colabone/q4-k-m"},
		{"org/abliterated-GGUF", "", "", "colabone/abliterated"},
		{"", "sub/dir/Qwen3-8B-Q4_K_M-00001-of-00003.gguf", "", "colabone/qwen3-8b"},
		{"", "gemma-4-31B-it-ARA-heretic.Q8_0.gguf", "", "colabone/gemma-4-31b-heretic.q8-0"},
		{"a/b", "", "", "colabone/b"},
		{
			"org/" + strings.Repeat("very-long-model-name-", 5) + "GGUF",
			"",
			"",
			"colabone/very-long-model-name-very-long-model-name-very-long-mode",
		},
	}
	for _, tc := range defaultCases {
		if got := DefaultAPIModelID(tc.repo, tc.filename, tc.name); got != tc.want {
			t.Errorf("DefaultAPIModelID(%q, %q, %q) = %q, want %q", tc.repo, tc.filename, tc.name, got, tc.want)
		}
	}

	quantCases := []struct {
		path, wantQuant, wantCompact string
	}{
		{"unsloth-UD-Q4_K_XL-V2/Qwen3.8-Flash-Next-UD-Q4_K_XL-00001-of-00004.gguf", "UD-Q4_K_XL", "q4"},
		{"model-Q4_K_M_S.gguf", "Q4_K_M", "q4"},
		{"model-Q4_Kfoo.gguf", "Q4_KFOO", "q4"},
		{"model-FP8_E4M3XYZ.gguf", "FP8", "fp8"},
		{"model-FP8_E4M3FN.gguf", "FP8_E4M3FN", "fp8"},
		{"model-FP8_E5M2.gguf", "FP8_E5M2", "fp8"},
		{"model-MXFP4_MOE.gguf", "MXFP4_MOE", "mxfp4"},
		{"model-Q4-Q8.gguf", "Q8", "q8"},
		{"Q4_K_M/model.gguf", "Q4_K_M", "q4"},
		{"model-UD-IQ2_XXS.gguf", "UD-IQ2_XXS", "iq2"},
		{"model-TQ2_0.gguf", "TQ2_0", "tq2"},
		{"model-FP16.gguf", "GGUF", ""},
	}
	for _, tc := range quantCases {
		if got := extractQuantization(tc.path); got != tc.wantQuant {
			t.Errorf("extractQuantization(%q) = %q, want %q", tc.path, got, tc.wantQuant)
		}
		if got := CompactQuantTag(tc.path); got != tc.wantCompact {
			t.Errorf("CompactQuantTag(%q) = %q, want %q", tc.path, got, tc.wantCompact)
		}
	}

	parseCases := []struct {
		raw, filename, rev          string
		wantRepo, wantFile, wantRev string
	}{
		{"https://huggingface.co/owner/repo/tree/dev/", "", "main", "owner/repo", "", "dev"},
		{"https://huggingface.co/owner/repo/blob/main/file.gguf/", "", "main", "owner/repo", "", "main"},
		{"https://huggingface.co/owner/repo/resolve/main/my%20model+v1.gguf?download=true#frag", "", "main", "owner/repo", "my model+v1.gguf", "main"},
		{"https://huggingface.co/owner/repo/blob/main/file.gguf", "explicit.gguf", "main", "owner/repo", "explicit.gguf", "main"},
		{"https://huggingface.co/owner/repo/blob/main/a%ZZb.gguf", "", "main", "owner/repo", "a%ZZb.gguf", "main"},
	}
	for _, tc := range parseCases {
		r, f, rev := ParseHFInput(tc.raw, tc.filename, tc.rev)
		if r != tc.wantRepo || f != tc.wantFile || rev != tc.wantRev {
			t.Errorf("ParseHFInput(%q, %q, %q) = (%q, %q, %q), want (%q, %q, %q)",
				tc.raw, tc.filename, tc.rev, r, f, rev, tc.wantRepo, tc.wantFile, tc.wantRev)
		}
	}

	// ApplyDefaults must not overwrite explicit Temperature == 0 (greedy decoding).
	greedy := ColabOneModelSpec{ID: "greedy", Name: "Greedy", Temperature: 0}
	greedy.ApplyDefaults()
	if greedy.Temperature != 0 {
		t.Fatalf("ApplyDefaults clobbered Temperature 0 -> %v", greedy.Temperature)
	}

	// NUL byte in GGUF relative path must be rejected.
	if isValidRelativeGGUFPath("dir/bad\x00.gguf") {
		t.Fatal("expected NUL byte in GGUF path to be rejected")
	}
}

func TestResolveImportRedirectPolicy(t *testing.T) {
	if defaultHFHTTPClient.CheckRedirect == nil {
		t.Fatal("defaultHFHTTPClient.CheckRedirect must be configured")
	}

	const secret = "hf_import_redirect_secret"
	validSHA := strings.Repeat("c", 40)
	validSHA256 := strings.Repeat("d", 64)

	validPayload := map[string]any{
		"sha":          validSHA,
		"pipeline_tag": "text-generation",
		"siblings": []map[string]any{
			{
				"rfilename": "model-Q4_K_M.gguf",
				"lfs":       map[string]any{"sha256": validSHA256, "size": 4096},
			},
		},
	}

	t.Run("same_origin_redirect_keeps_auth_strips_referer", func(t *testing.T) {
		var mu sync.Mutex
		var redirectAuth, redirectReferer string
		var targetHits atomic.Int32

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/models/org/old-repo/revision/main":
				http.Redirect(w, r, "/api/models/org/new-repo/revision/main?blobs=true", http.StatusMovedPermanently)
				return
			case "/api/models/org/new-repo/revision/main":
				targetHits.Add(1)
				mu.Lock()
				redirectAuth = r.Header.Get("Authorization")
				redirectReferer = r.Header.Get("Referer")
				mu.Unlock()
				_ = json.NewEncoder(w).Encode(validPayload)
				return
			default:
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()

		// Nil HTTPClient uses defaultHFHTTPClient directly.
		spec, err := ResolveImport(context.Background(), ColabOneResolveImportOptions{
			Repo:     "org/old-repo",
			Filename: "model-Q4_K_M.gguf",
			Token:    secret,
			BaseURL:  srv.URL,
		})
		if err != nil {
			t.Fatalf("ResolveImport same-origin redirect failed: %v", err)
		}
		if spec == nil || len(spec.Files) != 1 || spec.Files[0].Revision != validSHA {
			t.Fatalf("unexpected redirected spec: %+v", spec)
		}
		if targetHits.Load() != 1 {
			t.Fatalf("targetHits = %d, want 1", targetHits.Load())
		}
		mu.Lock()
		gotRedirectAuth, gotRedirectReferer := redirectAuth, redirectReferer
		mu.Unlock()
		if gotRedirectAuth != "Bearer "+secret {
			t.Fatalf("same-origin redirect Authorization = %q, want Bearer token", gotRedirectAuth)
		}
		if gotRedirectReferer != "" {
			t.Fatalf("same-origin redirect must strip Referer, got %q", gotRedirectReferer)
		}

		// Case-insensitive same-host redirect preserves Authorization and strips Referer.
		origReq, _ := http.NewRequest(http.MethodGet, "https://HUGGINGFACE.CO/api/models/org/old", nil)
		origReq.Header.Set("Authorization", "Bearer "+secret)
		nextReq, _ := http.NewRequest(http.MethodGet, "https://huggingface.co/api/models/org/new", nil)
		nextReq.Header.Set("Authorization", "Bearer "+secret)
		nextReq.Header.Set("Referer", origReq.URL.String())
		if err := colabOneHFCheckRedirect(nextReq, []*http.Request{origReq}); err != nil {
			t.Fatalf("case-insensitive same-origin redirect failed: %v", err)
		}
		if nextReq.Header.Get("Authorization") != "Bearer "+secret {
			t.Fatalf("case-insensitive same-origin stripped Authorization")
		}
		if nextReq.Header.Get("Referer") != "" {
			t.Fatalf("case-insensitive same-origin leaked Referer: %q", nextReq.Header.Get("Referer"))
		}
	})

	t.Run("cross_host_redirect_strips_auth_and_referer", func(t *testing.T) {
		var mu sync.Mutex
		var gotAuth, gotReferer string
		var targetHits atomic.Int32

		targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			targetHits.Add(1)
			mu.Lock()
			gotAuth = r.Header.Get("Authorization")
			gotReferer = r.Header.Get("Referer")
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(validPayload)
		}))
		defer targetSrv.Close()

		originSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, targetSrv.URL+"/redirected", http.StatusFound)
		}))
		defer originSrv.Close()

		spec, err := ResolveImport(context.Background(), ColabOneResolveImportOptions{
			Repo:     "org/repo",
			Filename: "model-Q4_K_M.gguf",
			Token:    secret,
			BaseURL:  originSrv.URL,
		})
		if err != nil {
			t.Fatalf("ResolveImport cross-host redirect failed: %v", err)
		}
		if spec == nil || targetHits.Load() != 1 {
			t.Fatalf("unexpected cross-host redirect result: hits=%d spec=%+v", targetHits.Load(), spec)
		}
		mu.Lock()
		authSnap, refSnap := gotAuth, gotReferer
		mu.Unlock()
		if authSnap != "" {
			t.Fatalf("cross-host redirect leaked Authorization: %q", authSnap)
		}
		if refSnap != "" {
			t.Fatalf("cross-host redirect leaked Referer: %q", refSnap)
		}

		// Subdomain, external host, and bounce-back must strip Authorization and Referer.
		for _, targetURL := range []string{
			"https://cdn.huggingface.co/api/models/org/repo",
			"https://evil.example/api/models/org/repo",
		} {
			origReq, _ := http.NewRequest(http.MethodGet, "https://huggingface.co/api/models/org/repo", nil)
			origReq.Header.Set("Authorization", "Bearer "+secret)
			nextReq, _ := http.NewRequest(http.MethodGet, targetURL, nil)
			nextReq.Header.Set("Authorization", "Bearer "+secret)
			nextReq.Header.Set("Referer", origReq.URL.String())

			if err := colabOneHFCheckRedirect(nextReq, []*http.Request{origReq}); err != nil {
				t.Fatalf("redirect to %s unexpected error: %v", targetURL, err)
			}
			if nextReq.Header.Get("Authorization") != "" {
				t.Fatalf("redirect to %s leaked Authorization", targetURL)
			}
			if nextReq.Header.Get("Referer") != "" {
				t.Fatalf("redirect to %s leaked Referer", targetURL)
			}
		}

		origReq, _ := http.NewRequest(http.MethodGet, "https://huggingface.co/api/models/org/a", nil)
		midReq, _ := http.NewRequest(http.MethodGet, "https://evil.example/bounce", nil)
		bounceReq, _ := http.NewRequest(http.MethodGet, "https://huggingface.co/api/models/org/b", nil)
		bounceReq.Header.Set("Authorization", "Bearer "+secret)
		bounceReq.Header.Set("Referer", midReq.URL.String())
		if err := colabOneHFCheckRedirect(bounceReq, []*http.Request{origReq, midReq}); err != nil {
			t.Fatalf("bounce redirect unexpected error: %v", err)
		}
		if bounceReq.Header.Get("Authorization") != "" || bounceReq.Header.Get("Referer") != "" {
			t.Fatalf("bounce-back redirect leaked headers: auth=%q referer=%q",
				bounceReq.Header.Get("Authorization"), bounceReq.Header.Get("Referer"))
		}
	})

	t.Run("https_to_http_downgrade_refused", func(t *testing.T) {
		var httpHits atomic.Int32
		httpTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			httpHits.Add(1)
			_ = json.NewEncoder(w).Encode(validPayload)
		}))
		defer httpTarget.Close()

		httpsOrigin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, httpTarget.URL+"/downgrade", http.StatusFound)
		}))
		defer httpsOrigin.Close()

		_, err := ResolveImport(context.Background(), ColabOneResolveImportOptions{
			Repo:       "org/repo",
			Filename:   "model-Q4_K_M.gguf",
			Token:      secret,
			BaseURL:    httpsOrigin.URL,
			HTTPClient: httpsOrigin.Client(),
		})
		if err == nil || !strings.Contains(err.Error(), "refusing insecure redirect downgrade") {
			t.Fatalf("downgrade err = %v, want refusing insecure redirect downgrade", err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("downgrade error leaked token: %q", err.Error())
		}
		if httpHits.Load() != 0 {
			t.Fatalf("insecure http target must never be contacted, got %d hits", httpHits.Load())
		}
	})

	t.Run("redirect_hop_limit", func(t *testing.T) {
		var maxHops atomic.Int32
		var finalHits atomic.Int32

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var hop int
			if strings.HasPrefix(r.URL.Path, "/hop/") {
				_, _ = fmt.Sscanf(r.URL.Path, "/hop/%d", &hop)
			}
			if int32(hop) < maxHops.Load() {
				http.Redirect(w, r, fmt.Sprintf("/hop/%d", hop+1), http.StatusFound)
				return
			}
			finalHits.Add(1)
			_ = json.NewEncoder(w).Encode(validPayload)
		}))
		defer srv.Close()

		// 4 redirects succeed under len(via) >= 5 cap.
		maxHops.Store(4)
		if _, err := ResolveImport(context.Background(), ColabOneResolveImportOptions{
			Repo:     "org/repo",
			Filename: "model-Q4_K_M.gguf",
			Token:    secret,
			BaseURL:  srv.URL,
		}); err != nil {
			t.Fatalf("4 redirects should succeed, got %v", err)
		}
		if finalHits.Load() != 1 {
			t.Fatalf("finalHits for 4 redirects = %d, want 1", finalHits.Load())
		}

		// 5 and 6 redirects are refused without reaching final target.
		for _, hops := range []int32{5, 6} {
			finalHits.Store(0)
			maxHops.Store(hops)
			_, err := ResolveImport(context.Background(), ColabOneResolveImportOptions{
				Repo:     "org/repo",
				Filename: "model-Q4_K_M.gguf",
				Token:    secret,
				BaseURL:  srv.URL,
			})
			if err == nil || !strings.Contains(err.Error(), "stopped after 5 redirects") {
				t.Fatalf("%d redirects err = %v, want stopped after 5 redirects", hops, err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("%d redirects error leaked token: %q", hops, err.Error())
			}
			if finalHits.Load() != 0 {
				t.Fatalf("%d redirects should not reach final target, got %d hits", hops, finalHits.Load())
			}
		}
	})
}

func TestColabOneResolveImportOptionsRedactsToken(t *testing.T) {
	const secret = "hf_super_secret_import_options_token"
	opts := ColabOneResolveImportOptions{
		Repo:       "org/Qwen3-32B-GGUF",
		Filename:   "Qwen3-32B-Q4_K_M.gguf",
		Revision:   "main",
		Token:      secret,
		CustomName: "Qwen3 32B",
		BaseURL:    "https://huggingface.co",
	}

	formatted := []string{
		opts.String(),
		opts.GoString(),
		fmt.Sprintf("%s", opts),
		fmt.Sprintf("%v", opts),
		fmt.Sprintf("%+v", opts),
		fmt.Sprintf("%#v", opts),
		fmt.Sprintf("%v", &opts),
		fmt.Sprintf("%+v", &opts),
		fmt.Sprintf("%#v", &opts),
	}
	for i, s := range formatted {
		if strings.Contains(s, secret) {
			t.Fatalf("format[%d] leaked Token: %q", i, s)
		}
		if !strings.Contains(s, "[REDACTED]") {
			t.Fatalf("format[%d] missing [REDACTED] marker: %q", i, s)
		}
		if !strings.Contains(s, "org/Qwen3-32B-GGUF") {
			t.Fatalf("format[%d] missing non-secret fields: %q", i, s)
		}
	}

	emptyOpts := ColabOneResolveImportOptions{Repo: "org/model", Filename: "m.gguf"}
	if s := emptyOpts.String(); strings.Contains(s, "[REDACTED]") || !strings.Contains(s, `Token:""`) {
		t.Fatalf("empty token formatting unexpected: %q", s)
	}

	var nilOpts *ColabOneResolveImportOptions
	if got := fmt.Sprintf("%v %#v", nilOpts, nilOpts); got == "" {
		t.Fatalf("nil options formatting failed")
	}
}
