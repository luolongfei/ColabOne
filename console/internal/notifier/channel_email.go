package notifier

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type colabOneEmailChannel struct {
	tlsConfig *tls.Config
}

func newColabOneEmailChannel() *colabOneEmailChannel {
	return &colabOneEmailChannel{}
}

func (c *colabOneEmailChannel) ID() string {
	return ChannelEmail
}

func (c *colabOneEmailChannel) IsRemote() bool {
	return true
}

func (c *colabOneEmailChannel) Send(
	ctx context.Context,
	cfg ColabOneNotificationConfig,
	event ColabOneNotificationEvent,
) error {
	if !cfg.EmailConfigured() {
		return errors.New("请先完善邮件 SMTP 服务器、发件人账号与收件人邮箱")
	}

	host, hostErr := colabOneNormalizeSMTPHost(cfg.Email.SMTPHost)
	if hostErr != nil || host == "" {
		if hostErr != nil {
			return hostErr
		}
		return errors.New("请先完善邮件 SMTP 服务器、发件人账号与收件人邮箱")
	}
	port := cfg.Email.SMTPPort
	security := strings.ToLower(strings.TrimSpace(cfg.Email.Security))
	fromAddr := colabOneEffectiveEmailFrom(cfg.Email)
	fromName := strings.TrimSpace(cfg.Email.FromName)
	recipients, err := colabOneParseEmailRecipients(cfg.Email.ToAddresses)
	if err != nil || len(recipients) == 0 {
		return errors.New("请先完善邮件 SMTP 服务器、发件人账号与收件人邮箱")
	}

	if err := colabOneRejectControlChars(fromName, "发件人名称"); err != nil {
		return err
	}
	if err := colabOneRejectControlChars(fromAddr, "发件人邮箱"); err != nil {
		return err
	}

	// Prepare optional inline JPEG preview image before rendering HTML so
	// cid:colabone-preview is only referenced when the MIME image part is attached.
	renderEvent := event
	var inlineJPEG []byte
	if cfg.Email.EmbedPreviewImage && len(event.ImageBytes) > 0 {
		if imgData, _, _, _, _, prepErr := PreparePreviewImage(event.ImageBytes, 1200, 3<<20, false); prepErr == nil && len(imgData) > 0 {
			inlineJPEG = imgData
		} else {
			renderEvent.ImageBytes = nil
		}
	} else {
		renderEvent.ImageBytes = nil
	}

	rawMIME, err := colabOneBuildEmailMIME(fromName, fromAddr, recipients, renderEvent, inlineJPEG)
	if err != nil {
		return errors.New(RedactSecrets(err.Error(), cfg))
	}

	if err := c.sendSMTP(ctx, host, port, security, cfg.Email.Username, cfg.Email.Password, fromAddr, recipients, rawMIME); err != nil {
		return errors.New(RedactSecrets(err.Error(), cfg))
	}
	return nil
}

func (c *colabOneEmailChannel) sendSMTP(
	ctx context.Context,
	host string,
	port int,
	security string,
	username string,
	password string,
	fromAddr string,
	recipients []string,
	rawMIME []byte,
) error {
	canFallbackLogin, err := c.sendSMTPOnce(ctx, host, port, security, username, password, fromAddr, recipients, rawMIME, false)
	if err == nil {
		return nil
	}
	// Go's net/smtp.Client.Auth() sends QUIT and closes the session on auth failure,
	// so falling back from AUTH PLAIN to AUTH LOGIN uses a fresh SMTP connection.
	if canFallbackLogin && (ctx == nil || ctx.Err() == nil) {
		_, loginErr := c.sendSMTPOnce(ctx, host, port, security, username, password, fromAddr, recipients, rawMIME, true)
		return loginErr
	}
	return err
}

func (c *colabOneEmailChannel) sendSMTPOnce(
	ctx context.Context,
	host string,
	port int,
	security string,
	username string,
	password string,
	fromAddr string,
	recipients []string,
	rawMIME []byte,
	forceLoginAuth bool,
) (canFallbackLogin bool, err error) {
	if ctx == nil {
		ctx = context.Background()
	}

	deadline := time.Now().Add(20 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	dialTimeout := time.Until(deadline)
	if dialTimeout <= 0 {
		return false, context.DeadlineExceeded
	}
	if dialTimeout > 15*time.Second {
		dialTimeout = 15 * time.Second
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	tlsCfg := c.effectiveTLSConfig(host)

	var conn net.Conn
	netDialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	switch security {
	case "tls":
		tlsDialer := &tls.Dialer{NetDialer: netDialer, Config: tlsCfg}
		conn, err = tlsDialer.DialContext(ctx, "tcp", addr)
	case "starttls", "none":
		conn, err = netDialer.DialContext(ctx, "tcp", addr)
	default:
		return false, errors.New("SMTP 加密方式仅支持 tls、starttls 或 none")
	}
	if err != nil {
		return false, fmt.Errorf("连接 SMTP 服务器失败：%w", err)
	}

	_ = conn.SetDeadline(deadline)
	stopWatch := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
	})
	defer func() {
		stopWatch()
		_ = conn.Close()
	}()

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return false, fmt.Errorf("初始化 SMTP 会话失败：%w", err)
	}
	defer func() {
		_ = client.Close()
	}()

	if err := client.Hello("colabone.local"); err != nil {
		return false, fmt.Errorf("SMTP EHLO 握手失败：%w", err)
	}

	if security == "starttls" {
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			return false, errors.New("SMTP 服务器不支持 STARTTLS 加密")
		}
		if err := client.StartTLS(tlsCfg); err != nil {
			return false, fmt.Errorf("SMTP STARTTLS 加密握手失败：%w", err)
		}
	}

	if strings.TrimSpace(username) != "" || password != "" {
		fallback, authErr := colabOneAuthenticateSMTP(client, username, password, security == "none", forceLoginAuth)
		if authErr != nil {
			return fallback, fmt.Errorf("SMTP 认证失败：%w", authErr)
		}
	}

	if err := client.Mail(fromAddr); err != nil {
		return false, fmt.Errorf("SMTP 发件人地址被拒绝：%w", err)
	}
	for _, rcpt := range recipients {
		if err := client.Rcpt(rcpt); err != nil {
			return false, fmt.Errorf("SMTP 收件人地址被拒绝 (%s)：%w", rcpt, err)
		}
	}

	wc, err := client.Data()
	if err != nil {
		return false, fmt.Errorf("SMTP DATA 命令失败：%w", err)
	}
	if _, err := wc.Write(rawMIME); err != nil {
		_ = wc.Close()
		return false, fmt.Errorf("写入邮件正文失败：%w", err)
	}
	if err := wc.Close(); err != nil {
		return false, fmt.Errorf("提交邮件正文失败：%w", err)
	}

	// Email is already accepted (250 OK after DATA); treat QUIT as best-effort so
	// immediate TCP RST/EOF from strict servers never turns a delivered email into an error.
	_ = client.Quit()
	return false, nil
}

func (c *colabOneEmailChannel) effectiveTLSConfig(host string) *tls.Config {
	var cfg *tls.Config
	if c != nil && c.tlsConfig != nil {
		cfg = c.tlsConfig.Clone()
	} else {
		cfg = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}
	if cfg.ServerName == "" {
		cfg.ServerName = strings.TrimRight(host, ".")
	}
	return cfg
}

func colabOneIsSMTPAuthMechanismError(err error) bool {
	if err == nil {
		return false
	}
	var protoErr *textproto.Error
	if errors.As(err, &protoErr) {
		return protoErr.Code == 504 || protoErr.Code == 502 || protoErr.Code == 500
	}
	return false
}

func colabOneAuthenticateSMTP(
	client *smtp.Client,
	username string,
	password string,
	allowPlaintext bool,
	forceLoginAuth bool,
) (canFallbackLogin bool, err error) {
	hasAuth, mechStr := client.Extension("AUTH")
	if !hasAuth {
		// Legacy servers (e.g. older Exchange) may advertise "AUTH=PLAIN LOGIN" or "AUTH=LOGIN".
		if ok, rest := client.Extension("AUTH=PLAIN"); ok {
			hasAuth = true
			mechStr = "PLAIN " + rest
		} else if ok, rest := client.Extension("AUTH=LOGIN"); ok {
			hasAuth = true
			mechStr = "LOGIN " + rest
		}
	}
	if !hasAuth {
		return false, errors.New("SMTP 服务器未通告 AUTH 认证能力，请确认是否需要启用 TLS 或 STARTTLS 加密")
	}
	upperMechs := " " + strings.ToUpper(strings.ReplaceAll(mechStr, "=", " ")) + " "
	supportsPlain := strings.Contains(upperMechs, " PLAIN ")
	supportsLogin := strings.Contains(upperMechs, " LOGIN ")

	if forceLoginAuth && supportsLogin {
		return false, client.Auth(&colabOneLoginAuth{
			username:       username,
			password:       password,
			allowPlaintext: allowPlaintext,
		})
	}

	if supportsPlain {
		err := client.Auth(&colabOnePlainAuth{
			username:       username,
			password:       password,
			allowPlaintext: allowPlaintext,
		})
		if err == nil {
			return false, nil
		}
		// Only fall back to AUTH LOGIN on mechanism errors (504/502/500), never on 535 bad credentials.
		return supportsLogin && colabOneIsSMTPAuthMechanismError(err), err
	}

	if supportsLogin {
		return false, client.Auth(&colabOneLoginAuth{
			username:       username,
			password:       password,
			allowPlaintext: allowPlaintext,
		})
	}

	return false, fmt.Errorf("SMTP 服务器不支持 PLAIN 或 LOGIN 认证（当前通告：%s）", strings.TrimSpace(mechStr))
}

type colabOnePlainAuth struct {
	username       string
	password       string
	allowPlaintext bool
}

func (a *colabOnePlainAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && !a.allowPlaintext && !colabOneIsLoopbackHost(server.Name) {
		return "", nil, errors.New("未加密连接禁止发送 SMTP 明文凭证")
	}
	resp := []byte("\x00" + a.username + "\x00" + a.password)
	return "PLAIN", resp, nil
}

func (a *colabOnePlainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("unexpected SMTP PLAIN challenge")
	}
	return nil, nil
}

type colabOneLoginAuth struct {
	username       string
	password       string
	allowPlaintext bool
	step           int
}

func (a *colabOneLoginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && !a.allowPlaintext && !colabOneIsLoopbackHost(server.Name) {
		return "", nil, errors.New("未加密连接禁止发送 SMTP 登录凭证")
	}
	a.step = 0
	return "LOGIN", nil, nil
}

func (a *colabOneLoginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	prompt := strings.ToLower(strings.TrimSpace(string(fromServer)))
	switch {
	case strings.Contains(prompt, "username") || strings.Contains(prompt, "user"):
		a.step = 1
		return []byte(a.username), nil
	case strings.Contains(prompt, "password") || strings.Contains(prompt, "pass"):
		a.step = 2
		return []byte(a.password), nil
	default:
		a.step++
		if a.step == 1 {
			return []byte(a.username), nil
		}
		if a.step == 2 {
			return []byte(a.password), nil
		}
		return nil, errors.New("unexpected SMTP LOGIN challenge")
	}
}

func colabOneIsLoopbackHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(strings.Trim(host, "[]")))
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

func colabOneBuildEmailMIME(
	fromName string,
	fromAddr string,
	recipients []string,
	event ColabOneNotificationEvent,
	inlineJPEG []byte,
) ([]byte, error) {
	locale := NormalizeLocale(event.Locale)
	msg := colabOneMessagesForLocale(locale)
	statusLabel := colabOneSeverityLabel(event.Severity, locale)

	title := strings.TrimSpace(event.Title)
	if title == "" {
		title = "ColabOne"
	}
	subject := fmt.Sprintf("[%s · %s] %s", msg.EmailSubjectPrefix, statusLabel, title)
	subject = strings.NewReplacer("\r", " ", "\n", " ").Replace(subject)

	if strings.ContainsAny(fromAddr, "\r\n") || strings.ContainsAny(fromName, "\r\n") {
		return nil, errors.New("邮件头不能包含换行符")
	}
	for _, r := range recipients {
		if strings.ContainsAny(r, "\r\n") {
			return nil, errors.New("收件人邮件头不能包含换行符")
		}
	}

	occurred := event.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now()
	}

	fromHeader := (&mail.Address{Name: fromName, Address: fromAddr}).String()
	toHeader := strings.Join(recipients, ",\r\n ")
	subjectHeader := colabOneFormatSubjectHeader(subject)

	fromDomain := "colabone.local"
	if at := strings.LastIndexByte(fromAddr, '@'); at >= 0 && at+1 < len(fromAddr) {
		fromDomain = fromAddr[at+1:]
	}
	msgID := fmt.Sprintf("<colabone-%d-%s@%s>", occurred.UnixNano(), colabOneRandomHex(6), fromDomain)

	plainBody := RenderEmailPlainText(event)
	htmlBody := RenderEmailHTML(event)

	altBoundary := "co-alt-" + colabOneRandomHex(12)
	relBoundary := "co-rel-" + colabOneRandomHex(12)

	var buf bytes.Buffer
	buf.Grow(len(htmlBody) + len(plainBody) + len(inlineJPEG)*2 + 1024)

	writeHeader := func(k, v string) {
		buf.WriteString(k)
		buf.WriteString(": ")
		buf.WriteString(v)
		buf.WriteString("\r\n")
	}

	writeHeader("From", fromHeader)
	writeHeader("To", toHeader)
	writeHeader("Subject", subjectHeader)
	writeHeader("Date", occurred.Format(time.RFC1123Z))
	writeHeader("Message-ID", msgID)
	writeHeader("MIME-Version", "1.0")
	writeHeader("X-Mailer", "ColabOne Notifier")

	writeAlternative := func() {
		// text/plain part
		buf.WriteString("--" + altBoundary + "\r\n")
		buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
		buf.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		colabOneWriteBase64Lines(&buf, []byte(plainBody))

		// text/html part
		buf.WriteString("--" + altBoundary + "\r\n")
		buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
		buf.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		colabOneWriteBase64Lines(&buf, []byte(htmlBody))

		buf.WriteString("--" + altBoundary + "--\r\n")
	}

	if len(inlineJPEG) > 0 {
		writeHeader("Content-Type", fmt.Sprintf(`multipart/related; type="multipart/alternative"; boundary="%s"`, relBoundary))
		buf.WriteString("\r\n")

		buf.WriteString("--" + relBoundary + "\r\n")
		buf.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", altBoundary))
		writeAlternative()

		buf.WriteString("--" + relBoundary + "\r\n")
		buf.WriteString("Content-Type: image/jpeg; name=\"preview.jpg\"\r\n")
		buf.WriteString("Content-Transfer-Encoding: base64\r\n")
		buf.WriteString("Content-ID: <colabone-preview>\r\n")
		buf.WriteString("Content-Disposition: inline; filename=\"preview.jpg\"\r\n\r\n")
		colabOneWriteBase64Lines(&buf, inlineJPEG)

		buf.WriteString("--" + relBoundary + "--\r\n")
	} else {
		writeHeader("Content-Type", fmt.Sprintf(`multipart/alternative; boundary="%s"`, altBoundary))
		buf.WriteString("\r\n")
		writeAlternative()
	}

	return buf.Bytes(), nil
}

// colabOneFormatSubjectHeader formats and folds Subject to stay RFC 5322-safe
// (<= 78 octets per line), using word folding for printable ASCII and RFC 2047
// UTF-8 B-encoded folded words for non-ASCII or unbreakable long ASCII tokens.
func colabOneFormatSubjectHeader(subject string) string {
	clean := strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ", "\x00", " ").Replace(subject))
	if clean == "" {
		return ""
	}
	const (
		firstLineMax = 78 - len("Subject: ")
		contLineMax  = 78 - 1 // leading single space on folded continuation lines
	)

	isPurePrintableASCII := !strings.Contains(clean, "=?")
	if isPurePrintableASCII {
		for i := 0; i < len(clean); i++ {
			if clean[i] < 0x20 || clean[i] > 0x7e {
				isPurePrintableASCII = false
				break
			}
		}
	}

	if isPurePrintableASCII {
		if len(clean) <= firstLineMax {
			return clean
		}
		words := strings.Split(clean, " ")
		var lines []string
		var cur strings.Builder
		limit := firstLineMax
		canFold := true
		for _, w := range words {
			if w == "" {
				continue
			}
			if len(w) > limit {
				if cur.Len() > 0 && len(w) <= contLineMax {
					lines = append(lines, cur.String())
					cur.Reset()
					limit = contLineMax
					cur.WriteString(w)
					continue
				}
				canFold = false
				break
			}
			if cur.Len() == 0 {
				cur.WriteString(w)
			} else if cur.Len()+1+len(w) <= limit {
				cur.WriteByte(' ')
				cur.WriteString(w)
			} else {
				lines = append(lines, cur.String())
				cur.Reset()
				limit = contLineMax
				cur.WriteString(w)
			}
		}
		if canFold {
			if cur.Len() > 0 {
				lines = append(lines, cur.String())
			}
			if len(lines) > 0 {
				return strings.Join(lines, "\r\n ")
			}
		}
	}

	// RFC 2047 B-encode in UTF-8 rune-safe chunks <= 36 raw bytes
	// (=?utf-8?b? (10) + 48 base64 chars + ?= (2) = 60 octets <= 75, and 9+60=69 <= 78).
	const maxRawChunkBytes = 36
	var encodedWords []string
	start := 0
	for start < len(clean) {
		end := start
		for end < len(clean) {
			_, size := utf8.DecodeRuneInString(clean[end:])
			if size <= 0 {
				size = 1
			}
			if end > start && (end-start)+size > maxRawChunkBytes {
				break
			}
			end += size
		}
		chunk := clean[start:end]
		encodedWords = append(encodedWords, "=?utf-8?b?"+base64.StdEncoding.EncodeToString([]byte(chunk))+"?=")
		start = end
	}
	return strings.Join(encodedWords, "\r\n ")
}

func colabOneWriteBase64Lines(buf *bytes.Buffer, data []byte) {
	encoded := base64.StdEncoding.EncodeToString(data)
	const lineWidth = 76
	for i := 0; i < len(encoded); i += lineWidth {
		end := i + lineWidth
		if end > len(encoded) {
			end = len(encoded)
		}
		buf.WriteString(encoded[i:end])
		buf.WriteString("\r\n")
	}
}

func colabOneRandomHex(numBytes int) string {
	b := make([]byte, numBytes)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
