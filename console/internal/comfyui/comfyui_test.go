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
		t.Fatalf("valid template failed: %v", err)
	}
	if tmpl.Name != "Demo Studio Flow" {
		t.Fatalf("trimmed template name = %q", tmpl.Name)
	}
	if tmpl.Source != "custom" {
		t.Fatalf("default source = %q, want custom", tmpl.Source)
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

	// Bad pip package rejected.
	badPip := ColabOneWorkflowTemplate{
		ID:          "bad_pip",
		Name:        "Bad pip",
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
