package controller

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

func TestComfyUITtsPreviewLocalDiskCachePrefetchAndFastPoll(t *testing.T) {
	ctx := context.Background()
	cfgDir := t.TempDir()
	readyCap := map[string]any{
		"state":        "ready",
		"id":           "ep-1",
		"protocol":     7,
		"capabilities": []string{"public_api", "comfyui", "comfyui_hf_token", "comfyui_tts_preview"},
	}

	makeCtrl := func(prov *mockProvider) *ColabOneController {
		v := NewColabOneMemoryVault()
		_ = v.Set("colab_sessions", map[string]any{"ep-1": map[string]any{"endpoint": "ep-1"}})
		_ = v.Set("active_endpoint", "ep-1")
		c := NewColabOneController(ColabOneControllerConfig{
			ConfigDir:      cfgDir,
			Vault:          v,
			Provider:       prov,
			Comfy:          &mockComfy{},
			ServiceBundler: defaultMockBundler(),
			Location:       mockLocation{},
		})
		t.Cleanup(func() { c.Close() })
		c.Update(map[string]any{
			"runtime": readyCap,
			"tts_engines": []map[string]any{
				{
					"id":                    "qwen3_tts",
					"repo":                  "Qwen/Qwen3-TTS-12Hz-1.7B-Base",
					"revision":              strings.Repeat("a", 40),
					"aux_repo":              "Qwen/Qwen3-TTS-12Hz-1.7B-VoiceDesign",
					"aux_revision":          strings.Repeat("b", 40),
					"custom_voice_repo":     "Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice",
					"custom_voice_revision": strings.Repeat("c", 40),
					"sample_rate":           24000,
					"voice_presets": []map[string]any{
						{"id": "shen_zhiyi", "name": "沈知意", "prompt": "female host", "seed": 202601, "preview_text": "你好我是沈知意"},
						{"id": "gu_qinglan", "name": "顾清岚", "prompt": "exec woman", "seed": 202604, "preview_text": "你好我是顾清岚"},
					},
				},
			},
		})
		return c
	}

	wavPayload := []byte("RIFF0000WAVEfmt local-cache-test-audio-bytes")
	wavSum := sha256.Sum256(wavPayload)
	wavSHA := hex.EncodeToString(wavSum[:])
	pid1 := strings.Repeat("d", 32)

	prov1 := &mockProvider{}
	prov1.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if method == "POST" && path == "comfyui/tts-preview" {
			return map[string]any{
				"cached":           true,
				"preview_id":       pid1,
				"engine_id":        "qwen3_tts",
				"voice_preset":     "shen_zhiyi",
				"text":             "你好我是沈知意",
				"sample_rate":      24000,
				"duration_seconds": 1.2,
				"mime":             "audio/wav",
				"size":             len(wavPayload),
				"sha256":           wavSHA,
				"data":             base64.StdEncoding.EncodeToString(wavPayload),
			}, nil
		}
		return map[string]any{"runtime": readyCap}, nil
	}
	ctrl1 := makeCtrl(prov1)

	// 1. Remote inline cached preview persists automatically into local disk cache.
	if _, err := ctrl1.Command(ctx, "comfyui.tts.preview", map[string]any{
		"engine_id":    "qwen3_tts",
		"voice_preset": "shen_zhiyi",
		"preview_text": "你好我是沈知意",
	}); err != nil {
		t.Fatalf("initial preview failed: %v", err)
	}

	// 2. Restart a brand-new controller on the same ConfigDir with NO remote RPCs -> local disk cache hits!
	var remoteCalls int
	prov2 := &mockProvider{}
	prov2.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		remoteCalls++
		if method == "POST" && path == "comfyui/tts-preview/prefetch" {
			m := asMap(body)
			items := asAnySlice(m["items"])
			if len(items) != 1 || asString(asMap(items[0])["voice_preset"]) != "gu_qinglan" {
				t.Fatalf("expected prefetch to filter out locally cached shen_zhiyi and only send gu_qinglan, got %#v", items)
			}
			return map[string]any{
				"active":      true,
				"state":       "running",
				"engine_id":   "qwen3_tts",
				"total":       1,
				"completed":   0,
				"ready_items": []any{},
			}, nil
		}
		if method == "POST" && path == "comfyui/tts-preview/prefetch/cancel" {
			return map[string]any{"active": false, "state": "cancelled"}, nil
		}
		return map[string]any{"runtime": readyCap}, nil
	}
	ctrl2 := makeCtrl(prov2)

	lookupRaw, err := ctrl2.Command(ctx, "comfyui.tts.preview.cache.lookup", map[string]any{
		"engine_id": "qwen3_tts",
		"items": []any{
			map[string]any{"voice_preset": "shen_zhiyi"},
			map[string]any{"voice_preset": "gu_qinglan"},
		},
	})
	if err != nil {
		t.Fatalf("cache.lookup failed: %v", err)
	}
	lookupItems := asMapSlice(asMap(lookupRaw)["items"])
	if len(lookupItems) != 2 || !asBool(lookupItems[0]["cached"]) || asBool(lookupItems[1]["cached"]) {
		t.Fatalf("unexpected cache.lookup statuses: %#v", lookupItems)
	}
	if remoteCalls != 0 {
		t.Fatalf("cache.lookup must be 100%% offline, got %d remote calls", remoteCalls)
	}

	readRaw, err := ctrl2.Command(ctx, "comfyui.tts.preview.cache.read", map[string]any{
		"engine_id":    "qwen3_tts",
		"voice_preset": "shen_zhiyi",
	})
	if err != nil {
		t.Fatalf("cache.read failed: %v", err)
	}
	readMap := asMap(readRaw)
	if !asBool(readMap["cached"]) || !asBool(readMap["local_cached"]) {
		t.Fatalf("expected local disk cache hit across restart, got %#v", readMap)
	}
	decoded, err := base64.StdEncoding.DecodeString(asString(readMap["data"]))
	if err != nil || string(decoded) != string(wavPayload) {
		t.Fatalf("local cached payload mismatch: %q err=%v", string(decoded), err)
	}

	// 3. Prefetch filters out shen_zhiyi (locally cached) and only dispatches gu_qinglan to remote.
	if _, err := ctrl2.Command(ctx, "comfyui.tts.preview.prefetch", map[string]any{
		"engine_id": "qwen3_tts",
		"items": []any{
			map[string]any{"voice_preset": "shen_zhiyi"},
			map[string]any{"voice_preset": "gu_qinglan"},
		},
	}); err != nil {
		t.Fatalf("prefetch failed: %v", err)
	}

	if _, err := ctrl2.Command(ctx, "comfyui.tts.preview.prefetch.cancel", map[string]any{}); err != nil {
		t.Fatalf("prefetch.cancel failed: %v", err)
	}

	// 4. effectiveMonitorWait accelerates polling during active tts preview or prefetch.
	ctrl2.Update(map[string]any{
		"runtime": map[string]any{
			"state": "ready",
			"tasks": []any{map[string]any{"id": "t1", "kind": "comfyui_tts_preview", "state": "running"}},
		},
	})
	if wait := ctrl2.effectiveMonitorWait(); wait > 400*time.Millisecond {
		t.Fatalf("expected accelerated preview monitor wait <= 400ms, got %v", wait)
	}
	ctrl2.Update(map[string]any{
		"runtime": map[string]any{
			"state":   "ready",
			"tasks":   []any{},
			"comfyui": map[string]any{"tts_preview_prefetch": map[string]any{"active": true}},
		},
	})
	if wait := ctrl2.effectiveMonitorWait(); wait > 800*time.Millisecond {
		t.Fatalf("expected accelerated prefetch monitor wait <= 800ms, got %v", wait)
	}
}

func TestTtsPreviewCacheHardeningJSONNumberCorruptDeleteSweepAndFIFO(t *testing.T) {
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
	defer ctrl.Close()

	// 1. saveLocalTtsPreview parses json.Number duration_seconds and rejects negative/NaN.
	wavBytes := []byte("RIFF\x24\x00\x00\x00WAVEfmt test-wav-body")
	pid1 := "1111aaaa2222bbbb3333cccc4444dddd"
	ctrl.saveLocalTtsPreview(
		map[string]any{"engine_id": "qwen3_tts", "voice_preset": "shen_zhiyi"},
		map[string]any{
			"preview_id":       pid1,
			"engine_id":        "qwen3_tts",
			"voice_preset":     "shen_zhiyi",
			"duration_seconds": json.Number("2.75"),
			"sample_rate":      json.Number("24000"),
		},
		wavBytes,
	)
	res1, err := ctrl.readLocalTtsPreview(map[string]any{"preview_id": pid1})
	if err != nil {
		t.Fatalf("readLocalTtsPreview after json.Number save failed: %v", err)
	}
	if d, _ := asFloat(asMap(res1)["duration_seconds"]); d != 2.75 {
		t.Fatalf("expected duration_seconds 2.75 from json.Number, got %#v", asMap(res1)["duration_seconds"])
	}

	pidNeg := "2222aaaa3333bbbb4444cccc5555dddd"
	ctrl.saveLocalTtsPreview(
		map[string]any{"engine_id": "qwen3_tts", "voice_preset": "gu_qinglan"},
		map[string]any{
			"preview_id":       pidNeg,
			"engine_id":        "qwen3_tts",
			"voice_preset":     "gu_qinglan",
			"duration_seconds": json.Number("-3.5"),
		},
		wavBytes,
	)
	resNeg, err := ctrl.readLocalTtsPreview(map[string]any{"preview_id": pidNeg})
	if err != nil {
		t.Fatalf("readLocalTtsPreview failed: %v", err)
	}
	if d, _ := asFloat(asMap(resNeg)["duration_seconds"]); d != 0 {
		t.Fatalf("expected negative duration_seconds clamped to 0, got %#v", d)
	}

	// 2. Corrupt regular .wav file is deleted on read; symlink .wav is NOT deleted.
	corruptPath := filepath.Join(ctrl.ttsPreviewCacheDir(), pid1+".wav")
	if err := os.WriteFile(corruptPath, []byte("corrupted-mismatch-bytes-longer"), 0o600); err != nil {
		t.Fatal(err)
	}
	if resCorrupt, err := ctrl.readLocalTtsPreview(map[string]any{"preview_id": pid1}); err != nil || asBool(asMap(resCorrupt)["cached"]) {
		t.Fatalf("expected corrupt wav read to return cached=false, got res=%#v err=%v", resCorrupt, err)
	}
	if _, statErr := os.Stat(corruptPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected corrupt regular wav deleted on read, statErr=%v", statErr)
	}

	if runtime.GOOS != "windows" {
		targetFile := filepath.Join(t.TempDir(), "outside.wav")
		if err := os.WriteFile(targetFile, []byte("outside-secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		symWav := filepath.Join(ctrl.ttsPreviewCacheDir(), pidNeg+".wav")
		_ = os.Remove(symWav)
		if err := os.Symlink(targetFile, symWav); err != nil {
			t.Fatal(err)
		}
		if resSym, err := ctrl.readLocalTtsPreview(map[string]any{"preview_id": pidNeg}); err != nil || asBool(asMap(resSym)["cached"]) {
			t.Fatalf("expected symlink wav read to return cached=false, got res=%#v err=%v", resSym, err)
		}
		if _, statErr := os.Lstat(symWav); statErr != nil {
			t.Fatalf("symlink wav should not be unlinked directly as corrupt regular file: %v", statErr)
		}
		if _, statErr := os.Stat(targetFile); statErr != nil {
			t.Fatalf("symlink target must remain intact: %v", statErr)
		}
		_ = os.Remove(symWav)
	}

	// 3. Unindexed .wav sweep during pruning and on corrupt index load.
	orphanWav := filepath.Join(ctrl.ttsPreviewCacheDir(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.wav")
	if err := os.WriteFile(orphanWav, []byte("orphan-wav"), 0o600); err != nil {
		t.Fatal(err)
	}
	pidKeep := "3333aaaa4444bbbb5555cccc6666dddd"
	ctrl.saveLocalTtsPreview(
		map[string]any{"engine_id": "qwen3_tts", "voice_preset": "shen_zhiyi"},
		map[string]any{"preview_id": pidKeep, "engine_id": "qwen3_tts", "voice_preset": "shen_zhiyi"},
		wavBytes,
	)
	if _, statErr := os.Stat(orphanWav); !os.IsNotExist(statErr) {
		t.Fatalf("expected unindexed orphan wav swept during prune, statErr=%v", statErr)
	}

	// Corrupt index.json causes sweep of all unindexed wav files on reload.
	if err := os.WriteFile(ctrl.ttsPreviewCacheIndexPath(), []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctrl.ttsPreviewCacheMu.Lock()
	ctrl.ttsPreviewIndex = nil
	ctrl.loadLocalTtsPreviewIndexLocked()
	ctrl.ttsPreviewCacheMu.Unlock()
	if _, statErr := os.Stat(filepath.Join(ctrl.ttsPreviewCacheDir(), pidKeep+".wav")); !os.IsNotExist(statErr) {
		t.Fatalf("expected unindexed wav swept on corrupt index.json load, statErr=%v", statErr)
	}

	// 4. Deterministic FIFO eviction in rememberPendingTtsPreviewReq (cap 64) and rememberPendingTtsPrefetchContext (cap 128).
	for i := 0; i < 70; i++ {
		pid := fmt.Sprintf("%032x", i+1)
		ctrl.rememberPendingTtsPreviewReq(pid, map[string]any{"engine_id": "qwen3_tts", "voice_preset": fmt.Sprintf("v_%d", i)})
	}
	for i := 0; i < 6; i++ {
		pid := fmt.Sprintf("%032x", i+1)
		if got := ctrl.consumePendingTtsPreviewReq(pid); got != nil {
			t.Fatalf("expected oldest pending preview #%d to be FIFO-evicted, got %#v", i, got)
		}
	}
	for i := 6; i < 70; i++ {
		pid := fmt.Sprintf("%032x", i+1)
		if got := ctrl.consumePendingTtsPreviewReq(pid); got == nil {
			t.Fatalf("expected newer pending preview #%d to be retained", i)
		}
	}

	prefetchItems := make([]map[string]any, 0, 135)
	for i := 0; i < 135; i++ {
		prefetchItems = append(prefetchItems, map[string]any{
			"voice_preset": fmt.Sprintf("preset_%03d", i),
			"preview_text": "hello",
		})
	}
	ctrl.rememberPendingTtsPrefetchContext("qwen3_tts", prefetchItems)
	for i := 0; i < 7; i++ {
		if got := ctrl.lookupPendingTtsPrefetchContext("qwen3_tts", fmt.Sprintf("preset_%03d", i)); got != nil {
			t.Fatalf("expected oldest prefetch context #%d to be FIFO-evicted, got %#v", i, got)
		}
	}
	for i := 7; i < 135; i++ {
		if got := ctrl.lookupPendingTtsPrefetchContext("qwen3_tts", fmt.Sprintf("preset_%03d", i)); got == nil {
			t.Fatalf("expected newer prefetch context #%d to be retained", i)
		}
	}
}
