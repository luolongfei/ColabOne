package controller

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"
)

func isGPUInstance(inst map[string]any) bool {
	variant := strings.ToUpper(asString(inst["variant"]))
	accel := strings.ToUpper(asString(inst["accelerator"]))
	if variant == "GPU" {
		return true
	}
	return accel != "" && accel != "CPU" && accel != "NONE" && accel != "TPU"
}

func (c *ColabOneController) isShuttingDown(ctx context.Context) bool {
	return c.stop.IsSet() || (ctx != nil && ctx.Err() != nil)
}

func (c *ColabOneController) closeExternalObserver(endpoint string, shutdownKernel bool) {
	if c.provider == nil {
		return
	}
	c.provider.CloseObserver(endpoint, shutdownKernel)
}

func (c *ColabOneController) resetMonitorToAuto(closeObserver, clearObservations bool) {
	c.monitorMu.Lock()
	c.monitorMode = "auto"
	c.monitorEndpoint = ""
	c.monitorPaused = false
	c.monitorGen++
	if clearObservations {
		c.externalObservations = make(map[string]map[string]any)
	}
	c.monitorMu.Unlock()
	if closeObserver {
		c.closeExternalObserver("", true)
	}
	c.observerWake.Set()
}

func (c *ColabOneController) resolveMonitorInstances(
	enriched []map[string]any,
	activeRuntime map[string]any,
	activeEP string,
) ([]map[string]any, string, bool) {
	byID := make(map[string]map[string]any, len(enriched))
	managedItems := make([]map[string]any, 0)
	extGPUItems := make([]map[string]any, 0)
	extOtherItems := make([]map[string]any, 0)

	for _, item := range enriched {
		ep := asString(item["id"])
		if ep == "" {
			continue
		}
		if _, dup := byID[ep]; dup {
			continue
		}
		byID[ep] = item
		if asBool(item["managed"]) {
			managedItems = append(managedItems, item)
		} else if isGPUInstance(item) {
			extGPUItems = append(extGPUItems, item)
		} else {
			extOtherItems = append(extOtherItems, item)
		}
	}

	sort.SliceStable(managedItems, func(i, j int) bool {
		ai := asBool(managedItems[i]["active"]) || asString(managedItems[i]["id"]) == activeEP
		aj := asBool(managedItems[j]["active"]) || asString(managedItems[j]["id"]) == activeEP
		if ai != aj {
			return ai
		}
		return false
	})

	ordered := make([]map[string]any, 0, len(managedItems)+len(extGPUItems)+len(extOtherItems))
	ordered = append(ordered, managedItems...)
	ordered = append(ordered, extGPUItems...)
	ordered = append(ordered, extOtherItems...)

	rtState := asString(activeRuntime["state"])
	if rtState == "" {
		rtState = "stopped"
	}

	c.monitorMu.Lock()
	mode := c.monitorMode
	selected := c.monitorEndpoint
	if mode == "manual" {
		if _, exists := byID[selected]; exists {
			c.monitorMu.Unlock()
			return ordered, selected, false
		}
		if selected != "" {
			// Selected instance stopped or disappeared -> fall back cleanly to auto.
			c.monitorMode = "auto"
			c.monitorEndpoint = ""
			c.monitorPaused = false
			c.monitorGen++
		}
	}
	c.monitorMu.Unlock()

	// Auto selection priority:
	// 1. Current machine's active managed instance
	// 2. Any other managed instance on this machine
	// 3. Local starting/releasing/disconnected runtime before allocation lands
	// 4. First running remote GPU instance
	// 5. First running remote non-GPU instance
	if activeEP != "" {
		if _, exists := byID[activeEP]; exists {
			return ordered, activeEP, true
		}
	}
	if len(managedItems) > 0 {
		return ordered, asString(managedItems[0]["id"]), true
	}
	if rtState == "starting" || rtState == "releasing" || rtState == "disconnected" {
		return ordered, asString(activeRuntime["id"]), true
	}
	if len(extGPUItems) > 0 {
		return ordered, asString(extGPUItems[0]["id"]), true
	}
	if len(extOtherItems) > 0 {
		return ordered, asString(extOtherItems[0]["id"]), true
	}
	if rtState != "stopped" {
		return ordered, asString(activeRuntime["id"]), true
	}
	return ordered, "", true
}

func (c *ColabOneController) buildMonitorSnapshot(
	enriched []map[string]any,
	activeRuntime map[string]any,
	decoratedModels []map[string]any,
	activeEP string,
) map[string]any {
	ordered, targetEP, isAuto := c.resolveMonitorInstances(enriched, activeRuntime, activeEP)

	c.monitorMu.Lock()
	paused := c.monitorPaused
	obsMap := make(map[string]map[string]any, len(c.externalObservations))
	for k, v := range c.externalObservations {
		obsMap[k] = deepCopyMap(v)
	}
	c.monitorMu.Unlock()

	byID := make(map[string]map[string]any, len(ordered))
	options := make([]map[string]any, 0, len(ordered))
	for _, item := range ordered {
		ep := asString(item["id"])
		if ep != "" {
			byID[ep] = item
		}
		isManaged := asBool(item["managed"])
		var optSource, optState, gpuName, loadedModel string
		var loc map[string]any
		if isManaged {
			optSource = "local"
			optState = asString(item["state"])
			if optState == "" {
				optState = "ready"
			}
			gpuName = asString(item["gpu_name"])
			loadedModel = asString(item["loaded_model"])
			loc = asMap(item["location"])
		} else {
			obs := obsMap[ep]
			if asString(obs["source"]) == "remote_colabone" {
				optSource = "remote_colabone"
			} else {
				optSource = "remote_readonly"
			}
			if paused && ep == targetEP {
				optState = "paused"
			} else if obs != nil && asMap(obs["runtime"]) != nil {
				optState = asString(obs["state"])
				if optState == "" {
					optState = "ready"
				}
			} else if obs != nil && asString(obs["error"]) != "" {
				optState = "error"
			} else if ep == targetEP {
				optState = "connecting"
			} else {
				optState = "idle"
			}
			obsRT := asMap(obs["runtime"])
			obsGPU := asMap(obsRT["gpu"])
			gpuName = asString(obsGPU["name"])
			if gpuName == "" {
				gpuName = asString(item["gpu_name"])
			}
			loadedModel = asString(obs["loaded_model_name"])
			if loadedModel == "" {
				loadedModel = asString(item["loaded_model"])
			}
			loc = asMap(obsRT["location"])
			if len(loc) == 0 {
				loc = asMap(item["location"])
			}
		}
		opt := map[string]any{
			"id":           ep,
			"managed":      isManaged,
			"active":       asBool(item["active"]) && isManaged,
			"accelerator":  asString(item["accelerator"]),
			"variant":      asString(item["variant"]),
			"shape":        asString(item["shape"]),
			"gpu_name":     gpuName,
			"loaded_model": loadedModel,
			"source":       optSource,
			"state":        optState,
		}
		if len(loc) > 0 {
			opt["location"] = deepCopyMap(loc)
		}
		options = append(options, opt)
	}

	mode := "manual"
	if isAuto {
		mode = "auto"
	}

	activeLoadedID := asString(activeRuntime["loaded_model"])
	activeLoadedName := activeLoadedID
	for _, m := range decoratedModels {
		if asString(m["id"]) == activeLoadedID {
			if n := asString(m["name"]); n != "" {
				activeLoadedName = n
			}
			break
		}
	}

	// Case 1: Active local runtime (or local starting/ready without vault entry)
	targetItem := byID[targetEP]
	if targetEP == "" {
		rtState := asString(activeRuntime["state"])
		if rtState == "" {
			rtState = "stopped"
		}
		if rtState != "stopped" {
			drive := asMap(activeRuntime["drive"])
			return map[string]any{
				"mode":              mode,
				"endpoint":          "",
				"managed":           true,
				"source":            "local",
				"paused":            false,
				"error":             "",
				"runtime":           deepCopyMap(activeRuntime),
				"loaded_model_name": activeLoadedName,
				"drive_connected":   asBool(drive["connected"]),
				"drive_free_bytes":  drive["free_bytes"],
				"options":           options,
			}
		}
		return map[string]any{
			"mode":              mode,
			"endpoint":          "",
			"managed":           false,
			"source":            "none",
			"paused":            false,
			"error":             "",
			"runtime":           map[string]any{"state": "stopped"},
			"loaded_model_name": "",
			"drive_connected":   false,
			"drive_free_bytes":  nil,
			"options":           options,
		}
	}

	if targetItem == nil || asBool(targetItem["managed"]) {
		if targetEP == activeEP || targetItem == nil {
			rt := deepCopyMap(activeRuntime)
			if rt == nil {
				rt = map[string]any{}
			}
			if targetEP != "" && asString(rt["id"]) == "" {
				rt["id"] = targetEP
			}
			drive := asMap(rt["drive"])
			return map[string]any{
				"mode":              mode,
				"endpoint":          targetEP,
				"managed":           true,
				"source":            "local",
				"paused":            false,
				"error":             "",
				"runtime":           rt,
				"loaded_model_name": activeLoadedName,
				"drive_connected":   asBool(drive["connected"]),
				"drive_free_bytes":  drive["free_bytes"],
				"options":           options,
			}
		}

		// Secondary managed instance on this client
		c.mu.Lock()
		cached := c.instanceStates[targetEP]
		var rt map[string]any
		var secModels []map[string]any
		if cached != nil {
			rt = deepCopyMap(cached.Runtime)
			secModels = deepCopyMapSlice(cached.Models)
		}
		c.mu.Unlock()
		if rt == nil {
			rt = map[string]any{"state": "ready", "id": targetEP}
		}
		if asString(rt["id"]) == "" {
			rt["id"] = targetEP
		}
		if loc := asMap(targetItem["location"]); len(loc) > 0 && len(asMap(rt["location"])) == 0 {
			rt["location"] = deepCopyMap(loc)
		}
		secLoadedID := asString(rt["loaded_model"])
		secLoadedName := secLoadedID
		for _, m := range secModels {
			if asString(m["id"]) == secLoadedID {
				if n := asString(m["name"]); n != "" {
					secLoadedName = n
				}
				break
			}
		}
		drive := asMap(rt["drive"])
		return map[string]any{
			"mode":              mode,
			"endpoint":          targetEP,
			"managed":           true,
			"source":            "local",
			"paused":            false,
			"error":             "",
			"runtime":           rt,
			"loaded_model_name": secLoadedName,
			"drive_connected":   asBool(drive["connected"]),
			"drive_free_bytes":  drive["free_bytes"],
			"options":           options,
		}
	}

	// Case 2: External (non-managed) remote instance (read-only observer)
	obs := obsMap[targetEP]
	source := "remote_readonly"
	if asString(obs["source"]) == "remote_colabone" {
		source = "remote_colabone"
	}
	obsRT := deepCopyMap(asMap(obs["runtime"]))
	errorMsg := ""
	if !paused {
		errorMsg = asString(obs["error"])
	}
	var rt map[string]any
	if obsRT != nil {
		if asString(obsRT["id"]) == "" {
			obsRT["id"] = targetEP
		}
		if loc := asMap(targetItem["location"]); len(loc) > 0 && len(asMap(obsRT["location"])) == 0 {
			obsRT["location"] = deepCopyMap(loc)
		}
		failures, _ := asInt(obs["consecutive_failures"])
		if !paused && errorMsg != "" && failures >= 3 {
			obsRT["state"] = "disconnected"
		} else {
			obsRT["state"] = "ready"
		}
		rt = obsRT
	} else {
		st := "connecting"
		if paused {
			st = "stopped"
		} else if errorMsg != "" {
			st = "disconnected"
		}
		rt = map[string]any{
			"id":    targetEP,
			"state": st,
		}
		if loc := asMap(targetItem["location"]); len(loc) > 0 {
			rt["location"] = deepCopyMap(loc)
		}
	}

	return map[string]any{
		"mode":              mode,
		"endpoint":          targetEP,
		"managed":           false,
		"source":            source,
		"paused":            paused,
		"error":             errorMsg,
		"runtime":           rt,
		"loaded_model_name": asString(obs["loaded_model_name"]),
		"drive_connected":   asBool(obs["drive_connected"]),
		"drive_free_bytes":  obs["drive_free_bytes"],
		"options":           options,
	}
}

// SelectMonitor selects an instance to observe in the telemetry monitor, or returns to auto mode when endpoint is empty.
func (c *ColabOneController) SelectMonitor(endpoint string) (map[string]any, error) {
	ep := strings.TrimSpace(endpoint)
	if ep == "" {
		c.resetMonitorToAuto(true, false)
		return map[string]any{"ok": true, "mode": "auto", "endpoint": ""}, nil
	}
	sessions := c.vaultSessions()
	remotes := c.vaultRemotes()
	activeEP := c.activeEndpointFrom(sessions)
	c.mu.Lock()
	account := deepCopyMap(asMap(c.data["account"]))
	c.mu.Unlock()
	enriched := c.enrichInstancesWith(account, sessions, remotes, activeEP)
	var targetItem map[string]any
	for _, item := range enriched {
		if asString(item["id"]) == ep {
			targetItem = item
			break
		}
	}
	if targetItem == nil {
		return nil, errors.New("所选实例不存在或已停止")
	}
	isManaged := asBool(targetItem["managed"])
	c.monitorMu.Lock()
	if isManaged && ep == activeEP {
		c.monitorMode = "auto"
		c.monitorEndpoint = ""
	} else {
		c.monitorMode = "manual"
		c.monitorEndpoint = ep
	}
	c.monitorPaused = false
	c.monitorGen++
	if !isManaged {
		if obs, ok := c.externalObservations[ep]; ok && obs != nil {
			obs["consecutive_failures"] = 0
			obs["last_attempt"] = 0.0
			obs["error"] = ""
		}
	}
	mode := c.monitorMode
	c.monitorMu.Unlock()

	if isManaged {
		c.closeExternalObserver("", true)
	} else {
		c.observerWake.Set()
	}
	return map[string]any{"ok": true, "mode": mode, "endpoint": ep}, nil
}

// SetMonitorPaused toggles or explicitly sets pause state for external telemetry observation.
func (c *ColabOneController) SetMonitorPaused(paused *bool) map[string]any {
	c.monitorMu.Lock()
	nextPaused := !c.monitorPaused
	if paused != nil {
		nextPaused = *paused
	}
	c.monitorPaused = nextPaused
	c.monitorGen++
	if !nextPaused {
		for _, obs := range c.externalObservations {
			if obs != nil {
				obs["consecutive_failures"] = 0
				obs["last_attempt"] = 0.0
				obs["error"] = ""
			}
		}
	}
	c.monitorMu.Unlock()
	if nextPaused {
		c.closeExternalObserver("", true)
	} else {
		c.observerWake.Set()
	}
	return map[string]any{"ok": true, "paused": nextPaused}
}

// RetryMonitor clears pause/failure backoff states and triggers an immediate external observation attempt.
func (c *ColabOneController) RetryMonitor() map[string]any {
	c.monitorMu.Lock()
	c.monitorPaused = false
	c.monitorGen++
	for _, obs := range c.externalObservations {
		if obs != nil {
			obs["consecutive_failures"] = 0
			obs["last_attempt"] = 0.0
			obs["error"] = ""
		}
	}
	c.monitorMu.Unlock()
	c.observerWake.Set()
	return map[string]any{"ok": true}
}

// PollExternalMonitorOnce samples hardware telemetry once for the currently targeted external instance if active.
func (c *ColabOneController) PollExternalMonitorOnce(ctx context.Context, force bool) bool {
	if c.isShuttingDown(ctx) {
		return false
	}
	if !c.observerPollBusy.CompareAndSwap(false, true) {
		return false
	}
	defer c.observerPollBusy.Store(false)

	if c.isShuttingDown(ctx) {
		return false
	}

	c.mu.Lock()
	account := deepCopyMap(asMap(c.data["account"]))
	activeRuntime := deepCopyMap(asMap(c.data["runtime"]))
	c.mu.Unlock()

	if !asBool(account["logged_in"]) && len(asMapSlice(account["instances"])) == 0 {
		c.closeExternalObserver("", true)
		return false
	}
	sessions := c.vaultSessions()
	remotes := c.vaultRemotes()
	activeEP := c.activeEndpointFrom(sessions)
	enriched := c.enrichInstancesWith(account, sessions, remotes, activeEP)
	knownIDs := make(map[string]bool, len(enriched))
	for _, item := range enriched {
		if id := asString(item["id"]); id != "" {
			knownIDs[id] = true
		}
	}

	// Evict stale observations for instances that no longer exist.
	stale := make([]string, 0)
	c.monitorMu.Lock()
	for ep := range c.externalObservations {
		if !knownIDs[ep] {
			stale = append(stale, ep)
			delete(c.externalObservations, ep)
		}
	}
	c.monitorMu.Unlock()
	for _, ep := range stale {
		c.closeExternalObserver(ep, false)
	}

	ordered, targetEP, _ := c.resolveMonitorInstances(enriched, activeRuntime, activeEP)
	var targetItem map[string]any
	for _, item := range ordered {
		if asString(item["id"]) == targetEP {
			targetItem = item
			break
		}
	}

	c.monitorMu.Lock()
	paused := c.monitorPaused
	c.monitorMu.Unlock()

	if targetEP == "" || targetItem == nil || asBool(targetItem["managed"]) || paused || c.opBusy.Load() {
		if paused || targetEP == "" || (targetItem != nil && asBool(targetItem["managed"])) {
			c.closeExternalObserver("", true)
		}
		return false
	}

	now := nowUnixFloat()
	c.monitorMu.Lock()
	if c.monitorPaused || c.isShuttingDown(ctx) {
		c.monitorMu.Unlock()
		return false
	}
	prev := c.externalObservations[targetEP]
	failures, _ := asInt(prev["consecutive_failures"])
	lastAttempt, _ := asFloat(prev["last_attempt"])
	c.monitorMu.Unlock()

	minGap := 2.5
	if failures > 0 {
		exp := failures - 1
		if exp > 3 {
			exp = 3
		}
		minGap = math.Min(15.0, 3.0*math.Pow(2, float64(exp)))
	}
	if !force && lastAttempt > 0 && (now-lastAttempt) < minGap {
		return false
	}

	if c.provider == nil || c.isShuttingDown(ctx) {
		return false
	}

	c.monitorMu.Lock()
	if c.monitorPaused || c.isShuttingDown(ctx) {
		c.monitorMu.Unlock()
		return false
	}
	gen := c.monitorGen
	slot, ok := c.externalObservations[targetEP]
	if !ok || slot == nil {
		slot = map[string]any{}
		c.externalObservations[targetEP] = slot
	}
	slot["last_attempt"] = now
	c.monitorMu.Unlock()

	sample, err := c.provider.ObserveExternalTelemetry(ctx, targetEP)
	if err != nil {
		if c.isShuttingDown(ctx) {
			return false
		}
		if errors.Is(err, ErrLookup) {
			c.monitorMu.Lock()
			if c.stop.IsSet() || c.monitorPaused || c.monitorGen != gen {
				c.monitorMu.Unlock()
				return false
			}
			delete(c.externalObservations, targetEP)
			c.monitorMu.Unlock()
			c.closeExternalObserver(targetEP, false)
			c.mu.Lock()
			c.lastAuth = 0
			c.mu.Unlock()
			return false
		}
		c.monitorMu.Lock()
		if c.stop.IsSet() || c.monitorPaused || c.monitorGen != gen {
			c.monitorMu.Unlock()
			return false
		}
		slot, ok := c.externalObservations[targetEP]
		if !ok || slot == nil {
			slot = map[string]any{}
			c.externalObservations[targetEP] = slot
		}
		curFail, _ := asInt(slot["consecutive_failures"])
		slot["consecutive_failures"] = curFail + 1
		slot["last_attempt"] = nowUnixFloat()
		msg := truncateRunes(SafeMessage(err.Error()), 180)
		if msg == "" {
			msg = "暂无法读取该远端实例的硬件负载，将自动重试"
		}
		slot["error"] = msg
		c.monitorMu.Unlock()
		return false
	}

	if c.isShuttingDown(ctx) {
		return false
	}

	// Re-verify that the user did not pause, retry, or switch away while RPC was in flight.
	c.mu.Lock()
	curAccount := deepCopyMap(asMap(c.data["account"]))
	curRuntime := deepCopyMap(asMap(c.data["runtime"]))
	c.mu.Unlock()
	curSessions := c.vaultSessions()
	curRemotes := c.vaultRemotes()
	curActiveEP := c.activeEndpointFrom(curSessions)
	curEnriched := c.enrichInstancesWith(curAccount, curSessions, curRemotes, curActiveEP)
	_, curTarget, _ := c.resolveMonitorInstances(curEnriched, curRuntime, curActiveEP)

	c.monitorMu.Lock()
	stillActive := !c.stop.IsSet() && (ctx == nil || ctx.Err() == nil) && !c.monitorPaused && c.monitorGen == gen && curTarget == targetEP
	if stillActive {
		rtData := deepCopyMap(asMap(sample["runtime"]))
		if rtData == nil {
			rtData = map[string]any{"id": targetEP, "state": "ready"}
		}
		if loc := asMap(targetItem["location"]); len(loc) > 0 && len(asMap(rtData["location"])) == 0 {
			rtData["location"] = deepCopyMap(loc)
		}
		source := asString(sample["source"])
		if source == "" {
			source = "remote_readonly"
		}
		ts := nowUnixFloat()
		c.externalObservations[targetEP] = map[string]any{
			"state":                "ready",
			"source":               source,
			"runtime":              rtData,
			"loaded_model_name":    asString(sample["loaded_model_name"]),
			"drive_connected":      asBool(sample["drive_connected"]),
			"drive_free_bytes":     sample["drive_free_bytes"],
			"error":                "",
			"updated_at":           ts,
			"last_attempt":         ts,
			"consecutive_failures": 0,
		}
	}
	c.monitorMu.Unlock()

	if !stillActive {
		c.closeExternalObserver(targetEP, true)
		return false
	}
	return true
}

func (c *ColabOneController) runExternalMonitorLoop() {
	for !c.stop.IsSet() {
		c.PollExternalMonitorOnce(c.rootCtx, false)
		timer := time.NewTimer(c.observerInterval)
		select {
		case <-c.stop.Chan():
			timer.Stop()
			return
		case <-c.observerWake.Chan():
			timer.Stop()
			c.observerWake.Clear()
		case <-timer.C:
		}
	}
}

// ---------------------------------------------------------------------------
// Main background monitor loop (account refresh, heartbeats, auto-upgrade)
// ---------------------------------------------------------------------------

type orphanRemoteCleaner interface {
	ClearOrphanedRemotes()
}

func (c *ColabOneController) canAutoUpgradeRuntime(rt map[string]any, expectedEP string) bool {
	if !c.RuntimeNeedsUpgrade(rt) {
		return false
	}
	if expectedEP != "" {
		if rtID := asString(rt["id"]); rtID != "" && rtID != expectedEP {
			return false
		}
	}
	if asBool(rt["loaded_model"]) || asBool(rt["active_engine"]) {
		return false
	}
	pub := asMap(rt["public_api"])
	pubState := asString(pub["state"])
	if asBool(pub["enabled"]) || pubState == "starting" || pubState == "ready" || pubState == "reconnecting" {
		return false
	}
	comfy := asMap(rt["comfyui"])
	comfyState := asString(comfy["state"])
	comfyCanvas := asMap(comfy["canvas"])
	canvasState := asString(comfyCanvas["state"])
	if asBool(comfy["running"]) ||
		comfyState == "starting" || comfyState == "running" || comfyState == "ready" ||
		asBool(comfyCanvas["enabled"]) ||
		canvasState == "starting" || canvasState == "ready" || canvasState == "reconnecting" {
		return false
	}
	for _, t := range asMapSlice(rt["tasks"]) {
		if asString(t["state"]) == "running" {
			return false
		}
	}
	return true
}

// MonitorOnce runs one iteration of the main background monitor loop.
func (c *ColabOneController) MonitorOnce(ctx context.Context) {
	if c.provider == nil || c.isShuttingDown(ctx) {
		return
	}
	hadConnError := false
	now := nowUnixFloat()

	c.mu.Lock()
	lastAuth := c.lastAuth
	c.mu.Unlock()

	if now-lastAuth > 30 {
		acct, err := c.provider.Account(ctx)
		if err != nil {
			if c.isShuttingDown(ctx) {
				return
			}
			hadConnError = true
			// Back off 15s instead of hammering every 2s and starving heartbeats.
			c.mu.Lock()
			c.lastAuth = now - 15
			c.mu.Unlock()
			c.Update(map[string]any{
				"error": "连接检查失败：" + errTypeName(err) + "。客户端会自动重试，请检查网络连接。",
			})
		} else {
			if c.isShuttingDown(ctx) {
				return
			}
			c.Update(map[string]any{"account": acct})
			c.mu.Lock()
			c.lastAuth = now
			c.mu.Unlock()
			c.observerWake.Set()
		}
	}

	if c.isShuttingDown(ctx) {
		return
	}

	var sessions map[string]map[string]any
	if !c.opBusy.Load() {
		sessions = c.vaultSessions()
		if len(sessions) == 0 && !c.opBusy.Load() {
			remotes := c.vaultRemotes()
			_, hasLegacyRemote := c.vault.Get("remote")
			_, hasRemotes := c.vault.Get("remotes")
			if len(remotes) > 0 || hasLegacyRemote || hasRemotes {
				if cleaner, ok := c.provider.(orphanRemoteCleaner); ok {
					cleaner.ClearOrphanedRemotes()
				} else if !c.opBusy.Load() && len(c.vaultSessions()) == 0 {
					c.provider.Clear("")
				}
			}
		}
	}

	sessionsFresh := false
	var sessionsOpGen uint64
	if !c.opBusy.Load() {
		sessionsOpGen = c.opGen.Load()
		sessions = c.vaultSessions()
		sessionsFresh = true
		if !c.opBusy.Load() && c.opGen.Load() == sessionsOpGen {
			var pendingLogs []pendingActivityLog
			c.mu.Lock()
			if !c.opBusy.Load() && c.opGen.Load() == sessionsOpGen {
				for ep := range c.instanceStates {
					if _, exists := sessions[ep]; !exists {
						delete(c.instanceStates, ep)
					}
				}
				for ep := range c.upgradeAttempted {
					if _, exists := sessions[ep]; !exists {
						delete(c.upgradeAttempted, ep)
					}
				}
				if len(sessions) == 0 {
					rtState := asString(asMap(c.data["runtime"])["state"])
					if rtState == "ready" || rtState == "disconnected" {
						c.remoteWorkflowVersions = make(map[string]float64)
						pendingLogs = c.updateLocked(map[string]any{
							"runtime":       map[string]any{"state": "stopped"},
							"models":        c.Presets(),
							"workflows":     c.mergeWorkflowsLocked(nil),
							"tts_engines":   c.builtinTtsEngines(),
							"comfy_outputs": []any{},
						})
					}
				}
			}
			c.mu.Unlock()
			c.flushActivityLogs(pendingLogs)
		}
	}

	if c.isShuttingDown(ctx) {
		return
	}

	var remotes map[string]map[string]any
	var activeEP string
	var activeRemote map[string]any
	activeOpGen := c.opGen.Load()
	if !c.opBusy.Load() {
		if !sessionsFresh || activeOpGen != sessionsOpGen {
			sessions = c.vaultSessions()
		}
		if len(sessions) > 0 {
			remotes = c.vaultRemotes()
			activeEP = c.activeEndpointFrom(sessions)
			activeRemote = remotes[activeEP]
		}
	}

	if activeRemote != nil && !c.opBusy.Load() && c.opGen.Load() == activeOpGen {
		ep := asString(activeRemote["endpoint"])
		// Renew the proxy before any RPC. A failed configuration request must not
		// starve the very heartbeat that repairs it.
		var activeErr error
		aborted := false

		c.mu.Lock()
		lastHB := c.lastHeartbeat
		c.mu.Unlock()
		if now-lastHB > 30 {
			if err := c.provider.Heartbeat(ctx, ""); err != nil {
				if c.isShuttingDown(ctx) {
					return
				}
				activeErr = err
			} else {
				if c.isShuttingDown(ctx) {
					return
				}
				if c.opBusy.Load() || c.opGen.Load() != activeOpGen {
					return
				}
				remainingSessions := c.vaultSessions()
				if len(remainingSessions) == 0 {
					if !c.opBusy.Load() && c.opGen.Load() == activeOpGen {
						var pendingLogs []pendingActivityLog
						c.mu.Lock()
						if !c.opBusy.Load() && c.opGen.Load() == activeOpGen {
							c.lastHeartbeat = 0
							clear(c.instanceStates)
							clear(c.upgradeAttempted)
							c.remoteWorkflowVersions = make(map[string]float64)
							pendingLogs = c.updateLocked(map[string]any{
								"runtime":       map[string]any{"state": "stopped"},
								"models":        c.Presets(),
								"workflows":     c.mergeWorkflowsLocked(nil),
								"tts_engines":   c.builtinTtsEngines(),
								"comfy_outputs": []any{},
							})
						}
						c.mu.Unlock()
						c.flushActivityLogs(pendingLogs)
					}
					aborted = true
				} else {
					nextActive := c.activeEndpointFrom(remainingSessions)
					if nextActive != activeEP {
						if !c.opBusy.Load() && c.opGen.Load() == activeOpGen {
							c.mu.Lock()
							if !c.opBusy.Load() && c.opGen.Load() == activeOpGen {
								c.lastHeartbeat = 0
								if activeEP != "" {
									if _, stillOwned := remainingSessions[activeEP]; !stillOwned {
										delete(c.instanceStates, activeEP)
										delete(c.upgradeAttempted, activeEP)
										if nextActive != "" {
											c.activateCachedWithSessionsLocked(nextActive, remainingSessions)
										}
									}
								}
							}
							c.mu.Unlock()
						}
						aborted = true
					} else {
						stamped := false
						if !c.opBusy.Load() && c.opGen.Load() == activeOpGen {
							c.mu.Lock()
							if !c.opBusy.Load() && c.opGen.Load() == activeOpGen {
								c.lastHeartbeat = now
								stamped = true
							}
							c.mu.Unlock()
						}
						if !stamped {
							aborted = true
						}
					}
				}
			}
		}

		if aborted || c.isShuttingDown(ctx) || c.opBusy.Load() || c.opGen.Load() != activeOpGen {
			return
		}

		if activeErr == nil {
			if !c.opBusy.Load() {
				c.SendActivity(ctx)
			}
			if c.isShuttingDown(ctx) || c.opBusy.Load() || c.opGen.Load() != activeOpGen {
				return
			}
			// Refresh snapshot BEFORE configure_remote so `protocol` is populated
			// and we never overwrite a live VM's idle_minutes down to legacy 4 min.
			_, activeErr = c.refreshRemote(ctx, "", true)
			if activeErr != nil && c.isShuttingDown(ctx) {
				return
			}
		}
		if activeErr == nil {
			if c.isShuttingDown(ctx) || c.opBusy.Load() || c.opGen.Load() != activeOpGen {
				return
			}
			c.mu.Lock()
			lastCfg := c.lastConfig
			c.mu.Unlock()
			if now-lastCfg > 600 {
				_, activeErr = c.configureRemote(ctx, "", true)
				if activeErr != nil && c.isShuttingDown(ctx) {
					return
				}
			}
		}
		if activeErr == nil {
			if c.isShuttingDown(ctx) || c.opBusy.Load() || c.opGen.Load() != activeOpGen {
				return
			}
			c.mu.Lock()
			rt := deepCopyMap(asMap(c.data["runtime"]))
			attempted := c.upgradeAttempted[ep]
			c.mu.Unlock()

			if !attempted && c.canAutoUpgradeRuntime(rt, ep) {
				upgradeEP := ep
				_, _ = c.asyncOperation("更新远端管理服务", func() error {
					return c.UpgradeService(c.rootCtx, false, true)
				}, nil, func() error {
					if c.isShuttingDown(ctx) {
						return errors.New("客户端正在关闭")
					}
					if c.activeEndpoint() != upgradeEP {
						return errors.New("当前活跃实例已切换")
					}
					c.mu.Lock()
					if c.upgradeAttempted[upgradeEP] {
						c.mu.Unlock()
						return errors.New("已尝试更新远端服务")
					}
					liveRT := asMap(c.data["runtime"])
					if !c.canAutoUpgradeRuntime(liveRT, upgradeEP) {
						c.mu.Unlock()
						return errors.New("远端实例状态已变化")
					}
					c.upgradeAttempted[upgradeEP] = true
					c.mu.Unlock()
					return nil
				})
			}
		} else {
			if c.isShuttingDown(ctx) {
				return
			}
			if !c.opBusy.Load() && c.opGen.Load() == activeOpGen {
				curSessions := c.vaultSessions()
				if ep == c.activeEndpointFrom(curSessions) {
					if _, owned := curSessions[ep]; owned {
						var pendingLogs []pendingActivityLog
						c.mu.Lock()
						if !c.opBusy.Load() && c.opGen.Load() == activeOpGen {
							pendingLogs = c.updateWithSessionsLocked(map[string]any{
								"runtime": map[string]any{
									"state": "disconnected",
									"id":    ep,
								},
							}, curSessions, ep)
						}
						c.mu.Unlock()
						c.flushActivityLogs(pendingLogs)
					}
				}
			}
		}
	}

	if c.isShuttingDown(ctx) {
		return
	}

	// Keep any secondary managed instances alive and refreshed in parallel.
	if !c.opBusy.Load() && c.opGen.Load() == activeOpGen {
		remoteKeys := make([]string, 0, len(remotes))
		for ep := range remotes {
			remoteKeys = append(remoteKeys, ep)
		}
		sort.Strings(remoteKeys)
		for _, ep := range remoteKeys {
			if c.isShuttingDown(ctx) || c.opBusy.Load() || c.opGen.Load() != activeOpGen {
				return
			}
			if ep == activeEP {
				continue
			}
			if _, owned := c.vaultSessions()[ep]; !owned {
				continue
			}
			c.mu.Lock()
			if c.opBusy.Load() || c.opGen.Load() != activeOpGen {
				c.mu.Unlock()
				return
			}
			entry := c.ensureInstanceStateLocked(ep)
			entryLastHB := entry.LastHeartbeat
			entryLastCfg := entry.LastConfig
			c.mu.Unlock()

			if now-entryLastHB > 30 {
				if err := c.provider.Heartbeat(ctx, ep); err != nil {
					if c.isShuttingDown(ctx) || c.opBusy.Load() || c.opGen.Load() != activeOpGen {
						return
					}
					if _, owned := c.vaultSessions()[ep]; owned {
						c.mu.Lock()
						if c.opBusy.Load() || c.opGen.Load() != activeOpGen {
							c.mu.Unlock()
							return
						}
						if st, ok := c.instanceStates[ep]; ok {
							st.Runtime = map[string]any{
								"state": "disconnected",
								"id":    ep,
							}
						}
						c.mu.Unlock()
					}
					continue
				}
				if c.isShuttingDown(ctx) || c.opBusy.Load() || c.opGen.Load() != activeOpGen {
					return
				}
				if _, owned := c.vaultSessions()[ep]; !owned {
					c.mu.Lock()
					if c.opBusy.Load() || c.opGen.Load() != activeOpGen {
						c.mu.Unlock()
						return
					}
					delete(c.instanceStates, ep)
					delete(c.upgradeAttempted, ep)
					c.mu.Unlock()
					continue
				}
				c.mu.Lock()
				if c.opBusy.Load() || c.opGen.Load() != activeOpGen {
					c.mu.Unlock()
					return
				}
				if st, ok := c.instanceStates[ep]; ok {
					st.LastHeartbeat = now
				}
				c.mu.Unlock()
			}
			if c.isShuttingDown(ctx) || c.opBusy.Load() || c.opGen.Load() != activeOpGen {
				return
			}
			if _, err := c.refreshRemote(ctx, ep, true); err != nil {
				if c.isShuttingDown(ctx) || c.opBusy.Load() || c.opGen.Load() != activeOpGen {
					return
				}
				if _, owned := c.vaultSessions()[ep]; owned {
					c.mu.Lock()
					if c.opBusy.Load() || c.opGen.Load() != activeOpGen {
						c.mu.Unlock()
						return
					}
					if st, ok := c.instanceStates[ep]; ok {
						st.Runtime = map[string]any{
							"state": "disconnected",
							"id":    ep,
						}
					}
					c.mu.Unlock()
				}
				continue
			}
			if c.isShuttingDown(ctx) || c.opBusy.Load() || c.opGen.Load() != activeOpGen {
				return
			}
			if now-entryLastCfg > 600 {
				if _, err := c.configureRemote(ctx, ep, true); err != nil {
					if c.isShuttingDown(ctx) || c.opBusy.Load() || c.opGen.Load() != activeOpGen {
						return
					}
					if _, owned := c.vaultSessions()[ep]; owned {
						c.mu.Lock()
						if c.opBusy.Load() || c.opGen.Load() != activeOpGen {
							c.mu.Unlock()
							return
						}
						if st, ok := c.instanceStates[ep]; ok {
							st.Runtime = map[string]any{
								"state": "disconnected",
								"id":    ep,
							}
						}
						c.mu.Unlock()
					}
					continue
				}
			}
		}
	}

	if !hadConnError && !c.isShuttingDown(ctx) {
		var pendingLogs []pendingActivityLog
		c.mu.Lock()
		if strings.HasPrefix(asString(c.data["error"]), "连接检查失败：") {
			pendingLogs = c.updateLocked(map[string]any{"error": ""})
		}
		c.mu.Unlock()
		c.flushActivityLogs(pendingLogs)
	}
}

func (c *ColabOneController) effectiveMonitorWait() time.Duration {
	wait := c.monitorInterval
	if wait <= 0 {
		wait = 2 * time.Second
	}
	c.mu.Lock()
	rt := asMap(c.data["runtime"])
	c.mu.Unlock()
	if rt == nil {
		return wait
	}
	for _, rawTask := range asAnySlice(rt["tasks"]) {
		t := asMap(rawTask)
		if t != nil && asString(t["state"]) == "running" && asString(t["kind"]) == "comfyui_tts_preview" {
			if fast := 350 * time.Millisecond; fast < wait {
				return fast
			}
			return wait
		}
	}
	if comfy := asMap(rt["comfyui"]); comfy != nil {
		if pf := asMap(comfy["tts_preview_prefetch"]); pf != nil && asBool(pf["active"]) {
			if fast := 700 * time.Millisecond; fast < wait {
				return fast
			}
		}
	}
	return wait
}

func (c *ColabOneController) runMonitorLoop() {
	for !c.stop.IsSet() {
		c.MonitorOnce(c.rootCtx)
		if c.stop.Wait(c.effectiveMonitorWait()) {
			return
		}
	}
}
