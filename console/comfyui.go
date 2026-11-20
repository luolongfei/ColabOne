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
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	maxWorkflowOutputSize = 512 << 20
	maxOutputChunkBytes   = 512 << 10
)

var (
	workflowOutputIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
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
		return "", errors.New("输出文件编号不合法")
	}
	if d.bridge == nil {
		return "", errors.New("控制进程未初始化")
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
	if chunk.Size <= 0 || chunk.Size > maxWorkflowOutputSize || !allowedOutputExtensions[ext] {
		return "", errors.New("输出文件格式或大小不合法")
	}
	size, digest := chunk.Size, chunk.SHA256
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Parent:      mygo.CallerWindow(ctx),
		DefaultPath: "ColabOne-" + id[:8] + "." + ext,
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
		if chunk.Offset != offset || chunk.Size != size || chunk.SHA256 != digest {
			return "", errors.New("输出文件传输状态发生变化")
		}
		data, err := base64.StdEncoding.DecodeString(chunk.Data)
		if err != nil || len(data) == 0 || len(data) > maxOutputChunkBytes || offset+int64(len(data)) > size {
			return "", errors.New("输出文件传输不完整")
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
		if err = json.Unmarshal([]byte(value), &chunk); err != nil {
			return "", err
		}
	}
	if hex.EncodeToString(hash.Sum(nil)) != digest {
		return "", errors.New("输出文件校验失败，请重试导出")
	}
	if err = out.Close(); err != nil {
		return "", err
	}
	if err = os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("无法保存输出文件：%w", err)
	}
	keepTmp = true
	return path, nil
}

func validateColabOneCanvasURL(raw string) (*url.URL, error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return nil, errors.New("画布地址不能为空")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("画布地址格式无效")
	}
	if u.Scheme != "https" || u.Opaque != "" {
		return nil, errors.New("画布地址仅支持 HTTPS")
	}
	if u.User != nil {
		return nil, errors.New("画布地址不允许包含认证用户信息")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" {
		return nil, errors.New("画布地址缺少有效主机名")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return nil, errors.New("画布地址不允许指向本机回环地址")
	}
	if host == "trycloudflare.com" || strings.HasSuffix(host, ".trycloudflare.com") {
		return nil, errors.New("画布地址不支持临时域名，请使用固定域名")
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return nil, errors.New("画布地址不允许指向本机回环地址")
	}
	if u.Path != "/_colabone_auth" {
		return nil, errors.New("画布认证路径不合法")
	}
	if strings.TrimSpace(u.Query().Get("token")) == "" {
		return nil, errors.New("画布认证地址缺少访问令牌")
	}
	return u, nil
}

// OpenComfyCanvas opens the authenticated ComfyUI canvas either in a reusable
// native desktop window or the system browser. The URL is always fetched from
// the trusted Python bridge, never accepted from the frontend.
func (d *ColabOneDesktop) OpenComfyCanvas(ctx context.Context, target string) error {
	if target != "window" && target != "browser" {
		return errors.New("不支持的画布打开方式")
	}
	if d.bridge == nil {
		return errors.New("控制进程未初始化")
	}
	raw, err := d.bridge.call(ctx, "comfyui.canvas.url", nil)
	if err != nil {
		return err
	}
	var resp struct {
		URL string `json:"url"`
	}
	if err = json.Unmarshal([]byte(raw), &resp); err != nil {
		return errors.New("画布地址响应无效")
	}
	u, err := validateColabOneCanvasURL(resp.URL)
	if err != nil {
		return err
	}
	canvasURL := u.String()
	if target == "browser" {
		return mygo.Shell.OpenExternal(canvasURL)
	}
	d.comfyMu.Lock()
	existing := d.comfyWin
	d.comfyMu.Unlock()
	if existing != nil && !existing.IsDestroyed() {
		if page := existing.Page(); page != nil {
			if err := page.LoadURL(canvasURL); err != nil {
				return err
			}
		}
		existing.Show()
		existing.Focus()
		return nil
	}
	bg := colabOneWindowBackground()
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:           "ComfyUI 原生画布 · ColabOne",
		URL:             canvasURL,
		Width:           1440,
		Height:          900,
		MinWidth:        comfyMinWidth,
		MinHeight:       comfyMinHeight,
		StateKey:        "comfyui-canvas",
		BackgroundColor: bg,
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
	if prev := d.comfyWin; prev != nil && !prev.IsDestroyed() {
		d.comfyMu.Unlock()
		win.Close()
		if page := prev.Page(); page != nil {
			if err := page.LoadURL(canvasURL); err != nil {
				return err
			}
		}
		prev.Show()
		prev.Focus()
		return nil
	}
	d.comfyWin = win
	d.comfyMu.Unlock()
	if win.IsDestroyed() {
		d.comfyMu.Lock()
		if d.comfyWin == win {
			d.comfyWin = nil
		}
		d.comfyMu.Unlock()
	}
	return nil
}
