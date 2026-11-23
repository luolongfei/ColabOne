package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestExtractQuantization(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"empty", "", "GGUF"},
		{"slashes only", "///", "GGUF"},
		{"plain model", "model.gguf", "GGUF"},
		{"unsloth ud split", "unsloth-UD-Q4_K_XL-V2/Qwen3.8-Flash-Next-UD-Q4_K_XL-00001-of-00002.gguf", "UD-Q4_K_XL"},
		{"ud single", "Qwen3-30B-A3B-UD-Q4_K_XL.gguf", "UD-Q4_K_XL"},
		{"ud iq lowercase", "model-ud-iq1_s.gguf", "UD-IQ1_S"},
		{"ud tq", "model-UD-TQ1_0.gguf", "UD-TQ1_0"},
		{"ud multiple takes last", "foo-UD-Q4_K_XL-UD-Q8_0.gguf", "UD-Q8_0"},
		{"ud greedy underscore parity", "foo-UD-Q4_K_XL_UD-Q8_0.gguf", "UD-Q4_K_XL_UD"},
		{"imatrix q4_k_m", "Llama-3-8B-imatrix-Q4_K_M.gguf", "Q4_K_M"},
		{"dot q8_0", "gemma-4-31B-it-ARA-heretic.Q8_0.gguf", "Q8_0"},
		{"iq2_xxs", "Model-IQ2_XXS.gguf", "IQ2_XXS"},
		{"iq4_xs", "Model-IQ4_XS.gguf", "IQ4_XS"},
		{"tq1_0", "Model-TQ1_0.gguf", "TQ1_0"},
		{"q6_k", "Model-Q6_K.gguf", "Q6_K"},
		{"adjacent hyphen takes last", "Model-Q4_K_M-Q8_0.gguf", "Q8_0"},
		{"adjacent underscore takes last", "Model_Q4_K_M_Q8_0.gguf", "Q8_0"},
		{"backtrack branch1 to branch2", "Model-Q4_Kabc.gguf", "Q4_KABC"},
		{"five letters falls back to Q4", "Model-Q4_ABCDE.gguf", "Q4"},
		{"alnum after q4_0 falls back to Q4", "Model-Q4_0abc.gguf", "Q4"},
		{"five letters iq4 rejected", "Model-IQ4_ABCDE.gguf", "GGUF"},
		{"alnum after iq4_0 rejected", "Model-IQ4_0abc.gguf", "GGUF"},
		{"trailing underscore lookahead", "Model-Q4_K_1_2.gguf", "Q4_K_1"},
		{"bf16", "RVN-BF16.gguf", "BF16"},
		{"f16", "model-F16.gguf", "F16"},
		{"f32", "model-F32.gguf", "F32"},
		{"mxfp4", "model-MXFP4.gguf", "MXFP4"},
		{"mxfp4_moe", "model-MXFP4_MOE.gguf", "MXFP4_MOE"},
		{"mxfp4 backtrack", "model-MXFP4_MOEX.gguf", "MXFP4"},
		{"fp8", "model-FP8.gguf", "FP8"},
		{"fp8_e4m3", "model-FP8_E4M3.gguf", "FP8_E4M3"},
		{"fp8_e4m3fn", "model-FP8_E4M3FN.gguf", "FP8_E4M3FN"},
		{"fp8 backtrack", "model-FP8_E4M3X.gguf", "FP8"},
		{"fp8_e5m2", "model-FP8_E5M2.gguf", "FP8_E5M2"},
		{"bf16 f16 adjacent", "model-BF16-F16.gguf", "F16"},
		{"short q4 filename", "Q4/GLM-5.3-Flash-Uncensored-GSQ-RCO-Q4.gguf", "Q4"},
		{"short q4 parent dir", "Q4/model.gguf", "Q4"},
		{"short q4 q8 hyphen", "Q4-Q8.gguf", "Q8"},
		{"short q4 q8 underscore", "Q4_Q8.gguf", "Q8"},
		{"q1 ignored", "model-Q1.gguf", "GGUF"},
		{"q9 ignored", "model-Q9.gguf", "GGUF"},
		{"preceded by alnum ignored", "model-AQ4.gguf", "GGUF"},
		{"followed by alnum ignored", "model-Q40.gguf", "GGUF"},
		{"utf8 boundaries", "模型-Q4_K_M模型.gguf", "Q4_K_M"},
		{"base checked before parent dir", "UD-Q4_K_XL/model-Q4.gguf", "Q4"},
		{"nested parent dir uppercase ext", "subdir/UD-Q4_K_XL/model.GGUF", "UD-Q4_K_XL"},
		{"priority pattern0 over others", "model-Q4-BF16-Q8_0-UD-Q2_K_XL.gguf", "UD-Q2_K_XL"},
		{"priority pattern1 over 2 and 3", "model-Q4-BF16-Q8_0.gguf", "Q8_0"},
		{"priority pattern2 over 3", "model-Q4-BF16.gguf", "BF16"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractQuantization(tc.path); got != tc.want {
				t.Fatalf("ExtractQuantization(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestIsAuxiliaryGGUF(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"mmproj-f16.gguf", true},
		{"mmproj-model-f16.gguf", true},
		{"dir/foo-mmproj.gguf", true},
		{"foo_projector_f16.gguf", true},
		{"vision-proj.gguf", true},
		{"vision_proj-f16.gguf", true},
		{"visionproj.gguf", true},
		{"imatrix.gguf", true},
		{"imatrix_micro3.gguf", true},
		{"unsloth-UD-Q4_K_XL-V2/imatrix_micro3.gguf", true},
		{"imatrix2.gguf", true},
		{"imatrix-v2.gguf", true},
		{"model-imatrix.gguf", true},
		{"model_imatrix_micro3.gguf", true},
		{"model.imatrix-v2.gguf", true},
		{"vocab.gguf", true},
		{"vocab-llama.gguf", true},
		{"tokenizer_v1.gguf", true},
		{"adapter-lora.gguf", true},
		{"adapter.gguf", true},
		{"foo-lora.gguf", true},
		{"foo_lora.gguf", true},
		{"foo.lora.gguf", true},
		{"MMPROJ-F16.GGUF", true},
		{"ADAPTER-LORA.GGUF", true},

		// Runnable model files must return false.
		{"Llama-3-8B-imatrix-Q4_K_M.gguf", false},
		{"Llama-3-8B-Q4_K_M-imatrix.gguf", false},
		{"Qwen3-32B-Q8_0.gguf", false},
		{"model-Q4_K_M-00001-of-00002.gguf", false},
		{"myadapter.gguf", false},
		{"flora.gguf", false},
		{"alora.gguf", false},
		{"projectorX.gguf", false},
	}
	for _, tc := range tests {
		if got := IsAuxiliaryGGUF(tc.path); got != tc.want {
			t.Errorf("IsAuxiliaryGGUF(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestClassifyModality(t *testing.T) {
	tests := []struct {
		name     string
		pipeline string
		tags     []string
		repo     string
		want     string
	}{
		{"video pipeline", "text-to-video", nil, "org/model", "video"},
		{"video pipeline trimmed case", " Image-To-Video ", nil, "org/model", "video"},
		{"video any-to-video", "any-to-video", nil, "", "video"},
		{"image pipeline", "text-to-image", nil, "org/wan2.1", "image"},
		{"image inpainting", "inpainting", nil, "", "image"},
		{"text pipeline overrides repo", "text-generation", []string{"text-to-image"}, "city96/FLUX.1-dev-gguf", "text"},
		{"text conversational", "conversational", nil, "city96/Wan2.1-T2V-14B-gguf", "text"},
		{"text image-text-to-text", "image-text-to-text", nil, "", "text"},
		{"tags video beats image regardless of order", "", []string{"text-to-image", "video-to-video"}, "org/model", "video"},
		{"tags image", "", []string{"gguf", " Image-To-Image "}, "org/model", "image"},
		{"repo wan2.1", "", nil, "city96/Wan2.1-T2V-14B-gguf", "video"},
		{"repo wan2.2", "", nil, "org/wan2.2-model", "video"},
		{"repo ltx video", "", nil, "org/ltx-video-2b", "video"},
		{"repo ltx_2", "", nil, "org/ltx_2", "video"},
		{"repo hunyuan video", "", nil, "org/HunyuanVideo-GGUF", "video"},
		{"repo cogvideox", "", nil, "org/CogVideoX-5B", "video"},
		{"repo mochi", "", nil, "org/mochi-1", "video"},
		{"repo skyreels", "", nil, "org/skyreels-v1", "video"},
		{"repo framepack", "", nil, "org/framepack", "video"},
		{"repo animatediff", "", nil, "org/animatediff", "video"},
		{"repo i2v", "", nil, "org/model-i2v-gguf", "video"},
		{"repo v2v", "", nil, "org/model.v2v.gguf", "video"},
		{"repo flux", "", nil, "city96/FLUX.1-dev-gguf", "image"},
		{"repo sdxl", "", nil, "org/sdxl-turbo", "image"},
		{"repo sd3.5", "", nil, "org/sd3.5-large", "image"},
		{"repo stable diffusion", "", nil, "org/stable-diffusion-xl", "image"},
		{"repo z-image", "", nil, "org/z-image-turbo", "image"},
		{"repo qwen-image", "", nil, "org/Qwen-Image-GGUF", "image"},
		{"repo hidream", "", nil, "org/hidream-i1", "image"},
		{"repo lumina-image", "", nil, "org/lumina-image", "image"},
		{"repo kolors", "", nil, "org/kolors", "image"},
		{"repo pixart", "", nil, "org/pixart-sigma", "image"},
		{"repo auraflow", "", nil, "org/auraflow", "image"},
		{"repo chroma", "", nil, "org/chroma", "image"},
		{"repo kontext", "", nil, "org/kontext", "image"},
		{"repo omnigen", "", nil, "org/omnigen", "image"},
		{"feature extraction defaults text", "feature-extraction", nil, "org/bge-m3-gguf", "text"},
		{"default text", "", nil, "unsloth/Qwen3-32B-GGUF", "text"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyModality(tc.pipeline, tc.tags, tc.repo); got != tc.want {
				t.Fatalf("ClassifyModality(%q, %v, %q) = %q, want %q", tc.pipeline, tc.tags, tc.repo, got, tc.want)
			}
		})
	}
}

func TestInferFamily(t *testing.T) {
	tests := []struct {
		repo string
		want string
	}{
		{"huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF", "Qwen"},
		{"org/QwQ-32B-GGUF", "Qwen"},
		{"huihui-ai/Huihui-DeepSeek-V4.1-Flash-1017-abliterated-GGUF", "DeepSeek"},
		{"GCSA-AiLab/GLM-5.3-Flash-Uncensored-RCO-GSQ-GGUF", "GLM"},
		{"THUDM/chatglm3-6b-gguf", "GLM"},
		{"mradermacher/gemma-4-31B-it-ARA-heretic-GGUF", "Gemma"},
		{"bartowski/Llama-3.3-70B-Instruct-GGUF", "Llama"},
		{"org/Mistral-Small-24B", "Mistral"},
		{"org/Mixtral-8x7B", "Mistral"},
		{"org/Pixtral-12B", "Mistral"},
		{"org/Devstral-Small", "Mistral"},
		{"org/Magistral-Small", "Mistral"},
		{"city96/FLUX.1-dev-gguf", "FLUX"},
		{"org/flux", "FLUX"},
		{"city96/Wan2.1-T2V-14B-gguf", "Wan"},
		{"org/wan-2-gguf", "Wan"},
		{"org/LTX-Video-GGUF", "LTX"},
		{"org/ltx_2-gguf", "LTX"},
		{"org/Hunyuan-Large-GGUF", "Hunyuan"},
		{"qwen-org/unknown-model-GGUF", ""},
		{"org/other-model", ""},
		{"", ""},
	}
	for _, tc := range tests {
		if got := InferFamily(tc.repo); got != tc.want {
			t.Errorf("InferFamily(%q) = %q, want %q", tc.repo, got, tc.want)
		}
	}
}

func TestInferParams(t *testing.T) {
	tests := []struct {
		repo string
		want string
	}{
		{"unsloth/Qwen3-32B-GGUF", "32B"},
		{"unsloth/Qwen3-30B-A3B-Instruct-2507-GGUF", "30B-A3B"},
		{"org/Model-125B-A6.5B-GGUF", "125B-A6.5B"},
		{"org/Model-0.5b-gguf", "0.5B"},
		{"org/Model-125m-GGUF", "125M"},
		{"org/Model-12B-A3X", "12B"},
		{"org/12B-32B", "12B"},
		{"org/Model-8B2", ""},
		{"org/Model8B", ""},
		{"32B-owner/unknown-model", ""},
		{"", ""},
	}
	for _, tc := range tests {
		if got := InferParams(tc.repo); got != tc.want {
			t.Errorf("InferParams(%q) = %q, want %q", tc.repo, got, tc.want)
		}
	}
}

func TestSearchModelsPassesFiltersAndTrustedPagination(t *testing.T) {
	var linkHeader atomic.Value
	linkHeader.Store(`<https://huggingface.co/api/models?cursor=next&filter=gguf>; rel="next"`)

	var sawResolve atomic.Bool
	var lastQuery string
	var lastAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/resolve/") {
			sawResolve.Store(true)
		}
		lastQuery = r.URL.RawQuery
		lastAuth = r.Header.Get("Authorization")
		if hdr, _ := linkHeader.Load().(string); hdr != "" {
			w.Header().Set("Link", hdr)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"org/model","downloads":123,"likes":4,"lastModified":"2026-01-02T03:04:05Z","gated":"auto"}]`))
	}))
	defer srv.Close()

	client := &ColabOneHubClient{BaseURL: srv.URL, HTTPClient: srv.Client()}

	res, err := client.SearchModels(context.Background(), ColabOneHubSearchOptions{
		Query:  "Qwen",
		Sort:   "updated",
		Cursor: "cur1",
		Token:  "hf_test_token",
	})
	if err != nil {
		t.Fatalf("SearchModels failed: %v", err)
	}
	if len(res.Models) != 1 || res.Models[0].Repo != "org/model" {
		t.Fatalf("unexpected models: %+v", res.Models)
	}
	if !res.Models[0].Gated {
		t.Fatalf("expected gated='auto' to decode as true")
	}
	if res.Models[0].Downloads != 123 || res.Models[0].Likes != 4 || res.Models[0].Updated != "2026-01-02T03:04:05Z" {
		t.Fatalf("unexpected metadata fields: %+v", res.Models[0])
	}
	if res.NextCursor != "next" {
		t.Fatalf("next_cursor = %q, want %q", res.NextCursor, "next")
	}
	if lastAuth != "Bearer hf_test_token" {
		t.Fatalf("Authorization = %q, want Bearer token", lastAuth)
	}
	if !strings.Contains(lastQuery, "filter=gguf") ||
		!strings.Contains(lastQuery, "sort=lastModified") ||
		!strings.Contains(lastQuery, "direction=-1") ||
		!strings.Contains(lastQuery, "limit=20") ||
		!strings.Contains(lastQuery, "cursor=cur1") {
		t.Fatalf("unexpected query params: %q", lastQuery)
	}

	// Untrusted pagination URLs must be dropped.
	untrustedLinks := []string{
		`<https://attacker.invalid/api/models?cursor=no>; rel="next"`,
		`<http://huggingface.co/api/models?cursor=no>; rel="next"`,
		`<https://huggingface.co:443/api/models?cursor=no>; rel="next"`,
		`<https://evil@huggingface.co/api/models?cursor=no>; rel="next"`,
		`<https://huggingface.co/api/models/evil?cursor=no>; rel="next"`,
		`<https://huggingface.co.evil.com/api/models?cursor=no>; rel="next"`,
		`<https://huggingface.co/api/models?cursor=prev>; rel="prev"`,
	}
	for _, hdr := range untrustedLinks {
		linkHeader.Store(hdr)
		res, err := client.SearchModels(context.Background(), ColabOneHubSearchOptions{})
		if err != nil {
			t.Fatalf("SearchModels(%q) failed: %v", hdr, err)
		}
		if res.NextCursor != "" {
			t.Fatalf("expected empty next_cursor for %q, got %q", hdr, res.NextCursor)
		}
	}

	// Multiple links, commas in URL and quoted params, unquoted/single-quoted rel.
	validLinks := []struct {
		hdr  string
		want string
	}{
		{
			`<https://huggingface.co/api/models?cursor=prev>; rel="prev", <https://huggingface.co/api/models?search=a,b;c&cursor=next2>; title="next, page"; rel="next"`,
			"next2",
		},
		{
			`<https://huggingface.co/api/models?cursor=next3>; rel=next`,
			"next3",
		},
		{
			`<https://huggingface.co/api/models?cursor=next4>; REL='next'`,
			"next4",
		},
	}
	for _, tc := range validLinks {
		linkHeader.Store(tc.hdr)
		res, err := client.SearchModels(context.Background(), ColabOneHubSearchOptions{})
		if err != nil {
			t.Fatalf("SearchModels(%q) failed: %v", tc.hdr, err)
		}
		if res.NextCursor != tc.want {
			t.Fatalf("next_cursor for %q = %q, want %q", tc.hdr, res.NextCursor, tc.want)
		}
	}

	if sawResolve.Load() {
		t.Fatalf("metadata client must never request /resolve/")
	}
}

func TestSearchModelsMultipleLinkHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Link", `<https://huggingface.co/api/models?cursor=prev>; rel="prev"`)
		w.Header().Add("Link", `<https://huggingface.co/api/models?cursor=page2%2Bfoo>; rel="next"`)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	client := &ColabOneHubClient{BaseURL: srv.URL, HTTPClient: srv.Client()}
	res, err := client.SearchModels(context.Background(), ColabOneHubSearchOptions{})
	if err != nil {
		t.Fatalf("SearchModels failed: %v", err)
	}
	if res.NextCursor != "page2+foo" {
		t.Fatalf("next_cursor = %q, want %q", res.NextCursor, "page2+foo")
	}
	// Empty slice must serialize as JSON [] rather than null.
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	if string(raw) != `{"models":[],"next_cursor":"page2+foo"}` {
		t.Fatalf("unexpected JSON: %s", raw)
	}
}

func TestSearchModelsModalityAndFamilyFiltering(t *testing.T) {
	var lastValues map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastValues = r.URL.Query()
		_, _ = w.Write([]byte(`[
			{"id":"unsloth/Qwen3-32B-GGUF","downloads":5000,"pipeline_tag":"text-generation","gated":false},
			{"id":"city96/FLUX.1-dev-gguf","downloads":9000,"pipeline_tag":"text-to-image","gated":"manual"},
			{"id":"city96/Wan2.1-T2V-14B-gguf","downloads":7000,"pipeline_tag":"text-to-video","gated":true},
			{"id":"org/bge-m3-gguf","downloads":3000,"pipeline_tag":"feature-extraction"},
			{"id":"invalid-no-slash","downloads":999},
			{"id":"../evil","downloads":999}
		]`))
	}))
	defer srv.Close()

	client := &ColabOneHubClient{BaseURL: srv.URL, HTTPClient: srv.Client()}

	textRes, err := client.SearchModels(context.Background(), ColabOneHubSearchOptions{
		Query:    "32B",
		Modality: "text",
		Family:   "Qwen",
	})
	if err != nil {
		t.Fatalf("text search failed: %v", err)
	}
	if got := lastValues["search"][0]; got != "Qwen 32B" {
		t.Fatalf("search = %q, want %q", got, "Qwen 32B")
	}
	if got := lastValues["limit"][0]; got != "30" {
		t.Fatalf("limit = %q, want 30 for text modality", got)
	}
	if _, hasPipe := lastValues["pipeline_tag"]; hasPipe {
		t.Fatalf("text modality must not send pipeline_tag")
	}
	if len(textRes.Models) != 1 || textRes.Models[0].Repo != "unsloth/Qwen3-32B-GGUF" {
		t.Fatalf("unexpected text models: %+v", textRes.Models)
	}
	if textRes.Models[0].Family != "Qwen" || textRes.Models[0].Params != "32B" || textRes.Models[0].ComfyOnly {
		t.Fatalf("unexpected text model metadata: %+v", textRes.Models[0])
	}

	// Family already present in query (case-insensitive) should not be duplicated.
	_, err = client.SearchModels(context.Background(), ColabOneHubSearchOptions{
		Query:  "qwen3 32B",
		Family: "Qwen",
	})
	if err != nil {
		t.Fatalf("dedup family search failed: %v", err)
	}
	if got := lastValues["search"][0]; got != "qwen3 32B" {
		t.Fatalf("search = %q, want %q", got, "qwen3 32B")
	}

	imgRes, err := client.SearchModels(context.Background(), ColabOneHubSearchOptions{
		Modality: "image",
		Family:   "FLUX",
	})
	if err != nil {
		t.Fatalf("image search failed: %v", err)
	}
	if got := lastValues["pipeline_tag"][0]; got != "text-to-image" {
		t.Fatalf("pipeline_tag = %q, want text-to-image", got)
	}
	if got := lastValues["search"][0]; got != "FLUX" {
		t.Fatalf("search = %q, want FLUX", got)
	}
	if got := lastValues["limit"][0]; got != "20" {
		t.Fatalf("limit = %q, want 20", got)
	}
	for _, m := range imgRes.Models {
		if m.Modality != "image" || !m.ComfyOnly {
			t.Fatalf("expected image+comfy_only, got %+v", m)
		}
	}

	vidRes, err := client.SearchModels(context.Background(), ColabOneHubSearchOptions{
		Modality: "video",
	})
	if err != nil {
		t.Fatalf("video search failed: %v", err)
	}
	if got := lastValues["pipeline_tag"][0]; got != "text-to-video" {
		t.Fatalf("pipeline_tag = %q, want text-to-video", got)
	}
	for _, m := range vidRes.Models {
		if m.Modality != "video" || !m.ComfyOnly {
			t.Fatalf("expected video+comfy_only, got %+v", m)
		}
	}

	allRes, err := client.SearchModels(context.Background(), ColabOneHubSearchOptions{
		Modality: "all",
	})
	if err != nil {
		t.Fatalf("all search failed: %v", err)
	}
	byRepo := make(map[string]ColabOneHubModel, len(allRes.Models))
	for _, m := range allRes.Models {
		byRepo[m.Repo] = m
	}
	if len(byRepo) != 4 {
		t.Fatalf("expected 4 valid repos, got %d: %+v", len(byRepo), byRepo)
	}
	if byRepo["unsloth/Qwen3-32B-GGUF"].Modality != "text" || byRepo["unsloth/Qwen3-32B-GGUF"].ComfyOnly {
		t.Fatalf("unexpected Qwen entry: %+v", byRepo["unsloth/Qwen3-32B-GGUF"])
	}
	if byRepo["city96/FLUX.1-dev-gguf"].Modality != "image" || !byRepo["city96/FLUX.1-dev-gguf"].ComfyOnly || !byRepo["city96/FLUX.1-dev-gguf"].Gated {
		t.Fatalf("unexpected FLUX entry: %+v", byRepo["city96/FLUX.1-dev-gguf"])
	}
	if byRepo["city96/Wan2.1-T2V-14B-gguf"].Modality != "video" || !byRepo["city96/Wan2.1-T2V-14B-gguf"].ComfyOnly || !byRepo["city96/Wan2.1-T2V-14B-gguf"].Gated {
		t.Fatalf("unexpected Wan entry: %+v", byRepo["city96/Wan2.1-T2V-14B-gguf"])
	}
}

func TestSearchModelsValidationErrors(t *testing.T) {
	client := NewColabOneHubClient()
	cases := []struct {
		name string
		opts ColabOneHubSearchOptions
		want error
	}{
		{"invalid sort", ColabOneHubSearchOptions{Sort: "arbitrary"}, ErrInvalidSearchQuery},
		{"whitespace sort", ColabOneHubSearchOptions{Sort: " "}, ErrInvalidSearchQuery},
		{"query too long", ColabOneHubSearchOptions{Query: strings.Repeat("模", 201)}, ErrInvalidSearchQuery},
		{"cursor too long", ColabOneHubSearchOptions{Cursor: strings.Repeat("a", 4097)}, ErrInvalidSearchCursor},
		{"invalid modality", ColabOneHubSearchOptions{Modality: "audio"}, ErrInvalidModalityFilter},
		{"family too long", ColabOneHubSearchOptions{Family: strings.Repeat("q", 65)}, ErrInvalidFamilyFilter},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.SearchModels(context.Background(), tc.opts)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got err=%v, want %v", err, tc.want)
			}
		})
	}
}

func TestHubHTTPErrorsWithoutLeakingToken(t *testing.T) {
	secret := "hf_super_secret_token_value"
	statuses := []struct {
		code int
		want error
	}{
		{http.StatusUnauthorized, ErrHubAccessDenied},
		{http.StatusForbidden, ErrHubAccessDenied},
		{http.StatusTooManyRequests, ErrHubRateLimited},
		{http.StatusInternalServerError, ErrHubNetwork},
		{http.StatusBadGateway, ErrHubNetwork},
	}
	for _, tc := range statuses {
		t.Run(fmt.Sprintf("status_%d", tc.code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(`{"error":"details"}`))
			}))
			defer srv.Close()

			client := &ColabOneHubClient{BaseURL: srv.URL, HTTPClient: srv.Client()}
			_, err := client.SearchModels(context.Background(), ColabOneHubSearchOptions{Token: secret})
			if !errors.Is(err, tc.want) {
				t.Fatalf("SearchModels status %d: err=%v, want %v", tc.code, err, tc.want)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error leaked secret token: %q", err.Error())
			}

			_, err = client.ModelFiles(context.Background(), "org/model", secret)
			if !errors.Is(err, tc.want) {
				t.Fatalf("ModelFiles status %d: err=%v, want %v", tc.code, err, tc.want)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error leaked secret token: %q", err.Error())
			}
		})
	}

	// Network failure and context cancellation map to ErrHubNetwork without leaking token.
	closedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := closedSrv.URL
	closedSrv.Close()

	client := &ColabOneHubClient{BaseURL: closedURL, HTTPClient: &http.Client{Timeout: time.Second}}
	_, err := client.SearchModels(context.Background(), ColabOneHubSearchOptions{Token: secret})
	if !errors.Is(err, ErrHubNetwork) || strings.Contains(err.Error(), secret) {
		t.Fatalf("closed server err=%v, want ErrHubNetwork without token", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.ModelFiles(ctx, "org/model", secret)
	if !errors.Is(err, ErrHubNetwork) || strings.Contains(err.Error(), secret) {
		t.Fatalf("canceled ctx err=%v, want ErrHubNetwork without token", err)
	}
}

func TestHubInvalidJSONResponses(t *testing.T) {
	badPayloads := []struct {
		name string
		body string
	}{
		{"empty", ""},
		{"malformed", "{bad"},
		{"null", "null"},
		{"trailing tokens", `[] {}`},
	}
	for _, tc := range badPayloads {
		t.Run("search_"+tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			client := &ColabOneHubClient{BaseURL: srv.URL, HTTPClient: srv.Client()}
			if _, err := client.SearchModels(context.Background(), ColabOneHubSearchOptions{}); !errors.Is(err, ErrHubInvalidResponse) {
				t.Fatalf("got err=%v, want ErrHubInvalidResponse", err)
			}
		})
	}

	// Object response for search_models and array response for model_files must fail.
	srvObj := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"not":"a list"}`))
	}))
	defer srvObj.Close()
	clientObj := &ColabOneHubClient{BaseURL: srvObj.URL, HTTPClient: srvObj.Client()}
	if _, err := clientObj.SearchModels(context.Background(), ColabOneHubSearchOptions{}); !errors.Is(err, ErrHubInvalidResponse) {
		t.Fatalf("object in SearchModels: err=%v, want ErrHubInvalidResponse", err)
	}

	srvArr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srvArr.Close()
	clientArr := &ColabOneHubClient{BaseURL: srvArr.URL, HTTPClient: srvArr.Client()}
	if _, err := clientArr.ModelFiles(context.Background(), "org/model", ""); !errors.Is(err, ErrHubInvalidResponse) {
		t.Fatalf("array in ModelFiles: err=%v, want ErrHubInvalidResponse", err)
	}
}

func TestModelFilesGroupsCompleteShardsAndDisablesMissingHashes(t *testing.T) {
	shaA := strings.Repeat("a", 40)
	shaB := strings.Repeat("b", 64)

	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		payload := map[string]any{
			"sha": shaA,
			"siblings": []map[string]any{
				{"rfilename": "model-Q4_K_M-00001-of-00002.gguf", "lfs": map[string]any{"size": 10, "sha256": shaB}},
				{"rfilename": "model-Q4_K_M-00002-of-00002.gguf", "lfs": map[string]any{"size": 10, "sha256": shaB}},
				{"rfilename": "mmproj-f16.gguf"},
				{"rfilename": "model-Q8_0.gguf"},
			},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	client := &ColabOneHubClient{BaseURL: srv.URL, HTTPClient: srv.Client()}
	res, err := client.ModelFiles(context.Background(), "org/model", "")
	if err != nil {
		t.Fatalf("ModelFiles failed: %v", err)
	}
	if gotPath != "/org/model/revision/main" || gotQuery != "blobs=true" {
		t.Fatalf("unexpected request path=%q query=%q", gotPath, gotQuery)
	}
	if res.Revision != shaA {
		t.Fatalf("revision = %q, want %q", res.Revision, shaA)
	}
	if len(res.Files) != 2 {
		t.Fatalf("expected 2 files, got %d: %+v", len(res.Files), res.Files)
	}
	// Sorted by (size, filename): missing-hash file (size 0) comes first, grouped file (size 20) second.
	if res.Files[0].Filename != "model-Q8_0.gguf" || res.Files[0].Supported || res.Files[0].Shards != 1 || res.Files[0].Reason != missingMetaReason {
		t.Fatalf("unexpected unsupported file: %+v", res.Files[0])
	}
	grouped := res.Files[1]
	if grouped.Filename != "model-Q4_K_M-00001-of-00002.gguf" ||
		grouped.Size != 20 ||
		grouped.Shards != 2 ||
		grouped.Quantization != "Q4_K_M" ||
		!grouped.Supported ||
		grouped.Reason != "" {
		t.Fatalf("unexpected grouped file: %+v", grouped)
	}
}

func TestModelFilesFiltersImatrixAndExtractsModernQuantizations(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]any{
			"sha":          strings.Repeat("b", 40),
			"pipeline_tag": "text-generation",
			"siblings": []map[string]any{
				{"rfilename": "unsloth-UD-Q4_K_XL-V2/imatrix_micro3.gguf", "lfs": map[string]any{"size": int64(1_160_000_000), "sha256": strings.Repeat("1", 64)}},
				{"rfilename": "unsloth-UD-Q4_K_XL-V2/Qwen3.8-Flash-Next-UD-Q4_K_XL-00001-of-00002.gguf", "lfs": map[string]any{"size": int64(40_000_000_000), "sha256": strings.Repeat("2", 64)}},
				{"rfilename": "unsloth-UD-Q4_K_XL-V2/Qwen3.8-Flash-Next-UD-Q4_K_XL-00002-of-00002.gguf", "lfs": map[string]any{"size": int64(30_000_000_000), "sha256": strings.Repeat("3", 64)}},
				{"rfilename": "Llama-3-8B-imatrix-Q4_K_M.gguf", "lfs": map[string]any{"size": int64(4_900_000_000), "sha256": strings.Repeat("4", 64)}},
				{"rfilename": "mmproj-model-f16.gguf", "lfs": map[string]any{"size": int64(800_000_000), "sha256": strings.Repeat("5", 64)}},
				{"rfilename": "adapter-lora.gguf", "lfs": map[string]any{"size": int64(200_000_000), "sha256": strings.Repeat("6", 64)}},
				{"rfilename": "Q4/model.gguf", "lfs": map[string]any{"size": int64(5_000_000_000), "sha256": strings.Repeat("7", 64)}},
			},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	client := &ColabOneHubClient{BaseURL: srv.URL, HTTPClient: srv.Client()}
	res, err := client.ModelFiles(context.Background(), "huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF", "")
	if err != nil {
		t.Fatalf("ModelFiles failed: %v", err)
	}
	if res.Modality != "text" || res.ComfyOnly || res.Family != "Qwen" {
		t.Fatalf("unexpected repo metadata: %+v", res)
	}
	if len(res.Files) != 3 {
		t.Fatalf("expected 3 runnable files, got %d: %+v", len(res.Files), res.Files)
	}
	// Verify size ordering (4.9GB < 5.0GB < 70.0GB).
	if res.Files[0].Filename != "Llama-3-8B-imatrix-Q4_K_M.gguf" || res.Files[0].Quantization != "Q4_K_M" {
		t.Fatalf("file[0] = %+v", res.Files[0])
	}
	if res.Files[1].Filename != "Q4/model.gguf" || res.Files[1].Quantization != "Q4" {
		t.Fatalf("file[1] = %+v", res.Files[1])
	}
	if res.Files[2].Filename != "unsloth-UD-Q4_K_XL-V2/Qwen3.8-Flash-Next-UD-Q4_K_XL-00001-of-00002.gguf" ||
		res.Files[2].Quantization != "UD-Q4_K_XL" ||
		res.Files[2].Size != 70_000_000_000 ||
		res.Files[2].Shards != 2 {
		t.Fatalf("file[2] = %+v", res.Files[2])
	}
}

func TestModelFilesShardAndMetadataEdgeCases(t *testing.T) {
	validSHA := strings.Repeat("A", 64) // Uppercase hex must be accepted.
	var siblings []map[string]any

	// 128-shard valid group (max allowed).
	for i := 1; i <= 128; i++ {
		siblings = append(siblings, map[string]any{
			"rfilename": fmt.Sprintf("maxshards-Q4_K_M-%05d-of-00128.GGUF", i),
			"lfs":       map[string]any{"size": 100, "sha256": validSHA},
		})
	}
	// 129-shard group (exceeds limit -> unsupported).
	siblings = append(siblings, map[string]any{
		"rfilename": "toomany-Q4_K_M-00001-of-00129.gguf",
		"lfs":       map[string]any{"size": 500, "sha256": validSHA},
	})
	// 0-shard count -> unsupported.
	siblings = append(siblings, map[string]any{
		"rfilename": "zeroshard-Q4_K_M-00001-of-00000.gguf",
		"lfs":       map[string]any{"size": 300, "sha256": validSHA},
	})
	// Missing shard 2 -> unsupported, size still counts shard 1.
	siblings = append(siblings, map[string]any{
		"rfilename": "missing-Q4_K_M-00001-of-00002.gguf",
		"lfs":       map[string]any{"size": 700, "sha256": validSHA},
	})
	// Invalid LFS sizes (bool, float, string, zero, negative) and bad sha256.
	siblings = append(siblings,
		map[string]any{"rfilename": "boolsize-Q4.gguf", "lfs": map[string]any{"size": true, "sha256": validSHA}},
		map[string]any{"rfilename": "floatsize-Q4.gguf", "lfs": map[string]any{"size": 12.5, "sha256": validSHA}},
		map[string]any{"rfilename": "strsize-Q4.gguf", "lfs": map[string]any{"size": "100", "sha256": validSHA}},
		map[string]any{"rfilename": "zerosize-Q4.gguf", "lfs": map[string]any{"size": 0, "sha256": validSHA}},
		map[string]any{"rfilename": "negsize-Q4.gguf", "lfs": map[string]any{"size": -5, "sha256": validSHA}},
		map[string]any{"rfilename": "badsha-Q4.gguf", "lfs": map[string]any{"size": 50, "sha256": strings.Repeat("g", 64)}},
		map[string]any{"rfilename": "tie-b-Q4.gguf", "lfs": map[string]any{"size": 50, "sha256": validSHA}},
		map[string]any{"rfilename": "tie-a-Q4.gguf", "lfs": map[string]any{"size": 50, "sha256": validSHA}},
		map[string]any{"rfilename": "README.md"},
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sha":      strings.Repeat("F", 40),
			"tags":     []any{"gguf", 123, "text-to-video"},
			"siblings": siblings,
		})
	}))
	defer srv.Close()

	client := &ColabOneHubClient{BaseURL: srv.URL, HTTPClient: srv.Client()}
	res, err := client.ModelFiles(context.Background(), "city96/Wan2.1-T2V-14B-gguf", "")
	if err != nil {
		t.Fatalf("ModelFiles failed: %v", err)
	}
	if res.Modality != "video" || !res.ComfyOnly || res.Family != "Wan" || res.Params != "14B" {
		t.Fatalf("unexpected video metadata: %+v", res)
	}

	byName := make(map[string]ColabOneHubFile, len(res.Files))
	for _, f := range res.Files {
		byName[f.Filename] = f
	}

	maxF := byName["maxshards-Q4_K_M-00001-of-00128.GGUF"]
	if !maxF.Supported || maxF.Shards != 128 || maxF.Size != 12800 || maxF.Reason != "" {
		t.Fatalf("unexpected 128-shard result: %+v", maxF)
	}
	tooMany := byName["toomany-Q4_K_M-00001-of-00129.gguf"]
	if tooMany.Supported || tooMany.Shards != 129 || tooMany.Size != 500 || tooMany.Reason != missingMetaReason {
		t.Fatalf("unexpected 129-shard result: %+v", tooMany)
	}
	zeroShard := byName["zeroshard-Q4_K_M-00001-of-00000.gguf"]
	if zeroShard.Supported || zeroShard.Shards != 0 || zeroShard.Size != 300 {
		t.Fatalf("unexpected 0-shard result: %+v", zeroShard)
	}
	missing := byName["missing-Q4_K_M-00001-of-00002.gguf"]
	if missing.Supported || missing.Shards != 2 || missing.Size != 700 {
		t.Fatalf("unexpected missing-shard result: %+v", missing)
	}
	for _, name := range []string{"boolsize-Q4.gguf", "floatsize-Q4.gguf", "strsize-Q4.gguf", "zerosize-Q4.gguf", "negsize-Q4.gguf"} {
		f := byName[name]
		if f.Supported || f.Size != 0 {
			t.Fatalf("%s should be unsupported with size 0, got %+v", name, f)
		}
	}
	if f := byName["badsha-Q4.gguf"]; f.Supported || f.Size != 50 {
		t.Fatalf("badsha-Q4.gguf should be unsupported with size 50, got %+v", f)
	}

	// Verify deterministic (size, filename) tie-breaking for size==50 files.
	var size50 []string
	for _, f := range res.Files {
		if f.Size == 50 {
			size50 = append(size50, f.Filename)
		}
	}
	wantOrder := []string{"badsha-Q4.gguf", "tie-a-Q4.gguf", "tie-b-Q4.gguf"}
	if strings.Join(size50, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("tie-break order = %v, want %v", size50, wantOrder)
	}
}

func TestModelFilesValidationAndMissingRevision(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"sha":"not-a-40-char-sha","siblings":[]}`))
	}))
	defer srv.Close()

	client := &ColabOneHubClient{BaseURL: srv.URL, HTTPClient: srv.Client()}

	for _, badRepo := range []string{"", "noslash", "a/b/c", "../evil", "org/..", "./org", "org/bad space"} {
		if _, err := client.ModelFiles(context.Background(), badRepo, ""); !errors.Is(err, ErrInvalidRepo) {
			t.Fatalf("ModelFiles(%q) err=%v, want ErrInvalidRepo", badRepo, err)
		}
	}

	if _, err := client.ModelFiles(context.Background(), "org/model", ""); !errors.Is(err, ErrHubMissingRevision) {
		t.Fatalf("short sha err=%v, want ErrHubMissingRevision", err)
	}
}

func TestModelFilesEmptyJSONSerialization(t *testing.T) {
	sha := strings.Repeat("c", 40)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": sha, "siblings": []any{}})
	}))
	defer srv.Close()

	client := &ColabOneHubClient{BaseURL: srv.URL, HTTPClient: srv.Client()}
	res, err := client.ModelFiles(context.Background(), "org/model", "")
	if err != nil {
		t.Fatalf("ModelFiles failed: %v", err)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	filesVal, ok := decoded["files"].([]any)
	if !ok || len(filesVal) != 0 {
		t.Fatalf("expected empty JSON array for files, got %s", raw)
	}
	for _, key := range []string{"repo", "revision", "pipeline", "modality", "comfy_only", "family", "params", "files"} {
		if _, exists := decoded[key]; !exists {
			t.Fatalf("missing JSON key %q in %s", key, raw)
		}
	}
}

func TestHubClientConcurrentCallsRaceFree(t *testing.T) {
	sha := strings.Repeat("d", 40)
	sha256 := strings.Repeat("e", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/revision/main") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sha": sha,
				"siblings": []map[string]any{
					{"rfilename": "model-Q4_K_M.gguf", "lfs": map[string]any{"size": 1024, "sha256": sha256}},
				},
			})
			return
		}
		w.Header().Set("Link", `<https://huggingface.co/api/models?cursor=c2>; rel="next"`)
		_, _ = w.Write([]byte(`[{"id":"unsloth/Qwen3-32B-GGUF","downloads":10,"pipeline_tag":"text-generation"}]`))
	}))
	defer srv.Close()

	client := &ColabOneHubClient{BaseURL: srv.URL, HTTPClient: srv.Client()}
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			res, err := client.SearchModels(context.Background(), ColabOneHubSearchOptions{Query: "Qwen", Modality: "text"})
			if err != nil {
				errs <- err
				return
			}
			if len(res.Models) != 1 || res.NextCursor != "c2" {
				errs <- fmt.Errorf("unexpected search result: %+v", res)
			}
		}()
		go func() {
			defer wg.Done()
			res, err := client.ModelFiles(context.Background(), "unsloth/Qwen3-32B-GGUF", "")
			if err != nil {
				errs <- err
				return
			}
			if len(res.Files) != 1 || !res.Files[0].Supported {
				errs <- fmt.Errorf("unexpected files result: %+v", res)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestSearchModelsGatedVariantsMalformedLinksAndJSONSchema(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("sort"); got != "likes" {
			t.Errorf("sort = %q, want likes", got)
		}
		w.Header().Add("Link", `garbage, <https://huggingface.co/api/models?cursor=ok>; title="next\"page"; rel="next"`)
		w.Header().Add("Link", `<unclosed-link`)
		_, _ = w.Write([]byte(`[
			{"id":"org/a","downloads":1,"likes":2,"lastModified":"2026-01-01","gated":false},
			{"id":"org/b","downloads":1,"likes":2,"gated":""},
			{"id":"org/c","downloads":1,"likes":2,"gated":"manual"},
			{"id":"org/d","downloads":1,"likes":2,"gated":0},
			{"id":"org/e","downloads":1,"likes":2,"gated":1},
			{"id":"org/f","downloads":1,"likes":2,"gated":null}
		]`))
	}))
	defer srv.Close()

	client := &ColabOneHubClient{BaseURL: srv.URL, HTTPClient: srv.Client()}
	res, err := client.SearchModels(context.Background(), ColabOneHubSearchOptions{Sort: "likes"})
	if err != nil {
		t.Fatalf("SearchModels failed: %v", err)
	}
	if res.NextCursor != "ok" {
		t.Fatalf("next_cursor = %q, want ok", res.NextCursor)
	}
	wantGated := []bool{false, false, true, false, true, false}
	if len(res.Models) != len(wantGated) {
		t.Fatalf("len(models) = %d, want %d", len(res.Models), len(wantGated))
	}
	for i, want := range wantGated {
		if res.Models[i].Gated != want {
			t.Fatalf("model[%d] (%s) gated = %v, want %v", i, res.Models[i].Repo, res.Models[i].Gated, want)
		}
	}

	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	items := decoded["models"].([]any)
	first := items[0].(map[string]any)
	for _, key := range []string{"repo", "downloads", "likes", "updated", "gated", "pipeline", "modality", "comfy_only", "family", "params"} {
		if _, exists := first[key]; !exists {
			t.Fatalf("missing JSON key %q in %s", key, raw)
		}
	}
}

func TestHubDefaultClientRedirectPolicy(t *testing.T) {
	if defaultHTTPClient.CheckRedirect == nil {
		t.Fatal("defaultHTTPClient.CheckRedirect must be configured")
	}
	if NewColabOneHubClient().HTTPClient.CheckRedirect == nil {
		t.Fatal("NewColabOneHubClient().HTTPClient.CheckRedirect must be configured")
	}

	const secret = "hf_redirect_secret_token"
	validSHA := strings.Repeat("a", 40)
	validSHA256 := strings.Repeat("b", 64)

	t.Run("same_origin_redirect_keeps_auth_strips_referer", func(t *testing.T) {
		var mu sync.Mutex
		var redirectAuth, redirectReferer string
		var targetHits atomic.Int32

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/org/old/revision/main":
				http.Redirect(w, r, "/org/new/revision/main?blobs=true", http.StatusMovedPermanently)
				return
			case "/org/new/revision/main":
				targetHits.Add(1)
				mu.Lock()
				redirectAuth = r.Header.Get("Authorization")
				redirectReferer = r.Header.Get("Referer")
				mu.Unlock()
				_ = json.NewEncoder(w).Encode(map[string]any{
					"sha": validSHA,
					"siblings": []map[string]any{
						{"rfilename": "model-Q4_K_M.gguf", "lfs": map[string]any{"size": 1024, "sha256": validSHA256}},
					},
				})
				return
			default:
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()

		// Nil HTTPClient uses defaultHTTPClient.
		client := &ColabOneHubClient{BaseURL: srv.URL}
		res, err := client.ModelFiles(context.Background(), "org/old", secret)
		if err != nil {
			t.Fatalf("ModelFiles same-origin redirect failed: %v", err)
		}
		if len(res.Files) != 1 || res.Revision != validSHA {
			t.Fatalf("unexpected redirected ModelFiles result: %+v", res)
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
		origReq, err := http.NewRequest(http.MethodGet, "https://HUGGINGFACE.CO/api/models/old/repo", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		origReq.Header.Set("Authorization", "Bearer "+secret)
		nextReq, err := http.NewRequest(http.MethodGet, "https://huggingface.co/api/models/new/repo", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		nextReq.Header.Set("Authorization", "Bearer "+secret)
		nextReq.Header.Set("Referer", origReq.URL.String())
		if err := colabOneHubCheckRedirect(nextReq, []*http.Request{origReq}); err != nil {
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
			_, _ = w.Write([]byte(`[{"id":"org/model","downloads":1}]`))
		}))
		defer targetSrv.Close()

		originSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, targetSrv.URL+"/redirected", http.StatusFound)
		}))
		defer originSrv.Close()

		client := NewColabOneHubClient()
		client.BaseURL = originSrv.URL
		res, err := client.SearchModels(context.Background(), ColabOneHubSearchOptions{Token: secret})
		if err != nil {
			t.Fatalf("SearchModels cross-host redirect failed: %v", err)
		}
		if len(res.Models) != 1 || targetHits.Load() != 1 {
			t.Fatalf("unexpected cross-host redirect result: hits=%d models=%+v", targetHits.Load(), res.Models)
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

		// Subdomain and external host redirects must strip Authorization and Referer.
		for _, targetURL := range []string{
			"https://sub.huggingface.co/api/models",
			"https://evil.example/api/models",
		} {
			origReq, _ := http.NewRequest(http.MethodGet, "https://huggingface.co/api/models", nil)
			origReq.Header.Set("Authorization", "Bearer "+secret)
			nextReq, _ := http.NewRequest(http.MethodGet, targetURL, nil)
			nextReq.Header.Set("Authorization", "Bearer "+secret)
			nextReq.Header.Set("Referer", origReq.URL.String())

			if err := colabOneHubCheckRedirect(nextReq, []*http.Request{origReq}); err != nil {
				t.Fatalf("redirect to %s unexpected error: %v", targetURL, err)
			}
			if nextReq.Header.Get("Authorization") != "" {
				t.Fatalf("redirect to %s leaked Authorization", targetURL)
			}
			if nextReq.Header.Get("Referer") != "" {
				t.Fatalf("redirect to %s leaked Referer", targetURL)
			}
		}

		// Cross-host bounce back to origin must still strip Authorization.
		origReq, _ := http.NewRequest(http.MethodGet, "https://huggingface.co/api/models", nil)
		midReq, _ := http.NewRequest(http.MethodGet, "https://evil.example/bounce", nil)
		bounceReq, _ := http.NewRequest(http.MethodGet, "https://huggingface.co/api/models/other", nil)
		bounceReq.Header.Set("Authorization", "Bearer "+secret)
		bounceReq.Header.Set("Referer", midReq.URL.String())
		if err := colabOneHubCheckRedirect(bounceReq, []*http.Request{origReq, midReq}); err != nil {
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
			_, _ = w.Write([]byte(`[]`))
		}))
		defer httpTarget.Close()

		httpsOrigin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, httpTarget.URL+"/downgrade", http.StatusFound)
		}))
		defer httpsOrigin.Close()

		client := &ColabOneHubClient{
			BaseURL:    httpsOrigin.URL,
			HTTPClient: httpsOrigin.Client(),
		}
		_, err := client.SearchModels(context.Background(), ColabOneHubSearchOptions{Token: secret})
		if !errors.Is(err, ErrHubNetwork) {
			t.Fatalf("downgrade err = %v, want ErrHubNetwork", err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("downgrade error leaked token: %q", err.Error())
		}
		if httpHits.Load() != 0 {
			t.Fatalf("insecure http target must never be contacted, got %d hits", httpHits.Load())
		}

		// Direct policy check asserts exact refusal reason.
		origReq, _ := http.NewRequest(http.MethodGet, "https://huggingface.co/api/models", nil)
		downReq, _ := http.NewRequest(http.MethodGet, "http://huggingface.co/api/models", nil)
		downErr := colabOneHubCheckRedirect(downReq, []*http.Request{origReq})
		if downErr == nil || !strings.Contains(downErr.Error(), "refusing insecure redirect downgrade") {
			t.Fatalf("direct downgrade err = %v, want refusing insecure redirect downgrade", downErr)
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
			_, _ = w.Write([]byte(`[]`))
		}))
		defer srv.Close()

		client := NewColabOneHubClient()
		client.BaseURL = srv.URL

		// 4 redirects (5 total requests) succeed under len(via) >= 5 cap.
		maxHops.Store(4)
		if _, err := client.SearchModels(context.Background(), ColabOneHubSearchOptions{Token: secret}); err != nil {
			t.Fatalf("4 redirects should succeed, got %v", err)
		}
		if finalHits.Load() != 1 {
			t.Fatalf("finalHits for 4 redirects = %d, want 1", finalHits.Load())
		}

		// 5 and 6 redirects are refused without reaching final endpoint.
		for _, hops := range []int32{5, 6} {
			finalHits.Store(0)
			maxHops.Store(hops)
			_, err := client.SearchModels(context.Background(), ColabOneHubSearchOptions{Token: secret})
			if !errors.Is(err, ErrHubNetwork) {
				t.Fatalf("%d redirects err = %v, want ErrHubNetwork", hops, err)
			}
			if finalHits.Load() != 0 {
				t.Fatalf("%d redirects should not reach final target, got %d hits", hops, finalHits.Load())
			}
		}

		// Direct policy check asserts exact hop-cap error message.
		req, _ := http.NewRequest(http.MethodGet, "https://huggingface.co/api/models", nil)
		via := []*http.Request{req, req, req, req, req}
		if err := colabOneHubCheckRedirect(req, via); err == nil || !strings.Contains(err.Error(), "stopped after 5 redirects") {
			t.Fatalf("direct 5-hop err = %v, want stopped after 5 redirects", err)
		}
	})
}

func TestColabOneHubSearchOptionsRedactsToken(t *testing.T) {
	const secret = "hf_super_secret_hub_search_token"
	opts := ColabOneHubSearchOptions{
		Query:    "Qwen",
		Sort:     "downloads",
		Cursor:   "cursor-1",
		Token:    secret,
		Modality: "text",
		Family:   "Qwen",
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
		if !strings.Contains(s, "Qwen") {
			t.Fatalf("format[%d] missing non-secret fields: %q", i, s)
		}
	}

	emptyOpts := ColabOneHubSearchOptions{Query: "Llama"}
	if s := emptyOpts.String(); strings.Contains(s, "[REDACTED]") || !strings.Contains(s, `Token:""`) {
		t.Fatalf("empty token formatting unexpected: %q", s)
	}

	var nilOpts *ColabOneHubSearchOptions
	if got := fmt.Sprintf("%v %#v", nilOpts, nilOpts); got == "" {
		t.Fatalf("nil options formatting failed")
	}
}

func TestModelFilesShardSizeOverflowGuarded(t *testing.T) {
	validSHA := strings.Repeat("a", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]any{
			"sha": strings.Repeat("b", 40),
			"siblings": []map[string]any{
				// Overflow across 2 shards: MaxInt64-10 + 100 must clamp to MaxInt64 and mark unsupported.
				{"rfilename": "overflow2-Q4_K_M-00001-of-00002.gguf", "lfs": map[string]any{"size": int64(math.MaxInt64 - 10), "sha256": validSHA}},
				{"rfilename": "overflow2-Q4_K_M-00002-of-00002.gguf", "lfs": map[string]any{"size": int64(100), "sha256": validSHA}},
				// Overflow across 3 shards (subsequent shard must stay clamped and unsupported).
				{"rfilename": "overflow3-Q4_K_M-00001-of-00003.gguf", "lfs": map[string]any{"size": int64(math.MaxInt64/2 + 10), "sha256": validSHA}},
				{"rfilename": "overflow3-Q4_K_M-00002-of-00003.gguf", "lfs": map[string]any{"size": int64(math.MaxInt64/2 + 10), "sha256": validSHA}},
				{"rfilename": "overflow3-Q4_K_M-00003-of-00003.gguf", "lfs": map[string]any{"size": int64(50), "sha256": validSHA}},
				// Exact MaxInt64 boundary: (MaxInt64-10) + 10 must remain supported without overflow.
				{"rfilename": "exactmax-Q4_K_M-00001-of-00002.gguf", "lfs": map[string]any{"size": int64(math.MaxInt64 - 10), "sha256": validSHA}},
				{"rfilename": "exactmax-Q4_K_M-00002-of-00002.gguf", "lfs": map[string]any{"size": int64(10), "sha256": validSHA}},
			},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	client := &ColabOneHubClient{BaseURL: srv.URL, HTTPClient: srv.Client()}
	res, err := client.ModelFiles(context.Background(), "org/overflow-model", "")
	if err != nil {
		t.Fatalf("ModelFiles failed: %v", err)
	}

	byName := make(map[string]ColabOneHubFile, len(res.Files))
	for _, f := range res.Files {
		byName[f.Filename] = f
	}

	for _, name := range []string{"overflow2-Q4_K_M-00001-of-00002.gguf", "overflow3-Q4_K_M-00001-of-00003.gguf"} {
		f, ok := byName[name]
		if !ok {
			t.Fatalf("missing expected file %s", name)
		}
		if f.Size != math.MaxInt64 {
			t.Errorf("%s: Size = %d, want math.MaxInt64 (%d)", name, f.Size, int64(math.MaxInt64))
		}
		if f.Supported {
			t.Errorf("%s: Supported = true, want false on int64 overflow", name)
		}
		if f.Reason != missingMetaReason {
			t.Errorf("%s: Reason = %q, want %q", name, f.Reason, missingMetaReason)
		}
	}

	exact, ok := byName["exactmax-Q4_K_M-00001-of-00002.gguf"]
	if !ok {
		t.Fatal("missing exactmax file")
	}
	if !exact.Supported || exact.Size != math.MaxInt64 || exact.Reason != "" {
		t.Errorf("exactmax: got %+v, want Supported=true Size=MaxInt64 Reason=\"\"", exact)
	}
}

func TestInt64FieldNaNInfAndOverflow(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want int64
	}{
		{"nil", nil, 0},
		{"non_number_string", "123", 0},
		{"valid_int", json.Number("42"), 42},
		{"max_int64", json.Number("9223372036854775807"), math.MaxInt64},
		{"min_int64", json.Number("-9223372036854775808"), math.MinInt64},
		{"min_int64_float", json.Number("-9223372036854775808.0"), math.MinInt64},
		{"valid_float", json.Number("1234.9"), 1234},
		{"valid_sci", json.Number("1.5e3"), 1500},
		{"max_int64_plus_1", json.Number("9223372036854775808"), 0},
		{"max_int64_plus_1_float", json.Number("9223372036854775808.0"), 0},
		{"min_int64_minus_1", json.Number("-9223372036854775809"), 0},
		{"large_pos_sci", json.Number("1e20"), 0},
		{"large_neg_sci", json.Number("-1e20"), 0},
		{"overflow_float64", json.Number("1e309"), 0},
		{"nan", json.Number("NaN"), 0},
		{"pos_inf", json.Number("+Inf"), 0},
		{"neg_inf", json.Number("-Inf"), 0},
		{"infinity", json.Number("Infinity"), 0},
		{"neg_infinity", json.Number("-Infinity"), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := int64Field(tc.in); got != tc.want {
				t.Errorf("int64Field(%v) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}

	// End-to-end SearchModels check with out-of-range JSON numbers.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"org/overflow","downloads":1e20,"likes":9223372036854775808,"pipeline_tag":"text-generation"}]`))
	}))
	defer srv.Close()

	client := &ColabOneHubClient{BaseURL: srv.URL, HTTPClient: srv.Client()}
	res, err := client.SearchModels(context.Background(), ColabOneHubSearchOptions{})
	if err != nil {
		t.Fatalf("SearchModels failed: %v", err)
	}
	if len(res.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(res.Models))
	}
	if res.Models[0].Downloads != 0 || res.Models[0].Likes != 0 {
		t.Fatalf("out-of-range downloads/likes must sanitize to 0, got downloads=%d likes=%d", res.Models[0].Downloads, res.Models[0].Likes)
	}
}
