//go:build windows

package vault

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func assertProtectedCurrentUserDACL(t *testing.T, vaultPath string) {
	t.Helper()
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
	if !strings.Contains(sddl, "(A;;FA;;;") {
		t.Fatalf("vault.enc SDDL = %q, expected explicit FILE_ALL_ACCESS ACE", sddl)
	}
	if strings.Count(sddl, "(A;") != 1 {
		t.Fatalf("vault.enc SDDL = %q, expected exactly 1 allow ACE for current user (sid=%s)", sddl, sidStr)
	}
}

func TestColabOneFileVaultWindowsProtectedDACL(t *testing.T) {
	dir := t.TempDir()
	v := NewFileVault(dir)
	if err := v.Set("colab", map[string]any{"token": "ya29.win-dacl-test-token-v1"}); err != nil {
		t.Fatalf("initial Set(colab) on Windows failed: %v", err)
	}

	vaultPath := filepath.Join(dir, ColabOneVaultFilename)
	assertProtectedCurrentUserDACL(t, vaultPath)

	// Atomic overwrite must preserve the protected current-user-only DACL and cold-reload cleanly.
	if err := v.Set("colab", map[string]any{"token": "ya29.win-dacl-test-token-v2"}); err != nil {
		t.Fatalf("overwrite Set(colab) on Windows failed: %v", err)
	}
	assertProtectedCurrentUserDACL(t, vaultPath)

	cold := NewFileVault(dir)
	var got map[string]string
	ok, err := cold.GetJSON("colab", &got)
	if err != nil || !ok || got["token"] != "ya29.win-dacl-test-token-v2" {
		t.Fatalf("cold GetJSON(colab) = (%v, %+v, %v), want v2 token", ok, got, err)
	}

	// Storage failures must be tagged with IsVaultStorageError() == true.
	dirAsFile := filepath.Join(t.TempDir(), "vault.enc")
	if err := os.MkdirAll(dirAsFile, 0o700); err != nil {
		t.Fatal(err)
	}
	badBackend := NewColabOneFileSecretBackend(dirAsFile, ColabOneVaultService)
	badErr := badBackend.Set("colab", []byte(`"x"`))
	var vse interface{ IsVaultStorageError() bool }
	if badErr == nil || !errors.As(badErr, &vse) || !vse.IsVaultStorageError() {
		t.Fatalf("expected IsVaultStorageError()==true for directory vault target, got %v", badErr)
	}
}
