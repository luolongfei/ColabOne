//go:build windows

package main

import (
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	i18nKernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procGetUserDefaultLocale = i18nKernel32.NewProc("GetUserDefaultLocaleName")
)

// colabOneDetectOSLocale returns the raw Windows user default locale tag
// (e.g. "zh-CN", "en-US", "ja-JP") via kernel32.GetUserDefaultLocaleName.
func colabOneDetectOSLocale() string {
	for _, envKey := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		raw := strings.TrimSpace(os.Getenv(envKey))
		upper := strings.ToUpper(raw)
		if raw != "" && upper != "C" && upper != "POSIX" && !strings.HasPrefix(upper, "C.") && !strings.HasPrefix(upper, "POSIX.") {
			return raw
		}
	}
	if procGetUserDefaultLocale.Find() != nil {
		return ""
	}
	buf := make([]uint16, 85) // LOCALE_NAME_MAX_LENGTH
	n, _, _ := procGetUserDefaultLocale.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	if n <= 1 || int(n) > len(buf) {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}
