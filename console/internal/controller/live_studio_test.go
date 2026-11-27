package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/luolongfei/ColabOne/console/internal/migration"
)

func newLiveStudioTestController(t *testing.T, capabilities []string) (*ColabOneController, *mockProvider, *ColabOneMemoryVault) {
	t.Helper()
	cfgDir := t.TempDir()
	vault := NewColabOneMemoryVault()
	prov := newMockProvider(vault)
	ctrl := NewColabOneController(ColabOneControllerConfig{
		Root:           cfgDir,
		ConfigDir:      cfgDir,
		Background:     false,
		Vault:          vault,
		Auth:           newMockAuth(),
		Provider:       prov,
		Models:         &mockModels{},
		Hub:            &mockHub{},
		Comfy:          realComfyTestAdapter{},
		PublicAPI:      mockPublicAPI{},
		AssignProxy:    &mockAssignProxy{},
		Shortcuts:      &mockShortcuts{},
		ServiceBundler: defaultMockBundler(),
		Location:       mockLocation{},
	})
	t.Cleanup(func() { ctrl.Close() })
	if capabilities != nil {
		ctrl.Update(map[string]any{
			"runtime": map[string]any{
				"state":        "ready",
				"id":           "ep-live-1",
				"protocol":     7,
				"capabilities": capabilities,
			},
		})
	}
	return ctrl, prov, vault
}

func TestLiveStudioExternalLLMVaultLifecycleAndNoSecretLeakage(t *testing.T) {
	ctx := context.Background()
	ctrl, prov, vault := newLiveStudioTestController(t, []string{"public_api", "comfyui", "comfyui_live_studio"})

	rawSecret := "sk-live-supersecret-9988abcd"

	// Initial summary is unconfigured.
	initRes, err := ctrl.Command(ctx, "comfyui.live.llm.get", nil)
	if err != nil {
		t.Fatalf("comfyui.live.llm.get failed: %v", err)
	}
	initMap := asMap(initRes)
	if asBool(initMap["configured"]) || asBool(initMap["has_api_key"]) || asString(initMap["masked_api_key"]) != "" {
		t.Fatalf("unexpected initial llm summary: %#v", initMap)
	}

	// Save external LLM config into encrypted vault.
	saveRes, err := ctrl.Command(ctx, "comfyui.live.llm.save", map[string]any{
		"base_url": "https://api.example.com/v1/",
		"model":    "gpt-4o-mini",
		"api_key":  rawSecret,
	})
	if err != nil {
		t.Fatalf("comfyui.live.llm.save failed: %v", err)
	}
	saveMap := asMap(saveRes)
	if !asBool(saveMap["configured"]) || !asBool(saveMap["has_api_key"]) {
		t.Fatalf("expected configured+has_api_key, got %#v", saveMap)
	}
	if asString(saveMap["masked_api_key"]) != "sk-***abcd" {
		t.Fatalf("masked_api_key = %q, want sk-***abcd", asString(saveMap["masked_api_key"]))
	}
	if _, leaked := saveMap["api_key"]; leaked {
		t.Fatalf("save response leaked raw api_key: %#v", saveMap)
	}

	// Verify vault stores raw secret under "live_external_llm".
	storedRaw, ok := vault.Get("live_external_llm")
	if !ok {
		t.Fatal("expected live_external_llm key in vault")
	}
	if asString(asMap(storedRaw)["api_key"]) != rawSecret {
		t.Fatalf("vault did not persist api_key")
	}

	// Update model on the SAME normalized origin (default :443 port) without resending api_key: must preserve existing key.
	updateRes, err := ctrl.Command(ctx, "comfyui.live.llm.save", map[string]any{
		"base_url": "https://API.EXAMPLE.COM:443/v2",
		"model":    "gpt-4.1-mini",
		"api_key":  "",
	})
	if err != nil {
		t.Fatalf("comfyui.live.llm.save preserving key on same origin failed: %v", err)
	}
	if !asBool(asMap(updateRes)["has_api_key"]) || asString(asMap(updateRes)["model"]) != "gpt-4.1-mini" {
		t.Fatalf("expected preserved key and updated model, got %#v", updateRes)
	}

	// Non-bool clear_key must be rejected strictly (Go M2).
	for _, badClear := range []any{"false", "true", 1} {
		if _, err := ctrl.Command(ctx, "comfyui.live.llm.save", map[string]any{
			"base_url":  "https://api.example.com/v1",
			"model":     "gpt-4.1-mini",
			"clear_key": badClear,
		}); err == nil {
			t.Fatalf("expected non-bool clear_key=%#v to fail", badClear)
		}
	}

	// Snapshot and activity logs must NEVER contain the raw secret.
	snapBytes, err := json.Marshal(ctrl.Snapshot())
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if strings.Contains(string(snapBytes), rawSecret) {
		t.Fatalf("snapshot leaked raw API key!")
	}
	snapLLM := asMap(ctrl.Snapshot()["live_external_llm"])
	if asString(snapLLM["masked_api_key"]) != "sk-***abcd" || !asBool(snapLLM["configured"]) {
		t.Fatalf("unexpected snapshot live_external_llm: %#v", snapLLM)
	}

	// Test LLM connectivity hydrates stored key when omitted or on same normalized origin,
	// and scales RPC timeout dynamically with timeout_seconds.
	var testedBody map[string]any
	var lastRPCTimeout time.Duration
	prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if method == "POST" && path == "live/llm-test" {
			testedBody = asMap(body)
			lastRPCTimeout = opts.Timeout
			return map[string]any{"ok": true, "latency_ms": 120}, nil
		}
		return map[string]any{"ok": true}, nil
	}
	if _, err := ctrl.Command(ctx, "comfyui.live.llm.test", map[string]any{}); err != nil {
		t.Fatalf("comfyui.live.llm.test failed: %v", err)
	}
	if asString(testedBody["api_key"]) != rawSecret || asString(testedBody["model"]) != "gpt-4.1-mini" {
		t.Fatalf("llm-test did not hydrate saved config: %#v", testedBody)
	}
	if lastRPCTimeout != 35*time.Second {
		t.Fatalf("default llm-test RPC timeout = %v, want 35s", lastRPCTimeout)
	}

	if _, err := ctrl.Command(ctx, "comfyui.live.llm.test", map[string]any{
		"base_url":        "https://api.example.com./v1",
		"timeout_seconds": 60,
	}); err != nil {
		t.Fatalf("same-origin llm-test with timeout_seconds=60 failed: %v", err)
	}
	if lastRPCTimeout != 120*time.Second {
		t.Fatalf("max llm-test RPC timeout = %v, want capped 120s", lastRPCTimeout)
	}

	// clear_key: true clears only the stored api_key.
	clearedKeyRes, err := ctrl.Command(ctx, "comfyui.live.llm.save", map[string]any{
		"base_url":  "https://api.example.com/v1",
		"model":     "gpt-4.1-mini",
		"clear_key": true,
	})
	if err != nil {
		t.Fatalf("clear_key save failed: %v", err)
	}
	if asBool(asMap(clearedKeyRes)["has_api_key"]) || asBool(asMap(clearedKeyRes)["configured"]) {
		t.Fatalf("expected cleared key state, got %#v", clearedKeyRes)
	}

	// Full clear deletes vault key.
	if _, err := ctrl.Command(ctx, "comfyui.live.llm.clear", nil); err != nil {
		t.Fatalf("comfyui.live.llm.clear failed: %v", err)
	}
	if _, exists := vault.Get("live_external_llm"); exists {
		t.Fatal("expected live_external_llm deleted from vault")
	}

	// Double-check logs never leaked rawSecret.
	for _, entry := range ctrl.activity.Snapshot() {
		if strings.Contains(entry.Message, rawSecret) {
			t.Fatalf("activity log leaked raw secret: %q", entry.Message)
		}
	}
}

func TestLiveStudioCrossOriginKeyExfiltrationBlocked(t *testing.T) {
	ctx := context.Background()
	ctrl, prov, vault := newLiveStudioTestController(t, []string{"public_api", "comfyui", "comfyui_live_studio"})

	vaultedSecret := "sk-openai-vaulted-secret-998877"
	_ = vault.Set("live_external_llm", map[string]any{
		"base_url": "https://api.openai.com/v1",
		"model":    "gpt-4o",
		"api_key":  vaultedSecret,
	})

	rpcCalls := 0
	var lastTestBody map[string]any
	var lastStartBody map[string]any
	prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		rpcCalls++
		if method == "POST" && path == "live/llm-test" {
			lastTestBody = asMap(body)
		}
		if method == "POST" && path == "live/start" {
			lastStartBody = asMap(body)
		}
		return map[string]any{"ok": true}, nil
	}

	// 1. llm.save to a DIFFERENT origin without an inline key MUST NOT carry over vaultedSecret.
	if _, err := ctrl.Command(ctx, "comfyui.live.llm.save", map[string]any{
		"base_url": "https://attacker.example/v1",
		"model":    "gpt-4o",
		"api_key":  "",
	}); err == nil {
		t.Fatal("expected llm.save to different HTTPS origin without api_key to fail")
	}
	// Different port on same host is also a different origin.
	if _, err := ctrl.Command(ctx, "comfyui.live.llm.save", map[string]any{
		"base_url": "https://api.openai.com:8443/v1",
		"model":    "gpt-4o",
	}); err == nil {
		t.Fatal("expected llm.save to different port without api_key to fail")
	}

	// 2. llm.test to a DIFFERENT HTTPS origin without inline api_key MUST fail before any RPC.
	if _, err := ctrl.Command(ctx, "comfyui.live.llm.test", map[string]any{
		"base_url": "https://attacker.example/v1",
		"model":    "gpt-4o",
	}); err == nil {
		t.Fatal("expected llm.test to different HTTPS origin without api_key to fail")
	}
	if rpcCalls != 0 {
		t.Fatalf("expected 0 RPC calls on cross-origin llm.test block, got %d", rpcCalls)
	}

	// 3. llm.test to loopback HTTP without inline api_key MUST NOT send vaulted cloud secret!
	if _, err := ctrl.Command(ctx, "comfyui.live.llm.test", map[string]any{
		"base_url": "http://127.0.0.1:11434/v1",
		"model":    "qwen3-8b",
	}); err != nil {
		t.Fatalf("loopback llm.test failed: %v", err)
	}
	if asString(lastTestBody["api_key"]) != "" {
		t.Fatalf("loopback llm.test leaked vaulted key to loopback host!")
	}

	// 4. live.start with inline external_llm on DIFFERENT origin without inline key MUST fail before RPC.
	prevCalls := rpcCalls
	if _, err := ctrl.Command(ctx, "comfyui.live.start", map[string]any{
		"llm_mode": "external_api",
		"external_llm": map[string]any{
			"base_url": "https://attacker.example/v1",
			"model":    "gpt-4o",
		},
	}); err == nil {
		t.Fatal("expected live.start to different HTTPS origin without api_key to fail")
	}
	if rpcCalls != prevCalls {
		t.Fatalf("expected no live/start RPC call on cross-origin block")
	}

	// 5. live.start with inline external_llm on SAME origin preserves vaulted key AND inline temperature/max_tokens.
	if _, err := ctrl.Command(ctx, "comfyui.live.start", map[string]any{
		"llm_mode": "external_api",
		"external_llm": map[string]any{
			"base_url":    "https://API.OPENAI.COM:443/v1",
			"model":       "gpt-4.1",
			"temperature": 0.4,
			"max_tokens":  512,
		},
	}); err != nil {
		t.Fatalf("same-origin live.start with inline overrides failed: %v", err)
	}
	extSent := asMap(lastStartBody["external_llm"])
	if asString(extSent["api_key"]) != vaultedSecret || asString(extSent["model"]) != "gpt-4.1" {
		t.Fatalf("expected hydrated key on same origin, got %#v", extSent)
	}
	if extSent["temperature"] != 0.4 || extSent["max_tokens"] != 512 {
		t.Fatalf("expected inline temperature & max_tokens preserved, got %#v", extSent)
	}

	// 6. Switching saved provider to loopback without inline key clears vaulted cloud key instead of retaining it.
	savedLoopback, err := ctrl.Command(ctx, "comfyui.live.llm.save", map[string]any{
		"base_url": "http://127.0.0.1:11434/v1",
		"model":    "qwen3-8b",
	})
	if err != nil {
		t.Fatalf("saving loopback provider failed: %v", err)
	}
	if asBool(asMap(savedLoopback)["has_api_key"]) {
		t.Fatalf("switching origin to loopback must not preserve previous provider api_key: %#v", savedLoopback)
	}
}

func TestLiveStudioStartCanvasConfigNilHydrationAndInlineValidation(t *testing.T) {
	ctx := context.Background()
	ctrl, prov, vault := newLiveStudioTestController(t, []string{"public_api", "comfyui", "comfyui_live_studio"})

	validToken := base64.StdEncoding.EncodeToString([]byte(`{"a":"acct123","t":"tun456","s":"secret789"}`))
	_ = vault.Set("comfyui_canvas", map[string]any{
		"mode":         "named",
		"domain":       "comfy.example.com",
		"tunnel_token": validToken,
	})

	var startBody map[string]any
	prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if method == "POST" && path == "live/start" {
			startBody = asMap(body)
		}
		return map[string]any{"ok": true}, nil
	}

	// Explicit nil canvas_config must still hydrate vaulted comfyui_canvas when canvas is not ready.
	if _, err := ctrl.Command(ctx, "comfyui.live.start", map[string]any{
		"canvas_config": nil,
	}); err != nil {
		t.Fatalf("live.start with nil canvas_config failed: %v", err)
	}
	canvasSent := asMap(startBody["canvas_config"])
	if asString(canvasSent["domain"]) != "https://comfy.example.com" {
		t.Fatalf("expected vaulted canvas_config hydrated when inline canvas_config is nil, got %#v", startBody["canvas_config"])
	}

	// Non-nil invalid inline canvas_config must be rejected before RPC.
	if _, err := ctrl.Command(ctx, "comfyui.live.start", map[string]any{
		"canvas_config": map[string]any{
			"mode":         "named",
			"domain":       "localhost",
			"tunnel_token": "bad",
		},
	}); err == nil {
		t.Fatal("expected invalid inline canvas_config to fail")
	}
}

func TestLiveStudioCapabilityGatingAndRoute404Mapping(t *testing.T) {
	ctx := context.Background()
	ctrl, prov, _ := newLiveStudioTestController(t, []string{"public_api", "comfyui"})

	for _, action := range []string{
		"comfyui.live.status",
		"comfyui.live.vram_check",
		"comfyui.live.start",
		"comfyui.live.stop",
		"comfyui.live.session_token",
		"comfyui.live.companion.ticket",
		"comfyui.live.companion.disconnect",
		"comfyui.live.llm.test",
		"comfyui.live.avatars.list",
		"comfyui.live.avatars.save",
		"comfyui.live.avatars.delete",
		"comfyui.live.avatars.clip.upload",
		"comfyui.live.avatars.build_pack",
		"comfyui.live.driving.list",
		"comfyui.live.driving.upload",
		"comfyui.live.driving.delete",
	} {
		_, err := ctrl.Command(ctx, action, map[string]any{})
		if err == nil || err.Error() != colabOneLiveStudioUpgradeHint {
			t.Fatalf("action %q without capability: got err %v, want %q", action, err, colabOneLiveStudioUpgradeHint)
		}
	}

	// When runtime does not advertise capabilities list at all, a route-level 404 still maps to upgrade hint.
	ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state":        "ready",
			"id":           "ep-live-1",
			"protocol":     7,
			"capabilities": []string{},
		},
	})
	prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		return nil, &ColabOneHTTPError{StatusCode: 404, Message: "Not Found"}
	}
	if _, err := ctrl.Command(ctx, "comfyui.live.status", nil); err == nil || err.Error() != colabOneLiveStudioUpgradeHint {
		t.Fatalf("expected 404 route to map to upgrade hint, got %v", err)
	}

	// Even when comfyui_live_studio IS advertised, route-level 404 on newer companion endpoints still maps to upgrade hint.
	ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state":        "ready",
			"id":           "ep-live-1",
			"protocol":     7,
			"capabilities": []string{"public_api", "comfyui", "comfyui_live_studio"},
		},
	})
	for _, action := range []string{"comfyui.live.companion.ticket", "comfyui.live.companion.disconnect"} {
		if _, err := ctrl.Command(ctx, action, nil); err == nil || err.Error() != colabOneLiveStudioUpgradeHint {
			t.Fatalf("action %q 404 with existing capability: got err %v, want %q", action, err, colabOneLiveStudioUpgradeHint)
		}
	}
}

func TestLiveStudioStartHydrationSessionTokenAvatarsDrivingAndSnapshotPropagation(t *testing.T) {
	ctx := context.Background()
	ctrl, prov, vault := newLiveStudioTestController(t, []string{"public_api", "comfyui", "comfyui_live_studio"})

	hfSecret := "hf_live_secret_token_xyz1234567890"
	extKey := "sk-live-start-key-1234567890abcd"
	ephemeralTok := "ephemeral_ws_tok_never_log_me_999"
	_ = vault.Set("hf", hfSecret)
	_ = vault.Set("live_external_llm", map[string]any{
		"base_url": "https://api.example.com/v1",
		"model":    "gpt-4o",
		"api_key":  extKey,
	})

	var startBody map[string]any
	var clipPath string
	var clipBody map[string]any
	var drivingBody map[string]any
	var drivingDeletePath string
	var buildBody map[string]any
	var deletePath string
	snapshotCalls := 0

	prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		switch {
		case method == "GET" && path == "snapshot":
			snapshotCalls++
			return map[string]any{
				"runtime": map[string]any{
					"state":        "ready",
					"id":           "ep-live-1",
					"protocol":     7,
					"capabilities": []any{"public_api", "comfyui", "comfyui_live_studio"},
				},
				"models":        []any{},
				"workflows":     []any{},
				"comfy_outputs": []any{},
				"live_avatars": []any{
					map[string]any{"id": "anchor_1", "name": "小晴"},
				},
				"live_driving_motions": []any{
					map[string]any{"id": "drive_1", "label": "点头驱动"},
				},
				"live_catalog": map[string]any{
					"visual_engines": []any{"flashhead_lite", "musetalk_bank"},
				},
			}, nil
		case method == "POST" && path == "live/start":
			startBody = asMap(body)
			return map[string]any{"ok": true, "state": "starting"}, nil
		case method == "POST" && path == "live/stop":
			return map[string]any{"ok": true, "state": "stopped"}, nil
		case method == "POST" && path == "live/session-token":
			return map[string]any{
				"token":        ephemeralTok,
				"ws_url":       "wss://comfy.example.com/live/ws",
				"ws_path":      "/live/ws",
				"expires_at":   1799999999,
				"canvas_ready": true,
			}, nil
		case method == "GET" && path == "live/avatars":
			return map[string]any{
				"avatars": []any{
					map[string]any{"id": "anchor_1", "name": "小晴"},
				},
			}, nil
		case method == "POST" && path == "live/avatars":
			return map[string]any{"id": "anchor_1", "name": "小晴"}, nil
		case method == "DELETE" && strings.HasPrefix(path, "live/avatars/"):
			deletePath = path
			return map[string]any{"ok": true}, nil
		case method == "POST" && strings.HasPrefix(path, "live/avatars/") && strings.HasSuffix(path, "/clips"):
			clipPath = path
			clipBody = asMap(body)
			return map[string]any{"ok": true, "clip_id": "clip_1"}, nil
		case method == "POST" && path == "live/avatars/build-pack":
			buildBody = asMap(body)
			return map[string]any{"ok": true, "task_id": "task_1"}, nil
		case method == "GET" && path == "live/driving-motions":
			return map[string]any{
				"driving_motions": []any{
					map[string]any{"id": "drive_1", "label": "点头驱动"},
				},
			}, nil
		case method == "POST" && path == "live/driving-motions":
			drivingBody = asMap(body)
			return map[string]any{"ok": true, "id": "drive_1"}, nil
		case method == "DELETE" && strings.HasPrefix(path, "live/driving-motions/"):
			drivingDeletePath = path
			return map[string]any{"ok": true}, nil
		default:
			return map[string]any{"ok": true}, nil
		}
	}

	// VRAM check strips start-only and secret fields.
	var vramBody map[string]any
	origRPC := prov.rpcFn
	prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if method == "POST" && path == "live/vram-check" {
			vramBody = asMap(body)
			return map[string]any{"fits": true, "resident_vram_mib": 16000}, nil
		}
		return origRPC(ctx, method, path, body, opts)
	}
	if _, err := ctrl.Command(ctx, "comfyui.live.vram_check", map[string]any{
		"visual_engine": "flashhead_lite",
		"tts_engine":    "qwen3_tts",
		"llm_mode":      "external_api",
		"hf_token":      hfSecret,
		"external_llm": map[string]any{
			"base_url": "https://api.example.com/v1",
			"model":    "gpt-4o",
			"api_key":  extKey,
		},
	}); err != nil {
		t.Fatalf("comfyui.live.vram_check failed: %v", err)
	}
	for _, forbiddenKey := range []string{"hf_token", "external_llm", "canvas_config", "portrait_input_id"} {
		if _, exists := vramBody[forbiddenKey]; exists {
			t.Fatalf("vram-check body must not contain %q, got %#v", forbiddenKey, vramBody)
		}
	}

	// Start with external_api hydrates vault external LLM and hf_token.
	if _, err := ctrl.Command(ctx, "comfyui.live.start", map[string]any{
		"visual_engine": "flashhead_lite",
		"tts_engine":    "qwen3_tts",
		"llm_mode":      "external_api",
		"director_mode": "dialogue",
		"mic_mode":      "vad",
		"resolution":    "720x1280",
	}); err != nil {
		t.Fatalf("comfyui.live.start failed: %v", err)
	}
	if asString(startBody["hf_token"]) != hfSecret {
		t.Fatalf("expected hydrated hf_token in live/start body")
	}
	extSent := asMap(startBody["external_llm"])
	if asString(extSent["api_key"]) != extKey || asString(extSent["model"]) != "gpt-4o" {
		t.Fatalf("expected hydrated external_llm in live/start body, got %#v", extSent)
	}
	if snapshotCalls < 1 {
		t.Fatalf("expected RefreshRemote after live.start")
	}

	// Snapshot should propagate live_avatars, live_driving_motions, and live_catalog from remote snapshot.
	snap := ctrl.Snapshot()
	avatars := asMapSlice(snap["live_avatars"])
	if len(avatars) != 1 || asString(avatars[0]["id"]) != "anchor_1" {
		t.Fatalf("expected live_avatars in snapshot, got %#v", snap["live_avatars"])
	}
	motions := asMapSlice(snap["live_driving_motions"])
	if len(motions) != 1 || asString(motions[0]["id"]) != "drive_1" {
		t.Fatalf("expected live_driving_motions in snapshot, got %#v", snap["live_driving_motions"])
	}
	catalog := asMap(snap["live_catalog"])
	if len(asAnySlice(catalog["visual_engines"])) != 2 {
		t.Fatalf("expected live_catalog in snapshot, got %#v", snap["live_catalog"])
	}

	// Session token returns token without logging it.
	tokRes, err := ctrl.Command(ctx, "comfyui.live.session_token", nil)
	if err != nil {
		t.Fatalf("comfyui.live.session_token failed: %v", err)
	}
	if asString(asMap(tokRes)["token"]) != ephemeralTok {
		t.Fatalf("unexpected session token response: %#v", tokRes)
	}

	// Avatar list, save, clip upload, build pack, driving list/upload/delete, avatar delete, stop.
	if _, err := ctrl.Command(ctx, "comfyui.live.avatars.list", nil); err != nil {
		t.Fatalf("avatars.list failed: %v", err)
	}
	if _, err := ctrl.Command(ctx, "comfyui.live.avatars.save", map[string]any{
		"avatar_id": "anchor_1",
		"name":      "小晴",
	}); err != nil {
		t.Fatalf("avatars.save failed: %v", err)
	}
	sampleB64 := base64.StdEncoding.EncodeToString([]byte("video-bytes"))
	if _, err := ctrl.Command(ctx, "comfyui.live.avatars.clip.upload", map[string]any{
		"avatar_id": "anchor_1",
		"action_id": "idle_loop",
		"kind":      "loop",
		"filename":  "idle.mp4",
		"data":      sampleB64,
	}); err != nil {
		t.Fatalf("avatars.clip.upload failed: %v", err)
	}
	if clipPath != "live/avatars/anchor_1/clips" {
		t.Fatalf("clip upload path = %q", clipPath)
	}
	if _, hasPathID := clipBody["avatar_id"]; hasPathID {
		t.Fatalf("expected avatar_id stripped from clip upload body, got %#v", clipBody)
	}
	if asString(clipBody["action_id"]) != "idle_loop" {
		t.Fatalf("expected action_id in clip upload body, got %#v", clipBody)
	}
	if _, err := ctrl.Command(ctx, "comfyui.live.avatars.build_pack", map[string]any{
		"avatar_id": "anchor_1",
	}); err != nil {
		t.Fatalf("avatars.build_pack failed: %v", err)
	}
	if asString(buildBody["hf_token"]) != hfSecret {
		t.Fatalf("expected hf_token hydrated into build-pack")
	}
	if _, err := ctrl.Command(ctx, "comfyui.live.driving.list", nil); err != nil {
		t.Fatalf("driving.list failed: %v", err)
	}
	if _, err := ctrl.Command(ctx, "comfyui.live.driving.upload", map[string]any{
		"id":       "drive_1",
		"label":    "点头驱动",
		"filename": "nod.mp4",
		"data":     sampleB64,
	}); err != nil {
		t.Fatalf("driving.upload failed: %v", err)
	}
	if asString(drivingBody["filename"]) != "nod.mp4" || asString(drivingBody["label"]) != "点头驱动" {
		t.Fatalf("unexpected driving upload body: %#v", drivingBody)
	}
	if _, err := ctrl.Command(ctx, "comfyui.live.driving.delete", map[string]any{
		"id": "drive_1",
	}); err != nil {
		t.Fatalf("driving.delete failed: %v", err)
	}
	if drivingDeletePath != "live/driving-motions/drive_1" {
		t.Fatalf("driving delete path = %q", drivingDeletePath)
	}
	if _, err := ctrl.Command(ctx, "comfyui.live.avatars.delete", map[string]any{
		"avatar_id": "anchor_1",
	}); err != nil {
		t.Fatalf("avatars.delete failed: %v", err)
	}
	if deletePath != "live/avatars/anchor_1" {
		t.Fatalf("delete path = %q", deletePath)
	}
	if _, err := ctrl.Command(ctx, "comfyui.live.stop", nil); err != nil {
		t.Fatalf("live.stop failed: %v", err)
	}

	// Stopped runtime clears live_driving_motions in Snapshot().
	ctrl.Update(map[string]any{"runtime": map[string]any{"state": "stopped"}})
	stoppedSnap := ctrl.Snapshot()
	if len(asMapSlice(stoppedSnap["live_driving_motions"])) != 0 {
		t.Fatalf("expected empty live_driving_motions when stopped, got %#v", stoppedSnap["live_driving_motions"])
	}

	// Verify NO secret or session token was ever written to activity logs.
	for _, entry := range ctrl.activity.Snapshot() {
		for _, secret := range []string{hfSecret, extKey, ephemeralTok} {
			if strings.Contains(entry.Message, secret) {
				t.Fatalf("activity log leaked secret %q in %q", secret, entry.Message)
			}
		}
	}
}

func TestLiveStudioClipAndDrivingUploadsRefreshSnapshot(t *testing.T) {
	ctx := context.Background()
	ctrl, prov, _ := newLiveStudioTestController(t, []string{"public_api", "comfyui", "comfyui_live_studio"})

	remoteAvatars := []any{
		map[string]any{"id": "anchor_1", "name": "小晴", "actions": []any{}},
	}
	remoteMotions := []any{}
	snapshotCalls := 0
	failSnapshot := false

	prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		switch {
		case method == "POST" && path == "live/avatars/anchor_1/clips":
			req := asMap(body)
			remoteAvatars = []any{
				map[string]any{
					"id":   "anchor_1",
					"name": "小晴",
					"actions": []any{
						map[string]any{
							"id":        asString(req["action_id"]),
							"label":     "待机循环",
							"clip_path": "clips/idle.mp4",
						},
					},
				},
			}
			return map[string]any{"ok": true, "clip_id": "clip_new_1"}, nil
		case method == "POST" && path == "live/driving-motions":
			req := asMap(body)
			remoteMotions = []any{
				map[string]any{
					"id":       asString(req["id"]),
					"label":    asString(req["label"]),
					"filename": asString(req["filename"]),
				},
			}
			return map[string]any{"ok": true, "id": asString(req["id"])}, nil
		case method == "GET" && path == "snapshot":
			snapshotCalls++
			if failSnapshot {
				return nil, errors.New("temporary snapshot error")
			}
			return map[string]any{
				"runtime": map[string]any{
					"state":        "ready",
					"id":           "ep-live-1",
					"protocol":     7,
					"capabilities": []any{"public_api", "comfyui", "comfyui_live_studio"},
				},
				"models":               []any{},
				"workflows":            []any{},
				"comfy_outputs":        []any{},
				"live_avatars":         remoteAvatars,
				"live_driving_motions": remoteMotions,
				"live_catalog":         map[string]any{},
			}, nil
		default:
			return map[string]any{"ok": true}, nil
		}
	}

	sampleB64 := base64.StdEncoding.EncodeToString([]byte("mock-video-bytes"))

	// 1. Uploading an avatar clip triggers RefreshRemote so the new clip appears immediately in Snapshot().
	clipRes, err := ctrl.Command(ctx, "comfyui.live.avatars.clip.upload", map[string]any{
		"avatar_id": "anchor_1",
		"action_id": "idle_loop",
		"kind":      "loop",
		"filename":  "idle.mp4",
		"data":      sampleB64,
	})
	if err != nil {
		t.Fatalf("comfyui.live.avatars.clip.upload failed: %v", err)
	}
	if asString(asMap(clipRes)["clip_id"]) != "clip_new_1" {
		t.Fatalf("unexpected clip upload response: %#v", clipRes)
	}
	if snapshotCalls != 1 {
		t.Fatalf("expected 1 RefreshRemote call after clip upload, got %d", snapshotCalls)
	}
	avatars := asMapSlice(ctrl.Snapshot()["live_avatars"])
	if len(avatars) != 1 {
		t.Fatalf("expected 1 avatar in Snapshot after clip upload, got %#v", avatars)
	}
	actions := asMapSlice(avatars[0]["actions"])
	if len(actions) != 1 || asString(actions[0]["id"]) != "idle_loop" {
		t.Fatalf("expected newly uploaded clip action in Snapshot, got %#v", actions)
	}

	// 2. Uploading a PersonaLive driving motion triggers RefreshRemote so it appears immediately in Snapshot().
	drvRes, err := ctrl.Command(ctx, "comfyui.live.driving.upload", map[string]any{
		"id":       "drive_nod",
		"label":    "点头驱动",
		"filename": "nod.mp4",
		"data":     sampleB64,
	})
	if err != nil {
		t.Fatalf("comfyui.live.driving.upload failed: %v", err)
	}
	if asString(asMap(drvRes)["id"]) != "drive_nod" {
		t.Fatalf("unexpected driving upload response: %#v", drvRes)
	}
	if snapshotCalls != 2 {
		t.Fatalf("expected 2 RefreshRemote calls after driving upload, got %d", snapshotCalls)
	}
	motions := asMapSlice(ctrl.Snapshot()["live_driving_motions"])
	if len(motions) != 1 || asString(motions[0]["id"]) != "drive_nod" {
		t.Fatalf("expected newly uploaded driving motion in Snapshot, got %#v", motions)
	}

	// 3. RefreshRemote failure after upload is non-fatal: upload response still succeeds and warning is logged.
	failSnapshot = true
	if res, err := ctrl.Command(ctx, "comfyui.live.avatars.clip.upload", map[string]any{
		"avatar_id": "anchor_1",
		"action_id": "idle_loop_2",
		"kind":      "loop",
		"filename":  "idle2.mp4",
		"data":      sampleB64,
	}); err != nil || asString(asMap(res)["clip_id"]) != "clip_new_1" {
		t.Fatalf("clip upload should succeed even when RefreshRemote fails: res=%#v err=%v", res, err)
	}
	if res, err := ctrl.Command(ctx, "comfyui.live.driving.upload", map[string]any{
		"id":       "drive_wave",
		"label":    "挥手驱动",
		"filename": "wave.mp4",
		"data":     sampleB64,
	}); err != nil || asString(asMap(res)["id"]) != "drive_wave" {
		t.Fatalf("driving upload should succeed even when RefreshRemote fails: res=%#v err=%v", res, err)
	}
	if snapshotCalls != 4 {
		t.Fatalf("expected 4 total RefreshRemote calls, got %d", snapshotCalls)
	}
	warnings := 0
	for _, entry := range ctrl.activity.Snapshot() {
		if entry.Level == "warning" && strings.Contains(entry.Message, "刷新状态失败") {
			warnings++
		}
	}
	if warnings < 2 {
		t.Fatalf("expected at least 2 warning logs on RefreshRemote failures, got %d", warnings)
	}
}

func TestLiveStudioAvatarActionsCap64AndMigrationAccountSwitchReset(t *testing.T) {
	ctx := context.Background()
	ctrl, prov, _ := newLiveStudioTestController(t, []string{"public_api", "comfyui", "comfyui_live_studio"})

	prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if method == "POST" && path == "live/avatars" {
			return map[string]any{"id": "anchor_64", "name": "满动作测试"}, nil
		}
		return map[string]any{"ok": true}, nil
	}

	makeActions := func(n int) []any {
		out := make([]any, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, map[string]any{
				"id":    fmt.Sprintf("act_%02d", i),
				"label": fmt.Sprintf("动作 %d", i),
			})
		}
		return out
	}

	// 64 actions must be accepted (aligned with Python max_length=64).
	if _, err := ctrl.Command(ctx, "comfyui.live.avatars.save", map[string]any{
		"id":      "anchor_64",
		"name":    "满动作测试",
		"actions": makeActions(64),
	}); err != nil {
		t.Fatalf("expected 64 actions to succeed, got %v", err)
	}
	// 65 actions must be rejected.
	if _, err := ctrl.Command(ctx, "comfyui.live.avatars.save", map[string]any{
		"id":      "anchor_65",
		"name":    "超限动作测试",
		"actions": makeActions(65),
	}); err == nil {
		t.Fatal("expected 65 actions to fail validation")
	}

	// Migration account switch must reset live_avatars, live_driving_motions, and live_catalog.
	ctrl.Update(map[string]any{
		"live_avatars": []map[string]any{
			{"id": "anchor_old", "name": "旧账号形象"},
		},
		"live_driving_motions": []map[string]any{
			{"id": "drive_old", "label": "旧账号驱动"},
		},
		"live_catalog": map[string]any{
			"visual_engines": []any{"flashhead_lite"},
		},
	})

	payload := &migration.ColabOneMigrationPayload{
		SchemaVersion:   migration.COLABONE_MIGRATION_SCHEMA_VERSION,
		IncludedModules: []string{migration.ModuleAccount},
		Account: &migration.ColabOneMigrationAccountData{
			Colab: map[string]any{
				"token":         "ya29.new-account-token",
				"refresh_token": "1//0g-new-refresh",
				"client_id":     "764086051850-new.apps.googleusercontent.com",
			},
			GoogleProfile: map[string]string{
				"email": "switched@example.com",
			},
		},
	}
	if _, err := ctrl.ApplyMigrationPayload(ctx, payload, []string{migration.ModuleAccount}); err != nil {
		t.Fatalf("ApplyMigrationPayload account switch failed: %v", err)
	}
	snap := ctrl.Snapshot()
	if len(asMapSlice(snap["live_avatars"])) != 0 {
		t.Fatalf("expected live_avatars reset on account switch, got %#v", snap["live_avatars"])
	}
	if len(asMapSlice(snap["live_driving_motions"])) != 0 {
		t.Fatalf("expected live_driving_motions reset on account switch, got %#v", snap["live_driving_motions"])
	}
	if len(asMap(snap["live_catalog"])) != 0 {
		t.Fatalf("expected live_catalog reset on account switch, got %#v", snap["live_catalog"])
	}
}

func TestLiveStudioPostMutationRefreshBoundedTimeout(t *testing.T) {
	ctrl, prov, _ := newLiveStudioTestController(t, []string{"public_api", "comfyui", "comfyui_live_studio"})
	ctrl.livePostMutationRefreshTimeout = 80 * time.Millisecond

	prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if method == "GET" && path == "snapshot" {
			// Simulate a slow/hung post-mutation snapshot refresh.
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return map[string]any{"ok": true, "path": path}, nil
	}

	sampleB64 := base64.StdEncoding.EncodeToString([]byte("video-payload"))
	outerCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cases := []struct {
		action  string
		payload map[string]any
	}{
		{
			action: "comfyui.live.start",
			payload: map[string]any{
				"visual_engine": "flashhead_lite",
				"tts_engine":    "qwen3_tts",
				"llm_mode":      "local_gguf",
				"director_mode": "dialogue",
				"mic_mode":      "vad",
				"resolution":    "720x1280",
			},
		},
		{
			action: "comfyui.live.avatars.clip.upload",
			payload: map[string]any{
				"avatar_id": "anchor_1",
				"action_id": "idle_loop",
				"kind":      "loop",
				"filename":  "idle.mp4",
				"data":      sampleB64,
			},
		},
		{
			action: "comfyui.live.driving.upload",
			payload: map[string]any{
				"label":    "挥手",
				"filename": "wave.mp4",
				"data":     sampleB64,
			},
		},
	}

	for _, tc := range cases {
		start := time.Now()
		res, err := ctrl.Command(outerCtx, tc.action, tc.payload)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("%s failed when post-mutation refresh timed out: %v", tc.action, err)
		}
		if !asBool(asMap(res)["ok"]) {
			t.Fatalf("%s returned unexpected result: %#v", tc.action, res)
		}
		if elapsed > 2*time.Second {
			t.Fatalf("%s took %v, expected bounded refresh timeout (~80ms)", tc.action, elapsed)
		}
	}
}

func TestLiveStudioAsyncStartTaskAndFastMonitorPoll(t *testing.T) {
	ctx := context.Background()
	ctrl, prov, _ := newLiveStudioTestController(t, []string{"public_api", "comfyui", "comfyui_live_studio"})
	ctrl.monitorInterval = 2 * time.Second

	prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if method == "POST" && path == "live/start" {
			return map[string]any{
				"id":       "task-live-start-1",
				"kind":     "live_start",
				"model_id": "qwen3_tts",
				"state":    "running",
				"progress": 0.02,
				"message":  "正在检查并准备实时演播依赖环境...",
			}, nil
		}
		if method == "GET" && path == "snapshot" {
			return map[string]any{
				"runtime": map[string]any{
					"state":        "ready",
					"id":           "ep-live-1",
					"protocol":     7,
					"capabilities": []any{"public_api", "comfyui", "comfyui_live_studio"},
					"live_studio":  map[string]any{"running": false, "state": "starting"},
					"tasks": []any{
						map[string]any{
							"id":       "task-live-start-1",
							"kind":     "live_start",
							"model_id": "qwen3_tts",
							"state":    "running",
							"progress": 0.45,
							"message":  "正在下载模型权重：Qwen3-TTS 1.7B 流式语音（Qwen/Qwen3-TTS-12Hz-1.7B-VoiceDesign）...",
						},
					},
				},
			}, nil
		}
		return map[string]any{"ok": true}, nil
	}

	res, err := ctrl.Command(ctx, "comfyui.live.start", map[string]any{
		"visual_engine": "flashhead_lite",
		"tts_engine":    "qwen3_tts",
		"llm_mode":      "local_gguf",
	})
	if err != nil {
		t.Fatalf("comfyui.live.start async task failed: %v", err)
	}
	resMap := asMap(res)
	if asString(resMap["kind"]) != "live_start" || asString(resMap["state"]) != "running" {
		t.Fatalf("expected async live_start task snapshot, got %#v", res)
	}
	if wait := ctrl.effectiveMonitorWait(); wait != 700*time.Millisecond {
		t.Fatalf("expected 700ms fast monitor poll while live_start running, got %v", wait)
	}
}

func TestLiveStudioCompanionTicketAndDisconnectCommands(t *testing.T) {
	ctx := context.Background()
	ctrl, prov, _ := newLiveStudioTestController(t, []string{"public_api", "comfyui", "comfyui_live_studio"})

	const validCode = "pair-code-xyz-0123456789"
	var calls []string
	prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		calls = append(calls, method+" "+path)
		if method == "POST" && path == "live/companion-ticket" {
			return map[string]any{
				"code":          validCode,
				"expires_at":    float64(time.Now().Add(10 * time.Minute).Unix()),
				"companion_url": "https://studio.example.com/_colabone_live/remote#code=" + validCode,
				"public_origin": "https://studio.example.com",
				"canvas_ready":  true,
			}, nil
		}
		if method == "POST" && path == "live/companion-disconnect" {
			return map[string]any{"connected": false}, nil
		}
		if method == "GET" && path == "snapshot" {
			return map[string]any{
				"runtime": map[string]any{
					"state":        "ready",
					"id":           "ep-live-1",
					"protocol":     7,
					"capabilities": []any{"public_api", "comfyui", "comfyui_live_studio"},
				},
			}, nil
		}
		return map[string]any{"ok": true}, nil
	}

	ticketRes, err := ctrl.Command(ctx, "comfyui.live.companion.ticket", nil)
	if err != nil {
		t.Fatalf("comfyui.live.companion.ticket failed: %v", err)
	}
	tm := asMap(ticketRes)
	if asString(tm["code"]) != validCode || !strings.Contains(asString(tm["companion_url"]), "/_colabone_live/remote#code="+validCode) {
		t.Fatalf("unexpected companion ticket response: %#v", ticketRes)
	}

	discRes, err := ctrl.Command(ctx, "comfyui.live.companion.disconnect", nil)
	if err != nil {
		t.Fatalf("comfyui.live.companion.disconnect failed: %v", err)
	}
	if asBool(asMap(discRes)["connected"]) {
		t.Fatalf("expected connected=false after disconnect, got %#v", discRes)
	}
	if len(calls) < 2 || calls[0] != "POST live/companion-ticket" || calls[1] != "POST live/companion-disconnect" {
		t.Fatalf("unexpected RPC call sequence: %#v", calls)
	}
}

func TestEffectiveMonitorWaitMinimumAcrossConcurrentTasks(t *testing.T) {
	ctrl, _, _ := newLiveStudioTestController(t, []string{"comfyui", "comfyui_live_studio"})
	ctrl.monitorInterval = 2 * time.Second
	ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state": "ready",
			"tasks": []any{
				map[string]any{"id": "t-live", "kind": "live_start", "state": "running"},
				map[string]any{"id": "t-tts", "kind": "comfyui_tts_preview", "state": "running"},
			},
		},
	})
	if wait := ctrl.effectiveMonitorWait(); wait != 350*time.Millisecond {
		t.Fatalf("expected 350ms min wait when live_start precedes comfyui_tts_preview, got %v", wait)
	}
}

func TestLiveStudioCompanionTicketValidation(t *testing.T) {
	ctx := context.Background()
	const goodCode = "AbCdEfGhIjKlMnOpQrStUvWx01234567"

	// 1) Reject malformed, non-HTTPS, private/metadata, wrong-path/fragment, or origin-mismatched ticket responses.
	badCases := []struct {
		name        string
		canvasURL   string
		vaultDomain string
		resp        any
	}{
		{
			name: "non_map_response",
			resp: "not-a-map",
		},
		{
			name: "short_code",
			resp: map[string]any{"code": "too-short-12345"},
		},
		{
			name: "whitespace_in_code",
			resp: map[string]any{"code": "AbCdEfGhIjKlMnOp QrStUvWx"},
		},
		{
			name: "control_char_in_code",
			resp: map[string]any{"code": "AbCdEfGhIjKlMnOp\nQrStUvWx"},
		},
		{
			name: "bad_companion_path",
			resp: map[string]any{"code": goodCode, "companion_path": "/evil/path"},
		},
		{
			name: "http_companion_url",
			resp: map[string]any{"code": goodCode, "companion_url": "http://studio.example.com/_colabone_live/remote#code=" + goodCode},
		},
		{
			name: "http_public_origin",
			resp: map[string]any{"code": goodCode, "public_origin": "http://studio.example.com"},
		},
		{
			name: "userinfo_in_companion_url",
			resp: map[string]any{"code": goodCode, "companion_url": "https://user:pass@studio.example.com/_colabone_live/remote#code=" + goodCode},
		},
		{
			name: "loopback_ip",
			resp: map[string]any{"code": goodCode, "companion_url": "https://127.0.0.1/_colabone_live/remote#code=" + goodCode},
		},
		{
			name: "localhost_host",
			resp: map[string]any{"code": goodCode, "public_origin": "https://localhost"},
		},
		{
			name: "private_rfc1918_ip",
			resp: map[string]any{"code": goodCode, "companion_url": "https://10.0.0.5/_colabone_live/remote#code=" + goodCode},
		},
		{
			name: "link_local_metadata_ip",
			resp: map[string]any{"code": goodCode, "companion_url": "https://169.254.169.254/_colabone_live/remote#code=" + goodCode},
		},
		{
			name: "metadata_hostname",
			resp: map[string]any{"code": goodCode, "public_origin": "https://metadata.google.internal"},
		},
		{
			name: "wrong_companion_url_path",
			resp: map[string]any{"code": goodCode, "companion_url": "https://studio.example.com/phish#code=" + goodCode},
		},
		{
			name: "query_in_companion_url",
			resp: map[string]any{"code": goodCode, "companion_url": "https://studio.example.com/_colabone_live/remote?x=1#code=" + goodCode},
		},
		{
			name: "wrong_fragment_in_companion_url",
			resp: map[string]any{"code": goodCode, "companion_url": "https://studio.example.com/_colabone_live/remote#code=other000000000000"},
		},
		{
			name: "origin_disagreement_between_url_and_public_origin",
			resp: map[string]any{
				"code":          goodCode,
				"public_origin": "https://studio.example.com",
				"companion_url": "https://other.example.com/_colabone_live/remote#code=" + goodCode,
			},
		},
		{
			name:      "mismatch_active_runtime_canvas_url",
			canvasURL: "https://expected.example.com",
			resp: map[string]any{
				"code":          goodCode,
				"public_origin": "https://attacker.example.com",
				"companion_url": "https://attacker.example.com/_colabone_live/remote#code=" + goodCode,
			},
		},
		{
			name:        "mismatch_vaulted_canvas_domain",
			vaultDomain: "vaulted.example.com",
			resp: map[string]any{
				"code":          goodCode,
				"companion_url": "https://attacker.example.com/_colabone_live/remote#code=" + goodCode,
			},
		},
	}

	for _, tc := range badCases {
		t.Run("Reject_"+tc.name, func(t *testing.T) {
			ctrl, prov, v := newLiveStudioTestController(t, []string{"public_api", "comfyui", "comfyui_live_studio"})
			if tc.canvasURL != "" {
				ctrl.Update(map[string]any{
					"runtime": map[string]any{
						"state":        "ready",
						"id":           "ep-live-1",
						"protocol":     7,
						"capabilities": []string{"public_api", "comfyui", "comfyui_live_studio"},
						"comfyui": map[string]any{
							"canvas": map[string]any{"state": "ready", "url": tc.canvasURL},
						},
					},
				})
			}
			if tc.vaultDomain != "" {
				_ = v.Set("comfyui_canvas", map[string]any{"domain": tc.vaultDomain})
			}
			prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
				return tc.resp, nil
			}
			if _, err := ctrl.Command(ctx, "comfyui.live.companion.ticket", nil); err == nil || err.Error() != colabOneLiveCompanionTicketInvalidMsg {
				t.Fatalf("case %s expected invalid ticket error %q, got %v", tc.name, colabOneLiveCompanionTicketInvalidMsg, err)
			}
		})
	}

	// 2) Valid ticket normalizes uppercase host & default :443 port and matches vaulted bare domain.
	t.Run("NormalizeValidTicketWithVaultedCanvasDomain", func(t *testing.T) {
		ctrl, prov, v := newLiveStudioTestController(t, []string{"public_api", "comfyui", "comfyui_live_studio"})
		_ = v.Set("comfyui_canvas", map[string]any{"domain": "Studio.Example.COM"})
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			return map[string]any{
				"code":           goodCode,
				"companion_path": "",
				"public_origin":  "HTTPS://STUDIO.EXAMPLE.COM:443/",
				"companion_url":  "HTTPS://STUDIO.EXAMPLE.COM:443/_colabone_live/remote#code=" + goodCode,
				"canvas_ready":   true,
			}, nil
		}
		res, err := ctrl.Command(ctx, "comfyui.live.companion.ticket", nil)
		if err != nil {
			t.Fatalf("expected valid ticket accepted, got %v", err)
		}
		m := asMap(res)
		if asString(m["companion_path"]) != colabOneLiveCompanionPath {
			t.Fatalf("expected normalized companion_path, got %q", asString(m["companion_path"]))
		}
		if asString(m["public_origin"]) != "https://studio.example.com" {
			t.Fatalf("expected normalized public_origin, got %q", asString(m["public_origin"]))
		}
		if asString(m["companion_url"]) != "https://studio.example.com/_colabone_live/remote#code="+goodCode {
			t.Fatalf("expected normalized companion_url, got %q", asString(m["companion_url"]))
		}
	})

	// 3) Valid ticket with empty companion_url/public_origin when canvas is not ready yet.
	t.Run("AcceptValidTicketWhenCanvasNotReady", func(t *testing.T) {
		ctrl, prov, _ := newLiveStudioTestController(t, []string{"public_api", "comfyui", "comfyui_live_studio"})
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			return map[string]any{
				"code":           goodCode,
				"companion_path": colabOneLiveCompanionPath,
				"public_origin":  "",
				"companion_url":  "",
				"canvas_ready":   false,
			}, nil
		}
		res, err := ctrl.Command(ctx, "comfyui.live.companion.ticket", nil)
		if err != nil {
			t.Fatalf("expected ticket with empty canvas URL accepted, got %v", err)
		}
		m := asMap(res)
		if asString(m["code"]) != goodCode || asString(m["companion_url"]) != "" {
			t.Fatalf("unexpected response: %#v", m)
		}
	})
}
