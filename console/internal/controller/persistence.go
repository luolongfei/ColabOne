package controller

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	mathrand "math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	colabOneWindowsRenameMaxAttempts = 20
	colabOneWindowsRenameBaseDelay   = 5 * time.Millisecond
	colabOneWindowsRenameMaxDelay    = 50 * time.Millisecond

	colabOneWinErrFileNotFound     syscall.Errno = 2
	colabOneWinErrAccessDenied     syscall.Errno = 5
	colabOneWinErrSharingViolation syscall.Errno = 32
	colabOneWinErrLockViolation    syscall.Errno = 33
)

func colabOneIsTransientWindowsFileErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, fs.ErrPermission) {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case colabOneWinErrFileNotFound,
			colabOneWinErrAccessDenied,
			colabOneWinErrSharingViolation,
			colabOneWinErrLockViolation:
			return true
		}
	}
	return false
}

func colabOneWindowsRetryDelay(attempt int) time.Duration {
	delay := min(time.Duration(attempt)*colabOneWindowsRenameBaseDelay, colabOneWindowsRenameMaxDelay)
	return delay + time.Duration(mathrand.Int64N(int64(colabOneWindowsRenameBaseDelay)))
}

// colabOneRenameReplace renames src onto dst, retrying transient Windows file-sharing
// errors (such as concurrent reads or AV scanning) with bounded jittered backoff.
// When dst is an existing directory, it fails fast.
func colabOneRenameReplace(src, dst string) error {
	return colabOneRenameReplaceWith(
		src,
		dst,
		runtime.GOOS == "windows",
		os.Rename,
		os.Stat,
		time.Sleep,
	)
}

func colabOneRenameReplaceWith(
	src, dst string,
	isWindows bool,
	renameFn func(oldpath, newpath string) error,
	statFn func(name string) (os.FileInfo, error),
	sleepFn func(time.Duration),
) error {
	err := renameFn(src, dst)
	if err == nil || !isWindows {
		return err
	}
	for attempt := 1; attempt < colabOneWindowsRenameMaxAttempts; attempt++ {
		if info, statErr := statFn(dst); statErr == nil && info.IsDir() {
			return err
		}
		if !colabOneIsTransientWindowsFileErr(err) {
			return err
		}
		sleepFn(colabOneWindowsRetryDelay(attempt))
		err = renameFn(src, dst)
		if err == nil {
			return nil
		}
	}
	return err
}

func decodeJSONWithNumbers(raw []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return dec.Decode(dst)
}

// AtomicJSON writes data as indented UTF-8 JSON (without HTML escaping) atomically via temp file + rename.
func AtomicJSON(path string, data any) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("controller: target path is empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".save-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	closed := false
	renamed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		if !renamed {
			_ = os.Remove(tmpName)
		}
	}()

	enc := json.NewEncoder(tmp)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(SanitizeJSONValue(data)); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	if err := colabOneRenameReplace(tmpName, path); err != nil {
		return err
	}
	renamed = true
	return nil
}

var (
	serviceBundleFolders  = []string{"server", "common", "catalog"}
	serviceBundleSuffixes = map[string]bool{
		".py":   true,
		".json": true,
		".txt":  true,
	}
)

// ColabOneServiceRevision computes the deterministic 16-hex-char SHA-256 over the
// packaged remote service files (matching common/files.py colabone_service_revision).
func ColabOneServiceRevision(root string) string {
	if root == "" {
		return ""
	}
	h := sha256.New()
	count := 0
	for _, folder := range serviceBundleFolders {
		folderPath := filepath.Join(root, folder)
		entries, err := os.ReadDir(folderPath)
		if err != nil {
			continue
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			ext := filepath.Ext(name)
			if len(name) <= len(ext) || strings.TrimLeft(name[:len(name)-len(ext)], ".") == "" || !serviceBundleSuffixes[ext] {
				continue
			}
			fullPath := filepath.Join(folderPath, name)
			info, err := os.Stat(fullPath)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			content, err := os.ReadFile(fullPath)
			if err != nil {
				continue
			}
			rel := folder + "/" + name
			h.Write([]byte(rel))
			h.Write([]byte{0})
			h.Write(content)
			h.Write([]byte{0})
			count++
		}
	}
	if count == 0 {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

type fsServiceBundler struct {
	root string
}

func (b fsServiceBundler) ServiceRevision() string {
	return ColabOneServiceRevision(b.root)
}

func (b fsServiceBundler) ReadPresetsJSON() ([]byte, error) {
	if b.root == "" {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(filepath.Join(b.root, "catalog", "presets.json"))
}

func (b fsServiceBundler) ReadComfyCatalogJSON() ([]byte, error) {
	if b.root == "" {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(filepath.Join(b.root, "catalog", "comfyui.json"))
}

// ---------------------------------------------------------------------------
// Default stdlib ActivityLogger
// ---------------------------------------------------------------------------

// ColabOneActivityLog is a bounded, credential-redacting activity feed with disk rotation.
type ColabOneActivityLog struct {
	mu       sync.Mutex
	items    []ColabOneActivityEntry
	limit    int
	sequence int64
	logPath  string
	maxBytes int64
}

// NewColabOneActivityLog constructs an activity log optionally mirrored to logPath.
func NewColabOneActivityLog(logPath string, limit int, maxBytes int64) *ColabOneActivityLog {
	if limit <= 0 {
		limit = 300
	}
	if maxBytes <= 0 {
		maxBytes = 256 * 1024
	}
	if maxBytes < 16*1024 {
		maxBytes = 16 * 1024
	}
	return &ColabOneActivityLog{
		items:    make([]ColabOneActivityEntry, 0, limit),
		limit:    limit,
		logPath:  logPath,
		maxBytes: maxBytes,
	}
}

var colabOneDiskLineReplacer = strings.NewReplacer("\r", " ", "\n", " ")

func (a *ColabOneActivityLog) appendDiskLocked(ts float64, level, source, message string) {
	if a.logPath == "" {
		return
	}
	dir := filepath.Dir(a.logPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	if info, err := os.Stat(a.logPath); err == nil && info.Size() >= a.maxBytes {
		backup := a.logPath + ".1"
		if err := colabOneRenameReplace(a.logPath, backup); err != nil {
			if os.Remove(backup) == nil {
				_ = colabOneRenameReplace(a.logPath, backup)
			}
		}
	}
	sec := int64(ts)
	nsec := int64((ts - float64(sec)) * 1e9)
	stamp := time.Unix(sec, nsec).Format("2006-01-02 15:04:05")
	lineMsg := colabOneDiskLineReplacer.Replace(message)
	lineSrc := colabOneDiskLineReplacer.Replace(source)
	lineLvl := strings.ToUpper(colabOneDiskLineReplacer.Replace(level))
	line := fmt.Sprintf("%s [%s] [%s] %s\n", stamp, lineLvl, lineSrc, lineMsg)
	_, statErr := os.Stat(a.logPath)
	created := os.IsNotExist(statErr)
	f, err := os.OpenFile(a.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = f.WriteString(line)
	_ = f.Close()
	if created {
		_ = os.Chmod(a.logPath, 0o600)
	}
}

// Add records a redacted entry to memory and disk.
func (a *ColabOneActivityLog) Add(message, source, level string) {
	if source == "" {
		source = "客户端"
	}
	if level == "" {
		level = "info"
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sequence++
	now := nowUnixFloat()
	cleanMsg := SafeMessage(message)
	cleanSrc := truncateRunes(SafeMessage(source), 40)
	cleanLvl := truncateRunes(strings.ToLower(level), 16)
	entry := ColabOneActivityEntry{
		ID:      a.sequence,
		Time:    now,
		Source:  cleanSrc,
		Level:   cleanLvl,
		Message: cleanMsg,
	}
	if len(a.items) >= a.limit {
		copy(a.items, a.items[1:])
		a.items[len(a.items)-1] = entry
	} else {
		a.items = append(a.items, entry)
	}
	a.appendDiskLocked(now, cleanLvl, cleanSrc, cleanMsg)
}

// Snapshot returns a copy of all retained entries.
func (a *ColabOneActivityLog) Snapshot() []ColabOneActivityEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]ColabOneActivityEntry, len(a.items))
	copy(out, a.items)
	return out
}

// Clear removes all in-memory activity entries.
func (a *ColabOneActivityLog) Clear() {
	a.mu.Lock()
	a.items = a.items[:0]
	a.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Settings persistence & migration
// ---------------------------------------------------------------------------

func (c *ColabOneController) loadSettings() {
	c.settings = map[string]any{
		"gpu":                    COLABONE_DEFAULT_GPU,
		"idle_minutes":           30,
		"idle_countdown_seconds": 60,
		"auto_drive":             true,
		"download_threads":       16,
		"assign_proxy_enabled":   false,
		"locale":                 "",
	}
	settingsPath := filepath.Join(c.directory, "settings.json")
	if rawBytes, err := os.ReadFile(settingsPath); err == nil {
		var saved map[string]any
		if decodeJSONWithNumbers(rawBytes, &saved) == nil && saved != nil {
			allowed := []string{
				"gpu",
				"idle_minutes",
				"idle_countdown_seconds",
				"auto_drive",
				"download_threads",
				"assign_proxy_enabled",
				"idle_policy",
				"rclone_drive_policy",
				"default_gpu_policy",
				"locale",
			}
			for _, k := range allowed {
				if val, ok := saved[k]; ok {
					c.settings[k] = val
				}
			}
		}
	}

	if gpu, ok := c.settings["gpu"].(string); !ok || !validColabOneGPUs[gpu] {
		c.settings["gpu"] = COLABONE_DEFAULT_GPU
	}

	if idleVal, ok := asInt(c.settings["idle_minutes"]); !ok || idleVal < 1 || idleVal > 240 {
		c.settings["idle_minutes"] = 30
	} else {
		c.settings["idle_minutes"] = idleVal
	}

	if countdownVal, ok := asInt(c.settings["idle_countdown_seconds"]); ok {
		c.settings["idle_countdown_seconds"] = countdownVal
	} else {
		c.settings["idle_countdown_seconds"] = 60
	}

	if _, ok := isStrictBool(c.settings["auto_drive"]); !ok {
		c.settings["auto_drive"] = true
	}
	if _, ok := isStrictBool(c.settings["assign_proxy_enabled"]); !ok {
		c.settings["assign_proxy_enabled"] = false
	}

	loc, ok := c.settings["locale"].(string)
	if !ok || (loc != "" && !validColabOneLocales[loc]) {
		c.settings["locale"] = ""
		loc = ""
	}
	if loc != "" && c.auth != nil {
		c.auth.SetLocale(loc)
	}

	// Migrate legacy 8-thread setting to the 16 / 32 concurrency options.
	threads, ok := asExactInt(c.settings["download_threads"])
	if !ok || (threads != 16 && threads != 32) {
		c.settings["download_threads"] = 16
	} else {
		c.settings["download_threads"] = threads
	}

	// Migrate legacy 3-minute policy to the 30-minute idle auto-release default.
	idlePolicy, ok := asExactInt(c.settings["idle_policy"])
	if !ok || idlePolicy != 3 {
		c.settings["idle_minutes"] = 30
		c.settings["idle_countdown_seconds"] = 60
		c.settings["idle_policy"] = 3
	} else {
		c.settings["idle_policy"] = 3
	}

	// Enable headless OAuth/rclone Drive persistence by default once upgraded.
	rclonePolicy, ok := asExactInt(c.settings["rclone_drive_policy"])
	if !ok || rclonePolicy != 1 {
		c.settings["auto_drive"] = true
		c.settings["rclone_drive_policy"] = 1
	} else {
		c.settings["rclone_drive_policy"] = 1
	}

	// Migrate legacy T4 default GPU to G4 once upgraded while preserving explicit non-T4 selections.
	defaultGPUPolicy, ok := asExactInt(c.settings["default_gpu_policy"])
	if !ok || defaultGPUPolicy != COLABONE_DEFAULT_GPU_POLICY_VERSION {
		if c.settings["gpu"] == "T4" {
			c.settings["gpu"] = COLABONE_DEFAULT_GPU
		}
		c.settings["default_gpu_policy"] = COLABONE_DEFAULT_GPU_POLICY_VERSION
	} else {
		c.settings["default_gpu_policy"] = COLABONE_DEFAULT_GPU_POLICY_VERSION
	}
}

// ---------------------------------------------------------------------------
// Library persistence & seeding (library.json)
// ---------------------------------------------------------------------------

func (c *ColabOneController) validateModelSpec(raw map[string]any) (map[string]any, error) {
	if c.models != nil {
		return c.models.ValidateModelSpec(raw)
	}
	// Minimal structural fallback when no ModelResolver is injected.
	id := asTrimmedString(raw["id"])
	if !reModelID.MatchString(id) {
		return nil, fmt.Errorf("模型 ID 不合法")
	}
	name := asTrimmedString(raw["name"])
	if name == "" {
		return nil, fmt.Errorf("模型名称不能为空")
	}
	files := asMapSlice(raw["files"])
	if len(files) == 0 {
		return nil, fmt.Errorf("缺少模型文件列表")
	}
	cp := deepCopyMap(raw)
	cp["id"] = id
	cp["name"] = name
	return cp, nil
}

func (c *ColabOneController) validateAPIModelID(value string, allowEmpty bool) (string, error) {
	if c.models != nil {
		return c.models.ValidateAPIModelID(value, allowEmpty)
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		if allowEmpty {
			return "", nil
		}
		return "", fmt.Errorf("模型 API ID 不能为空")
	}
	if strings.EqualFold(trimmed, "colabone/auto") {
		return "", fmt.Errorf("colabone/auto 为系统保留别名，请使用其他模型 ID")
	}
	return trimmed, nil
}

func (c *ColabOneController) resolveAPIModelID(model map[string]any) string {
	if c.models != nil {
		return c.models.ResolveAPIModelID(model)
	}
	if explicit := asTrimmedString(model["api_id"]); explicit != "" {
		return explicit
	}
	return asTrimmedString(model["id"])
}

func (c *ColabOneController) uniqueAPIModelID(baseAPIID string, existingIDs []string, repo, filename string) (string, error) {
	if c.models != nil {
		return c.models.UniqueAPIModelID(baseAPIID, existingIDs, repo, filename)
	}
	taken := make(map[string]bool, len(existingIDs)+1)
	for _, id := range existingIDs {
		if s := strings.TrimSpace(id); s != "" {
			taken[strings.ToLower(s)] = true
		}
	}
	taken["colabone/auto"] = true
	if !taken[strings.ToLower(baseAPIID)] {
		return baseAPIID, nil
	}
	for i := 2; i < 1000; i++ {
		cand := fmt.Sprintf("%s-%d", baseAPIID, i)
		if !taken[strings.ToLower(cand)] {
			return cand, nil
		}
	}
	return baseAPIID, nil
}

func (c *ColabOneController) parseHFInput(repoOrURL, filename, revision string) (string, string, string) {
	if c.models != nil {
		return c.models.ParseHFInput(repoOrURL, filename, revision)
	}
	r := strings.Trim(strings.TrimSpace(repoOrURL), "/")
	fn := strings.TrimSpace(filename)
	rev := strings.TrimSpace(revision)
	if rev == "" {
		rev = "main"
	}
	return r, fn, rev
}

func (c *ColabOneController) loadLibrary() {
	path := filepath.Join(c.directory, "library.json")
	info, statErr := os.Stat(path)
	existed := statErr == nil && !info.IsDir()

	var raw map[string]any
	if existed {
		if b, err := os.ReadFile(path); err == nil {
			var parsed map[string]any
			if decodeJSONWithNumbers(b, &parsed) == nil && parsed != nil {
				raw = parsed
			}
		}
	}

	seenRepos := make(map[string]bool)
	commonRepos := make([]map[string]any, 0)

	if raw != nil {
		rawIDsVal, hasCommonIDs := raw["common_ids"]
		if !hasCommonIDs {
			rawIDsVal = raw["starred_ids"]
		}
		for _, item := range asAnySlice(rawIDsVal) {
			s := asString(item)
			if reModelID.MatchString(s) {
				c.commonIDs[s] = true
			}
		}

		for _, item := range asMapSlice(raw["custom_models"]) {
			spec, err := c.validateModelSpec(item)
			if err != nil || spec == nil {
				continue
			}
			mid := asString(spec["id"])
			if strings.HasPrefix(mid, "hf-") {
				spec["min_vram_mib"] = 0
			}
			c.customModels[mid] = spec
		}

		if rawOverrides := asMap(raw["api_id_overrides"]); rawOverrides != nil {
			for rawMid, rawAlias := range rawOverrides {
				midStr := strings.TrimSpace(rawMid)
				if !reModelID.MatchString(midStr) {
					continue
				}
				cleanAlias, err := c.validateAPIModelID(asString(rawAlias), false)
				if err != nil {
					continue
				}
				c.apiIDOverrides[midStr] = cleanAlias
			}
		}

		rawReposVal, hasCommonRepos := raw["common_repos"]
		if !hasCommonRepos {
			rawReposVal = raw["bookmarks"]
		}
		for _, item := range asMapSlice(rawReposVal) {
			repo := asTrimmedString(item["repo"])
			repoLow := strings.ToLower(repo)
			if !reRepoSlug.MatchString(repo) || seenRepos[repoLow] {
				continue
			}
			seenRepos[repoLow] = true
			defaultName := repo
			if idx := strings.LastIndex(repo, "/"); idx >= 0 {
				defaultName = repo[idx+1:]
			}
			name := asString(item["name"])
			if name == "" {
				name = defaultName
			}
			dl, _ := asInt(item["downloads"])
			likes, _ := asInt(item["likes"])
			addedAt, ok := asFloat(item["added_at"])
			if !ok || addedAt == 0 {
				addedAt = nowUnixFloat()
			}
			commonRepos = append(commonRepos, map[string]any{
				"repo":      repo,
				"name":      truncateRunes(name, 120),
				"note":      truncateRunes(asString(item["note"]), 200),
				"filename":  truncateRunes(asString(item["filename"]), 200),
				"downloads": dl,
				"likes":     likes,
				"added_at":  addedAt,
			})
		}

		if ver, ok := asInt(raw["default_common_version"]); ok {
			c.defaultCommonVersion = ver
		} else {
			c.defaultCommonVersion = 0
		}
	}

	// Seed default common model repositories once (preserving any manual edits thereafter).
	seeded := false
	if c.commonIDs["qwen25-05b"] {
		if _, isCustom := c.customModels["qwen25-05b"]; !isCustom {
			delete(c.commonIDs, "qwen25-05b")
			seeded = true
		}
	}
	if c.defaultCommonVersion < COLABONE_DEFAULT_COMMON_VERSION {
		now := nowUnixFloat()
		for _, def := range COLABONE_DEFAULT_COMMON_REPOS {
			key := strings.ToLower(def.Repo)
			if seenRepos[key] {
				continue
			}
			seenRepos[key] = true
			commonRepos = append(commonRepos, map[string]any{
				"repo":      def.Repo,
				"name":      def.Name,
				"note":      def.Note,
				"filename":  "",
				"downloads": 0,
				"likes":     0,
				"added_at":  now,
			})
		}
		c.defaultCommonVersion = COLABONE_DEFAULT_COMMON_VERSION
		seeded = true
	}

	c.commonRepos = commonRepos
	if existed && raw == nil {
		if c.activity != nil {
			c.log("读取本地模型库配置失败，已保留原文件未覆盖", "模型", "warning")
		}
	} else if existed && raw != nil && seeded {
		_ = c.saveLibrary()
	}
}

func (c *ColabOneController) saveLibrary() error {
	c.persistMu.Lock()
	defer c.persistMu.Unlock()

	c.mu.Lock()
	commonIDs := make([]string, 0, len(c.commonIDs))
	for id := range c.commonIDs {
		commonIDs = append(commonIDs, id)
	}
	sort.Strings(commonIDs)

	customModels := make([]map[string]any, 0, len(c.customModels))
	for _, spec := range c.customModels {
		customModels = append(customModels, deepCopyMap(spec))
	}
	overrides := make(map[string]string, len(c.apiIDOverrides))
	for k, v := range c.apiIDOverrides {
		overrides[k] = v
	}
	repos := deepCopyMapSlice(c.commonRepos)
	payload := map[string]any{
		"default_common_version": c.defaultCommonVersion,
		"common_ids":             commonIDs,
		"custom_models":          customModels,
		"api_id_overrides":       overrides,
		"common_repos":           repos,
	}
	c.mu.Unlock()
	return AtomicJSON(filepath.Join(c.directory, "library.json"), payload)
}

// ---------------------------------------------------------------------------
// ComfyUI workflow persistence & merging (workflows.json)
// ---------------------------------------------------------------------------

func (c *ColabOneController) loadWorkflows() {
	path := filepath.Join(c.directory, "workflows.json")
	rawBytes, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var raw any
	if decodeJSONWithNumbers(rawBytes, &raw) != nil {
		return
	}
	var items []map[string]any
	switch v := raw.(type) {
	case []any:
		items = asMapSlice(v)
	case map[string]any:
		if wfList, ok := v["workflows"].([]any); ok {
			items = asMapSlice(wfList)
		} else {
			for _, val := range v {
				if m := asMap(val); m != nil {
					items = append(items, m)
				}
			}
		}
	default:
		return
	}
	builtinIDs := c.builtinWorkflowIDSet()
	for _, item := range items {
		if item == nil {
			continue
		}
		candidate := deepCopyMap(item)
		candidate["source"] = "custom"
		wf, err := c.validateWorkflowTemplate(candidate)
		if err != nil || wf == nil {
			continue
		}
		id := asString(wf["id"])
		if id == "" || builtinIDs[id] {
			continue
		}
		c.customWorkflows[id] = wf
	}
}

func (c *ColabOneController) validateWorkflowTemplate(raw map[string]any) (map[string]any, error) {
	if c.comfy != nil {
		return c.comfy.ValidateWorkflowTemplate(raw)
	}
	id := asString(raw["id"])
	if !reModelID.MatchString(id) {
		return nil, fmt.Errorf("工作流 ID 不合法")
	}
	if asTrimmedString(raw["name"]) == "" {
		return nil, fmt.Errorf("工作流名称不能为空")
	}
	return deepCopyMap(raw), nil
}

func (c *ColabOneController) saveWorkflows() error {
	c.persistMu.Lock()
	defer c.persistMu.Unlock()

	c.mu.Lock()
	list := make([]map[string]any, 0, len(c.customWorkflows))
	for _, wf := range c.customWorkflows {
		list = append(list, deepCopyMap(wf))
	}
	c.mu.Unlock()
	return AtomicJSON(filepath.Join(c.directory, "workflows.json"), list)
}

func (c *ColabOneController) loadBuiltinComfyCatalog() {
	c.builtinWFsOnce.Do(func() {
		c.cachedBuiltinWFs = []map[string]any{}
		c.cachedBuiltinTtsEngines = []map[string]any{}
		rawBytes, err := c.bundler.ReadComfyCatalogJSON()
		if err != nil || len(rawBytes) == 0 {
			return
		}
		var raw map[string]any
		if decErr := decodeJSONWithNumbers(rawBytes, &raw); decErr != nil || raw == nil {
			if c.activity != nil {
				c.log("解析内置 ComfyUI 目录失败，已回退为空目录："+SafeMessage(fmt.Sprint(decErr)), "ComfyUI", "warning")
			}
			return
		}
		if bundleValidator, ok := c.comfy.(ComfyCatalogValidator); ok {
			bundle, valErr := bundleValidator.ValidateCatalogBundle(raw)
			if valErr != nil {
				if c.activity != nil {
					c.log("校验内置 ComfyUI 目录失败，已回退为空目录："+SafeMessage(valErr.Error()), "ComfyUI", "warning")
				}
				return
			}
			if bundle.Templates != nil {
				c.cachedBuiltinWFs = deepCopyMapSlice(bundle.Templates)
			}
			if bundle.TtsEngines != nil {
				c.cachedBuiltinTtsEngines = deepCopyMapSlice(bundle.TtsEngines)
			}
			return
		}
		if c.comfy != nil {
			templates, valErr := c.comfy.ValidateCatalog(raw)
			if valErr != nil || templates == nil {
				if valErr != nil && c.activity != nil {
					c.log("校验内置 ComfyUI 模板目录失败，已回退为空目录："+SafeMessage(valErr.Error()), "ComfyUI", "warning")
				}
				return
			}
			c.cachedBuiltinWFs = deepCopyMapSlice(templates)
		} else if templates := asMapSlice(raw["templates"]); templates != nil {
			c.cachedBuiltinWFs = deepCopyMapSlice(templates)
		}
		if engines := asMapSlice(raw["tts_engines"]); engines != nil {
			c.cachedBuiltinTtsEngines = deepCopyMapSlice(engines)
		}
	})
}

func (c *ColabOneController) builtinWorkflows() []map[string]any {
	c.loadBuiltinComfyCatalog()
	return deepCopyMapSlice(c.cachedBuiltinWFs)
}

func (c *ColabOneController) builtinWorkflowIDSet() map[string]bool {
	builtins := c.builtinWorkflows()
	ids := make(map[string]bool, len(builtins))
	for _, wf := range builtins {
		if id := asString(wf["id"]); id != "" {
			ids[id] = true
		}
	}
	return ids
}

func (c *ColabOneController) builtinTtsEngines() []map[string]any {
	c.loadBuiltinComfyCatalog()
	return deepCopyMapSlice(c.cachedBuiltinTtsEngines)
}

func (c *ColabOneController) mergeTtsEngines(remote []map[string]any) []map[string]any {
	builtins := c.builtinTtsEngines()
	builtinIDs := make(map[string]bool, len(builtins))
	merged := make(map[string]map[string]any, len(builtins)+len(remote))
	order := make([]string, 0, len(builtins)+len(remote))

	for _, eng := range builtins {
		if eng == nil {
			continue
		}
		id := asString(eng["id"])
		if id == "" {
			continue
		}
		if _, exists := merged[id]; !exists {
			order = append(order, id)
		}
		builtinIDs[id] = true
		merged[id] = deepCopyMap(eng)
	}

	for _, eng := range remote {
		if eng == nil {
			continue
		}
		id := asString(eng["id"])
		if id == "" {
			continue
		}
		if builtinIDs[id] {
			// Keep embedded catalog authoritative for built-in TTS engine metadata
			// (matching mergeWorkflowsWithLocal's built-in policy so Snapshot().workflows
			// and Snapshot().tts_engines never split-brain on older runtimes).
			continue
		}
		if _, exists := merged[id]; !exists {
			order = append(order, id)
		}
		merged[id] = deepCopyMap(eng)
	}

	out := make([]map[string]any, 0, len(order))
	for _, id := range order {
		if eng := merged[id]; eng != nil {
			out = append(out, eng)
		}
	}
	return out
}

func (c *ColabOneController) mergeWorkflows(remoteWorkflows []map[string]any) []map[string]any {
	c.mu.Lock()
	localMap := make(map[string]map[string]any, len(c.customWorkflows))
	for wid, wf := range c.customWorkflows {
		localMap[wid] = deepCopyMap(wf)
	}
	c.mu.Unlock()
	return c.mergeWorkflowsWithLocal(localMap, remoteWorkflows)
}

func (c *ColabOneController) mergeWorkflowsLocked(remoteWorkflows []map[string]any) []map[string]any {
	localMap := make(map[string]map[string]any, len(c.customWorkflows))
	for wid, wf := range c.customWorkflows {
		localMap[wid] = deepCopyMap(wf)
	}
	return c.mergeWorkflowsWithLocal(localMap, remoteWorkflows)
}

func (c *ColabOneController) mergeWorkflowsWithLocal(localMap map[string]map[string]any, remoteWorkflows []map[string]any) []map[string]any {
	builtins := c.builtinWorkflows()
	builtinOrder := make(map[string]int, len(builtins))
	merged := make(map[string]map[string]any, len(builtins)+len(localMap)+len(remoteWorkflows))
	for i, wf := range builtins {
		if id := asString(wf["id"]); id != "" {
			builtinOrder[id] = i
			merged[id] = deepCopyMap(wf)
		}
	}
	for wid, wf := range localMap {
		if wf == nil || wid == "" {
			continue
		}
		if _, isBuiltin := builtinOrder[wid]; isBuiltin {
			continue
		}
		prev, exists := merged[wid]
		wfTS, _ := asFloat(wf["updated_at"])
		prevTS, _ := asFloat(prev["updated_at"])
		if !exists || wfTS >= prevTS {
			merged[wid] = wf
		}
	}
	for _, wf := range remoteWorkflows {
		if wf == nil {
			continue
		}
		wid := asString(wf["id"])
		if wid == "" {
			continue
		}
		if _, isBuiltin := builtinOrder[wid]; isBuiltin {
			// Keep embedded catalog authoritative for built-in template metadata;
			// never overwrite with stale remote/cached built-ins (updated_at == 0).
			continue
		}
		if asString(wf["source"]) == "builtin" {
			// Do not pull in retired remote built-in templates absent from builtinOrder.
			continue
		}
		prev, exists := merged[wid]
		wfTS, _ := asFloat(wf["updated_at"])
		prevTS := -1.0
		if exists {
			prevTS, _ = asFloat(prev["updated_at"])
		}
		_, inLocal := localMap[wid]
		if !exists || wfTS > prevTS || (wfTS == prevTS && !inLocal) {
			merged[wid] = deepCopyMap(wf)
		}
	}

	result := make([]map[string]any, 0, len(merged))
	for _, wf := range merged {
		result = append(result, wf)
	}
	sort.Slice(result, func(i, j int) bool {
		wi, wj := result[i], result[j]
		bi := asString(wi["source"]) == "builtin"
		bj := asString(wj["source"]) == "builtin"
		if bi != bj {
			return bi
		}
		idI := asString(wi["id"])
		idJ := asString(wj["id"])
		if bi {
			oi, okI := builtinOrder[idI]
			if !okI {
				oi = 9999
			}
			oj, okJ := builtinOrder[idJ]
			if !okJ {
				oj = 9999
			}
			if oi != oj {
				return oi < oj
			}
		} else {
			tsI, _ := asFloat(wi["updated_at"])
			tsJ, _ := asFloat(wj["updated_at"])
			if tsI != tsJ {
				return tsI > tsJ
			}
		}
		return idI < idJ
	})
	return result
}

// ---------------------------------------------------------------------------
// Presets & model decoration
// ---------------------------------------------------------------------------

func (c *ColabOneController) rawPresets() []map[string]any {
	c.presetsOnce.Do(func() {
		rawBytes, err := c.bundler.ReadPresetsJSON()
		if err != nil || len(rawBytes) == 0 {
			c.cachedRawPresets = []map[string]any{}
			return
		}
		var list []map[string]any
		if decodeJSONWithNumbers(rawBytes, &list) != nil || list == nil {
			c.cachedRawPresets = []map[string]any{}
			return
		}
		c.cachedRawPresets = list
	})
	return c.cachedRawPresets
}

// Presets returns the built-in model presets decorated with default downloaded/loaded/size fields.
func (c *ColabOneController) Presets() []map[string]any {
	raw := c.rawPresets()
	out := make([]map[string]any, 0, len(raw))
	for _, m := range raw {
		if m == nil {
			continue
		}
		cp := deepCopyMap(m)
		cp["downloaded"] = false
		cp["loaded"] = false
		cp["size"] = sumFileSizes(asMapSlice(m["files"]))
		out = append(out, cp)
	}
	return out
}

func (c *ColabOneController) decorateModels(baseModels []map[string]any, rt map[string]any) []map[string]any {
	rawPresets := c.rawPresets()
	presetMap := make(map[string]map[string]any, len(rawPresets))
	for _, m := range rawPresets {
		if id := asString(m["id"]); id != "" {
			presetMap[id] = m
		}
	}
	remoteReady := rt != nil && asString(rt["state"]) == "ready"
	legacyRemote := remoteReady && !asBool(rt["api_model_aliases"]) && asBool(rt["upgrade_available"])

	c.mu.Lock()
	commonIDs := make(map[string]bool, len(c.commonIDs))
	for k, v := range c.commonIDs {
		commonIDs[k] = v
	}
	customMap := make(map[string]map[string]any, len(c.customModels))
	for k, v := range c.customModels {
		customMap[k] = deepCopyMap(v)
	}
	apiOverrides := make(map[string]string, len(c.apiIDOverrides))
	for k, v := range c.apiIDOverrides {
		apiOverrides[k] = v
	}
	c.mu.Unlock()

	combined := make([]map[string]any, 0, len(baseModels)+len(customMap))
	present := make(map[string]bool, len(baseModels))
	for _, m := range baseModels {
		if m == nil {
			continue
		}
		cp := deepCopyMap(m)
		combined = append(combined, cp)
		present[asString(cp["id"])] = true
	}

	// Sort custom model IDs for deterministic ordering when appending local-only custom models.
	customIDs := make([]string, 0, len(customMap))
	for mid := range customMap {
		if !present[mid] {
			customIDs = append(customIDs, mid)
		}
	}
	sort.Strings(customIDs)
	for _, mid := range customIDs {
		spec := customMap[mid]
		cp := deepCopyMap(spec)
		cp["downloaded"] = false
		cp["loaded"] = false
		cp["size"] = sumFileSizes(asMapSlice(spec["files"]))
		cp["_local_only"] = true
		combined = append(combined, cp)
	}

	out := make([]map[string]any, 0, len(combined))
	for _, m := range combined {
		localOnly := asBool(m["_local_only"])
		delete(m, "_local_only")
		hasRemoteAPIID := asTrimmedString(m["api_id"]) != ""
		files := asMapSlice(m["files"])
		repo := asString(m["repo"])
		if len(files) > 0 && files[0] != nil {
			repo = asString(files[0]["repo"])
		}
		mid := asString(m["id"])
		presetAPIID := ""
		if p := presetMap[mid]; p != nil {
			presetAPIID = asTrimmedString(p["api_id"])
		}
		localCustomAPIID := ""
		if cm := customMap[mid]; cm != nil {
			localCustomAPIID = asTrimmedString(cm["api_id"])
		}
		override := apiOverrides[mid]
		remoteAPIID := ""
		if !localOnly {
			remoteAPIID = asTrimmedString(m["api_id"])
		}
		desiredAPIID := override
		if desiredAPIID == "" {
			desiredAPIID = remoteAPIID
		}
		if desiredAPIID == "" {
			desiredAPIID = localCustomAPIID
		}
		if desiredAPIID == "" {
			desiredAPIID = presetAPIID
		}
		if desiredAPIID == "" {
			desiredAPIID = c.resolveAPIModelID(m)
		}

		var effectiveAPIID, pendingAPIID string
		if legacyRemote && !localOnly && !hasRemoteAPIID {
			effectiveAPIID = mid
			if desiredAPIID != mid {
				pendingAPIID = desiredAPIID
			}
		} else {
			effectiveAPIID = desiredAPIID
		}

		// m was already freshly deep-copied when building combined above.
		m["api_id"] = effectiveAPIID
		m["repo"] = repo
		m["common"] = commonIDs[mid]
		_, isPreset := presetMap[mid]
		m["custom"] = !isPreset
		if pendingAPIID != "" {
			m["pending_api_id"] = pendingAPIID
		} else {
			delete(m, "pending_api_id")
		}
		out = append(out, m)
	}
	return out
}
