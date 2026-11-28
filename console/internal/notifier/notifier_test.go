package notifier

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"math/big"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/luolongfei/ColabOne/console/internal/outboundlog"
)

func makeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x * 255) / max(1, w)),
				G: uint8((y * 255) / max(1, h)),
				B: uint8(((x + y) * 127) / max(1, w+h)),
				A: 220,
			})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode failed: %v", err)
	}
	return buf.Bytes()
}

func makeTestJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8((x*7 + y*3) % 256),
				G: uint8((x*5 + y*11) % 256),
				B: uint8((x*13 + y*17) % 256),
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("jpeg.Encode failed: %v", err)
	}
	return buf.Bytes()
}

func makeSelfSignedLoopbackTLS(t *testing.T) (serverCfg *tls.Config, clientCfg *tls.Config) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("x509.CreateCertificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("x509.ParseCertificate: %v", err)
	}
	tlsCert := tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  priv,
		Leaf:        cert,
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		MinVersion:   tls.VersionTLS12,
	}, &tls.Config{
		RootCAs:    pool,
		ServerName: "127.0.0.1",
		MinVersion: tls.VersionTLS12,
	}
}

func TestConfigDefaultsPatchRedactionAndRoundTrip(t *testing.T) {
	def := DefaultConfig()
	if !def.NotifyGPU || !def.NotifyTask || !def.NotifyAlert {
		t.Fatalf("expected all notification categories enabled by default: %+v", def)
	}
	if !def.SystemOnlyWhenUnfocused || def.RemoteOnlyWhenUnfocused {
		t.Fatalf("unexpected focus defaults: sys=%v remote=%v", def.SystemOnlyWhenUnfocused, def.RemoteOnlyWhenUnfocused)
	}
	if !def.System.Enabled || !def.Telegram.SendPreviewImage {
		t.Fatalf("unexpected system/telegram defaults")
	}
	if def.Bark.ServerURL != COLABONE_DEFAULT_BARK_SERVER_URL || def.Bark.Group != "ColabOne" {
		t.Fatalf("unexpected bark defaults: %+v", def.Bark)
	}
	if def.Email.SMTPPort != 465 || def.Email.Security != "tls" || def.Email.FromName != "ColabOne" || !def.Email.EmbedPreviewImage {
		t.Fatalf("unexpected email defaults: %+v", def.Email)
	}

	// Enabling channels without required fields must fail with clear Chinese validation messages.
	if _, err := ApplyPatch(def, map[string]any{"telegram": map[string]any{"enabled": true}}); err == nil || !strings.Contains(err.Error(), "Telegram Bot Token") {
		t.Fatalf("expected Telegram validation error, got: %v", err)
	}
	if _, err := ApplyPatch(def, map[string]any{"bark": map[string]any{"enabled": true}}); err == nil || !strings.Contains(err.Error(), "Bark") {
		t.Fatalf("expected Bark validation error, got: %v", err)
	}
	if _, err := ApplyPatch(def, map[string]any{"email": map[string]any{"enabled": true}}); err == nil || !strings.Contains(err.Error(), "SMTP") {
		t.Fatalf("expected Email validation error, got: %v", err)
	}

	const (
		secretBotToken  = "987654321:AAH_SuperSecretBotToken_987654321"
		secretProxyPass = "ProxyPassSuperSecret99"
		secretProxyURL  = "http://alice:" + secretProxyPass + "@127.0.0.1:7890"
		secretBarkKey   = "BarkSuperSecretDeviceKey778899"
		secretEmailPass = "EmailAppPasswordSecret5566"
	)

	cfg, err := ApplyPatch(def, map[string]any{
		"notify_gpu":                 true,
		"notify_task":                false,
		"notify_alert":               true,
		"system_only_when_unfocused": false,
		"remote_only_when_unfocused": true,
		"telegram": map[string]any{
			"enabled":            true,
			"bot_token":          secretBotToken,
			"chat_id":            "-100123456789",
			"api_base_url":       "https://tg-relay.example.com/",
			"proxy_url":          secretProxyURL,
			"send_preview_image": true,
		},
		"bark": map[string]any{
			"enabled":      true,
			"endpoint_url": "https://bark.myhost.com/bark/" + secretBarkKey + "/",
			"group":        "GPU-Alerts",
			"sound":        "minuet",
			"level":        "timeSensitive",
		},
		"email": map[string]any{
			"enabled":             true,
			"smtp_host":           "smtp.example.com",
			"smtp_port":           float64(587),
			"security":            "starttls",
			"username":            "alerts@example.com",
			"password":            secretEmailPass,
			"from_address":        "alerts@example.com",
			"from_name":           "ColabOne Alerts",
			"to_addresses":        "dev1@example.com; dev2@example.com, DEV1@example.com",
			"embed_preview_image": true,
		},
	})
	if err != nil {
		t.Fatalf("ApplyPatch failed unexpectedly: %v", err)
	}

	if cfg.Telegram.APIBaseURL != "https://tg-relay.example.com" {
		t.Fatalf("expected trimmed api_base_url, got %q", cfg.Telegram.APIBaseURL)
	}
	if cfg.Bark.ServerURL != "https://bark.myhost.com/bark" || cfg.Bark.DeviceKey != secretBarkKey {
		t.Fatalf("unexpected bark endpoint parse: server=%q key_set=%v", cfg.Bark.ServerURL, cfg.Bark.DeviceKey == secretBarkKey)
	}
	if cfg.Email.ToAddresses != "dev1@example.com, dev2@example.com" {
		t.Fatalf("expected normalized deduplicated recipients, got %q", cfg.Email.ToAddresses)
	}

	// Verify ZERO secrets leak across String(), GoString(), JSON, PublicSettingsMap, and PublicView.
	allSecrets := []string{secretBotToken, secretProxyPass, secretProxyURL, secretBarkKey, secretEmailPass}
	checkNoLeaks := func(label, text string) {
		t.Helper()
		for _, sec := range allSecrets {
			if strings.Contains(text, sec) {
				t.Fatalf("%s leaked secret %q in: %s", label, sec, text)
			}
		}
	}

	checkNoLeaks("String()", cfg.String())
	checkNoLeaks("GoString()", cfg.GoString())
	checkNoLeaks("fmt %v", fmt.Sprintf("%v", cfg))
	checkNoLeaks("fmt %+v", fmt.Sprintf("%+v", cfg))
	checkNoLeaks("fmt %#v", fmt.Sprintf("%#v", cfg))

	rawCfgJSON, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal(cfg): %v", err)
	}
	checkNoLeaks("json.Marshal(cfg)", string(rawCfgJSON))

	pubSettings := cfg.PublicSettingsMap()
	pubSettingsJSON, err := json.Marshal(pubSettings)
	if err != nil {
		t.Fatalf("json.Marshal(PublicSettingsMap): %v", err)
	}
	checkNoLeaks("PublicSettingsMap", string(pubSettingsJSON))

	pubView := cfg.PublicView(true)
	pubViewJSON, err := json.Marshal(pubView)
	if err != nil {
		t.Fatalf("json.Marshal(PublicView): %v", err)
	}
	checkNoLeaks("PublicView", string(pubViewJSON))

	// Check exact PublicView shape keys.
	tgView := pubView["telegram"].(map[string]any)
	if tgView["has_bot_token"] != true || tgView["configured"] != true || tgView["has_proxy"] != true {
		t.Fatalf("unexpected telegram public view: %+v", tgView)
	}
	if tgView["masked_proxy_url"] != "http://127.0.0.1:7890" {
		t.Fatalf("expected credential-free masked_proxy_url, got %v", tgView["masked_proxy_url"])
	}
	if !strings.Contains(tgView["masked_bot_token"].(string), "****") {
		t.Fatalf("expected masked bot token, got %v", tgView["masked_bot_token"])
	}
	barkView := pubView["bark"].(map[string]any)
	if barkView["has_device_key"] != true || barkView["configured"] != true {
		t.Fatalf("unexpected bark public view: %+v", barkView)
	}
	emailView := pubView["email"].(map[string]any)
	if emailView["has_password"] != true || emailView["configured"] != true {
		t.Fatalf("unexpected email public view: %+v", emailView)
	}
	sysView := pubView["system"].(map[string]any)
	if sysView["supported"] != true || sysView["enabled"] != true {
		t.Fatalf("unexpected system public view: %+v", sysView)
	}

	// Verify PublicSettingsMap + SecretsMap round-trip via LoadConfigFromMaps.
	secretsMap := cfg.SecretsMap()
	if len(secretsMap) != 4 {
		t.Fatalf("expected 4 secrets in SecretsMap, got %d", len(secretsMap))
	}
	restored := LoadConfigFromMaps(pubSettings, secretsMap)
	if restored != cfg {
		t.Fatalf("LoadConfigFromMaps round-trip mismatch:\ngot:  %#v\nwant: %#v", restored, cfg)
	}

	// Partial update omitting secret fields must preserve existing secrets.
	patched, err := ApplyPatch(cfg, map[string]any{
		"telegram": map[string]any{
			"chat_id":     "-100999888777",
			"clear_proxy": true,
		},
	})
	if err != nil {
		t.Fatalf("partial telegram patch failed: %v", err)
	}
	if patched.Telegram.BotToken != secretBotToken {
		t.Fatalf("expected bot token preserved on partial patch")
	}
	if patched.Telegram.ProxyURL != "" {
		t.Fatalf("expected proxy cleared by clear_proxy: true")
	}

	// Channel clear resets fields & secrets and disables the channel.
	cleared, err := ApplyPatch(patched, map[string]any{
		"telegram": map[string]any{"clear": true},
		"bark":     map[string]any{"clear": true},
		"email":    map[string]any{"clear": true},
	})
	if err != nil {
		t.Fatalf("clear patch failed: %v", err)
	}
	if cleared.Telegram.Enabled || cleared.Telegram.BotToken != "" || cleared.Telegram.ChatID != "" {
		t.Fatalf("expected telegram fully cleared: %#v", cleared.Telegram)
	}
	if cleared.Bark.Enabled || cleared.Bark.DeviceKey != "" || cleared.Bark.ServerURL != COLABONE_DEFAULT_BARK_SERVER_URL {
		t.Fatalf("expected bark reset to defaults: %#v", cleared.Bark)
	}
	if cleared.Email.Enabled || cleared.Email.Password != "" || cleared.Email.SMTPHost != "" || cleared.Email.SMTPPort != 465 {
		t.Fatalf("expected email reset to defaults: %#v", cleared.Email)
	}
	if len(cleared.SecretsMap()) != 0 {
		t.Fatalf("expected empty SecretsMap after clear, got %d entries", len(cleared.SecretsMap()))
	}

	// Verify RedactSecrets scrubs tokens, URL-encoded secrets, proxy passwords, and generic patterns.
	rawErr := fmt.Sprintf("POST https://api.telegram.org/bot%s/sendMessage via %s bark=%s pass=%s other=bot11223344:AABBCCDDEEFFGGHH",
		secretBotToken, secretProxyURL, secretBarkKey, secretEmailPass)
	redacted := RedactSecrets(rawErr, cfg)
	checkNoLeaks("RedactSecrets", redacted)
	if strings.Contains(redacted, "AABBCCDDEEFFGGHH") {
		t.Fatalf("expected generic telegram bot token scrubbed: %s", redacted)
	}
}

func TestBarkSmartParsingAndEmailValidation(t *testing.T) {
	barkCases := []struct {
		raw        string
		wantServer string
		wantKey    string
		wantErr    bool
	}{
		{"https://api.day.app/MyKey123", "https://api.day.app", "MyKey123", false},
		{"https://bark.myhost.com/MyKey123/", "https://bark.myhost.com", "MyKey123", false},
		{"https://example.com/bark/MyKey123", "https://example.com/bark", "MyKey123", false},
		{"https://example.com/sub/path/MyKey123?sound=bell", "https://example.com/sub/path", "MyKey123", false},
		{"https://bark.myhost.com:8443/KeyABC/", "https://bark.myhost.com:8443", "KeyABC", false},
		{"https://bark.myhost.com", "https://bark.myhost.com", "", false},
		{"https://bark.myhost.com/", "https://bark.myhost.com", "", false},
		{"https://api.day.app/push", "https://api.day.app", "", false},
		{"https://example.com/bark/push", "https://example.com/bark", "", false},
		{"https://example.com/sub/path/PUSH/", "https://example.com/sub/path", "", false},
		{"", "", "", false},
		{"https://example.com:", "", "", true},
		{"https://[::1]:", "", "", true},
		{"ftp://api.day.app/MyKey123", "", "", true},
		{"not-a-url", "", "", true},
		{"https://api.day.app/MyKey\r\nBad", "", "", true},
		{"https://user:pass@api.day.app/MyKey", "", "", true},
	}
	for _, tc := range barkCases {
		srv, key, err := ParseBarkEndpoint(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("ParseBarkEndpoint(%q): expected error, got server=%q key=%q", tc.raw, srv, key)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseBarkEndpoint(%q): unexpected error: %v", tc.raw, err)
		}
		if srv != tc.wantServer || key != tc.wantKey {
			t.Fatalf("ParseBarkEndpoint(%q): got (%q, %q), want (%q, %q)", tc.raw, srv, key, tc.wantServer, tc.wantKey)
		}
	}

	// Pasting full Bark URL into server_url or device_key must extract cleanly while preserving subpaths.
	def := DefaultConfig()
	c1, err := ApplyPatch(def, map[string]any{
		"bark": map[string]any{
			"server_url": "https://bark.custom.io/sub/PastedInServerKey/",
			"enabled":    true,
		},
	})
	if err != nil {
		t.Fatalf("full URL in bark.server_url failed: %v", err)
	}
	if c1.Bark.ServerURL != "https://bark.custom.io/sub" || c1.Bark.DeviceKey != "PastedInServerKey" {
		t.Fatalf("unexpected parse from server_url: %#v", c1.Bark)
	}

	c2, err := ApplyPatch(def, map[string]any{
		"bark": map[string]any{
			"device_key": "https://bark.fromkey.io/bark/PastedInDeviceKey",
			"enabled":    true,
		},
	})
	if err != nil {
		t.Fatalf("full URL in bark.device_key failed: %v", err)
	}
	if c2.Bark.ServerURL != "https://bark.fromkey.io/bark" || c2.Bark.DeviceKey != "PastedInDeviceKey" {
		t.Fatalf("unexpected parse from device_key: %#v", c2.Bark)
	}

	// Explicit server_url + device_key preserves subpath without stripping segments into key.
	c3, err := ApplyPatch(def, map[string]any{
		"bark": map[string]any{
			"server_url": "https://example.com/bark/push",
			"device_key": "ExplicitKey99",
			"enabled":    true,
		},
	})
	if err != nil {
		t.Fatalf("explicit server_url+device_key subpath failed: %v", err)
	}
	if c3.Bark.ServerURL != "https://example.com/bark" || c3.Bark.DeviceKey != "ExplicitKey99" {
		t.Fatalf("unexpected explicit subpath result: %#v", c3.Bark)
	}

	// Updating server_url when device_key already exists preserves subpath & existing key.
	c4, err := ApplyPatch(c3, map[string]any{
		"bark": map[string]any{
			"server_url": "https://example.com/new/bark/push",
		},
	})
	if err != nil {
		t.Fatalf("updating server_url with existing key failed: %v", err)
	}
	if c4.Bark.ServerURL != "https://example.com/new/bark" || c4.Bark.DeviceKey != "ExplicitKey99" {
		t.Fatalf("expected existing key and new subpath preserved, got %#v", c4.Bark)
	}

	// clear_device_key runs BEFORE processing server_url/device_key in the same patch.
	c5, err := ApplyPatch(c4, map[string]any{
		"bark": map[string]any{
			"clear_device_key": true,
			"server_url":       "https://example.com/bark/FreshKey77",
		},
	})
	if err != nil {
		t.Fatalf("clear_device_key + server_url failed: %v", err)
	}
	if c5.Bark.ServerURL != "https://example.com/bark" || c5.Bark.DeviceKey != "FreshKey77" {
		t.Fatalf("expected clear_device_key before parse, got %#v", c5.Bark)
	}

	// Control characters in device_key must be rejected BEFORE trimming whitespace.
	if _, err := ApplyPatch(def, map[string]any{
		"bark": map[string]any{"device_key": "  \n  "},
	}); err == nil {
		t.Fatalf("expected error for whitespace-padded newline in bark.device_key")
	}

	// Email CRLF header injection and invalid formats must be rejected.
	badEmailPayloads := []map[string]any{
		{"from_address": "ok@example.com\r\nBcc: evil@example.com"},
		{"to_addresses": "ok@example.com\nCc: evil@example.com"},
		{"from_name": "ColabOne\r\nX-Evil: 1"},
		{"smtp_host": "smtp.example.com\r\n"},
		{"username": "user\nname"},
		{"password": "secret\r\npass"},
		{"to_addresses": "not-an-email"},
		{"from_address": "user@nodot"},
		{"smtp_port": 0},
		{"smtp_port": 70000},
		{"security": "ssl"},
	}
	for i, ep := range badEmailPayloads {
		if _, err := ApplyPatch(def, map[string]any{"email": ep}); err == nil {
			t.Fatalf("case %d (%v): expected email validation error, got nil", i, ep)
		}
	}
}

func TestDispatcherFilteringDedupeFocusTestChannelAndLogRedaction(t *testing.T) {
	var (
		mu          sync.Mutex
		sysCalls    []ColabOneSystemNotificationRequest
		barkCalls   int32
		logs        []string
		focused     atomic.Bool
		currentTime = time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
	)

	barkServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&barkCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"message":"success"}`))
	}))
	defer barkServer.Close()

	const barkKey = "BarkSecretKeyForDispatcherTest"
	cfg := DefaultConfig()
	cfg.NotifyGPU = true
	cfg.NotifyTask = false
	cfg.NotifyAlert = true
	cfg.SystemOnlyWhenUnfocused = true
	cfg.RemoteOnlyWhenUnfocused = false
	cfg.System.Enabled = true
	cfg.Bark.Enabled = true
	cfg.Bark.ServerURL = barkServer.URL
	cfg.Bark.DeviceKey = barkKey

	var cfgMu sync.RWMutex
	notifier := NewColabOneNotifier(ColabOneNotifierConfig{
		ConfigProvider: func() ColabOneNotificationConfig {
			cfgMu.RLock()
			defer cfgMu.RUnlock()
			return cfg
		},
		IsWindowFocused: func() bool { return focused.Load() },
		SystemSender: func(_ context.Context, req ColabOneSystemNotificationRequest) error {
			mu.Lock()
			sysCalls = append(sysCalls, req)
			mu.Unlock()
			return nil
		},
		SystemSupported: func() bool { return true },
		HTTPClient:      barkServer.Client(),
		OnLog: func(msg, source, level string) {
			mu.Lock()
			logs = append(logs, fmt.Sprintf("[%s:%s] %s", source, level, msg))
			mu.Unlock()
		},
		DedupeWindow:   60 * time.Second,
		ChannelTimeout: 5 * time.Second,
		NowFn: func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return currentTime
		},
	})
	defer notifier.Close()

	// 1. Disabled category (Task) is filtered out.
	res := notifier.DispatchSync(context.Background(), ColabOneNotificationEvent{
		Kind:     EventOutputReady,
		Category: CategoryTask,
		Title:    "Task Done",
	})
	if len(res) != 0 {
		t.Fatalf("expected disabled Task category to dispatch to 0 channels, got %v", res)
	}

	// 2. Foreground focus suppresses System (SystemOnlyWhenUnfocused=true) but still delivers Bark (RemoteOnlyWhenUnfocused=false).
	focused.Store(true)
	res = notifier.DispatchSync(context.Background(), ColabOneNotificationEvent{
		Kind:      EventGPUAllocated,
		Category:  CategoryGPU,
		Severity:  SeveritySuccess,
		DedupeKey: "gpu-alloc-1",
		Title:     "GPU Ready",
	})
	if len(res) != 1 || res[ChannelBark] != nil {
		t.Fatalf("expected only Bark delivered while focused, got %v", res)
	}
	if atomic.LoadInt32(&barkCalls) != 1 {
		t.Fatalf("expected 1 bark call, got %d", barkCalls)
	}

	// 3. Deduplication suppresses same DedupeKey within window.
	res = notifier.DispatchSync(context.Background(), ColabOneNotificationEvent{
		Kind:      EventGPUAllocated,
		Category:  CategoryGPU,
		DedupeKey: "gpu-alloc-1",
		Title:     "GPU Ready duplicate",
	})
	if len(res) != 0 {
		t.Fatalf("expected duplicate event suppressed within window, got %v", res)
	}

	// 4. Advance clock past dedupe window and unfocus -> both System & Bark deliver in parallel!
	mu.Lock()
	currentTime = currentTime.Add(65 * time.Second)
	mu.Unlock()
	focused.Store(false)

	res = notifier.DispatchSync(context.Background(), ColabOneNotificationEvent{
		Kind:       EventGPUAllocated,
		Category:   CategoryGPU,
		DedupeKey:  "gpu-alloc-1",
		Title:      "GPU Ready Again",
		ImageBytes: makeTestPNG(t, 800, 600),
	})
	if len(res) != 2 || res[ChannelSystem] != nil || res[ChannelBark] != nil {
		t.Fatalf("expected System and Bark to succeed, got %v", res)
	}
	mu.Lock()
	if len(sysCalls) != 1 || len(sysCalls[0].ImageBytes) == 0 || sysCalls[0].ImageMime != "image/png" {
		t.Fatalf("expected system notification with prepared PNG thumbnail, got %+v", sysCalls)
	}
	mu.Unlock()

	// 5. RemoteOnlyWhenUnfocused=true suppresses remote channels when focused.
	cfgMu.Lock()
	cfg.RemoteOnlyWhenUnfocused = true
	cfg.NotifyGPU = false
	cfg.NotifyAlert = false
	cfgMu.Unlock()
	focused.Store(true)

	res = notifier.DispatchSync(context.Background(), ColabOneNotificationEvent{
		Kind:     EventAuthRequired,
		Category: CategoryAlert,
		Title:    "Suppressed",
	})
	if len(res) != 0 {
		t.Fatalf("expected suppressed event when categories disabled & focused, got %v", res)
	}

	// 6. TestChannel bypasses disabled categories, dedupe, and foreground suppression, and sends preview image!
	if err := notifier.TestChannel(context.Background(), ChannelSystem, "en-US"); err != nil {
		t.Fatalf("TestChannel(system) failed: %v", err)
	}
	mu.Lock()
	lastSys := sysCalls[len(sysCalls)-1]
	mu.Unlock()
	if !strings.Contains(lastSys.Title, "Notification Channel Connected") || len(lastSys.ImageBytes) == 0 {
		t.Fatalf("expected localized en-US test system notification with preview card, got title=%q imgLen=%d", lastSys.Title, len(lastSys.ImageBytes))
	}

	// 7. Error logs must redact secrets strictly.
	failBarkServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintf(w, `{"code":500,"message":"rejected key %s"}`, barkKey)
	}))
	defer failBarkServer.Close()

	cfgMu.Lock()
	cfg.Bark.ServerURL = failBarkServer.URL
	cfgMu.Unlock()

	err := notifier.TestChannel(context.Background(), ChannelBark, "zh-CN")
	if err == nil {
		t.Fatalf("expected error from failing Bark server")
	}
	if strings.Contains(err.Error(), barkKey) {
		t.Fatalf("returned error leaked bark key: %v", err)
	}
	mu.Lock()
	allLogs := strings.Join(logs, "\n")
	mu.Unlock()
	if !strings.Contains(allLogs, "[通知:warning]") {
		t.Fatalf("expected warning log with source 通知, got: %s", allLogs)
	}
	if strings.Contains(allLogs, barkKey) {
		t.Fatalf("log output leaked bark key: %s", allLogs)
	}

	// 8. Async Notify + Close should never panic even if Notify is called after Close.
	notifier.Notify(ColabOneNotificationEvent{Kind: EventAuthRequired, Title: "async"})
	notifier.Close()
	notifier.Notify(ColabOneNotificationEvent{Kind: EventAuthRequired, Title: "after-close"})
}

func TestTelegramSendMessageSendPhotoFallbackAndAuthNoFallback(t *testing.T) {
	const botToken = "123456789:AAH_TestTelegramToken_XYZ987654"
	const chatID = "-100555666777"

	var (
		mu              sync.Mutex
		mode            = "ok"
		seenPaths       []string
		lastMsgPayload  map[string]any
		lastPhotoFields map[string]string
		lastPhotoBytes  []byte
	)

	tgServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seenPaths = append(seenPaths, r.URL.Path)
		curMode := mode
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/sendPhoto") {
			if curMode == "photo_400" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: PHOTO_INVALID_DIMENSIONS"}`))
				return
			}
			if curMode == "photo_401" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = fmt.Fprintf(w, `{"ok":false,"error_code":401,"description":"Unauthorized for bot%s"}`, botToken)
				return
			}
			if err := r.ParseMultipartForm(16 << 20); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			fields := map[string]string{
				"chat_id":    r.FormValue("chat_id"),
				"parse_mode": r.FormValue("parse_mode"),
				"caption":    r.FormValue("caption"),
			}
			f, _, err := r.FormFile("photo")
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			defer f.Close()
			photoData, _ := io.ReadAll(f)

			mu.Lock()
			lastPhotoFields = fields
			lastPhotoBytes = photoData
			mu.Unlock()

			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
			return
		}

		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			mu.Lock()
			lastMsgPayload = payload
			mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":2}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer tgServer.Close()

	cfg := DefaultConfig()
	cfg.Telegram = ColabOneTelegramChannelConfig{
		Enabled:          true,
		BotToken:         botToken,
		ChatID:           chatID,
		APIBaseURL:       tgServer.URL,
		SendPreviewImage: true,
	}
	ch := newColabOneTelegramChannel(tgServer.Client())

	// 1. Plain sendMessage with HTML escaping.
	err := ch.Send(context.Background(), cfg, ColabOneNotificationEvent{
		Kind:     EventGPUAllocated,
		Severity: SeveritySuccess,
		Title:    `GPU <T4> & "Ready"`,
		Body:     `Check <script>alert(1)</script> & details`,
		Fields:   []ColabOneNotificationField{{Label: "Card", Value: "Tesla T4 <16GB>"}},
		Locale:   "en-US",
	})
	if err != nil {
		t.Fatalf("Telegram sendMessage failed: %v", err)
	}
	mu.Lock()
	msgText, _ := lastMsgPayload["text"].(string)
	parseMode, _ := lastMsgPayload["parse_mode"].(string)
	mu.Unlock()
	if parseMode != "HTML" {
		t.Fatalf("expected parse_mode HTML, got %q", parseMode)
	}
	if strings.Contains(msgText, "<script>") || !strings.Contains(msgText, "&lt;script&gt;") {
		t.Fatalf("expected HTML escaping in telegram text, got: %s", msgText)
	}

	// 2. sendPhoto multipart upload with oversized caption safely truncated <= 1024 runes.
	longBody := strings.Repeat("生成完成详情测试文本内容。", 150)
	err = ch.Send(context.Background(), cfg, ColabOneNotificationEvent{
		Kind:          EventOutputReady,
		Severity:      SeveritySuccess,
		Title:         "作品生成完成",
		Body:          longBody,
		ImageBytes:    makeTestPNG(t, 1600, 900),
		ImageFilename: "output_01.png",
		Locale:        "zh-CN",
	})
	if err != nil {
		t.Fatalf("Telegram sendPhoto failed: %v", err)
	}
	mu.Lock()
	photoFields := lastPhotoFields
	photoBytes := lastPhotoBytes
	mu.Unlock()
	if photoFields["chat_id"] != chatID || photoFields["parse_mode"] != "HTML" {
		t.Fatalf("unexpected sendPhoto fields: %+v", photoFields)
	}
	if n := utf8.RuneCountInString(photoFields["caption"]); n == 0 || n > 1024 {
		t.Fatalf("expected caption length in 1..1024 runes, got %d", n)
	}
	decodedImg, format, err := image.Decode(bytes.NewReader(photoBytes))
	if err != nil || format != "jpeg" {
		t.Fatalf("expected valid JPEG photo upload, err=%v format=%q", err, format)
	}
	if decodedImg.Bounds().Dx() > 1280 || decodedImg.Bounds().Dy() > 1280 {
		t.Fatalf("expected photo scaled to maxEdge 1280, got %v", decodedImg.Bounds())
	}

	// 3. Non-auth photo error (400) automatically falls back to sendMessage.
	mu.Lock()
	mode = "photo_400"
	seenPaths = nil
	mu.Unlock()

	err = ch.Send(context.Background(), cfg, ColabOneNotificationEvent{
		Kind:       EventOutputReady,
		Severity:   SeveritySuccess,
		Title:      "Fallback Test",
		Body:       "Should fall back to sendMessage",
		ImageBytes: makeTestPNG(t, 200, 200),
	})
	if err != nil {
		t.Fatalf("expected fallback to sendMessage to succeed, got: %v", err)
	}
	mu.Lock()
	pathsCopy := append([]string(nil), seenPaths...)
	mu.Unlock()
	if len(pathsCopy) != 2 || !strings.HasSuffix(pathsCopy[0], "/sendPhoto") || !strings.HasSuffix(pathsCopy[1], "/sendMessage") {
		t.Fatalf("expected [sendPhoto, sendMessage], got %v", pathsCopy)
	}

	// 4. Auth error (401) must NOT fallback and must NOT leak botToken.
	mu.Lock()
	mode = "photo_401"
	seenPaths = nil
	mu.Unlock()

	err = ch.Send(context.Background(), cfg, ColabOneNotificationEvent{
		Kind:       EventOutputReady,
		Title:      "Auth Fail Test",
		ImageBytes: makeTestPNG(t, 128, 128),
	})
	if err == nil {
		t.Fatalf("expected 401 auth error")
	}
	if strings.Contains(err.Error(), botToken) {
		t.Fatalf("error leaked botToken: %v", err)
	}
	mu.Lock()
	pathsCopy = append([]string(nil), seenPaths...)
	mu.Unlock()
	if len(pathsCopy) != 1 || !strings.HasSuffix(pathsCopy[0], "/sendPhoto") {
		t.Fatalf("expected no fallback on 401 auth error, got paths: %v", pathsCopy)
	}
}

func TestTelegramHTTPConnectAndSOCKS5Proxy(t *testing.T) {
	const botToken = "555666777:AAH_ProxyTestToken_123456"
	const proxyUser = "tguser"
	const proxyPass = "tgpass_secret"

	// Target TLS server simulating https://api.telegram.org.
	var receivedCount atomic.Int32
	tlsTarget := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer tlsTarget.Close()

	// Local HTTP CONNECT proxy verifying Basic auth and tunneling raw TCP.
	var connectSeen atomic.Int32
	expectedAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(proxyUser+":"+proxyPass))
	proxyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen proxy: %v", err)
	}
	defer proxyLn.Close()

	go func() {
		for {
			clientConn, acceptErr := proxyLn.Accept()
			if acceptErr != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				req, readErr := http.ReadRequest(br)
				if readErr != nil || req.Method != http.MethodConnect {
					return
				}
				if req.Header.Get("Proxy-Authorization") != expectedAuth {
					_, _ = io.WriteString(c, "HTTP/1.1 407 Proxy Authentication Required\r\n\r\n")
					return
				}
				connectSeen.Add(1)
				up, dialErr := net.DialTimeout("tcp", req.Host, 3*time.Second)
				if dialErr != nil {
					_, _ = io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
					return
				}
				defer up.Close()
				_, _ = io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
				var wg sync.WaitGroup
				wg.Add(2)
				go func() {
					defer wg.Done()
					_, _ = io.Copy(up, br)
				}()
				go func() {
					defer wg.Done()
					_, _ = io.Copy(c, up)
				}()
				wg.Wait()
			}(clientConn)
		}
	}()

	cfg := DefaultConfig()
	cfg.Telegram = ColabOneTelegramChannelConfig{
		Enabled:    true,
		BotToken:   botToken,
		ChatID:     "42",
		APIBaseURL: tlsTarget.URL,
		ProxyURL:   fmt.Sprintf("http://%s:%s@%s", proxyUser, proxyPass, proxyLn.Addr().String()),
	}

	ch := newColabOneTelegramChannel(tlsTarget.Client())
	if err := ch.Send(context.Background(), cfg, ColabOneNotificationEvent{
		Kind:  EventGPUAllocated,
		Title: "Via HTTP CONNECT Proxy",
	}); err != nil {
		t.Fatalf("Telegram over HTTP CONNECT proxy failed: %v", err)
	}
	if connectSeen.Load() < 1 || receivedCount.Load() != 1 {
		t.Fatalf("expected traffic routed through CONNECT proxy: connectSeen=%d received=%d", connectSeen.Load(), receivedCount.Load())
	}

	// Local SOCKS5 proxy with username/password authentication.
	var socksSeen atomic.Int32
	socksLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen socks5: %v", err)
	}
	defer socksLn.Close()

	go func() {
		for {
			c, acceptErr := socksLn.Accept()
			if acceptErr != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				var hdr [2]byte
				if _, err := io.ReadFull(conn, hdr[:]); err != nil || hdr[0] != 0x05 {
					return
				}
				methods := make([]byte, hdr[1])
				if _, err := io.ReadFull(conn, methods); err != nil {
					return
				}
				_, _ = conn.Write([]byte{0x05, 0x02})

				var authHdr [2]byte
				if _, err := io.ReadFull(conn, authHdr[:]); err != nil || authHdr[0] != 0x01 {
					return
				}
				uBuf := make([]byte, authHdr[1])
				if _, err := io.ReadFull(conn, uBuf); err != nil {
					return
				}
				var pLen [1]byte
				if _, err := io.ReadFull(conn, pLen[:]); err != nil {
					return
				}
				pBuf := make([]byte, pLen[0])
				if _, err := io.ReadFull(conn, pBuf); err != nil {
					return
				}
				if string(uBuf) != proxyUser || string(pBuf) != proxyPass {
					_, _ = conn.Write([]byte{0x01, 0x01})
					return
				}
				_, _ = conn.Write([]byte{0x01, 0x00})

				var reqHdr [4]byte
				if _, err := io.ReadFull(conn, reqHdr[:]); err != nil || reqHdr[1] != 0x01 {
					return
				}
				var targetHost string
				switch reqHdr[3] {
				case 0x01:
					var ip4 [4]byte
					if _, err := io.ReadFull(conn, ip4[:]); err != nil {
						return
					}
					targetHost = net.IP(ip4[:]).String()
				case 0x03:
					var l [1]byte
					if _, err := io.ReadFull(conn, l[:]); err != nil {
						return
					}
					hostBuf := make([]byte, l[0])
					if _, err := io.ReadFull(conn, hostBuf); err != nil {
						return
					}
					targetHost = string(hostBuf)
				default:
					return
				}
				var portBuf [2]byte
				if _, err := io.ReadFull(conn, portBuf[:]); err != nil {
					return
				}
				targetPort := int(binary.BigEndian.Uint16(portBuf[:]))

				up, dialErr := net.DialTimeout("tcp", net.JoinHostPort(targetHost, strconv.Itoa(targetPort)), 3*time.Second)
				if dialErr != nil {
					_, _ = conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
					return
				}
				defer up.Close()
				socksSeen.Add(1)
				_, _ = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0, 0})

				var wg sync.WaitGroup
				wg.Add(2)
				go func() {
					defer wg.Done()
					_, _ = io.Copy(up, conn)
				}()
				go func() {
					defer wg.Done()
					_, _ = io.Copy(conn, up)
				}()
				wg.Wait()
			}(c)
		}
	}()

	cfg.Telegram.ProxyURL = fmt.Sprintf("socks5h://%s:%s@%s", proxyUser, proxyPass, socksLn.Addr().String())
	if err := ch.Send(context.Background(), cfg, ColabOneNotificationEvent{
		Kind:  EventGPUAllocated,
		Title: "Via SOCKS5 Proxy",
	}); err != nil {
		t.Fatalf("Telegram over SOCKS5 proxy failed: %v", err)
	}
	if socksSeen.Load() < 1 || receivedCount.Load() != 2 {
		t.Fatalf("expected traffic routed through SOCKS5 proxy: socksSeen=%d received=%d", socksSeen.Load(), receivedCount.Load())
	}
}

func TestBarkPushPayloadTimeSensitiveAndErrorHandling(t *testing.T) {
	const deviceKey = "BarkSecretKey_99887766"

	var (
		mu          sync.Mutex
		lastPath    string
		lastPayload map[string]any
		failMode    bool
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		lastPath = r.URL.Path
		lastPayload = body
		shouldFail := failMode
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if shouldFail {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, `{"code":400,"message":"invalid device key %s"}`, deviceKey)
			return
		}
		_, _ = w.Write([]byte(`{"code":200,"message":"success"}`))
	}))
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.Bark = ColabOneBarkChannelConfig{
		Enabled:   true,
		ServerURL: srv.URL,
		DeviceKey: deviceKey,
		Group:     "ColabOne",
		Sound:     "anticipate",
		Level:     "",
	}

	ch := newColabOneBarkChannel(srv.Client())
	err := ch.Send(context.Background(), cfg, ColabOneNotificationEvent{
		Kind:       EventGPUDisconnected,
		Severity:   SeverityError,
		Title:      "GPU 运行时异常断开",
		Subtitle:   "Tesla T4",
		Body:       "检测到远端连接中断",
		Fields:     []ColabOneNotificationField{{Label: "状态", Value: "已断开"}},
		ActionHint: "请重新分配 GPU",
	})
	if err != nil {
		t.Fatalf("Bark Send failed: %v", err)
	}

	mu.Lock()
	path := lastPath
	payload := lastPayload
	mu.Unlock()
	if path != "/push" {
		t.Fatalf("expected /push path, got %q", path)
	}
	if payload["device_key"] != deviceKey || payload["group"] != "ColabOne" || payload["sound"] != "anticipate" {
		t.Fatalf("unexpected Bark payload fields: %+v", payload)
	}
	if payload["level"] != "timeSensitive" {
		t.Fatalf("expected default timeSensitive on error severity, got %v", payload["level"])
	}
	if payload["isArchive"] != "1" {
		t.Fatalf("expected isArchive=1, got %v", payload["isArchive"])
	}

	mu.Lock()
	failMode = true
	mu.Unlock()
	err = ch.Send(context.Background(), cfg, ColabOneNotificationEvent{
		Kind:  EventGPUAllocated,
		Title: "Should Fail",
	})
	if err == nil {
		t.Fatalf("expected Bark error when code != 200")
	}
	if strings.Contains(err.Error(), deviceKey) {
		t.Fatalf("Bark error leaked device_key: %v", err)
	}
}

type mockSMTPState struct {
	mu           sync.Mutex
	authMethod   string
	authUser     string
	authPass     string
	mailFrom     string
	rcptTo       []string
	dataRaw      string
	usedStartTLS bool
}

func startMockSMTPServer(
	t *testing.T,
	authMechs string,
	expectUser string,
	expectPass string,
	rejectPlain bool,
	startTLSCfg *tls.Config,
	implicitTLS bool,
) (host string, port int, state *mockSMTPState, cleanup func()) {
	t.Helper()
	var (
		ln  net.Listener
		err error
	)
	if implicitTLS {
		ln, err = tls.Listen("tcp", "127.0.0.1:0", startTLSCfg)
	} else {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		t.Fatalf("listen mock SMTP: %v", err)
	}
	st := &mockSMTPState{}

	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				connUpgradedTLS := implicitTLS
				writeLine := func(s string) {
					_, _ = io.WriteString(c, s+"\r\n")
				}
				writeLine("220 mock.colabone.local ESMTP Ready")

				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					trimmed := strings.TrimRight(line, "\r\n")
					upper := strings.ToUpper(trimmed)

					switch {
					case strings.HasPrefix(upper, "EHLO") || strings.HasPrefix(upper, "HELO"):
						writeLine("250-mock.colabone.local")
						if !connUpgradedTLS && startTLSCfg != nil {
							writeLine("250-STARTTLS")
						}
						writeLine("250 AUTH " + authMechs)

					case upper == "STARTTLS" && startTLSCfg != nil:
						writeLine("220 Ready to start TLS")
						tlsConn := tls.Server(c, startTLSCfg)
						if err := tlsConn.Handshake(); err != nil {
							return
						}
						c = tlsConn
						r = bufio.NewReader(c)
						connUpgradedTLS = true
						st.mu.Lock()
						st.usedStartTLS = true
						st.mu.Unlock()

					case strings.HasPrefix(upper, "AUTH PLAIN"):
						if rejectPlain {
							writeLine("504 PLAIN mechanism disabled")
							continue
						}
						b64 := strings.TrimSpace(trimmed[len("AUTH PLAIN"):])
						raw, decErr := base64.StdEncoding.DecodeString(b64)
						if decErr != nil {
							writeLine("535 Invalid base64")
							continue
						}
						parts := bytes.Split(raw, []byte{0})
						if len(parts) < 3 || string(parts[1]) != expectUser || string(parts[2]) != expectPass {
							writeLine("535 Authentication failed")
							continue
						}
						st.mu.Lock()
						st.authMethod = "PLAIN"
						st.authUser = string(parts[1])
						st.authPass = string(parts[2])
						st.mu.Unlock()
						writeLine("235 Authentication successful")

					case upper == "AUTH LOGIN":
						writeLine("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
						uLine, err := r.ReadString('\n')
						if err != nil {
							return
						}
						uBytes, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(uLine))

						writeLine("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
						pLine, err := r.ReadString('\n')
						if err != nil {
							return
						}
						pBytes, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(pLine))

						if string(uBytes) != expectUser || string(pBytes) != expectPass {
							writeLine("535 Authentication failed")
							continue
						}
						st.mu.Lock()
						st.authMethod = "LOGIN"
						st.authUser = string(uBytes)
						st.authPass = string(pBytes)
						st.mu.Unlock()
						writeLine("235 Authentication successful")

					case strings.HasPrefix(upper, "MAIL FROM:"):
						st.mu.Lock()
						st.mailFrom = strings.TrimSpace(trimmed[len("MAIL FROM:"):])
						st.mu.Unlock()
						writeLine("250 OK")

					case strings.HasPrefix(upper, "RCPT TO:"):
						st.mu.Lock()
						st.rcptTo = append(st.rcptTo, strings.TrimSpace(trimmed[len("RCPT TO:"):]))
						st.mu.Unlock()
						writeLine("250 OK")

					case upper == "DATA":
						writeLine("354 Start mail input")
						var dataBuf strings.Builder
						for {
							dl, err := r.ReadString('\n')
							if err != nil {
								return
							}
							if dl == ".\r\n" || dl == ".\n" {
								break
							}
							if strings.HasPrefix(dl, "..") {
								dl = dl[1:]
							}
							dataBuf.WriteString(dl)
						}
						st.mu.Lock()
						st.dataRaw = dataBuf.String()
						st.mu.Unlock()
						writeLine("250 Message accepted")

					case upper == "QUIT":
						writeLine("221 Bye")
						return

					default:
						writeLine("502 Command not implemented")
					}
				}
			}(conn)
		}
	}()

	tcpAddr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", tcpAddr.Port, st, func() { _ = ln.Close() }
}

func TestEmailSMTPPlainLoginFallbackSTARTTLSImplicitTLSAndMIME(t *testing.T) {
	const (
		smtpUser = "sender@colabone.local"
		smtpPass = "SuperSecretSMTPPass_8899"
	)
	serverTLS, clientTLS := makeSelfSignedLoopbackTLS(t)

	// 1. STARTTLS + AUTH LOGIN fallback (PLAIN rejected -> LOGIN succeeds) + inline preview image + XSS escaping.
	host, port, st, stop := startMockSMTPServer(t, "PLAIN LOGIN", smtpUser, smtpPass, true, serverTLS, false)
	defer stop()

	ch := &colabOneEmailChannel{tlsConfig: clientTLS}
	cfg := DefaultConfig()
	cfg.Email = ColabOneEmailChannelConfig{
		Enabled:           true,
		SMTPHost:          host,
		SMTPPort:          port,
		Security:          "starttls",
		Username:          smtpUser,
		Password:          smtpPass,
		FromAddress:       smtpUser,
		FromName:          "ColabOne 通知中心",
		ToAddresses:       "alice@example.com, bob@example.com",
		EmbedPreviewImage: true,
	}

	ev := ColabOneNotificationEvent{
		Kind:          EventOutputReady,
		Category:      CategoryTask,
		Severity:      SeveritySuccess,
		Title:         `作品生成完成 <script>alert("xss")</script>`,
		Subtitle:      `ComfyUI Flux & Portrait`,
		Body:          "第一行内容\n包含 <b>HTML</b> & 特殊字符",
		Fields:        []ColabOneNotificationField{{Label: "工作流", Value: `Flux <v2> "Pro"`}, {Label: "耗时", Value: "18.4s"}},
		ActionHint:    `点击作品库查看 <img src=x onerror=alert(1)>`,
		ImageBytes:    makeTestPNG(t, 640, 360),
		ImageFilename: `render_<01>.png`,
		Locale:        "zh-CN",
		OccurredAt:    time.Date(2026, 11, 28, 12, 30, 0, 0, time.UTC),
	}

	if err := ch.Send(context.Background(), cfg, ev); err != nil {
		t.Fatalf("Email STARTTLS + AUTH LOGIN send failed: %v", err)
	}

	st.mu.Lock()
	usedStartTLS := st.usedStartTLS
	authMethod := st.authMethod
	rcptTo := append([]string(nil), st.rcptTo...)
	rawMIME := st.dataRaw
	st.mu.Unlock()

	if !usedStartTLS {
		t.Fatalf("expected STARTTLS upgrade")
	}
	if authMethod != "LOGIN" {
		t.Fatalf("expected AUTH LOGIN fallback, got %q", authMethod)
	}
	if len(rcptTo) != 2 {
		t.Fatalf("expected 2 recipients, got %v", rcptTo)
	}

	// Parse MIME message & verify RFC 2047 headers + multipart/related + cid:colabone-preview + XSS safety.
	parsedMsg, err := mail.ReadMessage(strings.NewReader(rawMIME))
	if err != nil {
		t.Fatalf("mail.ReadMessage failed: %v", err)
	}
	wd := new(mime.WordDecoder)
	decodedSubj, err := wd.DecodeHeader(parsedMsg.Header.Get("Subject"))
	if err != nil {
		t.Fatalf("DecodeHeader(Subject): %v", err)
	}
	if !strings.Contains(decodedSubj, "ColabOne") || !strings.Contains(decodedSubj, "作品生成完成") {
		t.Fatalf("unexpected decoded subject: %q", decodedSubj)
	}

	ct, params, err := mime.ParseMediaType(parsedMsg.Header.Get("Content-Type"))
	if err != nil || ct != "multipart/related" {
		t.Fatalf("expected multipart/related top-level Content-Type, got %q (err=%v)", ct, err)
	}

	var (
		foundPlain  string
		foundHTML   string
		foundInline []byte
		foundCID    string
	)
	relReader := multipart.NewReader(parsedMsg.Body, params["boundary"])
	for {
		part, err := relReader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("relReader.NextPart: %v", err)
		}
		pct, pparams, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if pct == "multipart/alternative" {
			altReader := multipart.NewReader(part, pparams["boundary"])
			for {
				ap, aerr := altReader.NextPart()
				if errors.Is(aerr, io.EOF) {
					break
				}
				if aerr != nil {
					t.Fatalf("altReader.NextPart: %v", aerr)
				}
				act, _, _ := mime.ParseMediaType(ap.Header.Get("Content-Type"))
				rawB64, _ := io.ReadAll(ap)
				decoded, decErr := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.ReplaceAll(string(rawB64), "\r", ""), "\n", ""))
				if decErr != nil {
					t.Fatalf("base64 decode %s: %v", act, decErr)
				}
				if act == "text/plain" {
					foundPlain = string(decoded)
				} else if act == "text/html" {
					foundHTML = string(decoded)
				}
			}
		} else if pct == "image/jpeg" {
			foundCID = part.Header.Get("Content-ID")
			rawB64, _ := io.ReadAll(part)
			decoded, decErr := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.ReplaceAll(string(rawB64), "\r", ""), "\n", ""))
			if decErr != nil {
				t.Fatalf("base64 decode image/jpeg: %v", decErr)
			}
			foundInline = decoded
		}
	}

	if foundPlain == "" || foundHTML == "" {
		t.Fatalf("expected both text/plain and text/html parts")
	}
	if foundCID != "<colabone-preview>" || len(foundInline) == 0 {
		t.Fatalf("expected inline JPEG with Content-ID <colabone-preview>, got cid=%q len=%d", foundCID, len(foundInline))
	}
	if _, _, err := image.Decode(bytes.NewReader(foundInline)); err != nil {
		t.Fatalf("inline preview JPEG is not valid image: %v", err)
	}

	// Verify Apple-inspired design tokens, cid image, zero remote URLs, and XSS escaping.
	for _, want := range []string{
		"#F5F5F7",
		"#FFFFFF",
		"border-radius:20px",
		"border:1px solid #E5E5EA",
		"#E8F8EE",
		"#157A3E",
		"cid:colabone-preview",
		"&lt;script&gt;alert(&#34;xss&#34;)&lt;/script&gt;",
		"&lt;img src=x onerror=alert(1)&gt;",
		"ColabOne",
	} {
		if !strings.Contains(foundHTML, want) {
			t.Fatalf("rendered HTML missing expected content %q", want)
		}
	}
	if strings.Contains(foundHTML, "<script>") || strings.Contains(foundHTML, "<img src=x") {
		t.Fatalf("XSS payload was not escaped in rendered HTML!")
	}
	if strings.Contains(foundHTML, "http://") || strings.Contains(foundHTML, "https://") {
		t.Fatalf("rendered HTML must not contain external remote asset URLs")
	}

	// 2. Plain SMTP (security="none") + AUTH PLAIN without image.
	host2, port2, st2, stop2 := startMockSMTPServer(t, "PLAIN", smtpUser, smtpPass, false, nil, false)
	defer stop2()

	cfg2 := cfg
	cfg2.Email.SMTPHost = host2
	cfg2.Email.SMTPPort = port2
	cfg2.Email.Security = "none"
	cfg2.Email.EmbedPreviewImage = false
	if err := ch.Send(context.Background(), cfg2, ev); err != nil {
		t.Fatalf("Email plain + AUTH PLAIN send failed: %v", err)
	}
	st2.mu.Lock()
	m2 := st2.authMethod
	raw2 := st2.dataRaw
	st2.mu.Unlock()
	if m2 != "PLAIN" {
		t.Fatalf("expected AUTH PLAIN, got %q", m2)
	}
	if strings.Contains(raw2, "cid:colabone-preview") {
		t.Fatalf("expected no cid reference when EmbedPreviewImage is false")
	}

	// 3. Implicit TLS (security="tls") + AUTH PLAIN.
	host3, port3, st3, stop3 := startMockSMTPServer(t, "PLAIN LOGIN", smtpUser, smtpPass, false, serverTLS, true)
	defer stop3()

	cfg3 := cfg
	cfg3.Email.SMTPHost = host3
	cfg3.Email.SMTPPort = port3
	cfg3.Email.Security = "tls"
	if err := ch.Send(context.Background(), cfg3, ev); err != nil {
		t.Fatalf("Email implicit TLS send failed: %v", err)
	}
	st3.mu.Lock()
	if st3.authMethod != "PLAIN" {
		t.Fatalf("expected AUTH PLAIN over implicit TLS, got %q", st3.authMethod)
	}
	st3.mu.Unlock()

	// 4. Wrong password fails and never leaks SMTP password.
	cfgBad := cfg3
	cfgBad.Email.Password = "WrongSecretSMTPPass_000"
	err = ch.Send(context.Background(), cfgBad, ev)
	if err == nil {
		t.Fatalf("expected SMTP auth failure for wrong password")
	}
	if strings.Contains(err.Error(), cfgBad.Email.Password) {
		t.Fatalf("SMTP error leaked password: %v", err)
	}
}

func TestPreparePreviewImageAndTestCard(t *testing.T) {
	// Oversized 2400x1200 PNG scaled to maxEdge 1000 preserving 2:1 ratio as JPEG.
	bigPNG := makeTestPNG(t, 2400, 1200)
	data, mimeType, ext, w, h, err := PreparePreviewImage(bigPNG, 1000, 500<<10, false)
	if err != nil {
		t.Fatalf("PreparePreviewImage JPEG failed: %v", err)
	}
	if mimeType != "image/jpeg" || ext != ".jpg" || w != 1000 || h != 500 {
		t.Fatalf("unexpected JPEG output: mime=%q ext=%q dims=%dx%d", mimeType, ext, w, h)
	}
	if len(data) > 500<<10 {
		t.Fatalf("expected <= 500KB, got %d bytes", len(data))
	}

	// PreferPNG keeps PNG format when within maxBytes.
	smallPNG := makeTestPNG(t, 300, 150)
	pData, pMime, pExt, pw, ph, err := PreparePreviewImage(smallPNG, 512, 1<<20, true)
	if err != nil {
		t.Fatalf("PreparePreviewImage PNG failed: %v", err)
	}
	if pMime != "image/png" || pExt != ".png" || pw != 300 || ph != 150 || len(pData) == 0 {
		t.Fatalf("unexpected PNG output: mime=%q ext=%q dims=%dx%d len=%d", pMime, pExt, pw, ph, len(pData))
	}

	// Tight maxBytes triggers quality stepping & downscaling cleanly.
	noisyJPG := makeTestJPEG(t, 1200, 900)
	tightData, tightMime, _, _, _, err := PreparePreviewImage(noisyJPG, 1200, 25<<10, false)
	if err != nil {
		t.Fatalf("PreparePreviewImage tight maxBytes failed: %v", err)
	}
	if tightMime != "image/jpeg" || len(tightData) > 25<<10 {
		t.Fatalf("expected JPEG <= 25KB, got mime=%q len=%d", tightMime, len(tightData))
	}

	// Invalid data returns error cleanly.
	if _, _, _, _, _, err := PreparePreviewImage([]byte("not-an-image"), 512, 1<<20, false); err == nil {
		t.Fatalf("expected decode error for invalid image bytes")
	}

	// Generated test preview card PNG decodes cleanly to 640x360.
	cardPNG := BuildTestPreviewCardPNG()
	cardImg, format, err := image.Decode(bytes.NewReader(cardPNG))
	if err != nil || format != "png" {
		t.Fatalf("BuildTestPreviewCardPNG invalid: err=%v format=%q", err, format)
	}
	if cardImg.Bounds().Dx() != 640 || cardImg.Bounds().Dy() != 360 {
		t.Fatalf("expected 640x360 test preview card, got %v", cardImg.Bounds())
	}
}

func TestLocalesAndNoForbiddenRightArrowsInRenderedOutput(t *testing.T) {
	locales := []struct {
		input string
		want  string
	}{
		{"", "zh-CN"},
		{"zh", "zh-CN"},
		{"zh_CN", "zh-CN"},
		{"zh-TW", "zh-TW"},
		{"zh-HK", "zh-HK"},
		{"en", "en-US"},
		{"en-GB", "en-US"},
		{"ja", "ja-JP"},
		{"ko-KR", "ko-KR"},
		{"fr-FR", "zh-CN"},
	}
	for _, tc := range locales {
		if got := NormalizeLocale(tc.input); got != tc.want {
			t.Fatalf("NormalizeLocale(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}

	forbiddenRe := regexp.MustCompile("[\u2192\u21d2\u2794\u279c\u27a1\u2b95]|&ra" + "rr;|&#85" + "94;|&#x21" + "92;")
	for _, loc := range []string{"zh-CN", "zh-TW", "zh-HK", "en-US", "ja-JP", "ko-KR"} {
		ev := BuildTestNotificationEvent(ChannelEmail, loc, time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC))
		htmlOut := RenderEmailHTML(ev)
		plainOut := RenderEmailPlainText(ev)
		tgOut := colabOneFormatTelegramHTML(ev, colabOneTelegramMaxMessageRunes)

		for name, text := range map[string]string{"html": htmlOut, "plain": plainOut, "telegram": tgOut} {
			if text == "" {
				t.Fatalf("empty %s render for locale %s", name, loc)
			}
			if forbiddenRe.MatchString(text) {
				t.Fatalf("forbidden right-arrow symbol in %s render for locale %s", name, loc)
			}
		}
	}

	// Ensure custom Telegram API URL query/fragment are stripped cleanly.
	cfg, err := ApplyPatch(DefaultConfig(), map[string]any{
		"telegram": map[string]any{
			"api_base_url": "https://tg.example.com/custom/?x=1#frag",
		},
	})
	if err != nil {
		t.Fatalf("ApplyPatch api_base_url: %v", err)
	}
	u, err := url.Parse(cfg.Telegram.APIBaseURL)
	if err != nil || u.RawQuery != "" || u.Fragment != "" {
		t.Fatalf("expected cleaned api_base_url, got %q", cfg.Telegram.APIBaseURL)
	}
}

func TestNotifierConcurrentFanOutAndCloseSafety(t *testing.T) {
	var sysCount, tgCount, barkCount atomic.Int32

	tgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tgCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer tgSrv.Close()

	barkSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		barkCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"message":"success"}`))
	}))
	defer barkSrv.Close()

	cfg := DefaultConfig()
	cfg.SystemOnlyWhenUnfocused = false
	cfg.System.Enabled = true
	cfg.Telegram = ColabOneTelegramChannelConfig{
		Enabled:    true,
		BotToken:   "111222333:AAH_ConcurrentToken_ABC123",
		ChatID:     "999",
		APIBaseURL: tgSrv.URL,
	}
	cfg.Bark = ColabOneBarkChannelConfig{
		Enabled:   true,
		ServerURL: barkSrv.URL,
		DeviceKey: "ConcurrentBarkKey",
		Group:     "ColabOne",
	}

	n := NewColabOneNotifier(ColabOneNotifierConfig{
		ConfigProvider: func() ColabOneNotificationConfig { return cfg },
		SystemSender: func(_ context.Context, _ ColabOneSystemNotificationRequest) error {
			sysCount.Add(1)
			return nil
		},
		SystemSupported: func() bool { return true },
		ChannelTimeout:  5 * time.Second,
	})

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := n.DispatchSync(context.Background(), ColabOneNotificationEvent{
				Kind:      EventGPUAllocated,
				Category:  CategoryGPU,
				Severity:  SeveritySuccess,
				DedupeKey: fmt.Sprintf("concurrent-%d", i),
				Title:     fmt.Sprintf("Event %d", i),
			})
			if len(res) != 3 {
				t.Errorf("expected 3 channels dispatched, got %v", res)
			}
			n.Notify(ColabOneNotificationEvent{
				Kind:  EventOutputReady,
				Title: fmt.Sprintf("Async %d", i),
			})
		}()
	}
	wg.Wait()
	n.Close()
	n.Close() // Idempotent close.

	if sysCount.Load() < 16 || tgCount.Load() < 16 || barkCount.Load() < 16 {
		t.Fatalf("expected >=16 fan-out deliveries per channel, got sys=%d tg=%d bark=%d",
			sysCount.Load(), tgCount.Load(), barkCount.Load())
	}
}

func makeForgedDimensionPNG(t *testing.T, w, h uint32) []byte {
	t.Helper()
	raw := append([]byte(nil), makeTestPNG(t, 1, 1)...)
	// PNG signature is 8 bytes; IHDR chunk: 4-byte len (8..12), "IHDR" (12..16), width (16..20), height (20..24), 5 bytes (24..29), 4-byte CRC (29..33).
	binary.BigEndian.PutUint32(raw[16:20], w)
	binary.BigEndian.PutUint32(raw[20:24], h)
	crc := crc32.ChecksumIEEE(raw[12:29])
	binary.BigEndian.PutUint32(raw[29:33], crc)
	return raw
}

func TestPreparePreviewImageDecompressionBombAndPremulBlend(t *testing.T) {
	// 1. Raw input byte cap (> 16 MiB) rejected immediately.
	oversizedRaw := make([]byte, colabOneMaxRawPreviewBytes+1)
	if _, _, _, _, _, err := PreparePreviewImage(oversizedRaw, 512, 1<<20, false); err == nil {
		t.Fatalf("expected error for raw image exceeding 16 MiB cap")
	}

	// 2. Decompression bomb headers (>16384 edge and >40M pixels) rejected by DecodeConfig before image.Decode.
	for _, dims := range [][2]uint32{{20000, 20000}, {16385, 1}, {8000, 6000}} {
		bomb := makeForgedDimensionPNG(t, dims[0], dims[1])
		if _, _, _, _, _, err := PreparePreviewImage(bomb, 512, 1<<20, false); err == nil {
			t.Fatalf("expected decompression bomb rejection for %dx%d PNG", dims[0], dims[1])
		}
	}

	// 3. Premultiplied alpha pixel blending must NOT double-multiply by alpha on semi-transparent edges.
	dst := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			dst.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	src := image.NewRGBA(image.Rect(0, 0, 16, 16))
	// Straight white (255,255,255) at 50% alpha (128) is premultiplied (128,128,128,128).
	// Blitting 50%-alpha white over opaque white must remain ~255, NOT darken to ~192!
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			src.SetRGBA(x, y, color.RGBA{R: 128, G: 128, B: 128, A: 128})
		}
	}
	colabOneBlitRounded(dst, image.Rect(0, 0, 16, 16), src, 2)
	center := dst.RGBAAt(8, 8)
	if center.R < 252 || center.G < 252 || center.B < 252 {
		t.Fatalf("premultiplied alpha double-darkened pixel over white: got %+v, want ~255", center)
	}
}

func TestTelegramAndBarkRefuseRedirectsAndNonJSON200WithoutBodyReflection(t *testing.T) {
	var redirectTargetHits atomic.Int32
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectTargetHits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"code":200}`))
	}))
	defer redirectTarget.Close()

	const (
		secretBodyHTML = "SENSITIVE_INTERNAL_HTML_BODY_9911"
		botToken       = "123456789:AAH_RedirectTestToken_987654"
		barkKey        = "BarkRedirectSecretKey_9988"
	)

	var (
		mu       sync.Mutex
		mode     = "redirect"
		tgPaths  []string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		curMode := mode
		tgPaths = append(tgPaths, r.URL.Path)
		mu.Unlock()

		switch curMode {
		case "redirect":
			http.Redirect(w, r, redirectTarget.URL+"/steal", http.StatusFound)
		case "html200":
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html><body>" + secretBodyHTML + "</body></html>"))
		case "html500":
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("<html><body>" + secretBodyHTML + "</body></html>"))
		case "longjson":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			longMsg := strings.Repeat("E", 320) + " " + botToken + " " + barkKey
			_, _ = fmt.Fprintf(w, `{"ok":false,"error_code":400,"description":%q,"code":400,"message":%q}`, longMsg, longMsg)
		}
	}))
	defer upstream.Close()

	// Use an injected client with default redirect-following enabled to verify channels override CheckRedirect safely.
	injectedClient := &http.Client{Timeout: 5 * time.Second}

	tgCfg := DefaultConfig()
	tgCfg.Telegram = ColabOneTelegramChannelConfig{
		Enabled:          true,
		BotToken:         botToken,
		ChatID:           "42",
		APIBaseURL:       upstream.URL,
		SendPreviewImage: true,
	}
	tgCh := newColabOneTelegramChannel(injectedClient)

	barkCfg := DefaultConfig()
	barkCfg.Bark = ColabOneBarkChannelConfig{
		Enabled:   true,
		ServerURL: upstream.URL,
		DeviceKey: barkKey,
	}
	barkCh := newColabOneBarkChannel(injectedClient)

	// 1. Both Telegram and Bark refuse HTTP 302 redirects without following or leaking tokens.
	if err := tgCh.Send(context.Background(), tgCfg, ColabOneNotificationEvent{
		Title:      "TG Redirect",
		ImageBytes: makeTestPNG(t, 64, 64),
	}); err == nil || strings.Contains(err.Error(), botToken) {
		t.Fatalf("expected clean Telegram redirect error without token leak, got: %v", err)
	}
	if err := barkCh.Send(context.Background(), barkCfg, ColabOneNotificationEvent{Title: "Bark Redirect"}); err == nil || strings.Contains(err.Error(), barkKey) {
		t.Fatalf("expected clean Bark redirect error without key leak, got: %v", err)
	}
	if redirectTargetHits.Load() != 0 {
		t.Fatalf("redirect target must NEVER be contacted, got %d hits", redirectTargetHits.Load())
	}
	if injectedClient.CheckRedirect != nil {
		t.Fatalf("channel must clone injected client rather than mutating it")
	}

	// 2. HTTP 200 non-JSON HTML must be rejected WITHOUT reflecting response body (and Telegram sendPhoto must not fall back).
	mu.Lock()
	mode = "html200"
	tgPaths = nil
	mu.Unlock()

	if err := tgCh.Send(context.Background(), tgCfg, ColabOneNotificationEvent{
		Title:      "TG HTML 200",
		ImageBytes: makeTestPNG(t, 64, 64),
	}); err == nil || strings.Contains(err.Error(), secretBodyHTML) {
		t.Fatalf("expected Telegram non-JSON 200 rejection without body reflection, got: %v", err)
	}
	mu.Lock()
	if len(tgPaths) != 1 || !strings.HasSuffix(tgPaths[0], "/sendPhoto") {
		t.Fatalf("expected no sendMessage fallback on non-JSON 200, got paths: %v", tgPaths)
	}
	mu.Unlock()

	if err := barkCh.Send(context.Background(), barkCfg, ColabOneNotificationEvent{Title: "Bark HTML 200"}); err == nil || strings.Contains(err.Error(), secretBodyHTML) {
		t.Fatalf("expected Bark non-JSON 200 rejection without body reflection, got: %v", err)
	}

	// 3. HTTP 500 non-JSON HTML must NOT reflect raw upstream body.
	mu.Lock()
	mode = "html500"
	mu.Unlock()

	if err := tgCh.Send(context.Background(), tgCfg, ColabOneNotificationEvent{Title: "TG HTML 500"}); err == nil || strings.Contains(err.Error(), secretBodyHTML) {
		t.Fatalf("expected Telegram non-JSON 500 error without body reflection, got: %v", err)
	}
	if err := barkCh.Send(context.Background(), barkCfg, ColabOneNotificationEvent{Title: "Bark HTML 500"}); err == nil || strings.Contains(err.Error(), secretBodyHTML) {
		t.Fatalf("expected Bark non-JSON 500 error without body reflection, got: %v", err)
	}

	// 4. JSON error descriptions truncated <= 200 runes and redacted.
	mu.Lock()
	mode = "longjson"
	mu.Unlock()

	if err := tgCh.Send(context.Background(), tgCfg, ColabOneNotificationEvent{Title: "TG Long JSON"}); err == nil {
		t.Fatalf("expected Telegram JSON error")
	} else if strings.Contains(err.Error(), botToken) || utf8.RuneCountInString(err.Error()) > 260 {
		t.Fatalf("expected truncated redacted Telegram JSON error, got len=%d err=%v", utf8.RuneCountInString(err.Error()), err)
	}
	if err := barkCh.Send(context.Background(), barkCfg, ColabOneNotificationEvent{Title: "Bark Long JSON"}); err == nil {
		t.Fatalf("expected Bark JSON error")
	} else if strings.Contains(err.Error(), barkKey) || utf8.RuneCountInString(err.Error()) > 260 {
		t.Fatalf("expected truncated redacted Bark JSON error, got len=%d err=%v", utf8.RuneCountInString(err.Error()), err)
	}

	// 5. Defense-in-depth validation in LoadConfigFromMaps and send-time validation for hand-edited settings.
	loaded := LoadConfigFromMaps(map[string]any{
		"telegram": map[string]any{"enabled": true, "api_base_url": "ftp://evil.example.com", "chat_id": "1"},
		"bark":     map[string]any{"enabled": true, "server_url": "https://example.com:"},
		"email":    map[string]any{"enabled": true, "smtp_host": "smtp.example.com:587"},
	}, nil)
	if loaded.Telegram.Enabled || loaded.Telegram.APIBaseURL != "" {
		t.Fatalf("expected invalid hand-edited telegram api_base_url sanitized & disabled: %#v", loaded.Telegram)
	}
	if loaded.Bark.Enabled || loaded.Bark.ServerURL != COLABONE_DEFAULT_BARK_SERVER_URL {
		t.Fatalf("expected invalid hand-edited bark server_url sanitized & disabled: %#v", loaded.Bark)
	}
	if loaded.Email.Enabled || loaded.Email.SMTPHost != "" {
		t.Fatalf("expected invalid hand-edited email smtp_host sanitized & disabled: %#v", loaded.Email)
	}

	badTGCfg := tgCfg
	badTGCfg.Telegram.APIBaseURL = "https://example.com:"
	if err := tgCh.Send(context.Background(), badTGCfg, ColabOneNotificationEvent{Title: "Bad URL"}); err == nil {
		t.Fatalf("expected send-time rejection for invalid Telegram APIBaseURL")
	}
	badBarkCfg := barkCfg
	badBarkCfg.Bark.ServerURL = "ftp://evil.example.com"
	if err := barkCh.Send(context.Background(), badBarkCfg, ColabOneNotificationEvent{Title: "Bad URL"}); err == nil {
		t.Fatalf("expected send-time rejection for invalid Bark ServerURL")
	}
}

func TestTelegramOutboundLogUnwrapProxyRedirectSOCKS5AndHTMLFormat(t *testing.T) {
	const botToken = "123456789:AAH_OutboundProxyToken_7788"

	// Target TLS server with self-signed cert.
	var lastFilename string
	var mu sync.Mutex
	tlsTarget := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendPhoto") {
			if err := r.ParseMultipartForm(8 << 20); err == nil {
				if _, hdr, err := r.FormFile("photo"); err == nil && hdr != nil {
					mu.Lock()
					lastFilename = hdr.Filename
					mu.Unlock()
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer tlsTarget.Close()

	// Local HTTP CONNECT proxy.
	proxyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen proxy: %v", err)
	}
	defer proxyLn.Close()
	go func() {
		for {
			c, acceptErr := proxyLn.Accept()
			if acceptErr != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				br := bufio.NewReader(conn)
				req, readErr := http.ReadRequest(br)
				if readErr != nil || req.Method != http.MethodConnect {
					return
				}
				up, dialErr := net.DialTimeout("tcp", req.Host, 3*time.Second)
				if dialErr != nil {
					return
				}
				defer up.Close()
				_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
				var wg sync.WaitGroup
				wg.Add(2)
				go func() { defer wg.Done(); _, _ = io.Copy(up, br) }()
				go func() { defer wg.Done(); _, _ = io.Copy(conn, up) }()
				wg.Wait()
			}(c)
		}
	}()

	logPath := filepath.Join(t.TempDir(), "logs", outboundlog.COLABONE_OUTBOUND_LOG_FILENAME)
	outLogger := outboundlog.NewColabOneOutboundLogger(logPath, 64*1024)
	defer outLogger.Close()

	wrappedBase := outboundlog.WrapHTTPClient(tlsTarget.Client(), outLogger, "notifier_http", "")
	ch := newColabOneTelegramChannel(wrappedBase)

	cfg := DefaultConfig()
	cfg.Telegram = ColabOneTelegramChannelConfig{
		Enabled:          true,
		BotToken:         botToken,
		ChatID:           "42",
		APIBaseURL:       tlsTarget.URL,
		ProxyURL:         "http://" + proxyLn.Addr().String(),
		SendPreviewImage: true,
	}

	longUploadName := "/tmp/dir/" + strings.Repeat("A", 90) + ".PNG"
	if err := ch.Send(context.Background(), cfg, ColabOneNotificationEvent{
		Title:         "Wrapped Proxy Test",
		ImageBytes:    makeTestPNG(t, 100, 100),
		ImageFilename: longUploadName,
	}); err != nil {
		t.Fatalf("Telegram via outboundlog-wrapped TLS client + proxy failed: %v", err)
	}

	mu.Lock()
	gotName := lastFilename
	mu.Unlock()
	if !strings.HasSuffix(gotName, ".jpg") || strings.Contains(strings.ToLower(gotName), ".png") || len(gotName) > 64 {
		t.Fatalf("expected case-insensitive .PNG stripped and capped <= 64 chars, got %q (len=%d)", gotName, len(gotName))
	}

	rawLog, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("expected outbound log written for proxied Telegram request: %v", err)
	}
	if strings.Contains(string(rawLog), botToken) {
		t.Fatalf("bot token leaked into outbound log: %s", string(rawLog))
	}
	if !strings.Contains(string(rawLog), proxyLn.Addr().String()) {
		t.Fatalf("expected proxied request logged with proxy label, got: %s", string(rawLog))
	}

	// SOCKS5 server demanding 0x02 auth when client has NO auth must be rejected immediately.
	socksLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen socks5: %v", err)
	}
	defer socksLn.Close()
	go func() {
		c, acceptErr := socksLn.Accept()
		if acceptErr != nil {
			return
		}
		defer c.Close()
		var hdr [3]byte
		_, _ = io.ReadFull(c, hdr[:])
		_, _ = c.Write([]byte{0x05, 0x02})
	}()
	cfg.Telegram.ProxyURL = "socks5h://" + socksLn.Addr().String()
	if err := ch.Send(context.Background(), cfg, ColabOneNotificationEvent{Title: "NoAuth 0x02"}); err == nil {
		t.Fatalf("expected SOCKS5 error when proxy demands 0x02 without configured credentials")
	}

	// colabOneFormatTelegramHTML drops hint alone while preserving fields before trimming fields.
	ev := ColabOneNotificationEvent{
		Title:    "Keep Fields",
		Body:     strings.Repeat("b", 180),
		Fields:   []ColabOneNotificationField{{Label: "GPU", Value: "A100"}, {Label: "Time", Value: "12s"}},
		ActionHint: strings.Repeat("h", 120),
		Locale:   "en-US",
	}
	formatted := colabOneFormatTelegramHTML(ev, 290)
	if utf8.RuneCountInString(formatted) > 290 {
		t.Fatalf("formatted HTML exceeded maxRunes: %d", utf8.RuneCountInString(formatted))
	}
	if !strings.Contains(formatted, "A100") || !strings.Contains(formatted, "12s") {
		t.Fatalf("expected dropping hint alone to preserve fields, got: %s", formatted)
	}

	// Cancelled context after sendPhoto failure must NOT attempt sendMessage fallback.
	var fallbackHits atomic.Int32
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancelSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendPhoto") {
			cancel()
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"photo fail"}`))
			return
		}
		fallbackHits.Add(1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer cancelSrv.Close()

	cfgCancel := DefaultConfig()
	cfgCancel.Telegram = ColabOneTelegramChannelConfig{
		Enabled:          true,
		BotToken:         botToken,
		ChatID:           "42",
		APIBaseURL:       cancelSrv.URL,
		SendPreviewImage: true,
	}
	if err := newColabOneTelegramChannel(cancelSrv.Client()).Send(cancelCtx, cfgCancel, ColabOneNotificationEvent{
		Title:      "Cancelled",
		ImageBytes: makeTestPNG(t, 64, 64),
	}); err == nil {
		t.Fatalf("expected error on cancelled context")
	}
	if fallbackHits.Load() != 0 {
		t.Fatalf("expected zero sendMessage fallback calls after context cancellation, got %d", fallbackHits.Load())
	}
}

func TestEmailRFC5322HeadersFoldingAndTemplateImprovements(t *testing.T) {
	recipients := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		recipients = append(recipients, fmt.Sprintf("recipient-%02d@very-long-domain-example.com", i))
	}
	ev := ColabOneNotificationEvent{
		Kind:          EventOutputReady,
		Severity:      SeveritySuccess,
		Title:         "Long ASCII Notification Subject Line With Many Words To Ensure Proper RFC 5322 Folding Below Seventy Eight Characters",
		Subtitle:      "Subtitle text",
		Body:          "Body text",
		Fields:        []ColabOneNotificationField{{Label: "GPU", Value: "Tesla T4"}, {Label: "EmptyVal", Value: "   "}},
		ImageBytes:    makeTestPNG(t, 128, 128),
		ImageFilename: "sample.png",
		Locale:        "en-US",
		OccurredAt:    time.Date(2026, 11, 28, 12, 30, 0, 0, time.UTC),
	}

	specialFromName := `ColabOne, Inc. (Alerts) <"ops":team>`
	rawMIME, err := colabOneBuildEmailMIME(specialFromName, "alerts@example.com", recipients, ev, makeTestJPEG(t, 64, 64))
	if err != nil {
		t.Fatalf("colabOneBuildEmailMIME failed: %v", err)
	}

	// Verify header section line lengths & folding.
	headerEnd := bytes.Index(rawMIME, []byte("\r\n\r\n"))
	if headerEnd < 0 {
		t.Fatalf("missing MIME header separator")
	}
	headerBlock := string(rawMIME[:headerEnd])
	for _, line := range strings.Split(headerBlock, "\r\n") {
		if len(line) > 998 {
			t.Fatalf("header line exceeded 998 octets (%d): %q", len(line), line)
		}
	}
	if !strings.Contains(headerBlock, ",\r\n ") {
		t.Fatalf("expected folded To header with CRLF+space")
	}

	parsedMsg, err := mail.ReadMessage(bytes.NewReader(rawMIME))
	if err != nil {
		t.Fatalf("mail.ReadMessage failed: %v", err)
	}

	// From: must be RFC 5322 parseable with exact display name containing specials.
	parsedFrom, err := mail.ParseAddress(parsedMsg.Header.Get("From"))
	if err != nil {
		t.Fatalf("mail.ParseAddress(From=%q) failed: %v", parsedMsg.Header.Get("From"), err)
	}
	if parsedFrom.Name != specialFromName || parsedFrom.Address != "alerts@example.com" {
		t.Fatalf("From round-trip mismatch: got Name=%q Address=%q", parsedFrom.Name, parsedFrom.Address)
	}

	// To: must parse all 20 recipients cleanly.
	parsedTo, err := mail.ParseAddressList(parsedMsg.Header.Get("To"))
	if err != nil || len(parsedTo) != len(recipients) {
		t.Fatalf("To round-trip mismatch: len=%d err=%v", len(parsedTo), err)
	}

	// Subject: decoded cleanly and every raw Subject line <= 78 bytes.
	wd := new(mime.WordDecoder)
	decodedSubj, err := wd.DecodeHeader(parsedMsg.Header.Get("Subject"))
	if err != nil || !strings.Contains(decodedSubj, ev.Title) {
		t.Fatalf("Subject decode mismatch: got %q err=%v", decodedSubj, err)
	}

	// Content-Type must specify multipart/related; type="multipart/alternative".
	ct, params, err := mime.ParseMediaType(parsedMsg.Header.Get("Content-Type"))
	if err != nil || ct != "multipart/related" || params["type"] != "multipart/alternative" {
		t.Fatalf("expected multipart/related with type=multipart/alternative, got ct=%q params=%v err=%v", ct, params, err)
	}

	// Unbreakable long ASCII subject and long Chinese subject must fold every line <= 78 bytes and decode accurately.
	for _, subj := range []string{
		strings.Repeat("X", 140),
		"[ColabOne · 已完成] " + strings.Repeat("模型下载与推理验证完成通知测试", 6),
	} {
		formatted := colabOneFormatSubjectHeader(subj)
		for i, line := range strings.Split(formatted, "\r\n") {
			prefix := 1 // leading space already included on continuation lines except first line
			if i == 0 {
				prefix = len("Subject: ")
			} else {
				prefix = 0
			}
			if prefix+len(line) > 78 {
				t.Fatalf("folded subject line %d exceeded 78 bytes (%d): %q", i, prefix+len(line), line)
			}
		}
		unfolded := strings.ReplaceAll(formatted, "\r\n ", " ")
		dec, decErr := wd.DecodeHeader(unfolded)
		if decErr != nil || dec != subj {
			t.Fatalf("subject round-trip mismatch: got %q want %q err=%v", dec, subj, decErr)
		}
	}

	// HTML & plain text template improvements.
	htmlOut := RenderEmailHTML(ev)
	for _, want := range []string{
		`<!--[if mso]><table role="presentation"`,
		`width="514"`,
		`&zwnj;&nbsp;`,
		`2026-11-28 12:30:00 UTC`,
	} {
		if !strings.Contains(htmlOut, want) {
			t.Fatalf("HTML email missing %q", want)
		}
	}
	if strings.Contains(htmlOut, "EmptyVal") {
		t.Fatalf("expected field with empty value skipped in HTML email")
	}
	plainOut := RenderEmailPlainText(ev)
	if !strings.Contains(plainOut, "2026-11-28 12:30:00 UTC") || !strings.Contains(plainOut, "sample.png") {
		t.Fatalf("expected timezone and preview attachment mention in plain-text email, got:\n%s", plainOut)
	}
}

func TestEmailSMTPAuth535NoDoubleLogin504ReturnsLoginErrAndQuitReset(t *testing.T) {
	const (
		smtpUser = "sender@colabone.local"
		smtpPass = "CorrectPass_1234"
	)

	startCustomSMTP := func(t *testing.T, ehloLines []string, onPlain func() string, onLogin func(u, p string) string, resetOnQuit bool) (int, *int32, *int32, func()) {
		t.Helper()
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen custom SMTP: %v", err)
		}
		var plainCount, loginCount int32
		go func() {
			for {
				conn, acceptErr := ln.Accept()
				if acceptErr != nil {
					return
				}
				go func(c net.Conn) {
					defer c.Close()
					r := bufio.NewReader(c)
					writeLine := func(s string) { _, _ = io.WriteString(c, s+"\r\n") }
					writeLine("220 custom.local ESMTP Ready")
					for {
						line, err := r.ReadString('\n')
						if err != nil {
							return
						}
						trimmed := strings.TrimRight(line, "\r\n")
						upper := strings.ToUpper(trimmed)
						switch {
						case strings.HasPrefix(upper, "EHLO") || strings.HasPrefix(upper, "HELO"):
							for _, el := range ehloLines {
								writeLine(el)
							}
						case strings.HasPrefix(upper, "AUTH PLAIN"):
							atomic.AddInt32(&plainCount, 1)
							writeLine(onPlain())
						case upper == "AUTH LOGIN":
							atomic.AddInt32(&loginCount, 1)
							writeLine("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
							uLine, err := r.ReadString('\n')
							if err != nil {
								return
							}
							uBytes, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(uLine))
							writeLine("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
							pLine, err := r.ReadString('\n')
							if err != nil {
								return
							}
							pBytes, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(pLine))
							writeLine(onLogin(string(uBytes), string(pBytes)))
						case strings.HasPrefix(upper, "MAIL FROM:"), strings.HasPrefix(upper, "RCPT TO:"):
							writeLine("250 OK")
						case upper == "DATA":
							writeLine("354 Start")
							for {
								dl, err := r.ReadString('\n')
								if err != nil {
									return
								}
								if dl == ".\r\n" || dl == ".\n" {
									break
								}
							}
							writeLine("250 Accepted")
						case upper == "QUIT":
							if resetOnQuit {
								if tc, ok := c.(*net.TCPConn); ok {
									_ = tc.SetLinger(0)
								}
								return
							}
							writeLine("221 Bye")
							return
						default:
							writeLine("502 Unknown")
						}
					}
				}(conn)
			}
		}()
		return ln.Addr().(*net.TCPAddr).Port, &plainCount, &loginCount, func() { _ = ln.Close() }
	}

	makeCfg := func(port int) ColabOneNotificationConfig {
		cfg := DefaultConfig()
		cfg.Email = ColabOneEmailChannelConfig{
			Enabled:     true,
			SMTPHost:    "127.0.0.1",
			SMTPPort:    port,
			Security:    "none",
			Username:    smtpUser,
			Password:    smtpPass,
			FromAddress: smtpUser,
			ToAddresses: "rcpt@example.com",
		}
		return cfg
	}
	ch := newColabOneEmailChannel()
	ev := ColabOneNotificationEvent{Title: "SMTP Edge Case"}

	// 1. 535 bad password on AUTH PLAIN must NOT trigger AUTH LOGIN fallback.
	port1, plain1, login1, stop1 := startCustomSMTP(t,
		[]string{"250-custom.local", "250 AUTH PLAIN LOGIN"},
		func() string { return "535 5.7.8 Bad credentials" },
		func(u, p string) string { return "535 Login should not be called" },
		false,
	)
	defer stop1()
	if err := ch.Send(context.Background(), makeCfg(port1), ev); err == nil || !strings.Contains(err.Error(), "535") {
		t.Fatalf("expected 535 auth error, got: %v", err)
	}
	if atomic.LoadInt32(plain1) != 1 || atomic.LoadInt32(login1) != 0 {
		t.Fatalf("expected 1 PLAIN and 0 LOGIN on 535, got plain=%d login=%d", atomic.LoadInt32(plain1), atomic.LoadInt32(login1))
	}

	// 2. 504 mechanism error on AUTH PLAIN falls back to AUTH LOGIN and returns LOGIN's error on failure.
	port2, plain2, login2, stop2 := startCustomSMTP(t,
		[]string{"250-custom.local", "250 AUTH PLAIN LOGIN"},
		func() string { return "504 PLAIN disabled" },
		func(u, p string) string { return "535 LOGIN_SPECIFIC_FAILURE_CODE" },
		false,
	)
	defer stop2()
	if err := ch.Send(context.Background(), makeCfg(port2), ev); err == nil || !strings.Contains(err.Error(), "LOGIN_SPECIFIC_FAILURE_CODE") {
		t.Fatalf("expected LOGIN error returned after 504 fallback failure, got: %v", err)
	}
	if atomic.LoadInt32(plain2) != 1 || atomic.LoadInt32(login2) != 1 {
		t.Fatalf("expected 1 PLAIN and 1 LOGIN on 504 fallback, got plain=%d login=%d", atomic.LoadInt32(plain2), atomic.LoadInt32(login2))
	}

	// 3. Server advertising NO AUTH with credentials provided returns explicit error advising TLS/STARTTLS.
	port3, _, _, stop3 := startCustomSMTP(t,
		[]string{"250-custom.local", "250 SIZE 10240000"},
		func() string { return "235 OK" },
		func(u, p string) string { return "235 OK" },
		false,
	)
	defer stop3()
	if err := ch.Send(context.Background(), makeCfg(port3), ev); err == nil || !strings.Contains(err.Error(), "AUTH") {
		t.Fatalf("expected explicit missing AUTH error, got: %v", err)
	}

	// 4. Server advertising AUTH without PLAIN or LOGIN returns unsupported-auth error (never blindly sends PLAIN).
	port4, plain4, _, stop4 := startCustomSMTP(t,
		[]string{"250-custom.local", "250 AUTH XOAUTH2 CRAM-MD5"},
		func() string { return "235 OK" },
		func(u, p string) string { return "235 OK" },
		false,
	)
	defer stop4()
	if err := ch.Send(context.Background(), makeCfg(port4), ev); err == nil || atomic.LoadInt32(plain4) != 0 {
		t.Fatalf("expected unsupported-auth error with 0 PLAIN attempts, got err=%v plain=%d", err, atomic.LoadInt32(plain4))
	}

	// 5. Legacy AUTH=LOGIN works AND TCP RST on QUIT after 250 Accepted still succeeds!
	port5, _, login5, stop5 := startCustomSMTP(t,
		[]string{"250-custom.local", "250 AUTH=LOGIN"},
		func() string { return "504 no plain" },
		func(u, p string) string {
			if u == smtpUser && p == smtpPass {
				return "235 OK"
			}
			return "535 Bad"
		},
		true,
	)
	defer stop5()
	if err := ch.Send(context.Background(), makeCfg(port5), ev); err != nil {
		t.Fatalf("expected legacy AUTH=LOGIN + QUIT TCP RST after DATA to succeed, got: %v", err)
	}
	if atomic.LoadInt32(login5) != 1 {
		t.Fatalf("expected 1 LOGIN attempt for AUTH=LOGIN, got %d", atomic.LoadInt32(login5))
	}
}

func TestStrictPatchValidationMaskAndRedactSecrets(t *testing.T) {
	def := DefaultConfig()

	// Nested type mismatches must return clear validation errors instead of being silently ignored.
	badTypePayloads := []map[string]any{
		{"system": map[string]any{"enabled": "true"}},
		{"system": map[string]any{"clear": "yes"}},
		{"telegram": map[string]any{"enabled": "true"}},
		{"telegram": map[string]any{"chat_id": 12345}},
		{"telegram": map[string]any{"send_preview_image": 1}},
		{"bark": map[string]any{"level": true}},
		{"bark": map[string]any{"server_url": 123}},
		{"bark": map[string]any{"enabled": "1"}},
		{"email": map[string]any{"security": 123}},
		{"email": map[string]any{"smtp_host": true}},
		{"email": map[string]any{"embed_preview_image": "yes"}},
		// clear: true combined with enabled: true must be rejected.
		{"system": map[string]any{"clear": true, "enabled": true}},
		{"telegram": map[string]any{"clear": true, "enabled": true}},
		{"bark": map[string]any{"clear": true, "enabled": true}},
		{"email": map[string]any{"clear": true, "enabled": true}},
		// Telegram bot_token forbidding / ? # @ \
		{"telegram": map[string]any{"bot_token": "123456:abc/def"}},
		{"telegram": map[string]any{"bot_token": "123456:abc?x=1"}},
		{"telegram": map[string]any{"bot_token": "123456:abc#frag"}},
		{"telegram": map[string]any{"bot_token": "123456:abc@host"}},
		{"telegram": map[string]any{"bot_token": `123456:abc\def`}},
		// Trailing empty colon in base URLs.
		{"telegram": map[string]any{"api_base_url": "https://example.com:"}},
		// Invalid SMTP host forms.
		{"email": map[string]any{"smtp_host": "smtp.example.com:587"}},
		{"email": map[string]any{"smtp_host": "[::1]:587"}},
		{"email": map[string]any{"smtp_host": "https://smtp.example.com"}},
		{"email": map[string]any{"smtp_host": "smtp.example.com/path"}},
		{"email": map[string]any{"smtp_host": "smtp host"}},
		// Unicode C1 control char (U+0085 NEL).
		{"email": map[string]any{"from_name": "Colab\u0085One"}},
	}
	for i, p := range badTypePayloads {
		if _, err := ApplyPatch(def, p); err == nil {
			t.Fatalf("case %d (%v): expected validation error, got nil", i, p)
		}
	}

	// Bracketed IPv6 SMTP host [::1] normalizes cleanly to ::1.
	ipv6Cfg, err := ApplyPatch(def, map[string]any{"email": map[string]any{"smtp_host": "[::1]"}})
	if err != nil || ipv6Cfg.Email.SMTPHost != "::1" {
		t.Fatalf("expected [::1] normalized to ::1, got host=%q err=%v", ipv6Cfg.Email.SMTPHost, err)
	}

	// Previously broken enabled channel (e.g. lost vault secret) must NOT lock user out of toggling notify_gpu or editing another channel.
	broken := def
	broken.Telegram.Enabled = true
	broken.Telegram.BotToken = "" // lost secret
	broken.Telegram.ChatID = "123"
	updated, err := ApplyPatch(broken, map[string]any{
		"notify_gpu": false,
		"bark": map[string]any{
			"server_url": "https://api.day.app",
			"device_key": "ValidKey12345",
			"enabled":    true,
		},
	})
	if err != nil {
		t.Fatalf("broken untouched channel must not block unrelated patch: %v", err)
	}
	if updated.NotifyGPU || !updated.Bark.Enabled {
		t.Fatalf("expected unrelated patch applied cleanly: %#v", updated)
	}

	// MaskSecret tightening: <=8 -> ****, <=16 -> 2+****+2, >16 -> 4+****+4.
	if got := MaskSecret("12345678"); got != "****" {
		t.Fatalf("MaskSecret(8 runes) = %q, want ****", got)
	}
	if got := MaskSecret("1234567890123456"); got != "12****56" {
		t.Fatalf("MaskSecret(16 runes) = %q, want 12****56", got)
	}
	if got := MaskSecret("12345678901234567"); got != "1234****4567" {
		t.Fatalf("MaskSecret(17 runes) = %q, want 1234****4567", got)
	}

	// RedactSecrets catches :pass@host, token@host, uppercase schemes, Basic auth, proxy username >=4, base64 email pass & AUTH PLAIN, and %3A bot tokens.
	secCfg := def
	secCfg.Telegram.ProxyURL = "http://proxyuser:proxypass99@127.0.0.1:7890"
	secCfg.Email.Username = "mail@example.com"
	secCfg.Email.Password = "EmailSecretPass77"
	b64Pass := base64.StdEncoding.EncodeToString([]byte(secCfg.Email.Password))
	b64Plain := base64.StdEncoding.EncodeToString([]byte("\x00" + secCfg.Email.Username + "\x00" + secCfg.Email.Password))

	raw := fmt.Sprintf(
		"HTTPS://:onlypass@proxy.test http://onlytoken@proxy.test Authorization: Basic dXNlcjpwYXNz user=proxyuser b64=%s plain=%s tg=bot123456%%3AABCDEFGH99",
		b64Pass, b64Plain,
	)
	redacted := RedactSecrets(raw, secCfg)
	for _, secret := range []string{"onlypass", "onlytoken", "dXNlcjpwYXNz", "proxyuser", b64Pass, b64Plain, "ABCDEFGH99"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("RedactSecrets leaked %q in: %s", secret, redacted)
		}
	}

	// zh-hant-mo normalizes to zh-HK.
	if got := NormalizeLocale("zh-hant-mo"); got != "zh-HK" {
		t.Fatalf("NormalizeLocale(zh-hant-mo) = %q, want zh-HK", got)
	}
}

func TestDispatcherConcurrencyPanicRecoveryDedupeCapAndSystemErrors(t *testing.T) {
	// 1. Slow remote channel on Event 1 must NOT head-of-line block instant system notification on Event 2.
	releaseSlow := make(chan struct{})
	slowEntered := make(chan struct{}, 1)
	slowBark := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slowEntered <- struct{}{}:
		default:
		}
		select {
		case <-releaseSlow:
		case <-r.Context().Done():
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"message":"ok"}`))
	}))
	defer slowBark.Close()

	var (
		mu       sync.Mutex
		sysReqs  []ColabOneSystemNotificationRequest
		gotEv2Ch = make(chan struct{}, 1)
	)

	cfg := DefaultConfig()
	cfg.SystemOnlyWhenUnfocused = false
	cfg.System.Enabled = true
	cfg.Bark.Enabled = true
	cfg.Bark.ServerURL = slowBark.URL
	cfg.Bark.DeviceKey = "SlowKey12345"

	n := NewColabOneNotifier(ColabOneNotifierConfig{
		ConfigProvider: func() ColabOneNotificationConfig { return cfg },
		SystemSender: func(_ context.Context, req ColabOneSystemNotificationRequest) error {
			mu.Lock()
			sysReqs = append(sysReqs, req)
			mu.Unlock()
			if req.Title == "Instant Event 2" {
				select {
				case gotEv2Ch <- struct{}{}:
				default:
				}
			}
			return nil
		},
		SystemSupported: func() bool { return true },
		HTTPClient:      slowBark.Client(),
		ChannelTimeout:  5 * time.Second,
	})

	imgBuf := makeTestPNG(t, 32, 32)
	fields := []ColabOneNotificationField{{Label: "K", Value: "V1"}}
	n.Notify(ColabOneNotificationEvent{
		Kind:       EventGPUAllocated,
		Title:      "Slow Event 1",
		ImageBytes: imgBuf,
		Fields:     fields,
	})
	// Immediately mutate caller slices after Notify to verify defensive cloning causes zero data race.
	imgBuf[0] ^= 0xff
	fields[0].Value = "MUTATED"

	<-slowEntered
	n.Notify(ColabOneNotificationEvent{
		Kind:  EventGPUAllocated,
		Title: "Instant Event 2",
	})

	select {
	case <-gotEv2Ch:
	case <-time.After(2 * time.Second):
		close(releaseSlow)
		n.Close()
		t.Fatalf("Event 2 was head-of-line blocked by slow remote channel on Event 1")
	}
	close(releaseSlow)
	n.Close()

	// 2. Panic recovery inside DispatchSync and TestChannel.
	panicNotifier := NewColabOneNotifier(ColabOneNotifierConfig{
		ConfigProvider: func() ColabOneNotificationConfig {
			c := DefaultConfig()
			c.SystemOnlyWhenUnfocused = false
			c.System.Enabled = true
			return c
		},
		SystemSender: func(_ context.Context, _ ColabOneSystemNotificationRequest) error {
			panic("simulated sender crash")
		},
		SystemSupported: func() bool { return true },
		ChannelTimeout:  2 * time.Second,
	})
	defer panicNotifier.Close()

	res := panicNotifier.DispatchSync(context.Background(), ColabOneNotificationEvent{
		Kind:  EventGPUAllocated,
		Title: "Panic Test",
	})
	if res[ChannelSystem] == nil {
		t.Fatalf("expected recovered error from panicking channel in DispatchSync")
	}
	if err := panicNotifier.TestChannel(context.Background(), ChannelSystem, "zh-CN"); err == nil {
		t.Fatalf("expected recovered error from panicking channel in TestChannel")
	}

	// 3. Dedupe map cap (1024) and clock-rollback safety.
	baseTime := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 1200; i++ {
		if ok := panicNotifier.checkAndRecordDedupe(fmt.Sprintf("key-%04d", i), baseTime.Add(time.Duration(i)*time.Millisecond)); !ok {
			t.Fatalf("expected unique key %d accepted", i)
		}
	}
	panicNotifier.mu.Lock()
	dedupeLen := len(panicNotifier.dedupe)
	_, hasOldest := panicNotifier.dedupe["key-0000"]
	panicNotifier.mu.Unlock()
	if dedupeLen > colabOneMaxDedupeEntries {
		t.Fatalf("dedupe map exceeded cap %d: got %d", colabOneMaxDedupeEntries, dedupeLen)
	}
	if hasOldest {
		t.Fatalf("expected oldest dedupe key evicted when cap reached")
	}
	// Clock rollback (now < stored timestamp) treats entry as expired instead of permanently blocking.
	if ok := panicNotifier.checkAndRecordDedupe("key-1199", baseTime.Add(-10*time.Second)); !ok {
		t.Fatalf("expected clock rollback to expire existing dedupe entry")
	}

	// 4. Narrowed colabOneMapSystemError does not misclassify generic filesystem permission errors.
	ioPermErr := fmt.Errorf("open /tmp/icon.png: %w", os.ErrPermission)
	if errors.Is(colabOneMapSystemError(ioPermErr), ErrSystemPermissionDenied) {
		t.Fatalf("generic os.ErrPermission must not be mapped to ErrSystemPermissionDenied")
	}
	if errors.Is(colabOneMapSystemError(errors.New("write temp file: permission denied")), ErrSystemPermissionDenied) {
		t.Fatalf("generic permission string must not be mapped to ErrSystemPermissionDenied")
	}
	if !errors.Is(colabOneMapSystemError(errors.New("notification permission denied")), ErrSystemPermissionDenied) {
		t.Fatalf("notification-specific permission denial should map to ErrSystemPermissionDenied")
	}
	if !errors.Is(colabOneMapSystemError(ErrSystemPermissionDenied), ErrSystemPermissionDenied) {
		t.Fatalf("sentinel ErrSystemPermissionDenied must be preserved")
	}
}
