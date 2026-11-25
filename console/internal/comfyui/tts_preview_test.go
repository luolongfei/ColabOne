package comfyui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestComfyCatalogEightFlagshipTtsEnginesAndPresets(t *testing.T) {
	catalogPath := filepath.Join("..", "..", "..", "catalog", "comfyui.json")
	raw, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatalf("read catalog/comfyui.json: %v", err)
	}
	var catalog ColabOneComfyCatalog
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatalf("unmarshal catalog/comfyui.json: %v", err)
	}
	if err := catalog.Validate(); err != nil {
		t.Fatalf("validate catalog/comfyui.json: %v", err)
	}

	expectedEngines := []string{
		"fish_drama3",
		"moss_tts_v1_5",
		"breeze_tts2",
		"ming_3_tts",
		"voxcpm2",
		"qwen3_tts",
		"indextts2",
		"fish_s2_pro",
	}
	nonCommercial := map[string]bool{
		"fish_drama3": true,
		"breeze_tts2": true,
		"indextts2":   true,
		"fish_s2_pro": true,
	}
	expectedQwenSpeakers := map[string]bool{
		"Vivian":   true,
		"Serena":   true,
		"Uncle_Fu": true,
		"Dylan":    true,
		"Eric":     true,
		"Ryan":     true,
		"Aiden":    true,
		"Ono_Anna": true,
		"Sohee":    true,
	}

	if len(catalog.TtsEngines) != len(expectedEngines) {
		t.Fatalf("len(catalog.TtsEngines) = %d, want %d", len(catalog.TtsEngines), len(expectedEngines))
	}
	for i, wantID := range expectedEngines {
		if catalog.TtsEngines[i].ID != wantID {
			t.Fatalf("catalog.TtsEngines[%d].ID = %q, want %q (canonical order)", i, catalog.TtsEngines[i].ID, wantID)
		}
	}

	byID := make(map[string]ColabOneComfyTtsEngineSpec, len(catalog.TtsEngines))
	for _, eng := range catalog.TtsEngines {
		byID[eng.ID] = eng
	}
	if byID["moss_tts_v1_5"].AuxRepo != "OpenMOSS-Team/MOSS-Audio-Tokenizer" || !commitSHARe.MatchString(byID["moss_tts_v1_5"].AuxRevision) {
		t.Fatalf("moss_tts_v1_5 missing pinned aux_repo/aux_revision: %+v", byID["moss_tts_v1_5"])
	}
	for _, tpl := range catalog.Templates {
		if tpl.ID == "digital-human-infinitetalk" || tpl.ID == "digital-human-longcat" {
			if tpl.Pipeline == nil || tpl.Pipeline.DefaultTtsEngine != "fish_drama3" {
				t.Fatalf("template %q default_tts_engine = %+v, want fish_drama3", tpl.ID, tpl.Pipeline)
			}
		}
	}

	// Duplicate voice_presets[].id inside the same engine must be rejected by Validate().
	dupEng := byID["voxcpm2"]
	dupEng.VoicePresets = []ColabOneComfyTtsVoicePreset{dupEng.VoicePresets[0], dupEng.VoicePresets[0]}
	if err := dupEng.Validate(); err == nil {
		t.Fatalf("expected Validate() to reject duplicate voice_presets[].id")
	}

	for _, eid := range expectedEngines {
		eng, ok := byID[eid]
		if !ok {
			t.Fatalf("catalog missing expected TTS engine %q", eid)
		}
		if !commitSHARe.MatchString(eng.Revision) {
			t.Fatalf("engine %q revision %q is not a 40-char hex SHA", eid, eng.Revision)
		}
		if eng.AuxRepo != "" && !commitSHARe.MatchString(eng.AuxRevision) {
			t.Fatalf("engine %q aux_revision %q is not a 40-char hex SHA", eid, eng.AuxRevision)
		}
		if eng.CustomVoiceRepo != "" && !commitSHARe.MatchString(eng.CustomVoiceRevision) {
			t.Fatalf("engine %q custom_voice_revision %q is not a 40-char hex SHA", eid, eng.CustomVoiceRevision)
		}
		if eng.SourceCommit != "" && !commitSHARe.MatchString(eng.SourceCommit) {
			t.Fatalf("engine %q source_commit %q is not a 40-char hex SHA", eid, eng.SourceCommit)
		}
		if eng.MinDiskMib <= 0 {
			t.Fatalf("engine %q min_disk_mib = %d, want > 0", eid, eng.MinDiskMib)
		}
		if eng.MaxSegmentChars < 40 || eng.MaxSegmentChars > 1000 {
			t.Fatalf("engine %q max_segment_chars = %d out of [40, 1000]", eid, eng.MaxSegmentChars)
		}

		if nonCommercial[eid] {
			if eng.CommercialOK {
				t.Fatalf("engine %q must have CommercialOK=false", eid)
			}
			if strings.TrimSpace(eng.LicenseNotice) == "" {
				t.Fatalf("non-commercial engine %q must provide LicenseNotice", eid)
			}
		} else if !eng.CommercialOK {
			t.Fatalf("commercial engine %q must have CommercialOK=true", eid)
		}

		seenPresets := make(map[string]struct{}, len(eng.VoicePresets))
		universalCount := 0
		femaleCount := 0
		maleCount := 0
		for _, preset := range eng.VoicePresets {
			if _, dup := seenPresets[preset.ID]; dup {
				t.Fatalf("engine %q has duplicate voice preset ID %q", eid, preset.ID)
			}
			seenPresets[preset.ID] = struct{}{}
			if strings.TrimSpace(preset.PreviewText) == "" {
				t.Fatalf("engine %q preset %q has empty PreviewText", eid, preset.ID)
			}
			if preset.SpeakerID == "" {
				universalCount++
				switch preset.Gender {
				case "female":
					femaleCount++
				case "male":
					maleCount++
				}
			}
		}
		if universalCount < 10 || femaleCount < 5 || maleCount < 5 {
			t.Fatalf("engine %q universal presets = %d (female=%d, male=%d), want >=10 (>=5 female, >=5 male)", eid, universalCount, femaleCount, maleCount)
		}
	}

	qwen3 := byID["qwen3_tts"]
	if qwen3.CustomVoiceRepo == "" || !commitSHARe.MatchString(qwen3.CustomVoiceRevision) {
		t.Fatalf("qwen3_tts missing pinned custom_voice_repo/custom_voice_revision: repo=%q rev=%q", qwen3.CustomVoiceRepo, qwen3.CustomVoiceRevision)
	}
	if !qwen3.SupportsCustomSpeakers {
		t.Fatalf("qwen3_tts must set SupportsCustomSpeakers=true")
	}

	foundSpeakers := make(map[string]bool)
	for _, preset := range qwen3.VoicePresets {
		if preset.SpeakerID != "" {
			foundSpeakers[preset.SpeakerID] = true
		}
	}
	if !reflect.DeepEqual(foundSpeakers, expectedQwenSpeakers) {
		t.Fatalf("qwen3_tts official speakers = %v, want %v", foundSpeakers, expectedQwenSpeakers)
	}
}

func TestColabOneComfyTtsPreviewRequestAndValidateTtsPreviewRequest(t *testing.T) {
	assetA := strings.Repeat("a", 32)
	assetB := strings.Repeat("b", 32)
	secretToken := "hf_preview_secret_token_987654"

	// 1. Minimal valid request via struct, JSON unmarshal, and ValidateTtsPreviewRequest.
	var minimal ColabOneComfyTtsPreviewRequest
	if err := unmarshalAndValidateSchema(`{"engine_id":"voxcpm2"}`, &minimal); err != nil {
		t.Fatalf("minimal preview request failed: %v", err)
	}
	if minimal.EngineID != "voxcpm2" || minimal.HFToken != "" || minimal.RefAudioInputID != "" {
		t.Fatalf("unexpected minimal preview request: %+v", minimal)
	}
	normMin, err := ValidateTtsPreviewRequest(map[string]any{"engine_id": "voxcpm2"})
	if err != nil {
		t.Fatalf("ValidateTtsPreviewRequest minimal failed: %v", err)
	}
	if normMin["engine_id"] != "voxcpm2" {
		t.Fatalf("normMin engine_id = %#v, want voxcpm2", normMin["engine_id"])
	}
	if _, hasTok := normMin["hf_token"]; hasTok {
		t.Fatalf("empty hf_token should be omitted from normalized map: %#v", normMin)
	}

	// 2. Full valid request with trimmed hf_token, redaction in String()/GoString(), and max-length boundaries.
	fullRaw := map[string]any{
		"engine_id":            "qwen3_tts",
		"voice_preset":         "qwen_vivian",
		"preview_text":         strings.Repeat("试", 600),
		"voice_prompt":         strings.Repeat("音", 600),
		"emotion_prompt":       strings.Repeat("情", 240),
		"ref_audio_input_id":   assetA,
		"ref_text":             strings.Repeat("文", 600),
		"emotion_ref_input_id": assetB,
		"hf_token":             "  " + secretToken + "  ",
	}
	var fullReq ColabOneComfyTtsPreviewRequest
	if err := unmarshalAndValidateSchema(mustMarshalJSON(t, fullRaw), &fullReq); err != nil {
		t.Fatalf("full preview request failed: %v", err)
	}
	if fullReq.HFToken != secretToken {
		t.Fatalf("trimmed HFToken = %q, want %q", fullReq.HFToken, secretToken)
	}
	if strings.Contains(fullReq.String(), secretToken) {
		t.Fatalf("String() leaked HFToken: %s", fullReq.String())
	}
	if strings.Contains(fmt.Sprintf("%#v", fullReq), secretToken) {
		t.Fatalf("GoString() leaked HFToken: %#v", fullReq)
	}

	normFull, err := ValidateTtsPreviewRequest(fullRaw)
	if err != nil {
		t.Fatalf("ValidateTtsPreviewRequest full failed: %v", err)
	}
	if normFull["hf_token"] != secretToken || normFull["ref_audio_input_id"] != assetA || normFull["emotion_ref_input_id"] != assetB {
		t.Fatalf("unexpected normalized full preview request: %#v", normFull)
	}

	// 3. Negative cases: bad engine_id, bad asset IDs, whitespace/control chars in hf_token, overlong fields, extra unknown field.
	badCases := []struct {
		name string
		body map[string]any
	}{
		{"emptyEngineID", map[string]any{"engine_id": ""}},
		{"traversalEngineID", map[string]any{"engine_id": "../evil"}},
		{"spaceEngineID", map[string]any{"engine_id": "bad engine"}},
		{"overlongEngineID", map[string]any{"engine_id": strings.Repeat("e", 81)}},
		{"badVoicePreset", map[string]any{"engine_id": "voxcpm2", "voice_preset": "../evil"}},
		{"overlongVoicePreset", map[string]any{"engine_id": "voxcpm2", "voice_preset": strings.Repeat("v", 65)}},
		{"badRefAssetTraversal", map[string]any{"engine_id": "voxcpm2", "ref_audio_input_id": "../secret"}},
		{"badRefAssetShort", map[string]any{"engine_id": "voxcpm2", "ref_audio_input_id": strings.Repeat("a", 31)}},
		{"badRefAssetLong", map[string]any{"engine_id": "voxcpm2", "ref_audio_input_id": strings.Repeat("a", 33)}},
		{"badRefAssetUpper", map[string]any{"engine_id": "voxcpm2", "ref_audio_input_id": strings.Repeat("A", 32)}},
		{"badRefAssetNonHex", map[string]any{"engine_id": "voxcpm2", "ref_audio_input_id": strings.Repeat("g", 32)}},
		{"badEmotionAsset", map[string]any{"engine_id": "indextts2", "emotion_ref_input_id": "not-32-hex"}},
		{"hfTokenEmbeddedSpace", map[string]any{"engine_id": "voxcpm2", "hf_token": "hf_abc def"}},
		{"hfTokenCR", map[string]any{"engine_id": "voxcpm2", "hf_token": "hf_abc\rdef"}},
		{"hfTokenLF", map[string]any{"engine_id": "voxcpm2", "hf_token": "hf_abc\ndef"}},
		{"hfTokenTab", map[string]any{"engine_id": "voxcpm2", "hf_token": "hf_abc\tdef"}},
		{"hfTokenNul", map[string]any{"engine_id": "voxcpm2", "hf_token": "hf_abc\x00def"}},
		{"hfTokenDel", map[string]any{"engine_id": "voxcpm2", "hf_token": "hf_abc\x7fdef"}},
		{"hfTokenUnicodeSpace", map[string]any{"engine_id": "voxcpm2", "hf_token": "hf_abc\u3000def"}},
		{"hfTokenOverlong", map[string]any{"engine_id": "voxcpm2", "hf_token": strings.Repeat("x", 513)}},
		{"overlongPreviewText", map[string]any{"engine_id": "voxcpm2", "preview_text": strings.Repeat("试", 601)}},
		{"overlongVoicePrompt", map[string]any{"engine_id": "voxcpm2", "voice_prompt": strings.Repeat("音", 601)}},
		{"overlongEmotionPrompt", map[string]any{"engine_id": "voxcpm2", "emotion_prompt": strings.Repeat("情", 241)}},
		{"overlongRefText", map[string]any{"engine_id": "voxcpm2", "ref_text": strings.Repeat("文", 601)}},
		{"extraUnknownField", map[string]any{"engine_id": "voxcpm2", "unknown_extra": true}},
		{"wrongCaseKey", map[string]any{"EngineID": "voxcpm2"}},
	}
	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			var req ColabOneComfyTtsPreviewRequest
			if err := unmarshalAndValidateSchema(mustMarshalJSON(t, tc.body), &req); err == nil {
				t.Fatalf("%s: expected unmarshal/validate error", tc.name)
			}
			if _, err := ValidateTtsPreviewRequest(tc.body); err == nil {
				t.Fatalf("%s: ValidateTtsPreviewRequest should fail", tc.name)
			}
		})
	}

	// 4. Explicit null payloads and nil map rejected.
	if _, err := ValidateTtsPreviewRequest(nil); err == nil {
		t.Fatalf("ValidateTtsPreviewRequest(nil) should fail")
	}
	for _, rawNull := range []string{
		`null`,
		`{"engine_id":null}`,
		`{"engine_id":"voxcpm2","preview_text":null}`,
		`{"engine_id":"voxcpm2","ref_audio_input_id":null}`,
		`{"engine_id":"voxcpm2","hf_token":null}`,
		`{"engine_id":"voxcpm2","extra":1}`,
	} {
		var req ColabOneComfyTtsPreviewRequest
		if err := unmarshalAndValidateSchema(rawNull, &req); err == nil {
			t.Fatalf("strict JSON payload %s should fail", rawNull)
		}
	}
}

func TestComfyTtsEngineSpecExtendedDefaultsAndValidation(t *testing.T) {
	revA := strings.Repeat("a", 40)
	revB := strings.Repeat("b", 40)

	// 1. Minimal strict JSON populates defaults: max_segment_chars=120, min_disk_mib=0, supports_custom_speakers=false.
	var minimal ColabOneComfyTtsEngineSpec
	minimalRaw := mustMarshalJSON(t, map[string]any{
		"id":       "voxcpm2",
		"name":     "VoxCPM2",
		"repo":     "openbmb/VoxCPM2",
		"revision": revA,
	})
	if err := unmarshalAndValidateSchema(minimalRaw, &minimal); err != nil {
		t.Fatalf("minimal tts engine failed: %v", err)
	}
	if minimal.MaxSegmentChars != 120 {
		t.Fatalf("default MaxSegmentChars = %d, want 120", minimal.MaxSegmentChars)
	}
	if minimal.MinDiskMib != 0 {
		t.Fatalf("default MinDiskMib = %d, want 0", minimal.MinDiskMib)
	}
	if minimal.CustomVoiceRepo != "" || minimal.CustomVoiceRevision != "" || minimal.SupportsCustomSpeakers {
		t.Fatalf("unexpected custom voice defaults: repo=%q rev=%q supports=%v", minimal.CustomVoiceRepo, minimal.CustomVoiceRevision, minimal.SupportsCustomSpeakers)
	}

	// Direct Go struct Validate() also defaults zero MaxSegmentChars to 120.
	direct := ColabOneComfyTtsEngineSpec{
		ID:       "direct_tts",
		Name:     "Direct TTS",
		Repo:     "openbmb/VoxCPM2",
		Revision: revA,
	}
	if err := direct.Validate(); err != nil {
		t.Fatalf("direct Validate() failed: %v", err)
	}
	if direct.MaxSegmentChars != 120 {
		t.Fatalf("direct Validate() MaxSegmentChars = %d, want 120", direct.MaxSegmentChars)
	}

	// 2. Valid extended spec with custom_voice_repo + custom_voice_revision, min_disk_mib, supports_custom_speakers, and boundary max_segment_chars.
	for _, segChars := range []int{40, 320, 1000} {
		var spec ColabOneComfyTtsEngineSpec
		raw := mustMarshalJSON(t, map[string]any{
			"id":                       fmt.Sprintf("ext_%d", segChars),
			"name":                     "Extended TTS",
			"repo":                     "Qwen/Qwen3-TTS-12Hz-1.7B-Base",
			"revision":                 revA,
			"custom_voice_repo":        "Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice",
			"custom_voice_revision":    revB,
			"supports_custom_speakers": true,
			"min_disk_mib":             12288,
			"max_segment_chars":        segChars,
			"voice_presets": []map[string]any{
				{
					"id":           "qwen_vivian",
					"name":         "Vivian",
					"gender":       "female",
					"preview_text": strings.Repeat("试", 400),
					"speaker_id":   "Vivian",
					"seed":         202611,
				},
			},
		})
		if err := unmarshalAndValidateSchema(raw, &spec); err != nil {
			t.Fatalf("extended spec max_segment_chars=%d failed: %v", segChars, err)
		}
		if spec.MaxSegmentChars != segChars || spec.MinDiskMib != 12288 || !spec.SupportsCustomSpeakers {
			t.Fatalf("extended spec fields mismatch: %+v", spec)
		}
		if spec.CustomVoiceRepo != "Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice" || spec.CustomVoiceRevision != revB {
			t.Fatalf("custom voice repo/rev mismatch: %q / %q", spec.CustomVoiceRepo, spec.CustomVoiceRevision)
		}
		if len(spec.VoicePresets) != 1 || spec.VoicePresets[0].SpeakerID != "Vivian" {
			t.Fatalf("nested voice preset mismatch: %+v", spec.VoicePresets)
		}
	}

	// 3. Rejections: custom_voice_repo without custom_voice_revision, max_segment_chars out of range, min_disk_mib < 0, invalid preset fields.
	baseEngine := func() map[string]any {
		return map[string]any{
			"id":       "qwen3_tts",
			"name":     "Qwen3-TTS",
			"repo":     "Qwen/Qwen3-TTS-12Hz-1.7B-Base",
			"revision": revA,
		}
	}
	badCases := []struct {
		name   string
		mutate func(m map[string]any)
	}{
		{"customVoiceRepoWithoutRevision", func(m map[string]any) {
			m["custom_voice_repo"] = "Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice"
		}},
		{"customVoiceRepoWithEmptyRevision", func(m map[string]any) {
			m["custom_voice_repo"] = "Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice"
			m["custom_voice_revision"] = ""
		}},
		{"customVoiceRepoWithBadRevision", func(m map[string]any) {
			m["custom_voice_repo"] = "Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice"
			m["custom_voice_revision"] = "not-a-sha"
		}},
		{"badCustomVoiceRepoSlug", func(m map[string]any) {
			m["custom_voice_repo"] = "../evil"
			m["custom_voice_revision"] = revB
		}},
		{"badCustomVoiceRevisionAlone", func(m map[string]any) {
			m["custom_voice_revision"] = strings.Repeat("g", 40)
		}},
		{"maxSegmentCharsZero", func(m map[string]any) {
			m["max_segment_chars"] = 0
		}},
		{"maxSegmentCharsTooSmall", func(m map[string]any) {
			m["max_segment_chars"] = 39
		}},
		{"maxSegmentCharsNegative", func(m map[string]any) {
			m["max_segment_chars"] = -120
		}},
		{"maxSegmentCharsTooLarge", func(m map[string]any) {
			m["max_segment_chars"] = 1001
		}},
		{"minDiskMibNegative", func(m map[string]any) {
			m["min_disk_mib"] = -1
		}},
		{"presetPreviewTextOverlong", func(m map[string]any) {
			m["voice_presets"] = []map[string]any{
				{"id": "v1", "name": "V1", "preview_text": strings.Repeat("试", 401)},
			}
		}},
		{"presetSpeakerIDInvalid", func(m map[string]any) {
			m["voice_presets"] = []map[string]any{
				{"id": "v1", "name": "V1", "speaker_id": "../evil"},
			}
		}},
		{"presetSpeakerIDSpaces", func(m map[string]any) {
			m["voice_presets"] = []map[string]any{
				{"id": "v1", "name": "V1", "speaker_id": "Ono Anna"},
			}
		}},
	}
	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			m := baseEngine()
			tc.mutate(m)
			var spec ColabOneComfyTtsEngineSpec
			if err := unmarshalAndValidateSchema(mustMarshalJSON(t, m), &spec); err == nil {
				t.Fatalf("%s: expected validation error", tc.name)
			}
		})
	}

	// 4. Null & wrong-case strict JSON checks for the new fields.
	for _, raw := range []string{
		`{"id":"qwen3_tts","name":"Qwen3","repo":"Qwen/Qwen3-TTS-12Hz-1.7B-Base","revision":"` + revA + `","min_disk_mib":null}`,
		`{"id":"qwen3_tts","name":"Qwen3","repo":"Qwen/Qwen3-TTS-12Hz-1.7B-Base","revision":"` + revA + `","max_segment_chars":null}`,
		`{"id":"qwen3_tts","name":"Qwen3","repo":"Qwen/Qwen3-TTS-12Hz-1.7B-Base","revision":"` + revA + `","custom_voice_repo":null}`,
		`{"id":"qwen3_tts","name":"Qwen3","repo":"Qwen/Qwen3-TTS-12Hz-1.7B-Base","revision":"` + revA + `","supports_custom_speakers":null}`,
		`{"id":"qwen3_tts","name":"Qwen3","repo":"Qwen/Qwen3-TTS-12Hz-1.7B-Base","revision":"` + revA + `","MaxSegmentChars":120}`,
	} {
		var spec ColabOneComfyTtsEngineSpec
		if err := unmarshalAndValidateSchema(raw, &spec); err == nil {
			t.Fatalf("strict JSON payload %s should fail", raw)
		}
	}
}
