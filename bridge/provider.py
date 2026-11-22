"""Colab allocation/heartbeat adapted from Colab-yoga (Apache-2.0).
Only sessions stored by this ColabOne profile may be operated on automatically.
"""

from __future__ import annotations
import base64
import inspect
import json
import secrets
import tempfile
import time
import uuid
import threading
import zipfile
from pathlib import Path
from urllib.parse import quote

import jupyter_kernel_client
import requests
from google.auth.exceptions import TransportError
from google.auth.transport.requests import AuthorizedSession
from colab_cli.client import (
    Client,
    Prod,
    Accelerator,
    Assignment,
    Variant,
    Shape,
    resolve_assign_shape,
    ColabRequestError,
    TooManyAssignmentsError,
)
from colab_cli.state import SessionState
from colab_cli.contents import ContentsClient
from colab_cli.runtime import ColabRuntime
from bridge.activity import safe_message
from bridge.drive_mount import ColabOneDriveMount
from common.files import (
    COLABONE_SERVICE_BUNDLE_FOLDERS,
    COLABONE_SERVICE_BUNDLE_SUFFIXES,
)
from common.location import (
    infer_location_from_endpoint,
    location_from_zone,
    normalize_geo_payload,
)

_REAL_COLAB_RUNTIME = ColabRuntime
_REAL_CONTENTS_CLIENT = ContentsClient
_COLABONE_WS_PING_INTERVAL = 15
_COLABONE_WS_CONNECT_TIMEOUT = 25
# Google Colab's /tun/m/assign gateway enforces a ~30s RPC deadline and returns
# HTTP 503 when provisioning high-tier GPUs (such as G4) takes longer or fails
# over across regions. Retry up to 10 total attempts (1 initial + 9 retries)
# using capped exponential backoff: 2s -> 4s -> 8s -> 16s -> 30s (capped).
_COLABONE_ASSIGN_RETRY_DELAYS = (2, 4, 8, 16, 30, 30, 30, 30, 30)


def _format_assign_error(
    gpu_label: str, exc: Exception, retry_count: int = 0
) -> str:
    if isinstance(exc, TooManyAssignmentsError):
        return (
            f"Google Colab 拒绝分配 {gpu_label} GPU（HTTP 412）："
            "当前账号活跃实例已达上限或受配额限制，请先释放已有实例或切换其他 GPU 型号"
        )
    if isinstance(exc, (requests.exceptions.Timeout, TimeoutError)):
        if retry_count > 0:
            return (
                f"Google Colab 分配 {gpu_label} GPU 请求超时，"
                f"已自动重试 {retry_count} 次仍未成功，请稍后重试或切换其他 GPU 型号"
            )
        return (
            f"Google Colab 分配 {gpu_label} GPU 请求超时，请稍后重试或切换其他 GPU 型号"
        )
    if isinstance(exc, (requests.RequestException, TransportError)):
        if retry_count > 0:
            return (
                f"Google Colab 分配 {gpu_label} GPU 网络连接中断，"
                f"已自动重试 {retry_count} 次仍未成功，请检查网络或代理后重试"
            )
        return (
            f"Google Colab 分配 {gpu_label} GPU 失败，请检查网络连接后重试"
        )
    if isinstance(exc, ColabRequestError):
        status = int(getattr(getattr(exc, "response", None), "status_code", 0) or 0)
        if status == 412:
            return (
                f"Google Colab 拒绝分配 {gpu_label} GPU（HTTP 412）："
                "当前账号活跃实例已达上限或受配额限制，请先释放已有实例或切换其他 GPU 型号"
            )
        if status == 400:
            return (
                f"Google Colab 拒绝分配 {gpu_label} GPU（HTTP 400）："
                "当前账号可能暂无该型号配额或权限，请切换其他 GPU 型号"
            )
        if status in (401, 403):
            return (
                f"Google Colab 授权校验失败（HTTP {status}），请在设置中重新登录后重试"
            )
        if status == 429:
            return "Google Colab 分配请求过于频繁（HTTP 429），请稍等片刻后重试"
        if status in (502, 503, 504):
            if retry_count > 0:
                return (
                    f"Google Colab 分配 {gpu_label} GPU 暂时不可用（HTTP {status}，"
                    f"该型号可能算力紧张或后端分配超时），已自动重试 {retry_count} 次仍未成功，"
                    "请稍后重试或切换其他 GPU 型号"
                )
            return (
                f"Google Colab 分配 {gpu_label} GPU 暂时不可用（HTTP {status}，"
                "该型号可能算力紧张或后端分配超时），请稍后重试或切换其他 GPU 型号"
            )
        if status > 0:
            return f"Google Colab 分配 {gpu_label} GPU 失败（HTTP {status}），请稍后重试"
        return f"Google Colab 分配 {gpu_label} GPU 失败，请检查网络连接后重试"
    return safe_message(exc)[:300]


def _is_transient_ws_error(exc: Exception) -> bool:
    msg = str(exc).lower()
    return any(
        needle in msg
        for needle in (
            "connection was lost",
            "websocket",
            "connection closed",
            "broken pipe",
            "connection reset",
            "remote end closed",
            "unexpected eof",
            "handshake",
            "timed out",
        )
    )


class ColabOneKernelRuntime(ColabRuntime):
    """ColabRuntime with responsive WebSocket keepalive, deadline guard, and cleanup."""

    def __init__(self, *args, shutdown_on_ws_timeout: bool = False, **kwargs):
        super().__init__(*args, **kwargs)
        self._shutdown_on_ws_timeout = shutdown_on_ws_timeout

    def _install_ws_wakeup(self, ws_client) -> None:
        if ws_client is None:
            return
        orig_on_close = getattr(ws_client, "_on_close", None)
        msg_evt = getattr(ws_client, "_message_received", None)
        if callable(orig_on_close) and msg_evt is not None:

            def _waking_on_close(*args, **kwargs):
                try:
                    return orig_on_close(*args, **kwargs)
                finally:
                    msg_evt.set()

            ws_client._on_close = _waking_on_close
            sock = getattr(ws_client, "kernel_socket", None)
            if sock is not None:
                sock.on_close = _waking_on_close

    @property
    def kernel_client(self):
        if not self._kernel_client:
            retries = 3
            backoff = 2
            last_err = None

            for i in range(retries):
                client = None
                try:
                    client_kwargs = {
                        "subprotocol": jupyter_kernel_client.JupyterSubprotocol.DEFAULT,
                        "extra_params": {"colab-runtime-proxy-token": self.token},
                        "ping_interval": _COLABONE_WS_PING_INTERVAL,
                        "timeout": _COLABONE_WS_CONNECT_TIMEOUT,
                    }
                    if self.session_id:
                        client_kwargs["session"] = self.session_id

                    if hasattr(jupyter_kernel_client, "ColabKernelClient"):
                        _ClientCls = jupyter_kernel_client.ColabKernelClient
                        _token_kw = "proxy_token"
                    elif hasattr(jupyter_kernel_client, "KernelClient"):
                        _ClientCls = jupyter_kernel_client.KernelClient
                        _token_kw = "token"
                    else:
                        _ClientCls = jupyter_kernel_client.JupyterKernelClient
                        _token_kw = "token"

                    client = _ClientCls(
                        server_url=self.url,
                        **{_token_kw: self.token},
                        kernel_id=self.kernel_id,
                        client_kwargs=client_kwargs,
                        headers={
                            "X-Colab-Client-Agent": "colab-cli",
                            "X-Colab-Runtime-Proxy-Token": self.token,
                        },
                    )
                    client._own_kernel = False
                    client.start()

                    # Guard against jupyter_kernel_client.wsclient.start_channels()
                    # returning silently when connection_ready.wait() times out.
                    ws_client = getattr(getattr(client, "_manager", None), "client", None)
                    ready_evt = getattr(ws_client, "connection_ready", None)
                    if ready_evt is not None and not ready_evt.is_set():
                        started_id = getattr(client, "id", None)
                        try:
                            ws_client.stop_channels()
                        except Exception:
                            pass
                        if self._shutdown_on_ws_timeout and started_id:
                            try:
                                client._manager.shutdown_kernel(now=True)
                            except Exception:
                                pass
                        elif started_id and not self.kernel_id:
                            # Reuse the already-created managed kernel on retry instead of leaking duplicates.
                            self.kernel_id = started_id
                            if self.on_kernel_started:
                                self.on_kernel_started(self.kernel_id)
                        raise ConnectionError("Colab WebSocket connection timed out")

                    self._kernel_client = client
                    self._install_ws_wakeup(ws_client)
                    self._apply_ws_hook()

                    if self._kernel_client.id and self._kernel_client.id != self.kernel_id:
                        self.kernel_id = self._kernel_client.id
                        if self.on_kernel_started:
                            self.on_kernel_started(self.kernel_id)

                    if (
                        not self.session_id
                        and self._kernel_client._manager.client.session.session
                    ):
                        self.session_id = (
                            self._kernel_client._manager.client.session.session
                        )
                        if self.on_session_started:
                            self.on_session_started(self.session_id)
                    break
                except (
                    requests.exceptions.ReadTimeout,
                    requests.exceptions.ConnectTimeout,
                    ConnectionError,
                    OSError,
                ) as e:
                    last_err = e
                    self._kernel_client = None
                    if i < retries - 1:
                        time.sleep(backoff ** (i + 1))
                    else:
                        raise last_err
                except requests.HTTPError as e:
                    status = int(getattr(getattr(e, "response", None), "status_code", 0) or 0)
                    last_err = e
                    self._kernel_client = None
                    if status in (502, 503, 504) and i < retries - 1:
                        time.sleep(backoff ** (i + 1))
                        continue
                    raise
                except Exception:
                    self._kernel_client = None
                    raise

        return self._kernel_client

    def execute_code(
        self,
        code: str,
        allow_stdin: bool = False,
        stdin_hook=None,
        output_hook=None,
        timeout=None,
    ):
        # jupyter_kernel_client.wsclient.execute_interactive() has a known
        # busy-loop bug when its deadline expires without an idle IOPub frame.
        # Enforce a hard wall-clock timer that stops channels and wakes the
        # waiter so a stalled kernel can never spin at 100% CPU indefinitely.
        timed_out = [False]
        timer = None
        if timeout is not None and float(timeout) > 0:

            def _abort_on_timeout():
                timed_out[0] = True
                kc = self._kernel_client
                ws_client = getattr(getattr(kc, "_manager", None), "client", None)
                if ws_client is not None:
                    try:
                        ws_client.stop_channels()
                    except Exception:
                        pass
                    evt = getattr(ws_client, "_message_received", None)
                    if evt is not None:
                        evt.set()

            timer = threading.Timer(float(timeout) + 2.0, _abort_on_timeout)
            timer.daemon = True
            timer.start()
        try:
            res = super().execute_code(
                code,
                allow_stdin=allow_stdin,
                stdin_hook=stdin_hook,
                output_hook=output_hook,
                timeout=timeout,
            )
            if timed_out[0]:
                raise TimeoutError("Colab 远端内核执行超时")
            return res
        except Exception:
            if timed_out[0]:
                raise TimeoutError("Colab 远端内核执行超时") from None
            raise
        finally:
            if timer is not None:
                timer.cancel()

    def stop(self, shutdown_kernel: bool = False):
        try:
            super().stop(shutdown_kernel=shutdown_kernel)
        finally:
            self._kernel_client = None


def _create_kernel_runtime(*args, shutdown_on_ws_timeout: bool = False, **kwargs):
    if ColabRuntime is _REAL_COLAB_RUNTIME:
        return ColabOneKernelRuntime(
            *args, shutdown_on_ws_timeout=shutdown_on_ws_timeout, **kwargs
        )
    return ColabRuntime(*args, **kwargs)


def describe_assignment(assignment, managed_set: set[str], active_ep: str | None) -> dict:
    endpoint = assignment.endpoint
    raw_variant = getattr(assignment, "variant", None)
    if raw_variant is None:
        variant = ""
    else:
        v_name = getattr(raw_variant, "name", str(raw_variant)).upper()
        if "GPU" in v_name or raw_variant == 1:
            variant = "GPU"
        elif "TPU" in v_name or raw_variant == 2:
            variant = "TPU"
        else:
            variant = "CPU"
    raw_accel = getattr(assignment, "accelerator", None)
    accel = (
        getattr(raw_accel, "value", None)
        or getattr(raw_accel, "name", None)
        or str(raw_accel or "")
    ).upper()
    if accel in ("", "NONE"):
        accel = "CPU" if variant == "CPU" else variant
    raw_shape = getattr(assignment, "machine_shape", None)
    if raw_shape is None:
        shape = ""
    else:
        s_name = getattr(raw_shape, "name", str(raw_shape)).upper()
        shape = "HIGH_RAM" if ("HIGH" in s_name or raw_shape == 1) else "STANDARD"
    proxy_url = str(getattr(getattr(assignment, "runtime_proxy_info", None), "url", "") or "")
    loc = infer_location_from_endpoint(endpoint, proxy_url)
    info = {
        "id": endpoint,
        "managed": endpoint in managed_set,
        "active": endpoint == active_ep,
        "accelerator": accel,
        "variant": variant,
        "shape": shape,
    }
    if loc:
        info["location"] = loc
    return info


def _accepts_kwarg(fn, name: str) -> bool:
    try:
        params = inspect.signature(fn).parameters
        return name in params or any(
            p.kind == inspect.Parameter.VAR_KEYWORD for p in params.values()
        )
    except (TypeError, ValueError):
        return False


def _emit_log(on_log, message: str, source: str = "GPU", level: str = "info") -> None:
    if not callable(on_log) or not message:
        return
    try:
        on_log(message, source, level)
    except TypeError:
        on_log(message)


def _format_location_short(loc: dict | None) -> str:
    if not isinstance(loc, dict):
        return ""
    country = str(loc.get("country_name") or loc.get("country") or "").strip()
    region = str(loc.get("region") or "").strip()
    if country and region and region != country:
        return f"{country} · {region}"
    return country or region


_EXTERNAL_OBSERVER_CODE = r"""
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
"""


class ColabOneProvider:
    _assign_retry_delays = _COLABONE_ASSIGN_RETRY_DELAYS

    def __init__(self, auth, vault, root):
        self.auth, self.vault, self.root = auth, vault, root
        self.kernel_lock = threading.RLock()
        self.state_lock = threading.RLock()
        self._kernels: dict[str, dict] = {}
        self._observer_lock = threading.RLock()
        self._proxy_lock = threading.RLock()
        self._observer: dict | None = None
        self._observer_gen = 0
        self._external_proxies: dict[str, dict] = {}
        self._loc_probed: set[str] = set()
        self.client_factory = lambda: Client(
            Prod(), AuthorizedSession(auth.credentials("colab"))
        )

    @property
    def kernel_runtime(self):
        ep = self.active_endpoint()
        return self._kernels.get(ep or "", {}).get("runtime")

    def _kernel_slot(self, endpoint: str) -> dict:
        with self.kernel_lock:
            slot = self._kernels.get(endpoint)
            if slot is None:
                slot = {"lock": threading.RLock(), "key": None, "runtime": None}
                self._kernels[endpoint] = slot
            return slot

    def _sessions_map(self) -> dict[str, dict]:
        with self.state_lock:
            sessions: dict[str, dict] = {}
            ids = self.vault.get("runtime_session_ids")
            if isinstance(ids, list):
                for ep in ids:
                    if not isinstance(ep, str) or not ep:
                        continue
                    item = self.vault.get(f"runtime_session:{ep}")
                    if isinstance(item, dict) and item.get("endpoint"):
                        sessions[item["endpoint"]] = item
            raw_map = self.vault.get("runtime_sessions")
            if isinstance(raw_map, dict):
                for ep, item in raw_map.items():
                    if isinstance(item, dict) and item.get("endpoint"):
                        sessions.setdefault(item["endpoint"], item)
            legacy = self.vault.get("runtime_session")
            if isinstance(legacy, dict) and legacy.get("endpoint"):
                sessions.setdefault(legacy["endpoint"], legacy)
            return sessions

    def _remotes_map(self) -> dict[str, dict]:
        with self.state_lock:
            remotes: dict[str, dict] = {}
            ids = self.vault.get("remote_ids")
            if isinstance(ids, list):
                for ep in ids:
                    if not isinstance(ep, str) or not ep:
                        continue
                    item = self.vault.get(f"remote:{ep}")
                    if isinstance(item, dict) and item.get("endpoint"):
                        remotes[item["endpoint"]] = item
            raw_map = self.vault.get("remotes")
            if isinstance(raw_map, dict):
                for ep, item in raw_map.items():
                    if isinstance(item, dict) and item.get("endpoint"):
                        remotes.setdefault(item["endpoint"], item)
            legacy = self.vault.get("remote")
            if isinstance(legacy, dict) and legacy.get("endpoint"):
                remotes.setdefault(legacy["endpoint"], legacy)
            return remotes

    def _write_state(
        self,
        sessions: dict[str, dict],
        remotes: dict[str, dict],
        active_ep: str | None = None,
    ):
        with self.state_lock:
            prev_session_ids = self.vault.get("runtime_session_ids")
            prev_remote_ids = self.vault.get("remote_ids")
            # Store each session and remote under its own per-endpoint key so
            # Windows Credential Manager's 2,560-byte UTF-16 CredWriteW limit is
            # never exceeded when running multiple concurrent GPUs.
            if sessions:
                chosen = (
                    active_ep
                    if active_ep in sessions
                    else (
                        self.vault.get("active_endpoint")
                        if self.vault.get("active_endpoint") in sessions
                        else next(iter(sessions))
                    )
                )
                for ep, item in sessions.items():
                    self.vault.set(f"runtime_session:{ep}", item)
                self.vault.set("runtime_session_ids", list(sessions.keys()))
                self.vault.set("active_endpoint", chosen)
                self.vault.set("runtime_session", sessions[chosen])
                self.vault.delete("runtime_sessions")
            else:
                chosen = None
                self.vault.delete("runtime_session_ids")
                self.vault.delete("runtime_sessions")
                self.vault.delete("active_endpoint")
                self.vault.delete("runtime_session")
            if isinstance(prev_session_ids, list):
                for old_ep in prev_session_ids:
                    if isinstance(old_ep, str) and old_ep not in sessions:
                        self.vault.delete(f"runtime_session:{old_ep}")

            if remotes:
                for ep, item in remotes.items():
                    self.vault.set(f"remote:{ep}", item)
                self.vault.set("remote_ids", list(remotes.keys()))
                if chosen and chosen in remotes:
                    self.vault.set("remote", remotes[chosen])
                else:
                    self.vault.delete("remote")
                self.vault.delete("remotes")
            else:
                self.vault.delete("remote_ids")
                self.vault.delete("remotes")
                self.vault.delete("remote")
            if isinstance(prev_remote_ids, list):
                for old_ep in prev_remote_ids:
                    if isinstance(old_ep, str) and old_ep not in remotes:
                        self.vault.delete(f"remote:{old_ep}")

    def managed_endpoints(self) -> list[str]:
        return list(self._sessions_map().keys())

    def active_endpoint(self) -> str | None:
        with self.state_lock:
            sessions = self._sessions_map()
            if not sessions:
                return None
            preferred = self.vault.get("active_endpoint")
            if preferred and preferred in sessions:
                return preferred
            legacy = self.vault.get("runtime_session")
            if isinstance(legacy, dict) and legacy.get("endpoint") in sessions:
                return legacy["endpoint"]
            return next(iter(sessions))

    def set_active(self, endpoint: str) -> str:
        with self.state_lock:
            sessions = self._sessions_map()
            if endpoint not in sessions:
                raise RuntimeError("所选实例不存在或已释放")
            remotes = self._remotes_map()
            self._write_state(sessions, remotes, active_ep=endpoint)
            return endpoint

    def remote_info(self, endpoint: str | None = None) -> dict | None:
        with self.state_lock:
            target = endpoint or self.active_endpoint()
            if not target:
                return None
            return self._remotes_map().get(target)

    def save_remote(self, remote: dict, activate: bool = False):
        with self.state_lock:
            sessions = self._sessions_map()
            remotes = self._remotes_map()
            ep = remote["endpoint"]
            if not activate and ep not in sessions:
                return
            remotes[ep] = remote
            self._write_state(
                sessions, remotes, active_ep=ep if activate else self.active_endpoint()
            )

    def client(self):
        return self.client_factory()

    def _cache_assignment_proxies(self, assignments) -> None:
        fresh: dict[str, dict] = {}
        now = time.time()
        for a in assignments or []:
            ep = str(getattr(a, "endpoint", "") or "")
            info = getattr(a, "runtime_proxy_info", None)
            url = str(getattr(info, "url", "") or "")
            token = str(getattr(info, "token", "") or "")
            if not ep or not url or not token:
                continue
            expires_at = None
            exp_fn = getattr(info, "expires_at", None)
            if callable(exp_fn):
                try:
                    exp_val = exp_fn()
                    expires_at = (
                        exp_val.timestamp()
                        if hasattr(exp_val, "timestamp")
                        else float(exp_val)
                    )
                except Exception:
                    expires_at = None
            if expires_at is None:
                ttl = getattr(info, "token_expires_in_seconds", None)
                if isinstance(ttl, (int, float)) and ttl > 0:
                    expires_at = now + float(ttl)
            fresh[ep] = {
                "url": url,
                "token": token,
                "expires_at": float(expires_at)
                if expires_at is not None
                else (now + 1800.0),
            }
        with self._proxy_lock:
            self._external_proxies = fresh

    def _evict_external_proxy(self, endpoint: str) -> None:
        with self._proxy_lock:
            self._external_proxies.pop(endpoint, None)

    def _external_proxy_info(
        self, endpoint: str, refresh: bool = False
    ) -> dict | None:
        if not endpoint:
            return None
        now = time.time()
        if not refresh:
            with self._proxy_lock:
                cached = self._external_proxies.get(endpoint)
                if cached and cached.get("expires_at", 0) > now + 60:
                    return dict(cached)
        assignments = self.client().list_assignments()
        self._cache_assignment_proxies(assignments)
        if not any(getattr(a, "endpoint", "") == endpoint for a in assignments):
            return None
        with self._proxy_lock:
            cached = self._external_proxies.get(endpoint)
            return dict(cached) if cached else None

    def close_observer(
        self, endpoint: str | None = None, shutdown_kernel: bool = True
    ) -> None:
        with self._observer_lock:
            obs = self._observer
            if not obs:
                return
            if endpoint is not None and obs.get("endpoint") != endpoint:
                return
            self._observer = None
            self._observer_gen += 1
            rt = obs.get("runtime")
        if rt is not None:
            try:
                rt.stop(shutdown_kernel=bool(shutdown_kernel))
            except Exception:
                pass

    def observe_external_telemetry(self, endpoint: str) -> dict:
        """Read-only hardware observation for an external (non-managed) Colab runtime.

        Uses a dedicated isolated observer kernel that never touches user notebooks,
        never writes files, never sends keep-alive pings, and never adopts the session.
        """
        if not endpoint:
            raise ValueError("缺少远端实例标识")
        proxy = self._external_proxy_info(endpoint)
        if not proxy:
            self.close_observer(endpoint, shutdown_kernel=False)
            raise LookupError("远端实例已不在运行列表中")

        to_stop: list[tuple[object, bool]] = []
        with self._observer_lock:
            obs = self._observer
            if obs and obs.get("endpoint") != endpoint:
                old_rt = obs.get("runtime")
                self._observer = None
                self._observer_gen += 1
                if old_rt is not None:
                    to_stop.append((old_rt, True))
                obs = None

            key = (endpoint, proxy["url"], proxy["token"])
            prev_kid = None
            prev_sid = None
            if obs and obs.get("key") != key:
                # Proxy token rotated for the same observed endpoint: keep our own
                # observer kernel_id so we reconnect instead of spawning duplicates.
                prev_kid = obs.get("kernel_id")
                prev_sid = obs.get("session_id")
                old_rt = obs.get("runtime")
                self._observer = None
                self._observer_gen += 1
                if old_rt is not None:
                    to_stop.append((old_rt, False))
                obs = None

            if obs is None:
                obs = {
                    "endpoint": endpoint,
                    "key": key,
                    "kernel_id": prev_kid,
                    "session_id": prev_sid,
                    "runtime": None,
                }

                def _on_kid(value, slot=obs):
                    slot["kernel_id"] = value

                def _on_sid(value, slot=obs):
                    slot["session_id"] = value

                obs["runtime"] = _create_kernel_runtime(
                    proxy["url"],
                    proxy["token"],
                    session_name="colabone-observer",
                    kernel_id=prev_kid,
                    session_id=prev_sid,
                    on_kernel_started=_on_kid,
                    on_session_started=_on_sid,
                    shutdown_on_ws_timeout=True,
                )
                self._observer = obs
                self._observer_gen += 1

            rt = obs["runtime"]
            obs_gen = self._observer_gen

        for old_rt, do_shutdown in to_stop:
            try:
                old_rt.stop(shutdown_kernel=do_shutdown)
            except Exception:
                pass

        try:
            outputs = rt.execute_code(
                _EXTERNAL_OBSERVER_CODE, allow_stdin=False, timeout=15
            )
        except Exception:
            self._evict_external_proxy(endpoint)
            with self._observer_lock:
                if self._observer is not None and self._observer_gen == obs_gen:
                    self._observer = None
                    self._observer_gen += 1
            try:
                rt.stop(shutdown_kernel=True)
            except Exception:
                pass
            raise

        stdout_combined = "".join(
            str(output.get("text", ""))
            for output in (outputs or [])
            if isinstance(output, dict)
            and not (
                output.get("output_type") == "stream"
                and output.get("name") == "stderr"
            )
        )
        payload = None
        for line in stdout_combined.splitlines():
            stripped = line.strip()
            if stripped.startswith("COLABONE_OBS="):
                try:
                    payload = json.loads(stripped.partition("=")[2])
                except ValueError:
                    payload = None
        if not isinstance(payload, dict):
            raise RuntimeError("远端实例未返回硬件监控采样数据")

        source = str(payload.get("source") or "remote_readonly")
        if source == "remote_colabone" and isinstance(payload.get("status"), dict):
            status = payload["status"]
            drive = status.get("drive") if isinstance(status.get("drive"), dict) else {}
            loc = status.get("location") or infer_location_from_endpoint(
                endpoint, proxy["url"]
            )
            rt_data: dict = {
                "id": endpoint,
                "state": "ready",
            }
            gpu = status.get("gpu") or (
                status.get("resources", {}).get("gpu")
                if isinstance(status.get("resources"), dict)
                else None
            )
            if isinstance(gpu, dict):
                rt_data["gpu"] = gpu
            if isinstance(status.get("resources"), dict):
                rt_data["resources"] = status["resources"]
            if status.get("disk_free") is not None:
                rt_data["disk_free"] = status["disk_free"]
            if status.get("loaded_model"):
                rt_data["loaded_model"] = str(status["loaded_model"])
            if drive:
                rt_data["drive"] = drive
            if loc:
                rt_data["location"] = loc
            if status.get("active_engine"):
                rt_data["active_engine"] = status["active_engine"]
            return {
                "endpoint": endpoint,
                "source": "remote_colabone",
                "runtime": rt_data,
                "loaded_model_name": str(
                    payload.get("loaded_model_name") or status.get("loaded_model") or ""
                ),
                "drive_connected": bool(drive.get("connected")),
                "drive_free_bytes": drive.get("free_bytes"),
            }

        resources = (
            payload.get("resources")
            if isinstance(payload.get("resources"), dict)
            else {}
        )
        gpu = payload.get("gpu") if isinstance(payload.get("gpu"), dict) else None
        zone = str(payload.get("zone") or "")
        loc = location_from_zone(zone) or infer_location_from_endpoint(
            endpoint, proxy["url"]
        )
        rt_data = {
            "id": endpoint,
            "state": "ready",
            "resources": resources,
        }
        if gpu:
            rt_data["gpu"] = gpu
        if payload.get("disk_free") is not None:
            rt_data["disk_free"] = payload["disk_free"]
        if loc:
            rt_data["location"] = loc
        return {
            "endpoint": endpoint,
            "source": "remote_readonly",
            "runtime": rt_data,
            "loaded_model_name": "",
            "drive_connected": False,
            "drive_free_bytes": None,
        }

    def account(self):
        if not self.vault.get("colab"):
            return {"logged_in": False}
        client = self.client()
        info = client.get_consumption_user_info()
        managed_set = set(self.managed_endpoints())
        active_ep = self.active_endpoint()
        assignments = client.list_assignments()
        self._cache_assignment_proxies(assignments)
        instances = [
            describe_assignment(a, managed_set, active_ep) for a in assignments
        ]
        return {
            "logged_in": True,
            "email": self.auth.profile().get("email", ""),
            "balance": float(info.paid_compute_units_balance),
            "rate": float(info.consumption_rate_hourly),
            "assignments": len(instances),
            "instances": instances,
            "checked_at": time.time(),
        }

    def session(self, refresh=False, endpoint: str | None = None, force_rotate=False):
        with self.state_lock:
            target = endpoint or self.active_endpoint()
            data = self._sessions_map().get(target) if target else None
        if not data:
            return None
        session = SessionState.model_validate(data)
        if refresh:
            assignments = self.client().list_assignments()
            assignment = next(
                (a for a in assignments if a.endpoint == session.endpoint), None
            )
            if assignment is None:
                self.clear(session.endpoint)
                return None
            info = assignment.runtime_proxy_info
            with self.state_lock:
                current = self._sessions_map().get(session.endpoint)
                if not current or current["endpoint"] != session.endpoint:
                    return None  # A concurrent release must not be undone by this read.
                session = SessionState.model_validate(current)
                needs_rotate = (
                    force_rotate
                    or not session.token
                    or session.url != info.url
                    or session.token_expires_at is None
                    or session.token_expires_at.timestamp() <= time.time() + 600
                )
                if needs_rotate:
                    session.url, session.token, session.token_expires_at = (
                        info.url,
                        info.token,
                        info.expires_at(),
                    )
                    self.save_session(session)
        return session

    def save_session(self, session, activate: bool = False):
        with self.state_lock:
            sessions = self._sessions_map()
            remotes = self._remotes_map()
            sessions[session.endpoint] = session.model_dump(mode="json")
            chosen = session.endpoint if activate else self.active_endpoint()
            self._write_state(sessions, remotes, active_ep=chosen or session.endpoint)

    def clear(self, endpoint=None):
        with self.state_lock:
            sessions = self._sessions_map()
            remotes = self._remotes_map()
            if endpoint is None:
                target = self.active_endpoint()
                if not target:
                    self._loc_probed.clear()
                    self._write_state({}, {})
                    return
                endpoint = target
            self._loc_probed.discard(endpoint)
            if endpoint not in sessions and endpoint not in remotes:
                return
            sessions.pop(endpoint, None)
            remotes.pop(endpoint, None)
            next_active = (
                self.vault.get("active_endpoint")
                if self.vault.get("active_endpoint") in sessions
                else next(iter(sessions), None)
            )
            self._write_state(sessions, remotes, active_ep=next_active)

    def release(self, endpoint: str | None = None, on_log=None):
        session = self.session(endpoint=endpoint)
        if not session:
            _emit_log(on_log, "本机未找到需要释放的实例会话", "GPU")
            return {"ok": True}
        client = self.client()
        _emit_log(on_log, f"查询 Google Colab 实例状态：{session.endpoint}", "GPU")
        endpoints = {a.endpoint for a in client.list_assignments()}
        unassign_ok = True
        if session.endpoint in endpoints:
            _emit_log(on_log, f"向 Google Colab 发送释放请求：{session.endpoint}", "GPU")
            try:
                client.unassign(session.endpoint)
            except Exception:
                # The independent remote guard may already have released it.
                # An HTTP error is not evidence either way: confirm disappearance.
                unassign_ok = False
                _emit_log(
                    on_log,
                    f"释放请求未获直接确认，改为轮询核实停止状态：{session.endpoint}",
                    "GPU",
                    "warning",
                )
        else:
            _emit_log(on_log, f"实例已不在 Colab 活跃列表中：{session.endpoint}", "GPU")
        for attempt in range(15):
            try:
                listed = {a.endpoint for a in client.list_assignments()}
            except (ColabRequestError, requests.RequestException, TransportError):
                if attempt < 14:
                    time.sleep(2)
                    continue
                raise
            if session.endpoint not in listed:
                self.clear(session.endpoint)
                self.close_kernel_channel(session.endpoint, evict=True)
                _emit_log(
                    on_log,
                    f"已确认实例停止并清理本机会话：{session.endpoint}",
                    "GPU",
                )
                return {"ok": True}
            if not unassign_ok and attempt in (2, 6):
                try:
                    client.unassign(session.endpoint)
                    unassign_ok = True
                except Exception:
                    pass
            if attempt == 0:
                _emit_log(
                    on_log,
                    f"等待 Google Colab 确认停止计费：{session.endpoint}",
                    "GPU",
                )
            elif attempt in (4, 9):
                _emit_log(
                    on_log,
                    f"仍在等待释放确认（第 {attempt + 1}/15 次查询）：{session.endpoint}",
                    "GPU",
                )
            time.sleep(2)
        raise RuntimeError("释放请求已发送，但尚未确认停止计费；请刷新后重试")

    def release_all(self, targets, notify):
        """Explicit user action only. Guards and startup rollback still release owned only.

        Stop the reviewed set; do not chase new instances created after confirmation.
        Confirm disappearance even if an unassign response is lost.
        """
        client = self.client()
        targets = set(targets)
        notify(f"正在查询账号运行实例（待停止 {len(targets)} 个）")
        current = {a.endpoint for a in client.list_assignments()}
        to_stop = sorted(targets & current)
        if not to_stop:
            notify("目标实例均已不在运行列表中")
        unassign_failed: set[str] = set()
        for endpoint in to_stop:
            self.close_observer(endpoint, shutdown_kernel=False)
            notify("正在停止实例：" + endpoint)
            try:
                client.unassign(endpoint)
            except Exception:
                unassign_failed.add(endpoint)
                notify("停止请求未获确认，正在重新查询：" + endpoint)
        confirmed: set[str] = set()
        remaining = set(to_stop)
        for attempt in range(15):
            try:
                current = {a.endpoint for a in client.list_assignments()}
            except (ColabRequestError, requests.RequestException, TransportError):
                if attempt < 14:
                    time.sleep(2)
                    continue
                raise
            for ep in sorted(targets - current - confirmed):
                confirmed.add(ep)
                unassign_failed.discard(ep)
                notify("已确认停止实例：" + ep)
            for owned in list(self.managed_endpoints()):
                if owned in targets and owned not in current:
                    self.clear(owned)
                    self.close_kernel_channel(owned, evict=True)
            remaining = targets & current
            if not remaining:
                return {"ok": True, "remaining": len(current)}
            if unassign_failed and attempt in (2, 6):
                for ep in sorted(unassign_failed & remaining):
                    try:
                        client.unassign(ep)
                        unassign_failed.discard(ep)
                    except Exception:
                        pass
            if attempt == 0:
                notify(f"等待 Google Colab 确认停止计费（剩余 {len(remaining)} 个）")
            if attempt < 14:
                time.sleep(2)
        raise RuntimeError(
            f"仍有 {len(remaining)} 个实例未确认停止，可能仍在计费，请重试"
        )

    def _assign_runtime(
        self,
        client,
        accel,
        shape,
        on_log=None,
        on_stage=None,
        retries_counter: list[int] | None = None,
        cancel_event: threading.Event | None = None,
    ):
        has_two_phase = callable(
            getattr(type(client), "_get_assignment", None)
        ) and callable(getattr(type(client), "_post_assignment", None))
        if not has_two_phase:
            return client.assign(
                uuid.uuid4(), variant=Variant.GPU, accelerator=accel, shape=shape
            )

        nbh = uuid.uuid4()
        delays = tuple(self._assign_retry_delays)
        total_attempts = 1 + len(delays)
        assignment = None
        for attempt in range(total_attempts):
            if cancel_event is not None and cancel_event.is_set():
                raise RuntimeError("已取消 GPU 分配")
            try:
                if assignment is None:
                    assignment = client._get_assignment(
                        nbh, Variant.GPU, accel, shape
                    )
                    if isinstance(assignment, Assignment):
                        if attempt > 0:
                            _emit_log(
                                on_log,
                                f"已通过状态核验确认 {accel.value} GPU 实例分配成功",
                                "GPU",
                            )
                        return assignment
                return client._post_assignment(
                    nbh, assignment.token, Variant.GPU, accel, shape
                )
            except (ColabRequestError, requests.RequestException, TransportError) as e:
                status = (
                    int(getattr(getattr(e, "response", None), "status_code", 0) or 0)
                    if isinstance(e, ColabRequestError)
                    else 0
                )
                if status == 412:
                    raise TooManyAssignmentsError(str(e)) from e
                is_net_err = isinstance(e, (requests.RequestException, TransportError))
                retryable = is_net_err or status in (502, 503, 504)
                if not retryable:
                    raise
                if attempt >= len(delays):
                    # Final reconciliation: if the last POST hit Google's 30s
                    # frontend proxy timeout or a connection drop after the VM
                    # was already assigned, adopt it instead of orphaning it.
                    try:
                        if any(d > 0 for d in delays):
                            if cancel_event is not None:
                                if cancel_event.wait(2):
                                    raise RuntimeError("已取消 GPU 分配")
                            else:
                                time.sleep(2)
                        reconciled = client._get_assignment(
                            nbh, Variant.GPU, accel, shape
                        )
                        if isinstance(reconciled, Assignment):
                            _emit_log(
                                on_log,
                                f"已通过状态核验确认 {accel.value} GPU 实例分配成功",
                                "GPU",
                            )
                            return reconciled
                    except RuntimeError:
                        raise
                    except Exception:
                        pass
                    raise
                delay = delays[attempt]
                next_attempt = attempt + 2
                if retries_counter is not None:
                    retries_counter[0] = attempt + 1
                reason_tag = (
                    f"HTTP {status}"
                    if status
                    else (
                        "请求超时"
                        if isinstance(e, requests.exceptions.Timeout)
                        else "网络异常"
                    )
                )
                _emit_log(
                    on_log,
                    f"Google Colab 分配 {accel.value} GPU 暂时繁忙或超时（{reason_tag}），{delay} 秒后自动重试（第 {next_attempt}/{total_attempts} 次）",
                    "GPU",
                    "warning",
                )
                if callable(on_stage):
                    on_stage(
                        f"正在分配 {accel.value}（第 {next_attempt}/{total_attempts} 次）"
                    )
                # Force _get_assignment reconciliation before submitting another
                # POST so an uncertain 503/504/timeout never double-allocates.
                assignment = None
                if delay > 0:
                    if cancel_event is not None:
                        if cancel_event.wait(delay):
                            raise RuntimeError("已取消 GPU 分配")
                    else:
                        time.sleep(delay)

    def allocate(self, gpu, on_log=None, on_stage=None, cancel_event=None):
        choices = {
            x.value.upper(): x
            for x in (
                Accelerator.T4,
                Accelerator.L4,
                Accelerator.A100,
                Accelerator.G4,
                Accelerator.H100,
            )
        }
        if gpu.upper() not in choices:
            raise ValueError("不支持的 GPU 型号")
        accel = choices[gpu.upper()]
        shape = (
            None
            if accel in {Accelerator.G4, Accelerator.H100, Accelerator.L4}
            else resolve_assign_shape(accel, high_mem=True)
        )
        shape_label = "高内存规格" if shape == Shape.HIGH_RAM else "默认规格"
        _emit_log(
            on_log,
            f"向 Google Colab 请求分配 {accel.value} GPU（{shape_label}）",
            "GPU",
        )
        client = self.client()
        retries_used = [0]
        try:
            try:
                result = self._assign_runtime(
                    client,
                    accel,
                    shape,
                    on_log=on_log,
                    on_stage=on_stage,
                    retries_counter=retries_used,
                    cancel_event=cancel_event,
                )
            except ColabRequestError as e:
                if shape is None or getattr(e.response, "status_code", 0) != 400:
                    raise
                _emit_log(
                    on_log,
                    "高内存规格暂不可用，自动切换为默认规格重试分配",
                    "GPU",
                    "warning",
                )
                retries_used[0] = 0
                result = self._assign_runtime(
                    client,
                    accel,
                    None,
                    on_log=on_log,
                    on_stage=on_stage,
                    retries_counter=retries_used,
                    cancel_event=cancel_event,
                )
                shape = None
        except (
            ColabRequestError,
            TooManyAssignmentsError,
            requests.RequestException,
            TransportError,
        ) as exc:
            raise RuntimeError(
                _format_assign_error(accel.value, exc, retries_used[0])
            ) from exc
        info = result.runtime_proxy_info
        session = SessionState(
            name="colabone",
            endpoint=result.endpoint,
            token=info.token,
            url=info.url,
            token_expires_at=info.expires_at(),
            variant=Variant.GPU.value,
            accelerator=accel.value,
            machine_shape=Shape.HIGH_RAM.name
            if shape == Shape.HIGH_RAM
            else Shape.STANDARD.name,
        )
        # Record ownership immediately, before readiness/deployment can fail.
        # If local vault persistence fails, immediately unassign the VM so it
        # is never orphaned while billing.
        try:
            self.save_session(session, activate=True)
        except Exception as save_exc:
            try:
                client.unassign(session.endpoint)
            except Exception:
                pass
            raise RuntimeError(
                f"保存实例会话失败，已尝试释放刚分配的实例：{safe_message(save_exc)[:200]}"
            ) from save_exc
        _emit_log(on_log, f"已保存实例会话与控制令牌：{session.endpoint}", "客户端")
        return session

    def execute(self, session, code, timeout=300, hook=None, output_hook=None):
        slot = self._kernel_slot(session.endpoint)
        with slot["lock"]:
            return self._execute(slot, session, code, timeout, hook, output_hook)

    def _execute(self, slot, session, code, timeout=300, hook=None, output_hook=None):
        def save_identity(field, value):
            # Callbacks can outlive a proxy refresh or release. Merge only the
            # kernel identity into the still-owned session, never old credentials.
            with self.state_lock:
                sessions = self._sessions_map()
                current = sessions.get(session.endpoint)
                if current and current["endpoint"] == session.endpoint:
                    sessions[session.endpoint] = current | {field: value}
                    self._write_state(sessions, self._remotes_map())

        def kid(value):
            session.kernel_id = value
            save_identity("kernel_id", value)

        def sid(value):
            session.session_id = value
            save_identity("session_id", value)

        key = (session.endpoint, session.url, session.token)
        if slot["key"] != key:
            self.close_kernel_channel(session.endpoint)
        if slot["runtime"] is None:
            slot["runtime"] = _create_kernel_runtime(
                session.url,
                session.token,
                session_name="colabone",
                kernel_id=session.kernel_id,
                session_id=session.session_id,
                on_kernel_started=kid,
                on_session_started=sid,
            )
            slot["key"] = key
        rt = slot["runtime"]
        rt.colab_request_hook = hook
        try:
            exec_kwargs = {"allow_stdin": False, "timeout": timeout}
            if output_hook is not None:
                exec_kwargs["output_hook"] = output_hook
            return rt.execute_code(code, **exec_kwargs)
        except Exception:
            # Reconnect on the next request; never replay a mutation whose outcome
            # is unknown. Closing the websocket must not shut down the kernel.
            self.close_kernel_channel(session.endpoint)
            raise

    def close_kernel_channel(self, endpoint: str | None = None, evict: bool = False):
        with self.kernel_lock:
            targets = [endpoint] if endpoint else list(self._kernels.keys())
        for ep in targets:
            with self.kernel_lock:
                slot = self._kernels.get(ep)
            if not slot:
                continue
            with slot["lock"]:
                if slot["runtime"]:
                    try:
                        slot["runtime"].stop(shutdown_kernel=False)
                    except Exception:
                        pass
                slot["runtime"], slot["key"] = None, None
            if evict:
                with self.kernel_lock:
                    if self._kernels.get(ep) is slot and slot["runtime"] is None:
                        self._kernels.pop(ep, None)

    def _upload_file(
        self,
        session,
        local_path: str,
        remote_path: str,
        timeout: tuple[float, float] = (10.0, 60.0),
    ):
        """Upload a file via Colab Contents API with explicit timeout and bounded retries."""
        if ContentsClient is not _REAL_CONTENTS_CLIENT:
            return ContentsClient(session).upload(local_path, remote_path)
        raw = Path(local_path).read_bytes()
        b64_content = base64.b64encode(raw).decode("ascii")
        filename = remote_path.split("/")[-1]
        quoted_path = quote(remote_path.strip("/"), safe="/")
        payload = {
            "name": filename,
            "path": remote_path,
            "type": "file",
            "format": "base64",
            "content": b64_content,
            "chunk": 1,
        }
        rotated = False
        cur = session
        for attempt in range(3):
            url = f"{cur.url.rstrip('/')}/api/contents/{quoted_path}"
            try:
                resp = requests.put(
                    url,
                    params={"authuser": "0", "colab-runtime-proxy-token": cur.token},
                    headers={"X-Colab-Client-Agent": "colab-cli"},
                    json=payload,
                    timeout=timeout,
                )
            except requests.RequestException:
                if attempt < 2:
                    time.sleep(1.0 * (attempt + 1))
                    continue
                raise
            if resp.status_code in (401, 403) and not rotated:
                rotated = True
                refreshed = self.session(
                    refresh=True, endpoint=cur.endpoint, force_rotate=True
                )
                if refreshed:
                    cur = refreshed
                    continue
            if resp.status_code in (502, 503, 504) and attempt < 2:
                time.sleep(1.0 * (attempt + 1))
                continue
            resp.raise_for_status()
            return resp.json()

    @staticmethod
    def _delete_remote_file(session, remote_path: str) -> None:
        if ContentsClient is not _REAL_CONTENTS_CLIENT:
            return
        quoted_path = quote(remote_path.strip("/"), safe="/")
        url = f"{session.url.rstrip('/')}/api/contents/{quoted_path}"
        try:
            requests.delete(
                url,
                params={"authuser": "0", "colab-runtime-proxy-token": session.token},
                headers={"X-Colab-Client-Agent": "colab-cli"},
                timeout=(5.0, 10.0),
            )
        except Exception:
            pass

    @staticmethod
    def ready(session, on_log=None):
        started = time.monotonic()
        last_note = started
        deadline = started + 90
        while time.monotonic() < deadline:
            try:
                r = requests.get(
                    session.url.rstrip("/") + "/api/kernels",
                    headers={
                        "X-Colab-Client-Agent": "colab-cli",
                        "X-Colab-Runtime-Proxy-Token": session.token,
                        "Authorization": "Bearer " + session.token,
                    },
                    timeout=8,
                )
                if r.status_code == 200:
                    _emit_log(
                        on_log,
                        f"远端 Jupyter 内核已响应（耗时 {time.monotonic() - started:.1f} 秒）",
                        "GPU",
                    )
                    return
            except requests.RequestException:
                pass
            now = time.monotonic()
            if now - last_note >= 8:
                last_note = now
                _emit_log(
                    on_log,
                    f"仍在等待远端 Jupyter 内核响应（已等待 {int(now - started)} 秒）",
                    "GPU",
                )
            time.sleep(2)
        raise RuntimeError("远端内核未能在 90 秒内就绪")

    def start(
        self,
        gpu,
        notify,
        create_new: bool = False,
        rclone_conf: str = "",
        on_telemetry=None,
        on_log=None,
        cancel_event=None,
    ):
        if not callable(notify) and callable(create_new):
            notify, create_new = create_new, False
        fresh = False
        previous_active = self.active_endpoint()
        allocated_endpoint = None
        if not create_new:
            session = self.session(refresh=True)
            if session:
                remote = self.remote_info(session.endpoint)
                if remote and remote.get("endpoint") == session.endpoint:
                    _emit_log(
                        on_log,
                        f"检测到已连接实例，直接复用：{session.endpoint}",
                        "GPU",
                    )
                    return remote
                raise RuntimeError("实例存在但部署未完成，请先释放后重新启动")
        _emit_log(on_log, "查询 Google Colab 账号余额与当前运行实例", "计费")
        try:
            before = self.account() or {}
        except Exception:
            before = {}
        if before.get("logged_in"):
            _emit_log(
                on_log,
                f"当前余额 {float(before.get('balance') or 0):.2f} CU · 活跃实例 {int(before.get('assignments') or 0)} 台",
                "计费",
            )
        try:
            notify("正在分配 " + gpu)
            started = time.time()
            alloc_kwargs = {}
            if _accepts_kwarg(self.allocate, "on_log"):
                alloc_kwargs["on_log"] = on_log
            if _accepts_kwarg(self.allocate, "on_stage"):
                alloc_kwargs["on_stage"] = notify
            if cancel_event is not None and _accepts_kwarg(self.allocate, "cancel_event"):
                alloc_kwargs["cancel_event"] = cancel_event
            session = self.allocate(gpu, **alloc_kwargs)
            allocated_endpoint = session.endpoint
            fresh = True
            try:
                account = self.account() or {}
            except Exception:
                account = {}
            rate = account.get("rate", 0)
            remote = {
                "endpoint": session.endpoint,
                "token": secrets.token_urlsafe(32),
                "transport": "colab",
                "started_at": started,
                "deadline": 0,
                "start_balance": before.get("balance", 0),
                "rate": rate,
                "gpu": gpu.upper(),
            }
            initial_loc = infer_location_from_endpoint(
                session.endpoint, str(getattr(session, "url", "") or "")
            )
            if initial_loc:
                remote["location"] = initial_loc
            self.save_remote(remote, activate=True)
            meta_parts = []
            loc_text = _format_location_short(initial_loc)
            if loc_text:
                meta_parts.append(loc_text)
            if getattr(session, "machine_shape", "") == Shape.HIGH_RAM.name:
                meta_parts.append("高内存")
            if rate:
                meta_parts.append(f"{float(rate):.2f} CU/小时")
            detail = f"（{' · '.join(meta_parts)}）" if meta_parts else ""
            _emit_log(on_log, f"已分配实例 {session.endpoint}{detail}", "GPU")
            notify("等待远端内核")
            ready_started = time.monotonic()
            if _accepts_kwarg(self.ready, "on_log"):
                self.ready(session, on_log=on_log)
            else:
                self.ready(session)
                _emit_log(
                    on_log,
                    f"远端 Jupyter 内核已就绪（耗时 {time.monotonic() - ready_started:.1f} 秒）",
                    "GPU",
                )
            notify("部署远端管理服务")
            with tempfile.TemporaryDirectory(prefix="colabone-upload-") as tmp:
                package = Path(tmp) / "colabone.zip"
                file_count = 0
                with zipfile.ZipFile(package, "w", zipfile.ZIP_DEFLATED) as archive:
                    for folder in COLABONE_SERVICE_BUNDLE_FOLDERS:
                        for path in sorted(
                            (self.root / folder).glob("*"), key=lambda p: p.name
                        ):
                            if (
                                path.is_file()
                                and path.suffix in COLABONE_SERVICE_BUNDLE_SUFFIXES
                            ):
                                archive.write(path, f"colabone/{folder}/{path.name}")
                                file_count += 1
                pkg_kb = max(1, round(package.stat().st_size / 1024))
                _emit_log(
                    on_log,
                    f"已打包远端管理服务（{file_count} 个文件，{pkg_kb} KB）",
                    "客户端",
                )
                config = Path(tmp) / "boot-config.json"
                config.write_text(
                    json.dumps(
                        {
                            "token": remote["token"],
                            "runtime_id": session.endpoint,
                            "root": "/content/colabone-data",
                            "deadline": 0,
                        }
                    )
                )
                config.chmod(0o600)
                _emit_log(on_log, "正在上传管理服务程序包 (colabone.zip)", "GPU")
                self._upload_file(session, str(package), "content/colabone.zip")
                _emit_log(on_log, "正在上传远端启动配置 (colabone-boot.json)", "GPU")
                self._upload_file(session, str(config), "content/colabone-boot.json")
                if rclone_conf:
                    rc_local = Path(tmp) / "colabone-rclone.conf"
                    rc_local.write_text(rclone_conf, encoding="utf-8")
                    rc_local.chmod(0o600)
                    _emit_log(on_log, "正在上传 Google 云盘授权配置", "云盘")
                    self._upload_file(
                        session, str(rc_local), "content/.colabone_rclone.conf"
                    )
                _emit_log(on_log, "文件上传完成，开始在远端内核执行引导脚本", "远端")
                line_bufs: dict[str, str] = {}
                streamed_telemetry = [False]
                streamed_logs = [False]

                def _consume_boot_line(raw_line: str, from_stream: bool = True):
                    stripped = raw_line.strip()
                    if stripped.startswith("COLABONE_TELEMETRY="):
                        try:
                            sample = json.loads(stripped.partition("=")[2])
                            if isinstance(sample, dict) and callable(on_telemetry):
                                if from_stream:
                                    streamed_telemetry[0] = True
                                on_telemetry(
                                    session.endpoint,
                                    remote.get("location"),
                                    sample,
                                )
                        except Exception:
                            pass
                        return
                    if stripped.startswith("COLABONE_LOG="):
                        try:
                            entry = json.loads(stripped.partition("=")[2])
                            if isinstance(entry, dict):
                                if from_stream:
                                    streamed_logs[0] = True
                                stage = str(entry.get("stage") or "").strip()
                                msg = str(entry.get("message") or "").strip()
                                src = str(entry.get("source") or "远端").strip()
                                lvl = str(entry.get("level") or "info").strip()
                                if stage and callable(notify):
                                    notify(stage)
                                if msg and msg != stage:
                                    _emit_log(on_log, msg, src, lvl)
                        except Exception:
                            pass

                def _boot_output_hook(output):
                    if not isinstance(output, dict):
                        return
                    if (
                        output.get("output_type") == "stream"
                        and output.get("name") == "stderr"
                    ):
                        return
                    text = str(output.get("text", ""))
                    if not text:
                        return
                    stream_key = str(output.get("name") or "stdout")
                    combined = line_bufs.get(stream_key, "") + text
                    parts = combined.splitlines(keepends=True)
                    line_bufs[stream_key] = ""
                    for part in parts:
                        if not part.endswith(("\n", "\r")):
                            line_bufs[stream_key] = part
                            continue
                        _consume_boot_line(part, from_stream=True)

                exec_kwargs = {"timeout": 300}
                if callable(on_telemetry) or callable(on_log):
                    exec_kwargs["output_hook"] = _boot_output_hook
                outputs = self.execute(
                    session,
                    (self.root / "server/vm_boot.py").read_text(encoding="utf-8"),
                    **exec_kwargs,
                )
                for leftover in list(line_bufs.values()):
                    if leftover:
                        _consume_boot_line(leftover, from_stream=True)
                line_bufs.clear()
            ready = False
            stdout_combined = "".join(
                str(output.get("text", ""))
                for output in (outputs or [])
                if isinstance(output, dict)
                and not (
                    output.get("output_type") == "stream"
                    and output.get("name") == "stderr"
                )
            )
            for line in stdout_combined.splitlines():
                stripped = line.strip()
                if stripped == "COLABONE_READY=1":
                    ready = True
                elif (
                    not streamed_telemetry[0]
                    and callable(on_telemetry)
                    and stripped.startswith("COLABONE_TELEMETRY=")
                ):
                    _consume_boot_line(line, from_stream=False)
                elif (
                    not streamed_logs[0]
                    and callable(on_log)
                    and stripped.startswith("COLABONE_LOG=")
                ):
                    _consume_boot_line(line, from_stream=False)
            if not ready:
                boot_err = ""
                for output in outputs or []:
                    if (
                        isinstance(output, dict)
                        and output.get("output_type") == "error"
                    ):
                        ename = str(output.get("ename") or "").strip()
                        evalue = str(output.get("evalue") or "").strip()
                        detail = (
                            f"{ename}: {evalue}"
                            if (ename and evalue)
                            else (evalue or ename)
                        )
                        if detail:
                            boot_err = safe_message(detail)[:240]
                            break
                if boot_err:
                    _emit_log(on_log, f"远端初始化异常：{boot_err}", "远端", "error")
                    raise RuntimeError(f"远端初始化未完成：{boot_err}")
                raise RuntimeError("远端初始化未完成；可查看 Colab 内核日志")
            self.save_remote(remote, activate=True)
            _emit_log(on_log, "远端管理服务引导完成", "远端")
            return remote
        except Exception as exc:
            if fresh:
                _emit_log(
                    on_log,
                    f"启动过程出错，正在自动回滚释放刚分配的实例：{allocated_endpoint}",
                    "GPU",
                    "warning",
                )
                try:
                    if len(self.managed_endpoints()) > 1 and allocated_endpoint:
                        if _accepts_kwarg(self.release, "on_log"):
                            self.release(allocated_endpoint, on_log=on_log)
                        else:
                            self.release(allocated_endpoint)
                    else:
                        if _accepts_kwarg(self.release, "on_log"):
                            self.release(on_log=on_log)
                        else:
                            self.release()
                except Exception:
                    raise RuntimeError(
                        "启动失败且释放未确认。实例记录已保留，请在设备页立即重试释放"
                    ) from exc
                if previous_active and previous_active in self.managed_endpoints():
                    self.set_active(previous_active)
            raise

    def upgrade_service(self, endpoint: str | None = None, on_log=None):
        session = self.session(refresh=True, endpoint=endpoint)
        if not session:
            raise RuntimeError("请先启动远端 GPU")
        slot = self._kernel_slot(session.endpoint)
        with slot["lock"]:
            with tempfile.TemporaryDirectory(prefix="colabone-update-") as tmp:
                package = Path(tmp) / "update.zip"
                file_count = 0
                with zipfile.ZipFile(package, "w", zipfile.ZIP_DEFLATED) as archive:
                    for folder in COLABONE_SERVICE_BUNDLE_FOLDERS:
                        for path in sorted(
                            (self.root / folder).glob("*"), key=lambda p: p.name
                        ):
                            if (
                                path.is_file()
                                and path.suffix in COLABONE_SERVICE_BUNDLE_SUFFIXES
                            ):
                                archive.write(path, f"colabone/{folder}/{path.name}")
                                file_count += 1
                _emit_log(
                    on_log,
                    f"已打包新版远端服务（{file_count} 个文件），正在上传",
                    "客户端",
                )
                self._upload_file(
                    session, str(package), "content/colabone-update.zip"
                )
                _emit_log(on_log, "更新包上传完成，开始在远端执行热更新脚本", "远端")
                line_bufs: dict[str, str] = {}
                streamed_logs = [False]

                def _consume_update_line(raw_line: str, from_stream: bool = True):
                    stripped = raw_line.strip()
                    if not stripped.startswith("COLABONE_LOG="):
                        return
                    try:
                        entry = json.loads(stripped.partition("=")[2])
                        if isinstance(entry, dict):
                            if from_stream:
                                streamed_logs[0] = True
                            msg = str(entry.get("message") or "").strip()
                            if msg:
                                _emit_log(
                                    on_log,
                                    msg,
                                    str(entry.get("source") or "远端"),
                                    str(entry.get("level") or "info"),
                                )
                    except Exception:
                        pass

                def _update_output_hook(output):
                    if not isinstance(output, dict):
                        return
                    if (
                        output.get("output_type") == "stream"
                        and output.get("name") == "stderr"
                    ):
                        return
                    text = str(output.get("text", ""))
                    if not text:
                        return
                    stream_key = str(output.get("name") or "stdout")
                    combined = line_bufs.get(stream_key, "") + text
                    parts = combined.splitlines(keepends=True)
                    line_bufs[stream_key] = ""
                    for part in parts:
                        if not part.endswith(("\n", "\r")):
                            line_bufs[stream_key] = part
                            continue
                        _consume_update_line(part, from_stream=True)

                exec_kwargs = {"timeout": 210}
                if callable(on_log):
                    exec_kwargs["output_hook"] = _update_output_hook
                outputs = self.execute(
                    session,
                    (self.root / "server/vm_update.py").read_text(encoding="utf-8"),
                    **exec_kwargs,
                )
                for leftover in list(line_bufs.values()):
                    if leftover:
                        _consume_update_line(leftover, from_stream=True)
                line_bufs.clear()
                if not streamed_logs[0] and callable(on_log):
                    for o in outputs or []:
                        for line in str(o.get("text", "")).splitlines():
                            _consume_update_line(line, from_stream=False)
                if not any(
                    "COLABONE_UPDATED=1" in str(o.get("text", ""))
                    for o in outputs or []
                ):
                    raise RuntimeError(
                        "远端服务更新未完成，缓存文件仍保留，请重试或下次启动时更新"
                    )

    def _ensure_valid_session(
        self, endpoint: str | None = None, min_ttl: float = 60.0
    ):
        session = self.session(endpoint=endpoint)
        if not session:
            return None
        if (
            session.token_expires_at is None
            or session.token_expires_at.timestamp() <= time.time() + min_ttl
        ):
            session = self.session(
                refresh=True, endpoint=session.endpoint, force_rotate=True
            )
        return session

    def upload_drive_config(self, rclone_conf: str, endpoint: str | None = None):
        session = self._ensure_valid_session(endpoint=endpoint)
        if not session:
            raise RuntimeError("请先启动远端 GPU")
        with tempfile.TemporaryDirectory(prefix="colabone-drive-") as tmp:
            rc_local = Path(tmp) / "colabone-rclone.conf"
            rc_local.write_text(rclone_conf, encoding="utf-8")
            rc_local.chmod(0o600)
            self._upload_file(session, str(rc_local), "content/.colabone_rclone.conf")

    def check_colab_drivefs_auth(self, endpoint: str | None = None) -> tuple[bool, str]:
        """Fast dry-run check of whether Colab DriveFS is already authorized on this VM."""
        session = self._ensure_valid_session(endpoint=endpoint)
        if not session:
            return False, ""
        mount = ColabOneDriveMount(self.client(), session.endpoint)
        return mount.dryrun_check()

    def mount_colab_drivefs(
        self,
        endpoint: str | None = None,
        on_auth_url=None,
        on_log=None,
        cancel_event: threading.Event | None = None,
        confirm_event: threading.Event | None = None,
        probe_event: threading.Event | None = None,
        on_hint=None,
        account_email: str = "",
        timeout: float = 300.0,
    ) -> dict:
        """Mount Google Drive via official `google.colab.drive.mount` (`dfs_ephemeral`).

        Uses a dedicated short-lived mount kernel so the primary RPC/telemetry
        kernel channel never blocks while waiting for browser confirmation.
        Once mounted, Colab's DriveFS daemon stays alive after kernel shutdown.
        """
        total_timeout = max(60.0, float(timeout))
        session = self._ensure_valid_session(
            endpoint=endpoint, min_ttl=total_timeout + 60.0
        )
        if not session:
            raise RuntimeError("请先启动远端 GPU")
        auth_timeout = min(240.0, max(30.0, total_timeout - 45.0))
        mount_helper = ColabOneDriveMount(
            self.client(),
            session.endpoint,
            on_auth_url=on_auth_url,
            on_log=on_log,
            cancel_event=cancel_event,
            confirm_event=confirm_event,
            probe_event=probe_event,
            on_hint=on_hint,
            account_email=account_email,
            timeout=auth_timeout,
        )
        rt = _create_kernel_runtime(
            session.url,
            session.token,
            session_name="colabone-drivefs",
            shutdown_on_ws_timeout=True,
        )
        rt.colab_request_hook = mount_helper.hook
        timeout_ms = int(total_timeout * 1000)
        mount_code = (
            "import os\n"
            "from google.colab import drive\n"
            f"drive.mount('/content/drive', timeout_ms={timeout_ms})\n"
            "ok = os.path.isdir('/content/drive/MyDrive') or os.path.isdir('/content/drive/My Drive')\n"
            "print('COLABONE_DRIVEFS_OK=' + ('1' if ok else '0'), flush=True)\n"
        )

        def _noop_stdin_hook(_prompt):
            # Never read desktop JSON-RPC sys.stdin if a fallback prompt appears.
            return ""

        cancel_watch_stop = threading.Event()

        def _watch_cancel():
            if cancel_event is None:
                return
            while not cancel_watch_stop.is_set():
                if cancel_event.wait(0.2):
                    mount_helper.stop()
                    kc = getattr(rt, "_kernel_client", None)
                    ws_client = getattr(getattr(kc, "_manager", None), "client", None)
                    if ws_client is not None:
                        try:
                            ws_client.stop_channels()
                        except Exception:
                            pass
                        evt = getattr(ws_client, "_message_received", None)
                        if evt is not None:
                            evt.set()
                    return

        if cancel_event is not None:
            threading.Thread(target=_watch_cancel, daemon=True).start()

        try:
            outputs = rt.execute_code(
                mount_code,
                allow_stdin=True,
                stdin_hook=_noop_stdin_hook,
                timeout=total_timeout,
            )
        except Exception as exc:
            if mount_helper.error:
                raise RuntimeError(mount_helper.error) from None
            if cancel_event is not None and cancel_event.is_set():
                raise RuntimeError("已取消 Google 云盘挂载") from None
            raise RuntimeError(
                "Google Colab 原生云盘挂载连接中断，请重试：" + safe_message(exc)[:200]
            ) from None
        finally:
            cancel_watch_stop.set()
            mount_helper.stop()
            try:
                rt.stop(shutdown_kernel=True)
            except Exception:
                pass

        if mount_helper.error:
            raise RuntimeError(mount_helper.error)

        stdout_combined = "".join(
            str(out.get("text", ""))
            for out in (outputs or [])
            if isinstance(out, dict)
            and not (
                out.get("output_type") == "stream" and out.get("name") == "stderr"
            )
        )
        mounted = any(
            line.strip() == "COLABONE_DRIVEFS_OK=1"
            for line in stdout_combined.splitlines()
        )
        err_text = ""
        for out in outputs or []:
            if isinstance(out, dict) and out.get("output_type") == "error":
                evalue = str(out.get("evalue") or "").strip()
                ename = str(out.get("ename") or "").strip()
                err_text = evalue or ename
        if not mounted:
            if err_text:
                raise RuntimeError(
                    "Google Colab 原生云盘挂载失败：" + safe_message(err_text)[:220]
                )
            raise RuntimeError("Google Colab 原生云盘未能完成挂载，请重试")
        return {"ok": True, "mount_path": "/content/drive/MyDrive", "mode": "colab_drivefs"}

    def rpc(
        self,
        method,
        path,
        body=None,
        endpoint: str | None = None,
        timeout: int = 75,
    ):
        """Use Colab's authenticated kernel channel; no public port or VPN account.

        Sensitive POST data is uploaded as an ephemeral file, never embedded in
        notebook source/history. GET paths contain only non-secret resource IDs.
        """
        session = self.session(endpoint=endpoint)
        if not session:
            raise RuntimeError("请先启动远端 GPU")
        idempotent = method.upper() == "GET" or path in (
            "drive/connect",
            "drive/sync",
            "configure",
        )
        exec_timeout = max(30, int(timeout))
        http_timeout = max(20, exec_timeout - 10)
        slot = self._kernel_slot(session.endpoint)
        with slot["lock"]:
            for attempt in range(2 if idempotent else 1):
                session = self.session(endpoint=session.endpoint)
                if session and (
                    session.token_expires_at is None
                    or session.token_expires_at.timestamp() <= time.time() + 60
                ):
                    # Refresh before uploading or executing. Retrying a failed POST
                    # could duplicate a model download, generation or release.
                    session = self.session(
                        refresh=True, endpoint=session.endpoint, force_rotate=True
                    )
                if not session:
                    raise RuntimeError("请先启动远端 GPU")
                request_path = ""
                remote_rel = ""
                if body is not None:
                    remote_name = "colabone-rpc-" + uuid.uuid4().hex + ".json"
                    remote_rel = "content/" + remote_name
                    with tempfile.TemporaryDirectory(prefix="colabone-rpc-") as tmp:
                        local = Path(tmp) / "request.json"
                        local.write_text(
                            json.dumps(body, ensure_ascii=False), encoding="utf-8"
                        )
                        local.chmod(0o600)
                        self._upload_file(
                            session,
                            str(local),
                            remote_rel,
                            timeout=(10.0, float(min(120, max(45, exec_timeout)))),
                        )
                    request_path = "/content/" + remote_name
                code = """
import json as _co_json, threading as _co_thr, urllib.request as _co_http
from pathlib import Path as _co_Path
_co_file = %r
_co_box = {}
try:
    _co_cfg = _co_json.loads(_co_Path('/content/colabone-boot.json').read_text(encoding="utf-8"))
    _co_body = _co_Path(_co_file).read_bytes() if _co_file else None
    _co_req = _co_http.Request('http://127.0.0.1:7860/api/v1/' + %r, data=_co_body, method=%r,
        headers={'Authorization': 'Bearer ' + _co_cfg['token'], 'Content-Type': 'application/json'})
    def _co_do():
        try:
            with _co_http.build_opener(_co_http.ProxyHandler({})).open(_co_req, timeout=%d) as _co_response:
                _co_box['res'] = {'ok':True, 'value':_co_json.loads(_co_response.read())}
        except _co_http.HTTPError as _co_error:
            _co_raw = _co_error.read()
            try:
                _co_detail = _co_json.loads(_co_raw).get('detail', '远端请求失败')
            except Exception:
                _co_detail = f'远端服务返回 HTTP {_co_error.code}'
            _co_box['res'] = {'ok':False, 'error':_co_detail}
        except Exception as _co_exc:
            _co_box['res'] = {'ok':False, 'error':str(_co_exc)[:300]}
    _co_t = _co_thr.Thread(target=_co_do, daemon=True)
    _co_t.start()
    while _co_t.is_alive():
        _co_t.join(5.0)
        if _co_t.is_alive():
            print('COLABONE_RPC_PING=1', flush=True)
    _co_result = _co_box.get('res') or {'ok':False, 'error':'远端服务未返回响应'}
    print('COLABONE_RPC=' + _co_json.dumps(_co_result, ensure_ascii=False), flush=True)
finally:
    if _co_file: _co_Path(_co_file).unlink(missing_ok=True)
""" % (request_path, path, method, http_timeout)
                try:
                    outputs = self.execute(session, code, timeout=exec_timeout)
                except Exception as exc:
                    if remote_rel:
                        self._delete_remote_file(session, remote_rel)
                    status = int(
                        getattr(getattr(exc, "response", None), "status_code", 0) or 0
                    )
                    if idempotent and attempt == 0 and (
                        _is_transient_ws_error(exc) or status in (401, 403)
                    ):
                        self.close_kernel_channel(session.endpoint)
                        if status in (401, 403):
                            self.session(
                                refresh=True,
                                endpoint=session.endpoint,
                                force_rotate=True,
                            )
                        continue
                    raise
                stdout_combined = "".join(
                    str(output.get("text", ""))
                    for output in (outputs or [])
                    if isinstance(output, dict)
                    and not (
                        output.get("output_type") == "stream"
                        and output.get("name") == "stderr"
                    )
                )
                for line in stdout_combined.splitlines():
                    if line.startswith("COLABONE_RPC="):
                        result = json.loads(line.partition("=")[2])
                        if not result["ok"]:
                            raise RuntimeError(safe_message(result["error"])[:500])
                        return result["value"]
                if idempotent and attempt == 0:
                    self.close_kernel_channel(session.endpoint)
                    continue
                raise RuntimeError("Colab 控制连接没有返回结果，请检查网络后重试")

    def heartbeat(self, endpoint: str | None = None):
        session = self.session(refresh=True, endpoint=endpoint)
        if not session:
            return
        slot = self._kernel_slot(session.endpoint)
        with slot["lock"]:
            # Cheap TFE ping plus the ColabOne kernel heartbeat. No local tool bridge.
            client = self.client()
            client.session.get(
                f"{client.colab_domain.rstrip('/')}/tun/m/{session.endpoint}/keep-alive/",
                headers={
                    "X-Colab-Tunnel": "Google",
                    "X-Colab-Client-Agent": "colab-cli",
                },
                timeout=10,
            )
            rem = self.remote_info(session.endpoint) or {}
            with self.state_lock:
                already_probed = session.endpoint in self._loc_probed
            if rem and not rem.get("location") and not already_probed:
                loc = infer_location_from_endpoint(
                    session.endpoint, str(getattr(session, "url", "") or "")
                )
                if not loc:
                    probe_code = """
import json as _co_j, urllib.request as _co_u
def _co_loc():
    _op = _co_u.build_opener(_co_u.ProxyHandler({}))
    try:
        with _op.open(_co_u.Request('http://metadata.google.internal/computeMetadata/v1/instance/zone', headers={'Metadata-Flavor':'Google'}), timeout=1.5) as _r:
            if _r.status == 200:
                return {'zone': _r.read().decode('utf-8','replace').strip()}
    except Exception:
        pass
    for _url in ('https://ipinfo.io/json', 'https://api.country.is/'):
        try:
            with _op.open(_url, timeout=3) as _r:
                if _r.status == 200:
                    return _co_j.loads(_r.read())
        except Exception:
            pass
    try:
        with _op.open('https://www.cloudflare.com/cdn-cgi/trace', timeout=3) as _r:
            for _ln in _r.read().decode('utf-8','replace').splitlines():
                if _ln.startswith('loc='):
                    return {'country': _ln[4:].strip()}
    except Exception:
        pass
    return {}
print('COLABONE_LOC=' + _co_j.dumps(_co_loc()))
"""
                    outputs = self.execute(session, probe_code, timeout=30)
                    stdout_combined = "".join(
                        str(out.get("text", ""))
                        for out in (outputs or [])
                        if isinstance(out, dict)
                        and not (
                            out.get("output_type") == "stream"
                            and out.get("name") == "stderr"
                        )
                    )
                    for line in stdout_combined.splitlines():
                        if line.startswith("COLABONE_LOC="):
                            raw = json.loads(line.partition("=")[2])
                            loc = location_from_zone(
                                str(raw.get("zone") or "")
                            ) or normalize_geo_payload(
                                str(raw.get("country") or ""),
                                str(raw.get("region") or ""),
                                str(raw.get("city") or ""),
                            )
                with self.state_lock:
                    self._loc_probed.add(session.endpoint)
                if loc and rem.get("endpoint") == session.endpoint:
                    self.save_remote(rem | {"location": loc})
            else:
                self.execute(session, "pass", timeout=30)
