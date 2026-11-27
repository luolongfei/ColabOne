package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/luolongfei/ColabOne/console/internal/activity"
	"github.com/luolongfei/ColabOne/console/internal/fsutil"
)

const (
	maxWorkflowOutputSize        = 512 << 20
	maxOutputChunkBytes          = 512 << 10
	maxDownloadNameBytes         = 180
	windowsDownloadPathLimit     = 240
	windowsDownloadPathSepUnits  = 1
	windowsDownloadSuffixReserve = 12
	minWindowsDownloadNameUnits  = 16
	reservedDownloadPrefix       = "colabone-"
)

var (
	workflowOutputIDPattern     = regexp.MustCompile(`^[a-f0-9]{32}$`)
	workflowOutputSHA256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
	allowedOutputExtensions     = map[string]bool{
		"png":  true,
		"jpg":  true,
		"jpeg": true,
		"webp": true,
		"gif":  true,
		"mp4":  true,
		"webm": true,
		"wav":  true,
		"mp3":  true,
		"flac": true,
	}
	blockedComfyDownloadExtensions = map[string]bool{
		".action":             true,
		".app":                true,
		".application":        true,
		".appref-ms":          true,
		".appx":               true,
		".appxbundle":         true,
		".bat":                true,
		".chm":                true,
		".cmd":                true,
		".com":                true,
		".command":            true,
		".cpl":                true,
		".desktop":            true,
		".diagcab":            true,
		".dll":                true,
		".dmg":                true,
		".exe":                true,
		".hta":                true,
		".img":                true,
		".inetloc":            true,
		".iso":                true,
		".jar":                true,
		".js":                 true,
		".jse":                true,
		".library-ms":         true,
		".lnk":                true,
		".mpkg":               true,
		".msc":                true,
		".msi":                true,
		".msix":               true,
		".msixbundle":         true,
		".msp":                true,
		".pif":                true,
		".pkg":                true,
		".ps1":                true,
		".reg":                true,
		".scf":                true,
		".scpt":               true,
		".scr":                true,
		".search-ms":          true,
		".searchconnector-ms": true,
		".sh":                 true,
		".terminal":           true,
		".tool":               true,
		".url":                true,
		".vbe":                true,
		".vbs":                true,
		".vhd":                true,
		".vhdx":               true,
		".workflow":           true,
		".wsf":                true,
		".wsh":                true,
	}
	windowsReservedDownloadNames = map[string]bool{
		"CON": true, "PRN": true, "AUX": true, "NUL": true,
		"COM1": true, "COM2": true, "COM3": true, "COM4": true,
		"COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
		"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
		"LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
	}
)

type colabOneMediaChunk struct {
	Data      string `json:"data"`
	Size      int64  `json:"size"`
	Offset    int64  `json:"offset"`
	SHA256    string `json:"sha256"`
	Extension string `json:"extension"`
	Filename  string `json:"filename"`
	Source    string `json:"source"`
}

// ExportWorkflowOutput accepts only a remote artifact ID. The save destination
// comes from the native dialog, never a path supplied by remote workflow output.
func (d *ColabOneDesktop) ExportWorkflowOutput(ctx context.Context, id string) (string, error) {
	if !workflowOutputIDPattern.MatchString(id) {
		return "", errors.New(colabOneT("err.invalidOutputID"))
	}
	if d.bridge == nil {
		return "", errors.New(colabOneT("err.bridgeNotInitialized"))
	}
	value, err := d.bridge.call(ctx, "comfyui.output.asset", map[string]any{"id": id, "offset": 0})
	if err != nil {
		return "", err
	}
	var chunk colabOneMediaChunk
	if err = json.Unmarshal([]byte(value), &chunk); err != nil {
		return "", err
	}
	ext := strings.ToLower(chunk.Extension)
	digest := strings.ToLower(strings.TrimSpace(chunk.SHA256))
	allowEmptyDigest := chunk.Source == "drive" && digest == ""
	if chunk.Size <= 0 || chunk.Size > maxWorkflowOutputSize || !allowedOutputExtensions[ext] || (!allowEmptyDigest && !workflowOutputSHA256Pattern.MatchString(digest)) {
		return "", errors.New(colabOneT("err.invalidOutputMetadata"))
	}
	size := chunk.Size
	defaultName := "ColabOne-" + id[:8] + "." + ext
	defaultPath := defaultName
	if dir, err := mygo.App.Path(mygo.PathDownloads); err == nil && dir != "" {
		defaultPath = filepath.Join(dir, defaultName)
	} else if home, err := mygo.App.Path(mygo.PathHome); err == nil && home != "" {
		defaultPath = filepath.Join(home, defaultName)
	}
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Parent:      mygo.CallerWindow(ctx),
		DefaultPath: defaultPath,
		Filters: []mygo.FileFilter{
			{Name: strings.ToUpper(ext), Extensions: []string{ext}},
		},
	})
	if err != nil || path == "" {
		return "", err
	}
	out, err := os.CreateTemp(filepath.Dir(path), ".colabone-export-*")
	if err != nil {
		return "", err
	}
	tmpPath := out.Name()
	keepTmp := false
	defer func() {
		_ = out.Close()
		if !keepTmp {
			_ = os.Remove(tmpPath)
		}
	}()
	hash := sha256.New()
	offset := int64(0)
	for {
		chunkDigest := strings.ToLower(strings.TrimSpace(chunk.SHA256))
		isDriveChunk := chunk.Source == "drive"
		digestMismatch := false
		if digest == "" {
			if chunkDigest != "" {
				if isDriveChunk && workflowOutputSHA256Pattern.MatchString(chunkDigest) {
					digest = chunkDigest
				} else {
					digestMismatch = true
				}
			}
		} else if chunkDigest != digest {
			if !(isDriveChunk && chunkDigest == "") {
				digestMismatch = true
			}
		}
		if chunk.Offset != offset || chunk.Size != size || digestMismatch {
			return "", errors.New(colabOneT("err.outputTransferChanged"))
		}
		data, err := base64.StdEncoding.DecodeString(chunk.Data)
		if err != nil || len(data) == 0 || len(data) > maxOutputChunkBytes || offset+int64(len(data)) > size {
			return "", errors.New(colabOneT("err.outputTransferPartial"))
		}
		if _, err = out.Write(data); err != nil {
			return "", err
		}
		hash.Write(data)
		offset += int64(len(data))
		if offset == size {
			break
		}
		value, err = d.bridge.call(ctx, "comfyui.output.asset", map[string]any{"id": id, "offset": offset})
		if err != nil {
			return "", err
		}
		var nextChunk colabOneMediaChunk
		if err = json.Unmarshal([]byte(value), &nextChunk); err != nil {
			return "", err
		}
		chunk = nextChunk
	}
	if digest != "" && hex.EncodeToString(hash.Sum(nil)) != digest {
		return "", errors.New(colabOneT("err.outputHashMismatch"))
	}
	if err = out.Sync(); err != nil {
		return "", err
	}
	if err = out.Close(); err != nil {
		return "", err
	}
	if renameErr := fsutil.RenameReplace(tmpPath, path); renameErr != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("%s%w", colabOneT("err.outputSaveFailedPrefix"), renameErr)
	}
	keepTmp = true
	return path, nil
}

func validateColabOneCanvasURL(raw string) (*url.URL, error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return nil, errors.New(colabOneT("err.canvasURLEmpty"))
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New(colabOneT("err.canvasURLInvalid"))
	}
	if u.Scheme != "https" || u.Opaque != "" {
		return nil, errors.New(colabOneT("err.canvasURLHTTPS"))
	}
	if u.User != nil {
		return nil, errors.New(colabOneT("err.canvasURLUserInfo"))
	}
	if strings.Contains(u.Host, "%") {
		return nil, errors.New(colabOneT("err.canvasURLHost"))
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" {
		return nil, errors.New(colabOneT("err.canvasURLHost"))
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return nil, errors.New(colabOneT("err.canvasURLLoopback"))
	}
	if host == "trycloudflare.com" || strings.HasSuffix(host, ".trycloudflare.com") {
		return nil, errors.New(colabOneT("err.canvasURLTemporary"))
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		unmapped := addr.Unmap()
		if unmapped.IsLoopback() || unmapped.IsUnspecified() || addr.Zone() != "" {
			return nil, errors.New(colabOneT("err.canvasURLLoopback"))
		}
	}
	if u.Path != "/_colabone_auth" {
		return nil, errors.New(colabOneT("err.canvasURLPath"))
	}
	if strings.TrimSpace(u.Query().Get("token")) == "" {
		return nil, errors.New(colabOneT("err.canvasURLToken"))
	}
	return u, nil
}

func colabOneHTTPSOrigin(u *url.URL) string {
	if u == nil || !strings.EqualFold(u.Scheme, "https") || u.Opaque != "" || u.User != nil {
		return ""
	}
	if strings.Contains(u.Host, "%") {
		return ""
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" {
		return ""
	}
	port := u.Port()
	if port == "443" {
		port = ""
	}
	if port != "" {
		return "https://" + net.JoinHostPort(host, port)
	}
	if strings.Contains(host, ":") {
		return "https://[" + host + "]"
	}
	return "https://" + host
}

func parseColabOneAllowedOrigin(allowedOrigin string) string {
	if allowedOrigin == "" || strings.TrimSpace(allowedOrigin) != allowedOrigin {
		return ""
	}
	u, err := url.Parse(allowedOrigin)
	if err != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	return colabOneHTTPSOrigin(u)
}

// allowColabOneComfyNavigate enforces same-origin navigation for the untrusted
// native ComfyUI canvas window. It permits only HTTPS URLs matching the active
// validated canvas origin and blob: URLs whose embedded origin matches it.
func allowColabOneComfyNavigate(rawURL, allowedOrigin string) bool {
	wantOrigin := parseColabOneAllowedOrigin(allowedOrigin)
	if wantOrigin == "" || rawURL == "" || strings.TrimSpace(rawURL) != rawURL {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return colabOneHTTPSOrigin(u) == wantOrigin
	case "blob":
		if u.Opaque == "" || u.Host != "" || u.User != nil {
			return false
		}
		inner, err := url.Parse(u.Opaque)
		if err != nil {
			return false
		}
		return colabOneHTTPSOrigin(inner) == wantOrigin
	default:
		return false
	}
}

func trimColabOneDownloadName(s string) string {
	s = strings.TrimLeftFunc(s, func(r rune) bool {
		return r == '.' || unicode.IsSpace(r)
	})
	s = strings.TrimRightFunc(s, func(r rune) bool {
		return r == '.' || unicode.IsSpace(r)
	})
	return s
}

func truncateColabOneUTF8Bytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "\ufffd")
	}
	if len(s) <= maxBytes {
		return s
	}
	end := 0
	for i, r := range s {
		next := i + utf8.RuneLen(r)
		if next > maxBytes {
			break
		}
		end = next
	}
	return s[:end]
}

func truncateColabOneDownloadName(name string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if !utf8.ValidString(name) {
		name = strings.ToValidUTF8(name, "\ufffd")
	}
	if len(name) <= maxBytes {
		return trimColabOneDownloadName(name)
	}
	ext := filepath.Ext(name)
	if ext != "" && len(ext) < maxBytes {
		base := strings.TrimSuffix(name, ext)
		limit := maxBytes - len(ext)
		truncBase := trimColabOneDownloadName(truncateColabOneUTF8Bytes(base, limit))
		if truncBase != "" {
			return trimColabOneDownloadName(truncBase + ext)
		}
	}
	return trimColabOneDownloadName(truncateColabOneUTF8Bytes(name, maxBytes))
}

func sanitizeColabOneDownloadName(suggested string) string {
	base := path.Base(path.Clean("/" + strings.ReplaceAll(suggested, `\`, "/")))
	if base == "" || base == "/" || base == "\\" || base == "." || base == ".." {
		return "download"
	}
	var b strings.Builder
	b.Grow(len(base))
	for _, r := range base {
		if unicode.IsControl(r) {
			continue
		}
		switch r {
		case '<', '>', ':', '"', '/', '\\', '|', '?', '*':
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	name := trimColabOneDownloadName(b.String())
	if name == "" || name == "." || name == ".." {
		return "download"
	}
	name = truncateColabOneDownloadName(name, maxDownloadNameBytes)
	if name == "" || name == "." || name == ".." {
		return "download"
	}
	if isWindowsReservedDownloadName(name) {
		budget := maxDownloadNameBytes - len(reservedDownloadPrefix)
		truncated := truncateColabOneDownloadName(name, budget)
		if truncated == "" || truncated == "." || truncated == ".." {
			return "download"
		}
		name = reservedDownloadPrefix + truncated
	}
	return name
}

func isBlockedComfyDownloadExt(name string) bool {
	return blockedComfyDownloadExtensions[strings.ToLower(filepath.Ext(name))]
}

func isWindowsReservedDownloadName(name string) bool {
	stem := name
	if dot := strings.IndexByte(stem, '.'); dot >= 0 {
		stem = stem[:dot]
	}
	return windowsReservedDownloadNames[strings.ToUpper(strings.TrimRightFunc(stem, unicode.IsSpace))]
}

func colabOneUTF16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}

func colabOneRuneUTF16Len(r rune) int {
	if r >= 0x10000 && r <= unicode.MaxRune {
		return 2
	}
	return 1
}

func truncateColabOneUTF16Units(s string, maxUnits int) string {
	if maxUnits <= 0 {
		return ""
	}
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "\ufffd")
	}
	units := 0
	end := 0
	for i, r := range s {
		rUnits := colabOneRuneUTF16Len(r)
		if units+rUnits > maxUnits {
			break
		}
		units += rUnits
		end = i + utf8.RuneLen(r)
	}
	return s[:end]
}

func truncateColabOneDownloadNameUTF16(name string, maxUnits int) string {
	if maxUnits <= 0 {
		return ""
	}
	if !utf8.ValidString(name) {
		name = strings.ToValidUTF8(name, "\ufffd")
	}
	name = trimColabOneDownloadName(name)
	if colabOneUTF16Len(name) <= maxUnits {
		return name
	}
	ext := filepath.Ext(name)
	extUnits := colabOneUTF16Len(ext)
	if ext != "" && extUnits < maxUnits {
		base := strings.TrimSuffix(name, ext)
		limit := maxUnits - extUnits
		truncBase := trimColabOneDownloadName(truncateColabOneUTF16Units(base, limit))
		if truncBase != "" {
			return trimColabOneDownloadName(truncBase + ext)
		}
	}
	return trimColabOneDownloadName(truncateColabOneUTF16Units(name, maxUnits))
}

func clampColabOneDownloadNameForDir(safeName, dir string, isWindows bool) (string, bool) {
	if !isWindows {
		if safeName == "" || isBlockedComfyDownloadExt(safeName) {
			return "", false
		}
		return safeName, true
	}
	dirUTF16Len := colabOneUTF16Len(dir)
	budget := windowsDownloadPathLimit - dirUTF16Len - windowsDownloadPathSepUnits - windowsDownloadSuffixReserve
	if budget < minWindowsDownloadNameUnits {
		return "", false
	}
	name := safeName
	if !utf8.ValidString(name) {
		name = strings.ToValidUTF8(name, "\ufffd")
	}
	if colabOneUTF16Len(name) > budget || budget < maxDownloadNameBytes {
		name = truncateColabOneDownloadNameUTF16(name, budget)
	} else {
		name = trimColabOneDownloadName(name)
	}
	if name == "" || name == "." || name == ".." {
		name = "download"
	}
	if isWindowsReservedDownloadName(name) {
		prefixBudget := budget - colabOneUTF16Len(reservedDownloadPrefix)
		truncated := truncateColabOneDownloadNameUTF16(name, prefixBudget)
		if truncated == "" || truncated == "." || truncated == ".." {
			name = "download"
		} else {
			name = reservedDownloadPrefix + truncated
		}
	}
	if isBlockedComfyDownloadExt(name) {
		return "", false
	}
	return name, true
}

func allowColabOneComfyDownload(rawURL, suggestedName, allowedOrigin string) bool {
	wantOrigin := parseColabOneAllowedOrigin(allowedOrigin)
	if wantOrigin == "" {
		return false
	}
	safeName := sanitizeColabOneDownloadName(suggestedName)
	if isBlockedComfyDownloadExt(safeName) {
		return false
	}
	if allowColabOneComfyNavigate(rawURL, wantOrigin) {
		return true
	}
	if rawURL == "" || strings.TrimSpace(rawURL) != rawURL {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Scheme, "data")
}

func uniqueColabOneDownloadPath(path string) (string, error) {
	return uniqueColabOneDownloadPathWith(path, os.Lstat)
}

func uniqueColabOneDownloadPathWith(path string, lstat func(string) (os.FileInfo, error)) (string, error) {
	if _, err := lstat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
		return "", err
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 2; ; i++ {
		candidate := base + " (" + strconv.Itoa(i) + ")" + ext
		if _, err := lstat(candidate); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return candidate, nil
			}
			return "", err
		}
	}
}

func isUnsafeColabOneSuggestedDirElement(suggested string) bool {
	if suggested == "" || suggested == "." || suggested == ".." {
		return true
	}
	return strings.ContainsAny(suggested, `/\`)
}

func isColabOneDownloadsSiblingPath(p string) bool {
	dlDir, err := mygo.App.Path(mygo.PathDownloads)
	if err != nil || dlDir == "" {
		return false
	}
	cleanDL := filepath.Clean(dlDir)
	cleanPath := filepath.Clean(p)
	if cleanPath == cleanDL {
		return true
	}
	if filepath.Dir(cleanPath) != filepath.Dir(cleanDL) {
		return false
	}
	dlBase := filepath.Base(cleanDL)
	pathBase := filepath.Base(cleanPath)
	return pathBase == dlBase || strings.HasPrefix(pathBase, dlBase+" (")
}

func applyColabOneComfyDownload(e *mygo.DownloadEvent, allowedOrigin string) {
	applyColabOneComfyDownloadForOS(e, allowedOrigin, runtime.GOOS == "windows")
}

func applyColabOneComfyDownloadForOS(e *mygo.DownloadEvent, allowedOrigin string, isWindows bool) {
	if e == nil {
		return
	}
	safeName := sanitizeColabOneDownloadName(e.SuggestedName)
	if !allowColabOneComfyDownload(e.URL, safeName, allowedOrigin) {
		e.PreventDefault()
		return
	}
	// MyGo pre-populates e.Path with a unique path inside PathDownloads for
	// SuggestedName. Preserve it only when SuggestedName is already sanitized
	// (including any Windows MAX_PATH clamping), e.Path is a safe absolute path,
	// and e.Path does not already exist on disk; otherwise resolve a unique
	// sanitized target in dir / PathDownloads (or PathHome).
	canReusePath := e.Path != "" &&
		filepath.IsAbs(e.Path) &&
		!isUnsafeColabOneSuggestedDirElement(e.SuggestedName) &&
		!isColabOneDownloadsSiblingPath(e.Path)
	dir := ""
	if canReusePath {
		dir = filepath.Dir(e.Path)
	}
	if dir == "" || dir == "." {
		if dlDir, err := mygo.App.Path(mygo.PathDownloads); err == nil && dlDir != "" {
			dir = dlDir
		} else if homeDir, err := mygo.App.Path(mygo.PathHome); err == nil && homeDir != "" {
			dir = homeDir
		}
	}
	if dir == "" {
		e.PreventDefault()
		return
	}
	clampedName, ok := clampColabOneDownloadNameForDir(safeName, dir, isWindows)
	if !ok {
		e.PreventDefault()
		return
	}
	safeName = clampedName
	if canReusePath && safeName == e.SuggestedName {
		_, err := os.Lstat(e.Path)
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if err != nil {
			e.PreventDefault()
			return
		}
		targetPath, err := uniqueColabOneDownloadPath(e.Path)
		if err != nil {
			e.PreventDefault()
			return
		}
		e.Path = targetPath
		return
	}
	if os.MkdirAll(dir, 0o755) != nil {
		e.PreventDefault()
		return
	}
	targetPath, err := uniqueColabOneDownloadPath(filepath.Join(dir, safeName))
	if err != nil {
		e.PreventDefault()
		return
	}
	e.Path = targetPath
}

func sanitizeColabOneCanvasError(err error, secretToken string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if secretToken != "" {
		msg = strings.ReplaceAll(msg, secretToken, "[已隐藏]")
		if escaped := url.QueryEscape(secretToken); escaped != "" && escaped != secretToken {
			msg = strings.ReplaceAll(msg, escaped, "[已隐藏]")
		}
	}
	safe := strings.TrimSpace(activity.SafeMessage(msg))
	if safe == "" {
		return errors.New(colabOneT("err.canvasURLInvalid"))
	}
	return errors.New(safe)
}

func colabOneComfyCanvasPageOptions() mygo.PageOptions {
	return mygo.PageOptions{
		Untrusted: true,
		Session:   mygo.NewEphemeralSession(),
	}
}

func (d *ColabOneDesktop) clearComfyWindowIfMatch(win *mygo.Window) {
	d.comfyMu.Lock()
	if d.comfyWin == win {
		d.comfyWin = nil
		d.comfyOrigin = ""
	}
	d.comfyMu.Unlock()
}

func (d *ColabOneDesktop) activeComfyOrigin(win *mygo.Window) string {
	d.comfyMu.Lock()
	defer d.comfyMu.Unlock()
	if d.comfyWin != win {
		return ""
	}
	return d.comfyOrigin
}

// OpenComfyCanvas opens the authenticated ComfyUI canvas either in a reusable
// native desktop window or the system browser. The URL is always fetched from
// the in-process Go controller, never accepted from the frontend.
func (d *ColabOneDesktop) OpenComfyCanvas(ctx context.Context, target string) error {
	if target != "window" && target != "browser" {
		return errors.New(colabOneT("err.canvasTarget"))
	}
	if d.bridge == nil {
		return errors.New(colabOneT("err.bridgeNotInitialized"))
	}
	raw, err := d.bridge.call(ctx, "comfyui.canvas.url", nil)
	if err != nil {
		return err
	}
	var resp struct {
		URL string `json:"url"`
	}
	if err = json.Unmarshal([]byte(raw), &resp); err != nil {
		return errors.New(colabOneT("err.canvasResponse"))
	}
	u, err := validateColabOneCanvasURL(resp.URL)
	if err != nil {
		return err
	}
	canvasOrigin := colabOneHTTPSOrigin(u)
	if canvasOrigin == "" {
		return errors.New(colabOneT("err.canvasURLInvalid"))
	}
	canvasURL := u.String()
	canvasToken := u.Query().Get("token")
	if target == "browser" {
		if err := mygo.Shell.OpenExternal(canvasURL); err != nil {
			return sanitizeColabOneCanvasError(err, canvasToken)
		}
		return nil
	}
	if d.appQuitting.Load() || !d.isAppReady() {
		return ErrBridgeClosed
	}
	var openErr error
	mygo.RunOnMain(func() {
		if d.appQuitting.Load() || !d.isAppReady() {
			openErr = ErrBridgeClosed
			return
		}
		d.comfyMu.Lock()
		existing := d.comfyWin
		prevOrigin := d.comfyOrigin
		d.comfyMu.Unlock()
		if existing != nil {
			if !existing.IsDestroyed() && prevOrigin == canvasOrigin {
				if page := existing.Page(); page != nil {
					if err := page.LoadURL(canvasURL); err != nil {
						openErr = sanitizeColabOneCanvasError(err, canvasToken)
						return
					}
				}
				revealColabOneWindow(existing)
				return
			}
			// Never reuse a canvas window across origins: Back/Forward history
			// entries retain prevOrigin and bypass OnWillNavigate (nav.Unasked).
			// Clear state before Destroy() so an immediate or delayed OnClosed
			// callback for existing cannot clobber the replacement window.
			d.clearComfyWindowIfMatch(existing)
			if !existing.IsDestroyed() {
				existing.Destroy()
			}
		}
		bg := colabOneWindowBackground()
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:           colabOneT("window.comfyCanvas"),
			URL:             "",
			Width:           1440,
			Height:          900,
			MinWidth:        comfyMinWidth,
			MinHeight:       comfyMinHeight,
			StateKey:        "comfyui-canvas",
			BackgroundColor: bg,
			Page:            colabOneComfyCanvasPageOptions(),
		})
		applyWindowIcon(win)
		trackColabOneWindowState(d.winStore, "comfyui-canvas", win, 1440, 900, comfyMinWidth, comfyMinHeight)
		win.OnClosed(func() {
			d.clearComfyWindowIfMatch(win)
		})
		if win.IsDestroyed() {
			return
		}
		d.comfyMu.Lock()
		d.comfyWin = win
		d.comfyOrigin = canvasOrigin
		d.comfyMu.Unlock()
		if page := win.Page(); page != nil {
			page.OnWillNavigate(func(e *mygo.NavigateEvent) {
				if !allowColabOneComfyNavigate(e.URL, d.activeComfyOrigin(win)) {
					e.PreventDefault()
				}
			})
			page.OnWillDownload(func(e *mygo.DownloadEvent) {
				applyColabOneComfyDownload(e, d.activeComfyOrigin(win))
			})
			if err := page.LoadURL(canvasURL); err != nil {
				openErr = sanitizeColabOneCanvasError(err, canvasToken)
				d.clearComfyWindowIfMatch(win)
				if !win.IsDestroyed() {
					win.Destroy()
				}
				return
			}
		}
	})
	return openErr
}
