// Package assignproxy provides an isolated proxy transport exclusively for
// Colab GPU allocation requests (/tun/m/assign).
//
// It supports HTTP, HTTPS, SOCKS5, and SOCKS5h proxies using only the Go
// standard library. TLS certificate verification is always enforced, SOCKS5
// hostnames are always sent as ATYP=DOMAIN (0x03) for remote DNS resolution
// (zero local DNS leaks), and process proxy environment variables are ignored.
package assignproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// DefaultProxyProbeURL is the default unauthenticated probe URL used to verify
	// allocation proxy connectivity and TLS verification without OAuth credentials.
	DefaultProxyProbeURL = "https://colab.research.google.com/robots.txt"

	// AssignProxyConnectTimeout is the bounded TCP/SOCKS5/TLS connect timeout for
	// GPU allocation requests routed through an allocation proxy.
	AssignProxyConnectTimeout = 15 * time.Second

	// AssignProxyReadTimeout is the response-header read timeout for GPU allocation
	// requests routed through an allocation proxy.
	AssignProxyReadTimeout = 60 * time.Second

	// DefaultProxyTestTimeout is the default timeout for TestAssignProxy probes.
	DefaultProxyTestTimeout = 10 * time.Second

	assignOnlyPath               = "/tun/m/assign"
	defaultAllowedScheme         = "https"
	defaultAllowedHost           = "colab.research.google.com"
	maxProxyTestConnDelay        = 8 * time.Second
	maxProxyConnectResponseBytes = 64 << 10
)

// Sentinel error categories returned or wrapped by assignproxy operations.
var (
	ErrProxyAuth       = errors.New("assignproxy: proxy authentication failed")
	ErrProxyTLS        = errors.New("assignproxy: proxy TLS verification failed")
	ErrProxyTimeout    = errors.New("assignproxy: proxy connection timed out")
	ErrProxyConnect    = errors.New("assignproxy: proxy connection failed")
	ErrForbiddenTarget = errors.New("仅 GPU 分配请求允许使用分配代理")
)

var (
	defaultPorts = map[string]int{
		"http":    80,
		"https":   443,
		"socks5":  1080,
		"socks5h": 1080,
	}

	socks5ReplyReasons = map[byte]string{
		0x01: "SOCKS5 代理通用错误",
		0x02: "SOCKS5 代理规则拒绝连接",
		0x03: "SOCKS5 代理网络不可达",
		0x04: "SOCKS5 代理目标主机不可达",
		0x05: "SOCKS5 代理目标连接被拒绝",
		0x06: "SOCKS5 代理连接超时 (TTL expired)",
		0x07: "SOCKS5 代理不支持 CONNECT 命令",
		0x08: "SOCKS5 代理不支持目标地址类型",
	}

	domainLabelRE = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
)

// ProxyError classifies allocation proxy transport failures while preserving
// user-facing Chinese messages and underlying causes.
type ProxyError struct {
	Kind    error
	Message string
	Cause   error
}

func (e *ProxyError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *ProxyError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *ProxyError) Is(target error) bool {
	if e == nil || target == nil {
		return false
	}
	return e.Kind != nil && target == e.Kind
}

func (e *ProxyError) Timeout() bool {
	if e == nil {
		return false
	}
	if e.Kind == ErrProxyTimeout {
		return true
	}
	var ne net.Error
	return errors.As(e.Cause, &ne) && ne.Timeout()
}

func (e *ProxyError) Temporary() bool {
	return false
}

// ProxyErrorType exposes the proxy failure category structurally across packages.
func (e *ProxyError) ProxyErrorType() string {
	if e == nil {
		return ""
	}
	switch e.Kind {
	case ErrProxyAuth:
		return "auth"
	case ErrProxyTLS:
		return "ssl"
	case ErrProxyTimeout:
		return "connect_timeout"
	default:
		return "proxy"
	}
}

// ColabOneAssignProxySpec holds a validated, normalized allocation proxy
// specification along with safe redacted representations for UI and logs.
type ColabOneAssignProxySpec struct {
	Scheme        string
	Host          string
	Port          int
	Username      string
	Password      string
	NormalizedURL string
	MaskedURL     string
	LogLabel      string
}

var (
	_ fmt.Stringer   = ColabOneAssignProxySpec{}
	_ fmt.GoStringer = ColabOneAssignProxySpec{}
)

// IsSOCKS5 reports whether the proxy uses SOCKS5 or SOCKS5h.
func (s ColabOneAssignProxySpec) IsSOCKS5() bool {
	return s.Scheme == "socks5" || s.Scheme == "socks5h"
}

// HasAuth reports whether the proxy includes a username or password.
func (s ColabOneAssignProxySpec) HasAuth() bool {
	return s.Username != "" || s.Password != ""
}

// String returns a credential-free proxy representation for %s, %v, and %+v
// formatting so Username, Password, and NormalizedURL never leak into logs.
func (s ColabOneAssignProxySpec) String() string {
	if s.MaskedURL != "" {
		return s.MaskedURL
	}
	if s.Scheme != "" && s.Host != "" {
		if s.Port > 0 {
			return s.Scheme + "://" + formatHostPort(s.Host, s.Port)
		}
		return s.Scheme + "://" + s.Host
	}
	if s.LogLabel != "" {
		return s.LogLabel
	}
	if s.NormalizedURL != "" {
		if masked := MaskAssignProxy(s.NormalizedURL); masked != "" {
			return masked
		}
		return "[REDACTED]"
	}
	return ""
}

// GoString returns a credential-redacted Go-syntax representation for %#v
// formatting so Username, Password, and NormalizedURL never leak.
func (s ColabOneAssignProxySpec) GoString() string {
	redact := func(v string) string {
		if v == "" {
			return ""
		}
		return "[REDACTED]"
	}
	return fmt.Sprintf(
		"assignproxy.ColabOneAssignProxySpec{Scheme:%q, Host:%q, Port:%d, Username:%q, Password:%q, NormalizedURL:%q, MaskedURL:%q, LogLabel:%q}",
		s.Scheme,
		s.Host,
		s.Port,
		redact(s.Username),
		redact(s.Password),
		redact(s.NormalizedURL),
		s.MaskedURL,
		s.LogLabel,
	)
}

func formatHostPort(host string, port int) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return fmt.Sprintf("[%s]:%d", host, port)
	}
	return fmt.Sprintf("%s:%d", host, port)
}

func isUnreservedRFC3986(b byte) bool {
	return (b >= 'A' && b <= 'Z') ||
		(b >= 'a' && b <= 'z') ||
		(b >= '0' && b <= '9') ||
		b == '-' || b == '.' || b == '_' || b == '~'
}

// rfc3986Quote percent-encodes every byte except RFC 3986 unreserved bytes,
// matching Python's urllib.parse.quote(s, safe="").
func rfc3986Quote(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUnreservedRFC3986(c) {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0x0F])
		}
	}
	return b.String()
}

func hexVal(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	default:
		return 0, false
	}
}

// percentUnquote decodes valid %HH hex pairs and leaves invalid % sequences
// verbatim, matching Python's urllib.parse.unquote.
func percentUnquote(s string) string {
	if !strings.ContainsRune(s, '%') {
		return s
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			hi, ok1 := hexVal(s[i+1])
			lo, ok2 := hexVal(s[i+2])
			if ok1 && ok2 {
				out = append(out, (hi<<4)|lo)
				i += 2
				continue
			}
		}
		out = append(out, s[i])
	}
	return string(out)
}

// Pure-Go RFC 3492 Punycode + RFC 3490 IDNA ASCII label conversion.
const (
	punyBase        = 36
	punyTMin        = 1
	punyTMax        = 26
	punySkew        = 38
	punyDamp        = 700
	punyInitialBias = 72
	punyInitialN    = 128
	punyMaxInt      = 1<<31 - 1
)

func punyAdapt(delta, numPoints int32, firstTime bool) int32 {
	if firstTime {
		delta /= punyDamp
	} else {
		delta /= 2
	}
	delta += delta / numPoints
	var k int32
	for delta > ((punyBase-punyTMin)*punyTMax)/2 {
		delta /= punyBase - punyTMin
		k += punyBase
	}
	return k + ((punyBase-punyTMin+1)*delta)/(delta+punySkew)
}

func punyEncodeDigit(d int32) byte {
	if d < 26 {
		return byte('a' + d)
	}
	return byte('0' + (d - 26))
}

func punyEncodeLabel(label string) (string, error) {
	runes := []rune(label)
	var out strings.Builder
	var basicCount int32
	for _, r := range runes {
		if r < 0x80 {
			out.WriteByte(byte(r))
			basicCount++
		}
	}
	h := basicCount
	b := basicCount
	if b > 0 {
		out.WriteByte('-')
	}
	var n int32 = punyInitialN
	var delta int32
	var bias int32 = punyInitialBias
	total := int32(len(runes))

	for h < total {
		var m int32 = punyMaxInt
		for _, r := range runes {
			if r >= n && r < m {
				m = r
			}
		}
		if m == punyMaxInt || (m-n) > (punyMaxInt-delta)/(h+1) {
			return "", errors.New("punycode overflow")
		}
		delta += (m - n) * (h + 1)
		n = m
		for _, r := range runes {
			if r < n {
				if delta == punyMaxInt {
					return "", errors.New("punycode overflow")
				}
				delta++
			}
			if r == n {
				q := delta
				for k := int32(punyBase); ; k += punyBase {
					t := k - bias
					if t < punyTMin {
						t = punyTMin
					} else if t > punyTMax {
						t = punyTMax
					}
					if q < t {
						break
					}
					baseMinusT := int32(punyBase) - t
					out.WriteByte(punyEncodeDigit(t + (q-t)%baseMinusT))
					q = (q - t) / baseMinusT
				}
				out.WriteByte(punyEncodeDigit(q))
				bias = punyAdapt(delta, h+1, h == b)
				delta = 0
				h++
			}
		}
		delta++
		n++
	}
	return out.String(), nil
}

func isIDNADot(r rune) bool {
	return r == '.' || r == '\u3002' || r == '\uff0e' || r == '\uff61'
}

func splitIDNALabels(host string) []string {
	var labels []string
	start := 0
	for i, r := range host {
		if isIDNADot(r) {
			labels = append(labels, host[start:i])
			start = i + utf8.RuneLen(r)
		}
	}
	labels = append(labels, host[start:])
	return labels
}

func isAllASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func toIDNAASCII(host string) (string, error) {
	if host == "" || !utf8.ValidString(host) {
		return "", errors.New("invalid idna host")
	}
	labels := splitIDNALabels(host)
	asciiLabels := make([]string, len(labels))
	for i, label := range labels {
		if label == "" {
			return "", errors.New("empty idna label")
		}
		var out string
		if isAllASCII(label) {
			out = label
		} else {
			lowered := strings.ToLower(label)
			if strings.HasPrefix(lowered, "xn--") {
				return "", errors.New("invalid ace prefix on non-ascii label")
			}
			encoded, err := punyEncodeLabel(lowered)
			if err != nil {
				return "", err
			}
			out = "xn--" + encoded
		}
		if len(out) == 0 || len(out) > 63 {
			return "", errors.New("idna label length out of range")
		}
		asciiLabels[i] = out
	}
	return strings.Join(asciiLabels, "."), nil
}

func validateProxyHost(host string) (string, error) {
	clean := strings.TrimSpace(host)
	if clean == "" || strings.ContainsRune(clean, '%') {
		return "", errors.New("代理地址缺少主机名或 IP")
	}
	if addr, err := netip.ParseAddr(clean); err == nil && addr.Zone() == "" {
		return addr.String(), nil
	}
	trimmed := strings.TrimRight(clean, ".")
	if trimmed == "" {
		return "", errors.New("代理地址缺少主机名或 IP")
	}
	asciiHost, err := toIDNAASCII(trimmed)
	if err != nil || asciiHost == "" || len(asciiHost) > 253 {
		return "", errors.New("代理地址缺少主机名或 IP")
	}
	for _, label := range strings.Split(asciiHost, ".") {
		if !domainLabelRE.MatchString(label) {
			return "", errors.New("代理地址缺少主机名或 IP")
		}
	}
	return strings.ToLower(asciiHost), nil
}

type splitURLParts struct {
	scheme      string
	rawHost     string
	rawPort     string
	hasPort     bool
	rawUser     string
	rawPass     string
	hasUser     bool
	hasPass     bool
	path        string
	query       string
	fragment    string
	hostInvalid bool
}

// splitProxyURL splits scheme://authority[path][?query][#fragment] following
// RFC 3986 / Python urllib.parse.urlsplit semantics so raw '@' in passwords,
// IPv6 literals, and malformed ports produce deterministic Chinese errors.
func splitProxyURL(text string) splitURLParts {
	var p splitURLParts
	idx := strings.Index(text, "://")
	p.scheme = strings.ToLower(text[:idx])
	rest := text[idx+3:]

	// Separate authority from path/query/fragment.
	authEnd := strings.IndexAny(rest, "/?#")
	var authority, tail string
	if authEnd >= 0 {
		authority = rest[:authEnd]
		tail = rest[authEnd:]
	} else {
		authority = rest
	}

	// Separate fragment, then query, then path.
	if hashIdx := strings.IndexByte(tail, '#'); hashIdx >= 0 {
		p.fragment = tail[hashIdx+1:]
		tail = tail[:hashIdx]
	}
	if qIdx := strings.IndexByte(tail, '?'); qIdx >= 0 {
		p.query = tail[qIdx+1:]
		tail = tail[:qIdx]
	}
	p.path = tail

	// Split userinfo from host[:port] at the LAST '@'.
	hostport := authority
	if atIdx := strings.LastIndexByte(authority, '@'); atIdx >= 0 {
		userinfo := authority[:atIdx]
		hostport = authority[atIdx+1:]
		p.hasUser = true
		if colonIdx := strings.IndexByte(userinfo, ':'); colonIdx >= 0 {
			p.rawUser = userinfo[:colonIdx]
			p.rawPass = userinfo[colonIdx+1:]
			p.hasPass = true
		} else {
			p.rawUser = userinfo
		}
	}

	// Split host[:port].
	if strings.HasPrefix(hostport, "[") {
		endBracket := strings.IndexByte(hostport, ']')
		if endBracket < 0 {
			p.hostInvalid = true
			return p
		}
		p.rawHost = hostport[1:endBracket]
		after := hostport[endBracket+1:]
		if after != "" {
			if strings.HasPrefix(after, ":") {
				p.rawPort = after[1:]
				p.hasPort = true
			} else {
				p.hostInvalid = true
			}
		}
		return p
	}

	if strings.ContainsAny(hostport, "[]") {
		p.hostInvalid = true
		return p
	}
	if colonIdx := strings.IndexByte(hostport, ':'); colonIdx >= 0 {
		p.rawHost = hostport[:colonIdx]
		p.rawPort = hostport[colonIdx+1:]
		p.hasPort = true
	} else {
		p.rawHost = hostport
	}
	return p
}

func parseStrictPort(rawPort string) (int, error) {
	if rawPort == "" {
		return 0, nil
	}
	for i := 0; i < len(rawPort); i++ {
		if rawPort[i] < '0' || rawPort[i] > '9' {
			return 0, errors.New("代理端口必须是 1 至 65535 之间的整数")
		}
	}
	val, err := strconv.Atoi(rawPort)
	if err != nil || val < 1 || val > 65535 {
		return 0, errors.New("代理端口必须是 1 至 65535 之间的整数")
	}
	return val, nil
}

// ParseAssignProxy validates and parses an allocation-only proxy URL.
//
// Returns (nil, nil) when raw is empty or whitespace-only. Returns an error
// with a user-facing Chinese validation message when invalid.
func ParseAssignProxy(raw string) (*ColabOneAssignProxySpec, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil, nil
	}
	if !utf8.ValidString(text) {
		return nil, errors.New("代理地址不能包含空格或控制字符")
	}
	for _, r := range text {
		if unicode.IsSpace(r) || r < 32 || r == 127 {
			return nil, errors.New("代理地址不能包含空格或控制字符")
		}
	}
	if !strings.Contains(text, "://") {
		return nil, errors.New("代理地址请包含协议前缀，例如 http://127.0.0.1:7890 或 socks5://127.0.0.1:1080")
	}

	parts := splitProxyURL(text)
	defaultPort, ok := defaultPorts[parts.scheme]
	if !ok {
		return nil, errors.New("仅支持 http://、https://、socks5:// 或 socks5h:// 代理协议")
	}
	if parts.hostInvalid {
		return nil, errors.New("代理地址缺少主机名或 IP")
	}

	host, err := validateProxyHost(parts.rawHost)
	if err != nil {
		return nil, err
	}

	port := defaultPort
	if parts.hasPort && parts.rawPort != "" {
		parsedPort, portErr := parseStrictPort(parts.rawPort)
		if portErr != nil {
			return nil, portErr
		}
		port = parsedPort
	}
	if port < 1 || port > 65535 {
		return nil, errors.New("代理端口必须是 1 至 65535 之间的整数")
	}

	if (parts.path != "" && parts.path != "/") || parts.query != "" || parts.fragment != "" {
		return nil, errors.New("代理地址不应包含路径或查询参数")
	}

	var username, password string
	if parts.hasUser {
		username = percentUnquote(parts.rawUser)
	}
	if parts.hasPass {
		password = percentUnquote(parts.rawPass)
	}
	if parts.scheme == "socks5" || parts.scheme == "socks5h" {
		if len(username) > 255 || len(password) > 255 {
			return nil, errors.New("SOCKS5 代理用户名或密码长度不能超过 255 字节")
		}
	}

	hostPort := formatHostPort(host, port)
	var normalizedNetloc string
	if username != "" || password != "" {
		encUser := rfc3986Quote(username)
		if parts.hasPass {
			encPass := rfc3986Quote(password)
			normalizedNetloc = encUser + ":" + encPass + "@" + hostPort
		} else {
			normalizedNetloc = encUser + "@" + hostPort
		}
	} else {
		normalizedNetloc = hostPort
	}

	normalizedURL := parts.scheme + "://" + normalizedNetloc
	maskedURL := parts.scheme + "://" + hostPort
	schemeTag := strings.ToUpper(parts.scheme)
	if parts.scheme == "socks5" || parts.scheme == "socks5h" {
		schemeTag = "SOCKS5"
	}
	logLabel := schemeTag + " " + hostPort

	return &ColabOneAssignProxySpec{
		Scheme:        parts.scheme,
		Host:          host,
		Port:          port,
		Username:      username,
		Password:      password,
		NormalizedURL: normalizedURL,
		MaskedURL:     maskedURL,
		LogLabel:      logLabel,
	}, nil
}

// NormalizeAssignProxy validates and returns the canonical RFC 3986 URL for an
// allocation proxy, or ("", nil) when raw is empty/whitespace.
func NormalizeAssignProxy(raw string) (string, error) {
	spec, err := ParseAssignProxy(raw)
	if err != nil {
		return "", err
	}
	if spec == nil {
		return "", nil
	}
	return spec.NormalizedURL, nil
}

// MaskAssignProxy returns the credential-free proxy URL (e.g.
// "socks5://127.0.0.1:1080"), or "" if raw is empty or invalid.
func MaskAssignProxy(raw string) string {
	spec, err := ParseAssignProxy(raw)
	if err != nil || spec == nil {
		return ""
	}
	return spec.MaskedURL
}

// ProxyLogLabel returns a safe human-readable proxy label for logs (e.g.
// "SOCKS5 127.0.0.1:1080"), or "" if raw is empty or invalid.
func ProxyLogLabel(raw string) string {
	spec, err := ParseAssignProxy(raw)
	if err != nil || spec == nil {
		return ""
	}
	return spec.LogLabel
}

func recvExact(conn net.Conn, buf []byte) error {
	_, err := io.ReadFull(conn, buf)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return errors.New("代理连接在握手完成前中断")
		}
		return err
	}
	return nil
}

func encodeSOCKS5Target(host string, port int) ([]byte, error) {
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("代理端口必须是 1 至 65535 之间的整数")
	}
	cleanHost := strings.TrimSpace(host)
	if strings.HasPrefix(cleanHost, "[") && strings.HasSuffix(cleanHost, "]") {
		cleanHost = cleanHost[1 : len(cleanHost)-1]
	}

	var payload []byte
	if addr, err := netip.ParseAddr(cleanHost); err == nil && addr.Zone() == "" {
		if addr.Is4() {
			b := addr.As4()
			payload = make([]byte, 1+4+2)
			payload[0] = 0x01
			copy(payload[1:5], b[:])
		} else {
			b := addr.As16()
			payload = make([]byte, 1+16+2)
			payload[0] = 0x04
			copy(payload[1:17], b[:])
		}
	} else {
		trimmed := strings.TrimRight(cleanHost, ".")
		hostASCII, idnaErr := toIDNAASCII(trimmed)
		if idnaErr != nil || len(hostASCII) == 0 || len(hostASCII) > 255 {
			return nil, fmt.Errorf("无效的代理目标主机名：%s", cleanHost)
		}
		payload = make([]byte, 1+1+len(hostASCII)+2)
		payload[0] = 0x03
		payload[1] = byte(len(hostASCII))
		copy(payload[2:2+len(hostASCII)], hostASCII)
	}
	binary.BigEndian.PutUint16(payload[len(payload)-2:], uint16(port))
	return payload, nil
}

func isTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrProxyTimeout) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func socks5Handshake(
	conn net.Conn,
	spec ColabOneAssignProxySpec,
	targetHost string,
	targetPort int,
) error {
	// 1) RFC 1928 method negotiation.
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
	if err := recvExact(conn, methodReply[:]); err != nil {
		return err
	}
	ver, method := methodReply[0], methodReply[1]
	if ver != 0x05 {
		return fmt.Errorf("非法的 SOCKS5 协议版本响应：%d", ver)
	}
	if method == 0xFF {
		return &ProxyError{
			Kind:    ErrProxyAuth,
			Message: "SOCKS5 代理拒绝认证方式或需要用户名与密码",
		}
	}
	switch method {
	case 0x02:
		if !spec.HasAuth() {
			return &ProxyError{
				Kind:    ErrProxyAuth,
				Message: "SOCKS5 代理要求用户名与密码认证",
			}
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
		if err := recvExact(conn, authReply[:]); err != nil {
			return err
		}
		if authReply[0] != 0x01 || authReply[1] != 0x00 {
			return &ProxyError{
				Kind:    ErrProxyAuth,
				Message: "SOCKS5 代理用户名或密码认证失败",
			}
		}
	case 0x00:
		// No authentication required.
	default:
		return fmt.Errorf("不支持的 SOCKS5 认证方式：%d", method)
	}

	// 2) RFC 1928 CONNECT request (always ATYP=DOMAIN for hostnames -> remote DNS).
	targetPayload, err := encodeSOCKS5Target(targetHost, targetPort)
	if err != nil {
		return err
	}
	connectReq := make([]byte, 3+len(targetPayload))
	connectReq[0] = 0x05
	connectReq[1] = 0x01
	connectReq[2] = 0x00
	copy(connectReq[3:], targetPayload)
	if _, err := conn.Write(connectReq); err != nil {
		return err
	}

	var repHeader [4]byte
	if err := recvExact(conn, repHeader[:]); err != nil {
		return err
	}
	repVer, repCode, atyp := repHeader[0], repHeader[1], repHeader[3]
	if repVer != 0x05 {
		return fmt.Errorf("非法的 SOCKS5 连接响应版本：%d", repVer)
	}
	// Evaluate reply status before draining BND.ADDR/BND.PORT so proxies that
	// close immediately after a 4-byte failure header report the exact reason.
	if repCode != 0x00 {
		reason, exists := socks5ReplyReasons[repCode]
		if !exists {
			reason = fmt.Sprintf("SOCKS5 代理拒绝连接 (代码 %d)", repCode)
		}
		return errors.New(reason)
	}

	switch atyp {
	case 0x01:
		var drain [4 + 2]byte
		if err := recvExact(conn, drain[:]); err != nil {
			return err
		}
	case 0x04:
		var drain [16 + 2]byte
		if err := recvExact(conn, drain[:]); err != nil {
			return err
		}
	case 0x03:
		var lenBuf [1]byte
		if err := recvExact(conn, lenBuf[:]); err != nil {
			return err
		}
		drain := make([]byte, int(lenBuf[0])+2)
		if err := recvExact(conn, drain); err != nil {
			return err
		}
	default:
		return fmt.Errorf("非法的 SOCKS5 绑定地址类型：%d", atyp)
	}

	return nil
}

func dialSOCKS5(
	ctx context.Context,
	spec ColabOneAssignProxySpec,
	targetHost string,
	targetPort int,
	connectTimeout time.Duration,
) (net.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if connectTimeout <= 0 || connectTimeout > AssignProxyConnectTimeout {
		connectTimeout = AssignProxyConnectTimeout
	}

	handshakeDeadline := time.Now().Add(connectTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(handshakeDeadline) {
		handshakeDeadline = ctxDeadline
	}

	dialCtx, dialCancel := context.WithDeadline(ctx, handshakeDeadline)
	defer dialCancel()

	proxyAddr := net.JoinHostPort(spec.Host, strconv.Itoa(spec.Port))
	dialer := &net.Dialer{
		Timeout:   connectTimeout,
		KeepAlive: 30 * time.Second,
	}
	conn, err := dialer.DialContext(dialCtx, "tcp", proxyAddr)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, ctx.Err()
		}
		if isTimeoutErr(err) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, &ProxyError{
				Kind:    ErrProxyTimeout,
				Message: "连接 SOCKS5 代理超时",
				Cause:   err,
			}
		}
		return nil, &ProxyError{
			Kind:    ErrProxyConnect,
			Message: fmt.Sprintf("SOCKS5 代理连接失败：%v", err),
			Cause:   err,
		}
	}

	wrapHandshakeErr := func(inner error) error {
		_ = conn.Close()
		if errors.Is(ctx.Err(), context.Canceled) {
			return ctx.Err()
		}
		var pe *ProxyError
		if errors.As(inner, &pe) {
			return pe
		}
		if isTimeoutErr(inner) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &ProxyError{
				Kind:    ErrProxyTimeout,
				Message: "连接 SOCKS5 代理超时",
				Cause:   inner,
			}
		}
		return &ProxyError{
			Kind:    ErrProxyConnect,
			Message: fmt.Sprintf("SOCKS5 代理连接失败：%s", inner.Error()),
			Cause:   inner,
		}
	}

	if err := conn.SetDeadline(handshakeDeadline); err != nil {
		return nil, wrapHandshakeErr(err)
	}
	stopWatch := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
	})

	if err := socks5Handshake(conn, spec, targetHost, targetPort); err != nil {
		stopWatch()
		return nil, wrapHandshakeErr(err)
	}

	if !stopWatch() {
		// Context finished concurrently while the handshake completed.
		_ = conn.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			if errors.Is(ctxErr, context.Canceled) {
				return nil, ctxErr
			}
			return nil, &ProxyError{
				Kind:    ErrProxyTimeout,
				Message: "连接 SOCKS5 代理超时",
				Cause:   ctxErr,
			}
		}
		return nil, &ProxyError{
			Kind:    ErrProxyConnect,
			Message: "SOCKS5 代理连接失败：连接已中断",
		}
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, wrapHandshakeErr(err)
	}
	return conn, nil
}

func dialTLSOverSOCKS5(
	ctx context.Context,
	spec ColabOneAssignProxySpec,
	targetAddr string,
	connectTimeout time.Duration,
	baseTLSConfig *tls.Config,
) (net.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if connectTimeout <= 0 || connectTimeout > AssignProxyConnectTimeout {
		connectTimeout = AssignProxyConnectTimeout
	}

	targetHost, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		return nil, &ProxyError{
			Kind:    ErrProxyConnect,
			Message: fmt.Sprintf("SOCKS5 代理连接失败：无效的目标地址 %s", targetAddr),
			Cause:   err,
		}
	}
	targetPort, err := strconv.Atoi(portStr)
	if err != nil || targetPort < 1 || targetPort > 65535 {
		return nil, &ProxyError{
			Kind:    ErrProxyConnect,
			Message: fmt.Sprintf("SOCKS5 代理连接失败：无效的目标端口 %s", portStr),
			Cause:   err,
		}
	}

	handshakeDeadline := time.Now().Add(connectTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(handshakeDeadline) {
		handshakeDeadline = ctxDeadline
	}

	dialCtx, dialCancel := context.WithDeadline(ctx, handshakeDeadline)
	defer dialCancel()

	proxyAddr := net.JoinHostPort(spec.Host, strconv.Itoa(spec.Port))
	dialer := &net.Dialer{
		Timeout:   connectTimeout,
		KeepAlive: 30 * time.Second,
	}
	rawConn, err := dialer.DialContext(dialCtx, "tcp", proxyAddr)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, ctx.Err()
		}
		if isTimeoutErr(err) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, &ProxyError{
				Kind:    ErrProxyTimeout,
				Message: "连接 GPU 分配代理超时，请检查代理地址、端口或网络连通性",
				Cause:   err,
			}
		}
		return nil, &ProxyError{
			Kind:    ErrProxyConnect,
			Message: fmt.Sprintf("SOCKS5 代理连接失败：%v", err),
			Cause:   err,
		}
	}

	failConn := func(inner error) error {
		_ = rawConn.Close()
		if errors.Is(ctx.Err(), context.Canceled) {
			return ctx.Err()
		}
		var pe *ProxyError
		if errors.As(inner, &pe) {
			return pe
		}
		if isTLSCertError(inner) {
			return &ProxyError{
				Kind:    ErrProxyTLS,
				Message: "通过分配代理建立加密连接失败（证书校验未通过），请勿使用会解密 HTTPS 流量的代理",
				Cause:   inner,
			}
		}
		if isTimeoutErr(inner) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &ProxyError{
				Kind:    ErrProxyTimeout,
				Message: "连接 GPU 分配代理超时，请检查代理地址、端口或网络连通性",
				Cause:   inner,
			}
		}
		return &ProxyError{
			Kind:    ErrProxyConnect,
			Message: fmt.Sprintf("SOCKS5 代理连接失败：%v", inner),
			Cause:   inner,
		}
	}

	if err := rawConn.SetDeadline(handshakeDeadline); err != nil {
		return nil, failConn(err)
	}
	stopWatch := context.AfterFunc(ctx, func() {
		_ = rawConn.SetDeadline(time.Now())
	})
	defer func() {
		// Stop watcher on error paths; cleared explicitly on success below.
		if stopWatch != nil {
			stopWatch()
		}
	}()

	if err := socks5Handshake(rawConn, spec, targetHost, targetPort); err != nil {
		return nil, failConn(err)
	}

	targetTLS := cloneTLSConfigForServer(baseTLSConfig, targetHost)
	tlsTargetConn := tls.Client(rawConn, targetTLS)
	if err := tlsTargetConn.HandshakeContext(dialCtx); err != nil {
		return nil, failConn(err)
	}

	if !stopWatch() {
		stopWatch = nil
		_ = tlsTargetConn.Close()
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, ctx.Err()
		}
		return nil, &ProxyError{
			Kind:    ErrProxyTimeout,
			Message: "连接 GPU 分配代理超时，请检查代理地址、端口或网络连通性",
			Cause:   ctx.Err(),
		}
	}
	stopWatch = nil
	if err := tlsTargetConn.SetDeadline(time.Time{}); err != nil {
		_ = tlsTargetConn.Close()
		return nil, &ProxyError{
			Kind:    ErrProxyConnect,
			Message: fmt.Sprintf("SOCKS5 代理连接失败：%v", err),
			Cause:   err,
		}
	}
	return tlsTargetConn, nil
}

func validateSpecForTransport(spec *ColabOneAssignProxySpec) (ColabOneAssignProxySpec, error) {
	if spec == nil {
		return ColabOneAssignProxySpec{}, errors.New("请先填写分配代理地址")
	}
	if spec.NormalizedURL != "" {
		parsed, err := ParseAssignProxy(spec.NormalizedURL)
		if err != nil {
			return ColabOneAssignProxySpec{}, err
		}
		if parsed == nil {
			return ColabOneAssignProxySpec{}, errors.New("请先填写分配代理地址")
		}
		return *parsed, nil
	}
	scheme := strings.ToLower(strings.TrimSpace(spec.Scheme))
	if _, ok := defaultPorts[scheme]; !ok {
		return ColabOneAssignProxySpec{}, errors.New("仅支持 http://、https://、socks5:// 或 socks5h:// 代理协议")
	}
	host, err := validateProxyHost(spec.Host)
	if err != nil {
		return ColabOneAssignProxySpec{}, err
	}
	if spec.Port < 1 || spec.Port > 65535 {
		return ColabOneAssignProxySpec{}, errors.New("代理端口必须是 1 至 65535 之间的整数")
	}
	if (scheme == "socks5" || scheme == "socks5h") && (len(spec.Username) > 255 || len(spec.Password) > 255) {
		return ColabOneAssignProxySpec{}, errors.New("SOCKS5 代理用户名或密码长度不能超过 255 字节")
	}
	hostPort := formatHostPort(host, spec.Port)
	var netloc string
	if spec.Username != "" || spec.Password != "" {
		netloc = rfc3986Quote(spec.Username) + ":" + rfc3986Quote(spec.Password) + "@" + hostPort
	} else {
		netloc = hostPort
	}
	schemeTag := strings.ToUpper(scheme)
	if scheme == "socks5" || scheme == "socks5h" {
		schemeTag = "SOCKS5"
	}
	return ColabOneAssignProxySpec{
		Scheme:        scheme,
		Host:          host,
		Port:          spec.Port,
		Username:      spec.Username,
		Password:      spec.Password,
		NormalizedURL: scheme + "://" + netloc,
		MaskedURL:     scheme + "://" + hostPort,
		LogLabel:      schemeTag + " " + hostPort,
	}, nil
}

func cloneTLSConfigForServer(base *tls.Config, serverName string) *tls.Config {
	var cfg *tls.Config
	if base != nil {
		cfg = base.Clone()
	} else {
		cfg = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: false,
		}
	}
	cfg.ServerName = strings.TrimRight(serverName, ".")
	return cfg
}

type bufferedNetConn struct {
	net.Conn
	prefix []byte
}

func (c *bufferedNetConn) Read(p []byte) (int, error) {
	if len(c.prefix) > 0 {
		n := copy(p, c.prefix)
		c.prefix = c.prefix[n:]
		if len(c.prefix) == 0 {
			c.prefix = nil
		}
		return n, nil
	}
	return c.Conn.Read(p)
}

func dialHTTPSViaHTTPProxy(
	ctx context.Context,
	spec ColabOneAssignProxySpec,
	targetAddr string,
	connectTimeout time.Duration,
	baseTLSConfig *tls.Config,
) (net.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if connectTimeout <= 0 || connectTimeout > AssignProxyConnectTimeout {
		connectTimeout = AssignProxyConnectTimeout
	}

	handshakeDeadline := time.Now().Add(connectTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(handshakeDeadline) {
		handshakeDeadline = ctxDeadline
	}

	dialCtx, dialCancel := context.WithDeadline(ctx, handshakeDeadline)
	defer dialCancel()

	proxyAddr := net.JoinHostPort(spec.Host, strconv.Itoa(spec.Port))
	dialer := &net.Dialer{
		Timeout:   connectTimeout,
		KeepAlive: 30 * time.Second,
	}
	rawConn, err := dialer.DialContext(dialCtx, "tcp", proxyAddr)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, ctx.Err()
		}
		if isTimeoutErr(err) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, &ProxyError{
				Kind:    ErrProxyTimeout,
				Message: "连接 GPU 分配代理超时，请检查代理地址、端口或网络连通性",
				Cause:   err,
			}
		}
		return nil, &ProxyError{
			Kind:    ErrProxyConnect,
			Message: fmt.Sprintf("代理连接失败：%v", err),
			Cause:   err,
		}
	}

	conn := rawConn
	failConn := func(inner error) error {
		_ = conn.Close()
		if errors.Is(ctx.Err(), context.Canceled) {
			return ctx.Err()
		}
		var pe *ProxyError
		if errors.As(inner, &pe) {
			return pe
		}
		if isTLSCertError(inner) {
			return &ProxyError{
				Kind:    ErrProxyTLS,
				Message: "通过分配代理建立加密连接失败（证书校验未通过），请勿使用会解密 HTTPS 流量的代理",
				Cause:   inner,
			}
		}
		if isTimeoutErr(inner) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &ProxyError{
				Kind:    ErrProxyTimeout,
				Message: "连接 GPU 分配代理超时，请检查代理地址、端口或网络连通性",
				Cause:   inner,
			}
		}
		return &ProxyError{
			Kind:    ErrProxyConnect,
			Message: fmt.Sprintf("代理连接失败：%v", inner),
			Cause:   inner,
		}
	}

	if err := rawConn.SetDeadline(handshakeDeadline); err != nil {
		return nil, failConn(err)
	}
	stopWatch := context.AfterFunc(ctx, func() {
		_ = rawConn.SetDeadline(time.Now())
	})
	defer func() {
		// Stop watcher on error paths; cleared explicitly on success below.
		if stopWatch != nil {
			stopWatch()
		}
	}()

	// Optional TLS wrapper when talking to an https:// proxy itself.
	if spec.Scheme == "https" {
		proxyTLS := cloneTLSConfigForServer(baseTLSConfig, spec.Host)
		tlsProxyConn := tls.Client(conn, proxyTLS)
		if err := tlsProxyConn.HandshakeContext(dialCtx); err != nil {
			return nil, failConn(err)
		}
		conn = tlsProxyConn
	}

	// Send HTTP CONNECT bounded strictly by connectTimeout (avoiding Go http.Transport's 1-minute CONNECT wait).
	var reqBuf strings.Builder
	reqBuf.WriteString("CONNECT ")
	reqBuf.WriteString(targetAddr)
	reqBuf.WriteString(" HTTP/1.1\r\nHost: ")
	reqBuf.WriteString(targetAddr)
	reqBuf.WriteString("\r\n")
	if spec.HasAuth() {
		creds := base64.StdEncoding.EncodeToString([]byte(spec.Username + ":" + spec.Password))
		reqBuf.WriteString("Proxy-Authorization: Basic ")
		reqBuf.WriteString(creds)
		reqBuf.WriteString("\r\n")
	}
	reqBuf.WriteString("\r\n")
	if _, err := io.WriteString(conn, reqBuf.String()); err != nil {
		return nil, failConn(err)
	}

	limited := io.LimitReader(conn, maxProxyConnectResponseBytes)
	br := bufio.NewReader(limited)
	connectReq := &http.Request{Method: http.MethodConnect}
	resp, err := http.ReadResponse(br, connectReq)
	if err != nil {
		if lr, ok := limited.(*io.LimitedReader); ok && lr.N <= 0 {
			return nil, failConn(&ProxyError{
				Kind:    ErrProxyConnect,
				Message: "代理连接失败：CONNECT 响应头超过 64 KiB 限制",
				Cause:   err,
			})
		}
		return nil, failConn(err)
	}
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	if resp.StatusCode == http.StatusProxyAuthRequired {
		return nil, failConn(&ProxyError{
			Kind:    ErrProxyAuth,
			Message: "GPU 分配代理认证失败，请检查代理用户名与密码后重试",
		})
	}
	if resp.StatusCode != http.StatusOK {
		return nil, failConn(&ProxyError{
			Kind:    ErrProxyConnect,
			Message: fmt.Sprintf("代理拒绝连接 (HTTP %d)", resp.StatusCode),
		})
	}

	tunnelConn := conn
	if buffered := br.Buffered(); buffered > 0 {
		peeked, peekErr := br.Peek(buffered)
		if peekErr != nil {
			return nil, failConn(peekErr)
		}
		prefix := make([]byte, len(peeked))
		copy(prefix, peeked)
		tunnelConn = &bufferedNetConn{Conn: conn, prefix: prefix}
	}

	// Perform verified TLS handshake with the destination host over the proxy tunnel.
	targetHost, _, splitErr := net.SplitHostPort(targetAddr)
	if splitErr != nil {
		targetHost = targetAddr
	}
	targetTLS := cloneTLSConfigForServer(baseTLSConfig, targetHost)
	tlsTargetConn := tls.Client(tunnelConn, targetTLS)
	if err := tlsTargetConn.HandshakeContext(dialCtx); err != nil {
		return nil, failConn(err)
	}

	if !stopWatch() {
		stopWatch = nil
		_ = tlsTargetConn.Close()
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, ctx.Err()
		}
		return nil, &ProxyError{
			Kind:    ErrProxyTimeout,
			Message: "连接 GPU 分配代理超时，请检查代理地址、端口或网络连通性",
			Cause:   ctx.Err(),
		}
	}
	stopWatch = nil
	if err := tlsTargetConn.SetDeadline(time.Time{}); err != nil {
		_ = tlsTargetConn.Close()
		return nil, &ProxyError{
			Kind:    ErrProxyConnect,
			Message: fmt.Sprintf("代理连接失败：%v", err),
			Cause:   err,
		}
	}
	return tlsTargetConn, nil
}

func buildIsolatedHTTPTransport(
	spec ColabOneAssignProxySpec,
	connectTimeout time.Duration,
	readTimeout time.Duration,
	rootCAs *x509.CertPool,
) *http.Transport {
	if connectTimeout <= 0 {
		connectTimeout = AssignProxyConnectTimeout
	}
	if readTimeout <= 0 {
		readTimeout = AssignProxyReadTimeout
	}

	tlsCfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: false,
		RootCAs:            rootCAs,
	}

	tr := &http.Transport{
		TLSClientConfig:       tlsCfg,
		TLSHandshakeTimeout:   connectTimeout,
		ResponseHeaderTimeout: readTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          10,
		MaxIdleConnsPerHost:   4,
	}

	if spec.IsSOCKS5() {
		// Explicitly disable process proxy env vars and dial through RFC 1928 SOCKS5.
		tr.Proxy = nil
		tr.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if network != "" && !strings.HasPrefix(network, "tcp") {
				return nil, &ProxyError{
					Kind:    ErrProxyConnect,
					Message: fmt.Sprintf("SOCKS5 代理连接失败：不支持的网络类型 %s", network),
				}
			}
			return dialTLSOverSOCKS5(ctx, spec, addr, connectTimeout, tlsCfg)
		}
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if network != "" && !strings.HasPrefix(network, "tcp") {
				return nil, &ProxyError{
					Kind:    ErrProxyConnect,
					Message: fmt.Sprintf("SOCKS5 代理连接失败：不支持的网络类型 %s", network),
				}
			}
			host, portStr, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, &ProxyError{
					Kind:    ErrProxyConnect,
					Message: fmt.Sprintf("SOCKS5 代理连接失败：无效的目标地址 %s", addr),
					Cause:   err,
				}
			}
			port, err := strconv.Atoi(portStr)
			if err != nil || port < 1 || port > 65535 {
				return nil, &ProxyError{
					Kind:    ErrProxyConnect,
					Message: fmt.Sprintf("SOCKS5 代理连接失败：无效的目标端口 %s", portStr),
					Cause:   err,
				}
			}
			return dialSOCKS5(ctx, spec, host, port, connectTimeout)
		}
		return tr
	}

	// HTTP / HTTPS proxy: construct explicit ProxyURL (ignoring process env vars).
	// Use explicit ProxyURL for plain http:// targets and our strictly bounded
	// dialHTTPSViaHTTPProxy (15s CONNECT + TLS handshake) for https:// targets.
	proxyURL := &url.URL{
		Scheme: spec.Scheme,
		Host:   formatHostPort(spec.Host, spec.Port),
	}
	if spec.HasAuth() {
		proxyURL.User = url.UserPassword(spec.Username, spec.Password)
	}
	tr.Proxy = func(req *http.Request) (*url.URL, error) {
		if req != nil && req.URL != nil && strings.EqualFold(req.URL.Scheme, "https") {
			return nil, nil
		}
		return proxyURL, nil
	}
	baseDialer := &net.Dialer{
		Timeout:   connectTimeout,
		KeepAlive: 30 * time.Second,
	}
	proxyAddr := net.JoinHostPort(spec.Host, strconv.Itoa(spec.Port))
	tr.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if network != "" && !strings.HasPrefix(network, "tcp") {
			return nil, &ProxyError{
				Kind:    ErrProxyConnect,
				Message: fmt.Sprintf("代理连接失败：不支持的网络类型 %s", network),
			}
		}
		// When forwarding a plain http:// target through an https:// proxy,
		// http.Transport dials the proxy itself via DialTLSContext (no CONNECT).
		if spec.Scheme == "https" && addr == proxyAddr {
			dialCtx, dialCancel := context.WithTimeout(ctx, connectTimeout)
			defer dialCancel()
			rawConn, err := baseDialer.DialContext(dialCtx, network, addr)
			if err != nil {
				if errors.Is(ctx.Err(), context.Canceled) {
					return nil, ctx.Err()
				}
				if isTimeoutErr(err) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
					return nil, &ProxyError{
						Kind:    ErrProxyTimeout,
						Message: "连接代理超时",
						Cause:   err,
					}
				}
				return nil, &ProxyError{
					Kind:    ErrProxyConnect,
					Message: fmt.Sprintf("代理连接失败：%v", err),
					Cause:   err,
				}
			}
			proxyTLS := tlsCfg.Clone()
			proxyTLS.ServerName = strings.TrimRight(spec.Host, ".")
			tlsConn := tls.Client(rawConn, proxyTLS)
			if err := tlsConn.HandshakeContext(dialCtx); err != nil {
				_ = rawConn.Close()
				if errors.Is(ctx.Err(), context.Canceled) {
					return nil, ctx.Err()
				}
				if isTLSCertError(err) {
					return nil, &ProxyError{
						Kind:    ErrProxyTLS,
						Message: "通过分配代理建立加密连接失败（证书校验未通过），请勿使用会解密 HTTPS 流量的代理",
						Cause:   err,
					}
				}
				if isTimeoutErr(err) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
					return nil, &ProxyError{
						Kind:    ErrProxyTimeout,
						Message: "连接代理超时",
						Cause:   err,
					}
				}
				return nil, &ProxyError{
					Kind:    ErrProxyConnect,
					Message: fmt.Sprintf("代理连接失败：%v", err),
					Cause:   err,
				}
			}
			return tlsConn, nil
		}
		return dialHTTPSViaHTTPProxy(ctx, spec, addr, connectTimeout, tlsCfg)
	}

	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := baseDialer.DialContext(ctx, network, addr)
		if err != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return nil, ctx.Err()
			}
			if isTimeoutErr(err) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, &ProxyError{
					Kind:    ErrProxyTimeout,
					Message: "连接代理超时",
					Cause:   err,
				}
			}
			return nil, &ProxyError{
				Kind:    ErrProxyConnect,
				Message: fmt.Sprintf("代理连接失败：%v", err),
				Cause:   err,
			}
		}
		return conn, nil
	}
	tr.OnProxyConnectResponse = func(_ context.Context, _ *url.URL, _ *http.Request, connectRes *http.Response) error {
		if connectRes == nil {
			return nil
		}
		if connectRes.StatusCode == http.StatusProxyAuthRequired {
			return &ProxyError{
				Kind:    ErrProxyAuth,
				Message: "GPU 分配代理认证失败，请检查代理用户名与密码后重试",
			}
		}
		if connectRes.StatusCode != http.StatusOK {
			return &ProxyError{
				Kind:    ErrProxyConnect,
				Message: fmt.Sprintf("代理拒绝连接 (HTTP %d)", connectRes.StatusCode),
			}
		}
		return nil
	}
	return tr
}

// ColabOneAssignOnlyTransport is an http.RoundTripper strictly scoped to Colab
// GPU allocation requests (/tun/m/assign) on a single allowed origin via an
// isolated proxy.
//
// Defense-in-depth guarantees:
//   - Only requests matching (allowedScheme, allowedHost, "/tun/m/assign") may
//     leave this transport; any other scheme, host, path, or redirect target is
//     rejected before hitting the network.
//   - Process proxy environment variables are never consulted.
//   - TLS certificate verification is always enforced (InsecureSkipVerify=false).
type ColabOneAssignOnlyTransport struct {
	spec          ColabOneAssignProxySpec
	allowedScheme string
	allowedHost   string
	allowedPort   string
	transport     *http.Transport
}

var _ http.RoundTripper = (*ColabOneAssignOnlyTransport)(nil)

// NewAssignOnlyTransport creates an isolated http.RoundTripper that routes only
// (allowedScheme, allowedHost, "/tun/m/assign") requests through spec.
//
// Empty allowedScheme/allowedHost default to "https" and
// "colab.research.google.com".
func NewAssignOnlyTransport(
	spec *ColabOneAssignProxySpec,
	allowedScheme, allowedHost string,
) (*ColabOneAssignOnlyTransport, error) {
	return newAssignOnlyTransportWithRoots(spec, allowedScheme, allowedHost, nil)
}

func newAssignOnlyTransportWithRoots(
	spec *ColabOneAssignProxySpec,
	allowedScheme, allowedHost string,
	rootCAs *x509.CertPool,
) (*ColabOneAssignOnlyTransport, error) {
	cleanSpec, err := validateSpecForTransport(spec)
	if err != nil {
		return nil, err
	}

	scheme := strings.ToLower(strings.TrimSpace(allowedScheme))
	if scheme == "" {
		scheme = defaultAllowedScheme
	}

	rawAllowedHost := strings.TrimSpace(allowedHost)
	var host, port string
	if rawAllowedHost == "" {
		host = defaultAllowedHost
	} else if splitHost, splitPort, splitErr := net.SplitHostPort(rawAllowedHost); splitErr == nil {
		host = strings.TrimRight(strings.ToLower(splitHost), ".")
		port = splitPort
	} else {
		unbracketed := rawAllowedHost
		if strings.HasPrefix(unbracketed, "[") && strings.HasSuffix(unbracketed, "]") {
			unbracketed = unbracketed[1 : len(unbracketed)-1]
		}
		host = strings.TrimRight(strings.ToLower(unbracketed), ".")
	}
	if host == "" {
		host = defaultAllowedHost
	}
	if port == "" {
		switch scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}

	tr := buildIsolatedHTTPTransport(
		cleanSpec,
		AssignProxyConnectTimeout,
		AssignProxyReadTimeout,
		rootCAs,
	)

	return &ColabOneAssignOnlyTransport{
		spec:          cleanSpec,
		allowedScheme: scheme,
		allowedHost:   host,
		allowedPort:   port,
		transport:     tr,
	}, nil
}

// Spec returns the validated proxy specification bound to this transport.
func (t *ColabOneAssignOnlyTransport) Spec() ColabOneAssignProxySpec {
	return t.spec
}

// AllowedOrigin returns the normalized allowed (scheme, host) enforced by this
// transport.
func (t *ColabOneAssignOnlyTransport) AllowedOrigin() (scheme, host string) {
	return t.allowedScheme, t.allowedHost
}

// CloseIdleConnections closes any idle pooled connections held by the proxy
// transport.
func (t *ColabOneAssignOnlyTransport) CloseIdleConnections() {
	if t != nil && t.transport != nil {
		t.transport.CloseIdleConnections()
	}
}

// Close closes idle pooled connections held by the proxy transport.
func (t *ColabOneAssignOnlyTransport) Close() error {
	t.CloseIdleConnections()
	return nil
}

func effectiveURLPort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

// ValidateURL verifies that u strictly targets (allowedScheme, allowedHost,
// "/tun/m/assign").
func (t *ColabOneAssignOnlyTransport) ValidateURL(u *url.URL) error {
	if t == nil || u == nil {
		return &ProxyError{
			Kind:    ErrForbiddenTarget,
			Message: ErrForbiddenTarget.Error(),
		}
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.TrimRight(strings.ToLower(u.Hostname()), ".")
	path := strings.TrimRight(u.Path, "/")
	if scheme != t.allowedScheme || host != t.allowedHost || path != assignOnlyPath {
		return &ProxyError{
			Kind:    ErrForbiddenTarget,
			Message: ErrForbiddenTarget.Error(),
		}
	}
	if u.RawPath != "" && strings.TrimRight(u.RawPath, "/") != assignOnlyPath {
		return &ProxyError{
			Kind:    ErrForbiddenTarget,
			Message: ErrForbiddenTarget.Error(),
		}
	}
	if t.allowedPort != "" && effectiveURLPort(u) != t.allowedPort {
		return &ProxyError{
			Kind:    ErrForbiddenTarget,
			Message: ErrForbiddenTarget.Error(),
		}
	}
	return nil
}

// CheckRedirect enforces that HTTP redirects may never leave the allowed origin
// or /tun/m/assign.
func (t *ColabOneAssignOnlyTransport) CheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if req == nil {
		return &ProxyError{
			Kind:    ErrForbiddenTarget,
			Message: ErrForbiddenTarget.Error(),
		}
	}
	return t.ValidateURL(req.URL)
}

// NewClient constructs an http.Client bound to this transport with redirect
// protection enabled.
func (t *ColabOneAssignOnlyTransport) NewClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport:     t,
		CheckRedirect: t.CheckRedirect,
		Timeout:       timeout,
	}
}

func isRedirectStatus(code int) bool {
	switch code {
	case http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

// RoundTrip executes req through the isolated proxy after verifying that req
// strictly targets (allowedScheme, allowedHost, "/tun/m/assign"). Any 3xx
// response attempting to redirect off /tun/m/assign is rejected immediately.
func (t *ColabOneAssignOnlyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, &ProxyError{
			Kind:    ErrForbiddenTarget,
			Message: ErrForbiddenTarget.Error(),
		}
	}
	if err := t.ValidateURL(req.URL); err != nil {
		return nil, err
	}

	resp, err := t.transport.RoundTrip(req)
	if err != nil {
		var pe *ProxyError
		if errors.As(err, &pe) {
			return nil, err
		}
		if isTLSCertError(err) {
			return nil, &ProxyError{
				Kind:    ErrProxyTLS,
				Message: FormatProxyError(err),
				Cause:   err,
			}
		}
		if isProxyAuthError(err) {
			return nil, &ProxyError{
				Kind:    ErrProxyAuth,
				Message: FormatProxyError(err),
				Cause:   err,
			}
		}
		if IsProxyFailFastError(err) {
			kind := ErrProxyConnect
			if isTimeoutErr(err) {
				kind = ErrProxyTimeout
			}
			return nil, &ProxyError{
				Kind:    kind,
				Message: FormatProxyError(err),
				Cause:   err,
			}
		}
		return nil, err
	}

	if isRedirectStatus(resp.StatusCode) {
		if loc := strings.TrimSpace(resp.Header.Get("Location")); loc != "" {
			nextURL, parseErr := req.URL.Parse(loc)
			if parseErr != nil || t.ValidateURL(nextURL) != nil {
				_ = resp.Body.Close()
				return nil, &ProxyError{
					Kind:    ErrForbiddenTarget,
					Message: ErrForbiddenTarget.Error(),
				}
			}
		}
	}
	return resp, nil
}

func isTLSCertError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrProxyTLS) {
		return true
	}
	var certVerifyErr *tls.CertificateVerificationError
	if errors.As(err, &certVerifyErr) {
		return true
	}
	var unknownAuthErr x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthErr) {
		return true
	}
	var certInvalidErr x509.CertificateInvalidError
	if errors.As(err, &certInvalidErr) {
		return true
	}
	var hostErr x509.HostnameError
	if errors.As(err, &hostErr) {
		return true
	}
	var sysRootsErr x509.SystemRootsError
	if errors.As(err, &sysRootsErr) {
		return true
	}
	var insecureAlgErr x509.InsecureAlgorithmError
	if errors.As(err, &insecureAlgErr) {
		return true
	}
	var recHdrErr tls.RecordHeaderError
	if errors.As(err, &recHdrErr) {
		return true
	}
	var alertErr tls.AlertError
	if errors.As(err, &alertErr) {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "x509:")
}

var reHTTP407Status = regexp.MustCompile(`(?:^|[\s(])407(?:$|[\s),])`)

func isProxyAuthError(err error) bool {
	if err == nil {
		return false
	}
	var pe *ProxyError
	if errors.As(err, &pe) {
		if pe.Kind != nil {
			return pe.Kind == ErrProxyAuth
		}
	} else {
		var typed interface{ ProxyErrorType() string }
		if errors.As(err, &typed) {
			if k := strings.ToLower(strings.TrimSpace(typed.ProxyErrorType())); k != "" {
				return k == "auth"
			}
		}
	}
	if errors.Is(err, ErrProxyAuth) {
		return true
	}
	raw := err.Error()
	lower := strings.ToLower(raw)
	return strings.Contains(lower, "proxy authentication required") ||
		strings.Contains(raw, "认证失败") ||
		strings.Contains(raw, "拒绝认证") ||
		strings.Contains(raw, "要求用户名与密码认证") ||
		strings.Contains(raw, "需要用户名与密码") ||
		reHTTP407Status.MatchString(lower)
}

// IsProxyFailFastError reports whether err represents a proxy transport,
// authentication, TLS verification, or proxy-connect timeout error that should
// fail fast during GPU allocation instead of retrying.
func IsProxyFailFastError(err error) bool {
	if err == nil {
		return false
	}
	var pe *ProxyError
	if errors.As(err, &pe) {
		return true
	}
	if isTLSCertError(err) || isProxyAuthError(err) {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "proxyconnect")
}

// FormatProxyError returns a user-friendly Chinese error message for allocation
// proxy failures matching ColabOne's frontend i18n dictionary.
func FormatProxyError(err error) string {
	if err == nil {
		return "GPU 分配代理连接失败，请检查代理地址、端口、认证信息或服务状态后重试"
	}
	if errors.Is(err, ErrForbiddenTarget) {
		return ErrForbiddenTarget.Error()
	}
	if isTLSCertError(err) {
		return "通过分配代理建立加密连接失败（证书校验未通过），请勿使用会解密 HTTPS 流量的代理"
	}
	if isProxyAuthError(err) {
		return "GPU 分配代理认证失败，请检查代理用户名与密码后重试"
	}
	raw := err.Error()
	lower := strings.ToLower(raw)
	if isTimeoutErr(err) ||
		strings.Contains(raw, "超时") ||
		strings.Contains(lower, "i/o timeout") ||
		strings.Contains(lower, "deadline exceeded") {
		return "连接 GPU 分配代理超时，请检查代理地址、端口或网络连通性"
	}
	return "GPU 分配代理连接失败，请检查代理地址、端口、认证信息或服务状态后重试"
}

// TestAssignProxy tests allocation proxy connectivity without sending any
// Google OAuth credentials.
func TestAssignProxy(
	ctx context.Context,
	proxyURL, probeURL string,
	timeout time.Duration,
) (map[string]any, error) {
	return testAssignProxyWithRoots(ctx, proxyURL, probeURL, timeout, nil)
}

func testAssignProxyWithRoots(
	ctx context.Context,
	proxyURL, probeURL string,
	timeout time.Duration,
	rootCAs *x509.CertPool,
) (map[string]any, error) {
	spec, err := ParseAssignProxy(proxyURL)
	if err != nil {
		return nil, err
	}
	if spec == nil {
		return nil, errors.New("请先填写分配代理地址")
	}

	targetURL := strings.TrimSpace(probeURL)
	if targetURL == "" {
		targetURL = DefaultProxyProbeURL
	}
	if timeout <= 0 {
		timeout = DefaultProxyTestTimeout
	}
	if ctx == nil {
		ctx = context.Background()
	}

	connectTimeout := timeout
	if connectTimeout > maxProxyTestConnDelay {
		connectTimeout = maxProxyTestConnDelay
	}

	tr := buildIsolatedHTTPTransport(*spec, connectTimeout, timeout, rootCAs)
	defer tr.CloseIdleConnections()

	client := &http.Client{
		Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, &ProxyError{
			Kind:    ErrProxyConnect,
			Message: FormatProxyError(err),
			Cause:   err,
		}
	}

	started := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		formatted := FormatProxyError(err)
		kind := ErrProxyConnect
		if isTLSCertError(err) {
			kind = ErrProxyTLS
		} else if isProxyAuthError(err) {
			kind = ErrProxyAuth
		} else if isTimeoutErr(err) || strings.Contains(err.Error(), "超时") {
			kind = ErrProxyTimeout
		}
		return nil, &ProxyError{
			Kind:    kind,
			Message: formatted,
			Cause:   err,
		}
	}
	statusCode := resp.StatusCode
	_ = resp.Body.Close()

	if statusCode == http.StatusProxyAuthRequired {
		return nil, &ProxyError{
			Kind:    ErrProxyAuth,
			Message: "GPU 分配代理认证失败，请检查代理用户名与密码后重试",
		}
	}

	latencyMS := int(math.Round(float64(time.Since(started)) / float64(time.Millisecond)))
	if latencyMS < 1 {
		latencyMS = 1
	}
	return map[string]any{
		"ok":          true,
		"latency_ms":  latencyMS,
		"status_code": statusCode,
		"proxy":       spec.LogLabel,
		"masked_url":  spec.MaskedURL,
	}, nil
}
