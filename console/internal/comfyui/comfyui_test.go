package comfyui

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func makeTestTunnelToken(t *testing.T, payload map[string]any, urlSafe bool, noPad bool) string {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal tunnel payload: %v", err)
	}
	if urlSafe {
		if noPad {
			return base64.RawURLEncoding.EncodeToString(raw)
		}
		return base64.URLEncoding.EncodeToString(raw)
	}
	if noPad {
		return base64.RawStdEncoding.EncodeToString(raw)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func defaultTestTunnelToken(t *testing.T) string {
	t.Helper()
	return makeTestTunnelToken(t, map[string]any{
		"a": "account",
		"t": "comfy-tunnel",
		"s": "comfy-secret",
	}, false, false)
}

func sampleAPIWorkflow() map[string]any {
	return map[string]any{
		"1": map[string]any{
			"class_type": "LoadImage",
			"_meta":      map[string]any{"title": "参考图"},
			"inputs": map[string]any{
				"image":  "example.png",
				"upload": "image",
			},
		},
		"2": map[string]any{
			"class_type": "LoadAudio",
			"_meta":      map[string]any{"title": "驱动音频"},
			"inputs": map[string]any{
				"audio":   "voice.wav",
				"audioUI": "",
			},
		},
		"3": map[string]any{
			"class_type": "CLIPTextEncode",
			"_meta":      map[string]any{"title": "Positive Prompt"},
			"inputs": map[string]any{
				"text": "a sunlit coastal studio",
				"clip": []any{"10", 1},
			},
		},
		"4": map[string]any{
			"class_type": "CLIPTextEncode",
			"_meta":      map[string]any{"title": "Negative Prompt"},
			"inputs": map[string]any{
				"text": "blurry, watermark",
				"clip": []any{"10", 1},
			},
		},
		"5": map[string]any{
			"class_type": "EmptyLatentImage",
			"_meta":      map[string]any{"title": "尺寸"},
			"inputs": map[string]any{
				"width":      768,
				"height":     512,
				"batch_size": 1,
			},
		},
		"6": map[string]any{
			"class_type": "KSampler",
			"_meta":      map[string]any{"title": "采样器"},
			"inputs": map[string]any{
				"seed":         42,
				"steps":        20,
				"cfg":          6.5,
				"model":        []any{"10", 0},
				"positive":     []any{"3", 0},
				"negative":     []any{"4", 0},
				"latent_image": []any{"5", 0},
			},
		},
	}
}

func roundTripWorkflowJSON(t *testing.T, wf map[string]any) map[string]any {
	t.Helper()
	b, err := json.Marshal(wf)
	if err != nil {
		t.Fatalf("marshal workflow: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal workflow: %v", err)
	}
	return out
}

func TestExportedConstants(t *testing.T) {
	if MaxWorkflowBytes != 2*1024*1024 {
		t.Fatalf("MaxWorkflowBytes = %d, want 2 MiB", MaxWorkflowBytes)
	}
	if MaxOutputBytes != 512*1024*1024 {
		t.Fatalf("MaxOutputBytes = %d, want 512 MiB", MaxOutputBytes)
	}
	if MaxImageInputBytes != 16*1024*1024 {
		t.Fatalf("MaxImageInputBytes = %d, want 16 MiB", MaxImageInputBytes)
	}
	if MaxAudioInputBytes != 64*1024*1024 {
		t.Fatalf("MaxAudioInputBytes = %d, want 64 MiB", MaxAudioInputBytes)
	}
	if len(AllowedOutputMIME) != 10 {
		t.Fatalf("len(AllowedOutputMIME) = %d, want 10", len(AllowedOutputMIME))
	}
	if got := AllowedOutputMIME["mp4"]; got.Kind != "video" || got.MIME != "video/mp4" {
		t.Fatalf("AllowedOutputMIME[mp4] = %+v", got)
	}
	if got := AllowedOutputMIME["flac"]; got.Kind != "audio" || got.MIME != "audio/flac" {
		t.Fatalf("AllowedOutputMIME[flac] = %+v", got)
	}
	if len(AllowedInputExtensions) != 9 {
		t.Fatalf("len(AllowedInputExtensions) = %d, want 9", len(AllowedInputExtensions))
	}
	for _, ext := range []string{"png", "jpg", "jpeg", "webp", "wav", "mp3", "flac", "ogg", "m4a"} {
		if !AllowedInputExtensions[ext] {
			t.Fatalf("AllowedInputExtensions missing %q", ext)
		}
	}
}

func TestValidateComfyAPIWorkflowAcceptsAPIAndRejectsCanvasOrMalformed(t *testing.T) {
	wf := sampleAPIWorkflow()
	wf["6"].(map[string]any)["class_type"] = "  KSampler  "

	normalized, err := ValidateComfyAPIWorkflow(wf)
	if err != nil {
		t.Fatalf("ValidateComfyAPIWorkflow failed: %v", err)
	}
	if normalized["6"]["class_type"] != "KSampler" {
		t.Fatalf("expected trimmed class_type KSampler, got %v", normalized["6"]["class_type"])
	}
	// Deep copy: mutating normalized must not mutate input.
	normalized["3"]["inputs"].(map[string]any)["text"] = "mutated"
	normalized["3"]["inputs"].(map[string]any)["clip"].([]any)[0] = "999"
	if wf["3"].(map[string]any)["inputs"].(map[string]any)["text"] != "a sunlit coastal studio" {
		t.Fatalf("input workflow text was mutated")
	}
	if wf["3"].(map[string]any)["inputs"].(map[string]any)["clip"].([]any)[0] != "10" {
		t.Fatalf("input workflow clip slice was mutated")
	}

	// Canvas format with nodes + links or nodes + version is rejected with helpful Chinese hint.
	canvasCases := []map[string]any{
		{"nodes": []any{map[string]any{"id": 1, "type": "KSampler"}}, "links": []any{}},
		{"nodes": []any{}, "version": 0.4},
		{"nodes": []map[string]any{{"id": 1}}, "links": []any{}},
	}
	for i, canvasJSON := range canvasCases {
		_, err := ValidateComfyAPIWorkflow(canvasJSON)
		if err == nil {
			t.Fatalf("canvas case %d: expected error", i)
		}
		msg := err.Error()
		if !strings.Contains(msg, "画布") || !strings.Contains(msg, "Save (API Format)") {
			t.Fatalf("canvas case %d: unexpected error message: %q", i, msg)
		}
	}

	// Malformed cases.
	badCases := []struct {
		name    string
		wf      map[string]any
		wantSub string
	}{
		{"nil", nil, "工作流必须为 JSON 对象"},
		{"empty", map[string]any{}, "工作流不能为空"},
		{"traversalNodeID", map[string]any{"../evil": map[string]any{"class_type": "KSampler", "inputs": map[string]any{}}}, "非法节点 ID"},
		{"overlongNodeID", map[string]any{strings.Repeat("a", 81): map[string]any{"class_type": "KSampler", "inputs": map[string]any{}}}, "非法节点 ID"},
		{"nonObjectNode", map[string]any{"1": "not-an-object"}, "节点 1 必须为对象"},
		{"nilNode", map[string]any{"1": nil}, "节点 1 必须为对象"},
		{"missingClassType", map[string]any{"1": map[string]any{"inputs": map[string]any{}}}, "缺少有效的 class_type"},
		{"blankClassType", map[string]any{"1": map[string]any{"class_type": "   ", "inputs": map[string]any{}}}, "缺少有效的 class_type"},
		{"overlongClassType", map[string]any{"1": map[string]any{"class_type": strings.Repeat("x", 161), "inputs": map[string]any{}}}, "缺少有效的 class_type"},
		{"inputsList", map[string]any{"1": map[string]any{"class_type": "KSampler", "inputs": []any{}}}, "缺少有效的 inputs 对象"},
		{"inputsNil", map[string]any{"1": map[string]any{"class_type": "KSampler", "inputs": nil}}, "缺少有效的 inputs 对象"},
		{"emptyInputKey", map[string]any{"1": map[string]any{"class_type": "KSampler", "inputs": map[string]any{"": 1}}}, "包含非法输入名称"},
		{"nanValue", map[string]any{"1": map[string]any{"class_type": "KSampler", "inputs": map[string]any{"cfg": math.NaN()}}}, "工作流包含无法序列化的内容"},
		{"posInfValue", map[string]any{"1": map[string]any{"class_type": "KSampler", "inputs": map[string]any{"cfg": math.Inf(1)}}}, "工作流包含无法序列化的内容"},
		{"negInfValue", map[string]any{"1": map[string]any{"class_type": "KSampler", "inputs": map[string]any{"cfg": math.Inf(-1)}}}, "工作流包含无法序列化的内容"},
		{"unserializableFunc", map[string]any{"1": map[string]any{"class_type": "KSampler", "inputs": map[string]any{"fn": func() {}}}}, "工作流包含无法序列化的内容"},
	}
	for _, tc := range badCases {
		_, err := ValidateComfyAPIWorkflow(tc.wf)
		if err == nil {
			t.Fatalf("%s: expected error", tc.name)
		}
		if !strings.Contains(err.Error(), tc.wantSub) {
			t.Fatalf("%s: error %q does not contain %q", tc.name, err.Error(), tc.wantSub)
		}
	}

	// Max node count boundary: 500 accepted, 501 rejected.
	wf500 := make(map[string]any, 500)
	for i := 1; i <= 500; i++ {
		wf500[fmt.Sprintf("%d", i)] = map[string]any{"class_type": "KSampler", "inputs": map[string]any{}}
	}
	if _, err := ValidateComfyAPIWorkflow(wf500); err != nil {
		t.Fatalf("500 nodes should be accepted, got %v", err)
	}
	wf500["501"] = map[string]any{"class_type": "KSampler", "inputs": map[string]any{}}
	if _, err := ValidateComfyAPIWorkflow(wf500); err == nil || !strings.Contains(err.Error(), "不能超过 500 个") {
		t.Fatalf("501 nodes should fail with node limit error, got %v", err)
	}

	// Max serialized size boundary (> 2 MiB rejected).
	oversized := map[string]any{
		"1": map[string]any{
			"class_type": "CLIPTextEncode",
			"inputs": map[string]any{
				"text": strings.Repeat("a", MaxWorkflowBytes+10),
			},
		},
	}
	if _, err := ValidateComfyAPIWorkflow(oversized); err == nil || !strings.Contains(err.Error(), "最大 2 MiB") {
		t.Fatalf("oversized workflow should fail with size error, got %v", err)
	}
}

func TestExtractWorkflowFieldsSkipsLinksAndExtractsPromptsNumbersAndMedia(t *testing.T) {
	// Test both Go-native map and JSON-roundtripped map (where JSON numbers become float64).
	for _, tc := range []struct {
		name string
		wf   map[string]any
	}{
		{"goMap", sampleAPIWorkflow()},
		{"jsonRoundTrip", roundTripWorkflowJSON(t, sampleAPIWorkflow())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := ExtractWorkflowFields(tc.wf)
			byKey := make(map[string]ColabOneWorkflowField, len(fields))
			for _, f := range fields {
				byKey[f.Key] = f
				if err := f.Validate(); err != nil {
					t.Fatalf("extracted field %q failed Validate: %v", f.Key, err)
				}
			}

			if byKey["1.image"].FieldType != "image" || byKey["1.image"].Label != "参考图 · 参考图片" {
				t.Fatalf("1.image = %+v", byKey["1.image"])
			}
			if byKey["2.audio"].FieldType != "audio" || byKey["2.audio"].Label != "驱动音频 · 输入音频" {
				t.Fatalf("2.audio = %+v", byKey["2.audio"])
			}
			if byKey["3.text"].FieldType != "textarea" || !strings.Contains(byKey["3.text"].Label, "正向提示词") {
				t.Fatalf("3.text = %+v", byKey["3.text"])
			}
			if byKey["4.text"].FieldType != "textarea" || !strings.Contains(byKey["4.text"].Label, "反向提示词") {
				t.Fatalf("4.text = %+v", byKey["4.text"])
			}
			if byKey["5.width"].FieldType != "integer" || byKey["5.width"].Default != int64(768) {
				t.Fatalf("5.width = %+v", byKey["5.width"])
			}
			if byKey["5.height"].FieldType != "integer" || byKey["5.height"].Default != int64(512) {
				t.Fatalf("5.height = %+v", byKey["5.height"])
			}
			if byKey["5.batch_size"].FieldType != "integer" || byKey["5.batch_size"].Default != int64(1) {
				t.Fatalf("5.batch_size = %+v", byKey["5.batch_size"])
			}
			if byKey["6.seed"].FieldType != "integer" || byKey["6.seed"].Default != int64(42) {
				t.Fatalf("6.seed = %+v", byKey["6.seed"])
			}
			if byKey["6.steps"].FieldType != "integer" || byKey["6.steps"].Default != int64(20) {
				t.Fatalf("6.steps = %+v", byKey["6.steps"])
			}
			if byKey["6.cfg"].FieldType != "number" || byKey["6.cfg"].Default != float64(6.5) {
				t.Fatalf("6.cfg = %+v", byKey["6.cfg"])
			}

			// Exact key ordering across nodes and priorities.
			wantOrder := []string{
				"1.image",
				"2.audio",
				"3.text",
				"4.text",
				"5.width",
				"5.height",
				"5.batch_size",
				"6.steps",
				"6.cfg",
				"6.seed",
			}
			gotOrder := make([]string, len(fields))
			for i, f := range fields {
				gotOrder[i] = f.Key
			}
			if !reflect.DeepEqual(gotOrder, wantOrder) {
				t.Fatalf("field order = %v, want %v", gotOrder, wantOrder)
			}

			// Linked node inputs and internal upload UI helper fields must never be extracted.
			for _, skipped := range []string{"1.upload", "2.audioUI", "3.clip", "6.model", "6.positive", "6.latent_image"} {
				if _, exists := byKey[skipped]; exists {
					t.Fatalf("field %q should have been skipped", skipped)
				}
			}
		})
	}
}

func TestExtractWorkflowFieldsOrderingPrioritiesLimitsAndEdgeCases(t *testing.T) {
	if got := ExtractWorkflowFields(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil workflow should return empty non-nil slice, got %#v", got)
	}

	// Verify numeric node sort (2 < 10, 01 < 1, huge numeric < string "a" < "b")
	// and full priority order within a single node.
	wf := map[string]any{
		"b": map[string]any{
			"class_type": "PlainNode",
			"inputs":     map[string]any{"text": "from-b"},
		},
		"a": map[string]any{
			"class_type": "PlainNode",
			"inputs":     map[string]any{"text": "from-a"},
		},
		"10": map[string]any{
			"class_type": "LoadImageLoadAudioText",
			"_meta":      map[string]any{"title": "全能节点"},
			"inputs": map[string]any{
				"seed":            int64(9007199254740993), // > 2^53 precision test
				"noise_seed":      100,
				"denoise":         0.75,
				"cfg":             7.0,
				"steps":           25,
				"batch_size":      2,
				"fps":             24,
				"length":          49,
				"frame_count":     33,
				"height":          512,
				"width":           768,
				"negative_prompt": "bad hands",
				"string":          "plain note",
				"positive_prompt": "masterpiece",
				"audio_file":      "clip.wav",
				"image_path":      "ref.png",
				"width_bool":      true,
				"bool_steps":      true,
			},
		},
		"2": map[string]any{
			"class_type": "TextNode",
			"_meta":      map[string]any{"title": "反向描述"},
			"inputs":     map[string]any{"text": "ugly"},
		},
		"01": map[string]any{
			"class_type": "TextNode",
			"_meta":      map[string]any{"title": "  "},
			"inputs":     map[string]any{"text": "zero-one"},
		},
		"1": map[string]any{
			"class_type": "TextNode",
			"_meta":      map[string]any{"title": "正向描述"},
			"inputs":     map[string]any{"text": "one"},
		},
	}

	fields := ExtractWorkflowFields(wf)
	gotKeys := make([]string, len(fields))
	for i, f := range fields {
		gotKeys[i] = f.Key
	}
	wantKeys := []string{
		"01.text",
		"1.text",
		"2.text",
		"10.image_path",
		"10.audio_file",
		"10.positive_prompt",
		"10.string",
		"10.negative_prompt",
		"10.width",
		"10.height",
		"10.frame_count",
		"10.length",
		"10.fps",
		"10.batch_size",
		"10.steps",
		"10.cfg",
		"10.denoise",
		"10.noise_seed",
		"10.seed",
		"a.text",
		"b.text",
	}
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("keys = %v\nwant = %v", gotKeys, wantKeys)
	}

	byKey := make(map[string]ColabOneWorkflowField, len(fields))
	for _, f := range fields {
		byKey[f.Key] = f
	}
	if byKey["01.text"].Label != "TextNode · 文本输入" {
		t.Fatalf("01.text label = %q", byKey["01.text"].Label)
	}
	if byKey["1.text"].Label != "正向描述 · 正向提示词" {
		t.Fatalf("1.text label = %q", byKey["1.text"].Label)
	}
	if byKey["2.text"].Label != "反向描述 · 反向提示词" {
		t.Fatalf("2.text label = %q", byKey["2.text"].Label)
	}
	if byKey["10.seed"].Default != int64(9007199254740993) {
		t.Fatalf("10.seed lost 64-bit precision: %v", byKey["10.seed"].Default)
	}
	if byKey["10.denoise"].Step == nil || *byKey["10.denoise"].Step != 0.05 {
		t.Fatalf("10.denoise step = %v", byKey["10.denoise"].Step)
	}
	if byKey["10.fps"].Label != "全能节点 · 帧率 (FPS)" || byKey["10.fps"].Default != float64(24) {
		t.Fatalf("10.fps = %+v", byKey["10.fps"])
	}

	// Mutating returned MinValue pointer must not affect future extractions.
	*byKey["10.width"].MinValue = 9999
	fields2 := ExtractWorkflowFields(wf)
	for _, f := range fields2 {
		if f.Key == "10.width" && (f.MinValue == nil || *f.MinValue != 64) {
			t.Fatalf("global numericSpec MinValue was mutated: %v", f.MinValue)
		}
	}

	// Max 32 auto fields cap + UTF-8 rune-safe label truncation + bool/NaN skipped.
	many := make(map[string]any, 40)
	for i := 1; i <= 40; i++ {
		many[fmt.Sprintf("%d", i)] = map[string]any{
			"class_type": "CLIPTextEncode",
			"_meta":      map[string]any{"title": strings.Repeat("中", 170)},
			"inputs": map[string]any{
				"text":  "hello",
				"steps": true,       // bool ignored
				"cfg":   math.NaN(), // NaN ignored
			},
		}
	}
	capped := ExtractWorkflowFields(many)
	if len(capped) != 32 {
		t.Fatalf("len(capped) = %d, want 32", len(capped))
	}
	if len([]rune(capped[0].Label)) != 160 {
		t.Fatalf("truncated label rune count = %d, want 160", len([]rune(capped[0].Label)))
	}
}

func TestApplyWorkflowOverridesDoesNotMutateOriginalAndProtectsLinks(t *testing.T) {
	for _, tc := range []struct {
		name string
		wf   map[string]any
	}{
		{"goMap", sampleAPIWorkflow()},
		{"jsonRoundTrip", roundTripWorkflowJSON(t, sampleAPIWorkflow())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := tc.wf
			beforeBytes, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("marshal before: %v", err)
			}

			updated, err := ApplyWorkflowOverrides(
				original,
				map[string]any{
					"3.text":      "cyberpunk harbor at dusk",
					"6.seed":      999,
					"6.steps":     30,
					"6.cfg":       7.5,
					"6.positive":  "should-not-overwrite-link",
					"999.missing": "ignored",
					"no_dot":      "ignored",
				},
				map[string]string{
					"1.image": "uploaded_ref.png",
					"2.audio": "uploaded_voice.wav",
					"3.clip":  "must-not-overwrite-link.png",
					"4.text":  "", // empty media filename ignored
				},
			)
			if err != nil {
				t.Fatalf("ApplyWorkflowOverrides failed: %v", err)
			}

			afterBytes, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("marshal after: %v", err)
			}
			if string(beforeBytes) != string(afterBytes) {
				t.Fatalf("original workflow mutated:\nbefore: %s\nafter:  %s", beforeBytes, afterBytes)
			}

			in3 := updated["3"].(map[string]any)["inputs"].(map[string]any)
			in6 := updated["6"].(map[string]any)["inputs"].(map[string]any)
			in1 := updated["1"].(map[string]any)["inputs"].(map[string]any)
			in2 := updated["2"].(map[string]any)["inputs"].(map[string]any)
			in4 := updated["4"].(map[string]any)["inputs"].(map[string]any)

			if in3["text"] != "cyberpunk harbor at dusk" {
				t.Fatalf("3.text = %v", in3["text"])
			}
			if in6["seed"] != 999 {
				t.Fatalf("6.seed = %v", in6["seed"])
			}
			if in6["steps"] != 30 {
				t.Fatalf("6.steps = %v", in6["steps"])
			}
			if in6["cfg"] != 7.5 {
				t.Fatalf("6.cfg = %v", in6["cfg"])
			}
			if !isNodeLink(in6["positive"]) {
				t.Fatalf("6.positive link overwritten: %v", in6["positive"])
			}
			if in1["image"] != "uploaded_ref.png" {
				t.Fatalf("1.image = %v", in1["image"])
			}
			if in2["audio"] != "uploaded_voice.wav" {
				t.Fatalf("2.audio = %v", in2["audio"])
			}
			if !isNodeLink(in3["clip"]) {
				t.Fatalf("3.clip link overwritten: %v", in3["clip"])
			}
			if in4["text"] != "blurry, watermark" {
				t.Fatalf("4.text unexpectedly changed to %v", in4["text"])
			}

			for _, badFloat := range []any{math.NaN(), math.Inf(1), math.Inf(-1), float32(math.NaN())} {
				_, err := ApplyWorkflowOverrides(original, map[string]any{"6.cfg": badFloat}, nil)
				if err == nil || !strings.Contains(err.Error(), "字段 6.cfg 包含非法浮点值") {
					t.Fatalf("bad float %v: expected error, got %v", badFloat, err)
				}
			}
		})
	}
}

func TestComfyCatalogFileAndValidation(t *testing.T) {
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
	if catalog.Port != 8188 || catalog.ProxyPort != 8189 || catalog.Port == catalog.ProxyPort {
		t.Fatalf("unexpected catalog ports: port=%d proxy_port=%d", catalog.Port, catalog.ProxyPort)
	}

	// Real catalog/comfyui.json round-trips cleanly whether templates / tts_engines are empty or populated.
	catalogWire := marshalJSONMap(t, &catalog)
	templatesWire, ok := catalogWire["templates"].([]any)
	if !ok {
		t.Fatalf("catalog templates should marshal as JSON array, got %#v", catalogWire["templates"])
	}
	seenTplIDs := make(map[string]struct{}, len(catalog.Templates))
	for i, tpl := range catalog.Templates {
		if tpl.ID == "" {
			t.Fatalf("catalog template[%d] has empty ID", i)
		}
		if _, dup := seenTplIDs[tpl.ID]; dup {
			t.Fatalf("catalog template[%d] duplicate ID %q", i, tpl.ID)
		}
		seenTplIDs[tpl.ID] = struct{}{}
		if tpl.NodeCount <= 0 {
			t.Fatalf("catalog template %q has invalid node_count=%d", tpl.ID, tpl.NodeCount)
		}
		if i < len(templatesWire) {
			if tplObj, ok := templatesWire[i].(map[string]any); ok {
				if rawPipe, hasPipe := tplObj["pipeline"]; hasPipe && rawPipe != nil {
					pipeObj, ok := rawPipe.(map[string]any)
					if !ok {
						t.Fatalf("catalog template %q pipeline should be object or null, got %#v", tpl.ID, rawPipe)
					}
					kind, _ := pipeObj["kind"].(string)
					if kind != "comfy_standard" && kind != "digital_human" {
						t.Fatalf("catalog template %q has invalid pipeline.kind=%q", tpl.ID, kind)
					}
				}
			}
		}
	}
	if rawEngines, exists := catalogWire["tts_engines"]; exists {
		enginesWire, ok := rawEngines.([]any)
		if !ok {
			t.Fatalf("catalog tts_engines should marshal as JSON array, got %#v", rawEngines)
		}
		seenEngineIDs := make(map[string]struct{}, len(enginesWire))
		for i, item := range enginesWire {
			eng, ok := item.(map[string]any)
			if !ok {
				t.Fatalf("catalog tts_engines[%d] should be JSON object, got %#v", i, item)
			}
			id, _ := eng["id"].(string)
			name, _ := eng["name"].(string)
			repo, _ := eng["repo"].(string)
			rev, _ := eng["revision"].(string)
			sr, _ := eng["sample_rate"].(float64)
			if id == "" || name == "" || repo == "" || rev == "" {
				t.Fatalf("catalog tts_engines[%d] missing required metadata: %#v", i, eng)
			}
			if _, dup := seenEngineIDs[id]; dup {
				t.Fatalf("catalog tts_engines[%d] duplicate ID %q", i, id)
			}
			seenEngineIDs[id] = struct{}{}
			if sr < 8000 || sr > 96000 {
				t.Fatalf("catalog tts_engines[%d] (%s) sample_rate=%v out of [8000, 96000]", i, id, sr)
			}
		}
	}
	roundTripRaw := mustMarshalJSON(t, &catalog)
	var roundTripCat ColabOneComfyCatalog
	if err := unmarshalAndValidateSchema(roundTripRaw, &roundTripCat); err != nil {
		t.Fatalf("round-trip catalog/comfyui.json failed: %v", err)
	}

	// Same port & proxy_port must fail.
	badSamePort := catalog
	badSamePort.ProxyPort = catalog.Port
	if err := badSamePort.Validate(); err == nil || err.Error() != "ComfyUI 端口与代理端口不能相同" {
		t.Fatalf("same port error = %v", err)
	}

	// Omitted ports default to 8188 / 8189, explicit 0 rejected.
	var omittedPorts ColabOneComfyCatalog
	if err := json.Unmarshal([]byte(`{"repo":"Comfy-Org/ComfyUI","tag":"v0.46.0","commit":"`+strings.Repeat("a", 40)+`"}`), &omittedPorts); err != nil {
		t.Fatalf("unmarshal omitted ports: %v", err)
	}
	if err := omittedPorts.Validate(); err != nil {
		t.Fatalf("validate omitted ports: %v", err)
	}
	if omittedPorts.Port != 8188 || omittedPorts.ProxyPort != 8189 {
		t.Fatalf("omitted ports = %d/%d, want 8188/8189", omittedPorts.Port, omittedPorts.ProxyPort)
	}
	omittedWire := marshalJSONMap(t, &omittedPorts)
	if rawEngines, exists := omittedWire["tts_engines"]; exists {
		if _, ok := rawEngines.([]any); !ok {
			t.Fatalf("omitted tts_engines should marshal as [] when present, got %#v", rawEngines)
		}
	}
	var explicitZero ColabOneComfyCatalog
	if err := json.Unmarshal([]byte(`{"repo":"Comfy-Org/ComfyUI","tag":"v0.46.0","commit":"`+strings.Repeat("a", 40)+`","port":0}`), &explicitZero); err == nil {
		t.Fatalf("explicit port=0 should fail unmarshal")
	}

	// Duplicate template IDs rejected.
	dupCat := catalog
	dupCat.Templates = []ColabOneWorkflowTemplate{
		{ID: "dup", Name: "Flow 1", Workflow: sampleAPIWorkflow()},
		{ID: "dup", Name: "Flow 2", Workflow: sampleAPIWorkflow()},
	}
	if err := dupCat.Validate(); err == nil || err.Error() != "内置工作流模板 ID 重复" {
		t.Fatalf("duplicate template ID error = %v", err)
	}

	// Valid distinct tts_engines accepted; duplicate IDs, invalid entry, and null rejected.
	makeEngine := func(id string) map[string]any {
		return map[string]any{
			"id":       id,
			"name":     "TTS " + id,
			"repo":     "openbmb/VoxCPM2",
			"revision": strings.Repeat("a", 40),
		}
	}
	validTTSCatalogRaw := mustMarshalJSON(t, map[string]any{
		"repo":   "Comfy-Org/ComfyUI",
		"tag":    "v0.46.0",
		"commit": strings.Repeat("a", 40),
		"tts_engines": []map[string]any{
			makeEngine("voxcpm2"),
			{
				"id":           "qwen3_tts",
				"name":         "Qwen3-TTS",
				"repo":         "Qwen/Qwen3-TTS-12Hz-1.7B-Base",
				"revision":     strings.Repeat("b", 40),
				"aux_repo":     "Qwen/Qwen3-TTS-12Hz-1.7B-VoiceDesign",
				"aux_revision": strings.Repeat("c", 40),
				"sample_rate":  24000,
			},
			{
				"id":             "fish_s2_pro",
				"name":           "Fish S2 Pro",
				"repo":           "fishaudio/s2-pro",
				"revision":       strings.Repeat("d", 40),
				"commercial_ok":  false,
				"license":        "Fish Audio Research License",
				"license_notice": "Non-commercial research only",
				"sample_rate":    44100,
			},
		},
	})
	var validTTSCatalog ColabOneComfyCatalog
	if err := unmarshalAndValidateSchema(validTTSCatalogRaw, &validTTSCatalog); err != nil {
		t.Fatalf("valid catalog with tts_engines failed: %v", err)
	}

	dupTTSCatalogRaw := mustMarshalJSON(t, map[string]any{
		"repo":   "Comfy-Org/ComfyUI",
		"tag":    "v0.46.0",
		"commit": strings.Repeat("a", 40),
		"tts_engines": []map[string]any{
			makeEngine("voxcpm2"),
			makeEngine("voxcpm2"),
		},
	})
	var dupTTSCatalog ColabOneComfyCatalog
	if err := unmarshalAndValidateSchema(dupTTSCatalogRaw, &dupTTSCatalog); err == nil {
		t.Fatalf("duplicate tts_engines IDs should fail validation")
	}

	invalidTTSCatalogRaw := mustMarshalJSON(t, map[string]any{
		"repo":   "Comfy-Org/ComfyUI",
		"tag":    "v0.46.0",
		"commit": strings.Repeat("a", 40),
		"tts_engines": []map[string]any{
			{
				"id":       "broken_tts",
				"name":     "Broken TTS",
				"repo":     "openbmb/VoxCPM2",
				"revision": strings.Repeat("a", 40),
				"aux_repo": "Qwen/Qwen3-TTS-12Hz-1.7B-VoiceDesign",
			},
		},
	})
	var invalidTTSCatalog ColabOneComfyCatalog
	if err := unmarshalAndValidateSchema(invalidTTSCatalogRaw, &invalidTTSCatalog); err == nil {
		t.Fatalf("catalog with invalid tts_engines entry should fail validation")
	}

	var nullTTSCatalog ColabOneComfyCatalog
	if err := json.Unmarshal([]byte(`{"repo":"Comfy-Org/ComfyUI","tag":"v0.46.0","commit":"`+strings.Repeat("a", 40)+`","tts_engines":null}`), &nullTTSCatalog); err == nil {
		t.Fatalf("explicit tts_engines: null should fail unmarshal")
	}

	// Extra unknown field rejected.
	var extraCat ColabOneComfyCatalog
	if err := json.Unmarshal([]byte(`{"repo":"Comfy-Org/ComfyUI","tag":"v0.46.0","commit":"`+strings.Repeat("a", 40)+`","extra":1}`), &extraCat); err == nil {
		t.Fatalf("unknown field in catalog should fail")
	}
}

func TestComfyNodeSpecAndModelRefValidation(t *testing.T) {
	// NodeSpec JSON defaults install_requirements to true, respects explicit false.
	var spec ColabOneComfyNodeSpec
	rawSpec := `{"name":"  ComfyUI-TestNode  ","repo":"https://github.com/example/ComfyUI-TestNode.git","commit":"` + strings.Repeat("a", 40) + `"}`
	if err := json.Unmarshal([]byte(rawSpec), &spec); err != nil {
		t.Fatalf("unmarshal node spec: %v", err)
	}
	if !spec.InstallRequirements {
		t.Fatalf("InstallRequirements should default to true when omitted")
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("validate node spec: %v", err)
	}
	if spec.Name != "ComfyUI-TestNode" {
		t.Fatalf("trimmed name = %q", spec.Name)
	}

	var specFalse ColabOneComfyNodeSpec
	rawSpecFalse := `{"name":"Node","repo":"owner/repo","commit":"` + strings.Repeat("b", 40) + `","install_requirements":false}`
	if err := json.Unmarshal([]byte(rawSpecFalse), &specFalse); err != nil {
		t.Fatalf("unmarshal node spec false: %v", err)
	}
	if specFalse.InstallRequirements {
		t.Fatalf("InstallRequirements explicit false should stay false")
	}
	if err := specFalse.Validate(); err != nil {
		t.Fatalf("validate node spec false: %v", err)
	}

	for _, badName := range []string{"", "   ", ".", "..", "a..b", "a/b", `a\b`, "a:b", "a\x00b"} {
		bad := ColabOneComfyNodeSpec{Name: badName, Repo: "owner/repo", Commit: strings.Repeat("a", 40)}
		if err := bad.Validate(); err == nil || err.Error() != "自定义节点名称不合法" {
			t.Fatalf("badName %q: err = %v", badName, err)
		}
	}
	for _, badRepo := range []string{"", "http://github.com/a/b", "https://github.com/../repo", "https://github.com/owner/..", "https://github.com/owner/...git", "../repo"} {
		bad := ColabOneComfyNodeSpec{Name: "Node", Repo: badRepo, Commit: strings.Repeat("a", 40)}
		if err := bad.Validate(); err == nil {
			t.Fatalf("badRepo %q: expected error", badRepo)
		}
	}

	// ModelRef validation & sha256 lowercasing.
	m := ColabOneComfyModelRef{
		Repo:      "org/comfy-weights",
		Revision:  strings.Repeat("B", 40),
		Path:      "flux/model.SAFETENSORS",
		TargetDir: "diffusion_models",
		Size:      1024,
		SHA256:    strings.Repeat("C", 64),
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("valid model ref failed: %v", err)
	}
	if m.SHA256 != strings.Repeat("c", 64) {
		t.Fatalf("SHA256 not lowercased: %q", m.SHA256)
	}

	for _, badPath := range []string{
		"",
		"/abs/model.safetensors",
		"../model.safetensors",
		"flux/../model.safetensors",
		"./model.safetensors",
		"flux/./model.safetensors",
		"flux/model.safetensors/",
		"C:model.safetensors",
		`flux\model.safetensors`,
		"flux/model\x00.safetensors",
		"flux/.safetensors",
		"flux/model.txt",
		strings.Repeat("a", 501) + ".safetensors",
	} {
		bad := m
		bad.Path = badPath
		if err := bad.Validate(); err == nil || err.Error() != "模型文件路径不合法" {
			t.Fatalf("badPath %q: err = %v", badPath, err)
		}
	}
	badSize := m
	badSize.Size = 0
	if err := badSize.Validate(); err == nil {
		t.Fatalf("size=0 should fail")
	}
	badDir := m
	badDir.TargetDir = "invalid_dir"
	if err := badDir.Validate(); err == nil {
		t.Fatalf("invalid target_dir should fail")
	}
}

func TestWorkflowTemplateImportRunOutputAndCanvasConfig(t *testing.T) {
	// WorkflowTemplate auto-populates node_count & extracts fields if empty.
	tmpl := ColabOneWorkflowTemplate{
		ID:          "wf_demo-1",
		Name:        "  Demo Studio Flow  ",
		Description: "Text + Image + Audio",
		Source:      "builtin",
		Workflow:    sampleAPIWorkflow(),
		PipPackages: []string{"soundfile>=0.12", "  einops  "},
		CustomNodes: []ColabOneComfyNodeSpec{
			{
				Name:                "ComfyUI-TestNode",
				Repo:                "https://github.com/example/ComfyUI-TestNode.git",
				Commit:              strings.Repeat("a", 40),
				InstallRequirements: true,
			},
		},
		Models: []ColabOneComfyModelRef{
			{
				Repo:      "org/comfy-weights",
				Revision:  strings.Repeat("b", 40),
				Path:      "flux/model.safetensors",
				TargetDir: "diffusion_models",
				Size:      1024,
				SHA256:    strings.Repeat("c", 64),
			},
		},
	}
	if err := tmpl.Validate(); err != nil {
		t.Fatalf("valid builtin template failed: %v", err)
	}
	if tmpl.Name != "Demo Studio Flow" {
		t.Fatalf("trimmed template name = %q", tmpl.Name)
	}
	if tmpl.Source != "builtin" {
		t.Fatalf("builtin source = %q, want builtin", tmpl.Source)
	}
	if tmpl.NodeCount != len(sampleAPIWorkflow()) {
		t.Fatalf("node_count = %d, want %d", tmpl.NodeCount, len(sampleAPIWorkflow()))
	}
	if len(tmpl.Fields) < 8 {
		t.Fatalf("expected >= 8 extracted fields, got %d", len(tmpl.Fields))
	}
	if tmpl.PipPackages[1] != "einops" {
		t.Fatalf("pip package not trimmed: %q", tmpl.PipPackages[1])
	}

	// Clean custom template defaults Source to "custom".
	customTmpl := ColabOneWorkflowTemplate{
		ID:       "wf_custom-clean",
		Name:     "  Clean Custom Flow  ",
		Workflow: sampleAPIWorkflow(),
	}
	if err := customTmpl.Validate(); err != nil {
		t.Fatalf("clean custom template failed: %v", err)
	}
	if customTmpl.Name != "Clean Custom Flow" {
		t.Fatalf("trimmed custom template name = %q", customTmpl.Name)
	}
	if customTmpl.Source != "custom" {
		t.Fatalf("default source = %q, want custom", customTmpl.Source)
	}
	if customTmpl.NodeCount != len(sampleAPIWorkflow()) {
		t.Fatalf("custom node_count = %d, want %d", customTmpl.NodeCount, len(sampleAPIWorkflow()))
	}

	// Bad pip package rejected.
	badPip := ColabOneWorkflowTemplate{
		ID:          "bad_pip",
		Name:        "Bad pip",
		Source:      "builtin",
		Workflow:    sampleAPIWorkflow(),
		PipPackages: []string{"--extra-index-url https://evil.invalid"},
	}
	if err := badPip.Validate(); err == nil || !strings.Contains(err.Error(), "非法的 pip 依赖声明") {
		t.Fatalf("bad pip error = %v", err)
	}

	// WorkflowField consistency checks.
	badKeyField := ColabOneWorkflowField{
		Key: "1.wrong", Label: "Test", NodeID: "1", InputName: "text", FieldType: "text",
	}
	if err := badKeyField.Validate(); err == nil || err.Error() != "字段 key 必须为 {node_id}.{input_name}" {
		t.Fatalf("bad key field error = %v", err)
	}
	badRangeField := ColabOneWorkflowField{
		Key: "1.cfg", Label: "CFG", NodeID: "1", InputName: "cfg", FieldType: "number",
		MinValue: floatPtr(10), MaxValue: floatPtr(5),
	}
	if err := badRangeField.Validate(); err == nil || err.Error() != "min_value 不能大于 max_value" {
		t.Fatalf("bad range field error = %v", err)
	}
	nanField := ColabOneWorkflowField{
		Key: "1.cfg", Label: "CFG", NodeID: "1", InputName: "cfg", FieldType: "number",
		Default: math.NaN(),
	}
	if err := nanField.Validate(); err == nil {
		t.Fatalf("NaN field default should fail")
	}

	// WorkflowImportRequest.
	imp := ColabOneWorkflowImportRequest{
		Name:     "  My Flow  ",
		Workflow: sampleAPIWorkflow(),
	}
	if err := imp.Validate(); err != nil {
		t.Fatalf("valid import request failed: %v", err)
	}
	if imp.Name != "My Flow" {
		t.Fatalf("trimmed import name = %q", imp.Name)
	}
	blankImp := ColabOneWorkflowImportRequest{Name: "   ", Workflow: sampleAPIWorkflow()}
	if err := blankImp.Validate(); err == nil || err.Error() != "工作流名称不能为空" {
		t.Fatalf("blank import name error = %v", err)
	}

	// WorkflowRunRequest.
	runReq := ColabOneWorkflowRunRequest{
		WorkflowID: "wf_demo-1",
		Overrides:  map[string]any{"3.text": "hello", "6.seed": 7, "6.cfg": 6.5, "6.flag": true},
		Inputs:     map[string]string{"1.image": strings.Repeat("a", 32)},
	}
	if err := runReq.Validate(); err != nil {
		t.Fatalf("valid run request failed: %v", err)
	}
	badRunPayloads := []string{
		`{"workflow_id":"../evil"}`,
		`{"workflow_id":"wf_demo-1","overrides":{"invalid_key_no_dot":1}}`,
		`{"workflow_id":"wf_demo-1","inputs":{"1.image":"../secret"}}`,
		`{"workflow_id":"wf_demo-1","extra_field":true}`,
	}
	for _, raw := range badRunPayloads {
		var r ColabOneWorkflowRunRequest
		err := json.Unmarshal([]byte(raw), &r)
		if err == nil {
			err = r.Validate()
		}
		if err == nil {
			t.Fatalf("bad run payload %s should fail", raw)
		}
	}
	nanRun := ColabOneWorkflowRunRequest{
		WorkflowID: "wf_demo-1",
		Overrides:  map[string]any{"6.cfg": math.NaN()},
	}
	if err := nanRun.Validate(); err == nil {
		t.Fatalf("NaN override in run request should fail")
	}
	tooManyOverrides := make(map[string]any, 129)
	for i := 0; i < 129; i++ {
		tooManyOverrides[fmt.Sprintf("1.k%d", i)] = i
	}
	if err := (&ColabOneWorkflowRunRequest{WorkflowID: "wf_1", Overrides: tooManyOverrides}).Validate(); err == nil || err.Error() != "参数覆盖项数量不能超过 128 个" {
		t.Fatalf("too many overrides error = %v", err)
	}
	tooManyInputs := make(map[string]string, 33)
	for i := 0; i < 33; i++ {
		tooManyInputs[fmt.Sprintf("1.i%d", i)] = strings.Repeat("a", 32)
	}
	if err := (&ColabOneWorkflowRunRequest{WorkflowID: "wf_1", Inputs: tooManyInputs}).Validate(); err == nil || err.Error() != "输入素材数量不能超过 32 个" {
		t.Fatalf("too many inputs error = %v", err)
	}

	// WorkflowOutputRecord.
	rec := ColabOneWorkflowOutputRecord{
		ID:           strings.Repeat("d", 32),
		WorkflowID:   "wf_demo-1",
		WorkflowName: "Demo",
		Kind:         "video",
		Filename:     "clip_0001.mp4",
		Extension:    "mp4",
		MIME:         "video/mp4",
		Size:         128,
		SHA256:       strings.Repeat("e", 64),
		CreatedAt:    1.0,
	}
	if err := rec.Validate(); err != nil {
		t.Fatalf("valid output record failed: %v", err)
	}
	badMime := rec
	badMime.MIME = "image/png"
	if err := badMime.Validate(); err == nil || err.Error() != "输出文件格式与类型不匹配" {
		t.Fatalf("mismatched mime error = %v", err)
	}
	badFilename := rec
	badFilename.Filename = "../clip.mp4"
	if err := badFilename.Validate(); err == nil || err.Error() != "输出文件名不合法" {
		t.Fatalf("traversal filename error = %v", err)
	}
	badUpperHash := rec
	badUpperHash.SHA256 = strings.Repeat("E", 64)
	if err := badUpperHash.Validate(); err == nil {
		t.Fatalf("uppercase sha256 in output record should fail")
	}
	tooBigOutput := rec
	tooBigOutput.Size = MaxOutputBytes + 1
	if err := tooBigOutput.Validate(); err == nil {
		t.Fatalf("oversized output record should fail")
	}

	// ComfyCanvasConfig.
	tok := defaultTestTunnelToken(t)
	urlSafeNoPadTok := makeTestTunnelToken(t, map[string]any{
		"a": "acct+1/2",
		"t": "tun-1_2",
		"s": "sec+ret/==",
	}, true, true)

	for _, bad := range []ColabOneComfyCanvasConfig{
		{Mode: "quick", Domain: "ignored.example.com", TunnelToken: "ignored"},
		{},
		{Domain: "demo.trycloudflare.com", TunnelToken: tok},
		{Domain: "trycloudflare.com", TunnelToken: tok},
		{Mode: "named", Domain: "http://localhost", TunnelToken: tok},
		{Mode: "named", Domain: "https://api.example.com:8443", TunnelToken: tok},
		{Mode: "named", Domain: "https://user@api.example.com", TunnelToken: tok},
		{Mode: "named", Domain: "https://api.example.com/other", TunnelToken: tok},
		{Mode: "named", Domain: "https://api.example.com/%761", TunnelToken: tok},
		{Mode: "named", Domain: "https://api.example.com?x=1", TunnelToken: tok},
		{Mode: "named", Domain: "https://api.example.com#frag", TunnelToken: tok},
		{Mode: "named", Domain: "app.local", TunnelToken: tok},
		{Mode: "named", Domain: "app.internal", TunnelToken: tok},
		{Mode: "named", Domain: "app.test", TunnelToken: tok},
		{Mode: "named", Domain: "app.invalid", TunnelToken: tok},
		{Mode: "named", Domain: "comfy.example.com", TunnelToken: tok + " " + urlSafeNoPadTok},
		{Mode: "named", Domain: "comfy.example.com", TunnelToken: makeTestTunnelToken(t, map[string]any{"a": "only-a"}, false, false)},
	} {
		cfg := bad
		if err := cfg.Validate(); err == nil {
			t.Fatalf("canvas config %+v should fail validation", bad)
		}
	}

	named := ColabOneComfyCanvasConfig{
		Mode:        "named",
		Domain:      "comfy.example.com/",
		TunnelToken: "sudo cloudflared service install " + tok,
	}
	if err := named.Validate(); err != nil {
		t.Fatalf("valid named canvas config failed: %v", err)
	}
	if named.Mode != "named" || named.Domain != "https://comfy.example.com" || named.TunnelToken != tok {
		t.Fatalf("normalized named canvas config = %+v", named)
	}
	// String() must redact TunnelToken.
	if strings.Contains(named.String(), tok) {
		t.Fatalf("String() leaked TunnelToken: %s", named.String())
	}

	// URL-safe unpadded token + /v1/ path + duplicated identical token in pasted text.
	namedURLSafe := ColabOneComfyCanvasConfig{
		Domain:      "https://Comfy.Example.COM/v1/",
		TunnelToken: "install " + urlSafeNoPadTok + " # " + urlSafeNoPadTok,
	}
	if err := namedURLSafe.Validate(); err != nil {
		t.Fatalf("url-safe canvas config failed: %v", err)
	}
	if namedURLSafe.Mode != "named" || namedURLSafe.Domain != "https://comfy.example.com" || namedURLSafe.TunnelToken != urlSafeNoPadTok {
		t.Fatalf("normalized url-safe canvas config = %+v", namedURLSafe)
	}
}

func TestJSONWireParityAndEdgeCases(t *testing.T) {
	// 1. WorkflowImportRequest unmarshaled from canvas JSON gives helpful Chinese hint on Validate().
	var canvasReq ColabOneWorkflowImportRequest
	canvasRaw := `{"name":"Canvas Export","workflow":{"nodes":[{"id":1,"type":"KSampler"}],"links":[]}}`
	if err := json.Unmarshal([]byte(canvasRaw), &canvasReq); err != nil {
		t.Fatalf("unmarshal canvas import request: %v", err)
	}
	if err := canvasReq.Validate(); err == nil || !strings.Contains(err.Error(), "Save (API Format)") {
		t.Fatalf("canvas import request Validate error = %v", err)
	}

	// 2. WorkflowTemplate preserves explicit custom fields & serializes empty slices as [] (never null).
	customField := ColabOneWorkflowField{
		Key:       "3.text",
		Label:     "Custom Prompt",
		NodeID:    "3",
		InputName: "text",
		FieldType: "textarea",
		Default:   "hello",
	}
	tmpl := ColabOneWorkflowTemplate{
		ID:       "wf-custom-fields",
		Name:     "Custom Fields",
		Source:   "builtin",
		Workflow: sampleAPIWorkflow(),
		Fields:   []ColabOneWorkflowField{customField},
	}
	if err := tmpl.Validate(); err != nil {
		t.Fatalf("validate template with explicit fields: %v", err)
	}
	if len(tmpl.Fields) != 1 || tmpl.Fields[0].Label != "Custom Prompt" {
		t.Fatalf("explicit fields were overwritten: %+v", tmpl.Fields)
	}
	if tmpl.Source != "builtin" {
		t.Fatalf("source = %q, want builtin", tmpl.Source)
	}
	wireBytes, err := json.Marshal(tmpl)
	if err != nil {
		t.Fatalf("marshal template: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(wireBytes, &wire); err != nil {
		t.Fatalf("unmarshal wire template: %v", err)
	}
	for _, sliceKey := range []string{"custom_nodes", "pip_packages", "models", "fields"} {
		if _, ok := wire[sliceKey].([]any); !ok {
			t.Fatalf("wire[%q] should be JSON array, got %#v", sliceKey, wire[sliceKey])
		}
	}
	fieldWire := wire["fields"].([]any)[0].(map[string]any)
	if _, ok := fieldWire["options"].([]any); !ok {
		t.Fatalf("field options should be JSON array [], got %#v", fieldWire["options"])
	}

	// 3. Explicit empty source/mode and explicit proxy_port=0 in JSON are rejected.
	var badSrc ColabOneWorkflowTemplate
	if err := json.Unmarshal([]byte(`{"id":"a","name":"a","source":"","workflow":{"1":{"class_type":"X","inputs":{}}}}`), &badSrc); err == nil {
		t.Fatalf("explicit empty source should fail")
	}
	var badMode ColabOneComfyCanvasConfig
	if err := json.Unmarshal([]byte(`{"mode":"","domain":"comfy.example.com","tunnel_token":"x"}`), &badMode); err == nil {
		t.Fatalf("explicit empty mode should fail")
	}
	var badProxyZero ColabOneComfyCatalog
	if err := json.Unmarshal([]byte(`{"repo":"Comfy-Org/ComfyUI","tag":"v0.46.0","commit":"`+strings.Repeat("a", 40)+`","proxy_port":0}`), &badProxyZero); err == nil {
		t.Fatalf("explicit proxy_port=0 should fail")
	}

	// 4. Generic *Load* nodes extract image/audio inputs, and Chinese 负向 / encode classification works.
	wfExtra := map[string]any{
		"1": map[string]any{
			"class_type": "GenericMediaLoader",
			"inputs": map[string]any{
				"image": "a.png",
				"audio": "b.wav",
			},
		},
		"2": map[string]any{
			"class_type": "CustomTextNode",
			"_meta":      map[string]any{"title": "负向条件"},
			"inputs":     map[string]any{"text": "low quality"},
		},
		"3": map[string]any{
			"class_type": "T5Encode",
			"_meta":      map[string]any{"title": "主描述"},
			"inputs":     map[string]any{"prompt": "sunset"},
		},
	}
	fields := ExtractWorkflowFields(wfExtra)
	if len(fields) != 4 {
		t.Fatalf("expected 4 fields, got %d: %+v", len(fields), fields)
	}
	if fields[0].Key != "1.image" || fields[0].FieldType != "image" {
		t.Fatalf("fields[0] = %+v", fields[0])
	}
	if fields[1].Key != "1.audio" || fields[1].FieldType != "audio" {
		t.Fatalf("fields[1] = %+v", fields[1])
	}
	if fields[2].Key != "2.text" || fields[2].Label != "负向条件 · 反向提示词" {
		t.Fatalf("fields[2] = %+v", fields[2])
	}
	if fields[3].Key != "3.prompt" || fields[3].Label != "主描述 · 正向提示词" {
		t.Fatalf("fields[3] = %+v", fields[3])
	}

	// 5. ApplyWorkflowOverrides supports json.Number, bool, and mediaMap precedence over overrides.
	wfSimple := map[string]any{
		"1": map[string]any{
			"class_type": "LoadImage",
			"inputs": map[string]any{
				"image":   "old.png",
				"enabled": false,
				"steps":   10,
				"cfg":     5.0,
				"link":    []int{2, 0},
			},
		},
	}
	out, err := ApplyWorkflowOverrides(
		wfSimple,
		map[string]any{
			"1.image":   "override.png",
			"1.enabled": true,
			"1.steps":   json.Number("25"),
			"1.cfg":     json.Number("7.25"),
			"1.link":    "ignored",
		},
		map[string]string{
			"1.image": "media_wins.png",
		},
	)
	if err != nil {
		t.Fatalf("ApplyWorkflowOverrides failed: %v", err)
	}
	in1 := out["1"].(map[string]any)["inputs"].(map[string]any)
	if in1["image"] != "media_wins.png" || in1["enabled"] != true || in1["steps"] != int64(25) || in1["cfg"] != float64(7.25) {
		t.Fatalf("unexpected overridden inputs: %+v", in1)
	}
	if !reflect.DeepEqual(in1["link"], []int{2, 0}) {
		t.Fatalf("typed []int node link was modified: %#v", in1["link"])
	}
	if _, err := ApplyWorkflowOverrides(wfSimple, map[string]any{"1.cfg": json.Number("NaN")}, nil); err == nil {
		t.Fatalf("json.Number(NaN) override should fail")
	}
}

func TestComfyUIConcurrentSafety(t *testing.T) {
	wf := sampleAPIWorkflow()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, err := ValidateComfyAPIWorkflow(wf); err != nil {
					t.Errorf("ValidateComfyAPIWorkflow err: %v", err)
					return
				}
				fields := ExtractWorkflowFields(wf)
				if len(fields) != 10 {
					t.Errorf("unexpected field count: %d", len(fields))
					return
				}
				if _, err := ApplyWorkflowOverrides(wf, map[string]any{"6.seed": idx*100 + j}, map[string]string{"1.image": "a.png"}); err != nil {
					t.Errorf("ApplyWorkflowOverrides err: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestComfyUI64BitSeedPrecisionStrictNullGoStringAndFragmentRejection(t *testing.T) {
	// 1. 64-bit seed > 2^53 must survive UnmarshalJSON -> Validate -> ApplyWorkflowOverrides -> Marshal intact.
	const seed64 = "1085739284759283741"
	raw := `{"name":"Seed64","workflow":{"1":{"class_type":"KSampler","inputs":{"seed":` + seed64 + `,"steps":20}}}}`
	var req ColabOneWorkflowImportRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal seed64 workflow: %v", err)
	}
	if err := req.Validate(); err != nil {
		t.Fatalf("validate seed64 workflow: %v", err)
	}
	encoded, err := json.Marshal(req.Workflow)
	if err != nil {
		t.Fatalf("marshal seed64 workflow: %v", err)
	}
	if !strings.Contains(string(encoded), seed64) {
		t.Fatalf("64-bit seed lost precision during unmarshal/validate: %s", string(encoded))
	}

	// 2. Explicit null on defaulted fields or top-level null must be rejected.
	var nodeSpec ColabOneComfyNodeSpec
	if err := json.Unmarshal([]byte(`{"name":"n","repo":"a/b","commit":"`+strings.Repeat("a", 40)+`","install_requirements":null}`), &nodeSpec); err == nil {
		t.Fatal("expected error for install_requirements: null")
	}
	var nullCanvas ColabOneComfyCanvasConfig
	if err := json.Unmarshal([]byte(`null`), &nullCanvas); err == nil {
		t.Fatal("expected error for top-level null canvas config")
	}
	if err := json.Unmarshal([]byte(`{"mode":null,"domain":"comfy.example.com","tunnel_token":"x"}`), &nullCanvas); err == nil {
		t.Fatal("expected error for mode: null")
	}

	// 3. GoString() redacts TunnelToken and domain '#' fragment is rejected via publicapi.
	tok := defaultTestTunnelToken(t)
	cfg := ColabOneComfyCanvasConfig{Mode: "named", Domain: "https://comfy.example.com#", TunnelToken: tok}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected '#' fragment domain rejected by publicapi delegation")
	}
	if strings.Contains(fmt.Sprintf("%#v", cfg), tok) {
		t.Fatalf("GoString() leaked TunnelToken: %#v", cfg)
	}

	// 4. Overflowing json.Number (e.g. 1e9999 -> Infinity in Python) must be rejected.
	var overflowReq ColabOneWorkflowImportRequest
	if err := json.Unmarshal([]byte(`{"name":"Inf","workflow":{"1":{"class_type":"KSampler","inputs":{"cfg":1e9999}}}}`), &overflowReq); err != nil {
		t.Fatalf("unexpected unmarshal err: %v", err)
	}
	if err := overflowReq.Validate(); err == nil || err.Error() != "工作流包含无法序列化的内容" {
		t.Fatalf("expected non-serializable error for 1e9999, got %v", err)
	}

	// 5. uint64 seed in [2^63, 2^64-1] survives ExtractWorkflowFields & ApplyWorkflowOverrides without float64 rounding.
	const maxUintSeed = "18446744073709551615"
	var uReq ColabOneWorkflowImportRequest
	if err := json.Unmarshal([]byte(`{"name":"Uint64","workflow":{"1":{"class_type":"KSampler","inputs":{"seed":`+maxUintSeed+`}}}}`), &uReq); err != nil {
		t.Fatalf("unmarshal uint64 seed: %v", err)
	}
	if err := uReq.Validate(); err != nil {
		t.Fatalf("validate uint64 seed: %v", err)
	}
	fields := ExtractWorkflowFields(uReq.Workflow)
	if len(fields) != 1 {
		t.Fatalf("expected 1 extracted field, got %d", len(fields))
	}
	if uVal, ok := fields[0].Default.(uint64); !ok || uVal != math.MaxUint64 {
		t.Fatalf("uint64 seed degraded in ExtractWorkflowFields: %#v (%T)", fields[0].Default, fields[0].Default)
	}
	applied, err := ApplyWorkflowOverrides(uReq.Workflow, map[string]any{"1.seed": json.Number(maxUintSeed)}, nil)
	if err != nil {
		t.Fatalf("ApplyWorkflowOverrides uint64 failed: %v", err)
	}
	appliedBytes, _ := json.Marshal(applied)
	if !strings.Contains(string(appliedBytes), maxUintSeed) {
		t.Fatalf("ApplyWorkflowOverrides lost uint64 precision: %s", string(appliedBytes))
	}

	// 6. Case-mismatched keys and null on non-nullable fields are strictly rejected.
	var badCat ColabOneComfyCatalog
	if err := json.Unmarshal([]byte(`{"repo":"a/b","tag":"v1","commit":"`+strings.Repeat("a", 40)+`","Port":null}`), &badCat); err == nil || !strings.Contains(err.Error(), "未知字段") {
		t.Fatalf("expected unknown-field rejection for Port:null, got %v", err)
	}
	var badTpl ColabOneWorkflowTemplate
	if err := json.Unmarshal([]byte(`{"id":null,"name":"x","workflow":{"1":{"class_type":"A","inputs":{}}}}`), &badTpl); err == nil || !strings.Contains(err.Error(), "不能为空值") {
		t.Fatalf("expected non-null rejection for id:null, got %v", err)
	}
}

func TestWorkflowOutputRecordCreatedAtAndFieldOptionsStrictUnmarshal(t *testing.T) {
	// 1. Omitted required created_at on ColabOneWorkflowOutputRecord must be rejected.
	omittedCreatedAt := `{"id":"` + strings.Repeat("d", 32) + `","workflow_id":"wf_demo-1","workflow_name":"Demo","kind":"video","filename":"clip_0001.mp4","extension":"mp4","mime":"video/mp4","size":128,"sha256":"` + strings.Repeat("e", 64) + `"}`
	var missingRec ColabOneWorkflowOutputRecord
	if err := json.Unmarshal([]byte(omittedCreatedAt), &missingRec); err == nil || err.Error() != "创建时间不合法" {
		t.Fatalf("omitted created_at: err = %v, want %q", err, "创建时间不合法")
	}

	// Explicit null created_at must also be rejected.
	nullCreatedAt := `{"id":"` + strings.Repeat("d", 32) + `","workflow_id":"wf_demo-1","workflow_name":"Demo","kind":"video","filename":"clip_0001.mp4","extension":"mp4","mime":"video/mp4","size":128,"sha256":"` + strings.Repeat("e", 64) + `","created_at":null}`
	var nullRec ColabOneWorkflowOutputRecord
	if err := json.Unmarshal([]byte(nullCreatedAt), &nullRec); err == nil || !strings.Contains(err.Error(), "不能为空值") {
		t.Fatalf("null created_at: err = %v, want non-null rejection", err)
	}

	// Explicit created_at = 0 must be accepted and validate cleanly.
	zeroCreatedAt := `{"id":"` + strings.Repeat("d", 32) + `","workflow_id":"wf_demo-1","workflow_name":"Demo","kind":"video","filename":"clip_0001.mp4","extension":"mp4","mime":"video/mp4","size":128,"sha256":"` + strings.Repeat("e", 64) + `","created_at":0}`
	var zeroRec ColabOneWorkflowOutputRecord
	if err := json.Unmarshal([]byte(zeroCreatedAt), &zeroRec); err != nil {
		t.Fatalf("explicit created_at=0 unmarshal failed: %v", err)
	}
	if zeroRec.CreatedAt != 0 {
		t.Fatalf("explicit created_at=0: got %v, want 0", zeroRec.CreatedAt)
	}
	if err := zeroRec.Validate(); err != nil {
		t.Fatalf("explicit created_at=0 validate failed: %v", err)
	}

	// 2. ColabOneWorkflowField.UnmarshalJSON must reject null elements in options.
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{
			name: "singleNullOption",
			raw:  `{"key":"1.mode","label":"Mode","node_id":"1","input_name":"mode","field_type":"select","options":[null]}`,
		},
		{
			name: "mixedNullOption",
			raw:  `{"key":"1.mode","label":"Mode","node_id":"1","input_name":"mode","field_type":"select","options":["fast",null,"quality"]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var f ColabOneWorkflowField
			err := json.Unmarshal([]byte(tc.raw), &f)
			if err == nil || err.Error() != "字段 options 不能为空值" {
				t.Fatalf("%s: err = %v, want %q", tc.name, err, "字段 options 不能为空值")
			}
		})
	}

	// Explicit options: null must also be rejected.
	var nullOptsField ColabOneWorkflowField
	if err := json.Unmarshal([]byte(`{"key":"1.mode","label":"Mode","node_id":"1","input_name":"mode","field_type":"select","options":null}`), &nullOptsField); err == nil || err.Error() != "字段 options 不能为空值" {
		t.Fatalf("null options: err = %v, want %q", err, "字段 options 不能为空值")
	}

	// Omitted options defaults to non-nil empty slice, and valid string options (including "") are preserved.
	var omittedOpts ColabOneWorkflowField
	if err := json.Unmarshal([]byte(`{"key":"1.mode","label":"Mode","node_id":"1","input_name":"mode","field_type":"select"}`), &omittedOpts); err != nil {
		t.Fatalf("omitted options unmarshal failed: %v", err)
	}
	if omittedOpts.Options == nil || len(omittedOpts.Options) != 0 {
		t.Fatalf("omitted options = %#v, want non-nil empty slice", omittedOpts.Options)
	}
	if err := omittedOpts.Validate(); err != nil {
		t.Fatalf("omitted options validate failed: %v", err)
	}

	var validOpts ColabOneWorkflowField
	if err := json.Unmarshal([]byte(`{"key":"1.mode","label":"Mode","node_id":"1","input_name":"mode","field_type":"select","options":["fast","","quality"]}`), &validOpts); err != nil {
		t.Fatalf("valid options unmarshal failed: %v", err)
	}
	if !reflect.DeepEqual(validOpts.Options, []string{"fast", "", "quality"}) {
		t.Fatalf("valid options = %#v, want [fast \"\" quality]", validOpts.Options)
	}
	if err := validOpts.Validate(); err != nil {
		t.Fatalf("valid options validate failed: %v", err)
	}

	// 3. Exact 2 MiB compact UTF-8 JSON boundary in ValidateComfyAPIWorkflow.
	shell := map[string]any{
		"1": map[string]any{
			"class_type": "CLIPTextEncode",
			"inputs": map[string]any{
				"text": "",
			},
		},
	}
	shellBytes, err := encodeWorkflowJSON(shell)
	if err != nil {
		t.Fatalf("encode shell: %v", err)
	}
	exactPad := MaxWorkflowBytes - len(shellBytes)
	exactWF := map[string]any{
		"1": map[string]any{
			"class_type": "CLIPTextEncode",
			"inputs": map[string]any{
				"text": strings.Repeat("a", exactPad),
			},
		},
	}
	if _, err := ValidateComfyAPIWorkflow(exactWF); err != nil {
		t.Fatalf("exact 2 MiB compact workflow should pass, got %v", err)
	}
	exactWF["1"].(map[string]any)["inputs"].(map[string]any)["text"] = strings.Repeat("a", exactPad+1)
	if _, err := ValidateComfyAPIWorkflow(exactWF); err == nil || !strings.Contains(err.Error(), "最大 2 MiB") {
		t.Fatalf("2 MiB + 1 byte workflow should fail with size error, got %v", err)
	}
}

type validatableSchema interface {
	Validate() error
}

func mustMarshalJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal JSON fixture %T: %v", v, err)
	}
	return string(b)
}

func marshalJSONMap(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal marshaled %T: %v", v, err)
	}
	return out
}

func unmarshalAndValidateSchema(raw string, dst validatableSchema) error {
	if err := json.Unmarshal([]byte(raw), dst); err != nil {
		return err
	}
	return dst.Validate()
}

func TestComfyNodePatchValidation(t *testing.T) {
	// Valid allowlisted patch kind + whitespace trimming on node_name.
	var patch ColabOneComfyNodePatch
	if err := unmarshalAndValidateSchema(`{"node_name":"  ComfyUI-WanVideoWrapper  ","kind":"wav2vec2_hidden_states_hook"}`, &patch); err != nil {
		t.Fatalf("valid node patch failed: %v", err)
	}
	wire := marshalJSONMap(t, &patch)
	if wire["node_name"] != "ComfyUI-WanVideoWrapper" || wire["kind"] != "wav2vec2_hidden_states_hook" {
		t.Fatalf("unexpected marshaled node patch: %#v", wire)
	}

	// Exact 120-rune node_name boundary accepted.
	var maxLenPatch ColabOneComfyNodePatch
	maxName := strings.Repeat("n", 120)
	if err := unmarshalAndValidateSchema(mustMarshalJSON(t, map[string]any{
		"node_name": maxName,
		"kind":      "wav2vec2_hidden_states_hook",
	}), &maxLenPatch); err != nil {
		t.Fatalf("120-rune node_name should pass: %v", err)
	}

	// Path traversal, unsafe characters, blank, and >120-rune node_name rejected.
	for _, badName := range []string{
		"",
		"   ",
		".",
		"..",
		"../evil",
		"a..b",
		"a/b",
		`a\b`,
		"a:b",
		"a\x00b",
		strings.Repeat("n", 121),
	} {
		var bad ColabOneComfyNodePatch
		raw := mustMarshalJSON(t, map[string]any{
			"node_name": badName,
			"kind":      "wav2vec2_hidden_states_hook",
		})
		if err := unmarshalAndValidateSchema(raw, &bad); err == nil {
			t.Fatalf("bad node_name %q should fail validation", badName)
		}
	}

	// Unknown / empty / wrong-case patch kind rejected.
	for _, badKind := range []string{
		"",
		"unknown_patch",
		"arbitrary_python_exec",
		"WAV2VEC2_HIDDEN_STATES_HOOK",
	} {
		var bad ColabOneComfyNodePatch
		raw := mustMarshalJSON(t, map[string]any{
			"node_name": "ComfyUI-WanVideoWrapper",
			"kind":      badKind,
		})
		if err := unmarshalAndValidateSchema(raw, &bad); err == nil {
			t.Fatalf("bad patch kind %q should fail validation", badKind)
		}
	}

	// Strict JSON: unknown fields and nulls rejected.
	for _, raw := range []string{
		`null`,
		`{"node_name":"ComfyUI-WanVideoWrapper","kind":"wav2vec2_hidden_states_hook","extra":true}`,
		`{"node_name":null,"kind":"wav2vec2_hidden_states_hook"}`,
		`{"node_name":"ComfyUI-WanVideoWrapper","kind":null}`,
	} {
		var bad ColabOneComfyNodePatch
		if err := unmarshalAndValidateSchema(raw, &bad); err == nil {
			t.Fatalf("strict JSON patch payload %s should fail", raw)
		}
	}
}

func TestComfyTtsEngineSpecValidation(t *testing.T) {
	revA := strings.Repeat("a", 40)
	revB := strings.Repeat("b", 40)
	revC := strings.Repeat("c", 40)

	// 1. Minimal valid payload verifies defaults: commercial_ok=true, sample_rate=48000, license="Apache-2.0", supports_voice_clone=true.
	var minimal ColabOneComfyTtsEngineSpec
	minimalRaw := mustMarshalJSON(t, map[string]any{
		"id":       "voxcpm2",
		"name":     "  VoxCPM2  ",
		"repo":     "openbmb/VoxCPM2",
		"revision": revA,
	})
	if err := unmarshalAndValidateSchema(minimalRaw, &minimal); err != nil {
		t.Fatalf("minimal tts engine failed: %v", err)
	}
	wire := marshalJSONMap(t, &minimal)
	if wire["id"] != "voxcpm2" || wire["name"] != "VoxCPM2" {
		t.Fatalf("unexpected id/name: %#v", wire)
	}
	if wire["commercial_ok"] != true {
		t.Fatalf("commercial_ok = %#v, want true", wire["commercial_ok"])
	}
	if wire["sample_rate"] != float64(48000) {
		t.Fatalf("sample_rate = %#v, want 48000", wire["sample_rate"])
	}
	if wire["license"] != "Apache-2.0" {
		t.Fatalf("license = %#v, want Apache-2.0", wire["license"])
	}
	if wire["supports_voice_clone"] != true {
		t.Fatalf("supports_voice_clone = %#v, want true", wire["supports_voice_clone"])
	}
	if wire["supports_voice_design"] != false || wire["supports_emotion_ref"] != false || wire["supports_inline_tags"] != false {
		t.Fatalf("unexpected default capability flags: %#v", wire)
	}
	if wire["min_vram_mib"] != float64(0) {
		t.Fatalf("min_vram_mib = %#v, want 0", wire["min_vram_mib"])
	}
	if rawPip, exists := wire["pip_packages"]; exists {
		if _, ok := rawPip.([]any); !ok {
			t.Fatalf("pip_packages should be JSON array [], got %#v", rawPip)
		}
	}
	if rawPresets, exists := wire["voice_presets"]; !exists {
		t.Fatalf("voice_presets missing from marshaled tts engine")
	} else if arr, ok := rawPresets.([]any); !ok || len(arr) != 0 {
		t.Fatalf("default voice_presets should marshal as empty JSON array [], got %#v", rawPresets)
	}

	// 2. Non-commercial engine preserves commercial_ok=false, license_notice, explicit supports_voice_clone=false, pip trimming, source_repo/source_commit.
	var nonCommercial ColabOneComfyTtsEngineSpec
	nonCommercialRaw := mustMarshalJSON(t, map[string]any{
		"id":                   "fish_s2_pro",
		"name":                 "Fish Audio S2 Pro",
		"description":          "Multi-speaker expressive TTS with inline emotion tags",
		"repo":                 "fishaudio/s2-pro",
		"revision":             revA,
		"license":              "Fish Audio Research License",
		"commercial_ok":        false,
		"license_notice":       "仅限个人学习与非商业研究使用；如需商业使用须另行向 Fish Audio 获取商业授权。",
		"min_vram_mib":         12288,
		"sample_rate":          44100,
		"supports_voice_clone": false,
		"supports_inline_tags": true,
		"pip_packages":         []string{"fish-speech", "  soundfile>=0.12  "},
		"source_repo":          "https://github.com/fishaudio/fish-speech.git",
		"source_commit":        revB,
	})
	if err := unmarshalAndValidateSchema(nonCommercialRaw, &nonCommercial); err != nil {
		t.Fatalf("non-commercial tts engine failed: %v", err)
	}
	ncWire := marshalJSONMap(t, &nonCommercial)
	if ncWire["commercial_ok"] != false {
		t.Fatalf("explicit commercial_ok=false was not preserved: %#v", ncWire["commercial_ok"])
	}
	if ncWire["supports_voice_clone"] != false {
		t.Fatalf("explicit supports_voice_clone=false was not preserved: %#v", ncWire["supports_voice_clone"])
	}
	if ncWire["supports_inline_tags"] != true || ncWire["sample_rate"] != float64(44100) || ncWire["min_vram_mib"] != float64(12288) {
		t.Fatalf("unexpected non-commercial fields: %#v", ncWire)
	}
	if ncWire["license_notice"] != "仅限个人学习与非商业研究使用；如需商业使用须另行向 Fish Audio 获取商业授权。" {
		t.Fatalf("license_notice = %#v", ncWire["license_notice"])
	}
	if pips, ok := ncWire["pip_packages"].([]any); !ok || len(pips) != 2 || pips[1] != "soundfile>=0.12" {
		t.Fatalf("pip_packages not trimmed/preserved: %#v", ncWire["pip_packages"])
	}

	// 3. Dual-repo engine (aux_repo + aux_revision) and owner/repo source_repo slug accepted.
	var dualRepo ColabOneComfyTtsEngineSpec
	dualRepoRaw := mustMarshalJSON(t, map[string]any{
		"id":                    "qwen3_tts",
		"name":                  "Qwen3-TTS 1.7B",
		"repo":                  "Qwen/Qwen3-TTS-12Hz-1.7B-Base",
		"revision":              revA,
		"aux_repo":              "Qwen/Qwen3-TTS-12Hz-1.7B-VoiceDesign",
		"aux_revision":          revB,
		"sample_rate":           24000,
		"supports_voice_design": true,
		"source_repo":           "QwenLM/Qwen3-TTS",
		"source_commit":         revC,
	})
	if err := unmarshalAndValidateSchema(dualRepoRaw, &dualRepo); err != nil {
		t.Fatalf("dual-repo tts engine failed: %v", err)
	}
	dualWire := marshalJSONMap(t, &dualRepo)
	if dualWire["aux_repo"] != "Qwen/Qwen3-TTS-12Hz-1.7B-VoiceDesign" || dualWire["aux_revision"] != revB {
		t.Fatalf("aux_repo/aux_revision not preserved: %#v", dualWire)
	}

	// Boundary sample_rate values (8000 and 96000) accepted.
	for _, sr := range []int{8000, 96000} {
		var boundary ColabOneComfyTtsEngineSpec
		raw := mustMarshalJSON(t, map[string]any{
			"id":          fmt.Sprintf("sr_%d", sr),
			"name":        "Boundary SR",
			"repo":        "openbmb/VoxCPM2",
			"revision":    revA,
			"sample_rate": sr,
		})
		if err := unmarshalAndValidateSchema(raw, &boundary); err != nil {
			t.Fatalf("sample_rate=%d should pass: %v", sr, err)
		}
	}

	// 4. Invalid payloads rejected.
	tooManyPips := make([]string, 65)
	for i := range tooManyPips {
		tooManyPips[i] = fmt.Sprintf("pkg%d", i)
	}
	baseEngine := func() map[string]any {
		return map[string]any{
			"id":       "voxcpm2",
			"name":     "VoxCPM2",
			"repo":     "openbmb/VoxCPM2",
			"revision": revA,
		}
	}
	badCases := []struct {
		name   string
		mutate func(m map[string]any)
	}{
		{"emptyID", func(m map[string]any) { m["id"] = "" }},
		{"traversalID", func(m map[string]any) { m["id"] = "../evil" }},
		{"overlongID", func(m map[string]any) { m["id"] = strings.Repeat("a", 81) }},
		{"blankName", func(m map[string]any) { m["name"] = "   " }},
		{"overlongName", func(m map[string]any) { m["name"] = strings.Repeat("n", 161) }},
		{"overlongDescription", func(m map[string]any) { m["description"] = strings.Repeat("d", 1001) }},
		{"badRepoNoSlash", func(m map[string]any) { m["repo"] = "invalid-repo" }},
		{"badRepoTraversal", func(m map[string]any) { m["repo"] = "../evil" }},
		{"badRepoURL", func(m map[string]any) { m["repo"] = "https://huggingface.co/openbmb/VoxCPM2" }},
		{"shortRevision", func(m map[string]any) { m["revision"] = "abc123" }},
		{"nonHexRevision", func(m map[string]any) { m["revision"] = strings.Repeat("g", 40) }},
		{"auxRepoWithoutAuxRevision", func(m map[string]any) { m["aux_repo"] = "Qwen/Qwen3-TTS-12Hz-1.7B-VoiceDesign" }},
		{"auxRepoWithEmptyAuxRevision", func(m map[string]any) {
			m["aux_repo"] = "Qwen/Qwen3-TTS-12Hz-1.7B-VoiceDesign"
			m["aux_revision"] = ""
		}},
		{"auxRepoWithBadAuxRevision", func(m map[string]any) {
			m["aux_repo"] = "Qwen/Qwen3-TTS-12Hz-1.7B-VoiceDesign"
			m["aux_revision"] = "bad-sha"
		}},
		{"badAuxRepoWithValidAuxRevision", func(m map[string]any) {
			m["aux_repo"] = "../evil"
			m["aux_revision"] = revB
		}},
		{"badAuxRevisionAlone", func(m map[string]any) { m["aux_revision"] = "not-a-sha" }},
		{"overlongLicense", func(m map[string]any) { m["license"] = strings.Repeat("L", 161) }},
		{"overlongLicenseNotice", func(m map[string]any) { m["license_notice"] = strings.Repeat("x", 601) }},
		{"negativeMinVRAM", func(m map[string]any) { m["min_vram_mib"] = -1 }},
		{"sampleRateTooLow", func(m map[string]any) { m["sample_rate"] = 7999 }},
		{"sampleRateTooHigh", func(m map[string]any) { m["sample_rate"] = 96001 }},
		{"sampleRateNegative", func(m map[string]any) { m["sample_rate"] = -48000 }},
		{"pipFlagInjection", func(m map[string]any) { m["pip_packages"] = []string{"--extra-index-url https://evil.invalid"} }},
		{"pipURLInjection", func(m map[string]any) { m["pip_packages"] = []string{"https://evil.invalid/pkg.whl"} }},
		{"pipTraversal", func(m map[string]any) { m["pip_packages"] = []string{"../localpkg"} }},
		{"pipBlank", func(m map[string]any) { m["pip_packages"] = []string{"   "} }},
		{"pipTooMany", func(m map[string]any) { m["pip_packages"] = tooManyPips }},
		{"badSourceRepoHTTP", func(m map[string]any) { m["source_repo"] = "http://github.com/owner/repo" }},
		{"badSourceRepoTraversal", func(m map[string]any) { m["source_repo"] = "../evil" }},
		{"badSourceCommit", func(m map[string]any) { m["source_commit"] = "not-40-hex" }},
		{"unknownExtraField", func(m map[string]any) { m["extra_field"] = true }},
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

	// Null strictness checks.
	for _, raw := range []string{
		`null`,
		`{"id":"voxcpm2","name":"VoxCPM2","repo":"openbmb/VoxCPM2","revision":"` + revA + `","commercial_ok":null}`,
		`{"id":"voxcpm2","name":"VoxCPM2","repo":"openbmb/VoxCPM2","revision":"` + revA + `","sample_rate":null}`,
	} {
		var spec ColabOneComfyTtsEngineSpec
		if err := unmarshalAndValidateSchema(raw, &spec); err == nil {
			t.Fatalf("null payload %s should fail", raw)
		}
	}
}

func TestComfyPipelineSpecValidation(t *testing.T) {
	// 1. Empty JSON object populates all locked defaults.
	var defSpec ColabOneComfyPipelineSpec
	if err := unmarshalAndValidateSchema(`{}`, &defSpec); err != nil {
		t.Fatalf("default pipeline spec failed: %v", err)
	}
	defWire := marshalJSONMap(t, &defSpec)
	if defWire["kind"] != "comfy_standard" {
		t.Fatalf("default kind = %#v, want comfy_standard", defWire["kind"])
	}
	if defWire["video_backend"] != "custom" {
		t.Fatalf("default video_backend = %#v, want custom", defWire["video_backend"])
	}
	if defWire["default_tts_engine"] != "voxcpm2" {
		t.Fatalf("default default_tts_engine = %#v, want voxcpm2", defWire["default_tts_engine"])
	}
	if defWire["max_segment_seconds"] != float64(45.0) {
		t.Fatalf("default max_segment_seconds = %#v, want 45.0", defWire["max_segment_seconds"])
	}
	if defWire["tail_frame_offset_seconds"] != float64(0.08) {
		t.Fatalf("default tail_frame_offset_seconds = %#v, want 0.08", defWire["tail_frame_offset_seconds"])
	}
	if defWire["output_fps"] != float64(25) {
		t.Fatalf("default output_fps = %#v, want 25", defWire["output_fps"])
	}
	if defWire["target_width"] != float64(1280) || defWire["target_height"] != float64(720) {
		t.Fatalf("default target resolution = %vx%v, want 1280x720", defWire["target_width"], defWire["target_height"])
	}
	if defWire["upscale_mode"] != "none" {
		t.Fatalf("default upscale_mode = %#v, want none", defWire["upscale_mode"])
	}
	if rawSupported, exists := defWire["supported_tts_engines"]; exists {
		if _, ok := rawSupported.([]any); !ok {
			t.Fatalf("default supported_tts_engines should be JSON array [], got %#v", rawSupported)
		}
	}

	// 2. Valid digital_human configs for both infinitetalk and longcat_avatar (plus lanczos upscale).
	var infSpec ColabOneComfyPipelineSpec
	infRaw := mustMarshalJSON(t, map[string]any{
		"kind":                      "digital_human",
		"video_backend":             "infinitetalk",
		"default_tts_engine":        "voxcpm2",
		"supported_tts_engines":     []string{"voxcpm2", "qwen3_tts", "fish_s2_pro", "indextts2"},
		"max_segment_seconds":       45.0,
		"tail_frame_offset_seconds": 0.08,
		"output_fps":                25,
		"target_width":              1280,
		"target_height":             720,
		"upscale_mode":              "ai_2x",
		"audio_node_key":            "2.audio",
		"image_node_key":            "1.image",
		"prompt_node_key":           "5.positive_prompt",
		"frames_node_key":           "7.frame_window_size",
		"width_node_key":            "7.width",
		"height_node_key":           "7.height",
		"fps_node_key":              "11.frame_rate",
		"steps_node_key":            "8.steps",
		"seed_node_key":             "8.seed",
	})
	if err := unmarshalAndValidateSchema(infRaw, &infSpec); err != nil {
		t.Fatalf("valid infinitetalk pipeline failed: %v", err)
	}
	infWire := marshalJSONMap(t, &infSpec)
	if infWire["kind"] != "digital_human" || infWire["video_backend"] != "infinitetalk" || infWire["upscale_mode"] != "ai_2x" {
		t.Fatalf("unexpected infinitetalk pipeline wire: %#v", infWire)
	}
	if infWire["audio_node_key"] != "2.audio" || infWire["seed_node_key"] != "8.seed" {
		t.Fatalf("node keys not preserved: %#v", infWire)
	}

	var longcatSpec ColabOneComfyPipelineSpec
	longcatRaw := mustMarshalJSON(t, map[string]any{
		"kind":                      "digital_human",
		"video_backend":             "longcat_avatar",
		"default_tts_engine":        "qwen3_tts",
		"supported_tts_engines":     []string{"voxcpm2", "qwen3_tts"},
		"max_segment_seconds":       60.0,
		"tail_frame_offset_seconds": 0.12,
		"output_fps":                30,
		"target_width":              1920,
		"target_height":             1080,
		"upscale_mode":              "lanczos",
		"audio_node_key":            "2.audio",
		"image_node_key":            "1.image",
	})
	if err := unmarshalAndValidateSchema(longcatRaw, &longcatSpec); err != nil {
		t.Fatalf("valid longcat_avatar pipeline failed: %v", err)
	}
	lcWire := marshalJSONMap(t, &longcatSpec)
	if lcWire["video_backend"] != "longcat_avatar" || lcWire["upscale_mode"] != "lanczos" || lcWire["output_fps"] != float64(30) {
		t.Fatalf("unexpected longcat_avatar pipeline wire: %#v", lcWire)
	}

	// Boundary values accepted (including 3.0s lower bound and LongCat-Avatar 3.7s golden window).
	for _, tc := range []map[string]any{
		{"max_segment_seconds": 3.0, "tail_frame_offset_seconds": 0.0, "output_fps": 1, "target_width": 64, "target_height": 64},
		{"max_segment_seconds": 3.7, "tail_frame_offset_seconds": 0.08, "output_fps": 25, "target_width": 1280, "target_height": 720},
		{"max_segment_seconds": 300.0, "tail_frame_offset_seconds": 1.0, "output_fps": 60, "target_width": 4096, "target_height": 4096},
	} {
		var bSpec ColabOneComfyPipelineSpec
		if err := unmarshalAndValidateSchema(mustMarshalJSON(t, tc), &bSpec); err != nil {
			t.Fatalf("boundary pipeline %v should pass: %v", tc, err)
		}
	}

	// 3. Invalid pipeline parameters rejected.
	tooManyEngines := make([]string, 17)
	for i := range tooManyEngines {
		tooManyEngines[i] = fmt.Sprintf("tts_%d", i)
	}
	badPayloads := []struct {
		name string
		body map[string]any
	}{
		{"unknownKind", map[string]any{"kind": "realtime_stream"}},
		{"unknownBackend", map[string]any{"video_backend": "sora_v2"}},
		{"unknownUpscaleMode", map[string]any{"upscale_mode": "esrgan_4x"}},
		{"maxSegTooSmall", map[string]any{"max_segment_seconds": 2.9}},
		{"maxSegExplicitZero", map[string]any{"max_segment_seconds": 0.0}},
		{"maxSegTooLarge", map[string]any{"max_segment_seconds": 300.1}},
		{"maxSegNegative", map[string]any{"max_segment_seconds": -5.0}},
		{"tailOffsetNegative", map[string]any{"tail_frame_offset_seconds": -0.01}},
		{"tailOffsetTooLarge", map[string]any{"tail_frame_offset_seconds": 1.01}},
		{"fpsNegative", map[string]any{"output_fps": -1}},
		{"fpsTooHigh", map[string]any{"output_fps": 61}},
		{"widthTooSmall", map[string]any{"target_width": 63}},
		{"widthTooLarge", map[string]any{"target_width": 4097}},
		{"heightTooSmall", map[string]any{"target_height": 63}},
		{"heightTooLarge", map[string]any{"target_height": 4097}},
		{"defaultTtsOverlong", map[string]any{"default_tts_engine": strings.Repeat("a", 81)}},
		{"supportedTtsInvalidID", map[string]any{"supported_tts_engines": []string{"../evil"}}},
		{"supportedTtsTooMany", map[string]any{"supported_tts_engines": tooManyEngines}},
		{"unknownExtraField", map[string]any{"extra": 123}},
	}
	for _, tc := range badPayloads {
		t.Run(tc.name, func(t *testing.T) {
			var p ColabOneComfyPipelineSpec
			if err := unmarshalAndValidateSchema(mustMarshalJSON(t, tc.body), &p); err == nil {
				t.Fatalf("%s: expected validation error", tc.name)
			}
		})
	}

	// Exact error message parity for max_segment_seconds [3.0, 300.0] bounds.
	const wantSegErr = "分段时长必须在 3.0 到 300.0 秒之间"
	for _, raw := range []string{
		`{"max_segment_seconds":0}`,
		`{"max_segment_seconds":2.9}`,
		`{"max_segment_seconds":300.1}`,
		`{"max_segment_seconds":-1.0}`,
	} {
		var p ColabOneComfyPipelineSpec
		if err := unmarshalAndValidateSchema(raw, &p); err == nil || err.Error() != wantSegErr {
			t.Fatalf("pipeline payload %s: err = %v, want %q", raw, err, wantSegErr)
		}
	}
	for _, badVal := range []float64{2.9, 300.1, -3.0, math.NaN(), math.Inf(1), math.Inf(-1)} {
		p := ColabOneComfyPipelineSpec{MaxSegmentSeconds: badVal}
		if err := p.Validate(); err == nil || err.Error() != wantSegErr {
			t.Fatalf("direct MaxSegmentSeconds=%v: err = %v, want %q", badVal, err, wantSegErr)
		}
	}
	for _, goodVal := range []float64{3.0, 3.7, 45.0, 300.0} {
		p := ColabOneComfyPipelineSpec{MaxSegmentSeconds: goodVal}
		if err := p.Validate(); err != nil {
			t.Fatalf("direct MaxSegmentSeconds=%v should pass, got %v", goodVal, err)
		}
	}

	// All 9 *_node_key fields reject invalid node_id.input_name keys.
	nodeKeyFields := []string{
		"audio_node_key",
		"image_node_key",
		"prompt_node_key",
		"frames_node_key",
		"width_node_key",
		"height_node_key",
		"fps_node_key",
		"steps_node_key",
		"seed_node_key",
	}
	for _, fieldName := range nodeKeyFields {
		for _, badKey := range []string{"no_dot", "../evil.input", "1.", ".input"} {
			var p ColabOneComfyPipelineSpec
			raw := mustMarshalJSON(t, map[string]any{fieldName: badKey})
			if err := unmarshalAndValidateSchema(raw, &p); err == nil {
				t.Fatalf("%s=%q should fail validation", fieldName, badKey)
			}
		}
	}

	// Top-level null and overflowing numeric JSON rejected.
	for _, raw := range []string{`null`, `{"max_segment_seconds":1e9999}`} {
		var p ColabOneComfyPipelineSpec
		if err := unmarshalAndValidateSchema(raw, &p); err == nil {
			t.Fatalf("pipeline payload %s should fail", raw)
		}
	}
}

func TestComfyPipelineSpecLongCatNativeAndUpscaleModes(t *testing.T) {
	// 1. Validate video_backend="longcat_avatar_native" with 2K (2560x1440) and portrait 2K (1440x2560) dimensions.
	for _, tc := range []struct {
		name   string
		width  int
		height int
		mode   string
	}{
		{"landscape2KAuto", 2560, 1440, "auto"},
		{"landscape2KSeedVR2", 2560, 1440, "seedvr2_7b"},
		{"landscape2KSeedVR2Sharp", 2560, 1440, "seedvr2_7b_sharp"},
		{"portrait2KFlashVSRPlus", 1440, 2560, "flashvsr_plus"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var spec ColabOneComfyPipelineSpec
			raw := mustMarshalJSON(t, map[string]any{
				"kind":                      "digital_human",
				"video_backend":             "longcat_avatar_native",
				"default_tts_engine":        "voxcpm2",
				"supported_tts_engines":     []string{"voxcpm2", "qwen3_tts"},
				"max_segment_seconds":       45.0,
				"tail_frame_offset_seconds": 0.08,
				"output_fps":                25,
				"target_width":              tc.width,
				"target_height":             tc.height,
				"upscale_mode":              tc.mode,
				"audio_node_key":            "2.audio",
				"image_node_key":            "1.image",
			})
			if err := unmarshalAndValidateSchema(raw, &spec); err != nil {
				t.Fatalf("%s: expected valid longcat_avatar_native pipeline, got %v", tc.name, err)
			}
			if spec.VideoBackend != "longcat_avatar_native" {
				t.Fatalf("%s: VideoBackend = %q, want longcat_avatar_native", tc.name, spec.VideoBackend)
			}
			if spec.UpscaleMode != tc.mode {
				t.Fatalf("%s: UpscaleMode = %q, want %q", tc.name, spec.UpscaleMode, tc.mode)
			}
			if spec.TargetWidth != tc.width || spec.TargetHeight != tc.height {
				t.Fatalf("%s: target dimensions = %dx%d, want %dx%d", tc.name, spec.TargetWidth, spec.TargetHeight, tc.width, tc.height)
			}

			// Round-trip wire parity check.
			var roundTripped ColabOneComfyPipelineSpec
			if err := unmarshalAndValidateSchema(mustMarshalJSON(t, &spec), &roundTripped); err != nil {
				t.Fatalf("%s: round-trip failed: %v", tc.name, err)
			}
			if !reflect.DeepEqual(roundTripped, spec) {
				t.Fatalf("%s: round-trip mismatch:\ngot:  %+v\nwant: %+v", tc.name, roundTripped, spec)
			}
		})
	}

	// 2. Verify all allowed upscale_mode values pass direct Validate() and JSON unmarshal.
	for _, mode := range []string{
		"none",
		"ai_2x",
		"lanczos",
		"auto",
		"seedvr2_7b",
		"seedvr2_7b_sharp",
		"flashvsr_plus",
	} {
		direct := ColabOneComfyPipelineSpec{
			Kind:         "digital_human",
			VideoBackend: "longcat_avatar_native",
			TargetWidth:  2560,
			TargetHeight: 1440,
			UpscaleMode:  mode,
		}
		if err := direct.Validate(); err != nil {
			t.Fatalf("direct Validate() upscale_mode=%q failed: %v", mode, err)
		}
		var fromJSON ColabOneComfyPipelineSpec
		raw := mustMarshalJSON(t, map[string]any{
			"kind":          "digital_human",
			"video_backend": "longcat_avatar_native",
			"target_width":  2560,
			"target_height": 1440,
			"upscale_mode":  mode,
		})
		if err := unmarshalAndValidateSchema(raw, &fromJSON); err != nil {
			t.Fatalf("JSON Validate() upscale_mode=%q failed: %v", mode, err)
		}
		if fromJSON.UpscaleMode != mode {
			t.Fatalf("JSON UpscaleMode = %q, want %q", fromJSON.UpscaleMode, mode)
		}
	}

	// 3. Builtin workflow template carrying longcat_avatar_native + seedvr2_7b + 2560x1440 validates cleanly.
	tmpl := ColabOneWorkflowTemplate{
		ID:       "digital-human-longcat-native-flagship",
		Name:     "LongCat-Video-Avatar 1.6 Native Flagship",
		Source:   "builtin",
		Workflow: sampleAPIWorkflow(),
		Pipeline: &ColabOneComfyPipelineSpec{
			Kind:         "digital_human",
			VideoBackend: "longcat_avatar_native",
			TargetWidth:  2560,
			TargetHeight: 1440,
			UpscaleMode:  "seedvr2_7b",
		},
	}
	if err := tmpl.Validate(); err != nil {
		t.Fatalf("builtin template with longcat_avatar_native failed: %v", err)
	}

	// 4. Reject invalid video_backend values with exact error message.
	const wantBackendErr = "视频生成后端不合法"
	for _, badBackend := range []string{
		"",
		"longcat_native",
		"longcat_avatar_v2",
		"LONGCAT_AVATAR_NATIVE",
		"unknown_backend",
	} {
		var p ColabOneComfyPipelineSpec
		raw := mustMarshalJSON(t, map[string]any{"video_backend": badBackend})
		if err := unmarshalAndValidateSchema(raw, &p); err == nil || err.Error() != wantBackendErr {
			t.Fatalf("invalid video_backend=%q: err = %v, want %q", badBackend, err, wantBackendErr)
		}
	}

	// 5. Reject invalid upscale_mode values with exact error message.
	const wantUpscaleErr = "超分模式不合法"
	for _, badMode := range []string{
		"",
		"seedvr2",
		"seedvr2_14b",
		"flashvsr",
		"SEEDVR2_7B",
		"AUTO",
		"esrgan_4x",
	} {
		var p ColabOneComfyPipelineSpec
		raw := mustMarshalJSON(t, map[string]any{"upscale_mode": badMode})
		if err := unmarshalAndValidateSchema(raw, &p); err == nil || err.Error() != wantUpscaleErr {
			t.Fatalf("invalid upscale_mode=%q: err = %v, want %q", badMode, err, wantUpscaleErr)
		}
	}
}

func TestWorkflowFieldExtendedSchemaValidation(t *testing.T) {
	// 1. Valid extended field with description, group, required, visible_when (string + bool), and option_labels.
	var extField ColabOneWorkflowField
	extRaw := mustMarshalJSON(t, map[string]any{
		"key":         "pipeline.emotion_ref_audio",
		"label":       "情绪参考音频",
		"node_id":     "pipeline",
		"input_name":  "emotion_ref_audio",
		"field_type":  "audio",
		"description": "上传一段用于引导情绪的短音频",
		"group":       "voice",
		"required":    true,
		"visible_when": map[string]any{
			"pipeline.audio_source": "tts",
			"pipeline.tts_engine":   "indextts2",
			"1.use_emotion":         true,
			"1.mute_audio":          false,
		},
		"options": []string{"tts", "audio"},
		"option_labels": map[string]string{
			"tts":   "文本配音 (TTS)",
			"audio": "上传驱动音频",
		},
	})
	if err := unmarshalAndValidateSchema(extRaw, &extField); err != nil {
		t.Fatalf("valid extended workflow field failed: %v", err)
	}
	wire := marshalJSONMap(t, &extField)
	if wire["description"] != "上传一段用于引导情绪的短音频" || wire["group"] != "voice" || wire["required"] != true {
		t.Fatalf("unexpected extended metadata on field: %#v", wire)
	}
	vw, ok := wire["visible_when"].(map[string]any)
	if !ok || vw["pipeline.audio_source"] != "tts" || vw["pipeline.tts_engine"] != "indextts2" || vw["1.use_emotion"] != true || vw["1.mute_audio"] != false {
		t.Fatalf("visible_when did not round-trip: %#v", wire["visible_when"])
	}
	ol, ok := wire["option_labels"].(map[string]any)
	if !ok || ol["tts"] != "文本配音 (TTS)" || ol["audio"] != "上传驱动音频" {
		t.Fatalf("option_labels did not round-trip: %#v", wire["option_labels"])
	}

	// 2. Explicit visible_when: null is allowed (nullable in decodeStrictJSONObject); omitted new fields default safely.
	var nullVW ColabOneWorkflowField
	if err := unmarshalAndValidateSchema(`{"key":"1.prompt","label":"Prompt","node_id":"1","input_name":"prompt","field_type":"textarea","visible_when":null}`, &nullVW); err != nil {
		t.Fatalf("explicit visible_when: null should pass: %v", err)
	}
	nullVWWire := marshalJSONMap(t, &nullVW)
	if nullVWWire["required"] != false {
		t.Fatalf("default required = %#v, want false", nullVWWire["required"])
	}
	if _, ok := nullVWWire["options"].([]any); !ok {
		t.Fatalf("options should marshal as [], got %#v", nullVWWire["options"])
	}
	if olVal, exists := nullVWWire["option_labels"]; exists && olVal == nil {
		t.Fatalf("option_labels should not marshal as null when present")
	}

	// 3. Exact boundary lengths accepted (description=400 runes, group=64 runes).
	var maxBoundary ColabOneWorkflowField
	if err := unmarshalAndValidateSchema(mustMarshalJSON(t, map[string]any{
		"key":         "1.text",
		"label":       "Text",
		"node_id":     "1",
		"input_name":  "text",
		"field_type":  "text",
		"description": strings.Repeat("文", 400),
		"group":       strings.Repeat("g", 64),
	}), &maxBoundary); err != nil {
		t.Fatalf("boundary description/group lengths should pass: %v", err)
	}

	// 4. Reject overlong description/group, invalid visible_when keys/values, and non-nullable nulls.
	baseField := func() map[string]any {
		return map[string]any{
			"key":        "1.text",
			"label":      "Text",
			"node_id":    "1",
			"input_name": "text",
			"field_type": "text",
		}
	}
	badCases := []struct {
		name   string
		mutate func(m map[string]any)
	}{
		{"overlongDescription", func(m map[string]any) { m["description"] = strings.Repeat("d", 401) }},
		{"overlongGroup", func(m map[string]any) { m["group"] = strings.Repeat("g", 65) }},
		{"visibleWhenBadKeyNoDot", func(m map[string]any) { m["visible_when"] = map[string]any{"no_dot": "tts"} }},
		{"visibleWhenBadKeyTraversal", func(m map[string]any) { m["visible_when"] = map[string]any{"../evil.mode": "tts"} }},
		{"visibleWhenBadKeyEmptyInput", func(m map[string]any) { m["visible_when"] = map[string]any{"1.": "tts"} }},
		{"visibleWhenBadKeyEmptyNode", func(m map[string]any) { m["visible_when"] = map[string]any{".mode": "tts"} }},
		{"visibleWhenIntValue", func(m map[string]any) { m["visible_when"] = map[string]any{"pipeline.audio_source": 123} }},
		{"visibleWhenFloatValue", func(m map[string]any) { m["visible_when"] = map[string]any{"pipeline.audio_source": 1.5} }},
		{"visibleWhenNullValue", func(m map[string]any) { m["visible_when"] = map[string]any{"pipeline.audio_source": nil} }},
		{"visibleWhenSliceValue", func(m map[string]any) { m["visible_when"] = map[string]any{"pipeline.audio_source": []string{"tts"}} }},
		{"visibleWhenObjectValue", func(m map[string]any) {
			m["visible_when"] = map[string]any{"pipeline.audio_source": map[string]string{"a": "b"}}
		}},
		{"optionLabelsNull", func(m map[string]any) { m["option_labels"] = nil }},
		{"optionLabelsNonStringVal", func(m map[string]any) { m["option_labels"] = map[string]any{"tts": 123} }},
		{"descriptionNull", func(m map[string]any) { m["description"] = nil }},
		{"groupNull", func(m map[string]any) { m["group"] = nil }},
		{"requiredNull", func(m map[string]any) { m["required"] = nil }},
		{"unknownField", func(m map[string]any) { m["unknown_extra"] = true }},
	}
	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			m := baseField()
			tc.mutate(m)
			var f ColabOneWorkflowField
			if err := unmarshalAndValidateSchema(mustMarshalJSON(t, m), &f); err == nil {
				t.Fatalf("%s: expected validation error", tc.name)
			}
		})
	}
}

func TestWorkflowTemplateExtendedSchemaValidation(t *testing.T) {
	baseTemplate := func() map[string]any {
		return map[string]any{
			"id":       "wf_digital_human",
			"name":     "Digital Human Template",
			"workflow": sampleAPIWorkflow(),
		}
	}

	// 1. Omitted commercial_ok defaults to true when unmarshaled from JSON.
	var defaultCommercial ColabOneWorkflowTemplate
	if err := unmarshalAndValidateSchema(mustMarshalJSON(t, baseTemplate()), &defaultCommercial); err != nil {
		t.Fatalf("template with omitted commercial_ok failed: %v", err)
	}
	defWire := marshalJSONMap(t, &defaultCommercial)
	if defWire["commercial_ok"] != true {
		t.Fatalf("omitted commercial_ok = %#v, want true", defWire["commercial_ok"])
	}
	if npVal, exists := defWire["node_patches"]; exists {
		if _, ok := npVal.([]any); !ok {
			t.Fatalf("node_patches should marshal as [] when present, got %#v", npVal)
		}
	}

	// 2. Explicit commercial_ok=false is preserved along with license_notice.
	mFalse := baseTemplate()
	mFalse["commercial_ok"] = false
	mFalse["license"] = "Non-Commercial Research License"
	mFalse["license_notice"] = "Research use only; contact upstream for commercial licensing."
	var explicitFalse ColabOneWorkflowTemplate
	if err := unmarshalAndValidateSchema(mustMarshalJSON(t, mFalse), &explicitFalse); err != nil {
		t.Fatalf("template with explicit commercial_ok=false failed: %v", err)
	}
	falseWire := marshalJSONMap(t, &explicitFalse)
	if falseWire["commercial_ok"] != false {
		t.Fatalf("explicit commercial_ok=false was not preserved: %#v", falseWire["commercial_ok"])
	}
	if falseWire["license"] != "Non-Commercial Research License" || falseWire["license_notice"] != "Research use only; contact upstream for commercial licensing." {
		t.Fatalf("license metadata not preserved: %#v", falseWire)
	}

	// 3. Full extended template with min_disk_mib, recommended_gpu, license, node_patches, and pipeline.
	mFull := baseTemplate()
	mFull["source"] = "builtin"
	mFull["min_vram_mib"] = 24576
	mFull["min_disk_mib"] = 61440
	mFull["recommended_gpu"] = "G4 96 GB / A100 80 GB"
	mFull["license"] = "Apache-2.0"
	mFull["commercial_ok"] = true
	mFull["license_notice"] = ""
	mFull["custom_nodes"] = []map[string]any{
		{
			"name":   "ComfyUI-WanVideoWrapper",
			"repo":   "https://github.com/kijai/ComfyUI-WanVideoWrapper.git",
			"commit": strings.Repeat("a", 40),
		},
	}
	mFull["node_patches"] = []map[string]any{
		{
			"node_name": "ComfyUI-WanVideoWrapper",
			"kind":      "wav2vec2_hidden_states_hook",
		},
	}
	mFull["pipeline"] = map[string]any{
		"kind":                  "digital_human",
		"video_backend":         "infinitetalk",
		"default_tts_engine":    "voxcpm2",
		"supported_tts_engines": []string{"voxcpm2", "qwen3_tts"},
		"upscale_mode":          "ai_2x",
		"audio_node_key":        "2.audio",
		"image_node_key":        "1.image",
	}
	var fullTpl ColabOneWorkflowTemplate
	if err := unmarshalAndValidateSchema(mustMarshalJSON(t, mFull), &fullTpl); err != nil {
		t.Fatalf("full extended template failed: %v", err)
	}
	fullWire := marshalJSONMap(t, &fullTpl)
	if fullWire["min_disk_mib"] != float64(61440) || fullWire["recommended_gpu"] != "G4 96 GB / A100 80 GB" || fullWire["license"] != "Apache-2.0" {
		t.Fatalf("unexpected extended template metadata: %#v", fullWire)
	}
	patchesWire, ok := fullWire["node_patches"].([]any)
	if !ok || len(patchesWire) != 1 {
		t.Fatalf("node_patches did not round-trip: %#v", fullWire["node_patches"])
	}
	pipeWire, ok := fullWire["pipeline"].(map[string]any)
	if !ok || pipeWire["kind"] != "digital_human" || pipeWire["video_backend"] != "infinitetalk" || pipeWire["upscale_mode"] != "ai_2x" {
		t.Fatalf("pipeline did not round-trip: %#v", fullWire["pipeline"])
	}

	// 4. Explicit pipeline: null is allowed (nullable in decodeStrictJSONObject).
	mNullPipe := baseTemplate()
	mNullPipe["pipeline"] = nil
	var nullPipeTpl ColabOneWorkflowTemplate
	if err := unmarshalAndValidateSchema(mustMarshalJSON(t, mNullPipe), &nullPipeTpl); err != nil {
		t.Fatalf("explicit pipeline: null should pass: %v", err)
	}

	// 5. Exact boundary lengths/counts accepted (recommended_gpu=120, license=160, license_notice=600, 16 node_patches).
	customNodes16 := make([]map[string]any, 16)
	patches16 := make([]map[string]any, 16)
	for i := range patches16 {
		nodeName := fmt.Sprintf("n%d", i)
		customNodes16[i] = map[string]any{
			"name":   nodeName,
			"repo":   fmt.Sprintf("https://github.com/example/%s.git", nodeName),
			"commit": strings.Repeat("a", 40),
		}
		patches16[i] = map[string]any{
			"node_name": nodeName,
			"kind":      "wav2vec2_hidden_states_hook",
		}
	}
	mBoundary := baseTemplate()
	mBoundary["source"] = "builtin"
	mBoundary["recommended_gpu"] = strings.Repeat("G", 120)
	mBoundary["license"] = strings.Repeat("L", 160)
	mBoundary["license_notice"] = strings.Repeat("N", 600)
	mBoundary["custom_nodes"] = customNodes16
	mBoundary["node_patches"] = patches16
	var boundaryTpl ColabOneWorkflowTemplate
	if err := unmarshalAndValidateSchema(mustMarshalJSON(t, mBoundary), &boundaryTpl); err != nil {
		t.Fatalf("boundary template fields should pass: %v", err)
	}

	// 6. All 10 locked ComfyModelRef target_dir values remain accepted.
	for _, dir := range []string{
		"checkpoints",
		"diffusion_models",
		"loras",
		"vae",
		"text_encoders",
		"clip_vision",
		"wav2vec2",
		"controlnet",
		"upscale_models",
		"audio_encoders",
	} {
		ref := ColabOneComfyModelRef{
			Repo:      "Kijai/WanVideo_comfy",
			Revision:  strings.Repeat("a", 40),
			Path:      "model.safetensors",
			TargetDir: dir,
			Size:      1024,
			SHA256:    strings.Repeat("b", 64),
		}
		if err := ref.Validate(); err != nil {
			t.Fatalf("target_dir %q should be valid: %v", dir, err)
		}
	}

	// 7. Invalid extended template fields rejected.
	patches17 := append(append([]map[string]any{}, patches16...), map[string]any{
		"node_name": "n16",
		"kind":      "wav2vec2_hidden_states_hook",
	})
	badCases := []struct {
		name   string
		mutate func(m map[string]any)
	}{
		{"negativeMinDisk", func(m map[string]any) { m["min_disk_mib"] = -1 }},
		{"overlongRecommendedGPU", func(m map[string]any) { m["recommended_gpu"] = strings.Repeat("G", 121) }},
		{"overlongLicense", func(m map[string]any) { m["license"] = strings.Repeat("L", 161) }},
		{"overlongLicenseNotice", func(m map[string]any) { m["license_notice"] = strings.Repeat("N", 601) }},
		{"commercialOKNull", func(m map[string]any) { m["commercial_ok"] = nil }},
		{"invalidNodePatchTraversal", func(m map[string]any) {
			m["node_patches"] = []map[string]any{{"node_name": "../evil", "kind": "wav2vec2_hidden_states_hook"}}
		}},
		{"invalidNodePatchKind", func(m map[string]any) {
			m["node_patches"] = []map[string]any{{"node_name": "ComfyUI-WanVideoWrapper", "kind": "bad_kind"}}
		}},
		{"tooManyNodePatches", func(m map[string]any) { m["node_patches"] = patches17 }},
		{"invalidNestedPipeline", func(m map[string]any) {
			m["pipeline"] = map[string]any{"kind": "digital_human", "upscale_mode": "invalid_mode"}
		}},
		{"invalidNestedPipelineSeg", func(m map[string]any) {
			m["pipeline"] = map[string]any{"max_segment_seconds": 2.0}
		}},
	}
	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			m := baseTemplate()
			tc.mutate(m)
			var tpl ColabOneWorkflowTemplate
			if err := unmarshalAndValidateSchema(mustMarshalJSON(t, m), &tpl); err == nil {
				t.Fatalf("%s: expected validation error", tc.name)
			}
		})
	}
}

func TestWorkflowRunRequestHFTokenAndPipelineKeysValidation(t *testing.T) {
	// 1. Omitted and empty hf_token accepted.
	for _, raw := range []string{
		`{"workflow_id":"wf_demo-1"}`,
		`{"workflow_id":"wf_demo-1","hf_token":""}`,
	} {
		var req ColabOneWorkflowRunRequest
		if err := unmarshalAndValidateSchema(raw, &req); err != nil {
			t.Fatalf("payload %s should pass: %v", raw, err)
		}
	}

	// 2. Valid hf_token accepted and preserved, including digital human pipeline.* overrides/inputs.
	token := "hf_test_token_value_1234567890"
	validRaw := mustMarshalJSON(t, map[string]any{
		"workflow_id": "wf_digital_human",
		"hf_token":    token,
		"overrides": map[string]any{
			"pipeline.audio_source":        "tts",
			"pipeline.tts_engine":          "voxcpm2",
			"pipeline.pause_seconds":       0.28,
			"pipeline.target_resolution":   "1280x720",
			"pipeline.upscale_mode":        "ai_2x",
			"pipeline.max_segment_seconds": 45,
		},
		"inputs": map[string]string{
			"1.image":                    strings.Repeat("a", 32),
			"pipeline.voice_ref_audio":   strings.Repeat("b", 32),
			"pipeline.emotion_ref_audio": strings.Repeat("c", 32),
		},
	})
	var req ColabOneWorkflowRunRequest
	if err := unmarshalAndValidateSchema(validRaw, &req); err != nil {
		t.Fatalf("valid run request with hf_token failed: %v", err)
	}
	wire := marshalJSONMap(t, &req)
	if wire["hf_token"] != token {
		t.Fatalf("hf_token not preserved: got %#v, want %q", wire["hf_token"], token)
	}

	// 3. Exact 512-rune hf_token boundary accepted.
	maxTok := "hf_" + strings.Repeat("x", 509)
	var maxReq ColabOneWorkflowRunRequest
	if err := unmarshalAndValidateSchema(mustMarshalJSON(t, map[string]any{
		"workflow_id": "wf_demo-1",
		"hf_token":    maxTok,
	}), &maxReq); err != nil {
		t.Fatalf("512-rune hf_token should pass: %v", err)
	}
	if marshalJSONMap(t, &maxReq)["hf_token"] != maxTok {
		t.Fatalf("512-rune hf_token not preserved")
	}

	// 4. Oversized 513-rune hf_token and explicit hf_token: null rejected.
	overTok := "hf_" + strings.Repeat("x", 510)
	var overReq ColabOneWorkflowRunRequest
	if err := unmarshalAndValidateSchema(mustMarshalJSON(t, map[string]any{
		"workflow_id": "wf_demo-1",
		"hf_token":    overTok,
	}), &overReq); err == nil {
		t.Fatalf("513-rune hf_token should fail validation")
	}

	var nullTokReq ColabOneWorkflowRunRequest
	if err := unmarshalAndValidateSchema(`{"workflow_id":"wf_demo-1","hf_token":null}`, &nullTokReq); err == nil {
		t.Fatalf("hf_token: null should fail validation")
	}
}

func TestValidateComfyInputUploadHardening(t *testing.T) {
	validPNG := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nvalid-png-bytes"))
	validWAV := base64.StdEncoding.EncodeToString([]byte("RIFF\x24\x00\x00\x00WAVEfmt "))

	// Valid PNG, WAV, double-dot basename (matching Python _safe_basename), data-URI wrapped, and whitespace-folded base64 payloads pass.
	for _, tc := range []struct {
		name     string
		filename string
		data     string
	}{
		{"validPNG", "portrait.png", validPNG},
		{"validUpperPNG", "PORTRAIT.PNG", validPNG},
		{"validDoubleDotBasename", "a..b.png", validPNG},
		{"validWAV", "voice.wav", validWAV},
		{"validDataURI", "portrait.png", "data:image/png;base64," + validPNG},
		{"validFoldedBase64", "voice.wav", validWAV[:4] + "\r\n " + validWAV[4:]},
	} {
		if err := ValidateComfyInputUpload(tc.filename, tc.data); err != nil {
			t.Fatalf("%s: expected valid upload to pass, got %v", tc.name, err)
		}
	}

	// Exact 16 MiB image boundary passes; 16 MiB + 1 byte is rejected.
	// 16 MiB = 16777216 = 3 * 5592405 + 1 -> 5592405*"AAAA" + "AA==" (exact), +"AAA=" (+1 byte).
	exactImgB64 := strings.Repeat("AAAA", MaxImageInputBytes/3) + "AA=="
	if err := ValidateComfyInputUpload("exact16m.png", exactImgB64); err != nil {
		t.Fatalf("exact 16 MiB image should pass, got %v", err)
	}
	overImgB64 := strings.Repeat("AAAA", MaxImageInputBytes/3) + "AAA="
	if err := ValidateComfyInputUpload("over16m.png", overImgB64); err == nil || !strings.Contains(err.Error(), "最大 16 MB") {
		t.Fatalf("oversized image (>16 MiB): err = %v, want size limit error (16 MB)", err)
	}

	// Oversized audio (>64 MiB): exact decoded +1 byte AND fast-rejected encoded length cap.
	// 64 MiB = 67108864 = 3 * 22369621 + 1 -> 22369621*"AAAA" + "AAA=" (64 MiB + 1 byte).
	overAudioB64 := strings.Repeat("AAAA", MaxAudioInputBytes/3) + "AAA="
	if err := ValidateComfyInputUpload("over64m.wav", overAudioB64); err == nil || !strings.Contains(err.Error(), "最大 64 MB") {
		t.Fatalf("oversized audio (>64 MiB): err = %v, want size limit error (64 MB)", err)
	}
	hugeAudioB64 := strings.Repeat("A", MaxAudioInputBytes+MaxAudioInputBytes/2+8192)
	if err := ValidateComfyInputUpload("huge.wav", hugeAudioB64); err == nil || !strings.Contains(err.Error(), "最大 64 MB") {
		t.Fatalf("huge encoded audio: err = %v, want size limit error (64 MB)", err)
	}

	// 0-byte empty payloads rejected.
	for _, tc := range []struct {
		name string
		data string
	}{
		{"emptyString", ""},
		{"whitespaceOnly", "   \n\t  "},
		{"emptyDataURI", "data:image/png;base64,"},
		{"whitespaceDataURI", "data:audio/wav;base64,   "},
	} {
		if err := ValidateComfyInputUpload("empty.png", tc.data); err == nil || !strings.Contains(err.Error(), "输入文件内容为空") {
			t.Fatalf("%s: err = %v, want empty file error", tc.name, err)
		}
	}

	// Forbidden extensions rejected.
	for _, badFile := range []string{"audio.aac", "payload.exe", "script.sh", "noext", ".png"} {
		if err := ValidateComfyInputUpload(badFile, validPNG); err == nil || !strings.Contains(err.Error(), "不支持的输入文件格式") {
			t.Fatalf("forbidden extension %q: err = %v, want unsupported format error", badFile, err)
		}
	}

	// Path traversal and unsafe filenames rejected.
	for _, badName := range []string{"", "   ", ".", "..", "../a.png", "dir/a.png", `dir\a.png`, "a\x00.png"} {
		if err := ValidateComfyInputUpload(badName, validPNG); err == nil || !strings.Contains(err.Error(), "输入文件名不合法") {
			t.Fatalf("unsafe filename %q: err = %v, want invalid filename error", badName, err)
		}
	}

	// Invalid base64 payloads rejected.
	for _, tc := range []struct {
		name string
		data string
	}{
		{"badLengthMod4", "abc"},
		{"illegalChars", "!!!!"},
		{"earlyPadding", strings.Repeat("AAAA", 1023) + "AA==" + "AAAA"},
		{"malformedDataURI", "data:image/png;base64"},
	} {
		if err := ValidateComfyInputUpload("bad.png", tc.data); err == nil {
			t.Fatalf("invalid base64 %s: expected error", tc.name)
		}
	}
}

func TestWorkflowRunRequestRedactsHFTokenInStringAndGoString(t *testing.T) {
	const secret = "hf_super_secret_run_token_987654321"
	req := ColabOneWorkflowRunRequest{
		WorkflowID: "digital-human-infinitetalk",
		Overrides:  map[string]any{"pipeline.tts_engine": "voxcpm2"},
		Inputs:     map[string]string{"1.image": strings.Repeat("a", 32)},
		HFToken:    secret,
	}
	for _, formatted := range []string{
		req.String(),
		fmt.Sprintf("%s", req),
		fmt.Sprintf("%v", req),
		fmt.Sprintf("%#v", req),
		fmt.Sprintf("%v", &req),
	} {
		if strings.Contains(formatted, secret) {
			t.Fatalf("ColabOneWorkflowRunRequest leaked HFToken in %q", formatted)
		}
		if !strings.Contains(formatted, "[redacted]") {
			t.Fatalf("expected [redacted] marker in %q", formatted)
		}
	}
}

func TestExtractWorkflowFieldsSkipsWanVideoTextEncodeCachedLoaderKeys(t *testing.T) {
	wf := map[string]any{
		"5": map[string]any{
			"class_type": "WanVideoTextEncodeCached",
			"_meta":      map[string]any{"title": "画面与口播风格提示词"},
			"inputs": map[string]any{
				"model_name":      "umt5-xxl-enc-fp8_e4m3fn.safetensors",
				"precision":       "bf16",
				"positive_prompt": "clear studio portrait speaking naturally",
				"negative_prompt": "blurry, distorted",
				"quantization":    "fp8_e4m3fn",
				"use_disk_cache":  true,
				"device":          "gpu",
				"load_device":     "offload_device",
				"base_precision":  "bf16",
				"attention_mode":  "sdpa",
			},
		},
	}

	fields := ExtractWorkflowFields(wf)
	gotKeys := make([]string, len(fields))
	for i, f := range fields {
		gotKeys[i] = f.Key
	}
	wantKeys := []string{"5.positive_prompt", "5.negative_prompt"}
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("extracted keys = %v, want %v", gotKeys, wantKeys)
	}
}

func TestComfyModelRefAudioEncodersAndCatalogCrossRefValidation(t *testing.T) {
	revA := strings.Repeat("a", 40)
	shaB := strings.Repeat("b", 64)

	// 1. "audio_encoders" MUST be accepted in ColabOneComfyModelRef.TargetDir.
	whisperRef := ColabOneComfyModelRef{
		Repo:      "Kijai/WanVideo_comfy",
		Revision:  revA,
		Path:      "HuMo/whisper_large_v3_encoder_fp16.safetensors",
		TargetDir: "audio_encoders",
		Size:      1693870272,
		SHA256:    shaB,
	}
	if err := whisperRef.Validate(); err != nil {
		t.Fatalf("audio_encoders target_dir should be accepted, got %v", err)
	}

	makeBaseCatalog := func() ColabOneComfyCatalog {
		return ColabOneComfyCatalog{
			Repo:      "https://github.com/Comfy-Org/ComfyUI.git",
			Tag:       "v0.46.0",
			Commit:    revA,
			Port:      8188,
			ProxyPort: 8189,
			TtsEngines: []ColabOneComfyTtsEngineSpec{
				{
					ID:         "voxcpm2",
					Name:       "VoxCPM2",
					Repo:       "openbmb/VoxCPM2",
					Revision:   revA,
					SampleRate: 48000,
				},
			},
			Templates: []ColabOneWorkflowTemplate{
				{
					ID:     "wf-cross-ref",
					Name:   "CrossRef Test",
					Source: "builtin",
					CustomNodes: []ColabOneComfyNodeSpec{
						{
							Name:                "ComfyUI-WanVideoWrapper",
							Repo:                "https://github.com/kijai/ComfyUI-WanVideoWrapper.git",
							Commit:              revA,
							InstallRequirements: true,
						},
					},
					NodePatches: []ColabOneComfyNodePatch{
						{
							NodeName: "ComfyUI-WanVideoWrapper",
							Kind:     "wav2vec2_hidden_states_hook",
						},
					},
					Models: []ColabOneComfyModelRef{whisperRef},
					Pipeline: &ColabOneComfyPipelineSpec{
						Kind:                "digital_human",
						VideoBackend:        "longcat_avatar",
						DefaultTtsEngine:    "voxcpm2",
						SupportedTtsEngines: []string{"voxcpm2"},
					},
					Workflow: sampleAPIWorkflow(),
				},
			},
		}
	}

	// Valid catalog with matching node_patches and supported_tts_engines passes.
	validCat := makeBaseCatalog()
	if err := validCat.Validate(); err != nil {
		t.Fatalf("valid cross-referenced catalog failed: %v", err)
	}

	// 2. Unknown node_patches[*].node_name rejected (both with empty and non-empty custom_nodes).
	badPatchCat := makeBaseCatalog()
	badPatchCat.Templates[0].NodePatches = []ColabOneComfyNodePatch{
		{NodeName: "ComfyUI-UnknownNode", Kind: "wav2vec2_hidden_states_hook"},
	}
	if err := badPatchCat.Validate(); err == nil || !strings.Contains(err.Error(), "节点补丁引用的自定义节点不存在") {
		t.Fatalf("unknown node_patches[*].node_name: err = %v", err)
	}

	badPatchEmptyNodesCat := makeBaseCatalog()
	badPatchEmptyNodesCat.Templates[0].CustomNodes = nil
	badPatchEmptyNodesCat.Templates[0].NodePatches = []ColabOneComfyNodePatch{
		{NodeName: "ComfyUI-GhostNode", Kind: "wav2vec2_hidden_states_hook"},
	}
	if err := badPatchEmptyNodesCat.Validate(); err == nil || !strings.Contains(err.Error(), "节点补丁引用的自定义节点不存在") {
		t.Fatalf("node_patches with empty custom_nodes: err = %v", err)
	}

	// 3. Unknown pipeline.supported_tts_engines and default_tts_engine rejected.
	badSupportedTTS := makeBaseCatalog()
	badSupportedTTS.Templates[0].Pipeline.SupportedTtsEngines = []string{"voxcpm2", "nonexistent_tts"}
	if err := badSupportedTTS.Validate(); err == nil || !strings.Contains(err.Error(), "引用了未定义的 TTS 引擎") {
		t.Fatalf("unknown pipeline.supported_tts_engines: err = %v", err)
	}

	badDefaultTTS := makeBaseCatalog()
	badDefaultTTS.Templates[0].Pipeline.DefaultTtsEngine = "ghost_tts"
	if err := badDefaultTTS.Validate(); err == nil || !strings.Contains(err.Error(), "引用了未定义的默认 TTS 引擎") {
		t.Fatalf("unknown pipeline.default_tts_engine: err = %v", err)
	}
}

func TestRound2WorkflowTemplateAuthorityAndNodePatchValidation(t *testing.T) {
	revA := strings.Repeat("a", 40)
	shaB := strings.Repeat("b", 64)

	// 1. Direct ColabOneWorkflowTemplate.Validate() rejects non-empty NodePatches when CustomNodes is empty.
	directEmptyNodes := ColabOneWorkflowTemplate{
		ID:     "wf_direct_patch_empty",
		Name:   "Direct Patch Empty Nodes",
		Source: "builtin",
		NodePatches: []ColabOneComfyNodePatch{
			{NodeName: "ComfyUI-WanVideoWrapper", Kind: "wav2vec2_hidden_states_hook"},
		},
		Workflow: sampleAPIWorkflow(),
	}
	if err := directEmptyNodes.Validate(); err == nil || !strings.Contains(err.Error(), "节点补丁引用的自定义节点不存在") {
		t.Fatalf("direct Validate() with NodePatches and empty CustomNodes: err = %v", err)
	}

	// Direct Validate() also rejects NodePatches referencing an undeclared node when CustomNodes is non-empty.
	directMismatchNodes := ColabOneWorkflowTemplate{
		ID:     "wf_direct_patch_mismatch",
		Name:   "Direct Patch Mismatch",
		Source: "builtin",
		CustomNodes: []ColabOneComfyNodeSpec{
			{
				Name:   "ComfyUI-OtherNode",
				Repo:   "https://github.com/example/ComfyUI-OtherNode.git",
				Commit: revA,
			},
		},
		NodePatches: []ColabOneComfyNodePatch{
			{NodeName: "ComfyUI-WanVideoWrapper", Kind: "wav2vec2_hidden_states_hook"},
		},
		Workflow: sampleAPIWorkflow(),
	}
	if err := directMismatchNodes.Validate(); err == nil || !strings.Contains(err.Error(), "节点补丁引用的自定义节点不存在") {
		t.Fatalf("direct Validate() with mismatched NodePatches: err = %v", err)
	}

	// 2. Non-builtin templates (explicit "custom" and defaulted "") reject every privileged dependency field.
	for _, src := range []string{"", "custom"} {
		srcLabel := src
		if srcLabel == "" {
			srcLabel = "default_empty"
		}
		cases := []struct {
			name   string
			mutate func(tpl *ColabOneWorkflowTemplate)
		}{
			{
				name: "custom_nodes",
				mutate: func(tpl *ColabOneWorkflowTemplate) {
					tpl.CustomNodes = []ColabOneComfyNodeSpec{{
						Name:   "ComfyUI-TestNode",
						Repo:   "https://github.com/example/ComfyUI-TestNode.git",
						Commit: revA,
					}}
				},
			},
			{
				name: "pip_packages",
				mutate: func(tpl *ColabOneWorkflowTemplate) {
					tpl.PipPackages = []string{"einops>=0.7"}
				},
			},
			{
				name: "node_patches",
				mutate: func(tpl *ColabOneWorkflowTemplate) {
					tpl.NodePatches = []ColabOneComfyNodePatch{{
						NodeName: "ComfyUI-TestNode",
						Kind:     "wav2vec2_hidden_states_hook",
					}}
				},
			},
			{
				name: "models",
				mutate: func(tpl *ColabOneWorkflowTemplate) {
					tpl.Models = []ColabOneComfyModelRef{{
						Repo:      "org/comfy-weights",
						Revision:  revA,
						Path:      "model.safetensors",
						TargetDir: "checkpoints",
						Size:      1024,
						SHA256:    shaB,
					}}
				},
			},
			{
				name: "pipeline",
				mutate: func(tpl *ColabOneWorkflowTemplate) {
					tpl.Pipeline = &ColabOneComfyPipelineSpec{
						Kind:         "digital_human",
						VideoBackend: "infinitetalk",
					}
				},
			},
		}
		for _, tc := range cases {
			t.Run(srcLabel+"/"+tc.name, func(t *testing.T) {
				tpl := ColabOneWorkflowTemplate{
					ID:       "wf_custom_reject_" + tc.name,
					Name:     "Custom Reject " + tc.name,
					Source:   src,
					Workflow: sampleAPIWorkflow(),
				}
				tc.mutate(&tpl)
				if err := tpl.Validate(); err == nil || !strings.Contains(err.Error(), "自定义工作流模板不允许声明远端依赖包、模型下载或流水线配置") {
					t.Fatalf("source=%q with %s: err = %v, want custom template dependency rejection", src, tc.name, err)
				}
			})
		}
	}
}

func TestRound2WorkflowJSONNestingDepthCap(t *testing.T) {
	buildNestedMap := func(levels int) any {
		var v any = "leaf"
		for i := 0; i < levels; i++ {
			v = map[string]any{"next": v}
		}
		return v
	}
	buildNestedSlice := func(levels int) any {
		var v any = 1
		for i := 0; i < levels; i++ {
			v = []any{v}
		}
		return v
	}
	makeWFWithDeepInput := func(deepVal any) map[string]any {
		return map[string]any{
			"1": map[string]any{
				"class_type": "CustomNode",
				"inputs": map[string]any{
					"deep": deepVal,
				},
			},
		}
	}

	// workflow(0) -> node(1) -> inputs(2) -> 29 nested containers(3..31) -> scalar(32): exact limit passes.
	for _, tc := range []struct {
		name string
		val  any
	}{
		{"mapAtLimit32", buildNestedMap(29)},
		{"sliceAtLimit32", buildNestedSlice(29)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateComfyAPIWorkflow(makeWFWithDeepInput(tc.val)); err != nil {
				t.Fatalf("%s should pass at exact 32-layer limit, got %v", tc.name, err)
			}
		})
	}

	// 30 nested containers inside inputs puts the innermost container at depth 32 -> exceeds 32-layer limit.
	const wantErr = "工作流 JSON 嵌套层级超过 32 层上限"
	for _, tc := range []struct {
		name string
		val  any
	}{
		{"mapDepth33", buildNestedMap(30)},
		{"sliceDepth33", buildNestedSlice(30)},
		{"mapDepth48", buildNestedMap(45)},
		{"sliceDepth48", buildNestedSlice(45)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wf := makeWFWithDeepInput(tc.val)
			if _, err := ValidateComfyAPIWorkflow(wf); err == nil || err.Error() != wantErr {
				t.Fatalf("ValidateComfyAPIWorkflow(%s): err = %v, want %q", tc.name, err, wantErr)
			}
			tpl := ColabOneWorkflowTemplate{
				ID:       "wf_deep",
				Name:     "Deep Flow",
				Workflow: wf,
			}
			if err := tpl.Validate(); err == nil || err.Error() != wantErr {
				t.Fatalf("Template.Validate(%s): err = %v, want %q", tc.name, err, wantErr)
			}
			imp := ColabOneWorkflowImportRequest{
				Name:     "Deep Import",
				Workflow: wf,
			}
			if err := imp.Validate(); err == nil || err.Error() != wantErr {
				t.Fatalf("ImportRequest.Validate(%s): err = %v, want %q", tc.name, err, wantErr)
			}
		})
	}
}

func TestRound2RunRequestHFTokenNodeNameAndPipFlags(t *testing.T) {
	revA := strings.Repeat("a", 40)

	// 1. ColabOneWorkflowRunRequest.Validate() rejects \r, \n, control chars, and embedded/unicode whitespace in HFToken.
	badTokens := []struct {
		name  string
		token string
	}{
		{"cr", "hf_abc\rdef"},
		{"lf", "hf_abc\ndef"},
		{"crlf", "hf_abc\r\ndef"},
		{"trailingLF", "hf_abcdef\n"},
		{"tab", "hf_abc\tdef"},
		{"nul", "hf_abc\x00def"},
		{"del", "hf_abc\x7fdef"},
		{"embeddedSpace", "hf_abc def"},
		{"unicodeSpace", "hf_abc\u3000def"},
		{"leadingNBSP", "\u00a0hf_abcdef"},
	}
	for _, tc := range badTokens {
		t.Run("hf_token/"+tc.name, func(t *testing.T) {
			req := ColabOneWorkflowRunRequest{
				WorkflowID: "wf_demo-1",
				HFToken:    tc.token,
			}
			if err := req.Validate(); err == nil || !strings.Contains(err.Error(), "hf_token 包含非法空白或控制字符") {
				t.Fatalf("direct Validate(%s): err = %v", tc.name, err)
			}
			var decoded ColabOneWorkflowRunRequest
			raw := mustMarshalJSON(t, map[string]any{
				"workflow_id": "wf_demo-1",
				"hf_token":    tc.token,
			})
			if err := unmarshalAndValidateSchema(raw, &decoded); err == nil || !strings.Contains(err.Error(), "hf_token 包含非法空白或控制字符") {
				t.Fatalf("unmarshal+Validate(%s): err = %v", tc.name, err)
			}
		})
	}

	// Leading/trailing ASCII spaces are trimmed cleanly on a valid token.
	trimReq := ColabOneWorkflowRunRequest{
		WorkflowID: "wf_demo-1",
		HFToken:    "  hf_clean_token_42  ",
	}
	if err := trimReq.Validate(); err != nil {
		t.Fatalf("ASCII-padded clean HFToken should pass: %v", err)
	}
	if trimReq.HFToken != "hf_clean_token_42" {
		t.Fatalf("trimmed HFToken = %q, want %q", trimReq.HFToken, "hf_clean_token_42")
	}

	// 2. Leading-dot custom node names rejected in both ColabOneComfyNodeSpec and ColabOneComfyNodePatch.
	for _, badNode := range []string{".hidden", ".git", " .leading-dot "} {
		nodeSpec := ColabOneComfyNodeSpec{
			Name:   badNode,
			Repo:   "https://github.com/example/ComfyUI-Node.git",
			Commit: revA,
		}
		if err := nodeSpec.Validate(); err == nil || !strings.Contains(err.Error(), "自定义节点名称不合法") {
			t.Fatalf("NodeSpec leading-dot %q: err = %v", badNode, err)
		}
		patchSpec := ColabOneComfyNodePatch{
			NodeName: badNode,
			Kind:     "wav2vec2_hidden_states_hook",
		}
		if err := patchSpec.Validate(); err == nil || !strings.Contains(err.Error(), "自定义节点名称不合法") {
			t.Fatalf("NodePatch leading-dot %q: err = %v", badNode, err)
		}
	}

	// 3. Pip flag injections (e.g. "numpy --pre") rejected in both workflow templates and TTS engine specs.
	for _, badPip := range []string{
		"numpy --pre",
		"numpy -e .",
		"numpy -i https://evil.invalid/simple",
		"torch --extra-index-url https://evil.invalid",
		"-r requirements.txt",
		"--pre",
	} {
		tpl := ColabOneWorkflowTemplate{
			ID:          "wf_bad_pip",
			Name:        "Bad Pip",
			Source:      "builtin",
			PipPackages: []string{badPip},
			Workflow:    sampleAPIWorkflow(),
		}
		if err := tpl.Validate(); err == nil || !strings.Contains(err.Error(), "非法的 pip 依赖声明") {
			t.Fatalf("Template bad pip %q: err = %v", badPip, err)
		}
		tts := ColabOneComfyTtsEngineSpec{
			ID:          "vox_bad_pip",
			Name:        "Vox Bad Pip",
			Repo:        "openbmb/VoxCPM2",
			Revision:    revA,
			PipPackages: []string{badPip},
		}
		if err := tts.Validate(); err == nil || !strings.Contains(err.Error(), "非法的 pip 依赖声明") {
			t.Fatalf("TtsEngine bad pip %q: err = %v", badPip, err)
		}
	}

	// Legitimate hyphenated package names continue to pass.
	goodPipTpl := ColabOneWorkflowTemplate{
		ID:          "wf_good_pip",
		Name:        "Good Pip",
		Source:      "builtin",
		PipPackages: []string{"huggingface-hub>=0.25", "soundfile>=0.12"},
		Workflow:    sampleAPIWorkflow(),
	}
	if err := goodPipTpl.Validate(); err != nil {
		t.Fatalf("valid hyphenated pip packages should pass: %v", err)
	}
}

func TestRound2ExtractFieldsInvalidNodeIDsAndIntegralFloatCoercion(t *testing.T) {
	// 1. ExtractWorkflowFields skips invalid node IDs while preserving valid node IDs (and "pipeline").
	wf := map[string]any{
		"bad node": map[string]any{
			"class_type": "CLIPTextEncode",
			"inputs":     map[string]any{"text": "should skip spaces"},
		},
		"../evil": map[string]any{
			"class_type": "CLIPTextEncode",
			"inputs":     map[string]any{"text": "should skip traversal"},
		},
		"node.with.dot": map[string]any{
			"class_type": "CLIPTextEncode",
			"inputs":     map[string]any{"text": "should skip dot"},
		},
		"": map[string]any{
			"class_type": "CLIPTextEncode",
			"inputs":     map[string]any{"text": "should skip empty"},
		},
		"1": map[string]any{
			"class_type": "CLIPTextEncode",
			"inputs":     map[string]any{"text": "valid prompt"},
		},
		"pipeline": map[string]any{
			"class_type": "CLIPTextEncode",
			"inputs":     map[string]any{"prompt": "valid pipeline prompt"},
		},
	}
	fields := ExtractWorkflowFields(wf)
	gotKeys := make([]string, len(fields))
	for i, f := range fields {
		gotKeys[i] = f.Key
		if err := f.Validate(); err != nil {
			t.Fatalf("extracted field %q should always pass Validate(), got %v", f.Key, err)
		}
	}
	wantKeys := []string{"1.text", "pipeline.prompt"}
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("ExtractWorkflowFields keys = %v, want %v", gotKeys, wantKeys)
	}

	// 2. ApplyWorkflowOverrides coerces integral floats (25.0) to int64 on existing integer inputs,
	// preserves non-integral floats, and leaves existing float inputs as float64.
	overrideWF := map[string]any{
		"6": map[string]any{
			"class_type": "KSampler",
			"inputs": map[string]any{
				"steps":       20,                // int
				"seed":        int64(42),         // int64
				"width":       json.Number("512"), // integral json.Number
				"height":      512,               // int overridden with fractional float
				"cfg":         7.5,               // float64 overridden with integral float
			},
		},
	}
	applied, err := ApplyWorkflowOverrides(overrideWF, map[string]any{
		"6.steps":  float64(25.0),
		"6.seed":   float32(16.0),
		"6.width":  json.Number("768.0"),
		"6.height": float64(512.5),
		"6.cfg":    float64(8.0),
	}, nil)
	if err != nil {
		t.Fatalf("ApplyWorkflowOverrides failed: %v", err)
	}
	inputs := applied["6"].(map[string]any)["inputs"].(map[string]any)

	stepsVal, ok := inputs["steps"].(int64)
	if !ok || stepsVal != 25 {
		t.Fatalf("6.steps = %#v (%T), want int64(25)", inputs["steps"], inputs["steps"])
	}
	seedVal, ok := inputs["seed"].(int64)
	if !ok || seedVal != 16 {
		t.Fatalf("6.seed = %#v (%T), want int64(16)", inputs["seed"], inputs["seed"])
	}
	widthVal, ok := inputs["width"].(int64)
	if !ok || widthVal != 768 {
		t.Fatalf("6.width = %#v (%T), want int64(768)", inputs["width"], inputs["width"])
	}
	heightVal, ok := inputs["height"].(float64)
	if !ok || heightVal != 512.5 {
		t.Fatalf("6.height = %#v (%T), want float64(512.5)", inputs["height"], inputs["height"])
	}
	cfgVal, ok := inputs["cfg"].(float64)
	if !ok || cfgVal != 8.0 {
		t.Fatalf("6.cfg = %#v (%T), want float64(8.0)", inputs["cfg"], inputs["cfg"])
	}
}

func TestComfyTtsVoicePresetValidation(t *testing.T) {
	revA := strings.Repeat("a", 40)

	// 1. Minimal valid preset populates defaults (gender="female", seed=42), trims name, and round-trips cleanly.
	var minimal ColabOneComfyTtsVoicePreset
	if err := unmarshalAndValidateSchema(`{"id":"shen_zhiyi","name":"  知性女主播 · 沈知意  "}`, &minimal); err != nil {
		t.Fatalf("minimal voice preset failed: %v", err)
	}
	if minimal.ID != "shen_zhiyi" || minimal.Name != "知性女主播 · 沈知意" {
		t.Fatalf("unexpected id/name: %+v", minimal)
	}
	if minimal.Gender != "female" {
		t.Fatalf("default gender = %q, want female", minimal.Gender)
	}
	if minimal.Seed != 42 {
		t.Fatalf("default seed = %d, want 42", minimal.Seed)
	}
	if minimal.Style != "" || minimal.Description != "" || minimal.Prompt != "" {
		t.Fatalf("unexpected non-empty optional strings: %+v", minimal)
	}

	minWire := marshalJSONMap(t, &minimal)
	if minWire["id"] != "shen_zhiyi" || minWire["name"] != "知性女主播 · 沈知意" || minWire["gender"] != "female" || minWire["seed"] != float64(42) {
		t.Fatalf("unexpected minimal voice preset wire: %#v", minWire)
	}
	var roundTripped ColabOneComfyTtsVoicePreset
	if err := unmarshalAndValidateSchema(mustMarshalJSON(t, &minimal), &roundTripped); err != nil {
		t.Fatalf("round-trip minimal voice preset failed: %v", err)
	}
	if !reflect.DeepEqual(roundTripped, minimal) {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", roundTripped, minimal)
	}

	// 2. All allowed genders and explicit boundary seeds (0 and 2147483647) accepted and preserved.
	for _, tc := range []struct {
		gender string
		seed   int
	}{
		{"female", 0},
		{"male", 202601},
		{"neutral", 2147483647},
	} {
		var p ColabOneComfyTtsVoicePreset
		raw := mustMarshalJSON(t, map[string]any{
			"id":          "voice_" + tc.gender,
			"name":        "Voice " + tc.gender,
			"gender":      tc.gender,
			"style":       "播音风格",
			"description": "清晰自然的中文人声",
			"prompt":      "Clear warm articulate studio voice",
			"seed":        tc.seed,
		})
		if err := unmarshalAndValidateSchema(raw, &p); err != nil {
			t.Fatalf("valid preset gender=%s seed=%d failed: %v", tc.gender, tc.seed, err)
		}
		if p.Gender != tc.gender || p.Seed != tc.seed {
			t.Fatalf("preset gender/seed not preserved: got (%q, %d), want (%q, %d)", p.Gender, p.Seed, tc.gender, tc.seed)
		}
	}

	// 3. Exact max-length boundaries (id=64, name=80 runes, style=80 runes, description=240 runes, prompt=600 runes) accepted.
	var maxPreset ColabOneComfyTtsVoicePreset
	maxRaw := mustMarshalJSON(t, map[string]any{
		"id":          strings.Repeat("a", 64),
		"name":        strings.Repeat("名", 80),
		"gender":      "female",
		"style":       strings.Repeat("风", 80),
		"description": strings.Repeat("描", 240),
		"prompt":      strings.Repeat("提", 600),
		"seed":        2147483647,
	})
	if err := unmarshalAndValidateSchema(maxRaw, &maxPreset); err != nil {
		t.Fatalf("max boundary voice preset failed: %v", err)
	}

	// 4. Direct Go struct defaults: empty Gender -> "female", Seed 0 stays 0.
	direct := ColabOneComfyTtsVoicePreset{
		ID:   "direct-voice_1",
		Name: "Direct Voice",
	}
	if err := direct.Validate(); err != nil {
		t.Fatalf("direct voice preset Validate failed: %v", err)
	}
	if direct.Gender != "female" || direct.Seed != 0 {
		t.Fatalf("direct voice preset defaults = (%q, %d), want (female, 0)", direct.Gender, direct.Seed)
	}

	// 5. Invalid voice preset field values rejected.
	basePreset := func() map[string]any {
		return map[string]any{
			"id":     "shen_zhiyi",
			"name":   "知性女主播 · 沈知意",
			"gender": "female",
			"seed":   202601,
		}
	}
	badPresetCases := []struct {
		name   string
		mutate func(m map[string]any)
	}{
		{"emptyID", func(m map[string]any) { m["id"] = "" }},
		{"overlongID", func(m map[string]any) { m["id"] = strings.Repeat("a", 65) }},
		{"traversalID", func(m map[string]any) { m["id"] = "../evil" }},
		{"spaceID", func(m map[string]any) { m["id"] = "bad id" }},
		{"dotID", func(m map[string]any) { m["id"] = "bad.id" }},
		{"unicodeID", func(m map[string]any) { m["id"] = "沈知意" }},
		{"emptyName", func(m map[string]any) { m["name"] = "" }},
		{"blankName", func(m map[string]any) { m["name"] = "   " }},
		{"overlongName", func(m map[string]any) { m["name"] = strings.Repeat("名", 81) }},
		{"emptyGender", func(m map[string]any) { m["gender"] = "" }},
		{"invalidGender", func(m map[string]any) { m["gender"] = "robot" }},
		{"uppercaseGender", func(m map[string]any) { m["gender"] = "FEMALE" }},
		{"overlongStyle", func(m map[string]any) { m["style"] = strings.Repeat("风", 81) }},
		{"overlongDescription", func(m map[string]any) { m["description"] = strings.Repeat("描", 241) }},
		{"overlongPrompt", func(m map[string]any) { m["prompt"] = strings.Repeat("提", 601) }},
		{"negativeSeed", func(m map[string]any) { m["seed"] = -1 }},
		{"seedTooLarge", func(m map[string]any) { m["seed"] = 2147483648 }},
		{"floatSeed", func(m map[string]any) { m["seed"] = 42.5 }},
		{"stringSeed", func(m map[string]any) { m["seed"] = "42" }},
		{"unknownField", func(m map[string]any) { m["extra"] = true }},
	}
	for _, tc := range badPresetCases {
		t.Run("preset/"+tc.name, func(t *testing.T) {
			m := basePreset()
			tc.mutate(m)
			var p ColabOneComfyTtsVoicePreset
			if err := unmarshalAndValidateSchema(mustMarshalJSON(t, m), &p); err == nil {
				t.Fatalf("preset/%s: expected validation error", tc.name)
			}
		})
	}

	// 6. Strict JSON decoding: unknown fields, wrong-case keys, NaN/Inf/overflow, nulls, and trailing garbage rejected.
	for _, raw := range []string{
		`null`,
		`{"id":"v1","name":"V1","extra":1}`,
		`{"ID":"v1","name":"V1"}`,
		`{"id":"v1","name":"V1","Gender":"male"}`,
		`{"id":"v1","name":"V1","Seed":7}`,
		`{"id":null,"name":"V1"}`,
		`{"id":"v1","name":null}`,
		`{"id":"v1","name":"V1","gender":null}`,
		`{"id":"v1","name":"V1","style":null}`,
		`{"id":"v1","name":"V1","description":null}`,
		`{"id":"v1","name":"V1","prompt":null}`,
		`{"id":"v1","name":"V1","seed":null}`,
		`{"id":"v1","name":"V1","seed":NaN}`,
		`{"id":"v1","name":"V1","seed":Infinity}`,
		`{"id":"v1","name":"V1","seed":-Infinity}`,
		`{"id":"v1","name":"V1","seed":1e9999}`,
		`{"id":"v1","name":"V1"} {"extra":true}`,
	} {
		var p ColabOneComfyTtsVoicePreset
		if err := unmarshalAndValidateSchema(raw, &p); err == nil {
			t.Fatalf("strict JSON preset payload %s should fail", raw)
		}
	}

	// 7. ColabOneComfyTtsEngineSpec integration with VoicePresets: 5 canonical presets round-trip, 24 accepted, 25 rejected.
	makeEngineWithPresets := func(presets any) map[string]any {
		return map[string]any{
			"id":            "voxcpm2",
			"name":          "VoxCPM2",
			"repo":          "openbmb/VoxCPM2",
			"revision":      revA,
			"voice_presets": presets,
		}
	}

	canonicalFive := []map[string]any{
		{"id": "shen_zhiyi", "name": "知性女主播 · 沈知意", "gender": "female", "style": "知性主播", "seed": 202601, "prompt": "A 26-year-old Chinese female host"},
		{"id": "lin_xiaoman", "name": "甜美活力 · 林小满", "gender": "female", "style": "甜美活力", "seed": 202602, "prompt": "A 22-year-old Chinese young woman"},
		{"id": "su_wanqing", "name": "温柔治愈 · 苏晚晴", "gender": "female", "style": "温柔治愈", "seed": 202603, "prompt": "A 25-year-old Chinese young woman"},
		{"id": "gu_qinglan", "name": "干练职场 · 顾清岚", "gender": "female", "style": "干练御姐", "seed": 202604, "prompt": "A 30-year-old mature Chinese professional woman"},
		{"id": "lu_mingwei", "name": "播音气质 · 陆明薇", "gender": "female", "style": "新闻播音", "seed": 202605, "prompt": "A 28-year-old professional Chinese female broadcast news anchor"},
	}
	var engWithFive ColabOneComfyTtsEngineSpec
	if err := unmarshalAndValidateSchema(mustMarshalJSON(t, makeEngineWithPresets(canonicalFive)), &engWithFive); err != nil {
		t.Fatalf("engine with 5 canonical voice presets failed: %v", err)
	}
	if len(engWithFive.VoicePresets) != 5 || engWithFive.VoicePresets[0].ID != "shen_zhiyi" || engWithFive.VoicePresets[4].Seed != 202605 {
		t.Fatalf("unexpected voice presets decoded: %+v", engWithFive.VoicePresets)
	}
	var engRoundTrip ColabOneComfyTtsEngineSpec
	if err := unmarshalAndValidateSchema(mustMarshalJSON(t, &engWithFive), &engRoundTrip); err != nil {
		t.Fatalf("engine with voice presets round-trip failed: %v", err)
	}
	if !reflect.DeepEqual(engRoundTrip, engWithFive) {
		t.Fatalf("engine round-trip mismatch:\ngot:  %+v\nwant: %+v", engRoundTrip, engWithFive)
	}

	// Exact 24 voice presets accepted; 25 rejected.
	presets24 := make([]map[string]any, 24)
	for i := range presets24 {
		presets24[i] = map[string]any{
			"id":   fmt.Sprintf("voice_%02d", i),
			"name": fmt.Sprintf("Voice %02d", i),
			"seed": 100 + i,
		}
	}
	var eng24 ColabOneComfyTtsEngineSpec
	if err := unmarshalAndValidateSchema(mustMarshalJSON(t, makeEngineWithPresets(presets24)), &eng24); err != nil {
		t.Fatalf("engine with 24 voice presets should pass: %v", err)
	}
	if len(eng24.VoicePresets) != 24 || eng24.VoicePresets[0].Gender != "female" {
		t.Fatalf("unexpected 24-preset result: len=%d first=%+v", len(eng24.VoicePresets), eng24.VoicePresets[0])
	}

	presets25 := append(append([]map[string]any{}, presets24...), map[string]any{
		"id":   "voice_24",
		"name": "Voice 24",
	})
	var eng25 ColabOneComfyTtsEngineSpec
	if err := unmarshalAndValidateSchema(mustMarshalJSON(t, makeEngineWithPresets(presets25)), &eng25); err == nil || !strings.Contains(err.Error(), "24") {
		t.Fatalf("engine with 25 voice presets should fail with 24-limit error, got %v", err)
	}

	// Direct Go struct Validate() normalizes nil VoicePresets to non-nil empty slice.
	directEng := ColabOneComfyTtsEngineSpec{
		ID:       "voxcpm2",
		Name:     "VoxCPM2",
		Repo:     "openbmb/VoxCPM2",
		Revision: revA,
	}
	if err := directEng.Validate(); err != nil {
		t.Fatalf("direct engine Validate failed: %v", err)
	}
	if directEng.VoicePresets == nil || len(directEng.VoicePresets) != 0 {
		t.Fatalf("direct engine VoicePresets = %#v, want non-nil empty slice", directEng.VoicePresets)
	}

	// Null voice_presets, [null] element, invalid nested preset, and unknown fields rejected on engine.
	for _, raw := range []string{
		`{"id":"voxcpm2","name":"VoxCPM2","repo":"openbmb/VoxCPM2","revision":"` + revA + `","voice_presets":null}`,
		`{"id":"voxcpm2","name":"VoxCPM2","repo":"openbmb/VoxCPM2","revision":"` + revA + `","voice_presets":[null]}`,
		`{"id":"voxcpm2","name":"VoxCPM2","repo":"openbmb/VoxCPM2","revision":"` + revA + `","voice_presets":[{"id":"v1","name":"V1","gender":"invalid"}]}`,
		`{"id":"voxcpm2","name":"VoxCPM2","repo":"openbmb/VoxCPM2","revision":"` + revA + `","voice_presets":[{"id":"v1","name":"V1","seed":-5}]}`,
		`{"id":"voxcpm2","name":"VoxCPM2","repo":"openbmb/VoxCPM2","revision":"` + revA + `","voice_presets":[{"id":"v1","name":"V1","unknown_key":1}]}`,
		`{"id":"voxcpm2","name":"VoxCPM2","repo":"openbmb/VoxCPM2","revision":"` + revA + `","VoicePresets":[]}`,
	} {
		var eng ColabOneComfyTtsEngineSpec
		if err := unmarshalAndValidateSchema(raw, &eng); err == nil {
			t.Fatalf("invalid engine voice_presets payload %s should fail", raw)
		}
	}
}

func TestComfyParityLongCatKindOverrideLimitFieldRulesAndRealCatalog(t *testing.T) {
	// 1. kind="comfy_standard" (explicit or defaulted) + video_backend="longcat_avatar_native" must be rejected.
	const wantLongCatKindErr = "video_backend 为 longcat_avatar_native 时流水线类型必须为 digital_human"
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"explicitStandard", `{"kind":"comfy_standard","video_backend":"longcat_avatar_native"}`},
		{"omittedKindDefaultsStandard", `{"video_backend":"longcat_avatar_native"}`},
	} {
		t.Run("longcatKind/"+tc.name, func(t *testing.T) {
			var p ColabOneComfyPipelineSpec
			if err := unmarshalAndValidateSchema(tc.raw, &p); err == nil || err.Error() != wantLongCatKindErr {
				t.Fatalf("%s: err = %v, want %q", tc.name, err, wantLongCatKindErr)
			}
		})
	}
	directBadPipe := ColabOneComfyPipelineSpec{
		Kind:         "comfy_standard",
		VideoBackend: "longcat_avatar_native",
	}
	if err := directBadPipe.Validate(); err == nil || err.Error() != wantLongCatKindErr {
		t.Fatalf("direct Validate() comfy_standard + longcat_avatar_native: err = %v, want %q", err, wantLongCatKindErr)
	}

	// 2. WorkflowRunRequest.overrides enforces MaxOverrideStringRunes (200,000 runes).
	exactRunes := strings.Repeat("文", MaxOverrideStringRunes)
	okReq := ColabOneWorkflowRunRequest{
		WorkflowID: "digital-human-longcat",
		Overrides:  map[string]any{"pipeline.script_text": exactRunes},
	}
	if err := okReq.Validate(); err != nil {
		t.Fatalf("exact %d-rune override string should pass, got %v", MaxOverrideStringRunes, err)
	}
	for _, tc := range []struct {
		name string
		val  string
	}{
		{"asciiOver", strings.Repeat("a", MaxOverrideStringRunes+1)},
		{"cjkOver", strings.Repeat("文", MaxOverrideStringRunes+1)},
	} {
		t.Run("overrideLen/"+tc.name, func(t *testing.T) {
			overReq := ColabOneWorkflowRunRequest{
				WorkflowID: "digital-human-longcat",
				Overrides:  map[string]any{"pipeline.script_text": tc.val},
			}
			if err := overReq.Validate(); err == nil || !strings.Contains(err.Error(), "200000") {
				t.Fatalf("direct Validate(%s): err = %v, want 200000-char limit error", tc.name, err)
			}
			var decoded ColabOneWorkflowRunRequest
			raw := mustMarshalJSON(t, map[string]any{
				"workflow_id": "digital-human-longcat",
				"overrides":   map[string]any{"pipeline.script_text": tc.val},
			})
			if err := unmarshalAndValidateSchema(raw, &decoded); err == nil || !strings.Contains(err.Error(), "200000") {
				t.Fatalf("unmarshal+Validate(%s): err = %v, want 200000-char limit error", tc.name, err)
			}
		})
	}

	// 3. ColabOneWorkflowField enforces step > 0, options <= 128, option_labels <= 128, and select default in options.
	for _, badStep := range []float64{0, -0.5, -1} {
		f := ColabOneWorkflowField{
			Key: "1.cfg", Label: "CFG", NodeID: "1", InputName: "cfg", FieldType: "number",
			Step: floatPtr(badStep),
		}
		if err := f.Validate(); err == nil || err.Error() != "step 必须大于 0" {
			t.Fatalf("step=%v: err = %v, want %q", badStep, err, "step 必须大于 0")
		}
	}
	opts128 := make([]string, 128)
	labels128 := make(map[string]string, 128)
	for i := 0; i < 128; i++ {
		k := fmt.Sprintf("opt_%d", i)
		opts128[i] = k
		labels128[k] = "Label " + k
	}
	boundarySelect := ColabOneWorkflowField{
		Key: "1.mode", Label: "Mode", NodeID: "1", InputName: "mode", FieldType: "select",
		Default: "opt_0", Options: opts128, OptionLabels: labels128,
	}
	if err := boundarySelect.Validate(); err != nil {
		t.Fatalf("128 options/option_labels select field should pass: %v", err)
	}

	tooManyOpts := append(append([]string{}, opts128...), "opt_128")
	if err := (&ColabOneWorkflowField{
		Key: "1.mode", Label: "Mode", NodeID: "1", InputName: "mode", FieldType: "select",
		Default: "opt_0", Options: tooManyOpts,
	}).Validate(); err == nil || !strings.Contains(err.Error(), "128") {
		t.Fatalf("129 options should fail validation, got %v", err)
	}

	tooManyLabels := make(map[string]string, 129)
	for k, v := range labels128 {
		tooManyLabels[k] = v
	}
	tooManyLabels["opt_128"] = "Label opt_128"
	if err := (&ColabOneWorkflowField{
		Key: "1.mode", Label: "Mode", NodeID: "1", InputName: "mode", FieldType: "select",
		Default: "opt_0", Options: opts128, OptionLabels: tooManyLabels,
	}).Validate(); err == nil || !strings.Contains(err.Error(), "128") {
		t.Fatalf("129 option_labels should fail validation, got %v", err)
	}

	badSelectDefault := ColabOneWorkflowField{
		Key: "1.mode", Label: "Mode", NodeID: "1", InputName: "mode", FieldType: "select",
		Default: "missing_opt", Options: []string{"fast", "quality"},
	}
	if err := badSelectDefault.Validate(); err == nil || !strings.Contains(err.Error(), "options") {
		t.Fatalf("select default outside options should fail, got %v", err)
	}
	emptyDefaultSelect := ColabOneWorkflowField{
		Key: "1.mode", Label: "Mode", NodeID: "1", InputName: "mode", FieldType: "select",
		Default: "", Options: []string{"fast", "quality"},
	}
	if err := emptyDefaultSelect.Validate(); err != nil {
		t.Fatalf("select field with empty default should pass: %v", err)
	}

	// 4. Real catalog/comfyui.json digital-human-longcat & digital-human-infinitetalk strict validation.
	catalogPath := filepath.Join("..", "..", "..", "catalog", "comfyui.json")
	rawCatalog, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatalf("read catalog/comfyui.json: %v", err)
	}
	var catalog ColabOneComfyCatalog
	if err := unmarshalAndValidateSchema(string(rawCatalog), &catalog); err != nil {
		t.Fatalf("strict unmarshal+validate catalog/comfyui.json: %v", err)
	}
	byID := make(map[string]ColabOneWorkflowTemplate, len(catalog.Templates))
	for _, tpl := range catalog.Templates {
		byID[tpl.ID] = tpl
	}
	longcat, ok := byID["digital-human-longcat"]
	if !ok {
		t.Fatalf("catalog/comfyui.json missing digital-human-longcat template")
	}
	if longcat.Pipeline == nil {
		t.Fatalf("digital-human-longcat missing pipeline spec")
	}
	if longcat.Pipeline.Kind != "digital_human" {
		t.Fatalf("digital-human-longcat pipeline.kind = %q, want digital_human", longcat.Pipeline.Kind)
	}
	if longcat.Pipeline.VideoBackend != "longcat_avatar_native" {
		t.Fatalf("digital-human-longcat pipeline.video_backend = %q, want longcat_avatar_native", longcat.Pipeline.VideoBackend)
	}
	if longcat.Pipeline.UpscaleMode != "auto" {
		t.Fatalf("digital-human-longcat pipeline.upscale_mode = %q, want auto", longcat.Pipeline.UpscaleMode)
	}
	if longcat.CommercialOK {
		t.Fatalf("digital-human-longcat commercial_ok = true, want false")
	}
	if strings.TrimSpace(longcat.LicenseNotice) == "" {
		t.Fatalf("digital-human-longcat license_notice must be non-empty")
	}
	if longcat.License != "MIT / Apache-2.0" {
		t.Fatalf("digital-human-longcat license = %q, want %q", longcat.License, "MIT / Apache-2.0")
	}
	if longcat.MinDiskMib != 79872 {
		t.Fatalf("digital-human-longcat min_disk_mib = %d, want 79872", longcat.MinDiskMib)
	}

	inf, ok := byID["digital-human-infinitetalk"]
	if !ok {
		t.Fatalf("catalog/comfyui.json missing digital-human-infinitetalk template")
	}
	foundSteps := false
	for _, f := range inf.Fields {
		if f.Key == "15.steps" {
			foundSteps = true
			if !strings.Contains(f.Description, "推荐 8 步") {
				t.Fatalf("digital-human-infinitetalk 15.steps description = %q, want to contain %q", f.Description, "推荐 8 步")
			}
		}
	}
	if !foundSteps {
		t.Fatalf("digital-human-infinitetalk missing 15.steps field")
	}
	if inf.License != "Apache-2.0 / CC-BY-4.0 / GPL-3.0" {
		t.Fatalf("digital-human-infinitetalk license = %q, want %q", inf.License, "Apache-2.0 / CC-BY-4.0 / GPL-3.0")
	}
	if inf.Pipeline == nil || inf.Pipeline.DefaultTtsEngine != "moss_tts_v1_5" {
		t.Fatalf("digital-human-infinitetalk default_tts_engine = %+v, want moss_tts_v1_5", inf.Pipeline)
	}
}

func TestRound2ComfySchemaHardeningParity(t *testing.T) {
	revA := strings.Repeat("a", 40)
	revB := strings.Repeat("b", 40)

	// 1. ComfyTtsEngineSpec: source_repo requires 40-hex source_commit.
	t.Run("tts_engine_source_repo_requires_source_commit", func(t *testing.T) {
		var noCommit ColabOneComfyTtsEngineSpec
		rawNoCommit := mustMarshalJSON(t, map[string]any{
			"id":          "test_tts",
			"name":        "Test TTS",
			"repo":        "owner/repo",
			"revision":    revA,
			"source_repo": "https://github.com/owner/repo.git",
		})
		if err := unmarshalAndValidateSchema(rawNoCommit, &noCommit); err == nil || !strings.Contains(err.Error(), "source_commit") {
			t.Fatalf("source_repo without source_commit should fail, got %v", err)
		}

		var emptyCommit ColabOneComfyTtsEngineSpec
		rawEmptyCommit := mustMarshalJSON(t, map[string]any{
			"id":            "test_tts",
			"name":          "Test TTS",
			"repo":          "owner/repo",
			"revision":      revA,
			"source_repo":   "owner/repo",
			"source_commit": "",
		})
		if err := unmarshalAndValidateSchema(rawEmptyCommit, &emptyCommit); err == nil || !strings.Contains(err.Error(), "source_commit") {
			t.Fatalf("source_repo with empty source_commit should fail, got %v", err)
		}

		var withCommit ColabOneComfyTtsEngineSpec
		rawWithCommit := mustMarshalJSON(t, map[string]any{
			"id":            "test_tts",
			"name":          "Test TTS",
			"repo":          "owner/repo",
			"revision":      revA,
			"source_repo":   "owner/repo",
			"source_commit": revB,
		})
		if err := unmarshalAndValidateSchema(rawWithCommit, &withCommit); err != nil {
			t.Fatalf("source_repo with 40-hex source_commit should pass, got %v", err)
		}
	})

	// 2. WorkflowField: visible_when <= 16 entries, string values <= 160 runes.
	t.Run("workflow_field_visible_when_caps", func(t *testing.T) {
		vw16 := make(map[string]any, 16)
		for i := 0; i < 16; i++ {
			vw16[fmt.Sprintf("pipeline.k%d", i)] = strings.Repeat("值", 160)
		}
		validField := ColabOneWorkflowField{
			Key: "1.prompt", Label: "Prompt", NodeID: "1", InputName: "prompt",
			FieldType: "textarea", VisibleWhen: vw16,
		}
		if err := validField.Validate(); err != nil {
			t.Fatalf("16 visible_when entries with 160-rune values should pass, got %v", err)
		}

		vw17 := make(map[string]any, 17)
		for k, v := range vw16 {
			vw17[k] = v
		}
		vw17["pipeline.k16"] = "ok"
		if err := (&ColabOneWorkflowField{
			Key: "1.prompt", Label: "Prompt", NodeID: "1", InputName: "prompt",
			FieldType: "textarea", VisibleWhen: vw17,
		}).Validate(); err == nil || !strings.Contains(err.Error(), "16") {
			t.Fatalf("17 visible_when entries should fail, got %v", err)
		}

		if err := (&ColabOneWorkflowField{
			Key: "1.prompt", Label: "Prompt", NodeID: "1", InputName: "prompt",
			FieldType: "textarea", VisibleWhen: map[string]any{"pipeline.mode": strings.Repeat("值", 161)},
		}).Validate(); err == nil || !strings.Contains(err.Error(), "160") {
			t.Fatalf("161-rune visible_when string value should fail, got %v", err)
		}
	})

	// 3. WorkflowField: default type compatibility against field_type.
	t.Run("workflow_field_default_type_compatibility", func(t *testing.T) {
		goodCases := []ColabOneWorkflowField{
			{Key: "1.steps", Label: "Steps", NodeID: "1", InputName: "steps", FieldType: "integer", Default: 20},
			{Key: "1.steps", Label: "Steps", NodeID: "1", InputName: "steps", FieldType: "integer", Default: float64(20)},
			{Key: "1.steps", Label: "Steps", NodeID: "1", InputName: "steps", FieldType: "integer", Default: json.Number("20")},
			{Key: "1.steps", Label: "Steps", NodeID: "1", InputName: "steps", FieldType: "integer", Default: nil},
			{Key: "1.cfg", Label: "CFG", NodeID: "1", InputName: "cfg", FieldType: "number", Default: 7.5},
			{Key: "1.cfg", Label: "CFG", NodeID: "1", InputName: "cfg", FieldType: "number", Default: 7},
			{Key: "1.cfg", Label: "CFG", NodeID: "1", InputName: "cfg", FieldType: "number", Default: json.Number("7.5")},
			{Key: "1.cfg", Label: "CFG", NodeID: "1", InputName: "cfg", FieldType: "number", Default: nil},
			{Key: "1.flag", Label: "Flag", NodeID: "1", InputName: "flag", FieldType: "boolean", Default: true},
			{Key: "1.flag", Label: "Flag", NodeID: "1", InputName: "flag", FieldType: "boolean", Default: false},
			{Key: "1.flag", Label: "Flag", NodeID: "1", InputName: "flag", FieldType: "boolean", Default: nil},
		}
		for i, f := range goodCases {
			if err := f.Validate(); err != nil {
				t.Fatalf("goodCases[%d] (%s default=%#v) failed: %v", i, f.FieldType, f.Default, err)
			}
		}

		badCases := []struct {
			name  string
			field ColabOneWorkflowField
		}{
			{"intStringNonInt", ColabOneWorkflowField{Key: "1.steps", Label: "Steps", NodeID: "1", InputName: "steps", FieldType: "integer", Default: "abc"}},
			{"intStringDigits", ColabOneWorkflowField{Key: "1.steps", Label: "Steps", NodeID: "1", InputName: "steps", FieldType: "integer", Default: "20"}},
			{"intFloatFractional", ColabOneWorkflowField{Key: "1.steps", Label: "Steps", NodeID: "1", InputName: "steps", FieldType: "integer", Default: 2.5}},
			{"intBool", ColabOneWorkflowField{Key: "1.steps", Label: "Steps", NodeID: "1", InputName: "steps", FieldType: "integer", Default: true}},
			{"numberStringNonNum", ColabOneWorkflowField{Key: "1.cfg", Label: "CFG", NodeID: "1", InputName: "cfg", FieldType: "number", Default: "abc"}},
			{"numberStringDigits", ColabOneWorkflowField{Key: "1.cfg", Label: "CFG", NodeID: "1", InputName: "cfg", FieldType: "number", Default: "7.5"}},
			{"numberBool", ColabOneWorkflowField{Key: "1.cfg", Label: "CFG", NodeID: "1", InputName: "cfg", FieldType: "number", Default: false}},
			{"boolString", ColabOneWorkflowField{Key: "1.flag", Label: "Flag", NodeID: "1", InputName: "flag", FieldType: "boolean", Default: "true"}},
			{"boolInt", ColabOneWorkflowField{Key: "1.flag", Label: "Flag", NodeID: "1", InputName: "flag", FieldType: "boolean", Default: 1}},
			{"boolFloat", ColabOneWorkflowField{Key: "1.flag", Label: "Flag", NodeID: "1", InputName: "flag", FieldType: "boolean", Default: 0.0}},
		}
		for _, tc := range badCases {
			f := tc.field
			if err := f.Validate(); err == nil {
				t.Fatalf("%s: expected default type incompatibility error", tc.name)
			}
		}
	})

	// 4. WorkflowTemplate: every visible_when key must exist in Fields.
	t.Run("workflow_template_visible_when_keys_must_exist_in_fields", func(t *testing.T) {
		wf := sampleAPIWorkflow()
		validTpl := ColabOneWorkflowTemplate{
			ID:       "wf-vw-ok",
			Name:     "Valid VW",
			Source:   "custom",
			Workflow: wf,
			Fields: []ColabOneWorkflowField{
				{Key: "1.mode", Label: "Mode", NodeID: "1", InputName: "mode", FieldType: "select", Default: "a", Options: []string{"a", "b"}},
				{Key: "1.extra", Label: "Extra", NodeID: "1", InputName: "extra", FieldType: "text", VisibleWhen: map[string]any{"1.mode": "b"}},
			},
		}
		if err := validTpl.Validate(); err != nil {
			t.Fatalf("template with valid visible_when reference should pass, got %v", err)
		}

		badTpl := ColabOneWorkflowTemplate{
			ID:       "wf-vw-missing",
			Name:     "Missing VW",
			Source:   "custom",
			Workflow: wf,
			Fields: []ColabOneWorkflowField{
				{Key: "1.extra", Label: "Extra", NodeID: "1", InputName: "extra", FieldType: "text", VisibleWhen: map[string]any{"1.nonexistent": "b"}},
			},
		}
		if err := badTpl.Validate(); err == nil || !strings.Contains(err.Error(), "1.nonexistent") {
			t.Fatalf("template referencing missing visible_when key should fail, got %v", err)
		}
	})

	// 5. ApplyWorkflowOverrides: reject bool targeting non-bool existing inputs.
	t.Run("apply_workflow_overrides_rejects_bool_on_non_bool", func(t *testing.T) {
		wf := map[string]any{
			"1": map[string]any{
				"class_type": "TestNode",
				"inputs": map[string]any{
					"flag":  false,
					"steps": 20,
					"cfg":   7.5,
					"text":  "hello",
					"seed":  json.Number("42"),
				},
			},
		}
		applied, err := ApplyWorkflowOverrides(wf, map[string]any{"1.flag": true}, nil)
		if err != nil {
			t.Fatalf("bool override on bool input should pass, got %v", err)
		}
		if applied["1"].(map[string]any)["inputs"].(map[string]any)["flag"] != true {
			t.Fatalf("bool override did not apply")
		}
		for _, targetKey := range []string{"1.steps", "1.cfg", "1.text", "1.seed"} {
			if _, err := ApplyWorkflowOverrides(wf, map[string]any{targetKey: true}, nil); err == nil {
				t.Fatalf("bool override on non-bool input %s should fail", targetKey)
			}
		}
	})
}
