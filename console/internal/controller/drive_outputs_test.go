package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
