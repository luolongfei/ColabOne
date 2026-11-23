package shortcuts

import (
	"bytes"
	"encoding/base64"
	"strings"
	"unicode/utf8"
)

// winDesktopShellScript queries the Windows Desktop path as UTF-8 encoded in
// pure ASCII Base64 so PowerShell never touches [Console]::OutputEncoding
// (which fails with ERROR_INVALID_HANDLE in GUI-subsystem processes spawned
// with CREATE_NO_WINDOW) and stdout cannot be corrupted by OEM codepages.
const winDesktopShellScript = "[Convert]::ToBase64String([System.Text.Encoding]::UTF8.GetBytes([Environment]::GetFolderPath('Desktop')))"

// decodeBase64UTF8Path decodes the ASCII Base64 UTF-8 path emitted by
// winDesktopShellScript. It tolerates leading/trailing whitespace, UTF-8 BOM,
// and embedded CR/LF line wraps, returning "" on empty or malformed input.
func decodeBase64UTF8Path(raw []byte) string {
	encoded := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(raw)), "\uFEFF"))
	if encoded == "" {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(decoded) == 0 {
		return ""
	}
	if !utf8.Valid(decoded) || bytes.IndexByte(decoded, 0) >= 0 {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(decoded)), "\uFEFF"))
}
