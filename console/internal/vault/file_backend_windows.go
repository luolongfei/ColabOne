//go:build windows

package vault

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

const (
	winCryptProtectUIForbidden          = 0x1
	winSDDLRevision1                    = 1
	winSEFileObject                     = 1
	winDACLSecurityInformation          = 0x00000004
	winProtectedDACLSecurityInformation = 0x80000000
	winErrorInvalidFunction             = syscall.Errno(1)
	winErrorAccessDenied                = syscall.Errno(5)
	winErrorSharingViolation            = syscall.Errno(32)
	winErrorLockViolation               = syscall.Errno(33)
	winErrorNotSupported                = syscall.Errno(50)
	winDACLApplyMaxAttempts             = 6
)

var (
	vaultCrypt32                                       = syscall.NewLazyDLL("crypt32.dll")
	procVaultCryptProtectData                          = vaultCrypt32.NewProc("CryptProtectData")
	procVaultCryptUnprotectData                        = vaultCrypt32.NewProc("CryptUnprotectData")
	vaultKernel32                                      = syscall.NewLazyDLL("kernel32.dll")
	procVaultLocalFree                                 = vaultKernel32.NewProc("LocalFree")
	vaultAdvapi32                                      = syscall.NewLazyDLL("advapi32.dll")
	procVaultConvertStringSecurityDescriptorToSDW      = vaultAdvapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
	procVaultGetSecurityDescriptorDacl                 = vaultAdvapi32.NewProc("GetSecurityDescriptorDacl")
	procVaultSetNamedSecurityInfoW                     = vaultAdvapi32.NewProc("SetNamedSecurityInfoW")
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

func colabOneSecureVaultTempFile(_ *os.File, tmpPath string) error {
	return colabOneApplyCurrentUserDACL(tmpPath)
}

func colabOneSecureVaultFinalPath(path string) error {
	return colabOneApplyCurrentUserDACL(path)
}

func colabOneCurrentUserSDDL() string {
	tok, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return "D:P(A;;FA;;;OW)"
	}
	defer tok.Close()
	user, err := tok.GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return "D:P(A;;FA;;;OW)"
	}
	sidStr, err := user.User.Sid.String()
	if err != nil || sidStr == "" {
		return "D:P(A;;FA;;;OW)"
	}
	return "D:P(A;;FA;;;" + sidStr + ")"
}

// colabOneApplyCurrentUserDACL applies an explicit protected NTFS DACL granting
// FILE_ALL_ACCESS exclusively to the current Windows user (disabling inherited
// ACEs). Non-ACL filesystems (FAT32/exFAT) returning ERROR_INVALID_FUNCTION or
// ERROR_NOT_SUPPORTED are tolerated cleanly.
func colabOneApplyCurrentUserDACL(path string) error {
	if procVaultConvertStringSecurityDescriptorToSDW.Find() != nil ||
		procVaultGetSecurityDescriptorDacl.Find() != nil ||
		procVaultSetNamedSecurityInfoW.Find() != nil ||
		procVaultLocalFree.Find() != nil {
		return nil
	}
	sddlPtr, err := syscall.UTF16PtrFromString(colabOneCurrentUserSDDL())
	if err != nil {
		return err
	}
	var sd uintptr
	r, _, callErr := procVaultConvertStringSecurityDescriptorToSDW.Call(
		uintptr(unsafe.Pointer(sddlPtr)),
		winSDDLRevision1,
		uintptr(unsafe.Pointer(&sd)),
		0,
	)
	runtime.KeepAlive(sddlPtr)
	if r == 0 || sd == 0 {
		return fmt.Errorf("colabone vault: ConvertStringSecurityDescriptorToSecurityDescriptorW failed: %w", callErr)
	}
	defer procVaultLocalFree.Call(sd)

	var daclPresent int32
	var dacl uintptr
	var daclDefaulted int32
	r, _, callErr = procVaultGetSecurityDescriptorDacl.Call(
		sd,
		uintptr(unsafe.Pointer(&daclPresent)),
		uintptr(unsafe.Pointer(&dacl)),
		uintptr(unsafe.Pointer(&daclDefaulted)),
	)
	if r == 0 || daclPresent == 0 || dacl == 0 {
		return fmt.Errorf("colabone vault: GetSecurityDescriptorDacl failed: %w", callErr)
	}

	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	secInfo := uintptr(winDACLSecurityInformation | winProtectedDACLSecurityInformation)
	var status uintptr
	for attempt := 0; attempt < winDACLApplyMaxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt*15) * time.Millisecond)
		}
		status, _, _ = procVaultSetNamedSecurityInfoW.Call(
			uintptr(unsafe.Pointer(pathPtr)),
			winSEFileObject,
			secInfo,
			0,
			0,
			dacl,
			0,
		)
		if status == 0 {
			break
		}
		errno := syscall.Errno(status)
		if errors.Is(errno, winErrorInvalidFunction) || errors.Is(errno, winErrorNotSupported) {
			status = 0
			break
		}
		if !colabOneIsTransientWinDACLErr(errno) {
			break
		}
	}
	runtime.KeepAlive(pathPtr)
	if status != 0 {
		return fmt.Errorf("colabone vault: SetNamedSecurityInfoW failed: %w", syscall.Errno(status))
	}
	return nil
}

func colabOneIsTransientWinDACLErr(errno syscall.Errno) bool {
	return errors.Is(errno, winErrorAccessDenied) ||
		errors.Is(errno, winErrorSharingViolation) ||
		errors.Is(errno, winErrorLockViolation)
}
