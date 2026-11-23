package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

// ErrExternalObserverClosed is returned when the active external observer is
// closed or superseded concurrently while starting or sampling telemetry.
var ErrExternalObserverClosed = errors.New("远端实例硬件监控通道已关闭")

// observerBeforeStartHooks allows package-internal concurrency tests to
// deterministically pause a specific provider immediately before rt.Start(ctx).
var observerBeforeStartHooks sync.Map // map[*ColabOneProvider]func()

// ExternalObserverCode is the verbatim read-only hardware sampling script executed
// inside the isolated "colabone-observer" kernel for non-managed Colab runtimes.
// It prefers read-only status from an existing remote ColabOne control service
// (/content/colabone-boot.json -> http://127.0.0.1:7860/api/v1/status) and falls
// back to zero-dependency nvidia-smi + /proc sampling.
const ExternalObserverCode = `
import collections as _co_col
import json as _co_json
import math as _co_math
import os as _co_os
import re as _co_re
import shutil as _co_shutil
import subprocess as _co_sp
import time as _co_time
import urllib.request as _co_http
from pathlib import Path as _co_Path


def _co_observe_once():
    _st = globals().setdefault("_co_obs_state", {})
    _opener = _co_http.build_opener(_co_http.ProxyHandler({}))

    # 1) Prefer read-only status from an existing remote ColabOne control service if present.
    _boot = _co_Path("/content/colabone-boot.json")
    if _boot.is_file():
        try:
            _cfg = _co_json.loads(_boot.read_text(encoding="utf-8"))
            _tok = str(_cfg.get("token") or "")
            if _tok:
                _req = _co_http.Request(
                    "http://127.0.0.1:7860/api/v1/status",
                    headers={"Authorization": "Bearer " + _tok},
                    method="GET",
                )
                with _opener.open(_req, timeout=4) as _resp:
                    _status = _co_json.loads(_resp.read())
                if isinstance(_status, dict) and (
                    "resources" in _status or "gpu" in _status or "state" in _status
                ):
                    _loaded_id = str(_status.get("loaded_model") or "")
                    _model_name = ""
                    if _loaded_id:
                        _names = _st.setdefault("model_names", {})
                        if _loaded_id not in _names:
                            try:
                                _mreq = _co_http.Request(
                                    "http://127.0.0.1:7860/api/v1/models",
                                    headers={"Authorization": "Bearer " + _tok},
                                    method="GET",
                                )
                                with _opener.open(_mreq, timeout=3) as _mresp:
                                    _mlist = _co_json.loads(_mresp.read())
                                if isinstance(_mlist, list):
                                    for _m in _mlist:
                                        if isinstance(_m, dict) and _m.get("id"):
                                            _names[str(_m["id"])] = str(
                                                _m.get("name") or _m["id"]
                                            )
                            except Exception:
                                pass
                        _model_name = str(_names.get(_loaded_id) or _loaded_id)
                    return {
                        "source": "remote_colabone",
                        "status": _status,
                        "loaded_model_name": _model_name,
                    }
        except Exception:
            pass

    # 2) Generic read-only hardware sampling for Colab Web / VS Code / external runtimes.
    def _opt_float(raw):
        if raw is None:
            return None
        s = str(raw).strip()
        if not s or s.startswith("[") or s.lower() in {"n/a", "not supported", "none", "-"}:
            return None
        try:
            v = float(s)
        except ValueError:
            return None
        return v if _co_math.isfinite(v) else None

    def _req_int(raw):
        s = str(raw).strip()
        if not s or s.startswith("["):
            raise ValueError(s)
        v = float(s)
        if not _co_math.isfinite(v) or v < 0:
            raise ValueError(s)
        return int(v)

    def _gpu_info():
        for fields in (
            "name,memory.total,memory.used,utilization.gpu,temperature.gpu,power.draw,power.limit",
            "name,memory.total,memory.used",
        ):
            try:
                res = _co_sp.run(
                    [
                        "nvidia-smi",
                        "--query-gpu=" + fields,
                        "--format=csv,noheader,nounits",
                    ],
                    capture_output=True,
                    text=True,
                    timeout=6,
                    check=True,
                )
                lines = [ln.strip() for ln in (res.stdout or "").splitlines() if ln.strip()]
                if not lines:
                    continue
                parts = [p.strip() for p in lines[0].split(",")]
                if len(parts) < 3:
                    continue
                if len(parts) > 7:
                    parts = [", ".join(parts[:-6]).strip(), *parts[-6:]]
                name = parts[0]
                if not name:
                    continue
                return {
                    "name": name,
                    "total_mib": _req_int(parts[1]),
                    "used_mib": _req_int(parts[2]),
                    "util_percent": _opt_float(parts[3]) if len(parts) > 3 else None,
                    "temp_c": _opt_float(parts[4]) if len(parts) > 4 else None,
                    "power_w": _opt_float(parts[5]) if len(parts) > 5 else None,
                    "power_limit_w": _opt_float(parts[6]) if len(parts) > 6 else None,
                }
            except Exception:
                continue
        return None

    def _mem_info():
        try:
            raw = _co_Path("/proc/meminfo").read_text(encoding="utf-8", errors="replace")
        except OSError:
            return {"total_bytes": 0, "used_bytes": 0, "available_bytes": 0, "percent": 0.0}
        fields = {}
        for line in raw.splitlines():
            if ":" not in line:
                continue
            k, rest = line.split(":", 1)
            k = k.strip()
            if k not in {"MemTotal", "MemAvailable", "MemFree", "Buffers", "Cached"}:
                continue
            toks = rest.strip().split()
            if toks:
                try:
                    fields[k] = max(0, int(toks[0]))
                except ValueError:
                    pass
        total = fields.get("MemTotal", 0) * 1024
        if "MemAvailable" in fields:
            avail = fields["MemAvailable"] * 1024
        else:
            avail = (
                fields.get("MemFree", 0)
                + fields.get("Buffers", 0)
                + fields.get("Cached", 0)
            ) * 1024
        used = max(0, total - avail)
        pct = round((used / total) * 100.0, 1) if total > 0 else 0.0
        return {
            "total_bytes": int(total),
            "used_bytes": int(used),
            "available_bytes": int(avail),
            "percent": float(pct),
        }

    def _read_cpu():
        try:
            raw = _co_Path("/proc/stat").read_text(encoding="utf-8", errors="replace")
        except OSError:
            return None
        for line in raw.splitlines():
            parts = line.split()
            if parts and parts[0] == "cpu" and len(parts) >= 5:
                try:
                    vals = [int(t) for t in parts[1:]]
                except ValueError:
                    return None
                return (sum(vals[:8]), vals[3] + (vals[4] if len(vals) > 4 else 0))
        return None

    def _cpu_info():
        cores = int(_co_os.cpu_count() or 1)
        cur = _read_cpu()
        if cur is None:
            return {"cores": cores, "percent": 0.0}
        prev = _st.get("prev_cpu")
        if prev is None:
            _co_time.sleep(0.15)
            nxt = _read_cpu()
            if nxt is not None:
                prev, cur = cur, nxt
        _st["prev_cpu"] = cur
        if prev is None:
            return {"cores": cores, "percent": 0.0}
        dt = cur[0] - prev[0]
        di = cur[1] - prev[1]
        pct = 0.0 if dt <= 0 else min(100.0, max(0.0, (max(0, dt - max(0, di)) / dt) * 100.0))
        return {"cores": cores, "percent": float(round(pct, 1))}

    _disk_re = _co_re.compile(r"^(nvme\d+n\d+|sd[a-z]+|vd[a-z]+|xvd[a-z]+|mmcblk\d+)$")

    def _disk_info(ts):
        total = used = free = 0
        for p in ("/content", "/"):
            try:
                u = _co_shutil.disk_usage(p)
                total, used, free = int(u.total), int(u.used), int(u.free)
                break
            except OSError:
                continue
        pct = round((used / total) * 100.0, 1) if total > 0 else 0.0
        read_bps = write_bps = 0.0
        try:
            raw = _co_Path("/proc/diskstats").read_text(encoding="utf-8", errors="replace")
            rs = ws = 0
            for line in raw.splitlines():
                parts = line.split()
                if len(parts) >= 10 and _disk_re.match(parts[2]):
                    try:
                        rs += max(0, int(parts[5]))
                        ws += max(0, int(parts[9]))
                    except ValueError:
                        pass
            rb, wb = rs * 512, ws * 512
            prev = _st.get("prev_disk")
            _st["prev_disk"] = (ts, rb, wb)
            if prev is not None and ts > prev[0]:
                dt = ts - prev[0]
                read_bps = max(0, rb - prev[1]) / dt
                write_bps = max(0, wb - prev[2]) / dt
        except OSError:
            pass
        return {
            "total_bytes": total,
            "used_bytes": used,
            "free_bytes": free,
            "percent": float(pct),
            "read_bps": float(round(read_bps, 1)),
            "write_bps": float(round(write_bps, 1)),
        }

    ts = _co_time.time()
    gpu = _gpu_info()
    mem = _mem_info()
    cpu = _cpu_info()
    disk = _disk_info(ts)
    if "zone" not in _st:
        _zone = ""
        try:
            _zreq = _co_http.Request(
                "http://metadata.google.internal/computeMetadata/v1/instance/zone",
                headers={"Metadata-Flavor": "Google"},
            )
            with _opener.open(_zreq, timeout=0.8) as _zr:
                if _zr.status == 200:
                    _zone = _zr.read().decode("utf-8", "replace").strip()
        except Exception:
            pass
        _st["zone"] = _zone

    hist = _st.get("history")
    if hist is None:
        hist = _co_col.deque(maxlen=60)
        _st["history"] = hist
    vram_pct = (
        float(round((max(0, gpu["used_mib"]) / gpu["total_mib"]) * 100.0, 1))
        if isinstance(gpu, dict) and (gpu.get("total_mib") or 0) > 0
        else 0.0
    )
    last_t = float(_st.get("last_t") or 0.0)
    if not hist or (ts - last_t) >= 0.9:
        hist.append(
            {
                "t": float(round(ts, 1)),
                "vram_pct": vram_pct,
                "gpu_util": float(gpu.get("util_percent") or 0.0) if isinstance(gpu, dict) else 0.0,
                "ram_pct": float(mem["percent"]),
                "cpu_pct": float(cpu["percent"]),
                "disk_pct": float(disk["percent"]),
                "disk_read_bps": float(disk["read_bps"]),
                "disk_write_bps": float(disk["write_bps"]),
            }
        )
        _st["last_t"] = ts

    resources = {
        "cpu": cpu,
        "memory": mem,
        "disk": disk,
        "history": [dict(pt) for pt in hist],
    }
    if isinstance(gpu, dict):
        resources["gpu"] = gpu
    return {
        "source": "remote_readonly",
        "resources": resources,
        "gpu": gpu,
        "disk_free": disk["free_bytes"],
        "zone": _st.get("zone", ""),
    }


print("\nCOLABONE_OBS=" + _co_json.dumps(_co_observe_once(), ensure_ascii=False), flush=True)
`

type cachedExternalProxy struct {
	url       string
	token     string
	expiresAt time.Time
}

type observerSlot struct {
	endpoint  string
	key       kernelKey
	kernelID  string
	sessionID string
	runtime   KernelClient
}

type observerStopItem struct {
	rt       KernelClient
	shutdown bool
}

func (p *ColabOneProvider) cacheAssignmentProxies(assignments []ListedAssignment) {
	now := p.now()
	fresh := make(map[string]cachedExternalProxy, len(assignments))
	for _, a := range assignments {
		if a.Endpoint == "" || a.RuntimeProxyInfo == nil {
			continue
		}
		if a.RuntimeProxyInfo.URL == "" || a.RuntimeProxyInfo.Token == "" {
			continue
		}
		exp := a.RuntimeProxyInfo.ResolveExpiresAt(now)
		if exp.IsZero() {
			exp = now.Add(1800 * time.Second)
		}
		fresh[a.Endpoint] = cachedExternalProxy{
			url:       a.RuntimeProxyInfo.URL,
			token:     a.RuntimeProxyInfo.Token,
			expiresAt: exp,
		}
	}
	p.proxyMu.Lock()
	p.externalProxies = fresh
	p.proxyMu.Unlock()
}

func (p *ColabOneProvider) evictExternalProxy(endpoint string) {
	p.proxyMu.Lock()
	delete(p.externalProxies, endpoint)
	p.proxyMu.Unlock()
}

func (p *ColabOneProvider) externalProxyInfo(ctx context.Context, endpoint string, refresh bool) (*cachedExternalProxy, error) {
	if endpoint == "" {
		return nil, nil
	}
	now := p.now()
	if !refresh {
		p.proxyMu.Lock()
		cached, ok := p.externalProxies[endpoint]
		p.proxyMu.Unlock()
		if ok && cached.expiresAt.After(now.Add(60*time.Second)) {
			cp := cached
			return &cp, nil
		}
	}
	client, err := p.regularClient()
	if err != nil {
		return nil, err
	}
	assignments, err := client.ListAssignments(ctx)
	if err != nil {
		return nil, err
	}
	p.cacheAssignmentProxies(assignments)
	found := false
	for _, a := range assignments {
		if a.Endpoint == endpoint {
			found = true
			break
		}
	}
	if !found {
		return nil, nil
	}
	p.proxyMu.Lock()
	cached, ok := p.externalProxies[endpoint]
	p.proxyMu.Unlock()
	if !ok {
		return nil, nil
	}
	cp := cached
	return &cp, nil
}

// isObserverSlotActive reports whether slot is still the active published observer
// at generation obsGen and the provider has not been closed.
func (p *ColabOneProvider) isObserverSlotActive(slot *observerSlot, obsGen uint64) bool {
	p.observerMu.Lock()
	active := !p.closed.Load() && slot != nil && p.observer == slot && p.observerGen == obsGen
	p.observerMu.Unlock()
	return active
}

// CloseObserver closes the isolated read-only external observer kernel (if any).
// When endpoint is non-empty, only closes the observer if it currently targets that endpoint.
func (p *ColabOneProvider) CloseObserver(endpoint string, shutdownKernel bool) {
	p.observerMu.Lock()
	obs := p.observer
	if obs == nil {
		// Advance observerGen when closing all observers or when targeting the
		// exact endpoint currently transitioning in ObserveExternalTelemetry,
		// without falsely invalidating a transition for an unrelated endpoint.
		if endpoint == "" || (p.observerPendingEndpoint != "" && endpoint == p.observerPendingEndpoint) {
			p.observerGen++
		}
		p.observerMu.Unlock()
		return
	}
	if endpoint != "" && obs.endpoint != endpoint {
		p.observerMu.Unlock()
		return
	}
	p.observer = nil
	p.observerGen++
	rt := obs.runtime
	p.observerMu.Unlock()

	if rt != nil {
		rt.Abort()
		_ = rt.Stop(context.Background(), shutdownKernel)
	}
}

// ObserveExternalTelemetry performs read-only hardware observation for an external
// (non-managed) Colab runtime using a dedicated isolated observer kernel.
// It never adopts the runtime into vault, never writes files, and never sends keep-alive pings.
func (p *ColabOneProvider) ObserveExternalTelemetry(ctx context.Context, endpoint string) (map[string]any, error) {
	if endpoint == "" {
		return nil, ErrMissingEndpoint
	}
	if p.closed.Load() {
		return nil, ErrExternalObserverClosed
	}
	proxy, err := p.externalProxyInfo(ctx, endpoint, false)
	if err != nil {
		return nil, err
	}
	if proxy == nil {
		p.CloseObserver(endpoint, false)
		return nil, ErrExternalEndpointGone
	}
	if p.kernelFactory == nil {
		return nil, errors.New("未配置远端内核客户端工厂")
	}

	p.observerExecMu.Lock()
	defer p.observerExecMu.Unlock()

	var toStop []observerStopItem
	var obs *observerSlot
	var rt KernelClient
	var obsGen uint64
	var baseGen uint64
	needStart := false
	pendingSet := false

	key := kernelKey{endpoint: endpoint, url: proxy.url, token: proxy.token}
	prevKID := ""
	prevSID := ""

	p.observerMu.Lock()
	if p.closed.Load() {
		p.observerMu.Unlock()
		return nil, ErrExternalObserverClosed
	}
	cur := p.observer
	if cur != nil && cur.endpoint != endpoint {
		if cur.runtime != nil {
			toStop = append(toStop, observerStopItem{rt: cur.runtime, shutdown: true})
		}
		p.observer = nil
		p.observerGen++
		cur = nil
	} else if cur != nil && cur.key != key {
		// Proxy token rotated for the same observed endpoint: keep our own
		// observer kernel_id so we reconnect instead of spawning duplicates.
		prevKID = cur.kernelID
		prevSID = cur.sessionID
		if cur.runtime != nil {
			toStop = append(toStop, observerStopItem{rt: cur.runtime, shutdown: false})
		}
		p.observer = nil
		p.observerGen++
		cur = nil
	}
	if cur != nil {
		obs = cur
		rt = cur.runtime
		obsGen = p.observerGen
	} else {
		baseGen = p.observerGen
		p.observerPendingEndpoint = endpoint
		pendingSet = true
	}
	p.observerMu.Unlock()

	if pendingSet {
		defer func() {
			if pendingSet {
				p.observerMu.Lock()
				if p.observerPendingEndpoint == endpoint {
					p.observerPendingEndpoint = ""
				}
				p.observerMu.Unlock()
			}
		}()
	}

	// Stop any superseded observer runtime BEFORE constructing or publishing a
	// new slot, so an unstarted runtime is never exposed across a synchronous
	// network DELETE.
	for _, item := range toStop {
		_ = item.rt.Stop(context.Background(), item.shutdown)
	}

	stopDeferredShutdown := func() {
		for _, item := range toStop {
			if !item.shutdown && item.rt != nil {
				_ = item.rt.Stop(context.Background(), true)
			}
		}
	}

	if obs == nil {
		if err := ctx.Err(); err != nil {
			stopDeferredShutdown()
			return nil, err
		}

		p.observerMu.Lock()
		invalidated := p.closed.Load() || p.observer != nil || p.observerGen != baseGen
		if invalidated && p.observerPendingEndpoint == endpoint {
			p.observerPendingEndpoint = ""
			pendingSet = false
		}
		p.observerMu.Unlock()
		if invalidated {
			stopDeferredShutdown()
			return nil, ErrExternalObserverClosed
		}

		slot := &observerSlot{
			endpoint:  endpoint,
			key:       key,
			kernelID:  prevKID,
			sessionID: prevSID,
		}
		newRT := p.kernelFactory(KernelOptions{
			ServerURL:           proxy.url,
			ProxyToken:          proxy.token,
			SessionName:         "colabone-observer",
			KernelID:            prevKID,
			SessionID:           prevSID,
			ShutdownOnWSTimeout: true,
			OnKernelStarted: func(val string) {
				p.observerMu.Lock()
				slot.kernelID = val
				p.observerMu.Unlock()
			},
			OnSessionStarted: func(val string) {
				p.observerMu.Lock()
				slot.sessionID = val
				p.observerMu.Unlock()
			},
		})
		if newRT == nil {
			stopDeferredShutdown()
			return nil, errors.New("未配置远端内核客户端工厂")
		}
		slot.runtime = newRT

		p.observerMu.Lock()
		if p.closed.Load() || p.observer != nil || p.observerGen != baseGen {
			if p.observerPendingEndpoint == endpoint {
				p.observerPendingEndpoint = ""
			}
			pendingSet = false
			p.observerMu.Unlock()
			stopDeferredShutdown()
			_ = newRT.Stop(context.Background(), true)
			return nil, ErrExternalObserverClosed
		}
		p.observer = slot
		p.observerPendingEndpoint = ""
		pendingSet = false
		p.observerGen++
		obs = slot
		rt = newRT
		obsGen = p.observerGen
		needStart = true
		p.observerMu.Unlock()
	}

	cleanupOnError := func() {
		p.evictExternalProxy(endpoint)
		p.observerMu.Lock()
		if p.observer == obs && p.observerGen == obsGen {
			p.observer = nil
			p.observerGen++
		}
		p.observerMu.Unlock()
		if rt != nil {
			_ = rt.Stop(context.Background(), true)
		}
	}

	if needStart {
		if hook, ok := observerBeforeStartHooks.Load(p); ok {
			hook.(func())()
		}
		if !p.isObserverSlotActive(obs, obsGen) {
			_ = rt.Stop(context.Background(), true)
			return nil, ErrExternalObserverClosed
		}
		if err := rt.Start(ctx); err != nil {
			invalidated := !p.isObserverSlotActive(obs, obsGen)
			cleanupOnError()
			if invalidated {
				return nil, ErrExternalObserverClosed
			}
			return nil, err
		}
		if !p.isObserverSlotActive(obs, obsGen) {
			_ = rt.Stop(context.Background(), true)
			return nil, ErrExternalObserverClosed
		}
	} else if !p.isObserverSlotActive(obs, obsGen) {
		if rt != nil {
			_ = rt.Stop(context.Background(), true)
		}
		return nil, ErrExternalObserverClosed
	}

	outputs, err := rt.ExecuteCode(ctx, ExternalObserverCode, ExecuteOptions{
		Timeout:    15 * time.Second,
		AllowStdin: false,
	})
	if err != nil {
		invalidated := !p.isObserverSlotActive(obs, obsGen)
		cleanupOnError()
		if invalidated {
			return nil, ErrExternalObserverClosed
		}
		return nil, err
	}
	if !p.isObserverSlotActive(obs, obsGen) {
		_ = rt.Stop(context.Background(), true)
		return nil, ErrExternalObserverClosed
	}

	// Capture kernel/session IDs if the KernelClient exposes them directly without firing callbacks.
	if kid := rt.KernelID(); kid != "" {
		p.observerMu.Lock()
		if obs.kernelID == "" {
			obs.kernelID = kid
		}
		p.observerMu.Unlock()
	}
	if sid := rt.SessionID(); sid != "" {
		p.observerMu.Lock()
		if obs.sessionID == "" {
			obs.sessionID = sid
		}
		p.observerMu.Unlock()
	}

	stdoutCombined := combineStdout(outputs)
	var payload map[string]any
	for _, line := range splitAllLines(stdoutCombined) {
		stripped := strings.TrimSpace(line)
		if strings.HasPrefix(stripped, "COLABONE_OBS=") {
			rawJSON := strings.TrimPrefix(stripped, "COLABONE_OBS=")
			var parsed map[string]any
			if err := json.Unmarshal([]byte(rawJSON), &parsed); err == nil && parsed != nil {
				payload = parsed
			} else {
				payload = nil
			}
		}
	}
	if payload == nil {
		return nil, errors.New("远端实例未返回硬件监控采样数据")
	}

	source := strVal(payload["source"])
	if source == "" {
		source = "remote_readonly"
	}

	if source == "remote_colabone" {
		if status, ok := payload["status"].(map[string]any); ok {
			drive, _ := status["drive"].(map[string]any)
			var loc map[string]any
			if statusLoc, ok := status["location"].(map[string]any); ok && len(statusLoc) > 0 {
				loc = cloneMap(statusLoc)
			} else {
				loc = p.inferLocationFromEndpoint(endpoint, proxy.url)
			}
			rtData := map[string]any{
				"id":    endpoint,
				"state": "ready",
			}
			var gpu map[string]any
			if g, ok := status["gpu"].(map[string]any); ok {
				gpu = g
			} else if resMap, ok := status["resources"].(map[string]any); ok {
				if g, ok := resMap["gpu"].(map[string]any); ok {
					gpu = g
				}
			}
			if gpu != nil {
				rtData["gpu"] = gpu
			}
			if resMap, ok := status["resources"].(map[string]any); ok {
				rtData["resources"] = resMap
			}
			if df, exists := status["disk_free"]; exists && df != nil {
				rtData["disk_free"] = df
			}
			if loadedModel := strVal(status["loaded_model"]); loadedModel != "" {
				rtData["loaded_model"] = loadedModel
			}
			if len(drive) > 0 {
				rtData["drive"] = drive
			}
			if len(loc) > 0 {
				rtData["location"] = loc
			}
			if activeEngine, exists := status["active_engine"]; exists && activeEngine != nil && activeEngine != "" {
				rtData["active_engine"] = activeEngine
			}

			loadedName := strVal(payload["loaded_model_name"])
			if loadedName == "" {
				loadedName = strVal(status["loaded_model"])
			}
			var driveFree any
			if drive != nil {
				driveFree = drive["free_bytes"]
			}
			return map[string]any{
				"endpoint":          endpoint,
				"source":            "remote_colabone",
				"runtime":           rtData,
				"loaded_model_name": loadedName,
				"drive_connected":   boolVal(drive["connected"]),
				"drive_free_bytes":  driveFree,
			}, nil
		}
	}

	resources, ok := payload["resources"].(map[string]any)
	if !ok || resources == nil {
		resources = map[string]any{}
	}
	gpu, _ := payload["gpu"].(map[string]any)
	zone := strVal(payload["zone"])
	loc := p.locationFromZone(zone)
	if len(loc) == 0 {
		loc = p.inferLocationFromEndpoint(endpoint, proxy.url)
	}
	rtData := map[string]any{
		"id":        endpoint,
		"state":     "ready",
		"resources": resources,
	}
	if gpu != nil {
		rtData["gpu"] = gpu
	}
	if df, exists := payload["disk_free"]; exists && df != nil {
		rtData["disk_free"] = df
	}
	if len(loc) > 0 {
		rtData["location"] = loc
	}
	return map[string]any{
		"endpoint":          endpoint,
		"source":            "remote_readonly",
		"runtime":           rtData,
		"loaded_model_name": "",
		"drive_connected":   false,
		"drive_free_bytes":  nil,
	}, nil
}
