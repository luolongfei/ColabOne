"""Authenticated HTTP/1.1 + WebSocket reverse proxy and Cloudflare tunnel gateway for ComfyUI Native Canvas."""

from __future__ import annotations

import asyncio
import base64
import errno
import hmac
import json
import os
import re
import secrets
import subprocess
import threading
import time
from collections import deque
from pathlib import Path
from typing import Callable
from urllib.parse import parse_qs, urlsplit

import requests

from common.comfyui import ComfyCanvasConfig
from server import tunnel as _tunnel
from server.tunnel import ensure_binary, public_addresses

COLABONE_COMFY_COOKIE = "colabone_comfy"
COLABONE_INTERNAL_PROBE_HEADER = "X-ColabOne-Internal-Probe"
COLABONE_LIVE_PREFIX = "/_colabone_live/"
DEFAULT_LIVE_UPSTREAM_PORT = 8190
MAX_HEAD_BYTES = 64 * 1024
HEAD_READ_TIMEOUT = 15.0
BODY_IDLE_TIMEOUT = 600.0
WS_IDLE_TIMEOUT = 1800.0
MAX_CONNECTIONS = 128
MAX_LIVE_WS_CONNECTIONS = 16
CHUNK_SIZE = 64 * 1024
ACTIVITY_THROTTLE_SECONDS = 3.0

_TOKEN_RE = re.compile(r"^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$")
_HTTP_VERSION_RE = re.compile(r"^HTTP/1\.[01]$")

_UNAUTHORIZED_HTML_TEXT: dict[str, tuple[str, str]] = {
    "zh-CN": (
        "401 未经授权",
        "未经授权：请通过 ColabOne 客户端打开 ComfyUI 原生画布。",
    ),
    "en-US": (
        "401 Unauthorized",
        "Unauthorized: please open ComfyUI Native Canvas from the ColabOne app.",
    ),
    "zh-TW": (
        "401 未經授權",
        "未經授權：請透過 ColabOne 用戶端開啟 ComfyUI 原生畫布。",
    ),
    "zh-HK": (
        "401 未經授權",
        "未經授權：請透過 ColabOne 程式開啟 ComfyUI 原生畫布。",
    ),
    "ja-JP": (
        "401 認証エラー",
        "認証されていません。ColabOne アプリから ComfyUI ネイティブキャンバスを開いてください。",
    ),
    "ko-KR": (
        "401 인증 필요",
        "인증되지 않았습니다. ColabOne 앱에서 ComfyUI 네이티브 캔버스를 열어 주세요.",
    ),
}


def _resolve_accept_language(header: str) -> str:
    candidates: list[tuple[float, int, str]] = []
    for idx, item in enumerate((header or "").split(",")):
        parts = [p.strip() for p in item.split(";") if p.strip()]
        if not parts:
            continue
        tag = parts[0].lower().replace("_", "-")
        q = 1.0
        for param in parts[1:]:
            if param.lower().startswith("q="):
                try:
                    q = float(param[2:])
                except ValueError:
                    q = 0.0
        if q > 0:
            candidates.append((q, -idx, tag))
    candidates.sort(reverse=True)
    for _, _, tag in candidates:
        if tag.startswith("zh-hk") or tag.startswith("zh-mo") or tag.startswith("zh-hant-hk") or tag.startswith("yue"):
            return "zh-HK"
        if tag.startswith("zh-tw") or tag.startswith("zh-hant"):
            return "zh-TW"
        if tag.startswith("zh"):
            return "zh-CN"
        if tag.startswith("ja"):
            return "ja-JP"
        if tag.startswith("ko"):
            return "ko-KR"
        if tag.startswith("en"):
            return "en-US"
    return "en-US"

_ORIG_ENSURE_BINARY = ensure_binary
_ORIG_PUBLIC_ADDRESSES = public_addresses


def _validate_canvas_config(config: dict | ComfyCanvasConfig | None) -> ComfyCanvasConfig:
    if config is None:
        raw = {}
    elif hasattr(config, "model_dump"):
        raw = config.model_dump()
    else:
        raw = config
    return ComfyCanvasConfig.model_validate(raw)


def _call_ensure_binary(root, catalog, cancel, note):
    fn = (
        ensure_binary
        if ensure_binary is not _ORIG_ENSURE_BINARY
        else _tunnel.ensure_binary
    )
    return fn(root, catalog, cancel, note)


def _call_public_addresses(origin: str):
    fn = (
        public_addresses
        if public_addresses is not _ORIG_PUBLIC_ADDRESSES
        else _tunnel.public_addresses
    )
    return fn(origin)


async def _close_writer(writer: asyncio.StreamWriter | None) -> None:
    if writer is None:
        return
    try:
        if not writer.is_closing():
            writer.close()
        await asyncio.wait_for(writer.wait_closed(), timeout=2.0)
    except Exception:
        pass


def _parse_cookie_pairs(cookie_headers: list[str]) -> list[tuple[str, str]]:
    pairs: list[tuple[str, str]] = []
    for header_val in cookie_headers:
        for part in header_val.split(";"):
            item = part.strip()
            if not item:
                continue
            name, sep, val = item.partition("=")
            if not sep:
                continue
            val = val.strip()
            if len(val) >= 2 and val[0] == '"' and val[-1] == '"':
                val = val[1:-1]
            pairs.append((name.strip(), val))
    return pairs


def _strip_session_cookie(cookie_value: str) -> str:
    kept: list[str] = []
    for part in cookie_value.split(";"):
        item = part.strip()
        if not item:
            continue
        name, sep, _ = item.partition("=")
        if sep and name.strip() == COLABONE_COMFY_COOKIE:
            continue
        kept.append(item)
    return "; ".join(kept)


_BASE_HOP_BY_HOP_HEADERS: frozenset[str] = frozenset(
    {
        "connection",
        "keep-alive",
        "proxy-authenticate",
        "proxy-authorization",
        "proxy-connection",
        "te",
        "trailers",
    }
)
_PROTECTED_FRAMING_HEADERS: frozenset[str] = frozenset(
    {"host", "content-length", "transfer-encoding"}
)
_WS_PROTECTED_HEADERS: frozenset[str] = frozenset(
    {
        "upgrade",
        "sec-websocket-key",
        "sec-websocket-version",
        "sec-websocket-protocol",
        "sec-websocket-extensions",
        "sec-websocket-accept",
    }
)
_LOOPBACK_HOSTS: frozenset[str] = frozenset({"127.0.0.1", "localhost", "::1"})


def _parse_header_tokens(
    raw_headers: list[tuple[str, str]], header_name: str
) -> set[str]:
    target = header_name.lower()
    tokens: set[str] = set()
    for name, value in raw_headers:
        if name.lower() != target:
            continue
        for tok in value.split(","):
            cleaned = tok.strip().lower()
            if cleaned:
                tokens.add(cleaned)
    return tokens


def _is_valid_ws_key(raw_value: str) -> bool:
    key = raw_value.strip()
    if len(key) != 24 or not key.isascii():
        return False
    try:
        decoded = base64.b64decode(key.encode("ascii"), validate=True)
    except Exception:
        return False
    if len(decoded) != 16:
        return False
    return base64.b64encode(decoded).decode("ascii") == key


def _is_valid_ws_upgrade(
    method: str,
    version: str,
    raw_headers: list[tuple[str, str]],
) -> bool:
    """Strictly validate RFC 6455 WebSocket upgrade request headers."""
    if method != "GET" or version != "HTTP/1.1":
        return False
    if any(
        name.lower() in ("content-length", "transfer-encoding")
        for name, _ in raw_headers
    ):
        return False
    upgrade_tokens = _parse_header_tokens(raw_headers, "upgrade")
    if upgrade_tokens != {"websocket"}:
        return False
    connection_tokens = _parse_header_tokens(raw_headers, "connection")
    if "upgrade" not in connection_tokens:
        return False
    ws_versions = [
        value.strip()
        for name, value in raw_headers
        if name.lower() == "sec-websocket-version"
    ]
    if len(ws_versions) != 1 or ws_versions[0] != "13":
        return False
    ws_keys = [
        value.strip()
        for name, value in raw_headers
        if name.lower() == "sec-websocket-key"
    ]
    if len(ws_keys) != 1 or not _is_valid_ws_key(ws_keys[0]):
        return False
    return True


def _is_loopback_host(host: str) -> bool:
    return host.lower() in _LOOPBACK_HOSTS


def _is_trusted_app_origin(raw_origin: str) -> bool:
    """Return True if raw_origin is an exact trusted ColabOne desktop WebView origin."""
    trimmed = raw_origin.strip()
    if (
        not trimmed
        or trimmed.lower() == "null"
        or any(ord(ch) <= 32 or ord(ch) == 127 for ch in trimmed)
    ):
        return False
    try:
        parsed = urlsplit(trimmed)
        port = parsed.port
    except ValueError:
        return False
    if (
        port is not None
        or parsed.username is not None
        or parsed.password is not None
        or parsed.query
        or parsed.fragment
        or parsed.path not in ("", "/")
    ):
        return False
    scheme = parsed.scheme.lower()
    host = (parsed.hostname or "").lower()
    if scheme == "mygo":
        return host == "localhost"
    if scheme == "http":
        return host == "mygo.localhost"
    if scheme in ("colabone", "app"):
        return host in ("", "localhost")
    if scheme == "wails":
        return host in ("", "localhost", "wails.localhost")
    return False


def _parse_http_origin(
    raw_origin: str,
) -> tuple[str, str, int, int | None] | None:
    """Parse an http(s) Origin into (scheme, host, effective_port, explicit_port)."""
    trimmed = raw_origin.strip()
    if (
        not trimmed
        or trimmed.lower() == "null"
        or any(ord(ch) <= 32 or ord(ch) == 127 for ch in trimmed)
    ):
        return None
    try:
        parsed = urlsplit(trimmed)
        explicit_port = parsed.port
    except ValueError:
        return None
    scheme = parsed.scheme.lower()
    host = (parsed.hostname or "").lower()
    if (
        scheme not in ("http", "https")
        or not host
        or parsed.username is not None
        or parsed.password is not None
        or parsed.query
        or parsed.fragment
        or parsed.path not in ("", "/")
    ):
        return None
    eff_port = (
        explicit_port
        if explicit_port is not None
        else (443 if scheme == "https" else 80)
    )
    return scheme, host, eff_port, explicit_port


def _parse_host_header(raw_host: str) -> tuple[str, int | None] | None:
    trimmed = raw_host.strip()
    if not trimmed or any(ord(ch) <= 32 or ord(ch) == 127 for ch in trimmed):
        return None
    try:
        parsed = urlsplit(f"http://{trimmed}")
        port = parsed.port
    except ValueError:
        return None
    host = (parsed.hostname or "").lower()
    if (
        not host
        or parsed.username is not None
        or parsed.password is not None
        or parsed.query
        or parsed.fragment
        or parsed.path
    ):
        return None
    return host, port


async def _read_with_shared_idle(
    reader: asyncio.StreamReader,
    size: int,
    idle_timeout: float,
    last_io: list[float],
) -> bytes | None:
    """Read up to ``size`` bytes using a bidirectional shared ``last_io`` idle deadline.

    Returns ``None`` if neither direction observed non-empty I/O for ``idle_timeout``
    seconds, ``b''`` on clean EOF, or a non-empty chunk while refreshing ``last_io[0]``.
    """
    while True:
        remaining = idle_timeout - (time.monotonic() - last_io[0])
        if remaining <= 0:
            return None
        try:
            chunk = await asyncio.wait_for(reader.read(size), timeout=remaining)
        except TimeoutError:
            if (time.monotonic() - last_io[0]) >= idle_timeout:
                return None
            continue
        if chunk:
            last_io[0] = time.monotonic()
        return chunk


class ColabOneCanvasProxyServer:
    """Pure asyncio HTTP/1.1 + WebSocket authenticated reverse proxy for ComfyUI."""

    def __init__(
        self,
        upstream_host: str = "127.0.0.1",
        upstream_port: int = 8188,
        listen_host: str = "127.0.0.1",
        listen_port: int = 8189,
        instance: str = "",
        canvas_token: str = "",
        session_cookie: str = "",
        on_activity: Callable[[], None] | None = None,
        live_upstream_port: int = DEFAULT_LIVE_UPSTREAM_PORT,
        expected_origin: str = "",
    ):
        self.upstream_host = upstream_host
        self.upstream_port = int(upstream_port)
        self.live_upstream_port = int(live_upstream_port)
        self.listen_host = listen_host
        self.listen_port = int(listen_port)
        self.port = self.listen_port
        self.instance = instance or secrets.token_hex(16)
        self.canvas_token = canvas_token or secrets.token_urlsafe(32)
        self.session_cookie = session_cookie or secrets.token_urlsafe(32)
        self.on_activity = on_activity
        self.expected_origin = str(expected_origin or "").strip()

        self.active_connections = 0
        self._live_ws_connections = 0
        self._last_touch: float | None = None
        self._lifecycle_lock = threading.RLock()
        self._thread: threading.Thread | None = None
        self._loop: asyncio.AbstractEventLoop | None = None
        self._server: asyncio.base_events.Server | None = None
        self._stop_event: asyncio.Event | None = None
        self._start_cancel: threading.Event | None = None
        self._tasks: set[asyncio.Task] = set()

    def _touch(self) -> None:
        callback = self.on_activity
        if callback is None:
            return
        now = time.monotonic()
        if (
            self._last_touch is not None
            and (now - self._last_touch) < ACTIVITY_THROTTLE_SECONDS
        ):
            return
        self._last_touch = now
        try:
            callback()
        except Exception:
            pass

    def start(self) -> None:
        with self._lifecycle_lock:
            if self._thread and self._thread.is_alive():
                return
            ready = threading.Event()
            cancel_start = threading.Event()
            self._start_cancel = cancel_start
            start_error: list[BaseException] = []

            def runner() -> None:
                loop = asyncio.new_event_loop()
                asyncio.set_event_loop(loop)
                try:
                    loop.run_until_complete(
                        self._serve(loop, ready, cancel_start, start_error)
                    )
                except BaseException as exc:
                    if not ready.is_set():
                        start_error.append(exc)
                        ready.set()
                finally:
                    try:
                        loop.run_until_complete(loop.shutdown_asyncgens())
                    except Exception:
                        pass
                    self._loop = None
                    self._server = None
                    self._stop_event = None
                    loop.close()

            self._thread = threading.Thread(target=runner, daemon=True)
            self._thread.start()
            if not ready.wait(timeout=10.0):
                self.stop()
                raise RuntimeError("ComfyUI 画布代理启动超时")
            if start_error:
                self._thread.join(timeout=5.0)
                self._thread = None
                raise start_error[0]

    async def _serve(
        self,
        loop: asyncio.AbstractEventLoop,
        ready: threading.Event,
        cancel_start: threading.Event,
        start_error: list[BaseException],
    ) -> None:
        stop_event = asyncio.Event()
        self._stop_event = stop_event
        self._loop = loop
        if cancel_start.is_set():
            ready.set()
            return
        try:
            server = await asyncio.start_server(
                self._handle_client_wrapper,
                host=self.listen_host,
                port=self.listen_port,
                reuse_address=True,
                limit=128 * 1024,
            )
        except BaseException as exc:
            start_error.append(exc)
            ready.set()
            return

        self._server = server
        sockets = server.sockets or ()
        if sockets:
            self.port = int(sockets[0].getsockname()[1])
        if cancel_start.is_set():
            server.close()
            try:
                await asyncio.wait_for(server.wait_closed(), timeout=5.0)
            except TimeoutError:
                pass
            ready.set()
            return
        ready.set()

        await stop_event.wait()
        server.close()
        pending = list(self._tasks)
        for task in pending:
            task.cancel()
        if pending:
            try:
                await asyncio.wait_for(
                    asyncio.gather(*pending, return_exceptions=True),
                    timeout=5.0,
                )
            except TimeoutError:
                pass
        try:
            await asyncio.wait_for(server.wait_closed(), timeout=5.0)
        except TimeoutError:
            pass

    def stop(self) -> None:
        with self._lifecycle_lock:
            if self._start_cancel is not None:
                self._start_cancel.set()
            loop = self._loop
            stop_event = self._stop_event
            thread = self._thread
            if loop is not None and stop_event is not None and not loop.is_closed():
                try:
                    loop.call_soon_threadsafe(stop_event.set)
                except RuntimeError:
                    pass
            if thread is not None and thread is not threading.current_thread():
                thread.join(timeout=10.0)
            self._thread = None
            self._loop = None
            self._server = None
            self._stop_event = None
            self._start_cancel = None
            self.active_connections = 0
            self._live_ws_connections = 0

    async def _handle_client_wrapper(
        self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter
    ) -> None:
        if len(self._tasks) >= MAX_CONNECTIONS:
            try:
                await self._send_json(
                    writer,
                    503,
                    "Service Unavailable",
                    {"error": "Too many active canvas connections"},
                )
            except Exception:
                pass
            await _close_writer(writer)
            return
        task = asyncio.current_task()
        if task is not None:
            self._tasks.add(task)
        try:
            await self._handle_client(reader, writer)
        except asyncio.CancelledError:
            raise
        except Exception:
            pass
        finally:
            if task is not None:
                self._tasks.discard(task)
            await _close_writer(writer)

    async def _send_response(
        self,
        writer: asyncio.StreamWriter,
        status: int,
        reason: str,
        body: bytes = b"",
        headers: list[tuple[str, str]] | None = None,
    ) -> None:
        lines = [f"HTTP/1.1 {status} {reason}"]
        has_len = False
        has_conn = False
        for k, v in headers or []:
            lk = k.lower()
            if lk == "content-length":
                has_len = True
            elif lk == "connection":
                has_conn = True
            lines.append(f"{k}: {v}")
        if not has_len:
            lines.append(f"Content-Length: {len(body)}")
        if not has_conn:
            lines.append("Connection: close")
        payload = ("\r\n".join(lines) + "\r\n\r\n").encode("latin-1") + body
        writer.write(payload)
        await writer.drain()

    async def _send_json(
        self,
        writer: asyncio.StreamWriter,
        status: int,
        reason: str,
        data: dict,
        extra_headers: list[tuple[str, str]] | None = None,
    ) -> None:
        body = json.dumps(data, ensure_ascii=False).encode("utf-8")
        headers = [
            ("Content-Type", "application/json"),
            ("Cache-Control", "no-store"),
            ("Connection", "close"),
        ]
        if extra_headers:
            headers.extend(extra_headers)
        await self._send_response(writer, status, reason, body, headers)

    async def _read_request_head(
        self, reader: asyncio.StreamReader
    ) -> tuple[bytes, bytes, int]:
        """Return (head_bytes, leftover_bytes, error_status). error_status == 0 on success."""
        buf = bytearray()
        try:
            async with asyncio.timeout(HEAD_READ_TIMEOUT):
                while True:
                    sep = buf.find(b"\r\n\r\n")
                    if sep != -1:
                        if sep + 4 > MAX_HEAD_BYTES:
                            return b"", b"", 431
                        return bytes(buf[:sep]), bytes(buf[sep + 4 :]), 0
                    if len(buf) > MAX_HEAD_BYTES:
                        return b"", b"", 431
                    chunk = await reader.read(8192)
                    if not chunk:
                        if not buf:
                            return b"", b"", -1  # clean EOF before request
                        return b"", b"", 400
                    buf.extend(chunk)
        except TimeoutError:
            return b"", b"", 408 if buf else -1

    def _parse_head(
        self, head_bytes: bytes
    ) -> tuple[str, str, str, list[tuple[str, str]], dict[str, str]] | None:
        raw_lines = head_bytes.split(b"\r\n")
        if not raw_lines:
            return None
        for line_b in raw_lines:
            if b"\r" in line_b or b"\n" in line_b:
                return None
        try:
            req_line = raw_lines[0].decode("latin-1")
        except UnicodeDecodeError:
            return None
        parts = req_line.split(" ")
        if len(parts) != 3:
            return None
        method, target, version = parts
        if (
            not _TOKEN_RE.match(method)
            or not target
            or not target.startswith("/")
            or any(ord(ch) < 33 or ord(ch) == 127 for ch in target)
            or not _HTTP_VERSION_RE.match(version)
        ):
            return None

        raw_headers: list[tuple[str, str]] = []
        header_map: dict[str, str] = {}
        host_count = 0
        cl_values: set[str] = set()
        has_te = False

        for line_b in raw_lines[1:]:
            line = line_b.decode("latin-1")
            name, sep, value = line.partition(":")
            if not sep or not _TOKEN_RE.match(name):
                return None
            val = value.strip(" \t")
            lname = name.lower()
            if lname == "host":
                host_count += 1
                if host_count > 1:
                    return None
            elif lname == "content-length":
                if not (val.isascii() and val.isdigit()):
                    return None
                cl_values.add(val)
                if len(cl_values) > 1:
                    return None
            elif lname == "transfer-encoding":
                has_te = True
            raw_headers.append((name, val))
            if lname in header_map:
                header_map[lname] = f"{header_map[lname]}, {val}"
            else:
                header_map[lname] = val

        if has_te and cl_values:
            return None

        return method, target, version, raw_headers, header_map

    def _check_auth(
        self, raw_headers: list[tuple[str, str]]
    ) -> tuple[bool, bool]:
        """Return (is_authorized, matched_bearer_token)."""
        cookie_ok = False
        if self.session_cookie:
            cookie_headers = [v for k, v in raw_headers if k.lower() == "cookie"]
            expected_cookie = self.session_cookie.encode("utf-8")
            for name, val in _parse_cookie_pairs(cookie_headers):
                if name == COLABONE_COMFY_COOKIE and hmac.compare_digest(
                    val.encode("utf-8"), expected_cookie
                ):
                    cookie_ok = True
                    break

        bearer_ok = False
        if self.canvas_token:
            auth_headers = [v for k, v in raw_headers if k.lower() == "authorization"]
            if len(auth_headers) == 1:
                expected_auth = ("Bearer " + self.canvas_token).encode("utf-8")
                bearer_ok = hmac.compare_digest(
                    auth_headers[0].encode("utf-8"), expected_auth
                )

        return (cookie_ok or bearer_ok), bearer_ok

    def _is_origin_allowed(
        self,
        raw_headers: list[tuple[str, str]],
        header_map: dict[str, str],
        *,
        is_live_ws: bool = False,
    ) -> bool:
        """Validate Origin header on WebSocket upgrades and non-GET mutating requests."""
        origin_headers = [
            value.strip()
            for name, value in raw_headers
            if name.lower() == "origin"
        ]
        if not origin_headers:
            return True
        if len(origin_headers) > 1:
            return False
        raw_origin = origin_headers[0]
        if not raw_origin or raw_origin.lower() == "null":
            return False

        if is_live_ws and _is_trusted_app_origin(raw_origin):
            return True

        parsed_origin = _parse_http_origin(raw_origin)
        parsed_host = _parse_host_header(header_map.get("host", ""))
        host_is_loopback = parsed_host is not None and _is_loopback_host(
            parsed_host[0]
        )

        if self.expected_origin:
            parsed_expected = _parse_http_origin(self.expected_origin)
            if (
                parsed_origin is not None
                and parsed_expected is not None
                and parsed_origin[:3] == parsed_expected[:3]
            ):
                return True

        if host_is_loopback:
            if _is_trusted_app_origin(raw_origin):
                return True
            if parsed_origin is not None and _is_loopback_host(parsed_origin[1]):
                return True
            return False

        if self.expected_origin:
            # When an expected public origin is configured, reject any non-matching
            # non-loopback Origin (even if Host was forged to match it).
            return False

        if parsed_origin is None or parsed_host is None:
            return False

        orig_scheme, orig_host, orig_eff_port, orig_explicit_port = parsed_origin
        host_name, host_port = parsed_host
        if orig_host != host_name:
            return False
        xfp = header_map.get("x-forwarded-proto", "").split(",")[0].strip().lower()
        if xfp in ("http", "https") and orig_scheme != xfp:
            return False
        if host_port is not None:
            return orig_eff_port == host_port
        return orig_explicit_port is None or orig_eff_port == (
            443 if orig_scheme == "https" else 80
        )

    async def _handle_client(
        self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter
    ) -> None:
        head_bytes, leftover, err_status = await self._read_request_head(reader)
        if err_status == -1:
            return
        if err_status == 431:
            await self._send_json(
                writer,
                431,
                "Request Header Fields Too Large",
                {"error": "Request header too large"},
            )
            return
        if err_status == 408:
            await self._send_json(
                writer,
                408,
                "Request Timeout",
                {"error": "Request header timeout"},
            )
            return
        if err_status != 0:
            await self._send_json(
                writer, 400, "Bad Request", {"error": "Malformed HTTP request"}
            )
            return

        parsed = self._parse_head(head_bytes)
        if parsed is None:
            await self._send_json(
                writer, 400, "Bad Request", {"error": "Malformed HTTP request"}
            )
            return

        method, target, version, raw_headers, header_map = parsed
        raw_path, _, raw_query = target.partition("?")

        # Route 1: Unauthenticated liveness & gateway identity check
        if method == "GET" and raw_path == "/_colabone_health":
            await self._send_json(
                writer,
                200,
                "OK",
                {
                    "ok": True,
                    "instance": self.instance,
                    "service": "comfyui-canvas",
                },
            )
            return

        # Route 2: One-click token-to-cookie authentication handshake
        if method == "GET" and raw_path == "/_colabone_auth":
            tokens = parse_qs(raw_query, keep_blank_values=True).get("token", [])
            provided = tokens[0] if len(tokens) == 1 else ""
            if not self.canvas_token or not hmac.compare_digest(
                provided.encode("utf-8"), self.canvas_token.encode("utf-8")
            ):
                await self._send_json(
                    writer, 401, "Unauthorized", {"error": "Unauthorized"}
                )
                return

            is_internal_probe = hmac.compare_digest(
                header_map.get(COLABONE_INTERNAL_PROBE_HEADER.lower(), "").encode(
                    "utf-8"
                ),
                self.instance.encode("utf-8"),
            )
            if not is_internal_probe:
                self._touch()
            host = header_map.get("host", "").strip().lower()
            xfp = header_map.get("x-forwarded-proto", "").split(",")[0].strip().lower()
            is_https = (xfp == "https") or (
                bool(host) and not host.startswith(("127.0.0.1", "localhost", "[::1]"))
            )
            cookie_header = (
                f"{COLABONE_COMFY_COOKIE}={self.session_cookie}; Path=/; HttpOnly; SameSite=Lax"
                + ("; Secure" if is_https else "")
            )
            await self._send_response(
                writer,
                302,
                "Found",
                b"",
                [
                    ("Location", "/"),
                    ("Set-Cookie", cookie_header),
                    ("Cache-Control", "no-store"),
                    ("Referrer-Policy", "no-referrer"),
                    ("Connection", "close"),
                ],
            )
            return

        is_upgrade = _is_valid_ws_upgrade(method, version, raw_headers)
        is_live_route = raw_path == "/_colabone_live/ws" or raw_path.startswith(
            COLABONE_LIVE_PREFIX
        )
        is_live_ws_upgrade = is_upgrade and raw_path == "/_colabone_live/ws"

        # All other routes require valid session cookie OR Bearer canvas_token, except
        # /_colabone_live/ws WebSocket upgrades which enforce their own mandatory
        # constant-time first-frame ephemeral token handshake inside Live Studio.
        authorized, matched_bearer = self._check_auth(raw_headers)
        if not authorized and not is_live_ws_upgrade:
            accept = header_map.get("accept", "").lower()
            if "text/html" in accept:
                lang = _resolve_accept_language(header_map.get("accept-language", ""))
                title_text, body_text = _UNAUTHORIZED_HTML_TEXT[lang]
                html = (
                    f"<!doctype html><html lang='{lang}'><meta charset='utf-8'>"
                    f"<title>{title_text}</title>"
                    f"<p>{body_text}</p></html>"
                ).encode("utf-8")
                await self._send_response(
                    writer,
                    401,
                    "Unauthorized",
                    html,
                    [
                        ("Content-Type", "text/html; charset=utf-8"),
                        ("Cache-Control", "no-store"),
                        ("Connection", "close"),
                    ],
                )
            else:
                await self._send_json(
                    writer, 401, "Unauthorized", {"error": "Unauthorized"}
                )
            return

        # Enforce strict Origin validation on WebSocket upgrades and non-GET mutating requests.
        if (
            is_upgrade or method.upper() not in ("GET", "HEAD", "OPTIONS")
        ) and not self._is_origin_allowed(
            raw_headers, header_map, is_live_ws=is_live_ws_upgrade
        ):
            await self._send_json(
                writer, 403, "Forbidden", {"error": "Forbidden origin"}
            )
            return

        live_ws_counted = False
        if is_live_ws_upgrade:
            if self._live_ws_connections >= MAX_LIVE_WS_CONNECTIONS:
                await self._send_json(
                    writer,
                    503,
                    "Service Unavailable",
                    {"error": "Too many active Live Studio WebSocket connections"},
                )
                return
            self._live_ws_connections += 1
            live_ws_counted = True

        # Live Studio WS upgrades defer activity updates to live_server.py after
        # first-frame token verification, preventing unauthenticated idle resets.
        touch_activity = not is_live_ws_upgrade
        target_port = self.live_upstream_port if is_live_route else self.upstream_port

        try:
            if touch_activity:
                self._touch()

            try:
                upstream_reader, upstream_writer = await asyncio.wait_for(
                    asyncio.open_connection(self.upstream_host, target_port),
                    timeout=10.0,
                )
            except (OSError, TimeoutError):
                await self._send_json(
                    writer,
                    502,
                    "Bad Gateway",
                    {
                        "error": (
                            "Live Studio upstream is unreachable"
                            if is_live_route
                            else "ComfyUI upstream is unreachable"
                        )
                    },
                )
                return

            self.active_connections += 1
            try:
                upstream_head = self._build_upstream_head(
                    method,
                    target,
                    version,
                    raw_headers,
                    is_upgrade=is_upgrade,
                    strip_auth=matched_bearer,
                    upstream_port=target_port,
                )
                upstream_writer.write(upstream_head)
                if leftover and not is_upgrade:
                    upstream_writer.write(leftover)
                await upstream_writer.drain()

                last_io = [time.monotonic()]
                upgrade_event: asyncio.Event | None = (
                    asyncio.Event() if is_upgrade else None
                )
                upgrade_state: dict[str, bool] = {"confirmed": False}

                c2u_task = asyncio.create_task(
                    self._pipe_client_to_upstream(
                        reader,
                        upstream_writer,
                        is_upgrade=is_upgrade,
                        touch_activity=touch_activity,
                        last_io=last_io,
                        initial_client_bytes=leftover if is_upgrade else b"",
                        upgrade_event=upgrade_event,
                        upgrade_state=upgrade_state,
                    )
                )
                u2c_task = asyncio.create_task(
                    self._pipe_upstream_to_client(
                        upstream_reader,
                        writer,
                        method=method,
                        is_upgrade=is_upgrade,
                        touch_activity=touch_activity,
                        last_io=last_io,
                        upgrade_event=upgrade_event,
                        upgrade_state=upgrade_state,
                    )
                )
                try:
                    done, _pending = await asyncio.wait(
                        {c2u_task, u2c_task},
                        return_when=asyncio.FIRST_COMPLETED,
                    )
                    if u2c_task in done:
                        if not c2u_task.done():
                            c2u_task.cancel()
                        await asyncio.gather(c2u_task, u2c_task, return_exceptions=True)
                    else:
                        c2u_exc = (
                            c2u_task.exception() if not c2u_task.cancelled() else None
                        )
                        if c2u_exc is not None or writer.is_closing():
                            u2c_task.cancel()
                            await asyncio.gather(u2c_task, return_exceptions=True)
                        elif is_upgrade and upgrade_state["confirmed"]:
                            try:
                                await asyncio.wait_for(u2c_task, timeout=2.0)
                            except (TimeoutError, asyncio.CancelledError, OSError):
                                u2c_task.cancel()
                                await asyncio.gather(u2c_task, return_exceptions=True)
                        else:
                            # Non-upgrade (or rejected upgrade): rely on u2c_task's own
                            # per-read BODY_IDLE_TIMEOUT instead of an outer wall-clock cap.
                            try:
                                await u2c_task
                            except (asyncio.CancelledError, OSError):
                                u2c_task.cancel()
                                await asyncio.gather(u2c_task, return_exceptions=True)
                finally:
                    for task in (c2u_task, u2c_task):
                        if not task.done():
                            task.cancel()
                    await asyncio.gather(c2u_task, u2c_task, return_exceptions=True)
            finally:
                self.active_connections = max(0, self.active_connections - 1)
                await _close_writer(upstream_writer)
        finally:
            if live_ws_counted:
                self._live_ws_connections = max(0, self._live_ws_connections - 1)

    def _build_upstream_head(
        self,
        method: str,
        target: str,
        version: str,
        raw_headers: list[tuple[str, str]],
        *,
        is_upgrade: bool,
        strip_auth: bool,
        upstream_port: int | None = None,
    ) -> bytes:
        out_headers: list[tuple[str, str]] = []
        host_written = False
        resolved_port = (
            self.upstream_port if upstream_port is None else int(upstream_port)
        )
        upstream_host_val = f"{self.upstream_host}:{resolved_port}"
        conn_tokens = _parse_header_tokens(raw_headers, "connection")
        strip_hop_by_hop: set[str] = (
            set(_BASE_HOP_BY_HOP_HEADERS) | conn_tokens
        ) - _PROTECTED_FRAMING_HEADERS
        if is_upgrade:
            strip_hop_by_hop -= _WS_PROTECTED_HEADERS
        else:
            strip_hop_by_hop.add("upgrade")

        for name, value in raw_headers:
            lname = name.lower()
            if lname == "host":
                out_headers.append(("Host", upstream_host_val))
                host_written = True
            elif lname == "cookie":
                cleaned = _strip_session_cookie(value)
                if cleaned:
                    out_headers.append((name, cleaned))
            elif lname == "authorization" and strip_auth:
                continue
            elif lname in strip_hop_by_hop:
                continue
            else:
                out_headers.append((name, value))

        if not host_written:
            out_headers.insert(0, ("Host", upstream_host_val))
        if is_upgrade:
            out_headers.append(("Connection", "Upgrade"))
        else:
            out_headers.append(("Connection", "close"))

        lines = [f"{method} {target} {version}"]
        lines.extend(f"{k}: {v}" for k, v in out_headers)
        return ("\r\n".join(lines) + "\r\n\r\n").encode("latin-1")

    async def _pipe_client_to_upstream(
        self,
        client_reader: asyncio.StreamReader,
        upstream_writer: asyncio.StreamWriter,
        *,
        is_upgrade: bool = False,
        touch_activity: bool = True,
        last_io: list[float] | None = None,
        initial_client_bytes: bytes = b"",
        upgrade_event: asyncio.Event | None = None,
        upgrade_state: dict[str, bool] | None = None,
    ) -> None:
        if is_upgrade and upgrade_event is not None:
            await upgrade_event.wait()
            if not (upgrade_state and upgrade_state.get("confirmed")):
                return
            if initial_client_bytes:
                if last_io is not None:
                    last_io[0] = time.monotonic()
                if touch_activity:
                    self._touch()
                try:
                    upstream_writer.write(initial_client_bytes)
                    await upstream_writer.drain()
                except OSError:
                    return

        shared_io = last_io if last_io is not None else [time.monotonic()]
        while True:
            if is_upgrade:
                chunk = await _read_with_shared_idle(
                    client_reader, CHUNK_SIZE, WS_IDLE_TIMEOUT, shared_io
                )
                if chunk is None:
                    return
            else:
                try:
                    chunk = await asyncio.wait_for(
                        client_reader.read(CHUNK_SIZE), timeout=BODY_IDLE_TIMEOUT
                    )
                except TimeoutError:
                    return
            if not chunk:
                if upstream_writer.can_write_eof():
                    try:
                        upstream_writer.write_eof()
                    except OSError:
                        pass
                return
            if touch_activity:
                self._touch()
            try:
                upstream_writer.write(chunk)
                await upstream_writer.drain()
            except OSError:
                # Upstream closed its read side (e.g. early 4xx response); allow u2c to deliver it.
                return

    async def _relay_chunked_body(
        self,
        upstream_reader: asyncio.StreamReader,
        client_writer: asyncio.StreamWriter,
        initial_data: bytes,
        *,
        touch_activity: bool = True,
    ) -> None:
        """Stream an HTTP/1.1 chunked body and stop cleanly at the terminating 0-chunk."""
        buf = bytearray(initial_data)

        async def _read_more() -> bool:
            try:
                chunk = await asyncio.wait_for(
                    upstream_reader.read(CHUNK_SIZE), timeout=BODY_IDLE_TIMEOUT
                )
            except TimeoutError:
                return False
            if not chunk:
                return False
            buf.extend(chunk)
            if touch_activity:
                self._touch()
            return True

        while True:
            while b"\r\n" not in buf:
                if len(buf) > MAX_HEAD_BYTES:
                    return
                if not await _read_more():
                    if buf:
                        client_writer.write(bytes(buf))
                        await client_writer.drain()
                    return
            line_end = buf.find(b"\r\n")
            size_line = bytes(buf[: line_end + 2])
            del buf[: line_end + 2]
            client_writer.write(size_line)
            await client_writer.drain()

            size_token = size_line[:-2].split(b";", 1)[0].strip()
            if not size_token:
                return
            try:
                chunk_size = int(size_token, 16)
            except ValueError:
                return
            if chunk_size < 0:
                return

            if chunk_size == 0:
                trailer_bytes = 0
                while True:
                    while b"\r\n" not in buf:
                        if len(buf) > MAX_HEAD_BYTES:
                            return
                        if not await _read_more():
                            if buf:
                                client_writer.write(bytes(buf))
                                await client_writer.drain()
                            return
                    t_end = buf.find(b"\r\n")
                    t_line = bytes(buf[: t_end + 2])
                    del buf[: t_end + 2]
                    trailer_bytes += len(t_line)
                    client_writer.write(t_line)
                    await client_writer.drain()
                    if t_line == b"\r\n":
                        return
                    if trailer_bytes > MAX_HEAD_BYTES:
                        return

            remaining = chunk_size
            while remaining > 0:
                if buf:
                    take = min(len(buf), remaining)
                    client_writer.write(bytes(buf[:take]))
                    await client_writer.drain()
                    del buf[:take]
                    remaining -= take
                    continue
                if not await _read_more():
                    return

            while len(buf) < 2:
                if not await _read_more():
                    return
            crlf = bytes(buf[:2])
            del buf[:2]
            client_writer.write(crlf)
            await client_writer.drain()
            if crlf != b"\r\n":
                return

    async def _pipe_upstream_to_client(
        self,
        upstream_reader: asyncio.StreamReader,
        client_writer: asyncio.StreamWriter,
        *,
        method: str,
        is_upgrade: bool,
        touch_activity: bool = True,
        last_io: list[float] | None = None,
        upgrade_event: asyncio.Event | None = None,
        upgrade_state: dict[str, bool] | None = None,
    ) -> None:
        buf = bytearray()
        sent_anything = False
        shared_io = last_io if last_io is not None else [time.monotonic()]
        try:
            while True:
                while True:
                    sep = buf.find(b"\r\n\r\n")
                    if sep != -1:
                        if sep + 4 > MAX_HEAD_BYTES:
                            if not sent_anything:
                                await self._send_json(
                                    client_writer,
                                    502,
                                    "Bad Gateway",
                                    {"error": "Upstream response headers too large"},
                                )
                            return
                        raw_head = bytes(buf[:sep])
                        leftover = bytes(buf[sep + 4 :])
                        break
                    if len(buf) > MAX_HEAD_BYTES:
                        if not sent_anything:
                            await self._send_json(
                                client_writer,
                                502,
                                "Bad Gateway",
                                {"error": "Upstream response headers too large"},
                            )
                        return
                    try:
                        chunk = await asyncio.wait_for(
                            upstream_reader.read(8192), timeout=BODY_IDLE_TIMEOUT
                        )
                    except TimeoutError:
                        if not sent_anything:
                            await self._send_json(
                                client_writer,
                                504,
                                "Gateway Timeout",
                                {"error": "Upstream response timeout"},
                            )
                        return
                    if not chunk:
                        if not sent_anything:
                            await self._send_json(
                                client_writer,
                                502,
                                "Bad Gateway",
                                {"error": "Upstream closed connection before response"},
                            )
                        return
                    buf.extend(chunk)

                lines = raw_head.split(b"\r\n")
                status_line = lines[0].decode("latin-1") if lines else ""
                parts = status_line.split(" ", 2)
                status_code = (
                    int(parts[1])
                    if len(parts) >= 2 and parts[1].isascii() and parts[1].isdigit()
                    else 0
                )

                # Forward 1xx interim responses (e.g. 100 Continue) and keep reading final response
                if 100 <= status_code < 200 and status_code != 101:
                    client_writer.write(raw_head + b"\r\n\r\n")
                    await client_writer.drain()
                    sent_anything = True
                    buf = bytearray(leftover)
                    continue

                resp_conn_tokens: set[str] = set()
                for hdr_line in lines[1:]:
                    name_b, colon, val_b = hdr_line.partition(b":")
                    if colon and name_b.strip().lower() == b"connection":
                        for tok in val_b.decode("latin-1").split(","):
                            cleaned_tok = tok.strip().lower()
                            if cleaned_tok:
                                resp_conn_tokens.add(cleaned_tok)

                # Only enter raw bidirectional WebSocket relay when upstream actually upgrades (101).
                if is_upgrade and status_code == 101:
                    ws_strip = (
                        _BASE_HOP_BY_HOP_HEADERS | resp_conn_tokens
                    ) - _WS_PROTECTED_HEADERS
                    out_101 = [lines[0]]
                    for hdr_line in lines[1:]:
                        name_b, colon, _ = hdr_line.partition(b":")
                        if not colon:
                            out_101.append(hdr_line)
                            continue
                        lname_s = name_b.strip().lower().decode("latin-1")
                        if lname_s in ws_strip:
                            continue
                        out_101.append(hdr_line)
                    out_101.append(b"Connection: Upgrade")
                    client_writer.write(b"\r\n".join(out_101) + b"\r\n\r\n")
                    if leftover:
                        client_writer.write(leftover)
                    await client_writer.drain()
                    shared_io[0] = time.monotonic()
                    if upgrade_state is not None:
                        upgrade_state["confirmed"] = True
                    if upgrade_event is not None:
                        upgrade_event.set()
                    if touch_activity:
                        self._touch()
                    while True:
                        chunk = await _read_with_shared_idle(
                            upstream_reader, CHUNK_SIZE, WS_IDLE_TIMEOUT, shared_io
                        )
                        if chunk is None or not chunk:
                            return
                        if touch_activity:
                            self._touch()
                        client_writer.write(chunk)
                        await client_writer.drain()

                non_101_strip = (
                    _BASE_HOP_BY_HOP_HEADERS | resp_conn_tokens | {"upgrade"}
                ) - _PROTECTED_FRAMING_HEADERS
                out_lines = [lines[0]]
                has_te = False
                is_chunked = False
                content_length: int | None = None
                for hdr_line in lines[1:]:
                    name_b, colon, val_b = hdr_line.partition(b":")
                    if not colon:
                        out_lines.append(hdr_line)
                        continue
                    lname_s = name_b.strip().lower().decode("latin-1")
                    if lname_s in non_101_strip:
                        continue
                    if lname_s == "transfer-encoding":
                        has_te = True
                        te_tokens = [
                            t.strip().lower()
                            for t in val_b.decode("latin-1").split(",")
                            if t.strip()
                        ]
                        if te_tokens and te_tokens[-1] == "chunked":
                            is_chunked = True
                    elif lname_s == "content-length":
                        val_s = val_b.strip().decode("latin-1")
                        if val_s.isascii() and val_s.isdigit():
                            content_length = int(val_s)
                    out_lines.append(hdr_line)
                out_lines.append(b"Connection: close")

                client_writer.write(b"\r\n".join(out_lines) + b"\r\n\r\n")
                await client_writer.drain()
                sent_anything = True
                if touch_activity:
                    self._touch()

                if method.upper() == "HEAD" or status_code in (204, 304):
                    return

                if has_te and is_chunked:
                    await self._relay_chunked_body(
                        upstream_reader,
                        client_writer,
                        leftover,
                        touch_activity=touch_activity,
                    )
                    return

                if not has_te and content_length is not None:
                    remaining = content_length
                    if leftover and remaining > 0:
                        piece = leftover[:remaining]
                        client_writer.write(piece)
                        await client_writer.drain()
                        remaining -= len(piece)
                        if touch_activity:
                            self._touch()
                    while remaining > 0:
                        try:
                            chunk = await asyncio.wait_for(
                                upstream_reader.read(min(CHUNK_SIZE, remaining)),
                                timeout=BODY_IDLE_TIMEOUT,
                            )
                        except TimeoutError:
                            return
                        if not chunk:
                            return
                        client_writer.write(chunk)
                        await client_writer.drain()
                        remaining -= len(chunk)
                        if touch_activity:
                            self._touch()
                    return

                if leftover:
                    client_writer.write(leftover)
                    await client_writer.drain()
                    if touch_activity:
                        self._touch()
                while True:
                    try:
                        chunk = await asyncio.wait_for(
                            upstream_reader.read(CHUNK_SIZE),
                            timeout=BODY_IDLE_TIMEOUT,
                        )
                    except TimeoutError:
                        return
                    if not chunk:
                        return
                    client_writer.write(chunk)
                    await client_writer.drain()
                    if touch_activity:
                        self._touch()
        finally:
            if upgrade_event is not None:
                upgrade_event.set()


class ColabOneComfyCanvasGateway:
    """Manages CanvasProxyServer and a dedicated Cloudflared tunnel for ComfyUI Native Canvas."""

    def __init__(
        self,
        runtime,
        upstream_port: int = 8188,
        proxy_port: int = 8189,
        live_upstream_port: int = DEFAULT_LIVE_UPSTREAM_PORT,
    ):
        self.runtime = runtime
        self.upstream_port = int(upstream_port)
        self.proxy_port = int(proxy_port)
        self.live_upstream_port = int(live_upstream_port)

        self.enabled = False
        self.phase = "stopped"
        self.mode = "named"
        self.url = ""
        self.error = ""
        self.checked_at = 0.0
        self.logs: deque[dict] = deque(maxlen=100)
        self.log_seq = 0

        self.lock = threading.RLock()
        self.control_lock = threading.Lock()
        self._proc_lock = threading.RLock()

        self.instance = secrets.token_hex(16)
        self.canvas_token = secrets.token_urlsafe(32)
        self.session_cookie = secrets.token_urlsafe(32)

        self.proxy: ColabOneCanvasProxyServer | None = None
        self._tunnel_thread: threading.Thread | None = None
        self._tunnel_proc: subprocess.Popen | None = None
        self._tunnel_cancel = threading.Event()
        self._token_path: Path | None = None

    def note(self, message: str, level: str = "info") -> None:
        with self.lock:
            self.log_seq += 1
            self.logs.append(
                {
                    "seq": self.log_seq,
                    "time": time.time(),
                    "message": message,
                    "level": level,
                }
            )

    def _on_canvas_activity(self) -> None:
        if self.runtime is None:
            return
        try:
            self.runtime.touch()
        except Exception:
            pass

    def _start_proxy(self, expected_origin: str = "") -> int:
        self._stop_proxy()
        proxy = ColabOneCanvasProxyServer(
            upstream_host="127.0.0.1",
            upstream_port=self.upstream_port,
            listen_host="127.0.0.1",
            listen_port=self.proxy_port,
            instance=self.instance,
            canvas_token=self.canvas_token,
            session_cookie=self.session_cookie,
            on_activity=self._on_canvas_activity,
            live_upstream_port=self.live_upstream_port,
            expected_origin=expected_origin,
        )
        proxy.start()
        with self.lock:
            self.proxy = proxy
        return proxy.port

    def _stop_proxy(self) -> None:
        with self.lock:
            proxy = self.proxy
            self.proxy = None
        if proxy is not None:
            proxy.stop()

    def start(self, config: dict | None = None) -> dict:
        from pydantic import ValidationError
        from server.runtime import _scrub_secret_text

        raw_secrets: list[str] = []
        if isinstance(config, dict):
            tok = config.get("tunnel_token")
            if isinstance(tok, str) and tok:
                raw_secrets.append(tok)
        elif hasattr(config, "tunnel_token"):
            tok = getattr(config, "tunnel_token", "")
            if isinstance(tok, str) and tok:
                raw_secrets.append(tok)
        try:
            parsed = _validate_canvas_config(config)
        except ValidationError as exc:
            raw_msg = str(exc) or type(exc).__name__
            scrubbed = _scrub_secret_text(raw_msg, *raw_secrets)
            raise ValueError(scrubbed) from None
        with self.control_lock:
            if self.enabled:
                raise ValueError("请先关闭当前画布通道，再更换配置")
            if self._tunnel_thread and self._tunnel_thread.is_alive():
                self._tunnel_thread.join(timeout=5.0)
                if self._tunnel_thread.is_alive():
                    raise ValueError("画布隧道仍在停止，请稍后重试")

            runtime_lock = getattr(self.runtime, "lock", None)
            if runtime_lock is not None:
                with runtime_lock:
                    if getattr(self.runtime, "quiescing", False):
                        raise ValueError("实例正在释放")
                    if hasattr(self.runtime, "touch"):
                        self.runtime.touch(180)
            elif hasattr(self.runtime, "touch"):
                self.runtime.touch(180)

            # Rotate credentials on every start so old auth URLs and cookies cannot be reused.
            with self.lock:
                self.canvas_token = secrets.token_urlsafe(32)
                self.session_cookie = secrets.token_urlsafe(32)

            actual_port = self._start_proxy(expected_origin=parsed.domain)
            if not actual_port:
                actual_port = self.proxy.port if self.proxy else self.proxy_port

            try:
                with self.lock:
                    self.mode = "named"
                    self.enabled = True
                    self.phase = "starting"
                    self.url = ""
                    self.error = ""
                    self.checked_at = 0.0
                self.note("正在开启 ComfyUI 原生画布 · 固定域名")
                self._tunnel_cancel.clear()
                self._tunnel_thread = threading.Thread(
                    target=self._run_tunnel,
                    args=(parsed, actual_port),
                    daemon=True,
                )
                self._tunnel_thread.start()
            except Exception:
                self._stop_proxy()
                with self.lock:
                    self.enabled = False
                    self.phase = "stopped"
                    self.url = ""
                raise
        return self.snapshot()

    def probe(self, origin: str) -> bool:
        if self._tunnel_cancel.is_set():
            return False
        _call_public_addresses(origin)
        # Never follow redirects or transmit canvas_token before verifying runtime identity.
        with requests.Session() as http:
            http.trust_env = False

            def read(
                path: str, extra_headers: dict[str, str] | None = None
            ) -> tuple[int, bytes, dict]:
                req_kwargs: dict = {
                    "stream": True,
                    "timeout": (3, 5),
                    "allow_redirects": False,
                }
                if extra_headers:
                    req_kwargs["headers"] = extra_headers
                with http.get(origin + path, **req_kwargs) as response:
                    data = bytearray()
                    for chunk in response.iter_content(2048):
                        data.extend(chunk)
                        if len(data) > 4096:
                            raise ValueError("画布公网探测响应过大")
                    headers = getattr(response, "headers", None) or {}
                    return response.status_code, bytes(data), headers

            status, body, _ = read("/_colabone_health")
            if status != 200 or self._tunnel_cancel.is_set():
                return False
            try:
                payload = json.loads(body)
            except ValueError:
                return False
            if not isinstance(payload, dict) or payload.get("instance") != self.instance:
                return False

            status, _, _ = read("/")
            if status != 401 or self._tunnel_cancel.is_set():
                return False

            status, _, headers = read(
                f"/_colabone_auth?token={self.canvas_token}",
                extra_headers={COLABONE_INTERNAL_PROBE_HEADER: self.instance},
            )
            set_cookie = ""
            for k, v in headers.items():
                if k.lower() == "set-cookie":
                    set_cookie = str(v)
                    break
            return status == 302 and f"{COLABONE_COMFY_COOKIE}=" in set_cookie

    def _run_tunnel(self, config, proxy_port: int) -> None:
        origin = config.domain
        try:
            binary = _call_ensure_binary(
                self.runtime.root,
                self.runtime.catalog,
                self._tunnel_cancel,
                self.note,
            )
            if self._tunnel_cancel.is_set():
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

            with self._proc_lock:
                if self._tunnel_cancel.is_set():
                    return
                token_path = (
                    Path(self.runtime.root) / ".comfy-cloudflared-token"
                )
                if token_path.is_symlink():
                    raise ValueError("画布隧道凭据文件不能是符号链接")
                open_flags = (
                    os.O_WRONLY
                    | os.O_CREAT
                    | os.O_TRUNC
                    | getattr(os, "O_NOFOLLOW", 0)
                )
                try:
                    fd = os.open(token_path, open_flags, 0o600)
                except OSError as exc:
                    if exc.errno == errno.ELOOP:
                        raise ValueError("画布隧道凭据文件不能是符号链接") from None
                    raise
                self._token_path = token_path
                try:
                    if hasattr(os, "fchmod"):
                        os.fchmod(fd, 0o600)
                except BaseException:
                    os.close(fd)
                    raise
                with os.fdopen(fd, "w", encoding="utf-8") as out:
                    out.write(config.tunnel_token)
                args += ["run", "--token-file", str(self._token_path)]
                self._tunnel_proc = subprocess.Popen(
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
                process = self._tunnel_proc
            self.note("Cloudflare 画布连接器已启动，正在验证固定域名连通性与会话鉴权")

            def read_output() -> None:
                for stream in (process.stdout, process.stderr):
                    if stream is None:
                        continue
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

            threading.Thread(target=read_output, daemon=True).start()

            deadline, was_ready = time.monotonic() + 120, False
            while not self._tunnel_cancel.is_set():
                if process.poll() is not None:
                    raise RuntimeError(
                        "Cloudflare 画布连接器已退出，请检查网络或 Tunnel Token 后重试"
                    )
                valid = False
                if origin:
                    try:
                        valid = self.probe(origin)
                    except (requests.RequestException, ValueError, OSError):
                        pass
                if self._tunnel_cancel.is_set():
                    break
                if valid:
                    with self.lock:
                        if not was_ready:
                            self.note("ComfyUI 原生画布已通过公网连通性和鉴权检查")
                        self.phase = "ready"
                        self.url = origin
                        self.error = ""
                        self.checked_at = time.time()
                    was_ready = True
                elif was_ready:
                    with self.lock:
                        if self.phase != "reconnecting":
                            self.note("画布公网连接暂时不可用，正在重新检查")
                        self.phase = "reconnecting"
                elif time.monotonic() > deadline:
                    raise RuntimeError(
                        f"画布公网地址验证超时；固定域名请映射到 http://127.0.0.1:{proxy_port}，并检查 Cloudflare 路由与访问策略"
                    )
                self._tunnel_cancel.wait(25 if was_ready else 2)
        except Exception as exc:
            if not self._tunnel_cancel.is_set():
                # Known messages only; HTTP exceptions can contain URLs or auth tokens.
                message = (
                    str(exc)
                    if isinstance(exc, RuntimeError)
                    else "Cloudflare 画布连接失败，请检查网络与隧道配置"
                )
                with self.lock:
                    self.error = message
                    self.phase = "failed"
                    self.enabled = False
                    self.url = ""
                self.note(message, "error")
        finally:
            self._terminate_tunnel()
            if not self.enabled and not self._tunnel_cancel.is_set():
                self._stop_proxy()

    def _terminate_tunnel(self) -> None:
        with self._proc_lock:
            proc = self._tunnel_proc
            if proc is not None:
                if proc.poll() is None:
                    proc.terminate()
                    try:
                        proc.wait(timeout=4)
                    except subprocess.TimeoutExpired:
                        proc.kill()
                        try:
                            proc.wait(timeout=3)
                        except subprocess.TimeoutExpired:
                            pass
                self._tunnel_proc = None
            if self._token_path is not None:
                self._token_path.unlink(missing_ok=True)
                self._token_path = None

    def stop(self) -> dict:
        with self.control_lock:
            with self.lock:
                was_active = self.enabled or self.phase != "stopped"
                self.enabled = False
                self.url = ""
                if was_active:
                    self.phase = "stopping"
            self._tunnel_cancel.set()
            self._terminate_tunnel()
            thread = self._tunnel_thread
            still_alive = False
            try:
                if thread is not None and thread is not threading.current_thread():
                    thread.join(timeout=35)
                    still_alive = thread.is_alive()
                if not still_alive:
                    self._tunnel_thread = None
            finally:
                self._stop_proxy()
                if self.runtime is not None and getattr(self.runtime, "root", None):
                    (Path(self.runtime.root) / ".comfy-cloudflared-token").unlink(
                        missing_ok=True
                    )
            if still_alive:
                raise RuntimeError("画布隧道仍在停止，请稍后重试")
            with self.lock:
                self.enabled = False
                self.phase = "stopped"
                self.url = ""
                self.error = ""
                self.checked_at = 0.0
            if was_active:
                self.note("ComfyUI 原生画布已关闭")
        return self.snapshot()

    def shutdown(self) -> None:
        self.stop()

    def public_origin(self) -> str:
        with self.lock:
            if self.enabled and self.phase == "ready" and self.url:
                return self.url
            return ""

    def auth_url(self) -> dict:
        with self.lock:
            if not self.enabled or self.phase != "ready" or not self.url:
                raise RuntimeError("ComfyUI 原生画布尚未就绪，请先开启画布")
            return {
                "url": f"{self.url}/_colabone_auth?token={self.canvas_token}",
                "base_url": self.url,
            }

    def snapshot(self) -> dict:
        with self.lock:
            proxy = self.proxy
            return {
                "state": self.phase,
                "enabled": self.enabled,
                "mode": self.mode,
                "url": self.url if self.enabled else "",
                "error": self.error,
                "checked_at": self.checked_at,
                "active_connections": proxy.active_connections if proxy else 0,
                "logs": list(self.logs),
            }
