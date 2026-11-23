//go:build !windows

package shortcuts

import "os/exec"

func winKnownDesktopPath() string {
	return ""
}

func winQueryDesktopViaShell() string {
	return ""
}

func applyWindowsProcAttr(_ *exec.Cmd) {}
