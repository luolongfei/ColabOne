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
        ]
        assert snap["runtime"]["comfyui"]["running"] is True
        assert any(w["id"] == "wf-e2e" for w in snap["workflows"])
        assert len(snap["comfy_outputs"]) == 1
        out = snap["comfy_outputs"][0]
        assert out["sha256"] == hashlib.sha256(generated_png).hexdigest()

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
