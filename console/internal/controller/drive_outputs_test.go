package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkflowAutoReleaseCommandGuardsAndToggle(t *testing.T) {
	h := newTestHarness(t, false)

	// 1) Non-bool enabled is rejected.
	if _, err := h.ctrl.Command(context.Background(), "comfyui.auto_release", map[string]any{"enabled": "yes"}); err == nil || !strings.Contains(err.Error(), "参数不合法") {
		t.Fatalf("expected invalid bool error, got %v", err)
	}

	// 2) Requires running GPU.
	if _, err := h.ctrl.Command(context.Background(), "comfyui.auto_release", map[string]any{"enabled": true}); err == nil || !strings.Contains(err.Error(), "请先启动远端 GPU 实例") {
		t.Fatalf("expected GPU required error, got %v", err)
	}

	// 3) GPU ready but Drive NOT connected -> must block enabling!
	h.provider.addManagedSession("ep-ar", "T4", "STANDARD", true)
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state":        "ready",
			"id":           "ep-ar",
			"protocol":     7,
			"capabilities": []any{"comfyui", "comfyui_workflow_auto_release"},
			"drive":        map[string]any{"connected": false, "mode": "disabled"},
		},
	})
	if _, err := h.ctrl.Command(context.Background(), "comfyui.auto_release", map[string]any{"enabled": true}); err == nil || !strings.Contains(err.Error(), "尚未连接 Google 云盘") {
		t.Fatalf("expected Drive required error, got %v", err)
	}

	// 4) Drive connected, but older remote lacks capability -> upgrade hint.
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state":        "ready",
			"id":           "ep-ar",
			"protocol":     7,
			"capabilities": []any{"comfyui", "comfyui_drive_export"},
			"drive":        map[string]any{"connected": true, "mode": "mount"},
		},
	})
	if _, err := h.ctrl.Command(context.Background(), "comfyui.auto_release", map[string]any{"enabled": true}); err == nil || !strings.Contains(err.Error(), "立即热更新") {
		t.Fatalf("expected hot-upgrade hint, got %v", err)
	}

	// 5) Full ready + Drive connected + capability -> succeeds and refreshes snapshot.
	armed := false
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		switch {
		case method == "POST" && path == "comfyui/auto-release":
			m := asMap(body)
			armed = asBool(m["enabled"])
			state := "idle"
			if armed {
				state = "armed"
			}
			return map[string]any{"enabled": armed, "state": state}, nil
		case method == "GET" && path == "snapshot":
			state := "idle"
			if armed {
				state = "armed"
			}
			return map[string]any{
				"runtime": map[string]any{
					"state":        "ready",
					"id":           "ep-ar",
					"protocol":     7,
					"capabilities": []any{"comfyui", "comfyui_workflow_auto_release"},
					"drive":        map[string]any{"connected": true, "mode": "mount"},
					"comfyui": map[string]any{
						"running": true,
						"auto_release": map[string]any{
							"enabled": armed,
							"state":   state,
						},
					},
				},
				"models":        []any{},
				"workflows":     []any{},
				"comfy_outputs": []any{},
			}, nil
		default:
			return map[string]any{"ok": true}, nil
		}
	}

	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state":        "ready",
			"id":           "ep-ar",
			"protocol":     7,
			"capabilities": []any{"comfyui", "comfyui_workflow_auto_release"},
			"drive":        map[string]any{"connected": true, "mode": "mount"},
		},
	})

	res, err := h.ctrl.Command(context.Background(), "comfyui.auto_release", map[string]any{"enabled": true})
	if err != nil {
		t.Fatalf("comfyui.auto_release(true) failed: %v", err)
	}
	if !asBool(asMap(res)["enabled"]) {
		t.Fatalf("expected enabled=true, got %#v", res)
	}
	snap := h.ctrl.Snapshot()
	ar := asMap(asMap(asMap(snap["runtime"])["comfyui"])["auto_release"])
	if !asBool(ar["enabled"]) || asString(ar["state"]) != "armed" {
		t.Fatalf("expected snapshot auto_release armed, got %#v", ar)
	}

	// Turn off cleanly.
	if _, err := h.ctrl.Command(context.Background(), "comfyui.auto_release", map[string]any{"enabled": false}); err != nil {
		t.Fatalf("comfyui.auto_release(false) failed: %v", err)
	}
	snap2 := h.ctrl.Snapshot()
	ar2 := asMap(asMap(asMap(snap2["runtime"])["comfyui"])["auto_release"])
	if asBool(ar2["enabled"]) || asString(ar2["state"]) != "idle" {
		t.Fatalf("expected snapshot auto_release idle, got %#v", ar2)
	}
}

func TestDriveOutputsPersistAfterReleaseAndStreamOfflineViaDriveAPI(t *testing.T) {
	assetID := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4"
	overnightMetaID := "f0e1d2c3b4a5968778695a4b3c2d1e0f"
	payloadBytes := []byte("fake-mp4-binary-content-0123456789abcdef")
	overnightBytes := []byte("overnight-video-generated-while-sleeping!!")
	overnightHashSum := sha256.Sum256(overnightBytes)
	overnightSHA256 := hex.EncodeToString(overnightHashSum[:])

	var (
		mu             sync.Mutex
		trashedFileIDs []string
		gotQuotaHeader string
	)

	mockDrive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer valid-tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mu.Lock()
		gotQuotaHeader = r.Header.Get("X-Goog-User-Project")
		mu.Unlock()

		// PATCH /files/{id} (trash)
		if r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/files/") {
			fid := strings.TrimPrefix(r.URL.Path, "/files/")
			mu.Lock()
			trashedFileIDs = append(trashedFileIDs, fid)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"` + fid + `","trashed":true}`))
			return
		}

		// GET /files/{id}?alt=media
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/files/") && r.URL.Query().Get("alt") == "media" {
			fid := strings.TrimPrefix(r.URL.Path, "/files/")
			switch fid {
			case "meta-overnight":
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id":            overnightMetaID,
					"workflow_id":   "digital_human",
					"workflow_name": "数字人长视频",
					"kind":          "video",
					"filename":      "sleep_render.mp4",
					"extension":     "mp4",
					"mime":          "video/mp4",
					"size":          len(overnightBytes),
					"sha256":        overnightSHA256,
					"created_at":    1760000200.0,
					"elapsed":       95.5,
				})
				return
			case "file-overnight-export":
				rng := r.Header.Get("Range")
				if strings.HasPrefix(rng, "bytes=") {
					var start, end int
					if _, err := fmt.Sscanf(rng, "bytes=%d-%d", &start, &end); err == nil {
						if end >= len(overnightBytes) {
							end = len(overnightBytes) - 1
						}
						w.WriteHeader(http.StatusPartialContent)
						_, _ = w.Write(overnightBytes[start : end+1])
						return
					}
				}
				_, _ = w.Write(overnightBytes)
				return
			case "file-live-export":
				_, _ = w.Write(payloadBytes)
				return
			default:
				http.NotFound(w, r)
				return
			}
		}

		// GET /files?q=...
		if r.Method == http.MethodGet && r.URL.Path == "/files" {
			q := r.URL.Query().Get("q")
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.Contains(q, "name = 'ColabOne_Drive'"):
				_, _ = w.Write([]byte(`{"files":[{"id":"dir-root","name":"ColabOne_Drive"}]}`))
			case strings.Contains(q, "name = 'workspace'"):
				_, _ = w.Write([]byte(`{"files":[{"id":"dir-ws","name":"workspace"}]}`))
			case strings.Contains(q, "name = 'comfyui-outputs'"):
				_, _ = w.Write([]byte(`{"files":[{"id":"dir-co","name":"comfyui-outputs"}]}`))
			case strings.Contains(q, "name = 'outputs'"):
				_, _ = w.Write([]byte(`{"files":[{"id":"dir-out","name":"outputs"}]}`))
			case strings.Contains(q, "'dir-co' in parents"):
				_ = json.NewEncoder(w).Encode(map[string]any{
					"files": []map[string]any{
						{
							"id":           "meta-overnight",
							"name":         overnightMetaID + ".json",
							"size":         "256",
							"modifiedTime": "2026-11-25T10:00:00Z",
						},
						{
							"id":             "asset-overnight-ws",
							"name":           overnightMetaID + ".mp4",
							"size":           fmt.Sprintf("%d", len(overnightBytes)),
							"modifiedTime":   "2026-11-25T10:00:00Z",
							"sha256Checksum": overnightSHA256,
							"webViewLink":    "https://drive.google.com/file/d/asset-overnight-ws/view",
						},
					},
				})
			case strings.Contains(q, "'dir-out' in parents"):
				_ = json.NewEncoder(w).Encode(map[string]any{
					"files": []map[string]any{
						{
							"id":             "file-overnight-export",
							"name":           "sleep_render.mp4",
							"size":           fmt.Sprintf("%d", len(overnightBytes)),
							"modifiedTime":   "2026-11-25T10:00:05Z",
							"sha256Checksum": overnightSHA256,
							"webViewLink":    "https://drive.google.com/file/d/file-overnight-export/view",
						},
					},
				})
			default:
				_, _ = w.Write([]byte(`{"files":[]}`))
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer mockDrive.Close()

	h := newTestHarness(t, false)
	h.ctrl.driveAPIBaseURL = mockDrive.URL
	h.ctrl.driveHTTPClient = mockDrive.Client()
	h.auth.driveScope = true
	h.auth.requiresQuota = true
	_ = h.vault.Set("colab", map[string]any{"token": "valid-tok", "refresh_token": "rt-1"})
	_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-sleep-user", "email": "sleep@example.com"})
	_ = h.vault.Set("cloud_project", "colabone-drive-1234567890")

	// 1) Simulate a running GPU instance with Drive connected & 1 output exported.
	h.provider.addManagedSession("ep-1", "A100", "STANDARD", true)
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if path == "snapshot" {
			return map[string]any{
				"runtime": map[string]any{
					"state":    "ready",
					"id":       "ep-1",
					"protocol": 7,
					"drive":    map[string]any{"connected": true, "mode": "mount", "remote_folder": "ColabOne_Drive"},
					"comfyui": map[string]any{
						"running": true,
						"auto_release": map[string]any{
							"enabled": false,
							"state":   "releasing",
							"exported": []any{
								map[string]any{
									"id":                  assetID,
									"filename":            "morning_clip.mp4",
									"remote_folder":       "ColabOne_Drive/outputs",
									"drive_path":          "My Drive/ColabOne_Drive/outputs/morning_clip.mp4",
									"drive_file_id":       "file-live-export",
									"drive_web_view_link": "https://drive.google.com/file/d/file-live-export/view",
								},
							},
						},
					},
				},
				"models":    []any{},
				"workflows": []any{},
				"comfy_outputs": []any{
					map[string]any{
						"id":            assetID,
						"workflow_id":   "wf-1",
						"workflow_name": "测试工作流",
						"kind":          "video",
						"filename":      "morning_clip.mp4",
						"extension":     "mp4",
						"mime":          "video/mp4",
						"size":          len(payloadBytes),
						"created_at":    1760000100.0,
						"elapsed":       42.0,
					},
				},
			}, nil
		}
		return map[string]any{"ok": true}, nil
	}

	if err := h.ctrl.RefreshRemote(context.Background(), ""); err != nil {
		t.Fatalf("RefreshRemote failed: %v", err)
	}

	// 2) Simulate GPU released (stopped, no sessions) — previously synced output MUST stay visible!
	h.provider.Clear("ep-1")
	h.ctrl.Update(map[string]any{
		"runtime":       map[string]any{"state": "stopped"},
		"comfy_outputs": []any{},
	})

	snapAfterStop := h.ctrl.Snapshot()
	if asString(asMap(snapAfterStop["runtime"])["state"]) != "stopped" {
		t.Fatalf("expected stopped runtime")
	}
	outputs := asMapSlice(snapAfterStop["comfy_outputs"])
	if len(outputs) != 1 || asString(outputs[0]["id"]) != assetID {
		t.Fatalf("expected persisted Drive output after release, got %#v", outputs)
	}
	if asString(outputs[0]["storage"]) != "drive" || !asBool(outputs[0]["drive_exported"]) || !asBool(outputs[0]["drive_previewable"]) {
		t.Fatalf("expected drive metadata on post-release output, got %#v", outputs[0])
	}

	// 3) Sync directly from Google Drive API v3 while GPU is STOPPED -> discovers overnight output!
	syncRes, err := h.ctrl.Command(context.Background(), "comfyui.outputs.drive.sync", nil)
	if err != nil {
		t.Fatalf("comfyui.outputs.drive.sync failed: %v", err)
	}
	if !asBool(asMap(syncRes)["synced"]) {
		t.Fatalf("expected synced=true, got %#v", syncRes)
	}
	mu.Lock()
	quotaHdr := gotQuotaHeader
	mu.Unlock()
	if quotaHdr != "colabone-drive-1234567890" {
		t.Fatalf("expected X-Goog-User-Project header sent, got %q", quotaHdr)
	}

	snapSynced := h.ctrl.Snapshot()
	syncedOutputs := asMapSlice(snapSynced["comfy_outputs"])
	if len(syncedOutputs) != 1 || asString(syncedOutputs[0]["id"]) != overnightMetaID {
		t.Fatalf("expected overnight output from Drive API, got %#v", syncedOutputs)
	}
	if asString(syncedOutputs[0]["filename"]) != "sleep_render.mp4" ||
		asString(syncedOutputs[0]["drive_file_id"]) != "file-overnight-export" ||
		asString(syncedOutputs[0]["drive_path"]) != "My Drive/ColabOne_Drive/outputs/sleep_render.mp4" {
		t.Fatalf("unexpected overnight output metadata: %#v", syncedOutputs[0])
	}

	// 4) Stream asset chunks directly from Google Drive API v3 WITHOUT GPU running!
	chunkRes, err := h.ctrl.Command(context.Background(), "comfyui.output.asset", map[string]any{
		"id":     overnightMetaID,
		"offset": 0,
	})
	if err != nil {
		t.Fatalf("offline comfyui.output.asset failed: %v", err)
	}
	chunkMap := asMap(chunkRes)
	decoded, err := base64.StdEncoding.DecodeString(asString(chunkMap["data"]))
	if err != nil {
		t.Fatalf("decode base64 chunk failed: %v", err)
	}
	if string(decoded) != string(overnightBytes) {
		t.Fatalf("offline Drive asset bytes mismatch: got %q, want %q", string(decoded), string(overnightBytes))
	}

	// 5) Delete offline -> trashes Drive files and removes from snapshot!
	if _, err := h.ctrl.Command(context.Background(), "comfyui.output.delete", map[string]any{"id": overnightMetaID}); err != nil {
		t.Fatalf("offline comfyui.output.delete failed: %v", err)
	}
	mu.Lock()
	trashed := append([]string(nil), trashedFileIDs...)
	mu.Unlock()
	if len(trashed) != 3 {
		t.Fatalf("expected 3 Drive files trashed (export + workspace asset + workspace meta), got %v", trashed)
	}
	if afterDel := asMapSlice(h.ctrl.Snapshot()["comfy_outputs"]); len(afterDel) != 0 {
		t.Fatalf("expected empty comfy_outputs after delete, got %#v", afterDel)
	}

	// 6) DriveFS-only account (no quota project) retains recorded metadata after release & hides on logout.
	h.auth.requiresQuota = true
	_ = h.vault.Delete("cloud_project")
	h.ctrl.recordExportedDriveOutput(assetID, map[string]any{
		"filename":      "drivefs_clip.mp4",
		"remote_folder": "ColabOne_Drive/outputs",
		"drive_path":    "My Drive/ColabOne_Drive/outputs/drivefs_clip.mp4",
		"size":          4096,
		"mode":          "colab_drivefs",
	})
	driveFSSnap := h.ctrl.Snapshot()
	dfsOut := asMapSlice(driveFSSnap["comfy_outputs"])
	if len(dfsOut) != 1 || asBool(dfsOut[0]["drive_previewable"]) {
		t.Fatalf("expected 1 non-directly-previewable DriveFS record, got %#v", dfsOut)
	}
	if _, err := h.ctrl.Command(context.Background(), "comfyui.output.asset", map[string]any{"id": assetID, "offset": 0}); err == nil || !strings.Contains(err.Error(), "在 Google 云盘查看") {
		t.Fatalf("expected friendly DriveFS open hint, got %v", err)
	}

	// Signing out hides account's Drive outputs.
	_ = h.vault.Delete("colab")
	if signedOut := asMapSlice(h.ctrl.Snapshot()["comfy_outputs"]); len(signedOut) != 0 {
		t.Fatalf("expected no Drive outputs after logout, got %#v", signedOut)
	}
}

func TestDriveOutputsSanitizationRange200FallbackAndErrorHandling(t *testing.T) {
	// Multi-chunk (> 512 KiB) payload served with 200 OK (ignoring Range header) and NO sha256Checksum.
	largePayload := bytes.Repeat([]byte("COLABONE-DRIVE-CHUNK-0123456789!"), (colabOneDriveAssetChunkBytes/32)+16)
	failTrash := true

	mockDrive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/files/") {
			if failTrash {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"trashed":true}`))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/files/file-large-nosha" && r.URL.Query().Get("alt") == "media" {
			// Intentionally ignore Range and respond 200 OK with full payload.
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(largePayload)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(largePayload)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/files" {
			q := r.URL.Query().Get("q")
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.Contains(q, "name = 'ColabOne_Drive'"):
				_, _ = w.Write([]byte(`{"files":[{"id":"dir-root","name":"ColabOne_Drive"}]}`))
			case strings.Contains(q, "name = 'outputs'"):
				_, _ = w.Write([]byte(`{"files":[{"id":"dir-out","name":"outputs"}]}`))
			case strings.Contains(q, "'dir-out' in parents"):
				_ = json.NewEncoder(w).Encode(map[string]any{
					"files": []map[string]any{
						{
							"id":             "file-large-nosha",
							"name":           "large_render.png",
							"size":           fmt.Sprintf("%d", len(largePayload)),
							"modifiedTime":   "2026-11-25T12:00:00Z",
							"sha256Checksum": "not-a-valid-sha256",
							"webViewLink":    "https://evil.example/phish",
						},
					},
				})
			default:
				_, _ = w.Write([]byte(`{"files":[]}`))
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer mockDrive.Close()

	h := newTestHarness(t, false)
	h.ctrl.driveAPIBaseURL = mockDrive.URL
	h.ctrl.driveHTTPClient = mockDrive.Client()
	h.auth.driveScope = true
	h.auth.requiresQuota = false
	_ = h.vault.Set("colab", map[string]any{"token": "valid-tok", "refresh_token": "rt-2"})
	_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-edge-user", "email": "edge@example.com"})

	if _, err := h.ctrl.Command(context.Background(), "comfyui.outputs.drive.sync", nil); err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	snap := h.ctrl.Snapshot()
	outputs := asMapSlice(snap["comfy_outputs"])
	if len(outputs) != 1 {
		t.Fatalf("expected 1 synced Drive output, got %#v", outputs)
	}
	item := outputs[0]
	itemID := asString(item["id"])
	// Untrusted webViewLink must fall back to canonical drive.google.com URL.
	if gotLink := asString(item["drive_web_view_link"]); gotLink != "https://drive.google.com/file/d/file-large-nosha/view" {
		t.Fatalf("expected sanitized Drive webViewLink fallback, got %q", gotLink)
	}
	// Invalid sha256 must NOT be stored or returned on output record.
	if _, hasSha := item["sha256"]; hasSha {
		t.Fatalf("expected invalid sha256 omitted from output metadata, got %#v", item["sha256"])
	}

	// Read multi-chunk payload across 200 OK Range-ignored fallback: chunk 1 + chunk 2 must match exact bytes!
	var reassembled []byte
	for offset := 0; offset < len(largePayload); {
		chunkRes, err := h.ctrl.Command(context.Background(), "comfyui.output.asset", map[string]any{
			"id":     itemID,
			"offset": offset,
		})
		if err != nil {
			t.Fatalf("chunk read at offset=%d failed: %v", offset, err)
		}
		cm := asMap(chunkRes)
		// Empty/invalid sha256 must be OMITTED from asset chunk so frontend outputBlobURL does not reject "".
		if _, hasSha := cm["sha256"]; hasSha {
			t.Fatalf("expected sha256 key omitted when unknown, got %#v", cm["sha256"])
		}
		rawChunk, err := base64.StdEncoding.DecodeString(asString(cm["data"]))
		if err != nil || len(rawChunk) == 0 {
			t.Fatalf("invalid chunk at offset=%d: len=%d err=%v", offset, len(rawChunk), err)
		}
		reassembled = append(reassembled, rawChunk...)
		offset += len(rawChunk)
	}
	if !bytes.Equal(reassembled, largePayload) {
		t.Fatalf("200 OK Range fallback reassembled bytes mismatch: got %d bytes, want %d", len(reassembled), len(largePayload))
	}

	// Offline delete MUST fail and preserve local index when Drive API returns 500.
	if _, err := h.ctrl.Command(context.Background(), "comfyui.output.delete", map[string]any{"id": itemID}); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("expected HTTP 500 delete error, got %v", err)
	}
	if stillThere := asMapSlice(h.ctrl.Snapshot()["comfy_outputs"]); len(stillThere) != 1 {
		t.Fatalf("expected local Drive record preserved after failed remote delete, got %#v", stillThere)
	}

	// Workspace-only record must NOT be resurrected when GPU is ready + Drive connected.
	wsOnlyID := "11112222333344445555666677778888"
	h.ctrl.mu.Lock()
	h.ctrl.driveOutputs["sub:sub-edge-user"][wsOnlyID] = &ColabOneDriveOutputRecord{
		ID:            wsOnlyID,
		WorkflowName:  "Deleted Elsewhere",
		Kind:          "image",
		Filename:      "gone.png",
		Extension:     "png",
		Mime:          "image/png",
		Size:          128,
		DriveSynced:   true,
		DriveExported: false,
	}
	h.ctrl.mu.Unlock()
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state": "ready",
			"id":    "ep-live",
			"drive": map[string]any{"connected": true, "mode": "mount"},
		},
		"comfy_outputs": []any{},
	})
	liveSnapOutputs := asMapSlice(h.ctrl.Snapshot()["comfy_outputs"])
	if len(liveSnapOutputs) != 1 || asString(liveSnapOutputs[0]["id"]) != itemID {
		t.Fatalf("expected only DriveExported item surfaced alongside connected GPU (not deleted workspace-only item), got %#v", liveSnapOutputs)
	}

	// Logout clears drive_outputs_status metadata in Snapshot().
	h.ctrl.mu.Lock()
	h.ctrl.driveOutputsSyncError = "stale error"
	h.ctrl.driveOutputsLastSynced = 12345.0
	h.ctrl.mu.Unlock()
	_ = h.vault.Delete("colab")
	loggedOutStatus := asMap(h.ctrl.Snapshot()["drive_outputs_status"])
	if asString(loggedOutStatus["error"]) != "" || asFloatZero(loggedOutStatus["last_synced_at"]) != 0 {
		t.Fatalf("expected zeroed drive_outputs_status after logout, got %#v", loggedOutStatus)
	}
}

func TestDriveOutputsCUAttributionCreatedTimeAndSortOrder(t *testing.T) {
	liveID := "11111111111111111111111111111111"
	archivedID := "22222222222222222222222222222222"
	nowAligned := (time.Now().Unix()/1800)*1800 - 3600
	base := float64(nowAligned)

	mockDrive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/files" {
			q := r.URL.Query().Get("q")
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.Contains(q, "name = 'ColabOne_Drive'"):
				_, _ = w.Write([]byte(`{"files":[{"id":"dir-root","name":"ColabOne_Drive"}]}`))
			case strings.Contains(q, "name = 'outputs'"):
				_, _ = w.Write([]byte(`{"files":[{"id":"dir-out","name":"outputs"}]}`))
			case strings.Contains(q, "'dir-out' in parents"):
				_ = json.NewEncoder(w).Encode(map[string]any{
					"files": []map[string]any{
						{
							"id":           "file-created-time",
							"name":         "archived_clip.mp4",
							"size":         "8192000",
							"createdTime":  "2026-11-24T08:00:00Z",
							"modifiedTime": "2026-11-25T10:00:00Z",
							"webViewLink":  "https://drive.google.com/file/d/file-created-time/view",
						},
					},
				})
			default:
				_, _ = w.Write([]byte(`{"files":[]}`))
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer mockDrive.Close()

	h := newTestHarness(t, false)
	h.ctrl.driveAPIBaseURL = mockDrive.URL
	h.ctrl.driveHTTPClient = mockDrive.Client()
	h.auth.driveScope = true
	h.auth.requiresQuota = false
	_ = h.vault.Set("colab", map[string]any{"token": "tok-cu", "refresh_token": "rt-cu"})
	_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-cu", "email": "cu@example.com"})

	// Record historical CU rate at 12.0 CU/hr covering [base, base+1800)
	h.ctrl.cuUsage.RecordSample("cu@example.com", base+60, 100.0, true, 12.0, 1)
	h.ctrl.cuUsage.RecordSample("cu@example.com", base+660, 98.0, true, 12.0, 1)

	// 1) SyncDriveOutputs prefers createdTime over modifiedTime!
	if _, err := h.ctrl.Command(context.Background(), "comfyui.outputs.drive.sync", nil); err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	syncedOut := asMapSlice(h.ctrl.Snapshot()["comfy_outputs"])
	if len(syncedOut) != 1 {
		t.Fatalf("expected 1 synced output, got %#v", syncedOut)
	}
	// 2026-11-24T08:00:00Z = 1795507200 (different from 2026-11-25T10:00:00Z = 1795600800)
	gotCreated := asFloatZero(syncedOut[0]["created_at"])
	if gotCreated < 1795507199 || gotCreated > 1795507201 {
		t.Fatalf("expected createdTime unix ~1795507200, got %.1f", gotCreated)
	}

	// 2) Live output without Drive connected STILL receives CU attribution and newest-first sort!
	h.ctrl.mu.Lock()
	h.ctrl.driveOutputs["sub:sub-cu"] = map[string]*ColabOneDriveOutputRecord{
		archivedID: {
			ID:            archivedID,
			WorkflowName:  "归档视频",
			Kind:          "video",
			Filename:      "older_archived.mp4",
			Extension:     "mp4",
			Mime:          "video/mp4",
			Size:          10_000_000,
			CreatedAt:     base + 900, // newer than liveID below to verify merged sorting!
			Elapsed:       300,
			DriveSynced:   true,
			DriveExported: true,
			DriveFileID:   "file-arch",
		},
	}
	h.ctrl.mu.Unlock()

	h.ctrl.Update(map[string]any{
		"account": map[string]any{
			"logged_in":   true,
			"email":       "cu@example.com",
			"rate":        12.0,
			"assignments": 1,
		},
		"runtime": map[string]any{
			"state": "ready",
			"id":    "ep-cu",
			"gpu":   map[string]any{"name": "NVIDIA A100-SXM4-40GB"},
			"drive": map[string]any{"connected": false, "mode": "disabled"},
		},
		"comfy_outputs": []any{
			map[string]any{
				"id":            liveID,
				"workflow_name": "口播数字人",
				"kind":          "video",
				"filename":      "live_clip.mp4",
				"extension":     "mp4",
				"mime":          "video/mp4",
				"size":          64_000_000,
				"created_at":    base + 600,
				"elapsed":       600.0, // 10 min @ 12 CU/hr = 2.00 CU
			},
		},
	})

	snap := h.ctrl.Snapshot()
	outputs := asMapSlice(snap["comfy_outputs"])
	if len(outputs) != 2 {
		t.Fatalf("expected 2 merged outputs, got %#v", outputs)
	}
	// Strictly sorted newest-first by created_at: archivedID (base+900) before liveID (base+600)!
	if asString(outputs[0]["id"]) != archivedID || asString(outputs[1]["id"]) != liveID {
		t.Fatalf("expected newest-first order [%s, %s], got [%s, %s]", archivedID, liveID, asString(outputs[0]["id"]), asString(outputs[1]["id"]))
	}
	// Archived item backfilled CU from cuUsage bucket: 300s @ 12 CU/hr = 1.00 CU
	if gotCU := asFloatZero(outputs[0]["cu_consumed"]); gotCU < 0.99 || gotCU > 1.01 {
		t.Fatalf("expected archived output cu_consumed ~1.00, got %.4f (%#v)", gotCU, outputs[0])
	}
	// Live item got CU (2.00 CU), rate (12.0), and GPU name!
	if gotCU := asFloatZero(outputs[1]["cu_consumed"]); gotCU < 1.99 || gotCU > 2.01 {
		t.Fatalf("expected live output cu_consumed ~2.00, got %.4f (%#v)", gotCU, outputs[1])
	}
	if asString(outputs[1]["gpu_name"]) != "NVIDIA A100-SXM4-40GB" {
		t.Fatalf("expected gpu_name on live output, got %q", asString(outputs[1]["gpu_name"]))
	}
}

func TestResolveOutputCUWorkspaceRestoredOldOutputDoesNotInheritCurrentSession(t *testing.T) {
	nowAligned := (time.Now().Unix()/1800)*1800 - 1800
	now := float64(nowAligned) + 900.0
	oldNoHistoryID := "33333333333333333333333333333333"
	oldWithHistoryID := "44444444444444444444444444444444"
	currentSessionID := "55555555555555555555555555555555"

	h := newTestHarness(t, false)
	_ = h.vault.Set("colab", map[string]any{"token": "tok-sess", "refresh_token": "rt-sess"})
	_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-sess", "email": "sess@example.com"})

	// Historical T4 session 4 hours ago at 2.0 CU/hr
	histBase := float64(nowAligned - 4*3600)
	h.ctrl.cuUsage.RecordSample("sess@example.com", histBase+60, 200.0, true, 2.0, 1)
	h.ctrl.cuUsage.RecordSample("sess@example.com", histBase+660, 199.67, true, 2.0, 1)
	// Stop T4 session so a new H100 run starts later
	h.ctrl.cuUsage.RecordSample("sess@example.com", histBase+720, 199.64, true, 0.0, 0)

	// New active H100 session started at now - 600 at 15.0 CU/hr
	h.ctrl.cuUsage.RecordSample("sess@example.com", now-600, 199.64, true, 15.0, 1)

	h.ctrl.Update(map[string]any{
		"account": map[string]any{
			"logged_in":   true,
			"email":       "sess@example.com",
			"rate":        15.0,
			"assignments": 1,
		},
		"runtime": map[string]any{
			"state": "ready",
			"id":    "ep-h100",
			"gpu":   map[string]any{"name": "NVIDIA H100 80GB"},
			"drive": map[string]any{"connected": true, "mode": "mount"},
		},
		"comfy_outputs": []any{
			map[string]any{
				"id":         oldNoHistoryID,
				"kind":       "video",
				"filename":   "yesterday.mp4",
				"extension":  "mp4",
				"mime":       "video/mp4",
				"size":       1024,
				"created_at": now - 86400, // 24 hours ago, outside active run & no bucket
				"elapsed":    600.0,
			},
			map[string]any{
				"id":         oldWithHistoryID,
				"kind":       "video",
				"filename":   "earlier_t4.mp4",
				"extension":  "mp4",
				"mime":       "video/mp4",
				"size":       1024,
				"created_at": histBase + 660, // during 2.0 CU/hr T4 bucket
				"elapsed":    600.0,
			},
			map[string]any{
				"id":         currentSessionID,
				"kind":       "video",
				"filename":   "current_h100.mp4",
				"extension":  "mp4",
				"mime":       "video/mp4",
				"size":       1024,
				"created_at": now - 60, // inside current H100 run
				"elapsed":    300.0,    // 5 min @ 15.0 CU/hr = 1.25 CU
			},
		},
	})

	byID := map[string]map[string]any{}
	for _, m := range asMapSlice(h.ctrl.Snapshot()["comfy_outputs"]) {
		byID[asString(m["id"])] = m
	}

	// 1) Yesterday's restored output MUST NOT inherit today's H100 GPU or 15.0 CU/hr rate!
	if got := asFloatZero(byID[oldNoHistoryID]["cu_consumed"]); got != 0 {
		t.Fatalf("old output without bucket must not inherit live rate, got cu_consumed=%.4f (%#v)", got, byID[oldNoHistoryID])
	}
	if gn := asString(byID[oldNoHistoryID]["gpu_name"]); gn != "" {
		t.Fatalf("old output without bucket must not inherit today's GPU name, got %q", gn)
	}

	// 2) Earlier T4 output backfills historical 2.0 CU/hr (~0.3333 CU) WITHOUT today's H100 GPU name!
	if got := asFloatZero(byID[oldWithHistoryID]["cu_consumed"]); got < 0.32 || got > 0.35 {
		t.Fatalf("old output with T4 bucket expected ~0.3333 CU, got %.4f (%#v)", got, byID[oldWithHistoryID])
	}
	if gn := asString(byID[oldWithHistoryID]["gpu_name"]); gn != "" {
		t.Fatalf("old output restored from earlier session must not stamp today's H100 GPU name, got %q", gn)
	}

	// 3) Current session output gets exact H100 GPU name and 1.25 CU!
	if got := asFloatZero(byID[currentSessionID]["cu_consumed"]); got < 1.24 || got > 1.26 {
		t.Fatalf("current session output expected ~1.25 CU, got %.4f (%#v)", got, byID[currentSessionID])
	}
	if gn := asString(byID[currentSessionID]["gpu_name"]); gn != "NVIDIA H100 80GB" {
		t.Fatalf("current session output expected NVIDIA H100 80GB, got %q", gn)
	}

	// 4) Rapid GPU switch: runtime.started_at is authoritative even when an earlier output
	//    is only 10 minutes old (inside a recent cuUsage run) and must not inherit the new GPU name.
	rapidOldID := "56565656565656565656565656565656"
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state":      "ready",
			"id":         "ep-h100-new",
			"started_at": now - 120,
			"gpu":        map[string]any{"name": "NVIDIA H100 80GB"},
			"drive":      map[string]any{"connected": true, "mode": "mount"},
		},
		"comfy_outputs": []any{
			map[string]any{
				"id":         rapidOldID,
				"kind":       "video",
				"filename":   "prev_vm.mp4",
				"extension":  "mp4",
				"mime":       "video/mp4",
				"size":       1024,
				"created_at": now - 540, // 7 minutes before new VM booted
				"elapsed":    180.0,
			},
		},
	})
	rapidSnap := asMapSlice(h.ctrl.Snapshot()["comfy_outputs"])
	if len(rapidSnap) != 1 || asString(rapidSnap[0]["gpu_name"]) != "" {
		t.Fatalf("output created before runtime.started_at must not inherit new GPU name, got %#v", rapidSnap)
	}
}

func TestSyncDriveOutputsPreservesExportedRecordIDMetadataAndMergeConsistency(t *testing.T) {
	realID := "66666666666666666666666666666666"
	var (
		mu        sync.Mutex
		syncCalls int
	)

	mockDrive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/files" {
			q := r.URL.Query().Get("q")
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.Contains(q, "name = 'ColabOne_Drive'"):
				mu.Lock()
				syncCalls++
				mu.Unlock()
				_, _ = w.Write([]byte(`{"files":[{"id":"dir-root","name":"ColabOne_Drive"}]}`))
			case strings.Contains(q, "name = 'outputs'"):
				_, _ = w.Write([]byte(`{"files":[{"id":"dir-out","name":"outputs"}]}`))
			case strings.Contains(q, "'dir-out' in parents"):
				_ = json.NewEncoder(w).Encode(map[string]any{
					"files": []map[string]any{
						{
							"id":           "file-exported-only-1",
							"name":         "avatar_export.mp4",
							"size":         "12345678",
							"createdTime":  "2026-11-26T00:05:00Z",
							"modifiedTime": "2026-11-26T00:05:00Z",
							"webViewLink":  "https://drive.google.com/file/d/file-exported-only-1/view",
						},
					},
				})
			default:
				_, _ = w.Write([]byte(`{"files":[]}`))
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer mockDrive.Close()

	h := newTestHarness(t, false)
	h.ctrl.driveAPIBaseURL = mockDrive.URL
	h.ctrl.driveHTTPClient = mockDrive.Client()
	h.auth.driveScope = true
	h.auth.requiresQuota = false
	_ = h.vault.Set("colab", map[string]any{"token": "tok-exp", "refresh_token": "rt-exp"})
	_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-exp", "email": "exp@example.com"})

	// 1) mergeDriveOutputRecord updates CURate when upgrading from estimated -> exact and never overwrites GPUName.
	dst := &ColabOneDriveOutputRecord{
		ID:          realID,
		CUConsumed:  1.50,
		CURate:      9.0,
		CUEstimated: true,
		GPUName:     "NVIDIA T4",
	}
	src := &ColabOneDriveOutputRecord{
		ID:          realID,
		CUConsumed:  2.00,
		CURate:      12.0,
		CUEstimated: false,
		GPUName:     "NVIDIA H100 80GB",
	}
	if !mergeDriveOutputRecord(dst, src) {
		t.Fatal("expected mergeDriveOutputRecord to report changed")
	}
	if dst.CUConsumed != 2.00 || dst.CURate != 12.0 || dst.CUEstimated {
		t.Fatalf("expected upgraded exact CU=2.00 and rate=12.0, got %+v", dst)
	}
	if dst.GPUName != "NVIDIA T4" {
		t.Fatalf("expected existing GPUName preserved, got %q", dst.GPUName)
	}

	// 2) Exported-only file (no workspace/comfyui-outputs copy) keeps its realID, CU, workflow name,
	//    elapsed, and original CreatedAt across SyncDriveOutputs without creating duplicate cards!
	origCreated := float64(time.Now().Unix() - 120)
	h.ctrl.Update(map[string]any{
		"account": map[string]any{
			"logged_in":   true,
			"email":       "exp@example.com",
			"rate":        12.0,
			"assignments": 1,
		},
		"runtime": map[string]any{
			"state": "ready",
			"id":    "ep-exp",
			"gpu":   map[string]any{"name": "NVIDIA A100-SXM4-40GB"},
			"drive": map[string]any{"connected": false, "mode": "disabled"},
		},
		"comfy_outputs": []any{
			map[string]any{
				"id":            realID,
				"workflow_id":   "digital-human-longcat",
				"workflow_name": "口播数字人",
				"kind":          "video",
				"filename":      "avatar_export.mp4",
				"extension":     "mp4",
				"mime":          "video/mp4",
				"size":          12345678,
				"created_at":    origCreated,
				"elapsed":       300.0,
			},
		},
	})
	// Simulate export_drive completing before drive_file_id was known (e.g. mount mode)
	h.ctrl.recordExportedDriveOutput(realID, map[string]any{
		"filename":      "avatar_export.mp4",
		"remote_folder": "ColabOne_Drive/outputs",
		"drive_path":    "My Drive/ColabOne_Drive/outputs/avatar_export.mp4",
		"size":          12345678,
	})

	if _, err := h.ctrl.Command(context.Background(), "comfyui.outputs.drive.sync", nil); err != nil {
		t.Fatalf("SyncDriveOutputs failed: %v", err)
	}

	// While GPU is still ready, there must be EXACTLY 1 output card (no duplicate synthetic card!),
	// enriched with drive_file_id and original CU/workflow metadata.
	liveOutputs := asMapSlice(h.ctrl.Snapshot()["comfy_outputs"])
	if len(liveOutputs) != 1 {
		t.Fatalf("expected 1 deduplicated output card while GPU ready, got %d: %#v", len(liveOutputs), liveOutputs)
	}
	if asString(liveOutputs[0]["id"]) != realID ||
		asString(liveOutputs[0]["drive_file_id"]) != "file-exported-only-1" ||
		asString(liveOutputs[0]["workflow_name"]) != "口播数字人" ||
		asFloatZero(liveOutputs[0]["cu_consumed"]) < 0.99 {
		t.Fatalf("expected realID + drive_file_id + preserved metadata, got %#v", liveOutputs[0])
	}

	// After GPU stops, the single Drive card still has original workflow_name, created_at, CU, GPU, and drive_file_id!
	h.ctrl.Update(map[string]any{
		"runtime":       map[string]any{"state": "stopped"},
		"comfy_outputs": []any{},
	})
	offlineOutputs := asMapSlice(h.ctrl.Snapshot()["comfy_outputs"])
	if len(offlineOutputs) != 1 {
		t.Fatalf("expected 1 offline Drive output card, got %d: %#v", len(offlineOutputs), offlineOutputs)
	}
	if asString(offlineOutputs[0]["id"]) != realID ||
		asString(offlineOutputs[0]["workflow_name"]) != "口播数字人" ||
		asString(offlineOutputs[0]["gpu_name"]) != "NVIDIA A100-SXM4-40GB" ||
		asFloatZero(offlineOutputs[0]["cu_consumed"]) < 0.99 ||
		asFloatZero(offlineOutputs[0]["created_at"]) != origCreated {
		t.Fatalf("offline Drive card lost metadata after SyncDriveOutputs: %#v", offlineOutputs[0])
	}

	// 3) readDriveOutputAssetChunk skips on-demand sync for colab_drivefs records and obeys cooldown.
	dfsID := "77777777777777777777777777777777"
	h.ctrl.mu.Lock()
	h.ctrl.driveOutputs["sub:sub-exp"][dfsID] = &ColabOneDriveOutputRecord{
		ID:          dfsID,
		Kind:        "image",
		Filename:    "dfs.png",
		Extension:   "png",
		Mime:        "image/png",
		Size:        1024,
		DriveSynced: true,
		DriveMode:   "colab_drivefs",
	}
	h.ctrl.driveOutputsLastSynced = 0
	h.ctrl.mu.Unlock()
	mu.Lock()
	beforeSyncs := syncCalls
	mu.Unlock()
	if _, err := h.ctrl.readDriveOutputAssetChunk(context.Background(), dfsID, 0); err == nil {
		t.Fatal("expected error for colab_drivefs record without file ID")
	}
	mu.Lock()
	afterSyncs := syncCalls
	mu.Unlock()
	if afterSyncs != beforeSyncs {
		t.Fatalf("expected colab_drivefs readDriveOutputAssetChunk not to trigger SyncDriveOutputs, calls %d -> %d", beforeSyncs, afterSyncs)
	}
}

func asFloatZero(v any) float64 {
	f, _ := asFloat(v)
	return f
}

func TestDriveOutputsCanonicalMIMECorruptBackupRedirectRefusalAndSyncRaces(t *testing.T) {
	// 1. driveOutputRecordFromMap ignores untrusted kind/mime and derives canonically from ext.
	rec := driveOutputRecordFromMap(map[string]any{
		"id":        "aaaa1111bbbb2222cccc3333dddd4444",
		"filename":  "render.png",
		"extension": "png",
		"kind":      "executable",
		"mime":      "text/html",
		"size":      1024,
	})
	if rec == nil || rec.Kind != "image" || rec.Mime != "image/png" {
		t.Fatalf("expected canonical image/image/png, got %+v", rec)
	}

	// 2. loadDriveOutputs forces canonical kind/mime and backs up corrupt JSON to .corrupt-<ts>.
	h := newTestHarness(t, false)
	validID := "bbbb1111cccc2222dddd3333eeee4444"
	spoofedDisk := map[string]any{
		"version": 1,
		"accounts": map[string]any{
			"sub:sub-load": map[string]any{
				validID: map[string]any{
					"id":        validID,
					"filename":  "clip.mp4",
					"extension": "mp4",
					"kind":      "html",
					"mime":      "text/html",
					"size":      2048,
				},
			},
		},
	}
	rawValid, _ := json.Marshal(spoofedDisk)
	if err := os.WriteFile(h.ctrl.driveOutputsPath(), rawValid, 0o600); err != nil {
		t.Fatal(err)
	}
	h.ctrl.loadDriveOutputs()
	h.ctrl.mu.Lock()
	loadedRec := h.ctrl.driveOutputs["sub:sub-load"][validID]
	h.ctrl.mu.Unlock()
	if loadedRec == nil || loadedRec.Kind != "video" || loadedRec.Mime != "video/mp4" {
		t.Fatalf("loadDriveOutputs did not canonicalize kind/mime: %+v", loadedRec)
	}

	if err := os.WriteFile(h.ctrl.driveOutputsPath(), []byte("{corrupt-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.ctrl.loadDriveOutputs()
	matches, err := filepath.Glob(h.ctrl.driveOutputsPath() + ".corrupt-*")
	if err != nil || len(matches) == 0 {
		t.Fatalf("expected corrupt drive_outputs.json backup .corrupt-<ts>, matches=%v err=%v", matches, err)
	}

	// 3. driveHTTPClient refuses HTTP 302 redirects without following them.
	redirectFollowed := false
	redirTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectFollowed = true
		w.WriteHeader(http.StatusOK)
	}))
	defer redirTarget.Close()
	redirDrive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirTarget.URL+"/leak", http.StatusFound)
	}))
	defer redirDrive.Close()

	hRedir := newTestHarness(t, false)
	hRedir.ctrl.driveAPIBaseURL = redirDrive.URL
	hRedir.auth.driveScope = true
	hRedir.auth.requiresQuota = false
	_ = hRedir.vault.Set("colab", map[string]any{"token": "tok-redir", "refresh_token": "rt-redir"})
	_ = hRedir.vault.Set("google_profile", map[string]any{"sub": "sub-redir", "email": "redir@example.com"})
	if _, err := hRedir.ctrl.SyncDriveOutputs(context.Background()); err == nil {
		t.Fatal("expected SyncDriveOutputs to fail on 302 redirect")
	}
	if redirectFollowed {
		t.Fatal("driveHTTPClient followed a 302 redirect")
	}

	// 4. SyncDriveOutputs concurrent delete (no resurrection), concurrent add (merged),
	//    account-scoped syncing status, and semaphore ctx.Done() cancellation.
	deletedID := "11111111222222223333333344444444"
	keptID := "22222222333333334444444455555555"
	addedID := "33333333444444445555555566666666"

	inListCh := make(chan struct{})
	releaseListCh := make(chan struct{})
	var closeOnce sync.Once
	raceDrive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(q, "name = 'ColabOne_Drive'"):
			_, _ = w.Write([]byte(`{"files":[{"id":"dir-root","name":"ColabOne_Drive"}]}`))
		case strings.Contains(q, "name = 'workspace'"):
			_, _ = w.Write([]byte(`{"files":[{"id":"dir-ws","name":"workspace"}]}`))
		case strings.Contains(q, "name = 'comfyui-outputs'"):
			_, _ = w.Write([]byte(`{"files":[{"id":"dir-co","name":"comfyui-outputs"}]}`))
		case strings.Contains(q, "'dir-co' in parents"):
			closeOnce.Do(func() { close(inListCh) })
			<-releaseListCh
			_ = json.NewEncoder(w).Encode(map[string]any{
				"files": []map[string]any{
					{"id": "fid-del", "name": deletedID + ".png", "size": "100"},
					{"id": "fid-keep", "name": keptID + ".png", "size": "200"},
				},
			})
		default:
			_, _ = w.Write([]byte(`{"files":[]}`))
		}
	}))
	defer raceDrive.Close()

	hRace := newTestHarness(t, false)
	hRace.ctrl.driveAPIBaseURL = raceDrive.URL
	hRace.auth.driveScope = true
	hRace.auth.requiresQuota = false
	_ = hRace.vault.Set("colab", map[string]any{"token": "tok-a", "refresh_token": "rt-a"})
	_ = hRace.vault.Set("google_profile", map[string]any{"sub": "sub-acct-a", "email": "a@example.com"})
	hRace.ctrl.mu.Lock()
	hRace.ctrl.driveOutputs["sub:sub-acct-a"] = map[string]*ColabOneDriveOutputRecord{
		deletedID: {
			ID:                   deletedID,
			Kind:                 "image",
			Filename:             deletedID + ".png",
			Extension:            "png",
			Mime:                 "image/png",
			Size:                 100,
			DriveSynced:          true,
			DriveWorkspaceFileID: "fid-del",
		},
	}
	hRace.ctrl.mu.Unlock()

	syncDone := make(chan error, 1)
	go func() {
		_, err := hRace.ctrl.SyncDriveOutputs(context.Background())
		syncDone <- err
	}()

	<-inListCh

	// While sync is in flight for sub:sub-acct-a:
	// (a) Snapshot syncing is true for acct-a.
	_, statusA := hRace.ctrl.mergeComfyOutputsForSnapshot(map[string]any{"state": "stopped"}, nil, true)
	if !asBool(statusA["syncing"]) {
		t.Fatalf("expected syncing=true for active syncing account A, got %#v", statusA)
	}
	// (b) Another SyncDriveOutputs waiting on the semaphore must respect ctx.Done().
	ctxCancel, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := hRace.ctrl.SyncDriveOutputs(ctxCancel); err == nil {
		t.Fatal("expected blocked SyncDriveOutputs to fail when ctx cancels")
	}
	// (c) Account-scoped syncing status: temporarily inspect as another account -> false.
	_ = hRace.vault.Set("google_profile", map[string]any{"sub": "sub-acct-b", "email": "b@example.com"})
	_, statusB := hRace.ctrl.mergeComfyOutputsForSnapshot(map[string]any{"state": "stopped"}, nil, true)
	if asBool(statusB["syncing"]) {
		t.Fatalf("expected syncing=false for different account B while A is syncing, got %#v", statusB)
	}
	_ = hRace.vault.Set("google_profile", map[string]any{"sub": "sub-acct-a", "email": "a@example.com"})

	// (d) Concurrent delete of deletedID and concurrent addition of addedID while sync is in flight.
	hRace.ctrl.removeDriveOutputRecord(deletedID)
	hRace.ctrl.recordExportedDriveOutput(addedID, map[string]any{
		"filename":      "fresh_export.png",
		"size":          333,
		"drive_file_id": "fid-added",
	})

	close(releaseListCh)
	if err := <-syncDone; err != nil {
		t.Fatalf("SyncDriveOutputs failed: %v", err)
	}

	hRace.ctrl.mu.Lock()
	finalBucket := hRace.ctrl.driveOutputs["sub:sub-acct-a"]
	hRace.ctrl.mu.Unlock()
	if _, resurrected := finalBucket[deletedID]; resurrected {
		t.Fatalf("deleted output %s was resurrected by in-flight SyncDriveOutputs", deletedID)
	}
	if finalBucket[keptID] == nil {
		t.Fatalf("expected discovered output %s to remain present", keptID)
	}
	if finalBucket[addedID] == nil || finalBucket[addedID].DriveFileID != "fid-added" {
		t.Fatalf("expected concurrently added output %s to be merged into final bucket, got %+v", addedID, finalBucket[addedID])
	}
}

func TestDriveOutputsTaskIDSizePersistNegativeClampAndOnlineDeleteTrash(t *testing.T) {
	// 1. TaskID extraction (capped to 120 runes), merge, emit in toOutputMap, and negative Size clamping.
	longTask := strings.Repeat("任", 150)
	rec := driveOutputRecordFromMap(map[string]any{
		"id":        "aaaa1111bbbb2222cccc3333dddd4444",
		"filename":  "clip.mp4",
		"extension": "mp4",
		"size":      -500,
		"task_id":   longTask,
	})
	if rec == nil {
		t.Fatal("expected valid drive output record")
	}
	if rec.Size != 0 {
		t.Fatalf("negative size must clamp to 0, got %d", rec.Size)
	}
	if len([]rune(rec.TaskID)) != 120 {
		t.Fatalf("expected task_id capped to 120 runes, got %d", len([]rune(rec.TaskID)))
	}
	outMap := rec.toOutputMap("drive", true)
	if asString(outMap["task_id"]) != rec.TaskID {
		t.Fatalf("toOutputMap missing task_id: %#v", outMap)
	}
	dst := &ColabOneDriveOutputRecord{ID: rec.ID, Extension: "mp4"}
	if !mergeDriveOutputRecord(dst, rec) || dst.TaskID != rec.TaskID {
		t.Fatalf("mergeDriveOutputRecord did not merge TaskID: %+v", dst)
	}

	// 2. loadDriveOutputs clamps negative Size < 0 to 0 and preserves TaskID.
	h := newTestHarness(t, false)
	oid := "dddd1111eeee2222ffff3333aaaa4444"
	diskPayload := map[string]any{
		"version": 1,
		"accounts": map[string]any{
			"sub:sub-task": map[string]any{
				oid: map[string]any{
					"id":        oid,
					"filename":  "a.mp4",
					"extension": "mp4",
					"size":      -42,
					"task_id":   "task-persisted-1",
				},
			},
		},
	}
	raw, _ := json.Marshal(diskPayload)
	if err := os.WriteFile(h.ctrl.driveOutputsPath(), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	h.ctrl.loadDriveOutputs()
	h.ctrl.mu.Lock()
	loaded := h.ctrl.driveOutputs["sub:sub-task"][oid]
	h.ctrl.mu.Unlock()
	if loaded == nil || loaded.Size != 0 || loaded.TaskID != "task-persisted-1" {
		t.Fatalf("loadDriveOutputs expected Size=0 and TaskID preserved, got %+v", loaded)
	}

	// 3. readDriveOutputAssetChunk persists resolved Content-Range total size back into c.driveOutputs
	//    when cached rec.Size <= 0, enabling subsequent chunk reads at offset > 0!
	fullBytes := bytes.Repeat([]byte("A"), colabOneDriveAssetChunkBytes+128)
	var trashMu sync.Mutex
	var trashedIDs []string
	trashFail := false
	driveSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/files/") {
			fid := strings.TrimPrefix(r.URL.Path, "/files/")
			trashMu.Lock()
			fail := trashFail
			if !fail {
				trashedIDs = append(trashedIDs, fid)
			}
			trashMu.Unlock()
			if fail {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"` + fid + `","trashed":true}`))
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/files/fid-zero-size") {
			rng := r.Header.Get("Range")
			start := 0
			end := colabOneDriveAssetChunkBytes - 1
			_, _ = fmt.Sscanf(rng, "bytes=%d-%d", &start, &end)
			if end >= len(fullBytes) {
				end = len(fullBytes) - 1
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(fullBytes)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(fullBytes[start : end+1])
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer driveSrv.Close()

	h.ctrl.driveAPIBaseURL = driveSrv.URL
	h.auth.driveScope = true
	h.auth.requiresQuota = false
	_ = h.vault.Set("colab", map[string]any{"token": "tok-sz", "refresh_token": "rt-sz"})
	_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-task", "email": "task@example.com"})
	h.ctrl.mu.Lock()
	h.ctrl.driveOutputs["sub:sub-task"][oid] = &ColabOneDriveOutputRecord{
		ID:            oid,
		Kind:          "video",
		Filename:      "a.mp4",
		Extension:     "mp4",
		Mime:          "video/mp4",
		Size:          0,
		DriveSynced:   true,
		DriveExported: true,
		DriveFileID:   "fid-zero-size",
	}
	h.ctrl.mu.Unlock()

	if _, err := h.ctrl.readDriveOutputAssetChunk(context.Background(), oid, 0); err != nil {
		t.Fatalf("chunk 0 failed: %v", err)
	}
	cachedAfter := h.ctrl.driveOutputRecordCopy(oid)
	if cachedAfter == nil || cachedAfter.Size != int64(len(fullBytes)) {
		t.Fatalf("expected resolved size %d persisted back to driveOutputs, got %+v", len(fullBytes), cachedAfter)
	}
	chunk1, err := h.ctrl.readDriveOutputAssetChunk(context.Background(), oid, colabOneDriveAssetChunkBytes)
	if err != nil {
		t.Fatalf("chunk 1 after size persistence failed: %v", err)
	}
	decoded1, _ := base64.StdEncoding.DecodeString(asString(chunk1["data"]))
	if len(decoded1) != 128 {
		t.Fatalf("expected 128 bytes in chunk 1, got %d", len(decoded1))
	}

	// 4. Host online comfyui.output.delete trashes exported DriveFileID via Drive API v3;
	//    if Drive API fails, keeps the exported record (clearing workspace links) to avoid orphaned resurrection.
	seedTestSession(h.vault, "ep-del", "L4", true)
	h.ctrl.Update(map[string]any{
		"runtime":       map[string]any{"state": "ready", "id": "ep-del"},
		"comfy_outputs": []any{map[string]any{"id": oid, "extension": "mp4"}},
	})
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if method == "DELETE" && path == "comfyui/outputs/"+oid {
			return map[string]any{"ok": true}, nil
		}
		if path == "snapshot" {
			return map[string]any{
				"runtime":       map[string]any{"state": "ready", "id": "ep-del", "protocol": 7, "tasks": []any{}},
				"models":        []any{},
				"workflows":     []any{},
				"comfy_outputs": []any{},
			}, nil
		}
		return map[string]any{"ok": true}, nil
	}

	// 4a. Drive API trash failure keeps exported record with workspace links cleared.
	h.ctrl.mu.Lock()
	h.ctrl.driveOutputs["sub:sub-task"][oid].DriveWorkspaceFileID = "fid-ws-old"
	h.ctrl.mu.Unlock()
	trashMu.Lock()
	trashFail = true
	trashMu.Unlock()
	if _, err := h.ctrl.Command(context.Background(), "comfyui.output.delete", map[string]any{"id": oid}); err != nil {
		t.Fatalf("online delete expected nil err when remote deleted & Drive trash warned, got %v", err)
	}
	kept := h.ctrl.driveOutputRecordCopy(oid)
	if kept == nil || kept.DriveFileID != "fid-zero-size" || kept.DriveWorkspaceFileID != "" {
		t.Fatalf("expected exported Drive record preserved with workspace link cleared on trash failure, got %+v", kept)
	}

	// 4b. Drive API trash success trashes DriveFileID and removes local Drive record.
	h.ctrl.Update(map[string]any{
		"runtime":       map[string]any{"state": "ready", "id": "ep-del"},
		"comfy_outputs": []any{map[string]any{"id": oid, "extension": "mp4"}},
	})
	trashMu.Lock()
	trashFail = false
	trashedIDs = nil
	trashMu.Unlock()
	if _, err := h.ctrl.Command(context.Background(), "comfyui.output.delete", map[string]any{"id": oid}); err != nil {
		t.Fatalf("online delete with Drive trash failed: %v", err)
	}
	trashMu.Lock()
	gotTrashed := append([]string(nil), trashedIDs...)
	trashMu.Unlock()
	if len(gotTrashed) != 1 || gotTrashed[0] != "fid-zero-size" {
		t.Fatalf("expected DriveFileID fid-zero-size trashed during online delete, got %v", gotTrashed)
	}
	if h.ctrl.driveOutputRecordCopy(oid) != nil {
		t.Fatalf("expected local Drive record removed after successful online delete + Drive trash")
	}
}

func TestDriveOutputsSyncHardeningAndSchemaV2Backfill(t *testing.T) {
	oid1 := "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"
	oid2 := "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2"

	var metaFail atomic.Bool
	metaFail.Store(false)

	mockDrive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/files" {
			q := r.URL.Query().Get("q")
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.Contains(q, "name = 'ColabOne_Drive'"):
				_, _ = w.Write([]byte(`{"files":[{"id":"dir-root","name":"ColabOne_Drive"}]}`))
			case strings.Contains(q, "name = 'workspace'"):
				_, _ = w.Write([]byte(`{"files":[{"id":"dir-ws","name":"workspace"}]}`))
			case strings.Contains(q, "name = 'comfyui-outputs'"):
				_, _ = w.Write([]byte(`{"files":[{"id":"dir-co","name":"comfyui-outputs"}]}`))
			case strings.Contains(q, "name = 'outputs'"):
				_, _ = w.Write([]byte(`{"files":[{"id":"dir-out","name":"outputs"}]}`))
			case strings.Contains(q, "'dir-co' in parents"):
				_ = json.NewEncoder(w).Encode(map[string]any{
					"files": []map[string]any{
						{"id": "meta-1", "name": oid1 + ".json", "size": "120", "modifiedTime": "2026-11-26T00:00:00Z"},
						{"id": "asset-1", "name": oid1 + ".png", "size": "50", "modifiedTime": "2026-11-26T00:00:00Z"},
						{"id": "meta-2", "name": oid2 + ".json", "size": "120", "modifiedTime": "2026-11-26T00:00:00Z"},
						{"id": "asset-2", "name": oid2 + ".png", "size": "60", "modifiedTime": "2026-11-26T00:00:00Z"},
					},
				})
			case strings.Contains(q, "'dir-out' in parents"):
				_, _ = w.Write([]byte(`{"files":[]}`))
			default:
				_, _ = w.Write([]byte(`{"files":[]}`))
			}
			return
		}
		if r.URL.Path == "/files/meta-1" && r.URL.Query().Get("alt") == "media" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":            oid1,
				"workflow_id":   "wf-v2",
				"workflow_name": "V2 工作流",
				"task_id":       "task-backfilled-v2",
				"filename":      "v2_art.png",
				"extension":     "png",
				"kind":          "image",
				"mime":          "image/png",
				"size":          50,
			})
			return
		}
		if r.URL.Path == "/files/meta-2" && r.URL.Query().Get("alt") == "media" {
			if metaFail.Load() {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("meta error"))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":            oid2,
				"workflow_id":   "wf-2",
				"workflow_name": "Recovered WF",
				"task_id":       "task-2",
				"filename":      "art2.png",
				"extension":     "png",
				"kind":          "image",
				"mime":          "image/png",
				"size":          60,
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer mockDrive.Close()

	// Seed a schema v1 drive_outputs.json where oid1 already has DriveWorkspaceMetaID="meta-1" but empty TaskID.
	cfgDir := t.TempDir()
	v1Payload := map[string]any{
		"version": 1,
		"accounts": map[string]any{
			"sub:sub-v2": map[string]any{
				oid1: map[string]any{
					"id":                      oid1,
					"filename":                "old_v1.png",
					"extension":               "png",
					"kind":                    "image",
					"mime":                    "image/png",
					"size":                    50,
					"drive_synced":            true,
					"drive_workspace_file_id": "asset-1",
					"drive_workspace_meta_id": "meta-1",
				},
				oid2: map[string]any{
					"id":            oid2,
					"filename":      "preserved_name.png",
					"workflow_id":   "wf-prev",
					"workflow_name": "Preserved WF",
					"task_id":       "task-prev",
					"extension":     "png",
					"kind":          "image",
					"mime":          "image/png",
					"size":          60,
					"drive_synced":  true,
				},
			},
		},
	}
	rawV1, _ := json.Marshal(v1Payload)
	if err := os.WriteFile(filepath.Join(cfgDir, "drive_outputs.json"), rawV1, 0o600); err != nil {
		t.Fatal(err)
	}

	h := newTestHarnessWithDir(t, cfgDir, false)
	h.ctrl.driveAPIBaseURL = mockDrive.URL
	h.ctrl.driveHTTPClient = mockDrive.Client()
	h.auth.driveScope = true
	_ = h.vault.Set("colab", map[string]any{"token": "tok-v2"})
	_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-v2"})

	// Verify v1 migration cleared DriveWorkspaceMetaID on load when TaskID was empty.
	before := h.ctrl.driveOutputRecordCopy(oid1)
	if before == nil || before.DriveWorkspaceMetaID != "" {
		t.Fatalf("expected schema v1 load to clear DriveWorkspaceMetaID for TaskID backfill, got %+v", before)
	}

	// 1) Sync with meta-2 failing: oid1 backfills TaskID from meta-1; oid2 preserves prev metadata AND does NOT stamp DriveWorkspaceMetaID="meta-2"!
	metaFail.Store(true)
	if _, err := h.ctrl.SyncDriveOutputs(context.Background()); err != nil {
		t.Fatalf("SyncDriveOutputs failed: %v", err)
	}
	rec1 := h.ctrl.driveOutputRecordCopy(oid1)
	if rec1 == nil || rec1.TaskID != "task-backfilled-v2" || rec1.DriveWorkspaceMetaID != "meta-1" {
		t.Fatalf("expected oid1 TaskID backfilled & meta ID stamped, got %+v", rec1)
	}
	rec2 := h.ctrl.driveOutputRecordCopy(oid2)
	if rec2 == nil || rec2.Filename != "preserved_name.png" || rec2.WorkflowName != "Preserved WF" || rec2.TaskID != "task-prev" {
		t.Fatalf("expected oid2 to preserve previous metadata on meta fetch failure, got %+v", rec2)
	}
	if rec2.DriveWorkspaceMetaID != "" {
		t.Fatalf("failed metadata fetch must NOT stamp DriveWorkspaceMetaID, got %q", rec2.DriveWorkspaceMetaID)
	}

	// 2) Next sync with meta-2 healthy succeeds and stamps DriveWorkspaceMetaID="meta-2".
	metaFail.Store(false)
	if _, err := h.ctrl.SyncDriveOutputs(context.Background()); err != nil {
		t.Fatalf("second SyncDriveOutputs failed: %v", err)
	}
	rec2After := h.ctrl.driveOutputRecordCopy(oid2)
	if rec2After == nil || rec2After.DriveWorkspaceMetaID != "meta-2" || rec2After.Filename != "art2.png" || rec2After.TaskID != "task-2" {
		t.Fatalf("expected oid2 retried and updated on second sync, got %+v", rec2After)
	}
}

func TestDriveOutputsAuditHardening(t *testing.T) {
	// 1) A3 M2: Cancelled on-demand SyncDriveOutputs does NOT poison cooldown timestamp.
	t.Run("CooldownNotStampedOnCancel", func(t *testing.T) {
		oid := "d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1"
		h := newTestHarness(t, false)
		h.auth.driveScope = true
		_ = h.vault.Set("colab", map[string]any{"token": "tok-cd"})
		_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-cd"})
		h.ctrl.mu.Lock()
		h.ctrl.driveOutputs["sub:sub-cd"] = map[string]*ColabOneDriveOutputRecord{
			oid: {
				ID: oid, Kind: "image", Filename: "cd.png", Extension: "png", Mime: "image/png",
				Size: 100, DriveSynced: true, DriveMode: "drive_api",
			},
		}
		h.ctrl.mu.Unlock()
		cctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := h.ctrl.readDriveOutputAssetChunk(cctx, oid, 0); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
		h.ctrl.mu.Lock()
		attempt := h.ctrl.driveOutputsLastSyncAttempt
		h.ctrl.mu.Unlock()
		if attempt != 0 {
			t.Fatalf("expected cooldown NOT stamped on cancelled ctx, got %v", attempt)
		}
	})

	// 2) A3 M3: clearDriveOutputWorkspaceLinks while SyncDriveOutputs is in-flight prevents workspace resurrection while keeping DriveExported.
	t.Run("ClearWorkspaceLinksDuringInflightSync", func(t *testing.T) {
		oid := "d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2"
		syncMid := make(chan struct{}, 1)
		syncGate := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query().Get("q")
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.Contains(q, "'ColabOne_Drive'"):
				_, _ = w.Write([]byte(`{"files":[{"id":"root-dir","name":"ColabOne_Drive"}]}`))
			case strings.Contains(q, "'workspace'"):
				_, _ = w.Write([]byte(`{"files":[{"id":"ws-dir","name":"workspace"}]}`))
			case strings.Contains(q, "'comfyui-outputs'"):
				_, _ = w.Write([]byte(`{"files":[{"id":"co-dir","name":"comfyui-outputs"}]}`))
			case strings.Contains(q, "'co-dir' in parents"):
				_, _ = w.Write([]byte(`{"files":[{"id":"ws-file-1","name":"` + oid + `.png","size":"50"}]}`))
			case strings.Contains(q, "'outputs'"):
				select {
				case syncMid <- struct{}{}:
				default:
				}
				<-syncGate
				_, _ = w.Write([]byte(`{"files":[{"id":"out-dir","name":"outputs"}]}`))
			case strings.Contains(q, "'out-dir' in parents"):
				_, _ = w.Write([]byte(`{"files":[{"id":"exp-file-1","name":"` + oid + `.png","size":"50"}]}`))
			default:
				_, _ = w.Write([]byte(`{"files":[]}`))
			}
		}))
		defer srv.Close()

		h := newTestHarness(t, false)
		h.ctrl.driveAPIBaseURL = srv.URL
		h.ctrl.driveHTTPClient = srv.Client()
		h.auth.driveScope = true
		_ = h.vault.Set("colab", map[string]any{"token": "tok-ws"})
		_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-ws"})
		h.ctrl.mu.Lock()
		h.ctrl.driveOutputs["sub:sub-ws"] = map[string]*ColabOneDriveOutputRecord{
			oid: {
				ID: oid, Kind: "image", Filename: oid + ".png", Extension: "png", Mime: "image/png",
				Size: 50, DriveSynced: true, DriveExported: true, DriveFileID: "exp-file-1",
				DriveWorkspaceFileID: "ws-file-1", DriveWorkspaceMetaID: "ws-meta-1",
			},
		}
		h.ctrl.mu.Unlock()

		done := make(chan error, 1)
		go func() {
			_, err := h.ctrl.SyncDriveOutputs(context.Background())
			done <- err
		}()
		select {
		case <-syncMid:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for mid-sync")
		}
		h.ctrl.clearDriveOutputWorkspaceLinks(oid)
		close(syncGate)
		if err := <-done; err != nil {
			t.Fatalf("SyncDriveOutputs failed: %v", err)
		}
		rec := h.ctrl.driveOutputRecordCopy(oid)
		if rec == nil || !rec.DriveExported || rec.DriveFileID != "exp-file-1" {
			t.Fatalf("expected DriveExported & DriveFileID preserved, got %+v", rec)
		}
		if rec.DriveWorkspaceFileID != "" || rec.DriveWorkspaceMetaID != "" {
			t.Fatalf("expected cleared workspace links NOT resurrected by in-flight sync, got %+v", rec)
		}
	})

	// 3) A3 M4: deleteDriveOutputOffline resolves empty IDs via on-demand sync and errors honestly when !canDirectRead.
	t.Run("DeleteDriveOutputOfflineResolveAndDirectReadGuard", func(t *testing.T) {
		oid := "d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3"
		var trashed []string
		var mu sync.Mutex
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodPatch {
				mu.Lock()
				trashed = append(trashed, filepath.Base(r.URL.Path))
				mu.Unlock()
				_, _ = w.Write([]byte(`{"trashed":true}`))
				return
			}
			q := r.URL.Query().Get("q")
			switch {
			case strings.Contains(q, "'ColabOne_Drive'"):
				_, _ = w.Write([]byte(`{"files":[{"id":"root-dir","name":"ColabOne_Drive"}]}`))
			case strings.Contains(q, "'workspace'"):
				_, _ = w.Write([]byte(`{"files":[{"id":"ws-dir","name":"workspace"}]}`))
			case strings.Contains(q, "'comfyui-outputs'"):
				_, _ = w.Write([]byte(`{"files":[{"id":"co-dir","name":"comfyui-outputs"}]}`))
			case strings.Contains(q, "'co-dir' in parents"):
				_, _ = w.Write([]byte(`{"files":[{"id":"resolved-ws-fid","name":"` + oid + `.png","size":"40"}]}`))
			default:
				_, _ = w.Write([]byte(`{"files":[]}`))
			}
		}))
		defer srv.Close()

		h := newTestHarness(t, false)
		h.ctrl.driveAPIBaseURL = srv.URL
		h.ctrl.driveHTTPClient = srv.Client()
		h.auth.driveScope = true
		_ = h.vault.Set("colab", map[string]any{"token": "tok-del"})
		_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-del"})
		h.ctrl.mu.Lock()
		h.ctrl.driveOutputs["sub:sub-del"] = map[string]*ColabOneDriveOutputRecord{
			oid: {
				ID: oid, Kind: "image", Filename: oid + ".png", Extension: "png", Mime: "image/png",
				Size: 40, DriveSynced: true, DriveMode: "drive_api",
			},
		}
		h.ctrl.mu.Unlock()

		if _, err := h.ctrl.deleteDriveOutputOffline(context.Background(), oid); err != nil {
			t.Fatalf("deleteDriveOutputOffline with empty IDs failed: %v", err)
		}
		mu.Lock()
		gotTrashed := append([]string(nil), trashed...)
		mu.Unlock()
		if len(gotTrashed) != 1 || gotTrashed[0] != "resolved-ws-fid" {
			t.Fatalf("expected on-demand sync to resolve and trash resolved-ws-fid, got %v", gotTrashed)
		}

		// When !canDirectRead and Drive file IDs exist, offline delete still removes the local record
		// and returns a manual-cloud-cleanup warning so outputs are always deletable in the app.
		oid2 := "d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4"
		h.auth.requiresQuota = true
		_ = h.vault.Delete("cloud_project")
		h.ctrl.mu.Lock()
		h.ctrl.driveOutputs["sub:sub-del"][oid2] = &ColabOneDriveOutputRecord{
			ID: oid2, Kind: "image", Filename: "keep.png", Extension: "png", Mime: "image/png",
			Size: 40, DriveSynced: true, DriveFileID: "fid-locked",
		}
		h.ctrl.mu.Unlock()
		res2, err := h.ctrl.deleteDriveOutputOffline(context.Background(), oid2)
		if err != nil {
			t.Fatalf("expected local delete with manual cloud warning when !canDirectRead, got err=%v", err)
		}
		if !asBool(res2["ok"]) || !asBool(res2["local_only"]) || !asBool(res2["manual_cloud_cleanup"]) || asString(res2["warning_reason"]) != "drive_api_unavailable" {
			t.Fatalf("unexpected local-only delete response when !canDirectRead: %#v", res2)
		}
		if h.ctrl.driveOutputRecordCopy(oid2) != nil {
			t.Fatal("expected local Drive record removed when !canDirectRead")
		}
	})

	// 4) A3 M5: SyncDriveOutputs unblocks promptly on Close() via rootCtx.
	t.Run("SyncDriveOutputsCancelsOnRootCtxClose", func(t *testing.T) {
		h := newTestHarness(t, false)
		sem := h.ctrl.driveOutputsSyncSemaphore()
		sem <- struct{}{}
		defer func() { <-sem }()

		errCh := make(chan error, 1)
		go func() {
			_, err := h.ctrl.SyncDriveOutputs(context.Background())
			errCh <- err
		}()
		time.Sleep(20 * time.Millisecond)
		h.ctrl.Close()
		select {
		case err := <-errCh:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("expected context.Canceled on Close(), got %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("SyncDriveOutputs hung waiting on semaphore after Close()")
		}
	})
}

func TestDriveOutputsFinalHardeningOfflineDeleteAndWorkspaceLinks(t *testing.T) {
	// 1) Offline delete ALWAYS removes the local app record when cloud deletion isn't possible
	//    (!canDirectRead, colab_drivefs mount mode, or unresolved file IDs) and returns a warning
	//    telling the user to delete the cloud copy manually in Google Drive.
	t.Run("DeleteDriveOutputOfflineLocalFallbackWithCloudWarning", func(t *testing.T) {
		oidNoRead := "e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1"
		oidDriveFS := "e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2"
		oidUnsynced := "e3e3e3e3e3e3e3e3e3e3e3e3e3e3e3e3"

		var httpHits int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			httpHits++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"files":[]}`))
		}))
		defer srv.Close()

		h := newTestHarness(t, false)
		h.ctrl.driveAPIBaseURL = srv.URL
		h.ctrl.driveHTTPClient = srv.Client()
		_ = h.vault.Set("colab", map[string]any{"token": "tok-h1"})
		_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-h1"})

		// Case A: !canDirectRead with empty file IDs removes local record immediately without hitting Drive API.
		h.auth.driveScope = false
		h.ctrl.mu.Lock()
		h.ctrl.driveOutputs["sub:sub-h1"] = map[string]*ColabOneDriveOutputRecord{
			oidNoRead: {
				ID: oidNoRead, Kind: "image", Filename: "no-read.png", Extension: "png", Mime: "image/png",
				Size: 40, DriveSynced: true, DriveMode: "drive_api",
			},
		}
		h.ctrl.mu.Unlock()
		resNoRead, err := h.ctrl.deleteDriveOutputOffline(context.Background(), oidNoRead)
		if err != nil {
			t.Fatalf("expected local delete when !canDirectRead with empty IDs, got %v", err)
		}
		if !asBool(resNoRead["ok"]) || !asBool(resNoRead["local_only"]) || !asBool(resNoRead["manual_cloud_cleanup"]) || asString(resNoRead["warning_reason"]) != "drive_api_unavailable" {
			t.Fatalf("unexpected response when !canDirectRead: %#v", resNoRead)
		}
		if h.ctrl.driveOutputRecordCopy(oidNoRead) != nil {
			t.Fatal("expected local record removed when !canDirectRead with empty IDs")
		}
		if httpHits != 0 {
			t.Fatalf("expected zero Drive API calls when !canDirectRead, got %d", httpHits)
		}

		// Case B: canDirectRead == true, colab_drivefs with empty IDs removes local record cleanly
		// without hitting Drive API, returns mount-mode warning, and stays deleted across SyncDriveOutputs.
		h.auth.driveScope = true
		h.ctrl.mu.Lock()
		h.ctrl.driveOutputs["sub:sub-h1"][oidDriveFS] = &ColabOneDriveOutputRecord{
			ID: oidDriveFS, Kind: "video", Filename: "drivefs.mp4", Extension: "mp4", Mime: "video/mp4",
			Size: 4096, DriveSynced: true, DriveExported: true, DriveMode: "colab_drivefs",
			DrivePath: "My Drive/ColabOne_Drive/outputs/drivefs.mp4",
		}
		h.ctrl.mu.Unlock()
		resDriveFS, err := h.ctrl.deleteDriveOutputOffline(context.Background(), oidDriveFS)
		if err != nil {
			t.Fatalf("expected colab_drivefs offline delete to succeed locally with warning, got %v", err)
		}
		if !asBool(resDriveFS["ok"]) || !asBool(resDriveFS["local_only"]) || !asBool(resDriveFS["manual_cloud_cleanup"]) || asString(resDriveFS["warning_reason"]) != "colab_drivefs" {
			t.Fatalf("unexpected colab_drivefs delete response: %#v", resDriveFS)
		}
		if !strings.Contains(asString(resDriveFS["warning"]), "挂载模式") || !strings.Contains(asString(resDriveFS["warning"]), "手动删除") {
			t.Fatalf("expected mount-mode manual cloud cleanup warning, got %q", asString(resDriveFS["warning"]))
		}
		if h.ctrl.driveOutputRecordCopy(oidDriveFS) != nil {
			t.Fatal("expected colab_drivefs local record removed")
		}
		if httpHits != 0 {
			t.Fatalf("expected zero Drive API calls for colab_drivefs empty-ID delete, got %d", httpHits)
		}
		if _, err := h.ctrl.SyncDriveOutputs(context.Background()); err != nil {
			t.Fatalf("SyncDriveOutputs after colab_drivefs delete failed: %v", err)
		}
		if h.ctrl.driveOutputRecordCopy(oidDriveFS) != nil {
			t.Fatal("expected deleted colab_drivefs record NOT to resurrect after SyncDriveOutputs")
		}

		// Case C: SyncDriveOutputs completes yet leaves empty IDs -> removes local record with warning.
		h2 := newTestHarness(t, false)
		h2.ctrl.driveAPIBaseURL = srv.URL
		h2.ctrl.driveHTTPClient = srv.Client()
		h2.auth.driveScope = true
		_ = h2.vault.Set("colab", map[string]any{"token": "tok-h1-c"})
		_ = h2.vault.Set("google_profile", map[string]any{"sub": "sub-h1-c"})
		h2.ctrl.mu.Lock()
		h2.ctrl.driveOutputs["sub:sub-h1-c"] = map[string]*ColabOneDriveOutputRecord{
			oidUnsynced: {
				ID: oidUnsynced, Kind: "image", Filename: "unsynced.png", Extension: "png", Mime: "image/png",
				Size: 40, DriveSynced: true, DriveMode: "drive_api", DriveExported: true,
			},
		}
		h2.ctrl.mu.Unlock()
		srvEmpty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Inject addedDuringSync mid-sync so SyncDriveOutputs finishes with synced=true yet preserves oidUnsynced with empty IDs.
			h2.ctrl.mu.Lock()
			h2.ctrl.noteDriveOutputAddedLocked("sub:sub-h1-c", oidUnsynced)
			h2.ctrl.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"files":[]}`))
		}))
		defer srvEmpty.Close()
		h2.ctrl.driveAPIBaseURL = srvEmpty.URL
		h2.ctrl.driveHTTPClient = srvEmpty.Client()

		resUnsynced, err := h2.ctrl.deleteDriveOutputOffline(context.Background(), oidUnsynced)
		if err != nil {
			t.Fatalf("expected unresolved empty-ID delete to succeed locally with warning, got %v", err)
		}
		if !asBool(resUnsynced["ok"]) || !asBool(resUnsynced["local_only"]) || !asBool(resUnsynced["manual_cloud_cleanup"]) || asString(resUnsynced["warning_reason"]) != "remote_file_unconfirmed" {
			t.Fatalf("unexpected unresolved empty-ID delete response: %#v", resUnsynced)
		}
		if h2.ctrl.driveOutputRecordCopy(oidUnsynced) != nil {
			t.Fatal("expected local record removed when file IDs remain empty after sync")
		}

		// Case D: Permanent HTTP 403 on Drive API trash falls back to local removal with warning,
		// whereas transient HTTP 500 returns an error and keeps the local record for retry.
		oidForbidden := "e8e8e8e8e8e8e8e8e8e8e8e8e8e8e8e8"
		oidTransient := "e9e9e9e9e9e9e9e9e9e9e9e9e9e9e9e9"
		srvErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "fid-403") {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"code":403,"message":"insufficientFilePermissions"}}`))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"code":500,"message":"backendError"}}`))
		}))
		defer srvErr.Close()
		h3 := newTestHarness(t, false)
		h3.ctrl.driveAPIBaseURL = srvErr.URL
		h3.ctrl.driveHTTPClient = srvErr.Client()
		h3.auth.driveScope = true
		_ = h3.vault.Set("colab", map[string]any{"token": "tok-h1-d"})
		_ = h3.vault.Set("google_profile", map[string]any{"sub": "sub-h1-d"})
		h3.ctrl.mu.Lock()
		h3.ctrl.driveOutputs["sub:sub-h1-d"] = map[string]*ColabOneDriveOutputRecord{
			oidForbidden: {
				ID: oidForbidden, Kind: "image", Filename: "forbidden.png", Extension: "png", Mime: "image/png",
				Size: 64, DriveSynced: true, DriveMode: "mount", DriveFileID: "fid-403",
			},
			oidTransient: {
				ID: oidTransient, Kind: "image", Filename: "transient.png", Extension: "png", Mime: "image/png",
				Size: 64, DriveSynced: true, DriveMode: "mount", DriveFileID: "fid-500",
			},
		}
		h3.ctrl.mu.Unlock()

		resForbidden, err := h3.ctrl.deleteDriveOutputOffline(context.Background(), oidForbidden)
		if err != nil {
			t.Fatalf("expected HTTP 403 permanent Drive error to fall back locally with warning, got %v", err)
		}
		if !asBool(resForbidden["ok"]) || !asBool(resForbidden["local_only"]) || !asBool(resForbidden["manual_cloud_cleanup"]) || asString(resForbidden["warning_reason"]) != "drive_api_unavailable" {
			t.Fatalf("unexpected HTTP 403 delete response: %#v", resForbidden)
		}
		if h3.ctrl.driveOutputRecordCopy(oidForbidden) != nil {
			t.Fatal("expected local record removed after permanent HTTP 403")
		}

		if _, err := h3.ctrl.deleteDriveOutputOffline(context.Background(), oidTransient); err == nil {
			t.Fatal("expected transient HTTP 500 on Drive API trash to return an error for retry")
		}
		if h3.ctrl.driveOutputRecordCopy(oidTransient) == nil {
			t.Fatal("expected local record preserved on transient HTTP 500 so user can retry")
		}
	})

	// 1b) Online delete of an exported colab_drivefs output deletes the runtime copy AND removes
	//     the local Drive record (no leftover zombie card) with a mount-mode manual cleanup warning.
	t.Run("OnlineDeleteExportedColabDriveFSRemovesCardWithWarning", func(t *testing.T) {
		h := newTestHarness(t, false)
		h.auth.driveScope = true
		_ = h.vault.Set("colab", map[string]any{"token": "tok-online-dfs"})
		_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-online-dfs"})
		seedTestSession(h.vault, "ep-dfs", "L4", true)

		oidDFS := "e6e6e6e6e6e6e6e6e6e6e6e6e6e6e6e6"
		oidNoAPI := "e7e7e7e7e7e7e7e7e7e7e7e7e7e7e7e7"
		h.ctrl.mu.Lock()
		h.ctrl.driveOutputs["sub:sub-online-dfs"] = map[string]*ColabOneDriveOutputRecord{
			oidDFS: {
				ID: oidDFS, Kind: "video", Filename: "dfs_export.mp4", Extension: "mp4", Mime: "video/mp4",
				Size: 4096, DriveSynced: true, DriveExported: true, DriveMode: "colab_drivefs",
				DrivePath: "My Drive/ColabOne_Drive/outputs/dfs_export.mp4",
			},
			oidNoAPI: {
				ID: oidNoAPI, Kind: "image", Filename: "noapi_export.png", Extension: "png", Mime: "image/png",
				Size: 256, DriveSynced: true, DriveExported: true, DriveMode: "mount",
				DriveFileID: "fid-noapi-exp",
			},
		}
		h.ctrl.mu.Unlock()

		remainingLive := []any{
			map[string]any{"id": oidDFS, "extension": "mp4"},
			map[string]any{"id": oidNoAPI, "extension": "png"},
		}
		h.ctrl.Update(map[string]any{
			"runtime":       map[string]any{"state": "ready", "id": "ep-dfs"},
			"comfy_outputs": remainingLive,
		})
		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if method == "DELETE" && strings.HasPrefix(path, "comfyui/outputs/") {
				delID := strings.TrimPrefix(path, "comfyui/outputs/")
				next := make([]any, 0, len(remainingLive))
				for _, it := range remainingLive {
					if asString(asMap(it)["id"]) != delID {
						next = append(next, it)
					}
				}
				remainingLive = next
				return map[string]any{"ok": true}, nil
			}
			if path == "snapshot" {
				return map[string]any{
					"runtime":       map[string]any{"state": "ready", "id": "ep-dfs", "protocol": 7, "tasks": []any{}},
					"models":        []any{},
					"workflows":     []any{},
					"comfy_outputs": remainingLive,
				}, nil
			}
			return map[string]any{"ok": true}, nil
		}

		resDFS, err := h.ctrl.Command(context.Background(), "comfyui.output.delete", map[string]any{"id": oidDFS})
		if err != nil {
			t.Fatalf("online delete of exported colab_drivefs failed: %v", err)
		}
		resMap := asMap(resDFS)
		if !asBool(resMap["manual_cloud_cleanup"]) || asString(resMap["warning_reason"]) != "colab_drivefs" {
			t.Fatalf("expected colab_drivefs manual cloud cleanup warning, got %#v", resMap)
		}
		if h.ctrl.driveOutputRecordCopy(oidDFS) != nil {
			t.Fatal("expected colab_drivefs Drive record removed after online delete (no zombie card)")
		}

		// Online delete when !canDirectRead also removes the exported record with drive_api_unavailable warning.
		h.auth.driveScope = false
		resNoAPI, err := h.ctrl.Command(context.Background(), "comfyui.output.delete", map[string]any{"id": oidNoAPI})
		if err != nil {
			t.Fatalf("online delete without Drive API failed: %v", err)
		}
		noAPIMap := asMap(resNoAPI)
		if !asBool(noAPIMap["manual_cloud_cleanup"]) || asString(noAPIMap["warning_reason"]) != "drive_api_unavailable" {
			t.Fatalf("expected drive_api_unavailable manual cloud cleanup warning, got %#v", noAPIMap)
		}
		if h.ctrl.driveOutputRecordCopy(oidNoAPI) != nil {
			t.Fatal("expected exported Drive record removed after online delete when !canDirectRead")
		}

		// Online delete when Drive API trash returns permanent HTTP 403 removes the exported record
		// with drive_api_unavailable warning instead of leaving a zombie card.
		oidOnline403 := "eaeaeaeaeaeaeaeaeaeaeaeaeaeaeaea"
		srv403 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":403,"message":"insufficientFilePermissions"}}`))
		}))
		defer srv403.Close()
		h.ctrl.driveAPIBaseURL = srv403.URL
		h.ctrl.driveHTTPClient = srv403.Client()
		h.auth.driveScope = true
		h.ctrl.mu.Lock()
		h.ctrl.driveOutputs["sub:sub-online-dfs"][oidOnline403] = &ColabOneDriveOutputRecord{
			ID: oidOnline403, Kind: "image", Filename: "online403.png", Extension: "png", Mime: "image/png",
			Size: 128, DriveSynced: true, DriveExported: true, DriveMode: "mount",
			DriveFileID: "fid-online-403",
		}
		h.ctrl.mu.Unlock()
		remainingLive = []any{map[string]any{"id": oidOnline403, "extension": "png"}}
		h.ctrl.Update(map[string]any{
			"runtime":       map[string]any{"state": "ready", "id": "ep-dfs"},
			"comfy_outputs": remainingLive,
		})
		res403, err := h.ctrl.Command(context.Background(), "comfyui.output.delete", map[string]any{"id": oidOnline403})
		if err != nil {
			t.Fatalf("online delete with HTTP 403 on Drive trash failed: %v", err)
		}
		map403 := asMap(res403)
		if !asBool(map403["manual_cloud_cleanup"]) || asString(map403["warning_reason"]) != "drive_api_unavailable" {
			t.Fatalf("expected drive_api_unavailable warning on online HTTP 403, got %#v", map403)
		}
		if h.ctrl.driveOutputRecordCopy(oidOnline403) != nil {
			t.Fatal("expected exported Drive record removed on permanent HTTP 403 during online delete")
		}
	})

	// 2) A3 L4: clearDriveOutputWorkspaceLinks also clears DriveWebViewLink when it points to oldWSFileID.
	t.Run("ClearWorkspaceLinksClearsWorkspaceWebViewLink", func(t *testing.T) {
		h := newTestHarness(t, false)
		_ = h.vault.Set("colab", map[string]any{"token": "tok-l4"})
		_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-l4"})
		oidWS := "e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4"
		oidExp := "e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5"
		h.ctrl.mu.Lock()
		h.ctrl.driveOutputs["sub:sub-l4"] = map[string]*ColabOneDriveOutputRecord{
			oidWS: {
				ID:                   oidWS,
				DriveSynced:          true,
				DriveExported:        true,
				DriveFileID:          "exp-fid-1",
				DriveWorkspaceFileID: "ws-fid-1",
				DriveWorkspaceMetaID: "ws-meta-1",
				DriveWebViewLink:     "https://drive.google.com/file/d/ws-fid-1/view",
			},
			oidExp: {
				ID:                   oidExp,
				DriveSynced:          true,
				DriveExported:        true,
				DriveFileID:          "exp-fid-2",
				DriveWorkspaceFileID: "ws-fid-2",
				DriveWebViewLink:     "https://drive.google.com/file/d/exp-fid-2/view",
			},
		}
		h.ctrl.mu.Unlock()

		h.ctrl.clearDriveOutputWorkspaceLinks(oidWS)
		recWS := h.ctrl.driveOutputRecordCopy(oidWS)
		if recWS == nil || recWS.DriveWorkspaceFileID != "" || recWS.DriveWorkspaceMetaID != "" || recWS.DriveWebViewLink != "" {
			t.Fatalf("expected workspace IDs and workspace DriveWebViewLink cleared, got %+v", recWS)
		}

		h.ctrl.clearDriveOutputWorkspaceLinks(oidExp)
		recExp := h.ctrl.driveOutputRecordCopy(oidExp)
		if recExp == nil || recExp.DriveWorkspaceFileID != "" || recExp.DriveWebViewLink != "https://drive.google.com/file/d/exp-fid-2/view" {
			t.Fatalf("expected exported DriveWebViewLink preserved while workspace ID cleared, got %+v", recExp)
		}
	})

	t.Run("DriveFSDefaultsToSeparateMountFolder", func(t *testing.T) {
		h := newTestHarness(t, false)
		_ = h.vault.Set("colab", map[string]any{"token": "tok-folder"})
		_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-folder"})
		oidMount := "f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1"
		oidRclone := "f2f2f2f2f2f2f2f2f2f2f2f2f2f2f2f2"

		// DriveFS mode defaults to ColabOne_Drive_Mount.
		h.ctrl.Update(map[string]any{
			"runtime": map[string]any{
				"state": "ready",
				"drive": map[string]any{"connected": true, "mode": "colab_drivefs", "remote_folder": "ColabOne_Drive_Mount"},
			},
			"comfy_outputs": []any{
				map[string]any{"id": oidMount, "filename": "dfs_clip.mp4", "extension": "mp4", "size": 1024},
			},
		})
		h.ctrl.recordExportedDriveOutput(oidMount, map[string]any{"filename": "dfs_clip.mp4", "size": 1024})
		recMount := h.ctrl.driveOutputRecordCopy(oidMount)
		if recMount == nil || recMount.DriveFolder != "ColabOne_Drive_Mount/outputs" || recMount.DrivePath != "My Drive/ColabOne_Drive_Mount/outputs/dfs_clip.mp4" {
			t.Fatalf("expected ColabOne_Drive_Mount paths for colab_drivefs, got %+v", recMount)
		}

		// Headless mount mode stays on ColabOne_Drive.
		h.ctrl.Update(map[string]any{
			"runtime": map[string]any{
				"state": "ready",
				"drive": map[string]any{"connected": true, "mode": "mount", "remote_folder": "ColabOne_Drive"},
			},
			"comfy_outputs": []any{
				map[string]any{"id": oidRclone, "filename": "rclone_clip.mp4", "extension": "mp4", "size": 2048},
			},
		})
		h.ctrl.recordExportedDriveOutput(oidRclone, map[string]any{"filename": "rclone_clip.mp4", "size": 2048})
		recRclone := h.ctrl.driveOutputRecordCopy(oidRclone)
		if recRclone == nil || recRclone.DriveFolder != "ColabOne_Drive/outputs" || recRclone.DrivePath != "My Drive/ColabOne_Drive/outputs/rclone_clip.mp4" {
			t.Fatalf("expected ColabOne_Drive paths for headless mount, got %+v", recRclone)
		}
	})
}

func TestDriveOutputsPersistentDeleteTombstonesPreventResurrection(t *testing.T) {
	ctx := context.Background()

	// 1. Deleting a colab_drivefs output locally persists a tombstone across app restart
	//    and suppresses resurrection when a live GPU remounts DriveFS and restores the workspace file.
	t.Run("MountModeOfflineDeleteSurvivesRestartAndLiveDriveFSRemount", func(t *testing.T) {
		h := newTestHarness(t, false)
		h.auth.driveScope = false
		_ = h.vault.Set("colab", map[string]any{"token": "tok-tomb-1"})
		_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-tomb-1"})

		oidDeleted := "d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1"
		oidFresh := "d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2"

		h.ctrl.mu.Lock()
		h.ctrl.driveOutputs["sub:sub-tomb-1"] = map[string]*ColabOneDriveOutputRecord{
			oidDeleted: {
				ID: oidDeleted, Kind: "video", Filename: "mounted_avatar.mp4", Extension: "mp4", Mime: "video/mp4",
				Size: 54693888, CreatedAt: float64(time.Now().Unix()) - 3600,
				DriveSynced: true, DriveExported: true, DriveMode: "colab_drivefs",
				DrivePath: "My Drive/ColabOne_Drive_Mount/outputs/mounted_avatar.mp4",
			},
		}
		h.ctrl.mu.Unlock()

		res, err := h.ctrl.deleteDriveOutputOffline(ctx, oidDeleted)
		if err != nil {
			t.Fatalf("deleteDriveOutputOffline failed: %v", err)
		}
		if !asBool(res["ok"]) || !asBool(res["manual_cloud_cleanup"]) {
			t.Fatalf("expected manual cloud cleanup warning response, got %#v", res)
		}

		// Simulate app restart by clearing in-memory maps and reloading drive_outputs.json.
		h.ctrl.loadDriveOutputs()
		if h.ctrl.driveOutputRecordCopy(oidDeleted) != nil {
			t.Fatal("deleted output must stay removed after reload")
		}

		// Simulate starting a GPU that mounts DriveFS and restores the deleted file alongside a fresh output.
		rt := map[string]any{
			"state": "ready",
			"id":    "ep-remount",
			"drive": map[string]any{"connected": true, "mode": "colab_drivefs", "remote_folder": "ColabOne_Drive_Mount"},
		}
		restoredLive := []any{
			map[string]any{"id": oidDeleted, "filename": "mounted_avatar.mp4", "extension": "mp4", "size": int64(54693888), "created_at": float64(time.Now().Unix()) - 3600},
			map[string]any{"id": oidFresh, "filename": "fresh_clip.mp4", "extension": "mp4", "size": int64(2048), "created_at": float64(time.Now().Unix())},
		}
		h.ctrl.Update(map[string]any{
			"runtime":       rt,
			"comfy_outputs": restoredLive,
		})
		h.ctrl.recordSyncedRemoteOutputs(rt, restoredLive)

		if h.ctrl.driveOutputRecordCopy(oidDeleted) != nil {
			t.Fatal("deleted mount-mode output MUST NOT be re-recorded into driveOutputs on remount")
		}
		if h.ctrl.driveOutputRecordCopy(oidFresh) == nil {
			t.Fatal("expected fresh non-deleted output to be recorded on remount")
		}
		snap := h.ctrl.Snapshot()
		snapOutputs := asAnySlice(snap["comfy_outputs"])
		if len(snapOutputs) != 1 || asString(asMap(snapOutputs[0])["id"]) != oidFresh {
			t.Fatalf("expected Snapshot to hide tombstoned live output %s and show only %s, got %#v", oidDeleted, oidFresh, snapOutputs)
		}
	})

	// 2. Refreshing Drive API (SyncDriveOutputs) never resurrects locally deleted outputs
	//    whether matched by hex32 workspace ID, known DriveFileID (HTTP 403 case), or
	//    pre-existing outputs/ filename+size when DriveFileID was empty at delete time.
	t.Run("SyncDriveOutputsSuppressesTombstonedFilesByIDFileIDAndPreExistingExport", func(t *testing.T) {
		oidNoID := "d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3"
		oid403 := "d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4"
		oidKeep := "d5d5d5d5d5d5d5d5d5d5d5d5d5d5d5d5"
		pastRFC := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
		futureRFC := time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodPatch {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"code":403,"message":"insufficientFilePermissions"}}`))
				return
			}
			q := r.URL.Query().Get("q")
			switch {
			case strings.Contains(q, "'root' in parents") && strings.Contains(q, "ColabOne_Drive"):
				_, _ = w.Write([]byte(`{"files":[{"id":"folder-root","name":"ColabOne_Drive","mimeType":"application/vnd.google-apps.folder"}]}`))
			case strings.Contains(q, "'folder-root' in parents") && strings.Contains(q, "workspace"):
				_, _ = w.Write([]byte(`{"files":[{"id":"folder-ws","name":"workspace","mimeType":"application/vnd.google-apps.folder"}]}`))
			case strings.Contains(q, "'folder-ws' in parents") && strings.Contains(q, "comfyui-outputs"):
				_, _ = w.Write([]byte(`{"files":[{"id":"folder-co","name":"comfyui-outputs","mimeType":"application/vnd.google-apps.folder"}]}`))
			case strings.Contains(q, "'folder-co' in parents"):
				_, _ = w.Write([]byte(fmt.Sprintf(`{"files":[
					{"id":"fid-ws-noid","name":"%s.mp4","size":"4096","createdTime":"%s"},
					{"id":"fid-ws-keep","name":"%s.png","size":"512","createdTime":"%s"}
				]}`, oidNoID, pastRFC, oidKeep, pastRFC)))
			case strings.Contains(q, "'folder-root' in parents") && strings.Contains(q, "outputs"):
				_, _ = w.Write([]byte(`{"files":[{"id":"folder-out","name":"outputs","mimeType":"application/vnd.google-apps.folder"}]}`))
			case strings.Contains(q, "'folder-out' in parents"):
				_, _ = w.Write([]byte(fmt.Sprintf(`{"files":[
					{"id":"fid-exp-noid","name":"legacy_mounted.mp4","size":"4096","createdTime":"%s"},
					{"id":"fid-exp-403","name":"forbidden_export.mp4","size":"8192","createdTime":"%s"},
					{"id":"fid-exp-future","name":"legacy_mounted.mp4","size":"4096","createdTime":"%s"}
				]}`, pastRFC, pastRFC, futureRFC)))
			default:
				_, _ = w.Write([]byte(`{"files":[]}`))
			}
		}))
		defer srv.Close()

		h := newTestHarness(t, false)
		h.ctrl.driveAPIBaseURL = srv.URL
		h.ctrl.driveHTTPClient = srv.Client()
		h.auth.driveScope = true
		_ = h.vault.Set("colab", map[string]any{"token": "tok-tomb-2"})
		_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-tomb-2"})

		h.ctrl.mu.Lock()
		h.ctrl.driveOutputs["sub:sub-tomb-2"] = map[string]*ColabOneDriveOutputRecord{
			oidNoID: {
				ID: oidNoID, Kind: "video", Filename: "legacy_mounted.mp4", Extension: "mp4", Mime: "video/mp4",
				Size: 4096, DriveSynced: true, DriveExported: true, DriveMode: "colab_drivefs",
			},
			oid403: {
				ID: oid403, Kind: "video", Filename: "forbidden_export.mp4", Extension: "mp4", Mime: "video/mp4",
				Size: 8192, DriveSynced: true, DriveExported: true, DriveMode: "mount",
				DriveFileID: "fid-exp-403",
			},
		}
		h.ctrl.mu.Unlock()

		// Delete both locally (oidNoID via colab_drivefs empty IDs; oid403 via HTTP 403 fallback).
		if _, err := h.ctrl.deleteDriveOutputOffline(ctx, oidNoID); err != nil {
			t.Fatalf("delete oidNoID failed: %v", err)
		}
		if _, err := h.ctrl.deleteDriveOutputOffline(ctx, oid403); err != nil {
			t.Fatalf("delete oid403 failed: %v", err)
		}

		// Reload from disk (simulating restart) and run SyncDriveOutputs (simulating Refresh click).
		h.ctrl.loadDriveOutputs()
		syncRes, err := h.ctrl.SyncDriveOutputs(ctx)
		if err != nil || !asBool(syncRes["synced"]) {
			t.Fatalf("SyncDriveOutputs failed: res=%#v err=%v", syncRes, err)
		}

		// Neither deleted item may resurrect (not by original ID nor by synthetic Drive ID!).
		if h.ctrl.driveOutputRecordCopy(oidNoID) != nil || h.ctrl.driveOutputRecordCopy(syntheticDriveOutputID("fid-exp-noid")) != nil {
			t.Fatal("deleted colab_drivefs output MUST NOT resurrect on SyncDriveOutputs refresh")
		}
		if h.ctrl.driveOutputRecordCopy(oid403) != nil || h.ctrl.driveOutputRecordCopy(syntheticDriveOutputID("fid-exp-403")) != nil {
			t.Fatal("HTTP 403 deleted output MUST NOT resurrect on SyncDriveOutputs refresh")
		}
		// Non-deleted workspace item and genuinely new future file MUST still appear!
		if h.ctrl.driveOutputRecordCopy(oidKeep) == nil {
			t.Fatal("expected non-deleted workspace output to remain visible after SyncDriveOutputs")
		}
		if h.ctrl.driveOutputRecordCopy(syntheticDriveOutputID("fid-exp-future")) == nil {
			t.Fatal("expected genuinely new future file created after delete NOT to be falsely hidden")
		}

		// Verify tombstones are isolated per Google account (Account B is unaffected by Account A).
		_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-tomb-other"})
		if _, err := h.ctrl.SyncDriveOutputs(ctx); err != nil {
			t.Fatalf("SyncDriveOutputs for Account B failed: %v", err)
		}
		if h.ctrl.driveOutputRecordCopy(oidNoID) == nil {
			t.Fatal("Account A tombstones MUST NOT hide outputs for Account B")
		}
	})
}

func TestNormalizeColabOneOutputWorkflowName(t *testing.T) {
	if got := normalizeColabOneOutputWorkflowName("AI 模特换装棚拍（仅非商用）"); got != "AI 模特换装棚拍" {
		t.Fatalf("expected legacy non-commercial suffix stripped, got %q", got)
	}
	if got := normalizeColabOneOutputWorkflowName("  AI 模特换装棚拍（仅非商用）  "); got != "AI 模特换装棚拍" {
		t.Fatalf("expected padded legacy non-commercial suffix stripped, got %q", got)
	}
	if got := normalizeColabOneOutputWorkflowName("口播数字人"); got != "口播数字人" {
		t.Fatalf("expected unrelated workflow name preserved, got %q", got)
	}

	rawOutputs := []any{
		map[string]any{
			"id":            "out-legacy",
			"workflow_id":   "fashion-tryon-h3",
			"workflow_name": "AI 模特换装棚拍（仅非商用）",
		},
		map[string]any{
			"id":            "out-custom",
			"workflow_id":   "custom-1",
			"workflow_name": "自定义棚拍",
		},
	}
	normalized := normalizeColabOneOutputsWorkflowNames(rawOutputs)
	if got := asString(asMap(normalized[0])["workflow_name"]); got != "AI 模特换装棚拍" {
		t.Fatalf("expected legacy output workflow_name normalized, got %q", got)
	}
	if got := asString(asMap(normalized[1])["workflow_name"]); got != "自定义棚拍" {
		t.Fatalf("expected custom output workflow_name preserved, got %q", got)
	}
}
