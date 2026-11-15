from __future__ import annotations
import copy
import json
import os
import sys
import threading
import time
from pathlib import Path

import requests

from bridge.auth import Auth, Vault
from bridge.provider import Provider
from common.files import atomic_json
from common.models import resolve_import


def config_directory():
    if os.environ.get("COLABONE_CONFIG_DIR"):
        return Path(os.environ["COLABONE_CONFIG_DIR"])
    if sys.platform == "darwin":
        return Path.home() / "Library/Application Support/ColabOne"
    return (
        Path(os.environ.get("LOCALAPPDATA", str(Path.home() / ".local/share")))
        / "ColabOne"
    )


class Controller:
    def __init__(self, root, vault=None, config_dir=None, background=True):
        self.root = root
        self.vault = vault or Vault()
        self.auth = Auth(self.vault)
        self.provider = Provider(self.auth, self.vault, root)
        self.directory = config_dir or config_directory()
        self.settings = {
            "gpu": "T4",
            "max_cu": 5,
            "idle_minutes": 30,
            "auto_drive": True,
        }
        if (self.directory / "settings.json").exists():
            self.settings.update(
                json.loads(
                    (self.directory / "settings.json").read_text(encoding="utf-8")
                )
            )
        self.lock = threading.RLock()
        self.operation_lock = threading.Lock()
        self.stop = threading.Event()
        self.data = {
            "runtime": {"state": "stopped"},
            "account": {"logged_in": False},
            "models": self.presets(),
            "conversations": [],
            "operation": None,
            "error": "",
            "auth_url": "",
        }
        self.last_auth = self.last_config = self.last_heartbeat = 0
        self.mount_cancel = threading.Event()
        if background:
            threading.Thread(target=self.monitor, daemon=True).start()

    def presets(self):
        return [
            m
            | {
                "downloaded": False,
                "loaded": False,
                "size": sum(f["size"] for f in m["files"]),
            }
            for m in json.loads(
                (self.root / "catalog/presets.json").read_text(encoding="utf-8")
            )
        ]

    def snapshot(self):
        with self.lock:
            value = copy.deepcopy(self.data)
        value["settings"] = self.settings.copy()
        value["setup"] = {
            "colab": bool(self.vault.get("colab")),
            "drive": bool(value["runtime"].get("sync", {}).get("connected")),
            "hf": bool(self.vault.get("hf")),
            "drive_waiting": bool(value.get("drive_waiting")),
        }
        return value

    def update(self, **values):
        with self.lock:
            self.data.update(values)

    def async_operation(self, title, fn):
        if not self.operation_lock.acquire(blocking=False):
            raise RuntimeError("请等待当前操作完成")
        self.update(operation={"title": title, "running": True}, error="", auth_url="")

        def run():
            try:
                fn()
            except Exception as exc:
                # HTTP exception URLs may contain OAuth codes: surface only a safe reason.
                if isinstance(exc, requests.HTTPError):
                    message = (
                        f"服务返回 HTTP {exc.response.status_code}，请检查授权和配置"
                    )
                else:
                    message = str(exc)[:500]
                self.update(error=message)
            finally:
                self.update(operation=None)
                self.operation_lock.release()

        threading.Thread(target=run, daemon=True).start()
        return {"ok": True}

    def remote(self, method, path, body=None):
        return self.provider.rpc(method, path, body)

    def connect_drive(self):
        self.mount_cancel.clear()

        def consent(url):
            self.update(
                auth_url=url,
                drive_waiting=True,
                operation={"title": "请在浏览器确认连接 Google 云盘", "running": True},
            )

        try:
            self.provider.mount_drive(consent, self.mount_cancel)
            self.remote("POST", "restore")
            self.remote("POST", "sync")
            self.refresh_remote()
        finally:
            self.update(auth_url="", drive_waiting=False)

    def configure_remote(self):
        remote = self.vault.get("remote") or {}
        self.remote(
            "POST",
            "configure",
            {
                "idle_minutes": self.settings["idle_minutes"],
                "deadline": remote.get("deadline", 0),
            },
        )
        self.last_config = time.time()

    def refresh_remote(self):
        state = self.remote("GET", "snapshot")
        self.update(
            runtime=state["runtime"],
            models=state["models"],
            conversations=state["conversations"],
        )

    def start(self):
        self.auth.credentials("colab")

        def notify(message):
            self.update(
                runtime={"state": "starting"},
                operation={"title": message, "running": True},
            )

        existing = self.vault.get("runtime_session")
        try:
            self.provider.start(self.settings["gpu"], self.settings["max_cu"], notify)
            self.configure_remote()
            self.refresh_remote()
        except Exception:
            if not existing and self.vault.get("runtime_session"):
                try:
                    self.provider.release()
                except Exception:
                    pass
            remaining = self.vault.get("runtime_session")
            self.update(runtime={"state": "disconnected" if remaining else "stopped"})
            raise

        if self.settings.get("auto_drive", True):
            try:
                self.connect_drive()
            except Exception as exc:
                self.update(error=str(exc)[:300])

    def release(self, force=False):
        if not force:
            self.remote("POST", "prepare-release")
        self.update(runtime={"state": "releasing"})
        self.provider.release()
        with self.lock:
            for m in self.data["models"]:
                m["downloaded"] = m["loaded"] = False
        self.update(runtime={"state": "stopped"})

    def import_model(self, payload):
        spec = resolve_import(
            payload["repo"].strip(),
            payload["filename"].strip(),
            payload.get("revision", "main").strip() or "main",
            self.vault.get("hf", ""),
        )
        self.remote("POST", "models", spec.model_dump())
        self.remote("POST", "sync")
        self.refresh_remote()

    def save_settings(self, payload):
        public = {
            k: payload[k]
            for k in ("gpu", "idle_minutes", "max_cu", "auto_drive")
            if k in payload
        }
        if public.get("gpu", self.settings["gpu"]) not in (
            "T4",
            "L4",
            "A100",
            "G4",
            "H100",
        ):
            raise ValueError("GPU 型号不合法")
        if (
            not 1
            <= int(public.get("idle_minutes", self.settings["idle_minutes"]))
            <= 120
        ):
            raise ValueError("空闲时间必须在 1 至 120 分钟之间")
        if not 0 < float(public.get("max_cu", self.settings["max_cu"])) <= 100:
            raise ValueError("CU 预算必须大于 0 且不超过 100")
        if payload.get("hf_token"):
            self.vault.set("hf", payload["hf_token"])
        if "auto_drive" in public and not isinstance(public["auto_drive"], bool):
            raise ValueError("自动连接云盘设置不合法")
        self.settings.update(public)
        atomic_json(self.directory / "settings.json", self.settings)
        self.last_config = 0
        return {"ok": True}

    def command(self, action, payload):
        if action == "settings.save":
            return self.save_settings(payload)
        if action == "auth.colab.begin":
            result = self.auth.colab_begin()
            self.update(auth_url=result["url"])
            return result
        if action == "auth.colab.auto":

            def login():
                self.auth.colab_login(lambda url: self.update(auth_url=url))
                self.last_auth = 0
                self.update(conversations=[], models=self.presets(), auth_url="")

            return self.async_operation("等待 Google 登录", login)
        if action == "auth.colab.finish":

            def finish():
                self.auth.colab_finish(payload["code"])
                self.last_auth = 0
                self.update(conversations=[], models=self.presets(), auth_url="")

            if self.operation_lock.locked():
                # A paste can finish a pending loopback login; its operation then
                # completes the login once the callback wait exits.
                self.auth.colab_finish(payload["code"])
                self.last_auth = 0
                return {"ok": True}
            return self.async_operation("完成 Google 登录", finish)
        if action == "drive.connect":
            return self.async_operation("连接 Google 云盘", self.connect_drive)
        if action == "drive.skip":
            self.mount_cancel.set()
            return {"ok": True}
        if action == "runtime.start":
            return self.async_operation("启动远端 GPU", self.start)
        if action == "runtime.release":
            return self.async_operation(
                "释放远端 GPU", lambda: self.release(bool(payload.get("force")))
            )
        if action == "model.import":
            return self.async_operation(
                "读取 Hugging Face 模型清单", lambda: self.import_model(payload)
            )
        if action == "model.download":
            return self.remote(
                "POST",
                "models/" + self.model_id(payload) + "/download",
                {"hf_token": self.vault.get("hf", "")},
            )
        if action == "model.load":
            return self.remote("POST", "models/" + self.model_id(payload) + "/load")
        if action == "model.unload":
            return self.remote("POST", "unload")
        if action == "model.delete":
            return self.remote("DELETE", "models/" + self.model_id(payload))
        if action == "chat.send":
            result = self.remote("POST", "turns", payload)
            self.follow_chat(result, payload["text"])
            return result
        if action == "task.cancel":
            import re

            if not re.fullmatch(r"[a-f0-9]{32}", payload["id"]):
                raise ValueError("任务 ID 不合法")
            return self.remote("DELETE", "tasks/" + payload["id"])
        if action == "sync":

            def sync():
                if self.vault.get("runtime_session"):
                    self.configure_remote()
                    result = self.remote("POST", "sync")
                    if not result["ok"]:
                        raise RuntimeError(result["error"])
                    self.refresh_remote()
                else:
                    raise RuntimeError("请先启动 GPU 并连接云盘，才能恢复或同步会话")

            return self.async_operation("同步 Google Drive", sync)
        if action == "error.clear":
            self.update(error="")
            return {"ok": True}
        raise ValueError("不支持的操作")

    def follow_chat(self, result, text):
        stream = {
            "conversation_id": result["conversation_id"],
            "text": text,
            "content": "",
            "state": "running",
        }
        self.update(stream=stream)

        def pump():
            cursor = 0
            failures = 0
            while not self.stop.is_set():
                try:
                    batch = self.remote(
                        "GET", f"tasks/{result['id']}/poll?after={cursor}"
                    )
                    failures = 0
                    for item in batch["events"]:
                        if item["seq"] <= cursor:
                            continue
                        cursor = item["seq"]
                        if item["event"] == "delta":
                            stream["content"] += item["data"].get("content", "")
                    stream["state"] = batch["state"]
                    self.update(stream=stream.copy())
                    if batch["state"] != "running":
                        self.refresh_remote()
                        return
                    self.stop.wait(0.2)
                except Exception:
                    failures += 1
                    if failures >= 4:
                        stream["state"] = "disconnected"
                        self.update(stream=stream.copy())
                        return
                    self.stop.wait(1)

        threading.Thread(target=pump, daemon=True).start()

    @staticmethod
    def model_id(payload):
        import re

        value = payload["id"]
        if not re.fullmatch(r"[a-zA-Z0-9_-]{1,80}", value):
            raise ValueError("模型 ID 不合法")
        return value

    def monitor(self):
        while not self.stop.is_set():
            try:
                now = time.time()
                if now - self.last_auth > 30:
                    self.update(account=self.provider.account())
                    self.last_auth = now
                remote = self.vault.get("remote")
                if remote and not self.operation_lock.locked():
                    account = self.data["account"]
                    spent = max(
                        0,
                        remote.get("start_balance", account.get("balance", 0))
                        - account.get("balance", 0),
                    )
                    # A balance guard complements the independent remote deadline.
                    if now >= remote["deadline"] or spent >= remote["max_cu"] * 0.8:
                        self.async_operation(
                            "已到预算保护阈值，正在释放", lambda: self.release(True)
                        )
                        continue
                    if now - self.last_config > 600:
                        self.configure_remote()
                    try:
                        self.refresh_remote()
                    except Exception:
                        self.update(
                            runtime={"state": "disconnected", "id": remote["endpoint"]}
                        )
                    if now - self.last_heartbeat > 30:
                        self.provider.heartbeat()
                        self.last_heartbeat = now
                        if not self.vault.get("runtime_session"):
                            self.update(
                                runtime={"state": "stopped"}, models=self.presets()
                            )
                if self.data.get("error", "").startswith("连接检查失败："):
                    self.update(error="")
            except Exception as exc:
                self.update(
                    error="连接检查失败："
                    + type(exc).__name__
                    + "。请检查网络或重新登录。"
                )
            self.stop.wait(2)
