package vault

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/luolongfei/ColabOne/console/internal/fsutil"
)

const (
	// ColabOneVaultFilename is the encrypted credential file stored in the
	// user's ColabOne configuration directory across all platforms.
	ColabOneVaultFilename = "vault.enc"

	// ColabOneVaultKeyFilename is the 0600 per-installation key-encryption-key
	// sibling file used by non-Windows v2 envelopes (zero OS keychain prompts).
	ColabOneVaultKeyFilename = "vault.key"

	// ColabOneVaultFileFormat identifies ColabOne local encrypted vault files.
	ColabOneVaultFileFormat = "colabone-vault"

	colabOneVaultFileVersionV1        = 1
	colabOneVaultFileVersionV2        = 2
	colabOneVaultFileAlgAES256GCM     = "aes-256-gcm"
	colabOneVaultKeyWrapDPAPI         = "dpapi"
	colabOneVaultKeyWrapFileKEK       = "file_kek"
	colabOneVaultKEKLen               = 32
	colabOneVaultSaltLen              = 32
	colabOneVaultNonceLen             = 12
	colabOneVaultMaxFileBytes         = 16 * 1024 * 1024
	colabOneVaultWinRenameMaxAttempts = 6
)

// ErrCorruptVault is returned when an existing vault.enc file cannot be parsed
// or fails authenticated decryption.
var ErrCorruptVault = errors.New("colabone vault: encrypted vault file is corrupted")

// ColabOneVaultStorageError marks a local encrypted vault persistence or I/O
// failure so higher layers (HTTP clients, auth refresh, background monitor)
// never misclassify local disk/permission/DPAPI errors as Google OAuth expiry
// or network connectivity failures.
type ColabOneVaultStorageError struct {
	Err error
}

func (e *ColabOneVaultStorageError) Error() string {
	if e == nil || e.Err == nil {
		return "colabone vault: local storage error"
	}
	return e.Err.Error()
}

func (e *ColabOneVaultStorageError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsVaultStorageError structurally identifies local encrypted vault storage errors
// across package boundaries.
func (e *ColabOneVaultStorageError) IsVaultStorageError() bool {
	return true
}

func wrapVaultStorageError(err error) error {
	if err == nil || errors.Is(err, ErrNotFound) {
		return err
	}
	var existing *ColabOneVaultStorageError
	if errors.As(err, &existing) {
		return err
	}
	return &ColabOneVaultStorageError{Err: err}
}

// colabOneSecureVaultFinalPathFn is a package-level seam for offline unit tests.
var colabOneSecureVaultFinalPathFn = colabOneSecureVaultFinalPath

// colabOneLocalVaultDomainKey is the legacy v1 static application domain key
// retained solely for transparent backward-compatible decryption of existing v1
// vault.enc files before upgrading them to v2 on the next write.
var colabOneLocalVaultDomainKey = [32]byte{
	0x43, 0x6f, 0x6c, 0x61, 0x62, 0x4f, 0x6e, 0x65,
	0x2d, 0x4c, 0x6f, 0x63, 0x61, 0x6c, 0x56, 0x61,
	0x75, 0x6c, 0x74, 0x2d, 0x41, 0x45, 0x53, 0x32,
	0x35, 0x36, 0x47, 0x43, 0x4d, 0x2d, 0x56, 0x31,
}

type colabOneVaultFileEnvelope struct {
	Format     string `json:"format"`
	Version    int    `json:"version"`
	Algorithm  string `json:"algorithm"`
	KeyWrap    string `json:"key_wrap,omitempty"`
	WrappedKey string `json:"wrapped_key,omitempty"`
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type colabOneVaultFilePayload struct {
	Version int               `json:"version"`
	Entries map[string][]byte `json:"entries"`
}

// ColabOneFileSecretBackend persists secrets in a single 0600 AES-256-GCM
// encrypted file (vault.enc) inside the ColabOne user configuration directory.
type ColabOneFileSecretBackend struct {
	mu      sync.RWMutex
	path    string
	service string
	loaded  bool
	entries map[string][]byte
}

// NewColabOneFileSecretBackend constructs an encrypted local-file secret backend
// rooted at path (or default configDir/vault.enc if path is empty).
func NewColabOneFileSecretBackend(path, service string) *ColabOneFileSecretBackend {
	resolvedPath := strings.TrimSpace(path)
	if resolvedPath == "" {
		resolvedPath = filepath.Join(colabOneDefaultVaultDir(), ColabOneVaultFilename)
	}
	resolvedService := strings.TrimSpace(service)
	if resolvedService == "" {
		resolvedService = ColabOneVaultService
	}
	return &ColabOneFileSecretBackend{
		path:    resolvedPath,
		service: resolvedService,
	}
}

// Path returns the filesystem path of the encrypted vault file.
func (b *ColabOneFileSecretBackend) Path() string {
	if b == nil {
		return ""
	}
	return b.path
}

func (b *ColabOneFileSecretBackend) serviceName() string {
	if b == nil || strings.TrimSpace(b.service) == "" {
		return ColabOneVaultService
	}
	return b.service
}

func (b *ColabOneFileSecretBackend) aadV1() []byte {
	return []byte("colabone-vault:v1:" + b.serviceName())
}

func (b *ColabOneFileSecretBackend) aadV2(keyWrap string) []byte {
	return []byte("colabone-vault:v2:" + keyWrap + ":" + b.serviceName())
}

// Get reads key from the encrypted local vault file.
// Returns ErrNotFound when the file or key does not exist.
func (b *ColabOneFileSecretBackend) Get(key string) ([]byte, error) {
	if b == nil {
		return nil, errors.New("colabone vault: nil file backend")
	}
	if err := validateVaultKey(key); err != nil {
		return nil, err
	}

	b.mu.RLock()
	if b.loaded {
		val, ok := b.entries[key]
		b.mu.RUnlock()
		if !ok {
			return nil, ErrNotFound
		}
		return cloneBytes(val), nil
	}
	b.mu.RUnlock()

	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.loaded {
		if err := b.loadLocked(); err != nil {
			return nil, wrapVaultStorageError(err)
		}
	}
	val, ok := b.entries[key]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneBytes(val), nil
}

// Set stores key/value in the encrypted local vault file using atomic write-and-replace.
func (b *ColabOneFileSecretBackend) Set(key string, value []byte) error {
	if b == nil {
		return errors.New("colabone vault: nil file backend")
	}
	if err := validateVaultKey(key); err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	recoveringCorrupt := false
	if !b.loaded {
		if err := b.loadLocked(); err != nil {
			if !errors.Is(err, ErrCorruptVault) {
				return wrapVaultStorageError(err)
			}
			recoveringCorrupt = true
			if qErr := b.quarantineCorruptFileLocked(); qErr != nil {
				return wrapVaultStorageError(qErr)
			}
			b.entries = make(map[string][]byte)
		}
	}

	prev, existed := b.entries[key]
	b.entries[key] = cloneBytes(value)
	if err := b.saveLocked(); err != nil {
		if recoveringCorrupt || !b.loaded {
			b.entries = nil
			b.loaded = false
		} else if existed {
			b.entries[key] = prev
		} else {
			delete(b.entries, key)
		}
		return wrapVaultStorageError(err)
	}
	b.loaded = true
	return nil
}

// Delete removes key from the encrypted local vault file. Deleting a missing
// key or non-existent file is a no-op that never creates an empty file on disk.
func (b *ColabOneFileSecretBackend) Delete(key string) error {
	if b == nil {
		return errors.New("colabone vault: nil file backend")
	}
	if err := validateVaultKey(key); err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.loaded {
		if err := b.loadLocked(); err != nil {
			if !errors.Is(err, ErrCorruptVault) {
				return wrapVaultStorageError(err)
			}
			if qErr := b.quarantineCorruptFileLocked(); qErr != nil {
				return wrapVaultStorageError(qErr)
			}
			b.entries = make(map[string][]byte)
			b.loaded = true
			return nil
		}
	}

	prev, existed := b.entries[key]
	if !existed {
		return nil
	}
	delete(b.entries, key)
	if err := b.saveLocked(); err != nil {
		if !b.loaded {
			b.entries = nil
		} else {
			b.entries[key] = prev
		}
		return wrapVaultStorageError(err)
	}
	return nil
}

func (b *ColabOneFileSecretBackend) checkRegularTargetLocked() (os.FileInfo, error) {
	info, err := os.Lstat(b.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("colabone vault: refusing symlink vault path %q", b.path)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("colabone vault: vault path %q is a directory", b.path)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("colabone vault: vault path %q is not a regular file", b.path)
	}
	return info, nil
}

func (b *ColabOneFileSecretBackend) loadLocked() error {
	linfo, err := b.checkRegularTargetLocked()
	if err != nil {
		return err
	}
	f, err := os.Open(b.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			b.entries = make(map[string][]byte)
			b.loaded = true
			return nil
		}
		return err
	}
	defer f.Close()

	finfo, err := f.Stat()
	if err != nil {
		return err
	}
	if !finfo.Mode().IsRegular() {
		return fmt.Errorf("colabone vault: vault path %q is not a regular file", b.path)
	}
	if linfo == nil {
		linfo, err = b.checkRegularTargetLocked()
		if err != nil {
			return err
		}
		if linfo == nil {
			return fmt.Errorf("colabone vault: refusing symlink or concurrently replaced vault path %q", b.path)
		}
	}
	if !os.SameFile(linfo, finfo) {
		return fmt.Errorf("colabone vault: refusing symlink or concurrently replaced vault path %q", b.path)
	}

	raw, err := io.ReadAll(io.LimitReader(f, colabOneVaultMaxFileBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > colabOneVaultMaxFileBytes {
		return fmt.Errorf("%w: file exceeds %d bytes", ErrCorruptVault, colabOneVaultMaxFileBytes)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return fmt.Errorf("%w: empty vault file", ErrCorruptVault)
	}

	var env colabOneVaultFileEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("%w: invalid envelope JSON: %v", ErrCorruptVault, err)
	}
	if env.Format != ColabOneVaultFileFormat || env.Algorithm != colabOneVaultFileAlgAES256GCM {
		return fmt.Errorf("%w: unsupported format=%q version=%d alg=%q", ErrCorruptVault, env.Format, env.Version, env.Algorithm)
	}

	salt, err := base64.StdEncoding.DecodeString(env.Salt)
	if err != nil || len(salt) != colabOneVaultSaltLen {
		return fmt.Errorf("%w: invalid salt", ErrCorruptVault)
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil || len(nonce) != colabOneVaultNonceLen {
		return fmt.Errorf("%w: invalid nonce", ErrCorruptVault)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil || len(ciphertext) == 0 {
		return fmt.Errorf("%w: invalid ciphertext", ErrCorruptVault)
	}

	var aad []byte
	var derivedKey []byte
	switch env.Version {
	case colabOneVaultFileVersionV1:
		aad = b.aadV1()
		derivedKey, err = hkdf.Key(sha256.New, colabOneLocalVaultDomainKey[:], salt, string(aad), 32)
		if err != nil {
			return err
		}
	case colabOneVaultFileVersionV2:
		kek, unwrapErr := b.unwrapV2KeyLocked(env.KeyWrap, env.WrappedKey)
		if unwrapErr != nil {
			return unwrapErr
		}
		defer clear(kek)
		aad = b.aadV2(env.KeyWrap)
		derivedKey, err = hkdf.Key(sha256.New, kek, salt, string(aad), 32)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: unsupported format=%q version=%d alg=%q", ErrCorruptVault, env.Format, env.Version, env.Algorithm)
	}
	defer clear(derivedKey)

	block, err := aes.NewCipher(derivedKey)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return fmt.Errorf("%w: authentication failed", ErrCorruptVault)
	}
	defer clear(plain)

	var payload colabOneVaultFilePayload
	if err := json.Unmarshal(plain, &payload); err != nil {
		return fmt.Errorf("%w: invalid payload JSON: %v", ErrCorruptVault, err)
	}
	if payload.Version != colabOneVaultFileVersionV1 && payload.Version != colabOneVaultFileVersionV2 {
		return fmt.Errorf("%w: unsupported payload version %d", ErrCorruptVault, payload.Version)
	}

	loadedEntries := make(map[string][]byte, len(payload.Entries))
	for k, v := range payload.Entries {
		if validateVaultKey(k) == nil {
			loadedEntries[k] = cloneBytes(v)
		}
	}
	b.entries = loadedEntries
	b.loaded = true
	return nil
}

func (b *ColabOneFileSecretBackend) saveLocked() error {
	if _, err := b.checkRegularTargetLocked(); err != nil {
		return err
	}
	dir := filepath.Dir(b.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	kek, keyWrap, wrappedKey, err := b.prepareV2SaveKeyLocked()
	if err != nil {
		return err
	}
	defer clear(kek)

	payload := colabOneVaultFilePayload{
		Version: colabOneVaultFileVersionV2,
		Entries: b.entries,
	}
	plain, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	defer clear(plain)

	salt := make([]byte, colabOneVaultSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	nonce := make([]byte, colabOneVaultNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}

	aad := b.aadV2(keyWrap)
	derivedKey, err := hkdf.Key(sha256.New, kek, salt, string(aad), 32)
	if err != nil {
		return err
	}
	defer clear(derivedKey)

	block, err := aes.NewCipher(derivedKey)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	ciphertext := gcm.Seal(nil, nonce, plain, aad)

	env := colabOneVaultFileEnvelope{
		Format:     ColabOneVaultFileFormat,
		Version:    colabOneVaultFileVersionV2,
		Algorithm:  colabOneVaultFileAlgAES256GCM,
		KeyWrap:    keyWrap,
		WrappedKey: wrappedKey,
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}
	encoded, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > colabOneVaultMaxFileBytes {
		return fmt.Errorf("colabone vault: encoded vault size %d exceeds maximum %d bytes", len(encoded), colabOneVaultMaxFileBytes)
	}

	tmp, err := colabOneCreateSecureVaultTempFile(dir, ".vault-")
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

	if _, err := tmp.Write(encoded); err != nil {
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

	if err := colabOneVaultRenameReplace(tmpName, b.path); err != nil {
		return err
	}
	renamed = true
	if err := colabOneSecureVaultFinalPathFn(b.path); err != nil {
		b.loaded = false
		b.entries = nil
		return err
	}
	return nil
}

func (b *ColabOneFileSecretBackend) quarantineCorruptFileLocked() error {
	backup := b.path + ".corrupt." + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := colabOneVaultRenameReplace(b.path, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return b.quarantineCorruptKeyLocked()
}

func colabOneVaultRenameReplace(src, dst string) error {
	return fsutil.RenameReplace(src, dst)
}

// colabOneDefaultVaultDir matches controller.DefaultConfigDir() so standalone
// vault callers resolve the exact same per-user configuration directory.
func colabOneDefaultVaultDir() string {
	if dir := strings.TrimSpace(os.Getenv("COLABONE_CONFIG_DIR")); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "ColabOne")
	}
	if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
		return filepath.Join(localAppData, "ColabOne")
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "AppData", "Local", "ColabOne")
	}
	return filepath.Join(home, ".local", "share", "ColabOne")
}
