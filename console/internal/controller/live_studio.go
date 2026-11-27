package controller

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/luolongfei/ColabOne/console/internal/live"
)

const (
	colabOneLiveStudioUpgradeHint         = "当前远端服务版本暂不支持实时演播功能，请先点击顶部「立即热更新」升级远端服务后再试"
	colabOneLiveCompanionPath             = "/_colabone_live/remote"
	colabOneLiveCompanionTicketInvalidMsg = "远端返回的手机接入票据格式异常，请稍后重试或升级远端服务"
	colabOneVaultLiveExternalLLMKey       = "live_external_llm"
	colabOneLiveStudioCapability          = "comfyui_live_studio"
	colabOneLivePostMutationRefreshTimeout = 15 * time.Second
)

var reLiveCompanionTicketCode = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

func (c *ColabOneController) refreshRemoteBounded(ctx context.Context, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = c.livePostMutationRefreshTimeout
	}
	if timeout <= 0 {
		timeout = colabOneLivePostMutationRefreshTimeout
	}
	if ctx == nil {
		ctx = c.rootCtx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	refreshCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return c.RefreshRemote(refreshCtx, "")
}

func sameExternalLLMOrigin(a, b string) bool {
	oa, errA := live.ColabOneNormalizedURLOrigin(a)
	if errA != nil || oa == "" {
		return false
	}
	ob, errB := live.ColabOneNormalizedURLOrigin(b)
	if errB != nil || ob == "" {
		return false
	}
	return oa == ob
}

func (c *ColabOneController) vaultLiveExternalLLMLocked() map[string]string {
	out := map[string]string{
		"base_url": "",
		"model":    "",
		"api_key":  "",
	}
	if c.vault == nil {
		return out
	}
	raw, ok := c.vault.Get(colabOneVaultLiveExternalLLMKey)
	if !ok {
		return out
	}
	m := asMap(raw)
	if m == nil {
		return out
	}
	out["base_url"] = strings.TrimSpace(asString(m["base_url"]))
	out["model"] = strings.TrimSpace(asString(m["model"]))
	if k, ok := m["api_key"].(string); ok {
		out["api_key"] = k
	}
	return out
}

func (c *ColabOneController) vaultLiveExternalLLM() map[string]string {
	c.vaultConfigMu.Lock()
	defer c.vaultConfigMu.Unlock()
	return c.vaultLiveExternalLLMLocked()
}

func (c *ColabOneController) liveExternalLLMSummaryLocked() map[string]any {
	saved := c.vaultLiveExternalLLMLocked()
	baseURL := saved["base_url"]
	model := saved["model"]
	apiKey := saved["api_key"]
	hasKey := apiKey != ""
	configured := baseURL != "" && model != "" && (hasKey || live.IsColabOneLoopbackURL(baseURL))
	return map[string]any{
		"configured":     configured,
		"base_url":       baseURL,
		"model":          model,
		"has_api_key":    hasKey,
		"masked_api_key": live.MaskColabOneSecret(apiKey),
	}
}

// liveExternalLLMSummary returns a safe, non-secret summary of the saved
// external LLM configuration for snapshots and RPC responses. It NEVER exposes
// the raw API key.
func (c *ColabOneController) liveExternalLLMSummary() map[string]any {
	c.vaultConfigMu.Lock()
	defer c.vaultConfigMu.Unlock()
	return c.liveExternalLLMSummaryLocked()
}

func (c *ColabOneController) mapLiveStudioError(err error) error {
	if err == nil {
		return nil
	}
	if !c.hasRemoteCapability(colabOneLiveStudioCapability) && isMissingRemoteRoute404(err) {
		return errors.New(colabOneLiveStudioUpgradeHint)
	}
	return err
}

func (c *ColabOneController) hydrateVaultHFToken(req map[string]any) error {
	if existing := asTrimmedString(req["hf_token"]); existing != "" {
		return nil
	}
	if c.vault == nil {
		return nil
	}
	v, ok := c.vault.Get("hf")
	if !ok {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		return nil
	}
	cleaned, err := validateCleanHFToken(s)
	if err != nil {
		return err
	}
	if cleaned != "" {
		req["hf_token"] = cleaned
	}
	return nil
}

func (c *ColabOneController) handleLiveStatus(ctx context.Context) (any, error) {
	if c.remoteExplicitlyLacksCapability(colabOneLiveStudioCapability) {
		return nil, errors.New(colabOneLiveStudioUpgradeHint)
	}
	res, err := c.remote(ctx, "GET", "live/status", nil, "", 30*time.Second)
	if err != nil {
		return nil, c.mapLiveStudioError(err)
	}
	return res, nil
}

func (c *ColabOneController) handleLiveVRAMCheck(ctx context.Context, payload map[string]any) (any, error) {
	if c.remoteExplicitlyLacksCapability(colabOneLiveStudioCapability) {
		return nil, errors.New(colabOneLiveStudioUpgradeHint)
	}
	req, err := live.ValidateColabOneLiveStartConfig(payload)
	if err != nil {
		return nil, err
	}
	// live/vram-check expects ColabOneLiveSessionConfig (extra="forbid"),
	// which excludes secrets and start-only fields.
	delete(req, "hf_token")
	delete(req, "external_llm")
	delete(req, "portrait_input_id")
	delete(req, "canvas_config")
	res, err := c.remote(ctx, "POST", "live/vram-check", req, "", 30*time.Second)
	if err != nil {
		return nil, c.mapLiveStudioError(err)
	}
	return res, nil
}

func (c *ColabOneController) handleLiveStart(ctx context.Context, payload map[string]any) (any, error) {
	if c.remoteExplicitlyLacksCapability(colabOneLiveStudioCapability) {
		return nil, errors.New(colabOneLiveStudioUpgradeHint)
	}
	req, err := live.ValidateColabOneLiveStartConfig(payload)
	if err != nil {
		return nil, err
	}

	if asString(req["llm_mode"]) == "external_api" {
		saved := c.vaultLiveExternalLLM()
		var merged map[string]any
		if inline := asMap(req["external_llm"]); inline != nil {
			// ValidateColabOneLiveStartConfig already validated non-empty inline
			// base_url and model; hydrate only the vaulted API key when omitted
			// and targeting the exact same normalized origin.
			targetBaseURL := asTrimmedString(inline["base_url"])
			targetModel := asTrimmedString(inline["model"])
			targetKey := asTrimmedString(inline["api_key"])
			if targetKey == "" && saved["api_key"] != "" && sameExternalLLMOrigin(targetBaseURL, saved["base_url"]) {
				targetKey = saved["api_key"]
			}
			merged = map[string]any{
				"base_url": targetBaseURL,
				"model":    targetModel,
				"api_key":  targetKey,
			}
			if v, exists := inline["temperature"]; exists && v != nil {
				merged["temperature"] = v
			}
			if v, exists := inline["max_tokens"]; exists && v != nil {
				merged["max_tokens"] = v
			}
		} else {
			merged = map[string]any{
				"base_url": saved["base_url"],
				"model":    saved["model"],
				"api_key":  saved["api_key"],
			}
		}

		requireKey := !live.IsColabOneLoopbackURL(asString(merged["base_url"]))
		validatedExt, err := live.ValidateColabOneLiveExternalLLMConfig(merged, requireKey)
		if err != nil {
			return nil, err
		}
		req["external_llm"] = validatedExt
	}

	if err := c.hydrateVaultHFToken(req); err != nil {
		return nil, err
	}

	// Delete explicit nil canvas_config before checking vault hydration, and
	// validate any non-nil inline canvas_config strictly before forwarding.
	if rawCanvas, hasInlineCanvas := req["canvas_config"]; hasInlineCanvas {
		if rawCanvas == nil {
			delete(req, "canvas_config")
		} else {
			inlineCanvasMap := asMap(rawCanvas)
			if inlineCanvasMap == nil {
				return nil, errors.New("ComfyUI 画布配置格式不合法")
			}
			canvasCfg, cfgErr := c.validateComfyCanvasConfig(inlineCanvasMap)
			if cfgErr != nil {
				return nil, errors.New("ComfyUI 画布配置无效：请填写公网 HTTPS 固定域名，并粘贴有效的 Cloudflare Tunnel 安装命令或 Token")
			}
			req["canvas_config"] = canvasCfg
		}
	}

	// Check canvas tunnel readiness: if not ready and user saved a valid
	// comfyui_canvas config, pass canvas_config to live/start so the remote
	// runtime can bring up the canvas tunnel; otherwise log an actionable hint.
	c.mu.Lock()
	rt := deepCopyMap(asMap(c.data["runtime"]))
	c.mu.Unlock()
	comfy := asMap(rt["comfyui"])
	canvas := asMap(comfy["canvas"])
	canvasState := asString(canvas["state"])
	if canvasState != "ready" {
		hasSavedCanvas := false
		if _, hasInlineCanvas := req["canvas_config"]; !hasInlineCanvas && c.vault != nil {
			c.vaultConfigMu.Lock()
			rawCanvas, ok := c.vault.Get("comfyui_canvas")
			c.vaultConfigMu.Unlock()
			if ok {
				if cm := asMap(rawCanvas); cm != nil {
					if canvasCfg, cfgErr := c.validateComfyCanvasConfig(map[string]any{
						"mode":         "named",
						"domain":       asString(cm["domain"]),
						"tunnel_token": asString(cm["tunnel_token"]),
					}); cfgErr == nil && canvasCfg != nil {
						req["canvas_config"] = canvasCfg
						hasSavedCanvas = true
					}
				}
			}
		} else if _, hasInlineCanvas := req["canvas_config"]; hasInlineCanvas {
			hasSavedCanvas = true
		}
		if hasSavedCanvas {
			c.log("检测到 ComfyUI 画布隧道尚未就绪，已附带已保存画布配置请求远端同步拉起", "LiveStudio", "info")
		} else {
			c.log("提示：尚未配置 ComfyUI 固定域名画布隧道，实时演播需先在画布设置中配置并开启固定域名隧道方可建立音视频流连接", "LiveStudio", "warning")
		}
	}

	c.log(
		fmt.Sprintf(
			"请求启动实时演播（视觉：%s · 语音：%s · 对话：%s · 模式：%s）",
			asString(req["visual_engine"]),
			asString(req["tts_engine"]),
			asString(req["llm_mode"]),
			asString(req["director_mode"]),
		),
		"LiveStudio",
		"info",
	)

	res, err := c.remote(ctx, "POST", "live/start", req, "", 120*time.Second)
	if err != nil {
		return nil, c.mapLiveStudioError(err)
	}
	if err := c.refreshRemoteBounded(ctx, 0); err != nil {
		c.log("远端已受理实时演播启动请求，但刷新状态失败（将自动重试）："+errTypeName(err), "LiveStudio", "warning")
		return res, nil
	}
	c.log("远端已受理实时演播启动请求", "LiveStudio", "info")
	return res, nil
}

func (c *ColabOneController) handleLiveStop(ctx context.Context) (any, error) {
	if c.remoteExplicitlyLacksCapability(colabOneLiveStudioCapability) {
		return nil, errors.New(colabOneLiveStudioUpgradeHint)
	}
	c.log("正在停止远端实时演播服务", "LiveStudio", "info")
	res, err := c.remote(ctx, "POST", "live/stop", nil, "", 60*time.Second)
	if err != nil {
		return nil, c.mapLiveStudioError(err)
	}
	if err := c.refreshRemoteBounded(ctx, 0); err != nil {
		c.log("已停止远端实时演播服务，但刷新状态失败（将自动重试）："+errTypeName(err), "LiveStudio", "warning")
		return res, nil
	}
	c.log("远端实时演播服务已停止", "LiveStudio", "info")
	return res, nil
}

func (c *ColabOneController) handleLiveSessionToken(ctx context.Context) (any, error) {
	if c.remoteExplicitlyLacksCapability(colabOneLiveStudioCapability) {
		return nil, errors.New(colabOneLiveStudioUpgradeHint)
	}
	res, err := c.remote(ctx, "POST", "live/session-token", nil, "", 30*time.Second)
	if err != nil {
		return nil, c.mapLiveStudioError(err)
	}
	return res, nil
}

func (c *ColabOneController) activeConfiguredCanvasOrigin() string {
	normalizeOrigin := func(raw string) string {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return ""
		}
		if !strings.Contains(trimmed, "://") {
			trimmed = "https://" + trimmed
		}
		orig, err := live.ColabOneNormalizedURLOrigin(trimmed)
		if err != nil {
			return ""
		}
		return orig
	}

	c.mu.Lock()
	rt := asMap(c.data["runtime"])
	comfy := asMap(rt["comfyui"])
	canvas := asMap(comfy["canvas"])
	runtimeCanvasURL := asString(canvas["url"])
	c.mu.Unlock()
	if orig := normalizeOrigin(runtimeCanvasURL); orig != "" {
		return orig
	}

	if c.vault != nil {
		c.vaultConfigMu.Lock()
		rawCanvas, ok := c.vault.Get("comfyui_canvas")
		c.vaultConfigMu.Unlock()
		if ok {
			if cm := asMap(rawCanvas); cm != nil {
				if orig := normalizeOrigin(asString(cm["domain"])); orig != "" {
					return orig
				}
			}
		}
	}
	return ""
}

func hasControlOrSpaceRune(s string) bool {
	for _, ch := range s {
		if ch == '\r' || ch == '\n' || ch == 0 || ch < 0x20 || ch == 0x7f || unicode.IsControl(ch) || unicode.IsSpace(ch) {
			return true
		}
	}
	return false
}

func (c *ColabOneController) validateAndNormalizeLiveCompanionTicket(res any) (map[string]any, error) {
	m := asMap(res)
	if m == nil {
		return nil, errors.New(colabOneLiveCompanionTicketInvalidMsg)
	}
	out := deepCopyMap(m)

	rawCode, ok := m["code"].(string)
	if !ok || !reLiveCompanionTicketCode.MatchString(rawCode) {
		return nil, errors.New(colabOneLiveCompanionTicketInvalidMsg)
	}

	if rawPath, exists := m["companion_path"]; exists && rawPath != nil {
		p, ok := rawPath.(string)
		if !ok || (p != "" && p != colabOneLiveCompanionPath) {
			return nil, errors.New(colabOneLiveCompanionTicketInvalidMsg)
		}
	}
	out["companion_path"] = colabOneLiveCompanionPath

	expectedOrigin := c.activeConfiguredCanvasOrigin()
	normPublicOrigin := ""

	if rawOrigin, exists := m["public_origin"]; exists && rawOrigin != nil {
		s, ok := rawOrigin.(string)
		if !ok {
			return nil, errors.New(colabOneLiveCompanionTicketInvalidMsg)
		}
		if s != "" {
			if hasControlOrSpaceRune(s) {
				return nil, errors.New(colabOneLiveCompanionTicketInvalidMsg)
			}
			orig, err := live.ValidateColabOnePublicHTTPSOrigin(s)
			if err != nil {
				return nil, errors.New(colabOneLiveCompanionTicketInvalidMsg)
			}
			if expectedOrigin != "" && orig != expectedOrigin {
				return nil, errors.New(colabOneLiveCompanionTicketInvalidMsg)
			}
			normPublicOrigin = orig
			out["public_origin"] = normPublicOrigin
		} else {
			out["public_origin"] = ""
		}
	}

	if rawURL, exists := m["companion_url"]; exists && rawURL != nil {
		s, ok := rawURL.(string)
		if !ok {
			return nil, errors.New(colabOneLiveCompanionTicketInvalidMsg)
		}
		if s != "" {
			if hasControlOrSpaceRune(s) {
				return nil, errors.New(colabOneLiveCompanionTicketInvalidMsg)
			}
			u, err := url.Parse(s)
			if err != nil || u.Opaque != "" || u.User != nil {
				return nil, errors.New(colabOneLiveCompanionTicketInvalidMsg)
			}
			if !strings.EqualFold(u.Scheme, "https") ||
				u.Host == "" ||
				u.RawPath != "" ||
				u.Path != colabOneLiveCompanionPath ||
				u.RawQuery != "" ||
				u.ForceQuery ||
				u.RawFragment != "" ||
				u.Fragment != "code="+rawCode {
				return nil, errors.New(colabOneLiveCompanionTicketInvalidMsg)
			}
			urlOrigin, err := live.ValidateColabOnePublicHTTPSOrigin("https://" + u.Host)
			if err != nil {
				return nil, errors.New(colabOneLiveCompanionTicketInvalidMsg)
			}
			if expectedOrigin != "" && urlOrigin != expectedOrigin {
				return nil, errors.New(colabOneLiveCompanionTicketInvalidMsg)
			}
			if normPublicOrigin != "" && urlOrigin != normPublicOrigin {
				return nil, errors.New(colabOneLiveCompanionTicketInvalidMsg)
			}
			out["companion_url"] = urlOrigin + colabOneLiveCompanionPath + "#code=" + rawCode
		} else {
			out["companion_url"] = ""
		}
	}

	return out, nil
}

func (c *ColabOneController) handleLiveCompanionTicket(ctx context.Context) (any, error) {
	if c.remoteExplicitlyLacksCapability(colabOneLiveStudioCapability) {
		return nil, errors.New(colabOneLiveStudioUpgradeHint)
	}
	res, err := c.remote(ctx, "POST", "live/companion-ticket", nil, "", 30*time.Second)
	if err != nil {
		if isMissingRemoteRoute404(err) {
			return nil, errors.New(colabOneLiveStudioUpgradeHint)
		}
		return nil, c.mapLiveStudioError(err)
	}
	normalized, valErr := c.validateAndNormalizeLiveCompanionTicket(res)
	if valErr != nil {
		return nil, valErr
	}
	c.log("已生成实时演播远端设备一次性扫码配对凭证", "LiveStudio", "info")
	return normalized, nil
}

func (c *ColabOneController) handleLiveCompanionDisconnect(ctx context.Context) (any, error) {
	if c.remoteExplicitlyLacksCapability(colabOneLiveStudioCapability) {
		return nil, errors.New(colabOneLiveStudioUpgradeHint)
	}
	res, err := c.remote(ctx, "POST", "live/companion-disconnect", nil, "", 30*time.Second)
	if err != nil {
		if isMissingRemoteRoute404(err) {
			return nil, errors.New(colabOneLiveStudioUpgradeHint)
		}
		return nil, c.mapLiveStudioError(err)
	}
	if err := c.refreshRemoteBounded(ctx, 0); err != nil {
		c.log("已断开远端输入设备，但刷新状态失败（将自动重试）："+errTypeName(err), "LiveStudio", "warning")
		return res, nil
	}
	c.log("已断开实时演播远端输入设备", "LiveStudio", "info")
	return res, nil
}

func (c *ColabOneController) handleLiveLLMGet() (any, error) {
	return c.liveExternalLLMSummary(), nil
}

func (c *ColabOneController) handleLiveLLMSave(payload map[string]any) (any, error) {
	if c.vault == nil {
		return nil, errors.New("本地保险库未配置")
	}
	c.vaultConfigMu.Lock()
	existing := c.vaultLiveExternalLLMLocked()

	clearKey := false
	if rawClear, exists := payload["clear_key"]; exists && rawClear != nil {
		b, ok := isStrictBool(rawClear)
		if !ok {
			c.vaultConfigMu.Unlock()
			return nil, errors.New("clear_key 必须为布尔值")
		}
		clearKey = b
	}

	candidate := map[string]any{
		"base_url": existing["base_url"],
		"model":    existing["model"],
	}
	if v, exists := payload["base_url"]; exists {
		candidate["base_url"] = v
	}
	if v, exists := payload["model"]; exists {
		candidate["model"] = v
	}

	candidateBaseURL := ""
	if s, ok := candidate["base_url"].(string); ok {
		candidateBaseURL = strings.TrimSpace(s)
	}

	hasInlineKey := false
	rawInlineKey := ""
	if rawKey, exists := payload["api_key"]; exists && rawKey != nil {
		keyStr, ok := rawKey.(string)
		if !ok {
			c.vaultConfigMu.Unlock()
			return nil, errors.New("外部 LLM API Key 格式不合法")
		}
		if strings.Trim(keyStr, " ") != "" {
			hasInlineKey = true
			rawInlineKey = keyStr
		}
	}

	if clearKey {
		candidate["api_key"] = ""
	} else if hasInlineKey {
		candidate["api_key"] = rawInlineKey
	} else if existing["api_key"] != "" && sameExternalLLMOrigin(candidateBaseURL, existing["base_url"]) {
		// Preserve the vaulted API key ONLY when the normalized origin is unchanged.
		candidate["api_key"] = existing["api_key"]
	} else {
		candidate["api_key"] = ""
	}

	requireKey := !clearKey && !live.IsColabOneLoopbackURL(candidateBaseURL)
	validated, err := live.ValidateColabOneLiveExternalLLMConfig(candidate, requireKey)
	if err != nil {
		c.vaultConfigMu.Unlock()
		return nil, err
	}

	stored := map[string]any{
		"base_url": asString(validated["base_url"]),
		"model":    asString(validated["model"]),
		"api_key":  asString(validated["api_key"]),
	}
	if err := c.vault.Set(colabOneVaultLiveExternalLLMKey, stored); err != nil {
		c.vaultConfigMu.Unlock()
		return nil, err
	}

	summary := c.liveExternalLLMSummaryLocked()
	c.vaultConfigMu.Unlock()
	c.Update(map[string]any{"live_external_llm": summary})

	masked := asString(summary["masked_api_key"])
	if masked == "" {
		masked = "未设置"
	}
	c.log(
		fmt.Sprintf("已保存实时演播外部 LLM 配置（模型：%s · 密钥：%s）", asString(summary["model"]), masked),
		"LiveStudio",
		"info",
	)
	return summary, nil
}

func (c *ColabOneController) handleLiveLLMClear() (any, error) {
	if c.vault == nil {
		return nil, errors.New("本地保险库未配置")
	}
	c.vaultConfigMu.Lock()
	if err := c.vault.Delete(colabOneVaultLiveExternalLLMKey); err != nil {
		c.vaultConfigMu.Unlock()
		return nil, err
	}
	summary := c.liveExternalLLMSummaryLocked()
	c.vaultConfigMu.Unlock()
	c.Update(map[string]any{"live_external_llm": summary})
	c.log("已清除实时演播外部 LLM 配置与密钥", "LiveStudio", "info")
	out := deepCopyMap(summary)
	out["ok"] = true
	return out, nil
}

func (c *ColabOneController) handleLiveLLMTest(ctx context.Context, payload map[string]any) (any, error) {
	if c.remoteExplicitlyLacksCapability(colabOneLiveStudioCapability) {
		return nil, errors.New(colabOneLiveStudioUpgradeHint)
	}
	saved := c.vaultLiveExternalLLM()

	candidate := map[string]any{
		"base_url": saved["base_url"],
		"model":    saved["model"],
	}
	inlineBaseURL := ""
	if v, exists := payload["base_url"]; exists && v != nil {
		s, ok := v.(string)
		if !ok {
			candidate["base_url"] = v
		} else if strings.Trim(s, " ") != "" {
			inlineBaseURL = strings.TrimSpace(s)
			candidate["base_url"] = s
		}
	}
	if v, exists := payload["model"]; exists && v != nil {
		if s, ok := v.(string); !ok || strings.Trim(s, " ") != "" {
			candidate["model"] = v
		}
	}

	hasInlineKey := false
	rawInlineKey := ""
	if v, exists := payload["api_key"]; exists && v != nil {
		s, ok := v.(string)
		if !ok {
			return nil, errors.New("外部 LLM API Key 格式不合法")
		}
		if strings.Trim(s, " ") != "" {
			hasInlineKey = true
			rawInlineKey = s
		}
	}

	if hasInlineKey {
		candidate["api_key"] = rawInlineKey
	} else if saved["api_key"] != "" &&
		(inlineBaseURL == "" || sameExternalLLMOrigin(inlineBaseURL, saved["base_url"])) {
		// Strictly hydrate the vaulted API key ONLY for the exact same normalized origin.
		candidate["api_key"] = saved["api_key"]
	} else {
		candidate["api_key"] = ""
	}

	baseURLPreview := ""
	if s, ok := candidate["base_url"].(string); ok {
		baseURLPreview = s
	}
	requireKey := !live.IsColabOneLoopbackURL(baseURLPreview)
	validated, err := live.ValidateColabOneLiveExternalLLMConfig(candidate, requireKey)
	if err != nil {
		return nil, err
	}
	testBody := map[string]any{
		"base_url": asString(validated["base_url"]),
		"model":    asString(validated["model"]),
		"api_key":  asString(validated["api_key"]),
	}

	timeoutSec := 10.0
	if rawTimeout, exists := payload["timeout_seconds"]; exists && rawTimeout != nil {
		if _, isBool := isStrictBool(rawTimeout); isBool {
			return nil, errors.New("外部 LLM 测试超时参数必须在 1 到 60 秒之间")
		}
		parsedTimeout, ok := asFloat(rawTimeout)
		if !ok || math.IsNaN(parsedTimeout) || math.IsInf(parsedTimeout, 0) || parsedTimeout < 1 || parsedTimeout > 60 {
			return nil, errors.New("外部 LLM 测试超时参数必须在 1 到 60 秒之间")
		}
		timeoutSec = parsedTimeout
		testBody["timeout_seconds"] = timeoutSec
	}

	rpcTimeout := min(120*time.Second, time.Duration(math.Ceil(2*timeoutSec+15))*time.Second)
	res, err := c.remote(ctx, "POST", "live/llm-test", testBody, "", rpcTimeout)
	if err != nil {
		return nil, c.mapLiveStudioError(err)
	}
	resMap := asMap(res)
	if resMap != nil && !asBoolDefault(resMap, "ok", true) {
		c.log(fmt.Sprintf("实时演播外部 LLM 连通性测试未通过（模型：%s）", asString(validated["model"])), "LiveStudio", "warning")
	} else {
		c.log(fmt.Sprintf("实时演播外部 LLM 连通性测试通过（模型：%s）", asString(validated["model"])), "LiveStudio", "info")
	}
	return res, nil
}

func (c *ColabOneController) handleLiveAvatarsList(ctx context.Context) (any, error) {
	if c.remoteExplicitlyLacksCapability(colabOneLiveStudioCapability) {
		return nil, errors.New(colabOneLiveStudioUpgradeHint)
	}
	res, err := c.remote(ctx, "GET", "live/avatars", nil, "", 30*time.Second)
	if err != nil {
		return nil, c.mapLiveStudioError(err)
	}
	if list := asMapSlice(res); list != nil {
		c.Update(map[string]any{"live_avatars": list})
	} else if m := asMap(res); m != nil {
		if nested := asMapSlice(m["avatars"]); nested != nil {
			c.Update(map[string]any{"live_avatars": nested})
		}
	}
	return res, nil
}

func (c *ColabOneController) handleLiveAvatarsSave(ctx context.Context, payload map[string]any) (any, error) {
	if c.remoteExplicitlyLacksCapability(colabOneLiveStudioCapability) {
		return nil, errors.New(colabOneLiveStudioUpgradeHint)
	}
	req, err := live.ValidateColabOneLiveAvatarPayload(payload)
	if err != nil {
		return nil, err
	}
	name := asString(req["name"])
	c.log("正在保存实时演播形象："+name, "LiveStudio", "info")
	res, err := c.remote(ctx, "POST", "live/avatars", req, "", 60*time.Second)
	if err != nil {
		return nil, c.mapLiveStudioError(err)
	}
	if err := c.refreshRemoteBounded(ctx, 0); err != nil {
		c.log("实时演播形象已保存，但刷新状态失败（将自动重试）："+errTypeName(err), "LiveStudio", "warning")
		return res, nil
	}
	c.log("实时演播形象已保存："+name, "LiveStudio", "info")
	return res, nil
}

func hasASCIIControlChars(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

func extractStrictLiveID(payload map[string]any, primaryKey, aliasKey, invalidMsg string) (string, error) {
	var primaryVal, aliasVal string
	if raw, exists := payload[primaryKey]; exists && raw != nil {
		s, ok := raw.(string)
		if !ok || hasASCIIControlChars(s) {
			return "", errors.New(invalidMsg)
		}
		primaryVal = strings.TrimSpace(s)
	}
	if raw, exists := payload[aliasKey]; exists && raw != nil {
		s, ok := raw.(string)
		if !ok || hasASCIIControlChars(s) {
			return "", errors.New(invalidMsg)
		}
		aliasVal = strings.TrimSpace(s)
	}
	if primaryVal != "" && aliasVal != "" && primaryVal != aliasVal {
		return "", errors.New(invalidMsg)
	}
	chosen := primaryVal
	if chosen == "" {
		chosen = aliasVal
	}
	id, err := live.ValidateColabOneLiveAvatarID(chosen)
	if err != nil {
		if invalidMsg != "" {
			return "", errors.New(invalidMsg)
		}
		return "", err
	}
	return id, nil
}

func (c *ColabOneController) handleLiveAvatarsDelete(ctx context.Context, payload map[string]any) (any, error) {
	if c.remoteExplicitlyLacksCapability(colabOneLiveStudioCapability) {
		return nil, errors.New(colabOneLiveStudioUpgradeHint)
	}
	avatarID, err := extractStrictLiveID(payload, "avatar_id", "id", "形象 ID 不合法")
	if err != nil {
		return nil, err
	}
	c.log("正在删除实时演播形象："+avatarID, "LiveStudio", "info")
	res, err := c.remote(ctx, "DELETE", "live/avatars/"+avatarID, nil, "", 30*time.Second)
	if err != nil {
		return nil, c.mapLiveStudioError(err)
	}
	if err := c.refreshRemoteBounded(ctx, 0); err != nil {
		c.log("已删除实时演播形象，但刷新状态失败（将自动重试）："+errTypeName(err), "LiveStudio", "warning")
		return res, nil
	}
	c.log("已删除实时演播形象："+avatarID, "LiveStudio", "info")
	return res, nil
}

func (c *ColabOneController) handleLiveAvatarClipUpload(ctx context.Context, payload map[string]any) (any, error) {
	if c.remoteExplicitlyLacksCapability(colabOneLiveStudioCapability) {
		return nil, errors.New(colabOneLiveStudioUpgradeHint)
	}
	avatarID, err := extractStrictLiveID(payload, "avatar_id", "id", "形象 ID 不合法")
	if err != nil {
		return nil, err
	}
	clipPayload := make(map[string]any, len(payload))
	for k, v := range payload {
		if k == "avatar_id" || k == "id" {
			continue
		}
		clipPayload[k] = v
	}
	req, err := live.ValidateColabOneLiveAvatarClipUpload(clipPayload)
	if err != nil {
		return nil, err
	}
	display := asString(req["filename"])
	c.log("正在上传实时演播形象素材："+display, "LiveStudio", "info")
	res, err := c.remote(ctx, "POST", "live/avatars/"+avatarID+"/clips", req, "", 300*time.Second)
	if err != nil {
		return nil, c.mapLiveStudioError(err)
	}
	if err := c.refreshRemoteBounded(ctx, 0); err != nil {
		c.log("实时演播形象素材上传完成，但刷新状态失败（将自动重试）："+errTypeName(err), "LiveStudio", "warning")
		return res, nil
	}
	c.log("实时演播形象素材上传完成："+display, "LiveStudio", "info")
	return res, nil
}

func (c *ColabOneController) handleLiveAvatarBuildPack(ctx context.Context, payload map[string]any) (any, error) {
	if c.remoteExplicitlyLacksCapability(colabOneLiveStudioCapability) {
		return nil, errors.New(colabOneLiveStudioUpgradeHint)
	}
	req, err := live.ValidateColabOneLiveBuildPackPayload(payload)
	if err != nil {
		return nil, err
	}
	if err := c.hydrateVaultHFToken(req); err != nil {
		return nil, err
	}
	avatarID := asString(req["avatar_id"])
	c.log("请求构建实时演播形象动作库："+avatarID, "LiveStudio", "info")
	res, err := c.remote(ctx, "POST", "live/avatars/build-pack", req, "", 120*time.Second)
	if err != nil {
		return nil, c.mapLiveStudioError(err)
	}
	if err := c.refreshRemoteBounded(ctx, 0); err != nil {
		c.log("远端已受理形象动作库构建任务，但刷新状态失败（将自动重试）："+errTypeName(err), "LiveStudio", "warning")
		return res, nil
	}
	c.log("远端已受理形象动作库构建任务："+avatarID, "LiveStudio", "info")
	return res, nil
}

func (c *ColabOneController) handleLiveDrivingList(ctx context.Context) (any, error) {
	if c.remoteExplicitlyLacksCapability(colabOneLiveStudioCapability) {
		return nil, errors.New(colabOneLiveStudioUpgradeHint)
	}
	res, err := c.remote(ctx, "GET", "live/driving-motions", nil, "", 30*time.Second)
	if err != nil {
		return nil, c.mapLiveStudioError(err)
	}
	if list := asMapSlice(res); list != nil {
		c.Update(map[string]any{"live_driving_motions": list})
	} else if m := asMap(res); m != nil {
		if nested := asMapSlice(m["driving_motions"]); nested != nil {
			c.Update(map[string]any{"live_driving_motions": nested})
		} else if nested := asMapSlice(m["motions"]); nested != nil {
			c.Update(map[string]any{"live_driving_motions": nested})
		}
	}
	return res, nil
}

func (c *ColabOneController) handleLiveDrivingUpload(ctx context.Context, payload map[string]any) (any, error) {
	if c.remoteExplicitlyLacksCapability(colabOneLiveStudioCapability) {
		return nil, errors.New(colabOneLiveStudioUpgradeHint)
	}
	req, err := live.ValidateColabOneLiveDrivingUpload(payload)
	if err != nil {
		return nil, err
	}
	display := asString(req["filename"])
	c.log("正在上传实时演播驱动视频素材："+display, "LiveStudio", "info")
	res, err := c.remote(ctx, "POST", "live/driving-motions", req, "", 300*time.Second)
	if err != nil {
		return nil, c.mapLiveStudioError(err)
	}
	if err := c.refreshRemoteBounded(ctx, 0); err != nil {
		c.log("实时演播驱动视频素材上传完成，但刷新状态失败（将自动重试）："+errTypeName(err), "LiveStudio", "warning")
		return res, nil
	}
	c.log("实时演播驱动视频素材上传完成："+display, "LiveStudio", "info")
	return res, nil
}

func (c *ColabOneController) handleLiveDrivingDelete(ctx context.Context, payload map[string]any) (any, error) {
	if c.remoteExplicitlyLacksCapability(colabOneLiveStudioCapability) {
		return nil, errors.New(colabOneLiveStudioUpgradeHint)
	}
	motionID, err := extractStrictLiveID(payload, "motion_id", "id", "驱动动作 ID 不合法")
	if err != nil {
		return nil, err
	}
	c.log("正在删除实时演播驱动视频素材："+motionID, "LiveStudio", "info")
	res, err := c.remote(ctx, "DELETE", "live/driving-motions/"+motionID, nil, "", 30*time.Second)
	if err != nil {
		return nil, c.mapLiveStudioError(err)
	}
	if err := c.refreshRemoteBounded(ctx, 0); err != nil {
		c.log("已删除实时演播驱动视频素材，但刷新状态失败（将自动重试）："+errTypeName(err), "LiveStudio", "warning")
		return res, nil
	}
	c.log("已删除实时演播驱动视频素材："+motionID, "LiveStudio", "info")
	return res, nil
}
