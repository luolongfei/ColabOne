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
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	reDriveSHA256Hex = regexp.MustCompile(`^[a-f0-9]{64}$`)
	reDriveFileID    = regexp.MustCompile(`^[A-Za-z0-9._-]{4,256}$`)
)

func sanitizeDriveSHA256(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if reDriveSHA256Hex.MatchString(s) {
		return s
	}
	return ""
}

func sanitizeDriveFileID(raw string) string {
	s := strings.TrimSpace(raw)
	if reDriveFileID.MatchString(s) {
		return s
	}
	return ""
}

func sanitizeDriveTaskID(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) > 120 {
		return string(runes[:120])
	}
	return s
}

func sanitizeDriveWebViewLink(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > 2048 {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return ""
	}
	if !strings.EqualFold(u.Hostname(), "drive.google.com") {
		return ""
	}
	return s
}

func parseContentRangeTotal(hdr string) int64 {
	s := strings.TrimSpace(hdr)
	slash := strings.LastIndex(s, "/")
	if slash < 0 || slash+1 >= len(s) {
		return 0
	}
	rawTotal := strings.TrimSpace(s[slash+1:])
	if rawTotal == "" || rawTotal == "*" {
		return 0
	}
	n, err := strconv.ParseInt(rawTotal, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func (c *ColabOneController) resetDriveOutputsSyncStateLocked() {
	c.driveOutputsLastSynced = 0
	c.driveOutputsSyncError = ""
}

func (c *ColabOneController) driveOutputsSyncSemaphore() chan struct{} {
	c.mu.Lock()
	if c.driveOutputsSyncSem == nil {
		c.driveOutputsSyncSem = make(chan struct{}, 1)
	}
	sem := c.driveOutputsSyncSem
	c.mu.Unlock()
	return sem
}

func (c *ColabOneController) clearDriveOutputsSyncTrackingLocked(acctKey string) {
	if acctKey == "" || c.driveOutputsSyncAccountKey == acctKey {
		c.driveOutputsSyncing = false
		c.driveOutputsSyncAccountKey = ""
	}
	if acctKey == "" {
		clear(c.driveOutputsSyncDeleted)
		clear(c.driveOutputsSyncDeletedFileIDs)
		clear(c.driveOutputsSyncAdded)
		return
	}
	delete(c.driveOutputsSyncDeleted, acctKey)
	delete(c.driveOutputsSyncDeletedFileIDs, acctKey)
	delete(c.driveOutputsSyncAdded, acctKey)
}

func (c *ColabOneController) noteDriveOutputAddedLocked(acctKey, id string) {
	if !c.driveOutputsSyncing || c.driveOutputsSyncAccountKey != acctKey || acctKey == "" || id == "" {
		return
	}
	if c.driveOutputsSyncAdded == nil {
		c.driveOutputsSyncAdded = make(map[string]map[string]bool)
	}
	if c.driveOutputsSyncAdded[acctKey] == nil {
		c.driveOutputsSyncAdded[acctKey] = make(map[string]bool)
	}
	c.driveOutputsSyncAdded[acctKey][id] = true
	if del := c.driveOutputsSyncDeleted[acctKey]; del != nil {
		delete(del, id)
	}
}

func (c *ColabOneController) noteDriveOutputDeletedLocked(acctKey, id string, oldRec *ColabOneDriveOutputRecord) {
	if !c.driveOutputsSyncing || c.driveOutputsSyncAccountKey != acctKey || acctKey == "" || id == "" {
		return
	}
	if c.driveOutputsSyncDeleted == nil {
		c.driveOutputsSyncDeleted = make(map[string]map[string]bool)
	}
	if c.driveOutputsSyncDeleted[acctKey] == nil {
		c.driveOutputsSyncDeleted[acctKey] = make(map[string]bool)
	}
	c.driveOutputsSyncDeleted[acctKey][id] = true
	if added := c.driveOutputsSyncAdded[acctKey]; added != nil {
		delete(added, id)
	}
	if oldRec != nil {
		for _, fid := range []string{sanitizeDriveFileID(oldRec.DriveFileID), sanitizeDriveFileID(oldRec.DriveWorkspaceFileID)} {
			if fid == "" {
				continue
			}
			if c.driveOutputsSyncDeletedFileIDs == nil {
				c.driveOutputsSyncDeletedFileIDs = make(map[string]map[string]bool)
			}
			if c.driveOutputsSyncDeletedFileIDs[acctKey] == nil {
				c.driveOutputsSyncDeletedFileIDs[acctKey] = make(map[string]bool)
			}
			c.driveOutputsSyncDeletedFileIDs[acctKey][fid] = true
		}
	}
}

func refuseDriveRedirects(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

const (
	colabOneDriveOutputsFileName     = "drive_outputs.json"
	colabOneDriveOutputsSchemaVer    = 1
	colabOneDriveAPIDefaultBaseURL   = "https://www.googleapis.com/drive/v3"
	colabOneDriveFolderName          = "ColabOne_Drive"
	colabOneDriveOutputsSubdir       = "outputs"
	colabOneDriveWorkspaceSubdir     = "workspace"
	colabOneDriveComfyOutputsSubdir  = "comfyui-outputs"
	colabOneDriveAssetChunkBytes     = 512 * 1024
	colabOneDriveMaxMetaBytes        = 64 * 1024
	colabOneDriveMaxListBodyBytes    = 4 << 20
	colabOneDriveOutputsMaxPerAcct   = 400
	colabOneWorkflowAutoRelUpgrHint  = "当前远端服务版本暂不支持工作流完成后自动同步释放，请先点击顶部「立即热更新」升级远端服务后再试"
)

var allowedDriveOutputKindMIME = map[string][2]string{
	"png":  {"image", "image/png"},
	"jpg":  {"image", "image/jpeg"},
	"jpeg": {"image", "image/jpeg"},
	"webp": {"image", "image/webp"},
	"gif":  {"image", "image/gif"},
	"mp4":  {"video", "video/mp4"},
	"webm": {"video", "video/webm"},
	"wav":  {"audio", "audio/wav"},
	"mp3":  {"audio", "audio/mpeg"},
	"flac": {"audio", "audio/flac"},
}

// ColabOneDriveOutputRecord stores metadata for a workflow output persisted in Google Drive,
// allowing the Outputs Drawer to list, inspect, preview, and download outputs even after
// the remote GPU instance has been released.
type ColabOneDriveOutputRecord struct {
	ID                   string  `json:"id"`
	WorkflowID           string  `json:"workflow_id"`
	WorkflowName         string  `json:"workflow_name"`
	Kind                 string  `json:"kind"`
	Filename             string  `json:"filename"`
	Extension            string  `json:"extension"`
	Mime                 string  `json:"mime"`
	Size                 int64   `json:"size"`
	SHA256               string  `json:"sha256,omitempty"`
	CreatedAt            float64 `json:"created_at"`
	Elapsed              float64 `json:"elapsed"`
	CUConsumed           float64 `json:"cu_consumed,omitempty"`
	CURate               float64 `json:"cu_rate,omitempty"`
	GPUName              string  `json:"gpu_name,omitempty"`
	CUEstimated          bool    `json:"cu_estimated,omitempty"`
	PromptID             string  `json:"prompt_id,omitempty"`
	TaskID               string  `json:"task_id,omitempty"`
	DriveSynced          bool    `json:"drive_synced"`
	DriveExported        bool    `json:"drive_exported,omitempty"`
	DrivePath            string  `json:"drive_path,omitempty"`
	DriveFolder          string  `json:"drive_folder,omitempty"`
	DriveFileID          string  `json:"drive_file_id,omitempty"`
	DriveWorkspaceFileID string  `json:"drive_workspace_file_id,omitempty"`
	DriveWorkspaceMetaID string  `json:"drive_workspace_meta_id,omitempty"`
	DriveWebViewLink     string  `json:"drive_web_view_link,omitempty"`
	DriveMode            string  `json:"drive_mode,omitempty"`
	SyncedAt             float64 `json:"synced_at,omitempty"`
}

func (r *ColabOneDriveOutputRecord) clone() *ColabOneDriveOutputRecord {
	if r == nil {
		return nil
	}
	cp := *r
	return &cp
}

func (r *ColabOneDriveOutputRecord) preferredDriveFileID() string {
	if r == nil {
		return ""
	}
	if id := sanitizeDriveFileID(r.DriveFileID); id != "" {
		return id
	}
	return sanitizeDriveFileID(r.DriveWorkspaceFileID)
}

func (r *ColabOneDriveOutputRecord) resolvedWebViewLink() string {
	if r == nil {
		return ""
	}
	if link := sanitizeDriveWebViewLink(r.DriveWebViewLink); link != "" {
		return link
	}
	if id := r.preferredDriveFileID(); id != "" {
		return "https://drive.google.com/file/d/" + url.PathEscape(id) + "/view"
	}
	return ""
}

func (r *ColabOneDriveOutputRecord) toOutputMap(storage string, canDirectRead bool) map[string]any {
	if r == nil {
		return nil
	}
	fileID := r.preferredDriveFileID()
	previewable := storage == "runtime" || (canDirectRead && (fileID != "" || (r.DriveSynced && r.DriveMode != "colab_drivefs")))
	out := map[string]any{
		"id":                r.ID,
		"workflow_id":       r.WorkflowID,
		"workflow_name":     r.WorkflowName,
		"kind":              r.Kind,
		"filename":          r.Filename,
		"extension":         r.Extension,
		"mime":              r.Mime,
		"size":              r.Size,
		"created_at":        r.CreatedAt,
		"elapsed":           r.Elapsed,
		"storage":           storage,
		"drive_synced":      r.DriveSynced,
		"drive_exported":    r.DriveExported,
		"drive_previewable": previewable,
	}
	if r.CUConsumed > 0 {
		out["cu_consumed"] = colabOneRoundCU(r.CUConsumed)
		if r.CURate > 0 {
			out["cu_rate"] = colabOneRoundCU(r.CURate)
		}
		if r.GPUName != "" {
			out["gpu_name"] = r.GPUName
		}
		if r.CUEstimated {
			out["cu_estimated"] = true
		}
	}
	if r.SHA256 != "" {
		out["sha256"] = r.SHA256
	}
	if r.PromptID != "" {
		out["prompt_id"] = r.PromptID
	}
	if r.TaskID != "" {
		out["task_id"] = r.TaskID
	}
	if r.DrivePath != "" {
		out["drive_path"] = r.DrivePath
	}
	if r.DriveFolder != "" {
		out["drive_folder"] = r.DriveFolder
	}
	if fileID != "" {
		out["drive_file_id"] = fileID
	}
	if link := r.resolvedWebViewLink(); link != "" {
		out["drive_web_view_link"] = link
	}
	if r.DriveMode != "" {
		out["drive_mode"] = r.DriveMode
	}
	return out
}

type colabOneDriveOutputsDisk struct {
	Version  int                                             `json:"version"`
	Accounts map[string]map[string]*ColabOneDriveOutputRecord `json:"accounts"`
}

type authAccessTokenProvider interface {
	AccessToken(ctx context.Context, scope string) (string, error)
}

func (c *ColabOneController) driveOutputsPath() string {
	return filepath.Join(c.directory, colabOneDriveOutputsFileName)
}

func (c *ColabOneController) loadDriveOutputs() {
	c.persistMu.Lock()
	defer c.persistMu.Unlock()

	c.mu.Lock()
	c.driveOutputs = make(map[string]map[string]*ColabOneDriveOutputRecord)
	c.mu.Unlock()

	path := c.driveOutputsPath()
	info, statErr := os.Lstat(path)
	if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return
	}
	var disk colabOneDriveOutputsDisk
	if err := json.Unmarshal(raw, &disk); err != nil || disk.Accounts == nil {
		corruptPath := fmt.Sprintf("%s.corrupt-%d", path, time.Now().Unix())
		if _, existsErr := os.Lstat(corruptPath); existsErr == nil {
			corruptPath = fmt.Sprintf("%s.corrupt-%d", path, time.Now().UnixNano())
		}
		if cInfo, cErr := os.Lstat(corruptPath); cErr != nil || (cInfo.Mode()&os.ModeSymlink == 0 && !cInfo.IsDir()) {
			if colabOneRenameReplace(path, corruptPath) != nil && len(raw) > 0 {
				_ = os.WriteFile(corruptPath, raw, 0o600)
			}
		}
		if c.activity != nil {
			c.log("读取本地 Google 云盘作品索引失败，已备份损坏文件为 .corrupt 副本", "云盘", "warning")
		}
		return
	}
	loaded := make(map[string]map[string]*ColabOneDriveOutputRecord, len(disk.Accounts))
	for acctKey, items := range disk.Accounts {
		cleanAcct := strings.TrimSpace(acctKey)
		if cleanAcct == "" || len(items) == 0 {
			continue
		}
		bucket := make(map[string]*ColabOneDriveOutputRecord, len(items))
		for id, rec := range items {
			if rec == nil {
				continue
			}
			cleanID := strings.ToLower(strings.TrimSpace(rec.ID))
			if cleanID == "" {
				cleanID = strings.ToLower(strings.TrimSpace(id))
			}
			if !reHex32ID.MatchString(cleanID) {
				continue
			}
			ext := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(rec.Extension), "."))
			km, ok := allowedDriveOutputKindMIME[ext]
			if !ok {
				continue
			}
			cp := rec.clone()
			cp.ID = cleanID
			cp.Extension = ext
			cp.Kind = km[0]
			cp.Mime = km[1]
			if cp.Size < 0 {
				cp.Size = 0
			}
			cp.SHA256 = sanitizeDriveSHA256(cp.SHA256)
			cp.TaskID = sanitizeDriveTaskID(cp.TaskID)
			cp.DriveFileID = sanitizeDriveFileID(cp.DriveFileID)
			cp.DriveWorkspaceFileID = sanitizeDriveFileID(cp.DriveWorkspaceFileID)
			cp.DriveWorkspaceMetaID = sanitizeDriveFileID(cp.DriveWorkspaceMetaID)
			cp.DriveWebViewLink = sanitizeDriveWebViewLink(cp.DriveWebViewLink)
			cp.DriveSynced = true
			bucket[cleanID] = cp
		}
		if len(bucket) > 0 {
			loaded[cleanAcct] = bucket
		}
	}
	c.mu.Lock()
	c.driveOutputs = loaded
	c.mu.Unlock()
}

func (c *ColabOneController) saveDriveOutputs() error {
	c.persistMu.Lock()
	defer c.persistMu.Unlock()

	c.mu.Lock()
	disk := colabOneDriveOutputsDisk{
		Version:  colabOneDriveOutputsSchemaVer,
		Accounts: make(map[string]map[string]*ColabOneDriveOutputRecord, len(c.driveOutputs)),
	}
	for acctKey, bucket := range c.driveOutputs {
		if len(bucket) == 0 {
			continue
		}
		outBucket := make(map[string]*ColabOneDriveOutputRecord, len(bucket))
		for id, rec := range bucket {
			if rec != nil {
				outBucket[id] = rec.clone()
			}
		}
		if len(outBucket) > 0 {
			disk.Accounts[acctKey] = outBucket
		}
	}
	c.mu.Unlock()

	return AtomicJSON(c.driveOutputsPath(), disk)
}

func (c *ColabOneController) currentDriveAccountKeyLocked() string {
	if c.vault == nil {
		return ""
	}
	colabVal, ok := c.vault.Get("colab")
	if !ok || !asBool(colabVal) {
		return ""
	}
	if profVal, ok := c.vault.Get("google_profile"); ok {
		if prof := asMap(profVal); prof != nil {
			if sub := strings.TrimSpace(asString(prof["sub"])); sub != "" {
				return "sub:" + sub
			}
			if email := strings.ToLower(strings.TrimSpace(asString(prof["email"]))); email != "" {
				return "email:" + email
			}
		}
	}
	if acct := asMap(c.data["account"]); acct != nil {
		if sub := strings.TrimSpace(asString(acct["sub"])); sub != "" {
			return "sub:" + sub
		}
		if email := strings.ToLower(strings.TrimSpace(asString(acct["email"]))); email != "" {
			return "email:" + email
		}
	}
	return ""
}

func (c *ColabOneController) currentDriveAccountKey() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.currentDriveAccountKeyLocked()
}

func (c *ColabOneController) driveDirectReadReady() (bool, string) {
	if c.auth == nil || c.vault == nil || !c.auth.HasDriveScope() {
		return false, ""
	}
	if !c.auth.RequiresDriveQuotaProject() {
		return true, ""
	}
	projVal, _ := c.vault.Get("cloud_project")
	proj := strings.TrimSpace(asString(projVal))
	if proj == "" {
		return false, ""
	}
	return true, proj
}

func (c *ColabOneController) driveAccessToken(ctx context.Context) (string, error) {
	if c.auth == nil {
		return "", errors.New("请先完成 Google 登录")
	}
	if prov, ok := c.auth.(authAccessTokenProvider); ok {
		tok, err := prov.AccessToken(ctx, "colab")
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(tok) == "" {
			return "", errors.New("请先完成 Google 登录")
		}
		return tok, nil
	}
	raw, err := c.auth.Credentials(ctx, "colab")
	if err != nil {
		return "", err
	}
	if raw == nil {
		return "", errors.New("请先完成 Google 登录")
	}
	if m := asMap(raw); m != nil {
		if tok := strings.TrimSpace(asString(m["token"])); tok != "" {
			return tok, nil
		}
		if tok := strings.TrimSpace(asString(m["access_token"])); tok != "" {
			return tok, nil
		}
	}
	if encoded, err := json.Marshal(raw); err == nil {
		var parsed struct {
			Token       string `json:"token"`
			AccessToken string `json:"access_token"`
		}
		if json.Unmarshal(encoded, &parsed) == nil {
			if tok := strings.TrimSpace(parsed.Token); tok != "" {
				return tok, nil
			}
			if tok := strings.TrimSpace(parsed.AccessToken); tok != "" {
				return tok, nil
			}
		}
	}
	return "", errors.New("请先完成 Google 登录")
}

func driveOutputRecordFromMap(raw map[string]any) *ColabOneDriveOutputRecord {
	if raw == nil {
		return nil
	}
	id := strings.ToLower(strings.TrimSpace(asString(raw["id"])))
	if !reHex32ID.MatchString(id) {
		return nil
	}
	ext := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(asString(raw["extension"])), "."))
	if ext == "" {
		ext = strings.ToLower(strings.TrimPrefix(filepath.Ext(asString(raw["filename"])), "."))
	}
	km, ok := allowedDriveOutputKindMIME[ext]
	if !ok {
		return nil
	}
	kind := km[0]
	mime := km[1]
	size, _ := asInt64(raw["size"])
	if size < 0 {
		size = 0
	}
	createdAt, _ := asFloat(raw["created_at"])
	elapsed, _ := asFloat(raw["elapsed"])
	cuConsumed, _ := asFloat(raw["cu_consumed"])
	if cuConsumed < 0 {
		cuConsumed = 0
	}
	cuRate, _ := asFloat(raw["cu_rate"])
	if cuRate < 0 {
		cuRate = 0
	}
	gpuName := strings.TrimSpace(asString(raw["gpu_name"]))
	cuEstimated := asBool(raw["cu_estimated"])
	fname := strings.TrimSpace(asString(raw["filename"]))
	if fname == "" {
		fname = id + "." + ext
	}
	return &ColabOneDriveOutputRecord{
		ID:           id,
		WorkflowID:   strings.TrimSpace(asString(raw["workflow_id"])),
		WorkflowName: strings.TrimSpace(asString(raw["workflow_name"])),
		Kind:         kind,
		Filename:     fname,
		Extension:    ext,
		Mime:         mime,
		Size:         size,
		SHA256:       sanitizeDriveSHA256(asString(raw["sha256"])),
		CreatedAt:    createdAt,
		Elapsed:      elapsed,
		CUConsumed:   colabOneRoundCU(cuConsumed),
		CURate:       colabOneRoundCU(cuRate),
		GPUName:      gpuName,
		CUEstimated:  cuEstimated,
		PromptID:     strings.TrimSpace(asString(raw["prompt_id"])),
		TaskID:       sanitizeDriveTaskID(asString(raw["task_id"])),
		DriveSynced:  true,
	}
}

func mergeDriveOutputRecord(dst, src *ColabOneDriveOutputRecord) bool {
	if dst == nil || src == nil {
		return false
	}
	changed := false
	setStr := func(target *string, val string) {
		if val != "" && *target != val {
			*target = val
			changed = true
		}
	}
	setStr(&dst.WorkflowID, src.WorkflowID)
	if src.WorkflowName != "" && (src.WorkflowName != "Google 云盘作品" || dst.WorkflowName == "") {
		setStr(&dst.WorkflowName, src.WorkflowName)
	}
	setStr(&dst.Filename, src.Filename)
	if srcExt := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(src.Extension), ".")); srcExt != "" {
		if km, ok := allowedDriveOutputKindMIME[srcExt]; ok {
			setStr(&dst.Extension, srcExt)
			setStr(&dst.Kind, km[0])
			setStr(&dst.Mime, km[1])
		}
	}
	if dstExt := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(dst.Extension), ".")); dstExt != "" {
		if km, ok := allowedDriveOutputKindMIME[dstExt]; ok {
			setStr(&dst.Kind, km[0])
			setStr(&dst.Mime, km[1])
		}
	}
	if src.Size > 0 && dst.Size != src.Size {
		dst.Size = src.Size
		changed = true
	}
	setStr(&dst.SHA256, sanitizeDriveSHA256(src.SHA256))
	if src.CreatedAt > 0 && dst.CreatedAt == 0 {
		dst.CreatedAt = src.CreatedAt
		changed = true
	}
	if src.Elapsed > 0 && dst.Elapsed == 0 {
		dst.Elapsed = src.Elapsed
		changed = true
	}
	if src.CUConsumed > 0 && (dst.CUConsumed <= 0 || (dst.CUEstimated && !src.CUEstimated)) {
		upgradingFromEst := dst.CUEstimated && !src.CUEstimated
		dst.CUConsumed = colabOneRoundCU(src.CUConsumed)
		dst.CUEstimated = src.CUEstimated
		if upgradingFromEst && src.CURate > 0 {
			dst.CURate = colabOneRoundCU(src.CURate)
		}
		changed = true
	}
	if src.CURate > 0 && dst.CURate <= 0 {
		dst.CURate = colabOneRoundCU(src.CURate)
		changed = true
	}
	if dst.GPUName == "" && src.GPUName != "" {
		dst.GPUName = src.GPUName
		changed = true
	}
	setStr(&dst.PromptID, src.PromptID)
	setStr(&dst.TaskID, sanitizeDriveTaskID(src.TaskID))
	if src.DriveSynced && !dst.DriveSynced {
		dst.DriveSynced = true
		changed = true
	}
	if src.DriveExported && !dst.DriveExported {
		dst.DriveExported = true
		changed = true
	}
	if src.DriveExported {
		setStr(&dst.DrivePath, src.DrivePath)
		setStr(&dst.DriveFolder, src.DriveFolder)
	} else if dst.DrivePath == "" {
		setStr(&dst.DrivePath, src.DrivePath)
		setStr(&dst.DriveFolder, src.DriveFolder)
	}
	setStr(&dst.DriveFileID, sanitizeDriveFileID(src.DriveFileID))
	setStr(&dst.DriveWorkspaceFileID, sanitizeDriveFileID(src.DriveWorkspaceFileID))
	setStr(&dst.DriveWorkspaceMetaID, sanitizeDriveFileID(src.DriveWorkspaceMetaID))
	setStr(&dst.DriveWebViewLink, sanitizeDriveWebViewLink(src.DriveWebViewLink))
	setStr(&dst.DriveMode, src.DriveMode)
	if src.SyncedAt > dst.SyncedAt {
		dst.SyncedAt = src.SyncedAt
	}
	return changed
}

func pruneDriveOutputsBucketLocked(bucket map[string]*ColabOneDriveOutputRecord) {
	if len(bucket) <= colabOneDriveOutputsMaxPerAcct {
		return
	}
	items := make([]*ColabOneDriveOutputRecord, 0, len(bucket))
	for _, rec := range bucket {
		if rec != nil {
			items = append(items, rec)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].CreatedAt != items[j].CreatedAt {
			return items[i].CreatedAt > items[j].CreatedAt
		}
		return items[i].ID < items[j].ID
	})
	for _, drop := range items[colabOneDriveOutputsMaxPerAcct:] {
		delete(bucket, drop.ID)
	}
}

type colabOneOutputBillingContext struct {
	Email        string
	Rate         float64
	Assignments  int
	GPUName      string
	RTReady      bool
	RTStartedAt  float64
	RunStartedAt float64
	Now          float64
}

func (c *ColabOneController) currentOutputBillingContext(rt map[string]any) colabOneOutputBillingContext {
	c.mu.Lock()
	acct := asMap(c.data["account"])
	email := strings.ToLower(strings.TrimSpace(asString(acct["email"])))
	rate, _ := asFloat(acct["rate"])
	assignments, _ := asInt(acct["assignments"])
	c.mu.Unlock()

	if email == "" && c.vault != nil {
		if profVal, ok := c.vault.Get("google_profile"); ok {
			if prof := asMap(profVal); prof != nil {
				email = strings.ToLower(strings.TrimSpace(asString(prof["email"])))
			}
		}
	}
	if rate < 0 {
		rate = 0
	}
	if assignments < 0 {
		assignments = 0
	}
	gpuName := ""
	rtReady := false
	rtStartedAt := 0.0
	if rt != nil {
		rtReady = asString(rt["state"]) == "ready"
		if gpuMap := asMap(rt["gpu"]); gpuMap != nil {
			gpuName = strings.TrimSpace(asString(gpuMap["name"]))
		}
		if st, ok := asFloat(rt["started_at"]); ok && st > 0 {
			rtStartedAt = st
		}
	}
	runStartedAt := 0.0
	if c.cuUsage != nil {
		runStartedAt = c.cuUsage.ActiveRunStartedAt(email)
	}
	return colabOneOutputBillingContext{
		Email:        email,
		Rate:         rate,
		Assignments:  assignments,
		GPUName:      gpuName,
		RTReady:      rtReady,
		RTStartedAt:  rtStartedAt,
		RunStartedAt: runStartedAt,
		Now:          nowUnixFloat(),
	}
}

func (c *ColabOneController) resolveOutputCU(
	bctx colabOneOutputBillingContext,
	createdAt, elapsed float64,
	isLiveRuntimeOutput bool,
) (cu float64, rate float64, gpuName string, estimated bool, ok bool) {
	if elapsed <= 0 {
		return 0, 0, "", false, false
	}
	// Only attribute the currently attached GPU name or live account burn rate to outputs
	// produced during the current runtime session — never to older outputs restored from Drive workspace.
	// Prefer the remote VM's authoritative started_at when available; fall back to cuUsage's active
	// run start (with 5 min tolerance for billing API startup lag) or recent-window check.
	isFromCurrentSession := false
	if isLiveRuntimeOutput && bctx.RTReady && createdAt > 0 {
		switch {
		case bctx.RTStartedAt > 0:
			isFromCurrentSession = createdAt >= bctx.RTStartedAt-120.0
		case bctx.RunStartedAt > 0:
			isFromCurrentSession = createdAt >= bctx.RunStartedAt-300.0
		default:
			now := bctx.Now
			if now <= 0 {
				now = nowUnixFloat()
			}
			isFromCurrentSession = math.Abs(now-createdAt) <= elapsed+600.0
		}
	}
	if c.cuUsage != nil && createdAt > 0 {
		startTS := createdAt - elapsed
		if estCU, avgRate, estFlag, found := c.cuUsage.EstimateWindowCU(bctx.Email, startTS, createdAt); found && estCU > 0 {
			gn := ""
			if isFromCurrentSession {
				gn = bctx.GPUName
			}
			return estCU, avgRate, gn, estFlag, true
		}
	}
	if isFromCurrentSession && bctx.Rate > colabOneCUEpsilon {
		effRate := bctx.Rate
		est := false
		if bctx.Assignments > 1 {
			effRate = bctx.Rate / float64(bctx.Assignments)
			est = true
		}
		calcCU := colabOneRoundCU(effRate * elapsed / 3600.0)
		if calcCU > 0 {
			return calcCU, colabOneRoundCU(effRate), bctx.GPUName, est, true
		}
	}
	return 0, 0, "", false, false
}

func (c *ColabOneController) recordSyncedRemoteOutputs(rt map[string]any, rawOutputs []any) {
	driveMap := asMap(rt["drive"])
	if driveMap == nil || !asBool(driveMap["connected"]) {
		return
	}
	bctx := c.currentOutputBillingContext(rt)
	remoteFolder := strings.TrimSpace(asString(driveMap["remote_folder"]))
	if remoteFolder == "" {
		remoteFolder = colabOneDriveFolderName
	}
	driveMode := strings.TrimSpace(asString(driveMap["mode"]))
	exportedByID := make(map[string]map[string]any)
	if comfy := asMap(rt["comfyui"]); comfy != nil {
		if ar := asMap(comfy["auto_release"]); ar != nil {
			for _, rawExp := range asAnySlice(ar["exported"]) {
				exp := asMap(rawExp)
				if exp == nil {
					continue
				}
				eid := strings.ToLower(strings.TrimSpace(asString(exp["id"])))
				if reHex32ID.MatchString(eid) {
					exportedByID[eid] = exp
				}
			}
		}
	}

	now := nowUnixFloat()
	// Pre-compute CU attribution outside c.mu (cuUsage has its own mutex hierarchy).
	parsedRecords := make([]*ColabOneDriveOutputRecord, 0, len(rawOutputs))
	for _, rawItem := range rawOutputs {
		rec := driveOutputRecordFromMap(asMap(rawItem))
		if rec == nil {
			continue
		}
		if rec.CUConsumed <= 0 && rec.Elapsed > 0 {
			if cu, rate, gn, est, ok := c.resolveOutputCU(bctx, rec.CreatedAt, rec.Elapsed, true); ok {
				rec.CUConsumed = cu
				rec.CURate = rate
				if gn != "" {
					rec.GPUName = gn
				}
				rec.CUEstimated = est
			}
		}
		parsedRecords = append(parsedRecords, rec)
	}

	c.mu.Lock()
	acctKey := c.currentDriveAccountKeyLocked()
	if acctKey == "" {
		c.mu.Unlock()
		return
	}
	if c.driveOutputs == nil {
		c.driveOutputs = make(map[string]map[string]*ColabOneDriveOutputRecord)
	}
	bucket := c.driveOutputs[acctKey]
	if bucket == nil {
		bucket = make(map[string]*ColabOneDriveOutputRecord)
		c.driveOutputs[acctKey] = bucket
	}
	changed := false
	for _, rec := range parsedRecords {
		rec.DriveSynced = true
		rec.DriveMode = driveMode
		rec.DriveFolder = remoteFolder + "/" + colabOneDriveWorkspaceSubdir + "/" + colabOneDriveComfyOutputsSubdir
		rec.DrivePath = "My Drive/" + rec.DriveFolder + "/" + rec.ID + "." + rec.Extension
		rec.SyncedAt = now
		if exp := exportedByID[rec.ID]; exp != nil {
			rec.DriveExported = true
			if rf := strings.TrimSpace(asString(exp["remote_folder"])); rf != "" {
				rec.DriveFolder = rf
			} else {
				rec.DriveFolder = remoteFolder + "/" + colabOneDriveOutputsSubdir
			}
			if dp := strings.TrimSpace(asString(exp["drive_path"])); dp != "" {
				rec.DrivePath = dp
			} else if fn := strings.TrimSpace(asString(exp["filename"])); fn != "" {
				rec.DrivePath = "My Drive/" + rec.DriveFolder + "/" + fn
			}
			if fid := sanitizeDriveFileID(asString(exp["drive_file_id"])); fid != "" {
				rec.DriveFileID = fid
			}
			if link := sanitizeDriveWebViewLink(asString(exp["drive_web_view_link"])); link != "" {
				rec.DriveWebViewLink = link
			}
		}
		if existing, exists := bucket[rec.ID]; exists && existing != nil {
			if mergeDriveOutputRecord(existing, rec) {
				changed = true
			}
		} else {
			bucket[rec.ID] = rec
			changed = true
		}
		c.noteDriveOutputAddedLocked(acctKey, rec.ID)
	}
	if changed {
		pruneDriveOutputsBucketLocked(bucket)
	}
	c.mu.Unlock()

	if changed {
		_ = c.saveDriveOutputs()
	}
}

func (c *ColabOneController) recordExportedDriveOutput(identity string, exportRes map[string]any) {
	cleanID := strings.ToLower(strings.TrimSpace(identity))
	if !reHex32ID.MatchString(cleanID) || exportRes == nil {
		return
	}
	now := nowUnixFloat()

	c.mu.Lock()
	rtSnap := deepCopyMap(asMap(c.data["runtime"]))
	var liveRec *ColabOneDriveOutputRecord
	for _, rawItem := range asAnySlice(c.data["comfy_outputs"]) {
		m := asMap(rawItem)
		if strings.EqualFold(asString(m["id"]), cleanID) {
			liveRec = driveOutputRecordFromMap(m)
			break
		}
	}
	c.mu.Unlock()

	if liveRec != nil && liveRec.CUConsumed <= 0 && liveRec.Elapsed > 0 {
		bctx := c.currentOutputBillingContext(rtSnap)
		if cu, rate, gn, est, ok := c.resolveOutputCU(bctx, liveRec.CreatedAt, liveRec.Elapsed, true); ok {
			liveRec.CUConsumed = cu
			liveRec.CURate = rate
			if gn != "" {
				liveRec.GPUName = gn
			}
			liveRec.CUEstimated = est
		}
	}

	c.mu.Lock()
	acctKey := c.currentDriveAccountKeyLocked()
	if acctKey == "" {
		c.mu.Unlock()
		return
	}
	if c.driveOutputs == nil {
		c.driveOutputs = make(map[string]map[string]*ColabOneDriveOutputRecord)
	}
	bucket := c.driveOutputs[acctKey]
	if bucket == nil {
		bucket = make(map[string]*ColabOneDriveOutputRecord)
		c.driveOutputs[acctKey] = bucket
	}
	target := bucket[cleanID]
	if target == nil && liveRec != nil {
		target = liveRec
		bucket[cleanID] = target
	} else if target != nil && liveRec != nil {
		mergeDriveOutputRecord(target, liveRec)
	}
	if target == nil {
		fname := strings.TrimSpace(asString(exportRes["filename"]))
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(fname), "."))
		km, ok := allowedDriveOutputKindMIME[ext]
		if !ok {
			c.mu.Unlock()
			return
		}
		sz, _ := asInt64(exportRes["size"])
		target = &ColabOneDriveOutputRecord{
			ID:           cleanID,
			WorkflowName: "Google 云盘作品",
			Kind:         km[0],
			Filename:     fname,
			Extension:    ext,
			Mime:         km[1],
			Size:         sz,
			CreatedAt:    now,
			DriveSynced:  true,
		}
		bucket[cleanID] = target
	}
	target.DriveSynced = true
	target.DriveExported = true
	if rf := strings.TrimSpace(asString(exportRes["remote_folder"])); rf != "" {
		target.DriveFolder = rf
	} else if target.DriveFolder == "" {
		target.DriveFolder = colabOneDriveFolderName + "/" + colabOneDriveOutputsSubdir
	}
	if dp := strings.TrimSpace(asString(exportRes["drive_path"])); dp != "" {
		target.DrivePath = dp
	} else if fn := strings.TrimSpace(asString(exportRes["filename"])); fn != "" {
		target.DrivePath = "My Drive/" + target.DriveFolder + "/" + fn
	}
	if fid := sanitizeDriveFileID(asString(exportRes["drive_file_id"])); fid != "" {
		target.DriveFileID = fid
	}
	if link := sanitizeDriveWebViewLink(asString(exportRes["drive_web_view_link"])); link != "" {
		target.DriveWebViewLink = link
	}
	if mode := strings.TrimSpace(asString(exportRes["mode"])); mode != "" {
		target.DriveMode = mode
	}
	target.SyncedAt = now
	c.noteDriveOutputAddedLocked(acctKey, cleanID)
	pruneDriveOutputsBucketLocked(bucket)
	c.mu.Unlock()

	_ = c.saveDriveOutputs()
}

func (c *ColabOneController) removeDriveOutputRecord(identity string) {
	cleanID := strings.ToLower(strings.TrimSpace(identity))
	if !reHex32ID.MatchString(cleanID) {
		return
	}
	c.mu.Lock()
	acctKey := c.currentDriveAccountKeyLocked()
	removed := false
	var oldRec *ColabOneDriveOutputRecord
	if acctKey != "" && c.driveOutputs != nil {
		if bucket := c.driveOutputs[acctKey]; bucket != nil {
			if rec, exists := bucket[cleanID]; exists {
				oldRec = rec
				delete(bucket, cleanID)
				removed = true
			}
		}
	}
	c.noteDriveOutputDeletedLocked(acctKey, cleanID, oldRec)
	c.mu.Unlock()
	if removed {
		_ = c.saveDriveOutputs()
	}
}

func (c *ColabOneController) mergeComfyOutputsForSnapshot(
	rt map[string]any,
	liveOutputs []any,
	canDirectRead bool,
) ([]any, map[string]any) {
	c.mu.Lock()
	acctKey := c.currentDriveAccountKeyLocked()
	var saved []*ColabOneDriveOutputRecord
	if acctKey != "" && c.driveOutputs != nil {
		if bucket := c.driveOutputs[acctKey]; len(bucket) > 0 {
			saved = make([]*ColabOneDriveOutputRecord, 0, len(bucket))
			for _, rec := range bucket {
				if rec != nil {
					saved = append(saved, rec.clone())
				}
			}
		}
	}
	syncing := false
	lastSyncedAt := 0.0
	lastErr := ""
	if acctKey != "" {
		syncing = c.driveOutputsSyncing && c.driveOutputsSyncAccountKey == acctKey
		lastSyncedAt = c.driveOutputsLastSynced
		lastErr = c.driveOutputsSyncError
	}
	c.mu.Unlock()

	savedByID := make(map[string]*ColabOneDriveOutputRecord, len(saved))
	for _, rec := range saved {
		savedByID[rec.ID] = rec
	}

	statusMap := map[string]any{
		"syncing":         syncing,
		"last_synced_at":  lastSyncedAt,
		"can_direct_read": canDirectRead,
		"count":           len(saved),
		"error":           lastErr,
	}

	rtState := asString(rt["state"])
	driveConnected := asBool(asMap(rt["drive"])["connected"])
	bctx := c.currentOutputBillingContext(rt)

	applyOutputCU := func(m map[string]any, rec *ColabOneDriveOutputRecord, isLive bool) {
		if m == nil {
			return
		}
		if existingCU, ok := asFloat(m["cu_consumed"]); ok && existingCU > 0 {
			return
		}
		if rec != nil && rec.CUConsumed > 0 {
			m["cu_consumed"] = colabOneRoundCU(rec.CUConsumed)
			if rec.CURate > 0 {
				m["cu_rate"] = colabOneRoundCU(rec.CURate)
			}
			if rec.GPUName != "" {
				m["gpu_name"] = rec.GPUName
			}
			if rec.CUEstimated {
				m["cu_estimated"] = true
			}
			return
		}
		createdAt, _ := asFloat(m["created_at"])
		elapsed, _ := asFloat(m["elapsed"])
		if cu, rate, gn, est, ok := c.resolveOutputCU(bctx, createdAt, elapsed, isLive); ok {
			m["cu_consumed"] = cu
			if rate > 0 {
				m["cu_rate"] = rate
			}
			if gn != "" {
				m["gpu_name"] = gn
			}
			if est {
				m["cu_estimated"] = true
			}
		}
	}

	sortOutputMaps := func(items []any) {
		sort.SliceStable(items, func(i, j int) bool {
			mi := asMap(items[i])
			mj := asMap(items[j])
			ci, _ := asFloat(mi["created_at"])
			cj, _ := asFloat(mj["created_at"])
			if ci != cj {
				return ci > cj
			}
			return asString(mi["id"]) < asString(mj["id"])
		})
	}

	// When a remote GPU is ready, return live outputs (enriched with Drive linkage and CU metadata)
	// plus any Drive-only archived outputs not currently on the remote VM.
	if rtState == "ready" {
		result := make([]any, 0, len(liveOutputs)+len(saved))
		seen := make(map[string]bool, len(liveOutputs))
		for _, rawItem := range liveOutputs {
			m := deepCopyMap(asMap(rawItem))
			if m == nil {
				continue
			}
			id := strings.ToLower(strings.TrimSpace(asString(m["id"])))
			if id != "" {
				seen[id] = true
			}
			m["storage"] = "runtime"
			m["drive_previewable"] = true
			rec := savedByID[id]
			if rec != nil {
				m["drive_synced"] = rec.DriveSynced || driveConnected
				m["drive_exported"] = rec.DriveExported
				if rec.DrivePath != "" {
					m["drive_path"] = rec.DrivePath
				}
				if rec.DriveFolder != "" {
					m["drive_folder"] = rec.DriveFolder
				}
				if fid := rec.preferredDriveFileID(); fid != "" {
					m["drive_file_id"] = fid
				}
				if link := rec.resolvedWebViewLink(); link != "" {
					m["drive_web_view_link"] = link
				}
				if rec.DriveMode != "" {
					m["drive_mode"] = rec.DriveMode
				}
			} else if driveConnected {
				m["drive_synced"] = true
			}
			applyOutputCU(m, rec, true)
			result = append(result, m)
		}
		// Append Drive-persisted items not already present on the active remote VM.
		// When Drive is already connected on this GPU, avoid resurrecting workspace-only
		// items deleted elsewhere; still surface archived ColabOne_Drive/outputs exports.
		for _, rec := range saved {
			if seen[rec.ID] {
				continue
			}
			if driveConnected && !rec.DriveExported {
				continue
			}
			m := rec.toOutputMap("drive", canDirectRead)
			applyOutputCU(m, rec, false)
			result = append(result, m)
		}
		sortOutputMaps(result)
		return result, statusMap
	}

	// GPU is stopped/releasing/starting: serve persisted Google Drive outputs for the signed-in account.
	if len(saved) == 0 {
		return []any{}, statusMap
	}
	sort.SliceStable(saved, func(i, j int) bool {
		if saved[i].CreatedAt != saved[j].CreatedAt {
			return saved[i].CreatedAt > saved[j].CreatedAt
		}
		return saved[i].ID < saved[j].ID
	})
	result := make([]any, 0, len(saved))
	for _, rec := range saved {
		m := rec.toOutputMap("drive", canDirectRead)
		applyOutputCU(m, rec, false)
		result = append(result, m)
	}
	return result, statusMap
}

// ---------------------------------------------------------------------------
// Direct Google Drive REST API v3 client (works offline from GPU)
// ---------------------------------------------------------------------------

func (c *ColabOneController) driveAPIBase() string {
	base := strings.TrimRight(strings.TrimSpace(c.driveAPIBaseURL), "/")
	if base != "" {
		return base
	}
	return colabOneDriveAPIDefaultBaseURL
}

func (c *ColabOneController) driveClient() *http.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.driveHTTPClient == nil {
		c.driveHTTPClient = &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: refuseDriveRedirects,
		}
		c.driveHTTPClientOwned = true
	}
	return c.driveHTTPClient
}

type driveAPIFile struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	MimeType       string `json:"mimeType"`
	Size           string `json:"size"`
	CreatedTime    string `json:"createdTime"`
	ModifiedTime   string `json:"modifiedTime"`
	SHA256Checksum string `json:"sha256Checksum"`
	WebViewLink    string `json:"webViewLink"`
}

func (f driveAPIFile) sizeBytes() int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(f.Size), 10, 64)
	if n < 0 {
		return 0
	}
	return n
}

func parseDriveRFC3339Unix(raw string) float64 {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return float64(t.UnixNano()) / 1e9
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return float64(t.Unix())
	}
	return 0
}

func (f driveAPIFile) createdOrModifiedUnix() float64 {
	if ts := parseDriveRFC3339Unix(f.CreatedTime); ts > 0 {
		return ts
	}
	return parseDriveRFC3339Unix(f.ModifiedTime)
}

func escapeDriveQueryLiteral(val string) string {
	val = strings.ReplaceAll(val, `\`, `\\`)
	return strings.ReplaceAll(val, `'`, `\'`)
}

func (c *ColabOneController) doDriveAPIRequest(
	ctx context.Context,
	method string,
	reqURL string,
	token string,
	quotaProject string,
	body io.Reader,
	headers map[string]string,
	maxBytes int64,
) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if quotaProject != "" {
		req.Header.Set("X-Goog-User-Project", quotaProject)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.driveClient().Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, data, nil
}

func (c *ColabOneController) driveFindChildFolder(
	ctx context.Context,
	token string,
	quotaProject string,
	parentID string,
	name string,
) (string, error) {
	q := fmt.Sprintf(
		"name = '%s' and mimeType = 'application/vnd.google-apps.folder' and '%s' in parents and trashed = false",
		escapeDriveQueryLiteral(name),
		escapeDriveQueryLiteral(parentID),
	)
	u := c.driveAPIBase() + "/files?q=" + url.QueryEscape(q) + "&fields=files(id,name)&pageSize=5&spaces=drive"
	status, body, err := c.doDriveAPIRequest(ctx, http.MethodGet, u, token, quotaProject, nil, nil, colabOneDriveMaxListBodyBytes)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", &ColabOneHTTPError{StatusCode: status, Message: fmt.Sprintf("读取 Google 云盘目录失败（HTTP %d）", status)}
	}
	var parsed struct {
		Files []driveAPIFile `json:"files"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Files) == 0 {
		return "", nil
	}
	return strings.TrimSpace(parsed.Files[0].ID), nil
}

func (c *ColabOneController) driveListChildFiles(
	ctx context.Context,
	token string,
	quotaProject string,
	parentID string,
) ([]driveAPIFile, error) {
	q := fmt.Sprintf(
		"'%s' in parents and mimeType != 'application/vnd.google-apps.folder' and trashed = false",
		escapeDriveQueryLiteral(parentID),
	)
	var all []driveAPIFile
	pageToken := ""
	for page := 0; page < 5; page++ {
		u := c.driveAPIBase() + "/files?q=" + url.QueryEscape(q) +
			"&fields=nextPageToken,files(id,name,mimeType,size,createdTime,modifiedTime,sha256Checksum,webViewLink)&pageSize=200&spaces=drive"
		if pageToken != "" {
			u += "&pageToken=" + url.QueryEscape(pageToken)
		}
		status, body, err := c.doDriveAPIRequest(ctx, http.MethodGet, u, token, quotaProject, nil, nil, colabOneDriveMaxListBodyBytes)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, &ColabOneHTTPError{StatusCode: status, Message: fmt.Sprintf("读取 Google 云盘作品列表失败（HTTP %d）", status)}
		}
		var parsed struct {
			NextPageToken string         `json:"nextPageToken"`
			Files         []driveAPIFile `json:"files"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, err
		}
		all = append(all, parsed.Files...)
		if strings.TrimSpace(parsed.NextPageToken) == "" {
			break
		}
		pageToken = strings.TrimSpace(parsed.NextPageToken)
	}
	return all, nil
}

func syntheticDriveOutputID(fileID string) string {
	sum := sha256.Sum256([]byte("colabone-drive-output:" + strings.TrimSpace(fileID)))
	return hex.EncodeToString(sum[:16])
}

// SyncDriveOutputs queries Google Drive API v3 for ColabOne_Drive outputs so users can browse,
// preview, and download results in the Outputs Drawer without running a GPU instance.
func (c *ColabOneController) SyncDriveOutputs(ctx context.Context) (map[string]any, error) {
	if ctx == nil {
		ctx = c.rootCtx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sem := c.driveOutputsSyncSemaphore()
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-sem }()

	acctKey := c.currentDriveAccountKey()
	if acctKey == "" {
		return map[string]any{
			"synced":          false,
			"can_direct_read": false,
			"count":           0,
		}, nil
	}
	canDirectRead, quotaProject := c.driveDirectReadReady()
	if !canDirectRead {
		c.mu.Lock()
		count := len(c.driveOutputs[acctKey])
		c.driveOutputsSyncError = ""
		c.mu.Unlock()
		return map[string]any{
			"synced":          false,
			"can_direct_read": false,
			"reason":          "drivefs_only",
			"count":           count,
		}, nil
	}

	c.mu.Lock()
	c.driveOutputsSyncing = true
	c.driveOutputsSyncAccountKey = acctKey
	c.driveOutputsSyncError = ""
	if c.driveOutputsSyncDeleted == nil {
		c.driveOutputsSyncDeleted = make(map[string]map[string]bool)
	}
	if c.driveOutputsSyncDeletedFileIDs == nil {
		c.driveOutputsSyncDeletedFileIDs = make(map[string]map[string]bool)
	}
	if c.driveOutputsSyncAdded == nil {
		c.driveOutputsSyncAdded = make(map[string]map[string]bool)
	}
	c.driveOutputsSyncDeleted[acctKey] = make(map[string]bool)
	c.driveOutputsSyncDeletedFileIDs[acctKey] = make(map[string]bool)
	c.driveOutputsSyncAdded[acctKey] = make(map[string]bool)
	c.mu.Unlock()

	finishErr := func(err error) (map[string]any, error) {
		msg := SafeMessage(err.Error())
		c.mu.Lock()
		c.clearDriveOutputsSyncTrackingLocked(acctKey)
		if c.currentDriveAccountKeyLocked() == acctKey {
			c.driveOutputsSyncError = msg
		}
		count := len(c.driveOutputs[acctKey])
		c.mu.Unlock()
		return map[string]any{
			"synced":          false,
			"can_direct_read": true,
			"count":           count,
			"error":           msg,
		}, err
	}

	token, err := c.driveAccessToken(ctx)
	if err != nil {
		return finishErr(err)
	}

	rootFolderID, err := c.driveFindChildFolder(ctx, token, quotaProject, "root", colabOneDriveFolderName)
	if err != nil {
		return finishErr(err)
	}

	discovered := make(map[string]*ColabOneDriveOutputRecord)
	now := nowUnixFloat()
	bctx := c.currentOutputBillingContext(nil)

	// Snapshot existing records so we can reuse parsed workspace metadata JSON without re-downloading.
	c.mu.Lock()
	existingSnap := make(map[string]*ColabOneDriveOutputRecord)
	if bucket := c.driveOutputs[acctKey]; bucket != nil {
		for id, rec := range bucket {
			existingSnap[id] = rec.clone()
		}
	}
	c.mu.Unlock()

	if rootFolderID != "" {
		// 1) Inspect workspace/comfyui-outputs
		wsFolderID, err := c.driveFindChildFolder(ctx, token, quotaProject, rootFolderID, colabOneDriveWorkspaceSubdir)
		if err != nil {
			return finishErr(err)
		}
		if wsFolderID != "" {
			coFolderID, err := c.driveFindChildFolder(ctx, token, quotaProject, wsFolderID, colabOneDriveComfyOutputsSubdir)
			if err != nil {
				return finishErr(err)
			}
			if coFolderID != "" {
				wsFiles, err := c.driveListChildFiles(ctx, token, quotaProject, coFolderID)
				if err != nil {
					return finishErr(err)
				}
				metaByID := make(map[string]driveAPIFile)
				assetByID := make(map[string]driveAPIFile)
				for _, f := range wsFiles {
					name := strings.TrimSpace(f.Name)
					if name == "" || strings.HasPrefix(name, ".") {
						continue
					}
					ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
					stem := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
					if !reHex32ID.MatchString(stem) {
						continue
					}
					if ext == "json" {
						metaByID[stem] = f
					} else if _, ok := allowedDriveOutputKindMIME[ext]; ok {
						assetByID[stem] = f
					}
				}
				for id, asset := range assetByID {
					assetSize := asset.sizeBytes()
					if assetSize <= 0 {
						continue
					}
					ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(asset.Name), "."))
					km := allowedDriveOutputKindMIME[ext]
					metaFile, hasMeta := metaByID[id]
					var rec *ColabOneDriveOutputRecord
					if prev := existingSnap[id]; prev != nil && hasMeta && prev.DriveWorkspaceMetaID == metaFile.ID && prev.Filename != "" {
						// Reuse immutable workflow metadata without carrying stale Drive export fields.
						rec = &ColabOneDriveOutputRecord{
							ID:           id,
							WorkflowID:   prev.WorkflowID,
							WorkflowName: prev.WorkflowName,
							Kind:         km[0],
							Filename:     prev.Filename,
							Extension:    ext,
							Mime:         km[1],
							Size:         prev.Size,
							SHA256:       sanitizeDriveSHA256(prev.SHA256),
							CreatedAt:    prev.CreatedAt,
							Elapsed:      prev.Elapsed,
							CUConsumed:   prev.CUConsumed,
							CURate:       prev.CURate,
							GPUName:      prev.GPUName,
							CUEstimated:  prev.CUEstimated,
							PromptID:     prev.PromptID,
							TaskID:       sanitizeDriveTaskID(prev.TaskID),
							DriveSynced:  true,
						}
					} else if hasMeta {
						metaURL := c.driveAPIBase() + "/files/" + url.PathEscape(metaFile.ID) + "?alt=media"
						st, metaBytes, merr := c.doDriveAPIRequest(ctx, http.MethodGet, metaURL, token, quotaProject, nil, nil, colabOneDriveMaxMetaBytes)
						if merr == nil && st == http.StatusOK {
							var parsed map[string]any
							if decodeJSONWithNumbers(metaBytes, &parsed) == nil {
								rec = driveOutputRecordFromMap(parsed)
							}
						}
					}
					if rec == nil {
						rec = &ColabOneDriveOutputRecord{
							ID:           id,
							WorkflowName: "Google 云盘作品",
							Kind:         km[0],
							Filename:     asset.Name,
							Extension:    ext,
							Mime:         km[1],
							Size:         assetSize,
							CreatedAt:    asset.createdOrModifiedUnix(),
						}
					}
					rec.Size = assetSize
					if sha := sanitizeDriveSHA256(asset.SHA256Checksum); sha != "" && rec.SHA256 == "" {
						rec.SHA256 = sha
					}
					if rec.CreatedAt <= 0 {
						rec.CreatedAt = asset.createdOrModifiedUnix()
					}
					if prev := existingSnap[id]; prev != nil {
						if rec.CUConsumed <= 0 && prev.CUConsumed > 0 {
							rec.CUConsumed = prev.CUConsumed
							rec.CUEstimated = prev.CUEstimated
						}
						if rec.CURate <= 0 && prev.CURate > 0 {
							rec.CURate = prev.CURate
						}
						if rec.GPUName == "" && prev.GPUName != "" {
							rec.GPUName = prev.GPUName
						}
					}
					if rec.CUConsumed <= 0 && rec.Elapsed > 0 && rec.CreatedAt > 0 {
						if cu, rate, gn, est, ok := c.resolveOutputCU(bctx, rec.CreatedAt, rec.Elapsed, false); ok {
							rec.CUConsumed = cu
							rec.CURate = rate
							if gn != "" {
								rec.GPUName = gn
							}
							rec.CUEstimated = est
						}
					}
					rec.DriveSynced = true
					rec.DriveWorkspaceFileID = sanitizeDriveFileID(asset.ID)
					if hasMeta {
						rec.DriveWorkspaceMetaID = sanitizeDriveFileID(metaFile.ID)
					}
					rec.DriveFolder = colabOneDriveFolderName + "/" + colabOneDriveWorkspaceSubdir + "/" + colabOneDriveComfyOutputsSubdir
					rec.DrivePath = "My Drive/" + rec.DriveFolder + "/" + asset.Name
					if link := sanitizeDriveWebViewLink(asset.WebViewLink); link != "" {
						rec.DriveWebViewLink = link
					}
					rec.DriveMode = "drive_api"
					rec.SyncedAt = now
					discovered[id] = rec
				}
			}
		}

		// 2) Inspect outputs/ readable exports
		outFolderID, err := c.driveFindChildFolder(ctx, token, quotaProject, rootFolderID, colabOneDriveOutputsSubdir)
		if err != nil {
			return finishErr(err)
		}
		if outFolderID != "" {
			outFiles, err := c.driveListChildFiles(ctx, token, quotaProject, outFolderID)
			if err != nil {
				return finishErr(err)
			}
			bySHA := make(map[string]*ColabOneDriveOutputRecord, len(discovered))
			byNameSize := make(map[string]*ColabOneDriveOutputRecord, len(discovered))
			byPrevExportID := make(map[string]*ColabOneDriveOutputRecord, len(discovered))
			for id, rec := range discovered {
				if rec.SHA256 != "" {
					bySHA[rec.SHA256] = rec
				}
				byNameSize[strings.ToLower(rec.Filename)+"|"+strconv.FormatInt(rec.Size, 10)] = rec
				if prev := existingSnap[id]; prev != nil {
					if pfid := sanitizeDriveFileID(prev.DriveFileID); pfid != "" {
						byPrevExportID[pfid] = rec
					}
				}
			}
			// Also index existing exported records that may not have a workspace copy
			// so re-syncing outputs/ preserves their original output ID, CU, elapsed,
			// workflow name, and creation time without creating duplicate cards.
			prevByExportFileID := make(map[string]*ColabOneDriveOutputRecord, len(existingSnap))
			prevExportedBySHA := make(map[string]*ColabOneDriveOutputRecord, len(existingSnap))
			prevExportedByNameSize := make(map[string]*ColabOneDriveOutputRecord, len(existingSnap))
			for _, prev := range existingSnap {
				if prev == nil {
					continue
				}
				if pfid := sanitizeDriveFileID(prev.DriveFileID); pfid != "" {
					prevByExportFileID[pfid] = prev
				}
				if prev.DriveExported {
					if psha := sanitizeDriveSHA256(prev.SHA256); psha != "" {
						prevExportedBySHA[psha] = prev
					}
					if sanitizeDriveFileID(prev.DriveFileID) == "" && prev.Filename != "" && prev.Size > 0 {
						prevExportedByNameSize[strings.ToLower(prev.Filename)+"|"+strconv.FormatInt(prev.Size, 10)] = prev
					}
				}
			}
			for _, f := range outFiles {
				name := strings.TrimSpace(f.Name)
				if name == "" || strings.HasPrefix(name, ".") {
					continue
				}
				ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
				km, ok := allowedDriveOutputKindMIME[ext]
				if !ok {
					continue
				}
				sz := f.sizeBytes()
				if sz <= 0 {
					continue
				}
				fid := sanitizeDriveFileID(f.ID)
				if fid == "" {
					continue
				}
				sha := sanitizeDriveSHA256(f.SHA256Checksum)
				nameSizeKey := strings.ToLower(name) + "|" + strconv.FormatInt(sz, 10)
				var matched *ColabOneDriveOutputRecord
				if m := byPrevExportID[fid]; m != nil {
					matched = m
				}
				if matched == nil && sha != "" {
					matched = bySHA[sha]
				}
				if matched == nil {
					matched = byNameSize[nameSizeKey]
				}
				if matched != nil {
					matched.DriveExported = true
					matched.DriveFileID = fid
					matched.DriveFolder = colabOneDriveFolderName + "/" + colabOneDriveOutputsSubdir
					matched.DrivePath = "My Drive/" + matched.DriveFolder + "/" + name
					if link := sanitizeDriveWebViewLink(f.WebViewLink); link != "" {
						matched.DriveWebViewLink = link
					}
					matched.SyncedAt = now
					continue
				}
				synthID := syntheticDriveOutputID(fid)
				prev := existingSnap[synthID]
				if prev == nil {
					prev = prevByExportFileID[fid]
				}
				if prev == nil && sha != "" {
					prev = prevExportedBySHA[sha]
				}
				if prev == nil {
					prev = prevExportedByNameSize[nameSizeKey]
				}
				if prev != nil && discovered[prev.ID] != nil {
					prev = nil
				}
				recID := synthID
				if prev != nil && reHex32ID.MatchString(prev.ID) {
					recID = prev.ID
				}
				rec := &ColabOneDriveOutputRecord{
					ID:               recID,
					WorkflowID:       "",
					WorkflowName:     "Google 云盘作品",
					Kind:             km[0],
					Filename:         name,
					Extension:        ext,
					Mime:             km[1],
					Size:             sz,
					SHA256:           sha,
					CreatedAt:        f.createdOrModifiedUnix(),
					Elapsed:          0,
					DriveSynced:      true,
					DriveExported:    true,
					DriveFolder:      colabOneDriveFolderName + "/" + colabOneDriveOutputsSubdir,
					DrivePath:        "My Drive/" + colabOneDriveFolderName + "/" + colabOneDriveOutputsSubdir + "/" + name,
					DriveFileID:      fid,
					DriveWebViewLink: sanitizeDriveWebViewLink(f.WebViewLink),
					DriveMode:        "drive_api",
					SyncedAt:         now,
				}
				if prev != nil {
					if prev.WorkflowName != "" && prev.WorkflowName != "Google 云盘作品" {
						rec.WorkflowName = prev.WorkflowName
					}
					if prev.WorkflowID != "" {
						rec.WorkflowID = prev.WorkflowID
					}
					if prev.CreatedAt > 0 {
						rec.CreatedAt = prev.CreatedAt
					}
					if prev.Elapsed > 0 {
						rec.Elapsed = prev.Elapsed
					}
					if prev.CUConsumed > 0 {
						rec.CUConsumed = prev.CUConsumed
						rec.CUEstimated = prev.CUEstimated
					}
					if prev.CURate > 0 {
						rec.CURate = prev.CURate
					}
					if prev.GPUName != "" {
						rec.GPUName = prev.GPUName
					}
					if rec.SHA256 == "" && prev.SHA256 != "" {
						rec.SHA256 = sanitizeDriveSHA256(prev.SHA256)
					}
					if prev.TaskID != "" {
						rec.TaskID = sanitizeDriveTaskID(prev.TaskID)
					}
				}
				if rec.CUConsumed <= 0 && rec.Elapsed > 0 && rec.CreatedAt > 0 {
					if cu, rate, gn, est, ok := c.resolveOutputCU(bctx, rec.CreatedAt, rec.Elapsed, false); ok {
						rec.CUConsumed = cu
						rec.CURate = rate
						if gn != "" {
							rec.GPUName = gn
						}
						rec.CUEstimated = est
					}
				}
				discovered[recID] = rec
			}
		}
	}

	c.mu.Lock()
	if c.currentDriveAccountKeyLocked() != acctKey {
		c.clearDriveOutputsSyncTrackingLocked(acctKey)
		c.mu.Unlock()
		return map[string]any{"synced": false, "can_direct_read": true, "count": 0}, nil
	}
	if c.driveOutputs == nil {
		c.driveOutputs = make(map[string]map[string]*ColabOneDriveOutputRecord)
	}
	deletedIDs := c.driveOutputsSyncDeleted[acctKey]
	deletedFileIDs := c.driveOutputsSyncDeletedFileIDs[acctKey]
	addedIDs := c.driveOutputsSyncAdded[acctKey]

	nextBucket := make(map[string]*ColabOneDriveOutputRecord, len(discovered))
	for id, rec := range discovered {
		if rec == nil || deletedIDs[id] {
			continue
		}
		if fid := sanitizeDriveFileID(rec.DriveFileID); fid != "" && deletedFileIDs[fid] {
			continue
		}
		if wfid := sanitizeDriveFileID(rec.DriveWorkspaceFileID); wfid != "" && deletedFileIDs[wfid] {
			continue
		}
		nextBucket[id] = rec
	}
	// Merge concurrent additions/updates made while sync was in flight and preserve
	// colab_drivefs records that drive.file scope cannot list (unless deleted).
	if prevBucket := c.driveOutputs[acctKey]; prevBucket != nil {
		for id, prev := range prevBucket {
			if prev == nil || deletedIDs[id] {
				continue
			}
			if addedIDs[id] {
				if existing, exists := nextBucket[id]; exists && existing != nil {
					mergeDriveOutputRecord(existing, prev.clone())
				} else {
					nextBucket[id] = prev.clone()
				}
				continue
			}
			if _, exists := nextBucket[id]; exists {
				continue
			}
			if prev.DriveMode == "colab_drivefs" {
				nextBucket[id] = prev.clone()
			}
		}
	}
	pruneDriveOutputsBucketLocked(nextBucket)
	c.driveOutputs[acctKey] = nextBucket
	c.clearDriveOutputsSyncTrackingLocked(acctKey)
	c.driveOutputsLastSynced = now
	c.driveOutputsSyncError = ""
	count := len(nextBucket)
	c.mu.Unlock()

	_ = c.saveDriveOutputs()
	return map[string]any{
		"synced":          true,
		"can_direct_read": true,
		"count":           count,
	}, nil
}

func (c *ColabOneController) readDriveOutputAssetChunk(ctx context.Context, identity string, offset int) (map[string]any, error) {
	cleanID := strings.ToLower(strings.TrimSpace(identity))
	if !reHex32ID.MatchString(cleanID) {
		return nil, errors.New("产物编号不合法")
	}
	c.mu.Lock()
	acctKey := c.currentDriveAccountKeyLocked()
	var rec *ColabOneDriveOutputRecord
	lastSynced := c.driveOutputsLastSynced
	if acctKey != "" && c.driveOutputs != nil {
		if bucket := c.driveOutputs[acctKey]; bucket != nil {
			rec = bucket[cleanID].clone()
		}
	}
	c.mu.Unlock()
	if rec == nil {
		return nil, ErrNotFound
	}
	canDirectRead, quotaProject := c.driveDirectReadReady()
	fileID := rec.preferredDriveFileID()
	needOnDemandSync := canDirectRead &&
		fileID == "" &&
		rec.DriveMode != "colab_drivefs" &&
		(lastSynced <= 0 || nowUnixFloat()-lastSynced >= colabOneMediaOnDemandSyncCooldownSec)
	if needOnDemandSync {
		if _, syncErr := c.SyncDriveOutputs(ctx); syncErr == nil {
			c.mu.Lock()
			if curAcct := c.currentDriveAccountKeyLocked(); curAcct != "" && c.driveOutputs != nil {
				if bucket := c.driveOutputs[curAcct]; bucket != nil && bucket[cleanID] != nil {
					rec = bucket[cleanID].clone()
				}
			}
			c.mu.Unlock()
			fileID = rec.preferredDriveFileID()
		}
	}
	if !canDirectRead || fileID == "" {
		return nil, errors.New("该作品已保存至 Google 云盘，请点击「在 Google 云盘查看」或开启无感云盘 API 后直接预览")
	}
	size := rec.Size
	if offset < 0 || (size > 0 && int64(offset) >= size) || (size == 0 && offset > 0) {
		return nil, errors.New("作品读取范围不合法")
	}
	token, err := c.driveAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	end := int64(offset) + colabOneDriveAssetChunkBytes - 1
	if size > 0 && end >= size {
		end = size - 1
	}
	reqURL := c.driveAPIBase() + "/files/" + url.PathEscape(fileID) + "?alt=media"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if quotaProject != "" {
		req.Header.Set("X-Goog-User-Project", quotaProject)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, end))

	resp, err := c.driveClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		return nil, errors.New("作品读取范围不合法")
	}
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return nil, &ColabOneHTTPError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("从 Google 云盘读取作品失败（HTTP %d）", resp.StatusCode)}
	}

	var chunk []byte
	resolvedAuthoritativeSize := int64(0)
	if resp.StatusCode == http.StatusPartialContent {
		if total := parseContentRangeTotal(resp.Header.Get("Content-Range")); total > 0 {
			resolvedAuthoritativeSize = total
			if size <= 0 {
				size = total
			}
		}
		chunk, err = io.ReadAll(io.LimitReader(resp.Body, colabOneDriveAssetChunkBytes))
		if err != nil {
			return nil, err
		}
	} else {
		// Server ignored Range and returned 200 OK: skip offset bytes before reading chunk.
		if resp.ContentLength > 0 {
			resolvedAuthoritativeSize = resp.ContentLength
			if size <= 0 {
				size = resp.ContentLength
			}
		}
		if offset > 0 {
			if _, skipErr := io.CopyN(io.Discard, resp.Body, int64(offset)); skipErr != nil {
				if errors.Is(skipErr, io.EOF) || errors.Is(skipErr, io.ErrUnexpectedEOF) {
					return nil, errors.New("作品读取范围不合法")
				}
				return nil, skipErr
			}
		}
		chunk, err = io.ReadAll(io.LimitReader(resp.Body, colabOneDriveAssetChunkBytes))
		if err != nil {
			return nil, err
		}
	}
	if size <= 0 {
		size = int64(offset) + int64(len(chunk))
	}
	if resolvedAuthoritativeSize > 0 && rec.Size <= 0 {
		persistedSize := false
		c.mu.Lock()
		if curAcct := c.currentDriveAccountKeyLocked(); curAcct != "" && c.driveOutputs != nil {
			if bucket := c.driveOutputs[curAcct]; bucket != nil {
				if liveEntry := bucket[cleanID]; liveEntry != nil && liveEntry.Size <= 0 {
					liveEntry.Size = resolvedAuthoritativeSize
					persistedSize = true
				}
			}
		}
		c.mu.Unlock()
		if persistedSize {
			_ = c.saveDriveOutputs()
		}
	}
	canonicalMIME := rec.Mime
	if km, ok := allowedDriveOutputKindMIME[strings.ToLower(strings.TrimPrefix(strings.TrimSpace(rec.Extension), "."))]; ok {
		canonicalMIME = km[1]
	}
	out := map[string]any{
		"data":      base64.StdEncoding.EncodeToString(chunk),
		"offset":    offset,
		"size":      size,
		"mime":      canonicalMIME,
		"extension": rec.Extension,
		"filename":  rec.Filename,
		"source":    "drive",
	}
	if sha := sanitizeDriveSHA256(rec.SHA256); sha != "" {
		out["sha256"] = sha
	}
	return out, nil
}

func (c *ColabOneController) driveOutputRecordCopy(identity string) *ColabOneDriveOutputRecord {
	cleanID := strings.ToLower(strings.TrimSpace(identity))
	if !reHex32ID.MatchString(cleanID) {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	acctKey := c.currentDriveAccountKeyLocked()
	if acctKey == "" || c.driveOutputs == nil {
		return nil
	}
	if bucket := c.driveOutputs[acctKey]; bucket != nil {
		return bucket[cleanID].clone()
	}
	return nil
}

func (c *ColabOneController) clearDriveOutputWorkspaceLinks(identity string) {
	cleanID := strings.ToLower(strings.TrimSpace(identity))
	if !reHex32ID.MatchString(cleanID) {
		return
	}
	changed := false
	c.mu.Lock()
	acctKey := c.currentDriveAccountKeyLocked()
	if acctKey != "" && c.driveOutputs != nil {
		if bucket := c.driveOutputs[acctKey]; bucket != nil {
			if rec := bucket[cleanID]; rec != nil {
				if rec.DriveWorkspaceFileID != "" || rec.DriveWorkspaceMetaID != "" {
					rec.DriveWorkspaceFileID = ""
					rec.DriveWorkspaceMetaID = ""
					changed = true
				}
			}
		}
	}
	c.mu.Unlock()
	if changed {
		_ = c.saveDriveOutputs()
	}
}

func (c *ColabOneController) trashDriveFileIDs(ctx context.Context, rawIDs ...string) (bool, error) {
	ids := make([]string, 0, len(rawIDs))
	seen := make(map[string]bool, len(rawIDs))
	for _, fid := range rawIDs {
		fid = sanitizeDriveFileID(fid)
		if fid != "" && !seen[fid] {
			seen[fid] = true
			ids = append(ids, fid)
		}
	}
	if len(ids) == 0 {
		return true, nil
	}
	canDirectRead, quotaProject := c.driveDirectReadReady()
	if !canDirectRead {
		return false, nil
	}
	token, err := c.driveAccessToken(ctx)
	if err != nil {
		return false, err
	}
	for _, fid := range ids {
		u := c.driveAPIBase() + "/files/" + url.PathEscape(fid)
		status, _, reqErr := c.doDriveAPIRequest(
			ctx,
			http.MethodPatch,
			u,
			token,
			quotaProject,
			strings.NewReader(`{"trashed":true}`),
			map[string]string{"Content-Type": "application/json"},
			64*1024,
		)
		if reqErr != nil {
			return false, reqErr
		}
		if status != http.StatusOK && status != http.StatusNoContent && status != http.StatusNotFound {
			return false, &ColabOneHTTPError{
				StatusCode: status,
				Message:    fmt.Sprintf("删除 Google 云盘作品失败（HTTP %d）", status),
			}
		}
	}
	return true, nil
}

func (c *ColabOneController) deleteDriveOutputOffline(ctx context.Context, identity string) (map[string]any, error) {
	cleanID := strings.ToLower(strings.TrimSpace(identity))
	if !reHex32ID.MatchString(cleanID) {
		return nil, errors.New("产物编号不合法")
	}
	rec := c.driveOutputRecordCopy(cleanID)
	if rec == nil {
		return nil, ErrNotFound
	}
	if _, err := c.trashDriveFileIDs(ctx, rec.DriveFileID, rec.DriveWorkspaceFileID, rec.DriveWorkspaceMetaID); err != nil {
		return nil, err
	}
	c.removeDriveOutputRecord(cleanID)
	return map[string]any{"ok": true}, nil
}

func (c *ColabOneController) hasLiveRemoteOutput(identity string) bool {
	cleanID := strings.ToLower(strings.TrimSpace(identity))
	if !reHex32ID.MatchString(cleanID) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, rawItem := range asAnySlice(c.data["comfy_outputs"]) {
		if strings.EqualFold(asString(asMap(rawItem)["id"]), cleanID) {
			return true
		}
	}
	return false
}

func (c *ColabOneController) hasDriveOutputRecord(identity string) bool {
	cleanID := strings.ToLower(strings.TrimSpace(identity))
	if !reHex32ID.MatchString(cleanID) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	acctKey := c.currentDriveAccountKeyLocked()
	if acctKey == "" || c.driveOutputs == nil {
		return false
	}
	bucket := c.driveOutputs[acctKey]
	return bucket != nil && bucket[cleanID] != nil
}

func (c *ColabOneController) triggerBackgroundDriveOutputsSync() {
	if c.isShuttingDown(c.rootCtx) {
		return
	}
	if !c.background && c.driveAPIBaseURL == "" {
		return
	}
	canDirectRead, _ := c.driveDirectReadReady()
	if !canDirectRead || c.currentDriveAccountKey() == "" {
		return
	}
	c.spawn(func() {
		ctx, cancel := context.WithTimeout(c.rootCtx, 25*time.Second)
		defer cancel()
		_, _ = c.SyncDriveOutputs(ctx)
	})
}
