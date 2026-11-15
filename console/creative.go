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
	"os"
	"path/filepath"
	"regexp"
)

type mediaChunk struct {
	Data      string `json:"data"`
	Size      int64  `json:"size"`
	Offset    int64  `json:"offset"`
	SHA256    string `json:"sha256"`
	Extension string `json:"extension"`
}

// ExportCreation accepts only a remote artifact ID. The save destination comes
// from the native dialog, never a path supplied by remote model output.
func (d *Desktop) ExportCreation(ctx context.Context, id string) (string, error) {
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(id) {
		return "", errors.New("作品编号不合法")
	}
	value, err := d.bridge.call(ctx, "creative.asset", map[string]any{"id": id, "offset": 0})
	if err != nil {
		return "", err
	}
	var chunk mediaChunk
	if err = json.Unmarshal([]byte(value), &chunk); err != nil {
		return "", err
	}
	if chunk.Size <= 0 || chunk.Size > 64<<20 || (chunk.Extension != "png" && chunk.Extension != "webm") {
		return "", errors.New("作品格式或大小不合法")
	}
	size, digest := chunk.Size, chunk.SHA256
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: mygo.CallerWindow(ctx), DefaultPath: "ColabOne-" + id[:8] + "." + chunk.Extension})
	if err != nil || path == "" {
		return "", err
	}
	out, err := os.CreateTemp(filepath.Dir(path), ".colabone-export-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(out.Name())
	defer out.Close()
	hash := sha256.New()
	offset := int64(0)
	for {
		if chunk.Offset != offset || chunk.Size != size || chunk.SHA256 != digest {
			return "", errors.New("作品传输状态发生变化")
		}
		data, err := base64.StdEncoding.DecodeString(chunk.Data)
		if err != nil || len(data) == 0 || len(data) > 512<<10 || offset+int64(len(data)) > size {
			return "", errors.New("作品传输不完整")
		}
		if _, err = out.Write(data); err != nil {
			return "", err
		}
		hash.Write(data)
		offset += int64(len(data))
		if offset == size {
			break
		}
		value, err = d.bridge.call(ctx, "creative.asset", map[string]any{"id": id, "offset": offset})
		if err != nil {
			return "", err
		}
		if err = json.Unmarshal([]byte(value), &chunk); err != nil {
			return "", err
		}
	}
	if hex.EncodeToString(hash.Sum(nil)) != digest {
		return "", errors.New("作品校验失败，请重试导出")
	}
	if err = out.Close(); err != nil {
		return "", err
	}
	if err = os.Rename(out.Name(), path); err != nil {
		return "", fmt.Errorf("无法保存作品：%w", err)
	}
	return path, nil
}
