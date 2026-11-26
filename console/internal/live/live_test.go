package live

import (
	"encoding/base64"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestMaskColabOneSecret(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"abc", "***"},
		{"sk-12345", "***"},
		{"sk-1234567", "***"},
		{"sk-1234567890", "***"},      // 13 runes (< 14)
		{"sk-12345678901", "sk-***8901"}, // 14 runes (>= 14)
		{"sk-12345678abcd", "sk-***abcd"},
		{"  co_secret_token_xyz9  ", "co_***xyz9"},
	}
	for _, tc := range cases {
		if got := MaskColabOneSecret(tc.in); got != tc.want {
			t.Fatalf("MaskColabOneSecret(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestColabOneNormalizedURLOrigin(t *testing.T) {
	valid := []struct {
		in   string
		want string
	}{
		{"https://API.OpenAI.com/v1/", "https://api.openai.com"},
		{"https://api.openai.com:443/v1", "https://api.openai.com"},
		{"https://api.openai.com./v1", "https://api.openai.com"},
		{"https://api.openai.com:8443/v1", "https://api.openai.com:8443"},
		{"http://LOCALHOST:80/v1", "http://localhost"},
		{"http://localhost:11434/v1", "http://localhost:11434"},
		{"http://127.0.0.1:80/v1", "http://127.0.0.1"},
		{"http://[::1]:80/v1", "http://[::1]"},
		{"http://[0:0::1]:8080/v1", "http://[::1]:8080"},
	}
	for _, tc := range valid {
		got, err := ColabOneNormalizedURLOrigin(tc.in)
		if err != nil {
			t.Fatalf("ColabOneNormalizedURLOrigin(%q) unexpected error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ColabOneNormalizedURLOrigin(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	invalid := []string{
		"",
		"   ",
		"ftp://api.openai.com/v1",
		"https://user:pass@api.openai.com/v1",
		"https://api.openai.com:/v1",
		"https://api.openai.com:99999/v1",
		"https://api.openai.com../v1",
		"https://api.openai.com\n/v1",
		"https://api.openai.com /v1",
		"mailto:user@example.com",
	}
	for _, raw := range invalid {
		if got, err := ColabOneNormalizedURLOrigin(raw); err == nil {
			t.Fatalf("ColabOneNormalizedURLOrigin(%q) unexpectedly succeeded with %q", raw, got)
		}
	}
}

func TestValidateColabOneLiveAvatarID(t *testing.T) {
	valid := []string{"a", "0", "avatar_01", "Host-Live_99", strings.Repeat("x", 64)}
	for _, id := range valid {
		got, err := ValidateColabOneLiveAvatarID(id)
		if err != nil || got != id {
			t.Fatalf("expected valid avatar_id %q, got (%q, %v)", id, got, err)
		}
	}
	invalid := []string{
		"",
		"   ",
		"-leading_dash",
		"_leading_underscore",
		"../etc/passwd",
		"a/b",
		"a\\b",
		"avatar 1",
		"avatar.1",
		"avatar\n1",
		"avatar\x001",
		strings.Repeat("x", 65),
	}
	for _, id := range invalid {
		if got, err := ValidateColabOneLiveAvatarID(id); err == nil {
			t.Fatalf("expected invalid avatar_id %q to fail, got %q", id, got)
		}
	}
}

func TestValidateColabOneLiveExternalLLMConfig(t *testing.T) {
	// Valid HTTPS config with optional parameters.
	got, err := ValidateColabOneLiveExternalLLMConfig(map[string]any{
		"base_url":    "https://api.openai.com/v1/",
		"model":       "gpt-4o-mini",
		"api_key":     "sk-proj-test123456abcd",
		"temperature": 0.6,
		"max_tokens":  512,
	}, true)
	if err != nil {
		t.Fatalf("unexpected error for valid HTTPS config: %v", err)
	}
	if got["base_url"] != "https://api.openai.com/v1" {
		t.Fatalf("base_url = %v, want trailing slash trimmed", got["base_url"])
	}
	if got["model"] != "gpt-4o-mini" || got["api_key"] != "sk-proj-test123456abcd" || got["temperature"] != 0.6 || got["max_tokens"] != 512 {
		t.Fatalf("unexpected validated map: %#v", got)
	}

	// Valid exact loopback HTTP URLs.
	for _, u := range []string{
		"http://127.0.0.1:11434/v1",
		"http://localhost:8000/v1/",
		"http://[::1]:8080/v1",
	} {
		res, err := ValidateColabOneLiveExternalLLMConfig(map[string]any{
			"base_url": u,
			"model":    "qwen3-8b",
			"api_key":  "",
		}, false)
		if err != nil {
			t.Fatalf("expected loopback URL %q to succeed: %v", u, err)
		}
		if strings.HasSuffix(res["base_url"].(string), "/") {
			t.Fatalf("expected trailing slash trimmed for %q, got %q", u, res["base_url"])
		}
	}

	// Reject SSRF hosts, query strings, credentials, long model names, unknown keys, dirty keys.
	badCases := []struct {
		name       string
		payload    map[string]any
		requireKey bool
	}{
		{
			name:       "nil payload",
			payload:    nil,
			requireKey: false,
		},
		{
			name: "unknown key rejected",
			payload: map[string]any{
				"base_url": "https://api.openai.com/v1",
				"model":    "gpt-4o",
				"api_key":  "sk-valid12345678",
				"extra":    true,
			},
			requireKey: true,
		},
		{
			name: "model over 160 runes",
			payload: map[string]any{
				"base_url": "https://api.openai.com/v1",
				"model":    strings.Repeat("m", 161),
				"api_key":  "sk-valid12345678",
			},
			requireKey: true,
		},
		{
			name: "non-loopback http",
			payload: map[string]any{
				"base_url": "http://api.openai.com/v1",
				"model":    "gpt-4o",
				"api_key":  "sk-valid12345678",
			},
			requireKey: true,
		},
		{
			name: "http non-exact loopback 127.0.0.2",
			payload: map[string]any{
				"base_url": "http://127.0.0.2:8000/v1",
				"model":    "local",
			},
			requireKey: false,
		},
		{
			name: "http ipv4-mapped loopback",
			payload: map[string]any{
				"base_url": "http://[::ffff:127.0.0.1]:8000/v1",
				"model":    "local",
			},
			requireKey: false,
		},
		{
			name: "http trailing-dot localhost",
			payload: map[string]any{
				"base_url": "http://localhost.:8000/v1",
				"model":    "local",
			},
			requireKey: false,
		},
		{
			name: "https CGNAT 100.64.0.1",
			payload: map[string]any{
				"base_url": "https://100.64.0.1/v1",
				"model":    "gpt-4o",
				"api_key":  "sk-valid12345678",
			},
			requireKey: true,
		},
		{
			name: "https RFC1918 10.0.0.1",
			payload: map[string]any{
				"base_url": "https://10.0.0.1/v1",
				"model":    "gpt-4o",
				"api_key":  "sk-valid12345678",
			},
			requireKey: true,
		},
		{
			name: "https link-local metadata IP",
			payload: map[string]any{
				"base_url": "https://169.254.169.254/latest",
				"model":    "gpt-4o",
				"api_key":  "sk-valid12345678",
			},
			requireKey: true,
		},
		{
			name: "https ipv6 link-local",
			payload: map[string]any{
				"base_url": "https://[fe80::1]/v1",
				"model":    "gpt-4o",
				"api_key":  "sk-valid12345678",
			},
			requireKey: true,
		},
		{
			name: "https trailing-dot metadata host",
			payload: map[string]any{
				"base_url": "https://metadata.google.internal./v1",
				"model":    "gpt-4o",
				"api_key":  "sk-valid12345678",
			},
			requireKey: true,
		},
		{
			name: "https trailing-dot internal host",
			payload: map[string]any{
				"base_url": "https://service.internal./v1",
				"model":    "gpt-4o",
				"api_key":  "sk-valid12345678",
			},
			requireKey: true,
		},
		{
			name: "https trailing-dot local host",
			payload: map[string]any{
				"base_url": "https://printer.local./v1",
				"model":    "gpt-4o",
				"api_key":  "sk-valid12345678",
			},
			requireKey: true,
		},
		{
			name: "decimal integer ipv4 literal",
			payload: map[string]any{
				"base_url": "https://2130706433/v1",
				"model":    "gpt-4o",
				"api_key":  "sk-valid12345678",
			},
			requireKey: true,
		},
		{
			name: "hex ipv4 literal",
			payload: map[string]any{
				"base_url": "https://0x7f000001/v1",
				"model":    "gpt-4o",
				"api_key":  "sk-valid12345678",
			},
			requireKey: true,
		},
		{
			name: "octal ipv4 literal",
			payload: map[string]any{
				"base_url": "https://0177.0.0.1/v1",
				"model":    "gpt-4o",
				"api_key":  "sk-valid12345678",
			},
			requireKey: true,
		},
		{
			name: "shorthand ipv4 literal",
			payload: map[string]any{
				"base_url": "https://127.1/v1",
				"model":    "gpt-4o",
				"api_key":  "sk-valid12345678",
			},
			requireKey: true,
		},
		{
			name: "file scheme",
			payload: map[string]any{
				"base_url": "file:///etc/passwd",
				"model":    "gpt-4o",
				"api_key":  "sk-valid12345678",
			},
			requireKey: true,
		},
		{
			name: "userinfo in url",
			payload: map[string]any{
				"base_url": "https://user:pass@api.openai.com/v1",
				"model":    "gpt-4o",
				"api_key":  "sk-valid12345678",
			},
			requireKey: true,
		},
		{
			name: "query in url",
			payload: map[string]any{
				"base_url": "https://api.openai.com/v1?x=1",
				"model":    "gpt-4o",
				"api_key":  "sk-valid12345678",
			},
			requireKey: true,
		},
		{
			name: "trailing question mark in url",
			payload: map[string]any{
				"base_url": "https://api.openai.com/v1?",
				"model":    "gpt-4o",
				"api_key":  "sk-valid12345678",
			},
			requireKey: true,
		},
		{
			name: "empty model",
			payload: map[string]any{
				"base_url": "https://api.openai.com/v1",
				"model":    "   ",
				"api_key":  "sk-valid12345678",
			},
			requireKey: true,
		},
		{
			name: "missing required api key",
			payload: map[string]any{
				"base_url": "https://api.openai.com/v1",
				"model":    "gpt-4o",
				"api_key":  "",
			},
			requireKey: true,
		},
		{
			name: "newline in api key",
			payload: map[string]any{
				"base_url": "https://api.openai.com/v1",
				"model":    "gpt-4o",
				"api_key":  "sk-valid\n12345678",
			},
			requireKey: true,
		},
		{
			name: "carriage return in api key",
			payload: map[string]any{
				"base_url": "https://api.openai.com/v1",
				"model":    "gpt-4o",
				"api_key":  "sk-valid\r12345678",
			},
			requireKey: true,
		},
		{
			name: "nul in api key",
			payload: map[string]any{
				"base_url": "https://api.openai.com/v1",
				"model":    "gpt-4o",
				"api_key":  "sk-valid\x0012345678",
			},
			requireKey: true,
		},
		{
			name: "whitespace in api key",
			payload: map[string]any{
				"base_url": "https://api.openai.com/v1",
				"model":    "gpt-4o",
				"api_key":  "sk-valid 12345678",
			},
			requireKey: true,
		},
		{
			name: "non-string api key",
			payload: map[string]any{
				"base_url": "https://api.openai.com/v1",
				"model":    "gpt-4o",
				"api_key":  12345,
			},
			requireKey: true,
		},
	}
	for _, tc := range badCases {
		if _, err := ValidateColabOneLiveExternalLLMConfig(tc.payload, tc.requireKey); err == nil {
			t.Fatalf("case %q: expected error, got nil", tc.name)
		}
	}
}

func TestValidateColabOneLiveStartConfig(t *testing.T) {
	// Defaults populate cleanly on empty map with ONLY canonical keys.
	def, err := ValidateColabOneLiveStartConfig(map[string]any{})
	if err != nil {
		t.Fatalf("default start config failed: %v", err)
	}
	if def["visual_engine"] != "flashhead_lite" ||
		def["tts_engine"] != "qwen3_tts" ||
		def["llm_mode"] != "local_gguf" ||
		def["director_mode"] != "dialogue" ||
		def["mic_mode"] != "vad" ||
		def["resolution"] != "720x1280" {
		t.Fatalf("unexpected defaults: %#v", def)
	}

	// Explicit valid alternate values, dimensions alias, empty external_llm omitted, nil canvas_config omitted.
	custom, err := ValidateColabOneLiveStartConfig(map[string]any{
		"visual_engine":       "musetalk_bank",
		"personalive_enabled": true,
		"tts_engine":          "voxcpm2",
		"voice_preset":        "shen_zhiyi",
		"llm_mode":            "external_api",
		"local_llm_model_id":  "qwen3-8b",
		"director_mode":       "director",
		"mic_mode":            "ptt",
		"width":               1080,
		"height":              1920,
		"avatar_id":           "host_01",
		"target_fps":          25,
		"enable_rtmp":         true,
		"rtmp_url":            "rtmps://a.rtmp.youtube.com/live2/stream-key",
		"portrait_input_id":   "0123456789abcdef0123456789abcdef",
		"external_llm":        map[string]any{},
		"canvas_config":       nil,
	})
	if err != nil {
		t.Fatalf("custom start config failed: %v", err)
	}
	if custom["resolution"] != "1080x1920" || custom["avatar_id"] != "host_01" || custom["target_fps"] != 25 || custom["personalive_enabled"] != true {
		t.Fatalf("unexpected custom config: %#v", custom)
	}
	for _, omitted := range []string{"width", "height", "external_llm", "canvas_config"} {
		if _, exists := custom[omitted]; exists {
			t.Fatalf("expected %q omitted from validated start config, got %#v", omitted, custom)
		}
	}

	// Invalid enum / resolution / strict bool / SSRF RTMP / unknown keys / float cases.
	badConfigs := []map[string]any{
		{"visual_engine": "unknown_engine"},
		{"personalive_enabled": "true"},
		{"tts_engine": "elevenlabs"},
		{"llm_mode": "cloud_magic"},
		{"director_mode": "autopilot"},
		{"mic_mode": "always_hot"},
		{"resolution": "641x480"},
		{"resolution": "254x256"},
		{"resolution": "2562x1080"},
		{"resolution": "720x1281"},
		{"width": 720},
		{"width": 720, "height": 1280, "resolution": "1280x720"},
		{"avatar_id": "../bad"},
		{"avatar_id": "-bad_leading"},
		{"target_fps": 120},
		{"target_fps": 25.5},
		{"target_fps": math.NaN()},
		{"fps": 25}, // unknown key on ColabOneLiveStartRequest
		{"unknown_extra_field": "forbidden"},
		{"portrait_input_id": "../not-hex"},
		{"hf_token": "hf_bad\ntoken"},
		{"external_llm": map[string]any{"base_url": "http://remote.invalid/v1", "model": "m"}},
		{"enable_rtmp": true, "rtmp_url": ""},
		{"enable_rtmp": true, "rtmp_url": "rtmp://127.0.0.1/live/key"},
		{"enable_rtmp": true, "rtmp_url": "rtmp://localhost/live/key"},
		{"enable_rtmp": true, "rtmp_url": "rtmp://[::1]/live/key"},
		{"enable_rtmp": true, "rtmp_url": "rtmp://10.0.0.5/live/key"},
		{"enable_rtmp": true, "rtmp_url": "rtmp://169.254.169.254/live/key"},
		{"enable_rtmp": true, "rtmp_url": "rtmp://100.64.0.1/live/key"},
		{"enable_rtmp": true, "rtmp_url": "rtmp://metadata.google.internal./live/key"},
		{"enable_rtmp": true, "rtmp_url": "rtmp://2130706433/live/key"},
	}
	for idx, cfg := range badConfigs {
		if _, err := ValidateColabOneLiveStartConfig(cfg); err == nil {
			t.Fatalf("bad config #%d (%#v) unexpectedly passed", idx, cfg)
		}
	}
}

func TestValidateColabOneLiveAvatarClipAndBuildPack(t *testing.T) {
	samplePortraitB64 := base64.StdEncoding.EncodeToString([]byte("mock-png-bytes"))

	// Avatar payload maps UI aliases (avatar_id -> id, portrait_data -> portrait_b64),
	// supports product tag, and preserves explicit empty arrays.
	av, err := ValidateColabOneLiveAvatarPayload(map[string]any{
		"avatar_id":            "anchor_1",
		"name":                 "数字主播小晴",
		"persona_prompt":       "热情开朗的直播间主播",
		"portrait_data":        "data:image/png;base64," + samplePortraitB64,
		"portrait_filename":    "cover.png",
		"default_tts_engine":   "qwen3_tts",
		"default_voice_preset": "shen_zhiyi",
		"actions":              []any{},
		"products": []any{
			map[string]any{
				"id":          "prod_1",
				"name":        "防晒霜",
				"price":       "¥99",
				"tag":         "爆款推荐",
				"highlights":  "轻薄不油腻",
				"script_hint": "夏天必备",
			},
		},
	})
	if err != nil {
		t.Fatalf("valid avatar payload failed: %v", err)
	}
	if av["id"] != "anchor_1" || av["name"] != "数字主播小晴" || av["portrait_b64"] != samplePortraitB64 {
		t.Fatalf("unexpected avatar result: %#v", av)
	}
	if _, leaked := av["avatar_id"]; leaked {
		t.Fatalf("expected avatar_id alias stripped from canonical output: %#v", av)
	}
	if _, leaked := av["portrait_data"]; leaked {
		t.Fatalf("expected portrait_data alias stripped from canonical output: %#v", av)
	}
	acts, hasActs := av["actions"].([]any)
	if !hasActs || acts == nil || len(acts) != 0 {
		t.Fatalf("expected explicit empty actions preserved as []any{}, got %#v", av["actions"])
	}
	prods, hasProds := av["products"].([]any)
	if !hasProds || len(prods) != 1 || prods[0].(map[string]any)["tag"] != "爆款推荐" {
		t.Fatalf("expected product tag preserved, got %#v", av["products"])
	}

	// Explicit empty products: [] preserved; absent/nil omitted.
	avEmptyProds, err := ValidateColabOneLiveAvatarPayload(map[string]any{
		"name":     "清空商品测试",
		"products": []any{},
		"actions":  nil,
	})
	if err != nil {
		t.Fatalf("empty products avatar save failed: %v", err)
	}
	if p, ok := avEmptyProds["products"].([]any); !ok || p == nil || len(p) != 0 {
		t.Fatalf("expected explicit empty products preserved, got %#v", avEmptyProds["products"])
	}
	if _, exists := avEmptyProds["actions"]; exists {
		t.Fatalf("expected nil actions omitted, got %#v", avEmptyProds["actions"])
	}

	for _, badAvatar := range []map[string]any{
		{"name": ""},
		{"name": "ok", "avatar_id": "../evil"},
		{"name": "ok", "id": "_bad_leading"},
		{"name": "ok", "persona_prompt": strings.Repeat("字", 4001)},
		{"name": "ok", "visual_engine": "flashhead_lite"}, // forbidden extra key
		{"name": "ok", "products": []any{map[string]any{"id": "p1", "name": "item", "tag": strings.Repeat("t", 41)}}},
		{"name": "ok", "products": []any{map[string]any{"id": "p1", "name": "item", "extra": 1}}},
		{"name": "ok", "actions": []any{map[string]any{"id": "a1", "label": "act", "clip_path": "../escape.mp4"}}},
	} {
		if _, err := ValidateColabOneLiveAvatarPayload(badAvatar); err == nil {
			t.Fatalf("expected invalid avatar payload %#v to fail", badAvatar)
		}
	}

	// Avatar clip upload (video extensions ONLY + optional BGM).
	sampleB64 := base64.StdEncoding.EncodeToString([]byte("mock-media-payload"))
	for _, fname := range []string{"idle.mp4", "loop.webm", "clip.mov", "dance.mkv"} {
		up, err := ValidateColabOneLiveAvatarClipUpload(map[string]any{
			"action_id":        "dance_01",
			"label":            "热舞片段",
			"kind":             "dance",
			"filename":         fname,
			"data":             "data:video/mp4;base64," + sampleB64,
			"bgm_data":         sampleB64,
			"bgm_filename":     "track.m4a",
			"duration_seconds": 12.5,
			"trigger_keywords": []any{"跳舞", "才艺"},
		})
		if err != nil {
			t.Fatalf("expected valid avatar clip %q to pass: %v", fname, err)
		}
		if up["data"] != sampleB64 || up["action_id"] != "dance_01" || up["bgm_data"] != sampleB64 {
			t.Fatalf("unexpected validated avatar clip for %q: %#v", fname, up)
		}
	}
	// Optional bgm_filename omitted when bgm_data is supplied.
	if _, err := ValidateColabOneLiveAvatarClipUpload(map[string]any{
		"action_id": "dance_02",
		"filename":  "clip.mp4",
		"data":      sampleB64,
		"bgm_data":  sampleB64,
	}); err != nil {
		t.Fatalf("expected avatar clip with bgm_data and omitted bgm_filename to pass: %v", err)
	}
	for _, bad := range []map[string]any{
		{"filename": "idle.mp4", "data": sampleB64}, // missing required action_id
		{"action_id": "-bad", "filename": "idle.mp4", "data": sampleB64},
		{"action_id": "act_1", "filename": "voice.mp3", "data": sampleB64}, // audio-only rejected as clip
		{"action_id": "act_1", "filename": "shell.sh", "data": sampleB64},
		{"action_id": "act_1", "filename": "../idle.mp4", "data": sampleB64},
		{"action_id": "act_1", "filename": ".mp4", "data": sampleB64},
		{"action_id": "act_1", "filename": "idle.mp4", "data": ""},
		{"action_id": "act_1", "filename": "idle.mp4", "data": "%%%not-base64%%%"},
		{"action_id": "act_1", "filename": "idle.mp4", "data": sampleB64, "kind": "invalid_kind"},
		{"action_id": "act_1", "filename": "idle.mp4", "data": sampleB64, "bgm_filename": "bad.exe"},
		{"action_id": "act_1", "filename": "idle.mp4", "data": sampleB64, "duration_seconds": 9999},
		{"action_id": "act_1", "filename": "idle.mp4", "data": sampleB64, "clip_kind": "idle"}, // forbidden extra key
	} {
		if _, err := ValidateColabOneLiveAvatarClipUpload(bad); err == nil {
			t.Fatalf("expected invalid avatar clip upload %#v to fail", bad)
		}
	}

	// Driving upload requires non-blank label and rejects avatar-clip-only keys.
	drv, err := ValidateColabOneLiveDrivingUpload(map[string]any{
		"id":               "drive_01",
		"label":            "挥手驱动",
		"filename":         "wave.mkv",
		"data":             sampleB64,
		"bgm_filename":     "bgm.ogg",
		"bgm_data":         sampleB64,
		"duration_seconds": 8.0,
		"trigger_keywords": []any{"挥手"},
	})
	if err != nil {
		t.Fatalf("valid driving upload failed: %v", err)
	}
	if drv["id"] != "drive_01" || drv["label"] != "挥手驱动" || drv["filename"] != "wave.mkv" {
		t.Fatalf("unexpected driving upload map: %#v", drv)
	}
	for _, bad := range []map[string]any{
		{"filename": "nod.mp4", "data": sampleB64},                     // missing required label
		{"label": "   ", "filename": "nod.mp4", "data": sampleB64},     // blank label
		{"label": strings.Repeat("a", 81), "filename": "nod.mp4", "data": sampleB64},
		{"label": "nod", "id": "_bad", "filename": "nod.mp4", "data": sampleB64},
		{"label": "nod", "filename": "voice.wav", "data": sampleB64}, // audio rejected
		{"label": "nod", "filename": "nod.mp4", "data": sampleB64, "action_id": "a1"}, // forbidden key
		{"label": "nod", "filename": "nod.mp4", "data": sampleB64, "kind": "dance"},   // forbidden key
	} {
		if _, err := ValidateColabOneLiveDrivingUpload(bad); err == nil {
			t.Fatalf("expected invalid driving upload %#v to fail", bad)
		}
	}

	// Build pack payload enforces strict keys, limits (actions <= 16, dance_ids <= 8), and integer fps 12..60.
	bp, err := ValidateColabOneLiveBuildPackPayload(map[string]any{
		"avatar_id":         "anchor_1",
		"name":              "数字主播小晴",
		"portrait_input_id": "0123456789abcdef0123456789abcdef",
		"portrait_b64":      samplePortraitB64,
		"portrait_filename": "cover.png",
		"actions":           []any{"idle_loop", "talk_explain"},
		"dance_ids":         []any{"dance_01"},
		"resolution":        "720x1280",
		"fps":               25,
		"overwrite":         true,
	})
	if err != nil {
		t.Fatalf("valid build-pack failed: %v", err)
	}
	if bp["avatar_id"] != "anchor_1" || bp["fps"] != 25 || bp["overwrite"] != true {
		t.Fatalf("unexpected build-pack map: %#v", bp)
	}
	tooManyActions := make([]any, 17)
	for i := range tooManyActions {
		tooManyActions[i] = "act_item"
	}
	tooManyDances := make([]any, 9)
	for i := range tooManyDances {
		tooManyDances[i] = "dance_item"
	}
	for _, badBP := range []map[string]any{
		{},
		{"avatar_id": "anchor_1", "visual_engine": "musetalk_bank"}, // forbidden extra key
		{"avatar_id": "anchor_1", "clip_ids": []any{"idle_01"}},     // forbidden extra key
		{"avatar_id": "anchor_1", "force": true},                    // forbidden extra key
		{"avatar_id": "anchor_1", "fps": 11},
		{"avatar_id": "anchor_1", "fps": 25.5},
		{"avatar_id": "anchor_1", "actions": tooManyActions},
		{"avatar_id": "anchor_1", "dance_ids": tooManyDances},
		{"avatar_id": "anchor_1", "actions": []any{"../bad"}},
	} {
		if _, err := ValidateColabOneLiveBuildPackPayload(badBP); err == nil {
			t.Fatalf("expected invalid build-pack %#v to fail", badBP)
		}
	}
}

func TestValidateColabOneLiveResolutionParityJSONNumberAndNonGlobalPrefixes(t *testing.T) {
	// 1. Resolution parity with Python: <w>x<h> where 256 <= w,h <= 2560 and even.
	for _, res := range []string{"256x256", "640x480", "720x1280", "1920x1080", "2560x1440", "2560x2560"} {
		out, err := ValidateColabOneLiveStartConfig(map[string]any{"resolution": res})
		if err != nil {
			t.Fatalf("expected valid resolution %q to pass start config, got %v", res, err)
		}
		if out["resolution"] != res {
			t.Fatalf("resolution = %v, want %q", out["resolution"], res)
		}
		bp, err := ValidateColabOneLiveBuildPackPayload(map[string]any{"avatar_id": "a1", "resolution": res})
		if err != nil || bp["resolution"] != res {
			t.Fatalf("expected valid resolution %q in build-pack, got bp=%#v err=%v", res, bp, err)
		}
	}
	for _, badRes := range []string{"254x256", "256x254", "2562x1080", "1080x2562", "641x480", "640x481", "100x100", "720X1280", "720x1280x1"} {
		if _, err := ValidateColabOneLiveStartConfig(map[string]any{"resolution": badRes}); err == nil {
			t.Fatalf("expected invalid resolution %q to fail start config", badRes)
		}
		if _, err := ValidateColabOneLiveBuildPackPayload(map[string]any{"avatar_id": "a1", "resolution": badRes}); err == nil {
			t.Fatalf("expected invalid resolution %q to fail build-pack", badRes)
		}
	}

	// 2. json.Number support in parseExactInt, parseFiniteFloat, and containsNonFiniteFloat.
	startCfg, err := ValidateColabOneLiveStartConfig(map[string]any{
		"width":      json.Number("640"),
		"height":     json.Number("480"),
		"target_fps": json.Number("25"),
		"external_llm": map[string]any{
			"base_url":    "https://api.example.com/v1",
			"model":       "gpt-4o",
			"api_key":     "sk-valid-key-1234",
			"temperature": json.Number("0.7"),
			"max_tokens":  json.Number("512"),
		},
	})
	if err != nil {
		t.Fatalf("expected json.Number start config to pass, got %v", err)
	}
	if startCfg["resolution"] != "640x480" || startCfg["target_fps"] != 25 {
		t.Fatalf("unexpected json.Number start config: %#v", startCfg)
	}
	if bp, err := ValidateColabOneLiveBuildPackPayload(map[string]any{
		"avatar_id": "a1",
		"fps":       json.Number("30"),
	}); err != nil || bp["fps"] != 30 {
		t.Fatalf("expected json.Number fps in build-pack to pass, got bp=%#v err=%v", bp, err)
	}
	sampleB64 := base64.StdEncoding.EncodeToString([]byte("video"))
	if clip, err := ValidateColabOneLiveAvatarClipUpload(map[string]any{
		"action_id":        "act_1",
		"filename":         "idle.mp4",
		"data":             sampleB64,
		"duration_seconds": json.Number("12.5"),
	}); err != nil || clip["duration_seconds"] != 12.5 {
		t.Fatalf("expected json.Number duration_seconds to pass, got clip=%#v err=%v", clip, err)
	}
	for _, badNumCfg := range []map[string]any{
		{"target_fps": json.Number("25.5")},
		{"target_fps": json.Number("NaN")},
		{"target_fps": json.Number("Infinity")},
		{"target_fps": json.Number("99999999999999999999999")},
		{"width": json.Number("641"), "height": json.Number("480")},
		{"external_llm": map[string]any{
			"base_url":    "https://api.example.com/v1",
			"model":       "gpt-4o",
			"api_key":     "sk-valid-key-1234",
			"temperature": json.Number("1e309"),
		}},
		{"external_llm": map[string]any{
			"base_url":    "https://api.example.com/v1",
			"model":       "gpt-4o",
			"api_key":     "sk-valid-key-1234",
			"temperature": json.Number("NaN"),
		}},
	} {
		if _, err := ValidateColabOneLiveStartConfig(badNumCfg); err == nil {
			t.Fatalf("expected invalid json.Number config %#v to fail", badNumCfg)
		}
	}

	// 3. Expanded non-global IPv4/IPv6 prefixes blocked for both external LLM URLs and RTMP URLs.
	nonGlobalHosts := []string{
		"192.0.2.1",
		"198.51.100.2",
		"203.0.113.3",
		"[64:ff9b::1]",
		"[64:ff9b:1::1]",
		"[2001::1]",
		"[2001:2::1]",
		"[2001:db8::1]",
		"[2002::1]",
		"[fec0::1]",
	}
	for _, host := range nonGlobalHosts {
		if _, err := ValidateColabOneLiveExternalLLMConfig(map[string]any{
			"base_url": "https://" + host + "/v1",
			"model":    "gpt-4o",
			"api_key":  "sk-test-12345",
		}, true); err == nil {
			t.Fatalf("expected non-global external LLM host %s to be rejected", host)
		}
		if _, err := ValidateColabOneLiveStartConfig(map[string]any{
			"enable_rtmp": true,
			"rtmp_url":    "rtmp://" + host + "/live/key",
		}); err == nil {
			t.Fatalf("expected non-global RTMP host %s to be rejected", host)
		}
	}
}
