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
)

const (
	// ColabOneVaultFilename is the encrypted credential file stored in the
	// user's ColabOne configuration directory across all platforms.
	ColabOneVaultFilename = "vault.enc"

	// ColabOneVaultFileFormat identifies ColabOne local encrypted vault files.
	ColabOneVaultFileFormat = "colabone-vault"

	colabOneVaultFileVersion          = 1
	colabOneVaultFileAlgAES256GCM     = "aes-256-gcm"
	colabOneVaultSaltLen              = 32
	colabOneVaultNonceLen             = 12
	colabOneVaultMaxFileBytes         = 16 * 1024 * 1024
	colabOneVaultWinRenameMaxAttempts = 6
)

// ErrCorruptVault is returned when an existing vault.enc file cannot be parsed
// or fails authenticated decryption.
var ErrCorruptVault = errors.New("colabone vault: encrypted vault file is corrupted")

// colabOneLocalVaultDomainKey is the static application domain key combined
// with a fresh 32-byte random per-write salt via HKDF-SHA256 to authenticate
// and encrypt local vault.enc payloads without invoking OS keychain dialogs.
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

func (b *ColabOneFileSecretBackend) aad() []byte {
	return []byte("colabone-vault:v1:" + b.serviceName())
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
			return nil, err
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
				return err
			}
			recoveringCorrupt = true
			_ = b.quarantineCorruptFileLocked()
			b.entries = make(map[string][]byte)
		}
	}

	prev, existed := b.entries[key]
	b.entries[key] = cloneBytes(value)
	if err := b.saveLocked(); err != nil {
		if recoveringCorrupt {
			b.entries = nil
			b.loaded = false
		} else if existed {
			b.entries[key] = prev
		} else {
			delete(b.entries, key)
		}
		return err
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
				return err
			}
			if qErr := b.quarantineCorruptFileLocked(); qErr != nil {
				return qErr
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
		b.entries[key] = prev
		return err
	}
	return nil
}

func (b *ColabOneFileSecretBackend) checkRegularTargetLocked() error {
	info, err := os.Lstat(b.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("colabone vault: refusing symlink vault path %q", b.path)
	}
	if info.IsDir() {
		return fmt.Errorf("colabone vault: vault path %q is a directory", b.path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("colabone vault: vault path %q is not a regular file", b.path)
	}
	return nil
}

func (b *ColabOneFileSecretBackend) loadLocked() error {
	if err := b.checkRegularTargetLocked(); err != nil {
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
	if env.Format != ColabOneVaultFileFormat || env.Version != colabOneVaultFileVersion || env.Algorithm != colabOneVaultFileAlgAES256GCM {
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

	derivedKey, err := hkdf.Key(sha256.New, colabOneLocalVaultDomainKey[:], salt, string(b.aad()), 32)
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
	plain, err := gcm.Open(nil, nonce, ciphertext, b.aad())
	if err != nil {
		return fmt.Errorf("%w: authentication failed", ErrCorruptVault)
	}
	defer clear(plain)

	var payload colabOneVaultFilePayload
	if err := json.Unmarshal(plain, &payload); err != nil {
		return fmt.Errorf("%w: invalid payload JSON: %v", ErrCorruptVault, err)
	}
	if payload.Version != colabOneVaultFileVersion {
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
	if err := b.checkRegularTargetLocked(); err != nil {
		return err
	}
	payload := colabOneVaultFilePayload{
		Version: colabOneVaultFileVersion,
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

	derivedKey, err := hkdf.Key(sha256.New, colabOneLocalVaultDomainKey[:], salt, string(b.aad()), 32)
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
	ciphertext := gcm.Seal(nil, nonce, plain, b.aad())

	env := colabOneVaultFileEnvelope{
		Format:     ColabOneVaultFileFormat,
		Version:    colabOneVaultFileVersion,
		Algorithm:  colabOneVaultFileAlgAES256GCM,
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}
	encoded, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')

	dir := filepath.Dir(b.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".vault-*.tmp")
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

	if runtime.GOOS != "windows" {
		if err := tmp.Chmod(0o600); err != nil {
			return err
		}
	}
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
	return nil
}

func (b *ColabOneFileSecretBackend) quarantineCorruptFileLocked() error {
	backup := b.path + ".corrupt." + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := colabOneVaultRenameReplace(b.path, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func colabOneVaultRenameReplace(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil || runtime.GOOS != "windows" {
		return err
	}
	for attempt := 1; attempt < colabOneVaultWinRenameMaxAttempts; attempt++ {
		if info, statErr := os.Stat(dst); statErr == nil && info.IsDir() {
			return err
		}
		time.Sleep(time.Duration(attempt*15) * time.Millisecond)
		err = os.Rename(src, dst)
		if err == nil {
			return nil
		}
	}
	return err
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
