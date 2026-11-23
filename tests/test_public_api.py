"""Offline public inference/Cloudflare, OpenAI Responses & Codex compaction contract tests."""

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

from common.public_api import ColabOnePublicConfig, public_origin, tunnel_token
from conftest import wait_task
from server.app import create_colabone_app
from server.openai_compat import (
    COLABONE_COMPACTION_PREFIX,
    MAX_BODY_BYTES,
    decode_compaction_content,
    encode_compaction_content,
    estimate_messages_tokens,
    estimate_tokens,
)
from server.public_api import ColabOneChatRequest, create_colabone_public_app
from server.runtime import ColabOneConflictError, ColabOneTask
from server.tunnel import ensure_binary

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


def parse_sse_events(text: str) -> list[tuple[str | None, dict]]:
    events: list[tuple[str | None, dict]] = []
    event_name: str | None = None
    for block in text.strip().split("\n\n"):
        event_name = None
        data_lines: list[str] = []
        for line in block.splitlines():
            if line.startswith("event:"):
                event_name = line[6:].strip()
            elif line.startswith("data:"):
                data_lines.append(line[5:].strip())
        if data_lines:
            raw = "\n".join(data_lines)
            if raw != "[DONE]":
                events.append((event_name, json.loads(raw)))
    return events


def test_gateway_never_exposes_control_or_credentials(gateway):
    with TestClient(create_colabone_public_app(gateway)) as client:
        for path in ("/api/v1/status", "/api/v1/snapshot", "/docs", "/openapi.json"):
            assert client.get(path, headers=AUTH).status_code == 404
        for removed in ("/v1/images/generations", "/v1/jobs"):
            assert client.post(removed, json={}, headers=AUTH).status_code == 404
        assert client.get("/v1/models").status_code == 401
        assert client.post("/v1/responses", json={"model": "test-model", "input": "hi"}).status_code == 401
        assert client.post("/v1/responses/compact", json={"model": "test-model", "input": []}).status_code == 401
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
        detail = client.get("/v1/models/test-model", headers=AUTH)
        assert detail.status_code == 200 and detail.json()["id"] == "test-model"
        assert client.get("/v1/models/other-model", headers=AUTH).status_code == 404
        snap = gateway.snapshot()
        assert snap["mode"] == "named" and snap["streaming"] is True
        assert KEY not in json.dumps(snap)
        gateway.stop()
        assert client.get("/v1/models", headers=AUTH).status_code == 503


def test_control_api_protects_tunnel_configuration(runtime):
    with TestClient(
        create_colabone_app(runtime, "private-control-key-at-least-32-long")
    ) as client:
        assert (
            client.post("/api/v1/public-api/start", json={"api_key": KEY}).status_code
            == 401
        )
        assert client.post("/api/v1/public-api/stop").status_code == 401


def test_invalid_inputs_rejected_without_work_or_prompt_leak(gateway):
    with TestClient(create_colabone_public_app(gateway)) as client:
        for body in (
            chat(model="missing"),
            chat(tools="not-a-list"),
            chat(
                messages=[
                    {"role": "user", "content": [{"image_url": "http://internal"}]}
                ]
            ),
        ):
            response = client.post("/v1/chat/completions", json=body, headers=AUTH)
            assert response.status_code in (400, 409)
            assert "private prompt" not in response.text
        # Vision input is also rejected in /v1/responses.
        resp = client.post(
            "/v1/responses",
            json={
                "model": "test-model",
                "input": [
                    {
                        "role": "user",
                        "content": [{"type": "input_image", "image_url": "http://internal"}],
                    }
                ],
            },
            headers=AUTH,
        )
        assert resp.status_code == 400 and "http://internal" not in resp.text
        assert (
            client.post(
                "/v1/chat/completions",
                content=b"x" * (MAX_BODY_BYTES + 1),
                headers=AUTH,
            ).status_code
            == 413
        )
        assert not gateway.runtime.tasks


def test_chat_proxy_usage_tools_developer_role_and_no_desktop_history(gateway):
    runtime = gateway.runtime
    before = runtime.last_active = time.time() - 200
    captured = []

    def upstream(request):
        captured.append(json.loads(request.content))
        assert runtime.busy()
        assert runtime.idle_status()["phase"] == "active"
        with pytest.raises(ColabOneConflictError):
            runtime.unload()
        return httpx.Response(
            200,
            json={
                "id": "chatcmpl-test",
                "object": "chat.completion",
                "choices": [
                    {
                        "message": {
                            "role": "assistant",
                            "content": None,
                            "tool_calls": [
                                {
                                    "id": "call_1",
                                    "type": "function",
                                    "function": {
                                        "name": "read_file",
                                        "arguments": '{"path":"README.md"}',
                                    },
                                }
                            ],
                        },
                        "finish_reason": "tool_calls",
                        "index": 0,
                    }
                ],
                "usage": {"prompt_tokens": 12, "completion_tokens": 8, "total_tokens": 20},
            },
        )

    runtime.chat_transport = httpx.MockTransport(upstream)
    with TestClient(create_colabone_public_app(gateway)) as client:
        client.get("/health")
        client.get("/v1/models", headers=AUTH)
        assert runtime.last_active == before
        response = client.post(
            "/v1/chat/completions",
            json={
                "model": "test-model",
                "messages": [
                    {"role": "developer", "content": "Be concise."},
                    {
                        "role": "user",
                        "content": [{"type": "text", "text": "private prompt"}],
                    },
                ],
                "max_completion_tokens": 64,
                "tools": [
                    {
                        "type": "function",
                        "function": {
                            "name": "read_file",
                            "description": "Read a file",
                            "parameters": {"type": "object"},
                        },
                    }
                ],
                "tool_choice": "auto",
                "store": False,
                "prompt_cache_key": "sess-1",
            },
            headers=AUTH,
        )
        assert response.status_code == 200, response.text
        payload = response.json()
        assert payload["usage"]["total_tokens"] == 20
        assert payload["choices"][0]["message"]["tool_calls"][0]["function"]["name"] == "read_file"
    assert captured[0]["max_tokens"] == 64
    assert captured[0]["messages"][0] == {"role": "system", "content": "Be concise."}
    assert captured[0]["messages"][1] == {"role": "user", "content": "private prompt"}
    assert captured[0]["tools"][0]["function"]["name"] == "read_file"
    assert not list(runtime.root.glob("conversation-*.json"))
    assert runtime.last_active > before and not runtime.busy()
    snapshot = gateway.snapshot()
    assert snapshot["completed"] == snapshot["requests"] == 1
    assert "private prompt" not in json.dumps(snapshot)


def test_stream_by_default_and_midstream_failure(gateway):
    assert gateway.mode == "named"
    gateway.runtime.chat_transport = httpx.MockTransport(
        lambda _: httpx.Response(
            200,
            text='data: {"choices":[{"delta":{"content":"Hello"}}]}\n\ndata: [DONE]\n\n',
        )
    )
    with TestClient(create_colabone_public_app(gateway)) as client:
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


def test_responses_api_non_streaming_tools_custom_tool_and_store(gateway):
    captured = []

    def upstream(request):
        body = json.loads(request.content)
        captured.append(body)
        if len(captured) == 1:
            return httpx.Response(
                200,
                json={
                    "id": "chatcmpl-r1",
                    "choices": [
                        {
                            "message": {
                                "role": "assistant",
                                "reasoning_content": "Let me patch the file.",
                                "content": "Applying patch now.",
                                "tool_calls": [
                                    {
                                        "id": "call_fn_1",
                                        "type": "function",
                                        "function": {
                                            "name": "shell",
                                            "arguments": '{"command":"ls"}',
                                        },
                                    },
                                    {
                                        "id": "call_patch_1",
                                        "type": "function",
                                        "function": {
                                            "name": "apply_patch",
                                            "arguments": json.dumps(
                                                {"input": "*** Begin Patch\n*** End Patch"}
                                            ),
                                        },
                                    },
                                ],
                            },
                            "finish_reason": "tool_calls",
                        }
                    ],
                    "usage": {"prompt_tokens": 50, "completion_tokens": 30, "total_tokens": 80},
                },
            )
        return httpx.Response(
            200,
            json={
                "id": "chatcmpl-r2",
                "choices": [
                    {
                        "message": {"role": "assistant", "content": "Done!"},
                        "finish_reason": "length",
                    }
                ],
                "usage": {"prompt_tokens": 70, "completion_tokens": 10, "total_tokens": 80},
            },
        )

    gateway.runtime.chat_transport = httpx.MockTransport(upstream)
    with TestClient(create_colabone_public_app(gateway)) as client:
        r1 = client.post(
            "/v1/responses",
            json={
                "model": "test-model",
                "instructions": "You are Codex.",
                "input": "Fix the bug in app.py",
                "tools": [
                    {
                        "type": "function",
                        "name": "shell",
                        "description": "Run shell command",
                        "parameters": {"type": "object"},
                    },
                    {
                        "type": "custom",
                        "name": "apply_patch",
                        "description": "Apply patch",
                    },
                    {"type": "web_search"},
                ],
                "tool_choice": "auto",
                "parallel_tool_calls": True,
                "service_tier": "default",
            },
            headers=AUTH,
        )
        assert r1.status_code == 200, r1.text
        data1 = r1.json()
        assert data1["object"] == "response"
        assert data1["status"] == "completed"
        assert data1["usage"]["input_tokens"] == 50
        assert data1["usage"]["output_tokens"] == 30
        assert [item["type"] for item in data1["output"]] == [
            "reasoning",
            "message",
            "function_call",
            "custom_tool_call",
        ]
        assert data1["output"][0]["summary"][0]["text"] == "Let me patch the file."
        assert data1["output"][1]["content"][0]["text"] == "Applying patch now."
        assert data1["output"][2]["name"] == "shell"
        assert data1["output"][2]["arguments"] == '{"command":"ls"}'
        assert data1["output"][3]["name"] == "apply_patch"
        assert data1["output"][3]["input"] == "*** Begin Patch\n*** End Patch"

        # Retrieve stored response via GET /v1/responses/{id}.
        fetched = client.get(f"/v1/responses/{data1['id']}", headers=AUTH)
        assert fetched.status_code == 200 and fetched.json()["id"] == data1["id"]

        # Continue via previous_response_id with tool outputs + orphan tool output.
        r2 = client.post(
            "/v1/responses",
            json={
                "model": "test-model",
                "previous_response_id": data1["id"],
                "input": [
                    {
                        "type": "function_call_output",
                        "call_id": "call_fn_1",
                        "output": "app.py\nREADME.md",
                    },
                    {
                        "type": "custom_tool_call_output",
                        "call_id": "call_patch_1",
                        "output": [{"type": "input_text", "text": "Applied cleanly"}],
                    },
                    {
                        "type": "function_call_output",
                        "call_id": "",
                        "output": "background worker note",
                    },
                ],
                "max_output_tokens": 10,
            },
            headers=AUTH,
        )
        assert r2.status_code == 200, r2.text
        data2 = r2.json()
        assert data2["status"] == "incomplete"
        assert data2["incomplete_details"] == {"reason": "max_output_tokens"}
        assert data2["output"][0]["content"][0]["text"] == "Done!"

        # Check upstream saw paired tool outputs + safe fallback for orphan output.
        second_msgs = captured[1]["messages"]
        tool_msgs = [m for m in second_msgs if m["role"] == "tool"]
        assert {m["tool_call_id"] for m in tool_msgs} == {"call_fn_1", "call_patch_1"}
        assert any("background worker note" in (m.get("content") or "") for m in second_msgs)

        # DELETE /v1/responses/{id}.
        deleted = client.delete(f"/v1/responses/{data1['id']}", headers=AUTH)
        assert deleted.status_code == 200 and deleted.json()["deleted"] is True
        assert client.get(f"/v1/responses/{data1['id']}", headers=AUTH).status_code == 404


def test_responses_api_streaming_sse_lifecycle_and_custom_tools(gateway):
    chunks = [
        {"choices": [{"delta": {"reasoning_content": "Think..."}, "finish_reason": None}]},
        {"choices": [{"delta": {"content": "Hi "}, "finish_reason": None}]},
        {"choices": [{"delta": {"content": "there"}, "finish_reason": None}]},
        {
            "choices": [
                {
                    "delta": {
                        "tool_calls": [
                            {
                                "index": 0,
                                "id": "call_sh",
                                "type": "function",
                                "function": {"name": "shell", "arguments": '{"cmd":'},
                            }
                        ]
                    },
                    "finish_reason": None,
                }
            ]
        },
        {
            "choices": [
                {
                    "delta": {
                        "tool_calls": [
                            {
                                "index": 0,
                                "function": {"arguments": '"pytest"}'},
                            },
                            {
                                "index": 1,
                                "id": "call_ap",
                                "type": "function",
                                "function": {
                                    "name": "apply_patch",
                                    "arguments": '{"input":"*** Begin Patch"}',
                                },
                            },
                        ]
                    },
                    "finish_reason": "tool_calls",
                }
            ]
        },
        {
            "choices": [],
            "usage": {"prompt_tokens": 40, "completion_tokens": 25, "total_tokens": 65},
        },
    ]
    sse_body = "".join(f"data: {json.dumps(c)}\n\n" for c in chunks) + "data: [DONE]\n\n"
    gateway.runtime.chat_transport = httpx.MockTransport(
        lambda _: httpx.Response(200, text=sse_body)
    )
    with TestClient(create_colabone_public_app(gateway)) as client:
        response = client.post(
            "/v1/responses",
            json={
                "model": "test-model",
                "input": "Run tests and patch",
                "tools": [
                    {"type": "function", "name": "shell", "parameters": {"type": "object"}},
                    {"type": "custom", "name": "apply_patch"},
                ],
                "stream": True,
                "store": False,
            },
            headers=AUTH,
        )
        assert response.status_code == 200
        assert response.headers["content-type"].startswith("text/event-stream")
        events = parse_sse_events(response.text)
        types = [t for t, _ in events]
        seqs = [p["sequence_number"] for _, p in events]
        assert seqs == list(range(len(events)))
        assert types[0] == "response.created"
        assert types[1] == "response.in_progress"
        assert "response.reasoning_summary_text.delta" in types
        assert "response.output_text.delta" in types
        assert "response.output_text.done" in types
        assert "response.function_call_arguments.delta" in types
        assert "response.function_call_arguments.done" in types
        assert "response.custom_tool_call_input.delta" in types
        assert "response.custom_tool_call_input.done" in types
        assert types[-1] == "response.completed"

        final_resp = events[-1][1]["response"]
        assert final_resp["usage"]["input_tokens"] == 40
        assert final_resp["usage"]["output_tokens"] == 25
        assert [i["type"] for i in final_resp["output"]] == [
            "reasoning",
            "message",
            "function_call",
            "custom_tool_call",
        ]
        assert final_resp["output"][2]["arguments"] == '{"cmd":"pytest"}'
        assert final_resp["output"][3]["input"] == "*** Begin Patch"
        # store=False -> must not persist in gateway.responses cache.
        assert not gateway.responses


def test_codex_compaction_endpoint_chunking_and_stateless_replay(gateway):
    # Configure a tight context window so a large history exercises rolling multi-chunk compaction.
    gateway.runtime.active_options = {"context_length": 4096}
    captured_requests = []

    def upstream(request):
        body = json.loads(request.content)
        captured_requests.append(body)
        if any("Compact the following" in (m.get("content") or "") for m in body["messages"]):
            return httpx.Response(
                200,
                json={
                    "choices": [{"message": {"role": "assistant", "content": "Summary part 1: edited foo.py"}}],
                    "usage": {"prompt_tokens": 1200, "completion_tokens": 40, "total_tokens": 1240},
                },
            )
        if any("Update the running continuation summary" in (m.get("content") or "") for m in body["messages"]):
            return httpx.Response(
                200,
                json={
                    "choices": [{"message": {"role": "assistant", "content": "Final summary: edited foo.py and tests pass."}}],
                    "usage": {"prompt_tokens": 900, "completion_tokens": 50, "total_tokens": 950},
                },
            )
        return httpx.Response(
            200,
            json={
                "choices": [{"message": {"role": "assistant", "content": "Continuing from foo.py summary!"}}],
                "usage": {"prompt_tokens": 120, "completion_tokens": 15, "total_tokens": 135},
            },
        )

    gateway.runtime.chat_transport = httpx.MockTransport(upstream)
    long_history = [
        {"type": "message", "role": "user", "content": [{"type": "input_text", "text": "Refactor foo.py cleanly."}]},
        {"type": "function_call", "call_id": "c1", "name": "read_file", "arguments": '{"path":"foo.py"}'},
        {"type": "function_call_output", "call_id": "c1", "output": "x" * 7000},
        {"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "a" * 7000}]},
        {"type": "message", "role": "user", "content": [{"type": "input_text", "text": "Also run pytest."}]},
    ]

    with TestClient(create_colabone_public_app(gateway)) as client:
        comp = client.post(
            "/v1/responses/compact",
            json={
                "model": "test-model",
                "instructions": "You are Codex.",
                "input": long_history,
                "service_tier": "default",
            },
            headers=AUTH,
        )
        assert comp.status_code == 200, comp.text
        body = comp.json()
        assert body["object"] == "response.compaction"
        assert body["id"].startswith("resp_")
        assert body["usage"]["input_tokens"] == 2100
        assert body["usage"]["output_tokens"] == 90
        assert body["usage"]["total_tokens"] == 2190
        assert len(body["output"]) == 2
        assert body["output"][0]["type"] == "message"
        assert body["output"][0]["role"] == "user"
        assert body["output"][0]["content"][0]["text"] == "Refactor foo.py cleanly."
        comp_item = body["output"][1]
        assert comp_item["type"] == "compaction"
        assert comp_item["encrypted_content"].startswith(COLABONE_COMPACTION_PREFIX)
        assert (
            decode_compaction_content(comp_item["encrypted_content"])
            == "Final summary: edited foo.py and tests pass."
        )
        # Multi-chunk compaction triggered 2 summarization passes.
        assert len(captured_requests) == 2

        # Simulate GPU restart / new gateway instance -> stateless compaction blob still decodes cleanly!
        gateway.instance = "brand-new-instance-id"
        # Also include a compaction_summary alias and an opaque external OpenAI Fernet token.
        next_turn = client.post(
            "/v1/responses",
            json={
                "model": "test-model",
                "instructions": "You are Codex.",
                "input": [
                    *body["output"],
                    {"type": "compaction_summary", "encrypted_content": "gAAAAABexternal-opaque-token"},
                    {"role": "user", "content": "What is our next step?"},
                ],
                "stream": False,
            },
            headers=AUTH,
        )
        assert next_turn.status_code == 200, next_turn.text
        assert next_turn.json()["output"][0]["content"][0]["text"] == "Continuing from foo.py summary!"
        replayed_msgs = captured_requests[-1]["messages"]
        assert replayed_msgs[0]["role"] == "system"
        assert any(
            m["role"] == "assistant"
            and "Final summary: edited foo.py and tests pass." in (m.get("content") or "")
            for m in replayed_msgs
        )
        assert replayed_msgs[-1] == {"role": "user", "content": "What is our next step?"}


def test_responses_inline_context_management_compaction(gateway):
    calls = []

    def upstream(request):
        body = json.loads(request.content)
        calls.append(body)
        if len(calls) == 1:
            return httpx.Response(
                200,
                json={
                    "choices": [{"message": {"role": "assistant", "content": "Compact state: auth fixed."}}],
                    "usage": {"prompt_tokens": 100, "completion_tokens": 12, "total_tokens": 112},
                },
            )
        return httpx.Response(
            200,
            json={
                "choices": [{"message": {"role": "assistant", "content": "Ready to deploy."}}],
                "usage": {"prompt_tokens": 40, "completion_tokens": 8, "total_tokens": 48},
            },
        )

    gateway.runtime.chat_transport = httpx.MockTransport(upstream)
    with TestClient(create_colabone_public_app(gateway)) as client:
        resp = client.post(
            "/v1/responses",
            json={
                "model": "test-model",
                "input": [
                    {"role": "user", "content": "Fix auth bug " + ("details " * 40)},
                    {"role": "assistant", "content": "Fixed auth in login.py"},
                    {"role": "user", "content": "Can we deploy now?"},
                ],
                "context_management": [{"type": "compaction", "compact_threshold": 20}],
                "stream": False,
            },
            headers=AUTH,
        )
        assert resp.status_code == 200, resp.text
        data = resp.json()
        assert data["output"][0]["type"] == "compaction"
        assert decode_compaction_content(data["output"][0]["encrypted_content"]) == "Compact state: auth fixed."
        assert data["output"][1]["type"] == "message"
        assert data["output"][1]["content"][0]["text"] == "Ready to deploy."
        assert data["usage"]["input_tokens"] == 140
        assert data["usage"]["output_tokens"] == 20


def test_compaction_codec_bounds_decompression():
    encoded = encode_compaction_content("hello summary")
    assert decode_compaction_content(encoded) == "hello summary"
    assert decode_compaction_content("gAAAAABexternal") is None
    assert decode_compaction_content(COLABONE_COMPACTION_PREFIX + "!!!invalid!!!") is None


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
    identity = gateway.chat(ColabOneChatRequest(**chat()))
    assert arrived.wait(2)
    gateway.stop()
    assert wait_task(gateway.runtime, {"id": identity}).state == "cancelled"
    assert not gateway.runtime.busy()
    assert gateway.runtime.loaded == "test-model"


def test_token_paste_domain_validation_and_key_redaction():
    assert tunnel_token("sudo cloudflared service install " + TOKEN) == TOKEN
    value = ColabOnePublicConfig(
        mode="named", domain="api.example.com/v1/", tunnel_token=TOKEN, api_key=KEY
    )
    assert value.domain == "https://api.example.com"
    assert value.mode == "named"

    # Quick mode and missing fixed domain/token are rejected.
    with pytest.raises(ValueError):
        ColabOnePublicConfig(mode="quick", api_key=KEY)
    with pytest.raises(ValueError):
        ColabOnePublicConfig(api_key=KEY)

    for invalid in (
        "http://example.com",
        "127.0.0.1",
        "https://user@evil.com",
        "localhost",
        "api.local",
        "api.example.com:7860",
        "api.example.com/path",
        "api.example.com?token=secret",
        "demo.trycloudflare.com",
        "https://DEMO.TRYCLOUDFLARE.COM",
        "trycloudflare.com",
    ):
        with pytest.raises(ValueError):
            public_origin(invalid)
    for invalid in ("", "exampletoken", TOKEN + " " + TOKEN[:-3] + "ZZZ"):
        with pytest.raises(ValueError):
            tunnel_token(invalid)


def test_job_memory_bounded_and_no_old_job_access(gateway):
    runtime = gateway.runtime
    for i in range(102):
        task = ColabOneTask("public_chat", "test-model")
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
    process.stdout = io.StringIO("connected to edge\n")
    process.poll.return_value = None
    process.wait.side_effect = lambda **kw: setattr(process.poll, "return_value", 0)
    popen = Mock(return_value=process)
    monkeypatch.setattr("server.tunnel.subprocess.Popen", popen)
    arrived = threading.Event()

    def probe(origin):
        assert origin == "https://api.example.com"
        assert gateway.phase == "starting"
        arrived.set()
        return True

    monkeypatch.setattr(gateway.tunnel, "probe", probe)
    gateway.start(
        {
            "api_key": KEY,
            "mode": "named",
            "domain": "api.example.com",
            "tunnel_token": TOKEN,
        }
    )
    assert arrived.wait(2)
    for _ in range(100):
        if gateway.phase == "ready":
            break
        time.sleep(0.01)
    assert gateway.phase == "ready"
    assert gateway.url == "https://api.example.com"
    args = popen.call_args.args[0]
    assert "--token-file" in args
    assert TOKEN not in str(popen.call_args) and KEY not in str(popen.call_args)
    gateway.stop()
    process.terminate.assert_called_once()
    assert not (gateway.runtime.root / ".cloudflared-token").exists()
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
                f"http://127.0.0.1:{gateway.port}/v1/responses",
                json={"model": "test-model", "input": "hello", "stream": True},
                headers=AUTH,
            ) as response:
                assert response.status_code == 200
                lines = []
                for line in response.iter_lines():
                    lines.append(line)
                    if "Hello" in line:
                        break
                assert any("Hello" in l for l in lines)
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


def test_public_api_records_per_request_token_speed_for_chat_responses_and_compact(
    gateway,
):
    captured_upstream = []

    def upstream(request):
        body = json.loads(request.content)
        captured_upstream.append(body)
        if body.get("stream"):
            sse = (
                'data: {"choices":[{"delta":{"content":"Hel"}}]}\n\n'
                'data: {"choices":[{"delta":{"content":"lo"}}]}\n\n'
                'data: {"choices":[],"usage":{"prompt_tokens":32,"completion_tokens":16,"total_tokens":48},'
                '"timings":{"prompt_n":32,"prompt_per_second":920.5,"predicted_n":16,"predicted_per_second":54.2}}\n\n'
                "data: [DONE]\n\n"
            )
            return httpx.Response(200, text=sse)
        return httpx.Response(
            200,
            json={
                "id": "chatcmpl-speed",
                "choices": [
                    {
                        "message": {"role": "assistant", "content": "Fast reply"},
                        "finish_reason": "stop",
                    }
                ],
                "usage": {"prompt_tokens": 20, "completion_tokens": 12, "total_tokens": 32},
                "timings": {
                    "prompt_n": 20,
                    "prompt_ms": 25.0,
                    "prompt_per_second": 800.0,
                    "predicted_n": 12,
                    "predicted_ms": 300.0,
                    "predicted_per_second": 40.0,
                },
            },
        )

    gateway.runtime.chat_transport = httpx.MockTransport(upstream)
    with TestClient(create_colabone_public_app(gateway)) as client:
        # 1. Non-streaming chat completion with llama.cpp timings.
        r1 = client.post("/v1/chat/completions", json=chat(), headers=AUTH)
        assert r1.status_code == 200

        # 2. Streaming chat completion WITHOUT client stream_options.include_usage:
        #    gateway asks llama-server for usage/timings internally and suppresses
        #    the synthetic usage-only chunk from the wire response.
        r2 = client.post(
            "/v1/chat/completions", json=chat(stream=True), headers=AUTH
        )
        assert r2.status_code == 200
        assert captured_upstream[1].get("stream_options") == {"include_usage": True}
        assert '"usage"' not in r2.text and "[DONE]" in r2.text

        # 3. Streaming chat completion WITH include_usage=True passes usage chunk through.
        r3 = client.post(
            "/v1/chat/completions",
            json=chat(stream=True, stream_options={"include_usage": True}),
            headers=AUTH,
        )
        assert r3.status_code == 200
        assert '"usage"' in r3.text

        # 4. Streaming /v1/responses records timings & TTFT.
        r4 = client.post(
            "/v1/responses",
            json={"model": "test-model", "input": "private prompt", "stream": True},
            headers=AUTH,
        )
        assert r4.status_code == 200

        # 5. Compaction /v1/responses/compact aggregates timings.
        r5 = client.post(
            "/v1/responses/compact",
            json={
                "model": "test-model",
                "input": [{"role": "user", "content": "private prompt"}],
            },
            headers=AUTH,
        )
        assert r5.status_code == 200

    snap = gateway.snapshot()
    assert "private prompt" not in json.dumps(snap)
    assert KEY not in json.dumps(snap)
    recent = snap["recent_requests"]
    assert len(recent) == 5
    # Newest first: #5 compact, #4 responses stream, #3 chat stream, #2 chat stream, #1 chat non-stream.
    assert [r["seq"] for r in recent] == [5, 4, 3, 2, 1]
    assert [r["kind"] for r in recent] == [
        "compact",
        "responses",
        "chat",
        "chat",
        "chat",
    ]
    assert recent[0]["tokens_per_second"] == 40.0
    assert recent[1]["tokens_per_second"] == 54.2
    assert recent[1]["ttft_ms"] is not None
    assert recent[2]["tokens_per_second"] == 54.2
    assert recent[3]["tokens_per_second"] == 54.2
    assert recent[3]["prompt_tokens"] == 32
    assert recent[3]["completion_tokens"] == 16
    assert recent[3]["prompt_tokens_per_second"] == 920.5
    assert recent[3]["estimated"] is False
    assert recent[4]["tokens_per_second"] == 40.0
    assert snap["last_tokens_per_second"] == 40.0
    assert snap["avg_tokens_per_second"] == round((40.0 + 54.2 + 54.2 + 54.2 + 40.0) / 5, 1)
    # Completion logs include tokens/s for the right-side runtime log drawer.
    assert any("54.2 tokens/s" in entry["message"] for entry in snap["logs"])
    assert any("40.0 tokens/s" in entry["message"] for entry in snap["logs"])


def test_public_api_estimates_speed_without_upstream_usage_and_bounds_history(
    gateway,
):
    gateway.runtime.chat_transport = httpx.MockTransport(
        lambda _: httpx.Response(
            200,
            text=(
                'data: {"choices":[{"delta":{"content":"one "}}]}\n\n'
                'data: {"choices":[{"delta":{"content":"two "}}]}\n\n'
                'data: {"choices":[{"delta":{"content":"three"}}]}\n\n'
                "data: [DONE]\n\n"
            ),
        )
    )
    with TestClient(create_colabone_public_app(gateway)) as client:
        resp = client.post(
            "/v1/chat/completions", json=chat(stream=True), headers=AUTH
        )
        assert resp.status_code == 200
    snap = gateway.snapshot()
    rec = snap["recent_requests"][0]
    assert rec["state"] == "completed"
    assert rec["completion_tokens"] == 3
    assert rec["estimated"] is True
    assert rec["tokens_per_second"] is not None and rec["tokens_per_second"] > 0
    assert any("≈" in entry["message"] and "tokens/s" in entry["message"] for entry in snap["logs"])
    from server.public_api import _compute_token_speed

    # Sub-tick completions (e.g. coarse Windows clocks) must still compute a positive speed estimate.
    assert _compute_token_speed(3, 0.0) == 3000.0
    from server.public_api import _compute_target_input_budget

    # Large 64K/128K windows must reserve generous output headroom (>10K / >20K) for thinking models.
    assert 65536 - _compute_target_input_budget(65536) >= 12000
    assert 131072 - _compute_target_input_budget(131072) >= 20000
    # Explicit small max_tokens can still use tighter reserve.
    assert _compute_target_input_budget(131072, 1024) > _compute_target_input_budget(131072)

    # Registering >50 requests stays bounded to MAX_REQUEST_HISTORY (50).
    for _ in range(60):
        t = ColabOneTask("public_chat", "test-model")
        t.state = "completed"
        gateway.runtime.tasks[t.id] = t
        gateway.register({"id": t.id}, {"kind": "chat"})
    assert len(gateway.snapshot()["recent_requests"]) == 50


def test_public_api_readable_model_id_auto_alias_legacy_compat_and_live_rename(
    gateway, spec
):
    runtime = gateway.runtime
    hf_spec = spec.model_copy(
        update={
            "id": "hf-82b42fca20f78bbf",
            "api_id": "",  # Legacy imported model without stored api_id
            "name": "Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
            "files": [
                spec.files[0].model_copy(
                    update={
                        "repo": "huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
                        "path": "Qwen3.8-Flash-Next-UD-Q4_K_XL.gguf",
                    }
                )
            ],
        }
    )
    runtime.models[hf_spec.id] = hf_spec
    runtime.loaded = hf_spec.id

    captured_upstream = []

    def upstream(request):
        body = json.loads(request.content)
        captured_upstream.append(body)
        if body.get("stream"):
            return httpx.Response(
                200,
                text=(
                    f'data: {{"id":"cmpl-1","model":"{hf_spec.id}","choices":[{{"delta":{{"content":"Hi"}}}}]}}\n\n'
                    "data: [DONE]\n\n"
                ),
            )
        return httpx.Response(
            200,
            json={
                "id": "cmpl-2",
                "object": "chat.completion",
                "model": hf_spec.id,
                "choices": [
                    {
                        "index": 0,
                        "message": {"role": "assistant", "content": "Hello"},
                        "finish_reason": "stop",
                    }
                ],
                "usage": {"prompt_tokens": 5, "completion_tokens": 2, "total_tokens": 7},
            },
        )

    runtime.chat_transport = httpx.MockTransport(upstream)

    with (
        TestClient(create_colabone_public_app(gateway)) as public_client,
        TestClient(
            create_colabone_app(runtime, "private-control-key-at-least-32-long")
        ) as control_client,
    ):
        ctrl_auth = {"Authorization": "Bearer private-control-key-at-least-32-long"}

        # /v1/models exposes clean readable colabone/* ID & HF owner instead of hf-<hash>.
        listed = public_client.get("/v1/models", headers=AUTH).json()
        assert len(listed["data"]) == 1
        assert listed["data"][0]["id"] == "colabone/qwen3.8-flash-next"
        assert listed["data"][0]["owned_by"] == "huihui-ai"

        # /v1/models/{model_id:path} resolves readable ID, colabone/auto, and legacy internal ID.
        for lookup in (
            "colabone/qwen3.8-flash-next",
            "colabone/auto",
            hf_spec.id,
        ):
            r = public_client.get(f"/v1/models/{lookup}", headers=AUTH)
            assert r.status_code == 200
            assert r.json()["id"] == "colabone/qwen3.8-flash-next"
        assert (
            public_client.get("/v1/models/colabone/other", headers=AUTH).status_code
            == 404
        )

        # Non-streaming chat accepts readable ID, forwards internal ID to llama-server,
        # and rewrites response model to readable public ID.
        r_chat = public_client.post(
            "/v1/chat/completions",
            json=chat(model="colabone/qwen3.8-flash-next"),
            headers=AUTH,
        )
        assert r_chat.status_code == 200
        assert r_chat.json()["model"] == "colabone/qwen3.8-flash-next"
        assert captured_upstream[-1]["model"] == hf_spec.id

        # Streaming chat with colabone/auto routes cleanly and rewrites SSE chunk model.
        r_stream = public_client.post(
            "/v1/chat/completions",
            json=chat(model="colabone/auto", stream=True),
            headers=AUTH,
        )
        assert r_stream.status_code == 200
        sse_events = parse_sse_events(r_stream.text)
        assert sse_events[0][1]["model"] == "colabone/qwen3.8-flash-next"
        assert captured_upstream[-1]["model"] == hf_spec.id

        # Responses API accepts colabone/auto and legacy hf-* ID, returning readable ID.
        r_resp = public_client.post(
            "/v1/responses",
            json={"model": "colabone/auto", "input": "ping", "stream": False},
            headers=AUTH,
        )
        assert r_resp.status_code == 200
        assert r_resp.json()["model"] == "colabone/qwen3.8-flash-next"
        assert captured_upstream[-1]["model"] == hf_spec.id

        r_legacy = public_client.post(
            "/v1/chat/completions",
            json=chat(model=hf_spec.id),
            headers=AUTH,
        )
        assert r_legacy.status_code == 200
        assert r_legacy.json()["model"] == "colabone/qwen3.8-flash-next"

        # Live rename via control API takes effect immediately without unloading VRAM.
        r_rename = control_client.post(
            f"/api/v1/models/{hf_spec.id}/api-id",
            json={"api_id": "colabone/my-qwen"},
            headers=ctrl_auth,
        )
        assert r_rename.status_code == 200
        assert r_rename.json()["api_id"] == "colabone/my-qwen"
        assert runtime.loaded == hf_spec.id
        assert (
            public_client.get("/v1/models", headers=AUTH).json()["data"][0]["id"]
            == "colabone/my-qwen"
        )
        assert (
            public_client.post(
                "/v1/chat/completions",
                json=chat(model="colabone/my-qwen"),
                headers=AUTH,
            ).status_code
            == 200
        )
        # Old readable ID no longer matches once renamed (strict), while colabone/auto still routes.
        assert (
            public_client.post(
                "/v1/chat/completions",
                json=chat(model="colabone/qwen3.8-flash-next"),
                headers=AUTH,
            ).status_code
            == 409
        )
        assert (
            public_client.post(
                "/v1/chat/completions",
                json=chat(model="colabone/auto"),
                headers=AUTH,
            ).json()["model"]
            == "colabone/my-qwen"
        )

        # Empty api_id resets back to default readable ID.
        r_reset = control_client.post(
            f"/api/v1/models/{hf_spec.id}/api-id",
            json={"api_id": ""},
            headers=ctrl_auth,
        )
        assert r_reset.status_code == 200
        assert r_reset.json()["api_id"] == "colabone/qwen3.8-flash-next"

    # Gateway logs & history record the readable model ID rather than raw hf-<hash>.
    snap = gateway.snapshot()
    assert any(
        "colabone/qwen3.8-flash-next" in entry["message"] for entry in snap["logs"]
    )
    assert any(
        rec["model"] == "colabone/my-qwen" for rec in snap["recent_requests"]
    )


def test_cjk_token_estimator_and_compaction_self_heal_on_overflow(gateway):
    # CJK text should not be underestimated by 4x like naive len//4.
    zh_sample = "测试上下文压缩与中文分词估算" * 100  # 1400 Chinese chars
    en_sample = "hello world " * 100  # 1200 ASCII chars
    assert estimate_tokens(zh_sample) >= 1500
    assert 300 <= estimate_tokens(en_sample) <= 420
    with_tools = estimate_messages_tokens(
        [{"role": "user", "content": "hi"}],
        tools=[{"type": "function", "function": {"name": "shell", "description": "d" * 800}}],
    )
    without_tools = estimate_messages_tokens([{"role": "user", "content": "hi"}])
    assert with_tools > without_tools + 200

    # /v1/responses/compact self-heals via shrink-and-retry if a chunk still triggers llama-server 400.
    gateway.runtime.active_options = {"context_length": 4096}
    attempts = []

    def upstream(request):
        body = json.loads(request.content)
        attempts.append(body)
        if len(attempts) == 1:
            return httpx.Response(
                400,
                json={
                    "error": {
                        "code": 400,
                        "type": "exceed_context_size_error",
                        "message": "request (4500 tokens) exceeds the available context size (4096 tokens)",
                        "n_prompt_tokens": 4500,
                        "n_ctx": 4096,
                    }
                },
            )
        return httpx.Response(
            200,
            json={
                "choices": [{"message": {"role": "assistant", "content": "已压缩中文对话摘要。"}}],
                "usage": {"prompt_tokens": 1100, "completion_tokens": 24, "total_tokens": 1124},
            },
        )

    gateway.runtime.chat_transport = httpx.MockTransport(upstream)
    with TestClient(create_colabone_public_app(gateway)) as client:
        resp = client.post(
            "/v1/responses/compact",
            json={
                "model": "test-model",
                "instructions": "你是中文编程助手。",
                "input": [
                    {"role": "user", "content": "请帮我重构登录模块。" + ("细节说明" * 400)},
                    {"role": "assistant", "content": "好的，先检查代码。" + ("分析过程" * 400)},
                ],
            },
            headers=AUTH,
        )
        assert resp.status_code == 200, resp.text
        data = resp.json()
        assert data["object"] == "response.compaction"
        comp_item = next(i for i in data["output"] if i["type"] == "compaction")
        assert decode_compaction_content(comp_item["encrypted_content"]) == "已压缩中文对话摘要。"
        # First attempt hit 400 -> gateway shrunk the chunk and succeeded on retry.
        assert len(attempts) >= 2
        assert len(attempts[1]["messages"][1]["content"]) < len(attempts[0]["messages"][1]["content"])


def test_gateway_auto_compaction_three_stages_and_prefix_cache(gateway):
    gateway.runtime.active_options = {"context_length": 4096}
    captured = []

    def upstream(request):
        body = json.loads(request.content)
        captured.append(body)
        is_compact_call = any(
            "Compact the following conversation history" in (m.get("content") or "")
            or "Update the running continuation summary" in (m.get("content") or "")
            for m in body.get("messages") or []
        )
        if is_compact_call:
            return httpx.Response(
                200,
                json={
                    "choices": [
                        {
                            "message": {
                                "role": "assistant",
                                "content": "Summary: investigated auth.py and found token expiry issue.",
                            }
                        }
                    ],
                    "usage": {"prompt_tokens": 900, "completion_tokens": 30, "total_tokens": 930},
                },
            )
        if body.get("stream"):
            sse = (
                'data: {"choices":[{"delta":{"content":"Auto-compacted reply!"}}]}\n\n'
                'data: {"choices":[],"usage":{"prompt_tokens":640,"completion_tokens":12,"total_tokens":652}}\n\n'
                "data: [DONE]\n\n"
            )
            return httpx.Response(200, text=sse)
        return httpx.Response(
            200,
            json={
                "id": "chatcmpl-compacted",
                "choices": [
                    {
                        "message": {"role": "assistant", "content": "Pruned reply!"},
                        "finish_reason": "stop",
                    }
                ],
                "usage": {"prompt_tokens": 520, "completion_tokens": 10, "total_tokens": 530},
            },
        )

    gateway.runtime.chat_transport = httpx.MockTransport(upstream)
    with TestClient(create_colabone_public_app(gateway)) as client:
        # 1. Stage 1 (prune): huge older tool output + JSON tool args pruned with 0 extra GPU calls.
        stage1_msgs = [
            {"role": "system", "content": "Be concise."},
            {"role": "user", "content": "Inspect big log."},
            {
                "role": "assistant",
                "content": None,
                "tool_calls": [
                    {
                        "id": "call_old",
                        "type": "function",
                        "function": {
                            "name": "write_file",
                            "arguments": json.dumps({"path": "a.py", "content": "z" * 6000}),
                        },
                    }
                ],
            },
            {"role": "tool", "tool_call_id": "call_old", "content": "log line\n" * 1800},
            {"role": "assistant", "content": "Checked the log."},
            {"role": "user", "content": "Now summarize the bug."},
        ]
        r1 = client.post(
            "/v1/chat/completions",
            json={"model": "test-model", "messages": stage1_msgs, "stream": False},
            headers=AUTH,
        )
        assert r1.status_code == 200, r1.text
        assert r1.json()["choices"][0]["message"]["content"] == "Pruned reply!"
        # Stage 1 solved it without needing any extra summarization call.
        assert len(captured) == 1
        pruned_sent = captured[0]["messages"]
        # Tool call arguments remain valid JSON and tool_call/tool pairing stays intact.
        pruned_tc_args = pruned_sent[2]["tool_calls"][0]["function"]["arguments"]
        assert isinstance(json.loads(pruned_tc_args), dict)
        assert pruned_sent[3]["role"] == "tool" and pruned_sent[3]["tool_call_id"] == "call_old"
        assert "pruned" in pruned_sent[3]["content"]
        rec1 = gateway.snapshot()["recent_requests"][0]
        assert rec1["compacted"] is True
        assert rec1["compaction_stage"] == "prune"
        assert rec1["original_prompt_tokens"] > rec1["prompt_tokens"]

        # 2. Stage 2 (summary + incremental prefix cache) on streaming /v1/responses:
        #    Wire usage.input_tokens reports real pre-compaction tokens (triggers Codex auto-compact),
        #    while desktop telemetry records actual GPU prompt_tokens + original_prompt_tokens.
        captured.clear()
        shared_prefix = [
            {"role": "user", "content": "Turn 1: " + ("alpha " * 900)},
            {"role": "assistant", "content": "Reply 1: " + ("beta " * 900)},
            {"role": "user", "content": "Turn 2: " + ("gamma " * 900)},
            {"role": "assistant", "content": "Reply 2: " + ("delta " * 900)},
        ]
        r2 = client.post(
            "/v1/responses",
            json={
                "model": "test-model",
                "instructions": "You are Codex.",
                "input": [*shared_prefix, {"role": "user", "content": "What is the root cause?"}],
                "stream": True,
            },
            headers=AUTH,
        )
        assert r2.status_code == 200, r2.text
        events2 = parse_sse_events(r2.text)
        assert events2[-1][0] == "response.completed"
        wire_usage2 = events2[-1][1]["response"]["usage"]
        # Wire usage reports real pre-compaction context size so Codex hits auto_compact_limit.
        assert wire_usage2["input_tokens"] > 4096
        assert wire_usage2["total_tokens"] == wire_usage2["input_tokens"] + wire_usage2["output_tokens"]
        # Desktop telemetry reports actual compacted GPU tokens + original pre-compaction tokens.
        rec2 = gateway.snapshot()["recent_requests"][0]
        assert rec2["compacted"] is True
        assert rec2["compaction_stage"] == "summary"
        assert rec2["prompt_tokens"] == 640
        assert rec2["original_prompt_tokens"] > 4096
        summary_calls_turn1 = sum(
            1
            for req in captured
            if any("Compact the following" in (m.get("content") or "") or "Update the running" in (m.get("content") or "") for m in req["messages"])
        )
        assert summary_calls_turn1 >= 1

        # Turn 2 resends the exact same oversized prefix + a new trailing user message ->
        # exact prefix cache hit means ZERO new summarization GPU calls!
        captured.clear()
        r3 = client.post(
            "/v1/responses",
            json={
                "model": "test-model",
                "instructions": "You are Codex.",
                "input": [*shared_prefix, {"role": "user", "content": "Apply the fix now."}],
                "stream": True,
            },
            headers=AUTH,
        )
        assert r3.status_code == 200, r3.text
        assert len(captured) == 1  # Directly hit prefix summary cache -> single inference call!
        assert any(
            "Compacted earlier conversation summary" in (m.get("content") or "")
            for m in captured[0]["messages"]
        )

        # 3. Stage 3 (extreme single-turn clamp): single 10K-token message fits cleanly into 4K window.
        captured.clear()
        r4 = client.post(
            "/v1/chat/completions",
            json={
                "model": "test-model",
                "messages": [{"role": "user", "content": "giant paste " * 3000}],
                "stream": False,
            },
            headers=AUTH,
        )
        assert r4.status_code == 200, r4.text
        assert estimate_messages_tokens(captured[0]["messages"]) < 4096
        assert gateway.snapshot()["recent_requests"][0]["compaction_stage"] == "truncate"

    # Stopping public gateway clears in-memory compaction cache.
    assert len(gateway.compaction_cache) > 0
    gateway.stop()
    assert len(gateway.compaction_cache) == 0


def test_gateway_reactive_retry_and_openai_context_length_exceeded_errors(gateway):
    gateway.runtime.active_options = {"context_length": 8192}
    calls = []

    def upstream(request):
        body = json.loads(request.content)
        calls.append(body)
        # First call simulates llama-server returning exceed_context_size_error even though
        # local token estimate was below 8192 (e.g., dense special tokens / Jinja overhead).
        if len(calls) == 1:
            return httpx.Response(
                400,
                json={
                    "error": {
                        "code": 400,
                        "type": "exceed_context_size_error",
                        "message": "request (9500 tokens) exceeds the available context size (8192 tokens), try increasing it",
                        "n_prompt_tokens": 9500,
                        "n_ctx": 8192,
                    }
                },
            )
        if body.get("stream"):
            sse = (
                'data: {"choices":[{"delta":{"content":"Recovered!"}}]}\n\n'
                'data: {"choices":[],"usage":{"prompt_tokens":1200,"completion_tokens":5,"total_tokens":1205}}\n\n'
                "data: [DONE]\n\n"
            )
            return httpx.Response(200, text=sse)
        return httpx.Response(
            200,
            json={
                "choices": [{"message": {"role": "assistant", "content": "Recovered!"}}],
                "usage": {"prompt_tokens": 1200, "completion_tokens": 5, "total_tokens": 1205},
            },
        )

    gateway.runtime.chat_transport = httpx.MockTransport(upstream)
    with TestClient(create_colabone_public_app(gateway)) as client:
        # /v1/models exposes active context_length.
        models_resp = client.get("/v1/models", headers=AUTH).json()
        assert models_resp["data"][0]["context_length"] == 8192

        # Reactive retry intercepts llama-server 400 exceed_context_size_error, compacts, and succeeds!
        r_ok = client.post(
            "/v1/responses",
            json={
                "model": "test-model",
                "input": [
                    {"role": "user", "content": "Step 1 " + ("x" * 2000)},
                    {"role": "assistant", "content": "Done 1 " + ("y" * 2000)},
                    {"role": "user", "content": "Step 2"},
                ],
                "stream": True,
            },
            headers=AUTH,
        )
        assert r_ok.status_code == 200, r_ok.text
        evs = parse_sse_events(r_ok.text)
        assert evs[-1][0] == "response.completed"
        assert evs[-1][1]["response"]["usage"]["input_tokens"] == 9500
        assert gateway.snapshot()["recent_requests"][0]["compacted"] is True

        # With X-ColabOne-Auto-Compact: off, return exact OpenAI context_length_exceeded errors
        # (HTTP 400 for non-stream, SSE response.failed code=context_length_exceeded for stream).
        def always_overflow(_request):
            return httpx.Response(
                400,
                json={
                    "error": {
                        "code": 400,
                        "type": "exceed_context_size_error",
                        "message": "request (210528 tokens) exceeds the available context size (8192 tokens)",
                        "n_prompt_tokens": 210528,
                        "n_ctx": 8192,
                    }
                },
            )

        gateway.runtime.chat_transport = httpx.MockTransport(always_overflow)
        opt_out_headers = {**AUTH, "X-ColabOne-Auto-Compact": "off"}

        # Non-streaming /v1/responses -> HTTP 400 (NOT 502) with code=context_length_exceeded.
        r_nonstream = client.post(
            "/v1/responses",
            json={"model": "test-model", "input": "secret oversized prompt", "stream": False},
            headers=opt_out_headers,
        )
        assert r_nonstream.status_code == 400
        err_json = r_nonstream.json()["error"]
        assert err_json["code"] == "context_length_exceeded"
        assert err_json["type"] == "invalid_request_error"
        assert err_json["param"] == "input"
        assert "210528" in err_json["message"] and "8192" in err_json["message"]
        assert "secret oversized prompt" not in r_nonstream.text

        # Non-streaming /v1/chat/completions -> HTTP 400 with param="messages".
        r_chat400 = client.post(
            "/v1/chat/completions",
            json=chat(stream=False),
            headers=opt_out_headers,
        )
        assert r_chat400.status_code == 400
        assert r_chat400.json()["error"]["code"] == "context_length_exceeded"
        assert r_chat400.json()["error"]["param"] == "messages"

        # Streaming /v1/responses -> spec-compliant response.failed with response.error.code=context_length_exceeded.
        r_stream_fail = client.post(
            "/v1/responses",
            json={"model": "test-model", "input": "secret oversized prompt", "stream": True},
            headers=opt_out_headers,
        )
        assert r_stream_fail.status_code == 200
        fail_events = parse_sse_events(r_stream_fail.text)
        assert fail_events[-1][0] == "response.failed"
        failed_err = fail_events[-1][1]["response"]["error"]
        assert failed_err["code"] == "context_length_exceeded"
        assert "210528" in failed_err["message"]
        assert "secret oversized prompt" not in r_stream_fail.text


def test_reactive_retry_calibrates_single_turn_underestimation_and_prunes_tool_args(
    gateway,
):
    from server.openai_compat import (
        _prune_tool_call_arguments,
        estimate_messages_tokens,
        estimate_tokens,
        truncate_messages_to_budget,
    )
    from server.public_api import _format_request_finish_log

    # 1) _prune_tool_call_arguments must keep valid JSON and stay strictly within tiny budgets.
    tricky_json = json.dumps(
        {"patch": ('line with "quotes" and \\backslash\n' * 120)},
        ensure_ascii=False,
    )
    pruned_tight = _prune_tool_call_arguments(tricky_json, 32)
    assert isinstance(json.loads(pruned_tight), dict)
    assert estimate_tokens(pruned_tight) <= 32

    # 2) Stage 3c must shrink oversized parallel tool_call arguments as well as message content.
    parallel_calls = [
        {
            "id": f"call_{i}",
            "type": "function",
            "function": {
                "name": "apply_patch",
                "arguments": json.dumps({"input": ("x" * 2000)}, ensure_ascii=False),
            },
        }
        for i in range(6)
    ]
    atomic_msgs = [
        {"role": "user", "content": "Apply patches"},
        {"role": "assistant", "content": None, "tool_calls": parallel_calls},
        *[
            {"role": "tool", "tool_call_id": f"call_{i}", "content": "ok " + ("y" * 2000)}
            for i in range(6)
        ],
    ]
    clamped, changed = truncate_messages_to_budget(atomic_msgs, None, 900)
    assert changed is True
    assert estimate_messages_tokens(clamped, None) <= 900
    for tc in clamped[1]["tool_calls"]:
        assert isinstance(json.loads(tc["function"]["arguments"]), dict)

    # 3) _format_request_finish_log must never print nonsensical inverted compaction counts.
    log_inverted = _format_request_finish_log(
        {
            "seq": 9,
            "state": "completed",
            "duration_ms": 500,
            "compacted": True,
            "original_prompt_tokens": 5000,
            "prompt_tokens": 5200,
            "completion_tokens": 10,
        }
    )
    assert "自动压缩" not in log_inverted

    # 4) Reactive retry on a single-turn message where estimator underestimates (est ~3500 < 8192*0.72)
    #    must still calibrate downward from llama-server's n_prompt_tokens=9200 and succeed.
    gateway.runtime.active_options = {"context_length": 8192}
    sent_lengths: list[int] = []

    def upstream(request):
        body = json.loads(request.content)
        total_chars = sum(len(str(m.get("content") or "")) for m in body["messages"])
        sent_lengths.append(total_chars)
        if len(sent_lengths) == 1:
            return httpx.Response(
                400,
                json={
                    "error": {
                        "code": 400,
                        "type": "exceed_context_size_error",
                        "message": "request (9200 tokens) exceeds the available context size (8192 tokens)",
                        "n_prompt_tokens": 9200,
                        "n_ctx": 8192,
                    }
                },
            )
        return httpx.Response(
            200,
            json={
                "choices": [{"message": {"role": "assistant", "content": "Calibrated OK"}}],
                "usage": {"prompt_tokens": 3100, "completion_tokens": 6, "total_tokens": 3106},
            },
        )

    gateway.runtime.chat_transport = httpx.MockTransport(upstream)
    with TestClient(create_colabone_public_app(gateway)) as client:
        # ~12,000 ASCII chars -> estimated ~3,500 tokens (< 8192 * 0.72 = 5898), single turn.
        resp = client.post(
            "/v1/responses",
            json={
                "model": "test-model",
                "input": "Single huge turn: " + ("abcdefghij" * 1200),
                "stream": False,
            },
            headers=AUTH,
        )
        assert resp.status_code == 200, resp.text
        data = resp.json()
        assert data["output"][0]["content"][0]["text"] == "Calibrated OK"
        assert data["usage"]["input_tokens"] == 9200
        assert len(sent_lengths) == 2
        assert sent_lengths[1] < sent_lengths[0] * 0.75


def test_compaction_log_ordering_deferred_until_register_and_skips_tiny_older_prefix(
    gateway,
):
    from server.runtime import ColabOneTask

    # 1) If _record_compaction_metric fires before register() (fast worker thread race),
    #    the warning log must be deferred and emitted AFTER the start log with the real #seq.
    metric, acc = gateway._init_slot_metric("responses", "test-model", False)
    slot = {
        "kind": "responses",
        "stream": False,
        "public_model": "test-model",
        "metric": metric,
        "acc": acc,
    }
    gateway._record_compaction_metric(
        slot,
        original_tokens=80000,
        compacted_tokens=12000,
        stage="prune",
        ctx_tokens=65536,
    )
    assert not any("上下文超限" in entry["message"] for entry in gateway.logs)
    task = ColabOneTask("public_responses", "test-model")
    gateway.runtime.tasks[task.id] = task
    gateway.register({"id": task.id}, slot)
    msgs = [entry["message"] for entry in gateway.logs]
    start_idx = next(i for i, m in enumerate(msgs) if m.startswith("API 请求 #1 ·"))
    comp_idx = next(
        i for i, m in enumerate(msgs) if m.startswith("API 请求 #1 上下文超限")
    )
    assert start_idx < comp_idx
    task.state = "completed"

    # 2) Tiny older prefix (<= 320 tokens) + huge newest message must skip Stage 2 LLM summary
    #    and go straight to Stage 3 truncation (exactly 1 upstream call, no summary round-trip).
    gateway.runtime.active_options = {"context_length": 4096}
    seen_calls: list[dict] = []

    def upstream(request):
        body = json.loads(request.content)
        seen_calls.append(body)
        return httpx.Response(
            200,
            json={
                "choices": [{"message": {"role": "assistant", "content": "Direct truncate OK"}}],
                "usage": {"prompt_tokens": 2200, "completion_tokens": 5, "total_tokens": 2205},
            },
        )

    gateway.runtime.chat_transport = httpx.MockTransport(upstream)
    with TestClient(create_colabone_public_app(gateway)) as client:
        resp = client.post(
            "/v1/responses",
            json={
                "model": "test-model",
                "input": [
                    {"role": "user", "content": "hi"},
                    {"role": "assistant", "content": "hello"},
                    {"role": "user", "content": "Big paste: " + ("z" * 20000)},
                ],
                "stream": False,
            },
            headers=AUTH,
        )
        assert resp.status_code == 200, resp.text
        # Must NOT have issued a separate summarization call for "hi" / "hello".
        assert len(seen_calls) == 1
        assert gateway.snapshot()["recent_requests"][0]["compaction_stage"] == "truncate"


def test_clip_text_to_token_budget_strictly_bounds_cjk_emoji_and_ascii():
    from server.openai_compat import clip_text_to_token_budget, estimate_tokens

    samples = [
        "中文上下文压缩测试" * 4,
        "中文上下文压缩测试" * 200,
        "🚀🔥✨🎉" * 100,
        "code_line_value = 12345;\n" * 400,
        "短中文" * 8,
    ]
    markers = [
        "\n[... omitted {omitted} chars ...]\n",
        "\n[... truncated {omitted} chars to fit context window ...]\n",
    ]
    for sample in samples:
        orig_toks = estimate_tokens(sample)
        for budget in (16, 24, 32, 48, 128, 512):
            for marker in markers:
                clipped = clip_text_to_token_budget(
                    sample, budget, marker_template=marker
                )
                clipped_toks = estimate_tokens(clipped)
                assert clipped_toks <= budget, (
                    f"budget={budget} got={clipped_toks} sample={sample[:20]!r}"
                )
                if orig_toks > budget:
                    assert clipped_toks < orig_toks
                    assert clipped != sample
                else:
                    assert clipped == sample.strip()


def test_gateway_auto_adapts_reasoning_tiers_large_max_tokens_and_model_metadata(gateway):
    from server.public_api import _compute_target_input_budget

    # High/xhigh/ultra reasoning tiers reserve extra output headroom on 64K and 128K windows.
    assert 65536 - _compute_target_input_budget(65536, reasoning_effort="xhigh") >= 18000
    assert 131072 - _compute_target_input_budget(131072, reasoning_effort="xhigh") >= 30000
    assert _compute_target_input_budget(131072, reasoning_effort="xhigh") < _compute_target_input_budget(131072)
    # Learned KV-exhaustion extra_reserve must still tighten budget even when explicit max_tokens is set.
    assert _compute_target_input_budget(131072, 8192, extra_reserve=16384) < _compute_target_input_budget(131072, 8192)

    gateway.runtime.active_options = {"context_length": 8192}
    seen_bodies: list[dict] = []

    def upstream(request):
        body = json.loads(request.content)
        seen_bodies.append(body)
        return httpx.Response(
            200,
            json={
                "choices": [{"message": {"role": "assistant", "content": "OK"}, "finish_reason": "stop"}],
                "usage": {"prompt_tokens": 120, "completion_tokens": 5, "total_tokens": 125},
            },
        )

    gateway.runtime.chat_transport = httpx.MockTransport(upstream)
    with TestClient(create_colabone_public_app(gateway)) as client:
        models = client.get("/v1/models", headers=AUTH).json()["data"]
        assert models[0]["context_length"] == 8192
        assert models[0]["context_window"] == 8192
        assert models[0]["max_model_len"] == 8192

        # Client configured for a 256K model with xhigh reasoning & huge max_output_tokens must adapt cleanly.
        resp = client.post(
            "/v1/responses",
            json={
                "model": "test-model",
                "input": "hello",
                "reasoning": {"effort": "xhigh", "summary": "auto"},
                "max_output_tokens": 262144,
                "stream": False,
            },
            headers=AUTH,
        )
        assert resp.status_code == 200, resp.text
        assert seen_bodies[0]["reasoning_effort"] == "high"
        assert seen_bodies[0]["max_tokens"] == 8192 - 256

        # Same for /v1/chat/completions with ultra effort and 400K max_completion_tokens.
        chat_resp = client.post(
            "/v1/chat/completions",
            json={
                "model": "test-model",
                "messages": [{"role": "user", "content": "hi"}],
                "reasoning_effort": "ultra",
                "max_completion_tokens": 400000,
                "stream": False,
            },
            headers=AUTH,
        )
        assert chat_resp.status_code == 200, chat_resp.text
        assert seen_bodies[1]["reasoning_effort"] == "high"
        assert seen_bodies[1]["max_tokens"] == 8192 - 256


def test_gateway_self_calibrates_tokenizer_underestimation_and_proactively_compacts(gateway):
    import asyncio
    from server.openai_compat import estimate_messages_tokens

    gateway.runtime.active_options = {"context_length": 8192}
    seen_prompt_ests: list[int] = []

    def upstream(request):
        body = json.loads(request.content)
        est = estimate_messages_tokens(body.get("messages") or [], body.get("tools"))
        seen_prompt_ests.append(est)
        # Simulate a dense-code/JSON tokenizer producing 1.8x more tokens than the heuristic.
        real_prompt = int(est * 1.8)
        return httpx.Response(
            200,
            json={
                "choices": [{"message": {"role": "assistant", "content": "Calibrated OK"}, "finish_reason": "stop"}],
                "usage": {"prompt_tokens": real_prompt, "completion_tokens": 20, "total_tokens": real_prompt + 20},
            },
        )

    gateway.runtime.chat_transport = httpx.MockTransport(upstream)
    with TestClient(create_colabone_public_app(gateway)) as client:
        # Turn 1: moderate prompt (~1000 est -> 1800 real) teaches the gateway scale ~= 1.8x.
        r1 = client.post(
            "/v1/responses",
            json={
                "model": "test-model",
                "input": [{"role": "user", "content": "code_token_density_sample " * 140}],
                "stream": False,
            },
            headers=AUTH,
        )
        assert r1.status_code == 200, r1.text

        # Turn 2: raw estimate is ~4200 (< 6227 uncalibrated budget), but calibrated real size is ~7560 (> 6227).
        # Gateway must proactively compact BEFORE hitting llama-server.
        long_items = []
        for idx in range(6):
            long_items.append({"role": "user", "content": f"Step {idx}: inspect log"})
            long_items.append(
                {
                    "type": "function_call",
                    "call_id": f"call_{idx}",
                    "name": "read_file",
                    "arguments": json.dumps({"path": f"file_{idx}.py"}),
                }
            )
            long_items.append(
                {
                    "type": "function_call_output",
                    "call_id": f"call_{idx}",
                    "output": f"dense_output_{idx}:" + ("a=1;b=2;" * 280),
                }
            )
            long_items.append({"role": "assistant", "content": f"Reviewed step {idx}."})
        long_items.append({"role": "user", "content": "Summarize and continue."})

        r2 = client.post(
            "/v1/responses",
            json={"model": "test-model", "input": long_items, "stream": False},
            headers=AUTH,
        )
        assert r2.status_code == 200, r2.text
        latest = gateway.snapshot()["recent_requests"][0]
        assert latest["compacted"] is True
        # Compacted raw estimate must have been scaled down by ~1.8x so real tokens fit safely inside 8192.
        assert seen_prompt_ests[-1] * 1.8 <= 6400

    # Also verify cold-start /tokenize probe helper works and fails open on errors.
    async def check_probe():
        ok_transport = httpx.MockTransport(
            lambda req: httpx.Response(200, json={"tokens": list(range(1500))})
            if req.url.path == "/tokenize"
            else httpx.Response(404)
        )
        async with httpx.AsyncClient(transport=ok_transport) as http:
            probed = await gateway._probe_exact_prompt_tokens(
                http, [{"role": "user", "content": "hello world"}], None
            )
            assert probed == 1500 + 20

        # Multi-MB text must be bounded to a <= 400K-char sample and proportionally scaled.
        seen_probe_lens: list[int] = []

        def bounded_handler(req):
            if req.url.path == "/tokenize":
                body = json.loads(req.content)
                seen_probe_lens.append(len(body["content"]))
                return httpx.Response(200, json={"tokens": list(range(100_000))})
            return httpx.Response(404)

        async with httpx.AsyncClient(transport=httpx.MockTransport(bounded_handler)) as http:
            huge_text = "x" * 1_000_000
            probed_huge = await gateway._probe_exact_prompt_tokens(
                http, [{"role": "user", "content": huge_text}], None
            )
            assert seen_probe_lens and seen_probe_lens[0] <= 400_001
            assert probed_huge is not None and 240_000 <= probed_huge <= 260_000

        err_transport = httpx.MockTransport(lambda req: httpx.Response(500))
        async with httpx.AsyncClient(transport=err_transport) as http:
            assert (
                await gateway._probe_exact_prompt_tokens(
                    http, [{"role": "user", "content": "hello world"}], None
                )
                is None
            )

    asyncio.run(check_probe())


def test_gateway_recovers_from_kv_cache_exhaustion_length_stop(gateway):
    gateway.runtime.active_options = {"context_length": 4096}
    calls: list[dict] = []

    def upstream(request):
        body = json.loads(request.content)
        calls.append(body)
        # First non-stream call simulates hitting exact KV-cache limit (3100 + 996 = 4096 -> finish_reason="length").
        if len(calls) == 1:
            return httpx.Response(
                200,
                json={
                    "choices": [
                        {
                            "message": {"role": "assistant", "content": "Partial answer cut off..."},
                            "finish_reason": "length",
                        }
                    ],
                    "usage": {"prompt_tokens": 3100, "completion_tokens": 996, "total_tokens": 4096},
                },
            )
        return httpx.Response(
            200,
            json={
                "choices": [
                    {
                        "message": {"role": "assistant", "content": "Complete answer after auto-compaction."},
                        "finish_reason": "stop",
                    }
                ],
                "usage": {"prompt_tokens": 1200, "completion_tokens": 40, "total_tokens": 1240},
            },
        )

    gateway.runtime.chat_transport = httpx.MockTransport(upstream)
    with TestClient(create_colabone_public_app(gateway)) as client:
        items = []
        for idx in range(5):
            items.append({"role": "user", "content": f"Turn {idx} question"})
            items.append(
                {
                    "type": "function_call",
                    "call_id": f"c_{idx}",
                    "name": "exec",
                    "arguments": "{}",
                }
            )
            items.append(
                {
                    "type": "function_call_output",
                    "call_id": f"c_{idx}",
                    "output": f"log_{idx} " + ("output_line " * 140),
                }
            )
            items.append({"role": "assistant", "content": f"Done {idx}"})
        items.append({"role": "user", "content": "Final task"})

        resp = client.post(
            "/v1/responses",
            json={"model": "test-model", "input": items, "stream": False},
            headers=AUTH,
        )
        assert resp.status_code == 200, resp.text
        data = resp.json()
        assert data["status"] == "completed"
        assert data["incomplete_details"] is None
        assert "Complete answer" in data["output"][0]["content"][0]["text"]
        # Gateway must have retried once with tighter compaction AND boosted output reserve for subsequent turns.
        assert len(calls) >= 2
        cal_key = gateway._calibration_key("test-model", 4096)
        assert gateway._get_output_reserve_boost(cal_key) > 0

        # Meanwhile, an explicit small max_output_tokens hitting finish_reason='length' must still report incomplete
        # and must NOT falsely boost KV-exhaustion reserve.
        prev_boost = gateway._get_output_reserve_boost(cal_key)

        def explicit_cap_upstream(request):
            return httpx.Response(
                200,
                json={
                    "choices": [
                        {
                            "message": {"role": "assistant", "content": "Short"},
                            "finish_reason": "length",
                        }
                    ],
                    "usage": {"prompt_tokens": 80, "completion_tokens": 16, "total_tokens": 96},
                },
            )

        gateway.runtime.chat_transport = httpx.MockTransport(explicit_cap_upstream)
        capped = client.post(
            "/v1/responses",
            json={
                "model": "test-model",
                "input": "Write a poem",
                "max_output_tokens": 16,
                "stream": False,
            },
            headers=AUTH,
        )
        assert capped.status_code == 200
        capped_data = capped.json()
        assert capped_data["status"] == "incomplete"
        assert capped_data["incomplete_details"] == {"reason": "max_output_tokens"}
        assert gateway._get_output_reserve_boost(cal_key) == prev_boost
