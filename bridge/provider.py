"""Colab allocation/heartbeat adapted from Colab-yoga (Apache-2.0).
Only sessions stored by this ColabOne profile may be operated on automatically.
"""

from __future__ import annotations
import inspect
import json
import secrets
import tempfile
import time
import uuid
import threading
import zipfile
from pathlib import Path

import requests
from google.auth.transport.requests import AuthorizedSession
from colab_cli.client import (
    Client,
    Prod,
    Accelerator,
    Variant,
    Shape,
    resolve_assign_shape,
    ColabRequestError,
)
from colab_cli.state import SessionState
from colab_cli.contents import ContentsClient
from colab_cli.runtime import ColabRuntime
from common.location import (
    infer_location_from_endpoint,
    location_from_zone,
    normalize_geo_payload,
)


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


class ColabOneProvider:
    def __init__(self, auth, vault, root):
        self.auth, self.vault, self.root = auth, vault, root
        self.kernel_lock = threading.RLock()
        self.state_lock = threading.RLock()
        self._kernels: dict[str, dict] = {}
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
            raw_map = self.vault.get("runtime_sessions")
            sessions = dict(raw_map) if isinstance(raw_map, dict) else {}
            legacy = self.vault.get("runtime_session")
            if isinstance(legacy, dict) and legacy.get("endpoint"):
                sessions[legacy["endpoint"]] = legacy
            return sessions

    def _remotes_map(self) -> dict[str, dict]:
        with self.state_lock:
            raw_map = self.vault.get("remotes")
            remotes = dict(raw_map) if isinstance(raw_map, dict) else {}
            legacy = self.vault.get("remote")
            if isinstance(legacy, dict) and legacy.get("endpoint"):
                remotes[legacy["endpoint"]] = legacy
            return remotes

    def _write_state(
        self,
        sessions: dict[str, dict],
        remotes: dict[str, dict],
        active_ep: str | None = None,
    ):
        with self.state_lock:
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
                self.vault.set("runtime_sessions", sessions)
                self.vault.set("active_endpoint", chosen)
                self.vault.set("runtime_session", sessions[chosen])
            else:
                chosen = None
                self.vault.delete("runtime_sessions")
                self.vault.delete("active_endpoint")
                self.vault.delete("runtime_session")
            if remotes:
                self.vault.set("remotes", remotes)
                if chosen and chosen in remotes:
                    self.vault.set("remote", remotes[chosen])
                else:
                    self.vault.delete("remote")
            else:
                self.vault.delete("remotes")
                self.vault.delete("remote")

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

    def account(self):
        if not self.vault.get("colab"):
            return {"logged_in": False}
        client = self.client()
        info = client.get_consumption_user_info()
        managed_set = set(self.managed_endpoints())
        active_ep = self.active_endpoint()
        instances = [
            describe_assignment(a, managed_set, active_ep)
            for a in client.list_assignments()
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

    def session(self, refresh=False, endpoint: str | None = None):
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
        if session.endpoint in endpoints:
            _emit_log(on_log, f"向 Google Colab 发送释放请求：{session.endpoint}", "GPU")
            try:
                client.unassign(session.endpoint)
            except Exception:
                # The independent remote guard may already have released it.
                # An HTTP error is not evidence either way: confirm disappearance.
                _emit_log(
                    on_log,
                    f"释放请求未获直接确认，改为轮询核实停止状态：{session.endpoint}",
                    "GPU",
                    "warning",
                )
        else:
            _emit_log(on_log, f"实例已不在 Colab 活跃列表中：{session.endpoint}", "GPU")
        for attempt in range(15):
            if session.endpoint not in {a.endpoint for a in client.list_assignments()}:
                self.clear(session.endpoint)
                self.close_kernel_channel(session.endpoint)
                _emit_log(
                    on_log,
                    f"已确认实例停止并清理本机会话：{session.endpoint}",
                    "GPU",
                )
                return {"ok": True}
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
        for endpoint in to_stop:
            notify("正在停止实例：" + endpoint)
            try:
                client.unassign(endpoint)
            except Exception:
                notify("停止请求未获确认，正在重新查询：" + endpoint)
        confirmed: set[str] = set()
        for attempt in range(15):
            current = {a.endpoint for a in client.list_assignments()}
            for ep in sorted(targets - current - confirmed):
                confirmed.add(ep)
                notify("已确认停止实例：" + ep)
            for owned in list(self.managed_endpoints()):
                if owned in targets and owned not in current:
                    self.clear(owned)
                    self.close_kernel_channel(owned)
            remaining = targets & current
            if not remaining:
                return {"ok": True, "remaining": len(current)}
            if attempt == 0:
                notify(f"等待 Google Colab 确认停止计费（剩余 {len(remaining)} 个）")
            if attempt < 14:
                time.sleep(2)
        raise RuntimeError(
            f"仍有 {len(remaining)} 个实例未确认停止，可能仍在计费，请重试"
        )

    def allocate(self, gpu, on_log=None):
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
        try:
            result = client.assign(
                uuid.uuid4(), variant=Variant.GPU, accelerator=accel, shape=shape
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
            result = client.assign(
                uuid.uuid4(), variant=Variant.GPU, accelerator=accel, shape=None
            )
            shape = None
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
        self.save_session(session, activate=True)
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
            slot["runtime"] = ColabRuntime(
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

    def close_kernel_channel(self, endpoint: str | None = None):
        with self.kernel_lock:
            targets = [endpoint] if endpoint else list(self._kernels.keys())
        for ep in targets:
            slot = self._kernels.get(ep)
            if not slot:
                continue
            with slot["lock"]:
                if slot["runtime"]:
                    slot["runtime"].stop(shutdown_kernel=False)
                slot["runtime"], slot["key"] = None, None

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
        before = self.account() or {}
        if before.get("logged_in"):
            _emit_log(
                on_log,
                f"当前余额 {float(before.get('balance') or 0):.2f} CU · 活跃实例 {int(before.get('assignments') or 0)} 台",
                "计费",
            )
        try:
            notify("正在分配 " + gpu)
            started = time.time()
            if _accepts_kwarg(self.allocate, "on_log"):
                session = self.allocate(gpu, on_log=on_log)
            else:
                session = self.allocate(gpu)
            allocated_endpoint = session.endpoint
            fresh = True
            account = self.account() or {}
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
                    for folder in ("server", "common", "catalog"):
                        for path in (self.root / folder).glob("*"):
                            if path.is_file() and path.suffix in (
                                ".py",
                                ".json",
                                ".txt",
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
                contents = ContentsClient(session)
                _emit_log(on_log, "正在上传管理服务程序包 (colabone.zip)", "GPU")
                contents.upload(str(package), "content/colabone.zip")
                _emit_log(on_log, "正在上传远端启动配置 (colabone-boot.json)", "GPU")
                contents.upload(str(config), "content/colabone-boot.json")
                if rclone_conf:
                    rc_local = Path(tmp) / "colabone-rclone.conf"
                    rc_local.write_text(rclone_conf, encoding="utf-8")
                    rc_local.chmod(0o600)
                    _emit_log(on_log, "正在上传 Google 云盘授权配置", "云盘")
                    contents.upload(str(rc_local), "content/.colabone_rclone.conf")
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
                    for folder in ("server", "common", "catalog"):
                        for path in (self.root / folder).glob("*"):
                            if path.is_file() and path.suffix in (
                                ".py",
                                ".json",
                                ".txt",
                            ):
                                archive.write(path, f"colabone/{folder}/{path.name}")
                                file_count += 1
                _emit_log(
                    on_log,
                    f"已打包新版远端服务（{file_count} 个文件），正在上传",
                    "客户端",
                )
                ContentsClient(session).upload(
                    str(package), "content/colabone-update.zip"
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

                exec_kwargs = {"timeout": 120}
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

    def upload_drive_config(self, rclone_conf: str, endpoint: str | None = None):
        session = self.session(refresh=True, endpoint=endpoint)
        if not session:
            raise RuntimeError("请先启动远端 GPU")
        with tempfile.TemporaryDirectory(prefix="colabone-drive-") as tmp:
            rc_local = Path(tmp) / "colabone-rclone.conf"
            rc_local.write_text(rclone_conf, encoding="utf-8")
            rc_local.chmod(0o600)
            ContentsClient(session).upload(
                str(rc_local), "content/.colabone_rclone.conf"
            )

    def rpc(self, method, path, body=None, endpoint: str | None = None):
        """Use Colab's authenticated kernel channel; no public port or VPN account.

        Sensitive POST data is uploaded as an ephemeral file, never embedded in
        notebook source/history. GET paths contain only non-secret resource IDs.
        """
        session = self.session(endpoint=endpoint)
        if not session:
            raise RuntimeError("请先启动远端 GPU")
        slot = self._kernel_slot(session.endpoint)
        with slot["lock"]:
            session = self.session(endpoint=session.endpoint)
            if session and (
                session.token_expires_at is None
                or session.token_expires_at.timestamp() <= time.time() + 60
            ):
                # Refresh before uploading or executing. Retrying a failed POST
                # could duplicate a model download, generation or release.
                session = self.session(refresh=True, endpoint=session.endpoint)
            if not session:
                raise RuntimeError("请先启动远端 GPU")
            request_path = ""
            if body is not None:
                remote_name = "colabone-rpc-" + uuid.uuid4().hex + ".json"
                with tempfile.TemporaryDirectory(prefix="colabone-rpc-") as tmp:
                    local = Path(tmp) / "request.json"
                    local.write_text(
                        json.dumps(body, ensure_ascii=False), encoding="utf-8"
                    )
                    local.chmod(0o600)
                    ContentsClient(session).upload(str(local), "content/" + remote_name)
                request_path = "/content/" + remote_name
            code = """
import json as _co_json, urllib.request as _co_http
from pathlib import Path as _co_Path
_co_file = %r
try:
    _co_cfg = _co_json.loads(_co_Path('/content/colabone-boot.json').read_text(encoding="utf-8"))
    _co_body = _co_Path(_co_file).read_bytes() if _co_file else None
    _co_req = _co_http.Request('http://127.0.0.1:7860/api/v1/' + %r, data=_co_body, method=%r,
        headers={'Authorization': 'Bearer ' + _co_cfg['token'], 'Content-Type': 'application/json'})
    try:
        with _co_http.build_opener(_co_http.ProxyHandler({})).open(_co_req, timeout=65) as _co_response:
            _co_result = {'ok':True, 'value':_co_json.loads(_co_response.read())}
    except _co_http.HTTPError as _co_error:
        _co_raw = _co_error.read()
        try:
            _co_detail = _co_json.loads(_co_raw).get('detail', '远端请求失败')
        except Exception:
            _co_detail = f'远端服务返回 HTTP {_co_error.code}'
        _co_result = {'ok':False, 'error':_co_detail}
    print('COLABONE_RPC=' + _co_json.dumps(_co_result, ensure_ascii=False))
finally:
    if _co_file: _co_Path(_co_file).unlink(missing_ok=True)
""" % (request_path, path, method)
            outputs = self.execute(session, code, timeout=75)
            for output in outputs or []:
                for line in str(output.get("text", "")).splitlines():
                    if line.startswith("COLABONE_RPC="):
                        result = json.loads(line.partition("=")[2])
                        if not result["ok"]:
                            raise RuntimeError(str(result["error"])[:500])
                        return result["value"]
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
            if rem and not rem.get("location") and session.endpoint not in self._loc_probed:
                self._loc_probed.add(session.endpoint)
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
                    for out in outputs or []:
                        for line in str(out.get("text", "")).splitlines():
                            if line.startswith("COLABONE_LOC="):
                                raw = json.loads(line.partition("=")[2])
                                loc = location_from_zone(
                                    str(raw.get("zone") or "")
                                ) or normalize_geo_payload(
                                    str(raw.get("country") or ""),
                                    str(raw.get("region") or ""),
                                    str(raw.get("city") or ""),
                                )
                if loc and rem.get("endpoint") == session.endpoint:
                    self.save_remote(rem | {"location": loc})
            else:
                self.execute(session, "pass", timeout=30)
