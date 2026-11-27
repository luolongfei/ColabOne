"""Remote-only Cloudflare connector; never exposes the management port (7860)."""

import hashlib
import ipaddress
import json
import os
import platform
import signal
import socket
import stat
import subprocess
import threading
import time
import uuid
from urllib.parse import urlparse, urlsplit, urlunparse
import requests
from requests.adapters import HTTPAdapter

from common.files import (
    COLABONE_SENSITIVE_SUBPROCESS_ENV_KEYS,
    colabone_sanitized_subprocess_env,
)

_CLOUDFLARED_INSTALL_LOCK = threading.Lock()
_SENSITIVE_TUNNEL_ENV_KEYS = COLABONE_SENSITIVE_SUBPROCESS_ENV_KEYS


def _sanitized_tunnel_env() -> dict[str, str]:
    return colabone_sanitized_subprocess_env()


class _ColabOneTunnelPinnedIPAdapter(HTTPAdapter):
    """Requests HTTPAdapter pinning TCP dials to a validated public IP while preserving SNI and Host."""

    def __init__(
        self,
        *,
        expected_host: str,
        pinned_ip: str,
        use_tls: bool = True,
        **kwargs,
    ) -> None:
        self.expected_host = expected_host.strip().lower().rstrip(".")
        self.pinned_ip = pinned_ip.strip()
        self.use_tls = bool(use_tls)
        super().__init__(**kwargs)

    def init_poolmanager(self, connections, maxsize, block=False, **pool_kwargs):
        if self.use_tls:
            pool_kwargs = dict(pool_kwargs)
            pool_kwargs["assert_hostname"] = self.expected_host
            pool_kwargs["server_hostname"] = self.expected_host
        return super().init_poolmanager(connections, maxsize, block=block, **pool_kwargs)

    def send(self, request, *args, **kwargs):
        parsed = urlparse(request.url)
        req_host = (parsed.hostname or "").strip().lower().rstrip(".")
        if req_host != self.expected_host:
            raise ValueError("探测请求主机与已校验公网解析结果不一致")
        scheme = (parsed.scheme or "").lower()
        default_port = 443 if scheme == "https" else 80
        port = parsed.port
        host_header = (
            f"{req_host}:{port}"
            if port is not None and port != default_port
            else req_host
        )
        ip_literal = f"[{self.pinned_ip}]" if ":" in self.pinned_ip else self.pinned_ip
        pinned_netloc = f"{ip_literal}:{port}" if port is not None else ip_literal
        request.url = urlunparse(parsed._replace(netloc=pinned_netloc))
        request.headers["Host"] = host_header
        return super().send(request, *args, **kwargs)


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
    if folder.is_symlink():
        raise ValueError("隧道组件目录不能是符号链接")
    folder.mkdir(exist_ok=True)
    target = folder / ("cloudflared-" + manifest["tag"])
    if target.is_symlink():
        raise ValueError("隧道组件文件不能是符号链接")
    expected = manifest["sha256"][arch]

    def _cached_cloudflared_valid() -> bool:
        if target.is_symlink():
            raise ValueError("隧道组件文件不能是符号链接")
        if not target.exists():
            return False
        fd = -1
        try:
            fd = os.open(
                str(target),
                os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0),
            )
            st = os.fstat(fd)
            if not stat.S_ISREG(st.st_mode) or st.st_size <= 0:
                return False
            with os.fdopen(fd, "rb") as cached:
                fd = -1
                if hashlib.file_digest(cached, "sha256").hexdigest() != expected:
                    return False
            if (st.st_mode & 0o100) == 0:
                target.chmod(0o700)
            return True
        except OSError:
            if fd >= 0:
                try:
                    os.close(fd)
                except OSError:
                    pass
            return False

    with _CLOUDFLARED_INSTALL_LOCK:
        if _cached_cloudflared_valid():
            note("复用远端已校验的 Cloudflared " + manifest["tag"])
            return target
        note("正在远端下载 Cloudflared " + manifest["tag"])
        partial = folder / f".cloudflared-{manifest['tag']}.{uuid.uuid4().hex[:8]}.part"
        if partial.is_symlink():
            raise ValueError("隧道临时文件不能是符号链接")
        url = f"https://github.com/cloudflare/cloudflared/releases/download/{manifest['tag']}/cloudflared-linux-{arch}"
        try:
            with requests.Session() as http:
                http.trust_env = False
                with http.get(url, stream=True, timeout=(10, 15)) as response:
                    response.raise_for_status()
                    digest, size, started = hashlib.sha256(), 0, time.monotonic()
                    p_fd = os.open(
                        str(partial),
                        os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0),
                        0o600,
                    )
                    try:
                        if hasattr(os, "fchmod"):
                            os.fchmod(p_fd, 0o600)
                        out_fh = os.fdopen(p_fd, "wb")
                    except BaseException:
                        os.close(p_fd)
                        raise
                    with out_fh as output:
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


_NAT64_PREFIXES = (
    ipaddress.IPv6Network("64:ff9b::/96"),
    ipaddress.IPv6Network("64:ff9b:1::/48"),
)


def _is_public_ip(raw_addr: str) -> bool:
    try:
        ip = ipaddress.ip_address(raw_addr)
    except ValueError:
        return False
    ip = getattr(ip, "ipv4_mapped", None) or ip
    if isinstance(ip, ipaddress.IPv6Address):
        if any(ip in prefix for prefix in _NAT64_PREFIXES):
            return False
        sixtofour = getattr(ip, "sixtofour", None)
        if sixtofour is not None and not _is_public_ip(str(sixtofour)):
            return False
        if getattr(ip, "teredo", None) is not None:
            return False
    if (
        not ip.is_global
        or ip.is_private
        or ip.is_loopback
        or ip.is_link_local
        or ip.is_multicast
        or ip.is_reserved
        or ip.is_unspecified
    ):
        return False
    return True


def public_addresses(origin):
    host = urlsplit(origin).hostname
    if not host:
        raise ValueError("域名必须解析到公网地址")
    ai_addrconfig = getattr(socket, "AI_ADDRCONFIG", 0)
    if ai_addrconfig:
        try:
            addresses = socket.getaddrinfo(
                host, 443, type=socket.SOCK_STREAM, flags=ai_addrconfig
            )
        except (socket.gaierror, TypeError):
            addresses = socket.getaddrinfo(host, 443, type=socket.SOCK_STREAM)
    else:
        addresses = socket.getaddrinfo(host, 443, type=socket.SOCK_STREAM)
    if not addresses:
        raise ValueError("域名必须解析到公网地址")
    ipv4_ips: list[str] = []
    ipv6_ips: list[str] = []
    seen: set[str] = set()
    for a in addresses:
        ip_str = str(a[4][0])
        if not _is_public_ip(ip_str):
            raise ValueError("域名必须解析到公网地址")
        if ip_str in seen:
            continue
        seen.add(ip_str)
        parsed_ip = ipaddress.ip_address(ip_str)
        if parsed_ip.version == 4:
            ipv4_ips.append(ip_str)
        else:
            ipv6_ips.append(ip_str)
    # Prefer IPv4 before IPv6 so IPv4-only Colab VMs don't stall on unreachable AAAA records.
    return ipv4_ips + ipv6_ips


class ColabOneTunnel:
    def __init__(self, gateway):
        self.gateway = gateway
        self.cancel = threading.Event()
        self.thread = None
        self._drain_thread = None
        self.process = None
        self.token_path = None
        self.lock = threading.RLock()

    def start(self, config):
        self.cancel.clear()
        self.thread = threading.Thread(target=self.run, args=(config,), daemon=True)
        self.thread.start()

    def probe(self, origin, *, timeout_seconds: float = 15.0):
        deadline = time.monotonic() + max(0.5, float(timeout_seconds))
        if self.cancel.is_set() or time.monotonic() >= deadline:
            return False
        pinned_ips = public_addresses(origin) or []
        if self.cancel.is_set() or time.monotonic() >= deadline:
            return False
        parsed_origin = urlsplit(origin)
        origin_host = (parsed_origin.hostname or "").strip().lower().rstrip(".")
        candidates: list[str | None] = (
            list(pinned_ips) if (pinned_ips and origin_host) else [None]
        )
        last_network_exc: Exception | None = None

        for idx, pinned_ip in enumerate(candidates):
            if self.cancel.is_set() or time.monotonic() >= deadline:
                return False
            # Never follow redirects or send the API key before verifying the runtime identity.
            try:
                with requests.Session() as http:
                    http.trust_env = False
                    if (
                        pinned_ip
                        and origin_host
                        and callable(getattr(http, "mount", None))
                    ):
                        http.mount(
                            "https://",
                            _ColabOneTunnelPinnedIPAdapter(
                                expected_host=origin_host,
                                pinned_ip=pinned_ip,
                                use_tls=True,
                            ),
                        )
                        http.mount(
                            "http://",
                            _ColabOneTunnelPinnedIPAdapter(
                                expected_host=origin_host,
                                pinned_ip=pinned_ip,
                                use_tls=False,
                            ),
                        )

                    def read(path, headers=None):
                        remaining = deadline - time.monotonic()
                        if self.cancel.is_set() or remaining <= 0:
                            return 0, b""
                        with http.get(
                            origin + path,
                            headers=headers,
                            stream=True,
                            timeout=(min(3.0, remaining), min(5.0, remaining)),
                            allow_redirects=False,
                        ) as response:
                            data = bytearray()
                            for chunk in response.iter_content(2048):
                                if self.cancel.is_set() or time.monotonic() >= deadline:
                                    return 0, b""
                                data.extend(chunk)
                                if len(data) > 4096:
                                    raise ValueError("公网探测响应过大")
                            return response.status_code, data

                    status, body = read("/health")
                    if status != 200 or self.cancel.is_set() or time.monotonic() >= deadline:
                        return False
                    parsed_health = json.loads(body)
                    if (
                        not isinstance(parsed_health, dict)
                        or parsed_health.get("instance") != self.gateway.instance
                    ):
                        return False
                    status, _ = read("/v1/models")
                    if status != 401 or self.cancel.is_set() or time.monotonic() >= deadline:
                        return False
                    with self.gateway.lock:
                        api_key = self.gateway.key
                    if not api_key or self.cancel.is_set() or time.monotonic() >= deadline:
                        return False
                    status, body = read(
                        "/v1/models", {"Authorization": "Bearer " + api_key}
                    )
                    if status != 200 or self.cancel.is_set() or time.monotonic() >= deadline:
                        return False
                    parsed_models = json.loads(body)
                    return (
                        isinstance(parsed_models, dict)
                        and parsed_models.get("object") == "list"
                    )
            except (requests.ConnectionError, requests.Timeout, OSError) as exc:
                last_network_exc = exc
                if (
                    idx + 1 < len(candidates)
                    and not self.cancel.is_set()
                    and time.monotonic() < deadline
                ):
                    continue
                raise
        if last_network_exc is not None:
            raise last_network_exc
        return False

    def run(self, config):
        g = self.gateway
        origin = config.domain
        try:
            g.note("正在检查远端 Cloudflared 隧道组件")
            binary = ensure_binary(
                g.runtime.root, g.runtime.catalog, self.cancel, g.note
            )
            if self.cancel.is_set():
                return
            g.note("正在启动 Cloudflared 隧道连接器")
            args = [
                str(binary),
                "tunnel",
                "--no-autoupdate",
                "--protocol",
                "http2",
                "--grace-period",
                "2s",
            ]
            with self.lock:
                if self.cancel.is_set():
                    return
                token_path = g.runtime.root / f".cloudflared-token.{uuid.uuid4().hex}"
                if token_path.is_symlink():
                    raise OSError("refusing symlink for cloudflared token file")
                fd = os.open(
                    token_path,
                    os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0),
                    0o600,
                )
                try:
                    if hasattr(os, "fchmod"):
                        os.fchmod(fd, 0o600)
                    out_file = os.fdopen(fd, "w", encoding="utf-8")
                except BaseException:
                    os.close(fd)
                    try:
                        token_path.unlink(missing_ok=True)
                    except OSError:
                        pass
                    raise
                try:
                    with out_file as out:
                        out.write(config.tunnel_token)
                except BaseException:
                    try:
                        token_path.unlink(missing_ok=True)
                    except OSError:
                        pass
                    raise
                self.token_path = token_path
                args += ["run", "--token-file", str(token_path)]
                self.process = subprocess.Popen(
                    args,
                    stdin=subprocess.DEVNULL,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.STDOUT,
                    text=True,
                    encoding="utf-8",
                    errors="replace",
                    env=_sanitized_tunnel_env(),
                    start_new_session=(os.name == "posix"),
                )
                process = self.process
            g.note("Cloudflare 连接器已启动，正在验证固定域名连通性与密钥鉴权")

            def drain():
                stream = process.stdout
                if stream is None:
                    return
                try:
                    for _ in stream:
                        # Never store raw connector output: it may contain tokens/configuration.
                        pass
                except (TypeError, ValueError, OSError):
                    pass
                finally:
                    try:
                        stream.close()
                    except Exception:
                        pass

            drain_t = threading.Thread(target=drain, daemon=True)
            with self.lock:
                self._drain_thread = drain_t
            drain_t.start()
            started = time.monotonic()
            last_probe_note = started
            deadline, was_ready = started + 120, False
            while not self.cancel.is_set():
                if process.poll() is not None:
                    raise RuntimeError(
                        "Cloudflare 连接器已退出，请检查网络或 Tunnel Token 后重试"
                    )
                valid = False
                try:
                    valid = self.probe(origin)
                except (requests.RequestException, ValueError, OSError):
                    pass
                if self.cancel.is_set():
                    break
                if valid:
                    with g.lock:
                        if self.cancel.is_set() or not g.enabled:
                            break
                        if not was_ready:
                            g.note("公网 API 已通过连通性和鉴权检查")
                        g.phase, g.url, g.error = "ready", origin, ""
                        g.checked_at = time.time()
                    was_ready = True
                elif was_ready:
                    with g.lock:
                        if g.phase != "reconnecting":
                            g.note("公网连接暂时不可用，正在重新检查")
                        g.phase = "reconnecting"
                elif time.monotonic() > deadline:
                    raise RuntimeError(
                        "公网地址验证超时；固定域名请映射到 http://127.0.0.1:7861，并检查 Cloudflare 路由与访问策略"
                    )
                elif time.monotonic() - last_probe_note >= 15:
                    last_probe_note = time.monotonic()
                    g.note(
                        f"正在等待公网域名路由生效并完成鉴权探测（已耗时 {int(last_probe_note - started)} 秒）"
                    )
                self.cancel.wait(20 if was_ready else 2)
        except Exception as exc:
            if not self.cancel.is_set():
                # Known messages only; HTTP exceptions can contain URLs/credentials.
                err_msg = (
                    str(exc)
                    if isinstance(exc, RuntimeError)
                    else "Cloudflare 连接失败，请检查网络与隧道配置"
                )
                with g.lock:
                    g.error = err_msg
                    g.phase, g.enabled, g.url = "failed", False, ""
                g.cancel_jobs()
                g.note(err_msg, "error")
        finally:
            self.terminate()

    def terminate(self):
        with self.lock:
            proc = self.process
            drain_t = self._drain_thread
            token_path = self.token_path
            self.process = None
            self._drain_thread = None
            self.token_path = None
        if proc is not None:
            pid = getattr(proc, "pid", None)
            own_pgrp = os.getpgrp() if hasattr(os, "getpgrp") else None
            can_killpg = (
                os.name == "posix"
                and isinstance(pid, int)
                and not isinstance(pid, bool)
                and pid > 0
                and hasattr(os, "killpg")
                and pid != own_pgrp
            )
            leader_alive = proc.poll() is None
            skip_pg = False
            if not leader_alive and can_killpg:
                try:
                    os.kill(pid, 0)
                    skip_pg = True
                except ProcessLookupError:
                    skip_pg = False
                except OSError:
                    skip_pg = True
            if can_killpg and not skip_pg:
                try:
                    os.killpg(pid, signal.SIGTERM)
                except OSError:
                    pass
            elif leader_alive:
                try:
                    proc.terminate()
                except OSError:
                    pass
            if leader_alive:
                try:
                    proc.wait(timeout=4)
                except subprocess.TimeoutExpired:
                    if can_killpg:
                        try:
                            os.killpg(pid, signal.SIGKILL)
                        except OSError:
                            pass
                    else:
                        try:
                            proc.kill()
                        except OSError:
                            pass
                    try:
                        proc.wait(timeout=3)
                    except subprocess.TimeoutExpired:
                        pass
            if can_killpg and not skip_pg:
                try:
                    os.killpg(pid, signal.SIGKILL)
                except OSError:
                    pass
            if drain_t is not None and drain_t is not threading.current_thread():
                try:
                    drain_t.join(timeout=1.5)
                except Exception:
                    pass
            if proc.stdout is not None:
                try:
                    proc.stdout.close()
                except Exception:
                    pass
        if token_path:
            try:
                if not token_path.is_symlink():
                    token_path.unlink(missing_ok=True)
            except OSError:
                pass

    def stop(self):
        self.cancel.set()
        self.terminate()
        try:
            if self.thread and self.thread is not threading.current_thread():
                self.thread.join(timeout=35)
                if self.thread.is_alive():
                    raise RuntimeError("隧道仍在停止，请稍后重试")
        finally:
            root = getattr(getattr(self.gateway, "runtime", None), "root", None)
            if root:
                try:
                    legacy = root / ".cloudflared-token"
                    if not legacy.is_symlink():
                        legacy.unlink(missing_ok=True)
                except OSError:
                    pass
