//go:build windows

package shortcuts

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	winCreateNoWindow  = 0x08000000
	winDesktopQuerySec = 8 * time.Second
)

type winGUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

// FOLDERID_Desktop = {B4BFCC3A-DB2C-424C-B029-7FE99A87C641}
var folderIDDesktop = winGUID{
	Data1: 0xB4BFCC3A,
	Data2: 0xDB2C,
	Data3: 0x424C,
	Data4: [8]byte{0xB0, 0x29, 0x7F, 0xE9, 0x9A, 0x87, 0xC6, 0x41},
}

var (
	modShell32               = syscall.NewLazyDLL("shell32.dll")
	modOle32                 = syscall.NewLazyDLL("ole32.dll")
	procSHGetKnownFolderPath = modShell32.NewProc("SHGetKnownFolderPath")
	procCoTaskMemFree        = modOle32.NewProc("CoTaskMemFree")
)

func winKnownDesktopPath() string {
	if procSHGetKnownFolderPath.Find() != nil || procCoTaskMemFree.Find() != nil {
		return ""
	}
	rfid := folderIDDesktop
	var ptr *uint16
	hr, _, _ := procSHGetKnownFolderPath.Call(
		uintptr(unsafe.Pointer(&rfid)),
		0,
		0,
		uintptr(unsafe.Pointer(&ptr)),
	)
	if ptr != nil {
		defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(ptr)))
	}
	if uint32(hr) != 0 || ptr == nil {
		return ""
	}
	return utf16PtrToString(ptr)
}

func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	const maxUTF16Len = 32768
	var units []uint16
	for i := 0; i < maxUTF16Len; i++ {
		v := *(*uint16)(unsafe.Add(unsafe.Pointer(p), uintptr(i)*unsafe.Sizeof(uint16(0))))
		if v == 0 {
			break
		}
		units = append(units, v)
	}
	if len(units) == 0 {
		return ""
	}
	return syscall.UTF16ToString(units)
}

func winPowerShellExecutable() string {
	root := strings.TrimSpace(os.Getenv("SystemRoot"))
	if root == "" {
		root = strings.TrimSpace(os.Getenv("WINDIR"))
	}
	if root != "" {
		return filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	}
	const fallback = `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
	if isFile(fallback) {
		return fallback
	}
	return "powershell.exe"
}

func winQueryDesktopViaShell() string {
	ctx, cancel := context.WithTimeout(context.Background(), winDesktopQuerySec)
	defer cancel()
	cmd := exec.CommandContext(
		ctx,
		winPowerShellExecutable(),
		"-NoProfile",
		"-NonInteractive",
		"-Command",
		winDesktopShellScript,
	)
	cmd.Stdin = nil
	applyWindowsProcAttr(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return decodeBase64UTF8Path(out)
}

func applyWindowsProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: winCreateNoWindow,
	}
}
