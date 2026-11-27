package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

type testMetaCaptureWriter struct {
	bytes.Buffer
	metaCalls int
	gotID     string
	gotName   string
	gotExt    string
	gotMime   string
	gotSize   int64
}

func (w *testMetaCaptureWriter) SetColabOneOutputMeta(id, filename, ext, mime string, size int64) {
	w.metaCalls++
	w.gotID = id
	w.gotName = filename
	w.gotExt = ext
	w.gotMime = mime
	w.gotSize = size
}

func TestOutputMediaSanitizeErrorsAndDriveOfflineServing(t *testing.T) {
	driveOKID := "10101010101010101010101010101010"
	driveErrID := "20202020202020202020202020202020"
	driveFSID := "30303030303030303030303030303030"
	missingID := "40404040404040404040404040404040"

	payload := bytes.Repeat([]byte("DRIVE-OFFLINE-SAN-MEDIA!"), 1024) // 24 KiB

	mockDrive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/files/file-ok" && r.URL.Query().Get("alt") == "media" {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
			return
		}
		if r.URL.Path == "/files/file-500" && r.URL.Query().Get("alt") == "media" {
			http.Error(w, "googleapis.com internal error /home/secret/path", http.StatusInternalServerError)
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
	_ = h.vault.Set("colab", map[string]any{"token": "tok-san", "refresh_token": "rt-san"})
	_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-san", "email": "san@example.com"})

	// GPU is completely offline!
	h.ctrl.Update(map[string]any{
		"runtime":       map[string]any{"state": "stopped"},
		"comfy_outputs": []any{},
	})
	h.ctrl.mu.Lock()
	h.ctrl.driveOutputs["sub:sub-san"] = map[string]*ColabOneDriveOutputRecord{
		driveOKID: {
			ID:          driveOKID,
			Kind:        "video",
			Filename:    "offline_ok.mp4",
			Extension:   "mp4",
			Mime:        "video/mp4",
			Size:        int64(len(payload)),
			DriveSynced: true,
			DriveMode:   "drive_api",
			DriveFileID: "file-ok",
		},
		driveErrID: {
			ID:          driveErrID,
			Kind:        "video",
			Filename:    "offline_err.mp4",
			Extension:   "mp4",
			Mime:        "video/mp4",
			Size:        4096,
			DriveSynced: true,
			DriveMode:   "drive_api",
			DriveFileID: "file-500",
		},
		driveFSID: {
			ID:          driveFSID,
			Kind:        "video",
			Filename:    "drivefs_only.mp4",
			Extension:   "mp4",
			Mime:        "video/mp4",
			Size:        2048,
			DriveSynced: true,
			DriveMode:   "colab_drivefs",
		},
	}
	h.ctrl.mu.Unlock()

	sanOpts := ColabOneOutputMediaOptions{RuntimeOnly: false, SanitizeErrors: true}

	// 1) Drive-backed output MUST still be served seamlessly when GPU is offline and SanitizeErrors is true.
	reqOK := httptest.NewRequest(http.MethodGet, "/api/gallery/outputs/"+driveOKID+"/media", nil)
	recOK := httptest.NewRecorder()
	h.ctrl.ServeOutputMediaHTTPWithOptions(context.Background(), recOK, reqOK, driveOKID, sanOpts)
	if recOK.Code != http.StatusOK || !bytes.Equal(recOK.Body.Bytes(), payload) {
		t.Fatalf("SanitizeErrors offline Drive expected 200 with exact payload, got code=%d len=%d body=%s", recOK.Code, recOK.Body.Len(), recOK.Body.String())
	}

	// 2) Upstream Drive 500 error MUST be sanitized (no Google Drive or HTTP 500 or host path leak).
	req500 := httptest.NewRequest(http.MethodGet, "/api/gallery/outputs/"+driveErrID+"/media", nil)
	rec500 := httptest.NewRecorder()
	h.ctrl.ServeOutputMediaHTTPWithOptions(context.Background(), rec500, req500, driveErrID, sanOpts)
	body500 := rec500.Body.String()
	if rec500.Code != http.StatusBadGateway || !strings.Contains(body500, "作品暂离线或暂不可访问，请稍后再试") {
		t.Fatalf("expected sanitized 502 error, got %d body=%s", rec500.Code, body500)
	}
	if strings.Contains(body500, "Google") || strings.Contains(body500, "云盘") || strings.Contains(body500, "500") || strings.Contains(body500, "/home/") {
		t.Fatalf("raw upstream Drive error leaked in SanitizeErrors response: %s", body500)
	}

	// 3) Missing output returns 404 with clean sanitized Chinese message.
	req404 := httptest.NewRequest(http.MethodGet, "/api/gallery/outputs/"+missingID+"/media", nil)
	rec404 := httptest.NewRecorder()
	h.ctrl.ServeOutputMediaHTTPWithOptions(context.Background(), rec404, req404, missingID, sanOpts)
	if rec404.Code != http.StatusNotFound || !strings.Contains(rec404.Body.String(), "作品不存在") {
		t.Fatalf("expected sanitized 404 error, got %d body=%s", rec404.Code, rec404.Body.String())
	}

	// 4) DriveFS-only offline output without direct read returns sanitized 503 (never mentions Google Drive).
	h.auth.driveScope = false
	reqDFS := httptest.NewRequest(http.MethodGet, "/api/gallery/outputs/"+driveFSID+"/media", nil)
	recDFS := httptest.NewRecorder()
	h.ctrl.ServeOutputMediaHTTPWithOptions(context.Background(), recDFS, reqDFS, driveFSID, sanOpts)
	bodyDFS := recDFS.Body.String()
	if recDFS.Code != http.StatusServiceUnavailable || !strings.Contains(bodyDFS, "该作品暂离线，请稍后再试") {
		t.Fatalf("expected sanitized 503 for DriveFS offline, got %d body=%s", recDFS.Code, bodyDFS)
	}
	if strings.Contains(bodyDFS, "Google") || strings.Contains(bodyDFS, "云盘") {
		t.Fatalf("DriveFS sanitized response must never leak Google Drive wording: %s", bodyDFS)
	}
}

func TestStreamOutputMediaToWriterDriveRuntimeAndFallback(t *testing.T) {
	driveID := "51515151515151515151515151515151"
	runtimeID := "62626262626262626262626262626262"
	missingID := "73737373737373737373737373737373"

	drivePayload := make([]byte, 128<<10) // 128 KiB (4 x 32 KiB chunks)
	for i := range drivePayload {
		drivePayload[i] = byte((i*17 + 9) % 251)
	}
	// > 512 KiB so runtime streaming exercises multiple sequential RPC chunks!
	runtimePayload := make([]byte, (700<<10)+13)
	for i := range runtimePayload {
		runtimePayload[i] = byte((i*29 + 5) % 241)
	}

	var (
		mu            sync.Mutex
		failDriveOnce bool
	)
	mockDrive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/files/file-stream-drive" && r.URL.Query().Get("alt") == "media" {
			mu.Lock()
			fail := failDriveOnce
			failDriveOnce = false
			mu.Unlock()
			if fail {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(drivePayload)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(drivePayload)
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
	_ = h.vault.Set("colab", map[string]any{"token": "tok-writer", "refresh_token": "rt-writer"})
	_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-writer", "email": "writer@example.com"})

	h.ctrl.mu.Lock()
	h.ctrl.driveOutputs["sub:sub-writer"] = map[string]*ColabOneDriveOutputRecord{
		driveID: {
			ID:          driveID,
			Kind:        "image",
			Filename:    "drive_art.png",
			Extension:   "png",
			Mime:        "image/png",
			Size:        int64(len(drivePayload)),
			DriveSynced: true,
			DriveMode:   "drive_api",
			DriveFileID: "file-stream-drive",
		},
	}
	h.ctrl.mu.Unlock()

	// 1) Offline Drive streaming via StreamOutputMediaToWriter with SanitizeErrors: true.
	h.ctrl.Update(map[string]any{
		"runtime":       map[string]any{"state": "stopped"},
		"comfy_outputs": []any{},
	})
	var driveDst testMetaCaptureWriter
	metaDrive, err := h.ctrl.StreamOutputMediaToWriter(context.Background(), driveID, &driveDst, ColabOneOutputMediaOptions{SanitizeErrors: true})
	if err != nil {
		t.Fatalf("Drive StreamOutputMediaToWriter failed: %v", err)
	}
	if metaDrive.Source != "drive" || metaDrive.Extension != "png" || !bytes.Equal(driveDst.Bytes(), drivePayload) {
		t.Fatalf("Drive stream mismatch: meta=%+v len=%d", metaDrive, driveDst.Len())
	}
	if driveDst.metaCalls != 1 || driveDst.gotExt != "png" || driveDst.gotMime != "image/png" {
		t.Fatalf("expected SetColabOneOutputMeta called once, got calls=%d ext=%q mime=%q", driveDst.metaCalls, driveDst.gotExt, driveDst.gotMime)
	}

	// 2) Missing ID with SanitizeErrors preserves errors.Is(err, ErrNotFound) and sanitizes text.
	var emptyDst bytes.Buffer
	if _, err := h.ctrl.StreamOutputMediaToWriter(context.Background(), missingID, &emptyDst, ColabOneOutputMediaOptions{SanitizeErrors: true}); err == nil || !errors.Is(err, ErrNotFound) || err.Error() != "作品不存在" {
		t.Fatalf("expected sanitized ErrNotFound, got %v", err)
	}
	if emptyDst.Len() != 0 {
		t.Fatalf("expected 0 bytes written on missing ID, got %d", emptyDst.Len())
	}

	// 3) Multi-chunk runtime streaming (> 512 KiB) + Drive 404 pre-first-byte fallback to runtime!
	h.provider.addManagedSession("ep-writer", "A100", "STANDARD", true)
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		if strings.HasPrefix(path, "comfyui/outputs/") && strings.Contains(path, "/asset?offset=") {
			parts := strings.Split(path, "offset=")
			off, _ := strconv.Atoi(parts[1])
			src := runtimePayload
			ext := "mp4"
			mime := "video/mp4"
			if strings.Contains(path, driveID) {
				src = drivePayload
				ext = "png"
				mime = "image/png"
			}
			end := off + int(colabOneMediaRuntimeChunkBytes)
			if end > len(src) {
				end = len(src)
			}
			return map[string]any{
				"data":      base64.StdEncoding.EncodeToString(src[off:end]),
				"offset":    off,
				"size":      len(src),
				"mime":      mime,
				"extension": ext,
			}, nil
		}
		return map[string]any{"ok": true}, nil
	}
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{"state": "ready", "id": "ep-writer"},
		"comfy_outputs": []any{
			map[string]any{
				"id":        runtimeID,
				"kind":      "video",
				"filename":  "multi_chunk.mp4",
				"extension": "mp4",
				"mime":      "video/mp4",
				"size":      len(runtimePayload),
			},
			map[string]any{
				"id":        driveID,
				"kind":      "image",
				"filename":  "drive_art.png",
				"extension": "png",
				"mime":      "image/png",
				"size":      len(drivePayload),
			},
		},
	})

	var rtDst testMetaCaptureWriter
	metaRT, err := h.ctrl.StreamOutputMediaToWriter(context.Background(), runtimeID, &rtDst, ColabOneOutputMediaOptions{})
	if err != nil {
		t.Fatalf("Runtime StreamOutputMediaToWriter failed: %v", err)
	}
	if metaRT.Source != "runtime" || metaRT.Size != int64(len(runtimePayload)) || !bytes.Equal(rtDst.Bytes(), runtimePayload) {
		t.Fatalf("Runtime multi-chunk stream mismatch: meta=%+v len=%d", metaRT, rtDst.Len())
	}

	// Cached Drive copy serves instantaneously even when Drive would return 404.
	mu.Lock()
	failDriveOnce = true
	mu.Unlock()
	var cachedDst testMetaCaptureWriter
	metaCached, err := h.ctrl.StreamOutputMediaToWriter(context.Background(), driveID, &cachedDst, ColabOneOutputMediaOptions{SanitizeErrors: true})
	if err != nil || metaCached.Source != "drive" || !bytes.Equal(cachedDst.Bytes(), drivePayload) {
		t.Fatalf("expected cached Drive hit before eviction, got meta=%+v err=%v len=%d", metaCached, err, cachedDst.Len())
	}

	// Evict local cache -> Drive returns 404 before first byte and seamlessly falls back to live runtime!
	h.ctrl.evictLocalOutputCache(driveID)
	mu.Lock()
	failDriveOnce = true
	mu.Unlock()
	var fbDst testMetaCaptureWriter
	metaFB, err := h.ctrl.StreamOutputMediaToWriter(context.Background(), driveID, &fbDst, ColabOneOutputMediaOptions{SanitizeErrors: true})
	if err != nil {
		t.Fatalf("Drive fallback StreamOutputMediaToWriter failed: %v", err)
	}
	if metaFB.Source != "runtime" || !bytes.Equal(fbDst.Bytes(), drivePayload) {
		t.Fatalf("expected Drive fallback to runtime with exact payload, got meta=%+v len=%d", metaFB, fbDst.Len())
	}
}

type failingSentinelWriter struct {
	err error
}

func (f failingSentinelWriter) Write([]byte) (int, error) {
	return 0, f.err
}

func TestOutputMediaHardeningFixes(t *testing.T) {
	// 1) isNotFoundError rejects transport/context errors and bare "404".
	if isNotFoundError(&url.Error{Op: "Get", URL: "https://example.invalid/404", Err: errors.New("404")}) {
		t.Fatal("isNotFoundError must reject *url.Error")
	}
	if isNotFoundError(context.Canceled) || isNotFoundError(context.DeadlineExceeded) {
		t.Fatal("isNotFoundError must reject context cancellation/deadline")
	}
	if isNotFoundError(errors.New("task 404999 failed")) {
		t.Fatal("isNotFoundError must reject bare 404 substring")
	}
	if !isNotFoundError(ErrNotFound) || !isNotFoundError(&ColabOneHTTPError{StatusCode: http.StatusNotFound, Message: "missing"}) {
		t.Fatal("isNotFoundError must accept ErrNotFound and HTTP 404")
	}

	// 2) StreamOutputMediaToWriter preserves caller w.Write error untouched even with SanitizeErrors: true.
	h := newTestHarness(t, false)
	outID := "91919191919191919191919191919191"
	payload := []byte("HELLO-WRITER-ERROR")
	h.provider.addManagedSession("ep-wr", "A100", "STANDARD", true)
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		return map[string]any{
			"data":      base64.StdEncoding.EncodeToString(payload),
			"offset":    0,
			"size":      len(payload),
			"extension": "png",
		}, nil
	}
	h.ctrl.Update(map[string]any{
		"runtime":       map[string]any{"state": "ready", "id": "ep-wr"},
		"comfy_outputs": []any{map[string]any{"id": outID, "extension": "png", "mime": "image/png", "size": len(payload)}},
	})
	callerSentinel := errors.New("caller-zip-limit-sentinel")
	if _, err := h.ctrl.StreamOutputMediaToWriter(context.Background(), outID, failingSentinelWriter{err: callerSentinel}, ColabOneOutputMediaOptions{SanitizeErrors: true}); !errors.Is(err, callerSentinel) {
		t.Fatalf("expected caller writer error preserved, got %v", err)
	}

	// 3) streamDriveOutputToWriter rejects HTTP 206 Partial Content and truncated ContentLength.
	mode := "206"
	mockDrive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == "206" {
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("partial"))
			return
		}
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("short"))
	}))
	defer mockDrive.Close()

	h2 := newTestHarness(t, false)
	h2.ctrl.driveAPIBaseURL = mockDrive.URL
	h2.ctrl.driveHTTPClient = mockDrive.Client()
	h2.auth.driveScope = true
	_ = h2.vault.Set("colab", map[string]any{"token": "tok"})
	meta := &ColabOneOutputMediaMeta{ID: outID, DriveFileID: "fid-trunc", Size: 100, Extension: "png", Mime: "image/png"}
	var dst bytes.Buffer
	if _, _, err := h2.ctrl.streamDriveOutputToWriter(context.Background(), &dst, meta); err == nil {
		t.Fatal("expected streamDriveOutputToWriter to reject HTTP 206")
	}
	mode = "trunc"
	dst.Reset()
	if _, _, err := h2.ctrl.streamDriveOutputToWriter(context.Background(), &dst, meta); err == nil {
		t.Fatal("expected streamDriveOutputToWriter to reject truncated body")
	}

	// 4) ResolveOutputMediaMetaWithOptions: on-demand sync deleted -> ErrNotFound, error cooldown, and non-404 runtime fallback propagation.
	var syncHits atomic.Int32
	syncMode := "empty"
	mockDrive2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		syncHits.Add(1)
		if syncMode == "error" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"files":[]}`))
	}))
	defer mockDrive2.Close()

	h3 := newTestHarness(t, false)
	h3.ctrl.driveAPIBaseURL = mockDrive2.URL
	h3.ctrl.driveHTTPClient = mockDrive2.Client()
	h3.auth.driveScope = true
	_ = h3.vault.Set("colab", map[string]any{"token": "tok"})
	_ = h3.vault.Set("google_profile", map[string]any{"sub": "sub-od"})
	h3.ctrl.mu.Lock()
	h3.ctrl.driveOutputs["sub:sub-od"] = map[string]*ColabOneDriveOutputRecord{
		outID: {ID: outID, Extension: "png", DriveSynced: true, DriveMode: "drive_api", Size: 10},
	}
	h3.ctrl.mu.Unlock()
	h3.ctrl.Update(map[string]any{"runtime": map[string]any{"state": "stopped"}, "comfy_outputs": []any{}})
	if _, err := h3.ctrl.ResolveOutputMediaMetaWithOptions(context.Background(), outID, ColabOneOutputMediaOptions{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after on-demand sync removed record, got %v", err)
	}

	// Cooldown prevents hammering Drive after sync failure.
	syncMode = "error"
	syncHits.Store(0)
	h3.ctrl.mu.Lock()
	h3.ctrl.driveOutputsLastSynced = 0
	h3.ctrl.driveOutputsLastSyncAttempt = 0
	h3.ctrl.driveOutputs["sub:sub-od"] = map[string]*ColabOneDriveOutputRecord{
		outID: {ID: outID, Extension: "png", DriveSynced: true, DriveMode: "drive_api", Size: 10},
	}
	h3.ctrl.mu.Unlock()
	if _, err := h3.ctrl.ResolveOutputMediaMetaWithOptions(context.Background(), outID, ColabOneOutputMediaOptions{}); err == nil {
		t.Fatal("expected sync error")
	}
	hits1 := syncHits.Load()
	_, _ = h3.ctrl.ResolveOutputMediaMetaWithOptions(context.Background(), outID, ColabOneOutputMediaOptions{})
	if syncHits.Load() != hits1 {
		t.Fatalf("expected sync-attempt cooldown to prevent repeated Drive hits (%d vs %d)", hits1, syncHits.Load())
	}

	// Non-404 error on runtime fallback must propagate instead of returning ErrNotFound.
	h3.provider.addManagedSession("ep-err", "T4", "STANDARD", true)
	h3.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		return nil, &ColabOneHTTPError{StatusCode: http.StatusServiceUnavailable, Message: "upstream busy"}
	}
	h3.ctrl.Update(map[string]any{"runtime": map[string]any{"state": "ready", "id": "ep-err"}, "comfy_outputs": []any{}})
	_, err := h3.ctrl.ResolveOutputMediaMetaWithOptions(context.Background(), "92929292929292929292929292929292", ColabOneOutputMediaOptions{RuntimeOnly: true})
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("expected non-404 runtime fallback error propagated, got %v", err)
	}

	// 5) HEAD X-ColabOne-Error uses PathEscape (spaces encoded as %20, not '+').
	headReq := httptest.NewRequest(http.MethodHead, "/outputs/bad", nil)
	headRec := httptest.NewRecorder()
	writeOutputMediaError(headRec, headReq, http.StatusBadRequest, "bad id value")
	rawHdr := headRec.Header().Get("X-ColabOne-Error")
	if strings.Contains(rawHdr, "+") {
		t.Fatalf("expected url.PathEscape without '+', got %q", rawHdr)
	}
	unesc, uErr := url.PathUnescape(rawHdr)
	if uErr != nil || unesc != "bad id value" {
		t.Fatalf("unexpected unescaped X-ColabOne-Error: %q err=%v", unesc, uErr)
	}

	// 6) driveStreamClient does not cache after Close().
	h4 := newTestHarness(t, false)
	h4.ctrl.Close()
	_ = h4.ctrl.driveStreamClient()
	h4.ctrl.mu.Lock()
	cachedClient := h4.ctrl.driveStreamHTTPClient
	h4.ctrl.mu.Unlock()
	if cachedClient != nil {
		t.Fatal("expected driveStreamHTTPClient to remain nil after Close()")
	}
}

type testErrReader struct {
	err error
}

func (r testErrReader) Read([]byte) (int, error) { return 0, r.err }

type testFailWriter struct {
	err error
}

func (w testFailWriter) Write([]byte) (int, error) { return 0, w.err }

func TestOutputMediaAuditHardening(t *testing.T) {
	// 1) A1 M1: copyCacheFileToWriter checks ctx, truncation, separates destErr vs readErr.
	meta := &ColabOneOutputMediaMeta{ID: "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1", Size: 10, Filename: "a.png", Extension: "png", Mime: "image/png"}
	if destErr, readErr := copyCacheFileToWriter(context.Background(), strings.NewReader("short"), io.Discard, meta, 10); destErr != nil || readErr == nil {
		t.Fatalf("expected readErr on truncated cache read, destErr=%v readErr=%v", destErr, readErr)
	}
	pathErr := &os.PathError{Op: "read", Path: "/home/secret/output-cache/a.bin", Err: errors.New("io error")}
	destErr, readErr := copyCacheFileToWriter(context.Background(), testErrReader{err: pathErr}, io.Discard, meta, 10)
	if destErr != nil || !errors.Is(readErr, pathErr) {
		t.Fatalf("expected readErr=pathErr, destErr=%v readErr=%v", destErr, readErr)
	}
	sanitized := sanitizeOutputMediaError(readErr, ColabOneOutputMediaOptions{SanitizeErrors: true})
	if strings.Contains(sanitized.Error(), "/home/secret") {
		t.Fatalf("sanitized readErr leaked disk path: %q", sanitized.Error())
	}
	sentinel := errors.New("zip limit sentinel")
	destErr, readErr = copyCacheFileToWriter(context.Background(), strings.NewReader("0123456789"), testFailWriter{err: sentinel}, meta, 10)
	if !errors.Is(destErr, sentinel) || readErr != nil {
		t.Fatalf("expected destErr preserved untouched, destErr=%v readErr=%v", destErr, readErr)
	}

	// 2) A2 L3: writeOutputMediaError encodes literal '+' as '%2B'.
	headReq := httptest.NewRequest(http.MethodHead, "/outputs/bad", nil)
	headRec := httptest.NewRecorder()
	writeOutputMediaError(headRec, headReq, http.StatusBadRequest, "a+b error")
	rawHdr := headRec.Header().Get("X-ColabOne-Error")
	if strings.Contains(rawHdr, "+") || !strings.Contains(rawHdr, "%2B") {
		t.Fatalf("expected literal '+' encoded as %%2B, got %q", rawHdr)
	}

	// 3) A3 H1: isNotFoundError trusts explicit HTTP status code over body text.
	if isNotFoundError(&ColabOneHTTPError{StatusCode: http.StatusInternalServerError, Message: "upstream resource not found / 资源不存在"}) {
		t.Fatal("expected HTTP 500 with 'not found' text to NOT match isNotFoundError")
	}
	if !isNotFoundError(&ColabOneHTTPError{StatusCode: http.StatusNotFound, Message: "missing"}) {
		t.Fatal("expected HTTP 404 to match isNotFoundError")
	}

	// 4) A1 L3: streamDriveOutputMedia rejects HTTP 206 when !isRange.
	srv206 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-3/10")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("abcd"))
	}))
	defer srv206.Close()
	h := newTestHarness(t, false)
	h.ctrl.driveAPIBaseURL = srv206.URL
	h.ctrl.driveHTTPClient = srv206.Client()
	h.auth.driveScope = true
	rec := httptest.NewRecorder()
	wrote, err := h.ctrl.streamDriveOutputMedia(context.Background(), rec, &ColabOneOutputMediaMeta{
		ID: "b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1", Size: 10, DriveFileID: "fid-206", Mime: "image/png", Filename: "x.png",
	}, 0, 9, false)
	if wrote || err == nil {
		t.Fatalf("expected non-range 206 response rejected, wrote=%v err=%v", wrote, err)
	}

	// 5) A1 M3: Cancelled ctx during Drive stream must NOT fall back to runtime.
	driveBlockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer driveBlockSrv.Close()
	h2 := newTestHarness(t, false)
	h2.ctrl.driveAPIBaseURL = driveBlockSrv.URL
	h2.ctrl.driveHTTPClient = driveBlockSrv.Client()
	h2.auth.driveScope = true
	_ = h2.vault.Set("colab", map[string]any{"token": "tok-c"})
	_ = h2.vault.Set("google_profile", map[string]any{"sub": "sub-c"})
	oid := "c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1"
	h2.ctrl.mu.Lock()
	h2.ctrl.driveOutputs["sub:sub-c"] = map[string]*ColabOneDriveOutputRecord{
		oid: {
			ID: oid, Kind: "image", Filename: "c.png", Extension: "png", Mime: "image/png",
			Size: 20, DriveSynced: true, DriveMode: "drive_api", DriveFileID: "fid-c",
		},
	}
	h2.ctrl.mu.Unlock()
	var rtHits atomic.Int32
	h2.provider.addManagedSession("ep-nofb", "L4", "STANDARD", true)
	h2.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		rtHits.Add(1)
		return nil, errors.New("should not be called")
	}
	h2.ctrl.Update(map[string]any{
		"runtime":       map[string]any{"state": "ready", "id": "ep-nofb"},
		"comfy_outputs": []any{map[string]any{"id": oid, "extension": "png", "mime": "image/png", "size": 20}},
	})
	cancelCtx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)
	if _, err := h2.ctrl.StreamOutputMediaToWriter(cancelCtx, oid, io.Discard, ColabOneOutputMediaOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled without runtime fallback, got %v", err)
	}
	if rtHits.Load() != 0 {
		t.Fatalf("expected 0 runtime fallback RPCs on cancelled ctx, got %d", rtHits.Load())
	}
}
