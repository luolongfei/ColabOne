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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestColabOneFileVaultEncryptsAtomicallyPersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	vaultPath := filepath.Join(dir, ColabOneVaultFilename)

	// Cold reads and deletes on an empty directory must NOT create vault.enc.
	v := NewFileVault(dir)
	if _, ok := v.GetAny("colab"); ok {
		t.Fatal("expected missing key on empty vault")
	}
	if err := v.Delete("colab"); err != nil {
		t.Fatalf("Delete on non-existent vault failed: %v", err)
	}
	if _, err := os.Stat(vaultPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("vault.enc should not exist before first write, stat err=%v", err)
	}
	keyPath := filepath.Join(dir, ColabOneVaultKeyFilename)
	if _, err := os.Stat(keyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("vault.key should not exist before first write, stat err=%v", err)
	}

	// Write small and large (>10KB) secrets; verify unchunked single-file persistence.
	largePayload := strings.Repeat("secret-payload-segment-", 600)
	if err := v.Set("colab", map[string]any{
		"token":         "ya29.super-secret-oauth-token-1234567890",
		"refresh_token": "1//0g-super-secret-refresh-token",
	}); err != nil {
		t.Fatalf("Set(colab) failed: %v", err)
	}
	if err := v.Set("hf", "hf_VerySecretHuggingFaceToken1234567890"); err != nil {
		t.Fatalf("Set(hf) failed: %v", err)
	}
	if err := v.Set("large_entry", map[string]any{"blob": largePayload}); err != nil {
		t.Fatalf("Set(large_entry) failed: %v", err)
	}

	info, err := os.Stat(vaultPath)
	if err != nil {
		t.Fatalf("expected vault.enc on disk: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("vault.enc mode = %o, want 0600", info.Mode().Perm())
	}

	diskBytes1, err := os.ReadFile(vaultPath)
	if err != nil {
		t.Fatalf("read vault.enc: %v", err)
	}
	for _, plaintext := range []string{
		"ya29.super-secret-oauth-token-1234567890",
		"1//0g-super-secret-refresh-token",
		"hf_VerySecretHuggingFaceToken1234567890",
		"secret-payload-segment-",
	} {
		if bytes.Contains(diskBytes1, []byte(plaintext)) {
			t.Fatalf("vault.enc leaked plaintext secret %q on disk", plaintext)
		}
	}

	var env1 colabOneVaultFileEnvelope
	if err := json.Unmarshal(diskBytes1, &env1); err != nil {
		t.Fatalf("vault.enc envelope invalid JSON: %v", err)
	}
	wantKeyWrap := colabOneVaultKeyWrapFileKEK
	if runtime.GOOS == "windows" {
		wantKeyWrap = colabOneVaultKeyWrapDPAPI
	}
	if env1.Format != ColabOneVaultFileFormat || env1.Version != colabOneVaultFileVersionV2 || env1.Algorithm != "aes-256-gcm" || env1.KeyWrap != wantKeyWrap {
		t.Fatalf("unexpected envelope metadata: %+v", env1)
	}
	if runtime.GOOS != "windows" {
		kInfo, err := os.Stat(keyPath)
		if err != nil {
			t.Fatalf("expected vault.key on disk: %v", err)
		}
		if kInfo.Mode().Perm() != 0o600 {
			t.Fatalf("vault.key mode = %o, want 0600", kInfo.Mode().Perm())
		}
		if kInfo.Size() != colabOneVaultKEKLen {
			t.Fatalf("vault.key size = %d, want %d", kInfo.Size(), colabOneVaultKEKLen)
		}
	} else if env1.WrappedKey == "" {
		t.Fatal("expected non-empty wrapped_key on Windows DPAPI v2 envelope")
	}

	// Re-writing the exact same value must use a fresh salt + nonce and produce distinct ciphertext.
	if err := v.Set("hf", "hf_VerySecretHuggingFaceToken1234567890"); err != nil {
		t.Fatalf("re-Set(hf) failed: %v", err)
	}
	diskBytes2, err := os.ReadFile(vaultPath)
	if err != nil {
		t.Fatalf("read vault.enc after re-Set: %v", err)
	}
	var env2 colabOneVaultFileEnvelope
	if err := json.Unmarshal(diskBytes2, &env2); err != nil {
		t.Fatalf("second envelope invalid JSON: %v", err)
	}
	if env1.Salt == env2.Salt || env1.Nonce == env2.Nonce || env1.Ciphertext == env2.Ciphertext {
		t.Fatal("expected fresh salt, nonce, and ciphertext on every write")
	}

	// Cold reload from disk must restore all secrets identically.
	cold := NewFileVault(dir)
	gotColab := cold.GetMap("colab")
	if gotColab == nil || gotColab["token"] != "ya29.super-secret-oauth-token-1234567890" {
		t.Fatalf("cold GetMap(colab) = %#v", gotColab)
	}
	if got := cold.GetString("hf", ""); got != "hf_VerySecretHuggingFaceToken1234567890" {
		t.Fatalf("cold GetString(hf) = %q", got)
	}
	gotLarge := cold.GetMap("large_entry")
	if gotLarge == nil || gotLarge["blob"] != largePayload {
		t.Fatalf("cold GetMap(large_entry) mismatch")
	}

	// Deleting a key persists across cold reloads.
	if err := cold.Delete("hf"); err != nil {
		t.Fatalf("Delete(hf) failed: %v", err)
	}
	cold2 := NewFileVault(dir)
	if _, ok := cold2.GetAny("hf"); ok {
		t.Fatal("deleted key hf resurrected after cold reload")
	}
	if cold2.GetMap("colab")["token"] != "ya29.super-secret-oauth-token-1234567890" {
		t.Fatal("deleting hf affected colab entry")
	}
}

func TestColabOneFileVaultTamperDetectionAndCorruptRecovery(t *testing.T) {
	dir := t.TempDir()
	vaultPath := filepath.Join(dir, ColabOneVaultFilename)

	v := NewFileVault(dir)
	if err := v.Set("hf", "hf_originalToken999"); err != nil {
		t.Fatal(err)
	}

	// Tamper with ciphertext on disk -> cold GetRaw must return ErrCorruptVault without deleting the file.
	raw, err := os.ReadFile(vaultPath)
	if err != nil {
		t.Fatal(err)
	}
	var env colabOneVaultFileEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	env.Ciphertext = "AAAA" + env.Ciphertext[4:]
	tampered, _ := json.MarshalIndent(env, "", "  ")
	if err := os.WriteFile(vaultPath, tampered, 0o600); err != nil {
		t.Fatal(err)
	}

	cold := NewFileVault(dir)
	_, ok, getErr := cold.GetRaw("hf")
	if ok || !errors.Is(getErr, ErrCorruptVault) {
		t.Fatalf("GetRaw on tampered vault = (ok=%v, err=%v), want ErrCorruptVault", ok, getErr)
	}
	// File must still exist untouched after read-only GetRaw.
	if _, err := os.Stat(vaultPath); err != nil {
		t.Fatalf("tampered vault.enc removed during read: %v", err)
	}

	// Writing a fresh login/backup must quarantine the corrupt file and succeed cleanly.
	if err := cold.Set("hf", "hf_recoveredToken123"); err != nil {
		t.Fatalf("Set after corruption failed: %v", err)
	}
	corruptMatches, _ := filepath.Glob(vaultPath + ".corrupt.*")
	if len(corruptMatches) != 1 {
		t.Fatalf("expected 1 quarantined corrupt backup, found %v", corruptMatches)
	}
	if got := NewFileVault(dir).GetString("hf", ""); got != "hf_recoveredToken123" {
		t.Fatalf("recovered vault hf = %q, want hf_recoveredToken123", got)
	}
}

func TestColabOneFileVaultMigratesPriorWindowsMyGoSingleAndChunkedEntries(t *testing.T) {
	dir := t.TempDir()

	largeVal := map[string]any{"blob": strings.Repeat("win-mygo-chunk-", 300)}
	largeJSON, err := json.Marshal(largeVal)
	if err != nil {
		t.Fatal(err)
	}
	splitAt := 2400
	manifestJSON, _ := json.Marshal(colabOneChunkManifest{Chunks: 2, Gen: 1})

	type winEntry struct {
		blob     []byte
		userName string
	}
	winStore := map[string]winEntry{
		// Prior pure-Go MyGo Credential Manager target ("ColabOne/colab")
		"ColabOne/colab": {
			blob:     []byte(`{"token":"ya29.mygo-win-newest"}`),
			userName: "colab",
		},
		// Older legacy Python keyring target should lose to newer MyGo target
		"colab@ColabOne": {
			blob:     encodeUTF16LE(`{"token":"ya29.python-oldest"}`),
			userName: "colab",
		},
		// Chunked MyGo Windows Credential Manager target
		"ColabOne/big_cred": {
			blob:     manifestJSON,
			userName: "big_cred",
		},
		"ColabOne/big_cred#chunk:1:0": {
			blob:     largeJSON[:splitAt],
			userName: "big_cred#chunk:1:0",
		},
		"ColabOne/big_cred#chunk:1:1": {
			blob:     largeJSON[splitAt:],
			userName: "big_cred#chunk:1:1",
		},
	}

	readWin := func(target string) ([]byte, string, bool, error) {
		e, ok := winStore[target]
		if !ok {
			return nil, "", false, nil
		}
		return e.blob, e.userName, true, nil
	}
	delWin := func(target string) error {
		delete(winStore, target)
		return nil
	}

	v := NewFileVault(dir)
	v.legacyReader = func(service, key string) ([]byte, bool, error) {
		return colabOneResolveLegacyWinCredential(service, key, readWin)
	}
	v.legacyDeleter = func(service, key string) error {
		return colabOneRemoveLegacyWinCredential(service, key, readWin, delWin)
	}

	// 1. Prior MyGo target takes precedence over older Python target and migrates into vault.enc.
	gotColab := v.GetMap("colab")
	if gotColab == nil || gotColab["token"] != "ya29.mygo-win-newest" {
		t.Fatalf("migrated colab = %#v, want ya29.mygo-win-newest", gotColab)
	}

	// 2. Chunked MyGo target is reassembled and written unchunked into vault.enc.
	gotBig := v.GetMap("big_cred")
	if gotBig == nil || gotBig["blob"] != largeVal["blob"] {
		t.Fatalf("migrated chunked big_cred mismatch")
	}

	// 3. Cold file vault WITHOUT legacyReader reads both keys directly from vault.enc.
	cold := NewFileVault(dir)
	if cold.GetMap("colab")["token"] != "ya29.mygo-win-newest" {
		t.Fatal("cold file vault failed to read migrated colab")
	}
	if cold.GetMap("big_cred")["blob"] != largeVal["blob"] {
		t.Fatal("cold file vault failed to read migrated big_cred")
	}

	// 4. Deleting keys cleans up both MyGo chunk targets and legacy Python targets.
	if err := v.Delete("colab"); err != nil {
		t.Fatalf("Delete(colab) failed: %v", err)
	}
	if err := v.Delete("big_cred"); err != nil {
		t.Fatalf("Delete(big_cred) failed: %v", err)
	}
	if len(winStore) != 0 {
		t.Fatalf("expected all legacy Windows entries removed, remaining: %#v", winStore)
	}
}

func TestColabOneFileVaultConcurrencyRace(t *testing.T) {
	dir := t.TempDir()
	v := NewFileVault(dir)
	const workers = 8
	const iters = 20

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("k-%d", id%3)
			for i := 0; i < iters; i++ {
				switch i % 3 {
				case 0:
					if err := v.Set(key, map[string]any{"w": id, "i": i}); err != nil {
						t.Errorf("Set(%s) failed: %v", key, err)
					}
				case 1:
					_, _ = v.GetAny(key)
				case 2:
					if err := v.Delete(key); err != nil {
						t.Errorf("Delete(%s) failed: %v", key, err)
					}
				}
			}
		}(w)
	}
	wg.Wait()

	// Final vault.enc on disk (if created) must still be valid and readable on cold reload.
	if err := v.Set("final_check", "ok"); err != nil {
		t.Fatalf("final Set failed: %v", err)
	}
	if got := NewFileVault(dir).GetString("final_check", ""); got != "ok" {
		t.Fatalf("cold final_check = %q, want ok", got)
	}
}

func TestColabOneFileVaultRefusesSymlinkDirectoryAndNilReceiver(t *testing.T) {
	var nilBackend *ColabOneFileSecretBackend
	if _, err := nilBackend.Get("k"); err == nil {
		t.Fatal("expected error on nil backend Get")
	}
	if err := nilBackend.Set("k", []byte("v")); err == nil {
		t.Fatal("expected error on nil backend Set")
	}
	if err := nilBackend.Delete("k"); err == nil {
		t.Fatal("expected error on nil backend Delete")
	}

	// Directory at vault.enc must be refused for Get and Set.
	dirCase := t.TempDir()
	vaultAsDir := filepath.Join(dirCase, ColabOneVaultFilename)
	if err := os.MkdirAll(vaultAsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	vDir := NewFileVault(dirCase)
	if _, _, err := vDir.GetRaw("colab"); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("GetRaw on directory vault.enc got %v, want directory error", err)
	}
	if err := vDir.Set("colab", "x"); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("Set on directory vault.enc got %v, want directory error", err)
	}

	// Symlink at vault.enc must be refused without reading or overwriting the link target.
	linkCase := t.TempDir()
	outsideTarget := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outsideTarget, []byte("keep-intact"), 0o600); err != nil {
		t.Fatal(err)
	}
	vaultLink := filepath.Join(linkCase, ColabOneVaultFilename)
	if err := os.Symlink(outsideTarget, vaultLink); err == nil {
		vLink := NewFileVault(linkCase)
		if _, _, err := vLink.GetRaw("colab"); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("GetRaw on symlink vault.enc got %v, want symlink error", err)
		}
		if err := vLink.Set("colab", "overwrite"); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("Set on symlink vault.enc got %v, want symlink error", err)
		}
		got, _ := os.ReadFile(outsideTarget)
		if string(got) != "keep-intact" {
			t.Fatalf("symlink target modified: %q", string(got))
		}
	}

	// Delete on a corrupt vault.enc quarantines the file cleanly and leaves vault usable.
	delCase := t.TempDir()
	corruptPath := filepath.Join(delCase, ColabOneVaultFilename)
	if err := os.WriteFile(corruptPath, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	vCorrupt := NewFileVault(delCase)
	if err := vCorrupt.Delete("colab"); err != nil {
		t.Fatalf("Delete on corrupt vault.enc failed: %v", err)
	}
	if _, ok, err := vCorrupt.GetRaw("colab"); ok || err != nil {
		t.Fatalf("GetRaw after quarantining corrupt vault.enc = (%v, %v), want (false, nil)", ok, err)
	}
	if matches, _ := filepath.Glob(corruptPath + ".corrupt.*"); len(matches) != 1 {
		t.Fatalf("expected 1 quarantined file after Delete, got %v", matches)
	}
}

func writeLegacyV1VaultFile(t *testing.T, vaultPath, service string, entries map[string][]byte) {
	t.Helper()
	payload := colabOneVaultFilePayload{
		Version: colabOneVaultFileVersionV1,
		Entries: entries,
	}
	plain, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal v1 payload: %v", err)
	}
	salt := make([]byte, colabOneVaultSaltLen)
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, colabOneVaultNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	aad := []byte("colabone-vault:v1:" + service)
	derivedKey, err := hkdf.Key(sha256.New, colabOneLocalVaultDomainKey[:], salt, string(aad), 32)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(derivedKey)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := gcm.Seal(nil, nonce, plain, aad)
	env := colabOneVaultFileEnvelope{
		Format:     ColabOneVaultFileFormat,
		Version:    colabOneVaultFileVersionV1,
		Algorithm:  colabOneVaultFileAlgAES256GCM,
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}
	raw, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vaultPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestColabOneFileVaultV1BackwardCompatibilityAndUpgradeToV2(t *testing.T) {
	dir := t.TempDir()
	vaultPath := filepath.Join(dir, ColabOneVaultFilename)
	keyPath := filepath.Join(dir, ColabOneVaultKeyFilename)

	writeLegacyV1VaultFile(t, vaultPath, ColabOneVaultService, map[string][]byte{
		"colab": []byte(`{"token":"ya29.legacy-v1-token","refresh_token":"1//legacy-v1-refresh"}`),
		"hf":    []byte(`"hf_legacy_v1_token_12345"`),
	})

	// Reading legacy v1 works cleanly with zero vault.key present.
	v := NewFileVault(dir)
	if got := v.GetMap("colab"); got == nil || got["token"] != "ya29.legacy-v1-token" {
		t.Fatalf("v1 GetMap(colab) = %#v", got)
	}
	if got := v.GetString("hf", ""); got != "hf_legacy_v1_token_12345" {
		t.Fatalf("v1 GetString(hf) = %q", got)
	}
	if runtime.GOOS != "windows" {
		if _, err := os.Stat(keyPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read-only load of v1 must not create vault.key, stat err=%v", err)
		}
	}

	// Next write transparently upgrades on-disk vault.enc from v1 to v2.
	if err := v.Set("api_key", "co_upgraded_secret_value"); err != nil {
		t.Fatalf("Set during v1 upgrade failed: %v", err)
	}
	raw, err := os.ReadFile(vaultPath)
	if err != nil {
		t.Fatal(err)
	}
	var env colabOneVaultFileEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env.Version != colabOneVaultFileVersionV2 {
		t.Fatalf("expected upgraded envelope version 2, got %d", env.Version)
	}
	if runtime.GOOS != "windows" && env.KeyWrap != colabOneVaultKeyWrapFileKEK {
		t.Fatalf("expected key_wrap=%q, got %q", colabOneVaultKeyWrapFileKEK, env.KeyWrap)
	}
	if runtime.GOOS == "windows" && (env.KeyWrap != colabOneVaultKeyWrapDPAPI || env.WrappedKey == "") {
		t.Fatalf("expected Windows DPAPI v2 envelope, got %+v", env)
	}

	// Cold reload reads all preserved v1 entries plus new entry from v2.
	cold := NewFileVault(dir)
	if got := cold.GetMap("colab"); got == nil || got["token"] != "ya29.legacy-v1-token" {
		t.Fatalf("cold v2 GetMap(colab) = %#v", got)
	}
	if got := cold.GetString("hf", ""); got != "hf_legacy_v1_token_12345" {
		t.Fatalf("cold v2 GetString(hf) = %q", got)
	}
	if got := cold.GetString("api_key", ""); got != "co_upgraded_secret_value" {
		t.Fatalf("cold v2 GetString(api_key) = %q", got)
	}
}

func TestColabOneFileVaultV2TamperKeyValidationAndQuarantineError(t *testing.T) {
	// 1. Tampering key_wrap or deleting/corrupting vault.key triggers ErrCorruptVault and recovers on Set.
	dir := t.TempDir()
	vaultPath := filepath.Join(dir, ColabOneVaultFilename)
	keyPath := filepath.Join(dir, ColabOneVaultKeyFilename)

	v := NewFileVault(dir)
	if err := v.Set("hf", "hf_v2_secret"); err != nil {
		t.Fatal(err)
	}

	// Tamper key_wrap field in v2 envelope.
	raw, err := os.ReadFile(vaultPath)
	if err != nil {
		t.Fatal(err)
	}
	var env colabOneVaultFileEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	env.KeyWrap = "unknown_wrap"
	tampered, _ := json.MarshalIndent(env, "", "  ")
	if err := os.WriteFile(vaultPath, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, getErr := NewFileVault(dir).GetRaw("hf"); ok || !errors.Is(getErr, ErrCorruptVault) {
		t.Fatalf("expected ErrCorruptVault on tampered key_wrap, got ok=%v err=%v", ok, getErr)
	}

	if runtime.GOOS != "windows" {
		// Corrupting vault.key length fails cold read with ErrCorruptVault, and Set recovers cleanly.
		dir2 := t.TempDir()
		v2 := NewFileVault(dir2)
		if err := v2.Set("hf", "hf_v2_secret_2"); err != nil {
			t.Fatal(err)
		}
		kp2 := filepath.Join(dir2, ColabOneVaultKeyFilename)
		if err := os.WriteFile(kp2, []byte("short"), 0o600); err != nil {
			t.Fatal(err)
		}
		cold2 := NewFileVault(dir2)
		if _, ok, getErr := cold2.GetRaw("hf"); ok || !errors.Is(getErr, ErrCorruptVault) {
			t.Fatalf("expected ErrCorruptVault on truncated vault.key, got ok=%v err=%v", ok, getErr)
		}
		if err := cold2.Set("hf", "hf_recovered_v2"); err != nil {
			t.Fatalf("Set recovery after corrupt vault.key failed: %v", err)
		}
		if got := NewFileVault(dir2).GetString("hf", ""); got != "hf_recovered_v2" {
			t.Fatalf("recovered value = %q, want hf_recovered_v2", got)
		}

		// Symlink at vault.key must be refused.
		dir3 := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside.key")
		if err := os.WriteFile(outside, bytes.Repeat([]byte{0x42}, colabOneVaultKEKLen), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(dir3, ColabOneVaultKeyFilename)); err == nil {
			if err := NewFileVault(dir3).Set("hf", "x"); err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("expected symlink refusal for vault.key, got %v", err)
			}
		}

		// Set() must return quarantineCorruptFileLocked() error when quarantine rename fails.
		if os.Geteuid() != 0 {
			roDir := filepath.Join(t.TempDir(), "readonly")
			if err := os.MkdirAll(roDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(roDir, ColabOneVaultFilename), []byte("{corrupt"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(roDir, 0o500); err != nil {
				t.Fatal(err)
			}
			defer os.Chmod(roDir, 0o700)
			if err := NewFileVault(roDir).Set("hf", "new-val"); err == nil {
				t.Fatal("expected Set() to return quarantine error on read-only directory")
			}
		}
	}
	_ = keyPath
}

func TestColabOneFileVaultPostRenameSecureFailureInvalidatesCacheAndUnixFDChmod(t *testing.T) {
	t.Run("post-rename secure failure invalidates loaded cache for Set and Delete", func(t *testing.T) {
		dir := t.TempDir()
		backend := NewColabOneFileSecretBackend(filepath.Join(dir, ColabOneVaultFilename), ColabOneVaultService)
		if err := backend.Set("k1", []byte(`"v1"`)); err != nil {
			t.Fatalf("initial Set failed: %v", err)
		}

		origSecure := colabOneSecureVaultFinalPathFn
		t.Cleanup(func() { colabOneSecureVaultFinalPathFn = origSecure })

		injectedErr := errors.New("simulated post-rename DACL failure")
		colabOneSecureVaultFinalPathFn = func(string) error { return injectedErr }

		// Set commits the new file via rename before final-path securing fails.
		// The cache must be invalidated so Get reloads the committed disk state.
		if err := backend.Set("k1", []byte(`"v2"`)); !errors.Is(err, injectedErr) {
			t.Fatalf("Set error = %v, want %v", err, injectedErr)
		}
		got, err := backend.Get("k1")
		if err != nil || string(got) != `"v2"` {
			t.Fatalf("Get(k1) after post-rename Set failure = (%q, %v), want (\"v2\", nil)", string(got), err)
		}

		// Delete also commits removal via rename before final-path securing fails.
		if err := backend.Delete("k1"); !errors.Is(err, injectedErr) {
			t.Fatalf("Delete error = %v, want %v", err, injectedErr)
		}
		if gotAfterDel, err := backend.Get("k1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get(k1) after post-rename Delete failure = (%q, %v), want ErrNotFound", string(gotAfterDel), err)
		}
	})

	t.Run("unix readExistingFileKEKLocked tightens permissions via open file descriptor", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Unix file mode check applies to non-Windows builds")
		}
		dir := t.TempDir()
		v := NewFileVault(dir)
		if err := v.Set("hf", "hf_fd_chmod_secret"); err != nil {
			t.Fatalf("initial Set failed: %v", err)
		}
		kp := filepath.Join(dir, ColabOneVaultKeyFilename)
		if err := os.Chmod(kp, 0o644); err != nil {
			t.Fatalf("chmod 0644 failed: %v", err)
		}

		cold := NewFileVault(dir)
		if got := cold.GetString("hf", ""); got != "hf_fd_chmod_secret" {
			t.Fatalf("cold GetString(hf) = %q, want hf_fd_chmod_secret", got)
		}
		info, err := os.Stat(kp)
		if err != nil {
			t.Fatalf("stat vault.key: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("vault.key mode after cold load = %04o, want 0600", perm)
		}
	})
}

func TestColabOneFileVaultTagsStorageErrorsWithoutWrappingErrNotFound(t *testing.T) {
	dir := t.TempDir()
	backend := NewColabOneFileSecretBackend(filepath.Join(dir, ColabOneVaultFilename), ColabOneVaultService)

	// Missing key returns plain ErrNotFound (never tagged as vault storage error).
	_, err := backend.Get("colab")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing key err = %v, want ErrNotFound", err)
	}
	var vse interface{ IsVaultStorageError() bool }
	if errors.As(err, &vse) && vse.IsVaultStorageError() {
		t.Fatalf("ErrNotFound must NOT be tagged as IsVaultStorageError(): %v", err)
	}

	// Corrupt vault read returns an error matching both ErrCorruptVault and IsVaultStorageError().
	corruptDir := t.TempDir()
	corruptPath := filepath.Join(corruptDir, ColabOneVaultFilename)
	if err := os.WriteFile(corruptPath, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	corruptBackend := NewColabOneFileSecretBackend(corruptPath, ColabOneVaultService)
	_, err = corruptBackend.Get("colab")
	if !errors.Is(err, ErrCorruptVault) {
		t.Fatalf("corrupt Get err = %v, want ErrCorruptVault", err)
	}
	if !errors.As(err, &vse) || !vse.IsVaultStorageError() {
		t.Fatalf("corrupt Get err must satisfy IsVaultStorageError(): %v", err)
	}

	// Directory at vault.enc path fails Set with IsVaultStorageError() == true.
	dirTarget := filepath.Join(t.TempDir(), ColabOneVaultFilename)
	if err := os.MkdirAll(dirTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	dirBackend := NewColabOneFileSecretBackend(dirTarget, ColabOneVaultService)
	err = dirBackend.Set("colab", []byte(`"v"`))
	if err == nil || !errors.As(err, &vse) || !vse.IsVaultStorageError() {
		t.Fatalf("directory Set err = %v, want IsVaultStorageError()==true", err)
	}
}
