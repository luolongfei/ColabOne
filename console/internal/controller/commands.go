package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/luolongfei/ColabOne/console/internal/comfyui"
)

const (
	colabOneTtsPreviewUpgradeHint   = "当前远端服务版本暂不支持 TTS 音色试听，请先点击顶部「立即热更新」升级远端服务后再试"
	colabOneLongCatNativeUpgradeHint = "当前远端实例服务版本较旧，暂不支持原生 LongCat-Video-Avatar 1.6 管线，请先点击「热更新服务」或重新启动 GPU 实例"
	colabOneTtsPreviewChunkBytes    = 512 * 1024
	colabOneMaxTtsPreviewBytes      = 16 * 1024 * 1024
	colabOneMaxTtsPreviewChunks     = 256
)

func (c *ColabOneController) hasExportedDriveOutput(identity string) bool {
	cleanID := strings.ToLower(strings.TrimSpace(identity))
	if !reHex32ID.MatchString(cleanID) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	acctKey := c.currentDriveAccountKeyLocked()
	if acctKey == "" || c.driveOutputs == nil {
		return false
	}
	bucket := c.driveOutputs[acctKey]
	if bucket == nil {
		return false
	}
	rec := bucket[cleanID]
	return rec != nil && (sanitizeDriveFileID(rec.DriveFileID) != "" || rec.DriveExported)
}

func (c *ColabOneController) builtinTemplateVideoBackend(workflowID string) string {
	if workflowID == "" {
		return ""
	}
	for _, wf := range c.builtinWorkflows() {
		if asString(wf["id"]) != workflowID {
			continue
		}
		if pipe := asMap(wf["pipeline"]); pipe != nil {
			return asString(pipe["video_backend"])
		}
		return ""
	}
	return ""
}

func (c *ColabOneController) readyRemoteLacksCapability(capability string) bool {
	if capability == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	rt := asMap(c.data["runtime"])
	if rt == nil || asString(rt["state"]) != "ready" {
		return false
	}
	for _, item := range asAnySlice(rt["capabilities"]) {
		if asString(item) == capability {
			return false
		}
	}
	return true
}

func (c *ColabOneController) remoteExplicitlyLacksCapability(capability string) bool {
	if capability == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	rt := asMap(c.data["runtime"])
	if rt == nil || asString(rt["state"]) != "ready" {
		return false
	}
	caps := asAnySlice(rt["capabilities"])
	if len(caps) == 0 {
		return false
	}
	for _, item := range caps {
		if asString(item) == capability {
			return false
		}
	}
	return true
}

func isMissingRemoteRoute404(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.TrimSpace(err.Error())
	lower := strings.ToLower(msg)
	if strings.Contains(msg, "不存在") ||
		strings.Contains(msg, "Colab request failed") ||
		strings.Contains(lower, "kernel") ||
		strings.Contains(lower, "proxy") {
		return false
	}
	return strings.EqualFold(msg, "not found") ||
		msg == "远端服务返回 HTTP 404" ||
		(httpStatusCode(err) == 404 && !strings.Contains(msg, "Colab request failed"))
}

// ComfyTtsPreviewValidator is an optional interface that ComfyValidator implementations
// may satisfy to validate TTS voice preview requests before making a remote RPC.
type ComfyTtsPreviewValidator interface {
	ValidateTtsPreviewRequest(raw map[string]any) (map[string]any, error)
}

// Command dispatches a desktop RPC action and sanitizes the result for JSON transport.
func (c *ColabOneController) Command(ctx context.Context, action string, payload map[string]any) (any, error) {
	if ctx == nil {
		ctx = c.rootCtx
	}
	if payload == nil {
		payload = map[string]any{}
	}
	res, err := c.dispatchCommand(ctx, action, payload)
	if err != nil {
		return nil, err
	}
	return SanitizeJSONValue(res), nil
}

func (c *ColabOneController) dispatchCommand(ctx context.Context, action string, payload map[string]any) (any, error) {
	switch action {
	case "snapshot":
		return c.Snapshot(), nil

	case "locale.set":
		rawLocale := asTrimmedString(payload["locale"])
		persist := asBool(payload["persist"])
		if !validColabOneLocales[rawLocale] {
			return nil, errors.New("不支持的语言选项")
		}
		if c.auth != nil {
			c.auth.SetLocale(rawLocale)
		}
		if persist {
			c.persistMu.Lock()
			c.mu.Lock()
			c.settings["locale"] = rawLocale
			snapshotSettings := deepCopyMap(c.settings)
			c.mu.Unlock()
			saveErr := AtomicJSON(filepath.Join(c.directory, "settings.json"), snapshotSettings)
			c.persistMu.Unlock()
			if saveErr != nil {
				return nil, saveErr
			}
		}
		return map[string]any{"ok": true, "locale": rawLocale, "persisted": persist}, nil

	case "billing.usage":
		c.mu.Lock()
		acct := deepCopyMap(asMap(c.data["account"]))
		c.mu.Unlock()
		colabCreds, _ := c.vault.Get("colab")
		loggedIn := asBool(acct["logged_in"]) || asBool(colabCreds)

		accountEmail := asTrimmedString(acct["email"])
		if accountEmail == "" && loggedIn {
			if profVal, ok := c.vault.Get("google_profile"); ok {
				accountEmail = asTrimmedString(asMap(profVal)["email"])
			}
		}

		var startSec, endSec int64
		if v, ok := asInt64(payload["start"]); ok {
			startSec = v
		}
		if v, ok := asInt64(payload["end"]); ok {
			endSec = v
		}
		timezone := asTrimmedString(payload["timezone"])
		var utcOffsetPtr *int
		if rawOff, exists := payload["utc_offset_seconds"]; exists && rawOff != nil {
			if off, ok := asInt(rawOff); ok {
				utcOffsetPtr = &off
			}
		}

		var curBalPtr, curRatePtr *float64
		if bal, ok := asFloat(acct["balance"]); ok {
			curBalPtr = &bal
		}
		if rate, ok := asFloat(acct["rate"]); ok {
			curRatePtr = &rate
		}
		checkedAt, _ := asFloat(acct["checked_at"])

		return c.cuUsage.Query(ColabOneCUUsageQuery{
			AccountEmail:   accountEmail,
			LoggedIn:       loggedIn,
			CurrentBalance: curBalPtr,
			CurrentRate:    curRatePtr,
			CheckedAt:      checkedAt,
			Start:          startSec,
			End:            endSec,
			Timezone:       timezone,
			UTCOffsetSec:   utcOffsetPtr,
		}), nil

	case "account.refresh":
		if c.provider == nil {
			return nil, errors.New("Provider 未配置")
		}
		c.log("正在刷新 Google 账号余额与运行实例状态", "计费", "info")
		acctGen, acctCreds := c.snapshotAccountGuard()
		account, err := c.provider.Account(ctx)
		if err != nil {
			return nil, err
		}
		if account == nil {
			account = map[string]any{"logged_in": false}
		}
		if !c.applyAccountRefreshIfCurrent(acctGen, acctCreds, account, nowUnixFloat()) {
			return map[string]any{"ok": true, "stale": true, "assignments": 0}, nil
		}
		c.observerWake.Set()
		assignments, _ := asInt(account["assignments"])
		if asBool(account["logged_in"]) {
			balance, _ := asFloat(account["balance"])
			rate, _ := asFloat(account["rate"])
			c.log(
				fmt.Sprintf("账号状态已刷新 · 余额 %.2f CU · 费率 %.2f CU/小时 · 活跃 %d 台", balance, rate, assignments),
				"计费",
				"info",
			)
		} else {
			c.log("账号状态已刷新（当前未登录）", "计费", "info")
		}
		return map[string]any{"ok": true, "assignments": assignments}, nil

	case "runtime.select":
		return c.SelectInstance(ctx, asString(payload["endpoint"]))

	case "monitor.select":
		return c.SelectMonitor(asString(payload["endpoint"]))

	case "monitor.pause":
		var paused *bool
		if v, exists := payload["paused"]; exists && v != nil {
			b := asBool(v)
			paused = &b
		}
		return c.SetMonitorPaused(paused), nil

	case "monitor.retry":
		return c.RetryMonitor(), nil

	case "api.key":
		c.vaultConfigMu.Lock()
		api := map[string]any{}
		if v, ok := c.vault.Get("public_api"); ok {
			if m := asMap(v); m != nil {
				api = deepCopyMap(m)
			}
		}
		if asString(api["api_key"]) == "" {
			api["api_key"] = generateAPIKey()
			if err := c.vault.Set("public_api", api); err != nil {
				c.vaultConfigMu.Unlock()
				return nil, err
			}
		}
		key := asString(api["api_key"])
		c.vaultConfigMu.Unlock()
		return map[string]any{"key": key}, nil

	case "api.tunnel_token":
		c.vaultConfigMu.Lock()
		api := map[string]any{}
		if v, ok := c.vault.Get("public_api"); ok {
			if m := asMap(v); m != nil {
				api = m
			}
		}
		tok := asString(api["tunnel_token"])
		c.vaultConfigMu.Unlock()
		return map[string]any{"token": tok}, nil

	case "api.rotate":
		// Revoke the running gateway first. Never report a local-only rotation as successful.
		if len(c.vaultSessions()) > 0 {
			c.log("正在停止运行中的对外 API 以吊销旧密钥", "API", "info")
			if _, err := c.remote(ctx, "POST", "public-api/stop", nil, "", 0); err != nil {
				return nil, err
			}
			if err := c.RefreshRemote(ctx, ""); err != nil {
				return nil, err
			}
		}
		c.vaultConfigMu.Lock()
		api := map[string]any{}
		if v, ok := c.vault.Get("public_api"); ok {
			if m := asMap(v); m != nil {
				api = deepCopyMap(m)
			}
		}
		api["api_key"] = generateAPIKey()
		if err := c.vault.Set("public_api", api); err != nil {
			c.vaultConfigMu.Unlock()
			return nil, err
		}
		c.vaultConfigMu.Unlock()
		c.log("API Key 已更换；重新开启服务后使用新密钥", "API", "info")
		return map[string]any{"ok": true}, nil

	case "api.start", "api.save":
		if rawMode, exists := payload["mode"]; exists && rawMode != nil {
			modeStr := asString(rawMode)
			if modeStr != "" && modeStr != "named" {
				return nil, errors.New("已不再支持临时域名，请配置固定域名和 Cloudflare Tunnel Token")
			}
		}
		c.vaultConfigMu.Lock()
		saved := map[string]any{}
		if v, ok := c.vault.Get("public_api"); ok {
			if m := asMap(v); m != nil {
				saved = deepCopyMap(m)
			}
		}
		domain := asString(saved["domain"])
		if _, exists := payload["domain"]; exists {
			domain = asString(payload["domain"])
		}
		tunnelTok := asString(payload["tunnel_token"])
		if tunnelTok == "" {
			tunnelTok = asString(saved["tunnel_token"])
		}
		apiKey := asString(saved["api_key"])
		if apiKey == "" {
			apiKey = generateAPIKey()
		}
		config, err := c.validatePublicAPIConfig(map[string]any{
			"mode":         "named",
			"domain":       domain,
			"tunnel_token": tunnelTok,
			"api_key":      apiKey,
		})
		if err != nil {
			c.vaultConfigMu.Unlock()
			return nil, errors.New("API 配置无效：请填写公网 HTTPS 固定域名，并粘贴有效的 Cloudflare Tunnel 安装命令或 Token")
		}
		stored := deepCopyMap(saved)
		stored["mode"] = "named"
		stored["api_key"] = asString(config["api_key"])
		stored["domain"] = asString(config["domain"])
		stored["tunnel_token"] = asString(config["tunnel_token"])
		if err := c.vault.Set("public_api", stored); err != nil {
			c.vaultConfigMu.Unlock()
			return nil, err
		}
		c.vaultConfigMu.Unlock()
		if action == "api.save" {
			c.log("已保存对外 API 固定域名与隧道配置", "API", "info")
			return map[string]any{"ok": true}, nil
		}
		cfgCopy := deepCopyMap(config)
		return c.asyncOperation("开启远端对外 API", func() error {
			c.log("正在向远端下发对外 API 配置并启动网关", "API", "info")
			if _, err := c.remote(c.rootCtx, "POST", "public-api/start", cfgCopy, "", 0); err != nil {
				return err
			}
			c.log("远端已受理对外 API 启动请求，正在刷新状态", "API", "info")
			return c.RefreshRemote(c.rootCtx, "")
		}, nil, nil)

	case "api.stop":
		return c.asyncOperation("关闭对外 API", func() error {
			c.log("正在关闭远端对外 API 与 Cloudflare 隧道", "API", "info")
			if _, err := c.remote(c.rootCtx, "POST", "public-api/stop", nil, "", 0); err != nil {
				return err
			}
			if err := c.RefreshRemote(c.rootCtx, ""); err != nil {
				return err
			}
			c.log("远端对外 API 已关闭", "API", "info")
			return nil
		}, nil, nil)

	case "comfyui.start":
		c.log("请求启动远端 ComfyUI 引擎", "ComfyUI", "info")
		res, err := c.remote(ctx, "POST", "comfyui/start", nil, "", 0)
		if err != nil {
			return nil, err
		}
		if err := c.RefreshRemote(ctx, ""); err != nil {
			return nil, err
		}
		c.log("远端已受理 ComfyUI 启动任务", "ComfyUI", "info")
		return res, nil

	case "comfyui.stop":
		return c.asyncOperation("停止远端 ComfyUI", func() error {
			c.log("正在停止远端 ComfyUI 引擎与画布隧道", "ComfyUI", "info")
			if _, err := c.remote(c.rootCtx, "POST", "comfyui/stop", nil, "", 0); err != nil {
				return err
			}
			if err := c.RefreshRemote(c.rootCtx, ""); err != nil {
				return err
			}
			c.log("远端 ComfyUI 引擎已停止", "ComfyUI", "info")
			return nil
		}, nil, nil)

	case "comfyui.free":
		c.log("请求释放 ComfyUI 远端显存", "ComfyUI", "info")
		res, err := c.remote(ctx, "POST", "comfyui/free", nil, "", 0)
		if err != nil {
			return nil, err
		}
		if err := c.RefreshRemote(ctx, ""); err != nil {
			return nil, err
		}
		c.log("已释放 ComfyUI 远端显存", "ComfyUI", "info")
		return res, nil

	case "comfyui.canvas.save", "comfyui.canvas.start":
		if rawMode, exists := payload["mode"]; exists && rawMode != nil {
			modeStr := asString(rawMode)
			if modeStr != "" && modeStr != "named" {
				return nil, errors.New("已不再支持临时域名，请配置固定域名和 Cloudflare Tunnel Token")
			}
		}
		c.vaultConfigMu.Lock()
		saved := map[string]any{}
		if v, ok := c.vault.Get("comfyui_canvas"); ok {
			if m := asMap(v); m != nil {
				saved = deepCopyMap(m)
			}
		}
		domain := asString(saved["domain"])
		if _, exists := payload["domain"]; exists {
			domain = asString(payload["domain"])
		}
		tunnelTok := asString(payload["tunnel_token"])
		if tunnelTok == "" {
			tunnelTok = asString(saved["tunnel_token"])
		}
		config, err := c.validateComfyCanvasConfig(map[string]any{
			"mode":         "named",
			"domain":       domain,
			"tunnel_token": tunnelTok,
		})
		if err != nil {
			c.vaultConfigMu.Unlock()
			return nil, errors.New("ComfyUI 画布配置无效：请填写公网 HTTPS 固定域名，并粘贴有效的 Cloudflare Tunnel 安装命令或 Token")
		}
		stored := deepCopyMap(saved)
		stored["mode"] = "named"
		stored["domain"] = asString(config["domain"])
		stored["tunnel_token"] = asString(config["tunnel_token"])
		if err := c.vault.Set("comfyui_canvas", stored); err != nil {
			c.vaultConfigMu.Unlock()
			return nil, err
		}
		c.vaultConfigMu.Unlock()
		if action == "comfyui.canvas.save" {
			c.log("已保存 ComfyUI 原生画布固定域名与隧道配置", "ComfyUI", "info")
			return map[string]any{"ok": true}, nil
		}
		cfgCopy := deepCopyMap(config)
		return c.asyncOperation("开启 ComfyUI 原生画布", func() error {
			c.log("正在启动 ComfyUI 原生画布代理与隧道", "ComfyUI", "info")
			if _, err := c.remote(c.rootCtx, "POST", "comfyui/canvas/start", cfgCopy, "", 0); err != nil {
				return err
			}
			c.log("远端已受理画布启动请求，等待公网连通检查", "ComfyUI", "info")
			return c.RefreshRemote(c.rootCtx, "")
		}, nil, nil)

	case "comfyui.canvas.stop":
		return c.asyncOperation("关闭 ComfyUI 原生画布", func() error {
			c.log("正在关闭 ComfyUI 原生画布隧道", "ComfyUI", "info")
			if _, err := c.remote(c.rootCtx, "POST", "comfyui/canvas/stop", nil, "", 0); err != nil {
				return err
			}
			if err := c.RefreshRemote(c.rootCtx, ""); err != nil {
				return err
			}
			c.log("ComfyUI 原生画布已关闭", "ComfyUI", "info")
			return nil
		}, nil, nil)

	case "comfyui.canvas.url":
		return c.remote(ctx, "GET", "comfyui/canvas/url", nil, "", 0)

	case "comfyui.canvas.tunnel_token":
		c.vaultConfigMu.Lock()
		canvas := map[string]any{}
		if v, ok := c.vault.Get("comfyui_canvas"); ok {
			if m := asMap(v); m != nil {
				canvas = m
			}
		}
		tok := asString(canvas["tunnel_token"])
		c.vaultConfigMu.Unlock()
		return map[string]any{"token": tok}, nil

	case "comfyui.workflow.import":
		if c.comfy == nil {
			return nil, errors.New("ComfyValidator 未配置")
		}
		req, err := c.comfy.ValidateWorkflowImport(payload)
		if err != nil {
			return nil, err
		}
		builtinIDs := c.builtinWorkflowIDSet()
		if reqID := asString(req["id"]); reqID != "" && builtinIDs[reqID] {
			return nil, errors.New("不能覆盖内置工作流模板")
		}
		c.mu.Lock()
		rtState := asString(asMap(c.data["runtime"])["state"])
		c.mu.Unlock()
		if rtState == "ready" {
			rawItem, err := c.remote(ctx, "POST", "comfyui/workflows", req, "", 0)
			if err != nil {
				return nil, err
			}
			itemMap := asMap(rawItem)
			storedItem := deepCopyMap(itemMap)
			if validated, err := c.validateWorkflowTemplate(itemMap); err == nil && validated != nil {
				storedItem = validated
			}
			if id := asString(storedItem["id"]); id != "" && !builtinIDs[id] {
				ver, _ := asFloat(storedItem["updated_at"])
				c.mu.Lock()
				c.customWorkflows[id] = deepCopyMap(storedItem)
				c.remoteWorkflowVersions[id] = ver
				c.mu.Unlock()
				if err := c.saveWorkflows(); err != nil {
					return nil, err
				}
			}
			if err := c.RefreshRemote(ctx, ""); err != nil {
				return nil, err
			}
			label := asString(storedItem["name"])
			if label == "" {
				label = asString(storedItem["id"])
			}
			c.log("已导入 ComfyUI 工作流："+label, "ComfyUI", "info")
			return storedItem, nil
		}
		now := nowUnixFloat()
		wfID := asString(req["id"])
		if wfID == "" {
			wfID = "wf-" + generateTokenHex(6)
		}
		if builtinIDs[wfID] {
			return nil, errors.New("不能覆盖内置工作流模板")
		}
		item, err := c.validateWorkflowTemplate(map[string]any{
			"id":          wfID,
			"name":        asString(req["name"]),
			"description": asString(req["description"]),
			"source":      "custom",
			"created_at":  now,
			"updated_at":  now,
			"workflow":    req["workflow"],
		})
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		itemID := asString(item["id"])
		c.customWorkflows[itemID] = deepCopyMap(item)
		delete(c.remoteWorkflowVersions, itemID)
		var baseWorkflows []map[string]any
		if asString(asMap(c.data["runtime"])["state"]) != "stopped" {
			baseWorkflows = asMapSlice(c.data["workflows"])
		}
		merged := c.mergeWorkflowsLocked(baseWorkflows)
		c.mu.Unlock()
		if err := c.saveWorkflows(); err != nil {
			return nil, err
		}
		c.Update(map[string]any{"workflows": merged})
		c.log("已离线保存 ComfyUI 工作流："+asString(item["name"]), "ComfyUI", "info")
		return item, nil

	case "comfyui.workflow.delete":
		wid := asString(payload["id"])
		if !reModelID.MatchString(wid) {
			return nil, errors.New("工作流 ID 不合法")
		}
		if c.builtinWorkflowIDSet()[wid] {
			return nil, errors.New("内置工作流模板不可删除")
		}
		c.mu.Lock()
		removedLocal, hadLocal := c.customWorkflows[wid]
		delete(c.customWorkflows, wid)
		prevVer, hadVer := c.remoteWorkflowVersions[wid]
		delete(c.remoteWorkflowVersions, wid)
		rtState := asString(asMap(c.data["runtime"])["state"])
		c.mu.Unlock()
		if err := c.saveWorkflows(); err != nil {
			return nil, err
		}
		if rtState == "ready" {
			if _, err := c.remote(ctx, "DELETE", "comfyui/workflows/"+wid, nil, "", 0); err != nil {
				if !(hadLocal && isNotFoundError(err)) {
					if hadLocal {
						c.mu.Lock()
						c.customWorkflows[wid] = removedLocal
						if hadVer {
							c.remoteWorkflowVersions[wid] = prevVer
						}
						c.mu.Unlock()
						_ = c.saveWorkflows()
					}
					return nil, err
				}
			}
			if err := c.RefreshRemote(ctx, ""); err != nil {
				return nil, err
			}
		}
		c.mu.Lock()
		var remaining []map[string]any
		if asString(asMap(c.data["runtime"])["state"]) != "stopped" {
			remaining = make([]map[string]any, 0)
			for _, w := range asMapSlice(c.data["workflows"]) {
				if asString(w["id"]) != wid {
					remaining = append(remaining, w)
				}
			}
		}
		merged := c.mergeWorkflowsLocked(remaining)
		c.mu.Unlock()
		c.Update(map[string]any{"workflows": merged})
		_ = c.clearWorkflowDraft(wid)
		c.log("已删除 ComfyUI 工作流："+wid, "ComfyUI", "info")
		return map[string]any{"ok": true}, nil

	case "comfyui.draft.get":
		return c.getWorkflowDraft(payload)

	case "comfyui.draft.save":
		return c.saveWorkflowDraft(payload)

	case "comfyui.draft.media.save":
		return c.saveWorkflowDraftMedia(payload)

	case "comfyui.draft.media.read":
		return c.readWorkflowDraftMedia(payload)

	case "comfyui.draft.media.delete":
		return c.deleteWorkflowDraftMedia(payload)

	case "comfyui.draft.clear":
		wfID, err := validateWorkflowDraftID(payload["workflow_id"])
		if err != nil {
			return nil, err
		}
		if err := c.clearWorkflowDraft(wfID); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil

	case "comfyui.input.upload":
		rawFname := asString(payload["filename"])
		rawData := asString(payload["data"])
		if uploadValidator, ok := c.comfy.(ComfyInputUploadValidator); ok {
			if err := uploadValidator.ValidateInputUpload(rawFname, rawData); err != nil {
				return nil, err
			}
		}
		fname := strings.TrimSpace(rawFname)
		display := fname
		if display == "" {
			display = "文件"
		}
		c.log("正在上传 ComfyUI 输入素材："+display, "ComfyUI", "info")
		res, err := c.remote(ctx, "POST", "comfyui/inputs", map[string]any{
			"filename": fname,
			"data":     rawData,
		}, "", 300*time.Second)
		if err != nil {
			return nil, err
		}
		c.log("ComfyUI 输入素材上传完成："+display, "ComfyUI", "info")
		return res, nil

	case "comfyui.run":
		if c.comfy == nil {
			return nil, errors.New("ComfyValidator 未配置")
		}
		reqCopy := deepCopyMap(payload)
		if reqCopy == nil {
			reqCopy = map[string]any{}
		}
		explicitTok := ""
		if rawTok, hasTok := reqCopy["hf_token"]; hasTok {
			if rawTok == nil {
				return nil, errors.New("字段 hf_token 不能为空值")
			}
			tokStr, ok := rawTok.(string)
			if !ok {
				return nil, errors.New("hf_token 格式不合法")
			}
			cleaned, err := validateCleanHFToken(tokStr)
			if err != nil {
				return nil, err
			}
			if cleaned == "" {
				delete(reqCopy, "hf_token")
			} else {
				explicitTok = cleaned
				reqCopy["hf_token"] = cleaned
			}
		}
		if explicitTok == "" {
			if _, hasTok := reqCopy["hf_token"]; !hasTok && c.hasRemoteCapability("comfyui_hf_token") {
				if v, ok := c.vault.Get("hf"); ok {
					if s, ok := v.(string); ok {
						cleaned, err := validateCleanHFToken(s)
						if err != nil {
							return nil, err
						}
						if cleaned != "" {
							reqCopy["hf_token"] = cleaned
						}
					}
				}
			}
		}
		req, err := c.comfy.ValidateWorkflowRun(reqCopy)
		if err != nil {
			return nil, err
		}
		if asBool(req["auto_release_after_sync"]) {
			if len(c.vaultSessions()) == 0 {
				return nil, errors.New("请先启动远端 GPU 实例")
			}
			c.mu.Lock()
			rt := deepCopyMap(asMap(c.data["runtime"]))
			c.mu.Unlock()
			if asString(rt["state"]) != "ready" {
				return nil, errors.New("请先启动远端 GPU 实例")
			}
			if !asBool(asMap(rt["drive"])["connected"]) {
				return nil, errors.New("当前实例尚未连接 Google 云盘，请先完成云盘授权与挂载")
			}
			if caps := asAnySlice(rt["capabilities"]); len(caps) > 0 && !c.hasRemoteCapability("comfyui_workflow_auto_release") {
				return nil, errors.New(colabOneWorkflowAutoRelUpgrHint)
			}
		} else {
			delete(req, "auto_release_after_sync")
		}
		if asBool(req["skip_auto_release"]) {
			if !c.hasRemoteCapability("comfyui_share_guard") {
				delete(req, "skip_auto_release")
			}
		} else {
			delete(req, "skip_auto_release")
		}
		wfID := asString(req["workflow_id"])
		if c.builtinTemplateVideoBackend(wfID) == "longcat_avatar_native" && c.readyRemoteLacksCapability("comfyui_longcat_native") {
			return nil, errors.New(colabOneLongCatNativeUpgradeHint)
		}
		if err := c.ensureRemoteWorkflow(ctx, wfID); err != nil {
			return nil, err
		}
		c.log("请求运行 ComfyUI 工作流："+wfID, "ComfyUI", "info")
		res, err := c.remote(ctx, "POST", "comfyui/run", req, "", 0)
		if err != nil {
			return nil, err
		}
		if err := c.RefreshRemote(ctx, ""); err != nil {
			c.log("远端已受理 ComfyUI 工作流任务，但刷新状态失败（将自动重试）："+errTypeName(err), "ComfyUI", "warning")
			return res, nil
		}
		c.log("远端已受理 ComfyUI 工作流任务", "ComfyUI", "info")
		return res, nil

	case "comfyui.auto_release":
		enabled, ok := isStrictBool(payload["enabled"])
		if !ok {
			return nil, errors.New("自动释放开关参数不合法")
		}
		if len(c.vaultSessions()) == 0 {
			return nil, errors.New("请先启动远端 GPU 实例")
		}
		c.mu.Lock()
		rt := deepCopyMap(asMap(c.data["runtime"]))
		c.mu.Unlock()
		if asString(rt["state"]) != "ready" {
			return nil, errors.New("请先启动远端 GPU 实例")
		}
		if enabled && !asBool(asMap(rt["drive"])["connected"]) {
			return nil, errors.New("当前实例尚未连接 Google 云盘，请先完成云盘授权与挂载")
		}
		if caps := asAnySlice(rt["capabilities"]); len(caps) > 0 && !c.hasRemoteCapability("comfyui_workflow_auto_release") {
			return nil, errors.New(colabOneWorkflowAutoRelUpgrHint)
		}
		res, err := c.remote(ctx, "POST", "comfyui/auto-release", map[string]any{"enabled": enabled}, "", 30*time.Second)
		if err != nil {
			if isMissingRemoteRoute404(err) {
				return nil, errors.New(colabOneWorkflowAutoRelUpgrHint)
			}
			errMsg := err.Error()
			if strings.Contains(errMsg, "Google 云盘连接已断开") ||
				strings.Contains(errMsg, "未检测到云盘凭据") ||
				strings.Contains(errMsg, "尚未连接 Google 云盘") {
				c.mu.Lock()
				nextRT := deepCopyMap(asMap(c.data["runtime"]))
				c.mu.Unlock()
				if nextRT != nil {
					driveMap := deepCopyMap(asMap(nextRT["drive"]))
					if driveMap == nil {
						driveMap = map[string]any{}
					}
					driveMap["connected"] = false
					driveMap["mode"] = "disabled"
					nextRT["drive"] = driveMap
					c.Update(map[string]any{"runtime": nextRT})
				}
			}
			return nil, err
		}
		if arMap := asMap(res); arMap != nil {
			c.mu.Lock()
			if nextRT := deepCopyMap(asMap(c.data["runtime"])); nextRT != nil {
				comfyMap := deepCopyMap(asMap(nextRT["comfyui"]))
				if comfyMap == nil {
					comfyMap = map[string]any{}
				}
				comfyMap["auto_release"] = deepCopyMap(arMap)
				nextRT["comfyui"] = comfyMap
				pendingLogs := c.updateLocked(map[string]any{"runtime": nextRT})
				c.mu.Unlock()
				c.flushActivityLogs(pendingLogs)
			} else {
				c.mu.Unlock()
			}
		}
		if enabled {
			c.log("已开启工作流完成后自动同步云盘并释放实例", "云盘", "info")
		} else {
			c.log("已关闭工作流完成后自动同步释放", "云盘", "info")
		}
		_ = c.RefreshRemote(ctx, "")
		return res, nil

	case "comfyui.outputs.drive.sync":
		return c.SyncDriveOutputs(ctx)

	case "comfyui.tts.preview":
		if c.remoteExplicitlyLacksCapability("comfyui_tts_preview") {
			return nil, errors.New(colabOneTtsPreviewUpgradeHint)
		}
		reqCopy := deepCopyMap(payload)
		if reqCopy == nil {
			reqCopy = map[string]any{}
		}
		skipLocalCache := asBool(reqCopy["skip_local_cache"])
		delete(reqCopy, "skip_local_cache")
		explicitTok := ""
		if rawTok, hasTok := reqCopy["hf_token"]; hasTok {
			if rawTok == nil {
				return nil, errors.New("字段 hf_token 不能为空值")
			}
			tokStr, ok := rawTok.(string)
			if !ok {
				return nil, errors.New("hf_token 格式不合法")
			}
			cleaned, err := validateCleanHFToken(tokStr)
			if err != nil {
				return nil, err
			}
			if cleaned == "" {
				delete(reqCopy, "hf_token")
			} else {
				explicitTok = cleaned
				reqCopy["hf_token"] = cleaned
			}
		}
		if explicitTok == "" {
			if _, hasTok := reqCopy["hf_token"]; !hasTok && (c.hasRemoteCapability("comfyui_hf_token") || c.hasRemoteCapability("comfyui_tts_preview")) {
				if v, ok := c.vault.Get("hf"); ok {
					if s, ok := v.(string); ok {
						cleaned, err := validateCleanHFToken(s)
						if err != nil {
							return nil, err
						}
						if cleaned != "" {
							reqCopy["hf_token"] = cleaned
						}
					}
				}
			}
		}
		var req map[string]any
		var err error
		if previewValidator, ok := c.comfy.(ComfyTtsPreviewValidator); ok {
			req, err = previewValidator.ValidateTtsPreviewRequest(reqCopy)
		} else {
			req, err = comfyui.ValidateTtsPreviewRequest(reqCopy)
		}
		if err != nil {
			return nil, err
		}
		engineID := asString(req["engine_id"])
		c.log("请求合成 TTS 音色试听："+engineID, "ComfyUI", "info")
		res, err := c.remote(ctx, "POST", "comfyui/tts-preview", req, "", 120*time.Second)
		if err != nil {
			if !c.hasRemoteCapability("comfyui_tts_preview") && isMissingRemoteRoute404(err) {
				return nil, errors.New(colabOneTtsPreviewUpgradeHint)
			}
			return nil, err
		}
		resMap := asMap(res)
		if asBool(resMap["cached"]) {
			if !skipLocalCache {
				if rawB64 := asString(resMap["data"]); rawB64 != "" {
					if decoded, decErr := base64.StdEncoding.DecodeString(rawB64); decErr == nil && len(decoded) > 0 {
						c.saveLocalTtsPreview(req, resMap, decoded)
					}
				} else {
					// Metadata-only cached hit (>512 KiB): remember req context so the subsequent
					// comfyui.tts.preview.asset fetch knows whether ref_audio/emotion_ref was used.
					c.rememberPendingTtsPreviewReq(asString(resMap["preview_id"]), req)
				}
			}
		} else {
			if !skipLocalCache {
				c.rememberPendingTtsPreviewReq(asString(resMap["preview_id"]), req)
			}
			if err := c.RefreshRemote(ctx, ""); err != nil {
				c.log("远端已受理 TTS 音色试听任务，但刷新状态失败（将自动重试）："+errTypeName(err), "ComfyUI", "warning")
				return res, nil
			}
			c.log("远端已受理 TTS 音色试听任务", "ComfyUI", "info")
		}
		return res, nil

	case "comfyui.tts.preview.cache.lookup":
		engineID := strings.TrimSpace(asString(payload["engine_id"]))
		if engineID == "" {
			return map[string]any{"items": []map[string]any{}}, nil
		}
		rawItems := asAnySlice(payload["items"])
		cleanItems := make([]map[string]any, 0, len(rawItems))
		for _, raw := range rawItems {
			if len(cleanItems) >= 24 {
				break
			}
			if m := asMap(raw); m != nil {
				cleanItems = append(cleanItems, m)
			}
		}
		return map[string]any{
			"engine_id": engineID,
			"items":     c.lookupLocalTtsPreviewBatch(engineID, cleanItems),
		}, nil

	case "comfyui.tts.preview.cache.read":
		return c.readLocalTtsPreview(payload)

	case "comfyui.tts.preview.prefetch":
		engineID := strings.TrimSpace(asString(payload["engine_id"]))
		if engineID == "" {
			return nil, errors.New("TTS 引擎 ID 不合法")
		}
		rawItems := asAnySlice(payload["items"])
		if len(rawItems) == 0 {
			return map[string]any{
				"active":      false,
				"state":       "idle",
				"engine_id":   engineID,
				"total":       0,
				"completed":   0,
				"failed":      0,
				"ready_items": []map[string]any{},
			}, nil
		}
		cleanItems := make([]map[string]any, 0, len(rawItems))
		for _, raw := range rawItems {
			if len(cleanItems) >= 24 {
				break
			}
			m := asMap(raw)
			if m == nil {
				continue
			}
			vp := strings.TrimSpace(asString(m["voice_preset"]))
			if vp == "" || strings.EqualFold(vp, "custom") {
				continue
			}
			cleanItems = append(cleanItems, map[string]any{
				"voice_preset":   vp,
				"preview_text":   strings.TrimSpace(asString(m["preview_text"])),
				"voice_prompt":   strings.TrimSpace(asString(m["voice_prompt"])),
				"emotion_prompt": strings.TrimSpace(asString(m["emotion_prompt"])),
			})
		}
		if len(cleanItems) == 0 {
			return map[string]any{
				"active":      false,
				"state":       "idle",
				"engine_id":   engineID,
				"total":       0,
				"completed":   0,
				"failed":      0,
				"ready_items": []map[string]any{},
			}, nil
		}

		localStatuses := c.lookupLocalTtsPreviewBatch(engineID, cleanItems)
		localReady := make([]map[string]any, 0)
		remotePending := make([]map[string]any, 0, len(cleanItems))
		for idx, st := range localStatuses {
			if asBool(st["cached"]) {
				localReady = append(localReady, map[string]any{
					"voice_preset": asString(st["voice_preset"]),
					"preview_id":   asString(st["preview_id"]),
					"local_cached": true,
				})
			} else if idx < len(cleanItems) {
				remotePending = append(remotePending, cleanItems[idx])
			}
		}
		if len(remotePending) == 0 {
			return map[string]any{
				"active":      false,
				"state":       "completed",
				"engine_id":   engineID,
				"total":       len(cleanItems),
				"completed":   len(localReady),
				"failed":      0,
				"ready_items": localReady,
			}, nil
		}
		if c.remoteExplicitlyLacksCapability("comfyui_tts_preview") {
			return map[string]any{
				"active":      false,
				"state":       "unsupported",
				"engine_id":   engineID,
				"total":       len(cleanItems),
				"completed":   len(localReady),
				"failed":      0,
				"ready_items": localReady,
			}, nil
		}

		reqBody := map[string]any{
			"engine_id": engineID,
			"items":     remotePending,
		}
		if c.hasRemoteCapability("comfyui_hf_token") || c.hasRemoteCapability("comfyui_tts_preview") {
			if v, ok := c.vault.Get("hf"); ok {
				if s, ok := v.(string); ok {
					if cleaned, tokErr := validateCleanHFToken(s); tokErr == nil && cleaned != "" {
						reqBody["hf_token"] = cleaned
					}
				}
			}
		}
		c.rememberPendingTtsPrefetchContext(engineID, remotePending)
		res, err := c.remote(ctx, "POST", "comfyui/tts-preview/prefetch", reqBody, "", 30*time.Second)
		if err != nil {
			if isMissingRemoteRoute404(err) {
				return map[string]any{
					"active":      false,
					"state":       "unsupported",
					"engine_id":   engineID,
					"total":       len(cleanItems),
					"completed":   len(localReady),
					"failed":      0,
					"ready_items": localReady,
				}, nil
			}
			return nil, err
		}
		_ = c.RefreshRemote(ctx, "")
		return res, nil

	case "comfyui.tts.preview.prefetch.cancel":
		res, err := c.remote(ctx, "POST", "comfyui/tts-preview/prefetch/cancel", nil, "", 15*time.Second)
		if err != nil {
			if isMissingRemoteRoute404(err) {
				return map[string]any{"active": false, "state": "cancelled"}, nil
			}
			return map[string]any{"active": false, "state": "cancelled"}, nil
		}
		_ = c.RefreshRemote(ctx, "")
		return res, nil

	case "comfyui.tts.preview.asset":
		previewID := asString(payload["preview_id"])
		if previewID == "" {
			previewID = asString(payload["id"])
		}
		if !reHex32ID.MatchString(previewID) {
			return nil, errors.New("试听音频编号不合法")
		}
		if c.remoteExplicitlyLacksCapability("comfyui_tts_preview") {
			return nil, errors.New(colabOneTtsPreviewUpgradeHint)
		}
		var buf bytes.Buffer
		var resultMeta map[string]any
		expectedSize := 0
		expectedSHA := ""
		offset := 0
		completed := false
		for iter := 0; iter < colabOneMaxTtsPreviewChunks; iter++ {
			reqOffset := offset
			chunkPath := fmt.Sprintf("comfyui/tts-previews/%s?offset=%d&length=%d", previewID, reqOffset, colabOneTtsPreviewChunkBytes)
			rawChunk, err := c.remote(ctx, "GET", chunkPath, nil, "", 60*time.Second)
			if err != nil {
				if iter == 0 && !c.hasRemoteCapability("comfyui_tts_preview") && isMissingRemoteRoute404(err) {
					return nil, errors.New(colabOneTtsPreviewUpgradeHint)
				}
				return nil, err
			}
			chunkMap := asMap(rawChunk)
			if chunkMap == nil {
				return nil, errors.New("试听音频响应格式不合法")
			}
			if resultMeta == nil {
				resultMeta = deepCopyMap(chunkMap)
				if rawSize, hasSize := chunkMap["size"]; hasSize {
					if totalSize, ok := asInt(rawSize); ok {
						if totalSize < 0 {
							return nil, errors.New("试听音频传输不完整，请重试")
						}
						if totalSize > colabOneMaxTtsPreviewBytes {
							return nil, errors.New("试听音频体积超过 16 MiB 上限")
						}
						expectedSize = totalSize
					}
				}
				if rawSHA, hasSHA := chunkMap["sha256"]; hasSHA {
					expectedSHA = strings.ToLower(strings.TrimSpace(asString(rawSHA)))
				}
			} else {
				if rawSize, hasSize := chunkMap["size"]; hasSize && expectedSize > 0 {
					if sz, ok := asInt(rawSize); !ok || sz != expectedSize {
						return nil, errors.New("试听音频缓存已变更，请重试")
					}
				}
				if rawSHA, hasSHA := chunkMap["sha256"]; hasSHA && expectedSHA != "" {
					if strings.ToLower(strings.TrimSpace(asString(rawSHA))) != expectedSHA {
						return nil, errors.New("试听音频缓存已变更，请重试")
					}
				}
			}
			if rawOff, hasOff := chunkMap["offset"]; hasOff {
				if gotOff, ok := asInt(rawOff); !ok || gotOff != reqOffset {
					return nil, errors.New("试听音频传输不完整，请重试")
				}
			}
			rawB64 := asString(chunkMap["data"])
			var decoded []byte
			if rawB64 != "" {
				var decErr error
				decoded, decErr = base64.StdEncoding.DecodeString(rawB64)
				if decErr != nil {
					return nil, errors.New("试听音频数据解码失败")
				}
			}
			chunkSize := len(decoded)
			if rawCS, hasCS := chunkMap["chunk_size"]; hasCS {
				cs, ok := asInt(rawCS)
				if !ok || cs != len(decoded) {
					return nil, errors.New("试听音频传输不完整，请重试")
				}
				chunkSize = cs
			}
			if buf.Len()+len(decoded) > colabOneMaxTtsPreviewBytes {
				return nil, errors.New("试听音频体积超过 16 MiB 上限")
			}
			if len(decoded) > 0 {
				buf.Write(decoded)
				offset += len(decoded)
			}
			hasMore := asBool(chunkMap["has_more"])
			if !hasMore || (expectedSize > 0 && buf.Len() >= expectedSize) {
				completed = true
				break
			}
			if chunkSize <= 0 || len(decoded) == 0 {
				if buf.Len() == 0 {
					return nil, errors.New("试听音频数据为空")
				}
				return nil, errors.New("试听音频传输不完整，请重试")
			}
		}
		if !completed {
			return nil, errors.New("试听音频分片读取次数超出上限")
		}
		if buf.Len() == 0 {
			return nil, errors.New("试听音频数据为空")
		}
		if expectedSize > 0 && buf.Len() != expectedSize {
			return nil, errors.New("试听音频传输不完整，请重试")
		}
		if expectedSHA != "" {
			sum := sha256.Sum256(buf.Bytes())
			actualSHA := hex.EncodeToString(sum[:])
			if actualSHA != expectedSHA {
				return nil, errors.New("试听音频数据校验失败，请重试")
			}
		}
		out := make(map[string]any, len(resultMeta)+1)
		for _, k := range []string{
			"preview_id",
			"cached",
			"engine_id",
			"voice_preset",
			"text",
			"sample_rate",
			"duration_seconds",
			"mime",
			"size",
			"sha256",
		} {
			if val, ok := resultMeta[k]; ok {
				out[k] = val
			}
		}
		for k, val := range resultMeta {
			if k == "data" || k == "offset" || k == "chunk_size" || k == "has_more" {
				continue
			}
			if _, exists := out[k]; !exists {
				out[k] = val
			}
		}
		if !asBool(payload["skip_local_cache"]) {
			pendingReq := c.consumePendingTtsPreviewReq(previewID)
			c.saveLocalTtsPreview(pendingReq, out, buf.Bytes())
		}
		out["data"] = base64.StdEncoding.EncodeToString(buf.Bytes())
		out["size"] = buf.Len()
		return out, nil

	case "comfyui.output.asset", "comfyui.output.delete", "comfyui.output.export_drive":
		identity := asString(payload["id"])
		if !reHex32ID.MatchString(identity) {
			return nil, errors.New("产物编号不合法")
		}
		if action == "comfyui.output.export_drive" {
			if len(c.vaultSessions()) == 0 {
				return nil, errors.New("请先启动远端 GPU 实例")
			}
			c.mu.Lock()
			rt := deepCopyMap(asMap(c.data["runtime"]))
			c.mu.Unlock()
			if asString(rt["state"]) != "ready" {
				return nil, errors.New("请先启动远端 GPU 实例")
			}
			if !asBool(asMap(rt["drive"])["connected"]) {
				return nil, errors.New("当前实例尚未连接 Google 云盘，请先完成云盘授权与挂载")
			}
			if caps := asAnySlice(rt["capabilities"]); len(caps) > 0 && !c.hasRemoteCapability("comfyui_drive_export") {
				return nil, errors.New("当前远端服务版本暂不支持作品直传 Google 云盘，请先点击顶部「立即热更新」升级远端服务后再试")
			}
			c.log("正在将 ComfyUI 作品直传导出至 Google 云盘 (ColabOne_Drive/outputs)", "云盘", "info")
			res, err := c.remoteLong(ctx, "POST", "comfyui/outputs/"+identity+"/export-drive", nil, 240*time.Second)
			if err != nil {
				if isNotFoundError(err) && !strings.Contains(err.Error(), "资源不存在") {
					return nil, errors.New("当前远端服务版本暂不支持作品直传 Google 云盘，请先点击顶部「立即热更新」升级远端服务后再试")
				}
				errMsg := err.Error()
				if strings.Contains(errMsg, "Google 云盘连接已断开") ||
					strings.Contains(errMsg, "未检测到云盘凭据") ||
					strings.Contains(errMsg, "尚未连接 Google 云盘") {
					c.mu.Lock()
					nextRT := deepCopyMap(asMap(c.data["runtime"]))
					c.mu.Unlock()
					if nextRT != nil {
						driveMap := deepCopyMap(asMap(nextRT["drive"]))
						if driveMap == nil {
							driveMap = map[string]any{}
						}
						driveMap["connected"] = false
						driveMap["mode"] = "disabled"
						nextRT["drive"] = driveMap
						c.Update(map[string]any{"runtime": nextRT})
					}
				}
				return nil, err
			}
			resMap := asMap(res)
			c.recordExportedDriveOutput(identity, resMap)
			if sanitizeDriveFileID(asString(resMap["drive_file_id"])) == "" {
				c.triggerBackgroundDriveOutputsSync()
			}
			remoteFolder := asString(resMap["remote_folder"])
			if remoteFolder == "" {
				remoteFolder = "ColabOne_Drive/outputs"
			}
			exportedName := asString(resMap["filename"])
			if exportedName != "" {
				c.log(fmt.Sprintf("已将作品导出至 Google 云盘：My Drive / %s / %s", remoteFolder, exportedName), "云盘", "info")
			} else {
				c.log("已将作品导出至 Google 云盘 ("+remoteFolder+")", "云盘", "info")
			}
			return res, nil
		}
		c.mu.Lock()
		gpuReady := asString(asMap(c.data["runtime"])["state"]) == "ready"
		c.mu.Unlock()
		runtimeOnly := asBool(payload["runtime_only"])
		if action == "comfyui.output.delete" {
			if runtimeOnly {
				if !gpuReady {
					return nil, errors.New("当前算力服务暂未就绪，暂不可访问该作品文件")
				}
				c.log("正在删除远端 ComfyUI 作品文件", "ComfyUI", "info")
				res, err := c.remote(ctx, "DELETE", "comfyui/outputs/"+identity, nil, "", 0)
				if err != nil {
					return nil, err
				}
				if !c.hasExportedDriveOutput(identity) {
					c.removeDriveOutputRecord(identity)
				}
				if err := c.RefreshRemote(ctx, ""); err != nil {
					return nil, err
				}
				c.log("已删除远端 ComfyUI 作品文件", "ComfyUI", "info")
				return res, nil
			}
			if gpuReady && (c.hasLiveRemoteOutput(identity) || !c.hasDriveOutputRecord(identity)) {
				c.log("正在删除远端 ComfyUI 作品文件", "ComfyUI", "info")
				res, err := c.remote(ctx, "DELETE", "comfyui/outputs/"+identity, nil, "", 0)
				if err != nil {
					if isNotFoundError(err) && c.hasDriveOutputRecord(identity) {
						return c.deleteDriveOutputOffline(ctx, identity)
					}
					return nil, err
				}
				c.removeDriveOutputRecord(identity)
				if err := c.RefreshRemote(ctx, ""); err != nil {
					return nil, err
				}
				c.log("已删除远端 ComfyUI 作品文件", "ComfyUI", "info")
				return res, nil
			}
			c.log("正在删除 Google 云盘作品记录", "云盘", "info")
			res, err := c.deleteDriveOutputOffline(ctx, identity)
			if err != nil {
				return nil, err
			}
			c.log("已删除 Google 云盘作品记录", "云盘", "info")
			return res, nil
		}
		offset := 0
		if rawOffset, exists := payload["offset"]; exists {
			if _, isBool := isStrictBool(rawOffset); isBool {
				return nil, errors.New("读取范围不合法")
			}
			parsed, ok := asInt(rawOffset)
			if !ok {
				return nil, errors.New("读取范围不合法")
			}
			offset = parsed
		}
		if offset < 0 || offset > colabOneMaxOutputAssetOffset {
			return nil, errors.New("读取范围不合法")
		}
		if runtimeOnly {
			if !gpuReady {
				return nil, errors.New("当前算力服务暂未就绪，暂不可访问该作品文件")
			}
			return c.remote(ctx, "GET", fmt.Sprintf("comfyui/outputs/%s/asset?offset=%d", identity, offset), nil, "", 0)
		}
		if gpuReady && (c.hasLiveRemoteOutput(identity) || !c.hasDriveOutputRecord(identity)) {
			res, err := c.remote(ctx, "GET", fmt.Sprintf("comfyui/outputs/%s/asset?offset=%d", identity, offset), nil, "", 0)
			if err != nil && isNotFoundError(err) && c.hasDriveOutputRecord(identity) {
				return c.readDriveOutputAssetChunk(ctx, identity, offset)
			}
			return res, err
		}
		return c.readDriveOutputAssetChunk(ctx, identity, offset)

	case "comfyui.live.status":
		return c.handleLiveStatus(ctx)

	case "comfyui.live.vram_check":
		return c.handleLiveVRAMCheck(ctx, payload)

	case "comfyui.live.start":
		return c.handleLiveStart(ctx, payload)

	case "comfyui.live.stop":
		return c.handleLiveStop(ctx)

	case "comfyui.live.session_token":
		return c.handleLiveSessionToken(ctx)

	case "comfyui.live.llm.get":
		return c.handleLiveLLMGet()

	case "comfyui.live.llm.save":
		return c.handleLiveLLMSave(payload)

	case "comfyui.live.llm.clear":
		return c.handleLiveLLMClear()

	case "comfyui.live.llm.test":
		return c.handleLiveLLMTest(ctx, payload)

	case "comfyui.live.avatars.list":
		return c.handleLiveAvatarsList(ctx)

	case "comfyui.live.avatars.save":
		return c.handleLiveAvatarsSave(ctx, payload)

	case "comfyui.live.avatars.delete":
		return c.handleLiveAvatarsDelete(ctx, payload)

	case "comfyui.live.avatars.clip.upload":
		return c.handleLiveAvatarClipUpload(ctx, payload)

	case "comfyui.live.avatars.build_pack":
		return c.handleLiveAvatarBuildPack(ctx, payload)

	case "comfyui.live.driving.list":
		return c.handleLiveDrivingList(ctx)

	case "comfyui.live.driving.upload":
		return c.handleLiveDrivingUpload(ctx, payload)

	case "comfyui.live.driving.delete":
		return c.handleLiveDrivingDelete(ctx, payload)

	case "runtime.upgrade":
		forceUpgrade := asBool(payload["force"])
		restoreAfter := asBoolDefault(payload, "restore", true)
		return c.asyncOperation("更新远端管理服务", func() error {
			return c.UpgradeService(c.rootCtx, forceUpgrade, restoreAfter)
		}, nil, nil)

	case "runtime.activity":
		// Queue rather than blocking the UI behind a kernel RPC.
		c.mu.Lock()
		isReady := asString(asMap(c.data["runtime"])["state"]) == "ready"
		if isReady {
			c.pendingTouch = true
		}
		c.mu.Unlock()
		if isReady {
			c.spawn(func() {
				c.SendActivity(c.rootCtx)
			})
		}
		return map[string]any{"ok": true}, nil

	case "hub.search":
		if c.hub == nil {
			return nil, errors.New("HubClient 未配置")
		}
		query := asString(payload["query"])
		sortBy := asString(payload["sort"])
		if sortBy == "" {
			sortBy = "downloads"
		}
		cursor := asString(payload["cursor"])
		modality := asString(payload["modality"])
		if modality == "" {
			modality = "all"
		}
		family := asString(payload["family"])
		hfTok := ""
		if v, ok := c.vault.Get("hf"); ok {
			if s, ok := v.(string); ok {
				hfTok = s
			}
		}
		return c.hub.SearchModels(ctx, query, sortBy, cursor, hfTok, modality, family)

	case "hub.files":
		if c.hub == nil {
			return nil, errors.New("HubClient 未配置")
		}
		repo, _, _ := c.parseHFInput(asString(payload["repo"]), "", "main")
		hfTok := ""
		if v, ok := c.vault.Get("hf"); ok {
			if s, ok := v.(string); ok {
				hfTok = s
			}
		}
		return c.hub.ModelFiles(ctx, repo, hfTok)

	case "hub.install":
		wantDownload := asBoolDefault(payload, "download", true)
		wantLoad := asBool(payload["load"])
		payloadCopy := deepCopyMap(payload)
		opTitle := "加入我的模型"
		if wantDownload && wantLoad {
			opTitle = "添加模型并下载装载到远端显存"
		} else if wantDownload {
			opTitle = "添加社区模型并下载到远端"
		}
		return c.asyncOperation(opTitle, func() error {
			spec, err := c.ImportModel(c.rootCtx, payloadCopy)
			if err != nil {
				return err
			}
			if wantDownload {
				specID := asString(spec["id"])
				specName := asString(spec["name"])
				if err := c.ensureRemoteModel(c.rootCtx, specID, ""); err != nil {
					return err
				}
				c.mu.Lock()
				threads, _ := asInt(c.settings["download_threads"])
				c.mu.Unlock()
				hfTok := ""
				if v, ok := c.vault.Get("hf"); ok {
					if s, ok := v.(string); ok {
						hfTok = s
					}
				}
				dlBody := map[string]any{
					"hf_token": hfTok,
					"threads":  threads,
				}
				if wantLoad {
					dlBody["load"] = true
					for _, k := range []string{"context_length", "temperature"} {
						if val, exists := payloadCopy[k]; exists {
							dlBody[k] = val
						}
					}
				}
				autoLoadSuffix := ""
				if wantLoad {
					autoLoadSuffix = "，完成后自动装载"
				}
				c.log(fmt.Sprintf("正在提交远端下载任务：%s（%d 线程%s）", specName, threads, autoLoadSuffix), "模型", "info")
				if _, err := c.remote(c.rootCtx, "POST", "models/"+specID+"/download", dlBody, "", 0); err != nil {
					return err
				}
				if err := c.RefreshRemote(c.rootCtx, ""); err != nil {
					return err
				}
				c.log("远端已启动模型下载："+specName, "模型", "info")
			}
			return nil
		}, nil, nil)

	case "model.common":
		var common *bool
		if v, exists := payload["common"]; exists && v != nil {
			b := asBool(v)
			common = &b
		}
		return c.SetCommonModel(asString(payload["id"]), common)

	case "model.remove_local":
		return c.RemoveLocalModel(asString(payload["id"]))

	case "common_repo.save":
		return c.SaveCommonRepo(payload)

	case "common_repo.remove":
		return c.RemoveCommonRepo(asString(payload["repo"]))

	case "desktop.shortcuts":
		if c.shortcuts == nil {
			return nil, errors.New("ShortcutManager 未配置")
		}
		target := asString(payload["target"])
		if target == "" {
			target = "start_menu"
		}
		remove := asBool(payload["remove"])
		locale := "zh-CN"
		if c.auth != nil && c.auth.Locale() != "" {
			locale = c.auth.Locale()
		}
		status, err := c.shortcuts.Manage(c.root, target, remove, c.shortcutSandbox, locale)
		if err != nil {
			return nil, err
		}
		label := "开始菜单"
		if target == "both" {
			label = "开始菜单与桌面"
		} else if target == "desktop" {
			label = "桌面"
		}
		if remove {
			c.log("已移除"+label+"快捷方式", "客户端", "info")
		} else {
			c.log("已更新"+label+"快捷方式", "客户端", "info")
		}
		out := map[string]any{"ok": true}
		for k, v := range status {
			out[k] = v
		}
		return out, nil

	case "logs.clear":
		c.activity.Clear()
		return map[string]any{"ok": true}, nil

	case "runtime.release.preview":
		return c.PreviewReleaseAll(ctx)

	case "runtime.release.all":
		planID := asString(payload["plan_id"])
		force := asBool(payload["force"])
		rawEndpoints, exists := payload["endpoints"]
		hasEndpoints := exists && rawEndpoints != nil
		var endpoints []string
		if hasEndpoints {
			slice := asAnySlice(rawEndpoints)
			if slice == nil {
				endpoints = []string{}
			} else {
				endpoints = make([]string, 0, len(slice))
				for _, item := range slice {
					endpoints = append(endpoints, asString(item))
				}
			}
		}
		title := "停止账号下全部已确认实例"
		if hasEndpoints {
			title = "停止所选远端实例"
		}
		return c.asyncOperation(title, func() error {
			return c.ReleaseAll(c.rootCtx, planID, force, endpoints, hasEndpoints)
		}, nil, nil)

	case "settings.save":
		return c.SaveSettings(ctx, payload)

	case "settings.hf_token":
		raw := ""
		if v, ok := c.vault.Get("hf"); ok {
			if s, ok := v.(string); ok {
				raw = s
			}
		}
		return map[string]any{"token": raw}, nil

	case "settings.assign_proxy":
		raw := ""
		if v, ok := c.vault.Get("assign_proxy"); ok {
			if s, ok := v.(string); ok {
				raw = s
			}
		}
		return map[string]any{"url": raw}, nil

	case "settings.assign_proxy.test":
		if c.assignProxy == nil {
			return nil, errors.New("分配代理组件未配置")
		}
		raw := asTrimmedString(payload["assign_proxy"])
		if raw == "" {
			if savedVal, ok := c.vault.Get("assign_proxy"); ok {
				if s, ok := savedVal.(string); ok {
					raw = s
				}
			}
		}
		if strings.TrimSpace(raw) == "" {
			return nil, errors.New("请先填写或保存分配代理地址")
		}
		res, err := c.assignProxy.TestAssignProxy(ctx, raw)
		if err != nil {
			return nil, err
		}
		c.log(
			fmt.Sprintf("分配代理连通性测试通过（%s · %v ms）", asString(res["proxy"]), res["latency_ms"]),
			"设置",
			"info",
		)
		return res, nil

	case "auth.colab.begin":
		if c.auth == nil {
			return nil, errors.New("AuthClient 未配置")
		}
		if c.opBusy.Load() && !c.auth.LoopbackActive() {
			return nil, errors.New("请等待当前操作完成")
		}
		result, err := c.auth.ColabBegin(ctx)
		if err != nil {
			return nil, err
		}
		c.Update(map[string]any{"auth_url": asString(result["url"])})
		c.log("已生成 Google 授权链接，等待完成登录", "登录", "info")
		return result, nil

	case "auth.colab.auto":
		if c.auth == nil {
			return nil, errors.New("AuthClient 未配置")
		}
		return c.asyncOperation("等待 Google 登录", func() error {
			c.log("已启动本地回调服务，等待浏览器完成 Google 授权", "登录", "info")
			err := c.auth.ColabLogin(c.rootCtx, func(u string) {
				c.Update(map[string]any{"auth_url": u})
			})
			c.Update(map[string]any{"auth_url": ""})
			if err != nil {
				return err
			}
			c.resetAccountStateForSwitch(nil, false)
			c.log("Google 登录已保存到本机，后续自动续期", "登录", "info")
			c.triggerBackgroundDriveOutputsSync()
			return nil
		}, nil, nil)

	case "auth.colab.finish":
		if c.auth == nil {
			return nil, errors.New("AuthClient 未配置")
		}
		code := asString(payload["code"])
		if c.auth.LoopbackActive() {
			// A paste can finish a pending loopback login; its operation then completes
			// the login once the callback wait exits.
			c.log("收到手动粘贴的 Google 授权内容，正在校验", "登录", "info")
			if err := c.auth.ColabFinish(ctx, code); err != nil {
				return nil, err
			}
			c.resetAccountStateForSwitch(nil, false)
			c.triggerBackgroundDriveOutputsSync()
			return map[string]any{"ok": true}, nil
		}
		return c.asyncOperation("完成 Google 登录", func() error {
			c.log("正在校验 Google 授权码并换取访问凭据", "登录", "info")
			if err := c.auth.ColabFinish(c.rootCtx, code); err != nil {
				return err
			}
			c.resetAccountStateForSwitch(nil, false)
			c.log("Google 登录已保存到本机，后续自动续期", "登录", "info")
			c.triggerBackgroundDriveOutputsSync()
			return nil
		}, nil, nil)

	case "drive.connect":
		return c.asyncOperation("挂载 Google 云盘 (ColabOne_Drive)", func() error {
			_, err := c.ConnectDrive(c.rootCtx)
			return err
		}, map[string]any{"kind": "drive_connect", "stage": "checking"}, nil)

	case "drive.setup_headless":
		return c.asyncOperation("检测并开启云盘免弹窗模式", func() error {
			_, err := c.SetupHeadlessDrive(c.rootCtx)
			return err
		}, nil, nil)

	case "drive.confirm":
		c.driveConfirm.Set()
		c.log("已收到完成确认，正在立即核验 Google 云盘授权状态", "云盘", "info")
		return map[string]any{"ok": true}, nil

	case "drive.probe":
		c.driveProbe.Set()
		return map[string]any{"ok": true}, nil

	case "drive.cancel":
		c.driveCancel.Set()
		c.mu.Lock()
		patch := map[string]any{"auth_url": "", "drive_auth_hint": ""}
		if op := asMap(c.data["operation"]); op != nil && asString(op["kind"]) == "drive_connect" {
			nextOp := deepCopyMap(op)
			nextOp["cancelling"] = true
			patch["operation"] = nextOp
		}
		pendingLogs := c.updateLocked(patch)
		c.mu.Unlock()
		c.flushActivityLogs(pendingLogs)
		c.log("已请求取消 Google 云盘挂载", "云盘", "info")
		return map[string]any{"ok": true}, nil

	case "drive.sync", "sync":
		return c.asyncOperation("同步 Google 云盘 (ColabOne_Drive)", func() error {
			_, err := c.SyncDrive(c.rootCtx)
			return err
		}, nil, nil)

	case "runtime.start":
		createNew := asBool(payload["new"])
		gpu := asString(payload["gpu"])
		targetGPU := gpu
		if targetGPU == "" {
			c.mu.Lock()
			targetGPU = asString(c.settings["gpu"])
			c.mu.Unlock()
		}
		title := "启动远端 GPU"
		if createNew {
			title = "新增启动远端 GPU"
		}
		return c.asyncOperation(
			title,
			func() error { return c.Start(c.rootCtx, createNew, gpu) },
			map[string]any{
				"kind":          "runtime_start",
				"cancellable":   true,
				"cancelling":    false,
				"waiting_retry": false,
				"retry_at":      0.0,
				"allocated":     false,
				"gpu":           targetGPU,
			},
			func() error {
				c.startCancel.Clear()
				return nil
			},
		)

	case "runtime.start.cancel":
		c.mu.Lock()
		op := asMap(c.data["operation"])
		if op == nil || asString(op["kind"]) != "runtime_start" || !asBool(op["cancellable"]) {
			c.mu.Unlock()
			return map[string]any{"ok": true, "cancelled": false}, nil
		}
		nextOp := deepCopyMap(op)
		cancelTitle := "正在取消 GPU 分配"
		if asBool(op["allocated"]) {
			cancelTitle = "正在取消启动并释放实例"
		}
		nextOp["title"] = cancelTitle
		nextOp["cancellable"] = false
		nextOp["cancelling"] = true
		nextOp["waiting_retry"] = false
		nextOp["retry_at"] = 0.0
		c.startCancel.Set()
		pendingLogs := c.updateLocked(map[string]any{"operation": nextOp})
		c.mu.Unlock()
		c.flushActivityLogs(pendingLogs)
		c.log("已请求取消 GPU 分配", "GPU", "info")
		return map[string]any{"ok": true, "cancelled": true}, nil

	case "runtime.release":
		targetEP := asString(payload["endpoint"])
		force := asBool(payload["force"])
		title := "释放远端 GPU"
		if targetEP != "" {
			title = "释放远端实例 " + targetEP
		}
		return c.asyncOperation(title, func() error {
			return c.Release(c.rootCtx, force, targetEP)
		}, nil, nil)

	case "model.import":
		payloadCopy := deepCopyMap(payload)
		return c.asyncOperation("读取 Hugging Face 模型清单", func() error {
			_, err := c.ImportModel(c.rootCtx, payloadCopy)
			return err
		}, nil, nil)

	case "model.set_api_id":
		return c.SetModelAPIID(ctx, asString(payload["id"]), asString(payload["api_id"]))

	case "model.download":
		mid, err := ValidateModelID(payload)
		if err != nil {
			return nil, err
		}
		label := c.modelLabel(mid)
		c.mu.Lock()
		threads, _ := asInt(c.settings["download_threads"])
		c.mu.Unlock()
		wantLoad := asBool(payload["load"])
		suffix := ""
		if wantLoad {
			suffix = "，完成后自动装载"
		}
		c.log(fmt.Sprintf("请求下载模型 %s 到远端 GPU（%d 线程%s）", label, threads, suffix), "模型", "info")
		if err := c.ensureRemoteModel(ctx, mid, ""); err != nil {
			return nil, err
		}
		hfTok := ""
		if v, ok := c.vault.Get("hf"); ok {
			if s, ok := v.(string); ok {
				hfTok = s
			}
		}
		dlBody := map[string]any{
			"hf_token": hfTok,
			"threads":  threads,
		}
		if wantLoad {
			dlBody["load"] = true
			for _, k := range []string{"context_length", "temperature"} {
				if val, exists := payload[k]; exists {
					dlBody[k] = val
				}
			}
		}
		res, err := c.remote(ctx, "POST", "models/"+mid+"/download", dlBody, "", 0)
		if err != nil {
			return nil, err
		}
		c.log("远端下载任务已启动："+label, "模型", "info")
		return res, nil

	case "model.load":
		mid, err := ValidateModelID(payload)
		if err != nil {
			return nil, err
		}
		label := c.modelLabel(mid)
		c.log("请求装载模型 "+label+" 到远端显存", "模型", "info")
		if err := c.ensureRemoteModel(ctx, mid, ""); err != nil {
			return nil, err
		}
		options := make(map[string]any)
		for _, k := range []string{"context_length", "temperature"} {
			if val, exists := payload[k]; exists {
				options[k] = val
			}
		}
		res, err := c.remote(ctx, "POST", "models/"+mid+"/load", options, "", 0)
		if err != nil {
			return nil, err
		}
		c.log("远端模型装载任务已启动："+label, "模型", "info")
		return res, nil

	case "model.unload":
		c.log("请求卸载当前远端模型并释放显存", "模型", "info")
		res, err := c.remote(ctx, "POST", "unload", nil, "", 0)
		if err != nil {
			return nil, err
		}
		c.log("已卸载远端模型并释放显存", "模型", "info")
		return res, nil

	case "model.delete":
		mid, err := ValidateModelID(payload)
		if err != nil {
			return nil, err
		}
		label := c.modelLabel(mid)
		c.log("请求删除远端模型文件："+label, "模型", "info")
		res, err := c.remote(ctx, "DELETE", "models/"+mid, nil, "", 0)
		if err != nil {
			return nil, err
		}
		c.log("已删除远端模型文件："+label, "模型", "info")
		return res, nil

	case "task.cancel":
		tid := asString(payload["id"])
		if !reHex32ID.MatchString(tid) {
			return nil, errors.New("任务 ID 不合法")
		}
		c.log("请求取消当前远端任务", "远端", "info")
		res, err := c.remote(ctx, "DELETE", "tasks/"+tid, nil, "", 0)
		if err != nil {
			return nil, err
		}
		c.log("已向远端发送任务取消指令", "远端", "info")
		return res, nil

	case "error.clear":
		expected := asTrimmedString(payload["expected"])
		c.mu.Lock()
		current := asTrimmedString(c.data["error"])
		if expected == "" || current == expected {
			c.data["error"] = ""
		}
		c.mu.Unlock()
		return map[string]any{"ok": true}, nil

	default:
		return nil, errors.New("不支持的操作")
	}
}
