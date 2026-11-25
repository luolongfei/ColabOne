package controller

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/luolongfei/ColabOne/console/internal/comfyui"
)

func TestComfyUITtsPreviewCommandsCapabilityTokenAndChunking(t *testing.T) {
	ctx := context.Background()
	cfgDir := t.TempDir()
	vault := NewColabOneMemoryVault()
	_ = vault.Set("hf", "hf_vault_preview_token_999")

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

	// Seed active remote state advertising legacy capabilities (omitting comfyui_tts_preview).
	ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state":        "ready",
			"id":           "ep-1",
			"protocol":     7,
			"capabilities": []string{"public_api", "comfyui", "comfyui_hf_token"},
		},
	})

	if _, err := ctrl.Command(ctx, "comfyui.tts.preview", map[string]any{
		"engine_id": "voxcpm2",
	}); err == nil {
		t.Fatalf("expected pre-upgrade capability error when comfyui_tts_preview is absent")
	}

	// Enable comfyui_tts_preview capability.
	ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state":        "ready",
			"id":           "ep-1",
			"protocol":     7,
			"capabilities": []string{"public_api", "comfyui", "comfyui_hf_token", "comfyui_tts_preview"},
		},
	})

	// Non-string and nil hf_token must be rejected explicitly (no raw Go JSON reflection leak).
	if _, err := ctrl.Command(ctx, "comfyui.tts.preview", map[string]any{
		"engine_id": "voxcpm2",
		"hf_token":  12345,
	}); err == nil || strings.Contains(err.Error(), "alias.") {
		t.Fatalf("expected clean non-string hf_token error, got %v", err)
	}
	if _, err := ctrl.Command(ctx, "comfyui.tts.preview", map[string]any{
		"engine_id": "voxcpm2",
		"hf_token":  nil,
	}); err == nil || strings.Contains(err.Error(), "alias.") {
		t.Fatalf("expected clean nil hf_token error, got %v", err)
	}

	// Cache-hit: injects vault hf_token and does NOT call RefreshRemote (snapshot).
	previewID := strings.Repeat("a", 32)
	snapshotCalls := 0
	var lastPreviewBody map[string]any
	prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if path == "snapshot" {
			snapshotCalls++
			return map[string]any{
				"runtime":       map[string]any{"state": "ready", "id": "ep-1", "protocol": 7, "capabilities": []any{"public_api", "comfyui", "comfyui_hf_token", "comfyui_tts_preview"}, "tasks": []any{}},
				"models":        []any{},
				"workflows":     []any{},
				"comfy_outputs": []any{},
			}, nil
		}
		if method == "POST" && path == "comfyui/tts-preview" {
			lastPreviewBody = asMap(body)
			return map[string]any{
				"cached":     true,
				"preview_id": previewID,
				"engine_id":  "voxcpm2",
				"data":       base64.StdEncoding.EncodeToString([]byte("RIFFmock")),
			}, nil
		}
		return map[string]any{"ok": true}, nil
	}

	hitRes, err := ctrl.Command(ctx, "comfyui.tts.preview", map[string]any{
		"engine_id":    "voxcpm2",
		"voice_preset": "shen_zhiyi",
	})
	if err != nil {
		t.Fatalf("cache-hit comfyui.tts.preview failed: %v", err)
	}
	if !asBool(asMap(hitRes)["cached"]) {
		t.Fatalf("expected cached=true, got %#v", hitRes)
	}
	if asString(lastPreviewBody["hf_token"]) != "hf_vault_preview_token_999" {
		t.Fatalf("expected vault hf_token auto-injection, got %#v", lastPreviewBody)
	}
	if snapshotCalls != 0 {
		t.Fatalf("cache hit must not trigger RefreshRemote, got %d calls", snapshotCalls)
	}

	// Cache-miss: triggers RefreshRemote once.
	prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if path == "snapshot" {
			snapshotCalls++
			return map[string]any{
				"runtime":       map[string]any{"state": "ready", "id": "ep-1", "protocol": 7, "capabilities": []any{"public_api", "comfyui", "comfyui_hf_token", "comfyui_tts_preview"}, "tasks": []any{}},
				"models":        []any{},
				"workflows":     []any{},
				"comfy_outputs": []any{},
			}, nil
		}
		if method == "POST" && path == "comfyui/tts-preview" {
			return map[string]any{
				"cached":     false,
				"preview_id": previewID,
				"engine_id":  "voxcpm2",
				"task":       map[string]any{"id": "task-1", "kind": "comfyui_tts_preview", "state": "running"},
			}, nil
		}
		return map[string]any{"ok": true}, nil
	}
	if _, err := ctrl.Command(ctx, "comfyui.tts.preview", map[string]any{
		"engine_id": "voxcpm2",
	}); err != nil {
		t.Fatalf("cache-miss comfyui.tts.preview failed: %v", err)
	}
	if snapshotCalls != 1 {
		t.Fatalf("cache miss should trigger RefreshRemote once, got %d", snapshotCalls)
	}

	// Chunked comfyui.tts.preview.asset reassembles multi-chunk WAV payloads cleanly and accepts `id` alias.
	fullPayload := []byte("RIFF----WAVEfmt chunk0-data||chunk1-data-end")
	sum := sha256.Sum256(fullPayload)
	sumHex := hex.EncodeToString(sum[:])
	part0 := fullPayload[:20]
	part1 := fullPayload[20:]
	chunkRequests := 0
	prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if method == "GET" && strings.HasPrefix(path, "comfyui/tts-previews/"+previewID) {
			chunkRequests++
			if strings.Contains(path, "offset=0") {
				return map[string]any{
					"cached":           true,
					"preview_id":       previewID,
					"engine_id":        "voxcpm2",
					"mime":             "audio/wav",
					"size":             len(fullPayload),
					"sha256":           sumHex,
					"sample_rate":      48000,
					"duration_seconds": 1.2,
					"offset":           0,
					"chunk_size":       len(part0),
					"has_more":         true,
					"data":             base64.StdEncoding.EncodeToString(part0),
				}, nil
			}
			return map[string]any{
				"cached":           true,
				"preview_id":       previewID,
				"engine_id":        "voxcpm2",
				"mime":             "audio/wav",
				"size":             len(fullPayload),
				"sha256":           sumHex,
				"sample_rate":      48000,
				"duration_seconds": 1.2,
				"offset":           len(part0),
				"chunk_size":       len(part1),
				"has_more":         false,
				"data":             base64.StdEncoding.EncodeToString(part1),
			}, nil
		}
		return map[string]any{"ok": true}, nil
	}

	assetRes, err := ctrl.Command(ctx, "comfyui.tts.preview.asset", map[string]any{
		"id": previewID,
	})
	if err != nil {
		t.Fatalf("chunked comfyui.tts.preview.asset failed: %v", err)
	}
	if chunkRequests != 2 {
		t.Fatalf("expected 2 chunked GET requests, got %d", chunkRequests)
	}
	assetMap := asMap(assetRes)
	decoded, err := base64.StdEncoding.DecodeString(asString(assetMap["data"]))
	if err != nil {
		t.Fatalf("decode assembled base64: %v", err)
	}
	if string(decoded) != string(fullPayload) {
		t.Fatalf("assembled payload = %q, want %q", string(decoded), string(fullPayload))
	}
	if asString(assetMap["sha256"]) != sumHex {
		t.Fatalf("sha256 = %q, want %q", asString(assetMap["sha256"]), sumHex)
	}

	// Invalid preview_id rejected before RPC.
	if _, err := ctrl.Command(ctx, "comfyui.tts.preview.asset", map[string]any{
		"preview_id": "not-32-hex",
	}); err == nil {
		t.Fatalf("expected invalid preview_id error")
	}
}

func newTtsPreviewTestController(t *testing.T, rt map[string]any) (*ColabOneController, *mockProvider) {
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
	if rt != nil {
		ctrl.Update(map[string]any{"runtime": rt})
	}
	return ctrl, prov
}

func TestComfyUITtsPreviewHardeningRegressions(t *testing.T) {
	ctx := context.Background()
	previewID := strings.Repeat("b", 32)
	readyWithPreviewCap := func() map[string]any {
		return map[string]any{
			"state":        "ready",
			"id":           "ep-1",
			"protocol":     7,
			"capabilities": []string{"public_api", "comfyui", "comfyui_hf_token", "comfyui_tts_preview"},
		}
	}

	t.Run("hf_token_length_priority_matches_server", func(t *testing.T) {
		overlongWithControl := strings.Repeat("a", 513) + "\n"
		if _, err := comfyui.ValidateCleanHFToken(overlongWithControl); err == nil || !strings.Contains(err.Error(), "hf_token 长度不能超过 512") {
			t.Fatalf("expected length error before control-char error, got %v", err)
		}
		if _, err := validateCleanHFToken(overlongWithControl); err == nil || !strings.Contains(err.Error(), "hf_token 长度不能超过 512") {
			t.Fatalf("expected delegated controller validateCleanHFToken to return length error, got %v", err)
		}
	})

	t.Run("chunked_sha256_happy_and_tampered", func(t *testing.T) {
		ctrl, prov := newTtsPreviewTestController(t, readyWithPreviewCap())
		fullPayload := []byte("RIFF0000WAVEfmt valid-chunk-0||valid-chunk-1")
		sum := sha256.Sum256(fullPayload)
		sumHex := hex.EncodeToString(sum[:])
		part0 := fullPayload[:18]
		part1 := fullPayload[18:]

		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if strings.Contains(path, "offset=0") {
				return map[string]any{
					"preview_id": previewID,
					"size":       len(fullPayload),
					"sha256":     strings.ToUpper(sumHex),
					"offset":     0,
					"chunk_size": len(part0),
					"has_more":   true,
					"data":       base64.StdEncoding.EncodeToString(part0),
				}, nil
			}
			return map[string]any{
				"preview_id": previewID,
				"size":       len(fullPayload),
				"sha256":     sumHex,
				"offset":     len(part0),
				"chunk_size": len(part1),
				"has_more":   false,
				"data":       base64.StdEncoding.EncodeToString(part1),
			}, nil
		}

		res, err := ctrl.Command(ctx, "comfyui.tts.preview.asset", map[string]any{"preview_id": previewID})
		if err != nil {
			t.Fatalf("expected valid chunked transfer to succeed, got %v", err)
		}
		resMap := asMap(res)
		if gotSize, ok := asInt(resMap["size"]); !ok || gotSize != len(fullPayload) {
			t.Fatalf("size = %v, want %d", resMap["size"], len(fullPayload))
		}
		gotBytes, err := base64.StdEncoding.DecodeString(asString(resMap["data"]))
		if err != nil || string(gotBytes) != string(fullPayload) {
			t.Fatalf("assembled payload = %q (err=%v), want %q", string(gotBytes), err, string(fullPayload))
		}

		// Tampered second chunk of same length must fail SHA-256 verification.
		tamperedPart1 := append([]byte(nil), part1...)
		tamperedPart1[len(tamperedPart1)-1] ^= 0xff
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if strings.Contains(path, "offset=0") {
				return map[string]any{
					"preview_id": previewID,
					"size":       len(fullPayload),
					"sha256":     sumHex,
					"offset":     0,
					"chunk_size": len(part0),
					"has_more":   true,
					"data":       base64.StdEncoding.EncodeToString(part0),
				}, nil
			}
			return map[string]any{
				"preview_id": previewID,
				"size":       len(fullPayload),
				"sha256":     sumHex,
				"offset":     len(part0),
				"chunk_size": len(tamperedPart1),
				"has_more":   false,
				"data":       base64.StdEncoding.EncodeToString(tamperedPart1),
			}, nil
		}
		if _, err := ctrl.Command(ctx, "comfyui.tts.preview.asset", map[string]any{"preview_id": previewID}); err == nil || err.Error() != "试听音频数据校验失败，请重试" {
			t.Fatalf("expected SHA-256 mismatch error, got %v", err)
		}
	})

	t.Run("single_shot_remote_backward_compatible", func(t *testing.T) {
		ctrl, prov := newTtsPreviewTestController(t, readyWithPreviewCap())
		wav := []byte("RIFF----WAVEfmt legacy-single-shot")
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			return map[string]any{
				"cached":     true,
				"preview_id": previewID,
				"mime":       "audio/wav",
				"data":       base64.StdEncoding.EncodeToString(wav),
			}, nil
		}
		res, err := ctrl.Command(ctx, "comfyui.tts.preview.asset", map[string]any{"preview_id": previewID})
		if err != nil {
			t.Fatalf("expected single-shot asset response to succeed, got %v", err)
		}
		resMap := asMap(res)
		if gotSize, ok := asInt(resMap["size"]); !ok || gotSize != len(wav) {
			t.Fatalf("expected out[size]=%d, got %v", len(wav), resMap["size"])
		}
	})

	t.Run("truncated_oversized_and_empty_transfers_rejected", func(t *testing.T) {
		ctrl, prov := newTtsPreviewTestController(t, readyWithPreviewCap())

		// Truncated: expectedSize=40, total delivered=25.
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if strings.Contains(path, "offset=0") {
				chunk := []byte(strings.Repeat("a", 15))
				return map[string]any{
					"size":       40,
					"offset":     0,
					"chunk_size": len(chunk),
					"has_more":   true,
					"data":       base64.StdEncoding.EncodeToString(chunk),
				}, nil
			}
			chunk := []byte(strings.Repeat("b", 10))
			return map[string]any{
				"size":       40,
				"offset":     15,
				"chunk_size": len(chunk),
				"has_more":   false,
				"data":       base64.StdEncoding.EncodeToString(chunk),
			}, nil
		}
		if _, err := ctrl.Command(ctx, "comfyui.tts.preview.asset", map[string]any{"preview_id": previewID}); err == nil || err.Error() != "试听音频传输不完整，请重试" {
			t.Fatalf("expected truncated transfer error, got %v", err)
		}

		// Oversized: expectedSize=20, total delivered=24.
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if strings.Contains(path, "offset=0") {
				chunk := []byte(strings.Repeat("a", 12))
				return map[string]any{
					"size":       20,
					"offset":     0,
					"chunk_size": len(chunk),
					"has_more":   true,
					"data":       base64.StdEncoding.EncodeToString(chunk),
				}, nil
			}
			chunk := []byte(strings.Repeat("b", 12))
			return map[string]any{
				"size":       20,
				"offset":     12,
				"chunk_size": len(chunk),
				"has_more":   false,
				"data":       base64.StdEncoding.EncodeToString(chunk),
			}, nil
		}
		if _, err := ctrl.Command(ctx, "comfyui.tts.preview.asset", map[string]any{"preview_id": previewID}); err == nil || err.Error() != "试听音频传输不完整，请重试" {
			t.Fatalf("expected oversized transfer error, got %v", err)
		}

		// Empty payload.
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			return map[string]any{
				"size":       0,
				"offset":     0,
				"chunk_size": 0,
				"has_more":   false,
				"data":       "",
			}, nil
		}
		if _, err := ctrl.Command(ctx, "comfyui.tts.preview.asset", map[string]any{"preview_id": previewID}); err == nil || err.Error() != "试听音频数据为空" {
			t.Fatalf("expected empty payload error, got %v", err)
		}
	})

	t.Run("wrong_offset_and_chunk_size_rejected", func(t *testing.T) {
		ctrl, prov := newTtsPreviewTestController(t, readyWithPreviewCap())
		part0 := []byte("0123456789")
		part1 := []byte("abcdefghij")

		// Wrong offset on chunk 1.
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if strings.Contains(path, "offset=0") {
				return map[string]any{
					"size":       20,
					"offset":     0,
					"chunk_size": len(part0),
					"has_more":   true,
					"data":       base64.StdEncoding.EncodeToString(part0),
				}, nil
			}
			return map[string]any{
				"size":       20,
				"offset":     99,
				"chunk_size": len(part1),
				"has_more":   false,
				"data":       base64.StdEncoding.EncodeToString(part1),
			}, nil
		}
		if _, err := ctrl.Command(ctx, "comfyui.tts.preview.asset", map[string]any{"preview_id": previewID}); err == nil || err.Error() != "试听音频传输不完整，请重试" {
			t.Fatalf("expected wrong offset error, got %v", err)
		}

		// Wrong chunk_size mismatching decoded length.
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			return map[string]any{
				"size":       len(part0),
				"offset":     0,
				"chunk_size": len(part0) + 5,
				"has_more":   false,
				"data":       base64.StdEncoding.EncodeToString(part0),
			}, nil
		}
		if _, err := ctrl.Command(ctx, "comfyui.tts.preview.asset", map[string]any{"preview_id": previewID}); err == nil || err.Error() != "试听音频传输不完整，请重试" {
			t.Fatalf("expected wrong chunk_size error, got %v", err)
		}
	})

	t.Run("cross_chunk_sha256_and_size_drift_rejected", func(t *testing.T) {
		ctrl, prov := newTtsPreviewTestController(t, readyWithPreviewCap())
		part0 := []byte("0123456789")
		part1 := []byte("abcdefghij")
		shaA := strings.Repeat("1", 64)
		shaB := strings.Repeat("2", 64)

		// SHA-256 drift between chunk 0 and chunk 1.
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if strings.Contains(path, "offset=0") {
				return map[string]any{
					"size":       20,
					"sha256":     shaA,
					"offset":     0,
					"chunk_size": len(part0),
					"has_more":   true,
					"data":       base64.StdEncoding.EncodeToString(part0),
				}, nil
			}
			return map[string]any{
				"size":       20,
				"sha256":     shaB,
				"offset":     len(part0),
				"chunk_size": len(part1),
				"has_more":   false,
				"data":       base64.StdEncoding.EncodeToString(part1),
			}, nil
		}
		if _, err := ctrl.Command(ctx, "comfyui.tts.preview.asset", map[string]any{"preview_id": previewID}); err == nil || err.Error() != "试听音频缓存已变更，请重试" {
			t.Fatalf("expected sha256 drift error, got %v", err)
		}

		// Size drift between chunk 0 and chunk 1.
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if strings.Contains(path, "offset=0") {
				return map[string]any{
					"size":       20,
					"sha256":     shaA,
					"offset":     0,
					"chunk_size": len(part0),
					"has_more":   true,
					"data":       base64.StdEncoding.EncodeToString(part0),
				}, nil
			}
			return map[string]any{
				"size":       24,
				"sha256":     shaA,
				"offset":     len(part0),
				"chunk_size": len(part1),
				"has_more":   false,
				"data":       base64.StdEncoding.EncodeToString(part1),
			}, nil
		}
		if _, err := ctrl.Command(ctx, "comfyui.tts.preview.asset", map[string]any{"preview_id": previewID}); err == nil || err.Error() != "试听音频缓存已变更，请重试" {
			t.Fatalf("expected size drift error, got %v", err)
		}
	})

	t.Run("size_limit_invalid_base64_and_chunk_cap_rejected", func(t *testing.T) {
		ctrl, prov := newTtsPreviewTestController(t, readyWithPreviewCap())

		// Declared size > 16 MiB rejected.
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			return map[string]any{
				"size":     colabOneMaxTtsPreviewBytes + 1,
				"has_more": false,
				"data":     base64.StdEncoding.EncodeToString([]byte("x")),
			}, nil
		}
		if _, err := ctrl.Command(ctx, "comfyui.tts.preview.asset", map[string]any{"preview_id": previewID}); err == nil || !strings.Contains(err.Error(), "16 MiB") {
			t.Fatalf("expected >16 MiB limit error, got %v", err)
		}

		// Invalid base64 rejected.
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			return map[string]any{
				"size":     4,
				"has_more": false,
				"data":     "!!!not-valid-base64!!!",
			}, nil
		}
		if _, err := ctrl.Command(ctx, "comfyui.tts.preview.asset", map[string]any{"preview_id": previewID}); err == nil || err.Error() != "试听音频数据解码失败" {
			t.Fatalf("expected invalid base64 error, got %v", err)
		}

		// Exceeding 256-chunk cap rejected.
		calls := 0
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			off := calls
			calls++
			return map[string]any{
				"size":       colabOneMaxTtsPreviewChunks + 10,
				"offset":     off,
				"chunk_size": 1,
				"has_more":   true,
				"data":       base64.StdEncoding.EncodeToString([]byte("a")),
			}, nil
		}
		if _, err := ctrl.Command(ctx, "comfyui.tts.preview.asset", map[string]any{"preview_id": previewID}); err == nil || err.Error() != "试听音频分片读取次数超出上限" {
			t.Fatalf("expected 256-chunk cap error, got %v", err)
		}
		if calls != colabOneMaxTtsPreviewChunks {
			t.Fatalf("expected exactly %d chunk calls before cap error, got %d", colabOneMaxTtsPreviewChunks, calls)
		}
	})

	t.Run("404_distinction_and_capability_gating", func(t *testing.T) {
		// Pre-upgrade remote omitting capabilities: FastAPI "Not Found" maps to upgrade hint.
		ctrl, prov := newTtsPreviewTestController(t, map[string]any{
			"state":    "ready",
			"id":       "ep-legacy",
			"protocol": 7,
		})
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			return nil, &ColabOneHTTPError{StatusCode: 404, Message: "Not Found"}
		}
		if _, err := ctrl.Command(ctx, "comfyui.tts.preview", map[string]any{"engine_id": "voxcpm2"}); err == nil || err.Error() != colabOneTtsPreviewUpgradeHint {
			t.Fatalf("expected upgrade hint for pre-upgrade FastAPI 404 on preview, got %v", err)
		}
		if _, err := ctrl.Command(ctx, "comfyui.tts.preview.asset", map[string]any{"preview_id": previewID}); err == nil || err.Error() != colabOneTtsPreviewUpgradeHint {
			t.Fatalf("expected upgrade hint for pre-upgrade FastAPI 404 on asset iter 0, got %v", err)
		}

		// Missing resource ("资源不存在") passed through intact (NOT upgrade hint).
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			return nil, &ColabOneHTTPError{StatusCode: 404, Message: "资源不存在"}
		}
		if _, err := ctrl.Command(ctx, "comfyui.tts.preview.asset", map[string]any{"preview_id": previewID}); err == nil || err.Error() != "资源不存在" {
			t.Fatalf("expected resource-missing 404 passed through, got %v", err)
		}

		// HTTP 400 mentioning "404" in engine_id passed through intact (NOT upgrade hint).
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			return nil, &ColabOneHTTPError{StatusCode: 400, Message: "未知的远端 TTS 引擎：'tts_404'"}
		}
		if _, err := ctrl.Command(ctx, "comfyui.tts.preview", map[string]any{"engine_id": "tts_404"}); err == nil || err.Error() != "未知的远端 TTS 引擎：'tts_404'" {
			t.Fatalf("expected HTTP 400 tts_404 error passed through, got %v", err)
		}

		// Chunk iter > 0 returning 404 must NOT be translated into upgrade hint (chunk 0 proved route exists).
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if strings.Contains(path, "offset=0") {
				return map[string]any{
					"size":       20,
					"offset":     0,
					"chunk_size": 10,
					"has_more":   true,
					"data":       base64.StdEncoding.EncodeToString([]byte("0123456789")),
				}, nil
			}
			return nil, &ColabOneHTTPError{StatusCode: 404, Message: "Not Found"}
		}
		if _, err := ctrl.Command(ctx, "comfyui.tts.preview.asset", map[string]any{"preview_id": previewID}); err == nil || err.Error() == colabOneTtsPreviewUpgradeHint || err.Error() != "Not Found" {
			t.Fatalf("expected iter>0 404 not translated to upgrade hint, got %v", err)
		}

		// When comfyui_tts_preview capability is advertised, Colab proxy 404 is NOT translated to upgrade hint.
		ctrl.Update(map[string]any{"runtime": readyWithPreviewCap()})
		for _, proxyErr := range []error{
			&ColabOneHTTPError{StatusCode: 404, Message: "Colab request failed (HTTP 404): Not Found"},
			&ColabOneHTTPError{StatusCode: 404, Message: "Jupyter proxy returned 404"},
			&ColabOneHTTPError{StatusCode: 404, Message: "Kernel does not exist"},
			&ColabOneHTTPError{StatusCode: 404, Message: "Not Found"},
		} {
			prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
				return nil, proxyErr
			}
			if _, err := ctrl.Command(ctx, "comfyui.tts.preview", map[string]any{"engine_id": "voxcpm2"}); err == nil || err.Error() == colabOneTtsPreviewUpgradeHint {
				t.Fatalf("expected advertised capability to suppress upgrade hint for %q, got %v", proxyErr.Error(), err)
			}
			if _, err := ctrl.Command(ctx, "comfyui.tts.preview.asset", map[string]any{"preview_id": previewID}); err == nil || err.Error() == colabOneTtsPreviewUpgradeHint {
				t.Fatalf("expected advertised capability to suppress upgrade hint on asset for %q, got %v", proxyErr.Error(), err)
			}
		}

		// Even without advertised capabilities, Colab proxy/kernel 404 is never translated to upgrade hint.
		ctrl.Update(map[string]any{"runtime": map[string]any{"state": "ready", "id": "ep-legacy", "protocol": 7}})
		for _, proxyErr := range []error{
			&ColabOneHTTPError{StatusCode: 404, Message: "Colab request failed (HTTP 404): Not Found"},
			&ColabOneHTTPError{StatusCode: 404, Message: "Jupyter proxy error"},
			&ColabOneHTTPError{StatusCode: 404, Message: "Kernel missing"},
		} {
			prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
				return nil, proxyErr
			}
			if _, err := ctrl.Command(ctx, "comfyui.tts.preview", map[string]any{"engine_id": "voxcpm2"}); err == nil || err.Error() == colabOneTtsPreviewUpgradeHint {
				t.Fatalf("expected Colab proxy/kernel 404 %q not translated to upgrade hint, got %v", proxyErr.Error(), err)
			}
		}
	})

	t.Run("oversized_metadata_only_cached_passthrough_skips_refresh", func(t *testing.T) {
		ctrl, prov := newTtsPreviewTestController(t, readyWithPreviewCap())
		snapshotCalls := 0
		prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if path == "snapshot" {
				snapshotCalls++
				return map[string]any{"runtime": readyWithPreviewCap()}, nil
			}
			if method == "POST" && path == "comfyui/tts-preview" {
				return map[string]any{
					"cached":           true,
					"preview_id":       previewID,
					"engine_id":        "voxcpm2",
					"mime":             "audio/wav",
					"size":             4 * 1024 * 1024,
					"sample_rate":      48000,
					"duration_seconds": 42.5,
				}, nil
			}
			return map[string]any{"ok": true}, nil
		}
		res, err := ctrl.Command(ctx, "comfyui.tts.preview", map[string]any{"engine_id": "voxcpm2"})
		if err != nil {
			t.Fatalf("oversized metadata-only cached preview failed: %v", err)
		}
		resMap := asMap(res)
		if !asBool(resMap["cached"]) || asString(resMap["preview_id"]) != previewID {
			t.Fatalf("unexpected cached metadata-only response: %#v", res)
		}
		if _, hasData := resMap["data"]; hasData {
			t.Fatalf("expected metadata-only response without inline data, got %#v", res)
		}
		if snapshotCalls != 0 {
			t.Fatalf("expected 0 RefreshRemote calls for oversized cached pass-through, got %d", snapshotCalls)
		}
	})

	t.Run("non_ready_state_with_stale_legacy_capabilities_not_upgrade_hint", func(t *testing.T) {
		for _, state := range []string{"stopped", "starting", "reconnecting"} {
			ctrl, prov := newTtsPreviewTestController(t, map[string]any{
				"state":        state,
				"id":           "ep-stale",
				"protocol":     7,
				"capabilities": []string{"public_api", "comfyui"},
			})
			if ctrl.remoteExplicitlyLacksCapability("comfyui_tts_preview") {
				t.Fatalf("state=%q: remoteExplicitlyLacksCapability must be false when state != ready", state)
			}
			if ctrl.hasRemoteCapability("comfyui") {
				t.Fatalf("state=%q: hasRemoteCapability must be false when state != ready", state)
			}
			prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
				return nil, errors.New("请先启动远端 GPU 实例")
			}
			if _, err := ctrl.Command(ctx, "comfyui.tts.preview", map[string]any{"engine_id": "voxcpm2"}); err == nil || err.Error() == colabOneTtsPreviewUpgradeHint || err.Error() != "请先启动远端 GPU 实例" {
				t.Fatalf("state=%q: expected runtime-not-ready error instead of upgrade hint, got %v", state, err)
			}
			if _, err := ctrl.Command(ctx, "comfyui.tts.preview.asset", map[string]any{"preview_id": previewID}); err == nil || err.Error() == colabOneTtsPreviewUpgradeHint || err.Error() != "请先启动远端 GPU 实例" {
				t.Fatalf("state=%q: expected runtime-not-ready error on asset instead of upgrade hint, got %v", state, err)
			}
		}
	})
}

func TestComfyUIRunLongCatNativeCapabilityGuard(t *testing.T) {
	ctx := context.Background()
	revA := strings.Repeat("a", 40)
	comfyRaw, err := json.Marshal(map[string]any{
		"repo":       "https://github.com/Comfy-Org/ComfyUI.git",
		"tag":        "v0.46.0",
		"commit":     revA,
		"port":       8188,
		"proxy_port": 8189,
		"tts_engines": []map[string]any{
			{
				"id":       "moss_tts_v1_5",
				"name":     "MOSS-TTS",
				"repo":     "OpenMOSS-Team/MOSS-TTS",
				"revision": revA,
			},
		},
		"templates": []map[string]any{
			{
				"id":          "digital-human-longcat",
				"name":        "LongCat 1.6 Native",
				"description": "native pipeline",
				"source":      "builtin",
				"pipeline": map[string]any{
					"kind":                  "digital_human",
					"video_backend":         "longcat_avatar_native",
					"default_tts_engine":    "moss_tts_v1_5",
					"supported_tts_engines": []string{"moss_tts_v1_5"},
				},
				"workflow": map[string]any{
					"1": map[string]any{"class_type": "CLIPTextEncode", "inputs": map[string]any{"text": "hi"}},
				},
			},
			{
				"id":          "digital-human-infinitetalk",
				"name":        "InfiniteTalk",
				"description": "comfy pipeline",
				"source":      "builtin",
				"pipeline": map[string]any{
					"kind":                  "digital_human",
					"video_backend":         "infinitetalk",
					"default_tts_engine":    "moss_tts_v1_5",
					"supported_tts_engines": []string{"moss_tts_v1_5"},
				},
				"workflow": map[string]any{
					"1": map[string]any{"class_type": "CLIPTextEncode", "inputs": map[string]any{"text": "hi"}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}

	bundler := defaultMockBundler()
	bundler.comfyJSON = comfyRaw

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
		ServiceBundler: bundler,
		Location:       mockLocation{},
	})
	t.Cleanup(func() { ctrl.Close() })

	var runCalls int
	prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if path == "comfyui/run" {
			runCalls++
			return map[string]any{"id": "task-1", "state": "running"}, nil
		}
		if path == "snapshot" {
			return map[string]any{
				"runtime": map[string]any{
					"state":        "ready",
					"id":           "ep-1",
					"protocol":     7,
					"capabilities": []any{"public_api", "comfyui", "comfyui_hf_token", "comfyui_tts_preview", "comfyui_longcat_native"},
					"tasks":        []any{},
				},
				"models":        []any{},
				"workflows":     []any{},
				"comfy_outputs": []any{},
			}, nil
		}
		return map[string]any{"ok": true}, nil
	}

	// 1. Legacy ready remote without comfyui_longcat_native (both explicit legacy list and omitted capabilities)
	// must reject digital-human-longcat BEFORE sending any RPC, while still allowing digital-human-infinitetalk.
	for _, tc := range []struct {
		name string
		caps any
	}{
		{"legacy_caps_list", []string{"public_api", "comfyui", "comfyui_hf_token", "comfyui_tts_preview"}},
		{"omitted_caps", nil},
	} {
		rt := map[string]any{"state": "ready", "id": "ep-1", "protocol": 7}
		if tc.caps != nil {
			rt["capabilities"] = tc.caps
		}
		ctrl.Update(map[string]any{"runtime": rt})

		before := runCalls
		_, err := ctrl.Command(ctx, "comfyui.run", map[string]any{
			"workflow_id": "digital-human-longcat",
			"overrides":   map[string]any{},
			"inputs":      map[string]any{},
		})
		if err == nil || err.Error() != colabOneLongCatNativeUpgradeHint {
			t.Fatalf("%s: expected upgrade hint %q, got %v", tc.name, colabOneLongCatNativeUpgradeHint, err)
		}
		if runCalls != before {
			t.Fatalf("%s: expected 0 remote comfyui/run calls when blocked, got %d", tc.name, runCalls-before)
		}

		// Non-native built-in template must still run cleanly.
		if _, err := ctrl.Command(ctx, "comfyui.run", map[string]any{
			"workflow_id": "digital-human-infinitetalk",
			"overrides":   map[string]any{},
			"inputs":      map[string]any{},
		}); err != nil {
			t.Fatalf("%s: non-native infinitetalk should run without comfyui_longcat_native, got %v", tc.name, err)
		}
	}

	// 2. Upgraded ready remote advertising comfyui_longcat_native succeeds.
	ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state":        "ready",
			"id":           "ep-1",
			"protocol":     7,
			"capabilities": []string{"public_api", "comfyui", "comfyui_hf_token", "comfyui_longcat_native"},
		},
	})
	beforeNative := runCalls
	if _, err := ctrl.Command(ctx, "comfyui.run", map[string]any{
		"workflow_id": "digital-human-longcat",
		"overrides":   map[string]any{},
		"inputs":      map[string]any{},
	}); err != nil {
		t.Fatalf("native longcat with comfyui_longcat_native capability failed: %v", err)
	}
	if runCalls != beforeNative+1 {
		t.Fatalf("expected 1 remote comfyui/run call, got %d", runCalls-beforeNative)
	}

	// 3. Non-ready runtime state does not emit the upgrade hint.
	ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state":        "stopped",
			"capabilities": []string{"public_api", "comfyui"},
		},
	})
	prov.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		return nil, errors.New("请先启动远端 GPU")
	}
	if _, err := ctrl.Command(ctx, "comfyui.run", map[string]any{
		"workflow_id": "digital-human-longcat",
		"overrides":   map[string]any{},
		"inputs":      map[string]any{},
	}); err == nil || err.Error() == colabOneLongCatNativeUpgradeHint || err.Error() != "请先启动远端 GPU" {
		t.Fatalf("stopped runtime should return start-GPU error rather than upgrade hint, got %v", err)
	}
}
