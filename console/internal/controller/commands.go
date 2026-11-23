package controller

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

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

	case "account.refresh":
		if c.provider == nil {
			return nil, errors.New("Provider 未配置")
		}
		c.log("正在刷新 Google 账号余额与运行实例状态", "计费", "info")
		account, err := c.provider.Account(ctx)
		if err != nil {
			return nil, err
		}
		if account == nil {
			account = map[string]any{"logged_in": false}
		}
		c.Update(map[string]any{"account": account})
		c.mu.Lock()
		c.lastAuth = nowUnixFloat()
		c.mu.Unlock()
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
		api := map[string]any{}
		if v, ok := c.vault.Get("public_api"); ok {
			if m := asMap(v); m != nil {
				api = m
			}
		}
		if asString(api["api_key"]) == "" {
			api["api_key"] = generateAPIKey()
			if err := c.vault.Set("public_api", api); err != nil {
				return nil, err
			}
		}
		return map[string]any{"key": asString(api["api_key"])}, nil

	case "api.tunnel_token":
		api := map[string]any{}
		if v, ok := c.vault.Get("public_api"); ok {
			if m := asMap(v); m != nil {
				api = m
			}
		}
		return map[string]any{"token": asString(api["tunnel_token"])}, nil

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
		api := map[string]any{}
		if v, ok := c.vault.Get("public_api"); ok {
			if m := asMap(v); m != nil {
				api = m
			}
		}
		api["api_key"] = generateAPIKey()
		if err := c.vault.Set("public_api", api); err != nil {
			return nil, err
		}
		c.log("API Key 已更换；重新开启服务后使用新密钥", "API", "info")
		return map[string]any{"ok": true}, nil

	case "api.start", "api.save":
		saved := map[string]any{}
		if v, ok := c.vault.Get("public_api"); ok {
			if m := asMap(v); m != nil {
				saved = m
			}
		}
		if rawMode, exists := payload["mode"]; exists && rawMode != nil {
			modeStr := asString(rawMode)
			if modeStr != "" && modeStr != "named" {
				return nil, errors.New("已不再支持临时域名，请配置固定域名和 Cloudflare Tunnel Token")
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
			return nil, errors.New("API 配置无效：请填写公网 HTTPS 固定域名，并粘贴有效的 Cloudflare Tunnel 安装命令或 Token")
		}
		stored := deepCopyMap(saved)
		stored["mode"] = "named"
		stored["api_key"] = asString(config["api_key"])
		stored["domain"] = asString(config["domain"])
		stored["tunnel_token"] = asString(config["tunnel_token"])
		if err := c.vault.Set("public_api", stored); err != nil {
			return nil, err
		}
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
		saved := map[string]any{}
		if v, ok := c.vault.Get("comfyui_canvas"); ok {
			if m := asMap(v); m != nil {
				saved = m
			}
		}
		if rawMode, exists := payload["mode"]; exists && rawMode != nil {
			modeStr := asString(rawMode)
			if modeStr != "" && modeStr != "named" {
				return nil, errors.New("已不再支持临时域名，请配置固定域名和 Cloudflare Tunnel Token")
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
			return nil, errors.New("ComfyUI 画布配置无效：请填写公网 HTTPS 固定域名，并粘贴有效的 Cloudflare Tunnel 安装命令或 Token")
		}
		stored := deepCopyMap(saved)
		stored["mode"] = "named"
		stored["domain"] = asString(config["domain"])
		stored["tunnel_token"] = asString(config["tunnel_token"])
		if err := c.vault.Set("comfyui_canvas", stored); err != nil {
			return nil, err
		}
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
		canvas := map[string]any{}
		if v, ok := c.vault.Get("comfyui_canvas"); ok {
			if m := asMap(v); m != nil {
				canvas = m
			}
		}
		return map[string]any{"token": asString(canvas["tunnel_token"])}, nil

	case "comfyui.workflow.import":
		if c.comfy == nil {
			return nil, errors.New("ComfyValidator 未配置")
		}
		req, err := c.comfy.ValidateWorkflowImport(payload)
		if err != nil {
			return nil, err
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
			if id := asString(storedItem["id"]); id != "" {
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
		merged := c.mergeWorkflowsLocked(asMapSlice(c.data["workflows"]))
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
		remaining := make([]map[string]any, 0)
		for _, w := range asMapSlice(c.data["workflows"]) {
			if asString(w["id"]) != wid {
				remaining = append(remaining, w)
			}
		}
		merged := c.mergeWorkflowsLocked(remaining)
		c.mu.Unlock()
		c.Update(map[string]any{"workflows": merged})
		c.log("已删除 ComfyUI 工作流："+wid, "ComfyUI", "info")
		return map[string]any{"ok": true}, nil

	case "comfyui.input.upload":
		fname := asString(payload["filename"])
		display := fname
		if display == "" {
			display = "文件"
		}
		c.log("正在上传 ComfyUI 输入素材："+display, "ComfyUI", "info")
		res, err := c.remote(ctx, "POST", "comfyui/inputs", map[string]any{
			"filename": fname,
			"data":     asString(payload["data"]),
		}, "", 0)
		if err != nil {
			return nil, err
		}
		c.log("ComfyUI 输入素材上传完成："+display, "ComfyUI", "info")
		return res, nil

	case "comfyui.run":
		if c.comfy == nil {
			return nil, errors.New("ComfyValidator 未配置")
		}
		req, err := c.comfy.ValidateWorkflowRun(payload)
		if err != nil {
			return nil, err
		}
		wfID := asString(req["workflow_id"])
		if err := c.ensureRemoteWorkflow(ctx, wfID); err != nil {
			return nil, err
		}
		c.log("请求运行 ComfyUI 工作流："+wfID, "ComfyUI", "info")
		res, err := c.remote(ctx, "POST", "comfyui/run", req, "", 0)
		if err != nil {
			return nil, err
		}
		if err := c.RefreshRemote(ctx, ""); err != nil {
			return nil, err
		}
		c.log("远端已受理 ComfyUI 工作流任务", "ComfyUI", "info")
		return res, nil

	case "comfyui.output.asset", "comfyui.output.delete":
		identity := asString(payload["id"])
		if !reHex32ID.MatchString(identity) {
			return nil, errors.New("产物编号不合法")
		}
		if action == "comfyui.output.delete" {
			c.log("正在删除远端 ComfyUI 作品文件", "ComfyUI", "info")
			res, err := c.remote(ctx, "DELETE", "comfyui/outputs/"+identity, nil, "", 0)
			if err != nil {
				return nil, err
			}
			if err := c.RefreshRemote(ctx, ""); err != nil {
				return nil, err
			}
			c.log("已删除远端 ComfyUI 作品文件", "ComfyUI", "info")
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
		return c.remote(ctx, "GET", fmt.Sprintf("comfyui/outputs/%s/asset?offset=%d", identity, offset), nil, "", 0)

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
			c.mu.Lock()
			c.lastAuth = 0
			c.remoteWorkflowVersions = make(map[string]float64)
			c.mu.Unlock()
			c.resetMonitorToAuto(true, true)
			c.Update(map[string]any{
				"models":        c.Presets(),
				"workflows":     c.mergeWorkflows(nil),
				"comfy_outputs": []any{},
				"auth_url":      "",
			})
			c.log("Google 登录已保存到本机，后续自动续期", "登录", "info")
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
			c.mu.Lock()
			c.lastAuth = 0
			c.mu.Unlock()
			c.resetMonitorToAuto(true, true)
			return map[string]any{"ok": true}, nil
		}
		return c.asyncOperation("完成 Google 登录", func() error {
			c.log("正在校验 Google 授权码并换取访问凭据", "登录", "info")
			if err := c.auth.ColabFinish(c.rootCtx, code); err != nil {
				return err
			}
			c.mu.Lock()
			c.lastAuth = 0
			c.remoteWorkflowVersions = make(map[string]float64)
			c.mu.Unlock()
			c.resetMonitorToAuto(true, true)
			c.Update(map[string]any{
				"models":        c.Presets(),
				"workflows":     c.mergeWorkflows(nil),
				"comfy_outputs": []any{},
				"auth_url":      "",
			})
			c.log("Google 登录已保存到本机，后续自动续期", "登录", "info")
			return nil
		}, nil, nil)

	case "drive.connect":
		return c.asyncOperation("挂载 Google 云盘 (ColabOne_Drive)", func() error {
			_, err := c.ConnectDrive(c.rootCtx)
			return err
		}, nil, nil)

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
		c.Update(map[string]any{"auth_url": "", "drive_auth_hint": ""})
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
				"retryable":     false,
				"waiting_retry": false,
				"retry_at":      0.0,
				"has_retried":   false,
				"allocated":     false,
				"gpu":           targetGPU,
			},
			func() error {
				c.startCancel.Clear()
				c.startRetry.Clear()
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
		nextOp["retryable"] = false
		nextOp["waiting_retry"] = false
		nextOp["retry_at"] = 0.0
		c.startCancel.Set()
		c.updateLocked(map[string]any{"operation": nextOp})
		c.mu.Unlock()
		c.log("已请求取消 GPU 分配", "GPU", "info")
		return map[string]any{"ok": true, "cancelled": true}, nil

	case "runtime.start.retry":
		c.mu.Lock()
		op := asMap(c.data["operation"])
		if op == nil ||
			asString(op["kind"]) != "runtime_start" ||
			!asBool(op["waiting_retry"]) ||
			asBool(op["cancelling"]) ||
			c.startCancel.IsSet() {
			c.mu.Unlock()
			return map[string]any{"ok": true, "triggered": false}, nil
		}
		nextOp := deepCopyMap(op)
		nextOp["waiting_retry"] = false
		nextOp["retryable"] = false
		nextOp["retry_at"] = 0.0
		c.startRetry.Set()
		c.updateLocked(map[string]any{"operation": nextOp})
		c.mu.Unlock()
		return map[string]any{"ok": true, "triggered": true}, nil

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
