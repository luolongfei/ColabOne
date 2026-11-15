"""Remote-only Cloudflare connector; never exposes the management port (7860)."""

import hashlib
import ipaddress
import json
import os
import platform
import re
import socket
import subprocess
import threading
import time
from urllib.parse import urlsplit
import requests

QUICK_URL = re.compile(r"https://[a-z0-9]+(?:-[a-z0-9]+)*\.trycloudflare\.com\b")


def ensure_binary(root, catalog, cancel, note):
    manifest = json.loads((catalog / "cloudflared.json").read_text(encoding="utf-8"))
    machine = platform.machine().lower()
    arch = {
        "x86_64": "amd64",
        "amd64": "amd64",
        "aarch64": "arm64",
        "arm64": "arm64",
    }.get(machine)
    if platform.system() != "Linux" or not arch:
        raise RuntimeError("Cloudflared 只能安装在远端 Linux GPU 设备")
    folder = root / "bin"
    folder.mkdir(exist_ok=True)
    target = folder / ("cloudflared-" + manifest["tag"])
    expected = manifest["sha256"][arch]
    if target.exists():
        with target.open("rb") as cached:
            if hashlib.file_digest(cached, "sha256").hexdigest() == expected:
                return target
    note("正在远端下载 Cloudflared " + manifest["tag"])
    partial = target.with_suffix(".part")
    url = f"https://github.com/cloudflare/cloudflared/releases/download/{manifest['tag']}/cloudflared-linux-{arch}"
    try:
        with requests.Session() as http:
            http.trust_env = False
            with http.get(url, stream=True, timeout=(10, 15)) as response:
                response.raise_for_status()
                digest, size, started = hashlib.sha256(), 0, time.monotonic()
                with partial.open("wb") as output:
                    for chunk in response.iter_content(1024 * 1024):
                        if cancel.is_set() or time.monotonic() - started > 180:
                            raise RuntimeError("隧道安装已取消或超时")
                        size += len(chunk)
                        if size > 100 * 1024**2:
                            raise RuntimeError("Cloudflared 文件大小异常")
                        digest.update(chunk)
                        output.write(chunk)
                if digest.hexdigest() != expected:
                    raise RuntimeError("Cloudflared 完整性校验失败")
        partial.chmod(0o700)
        partial.replace(target)
        note("Cloudflared 下载完成，SHA-256 校验通过")
        return target
    finally:
        partial.unlink(missing_ok=True)


def public_addresses(origin):
    host = urlsplit(origin).hostname
    addresses = socket.getaddrinfo(host, 443, type=socket.SOCK_STREAM)
    if not addresses or any(
        not ipaddress.ip_address(a[4][0]).is_global for a in addresses
    ):
        raise ValueError("API 域名必须解析到公网地址")


class Tunnel:
    def __init__(self, gateway):
        self.gateway = gateway
        self.cancel = threading.Event()
        self.thread = None
        self.process = None
        self.token_path = None
        self.lock = threading.RLock()

    def start(self, config):
        self.cancel.clear()
        self.thread = threading.Thread(target=self.run, args=(config,), daemon=True)
        self.thread.start()

    def probe(self, origin):
        public_addresses(origin)
        # Never follow redirects or send the API key before verifying the runtime identity.
        with requests.Session() as http:
            http.trust_env = False

            def read(path, headers=None):
                with http.get(
                    origin + path,
                    headers=headers,
                    stream=True,
                    timeout=(5, 8),
                    allow_redirects=False,
                ) as response:
                    data = bytearray()
                    for chunk in response.iter_content(2048):
                        data.extend(chunk)
                        if len(data) > 4096:
                            raise ValueError("公网探测响应过大")
                    return response.status_code, data

            status, body = read("/health")
            if (
                status != 200
                or json.loads(body).get("instance") != self.gateway.instance
            ):
                return False
            status, _ = read("/v1/models")
            if status != 401:
                return False
            status, body = read(
                "/v1/models", {"Authorization": "Bearer " + self.gateway.key}
            )
            return status == 200 and json.loads(body).get("object") == "list"

    def run(self, config):
        g = self.gateway
        origin = config.domain
        url_ready = threading.Event()
        if origin:
            url_ready.set()
        try:
            binary = ensure_binary(
                g.runtime.root, g.runtime.catalog, self.cancel, g.note
            )
            if self.cancel.is_set():
                return
            args = [
                str(binary),
                "tunnel",
                "--no-autoupdate",
                "--protocol",
                "http2",
                "--grace-period",
                "2s",
            ]
            if config.mode == "quick":
                # Explicit empty config avoids accidentally adopting a preexisting tunnel.
                config_path = g.runtime.root / "quick-tunnel.yml"
                config_path.write_text("{}\n", encoding="utf-8")
                args += ["--config", str(config_path), "--url", "http://127.0.0.1:7861"]
            else:
                self.token_path = g.runtime.root / ".cloudflared-token"
                fd = os.open(
                    self.token_path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600
                )
                with os.fdopen(fd, "w", encoding="utf-8") as out:
                    out.write(config.tunnel_token)
                args += ["run", "--token-file", str(self.token_path)]
            with self.lock:
                if self.cancel.is_set():
                    return
                self.process = subprocess.Popen(
                    args,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.STDOUT,
                    text=True,
                    encoding="utf-8",
                    errors="replace",
                    env={
                        k: v
                        for k, v in os.environ.items()
                        if not k.startswith("TUNNEL_")
                    },
                )
                process = self.process
            g.note("Cloudflare 连接器已启动，正在验证公网地址与密钥鉴权")

            def read():
                nonlocal origin
                for line in process.stdout:
                    # Never store raw connector output: it may contain tokens/configuration.
                    match = QUICK_URL.search(line)
                    if match and config.mode == "quick":
                        origin = match.group(0)
                        url_ready.set()
                process.stdout.close()

            threading.Thread(target=read, daemon=True).start()
            deadline, was_ready = time.monotonic() + 120, False
            while not self.cancel.is_set():
                if process.poll() is not None:
                    raise RuntimeError(
                        "Cloudflare 连接器已退出，请检查网络或 Tunnel Token 后重试"
                    )
                valid = False
                if url_ready.is_set():
                    try:
                        valid = self.probe(origin)
                    except (requests.RequestException, ValueError, OSError):
                        pass
                if self.cancel.is_set():
                    break
                if valid:
                    if not was_ready:
                        g.note("公网 API 已通过连通性和鉴权检查")
                    g.phase, g.url, g.error = "ready", origin, ""
                    g.checked_at = time.time()
                    was_ready = True
                elif was_ready:
                    if g.phase != "reconnecting":
                        g.note("公网连接暂时不可用，正在重新检查")
                    g.phase = "reconnecting"
                elif time.monotonic() > deadline:
                    raise RuntimeError(
                        "公网地址验证超时；固定域名请映射到 http://127.0.0.1:7861，并检查 Cloudflare 路由与访问策略"
                    )
                self.cancel.wait(20 if was_ready else 2)
        except Exception as exc:
            if not self.cancel.is_set():
                # Known messages only; HTTP exceptions can contain URLs/credentials.
                g.error = (
                    str(exc)
                    if isinstance(exc, RuntimeError)
                    else "Cloudflare 连接失败，请检查网络与隧道配置"
                )
                g.phase, g.enabled, g.url = "failed", False, ""
                g.cancel_jobs()
                g.note(g.error, "error")
        finally:
            self.terminate()

    def terminate(self):
        with self.lock:
            if self.process:
                if self.process.poll() is None:
                    self.process.terminate()
                    try:
                        self.process.wait(timeout=4)
                    except subprocess.TimeoutExpired:
                        self.process.kill()
                        self.process.wait(timeout=3)
                self.process = None
            if self.token_path:
                self.token_path.unlink(missing_ok=True)

    def stop(self):
        self.cancel.set()
        self.terminate()
        if self.thread and self.thread is not threading.current_thread():
            self.thread.join(timeout=35)
            if self.thread.is_alive():
                raise RuntimeError("隧道仍在停止，请稍后重试")
