"""Offline public inference/Cloudflare contract tests. No live account or GPU calls."""

import asyncio
import base64
import io
import json
import threading
import time
from pathlib import Path
from unittest.mock import Mock

import httpx
import pytest
from fastapi.testclient import TestClient

from bridge.controller import Controller
from bridge.activity import safe_message
from common.public_api import PublicConfig, public_origin, tunnel_token
from server.app import create_app
from server.public_api import ChatRequest, create_public_app
from server.runtime import Conflict, Task
from server.tunnel import ensure_binary
from conftest import MemoryVault, wait_task
from test_creative import creative_model, png

KEY = "co_" + "k" * 48
AUTH = {"Authorization": "Bearer " + KEY}
TOKEN = base64.b64encode(
    json.dumps({"a": "account", "t": "tunnel", "s": "secret"}).encode()
).decode()


@pytest.fixture
def gateway(runtime):
    g = runtime.public_api
    g.key, g.enabled, g.phase = KEY, True, "ready"
    runtime.loaded = "test-model"
    return g


def chat(**kwargs):
    return {
        "model": "test-model",
        "messages": [{"role": "user", "content": "private prompt"}],
        "max_tokens": 20,
    } | kwargs


def test_gateway_never_exposes_control_or_credentials(gateway):
    with TestClient(create_public_app(gateway)) as client:
        for path in ("/api/v1/status", "/api/v1/snapshot", "/docs", "/openapi.json"):
            assert client.get(path, headers=AUTH).status_code == 404
        assert client.get("/v1/models").status_code == 401
        assert (
            client.get(
                "/v1/models", headers={"Authorization": "Bearer internal-control-key"}
            ).status_code
            == 401
        )
        health = client.get("/health").json()
        assert set(health) == {"service", "instance"}
        data = client.get("/v1/models", headers=AUTH).json()
        assert data["data"][0]["id"] == "test-model"
        assert KEY not in json.dumps(gateway.snapshot())
        gateway.stop()
        assert client.get("/v1/models", headers=AUTH).status_code == 503


def test_control_api_protects_tunnel_configuration(runtime):
    with TestClient(
        create_app(runtime, "private-control-key-at-least-32-long")
    ) as client:
        assert (
            client.post("/api/v1/public-api/start", json={"api_key": KEY}).status_code
            == 401
        )
        assert client.post("/api/v1/public-api/stop").status_code == 401


def test_quick_rejects_stream_and_invalid_input_without_work(gateway):
    with TestClient(create_public_app(gateway)) as client:
        response = client.post(
            "/v1/chat/completions", json=chat(stream=True), headers=AUTH
        )
        assert response.status_code == 400 and "stream=false" in response.text
        for body in (
            chat(model="missing"),
            chat(tools=[]),
            chat(
                messages=[
                    {"role": "user", "content": [{"image_url": "http://internal"}]}
                ]
            ),
        ):
            response = client.post("/v1/chat/completions", json=body, headers=AUTH)
            assert response.status_code in (400, 409)
            assert "private prompt" not in response.text
        assert (
            client.post(
                "/v1/chat/completions", content=b"x" * (1024**2 + 1), headers=AUTH
            ).status_code
            == 413
        )
        assert not gateway.runtime.tasks


def test_chat_proxy_usage_and_no_desktop_history(gateway):
    runtime = gateway.runtime
    before = runtime.last_active = time.time() - 200
    captured = []

    def upstream(request):
        captured.append(json.loads(request.content))
        assert runtime.busy()
        assert runtime.idle_status()["phase"] == "active"
        with pytest.raises(Conflict):
            runtime.unload()
        return httpx.Response(
            200,
            json={
                "id": "chatcmpl-test",
                "object": "chat.completion",
                "choices": [
                    {
                        "message": {"role": "assistant", "content": "Hello"},
                        "finish_reason": "stop",
                        "index": 0,
                    }
                ],
                "usage": {"total_tokens": 5},
            },
        )

    runtime.chat_transport = httpx.MockTransport(upstream)
    with TestClient(create_public_app(gateway)) as client:
        client.get("/health")
        client.get("/v1/models", headers=AUTH)
        assert runtime.last_active == before
        response = client.post("/v1/chat/completions", json=chat(), headers=AUTH)
        assert response.status_code == 200, response.text
        assert response.json()["usage"]["total_tokens"] == 5
    assert captured[0]["max_tokens"] == 20
    assert not runtime.conversations and not list(
        runtime.root.glob("conversation-*.json")
    )
    assert runtime.last_active > before and not runtime.busy()
    snapshot = gateway.snapshot()
    assert snapshot["completed"] == snapshot["requests"] == 1
    assert "private prompt" not in json.dumps(snapshot)


def test_named_stream_and_midstream_failure(gateway):
    gateway.mode = "named"
    gateway.runtime.chat_transport = httpx.MockTransport(
        lambda _: httpx.Response(
            200,
            text='data: {"choices":[{"delta":{"content":"Hello"}}]}\n\ndata: [DONE]\n\n',
        )
    )
    with TestClient(create_public_app(gateway)) as client:
        response = client.post(
            "/v1/chat/completions", json=chat(stream=True), headers=AUTH
        )
        assert response.status_code == 200 and "[DONE]" in response.text
        assert response.headers["content-type"].startswith("text/event-stream")
        gateway.runtime.chat_transport = httpx.MockTransport(
            lambda _: httpx.Response(200, text='data: {"choices":[]}\n\n')
        )
        response = client.post(
            "/v1/chat/completions", json=chat(stream=True), headers=AUTH
        )
        assert '"error"' in response.text and "提前中断" in response.text


def test_api_stop_cancels_stalled_engine_and_keeps_loaded_model(gateway):
    arrived = threading.Event()

    class Slow(httpx.AsyncByteStream):
        async def __aiter__(self):
            arrived.set()
            await asyncio.sleep(60)
            yield b"unused"

    gateway.runtime.chat_transport = httpx.MockTransport(
        lambda _: httpx.Response(200, stream=Slow())
    )
    identity = gateway.chat(ChatRequest(**chat()))
    assert arrived.wait(2)
    gateway.stop()
    assert wait_task(gateway.runtime, {"id": identity}).state == "cancelled"
    assert not gateway.runtime.busy()
    assert gateway.runtime.loaded == "test-model"


@pytest.mark.parametrize("kind", ["image", "video"])
def test_async_creative_results_owned_and_downloadable(gateway, monkeypatch, kind):
    runtime = gateway.runtime
    model = creative_model(runtime, kind)
    raw = (
        png()
        if kind == "image"
        else base64.b64encode(b"\x1a\x45\xdf\xa3test-video").decode()
    )

    def generate(runtime, task, request):
        task.outputs.append(runtime.creative.save(raw, request, model.name, 42, 1))

    monkeypatch.setattr("server.creative.generate", generate)
    with TestClient(create_public_app(gateway)) as client:
        response = client.post(
            "/v1/jobs",
            headers=AUTH,
            json={
                "model_id": model.id,
                "kind": kind,
                "prompt": "sea",
                "width": 512,
                "height": 512,
            },
        )
        assert response.status_code == 202, response.text
        identity = response.json()["id"]
        wait_task(runtime, {"id": identity})
        job = client.get("/v1/jobs/" + identity, headers=AUTH).json()
        assert job["status"] == "completed" and len(job["outputs"]) == 1
        path = job["outputs"][0]["content_path"]
        assert client.get(path).status_code == 401
        content = client.get(path, headers=AUTH)
        assert content.content == base64.b64decode(raw)
        assert content.headers["content-type"] == (
            "image/png" if kind == "image" else "video/webm"
        )
        assert (
            client.get(path.replace(identity, "foreign"), headers=AUTH).status_code
            == 404
        )
        assert (
            client.get("/v1/jobs/private-desktop-job", headers=AUTH).status_code == 404
        )
        assert (
            client.post(
                "/v1/jobs",
                headers=AUTH,
                json={
                    "model_id": model.id,
                    "kind": kind,
                    "prompt": "sea",
                    "reference_id": "a" * 32,
                },
            ).status_code
            == 400
        )
        assert client.delete("/v1/jobs/" + identity, headers=AUTH).status_code == 200


def test_openai_image_generations(gateway, monkeypatch):
    model = creative_model(gateway.runtime)

    def generate(runtime, task, request):
        assert request.width == request.height == 512
        task.outputs.append(runtime.creative.save(png(), request, model.name, 42, 1))

    monkeypatch.setattr("server.creative.generate", generate)
    with TestClient(create_public_app(gateway)) as client:
        response = client.post(
            "/v1/images/generations",
            headers=AUTH,
            json={"model": model.id, "prompt": "sea", "size": "512x512"},
        )
        assert response.status_code == 200, response.text
        assert base64.b64decode(response.json()["data"][0]["b64_json"]).startswith(
            b"\x89PNG"
        )


def test_token_paste_domain_validation_and_key_redaction():
    assert tunnel_token("sudo cloudflared service install " + TOKEN) == TOKEN
    value = PublicConfig(
        mode="named", domain="api.example.com/v1/", tunnel_token=TOKEN, api_key=KEY
    )
    assert value.domain == "https://api.example.com"
    for invalid in (
        "http://example.com",
        "127.0.0.1",
        "https://user@evil.com",
        "localhost",
        "api.local",
        "api.example.com:7860",
        "api.example.com/path",
        "api.example.com?token=secret",
    ):
        with pytest.raises(ValueError):
            public_origin(invalid)
    for invalid in ("", "exampletoken", TOKEN + " " + TOKEN[:-3] + "ZZZ"):
        with pytest.raises(ValueError):
            tunnel_token(invalid)
    assert KEY not in safe_message(KEY) and TOKEN not in safe_message(TOKEN)


def test_controller_vault_persists_config_not_secrets_in_snapshot(tmp_path):
    vault = MemoryVault()
    c = Controller(
        Path(__file__).parents[1], vault=vault, config_dir=tmp_path, background=False
    )
    c.command(
        "api.save",
        {
            "mode": "named",
            "domain": "api.example.com",
            "tunnel_token": "cloudflared service install " + TOKEN,
        },
    )
    key = c.command("api.key", {})["key"]
    snap = c.snapshot()
    assert (
        snap["api_settings"]["has_token"]
        and key not in json.dumps(snap)
        and TOKEN not in json.dumps(snap)
    )
    assert not (tmp_path / "settings.json").exists()
    recreated = Controller(c.root, vault=vault, config_dir=tmp_path, background=False)
    assert recreated.command("api.key", {})["key"] == key
    vault.set("runtime_session", {"endpoint": "existing"})
    c.remote = Mock(side_effect=RuntimeError("offline"))
    with pytest.raises(RuntimeError):
        c.command("api.rotate", {})
    assert c.command("api.key", {})["key"] == key
    c.remote = Mock(return_value={"ok": True})
    c.refresh_remote = Mock()
    c.command("api.rotate", {})
    c.remote.assert_called_once_with("POST", "public-api/stop")
    assert c.command("api.key", {})["key"] != key


def test_job_memory_bounded_and_no_old_job_access(gateway):
    runtime = gateway.runtime
    for i in range(102):
        task = Task("public_chat", "test-model")
        task.state = "completed"
        runtime.tasks[task.id] = task
        gateway.register({"id": task.id}, {"kind": "chat"})
    assert len(gateway.jobs) == len(runtime.tasks) == 100
    assert gateway.snapshot()["completed"] == 102


def test_connector_ready_only_after_probe_and_exact_process_stop(
    gateway, monkeypatch, tmp_path
):
    gateway.enabled = False
    monkeypatch.setattr(gateway, "serve", lambda: None)
    monkeypatch.setattr(
        "server.tunnel.ensure_binary", lambda *args: tmp_path / "cloudflared"
    )
    process = Mock()
    process.stdout = io.StringIO("https://test-only.trycloudflare.com\n")
    process.poll.return_value = None
    process.wait.side_effect = lambda **kw: setattr(process.poll, "return_value", 0)
    popen = Mock(return_value=process)
    monkeypatch.setattr("server.tunnel.subprocess.Popen", popen)
    arrived = threading.Event()

    def probe(origin):
        assert origin == "https://test-only.trycloudflare.com"
        assert gateway.phase == "starting"
        arrived.set()
        return True

    monkeypatch.setattr(gateway.tunnel, "probe", probe)
    gateway.start({"api_key": KEY})
    assert arrived.wait(2)
    for _ in range(100):
        if gateway.phase == "ready":
            break
        time.sleep(0.01)
    assert gateway.phase == "ready"
    args = popen.call_args.args[0]
    assert "http://127.0.0.1:7861" in args and KEY not in str(popen.call_args)
    gateway.stop()
    process.terminate.assert_called_once()
    assert (
        not gateway.tunnel.thread.is_alive() and not gateway.enabled and not gateway.url
    )


def test_named_connector_token_is_file_only_and_removed(gateway, monkeypatch, tmp_path):
    gateway.enabled = False
    monkeypatch.setattr(gateway, "serve", lambda: None)
    monkeypatch.setattr(
        "server.tunnel.ensure_binary", lambda *args: tmp_path / "cloudflared"
    )
    process = Mock(stdout=io.StringIO("token must not be logged " + TOKEN))
    process.poll.return_value = 1
    popen = Mock(return_value=process)
    monkeypatch.setattr("server.tunnel.subprocess.Popen", popen)
    gateway.start(
        {
            "api_key": KEY,
            "mode": "named",
            "domain": "api.example.com",
            "tunnel_token": TOKEN,
        }
    )
    gateway.tunnel.thread.join(2)
    assert gateway.phase == "failed"
    assert "--token-file" in popen.call_args.args[0]
    assert TOKEN not in str(popen.call_args) and TOKEN not in json.dumps(
        gateway.snapshot()
    )
    assert not (gateway.runtime.root / ".cloudflared-token").exists()


def test_binary_wrong_hash_never_executed(tmp_path, monkeypatch):
    catalog = tmp_path / "catalog"
    catalog.mkdir()
    (catalog / "cloudflared.json").write_text(
        json.dumps({"tag": "test", "sha256": {"amd64": "a" * 64}}), encoding="utf-8"
    )
    monkeypatch.setattr("server.tunnel.platform.system", lambda: "Linux")
    monkeypatch.setattr("server.tunnel.platform.machine", lambda: "x86_64")
    session, response = Mock(), Mock()
    session.__enter__ = Mock(return_value=session)
    session.__exit__ = Mock(return_value=False)
    response.__enter__ = Mock(return_value=response)
    response.__exit__ = Mock(return_value=False)
    response.iter_content.return_value = [b"not a real binary"]
    session.get.return_value = response
    monkeypatch.setattr("server.tunnel.requests.Session", lambda: session)
    with pytest.raises(RuntimeError, match="校验失败"):
        ensure_binary(tmp_path, catalog, threading.Event(), lambda _: None)
    assert not list((tmp_path / "bin").iterdir())


def test_real_loopback_stream_disconnect_cancels_inference(gateway):
    gateway.mode = "named"

    class Slow(httpx.AsyncByteStream):
        async def __aiter__(self):
            yield b'data: {"choices":[{"delta":{"content":"Hello"}}]}\n\n'
            await asyncio.sleep(60)
            yield b"data: [DONE]\n\n"

    gateway.runtime.chat_transport = httpx.MockTransport(
        lambda _: httpx.Response(200, stream=Slow())
    )
    try:
        gateway.serve(port=0)
        with httpx.Client(trust_env=False, timeout=3) as client:
            with client.stream(
                "POST",
                f"http://127.0.0.1:{gateway.port}/v1/chat/completions",
                json=chat(stream=True),
                headers=AUTH,
            ) as response:
                assert response.status_code == 200
                assert "Hello" in next(response.iter_lines())
        identity = next(iter(gateway.jobs))
        assert wait_task(gateway.runtime, {"id": identity}).state == "cancelled"
    finally:
        gateway.shutdown()
    assert not gateway.server_thread.is_alive()


def test_probe_checks_identity_before_sending_key(gateway, monkeypatch):
    monkeypatch.setattr("server.tunnel.public_addresses", lambda _: None)
    calls = []
    replies = [(200, {"instance": "another-runtime"})]

    class Response:
        def __init__(self, status, data):
            self.status_code, self.data = status, data

        def __enter__(self):
            return self

        def __exit__(self, *args):
            pass

        def iter_content(self, size):
            yield json.dumps(self.data).encode()

    class Session:
        def __enter__(self):
            return self

        def __exit__(self, *args):
            pass

        def get(self, url, **kwargs):
            calls.append(kwargs)
            assert kwargs["allow_redirects"] is False and kwargs["stream"] is True
            return Response(*replies.pop(0))

    monkeypatch.setattr("server.tunnel.requests.Session", Session)
    assert not gateway.tunnel.probe("https://api.example.com")
    assert len(calls) == 1 and calls[0]["headers"] is None
    calls.clear()
    replies.extend(
        [(200, {"instance": gateway.instance}), (401, {}), (200, {"object": "list"})]
    )
    assert gateway.tunnel.probe("https://api.example.com")
    assert len(calls) == 3 and calls[2]["headers"] == AUTH
