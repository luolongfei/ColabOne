"""Colab allocation/heartbeat adapted from Colab-yoga (Apache-2.0).
Only the session stored by this ColabOne profile may be operated on.
"""

from __future__ import annotations
import json
import secrets
import tempfile
import time
import uuid
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


class Provider:
    def __init__(self, auth, vault, root):
        self.auth, self.vault, self.root = auth, vault, root
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

    def execute(self, session, code, timeout=300):
        def kid(value):
            session.kernel_id = value
            self.save_session(session)

        def sid(value):
            session.session_id = value
            self.save_session(session)

        rt = ColabRuntime(
            session.url,
            session.token,
            session_name="colabone",
            kernel_id=session.kernel_id,
            session_id=session.session_id,
            on_kernel_started=kid,
            on_session_started=sid,
        )
        try:
            return rt.execute_code(code, allow_stdin=False, timeout=timeout)
        finally:
            rt.stop(shutdown_kernel=False)

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

    def tailscale_key(self):
        config = self.vault.get("tailscale") or {}
        if not config.get("client_id") or not config.get("client_secret"):
            raise RuntimeError("请先配置 Tailscale OAuth 客户端")
        r = requests.post(
            "https://api.tailscale.com/api/v2/oauth/token",
            data={
                "client_id": config["client_id"],
                "client_secret": config["client_secret"],
            },
            timeout=20,
        )
        r.raise_for_status()
        r = requests.post(
            "https://api.tailscale.com/api/v2/tailnet/-/keys",
            headers={"Authorization": "Bearer " + r.json()["access_token"]},
            json={
                "capabilities": {
                    "devices": {
                        "create": {
                            "reusable": False,
                            "ephemeral": True,
                            "preauthorized": True,
                            "tags": [config.get("tag", "tag:colabone")],
                        }
                    }
                },
                "expirySeconds": 600,
            },
            timeout=20,
        )
        r.raise_for_status()
        return r.json()["key"]

    def start(self, gpu, max_cu, notify):
        fresh = False
        # Obtain an ephemeral key before starting the meter, so missing network setup is free.
        ts_key = self.tailscale_key()
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
                "url": "",
                "started_at": started,
                "deadline": deadline,
                "start_balance": before["balance"],
                "max_cu": max_cu,
                "rate": rate,
            }
            self.vault.set("remote", remote)
            notify("等待远端内核")
            self.ready(session)
            notify("部署管理服务与私有网络")
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
                            "ts_key": ts_key,
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
                    session, (self.root / "server/vm_boot.py").read_text(), timeout=300
                )
            url = None
            for output in outputs or []:
                for line in str(output.get("text", "")).splitlines():
                    if line.startswith("COLABONE_URL="):
                        url = line.partition("=")[2].strip()
            if not url:
                raise RuntimeError("远端初始化未返回服务地址；可查看 Colab 内核日志")
            remote["url"] = url
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
