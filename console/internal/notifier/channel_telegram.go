package notifier

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/netip"
	"net/textproto"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/luolongfei/ColabOne/console/internal/assignproxy"
	"github.com/luolongfei/ColabOne/console/internal/outboundlog"
)

const (
	colabOneTelegramMaxMessageRunes = 4096
	colabOneTelegramMaxCaptionRunes = 1024
	colabOneTelegramMaxRespBytes    = 64 << 10
)

type colabOneTelegramChannel struct {
	baseClient *http.Client
}

func newColabOneTelegramChannel(baseClient *http.Client) *colabOneTelegramChannel {
	return &colabOneTelegramChannel{
		baseClient: baseClient,
	}
}

func (c *colabOneTelegramChannel) ID() string {
	return ChannelTelegram
}

func (c *colabOneTelegramChannel) IsRemote() bool {
	return true
}

type colabOneTelegramAPIResponse struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
}

type colabOneTelegramAPIError struct {
	StatusCode  int
	ErrorCode   int
	Description string
	NoFallback  bool
	IsRedirect  bool
	RawNonJSON  bool
}

func (e *colabOneTelegramAPIError) Error() string {
	if e.IsRedirect {
		return fmt.Sprintf("Telegram 请求被重定向（HTTP %d），已拒绝跟随重定向，请检查 API 地址或代理配置", e.StatusCode)
	}
	if e.RawNonJSON {
		return fmt.Sprintf("Telegram API 返回非 JSON 响应（HTTP %d），请检查 API 地址或代理配置", e.StatusCode)
	}
	if e.Description != "" {
		return fmt.Sprintf("Telegram 接口返回错误 (HTTP %d / code %d): %s", e.StatusCode, e.ErrorCode, e.Description)
	}
	return fmt.Sprintf("Telegram 接口返回异常状态 (HTTP %d)", e.StatusCode)
}

func (e *colabOneTelegramAPIError) isAuthError() bool {
	return e.StatusCode == http.StatusUnauthorized ||
		e.StatusCode == http.StatusForbidden ||
		e.ErrorCode == http.StatusUnauthorized ||
		e.ErrorCode == http.StatusForbidden
}

func (c *colabOneTelegramChannel) Send(
	ctx context.Context,
	cfg ColabOneNotificationConfig,
	event ColabOneNotificationEvent,
) error {
	token := strings.TrimSpace(cfg.Telegram.BotToken)
	chatID := strings.TrimSpace(cfg.Telegram.ChatID)
	if token == "" || chatID == "" {
		return errors.New("请先填写 Telegram Bot Token 和 Chat ID")
	}
	if err := colabOneValidateTelegramBotToken(token); err != nil {
		return err
	}
	if err := colabOneRejectControlOrSpace(chatID, "Telegram Chat ID"); err != nil {
		return err
	}

	apiBase := COLABONE_DEFAULT_TELEGRAM_API_BASE
	if strings.TrimSpace(cfg.Telegram.APIBaseURL) != "" {
		cleanBase, err := colabOneValidateHTTPBaseURL(cfg.Telegram.APIBaseURL, "Telegram API 地址")
		if err != nil {
			return err
		}
		if cleanBase != "" {
			apiBase = cleanBase
		}
	}
	client, cleanup, err := c.httpClientForConfig(cfg)
	if err != nil {
		return errors.New(RedactSecrets(err.Error(), cfg))
	}
	if cleanup != nil {
		defer cleanup()
	}

	if len(event.ImageBytes) > 0 && cfg.Telegram.SendPreviewImage {
		imgData, _, _, _, _, prepErr := PreparePreviewImage(event.ImageBytes, 1280, 4<<20, false)
		if prepErr == nil && len(imgData) > 0 {
			caption := colabOneFormatTelegramHTML(event, colabOneTelegramMaxCaptionRunes)
			photoErr := c.sendPhoto(ctx, client, apiBase, token, chatID, caption, imgData, event.ImageFilename)
			if photoErr == nil {
				return nil
			}
			if ctx != nil && ctx.Err() != nil {
				return errors.New(RedactSecrets(photoErr.Error(), cfg))
			}
			var apiErr *colabOneTelegramAPIError
			if errors.As(photoErr, &apiErr) && (apiErr.isAuthError() || apiErr.NoFallback) {
				return errors.New(RedactSecrets(photoErr.Error(), cfg))
			}
			// Non-auth photo upload failure falls back cleanly to sendMessage.
		}
	}

	text := colabOneFormatTelegramHTML(event, colabOneTelegramMaxMessageRunes)
	if err := c.sendMessage(ctx, client, apiBase, token, chatID, text); err != nil {
		return errors.New(RedactSecrets(err.Error(), cfg))
	}
	return nil
}

func (c *colabOneTelegramChannel) sendMessage(
	ctx context.Context,
	client *http.Client,
	apiBase, token, chatID, htmlText string,
) error {
	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", apiBase, token)
	payload := map[string]any{
		"chat_id":                  chatID,
		"text":                     htmlText,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	rawBody, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(rawBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	return colabOneDoTelegramRequest(client, req)
}

func (c *colabOneTelegramChannel) sendPhoto(
	ctx context.Context,
	client *http.Client,
	apiBase, token, chatID, caption string,
	photoJPEG []byte,
	filename string,
) error {
	endpoint := fmt.Sprintf("%s/bot%s/sendPhoto", apiBase, token)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.WriteField("chat_id", chatID); err != nil {
		return err
	}
	if err := mw.WriteField("parse_mode", "HTML"); err != nil {
		return err
	}
	if caption != "" {
		if err := mw.WriteField("caption", caption); err != nil {
			return err
		}
	}

	uploadName := colabOneSanitizeUploadFilename(filename, "preview.jpg")
	ext := filepath.Ext(uploadName)
	lowerExt := strings.ToLower(ext)
	if lowerExt != ".jpg" && lowerExt != ".jpeg" {
		base := strings.TrimRight(strings.TrimSuffix(uploadName, ext), ".")
		if base == "" {
			base = "preview"
		}
		if len(base) > 60 {
			base = base[:60]
		}
		uploadName = base + ".jpg"
	}
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", fmt.Sprintf(`form-data; name="photo"; filename="%s"`, uploadName))
	partHeader.Set("Content-Type", "image/jpeg")
	pw, err := mw.CreatePart(partHeader)
	if err != nil {
		return err
	}
	if _, err := pw.Write(photoJPEG); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return colabOneDoTelegramRequest(client, req)
}

func colabOneDoTelegramRequest(client *http.Client, req *http.Request) error {
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Telegram 请求失败：%w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return &colabOneTelegramAPIError{
			StatusCode: resp.StatusCode,
			NoFallback: true,
			IsRedirect: true,
		}
	}

	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, colabOneTelegramMaxRespBytes))
	if readErr != nil {
		return fmt.Errorf("读取 Telegram 响应失败：%w", readErr)
	}

	trimmedRaw := bytes.TrimSpace(raw)
	var parsed colabOneTelegramAPIResponse
	if len(trimmedRaw) > 0 && trimmedRaw[0] == '{' {
		if jsonErr := json.Unmarshal(trimmedRaw, &parsed); jsonErr == nil {
			if resp.StatusCode == http.StatusOK && parsed.OK {
				return nil
			}
			return &colabOneTelegramAPIError{
				StatusCode:  resp.StatusCode,
				ErrorCode:   parsed.ErrorCode,
				Description: colabOneTruncateRunes(strings.TrimSpace(parsed.Description), 200),
			}
		}
	}

	return &colabOneTelegramAPIError{
		StatusCode: resp.StatusCode,
		NoFallback: resp.StatusCode == http.StatusOK,
		RawNonJSON: true,
	}
}

func colabOneSanitizeUploadFilename(raw, fallback string) string {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return fallback
	}
	if idx := strings.LastIndexAny(clean, "/\\"); idx >= 0 {
		clean = clean[idx+1:]
	}
	var b strings.Builder
	for _, r := range clean {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), ".")
	if out == "" {
		return fallback
	}
	const maxFilenameLen = 64
	if len(out) > maxFilenameLen {
		ext := filepath.Ext(out)
		if len(ext) > 0 && len(ext) <= 8 {
			baseMax := maxFilenameLen - len(ext)
			if baseMax < 1 {
				return fallback
			}
			base := strings.TrimRight(strings.TrimSuffix(out, ext), ".")
			if len(base) > baseMax {
				base = base[:baseMax]
			}
			if base == "" {
				return fallback
			}
			out = base + ext
		} else {
			out = out[:maxFilenameLen]
		}
	}
	return out
}

func colabOneFormatTelegramHTML(event ColabOneNotificationEvent, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = colabOneTelegramMaxMessageRunes
	}
	locale := NormalizeLocale(event.Locale)
	msg := colabOneMessagesForLocale(locale)
	badge := colabOneSeverityLabel(event.Severity, locale)

	title := strings.TrimSpace(event.Title)
	if title == "" {
		title = "ColabOne"
	}
	subtitle := strings.TrimSpace(event.Subtitle)
	body := strings.TrimSpace(event.Body)
	actionHint := strings.TrimSpace(event.ActionHint)

	render := func(t, sub, b string, fields []ColabOneNotificationField, hint string) string {
		var sb strings.Builder
		sb.WriteString("<b>[")
		sb.WriteString(html.EscapeString(badge))
		sb.WriteString("] ")
		sb.WriteString(html.EscapeString(t))
		sb.WriteString("</b>")
		if sub != "" {
			sb.WriteString("\n<i>")
			sb.WriteString(html.EscapeString(sub))
			sb.WriteString("</i>")
		}
		if b != "" {
			sb.WriteString("\n\n")
			sb.WriteString(html.EscapeString(b))
		}
		if len(fields) > 0 {
			var fieldLines []string
			for _, f := range fields {
				lbl := strings.TrimSpace(f.Label)
				val := strings.TrimSpace(f.Value)
				if lbl == "" && val == "" {
					continue
				}
				if lbl != "" && val != "" {
					fieldLines = append(fieldLines, "<b>"+html.EscapeString(lbl)+":</b> "+html.EscapeString(val))
				} else if val != "" {
					fieldLines = append(fieldLines, html.EscapeString(val))
				}
			}
			if len(fieldLines) > 0 {
				sb.WriteString("\n\n")
				sb.WriteString(strings.Join(fieldLines, "\n"))
			}
		}
		if hint != "" {
			sb.WriteString("\n\n<i>")
			sb.WriteString(html.EscapeString(msg.ActionHintLabel))
			sb.WriteString(": ")
			sb.WriteString(html.EscapeString(hint))
			sb.WriteString("</i>")
		}
		return sb.String()
	}

	full := render(title, subtitle, body, event.Fields, actionHint)
	if utf8.RuneCountInString(full) <= maxRunes {
		return full
	}

	// Progressively trim plain text segments before HTML escaping so tags/entities are never split.
	fields := event.Fields
	hint := actionHint
	if hint != "" {
		hint = colabOneTruncateRunes(hint, 140)
		candidate := render(title, subtitle, body, fields, hint)
		if utf8.RuneCountInString(candidate) <= maxRunes {
			return candidate
		}
	}

	if body != "" {
		overhead := utf8.RuneCountInString(render(title, subtitle, "", fields, hint))
		budget := maxRunes - overhead - 32
		if budget > 40 {
			// Account for potential HTML entity expansion by shrinking until it fits.
			for limit := budget; limit >= 24; limit -= 24 {
				candidate := render(title, subtitle, colabOneTruncateRunes(body, limit), fields, hint)
				if utf8.RuneCountInString(candidate) <= maxRunes {
					return candidate
				}
			}
		}
	}

	// Try dropping hint alone first while preserving all fields.
	if hint != "" {
		hint = ""
		candidate := render(title, subtitle, body, fields, hint)
		if utf8.RuneCountInString(candidate) <= maxRunes {
			return candidate
		}
		if body != "" {
			overhead := utf8.RuneCountInString(render(title, subtitle, "", fields, hint))
			budget := maxRunes - overhead - 32
			if budget > 40 {
				for limit := budget; limit >= 24; limit -= 24 {
					candidate := render(title, subtitle, colabOneTruncateRunes(body, limit), fields, hint)
					if utf8.RuneCountInString(candidate) <= maxRunes {
						return candidate
					}
				}
			}
		}
	}

	// Trim fields if caption is still too long.
	for len(fields) > 0 {
		fields = fields[:len(fields)-1]
		candidate := render(title, subtitle, colabOneTruncateRunes(body, 220), fields, hint)
		if utf8.RuneCountInString(candidate) <= maxRunes {
			return candidate
		}
	}

	// Minimal title + compact body fallback guaranteed <= maxRunes.
	compactTitle := colabOneTruncateRunes(title, 120)
	for limit := 300; limit >= 0; limit -= 40 {
		b := ""
		if limit > 0 && body != "" {
			b = colabOneTruncateRunes(body, limit)
		}
		candidate := render(compactTitle, "", b, nil, "")
		if utf8.RuneCountInString(candidate) <= maxRunes {
			return candidate
		}
	}
	return "<b>" + html.EscapeString(colabOneTruncateRunes(title, maxRunes/6)) + "</b>"
}

func colabOneTruncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return strings.TrimSpace(string(runes[:max-3])) + "..."
}

func (c *colabOneTelegramChannel) httpClientForConfig(
	cfg ColabOneNotificationConfig,
) (*http.Client, func(), error) {
	directClient := func() *http.Client {
		if c.baseClient != nil {
			copied := *c.baseClient
			copied.CheckRedirect = colabOneNoRedirect
			return &copied
		}
		return &http.Client{
			Timeout:       20 * time.Second,
			CheckRedirect: colabOneNoRedirect,
		}
	}

	proxyRaw := strings.TrimSpace(cfg.Telegram.ProxyURL)
	if proxyRaw == "" {
		return directClient(), nil, nil
	}

	spec, err := assignproxy.ParseAssignProxy(proxyRaw)
	if err != nil {
		return nil, nil, err
	}
	if spec == nil {
		return directClient(), nil, nil
	}

	var (
		tlsCfg       *tls.Config
		outLogger    *outboundlog.ColabOneOutboundLogger
		outComponent string
	)
	if c.baseClient != nil {
		rt := c.baseClient.Transport
		for depth := 0; depth < 8 && rt != nil; depth++ {
			if ot, ok := rt.(*outboundlog.ColabOneOutboundTransport); ok && ot != nil {
				if outLogger == nil && ot.Logger != nil {
					outLogger = ot.Logger
					outComponent = ot.Component
				}
				rt = ot.Base
				continue
			}
			break
		}
		if baseTr, ok := rt.(*http.Transport); ok && baseTr != nil && baseTr.TLSClientConfig != nil {
			tlsCfg = baseTr.TLSClientConfig.Clone()
		}
	}
	if tlsCfg == nil {
		tlsCfg = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}

	tr := &http.Transport{
		TLSClientConfig:       tlsCfg,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		IdleConnTimeout:       15 * time.Second,
		MaxIdleConns:          4,
	}

	if spec.IsSOCKS5() {
		tr.Proxy = nil
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if network != "" && !strings.HasPrefix(network, "tcp") {
				return nil, fmt.Errorf("SOCKS5 代理不支持网络类型 %s", network)
			}
			return colabOneDialSOCKS5(ctx, *spec, addr, 15*time.Second)
		}
	} else {
		proxyHost := net.JoinHostPort(spec.Host, strconv.Itoa(spec.Port))
		proxyURL := &url.URL{
			Scheme: spec.Scheme,
			Host:   proxyHost,
		}
		if spec.HasAuth() {
			proxyURL.User = url.UserPassword(spec.Username, spec.Password)
		}
		tr.Proxy = http.ProxyURL(proxyURL)
		tr.DialContext = (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext
	}

	var finalRT http.RoundTripper = tr
	if outLogger != nil {
		comp := outComponent
		if comp == "" {
			comp = "notifier_telegram"
		}
		proxyLabel := spec.LogLabel
		if proxyLabel == "" {
			proxyLabel = spec.MaskedURL
		}
		finalRT = outboundlog.WrapTransport(tr, outLogger, comp, proxyLabel)
	}

	timeout := 20 * time.Second
	if c.baseClient != nil && c.baseClient.Timeout > 0 {
		timeout = c.baseClient.Timeout
	}
	client := &http.Client{
		Transport:     finalRT,
		Timeout:       timeout,
		CheckRedirect: colabOneNoRedirect,
	}
	return client, tr.CloseIdleConnections, nil
}

func colabOneDialSOCKS5(
	ctx context.Context,
	spec assignproxy.ColabOneAssignProxySpec,
	targetAddr string,
	connectTimeout time.Duration,
) (net.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	targetHost, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		return nil, fmt.Errorf("无效的目标地址：%w", err)
	}
	targetPort, err := strconv.Atoi(portStr)
	if err != nil || targetPort < 1 || targetPort > 65535 {
		return nil, fmt.Errorf("无效的目标端口：%s", portStr)
	}

	deadline := time.Now().Add(connectTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	dialCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	proxyAddr := net.JoinHostPort(spec.Host, strconv.Itoa(spec.Port))
	dialer := &net.Dialer{Timeout: connectTimeout, KeepAlive: 30 * time.Second}
	conn, err := dialer.DialContext(dialCtx, "tcp", proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("连接 SOCKS5 代理失败：%w", err)
	}

	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return nil, err
	}
	stopWatch := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
	})

	if err := colabOneSOCKS5Handshake(conn, spec, targetHost, targetPort); err != nil {
		stopWatch()
		_ = conn.Close()
		return nil, err
	}
	if !stopWatch() {
		_ = conn.Close()
		return nil, ctx.Err()
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func colabOneSOCKS5Handshake(
	conn net.Conn,
	spec assignproxy.ColabOneAssignProxySpec,
	targetHost string,
	targetPort int,
) error {
	var greeting []byte
	if spec.HasAuth() {
		greeting = []byte{0x05, 0x02, 0x00, 0x02}
	} else {
		greeting = []byte{0x05, 0x01, 0x00}
	}
	if _, err := conn.Write(greeting); err != nil {
		return err
	}

	var methodReply [2]byte
	if _, err := io.ReadFull(conn, methodReply[:]); err != nil {
		return err
	}
	if methodReply[0] != 0x05 {
		return fmt.Errorf("非法的 SOCKS5 响应版本：%d", methodReply[0])
	}
	switch methodReply[1] {
	case 0x00:
		// No auth.
	case 0x02:
		if !spec.HasAuth() {
			return errors.New("SOCKS5 代理要求用户名密码认证，但未配置代理凭证")
		}
		uBytes := []byte(spec.Username)
		pBytes := []byte(spec.Password)
		if len(uBytes) > 255 || len(pBytes) > 255 {
			return errors.New("SOCKS5 代理用户名或密码长度不能超过 255 字节")
		}
		authReq := make([]byte, 0, 3+len(uBytes)+len(pBytes))
		authReq = append(authReq, 0x01, byte(len(uBytes)))
		authReq = append(authReq, uBytes...)
		authReq = append(authReq, byte(len(pBytes)))
		authReq = append(authReq, pBytes...)
		if _, err := conn.Write(authReq); err != nil {
			return err
		}
		var authReply [2]byte
		if _, err := io.ReadFull(conn, authReply[:]); err != nil {
			return err
		}
		if authReply[0] != 0x01 || authReply[1] != 0x00 {
			return errors.New("SOCKS5 代理认证失败")
		}
	default:
		return errors.New("SOCKS5 代理拒绝认证方式")
	}

	cleanHost := strings.Trim(strings.TrimSpace(targetHost), "[]")
	var req []byte
	if addr, err := netip.ParseAddr(cleanHost); err == nil && addr.Zone() == "" {
		if addr.Is4() {
			b := addr.As4()
			req = make([]byte, 3+1+4+2)
			req[0], req[1], req[2], req[3] = 0x05, 0x01, 0x00, 0x01
			copy(req[4:8], b[:])
		} else {
			b := addr.As16()
			req = make([]byte, 3+1+16+2)
			req[0], req[1], req[2], req[3] = 0x05, 0x01, 0x00, 0x04
			copy(req[4:20], b[:])
		}
	} else {
		if len(cleanHost) == 0 || len(cleanHost) > 255 {
			return fmt.Errorf("无效的目标主机名：%s", cleanHost)
		}
		req = make([]byte, 3+1+1+len(cleanHost)+2)
		req[0], req[1], req[2], req[3] = 0x05, 0x01, 0x00, 0x03
		req[4] = byte(len(cleanHost))
		copy(req[5:5+len(cleanHost)], cleanHost)
	}
	binary.BigEndian.PutUint16(req[len(req)-2:], uint16(targetPort))
	if _, err := conn.Write(req); err != nil {
		return err
	}

	var repHeader [4]byte
	if _, err := io.ReadFull(conn, repHeader[:]); err != nil {
		return err
	}
	if repHeader[0] != 0x05 {
		return fmt.Errorf("非法的 SOCKS5 连接响应版本：%d", repHeader[0])
	}
	if repHeader[1] != 0x00 {
		return fmt.Errorf("SOCKS5 代理拒绝连接 (code %d)", repHeader[1])
	}

	switch repHeader[3] {
	case 0x01:
		var drain [6]byte
		_, err := io.ReadFull(conn, drain[:])
		return err
	case 0x04:
		var drain [18]byte
		_, err := io.ReadFull(conn, drain[:])
		return err
	case 0x03:
		var lenBuf [1]byte
		if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
			return err
		}
		drain := make([]byte, int(lenBuf[0])+2)
		_, err := io.ReadFull(conn, drain)
		return err
	default:
		return fmt.Errorf("非法的 SOCKS5 地址类型：%d", repHeader[3])
	}
}
