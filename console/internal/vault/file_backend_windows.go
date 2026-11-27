//go:build windows

package vault

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	winCryptProtectUIForbidden = 0x1
)

var (
	vaultCrypt32                = windows.NewLazySystemDLL("crypt32.dll")
	procVaultCryptProtectData   = vaultCrypt32.NewProc("CryptProtectData")
	procVaultCryptUnprotectData = vaultCrypt32.NewProc("CryptUnprotectData")
	vaultKernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procVaultLocalFree          = vaultKernel32.NewProc("LocalFree")
)

type winDataBlob struct {
	cbData uint32
	pbData *byte
}

func (b *ColabOneFileSecretBackend) dpapiEntropy() []byte {
	return []byte("colabone-vault:dpapi:v2:" + b.serviceName())
}

func (b *ColabOneFileSecretBackend) prepareV2SaveKeyLocked() (kek []byte, keyWrap string, wrappedKeyB64 string, err error) {
	kek = make([]byte, colabOneVaultKEKLen)
	if _, err := rand.Read(kek); err != nil {
		clear(kek)
		return nil, "", "", err
	}
	wrapped, err := colabOneDPAPIProtect(kek, b.dpapiEntropy(), "ColabOne Local Credential Vault")
	if err != nil {
		clear(kek)
		return nil, "", "", err
	}
	return kek, colabOneVaultKeyWrapDPAPI, base64.StdEncoding.EncodeToString(wrapped), nil
}

func (b *ColabOneFileSecretBackend) unwrapV2KeyLocked(keyWrap, wrappedKeyB64 string) ([]byte, error) {
	if keyWrap != colabOneVaultKeyWrapDPAPI {
		return nil, fmt.Errorf("%w: unsupported key_wrap %q", ErrCorruptVault, keyWrap)
	}
	wrapped, err := base64.StdEncoding.DecodeString(wrappedKeyB64)
	if err != nil || len(wrapped) == 0 {
		return nil, fmt.Errorf("%w: invalid wrapped_key", ErrCorruptVault)
	}
	if procVaultCryptUnprotectData.Find() != nil || procVaultLocalFree.Find() != nil {
		return nil, errors.New("colabone vault: Windows DPAPI is unavailable")
	}
	kek, err := colabOneDPAPIUnprotect(wrapped, b.dpapiEntropy())
	if err != nil {
		return nil, fmt.Errorf("%w: dpapi unwrap failed: %v", ErrCorruptVault, err)
	}
	if len(kek) != colabOneVaultKEKLen {
		clear(kek)
		return nil, fmt.Errorf("%w: invalid unwrapped KEK length %d", ErrCorruptVault, len(kek))
	}
	return kek, nil
}

func (b *ColabOneFileSecretBackend) quarantineCorruptKeyLocked() error {
	return nil
}

func colabOneDPAPIProtect(plain, entropy []byte, descr string) ([]byte, error) {
	if len(plain) == 0 {
		return nil, errors.New("colabone vault: empty DPAPI input")
	}
	if procVaultCryptProtectData.Find() != nil || procVaultLocalFree.Find() != nil {
		return nil, errors.New("colabone vault: Windows DPAPI is unavailable")
	}
	inBlob := winDataBlob{
		cbData: uint32(len(plain)),
		pbData: &plain[0],
	}
	var entropyBlob *winDataBlob
	if len(entropy) > 0 {
		entropyBlob = &winDataBlob{
			cbData: uint32(len(entropy)),
			pbData: &entropy[0],
		}
	}
	var descrPtr *uint16
	if descr != "" {
		if ptr, err := syscall.UTF16PtrFromString(descr); err == nil {
			descrPtr = ptr
		}
	}
	var outBlob winDataBlob
	r, _, callErr := procVaultCryptProtectData.Call(
		uintptr(unsafe.Pointer(&inBlob)),
		uintptr(unsafe.Pointer(descrPtr)),
		uintptr(unsafe.Pointer(entropyBlob)),
		0,
		0,
		winCryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&outBlob)),
	)
	runtime.KeepAlive(plain)
	runtime.KeepAlive(entropy)
	runtime.KeepAlive(descrPtr)
	runtime.KeepAlive(&inBlob)
	runtime.KeepAlive(entropyBlob)
	if r == 0 {
		return nil, fmt.Errorf("colabone vault: CryptProtectData failed: %w", callErr)
	}
	if outBlob.pbData != nil {
		defer procVaultLocalFree.Call(uintptr(unsafe.Pointer(outBlob.pbData)))
	}
	if outBlob.cbData == 0 || outBlob.pbData == nil {
		return nil, errors.New("colabone vault: CryptProtectData returned empty output")
	}
	out := make([]byte, outBlob.cbData)
	copy(out, unsafe.Slice(outBlob.pbData, outBlob.cbData))
	return out, nil
}

func colabOneDPAPIUnprotect(wrapped, entropy []byte) ([]byte, error) {
	if len(wrapped) == 0 {
		return nil, errors.New("colabone vault: empty DPAPI ciphertext")
	}
	inBlob := winDataBlob{
		cbData: uint32(len(wrapped)),
		pbData: &wrapped[0],
	}
	var entropyBlob *winDataBlob
	if len(entropy) > 0 {
		entropyBlob = &winDataBlob{
			cbData: uint32(len(entropy)),
			pbData: &entropy[0],
		}
	}
	var outBlob winDataBlob
	r, _, callErr := procVaultCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&inBlob)),
		0,
		uintptr(unsafe.Pointer(entropyBlob)),
		0,
		0,
		winCryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&outBlob)),
	)
	runtime.KeepAlive(wrapped)
	runtime.KeepAlive(entropy)
	runtime.KeepAlive(&inBlob)
	runtime.KeepAlive(entropyBlob)
	if r == 0 {
		return nil, fmt.Errorf("CryptUnprotectData failed: %w", callErr)
	}
	if outBlob.pbData != nil {
		defer func() {
			clear(unsafe.Slice(outBlob.pbData, outBlob.cbData))
			_, _, _ = procVaultLocalFree.Call(uintptr(unsafe.Pointer(outBlob.pbData)))
		}()
	}
	if outBlob.cbData == 0 || outBlob.pbData == nil {
		return nil, errors.New("CryptUnprotectData returned empty output")
	}
	out := make([]byte, outBlob.cbData)
	copy(out, unsafe.Slice(outBlob.pbData, outBlob.cbData))
	return out, nil
}

// colabOneCreateSecureVaultTempFile atomically creates a new vault temporary file
// with an explicit protected NTFS DACL granting FILE_ALL_ACCESS exclusively to the
// current Windows user from birth (zero inherited-ACL window, no post-hoc
// ReOpenFile/SetSecurityInfo handle-reopening). Non-ACL filesystems (FAT32/exFAT)
// natively ignore the SecurityAttributes descriptor on CreateFileW.
func colabOneCreateSecureVaultTempFile(dir, prefix string) (*os.File, error) {
	sddl, err := colabOneCurrentUserSDDL()
	if err != nil {
		return nil, err
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, fmt.Errorf("colabone vault: SecurityDescriptorFromString failed: %w", err)
	}
	sa := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
		InheritHandle:      0,
	}

	var rnd [16]byte
	for attempt := 0; attempt < 32; attempt++ {
		if _, err := rand.Read(rnd[:]); err != nil {
			return nil, err
		}
		candidate := filepath.Join(dir, prefix+hex.EncodeToString(rnd[:])+".tmp")
		pathPtr, err := windows.UTF16PtrFromString(candidate)
		if err != nil {
			return nil, err
		}
		h, createErr := windows.CreateFile(
			pathPtr,
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			&sa,
			windows.CREATE_NEW,
			windows.FILE_ATTRIBUTE_NORMAL,
			0,
		)
		runtime.KeepAlive(sd)
		runtime.KeepAlive(pathPtr)
		if createErr != nil {
			if errors.Is(createErr, windows.ERROR_FILE_EXISTS) || errors.Is(createErr, os.ErrExist) {
				continue
			}
			return nil, fmt.Errorf("colabone vault: CreateFile failed: %w", createErr)
		}
		return os.NewFile(uintptr(h), candidate), nil
	}
	return nil, errors.New("colabone vault: failed to allocate unique temporary vault file")
}

func colabOneSecureVaultFinalPath(_ string) error {
	// Protected current-user DACL is attached atomically at CreateFileW creation
	// time prior to atomic rename.
	return nil
}

func colabOneCurrentUserSDDL() (string, error) {
	tok, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return "", fmt.Errorf("colabone vault: OpenCurrentProcessToken failed: %w", err)
	}
	defer tok.Close()
	user, err := tok.GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return "", fmt.Errorf("colabone vault: GetTokenUser failed: %w", err)
	}
	sidStr, err := user.User.Sid.String()
	if err != nil || sidStr == "" {
		return "", fmt.Errorf("colabone vault: user SID conversion failed: %w", err)
	}
	return "D:P(A;;FA;;;" + sidStr + ")", nil
}
