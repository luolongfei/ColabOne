package controller

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	colabOneWorkflowDraftsSchemaVersion = 1
	colabOneWorkflowDraftsDirName       = "comfyui-drafts"
	colabOneWorkflowDraftsFileName      = "drafts.json"
	colabOneWorkflowDraftsMediaDirName  = "media"

	colabOneMaxWorkflowDraftFields           = 128
	colabOneMaxWorkflowDraftFieldStringBytes = 200000
	colabOneMaxWorkflowDraftFilenameRunes    = 180
	colabOneMaxWorkflowDraftImageBytes       = 16 * 1024 * 1024
	colabOneMaxWorkflowDraftAudioBytes       = 64 * 1024 * 1024
)

var (
	reWorkflowDraftFieldKey = regexp.MustCompile(`^[a-zA-Z0-9_.:-]{1,160}$`)
	reWorkflowDraftSHA256   = regexp.MustCompile(`^[a-f0-9]{64}$`)

	colabOneDraftImageMIME = map[string]string{
		"png":  "image/png",
		"jpg":  "image/jpeg",
		"jpeg": "image/jpeg",
		"webp": "image/webp",
	}

	colabOneDraftAudioMIME = map[string]string{
		"wav":  "audio/wav",
		"mp3":  "audio/mpeg",
		"flac": "audio/flac",
		"ogg":  "audio/ogg",
		"m4a":  "audio/mp4",
	}
)

// ColabOneWorkflowDraftMedia describes a locally persisted ComfyUI workflow input
// media file (image or audio) stored under comfyui-drafts/media/.
type ColabOneWorkflowDraftMedia struct {
	ID        string  `json:"id"`
	Filename  string  `json:"filename"`
	Kind      string  `json:"kind"` // "image" | "audio"
	MIME      string  `json:"mime"`
	Size      int64   `json:"size"`
	SHA256    string  `json:"sha256"`
	UpdatedAt float64 `json:"updated_at"`
}

// ColabOneWorkflowDraft holds locally persisted ComfyUI workflow field values and
// input media references for a single workflow ID.
type ColabOneWorkflowDraft struct {
	WorkflowID string                                `json:"workflow_id"`
	Fields     map[string]any                        `json:"fields"`
	Media      map[string]ColabOneWorkflowDraftMedia `json:"media"`
	UpdatedAt  float64                               `json:"updated_at"`
}

type colabOneWorkflowDraftsDiskFile struct {
	Version int                               `json:"version"`
	Drafts  map[string]*ColabOneWorkflowDraft `json:"drafts"`
}

func (c *ColabOneController) workflowDraftsDir() string {
	if strings.TrimSpace(c.directory) == "" {
		return ""
	}
	return filepath.Join(c.directory, colabOneWorkflowDraftsDirName)
}

func (c *ColabOneController) workflowDraftsFilePath() string {
	dir := c.workflowDraftsDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, colabOneWorkflowDraftsFileName)
}

func (c *ColabOneController) workflowDraftsMediaDir() string {
	dir := c.workflowDraftsDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, colabOneWorkflowDraftsMediaDirName)
}

func cloneWorkflowDraft(src *ColabOneWorkflowDraft) *ColabOneWorkflowDraft {
	if src == nil {
		return nil
	}
	fields := make(map[string]any, len(src.Fields))
	for k, v := range src.Fields {
		fields[k] = deepCopyAny(v)
	}
	media := make(map[string]ColabOneWorkflowDraftMedia, len(src.Media))
	for k, v := range src.Media {
		media[k] = v
	}
	return &ColabOneWorkflowDraft{
		WorkflowID: src.WorkflowID,
		Fields:     fields,
		Media:      media,
		UpdatedAt:  src.UpdatedAt,
	}
}

func cloneWorkflowDraftMap(src map[string]*ColabOneWorkflowDraft) map[string]*ColabOneWorkflowDraft {
	out := make(map[string]*ColabOneWorkflowDraft, len(src))
	for k, v := range src {
		if v != nil {
			out[k] = cloneWorkflowDraft(v)
		}
	}
	return out
}

func validateWorkflowDraftID(raw any) (string, error) {
	wfID, ok := raw.(string)
	if !ok || !reModelID.MatchString(wfID) {
		return "", errors.New("工作流 ID 不合法")
	}
	return wfID, nil
}

func validateWorkflowDraftFieldKey(raw any) (string, error) {
	key, ok := raw.(string)
	if !ok || key == "" || utf8.RuneCountInString(key) > 160 || !reWorkflowDraftFieldKey.MatchString(key) {
		return "", errors.New("草稿字段键不合法")
	}
	return key, nil
}

func validateWorkflowDraftFieldValue(v any) (any, bool, error) {
	switch x := v.(type) {
	case nil:
		return nil, false, nil
	case string:
		if len(x) > colabOneMaxWorkflowDraftFieldStringBytes {
			return nil, false, errors.New("草稿文本字段超过 200000 字节上限")
		}
		return x, true, nil
	case bool:
		return x, true, nil
	case int:
		return int64(x), true, nil
	case int8:
		return int64(x), true, nil
	case int16:
		return int64(x), true, nil
	case int32:
		return int64(x), true, nil
	case int64:
		return x, true, nil
	case uint:
		if uint64(x) <= math.MaxInt64 {
			return int64(x), true, nil
		}
		return uint64(x), true, nil
	case uint8:
		return int64(x), true, nil
	case uint16:
		return int64(x), true, nil
	case uint32:
		return int64(x), true, nil
	case uint64:
		if x <= math.MaxInt64 {
			return int64(x), true, nil
		}
		return x, true, nil
	case float32:
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, false, errors.New("草稿字段数值不合法")
		}
		return f, true, nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, false, errors.New("草稿字段数值不合法")
		}
		return x, true, nil
	case json.Number:
		s := string(x)
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n, true, nil
		}
		if u, err := strconv.ParseUint(s, 10, 64); err == nil {
			return u, true, nil
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f, true, nil
		}
		return nil, false, errors.New("草稿字段数值不合法")
	default:
		return nil, false, errors.New("草稿字段仅支持文本、数值或布尔值")
	}
}

func validateWorkflowDraftFields(raw any) (map[string]any, error) {
	if raw == nil {
		return make(map[string]any), nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("草稿字段必须为对象")
	}
	if len(m) > colabOneMaxWorkflowDraftFields {
		return nil, errors.New("草稿字段数量不能超过 128 个")
	}
	cleaned := make(map[string]any, len(m))
	for k, v := range m {
		if k == "" || utf8.RuneCountInString(k) > 160 || !reWorkflowDraftFieldKey.MatchString(k) {
			return nil, errors.New("草稿字段键不合法")
		}
		val, keep, err := validateWorkflowDraftFieldValue(v)
		if err != nil {
			return nil, err
		}
		if keep {
			cleaned[k] = val
		}
	}
	return cleaned, nil
}

func workflowDraftMediaSpecForKind(kind string) (map[string]string, int64, string, error) {
	switch kind {
	case "image":
		return colabOneDraftImageMIME, colabOneMaxWorkflowDraftImageBytes, "图片", nil
	case "audio":
		return colabOneDraftAudioMIME, colabOneMaxWorkflowDraftAudioBytes, "音频", nil
	default:
		return nil, 0, "", errors.New("草稿素材类型仅支持 image 或 audio")
	}
}

func sanitizeWorkflowDraftFilename(rawFilename, kind string) (cleanName, ext, mimeType string, maxBytes int64, err error) {
	allowed, limit, kindLabel, err := workflowDraftMediaSpecForKind(kind)
	if err != nil {
		return "", "", "", 0, err
	}

	base := rawFilename
	if idx := strings.LastIndexAny(base, `/\`); idx >= 0 {
		base = base[idx+1:]
	}
	base = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || unicode.IsControl(r) {
			return -1
		}
		return r
	}, base)
	base = strings.TrimSpace(base)
	if base == "" || base == "." || base == ".." || strings.ContainsAny(base, `/\`+"\x00") {
		return "", "", "", 0, errors.New("输入文件名不合法")
	}

	lastDot := strings.LastIndexByte(base, '.')
	if lastDot <= 0 || lastDot >= len(base)-1 {
		return "", "", "", 0, fmt.Errorf("不支持的%s输入文件格式", kindLabel)
	}
	stem := strings.TrimSpace(base[:lastDot])
	origExt := base[lastDot:]
	lowerExt := strings.ToLower(base[lastDot+1:])
	if stem == "" || stem == "." || stem == ".." {
		return "", "", "", 0, errors.New("输入文件名不合法")
	}
	mimeType, ok := allowed[lowerExt]
	if !ok {
		return "", "", "", 0, fmt.Errorf("不支持的%s输入文件格式：%s", kindLabel, lowerExt)
	}

	clean := stem + origExt
	if utf8.RuneCountInString(clean) > colabOneMaxWorkflowDraftFilenameRunes {
		extRunes := utf8.RuneCountInString(origExt)
		maxStemRunes := colabOneMaxWorkflowDraftFilenameRunes - extRunes
		if maxStemRunes <= 0 {
			return "", "", "", 0, errors.New("输入文件名不合法")
		}
		stem = strings.TrimSpace(truncateRunes(stem, maxStemRunes))
		if stem == "" || stem == "." || stem == ".." {
			return "", "", "", 0, errors.New("输入文件名不合法")
		}
		clean = stem + origExt
	}
	if strings.ContainsAny(clean, `/\`+"\x00") || clean == "." || clean == ".." {
		return "", "", "", 0, errors.New("输入文件名不合法")
	}
	return clean, lowerExt, mimeType, limit, nil
}

func decodeWorkflowDraftBase64(dataB64 string, maxBytes int64) ([]byte, error) {
	limitMB := maxBytes / (1024 * 1024)
	payload := strings.TrimSpace(dataB64)
	if payload == "" {
		return nil, errors.New("输入文件内容为空")
	}
	if strings.HasPrefix(payload, "data:") {
		_, after, ok := strings.Cut(payload, ",")
		if !ok {
			return nil, errors.New("输入文件编码格式无效")
		}
		payload = after
	}

	maxEncodedLen := maxBytes + maxBytes/2 + 4096
	if int64(len(payload)) > maxEncodedLen {
		return nil, fmt.Errorf("输入文件超过大小限制（最大 %d MB）", limitMB)
	}

	clean := payload
	if strings.IndexFunc(payload, unicode.IsSpace) >= 0 {
		clean = strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, payload)
	}
	if clean == "" {
		return nil, errors.New("输入文件内容为空")
	}
	if len(clean)%4 != 0 {
		return nil, errors.New("输入文件 Base64 编码无效")
	}

	estLen := int64(base64.StdEncoding.DecodedLen(len(clean)))
	if estLen > maxBytes+2 {
		return nil, fmt.Errorf("输入文件超过大小限制（最大 %d MB）", limitMB)
	}
	allocLen := estLen
	if allocLen > maxBytes {
		allocLen = maxBytes
	}
	out := make([]byte, 0, allocLen)

	const b64ChunkSize = 4096 // multiple of 4
	var decodeBuf [(b64ChunkSize / 4) * 3]byte
	for offset := 0; offset < len(clean); offset += b64ChunkSize {
		end := offset + b64ChunkSize
		isLast := end >= len(clean)
		if isLast {
			end = len(clean)
		}
		chunk := clean[offset:end]
		if !isLast && strings.IndexByte(chunk, '=') >= 0 {
			return nil, errors.New("输入文件 Base64 编码无效")
		}
		n, err := base64.StdEncoding.Decode(decodeBuf[:], []byte(chunk))
		if err != nil {
			return nil, errors.New("输入文件 Base64 编码无效")
		}
		if int64(len(out))+int64(n) > maxBytes {
			return nil, fmt.Errorf("输入文件超过大小限制（最大 %d MB）", limitMB)
		}
		out = append(out, decodeBuf[:n]...)
	}
	if len(out) == 0 {
		return nil, errors.New("输入文件内容为空")
	}
	return out, nil
}

func ensureSafeDraftDir(dir string, create bool) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("草稿目录未配置")
	}
	info, err := os.Lstat(dir)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("草稿目录不安全")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !create {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err = os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("草稿目录不安全")
	}
	return nil
}

func (c *ColabOneController) ensureSafeDraftStorageLocked(createDraftsDir, createMediaDir bool) error {
	draftsDir := c.workflowDraftsDir()
	if err := ensureSafeDraftDir(draftsDir, createDraftsDir); err != nil {
		return err
	}
	draftsPath := c.workflowDraftsFilePath()
	if info, err := os.Lstat(draftsPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("草稿元数据路径不安全")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if createMediaDir {
		if err := ensureSafeDraftDir(c.workflowDraftsMediaDir(), true); err != nil {
			return err
		}
	}
	return nil
}

func (c *ColabOneController) areDraftDirsSafeLocked() bool {
	draftsDir := c.workflowDraftsDir()
	if draftsDir == "" {
		return false
	}
	info, err := os.Lstat(draftsDir)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false
	}
	mediaDir := c.workflowDraftsMediaDir()
	mInfo, mErr := os.Lstat(mediaDir)
	if mErr != nil || mInfo.Mode()&os.ModeSymlink != 0 || !mInfo.IsDir() {
		return false
	}
	return true
}

func (c *ColabOneController) resolveDraftMediaPath(meta ColabOneWorkflowDraftMedia) (mediaPath, fileName string, maxBytes int64, err error) {
	if !reHex32ID.MatchString(meta.ID) {
		return "", "", 0, errors.New("草稿素材 ID 不合法")
	}
	_, ext, _, limit, err := sanitizeWorkflowDraftFilename(meta.Filename, meta.Kind)
	if err != nil {
		return "", "", 0, err
	}
	if meta.Size <= 0 || meta.Size > limit {
		return "", "", 0, errors.New("草稿素材大小信息不合法")
	}
	if !reWorkflowDraftSHA256.MatchString(meta.SHA256) {
		return "", "", 0, errors.New("草稿素材校验信息不合法")
	}
	mediaDir := c.workflowDraftsMediaDir()
	if mediaDir == "" {
		return "", "", 0, errors.New("草稿目录未配置")
	}
	fileName = meta.ID + "." + ext
	mediaPath = filepath.Join(mediaDir, fileName)
	if filepath.Dir(mediaPath) != mediaDir || filepath.Base(mediaPath) != fileName {
		return "", "", 0, errors.New("草稿素材路径不安全")
	}
	return mediaPath, fileName, limit, nil
}

func (c *ColabOneController) draftMediaFileValidLocked(meta ColabOneWorkflowDraftMedia) bool {
	if !c.areDraftDirsSafeLocked() {
		return false
	}
	mediaPath, _, _, err := c.resolveDraftMediaPath(meta)
	if err != nil {
		return false
	}
	info, err := os.Lstat(mediaPath)
	if err != nil {
		return false
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false
	}
	if info.Size() != meta.Size {
		return false
	}
	return true
}

func colabOneWriteFileAtomic(dir, targetPath string, data []byte, perm os.FileMode) error {
	if strings.TrimSpace(dir) == "" || strings.TrimSpace(targetPath) == "" {
		return errors.New("controller: target path is empty")
	}
	if info, err := os.Lstat(targetPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
			return errors.New("草稿素材路径不安全")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
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

	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
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
	if err := colabOneRenameReplace(tmpName, targetPath); err != nil {
		return err
	}
	renamed = true
	return nil
}

func (c *ColabOneController) writeWorkflowDraftsFileLocked(snapshot map[string]*ColabOneWorkflowDraft) error {
	if err := c.ensureSafeDraftStorageLocked(true, false); err != nil {
		return err
	}
	if snapshot == nil {
		snapshot = make(map[string]*ColabOneWorkflowDraft)
	}
	disk := colabOneWorkflowDraftsDiskFile{
		Version: colabOneWorkflowDraftsSchemaVersion,
		Drafts:  snapshot,
	}
	return AtomicJSON(c.workflowDraftsFilePath(), disk)
}

func (c *ColabOneController) referencedDraftMediaFilesLocked() map[string]bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	refs := make(map[string]bool)
	for _, draft := range c.workflowDrafts {
		if draft == nil {
			continue
		}
		for _, meta := range draft.Media {
			_, fileName, _, err := c.resolveDraftMediaPath(meta)
			if err == nil && fileName != "" {
				refs[fileName] = true
			}
		}
	}
	return refs
}

func (c *ColabOneController) removeDraftMediaIfUnreferencedLocked(meta ColabOneWorkflowDraftMedia) {
	if !c.areDraftDirsSafeLocked() {
		return
	}
	mediaPath, fileName, _, err := c.resolveDraftMediaPath(meta)
	if err != nil || fileName == "" {
		return
	}
	if c.referencedDraftMediaFilesLocked()[fileName] {
		return
	}
	info, err := os.Lstat(mediaPath)
	if err != nil {
		return
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return
	}
	_ = os.Remove(mediaPath)
}

func (c *ColabOneController) pruneUnreferencedDraftMediaLocked() {
	if !c.areDraftDirsSafeLocked() {
		return
	}
	mediaDir := c.workflowDraftsMediaDir()
	refs := c.referencedDraftMediaFilesLocked()
	entries, err := os.ReadDir(mediaDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".save-") {
			continue
		}
		if refs[name] {
			continue
		}
		fullPath := filepath.Join(mediaDir, name)
		info, statErr := os.Lstat(fullPath)
		if statErr != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		_ = os.Remove(fullPath)
	}
}

func (c *ColabOneController) loadWorkflowDrafts() {
	c.persistMu.Lock()
	defer c.persistMu.Unlock()

	draftsDir := c.workflowDraftsDir()
	if draftsDir == "" {
		return
	}
	dirInfo, err := os.Lstat(draftsDir)
	if err != nil {
		return
	}
	if dirInfo.Mode()&os.ModeSymlink != 0 || !dirInfo.IsDir() {
		return
	}

	draftsPath := c.workflowDraftsFilePath()
	draftsFileExists := false
	if fileInfo, statErr := os.Lstat(draftsPath); statErr == nil {
		if fileInfo.Mode()&os.ModeSymlink != 0 || !fileInfo.Mode().IsRegular() {
			return
		}
		draftsFileExists = true
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return
	}

	loaded := make(map[string]*ColabOneWorkflowDraft)
	reconciled := false

	if draftsFileExists {
		rawBytes, readErr := os.ReadFile(draftsPath)
		if readErr != nil {
			return
		}
		var rawObj map[string]any
		if decodeJSONWithNumbers(rawBytes, &rawObj) != nil || rawObj == nil {
			if c.activity != nil {
				c.log("读取本地 ComfyUI 草稿配置失败，已保留原文件未覆盖", "ComfyUI", "warning")
			}
			return
		}

		var rawDrafts map[string]any
		if rawVal, hasDrafts := rawObj["drafts"]; hasDrafts {
			m := asMap(rawVal)
			if m == nil {
				if c.activity != nil {
					c.log("读取本地 ComfyUI 草稿配置失败，已保留原文件未覆盖", "ComfyUI", "warning")
				}
				return
			}
			rawDrafts = m
		} else if _, hasVer := rawObj["version"]; !hasVer {
			rawDrafts = rawObj
		}

		for wfID, rawItem := range rawDrafts {
			if !reModelID.MatchString(wfID) {
				reconciled = true
				continue
			}
			itemMap := asMap(rawItem)
			if itemMap == nil {
				reconciled = true
				continue
			}
			fields := make(map[string]any)
			if rawFields := asMap(itemMap["fields"]); rawFields != nil {
				for fk, fv := range rawFields {
					if len(fields) >= colabOneMaxWorkflowDraftFields {
						reconciled = true
						break
					}
					if fk == "" || utf8.RuneCountInString(fk) > 160 || !reWorkflowDraftFieldKey.MatchString(fk) {
						reconciled = true
						continue
					}
					norm, keep, verr := validateWorkflowDraftFieldValue(fv)
					if verr != nil {
						reconciled = true
						continue
					}
					if keep {
						fields[fk] = norm
					}
				}
			}

			media := make(map[string]ColabOneWorkflowDraftMedia)
			if rawMedia := asMap(itemMap["media"]); rawMedia != nil {
				for fk, mv := range rawMedia {
					if fk == "" || utf8.RuneCountInString(fk) > 160 || !reWorkflowDraftFieldKey.MatchString(fk) {
						reconciled = true
						continue
					}
					mm := asMap(mv)
					if mm == nil {
						delete(fields, fk)
						reconciled = true
						continue
					}
					id := asString(mm["id"])
					filename := asString(mm["filename"])
					kind := asString(mm["kind"])
					mimeStr := asString(mm["mime"])
					size, sizeOK := asInt64(mm["size"])
					sha := strings.ToLower(strings.TrimSpace(asString(mm["sha256"])))
					updatedAt, _ := asFloat(mm["updated_at"])
					if updatedAt < 0 {
						updatedAt = 0
					}

					if !reHex32ID.MatchString(id) || !sizeOK || !reWorkflowDraftSHA256.MatchString(sha) {
						delete(fields, fk)
						reconciled = true
						continue
					}
					cleanFilename, _, expectedMIME, limit, serr := sanitizeWorkflowDraftFilename(filename, kind)
					if serr != nil || size <= 0 || size > limit {
						delete(fields, fk)
						reconciled = true
						continue
					}
					if mimeStr == "" {
						mimeStr = expectedMIME
					} else if mimeStr != expectedMIME {
						delete(fields, fk)
						reconciled = true
						continue
					}

					meta := ColabOneWorkflowDraftMedia{
						ID:        id,
						Filename:  cleanFilename,
						Kind:      kind,
						MIME:      mimeStr,
						Size:      size,
						SHA256:    sha,
						UpdatedAt: updatedAt,
					}
					if !c.draftMediaFileValidLocked(meta) {
						delete(fields, fk)
						reconciled = true
						continue
					}
					media[fk] = meta
				}
			}

			updatedAt, _ := asFloat(itemMap["updated_at"])
			if updatedAt < 0 {
				updatedAt = 0
			}
			loaded[wfID] = &ColabOneWorkflowDraft{
				WorkflowID: wfID,
				Fields:     fields,
				Media:      media,
				UpdatedAt:  updatedAt,
			}
		}
	}

	c.mu.Lock()
	c.workflowDrafts = loaded
	var diskSnapshot map[string]*ColabOneWorkflowDraft
	if draftsFileExists && reconciled {
		diskSnapshot = cloneWorkflowDraftMap(c.workflowDrafts)
	}
	c.mu.Unlock()

	if diskSnapshot != nil {
		_ = c.writeWorkflowDraftsFileLocked(diskSnapshot)
	}
	c.pruneUnreferencedDraftMediaLocked()
}

func (c *ColabOneController) getWorkflowDraft(payload map[string]any) (any, error) {
	wfID := ""
	if rawWFID, exists := payload["workflow_id"]; exists && rawWFID != nil {
		s, ok := rawWFID.(string)
		if !ok {
			return nil, errors.New("工作流 ID 不合法")
		}
		if s != "" {
			if !reModelID.MatchString(s) {
				return nil, errors.New("工作流 ID 不合法")
			}
			wfID = s
		}
	}

	c.persistMu.Lock()
	defer c.persistMu.Unlock()

	dirsSafe := c.areDraftDirsSafeLocked()
	canReconcile := c.ensureSafeDraftStorageLocked(false, false) == nil

	if wfID != "" {
		c.mu.Lock()
		existing := c.workflowDrafts[wfID]
		if existing == nil {
			c.mu.Unlock()
			return map[string]any{"draft": nil}, nil
		}
		candidate := cloneWorkflowDraft(existing)
		c.mu.Unlock()

		stale := make(map[string]string)
		for fk, meta := range candidate.Media {
			if !dirsSafe || !c.draftMediaFileValidLocked(meta) {
				stale[fk] = meta.ID
				delete(candidate.Media, fk)
				delete(candidate.Fields, fk)
			}
		}
		if len(stale) > 0 {
			c.mu.Lock()
			changed := false
			if cur := c.workflowDrafts[wfID]; cur != nil {
				for fk, staleID := range stale {
					if curMeta, ok := cur.Media[fk]; ok && curMeta.ID == staleID {
						delete(cur.Media, fk)
						delete(cur.Fields, fk)
						changed = true
					}
				}
			}
			var diskSnapshot map[string]*ColabOneWorkflowDraft
			if changed && canReconcile {
				diskSnapshot = cloneWorkflowDraftMap(c.workflowDrafts)
			}
			c.mu.Unlock()
			if diskSnapshot != nil {
				_ = c.writeWorkflowDraftsFileLocked(diskSnapshot)
			}
		}
		return map[string]any{"draft": candidate}, nil
	}

	c.mu.Lock()
	allCopies := cloneWorkflowDraftMap(c.workflowDrafts)
	c.mu.Unlock()

	staleByWF := make(map[string]map[string]string)
	for id, draft := range allCopies {
		for fk, meta := range draft.Media {
			if !dirsSafe || !c.draftMediaFileValidLocked(meta) {
				if staleByWF[id] == nil {
					staleByWF[id] = make(map[string]string)
				}
				staleByWF[id][fk] = meta.ID
				delete(draft.Media, fk)
				delete(draft.Fields, fk)
			}
		}
	}

	if len(staleByWF) > 0 {
		c.mu.Lock()
		changed := false
		for id, stale := range staleByWF {
			cur := c.workflowDrafts[id]
			if cur == nil {
				continue
			}
			for fk, staleID := range stale {
				if curMeta, ok := cur.Media[fk]; ok && curMeta.ID == staleID {
					delete(cur.Media, fk)
					delete(cur.Fields, fk)
					changed = true
				}
			}
		}
		var diskSnapshot map[string]*ColabOneWorkflowDraft
		if changed && canReconcile {
			diskSnapshot = cloneWorkflowDraftMap(c.workflowDrafts)
		}
		c.mu.Unlock()
		if diskSnapshot != nil {
			_ = c.writeWorkflowDraftsFileLocked(diskSnapshot)
		}
	}

	return map[string]any{"drafts": allCopies}, nil
}

func (c *ColabOneController) saveWorkflowDraft(payload map[string]any) (any, error) {
	wfID, err := validateWorkflowDraftID(payload["workflow_id"])
	if err != nil {
		return nil, err
	}
	cleanedFields, err := validateWorkflowDraftFields(payload["fields"])
	if err != nil {
		return nil, err
	}

	now := nowUnixFloat()

	c.persistMu.Lock()
	defer c.persistMu.Unlock()

	if err := c.ensureSafeDraftStorageLocked(true, false); err != nil {
		return nil, err
	}

	c.mu.Lock()
	prevCopy, hadPrev := cloneWorkflowDraft(c.workflowDrafts[wfID]), c.workflowDrafts[wfID] != nil
	existingMedia := make(map[string]ColabOneWorkflowDraftMedia)
	if hadPrev {
		for k, v := range prevCopy.Media {
			existingMedia[k] = v
		}
	}
	nextDraft := &ColabOneWorkflowDraft{
		WorkflowID: wfID,
		Fields:     cleanedFields,
		Media:      existingMedia,
		UpdatedAt:  now,
	}
	c.workflowDrafts[wfID] = nextDraft
	diskSnapshot := cloneWorkflowDraftMap(c.workflowDrafts)
	returnCopy := cloneWorkflowDraft(nextDraft)
	c.mu.Unlock()

	if err := c.writeWorkflowDraftsFileLocked(diskSnapshot); err != nil {
		c.mu.Lock()
		if hadPrev {
			c.workflowDrafts[wfID] = prevCopy
		} else {
			delete(c.workflowDrafts, wfID)
		}
		c.mu.Unlock()
		return nil, err
	}

	return map[string]any{"ok": true, "draft": returnCopy}, nil
}

func (c *ColabOneController) saveWorkflowDraftMedia(payload map[string]any) (any, error) {
	wfID, err := validateWorkflowDraftID(payload["workflow_id"])
	if err != nil {
		return nil, err
	}
	fieldKey, err := validateWorkflowDraftFieldKey(payload["field_key"])
	if err != nil {
		return nil, err
	}
	kind, ok := payload["kind"].(string)
	if !ok {
		return nil, errors.New("草稿素材类型仅支持 image 或 audio")
	}
	rawFilename, ok := payload["filename"].(string)
	if !ok {
		return nil, errors.New("输入文件名不合法")
	}
	cleanFilename, ext, mimeType, limit, err := sanitizeWorkflowDraftFilename(rawFilename, kind)
	if err != nil {
		return nil, err
	}
	rawData, ok := payload["data"].(string)
	if !ok {
		return nil, errors.New("输入文件内容为空")
	}
	if uploadValidator, ok := c.comfy.(ComfyInputUploadValidator); ok {
		if err := uploadValidator.ValidateInputUpload(cleanFilename, rawData); err != nil {
			return nil, err
		}
	}
	decoded, err := decodeWorkflowDraftBase64(rawData, limit)
	if err != nil {
		return nil, err
	}

	sum := sha256.Sum256(decoded)
	shaHex := hex.EncodeToString(sum[:])
	mediaID := generateTokenHex(16)
	now := nowUnixFloat()

	meta := ColabOneWorkflowDraftMedia{
		ID:        mediaID,
		Filename:  cleanFilename,
		Kind:      kind,
		MIME:      mimeType,
		Size:      int64(len(decoded)),
		SHA256:    shaHex,
		UpdatedAt: now,
	}

	c.persistMu.Lock()
	defer c.persistMu.Unlock()

	if err := c.ensureSafeDraftStorageLocked(true, true); err != nil {
		return nil, err
	}

	mediaDir := c.workflowDraftsMediaDir()
	mediaPath := filepath.Join(mediaDir, mediaID+"."+ext)
	if filepath.Dir(mediaPath) != mediaDir || filepath.Base(mediaPath) != mediaID+"."+ext {
		return nil, errors.New("草稿素材路径不安全")
	}
	if err := colabOneWriteFileAtomic(mediaDir, mediaPath, decoded, 0o600); err != nil {
		return nil, err
	}

	c.mu.Lock()
	prevCopy, hadPrev := cloneWorkflowDraft(c.workflowDrafts[wfID]), c.workflowDrafts[wfID] != nil
	var prevMedia ColabOneWorkflowDraftMedia
	hadPrevMedia := false

	var nextDraft *ColabOneWorkflowDraft
	if hadPrev {
		nextDraft = cloneWorkflowDraft(prevCopy)
		if old, exists := nextDraft.Media[fieldKey]; exists {
			prevMedia = old
			hadPrevMedia = true
		}
	} else {
		nextDraft = &ColabOneWorkflowDraft{
			WorkflowID: wfID,
			Fields:     make(map[string]any),
			Media:      make(map[string]ColabOneWorkflowDraftMedia),
		}
	}
	if _, exists := nextDraft.Fields[fieldKey]; !exists && len(nextDraft.Fields) >= colabOneMaxWorkflowDraftFields {
		c.mu.Unlock()
		_ = os.Remove(mediaPath)
		return nil, errors.New("草稿字段数量不能超过 128 个")
	}
	nextDraft.Media[fieldKey] = meta
	nextDraft.Fields[fieldKey] = cleanFilename
	nextDraft.UpdatedAt = now
	c.workflowDrafts[wfID] = nextDraft

	diskSnapshot := cloneWorkflowDraftMap(c.workflowDrafts)
	returnCopy := cloneWorkflowDraft(nextDraft)
	c.mu.Unlock()

	if err := c.writeWorkflowDraftsFileLocked(diskSnapshot); err != nil {
		c.mu.Lock()
		if hadPrev {
			c.workflowDrafts[wfID] = prevCopy
		} else {
			delete(c.workflowDrafts, wfID)
		}
		c.mu.Unlock()
		_ = os.Remove(mediaPath)
		return nil, err
	}

	if hadPrevMedia && prevMedia.ID != meta.ID {
		c.removeDraftMediaIfUnreferencedLocked(prevMedia)
	}

	return map[string]any{
		"ok":    true,
		"media": meta,
		"draft": returnCopy,
	}, nil
}

func (c *ColabOneController) readWorkflowDraftMedia(payload map[string]any) (any, error) {
	wfID, err := validateWorkflowDraftID(payload["workflow_id"])
	if err != nil {
		return nil, err
	}
	fieldKey, err := validateWorkflowDraftFieldKey(payload["field_key"])
	if err != nil {
		return nil, err
	}

	c.persistMu.Lock()
	c.mu.Lock()
	draft := c.workflowDrafts[wfID]
	var meta ColabOneWorkflowDraftMedia
	found := false
	if draft != nil {
		meta, found = draft.Media[fieldKey]
	}
	c.mu.Unlock()

	if !found {
		c.persistMu.Unlock()
		return nil, fmt.Errorf("%w: 草稿素材不存在", ErrNotFound)
	}

	if err := ensureSafeDraftDir(c.workflowDraftsDir(), false); err != nil {
		c.persistMu.Unlock()
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: 草稿素材文件已丢失", ErrNotFound)
		}
		return nil, err
	}
	if err := ensureSafeDraftDir(c.workflowDraftsMediaDir(), false); err != nil {
		c.persistMu.Unlock()
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: 草稿素材文件已丢失", ErrNotFound)
		}
		return nil, err
	}

	mediaPath, _, limit, err := c.resolveDraftMediaPath(meta)
	if err != nil {
		c.persistMu.Unlock()
		return nil, err
	}

	info, err := os.Lstat(mediaPath)
	if err != nil {
		c.persistMu.Unlock()
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: 草稿素材文件已丢失", ErrNotFound)
		}
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		c.persistMu.Unlock()
		return nil, errors.New("草稿素材路径不安全")
	}
	if info.Size() > limit {
		c.persistMu.Unlock()
		return nil, errors.New("草稿素材大小超出允许范围，文件可能已损坏")
	}

	f, err := os.Open(mediaPath)
	if err != nil {
		c.persistMu.Unlock()
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: 草稿素材文件已丢失", ErrNotFound)
		}
		return nil, err
	}
	fInfo, fStatErr := f.Stat()
	if fStatErr != nil || !fInfo.Mode().IsRegular() || !os.SameFile(info, fInfo) {
		_ = f.Close()
		c.persistMu.Unlock()
		return nil, errors.New("草稿素材路径不安全")
	}
	rawBytes, readErr := io.ReadAll(io.LimitReader(f, limit+1))
	_ = f.Close()
	c.persistMu.Unlock()

	if readErr != nil {
		return nil, readErr
	}
	if int64(len(rawBytes)) > limit || int64(len(rawBytes)) != meta.Size {
		return nil, errors.New("草稿素材大小校验失败，文件可能已损坏")
	}
	sum := sha256.Sum256(rawBytes)
	if hex.EncodeToString(sum[:]) != meta.SHA256 {
		return nil, errors.New("草稿素材哈希校验失败，文件可能已损坏")
	}

	return map[string]any{
		"id":       meta.ID,
		"filename": meta.Filename,
		"kind":     meta.Kind,
		"mime":     meta.MIME,
		"size":     meta.Size,
		"sha256":   meta.SHA256,
		"data":     base64.StdEncoding.EncodeToString(rawBytes),
	}, nil
}

func (c *ColabOneController) deleteWorkflowDraftMedia(payload map[string]any) (any, error) {
	wfID, err := validateWorkflowDraftID(payload["workflow_id"])
	if err != nil {
		return nil, err
	}
	fieldKey, err := validateWorkflowDraftFieldKey(payload["field_key"])
	if err != nil {
		return nil, err
	}

	c.persistMu.Lock()
	defer c.persistMu.Unlock()

	c.mu.Lock()
	existing := c.workflowDrafts[wfID]
	if existing == nil {
		c.mu.Unlock()
		return map[string]any{"ok": true}, nil
	}
	prevCopy := cloneWorkflowDraft(existing)
	oldMedia, hadMedia := existing.Media[fieldKey]
	_, hadField := existing.Fields[fieldKey]
	if !hadMedia && !hadField {
		c.mu.Unlock()
		return map[string]any{"ok": true}, nil
	}
	delete(existing.Media, fieldKey)
	delete(existing.Fields, fieldKey)
	existing.UpdatedAt = nowUnixFloat()
	diskSnapshot := cloneWorkflowDraftMap(c.workflowDrafts)
	c.mu.Unlock()

	if err := c.writeWorkflowDraftsFileLocked(diskSnapshot); err != nil {
		c.mu.Lock()
		c.workflowDrafts[wfID] = prevCopy
		c.mu.Unlock()
		return nil, err
	}

	if hadMedia {
		c.removeDraftMediaIfUnreferencedLocked(oldMedia)
	}
	return map[string]any{"ok": true}, nil
}

func (c *ColabOneController) clearWorkflowDraft(wfID string) error {
	if !reModelID.MatchString(wfID) {
		return errors.New("工作流 ID 不合法")
	}

	c.persistMu.Lock()
	defer c.persistMu.Unlock()

	c.mu.Lock()
	existing, existed := c.workflowDrafts[wfID]
	if !existed {
		c.mu.Unlock()
		return nil
	}
	prevCopy := cloneWorkflowDraft(existing)
	delete(c.workflowDrafts, wfID)
	diskSnapshot := cloneWorkflowDraftMap(c.workflowDrafts)
	c.mu.Unlock()

	if err := c.writeWorkflowDraftsFileLocked(diskSnapshot); err != nil {
		c.mu.Lock()
		c.workflowDrafts[wfID] = prevCopy
		c.mu.Unlock()
		return err
	}

	for _, meta := range prevCopy.Media {
		c.removeDraftMediaIfUnreferencedLocked(meta)
	}
	c.pruneUnreferencedDraftMediaLocked()
	return nil
}
