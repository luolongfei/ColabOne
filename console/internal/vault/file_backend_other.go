//go:build !windows

package vault

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (b *ColabOneFileSecretBackend) keyPath() string {
	return filepath.Join(filepath.Dir(b.path), ColabOneVaultKeyFilename)
}

func (b *ColabOneFileSecretBackend) checkRegularKeyPathLocked() (os.FileInfo, error) {
	kp := b.keyPath()
	info, err := os.Lstat(kp)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("colabone vault: refusing symlink vault key path %q", kp)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("colabone vault: vault key path %q is a directory", kp)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("colabone vault: vault key path %q is not a regular file", kp)
	}
	return info, nil
}

func (b *ColabOneFileSecretBackend) readExistingFileKEKLocked() ([]byte, bool, error) {
	linfo, err := b.checkRegularKeyPathLocked()
	if err != nil {
		return nil, false, err
	}
	kp := b.keyPath()
	f, err := os.Open(kp)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer f.Close()

	finfo, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if !finfo.Mode().IsRegular() {
		return nil, false, fmt.Errorf("colabone vault: vault key path %q is not a regular file", kp)
	}
	if linfo == nil {
		linfo, err = b.checkRegularKeyPathLocked()
		if err != nil {
			return nil, false, err
		}
		if linfo == nil {
			return nil, false, fmt.Errorf("colabone vault: vault key path %q changed while opening", kp)
		}
	}
	if !os.SameFile(linfo, finfo) {
		return nil, false, fmt.Errorf("colabone vault: vault key path %q changed while opening", kp)
	}

	raw, err := io.ReadAll(io.LimitReader(f, colabOneVaultKEKLen+1))
	if err != nil {
		return nil, false, err
	}
	if len(raw) != colabOneVaultKEKLen {
		clear(raw)
		return nil, true, fmt.Errorf("%w: invalid vault key length %d", ErrCorruptVault, len(raw))
	}
	_ = f.Chmod(0o600)
	return raw, true, nil
}

func (b *ColabOneFileSecretBackend) prepareV2SaveKeyLocked() (kek []byte, keyWrap string, wrappedKeyB64 string, err error) {
	existing, found, err := b.readExistingFileKEKLocked()
	if err != nil {
		return nil, "", "", err
	}
	if found {
		return existing, colabOneVaultKeyWrapFileKEK, "", nil
	}

	kek = make([]byte, colabOneVaultKEKLen)
	if _, err := rand.Read(kek); err != nil {
		clear(kek)
		return nil, "", "", err
	}

	kp := b.keyPath()
	dir := filepath.Dir(kp)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		clear(kek)
		return nil, "", "", err
	}

	tmp, err := os.CreateTemp(dir, ".vault-key-*.tmp")
	if err != nil {
		clear(kek)
		return nil, "", "", err
	}
	tmpName := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(0o600); err != nil {
		clear(kek)
		return nil, "", "", err
	}
	if _, err := tmp.Write(kek); err != nil {
		clear(kek)
		return nil, "", "", err
	}
	if err := tmp.Sync(); err != nil {
		clear(kek)
		return nil, "", "", err
	}
	if err := tmp.Close(); err != nil {
		closed = true
		clear(kek)
		return nil, "", "", err
	}
	closed = true

	// Atomically link into place without clobbering a concurrent creator.
	if linkErr := os.Link(tmpName, kp); linkErr != nil {
		if errors.Is(linkErr, os.ErrExist) {
			clear(kek)
			loaded, ok, readErr := b.readExistingFileKEKLocked()
			if readErr != nil {
				return nil, "", "", readErr
			}
			if !ok {
				return nil, "", "", fmt.Errorf("colabone vault: vault key disappeared during creation")
			}
			return loaded, colabOneVaultKeyWrapFileKEK, "", nil
		}
		// Fallback for filesystems that do not support hard links (O_CREATE|O_EXCL).
		f, openErr := os.OpenFile(kp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if openErr != nil {
			if errors.Is(openErr, os.ErrExist) {
				clear(kek)
				loaded, ok, readErr := b.readExistingFileKEKLocked()
				if readErr != nil {
					return nil, "", "", readErr
				}
				if !ok {
					return nil, "", "", fmt.Errorf("colabone vault: vault key disappeared during creation")
				}
				return loaded, colabOneVaultKeyWrapFileKEK, "", nil
			}
			clear(kek)
			return nil, "", "", openErr
		}
		if err := f.Chmod(0o600); err != nil {
			_ = f.Close()
			_ = os.Remove(kp)
			clear(kek)
			return nil, "", "", err
		}
		if _, err := f.Write(kek); err != nil {
			_ = f.Close()
			_ = os.Remove(kp)
			clear(kek)
			return nil, "", "", err
		}
		if err := f.Sync(); err != nil {
			_ = f.Close()
			_ = os.Remove(kp)
			clear(kek)
			return nil, "", "", err
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(kp)
			clear(kek)
			return nil, "", "", err
		}
	}

	return kek, colabOneVaultKeyWrapFileKEK, "", nil
}

func (b *ColabOneFileSecretBackend) unwrapV2KeyLocked(keyWrap, wrappedKeyB64 string) ([]byte, error) {
	if keyWrap != colabOneVaultKeyWrapFileKEK {
		return nil, fmt.Errorf("%w: unsupported key_wrap %q", ErrCorruptVault, keyWrap)
	}
	if strings.TrimSpace(wrappedKeyB64) != "" {
		return nil, fmt.Errorf("%w: unexpected wrapped_key for file_kek", ErrCorruptVault)
	}
	kek, found, err := b.readExistingFileKEKLocked()
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: missing vault key file", ErrCorruptVault)
	}
	return kek, nil
}

func (b *ColabOneFileSecretBackend) quarantineCorruptKeyLocked() error {
	kp := b.keyPath()
	info, err := os.Lstat(kp)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !info.Mode().IsRegular() || info.Size() == colabOneVaultKEKLen {
		return nil
	}
	backup := kp + ".corrupt." + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := os.Rename(kp, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func colabOneCreateSecureVaultTempFile(dir, prefix string) (*os.File, error) {
	var rnd [16]byte
	for attempt := 0; attempt < 32; attempt++ {
		if _, err := rand.Read(rnd[:]); err != nil {
			return nil, err
		}
		candidate := filepath.Join(dir, prefix+hex.EncodeToString(rnd[:])+".tmp")
		f, err := os.OpenFile(candidate, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return nil, err
		}
		if err := f.Chmod(0o600); err != nil {
			_ = f.Close()
			_ = os.Remove(candidate)
			return nil, err
		}
		return f, nil
	}
	return nil, errors.New("colabone vault: failed to allocate unique temporary vault file")
}

func colabOneSecureVaultFinalPath(_ string) error {
	return nil
}
