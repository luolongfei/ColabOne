//go:build windows

package vault

import (
	"errors"
	"runtime"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

const (
	winCredTypeGeneric = 1
	winErrorNotFound   = syscall.Errno(1168) // ERROR_NOT_FOUND
)

var (
	legacyAdvapi32      = syscall.NewLazyDLL("advapi32.dll")
	procLegacyCredReadW = legacyAdvapi32.NewProc("CredReadW")
	procLegacyCredDelW  = legacyAdvapi32.NewProc("CredDeleteW")
	procLegacyCredFree  = legacyAdvapi32.NewProc("CredFree")
)

// winCredential matches Windows wincred.h CREDENTIALW.
type winCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        [2]uint32 // FILETIME
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

// colabOneReadLegacySecret checks Windows Credential Manager for credentials
// written by the legacy Python keyring backend (WinVaultKeyring):
//  1. TargetName = service (e.g. "ColabOne") with UserName == key (newest value)
//  2. TargetName = key + "@" + service (e.g. "colab@ColabOne") fallback
//
// CredentialBlob is decoded from UTF-16LE (or UTF-8 fallback) into UTF-8 JSON bytes.
func colabOneReadLegacySecret(service, key string) ([]byte, bool, error) {
	if procLegacyCredReadW.Find() != nil || procLegacyCredFree.Find() != nil {
		return nil, false, nil
	}
	return colabOneResolveLegacyWinCredential(service, key, readWinGenericCredential)
}

// colabOneDeleteLegacySecret removes any legacy Python keyring credential for
// key so deleted credentials or signed-out sessions are never re-migrated on
// subsequent launches.
func colabOneDeleteLegacySecret(service, key string) error {
	if procLegacyCredDelW.Find() != nil {
		return nil
	}
	var reader func(string) ([]byte, string, bool, error)
	if procLegacyCredReadW.Find() == nil && procLegacyCredFree.Find() == nil {
		reader = readWinGenericCredential
	}
	return colabOneRemoveLegacyWinCredential(service, key, reader, deleteWinGenericCredential)
}

func readWinGenericCredential(target string) (blob []byte, userName string, ok bool, err error) {
	targetPtr, convErr := syscall.UTF16PtrFromString(target)
	if convErr != nil {
		return nil, "", false, convErr
	}
	var cred *winCredential
	r, _, callErr := procLegacyCredReadW.Call(
		uintptr(unsafe.Pointer(targetPtr)),
		winCredTypeGeneric,
		0,
		uintptr(unsafe.Pointer(&cred)),
	)
	runtime.KeepAlive(targetPtr)
	if r == 0 {
		if errors.Is(callErr, winErrorNotFound) {
			return nil, "", false, nil
		}
		return nil, "", false, callErr
	}
	if cred == nil {
		return nil, "", false, nil
	}
	defer procLegacyCredFree.Call(uintptr(unsafe.Pointer(cred)))

	userName = winUTF16PtrToString(cred.UserName)
	if cred.CredentialBlobSize > 0 && cred.CredentialBlob != nil {
		blob = append([]byte(nil), unsafe.Slice(cred.CredentialBlob, cred.CredentialBlobSize)...)
	}
	return blob, userName, true, nil
}

func deleteWinGenericCredential(target string) error {
	targetPtr, convErr := syscall.UTF16PtrFromString(target)
	if convErr != nil {
		return convErr
	}
	r, _, callErr := procLegacyCredDelW.Call(
		uintptr(unsafe.Pointer(targetPtr)),
		winCredTypeGeneric,
		0,
	)
	runtime.KeepAlive(targetPtr)
	if r == 0 && !errors.Is(callErr, winErrorNotFound) {
		return callErr
	}
	return nil
}

func winUTF16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	const maxLen = 4096
	var buf []uint16
	for i := 0; i < maxLen; i++ {
		u := *(*uint16)(unsafe.Add(unsafe.Pointer(p), uintptr(i)*2))
		if u == 0 {
			break
		}
		buf = append(buf, u)
	}
	return string(utf16.Decode(buf))
}
