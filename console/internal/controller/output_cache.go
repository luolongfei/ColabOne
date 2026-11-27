package controller

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	colabOneOutputCacheVersion            = 1
	colabOneOutputCacheDirName            = "output-cache"
	colabOneOutputCacheIndexName          = "index.json"
	colabOneOutputCacheMaxBytes     int64 = 1 << 30  // 1 GiB
	colabOneOutputCacheMaxEntries         = 512
	colabOneOutputCacheMaxFileBytes int64 = 64 << 20 // 64 MiB
	colabOneMinRuntimeFetchStep     int64 = 64 << 10 // 64 KiB floor for lazy chunk dispatch
	colabOneMediaRuntimeFetchConcurrency  = 6
	colabOneOutputCacheFetchTimeout       = 5 * time.Minute
	colabOneOutputCacheTouchFlushInterval = 5 * time.Second
)

var errOutputCacheSkipped = errors.New("output cache skipped")

func outputCacheFlightKey(source, cleanID string) string {
	if source == "drive" {
		return "drive:" + cleanID
	}
	return "runtime:" + cleanID
}

type colabOneOutputCacheEntry struct {
	ID         string  `json:"id"`
	Size       int64   `json:"size"`
	SHA256     string  `json:"sha256,omitempty"`
	Extension  string  `json:"extension,omitempty"`
	Filename   string  `json:"filename,omitempty"`
	Mime       string  `json:"mime,omitempty"`
	LastAccess float64 `json:"last_access"`
	CreatedAt  float64 `json:"created_at"`
}

func (e *colabOneOutputCacheEntry) clone() *colabOneOutputCacheEntry {
	if e == nil {
		return nil
	}
	cp := *e
	return &cp
}

type colabOneOutputCacheIndex struct {
	Version int                                  `json:"version"`
	Entries map[string]*colabOneOutputCacheEntry `json:"entries"`
}

type colabOneOutputCacheFlight struct {
	done       chan struct{}
	cancel     context.CancelFunc
	waiters    int
	cancelled  bool
	evicted    bool
	finished   bool
	actualSize int64
	err        error
}

func newEmptyLocalOutputCacheIndex() *colabOneOutputCacheIndex {
	return &colabOneOutputCacheIndex{
		Version: colabOneOutputCacheVersion,
		Entries: make(map[string]*colabOneOutputCacheEntry),
	}
}

func (c *ColabOneController) outputCacheDir() string {
	if strings.TrimSpace(c.directory) == "" {
		return ""
	}
	return filepath.Join(c.directory, colabOneOutputCacheDirName)
}

func (c *ColabOneController) outputCacheIndexPath() string {
	dir := c.outputCacheDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, colabOneOutputCacheIndexName)
}

func (c *ColabOneController) outputCacheLimits() (maxBytes int64, maxEntries int, maxFileBytes int64) {
	maxBytes = c.outputCacheMaxBytes
	if maxBytes <= 0 {
		maxBytes = colabOneOutputCacheMaxBytes
	}
	maxEntries = c.outputCacheMaxEntries
	if maxEntries <= 0 {
		maxEntries = colabOneOutputCacheMaxEntries
	}
	maxFileBytes = c.outputCacheMaxFileBytes
	if maxFileBytes <= 0 {
		maxFileBytes = colabOneOutputCacheMaxFileBytes
	}
	return maxBytes, maxEntries, maxFileBytes
}

func (c *ColabOneController) canCacheOutputSize(size int64) bool {
	if c.outputCacheDir() == "" {
		return false
	}
	_, _, maxFileBytes := c.outputCacheLimits()
	return size >= 0 && size <= maxFileBytes
}

func (c *ColabOneController) isControllerClosed() bool {
	c.spawnMu.Lock()
	closed := c.closed
	c.spawnMu.Unlock()
	return closed
}

func (c *ColabOneController) sweepUnindexedOutputCacheFilesLocked(idx *colabOneOutputCacheIndex) {
	dir := c.outputCacheDir()
	if dir == "" || !isSafeCacheDir(dir) {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, de := range entries {
		name := de.Name()
		if strings.HasPrefix(name, ".save-") || strings.HasPrefix(name, ".outcache-") || !strings.HasSuffix(strings.ToLower(name), ".bin") {
			continue
		}
		stem := strings.ToLower(name[:len(name)-4])
		if idx != nil && idx.Entries != nil {
			if entry, ok := idx.Entries[stem]; ok && entry != nil {
				continue
			}
		}
		removeRegularNonSymlinkFile(filepath.Join(dir, name))
	}
}

func (c *ColabOneController) sweepStaleOutputCacheTempFilesLocked(skipBase string) {
	if len(c.outputCacheInflight) > 0 {
		return
	}
	dir := c.outputCacheDir()
	if dir == "" || !isSafeCacheDir(dir) {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, de := range entries {
		name := de.Name()
		if skipBase != "" && name == skipBase {
			continue
		}
		if (strings.HasPrefix(name, ".outcache-") || strings.HasPrefix(name, ".save-")) && strings.HasSuffix(name, ".tmp") {
			removeRegularNonSymlinkFile(filepath.Join(dir, name))
		}
	}
}

func (c *ColabOneController) loadLocalOutputCacheIndexLocked() *colabOneOutputCacheIndex {
	return c.loadLocalOutputCacheIndexLockedSkipTemp("")
}

func (c *ColabOneController) loadLocalOutputCacheIndexLockedSkipTemp(skipTmpBase string) *colabOneOutputCacheIndex {
	if c.outputCacheIndex != nil {
		return c.outputCacheIndex
	}
	idx := newEmptyLocalOutputCacheIndex()
	dir := c.outputCacheDir()
	idxPath := c.outputCacheIndexPath()
	if dir == "" || idxPath == "" || !isSafeCacheDir(dir) {
		c.outputCacheIndex = idx
		return idx
	}
	c.sweepStaleOutputCacheTempFilesLocked(skipTmpBase)
	if !isSafeCacheFileTarget(idxPath) {
		c.outputCacheIndex = idx
		return idx
	}
	lfi, lstatErr := os.Lstat(idxPath)
	if lstatErr != nil || lfi.Mode()&os.ModeSymlink != 0 || !lfi.Mode().IsRegular() {
		c.outputCacheIndex = idx
		return idx
	}
	f, err := os.Open(idxPath)
	if err != nil {
		c.outputCacheIndex = idx
		return idx
	}
	ffi, fstatErr := f.Stat()
	if fstatErr != nil || !ffi.Mode().IsRegular() || !os.SameFile(lfi, ffi) {
		_ = f.Close()
		c.outputCacheIndex = idx
		return idx
	}
	raw, readErr := io.ReadAll(io.LimitReader(f, 8<<20))
	_ = f.Close()
	if readErr != nil {
		c.outputCacheIndex = idx
		return idx
	}
	var parsed colabOneOutputCacheIndex
	if err := json.Unmarshal(raw, &parsed); err != nil {
		c.outputCacheIndex = idx
		c.sweepUnindexedOutputCacheFilesLocked(idx)
		c.saveLocalOutputCacheIndexLocked(idx)
		return idx
	}
	cleanEntries := make(map[string]*colabOneOutputCacheEntry, len(parsed.Entries))
	for k, e := range parsed.Entries {
		if e == nil {
			continue
		}
		cleanID := strings.ToLower(strings.TrimSpace(e.ID))
		if cleanID == "" {
			cleanID = strings.ToLower(strings.TrimSpace(k))
		}
		if !reHex32ID.MatchString(cleanID) || e.Size <= 0 {
			continue
		}
		cp := e.clone()
		cp.ID = cleanID
		cp.SHA256 = sanitizeDriveSHA256(cp.SHA256)
		cp.Extension = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(cp.Extension), "."))
		cleanEntries[cleanID] = cp
	}
	idx.Entries = cleanEntries
	c.outputCacheIndex = idx
	return c.outputCacheIndex
}

func (c *ColabOneController) saveLocalOutputCacheIndexLocked(idx *colabOneOutputCacheIndex) {
	dir := c.outputCacheDir()
	idxPath := c.outputCacheIndexPath()
	if dir == "" || idxPath == "" || !isSafeCacheDir(dir) {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	if !isSafeCacheDir(dir) || !isSafeCacheFileTarget(idxPath) {
		return
	}
	if err := AtomicJSON(idxPath, idx); err == nil {
		c.outputCacheIndexDirty = false
		c.outputCacheLastFlushed = time.Now()
	}
}

func (c *ColabOneController) flushOutputCache() {
	c.outputCacheMu.Lock()
	defer c.outputCacheMu.Unlock()
	if c.outputCacheIndex != nil && c.outputCacheIndexDirty {
		c.saveLocalOutputCacheIndexLocked(c.outputCacheIndex)
	}
}

func (c *ColabOneController) pruneLocalOutputCacheLocked(idx *colabOneOutputCacheIndex, keepID string) {
	maxBytes, maxEntries, _ := c.outputCacheLimits()
	var totalBytes int64
	entries := make([]*colabOneOutputCacheEntry, 0, len(idx.Entries))
	for _, e := range idx.Entries {
		if e == nil {
			continue
		}
		totalBytes += e.Size
		entries = append(entries, e)
	}
	if len(entries) <= maxEntries && totalBytes <= maxBytes {
		c.sweepUnindexedOutputCacheFilesLocked(idx)
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].LastAccess != entries[j].LastAccess {
			return entries[i].LastAccess < entries[j].LastAccess
		}
		if entries[i].CreatedAt != entries[j].CreatedAt {
			return entries[i].CreatedAt < entries[j].CreatedAt
		}
		return entries[i].ID < entries[j].ID
	})
	dir := c.outputCacheDir()
	remaining := len(entries)
	for _, e := range entries {
		if remaining <= maxEntries && totalBytes <= maxBytes {
			break
		}
		if e.ID == keepID {
			continue
		}
		removeRegularNonSymlinkFile(filepath.Join(dir, e.ID+".bin"))
		delete(idx.Entries, e.ID)
		totalBytes -= e.Size
		remaining--
	}
	c.sweepUnindexedOutputCacheFilesLocked(idx)
}

func (c *ColabOneController) openLocalOutputCacheFileLocked(
	cleanID string,
	expectedSize int64,
) (*os.File, *colabOneOutputCacheEntry, bool) {
	return c.openLocalOutputCacheFileCheckedLocked(cleanID, expectedSize, "")
}

func (c *ColabOneController) openLocalOutputCacheFileCheckedLocked(
	cleanID string,
	expectedSize int64,
	expectedSHA256 string,
) (*os.File, *colabOneOutputCacheEntry, bool) {
	if !reHex32ID.MatchString(cleanID) {
		return nil, nil, false
	}
	dir := c.outputCacheDir()
	if dir == "" || !isSafeCacheDir(dir) {
		return nil, nil, false
	}
	idx := c.loadLocalOutputCacheIndexLocked()
	entry, ok := idx.Entries[cleanID]
	if !ok || entry == nil {
		return nil, nil, false
	}
	binPath := filepath.Join(dir, cleanID+".bin")
	evictBad := func(removeFile bool) {
		if removeFile {
			removeRegularNonSymlinkFile(binPath)
		}
		delete(idx.Entries, cleanID)
		c.saveLocalOutputCacheIndexLocked(idx)
	}
	wantSHA := sanitizeDriveSHA256(expectedSHA256)
	entrySHA := sanitizeDriveSHA256(entry.SHA256)
	if wantSHA != "" && entrySHA != "" && !strings.EqualFold(entrySHA, wantSHA) {
		evictBad(true)
		return nil, nil, false
	}

	lfi, lstatErr := os.Lstat(binPath)
	if lstatErr != nil {
		evictBad(false)
		return nil, nil, false
	}
	if lfi.Mode()&os.ModeSymlink != 0 || !lfi.Mode().IsRegular() {
		return nil, nil, false
	}
	if lfi.Size() <= 0 || (expectedSize > 0 && lfi.Size() != expectedSize) || (entry.Size > 0 && lfi.Size() != entry.Size) {
		evictBad(true)
		return nil, nil, false
	}
	f, openErr := os.Open(binPath)
	if openErr != nil {
		return nil, nil, false
	}
	ffi, fstatErr := f.Stat()
	if fstatErr != nil || !ffi.Mode().IsRegular() || !os.SameFile(lfi, ffi) {
		_ = f.Close()
		return nil, nil, false
	}
	if ffi.Size() <= 0 || (expectedSize > 0 && ffi.Size() != expectedSize) || (entry.Size > 0 && ffi.Size() != entry.Size) {
		_ = f.Close()
		evictBad(true)
		return nil, nil, false
	}
	entry.Size = ffi.Size()
	entry.LastAccess = nowUnixFloat()
	c.outputCacheIndexDirty = true
	if c.outputCacheLastFlushed.IsZero() || time.Since(c.outputCacheLastFlushed) >= colabOneOutputCacheTouchFlushInterval {
		c.saveLocalOutputCacheIndexLocked(idx)
	}
	return f, entry.clone(), true
}

func (c *ColabOneController) openLocalOutputCacheFile(
	cleanID string,
	expectedSize int64,
) (*os.File, *colabOneOutputCacheEntry, bool) {
	c.outputCacheMu.Lock()
	defer c.outputCacheMu.Unlock()
	return c.openLocalOutputCacheFileCheckedLocked(cleanID, expectedSize, "")
}

func (c *ColabOneController) openLocalOutputCacheFileChecked(
	cleanID string,
	expectedSize int64,
	expectedSHA256 string,
) (*os.File, *colabOneOutputCacheEntry, bool) {
	c.outputCacheMu.Lock()
	defer c.outputCacheMu.Unlock()
	return c.openLocalOutputCacheFileCheckedLocked(cleanID, expectedSize, expectedSHA256)
}

func (c *ColabOneController) evictLocalOutputCache(identity string) {
	cleanID := strings.ToLower(strings.TrimSpace(identity))
	if !reHex32ID.MatchString(cleanID) {
		return
	}
	c.outputCacheMu.Lock()
	defer c.outputCacheMu.Unlock()

	if c.outputCacheEvictGen == nil {
		c.outputCacheEvictGen = make(map[string]uint64)
	}
	c.outputCacheEvictGen[cleanID]++

	if c.outputCacheInflight != nil {
		for _, fk := range []string{outputCacheFlightKey("drive", cleanID), outputCacheFlightKey("runtime", cleanID)} {
			if flight, ok := c.outputCacheInflight[fk]; ok && flight != nil {
				flight.cancelled = true
				flight.evicted = true
				delete(c.outputCacheInflight, fk)
				if flight.cancel != nil {
					flight.cancel()
				}
			}
		}
	}

	dir := c.outputCacheDir()
	if dir != "" && isSafeCacheDir(dir) {
		removeRegularNonSymlinkFile(filepath.Join(dir, cleanID+".bin"))
	}
	idx := c.loadLocalOutputCacheIndexLocked()
	if _, exists := idx.Entries[cleanID]; exists {
		delete(idx.Entries, cleanID)
		c.saveLocalOutputCacheIndexLocked(idx)
	}
}

func (c *ColabOneController) resetOutputCacheForAccountSwitch() {
	c.outputCacheMu.Lock()
	defer c.outputCacheMu.Unlock()

	c.outputCacheResetGen++
	if c.outputCacheEvictGen == nil {
		c.outputCacheEvictGen = make(map[string]uint64)
	}
	bumped := make(map[string]bool)
	for fk, flight := range c.outputCacheInflight {
		if flight != nil {
			flight.cancelled = true
			flight.evicted = true
			if flight.cancel != nil {
				flight.cancel()
			}
		}
		id := fk
		if idx := strings.LastIndex(fk, ":"); idx >= 0 && idx+1 < len(fk) {
			id = fk[idx+1:]
		}
		if !bumped[id] {
			c.outputCacheEvictGen[id]++
			bumped[id] = true
		}
		delete(c.outputCacheInflight, fk)
	}
	if c.outputCacheIndex != nil {
		for id := range c.outputCacheIndex.Entries {
			if !bumped[id] {
				c.outputCacheEvictGen[id]++
				bumped[id] = true
			}
		}
	}
	c.outputCacheIndex = newEmptyLocalOutputCacheIndex()
	c.outputCacheIndexDirty = false

	dir := c.outputCacheDir()
	if dir != "" && isSafeCacheDir(dir) {
		if entries, err := os.ReadDir(dir); err == nil {
			for _, de := range entries {
				name := de.Name()
				lower := strings.ToLower(name)
				if strings.HasSuffix(lower, ".bin") ||
					name == colabOneOutputCacheIndexName ||
					strings.HasPrefix(name, ".outcache-") ||
					strings.HasPrefix(name, ".save-") {
					removeRegularNonSymlinkFile(filepath.Join(dir, name))
				}
			}
		}
	}
}

func (c *ColabOneController) pruneOutputFromSnapshot(identity string) {
	cleanID := strings.ToLower(strings.TrimSpace(identity))
	if !reHex32ID.MatchString(cleanID) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	raw := asAnySlice(c.data["comfy_outputs"])
	if len(raw) == 0 {
		return
	}
	filtered := make([]any, 0, len(raw))
	removed := false
	for _, item := range raw {
		if strings.EqualFold(strings.TrimSpace(asString(asMap(item)["id"])), cleanID) {
			removed = true
			continue
		}
		filtered = append(filtered, item)
	}
	if removed {
		c.data["comfy_outputs"] = filtered
	}
}

func (c *ColabOneController) commitTempToOutputCache(
	cleanID string,
	tmpPath string,
	size int64,
	sha256Hex, ext, filename, mime string,
	startGen uint64,
) (bool, error) {
	c.outputCacheMu.Lock()
	resetGen := c.outputCacheResetGen
	c.outputCacheMu.Unlock()
	return c.commitTempToOutputCacheWithResetGen(cleanID, tmpPath, size, sha256Hex, ext, filename, mime, startGen, resetGen)
}

func (c *ColabOneController) commitTempToOutputCacheWithResetGen(
	cleanID string,
	tmpPath string,
	size int64,
	sha256Hex, ext, filename, mime string,
	startGen uint64,
	startResetGen uint64,
) (bool, error) {
	dir := c.outputCacheDir()
	if dir == "" || !reHex32ID.MatchString(cleanID) || size <= 0 || !c.canCacheOutputSize(size) {
		_ = os.Remove(tmpPath)
		return false, errOutputCacheSkipped
	}

	c.outputCacheMu.Lock()
	defer c.outputCacheMu.Unlock()

	if c.outputCacheResetGen != startResetGen {
		_ = os.Remove(tmpPath)
		return false, errOutputCacheSkipped
	}
	if c.outputCacheEvictGen != nil && c.outputCacheEvictGen[cleanID] != startGen {
		_ = os.Remove(tmpPath)
		return false, errOutputCacheSkipped
	}
	if !isSafeCacheDir(dir) {
		_ = os.Remove(tmpPath)
		return false, errOutputCacheSkipped
	}
	if err := os.MkdirAll(dir, 0o700); err != nil || !isSafeCacheDir(dir) {
		_ = os.Remove(tmpPath)
		return false, errOutputCacheSkipped
	}
	binPath := filepath.Join(dir, cleanID+".bin")
	if !isSafeCacheFileTarget(binPath) {
		_ = os.Remove(tmpPath)
		return false, errOutputCacheSkipped
	}

	cleanSHA := sanitizeDriveSHA256(sha256Hex)
	cleanExt := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
	cleanFilename := strings.TrimSpace(filename)
	cleanMime := strings.TrimSpace(mime)
	now := nowUnixFloat()

	idx := c.loadLocalOutputCacheIndexLockedSkipTemp(filepath.Base(tmpPath))
	if existing := idx.Entries[cleanID]; existing != nil && existing.Size == size {
		existingSHA := sanitizeDriveSHA256(existing.SHA256)
		if existingSHA == "" || cleanSHA == "" || strings.EqualFold(existingSHA, cleanSHA) {
			if lfi, lErr := os.Lstat(binPath); lErr == nil && lfi.Mode()&os.ModeSymlink == 0 && lfi.Mode().IsRegular() && lfi.Size() == size {
				_ = os.Remove(tmpPath)
				existing.LastAccess = now
				c.pruneLocalOutputCacheLocked(idx, cleanID)
				c.saveLocalOutputCacheIndexLocked(idx)
				return true, nil
			}
		}
	}

	// Single-attempt rename under outputCacheMu so Windows sharing locks on an
	// open .bin never stall unrelated cache operations for 750ms.
	if err := os.Rename(tmpPath, binPath); err != nil {
		_ = os.Remove(tmpPath)
		return false, errOutputCacheSkipped
	}
	idx.Entries[cleanID] = &colabOneOutputCacheEntry{
		ID:         cleanID,
		Size:       size,
		SHA256:     cleanSHA,
		Extension:  cleanExt,
		Filename:   cleanFilename,
		Mime:       cleanMime,
		LastAccess: now,
		CreatedAt:  now,
	}
	c.pruneLocalOutputCacheLocked(idx, cleanID)
	c.saveLocalOutputCacheIndexLocked(idx)
	return true, nil
}

// fetchRuntimeOutputChunks downloads byte range [start, end] (or [start, actualSize-1] when end < 0)
// from the remote runtime using up to colabOneMediaRuntimeFetchConcurrency parallel 512 KiB RPCs
// while emitting chunks strictly in order with bounded memory lookahead.
func (c *ColabOneController) fetchRuntimeOutputChunks(
	ctx context.Context,
	cleanID string,
	start, end int64,
	onChunk func(chunk []byte, actualSize int64, firstMeta map[string]any) error,
) (int64, map[string]any, error) {
	reqCtx, cancelReq := c.withRootContext(ctx)
	defer cancelReq()
	if err := reqCtx.Err(); err != nil {
		return 0, nil, err
	}
	if start < 0 || (end >= 0 && end < start) {
		return 0, nil, errUnsatisfiableMediaRange
	}
	res, err := c.remote(reqCtx, "GET", fmt.Sprintf("comfyui/outputs/%s/asset?offset=%d", cleanID, start), nil, "", 0)
	if err != nil {
		return 0, nil, err
	}
	m := asMap(res)
	if m == nil {
		return 0, nil, errors.New("输出文件分片数据不完整")
	}
	if rawOff, hasOff := m["offset"]; hasOff {
		if offVal, ok := asInt64(rawOff); !ok || offVal != start {
			return 0, nil, errors.New("输出文件分片数据不完整")
		}
	}
	var actualSize int64
	if rawSz, hasRawSz := m["size"]; hasRawSz {
		szVal, ok := asInt64(rawSz)
		if !ok || szVal <= 0 || szVal > colabOneMaxOutputAssetOffset {
			return 0, nil, errors.New("作品大小不合法")
		}
		actualSize = szVal
		if start >= actualSize {
			return 0, nil, &colabOneUnsatisfiableMediaRangeError{totalSize: actualSize}
		}
	} else if end >= 0 && end < colabOneMaxOutputAssetOffset {
		actualSize = end + 1
	} else {
		return 0, nil, errors.New("作品大小不合法")
	}
	firstSHA := sanitizeDriveSHA256(asString(m["sha256"]))
	rawB64 := asString(m["data"])
	if rawB64 == "" {
		return 0, nil, errors.New("输出文件分片数据不完整")
	}
	firstChunk, decErr := base64.StdEncoding.DecodeString(rawB64)
	if decErr != nil || len(firstChunk) == 0 || int64(len(firstChunk)) > colabOneMediaRuntimeChunkBytes {
		return 0, nil, errors.New("输出文件分片数据不完整")
	}
	effectiveEnd := end
	if effectiveEnd < 0 || effectiveEnd >= actualSize {
		effectiveEnd = actualSize - 1
	}
	remainingFirst := effectiveEnd - start + 1
	if int64(len(firstChunk)) > remainingFirst {
		firstChunk = firstChunk[:remainingFirst]
	}
	if err := onChunk(firstChunk, actualSize, m); err != nil {
		return actualSize, m, err
	}

	cur := start + int64(len(firstChunk))
	if cur > effectiveEnd {
		return actualSize, m, nil
	}

	step := int64(len(firstChunk))
	if step < colabOneMinRuntimeFetchStep {
		step = colabOneMinRuntimeFetchStep
	}
	if step > colabOneMediaRuntimeChunkBytes {
		step = colabOneMediaRuntimeChunkBytes
	}

	type chunkJob struct {
		idx     int
		offset  int64
		wantLen int64
	}
	type chunkResult struct {
		idx  int
		data []byte
		err  error
	}

	remainingBytes := effectiveEnd - cur + 1
	totalJobs := int((remainingBytes-1)/step + 1)

	workers := colabOneMediaRuntimeFetchConcurrency
	if totalJobs < workers {
		workers = totalJobs
	}
	windowSize := workers * 2
	if windowSize > totalJobs {
		windowSize = totalJobs
	}

	workCtx, cancelWork := context.WithCancel(reqCtx)
	defer cancelWork()

	windowSem := make(chan struct{}, windowSize)
	jobsCh := make(chan chunkJob, workers)
	resultsCh := make(chan chunkResult, workers)

	var wg sync.WaitGroup

	// Bounded O(1)-memory lookahead dispatcher generating chunk jobs lazily.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(jobsCh)
		jobIdx := 0
		for off := cur; off <= effectiveEnd; {
			want := step
			if rem := effectiveEnd - off + 1; rem < want {
				want = rem
			}
			job := chunkJob{
				idx:     jobIdx,
				offset:  off,
				wantLen: want,
			}
			select {
			case windowSem <- struct{}{}:
			case <-workCtx.Done():
				return
			}
			select {
			case jobsCh <- job:
			case <-workCtx.Done():
				return
			}
			jobIdx++
			if effectiveEnd-off < want {
				break
			}
			off += want
		}
	}()

	// Parallel chunk fetcher workers.
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if rec := recover(); rec != nil {
					cancelWork()
					select {
					case resultsCh <- chunkResult{idx: -1, err: fmt.Errorf("输出文件分片拉取异常：%v", rec)}:
					default:
					}
				}
			}()
			for job := range jobsCh {
				buf := make([]byte, 0, job.wantLen)
				subOff := job.offset
				var fetchErr error
				for int64(len(buf)) < job.wantLen {
					if err := workCtx.Err(); err != nil {
						fetchErr = err
						break
					}
					cRes, cErr := c.remote(workCtx, "GET", fmt.Sprintf("comfyui/outputs/%s/asset?offset=%d", cleanID, subOff), nil, "", 0)
					if cErr != nil {
						fetchErr = cErr
						break
					}
					cm := asMap(cRes)
					if cm == nil {
						fetchErr = errors.New("输出文件分片数据不完整")
						break
					}
					if rawOff, hasOff := cm["offset"]; hasOff {
						if offVal, ok := asInt64(rawOff); !ok || offVal != subOff {
							fetchErr = errors.New("输出文件分片数据不完整")
							break
						}
					}
					if rawSz, hasSz := cm["size"]; hasSz {
						if szVal, ok := asInt64(rawSz); !ok || szVal <= 0 || (actualSize > 0 && szVal != actualSize) {
							fetchErr = errors.New("输出文件分片数据不完整")
							break
						}
					}
					if firstSHA != "" {
						if rawChunkSHA := strings.TrimSpace(asString(cm["sha256"])); rawChunkSHA != "" {
							chunkSHA := sanitizeDriveSHA256(rawChunkSHA)
							if chunkSHA == "" || !strings.EqualFold(chunkSHA, firstSHA) {
								fetchErr = errors.New("输出文件分片数据不完整")
								break
							}
						}
					}
					b64 := asString(cm["data"])
					if b64 == "" {
						fetchErr = errors.New("输出文件分片数据不完整")
						break
					}
					dec, dErr := base64.StdEncoding.DecodeString(b64)
					if dErr != nil || len(dec) == 0 || int64(len(dec)) > colabOneMediaRuntimeChunkBytes {
						fetchErr = errors.New("输出文件分片数据不完整")
						break
					}
					need := job.wantLen - int64(len(buf))
					if int64(len(dec)) > need {
						dec = dec[:need]
					}
					buf = append(buf, dec...)
					subOff += int64(len(dec))
				}
				select {
				case resultsCh <- chunkResult{idx: job.idx, data: buf, err: fetchErr}:
				case <-workCtx.Done():
					return
				}
			}
		}()
	}

	pending := make(map[int][]byte, windowSize)
	nextIdx := 0
	var firstErr error
	for nextIdx < totalJobs {
		select {
		case res := <-resultsCh:
			if res.err != nil {
				firstErr = res.err
				cancelWork()
				goto finish
			}
			pending[res.idx] = res.data
			for {
				data, ok := pending[nextIdx]
				if !ok {
					break
				}
				delete(pending, nextIdx)
				nextIdx++
				if err := onChunk(data, actualSize, m); err != nil {
					firstErr = err
					cancelWork()
					goto finish
				}
				<-windowSem
			}
		case <-workCtx.Done():
			firstErr = workCtx.Err()
			goto finish
		}
	}

finish:
	cancelWork()
	wg.Wait()
	if firstErr != nil {
		return actualSize, m, firstErr
	}
	return actualSize, m, nil
}

func (c *ColabOneController) populateOutputCacheFromRuntime(
	ctx context.Context,
	meta *ColabOneOutputMediaMeta,
	startGen uint64,
	startResetGen uint64,
) (int64, error) {
	dir := c.outputCacheDir()
	if dir == "" || !isSafeCacheDir(dir) {
		return 0, errOutputCacheSkipped
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, errOutputCacheSkipped
	}
	if !isSafeCacheDir(dir) {
		return 0, errOutputCacheSkipped
	}
	tmp, err := os.CreateTemp(dir, ".outcache-*.tmp")
	if err != nil {
		return 0, errOutputCacheSkipped
	}
	tmpPath := tmp.Name()
	closed := false
	committed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	_ = tmp.Chmod(0o600)

	_, _, maxFileBytes := c.outputCacheLimits()
	hasher := sha256.New()
	var written int64
	actualSize, firstMeta, err := c.fetchRuntimeOutputChunks(ctx, meta.ID, 0, -1, func(chunk []byte, total int64, _ map[string]any) error {
		if total > maxFileBytes || written+int64(len(chunk)) > maxFileBytes {
			return errOutputCacheSkipped
		}
		if _, wErr := tmp.Write(chunk); wErr != nil {
			return errOutputCacheSkipped
		}
		_, _ = hasher.Write(chunk)
		written += int64(len(chunk))
		return nil
	})
	if err != nil {
		return actualSize, err
	}
	if written <= 0 || (actualSize > 0 && written != actualSize) {
		return actualSize, errors.New("输出文件分片数据不完整")
	}
	if err := tmp.Sync(); err != nil {
		return written, errOutputCacheSkipped
	}
	if err := tmp.Close(); err != nil {
		closed = true
		return written, errOutputCacheSkipped
	}
	closed = true

	shaHex := hex.EncodeToString(hasher.Sum(nil))
	if remoteSHA := sanitizeDriveSHA256(asString(firstMeta["sha256"])); remoteSHA != "" && !strings.EqualFold(remoteSHA, shaHex) {
		return written, errors.New("输出文件完整性校验失败")
	}
	if metaSHA := sanitizeDriveSHA256(meta.SHA256); metaSHA != "" && !strings.EqualFold(metaSHA, shaHex) {
		return written, errors.New("输出文件完整性校验失败")
	}
	ext := meta.Extension
	if ext == "" {
		ext = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(asString(firstMeta["extension"])), "."))
	}
	fname := meta.Filename
	if fn := strings.TrimSpace(asString(firstMeta["filename"])); fn != "" {
		fname = fn
	}
	ok, commitErr := c.commitTempToOutputCacheWithResetGen(meta.ID, tmpPath, written, shaHex, ext, fname, meta.Mime, startGen, startResetGen)
	if commitErr != nil || !ok {
		return written, errOutputCacheSkipped
	}
	committed = true
	return written, nil
}

func (c *ColabOneController) populateOutputCacheFromDrive(
	ctx context.Context,
	meta *ColabOneOutputMediaMeta,
	startGen uint64,
	startResetGen uint64,
) (int64, error) {
	dir := c.outputCacheDir()
	if dir == "" || !isSafeCacheDir(dir) {
		return 0, errOutputCacheSkipped
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, errOutputCacheSkipped
	}
	if !isSafeCacheDir(dir) {
		return 0, errOutputCacheSkipped
	}
	initAcctKey := c.currentDriveAccountKey()
	// Create the temp file before issuing the Drive HTTP GET so read-only or
	// unwritable cache directories bail out immediately without wasting upstream I/O.
	tmp, err := os.CreateTemp(dir, ".outcache-*.tmp")
	if err != nil {
		return 0, errOutputCacheSkipped
	}
	tmpPath := tmp.Name()
	closed := false
	committed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	_ = tmp.Chmod(0o600)

	reqCtx, cancelReq := c.withRootContext(ctx)
	defer cancelReq()

	token, err := c.driveAccessToken(reqCtx)
	if err != nil {
		return 0, err
	}
	reqURL := c.driveAPIBase() + "/files/" + url.PathEscape(meta.DriveFileID) + "?alt=media"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if meta.QuotaProject != "" {
		req.Header.Set("X-Goog-User-Project", meta.QuotaProject)
	}
	if err := reqCtx.Err(); err != nil {
		return 0, err
	}
	resp, err := c.driveStreamClient().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return 0, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return 0, &ColabOneHTTPError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("从 Google 云盘读取作品失败（HTTP %d）", resp.StatusCode),
		}
	}
	_, _, maxFileBytes := c.outputCacheLimits()
	if resp.ContentLength > maxFileBytes {
		return resp.ContentLength, errOutputCacheSkipped
	}

	hasher := sha256.New()
	buf := make([]byte, 32*1024)
	var written int64
	for {
		if err := reqCtx.Err(); err != nil {
			return written, err
		}
		n, rErr := resp.Body.Read(buf)
		if n > 0 {
			if written+int64(n) > maxFileBytes {
				return written + int64(n), errOutputCacheSkipped
			}
			if _, wErr := tmp.Write(buf[:n]); wErr != nil {
				return written, errOutputCacheSkipped
			}
			_, _ = hasher.Write(buf[:n])
			written += int64(n)
		}
		if rErr != nil {
			if errors.Is(rErr, io.EOF) {
				break
			}
			return written, rErr
		}
	}
	if written <= 0 || (resp.ContentLength > 0 && written != resp.ContentLength) || (resp.ContentLength < 0 && meta.Size > 0 && written != meta.Size) {
		return written, errors.New("输出文件分片数据不完整")
	}
	shaHex := hex.EncodeToString(hasher.Sum(nil))
	if expectedSHA := sanitizeDriveSHA256(meta.SHA256); expectedSHA != "" && !strings.EqualFold(expectedSHA, shaHex) {
		return written, errors.New("输出文件完整性校验失败")
	}
	if err := tmp.Sync(); err != nil {
		return written, errOutputCacheSkipped
	}
	if err := tmp.Close(); err != nil {
		closed = true
		return written, errOutputCacheSkipped
	}
	closed = true

	ok, commitErr := c.commitTempToOutputCacheWithResetGen(meta.ID, tmpPath, written, shaHex, meta.Extension, meta.Filename, meta.Mime, startGen, startResetGen)
	if commitErr != nil || !ok {
		return written, errOutputCacheSkipped
	}
	committed = true

	// Backfill SHA256 and authoritative Size onto the persisted Drive record when missing
	// or stale so subsequent requests hit SSD cache without repeated probes or eviction thrashing.
	c.outputCacheMu.Lock()
	resetUnchanged := c.outputCacheResetGen == startResetGen
	c.outputCacheMu.Unlock()
	if resetUnchanged {
		cleanSHA := sanitizeDriveSHA256(shaHex)
		persistedMeta := false
		c.mu.Lock()
		acctKey := c.currentDriveAccountKeyLocked()
		if acctKey != "" && (initAcctKey == "" || acctKey == initAcctKey) && c.driveOutputs != nil {
			if bucket := c.driveOutputs[acctKey]; bucket != nil {
				if liveRec := bucket[meta.ID]; liveRec != nil &&
					(meta.DriveFileID == "" || liveRec.DriveFileID == meta.DriveFileID || liveRec.DriveWorkspaceFileID == meta.DriveFileID) {
					if cleanSHA != "" && sanitizeDriveSHA256(liveRec.SHA256) == "" && (liveRec.Size <= 0 || liveRec.Size == written) {
						liveRec.SHA256 = cleanSHA
						c.noteDriveOutputAddedLocked(acctKey, meta.ID)
						persistedMeta = true
					}
					if resp.ContentLength > 0 && liveRec.Size != written {
						liveRec.Size = written
						c.noteDriveOutputAddedLocked(acctKey, meta.ID)
						persistedMeta = true
					}
				}
			}
		}
		c.mu.Unlock()
		if persistedMeta {
			c.outputCacheMu.Lock()
			stillUnchanged := c.outputCacheResetGen == startResetGen
			c.outputCacheMu.Unlock()
			if stillUnchanged {
				_ = c.saveDriveOutputs()
			}
		}
	}
	return written, nil
}

// ensureOutputCached returns an open verified local cache file for meta.ID.
// Concurrent misses for the same output ID and source coalesce onto a single upstream fetch.
// If caching is skipped (file too large, symlink/disk issue, controller closing),
// it returns (nil, nil, false, nil) so the caller can stream directly.
func (c *ColabOneController) ensureOutputCached(
	ctx context.Context,
	meta *ColabOneOutputMediaMeta,
	source string,
) (*os.File, *colabOneOutputCacheEntry, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, false, err
	}
	if meta == nil || !reHex32ID.MatchString(meta.ID) {
		return nil, nil, false, nil
	}
	if !c.canCacheOutputSize(meta.Size) || c.isControllerClosed() {
		return nil, nil, false, nil
	}
	if source != "drive" {
		source = "runtime"
	}
	flightKey := outputCacheFlightKey(source, meta.ID)

	c.outputCacheMu.Lock()
	if f, entry, hit := c.openLocalOutputCacheFileCheckedLocked(meta.ID, meta.Size, meta.SHA256); hit {
		c.outputCacheMu.Unlock()
		return f, entry, true, nil
	}
	if c.outputCacheInflight == nil {
		c.outputCacheInflight = make(map[string]*colabOneOutputCacheFlight)
	}
	if c.outputCacheEvictGen == nil {
		c.outputCacheEvictGen = make(map[string]uint64)
	}
	flight := c.outputCacheInflight[flightKey]
	if flight != nil && !flight.cancelled {
		flight.waiters++
		c.outputCacheMu.Unlock()
	} else {
		parentCtx := c.rootCtx
		if parentCtx == nil {
			parentCtx = context.Background()
		}
		flightCtx, cancelFlight := context.WithTimeout(parentCtx, colabOneOutputCacheFetchTimeout)
		startGen := c.outputCacheEvictGen[meta.ID]
		startResetGen := c.outputCacheResetGen
		flight = &colabOneOutputCacheFlight{
			done:    make(chan struct{}),
			cancel:  cancelFlight,
			waiters: 1,
		}
		c.outputCacheInflight[flightKey] = flight
		metaCopy := *meta
		c.outputCacheMu.Unlock()

		runFetch := func() {
			var (
				sz  int64
				err error
			)
			defer func() {
				if r := recover(); r != nil && err == nil {
					err = errOutputCacheSkipped
				}
				cancelFlight()
				c.outputCacheMu.Lock()
				flight.actualSize = sz
				flight.err = err
				flight.finished = true
				if c.outputCacheInflight[flightKey] == flight {
					delete(c.outputCacheInflight, flightKey)
				}
				close(flight.done)
				c.outputCacheMu.Unlock()
			}()
			if source == "drive" {
				sz, err = c.populateOutputCacheFromDrive(flightCtx, &metaCopy, startGen, startResetGen)
			} else {
				sz, err = c.populateOutputCacheFromRuntime(flightCtx, &metaCopy, startGen, startResetGen)
			}
		}
		if !c.spawn(runFetch) {
			cancelFlight()
			c.outputCacheMu.Lock()
			if c.outputCacheInflight[flightKey] == flight {
				delete(c.outputCacheInflight, flightKey)
			}
			flight.finished = true
			flight.err = errOutputCacheSkipped
			close(flight.done)
			c.outputCacheMu.Unlock()
			return nil, nil, false, nil
		}
	}

	// Wait for single-flight completion or caller context cancellation.
	select {
	case <-flight.done:
		c.outputCacheMu.Lock()
		flight.waiters--
		fetchErr := flight.err
		actualSize := flight.actualSize
		wasEvicted := flight.evicted
		c.outputCacheMu.Unlock()
		if fetchErr != nil {
			if errors.Is(fetchErr, errOutputCacheSkipped) || (wasEvicted && errors.Is(fetchErr, context.Canceled)) {
				return nil, nil, false, nil
			}
			return nil, nil, false, fetchErr
		}
		expected := meta.Size
		if actualSize > 0 {
			expected = actualSize
			meta.Size = actualSize
		}
		if f, entry, hit := c.openLocalOutputCacheFileChecked(meta.ID, expected, meta.SHA256); hit {
			return f, entry, true, nil
		}
		return nil, nil, false, nil

	case <-ctx.Done():
		c.outputCacheMu.Lock()
		flight.waiters--
		if flight.waiters <= 0 && !flight.finished {
			flight.cancelled = true
			if c.outputCacheInflight[flightKey] == flight {
				delete(c.outputCacheInflight, flightKey)
			}
			if flight.cancel != nil {
				flight.cancel()
			}
		}
		c.outputCacheMu.Unlock()
		return nil, nil, false, ctx.Err()
	}
}

func (c *ColabOneController) serveOutputMediaFromFile(
	rw http.ResponseWriter,
	r *http.Request,
	meta *ColabOneOutputMediaMeta,
	f *os.File,
	rawRange string,
	start, end int64,
	isRange bool,
) {
	if fi, err := f.Stat(); err == nil && fi.Size() > 0 {
		meta.Size = fi.Size()
	}
	if !isRange {
		start = 0
		end = meta.Size - 1
	} else if strings.TrimSpace(rawRange) != "" {
		rs, re, rIsRange, rErr := parseColabOneByteRange(rawRange, meta.Size)
		if rErr != nil {
			rw.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", meta.Size))
			rw.Header().Set("Accept-Ranges", "bytes")
			writeOutputMediaError(rw, r, http.StatusRequestedRangeNotSatisfiable, errUnsatisfiableMediaRange.Error())
			return
		}
		start, end, isRange = rs, re, rIsRange
	} else if end >= meta.Size {
		end = meta.Size - 1
	}
	if start < 0 || start >= meta.Size || end < start {
		rw.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", meta.Size))
		rw.Header().Set("Accept-Ranges", "bytes")
		writeOutputMediaError(rw, r, http.StatusRequestedRangeNotSatisfiable, errUnsatisfiableMediaRange.Error())
		return
	}

	writeOutputMediaHeaders(rw, meta, start, end, isRange)
	if isRange {
		rw.WriteHeader(http.StatusPartialContent)
	} else {
		rw.WriteHeader(http.StatusOK)
	}

	sr := io.NewSectionReader(f, start, end-start+1)
	flusher, _ := rw.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, rErr := sr.Read(buf)
		if n > 0 {
			if _, wErr := rw.Write(buf[:n]); wErr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rErr != nil {
			return
		}
	}
}

func (c *ColabOneController) readLocalOutputCacheChunk(
	identity string,
	offset int,
	rec *ColabOneDriveOutputRecord,
	source string,
) (map[string]any, bool) {
	cleanID := strings.ToLower(strings.TrimSpace(identity))
	if !reHex32ID.MatchString(cleanID) || offset < 0 {
		return nil, false
	}
	var expectedSize int64
	if rec != nil && rec.Size > 0 {
		expectedSize = rec.Size
	}
	f, entry, hit := c.openLocalOutputCacheFile(cleanID, expectedSize)
	if !hit {
		return nil, false
	}
	defer f.Close()

	size := entry.Size
	if size <= 0 || int64(offset) >= size {
		return nil, false
	}

	entrySHA := sanitizeDriveSHA256(entry.SHA256)
	recSHA := ""
	if rec != nil {
		recSHA = sanitizeDriveSHA256(rec.SHA256)
	}
	if entrySHA != "" && recSHA != "" && !strings.EqualFold(entrySHA, recSHA) {
		_ = f.Close()
		c.evictLocalOutputCache(cleanID)
		return nil, false
	}
	sha := entrySHA
	if sha == "" {
		sha = recSHA
	}
	if source == "runtime" && sha == "" {
		// Runtime chunks require a verified SHA-256 digest for desktop export validation.
		return nil, false
	}

	ext := ""
	if rec != nil {
		ext = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(rec.Extension), "."))
	}
	if ext == "" {
		ext = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(entry.Extension), "."))
	}
	km, ok := allowedDriveOutputKindMIME[ext]
	if !ok {
		return nil, false
	}

	filename := ""
	if rec != nil {
		filename = strings.TrimSpace(rec.Filename)
	}
	if filename == "" {
		filename = strings.TrimSpace(entry.Filename)
	}
	if filename == "" {
		filename = fmt.Sprintf("ColabOne-%s.%s", cleanID[:8], ext)
	}

	chunkLen := colabOneMediaRuntimeChunkBytes
	if int64(offset)+chunkLen > size {
		chunkLen = size - int64(offset)
	}
	buf := make([]byte, chunkLen)
	n, err := io.ReadFull(io.NewSectionReader(f, int64(offset), chunkLen), buf)
	if err != nil || int64(n) != chunkLen {
		_ = f.Close()
		c.evictLocalOutputCache(cleanID)
		return nil, false
	}

	out := map[string]any{
		"data":      base64.StdEncoding.EncodeToString(buf[:n]),
		"offset":    offset,
		"size":      size,
		"mime":      km[1],
		"extension": ext,
		"filename":  filename,
		"source":    source,
	}
	if sha != "" {
		out["sha256"] = sha
	}
	return out, true
}
