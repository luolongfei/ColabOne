package webconsole

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/luolongfei/ColabOne/console/internal/fsutil"
)

const (
	// WorkflowShareCookieName is the HttpOnly session cookie issued to guest workflow share clients.
	WorkflowShareCookieName = "colabone_wf_session"
	// WorkflowShareClientHeaderValue is the expected X-ColabOne-Client header value on share API calls.
	WorkflowShareClientHeaderValue = "web-share"
	// WorkflowShareRoutePath is the HTTP route path for guest workflow sharing.
	WorkflowShareRoutePath = "/share"
	// VaultWorkflowShareHMACKey is the vault entry key storing the 32-byte HMAC master key for workflow share links.
	VaultWorkflowShareHMACKey = "web_workflow_share_hmac_key"
	// VaultWorkflowShareSessionsKey is the vault entry key storing persisted guest workflow share sessions.
	VaultWorkflowShareSessionsKey = "web_workflow_share_sessions"
	// WorkflowSharesFilename is the JSON file persisted in ConfigDir holding share records and ownership maps.
	WorkflowSharesFilename = "workflow-shares.json"

	// WorkflowShareTTLOneTime grants a single-use link that also expires after 24h if never activated.
	WorkflowShareTTLOneTime = "once"
	// WorkflowShareTTL24h grants a share link valid for 24 hours (default).
	WorkflowShareTTL24h = "24h"
	// WorkflowShareTTL7d grants a share link valid for 7 days.
	WorkflowShareTTL7d = "7d"
	// WorkflowShareTTLForever grants a share link valid until manually revoked.
	WorkflowShareTTLForever = "forever"

	// BuiltinDigitalHumanGroupKey expands to both built-in digital human video workflows.
	BuiltinDigitalHumanGroupKey = "colabone-digital-human-group"
)

const (
	workflowShareKeyPrefix             = "co_wf_"
	workflowShareHMACContext           = "colabone:wf_share:v1:"
	workflowShareSwitchRoutePath       = "/share/switch"
	workflowShareSwitchHMACContext     = "colabone:wf_share_switch:v1:"
	workflowShareSwitchTokenTTL        = 5 * time.Minute
	workflowShareCrockfordAlphabet     = "0123456789abcdefghjkmnpqrstvwxyz"
	workflowShareIDLen                 = 12
	workflowShareNonceBytes            = 16
	workflowShareTagBytes              = 16
	workflowShareHMACKeyBytes          = 32
	workflowShareFileVersion           = 1
	workflowShareSessionsVersion       = 1
	maxWorkflowShareLabelRunes         = 80
	maxWorkflowShareTitleRunes         = 80
	maxWorkflowShareWorkflowIDs        = 64
	maxWorkflowShareRecords            = 256
	maxWorkflowShareSessions           = 256
	maxWorkflowShareSessionsPerShare   = 32
	maxWorkflowShareInputsTracked      = 2048
	maxWorkflowShareInputsPerShare     = 256
	maxWorkflowShareTasksTracked       = 256
	maxWorkflowSharePreviewsPerShare   = 128
	maxWorkflowShareOutputsTracked     = 4096
	maxWorkflowShareUploadBodyBytes    = int64(96 << 20)
	maxWorkflowShareConcurrentUploads  = 2
	maxGuestOverrideStringRunes        = 200000
	workflowShareTaskRetention         = 2 * time.Minute
	workflowShareRateLimitMaxPerIP     = 5
	workflowShareRateLimitMaxGlobal    = 25
	workflowShareRateLimitWindow       = 5 * time.Minute
)

var (
	workflowShareIDRe          = regexp.MustCompile(`^[0-9a-hjkmnp-tv-z]{12}$`)
	workflowShareWorkflowIDRe  = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)
	workflowShareVoicePresetRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	guestHostUnixPathRe        = regexp.MustCompile(`(?:^|[\s"'(\[:=])/(?:content|home|Users|tmp|var|etc|root|opt|mnt|proc|sys|Volumes)/`)
	guestHostWinPathRe         = regexp.MustCompile(`(?i)(?:^|[\s"'(\[:=])[a-z]:\\`)
	guestSensitiveTokenRe      = regexp.MustCompile(`(?i)\b(?:hf_[a-z0-9]{8,}|ya29\.[a-z0-9._-]{8,}|co_wf_[a-z0-9._-]{8,}|co_gal_[a-z0-9._-]{8,}|co_web_[a-z0-9._-]{8,}|sk-[a-z0-9_-]{12,}|bearer\s+[a-z0-9._~+/-]{8,})`)
	builtinDigitalHumanIDs     = []string{"digital-human-infinitetalk", "digital-human-longcat"}
	guestAllowedTtsPreviewFields = []string{
		"preview_id",
		"cached",
		"engine_id",
		"voice_preset",
		"text",
		"sample_rate",
		"duration_seconds",
		"mime",
		"size",
		"sha256",
		"data",
	}
	guestAllowedOutputFields   = []string{
		"id",
		"workflow_id",
		"workflow_name",
		"kind",
		"filename",
		"extension",
		"mime",
		"size",
		"sha256",
		"created_at",
		"elapsed",
		"drive_exported",
	}
	guestAllowedComfyUIFields = []string{
		"installed",
		"running",
		"state",
		"tag",
		"cached_tts_engines",
		"queue_running",
		"queue_pending",
		"error",
	}
	guestAllowedTaskFields = []string{
		"id",
		"kind",
		"model_id",
		"state",
		"progress",
		"message",
		"error",
		"created_at",
		"finished_at",
		"elapsed_seconds",
		"download",
	}
)

// ColabOneWorkflowShareCreateRequest configures a new guest workflow share link.
type ColabOneWorkflowShareCreateRequest struct {
	Label       string   `json:"label"`
	Title       string   `json:"title"`
	WorkflowIDs []string `json:"workflow_ids"`
	TTLMode     string   `json:"ttl_mode"`
}

// ColabOneWorkflowShareSummary describes a workflow share entry for the host desktop UI.
type ColabOneWorkflowShareSummary struct {
	ID             string   `json:"id"`
	Label          string   `json:"label"`
	Title          string   `json:"title"`
	WorkflowIDs    []string `json:"workflow_ids"`
	TTLMode        string   `json:"ttl_mode"`
	SingleUse      bool     `json:"single_use"`
	Status         string   `json:"status"`
	CreatedAt      int64    `json:"created_at"`
	ExpiresAt      int64    `json:"expires_at"`
	RevokedAt      int64    `json:"revoked_at,omitempty"`
	ConsumedAt     int64    `json:"consumed_at,omitempty"`
	ActiveSessions int      `json:"active_sessions"`
	OutputCount    int      `json:"output_count"`
	URL            string   `json:"url,omitempty"`
	LocalURL       string   `json:"local_url,omitempty"`
	UsesPublicHost bool     `json:"uses_public_host"`
	CanCopyLink    bool     `json:"can_copy_link"`
}

// ColabOneWorkflowShareListState aggregates all workflow shares and active guest session metrics.
type ColabOneWorkflowShareListState struct {
	Shares              []ColabOneWorkflowShareSummary `json:"shares"`
	ActiveCount         int                            `json:"active_count"`
	TotalActiveSessions int                            `json:"total_active_sessions"`
}

// ColabOneWorkflowShareCapabilities advertises guest-mode capabilities on /api/share/capabilities.
type ColabOneWorkflowShareCapabilities struct {
	Runtime              string   `json:"runtime"`
	Version              string   `json:"version"`
	ShareID              string   `json:"share_id"`
	Title                string   `json:"title"`
	AllowedWorkflowIDs   []string `json:"allowed_workflow_ids"`
	CanDownloadOutput    bool     `json:"can_download_output"`
	CanDeleteOutput      bool     `json:"can_delete_output"`
	CanExportToHostDrive bool     `json:"can_export_to_host_drive"`
}

type colabOneWorkflowShareRecord struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Title       string   `json:"title"`
	WorkflowIDs []string `json:"workflow_ids"`
	TTLMode     string   `json:"ttl_mode"`
	SingleUse   bool     `json:"single_use"`
	NonceB64    string   `json:"nonce"`
	CreatedAt   int64    `json:"created_at"`
	ExpiresAt   int64    `json:"expires_at"`
	RevokedAt   int64    `json:"revoked_at,omitempty"`
	ConsumedAt  int64    `json:"consumed_at,omitempty"`
}

type colabOneShareOutputOwner struct {
	ShareID    string `json:"share_id"`
	ShareLabel string `json:"share_label"`
	CreatedAt  int64  `json:"created_at"`
}

type colabOneShareTrackedTask struct {
	TaskID               string   `json:"task_id"`
	ShareID              string   `json:"share_id"`
	WorkflowID           string   `json:"workflow_id,omitempty"`
	HostRuntimeID        string   `json:"host_runtime_id,omitempty"`
	StartedAt            int64    `json:"started_at"`
	FinishedAt           int64    `json:"finished_at,omitempty"`
	RemoteCreatedAt      float64  `json:"remote_created_at,omitempty"`
	RemoteFinishedAt     float64  `json:"remote_finished_at,omitempty"`
	FallbackClosed       bool     `json:"fallback_closed,omitempty"`
	PreExistingOutputIDs []string `json:"pre_existing_output_ids,omitempty"`
}

type colabOneShareInputOwner struct {
	shareID   string
	touchedAt time.Time
}

type colabOneWorkflowShareSession struct {
	shareID           string
	createdAt         time.Time
	lastSeen          time.Time
	expiresAt         time.Time
	cookieRefreshedAt time.Time
}

type colabOnePersistedShareSession struct {
	DigestHex string `json:"d"`
	ShareID   string `json:"sid"`
	CreatedAt int64  `json:"c"`
	LastSeen  int64  `json:"l"`
	ExpiresAt int64  `json:"e"`
}

type colabOnePersistedShareSessionStore struct {
	Version  int                             `json:"v"`
	HMACFp   string                          `json:"h"`
	Sessions []colabOnePersistedShareSession `json:"s"`
}

type colabOneWorkflowSharesFile struct {
	Version      int                                 `json:"version"`
	Shares       []colabOneWorkflowShareRecord       `json:"shares"`
	OutputOwners map[string]colabOneShareOutputOwner `json:"output_owners,omitempty"`
	TrackedTasks map[string]colabOneShareTrackedTask `json:"tracked_tasks,omitempty"`
}

// --- Workflow Share Lifecycle & Persistence ---

func (w *ColabOneWebConsole) workflowSharesPath() string {
	dir := strings.TrimSpace(w.opts.ConfigDir)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, WorkflowSharesFilename)
}

func (w *ColabOneWebConsole) emitWorkflowSharesChange(state ColabOneWorkflowShareListState) {
	if w.opts.OnWorkflowSharesChange == nil {
		return
	}
	w.mu.RLock()
	closed := w.closed
	w.mu.RUnlock()
	if closed {
		return
	}
	w.opts.OnWorkflowSharesChange(state)
}

func (w *ColabOneWebConsole) hasWorkflowShares() bool {
	w.mu.RLock()
	has := len(w.workflowShares) > 0
	w.mu.RUnlock()
	return has
}

func hashWorkflowShareHMACFingerprint(hmacKey []byte) string {
	if len(hmacKey) == 0 {
		return ""
	}
	h := sha256.New()
	h.Write([]byte("colabone:wf_share_session_bind:v1:"))
	h.Write(hmacKey)
	return hex.EncodeToString(h.Sum(nil))
}

func (w *ColabOneWebConsole) loadWorkflowShareHMACKey() {
	if w.opts.Vault == nil {
		return
	}
	var encoded string
	found, err := w.opts.Vault.GetJSON(VaultWorkflowShareHMACKey, &encoded)
	if err != nil || !found || strings.TrimSpace(encoded) == "" {
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(raw) != workflowShareHMACKeyBytes {
		return
	}
	w.mu.Lock()
	if !w.closed && len(w.shareHMACKey) == 0 {
		w.shareHMACKey = raw
	}
	w.mu.Unlock()
}

func (w *ColabOneWebConsole) ensureWorkflowShareHMACKeyOpLocked() ([]byte, error) {
	w.mu.RLock()
	if w.closed {
		w.mu.RUnlock()
		return nil, errWebConsoleClosed
	}
	existing := append([]byte(nil), w.shareHMACKey...)
	w.mu.RUnlock()
	if len(existing) == workflowShareHMACKeyBytes {
		return existing, nil
	}

	w.sharePersistMu.Lock()
	defer w.sharePersistMu.Unlock()

	w.mu.RLock()
	if w.closed {
		w.mu.RUnlock()
		return nil, errWebConsoleClosed
	}
	existing = append([]byte(nil), w.shareHMACKey...)
	w.mu.RUnlock()
	if len(existing) == workflowShareHMACKeyBytes {
		return existing, nil
	}

	if w.opts.Vault != nil {
		var encoded string
		found, err := w.opts.Vault.GetJSON(VaultWorkflowShareHMACKey, &encoded)
		if err != nil {
			return nil, fmt.Errorf("读取工作流分享签名密钥失败：%w", err)
		}
		if found && strings.TrimSpace(encoded) != "" {
			if raw, decErr := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded)); decErr == nil && len(raw) == workflowShareHMACKeyBytes {
				w.mu.Lock()
				if w.closed {
					w.mu.Unlock()
					return nil, errWebConsoleClosed
				}
				w.shareHMACKey = append([]byte(nil), raw...)
				w.mu.Unlock()
				return raw, nil
			}
		}
	}

	key := make([]byte, workflowShareHMACKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("生成工作流分享签名密钥失败：%w", err)
	}
	if w.opts.Vault != nil {
		encoded := base64.RawURLEncoding.EncodeToString(key)
		if err := w.opts.Vault.Set(VaultWorkflowShareHMACKey, encoded); err != nil {
			return nil, fmt.Errorf("保存工作流分享签名密钥失败：%w", err)
		}
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil, errWebConsoleClosed
	}
	w.shareHMACKey = append([]byte(nil), key...)
	w.mu.Unlock()
	return key, nil
}

func (w *ColabOneWebConsole) loadWorkflowShares() {
	filePath := w.workflowSharesPath()
	if filePath == "" {
		return
	}
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return
	}
	var loaded colabOneWorkflowSharesFile
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return
	}
	if loaded.Version != 0 && loaded.Version != workflowShareFileVersion {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	for _, rec := range loaded.Shares {
		nonceB64 := strings.TrimSpace(rec.NonceB64)
		nonceBytes, nonceErr := base64.RawURLEncoding.DecodeString(nonceB64)
		if !workflowShareIDRe.MatchString(rec.ID) || nonceErr != nil || len(nonceBytes) != workflowShareNonceBytes || base64.RawURLEncoding.EncodeToString(nonceBytes) != nonceB64 {
			continue
		}
		rec.NonceB64 = nonceB64
		if normIDs, ok := validateWorkflowShareIDs(rec.WorkflowIDs); ok {
			rec.WorkflowIDs = normIDs
		} else {
			continue
		}
		if normTTL, ok := normalizeWorkflowShareTTLMode(rec.TTLMode); ok {
			rec.TTLMode = normTTL
		} else {
			rec.TTLMode = WorkflowShareTTL24h
		}
		w.workflowShares[rec.ID] = rec
	}
	for outID, owner := range loaded.OutputOwners {
		if outputIDRe.MatchString(outID) && workflowShareIDRe.MatchString(owner.ShareID) {
			w.shareOutputOwners[outID] = owner
		}
	}
	for tid, tt := range loaded.TrackedTasks {
		if strings.TrimSpace(tid) != "" && workflowShareIDRe.MatchString(tt.ShareID) {
			if tt.TaskID == "" {
				tt.TaskID = tid
			}
			w.shareTasks[tid] = tt
		}
	}
}

func (w *ColabOneWebConsole) persistWorkflowShares() error {
	w.sharePersistMu.Lock()
	defer w.sharePersistMu.Unlock()

	w.mu.RLock()
	if w.closed {
		w.mu.RUnlock()
		return errWebConsoleClosed
	}
	filePath := w.workflowSharesPath()
	if filePath == "" {
		w.mu.RUnlock()
		return nil
	}
	shares := make([]colabOneWorkflowShareRecord, 0, len(w.workflowShares))
	for _, rec := range w.workflowShares {
		cp := rec
		cp.WorkflowIDs = append([]string(nil), rec.WorkflowIDs...)
		shares = append(shares, cp)
	}
	owners := make(map[string]colabOneShareOutputOwner, len(w.shareOutputOwners))
	for k, v := range w.shareOutputOwners {
		owners[k] = v
	}
	tasks := make(map[string]colabOneShareTrackedTask, len(w.shareTasks))
	for k, v := range w.shareTasks {
		cp := v
		if len(v.PreExistingOutputIDs) > 0 {
			cp.PreExistingOutputIDs = append([]string(nil), v.PreExistingOutputIDs...)
		}
		tasks[k] = cp
	}
	w.mu.RUnlock()

	sort.Slice(shares, func(i, j int) bool {
		if shares[i].CreatedAt != shares[j].CreatedAt {
			return shares[i].CreatedAt > shares[j].CreatedAt
		}
		return shares[i].ID < shares[j].ID
	})

	payload := colabOneWorkflowSharesFile{
		Version:      workflowShareFileVersion,
		Shares:       shares,
		OutputOwners: owners,
		TrackedTasks: tasks,
	}

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("保存工作流分享配置失败：%w", err)
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("保存工作流分享配置失败：%w", err)
	}
	data = append(data, '\n')

	tmpFile, err := os.CreateTemp(dir, ".workflow-shares-*.tmp")
	if err != nil {
		return fmt.Errorf("保存工作流分享配置失败：%w", err)
	}
	tmpPath := tmpFile.Name()
	closed := false
	renamed := false
	defer func() {
		if !closed {
			_ = tmpFile.Close()
		}
		if !renamed {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmpFile.Write(data); err != nil {
		return fmt.Errorf("保存工作流分享配置失败：%w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("保存工作流分享配置失败：%w", err)
	}
	if err := tmpFile.Close(); err != nil {
		closed = true
		return fmt.Errorf("保存工作流分享配置失败：%w", err)
	}
	closed = true
	if err := fsutil.RenameReplace(tmpPath, filePath); err != nil {
		return fmt.Errorf("保存工作流分享配置失败：%w", err)
	}
	renamed = true
	return nil
}

func (w *ColabOneWebConsole) loadPersistedWorkflowShareSessions() {
	if w.opts.Vault == nil {
		return
	}
	w.mu.RLock()
	hmacKey := append([]byte(nil), w.shareHMACKey...)
	closed := w.closed
	w.mu.RUnlock()
	if closed || len(hmacKey) != workflowShareHMACKeyBytes {
		return
	}

	var store colabOnePersistedShareSessionStore
	found, err := w.opts.Vault.GetJSON(VaultWorkflowShareSessionsKey, &store)
	if err != nil || !found {
		return
	}
	if store.Version != workflowShareSessionsVersion {
		return
	}
	expectedFp := hashWorkflowShareHMACFingerprint(hmacKey)
	if subtle.ConstantTimeCompare([]byte(store.HMACFp), []byte(expectedFp)) != 1 {
		return
	}

	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	for _, rec := range store.Sessions {
		if rec.ExpiresAt <= 0 {
			continue
		}
		expiresAt := time.Unix(rec.ExpiresAt, 0)
		if !now.Before(expiresAt) {
			continue
		}
		shareRec, exists := w.workflowShares[rec.ShareID]
		if !exists || shareRec.RevokedAt > 0 {
			continue
		}
		rawDigest, decErr := hex.DecodeString(strings.TrimSpace(rec.DigestHex))
		if decErr != nil || len(rawDigest) != 32 {
			continue
		}
		var digest [32]byte
		copy(digest[:], rawDigest)

		createdAt := now
		if rec.CreatedAt > 0 {
			createdAt = time.Unix(rec.CreatedAt, 0)
		}
		lastSeen := createdAt
		if rec.LastSeen > 0 {
			lastSeen = time.Unix(rec.LastSeen, 0)
		}
		if !w.ensureShareSessionCapacityLocked(rec.ShareID, now) {
			continue
		}
		w.shareSessions[digest] = colabOneWorkflowShareSession{
			shareID:           rec.ShareID,
			createdAt:         createdAt,
			lastSeen:          lastSeen,
			expiresAt:         expiresAt,
			cookieRefreshedAt: lastSeen,
		}
	}
}

func (w *ColabOneWebConsole) persistWorkflowShareSessions() {
	if w.opts.Vault == nil {
		return
	}
	w.sharePersistMu.Lock()
	defer w.sharePersistMu.Unlock()

	now := w.now()
	w.mu.RLock()
	if w.closed {
		w.mu.RUnlock()
		return
	}
	hmacKey := append([]byte(nil), w.shareHMACKey...)
	entries := make([]colabOnePersistedShareSession, 0, len(w.shareSessions))
	for digest, s := range w.shareSessions {
		if !now.Before(s.expiresAt) {
			continue
		}
		shareRec, exists := w.workflowShares[s.shareID]
		if !exists || shareRec.RevokedAt > 0 {
			continue
		}
		entries = append(entries, colabOnePersistedShareSession{
			DigestHex: hex.EncodeToString(digest[:]),
			ShareID:   s.shareID,
			CreatedAt: s.createdAt.Unix(),
			LastSeen:  s.lastSeen.Unix(),
			ExpiresAt: s.expiresAt.Unix(),
		})
	}
	w.mu.RUnlock()

	if len(hmacKey) != workflowShareHMACKeyBytes || len(entries) == 0 {
		if err := w.opts.Vault.Delete(VaultWorkflowShareSessionsKey); err != nil {
			w.logActivity(fmt.Sprintf("清理工作流分享持久化会话失败：%s", err.Error()), "warn")
		}
		return
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].LastSeen != entries[j].LastSeen {
			return entries[i].LastSeen < entries[j].LastSeen
		}
		return entries[i].DigestHex < entries[j].DigestHex
	})

	store := colabOnePersistedShareSessionStore{
		Version:  workflowShareSessionsVersion,
		HMACFp:   hashWorkflowShareHMACFingerprint(hmacKey),
		Sessions: entries,
	}
	if err := w.opts.Vault.Set(VaultWorkflowShareSessionsKey, store); err != nil {
		w.logActivity(fmt.Sprintf("保存工作流分享持久化会话失败：%s", err.Error()), "warn")
	}
}

// --- Key Generation & Verification ---

func generateWorkflowShareID() (string, error) {
	buf := make([]byte, workflowShareIDLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成分享链接编号失败：%w", err)
	}
	out := make([]byte, workflowShareIDLen)
	for i, b := range buf {
		out[i] = workflowShareCrockfordAlphabet[b&31]
	}
	return string(out), nil
}

func computeWorkflowShareKey(hmacKey []byte, shareID, nonceB64 string) string {
	mac := hmac.New(sha256.New, hmacKey)
	mac.Write([]byte(workflowShareHMACContext))
	mac.Write([]byte(shareID))
	mac.Write([]byte(":"))
	mac.Write([]byte(nonceB64))
	tag := mac.Sum(nil)[:workflowShareTagBytes]
	tagB64 := base64.RawURLEncoding.EncodeToString(tag)
	return workflowShareKeyPrefix + shareID + "." + nonceB64 + "." + tagB64
}

func extractWorkflowShareIDFromKey(rawKey string) (string, bool) {
	if !strings.HasPrefix(rawKey, workflowShareKeyPrefix) {
		return "", false
	}
	body := strings.TrimPrefix(rawKey, workflowShareKeyPrefix)
	dot := strings.IndexByte(body, '.')
	if dot <= 0 {
		return "", false
	}
	sid := body[:dot]
	if !workflowShareIDRe.MatchString(sid) {
		return "", false
	}
	return sid, true
}

func deriveGuestRuntimeID(hmacKey []byte, shareID, hostRuntimeID string) string {
	hostRuntimeID = strings.TrimSpace(hostRuntimeID)
	if hostRuntimeID == "" || len(hmacKey) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, hmacKey)
	mac.Write([]byte("colabone:wf_share_rt:v1:"))
	mac.Write([]byte(shareID))
	mac.Write([]byte(":"))
	mac.Write([]byte(hostRuntimeID))
	return hex.EncodeToString(mac.Sum(nil))[:16]
}

func computeWorkflowShareStatus(rec colabOneWorkflowShareRecord, now time.Time) string {
	if rec.RevokedAt > 0 {
		return "revoked"
	}
	if rec.SingleUse && rec.ConsumedAt > 0 {
		return "used"
	}
	if rec.ExpiresAt > 0 && !now.Before(time.Unix(rec.ExpiresAt, 0)) {
		return "expired"
	}
	return "active"
}

func (w *ColabOneWebConsole) verifyWorkflowShareKeyLocked(rawKey string, now time.Time) (string, string, bool) {
	if len(w.shareHMACKey) != workflowShareHMACKeyBytes {
		return "", workflowShareErrInvalid, false
	}
	if !strings.HasPrefix(rawKey, workflowShareKeyPrefix) {
		return "", workflowShareErrInvalid, false
	}
	body := strings.TrimPrefix(rawKey, workflowShareKeyPrefix)
	parts := strings.Split(body, ".")
	if len(parts) != 3 {
		return "", workflowShareErrInvalid, false
	}
	shareID, nonceB64, tagB64 := parts[0], parts[1], parts[2]
	if !workflowShareIDRe.MatchString(shareID) {
		return "", workflowShareErrInvalid, false
	}
	nonceBytes, err := base64.RawURLEncoding.DecodeString(nonceB64)
	if err != nil || len(nonceBytes) != workflowShareNonceBytes || base64.RawURLEncoding.EncodeToString(nonceBytes) != nonceB64 {
		return "", workflowShareErrInvalid, false
	}
	tagBytes, err := base64.RawURLEncoding.DecodeString(tagB64)
	if err != nil || len(tagBytes) != workflowShareTagBytes || base64.RawURLEncoding.EncodeToString(tagBytes) != tagB64 {
		return "", workflowShareErrInvalid, false
	}

	mac := hmac.New(sha256.New, w.shareHMACKey)
	mac.Write([]byte(workflowShareHMACContext))
	mac.Write([]byte(shareID))
	mac.Write([]byte(":"))
	mac.Write([]byte(nonceB64))
	expectedTag := mac.Sum(nil)[:workflowShareTagBytes]
	if subtle.ConstantTimeCompare(tagBytes, expectedTag) != 1 {
		return "", workflowShareErrInvalid, false
	}

	rec, exists := w.workflowShares[shareID]
	if !exists {
		return "", workflowShareErrInvalid, false
	}
	if subtle.ConstantTimeCompare([]byte(nonceB64), []byte(rec.NonceB64)) != 1 {
		return "", workflowShareErrInvalid, false
	}

	switch computeWorkflowShareStatus(rec, now) {
	case "revoked":
		return shareID, workflowShareErrRevoked, false
	case "used":
		return shareID, workflowShareErrUsed, false
	case "expired":
		return shareID, workflowShareErrExpired, false
	default:
		return shareID, "", true
	}
}

func normalizeWorkflowShareText(raw string, maxRunes int, fieldLabel string) (string, error) {
	if !utf8.ValidString(raw) {
		return "", fmt.Errorf("%s包含无效字符", fieldLabel)
	}
	trimmed := strings.TrimSpace(raw)
	if utf8.RuneCountInString(trimmed) > maxRunes {
		return "", fmt.Errorf("%s长度不能超过 %d 个字符", fieldLabel, maxRunes)
	}
	for _, r := range trimmed {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%s不能包含控制字符", fieldLabel)
		}
	}
	return trimmed, nil
}

func normalizeWorkflowShareTTLMode(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", WorkflowShareTTL24h:
		return WorkflowShareTTL24h, true
	case WorkflowShareTTLOneTime:
		return WorkflowShareTTLOneTime, true
	case WorkflowShareTTL7d:
		return WorkflowShareTTL7d, true
	case WorkflowShareTTLForever:
		return WorkflowShareTTLForever, true
	default:
		return "", false
	}
}

func validateWorkflowShareIDs(ids []string) ([]string, bool) {
	if len(ids) == 0 {
		return nil, false
	}
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, raw := range ids {
		trimmed := strings.TrimSpace(raw)
		if !workflowShareWorkflowIDRe.MatchString(trimmed) {
			return nil, false
		}
		if _, dup := seen[trimmed]; dup {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
		if len(out) > maxWorkflowShareWorkflowIDs {
			return nil, false
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func expandWorkflowShareIDs(entryIDs []string) []string {
	seen := make(map[string]struct{}, len(entryIDs)+2)
	out := make([]string, 0, len(entryIDs)+2)
	appendUnique := func(id string) {
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, id := range entryIDs {
		if id == BuiltinDigitalHumanGroupKey {
			for _, dhID := range builtinDigitalHumanIDs {
				appendUnique(dhID)
			}
			continue
		}
		appendUnique(id)
	}
	return out
}

func allowsDigitalHumanWorkflow(expandedIDs []string) bool {
	for _, id := range expandedIDs {
		for _, dhID := range builtinDigitalHumanIDs {
			if id == dhID {
				return true
			}
		}
	}
	return false
}

func (w *ColabOneWebConsole) buildWorkflowShareSummaryLocked(
	rec colabOneWorkflowShareRecord,
	now time.Time,
	sessionCounts map[string]int,
	outputCounts map[string]int,
) ColabOneWorkflowShareSummary {
	status := computeWorkflowShareStatus(rec, now)
	canCopy := status == "active" && len(w.shareHMACKey) == workflowShareHMACKeyBytes

	var shareURL, localURL string
	usesPublic := false
	if canCopy {
		key := computeWorkflowShareKey(w.shareHMACKey, rec.ID, rec.NonceB64)
		localURL = fmt.Sprintf("http://127.0.0.1:%d%s?k=%s", w.cfg.Port, WorkflowShareRoutePath, key)
		shareURL = localURL
		if w.cfg.PublicOrigin != "" {
			shareURL = fmt.Sprintf("%s%s?k=%s", w.cfg.PublicOrigin, WorkflowShareRoutePath, key)
			usesPublic = true
		}
	}

	wfIDs := append([]string{}, rec.WorkflowIDs...)
	if wfIDs == nil {
		wfIDs = []string{}
	}

	return ColabOneWorkflowShareSummary{
		ID:             rec.ID,
		Label:          rec.Label,
		Title:          rec.Title,
		WorkflowIDs:    wfIDs,
		TTLMode:        rec.TTLMode,
		SingleUse:      rec.SingleUse,
		Status:         status,
		CreatedAt:      rec.CreatedAt,
		ExpiresAt:      rec.ExpiresAt,
		RevokedAt:      rec.RevokedAt,
		ConsumedAt:     rec.ConsumedAt,
		ActiveSessions: sessionCounts[rec.ID],
		OutputCount:    outputCounts[rec.ID],
		URL:            shareURL,
		LocalURL:       localURL,
		UsesPublicHost: usesPublic,
		CanCopyLink:    canCopy,
	}
}

// --- Public Host Management API ---

// WorkflowSharesState returns the live summary list of all workflow shares and active guest sessions.
func (w *ColabOneWebConsole) WorkflowSharesState() ColabOneWorkflowShareListState {
	now := w.now()
	w.mu.RLock()
	defer w.mu.RUnlock()

	sessionCounts := make(map[string]int)
	for _, s := range w.shareSessions {
		if !now.Before(s.expiresAt) {
			continue
		}
		if rec, ok := w.workflowShares[s.shareID]; ok && rec.RevokedAt == 0 {
			sessionCounts[s.shareID]++
		}
	}

	outputCounts := make(map[string]int)
	for _, owner := range w.shareOutputOwners {
		outputCounts[owner.ShareID]++
	}

	summaries := make([]ColabOneWorkflowShareSummary, 0, len(w.workflowShares))
	activeCount := 0
	totalActiveSessions := 0
	for _, rec := range w.workflowShares {
		summary := w.buildWorkflowShareSummaryLocked(rec, now, sessionCounts, outputCounts)
		if summary.Status == "active" {
			activeCount++
		}
		totalActiveSessions += summary.ActiveSessions
		summaries = append(summaries, summary)
	}

	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].CreatedAt != summaries[j].CreatedAt {
			return summaries[i].CreatedAt > summaries[j].CreatedAt
		}
		return summaries[i].ID < summaries[j].ID
	})

	return ColabOneWorkflowShareListState{
		Shares:              summaries,
		ActiveCount:         activeCount,
		TotalActiveSessions: totalActiveSessions,
	}
}

// CreateWorkflowShare creates, signs, persists, and returns a new guest workflow share link.
func (w *ColabOneWebConsole) CreateWorkflowShare(
	req ColabOneWorkflowShareCreateRequest,
) (ColabOneWorkflowShareSummary, error) {
	label, err := normalizeWorkflowShareText(req.Label, maxWorkflowShareLabelRunes, "分享备注")
	if err != nil {
		return ColabOneWorkflowShareSummary{}, err
	}
	title, err := normalizeWorkflowShareText(req.Title, maxWorkflowShareTitleRunes, "页面标题")
	if err != nil {
		return ColabOneWorkflowShareSummary{}, err
	}
	wfIDs, ok := validateWorkflowShareIDs(req.WorkflowIDs)
	if !ok {
		return ColabOneWorkflowShareSummary{}, errors.New("请至少选择一个有效的工作流（最多 64 项）")
	}
	ttlMode, ok := normalizeWorkflowShareTTLMode(req.TTLMode)
	if !ok {
		return ColabOneWorkflowShareSummary{}, errors.New("不支持的分享有效期设置")
	}

	w.workflowShareOpMu.Lock()
	w.mu.RLock()
	closed := w.closed
	existingCount := len(w.workflowShares)
	w.mu.RUnlock()
	if closed {
		w.workflowShareOpMu.Unlock()
		return ColabOneWorkflowShareSummary{}, errWebConsoleClosed
	}
	if existingCount >= maxWorkflowShareRecords {
		w.workflowShareOpMu.Unlock()
		return ColabOneWorkflowShareSummary{}, errors.New("工作流分享链接数量已达上限，请先删除旧记录")
	}

	if _, err := w.ensureWorkflowShareHMACKeyOpLocked(); err != nil {
		w.workflowShareOpMu.Unlock()
		return ColabOneWorkflowShareSummary{}, err
	}

	nonceRaw := make([]byte, workflowShareNonceBytes)
	if _, err := rand.Read(nonceRaw); err != nil {
		w.workflowShareOpMu.Unlock()
		return ColabOneWorkflowShareSummary{}, fmt.Errorf("生成分享链接随机凭证失败：%w", err)
	}
	nonceB64 := base64.RawURLEncoding.EncodeToString(nonceRaw)

	var shareID string
	for attempts := 0; attempts < 8; attempts++ {
		cand, genErr := generateWorkflowShareID()
		if genErr != nil {
			w.workflowShareOpMu.Unlock()
			return ColabOneWorkflowShareSummary{}, genErr
		}
		w.mu.RLock()
		_, exists := w.workflowShares[cand]
		w.mu.RUnlock()
		if !exists {
			shareID = cand
			break
		}
	}
	if shareID == "" {
		w.workflowShareOpMu.Unlock()
		return ColabOneWorkflowShareSummary{}, errors.New("无法分配唯一分享编号，请重试")
	}

	now := w.now()
	singleUse := ttlMode == WorkflowShareTTLOneTime
	var expiresAt int64
	switch ttlMode {
	case WorkflowShareTTLOneTime, WorkflowShareTTL24h:
		expiresAt = now.Add(24 * time.Hour).Unix()
	case WorkflowShareTTL7d:
		expiresAt = now.Add(7 * 24 * time.Hour).Unix()
	case WorkflowShareTTLForever:
		expiresAt = 0
	}

	rec := colabOneWorkflowShareRecord{
		ID:          shareID,
		Label:       label,
		Title:       title,
		WorkflowIDs: wfIDs,
		TTLMode:     ttlMode,
		SingleUse:   singleUse,
		NonceB64:    nonceB64,
		CreatedAt:   now.Unix(),
		ExpiresAt:   expiresAt,
	}

	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		w.workflowShareOpMu.Unlock()
		return ColabOneWorkflowShareSummary{}, errWebConsoleClosed
	}
	w.workflowShares[shareID] = rec
	summary := w.buildWorkflowShareSummaryLocked(rec, now, map[string]int{}, map[string]int{})
	w.mu.Unlock()

	if err := w.persistWorkflowShares(); err != nil {
		w.mu.Lock()
		delete(w.workflowShares, shareID)
		w.mu.Unlock()
		w.workflowShareOpMu.Unlock()
		return ColabOneWorkflowShareSummary{}, err
	}
	w.workflowShareOpMu.Unlock()

	w.logActivity(fmt.Sprintf("已创建工作流分享链接（%d 个工作流，有效期 %s）", len(wfIDs), ttlMode), "info")
	w.emitWorkflowSharesChange(w.WorkflowSharesState())
	return summary, nil
}

// RevokeWorkflowShare manually revokes a share link and immediately invalidates all of its active guest sessions.
func (w *ColabOneWebConsole) RevokeWorkflowShare(shareID string) (ColabOneWorkflowShareListState, error) {
	shareID = strings.TrimSpace(shareID)
	if !workflowShareIDRe.MatchString(shareID) {
		return w.WorkflowSharesState(), errors.New("分享编号无效")
	}

	w.workflowShareOpMu.Lock()
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		w.workflowShareOpMu.Unlock()
		return w.WorkflowSharesState(), errWebConsoleClosed
	}
	rec, exists := w.workflowShares[shareID]
	if !exists {
		w.mu.Unlock()
		w.workflowShareOpMu.Unlock()
		return w.WorkflowSharesState(), errors.New("未找到对应的工作流分享链接")
	}
	now := w.now()
	changed := false
	if rec.RevokedAt == 0 {
		rec.RevokedAt = now.Unix()
		w.workflowShares[shareID] = rec
		changed = true
	}
	purgedSessions := w.purgeShareSessionsForShareLocked(shareID)
	w.mu.Unlock()

	if purgedSessions {
		w.persistWorkflowShareSessions()
	}
	if err := w.persistWorkflowShares(); err != nil {
		w.workflowShareOpMu.Unlock()
		state := w.WorkflowSharesState()
		if changed || purgedSessions {
			w.emitWorkflowSharesChange(state)
		}
		return state, err
	}
	w.workflowShareOpMu.Unlock()

	w.logActivity(fmt.Sprintf("已撤销工作流分享链接 %s 并注销关联访客会话", shareID), "info")
	state := w.WorkflowSharesState()
	w.emitWorkflowSharesChange(state)
	return state, nil
}

// DeleteWorkflowShare permanently removes a workflow share entry and immediately invalidates all of its active guest sessions.
func (w *ColabOneWebConsole) DeleteWorkflowShare(shareID string) (ColabOneWorkflowShareListState, error) {
	shareID = strings.TrimSpace(shareID)
	if !workflowShareIDRe.MatchString(shareID) {
		return w.WorkflowSharesState(), errors.New("分享编号无效")
	}

	w.workflowShareOpMu.Lock()
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		w.workflowShareOpMu.Unlock()
		return w.WorkflowSharesState(), errWebConsoleClosed
	}
	existingRec, exists := w.workflowShares[shareID]
	if !exists {
		w.mu.Unlock()
		w.workflowShareOpMu.Unlock()
		return w.WorkflowSharesState(), errors.New("未找到对应的工作流分享链接")
	}
	backupRec := existingRec
	backupRec.WorkflowIDs = append([]string(nil), existingRec.WorkflowIDs...)
	now := w.now()
	delete(w.workflowShares, shareID)
	purgedSessions := w.purgeShareSessionsForShareLocked(shareID)
	for inputID, owner := range w.shareInputs {
		if owner.shareID == shareID {
			delete(w.shareInputs, inputID)
		}
	}
	for tid, tt := range w.shareTasks {
		if tt.ShareID == shareID {
			delete(w.shareTasks, tid)
		}
	}
	delete(w.sharePreviewOwners, shareID)
	w.mu.Unlock()

	if purgedSessions {
		w.persistWorkflowShareSessions()
	}
	if err := w.persistWorkflowShares(); err != nil {
		// Restore the record in-memory as revoked so active access stays blocked
		// and the user can retry deletion instead of hitting "not found" while disk
		// still retains the un-revoked share.
		w.mu.Lock()
		if !w.closed {
			if backupRec.RevokedAt == 0 {
				backupRec.RevokedAt = now.Unix()
			}
			w.workflowShares[shareID] = backupRec
		}
		w.mu.Unlock()
		w.workflowShareOpMu.Unlock()
		state := w.WorkflowSharesState()
		w.emitWorkflowSharesChange(state)
		return state, err
	}
	w.workflowShareOpMu.Unlock()

	w.logActivity(fmt.Sprintf("已删除工作流分享记录 %s", shareID), "info")
	state := w.WorkflowSharesState()
	w.emitWorkflowSharesChange(state)
	return state, nil
}

func (w *ColabOneWebConsole) purgeShareSessionsForShareLocked(shareID string) bool {
	removed := false
	for digest, s := range w.shareSessions {
		if s.shareID == shareID {
			delete(w.shareSessions, digest)
			removed = true
		}
	}
	return removed
}

// --- Guest Session, Cookie, and Rate-Limiting Helpers ---

func (w *ColabOneWebConsole) setWorkflowShareCookie(rw http.ResponseWriter, r *http.Request, token string, now time.Time) {
	http.SetCookie(rw, &http.Cookie{
		Name:     WorkflowShareCookieName,
		Value:    token,
		Path:     "/",
		Expires:  now.Add(SessionTTL),
		MaxAge:   int(SessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   w.isHTTPSRequest(r),
	})
}

func (w *ColabOneWebConsole) ensureShareSessionCapacityLocked(shareID string, now time.Time) bool {
	w.pruneExpiredShareSessionsLocked(now)
	for countShareSessionsForShareLocked(w.shareSessions, shareID) >= maxWorkflowShareSessionsPerShare {
		if !evictOldestShareSessionForShareLocked(w.shareSessions, shareID) {
			break
		}
	}
	if len(w.shareSessions) >= maxWorkflowShareSessions {
		// Never let one share evict another share's sessions; only rotate within the same share.
		if !evictOldestShareSessionForShareLocked(w.shareSessions, shareID) {
			return false
		}
	}
	return len(w.shareSessions) < maxWorkflowShareSessions
}

func (w *ColabOneWebConsole) storeWorkflowShareSessionLocked(rawToken, shareID string, now time.Time) bool {
	if !w.ensureShareSessionCapacityLocked(shareID, now) {
		return false
	}
	digest := sha256.Sum256([]byte(rawToken))
	w.shareSessions[digest] = colabOneWorkflowShareSession{
		shareID:           shareID,
		createdAt:         now,
		lastSeen:          now,
		expiresAt:         now.Add(SessionTTL),
		cookieRefreshedAt: now,
	}
	return true
}

func (w *ColabOneWebConsole) pruneExpiredShareSessionsLocked(now time.Time) {
	for k, s := range w.shareSessions {
		if !now.Before(s.expiresAt) {
			delete(w.shareSessions, k)
		}
	}
}

func countShareSessionsForShareLocked(sessions map[[32]byte]colabOneWorkflowShareSession, shareID string) int {
	n := 0
	for _, s := range sessions {
		if s.shareID == shareID {
			n++
		}
	}
	return n
}

func evictOldestShareSessionForShareLocked(sessions map[[32]byte]colabOneWorkflowShareSession, shareID string) bool {
	var oldestKey [32]byte
	var oldest colabOneWorkflowShareSession
	first := true
	for k, s := range sessions {
		if s.shareID != shareID {
			continue
		}
		if first || s.lastSeen.Before(oldest.lastSeen) || (s.lastSeen.Equal(oldest.lastSeen) && s.createdAt.Before(oldest.createdAt)) {
			oldestKey = k
			oldest = s
			first = false
		}
	}
	if first {
		return false
	}
	delete(sessions, oldestKey)
	return true
}

func (w *ColabOneWebConsole) authenticateShareRequest(rw http.ResponseWriter, r *http.Request) (string, bool) {
	shareID, _, ok := w.authenticateShareRequestWithDigest(rw, r)
	return shareID, ok
}

func (w *ColabOneWebConsole) authenticateShareRequestWithDigest(rw http.ResponseWriter, r *http.Request) (string, [32]byte, bool) {
	var zeroDigest [32]byte
	c, err := r.Cookie(WorkflowShareCookieName)
	if err != nil || c.Value == "" {
		return "", zeroDigest, false
	}
	digest := sha256.Sum256([]byte(c.Value))
	now := w.now()

	w.mu.Lock()
	s, ok := w.shareSessions[digest]
	if !ok {
		w.mu.Unlock()
		return "", zeroDigest, false
	}
	rec, exists := w.workflowShares[s.shareID]
	if !now.Before(s.expiresAt) || !exists || rec.RevokedAt > 0 {
		delete(w.shareSessions, digest)
		w.mu.Unlock()
		w.persistWorkflowShareSessions()
		w.emitWorkflowSharesChange(w.WorkflowSharesState())
		return "", zeroDigest, false
	}
	s.lastSeen = now
	s.expiresAt = now.Add(SessionTTL)
	needsRefresh := rw != nil && now.Sub(s.cookieRefreshedAt) >= sessionRenewalInterval
	if needsRefresh {
		s.cookieRefreshedAt = now
	}
	w.shareSessions[digest] = s
	shareID := s.shareID
	w.mu.Unlock()

	if needsRefresh {
		w.setWorkflowShareCookie(rw, r, c.Value, now)
		w.persistWorkflowShareSessions()
	}
	return shareID, digest, true
}

func (w *ColabOneWebConsole) checkShareRateLimitLocked(ip string, now time.Time) int {
	cutoff := now.Add(-workflowShareRateLimitWindow)
	maxSecs := 0

	w.globalShareKeyFailures = pruneTimestamps(w.globalShareKeyFailures, cutoff)
	if len(w.globalShareKeyFailures) >= workflowShareRateLimitMaxGlobal {
		wait := w.globalShareKeyFailures[0].Add(workflowShareRateLimitWindow).Sub(now)
		secs := int(math.Ceil(wait.Seconds()))
		if secs < 1 {
			secs = 1
		}
		if secs > maxSecs {
			maxSecs = secs
		}
	}

	times := pruneTimestamps(w.shareKeyFailures[ip], cutoff)
	if len(times) == 0 {
		delete(w.shareKeyFailures, ip)
	} else {
		w.shareKeyFailures[ip] = times
		if len(times) >= workflowShareRateLimitMaxPerIP {
			wait := times[0].Add(workflowShareRateLimitWindow).Sub(now)
			secs := int(math.Ceil(wait.Seconds()))
			if secs < 1 {
				secs = 1
			}
			if secs > maxSecs {
				maxSecs = secs
			}
		}
	}
	return maxSecs
}

func (w *ColabOneWebConsole) recordShareKeyFailureLocked(ip string, now time.Time) {
	cutoff := now.Add(-workflowShareRateLimitWindow)
	w.globalShareKeyFailures = append(pruneTimestamps(w.globalShareKeyFailures, cutoff), now)
	times := pruneTimestamps(w.shareKeyFailures[ip], cutoff)
	times = append(times, now)
	if len(w.shareKeyFailures) >= maxRateLimitIPs {
		for k, v := range w.shareKeyFailures {
			pruned := pruneTimestamps(v, cutoff)
			if len(pruned) == 0 {
				delete(w.shareKeyFailures, k)
			} else {
				w.shareKeyFailures[k] = pruned
			}
		}
		if len(w.shareKeyFailures) >= maxRateLimitIPs {
			var oldestIP string
			var oldestTime time.Time
			first := true
			for k, v := range w.shareKeyFailures {
				last := v[len(v)-1]
				if first || last.Before(oldestTime) {
					oldestIP = k
					oldestTime = last
					first = false
				}
			}
			if !first {
				delete(w.shareKeyFailures, oldestIP)
			}
		}
	}
	w.shareKeyFailures[ip] = times
}

func (w *ColabOneWebConsole) validateSameOriginShareRequest(r *http.Request) bool {
	return w.validateSameOriginWithClientHeader(r, WorkflowShareClientHeaderValue)
}

// --- Ownership Tracking Helpers ---

func (w *ColabOneWebConsole) recordShareInput(shareID, inputID string) {
	if !workflowShareIDRe.MatchString(shareID) || !outputIDRe.MatchString(inputID) {
		return
	}
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	evictOldestInputLocked := func(filterShareID string) bool {
		var oldestID string
		var oldestTime time.Time
		first := true
		for k, v := range w.shareInputs {
			if filterShareID != "" && v.shareID != filterShareID {
				continue
			}
			if first || v.touchedAt.Before(oldestTime) || (v.touchedAt.Equal(oldestTime) && k < oldestID) {
				oldestID = k
				oldestTime = v.touchedAt
				first = false
			}
		}
		if first {
			return false
		}
		delete(w.shareInputs, oldestID)
		return true
	}

	existing, exists := w.shareInputs[inputID]
	if !exists || existing.shareID != shareID {
		perShareCount := 0
		for _, v := range w.shareInputs {
			if v.shareID == shareID {
				perShareCount++
			}
		}
		for perShareCount >= maxWorkflowShareInputsPerShare {
			if !evictOldestInputLocked(shareID) {
				break
			}
			perShareCount--
		}
		for !exists && len(w.shareInputs) >= maxWorkflowShareInputsTracked {
			if !evictOldestInputLocked(shareID) {
				if !evictOldestInputLocked("") {
					break
				}
			}
		}
	}
	w.shareInputs[inputID] = colabOneShareInputOwner{
		shareID:   shareID,
		touchedAt: now,
	}
}

func (w *ColabOneWebConsole) ownsInputForShare(shareID, inputID string) bool {
	if !outputIDRe.MatchString(inputID) {
		return false
	}
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	owner, ok := w.shareInputs[inputID]
	if !ok || owner.shareID != shareID {
		return false
	}
	owner.touchedAt = now
	w.shareInputs[inputID] = owner
	return true
}

func (w *ColabOneWebConsole) recordSharePreview(shareID, previewID string) {
	previewID = strings.TrimSpace(previewID)
	if !workflowShareIDRe.MatchString(shareID) || previewID == "" || len(previewID) > 128 {
		return
	}
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	if w.sharePreviewOwners == nil {
		w.sharePreviewOwners = make(map[string]map[string]time.Time)
	}
	bucket := w.sharePreviewOwners[shareID]
	if bucket == nil {
		bucket = make(map[string]time.Time)
		w.sharePreviewOwners[shareID] = bucket
	}
	if _, exists := bucket[previewID]; !exists && len(bucket) >= maxWorkflowSharePreviewsPerShare {
		var oldestID string
		var oldestTime time.Time
		first := true
		for pid, ts := range bucket {
			if first || ts.Before(oldestTime) || (ts.Equal(oldestTime) && pid < oldestID) {
				oldestID = pid
				oldestTime = ts
				first = false
			}
		}
		if !first {
			delete(bucket, oldestID)
		}
	}
	bucket[previewID] = now
}

func (w *ColabOneWebConsole) ownsPreviewForShare(shareID, previewID string) bool {
	previewID = strings.TrimSpace(previewID)
	if !workflowShareIDRe.MatchString(shareID) || previewID == "" {
		return false
	}
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	bucket := w.sharePreviewOwners[shareID]
	if bucket == nil {
		return false
	}
	if _, ok := bucket[previewID]; !ok {
		return false
	}
	bucket[previewID] = now
	return true
}

func (w *ColabOneWebConsole) pruneShareTasksCapLocked(now time.Time) {
	for tid, tt := range w.shareTasks {
		if tt.FinishedAt > 0 && now.Sub(time.Unix(tt.FinishedAt, 0)) > workflowShareTaskRetention {
			delete(w.shareTasks, tid)
		}
	}
	for len(w.shareTasks) >= maxWorkflowShareTasksTracked {
		var evictID string
		var evictTask colabOneShareTrackedTask
		foundFinished := false
		for tid, tt := range w.shareTasks {
			if tt.FinishedAt <= 0 {
				continue
			}
			if !foundFinished || tt.FinishedAt < evictTask.FinishedAt ||
				(tt.FinishedAt == evictTask.FinishedAt && tt.StartedAt < evictTask.StartedAt) ||
				(tt.FinishedAt == evictTask.FinishedAt && tt.StartedAt == evictTask.StartedAt && tid < evictID) {
				evictID = tid
				evictTask = tt
				foundFinished = true
			}
		}
		if foundFinished {
			delete(w.shareTasks, evictID)
			continue
		}
		first := true
		for tid, tt := range w.shareTasks {
			if first || tt.StartedAt < evictTask.StartedAt || (tt.StartedAt == evictTask.StartedAt && tid < evictID) {
				evictID = tid
				evictTask = tt
				first = false
			}
		}
		if first {
			break
		}
		delete(w.shareTasks, evictID)
	}
}

func (w *ColabOneWebConsole) recordShareTask(
	shareID, taskID, workflowID, hostRuntimeID string,
	preExisting []string,
	remoteCreatedAt float64,
	fallbackClosed bool,
) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || !workflowShareIDRe.MatchString(shareID) {
		return
	}
	wfID := strings.TrimSpace(workflowID)
	now := w.now()
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	if wfID != "" {
		// Starting a newer workflow run closes fallback on any older finished guest workflow task.
		for tid, tt := range w.shareTasks {
			if tid != taskID && tt.WorkflowID != "" && tt.FinishedAt > 0 && !tt.FallbackClosed {
				tt.FallbackClosed = true
				w.shareTasks[tid] = tt
			}
		}
	}
	if _, exists := w.shareTasks[taskID]; !exists {
		w.pruneShareTasksCapLocked(now)
	}
	w.shareTasks[taskID] = colabOneShareTrackedTask{
		TaskID:               taskID,
		ShareID:              shareID,
		WorkflowID:           wfID,
		HostRuntimeID:        strings.TrimSpace(hostRuntimeID),
		StartedAt:            now.Unix(),
		RemoteCreatedAt:      remoteCreatedAt,
		FallbackClosed:       fallbackClosed,
		PreExistingOutputIDs: append([]string(nil), preExisting...),
	}
	w.mu.Unlock()
	_ = w.persistWorkflowShares()
}

// NotifyHostWorkflowRunStarting closes legacy fallback attribution on already-finished guest
// workflow tasks before a host comfyui.run executes, closing the race window where a fast
// host run produces an output before NotifyHostWorkflowRunStarted is invoked.
func (w *ColabOneWebConsole) NotifyHostWorkflowRunStarting() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	changed := false
	for tid, tt := range w.shareTasks {
		if tt.WorkflowID != "" && tt.FinishedAt > 0 && !tt.FallbackClosed {
			tt.FallbackClosed = true
			w.shareTasks[tid] = tt
			changed = true
		}
	}
	w.mu.Unlock()
	if changed {
		_ = w.persistWorkflowShares()
	}
}

// NotifyHostWorkflowRunStarted closes legacy fallback output attribution on prior guest
// workflow tasks so a subsequent host workflow run on an un-upgraded remote server (without
// explicit output task_id) can never be mis-attributed to a guest share.
func (w *ColabOneWebConsole) NotifyHostWorkflowRunStarted() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	changed := false
	for tid, tt := range w.shareTasks {
		if tt.WorkflowID != "" && !tt.FallbackClosed {
			tt.FallbackClosed = true
			w.shareTasks[tid] = tt
			changed = true
		}
	}
	w.mu.Unlock()
	if changed {
		_ = w.persistWorkflowShares()
	}
}

func (w *ColabOneWebConsole) ownsTaskForShare(shareID, taskID string) bool {
	w.mu.RLock()
	tt, ok := w.shareTasks[taskID]
	w.mu.RUnlock()
	return ok && tt.ShareID == shareID
}

func (w *ColabOneWebConsole) ownsOutputForShare(shareID, outputID string) bool {
	w.mu.RLock()
	owner, ok := w.shareOutputOwners[outputID]
	w.mu.RUnlock()
	return ok && owner.ShareID == shareID
}

func (w *ColabOneWebConsole) requireShareLiveOutput(ctx context.Context, shareID, outID string) (int, string) {
	if !outputIDRe.MatchString(outID) {
		return http.StatusBadRequest, "输出文件 ID 无效"
	}
	if !w.ownsOutputForShare(shareID, outID) {
		return http.StatusForbidden, "无权访问该作品文件"
	}
	if w.opts.CallBridge == nil {
		return http.StatusServiceUnavailable, "控制器桥接未初始化"
	}
	snapRaw, err := w.opts.CallBridge(ctx, "snapshot", nil)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return http.StatusGatewayTimeout, sanitizeGuestBridgeError(err.Error())
		}
		return http.StatusBadRequest, "当前算力服务暂未就绪，暂不可访问该作品文件"
	}
	var snap map[string]any
	if json.Unmarshal([]byte(snapRaw), &snap) != nil || snap == nil {
		return http.StatusBadRequest, "当前算力服务暂未就绪，暂不可访问该作品文件"
	}
	if strings.TrimSpace(shareAsString(shareAsMap(snap["runtime"])["state"])) != "ready" {
		return http.StatusBadRequest, "当前算力服务暂未就绪，暂不可访问该作品文件"
	}
	for _, rawOut := range shareAsSlice(snap["comfy_outputs"]) {
		om := shareAsMap(rawOut)
		if om == nil || strings.TrimSpace(shareAsString(om["id"])) != outID {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(shareAsString(om["storage"])), "drive") {
			return http.StatusForbidden, "该作品已归档至宿主云盘，分享模式下不可访问"
		}
		return 0, ""
	}
	return http.StatusNotFound, "作品文件不存在或已清理"
}

// ForgetOutputOwnership removes an output ownership record when the output is deleted.
func (w *ColabOneWebConsole) ForgetOutputOwnership(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	_, exists := w.shareOutputOwners[id]
	if exists {
		delete(w.shareOutputOwners, id)
	}
	w.mu.Unlock()

	if exists {
		_ = w.persistWorkflowShares()
		w.emitWorkflowSharesChange(w.WorkflowSharesState())
	}
}

// EnrichHostSnapshotJSON reconciles guest output attribution and stamps share_id/share_label onto host outputs.
func (w *ColabOneWebConsole) EnrichHostSnapshotJSON(raw string) string {
	return w.ReconcileAndEnrichHostSnapshotJSON(raw)
}

// ReconcileAndEnrichHostSnapshotJSON reconciles guest-submitted tasks against host comfy_outputs
// (supporting both explicit task_id and fallback attribution for older remote servers) and stamps
// share_id and share_label onto attributed outputs in the host snapshot JSON.
func (w *ColabOneWebConsole) ReconcileAndEnrichHostSnapshotJSON(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return raw
	}
	w.mu.RLock()
	hasWork := len(w.shareTasks) > 0 || len(w.shareOutputOwners) > 0
	w.mu.RUnlock()
	if !hasWork {
		return raw
	}

	var snap map[string]any
	if err := json.Unmarshal([]byte(raw), &snap); err != nil || snap == nil {
		return raw
	}

	if !w.reconcileShareOutputsMap(snap, true) {
		return raw
	}

	encoded, err := encodeJSONNoEscape(snap)
	if err != nil {
		return raw
	}
	return string(encoded)
}

func (w *ColabOneWebConsole) reconcileShareOutputsMap(snap map[string]any, stampHostFields bool) bool {
	if snap == nil {
		return false
	}
	w.mu.RLock()
	hasWork := len(w.shareTasks) > 0 || len(w.shareOutputOwners) > 0
	w.mu.RUnlock()
	if !hasWork {
		return false
	}

	now := w.now()
	nowUnix := now.Unix()

	hostRuntime := shareAsMap(snap["runtime"])
	hostState := strings.TrimSpace(shareAsString(hostRuntime["state"]))
	currentHostRuntimeID := strings.TrimSpace(shareAsString(hostRuntime["id"]))

	liveTasksByID := make(map[string]map[string]any)
	if hostRuntime != nil {
		for _, rawTask := range shareAsSlice(hostRuntime["tasks"]) {
			if tm := shareAsMap(rawTask); tm != nil {
				if tid := strings.TrimSpace(shareAsString(tm["id"])); tid != "" {
					liveTasksByID[tid] = tm
				}
			}
		}
	}

	rawOutputs := shareAsSlice(snap["comfy_outputs"])

	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return false
	}

	ownersChanged := false
	tasksChanged := false
	snapshotModified := false

	// 1. Update tracked task remote timestamps & completion state from runtime.tasks.
	for tid, tt := range w.shareTasks {
		if tm, found := liveTasksByID[tid]; found {
			updated := false
			if tt.HostRuntimeID == "" && hostState == "ready" && currentHostRuntimeID != "" {
				tt.HostRuntimeID = currentHostRuntimeID
				updated = true
			}
			if tt.RemoteCreatedAt == 0 {
				if rc := shareAsFloat64(tm["created_at"]); rc > 0 {
					tt.RemoteCreatedAt = rc
					updated = true
				}
			}
			state := strings.ToLower(strings.TrimSpace(shareAsString(tm["state"])))
			if state == "running" {
				if tt.FinishedAt != 0 || tt.RemoteFinishedAt != 0 {
					tt.FinishedAt = 0
					tt.RemoteFinishedAt = 0
					updated = true
				}
			} else if state != "" {
				if tt.FinishedAt == 0 {
					remoteFin := shareAsFloat64(tm["finished_at"])
					if remoteFin > 0 {
						tt.RemoteFinishedAt = remoteFin
					}
					tt.FinishedAt = nowUnix
					updated = true
				} else if tt.RemoteFinishedAt == 0 {
					if remoteFin := shareAsFloat64(tm["finished_at"]); remoteFin > 0 {
						tt.RemoteFinishedAt = remoteFin
						updated = true
					}
				}
				if state == "failed" || state == "error" || state == "cancelled" || state == "canceled" {
					if !tt.FallbackClosed {
						tt.FallbackClosed = true
						updated = true
					}
				}
			}
			if updated {
				w.shareTasks[tid] = tt
				tasksChanged = true
			}
		} else if tt.FinishedAt == 0 && hostState == "ready" {
			if tt.HostRuntimeID != "" && currentHostRuntimeID != "" && tt.HostRuntimeID != currentHostRuntimeID {
				tt.FinishedAt = nowUnix
				tt.FallbackClosed = true
				w.shareTasks[tid] = tt
				tasksChanged = true
			} else if (tt.HostRuntimeID == "" || tt.HostRuntimeID == currentHostRuntimeID) && nowUnix-tt.StartedAt > 30 {
				tt.FinishedAt = nowUnix
				w.shareTasks[tid] = tt
				tasksChanged = true
			}
		}
	}

	// 1b. Close fallback attribution on older guest workflow tasks whenever ANY newer
	// comfyui_run task (host OR another guest share) starts so legacy remotes without
	// explicit output task_id cannot mis-attribute outputs across runs.
	for tid, tm := range liveTasksByID {
		if strings.TrimSpace(shareAsString(tm["kind"])) != "comfyui_run" {
			continue
		}
		otherTT, isShareTask := w.shareTasks[tid]
		ftCreated := shareAsFloat64(tm["created_at"])
		ftRunning := strings.EqualFold(strings.TrimSpace(shareAsString(tm["state"])), "running")
		for stid, tt := range w.shareTasks {
			if stid == tid || tt.WorkflowID == "" || tt.FallbackClosed {
				continue
			}
			shouldClose := false
			if !isShareTask {
				shouldClose = (tt.FinishedAt > 0 && ftRunning) ||
					(tt.RemoteCreatedAt > 0 && ftCreated > tt.RemoteCreatedAt) ||
					(tt.RemoteFinishedAt > 0 && ftCreated >= tt.RemoteFinishedAt-1)
			} else if tt.FinishedAt > 0 {
				shouldClose = ftRunning ||
					(tt.RemoteCreatedAt > 0 && ftCreated > tt.RemoteCreatedAt) ||
					(tt.RemoteFinishedAt > 0 && ftCreated >= tt.RemoteFinishedAt-1) ||
					otherTT.StartedAt >= tt.FinishedAt
			}
			if shouldClose {
				tt.FallbackClosed = true
				w.shareTasks[stid] = tt
				tasksChanged = true
			}
		}
	}

	resolveShareLabelLocked := func(shareID, fallback string) string {
		if rec, ok := w.workflowShares[shareID]; ok {
			if rec.Label != "" {
				return rec.Label
			}
		}
		return fallback
	}

	// 2. Attribute outputs and optionally stamp share_id / share_label onto host snapshot maps.
	for _, rawOut := range rawOutputs {
		outMap := shareAsMap(rawOut)
		if outMap == nil {
			continue
		}
		outID := strings.TrimSpace(shareAsString(outMap["id"]))
		if !outputIDRe.MatchString(outID) {
			continue
		}

		if owner, owned := w.shareOutputOwners[outID]; owned {
			if stampHostFields {
				label := resolveShareLabelLocked(owner.ShareID, owner.ShareLabel)
				outMap["share_id"] = owner.ShareID
				outMap["share_label"] = label
				snapshotModified = true
			}
			continue
		}
		if strings.EqualFold(strings.TrimSpace(shareAsString(outMap["storage"])), "drive") {
			continue
		}

		outTaskID := strings.TrimSpace(shareAsString(outMap["task_id"]))
		outCreated := shareAsFloat64(outMap["created_at"])
		createdInt := int64(outCreated)
		if createdInt <= 0 {
			createdInt = nowUnix
		}

		var matchedTask *colabOneShareTrackedTask
		if outTaskID != "" {
			if tt, ok := w.shareTasks[outTaskID]; ok {
				cp := tt
				matchedTask = &cp
			}
		} else if hostState == "ready" {
			// Fallback attribution for un-upgraded remote instances that omit task_id on comfy_outputs.
			// Uses same-clock remote timestamps, requires matching HostRuntimeID, never matches
			// storage="drive" or tasks lacking RemoteCreatedAt, and prefers strict interval bracketing.
			outWfID := strings.TrimSpace(shareAsString(outMap["workflow_id"]))
			if outWfID != "" {
				var best *colabOneShareTrackedTask
				for _, tt := range w.shareTasks {
					if tt.WorkflowID == "" || tt.WorkflowID != outWfID || tt.FallbackClosed {
						continue
					}
					if tt.HostRuntimeID != "" && tt.HostRuntimeID != currentHostRuntimeID {
						continue
					}
					if tt.RemoteCreatedAt <= 0 {
						continue
					}
					if stringSliceContains(tt.PreExistingOutputIDs, outID) {
						continue
					}
					if outCreated > 0 && outCreated < tt.RemoteCreatedAt-5 {
						continue
					}
					if tt.RemoteFinishedAt > 0 && outCreated > 0 && outCreated > tt.RemoteFinishedAt+10 {
						continue
					}
					if best == nil || isBetterShareFallbackCandidate(tt, *best, outCreated) {
						cp := tt
						best = &cp
					}
				}
				matchedTask = best
			}
		}

		if matchedTask != nil {
			for len(w.shareOutputOwners) >= maxWorkflowShareOutputsTracked {
				var oldestID string
				var oldestCreated int64
				first := true
				for k, v := range w.shareOutputOwners {
					if first || v.CreatedAt < oldestCreated || (v.CreatedAt == oldestCreated && k < oldestID) {
						oldestID = k
						oldestCreated = v.CreatedAt
						first = false
					}
				}
				if first {
					break
				}
				delete(w.shareOutputOwners, oldestID)
			}
			label := resolveShareLabelLocked(matchedTask.ShareID, "")
			w.shareOutputOwners[outID] = colabOneShareOutputOwner{
				ShareID:    matchedTask.ShareID,
				ShareLabel: label,
				CreatedAt:  createdInt,
			}
			ownersChanged = true
			if stampHostFields {
				outMap["share_id"] = matchedTask.ShareID
				outMap["share_label"] = label
				snapshotModified = true
			}
		}
	}

	// 3. Prune tracked tasks finished longer than workflowShareTaskRetention ago.
	for tid, tt := range w.shareTasks {
		if tt.FinishedAt > 0 && now.Sub(time.Unix(tt.FinishedAt, 0)) > workflowShareTaskRetention {
			delete(w.shareTasks, tid)
			tasksChanged = true
		}
	}
	w.mu.Unlock()

	if ownersChanged || tasksChanged {
		_ = w.persistWorkflowShares()
	}
	if ownersChanged {
		w.emitWorkflowSharesChange(w.WorkflowSharesState())
	}

	return snapshotModified
}

func isBetterShareFallbackCandidate(cand, best colabOneShareTrackedTask, outCreated float64) bool {
	candBracketed := outCreated > 0 && cand.RemoteCreatedAt > 0 && cand.RemoteFinishedAt > 0 &&
		outCreated >= cand.RemoteCreatedAt-5 && outCreated <= cand.RemoteFinishedAt+10
	bestBracketed := outCreated > 0 && best.RemoteCreatedAt > 0 && best.RemoteFinishedAt > 0 &&
		outCreated >= best.RemoteCreatedAt-5 && outCreated <= best.RemoteFinishedAt+10
	if candBracketed != bestBracketed {
		return candBracketed
	}
	if candBracketed {
		candExact := outCreated >= cand.RemoteCreatedAt && outCreated <= cand.RemoteFinishedAt
		bestExact := outCreated >= best.RemoteCreatedAt && outCreated <= best.RemoteFinishedAt
		if candExact != bestExact {
			return candExact
		}
		candDist := math.Abs(outCreated - cand.RemoteFinishedAt)
		bestDist := math.Abs(outCreated - best.RemoteFinishedAt)
		if candDist != bestDist {
			return candDist < bestDist
		}
	}
	if cand.StartedAt != best.StartedAt {
		return cand.StartedAt > best.StartedAt
	}
	return cand.TaskID < best.TaskID
}

// --- HTTP Handlers for Workflow Share Routes ---

func isCrossSiteSubresourceRequest(r *http.Request) bool {
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
		return false
	}
	dest := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Dest")))
	if dest != "" && dest != "document" {
		return true
	}
	mode := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Mode")))
	if mode != "" && mode != "navigate" {
		return true
	}
	return false
}

func computeWorkflowShareSwitchSignature(
	hmacKey []byte,
	sessionDigest [32]byte,
	currentShareID, targetShareID, targetNonceB64, tokenNonceB64 string,
	expUnix int64,
) []byte {
	mac := hmac.New(sha256.New, hmacKey)
	mac.Write([]byte(workflowShareSwitchHMACContext))
	mac.Write([]byte(hex.EncodeToString(sessionDigest[:])))
	mac.Write([]byte(":"))
	mac.Write([]byte(currentShareID))
	mac.Write([]byte(":"))
	mac.Write([]byte(targetShareID))
	mac.Write([]byte(":"))
	mac.Write([]byte(targetNonceB64))
	mac.Write([]byte(":"))
	mac.Write([]byte(tokenNonceB64))
	mac.Write([]byte(":"))
	mac.Write([]byte(strconv.FormatInt(expUnix, 10)))
	return mac.Sum(nil)
}

func generateWorkflowShareSwitchToken(
	hmacKey []byte,
	sessionDigest [32]byte,
	currentShareID, targetShareID, targetNonceB64 string,
	now time.Time,
) (string, error) {
	if len(hmacKey) != workflowShareHMACKeyBytes {
		return "", errors.New("missing share hmac key")
	}
	nonceRaw := make([]byte, 16)
	if _, err := rand.Read(nonceRaw); err != nil {
		return "", err
	}
	tokenNonceB64 := base64.RawURLEncoding.EncodeToString(nonceRaw)
	expUnix := now.Add(workflowShareSwitchTokenTTL).Unix()
	sig := computeWorkflowShareSwitchSignature(hmacKey, sessionDigest, currentShareID, targetShareID, targetNonceB64, tokenNonceB64, expUnix)
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)
	return targetShareID + "." + tokenNonceB64 + "." + strconv.FormatInt(expUnix, 10) + "." + sigB64, nil
}

func (w *ColabOneWebConsole) verifyWorkflowShareSwitchTokenLocked(
	rawToken string,
	sessionDigest [32]byte,
	currentShareID string,
	now time.Time,
) (string, string, bool) {
	if len(w.shareHMACKey) != workflowShareHMACKeyBytes {
		return "", workflowShareErrInvalid, false
	}
	parts := strings.Split(strings.TrimSpace(rawToken), ".")
	if len(parts) != 4 {
		return "", workflowShareErrInvalid, false
	}
	targetShareID, tokenNonceB64, expStr, sigB64 := parts[0], parts[1], parts[2], parts[3]
	if !workflowShareIDRe.MatchString(targetShareID) {
		return "", workflowShareErrInvalid, false
	}
	nonceBytes, err := base64.RawURLEncoding.DecodeString(tokenNonceB64)
	if err != nil || len(nonceBytes) != 16 || base64.RawURLEncoding.EncodeToString(nonceBytes) != tokenNonceB64 {
		return "", workflowShareErrInvalid, false
	}
	expUnix, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || expUnix <= 0 {
		return "", workflowShareErrInvalid, false
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil || len(sigBytes) != sha256.Size || base64.RawURLEncoding.EncodeToString(sigBytes) != sigB64 {
		return "", workflowShareErrInvalid, false
	}
	rec, exists := w.workflowShares[targetShareID]
	if !exists {
		return "", workflowShareErrInvalid, false
	}
	expectedSig := computeWorkflowShareSwitchSignature(w.shareHMACKey, sessionDigest, currentShareID, targetShareID, rec.NonceB64, tokenNonceB64, expUnix)
	if subtle.ConstantTimeCompare(sigBytes, expectedSig) != 1 {
		return "", workflowShareErrInvalid, false
	}
	if now.Unix() > expUnix {
		return targetShareID, workflowShareErrExpired, false
	}
	switch computeWorkflowShareStatus(rec, now) {
	case "revoked":
		return targetShareID, workflowShareErrRevoked, false
	case "used":
		return targetShareID, workflowShareErrUsed, false
	case "expired":
		return targetShareID, workflowShareErrExpired, false
	default:
		return targetShareID, "", true
	}
}

func (w *ColabOneWebConsole) handleWorkflowShareErrorPage(rw http.ResponseWriter, r *http.Request, status int, reason string) {
	nonce, err := generateBase64Token(16)
	if err != nil {
		nonce = "colabone"
	}
	locale := w.resolveGuestLocale(r)
	htmlDoc := renderColabOneWorkflowShareErrorPage(locale, reason, nonce)

	csp := fmt.Sprintf(
		"default-src 'none'; style-src 'nonce-%s'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'",
		nonce,
	)
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.Header().Set("Cache-Control", "no-store")
	rw.Header().Set("X-Robots-Tag", "noindex, nofollow")
	rw.Header().Set("Content-Security-Policy", csp)
	rw.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(rw, htmlDoc)
	}
}

func (w *ColabOneWebConsole) handleWorkflowShareSwitchPage(
	rw http.ResponseWriter,
	r *http.Request,
	currentTitle, targetTitle, switchToken string,
) {
	nonce, err := generateBase64Token(16)
	if err != nil {
		nonce = "colabone"
	}
	locale := w.resolveGuestLocale(r)
	htmlDoc := renderColabOneWorkflowShareSwitchPage(locale, currentTitle, targetTitle, switchToken, nonce)

	csp := fmt.Sprintf(
		"default-src 'none'; style-src 'nonce-%s'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'",
		nonce,
	)
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.Header().Set("Cache-Control", "no-store")
	rw.Header().Set("X-Robots-Tag", "noindex, nofollow")
	rw.Header().Set("Content-Security-Policy", csp)
	rw.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(rw, htmlDoc)
	}
}

func (w *ColabOneWebConsole) handleWorkflowShareEntry(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeMethodNotAllowed(rw, "GET, HEAD")
		return
	}

	rw.Header().Set("Referrer-Policy", "no-referrer")

	rawKey := strings.TrimSpace(r.URL.Query().Get("k"))

	// Block non-document / subresource cross-site share key activation (e.g. <img>, fetch, script).
	if rawKey != "" && isCrossSiteSubresourceRequest(r) {
		w.handleWorkflowShareErrorPage(rw, r, http.StatusForbidden, workflowShareErrInvalid)
		return
	}

	// Messaging link-preview crawlers, browser prefetchers, and HEAD probes with a key
	// must never consume single-use keys or mint sessions.
	if isLinkPreviewRequest(r) || (r.Method == http.MethodHead && rawKey != "") {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		rw.Header().Set("Cache-Control", "no-store")
		rw.Header().Set("X-Robots-Tag", "noindex, nofollow")
		rw.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(rw, `<!doctype html><html><head><meta charset="utf-8"><meta name="robots" content="noindex,nofollow"><title>ColabOne</title></head><body>ColabOne Workflow Share</body></html>`)
		}
		return
	}

	// Check active share cookie without mutating response headers first so switching shares
	// or rendering the confirmation interstitial never emits duplicate Set-Cookie headers.
	currentShareID, currentDigest, hasCurrentSession := w.authenticateShareRequestWithDigest(nil, r)
	now := w.now()

	// Case 1 & Case 2: Browser already holds a valid guest workflow share session.
	if hasCurrentSession {
		if rawKey != "" {
			// If the URL key carries the same share ID the browser is already signed into,
			// skip verification and rate limiting completely (re-visiting an expired/used own link).
			if candidateID, ok := extractWorkflowShareIDFromKey(rawKey); ok && candidateID == currentShareID {
				_, _ = w.authenticateShareRequest(rw, r)
				rw.Header().Set("Cache-Control", "no-store")
				http.Redirect(rw, r, WorkflowShareRoutePath, http.StatusFound)
				return
			}

			// Otherwise check whether k is a currently-activatable key for a DIFFERENT share,
			// enforcing rate limiting so authenticated sessions cannot brute-force other share keys.
			// Never silently overwrite the active session or consume a single-use key on GET:
			// render an explicit same-origin POST confirmation interstitial instead (Login CSRF protection).
			ip := w.clientIPForRateLimit(r)
			var retryAfter int
			var showConfirm bool
			var currentTitle, targetTitle, targetShareID, targetNonceB64 string
			var hmacKeyCopy []byte

			w.mu.Lock()
			if w.closed || w.stopping {
				w.mu.Unlock()
				w.handleWorkflowShareErrorPage(rw, r, http.StatusServiceUnavailable, workflowShareErrInvalid)
				return
			}
			retryAfter = w.checkShareRateLimitLocked(ip, now)
			if retryAfter == 0 {
				verifiedID, _, ok := w.verifyWorkflowShareKeyLocked(rawKey, now)
				if ok && verifiedID != currentShareID {
					curRec := w.workflowShares[currentShareID]
					targetRec := w.workflowShares[verifiedID]
					currentTitle = curRec.Title
					targetTitle = targetRec.Title
					targetShareID = verifiedID
					targetNonceB64 = targetRec.NonceB64
					hmacKeyCopy = append([]byte(nil), w.shareHMACKey...)
					showConfirm = true
				} else if !ok {
					w.recordShareKeyFailureLocked(ip, now)
				}
			}
			w.mu.Unlock()

			if retryAfter > 0 {
				rw.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				w.handleWorkflowShareErrorPage(rw, r, http.StatusTooManyRequests, workflowShareErrRateLimited)
				return
			}

			if showConfirm {
				switchToken, tokErr := generateWorkflowShareSwitchToken(hmacKeyCopy, currentDigest, currentShareID, targetShareID, targetNonceB64, now)
				if tokErr != nil {
					http.Error(rw, "Internal Server Error", http.StatusInternalServerError)
					return
				}
				w.handleWorkflowShareSwitchPage(rw, r, currentTitle, targetTitle, switchToken)
				return
			}
		}

		// Case 2: Staying on current share; apply sliding cookie refresh if due, strip query/subpath, or serve SPA.
		if _, stillValid := w.authenticateShareRequest(rw, r); !stillValid {
			w.handleWorkflowShareErrorPage(rw, r, http.StatusUnauthorized, workflowShareErrMissing)
			return
		}
		if r.URL.RawQuery != "" || r.URL.Path != WorkflowShareRoutePath {
			rw.Header().Set("Cache-Control", "no-store")
			http.Redirect(rw, r, WorkflowShareRoutePath, http.StatusFound)
			return
		}
		w.serveFrontendIndex(rw, r)
		return
	}

	// Case 4: No valid session and no share key query parameter.
	if rawKey == "" {
		w.handleWorkflowShareErrorPage(rw, r, http.StatusUnauthorized, workflowShareErrMissing)
		return
	}

	// Case 3: No valid session, attempting activation with ?k=...
	ip := w.clientIPForRateLimit(r)
	token, tokenErr := generateBase64Token(32)

	w.mu.Lock()
	if w.closed || w.stopping {
		w.mu.Unlock()
		w.handleWorkflowShareErrorPage(rw, r, http.StatusServiceUnavailable, workflowShareErrInvalid)
		return
	}
	if retryAfter := w.checkShareRateLimitLocked(ip, now); retryAfter > 0 {
		w.mu.Unlock()
		rw.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		w.handleWorkflowShareErrorPage(rw, r, http.StatusTooManyRequests, workflowShareErrRateLimited)
		return
	}
	targetShareID, reason, ok := w.verifyWorkflowShareKeyLocked(rawKey, now)
	if !ok {
		w.recordShareKeyFailureLocked(ip, now)
		w.mu.Unlock()
		status := http.StatusUnauthorized
		if reason == workflowShareErrExpired || reason == workflowShareErrUsed || reason == workflowShareErrRevoked {
			status = http.StatusGone
		}
		w.handleWorkflowShareErrorPage(rw, r, status, reason)
		return
	}
	if tokenErr != nil {
		w.mu.Unlock()
		http.Error(rw, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Allocate session BEFORE consuming a single-use link so hitting the global session
	// quota (429) never burns an unactivated single-use share.
	if !w.storeWorkflowShareSessionLocked(token, targetShareID, now) {
		w.mu.Unlock()
		w.handleWorkflowShareErrorPage(rw, r, http.StatusTooManyRequests, workflowShareErrSessionLimit)
		return
	}

	newDigest := sha256.Sum256([]byte(token))
	consumed := false
	consumedAt := now.Unix()
	rec := w.workflowShares[targetShareID]
	if rec.SingleUse {
		rec.ConsumedAt = consumedAt
		w.workflowShares[targetShareID] = rec
		consumed = true
	}
	w.mu.Unlock()

	if consumed {
		if err := w.persistWorkflowShares(); err != nil {
			w.mu.Lock()
			if !w.closed {
				delete(w.shareSessions, newDigest)
				if curRec, ok := w.workflowShares[targetShareID]; ok && curRec.ConsumedAt == consumedAt {
					curRec.ConsumedAt = 0
					w.workflowShares[targetShareID] = curRec
				}
			}
			w.mu.Unlock()
			w.handleWorkflowShareErrorPage(rw, r, http.StatusServiceUnavailable, workflowShareErrInvalid)
			return
		}
	}
	w.persistWorkflowShareSessions()
	w.setWorkflowShareCookie(rw, r, token, now)
	w.emitWorkflowSharesChange(w.WorkflowSharesState())

	rw.Header().Set("Cache-Control", "no-store")
	http.Redirect(rw, r, WorkflowShareRoutePath, http.StatusFound)
}

func (w *ColabOneWebConsole) handleWorkflowShareSwitch(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(rw, "POST")
		return
	}
	rw.Header().Set("Referrer-Policy", "no-referrer")
	rw.Header().Set("Cache-Control", "no-store")

	if !w.validateSameOriginRequest(r, false) {
		w.handleWorkflowShareErrorPage(rw, r, http.StatusForbidden, workflowShareErrInvalid)
		return
	}

	currentShareID, currentDigest, hasSession := w.authenticateShareRequestWithDigest(nil, r)
	if !hasSession {
		w.handleWorkflowShareErrorPage(rw, r, http.StatusUnauthorized, workflowShareErrMissing)
		return
	}

	r.Body = http.MaxBytesReader(rw, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		w.handleWorkflowShareErrorPage(rw, r, http.StatusBadRequest, workflowShareErrInvalid)
		return
	}
	rawToken := strings.TrimSpace(r.PostFormValue("token"))
	if rawToken == "" {
		w.handleWorkflowShareErrorPage(rw, r, http.StatusBadRequest, workflowShareErrInvalid)
		return
	}

	newToken, tokenErr := generateBase64Token(32)
	now := w.now()

	w.mu.Lock()
	if w.closed || w.stopping {
		w.mu.Unlock()
		w.handleWorkflowShareErrorPage(rw, r, http.StatusServiceUnavailable, workflowShareErrInvalid)
		return
	}
	curSession, curExists := w.shareSessions[currentDigest]
	curRec, curRecExists := w.workflowShares[currentShareID]
	if !curExists || curSession.shareID != currentShareID || !now.Before(curSession.expiresAt) || !curRecExists || curRec.RevokedAt > 0 {
		w.mu.Unlock()
		w.handleWorkflowShareErrorPage(rw, r, http.StatusUnauthorized, workflowShareErrMissing)
		return
	}

	targetShareID, reason, ok := w.verifyWorkflowShareSwitchTokenLocked(rawToken, currentDigest, currentShareID, now)
	if !ok {
		w.mu.Unlock()
		status := http.StatusForbidden
		if reason == workflowShareErrExpired || reason == workflowShareErrUsed || reason == workflowShareErrRevoked {
			status = http.StatusGone
		}
		w.handleWorkflowShareErrorPage(rw, r, status, reason)
		return
	}
	if targetShareID == currentShareID {
		w.mu.Unlock()
		http.Redirect(rw, r, WorkflowShareRoutePath, http.StatusSeeOther)
		return
	}
	if tokenErr != nil {
		w.mu.Unlock()
		http.Error(rw, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	delete(w.shareSessions, currentDigest)
	if !w.storeWorkflowShareSessionLocked(newToken, targetShareID, now) {
		w.shareSessions[currentDigest] = curSession
		w.mu.Unlock()
		w.handleWorkflowShareErrorPage(rw, r, http.StatusTooManyRequests, workflowShareErrSessionLimit)
		return
	}

	newDigest := sha256.Sum256([]byte(newToken))
	consumed := false
	consumedAt := now.Unix()
	rec := w.workflowShares[targetShareID]
	if rec.SingleUse {
		rec.ConsumedAt = consumedAt
		w.workflowShares[targetShareID] = rec
		consumed = true
	}
	w.mu.Unlock()

	if consumed {
		if err := w.persistWorkflowShares(); err != nil {
			w.mu.Lock()
			if !w.closed {
				delete(w.shareSessions, newDigest)
				if oldRec, oldOK := w.workflowShares[currentShareID]; oldOK && oldRec.RevokedAt == 0 && now.Before(curSession.expiresAt) {
					w.shareSessions[currentDigest] = curSession
				}
				if tgtRec, ok := w.workflowShares[targetShareID]; ok && tgtRec.ConsumedAt == consumedAt {
					tgtRec.ConsumedAt = 0
					w.workflowShares[targetShareID] = tgtRec
				}
			}
			w.mu.Unlock()
			w.handleWorkflowShareErrorPage(rw, r, http.StatusServiceUnavailable, workflowShareErrInvalid)
			return
		}
	}
	w.persistWorkflowShareSessions()
	w.setWorkflowShareCookie(rw, r, newToken, now)
	w.emitWorkflowSharesChange(w.WorkflowSharesState())

	http.Redirect(rw, r, WorkflowShareRoutePath, http.StatusSeeOther)
}

func (w *ColabOneWebConsole) handleShareCapabilities(rw http.ResponseWriter, shareID string) {
	w.mu.RLock()
	rec, exists := w.workflowShares[shareID]
	w.mu.RUnlock()
	if !exists || rec.RevokedAt > 0 {
		writeJSONError(rw, http.StatusUnauthorized, "分享会话已失效")
		return
	}
	expanded := expandWorkflowShareIDs(rec.WorkflowIDs)
	writeJSON(rw, http.StatusOK, ColabOneWorkflowShareCapabilities{
		Runtime:              WorkflowShareClientHeaderValue,
		Version:              w.opts.AppVersion,
		ShareID:              rec.ID,
		Title:                rec.Title,
		AllowedWorkflowIDs:   expanded,
		CanDownloadOutput:    true,
		CanDeleteOutput:      true,
		CanExportToHostDrive: true,
	})
}

func (w *ColabOneWebConsole) handleShareSnapshot(rw http.ResponseWriter, r *http.Request, shareID string) {
	if w.opts.CallBridge == nil {
		writeJSONError(rw, http.StatusServiceUnavailable, "控制器桥接未初始化")
		return
	}
	raw, err := w.opts.CallBridge(r.Context(), "snapshot", nil)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			writeJSONError(rw, http.StatusGatewayTimeout, sanitizeGuestBridgeError(err.Error()))
			return
		}
		writeJSONError(rw, http.StatusInternalServerError, sanitizeGuestBridgeError(err.Error()))
		return
	}

	var hostSnap map[string]any
	if err := json.Unmarshal([]byte(raw), &hostSnap); err != nil || hostSnap == nil {
		writeJSONError(rw, http.StatusInternalServerError, "宿主机快照格式无效")
		return
	}

	// Reconcile output ownership in-place without re-encoding the full host snapshot.
	w.reconcileShareOutputsMap(hostSnap, false)

	w.mu.RLock()
	rec, exists := w.workflowShares[shareID]
	if !exists || rec.RevokedAt > 0 {
		w.mu.RUnlock()
		writeJSONError(rw, http.StatusUnauthorized, "分享会话已失效")
		return
	}
	hmacKeyCopy := append([]byte(nil), w.shareHMACKey...)
	entryIDs := append([]string{}, rec.WorkflowIDs...)
	shareTitle := rec.Title
	ownedOutputs := make(map[string]bool)
	for outID, owner := range w.shareOutputOwners {
		if owner.ShareID == shareID {
			ownedOutputs[outID] = true
		}
	}
	ownedTasks := make(map[string]bool)
	for tid, tt := range w.shareTasks {
		if tt.ShareID == shareID {
			ownedTasks[tid] = true
		}
	}
	w.mu.RUnlock()

	expandedIDs := expandWorkflowShareIDs(entryIDs)
	allowedSet := make(map[string]bool, len(expandedIDs))
	for _, id := range expandedIDs {
		allowedSet[id] = true
	}
	allowsDH := allowsDigitalHumanWorkflow(expandedIDs)

	hostRuntime := shareAsMap(hostSnap["runtime"])
	hostState := strings.TrimSpace(shareAsString(hostRuntime["state"]))
	if hostState == "" {
		hostState = "stopped"
	}
	hostDriveConnected := shareAsBool(shareAsMap(hostRuntime["drive"])["connected"])
	driveExportReady := hostState == "ready" && hostDriveConnected && runtimeSupportsCapability(hostRuntime, "comfyui_drive_export")

	guestRuntimeID := ""
	if hostState == "ready" {
		guestRuntimeID = deriveGuestRuntimeID(hmacKeyCopy, shareID, shareAsString(hostRuntime["id"]))
	}

	guestRuntime := map[string]any{
		"id":                 guestRuntimeID,
		"state":              hostState,
		"drive_export_ready": driveExportReady,
	}

	if hostGPU := shareAsMap(hostRuntime["gpu"]); hostGPU != nil {
		gpuOut := map[string]any{}
		if v, ok := hostGPU["name"]; ok {
			gpuOut["name"] = v
		}
		if v, ok := hostGPU["total_mib"]; ok {
			gpuOut["total_mib"] = v
		}
		if len(gpuOut) > 0 {
			guestRuntime["gpu"] = gpuOut
		}
	}

	guestTasks := make([]map[string]any, 0)
	busyOther := isHostBusyWithExclusiveWorkload(hostRuntime)
	hasGuestComfyRunTask := false
	for _, rawTask := range shareAsSlice(hostRuntime["tasks"]) {
		tm := shareAsMap(rawTask)
		if tm == nil {
			continue
		}
		tid := strings.TrimSpace(shareAsString(tm["id"]))
		isRunning := strings.EqualFold(strings.TrimSpace(shareAsString(tm["state"])), "running")
		if ownedTasks[tid] {
			if isRunning && strings.TrimSpace(shareAsString(tm["kind"])) == "comfyui_run" {
				hasGuestComfyRunTask = true
			}
			guestTasks = append(guestTasks, sanitizeGuestTaskMap(tm))
			continue
		}
		if isRunning {
			busyOther = true
		}
	}
	guestRuntime["tasks"] = guestTasks

	if hostComfy := shareAsMap(hostRuntime["comfyui"]); hostComfy != nil {
		comfyOut := map[string]any{}
		for _, k := range guestAllowedComfyUIFields {
			if v, ok := hostComfy[k]; ok {
				if k == "error" {
					if s, okStr := v.(string); okStr && strings.TrimSpace(s) != "" {
						comfyOut[k] = sanitizeGuestBridgeError(s)
						continue
					}
				}
				comfyOut[k] = v
			}
		}
		if activePack := strings.TrimSpace(shareAsString(hostComfy["active_pack"])); activePack != "" && allowedSet[activePack] {
			comfyOut["active_pack"] = activePack
		}
		queueRunning := int(shareAsFloat64(hostComfy["queue_running"]))
		queuePending := int(shareAsFloat64(hostComfy["queue_pending"]))
		if !hasGuestComfyRunTask && (queueRunning > 0 || queuePending > 0) {
			busyOther = true
			comfyOut["queue_running"] = 0
			comfyOut["queue_pending"] = 0
		}
		guestRuntime["comfyui"] = comfyOut
	}

	if hostIdle := shareAsMap(hostRuntime["idle"]); hostIdle != nil {
		if serverTime, ok := hostIdle["server_time"]; ok {
			guestRuntime["idle"] = map[string]any{"server_time": serverTime}
		}
	}

	guestWorkflows := make([]any, 0)
	for _, rawWf := range shareAsSlice(hostSnap["workflows"]) {
		wfMap := shareAsMap(rawWf)
		if wfMap == nil {
			continue
		}
		if allowedSet[strings.TrimSpace(shareAsString(wfMap["id"]))] {
			guestWorkflows = append(guestWorkflows, wfMap)
		}
	}

	var guestTTSEngines any = []any{}
	if allowsDH {
		if rawTTS := shareAsSlice(hostSnap["tts_engines"]); rawTTS != nil {
			guestTTSEngines = rawTTS
		}
	}

	guestOutputs := make([]map[string]any, 0)
	if hostState == "ready" {
		for _, rawOut := range shareAsSlice(hostSnap["comfy_outputs"]) {
			outMap := shareAsMap(rawOut)
			if outMap == nil {
				continue
			}
			outID := strings.TrimSpace(shareAsString(outMap["id"]))
			if !ownedOutputs[outID] {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(shareAsString(outMap["storage"])), "drive") {
				continue
			}
			cleanOut := make(map[string]any, len(guestAllowedOutputFields))
			for _, k := range guestAllowedOutputFields {
				if v, ok := outMap[k]; ok {
					cleanOut[k] = v
				}
			}
			if shareAsBool(outMap["drive_exported"]) {
				cleanOut["drive_exported"] = true
			} else {
				delete(cleanOut, "drive_exported")
			}
			guestOutputs = append(guestOutputs, cleanOut)
		}
	}

	guestSnap := map[string]any{
		"share": map[string]any{
			"id":                   shareID,
			"title":                shareTitle,
			"allowed_workflow_ids": expandedIDs,
			"workflow_entry_ids":   entryIDs,
			"busy_other":           busyOther,
			"drive_export_ready":   driveExportReady,
		},
		"runtime":       guestRuntime,
		"workflows":     guestWorkflows,
		"tts_engines":   guestTTSEngines,
		"comfy_outputs": guestOutputs,
		"models":        []any{},
		"logs":          []any{},
		"account":       map[string]any{"logged_in": false},
		"setup":         map[string]any{"colab": false, "drive": false, "hf": false},
		"settings":      map[string]any{},
	}

	rawBytes, err := encodeJSONNoEscape(guestSnap)
	if err != nil {
		writeJSONError(rw, http.StatusInternalServerError, "序列化分享快照失败")
		return
	}
	writeSnapshotBytes(rw, r, rawBytes)
}

func (w *ColabOneWebConsole) handleShareCommand(rw http.ResponseWriter, r *http.Request, shareID string) {
	if w.opts.CallBridge == nil {
		writeJSONError(rw, http.StatusServiceUnavailable, "控制器桥接未初始化")
		return
	}

	const maxWorkflowShareCommandBodyBytes int64 = 2 << 20
	normalLimit, mediaLimit := w.commandBodyLimits()
	if normalLimit > maxWorkflowShareCommandBodyBytes {
		normalLimit = maxWorkflowShareCommandBodyBytes
	}
	if mediaLimit > maxWorkflowShareUploadBodyBytes {
		mediaLimit = maxWorkflowShareUploadBodyBytes
	}
	if r.ContentLength > mediaLimit {
		writeJSONError(rw, http.StatusRequestEntityTooLarge, "请求体超出限制")
		return
	}

	peekSize := int64(commandActionPeekBytes)
	if normalLimit+1 < peekSize {
		peekSize = normalLimit + 1
	}
	prefixBuf := make([]byte, peekSize)
	prefixN, prefixErr := io.ReadFull(r.Body, prefixBuf)
	if prefixErr != nil && !errors.Is(prefixErr, io.EOF) && !errors.Is(prefixErr, io.ErrUnexpectedEOF) {
		writeJSONError(rw, http.StatusBadRequest, "命令请求格式无效")
		return
	}
	prefix := prefixBuf[:prefixN]

	bodyLimit := normalLimit
	var releaseUploadSlot func()
	if peekCommandAction(prefix) == "comfyui.input.upload" {
		bodyLimit = mediaLimit
		var acquired bool
		releaseUploadSlot, acquired = w.acquireShareUploadSlot(r.Context())
		if !acquired {
			rw.Header().Set("Retry-After", "2")
			writeJSONError(rw, http.StatusTooManyRequests, "当前上传任务较多，请稍后再试")
			return
		}
		defer releaseUploadSlot()
	}
	if r.ContentLength > bodyLimit {
		writeJSONError(rw, http.StatusRequestEntityTooLarge, "请求体超出限制")
		return
	}

	cr := &countingReadCloser{
		r: io.MultiReader(bytes.NewReader(prefix), r.Body),
		c: r.Body,
	}
	r.Body = http.MaxBytesReader(rw, cr, bodyLimit)
	var req struct {
		Action  string         `json:"action"`
		Payload map[string]any `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSONError(rw, http.StatusRequestEntityTooLarge, "请求体超出限制")
			return
		}
		writeJSONError(rw, http.StatusBadRequest, "命令请求格式无效")
		return
	}

	action := strings.TrimSpace(req.Action)
	if cr.n > normalLimit && action != "comfyui.input.upload" {
		writeJSONError(rw, http.StatusRequestEntityTooLarge, "请求体超出限制")
		return
	}
	if action == "" {
		writeJSONError(rw, http.StatusBadRequest, "缺少命令名称")
		return
	}

	w.mu.RLock()
	rec, exists := w.workflowShares[shareID]
	w.mu.RUnlock()
	if !exists || rec.RevokedAt > 0 {
		writeJSONError(rw, http.StatusUnauthorized, "分享会话已失效")
		return
	}
	expandedIDs := expandWorkflowShareIDs(rec.WorkflowIDs)
	allowedSet := make(map[string]bool, len(expandedIDs))
	for _, id := range expandedIDs {
		allowedSet[id] = true
	}
	allowsDH := allowsDigitalHumanWorkflow(expandedIDs)

	payload := req.Payload
	if payload == nil {
		payload = map[string]any{}
	}

	writeBridgeErr := func(err error) {
		if errors.Is(err, context.DeadlineExceeded) {
			writeJSONError(rw, http.StatusGatewayTimeout, sanitizeGuestBridgeError(err.Error()))
			return
		}
		writeJSONError(rw, http.StatusBadRequest, sanitizeGuestBridgeError(err.Error()))
	}

	writeRawJSON := func(res string) {
		rw.Header().Set("Content-Type", "application/json; charset=utf-8")
		rw.Header().Set("Cache-Control", "no-store")
		rw.WriteHeader(http.StatusOK)
		if strings.TrimSpace(res) == "" {
			_, _ = io.WriteString(rw, "{}")
			return
		}
		_, _ = io.WriteString(rw, res)
	}

	switch action {
	case "comfyui.start":
		snapRaw, snapErr := w.opts.CallBridge(r.Context(), "snapshot", nil)
		if snapErr != nil {
			if errors.Is(snapErr, context.DeadlineExceeded) {
				writeJSONError(rw, http.StatusGatewayTimeout, sanitizeGuestBridgeError(snapErr.Error()))
				return
			}
			writeJSONError(rw, http.StatusBadRequest, "当前算力服务暂未就绪，请联系分享者开启")
			return
		}
		var snap map[string]any
		if json.Unmarshal([]byte(snapRaw), &snap) != nil {
			writeJSONError(rw, http.StatusBadRequest, "当前算力服务暂未就绪，请联系分享者开启")
			return
		}
		rt := shareAsMap(snap["runtime"])
		if strings.TrimSpace(shareAsString(rt["state"])) != "ready" {
			writeJSONError(rw, http.StatusBadRequest, "当前算力服务暂未就绪，请联系分享者开启")
			return
		}
		if isHostBusyWithExclusiveWorkload(rt) {
			writeJSONError(rw, http.StatusConflict, "云端算力当前正忙，暂不可执行该操作，请稍后再试")
			return
		}
		hostRuntimeID := strings.TrimSpace(shareAsString(rt["id"]))
		res, err := w.opts.CallBridge(r.Context(), "comfyui.start", map[string]any{})
		if err != nil {
			writeBridgeErr(err)
			return
		}
		var taskObj map[string]any
		if json.Unmarshal([]byte(res), &taskObj) == nil && taskObj != nil {
			if tid := strings.TrimSpace(shareAsString(taskObj["id"])); tid != "" {
				w.recordShareTask(shareID, tid, "", hostRuntimeID, nil, shareAsFloat64(taskObj["created_at"]), true)
			}
			if encoded, encErr := encodeJSONNoEscape(sanitizeGuestTaskMap(taskObj)); encErr == nil {
				writeRawJSON(string(encoded))
				return
			}
		}
		writeRawJSON(res)
		return

	case "comfyui.input.upload":
		if releaseUploadSlot == nil {
			var acquired bool
			releaseUploadSlot, acquired = w.acquireShareUploadSlot(r.Context())
			if !acquired {
				rw.Header().Set("Retry-After", "2")
				writeJSONError(rw, http.StatusTooManyRequests, "当前上传任务较多，请稍后再试")
				return
			}
			defer releaseUploadSlot()
		}
		res, err := w.opts.CallBridge(r.Context(), "comfyui.input.upload", payload)
		if err != nil {
			writeBridgeErr(err)
			return
		}
		var uploaded map[string]any
		if json.Unmarshal([]byte(res), &uploaded) == nil && uploaded != nil {
			if inputID := strings.TrimSpace(shareAsString(uploaded["id"])); outputIDRe.MatchString(inputID) {
				w.recordShareInput(shareID, inputID)
			}
		}
		writeRawJSON(res)
		return

	case "comfyui.run":
		wfID := strings.TrimSpace(shareAsString(payload["workflow_id"]))
		if wfID == "" || !allowedSet[wfID] {
			writeJSONError(rw, http.StatusForbidden, "当前分享链接未授权运行该工作流")
			return
		}
		cleanPayload := cloneShallowMap(payload)
		delete(cleanPayload, "auto_release_after_sync")
		delete(cleanPayload, "hf_token")
		delete(cleanPayload, "skip_auto_release")
		if rawInputs, hasInputs := cleanPayload["inputs"]; hasInputs && rawInputs != nil {
			inputsMap, ok := rawInputs.(map[string]any)
			if !ok {
				writeJSONError(rw, http.StatusBadRequest, "工作流输入参数格式无效")
				return
			}
			for _, v := range inputsMap {
				inputID, ok := v.(string)
				if !ok {
					writeJSONError(rw, http.StatusBadRequest, "工作流输入素材编号格式无效")
					return
				}
				inputID = strings.TrimSpace(inputID)
				if inputID == "" {
					continue
				}
				if !w.ownsInputForShare(shareID, inputID) {
					writeJSONError(rw, http.StatusForbidden, "无权使用该输入素材")
					return
				}
			}
		}

		// Inspect pre-run host state for exclusive workload / auto_release protection,
		// workflow override schema validation, and pre-existing outputs (fail closed).
		preSnapOK := false
		var hostRuntimeID string
		var targetWF map[string]any
		var preExistingIDs []string
		snapRaw, snapErr := w.opts.CallBridge(r.Context(), "snapshot", nil)
		if snapErr != nil {
			writeBridgeErr(snapErr)
			return
		}
		var snap map[string]any
		if json.Unmarshal([]byte(snapRaw), &snap) != nil || snap == nil {
			writeJSONError(rw, http.StatusServiceUnavailable, "无法确认宿主机当前运行状态，请稍后再试")
			return
		}
		preSnapOK = true
		rt := shareAsMap(snap["runtime"])
		hostRuntimeID = strings.TrimSpace(shareAsString(rt["id"]))
		if isHostBusyWithExclusiveWorkload(rt) {
			writeJSONError(rw, http.StatusConflict, "云端算力当前正忙，暂不可执行该操作，请稍后再试")
			return
		}
		autoRelease := shareAsMap(shareAsMap(rt["comfyui"])["auto_release"])
		if shareAsBool(autoRelease["enabled"]) && !runtimeExplicitlySupportsCapability(rt, "comfyui_share_guard") {
			writeJSONError(rw, http.StatusConflict, "宿主机已开启任务完成后自动释放算力，请稍后再试")
			return
		}
		for _, rawWF := range shareAsSlice(snap["workflows"]) {
			if wm := shareAsMap(rawWF); wm != nil && strings.TrimSpace(shareAsString(wm["id"])) == wfID {
				targetWF = wm
				break
			}
		}
		for _, rawOut := range shareAsSlice(snap["comfy_outputs"]) {
			if om := shareAsMap(rawOut); om != nil {
				if oid := strings.TrimSpace(shareAsString(om["id"])); outputIDRe.MatchString(oid) {
					preExistingIDs = append(preExistingIDs, oid)
				}
			}
		}

		if err := validateGuestWorkflowOverrides(cleanPayload["overrides"], targetWF); err != nil {
			writeJSONError(rw, http.StatusBadRequest, err.Error())
			return
		}
		// Always mark guest workflow runs with skip_auto_release so they can never
		// trigger or disarm host workflow_auto_release (controller strips this flag
		// automatically when targeting older remotes without comfyui_share_guard).
		cleanPayload["skip_auto_release"] = true

		res, err := w.opts.CallBridge(r.Context(), "comfyui.run", cleanPayload)
		if err != nil {
			writeBridgeErr(err)
			return
		}
		var taskObj map[string]any
		if json.Unmarshal([]byte(res), &taskObj) == nil && taskObj != nil {
			if tid := strings.TrimSpace(shareAsString(taskObj["id"])); tid != "" {
				w.recordShareTask(shareID, tid, wfID, hostRuntimeID, preExistingIDs, shareAsFloat64(taskObj["created_at"]), !preSnapOK)
			}
			if encoded, encErr := encodeJSONNoEscape(sanitizeGuestTaskMap(taskObj)); encErr == nil {
				writeRawJSON(string(encoded))
				return
			}
		}
		writeRawJSON(res)
		return

	case "task.cancel":
		tid := strings.TrimSpace(shareAsString(payload["id"]))
		if !outputIDRe.MatchString(tid) {
			writeJSONError(rw, http.StatusBadRequest, "任务编号无效")
			return
		}
		if !w.ownsTaskForShare(shareID, tid) {
			writeJSONError(rw, http.StatusForbidden, "无权取消该任务")
			return
		}
		res, err := w.opts.CallBridge(r.Context(), "task.cancel", map[string]any{"id": tid})
		if err != nil {
			writeBridgeErr(err)
			return
		}
		writeRawJSON(res)
		return

	case "comfyui.tts.preview":
		if !allowsDH {
			writeJSONError(rw, http.StatusForbidden, "当前分享链接未开放数字人语音试听功能")
			return
		}
		var hostRuntimeID string
		snapRaw, snapErr := w.opts.CallBridge(r.Context(), "snapshot", nil)
		if snapErr != nil {
			writeBridgeErr(snapErr)
			return
		}
		var snap map[string]any
		if json.Unmarshal([]byte(snapRaw), &snap) != nil || snap == nil {
			writeJSONError(rw, http.StatusServiceUnavailable, "无法确认宿主机当前运行状态，请稍后再试")
			return
		}
		rt := shareAsMap(snap["runtime"])
		hostRuntimeID = strings.TrimSpace(shareAsString(rt["id"]))
		if isHostBusyWithExclusiveWorkload(rt) {
			writeJSONError(rw, http.StatusConflict, "云端算力当前正忙，暂不可执行该操作，请稍后再试")
			return
		}
		cleanPayload := cloneShallowMap(payload)
		delete(cleanPayload, "hf_token")
		cleanPayload["skip_local_cache"] = true
		for _, field := range []string{"ref_audio_input_id", "emotion_ref_input_id"} {
			if rawVal, exists := cleanPayload[field]; exists && rawVal != nil {
				inputID, ok := rawVal.(string)
				if !ok {
					writeJSONError(rw, http.StatusBadRequest, "参考音频素材编号格式无效")
					return
				}
				inputID = strings.TrimSpace(inputID)
				if inputID != "" && !w.ownsInputForShare(shareID, inputID) {
					writeJSONError(rw, http.StatusForbidden, "无权使用该参考音频素材")
					return
				}
			}
		}
		res, err := w.opts.CallBridge(r.Context(), "comfyui.tts.preview", cleanPayload)
		if err != nil {
			writeBridgeErr(err)
			return
		}
		var previewObj map[string]any
		if json.Unmarshal([]byte(res), &previewObj) == nil && previewObj != nil {
			if pid := strings.TrimSpace(shareAsString(previewObj["preview_id"])); pid != "" {
				w.recordSharePreview(shareID, pid)
			}
			cleanPreview := sanitizeGuestTtsPreviewMap(previewObj)
			if taskMap := shareAsMap(previewObj["task"]); taskMap != nil {
				if tid := strings.TrimSpace(shareAsString(taskMap["id"])); tid != "" {
					w.recordShareTask(shareID, tid, "", hostRuntimeID, nil, shareAsFloat64(taskMap["created_at"]), true)
				}
				cleanPreview["task"] = sanitizeGuestTaskMap(taskMap)
			}
			if encoded, encErr := encodeJSONNoEscape(cleanPreview); encErr == nil {
				writeRawJSON(string(encoded))
				return
			}
		}
		writeRawJSON(res)
		return

	case "comfyui.tts.preview.asset":
		if !allowsDH {
			writeJSONError(rw, http.StatusForbidden, "当前分享链接未开放数字人语音试听功能")
			return
		}
		previewID := strings.TrimSpace(shareAsString(payload["preview_id"]))
		if previewID == "" {
			writeJSONError(rw, http.StatusBadRequest, "试听音频编号无效")
			return
		}
		if !w.ownsPreviewForShare(shareID, previewID) {
			writeJSONError(rw, http.StatusForbidden, "当前分享链接未授权访问该试听音频")
			return
		}
		assetPayload := map[string]any{
			"preview_id":       previewID,
			"skip_local_cache": true,
		}
		if offVal, hasOff := payload["offset"]; hasOff {
			assetPayload["offset"] = offVal
		}
		res, err := w.opts.CallBridge(r.Context(), action, assetPayload)
		if err != nil {
			writeBridgeErr(err)
			return
		}
		var assetObj map[string]any
		if json.Unmarshal([]byte(res), &assetObj) == nil && assetObj != nil {
			if encoded, encErr := encodeJSONNoEscape(sanitizeGuestTtsPreviewMap(assetObj)); encErr == nil {
				writeRawJSON(string(encoded))
				return
			}
		}
		writeRawJSON(res)
		return

	case "comfyui.tts.preview.cache.lookup":
		if !allowsDH {
			writeJSONError(rw, http.StatusForbidden, "当前分享链接未开放数字人语音试听功能")
			return
		}
		// Guest mode never probes host local disk cache; return an empty miss so UI falls through cleanly.
		engineID := strings.TrimSpace(shareAsString(payload["engine_id"]))
		writeJSON(rw, http.StatusOK, map[string]any{
			"found":     false,
			"engine_id": engineID,
			"items":     []any{},
		})
		return

	case "comfyui.tts.preview.cache.read":
		if !allowsDH {
			writeJSONError(rw, http.StatusForbidden, "当前分享链接未开放数字人语音试听功能")
			return
		}
		// Guest mode never reads host local disk cache; report a clean cache miss.
		writeJSON(rw, http.StatusOK, map[string]any{
			"found":        false,
			"cached":       false,
			"local_cached": false,
		})
		return

	case "comfyui.output.asset":
		outID := strings.TrimSpace(shareAsString(payload["id"]))
		if status, msg := w.requireShareLiveOutput(r.Context(), shareID, outID); status != 0 {
			writeJSONError(rw, status, msg)
			return
		}
		assetPayload := map[string]any{
			"id":           outID,
			"runtime_only": true,
		}
		if offsetVal, hasOffset := payload["offset"]; hasOffset {
			assetPayload["offset"] = offsetVal
		}
		res, err := w.opts.CallBridge(r.Context(), "comfyui.output.asset", assetPayload)
		if err != nil {
			writeBridgeErr(err)
			return
		}
		var chunkMap map[string]any
		if json.Unmarshal([]byte(res), &chunkMap) == nil && chunkMap != nil {
			if strings.EqualFold(strings.TrimSpace(shareAsString(chunkMap["source"])), "drive") {
				writeJSONError(rw, http.StatusForbidden, "该作品已归档至宿主云盘，分享模式下不可访问")
				return
			}
		}
		writeRawJSON(res)
		return

	case "comfyui.output.delete":
		outID := strings.TrimSpace(shareAsString(payload["id"]))
		if status, msg := w.requireShareLiveOutput(r.Context(), shareID, outID); status != 0 {
			writeJSONError(rw, status, msg)
			return
		}
		res, err := w.opts.CallBridge(r.Context(), "comfyui.output.delete", map[string]any{
			"id":           outID,
			"runtime_only": true,
		})
		if err != nil {
			writeBridgeErr(err)
			return
		}
		w.ForgetOutputOwnership(outID)
		writeRawJSON(res)
		return

	case "comfyui.output.export_drive":
		outID := strings.TrimSpace(shareAsString(payload["id"]))
		if !outputIDRe.MatchString(outID) {
			writeJSONError(rw, http.StatusBadRequest, "输出文件 ID 无效")
			return
		}
		if !w.ownsOutputForShare(shareID, outID) {
			writeJSONError(rw, http.StatusForbidden, "无权导出该作品文件")
			return
		}
		snapRaw, snapErr := w.opts.CallBridge(r.Context(), "snapshot", nil)
		if snapErr != nil {
			if errors.Is(snapErr, context.DeadlineExceeded) {
				writeJSONError(rw, http.StatusGatewayTimeout, sanitizeGuestBridgeError(snapErr.Error()))
				return
			}
			writeJSONError(rw, http.StatusBadRequest, "宿主机尚未连接云盘，暂不可保存到宿主云盘")
			return
		}
		var snap map[string]any
		if json.Unmarshal([]byte(snapRaw), &snap) != nil {
			writeJSONError(rw, http.StatusBadRequest, "宿主机尚未连接云盘，暂不可保存到宿主云盘")
			return
		}
		rt := shareAsMap(snap["runtime"])
		if strings.TrimSpace(shareAsString(rt["state"])) != "ready" || !shareAsBool(shareAsMap(rt["drive"])["connected"]) {
			writeJSONError(rw, http.StatusBadRequest, "宿主机尚未连接云盘，暂不可保存到宿主云盘")
			return
		}
		if !runtimeSupportsCapability(rt, "comfyui_drive_export") {
			writeJSONError(rw, http.StatusBadRequest, "宿主机当前算力版本暂不支持保存到宿主云盘")
			return
		}
		liveFound := false
		for _, rawOut := range shareAsSlice(snap["comfy_outputs"]) {
			om := shareAsMap(rawOut)
			if om == nil || strings.TrimSpace(shareAsString(om["id"])) != outID {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(shareAsString(om["storage"])), "drive") {
				writeJSONError(rw, http.StatusForbidden, "该作品已归档至宿主云盘，分享模式下不可访问")
				return
			}
			liveFound = true
			break
		}
		if !liveFound {
			writeJSONError(rw, http.StatusNotFound, "作品文件不存在或已清理")
			return
		}
		res, err := w.opts.CallBridge(r.Context(), "comfyui.output.export_drive", map[string]any{"id": outID})
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				writeJSONError(rw, http.StatusGatewayTimeout, sanitizeGuestBridgeError(err.Error()))
				return
			}
			writeJSONError(rw, http.StatusBadRequest, sanitizeGuestDriveExportError(err.Error()))
			return
		}
		alreadyExists := false
		var resMap map[string]any
		if json.Unmarshal([]byte(res), &resMap) == nil && resMap != nil {
			alreadyExists = shareAsBool(resMap["already_exists"])
		}
		writeJSON(rw, http.StatusOK, map[string]any{
			"ok":             true,
			"already_exists": alreadyExists,
		})
		return

	default:
		writeJSONError(rw, http.StatusForbidden, "该操作在工作流分享模式下不可用")
		return
	}
}

func (w *ColabOneWebConsole) handleShareOutputMedia(rw http.ResponseWriter, r *http.Request, shareID, id string) {
	rw.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	if !isSameOriginDownloadRequest(r) {
		writeHeadAwareJSONError(rw, r, http.StatusForbidden, "跨源请求已被拒绝")
		return
	}
	if status, msg := w.requireShareLiveOutput(r.Context(), shareID, id); status != 0 {
		writeHeadAwareJSONError(rw, r, status, msg)
		return
	}
	if w.opts.ServeShareOutputMedia != nil {
		if !outputIDRe.MatchString(id) {
			writeHeadAwareJSONError(rw, r, http.StatusBadRequest, "输出文件 ID 无效")
			return
		}
		w.opts.ServeShareOutputMedia(r.Context(), rw, r, id)
		return
	}
	w.handleOutputMedia(rw, r, id)
}

func (w *ColabOneWebConsole) handleShareOutputDownload(rw http.ResponseWriter, r *http.Request, shareID, id string) {
	if !isSameOriginDownloadRequest(r) {
		writeHeadAwareJSONError(rw, r, http.StatusForbidden, "跨源请求已被拒绝")
		return
	}
	if status, msg := w.requireShareLiveOutput(r.Context(), shareID, id); status != 0 {
		writeHeadAwareJSONError(rw, r, status, msg)
		return
	}
	w.handleOutputDownloadWithOptions(rw, r, id, true)
}

// --- Small Internal Utilities ---

func runtimeSupportsCapability(rt map[string]any, capName string) bool {
	caps := shareAsSlice(rt["capabilities"])
	if len(caps) == 0 {
		return true
	}
	for _, raw := range caps {
		if strings.TrimSpace(shareAsString(raw)) == capName {
			return true
		}
	}
	return false
}

func runtimeExplicitlySupportsCapability(rt map[string]any, capName string) bool {
	caps := shareAsSlice(rt["capabilities"])
	if len(caps) == 0 {
		return false
	}
	for _, raw := range caps {
		if strings.TrimSpace(shareAsString(raw)) == capName {
			return true
		}
	}
	return false
}

func (w *ColabOneWebConsole) acquireShareUploadSlot(ctx context.Context) (func(), bool) {
	if ctx != nil && ctx.Err() != nil {
		return nil, false
	}
	sem := w.shareUploadSem
	if sem == nil {
		return func() {}, true
	}
	select {
	case sem <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() {
				<-sem
			})
		}, true
	default:
		return nil, false
	}
}

func isHostLiveStudioRunning(rt map[string]any) bool {
	return shareAsBool(shareAsMap(rt["live_studio"])["running"])
}

func isHostLLMLoaded(rt map[string]any) bool {
	return strings.TrimSpace(shareAsString(rt["loaded_model"])) != ""
}

func isHostBusyWithExclusiveWorkload(rt map[string]any) bool {
	return isHostLiveStudioRunning(rt) || isHostLLMLoaded(rt)
}

func containsGuestSensitiveBridgeHint(msg string) bool {
	if guestHostUnixPathRe.MatchString(msg) || guestHostWinPathRe.MatchString(msg) || guestSensitiveTokenRe.MatchString(msg) {
		return true
	}
	lower := strings.ToLower(msg)
	for _, hostHint := range []string{
		"立即热更新",
		"热更新服务",
		"热更新",
		"google 云盘",
		"云盘凭据",
		"授权与挂载",
		"my drive",
		"colabone_drive",
		"google drive",
		"drive_file_id",
		"drive_path",
		"实时演播",
		"live_studio",
		"live studio",
		"loaded_model",
		"authorization:",
		"token=",
		"traceback (most recent",
		"http://127.0.0.1",
		"http://localhost",
	} {
		if strings.Contains(lower, hostHint) {
			return true
		}
	}
	return false
}

func sanitizeGuestBridgeError(raw string) string {
	msg := strings.TrimSpace(raw)
	if msg == "" || containsGuestSensitiveBridgeHint(msg) {
		return "操作暂时不可用，请联系分享者检查云端服务状态"
	}
	return msg
}

func sanitizeGuestDriveExportError(raw string) string {
	msg := strings.TrimSpace(raw)
	if msg == "" || containsGuestSensitiveBridgeHint(msg) {
		return "当前暂无法保存到宿主云盘，请联系分享者检查云盘状态"
	}
	return msg
}

func sanitizeGuestTtsPreviewMap(src map[string]any) map[string]any {
	out := make(map[string]any, len(guestAllowedTtsPreviewFields))
	for _, k := range guestAllowedTtsPreviewFields {
		if v, ok := src[k]; ok {
			out[k] = v
		}
	}
	return out
}

func shareAsFiniteFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return 0, false
		}
		return n, true
	case float32:
		f := float64(n)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

func validateGuestWorkflowOverrides(rawOverrides any, targetWF map[string]any) error {
	if rawOverrides == nil {
		return nil
	}
	overridesMap, ok := rawOverrides.(map[string]any)
	if !ok {
		return errors.New("工作流参数格式无效")
	}
	if len(overridesMap) == 0 {
		return nil
	}
	if targetWF == nil {
		return errors.New("无法校验工作流参数，请稍后再试")
	}
	fieldsByKey := make(map[string]map[string]any)
	for _, rawField := range shareAsSlice(targetWF["fields"]) {
		fm := shareAsMap(rawField)
		if fm == nil {
			continue
		}
		if key := strings.TrimSpace(shareAsString(fm["key"])); key != "" {
			fieldsByKey[key] = fm
		}
	}
	for k, v := range overridesMap {
		fieldDef, declared := fieldsByKey[k]
		if !declared {
			return errors.New("包含未声明的工作流参数")
		}
		ft := strings.ToLower(strings.TrimSpace(shareAsString(fieldDef["field_type"])))
		switch ft {
		case "image", "audio":
			return errors.New("媒体素材参数必须通过已上传素材传入")
		case "boolean":
			if _, ok := v.(bool); !ok {
				return errors.New("布尔参数格式无效")
			}
		case "integer":
			if _, isBool := v.(bool); isBool {
				return errors.New("整数参数格式无效")
			}
			fVal, okNum := shareAsFiniteFloat(v)
			if !okNum || math.Trunc(fVal) != fVal {
				return errors.New("整数参数格式无效")
			}
			if minRaw, hasMin := fieldDef["min_value"]; hasMin && minRaw != nil {
				if minVal, okMin := shareAsFiniteFloat(minRaw); okMin && fVal < minVal {
					return errors.New("整数参数超出允许范围")
				}
			}
			if maxRaw, hasMax := fieldDef["max_value"]; hasMax && maxRaw != nil {
				if maxVal, okMax := shareAsFiniteFloat(maxRaw); okMax && fVal > maxVal {
					return errors.New("整数参数超出允许范围")
				}
			}
		case "number":
			if _, isBool := v.(bool); isBool {
				return errors.New("数值参数格式无效")
			}
			fVal, okNum := shareAsFiniteFloat(v)
			if !okNum {
				return errors.New("数值参数格式无效")
			}
			if minRaw, hasMin := fieldDef["min_value"]; hasMin && minRaw != nil {
				if minVal, okMin := shareAsFiniteFloat(minRaw); okMin && fVal < minVal {
					return errors.New("数值参数超出允许范围")
				}
			}
			if maxRaw, hasMax := fieldDef["max_value"]; hasMax && maxRaw != nil {
				if maxVal, okMax := shareAsFiniteFloat(maxRaw); okMax && fVal > maxVal {
					return errors.New("数值参数超出允许范围")
				}
			}
		case "select":
			sVal, okStr := v.(string)
			if !okStr || !utf8.ValidString(sVal) {
				return errors.New("选项参数格式无效")
			}
			opts := shareAsSlice(fieldDef["options"])
			matchedOpt := false
			for _, rawOpt := range opts {
				if shareAsString(rawOpt) == sVal {
					matchedOpt = true
					break
				}
			}
			if !matchedOpt {
				if k == "pipeline.voice_preset" {
					if !workflowShareVoicePresetRe.MatchString(sVal) {
						return errors.New("选项参数不在允许范围内")
					}
				} else if len(opts) > 0 {
					return errors.New("选项参数不在允许范围内")
				} else if utf8.RuneCountInString(sVal) > 160 {
					return errors.New("选项参数长度超出限制")
				}
			}
		case "text", "textarea":
			sVal, okStr := v.(string)
			if !okStr || !utf8.ValidString(sVal) {
				return errors.New("文本参数格式无效")
			}
			if utf8.RuneCountInString(sVal) > maxGuestOverrideStringRunes {
				return errors.New("文本参数长度超出限制")
			}
		default:
			return errors.New("不支持的工作流参数类型")
		}
	}
	return nil
}

func sanitizeGuestTaskMap(src map[string]any) map[string]any {
	out := make(map[string]any, len(guestAllowedTaskFields))
	for _, k := range guestAllowedTaskFields {
		if v, ok := src[k]; ok {
			if k == "error" {
				if s, okStr := v.(string); okStr && strings.TrimSpace(s) != "" {
					out[k] = sanitizeGuestBridgeError(s)
					continue
				}
			}
			out[k] = v
		}
	}
	return out
}

func cloneShallowMap(src map[string]any) map[string]any {
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func shareAsMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func shareAsSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

func shareAsString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func shareAsBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func shareAsFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return f
		}
	}
	return 0
}

func stringSliceContains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func encodeJSONNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte{'\n'}), nil
}
