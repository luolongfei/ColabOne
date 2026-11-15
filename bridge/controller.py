from __future__ import annotations
import copy
import json
import os
import shutil
import subprocess
import sys
import threading
import time
from pathlib import Path
from urllib.parse import urlparse
import ipaddress

import requests

from bridge.auth import Auth, Vault
from bridge.provider import Provider
from common.files import atomic_json
from common.models import resolve_import
from common.storage import DriveStore


def config_directory():
    if os.environ.get("COLABONE_CONFIG_DIR"):
        return Path(os.environ["COLABONE_CONFIG_DIR"])
    if sys.platform == "darwin":
        return Path.home() / "Library/Application Support/ColabOne"
    return (
        Path(os.environ.get("LOCALAPPDATA", str(Path.home() / ".local/share")))
        / "ColabOne"
    )


def tailscale_status():
    candidates = [
        shutil.which("tailscale"),
        "/Applications/Tailscale.app/Contents/MacOS/Tailscale",
        os.path.join(
            os.environ.get("ProgramFiles", "C:\\Program Files"),
            "Tailscale",
            "tailscale.exe",
        ),
    ]
    executable = next((p for p in candidates if p and Path(p).is_file()), None)
    if not executable:
        return {"installed": False, "running": False}
    try:
        result = subprocess.run(
            [executable, "status", "--json"], capture_output=True, text=True, timeout=5
        )
        status = json.loads(result.stdout)
        return {"installed": True, "running": status.get("BackendState") == "Running"}
    except (OSError, ValueError, subprocess.SubprocessError):
        return {"installed": True, "running": False}


class Controller:
    def __init__(self, root, vault=None, config_dir=None, background=True):
        self.root = root
        self.vault = vault or Vault()
        self.auth = Auth(self.vault)
        self.provider = Provider(self.auth, self.vault, root)
        self.directory = config_dir or config_directory()
        self.settings = {"gpu": "T4", "max_cu": 5, "idle_minutes": 30}
        if (self.directory / "settings.json").exists():
            self.settings.update(
                json.loads((self.directory / "settings.json").read_text())
            )
        self.lock = threading.RLock()
        self.operation_lock = threading.Lock()
        self.stop = threading.Event()
        self.http = requests.Session()
        self.http.trust_env = False
        self.data = {
            "runtime": {"state": "stopped"},
            "account": {"logged_in": False},
            "models": self.presets(),
            "conversations": [],
            "operation": None,
            "error": "",
            "auth_url": "",
            "network": {"installed": False, "running": False},
        }
        self.last_auth = self.last_config = self.last_heartbeat = 0
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
            for m in json.loads((self.root / "catalog/presets.json").read_text())
        ]

    def snapshot(self):
        with self.lock:
            value = copy.deepcopy(self.data)
        value["settings"] = self.settings.copy()
        value["setup"] = {
            "colab": bool(self.vault.get("colab")),
            "drive": bool(self.vault.get("drive")),
            "tailscale": bool(self.vault.get("tailscale")),
            "hf": bool(self.vault.get("hf")),
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
        config = self.vault.get("remote")
        if not config or not config.get("url"):
            raise RuntimeError("请先启动远端 GPU")
        parsed = urlparse(config["url"])
        if (
            parsed.scheme != "http"
            or parsed.port != 7860
            or ipaddress.ip_address(parsed.hostname)
            not in ipaddress.ip_network("100.64.0.0/10")
        ):
            raise RuntimeError("远端地址不是受支持的 Tailscale 地址")
        response = self.http.request(
            method,
            config["url"] + "/api/v1/" + path,
            headers={"Authorization": "Bearer " + config["token"]},
            json=body,
            timeout=(5, 45),
        )
        if response.status_code >= 400:
            try:
                detail = response.json().get("detail", "远端请求失败")
            except ValueError:
                detail = "远端请求失败"
            raise RuntimeError(str(detail)[:500])
        return response.json()

    def configure_remote(self):
        remote = self.vault.get("remote") or {}
        token = self.auth.credentials("drive").token if self.vault.get("drive") else ""
        self.remote(
            "POST",
            "configure",
            {
                "drive_token": token,
                "idle_minutes": self.settings["idle_minutes"],
                "deadline": remote.get("deadline", 0),
            },
        )
        self.last_config = time.time()

    def refresh_remote(self):
        status = self.remote("GET", "status")
        self.update(
            runtime=status,
            models=self.remote("GET", "models"),
            conversations=self.remote("GET", "conversations"),
        )

    def start(self):
        network = tailscale_status()
        self.update(network=network)
        if not network["running"]:
            raise RuntimeError("请先安装并登录 Tailscale，再启动 GPU")
        if not self.vault.get("drive"):
            raise RuntimeError("请先授权 Google Drive，以便保存会话和模型配置")
        self.auth.credentials("drive")

        def notify(message):
            self.update(
                runtime={"state": "starting"},
                operation={"title": message, "running": True},
            )

        existing = self.vault.get("runtime_session")
        try:
            self.provider.start(self.settings["gpu"], self.settings["max_cu"], notify)
            self.configure_remote()
            self.remote("POST", "restore")
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

    def release(self, force=False):
        if not force:
            # If synchronization cannot be verified, the caller must deliberately force release.
            result = self.remote("POST", "sync")
            if not result["ok"] or result["pending"]:
                raise RuntimeError("会话尚未同步。请重试同步，或选择“仍然释放”")
        self.update(runtime={"state": "releasing"})
        self.provider.release()
        with self.lock:
            for m in self.data["models"]:
                m["downloaded"] = m["loaded"] = False
        self.update(runtime={"state": "stopped"})

    def cloud_history(self):
        drive = DriveStore(self.auth.credentials("drive").token)
        models = {m["id"]: m for m in self.presets()}
        for m in drive.get("models.json", []):
            models[m["id"]] = m | {
                "downloaded": False,
                "loaded": False,
                "size": sum(f["size"] for f in m["files"]),
            }
        self.update(models=list(models.values()), conversations=drive.conversations())

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
            k: payload[k] for k in ("gpu", "idle_minutes", "max_cu") if k in payload
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
        if payload.get("ts_client_id") and payload.get("ts_client_secret"):
            tag = payload.get("ts_tag") or "tag:colabone"
            if not tag.startswith("tag:"):
                raise ValueError("Tailscale 标签需以 tag: 开头")
            self.vault.set(
                "tailscale",
                {
                    "client_id": payload["ts_client_id"],
                    "client_secret": payload["ts_client_secret"],
                    "tag": tag,
                },
            )
        if payload.get("hf_token"):
            self.vault.set("hf", payload["hf_token"])
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
        if action == "auth.colab.finish":

            def finish():
                self.auth.colab_finish(payload["code"])
                self.last_auth = 0

            return self.async_operation("验证 Colab 登录", finish)
        if action == "auth.drive":

            def drive():
                self.auth.drive_login(
                    payload["config"], lambda url: self.update(auth_url=url)
                )
                self.cloud_history()
                self.last_config = 0

            return self.async_operation("等待 Google Drive 授权", drive)
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
                    self.cloud_history()

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
            remote = self.vault.get("remote")
            cursor = 0
            for attempt in range(4):
                try:
                    with self.http.get(
                        remote["url"] + "/api/v1/tasks/" + result["id"] + "/events",
                        params={"after": cursor},
                        headers={"Authorization": "Bearer " + remote["token"]},
                        stream=True,
                        timeout=(5, 30),
                    ) as response:
                        response.raise_for_status()
                        event, seq = "", cursor
                        for line in response.iter_lines(
                            chunk_size=1, decode_unicode=True
                        ):
                            if self.stop.is_set():
                                return
                            if line.startswith("id:"):
                                seq = int(line[3:].strip())
                            elif line.startswith("event:"):
                                event = line[6:].strip()
                            elif line.startswith("data:"):
                                value = json.loads(line[5:].strip())
                                if seq <= cursor:
                                    continue
                                cursor = seq
                                if event == "delta":
                                    stream["content"] += value.get("content", "")
                                elif event == "done":
                                    stream["state"] = value["state"]
                                self.update(stream=stream.copy())
                    self.refresh_remote()
                    return
                except requests.RequestException:
                    if attempt < 3:
                        self.stop.wait(2)
            # A reconnect never resubmits the user turn. Refresh retrieves the saved answer.
            stream["state"] = "disconnected"
            self.update(stream=stream.copy())

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
                    self.update(
                        account=self.provider.account(), network=tailscale_status()
                    )
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
                    if not remote.get("url"):
                        self.update(
                            runtime={"state": "disconnected", "id": remote["endpoint"]}
                        )
                        self.stop.wait(2)
                        continue
                    if now - self.last_config > 600:
                        self.configure_remote()
                    try:
                        self.refresh_remote()
                    except requests.RequestException:
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
            except Exception as exc:
                self.update(
                    error="连接检查失败："
                    + type(exc).__name__
                    + "。请检查网络或重新登录。"
                )
            self.stop.wait(2)
