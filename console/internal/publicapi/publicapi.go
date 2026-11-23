// Package publicapi provides shared public API gateway configuration and
// validation for the ColabOne desktop client and remote service.
package publicapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	// ModeNamed is the only supported Cloudflare tunnel mode.
	ModeNamed = "named"

	// MaxDomainLength is the maximum permitted rune length for an unnormalized domain.
	MaxDomainLength = 300

	// MaxTunnelTokenLength is the maximum permitted rune length for a pasted tunnel install command or token.
	MaxTunnelTokenLength = 8000
)

// Exact user-facing Chinese error messages preserved for frontend i18n parity.
var (
	ErrTunnelTokenAmbiguous = errors.New("请粘贴 Cloudflare 的完整安装命令或唯一的 Tunnel Token")
	ErrTunnelTokenInvalid   = errors.New("未识别到有效的 Cloudflare Tunnel Token")
	ErrPublicOriginInvalid  = errors.New("请填写公网 HTTPS 固定域名，例如 api.example.com（不含端口或其他路径，不支持 trycloudflare.com 临时域名）")
	ErrPublicModeInvalid    = errors.New("已不再支持临时域名，请配置固定域名和 Cloudflare Tunnel Token")
	ErrAPIKeyInvalid        = errors.New("API Key 格式无效")
)

var (
	colabOneTunnelTokenRe   = regexp.MustCompile(`eyJ[A-Za-z0-9_+/=-]{20,}`)
	colabOnePublicHostRe    = regexp.MustCompile(`^(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,63}$`)
	colabOneAPIKeyRe        = regexp.MustCompile(`^co_[a-zA-Z0-9_-]{40,100}$`)
	colabOneBase64Replacer  = strings.NewReplacer("-", "+", "_", "/")
	colabOneBlockedSuffixes = []string{
		".localhost",
		".local",
		".internal",
		".test",
		".invalid",
		".trycloudflare.com",
	}
	colabOneRequiredTokenKeys = [...]string{"a", "t", "s"}
)

// ValidateTunnelToken extracts a unique Cloudflare Tunnel Token from a pasted
// cloudflared install command or raw token string, verifies that it base64 /
// base64url decodes to a UTF-8 JSON object containing non-empty string fields
// "a", "t", and "s", and returns the original extracted token.
func ValidateTunnelToken(text string) (string, error) {
	rawMatches := colabOneTunnelTokenRe.FindAllString(strings.TrimSpace(text), -1)
	if len(rawMatches) == 0 {
		return "", ErrTunnelTokenAmbiguous
	}

	seen := make(map[string]struct{}, len(rawMatches))
	unique := make([]string, 0, len(rawMatches))
	for _, match := range rawMatches {
		if _, exists := seen[match]; !exists {
			seen[match] = struct{}{}
			unique = append(unique, match)
		}
	}
	if len(unique) != 1 {
		return "", ErrTunnelTokenAmbiguous
	}

	value := unique[0]
	normalized := colabOneBase64Replacer.Replace(value)
	if rem := len(normalized) % 4; rem != 0 {
		normalized += strings.Repeat("=", 4-rem)
	}

	decoded, err := base64.StdEncoding.DecodeString(normalized)
	if err != nil || !utf8.Valid(decoded) {
		return "", ErrTunnelTokenInvalid
	}

	var data map[string]any
	if err := json.Unmarshal(decoded, &data); err != nil || data == nil {
		return "", ErrTunnelTokenInvalid
	}
	for _, key := range colabOneRequiredTokenKeys {
		raw, ok := data[key]
		if !ok {
			return "", ErrTunnelTokenInvalid
		}
		str, ok := raw.(string)
		if !ok || str == "" {
			return "", ErrTunnelTokenInvalid
		}
	}
	return value, nil
}

// ValidatePublicOrigin validates and normalizes a fixed public HTTPS origin
// (e.g. "api.example.com/v1/" -> "https://api.example.com").
func ValidatePublicOrigin(value string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(value), "/")
	if !strings.HasPrefix(trimmed, "https://") {
		trimmed = "https://" + trimmed
	}

	// Reject percent-encoding, query markers, and fragment markers upfront so
	// net/url cannot silently unescape host/path characters or accept empty
	// trailing "?" / "#".
	if strings.ContainsAny(trimmed, "%?#") {
		return "", ErrPublicOriginInvalid
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", ErrPublicOriginInvalid
	}

	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme != "https" ||
		parsed.Opaque != "" ||
		parsed.User != nil ||
		parsed.Port() != "" ||
		strings.Contains(parsed.Host, ":") ||
		strings.ToLower(parsed.Host) != host ||
		(parsed.Path != "" && parsed.Path != "/v1") ||
		parsed.RawPath != "" ||
		parsed.RawQuery != "" ||
		parsed.ForceQuery ||
		parsed.Fragment != "" ||
		parsed.RawFragment != "" ||
		!colabOnePublicHostRe.MatchString(host) ||
		host == "trycloudflare.com" ||
		colabOneHasBlockedSuffix(host) {
		return "", ErrPublicOriginInvalid
	}

	return "https://" + host, nil
}

func colabOneHasBlockedSuffix(host string) bool {
	for _, suffix := range colabOneBlockedSuffixes {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// IsValidAPIKey reports whether value matches the ColabOne public API key pattern.
func IsValidAPIKey(value string) bool {
	return colabOneAPIKeyRe.MatchString(value)
}

// ColabOnePublicConfig holds validated public API gateway configuration shared
// by the desktop client and the remote service.
type ColabOnePublicConfig struct {
	Mode        string `json:"mode"`
	Domain      string `json:"domain"`
	TunnelToken string `json:"tunnel_token"`
	APIKey      string `json:"api_key"`
}

// UnmarshalJSON strictly decodes ColabOnePublicConfig (matching Pydantic extra="forbid"):
// unknown fields and explicit null values are rejected, while an omitted "mode"
// field defaults to ModeNamed (explicit "mode": "" or "mode": null is rejected).
func (c *ColabOnePublicConfig) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ErrPublicModeInvalid
	}
	var fields map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	if err := dec.Decode(&fields); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("JSON 包含多余内容")
	}
	if fields == nil {
		return ErrPublicModeInvalid
	}

	out := ColabOnePublicConfig{Mode: ModeNamed}
	for k, raw := range fields {
		rawTrim := bytes.TrimSpace(raw)
		if bytes.Equal(rawTrim, []byte("null")) {
			switch k {
			case "mode":
				return ErrPublicModeInvalid
			case "domain":
				return ErrPublicOriginInvalid
			case "tunnel_token":
				return ErrTunnelTokenInvalid
			case "api_key":
				return ErrAPIKeyInvalid
			default:
				return fmt.Errorf("未知字段: %s", k)
			}
		}
		switch k {
		case "mode":
			var v string
			if err := json.Unmarshal(rawTrim, &v); err != nil {
				return ErrPublicModeInvalid
			}
			if v == "" {
				return ErrPublicModeInvalid
			}
			out.Mode = v
		case "domain":
			var v string
			if err := json.Unmarshal(rawTrim, &v); err != nil {
				return ErrPublicOriginInvalid
			}
			out.Domain = v
		case "tunnel_token":
			var v string
			if err := json.Unmarshal(rawTrim, &v); err != nil {
				return ErrTunnelTokenInvalid
			}
			out.TunnelToken = v
		case "api_key":
			var v string
			if err := json.Unmarshal(rawTrim, &v); err != nil {
				return ErrAPIKeyInvalid
			}
			out.APIKey = v
		default:
			return fmt.Errorf("未知字段: %s", k)
		}
	}
	*c = out
	return nil
}

// Validate validates all fields of the configuration and normalizes Domain and
// TunnelToken in place only when every check succeeds.
func (c *ColabOnePublicConfig) Validate() error {
	if c == nil {
		return ErrPublicModeInvalid
	}
	if c.Mode != ModeNamed {
		return ErrPublicModeInvalid
	}
	if !IsValidAPIKey(c.APIKey) {
		return ErrAPIKeyInvalid
	}
	if utf8.RuneCountInString(c.Domain) > MaxDomainLength {
		return ErrPublicOriginInvalid
	}
	normalizedDomain, err := ValidatePublicOrigin(c.Domain)
	if err != nil {
		return err
	}
	if utf8.RuneCountInString(c.TunnelToken) > MaxTunnelTokenLength {
		return ErrTunnelTokenInvalid
	}
	normalizedToken, err := ValidateTunnelToken(c.TunnelToken)
	if err != nil {
		return err
	}

	c.Domain = normalizedDomain
	c.TunnelToken = normalizedToken
	return nil
}

// String redacts secret fields to prevent accidental leakage in logs.
func (c ColabOnePublicConfig) String() string {
	return fmt.Sprintf(
		"ColabOnePublicConfig{Mode:%q, Domain:%q, HasToken:%t, HasAPIKey:%t}",
		c.Mode,
		c.Domain,
		c.TunnelToken != "",
		c.APIKey != "",
	)
}

// GoString redacts secret fields when formatted with %#v.
func (c ColabOnePublicConfig) GoString() string {
	return c.String()
}
