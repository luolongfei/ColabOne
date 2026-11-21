//go:build !windows

package main

import (
	"os/exec"
	"runtime"
	"strings"
)

// colabOneDetectSystemAppearance reads the true OS appearance ("dark" or
// "light") independent of any per-app theme override. Returns "" when the
// platform cannot be queried directly so the frontend falls back to matchMedia.
func colabOneDetectSystemAppearance() string {
	if runtime.GOOS == "darwin" {
		out, err := exec.Command("defaults", "read", "-g", "AppleInterfaceStyle").Output()
		if err != nil {
			// macOS omits AppleInterfaceStyle when in Light mode.
			return "light"
		}
		if strings.EqualFold(strings.TrimSpace(string(out)), "Dark") {
			return "dark"
		}
		return "light"
	}
	return ""
}
