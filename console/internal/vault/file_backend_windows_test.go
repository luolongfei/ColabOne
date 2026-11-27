//go:build windows

package vault

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestColabOneFileVaultWindowsProtectedDACL(t *testing.T) {
	dir := t.TempDir()
	v := NewFileVault(dir)
	if err := v.Set("colab", map[string]any{"token": "ya29.win-dacl-test-token"}); err != nil {
		t.Fatalf("Set(colab) on Windows failed: %v", err)
	}

	vaultPath := filepath.Join(dir, ColabOneVaultFilename)
	sd, err := windows.GetNamedSecurityInfo(
		vaultPath,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo(%s) failed: %v", vaultPath, err)
	}
	sddl := sd.String()
	if !strings.HasPrefix(sddl, "D:P") {
		t.Fatalf("vault.enc SDDL = %q, want protected DACL starting with D:P", sddl)
	}

	tok, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		t.Fatalf("OpenCurrentProcessToken failed: %v", err)
	}
	defer tok.Close()
	user, err := tok.GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		t.Fatalf("GetTokenUser failed: %v", err)
	}
	sidStr, err := user.User.Sid.String()
	if err != nil || sidStr == "" {
		t.Fatalf("user SID string failed: %v", err)
	}
	// SDDL may abbreviate well-known SIDs (such as built-in Administrator -> "LA"),
	// so verify via raw DACL or accept either the explicit SID or well-known alias
	// when the single ACE grants FILE_ALL_ACCESS ("FA").
	if !strings.Contains(sddl, "(A;;FA;;;") {
		t.Fatalf("vault.enc SDDL = %q, expected explicit FILE_ALL_ACCESS ACE", sddl)
	}
	if strings.Count(sddl, "(A;") != 1 {
		t.Fatalf("vault.enc SDDL = %q, expected exactly 1 allow ACE for current user (sid=%s)", sddl, sidStr)
	}
}
