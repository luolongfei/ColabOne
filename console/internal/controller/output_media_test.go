package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestParseColabOneByteRange(t *testing.T) {
	const total int64 = 20 << 20 // 20 MiB

	// Empty header -> full representation
	s, e, isRange, err := parseColabOneByteRange("", total)
	if err != nil || isRange || s != 0 || e != total-1 {
		t.Fatalf("empty range got (%d,%d,%v,%v)", s, e, isRange, err)
	}

	// Open-ended range bytes=0- -> capped at 2 MiB streaming window
	s, e, isRange, err = parseColabOneByteRange("bytes=0-", total)
	if err != nil || !isRange || s != 0 || e != colabOneMediaOpenRangeWindowBytes-1 {
		t.Fatalf("open-ended range got (%d,%d,%v,%v), want (0,%d,true,nil)", s, e, isRange, err, colabOneMediaOpenRangeWindowBytes-1)
	}

	// WKWebView/Safari 2-byte probe
	s, e, isRange, err = parseColabOneByteRange("bytes=0-1", total)
	if err != nil || !isRange || s != 0 || e != 1 {
		t.Fatalf("bytes=0-1 got (%d,%d,%v,%v)", s, e, isRange, err)
	}

	// Explicit huge range -> capped at 8 MiB
	s, e, isRange, err = parseColabOneByteRange("bytes=1024-15728640", total)
	if err != nil || !isRange || s != 1024 || e != 1024+colabOneMediaMaxRangeBytes-1 {
		t.Fatalf("large explicit range got (%d,%d,%v,%v)", s, e, isRange, err)
	}

	// Suffix range bytes=-128
	s, e, isRange, err = parseColabOneByteRange("bytes=-128", total)
	if err != nil || !isRange || s != total-128 || e != total-1 {
		t.Fatalf("suffix range got (%d,%d,%v,%v)", s, e, isRange, err)
	}

	// Out-of-bounds start -> error
	if _, _, _, err = parseColabOneByteRange(fmt.Sprintf("bytes=%d-", total), total); err == nil {
		t.Fatal("expected error for start == total")
	}

	// Reversed range -> error
	if _, _, _, err = parseColabOneByteRange("bytes=500-100", total); err == nil {
		t.Fatal("expected error for reversed range")
	}

	// Unknown range unit -> ignored per RFC 9110
	s, e, isRange, err = parseColabOneByteRange("items=0-5", total)
	if err != nil || isRange || s != 0 || e != total-1 {
		t.Fatalf("unknown unit got (%d,%d,%v,%v)", s, e, isRange, err)
	}
}

func TestServeOutputMediaHTTPDriveRangeFallbackAndRuntime(t *testing.T) {
	driveVideoID := "1234567890abcdef1234567890abcdef"
	runtimeVideoID := "fedcba0987654321fedcba0987654321"
	driveFSOnlyID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	// 3 MiB payload (> 2 MiB open-range window) so we verify progressive 206 streaming.
	driveVideo := make([]byte, 3<<20)
	for i := range driveVideo {
		driveVideo[i] = byte((i * 31) % 251)
	}
	runtimeVideo := bytes.Repeat([]byte("RUNTIME-MP4-FRAME-DATA-01234567!"), 4096) // 128 KiB

	var (
		mu              sync.Mutex
		ignoreRangeOnce bool
		failDriveOnce   bool
		lastDriveRange  string
		syncCalls       int
	)

	mockDrive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer valid-tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/files" {
			q := r.URL.Query().Get("q")
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.Contains(q, "name = 'ColabOne_Drive'"):
				mu.Lock()
				syncCalls++
				mu.Unlock()
				_, _ = w.Write([]byte(`{"files":[{"id":"dir-root","name":"ColabOne_Drive"}]}`))
			case strings.Contains(q, "name = 'workspace'"):
				_, _ = w.Write([]byte(`{"files":[{"id":"dir-ws","name":"workspace"}]}`))
			case strings.Contains(q, "name = 'comfyui-outputs'"):
				_, _ = w.Write([]byte(`{"files":[{"id":"dir-co","name":"comfyui-outputs"}]}`))
			case strings.Contains(q, "'dir-co' in parents"):
				_ = json.NewEncoder(w).Encode(map[string]any{
					"files": []map[string]any{
						{
							"id":           "file-drive-video",
							"name":         driveVideoID + ".mp4",
							"size":         strconv.Itoa(len(driveVideo)),
							"createdTime":  "2026-11-25T18:00:00Z",
							"modifiedTime": "2026-11-25T18:01:00Z",
							"webViewLink":  "https://drive.google.com/file/d/file-drive-video/view",
						},
					},
				})
			default:
				_, _ = w.Write([]byte(`{"files":[]}`))
			}
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/files/file-drive-video" && r.URL.Query().Get("alt") == "media" {
			mu.Lock()
			fail := failDriveOnce
			failDriveOnce = false
			ignore := ignoreRangeOnce
			ignoreRangeOnce = false
			lastDriveRange = r.Header.Get("Range")
			mu.Unlock()

			if fail {
				http.NotFound(w, r)
				return
			}
			rng := r.Header.Get("Range")
			if !ignore && strings.HasPrefix(rng, "bytes=") {
				var start, end int
				if _, err := fmt.Sscanf(rng, "bytes=%d-%d", &start, &end); err == nil {
					if end >= len(driveVideo) {
						end = len(driveVideo) - 1
					}
					w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(driveVideo)))
					w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
					w.WriteHeader(http.StatusPartialContent)
					_, _ = w.Write(driveVideo[start : end+1])
					return
				}
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(driveVideo)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(driveVideo)
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
	_ = h.vault.Set("colab", map[string]any{"token": "stream-tok", "refresh_token": "rt-stream"})
	_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-stream", "email": "stream@example.com"})

	// Seed a Drive-synced record WITHOUT DriveFileID yet to verify on-demand SyncDriveOutputs resolution!
	h.ctrl.mu.Lock()
	h.ctrl.driveOutputs["sub:sub-stream"] = map[string]*ColabOneDriveOutputRecord{
		driveVideoID: {
			ID:           driveVideoID,
			WorkflowName: "口播数字人",
			Kind:         "video",
			Filename:     "long_clip.mp4",
			Extension:    "mp4",
			Mime:         "video/mp4",
			Size:         int64(len(driveVideo)),
			CreatedAt:    1764093600,
			Elapsed:      300,
			DriveSynced:  true,
			DriveMode:    "mount",
		},
	}
	h.ctrl.mu.Unlock()

	// 1) HEAD request triggers on-demand sync to find file ID and returns Accept-Ranges + exact size.
	headReq := httptest.NewRequest(http.MethodHead, "/media/outputs/"+driveVideoID, nil)
	headRec := httptest.NewRecorder()
	h.ctrl.ServeOutputMediaHTTP(context.Background(), headRec, headReq, driveVideoID)
	if headRec.Code != http.StatusOK {
		t.Fatalf("HEAD expected 200, got %d body=%s", headRec.Code, headRec.Body.String())
	}
	if headRec.Header().Get("Accept-Ranges") != "bytes" ||
		headRec.Header().Get("Content-Type") != "video/mp4" ||
		headRec.Header().Get("Content-Length") != strconv.Itoa(len(driveVideo)) {
		t.Fatalf("unexpected HEAD headers: %#v", headRec.Header())
	}
	mu.Lock()
	gotSyncs := syncCalls
	mu.Unlock()
	if gotSyncs != 1 {
		t.Fatalf("expected 1 on-demand SyncDriveOutputs call, got %d", gotSyncs)
	}

	// 2) Open-ended GET Range: bytes=0- streams first 2 MiB window as 206 Partial Content.
	reqOpen := httptest.NewRequest(http.MethodGet, "/media/outputs/"+driveVideoID, nil)
	reqOpen.Header.Set("Range", "bytes=0-")
	recOpen := httptest.NewRecorder()
	h.ctrl.ServeOutputMediaHTTP(context.Background(), recOpen, reqOpen, driveVideoID)
	if recOpen.Code != http.StatusPartialContent {
		t.Fatalf("open-ended Range expected 206, got %d body=%s", recOpen.Code, recOpen.Body.String())
	}
	wantCR := fmt.Sprintf("bytes 0-%d/%d", colabOneMediaOpenRangeWindowBytes-1, len(driveVideo))
	if recOpen.Header().Get("Content-Range") != wantCR {
		t.Fatalf("Content-Range=%q, want %q", recOpen.Header().Get("Content-Range"), wantCR)
	}
	if !bytes.Equal(recOpen.Body.Bytes(), driveVideo[:colabOneMediaOpenRangeWindowBytes]) {
		t.Fatalf("open-ended Range body mismatch (len=%d)", recOpen.Body.Len())
	}
	mu.Lock()
	gotRange := lastDriveRange
	mu.Unlock()
	if gotRange != fmt.Sprintf("bytes=0-%d", colabOneMediaOpenRangeWindowBytes-1) {
		t.Fatalf("expected upstream Range forwarded, got %q", gotRange)
	}

	// 3) Tail seek Range: bytes=-64 with 200 OK Range-ignored upstream fallback.
	mu.Lock()
	ignoreRangeOnce = true
	mu.Unlock()
	reqTail := httptest.NewRequest(http.MethodGet, "/media/outputs/"+driveVideoID, nil)
	reqTail.Header.Set("Range", "bytes=-64")
	recTail := httptest.NewRecorder()
	h.ctrl.ServeOutputMediaHTTP(context.Background(), recTail, reqTail, driveVideoID)
	if recTail.Code != http.StatusPartialContent {
		t.Fatalf("tail seek fallback expected 206, got %d", recTail.Code)
	}
	if !bytes.Equal(recTail.Body.Bytes(), driveVideo[len(driveVideo)-64:]) {
		t.Fatalf("tail seek fallback body mismatch")
	}

	// 4) Unsatisfiable range -> 416 with Content-Range: bytes */<total>
	req416 := httptest.NewRequest(http.MethodGet, "/media/outputs/"+driveVideoID, nil)
	req416.Header.Set("Range", fmt.Sprintf("bytes=%d-", len(driveVideo)+10))
	rec416 := httptest.NewRecorder()
	h.ctrl.ServeOutputMediaHTTP(context.Background(), rec416, req416, driveVideoID)
	if rec416.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("expected 416, got %d", rec416.Code)
	}
	if rec416.Header().Get("Content-Range") != fmt.Sprintf("bytes */%d", len(driveVideo)) {
		t.Fatalf("expected Content-Range bytes */%d, got %q", len(driveVideo), rec416.Header().Get("Content-Range"))
	}

	// 5) POST rejected with 405 Method Not Allowed.
	reqPost := httptest.NewRequest(http.MethodPost, "/media/outputs/"+driveVideoID, nil)
	recPost := httptest.NewRecorder()
	h.ctrl.ServeOutputMediaHTTP(context.Background(), recPost, reqPost, driveVideoID)
	if recPost.Code != http.StatusMethodNotAllowed || recPost.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST expected 405, got %d Allow=%q", recPost.Code, recPost.Header().Get("Allow"))
	}

	// 6) Active runtime streaming with arbitrary byte seek + Drive 404 fallback to runtime.
	h.provider.addManagedSession("ep-stream", "A100", "STANDARD", true)
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if strings.HasPrefix(path, "comfyui/outputs/") && strings.Contains(path, "/asset?offset=") {
			parts := strings.Split(path, "offset=")
			off, _ := strconv.Atoi(parts[1])
			src := runtimeVideo
			if strings.Contains(path, driveVideoID) {
				src = driveVideo
			}
			end := off + int(colabOneMediaRuntimeChunkBytes)
			if end > len(src) {
				end = len(src)
			}
			return map[string]any{
				"data":      base64.StdEncoding.EncodeToString(src[off:end]),
				"offset":    off,
				"size":      len(src),
				"mime":      "video/mp4",
				"extension": "mp4",
				"filename":  "live.mp4",
			}, nil
		}
		return map[string]any{"ok": true}, nil
	}
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{
			"state": "ready",
			"id":    "ep-stream",
		},
		"comfy_outputs": []any{
			map[string]any{
				"id":        runtimeVideoID,
				"kind":      "video",
				"filename":  "live.mp4",
				"extension": "mp4",
				"mime":      "video/mp4",
				"size":      len(runtimeVideo),
			},
			map[string]any{
				"id":        driveVideoID,
				"kind":      "video",
				"filename":  "long_clip.mp4",
				"extension": "mp4",
				"mime":      "video/mp4",
				"size":      len(driveVideo),
			},
		},
	})

	reqRT := httptest.NewRequest(http.MethodGet, "/media/outputs/"+runtimeVideoID, nil)
	reqRT.Header.Set("Range", "bytes=100-511")
	recRT := httptest.NewRecorder()
	h.ctrl.ServeOutputMediaHTTP(context.Background(), recRT, reqRT, runtimeVideoID)
	if recRT.Code != http.StatusPartialContent {
		t.Fatalf("runtime Range expected 206, got %d body=%s", recRT.Code, recRT.Body.String())
	}
	if !bytes.Equal(recRT.Body.Bytes(), runtimeVideo[100:512]) {
		t.Fatalf("runtime Range body mismatch")
	}

	// Drive 404 while runtime has the file -> seamless fallback to live runtime!
	mu.Lock()
	failDriveOnce = true
	mu.Unlock()
	reqFallback := httptest.NewRequest(http.MethodGet, "/media/outputs/"+driveVideoID, nil)
	reqFallback.Header.Set("Range", "bytes=0-255")
	recFallback := httptest.NewRecorder()
	h.ctrl.ServeOutputMediaHTTP(context.Background(), recFallback, reqFallback, driveVideoID)
	if recFallback.Code != http.StatusPartialContent || !bytes.Equal(recFallback.Body.Bytes(), driveVideo[:256]) {
		t.Fatalf("Drive->runtime fallback failed: code=%d len=%d", recFallback.Code, recFallback.Body.Len())
	}

	// 7) DriveFS-only offline record without direct read returns 403 + friendly hint.
	h.auth.driveScope = false
	h.ctrl.Update(map[string]any{
		"runtime":       map[string]any{"state": "stopped"},
		"comfy_outputs": []any{},
	})
	h.ctrl.mu.Lock()
	h.ctrl.driveOutputs["sub:sub-stream"][driveFSOnlyID] = &ColabOneDriveOutputRecord{
		ID:          driveFSOnlyID,
		Kind:        "video",
		Filename:    "drivefs.mp4",
		Extension:   "mp4",
		Mime:        "video/mp4",
		Size:        1024,
		DriveSynced: true,
		DriveMode:   "colab_drivefs",
	}
	h.ctrl.mu.Unlock()

	reqDFS := httptest.NewRequest(http.MethodGet, "/media/outputs/"+driveFSOnlyID, nil)
	recDFS := httptest.NewRecorder()
	h.ctrl.ServeOutputMediaHTTP(context.Background(), recDFS, reqDFS, driveFSOnlyID)
	if recDFS.Code != http.StatusForbidden || !strings.Contains(recDFS.Body.String(), "在 Google 云盘查看") {
		t.Fatalf("DriveFS expected 403 friendly hint, got %d body=%s", recDFS.Code, recDFS.Body.String())
	}
}

func TestOutputMediaStreamClientPoolingLiveBypassCooldownAndValidation(t *testing.T) {
	liveNoFileID := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	missingDriveID := "cccccccccccccccccccccccccccccccc"
	shrunkDriveID := "dddddddddddddddddddddddddddddddd"
	badOffsetID := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"

	var (
		mu        sync.Mutex
		syncCalls int
	)

	mockDrive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/files" {
			q := r.URL.Query().Get("q")
			if strings.Contains(q, "name = 'ColabOne_Drive'") {
				mu.Lock()
				syncCalls++
				mu.Unlock()
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"files":[]}`))
			return
		}
		if r.URL.Path == "/files/file-shrunk" && r.URL.Query().Get("alt") == "media" {
			// Simulate upstream Drive file shrinking below cached size (actual total 100 bytes).
			w.Header().Set("Content-Range", "bytes 0-99/100")
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(bytes.Repeat([]byte("x"), 100))
			return
		}
		if r.URL.Path == "/files/file-wrong-start" && r.URL.Query().Get("alt") == "media" {
			// Simulate buggy upstream returning wrong start byte in 206 Content-Range.
			w.Header().Set("Content-Range", "bytes 0-99/4096")
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(bytes.Repeat([]byte("y"), 100))
			return
		}
		http.NotFound(w, r)
	}))
	defer mockDrive.Close()

	h := newTestHarness(t, false)
	// 1) Owned driveStreamClient() returns the same pooled *http.Client across calls (no per-request transport leak).
	h.ctrl.driveHTTPClientOwned = true
	c1 := h.ctrl.driveStreamClient()
	c2 := h.ctrl.driveStreamClient()
	if c1 == nil || c1 != c2 {
		t.Fatalf("expected pooled driveStreamHTTPClient reuse, got %p vs %p", c1, c2)
	}
	h.ctrl.driveHTTPClientOwned = false
	h.ctrl.driveAPIBaseURL = mockDrive.URL
	h.ctrl.driveHTTPClient = mockDrive.Client()
	if got := h.ctrl.driveStreamClient(); got != mockDrive.Client() {
		t.Fatalf("expected non-owned test driveHTTPClient returned directly")
	}

	h.auth.driveScope = true
	h.auth.requiresQuota = false
	_ = h.vault.Set("colab", map[string]any{"token": "tok-pool", "refresh_token": "rt-pool"})
	_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-pool", "email": "pool@example.com"})

	h.provider.addManagedSession("ep-pool", "A100", "STANDARD", true)
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if strings.Contains(path, badOffsetID) {
			return map[string]any{
				"data":      base64.StdEncoding.EncodeToString([]byte("wrong-offset-data")),
				"offset":    999,
				"size":      1024,
				"mime":      "video/mp4",
				"extension": "mp4",
			}, nil
		}
		return map[string]any{"ok": true}, nil
	}

	h.ctrl.mu.Lock()
	h.ctrl.driveOutputs["sub:sub-pool"] = map[string]*ColabOneDriveOutputRecord{
		liveNoFileID: {
			ID:          liveNoFileID,
			Kind:        "video",
			Filename:    "live_no_fid.mp4",
			Extension:   "mp4",
			Mime:        "video/mp4",
			Size:        4096,
			DriveSynced: true,
			DriveMode:   "mount",
		},
		missingDriveID: {
			ID:          missingDriveID,
			Kind:        "video",
			Filename:    "missing_fid.mp4",
			Extension:   "mp4",
			Mime:        "video/mp4",
			Size:        4096,
			DriveSynced: true,
			DriveMode:   "mount",
		},
		shrunkDriveID: {
			ID:          shrunkDriveID,
			Kind:        "video",
			Filename:    "shrunk.mp4",
			Extension:   "mp4",
			Mime:        "video/mp4",
			Size:        4096, // stale larger size
			DriveSynced: true,
			DriveMode:   "mount",
			DriveFileID: "file-shrunk",
		},
	}
	h.ctrl.mu.Unlock()

	// 2) Live runtime output without DriveFileID bypasses blocking Drive sync and resolves immediately to runtime.
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{"state": "ready", "id": "ep-pool"},
		"comfy_outputs": []any{
			map[string]any{
				"id":        liveNoFileID,
				"kind":      "video",
				"filename":  "live_no_fid.mp4",
				"extension": "mp4",
				"mime":      "video/mp4",
				"size":      4096,
			},
			map[string]any{
				"id":        badOffsetID,
				"kind":      "video",
				"filename":  "bad_offset.mp4",
				"extension": "mp4",
				"mime":      "video/mp4",
				"size":      1024,
			},
		},
	})
	metaLive, err := h.ctrl.ResolveOutputMediaMeta(context.Background(), liveNoFileID)
	if err != nil || metaLive.Source != "runtime" {
		t.Fatalf("expected immediate runtime resolution without blocking Drive sync, got meta=%#v err=%v", metaLive, err)
	}
	mu.Lock()
	gotSyncs := syncCalls
	mu.Unlock()
	if gotSyncs != 0 {
		t.Fatalf("expected 0 blocking SyncDriveOutputs calls for live runtime output, got %d", gotSyncs)
	}

	// 3) Runtime chunk offset mismatch is rejected cleanly with 502 and translated message.
	reqBadOff := httptest.NewRequest(http.MethodGet, "/media/outputs/"+badOffsetID, nil)
	reqBadOff.Header.Set("Range", "bytes=0-15")
	recBadOff := httptest.NewRecorder()
	h.ctrl.ServeOutputMediaHTTP(context.Background(), recBadOff, reqBadOff, badOffsetID)
	if recBadOff.Code != http.StatusBadGateway || !strings.Contains(recBadOff.Body.String(), "输出文件分片数据不完整") {
		t.Fatalf("expected 502 chunk mismatch error, got %d body=%s", recBadOff.Code, recBadOff.Body.String())
	}

	// 4) Offline missing DriveFileID triggers on-demand sync once, then respects 15s cooldown on consecutive probes.
	h.ctrl.Update(map[string]any{
		"runtime":       map[string]any{"state": "stopped"},
		"comfy_outputs": []any{},
	})
	if _, err := h.ctrl.ResolveOutputMediaMeta(context.Background(), missingDriveID); err == nil {
		t.Fatal("expected error when DriveFileID remains missing after sync")
	}
	if _, err := h.ctrl.ResolveOutputMediaMeta(context.Background(), missingDriveID); err == nil {
		t.Fatal("expected error on second probe during cooldown")
	}
	mu.Lock()
	gotSyncs = syncCalls
	mu.Unlock()
	if gotSyncs != 1 {
		t.Fatalf("expected cooldown to limit on-demand SyncDriveOutputs to 1 call, got %d", gotSyncs)
	}

	// 5) Upstream Drive file shrunk below requested Range start returns 416 instead of invalid Content-Range.
	h.ctrl.mu.Lock()
	h.ctrl.driveOutputs["sub:sub-pool"][shrunkDriveID] = &ColabOneDriveOutputRecord{
		ID:          shrunkDriveID,
		Kind:        "video",
		Filename:    "shrunk.mp4",
		Extension:   "mp4",
		Mime:        "video/mp4",
		Size:        4096, // stale larger size
		DriveSynced: true,
		DriveMode:   "mount",
		DriveFileID: "file-shrunk",
	}
	h.ctrl.mu.Unlock()
	reqShrunk := httptest.NewRequest(http.MethodGet, "/media/outputs/"+shrunkDriveID, nil)
	reqShrunk.Header.Set("Range", "bytes=500-999")
	recShrunk := httptest.NewRecorder()
	h.ctrl.ServeOutputMediaHTTP(context.Background(), recShrunk, reqShrunk, shrunkDriveID)
	if recShrunk.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("expected 416 when upstream Drive total < start, got %d body=%s", recShrunk.Code, recShrunk.Body.String())
	}

	// 6) Upstream Drive 206 returning mismatched Content-Range start byte is rejected cleanly with 502.
	wrongStartID := "ffffffffffffffffffffffffffffffff"
	h.ctrl.mu.Lock()
	h.ctrl.driveOutputs["sub:sub-pool"][wrongStartID] = &ColabOneDriveOutputRecord{
		ID:          wrongStartID,
		Kind:        "video",
		Filename:    "wrong_start.mp4",
		Extension:   "mp4",
		Mime:        "video/mp4",
		Size:        4096,
		DriveSynced: true,
		DriveMode:   "mount",
		DriveFileID: "file-wrong-start",
	}
	h.ctrl.mu.Unlock()
	reqWrongStart := httptest.NewRequest(http.MethodGet, "/media/outputs/"+wrongStartID, nil)
	reqWrongStart.Header.Set("Range", "bytes=500-599")
	recWrongStart := httptest.NewRecorder()
	h.ctrl.ServeOutputMediaHTTP(context.Background(), recWrongStart, reqWrongStart, wrongStartID)
	if recWrongStart.Code != http.StatusBadGateway || !strings.Contains(recWrongStart.Body.String(), "输出文件分片数据不完整") {
		t.Fatalf("expected 502 for mismatched upstream Content-Range start, got %d body=%s", recWrongStart.Code, recWrongStart.Body.String())
	}
}

func TestResolveOutputMediaMetaCanonicalMIMEAndStreamRedirectRefusal(t *testing.T) {
	liveID := "11112222333344445555666677778888"
	driveID := "88887777666655554444333322221111"
	postSyncID := "99998888777766665555444433332222"

	redirectTargetHit := false
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectTargetHit = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("should-never-be-followed"))
	}))
	defer redirectTarget.Close()

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
			case strings.Contains(q, "'dir-co' in parents"):
				_ = json.NewEncoder(w).Encode(map[string]any{
					"files": []map[string]any{
						{"id": "file-postsync", "name": postSyncID + ".png", "size": "128"},
						{"id": "meta-postsync", "name": postSyncID + ".json", "size": "64"},
					},
				})
			default:
				_, _ = w.Write([]byte(`{"files":[]}`))
			}
			return
		}
		if r.URL.Path == "/files/meta-postsync" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":        postSyncID,
				"filename":  "spoofed.png",
				"extension": "png",
				"kind":      "script",
				"mime":      "text/html",
				"size":      128,
			})
			return
		}
		if r.URL.Path == "/files/file-redirect" {
			http.Redirect(w, r, redirectTarget.URL+"/steal", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	defer mockDrive.Close()

	h := newTestHarness(t, false)
	h.ctrl.driveAPIBaseURL = mockDrive.URL
	h.auth.driveScope = true
	h.auth.requiresQuota = false
	_ = h.vault.Set("colab", map[string]any{"token": "tok-mime", "refresh_token": "rt-mime"})
	_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-mime", "email": "mime@example.com"})

	// Branch 1: Live runtime output with spoofed MIME must return canonical image/png.
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{"state": "ready", "id": "ep-mime"},
		"comfy_outputs": []any{
			map[string]any{
				"id":        liveID,
				"filename":  "evil.png",
				"extension": "png",
				"kind":      "html",
				"mime":      "text/html",
				"size":      256,
			},
		},
	})
	meta1, err := h.ctrl.ResolveOutputMediaMeta(context.Background(), liveID)
	if err != nil {
		t.Fatalf("ResolveOutputMediaMeta live branch failed: %v", err)
	}
	if meta1.Mime != "image/png" {
		t.Fatalf("live branch expected canonical image/png, got %q", meta1.Mime)
	}

	// Branch 2: Offline driveOutputs record with spoofed MIME must return canonical video/mp4.
	h.ctrl.Update(map[string]any{
		"runtime":       map[string]any{"state": "stopped"},
		"comfy_outputs": []any{},
	})
	h.ctrl.mu.Lock()
	h.ctrl.driveOutputs["sub:sub-mime"] = map[string]*ColabOneDriveOutputRecord{
		driveID: {
			ID:          driveID,
			Kind:        "html",
			Filename:    "clip.mp4",
			Extension:   "mp4",
			Mime:        "text/html",
			Size:        512,
			DriveSynced: true,
			DriveMode:   "drive_api",
			DriveFileID: "file-redirect",
		},
		postSyncID: {
			ID:          postSyncID,
			Kind:        "html",
			Filename:    "pending.png",
			Extension:   "png",
			Mime:        "application/javascript",
			Size:        128,
			DriveSynced: true,
			DriveMode:   "drive_api",
			DriveFileID: "", // triggers Branch 3 on-demand SyncDriveOutputs
		},
	}
	h.ctrl.mu.Unlock()

	meta2, err := h.ctrl.ResolveOutputMediaMeta(context.Background(), driveID)
	if err != nil {
		t.Fatalf("ResolveOutputMediaMeta drive branch failed: %v", err)
	}
	if meta2.Mime != "video/mp4" {
		t.Fatalf("drive branch expected canonical video/mp4, got %q", meta2.Mime)
	}

	// Branch 3: Post-sync driveOutputs branch must also return canonical image/png.
	meta3, err := h.ctrl.ResolveOutputMediaMeta(context.Background(), postSyncID)
	if err != nil {
		t.Fatalf("ResolveOutputMediaMeta post-sync branch failed: %v", err)
	}
	if meta3.Mime != "image/png" {
		t.Fatalf("post-sync branch expected canonical image/png, got %q", meta3.Mime)
	}

	// Stream client must refuse HTTP 302 redirect and never contact redirectTarget.
	req := httptest.NewRequest(http.MethodGet, "/media/outputs/"+driveID, nil)
	req.Header.Set("Range", "bytes=0-15")
	rec := httptest.NewRecorder()
	h.ctrl.ServeOutputMediaHTTP(context.Background(), rec, req, driveID)
	if redirectTargetHit {
		t.Fatal("driveStreamHTTPClient followed a 302 redirect")
	}
	if rec.Code == http.StatusOK || rec.Code == http.StatusPartialContent {
		t.Fatalf("expected error status on 302 redirect from Drive stream, got %d", rec.Code)
	}
}
