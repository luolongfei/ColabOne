package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const colabOneBarkMaxRespBytes = 64 << 10

type colabOneBarkChannel struct {
	baseClient *http.Client
}

func newColabOneBarkChannel(baseClient *http.Client) *colabOneBarkChannel {
	return &colabOneBarkChannel{
		baseClient: baseClient,
	}
}

func (c *colabOneBarkChannel) ID() string {
	return ChannelBark
}

func (c *colabOneBarkChannel) IsRemote() bool {
	return true
}

type colabOneBarkResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *colabOneBarkChannel) Send(
	ctx context.Context,
	cfg ColabOneNotificationConfig,
	event ColabOneNotificationEvent,
) error {
	deviceKey := strings.TrimSpace(cfg.Bark.DeviceKey)
	if deviceKey == "" {
		return errors.New("请先填写 Bark 推送地址或 Device Key")
	}
	if err := colabOneRejectControlOrSpace(deviceKey, "Bark Device Key"); err != nil {
		return err
	}
	if strings.ContainsRune(deviceKey, '/') {
		return errors.New("Bark Device Key 不能包含斜杠，若需填写完整地址请包含 https:// 前缀")
	}
	serverURL := COLABONE_DEFAULT_BARK_SERVER_URL
	if strings.TrimSpace(cfg.Bark.ServerURL) != "" {
		cleanServer, err := colabOneNormalizeBarkBaseURL(cfg.Bark.ServerURL)
		if err != nil {
			return err
		}
		if cleanServer != "" {
			serverURL = cleanServer
		}
	}

	title := strings.TrimSpace(event.Title)
	if title == "" {
		title = "ColabOne"
	}
	subtitle := strings.TrimSpace(event.Subtitle)
	body := colabOneBuildBarkBody(event)

	payload := map[string]any{
		"device_key": deviceKey,
		"title":      title,
		"body":       body,
		"isArchive":  "1",
	}
	if subtitle != "" {
		payload["subtitle"] = subtitle
	}
	if group := strings.TrimSpace(cfg.Bark.Group); group != "" {
		payload["group"] = group
	}
	if sound := strings.TrimSpace(cfg.Bark.Sound); sound != "" {
		payload["sound"] = sound
	}

	level := strings.TrimSpace(cfg.Bark.Level)
	if level == "" && (event.Severity == SeverityError || event.Severity == SeverityWarning) {
		level = "timeSensitive"
	}
	if level != "" {
		payload["level"] = level
	}

	rawBody, err := json.Marshal(payload)
	if err != nil {
		return errors.New(RedactSecrets(err.Error(), cfg))
	}

	endpoint := serverURL + "/push"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(rawBody))
	if err != nil {
		return errors.New(RedactSecrets(err.Error(), cfg))
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	var client *http.Client
	if c.baseClient != nil {
		copied := *c.baseClient
		copied.CheckRedirect = colabOneNoRedirect
		client = &copied
	} else {
		client = &http.Client{
			Timeout:       20 * time.Second,
			CheckRedirect: colabOneNoRedirect,
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return errors.New(RedactSecrets(fmt.Sprintf("Bark 推送请求失败：%v", err), cfg))
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("Bark 推送被重定向（HTTP %d），已拒绝跟随重定向，请检查推送地址配置", resp.StatusCode)
	}

	respBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, colabOneBarkMaxRespBytes))
	if readErr != nil {
		return errors.New(RedactSecrets(fmt.Sprintf("读取 Bark 响应失败：%v", readErr), cfg))
	}

	trimmedResp := bytes.TrimSpace(respBytes)
	var parsed colabOneBarkResponse
	if len(trimmedResp) > 0 && trimmedResp[0] == '{' {
		if jsonErr := json.Unmarshal(trimmedResp, &parsed); jsonErr == nil {
			if resp.StatusCode == http.StatusOK && parsed.Code == 200 {
				return nil
			}
			detail := colabOneTruncateRunes(strings.TrimSpace(parsed.Message), 200)
			if detail != "" {
				return errors.New(RedactSecrets(fmt.Sprintf("Bark 推送失败 (HTTP %d / code %d): %s", resp.StatusCode, parsed.Code, detail), cfg))
			}
			return errors.New(RedactSecrets(fmt.Sprintf("Bark 推送失败 (HTTP %d / code %d)", resp.StatusCode, parsed.Code), cfg))
		}
	}

	return fmt.Errorf("Bark 服务返回非 JSON 响应（HTTP %d），请检查推送地址配置", resp.StatusCode)
}

func colabOneBuildBarkBody(event ColabOneNotificationEvent) string {
	var sections []string
	if body := strings.TrimSpace(event.Body); body != "" {
		sections = append(sections, body)
	}
	var fieldLines []string
	for _, f := range event.Fields {
		lbl := strings.TrimSpace(f.Label)
		val := strings.TrimSpace(f.Value)
		if lbl == "" && val == "" {
			continue
		}
		if lbl != "" && val != "" {
			fieldLines = append(fieldLines, lbl+": "+val)
		} else if val != "" {
			fieldLines = append(fieldLines, val)
		}
	}
	if len(fieldLines) > 0 {
		sections = append(sections, strings.Join(fieldLines, "\n"))
	}
	if hint := strings.TrimSpace(event.ActionHint); hint != "" {
		sections = append(sections, hint)
	}
	if len(sections) == 0 {
		return strings.TrimSpace(event.Title)
	}
	return strings.Join(sections, "\n\n")
}
