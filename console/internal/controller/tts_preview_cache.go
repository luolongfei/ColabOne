package controller

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	colabOneLocalTtsPreviewCacheVersion = 1
	colabOneLocalTtsPreviewDirName      = "tts-preview-cache"
	colabOneLocalTtsPreviewIndexName    = "index.json"
	colabOneLocalTtsPreviewMaxBytes     = 100 * 1024 * 1024 // 100 MiB
	colabOneLocalTtsPreviewMaxEntries   = 200
	colabOneDefaultVoicePresetID        = "shen_zhiyi"
	colabOneDefaultVoiceAnchorText      = "大家好，欢迎来到今天的节目。我们将用清晰自然的声音，为您娓娓道来每一个精彩细节，希望这段分享能带给您轻松愉快的收听体验。"
)

type colabOneLocalTtsPreviewEntry struct {
	PreviewID       string  `json:"preview_id"`
	RequestKey      string  `json:"request_key,omitempty"`
	EngineID        string  `json:"engine_id"`
	VoicePreset     string  `json:"voice_preset"`
	Text            string  `json:"text"`
	SampleRate      int     `json:"sample_rate"`
	DurationSeconds float64 `json:"duration_seconds"`
	MIME            string  `json:"mime"`
	Size            int     `json:"size"`
	SHA256          string  `json:"sha256"`
	UpdatedAt       float64 `json:"updated_at"`
}

type colabOnePendingTtsEntry struct {
	seq  uint64
	data map[string]any
}

type colabOneLocalTtsPreviewIndex struct {
	Version      int                                      `json:"version"`
	ByPreviewID  map[string]*colabOneLocalTtsPreviewEntry `json:"by_preview_id"`
	ByRequestKey map[string]string                        `json:"by_request_key"`
}

func newEmptyLocalTtsPreviewIndex() *colabOneLocalTtsPreviewIndex {
	return &colabOneLocalTtsPreviewIndex{
		Version:      colabOneLocalTtsPreviewCacheVersion,
		ByPreviewID:  make(map[string]*colabOneLocalTtsPreviewEntry),
		ByRequestKey: make(map[string]string),
	}
}

func (c *ColabOneController) ttsPreviewCacheDir() string {
	return filepath.Join(c.directory, colabOneLocalTtsPreviewDirName)
}

func (c *ColabOneController) ttsPreviewCacheIndexPath() string {
	return filepath.Join(c.ttsPreviewCacheDir(), colabOneLocalTtsPreviewIndexName)
}

func isSymlinkPath(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeSymlink != 0
}

func (c *ColabOneController) findTtsEngineSpec(engineID string) map[string]any {
	target := strings.TrimSpace(engineID)
	if target == "" {
		return nil
	}
	c.mu.Lock()
	engines := asMapSlice(c.data["tts_engines"])
	c.mu.Unlock()
	for _, e := range engines {
		if strings.TrimSpace(asString(e["id"])) == target {
			return deepCopyMap(e)
		}
	}
	for _, e := range c.builtinTtsEngines() {
		if strings.TrimSpace(asString(e["id"])) == target {
			return deepCopyMap(e)
		}
	}
	return nil
}

func computeLocalTtsPreviewRequestKey(engine map[string]any, req map[string]any) (string, bool) {
	if engine == nil || req == nil {
		return "", false
	}
	if strings.TrimSpace(asString(req["ref_audio_input_id"])) != "" ||
		strings.TrimSpace(asString(req["emotion_ref_input_id"])) != "" {
		return "", false
	}
	engineID := strings.TrimSpace(asString(engine["id"]))
	if engineID == "" {
		engineID = strings.TrimSpace(asString(req["engine_id"]))
	}
	if engineID == "" {
		return "", false
	}

	rawPreset := strings.TrimSpace(asString(req["voice_preset"]))
	normPreset := rawPreset
	if strings.EqualFold(rawPreset, "custom") {
		normPreset = "custom"
	} else if normPreset == "" {
		normPreset = colabOneDefaultVoicePresetID
	}

	var matchedPreset map[string]any
	if normPreset != "custom" {
		for _, item := range asMapSlice(engine["voice_presets"]) {
			if strings.TrimSpace(asString(item["id"])) == normPreset {
				matchedPreset = item
				break
			}
		}
	}

	resolvedText := strings.TrimSpace(asString(req["preview_text"]))
	if resolvedText == "" {
		if matchedPreset != nil {
			resolvedText = strings.TrimSpace(asString(matchedPreset["preview_text"]))
		}
		if resolvedText == "" {
			resolvedText = colabOneDefaultVoiceAnchorText
		}
	}

	presetPrompt := ""
	var presetSeed any = nil
	presetSpeaker := ""
	if matchedPreset != nil {
		presetPrompt = strings.TrimSpace(asString(matchedPreset["prompt"]))
		if presetPrompt == "" {
			if engineID == "qwen3_tts" || engineID == "ming_3_tts" || engineID == "breeze_tts2" {
				presetPrompt = strings.TrimSpace(asString(matchedPreset["qwen_instruct"]))
			} else {
				presetPrompt = strings.TrimSpace(asString(matchedPreset["voxcpm_prompt"]))
			}
		}
		if seedVal, ok := asInt(matchedPreset["seed"]); ok && seedVal >= 0 {
			presetSeed = seedVal
		}
		presetSpeaker = strings.TrimSpace(asString(matchedPreset["speaker_id"]))
	}

	payload := []any{
		engineID,
		strings.TrimSpace(asString(engine["repo"])),
		strings.TrimSpace(asString(engine["revision"])),
		strings.TrimSpace(asString(engine["aux_repo"])),
		strings.TrimSpace(asString(engine["aux_revision"])),
		strings.TrimSpace(asString(engine["custom_voice_repo"])),
		strings.TrimSpace(asString(engine["custom_voice_revision"])),
		strings.TrimSpace(asString(engine["source_commit"])),
		normPreset,
		presetPrompt,
		presetSeed,
		presetSpeaker,
		resolvedText,
		strings.TrimSpace(asString(req["voice_prompt"])),
		strings.TrimSpace(asString(req["emotion_prompt"])),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])[:32], true
}

func removeRegularNonSymlinkFile(path string) {
	fi, err := os.Lstat(path)
	if err != nil {
		return
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		return
	}
	_ = os.Remove(path)
}

func (c *ColabOneController) sweepUnindexedTtsPreviewFilesLocked(idx *colabOneLocalTtsPreviewIndex) {
	dir := c.ttsPreviewCacheDir()
	if isSymlinkPath(dir) {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, de := range entries {
		name := de.Name()
		if strings.HasPrefix(name, ".save-") || !strings.HasSuffix(strings.ToLower(name), ".wav") {
			continue
		}
		stem := name[:len(name)-4]
		if idx != nil && idx.ByPreviewID != nil {
			if entry, ok := idx.ByPreviewID[stem]; ok && entry != nil {
				continue
			}
		}
		removeRegularNonSymlinkFile(filepath.Join(dir, name))
	}
}

func evictOldestPendingTtsEntries(m map[string]colabOnePendingTtsEntry, maxEntries, targetEntries int) {
	if len(m) < maxEntries {
		return
	}
	type kv struct {
		key string
		seq uint64
	}
	items := make([]kv, 0, len(m))
	for k, v := range m {
		items = append(items, kv{key: k, seq: v.seq})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].seq != items[j].seq {
			return items[i].seq < items[j].seq
		}
		return items[i].key < items[j].key
	})
	toDrop := len(m) - targetEntries
	if toDrop < 1 {
		toDrop = 1
	}
	for i := 0; i < toDrop && i < len(items); i++ {
		delete(m, items[i].key)
	}
}

func (c *ColabOneController) loadLocalTtsPreviewIndexLocked() *colabOneLocalTtsPreviewIndex {
	if c.ttsPreviewIndex != nil {
		return c.ttsPreviewIndex
	}
	idx := newEmptyLocalTtsPreviewIndex()
	dir := c.ttsPreviewCacheDir()
	idxPath := c.ttsPreviewCacheIndexPath()
	if isSymlinkPath(dir) || isSymlinkPath(idxPath) {
		c.ttsPreviewIndex = idx
		return idx
	}
	raw, err := os.ReadFile(idxPath)
	if err != nil {
		c.ttsPreviewIndex = idx
		return idx
	}
	var parsed colabOneLocalTtsPreviewIndex
	if err := json.Unmarshal(raw, &parsed); err != nil {
		c.ttsPreviewIndex = idx
		c.sweepUnindexedTtsPreviewFilesLocked(idx)
		c.saveLocalTtsPreviewIndexLocked(idx)
		return idx
	}
	if parsed.ByPreviewID == nil {
		parsed.ByPreviewID = make(map[string]*colabOneLocalTtsPreviewEntry)
	}
	if parsed.ByRequestKey == nil {
		parsed.ByRequestKey = make(map[string]string)
	}
	parsed.Version = colabOneLocalTtsPreviewCacheVersion
	c.ttsPreviewIndex = &parsed
	return c.ttsPreviewIndex
}

func (c *ColabOneController) saveLocalTtsPreviewIndexLocked(idx *colabOneLocalTtsPreviewIndex) {
	dir := c.ttsPreviewCacheDir()
	if isSymlinkPath(dir) {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	idxPath := c.ttsPreviewCacheIndexPath()
	if isSymlinkPath(idxPath) {
		return
	}
	_ = AtomicJSON(idxPath, idx)
}

func (c *ColabOneController) rememberPendingTtsPreviewReq(previewID string, req map[string]any) {
	pid := strings.TrimSpace(previewID)
	if !reHex32ID.MatchString(pid) || req == nil {
		return
	}
	sanitized := deepCopyMap(req)
	delete(sanitized, "hf_token")

	c.ttsPreviewCacheMu.Lock()
	defer c.ttsPreviewCacheMu.Unlock()
	if c.pendingTtsPreviewReqs == nil {
		c.pendingTtsPreviewReqs = make(map[string]colabOnePendingTtsEntry)
	}
	if _, exists := c.pendingTtsPreviewReqs[pid]; !exists {
		evictOldestPendingTtsEntries(c.pendingTtsPreviewReqs, 64, 63)
	}
	c.pendingTtsSeq++
	c.pendingTtsPreviewReqs[pid] = colabOnePendingTtsEntry{
		seq:  c.pendingTtsSeq,
		data: sanitized,
	}
}

func (c *ColabOneController) consumePendingTtsPreviewReq(previewID string) map[string]any {
	pid := strings.TrimSpace(previewID)
	if pid == "" {
		return nil
	}
	c.ttsPreviewCacheMu.Lock()
	defer c.ttsPreviewCacheMu.Unlock()
	if c.pendingTtsPreviewReqs == nil {
		return nil
	}
	entry, ok := c.pendingTtsPreviewReqs[pid]
	if !ok {
		return nil
	}
	delete(c.pendingTtsPreviewReqs, pid)
	return entry.data
}

func (c *ColabOneController) rememberPendingTtsPrefetchContext(engineID string, items []map[string]any) {
	eid := strings.TrimSpace(engineID)
	if eid == "" {
		return
	}
	c.ttsPreviewCacheMu.Lock()
	defer c.ttsPreviewCacheMu.Unlock()
	if c.pendingTtsPrefetchCtx == nil {
		c.pendingTtsPrefetchCtx = make(map[string]colabOnePendingTtsEntry)
	}
	for _, it := range items {
		vp := strings.TrimSpace(asString(it["voice_preset"]))
		if vp == "" {
			continue
		}
		key := eid + "::" + vp
		if _, exists := c.pendingTtsPrefetchCtx[key]; !exists {
			evictOldestPendingTtsEntries(c.pendingTtsPrefetchCtx, 128, 127)
		}
		c.pendingTtsSeq++
		c.pendingTtsPrefetchCtx[key] = colabOnePendingTtsEntry{
			seq: c.pendingTtsSeq,
			data: map[string]any{
				"engine_id":      eid,
				"voice_preset":   vp,
				"preview_text":   strings.TrimSpace(asString(it["preview_text"])),
				"voice_prompt":   strings.TrimSpace(asString(it["voice_prompt"])),
				"emotion_prompt": strings.TrimSpace(asString(it["emotion_prompt"])),
			},
		}
	}
}

func (c *ColabOneController) lookupPendingTtsPrefetchContext(engineID, voicePreset string) map[string]any {
	key := strings.TrimSpace(engineID) + "::" + strings.TrimSpace(voicePreset)
	c.ttsPreviewCacheMu.Lock()
	defer c.ttsPreviewCacheMu.Unlock()
	if c.pendingTtsPrefetchCtx == nil {
		return nil
	}
	if entry, ok := c.pendingTtsPrefetchCtx[key]; ok {
		return deepCopyMap(entry.data)
	}
	return nil
}

func (c *ColabOneController) readLocalTtsPreviewEntryLocked(
	idx *colabOneLocalTtsPreviewIndex,
	previewID string,
) (map[string]any, bool) {
	if !reHex32ID.MatchString(previewID) {
		return nil, false
	}
	entry, ok := idx.ByPreviewID[previewID]
	if !ok || entry == nil {
		return nil, false
	}
	dir := c.ttsPreviewCacheDir()
	wavPath := filepath.Join(dir, previewID+".wav")
	if isSymlinkPath(dir) || isSymlinkPath(wavPath) {
		return nil, false
	}
	raw, err := os.ReadFile(wavPath)
	if err != nil || len(raw) == 0 || len(raw) > colabOneMaxTtsPreviewBytes {
		if err == nil {
			removeRegularNonSymlinkFile(wavPath)
		}
		delete(idx.ByPreviewID, previewID)
		if entry.RequestKey != "" && idx.ByRequestKey[entry.RequestKey] == previewID {
			delete(idx.ByRequestKey, entry.RequestKey)
		}
		c.saveLocalTtsPreviewIndexLocked(idx)
		return nil, false
	}
	if entry.Size > 0 && len(raw) != entry.Size {
		removeRegularNonSymlinkFile(wavPath)
		delete(idx.ByPreviewID, previewID)
		if entry.RequestKey != "" && idx.ByRequestKey[entry.RequestKey] == previewID {
			delete(idx.ByRequestKey, entry.RequestKey)
		}
		c.saveLocalTtsPreviewIndexLocked(idx)
		return nil, false
	}
	sum := sha256.Sum256(raw)
	actualSHA := hex.EncodeToString(sum[:])
	if entry.SHA256 != "" && !strings.EqualFold(actualSHA, entry.SHA256) {
		removeRegularNonSymlinkFile(wavPath)
		delete(idx.ByPreviewID, previewID)
		if entry.RequestKey != "" && idx.ByRequestKey[entry.RequestKey] == previewID {
			delete(idx.ByRequestKey, entry.RequestKey)
		}
		c.saveLocalTtsPreviewIndexLocked(idx)
		return nil, false
	}
	entry.UpdatedAt = float64(time.Now().UnixMilli()) / 1000.0
	c.saveLocalTtsPreviewIndexLocked(idx)

	mime := entry.MIME
	if mime == "" {
		mime = "audio/wav"
	}
	return map[string]any{
		"cached":           true,
		"local_cached":     true,
		"preview_id":       entry.PreviewID,
		"engine_id":        entry.EngineID,
		"voice_preset":     entry.VoicePreset,
		"text":             entry.Text,
		"sample_rate":      entry.SampleRate,
		"duration_seconds": entry.DurationSeconds,
		"mime":             mime,
		"size":             len(raw),
		"sha256":           actualSHA,
		"data":             base64.StdEncoding.EncodeToString(raw),
	}, true
}

func (c *ColabOneController) hasValidLocalPreviewFileLocked(
	idx *colabOneLocalTtsPreviewIndex,
	previewID string,
) bool {
	if !reHex32ID.MatchString(previewID) {
		return false
	}
	entry, ok := idx.ByPreviewID[previewID]
	if !ok || entry == nil {
		return false
	}
	dir := c.ttsPreviewCacheDir()
	wavPath := filepath.Join(dir, previewID+".wav")
	if isSymlinkPath(dir) || isSymlinkPath(wavPath) {
		return false
	}
	fi, err := os.Stat(wavPath)
	if err != nil || fi.IsDir() || fi.Size() <= 0 {
		return false
	}
	if entry.Size > 0 && int(fi.Size()) != entry.Size {
		return false
	}
	return true
}

func (c *ColabOneController) readLocalTtsPreview(payload map[string]any) (map[string]any, error) {
	previewID := strings.TrimSpace(asString(payload["preview_id"]))
	if previewID != "" && !reHex32ID.MatchString(previewID) {
		return nil, errors.New("试听音频编号不合法")
	}

	c.ttsPreviewCacheMu.Lock()
	defer c.ttsPreviewCacheMu.Unlock()
	idx := c.loadLocalTtsPreviewIndexLocked()

	if previewID != "" {
		if res, ok := c.readLocalTtsPreviewEntryLocked(idx, previewID); ok {
			return res, nil
		}
	}

	engineID := strings.TrimSpace(asString(payload["engine_id"]))
	if engineID != "" {
		engine := c.findTtsEngineSpec(engineID)
		if reqKey, ok := computeLocalTtsPreviewRequestKey(engine, payload); ok {
			if mappedID, found := idx.ByRequestKey[reqKey]; found {
				if res, ok := c.readLocalTtsPreviewEntryLocked(idx, mappedID); ok {
					return res, nil
				}
			}
		}
	}
	return map[string]any{"cached": false, "local_cached": false}, nil
}

func (c *ColabOneController) lookupLocalTtsPreviewBatch(
	engineID string,
	items []map[string]any,
) []map[string]any {
	engine := c.findTtsEngineSpec(engineID)
	out := make([]map[string]any, 0, len(items))

	c.ttsPreviewCacheMu.Lock()
	defer c.ttsPreviewCacheMu.Unlock()
	idx := c.loadLocalTtsPreviewIndexLocked()

	for _, item := range items {
		vp := strings.TrimSpace(asString(item["voice_preset"]))
		if vp == "" {
			continue
		}
		reqMap := map[string]any{
			"engine_id":      engineID,
			"voice_preset":   vp,
			"preview_text":   strings.TrimSpace(asString(item["preview_text"])),
			"voice_prompt":   strings.TrimSpace(asString(item["voice_prompt"])),
			"emotion_prompt": strings.TrimSpace(asString(item["emotion_prompt"])),
		}
		cached := false
		matchedPID := ""
		if reqKey, ok := computeLocalTtsPreviewRequestKey(engine, reqMap); ok {
			if mappedID, found := idx.ByRequestKey[reqKey]; found && c.hasValidLocalPreviewFileLocked(idx, mappedID) {
				cached = true
				matchedPID = mappedID
			}
		}
		out = append(out, map[string]any{
			"voice_preset": vp,
			"preview_id":   matchedPID,
			"cached":       cached,
			"local_cached": cached,
		})
	}
	return out
}

func (c *ColabOneController) saveLocalTtsPreview(
	req map[string]any,
	meta map[string]any,
	rawAudio []byte,
) {
	if len(rawAudio) == 0 || len(rawAudio) > colabOneMaxTtsPreviewBytes || meta == nil {
		return
	}
	previewID := strings.TrimSpace(asString(meta["preview_id"]))
	if !reHex32ID.MatchString(previewID) {
		return
	}
	engineID := strings.TrimSpace(asString(meta["engine_id"]))
	if engineID == "" && req != nil {
		engineID = strings.TrimSpace(asString(req["engine_id"]))
	}
	voicePreset := strings.TrimSpace(asString(meta["voice_preset"]))
	if voicePreset == "" && req != nil {
		voicePreset = strings.TrimSpace(asString(req["voice_preset"]))
	}
	textVal := strings.TrimSpace(asString(meta["text"]))
	if textVal == "" && req != nil {
		textVal = strings.TrimSpace(asString(req["preview_text"]))
	}

	// Build request map for deterministic local request_key indexing (preset-only previews).
	var effectiveReq map[string]any
	if req != nil {
		effectiveReq = deepCopyMap(req)
	} else if engineID != "" && voicePreset != "" {
		if pfCtx := c.lookupPendingTtsPrefetchContext(engineID, voicePreset); pfCtx != nil {
			effectiveReq = pfCtx
		} else {
			effectiveReq = map[string]any{
				"engine_id":    engineID,
				"voice_preset": voicePreset,
			}
		}
	}
	if effectiveReq != nil {
		if textVal != "" {
			effectiveReq["preview_text"] = textVal
		}
		// Prefer authoritative voice_prompt / emotion_prompt returned by remote metadata
		// when present, eliminating any race across concurrent prefetch scope changes.
		if rawVP, hasVP := meta["voice_prompt"]; hasVP {
			effectiveReq["voice_prompt"] = strings.TrimSpace(asString(rawVP))
		}
		if rawEP, hasEP := meta["emotion_prompt"]; hasEP {
			effectiveReq["emotion_prompt"] = strings.TrimSpace(asString(rawEP))
		}
	}

	requestKey := ""
	if effectiveReq != nil {
		if engine := c.findTtsEngineSpec(engineID); engine != nil {
			if k, ok := computeLocalTtsPreviewRequestKey(engine, effectiveReq); ok {
				requestKey = k
			}
		}
	}

	sum := sha256.Sum256(rawAudio)
	shaHex := hex.EncodeToString(sum[:])
	sampleRate, _ := asInt(meta["sample_rate"])
	durSec := 0.0
	if parsedDur, ok := asFloat(meta["duration_seconds"]); ok && !math.IsNaN(parsedDur) && !math.IsInf(parsedDur, 0) && parsedDur >= 0 {
		durSec = parsedDur
	}
	mime := strings.TrimSpace(asString(meta["mime"]))
	if mime == "" {
		mime = "audio/wav"
	}

	c.ttsPreviewCacheMu.Lock()
	defer c.ttsPreviewCacheMu.Unlock()

	dir := c.ttsPreviewCacheDir()
	if isSymlinkPath(dir) {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	wavPath := filepath.Join(dir, previewID+".wav")
	if isSymlinkPath(wavPath) {
		return
	}
	if err := colabOneWriteFileAtomic(dir, wavPath, rawAudio, 0o600); err != nil {
		return
	}

	idx := c.loadLocalTtsPreviewIndexLocked()
	entry := &colabOneLocalTtsPreviewEntry{
		PreviewID:       previewID,
		RequestKey:      requestKey,
		EngineID:        engineID,
		VoicePreset:     voicePreset,
		Text:            textVal,
		SampleRate:      sampleRate,
		DurationSeconds: durSec,
		MIME:            mime,
		Size:            len(rawAudio),
		SHA256:          shaHex,
		UpdatedAt:       float64(time.Now().UnixMilli()) / 1000.0,
	}
	idx.ByPreviewID[previewID] = entry
	if requestKey != "" {
		idx.ByRequestKey[requestKey] = previewID
	}
	c.pruneLocalTtsPreviewCacheLocked(idx, previewID)
	c.saveLocalTtsPreviewIndexLocked(idx)
}

func (c *ColabOneController) pruneLocalTtsPreviewCacheLocked(
	idx *colabOneLocalTtsPreviewIndex,
	keepID string,
) {
	totalBytes := 0
	entries := make([]*colabOneLocalTtsPreviewEntry, 0, len(idx.ByPreviewID))
	for _, e := range idx.ByPreviewID {
		if e == nil {
			continue
		}
		totalBytes += e.Size
		entries = append(entries, e)
	}
	if len(entries) <= colabOneLocalTtsPreviewMaxEntries && totalBytes <= colabOneLocalTtsPreviewMaxBytes {
		c.sweepUnindexedTtsPreviewFilesLocked(idx)
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].UpdatedAt < entries[j].UpdatedAt
	})
	dir := c.ttsPreviewCacheDir()
	remaining := len(entries)
	for _, e := range entries {
		if remaining <= colabOneLocalTtsPreviewMaxEntries && totalBytes <= colabOneLocalTtsPreviewMaxBytes {
			break
		}
		if e.PreviewID == keepID {
			continue
		}
		removeRegularNonSymlinkFile(filepath.Join(dir, e.PreviewID+".wav"))
		delete(idx.ByPreviewID, e.PreviewID)
		if e.RequestKey != "" && idx.ByRequestKey[e.RequestKey] == e.PreviewID {
			delete(idx.ByRequestKey, e.RequestKey)
		}
		totalBytes -= e.Size
		remaining--
	}
	c.sweepUnindexedTtsPreviewFilesLocked(idx)
}
