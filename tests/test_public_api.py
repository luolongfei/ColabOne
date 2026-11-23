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

from bridge.activity import safe_message
from bridge.controller import ColabOneController
from common.public_api import ColabOnePublicConfig, public_origin, tunnel_token
from conftest import MemoryVault, wait_task
from server.app import create_colabone_app
from server.openai_compat import (
    COLABONE_COMPACTION_PREFIX,
    MAX_BODY_BYTES,
    decode_compaction_content,
    encode_compaction_content,
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
    assert KEY not in safe_message(KEY) and TOKEN not in safe_message(TOKEN)


def test_controller_vault_persists_config_not_secrets_in_snapshot(tmp_path):
    vault = MemoryVault()
    # Legacy vault entry with mode="quick" is normalized to mode="named" in snapshots.
    vault.set("public_api", {"mode": "quick", "api_key": KEY})
    c = ColabOneController(
        Path(__file__).parents[1], vault=vault, config_dir=tmp_path, background=False
    )
    assert c.snapshot()["api_settings"]["mode"] == "named"
    assert c.command("api.tunnel_token", {}) == {"token": ""}

    with pytest.raises(ValueError, match="临时域名"):
        c.command("api.save", {"mode": "quick"})

    c.command(
        "api.save",
        {
            "domain": "api.example.com",
            "tunnel_token": "cloudflared service install " + TOKEN,
        },
    )
    key = c.command("api.key", {})["key"]
    assert c.command("api.tunnel_token", {}) == {"token": TOKEN}
    snap = c.snapshot()
    assert (
        snap["api_settings"]["mode"] == "named"
        and snap["api_settings"]["domain"] == "https://api.example.com"
        and snap["api_settings"]["has_token"]
        and key not in json.dumps(snap)
        and TOKEN not in json.dumps(snap)
    )
    assert not (tmp_path / "settings.json").exists()
    recreated = ColabOneController(c.root, vault=vault, config_dir=tmp_path, background=False)
    assert recreated.command("api.key", {})["key"] == key
    assert recreated.command("api.tunnel_token", {}) == {"token": TOKEN}
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
