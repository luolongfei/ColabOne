"""Colab allocation/heartbeat adapted from Colab-yoga (Apache-2.0).
Only the session stored by this ColabOne profile may be operated on.
"""

from __future__ import annotations
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
from bridge.drive_mount import DriveMount


class Provider:
    def __init__(self, auth, vault, root):
        self.auth, self.vault, self.root = auth, vault, root
        self.kernel_lock = threading.RLock()
        self.kernel_runtime = None
        self.kernel_key = None
        self.client_factory = lambda: Client(
            Prod(), AuthorizedSession(auth.credentials("colab"))
        )

    def client(self):
        return self.client_factory()

    def account(self):
        if not self.vault.get("colab"):
            return {"logged_in": False}
        client = self.client()
        info = client.get_consumption_user_info()
        return {
            "logged_in": True,
            "email": self.auth.profile().get("email", ""),
            "balance": float(info.paid_compute_units_balance),
            "rate": float(info.consumption_rate_hourly),
            "assignments": int(info.assignments_count),
        }

    def session(self, refresh=False):
        data = self.vault.get("runtime_session")
        if not data:
            return None
        session = SessionState.model_validate(data)
        if refresh:
            assignments = self.client().list_assignments()
            assignment = next(
                (a for a in assignments if a.endpoint == session.endpoint), None
            )
            if assignment is None:
                self.clear()
                return None
            info = assignment.runtime_proxy_info
            session.url, session.token, session.token_expires_at = (
                info.url,
                info.token,
                info.expires_at(),
            )
            self.save_session(session)
        return session

    def save_session(self, session):
        self.vault.set("runtime_session", session.model_dump(mode="json"))

    def clear(self):
        self.vault.delete("runtime_session")
        self.vault.delete("remote")

    def release(self):
        session = self.session()
        if not session:
            return {"ok": True}
        client = self.client()
        endpoints = {a.endpoint for a in client.list_assignments()}
        if session.endpoint in endpoints:
            client.unassign(session.endpoint)
        for _ in range(15):
            if session.endpoint not in {a.endpoint for a in client.list_assignments()}:
                self.clear()
                return {"ok": True}
            time.sleep(2)
        raise RuntimeError("释放请求已发送，但尚未确认停止计费；请刷新后重试")

    def allocate(self, gpu):
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
        client = self.client()
        try:
            result = client.assign(
                uuid.uuid4(), variant=Variant.GPU, accelerator=accel, shape=shape
            )
        except ColabRequestError as e:
            if shape is None or getattr(e.response, "status_code", 0) != 400:
                raise
            result = client.assign(
                uuid.uuid4(), variant=Variant.GPU, accelerator=accel, shape=None
            )
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
        self.save_session(session)
        return session

    def execute(self, session, code, timeout=300, hook=None):
        with self.kernel_lock:
            return self._execute(session, code, timeout, hook)

    def _execute(self, session, code, timeout=300, hook=None):
        def kid(value):
            session.kernel_id = value
            self.save_session(session)

        def sid(value):
            session.session_id = value
            self.save_session(session)

        key = (session.endpoint, session.url, session.token)
        if self.kernel_key != key:
            self.close_kernel_channel()
        if self.kernel_runtime is None:
            self.kernel_runtime = ColabRuntime(
                session.url,
                session.token,
                session_name="colabone",
                kernel_id=session.kernel_id,
                session_id=session.session_id,
                on_kernel_started=kid,
                on_session_started=sid,
            )
            self.kernel_key = key
        rt = self.kernel_runtime
        rt.colab_request_hook = hook
        try:
            return rt.execute_code(code, allow_stdin=False, timeout=timeout)
        except Exception:
            # Reconnect on the next request; never replay a mutation whose outcome
            # is unknown. Closing the websocket must not shut down the kernel.
            self.close_kernel_channel()
            raise

    def close_kernel_channel(self):
        if self.kernel_runtime:
            self.kernel_runtime.stop(shutdown_kernel=False)
        self.kernel_runtime, self.kernel_key = None, None

    @staticmethod
    def ready(session):
        deadline = time.monotonic() + 90
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
                    return
            except requests.RequestException:
                pass
            time.sleep(2)
        raise RuntimeError("远端内核未能在 90 秒内就绪")

    def start(self, gpu, max_cu, notify):
        fresh = False
        session = self.session(refresh=True)
        if session:
            remote = self.vault.get("remote")
            if remote and remote.get("endpoint") == session.endpoint:
                return remote
            raise RuntimeError("实例存在但部署未完成，请先释放后重新启动")
        before = self.account()
        try:
            notify("正在分配 " + gpu)
            started = time.time()
            session = self.allocate(gpu)
            fresh = True
            rate = self.account()["rate"]
            if rate <= 0 or max_cu <= 0:
                raise RuntimeError("无法确认当前 CU 费率，已取消启动")
            # Account rate includes any other assignments, making this conservative.
            deadline = started + max(0, (max_cu * 0.8 / rate) * 3600 - 60)
            if deadline - time.time() < 120:
                raise RuntimeError("当前费率下预算不足以完成初始化")
            remote = {
                "endpoint": session.endpoint,
                "token": secrets.token_urlsafe(32),
                "transport": "colab",
                "started_at": started,
                "deadline": deadline,
                "start_balance": before["balance"],
                "max_cu": max_cu,
                "rate": rate,
            }
            self.vault.set("remote", remote)
            notify("等待远端内核")
            self.ready(session)
            notify("部署远端管理服务")
            with tempfile.TemporaryDirectory(prefix="colabone-upload-") as tmp:
                package = Path(tmp) / "colabone.zip"
                with zipfile.ZipFile(package, "w", zipfile.ZIP_DEFLATED) as archive:
                    for folder in ("server", "common", "catalog"):
                        for path in (self.root / folder).glob("*"):
                            if path.is_file() and path.suffix in (
                                ".py",
                                ".json",
                                ".txt",
                            ):
                                archive.write(path, f"colabone/{folder}/{path.name}")
                config = Path(tmp) / "boot-config.json"
                config.write_text(
                    json.dumps(
                        {
                            "token": remote["token"],
                            "runtime_id": session.endpoint,
                            "root": "/content/colabone-data",
                            "deadline": deadline,
                        }
                    )
                )
                config.chmod(0o600)
                contents = ContentsClient(session)
                contents.upload(str(package), "content/colabone.zip")
                contents.upload(str(config), "content/colabone-boot.json")
                outputs = self.execute(
                    session,
                    (self.root / "server/vm_boot.py").read_text(encoding="utf-8"),
                    timeout=300,
                )
            ready = False
            for output in outputs or []:
                for line in str(output.get("text", "")).splitlines():
                    if line.strip() == "COLABONE_READY=1":
                        ready = True
            if not ready:
                raise RuntimeError("远端初始化未完成；可查看 Colab 内核日志")
            self.vault.set("remote", remote)
            return remote
        except Exception as exc:
            if fresh:
                try:
                    self.release()
                except Exception:
                    raise RuntimeError(
                        "启动失败且释放未确认。实例记录已保留，请在设备页立即重试释放"
                    ) from exc
            raise

    def mount_drive(self, notify, cancel):
        with self.kernel_lock:
            session = self.session(refresh=True)
            if not session:
                raise RuntimeError("请先启动远端 GPU")
            remote = self.vault.get("remote") or {}
            timeout = min(
                300, int(remote.get("deadline", time.time()) - time.time() - 45)
            )
            if timeout < 30:
                raise RuntimeError("本次 GPU 预算即将用完，请先释放后重新启动")
            mount = DriveMount(self.client(), session.endpoint, notify, cancel, timeout)
            code = """
import os as _co_os
if not _co_os.path.ismount('/content/drive'):
    from google.colab import drive as _co_drive
    _co_drive.mount('/content/drive', timeout_ms=%d)
if _co_os.path.ismount('/content/drive') and _co_os.path.isdir('/content/drive/MyDrive'):
    print('COLABONE_DRIVE_READY=1')
""" % ((timeout + 20) * 1000)
            try:
                outputs = self.execute(
                    session, code, timeout=timeout + 30, hook=mount.hook
                )
                if mount.error:
                    raise RuntimeError(mount.error)
                if not any(
                    "COLABONE_DRIVE_READY=1" in str(o.get("text", ""))
                    for o in outputs or []
                ):
                    raise RuntimeError(
                        "云盘尚未连接成功，请在设置中重试；GPU 可以继续使用"
                    )
            finally:
                cancel.set()

    def rpc(self, method, path, body=None):
        """Use Colab's authenticated kernel channel; no public port or VPN account.

        Sensitive POST data is uploaded as an ephemeral file, never embedded in
        notebook source/history. GET paths contain only non-secret resource IDs.
        """
        with self.kernel_lock:
            session = self.session()
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
        _co_result = {'ok':False, 'error':_co_json.loads(_co_error.read()).get('detail','远端请求失败')}
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

    def heartbeat(self):
        session = self.session(refresh=True)
        if session:
            # Cheap TFE ping plus the kernel heartbeat reused from Yoga. No local tool bridge.
            client = self.client()
            client.session.get(
                f"{client.colab_domain.rstrip('/')}/tun/m/{session.endpoint}/keep-alive/",
                headers={
                    "X-Colab-Tunnel": "Google",
                    "X-Colab-Client-Agent": "colab-cli",
                },
                timeout=10,
            )
            self.execute(session, "pass", timeout=30)
