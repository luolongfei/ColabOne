// Package vault implements ColabOne's secure OS credential vault backed by
// mygo.Secrets, with transparent chunking for secrets larger than
// mygo.MaxSecretSize and seamless Windows legacy keyring migration.
package vault

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/egoist/mygo"
)

// ColabOneVaultService is the OS credential store service name used by ColabOne.
const ColabOneVaultService = "ColabOne"

const (
	// colabOneVaultChunkSize stays safely below mygo.MaxSecretSize (2560 bytes)
	// so every chunk fits in Windows Credential Manager, macOS Keychain, and
	// Linux Secret Service without writing any secret bytes to disk.
	colabOneVaultChunkSize        = 2400
	colabOneVaultMaxChunks        = 4096
	colabOneChunkKeySep           = "#chunk:"
	colabOneChunkManifestMaxBytes = 128
)

// ErrNotFound is returned by ColabOneSecretBackend.Get when no secret exists
// for the requested key.
var ErrNotFound = errors.New("colabone vault: secret not found")

// ColabOneSecretBackend abstracts raw byte storage in an OS credential store
// or an in-memory test double.
type ColabOneSecretBackend interface {
	Get(key string) ([]byte, error)
	Set(key string, value []byte) error
	Delete(key string) error
}

// ColabOneMyGoSecretBackend stores secrets through mygo.Secrets.Service.
type ColabOneMyGoSecretBackend struct {
	service string
}

// NewColabOneMyGoSecretBackend creates a mygo.Secrets backend scoped to service
// (defaulting to ColabOneVaultService when empty).
func NewColabOneMyGoSecretBackend(service string) *ColabOneMyGoSecretBackend {
	if service == "" {
		service = ColabOneVaultService
	}
	return &ColabOneMyGoSecretBackend{service: service}
}

func (b *ColabOneMyGoSecretBackend) serviceName() string {
	if b == nil || b.service == "" {
		return ColabOneVaultService
	}
	return b.service
}

// Get fetches raw secret bytes for key, mapping mygo.ErrSecretNotFound to ErrNotFound.
func (b *ColabOneMyGoSecretBackend) Get(key string) ([]byte, error) {
	data, err := mygo.Secrets.Service(b.serviceName()).Get(key)
	if err != nil {
		if errors.Is(err, mygo.ErrSecretNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return data, nil
}

// Set stores raw secret bytes for key in the OS credential store.
func (b *ColabOneMyGoSecretBackend) Set(key string, value []byte) error {
	return mygo.Secrets.Service(b.serviceName()).Set(key, value)
}

// Delete removes key from the OS credential store. Deleting a missing key is not an error.
func (b *ColabOneMyGoSecretBackend) Delete(key string) error {
	err := mygo.Secrets.Service(b.serviceName()).Delete(key)
	if errors.Is(err, mygo.ErrSecretNotFound) {
		return nil
	}
	return err
}

// colabOneMemorySecretBackend is a thread-safe in-memory backend for offline unit tests.
// It enforces mygo.MaxSecretSize so tests verify chunking invariants automatically.
type colabOneMemorySecretBackend struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func newColabOneMemorySecretBackend() *colabOneMemorySecretBackend {
	return &colabOneMemorySecretBackend{
		data: make(map[string][]byte),
	}
}

func (m *colabOneMemorySecretBackend) Get(key string) ([]byte, error) {
	if err := validateVaultKey(key); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	val, ok := m.data[key]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneBytes(val), nil
}

func (m *colabOneMemorySecretBackend) Set(key string, value []byte) error {
	if err := validateVaultKey(key); err != nil {
		return err
	}
	if len(value) > mygo.MaxSecretSize {
		return fmt.Errorf("colabone vault: secret %q has %d bytes, exceeds MaxSecretSize (%d)", key, len(value), mygo.MaxSecretSize)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = cloneBytes(value)
	return nil
}

func (m *colabOneMemorySecretBackend) Delete(key string) error {
	if err := validateVaultKey(key); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

type colabOneChunkManifest struct {
	Chunks int   `json:"__colabone_chunks__"`
	Gen    int64 `json:"gen,omitempty"`
}

type colabOneLegacyReaderFunc func(service, key string) ([]byte, bool, error)
type colabOneLegacyDeleterFunc func(service, key string) error

// ColabOneVault stores JSON-serialized credentials in an OS credential store
// with an in-memory cache, transparent chunking for secrets > 2400 bytes, and
// optional legacy Windows keyring migration.
type ColabOneVault struct {
	mu            sync.RWMutex
	service       string
	backend       ColabOneSecretBackend
	cache         map[string][]byte
	chunkMeta     map[string]colabOneChunkManifest
	legacyReader  colabOneLegacyReaderFunc
	legacyDeleter colabOneLegacyDeleterFunc
}

// NewColabOneVault creates a ColabOneVault using the provided secret backend.
// If backend is nil, it falls back to NewColabOneMyGoSecretBackend(ColabOneVaultService).
func NewColabOneVault(backend ColabOneSecretBackend) *ColabOneVault {
	if backend == nil {
		backend = NewColabOneMyGoSecretBackend(ColabOneVaultService)
	}
	return &ColabOneVault{
		service:   ColabOneVaultService,
		backend:   backend,
		cache:     make(map[string][]byte),
		chunkMeta: make(map[string]colabOneChunkManifest),
	}
}

// NewDefaultColabOneVault creates the production ColabOneVault backed by
// mygo.Secrets.Service("ColabOne") with Windows legacy Python keyring migration.
func NewDefaultColabOneVault() *ColabOneVault {
	v := NewColabOneVault(NewColabOneMyGoSecretBackend(ColabOneVaultService))
	v.legacyReader = colabOneReadLegacySecret
	v.legacyDeleter = colabOneDeleteLegacySecret
	return v
}

// NewMemoryVault creates an isolated in-memory ColabOneVault for offline unit tests.
func NewMemoryVault() *ColabOneVault {
	return NewColabOneVault(newColabOneMemorySecretBackend())
}

// GetRaw returns the raw JSON bytes stored under name.
// It returns (nil, false, nil) when the key is absent.
func (v *ColabOneVault) GetRaw(name string) ([]byte, bool, error) {
	if err := validateVaultKey(name); err != nil {
		return nil, false, err
	}

	v.mu.RLock()
	if cached, ok := v.cache[name]; ok {
		cloned := cloneBytes(cached)
		v.mu.RUnlock()
		if cloned == nil {
			return nil, false, nil
		}
		return cloned, true, nil
	}
	v.mu.RUnlock()

	v.mu.Lock()
	defer v.mu.Unlock()

	if cached, ok := v.cache[name]; ok {
		if cached == nil {
			return nil, false, nil
		}
		return cloneBytes(cached), true, nil
	}

	raw, err := v.backend.Get(name)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return nil, false, err
		}

		// On Windows, transparently migrate credentials written by the legacy
		// Python keyring backend (UTF-16LE blobs under key@ColabOne or ColabOne).
		if v.legacyReader != nil {
			legacyBytes, found, legacyErr := v.legacyReader(v.service, name)
			if legacyErr == nil && found && len(legacyBytes) > 0 {
				meta, migErr := v.writeBackendLocked(name, legacyBytes, colabOneChunkManifest{})
				if migErr == nil {
					v.chunkMeta[name] = meta
				}
				v.cache[name] = cloneBytes(legacyBytes)
				return cloneBytes(legacyBytes), true, nil
			}
		}

		if !colabOneIsEphemeralVaultKey(name) {
			v.cache[name] = nil
			v.chunkMeta[name] = colabOneChunkManifest{}
		}
		return nil, false, nil
	}

	if len(raw) == 0 {
		if !colabOneIsEphemeralVaultKey(name) {
			v.cache[name] = nil
			v.chunkMeta[name] = colabOneChunkManifest{}
		}
		return nil, false, nil
	}

	if manifest, isChunked := parseChunkManifest(raw); isChunked {
		assembled, err := v.readChunksLocked(name, manifest)
		if err != nil {
			return nil, false, err
		}
		if len(assembled) == 0 {
			if !colabOneIsEphemeralVaultKey(name) {
				v.cache[name] = nil
				v.chunkMeta[name] = manifest
			}
			return nil, false, nil
		}
		v.cache[name] = cloneBytes(assembled)
		v.chunkMeta[name] = manifest
		return cloneBytes(assembled), true, nil
	}

	v.cache[name] = cloneBytes(raw)
	v.chunkMeta[name] = colabOneChunkManifest{}
	return cloneBytes(raw), true, nil
}

// GetJSON reads the secret under name and unmarshals it into dest.
// Returns (false, nil) when the key is absent.
func (v *ColabOneVault) GetJSON(name string, dest any) (bool, error) {
	raw, ok, err := v.GetRaw(name)
	if err != nil || !ok {
		return false, err
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return false, err
	}
	return true, nil
}

// GetAny returns the JSON-decoded value for name and whether a valid entry exists.
func (v *ColabOneVault) GetAny(name string) (any, bool) {
	raw, ok, err := v.GetRaw(name)
	if err != nil || !ok {
		return nil, false
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false
	}
	return out, true
}

// GetString returns the stored string value for name, or defaultVal if absent,
// null, or not a JSON string.
func (v *ColabOneVault) GetString(name, defaultVal string) string {
	raw, ok, err := v.GetRaw(name)
	if err != nil || !ok {
		return defaultVal
	}
	var s *string
	if err := json.Unmarshal(raw, &s); err != nil || s == nil {
		return defaultVal
	}
	return *s
}

// GetMap returns a fresh JSON-decoded map for name, or nil if absent or not a JSON object.
func (v *ColabOneVault) GetMap(name string) map[string]any {
	raw, ok, err := v.GetRaw(name)
	if err != nil || !ok {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// GetStringSlice returns a fresh JSON-decoded []string for name, or nil if absent
// or not a JSON array of strings.
func (v *ColabOneVault) GetStringSlice(name string) []string {
	raw, ok, err := v.GetRaw(name)
	if err != nil || !ok {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// Set JSON-encodes value, writes it to the credential store (transparently
// chunking payloads larger than 2400 bytes), removes any superseded legacy
// Windows keyring entry, cleans up any obsolete chunks, and updates the
// in-memory cache.
func (v *ColabOneVault) Set(name string, value any) error {
	if err := validateVaultKey(name); err != nil {
		return err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	prev := v.existingChunkManifestLocked(name)
	nextMeta, err := v.writeBackendLocked(name, encoded, prev)
	if err != nil {
		return err
	}

	if v.legacyDeleter != nil {
		_ = v.legacyDeleter(v.service, name)
	}

	v.cache[name] = cloneBytes(encoded)
	v.chunkMeta[name] = nextMeta
	return nil
}

// Delete removes name and any associated chunk entries from the credential store
// and marks persistent keys absent in the in-memory cache while evicting
// ephemeral per-endpoint keys to keep cache and chunk metadata bounded.
func (v *ColabOneVault) Delete(name string) error {
	if err := validateVaultKey(name); err != nil {
		return err
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	deletedMain, err := v.deleteBackendLocked(name)
	if !deletedMain {
		return err
	}

	if v.legacyDeleter != nil {
		_ = v.legacyDeleter(v.service, name)
	}

	if colabOneIsEphemeralVaultKey(name) {
		delete(v.cache, name)
		delete(v.chunkMeta, name)
	} else {
		v.cache[name] = nil
		v.chunkMeta[name] = colabOneChunkManifest{}
	}
	return err
}

func (v *ColabOneVault) existingChunkManifestLocked(name string) colabOneChunkManifest {
	if meta, ok := v.chunkMeta[name]; ok {
		return meta
	}
	raw, err := v.backend.Get(name)
	if err != nil {
		return colabOneChunkManifest{}
	}
	if manifest, ok := parseChunkManifest(raw); ok {
		return manifest
	}
	return colabOneChunkManifest{}
}

func (v *ColabOneVault) writeBackendLocked(name string, encoded []byte, prev colabOneChunkManifest) (colabOneChunkManifest, error) {
	if len(encoded) <= colabOneVaultChunkSize {
		if err := v.backend.Set(name, encoded); err != nil {
			return prev, err
		}
		for i := 0; i < prev.Chunks; i++ {
			_ = v.backend.Delete(colabOneChunkKey(name, prev.Gen, i))
		}
		return colabOneChunkManifest{}, nil
	}

	newChunks := (len(encoded) + colabOneVaultChunkSize - 1) / colabOneVaultChunkSize
	if newChunks > colabOneVaultMaxChunks {
		return prev, fmt.Errorf("colabone vault: secret %q requires %d chunks (max %d)", name, newChunks, colabOneVaultMaxChunks)
	}

	nextGen := prev.Gen + 1
	if nextGen <= 0 {
		nextGen = 1
	}

	for i := 0; i < newChunks; i++ {
		start := i * colabOneVaultChunkSize
		end := start + colabOneVaultChunkSize
		if end > len(encoded) {
			end = len(encoded)
		}
		if err := v.backend.Set(colabOneChunkKey(name, nextGen, i), encoded[start:end]); err != nil {
			// Clean up newly written chunks of nextGen so we don't leak orphans;
			// the previous manifest and previous generation's chunks stay untouched.
			for j := 0; j < i; j++ {
				_ = v.backend.Delete(colabOneChunkKey(name, nextGen, j))
			}
			return prev, err
		}
	}

	nextManifest := colabOneChunkManifest{Chunks: newChunks, Gen: nextGen}
	manifestBytes, err := json.Marshal(nextManifest)
	if err != nil {
		for j := 0; j < newChunks; j++ {
			_ = v.backend.Delete(colabOneChunkKey(name, nextGen, j))
		}
		return prev, err
	}
	if err := v.backend.Set(name, manifestBytes); err != nil {
		for j := 0; j < newChunks; j++ {
			_ = v.backend.Delete(colabOneChunkKey(name, nextGen, j))
		}
		return prev, err
	}

	for i := 0; i < prev.Chunks; i++ {
		_ = v.backend.Delete(colabOneChunkKey(name, prev.Gen, i))
	}
	return nextManifest, nil
}

func (v *ColabOneVault) deleteBackendLocked(name string) (bool, error) {
	prev := v.existingChunkManifestLocked(name)

	if err := v.backend.Delete(name); err != nil && !errors.Is(err, ErrNotFound) {
		return false, err
	}

	var firstErr error
	for i := 0; i < prev.Chunks; i++ {
		if err := v.backend.Delete(colabOneChunkKey(name, prev.Gen, i)); err != nil && !errors.Is(err, ErrNotFound) {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return true, firstErr
}

func (v *ColabOneVault) readChunksLocked(name string, manifest colabOneChunkManifest) ([]byte, error) {
	buf := make([]byte, 0, manifest.Chunks*colabOneVaultChunkSize)
	for i := 0; i < manifest.Chunks; i++ {
		part, err := v.backend.Get(colabOneChunkKey(name, manifest.Gen, i))
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, fmt.Errorf("colabone vault: missing chunk %d/%d for %q: %w", i, manifest.Chunks, name, ErrNotFound)
			}
			return nil, err
		}
		buf = append(buf, part...)
	}
	return buf, nil
}

func colabOneChunkKey(name string, gen int64, index int) string {
	if gen <= 0 {
		return name + colabOneChunkKeySep + strconv.Itoa(index)
	}
	return name + colabOneChunkKeySep + strconv.FormatInt(gen, 10) + ":" + strconv.Itoa(index)
}

func parseChunkManifest(raw []byte) (colabOneChunkManifest, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || len(trimmed) > colabOneChunkManifestMaxBytes || trimmed[0] != '{' {
		return colabOneChunkManifest{}, false
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var manifest colabOneChunkManifest
	if err := dec.Decode(&manifest); err != nil {
		return colabOneChunkManifest{}, false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return colabOneChunkManifest{}, false
	}
	if manifest.Chunks <= 0 || manifest.Chunks > colabOneVaultMaxChunks {
		return colabOneChunkManifest{}, false
	}
	if manifest.Gen < 0 {
		return colabOneChunkManifest{}, false
	}
	return manifest, true
}

func colabOneIsEphemeralVaultKey(name string) bool {
	return strings.HasPrefix(name, "runtime_session:") || strings.HasPrefix(name, "remote:")
}

func validateVaultKey(name string) error {
	if name == "" {
		return errors.New("colabone vault: empty secret key")
	}
	if strings.ContainsRune(name, 0) {
		return fmt.Errorf("colabone vault: secret key %q contains NUL", name)
	}
	return nil
}

func cloneBytes(src []byte) []byte {
	if src == nil {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}

// decodeUTF16LE converts a Windows Credential Manager UTF-16LE blob
// (as written by Python keyring's WinVaultKeyring) into UTF-8 bytes,
// rejecting odd lengths and unpaired UTF-16 surrogates.
func decodeUTF16LE(blob []byte) ([]byte, error) {
	if len(blob) == 0 {
		return nil, nil
	}
	if len(blob)%2 != 0 {
		return nil, fmt.Errorf("colabone vault: invalid UTF-16LE byte length %d", len(blob))
	}
	units := make([]uint16, len(blob)/2)
	for i := range units {
		units[i] = uint16(blob[2*i]) | uint16(blob[2*i+1])<<8
	}
	if len(units) > 0 && units[0] == 0xFEFF {
		units = units[1:]
	}
	for len(units) > 0 && units[len(units)-1] == 0 {
		units = units[:len(units)-1]
	}
	if len(units) == 0 {
		return nil, nil
	}
	for i := 0; i < len(units); i++ {
		u := units[i]
		if u >= 0xD800 && u <= 0xDBFF {
			if i+1 >= len(units) || units[i+1] < 0xDC00 || units[i+1] > 0xDFFF {
				return nil, fmt.Errorf("colabone vault: unpaired UTF-16 high surrogate at unit %d", i)
			}
			i++
		} else if u >= 0xDC00 && u <= 0xDFFF {
			return nil, fmt.Errorf("colabone vault: unpaired UTF-16 low surrogate at unit %d", i)
		}
	}
	return []byte(string(utf16.Decode(units))), nil
}

// decodeLegacyWinBlob decodes a Windows Credential Manager blob written by
// Python keyring's WinVaultKeyring (UTF-16LE normally, or raw UTF-8 bytes
// including even-length UTF-8 JSON payloads).
func decodeLegacyWinBlob(blob []byte) ([]byte, error) {
	if len(blob) == 0 {
		return nil, nil
	}
	if len(blob) >= 2 && blob[0] == 0xFF && blob[1] == 0xFE {
		return decodeUTF16LE(blob)
	}
	if len(blob) >= 3 && blob[0] == 0xEF && blob[1] == 0xBB && blob[2] == 0xBF {
		trimmed := bytes.TrimRight(blob[3:], "\x00")
		if len(trimmed) > 0 && utf8.Valid(trimmed) {
			return cloneBytes(trimmed), nil
		}
		return nil, fmt.Errorf("colabone vault: invalid UTF-8 BOM legacy credential (%d bytes)", len(blob))
	}

	utf16Bytes, utf16Err := decodeUTF16LE(blob)
	utf8Trimmed := bytes.TrimRight(blob, "\x00")
	utf8OK := len(utf8Trimmed) > 0 && utf8.Valid(utf8Trimmed)

	// Prefer whichever decoding yields valid JSON (distinguishes UTF-16LE JSON
	// from even-length UTF-8 JSON without corrupting either).
	if utf16Err == nil && len(utf16Bytes) > 0 && json.Valid(utf16Bytes) {
		return utf16Bytes, nil
	}
	if utf8OK && json.Valid(utf8Trimmed) {
		return cloneBytes(utf8Trimmed), nil
	}
	// For non-JSON secrets (tokens/passwords), UTF-16LE ASCII payloads contain
	// NUL high bytes, whereas plain UTF-8 secrets do not.
	if utf16Err == nil && bytes.IndexByte(blob, 0) >= 0 {
		return utf16Bytes, nil
	}
	if utf8OK && !bytes.ContainsRune(utf8Trimmed, 0) {
		return cloneBytes(utf8Trimmed), nil
	}
	if utf16Err == nil {
		return utf16Bytes, nil
	}
	return nil, fmt.Errorf("colabone vault: invalid legacy credential encoding (%d bytes)", len(blob))
}

// colabOneResolveLegacyWinCredential implements Python keyring's WinVaultKeyring
// lookup order: TargetName = service (requiring UserName == key) FIRST — because
// WinVaultKeyring.set_password writes the newest credential to bare service and
// moves the previous credential to key + "@" + service — and only falls back to
// TargetName = key + "@" + service if bare service is absent or belongs to
// another key.
func colabOneResolveLegacyWinCredential(
	service, key string,
	readTarget func(target string) (blob []byte, userName string, ok bool, err error),
) ([]byte, bool, error) {
	if service == "" || key == "" || readTarget == nil {
		return nil, false, nil
	}
	if blob, userName, ok, err := readTarget(service); err == nil && ok && userName == key && len(blob) > 0 {
		if utf8Bytes, decErr := decodeLegacyWinBlob(blob); decErr == nil && len(utf8Bytes) > 0 {
			return utf8Bytes, true, nil
		}
	}
	if blob, _, ok, err := readTarget(key + "@" + service); err == nil && ok && len(blob) > 0 {
		if utf8Bytes, decErr := decodeLegacyWinBlob(blob); decErr == nil && len(utf8Bytes) > 0 {
			return utf8Bytes, true, nil
		}
	}
	return nil, false, nil
}

func colabOneRemoveLegacyWinCredential(
	service, key string,
	readTarget func(target string) (blob []byte, userName string, ok bool, err error),
	deleteTarget func(target string) error,
) error {
	if service == "" || key == "" || deleteTarget == nil {
		return nil
	}
	_ = deleteTarget(key + "@" + service)
	if readTarget != nil {
		if _, userName, ok, err := readTarget(service); err == nil && ok && userName == key {
			_ = deleteTarget(service)
		}
	}
	return nil
}
