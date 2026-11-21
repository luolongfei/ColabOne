//go:build !windows

package main

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// colabOneDetectOSLocale returns the active OS locale tag from POSIX locale
// environment variables, with a macOS AppleLanguages fallback for Dock/Finder
// launches where LANG is often unset.
func colabOneDetectOSLocale() string {
	for _, envKey := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if raw := strings.TrimSpace(os.Getenv(envKey)); raw != "" && !strings.EqualFold(raw, "C") && !strings.EqualFold(raw, "POSIX") {
			return raw
		}
	}
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("defaults", "read", "-g", "AppleLanguages").Output(); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				trimmed := strings.Trim(strings.TrimSpace(line), `",() `)
				if trimmed != "" {
					return trimmed
				}
			}
		}
	}
	return ""
}
