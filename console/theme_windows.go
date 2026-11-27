//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	themeHKEYCurrentUser = 0x80000001
	themeKeyQueryValue   = 0x0001
	themePersonalizePath = `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`
	themeAppsUseLightKey = "AppsUseLightTheme"
)

var (
	themeAdvapi32       = windows.NewLazySystemDLL("advapi32.dll")
	procRegOpenKeyExW   = themeAdvapi32.NewProc("RegOpenKeyExW")
	procRegQueryValueEx = themeAdvapi32.NewProc("RegQueryValueExW")
	procRegCloseKey     = themeAdvapi32.NewProc("RegCloseKey")
)

// colabOneDetectSystemAppearance reads the true Windows user app theme
// ("dark" or "light") directly from the registry, independent of any
// per-app WebView2 PreferredColorScheme override.
func colabOneDetectSystemAppearance() string {
	if procRegOpenKeyExW.Find() != nil || procRegQueryValueEx.Find() != nil || procRegCloseKey.Find() != nil {
		return ""
	}
	subKey, err := windows.UTF16PtrFromString(themePersonalizePath)
	if err != nil {
		return ""
	}
	valueName, err := windows.UTF16PtrFromString(themeAppsUseLightKey)
	if err != nil {
		return ""
	}
	var key uintptr
	if r, _, _ := procRegOpenKeyExW.Call(
		themeHKEYCurrentUser,
		uintptr(unsafe.Pointer(subKey)),
		0,
		themeKeyQueryValue,
		uintptr(unsafe.Pointer(&key)),
	); r != 0 {
		return ""
	}
	defer procRegCloseKey.Call(key)
	var typ, val uint32
	size := uint32(4)
	if r, _, _ := procRegQueryValueEx.Call(
		key,
		uintptr(unsafe.Pointer(valueName)),
		0,
		uintptr(unsafe.Pointer(&typ)),
		uintptr(unsafe.Pointer(&val)),
		uintptr(unsafe.Pointer(&size)),
	); r != 0 || typ != 4 || size != 4 {
		return ""
	}
	if val == 0 {
		return "dark"
	}
	return "light"
}
