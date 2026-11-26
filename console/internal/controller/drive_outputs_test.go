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
	"strings"
	"sync"
	"testing"
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

func asFloatZero(v any) float64 {
	f, _ := asFloat(v)
	return f
}
