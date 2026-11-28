package webconsole

import (
	"archive/zip"
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
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/luolongfei/ColabOne/console/internal/fsutil"
	"golang.org/x/crypto/scrypt"
)

const (
	// OutputShareRoutePath is the HTTP route path for guest output gallery sharing.
	OutputShareRoutePath = "/gallery"
	// OutputShareUnlockRoutePath is the HTTP POST endpoint for unlocking password-protected galleries.
	OutputShareUnlockRoutePath = "/gallery/unlock"
	// OutputShareCookieName is the HttpOnly session cookie issued to guest output gallery clients.
	OutputShareCookieName = "colabone_gal_session"
	// OutputShareClientHeaderValue is the expected X-ColabOne-Client header value on gallery API calls.
	OutputShareClientHeaderValue = "web-gallery"
	// VaultOutputShareHMACKey is the vault entry key storing the 32-byte HMAC master key for output share links.
	VaultOutputShareHMACKey = "web_output_share_hmac_key"
	// VaultOutputShareSessionsKey is the vault entry key storing persisted guest output gallery sessions.
	VaultOutputShareSessionsKey = "web_output_share_sessions"
	// VaultOutputSharePasswordsKey is the vault entry key storing plaintext gallery passwords for host view/copy.
	VaultOutputSharePasswordsKey = "web_output_share_passwords"
	// OutputSharesFilename is the JSON file persisted in ConfigDir holding output share records.
	OutputSharesFilename = "output-shares.json"

	// OutputShareTTL24h grants an output share link valid for 24 hours.
	OutputShareTTL24h = "24h"
	// OutputShareTTL7d grants an output share link valid for 7 days (default).
	OutputShareTTL7d = "7d"
	// OutputShareTTL30d grants an output share link valid for 30 days.
	OutputShareTTL30d = "30d"
	// OutputShareTTLForever grants an output share link valid until manually revoked.
	OutputShareTTLForever = "forever"
)

const (
	outputShareKeyPrefix             = "co_gal_"
	outputShareHMACContext           = "colabone:out_share:v1:"
	outputShareUnlockHMACContext     = "colabone:out_share_unlock:v1:"
	outputShareUnlockTokenTTL        = 5 * time.Minute
	outputShareCrockfordAlphabet     = "0123456789abcdefghjkmnpqrstvwxyz"
	outputShareIDLen                 = 12
	outputShareNonceBytes            = 16
	outputShareTagBytes              = 16
	outputShareHMACKeyBytes          = 32
	outputSharePasswordSaltBytes     = 16
	outputSharePasswordHashBytes     = 32
	outputShareScryptN               = 16384
	outputShareScryptR               = 8
	outputShareScryptP               = 1
	outputShareFileVersion           = 1
	outputShareSessionsVersion       = 1
	maxOutputShareTitleRunes         = 100
	maxOutputShareNoteRunes          = 160
	maxOutputShareGroupTitleRunes    = 100
	maxOutputShareWorkflowIDRunes    = 80
	maxOutputShareWorkflowNameRunes  = 100
	maxOutputShareTaskIDRunes        = 120
	maxOutputShareGroups             = 64
	maxOutputShareTotalOutputs       = 256
	maxOutputShareRecords            = 256
	maxOutputShareSessions           = 256
	maxOutputShareSessionsPerShare   = 64
	minOutputSharePasswordRunes      = 4
	maxOutputSharePasswordRunes      = 32
	outputShareRateLimitMaxPerIP      = 5
	outputShareRateLimitMaxGlobal     = 25
	outputShareRateLimitWindow        = 5 * time.Minute
	outputShareMintRateLimitMaxPerIP  = 20
	outputShareMintRateLimitWindow    = 1 * time.Minute
	outputShareSessionIdleEvictAfter  = 30 * time.Minute
	maxOutputShareConcurrentUnlocks   = 4
	maxOutputShareConcurrentZips      = 4
	maxOutputShareBodyBytes           = 256 << 10
	maxOutputShareUnlockBodyBytes     = 8 << 10
	maxOutputZipItems                 = 256
	maxOutputZipTotalBytes      int64 = 1 << 30 // 1 GiB
	maxArchiveSegmentBytes            = 180
	archiveSegmentHeadroomBytes       = 20
)

var (
	// ErrOutputMediaNotFound signals that an output artifact could not be found.
	ErrOutputMediaNotFound = errors.New("作品不存在")
	// ErrOutputMediaOffline signals that an output artifact is offline or temporarily unavailable.
	ErrOutputMediaOffline = errors.New("该作品暂离线，请稍后再试")

	errOutputZipSizeLimitExceeded = errors.New("打包下载总大小超出 1 GB 限制")

	outputShareIDRe      = regexp.MustCompile(`^[0-9a-hjkmnp-tv-z]{12}$`)
	outputShareGroupIDRe = regexp.MustCompile(`^[a-zA-Z0-9._:-]{1,80}$`)

	outputKindByExt = map[string]string{
		"png":  "image",
		"jpg":  "image",
		"jpeg": "image",
		"webp": "image",
		"gif":  "image",
		"mp4":  "video",
		"webm": "video",
		"wav":  "audio",
		"mp3":  "audio",
		"flac": "audio",
	}

	windowsReservedArchiveNames = map[string]bool{
		"CON": true, "PRN": true, "AUX": true, "NUL": true,
		"COM1": true, "COM2": true, "COM3": true, "COM4": true,
		"COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
		"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
		"LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
	}
)

// ColabOneOutputStreamMeta carries resolved media metadata returned by StreamOutputMedia.
type ColabOneOutputStreamMeta struct {
	ID        string
	Filename  string
	Extension string
	Mime      string
	Size      int64
}

// ColabOneOutputShareGroupInput describes one logical album/task group in an output share request.
type ColabOneOutputShareGroupInput struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	WorkflowID   string   `json:"workflow_id,omitempty"`
	WorkflowName string   `json:"workflow_name,omitempty"`
	TaskID       string   `json:"task_id,omitempty"`
	OutputIDs    []string `json:"output_ids"`
}

// ColabOneOutputShareCreateRequest configures a new guest output gallery share link.
type ColabOneOutputShareCreateRequest struct {
	Title         string                          `json:"title"`
	Note          string                          `json:"note"`
	TTLMode       string                          `json:"ttl_mode"`
	AllowDownload bool                            `json:"allow_download"`
	Password      string                          `json:"password,omitempty"`
	Groups        []ColabOneOutputShareGroupInput `json:"groups"`
}

// ColabOneOutputShareSummary describes an output share entry for the host UI.
type ColabOneOutputShareSummary struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Note           string   `json:"note"`
	TTLMode        string   `json:"ttl_mode"`
	AllowDownload  bool     `json:"allow_download"`
	HasPassword    bool     `json:"has_password"`
	Password       string   `json:"password,omitempty"`
	Status         string   `json:"status"`
	CreatedAt      int64    `json:"created_at"`
	ExpiresAt      int64    `json:"expires_at"`
	RevokedAt      int64    `json:"revoked_at,omitempty"`
	ActiveSessions int      `json:"active_sessions"`
	GroupCount     int      `json:"group_count"`
	ItemCount      int      `json:"item_count"`
	OutputIDs      []string `json:"output_ids"`
	URL            string   `json:"url,omitempty"`
	LocalURL       string   `json:"local_url,omitempty"`
	UsesPublicHost bool     `json:"uses_public_host"`
	CanCopyLink    bool     `json:"can_copy_link"`
}

// ColabOneOutputShareListState aggregates all output gallery shares and active guest session metrics.
type ColabOneOutputShareListState struct {
	Shares              []ColabOneOutputShareSummary `json:"shares"`
	ActiveCount         int                          `json:"active_count"`
	TotalActiveSessions int                          `json:"total_active_sessions"`
}

// ColabOneOutputShareManifestItem describes a single sanitized artifact entry exposed to gallery guests.
type ColabOneOutputShareManifestItem struct {
	ID           string  `json:"id"`
	Filename     string  `json:"filename"`
	Extension    string  `json:"extension,omitempty"`
	Kind         string  `json:"kind"`
	Mime         string  `json:"mime"`
	Size         int64   `json:"size"`
	WorkflowID   string  `json:"workflow_id,omitempty"`
	WorkflowName string  `json:"workflow_name,omitempty"`
	CreatedAt    float64 `json:"created_at,omitempty"`
	TaskID       string  `json:"task_id,omitempty"`
	IsInspect    bool    `json:"is_inspect"`
	Available    bool    `json:"available"`
}

// ColabOneOutputShareManifestGroup describes a group of artifacts exposed in GET /api/gallery/manifest.
type ColabOneOutputShareManifestGroup struct {
	ID             string                            `json:"id"`
	Title          string                            `json:"title"`
	WorkflowID     string                            `json:"workflow_id,omitempty"`
	WorkflowName   string                            `json:"workflow_name,omitempty"`
	TaskID         string                            `json:"task_id,omitempty"`
	ItemCount      int                               `json:"item_count"`
	AvailableCount int                               `json:"available_count"`
	Items          []ColabOneOutputShareManifestItem `json:"items"`
}

// ColabOneOutputShareManifest is the sanitized payload returned by GET /api/gallery/manifest.
type ColabOneOutputShareManifest struct {
	Runtime        string                             `json:"runtime"`
	Version        string                             `json:"version"`
	ShareID        string                             `json:"share_id"`
	Title          string                             `json:"title"`
	AllowDownload  bool                               `json:"allow_download"`
	CreatedAt      int64                              `json:"created_at"`
	ExpiresAt      int64                              `json:"expires_at"`
	GroupCount     int                                `json:"group_count"`
	ItemCount      int                                `json:"item_count"`
	AvailableCount int                                `json:"available_count"`
	Groups         []ColabOneOutputShareManifestGroup `json:"groups"`
}

type colabOneOutputShareItemSnapshot struct {
	ID           string  `json:"id"`
	Filename     string  `json:"filename"`
	Extension    string  `json:"extension,omitempty"`
	Kind         string  `json:"kind"`
	Mime         string  `json:"mime"`
	Size         int64   `json:"size"`
	WorkflowID   string  `json:"workflow_id,omitempty"`
	WorkflowName string  `json:"workflow_name,omitempty"`
	CreatedAt    float64 `json:"created_at,omitempty"`
	TaskID       string  `json:"task_id,omitempty"`
	IsInspect    bool    `json:"is_inspect,omitempty"`
}

type colabOneOutputShareGroupRecord struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	WorkflowID   string   `json:"workflow_id,omitempty"`
	WorkflowName string   `json:"workflow_name,omitempty"`
	TaskID       string   `json:"task_id,omitempty"`
	OutputIDs    []string `json:"output_ids"`
}

type colabOneOutputShareRecord struct {
	ID              string                                     `json:"id"`
	Title           string                                     `json:"title"`
	Note            string                                     `json:"note"`
	TTLMode         string                                     `json:"ttl_mode"`
	AllowDownload   bool                                       `json:"allow_download"`
	HasPassword     bool                                       `json:"has_password"`
	PasswordSaltB64 string                                     `json:"password_salt,omitempty"`
	PasswordHashB64 string                                     `json:"password_hash,omitempty"`
	NonceB64        string                                     `json:"nonce"`
	Groups          []colabOneOutputShareGroupRecord           `json:"groups"`
	OutputIDs       []string                                   `json:"output_ids"`
	Items           map[string]colabOneOutputShareItemSnapshot `json:"items,omitempty"`
	CreatedAt       int64                                      `json:"created_at"`
	ExpiresAt       int64                                      `json:"expires_at"`
	RevokedAt       int64                                      `json:"revoked_at,omitempty"`
}

type colabOneOutputShareSession struct {
	shareID           string
	createdAt         time.Time
	lastSeen          time.Time
	expiresAt         time.Time
	cookieRefreshedAt time.Time
}

type colabOnePersistedOutputShareSession struct {
	DigestHex string `json:"d"`
	ShareID   string `json:"sid"`
	CreatedAt int64  `json:"c"`
	LastSeen  int64  `json:"l"`
	ExpiresAt int64  `json:"e"`
}

type colabOnePersistedOutputShareSessionStore struct {
	Version  int                                   `json:"v"`
	HMACFp   string                                `json:"h"`
	Sessions []colabOnePersistedOutputShareSession `json:"s"`
}

type colabOneOutputSharesFile struct {
	Version int                         `json:"version"`
	Shares  []colabOneOutputShareRecord `json:"shares"`
}

func cloneOutputShareRecord(rec colabOneOutputShareRecord) colabOneOutputShareRecord {
	cp := rec
	cp.OutputIDs = append([]string(nil), rec.OutputIDs...)
	if len(rec.Groups) > 0 {
		cp.Groups = make([]colabOneOutputShareGroupRecord, len(rec.Groups))
		for i, g := range rec.Groups {
			gCopy := g
			gCopy.OutputIDs = append([]string(nil), g.OutputIDs...)
			cp.Groups[i] = gCopy
		}
	}
	if len(rec.Items) > 0 {
		cp.Items = make(map[string]colabOneOutputShareItemSnapshot, len(rec.Items))
		for k, v := range rec.Items {
			cp.Items[k] = v
		}
	}
	return cp
}

// --- Persistence & Vault Management ---

func (w *ColabOneWebConsole) outputSharesPath() string {
	dir := strings.TrimSpace(w.opts.ConfigDir)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, OutputSharesFilename)
}

func (w *ColabOneWebConsole) emitOutputSharesChange(state ColabOneOutputShareListState) {
	if w.opts.OnOutputSharesChange == nil {
		return
	}
	w.mu.RLock()
	closed := w.closed
	w.mu.RUnlock()
	if closed {
		return
	}
	w.opts.OnOutputSharesChange(state)
}

func (w *ColabOneWebConsole) hasOutputShares() bool {
	w.mu.RLock()
	has := len(w.outputShares) > 0
	w.mu.RUnlock()
	return has
}

func hashOutputShareHMACFingerprint(hmacKey []byte) string {
	if len(hmacKey) == 0 {
		return ""
	}
	h := sha256.New()
	h.Write([]byte("colabone:out_share_session_bind:v1:"))
	h.Write(hmacKey)
	return hex.EncodeToString(h.Sum(nil))
}

func (w *ColabOneWebConsole) loadOutputShareHMACKey() {
	if w.opts.Vault == nil {
		return
	}
	var encoded string
	found, err := w.opts.Vault.GetJSON(VaultOutputShareHMACKey, &encoded)
	if err != nil || !found || strings.TrimSpace(encoded) == "" {
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(raw) != outputShareHMACKeyBytes {
		return
	}
	w.mu.Lock()
	if !w.closed && len(w.outputShareHMACKey) == 0 {
		w.outputShareHMACKey = raw
	}
	w.mu.Unlock()
}

func (w *ColabOneWebConsole) ensureOutputShareHMACKeyOpLocked() ([]byte, error) {
	w.mu.RLock()
	if w.closed {
		w.mu.RUnlock()
		return nil, errWebConsoleClosed
	}
	existing := append([]byte(nil), w.outputShareHMACKey...)
	w.mu.RUnlock()
	if len(existing) == outputShareHMACKeyBytes {
		return existing, nil
	}

	w.outputSharePersistMu.Lock()
	defer w.outputSharePersistMu.Unlock()

	w.mu.RLock()
	if w.closed {
		w.mu.RUnlock()
		return nil, errWebConsoleClosed
	}
	existing = append([]byte(nil), w.outputShareHMACKey...)
	w.mu.RUnlock()
	if len(existing) == outputShareHMACKeyBytes {
		return existing, nil
	}

	if w.opts.Vault != nil {
		var encoded string
		found, err := w.opts.Vault.GetJSON(VaultOutputShareHMACKey, &encoded)
		if err != nil {
			return nil, fmt.Errorf("读取成果物分享签名密钥失败：%w", err)
		}
		if found && strings.TrimSpace(encoded) != "" {
			if raw, decErr := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded)); decErr == nil && len(raw) == outputShareHMACKeyBytes {
				w.mu.Lock()
				if w.closed {
					w.mu.Unlock()
					return nil, errWebConsoleClosed
				}
				w.outputShareHMACKey = append([]byte(nil), raw...)
				w.mu.Unlock()
				return raw, nil
			}
		}
	}

	key := make([]byte, outputShareHMACKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("生成成果物分享签名密钥失败：%w", err)
	}
	if w.opts.Vault != nil {
		encoded := base64.RawURLEncoding.EncodeToString(key)
		if err := w.opts.Vault.Set(VaultOutputShareHMACKey, encoded); err != nil {
			return nil, fmt.Errorf("保存成果物分享签名密钥失败：%w", err)
		}
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil, errWebConsoleClosed
	}
	w.outputShareHMACKey = append([]byte(nil), key...)
	w.mu.Unlock()
	return key, nil
}

func (w *ColabOneWebConsole) loadOutputSharePasswords() {
	if w.opts.Vault == nil {
		return
	}
	var stored map[string]string
	found, err := w.opts.Vault.GetJSON(VaultOutputSharePasswordsKey, &stored)
	if err != nil {
		w.logActivity(fmt.Sprintf("读取成果物分享访问密码失败：%s", err.Error()), "warn")
		return
	}
	if !found || len(stored) == 0 {
		return
	}
	now := w.now()
	needsPrunePersist := false
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	for sid, pw := range stored {
		rec, exists := w.outputShares[sid]
		if !exists || computeOutputShareStatus(rec, now) != "active" || !rec.HasPassword {
			needsPrunePersist = true
			continue
		}
		trimmed := strings.TrimSpace(pw)
		if trimmed != "" {
			w.outputSharePasswords[sid] = trimmed
		} else {
			needsPrunePersist = true
		}
	}
	w.mu.Unlock()
	if needsPrunePersist {
		if pErr := w.persistOutputSharePasswords(); pErr != nil && !errors.Is(pErr, errWebConsoleClosed) {
			w.logActivity(pErr.Error(), "warn")
		}
	}
}

func (w *ColabOneWebConsole) pruneExpiredOutputSharePasswords() {
	now := w.now()
	pruned := false
	w.mu.Lock()
	if !w.closed {
		for sid := range w.outputSharePasswords {
			rec, exists := w.outputShares[sid]
			if !exists || computeOutputShareStatus(rec, now) != "active" || !rec.HasPassword {
				delete(w.outputSharePasswords, sid)
				pruned = true
			}
		}
	}
	w.mu.Unlock()
	if pruned {
		if err := w.persistOutputSharePasswords(); err != nil && !errors.Is(err, errWebConsoleClosed) {
			w.logActivity(err.Error(), "warn")
		}
	}
}

func (w *ColabOneWebConsole) persistOutputSharePasswords() error {
	w.outputSharePersistMu.Lock()
	defer w.outputSharePersistMu.Unlock()

	now := w.now()
	w.mu.RLock()
	if w.closed {
		w.mu.RUnlock()
		return errWebConsoleClosed
	}
	if w.opts.Vault == nil {
		w.mu.RUnlock()
		return nil
	}
	snapshot := make(map[string]string, len(w.outputSharePasswords))
	for sid, pw := range w.outputSharePasswords {
		rec, exists := w.outputShares[sid]
		if !exists || computeOutputShareStatus(rec, now) != "active" || !rec.HasPassword || pw == "" {
			continue
		}
		snapshot[sid] = pw
	}
	w.mu.RUnlock()

	if len(snapshot) == 0 {
		if err := w.opts.Vault.Delete(VaultOutputSharePasswordsKey); err != nil {
			w.logActivity(fmt.Sprintf("清理成果物分享访问密码失败：%s", err.Error()), "warn")
			return fmt.Errorf("清理成果物分享访问密码失败：%w", err)
		}
		return nil
	}
	if err := w.opts.Vault.Set(VaultOutputSharePasswordsKey, snapshot); err != nil {
		w.logActivity(fmt.Sprintf("保存成果物分享访问密码失败：%s", err.Error()), "warn")
		return fmt.Errorf("保存成果物分享访问密码失败：%w", err)
	}
	return nil
}

func (w *ColabOneWebConsole) loadOutputShares() {
	filePath := w.outputSharesPath()
	if filePath == "" {
		return
	}
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return
	}
	var loaded colabOneOutputSharesFile
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return
	}
	if loaded.Version != 0 && loaded.Version != outputShareFileVersion {
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
		if !outputShareIDRe.MatchString(rec.ID) || nonceErr != nil || len(nonceBytes) != outputShareNonceBytes || base64.RawURLEncoding.EncodeToString(nonceBytes) != nonceB64 {
			continue
		}
		rec.NonceB64 = nonceB64
		normGroups, normIDs, err := normalizeOutputShareGroupsFromDisk(rec.Groups)
		if err != nil {
			continue
		}
		rec.Groups = normGroups
		rec.OutputIDs = normIDs
		if normTTL, ok := normalizeOutputShareTTLMode(rec.TTLMode); ok {
			rec.TTLMode = normTTL
		} else {
			rec.TTLMode = OutputShareTTL7d
		}
		if rec.HasPassword {
			salt, sErr := base64.RawURLEncoding.DecodeString(strings.TrimSpace(rec.PasswordSaltB64))
			hash, hErr := base64.RawURLEncoding.DecodeString(strings.TrimSpace(rec.PasswordHashB64))
			if sErr != nil || hErr != nil || len(salt) != outputSharePasswordSaltBytes || len(hash) != outputSharePasswordHashBytes {
				continue
			}
		}
		// Keep only allowlisted item snapshots matching rec.OutputIDs.
		allowedSet := make(map[string]bool, len(normIDs))
		for _, id := range normIDs {
			allowedSet[id] = true
		}
		cleanItems := make(map[string]colabOneOutputShareItemSnapshot, len(rec.Items))
		for id, item := range rec.Items {
			cleanID := strings.ToLower(strings.TrimSpace(id))
			if !allowedSet[cleanID] {
				continue
			}
			ext, kind, mimeType := canonicalizeOutputExtKindMIME(item.Extension, item.Filename, item.Kind, item.Mime)
			item.ID = cleanID
			item.Extension = ext
			item.Kind = kind
			item.Mime = mimeType
			cleanItems[cleanID] = item
		}
		rec.Items = cleanItems
		w.outputShares[rec.ID] = rec
	}
}

func (w *ColabOneWebConsole) persistOutputShares() error {
	w.outputSharePersistMu.Lock()
	defer w.outputSharePersistMu.Unlock()

	w.mu.RLock()
	if w.closed {
		w.mu.RUnlock()
		return errWebConsoleClosed
	}
	filePath := w.outputSharesPath()
	if filePath == "" {
		w.mu.RUnlock()
		return nil
	}
	shares := make([]colabOneOutputShareRecord, 0, len(w.outputShares))
	for _, rec := range w.outputShares {
		shares = append(shares, cloneOutputShareRecord(rec))
	}
	w.mu.RUnlock()

	sort.Slice(shares, func(i, j int) bool {
		if shares[i].CreatedAt != shares[j].CreatedAt {
			return shares[i].CreatedAt > shares[j].CreatedAt
		}
		return shares[i].ID < shares[j].ID
	})

	payload := colabOneOutputSharesFile{
		Version: outputShareFileVersion,
		Shares:  shares,
	}

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("保存成果物分享配置失败：%w", err)
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("保存成果物分享配置失败：%w", err)
	}
	data = append(data, '\n')

	tmpFile, err := os.CreateTemp(dir, ".output-shares-*.tmp")
	if err != nil {
		return fmt.Errorf("保存成果物分享配置失败：%w", err)
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
		return fmt.Errorf("保存成果物分享配置失败：%w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("保存成果物分享配置失败：%w", err)
	}
	if err := tmpFile.Close(); err != nil {
		closed = true
		return fmt.Errorf("保存成果物分享配置失败：%w", err)
	}
	closed = true
	if err := fsutil.RenameReplace(tmpPath, filePath); err != nil {
		return fmt.Errorf("保存成果物分享配置失败：%w", err)
	}
	renamed = true
	return nil
}

func (w *ColabOneWebConsole) loadPersistedOutputShareSessions() {
	if w.opts.Vault == nil {
		return
	}
	w.mu.RLock()
	hmacKey := append([]byte(nil), w.outputShareHMACKey...)
	closed := w.closed
	w.mu.RUnlock()
	if closed || len(hmacKey) != outputShareHMACKeyBytes {
		return
	}

	var store colabOnePersistedOutputShareSessionStore
	found, err := w.opts.Vault.GetJSON(VaultOutputShareSessionsKey, &store)
	if err != nil || !found {
		return
	}
	if store.Version != outputShareSessionsVersion {
		return
	}
	expectedFp := hashOutputShareHMACFingerprint(hmacKey)
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
		shareRec, exists := w.outputShares[rec.ShareID]
		if !exists || computeOutputShareStatus(shareRec, now) != "active" {
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
		if !w.ensureOutputShareSessionCapacityLocked(rec.ShareID, now) {
			continue
		}
		w.outputShareSessions[digest] = colabOneOutputShareSession{
			shareID:           rec.ShareID,
			createdAt:         createdAt,
			lastSeen:          lastSeen,
			expiresAt:         expiresAt,
			cookieRefreshedAt: lastSeen,
		}
	}
}

func (w *ColabOneWebConsole) persistOutputShareSessions() {
	if w.opts.Vault == nil {
		return
	}
	w.outputSharePersistMu.Lock()
	defer w.outputSharePersistMu.Unlock()

	now := w.now()
	w.mu.RLock()
	if w.closed {
		w.mu.RUnlock()
		return
	}
	hmacKey := append([]byte(nil), w.outputShareHMACKey...)
	entries := make([]colabOnePersistedOutputShareSession, 0, len(w.outputShareSessions))
	for digest, s := range w.outputShareSessions {
		if !now.Before(s.expiresAt) {
			continue
		}
		shareRec, exists := w.outputShares[s.shareID]
		if !exists || computeOutputShareStatus(shareRec, now) != "active" {
			continue
		}
		entries = append(entries, colabOnePersistedOutputShareSession{
			DigestHex: hex.EncodeToString(digest[:]),
			ShareID:   s.shareID,
			CreatedAt: s.createdAt.Unix(),
			LastSeen:  s.lastSeen.Unix(),
			ExpiresAt: s.expiresAt.Unix(),
		})
	}
	w.mu.RUnlock()

	if len(hmacKey) != outputShareHMACKeyBytes || len(entries) == 0 {
		if err := w.opts.Vault.Delete(VaultOutputShareSessionsKey); err != nil {
			w.logActivity(fmt.Sprintf("清理成果物分享持久化会话失败：%s", err.Error()), "warn")
		}
		return
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].LastSeen != entries[j].LastSeen {
			return entries[i].LastSeen < entries[j].LastSeen
		}
		return entries[i].DigestHex < entries[j].DigestHex
	})

	store := colabOnePersistedOutputShareSessionStore{
		Version:  outputShareSessionsVersion,
		HMACFp:   hashOutputShareHMACFingerprint(hmacKey),
		Sessions: entries,
	}
	if err := w.opts.Vault.Set(VaultOutputShareSessionsKey, store); err != nil {
		w.logActivity(fmt.Sprintf("保存成果物分享持久化会话失败：%s", err.Error()), "warn")
	}
}

// --- Key, Challenge & Password Cryptography ---

func generateOutputShareID() (string, error) {
	buf := make([]byte, outputShareIDLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成成果物分享编号失败：%w", err)
	}
	out := make([]byte, outputShareIDLen)
	for i, b := range buf {
		out[i] = outputShareCrockfordAlphabet[b&31]
	}
	return string(out), nil
}

func computeOutputShareKey(hmacKey []byte, shareID, nonceB64 string) string {
	mac := hmac.New(sha256.New, hmacKey)
	mac.Write([]byte(outputShareHMACContext))
	mac.Write([]byte(shareID))
	mac.Write([]byte(":"))
	mac.Write([]byte(nonceB64))
	tag := mac.Sum(nil)[:outputShareTagBytes]
	tagB64 := base64.RawURLEncoding.EncodeToString(tag)
	return outputShareKeyPrefix + shareID + "." + nonceB64 + "." + tagB64
}

func extractOutputShareIDFromKey(rawKey string) (string, bool) {
	if !strings.HasPrefix(rawKey, outputShareKeyPrefix) {
		return "", false
	}
	body := strings.TrimPrefix(rawKey, outputShareKeyPrefix)
	dot := strings.IndexByte(body, '.')
	if dot <= 0 {
		return "", false
	}
	sid := body[:dot]
	if !outputShareIDRe.MatchString(sid) {
		return "", false
	}
	return sid, true
}

func computeOutputShareStatus(rec colabOneOutputShareRecord, now time.Time) string {
	if rec.RevokedAt > 0 {
		return "revoked"
	}
	if rec.ExpiresAt > 0 && !now.Before(time.Unix(rec.ExpiresAt, 0)) {
		return "expired"
	}
	return "active"
}

func (w *ColabOneWebConsole) verifyOutputShareKeyLocked(rawKey string, now time.Time) (string, string, bool) {
	if len(w.outputShareHMACKey) != outputShareHMACKeyBytes {
		return "", outputShareErrInvalid, false
	}
	if !strings.HasPrefix(rawKey, outputShareKeyPrefix) {
		return "", outputShareErrInvalid, false
	}
	body := strings.TrimPrefix(rawKey, outputShareKeyPrefix)
	parts := strings.Split(body, ".")
	if len(parts) != 3 {
		return "", outputShareErrInvalid, false
	}
	shareID, nonceB64, tagB64 := parts[0], parts[1], parts[2]
	if !outputShareIDRe.MatchString(shareID) {
		return "", outputShareErrInvalid, false
	}
	nonceBytes, err := base64.RawURLEncoding.DecodeString(nonceB64)
	if err != nil || len(nonceBytes) != outputShareNonceBytes || base64.RawURLEncoding.EncodeToString(nonceBytes) != nonceB64 {
		return "", outputShareErrInvalid, false
	}
	tagBytes, err := base64.RawURLEncoding.DecodeString(tagB64)
	if err != nil || len(tagBytes) != outputShareTagBytes || base64.RawURLEncoding.EncodeToString(tagBytes) != tagB64 {
		return "", outputShareErrInvalid, false
	}

	mac := hmac.New(sha256.New, w.outputShareHMACKey)
	mac.Write([]byte(outputShareHMACContext))
	mac.Write([]byte(shareID))
	mac.Write([]byte(":"))
	mac.Write([]byte(nonceB64))
	expectedTag := mac.Sum(nil)[:outputShareTagBytes]
	if subtle.ConstantTimeCompare(tagBytes, expectedTag) != 1 {
		return "", outputShareErrInvalid, false
	}

	rec, exists := w.outputShares[shareID]
	if !exists {
		return "", outputShareErrInvalid, false
	}
	if subtle.ConstantTimeCompare([]byte(nonceB64), []byte(rec.NonceB64)) != 1 {
		return "", outputShareErrInvalid, false
	}

	switch computeOutputShareStatus(rec, now) {
	case "revoked":
		return shareID, outputShareErrRevoked, false
	case "expired":
		return shareID, outputShareErrExpired, false
	default:
		return shareID, "", true
	}
}

func computeOutputShareUnlockSignature(
	hmacKey []byte,
	shareID, shareNonceB64, challengeNonceB64 string,
	expUnix int64,
) []byte {
	mac := hmac.New(sha256.New, hmacKey)
	mac.Write([]byte(outputShareUnlockHMACContext))
	mac.Write([]byte(shareID))
	mac.Write([]byte(":"))
	mac.Write([]byte(shareNonceB64))
	mac.Write([]byte(":"))
	mac.Write([]byte(challengeNonceB64))
	mac.Write([]byte(":"))
	mac.Write([]byte(strconv.FormatInt(expUnix, 10)))
	return mac.Sum(nil)
}

func generateOutputShareUnlockChallenge(
	hmacKey []byte,
	shareID, shareNonceB64 string,
	now time.Time,
) (string, error) {
	if len(hmacKey) != outputShareHMACKeyBytes {
		return "", errors.New("missing output share hmac key")
	}
	nonceRaw := make([]byte, 16)
	if _, err := rand.Read(nonceRaw); err != nil {
		return "", err
	}
	challengeNonceB64 := base64.RawURLEncoding.EncodeToString(nonceRaw)
	expUnix := now.Add(outputShareUnlockTokenTTL).Unix()
	sig := computeOutputShareUnlockSignature(hmacKey, shareID, shareNonceB64, challengeNonceB64, expUnix)
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)
	return shareID + "." + challengeNonceB64 + "." + strconv.FormatInt(expUnix, 10) + "." + sigB64, nil
}

func (w *ColabOneWebConsole) verifyOutputShareUnlockChallengeLocked(
	rawChallenge string,
	now time.Time,
) (colabOneOutputShareRecord, string, bool) {
	if len(w.outputShareHMACKey) != outputShareHMACKeyBytes {
		return colabOneOutputShareRecord{}, outputShareErrInvalid, false
	}
	parts := strings.Split(strings.TrimSpace(rawChallenge), ".")
	if len(parts) != 4 {
		return colabOneOutputShareRecord{}, outputShareErrInvalid, false
	}
	shareID, challengeNonceB64, expStr, sigB64 := parts[0], parts[1], parts[2], parts[3]
	if !outputShareIDRe.MatchString(shareID) {
		return colabOneOutputShareRecord{}, outputShareErrInvalid, false
	}
	nonceBytes, err := base64.RawURLEncoding.DecodeString(challengeNonceB64)
	if err != nil || len(nonceBytes) != 16 || base64.RawURLEncoding.EncodeToString(nonceBytes) != challengeNonceB64 {
		return colabOneOutputShareRecord{}, outputShareErrInvalid, false
	}
	expUnix, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || expUnix <= 0 {
		return colabOneOutputShareRecord{}, outputShareErrInvalid, false
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil || len(sigBytes) != sha256.Size || base64.RawURLEncoding.EncodeToString(sigBytes) != sigB64 {
		return colabOneOutputShareRecord{}, outputShareErrInvalid, false
	}
	rec, exists := w.outputShares[shareID]
	if !exists || !rec.HasPassword {
		return colabOneOutputShareRecord{}, outputShareErrInvalid, false
	}
	expectedSig := computeOutputShareUnlockSignature(w.outputShareHMACKey, shareID, rec.NonceB64, challengeNonceB64, expUnix)
	if subtle.ConstantTimeCompare(sigBytes, expectedSig) != 1 {
		return colabOneOutputShareRecord{}, outputShareErrInvalid, false
	}
	if now.Unix() > expUnix {
		return rec, outputShareErrChallengeExpired, false
	}
	switch computeOutputShareStatus(rec, now) {
	case "revoked":
		return rec, outputShareErrRevoked, false
	case "expired":
		return rec, outputShareErrExpired, false
	default:
		return rec, "", true
	}
}

func hashOutputSharePassword(password string) (saltB64, hashB64 string, err error) {
	salt := make([]byte, outputSharePasswordSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", "", fmt.Errorf("生成访问密码盐值失败：%w", err)
	}
	derived, err := scrypt.Key(
		[]byte(password),
		salt,
		outputShareScryptN,
		outputShareScryptR,
		outputShareScryptP,
		outputSharePasswordHashBytes,
	)
	if err != nil {
		return "", "", fmt.Errorf("生成访问密码摘要失败：%w", err)
	}
	return base64.RawURLEncoding.EncodeToString(salt), base64.RawURLEncoding.EncodeToString(derived), nil
}

func verifyOutputSharePasswordHash(password, saltB64, expectedHashB64 string) bool {
	salt, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(saltB64))
	if err != nil || len(salt) != outputSharePasswordSaltBytes {
		return false
	}
	expectedHash, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(expectedHashB64))
	if err != nil || len(expectedHash) != outputSharePasswordHashBytes {
		return false
	}
	derived, err := scrypt.Key(
		[]byte(password),
		salt,
		outputShareScryptN,
		outputShareScryptR,
		outputShareScryptP,
		outputSharePasswordHashBytes,
	)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(derived, expectedHash) == 1
}

// --- Input Validation Helpers ---

func normalizeOutputShareText(raw string, maxRunes int, fieldLabel string) (string, error) {
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

func validateOutputSharePassword(raw string) (string, bool, error) {
	if !utf8.ValidString(raw) {
		return "", false, errors.New("访问密码长度需在 4 至 32 个字符之间，且不能包含控制字符")
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false, nil
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return "", false, errors.New("访问密码长度需在 4 至 32 个字符之间，且不能包含控制字符")
		}
	}
	n := utf8.RuneCountInString(trimmed)
	if n < minOutputSharePasswordRunes || n > maxOutputSharePasswordRunes {
		return "", false, errors.New("访问密码长度需在 4 至 32 个字符之间，且不能包含控制字符")
	}
	return trimmed, true, nil
}

func normalizeOutputShareTTLMode(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", OutputShareTTL7d:
		return OutputShareTTL7d, true
	case OutputShareTTL24h:
		return OutputShareTTL24h, true
	case OutputShareTTL30d:
		return OutputShareTTL30d, true
	case OutputShareTTLForever:
		return OutputShareTTLForever, true
	default:
		return "", false
	}
}

func normalizeOutputShareGroups(inputs []ColabOneOutputShareGroupInput) ([]colabOneOutputShareGroupRecord, []string, error) {
	if len(inputs) == 0 {
		return nil, nil, errors.New("请至少选择一项有效作品")
	}
	if len(inputs) > maxOutputShareGroups {
		return nil, nil, errors.New("分享分组数量不能超过 64 组")
	}
	usedGroupIDs := make(map[string]bool, len(inputs))
	allUniqueSeen := make(map[string]bool)
	allUniqueIDs := make([]string, 0)
	totalExpandedSlots := 0
	groups := make([]colabOneOutputShareGroupRecord, 0, len(inputs))

	for idx, g := range inputs {
		title, err := normalizeOutputShareText(g.Title, maxOutputShareGroupTitleRunes, "分组标题")
		if err != nil {
			return nil, nil, err
		}
		wfID, err := normalizeOutputShareText(g.WorkflowID, maxOutputShareWorkflowIDRunes, "工作流编号")
		if err != nil {
			return nil, nil, err
		}
		wfName, err := normalizeOutputShareText(g.WorkflowName, maxOutputShareWorkflowNameRunes, "工作流名称")
		if err != nil {
			return nil, nil, err
		}
		taskID, err := normalizeOutputShareText(g.TaskID, maxOutputShareTaskIDRunes, "任务编号")
		if err != nil {
			return nil, nil, err
		}

		gid := strings.TrimSpace(g.ID)
		if gid == "" || !outputShareGroupIDRe.MatchString(gid) {
			gid = fmt.Sprintf("g%d", idx+1)
		}
		if usedGroupIDs[gid] {
			base := gid
			for suffix := 2; ; suffix++ {
				cand := fmt.Sprintf("%s-%d", base, suffix)
				if len(cand) > 80 {
					cand = fmt.Sprintf("g%d-%d", idx+1, suffix)
				}
				if !usedGroupIDs[cand] {
					gid = cand
					break
				}
			}
		}
		usedGroupIDs[gid] = true

		if len(g.OutputIDs) == 0 {
			return nil, nil, errors.New("请至少选择一项有效作品")
		}
		groupSeen := make(map[string]bool, len(g.OutputIDs))
		groupOutIDs := make([]string, 0, len(g.OutputIDs))
		for _, rawID := range g.OutputIDs {
			cleanID := strings.ToLower(strings.TrimSpace(rawID))
			if !outputIDRe.MatchString(cleanID) {
				return nil, nil, errors.New("包含无效的作品编号")
			}
			if groupSeen[cleanID] {
				continue
			}
			groupSeen[cleanID] = true
			totalExpandedSlots++
			if totalExpandedSlots > maxOutputShareTotalOutputs {
				return nil, nil, errors.New("单次分享作品数量不能超过 256 项")
			}
			groupOutIDs = append(groupOutIDs, cleanID)
			if !allUniqueSeen[cleanID] {
				allUniqueSeen[cleanID] = true
				allUniqueIDs = append(allUniqueIDs, cleanID)
				if len(allUniqueIDs) > maxOutputShareTotalOutputs {
					return nil, nil, errors.New("单次分享作品数量不能超过 256 项")
				}
			}
		}
		if len(groupOutIDs) == 0 {
			return nil, nil, errors.New("请至少选择一项有效作品")
		}
		groups = append(groups, colabOneOutputShareGroupRecord{
			ID:           gid,
			Title:        title,
			WorkflowID:   wfID,
			WorkflowName: wfName,
			TaskID:       taskID,
			OutputIDs:    groupOutIDs,
		})
	}
	if len(allUniqueIDs) == 0 {
		return nil, nil, errors.New("请至少选择一项有效作品")
	}
	return groups, allUniqueIDs, nil
}

func normalizeOutputShareGroupsFromDisk(recs []colabOneOutputShareGroupRecord) ([]colabOneOutputShareGroupRecord, []string, error) {
	inputs := make([]ColabOneOutputShareGroupInput, len(recs))
	for i, g := range recs {
		inputs[i] = ColabOneOutputShareGroupInput{
			ID:           g.ID,
			Title:        g.Title,
			WorkflowID:   g.WorkflowID,
			WorkflowName: g.WorkflowName,
			TaskID:       g.TaskID,
			OutputIDs:    g.OutputIDs,
		}
	}
	return normalizeOutputShareGroups(inputs)
}

func isOutputShareInspectFilename(filename string) bool {
	lower := strings.ToLower(strings.TrimSpace(filename))
	if lower == "" {
		return false
	}
	return strings.HasPrefix(lower, "00_抠衣质检_") ||
		strings.Contains(lower, "抠衣质检") ||
		strings.HasPrefix(lower, "00_inspect_") ||
		strings.Contains(lower, "rmbg_inspect_") ||
		strings.Contains(lower, "_inspect_")
}

func extensionForAllowedOutputMIME(mimeType string) string {
	lm := strings.ToLower(strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0]))
	switch lm {
	case "image/png":
		return "png"
	case "image/jpeg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	case "video/mp4":
		return "mp4"
	case "video/webm":
		return "webm"
	case "audio/wav", "audio/x-wav", "audio/wave":
		return "wav"
	case "audio/mpeg":
		return "mp3"
	case "audio/flac", "audio/x-flac":
		return "flac"
	default:
		return ""
	}
}

func cleanAllowlistedOutputExt(rawExt string) string {
	ext := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(rawExt), "."))
	if _, ok := allowedOutputMIME[ext]; ok {
		return ext
	}
	return ""
}

func canonicalizeOutputExtKindMIME(rawExt, rawFilename, rawKind, rawMime string) (ext, kind, mimeType string) {
	ext = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(rawExt), "."))
	if ext == "" && rawFilename != "" {
		ext = strings.ToLower(strings.TrimPrefix(filepath.Ext(strings.TrimSpace(rawFilename)), "."))
	}
	if m, ok := allowedOutputMIME[ext]; ok {
		return ext, outputKindByExt[ext], m
	}
	// Try to infer deterministically from known MIME if extension is missing or unrecognised.
	if inferred := extensionForAllowedOutputMIME(rawMime); inferred != "" {
		return inferred, outputKindByExt[inferred], allowedOutputMIME[inferred]
	}
	switch strings.ToLower(strings.TrimSpace(rawKind)) {
	case "image":
		return "", "image", "application/octet-stream"
	case "video":
		return "", "video", "application/octet-stream"
	case "audio":
		return "", "audio", "application/octet-stream"
	default:
		return "", "image", "application/octet-stream"
	}
}

func shareNormalizeWorkflowName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "AI 模特换装棚拍（仅非商用）" {
		return "AI 模特换装棚拍"
	}
	return trimmed
}

func extractSanitizedItemSnapshot(outID string, outMap map[string]any) colabOneOutputShareItemSnapshot {
	rawFilename := strings.TrimSpace(shareAsString(outMap["filename"]))
	rawExt := strings.TrimSpace(shareAsString(outMap["extension"]))
	rawKind := strings.TrimSpace(shareAsString(outMap["kind"]))
	rawMime := strings.TrimSpace(shareAsString(outMap["mime"]))
	ext, kind, mimeType := canonicalizeOutputExtKindMIME(rawExt, rawFilename, rawKind, rawMime)

	filename := rawFilename
	if filename == "" {
		if ext != "" {
			filename = fmt.Sprintf("ColabOne-%s.%s", outID[:8], ext)
		} else {
			filename = fmt.Sprintf("ColabOne-%s", outID[:8])
		}
	}
	size := int64(shareAsFloat64(outMap["size"]))
	if size < 0 {
		size = 0
	}
	createdAt := shareAsFloat64(outMap["created_at"])
	if createdAt < 0 {
		createdAt = 0
	}
	return colabOneOutputShareItemSnapshot{
		ID:           outID,
		Filename:     filename,
		Extension:    ext,
		Kind:         kind,
		Mime:         mimeType,
		Size:         size,
		WorkflowID:   strings.TrimSpace(shareAsString(outMap["workflow_id"])),
		WorkflowName: shareNormalizeWorkflowName(shareAsString(outMap["workflow_name"])),
		CreatedAt:    createdAt,
		TaskID:       strings.TrimSpace(shareAsString(outMap["task_id"])),
		IsInspect:    shareAsBool(outMap["is_inspect"]) || isOutputShareInspectFilename(filename),
	}
}

func (w *ColabOneWebConsole) fetchSnapshotMap(ctx context.Context) map[string]any {
	var (
		raw string
		err error
	)
	if w.opts.FetchSnapshot != nil {
		raw, err = w.opts.FetchSnapshot(ctx)
	} else if w.opts.CallBridge != nil {
		raw, err = w.opts.CallBridge(ctx, "snapshot", nil)
	} else {
		return nil
	}
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil
	}
	var snap map[string]any
	if json.Unmarshal([]byte(raw), &snap) != nil || snap == nil {
		return nil
	}
	return snap
}

func isLiveOutputAvailableForGallery(outMap map[string]any, runtimeReady bool) bool {
	if outMap == nil {
		return false
	}
	storage := strings.ToLower(strings.TrimSpace(shareAsString(outMap["storage"])))
	if storage == "" || storage == "runtime" {
		if runtimeReady {
			return true
		}
		if explicit, ok := outMap["drive_previewable"]; ok {
			return shareAsBool(explicit)
		}
		return false
	}
	if storage == "drive" {
		if explicit, ok := outMap["drive_previewable"]; ok {
			return shareAsBool(explicit)
		}
		if strings.TrimSpace(shareAsString(outMap["drive_file_id"])) != "" {
			return true
		}
		if (shareAsBool(outMap["drive_synced"]) || shareAsBool(outMap["drive_exported"])) &&
			!strings.EqualFold(strings.TrimSpace(shareAsString(outMap["drive_mode"])), "colab_drivefs") {
			return true
		}
		return false
	}
	return false
}

// --- Host Management Methods ---

func (w *ColabOneWebConsole) preferredOutputShareLANBase(cfg ColabOneWebConsoleConfig) string {
	if cfg.PublicOrigin != "" || cfg.BindMode != BindModeLAN {
		return ""
	}
	urls := w.lanURLs(cfg.Port)
	if len(urls) == 0 {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(urls[0]), "/")
}

func (w *ColabOneWebConsole) buildOutputShareSummaryLocked(
	rec colabOneOutputShareRecord,
	now time.Time,
	sessionCounts map[string]int,
	lanBase string,
) ColabOneOutputShareSummary {
	status := computeOutputShareStatus(rec, now)
	canCopy := status == "active" && len(w.outputShareHMACKey) == outputShareHMACKeyBytes

	var shareURL, localURL string
	usesPublic := false
	if canCopy {
		key := computeOutputShareKey(w.outputShareHMACKey, rec.ID, rec.NonceB64)
		localURL = fmt.Sprintf("http://127.0.0.1:%d%s?k=%s", w.cfg.Port, OutputShareRoutePath, key)
		shareURL = localURL
		if w.cfg.PublicOrigin != "" {
			shareURL = fmt.Sprintf("%s%s?k=%s", w.cfg.PublicOrigin, OutputShareRoutePath, key)
			usesPublic = true
		} else if w.cfg.BindMode == BindModeLAN && lanBase != "" {
			shareURL = fmt.Sprintf("%s%s?k=%s", lanBase, OutputShareRoutePath, key)
		}
	}

	outIDs := append([]string{}, rec.OutputIDs...)
	if outIDs == nil {
		outIDs = []string{}
	}

	pw := ""
	if canCopy && rec.HasPassword {
		pw = w.outputSharePasswords[rec.ID]
	}

	return ColabOneOutputShareSummary{
		ID:             rec.ID,
		Title:          rec.Title,
		Note:           rec.Note,
		TTLMode:        rec.TTLMode,
		AllowDownload:  rec.AllowDownload,
		HasPassword:    rec.HasPassword,
		Password:       pw,
		Status:         status,
		CreatedAt:      rec.CreatedAt,
		ExpiresAt:      rec.ExpiresAt,
		RevokedAt:      rec.RevokedAt,
		ActiveSessions: sessionCounts[rec.ID],
		GroupCount:     len(rec.Groups),
		ItemCount:      len(outIDs),
		OutputIDs:      outIDs,
		URL:            shareURL,
		LocalURL:       localURL,
		UsesPublicHost: usesPublic,
		CanCopyLink:    canCopy,
	}
}

// OutputSharesState returns the live summary list of all output gallery shares and active guest sessions.
func (w *ColabOneWebConsole) OutputSharesState() ColabOneOutputShareListState {
	w.mu.RLock()
	cfgSnapshot := w.cfg
	w.mu.RUnlock()
	lanBase := w.preferredOutputShareLANBase(cfgSnapshot)

	now := w.now()
	w.mu.RLock()
	defer w.mu.RUnlock()
	effectiveLANBase := ""
	if w.cfg.PublicOrigin == "" && w.cfg.BindMode == BindModeLAN && w.cfg.Port == cfgSnapshot.Port {
		effectiveLANBase = lanBase
	}

	sessionCounts := make(map[string]int)
	for _, s := range w.outputShareSessions {
		if !now.Before(s.expiresAt) {
			continue
		}
		if rec, ok := w.outputShares[s.shareID]; ok && computeOutputShareStatus(rec, now) == "active" {
			sessionCounts[s.shareID]++
		}
	}

	summaries := make([]ColabOneOutputShareSummary, 0, len(w.outputShares))
	activeCount := 0
	totalActiveSessions := 0
	for _, rec := range w.outputShares {
		summary := w.buildOutputShareSummaryLocked(rec, now, sessionCounts, effectiveLANBase)
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

	return ColabOneOutputShareListState{
		Shares:              summaries,
		ActiveCount:         activeCount,
		TotalActiveSessions: totalActiveSessions,
	}
}

// GetOutputShareURL resolves the active browser URL for a persisted output gallery share ID.
func (w *ColabOneWebConsole) GetOutputShareURL(shareID string) (string, error) {
	shareID = strings.TrimSpace(shareID)
	if !outputShareIDRe.MatchString(shareID) {
		return "", errors.New("分享编号无效")
	}

	w.mu.RLock()
	cfgSnapshot := w.cfg
	w.mu.RUnlock()
	lanBase := w.preferredOutputShareLANBase(cfgSnapshot)

	now := w.now()
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed {
		return "", errors.New("网页控制台服务已关闭")
	}
	if !w.running {
		return "", errors.New("网页控制台未启动，请先启动后再打开分享链接")
	}
	rec, exists := w.outputShares[shareID]
	if !exists {
		return "", errors.New("未找到对应的成果物分享链接")
	}
	status := computeOutputShareStatus(rec, now)
	if status == "revoked" {
		return "", errors.New("该成果物分享链接已被撤销")
	}
	if status != "active" {
		return "", errors.New("该成果物分享链接已过期")
	}
	effectiveLANBase := ""
	if w.cfg.PublicOrigin == "" && w.cfg.BindMode == BindModeLAN && w.cfg.Port == cfgSnapshot.Port {
		effectiveLANBase = lanBase
	}
	summary := w.buildOutputShareSummaryLocked(rec, now, nil, effectiveLANBase)
	if !summary.CanCopyLink || strings.TrimSpace(summary.URL) == "" {
		return "", errors.New("无法生成分享链接地址")
	}
	return summary.URL, nil
}

// CreateOutputShare validates, signs, persists, and returns a new guest output gallery share link.
func (w *ColabOneWebConsole) CreateOutputShare(
	req ColabOneOutputShareCreateRequest,
) (ColabOneOutputShareSummary, error) {
	return w.createOutputShareWithContext(context.Background(), req)
}

func (w *ColabOneWebConsole) createOutputShareWithContext(
	ctx context.Context,
	req ColabOneOutputShareCreateRequest,
) (ColabOneOutputShareSummary, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ColabOneOutputShareSummary{}, err
	}
	title, err := normalizeOutputShareText(req.Title, maxOutputShareTitleRunes, "相册标题")
	if err != nil {
		return ColabOneOutputShareSummary{}, err
	}
	note, err := normalizeOutputShareText(req.Note, maxOutputShareNoteRunes, "分享备注")
	if err != nil {
		return ColabOneOutputShareSummary{}, err
	}
	ttlMode, ok := normalizeOutputShareTTLMode(req.TTLMode)
	if !ok {
		return ColabOneOutputShareSummary{}, errors.New("不支持的分享有效期设置")
	}
	password, hasPassword, err := validateOutputSharePassword(req.Password)
	if err != nil {
		return ColabOneOutputShareSummary{}, err
	}
	groups, uniqueOutputIDs, err := normalizeOutputShareGroups(req.Groups)
	if err != nil {
		return ColabOneOutputShareSummary{}, err
	}

	// Capture frozen sanitized fallback metadata before acquiring outputShareOpMu so snapshot I/O never stalls lock holders.
	frozenItems := make(map[string]colabOneOutputShareItemSnapshot, len(uniqueOutputIDs))
	if snap := w.fetchSnapshotMap(ctx); snap != nil {
		wanted := make(map[string]bool, len(uniqueOutputIDs))
		for _, id := range uniqueOutputIDs {
			wanted[id] = true
		}
		for _, rawOut := range shareAsSlice(snap["comfy_outputs"]) {
			outMap := shareAsMap(rawOut)
			if outMap == nil {
				continue
			}
			outID := strings.ToLower(strings.TrimSpace(shareAsString(outMap["id"])))
			if wanted[outID] {
				frozenItems[outID] = extractSanitizedItemSnapshot(outID, outMap)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return ColabOneOutputShareSummary{}, err
	}

	// Derive scrypt password hash outside locks so heavy CPU work does not block concurrent HTTP requests.
	var pwSaltB64, pwHashB64 string
	if hasPassword {
		pwSaltB64, pwHashB64, err = hashOutputSharePassword(password)
		if err != nil {
			return ColabOneOutputShareSummary{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return ColabOneOutputShareSummary{}, err
	}

	w.outputShareOpMu.Lock()
	if err := ctx.Err(); err != nil {
		w.outputShareOpMu.Unlock()
		return ColabOneOutputShareSummary{}, err
	}
	w.mu.RLock()
	closed := w.closed
	existingCount := len(w.outputShares)
	w.mu.RUnlock()
	if closed {
		w.outputShareOpMu.Unlock()
		return ColabOneOutputShareSummary{}, errWebConsoleClosed
	}
	if existingCount >= maxOutputShareRecords {
		w.outputShareOpMu.Unlock()
		return ColabOneOutputShareSummary{}, errors.New("成果物分享链接数量已达上限，请先删除旧记录")
	}

	if _, err := w.ensureOutputShareHMACKeyOpLocked(); err != nil {
		w.outputShareOpMu.Unlock()
		return ColabOneOutputShareSummary{}, err
	}

	nonceRaw := make([]byte, outputShareNonceBytes)
	if _, err := rand.Read(nonceRaw); err != nil {
		w.outputShareOpMu.Unlock()
		return ColabOneOutputShareSummary{}, fmt.Errorf("生成分享链接随机凭证失败：%w", err)
	}
	nonceB64 := base64.RawURLEncoding.EncodeToString(nonceRaw)

	var shareID string
	for attempts := 0; attempts < 8; attempts++ {
		cand, genErr := generateOutputShareID()
		if genErr != nil {
			w.outputShareOpMu.Unlock()
			return ColabOneOutputShareSummary{}, genErr
		}
		w.mu.RLock()
		_, exists := w.outputShares[cand]
		w.mu.RUnlock()
		if !exists {
			shareID = cand
			break
		}
	}
	if shareID == "" {
		w.outputShareOpMu.Unlock()
		return ColabOneOutputShareSummary{}, errors.New("无法分配唯一分享编号，请重试")
	}

	now := w.now()
	var expiresAt int64
	switch ttlMode {
	case OutputShareTTL24h:
		expiresAt = now.Add(24 * time.Hour).Unix()
	case OutputShareTTL7d:
		expiresAt = now.Add(7 * 24 * time.Hour).Unix()
	case OutputShareTTL30d:
		expiresAt = now.Add(30 * 24 * time.Hour).Unix()
	case OutputShareTTLForever:
		expiresAt = 0
	}

	rec := colabOneOutputShareRecord{
		ID:              shareID,
		Title:           title,
		Note:            note,
		TTLMode:         ttlMode,
		AllowDownload:   req.AllowDownload,
		HasPassword:     hasPassword,
		PasswordSaltB64: pwSaltB64,
		PasswordHashB64: pwHashB64,
		NonceB64:        nonceB64,
		Groups:          groups,
		OutputIDs:       uniqueOutputIDs,
		Items:           frozenItems,
		CreatedAt:       now.Unix(),
		ExpiresAt:       expiresAt,
	}

	w.mu.RLock()
	cfgSnapshot := w.cfg
	w.mu.RUnlock()
	lanBase := w.preferredOutputShareLANBase(cfgSnapshot)

	if err := ctx.Err(); err != nil {
		w.outputShareOpMu.Unlock()
		return ColabOneOutputShareSummary{}, err
	}

	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		w.outputShareOpMu.Unlock()
		return ColabOneOutputShareSummary{}, errWebConsoleClosed
	}
	w.outputShares[shareID] = rec
	if hasPassword {
		w.outputSharePasswords[shareID] = password
	}
	effectiveLANBase := ""
	if w.cfg.PublicOrigin == "" && w.cfg.BindMode == BindModeLAN && w.cfg.Port == cfgSnapshot.Port {
		effectiveLANBase = lanBase
	}
	summary := w.buildOutputShareSummaryLocked(rec, now, map[string]int{}, effectiveLANBase)
	w.mu.Unlock()

	if hasPassword {
		if err := w.persistOutputSharePasswords(); err != nil {
			w.mu.Lock()
			delete(w.outputShares, shareID)
			delete(w.outputSharePasswords, shareID)
			w.mu.Unlock()
			w.outputShareOpMu.Unlock()
			return ColabOneOutputShareSummary{}, err
		}
	}
	if err := w.persistOutputShares(); err != nil {
		w.mu.Lock()
		delete(w.outputShares, shareID)
		delete(w.outputSharePasswords, shareID)
		w.mu.Unlock()
		if hasPassword {
			if rbErr := w.persistOutputSharePasswords(); rbErr != nil && !errors.Is(rbErr, errWebConsoleClosed) {
				w.logActivity(fmt.Sprintf("回滚成果物分享访问密码失败：%v", rbErr), "warn")
			}
		}
		w.outputShareOpMu.Unlock()
		return ColabOneOutputShareSummary{}, err
	}
	w.outputShareOpMu.Unlock()

	w.logActivity(fmt.Sprintf("已创建成果物分享链接（%d 项作品，有效期 %s）", len(uniqueOutputIDs), ttlMode), "info")
	w.emitOutputSharesChange(w.OutputSharesState())
	return summary, nil
}

// RevokeOutputShare revokes an output gallery share link and immediately clears its guest sessions and vaulted password.
func (w *ColabOneWebConsole) RevokeOutputShare(shareID string) (ColabOneOutputShareListState, error) {
	return w.revokeOutputShareWithContext(context.Background(), shareID)
}

func (w *ColabOneWebConsole) revokeOutputShareWithContext(ctx context.Context, shareID string) (ColabOneOutputShareListState, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return w.OutputSharesState(), err
	}
	shareID = strings.TrimSpace(shareID)
	if !outputShareIDRe.MatchString(shareID) {
		return w.OutputSharesState(), errors.New("分享编号无效")
	}

	w.outputShareOpMu.Lock()
	if err := ctx.Err(); err != nil {
		w.outputShareOpMu.Unlock()
		return w.OutputSharesState(), err
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		w.outputShareOpMu.Unlock()
		return w.OutputSharesState(), errWebConsoleClosed
	}
	rec, exists := w.outputShares[shareID]
	if !exists {
		w.mu.Unlock()
		w.outputShareOpMu.Unlock()
		return w.OutputSharesState(), errors.New("未找到对应的成果物分享链接")
	}
	now := w.now()
	changed := false
	if rec.RevokedAt == 0 {
		rec.RevokedAt = now.Unix()
		w.outputShares[shareID] = rec
		changed = true
	}
	purgedSessions := w.purgeOutputShareSessionsForShareLocked(shareID)
	_, hadPassword := w.outputSharePasswords[shareID]
	delete(w.outputSharePasswords, shareID)
	w.mu.Unlock()

	if purgedSessions {
		w.persistOutputShareSessions()
	}
	if hadPassword {
		if pwErr := w.persistOutputSharePasswords(); pwErr != nil && !errors.Is(pwErr, errWebConsoleClosed) {
			w.logActivity(fmt.Sprintf("清理已撤销分享 %s 的密码记录失败：%v", shareID, pwErr), "warn")
		}
	}
	if err := w.persistOutputShares(); err != nil {
		w.outputShareOpMu.Unlock()
		state := w.OutputSharesState()
		if changed || purgedSessions || hadPassword {
			w.emitOutputSharesChange(state)
		}
		return state, err
	}
	w.outputShareOpMu.Unlock()

	w.logActivity(fmt.Sprintf("已撤销成果物分享链接 %s 并注销关联访客会话", shareID), "info")
	state := w.OutputSharesState()
	w.emitOutputSharesChange(state)
	return state, nil
}

// DeleteOutputShare permanently removes an output gallery share record and invalidates its active sessions and password.
func (w *ColabOneWebConsole) DeleteOutputShare(shareID string) (ColabOneOutputShareListState, error) {
	return w.deleteOutputShareWithContext(context.Background(), shareID)
}

func (w *ColabOneWebConsole) deleteOutputShareWithContext(ctx context.Context, shareID string) (ColabOneOutputShareListState, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return w.OutputSharesState(), err
	}
	shareID = strings.TrimSpace(shareID)
	if !outputShareIDRe.MatchString(shareID) {
		return w.OutputSharesState(), errors.New("分享编号无效")
	}

	w.outputShareOpMu.Lock()
	if err := ctx.Err(); err != nil {
		w.outputShareOpMu.Unlock()
		return w.OutputSharesState(), err
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		w.outputShareOpMu.Unlock()
		return w.OutputSharesState(), errWebConsoleClosed
	}
	existingRec, exists := w.outputShares[shareID]
	if !exists {
		w.mu.Unlock()
		w.outputShareOpMu.Unlock()
		return w.OutputSharesState(), errors.New("未找到对应的成果物分享链接")
	}
	backupRec := cloneOutputShareRecord(existingRec)
	now := w.now()
	delete(w.outputShares, shareID)
	purgedSessions := w.purgeOutputShareSessionsForShareLocked(shareID)
	_, hadPassword := w.outputSharePasswords[shareID]
	delete(w.outputSharePasswords, shareID)
	w.mu.Unlock()

	if purgedSessions {
		w.persistOutputShareSessions()
	}
	if hadPassword {
		if pwErr := w.persistOutputSharePasswords(); pwErr != nil && !errors.Is(pwErr, errWebConsoleClosed) {
			w.logActivity(fmt.Sprintf("清理已删除分享 %s 的密码记录失败：%v", shareID, pwErr), "warn")
		}
	}
	if err := w.persistOutputShares(); err != nil {
		w.mu.Lock()
		if !w.closed {
			if backupRec.RevokedAt == 0 {
				backupRec.RevokedAt = now.Unix()
			}
			w.outputShares[shareID] = backupRec
		}
		w.mu.Unlock()
		w.outputShareOpMu.Unlock()
		state := w.OutputSharesState()
		w.emitOutputSharesChange(state)
		return state, err
	}
	w.outputShareOpMu.Unlock()

	w.logActivity(fmt.Sprintf("已删除成果物分享记录 %s", shareID), "info")
	state := w.OutputSharesState()
	w.emitOutputSharesChange(state)
	return state, nil
}

func (w *ColabOneWebConsole) purgeOutputShareSessionsForShareLocked(shareID string) bool {
	removed := false
	for digest, s := range w.outputShareSessions {
		if s.shareID == shareID {
			delete(w.outputShareSessions, digest)
			removed = true
		}
	}
	return removed
}

// --- Guest Gallery Sessions, Cookies & Rate Limiting ---

func (w *ColabOneWebConsole) setOutputShareCookie(rw http.ResponseWriter, r *http.Request, token string, expiresAt time.Time, now time.Time) {
	maxAge := int(expiresAt.Sub(now).Seconds())
	if maxAge < 60 {
		maxAge = 60
	}
	http.SetCookie(rw, &http.Cookie{
		Name:     OutputShareCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   w.isHTTPSRequest(r),
	})
}

func computeOutputShareSessionExpiry(rec colabOneOutputShareRecord, now time.Time) time.Time {
	exp := now.Add(SessionTTL)
	if rec.ExpiresAt > 0 {
		shareExp := time.Unix(rec.ExpiresAt, 0)
		if shareExp.Before(exp) {
			exp = shareExp
		}
	}
	return exp
}

func (w *ColabOneWebConsole) ensureOutputShareSessionCapacityLocked(shareID string, now time.Time) bool {
	w.pruneExpiredOutputShareSessionsLocked(now)
	for countOutputShareSessionsForShareLocked(w.outputShareSessions, shareID) >= maxOutputShareSessionsPerShare {
		if !evictOldestOutputShareSessionForShareLocked(w.outputShareSessions, shareID) {
			break
		}
	}
	if len(w.outputShareSessions) >= maxOutputShareSessions {
		if !evictOldestOutputShareSessionForShareLocked(w.outputShareSessions, shareID) {
			if !evictOldestIdleOutputShareSessionLocked(w.outputShareSessions, now, outputShareSessionIdleEvictAfter) {
				return false
			}
		}
	}
	return len(w.outputShareSessions) < maxOutputShareSessions
}

func evictOldestIdleOutputShareSessionLocked(
	sessions map[[32]byte]colabOneOutputShareSession,
	now time.Time,
	idleThreshold time.Duration,
) bool {
	var oldestKey [32]byte
	var oldest colabOneOutputShareSession
	found := false
	for k, s := range sessions {
		if now.Sub(s.lastSeen) < idleThreshold {
			continue
		}
		if !found || s.lastSeen.Before(oldest.lastSeen) || (s.lastSeen.Equal(oldest.lastSeen) && s.createdAt.Before(oldest.createdAt)) {
			oldestKey = k
			oldest = s
			found = true
		}
	}
	if !found {
		return false
	}
	delete(sessions, oldestKey)
	return true
}

func (w *ColabOneWebConsole) storeOutputShareSessionLocked(rawToken string, rec colabOneOutputShareRecord, now time.Time) (time.Time, bool) {
	if !w.ensureOutputShareSessionCapacityLocked(rec.ID, now) {
		return time.Time{}, false
	}
	exp := computeOutputShareSessionExpiry(rec, now)
	if !now.Before(exp) {
		return time.Time{}, false
	}
	digest := sha256.Sum256([]byte(rawToken))
	w.outputShareSessions[digest] = colabOneOutputShareSession{
		shareID:           rec.ID,
		createdAt:         now,
		lastSeen:          now,
		expiresAt:         exp,
		cookieRefreshedAt: now,
	}
	return exp, true
}

func (w *ColabOneWebConsole) pruneExpiredOutputShareSessionsLocked(now time.Time) {
	for k, s := range w.outputShareSessions {
		if !now.Before(s.expiresAt) {
			delete(w.outputShareSessions, k)
			continue
		}
		if rec, ok := w.outputShares[s.shareID]; !ok || computeOutputShareStatus(rec, now) != "active" {
			delete(w.outputShareSessions, k)
		}
	}
}

func countOutputShareSessionsForShareLocked(sessions map[[32]byte]colabOneOutputShareSession, shareID string) int {
	n := 0
	for _, s := range sessions {
		if s.shareID == shareID {
			n++
		}
	}
	return n
}

func evictOldestOutputShareSessionForShareLocked(sessions map[[32]byte]colabOneOutputShareSession, shareID string) bool {
	var oldestKey [32]byte
	var oldest colabOneOutputShareSession
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

func (w *ColabOneWebConsole) authenticateOutputShareRequest(rw http.ResponseWriter, r *http.Request) (string, bool) {
	shareID, _, ok := w.authenticateOutputShareRequestWithReason(rw, r)
	return shareID, ok
}

func (w *ColabOneWebConsole) authenticateOutputShareRequestWithReason(rw http.ResponseWriter, r *http.Request) (string, string, bool) {
	c, err := r.Cookie(OutputShareCookieName)
	if err != nil || c.Value == "" {
		return "", outputShareErrMissing, false
	}
	digest := sha256.Sum256([]byte(c.Value))
	now := w.now()

	w.mu.Lock()
	s, ok := w.outputShareSessions[digest]
	if !ok {
		w.mu.Unlock()
		return "", outputShareErrMissing, false
	}
	rec, exists := w.outputShares[s.shareID]
	if !exists {
		delete(w.outputShareSessions, digest)
		w.mu.Unlock()
		w.persistOutputShareSessions()
		w.emitOutputSharesChange(w.OutputSharesState())
		return "", outputShareErrRevoked, false
	}
	status := computeOutputShareStatus(rec, now)
	if status != "active" || !now.Before(s.expiresAt) {
		delete(w.outputShareSessions, digest)
		w.mu.Unlock()
		w.persistOutputShareSessions()
		w.emitOutputSharesChange(w.OutputSharesState())
		if status == "revoked" {
			return "", outputShareErrRevoked, false
		}
		return "", outputShareErrExpired, false
	}

	s.lastSeen = now
	s.expiresAt = computeOutputShareSessionExpiry(rec, now)
	needsRefresh := rw != nil && now.Sub(s.cookieRefreshedAt) >= sessionRenewalInterval
	if needsRefresh {
		s.cookieRefreshedAt = now
	}
	w.outputShareSessions[digest] = s
	shareID := s.shareID
	exp := s.expiresAt
	w.mu.Unlock()

	if needsRefresh {
		w.setOutputShareCookie(rw, r, c.Value, exp, now)
		w.persistOutputShareSessions()
	}
	return shareID, "", true
}

func (w *ColabOneWebConsole) checkOutputShareRateLimitLocked(bucketKey string, now time.Time) int {
	cutoff := now.Add(-outputShareRateLimitWindow)
	maxSecs := 0

	w.globalOutputShareAuthFailures = pruneTimestamps(w.globalOutputShareAuthFailures, cutoff)
	if len(w.globalOutputShareAuthFailures)+w.outputShareUnlockInflightGlobal >= outputShareRateLimitMaxGlobal {
		secs := 2
		if len(w.globalOutputShareAuthFailures) > 0 {
			wait := w.globalOutputShareAuthFailures[0].Add(outputShareRateLimitWindow).Sub(now)
			secs = int(math.Ceil(wait.Seconds()))
			if secs < 1 {
				secs = 1
			}
		}
		if secs > maxSecs {
			maxSecs = secs
		}
	}

	times := pruneTimestamps(w.outputShareAuthFailures[bucketKey], cutoff)
	if len(times) == 0 {
		delete(w.outputShareAuthFailures, bucketKey)
	} else {
		w.outputShareAuthFailures[bucketKey] = times
	}
	inflightForBucket := w.outputShareUnlockInflightIP[bucketKey]
	if len(times)+inflightForBucket >= outputShareRateLimitMaxPerIP {
		secs := 2
		if len(times) > 0 {
			wait := times[0].Add(outputShareRateLimitWindow).Sub(now)
			secs = int(math.Ceil(wait.Seconds()))
			if secs < 1 {
				secs = 1
			}
		}
		if secs > maxSecs {
			maxSecs = secs
		}
	}
	return maxSecs
}

func (w *ColabOneWebConsole) recordOutputShareFailureLocked(bucketKey string, now time.Time) {
	cutoff := now.Add(-outputShareRateLimitWindow)
	w.globalOutputShareAuthFailures = append(pruneTimestamps(w.globalOutputShareAuthFailures, cutoff), now)
	times := pruneTimestamps(w.outputShareAuthFailures[bucketKey], cutoff)
	times = append(times, now)
	if len(w.outputShareAuthFailures) >= maxRateLimitIPs {
		for k, v := range w.outputShareAuthFailures {
			pruned := pruneTimestamps(v, cutoff)
			if len(pruned) == 0 {
				delete(w.outputShareAuthFailures, k)
			} else {
				w.outputShareAuthFailures[k] = pruned
			}
		}
		if len(w.outputShareAuthFailures) >= maxRateLimitIPs {
			var oldestKey string
			var oldestTime time.Time
			first := true
			for k, v := range w.outputShareAuthFailures {
				last := v[len(v)-1]
				if first || last.Before(oldestTime) {
					oldestKey = k
					oldestTime = last
					first = false
				}
			}
			if !first {
				delete(w.outputShareAuthFailures, oldestKey)
			}
		}
	}
	w.outputShareAuthFailures[bucketKey] = times
}

func (w *ColabOneWebConsole) checkOutputShareMintRateLimitLocked(ip string, now time.Time) int {
	if w.outputShareMintAttempts == nil {
		return 0
	}
	cutoff := now.Add(-outputShareMintRateLimitWindow)
	times := pruneTimestamps(w.outputShareMintAttempts[ip], cutoff)
	if len(times) == 0 {
		delete(w.outputShareMintAttempts, ip)
		return 0
	}
	w.outputShareMintAttempts[ip] = times
	if len(times) >= outputShareMintRateLimitMaxPerIP {
		wait := times[0].Add(outputShareMintRateLimitWindow).Sub(now)
		secs := int(math.Ceil(wait.Seconds()))
		if secs < 1 {
			secs = 1
		}
		return secs
	}
	return 0
}

func (w *ColabOneWebConsole) checkAndRecordOutputShareMintLocked(ip string, now time.Time) int {
	if w.outputShareMintAttempts == nil {
		w.outputShareMintAttempts = make(map[string][]time.Time)
	}
	cutoff := now.Add(-outputShareMintRateLimitWindow)
	times := pruneTimestamps(w.outputShareMintAttempts[ip], cutoff)
	if len(times) >= outputShareMintRateLimitMaxPerIP {
		w.outputShareMintAttempts[ip] = times
		wait := times[0].Add(outputShareMintRateLimitWindow).Sub(now)
		secs := int(math.Ceil(wait.Seconds()))
		if secs < 1 {
			secs = 1
		}
		return secs
	}
	times = append(times, now)
	if len(w.outputShareMintAttempts) >= maxRateLimitIPs {
		for k, v := range w.outputShareMintAttempts {
			pruned := pruneTimestamps(v, cutoff)
			if len(pruned) == 0 {
				delete(w.outputShareMintAttempts, k)
			} else {
				w.outputShareMintAttempts[k] = pruned
			}
		}
		if len(w.outputShareMintAttempts) >= maxRateLimitIPs {
			var oldestIP string
			var oldestTime time.Time
			first := true
			for k, v := range w.outputShareMintAttempts {
				last := v[len(v)-1]
				if first || last.Before(oldestTime) {
					oldestIP = k
					oldestTime = last
					first = false
				}
			}
			if !first {
				delete(w.outputShareMintAttempts, oldestIP)
			}
		}
	}
	w.outputShareMintAttempts[ip] = times
	return 0
}

// --- Guest Gallery HTTP Handlers ---

func (w *ColabOneWebConsole) handleOutputShareErrorPage(rw http.ResponseWriter, r *http.Request, status int, reason string) {
	nonce, err := generateBase64Token(16)
	if err != nil {
		nonce = "colabone"
	}
	locale := w.resolveGuestLocale(r)
	htmlDoc := renderColabOneOutputShareErrorPage(locale, reason, nonce)

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

func (w *ColabOneWebConsole) handleOutputShareUnlockPage(rw http.ResponseWriter, r *http.Request, status int, challenge, errCode string) {
	nonce, err := generateBase64Token(16)
	if err != nil {
		nonce = "colabone"
	}
	locale := w.resolveGuestLocale(r)
	htmlDoc := renderColabOneOutputShareUnlockPage(locale, challenge, errCode, nonce)

	csp := fmt.Sprintf(
		"default-src 'none'; style-src 'nonce-%s'; script-src 'nonce-%s'; img-src 'self' data:; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'",
		nonce, nonce,
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

func (w *ColabOneWebConsole) handleOutputShareEntry(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeMethodNotAllowed(rw, "GET, HEAD")
		return
	}

	rw.Header().Set("Referrer-Policy", "no-referrer")
	rawKey := strings.TrimSpace(r.URL.Query().Get("k"))

	if rawKey != "" && isCrossSiteSubresourceRequest(r) {
		w.handleOutputShareErrorPage(rw, r, http.StatusForbidden, outputShareErrInvalid)
		return
	}

	// Messaging link-preview crawlers, browser prefetchers, and HEAD probes with ?k=
	// must never trigger rate limits or mint sessions.
	if isLinkPreviewRequest(r) || (r.Method == http.MethodHead && rawKey != "") {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		rw.Header().Set("Cache-Control", "no-store")
		rw.Header().Set("X-Robots-Tag", "noindex, nofollow")
		rw.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(rw, `<!doctype html><html><head><meta charset="utf-8"><meta name="robots" content="noindex,nofollow"><title>ColabOne</title></head><body>ColabOne Output Gallery</body></html>`)
		}
		return
	}

	// Case A: Visiting with ?k=co_gal_...
	if rawKey != "" {
		// If browser already holds a valid session for the exact same gallery, redirect cleanly without re-prompting.
		if curID, _, hasSession := w.authenticateOutputShareRequestWithReason(nil, r); hasSession {
			if candidateID, ok := extractOutputShareIDFromKey(rawKey); ok && candidateID == curID {
				_, _ = w.authenticateOutputShareRequest(rw, r)
				rw.Header().Set("Cache-Control", "no-store")
				http.Redirect(rw, r, OutputShareRoutePath, http.StatusFound)
				return
			}
		}

		ip := w.clientIPForRateLimit(r)
		now := w.now()
		token, tokenErr := generateBase64Token(32)

		var existingDigest [32]byte
		hasExistingCookie := false
		if c, err := r.Cookie(OutputShareCookieName); err == nil && c.Value != "" {
			existingDigest = sha256.Sum256([]byte(c.Value))
			hasExistingCookie = true
		}

		w.mu.Lock()
		if w.closed || w.stopping {
			w.mu.Unlock()
			w.handleOutputShareErrorPage(rw, r, http.StatusServiceUnavailable, outputShareErrInvalid)
			return
		}
		if retryAfter := w.checkOutputShareRateLimitLocked(ip, now); retryAfter > 0 {
			w.mu.Unlock()
			rw.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			w.handleOutputShareErrorPage(rw, r, http.StatusTooManyRequests, outputShareErrRateLimited)
			return
		}
		shareID, reason, ok := w.verifyOutputShareKeyLocked(rawKey, now)
		if !ok {
			w.recordOutputShareFailureLocked(ip, now)
			w.mu.Unlock()
			if reason == outputShareErrExpired {
				w.pruneExpiredOutputSharePasswords()
			}
			status := http.StatusUnauthorized
			if reason == outputShareErrExpired || reason == outputShareErrRevoked {
				status = http.StatusGone
			}
			w.handleOutputShareErrorPage(rw, r, status, reason)
			return
		}
		rec := w.outputShares[shareID]

		if rec.HasPassword {
			hmacKeyCopy := append([]byte(nil), w.outputShareHMACKey...)
			nonceB64 := rec.NonceB64
			w.mu.Unlock()

			challenge, chalErr := generateOutputShareUnlockChallenge(hmacKeyCopy, shareID, nonceB64, now)
			if chalErr != nil {
				http.Error(rw, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			w.handleOutputShareUnlockPage(rw, r, http.StatusOK, challenge, "")
			return
		}

		if retryAfter := w.checkAndRecordOutputShareMintLocked(ip, now); retryAfter > 0 {
			w.mu.Unlock()
			rw.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			w.handleOutputShareErrorPage(rw, r, http.StatusTooManyRequests, outputShareErrRateLimited)
			return
		}

		if tokenErr != nil {
			w.mu.Unlock()
			http.Error(rw, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		var prevSession colabOneOutputShareSession
		hadPrevSession := false
		if hasExistingCookie {
			prevSession, hadPrevSession = w.outputShareSessions[existingDigest]
			delete(w.outputShareSessions, existingDigest)
		}
		exp, stored := w.storeOutputShareSessionLocked(token, rec, now)
		if !stored {
			if hadPrevSession {
				w.outputShareSessions[existingDigest] = prevSession
			}
			w.mu.Unlock()
			w.handleOutputShareErrorPage(rw, r, http.StatusTooManyRequests, outputShareErrSessionLimit)
			return
		}
		w.mu.Unlock()

		w.persistOutputShareSessions()
		w.setOutputShareCookie(rw, r, token, exp, now)
		w.emitOutputSharesChange(w.OutputSharesState())
		rw.Header().Set("Cache-Control", "no-store")
		http.Redirect(rw, r, OutputShareRoutePath, http.StatusFound)
		return
	}

	// Case B: Visiting /gallery or /gallery/* without ?k= validates cookie and serves SPA.
	if _, reason, ok := w.authenticateOutputShareRequestWithReason(rw, r); !ok {
		status := http.StatusUnauthorized
		if reason == outputShareErrExpired || reason == outputShareErrRevoked {
			status = http.StatusGone
		}
		if reason == outputShareErrExpired {
			w.pruneExpiredOutputSharePasswords()
		}
		w.handleOutputShareErrorPage(rw, r, status, reason)
		return
	}
	if r.URL.Path != OutputShareRoutePath || r.URL.RawQuery != "" {
		rw.Header().Set("Cache-Control", "no-store")
		http.Redirect(rw, r, OutputShareRoutePath, http.StatusFound)
		return
	}
	w.serveFrontendIndex(rw, r)
}

func (w *ColabOneWebConsole) handleOutputShareUnlock(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(rw, "POST")
		return
	}
	rw.Header().Set("Referrer-Policy", "no-referrer")
	rw.Header().Set("Cache-Control", "no-store")

	_, copySet := resolveOutputShareUnlockCopy(w.resolveGuestLocale(r))
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	accept := strings.ToLower(r.Header.Get("Accept"))
	wantsJSON := strings.Contains(accept, "application/json") || strings.Contains(ct, "application/json")

	if !w.validateSameOriginRequest(r, false) {
		if wantsJSON {
			writeJSONError(rw, http.StatusForbidden, copySet.forbiddenError)
		} else {
			w.handleOutputShareErrorPage(rw, r, http.StatusForbidden, outputShareErrInvalid)
		}
		return
	}

	r.Body = http.MaxBytesReader(rw, r.Body, maxOutputShareUnlockBodyBytes)
	var challenge, password string
	if strings.Contains(ct, "application/json") {
		var body struct {
			Challenge string `json:"challenge"`
			Token     string `json:"token"`
			Password  string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			if wantsJSON {
				writeJSONError(rw, http.StatusBadRequest, copySet.genericError)
			} else {
				w.handleOutputShareErrorPage(rw, r, http.StatusBadRequest, outputShareErrInvalid)
			}
			return
		}
		challenge = strings.TrimSpace(body.Challenge)
		if challenge == "" {
			challenge = strings.TrimSpace(body.Token)
		}
		password = strings.TrimSpace(body.Password)
	} else {
		if err := r.ParseForm(); err != nil {
			if wantsJSON {
				writeJSONError(rw, http.StatusBadRequest, copySet.genericError)
			} else {
				w.handleOutputShareErrorPage(rw, r, http.StatusBadRequest, outputShareErrInvalid)
			}
			return
		}
		challenge = strings.TrimSpace(r.PostFormValue("challenge"))
		if challenge == "" {
			challenge = strings.TrimSpace(r.PostFormValue("token"))
		}
		password = strings.TrimSpace(r.PostFormValue("password"))
	}

	ip := w.clientIPForRateLimit(r)
	now := w.now()

	w.mu.Lock()
	if w.closed || w.stopping {
		w.mu.Unlock()
		if wantsJSON {
			writeJSONError(rw, http.StatusServiceUnavailable, copySet.genericError)
		} else {
			w.handleOutputShareErrorPage(rw, r, http.StatusServiceUnavailable, outputShareErrInvalid)
		}
		return
	}
	if retryAfter := w.checkOutputShareRateLimitLocked(ip, now); retryAfter > 0 {
		w.mu.Unlock()
		rw.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		if wantsJSON {
			writeJSON(rw, http.StatusTooManyRequests, map[string]any{
				"error":       copySet.rateLimitError,
				"retry_after": retryAfter,
			})
		} else {
			w.handleOutputShareErrorPage(rw, r, http.StatusTooManyRequests, outputShareErrRateLimited)
		}
		return
	}
	rec, reason, ok := w.verifyOutputShareUnlockChallengeLocked(challenge, now)
	if !ok {
		if reason == outputShareErrInvalid {
			w.recordOutputShareFailureLocked(ip, now)
		}
		w.mu.Unlock()
		if reason == outputShareErrExpired {
			w.pruneExpiredOutputSharePasswords()
		}
		status := http.StatusUnauthorized
		if reason == outputShareErrExpired || reason == outputShareErrRevoked || reason == outputShareErrChallengeExpired {
			status = http.StatusGone
		}
		if wantsJSON {
			msg := copySet.genericError
			switch reason {
			case outputShareErrChallengeExpired:
				msg = copySet.expiredChallengeErr
			case outputShareErrExpired:
				msg = copySet.shareExpiredErr
			case outputShareErrRevoked:
				msg = copySet.shareRevokedErr
			}
			writeJSONError(rw, status, msg)
		} else {
			w.handleOutputShareErrorPage(rw, r, status, reason)
		}
		return
	}
	shareID := rec.ID
	saltB64 := rec.PasswordSaltB64
	hashB64 := rec.PasswordHashB64

	if password == "" {
		w.mu.Unlock()
		if wantsJSON {
			writeJSONError(rw, http.StatusUnauthorized, copySet.emptyPasswordError)
		} else {
			w.handleOutputShareUnlockPage(rw, r, http.StatusUnauthorized, challenge, "empty_password")
		}
		return
	}

	// Scope password-failure buckets by (ip, shareID) and reserve an in-flight slot under w.mu
	// before running scrypt so parallel requests cannot bypass the 5-failure limit via TOCTOU.
	rateKey := ip + "|" + shareID
	if retryAfter := w.checkOutputShareRateLimitLocked(rateKey, now); retryAfter > 0 {
		w.mu.Unlock()
		rw.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		if wantsJSON {
			writeJSON(rw, http.StatusTooManyRequests, map[string]any{
				"error":       copySet.rateLimitError,
				"retry_after": retryAfter,
			})
		} else {
			w.handleOutputShareErrorPage(rw, r, http.StatusTooManyRequests, outputShareErrRateLimited)
		}
		return
	}
	if retryAfter := w.checkOutputShareMintRateLimitLocked(ip, now); retryAfter > 0 {
		w.mu.Unlock()
		rw.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		if wantsJSON {
			writeJSON(rw, http.StatusTooManyRequests, map[string]any{
				"error":       copySet.rateLimitError,
				"retry_after": retryAfter,
			})
		} else {
			w.handleOutputShareErrorPage(rw, r, http.StatusTooManyRequests, outputShareErrRateLimited)
		}
		return
	}

	semAcquired := false
	if w.outputShareUnlockSem != nil {
		select {
		case w.outputShareUnlockSem <- struct{}{}:
			semAcquired = true
		default:
			w.mu.Unlock()
			rw.Header().Set("Retry-After", "2")
			if wantsJSON {
				writeJSON(rw, http.StatusTooManyRequests, map[string]any{
					"error":       copySet.rateLimitError,
					"retry_after": 2,
				})
			} else {
				w.handleOutputShareErrorPage(rw, r, http.StatusTooManyRequests, outputShareErrRateLimited)
			}
			return
		}
	}

	if w.outputShareUnlockInflightIP == nil {
		w.outputShareUnlockInflightIP = make(map[string]int)
	}
	w.outputShareUnlockInflightIP[rateKey]++
	w.outputShareUnlockInflightGlobal++
	inflightReserved := true
	w.mu.Unlock()

	releaseInflightLocked := func() {
		if !inflightReserved {
			return
		}
		inflightReserved = false
		if w.outputShareUnlockInflightGlobal > 0 {
			w.outputShareUnlockInflightGlobal--
		}
		if cur := w.outputShareUnlockInflightIP[rateKey]; cur <= 1 {
			delete(w.outputShareUnlockInflightIP, rateKey)
		} else {
			w.outputShareUnlockInflightIP[rateKey] = cur - 1
		}
	}
	defer func() {
		if inflightReserved {
			w.mu.Lock()
			releaseInflightLocked()
			w.mu.Unlock()
		}
		if semAcquired {
			<-w.outputShareUnlockSem
		}
	}()

	// Run scrypt verification strictly outside w.mu.
	matched := verifyOutputSharePasswordHash(password, saltB64, hashB64)
	if !matched {
		w.mu.Lock()
		releaseInflightLocked()
		w.recordOutputShareFailureLocked(rateKey, now)
		w.mu.Unlock()
		if wantsJSON {
			writeJSONError(rw, http.StatusUnauthorized, copySet.invalidPasswordError)
		} else {
			w.handleOutputShareUnlockPage(rw, r, http.StatusUnauthorized, challenge, "invalid_password")
		}
		return
	}

	token, tokenErr := generateBase64Token(32)
	if tokenErr != nil {
		if wantsJSON {
			writeJSONError(rw, http.StatusInternalServerError, copySet.genericError)
		} else {
			http.Error(rw, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	var existingDigest [32]byte
	hasExistingCookie := false
	if c, err := r.Cookie(OutputShareCookieName); err == nil && c.Value != "" {
		existingDigest = sha256.Sum256([]byte(c.Value))
		hasExistingCookie = true
	}

	w.mu.Lock()
	releaseInflightLocked()
	if w.closed || w.stopping {
		w.mu.Unlock()
		if wantsJSON {
			writeJSONError(rw, http.StatusServiceUnavailable, copySet.genericError)
		} else {
			w.handleOutputShareErrorPage(rw, r, http.StatusServiceUnavailable, outputShareErrInvalid)
		}
		return
	}
	curRec, stillExists := w.outputShares[shareID]
	if !stillExists || computeOutputShareStatus(curRec, now) != "active" || curRec.PasswordHashB64 != hashB64 {
		curStatus := ""
		if stillExists {
			curStatus = computeOutputShareStatus(curRec, now)
		}
		w.mu.Unlock()
		if curStatus == "expired" {
			w.pruneExpiredOutputSharePasswords()
		}
		if wantsJSON {
			msg := copySet.shareRevokedErr
			if curStatus == "expired" {
				msg = copySet.shareExpiredErr
			}
			writeJSONError(rw, http.StatusGone, msg)
		} else {
			errReason := outputShareErrRevoked
			if curStatus == "expired" {
				errReason = outputShareErrExpired
			}
			w.handleOutputShareErrorPage(rw, r, http.StatusGone, errReason)
		}
		return
	}
	delete(w.outputShareAuthFailures, rateKey)
	if retryAfter := w.checkAndRecordOutputShareMintLocked(ip, now); retryAfter > 0 {
		w.mu.Unlock()
		rw.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		if wantsJSON {
			writeJSON(rw, http.StatusTooManyRequests, map[string]any{
				"error":       copySet.rateLimitError,
				"retry_after": retryAfter,
			})
		} else {
			w.handleOutputShareErrorPage(rw, r, http.StatusTooManyRequests, outputShareErrRateLimited)
		}
		return
	}
	var prevSession colabOneOutputShareSession
	hadPrevSession := false
	if hasExistingCookie {
		prevSession, hadPrevSession = w.outputShareSessions[existingDigest]
		delete(w.outputShareSessions, existingDigest)
	}
	exp, stored := w.storeOutputShareSessionLocked(token, curRec, now)
	if !stored {
		if hadPrevSession {
			w.outputShareSessions[existingDigest] = prevSession
		}
		w.mu.Unlock()
		if wantsJSON {
			writeJSONError(rw, http.StatusTooManyRequests, copySet.sessionLimitError)
		} else {
			w.handleOutputShareErrorPage(rw, r, http.StatusTooManyRequests, outputShareErrSessionLimit)
		}
		return
	}
	w.mu.Unlock()

	w.persistOutputShareSessions()
	w.setOutputShareCookie(rw, r, token, exp, now)
	w.emitOutputSharesChange(w.OutputSharesState())

	if wantsJSON {
		writeJSON(rw, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	http.Redirect(rw, r, OutputShareRoutePath, http.StatusFound)
}

func (w *ColabOneWebConsole) handleOutputShareManifest(rw http.ResponseWriter, r *http.Request, shareID string) {
	if r.Header.Get(ClientHeaderName) != OutputShareClientHeaderValue {
		writeJSONError(rw, http.StatusForbidden, "非法客户端请求")
		return
	}

	now := w.now()
	w.mu.RLock()
	rec, exists := w.outputShares[shareID]
	if !exists || computeOutputShareStatus(rec, now) != "active" {
		w.mu.RUnlock()
		writeJSONError(rw, http.StatusUnauthorized, "分享会话已失效")
		return
	}
	recCopy := cloneOutputShareRecord(rec)
	w.mu.RUnlock()

	snap := w.fetchSnapshotMap(r.Context())
	runtimeReady := false
	liveByID := make(map[string]map[string]any)
	if snap != nil {
		runtimeReady = strings.TrimSpace(shareAsString(shareAsMap(snap["runtime"])["state"])) == "ready"
		for _, rawOut := range shareAsSlice(snap["comfy_outputs"]) {
			outMap := shareAsMap(rawOut)
			if outMap == nil {
				continue
			}
			oid := strings.ToLower(strings.TrimSpace(shareAsString(outMap["id"])))
			if outputIDRe.MatchString(oid) {
				liveByID[oid] = outMap
			}
		}
	}

	manifestGroups := make([]ColabOneOutputShareManifestGroup, 0, len(recCopy.Groups))
	totalItems := 0
	totalAvailable := 0

	for _, g := range recCopy.Groups {
		items := make([]ColabOneOutputShareManifestItem, 0, len(g.OutputIDs))
		groupAvailable := 0
		for _, outID := range g.OutputIDs {
			liveOut := liveByID[outID]
			frozen, hasFrozen := recCopy.Items[outID]

			var item ColabOneOutputShareManifestItem
			if liveOut != nil {
				snapItem := extractSanitizedItemSnapshot(outID, liveOut)
				item = ColabOneOutputShareManifestItem{
					ID:           outID,
					Filename:     snapItem.Filename,
					Extension:    snapItem.Extension,
					Kind:         snapItem.Kind,
					Mime:         snapItem.Mime,
					Size:         snapItem.Size,
					WorkflowID:   snapItem.WorkflowID,
					WorkflowName: snapItem.WorkflowName,
					CreatedAt:    snapItem.CreatedAt,
					TaskID:       snapItem.TaskID,
					IsInspect:    snapItem.IsInspect,
					Available:    isLiveOutputAvailableForGallery(liveOut, runtimeReady),
				}
				if hasFrozen {
					if item.WorkflowID == "" {
						item.WorkflowID = frozen.WorkflowID
					}
					if item.WorkflowName == "" {
						item.WorkflowName = frozen.WorkflowName
					}
					if item.TaskID == "" {
						item.TaskID = frozen.TaskID
					}
					if item.CreatedAt == 0 {
						item.CreatedAt = frozen.CreatedAt
					}
				}
			} else if hasFrozen {
				ext, kind, mimeType := canonicalizeOutputExtKindMIME(frozen.Extension, frozen.Filename, frozen.Kind, frozen.Mime)
				fname := frozen.Filename
				if fname == "" {
					if ext != "" {
						fname = fmt.Sprintf("ColabOne-%s.%s", outID[:8], ext)
					} else {
						fname = fmt.Sprintf("ColabOne-%s", outID[:8])
					}
				}
				item = ColabOneOutputShareManifestItem{
					ID:           outID,
					Filename:     fname,
					Extension:    ext,
					Kind:         kind,
					Mime:         mimeType,
					Size:         frozen.Size,
					WorkflowID:   frozen.WorkflowID,
					WorkflowName: frozen.WorkflowName,
					CreatedAt:    frozen.CreatedAt,
					TaskID:       frozen.TaskID,
					IsInspect:    frozen.IsInspect || isOutputShareInspectFilename(fname),
					Available:    false,
				}
			} else {
				item = ColabOneOutputShareManifestItem{
					ID:        outID,
					Filename:  fmt.Sprintf("ColabOne-%s", outID[:8]),
					Kind:      "image",
					Mime:      "application/octet-stream",
					Available: false,
				}
			}

			if item.Available {
				groupAvailable++
			}
			items = append(items, item)
		}
		totalItems += len(items)
		totalAvailable += groupAvailable
		manifestGroups = append(manifestGroups, ColabOneOutputShareManifestGroup{
			ID:             g.ID,
			Title:          g.Title,
			WorkflowID:     g.WorkflowID,
			WorkflowName:   g.WorkflowName,
			TaskID:         g.TaskID,
			ItemCount:      len(items),
			AvailableCount: groupAvailable,
			Items:          items,
		})
	}

	manifest := ColabOneOutputShareManifest{
		Runtime:        OutputShareClientHeaderValue,
		Version:        w.opts.AppVersion,
		ShareID:        recCopy.ID,
		Title:          recCopy.Title,
		AllowDownload:  recCopy.AllowDownload,
		CreatedAt:      recCopy.CreatedAt,
		ExpiresAt:      recCopy.ExpiresAt,
		GroupCount:     len(manifestGroups),
		ItemCount:      totalItems,
		AvailableCount: totalAvailable,
		Groups:         manifestGroups,
	}

	rawBytes, err := encodeJSONNoEscape(manifest)
	if err != nil {
		writeJSONError(rw, http.StatusInternalServerError, "序列化相册清单失败")
		return
	}
	writeSnapshotBytes(rw, r, rawBytes)
}

func (w *ColabOneWebConsole) lookupOutputShareRecordForItem(shareID, outID string) (colabOneOutputShareRecord, bool, int, string) {
	cleanID := strings.ToLower(strings.TrimSpace(outID))
	if !outputIDRe.MatchString(cleanID) {
		return colabOneOutputShareRecord{}, false, http.StatusBadRequest, "输出文件 ID 无效"
	}
	now := w.now()
	w.mu.RLock()
	rec, exists := w.outputShares[shareID]
	if !exists || computeOutputShareStatus(rec, now) != "active" {
		w.mu.RUnlock()
		return colabOneOutputShareRecord{}, false, http.StatusUnauthorized, "分享会话已失效"
	}
	allowed := stringSliceContains(rec.OutputIDs, cleanID)
	var recCopy colabOneOutputShareRecord
	if allowed {
		recCopy = cloneOutputShareRecord(rec)
	}
	w.mu.RUnlock()
	if !allowed {
		return colabOneOutputShareRecord{}, false, http.StatusForbidden, "无权访问该作品文件"
	}
	return recCopy, true, 0, ""
}

func (w *ColabOneWebConsole) handleOutputShareMedia(rw http.ResponseWriter, r *http.Request, shareID, id string) {
	rw.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	if !isSameOriginDownloadRequest(r) {
		writeHeadAwareJSONError(rw, r, http.StatusForbidden, "跨源请求已被拒绝")
		return
	}
	cleanID := strings.ToLower(strings.TrimSpace(id))
	_, ok, status, msg := w.lookupOutputShareRecordForItem(shareID, cleanID)
	if !ok {
		writeHeadAwareJSONError(rw, r, status, msg)
		return
	}
	if w.opts.ServeOutputShareMedia == nil {
		writeHeadAwareJSONError(rw, r, http.StatusServiceUnavailable, "作品暂离线或暂不可访问，请稍后再试")
		return
	}
	rw.Header().Set("X-Content-Type-Options", "nosniff")
	rw.Header().Set("Cache-Control", "no-store")
	rw.Header().Set("Referrer-Policy", "no-referrer")
	w.opts.ServeOutputShareMedia(r.Context(), rw, r, cleanID)
}

func appendAllowlistedOutputExtension(baseName, rawExt string) string {
	cleanExt := cleanAllowlistedOutputExt(rawExt)
	if cleanExt == "" {
		return baseName
	}
	existingExt := strings.ToLower(strings.TrimPrefix(filepath.Ext(baseName), "."))
	if strings.EqualFold(existingExt, cleanExt) {
		return baseName
	}
	if existingMIME, ok := allowedOutputMIME[existingExt]; ok && existingMIME == allowedOutputMIME[cleanExt] {
		return baseName
	}
	suffix := "." + cleanExt
	maxStemBytes := maxArchiveSegmentBytes - len(suffix)
	stem := baseName
	if len(stem) > maxStemBytes {
		stem = strings.TrimRightFunc(truncateUTF8RunesAndBytes(stem, 96, maxStemBytes), func(r rune) bool {
			return r == '.' || unicode.IsSpace(r)
		})
		if stem == "" || stem == "." || stem == ".." {
			stem = "ColabOne"
		}
	}
	return stem + suffix
}

type attachmentMediaResponseWriter struct {
	rw             http.ResponseWriter
	baseName       string
	headersWritten bool
}

func (a *attachmentMediaResponseWriter) Header() http.Header {
	return a.rw.Header()
}

func (a *attachmentMediaResponseWriter) WriteHeader(code int) {
	if !a.headersWritten {
		a.headersWritten = true
		if code == http.StatusOK || code == http.StatusPartialContent {
			finalName := a.baseName
			existingExt := strings.ToLower(strings.TrimPrefix(filepath.Ext(finalName), "."))
			if _, ok := allowedOutputMIME[existingExt]; !ok {
				if inferred := extensionForAllowedOutputMIME(a.rw.Header().Get("Content-Type")); inferred != "" {
					finalName = appendAllowlistedOutputExtension(finalName, inferred)
				}
			}
			a.rw.Header().Set("Content-Disposition", buildAttachmentContentDisposition(finalName))
			a.rw.Header().Set("X-Content-Type-Options", "nosniff")
			a.rw.Header().Set("Cache-Control", "no-store")
			a.rw.Header().Set("Referrer-Policy", "no-referrer")
		}
	}
	a.rw.WriteHeader(code)
}

func (a *attachmentMediaResponseWriter) Write(p []byte) (int, error) {
	if !a.headersWritten {
		a.WriteHeader(http.StatusOK)
	}
	return a.rw.Write(p)
}

func (a *attachmentMediaResponseWriter) Flush() {
	if f, ok := a.rw.(http.Flusher); ok {
		f.Flush()
	}
}

type lazyDownloadResponseWriter struct {
	rw             http.ResponseWriter
	flusher        http.Flusher
	fallbackName   string
	fallbackMime   string
	resolvedName   string
	resolvedMime   string
	resolvedSize   int64
	headersWritten bool
}

func (l *lazyDownloadResponseWriter) SetColabOneOutputMeta(id, filename, ext, mimeType string, size int64) {
	if mimeType != "" {
		l.resolvedMime = mimeType
	}
	if size > 0 {
		l.resolvedSize = size
	}
	base := l.fallbackName
	if base == "" && filename != "" {
		base = sanitizeArchiveSegment(filename, "ColabOne-download")
	}
	cleanExt := cleanAllowlistedOutputExt(ext)
	if cleanExt == "" {
		cleanExt = extensionForAllowedOutputMIME(mimeType)
	}
	if base != "" && cleanExt != "" {
		l.resolvedName = appendAllowlistedOutputExtension(base, cleanExt)
	} else if l.fallbackName == "" && base != "" {
		l.resolvedName = base
	}
}

func (l *lazyDownloadResponseWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if !l.headersWritten {
		l.headersWritten = true
		name := l.resolvedName
		if name == "" {
			name = l.fallbackName
		}
		mimeType := l.resolvedMime
		if mimeType == "" {
			mimeType = l.fallbackMime
		}
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		if existingExt := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")); allowedOutputMIME[existingExt] == "" {
			if inferred := extensionForAllowedOutputMIME(mimeType); inferred != "" {
				name = appendAllowlistedOutputExtension(name, inferred)
			}
		}
		h := l.rw.Header()
		h.Set("Content-Type", mimeType)
		h.Set("Content-Disposition", buildAttachmentContentDisposition(name))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		if l.resolvedSize > 0 {
			h.Set("Content-Length", strconv.FormatInt(l.resolvedSize, 10))
		}
		l.rw.WriteHeader(http.StatusOK)
	}
	n, err := l.rw.Write(p)
	if err == nil && l.flusher != nil {
		l.flusher.Flush()
	}
	return n, err
}

func (w *ColabOneWebConsole) handleOutputShareDownload(rw http.ResponseWriter, r *http.Request, shareID, id string) {
	if !isSameOriginDownloadRequest(r) {
		writeHeadAwareJSONError(rw, r, http.StatusForbidden, "跨源请求已被拒绝")
		return
	}
	cleanID := strings.ToLower(strings.TrimSpace(id))
	rec, ok, status, msg := w.lookupOutputShareRecordForItem(shareID, cleanID)
	if !ok {
		writeHeadAwareJSONError(rw, r, status, msg)
		return
	}
	if !rec.AllowDownload {
		writeHeadAwareJSONError(rw, r, http.StatusForbidden, "该分享链接未开启下载权限")
		return
	}

	fallbackName := fmt.Sprintf("ColabOne-%s", cleanID[:8])
	fallbackExt := ""
	fallbackMime := "application/octet-stream"
	if item, exists := rec.Items[cleanID]; exists {
		fallbackExt = cleanAllowlistedOutputExt(item.Extension)
		if strings.TrimSpace(item.Filename) != "" {
			fallbackName = item.Filename
		} else if fallbackExt != "" {
			fallbackName = fmt.Sprintf("ColabOne-%s.%s", cleanID[:8], fallbackExt)
		}
		if item.Mime != "" {
			fallbackMime = item.Mime
		}
	}
	// Prefer live filename/extension if available in snapshot.
	if snap := w.fetchSnapshotMap(r.Context()); snap != nil {
		for _, rawOut := range shareAsSlice(snap["comfy_outputs"]) {
			outMap := shareAsMap(rawOut)
			if outMap == nil {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(shareAsString(outMap["id"])), cleanID) {
				snapItem := extractSanitizedItemSnapshot(cleanID, outMap)
				if snapItem.Filename != "" {
					fallbackName = snapItem.Filename
				}
				if snapItem.Extension != "" {
					fallbackExt = cleanAllowlistedOutputExt(snapItem.Extension)
				}
				if snapItem.Mime != "" {
					fallbackMime = snapItem.Mime
				}
				break
			}
		}
	}

	safeName := sanitizeArchiveSegment(fallbackName, fmt.Sprintf("ColabOne-%s", cleanID[:8]))
	if fallbackExt == "" {
		fallbackExt = extensionForAllowedOutputMIME(fallbackMime)
	}
	if fallbackExt != "" {
		safeName = appendAllowlistedOutputExtension(safeName, fallbackExt)
	}
	if w.opts.ServeOutputShareMedia != nil {
		wrapped := &attachmentMediaResponseWriter{
			rw:       rw,
			baseName: safeName,
		}
		w.opts.ServeOutputShareMedia(r.Context(), wrapped, r, cleanID)
		return
	}

	if w.opts.StreamOutputMedia == nil {
		writeHeadAwareJSONError(rw, r, http.StatusServiceUnavailable, "作品暂离线或暂不可访问，请稍后再试")
		return
	}
	if r.Method == http.MethodHead {
		h := rw.Header()
		h.Set("Content-Type", fallbackMime)
		h.Set("Content-Disposition", buildAttachmentContentDisposition(safeName))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		rw.WriteHeader(http.StatusOK)
		return
	}

	flusher, _ := rw.(http.Flusher)
	lw := &lazyDownloadResponseWriter{
		rw:           rw,
		flusher:      flusher,
		fallbackName: safeName,
		fallbackMime: fallbackMime,
	}
	_, err := w.opts.StreamOutputMedia(r.Context(), cleanID, lw, true)
	if err != nil {
		if lw.headersWritten {
			w.logActivity("成果物分享下载流式传输中断，已中止响应", "warn")
			panic(http.ErrAbortHandler)
		}
		errStatus, errMsg := classifyStreamMediaError(err, true)
		writeHeadAwareJSONError(rw, r, errStatus, errMsg)
	}
}

// --- Streaming ZIP Helpers (Shared by Guest Gallery & Host Web Console) ---

type colabOneZipItemSpec struct {
	OutputID  string
	Folder    string
	Filename  string
	Extension string
	Size      int64
}

type limitedCountingWriter struct {
	w        io.Writer
	limit    int64
	written  int64
	exceeded bool
}

func (l *limitedCountingWriter) Write(p []byte) (int, error) {
	if l.exceeded {
		return 0, errOutputZipSizeLimitExceeded
	}
	if l.limit > 0 && l.written+int64(len(p)) > l.limit {
		l.exceeded = true
		return 0, errOutputZipSizeLimitExceeded
	}
	n, err := l.w.Write(p)
	l.written += int64(n)
	return n, err
}

type lazyHTTPZipState struct {
	rw             http.ResponseWriter
	flusher        http.Flusher
	zipFilename    string
	now            time.Time
	headersSent    bool
	limitWriter    *limitedCountingWriter
	zipWriter      *zip.Writer
	usedEntryPaths map[string]bool
	writtenItems   int
}

func (s *lazyHTTPZipState) allocateUniqueEntryPath(folder, rawName, fallbackID, ext string) string {
	base := sanitizeArchiveSegment(rawName, fmt.Sprintf("ColabOne-%s", fallbackID[:8]))
	if cleanExt := cleanAllowlistedOutputExt(ext); cleanExt != "" {
		base = appendAllowlistedOutputExtension(base, cleanExt)
	}
	prefix := ""
	if folder != "" {
		prefix = folder + "/"
	}
	cand := prefix + base
	key := strings.ToLower(cand)
	if !s.usedEntryPaths[key] {
		s.usedEntryPaths[key] = true
		return cand
	}
	fileExt := filepath.Ext(base)
	stem := strings.TrimSuffix(base, fileExt)
	for i := 2; ; i++ {
		nextCand := fmt.Sprintf("%s%s (%d)%s", prefix, stem, i, fileExt)
		nextKey := strings.ToLower(nextCand)
		if !s.usedEntryPaths[nextKey] {
			s.usedEntryPaths[nextKey] = true
			return nextCand
		}
	}
}

type lazyHTTPZipEntryWriter struct {
	state           *lazyHTTPZipState
	item            colabOneZipItemSpec
	headerAttempted bool
	started         bool
	entryWriter     io.Writer
}

func (e *lazyHTTPZipEntryWriter) SetColabOneOutputMeta(id, filename, ext, mimeType string, size int64) {
	if e.item.Extension == "" {
		if cleanExt := cleanAllowlistedOutputExt(ext); cleanExt != "" {
			e.item.Extension = cleanExt
		} else if inferred := extensionForAllowedOutputMIME(mimeType); inferred != "" {
			e.item.Extension = inferred
		}
	}
	if e.item.Filename == "" && filename != "" {
		e.item.Filename = filename
	}
}

func (e *lazyHTTPZipEntryWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if !e.started {
		if !e.state.headersSent {
			h := e.state.rw.Header()
			h.Set("Content-Type", "application/zip")
			h.Set("Content-Disposition", buildAttachmentContentDisposition(e.state.zipFilename))
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Cache-Control", "no-store")
			h.Set("Referrer-Policy", "no-referrer")
			e.state.rw.WriteHeader(http.StatusOK)
			e.state.limitWriter = &limitedCountingWriter{
				w:     e.state.rw,
				limit: maxOutputZipTotalBytes,
			}
			e.state.zipWriter = zip.NewWriter(e.state.limitWriter)
			e.state.headersSent = true
		}
		entryPath := e.state.allocateUniqueEntryPath(e.item.Folder, e.item.Filename, e.item.OutputID, e.item.Extension)
		hdr := &zip.FileHeader{
			Name:     entryPath,
			Method:   zip.Store,
			Modified: e.state.now,
		}
		e.headerAttempted = true
		ew, err := e.state.zipWriter.CreateHeader(hdr)
		if err != nil {
			return 0, err
		}
		e.entryWriter = ew
		e.started = true
		e.state.writtenItems++
	}
	n, err := e.entryWriter.Write(p)
	if err == nil && e.state.flusher != nil {
		e.state.flusher.Flush()
	}
	return n, err
}

func classifyStreamMediaError(err error, sanitize bool) (int, string) {
	if err == nil {
		return http.StatusOK, ""
	}
	if errors.Is(err, ErrOutputMediaNotFound) {
		return http.StatusNotFound, ErrOutputMediaNotFound.Error()
	}
	if errors.Is(err, ErrOutputMediaOffline) {
		return http.StatusServiceUnavailable, ErrOutputMediaOffline.Error()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		if sanitize {
			return http.StatusServiceUnavailable, "作品暂离线或暂不可访问，请稍后再试"
		}
		return http.StatusGatewayTimeout, err.Error()
	}
	if sanitize {
		return http.StatusServiceUnavailable, "作品暂离线或暂不可访问，请稍后再试"
	}
	return http.StatusBadGateway, err.Error()
}

func (w *ColabOneWebConsole) streamOutputsZipHTTP(
	rw http.ResponseWriter,
	r *http.Request,
	items []colabOneZipItemSpec,
	zipBaseName string,
	sanitizeErrors bool,
	shouldContinue func() bool,
	headPrecheckStatus int,
	headPrecheckMsg string,
) {
	if len(items) == 0 {
		writeHeadAwareJSONError(rw, r, http.StatusBadRequest, "请至少选择一项有效作品")
		return
	}
	if len(items) > maxOutputZipItems {
		writeHeadAwareJSONError(rw, r, http.StatusBadRequest, fmt.Sprintf("单次打包下载作品数量不能超过 %d 项", maxOutputZipItems))
		return
	}
	if w.opts.StreamOutputMedia == nil {
		if sanitizeErrors {
			writeHeadAwareJSONError(rw, r, http.StatusServiceUnavailable, "作品暂离线或暂不可访问，请稍后再试")
		} else {
			writeHeadAwareJSONError(rw, r, http.StatusServiceUnavailable, "控制器桥接未初始化")
		}
		return
	}

	var knownTotalBytes int64
	for _, item := range items {
		if item.Size > 0 {
			if knownTotalBytes > maxOutputZipTotalBytes-item.Size {
				writeHeadAwareJSONError(rw, r, http.StatusRequestEntityTooLarge, errOutputZipSizeLimitExceeded.Error())
				return
			}
			knownTotalBytes += item.Size
		}
	}

	zipFilename := ensureZipExtension(sanitizeArchiveSegment(zipBaseName, "ColabOne-Outputs"))

	if r.Method == http.MethodHead {
		if shouldContinue != nil && !shouldContinue() {
			writeHeadAwareJSONError(rw, r, http.StatusUnauthorized, "分享会话已失效")
			return
		}
		if headPrecheckStatus != 0 && headPrecheckStatus != http.StatusOK {
			writeHeadAwareJSONError(rw, r, headPrecheckStatus, headPrecheckMsg)
			return
		}
		h := rw.Header()
		h.Set("Content-Type", "application/zip")
		h.Set("Content-Disposition", buildAttachmentContentDisposition(zipFilename))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		rw.WriteHeader(http.StatusOK)
		return
	}

	flusher, _ := rw.(http.Flusher)
	state := &lazyHTTPZipState{
		rw:             rw,
		flusher:        flusher,
		zipFilename:    zipFilename,
		now:            w.now(),
		usedEntryPaths: make(map[string]bool, len(items)),
	}

	sawNotFound := false
	sawUnavailable := false
	var lastUnavailableErr error
	aborted := false

	for _, item := range items {
		if r.Context().Err() != nil {
			aborted = true
			break
		}
		if shouldContinue != nil && !shouldContinue() {
			if state.headersSent {
				w.logActivity("成果物打包下载因分享权限变更已中止", "warn")
				panic(http.ErrAbortHandler)
			}
			writeHeadAwareJSONError(rw, r, http.StatusUnauthorized, "分享会话已失效")
			return
		}
		entryW := &lazyHTTPZipEntryWriter{
			state: state,
			item:  item,
		}
		_, err := w.opts.StreamOutputMedia(r.Context(), item.OutputID, entryW, sanitizeErrors)
		limitExceeded := (state.limitWriter != nil && state.limitWriter.exceeded) || errors.Is(err, errOutputZipSizeLimitExceeded)
		if limitExceeded {
			w.logActivity("成果物打包下载超出 1 GB 限制，已中止归档", "warn")
			if state.headersSent {
				panic(http.ErrAbortHandler)
			}
			writeHeadAwareJSONError(rw, r, http.StatusRequestEntityTooLarge, errOutputZipSizeLimitExceeded.Error())
			return
		}
		if r.Context().Err() != nil {
			aborted = true
			break
		}
		if err != nil {
			if entryW.started || (state.headersSent && entryW.headerAttempted) {
				// Bytes were already committed into the HTTP 200 ZIP stream; abort the HTTP connection cleanly so clients reject a corrupt archive.
				w.logActivity("成果物打包下载流式传输中断，已中止归档", "warn")
				panic(http.ErrAbortHandler)
			}
			if errors.Is(err, ErrOutputMediaNotFound) {
				sawNotFound = true
			} else {
				sawUnavailable = true
				lastUnavailableErr = err
			}
			continue
		}
	}

	if aborted || r.Context().Err() != nil {
		if state.headersSent {
			w.logActivity("成果物打包下载连接已取消，已中止归档", "warn")
			panic(http.ErrAbortHandler)
		}
		return
	}

	if shouldContinue != nil && !shouldContinue() {
		if state.headersSent {
			w.logActivity("成果物打包下载因分享权限变更已中止", "warn")
			panic(http.ErrAbortHandler)
		}
		writeHeadAwareJSONError(rw, r, http.StatusUnauthorized, "分享会话已失效")
		return
	}

	if !state.headersSent {
		if sawUnavailable {
			status, msg := classifyStreamMediaError(lastUnavailableErr, sanitizeErrors)
			if status == http.StatusBadGateway && sanitizeErrors {
				status = http.StatusServiceUnavailable
			}
			writeHeadAwareJSONError(rw, r, status, msg)
			return
		}
		if sawNotFound {
			writeHeadAwareJSONError(rw, r, http.StatusNotFound, ErrOutputMediaNotFound.Error())
			return
		}
		writeHeadAwareJSONError(rw, r, http.StatusNotFound, ErrOutputMediaNotFound.Error())
		return
	}

	if closeErr := state.zipWriter.Close(); closeErr != nil || (state.limitWriter != nil && state.limitWriter.exceeded) || r.Context().Err() != nil {
		w.logActivity("成果物打包下载结束归档失败，已中止响应", "warn")
		panic(http.ErrAbortHandler)
	}
	if state.flusher != nil {
		state.flusher.Flush()
	}
}

func (w *ColabOneWebConsole) handleOutputShareZipDownload(rw http.ResponseWriter, r *http.Request, shareID string) {
	if !isSameOriginDownloadRequest(r) {
		writeHeadAwareJSONError(rw, r, http.StatusForbidden, "跨源请求已被拒绝")
		return
	}

	now := w.now()
	w.mu.RLock()
	rec, exists := w.outputShares[shareID]
	if !exists || computeOutputShareStatus(rec, now) != "active" {
		w.mu.RUnlock()
		writeHeadAwareJSONError(rw, r, http.StatusUnauthorized, "分享会话已失效")
		return
	}
	recCopy := cloneOutputShareRecord(rec)
	w.mu.RUnlock()

	if !recCopy.AllowDownload {
		writeHeadAwareJSONError(rw, r, http.StatusForbidden, "该分享链接未开启下载权限")
		return
	}

	groupFilter := strings.TrimSpace(r.URL.Query().Get("group"))
	targetGroups := recCopy.Groups
	zipBaseName := recCopy.Title
	if zipBaseName == "" {
		zipBaseName = "ColabOne-Gallery"
	}
	if groupFilter != "" {
		var matched []colabOneOutputShareGroupRecord
		for _, g := range recCopy.Groups {
			if g.ID == groupFilter {
				matched = append(matched, g)
				if g.Title != "" {
					zipBaseName = g.Title
				}
				break
			}
		}
		if len(matched) == 0 {
			writeHeadAwareJSONError(rw, r, http.StatusNotFound, "未找到对应的作品分组")
			return
		}
		targetGroups = matched
	}

	if w.outputShareZipSem != nil {
		if r.Method == http.MethodHead {
			if len(w.outputShareZipSem) >= cap(w.outputShareZipSem) {
				rw.Header().Set("Retry-After", "2")
				writeHeadAwareJSONError(rw, r, http.StatusTooManyRequests, "当前打包下载任务过多，请稍后再试")
				return
			}
		} else {
			select {
			case w.outputShareZipSem <- struct{}{}:
				defer func() { <-w.outputShareZipSem }()
			default:
				rw.Header().Set("Retry-After", "2")
				writeJSON(rw, http.StatusTooManyRequests, map[string]any{
					"error":       "当前打包下载任务过多，请稍后再试",
					"retry_after": 2,
				})
				return
			}
		}
	}

	liveByID := make(map[string]colabOneOutputShareItemSnapshot)
	liveRawByID := make(map[string]map[string]any)
	snap := w.fetchSnapshotMap(r.Context())
	runtimeReady := false
	if snap != nil {
		runtimeReady = strings.TrimSpace(shareAsString(shareAsMap(snap["runtime"])["state"])) == "ready"
		for _, rawOut := range shareAsSlice(snap["comfy_outputs"]) {
			outMap := shareAsMap(rawOut)
			if outMap == nil {
				continue
			}
			oid := strings.ToLower(strings.TrimSpace(shareAsString(outMap["id"])))
			if outputIDRe.MatchString(oid) {
				liveRawByID[oid] = outMap
				liveByID[oid] = extractSanitizedItemSnapshot(oid, outMap)
			}
		}
	}

	useSubfolders := len(targetGroups) > 1
	usedFolders := make(map[string]bool, len(targetGroups))
	items := make([]colabOneZipItemSpec, 0)
	availCount := 0
	offlineCount := 0

	for idx, g := range targetGroups {
		folder := ""
		if useSubfolders {
			folderSeed := strings.TrimSpace(g.Title)
			if folderSeed == "" {
				folderSeed = strings.TrimSpace(g.WorkflowName)
			}
			if folderSeed == "" {
				folderSeed = fmt.Sprintf("Group-%02d", idx+1)
			}
			folder = allocateUniqueFolderName(usedFolders, folderSeed, fmt.Sprintf("Group-%02d", idx+1))
		}
		for _, outID := range g.OutputIDs {
			fname := ""
			ext := ""
			var size int64
			if liveItem, ok := liveByID[outID]; ok {
				fname = liveItem.Filename
				ext = liveItem.Extension
				if snap != nil && isLiveOutputAvailableForGallery(liveRawByID[outID], runtimeReady) {
					availCount++
					size = liveItem.Size
				} else {
					offlineCount++
				}
			} else if frozen, ok := recCopy.Items[outID]; ok {
				fname = frozen.Filename
				ext = frozen.Extension
				// Leave Size as 0 on frozen-only items so deleted/offline items skipped
				// during streaming never trigger a false pre-header HTTP 413.
			}
			items = append(items, colabOneZipItemSpec{
				OutputID:  outID,
				Folder:    folder,
				Filename:  fname,
				Extension: ext,
				Size:      size,
			})
		}
	}

	headStatus := http.StatusOK
	headMsg := ""
	if r.Method == http.MethodHead && snap != nil && availCount == 0 {
		if offlineCount > 0 {
			headStatus = http.StatusServiceUnavailable
			headMsg = ErrOutputMediaOffline.Error()
		} else {
			headStatus = http.StatusNotFound
			headMsg = ErrOutputMediaNotFound.Error()
		}
	}

	shouldContinue := func() bool {
		checkNow := w.now()
		w.mu.RLock()
		cur, ok := w.outputShares[shareID]
		w.mu.RUnlock()
		return ok && cur.AllowDownload && computeOutputShareStatus(cur, checkNow) == "active"
	}

	w.streamOutputsZipHTTP(rw, r, items, zipBaseName, true, shouldContinue, headStatus, headMsg)
}

// --- Host Authenticated Web Console HTTP Handlers ---

func isHostWebClientHeader(r *http.Request) bool {
	val := r.Header.Get(ClientHeaderName)
	return val == ClientHeaderValue || val == "web-console"
}

func (w *ColabOneWebConsole) validateHostWebSameOrigin(r *http.Request) bool {
	if !isHostWebClientHeader(r) {
		return false
	}
	return w.validateSameOriginWithClientHeader(r, "")
}

func (w *ColabOneWebConsole) handleHostOutputSharesList(rw http.ResponseWriter) {
	writeJSON(rw, http.StatusOK, w.OutputSharesState())
}

func (w *ColabOneWebConsole) handleHostOutputShareCreate(rw http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(rw, r.Body, maxOutputShareBodyBytes)
	var req ColabOneOutputShareCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSONError(rw, http.StatusRequestEntityTooLarge, "请求体超出限制")
			return
		}
		writeJSONError(rw, http.StatusBadRequest, "请求格式无效")
		return
	}
	summary, err := w.createOutputShareWithContext(r.Context(), req)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errWebConsoleClosed) {
			status = http.StatusServiceUnavailable
		}
		writeJSONError(rw, status, err.Error())
		return
	}
	writeJSON(rw, http.StatusOK, summary)
}

func (w *ColabOneWebConsole) handleHostOutputShareRevoke(rw http.ResponseWriter, r *http.Request, shareID string) {
	state, err := w.revokeOutputShareWithContext(r.Context(), shareID)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errWebConsoleClosed) {
			status = http.StatusServiceUnavailable
		} else if strings.Contains(err.Error(), "未找到") {
			status = http.StatusNotFound
		}
		writeJSONError(rw, status, err.Error())
		return
	}
	writeJSON(rw, http.StatusOK, state)
}

func (w *ColabOneWebConsole) handleHostOutputShareDelete(rw http.ResponseWriter, r *http.Request, shareID string) {
	state, err := w.deleteOutputShareWithContext(r.Context(), shareID)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errWebConsoleClosed) {
			status = http.StatusServiceUnavailable
		} else if strings.Contains(err.Error(), "未找到") {
			status = http.StatusNotFound
		}
		writeJSONError(rw, status, err.Error())
		return
	}
	writeJSON(rw, http.StatusOK, state)
}

func (w *ColabOneWebConsole) handleHostOutputsZip(rw http.ResponseWriter, r *http.Request) {
	if !isSameOriginDownloadRequest(r) {
		writeHeadAwareJSONError(rw, r, http.StatusForbidden, "跨源请求已被拒绝")
		return
	}
	rawIDs := strings.TrimSpace(r.URL.Query().Get("ids"))
	if rawIDs == "" {
		writeHeadAwareJSONError(rw, r, http.StatusBadRequest, "请至少选择一项有效作品")
		return
	}
	parts := strings.Split(rawIDs, ",")
	seen := make(map[string]bool, len(parts))
	ids := make([]string, 0, len(parts))
	for _, p := range parts {
		cleanID := strings.ToLower(strings.TrimSpace(p))
		if cleanID == "" {
			continue
		}
		if !outputIDRe.MatchString(cleanID) {
			writeHeadAwareJSONError(rw, r, http.StatusBadRequest, "输出文件 ID 无效")
			return
		}
		if seen[cleanID] {
			continue
		}
		seen[cleanID] = true
		ids = append(ids, cleanID)
		if len(ids) > maxOutputZipItems {
			writeHeadAwareJSONError(rw, r, http.StatusBadRequest, fmt.Sprintf("单次打包下载作品数量不能超过 %d 项", maxOutputZipItems))
			return
		}
	}
	if len(ids) == 0 {
		writeHeadAwareJSONError(rw, r, http.StatusBadRequest, "请至少选择一项有效作品")
		return
	}

	if w.hostOutputZipSem != nil {
		if r.Method == http.MethodHead {
			if len(w.hostOutputZipSem) >= cap(w.hostOutputZipSem) {
				rw.Header().Set("Retry-After", "2")
				writeHeadAwareJSONError(rw, r, http.StatusTooManyRequests, "当前打包下载任务过多，请稍后再试")
				return
			}
		} else {
			select {
			case w.hostOutputZipSem <- struct{}{}:
				defer func() { <-w.hostOutputZipSem }()
			default:
				rw.Header().Set("Retry-After", "2")
				writeJSON(rw, http.StatusTooManyRequests, map[string]any{
					"error":       "当前打包下载任务过多，请稍后再试",
					"retry_after": 2,
				})
				return
			}
		}
	}

	liveByID := make(map[string]colabOneOutputShareItemSnapshot)
	liveRawByID := make(map[string]map[string]any)
	snap := w.fetchSnapshotMap(r.Context())
	runtimeReady := false
	if snap != nil {
		runtimeReady = strings.TrimSpace(shareAsString(shareAsMap(snap["runtime"])["state"])) == "ready"
		for _, rawOut := range shareAsSlice(snap["comfy_outputs"]) {
			outMap := shareAsMap(rawOut)
			if outMap == nil {
				continue
			}
			oid := strings.ToLower(strings.TrimSpace(shareAsString(outMap["id"])))
			if seen[oid] {
				liveRawByID[oid] = outMap
				liveByID[oid] = extractSanitizedItemSnapshot(oid, outMap)
			}
		}
	}

	items := make([]colabOneZipItemSpec, 0, len(ids))
	availCount := 0
	offlineCount := 0
	for _, id := range ids {
		fname := ""
		ext := ""
		var size int64
		if liveItem, ok := liveByID[id]; ok {
			fname = liveItem.Filename
			ext = liveItem.Extension
			if snap != nil && isLiveOutputAvailableForGallery(liveRawByID[id], runtimeReady) {
				availCount++
				size = liveItem.Size
			} else {
				offlineCount++
			}
		}
		items = append(items, colabOneZipItemSpec{
			OutputID:  id,
			Filename:  fname,
			Extension: ext,
			Size:      size,
		})
	}

	headStatus := http.StatusOK
	headMsg := ""
	if r.Method == http.MethodHead && snap != nil && availCount == 0 {
		if offlineCount > 0 {
			headStatus = http.StatusServiceUnavailable
			headMsg = ErrOutputMediaOffline.Error()
		} else {
			headStatus = http.StatusNotFound
			headMsg = ErrOutputMediaNotFound.Error()
		}
	}

	zipName := strings.TrimSpace(r.URL.Query().Get("name"))
	if zipName == "" {
		zipName = "ColabOne-Outputs"
	}
	w.streamOutputsZipHTTP(rw, r, items, zipName, false, nil, headStatus, headMsg)
}

// --- Filename & Content-Disposition Sanitization Helpers ---

func allocateUniqueFolderName(used map[string]bool, raw, fallback string) string {
	base := sanitizeArchiveSegment(raw, fallback)
	key := strings.ToLower(base)
	if !used[key] {
		used[key] = true
		return base
	}
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s (%d)", base, i)
		ck := strings.ToLower(cand)
		if !used[ck] {
			used[ck] = true
			return cand
		}
	}
}

func truncateUTF8RunesAndBytes(s string, maxRunes, maxBytes int) string {
	if maxRunes <= 0 || maxBytes <= 0 {
		return ""
	}
	runes := 0
	bytesCount := 0
	for i, r := range s {
		rLen := utf8.RuneLen(r)
		if rLen < 0 {
			rLen = 1
		}
		if runes+1 > maxRunes || bytesCount+rLen > maxBytes {
			return s[:i]
		}
		runes++
		bytesCount += rLen
	}
	return s
}

func sanitizeArchiveSegment(raw, fallback string) string {
	if fallback == "" {
		fallback = "ColabOne"
	}
	cleaned := strings.ReplaceAll(strings.TrimSpace(raw), `\`, "/")
	base := path.Base(path.Clean("/" + cleaned))
	if base == "" || base == "/" || base == "." || base == ".." {
		return fallback
	}
	var b strings.Builder
	b.Grow(len(base))
	for _, r := range base {
		if r < 0x20 || r == 0x7f || unicode.IsControl(r) {
			continue
		}
		switch r {
		case '<', '>', ':', '"', '/', '\\', '|', '?', '*':
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	trimDotsAndSpaces := func(r rune) bool {
		return r == '.' || unicode.IsSpace(r)
	}
	name := strings.TrimFunc(b.String(), trimDotsAndSpaces)
	if name == "" || name == "." || name == ".." {
		return fallback
	}
	const maxSegmentContentBytes = maxArchiveSegmentBytes - archiveSegmentHeadroomBytes
	if utf8.RuneCountInString(name) > 96 || len(name) > maxSegmentContentBytes {
		ext := filepath.Ext(name)
		if ext != "" && utf8.RuneCountInString(ext) < 24 && len(ext) <= 32 {
			stem := strings.TrimSuffix(name, ext)
			stemMaxRunes := 96 - utf8.RuneCountInString(ext)
			stemMaxBytes := maxSegmentContentBytes - len(ext)
			if stemMaxRunes > 0 && stemMaxBytes > 0 {
				truncatedStem := strings.TrimRightFunc(truncateUTF8RunesAndBytes(stem, stemMaxRunes, stemMaxBytes), trimDotsAndSpaces)
				if truncatedStem == "" || truncatedStem == "." || truncatedStem == ".." {
					name = ""
				} else {
					name = truncatedStem + ext
				}
			} else {
				name = truncateUTF8RunesAndBytes(name, 96, maxSegmentContentBytes)
			}
		} else {
			name = truncateUTF8RunesAndBytes(name, 96, maxSegmentContentBytes)
		}
		name = strings.TrimFunc(name, trimDotsAndSpaces)
		if name == "" || name == "." || name == ".." {
			return fallback
		}
	}
	stem := name
	if dot := strings.IndexByte(stem, '.'); dot >= 0 {
		stem = stem[:dot]
	}
	if windowsReservedArchiveNames[strings.ToUpper(strings.TrimRightFunc(stem, unicode.IsSpace))] {
		name = "colabone-" + name
	}
	return name
}

func ensureZipExtension(name string) string {
	if strings.HasSuffix(strings.ToLower(name), ".zip") {
		return name
	}
	return name + ".zip"
}

func buildAttachmentContentDisposition(filename string) string {
	safe := sanitizeArchiveSegment(filename, "ColabOne-download")
	var asciiBuf strings.Builder
	hasNonASCII := false
	for _, r := range safe {
		if r >= 0x20 && r <= 0x7e && r != '"' && r != '\\' && r != ';' {
			asciiBuf.WriteRune(r)
		} else {
			hasNonASCII = true
			if r > 0x7e {
				asciiBuf.WriteByte('_')
			}
		}
	}
	asciiFallback := strings.Trim(asciiBuf.String(), ". ")
	ext := filepath.Ext(safe)
	asciiStem := strings.TrimSuffix(asciiFallback, ext)
	if asciiFallback == "" || strings.Trim(asciiFallback, "_.") == "" || strings.Trim(asciiStem, "_.- ") == "" {
		if ext != "" && len(ext) <= 12 && isPureASCIIExtension(ext) {
			asciiFallback = "ColabOne-download" + ext
		} else {
			asciiFallback = "ColabOne-download"
		}
	}
	if !hasNonASCII {
		return fmt.Sprintf(`attachment; filename="%s"`, asciiFallback)
	}
	encoded := url.PathEscape(safe)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, asciiFallback, encoded)
}

func isPureASCIIExtension(ext string) bool {
	if ext == "" || ext[0] != '.' {
		return false
	}
	for i := 1; i < len(ext); i++ {
		c := ext[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return len(ext) > 1
}
