"""Isolated proxy transport exclusively for Colab GPU allocation (/tun/m/assign).

Supports HTTP, HTTPS, SOCKS5, and SOCKS5h proxies using Python stdlib + urllib3
with zero extra third-party dependencies. Always enforces TLS verification and
performs remote DNS resolution for SOCKS5 domain targets to avoid local DNS
leaks. Never mutates process-wide environment variables or unrelated sessions.
"""

from __future__ import annotations

import ipaddress
import re
import socket
import struct
import time
from dataclasses import dataclass
from urllib.parse import quote, unquote, urlsplit, urlunsplit

import requests
import urllib3
from google.auth.transport.requests import AuthorizedSession
from requests.adapters import HTTPAdapter
from urllib3.connection import HTTPConnection, HTTPSConnection
from urllib3.connectionpool import HTTPConnectionPool, HTTPSConnectionPool
from urllib3.exceptions import ProxyError as Urllib3ProxyError

_ALLOWED_SCHEMES = frozenset({"http", "https", "socks5", "socks5h"})
_DEFAULT_PORTS = {
    "http": 80,
    "https": 443,
    "socks5": 1080,
    "socks5h": 1080,
}

_SOCKS5_REPLY_REASONS = {
    0x01: "SOCKS5 代理通用错误",
    0x02: "SOCKS5 代理规则拒绝连接",
    0x03: "SOCKS5 代理网络不可达",
    0x04: "SOCKS5 代理目标主机不可达",
    0x05: "SOCKS5 代理目标连接被拒绝",
    0x06: "SOCKS5 代理连接超时 (TTL expired)",
    0x07: "SOCKS5 代理不支持 CONNECT 命令",
    0x08: "SOCKS5 代理不支持目标地址类型",
}

_DOMAIN_LABEL_RE = re.compile(r"^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$", re.IGNORECASE)

COLABONE_DEFAULT_PROXY_PROBE_URL = "https://colab.research.google.com/robots.txt"
COLABONE_ASSIGN_PROXY_TIMEOUT: tuple[float, float] = (15.0, 60.0)


@dataclass(frozen=True)
class ColabOneAssignProxySpec:
    scheme: str
    host: str
    port: int
    username: str = ""
    password: str = ""
    normalized_url: str = ""
    masked_url: str = ""
    log_label: str = ""

    @property
    def is_socks5(self) -> bool:
        return self.scheme in ("socks5", "socks5h")

    @property
    def has_auth(self) -> bool:
        return bool(self.username or self.password)


def _format_host_port(host: str, port: int) -> str:
    if ":" in host and not host.startswith("["):
        return f"[{host}]:{port}"
    return f"{host}:{port}"


def _validate_proxy_host(host: str) -> str:
    clean = (host or "").strip()
    if not clean or "%" in clean:
        raise ValueError("代理地址缺少主机名或 IP")
    try:
        ip = ipaddress.ip_address(clean)
        return str(ip)
    except ValueError:
        pass
    trimmed = clean.rstrip(".")
    if not trimmed:
        raise ValueError("代理地址缺少主机名或 IP")
    try:
        ascii_host = trimmed.encode("idna").decode("ascii")
    except UnicodeError:
        raise ValueError("代理地址缺少主机名或 IP") from None
    if not ascii_host or len(ascii_host) > 253:
        raise ValueError("代理地址缺少主机名或 IP")
    labels = ascii_host.split(".")
    if any(not _DOMAIN_LABEL_RE.match(label) for label in labels):
        raise ValueError("代理地址缺少主机名或 IP")
    return ascii_host.lower()


def _is_numeric_timeout(value) -> bool:
    return isinstance(value, (int, float)) and not isinstance(value, bool)


def colabone_parse_assign_proxy(raw: str) -> ColabOneAssignProxySpec | None:
    """Validate and parse an allocation-only proxy URL.

    Returns ``None`` when ``raw`` is empty/whitespace. Raises ``ValueError``
    with a user-facing Chinese error message when invalid.
    """
    text = str(raw or "").strip()
    if not text:
        return None
    if any(ch.isspace() or ord(ch) < 32 or ord(ch) == 127 for ch in text):
        raise ValueError("代理地址不能包含空格或控制字符")
    if "://" not in text:
        raise ValueError(
            "代理地址请包含协议前缀，例如 http://127.0.0.1:7890 或 socks5://127.0.0.1:1080"
        )

    parts = urlsplit(text)
    scheme = (parts.scheme or "").lower()
    if scheme not in _ALLOWED_SCHEMES:
        raise ValueError("仅支持 http://、https://、socks5:// 或 socks5h:// 代理协议")

    host = _validate_proxy_host(parts.hostname or "")

    try:
        port = parts.port
    except ValueError:
        raise ValueError("代理端口必须是 1 至 65535 之间的整数") from None
    if port is None:
        port = _DEFAULT_PORTS[scheme]
    if not 1 <= int(port) <= 65535:
        raise ValueError("代理端口必须是 1 至 65535 之间的整数")

    if parts.path not in ("", "/") or parts.query or parts.fragment:
        raise ValueError("代理地址不应包含路径或查询参数")

    raw_user = parts.username
    raw_pass = parts.password
    username = unquote(raw_user) if raw_user is not None else ""
    password = unquote(raw_pass) if raw_pass is not None else ""
    if scheme in ("socks5", "socks5h"):
        if len(username.encode("utf-8")) > 255 or len(password.encode("utf-8")) > 255:
            raise ValueError("SOCKS5 代理用户名或密码长度不能超过 255 字节")

    host_port = _format_host_port(host, int(port))
    # Re-encode userinfo strictly per RFC 3986 so raw '@' or ':' in passwords
    # never break urllib3 proxy URL parsing, and drop empty '@' prefixes.
    if username or password:
        enc_user = quote(username, safe="")
        if raw_pass is not None:
            enc_pass = quote(password, safe="")
            normalized_netloc = f"{enc_user}:{enc_pass}@{host_port}"
        else:
            normalized_netloc = f"{enc_user}@{host_port}"
    else:
        normalized_netloc = host_port

    normalized_url = urlunsplit((scheme, normalized_netloc, "", "", ""))
    masked_url = f"{scheme}://{host_port}"
    scheme_tag = "SOCKS5" if scheme in ("socks5", "socks5h") else scheme.upper()
    log_label = f"{scheme_tag} {host_port}"
    return ColabOneAssignProxySpec(
        scheme=scheme,
        host=host,
        port=int(port),
        username=username,
        password=password,
        normalized_url=normalized_url,
        masked_url=masked_url,
        log_label=log_label,
    )


def colabone_normalize_assign_proxy(raw: str) -> str:
    spec = colabone_parse_assign_proxy(raw)
    return spec.normalized_url if spec else ""


def colabone_mask_assign_proxy(raw: str) -> str:
    try:
        spec = colabone_parse_assign_proxy(raw)
    except ValueError:
        return ""
    return spec.masked_url if spec else ""


def colabone_proxy_log_label(raw: str) -> str:
    try:
        spec = colabone_parse_assign_proxy(raw)
    except ValueError:
        return ""
    return spec.log_label if spec else ""


def _recv_exact(sock: socket.socket, count: int) -> bytes:
    buf = bytearray()
    while len(buf) < count:
        chunk = sock.recv(count - len(buf))
        if not chunk:
            raise EOFError("代理连接在握手完成前中断")
        buf.extend(chunk)
    return bytes(buf)


def _encode_socks5_target(host: str, port: int) -> bytes:
    clean_host = (host or "").strip()
    if clean_host.startswith("[") and clean_host.endswith("]"):
        clean_host = clean_host[1:-1]
    try:
        ip = ipaddress.ip_address(clean_host)
    except ValueError:
        host_bytes = clean_host.rstrip(".").encode("idna")
        if not host_bytes or len(host_bytes) > 255:
            raise ValueError(f"无效的代理目标主机名：{clean_host}")
        addr_part = b"\x03" + bytes([len(host_bytes)]) + host_bytes
    else:
        if ip.version == 4:
            addr_part = b"\x01" + ip.packed
        else:
            addr_part = b"\x04" + ip.packed
    return addr_part + struct.pack("!H", int(port))


def _socks5_connect(
    proxy: ColabOneAssignProxySpec,
    target_host: str,
    target_port: int,
    timeout=None,
    source_address=None,
    socket_options=None,
) -> socket.socket:
    connect_timeout = (
        min(float(timeout), 15.0)
        if (_is_numeric_timeout(timeout) and float(timeout) > 0)
        else 15.0
    )
    sock = None
    try:
        sock = socket.create_connection(
            (proxy.host, proxy.port),
            timeout=connect_timeout,
            source_address=source_address,
        )
        if socket_options:
            for opt in socket_options:
                sock.setsockopt(*opt)
        sock.settimeout(connect_timeout)

        # 1) RFC 1928 method negotiation
        if proxy.has_auth:
            sock.sendall(b"\x05\x02\x00\x02")
        else:
            sock.sendall(b"\x05\x01\x00")
        ver, method = _recv_exact(sock, 2)
        if ver != 0x05:
            raise OSError(f"非法的 SOCKS5 协议版本响应：{ver}")
        if method == 0xFF:
            raise PermissionError("SOCKS5 代理拒绝认证方式或需要用户名与密码")
        if method == 0x02:
            if not proxy.has_auth:
                raise PermissionError("SOCKS5 代理要求用户名与密码认证")
            u_bytes = proxy.username.encode("utf-8")
            p_bytes = proxy.password.encode("utf-8")
            sock.sendall(
                b"\x01"
                + bytes([len(u_bytes)])
                + u_bytes
                + bytes([len(p_bytes)])
                + p_bytes
            )
            auth_ver, auth_status = _recv_exact(sock, 2)
            if auth_ver != 0x01 or auth_status != 0x00:
                raise PermissionError("SOCKS5 代理用户名或密码认证失败")
        elif method != 0x00:
            raise OSError(f"不支持的 SOCKS5 认证方式：{method}")

        # 2) RFC 1928 CONNECT request (always uses ATYP=DOMAIN for hostnames -> remote DNS)
        target_payload = _encode_socks5_target(target_host, target_port)
        sock.sendall(b"\x05\x01\x00" + target_payload)
        rep_ver, rep_code, _rsv, atyp = _recv_exact(sock, 4)
        if rep_ver != 0x05:
            raise OSError(f"非法的 SOCKS5 连接响应版本：{rep_ver}")
        # Evaluate reply status before draining BND.ADDR/BND.PORT so proxies
        # that close immediately after a 4-byte failure header report the exact reason.
        if rep_code != 0x00:
            reason = _SOCKS5_REPLY_REASONS.get(
                rep_code, f"SOCKS5 代理拒绝连接 (代码 {rep_code})"
            )
            raise OSError(reason)
        if atyp == 0x01:
            _recv_exact(sock, 4 + 2)
        elif atyp == 0x04:
            _recv_exact(sock, 16 + 2)
        elif atyp == 0x03:
            domain_len = _recv_exact(sock, 1)[0]
            _recv_exact(sock, domain_len + 2)
        else:
            raise OSError(f"非法的 SOCKS5 绑定地址类型：{atyp}")

        if timeout is None or _is_numeric_timeout(timeout):
            sock.settimeout(timeout)
        else:
            sock.settimeout(None)
        return sock
    except BaseException as exc:
        if sock is not None:
            try:
                sock.close()
            except OSError:
                pass
        if not isinstance(exc, Exception):
            raise
        if isinstance(exc, Urllib3ProxyError):
            raise
        if isinstance(exc, PermissionError):
            raise Urllib3ProxyError(str(exc), exc) from exc
        if isinstance(exc, (socket.timeout, TimeoutError)):
            raise Urllib3ProxyError("连接 SOCKS5 代理超时", exc) from exc
        raise Urllib3ProxyError(f"SOCKS5 代理连接失败：{exc}", exc) from exc


def _make_socks5_http_pool_cls(proxy: ColabOneAssignProxySpec):
    class _ColabOneSocks5HTTPConnection(HTTPConnection):
        def _new_conn(self) -> socket.socket:
            return _socks5_connect(
                proxy,
                self.host,
                self.port,
                timeout=self.timeout,
                source_address=self.source_address,
                socket_options=self.socket_options,
            )

    class _ColabOneSocks5HTTPPool(HTTPConnectionPool):
        ConnectionCls = _ColabOneSocks5HTTPConnection

    return _ColabOneSocks5HTTPPool


def _make_socks5_https_pool_cls(proxy: ColabOneAssignProxySpec):
    class _ColabOneSocks5HTTPSConnection(HTTPSConnection):
        def _new_conn(self) -> socket.socket:
            return _socks5_connect(
                proxy,
                self.host,
                self.port,
                timeout=self.timeout,
                source_address=self.source_address,
                socket_options=self.socket_options,
            )

    class _ColabOneSocks5HTTPSPool(HTTPSConnectionPool):
        ConnectionCls = _ColabOneSocks5HTTPSConnection

    return _ColabOneSocks5HTTPSPool


class ColabOneSocks5PoolManager(urllib3.PoolManager):
    def __init__(
        self,
        proxy_spec: ColabOneAssignProxySpec,
        num_pools: int = 10,
        headers=None,
        **connection_pool_kw,
    ):
        super().__init__(num_pools=num_pools, headers=headers, **connection_pool_kw)
        self._proxy_spec = proxy_spec
        self.pool_classes_by_scheme = {
            "http": _make_socks5_http_pool_cls(proxy_spec),
            "https": _make_socks5_https_pool_cls(proxy_spec),
        }


class ColabOneSocks5HTTPAdapter(HTTPAdapter):
    """Requests HTTPAdapter routing connections through a SOCKS5/SOCKS5h proxy."""

    def __init__(self, proxy_spec: ColabOneAssignProxySpec, *args, **kwargs):
        self._proxy_spec = proxy_spec
        super().__init__(*args, **kwargs)

    def init_poolmanager(
        self,
        connections,
        maxsize,
        block=False,
        **pool_kwargs,
    ):
        self._pool_connections = connections
        self._pool_maxsize = maxsize
        self._pool_block = block
        self.poolmanager = ColabOneSocks5PoolManager(
            self._proxy_spec,
            num_pools=connections,
            maxsize=maxsize,
            block=block,
            **pool_kwargs,
        )


def colabone_configure_assign_proxy_session(
    session: requests.Session, proxy_url: str
) -> ColabOneAssignProxySpec:
    """Attach an isolated proxy to a single requests/AuthorizedSession instance.

    Disables process proxy env vars (`trust_env = False`) and forces TLS
    certificate verification (`verify = True`). Never modifies other sessions
    or process environment variables.
    """
    spec = colabone_parse_assign_proxy(proxy_url)
    if spec is None:
        raise ValueError("请先填写分配代理地址")

    session.trust_env = False
    session.verify = True
    if spec.is_socks5:
        session.proxies = {}
        adapter = ColabOneSocks5HTTPAdapter(spec)
        session.mount("https://", adapter)
        session.mount("http://", adapter)
    else:
        session.proxies = {
            "http": spec.normalized_url,
            "https": spec.normalized_url,
        }
    return spec


class ColabOneAssignOnlySession(AuthorizedSession):
    """AuthorizedSession strictly scoped to Colab `/tun/m/assign` via an isolated proxy.

    Defense-in-depth guarantees:
    - Only `https://colab.research.google.com/tun/m/assign` (or configured origin)
      requests may be issued on this session; any other scheme, host, path, or
      redirect target is rejected before hitting the network.
    - OAuth credential refreshes use `AuthorizedSession`'s separate internal
      `_auth_request_session`, which never receives the allocation proxy.
    - TLS certificate verification (`verify=True`) and bounded connect/read
      timeouts `(15s, 60s)` are always enforced.
    """

    def __init__(
        self,
        credentials,
        proxy_url: str,
        allowed_origin: tuple[str, str] = ("https", "colab.research.google.com"),
        **kwargs,
    ):
        super().__init__(credentials, **kwargs)
        self._allowed_origin = (
            str(allowed_origin[0] or "https").lower(),
            str(allowed_origin[1] or "colab.research.google.com").lower().rstrip("."),
        )
        self.assign_proxy_spec = colabone_configure_assign_proxy_session(
            self, proxy_url
        )

    def _assert_assign_url(self, url: str) -> None:
        parsed = urlsplit(str(url or ""))
        scheme = (parsed.scheme or "").lower()
        host = (parsed.hostname or "").lower().rstrip(".")
        path = parsed.path.rstrip("/")
        allowed_scheme, allowed_host = self._allowed_origin
        if (
            scheme != allowed_scheme
            or host != allowed_host
            or path != "/tun/m/assign"
        ):
            raise RuntimeError("仅 GPU 分配请求允许使用分配代理")

    def request(self, method, url, *args, **kwargs):
        self._assert_assign_url(url)
        kwargs["verify"] = True
        if kwargs.get("timeout") is None:
            kwargs["timeout"] = COLABONE_ASSIGN_PROXY_TIMEOUT
        return super().request(method, url, *args, **kwargs)

    def send(self, request, **kwargs):
        self._assert_assign_url(getattr(request, "url", ""))
        kwargs["verify"] = True
        if kwargs.get("timeout") is None:
            kwargs["timeout"] = COLABONE_ASSIGN_PROXY_TIMEOUT
        return super().send(request, **kwargs)


def colabone_format_proxy_error(exc: Exception) -> str:
    """Format a user-friendly Chinese error message for allocation proxy failures."""
    msg = str(exc or "").lower()
    if isinstance(exc, requests.exceptions.SSLError):
        return (
            "通过分配代理建立加密连接失败（证书校验未通过），"
            "请勿使用会解密 HTTPS 流量的代理"
        )
    if (
        "407" in msg
        or "proxy authentication required" in msg
        or "认证" in str(exc or "")
        or isinstance(getattr(exc, "__cause__", None), PermissionError)
    ):
        return "GPU 分配代理认证失败，请检查代理用户名与密码后重试"
    if isinstance(exc, (requests.exceptions.Timeout, TimeoutError)) or "超时" in str(
        exc or ""
    ):
        return "连接 GPU 分配代理超时，请检查代理地址、端口或网络连通性"
    return "GPU 分配代理连接失败，请检查代理地址、端口、认证信息或服务状态后重试"


def colabone_test_assign_proxy(
    proxy_url: str,
    probe_url: str = COLABONE_DEFAULT_PROXY_PROBE_URL,
    timeout: float = 10.0,
) -> dict:
    """Test allocation proxy connectivity without sending any Google OAuth credentials."""
    spec = colabone_parse_assign_proxy(proxy_url)
    if spec is None:
        raise ValueError("请先填写分配代理地址")

    session = requests.Session()
    try:
        colabone_configure_assign_proxy_session(session, spec.normalized_url)
        started = time.monotonic()
        resp = session.get(
            probe_url,
            timeout=(min(float(timeout), 8.0), float(timeout)),
            allow_redirects=False,
            stream=True,
        )
        try:
            status_code = int(resp.status_code)
        finally:
            resp.close()
        if status_code == 407:
            raise RuntimeError("GPU 分配代理认证失败，请检查代理用户名与密码后重试")
        latency_ms = max(1, round((time.monotonic() - started) * 1000))
        return {
            "ok": True,
            "latency_ms": latency_ms,
            "status_code": status_code,
            "proxy": spec.log_label,
            "masked_url": spec.masked_url,
        }
    except (
        requests.exceptions.ProxyError,
        requests.exceptions.SSLError,
        requests.exceptions.Timeout,
        requests.RequestException,
        OSError,
    ) as exc:
        raise RuntimeError(colabone_format_proxy_error(exc)) from exc
    finally:
        try:
            session.close()
        except Exception:
            pass
