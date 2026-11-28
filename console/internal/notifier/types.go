// Package notifier implements ColabOne's unified pure-Go notification engine
// supporting native desktop notifications, Telegram Bot API, Bark, and SMTP email.
package notifier

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/luolongfei/ColabOne/console/internal/assignproxy"
)

// ColabOneNotificationCategory classifies notification events for user filter toggles.
type ColabOneNotificationCategory string

const (
	CategoryGPU   ColabOneNotificationCategory = "gpu"
	CategoryTask  ColabOneNotificationCategory = "task"
	CategoryAlert ColabOneNotificationCategory = "alert"
)

// ColabOneNotificationSeverity represents the visual and urgency level of an event.
type ColabOneNotificationSeverity string

const (
	SeveritySuccess ColabOneNotificationSeverity = "success"
	SeverityWarning ColabOneNotificationSeverity = "warning"
	SeverityError   ColabOneNotificationSeverity = "error"
	SeverityInfo    ColabOneNotificationSeverity = "info"
)

// ColabOneNotificationEventKind identifies the specific lifecycle event.
type ColabOneNotificationEventKind string

const (
	EventGPUAllocated           ColabOneNotificationEventKind = "gpu.allocated"
	EventGPUAllocateFailed      ColabOneNotificationEventKind = "gpu.allocate_failed"
	EventGPUDisconnected        ColabOneNotificationEventKind = "gpu.disconnected"
	EventGPUAutoReleased        ColabOneNotificationEventKind = "gpu.auto_released"
	EventOutputReady            ColabOneNotificationEventKind = "output.ready"
	EventOutputFailed           ColabOneNotificationEventKind = "output.failed"
	EventModelDownloadCompleted ColabOneNotificationEventKind = "model.download_completed"
	EventModelDownloadFailed    ColabOneNotificationEventKind = "model.download_failed"
	EventModelLoadCompleted     ColabOneNotificationEventKind = "model.load_completed"
	EventModelLoadFailed        ColabOneNotificationEventKind = "model.load_failed"
	EventAuthRequired           ColabOneNotificationEventKind = "auth.required"
	EventTest                   ColabOneNotificationEventKind = "system.test"
)

// Channel IDs.
const (
	ChannelSystem   = "system"
	ChannelTelegram = "telegram"
	ChannelBark     = "bark"
	ChannelEmail    = "email"
)

// Default remote notification endpoints.
const (
	COLABONE_DEFAULT_TELEGRAM_API_BASE = "https://api.telegram.org"
	COLABONE_DEFAULT_BARK_SERVER_URL   = "https://api.day.app"
)

// Secret field IDs stored in ColabOne's encrypted credential vault.
const (
	SecretTelegramBotToken = "telegram_bot_token"
	SecretTelegramProxyURL = "telegram_proxy_url"
	SecretBarkDeviceKey    = "bark_device_key"
	SecretEmailPassword    = "email_password"
)

// ColabOneNotificationField represents a key-value detail row rendered in rich notifications.
type ColabOneNotificationField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// ColabOneNotificationEvent carries all structured metadata for a notification dispatch.
type ColabOneNotificationEvent struct {
	ID            string
	Kind          ColabOneNotificationEventKind
	Category      ColabOneNotificationCategory
	Severity      ColabOneNotificationSeverity
	DedupeKey     string
	Title         string
	Subtitle      string
	Body          string
	Fields        []ColabOneNotificationField
	ActionHint    string
	NavigateTab   string
	OpenOutputs   bool
	ImageBytes    []byte
	ImageMime     string
	ImageFilename string
	Locale        string
	OccurredAt    time.Time
	IsTest        bool
}

// ColabOneSystemChannelConfig configures native OS desktop notifications.
type ColabOneSystemChannelConfig struct {
	Enabled bool `json:"enabled"`
}

// ColabOneTelegramChannelConfig configures Telegram Bot API delivery.
type ColabOneTelegramChannelConfig struct {
	Enabled          bool   `json:"enabled"`
	BotToken         string `json:"-"`
	ChatID           string `json:"chat_id"`
	APIBaseURL       string `json:"api_base_url"`
	ProxyURL         string `json:"-"`
	SendPreviewImage bool   `json:"send_preview_image"`
}

// ColabOneBarkChannelConfig configures Bark iOS push delivery.
type ColabOneBarkChannelConfig struct {
	Enabled   bool   `json:"enabled"`
	ServerURL string `json:"server_url"`
	DeviceKey string `json:"-"`
	Group     string `json:"group"`
	Sound     string `json:"sound"`
	Level     string `json:"level"`
}

// ColabOneEmailChannelConfig configures SMTP email delivery.
type ColabOneEmailChannelConfig struct {
	Enabled           bool   `json:"enabled"`
	SMTPHost          string `json:"smtp_host"`
	SMTPPort          int    `json:"smtp_port"`
	Security          string `json:"security"` // "tls" | "starttls" | "none"
	Username          string `json:"username"`
	Password          string `json:"-"`
	FromAddress       string `json:"from_address"`
	FromName          string `json:"from_name"`
	ToAddresses       string `json:"to_addresses"`
	EmbedPreviewImage bool   `json:"embed_preview_image"`
}

// ColabOneNotificationConfig holds all user notification preferences and channel settings.
type ColabOneNotificationConfig struct {
	NotifyGPU               bool                          `json:"notify_gpu"`
	NotifyTask              bool                          `json:"notify_task"`
	NotifyAlert             bool                          `json:"notify_alert"`
	SystemOnlyWhenUnfocused bool                          `json:"system_only_when_unfocused"`
	RemoteOnlyWhenUnfocused bool                          `json:"remote_only_when_unfocused"`
	System                  ColabOneSystemChannelConfig   `json:"system"`
	Telegram                ColabOneTelegramChannelConfig `json:"telegram"`
	Bark                    ColabOneBarkChannelConfig     `json:"bark"`
	Email                   ColabOneEmailChannelConfig    `json:"email"`
}

var (
	_ fmt.Stringer   = ColabOneNotificationConfig{}
	_ fmt.GoStringer = ColabOneNotificationConfig{}
	_ fmt.Stringer   = ColabOneTelegramChannelConfig{}
	_ fmt.GoStringer = ColabOneTelegramChannelConfig{}
	_ fmt.Stringer   = ColabOneBarkChannelConfig{}
	_ fmt.GoStringer = ColabOneBarkChannelConfig{}
	_ fmt.Stringer   = ColabOneEmailChannelConfig{}
	_ fmt.GoStringer = ColabOneEmailChannelConfig{}
)

func colabOneRedactedMarker(secret string) string {
	if strings.TrimSpace(secret) == "" {
		return ""
	}
	return "[REDACTED]"
}

// String returns a secret-redacted representation safe for logging.
func (c ColabOneTelegramChannelConfig) String() string {
	return fmt.Sprintf(
		"Telegram{Enabled:%t, BotToken:%q, ChatID:%q, APIBaseURL:%q, ProxyURL:%q, SendPreviewImage:%t}",
		c.Enabled,
		colabOneRedactedMarker(c.BotToken),
		c.ChatID,
		c.APIBaseURL,
		colabOneRedactedMarker(c.ProxyURL),
		c.SendPreviewImage,
	)
}

// GoString returns a secret-redacted Go-syntax representation safe for %#v formatting.
func (c ColabOneTelegramChannelConfig) GoString() string {
	return fmt.Sprintf(
		"notifier.ColabOneTelegramChannelConfig{Enabled:%t, BotToken:%q, ChatID:%q, APIBaseURL:%q, ProxyURL:%q, SendPreviewImage:%t}",
		c.Enabled,
		colabOneRedactedMarker(c.BotToken),
		c.ChatID,
		c.APIBaseURL,
		colabOneRedactedMarker(c.ProxyURL),
		c.SendPreviewImage,
	)
}

// String returns a secret-redacted representation safe for logging.
func (c ColabOneBarkChannelConfig) String() string {
	return fmt.Sprintf(
		"Bark{Enabled:%t, ServerURL:%q, DeviceKey:%q, Group:%q, Sound:%q, Level:%q}",
		c.Enabled,
		c.ServerURL,
		colabOneRedactedMarker(c.DeviceKey),
		c.Group,
		c.Sound,
		c.Level,
	)
}

// GoString returns a secret-redacted Go-syntax representation safe for %#v formatting.
func (c ColabOneBarkChannelConfig) GoString() string {
	return fmt.Sprintf(
		"notifier.ColabOneBarkChannelConfig{Enabled:%t, ServerURL:%q, DeviceKey:%q, Group:%q, Sound:%q, Level:%q}",
		c.Enabled,
		c.ServerURL,
		colabOneRedactedMarker(c.DeviceKey),
		c.Group,
		c.Sound,
		c.Level,
	)
}

// String returns a secret-redacted representation safe for logging.
func (c ColabOneEmailChannelConfig) String() string {
	return fmt.Sprintf(
		"Email{Enabled:%t, SMTPHost:%q, SMTPPort:%d, Security:%q, Username:%q, Password:%q, FromAddress:%q, FromName:%q, ToAddresses:%q, EmbedPreviewImage:%t}",
		c.Enabled,
		c.SMTPHost,
		c.SMTPPort,
		c.Security,
		c.Username,
		colabOneRedactedMarker(c.Password),
		c.FromAddress,
		c.FromName,
		c.ToAddresses,
		c.EmbedPreviewImage,
	)
}

// GoString returns a secret-redacted Go-syntax representation safe for %#v formatting.
func (c ColabOneEmailChannelConfig) GoString() string {
	return fmt.Sprintf(
		"notifier.ColabOneEmailChannelConfig{Enabled:%t, SMTPHost:%q, SMTPPort:%d, Security:%q, Username:%q, Password:%q, FromAddress:%q, FromName:%q, ToAddresses:%q, EmbedPreviewImage:%t}",
		c.Enabled,
		c.SMTPHost,
		c.SMTPPort,
		c.Security,
		c.Username,
		colabOneRedactedMarker(c.Password),
		c.FromAddress,
		c.FromName,
		c.ToAddresses,
		c.EmbedPreviewImage,
	)
}

// String returns a secret-redacted summary safe for %s, %v, and %+v logging.
func (c ColabOneNotificationConfig) String() string {
	return fmt.Sprintf(
		"ColabOneNotificationConfig{NotifyGPU:%t, NotifyTask:%t, NotifyAlert:%t, SystemOnlyWhenUnfocused:%t, RemoteOnlyWhenUnfocused:%t, System:{Enabled:%t}, %s, %s, %s}",
		c.NotifyGPU,
		c.NotifyTask,
		c.NotifyAlert,
		c.SystemOnlyWhenUnfocused,
		c.RemoteOnlyWhenUnfocused,
		c.System.Enabled,
		c.Telegram.String(),
		c.Bark.String(),
		c.Email.String(),
	)
}

// GoString returns a secret-redacted Go-syntax summary safe for %#v logging.
func (c ColabOneNotificationConfig) GoString() string {
	return fmt.Sprintf(
		"notifier.ColabOneNotificationConfig{NotifyGPU:%t, NotifyTask:%t, NotifyAlert:%t, SystemOnlyWhenUnfocused:%t, RemoteOnlyWhenUnfocused:%t, System:%#v, Telegram:%#v, Bark:%#v, Email:%#v}",
		c.NotifyGPU,
		c.NotifyTask,
		c.NotifyAlert,
		c.SystemOnlyWhenUnfocused,
		c.RemoteOnlyWhenUnfocused,
		c.System,
		c.Telegram,
		c.Bark,
		c.Email,
	)
}

// DefaultConfig returns ColabOne's recommended default notification configuration.
func DefaultConfig() ColabOneNotificationConfig {
	return ColabOneNotificationConfig{
		NotifyGPU:               true,
		NotifyTask:              true,
		NotifyAlert:             true,
		SystemOnlyWhenUnfocused: true,
		RemoteOnlyWhenUnfocused: false,
		System: ColabOneSystemChannelConfig{
			Enabled: true,
		},
		Telegram: ColabOneTelegramChannelConfig{
			SendPreviewImage: true,
		},
		Bark: ColabOneBarkChannelConfig{
			ServerURL: COLABONE_DEFAULT_BARK_SERVER_URL,
			Group:     "ColabOne",
		},
		Email: ColabOneEmailChannelConfig{
			SMTPPort:          465,
			Security:          "tls",
			FromName:          "ColabOne",
			EmbedPreviewImage: true,
		},
	}
}

// TelegramConfigured reports whether Telegram has the required bot token and chat ID.
func (c ColabOneNotificationConfig) TelegramConfigured() bool {
	tok := strings.TrimSpace(c.Telegram.BotToken)
	chatID := strings.TrimSpace(c.Telegram.ChatID)
	if tok == "" || chatID == "" {
		return false
	}
	if err := colabOneValidateTelegramBotToken(tok); err != nil {
		return false
	}
	if err := colabOneRejectControlOrSpace(chatID, "Telegram Chat ID"); err != nil {
		return false
	}
	if strings.TrimSpace(c.Telegram.APIBaseURL) != "" {
		if _, err := colabOneValidateHTTPBaseURL(c.Telegram.APIBaseURL, "Telegram API 地址"); err != nil {
			return false
		}
	}
	return true
}

// BarkConfigured reports whether Bark has a valid server URL and device key.
func (c ColabOneNotificationConfig) BarkConfigured() bool {
	key := strings.TrimSpace(c.Bark.DeviceKey)
	if key == "" || strings.ContainsRune(key, '/') {
		return false
	}
	if err := colabOneRejectControlOrSpace(key, "Bark Device Key"); err != nil {
		return false
	}
	if strings.TrimSpace(c.Bark.ServerURL) != "" {
		clean, err := colabOneNormalizeBarkBaseURL(c.Bark.ServerURL)
		return err == nil && clean != ""
	}
	return COLABONE_DEFAULT_BARK_SERVER_URL != ""
}

// EmailConfigured reports whether Email has all required SMTP server, sender, and recipient fields.
func (c ColabOneNotificationConfig) EmailConfigured() bool {
	host, err := colabOneNormalizeSMTPHost(c.Email.SMTPHost)
	if err != nil || host == "" {
		return false
	}
	if c.Email.SMTPPort < 1 || c.Email.SMTPPort > 65535 {
		return false
	}
	if !colabOneValidEmailSecurity(c.Email.Security) {
		return false
	}
	if colabOneEffectiveEmailFrom(c.Email) == "" {
		return false
	}
	recipients, err := colabOneParseEmailRecipients(c.Email.ToAddresses)
	return err == nil && len(recipients) > 0
}

// PublicSettingsMap returns the non-secret portion of the configuration safe for settings.json.
func (c ColabOneNotificationConfig) PublicSettingsMap() map[string]any {
	barkServer := strings.TrimSpace(c.Bark.ServerURL)
	if barkServer == "" {
		barkServer = COLABONE_DEFAULT_BARK_SERVER_URL
	}
	smtpPort := c.Email.SMTPPort
	if smtpPort < 1 || smtpPort > 65535 {
		smtpPort = 465
	}
	security := strings.ToLower(strings.TrimSpace(c.Email.Security))
	if !colabOneValidEmailSecurity(security) {
		security = "tls"
	}
	return map[string]any{
		"notify_gpu":                 c.NotifyGPU,
		"notify_task":                c.NotifyTask,
		"notify_alert":               c.NotifyAlert,
		"system_only_when_unfocused": c.SystemOnlyWhenUnfocused,
		"remote_only_when_unfocused": c.RemoteOnlyWhenUnfocused,
		"system": map[string]any{
			"enabled": c.System.Enabled,
		},
		"telegram": map[string]any{
			"enabled":            c.Telegram.Enabled,
			"chat_id":            strings.TrimSpace(c.Telegram.ChatID),
			"api_base_url":       strings.TrimRight(strings.TrimSpace(c.Telegram.APIBaseURL), "/"),
			"send_preview_image": c.Telegram.SendPreviewImage,
		},
		"bark": map[string]any{
			"enabled":    c.Bark.Enabled,
			"server_url": strings.TrimRight(barkServer, "/"),
			"group":      strings.TrimSpace(c.Bark.Group),
			"sound":      strings.TrimSpace(c.Bark.Sound),
			"level":      strings.TrimSpace(c.Bark.Level),
		},
		"email": map[string]any{
			"enabled":             c.Email.Enabled,
			"smtp_host":           strings.TrimSpace(c.Email.SMTPHost),
			"smtp_port":           smtpPort,
			"security":            security,
			"username":            strings.TrimSpace(c.Email.Username),
			"from_address":        strings.TrimSpace(c.Email.FromAddress),
			"from_name":           strings.TrimSpace(c.Email.FromName),
			"to_addresses":        strings.TrimSpace(c.Email.ToAddresses),
			"embed_preview_image": c.Email.EmbedPreviewImage,
		},
	}
}

// SecretsMap returns only the non-empty secrets suitable for storing in VaultStore.
func (c ColabOneNotificationConfig) SecretsMap() map[string]any {
	out := make(map[string]any)
	if v := strings.TrimSpace(c.Telegram.BotToken); v != "" {
		out[SecretTelegramBotToken] = v
	}
	if v := strings.TrimSpace(c.Telegram.ProxyURL); v != "" {
		out[SecretTelegramProxyURL] = v
	}
	if v := strings.TrimSpace(c.Bark.DeviceKey); v != "" {
		out[SecretBarkDeviceKey] = v
	}
	if c.Email.Password != "" {
		out[SecretEmailPassword] = c.Email.Password
	}
	return out
}

// LoadConfigFromMaps reconstructs a ColabOneNotificationConfig from settings.json
// public values and VaultStore secrets, falling back to DefaultConfig() for missing keys.
func LoadConfigFromMaps(public map[string]any, secrets map[string]any) ColabOneNotificationConfig {
	cfg := DefaultConfig()

	if public != nil {
		if b, ok := colabOneReadBool(public, "notify_gpu"); ok {
			cfg.NotifyGPU = b
		}
		if b, ok := colabOneReadBool(public, "notify_task"); ok {
			cfg.NotifyTask = b
		}
		if b, ok := colabOneReadBool(public, "notify_alert"); ok {
			cfg.NotifyAlert = b
		}
		if b, ok := colabOneReadBool(public, "system_only_when_unfocused"); ok {
			cfg.SystemOnlyWhenUnfocused = b
		}
		if b, ok := colabOneReadBool(public, "remote_only_when_unfocused"); ok {
			cfg.RemoteOnlyWhenUnfocused = b
		}

		if sysMap, ok := colabOneReadMap(public, "system"); ok {
			if b, ok := colabOneReadBool(sysMap, "enabled"); ok {
				cfg.System.Enabled = b
			}
		}

		if tgMap, ok := colabOneReadMap(public, "telegram"); ok {
			if b, ok := colabOneReadBool(tgMap, "enabled"); ok {
				cfg.Telegram.Enabled = b
			}
			if s, ok := colabOneReadString(tgMap, "chat_id"); ok {
				cfg.Telegram.ChatID = strings.TrimSpace(s)
			}
			if s, ok := colabOneReadString(tgMap, "api_base_url"); ok {
				if strings.TrimSpace(s) == "" {
					cfg.Telegram.APIBaseURL = ""
				} else if cleanURL, err := colabOneValidateHTTPBaseURL(s, "Telegram API 地址"); err == nil {
					cfg.Telegram.APIBaseURL = cleanURL
				} else {
					// Defense-in-depth against hand-edited invalid URLs in settings.json.
					cfg.Telegram.APIBaseURL = ""
					cfg.Telegram.Enabled = false
				}
			}
			if b, ok := colabOneReadBool(tgMap, "send_preview_image"); ok {
				cfg.Telegram.SendPreviewImage = b
			}
		}

		if barkMap, ok := colabOneReadMap(public, "bark"); ok {
			if b, ok := colabOneReadBool(barkMap, "enabled"); ok {
				cfg.Bark.Enabled = b
			}
			if s, ok := colabOneReadString(barkMap, "server_url"); ok {
				if strings.TrimSpace(s) != "" {
					if cleanURL, err := colabOneNormalizeBarkBaseURL(s); err == nil && cleanURL != "" {
						cfg.Bark.ServerURL = cleanURL
					} else {
						// Defense-in-depth: never route self-hosted keys to the default server on invalid settings.json.
						cfg.Bark.ServerURL = COLABONE_DEFAULT_BARK_SERVER_URL
						cfg.Bark.Enabled = false
					}
				}
			}
			if s, ok := colabOneReadString(barkMap, "group"); ok {
				cfg.Bark.Group = strings.TrimSpace(s)
			}
			if s, ok := colabOneReadString(barkMap, "sound"); ok {
				cfg.Bark.Sound = strings.TrimSpace(s)
			}
			if s, ok := colabOneReadString(barkMap, "level"); ok {
				cfg.Bark.Level = strings.TrimSpace(s)
			}
		}

		if emailMap, ok := colabOneReadMap(public, "email"); ok {
			if b, ok := colabOneReadBool(emailMap, "enabled"); ok {
				cfg.Email.Enabled = b
			}
			if s, ok := colabOneReadString(emailMap, "smtp_host"); ok {
				if strings.TrimSpace(s) == "" {
					cfg.Email.SMTPHost = ""
				} else if cleanHost, err := colabOneNormalizeSMTPHost(s); err == nil {
					cfg.Email.SMTPHost = cleanHost
				} else {
					cfg.Email.SMTPHost = ""
					cfg.Email.Enabled = false
				}
			}
			if p, ok := colabOneReadInt(emailMap, "smtp_port"); ok && p >= 1 && p <= 65535 {
				cfg.Email.SMTPPort = p
			}
			if s, ok := colabOneReadString(emailMap, "security"); ok {
				sec := strings.ToLower(strings.TrimSpace(s))
				if colabOneValidEmailSecurity(sec) {
					cfg.Email.Security = sec
				}
			}
			if s, ok := colabOneReadString(emailMap, "username"); ok {
				cfg.Email.Username = strings.TrimSpace(s)
			}
			if s, ok := colabOneReadString(emailMap, "from_address"); ok {
				cfg.Email.FromAddress = strings.TrimSpace(s)
			}
			if s, ok := colabOneReadString(emailMap, "from_name"); ok {
				cfg.Email.FromName = strings.TrimSpace(s)
			}
			if s, ok := colabOneReadString(emailMap, "to_addresses"); ok {
				cfg.Email.ToAddresses = strings.TrimSpace(s)
			}
			if b, ok := colabOneReadBool(emailMap, "embed_preview_image"); ok {
				cfg.Email.EmbedPreviewImage = b
			}
		}
	}

	if secrets != nil {
		if s, ok := colabOneReadString(secrets, SecretTelegramBotToken); ok {
			cfg.Telegram.BotToken = strings.TrimSpace(s)
		}
		if s, ok := colabOneReadString(secrets, SecretTelegramProxyURL); ok {
			cfg.Telegram.ProxyURL = strings.TrimSpace(s)
		}
		if s, ok := colabOneReadString(secrets, SecretBarkDeviceKey); ok {
			cfg.Bark.DeviceKey = strings.TrimSpace(s)
		}
		if s, ok := colabOneReadString(secrets, SecretEmailPassword); ok {
			cfg.Email.Password = s
		}
	}

	if strings.TrimSpace(cfg.Bark.ServerURL) == "" {
		cfg.Bark.ServerURL = COLABONE_DEFAULT_BARK_SERVER_URL
	}
	if cfg.Email.SMTPPort < 1 || cfg.Email.SMTPPort > 65535 {
		cfg.Email.SMTPPort = 465
	}
	if !colabOneValidEmailSecurity(cfg.Email.Security) {
		cfg.Email.Security = "tls"
	}
	return cfg
}

// PublicView returns the redacted UI view map safe to serialize over RPC/JSON.
func (c ColabOneNotificationConfig) PublicView(systemSupported bool) map[string]any {
	barkServer := strings.TrimRight(strings.TrimSpace(c.Bark.ServerURL), "/")
	if barkServer == "" {
		barkServer = COLABONE_DEFAULT_BARK_SERVER_URL
	}
	smtpPort := c.Email.SMTPPort
	if smtpPort < 1 || smtpPort > 65535 {
		smtpPort = 465
	}
	security := strings.ToLower(strings.TrimSpace(c.Email.Security))
	if !colabOneValidEmailSecurity(security) {
		security = "tls"
	}

	hasBotToken := strings.TrimSpace(c.Telegram.BotToken) != ""
	hasProxy := strings.TrimSpace(c.Telegram.ProxyURL) != ""
	hasDeviceKey := strings.TrimSpace(c.Bark.DeviceKey) != ""
	hasPassword := c.Email.Password != ""

	return map[string]any{
		"notify_gpu":                 c.NotifyGPU,
		"notify_task":                c.NotifyTask,
		"notify_alert":               c.NotifyAlert,
		"system_only_when_unfocused": c.SystemOnlyWhenUnfocused,
		"remote_only_when_unfocused": c.RemoteOnlyWhenUnfocused,
		"system": map[string]any{
			"enabled":   c.System.Enabled,
			"supported": systemSupported,
		},
		"telegram": map[string]any{
			"enabled":            c.Telegram.Enabled,
			"configured":         c.TelegramConfigured(),
			"has_bot_token":      hasBotToken,
			"masked_bot_token":   MaskSecret(c.Telegram.BotToken),
			"chat_id":            strings.TrimSpace(c.Telegram.ChatID),
			"api_base_url":       strings.TrimRight(strings.TrimSpace(c.Telegram.APIBaseURL), "/"),
			"has_proxy":          hasProxy,
			"masked_proxy_url":   colabOneMaskProxyURL(c.Telegram.ProxyURL),
			"send_preview_image": c.Telegram.SendPreviewImage,
		},
		"bark": map[string]any{
			"enabled":           c.Bark.Enabled,
			"configured":        c.BarkConfigured(),
			"server_url":        barkServer,
			"has_device_key":    hasDeviceKey,
			"masked_device_key": MaskSecret(c.Bark.DeviceKey),
			"group":             strings.TrimSpace(c.Bark.Group),
			"sound":             strings.TrimSpace(c.Bark.Sound),
			"level":             strings.TrimSpace(c.Bark.Level),
		},
		"email": map[string]any{
			"enabled":             c.Email.Enabled,
			"configured":          c.EmailConfigured(),
			"smtp_host":           strings.TrimSpace(c.Email.SMTPHost),
			"smtp_port":           smtpPort,
			"security":            security,
			"username":            strings.TrimSpace(c.Email.Username),
			"has_password":        hasPassword,
			"from_address":        strings.TrimSpace(c.Email.FromAddress),
			"from_name":           strings.TrimSpace(c.Email.FromName),
			"to_addresses":        strings.TrimSpace(c.Email.ToAddresses),
			"embed_preview_image": c.Email.EmbedPreviewImage,
		},
	}
}

// ParseBarkEndpoint parses either a full Bark push URL (e.g.
// "https://api.day.app/MyKey123", "https://example.com/bark/MyKey123", or
// "https://example.com/sub/path/MyKey123") or a Bark server/push URL
// (e.g. "https://bark.myhost.com", "https://example.com/bark/push") into
// normalized serverURL (preserving reverse-proxy subpaths) and optional deviceKey.
func ParseBarkEndpoint(raw string) (serverURL, deviceKey string, err error) {
	if strings.ContainsAny(raw, "\r\n\x00") {
		return "", "", errors.New("Bark 服务器地址不能包含换行或控制字符")
	}
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", "", nil
	}
	if err := colabOneRejectControlOrSpace(text, "Bark 服务器地址"); err != nil {
		return "", "", err
	}
	if !strings.Contains(text, "://") {
		return "", "", errors.New("Bark 地址请包含 http:// 或 https:// 协议前缀")
	}
	u, err := url.Parse(text)
	if err != nil {
		return "", "", errors.New("Bark 地址格式不合法")
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme != "http" && scheme != "https" {
		return "", "", errors.New("Bark 地址仅支持 http:// 或 https:// 协议")
	}
	if strings.TrimSpace(u.Host) == "" || strings.TrimSpace(u.Hostname()) == "" {
		return "", "", errors.New("Bark 地址缺少主机名或端口")
	}
	if strings.HasSuffix(u.Host, ":") {
		return "", "", errors.New("Bark 地址端口不能为空")
	}
	if u.User != nil {
		return "", "", errors.New("Bark 地址不应包含嵌入式认证信息")
	}
	if portStr := u.Port(); portStr != "" {
		p, pErr := strconv.Atoi(portStr)
		if pErr != nil || p < 1 || p > 65535 {
			return "", "", errors.New("Bark 地址端口必须是 1 至 65535 之间的整数")
		}
	}

	origin := scheme + "://" + u.Host
	trimmedPath := strings.Trim(u.Path, "/")
	if trimmedPath == "" {
		return origin, "", nil
	}

	rawSegments := strings.Split(trimmedPath, "/")
	var segments []string
	for _, seg := range rawSegments {
		seg = strings.TrimSpace(seg)
		if seg != "" {
			segments = append(segments, seg)
		}
	}
	if len(segments) == 0 {
		return origin, "", nil
	}

	if strings.EqualFold(segments[len(segments)-1], "push") {
		baseSegments := segments[:len(segments)-1]
		if len(baseSegments) == 0 {
			return origin, "", nil
		}
		return origin + "/" + strings.Join(baseSegments, "/"), "", nil
	}

	if len(segments) == 1 {
		key := segments[0]
		if err := colabOneRejectControlOrSpace(key, "Bark Device Key"); err != nil {
			return "", "", err
		}
		return origin, key, nil
	}

	key := segments[len(segments)-1]
	if err := colabOneRejectControlOrSpace(key, "Bark Device Key"); err != nil {
		return "", "", err
	}
	serverURL = origin + "/" + strings.Join(segments[:len(segments)-1], "/")
	return serverURL, key, nil
}

// ApplyPatch validates and applies a partial update map to cur, returning the updated
// configuration or a clear user-facing validation error without mutating cur.
func ApplyPatch(cur ColabOneNotificationConfig, payload map[string]any) (ColabOneNotificationConfig, error) {
	next := cur
	if strings.TrimSpace(next.Bark.ServerURL) == "" {
		next.Bark.ServerURL = COLABONE_DEFAULT_BARK_SERVER_URL
	}
	if next.Email.SMTPPort == 0 {
		next.Email.SMTPPort = 465
	}
	if strings.TrimSpace(next.Email.Security) == "" {
		next.Email.Security = "tls"
	}

	if payload == nil {
		return next, nil
	}

	if v, exists := payload["notify_gpu"]; exists {
		b, ok := v.(bool)
		if !ok {
			return cur, errors.New("notify_gpu 参数格式不合法")
		}
		next.NotifyGPU = b
	}
	if v, exists := payload["notify_task"]; exists {
		b, ok := v.(bool)
		if !ok {
			return cur, errors.New("notify_task 参数格式不合法")
		}
		next.NotifyTask = b
	}
	if v, exists := payload["notify_alert"]; exists {
		b, ok := v.(bool)
		if !ok {
			return cur, errors.New("notify_alert 参数格式不合法")
		}
		next.NotifyAlert = b
	}
	if v, exists := payload["system_only_when_unfocused"]; exists {
		b, ok := v.(bool)
		if !ok {
			return cur, errors.New("system_only_when_unfocused 参数格式不合法")
		}
		next.SystemOnlyWhenUnfocused = b
	}
	if v, exists := payload["remote_only_when_unfocused"]; exists {
		b, ok := v.(bool)
		if !ok {
			return cur, errors.New("remote_only_when_unfocused 参数格式不合法")
		}
		next.RemoteOnlyWhenUnfocused = b
	}

	if rawSys, exists := payload["system"]; exists && rawSys != nil {
		sysMap, ok := rawSys.(map[string]any)
		if !ok {
			return cur, errors.New("system 配置格式不合法")
		}
		clearFlag, _, err := colabOneRequireBoolField(sysMap, "clear")
		if err != nil {
			return cur, err
		}
		enabledVal, hasEnabled, err := colabOneRequireBoolField(sysMap, "enabled")
		if err != nil {
			return cur, err
		}
		if clearFlag && hasEnabled && enabledVal {
			return cur, errors.New("不能同时清除并启用系统桌面通知渠道")
		}
		if clearFlag {
			next.System = ColabOneSystemChannelConfig{Enabled: false}
		} else if hasEnabled {
			next.System.Enabled = enabledVal
		}
	}

	tgTouched := false
	if rawTG, exists := payload["telegram"]; exists && rawTG != nil {
		tgMap, ok := rawTG.(map[string]any)
		if !ok {
			return cur, errors.New("telegram 配置格式不合法")
		}
		tgTouched = true
		if err := colabOneApplyTelegramPatch(&next.Telegram, tgMap); err != nil {
			return cur, err
		}
	}

	barkTouched := false
	if rawBark, exists := payload["bark"]; exists && rawBark != nil {
		barkMap, ok := rawBark.(map[string]any)
		if !ok {
			return cur, errors.New("bark 配置格式不合法")
		}
		barkTouched = true
		if err := colabOneApplyBarkPatch(&next.Bark, barkMap); err != nil {
			return cur, err
		}
	}

	emailTouched := false
	if rawEmail, exists := payload["email"]; exists && rawEmail != nil {
		emailMap, ok := rawEmail.(map[string]any)
		if !ok {
			return cur, errors.New("email 配置格式不合法")
		}
		emailTouched = true
		if err := colabOneApplyEmailPatch(&next.Email, emailMap); err != nil {
			return cur, err
		}
	}

	if (tgTouched || (!cur.Telegram.Enabled && next.Telegram.Enabled)) && next.Telegram.Enabled && !next.TelegramConfigured() {
		return cur, errors.New("请先填写 Telegram Bot Token 和 Chat ID")
	}
	if (barkTouched || (!cur.Bark.Enabled && next.Bark.Enabled)) && next.Bark.Enabled && !next.BarkConfigured() {
		return cur, errors.New("请先填写 Bark 推送地址或 Device Key")
	}
	if (emailTouched || (!cur.Email.Enabled && next.Email.Enabled)) && next.Email.Enabled && !next.EmailConfigured() {
		return cur, errors.New("请先完善邮件 SMTP 服务器、发件人账号与收件人邮箱")
	}

	return next, nil
}

func colabOneApplyTelegramPatch(dst *ColabOneTelegramChannelConfig, m map[string]any) error {
	clearFlag, _, err := colabOneRequireBoolField(m, "clear")
	if err != nil {
		return err
	}
	enabledVal, hasEnabled, err := colabOneRequireBoolField(m, "enabled")
	if err != nil {
		return err
	}
	if clearFlag && hasEnabled && enabledVal {
		return errors.New("不能同时清除并启用 Telegram 通知渠道")
	}
	if clearFlag {
		*dst = ColabOneTelegramChannelConfig{
			SendPreviewImage: true,
		}
		return nil
	}

	clearToken, _, err := colabOneRequireBoolField(m, "clear_bot_token")
	if err != nil {
		return err
	}
	rawBotToken, hasBotToken, err := colabOneRequireStringField(m, "bot_token")
	if err != nil {
		return err
	}
	rawChatID, hasChatID, err := colabOneRequireStringField(m, "chat_id")
	if err != nil {
		return err
	}
	rawAPIBase, hasAPIBase, err := colabOneRequireStringField(m, "api_base_url")
	if err != nil {
		return err
	}
	clearProxy, _, err := colabOneRequireBoolField(m, "clear_proxy")
	if err != nil {
		return err
	}
	rawProxyURL, hasProxyURL, err := colabOneRequireStringField(m, "proxy_url")
	if err != nil {
		return err
	}
	sendPreviewVal, hasSendPreview, err := colabOneRequireBoolField(m, "send_preview_image")
	if err != nil {
		return err
	}

	if clearToken {
		dst.BotToken = ""
	}
	if hasBotToken {
		if strings.ContainsAny(rawBotToken, "\r\n\x00") {
			return errors.New("Telegram Bot Token 不能包含换行或控制字符")
		}
		trimmed := strings.TrimSpace(rawBotToken)
		if trimmed != "" {
			if err := colabOneValidateTelegramBotToken(trimmed); err != nil {
				return err
			}
			dst.BotToken = trimmed
		}
	}

	if hasChatID {
		if strings.ContainsAny(rawChatID, "\r\n\x00") {
			return errors.New("Telegram Chat ID 不能包含换行或控制字符")
		}
		trimmed := strings.TrimSpace(rawChatID)
		if trimmed != "" {
			if err := colabOneRejectControlOrSpace(trimmed, "Telegram Chat ID"); err != nil {
				return err
			}
		}
		dst.ChatID = trimmed
	}

	if hasAPIBase {
		if strings.ContainsAny(rawAPIBase, "\r\n\x00") {
			return errors.New("Telegram API 地址不能包含换行或控制字符")
		}
		trimmed := strings.TrimSpace(rawAPIBase)
		if trimmed == "" {
			dst.APIBaseURL = ""
		} else {
			cleanURL, err := colabOneValidateHTTPBaseURL(trimmed, "Telegram API 地址")
			if err != nil {
				return err
			}
			dst.APIBaseURL = cleanURL
		}
	}

	if clearProxy {
		dst.ProxyURL = ""
	}
	if hasProxyURL {
		if strings.ContainsAny(rawProxyURL, "\r\n\x00") {
			return errors.New("代理地址不能包含换行或控制字符")
		}
		trimmed := strings.TrimSpace(rawProxyURL)
		if trimmed != "" {
			norm, err := assignproxy.NormalizeAssignProxy(trimmed)
			if err != nil {
				return err
			}
			dst.ProxyURL = norm
		}
	}

	if hasSendPreview {
		dst.SendPreviewImage = sendPreviewVal
	}
	if hasEnabled {
		dst.Enabled = enabledVal
	}
	return nil
}

func colabOneApplyBarkPatch(dst *ColabOneBarkChannelConfig, m map[string]any) error {
	clearFlag, _, err := colabOneRequireBoolField(m, "clear")
	if err != nil {
		return err
	}
	enabledVal, hasEnabled, err := colabOneRequireBoolField(m, "enabled")
	if err != nil {
		return err
	}
	if clearFlag && hasEnabled && enabledVal {
		return errors.New("不能同时清除并启用 Bark 通知渠道")
	}
	if clearFlag {
		*dst = ColabOneBarkChannelConfig{
			ServerURL: COLABONE_DEFAULT_BARK_SERVER_URL,
			Group:     "ColabOne",
		}
		return nil
	}

	clearKey, _, err := colabOneRequireBoolField(m, "clear_device_key")
	if err != nil {
		return err
	}
	rawEndpointOrig, hasExplicitEndpoint, err := colabOneRequireStringField(m, "endpoint_url")
	if err != nil {
		return err
	}
	rawServerOrig, hasServerKey, err := colabOneRequireStringField(m, "server_url")
	if err != nil {
		return err
	}
	rawDeviceKeyOrig, hasDeviceKey, err := colabOneRequireStringField(m, "device_key")
	if err != nil {
		return err
	}
	rawGroupOrig, hasGroup, err := colabOneRequireStringField(m, "group")
	if err != nil {
		return err
	}
	rawSoundOrig, hasSound, err := colabOneRequireStringField(m, "sound")
	if err != nil {
		return err
	}
	rawLevelOrig, hasLevel, err := colabOneRequireStringField(m, "level")
	if err != nil {
		return err
	}

	if hasExplicitEndpoint && strings.ContainsAny(rawEndpointOrig, "\r\n\x00") {
		return errors.New("Bark 服务器地址不能包含换行或控制字符")
	}
	if hasServerKey && strings.ContainsAny(rawServerOrig, "\r\n\x00") {
		return errors.New("Bark 服务器地址不能包含换行或控制字符")
	}
	if hasDeviceKey && strings.ContainsAny(rawDeviceKeyOrig, "\r\n\x00") {
		return errors.New("Bark Device Key 不能包含换行或控制字符")
	}

	rawEndpoint := strings.TrimSpace(rawEndpointOrig)
	rawServer := strings.TrimSpace(rawServerOrig)
	rawDeviceKey := strings.TrimSpace(rawDeviceKeyOrig)

	// Clear existing device key BEFORE processing endpoint_url / server_url / device_key.
	if clearKey {
		dst.DeviceKey = ""
	}

	endpointUsed := false
	if hasExplicitEndpoint && rawEndpoint != "" {
		srv, key, err := ParseBarkEndpoint(rawEndpointOrig)
		if err != nil {
			return err
		}
		if srv != "" {
			dst.ServerURL = srv
		}
		if key != "" {
			dst.DeviceKey = key
		}
		endpointUsed = true
	}

	if hasServerKey {
		if rawServer == "" {
			if !endpointUsed {
				dst.ServerURL = COLABONE_DEFAULT_BARK_SERVER_URL
			}
		} else if (hasDeviceKey && rawDeviceKey != "") || (dst.DeviceKey != "" && !endpointUsed) {
			cleanServer, err := colabOneNormalizeBarkBaseURL(rawServerOrig)
			if err != nil {
				return err
			}
			dst.ServerURL = cleanServer
		} else {
			srv, key, err := ParseBarkEndpoint(rawServerOrig)
			if err != nil {
				return err
			}
			dst.ServerURL = srv
			if key != "" {
				dst.DeviceKey = key
			}
		}
	}

	if hasDeviceKey && rawDeviceKey != "" {
		if strings.Contains(rawDeviceKey, "://") {
			srv, key, err := ParseBarkEndpoint(rawDeviceKeyOrig)
			if err != nil {
				return err
			}
			if key == "" {
				return errors.New("Bark 推送地址中未包含有效的 Device Key")
			}
			dst.DeviceKey = key
			if srv != "" && (!hasServerKey || rawServer == "") && !endpointUsed {
				dst.ServerURL = srv
			}
		} else {
			if err := colabOneRejectControlOrSpace(rawDeviceKey, "Bark Device Key"); err != nil {
				return err
			}
			if strings.ContainsRune(rawDeviceKey, '/') {
				return errors.New("Bark Device Key 不能包含斜杠，若需填写完整地址请包含 https:// 前缀")
			}
			dst.DeviceKey = rawDeviceKey
		}
	}

	if hasGroup {
		if strings.ContainsAny(rawGroupOrig, "\r\n\x00") {
			return errors.New("Bark 分组名称不能包含换行或控制字符")
		}
		trimmed := strings.TrimSpace(rawGroupOrig)
		if err := colabOneRejectControlChars(trimmed, "Bark 分组名称"); err != nil {
			return err
		}
		dst.Group = trimmed
	}
	if hasSound {
		if strings.ContainsAny(rawSoundOrig, "\r\n\x00") {
			return errors.New("Bark 提示音不能包含换行或控制字符")
		}
		trimmed := strings.TrimSpace(rawSoundOrig)
		if err := colabOneRejectControlChars(trimmed, "Bark 提示音"); err != nil {
			return err
		}
		dst.Sound = trimmed
	}
	if hasLevel {
		if strings.ContainsAny(rawLevelOrig, "\r\n\x00") {
			return errors.New("Bark 通知级别不能包含换行或控制字符")
		}
		trimmed := strings.TrimSpace(rawLevelOrig)
		if trimmed != "" {
			switch trimmed {
			case "critical", "active", "timeSensitive", "passive":
			default:
				return errors.New("Bark 通知级别仅支持 active、timeSensitive、passive 或 critical")
			}
		}
		dst.Level = trimmed
	}
	if hasEnabled {
		dst.Enabled = enabledVal
	}

	if strings.TrimSpace(dst.ServerURL) == "" {
		dst.ServerURL = COLABONE_DEFAULT_BARK_SERVER_URL
	}
	return nil
}

func colabOneApplyEmailPatch(dst *ColabOneEmailChannelConfig, m map[string]any) error {
	clearFlag, _, err := colabOneRequireBoolField(m, "clear")
	if err != nil {
		return err
	}
	enabledVal, hasEnabled, err := colabOneRequireBoolField(m, "enabled")
	if err != nil {
		return err
	}
	if clearFlag && hasEnabled && enabledVal {
		return errors.New("不能同时清除并启用邮件通知渠道")
	}
	if clearFlag {
		*dst = ColabOneEmailChannelConfig{
			SMTPPort:          465,
			Security:          "tls",
			FromName:          "ColabOne",
			EmbedPreviewImage: true,
		}
		return nil
	}

	rawHost, hasHost, err := colabOneRequireStringField(m, "smtp_host")
	if err != nil {
		return err
	}
	rawSec, hasSec, err := colabOneRequireStringField(m, "security")
	if err != nil {
		return err
	}
	rawUser, hasUser, err := colabOneRequireStringField(m, "username")
	if err != nil {
		return err
	}
	clearPass, _, err := colabOneRequireBoolField(m, "clear_password")
	if err != nil {
		return err
	}
	rawPass, hasPass, err := colabOneRequireStringField(m, "password")
	if err != nil {
		return err
	}
	rawFromAddr, hasFromAddr, err := colabOneRequireStringField(m, "from_address")
	if err != nil {
		return err
	}
	rawFromName, hasFromName, err := colabOneRequireStringField(m, "from_name")
	if err != nil {
		return err
	}
	rawToAddrs, hasToAddrs, err := colabOneRequireStringField(m, "to_addresses")
	if err != nil {
		return err
	}
	embedVal, hasEmbed, err := colabOneRequireBoolField(m, "embed_preview_image")
	if err != nil {
		return err
	}

	if hasHost {
		cleanHost, err := colabOneNormalizeSMTPHost(rawHost)
		if err != nil {
			return err
		}
		dst.SMTPHost = cleanHost
	}

	if _, exists := m["smtp_port"]; exists {
		port, ok := colabOneReadInt(m, "smtp_port")
		if !ok || port < 1 || port > 65535 {
			return errors.New("SMTP 端口必须是 1 至 65535 之间的整数")
		}
		dst.SMTPPort = port
	}

	if hasSec {
		sec := strings.ToLower(strings.TrimSpace(rawSec))
		if !colabOneValidEmailSecurity(sec) {
			return errors.New("SMTP 加密方式仅支持 tls、starttls 或 none")
		}
		dst.Security = sec
	}

	if hasUser {
		if strings.ContainsAny(rawUser, "\r\n\x00") {
			return errors.New("SMTP 账号不能包含换行或控制字符")
		}
		trimmed := strings.TrimSpace(rawUser)
		if err := colabOneRejectControlChars(trimmed, "SMTP 账号"); err != nil {
			return err
		}
		dst.Username = trimmed
	}

	if clearPass {
		dst.Password = ""
	}
	if hasPass && rawPass != "" {
		if strings.ContainsAny(rawPass, "\r\n\x00") {
			return errors.New("SMTP 密码不能包含换行或控制字符")
		}
		dst.Password = rawPass
	}

	if hasFromAddr {
		if strings.ContainsAny(rawFromAddr, "\r\n\x00") {
			return errors.New("发件人邮箱不能包含换行或控制字符")
		}
		trimmed := strings.TrimSpace(rawFromAddr)
		if trimmed != "" {
			addr, err := colabOneValidateEmailAddress(trimmed)
			if err != nil {
				return fmt.Errorf("发件人邮箱格式不合法：%w", err)
			}
			trimmed = addr
		}
		dst.FromAddress = trimmed
	}

	if hasFromName {
		if strings.ContainsAny(rawFromName, "\r\n\x00") {
			return errors.New("发件人名称不能包含换行或控制字符")
		}
		trimmed := strings.TrimSpace(rawFromName)
		if err := colabOneRejectControlChars(trimmed, "发件人名称"); err != nil {
			return err
		}
		dst.FromName = trimmed
	}

	if hasToAddrs {
		if strings.ContainsAny(rawToAddrs, "\r\n\x00") {
			return errors.New("收件人邮箱不能包含换行或控制字符")
		}
		trimmed := strings.TrimSpace(rawToAddrs)
		if trimmed == "" {
			dst.ToAddresses = ""
		} else {
			list, err := colabOneParseEmailRecipients(trimmed)
			if err != nil {
				return err
			}
			dst.ToAddresses = strings.Join(list, ", ")
		}
	}

	if hasEmbed {
		dst.EmbedPreviewImage = embedVal
	}
	if hasEnabled {
		dst.Enabled = enabledVal
	}
	return nil
}

// MaskSecret masks a sensitive credential string for safe display in UI and logs.
func MaskSecret(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}
	runes := []rune(trimmed)
	n := len(runes)
	if n <= 8 {
		return "****"
	}
	if n <= 16 {
		return string(runes[:2]) + "****" + string(runes[n-2:])
	}
	return string(runes[:4]) + "****" + string(runes[n-4:])
}

var (
	colabOneReTelegramBotPath = regexp.MustCompile(`(?i)bot[0-9]{5,}(?::|%3[aA])[A-Za-z0-9_-]{8,}`)
	colabOneReURLUserInfo     = regexp.MustCompile(`(?i)\b((?:https?|socks5h?)://)([^\s/@"'<>]+@)`)
	colabOneReBasicAuth       = regexp.MustCompile(`(?i)\bBasic\s+[A-Za-z0-9+/=]{4,}`)
)

// RedactSecrets removes any configured channel secrets, proxy credentials, or
// recognizable Telegram bot token patterns from msg.
func RedactSecrets(msg string, cfg ColabOneNotificationConfig) string {
	if msg == "" {
		return ""
	}
	out := msg

	type replacement struct {
		old string
		new string
	}
	var reps []replacement
	addSecret := func(raw string, masked string) {
		if raw == "" {
			return
		}
		if masked == "" {
			masked = "[REDACTED]"
		}
		reps = append(reps, replacement{old: raw, new: masked})
		if escaped := url.QueryEscape(raw); escaped != "" && escaped != raw {
			reps = append(reps, replacement{old: escaped, new: masked})
		}
		if escaped := url.PathEscape(raw); escaped != "" && escaped != raw {
			reps = append(reps, replacement{old: escaped, new: masked})
		}
	}

	if tok := strings.TrimSpace(cfg.Telegram.BotToken); tok != "" {
		addSecret("bot"+tok, "bot[REDACTED]")
		addSecret(tok, "[REDACTED]")
	}
	if proxyRaw := strings.TrimSpace(cfg.Telegram.ProxyURL); proxyRaw != "" {
		maskedProxy := colabOneMaskProxyURL(proxyRaw)
		if maskedProxy == "" {
			maskedProxy = "[REDACTED]"
		}
		addSecret(proxyRaw, maskedProxy)
		if spec, err := assignproxy.ParseAssignProxy(proxyRaw); err == nil && spec != nil {
			if spec.NormalizedURL != "" && spec.NormalizedURL != proxyRaw {
				addSecret(spec.NormalizedURL, spec.MaskedURL)
			}
			if spec.Password != "" {
				addSecret(spec.Password, "[REDACTED]")
			}
			if len(spec.Username) >= 4 {
				addSecret(spec.Username, "[REDACTED]")
			}
			if spec.HasAuth() {
				b64Proxy := base64.StdEncoding.EncodeToString([]byte(spec.Username + ":" + spec.Password))
				if b64Proxy != "" {
					addSecret(b64Proxy, "[REDACTED]")
				}
			}
		}
	}
	if key := strings.TrimSpace(cfg.Bark.DeviceKey); key != "" {
		addSecret(key, "[REDACTED]")
	}
	if cfg.Email.Password != "" {
		addSecret(cfg.Email.Password, "[REDACTED]")
		if b64Pass := base64.StdEncoding.EncodeToString([]byte(cfg.Email.Password)); b64Pass != "" {
			addSecret(b64Pass, "[REDACTED]")
		}
		if b64Plain := base64.StdEncoding.EncodeToString([]byte("\x00" + cfg.Email.Username + "\x00" + cfg.Email.Password)); b64Plain != "" {
			addSecret(b64Plain, "[REDACTED]")
		}
		if cfg.Email.Username != "" {
			if b64PlainAuthzid := base64.StdEncoding.EncodeToString([]byte(cfg.Email.Username + "\x00" + cfg.Email.Username + "\x00" + cfg.Email.Password)); b64PlainAuthzid != "" {
				addSecret(b64PlainAuthzid, "[REDACTED]")
			}
		}
	}

	// Apply longest secrets first so full URLs/prefixed tokens are redacted cleanly.
	sort.SliceStable(reps, func(i, j int) bool {
		return len(reps[i].old) > len(reps[j].old)
	})
	for _, r := range reps {
		out = strings.ReplaceAll(out, r.old, r.new)
	}

	out = colabOneReTelegramBotPath.ReplaceAllString(out, "bot[REDACTED]")
	out = colabOneReURLUserInfo.ReplaceAllString(out, "${1}[REDACTED]@")
	out = colabOneReBasicAuth.ReplaceAllString(out, "Basic [REDACTED]")
	return out
}

func colabOneMaskProxyURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if masked := assignproxy.MaskAssignProxy(trimmed); masked != "" {
		return masked
	}
	return "[REDACTED]"
}

func colabOneEffectiveBarkServerURL(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return COLABONE_DEFAULT_BARK_SERVER_URL
	}
	return trimmed
}

func colabOneEffectiveTelegramAPIBase(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return COLABONE_DEFAULT_TELEGRAM_API_BASE
	}
	return trimmed
}

func colabOneValidEmailSecurity(sec string) bool {
	switch strings.ToLower(strings.TrimSpace(sec)) {
	case "tls", "starttls", "none":
		return true
	default:
		return false
	}
}

func colabOneEffectiveEmailFrom(cfg ColabOneEmailChannelConfig) string {
	if from := strings.TrimSpace(cfg.FromAddress); from != "" {
		if addr, err := colabOneValidateEmailAddress(from); err == nil {
			return addr
		}
		return ""
	}
	if user := strings.TrimSpace(cfg.Username); user != "" {
		if addr, err := colabOneValidateEmailAddress(user); err == nil {
			return addr
		}
	}
	return ""
}

func colabOneValidateTelegramBotToken(token string) error {
	if err := colabOneRejectControlOrSpace(token, "Telegram Bot Token"); err != nil {
		return err
	}
	if strings.ContainsAny(token, "/?#@\\") {
		return errors.New("Telegram Bot Token 不能包含 /、?、#、@ 或 \\ 字符")
	}
	return nil
}

func colabOneNormalizeBarkBaseURL(raw string) (string, error) {
	clean, err := colabOneValidateHTTPBaseURL(raw, "Bark 服务器地址")
	if err != nil || clean == "" {
		return clean, err
	}
	u, err := url.Parse(clean)
	if err != nil {
		return "", errors.New("Bark 服务器地址格式不合法")
	}
	trimmedPath := strings.Trim(u.Path, "/")
	if trimmedPath == "" {
		return clean, nil
	}
	rawSegments := strings.Split(trimmedPath, "/")
	var segments []string
	for _, seg := range rawSegments {
		seg = strings.TrimSpace(seg)
		if seg != "" {
			segments = append(segments, seg)
		}
	}
	if len(segments) > 0 && strings.EqualFold(segments[len(segments)-1], "push") {
		segments = segments[:len(segments)-1]
	}
	origin := u.Scheme + "://" + u.Host
	if len(segments) == 0 {
		return origin, nil
	}
	return origin + "/" + strings.Join(segments, "/"), nil
}

func colabOneNormalizeSMTPHost(raw string) (string, error) {
	if strings.ContainsAny(raw, "\r\n\x00") {
		return "", errors.New("SMTP 服务器地址不能包含换行或控制字符")
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	if err := colabOneRejectControlOrSpace(trimmed, "SMTP 服务器地址"); err != nil {
		return "", err
	}
	if strings.Contains(trimmed, "://") {
		return "", errors.New("SMTP 服务器地址不应包含协议前缀")
	}
	if strings.ContainsAny(trimmed, "/?#@\\") {
		return "", errors.New("SMTP 服务器地址格式不合法，不应包含路径或特殊字符")
	}
	if strings.HasPrefix(trimmed, "[") {
		if !strings.HasSuffix(trimmed, "]") {
			return "", errors.New("SMTP 服务器地址格式不合法")
		}
		inner := trimmed[1 : len(trimmed)-1]
		ip, err := netip.ParseAddr(inner)
		if err != nil || !ip.Is6() || ip.Zone() != "" {
			return "", errors.New("SMTP 服务器 IPv6 地址格式不合法")
		}
		return inner, nil
	}
	if strings.ContainsAny(trimmed, "[]") {
		return "", errors.New("SMTP 服务器地址格式不合法")
	}
	if ip, err := netip.ParseAddr(trimmed); err == nil {
		if ip.Zone() != "" {
			return "", errors.New("SMTP 服务器 IP 地址不支持区域标识符")
		}
		return trimmed, nil
	}
	if strings.ContainsRune(trimmed, ':') {
		return "", errors.New("SMTP 服务器地址不应包含端口，请在端口栏填写端口号")
	}
	hostNoTrail := strings.TrimSuffix(trimmed, ".")
	if hostNoTrail == "" || len(hostNoTrail) > 253 || strings.HasPrefix(hostNoTrail, ".") || strings.Contains(hostNoTrail, "..") {
		return "", errors.New("SMTP 服务器主机名格式不合法")
	}
	for _, lbl := range strings.Split(hostNoTrail, ".") {
		if len(lbl) == 0 || len(lbl) > 63 || lbl[0] == '-' || lbl[len(lbl)-1] == '-' {
			return "", errors.New("SMTP 服务器主机名格式不合法")
		}
		for i := 0; i < len(lbl); i++ {
			ch := lbl[i]
			if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-') {
				return "", errors.New("SMTP 服务器主机名格式不合法")
			}
		}
	}
	return trimmed, nil
}

func colabOneValidateHTTPBaseURL(raw string, label string) (string, error) {
	if strings.ContainsAny(raw, "\r\n\x00") {
		return "", fmt.Errorf("%s不能包含换行或控制字符", label)
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	if err := colabOneRejectControlOrSpace(trimmed, label); err != nil {
		return "", err
	}
	if !strings.Contains(trimmed, "://") {
		return "", fmt.Errorf("%s请包含 http:// 或 https:// 协议前缀", label)
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("%s格式不合法", label)
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("%s仅支持 http:// 或 https:// 协议", label)
	}
	if strings.TrimSpace(u.Host) == "" || strings.TrimSpace(u.Hostname()) == "" {
		return "", fmt.Errorf("%s缺少有效主机名", label)
	}
	if strings.HasSuffix(u.Host, ":") {
		return "", fmt.Errorf("%s端口不能为空", label)
	}
	if u.User != nil {
		return "", fmt.Errorf("%s不应包含嵌入式认证信息", label)
	}
	if portStr := u.Port(); portStr != "" {
		p, pErr := strconv.Atoi(portStr)
		if pErr != nil || p < 1 || p > 65535 {
			return "", fmt.Errorf("%s端口必须是 1 至 65535 之间的整数", label)
		}
	}
	u.Scheme = scheme
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/"), nil
}

func colabOneRejectControlOrSpace(s string, label string) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("%s包含非法字符", label)
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("%s不能包含空格或控制字符", label)
		}
	}
	return nil
}

func colabOneRejectControlChars(s string, label string) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("%s包含非法字符", label)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s不能包含换行或控制字符", label)
		}
	}
	return nil
}

func colabOneValidateEmailAddress(raw string) (string, error) {
	if strings.ContainsAny(raw, "\r\n\x00") {
		return "", errors.New("不能包含换行或控制字符")
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("邮箱地址不能为空")
	}
	for _, r := range trimmed {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", errors.New("邮箱地址不能包含空格或控制字符")
		}
	}
	parsed, err := mail.ParseAddress(trimmed)
	if err != nil || parsed == nil || parsed.Address != trimmed {
		return "", errors.New("请输入有效的邮箱地址")
	}
	parts := strings.Split(trimmed, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", errors.New("请输入有效的邮箱地址")
	}
	domain := parts[1]
	if !strings.Contains(domain, ".") ||
		strings.HasPrefix(domain, ".") ||
		strings.HasSuffix(domain, ".") ||
		strings.Contains(domain, "..") {
		return "", errors.New("邮箱域名格式不合法")
	}
	return trimmed, nil
}

func colabOneParseEmailRecipients(raw string) ([]string, error) {
	if strings.ContainsAny(raw, "\r\n\x00") {
		return nil, errors.New("收件人邮箱不能包含换行或控制字符")
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	normalized := strings.NewReplacer(";", ",", "，", ",", "；", ",").Replace(trimmed)
	tokens := strings.Split(normalized, ",")
	seen := make(map[string]struct{}, len(tokens))
	out := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		candidate := strings.TrimSpace(tok)
		if candidate == "" {
			continue
		}
		addr, err := colabOneValidateEmailAddress(candidate)
		if err != nil {
			return nil, fmt.Errorf("收件人邮箱格式不合法 (%s)：%w", candidate, err)
		}
		key := strings.ToLower(addr)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, addr)
	}
	if len(out) == 0 {
		return nil, errors.New("请至少填写一个有效的收件人邮箱")
	}
	return out, nil
}

func colabOneRequireBoolField(m map[string]any, key string) (bool, bool, error) {
	v, exists := m[key]
	if !exists {
		return false, false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, true, fmt.Errorf("%s 参数格式不合法", key)
	}
	return b, true, nil
}

func colabOneRequireStringField(m map[string]any, key string) (string, bool, error) {
	v, exists := m[key]
	if !exists {
		return "", false, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", true, fmt.Errorf("%s 参数格式不合法", key)
	}
	return s, true, nil
}

func colabOneReadBool(m map[string]any, key string) (bool, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

func colabOneReadString(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func colabOneReadMap(m map[string]any, key string) (map[string]any, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return nil, false
	}
	sub, ok := v.(map[string]any)
	return sub, ok
}

func colabOneReadInt(m map[string]any, key string) (int, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		if n != float64(int(n)) {
			return 0, false
		}
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return 0, false
		}
		return i, true
	default:
		return 0, false
	}
}
