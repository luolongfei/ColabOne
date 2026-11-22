package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/egoist/mygo"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	maxWorkflowOutputSize = 512 << 20
	maxOutputChunkBytes   = 512 << 10
)

var (
	workflowOutputIDPattern     = regexp.MustCompile(`^[a-f0-9]{32}$`)
	workflowOutputSHA256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
	allowedOutputExtensions = map[string]bool{
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
)

type colabOneMediaChunk struct {
	Data      string `json:"data"`
	Size      int64  `json:"size"`
	Offset    int64  `json:"offset"`
	SHA256    string `json:"sha256"`
	Extension string `json:"extension"`
	Filename  string `json:"filename"`
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
	if chunk.Size <= 0 || chunk.Size > maxWorkflowOutputSize || !allowedOutputExtensions[ext] || !workflowOutputSHA256Pattern.MatchString(digest) {
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
		if chunk.Offset != offset || chunk.Size != size || strings.ToLower(strings.TrimSpace(chunk.SHA256)) != digest {
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
	if hex.EncodeToString(hash.Sum(nil)) != digest {
		return "", errors.New(colabOneT("err.outputHashMismatch"))
	}
	if err = out.Sync(); err != nil {
		return "", err
	}
	if err = out.Close(); err != nil {
		return "", err
	}
	var renameErr error
	for range 4 {
		renameErr = os.Rename(tmpPath, path)
		if renameErr == nil {
			keepTmp = true
			return path, nil
		}
		time.Sleep(15 * time.Millisecond)
	}
	_ = os.Remove(tmpPath)
	return "", fmt.Errorf("%s%w", colabOneT("err.outputSaveFailedPrefix"), renameErr)
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
		return nil, errors.New(colabOneT("err.canvasURLLoopback"))
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

// OpenComfyCanvas opens the authenticated ComfyUI canvas either in a reusable
// native desktop window or the system browser. The URL is always fetched from
// the trusted Python bridge, never accepted from the frontend.
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
	canvasURL := u.String()
	if target == "browser" {
		return mygo.Shell.OpenExternal(canvasURL)
	}
	var openErr error
	mygo.RunOnMain(func() {
		d.comfyMu.Lock()
		existing := d.comfyWin
		d.comfyMu.Unlock()
		if existing != nil && !existing.IsDestroyed() {
			if page := existing.Page(); page != nil {
				if err := page.LoadURL(canvasURL); err != nil {
					openErr = err
					return
				}
			}
			existing.Show()
			existing.Focus()
			return
		}
		bg := colabOneWindowBackground()
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:           colabOneT("window.comfyCanvas"),
			URL:             canvasURL,
			Width:           1440,
			Height:          900,
			MinWidth:        comfyMinWidth,
			MinHeight:       comfyMinHeight,
			StateKey:        "comfyui-canvas",
			BackgroundColor: bg,
			Page:            mygo.PageOptions{Untrusted: true},
		})
		applyWindowIcon(win)
		trackColabOneWindowState(d.winStore, "comfyui-canvas", win, 1440, 900, comfyMinWidth, comfyMinHeight)
		win.OnClosed(func() {
			d.comfyMu.Lock()
			if d.comfyWin == win {
				d.comfyWin = nil
			}
			d.comfyMu.Unlock()
		})
		d.comfyMu.Lock()
		if !win.IsDestroyed() {
			d.comfyWin = win
		}
		d.comfyMu.Unlock()
	})
	return openErr
}
