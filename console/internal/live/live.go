// Package live provides pure-Go Live Studio schemas, validators, and secret
// masking helpers shared by the ColabOne desktop controller.
package live

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// ColabOneLiveDefaultVisualEngine is the default real-time visual driver.
	ColabOneLiveDefaultVisualEngine = "flashhead_lite"
	// ColabOneLiveDefaultTTSEngine is the default streaming TTS backend.
	ColabOneLiveDefaultTTSEngine = "qwen3_tts"
	// ColabOneLiveDefaultLLMMode is the default Live Studio dialogue LLM backend mode.
	ColabOneLiveDefaultLLMMode = "local_gguf"
	// ColabOneLiveDefaultDirectorMode is the default interaction mode.
	ColabOneLiveDefaultDirectorMode = "dialogue"
	// ColabOneLiveDefaultMicMode is the default microphone capture mode.
	ColabOneLiveDefaultMicMode = "vad"
	// ColabOneLiveDefaultResolution is the default output canvas resolution.
	ColabOneLiveDefaultResolution = "720x1280"

	// ColabOneLiveMaxVideoClipBytes is the maximum decoded size for uploaded video clips (128 MiB).
	ColabOneLiveMaxVideoClipBytes = 128 * 1024 * 1024
	// ColabOneLiveMaxBGMBytes is the maximum decoded size for uploaded BGM audio tracks (32 MiB).
	ColabOneLiveMaxBGMBytes = 32 * 1024 * 1024
	// ColabOneLiveMaxAudioClipBytes aliases ColabOneLiveMaxBGMBytes for backward compatibility (32 MiB).
	ColabOneLiveMaxAudioClipBytes = ColabOneLiveMaxBGMBytes
	// ColabOneLiveMaxPortraitBytes is the maximum decoded size for an inline avatar portrait (16 MiB).
	ColabOneLiveMaxPortraitBytes = 16 * 1024 * 1024

	colabOneLiveMaxSecretRunes       = 512
	colabOneLiveMaxURLRunes          = 512
	colabOneLiveMaxModelRunes        = 160
	colabOneLiveMaxLocalModelIDRunes = 120
	colabOneLiveMaxPromptRunes       = 4000
	colabOneLiveMaxNameRunes         = 120
	colabOneLiveMaxLabelRunes        = 80
	colabOneLiveMaxPriceRunes        = 64
	colabOneLiveMaxTagRunes          = 40
	colabOneLiveMaxHighlightsRunes   = 600
	colabOneLiveMaxScriptHintRunes   = 1000
	colabOneLiveMaxKeywordRunes      = 40
	colabOneLiveMaxKeywords          = 32
	colabOneLiveMaxAvatarActions     = 64
	colabOneLiveMaxAvatarProducts    = 64
	colabOneLiveMaxBuildPackActions  = 16
	colabOneLiveMaxBuildPackDances   = 8
	colabOneLiveMaxFilenameRunes     = 255
	colabOneLiveMaxRelPathRunes      = 512
)

// Allowed visual engines, TTS engines, LLM modes, director modes, mic modes,
// action kinds, resolution presets, and media extensions aligned with common/live_studio.py.
var (
	ColabOneLiveVisualEngines = map[string]bool{
		"flashhead_lite": true,
		"musetalk_bank":  true,
	}

	ColabOneLiveTTSEngines = map[string]bool{
		"qwen3_tts": true,
		"voxcpm2":   true,
	}

	ColabOneLiveLLMModes = map[string]bool{
		"local_gguf":   true,
		"external_api": true,
	}

	ColabOneLiveDirectorModes = map[string]bool{
		"dialogue": true,
		"director": true,
	}

	ColabOneLiveMicModes = map[string]bool{
		"vad": true,
		"ptt": true,
	}

	ColabOneLiveActionKinds = map[string]bool{
		"loop":              true,
		"oneshot":           true,
		"dance":             true,
		"personalive_drive": true,
	}

	ColabOneLiveResolutions = map[string][2]int{
		"720x1280":  {720, 1280},
		"1280x720":  {1280, 720},
		"1080x1920": {1080, 1920},
		"1920x1080": {1920, 1080},
		"512x512":   {512, 512},
	}

	ColabOneLiveVideoExtensions = map[string]bool{
		".mp4":  true,
		".webm": true,
		".mov":  true,
		".mkv":  true,
	}

	ColabOneLiveAudioExtensions = map[string]bool{
		".mp3":  true,
		".wav":  true,
		".flac": true,
		".ogg":  true,
		".m4a":  true,
	}

	ColabOneLivePortraitExtensions = map[string]bool{
		".png":  true,
		".jpg":  true,
		".jpeg": true,
		".webp": true,
	}

	colabOneLiveAvatarIDRe    = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
	colabOneLiveVoicePresetRe = regexp.MustCompile(`^(?:[a-zA-Z0-9_-]{1,64})?$`)
	colabOneLiveAssetIDRe     = regexp.MustCompile(`^(?:[a-f0-9]{32})?$`)
	colabOneLiveResolutionRe  = regexp.MustCompile(`^(\d{3,4})x(\d{3,4})$`)

	colabOneBlockedMetadataHosts = map[string]bool{
		"metadata.google.internal": true,
		"metadata.goog":            true,
		"instance-data":            true,
		"metadata":                 true,
	}

	colabOneNonGlobalPrefixes = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("240.0.0.0/4"),
		netip.MustParsePrefix("64:ff9b::/96"),
		netip.MustParsePrefix("64:ff9b:1::/48"),
		netip.MustParsePrefix("100::/64"),
		netip.MustParsePrefix("2001::/32"),
		netip.MustParsePrefix("2001:2::/48"),
		netip.MustParsePrefix("2001:db8::/32"),
		netip.MustParsePrefix("2002::/16"),
		netip.MustParsePrefix("fec0::/10"),
	}

	colabOneExternalLLMAllowedKeys = map[string]bool{
		"base_url":    true,
		"api_key":     true,
		"model":       true,
		"temperature": true,
		"max_tokens":  true,
	}

	colabOneStartConfigAllowedKeys = map[string]bool{
		"visual_engine":       true,
		"personalive_enabled": true,
		"tts_engine":          true,
		"voice_preset":        true,
		"llm_mode":            true,
		"local_llm_model_id":  true,
		"director_mode":       true,
		"mic_mode":            true,
		"avatar_id":           true,
		"target_fps":          true,
		"resolution":          true,
		"width":               true,
		"height":              true,
		"enable_rtmp":         true,
		"rtmp_url":            true,
		"external_llm":        true,
		"hf_token":            true,
		"portrait_input_id":   true,
		"canvas_config":       true,
	}

	colabOneAvatarSaveAllowedKeys = map[string]bool{
		"id":                   true,
		"avatar_id":            true,
		"name":                 true,
		"persona_prompt":       true,
		"portrait_input_id":    true,
		"portrait_b64":         true,
		"portrait_data":        true,
		"portrait_filename":    true,
		"default_tts_engine":   true,
		"default_voice_preset": true,
		"actions":              true,
		"products":             true,
	}

	colabOneActionSpecAllowedKeys = map[string]bool{
		"id":               true,
		"label":            true,
		"kind":             true,
		"clip_path":        true,
		"bgm_path":         true,
		"duration_seconds": true,
		"trigger_keywords": true,
	}

	colabOneProductItemAllowedKeys = map[string]bool{
		"id":             true,
		"name":           true,
		"price":          true,
		"tag":            true,
		"highlights":     true,
		"script_hint":    true,
		"image_input_id": true,
	}

	colabOneAvatarClipAllowedKeys = map[string]bool{
		"action_id":        true,
		"label":            true,
		"kind":             true,
		"filename":         true,
		"data":             true,
		"bgm_filename":     true,
		"bgm_data":         true,
		"duration_seconds": true,
		"trigger_keywords": true,
	}

	colabOneDrivingUploadAllowedKeys = map[string]bool{
		"id":               true,
		"label":            true,
		"filename":         true,
		"data":             true,
		"bgm_filename":     true,
		"bgm_data":         true,
		"duration_seconds": true,
		"trigger_keywords": true,
	}

	colabOneBuildPackAllowedKeys = map[string]bool{
		"avatar_id":         true,
		"name":              true,
		"portrait_input_id": true,
		"portrait_b64":      true,
		"portrait_data":     true,
		"portrait_filename": true,
		"actions":           true,
		"dance_ids":         true,
		"resolution":        true,
		"fps":               true,
		"hf_token":          true,
		"overwrite":         true,
	}
)

// MaskColabOneSecret masks a sensitive token or API key for safe display in
// snapshots and logs. Returns "" when empty, "***" when shorter than 14 runes,
// and "<prefix>***<last4>" for secrets of 14 or more runes.
func MaskColabOneSecret(secret string) string {
	trimmed := strings.TrimSpace(secret)
	if trimmed == "" {
		return ""
	}
	runes := []rune(trimmed)
	if len(runes) < 14 {
		return "***"
	}
	return string(runes[:3]) + "***" + string(runes[len(runes)-4:])
}

// ColabOneNormalizedURLOrigin parses an http/https URL and returns its
// normalized origin string in the form "scheme://host[:port]" (lowercased,
// single trailing DNS root dot stripped, default ports :443 for https and :80
// for http stripped, IPv6 literals bracketed and canonicalized).
func ColabOneNormalizedURLOrigin(raw string) (string, error) {
	if utf8.RuneCountInString(raw) > colabOneLiveMaxURLRunes {
		return "", errors.New("URL 长度超过限制")
	}
	for _, ch := range raw {
		if ch == '\r' || ch == '\n' || ch == 0 || ch < 0x20 || ch == 0x7f || unicode.IsControl(ch) {
			return "", errors.New("URL 包含非法控制字符")
		}
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("URL 不能为空")
	}
	for _, ch := range trimmed {
		if unicode.IsSpace(ch) {
			return "", errors.New("URL 不能包含空白字符")
		}
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Opaque != "" || u.User != nil {
		return "", errors.New("URL 格式无效")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("URL 仅支持 http 或 https 协议")
	}
	if u.Host == "" || strings.HasSuffix(u.Host, ":") {
		return "", errors.New("URL 主机名或端口无效")
	}
	rawHost := strings.ToLower(u.Hostname())
	host := strings.TrimSuffix(rawHost, ".")
	if host == "" || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return "", errors.New("URL 缺少有效主机名")
	}

	formattedHost := host
	if addr, addrErr := netip.ParseAddr(host); addrErr == nil {
		formattedHost = addr.String()
		if addr.Is6() {
			formattedHost = "[" + formattedHost + "]"
		}
	} else if strings.Contains(formattedHost, ":") {
		return "", errors.New("URL 主机名无效")
	}

	portSuffix := ""
	if port := u.Port(); port != "" {
		p, pErr := strconv.Atoi(port)
		if pErr != nil || p < 1 || p > 65535 {
			return "", errors.New("URL 端口不合法")
		}
		if !((scheme == "https" && p == 443) || (scheme == "http" && p == 80)) {
			portSuffix = ":" + strconv.Itoa(p)
		}
	}

	return scheme + "://" + formattedHost + portSuffix, nil
}

// IsColabOneLoopbackURL reports whether rawURL uses http or https targeting an
// exact loopback host (127.0.0.1, localhost, or ::1).
func IsColabOneLoopbackURL(rawURL string) bool {
	if utf8.RuneCountInString(rawURL) > colabOneLiveMaxURLRunes {
		return false
	}
	for _, ch := range rawURL {
		if ch == '\r' || ch == '\n' || ch == 0 || ch < 0x20 || ch == 0x7f || unicode.IsControl(ch) {
			return false
		}
	}
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return false
	}
	for _, ch := range trimmed {
		if unicode.IsSpace(ch) {
			return false
		}
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" {
		return false
	}
	if u.Host == "" || strings.HasSuffix(u.Host, ":") {
		return false
	}
	if port := u.Port(); port != "" {
		p, pErr := strconv.Atoi(port)
		if pErr != nil || p < 1 || p > 65535 {
			return false
		}
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return false
	}
	return isExactLoopbackHost(strings.ToLower(u.Hostname()))
}

func isExactLoopbackHost(host string) bool {
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func isNonGlobalIP(addr netip.Addr) bool {
	ip := addr.Unmap()
	if !ip.IsValid() {
		return true
	}
	if !ip.IsGlobalUnicast() ||
		ip.IsPrivate() ||
		ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() {
		return true
	}
	for _, prefix := range colabOneNonGlobalPrefixes {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

func isBlockedInternalHostname(host string) bool {
	if host == "localhost" || colabOneBlockedMetadataHosts[host] {
		return true
	}
	return strings.HasSuffix(host, ".internal") ||
		strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".localhost")
}

func isAllASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isCIntHostLabel(label string) bool {
	if label == "" {
		return false
	}
	if isAllASCIIDigits(label) {
		return true
	}
	if strings.HasPrefix(label, "0x") && len(label) > 2 {
		for i := 2; i < len(label); i++ {
			c := label[i]
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				return false
			}
		}
		return true
	}
	return false
}

func isSuspiciousNumericIPv4Host(host string) bool {
	labels := strings.Split(host, ".")
	if len(labels) == 0 {
		return true
	}
	allNumeric := true
	for _, label := range labels {
		if label == "" {
			return true
		}
		if !isCIntHostLabel(label) {
			allNumeric = false
		}
	}
	if allNumeric {
		return true
	}
	// A DNS top-level domain label is never purely numeric.
	return isAllASCIIDigits(labels[len(labels)-1])
}

func rejectUnknownKeys(raw map[string]any, allowed map[string]bool, label string) error {
	for k := range raw {
		if !allowed[k] {
			return fmt.Errorf("%s包含不支持的字段：%s", label, k)
		}
	}
	return nil
}

func sanitizeColabOneSecret(raw, fieldName string) (string, error) {
	if utf8.RuneCountInString(raw) > colabOneLiveMaxSecretRunes {
		return "", fmt.Errorf("%s 长度不能超过 %d", fieldName, colabOneLiveMaxSecretRunes)
	}
	for _, ch := range raw {
		if ch == '\r' || ch == '\n' || ch == 0 || ch < 0x20 || ch == 0x7f || unicode.IsControl(ch) {
			return "", fmt.Errorf("%s 包含非法空白或控制字符", fieldName)
		}
	}
	trimmed := strings.Trim(raw, " ")
	for _, ch := range trimmed {
		if unicode.IsSpace(ch) {
			return "", fmt.Errorf("%s 包含非法空白或控制字符", fieldName)
		}
	}
	return trimmed, nil
}

func validateSafeSingleLineString(raw, fieldName string, maxRunes int, allowEmpty bool) (string, error) {
	for _, ch := range raw {
		if ch == '\r' || ch == '\n' || ch == 0 || ch < 0x20 || ch == 0x7f || unicode.IsControl(ch) {
			return "", fmt.Errorf("%s 包含非法控制字符", fieldName)
		}
	}
	trimmed := strings.TrimSpace(raw)
	if !allowEmpty && trimmed == "" {
		return "", fmt.Errorf("请填写%s", fieldName)
	}
	if utf8.RuneCountInString(trimmed) > maxRunes {
		return "", fmt.Errorf("%s 长度不能超过 %d 个字符", fieldName, maxRunes)
	}
	return trimmed, nil
}

func validateSafeTextString(raw, fieldName string, maxRunes int) (string, error) {
	if strings.ContainsRune(raw, 0) {
		return "", fmt.Errorf("%s 包含非法空字符", fieldName)
	}
	trimmed := strings.TrimSpace(raw)
	if utf8.RuneCountInString(trimmed) > maxRunes {
		return "", fmt.Errorf("%s 长度不能超过 %d 个字符", fieldName, maxRunes)
	}
	return trimmed, nil
}

func validateStrictStringField(raw map[string]any, key, fieldName string) (string, bool, error) {
	val, exists := raw[key]
	if !exists || val == nil {
		return "", false, nil
	}
	s, ok := val.(string)
	if !ok {
		return "", true, fmt.Errorf("%s 格式不合法", fieldName)
	}
	return s, true, nil
}

func validateColabOneLiveAssetID(raw, fieldName string) (string, error) {
	for _, ch := range raw {
		if ch == '\r' || ch == '\n' || ch == 0 || ch < 0x20 || ch == 0x7f || unicode.IsControl(ch) {
			return "", fmt.Errorf("%s 不合法", fieldName)
		}
	}
	trimmed := strings.TrimSpace(raw)
	if !colabOneLiveAssetIDRe.MatchString(trimmed) {
		return "", fmt.Errorf("%s 格式不合法", fieldName)
	}
	return trimmed, nil
}

func validateColabOneLiveVoicePreset(raw, fieldName string) (string, error) {
	cleaned, err := validateSafeSingleLineString(raw, fieldName, 64, true)
	if err != nil {
		return "", err
	}
	if !colabOneLiveVoicePresetRe.MatchString(cleaned) {
		return "", fmt.Errorf("%s 格式不合法", fieldName)
	}
	return cleaned, nil
}

func validateColabOneLiveRelativePath(raw, fieldName string) (string, error) {
	for _, ch := range raw {
		if ch == '\r' || ch == '\n' || ch == 0 || ch < 0x20 || ch == 0x7f || unicode.IsControl(ch) {
			return "", fmt.Errorf("%s 不合法", fieldName)
		}
	}
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return "", nil
	}
	if utf8.RuneCountInString(cleaned) > colabOneLiveMaxRelPathRunes ||
		strings.ContainsAny(cleaned, `\:`) ||
		strings.HasPrefix(cleaned, "/") ||
		strings.HasSuffix(cleaned, "/") {
		return "", fmt.Errorf("%s 不合法", fieldName)
	}
	for _, seg := range strings.Split(cleaned, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("%s 包含非法路径段", fieldName)
		}
	}
	return path.Clean(cleaned), nil
}

func parseExactInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int32:
		return int(x), true
	case int64:
		if x < math.MinInt32 || x > math.MaxInt32 {
			return 0, false
		}
		return int(x), true
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) || math.Trunc(x) != x {
			return 0, false
		}
		if x < math.MinInt32 || x > math.MaxInt32 {
			return 0, false
		}
		return int(x), true
	case json.Number:
		s := strings.TrimSpace(string(x))
		if s == "" {
			return 0, false
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			if n < math.MinInt32 || n > math.MaxInt32 {
				return 0, false
			}
			return int(n), true
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f {
			return 0, false
		}
		if f < math.MinInt32 || f > math.MaxInt32 {
			return 0, false
		}
		return int(f), true
	default:
		return 0, false
	}
}

func parseFiniteFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case float32:
		f := float64(x)
		return f, !math.IsNaN(f) && !math.IsInf(f, 0)
	case float64:
		return x, !math.IsNaN(x) && !math.IsInf(x, 0)
	case json.Number:
		f, err := strconv.ParseFloat(strings.TrimSpace(string(x)), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

func containsNonFiniteFloat(v any) bool {
	switch x := v.(type) {
	case float64:
		return math.IsNaN(x) || math.IsInf(x, 0)
	case float32:
		f := float64(x)
		return math.IsNaN(f) || math.IsInf(f, 0)
	case json.Number:
		s := strings.TrimSpace(string(x))
		if s == "" {
			return true
		}
		if _, err := strconv.ParseInt(s, 10, 64); err == nil {
			return false
		}
		if _, err := strconv.ParseUint(s, 10, 64); err == nil {
			return false
		}
		f, err := strconv.ParseFloat(s, 64)
		return err != nil || math.IsNaN(f) || math.IsInf(f, 0)
	case map[string]any:
		for _, item := range x {
			if containsNonFiniteFloat(item) {
				return true
			}
		}
	case []any:
		for _, item := range x {
			if containsNonFiniteFloat(item) {
				return true
			}
		}
	case []map[string]any:
		for _, item := range x {
			if containsNonFiniteFloat(item) {
				return true
			}
		}
	}
	return false
}

const (
	colabOneLiveMinResolutionDim = 256
	colabOneLiveMaxResolutionDim = 2560
)

func validateColabOneLiveDimensions(w, h int) (string, error) {
	if w < colabOneLiveMinResolutionDim || w > colabOneLiveMaxResolutionDim ||
		h < colabOneLiveMinResolutionDim || h > colabOneLiveMaxResolutionDim {
		return "", fmt.Errorf("画布分辨率宽高必须在 %d 到 %d 之间", colabOneLiveMinResolutionDim, colabOneLiveMaxResolutionDim)
	}
	if w%2 != 0 || h%2 != 0 {
		return "", errors.New("画布分辨率宽高必须为偶数")
	}
	return fmt.Sprintf("%dx%d", w, h), nil
}

func validateColabOneLiveResolution(raw string) (string, error) {
	cleaned := strings.TrimSpace(raw)
	m := colabOneLiveResolutionRe.FindStringSubmatch(cleaned)
	if m == nil {
		return "", fmt.Errorf("不支持的画布分辨率：%s", cleaned)
	}
	w, errW := strconv.Atoi(m[1])
	h, errH := strconv.Atoi(m[2])
	if errW != nil || errH != nil {
		return "", fmt.Errorf("不支持的画布分辨率：%s", cleaned)
	}
	return validateColabOneLiveDimensions(w, h)
}

// ValidateColabOneLiveAvatarID validates a Live Studio identifier
// against ^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$ (aligned with common/live_studio.py).
func ValidateColabOneLiveAvatarID(id string) (string, error) {
	for _, ch := range id {
		if ch == '\r' || ch == '\n' || ch == 0 || ch < 0x20 || ch == 0x7f || unicode.IsControl(ch) {
			return "", errors.New("形象 ID 不合法")
		}
	}
	trimmed := strings.TrimSpace(id)
	if !colabOneLiveAvatarIDRe.MatchString(trimmed) {
		return "", errors.New("形象 ID 不合法（需以字母或数字开头，仅支持 1-64 位字母、数字、下划线和连字符）")
	}
	return trimmed, nil
}

// ValidateColabOneLiveExternalLLMConfig validates an external OpenAI-compatible
// LLM configuration with strict key parity against ColabOneLiveExternalLLMConfig.
func ValidateColabOneLiveExternalLLMConfig(raw map[string]any, requireAPIKey bool) (map[string]any, error) {
	if raw == nil {
		return nil, errors.New("外部 LLM 配置不能为空")
	}
	if containsNonFiniteFloat(raw) {
		return nil, errors.New("外部 LLM 配置包含非法数值")
	}
	if err := rejectUnknownKeys(raw, colabOneExternalLLMAllowedKeys, "外部 LLM 配置"); err != nil {
		return nil, err
	}

	rawURL, _, err := validateStrictStringField(raw, "base_url", "外部 LLM 地址")
	if err != nil {
		return nil, err
	}
	baseURL, err := validateColabOneExternalLLMURL(rawURL)
	if err != nil {
		return nil, err
	}

	rawModel, _, err := validateStrictStringField(raw, "model", "外部 LLM 模型名称")
	if err != nil {
		return nil, err
	}
	model, err := validateSafeSingleLineString(rawModel, "外部 LLM 模型名称", colabOneLiveMaxModelRunes, false)
	if err != nil {
		return nil, err
	}

	apiKey := ""
	if rawKeyVal, hasKey := raw["api_key"]; hasKey && rawKeyVal != nil {
		keyStr, ok := rawKeyVal.(string)
		if !ok {
			return nil, errors.New("外部 LLM API Key 格式不合法")
		}
		cleaned, err := sanitizeColabOneSecret(keyStr, "API Key")
		if err != nil {
			return nil, err
		}
		apiKey = cleaned
	}
	if requireAPIKey && apiKey == "" {
		return nil, errors.New("请填写外部 LLM API Key")
	}

	out := map[string]any{
		"base_url": baseURL,
		"model":    model,
		"api_key":  apiKey,
	}
	if rawTemp, exists := raw["temperature"]; exists && rawTemp != nil {
		temp, ok := parseFiniteFloat(rawTemp)
		if !ok || temp < 0 || temp > 2 {
			return nil, errors.New("外部 LLM temperature 必须在 0 到 2 之间")
		}
		out["temperature"] = temp
	}
	if rawMaxTok, exists := raw["max_tokens"]; exists && rawMaxTok != nil {
		maxTok, ok := parseExactInt(rawMaxTok)
		if !ok || maxTok < 16 || maxTok > 4096 {
			return nil, errors.New("外部 LLM max_tokens 必须在 16 到 4096 之间")
		}
		out["max_tokens"] = maxTok
	}
	return out, nil
}

func validateColabOneExternalLLMURL(raw string) (string, error) {
	if utf8.RuneCountInString(raw) > colabOneLiveMaxURLRunes {
		return "", errors.New("外部 LLM 地址长度超过限制")
	}
	for _, ch := range raw {
		if ch == '\r' || ch == '\n' || ch == 0 || ch < 0x20 || ch == 0x7f || unicode.IsControl(ch) {
			return "", errors.New("外部 LLM 地址包含非法控制字符")
		}
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("请填写外部 LLM API 地址")
	}
	for _, ch := range trimmed {
		if unicode.IsSpace(ch) {
			return "", errors.New("外部 LLM 地址不能包含空白字符")
		}
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" {
		return "", errors.New("外部 LLM 地址格式无效")
	}
	if u.Host == "" || strings.HasSuffix(u.Host, ":") {
		return "", errors.New("外部 LLM 地址缺少有效主机名或端口")
	}
	scheme := strings.ToLower(u.Scheme)
	rawHost := strings.ToLower(u.Hostname())
	if strings.Contains(rawHost, "%") {
		return "", errors.New("外部 LLM 地址主机名不合法")
	}
	host := strings.TrimSuffix(rawHost, ".")
	if host == "" || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return "", errors.New("外部 LLM 地址缺少有效主机名")
	}
	if port := u.Port(); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return "", errors.New("外部 LLM 地址端口不合法")
		}
	}

	exactLoopback := isExactLoopbackHost(rawHost)
	switch scheme {
	case "http":
		if !exactLoopback {
			return "", errors.New("外部 LLM 地址仅支持 HTTPS，或本机回环地址 http://127.0.0.1 / http://localhost / http://[::1]")
		}
	case "https":
		if exactLoopback || isBlockedInternalHostname(host) {
			return "", errors.New("外部 LLM 接口禁止访问回环、内网或元数据域名")
		}
		if addr, addrErr := netip.ParseAddr(host); addrErr == nil {
			if isNonGlobalIP(addr) {
				return "", errors.New("外部 LLM 接口禁止访问私有网段、链路本地、CGNAT 或非公网 IP 地址")
			}
		} else if isSuspiciousNumericIPv4Host(host) {
			return "", errors.New("外部 LLM 接口禁止使用非规范数字 IP 地址")
		}
	default:
		return "", errors.New("外部 LLM 地址仅支持 HTTPS，或本机回环地址 http://127.0.0.1 / http://localhost / http://[::1]")
	}
	return strings.TrimRight(trimmed, "/"), nil
}

func validateColabOneRTMPURL(raw string) error {
	if utf8.RuneCountInString(raw) > 1024 {
		return errors.New("RTMP 推流地址过长")
	}
	for _, ch := range raw {
		if ch == '\r' || ch == '\n' || ch == 0 || ch < 0x20 || ch == 0x7f || unicode.IsControl(ch) || unicode.IsSpace(ch) {
			return errors.New("RTMP 推流地址包含非法字符")
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.Host == "" || strings.HasSuffix(u.Host, ":") {
		return errors.New("RTMP 推流地址格式无效")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "rtmp" && scheme != "rtmps" {
		return errors.New("RTMP 推流地址仅支持 rtmp:// 或 rtmps://")
	}
	if port := u.Port(); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return errors.New("RTMP 推流地址端口不合法")
		}
	}
	rawHost := strings.ToLower(u.Hostname())
	if strings.Contains(rawHost, "%") {
		return errors.New("RTMP 推流地址主机名不合法")
	}
	host := strings.TrimSuffix(rawHost, ".")
	if host == "" || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return errors.New("RTMP 推流地址缺少有效主机名")
	}
	if isExactLoopbackHost(rawHost) || isBlockedInternalHostname(host) {
		return errors.New("RTMP 推流地址禁止指向本机回环、内网或元数据主机")
	}
	if addr, addrErr := netip.ParseAddr(host); addrErr == nil {
		if isNonGlobalIP(addr) {
			return errors.New("RTMP 推流地址禁止指向私有网段、链路本地、CGNAT 或非公网 IP 地址")
		}
	} else if isSuspiciousNumericIPv4Host(host) {
		return errors.New("RTMP 推流地址禁止使用非规范数字 IP 地址")
	}
	return nil
}

// ValidateColabOneLiveStartConfig validates and normalizes a Live Studio start
// or VRAM-check configuration payload, emitting ONLY canonical
// ColabOneLiveStartRequest keys.
func ValidateColabOneLiveStartConfig(raw map[string]any) (map[string]any, error) {
	if raw == nil {
		raw = map[string]any{}
	}
	if containsNonFiniteFloat(raw) {
		return nil, errors.New("实时演播配置包含非法数值")
	}
	if err := rejectUnknownKeys(raw, colabOneStartConfigAllowedKeys, "实时演播配置"); err != nil {
		return nil, err
	}

	out := map[string]any{}

	visualEngine, hasVisual, err := validateStrictStringField(raw, "visual_engine", "视觉驱动引擎")
	if err != nil {
		return nil, err
	}
	visualEngine = strings.TrimSpace(visualEngine)
	if !hasVisual || visualEngine == "" {
		visualEngine = ColabOneLiveDefaultVisualEngine
	}
	if !ColabOneLiveVisualEngines[visualEngine] {
		return nil, fmt.Errorf("不支持的视觉驱动引擎：%s", visualEngine)
	}
	out["visual_engine"] = visualEngine

	if rawPL, hasPL := raw["personalive_enabled"]; hasPL && rawPL != nil {
		b, ok := rawPL.(bool)
		if !ok {
			return nil, errors.New("personalive_enabled 必须为布尔值")
		}
		out["personalive_enabled"] = b
	}

	ttsEngine, hasTTS, err := validateStrictStringField(raw, "tts_engine", "语音合成引擎")
	if err != nil {
		return nil, err
	}
	ttsEngine = strings.TrimSpace(ttsEngine)
	if !hasTTS || ttsEngine == "" {
		ttsEngine = ColabOneLiveDefaultTTSEngine
	}
	if !ColabOneLiveTTSEngines[ttsEngine] {
		return nil, fmt.Errorf("不支持的语音合成引擎：%s", ttsEngine)
	}
	out["tts_engine"] = ttsEngine

	if vp, hasVP, err := validateStrictStringField(raw, "voice_preset", "音色预设"); err != nil {
		return nil, err
	} else if hasVP {
		cleanVP, err := validateColabOneLiveVoicePreset(vp, "音色预设")
		if err != nil {
			return nil, err
		}
		out["voice_preset"] = cleanVP
	}

	llmMode, hasLLMMode, err := validateStrictStringField(raw, "llm_mode", "对话模型模式")
	if err != nil {
		return nil, err
	}
	llmMode = strings.TrimSpace(llmMode)
	if !hasLLMMode || llmMode == "" {
		llmMode = ColabOneLiveDefaultLLMMode
	}
	if !ColabOneLiveLLMModes[llmMode] {
		return nil, fmt.Errorf("不支持的对话模型模式：%s", llmMode)
	}
	out["llm_mode"] = llmMode

	if localModelID, hasLocalModel, err := validateStrictStringField(raw, "local_llm_model_id", "本地对话模型 ID"); err != nil {
		return nil, err
	} else if hasLocalModel {
		cleaned, err := validateSafeSingleLineString(localModelID, "本地对话模型 ID", colabOneLiveMaxLocalModelIDRunes, true)
		if err != nil {
			return nil, err
		}
		out["local_llm_model_id"] = cleaned
	}

	directorMode, hasDirector, err := validateStrictStringField(raw, "director_mode", "演播模式")
	if err != nil {
		return nil, err
	}
	directorMode = strings.TrimSpace(directorMode)
	if !hasDirector || directorMode == "" {
		directorMode = ColabOneLiveDefaultDirectorMode
	}
	if !ColabOneLiveDirectorModes[directorMode] {
		return nil, fmt.Errorf("不支持的演播模式：%s", directorMode)
	}
	out["director_mode"] = directorMode

	micMode, hasMic, err := validateStrictStringField(raw, "mic_mode", "麦克风模式")
	if err != nil {
		return nil, err
	}
	micMode = strings.TrimSpace(micMode)
	if !hasMic || micMode == "" {
		micMode = ColabOneLiveDefaultMicMode
	}
	if !ColabOneLiveMicModes[micMode] {
		return nil, fmt.Errorf("不支持的麦克风模式：%s", micMode)
	}
	out["mic_mode"] = micMode

	resolution, hasRes, err := validateStrictStringField(raw, "resolution", "画布分辨率")
	if err != nil {
		return nil, err
	}
	resolution = strings.TrimSpace(resolution)

	rawW, hasW := raw["width"]
	rawH, hasH := raw["height"]
	if (hasW && rawW != nil) != (hasH && rawH != nil) {
		return nil, errors.New("画布宽度与高度必须成对提供")
	}
	if hasW && rawW != nil && hasH && rawH != nil {
		w, okW := parseExactInt(rawW)
		h, okH := parseExactInt(rawH)
		if !okW || !okH || w <= 0 || h <= 0 {
			return nil, errors.New("画布宽高必须为正整数")
		}
		dimKey, err := validateColabOneLiveDimensions(w, h)
		if err != nil {
			return nil, err
		}
		if resolution != "" {
			normRes, err := validateColabOneLiveResolution(resolution)
			if err != nil {
				return nil, err
			}
			if normRes != dimKey {
				return nil, errors.New("画布分辨率与宽高参数不一致")
			}
		}
		resolution = dimKey
	}
	if !hasRes && resolution == "" {
		resolution = ColabOneLiveDefaultResolution
	}
	if resolution == "" {
		resolution = ColabOneLiveDefaultResolution
	}
	normResolution, err := validateColabOneLiveResolution(resolution)
	if err != nil {
		return nil, err
	}
	out["resolution"] = normResolution

	if avatarRaw, hasAvatar, err := validateStrictStringField(raw, "avatar_id", "形象 ID"); err != nil {
		return nil, err
	} else if hasAvatar && strings.TrimSpace(avatarRaw) != "" {
		cleanID, err := ValidateColabOneLiveAvatarID(avatarRaw)
		if err != nil {
			return nil, err
		}
		out["avatar_id"] = cleanID
	}

	if rawTargetFPS, hasTargetFPS := raw["target_fps"]; hasTargetFPS && rawTargetFPS != nil {
		tfps, ok := parseExactInt(rawTargetFPS)
		if !ok || tfps < 10 || tfps > 60 {
			return nil, errors.New("目标帧率参数超出允许范围 (10-60)")
		}
		out["target_fps"] = tfps
	}

	enableRTMP := false
	if rawEnableRTMP, hasRTMP := raw["enable_rtmp"]; hasRTMP && rawEnableRTMP != nil {
		b, ok := rawEnableRTMP.(bool)
		if !ok {
			return nil, errors.New("enable_rtmp 必须为布尔值")
		}
		enableRTMP = b
		out["enable_rtmp"] = b
	}
	if rawRTMP, hasRTMPURL, err := validateStrictStringField(raw, "rtmp_url", "RTMP 推流地址"); err != nil {
		return nil, err
	} else if hasRTMPURL {
		trimmedRTMP := strings.TrimSpace(rawRTMP)
		if trimmedRTMP != "" {
			if err := validateColabOneRTMPURL(trimmedRTMP); err != nil {
				return nil, err
			}
		}
		out["rtmp_url"] = trimmedRTMP
	}
	if enableRTMP {
		rtmpStr, _ := out["rtmp_url"].(string)
		if strings.TrimSpace(rtmpStr) == "" {
			return nil, errors.New("启用 RTMP 推流时必须提供有效的 rtmp:// 或 rtmps:// 地址")
		}
	}

	if rawExt, hasExt := raw["external_llm"]; hasExt && rawExt != nil {
		extMap, ok := rawExt.(map[string]any)
		if !ok {
			return nil, errors.New("external_llm 配置格式不合法")
		}
		if len(extMap) > 0 {
			validatedExt, err := ValidateColabOneLiveExternalLLMConfig(extMap, false)
			if err != nil {
				return nil, err
			}
			out["external_llm"] = validatedExt
		}
	}

	if rawTok, hasTok := raw["hf_token"]; hasTok {
		if rawTok == nil {
			return nil, errors.New("字段 hf_token 不能为空值")
		}
		tokStr, ok := rawTok.(string)
		if !ok {
			return nil, errors.New("hf_token 格式不合法")
		}
		cleaned, err := sanitizeColabOneSecret(tokStr, "hf_token")
		if err != nil {
			return nil, err
		}
		if cleaned != "" {
			out["hf_token"] = cleaned
		}
	}

	if pInputID, hasPInput, err := validateStrictStringField(raw, "portrait_input_id", "立绘素材 ID"); err != nil {
		return nil, err
	} else if hasPInput && strings.TrimSpace(pInputID) != "" {
		cleanInputID, err := validateColabOneLiveAssetID(pInputID, "立绘素材 ID")
		if err != nil {
			return nil, err
		}
		out["portrait_input_id"] = cleanInputID
	}

	if rawCanvas, hasCanvas := raw["canvas_config"]; hasCanvas && rawCanvas != nil {
		canvasMap, ok := rawCanvas.(map[string]any)
		if !ok {
			return nil, errors.New("canvas_config 格式不合法")
		}
		copiedCanvas := make(map[string]any, len(canvasMap))
		for k, v := range canvasMap {
			copiedCanvas[k] = v
		}
		out["canvas_config"] = copiedCanvas
	}

	return out, nil
}

// ValidateColabOneLiveAvatarPayload validates a Live Studio avatar create/update
// payload, emitting ONLY canonical ColabOneLiveAvatarSaveRequest keys.
func ValidateColabOneLiveAvatarPayload(raw map[string]any) (map[string]any, error) {
	if raw == nil {
		return nil, errors.New("形象配置不能为空")
	}
	if containsNonFiniteFloat(raw) {
		return nil, errors.New("形象配置包含非法数值")
	}
	if err := rejectUnknownKeys(raw, colabOneAvatarSaveAllowedKeys, "形象配置"); err != nil {
		return nil, err
	}

	out := map[string]any{}

	idVal, hasID, err := validateStrictStringField(raw, "id", "形象 ID")
	if err != nil {
		return nil, err
	}
	aliasID, hasAliasID, err := validateStrictStringField(raw, "avatar_id", "形象 ID")
	if err != nil {
		return nil, err
	}
	cleanID := ""
	if hasID && strings.TrimSpace(idVal) != "" {
		cleanID, err = ValidateColabOneLiveAvatarID(idVal)
		if err != nil {
			return nil, err
		}
	}
	if hasAliasID && strings.TrimSpace(aliasID) != "" {
		cleanAlias, err := ValidateColabOneLiveAvatarID(aliasID)
		if err != nil {
			return nil, err
		}
		if cleanID != "" && cleanID != cleanAlias {
			return nil, errors.New("id 与 avatar_id 不一致")
		}
		cleanID = cleanAlias
	}
	if cleanID != "" {
		out["id"] = cleanID
	}

	nameRaw, _, err := validateStrictStringField(raw, "name", "形象名称")
	if err != nil {
		return nil, err
	}
	name, err := validateSafeSingleLineString(nameRaw, "形象名称", colabOneLiveMaxNameRunes, false)
	if err != nil {
		return nil, err
	}
	out["name"] = name

	if personaRaw, hasPersona, err := validateStrictStringField(raw, "persona_prompt", "人设提示词"); err != nil {
		return nil, err
	} else if hasPersona {
		cleaned, err := validateSafeTextString(personaRaw, "人设提示词", colabOneLiveMaxPromptRunes)
		if err != nil {
			return nil, err
		}
		out["persona_prompt"] = cleaned
	}

	if pInputID, hasPInput, err := validateStrictStringField(raw, "portrait_input_id", "立绘素材 ID"); err != nil {
		return nil, err
	} else if hasPInput && strings.TrimSpace(pInputID) != "" {
		cleanInputID, err := validateColabOneLiveAssetID(pInputID, "立绘素材 ID")
		if err != nil {
			return nil, err
		}
		out["portrait_input_id"] = cleanInputID
	}

	portraitB64, hasPB64, err := validateStrictStringField(raw, "portrait_b64", "立绘文件数据")
	if err != nil {
		return nil, err
	}
	portraitData, hasPData, err := validateStrictStringField(raw, "portrait_data", "立绘文件数据")
	if err != nil {
		return nil, err
	}
	rawPortrait := ""
	if hasPB64 && strings.TrimSpace(portraitB64) != "" {
		rawPortrait = portraitB64
	}
	if hasPData && strings.TrimSpace(portraitData) != "" {
		if rawPortrait != "" && strings.TrimSpace(rawPortrait) != strings.TrimSpace(portraitData) {
			return nil, errors.New("portrait_b64 与 portrait_data 不一致")
		}
		rawPortrait = portraitData
	}
	if rawPortrait != "" {
		cleanB64, err := validateBoundedBase64(rawPortrait, ColabOneLiveMaxPortraitBytes, "立绘图片")
		if err != nil {
			return nil, err
		}
		out["portrait_b64"] = cleanB64
	}

	if portraitName, hasPName, err := validateStrictStringField(raw, "portrait_filename", "立绘文件名"); err != nil {
		return nil, err
	} else if hasPName && strings.TrimSpace(portraitName) != "" {
		cleanName, _, err := validateSafeFilename(portraitName, ColabOneLivePortraitExtensions, "立绘文件名")
		if err != nil {
			return nil, err
		}
		out["portrait_filename"] = cleanName
	}

	if te, hasTE, err := validateStrictStringField(raw, "default_tts_engine", "语音合成引擎"); err != nil {
		return nil, err
	} else if hasTE && strings.TrimSpace(te) != "" {
		te = strings.TrimSpace(te)
		if !ColabOneLiveTTSEngines[te] {
			return nil, fmt.Errorf("不支持的语音合成引擎：%s", te)
		}
		out["default_tts_engine"] = te
	}

	if vp, hasVP, err := validateStrictStringField(raw, "default_voice_preset", "默认音色预设"); err != nil {
		return nil, err
	} else if hasVP {
		cleanVP, err := validateColabOneLiveVoicePreset(vp, "默认音色预设")
		if err != nil {
			return nil, err
		}
		out["default_voice_preset"] = cleanVP
	}

	if rawActions, exists := raw["actions"]; exists && rawActions != nil {
		cleanActions, err := validateActionSpecList(rawActions)
		if err != nil {
			return nil, err
		}
		out["actions"] = cleanActions
	}

	if rawProducts, exists := raw["products"]; exists && rawProducts != nil {
		cleanProducts, err := validateProductItemList(rawProducts)
		if err != nil {
			return nil, err
		}
		out["products"] = cleanProducts
	}

	return out, nil
}

func extractMapItems(raw any, fieldName string, maxItems int) ([]map[string]any, error) {
	switch x := raw.(type) {
	case []map[string]any:
		if len(x) > maxItems {
			return nil, fmt.Errorf("字段 %s 数量超过上限 (%d)", fieldName, maxItems)
		}
		for _, item := range x {
			if item == nil {
				return nil, fmt.Errorf("字段 %s 包含空元素", fieldName)
			}
		}
		return x, nil
	case []any:
		if len(x) > maxItems {
			return nil, fmt.Errorf("字段 %s 数量超过上限 (%d)", fieldName, maxItems)
		}
		out := make([]map[string]any, 0, len(x))
		for _, elem := range x {
			m, ok := elem.(map[string]any)
			if !ok || m == nil {
				return nil, fmt.Errorf("字段 %s 元素必须为对象", fieldName)
			}
			out = append(out, m)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("字段 %s 必须为对象数组", fieldName)
	}
}

func validateActionSpecList(raw any) ([]any, error) {
	items, err := extractMapItems(raw, "actions", colabOneLiveMaxAvatarActions)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if err := rejectUnknownKeys(item, colabOneActionSpecAllowedKeys, "动作配置"); err != nil {
			return nil, err
		}
		rawID, _, err := validateStrictStringField(item, "id", "动作 ID")
		if err != nil {
			return nil, err
		}
		cleanID, err := ValidateColabOneLiveAvatarID(rawID)
		if err != nil {
			return nil, errors.New("动作 ID 不合法")
		}
		if seen[cleanID] {
			return nil, fmt.Errorf("动作列表包含重复 ID：%s", cleanID)
		}
		seen[cleanID] = true

		rawLabel, _, err := validateStrictStringField(item, "label", "动作名称")
		if err != nil {
			return nil, err
		}
		cleanLabel, err := validateSafeSingleLineString(rawLabel, "动作名称", colabOneLiveMaxLabelRunes, false)
		if err != nil {
			return nil, err
		}

		cleanItem := map[string]any{
			"id":    cleanID,
			"label": cleanLabel,
		}
		if rawKind, hasKind, err := validateStrictStringField(item, "kind", "动作类型"); err != nil {
			return nil, err
		} else if hasKind && strings.TrimSpace(rawKind) != "" {
			k := strings.TrimSpace(rawKind)
			if !ColabOneLiveActionKinds[k] {
				return nil, fmt.Errorf("不支持的动作类型：%s", k)
			}
			cleanItem["kind"] = k
		}
		if rawClip, hasClip, err := validateStrictStringField(item, "clip_path", "动作片段路径"); err != nil {
			return nil, err
		} else if hasClip {
			cleanClip, err := validateColabOneLiveRelativePath(rawClip, "动作片段路径")
			if err != nil {
				return nil, err
			}
			cleanItem["clip_path"] = cleanClip
		}
		if rawBGM, hasBGM, err := validateStrictStringField(item, "bgm_path", "背景音乐路径"); err != nil {
			return nil, err
		} else if hasBGM {
			cleanBGM, err := validateColabOneLiveRelativePath(rawBGM, "背景音乐路径")
			if err != nil {
				return nil, err
			}
			cleanItem["bgm_path"] = cleanBGM
		}
		if rawDur, hasDur := item["duration_seconds"]; hasDur && rawDur != nil {
			dur, ok := parseFiniteFloat(rawDur)
			if !ok || dur < 0 || dur > 600 {
				return nil, errors.New("动作时长超出允许范围 (0-600 秒)")
			}
			cleanItem["duration_seconds"] = dur
		}
		if rawKWs, hasKWs := item["trigger_keywords"]; hasKWs && rawKWs != nil {
			kws, err := validateTriggerKeywords(rawKWs)
			if err != nil {
				return nil, err
			}
			cleanItem["trigger_keywords"] = kws
		}
		out = append(out, cleanItem)
	}
	return out, nil
}

func validateProductItemList(raw any) ([]any, error) {
	items, err := extractMapItems(raw, "products", colabOneLiveMaxAvatarProducts)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if err := rejectUnknownKeys(item, colabOneProductItemAllowedKeys, "商品配置"); err != nil {
			return nil, err
		}
		rawID, _, err := validateStrictStringField(item, "id", "商品 ID")
		if err != nil {
			return nil, err
		}
		cleanID, err := ValidateColabOneLiveAvatarID(rawID)
		if err != nil {
			return nil, errors.New("商品 ID 不合法")
		}
		if seen[cleanID] {
			return nil, fmt.Errorf("商品列表包含重复 ID：%s", cleanID)
		}
		seen[cleanID] = true

		rawName, _, err := validateStrictStringField(item, "name", "商品名称")
		if err != nil {
			return nil, err
		}
		cleanName, err := validateSafeSingleLineString(rawName, "商品名称", colabOneLiveMaxNameRunes, false)
		if err != nil {
			return nil, err
		}

		cleanItem := map[string]any{
			"id":   cleanID,
			"name": cleanName,
		}
		if rawPrice, hasPrice, err := validateStrictStringField(item, "price", "商品价格"); err != nil {
			return nil, err
		} else if hasPrice {
			cleanPrice, err := validateSafeSingleLineString(rawPrice, "商品价格", colabOneLiveMaxPriceRunes, true)
			if err != nil {
				return nil, err
			}
			cleanItem["price"] = cleanPrice
		}
		if rawTag, hasTag, err := validateStrictStringField(item, "tag", "商品标签"); err != nil {
			return nil, err
		} else if hasTag {
			cleanTag, err := validateSafeSingleLineString(rawTag, "商品标签", colabOneLiveMaxTagRunes, true)
			if err != nil {
				return nil, err
			}
			if cleanTag != "" {
				cleanItem["tag"] = cleanTag
			}
		}
		if rawHighlights, hasHL, err := validateStrictStringField(item, "highlights", "商品卖点"); err != nil {
			return nil, err
		} else if hasHL {
			cleanHL, err := validateSafeTextString(rawHighlights, "商品卖点", colabOneLiveMaxHighlightsRunes)
			if err != nil {
				return nil, err
			}
			cleanItem["highlights"] = cleanHL
		}
		if rawHint, hasHint, err := validateStrictStringField(item, "script_hint", "话术提示"); err != nil {
			return nil, err
		} else if hasHint {
			cleanHint, err := validateSafeTextString(rawHint, "话术提示", colabOneLiveMaxScriptHintRunes)
			if err != nil {
				return nil, err
			}
			cleanItem["script_hint"] = cleanHint
		}
		if rawImgID, hasImgID, err := validateStrictStringField(item, "image_input_id", "商品图片 ID"); err != nil {
			return nil, err
		} else if hasImgID {
			cleanImgID, err := validateColabOneLiveAssetID(rawImgID, "商品图片 ID")
			if err != nil {
				return nil, err
			}
			cleanItem["image_input_id"] = cleanImgID
		}
		out = append(out, cleanItem)
	}
	return out, nil
}

func validateTriggerKeywords(raw any) ([]string, error) {
	var rawStrs []string
	switch x := raw.(type) {
	case []string:
		rawStrs = x
	case []any:
		rawStrs = make([]string, 0, len(x))
		for _, item := range x {
			s, ok := item.(string)
			if !ok {
				return nil, errors.New("trigger_keywords 必须为字符串数组")
			}
			rawStrs = append(rawStrs, s)
		}
	default:
		return nil, errors.New("trigger_keywords 必须为字符串数组")
	}
	if len(rawStrs) > colabOneLiveMaxKeywords {
		return nil, fmt.Errorf("trigger_keywords 数量超过上限 (%d)", colabOneLiveMaxKeywords)
	}
	out := make([]string, 0, len(rawStrs))
	seen := make(map[string]bool, len(rawStrs))
	for _, s := range rawStrs {
		cleaned, err := validateSafeSingleLineString(s, "触发关键词", colabOneLiveMaxKeywordRunes, true)
		if err != nil {
			return nil, err
		}
		if cleaned != "" && !seen[cleaned] {
			seen[cleaned] = true
			out = append(out, cleaned)
		}
	}
	return out, nil
}

func validateOptionalBGMAndTiming(raw, out map[string]any) error {
	bgmName, hasBGMName, err := validateStrictStringField(raw, "bgm_filename", "背景音乐文件名")
	if err != nil {
		return err
	}
	if hasBGMName && strings.TrimSpace(bgmName) != "" {
		cleanBGMName, _, err := validateSafeFilename(bgmName, ColabOneLiveAudioExtensions, "背景音乐文件名")
		if err != nil {
			return err
		}
		out["bgm_filename"] = cleanBGMName
	}

	bgmData, hasBGMData, err := validateStrictStringField(raw, "bgm_data", "背景音乐数据")
	if err != nil {
		return err
	}
	if hasBGMData && strings.TrimSpace(bgmData) != "" {
		cleanBGMB64, err := validateBoundedBase64(bgmData, ColabOneLiveMaxBGMBytes, "背景音乐文件")
		if err != nil {
			return err
		}
		out["bgm_data"] = cleanBGMB64
	}

	if rawDur, hasDur := raw["duration_seconds"]; hasDur && rawDur != nil {
		dur, ok := parseFiniteFloat(rawDur)
		if !ok || dur < 0 || dur > 600 {
			return errors.New("素材时长超出允许范围 (0-600 秒)")
		}
		out["duration_seconds"] = dur
	}

	if rawKWs, hasKWs := raw["trigger_keywords"]; hasKWs && rawKWs != nil {
		kws, err := validateTriggerKeywords(rawKWs)
		if err != nil {
			return err
		}
		out["trigger_keywords"] = kws
	}
	return nil
}

// ValidateColabOneLiveAvatarClipUpload validates an avatar action video clip
// upload payload with strict parity against ColabOneLiveClipUploadRequest.
func ValidateColabOneLiveAvatarClipUpload(raw map[string]any) (map[string]any, error) {
	if raw == nil {
		return nil, errors.New("素材上传请求不能为空")
	}
	if containsNonFiniteFloat(raw) {
		return nil, errors.New("素材上传请求包含非法数值")
	}
	if err := rejectUnknownKeys(raw, colabOneAvatarClipAllowedKeys, "动作素材上传请求"); err != nil {
		return nil, err
	}

	actIDRaw, _, err := validateStrictStringField(raw, "action_id", "动作 ID")
	if err != nil {
		return nil, err
	}
	cleanActID, err := ValidateColabOneLiveAvatarID(actIDRaw)
	if err != nil {
		return nil, errors.New("动作 ID 不合法")
	}

	filenameRaw, _, err := validateStrictStringField(raw, "filename", "素材文件名")
	if err != nil {
		return nil, err
	}
	cleanName, _, err := validateSafeFilename(filenameRaw, ColabOneLiveVideoExtensions, "素材文件名")
	if err != nil {
		return nil, err
	}

	dataRaw, _, err := validateStrictStringField(raw, "data", "素材数据")
	if err != nil {
		return nil, err
	}
	cleanB64, err := validateBoundedBase64(dataRaw, ColabOneLiveMaxVideoClipBytes, "素材文件")
	if err != nil {
		return nil, err
	}

	out := map[string]any{
		"action_id": cleanActID,
		"filename":  cleanName,
		"data":      cleanB64,
	}

	if labelRaw, hasLabel, err := validateStrictStringField(raw, "label", "动作名称"); err != nil {
		return nil, err
	} else if hasLabel {
		cleaned, err := validateSafeSingleLineString(labelRaw, "动作名称", colabOneLiveMaxLabelRunes, true)
		if err != nil {
			return nil, err
		}
		out["label"] = cleaned
	}

	if kindRaw, hasKind, err := validateStrictStringField(raw, "kind", "动作类型"); err != nil {
		return nil, err
	} else if hasKind && strings.TrimSpace(kindRaw) != "" {
		k := strings.TrimSpace(kindRaw)
		if !ColabOneLiveActionKinds[k] {
			return nil, fmt.Errorf("不支持的动作类型：%s", k)
		}
		out["kind"] = k
	}

	if err := validateOptionalBGMAndTiming(raw, out); err != nil {
		return nil, err
	}
	return out, nil
}

// ValidateColabOneLiveDrivingUpload validates a PersonaLive driving motion video
// upload payload with strict parity against ColabOneLiveDrivingUploadRequest.
func ValidateColabOneLiveDrivingUpload(raw map[string]any) (map[string]any, error) {
	if raw == nil {
		return nil, errors.New("驱动视频上传请求不能为空")
	}
	if containsNonFiniteFloat(raw) {
		return nil, errors.New("驱动视频上传请求包含非法数值")
	}
	if err := rejectUnknownKeys(raw, colabOneDrivingUploadAllowedKeys, "驱动视频上传请求"); err != nil {
		return nil, err
	}

	labelRaw, _, err := validateStrictStringField(raw, "label", "驱动动作名称")
	if err != nil {
		return nil, err
	}
	cleanLabel, err := validateSafeSingleLineString(labelRaw, "驱动动作名称", colabOneLiveMaxLabelRunes, false)
	if err != nil {
		return nil, err
	}

	filenameRaw, _, err := validateStrictStringField(raw, "filename", "素材文件名")
	if err != nil {
		return nil, err
	}
	cleanName, _, err := validateSafeFilename(filenameRaw, ColabOneLiveVideoExtensions, "素材文件名")
	if err != nil {
		return nil, err
	}

	dataRaw, _, err := validateStrictStringField(raw, "data", "素材数据")
	if err != nil {
		return nil, err
	}
	cleanB64, err := validateBoundedBase64(dataRaw, ColabOneLiveMaxVideoClipBytes, "素材文件")
	if err != nil {
		return nil, err
	}

	out := map[string]any{
		"label":    cleanLabel,
		"filename": cleanName,
		"data":     cleanB64,
	}

	if idRaw, hasID, err := validateStrictStringField(raw, "id", "驱动动作 ID"); err != nil {
		return nil, err
	} else if hasID && strings.TrimSpace(idRaw) != "" {
		cleanID, err := ValidateColabOneLiveAvatarID(idRaw)
		if err != nil {
			return nil, errors.New("驱动动作 ID 不合法")
		}
		out["id"] = cleanID
	}

	if err := validateOptionalBGMAndTiming(raw, out); err != nil {
		return nil, err
	}
	return out, nil
}

// ValidateColabOneLiveClipUpload is a deprecated alias for ValidateColabOneLiveAvatarClipUpload.
func ValidateColabOneLiveClipUpload(raw map[string]any) (map[string]any, error) {
	return ValidateColabOneLiveAvatarClipUpload(raw)
}

// ValidateColabOneLiveBuildPackPayload validates a request to compile/build an
// avatar animation bank / pack on the remote GPU, emitting ONLY canonical
// ColabOneLiveBuildPackRequest keys.
func ValidateColabOneLiveBuildPackPayload(raw map[string]any) (map[string]any, error) {
	if raw == nil {
		return nil, errors.New("形象动作库构建参数不能为空")
	}
	if containsNonFiniteFloat(raw) {
		return nil, errors.New("形象动作库构建参数包含非法数值")
	}
	if err := rejectUnknownKeys(raw, colabOneBuildPackAllowedKeys, "形象动作库构建参数"); err != nil {
		return nil, err
	}

	avatarRaw, _, err := validateStrictStringField(raw, "avatar_id", "形象 ID")
	if err != nil {
		return nil, err
	}
	avatarID, err := ValidateColabOneLiveAvatarID(avatarRaw)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"avatar_id": avatarID,
	}

	if nameRaw, hasName, err := validateStrictStringField(raw, "name", "形象名称"); err != nil {
		return nil, err
	} else if hasName {
		cleanName, err := validateSafeSingleLineString(nameRaw, "形象名称", colabOneLiveMaxNameRunes, true)
		if err != nil {
			return nil, err
		}
		out["name"] = cleanName
	}

	if pInputID, hasPInput, err := validateStrictStringField(raw, "portrait_input_id", "立绘素材 ID"); err != nil {
		return nil, err
	} else if hasPInput && strings.TrimSpace(pInputID) != "" {
		cleanInputID, err := validateColabOneLiveAssetID(pInputID, "立绘素材 ID")
		if err != nil {
			return nil, err
		}
		out["portrait_input_id"] = cleanInputID
	}

	pB64, hasPB64, err := validateStrictStringField(raw, "portrait_b64", "立绘文件数据")
	if err != nil {
		return nil, err
	}
	pData, hasPData, err := validateStrictStringField(raw, "portrait_data", "立绘文件数据")
	if err != nil {
		return nil, err
	}
	rawPortrait := ""
	if hasPB64 && strings.TrimSpace(pB64) != "" {
		rawPortrait = pB64
	}
	if hasPData && strings.TrimSpace(pData) != "" {
		if rawPortrait != "" && strings.TrimSpace(rawPortrait) != strings.TrimSpace(pData) {
			return nil, errors.New("portrait_b64 与 portrait_data 不一致")
		}
		rawPortrait = pData
	}
	if rawPortrait != "" {
		cleanPB64, err := validateBoundedBase64(rawPortrait, ColabOneLiveMaxPortraitBytes, "立绘图片")
		if err != nil {
			return nil, err
		}
		out["portrait_b64"] = cleanPB64
	}

	if pName, hasPName, err := validateStrictStringField(raw, "portrait_filename", "立绘文件名"); err != nil {
		return nil, err
	} else if hasPName && strings.TrimSpace(pName) != "" {
		cleanPName, _, err := validateSafeFilename(pName, ColabOneLivePortraitExtensions, "立绘文件名")
		if err != nil {
			return nil, err
		}
		out["portrait_filename"] = cleanPName
	}

	if rawActions, exists := raw["actions"]; exists && rawActions != nil {
		items, err := validateStringIDSlice(rawActions, "actions", colabOneLiveMaxBuildPackActions)
		if err != nil {
			return nil, err
		}
		out["actions"] = items
	}

	if rawDances, exists := raw["dance_ids"]; exists && rawDances != nil {
		items, err := validateStringIDSlice(rawDances, "dance_ids", colabOneLiveMaxBuildPackDances)
		if err != nil {
			return nil, err
		}
		out["dance_ids"] = items
	}

	if res, ok, err := validateStrictStringField(raw, "resolution", "画布分辨率"); err != nil {
		return nil, err
	} else if ok && strings.TrimSpace(res) != "" {
		normRes, err := validateColabOneLiveResolution(res)
		if err != nil {
			return nil, err
		}
		out["resolution"] = normRes
	}

	if rawFPS, hasFPS := raw["fps"]; hasFPS && rawFPS != nil {
		fps, ok := parseExactInt(rawFPS)
		if !ok || fps < 12 || fps > 60 {
			return nil, errors.New("帧率参数必须为 12 到 60 之间的整数")
		}
		out["fps"] = fps
	}

	if rawTok, hasTok := raw["hf_token"]; hasTok {
		if rawTok == nil {
			return nil, errors.New("字段 hf_token 不能为空值")
		}
		tokStr, ok := rawTok.(string)
		if !ok {
			return nil, errors.New("hf_token 格式不合法")
		}
		cleaned, err := sanitizeColabOneSecret(tokStr, "hf_token")
		if err != nil {
			return nil, err
		}
		if cleaned != "" {
			out["hf_token"] = cleaned
		}
	}

	if rawOverwrite, exists := raw["overwrite"]; exists && rawOverwrite != nil {
		b, ok := rawOverwrite.(bool)
		if !ok {
			return nil, errors.New("字段 overwrite 必须为布尔值")
		}
		out["overwrite"] = b
	}

	return out, nil
}

func validateStringIDSlice(raw any, fieldName string, maxItems int) ([]string, error) {
	var rawStrs []string
	switch x := raw.(type) {
	case []string:
		rawStrs = x
	case []any:
		rawStrs = make([]string, 0, len(x))
		for _, item := range x {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("字段 %s 包含非字符串项", fieldName)
			}
			rawStrs = append(rawStrs, s)
		}
	default:
		return nil, fmt.Errorf("字段 %s 必须为字符串数组", fieldName)
	}
	if len(rawStrs) > maxItems {
		return nil, fmt.Errorf("字段 %s 数量超过上限 (%d)", fieldName, maxItems)
	}
	out := make([]string, 0, len(rawStrs))
	for _, s := range rawStrs {
		clean, err := ValidateColabOneLiveAvatarID(s)
		if err != nil {
			return nil, fmt.Errorf("字段 %s 包含非法标识", fieldName)
		}
		out = append(out, clean)
	}
	return out, nil
}

func validateSafeFilename(filename string, allowedExts map[string]bool, label string) (string, string, error) {
	for _, ch := range filename {
		if ch == '\r' || ch == '\n' || ch == 0 || ch < 0x20 || ch == 0x7f || unicode.IsControl(ch) {
			return "", "", fmt.Errorf("%s不合法", label)
		}
	}
	name := strings.TrimSpace(filename)
	if name == "" ||
		utf8.RuneCountInString(name) > colabOneLiveMaxFilenameRunes ||
		name == "." ||
		name == ".." ||
		strings.ContainsAny(name, `/\`+"\x00") {
		return "", "", fmt.Errorf("%s不合法", label)
	}
	ext := strings.ToLower(path.Ext(name))
	if ext == "" || len(name) <= len(ext) || !allowedExts[ext] {
		displayExt := strings.TrimPrefix(ext, ".")
		if displayExt == "" {
			displayExt = "未知"
		}
		return "", "", fmt.Errorf("不支持的文件格式：%s", displayExt)
	}
	return name, ext, nil
}

func validateBoundedBase64(dataB64 string, maxBytes int, label string) (string, error) {
	payload := strings.TrimSpace(dataB64)
	if payload == "" {
		return "", fmt.Errorf("%s内容为空", label)
	}
	if strings.HasPrefix(payload, "data:") {
		_, after, ok := strings.Cut(payload, ",")
		if !ok {
			return "", fmt.Errorf("%s编码格式无效", label)
		}
		payload = after
	}

	limitMB := maxBytes / (1024 * 1024)
	maxEncodedLen := maxBytes + maxBytes/2 + 4096
	if len(payload) > maxEncodedLen {
		return "", fmt.Errorf("%s超过大小限制（最大 %d MB）", label, limitMB)
	}

	clean := payload
	if strings.IndexFunc(payload, unicode.IsSpace) >= 0 {
		clean = strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, payload)
	}
	if clean == "" {
		return "", fmt.Errorf("%s内容为空", label)
	}
	if len(clean)%4 != 0 {
		return "", fmt.Errorf("%s Base64 编码无效", label)
	}

	const b64ChunkSize = 4096 // multiple of 4
	var decodeBuf [(b64ChunkSize / 4) * 3]byte
	totalDecoded := 0
	for offset := 0; offset < len(clean); offset += b64ChunkSize {
		end := offset + b64ChunkSize
		if end > len(clean) {
			end = len(clean)
		}
		chunk := clean[offset:end]
		if end < len(clean) && strings.ContainsRune(chunk, '=') {
			return "", fmt.Errorf("%s Base64 编码无效", label)
		}
		n, err := base64.StdEncoding.Decode(decodeBuf[:], []byte(chunk))
		if err != nil {
			return "", fmt.Errorf("%s Base64 编码无效", label)
		}
		totalDecoded += n
		if totalDecoded > maxBytes {
			return "", fmt.Errorf("%s超过大小限制（最大 %d MB）", label, limitMB)
		}
	}
	if totalDecoded == 0 {
		return "", fmt.Errorf("%s内容为空", label)
	}
	return clean, nil
}
