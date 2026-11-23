"""Offline tests for GPU-allocation-only proxy transport, isolation, and controller wiring."""

from __future__ import annotations

import base64
import json
import os
import socket
import struct
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock

import pytest
import requests

from bridge.activity import safe_message
from bridge.assign_proxy import (
    COLABONE_ASSIGN_PROXY_TIMEOUT,
    ColabOneAssignOnlySession,
    _socks5_connect,
    colabone_mask_assign_proxy,
    colabone_normalize_assign_proxy,
    colabone_parse_assign_proxy,
    colabone_proxy_log_label,
    colabone_test_assign_proxy,
)
from bridge.auth import ColabOneAuth
from bridge.controller import ColabOneController
from bridge.provider import ColabOneProvider
from colab_cli.client import Assignment, RuntimeProxyInfo
from conftest import MemoryVault


def _start_target_http_server():
    hits: list[str] = []

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def do_GET(self):
            hits.append(self.path)
            body = b"User-agent: *\nDisallow:\n"
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

    server = HTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server, server.server_port, hits


def _start_http_proxy_server(required_auth: tuple[str, str] | None = None):
    seen_requests: list[dict] = []

    class ProxyHandler(BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def do_GET(self):
            auth_header = self.headers.get("Proxy-Authorization", "")
            seen_requests.append({"path": self.path, "auth": auth_header})
            if required_auth is not None:
                expected = "Basic " + base64.b64encode(
                    f"{required_auth[0]}:{required_auth[1]}".encode("utf-8")
                ).decode("ascii")
                if auth_header != expected:
                    self.send_response(407)
                    self.send_header("Proxy-Authenticate", 'Basic realm="test"')
                    self.end_headers()
                    return
            # Forward plain HTTP request locally (offline loopback test).
            with requests.Session() as upstream:
                upstream.trust_env = False
                resp = upstream.get(self.path, timeout=5)
            self.send_response(resp.status_code)
            self.send_header("Content-Length", str(len(resp.content)))
            self.end_headers()
            self.wfile.write(resp.content)

    server = HTTPServer(("127.0.0.1", 0), ProxyHandler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server, server.server_port, seen_requests


def _recv_exact(conn: socket.socket, n: int) -> bytes:
    data = bytearray()
    while len(data) < n:
        chunk = conn.recv(n - len(data))
        if not chunk:
            raise EOFError("short read")
        data.extend(chunk)
    return bytes(data)


def _start_socks5_server(
    target_port: int, required_auth: tuple[str, str] | None = None
):
    """Minimal offline RFC 1928 / RFC 1929 SOCKS5 loopback server.

    Records requested ATYP + target host/port and redirects accepted connections
    to 127.0.0.1:<target_port> so even remote-DNS domain tests stay 100% offline.
    """
    records: list[dict] = []
    srv = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    srv.bind(("127.0.0.1", 0))
    srv.listen(8)
    srv.settimeout(0.2)
    stop_evt = threading.Event()

    def _pipe(src: socket.socket, dst: socket.socket):
        try:
            while True:
                buf = src.recv(4096)
                if not buf:
                    break
                dst.sendall(buf)
        except OSError:
            pass
        finally:
            try:
                dst.shutdown(socket.SHUT_WR)
            except OSError:
                pass

    def _handle(conn: socket.socket):
        upstream = None
        try:
            conn.settimeout(5)
            ver, nmethods = _recv_exact(conn, 2)
            methods = _recv_exact(conn, nmethods)
            assert ver == 5
            if required_auth is not None:
                if 2 not in methods:
                    conn.sendall(b"\x05\xff")
                    return
                conn.sendall(b"\x05\x02")
                aver, ulen = _recv_exact(conn, 2)
                assert aver == 1
                user = _recv_exact(conn, ulen).decode("utf-8")
                plen = _recv_exact(conn, 1)[0]
                pwd = _recv_exact(conn, plen).decode("utf-8")
                if (user, pwd) != required_auth:
                    conn.sendall(b"\x01\x01")
                    return
                conn.sendall(b"\x01\x00")
            else:
                conn.sendall(b"\x05\x00")

            rver, cmd, _rsv, atyp = _recv_exact(conn, 4)
            assert rver == 5 and cmd == 1
            if atyp == 1:
                host = socket.inet_ntop(socket.AF_INET, _recv_exact(conn, 4))
            elif atyp == 3:
                dlen = _recv_exact(conn, 1)[0]
                host = _recv_exact(conn, dlen).decode("ascii")
            elif atyp == 4:
                host = socket.inet_ntop(socket.AF_INET6, _recv_exact(conn, 16))
            else:
                conn.sendall(b"\x05\x08\x00\x01\x00\x00\x00\x00\x00\x00")
                return
            port = struct.unpack("!H", _recv_exact(conn, 2))[0]
            records.append({"atyp": atyp, "host": host, "port": port})

            upstream = socket.create_connection(("127.0.0.1", target_port), timeout=5)
            conn.sendall(b"\x05\x00\x00\x01\x7f\x00\x00\x01" + struct.pack("!H", target_port))
            t1 = threading.Thread(target=_pipe, args=(conn, upstream), daemon=True)
            t2 = threading.Thread(target=_pipe, args=(upstream, conn), daemon=True)
            t1.start()
            t2.start()
            t1.join(timeout=5)
            t2.join(timeout=5)
        except Exception:
            pass
        finally:
            try:
                conn.close()
            except OSError:
                pass
            if upstream is not None:
                try:
                    upstream.close()
                except OSError:
                    pass

    def _serve():
        while not stop_evt.is_set():
            try:
                conn, _ = srv.accept()
            except socket.timeout:
                continue
            except OSError:
                break
            threading.Thread(target=_handle, args=(conn,), daemon=True).start()

    thread = threading.Thread(target=_serve, daemon=True)
    thread.start()
    return srv, srv.getsockname()[1], records, stop_evt


def test_parse_and_mask_assign_proxy_urls():
    assert colabone_parse_assign_proxy("") is None
    assert colabone_normalize_assign_proxy("   ") == ""

    spec = colabone_parse_assign_proxy("SOCKS5://alice:s3cr%40et@us.proxy.test:1080/")
    assert spec is not None
    assert spec.scheme == "socks5"
    assert spec.host == "us.proxy.test"
    assert spec.port == 1080
    assert spec.username == "alice"
    assert spec.password == "s3cr@et"
    assert spec.has_auth is True
    assert spec.normalized_url == "socks5://alice:s3cr%40et@us.proxy.test:1080"
    assert spec.masked_url == "socks5://us.proxy.test:1080"
    assert spec.log_label == "SOCKS5 us.proxy.test:1080"
    assert "alice" not in spec.masked_url
    assert "s3cr" not in spec.log_label

    assert colabone_mask_assign_proxy("http://user:pw@127.0.0.1:7890") == "http://127.0.0.1:7890"
    assert colabone_proxy_log_label("socks5h://[::1]:1081") == "SOCKS5 [::1]:1081"
    assert colabone_parse_assign_proxy("http://127.0.0.1").port == 80
    assert colabone_parse_assign_proxy("https://proxy.example").port == 443
    assert colabone_parse_assign_proxy("socks5h://127.0.0.1").port == 1080
    # Re-encodes raw '@' in password per RFC 3986 and drops empty '@' prefixes.
    at_spec = colabone_parse_assign_proxy("http://alice:p@ss@127.0.0.1:8080")
    assert at_spec is not None
    assert at_spec.password == "p@ss"
    assert at_spec.normalized_url == "http://alice:p%40ss@127.0.0.1:8080"
    empty_at = colabone_parse_assign_proxy("http://@127.0.0.1:7890")
    assert empty_at is not None
    assert empty_at.normalized_url == "http://127.0.0.1:7890"
    assert empty_at.has_auth is False


@pytest.mark.parametrize(
    "bad",
    [
        "127.0.0.1:7890",
        "ftp://127.0.0.1:21",
        "http://127.0.0.1:99999",
        "http://127.0.0.1:0",
        "http://127.0.0.1:7890/path",
        "http://127.0.0.1:7890?a=1",
        "socks5://127.0.0.1:1080 bad",
        "socks5://:1080",
        "http://..:8080",
        "http://-bad.example:8080",
        "http://[fe80::1%25lo0]:8080",
        "socks5://" + ("u" * 256) + ":pw@127.0.0.1:1080",
    ],
)
def test_parse_assign_proxy_rejects_invalid_urls(bad):
    with pytest.raises(ValueError):
        colabone_parse_assign_proxy(bad)


def test_safe_message_redacts_socks5_urls():
    msg = "proxy failed via socks5h://alice:supersecret@10.0.0.2:1080 and socks5://u:p@1.2.3.4:1080"
    cleaned = safe_message(msg)
    assert "alice" not in cleaned
    assert "supersecret" not in cleaned
    assert "10.0.0.2" not in cleaned
    assert "[链接已隐藏]" in cleaned


def test_http_proxy_probe_and_auth_offline():
    target_srv, target_port, hits = _start_target_http_server()
    proxy_srv, proxy_port, seen = _start_http_proxy_server(
        required_auth=("colabuser", "topsecret")
    )
    try:
        probe_url = f"http://127.0.0.1:{target_port}/robots.txt"
        res = colabone_test_assign_proxy(
            f"http://colabuser:topsecret@127.0.0.1:{proxy_port}",
            probe_url=probe_url,
            timeout=5.0,
        )
        assert res["ok"] is True
        assert res["status_code"] == 200
        assert res["proxy"] == f"HTTP 127.0.0.1:{proxy_port}"
        assert hits == ["/robots.txt"]
        assert len(seen) == 1

        with pytest.raises(RuntimeError, match="认证失败"):
            colabone_test_assign_proxy(
                f"http://colabuser:wrongpass@127.0.0.1:{proxy_port}",
                probe_url=probe_url,
                timeout=5.0,
            )
    finally:
        proxy_srv.shutdown()
        proxy_srv.server_close()
        target_srv.shutdown()
        target_srv.server_close()


def test_socks5_proxy_probe_remote_dns_and_auth_offline():
    target_srv, target_port, hits = _start_target_http_server()
    s_sock, s_port, records, stop_evt = _start_socks5_server(
        target_port, required_auth=("u1", "p@ss")
    )
    try:
        # Use an unresolvable domain name to prove SOCKS5 uses ATYP=0x03 (remote DNS)
        # instead of resolving locally!
        res = colabone_test_assign_proxy(
            f"socks5://u1:p%40ss@127.0.0.1:{s_port}",
            probe_url="http://colab-offline-remote-dns.invalid:80/robots.txt",
            timeout=5.0,
        )
        assert res["ok"] is True
        assert res["status_code"] == 200
        assert hits == ["/robots.txt"]
        assert records == [
            {"atyp": 3, "host": "colab-offline-remote-dns.invalid", "port": 80}
        ]

        with pytest.raises(RuntimeError, match="认证失败"):
            colabone_test_assign_proxy(
                f"socks5://u1:bad@127.0.0.1:{s_port}",
                probe_url="http://colab-offline-remote-dns.invalid:80/robots.txt",
                timeout=5.0,
            )
    finally:
        stop_evt.set()
        s_sock.close()
        target_srv.shutdown()
        target_srv.server_close()


def test_assign_only_session_enforces_path_tls_and_refresh_isolation():
    creds = SimpleNamespace(
        valid=True,
        expired=False,
        token="fake-token",
        before_request=lambda _req, _m, _u, headers: headers.update(
            {"Authorization": "Bearer fake-token"}
        ),
    )
    session = ColabOneAssignOnlySession(creds, "http://127.0.0.1:7890")
    try:
        assert session.trust_env is False
        assert session.verify is True
        assert session.proxies == {
            "http": "http://127.0.0.1:7890",
            "https": "http://127.0.0.1:7890",
        }
        # OAuth token refresh uses AuthorizedSession's internal session, which must NOT
        # carry the allocation proxy!
        assert session._auth_request_session is not None
        assert session._auth_request_session.proxies == {}

        for forbidden_url in (
            "http://colab.research.google.com/tun/m/assign",
            "https://evil.example/tun/m/assign",
            "https://colab.research.google.com/tun/m/assignments",
            "https://colab.research.google.com/tun/m/unassign/m-s-123",
            "https://colab.research.google.com/tun/m/ccu-info",
            "https://colab.research.google.com/tun/m/m-s-123/keep-alive/",
            "https://runtime.invalid/api/kernels",
        ):
            with pytest.raises(RuntimeError, match="仅 GPU 分配请求允许使用分配代理"):
                session.get(forbidden_url)

        captured_timeouts: list = []

        def fake_super_send(self_obj, req, **kwargs):
            captured_timeouts.append((req.url, kwargs.get("verify"), kwargs.get("timeout")))
            resp = requests.Response()
            resp.status_code = 200
            resp._content = b"{}"
            return resp

        from google.auth.transport.requests import AuthorizedSession

        orig_send = AuthorizedSession.send
        AuthorizedSession.send = fake_super_send
        try:
            session.get(
                "https://colab.research.google.com/tun/m/assign?nbh=abc",
                verify=False,
                timeout=None,
            )
        finally:
            AuthorizedSession.send = orig_send
        assert captured_timeouts == [
            (
                "https://colab.research.google.com/tun/m/assign?nbh=abc",
                True,
                COLABONE_ASSIGN_PROXY_TIMEOUT,
            )
        ]
    finally:
        session.close()


def test_socks5_handles_global_default_timeout_sentinel_and_short_error_header():
    target_srv, target_port, _hits = _start_target_http_server()
    s_sock, s_port, _records, stop_evt = _start_socks5_server(target_port)
    try:
        spec = colabone_parse_assign_proxy(f"socks5://127.0.0.1:{s_port}")
        assert spec is not None
        conn = _socks5_connect(
            spec,
            "colab-sentinel.invalid",
            80,
            timeout=socket._GLOBAL_DEFAULT_TIMEOUT,
        )
        try:
            assert conn.gettimeout() is None
        finally:
            conn.close()
    finally:
        stop_evt.set()
        s_sock.close()
        target_srv.shutdown()
        target_srv.server_close()

    # Short 4-byte error reply followed by immediate close should report RFC 1928 refusal.
    err_srv = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    err_srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    err_srv.bind(("127.0.0.1", 0))
    err_srv.listen(2)
    err_port = err_srv.getsockname()[1]

    def _serve_short_refusal():
        try:
            conn, _ = err_srv.accept()
            with conn:
                conn.settimeout(3)
                _recv_exact(conn, 3)  # greeting
                conn.sendall(b"\x05\x00")
                _recv_exact(conn, 4)  # CONNECT header
                conn.sendall(b"\x05\x05\x00\x01")  # connection refused, no BND.ADDR
        except OSError:
            pass

    t = threading.Thread(target=_serve_short_refusal, daemon=True)
    t.start()
    try:
        spec = colabone_parse_assign_proxy(f"socks5://127.0.0.1:{err_port}")
        assert spec is not None
        with pytest.raises(Exception, match="目标连接被拒绝"):
            _socks5_connect(spec, "127.0.0.1", 9, timeout=3.0)
    finally:
        err_srv.close()
        t.join(timeout=2)


def test_non_assign_proxy_transient_proxy_error_keeps_retrying():
    """Regression guard: when assign_proxy is NOT configured, transient system
    ProxyError must keep the normal retry behavior instead of failing fast."""
    vault = MemoryVault()
    provider = ColabOneProvider(
        ColabOneAuth(vault), vault, Path(__file__).resolve().parents[1]
    )
    provider._assign_retry_delays = (0, 0)
    attempts = [0]

    class FlakySystemProxyClient:
        def _get_assignment(self, *_args, **_kwargs):
            attempts[0] += 1
            if attempts[0] == 1:
                raise requests.exceptions.ProxyError("transient system proxy reset")
            return SimpleNamespace(token="xsrf")

        def _post_assignment(self, *_args, **_kwargs):
            return Assignment(
                endpoint="m-s-recovered",
                runtimeProxyInfo=RuntimeProxyInfo(
                    token="tok",
                    tokenExpiresInSeconds=3600,
                    url="https://m-s-recovered.prod.colab.googleusercontent.com",
                ),
            )

    provider.client_factory = lambda: FlakySystemProxyClient()
    sess = provider.allocate("T4")
    assert sess.endpoint == "m-s-recovered"
    assert attempts[0] == 2


def test_assign_proxy_connect_timeout_fails_fast_while_read_timeout_retries():
    vault = MemoryVault()
    provider = ColabOneProvider(
        ColabOneAuth(vault), vault, Path(__file__).resolve().parents[1]
    )
    provider._assign_retry_delays = (0, 0)

    connect_attempts = [0]

    class ConnectTimeoutProxyClient:
        session = SimpleNamespace(close=lambda: None)

        def _get_assignment(self, *_args, **_kwargs):
            connect_attempts[0] += 1
            raise requests.exceptions.ConnectTimeout("proxy blackhole")

        def _post_assignment(self, *_args, **_kwargs):
            raise AssertionError("should fail fast on ConnectTimeout")

    provider.assign_client_factory = lambda _url: ConnectTimeoutProxyClient()
    with pytest.raises(RuntimeError, match="连接 GPU 分配代理超时"):
        provider.allocate("T4", assign_proxy="http://127.0.0.1:7890")
    assert connect_attempts[0] == 1

    read_attempts = [0]

    class ReadTimeoutThenOkClient:
        session = SimpleNamespace(close=lambda: None)

        def _get_assignment(self, *_args, **_kwargs):
            read_attempts[0] += 1
            if read_attempts[0] == 1:
                raise requests.exceptions.ReadTimeout("google assign slow")
            return SimpleNamespace(token="xsrf")

        def _post_assignment(self, *_args, **_kwargs):
            return Assignment(
                endpoint="m-s-after-read-timeout",
                runtimeProxyInfo=RuntimeProxyInfo(
                    token="tok",
                    tokenExpiresInSeconds=3600,
                    url="https://m-s-after-read-timeout.prod.colab.googleusercontent.com",
                ),
            )

    provider.assign_client_factory = lambda _url: ReadTimeoutThenOkClient()
    sess = provider.allocate("T4", assign_proxy="http://127.0.0.1:7890")
    assert sess.endpoint == "m-s-after-read-timeout"
    assert read_attempts[0] == 2


def test_provider_uses_assign_proxy_exclusively_for_allocation_and_closes_session(monkeypatch):
    vault = MemoryVault()
    auth = ColabOneAuth(vault)
    provider = ColabOneProvider(auth, vault, Path(__file__).resolve().parents[1])

    regular_calls: list[str] = []
    proxy_calls: list[tuple[str, str]] = []
    closed_sessions: list[bool] = []

    class FakeRegularClient:
        def get_consumption_user_info(self):
            regular_calls.append("ccu-info")
            return SimpleNamespace(
                paid_compute_units_balance=50.0, consumption_rate_hourly=1.5
            )

        def list_assignments(self):
            regular_calls.append("list_assignments")
            return []

        def unassign(self, ep):
            regular_calls.append(f"unassign:{ep}")

    class FakeProxySession:
        def close(self):
            closed_sessions.append(True)

    class FakeAssignClient:
        def __init__(self, proxy_url: str):
            self.proxy_url = proxy_url
            self.session = FakeProxySession()

        def _get_assignment(self, *_args, **_kwargs):
            proxy_calls.append(("get_assign", self.proxy_url))
            return SimpleNamespace(token="xsrf-tok")

        def _post_assignment(self, *_args, **_kwargs):
            proxy_calls.append(("post_assign", self.proxy_url))
            return Assignment(
                endpoint="m-s-usw1-abc",
                runtimeProxyInfo=RuntimeProxyInfo(
                    token="rt-tok",
                    tokenExpiresInSeconds=3600,
                    url="https://m-s-usw1-abc.prod.colab.googleusercontent.com",
                ),
            )

    provider.client_factory = lambda: FakeRegularClient()
    provider.assign_client_factory = lambda proxy_url: FakeAssignClient(proxy_url)

    monkeypatch.setattr(provider, "ready", lambda _s, **_kw: regular_calls.append("ready"))
    monkeypatch.setattr(
        provider,
        "_upload_file",
        lambda _s, _l, remote_path, **_kw: regular_calls.append(f"upload:{remote_path}"),
    )
    monkeypatch.setattr(
        provider,
        "execute",
        lambda _s, _code, **_kw: (
            regular_calls.append("execute"),
            [{"text": "COLABONE_READY=1\n"}],
        )[1],
    )

    logs: list[str] = []
    vault.set("colab", {"token": "t", "refresh_token": "r"})
    monkeypatch.setattr(auth, "profile", lambda: {"email": "u@example.com", "sub": "1"})

    remote = provider.start(
        "T4",
        lambda _msg: None,
        on_log=lambda msg, *_a: logs.append(msg),
        assign_proxy="socks5://user:secretpw@127.0.0.1:1080",
    )
    assert remote["endpoint"] == "m-s-usw1-abc"
    assert proxy_calls == [
        ("get_assign", "socks5://user:secretpw@127.0.0.1:1080"),
        ("post_assign", "socks5://user:secretpw@127.0.0.1:1080"),
    ]
    assert closed_sessions == [True]
    # All non-allocation operations ran on the regular non-proxy path.
    assert "ccu-info" in regular_calls
    assert "list_assignments" in regular_calls
    assert "ready" in regular_calls
    assert "execute" in regular_calls
    assert any(c.startswith("upload:") for c in regular_calls)
    # Logs must show safe masked proxy label, never credentials.
    assert any("SOCKS5 127.0.0.1:1080" in line for line in logs)
    assert not any("secretpw" in line or "user:" in line for line in logs)
    # Process environment must remain untouched.
    assert "127.0.0.1:1080" not in os.environ.get("HTTPS_PROXY", "")
    assert "127.0.0.1:1080" not in os.environ.get("HTTP_PROXY", "")


def test_provider_proxy_error_fails_fast_without_retrying():
    vault = MemoryVault()
    provider = ColabOneProvider(
        ColabOneAuth(vault), vault, Path(__file__).resolve().parents[1]
    )
    provider._assign_retry_delays = (6, 12, 20)
    attempts = [0]
    closed = [False]

    class BrokenProxyClient:
        session = SimpleNamespace(close=lambda: closed.__setitem__(0, True))

        def _get_assignment(self, *_args, **_kwargs):
            attempts[0] += 1
            raise requests.exceptions.ProxyError("SOCKS5 代理用户名或密码认证失败")

        def _post_assignment(self, *_args, **_kwargs):
            raise AssertionError("should not reach POST")

    provider.assign_client_factory = lambda _url: BrokenProxyClient()

    with pytest.raises(RuntimeError, match="GPU 分配代理认证失败"):
        provider.allocate("T4", assign_proxy="socks5://u:bad@127.0.0.1:1080")
    assert attempts[0] == 1
    assert closed[0] is True


def test_provider_save_session_rollback_uses_regular_client_not_assign_proxy(monkeypatch):
    vault = MemoryVault()
    provider = ColabOneProvider(
        ColabOneAuth(vault), vault, Path(__file__).resolve().parents[1]
    )
    unassigned_via_regular: list[str] = []

    class FakeRegularClient:
        def unassign(self, ep):
            unassigned_via_regular.append(ep)

    class FakeAssignClient:
        session = SimpleNamespace(close=lambda: None)

        def _get_assignment(self, *_args, **_kwargs):
            return SimpleNamespace(token="xsrf")

        def _post_assignment(self, *_args, **_kwargs):
            return Assignment(
                endpoint="m-s-rollback",
                runtimeProxyInfo=RuntimeProxyInfo(
                    token="tok",
                    tokenExpiresInSeconds=3600,
                    url="https://m-s-rollback.prod.colab.googleusercontent.com",
                ),
            )

        def unassign(self, _ep):
            raise AssertionError("rollback unassign must not use assign-only proxy client")

    provider.client_factory = lambda: FakeRegularClient()
    provider.assign_client_factory = lambda _url: FakeAssignClient()
    monkeypatch.setattr(
        provider, "save_session", Mock(side_effect=OSError("keyring full"))
    )

    with pytest.raises(RuntimeError, match="保存实例会话失败"):
        provider.allocate("T4", assign_proxy="http://127.0.0.1:7890")
    assert unassigned_via_regular == ["m-s-rollback"]


def test_controller_assign_proxy_settings_vault_isolation_and_start(tmp_path):
    vault = MemoryVault()
    c = ColabOneController(
        Path(__file__).resolve().parents[1],
        vault=vault,
        config_dir=tmp_path,
        background=False,
    )
    c.auth = Mock()
    c.auth.build_rclone_conf.return_value = ""
    c.auth.has_drive_scope.return_value = True
    c.provider = Mock()
    c.provider.account.return_value = {"logged_in": True, "assignments": 1, "rate": 1.0}
    c.remote = Mock()
    c.refresh_remote = Mock()

    # Cannot enable before saving a proxy URL.
    with pytest.raises(ValueError, match="请先填写并保存分配代理地址"):
        c.command("settings.save", {"assign_proxy_enabled": True})

    # Save authenticated proxy: stored in vault (never in settings.json), auto-enabled, masked in snapshot.
    c.command(
        "settings.save",
        {"assign_proxy": "socks5://alice:supersecret@127.0.0.1:1080"},
    )
    assert vault.get("assign_proxy") == "socks5://alice:supersecret@127.0.0.1:1080"
    settings_file = (tmp_path / "settings.json").read_text(encoding="utf-8")
    assert "supersecret" not in settings_file
    assert "alice" not in settings_file
    assert json.loads(settings_file)["assign_proxy_enabled"] is True

    snap = c.snapshot()
    assert snap["settings"]["assign_proxy_enabled"] is True
    assert snap["assign_proxy_settings"] == {
        "enabled": True,
        "has_proxy": True,
        "masked_url": "socks5://127.0.0.1:1080",
        "has_auth": True,
    }
    assert "supersecret" not in json.dumps(snap, ensure_ascii=False)
    assert c.command("settings.assign_proxy", {}) == {
        "url": "socks5://alice:supersecret@127.0.0.1:1080"
    }

    # Start passes assign_proxy when enabled.
    captured_start_kwargs: list[dict] = []

    def fake_start(gpu, notify, assign_proxy="", **_kwargs):
        captured_start_kwargs.append({"gpu": gpu, "assign_proxy": assign_proxy})

    c.provider.start = fake_start
    c.start()
    assert captured_start_kwargs[-1]["assign_proxy"] == "socks5://alice:supersecret@127.0.0.1:1080"

    # Toggle off preserves saved proxy in vault, stops passing assign_proxy on start.
    c.command("settings.save", {"assign_proxy_enabled": False})
    snap_off = c.snapshot()
    assert snap_off["settings"]["assign_proxy_enabled"] is False
    assert snap_off["assign_proxy_settings"]["has_proxy"] is True
    assert snap_off["assign_proxy_settings"]["enabled"] is False
    c.start()
    assert captured_start_kwargs[-1]["assign_proxy"] == ""

    # Clear deletes from vault and disables flag.
    c.command("settings.save", {"clear_assign_proxy": True})
    assert vault.get("assign_proxy") is None
    snap_cleared = c.snapshot()
    assert snap_cleared["assign_proxy_settings"] == {
        "enabled": False,
        "has_proxy": False,
        "masked_url": "",
        "has_auth": False,
    }

    # Corrupted stored proxy value cannot be enabled either.
    vault.set("assign_proxy", "not-a-valid-proxy")
    with pytest.raises(ValueError, match="请先填写并保存分配代理地址"):
        c.command("settings.save", {"assign_proxy_enabled": True})
