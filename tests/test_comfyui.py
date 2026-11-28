"""Comprehensive offline tests for ComfyUI schemas, authenticated canvas proxy, remote manager, and controller."""

from __future__ import annotations

import base64
import hashlib
import io
import json
import socket
import threading
import time
from pathlib import Path
from unittest.mock import Mock

import pytest
from fastapi.testclient import TestClient
from PIL import Image
from pydantic import ValidationError

from common.comfyui import (
    ComfyCanvasConfig,
    ComfyCatalog,
    ComfyModelRef,
    ComfyNodeSpec,
    WorkflowImportRequest,
    WorkflowOutputRecord,
    WorkflowRunRequest,
    WorkflowTemplate,
    apply_workflow_overrides,
    extract_workflow_fields,
    validate_comfy_api_workflow,
)
from common.files import atomic_json
from conftest import MemoryVault, wait_task
from server.app import create_colabone_app
from server.comfy_proxy import COLABONE_COMFY_COOKIE, ColabOneCanvasProxyServer
from server.comfyui import (
    COMFY_MODEL_SUBDIRS,
    ColabOneComfyWorkflowStore,
    write_extra_model_paths,
)

ROOT = Path(__file__).resolve().parents[1]
TOKEN = "comfyui-test-control-token-32chars"
AUTH = {"Authorization": "Bearer " + TOKEN}
TUNNEL_TOKEN = base64.b64encode(
    json.dumps({"a": "account", "t": "comfy-tunnel", "s": "comfy-secret"}).encode()
).decode()


def make_png_bytes(color: str = "blue", size: tuple[int, int] = (32, 32)) -> bytes:
    buf = io.BytesIO()
    Image.new("RGB", size, color).save(buf, format="PNG")
    return buf.getvalue()


def make_png_b64(color: str = "blue") -> str:
    return base64.b64encode(make_png_bytes(color)).decode()


def sample_api_workflow() -> dict:
    return {
        "1": {
            "class_type": "LoadImage",
            "_meta": {"title": "参考图"},
            "inputs": {
                "image": "example.png",
                "upload": "image",
            },
        },
        "2": {
            "class_type": "LoadAudio",
            "_meta": {"title": "驱动音频"},
            "inputs": {
                "audio": "voice.wav",
                "audioUI": "",
            },
        },
        "3": {
            "class_type": "CLIPTextEncode",
            "_meta": {"title": "Positive Prompt"},
            "inputs": {
                "text": "a sunlit coastal studio",
                "clip": ["10", 1],
            },
        },
        "4": {
            "class_type": "CLIPTextEncode",
            "_meta": {"title": "Negative Prompt"},
            "inputs": {
                "text": "blurry, watermark",
                "clip": ["10", 1],
            },
        },
        "5": {
            "class_type": "EmptyLatentImage",
            "_meta": {"title": "尺寸"},
            "inputs": {
                "width": 768,
                "height": 512,
                "batch_size": 1,
            },
        },
        "6": {
            "class_type": "KSampler",
            "_meta": {"title": "采样器"},
            "inputs": {
                "seed": 42,
                "steps": 20,
                "cfg": 6.5,
                "model": ["10", 0],
                "positive": ["3", 0],
                "negative": ["4", 0],
                "latent_image": ["5", 0],
            },
        },
    }


def make_http_resp(body=None, status: int = 200, content: bytes | None = None):
    resp = Mock()
    resp.status_code = status
    resp.ok = 200 <= status < 300
    resp.json.return_value = body if body is not None else {}
    resp.content = content if content is not None else b""
    resp.raise_for_status = Mock()
    if status >= 400:
        import requests

        resp.raise_for_status.side_effect = requests.HTTPError(f"HTTP {status}")
    return resp


def install_gguf(runtime, spec):
    directory = runtime.models_dir / spec.id
    directory.mkdir(parents=True, exist_ok=True)
    (directory / "model.gguf").write_bytes(b"12345678")
    atomic_json(directory / "verified.json", {"model.gguf": spec.files[0].sha256})


# ---------------------------------------------------------------------------
# 1. Schema & Workflow JSON helpers (common.comfyui)
# ---------------------------------------------------------------------------


def test_validate_comfy_api_workflow_accepts_api_and_rejects_canvas_or_malformed():
    wf = sample_api_workflow()
    normalized = validate_comfy_api_workflow(wf)
    assert normalized == wf
    # Normalization deep-copies so mutating result never mutates input.
    normalized["3"]["inputs"]["text"] = "mutated"
    assert wf["3"]["inputs"]["text"] == "a sunlit coastal studio"

    # Canvas format with nodes + links or nodes + version is rejected with helpful Chinese hint.
    for canvas_json in (
        {"nodes": [{"id": 1, "type": "KSampler"}], "links": []},
        {"nodes": [], "version": 0.4},
    ):
        with pytest.raises(ValueError) as exc_info:
            validate_comfy_api_workflow(canvas_json)
        msg = str(exc_info.value)
        assert "画布" in msg and "Save (API Format)" in msg

    for bad in (
        None,
        [],
        "not-a-dict",
        {},
        {"../evil": {"class_type": "KSampler", "inputs": {}}},
        {"1": "not-an-object"},
        {"1": {"inputs": {}}},
        {"1": {"class_type": "   ", "inputs": {}}},
        {"1": {"class_type": "KSampler", "inputs": []}},
        {"1": {"class_type": "KSampler", "inputs": {"": 1}}},
    ):
        with pytest.raises(ValueError):
            validate_comfy_api_workflow(bad)


def test_extract_workflow_fields_skips_links_and_extracts_prompts_numbers_and_media():
    fields = extract_workflow_fields(sample_api_workflow())
    by_key = {f.key: f for f in fields}

    assert by_key["1.image"].field_type == "image"
    assert by_key["2.audio"].field_type == "audio"
    assert by_key["3.text"].field_type == "textarea"
    assert "正向提示词" in by_key["3.text"].label
    assert by_key["4.text"].field_type == "textarea"
    assert "反向提示词" in by_key["4.text"].label
    assert by_key["5.width"].field_type == "integer" and by_key["5.width"].default == 768
    assert by_key["5.height"].field_type == "integer" and by_key["5.height"].default == 512
    assert by_key["6.seed"].field_type == "integer" and by_key["6.seed"].default == 42
    assert by_key["6.steps"].field_type == "integer" and by_key["6.steps"].default == 20
    assert by_key["6.cfg"].field_type == "number" and by_key["6.cfg"].default == 6.5

    # Linked node inputs and internal upload UI helper fields must never be extracted.
    for skipped in ("1.upload", "2.audioUI", "3.clip", "6.model", "6.positive", "6.latent_image"):
        assert skipped not in by_key


def test_apply_workflow_overrides_does_not_mutate_original_and_protects_links():
    original = sample_api_workflow()
    snapshot_before = json.dumps(original, sort_keys=True)

    updated = apply_workflow_overrides(
        original,
        overrides={
            "3.text": "cyberpunk harbor at dusk",
            "6.seed": 999,
            "6.steps": 30,
            "6.cfg": 7.5,
            "6.positive": "should-not-overwrite-link",
            "999.missing": "ignored",
        },
        media_map={
            "1.image": "uploaded_ref.png",
            "2.audio": "uploaded_voice.wav",
            "3.clip": "must-not-overwrite-link.png",
        },
    )

    assert json.dumps(original, sort_keys=True) == snapshot_before
    assert updated["3"]["inputs"]["text"] == "cyberpunk harbor at dusk"
    assert updated["6"]["inputs"]["seed"] == 999
    assert updated["6"]["inputs"]["steps"] == 30
    assert updated["6"]["inputs"]["cfg"] == 7.5
    assert updated["6"]["inputs"]["positive"] == ["3", 0]
    assert updated["1"]["inputs"]["image"] == "uploaded_ref.png"
    assert updated["2"]["inputs"]["audio"] == "uploaded_voice.wav"
    assert updated["3"]["inputs"]["clip"] == ["10", 1]

    with pytest.raises(ValueError):
        apply_workflow_overrides(original, overrides={"6.cfg": float("nan")})


def test_comfy_models_and_catalog_validation():
    catalog_data = json.loads((ROOT / "catalog/comfyui.json").read_text(encoding="utf-8"))
    catalog = ComfyCatalog.model_validate(catalog_data)
    assert catalog.port == 8188
    assert catalog.proxy_port == 8189
    assert catalog.port != catalog.proxy_port

    with pytest.raises(ValidationError):
        ComfyCatalog.model_validate(catalog_data | {"proxy_port": catalog.port})

    tmpl = WorkflowTemplate(
        id="wf_demo-1",
        name="Demo Studio Flow",
        description="Text + Image + Audio",
        source="builtin",
        workflow=sample_api_workflow(),
        pip_packages=["soundfile>=0.12", "einops"],
        custom_nodes=[
            ComfyNodeSpec(
                name="ComfyUI-TestNode",
                repo="https://github.com/example/ComfyUI-TestNode.git",
                commit="a" * 40,
            )
        ],
        models=[
            ComfyModelRef(
                repo="org/comfy-weights",
                revision="b" * 40,
                path="flux/model.safetensors",
                target_dir="diffusion_models",
                size=1024,
                sha256="c" * 64,
            )
        ],
    )
    assert tmpl.node_count == len(sample_api_workflow())
    assert len(tmpl.fields) >= 8

    with pytest.raises(ValidationError):
        WorkflowTemplate(
            id="bad_pip",
            name="Bad pip",
            source="builtin",
            workflow=sample_api_workflow(),
            pip_packages=["--extra-index-url https://evil.invalid"],
        )

    for forbidden_kwargs in (
        {
            "custom_nodes": [
                ComfyNodeSpec(
                    name="ComfyUI-TestNode",
                    repo="https://github.com/example/ComfyUI-TestNode.git",
                    commit="a" * 40,
                )
            ]
        },
        {"pip_packages": ["einops"]},
        {
            "models": [
                ComfyModelRef(
                    repo="org/comfy-weights",
                    revision="b" * 40,
                    path="flux/model.safetensors",
                    target_dir="diffusion_models",
                    size=1024,
                    sha256="c" * 64,
                )
            ]
        },
        {"pipeline": {"kind": "digital_human"}},
    ):
        with pytest.raises(
            ValidationError,
            match="自定义工作流模板不允许声明远端依赖包、模型下载或流水线配置",
        ):
            WorkflowTemplate(
                id="custom_forbidden",
                name="Custom Forbidden",
                source="custom",
                workflow=sample_api_workflow(),
                **forbidden_kwargs,
            )

    req = WorkflowImportRequest(name="  My Flow  ", workflow=sample_api_workflow())
    assert req.name == "My Flow"
    with pytest.raises(ValidationError):
        WorkflowImportRequest(name="   ", workflow=sample_api_workflow())

    run_req = WorkflowRunRequest(
        workflow_id="wf_demo-1",
        overrides={"3.text": "hello", "6.seed": 7},
        inputs={"1.image": "a" * 32},
    )
    assert run_req.inputs["1.image"] == "a" * 32
    for bad_run in (
        {"workflow_id": "../evil"},
        {"workflow_id": "wf_demo-1", "overrides": {"invalid_key_no_dot": 1}},
        {"workflow_id": "wf_demo-1", "inputs": {"1.image": "../secret"}},
    ):
        with pytest.raises(ValidationError):
            WorkflowRunRequest.model_validate(bad_run)

    rec = WorkflowOutputRecord(
        id="d" * 32,
        workflow_id="wf_demo-1",
        workflow_name="Demo",
        kind="video",
        filename="clip_0001.mp4",
        extension="mp4",
        mime="video/mp4",
        size=128,
        sha256="e" * 64,
        created_at=1.0,
    )
    assert rec.kind == "video"
    with pytest.raises(ValidationError):
        WorkflowOutputRecord.model_validate(rec.model_dump() | {"mime": "image/png"})
    with pytest.raises(ValidationError):
        WorkflowOutputRecord.model_validate(rec.model_dump() | {"filename": "../clip.mp4"})

    with pytest.raises(ValidationError):
        ComfyCanvasConfig(mode="quick", domain="ignored.example.com", tunnel_token="ignored")
    with pytest.raises(ValidationError):
        ComfyCanvasConfig()
    with pytest.raises(ValidationError):
        ComfyCanvasConfig(domain="demo.trycloudflare.com", tunnel_token=TUNNEL_TOKEN)
    named = ComfyCanvasConfig(
        mode="named",
        domain="comfy.example.com/",
        tunnel_token="sudo cloudflared service install " + TUNNEL_TOKEN,
    )
    assert named.mode == "named"
    assert named.domain == "https://comfy.example.com"
    assert named.tunnel_token == TUNNEL_TOKEN
    with pytest.raises(ValidationError):
        ComfyCanvasConfig(mode="named", domain="http://localhost", tunnel_token=TUNNEL_TOKEN)


# ---------------------------------------------------------------------------
# 2. Authenticated Canvas Reverse Proxy (server.comfy_proxy.CanvasProxyServer)
# ---------------------------------------------------------------------------


class _MockComfyUpstream:
    """Real localhost TCP HTTP/1.1 + WebSocket upstream for offline proxy tests."""

    def __init__(self):
        self.sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.sock.bind(("127.0.0.1", 0))
        self.port = self.sock.getsockname()[1]
        self.sock.listen(16)
        self.sock.settimeout(0.2)
        self.stop_event = threading.Event()
        self.requests: list[dict] = []
        self.lock = threading.Lock()
        self.thread = threading.Thread(target=self._serve, daemon=True)
        self.thread.start()

    def _serve(self):
        while not self.stop_event.is_set():
            try:
                conn, _ = self.sock.accept()
            except socket.timeout:
                continue
            except OSError:
                break
            threading.Thread(target=self._handle, args=(conn,), daemon=True).start()

    def _handle(self, conn: socket.socket):
        with conn:
            conn.settimeout(3.0)
            buf = bytearray()
            while b"\r\n\r\n" not in buf:
                chunk = conn.recv(4096)
                if not chunk:
                    return
                buf.extend(chunk)
            sep = buf.index(b"\r\n\r\n")
            head = bytes(buf[:sep]).decode("latin-1")
            leftover = bytes(buf[sep + 4 :])
            lines = head.split("\r\n")
            req_line = lines[0]
            headers: dict[str, str] = {}
            for line in lines[1:]:
                k, _, v = line.partition(":")
                headers[k.strip().lower()] = v.strip()
            with self.lock:
                self.requests.append({"line": req_line, "headers": headers})

            if headers.get("upgrade", "").lower() == "websocket":
                conn.sendall(
                    b"HTTP/1.1 101 Switching Protocols\r\n"
                    b"Upgrade: websocket\r\n"
                    b"Connection: Upgrade\r\n"
                    b"\r\n"
                )
                data = leftover
                if not data:
                    data = conn.recv(4096)
                if data:
                    conn.sendall(b"echo:" + data)
                return

            body = json.dumps(
                {
                    "system": {"comfyui_version": "0.46.0"},
                    "seen_cookie": headers.get("cookie", ""),
                    "seen_auth": headers.get("authorization", ""),
                }
            ).encode("utf-8")
            conn.sendall(
                (
                    "HTTP/1.1 200 OK\r\n"
                    "Content-Type: application/json\r\n"
                    f"Content-Length: {len(body)}\r\n"
                    "Connection: close\r\n\r\n"
                ).encode("latin-1")
                + body
            )

    def close(self):
        self.stop_event.set()
        try:
            self.sock.close()
        except OSError:
            pass
        self.thread.join(timeout=3.0)


def _raw_http_exchange(port: int, request_bytes: bytes) -> tuple[int, dict[str, str], bytes]:
    with socket.create_connection(("127.0.0.1", port), timeout=5.0) as s:
        s.sendall(request_bytes)
        s.shutdown(socket.SHUT_WR)
        raw = bytearray()
        while True:
            chunk = s.recv(4096)
            if not chunk:
                break
            raw.extend(chunk)
    head_b, _, body = bytes(raw).partition(b"\r\n\r\n")
    lines = head_b.decode("latin-1").split("\r\n")
    status = int(lines[0].split(" ")[1])
    headers: dict[str, str] = {}
    for line in lines[1:]:
        k, _, v = line.partition(":")
        headers[k.strip().lower()] = v.strip()
    return status, headers, body


def test_canvas_proxy_auth_cookie_stripping_activity_and_websocket_tunnel():
    upstream = _MockComfyUpstream()
    activities: list[float] = []
    proxy = ColabOneCanvasProxyServer(
        upstream_host="127.0.0.1",
        upstream_port=upstream.port,
        listen_host="127.0.0.1",
        listen_port=0,
        instance="inst-test-42",
        canvas_token="secret-canvas-token-xyz",
        session_cookie="secret-session-cookie-abc",
        on_activity=lambda: activities.append(time.monotonic()),
    )
    proxy.start()
    try:
        # 1. Health endpoint requires no auth and returns exact instance ID.
        status, _, body = _raw_http_exchange(
            proxy.port,
            b"GET /_colabone_health HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n",
        )
        assert status == 200
        assert json.loads(body) == {
            "ok": True,
            "instance": "inst-test-42",
            "service": "comfyui-canvas",
        }

        # 2. Anonymous HTTP and WebSocket requests return 401 and never reach upstream.
        for req in (
            b"GET / HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n",
            b"GET / HTTP/1.1\r\nHost: 127.0.0.1\r\nAccept: text/html\r\n\r\n",
            b"GET /ws HTTP/1.1\r\nHost: 127.0.0.1\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n",
            b"GET /_colabone_auth?token=wrong HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n",
            b"GET /_colabone_auth HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n",
        ):
            st, _, _ = _raw_http_exchange(proxy.port, req)
            assert st == 401
        assert upstream.requests == []
        assert activities == []

        # 3. Valid /_colabone_auth redirects to / with HttpOnly session cookie.
        st, hdrs, _ = _raw_http_exchange(
            proxy.port,
            b"GET /_colabone_auth?token=secret-canvas-token-xyz HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n",
        )
        assert st == 302
        assert hdrs["location"] == "/"
        set_cookie = hdrs["set-cookie"]
        assert f"{COLABONE_COMFY_COOKIE}=secret-session-cookie-abc" in set_cookie
        assert "HttpOnly" in set_cookie
        assert len(activities) == 1

        # Reset throttle so we verify forwarded request also triggers on_activity.
        proxy._last_touch = None

        # 4. Authorized GET with cookie forwards to upstream and strips colabone_comfy cookie.
        cookie_pair = set_cookie.split(";", 1)[0]
        st, _, body = _raw_http_exchange(
            proxy.port,
            (
                "GET /system_stats HTTP/1.1\r\n"
                "Host: 127.0.0.1\r\n"
                f"Cookie: {cookie_pair}; user_pref=dark\r\n\r\n"
            ).encode("latin-1"),
        )
        assert st == 200
        payload = json.loads(body)
        assert payload["system"]["comfyui_version"] == "0.46.0"
        assert COLABONE_COMFY_COOKIE not in payload["seen_cookie"]
        assert "user_pref=dark" in payload["seen_cookie"]
        assert len(activities) == 2

        # 5. Authorized GET with Bearer token also forwards and strips Authorization header.
        proxy._last_touch = None
        st, _, body = _raw_http_exchange(
            proxy.port,
            (
                "GET /system_stats HTTP/1.1\r\n"
                "Host: 127.0.0.1\r\n"
                "Authorization: Bearer secret-canvas-token-xyz\r\n\r\n"
            ).encode("latin-1"),
        )
        assert st == 200
        payload = json.loads(body)
        assert payload["seen_auth"] == ""
        assert len(activities) == 3

        # 6. Authorized WebSocket upgrade tunnels bidirectional bytes cleanly.
        with socket.create_connection(("127.0.0.1", proxy.port), timeout=5.0) as ws:
            ws.sendall(
                (
                    "GET /ws?clientId=test HTTP/1.1\r\n"
                    "Host: 127.0.0.1\r\n"
                    "Upgrade: websocket\r\n"
                    "Connection: Upgrade\r\n"
                    "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"
                    "Sec-WebSocket-Version: 13\r\n"
                    f"Cookie: {cookie_pair}\r\n\r\n"
                ).encode("latin-1")
            )
            resp_buf = bytearray()
            while b"\r\n\r\n" not in resp_buf:
                chunk = ws.recv(4096)
                assert chunk
                resp_buf.extend(chunk)
            assert resp_buf.startswith(b"HTTP/1.1 101")
            ws.sendall(b"ping-comfy-ws")
            echoed = ws.recv(4096)
            assert echoed == b"echo:ping-comfy-ws"
            assert COLABONE_COMFY_COOKIE not in upstream.requests[-1]["headers"].get("cookie", "")
    finally:
        proxy.stop()
        upstream.close()


# ---------------------------------------------------------------------------
# 3. Remote ComfyUI Store, Manager & FastAPI routes (server.comfyui, server.app)
# ---------------------------------------------------------------------------


def test_comfy_workflow_store_workflows_inputs_outputs_and_trash(tmp_path):
    builtin = WorkflowTemplate(
        id="builtin-sample",
        name="Builtin Sample",
        source="builtin",
        workflow=sample_api_workflow(),
    )
    store = ColabOneComfyWorkflowStore(tmp_path, builtin_templates=[builtin])

    # Builtin cannot be overwritten or deleted.
    with pytest.raises(ValueError):
        store.save_workflow(
            {"id": "builtin-sample", "name": "Hijack", "workflow": sample_api_workflow()}
        )
    with pytest.raises(ValueError):
        store.delete_workflow("builtin-sample")

    # Save / list / reload / delete custom workflow.
    saved = store.save_workflow(
        {
            "id": "wf-custom-1",
            "name": "我的工作流",
            "description": "测试",
            "workflow": sample_api_workflow(),
        }
    )
    assert saved["source"] == "custom"
    assert {w["id"] for w in store.list_workflows()} == {"builtin-sample", "wf-custom-1"}
    reloaded = ColabOneComfyWorkflowStore(tmp_path, builtin_templates=[builtin])
    assert reloaded.get_workflow("wf-custom-1")["name"] == "我的工作流"

    store.delete_workflow("wf-custom-1")
    assert [w["id"] for w in store.list_workflows()] == ["builtin-sample"]
    assert (tmp_path / "workflows/.trash/wf-custom-1.json").is_file()

    # Save PNG image input & WAV audio input.
    img_in = store.save_input("photo.png", "data:image/png;base64," + make_png_b64())
    fname, fpath = store.input_file(img_in["id"])
    assert fname.endswith(".png") and fpath.read_bytes().startswith(b"\x89PNG")

    wav_raw = b"RIFF\x24\x00\x00\x00WAVEfmt " + b"\x00" * 16
    aud_in = store.save_input("voice.wav", base64.b64encode(wav_raw).decode())
    afname, afpath = store.input_file(aud_in["id"])
    assert afname.endswith(".wav") and afpath.read_bytes() == wav_raw

    with pytest.raises(ValueError):
        store.save_input("bad.exe", base64.b64encode(b"MZ").decode())
    bmp = io.BytesIO()
    Image.new("RGB", (4, 4)).save(bmp, format="BMP")
    with pytest.raises(ValueError):
        store.save_input("bad.png", base64.b64encode(bmp.getvalue()).decode())

    # Save PNG, MP4, WAV outputs with SHA-256, chunked read, and soft-delete to .trash.
    png_raw = make_png_bytes("red")
    png_rec = store.save_output(png_raw, "wf-custom-1", "我的工作流", "out_001.png", 1.5, "p1")
    mp4_raw = b"\x00\x00\x00\x18ftypmp42test-mp4-data"
    mp4_rec = store.save_output(mp4_raw, "wf-custom-1", "我的工作流", "vid_001.mp4", 4.2, "p2")
    wav_rec = store.save_output(wav_raw, "wf-custom-1", "我的工作流", "aud_001.wav", 0.8, "p3")

    assert png_rec["kind"] == "image" and png_rec["mime"] == "image/png"
    assert mp4_rec["kind"] == "video" and mp4_rec["mime"] == "video/mp4"
    assert wav_rec["kind"] == "audio" and wav_rec["mime"] == "audio/wav"

    part1 = store.asset(png_rec["id"], offset=0, length=20)
    part2 = store.asset(png_rec["id"], offset=20, length=512 * 1024)
    reconstructed = base64.b64decode(part1["data"]) + base64.b64decode(part2["data"])
    assert reconstructed == png_raw
    assert hashlib.sha256(reconstructed).hexdigest() == png_rec["sha256"]

    store.delete_output(mp4_rec["id"])
    assert mp4_rec["id"] not in {o["id"] for o in store.list_outputs()}
    assert (tmp_path / "comfyui-outputs/.trash" / f"{mp4_rec['id']}.mp4").is_file()
    assert (tmp_path / "comfyui-outputs/.trash" / f"{mp4_rec['id']}.json").is_file()


def test_write_extra_model_paths_points_to_comfy_and_shared_models(tmp_path):
    comfy_models = tmp_path / "comfyui-models"
    shared_models = tmp_path / "models"
    yaml_path = write_extra_model_paths(
        tmp_path / "extra_model_paths.yaml", comfy_models, shared_models
    )
    text = yaml_path.read_text(encoding="utf-8")
    assert "colabone_comfy:" in text
    assert str(comfy_models) in text
    assert "colabone_shared:" in text
    assert str(shared_models) in text
    for subdir in COMFY_MODEL_SUBDIRS:
        assert (comfy_models / subdir).is_dir()
        assert f"  {subdir}: {subdir}" in text


def test_comfy_and_llama_mutual_exclusion_and_queue_idle_protection(
    runtime, spec, monkeypatch
):
    install_gguf(runtime, spec)
    monkeypatch.setattr(
        runtime, "gpu_info", lambda: {"name": "NVIDIA T4", "total_mib": 15360, "used_mib": 500}
    )

    # 1. Start with a loaded llama-server; starting ComfyUI must stop llama-server first.
    llama_proc = Mock()
    llama_proc.poll.return_value = None
    runtime.proc = llama_proc
    runtime.loaded = spec.id

    monkeypatch.setattr(runtime.comfy, "prepare", lambda _t: runtime.comfy.install_dir)
    comfy_proc = Mock()
    comfy_proc.poll.return_value = None
    comfy_proc.pid = None
    comfy_proc.terminate.side_effect = lambda: setattr(comfy_proc.poll, "return_value", 0)
    monkeypatch.setattr("server.comfyui.subprocess.Popen", lambda *a, **k: comfy_proc)

    queue_state = {"queue_running": [], "queue_pending": []}

    def fake_get(url, **kwargs):
        if url.endswith("/system_stats"):
            return make_http_resp({"system": {"comfyui_version": "0.46.0"}})
        if url.endswith("/queue"):
            return make_http_resp(queue_state)
        if url.endswith("/health"):
            return make_http_resp({"status": "ok"})
        raise AssertionError(f"Unexpected GET {url}")

    runtime.http = Mock()
    runtime.http.get.side_effect = fake_get

    task = wait_task(runtime, runtime.start_comfyui())
    assert task.state == "completed"
    llama_proc.terminate.assert_called_once()
    assert runtime.loaded is None and runtime.proc is None
    assert runtime.comfy.is_running() is True
    assert runtime.status()["active_engine"] == "comfyui"

    # 2. Active ComfyUI queue keeps runtime busy and pauses idle auto-release countdown.
    runtime.last_active = 0
    queue_state["queue_running"] = [[0, "prompt-running-1"]]
    assert runtime.busy() is True
    assert runtime.idle_status(now=5000)["phase"] == "active"
    assert runtime.claim_auto_release(now=5000) is False

    # When queue empties, runtime is no longer busy.
    queue_state["queue_running"] = []
    assert runtime.busy() is False

    # 3. Loading a GGUF LLM stops ComfyUI first.
    monkeypatch.setattr(runtime, "prepare_engine", lambda _t: runtime.root / "llama-server")
    new_llama = Mock()
    new_llama.poll.return_value = None
    monkeypatch.setattr("server.runtime.subprocess.Popen", lambda *a, **k: new_llama)

    load_task = wait_task(runtime, runtime.load(spec.id))
    assert load_task.state == "completed"
    comfy_proc.terminate.assert_called_once()
    assert runtime.comfy.is_running() is False
    assert runtime.loaded == spec.id
    assert runtime.status()["active_engine"] == "llama"


def test_comfy_run_route_uploads_inputs_saves_output_and_snapshot_hides_canvas_token(
    runtime, monkeypatch
):
    monkeypatch.setattr(
        runtime, "gpu_info", lambda: {"name": "NVIDIA T4", "total_mib": 15360, "used_mib": 500}
    )
    monkeypatch.setattr("server.comfyui.time.sleep", lambda _: None)

    # Mark ComfyUI server as already running with a mock process.
    comfy_proc = Mock()
    comfy_proc.poll.return_value = None
    comfy_proc.pid = None
    runtime.comfy.proc = comfy_proc
    runtime.comfy.state = "ready"

    generated_png = make_png_bytes("green")
    captured_posts: list[tuple[str, dict]] = []
    history_calls = {"count": 0}

    def fake_post(url, **kwargs):
        captured_posts.append((url, kwargs))
        if url.endswith("/upload/image"):
            return make_http_resp({"name": "comfy_uploaded.png", "subfolder": ""})
        if url.endswith("/prompt"):
            return make_http_resp({"prompt_id": "prompt-abc-123"})
        if url.endswith("/free"):
            return make_http_resp({})
        raise AssertionError(f"Unexpected POST {url}")

    def fake_get(url, **kwargs):
        if url.endswith("/queue"):
            return make_http_resp({"queue_running": [], "queue_pending": []})
        if url.endswith("/history/prompt-abc-123"):
            history_calls["count"] += 1
            if history_calls["count"] == 1:
                return make_http_resp({})
            return make_http_resp(
                {
                    "prompt-abc-123": {
                        "status": {"status_str": "success"},
                        "outputs": {
                            "9": {
                                "images": [
                                    {
                                        "filename": "ComfyUI_00001_.png",
                                        "subfolder": "",
                                        "type": "output",
                                    }
                                ]
                            }
                        },
                    }
                }
            )
        if url.endswith("/view"):
            return make_http_resp(content=generated_png)
        raise AssertionError(f"Unexpected GET {url}")

    runtime.http = Mock()
    runtime.http.post.side_effect = fake_post
    runtime.http.get.side_effect = fake_get

    with TestClient(create_colabone_app(runtime, TOKEN)) as client:
        # Import workflow via HTTP control API.
        wf_resp = client.post(
            "/api/v1/comfyui/workflows",
            headers=AUTH,
            json={
                "id": "wf-e2e",
                "name": "端到端测试流",
                "workflow": sample_api_workflow(),
            },
        )
        assert wf_resp.status_code == 200, wf_resp.text

        # Upload input image via HTTP control API.
        in_resp = client.post(
            "/api/v1/comfyui/inputs",
            headers=AUTH,
            json={"filename": "ref.png", "data": make_png_b64("yellow")},
        )
        assert in_resp.status_code == 200, in_resp.text
        input_id = in_resp.json()["id"]

        # Execute workflow via POST /api/v1/comfyui/run.
        run_resp = client.post(
            "/api/v1/comfyui/run",
            headers=AUTH,
            json={
                "workflow_id": "wf-e2e",
                "overrides": {"3.text": "golden hour mountains", "6.seed": 777},
                "inputs": {"1.image": input_id},
            },
        )
        assert run_resp.status_code == 200, run_resp.text
        task = wait_task(runtime, run_resp.json())
        assert task.state == "completed", task.error

        # Verify uploaded image and parameter overrides were sent to ComfyUI /prompt.
        prompt_call = next(kw for url, kw in captured_posts if url.endswith("/prompt"))
        submitted_prompt = prompt_call["json"]["prompt"]
        assert submitted_prompt["1"]["inputs"]["image"] == "comfy_uploaded.png"
        assert submitted_prompt["3"]["inputs"]["text"] == "golden hour mountains"
        assert submitted_prompt["6"]["inputs"]["seed"] == 777

        # Verify snapshot exposes workflows, comfy_outputs, protocol 7, comfyui, and NEVER canvas_token.
        snap_resp = client.get("/api/v1/snapshot", headers=AUTH)
        assert snap_resp.status_code == 200
        snap = snap_resp.json()
        assert snap["runtime"]["protocol"] == 7
        assert snap["runtime"]["capabilities"] == [
            "public_api",
            "comfyui",
            "comfyui_hf_token",
            "comfyui_drive_export",
            "comfyui_tts_preview",
            "comfyui_longcat_native",
            "comfyui_workflow_auto_release",
            "comfyui_live_studio",
            "comfyui_fashion_tryon",
            "comfyui_fashion_pose_studio",
            "comfyui_share_guard",
            "idle_release_modes",
        ]
        assert snap["runtime"]["comfyui"]["running"] is True
        assert any(w["id"] == "wf-e2e" for w in snap["workflows"])
        assert len(snap["comfy_outputs"]) == 1
        out = snap["comfy_outputs"][0]
        assert out["sha256"] == hashlib.sha256(generated_png).hexdigest()
        assert out["task_id"] == task.id

        snap_dump = json.dumps(snap)
        assert runtime.comfy.canvas.canvas_token not in snap_dump
        assert runtime.comfy.canvas.session_cookie not in snap_dump

        # Verify chunked output download and range validation.
        asset_path = f"/api/v1/comfyui/outputs/{out['id']}/asset"
        assert client.get(asset_path).status_code == 401
        assert client.get(asset_path + "?offset=-1", headers=AUTH).status_code == 422
        assert client.get(asset_path + "?length=9999999", headers=AUTH).status_code == 422
        c1 = client.get(asset_path + "?offset=0&length=30", headers=AUTH).json()
        c2 = client.get(asset_path + "?offset=30", headers=AUTH).json()
        downloaded = base64.b64decode(c1["data"]) + base64.b64decode(c2["data"])
        assert downloaded == generated_png

        # Verify /api/v1/comfyui/free posts to ComfyUI /free.
        free_resp = client.post("/api/v1/comfyui/free", headers=AUTH)
        assert free_resp.status_code == 200
        free_call = next(kw for url, kw in captured_posts if url.endswith("/free"))
        assert free_call["json"] == {"unload_models": True, "free_memory": True}


def test_canvas_proxy_internal_probe_skips_activity_and_rejected_ws_does_not_hang():
    import requests
    import socket
    from http.server import BaseHTTPRequestHandler, HTTPServer
    from server.comfy_proxy import COLABONE_INTERNAL_PROBE_HEADER

    class RejectWsHandler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, *_args):
            pass

        def do_GET(self):
            body = b"ws forbidden"
            self.send_response(403)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Connection", "keep-alive")
            self.end_headers()
            self.wfile.write(body)

    upstream = HTTPServer(("127.0.0.1", 0), RejectWsHandler)
    up_thread = threading.Thread(target=upstream.serve_forever, daemon=True)
    up_thread.start()

    touches = []
    proxy = ColabOneCanvasProxyServer(
        upstream_host="127.0.0.1",
        upstream_port=upstream.server_port,
        listen_host="127.0.0.1",
        listen_port=0,
        instance="inst-probe-test",
        canvas_token="tok-probe-test",
        session_cookie="sess-probe-test",
        on_activity=lambda: touches.append(1),
    )
    proxy.start()
    try:
        base = f"http://127.0.0.1:{proxy.port}"
        with requests.Session() as http:
            http.trust_env = False
            # Internal gateway health probe must NOT reset idle timer.
            r1 = http.get(
                f"{base}/_colabone_auth?token=tok-probe-test",
                headers={COLABONE_INTERNAL_PROBE_HEADER: "inst-probe-test"},
                allow_redirects=False,
                timeout=3,
            )
            assert r1.status_code == 302
            assert touches == []

            # Real user browser handshake DOES reset idle timer.
            r2 = http.get(
                f"{base}/_colabone_auth?token=tok-probe-test",
                allow_redirects=False,
                timeout=3,
            )
            assert r2.status_code == 302
            assert len(touches) == 1

        # Rejected WebSocket upgrade (403 with upstream keep-alive) returns promptly without hanging.
        with socket.create_connection(("127.0.0.1", proxy.port), timeout=3) as sock:
            sock.settimeout(3)
            req = (
                "GET /ws HTTP/1.1\r\n"
                f"Host: 127.0.0.1:{proxy.port}\r\n"
                "Connection: Upgrade\r\n"
                "Upgrade: websocket\r\n"
                "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"
                "Sec-WebSocket-Version: 13\r\n"
                f"Cookie: {COLABONE_COMFY_COOKIE}=sess-probe-test\r\n\r\n"
            ).encode("latin-1")
            sock.sendall(req)
            chunks = []
            while True:
                data = sock.recv(4096)
                if not data:
                    break
                chunks.append(data)
            resp_text = b"".join(chunks).decode("latin-1", errors="replace")
            assert "403" in resp_text and "ws forbidden" in resp_text
    finally:
        proxy.stop()
        upstream.shutdown()
        upstream.server_close()


def test_format_colabone_duration_zh_and_task_elapsed_fields(runtime):
    from common.comfyui import format_colabone_duration_zh

    assert format_colabone_duration_zh(0) == "0 秒"
    assert format_colabone_duration_zh(0, subsecond=True) == "0.0 秒"
    assert format_colabone_duration_zh(4.24, subsecond=True) == "4.2 秒"
    assert format_colabone_duration_zh(59.96, subsecond=True) == "1 分钟"
    assert format_colabone_duration_zh(60) == "1 分钟"
    assert format_colabone_duration_zh(754) == "12 分 34 秒"
    assert format_colabone_duration_zh(3600) == "1 小时"
    assert format_colabone_duration_zh(4500) == "1 小时 15 分"
    assert format_colabone_duration_zh(4523.4, subsecond=True) == "1 小时 15 分 23 秒"
    assert format_colabone_duration_zh(-5) == "0 秒"

    running_seen = threading.Event()
    release_task = threading.Event()

    def _work(task):
        running_seen.set()
        release_task.wait(timeout=3.0)
        return {"ok": True}

    submitted = runtime.submit("comfyui_run", "digital-human-longcat", _work)
    t = runtime.tasks[submitted["id"]]
    assert running_seen.wait(timeout=2.0)
    live_snap = t.snapshot()
    assert live_snap["state"] == "running"
    assert live_snap.get("finished_at") is None
    assert isinstance(live_snap["elapsed_seconds"], float)
    assert live_snap["elapsed_seconds"] >= 0.0

    release_task.set()
    deadline = time.monotonic() + 3.0
    while time.monotonic() < deadline and t.state == "running":
        time.sleep(0.01)

    done_snap = t.snapshot()
    assert done_snap["state"] == "completed"
    assert isinstance(done_snap["finished_at"], float)
    assert done_snap["finished_at"] >= done_snap["created_at"]
    assert isinstance(done_snap["elapsed_seconds"], float)
    assert done_snap["elapsed_seconds"] >= 0.0
    frozen_elapsed = done_snap["elapsed_seconds"]
    time.sleep(0.05)
    assert t.snapshot()["elapsed_seconds"] == frozen_elapsed


def test_canvas_proxy_enforces_live_ws_cap_without_exhausting_comfy_canvas(monkeypatch):
    import socket
    from server import comfy_proxy

    monkeypatch.setattr(comfy_proxy, "MAX_LIVE_WS_CONNECTIONS", 2)

    hold_release = threading.Event()

    class _HoldingUpstream:
        def __init__(self, tag: str):
            self.tag = tag
            self.sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            self.sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            self.sock.bind(("127.0.0.1", 0))
            self.port = self.sock.getsockname()[1]
            self.sock.listen(16)
            self.sock.settimeout(0.2)
            self.stop_event = threading.Event()
            self.thread = threading.Thread(target=self._serve, daemon=True)
            self.thread.start()

        def _serve(self):
            while not self.stop_event.is_set():
                try:
                    conn, _ = self.sock.accept()
                except socket.timeout:
                    continue
                except OSError:
                    break
                threading.Thread(target=self._handle, args=(conn,), daemon=True).start()

        def _handle(self, conn: socket.socket):
            with conn:
                conn.settimeout(3.0)
                buf = bytearray()
                while b"\r\n\r\n" not in buf:
                    chunk = conn.recv(4096)
                    if not chunk:
                        return
                    buf.extend(chunk)
                head = bytes(buf).decode("latin-1")
                if "upgrade: websocket" in head.lower():
                    conn.sendall(
                        b"HTTP/1.1 101 Switching Protocols\r\n"
                        b"Upgrade: websocket\r\n"
                        b"Connection: Upgrade\r\n\r\n"
                    )
                    hold_release.wait(timeout=5.0)
                    return
                body = f'{{"ok":true,"tag":"{self.tag}"}}'.encode("utf-8")
                conn.sendall(
                    (
                        "HTTP/1.1 200 OK\r\n"
                        "Content-Type: application/json\r\n"
                        f"Content-Length: {len(body)}\r\n"
                        "Connection: close\r\n\r\n"
                    ).encode("latin-1")
                    + body
                )

        def close(self):
            self.stop_event.set()
            try:
                self.sock.close()
            except OSError:
                pass
            self.thread.join(timeout=3.0)

    comfy_up = _HoldingUpstream("comfy")
    live_up = _HoldingUpstream("live")
    touches: list[int] = []
    proxy = ColabOneCanvasProxyServer(
        upstream_host="127.0.0.1",
        upstream_port=comfy_up.port,
        listen_host="127.0.0.1",
        listen_port=0,
        instance="inst-cap",
        canvas_token="tok-cap",
        session_cookie="sess-cap",
        on_activity=lambda: touches.append(1),
        live_upstream_port=live_up.port,
    )
    proxy.start()
    open_ws: list[socket.socket] = []
    try:
        ws_req = (
            "GET /_colabone_live/ws HTTP/1.1\r\n"
            "Host: 127.0.0.1\r\n"
            "Upgrade: websocket\r\n"
            "Connection: keep-alive, Upgrade\r\n"
            "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"
            "Sec-WebSocket-Version: 13\r\n\r\n"
        ).encode("latin-1")

        # Open MAX_LIVE_WS_CONNECTIONS (=2) live WS connections and verify none call on_activity.
        for _ in range(2):
            s = socket.create_connection(("127.0.0.1", proxy.port), timeout=3.0)
            s.sendall(ws_req)
            resp = bytearray()
            while b"\r\n\r\n" not in resp:
                resp.extend(s.recv(4096))
            assert resp.startswith(b"HTTP/1.1 101")
            open_ws.append(s)
        assert touches == []

        # 3rd live WS connection hits the separate live WS cap with 503 JSON.
        st_503, _, body_503 = _raw_http_exchange(proxy.port, ws_req)
        assert st_503 == 503
        assert "Live Studio" in json.loads(body_503)["error"]
        assert touches == []

        # Meanwhile authorized ComfyUI Canvas traffic still succeeds cleanly.
        st_comfy, _, body_comfy = _raw_http_exchange(
            proxy.port,
            b"GET /system_stats HTTP/1.1\r\nHost: 127.0.0.1\r\nAuthorization: Bearer tok-cap\r\n\r\n",
        )
        assert st_comfy == 200
        assert json.loads(body_comfy)["tag"] == "comfy"
        assert len(touches) == 1
    finally:
        hold_release.set()
        for s in open_ws:
            try:
                s.close()
            except OSError:
                pass
        proxy.stop()
        comfy_up.close()
        live_up.close()


def test_canvas_proxy_hardening_bidirectional_ws_gating_chunked_hop_origin_and_token(
    tmp_path, monkeypatch
):
    import os
    import stat
    from types import SimpleNamespace
    from server import comfy_proxy
    from server.comfy_proxy import ColabOneComfyCanvasGateway

    # 1. Upstream server supporting:
    #    - /ws_stream: sends 101 then streams chunks every 0.10s for 0.45s (tests bidirectional WS idle)
    #    - /ws_reject_chunked: sends 403 Transfer-Encoding: chunked + Connection: keep-alive (never closes)
    #    - /ws_pipelined: sends 101 then echoes any client bytes received
    #    - /slow_stream: sends 200 streaming 4 chunks spaced 0.12s apart (tests no outer wall-clock cap)
    #    - /echo_headers: returns received request headers + adds hop-by-hop headers in response
    class _HardeningUpstream:
        def __init__(self):
            self.sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            self.sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            self.sock.bind(("127.0.0.1", 0))
            self.port = self.sock.getsockname()[1]
            self.sock.listen(32)
            self.sock.settimeout(0.2)
            self.stop_event = threading.Event()
            self.records: list[dict] = []
            self.lock = threading.Lock()
            self.thread = threading.Thread(target=self._serve, daemon=True)
            self.thread.start()

        def _serve(self):
            while not self.stop_event.is_set():
                try:
                    conn, _ = self.sock.accept()
                except socket.timeout:
                    continue
                except OSError:
                    break
                threading.Thread(target=self._handle, args=(conn,), daemon=True).start()

        def _handle(self, conn: socket.socket):
            with conn:
                conn.settimeout(5.0)
                buf = bytearray()
                while b"\r\n\r\n" not in buf:
                    chunk = conn.recv(4096)
                    if not chunk:
                        return
                    buf.extend(chunk)
                sep = buf.index(b"\r\n\r\n")
                head = bytes(buf[:sep]).decode("latin-1")
                leftover = bytes(buf[sep + 4 :])
                lines = head.split("\r\n")
                req_line = lines[0]
                target = req_line.split(" ")[1]
                hdrs: dict[str, str] = {}
                for line in lines[1:]:
                    k, _, v = line.partition(":")
                    hdrs[k.strip().lower()] = v.strip()

                if target == "/ws_reject_chunked":
                    # Wait briefly to verify proxy did NOT forward pipelined client bytes before 101.
                    conn.settimeout(0.15)
                    extra = b""
                    try:
                        extra = conn.recv(4096)
                    except socket.timeout:
                        pass
                    with self.lock:
                        self.records.append(
                            {"target": target, "headers": hdrs, "early_bytes": leftover + extra}
                        )
                    conn.sendall(
                        b"HTTP/1.1 403 Forbidden\r\n"
                        b"Transfer-Encoding: chunked\r\n"
                        b"Connection: keep-alive, X-Resp-Hop\r\n"
                        b"X-Resp-Hop: secret-hop\r\n"
                        b"Upgrade: websocket\r\n"
                        b"Proxy-Authenticate: Basic\r\n"
                        b"\r\n"
                        b"6\r\ndenied\r\n"
                        b"0\r\n\r\n"
                    )
                    # Deliberately keep connection open until proxy closes it after 0\r\n\r\n!
                    conn.settimeout(2.0)
                    try:
                        while conn.recv(1024):
                            pass
                    except OSError:
                        pass
                    return

                with self.lock:
                    self.records.append({"target": target, "headers": hdrs, "leftover": leftover})

                if target == "/ws_stream":
                    try:
                        conn.sendall(
                            b"HTTP/1.1 101 Switching Protocols\r\n"
                            b"Upgrade: websocket\r\n"
                            b"Connection: Upgrade\r\n\r\n"
                        )
                        for idx in range(6):
                            time.sleep(0.15)
                            conn.sendall(f"tick-{idx}|".encode("ascii"))
                    except OSError:
                        return
                    # Now go completely silent so shared WS_IDLE_TIMEOUT triggers cleanly.
                    conn.settimeout(5.0)
                    try:
                        while conn.recv(1024):
                            pass
                    except OSError:
                        pass
                    return

                if target == "/ws_pipelined":
                    conn.sendall(
                        b"HTTP/1.1 101 Switching Protocols\r\n"
                        b"Upgrade: websocket\r\n"
                        b"Connection: Upgrade\r\n\r\n"
                    )
                    data = leftover or conn.recv(4096)
                    if data:
                        conn.sendall(b"got:" + data)
                    return

                if target == "/slow_stream":
                    try:
                        conn.sendall(
                            b"HTTP/1.1 200 OK\r\n"
                            b"Content-Type: text/plain\r\n"
                            b"Transfer-Encoding: chunked\r\n"
                            b"Connection: close\r\n\r\n"
                        )
                        for i in range(5):
                            time.sleep(0.16)
                            part = f"p{i}".encode("ascii")
                            conn.sendall(
                                f"{len(part):x}\r\n".encode("ascii") + part + b"\r\n"
                            )
                        conn.sendall(b"0\r\n\r\n")
                    except OSError:
                        return
                    return

                body = json.dumps({"headers": hdrs}).encode("utf-8")
                conn.sendall(
                    (
                        "HTTP/1.1 200 OK\r\n"
                        "Content-Type: application/json\r\n"
                        f"Content-Length: {len(body)}\r\n"
                        "Connection: close, X-Drop-Me\r\n"
                        "X-Drop-Me: nope\r\n"
                        "Proxy-Authenticate: Basic\r\n"
                        "TE: trailers\r\n"
                        "Trailers: X-Foo\r\n"
                        "Upgrade: h2c\r\n\r\n"
                    ).encode("latin-1")
                    + body
                )

        def close(self):
            self.stop_event.set()
            try:
                self.sock.close()
            except OSError:
                pass
            self.thread.join(timeout=3.0)

    up = _HardeningUpstream()
    proxy = ColabOneCanvasProxyServer(
        upstream_host="127.0.0.1",
        upstream_port=up.port,
        live_upstream_port=up.port,
        listen_host="127.0.0.1",
        listen_port=0,
        instance="inst-hard",
        canvas_token="tok-hard",
        session_cookie="sess-hard",
        expected_origin="https://comfy.example.com",
    )
    proxy.start()
    try:
        # A. Hop-by-hop headers stripped on both request and response.
        st, resp_hdrs, resp_body = _raw_http_exchange(
            proxy.port,
            (
                "GET /echo_headers HTTP/1.1\r\n"
                "Host: comfy.example.com\r\n"
                "Authorization: Bearer tok-hard\r\n"
                "Connection: keep-alive, X-Client-Hop\r\n"
                "X-Client-Hop: drop-me\r\n"
                "Proxy-Authorization: Basic abc\r\n"
                "TE: trailers\r\n"
                "Trailers: X-Bar\r\n"
                "Upgrade: h2c\r\n\r\n"
            ).encode("latin-1"),
        )
        assert st == 200
        seen_req_hdrs = json.loads(resp_body)["headers"]
        for stripped in ("x-client-hop", "proxy-authorization", "te", "trailers", "upgrade"):
            assert stripped not in seen_req_hdrs
        for stripped in ("x-drop-me", "proxy-authenticate", "te", "trailers", "upgrade"):
            assert stripped not in resp_hdrs

        # B. Strict RFC 6455 upgrade validation: bogus upgrade headers are NOT forwarded as WS upgrades.
        for bad_upgrade_req in (
            (
                "GET /echo_headers HTTP/1.1\r\nHost: comfy.example.com\r\n"
                "Authorization: Bearer tok-hard\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"
                "Sec-WebSocket-Key: not-16-bytes==\r\nSec-WebSocket-Version: 13\r\n\r\n"
            ),
            (
                "GET /echo_headers HTTP/1.1\r\nHost: comfy.example.com\r\n"
                "Authorization: Bearer tok-hard\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"
                "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 8\r\n\r\n"
            ),
            (
                "GET /echo_headers HTTP/1.1\r\nHost: comfy.example.com\r\n"
                "Authorization: Bearer tok-hard\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"
                "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n"
                "Content-Length: 0\r\n\r\n"
            ),
        ):
            st_b, _, body_b = _raw_http_exchange(proxy.port, bad_upgrade_req.encode("latin-1"))
            assert st_b == 200
            assert "upgrade" not in json.loads(body_b)["headers"]

        # C. Upgrade gating on non-101 chunked keep-alive response:
        #    leftover is NOT forwarded, Connection is normalized to Upgrade (no keep-alive),
        #    and proxy terminates cleanly at 0\r\n\r\n without hanging!
        t_start = time.monotonic()
        with socket.create_connection(("127.0.0.1", proxy.port), timeout=3.0) as s:
            s.settimeout(3.0)
            s.sendall(
                (
                    "GET /ws_reject_chunked HTTP/1.1\r\n"
                    "Host: comfy.example.com\r\n"
                    "Origin: https://comfy.example.com\r\n"
                    "Upgrade: websocket\r\n"
                    "Connection: keep-alive, Upgrade\r\n"
                    "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"
                    "Sec-WebSocket-Version: 13\r\n"
                    "Authorization: Bearer tok-hard\r\n\r\n"
                    "SMUGGLED-PIPELINE-BYTES"
                ).encode("latin-1")
            )
            chunks = []
            while True:
                data = s.recv(4096)
                if not data:
                    break
                chunks.append(data)
        assert (time.monotonic() - t_start) < 2.0
        raw_rej = b"".join(chunks).decode("latin-1")
        assert raw_rej.startswith("HTTP/1.1 403")
        assert "denied" in raw_rej
        assert "x-resp-hop" not in raw_rej.lower()
        assert "proxy-authenticate" not in raw_rej.lower()
        rej_rec = next(r for r in up.records if r["target"] == "/ws_reject_chunked")
        assert rej_rec["early_bytes"] == b""
        assert rej_rec["headers"]["connection"].lower() == "upgrade"

        # D. Valid 101 releases pipelined leftover bytes after confirmation.
        with socket.create_connection(("127.0.0.1", proxy.port), timeout=3.0) as ws:
            ws.sendall(
                (
                    "GET /ws_pipelined HTTP/1.1\r\n"
                    "Host: comfy.example.com\r\n"
                    "Origin: https://comfy.example.com\r\n"
                    "Upgrade: websocket\r\n"
                    "Connection: keep-alive, Upgrade\r\n"
                    "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"
                    "Sec-WebSocket-Version: 13\r\n"
                    "Authorization: Bearer tok-hard\r\n\r\n"
                    "hello-pipelined"
                ).encode("latin-1")
            )
            buf = bytearray()
            while b"got:hello-pipelined" not in buf:
                chunk = ws.recv(4096)
                assert chunk
                buf.extend(chunk)
            assert buf.startswith(b"HTTP/1.1 101")

        # E. Strict Origin validation on WS upgrades and POST mutating requests.
        for bad_origin_req in (
            (
                "POST /echo_headers HTTP/1.1\r\nHost: comfy.example.com\r\n"
                "Origin: https://evil.example\r\nAuthorization: Bearer tok-hard\r\n"
                "Content-Length: 0\r\n\r\n"
            ),
            (
                "POST /echo_headers HTTP/1.1\r\nHost: comfy.example.com\r\n"
                "Origin: http://comfy.example.com\r\nAuthorization: Bearer tok-hard\r\n"
                "Content-Length: 0\r\n\r\n"
            ),
            (
                "POST /echo_headers HTTP/1.1\r\nHost: evil.example\r\n"
                "Origin: https://evil.example\r\nAuthorization: Bearer tok-hard\r\n"
                "Content-Length: 0\r\n\r\n"
            ),
            (
                "GET /ws_pipelined HTTP/1.1\r\nHost: comfy.example.com\r\n"
                "Origin: null\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"
                "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n"
                "Authorization: Bearer tok-hard\r\n\r\n"
            ),
            (
                "GET /_colabone_live/ws HTTP/1.1\r\nHost: comfy.example.com\r\n"
                "Origin: mygo://evil.com\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"
                "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n"
            ),
            (
                "GET /_colabone_live/ws HTTP/1.1\r\nHost: comfy.example.com\r\n"
                "Origin: https://evil.com\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"
                "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n"
            ),
        ):
            st_o, _, _ = _raw_http_exchange(proxy.port, bad_origin_req.encode("latin-1"))
            assert st_o == 403

        # Allowed origins: loopback on loopback Host, and trusted desktop app origins on /_colabone_live/ws.
        st_lo, _, _ = _raw_http_exchange(
            proxy.port,
            (
                "POST /echo_headers HTTP/1.1\r\nHost: 127.0.0.1\r\n"
                "Origin: http://localhost:5173\r\nAuthorization: Bearer tok-hard\r\n"
                "Content-Length: 0\r\n\r\n"
            ).encode("latin-1"),
        )
        assert st_lo == 200

        for app_origin in (
            "mygo://localhost",
            "http://mygo.localhost",
            "colabone://",
            "app://localhost",
            "wails://wails.localhost",
        ):
            with socket.create_connection(("127.0.0.1", proxy.port), timeout=3.0) as ws:
                ws.sendall(
                    (
                        "GET /_colabone_live/ws HTTP/1.1\r\n"
                        "Host: comfy.example.com\r\n"
                        f"Origin: {app_origin}\r\n"
                        "Upgrade: websocket\r\n"
                        "Connection: Upgrade\r\n"
                        "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"
                        "Sec-WebSocket-Version: 13\r\n\r\n"
                    ).encode("latin-1")
                )
                first = ws.recv(4096)
                # /_colabone_live/ws passed Origin check and reached upstream (not 403).
                assert not first.startswith(b"HTTP/1.1 403")

        # F. Bidirectional WS idle timeout & non-upgrade streaming without outer wall-clock cap.
        with monkeypatch.context() as mp:
            mp.setattr(comfy_proxy, "WS_IDLE_TIMEOUT", 0.60)
            mp.setattr(comfy_proxy, "BODY_IDLE_TIMEOUT", 0.60)

            # Active server-only WS stream lasts ~0.90s (> 0.60s) while client sends nothing;
            # all 6 ticks must arrive before shared idle timeout closes the socket.
            with socket.create_connection(("127.0.0.1", proxy.port), timeout=5.0) as ws:
                ws.settimeout(5.0)
                ws.sendall(
                    (
                        "GET /ws_stream HTTP/1.1\r\n"
                        "Host: comfy.example.com\r\n"
                        "Origin: https://comfy.example.com\r\n"
                        "Upgrade: websocket\r\n"
                        "Connection: Upgrade\r\n"
                        "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"
                        "Sec-WebSocket-Version: 13\r\n"
                        "Authorization: Bearer tok-hard\r\n\r\n"
                    ).encode("latin-1")
                )
                collected = bytearray()
                while True:
                    chunk = ws.recv(4096)
                    if not chunk:
                        break
                    collected.extend(chunk)
                text_ws = collected.decode("latin-1")
                for idx in range(6):
                    assert f"tick-{idx}|" in text_ws

            # Non-upgrade HTTP stream lasts ~0.80s (> BODY_IDLE_TIMEOUT 0.60s total, 0.16s per chunk).
            st_stream, _, body_stream = _raw_http_exchange(
                proxy.port,
                (
                    "GET /slow_stream HTTP/1.1\r\n"
                    "Host: comfy.example.com\r\n"
                    "Authorization: Bearer tok-hard\r\n\r\n"
                ).encode("latin-1"),
            )
            assert st_stream == 200
            for i in range(5):
                assert f"p{i}".encode("ascii") in body_stream
    finally:
        proxy.stop()
        up.close()

    # G. start() calls stop() when ready.wait times out.
    stop_called = {"called": False}
    timeout_proxy = ColabOneCanvasProxyServer(listen_port=0)
    orig_stop = timeout_proxy.stop

    def _spy_stop():
        stop_called["called"] = True
        orig_stop()

    async def _never_ready_serve(self, loop, ready, cancel_start, start_error):
        while not cancel_start.is_set():
            await asyncio.sleep(0.01)

    import asyncio

    monkeypatch.setattr(timeout_proxy, "stop", _spy_stop)
    monkeypatch.setattr(
        ColabOneCanvasProxyServer, "_serve", _never_ready_serve
    )
    orig_event_cls = comfy_proxy.threading.Event

    class _FastTimeoutEvent(orig_event_cls):
        def wait(self, timeout=None):
            return super().wait(timeout=0.05 if timeout == 10.0 else timeout)

    monkeypatch.setattr(comfy_proxy.threading, "Event", _FastTimeoutEvent)
    with pytest.raises(RuntimeError, match="超时"):
        timeout_proxy.start()
    assert stop_called["called"] is True
    monkeypatch.undo()

    # H. .comfy-cloudflared-token rejects symlinks (O_NOFOLLOW) and enforces 0o600 via fchmod.
    if os.name == "posix":
        gw_root = tmp_path / "gw_root"
        gw_root.mkdir()
        gw = ColabOneComfyCanvasGateway(
            SimpleNamespace(root=gw_root, catalog=None, touch=lambda *a: None)
        )
        token_file = gw_root / ".comfy-cloudflared-token"
        victim = tmp_path / "victim.txt"
        victim.write_text("keep", encoding="utf-8")
        token_file.symlink_to(victim)

        monkeypatch.setattr(comfy_proxy, "_call_ensure_binary", lambda *a: Path("/bin/true"))
        gw._run_tunnel(
            SimpleNamespace(domain="https://comfy.example.com", tunnel_token="secret-tok"),
            proxy_port=8189,
        )
        assert victim.read_text(encoding="utf-8") == "keep"
        assert gw.phase == "failed"

        # Pre-existing world-readable token file gets chmodded to 0o600 via os.fchmod.
        token_file.unlink(missing_ok=True)
        token_file.write_text("old", encoding="utf-8")
        os.chmod(token_file, 0o644)
        gw2 = ColabOneComfyCanvasGateway(
            SimpleNamespace(root=gw_root, catalog=None, touch=lambda *a: None)
        )
        seen_modes: list[int] = []

        def _fake_popen(*a, **kw):
            seen_modes.append(stat.S_IMODE(token_file.stat().st_mode))
            gw2._tunnel_cancel.set()
            p = Mock()
            p.stdout = None
            p.stderr = None
            p.poll.return_value = 0
            return p

        monkeypatch.setattr(comfy_proxy.subprocess, "Popen", _fake_popen)
        gw2._run_tunnel(
            SimpleNamespace(domain="https://comfy.example.com", tunnel_token="fresh-tok"),
            proxy_port=8189,
        )
        assert seen_modes == [0o600]


def test_comfyui_pg_reap_trash_tombstone_cleanup_and_log_tail_symlink_safety(
    tmp_path, monkeypatch
):
    import os
    from server import comfyui as comfy_mod
    from server.comfyui import (
        ColabOneComfyWorkflowStore,
        _open_secure_log_write,
        _read_log_tail,
        _terminate_process_group,
    )

    # 1. _terminate_process_group calls proc.poll() inside the _pg_has_members loop before SIGKILL.
    poll_calls = {"count": 0}
    killpg_signals: list[int] = []

    class _ZombieLeaderProc:
        pid = 999991

        def poll(self):
            poll_calls["count"] += 1
            return 0 if poll_calls["count"] >= 2 else None

        def wait(self, timeout=None):
            return 0

        def terminate(self):
            pass

        def kill(self):
            pass

    def _fake_killpg(pgid, sig):
        killpg_signals.append(sig)

    monkeypatch.setattr(comfy_mod.os, "killpg", _fake_killpg, raising=False)
    monkeypatch.setattr(comfy_mod.os, "getpgrp", lambda: 1, raising=False)
    monkeypatch.setattr(
        comfy_mod,
        "_pg_has_members",
        lambda pgid: poll_calls["count"] < 2,
    )
    _terminate_process_group(_ZombieLeaderProc(), timeout=0.5)
    assert poll_calls["count"] >= 2
    sigkill = getattr(comfy_mod.signal, "SIGKILL", None)
    if sigkill is not None:
        assert sigkill not in killpg_signals
    assert len(killpg_signals) <= 1
    monkeypatch.undo()

    # 2. save_workflow clears matching .trash tombstone on re-create.
    store = ColabOneComfyWorkflowStore(tmp_path)
    store.save_workflow(
        {"id": "wf-recreate-1", "name": "First", "workflow": sample_api_workflow()}
    )
    store.delete_workflow("wf-recreate-1")
    tombstone = tmp_path / "workflows" / ".trash" / "wf-recreate-1.json"
    assert tombstone.is_file()
    store.save_workflow(
        {"id": "wf-recreate-1", "name": "Recreated", "workflow": sample_api_workflow()}
    )
    assert not tombstone.exists()
    assert store.get_workflow("wf-recreate-1")["name"] == "Recreated"

    # 3. comfyui.log rejects symlinks on open-for-write and reads bounded EOF tail cleanly.
    big_log = tmp_path / "comfyui.log"
    big_log.write_bytes(b"A" * 20000 + b"TAIL-MARKER-END\n")
    tail = _read_log_tail(big_log, max_bytes=4096)
    assert len(tail.encode("utf-8")) <= 4096
    assert tail.endswith("TAIL-MARKER-END\n")
    assert len(tail) < 5000

    if os.name == "posix":
        secret = tmp_path / "secret.txt"
        secret.write_text("TOP-SECRET", encoding="utf-8")
        sym_log = tmp_path / "sym_comfyui.log"
        sym_log.symlink_to(secret)
        assert _read_log_tail(sym_log, max_bytes=4096) == ""
        with pytest.raises(ValueError, match="符号链接"):
            _open_secure_log_write(sym_log)
        assert secret.read_text(encoding="utf-8") == "TOP-SECRET"


def test_workflow_output_record_and_store_task_id_contract(tmp_path):
    # 1. Schema accepts optional task_id, defaults to "" for legacy JSON, and rejects >120 chars.
    legacy_payload = {
        "id": "a" * 32,
        "workflow_id": "wf_demo-1",
        "workflow_name": "Demo",
        "kind": "image",
        "filename": "frame_001.png",
        "extension": "png",
        "mime": "image/png",
        "size": 64,
        "sha256": "b" * 64,
        "created_at": 100.0,
        "elapsed": 1.2,
        "prompt_id": "prompt-1",
    }
    legacy_rec = WorkflowOutputRecord.model_validate_json(json.dumps(legacy_payload))
    assert legacy_rec.task_id == ""

    exact_120 = "t" * 120
    tagged_rec = WorkflowOutputRecord.model_validate(legacy_payload | {"task_id": exact_120})
    assert tagged_rec.task_id == exact_120

    with pytest.raises(ValidationError):
        WorkflowOutputRecord.model_validate(legacy_payload | {"task_id": "t" * 121})

    # 2. Store save_output and save_output_file persist task_id, preserve positional compat,
    #    truncate overlong task_id to 120 chars, and reload legacy JSON files omitting task_id.
    store = ColabOneComfyWorkflowStore(tmp_path)
    png_raw = make_png_bytes("purple")

    # Positional save_output without task_id stays backward compatible.
    rec_pos = store.save_output(png_raw, "wf-1", "Flow 1", "pos.png", 1.1, "p-pos")
    assert rec_pos["task_id"] == ""

    # Keyword task_id on save_output is persisted and reloaded.
    rec_kw = store.save_output(
        png_raw,
        "wf-1",
        "Flow 1",
        "kw.png",
        2.2,
        prompt_id="p-kw",
        task_id="task-abc-123",
    )
    assert rec_kw["task_id"] == "task-abc-123"

    # save_output_file persists task_id and truncates >120 chars cleanly.
    src_file = tmp_path / "src_clip.mp4"
    src_file.write_bytes(b"\x00\x00\x00\x18ftypmp42task-tag-test")
    overlong_task_id = "task-" + ("x" * 140)
    rec_file = store.save_output_file(
        src_path=src_file,
        workflow_id="wf-1",
        workflow_name="Flow 1",
        filename="clip.mp4",
        elapsed=3.4,
        prompt_id="p-file",
        task_id=overlong_task_id,
    )
    assert rec_file["task_id"] == overlong_task_id[:120]

    # Legacy positional save_output_file(src_path, wf_id, wf_name, filename, elapsed, prompt_id) still works.
    rec_file_pos = store.save_output_file(
        src_file, "wf-1", "Flow 1", "clip_pos.mp4", 1.5, "p-file-pos"
    )
    assert rec_file_pos["prompt_id"] == "p-file-pos"
    assert rec_file_pos["task_id"] == ""

    # Simulate an existing persisted output JSON on disk without task_id.
    legacy_meta_path = store.outputs_dir / f"{rec_pos['id']}.json"
    legacy_meta = json.loads(legacy_meta_path.read_text(encoding="utf-8"))
    legacy_meta.pop("task_id", None)
    legacy_meta_path.write_text(json.dumps(legacy_meta), encoding="utf-8")

    reloaded = ColabOneComfyWorkflowStore(tmp_path)
    by_id = {o["id"]: o for o in reloaded.list_outputs()}
    assert by_id[rec_pos["id"]]["task_id"] == ""
    assert by_id[rec_kw["id"]]["task_id"] == "task-abc-123"
    assert by_id[rec_file["id"]]["task_id"] == overlong_task_id[:120]
    assert by_id[rec_file_pos["id"]]["task_id"] == ""


def test_comfyui_native_pack_reset_save_output_7arg_log_fd_skip_auto_release_and_tts_ref_flags(
    runtime, tmp_path, monkeypatch
):
    import os
    import threading
    import wave
    from server import comfyui as comfy_mod
    from server.comfyui import ColabOneComfyWorkflowStore, _read_log_tail
    from server.runtime import ColabOneTask
    from server.tts_engines import ColabOneRemoteTtsRunner

    # 1. Stale _native_active_pack_id cleared on standard/custom workflow after fashion_tryon.
    fake_proc = Mock()
    fake_proc.poll.return_value = None
    runtime.comfy.proc = fake_proc
    runtime.comfy.state = "ready"
    runtime.http = Mock()
    runtime.http.get.return_value = make_http_resp({"queue_running": [], "queue_pending": []})

    runtime.comfy.mark_native_pack_ready("fashion-tryon-h3")
    assert runtime.comfy.status()["active_pack"] == "fashion-tryon-h3"

    custom_wf = {
        "id": "custom-plain-wf",
        "name": "Custom Plain",
        "workflow": sample_api_workflow(),
    }
    runtime.comfy.ensure_template_ready(ColabOneTask("comfyui_run", "custom-plain-wf"), custom_wf)
    assert runtime.comfy._native_active_pack_id == ""
    assert runtime.comfy.status()["active_pack"] == ""
    runtime.comfy.proc = None
    runtime.comfy.state = "stopped"

    # 2. save_output_file 7-positional-arg compat preserves BOTH prompt_id and task_id,
    #    and save_output/save_output_file normalize whitespace and cap at 120 chars.
    store = ColabOneComfyWorkflowStore(tmp_path / "store_7arg")
    src_file = tmp_path / "clip7.mp4"
    src_file.write_bytes(b"\x00\x00\x00\x18ftypmp42seven-pos-args")
    rec_7 = store.save_output_file(
        src_file,
        "wf-7",
        "Flow 7",
        "clip7.mp4",
        1.75,
        "  p-seven  ",
        "  task-seven  ",
    )
    assert rec_7["elapsed"] == 1.75
    assert rec_7["prompt_id"] == "p-seven"
    assert rec_7["task_id"] == "task-seven"

    rec_raw = store.save_output(
        make_png_bytes("teal"),
        "wf-7",
        "Flow 7",
        "trim.png",
        0.5,
        "  p-" + ("a" * 140) + "  ",
        "  t-" + ("b" * 140) + "  ",
    )
    assert rec_raw["prompt_id"] == ("p-" + ("a" * 140))[:120]
    assert rec_raw["task_id"] == ("t-" + ("b" * 140))[:120]

    # 3. _read_log_tail on a non-regular file (directory) must NOT leak file descriptors.
    if os.name == "posix":
        dir_as_log = tmp_path / "not_a_regular_file"
        dir_as_log.mkdir()
        opened_fds: list[int] = []
        closed_fds: list[int] = []
        orig_open = comfy_mod.os.open
        orig_close = comfy_mod.os.close

        def _spy_open(path, flags, *args, **kwargs):
            fd = orig_open(path, flags, *args, **kwargs)
            opened_fds.append(fd)
            return fd

        def _spy_close(fd):
            closed_fds.append(fd)
            return orig_close(fd)

        with monkeypatch.context() as mp:
            mp.setattr(comfy_mod.os, "open", _spy_open)
            mp.setattr(comfy_mod.os, "close", _spy_close)
            assert _read_log_tail(dir_as_log, max_bytes=1024) == ""
        assert opened_fds, "Expected os.open to open directory fd before fstat check"
        assert set(opened_fds).issubset(set(closed_fds)), (
            f"Leaked fd(s) on non-regular log path: opened={opened_fds}, closed={closed_fds}"
        )

    # 4. skip_auto_release isolates guest workflow runs from host-armed auto_release.
    runtime.comfy.store.save_workflow(
        {"id": "wf-guest-iso", "name": "Guest Iso", "workflow": sample_api_workflow()}
    )
    monkeypatch.setattr(runtime.drive, "available", lambda: True)
    finalize_calls: list[str] = []
    monkeypatch.setattr(
        runtime,
        "_finalize_comfyui_workflow_auto_release",
        lambda t: finalize_calls.append(t.id),
    )

    armed = runtime.set_comfyui_auto_release(True)
    assert armed["enabled"] is True
    assert armed["state"] == "armed"

    gate = threading.Event()
    started_ev = threading.Event()

    def _guest_run_wf(task, req):
        started_ev.set()
        gate.wait(timeout=3.0)
        task.outputs.append({"id": "a" * 32, "filename": "guest.png"})
        return list(task.outputs)

    monkeypatch.setattr(runtime.comfy, "run_workflow", _guest_run_wf)
    snap_guest = runtime.run_comfyui_workflow(
        WorkflowRunRequest(
            workflow_id="wf-guest-iso",
            skip_auto_release=True,
            auto_release_after_sync=True,
        )
    )
    assert started_ev.wait(timeout=3.0)

    # Host re-arming auto_release while a guest skip_auto_release task runs must NOT bind to guest task.
    rearmed = runtime.set_comfyui_auto_release(True)
    assert rearmed["task_id"] == ""
    gate.set()

    done_guest = wait_task(runtime, snap_guest)
    assert done_guest.state == "completed", done_guest.error
    assert finalize_calls == []
    ar_after = runtime.workflow_auto_release_status()
    assert ar_after["enabled"] is True
    assert ar_after["state"] == "armed"
    assert ar_after["task_id"] == ""
    assert runtime._pending_workflow_auto_release is False
    assert runtime.quiescing is False

    # A failing guest run with skip_auto_release=True must also leave host's armed auto_release intact.
    monkeypatch.setattr(
        runtime.comfy,
        "run_workflow",
        lambda task, req: (_ for _ in ()).throw(RuntimeError("guest fail")),
    )
    snap_fail = runtime.run_comfyui_workflow(
        WorkflowRunRequest(workflow_id="wf-guest-iso", skip_auto_release=True)
    )
    done_fail = wait_task(runtime, snap_fail)
    assert done_fail.state == "failed"
    ar_after_fail = runtime.workflow_auto_release_status()
    assert ar_after_fail["enabled"] is True
    assert ar_after_fail["state"] == "armed"

    # 5. TTS preview persists and emits explicit has_ref_audio & has_emotion_ref boolean flags.
    def _write_short_wav(path: Path, sr: int = 24000) -> None:
        with wave.open(str(path), "wb") as wf_w:
            wf_w.setnchannels(1)
            wf_w.setsampwidth(2)
            wf_w.setframerate(sr)
            wf_w.writeframes(b"\x10\x00" * 600)

    def _fake_tts_synth(engine, segments, segments_dir):
        sr = int(engine.get("sample_rate") or 24000)
        out_paths = []
        for idx, _ in enumerate(segments):
            p = Path(segments_dir) / f"seg_{idx:03d}.wav"
            _write_short_wav(p, sr=sr)
            out_paths.append(p)
        return out_paths

    real_catalog = ComfyCatalog.model_validate_json(
        (Path(__file__).resolve().parents[1] / "catalog" / "comfyui.json").read_text(
            encoding="utf-8"
        )
    )
    engines_by_id = {e.id: e.model_dump() for e in real_catalog.tts_engines}
    tts_runner = ColabOneRemoteTtsRunner(
        runtime, engines_by_id, synth_fn=_fake_tts_synth
    )

    # (a) Preset-only preview -> both flags False.
    preset_res = tts_runner.synthesize_preview(
        ColabOneTask("comfyui_tts_preview", "voxcpm2"),
        engine_id="voxcpm2",
        voice_preset="shen_zhiyi",
        preview_text="测试预置音色试听。",
    )
    assert preset_res["has_ref_audio"] is False
    assert preset_res["has_emotion_ref"] is False
    preset_meta = json.loads(
        (tts_runner.previews_dir / f"{preset_res['preview_id']}.json").read_text(encoding="utf-8")
    )
    assert preset_meta["has_ref_audio"] is False
    assert preset_meta["has_emotion_ref"] is False
    preset_cached = tts_runner.get_cached_preview(preset_res["preview_id"])
    assert preset_cached is not None
    assert preset_cached["has_ref_audio"] is False
    assert preset_cached["has_emotion_ref"] is False
    preset_chunk = tts_runner.get_cached_preview_chunk(preset_res["preview_id"], offset=0, length=256)
    assert preset_chunk is not None
    assert preset_chunk["has_ref_audio"] is False
    assert preset_chunk["has_emotion_ref"] is False

    # (b) Custom ref audio + emotion ref audio -> both flags True.
    ref_wav = tmp_path / "custom_ref.wav"
    emo_wav = tmp_path / "emotion_ref.wav"
    _write_short_wav(ref_wav, sr=24000)
    _write_short_wav(emo_wav, sr=24000)
    ref_res = tts_runner.synthesize_preview(
        ColabOneTask("comfyui_tts_preview", "indextts2"),
        engine_id="indextts2",
        voice_preset="custom",
        preview_text="测试参考音频与情绪参考试听。",
        ref_audio_path=ref_wav,
        emotion_ref_path=emo_wav,
    )
    assert ref_res["has_ref_audio"] is True
    assert ref_res["has_emotion_ref"] is True
    ref_meta = json.loads(
        (tts_runner.previews_dir / f"{ref_res['preview_id']}.json").read_text(encoding="utf-8")
    )
    assert ref_meta["has_ref_audio"] is True
    assert ref_meta["has_emotion_ref"] is True
    ref_cached = tts_runner.get_cached_preview(ref_res["preview_id"])
    assert ref_cached is not None
    assert ref_cached["has_ref_audio"] is True
    assert ref_cached["has_emotion_ref"] is True
    ref_chunk = tts_runner.get_cached_preview_chunk(ref_res["preview_id"], offset=0, length=256)
    assert ref_chunk is not None
    assert ref_chunk["has_ref_audio"] is True
    assert ref_chunk["has_emotion_ref"] is True


