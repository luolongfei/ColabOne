package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestHarnessWithDir(t *testing.T, cfgDir string, background bool) *testHarness {
	t.Helper()
	vault := NewColabOneMemoryVault()
	auth := newMockAuth()
	provider := newMockProvider(vault)
	models := &mockModels{}
	hub := &mockHub{}
	proxy := &mockAssignProxy{}
	shortcuts := &mockShortcuts{}
	bundler := defaultMockBundler()

	ctrl := NewColabOneController(ColabOneControllerConfig{
		Root:               cfgDir,
		ConfigDir:          cfgDir,
		Background:         background,
		Vault:              vault,
		Auth:               auth,
		Provider:           provider,
		Hub:                hub,
		Models:             models,
		Comfy:              mockComfy{},
		PublicAPI:          mockPublicAPI{},
		AssignProxy:        proxy,
		Shortcuts:          shortcuts,
		ServiceBundler:     bundler,
		Location:           mockLocation{},
		MonitorInterval:    15 * time.Millisecond,
		ObserverInterval:   15 * time.Millisecond,
		CanvasWaitInterval: 10 * time.Millisecond,
		UpgradePollDelay:   10 * time.Millisecond,
		StartRetryDelay:    10 * time.Millisecond,
	})
	t.Cleanup(func() { ctrl.Close() })
	return &testHarness{
		ctrl:        ctrl,
		vault:       vault,
		auth:        auth,
		provider:    provider,
		models:      models,
		hub:         hub,
		assignProxy: proxy,
		shortcuts:   shortcuts,
		bundler:     bundler,
		configDir:   cfgDir,
	}
}

func TestOutputCacheLRUEvictionSizeCapAndPersistenceAcrossReload(t *testing.T) {
	cfgDir := t.TempDir()
	h := newTestHarnessWithDir(t, cfgDir, false)
	h.ctrl.outputCacheMaxBytes = 200
	h.ctrl.outputCacheMaxEntries = 3
	h.ctrl.outputCacheMaxFileBytes = 120

	idA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1"
	idB := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb2"
	idC := "ccccccccccccccccccccccccccccccc3"
	idD := "ddddddddddddddddddddddddddddddd4"
	idTooBig := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeee5"

	commit := func(id string, payload []byte) bool {
		dir := h.ctrl.outputCacheDir()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		tmp, err := os.CreateTemp(dir, ".test-*.tmp")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tmp.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err := tmp.Close(); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(payload)
		ok, err := h.ctrl.commitTempToOutputCache(id, tmp.Name(), int64(len(payload)), hex.EncodeToString(sum[:]), "png", id+".png", "image/png", 0)
		if err != nil && !errors.Is(err, errOutputCacheSkipped) {
			t.Fatalf("commitTempToOutputCache(%s) error: %v", id, err)
		}
		return ok
	}

	// File above maxFileBytes is skipped.
	if h.ctrl.canCacheOutputSize(121) {
		t.Fatal("expected size > maxFileBytes to be non-cacheable")
	}
	if commit(idTooBig, bytes.Repeat([]byte("X"), 121)) {
		t.Fatal("expected commit > maxFileBytes to be skipped")
	}

	// Commit A (70B), B (70B) -> total 140B <= 200B.
	if !commit(idA, bytes.Repeat([]byte("A"), 70)) {
		t.Fatal("commit A failed")
	}
	time.Sleep(5 * time.Millisecond)
	if !commit(idB, bytes.Repeat([]byte("B"), 70)) {
		t.Fatal("commit B failed")
	}
	time.Sleep(5 * time.Millisecond)

	// Touch A so B becomes least recently used.
	fA, _, hitA := h.ctrl.openLocalOutputCacheFile(idA, 70)
	if !hitA {
		t.Fatal("expected cache hit on A")
	}
	_ = fA.Close()
	time.Sleep(5 * time.Millisecond)

	// Commit C (70B) -> total 210B > 200B -> must evict B (LRU), keeping A and C (140B).
	if !commit(idC, bytes.Repeat([]byte("C"), 70)) {
		t.Fatal("commit C failed")
	}
	if f, _, hit := h.ctrl.openLocalOutputCacheFile(idB, 70); hit {
		_ = f.Close()
		t.Fatal("expected B to be evicted by byte LRU cap")
	}
	if f, _, hit := h.ctrl.openLocalOutputCacheFile(idA, 70); !hit {
		t.Fatal("expected A to remain cached after LRU touch")
	} else {
		_ = f.Close()
	}

	// Entry-count eviction: shrink maxEntries to 2 and commit D (40B) -> evicts C or A so total entries <= 2.
	h.ctrl.outputCacheMaxEntries = 2
	if !commit(idD, bytes.Repeat([]byte("D"), 40)) {
		t.Fatal("commit D failed")
	}
	h.ctrl.outputCacheMu.Lock()
	entryCount := len(h.ctrl.outputCacheIndex.Entries)
	h.ctrl.outputCacheMu.Unlock()
	if entryCount > 2 {
		t.Fatalf("expected <= 2 entries after count cap, got %d", entryCount)
	}

	// Flush index via Close() and reload with a fresh controller pointing at the same ConfigDir.
	h.ctrl.Close()
	h2 := newTestHarnessWithDir(t, cfgDir, false)
	h2.ctrl.outputCacheMaxBytes = 200
	h2.ctrl.outputCacheMaxEntries = 3
	h2.ctrl.outputCacheMaxFileBytes = 120
	fD, entryD, hitD := h2.ctrl.openLocalOutputCacheFile(idD, 40)
	if !hitD || entryD == nil || entryD.Size != 40 {
		t.Fatalf("expected persisted cache hit on D after reload, hit=%v entry=%+v", hitD, entryD)
	}
	_ = fD.Close()
}

func TestOutputCacheSymlinkAndIntegrityRejection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	cfgDir := t.TempDir()
	h := newTestHarnessWithDir(t, cfgDir, false)
	cacheDir := h.ctrl.outputCacheDir()
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// 1) Invalid IDs are rejected cleanly.
	for _, bad := range []string{"", "../escape", "ABCDEF0123456789abcdef0123456789", "short"} {
		if f, _, hit := h.ctrl.openLocalOutputCacheFile(bad, 10); hit {
			_ = f.Close()
			t.Fatalf("expected invalid ID %q to be rejected", bad)
		}
	}

	// 2) Symlink .bin file is rejected on open and commit.
	symID := "1111111111111111111111111111111a"
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("top-secret-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cacheDir, symID+".bin")); err != nil {
		t.Fatal(err)
	}
	h.ctrl.outputCacheMu.Lock()
	idx := h.ctrl.loadLocalOutputCacheIndexLocked()
	idx.Entries[symID] = &colabOneOutputCacheEntry{ID: symID, Size: 15, LastAccess: nowUnixFloat()}
	h.ctrl.outputCacheMu.Unlock()
	if f, _, hit := h.ctrl.openLocalOutputCacheFile(symID, 15); hit {
		_ = f.Close()
		t.Fatal("expected symlink .bin to be rejected on open")
	}

	tmp, err := os.CreateTemp(cacheDir, ".symtest-*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = tmp.Write([]byte("new-data"))
	_ = tmp.Close()
	if ok, _ := h.ctrl.commitTempToOutputCache(symID, tmp.Name(), 8, "", "png", "a.png", "image/png", 0); ok {
		t.Fatal("expected commit over symlink .bin to be rejected")
	}
	// Verify outside symlink target was NOT overwritten!
	gotOutside, err := os.ReadFile(outside)
	if err != nil || string(gotOutside) != "top-secret-data" {
		t.Fatalf("outside symlink target was modified: %q err=%v", string(gotOutside), err)
	}

	// 3) Size mismatch on disk invalidates and evicts cached file.
	mismatchID := "2222222222222222222222222222222b"
	mismatchPath := filepath.Join(cacheDir, mismatchID+".bin")
	if err := os.WriteFile(mismatchPath, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.ctrl.outputCacheMu.Lock()
	idx = h.ctrl.loadLocalOutputCacheIndexLocked()
	idx.Entries[mismatchID] = &colabOneOutputCacheEntry{ID: mismatchID, Size: 5, LastAccess: nowUnixFloat()}
	h.ctrl.outputCacheMu.Unlock()
	if f, _, hit := h.ctrl.openLocalOutputCacheFile(mismatchID, 999); hit {
		_ = f.Close()
		t.Fatal("expected size mismatch to reject cache entry")
	}
	if _, err := os.Stat(mismatchPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected mismatched cache file to be evicted, stat err=%v", err)
	}

	// 4) Symlink output-cache directory is skipped safely without erroring.
	cfgDir2 := t.TempDir()
	h2 := newTestHarnessWithDir(t, cfgDir2, false)
	realDir := t.TempDir()
	if err := os.Symlink(realDir, h2.ctrl.outputCacheDir()); err != nil {
		t.Fatal(err)
	}
	if f, _, hit := h2.ctrl.openLocalOutputCacheFile(mismatchID, 0); hit {
		_ = f.Close()
		t.Fatal("expected symlink output-cache dir to be rejected")
	}
}

func TestOutputCacheSingleFlightCoalescingAndPartialCancel(t *testing.T) {
	h := newTestHarness(t, false)
	outID := "3333333333333333333333333333333c"
	payload := bytes.Repeat([]byte("COALESCED-BYTES-"), 128)
	sum := sha256.Sum256(payload)
	shaHex := hex.EncodeToString(sum[:])

	var rpcCalls atomic.Int32
	gate := make(chan struct{})
	started := make(chan struct{}, 1)

	h.provider.addManagedSession("ep-sf", "A100", "STANDARD", true)
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		rpcCalls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return map[string]any{
			"data":      base64.StdEncoding.EncodeToString(payload),
			"offset":    0,
			"size":      len(payload),
			"sha256":    shaHex,
			"extension": "png",
			"filename":  "coalesced.png",
		}, nil
	}
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{"state": "ready", "id": "ep-sf"},
		"comfy_outputs": []any{
			map[string]any{"id": outID, "extension": "png", "mime": "image/png", "size": len(payload), "filename": "coalesced.png"},
		},
	})

	// 1) 4 concurrent requests coalesce into 1 upstream fetch; cancelling 1 waiter does not abort the others!
	ctxCancel, cancelOne := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	errs := make([]error, 4)
	bodies := make([][]byte, 4)

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			reqCtx := context.Background()
			if idx == 0 {
				reqCtx = ctxCancel
			}
			var buf bytes.Buffer
			_, err := h.ctrl.StreamOutputMediaToWriter(reqCtx, outID, &buf, ColabOneOutputMediaOptions{})
			errs[idx] = err
			bodies[idx] = buf.Bytes()
		}(i)
	}

	// Wait for the single upstream RPC to start and all 4 waiters to attach.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for upstream RPC")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.ctrl.outputCacheMu.Lock()
		fl := h.ctrl.outputCacheInflight[outputCacheFlightKey("runtime", outID)]
		w := 0
		if fl != nil {
			w = fl.waiters
		}
		h.ctrl.outputCacheMu.Unlock()
		if w == 4 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Cancel waiter 0 ONLY, then release upstream gate.
	cancelOne()
	time.Sleep(20 * time.Millisecond)
	close(gate)
	wg.Wait()

	if !errors.Is(errs[0], context.Canceled) {
		t.Fatalf("waiter 0 expected context.Canceled, got %v", errs[0])
	}
	for i := 1; i < 4; i++ {
		if errs[i] != nil || !bytes.Equal(bodies[i], payload) {
			t.Fatalf("waiter %d expected full payload after peer cancel, err=%v len=%d", i, errs[i], len(bodies[i]))
		}
	}
	if got := rpcCalls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 upstream RPC for 4 coalesced waiters, got %d", got)
	}

	// 2) When ALL waiters cancel, the shared upstream flight context IS cancelled cleanly.
	outID2 := "4444444444444444444444444444444d"
	upstreamCancelled := make(chan struct{}, 1)
	started2 := make(chan struct{}, 1)
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		select {
		case started2 <- struct{}{}:
		default:
		}
		<-ctx.Done()
		select {
		case upstreamCancelled <- struct{}{}:
		default:
		}
		return nil, ctx.Err()
	}
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{"state": "ready", "id": "ep-sf"},
		"comfy_outputs": []any{
			map[string]any{"id": outID2, "extension": "png", "mime": "image/png", "size": 1024},
		},
	})

	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	var wg2 sync.WaitGroup
	wg2.Add(2)
	go func() {
		defer wg2.Done()
		var buf bytes.Buffer
		_, _ = h.ctrl.StreamOutputMediaToWriter(ctxA, outID2, &buf, ColabOneOutputMediaOptions{})
	}()
	go func() {
		defer wg2.Done()
		var buf bytes.Buffer
		_, _ = h.ctrl.StreamOutputMediaToWriter(ctxB, outID2, &buf, ColabOneOutputMediaOptions{})
	}()
	select {
	case <-started2:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second flight")
	}
	time.Sleep(20 * time.Millisecond)
	cancelA()
	cancelB()
	wg2.Wait()

	select {
	case <-upstreamCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("expected upstream flight context to cancel when all waiters cancelled")
	}
}

func TestOutputCacheParallelRuntimeChunkFetcherAndValidation(t *testing.T) {
	h := newTestHarness(t, false)
	outID := "5555555555555555555555555555555e"
	// 7 chunks (3.5 MiB) to exercise parallel worker pool (concurrency = 6).
	totalSize := int(colabOneMediaRuntimeChunkBytes*6 + 256*1024)
	payload := make([]byte, totalSize)
	for i := range payload {
		payload[i] = byte((i*31 + 7) & 0xff)
	}
	sum := sha256.Sum256(payload)
	shaHex := hex.EncodeToString(sum[:])

	var (
		totalRPCs     atomic.Int32
		activeRPCs    atomic.Int32
		maxConcurrent atomic.Int32
	)

	h.provider.addManagedSession("ep-par", "A100", "STANDARD", true)
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		totalRPCs.Add(1)
		cur := activeRPCs.Add(1)
		for {
			prev := maxConcurrent.Load()
			if cur <= prev || maxConcurrent.CompareAndSwap(prev, cur) {
				break
			}
		}
		defer activeRPCs.Add(-1)

		parts := strings.Split(path, "offset=")
		off, _ := strconv.Atoi(parts[1])
		if off > 0 {
			time.Sleep(25 * time.Millisecond)
		}
		end := off + int(colabOneMediaRuntimeChunkBytes)
		if end > len(payload) {
			end = len(payload)
		}
		return map[string]any{
			"data":      base64.StdEncoding.EncodeToString(payload[off:end]),
			"offset":    off,
			"size":      len(payload),
			"sha256":    shaHex,
			"extension": "mp4",
			"filename":  "parallel_clip.mp4",
		}, nil
	}
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{"state": "ready", "id": "ep-par"},
		"comfy_outputs": []any{
			map[string]any{
				"id":        outID,
				"kind":      "video",
				"filename":  "parallel_clip.mp4",
				"extension": "mp4",
				"mime":      "video/mp4",
				"size":      len(payload),
			},
		},
	})

	// 1) First HTTP GET triggers parallel 6-way fetch into local SSD cache and returns exact payload.
	req1 := httptest.NewRequest(http.MethodGet, "/outputs/"+outID, nil)
	rec1 := httptest.NewRecorder()
	h.ctrl.ServeOutputMediaHTTPWithOptions(context.Background(), rec1, req1, outID, ColabOneOutputMediaOptions{})
	if rec1.Code != http.StatusOK || !bytes.Equal(rec1.Body.Bytes(), payload) {
		t.Fatalf("first GET mismatch: code=%d len=%d want=%d", rec1.Code, rec1.Body.Len(), len(payload))
	}
	if rec1.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expected Cache-Control: no-store, got %q", rec1.Header().Get("Cache-Control"))
	}
	if peak := maxConcurrent.Load(); peak < 2 {
		t.Fatalf("expected parallel chunk fetches (peak >= 2), got peak=%d", peak)
	}
	rpcsAfterFirst := totalRPCs.Load()

	// 2) Subsequent Range GET, StreamOutputMediaToWriter, and comfyui.output.asset all hit local SSD cache with 0 RPCs!
	rangeReq := httptest.NewRequest(http.MethodGet, "/outputs/"+outID, nil)
	rangeReq.Header.Set("Range", "bytes=1000-2999")
	rangeRec := httptest.NewRecorder()
	h.ctrl.ServeOutputMediaHTTPWithOptions(context.Background(), rangeRec, rangeReq, outID, ColabOneOutputMediaOptions{})
	if rangeRec.Code != http.StatusPartialContent || !bytes.Equal(rangeRec.Body.Bytes(), payload[1000:3000]) {
		t.Fatalf("cached Range GET mismatch: code=%d len=%d", rangeRec.Code, rangeRec.Body.Len())
	}

	var streamBuf bytes.Buffer
	if _, err := h.ctrl.StreamOutputMediaToWriter(context.Background(), outID, &streamBuf, ColabOneOutputMediaOptions{}); err != nil || !bytes.Equal(streamBuf.Bytes(), payload) {
		t.Fatalf("cached StreamOutputMediaToWriter failed: err=%v len=%d", err, streamBuf.Len())
	}

	chunkRes, err := h.ctrl.Command(context.Background(), "comfyui.output.asset", map[string]any{
		"id":     outID,
		"offset": int(colabOneMediaRuntimeChunkBytes),
	})
	if err != nil {
		t.Fatalf("cached comfyui.output.asset failed: %v", err)
	}
	chunkMap := asMap(chunkRes)
	decoded, err := base64.StdEncoding.DecodeString(asString(chunkMap["data"]))
	if err != nil || !bytes.Equal(decoded, payload[colabOneMediaRuntimeChunkBytes:colabOneMediaRuntimeChunkBytes*2]) {
		t.Fatalf("cached chunk 1 mismatch: err=%v len=%d", err, len(decoded))
	}
	if asString(chunkMap["sha256"]) != shaHex {
		t.Fatalf("expected sha256 %s, got %q", shaHex, asString(chunkMap["sha256"]))
	}
	if totalRPCs.Load() != rpcsAfterFirst {
		t.Fatalf("expected 0 extra RPCs on local cache hits, got %d -> %d", rpcsAfterFirst, totalRPCs.Load())
	}

	// 3) Bad offset / size / base64 validation in fetchRuntimeOutputChunks.
	badCases := []struct {
		name string
		resp map[string]any
	}{
		{"wrong_offset", map[string]any{"offset": 99, "size": 10, "data": base64.StdEncoding.EncodeToString([]byte("0123456789"))}},
		{"bad_base64", map[string]any{"offset": 0, "size": 10, "data": "%%%not-base64%%%"}},
		{"oversized_chunk", map[string]any{"offset": 0, "size": int(colabOneMediaRuntimeChunkBytes + 10), "data": base64.StdEncoding.EncodeToString(make([]byte, colabOneMediaRuntimeChunkBytes+1))}},
	}
	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
				return tc.resp, nil
			}
			_, _, err := h.ctrl.fetchRuntimeOutputChunks(context.Background(), outID, 0, -1, func(chunk []byte, total int64, _ map[string]any) error {
				return nil
			})
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestOutputCacheRuntimeOnlyIsolationAndDeleteEviction(t *testing.T) {
	driveOnlyID := "6666666666666666666666666666666a"
	liveID := "7777777777777777777777777777777b"
	rtOnlyDelID := "8888888888888888888888888888888c"
	drivePayload := []byte("DRIVE-CACHED-PAYLOAD-PNG")
	livePayload := []byte("LIVE-CACHED-PAYLOAD-PNG")
	driveSHA := sha256.Sum256(drivePayload)
	liveSHA := sha256.Sum256(livePayload)

	var driveReads atomic.Int32
	mockDrive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"f1","trashed":true}`))
			return
		}
		driveReads.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", strconv.Itoa(len(drivePayload)))
		_, _ = w.Write(drivePayload)
	}))
	defer mockDrive.Close()

	h := newTestHarness(t, false)
	h.ctrl.driveAPIBaseURL = mockDrive.URL
	h.ctrl.driveHTTPClient = mockDrive.Client()
	h.auth.driveScope = true
	_ = h.vault.Set("colab", map[string]any{"token": "tok-1"})
	_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-iso"})

	h.ctrl.mu.Lock()
	h.ctrl.driveOutputs["sub:sub-iso"] = map[string]*ColabOneDriveOutputRecord{
		driveOnlyID: {
			ID:          driveOnlyID,
			Kind:        "image",
			Filename:    "drive_only.png",
			Extension:   "png",
			Mime:        "image/png",
			Size:        int64(len(drivePayload)),
			SHA256:      hex.EncodeToString(driveSHA[:]),
			DriveSynced: true,
			DriveMode:   "drive_api",
			DriveFileID: "drive-file-iso",
		},
	}
	h.ctrl.mu.Unlock()

	// Warm local cache for driveOnlyID via host HTTP GET.
	h.ctrl.Update(map[string]any{"runtime": map[string]any{"state": "stopped"}, "comfy_outputs": []any{}})
	req := httptest.NewRequest(http.MethodGet, "/outputs/"+driveOnlyID, nil)
	rec := httptest.NewRecorder()
	h.ctrl.ServeOutputMediaHTTPWithOptions(context.Background(), rec, req, driveOnlyID, ColabOneOutputMediaOptions{})
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), drivePayload) {
		t.Fatalf("warm Drive cache failed: code=%d", rec.Code)
	}
	if driveReads.Load() != 1 {
		t.Fatalf("expected 1 Drive read, got %d", driveReads.Load())
	}

	// Host comfyui.output.asset hits local SSD cache (0 extra Drive reads).
	assetRes, err := h.ctrl.Command(context.Background(), "comfyui.output.asset", map[string]any{"id": driveOnlyID, "offset": 0})
	if err != nil {
		t.Fatalf("cached host asset read failed: %v", err)
	}
	dec, _ := base64.StdEncoding.DecodeString(asString(asMap(assetRes)["data"]))
	if !bytes.Equal(dec, drivePayload) || driveReads.Load() != 1 {
		t.Fatalf("expected cached host asset hit with 0 extra Drive reads, reads=%d", driveReads.Load())
	}

	// RuntimeOnly MUST NEVER read Drive-only cached output even when GPU is ready!
	h.provider.addManagedSession("ep-iso", "L4", "STANDARD", true)
	h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
		switch {
		case method == "GET" && strings.Contains(path, liveID+"/asset"):
			return map[string]any{
				"data": base64.StdEncoding.EncodeToString(livePayload), "offset": 0,
				"size": len(livePayload), "sha256": hex.EncodeToString(liveSHA[:]), "extension": "png",
			}, nil
		case method == "GET" && strings.Contains(path, rtOnlyDelID+"/asset"):
			return map[string]any{
				"data": base64.StdEncoding.EncodeToString(livePayload), "offset": 0,
				"size": len(livePayload), "sha256": hex.EncodeToString(liveSHA[:]), "extension": "png",
			}, nil
		case method == "GET" && strings.Contains(path, driveOnlyID+"/asset"):
			return nil, ErrNotFound
		case method == "DELETE":
			return map[string]any{"ok": true}, nil
		case path == "snapshot":
			return map[string]any{"runtime": map[string]any{"state": "ready", "id": "ep-iso"}, "comfy_outputs": []any{}}, nil
		default:
			return map[string]any{"ok": true}, nil
		}
	}
	h.ctrl.Update(map[string]any{
		"runtime": map[string]any{"state": "ready", "id": "ep-iso"},
		"comfy_outputs": []any{
			map[string]any{"id": liveID, "extension": "png", "mime": "image/png", "size": len(livePayload), "storage": "runtime"},
			map[string]any{"id": rtOnlyDelID, "extension": "png", "mime": "image/png", "size": len(livePayload), "storage": "runtime"},
		},
	})
	if _, err := h.ctrl.Command(context.Background(), "comfyui.output.asset", map[string]any{"id": driveOnlyID, "runtime_only": true}); err == nil {
		t.Fatal("runtime_only comfyui.output.asset MUST reject Drive-only cached output")
	}

	// Warm caches for liveID & rtOnlyDelID, then test delete eviction on all 3 delete paths!
	for _, id := range []string{liveID, rtOnlyDelID} {
		var buf bytes.Buffer
		if _, err := h.ctrl.StreamOutputMediaToWriter(context.Background(), id, &buf, ColabOneOutputMediaOptions{}); err != nil {
			t.Fatalf("warm cache for %s failed: %v", id, err)
		}
		if _, err := os.Stat(filepath.Join(h.ctrl.outputCacheDir(), id+".bin")); err != nil {
			t.Fatalf("expected cached .bin for %s: %v", id, err)
		}
	}

	// Path 1: runtime_only delete evicts cache.
	if _, err := h.ctrl.Command(context.Background(), "comfyui.output.delete", map[string]any{"id": rtOnlyDelID, "runtime_only": true}); err != nil {
		t.Fatalf("runtime_only delete failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.ctrl.outputCacheDir(), rtOnlyDelID+".bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected runtime_only delete to evict local cache, stat err=%v", err)
	}

	// Path 2: online host delete evicts cache.
	if _, err := h.ctrl.Command(context.Background(), "comfyui.output.delete", map[string]any{"id": liveID}); err != nil {
		t.Fatalf("online delete failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.ctrl.outputCacheDir(), liveID+".bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected online delete to evict local cache, stat err=%v", err)
	}

	// Path 3: offline Drive delete evicts cache.
	h.ctrl.Update(map[string]any{"runtime": map[string]any{"state": "stopped"}, "comfy_outputs": []any{}})
	if _, err := h.ctrl.Command(context.Background(), "comfyui.output.delete", map[string]any{"id": driveOnlyID}); err != nil {
		t.Fatalf("offline Drive delete failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.ctrl.outputCacheDir(), driveOnlyID+".bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected offline Drive delete to evict local cache, stat err=%v", err)
	}
}

func TestOutputCacheAuditHardening(t *testing.T) {
	// 1) A1 H1: Single-flight source isolation (drive vs runtime flights do NOT coalesce).
	t.Run("SingleFlightSourceIsolation", func(t *testing.T) {
		outID := "91919191919191919191919191919191"
		drivePayload := []byte("DRIVE-PAYLOAD-ISOLATED")
		rtPayload := []byte("RUNTIME-PAYLOAD-ISOLATED")
		rtSHA := sha256.Sum256(rtPayload)

		driveStarted := make(chan struct{}, 1)
		driveGate := make(chan struct{})
		mockDrive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case driveStarted <- struct{}{}:
			default:
			}
			select {
			case <-driveGate:
			case <-r.Context().Done():
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(drivePayload)))
			_, _ = w.Write(drivePayload)
		}))
		defer mockDrive.Close()

		h := newTestHarness(t, false)
		h.ctrl.driveAPIBaseURL = mockDrive.URL
		h.ctrl.driveHTTPClient = mockDrive.Client()
		h.auth.driveScope = true
		_ = h.vault.Set("colab", map[string]any{"token": "tok-iso"})
		_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-iso-sf"})

		h.provider.addManagedSession("ep-sf-iso", "L4", "STANDARD", true)
		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			return map[string]any{
				"data":      base64.StdEncoding.EncodeToString(rtPayload),
				"offset":    0,
				"size":      len(rtPayload),
				"sha256":    hex.EncodeToString(rtSHA[:]),
				"extension": "png",
				"filename":  "rt.png",
			}, nil
		}

		driveMeta := &ColabOneOutputMediaMeta{
			ID: outID, Size: int64(len(drivePayload)), Mime: "image/png",
			Extension: "png", Filename: "d.png", Source: "drive", DriveFileID: "fid-sf",
		}
		rtMeta := &ColabOneOutputMediaMeta{
			ID: outID, Size: int64(len(rtPayload)), Mime: "image/png",
			Extension: "png", Filename: "rt.png", Source: "runtime",
		}

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, _, hit, _ := h.ctrl.ensureOutputCached(context.Background(), driveMeta, "drive")
			if hit && f != nil {
				_ = f.Close()
			}
		}()

		select {
		case <-driveStarted:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for drive flight to start")
		}

		// While the drive flight is blocked, a runtime flight for the SAME outID MUST NOT coalesce onto it!
		rtCtx, rtCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer rtCancel()
		fRT, _, hitRT, errRT := h.ctrl.ensureOutputCached(rtCtx, rtMeta, "runtime")
		if errRT != nil || !hitRT || fRT == nil {
			close(driveGate)
			wg.Wait()
			t.Fatalf("expected runtime flight to run independently of blocked drive flight, hit=%v err=%v", hitRT, errRT)
		}
		_ = fRT.Close()

		close(driveGate)
		wg.Wait()
	})

	// 2) A1 M2 & M3: Eviction cancels inflight cleanly as skip (not 502) and already-cancelled ctx returns immediately.
	t.Run("EvictionSkipsWaitersAndCancelledCtxFastFail", func(t *testing.T) {
		h := newTestHarness(t, false)
		outID := "92929292929292929292929292929292"
		cctx, cancel := context.WithCancel(context.Background())
		cancel()
		meta := &ColabOneOutputMediaMeta{ID: outID, Size: 64, Extension: "png", Mime: "image/png", Source: "runtime"}
		if _, _, hit, err := h.ctrl.ensureOutputCached(cctx, meta, "runtime"); hit || !errors.Is(err, context.Canceled) {
			t.Fatalf("expected already-cancelled ctx to return context.Canceled, hit=%v err=%v", hit, err)
		}

		started := make(chan struct{}, 1)
		h.provider.addManagedSession("ep-ev", "L4", "STANDARD", true)
		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			select {
			case started <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return nil, ctx.Err()
		}

		errCh := make(chan error, 1)
		hitCh := make(chan bool, 1)
		go func() {
			f, _, hit, err := h.ctrl.ensureOutputCached(context.Background(), meta, "runtime")
			if f != nil {
				_ = f.Close()
			}
			hitCh <- hit
			errCh <- err
		}()
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for flight start")
		}
		h.ctrl.evictLocalOutputCache(outID)
		if hit := <-hitCh; hit {
			t.Fatal("expected hit=false after eviction")
		}
		if err := <-errCh; err != nil {
			t.Fatalf("expected evicted flight waiter to fall through with nil err, got %v", err)
		}
	})

	// 3) A1 H2, L1, L4: Non-regular targets rejected, stale .tmp swept on cold load, and unwritable dir skips Drive GET.
	t.Run("SafeDirCheckStaleTmpSweepAndPreDriveTempCheck", func(t *testing.T) {
		cfgDir := t.TempDir()
		h := newTestHarnessWithDir(t, cfgDir, false)
		cacheDir := h.ctrl.outputCacheDir()
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			t.Fatal(err)
		}
		staleTmp := filepath.Join(cacheDir, ".outcache-stale.tmp")
		staleSave := filepath.Join(cacheDir, ".save-stale.tmp")
		_ = os.WriteFile(staleTmp, []byte("old"), 0o600)
		_ = os.WriteFile(staleSave, []byte("old"), 0o600)

		h.ctrl.outputCacheMu.Lock()
		_ = h.ctrl.loadLocalOutputCacheIndexLocked()
		h.ctrl.outputCacheMu.Unlock()
		if _, err := os.Stat(staleTmp); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected stale .outcache-*.tmp swept on cold load, err=%v", err)
		}
		if _, err := os.Stat(staleSave); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected stale .save-*.tmp swept on cold load, err=%v", err)
		}

		// File blocking output-cache dir prevents Drive HTTP GET entirely (A1 L4 + A1 H2).
		cfgDir2 := t.TempDir()
		h2 := newTestHarnessWithDir(t, cfgDir2, false)
		if err := os.WriteFile(h2.ctrl.outputCacheDir(), []byte("not-a-dir"), 0o600); err != nil {
			t.Fatal(err)
		}
		var driveHits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			driveHits.Add(1)
			_, _ = w.Write([]byte("ok"))
		}))
		defer srv.Close()
		h2.ctrl.driveAPIBaseURL = srv.URL
		h2.ctrl.driveHTTPClient = srv.Client()
		h2.auth.driveScope = true
		_, err := h2.ctrl.populateOutputCacheFromDrive(context.Background(), &ColabOneOutputMediaMeta{
			ID: "93939393939393939393939393939393", Size: 2, DriveFileID: "fid-block",
		}, 0, 0)
		if !errors.Is(err, errOutputCacheSkipped) {
			t.Fatalf("expected errOutputCacheSkipped for non-dir cache path, got %v", err)
		}
		if driveHits.Load() != 0 {
			t.Fatalf("expected 0 upstream Drive requests when cache dir unsafe/unwritable, got %d", driveHits.Load())
		}
	})

	// 4) A1 H3 & L2: fetchRuntimeOutputChunks caps actualSize and rejects bad worker size/sha256.
	t.Run("FetchRuntimeOutputChunksValidation", func(t *testing.T) {
		h := newTestHarness(t, false)
		outID := "94949494949494949494949494949494"
		h.provider.addManagedSession("ep-chk", "A100", "STANDARD", true)

		// Reject size > colabOneMaxOutputAssetOffset (512 MiB).
		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			return map[string]any{
				"offset": 0,
				"size":   colabOneMaxOutputAssetOffset + 1,
				"data":   base64.StdEncoding.EncodeToString([]byte("A")),
			}, nil
		}
		if _, _, err := h.ctrl.fetchRuntimeOutputChunks(context.Background(), outID, 0, -1, func([]byte, int64, map[string]any) error { return nil }); err == nil {
			t.Fatal("expected error when size exceeds 512 MiB cap")
		}

		// Reject worker chunk with mismatched sha256 or size <= 0.
		chunkA := bytes.Repeat([]byte("A"), int(colabOneMediaRuntimeChunkBytes))
		chunkB := bytes.Repeat([]byte("B"), 1024)
		total := len(chunkA) + len(chunkB)
		shaA := hex.EncodeToString(sha256.New().Sum(nil))
		shaOther := strings.Repeat("f", 64)
		for _, mode := range []string{"bad_sha", "zero_size"} {
			h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
				if strings.Contains(path, "offset=0") {
					return map[string]any{
						"offset": 0, "size": total, "sha256": shaA,
						"data": base64.StdEncoding.EncodeToString(chunkA),
					}, nil
				}
				if mode == "bad_sha" {
					return map[string]any{
						"offset": len(chunkA), "size": total, "sha256": shaOther,
						"data": base64.StdEncoding.EncodeToString(chunkB),
					}, nil
				}
				return map[string]any{
					"offset": len(chunkA), "size": 0, "sha256": shaA,
					"data": base64.StdEncoding.EncodeToString(chunkB),
				}, nil
			}
			if _, _, err := h.ctrl.fetchRuntimeOutputChunks(context.Background(), outID, 0, -1, func([]byte, int64, map[string]any) error { return nil }); err == nil {
				t.Fatalf("expected worker validation error for mode=%s", mode)
			}
		}
	})

	// 5) A1 H4, M4, M5, M6: Drive SHA256 backfill, readLocalOutputCacheChunk SHA256 mismatch eviction,
	//    immediate snapshot prune on delete, and account-switch cache reset.
	t.Run("DriveSHABackfillReadChunkEvictDeletePruneAndAccountReset", func(t *testing.T) {
		outID := "95959595959595959595959595959595"
		payload := []byte("DRIVE-BACKFILL-DATA-1234567890")
		wantSHA := sha256.Sum256(payload)
		wantSHAHex := hex.EncodeToString(wantSHA[:])

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload)
		}))
		defer srv.Close()

		h := newTestHarness(t, false)
		h.ctrl.driveAPIBaseURL = srv.URL
		h.ctrl.driveHTTPClient = srv.Client()
		h.auth.driveScope = true
		_ = h.vault.Set("colab", map[string]any{"token": "tok-bf"})
		_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-bf"})
		h.ctrl.mu.Lock()
		h.ctrl.driveOutputs["sub:sub-bf"] = map[string]*ColabOneDriveOutputRecord{
			outID: {
				ID: outID, Kind: "image", Filename: "bf.png", Extension: "png", Mime: "image/png",
				Size: int64(len(payload)), SHA256: "", DriveSynced: true, DriveMode: "drive_api", DriveFileID: "fid-bf",
			},
		}
		h.ctrl.mu.Unlock()

		// Populate from Drive and verify rec.SHA256 was backfilled (A1 H4).
		meta := &ColabOneOutputMediaMeta{
			ID: outID, Size: int64(len(payload)), Mime: "image/png",
			Extension: "png", Filename: "bf.png", Source: "drive", DriveFileID: "fid-bf",
		}
		if _, err := h.ctrl.populateOutputCacheFromDrive(context.Background(), meta, 0, 0); err != nil {
			t.Fatalf("populateOutputCacheFromDrive failed: %v", err)
		}
		rec := h.ctrl.driveOutputRecordCopy(outID)
		if rec == nil || rec.SHA256 != wantSHAHex {
			t.Fatalf("expected Drive record SHA256 backfilled to %s, got %+v", wantSHAHex, rec)
		}

		// A1 M4: readLocalOutputCacheChunk evicts when rec.SHA256 disagrees with entry.SHA256.
		wrongRec := rec.clone()
		wrongRec.SHA256 = strings.Repeat("0", 64)
		if _, hit := h.ctrl.readLocalOutputCacheChunk(outID, 0, wrongRec, "drive"); hit {
			t.Fatal("expected SHA256 mismatch to reject and evict cached chunk")
		}
		if _, err := os.Stat(filepath.Join(h.ctrl.outputCacheDir(), outID+".bin")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected mismatched cache file evicted, err=%v", err)
		}

		// Re-populate cache, then test A1 M6 account-switch cache cleanup.
		if _, err := h.ctrl.populateOutputCacheFromDrive(context.Background(), meta, 1, 0); err != nil {
			t.Fatalf("re-populate failed: %v", err)
		}
		h.ctrl.resetAccountStateForSwitch(nil, false)
		if _, err := os.Stat(filepath.Join(h.ctrl.outputCacheDir(), outID+".bin")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected account switch to delete cached .bin, err=%v", err)
		}
		if _, err := os.Stat(h.ctrl.outputCacheIndexPath()); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected account switch to delete index.json, err=%v", err)
		}

		// A1 M5: comfyui.output.delete prunes c.data["comfy_outputs"] immediately BEFORE RefreshRemote finishes.
		delID := "96969696969696969696969696969696"
		h.provider.addManagedSession("ep-prune", "L4", "STANDARD", true)
		var seenCountDuringRefresh int
		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			if method == "DELETE" {
				return map[string]any{"ok": true}, nil
			}
			if path == "snapshot" {
				h.ctrl.mu.Lock()
				seenCountDuringRefresh = len(asAnySlice(h.ctrl.data["comfy_outputs"]))
				h.ctrl.mu.Unlock()
				return nil, errors.New("simulated refresh error")
			}
			return map[string]any{"ok": true}, nil
		}
		h.ctrl.Update(map[string]any{
			"runtime":       map[string]any{"state": "ready", "id": "ep-prune"},
			"comfy_outputs": []any{map[string]any{"id": delID, "extension": "png", "size": 10}},
		})
		_, _ = h.ctrl.Command(context.Background(), "comfyui.output.delete", map[string]any{"id": delID, "runtime_only": true})
		if seenCountDuringRefresh != 0 {
			t.Fatalf("expected comfy_outputs pruned BEFORE RefreshRemote, saw %d items", seenCountDuringRefresh)
		}
	})
}

func TestOutputCacheFinalHardening(t *testing.T) {
	// 1) A1 M1: Duplicate identical commit avoids replacing existing .bin, removes temp, and updates LastAccess.
	t.Run("DuplicateIdenticalCommitSkipsOverwrite", func(t *testing.T) {
		h := newTestHarness(t, false)
		outID := "97979797979797979797979797979797"
		payload := []byte("IDENTICAL-OUTPUT-CACHE-BYTES-0123456789")
		sum := sha256.Sum256(payload)
		shaHex := hex.EncodeToString(sum[:])

		cacheDir := h.ctrl.outputCacheDir()
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			t.Fatal(err)
		}
		tmp1, err := os.CreateTemp(cacheDir, ".outcache-*.tmp")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = tmp1.Write(payload)
		_ = tmp1.Close()

		committed, err := h.ctrl.commitTempToOutputCacheWithResetGen(outID, tmp1.Name(), int64(len(payload)), shaHex, "png", "dup.png", "image/png", 0, 0)
		if err != nil || !committed {
			t.Fatalf("initial commit failed: committed=%v err=%v", committed, err)
		}
		binPath := filepath.Join(cacheDir, outID+".bin")
		fi1, err := os.Lstat(binPath)
		if err != nil {
			t.Fatalf("stat committed bin: %v", err)
		}
		// Backdate LastAccess so we can verify duplicate commit refreshes it.
		h.ctrl.outputCacheMu.Lock()
		idx := h.ctrl.loadLocalOutputCacheIndexLocked()
		entry := idx.Entries[outID]
		entry.LastAccess = 100
		idx.Entries[outID] = entry
		h.ctrl.saveLocalOutputCacheIndexLocked(idx)
		h.ctrl.outputCacheMu.Unlock()

		tmp2, err := os.CreateTemp(cacheDir, ".outcache-*.tmp")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = tmp2.Write(payload)
		_ = tmp2.Close()

		committed2, err2 := h.ctrl.commitTempToOutputCacheWithResetGen(outID, tmp2.Name(), int64(len(payload)), shaHex, "png", "dup.png", "image/png", 0, 0)
		if err2 != nil || !committed2 {
			t.Fatalf("duplicate identical commit failed: committed=%v err=%v", committed2, err2)
		}
		if _, err := os.Stat(tmp2.Name()); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected duplicate tmp file removed, stat err=%v", err)
		}
		fi2, err := os.Lstat(binPath)
		if err != nil {
			t.Fatalf("stat bin after duplicate commit: %v", err)
		}
		if !os.SameFile(fi1, fi2) {
			t.Fatal("expected duplicate identical commit to preserve existing .bin without overwriting it")
		}
		h.ctrl.outputCacheMu.Lock()
		idx2 := h.ctrl.loadLocalOutputCacheIndexLocked()
		gotAccess := idx2.Entries[outID].LastAccess
		h.ctrl.outputCacheMu.Unlock()
		if gotAccess <= 100 {
			t.Fatalf("expected LastAccess updated on duplicate identical commit, got %.0f", gotAccess)
		}
	})

	// 2) A1 L1: fetchRuntimeOutputChunks rejects negative/inverted bounds and explicit invalid size even when end >= 0.
	t.Run("FetchRuntimeOutputChunksBoundsAndExplicitSize", func(t *testing.T) {
		h := newTestHarness(t, false)
		outID := "98989898989898989898989898989898"
		rpcCalls := 0
		h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
			rpcCalls++
			return map[string]any{"offset": 0, "data": base64.StdEncoding.EncodeToString([]byte("hello"))}, nil
		}
		if _, _, err := h.ctrl.fetchRuntimeOutputChunks(context.Background(), outID, -1, 4, func([]byte, int64, map[string]any) error { return nil }); !errors.Is(err, errUnsatisfiableMediaRange) {
			t.Fatalf("negative start expected errUnsatisfiableMediaRange, got %v", err)
		}
		if _, _, err := h.ctrl.fetchRuntimeOutputChunks(context.Background(), outID, 10, 5, func([]byte, int64, map[string]any) error { return nil }); !errors.Is(err, errUnsatisfiableMediaRange) {
			t.Fatalf("inverted end < start expected errUnsatisfiableMediaRange, got %v", err)
		}
		if rpcCalls != 0 {
			t.Fatalf("invalid bounds must not issue RPC, got %d calls", rpcCalls)
		}

		// Omitted size with bounded end >= 0 still falls back to end + 1.
		sz, _, err := h.ctrl.fetchRuntimeOutputChunks(context.Background(), outID, 0, 4, func([]byte, int64, map[string]any) error { return nil })
		if err != nil || sz != 5 {
			t.Fatalf("omitted size with end=4 expected actualSize=5, got sz=%d err=%v", sz, err)
		}

		// Explicit invalid size values MUST fail even when end >= 0.
		for _, badSize := range []any{0, -1, "bad-size", int64(colabOneMaxOutputAssetOffset + 1)} {
			h.provider.rpcFn = func(ctx context.Context, method, path string, body any, opts ColabOneRPCOptions) (any, error) {
				return map[string]any{
					"offset": 0,
					"size":   badSize,
					"data":   base64.StdEncoding.EncodeToString([]byte("hello")),
				}, nil
			}
			if _, _, err := h.ctrl.fetchRuntimeOutputChunks(context.Background(), outID, 0, 4, func([]byte, int64, map[string]any) error { return nil }); err == nil || !strings.Contains(err.Error(), "作品大小不合法") {
				t.Fatalf("explicit invalid size=%#v expected 作品大小不合法 error, got %v", badSize, err)
			}
		}
	})

	// 3) A1 L2: populateOutputCacheFromDrive guards SHA-256 backfill against resetGen/fileID/size drift and marks addedDuringSync.
	t.Run("PopulateFromDriveSHABackfillGuardsAndSyncNote", func(t *testing.T) {
		payload := []byte("DRIVE-BACKFILL-GUARD-BYTES-12345")
		wantSum := sha256.Sum256(payload)
		wantSHA := hex.EncodeToString(wantSum[:])

		for _, tc := range []struct {
			name        string
			mutate      func(ctrl *ColabOneController, outID string)
			wantApplied bool
		}{
			{
				name: "reset_gen_changed",
				mutate: func(ctrl *ColabOneController, outID string) {
					ctrl.outputCacheMu.Lock()
					ctrl.outputCacheResetGen++
					ctrl.outputCacheMu.Unlock()
				},
				wantApplied: false,
			},
			{
				name: "drive_file_id_changed",
				mutate: func(ctrl *ColabOneController, outID string) {
					ctrl.mu.Lock()
					ctrl.driveOutputs["sub:sub-l2"][outID].DriveFileID = "other-fid"
					ctrl.mu.Unlock()
				},
				wantApplied: false,
			},
			{
				name: "size_mismatch",
				mutate: func(ctrl *ColabOneController, outID string) {
					ctrl.mu.Lock()
					ctrl.driveOutputs["sub:sub-l2"][outID].Size = 9999
					ctrl.mu.Unlock()
				},
				wantApplied: false,
			},
			{
				name: "clean_backfill_notes_sync",
				mutate: func(ctrl *ColabOneController, outID string) {
					ctrl.mu.Lock()
					ctrl.driveOutputsSyncing = true
					ctrl.driveOutputsSyncAccountKey = "sub:sub-l2"
					ctrl.mu.Unlock()
				},
				wantApplied: true,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				outID := "99999999999999999999999999999999"
				var ctrlRef *ColabOneController
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if tc.mutate != nil && ctrlRef != nil {
						tc.mutate(ctrlRef, outID)
					}
					w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
					_, _ = w.Write(payload)
				}))
				defer srv.Close()

				h := newTestHarness(t, false)
				ctrlRef = h.ctrl
				h.ctrl.driveAPIBaseURL = srv.URL
				h.ctrl.driveHTTPClient = srv.Client()
				h.auth.driveScope = true
				_ = h.vault.Set("colab", map[string]any{"token": "tok-l2"})
				_ = h.vault.Set("google_profile", map[string]any{"sub": "sub-l2"})
				h.ctrl.mu.Lock()
				h.ctrl.driveOutputs["sub:sub-l2"] = map[string]*ColabOneDriveOutputRecord{
					outID: {
						ID: outID, Kind: "image", Filename: "g.png", Extension: "png", Mime: "image/png",
						Size: int64(len(payload)), SHA256: "", DriveSynced: true, DriveMode: "drive_api", DriveFileID: "fid-l2",
					},
				}
				h.ctrl.mu.Unlock()

				meta := &ColabOneOutputMediaMeta{
					ID: outID, Size: int64(len(payload)), Mime: "image/png",
					Extension: "png", Filename: "g.png", Source: "drive", DriveFileID: "fid-l2",
				}
				_, _ = h.ctrl.populateOutputCacheFromDrive(context.Background(), meta, 0, 0)
				rec := h.ctrl.driveOutputRecordCopy(outID)
				if tc.wantApplied {
					if rec == nil || rec.SHA256 != wantSHA {
						t.Fatalf("expected SHA256 backfilled to %s, got %+v", wantSHA, rec)
					}
					h.ctrl.mu.Lock()
					noted := h.ctrl.driveOutputsSyncAdded["sub:sub-l2"][outID]
					h.ctrl.mu.Unlock()
					if !noted {
						t.Fatal("expected noteDriveOutputAddedLocked recorded during in-flight sync")
					}
				} else if rec != nil && rec.SHA256 != "" {
					t.Fatalf("expected SHA256 backfill skipped for %s, got %q", tc.name, rec.SHA256)
				}
			})
		}
	})
}
