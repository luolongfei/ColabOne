// Package migration implements encrypted one-click backup and cross-machine
// migration archives (.colabone) for ColabOne using Argon2id key derivation
// and AES-256-GCM authenticated encryption over gzip-compressed JSON payloads.
package migration

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	// COLABONE_MIGRATION_FORMAT identifies ColabOne encrypted migration archives.
	COLABONE_MIGRATION_FORMAT = "colabone-migration"

	// COLABONE_MIGRATION_SCHEMA_VERSION is the current migration archive schema version.
	COLABONE_MIGRATION_SCHEMA_VERSION = 1

	// COLABONE_MIGRATION_FILE_EXT is the canonical extension (without dot) for migration files.
	COLABONE_MIGRATION_FILE_EXT = "colabone"

	// COLABONE_MIGRATION_MIN_PASSWORD_LEN is the minimum rune length for a migration password.
	COLABONE_MIGRATION_MIN_PASSWORD_LEN = 6

	// COLABONE_MIGRATION_MAX_PASSWORD_LEN is the maximum byte length for a migration password.
	COLABONE_MIGRATION_MAX_PASSWORD_LEN = 512

	// COLABONE_MIGRATION_MAX_FILE_BYTES caps raw .colabone file size read from disk (16 MiB).
	COLABONE_MIGRATION_MAX_FILE_BYTES = 16 * 1024 * 1024

	// COLABONE_MIGRATION_MAX_PLAINTEXT_BYTES caps decompressed inner JSON size (32 MiB).
	COLABONE_MIGRATION_MAX_PLAINTEXT_BYTES = 32 * 1024 * 1024

	colabOneKDFAlgorithmArgon2id = "argon2id"
	colabOneCipherAlgAES256GCM   = "aes-256-gcm"
	colabOneCompressionGzip      = "gzip"
	colabOneCompressionNone      = "none"

	// RFC 9106 Section 4 second recommended Argon2id parameters: t=3, m=64 MiB, p=4.
	colabOneDefaultArgon2Time      uint32 = 3
	colabOneDefaultArgon2MemoryKiB uint32 = 64 * 1024
	colabOneDefaultArgon2Threads   uint8  = 4
	colabOneAES256KeyLen           uint32 = 32
	colabOneSaltLen                       = 16
	colabOneGCMNonceLen                   = 12

	// Defensive bounds enforced on untrusted .colabone envelopes before running Argon2id.
	colabOneMinArgon2Time      uint32 = 1
	colabOneMaxArgon2Time      uint32 = 10
	colabOneMinArgon2MemoryKiB uint32 = 8 * 1024
	colabOneMaxArgon2MemoryKiB uint32 = 256 * 1024
	colabOneMinArgon2Threads   uint8  = 1
	colabOneMaxArgon2Threads   uint8  = 16
	colabOneMinSaltLen                = 16
	colabOneMaxSaltLen                = 64
)

var (
	// ErrPasswordTooShort is returned when the supplied migration password is shorter than 6 characters.
	ErrPasswordTooShort = errors.New("迁移密码至少需要 6 位字符")
	// ErrPasswordTooLong is returned when the supplied migration password exceeds the maximum length.
	ErrPasswordTooLong = errors.New("迁移密码过长，请缩短后重试")
	// ErrInvalidEnvelope is returned when the input file is not a valid .colabone archive envelope.
	ErrInvalidEnvelope = errors.New("不是有效的 ColabOne 迁移文件（.colabone）")
	// ErrUnsupportedVersion is returned when the file uses a newer or unsupported schema version.
	ErrUnsupportedVersion = errors.New("该迁移文件版本高于当前客户端支持范围，请先升级 ColabOne 后再导入")
	// ErrFileTooLarge is returned when the selected .colabone file exceeds the safety size limit.
	ErrFileTooLarge = errors.New("迁移文件体积超过上限（最大 16 MB）")
	// ErrPayloadTooLarge is returned when the decompressed payload exceeds the safety size limit.
	ErrPayloadTooLarge = errors.New("迁移文件解压后体积超过上限")
	// ErrWrongPassword is returned when AES-256-GCM authentication fails (incorrect password or tampered ciphertext).
	ErrWrongPassword = errors.New("迁移密码错误或文件已被篡改，请重试")
	// ErrCorruptPayload is returned when authenticated plaintext cannot be decompressed or parsed.
	ErrCorruptPayload = errors.New("迁移文件内容已损坏或格式无效")
)

// ColabOneMigrationKDFParams controls Argon2id derivation costs when sealing archives.
type ColabOneMigrationKDFParams struct {
	Time      uint32
	MemoryKiB uint32
	Threads   uint8
}

// DefaultKDFParams returns the production RFC 9106 Argon2id parameters.
func DefaultKDFParams() ColabOneMigrationKDFParams {
	return ColabOneMigrationKDFParams{
		Time:      colabOneDefaultArgon2Time,
		MemoryKiB: colabOneDefaultArgon2MemoryKiB,
		Threads:   colabOneDefaultArgon2Threads,
	}
}

// ColabOneMigrationKDF describes the key derivation parameters stored in the outer envelope.
type ColabOneMigrationKDF struct {
	Algorithm string `json:"algorithm"`
	Salt      string `json:"salt"`
	Time      uint32 `json:"time"`
	MemoryKiB uint32 `json:"memory_kib"`
	Threads   uint8  `json:"threads"`
}

// ColabOneMigrationCipher describes the authenticated encryption parameters stored in the outer envelope.
type ColabOneMigrationCipher struct {
	Algorithm   string `json:"algorithm"`
	Nonce       string `json:"nonce"`
	Compression string `json:"compression"`
}

// ColabOneMigrationEnvelope is the outer UTF-8 JSON structure written to .colabone files.
// It contains zero PII or secret material — only cryptographic header fields and AEAD ciphertext.
type ColabOneMigrationEnvelope struct {
	Format        string                  `json:"format"`
	SchemaVersion int                     `json:"schema_version"`
	KDF           ColabOneMigrationKDF    `json:"kdf"`
	Cipher        ColabOneMigrationCipher `json:"cipher"`
	Ciphertext    string                  `json:"ciphertext"`
}

// ColabOneMigrationHeaderInfo summarizes validated outer-envelope metadata before decryption.
type ColabOneMigrationHeaderInfo struct {
	Format        string `json:"format"`
	SchemaVersion int    `json:"schema_version"`
	FileSize      int64  `json:"file_size"`
}

// ValidatePassword checks whether a user-supplied export password satisfies length constraints.
func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < COLABONE_MIGRATION_MIN_PASSWORD_LEN || strings.TrimSpace(password) == "" {
		return ErrPasswordTooShort
	}
	if len(password) > COLABONE_MIGRATION_MAX_PASSWORD_LEN {
		return ErrPasswordTooLong
	}
	return nil
}

// InspectEnvelope parses and validates the outer .colabone JSON envelope without
// attempting decryption or requiring a password.
func InspectEnvelope(raw []byte) (*ColabOneMigrationHeaderInfo, error) {
	env, _, _, _, err := parseAndValidateEnvelope(raw)
	if err != nil {
		return nil, err
	}
	return &ColabOneMigrationHeaderInfo{
		Format:        env.Format,
		SchemaVersion: env.SchemaVersion,
		FileSize:      int64(len(raw)),
	}, nil
}

func parseAndValidateEnvelope(raw []byte) (env *ColabOneMigrationEnvelope, salt, nonce, ciphertext []byte, err error) {
	if int64(len(raw)) > COLABONE_MIGRATION_MAX_FILE_BYTES {
		return nil, nil, nil, nil, ErrFileTooLarge
	}
	trimmed := bytes.TrimSpace(raw)
	// Strip optional UTF-8 BOM (and any whitespace following it) if transferred through an editor/tool.
	trimmed = bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte{0xEF, 0xBB, 0xBF}))
	if len(trimmed) < 2 || trimmed[0] != '{' {
		return nil, nil, nil, nil, ErrInvalidEnvelope
	}

	var parsed ColabOneMigrationEnvelope
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&parsed); err != nil {
		return nil, nil, nil, nil, ErrInvalidEnvelope
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, nil, nil, nil, ErrInvalidEnvelope
	}

	if parsed.Format != COLABONE_MIGRATION_FORMAT {
		return nil, nil, nil, nil, ErrInvalidEnvelope
	}
	if parsed.SchemaVersion < 1 {
		return nil, nil, nil, nil, ErrInvalidEnvelope
	}
	if parsed.SchemaVersion > COLABONE_MIGRATION_SCHEMA_VERSION {
		return nil, nil, nil, nil, ErrUnsupportedVersion
	}
	if parsed.KDF.Algorithm != colabOneKDFAlgorithmArgon2id {
		return nil, nil, nil, nil, ErrInvalidEnvelope
	}
	if parsed.KDF.Time < colabOneMinArgon2Time || parsed.KDF.Time > colabOneMaxArgon2Time ||
		parsed.KDF.MemoryKiB < colabOneMinArgon2MemoryKiB || parsed.KDF.MemoryKiB > colabOneMaxArgon2MemoryKiB ||
		parsed.KDF.Threads < colabOneMinArgon2Threads || parsed.KDF.Threads > colabOneMaxArgon2Threads {
		return nil, nil, nil, nil, ErrInvalidEnvelope
	}
	salt, err = base64.StdEncoding.DecodeString(parsed.KDF.Salt)
	if err != nil || len(salt) < colabOneMinSaltLen || len(salt) > colabOneMaxSaltLen {
		return nil, nil, nil, nil, ErrInvalidEnvelope
	}
	if parsed.Cipher.Algorithm != colabOneCipherAlgAES256GCM {
		return nil, nil, nil, nil, ErrInvalidEnvelope
	}
	if parsed.Cipher.Compression != colabOneCompressionGzip && parsed.Cipher.Compression != colabOneCompressionNone {
		return nil, nil, nil, nil, ErrInvalidEnvelope
	}
	nonce, err = base64.StdEncoding.DecodeString(parsed.Cipher.Nonce)
	if err != nil || len(nonce) != colabOneGCMNonceLen {
		return nil, nil, nil, nil, ErrInvalidEnvelope
	}
	ciphertext, err = base64.StdEncoding.DecodeString(parsed.Ciphertext)
	if err != nil || len(ciphertext) < 16 {
		return nil, nil, nil, nil, ErrInvalidEnvelope
	}
	return &parsed, salt, nonce, ciphertext, nil
}

func buildEnvelopeAAD(env *ColabOneMigrationEnvelope) []byte {
	return []byte(fmt.Sprintf(
		"%s|v%d|%s|%s|%d|%d|%d|%s|%s",
		env.Format,
		env.SchemaVersion,
		env.KDF.Algorithm,
		env.KDF.Salt,
		env.KDF.Time,
		env.KDF.MemoryKiB,
		env.KDF.Threads,
		env.Cipher.Algorithm,
		env.Cipher.Compression,
	))
}

func compressGzip(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	gw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := gw.Write(data); err != nil {
		_ = gw.Close()
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decompressGzipBounded(data []byte, limit int64) ([]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, ErrCorruptPayload
	}
	defer func() { _ = gr.Close() }()

	out, err := io.ReadAll(io.LimitReader(gr, limit+1))
	if err != nil {
		return nil, ErrCorruptPayload
	}
	if int64(len(out)) > limit {
		return nil, ErrPayloadTooLarge
	}
	return out, nil
}

// SealPayload validates, compresses, encrypts, and serializes payload into a
// formatted UTF-8 JSON .colabone archive using production Argon2id parameters.
func SealPayload(payload *ColabOneMigrationPayload, password string) ([]byte, error) {
	return SealPayloadWithParams(payload, password, DefaultKDFParams())
}

// SealPayloadWithParams seals payload using explicit Argon2id parameters
// (clamped/validated within defensive bounds). Useful for deterministic tests.
func SealPayloadWithParams(payload *ColabOneMigrationPayload, password string, params ColabOneMigrationKDFParams) ([]byte, error) {
	if err := ValidatePassword(password); err != nil {
		return nil, err
	}
	if err := ValidatePayload(payload); err != nil {
		return nil, err
	}
	if params.Time < colabOneMinArgon2Time || params.Time > colabOneMaxArgon2Time ||
		params.MemoryKiB < colabOneMinArgon2MemoryKiB || params.MemoryKiB > colabOneMaxArgon2MemoryKiB ||
		params.Threads < colabOneMinArgon2Threads || params.Threads > colabOneMaxArgon2Threads {
		params = DefaultKDFParams()
	}

	var plainBuf bytes.Buffer
	enc := json.NewEncoder(&plainBuf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return nil, fmt.Errorf("migration: encode payload: %w", err)
	}
	plainJSON := bytes.TrimSuffix(plainBuf.Bytes(), []byte{'\n'})
	defer clear(plainJSON)

	if int64(len(plainJSON)) > COLABONE_MIGRATION_MAX_PLAINTEXT_BYTES {
		return nil, ErrPayloadTooLarge
	}

	compressed, err := compressGzip(plainJSON)
	if err != nil {
		return nil, fmt.Errorf("migration: compress payload: %w", err)
	}
	defer clear(compressed)

	salt := make([]byte, colabOneSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("migration: generate salt: %w", err)
	}
	nonce := make([]byte, colabOneGCMNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("migration: generate nonce: %w", err)
	}

	pwBytes := []byte(password)
	defer clear(pwBytes)
	key := argon2.IDKey(pwBytes, salt, params.Time, params.MemoryKiB, params.Threads, colabOneAES256KeyLen)
	defer clear(key)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("migration: init cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("migration: init gcm: %w", err)
	}

	env := ColabOneMigrationEnvelope{
		Format:        COLABONE_MIGRATION_FORMAT,
		SchemaVersion: COLABONE_MIGRATION_SCHEMA_VERSION,
		KDF: ColabOneMigrationKDF{
			Algorithm: colabOneKDFAlgorithmArgon2id,
			Salt:      base64.StdEncoding.EncodeToString(salt),
			Time:      params.Time,
			MemoryKiB: params.MemoryKiB,
			Threads:   params.Threads,
		},
		Cipher: ColabOneMigrationCipher{
			Algorithm:   colabOneCipherAlgAES256GCM,
			Nonce:       base64.StdEncoding.EncodeToString(nonce),
			Compression: colabOneCompressionGzip,
		},
	}

	aad := buildEnvelopeAAD(&env)
	sealed := gcm.Seal(nil, nonce, compressed, aad)
	env.Ciphertext = base64.StdEncoding.EncodeToString(sealed)

	var out bytes.Buffer
	outEnc := json.NewEncoder(&out)
	outEnc.SetEscapeHTML(false)
	outEnc.SetIndent("", "  ")
	if err := outEnc.Encode(env); err != nil {
		return nil, fmt.Errorf("migration: encode envelope: %w", err)
	}
	result := out.Bytes()
	if int64(len(result)) > COLABONE_MIGRATION_MAX_FILE_BYTES {
		return nil, ErrFileTooLarge
	}
	return result, nil
}

// OpenPayload verifies, decrypts, decompresses, and parses a .colabone archive.
func OpenPayload(raw []byte, password string) (*ColabOneMigrationPayload, error) {
	env, salt, nonce, ciphertext, err := parseAndValidateEnvelope(raw)
	if err != nil {
		return nil, err
	}
	if password == "" {
		return nil, ErrWrongPassword
	}
	if len(password) > COLABONE_MIGRATION_MAX_PASSWORD_LEN {
		return nil, ErrPasswordTooLong
	}

	pwBytes := []byte(password)
	defer clear(pwBytes)
	key := argon2.IDKey(pwBytes, salt, env.KDF.Time, env.KDF.MemoryKiB, env.KDF.Threads, colabOneAES256KeyLen)
	defer clear(key)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrInvalidEnvelope
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrInvalidEnvelope
	}

	aad := buildEnvelopeAAD(env)
	decrypted, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrWrongPassword
	}
	defer clear(decrypted)

	var plainJSON []byte
	switch env.Cipher.Compression {
	case colabOneCompressionGzip:
		plainJSON, err = decompressGzipBounded(decrypted, COLABONE_MIGRATION_MAX_PLAINTEXT_BYTES)
		if err != nil {
			return nil, err
		}
		defer clear(plainJSON)
	case colabOneCompressionNone:
		if int64(len(decrypted)) > COLABONE_MIGRATION_MAX_PLAINTEXT_BYTES {
			return nil, ErrPayloadTooLarge
		}
		plainJSON = decrypted
	default:
		return nil, ErrInvalidEnvelope
	}

	var payload ColabOneMigrationPayload
	dec := json.NewDecoder(bytes.NewReader(plainJSON))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return nil, ErrCorruptPayload
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, ErrCorruptPayload
	}
	if err := ValidatePayload(&payload); err != nil {
		return nil, err
	}
	return &payload, nil
}
