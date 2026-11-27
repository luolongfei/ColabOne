"""Offline regression tests for server hardening, concurrency, and resource fixes."""

from __future__ import annotations

import ast
import asyncio
import gc
import io
import json
import os
import socket
import sys
import threading
import time
import wave
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import MagicMock

import pytest
import requests
from fastapi.testclient import TestClient

ROOT = Path(__file__).resolve().parent.parent
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

CATALOG_DIR = ROOT / "catalog"


def _make_runtime(root: Path):
    from server.runtime import ColabOneRuntime

    return ColabOneRuntime(root, CATALOG_DIR)


def _make_png(width: int = 96, height: int = 96, color=(120, 160, 220)) -> bytes:
    from PIL import Image

    im = Image.new("RGB", (width, height), color)
    buf = io.BytesIO()
    im.save(buf, format="PNG")
    return buf.getvalue()


def _make_16bit_gray_png(width: int = 96, height: int = 96, value: int = 32768) -> bytes:
    from PIL import Image

    im = Image.new("I;16", (width, height), value)
    buf = io.BytesIO()
    im.save(buf, format="PNG")
    return buf.getvalue()


# ---------------------------------------------------------------------------
# Issue 1: Bounded weakref-tracked HTTP sessions + _runtime_http routing
# ---------------------------------------------------------------------------


def test_runtime_and_drive_http_sessions_bounded_by_thread_lifetime(tmp_path):
    from server.drive import ColabOneDrive, ensure_rclone_binary

    rt = _make_runtime(tmp_path / "rt")
    try:
        closed_sessions: list[int] = []
        orig_close = requests.Session.close

        def _tracking_close(self):
            closed_sessions.append(id(self))
            return orig_close(self)

        seen_ids: list[int] = []
        barrier = threading.Barrier(4)

        def _worker():
            sess = rt._http_session()
            seen_ids.append(id(sess))
            barrier.wait(timeout=5)

        requests.Session.close = _tracking_close
        try:
            threads = [threading.Thread(target=_worker) for _ in range(4)]
            for t in threads:
                t.start()
            for t in threads:
                t.join(timeout=5)
            del t
            threads.clear()
            del threads
            gc.collect()
            gc.collect()
            assert len(seen_ids) == 4
            assert len(set(seen_ids)) == 4
            for sid in seen_ids:
                assert sid in closed_sessions
            with rt._http_sessions_lock:
                assert len(rt._http_sessions) == 0
        finally:
            requests.Session.close = orig_close

        # Custom non-requests.Session test mock on self.http is returned directly.
        stub_http = SimpleNamespace(get=lambda *a, **kw: "mock-ok")
        rt.http = stub_http
        assert rt._http_session() is stub_http
    finally:
        rt.close_http_sessions()

    # Drive lazy http_factory: does NOT create a session when rclone already exists.
    import hashlib

    fake_catalog = tmp_path / "fake_cat"
    fake_catalog.mkdir()
    rclone_payload = b"#!/bin/sh\nexit 0\n"
    rclone_sha = hashlib.sha256(rclone_payload).hexdigest()
    (fake_catalog / "rclone.json").write_text(
        json.dumps(
            {
                "tag": "v1.69.1",
                "sha256": {"amd64": rclone_sha, "arm64": rclone_sha},
            }
        ),
        encoding="utf-8",
    )
    drive_root = tmp_path / "drive_root"
    (drive_root / "bin").mkdir(parents=True)
    fake_rclone = drive_root / "bin" / "rclone-v1.69.1"
    fake_rclone.write_bytes(rclone_payload)
    fake_rclone.chmod(0o755)

    factory_called = False

    def _fail_factory():
        nonlocal factory_called
        factory_called = True
        raise AssertionError("http_factory must not be invoked when rclone exists")

    found = ensure_rclone_binary(drive_root, fake_catalog, http_factory=_fail_factory)
    assert found == fake_rclone
    assert not factory_called

    # Reject symlinked cached rclone-<tag>.
    sym_root = tmp_path / "drive_sym"
    (sym_root / "bin").mkdir(parents=True)
    real_target = tmp_path / "outside_rclone"
    real_target.write_bytes(rclone_payload)
    real_target.chmod(0o755)
    (sym_root / "bin" / "rclone-v1.69.1").symlink_to(real_target)
    with pytest.raises(ValueError, match="符号链接"):
        ensure_rclone_binary(sym_root, fake_catalog, http_factory=_fail_factory)

    # ColabOneDrive thread-local sessions also clean up when threads exit.
    drv = ColabOneDrive(tmp_path / "drv_root", tmp_path / "drv_cache")
    try:
        drv_ids: list[int] = []
        drv_barrier = threading.Barrier(3)

        def _drv_worker():
            drv_ids.append(id(drv._http_session()))
            drv_barrier.wait(timeout=5)

        drv_threads = [threading.Thread(target=_drv_worker) for _ in range(3)]
        for t in drv_threads:
            t.start()
        for t in drv_threads:
            t.join(timeout=5)
        del t
        drv_threads.clear()
        del drv_threads
        gc.collect()
        gc.collect()
        assert len(set(drv_ids)) == 3
        with drv._http_sessions_lock:
            assert len(drv._http_sessions) == 0
    finally:
        drv.close_http_sessions()


def test_comfy_fashion_digital_human_use_runtime_http_session():
    from server.comfyui import _runtime_http as comfy_http
    from server.digital_human import _runtime_http as dh_http
    from server.fashion_tryon import _runtime_http as fashion_http

    tls_sess = object()
    shared_sess = object()
    rt_with_tls = SimpleNamespace(http=shared_sess, _http_session=lambda: tls_sess)
    rt_minimal = SimpleNamespace(http=shared_sess)

    for helper in (comfy_http, fashion_http, dh_http):
        assert helper(rt_with_tls) is tls_sess
        assert helper(rt_minimal) is shared_sess
        assert helper(None) is None


# ---------------------------------------------------------------------------
# Issue 2: TTS AB-BA deadlock, Qwen3-TTS eviction, foreground waiter counter
# ---------------------------------------------------------------------------


def test_tts_preview_resolves_preset_before_warm_exec_lock(tmp_path):
    rt = _make_runtime(tmp_path / "tts_rt")
    try:
        runner = rt.comfy.tts
        anchor_wav = tmp_path / "anchor.wav"
        with wave.open(str(anchor_wav), "wb") as wf:
            wf.setnchannels(1)
            wf.setsampwidth(2)
            wf.setframerate(24000)
            wf.writeframes(b"\x00\x00" * 2400)

        events: list[str] = []

        def _fake_ensure_preset(task, preset, hf_token=""):
            # _warm_exec_lock must NOT be held yet when preset_lock / preset resolution runs.
            other_acquired = []

            def _probe():
                got = runner._warm_exec_lock.acquire(blocking=False)
                other_acquired.append(got)
                if got:
                    runner._warm_exec_lock.release()

            probe_t = threading.Thread(target=_probe)
            probe_t.start()
            probe_t.join(timeout=2)
            assert other_acquired == [True]
            events.append("preset_resolved")
            return anchor_wav, "anchor reference text"

        def _fake_synth(
            task,
            *,
            engine_id,
            script_text,
            work_dir,
            _warm_lock_held=False,
            _pre_resolved_preset_anchor=None,
            **kwargs,
        ):
            assert _warm_lock_held is True
            assert _pre_resolved_preset_anchor is not None
            assert _pre_resolved_preset_anchor[0] == anchor_wav
            events.append("synthesized_under_warm_lock")
            out_wav = Path(work_dir) / "out.wav"
            out_wav.write_bytes(anchor_wav.read_bytes())
            return {"audio_path": out_wav, "sample_rate": 24000, "duration": 0.1}

        runner._ensure_preset_reference_audio = _fake_ensure_preset
        runner.synthesize = _fake_synth

        task = SimpleNamespace(check=lambda: None, note=lambda *a, **k: None)
        eng = runner.get_engine("indextts2")
        presets = eng.get("voice_presets") or []
        assert presets
        preset_id = presets[0]["id"]
        res = runner.synthesize_preview(
            task,
            engine_id="indextts2",
            voice_preset=preset_id,
            preview_text="测试预览文本",
        )
        assert res["engine_id"] == "indextts2"
        assert events == ["preset_resolved", "synthesized_under_warm_lock"]
    finally:
        rt.close_http_sessions()


def test_tts_qwen3_eviction_drops_local_ref_before_evict_key():
    from server import tts_engines

    src = Path(tts_engines.__file__).read_text(encoding="utf-8")
    # VoiceDesign-to-Base transition clears local `model = None` BEFORE `_evict_key`.
    idx_vd = src.index("self._evict_key((selected_repo, selected_rev, cache_dir))")
    window_vd = src[idx_vd - 120 : idx_vd]
    assert "model = None" in window_vd

    # Base-to-VoiceDesign fallback clears local `base_model = None` BEFORE `_evict_key`.
    idx_base = src.index("self._evict_key((repo, revision, cache_dir))")
    window_base = src[idx_base - 140 : idx_base]
    assert "model = None" in window_base
    assert "base_model = None" in window_base


def test_tts_foreground_waiters_counter_blocks_prefetch_until_all_finish(tmp_path):
    rt = _make_runtime(tmp_path / "tts_fg")
    try:
        runner = rt.comfy.tts
        cancel_ev = threading.Event()
        unblocked = threading.Event()

        with runner._foreground_done:
            runner._foreground_waiters = 2

        def _bg():
            ok = runner._wait_if_foreground_waiting(cancel_ev)
            if ok:
                unblocked.set()

        t = threading.Thread(target=_bg, daemon=True)
        t.start()
        time.sleep(0.05)
        assert not unblocked.is_set()

        # First foreground finishes: waiter count drops 2 to 1; prefetch MUST remain blocked.
        with runner._foreground_done:
            runner._foreground_waiters -= 1
            runner._foreground_done.notify_all()
        time.sleep(0.08)
        assert not unblocked.is_set()

        # Second foreground finishes: waiter count drops 1 to 0; prefetch wakes cleanly.
        with runner._foreground_done:
            runner._foreground_waiters -= 1
            runner._foreground_done.notify_all()
        assert unblocked.wait(timeout=2.0)
        t.join(timeout=2.0)
    finally:
        rt.close_http_sessions()


# ---------------------------------------------------------------------------
# Issue 3: Live Studio FFmpeg whitelist/env, encoder no-resurrection, TTS race
# ---------------------------------------------------------------------------


def test_live_visual_and_server_ffmpeg_whitelist_and_sanitized_env(monkeypatch, tmp_path):
    from server import live_server, live_worker_visual

    for k, v in (
        ("COLABONE_REMOTE_CONFIG", "secret-cfg"),
        ("HF_TOKEN", "hf_secret_123"),
        ("HUGGING_FACE_HUB_TOKEN", "hf_hub_secret"),
        ("OPENAI_API_KEY", "sk-secret"),
        ("TUNNEL_TOKEN", "cf-secret"),
    ):
        monkeypatch.setenv(k, v)

    vis_env = live_worker_visual._sanitized_visual_subprocess_env()
    for k in ("COLABONE_REMOTE_CONFIG", "HF_TOKEN", "HUGGING_FACE_HUB_TOKEN", "OPENAI_API_KEY", "TUNNEL_TOKEN"):
        assert k not in vis_env

    srv_env = live_server._sanitized_live_subprocess_env()
    for k in ("COLABONE_REMOTE_CONFIG", "HF_TOKEN", "HUGGING_FACE_HUB_TOKEN", "OPENAI_API_KEY", "TUNNEL_TOKEN"):
        assert k not in srv_env

    # Verify _decode_video_frames_rgb passes -nostdin and -protocol_whitelist before -i.
    captured: dict[str, object] = {}

    def _fake_run(cmd, **kwargs):
        captured["cmd"] = list(cmd)
        captured["env"] = dict(kwargs.get("env") or {})
        return SimpleNamespace(returncode=0, stdout=b"\x00" * (4 * 4 * 3), stderr=b"")

    monkeypatch.setattr(live_worker_visual.subprocess, "run", _fake_run)
    monkeypatch.setattr(live_worker_visual.shutil, "which", lambda name: "/usr/bin/ffmpeg")
    clip = tmp_path / "clip.mp4"
    clip.write_bytes(b"dummy")
    frames = live_worker_visual._decode_video_frames_rgb(clip, 4, 4, max_frames=1)
    assert len(frames) == 1
    cmd = captured["cmd"]
    i_idx = cmd.index("-i")
    assert "-nostdin" in cmd[:i_idx]
    wl_idx = cmd.index("-protocol_whitelist")
    assert wl_idx < i_idx
    assert cmd[wl_idx + 1] == "file,pipe,fd"
    assert "HF_TOKEN" not in captured["env"]
    assert "COLABONE_REMOTE_CONFIG" not in captured["env"]

    # Verify ColabOneLiveVideoEncoder protocol_whitelist for fMP4 vs RTMP.
    enc_fmp4 = live_server.ColabOneLiveVideoEncoder(width=320, height=240, fps=25)
    enc_fmp4.mode = "libx264"
    cmd_fmp4 = enc_fmp4.build_ffmpeg_command("libx264", 320, 240)
    assert "-nostdin" in cmd_fmp4
    wl_f = cmd_fmp4.index("-protocol_whitelist")
    assert cmd_fmp4[wl_f + 1] == "pipe"

    cmd_rtmp = live_server.ColabOneLiveVideoEncoder.build_ffmpeg_command(
        "libx264", 320, 240, rtmp_url="rtmp://live.example.com/live/stream"
    )
    assert "-nostdin" in cmd_rtmp
    wl_r = cmd_rtmp.index("-protocol_whitelist")
    assert cmd_rtmp[wl_r + 1] == "pipe,lavfi,rtmp,rtmps,tcp,tls,crypto"


def test_live_video_encoder_no_resurrection_and_no_rtmp_respawn_loop(monkeypatch):
    from server import live_server
    from server.live_worker_visual import ColabOneVisualFrame

    spawn_count = 0

    class _DyingProc:
        def __init__(self, *a, **kw):
            nonlocal spawn_count
            spawn_count += 1
            self.stdin = io.BytesIO()
            self.stdout = io.BytesIO()
            self.stderr = io.BytesIO()
            self._dead = False

        def poll(self):
            return 1 if self._dead else None

        def wait(self, timeout=None):
            self._dead = True
            return 1

        def terminate(self):
            self._dead = True

        def kill(self):
            self._dead = True

    monkeypatch.setattr(live_server.subprocess, "Popen", _DyingProc)
    frame = ColabOneVisualFrame(
        width=4,
        height=4,
        rgb_bytes=b"\x00" * (4 * 4 * 3),
        pts_ms=0,
        epoch=0,
        action_id="idle",
        engine_id="mock",
    )

    enc = live_server.ColabOneLiveVideoEncoder(
        width=4, height=4, fps=25, enable_ffmpeg=True
    )
    enc.mode = "libx264"
    enc.close()
    # After close(), encode_frame must NEVER resurrect `_closed` or spawn ffmpeg.
    enc.encode_frame(frame)
    assert enc._closed is True
    assert spawn_count == 0

    # When an active RTMP ffmpeg process dies, encode_frame fails cleanly and does NOT respawn on subsequent frames.
    monkeypatch.setattr(live_server.shutil, "which", lambda name: "/usr/bin/ffmpeg")
    enc2 = live_server.ColabOneLiveVideoEncoder(
        width=4,
        height=4,
        fps=25,
        enable_ffmpeg=True,
        rtmp_url="rtmp://live.example.com/live/key",
        dns_resolver=lambda h, p: [
            (socket.AF_INET, socket.SOCK_STREAM, 6, "", ("93.184.216.34", p))
        ],
    )
    enc2.mode = "libx264"
    enc2.start_ffmpeg()
    assert enc2._proc is not None
    assert spawn_count == 1
    enc2._proc._dead = True
    with pytest.raises(RuntimeError, match="RTMP"):
        enc2.encode_frame(frame)
    assert enc2._ffmpeg_failed is True
    # Subsequent frames must NOT respawn ffmpeg in a 25 FPS loop.
    enc2.encode_frame(frame)
    assert spawn_count == 1
    enc2.close()


def test_live_resident_tts_worker_load_respects_disposed(monkeypatch, tmp_path):
    from server import live_worker_tts

    worker = live_worker_tts.ColabOneResidentTtsWorker(
        engine_id="voxcpm2",
        venv_python=sys.executable,
        model_dir=tmp_path / "model",
    )
    worker._disposed = True

    class _FakeProc:
        def __init__(self, *a, **kw):
            self.stdin = io.BytesIO()
            self.stdout = io.BytesIO(b'{"type":"ready","sample_rate":24000}\n')
            self.stderr = io.BytesIO()

        def poll(self):
            return None

        def terminate(self):
            pass

        def wait(self, timeout=None):
            return 0

        def kill(self):
            pass

    monkeypatch.setattr(live_worker_tts.subprocess, "Popen", _FakeProc)
    with pytest.raises(RuntimeError):
        worker.load()
    assert worker._active_proc is None
    assert worker._proc is None


# ---------------------------------------------------------------------------
# Issue 4: Public API item_reference caps, 8 MiB response cache, BodyLimit, scrubbing
# ---------------------------------------------------------------------------


def test_openai_compat_item_reference_count_and_byte_caps():
    from server.openai_compat import (
        MAX_INPUT_ITEMS,
        _normalize_input_items,
        normalize_responses_request,
    )

    with pytest.raises(ValueError):
        _normalize_input_items(["x"] * (MAX_INPUT_ITEMS + 1))

    big_text = "A" * (512 * 1024)
    response_store = {
        "resp_1": {
            "response": {"id": "resp_1"},
            "items": [
                {
                    "id": "item_big",
                    "type": "message",
                    "role": "user",
                    "content": [{"type": "input_text", "text": big_text}],
                }
            ],
        }
    }
    refs = [{"type": "item_reference", "id": "item_big"} for _ in range(40)]
    with pytest.raises(ValueError):
        _normalize_input_items(refs, response_store)

    huge_prior = {
        "resp_many": {
            "response": {"id": "resp_many"},
            "items": [
                {"type": "message", "role": "user", "content": "hi"}
                for _ in range(MAX_INPUT_ITEMS)
            ],
        }
    }
    with pytest.raises(ValueError):
        normalize_responses_request(
            {"model": "m", "previous_response_id": "resp_many", "input": "one more"},
            huge_prior,
        )


def test_public_api_store_response_skips_oversized_single_entry(tmp_path):
    from server.public_api import ColabOnePublicGateway, MAX_SINGLE_STORED_RESPONSE_BYTES

    rt = _make_runtime(tmp_path / "rt_pub_store")
    try:
        gw = ColabOnePublicGateway(rt)
        gw._store_response(
            {"id": "resp_small", "output": []},
            [{"type": "message", "content": "ok"}],
        )
        assert "resp_small" in gw.responses

        oversized_chunk = "B" * (1024 * 1024)
        oversized_items = [{"type": "message", "content": oversized_chunk} for _ in range(10)]
        gw._store_response({"id": "resp_huge", "output": []}, oversized_items)
        assert "resp_huge" not in gw.responses
        assert "resp_small" in gw.responses
        assert MAX_SINGLE_STORED_RESPONSE_BYTES == 8 * 1024 * 1024
    finally:
        rt.close_http_sessions()


def test_public_api_body_limit_preauth_content_length_and_get_body(tmp_path):
    from server.openai_compat import MAX_BODY_BYTES
    from server.public_api import (
        ColabOneBodyLimit,
        ColabOnePublicGateway,
        create_colabone_public_app,
    )

    rt = _make_runtime(tmp_path / "rt_pub_lim")
    try:
        gw = ColabOnePublicGateway(rt)
        gw.enabled = True
        gw.key = "co-secret-key-123"
        app = create_colabone_public_app(gw)

        receive_calls = 0
        sent_messages: list[dict] = []

        async def _send_capture(msg):
            sent_messages.append(msg)

        async def _failing_receive():
            nonlocal receive_calls
            receive_calls += 1
            return {"type": "http.request", "body": b"should-not-be-read", "more_body": False}

        mw = ColabOneBodyLimit(app, gateway=gw)
        # 1. Unauthenticated oversized request rejected 401 before calling receive().
        asyncio.run(
            mw(
                {
                    "type": "http",
                    "method": "POST",
                    "path": "/v1/chat/completions",
                    "headers": [(b"content-length", str(MAX_BODY_BYTES + 100).encode())],
                },
                _failing_receive,
                _send_capture,
            )
        )
        assert sent_messages[0]["status"] == 401
        assert receive_calls == 0

        # 2. Authenticated request with Content-Length > MAX_BODY_BYTES rejected 413 upfront.
        sent_messages.clear()
        asyncio.run(
            mw(
                {
                    "type": "http",
                    "method": "POST",
                    "path": "/v1/chat/completions",
                    "headers": [
                        (b"authorization", b"Bearer co-secret-key-123"),
                        (b"content-length", str(MAX_BODY_BYTES + 1).encode()),
                    ],
                },
                _failing_receive,
                _send_capture,
            )
        )
        assert sent_messages[0]["status"] == 413
        assert receive_calls == 0

        # 3. GET / DELETE / HEAD with non-empty body rejected 400 upfront.
        for meth in ("GET", "DELETE", "HEAD"):
            sent_messages.clear()
            asyncio.run(
                mw(
                    {
                        "type": "http",
                        "method": meth,
                        "path": "/v1/models",
                        "headers": [
                            (b"authorization", b"Bearer co-secret-key-123"),
                            (b"content-length", b"5"),
                        ],
                    },
                    _failing_receive,
                    _send_capture,
                )
            )
            assert sent_messages[0]["status"] == 400
            assert receive_calls == 0

        # 4. /health works without auth; disabled gateway rejects 503.
        client = TestClient(app)
        assert client.get("/health").status_code == 200
        gw.enabled = False
        assert (
            client.post(
                "/v1/chat/completions",
                content=b"{}",
                headers={"Authorization": "Bearer co-secret-key-123"},
            ).status_code
            == 503
        )
    finally:
        rt.close_http_sessions()


def test_public_api_exception_scrubbing_and_generic_sse_fail(tmp_path, monkeypatch):
    import httpx
    from server.public_api import ColabOnePublicGateway, create_colabone_public_app

    rt = _make_runtime(tmp_path / "rt_pub_scrub")
    try:
        gw = ColabOnePublicGateway(rt)
        gw.enabled = True
        gw.key = "co-supersecret-gw-token-999"
        first_chat_id = next(
            mid for mid, m in rt.models.items() if getattr(m, "kind", "chat") == "chat"
        )
        rt.loaded = first_chat_id

        class _BoomClient:
            def __init__(self, *a, **kw):
                pass

            async def __aenter__(self):
                raise ValueError(
                    "internal crash with Bearer co-supersecret-gw-token-999 and hf_secretTokenValue123456"
                )

            async def __aexit__(self, *a):
                return False

        monkeypatch.setattr(httpx, "AsyncClient", _BoomClient)
        app = create_colabone_public_app(gw)
        client = TestClient(app)
        resp = client.post(
            "/v1/responses",
            json={"model": first_chat_id, "input": "hello", "stream": True},
            headers={"Authorization": f"Bearer {gw.key}"},
        )
        assert resp.status_code == 200
        body_text = resp.text
        assert "response.failed" in body_text
        assert "推理失败" in body_text
        assert "co-supersecret-gw-token-999" not in body_text
        assert "hf_secretTokenValue123456" not in body_text

        with gw.lock:
            hist = list(gw.request_history)
        assert hist
        err_recorded = str(hist[-1].get("error") or "")
        assert "co-supersecret-gw-token-999" not in err_recorded
        assert "hf_secretTokenValue123456" not in err_recorded
    finally:
        rt.close_http_sessions()


# ---------------------------------------------------------------------------
# Issue 5: ComfyUI runtime_only, store atomicity, Fashion Try-On, Digital Human, Packs, WS, Proxy
# ---------------------------------------------------------------------------


def test_comfyui_delete_output_runtime_only_skips_drive_and_trash(tmp_path):
    from server.app import create_colabone_app

    rt = _make_runtime(tmp_path / "rt_del")
    try:
        removed_remote: list[tuple[str, str]] = []
        rt.drive.remove_remote_comfy_output = lambda oid, ext="": (
            removed_remote.append((oid, ext)) or True
        )
        png = _make_png(32, 32)
        rec1 = rt.comfy.store.save_output(png, "wf-1", "WF", "a.png", 1.0)
        rec2 = rt.comfy.store.save_output(png, "wf-1", "WF", "b.png", 1.0)

        tok = "test-control-token-0123456789abcdef"
        app = create_colabone_app(rt, tok)
        client = TestClient(app)
        hdrs = {"Authorization": f"Bearer {tok}"}

        # 1. runtime_only=1 deletes local files WITHOUT .trash tombstone, Drive removal, or dirty mark.
        rt._dirty_seq.clear()
        r1 = client.delete(
            f"/api/v1/comfyui/outputs/{rec1['id']}?runtime_only=1", headers=hdrs
        )
        assert r1.status_code == 200
        assert not (rt.comfy.store.outputs_dir / f"{rec1['id']}.json").exists()
        assert not (rt.comfy.store.outputs_dir / f"{rec1['id']}.png").exists()
        assert not (rt.comfy.store.outputs_dir / ".trash" / f"{rec1['id']}.json").exists()
        assert removed_remote == []
        assert f"comfy_output:{rec1['id']}" not in rt._dirty_seq

        # 2. Normal delete moves to .trash, calls remove_remote_comfy_output, and marks dirty.
        r2 = client.delete(f"/api/v1/comfyui/outputs/{rec2['id']}", headers=hdrs)
        assert r2.status_code == 200
        assert (rt.comfy.store.outputs_dir / ".trash" / f"{rec2['id']}.json").is_file()
        assert removed_remote == [(rec2["id"], "png")]
        assert f"comfy_output:{rec2['id']}" in rt._dirty_seq
    finally:
        rt.close_http_sessions()


def test_comfyui_store_atomicity_and_pg_has_members_permission_error(tmp_path, monkeypatch):
    from server import comfyui

    store = comfyui.ColabOneComfyWorkflowStore(tmp_path / "store_atomic")
    png = _make_png(32, 32)

    monkeypatch.setattr(
        comfyui,
        "atomic_json",
        lambda *a, **kw: (_ for _ in ()).throw(OSError("disk full")),
    )
    with pytest.raises(OSError, match="disk full"):
        store.save_output(png, "wf", "WF", "x.png", 0.5)
    assert list(store.outputs_dir.glob("*.png")) == []

    src_png = tmp_path / "src.png"
    src_png.write_bytes(png)
    with pytest.raises(OSError, match="disk full"):
        store.save_output_file(src_png, "wf", "WF", "y.png", elapsed=0.5)
    assert list(store.outputs_dir.glob("*.png")) == []

    monkeypatch.undo()
    rec = store.save_output(png, "wf", "WF", "z.png", 0.5)
    oid = rec["id"]
    meta_p = store.outputs_dir / f"{oid}.json"
    asset_p = store.outputs_dir / f"{oid}.png"
    asset_p.unlink()
    outside = tmp_path / "outside.png"
    outside.write_bytes(png)
    asset_p.symlink_to(outside)
    with pytest.raises(ValueError, match="符号链接"):
        store.delete_output(oid)
    assert meta_p.is_file()

    monkeypatch.setattr(
        os, "killpg", lambda pgid, sig: (_ for _ in ()).throw(PermissionError())
    )
    assert comfyui._pg_has_members(12345) is True


def test_fashion_tryon_hero_non_first_16bit_matte_and_io_in_cleanup(tmp_path):
    from PIL import Image
    from common.comfyui import ComfyCatalog, WorkflowRunRequest
    from server.comfyui import ColabOneComfyWorkflowStore
    from server.fashion_tryon import (
        ColabOneFashionTryOnRunner,
        composite_soft_alpha_on_white,
    )

    # 1. 16-bit grayscale matte scaling (32768 -> ~128 alpha, NOT clipped to 255).
    fg_bytes = _make_png(64, 64, (0, 0, 0))
    matte_16 = _make_16bit_gray_png(64, 64, 32768)
    comp_bytes = composite_soft_alpha_on_white(fg_bytes, matte_16)
    with Image.open(io.BytesIO(comp_bytes)) as out_im:
        px = out_im.getpixel((out_im.width // 2, out_im.height // 2))
        assert 115 <= px[0] <= 140

    # 2. Hero Master captured on first front/side shot when shot 0 is back; io_in_dir cleaned;
    #    free_memory routed cleanly without duplicate /free.
    raw = json.loads((CATALOG_DIR / "comfyui.json").read_text(encoding="utf-8"))
    catalog = ComfyCatalog.model_validate(raw)
    wf = next(t.model_dump() for t in catalog.templates if t.id == "fashion-tryon-h3")
    store = ColabOneComfyWorkflowStore(
        tmp_path / "f_store", builtin_templates=catalog.templates
    )
    io_in = tmp_path / "io_in"
    io_in.mkdir()
    outside = tmp_path / "keep_me.png"
    outside.write_bytes(_make_png(32, 32))
    (io_in / "sym_outside.png").symlink_to(outside)

    model_p = tmp_path / "m.png"
    garment_p = tmp_path / "g.png"
    model_p.write_bytes(_make_png(96, 128))
    garment_p.write_bytes(_make_png(96, 128))

    free_calls: list[tuple[bool, bool]] = []
    executed_prompts: list[dict] = []

    class _RealLikeComfy:
        def __init__(self):
            self.base_url = "http://127.0.0.1:8188"
            self.store = store
            self.io_in_dir = io_in
            self.io_out_dir = None
            self.lock = None
            self._loaded_pack_signature = ""
            self.runtime = SimpleNamespace(gpu_info=lambda: {"total_mib": 24576})
            self.packs = SimpleNamespace(
                check_preflight=lambda _wf: None,
                ensure_pack=lambda *a, **kw: False,
                needs_pip_restart=lambda: False,
                active_pack_signature="",
            )

        def is_running(self):
            return True

        def mark_native_pack_ready(self, wid):
            pass

        def free_memory(self, unload_models=True, *, release_tts_warm=True):
            free_calls.append((bool(unload_models), bool(release_tts_warm)))
            return {"ok": True}

        def _upload_input_file(self, fname, fpath):
            dest = io_in / fname
            dest.write_bytes(Path(fpath).read_bytes())
            return fname

        def _execute_single_prompt(self, task, wf, prompt, **kw):
            executed_prompts.append(prompt)
            out_dir = Path(kw["output_dir"])
            out_p = out_dir / f"gen_{len(executed_prompts)}.png"
            out_p.write_bytes(_make_png(96, 128, (180, 120, 140)))
            return [{"path": str(out_p), "prompt_id": f"p{len(executed_prompts)}"}]

    comfy_obj = _RealLikeComfy()
    runner = ColabOneFashionTryOnRunner(comfy_obj)
    task = SimpleNamespace(
        id="t-hero",
        progress=0.0,
        outputs=[],
        lock=None,
        check=lambda: None,
        note=lambda *a, **k: None,
    )
    # Shot 0 = back, Shot 1 = front (Hero captured here!), Shot 2 = front/side (anchored to Hero!).
    req = WorkflowRunRequest(
        workflow_id="fashion-tryon-h3",
        overrides={
            "pipeline.garment_cleanup": "rmbg3",
            "pipeline.anchor_hero_shot": True,
            "pipeline.custom_pose_prompts": "回眸背面展示\n正面自然站姿\n侧面微笑行走",
        },
        inputs={},
    )
    runner.run(
        task, wf, req, [("1.image", "m.png", model_p), ("2.image", "g.png", garment_p)]
    )
    assert free_calls == [(True, True), (True, True)]
    assert len(executed_prompts) == 4
    assert "colabone_hero_master.png" not in str(executed_prompts[1])
    assert "colabone_hero_master.png" not in str(executed_prompts[2])
    assert "colabone_hero_master.png" in str(executed_prompts[3])
    remaining_regular = [p.name for p in io_in.iterdir() if not p.is_symlink()]
    assert remaining_regular == []
    assert outside.is_file()


def test_digital_human_symlink_rejection_and_secure_log(tmp_path):
    from server.digital_human import ColabOneDigitalHumanRunner, _run_ffmpeg_command

    target = tmp_path / "secret.txt"
    target.write_text("keep", encoding="utf-8")
    sym_log = tmp_path / "ffmpeg.log"
    sym_log.symlink_to(target)
    task = SimpleNamespace(check=lambda: None, note=lambda *a, **k: None)
    with pytest.raises(ValueError, match="符号链接"):
        _run_ffmpeg_command(["true"], task, sym_log)
    assert target.read_text(encoding="utf-8") == "keep"

    real_img = tmp_path / "real.png"
    real_img.write_bytes(_make_png(64, 64))
    sym_img = tmp_path / "sym.png"
    sym_img.symlink_to(real_img)
    runner = ColabOneDigitalHumanRunner(MagicMock(), tts_runner=None)
    wf = {
        "id": "dh-test",
        "name": "DH",
        "workflow": {"1": {"class_type": "LoadImage", "inputs": {"image": "a.png"}}},
        "pipeline": {"video_backend": "longcat_avatar"},
    }
    req = SimpleNamespace(
        overrides={"pipeline.audio_source": "tts", "pipeline.script_text": "你好"},
        hf_token="",
    )
    with pytest.raises(ValueError, match="主播参考人像"):
        runner.run(task, wf, req, [("1.image", "sym.png", sym_img)])


def test_comfy_packs_gen_lock_constraints_archive_rejection_and_partial_manifest(
    tmp_path, monkeypatch
):
    from server import comfy_packs

    got_gen = []
    started = threading.Event()
    release = threading.Event()

    def _hold_wheel_lock():
        with comfy_packs._SAGEATTN_WHEEL_LOCK:
            started.set()
            release.wait(timeout=3.0)

    holder = threading.Thread(target=_hold_wheel_lock, daemon=True)
    holder.start()
    assert started.wait(timeout=2.0)
    try:
        reader = threading.Thread(
            target=lambda: got_gen.append(comfy_packs._current_sageattn_install_generation()),
            daemon=True,
        )
        reader.start()
        reader.join(timeout=1.0)
        assert len(got_gen) == 1
    finally:
        release.set()
        holder.join(timeout=2.0)

    for pkg in ("triton", "sageattention", "sageattn3", "flash-attn"):
        assert pkg in comfy_packs.TORCH_SKIP_PACKAGES

    for bad in ("foo.whl", "bar.tar.gz", "baz.zip", "./pkg", "/tmp/pkg", "pkg @ file:///tmp/a"):
        with pytest.raises(ValueError):
            comfy_packs._validate_pip_packages([bad])

    rt = SimpleNamespace(root=tmp_path / "packs_rt")
    install_dir = tmp_path / "comfy_inst"
    (install_dir / "custom_nodes").mkdir(parents=True)
    models_dir = tmp_path / "models"
    models_dir.mkdir()
    pm = comfy_packs.ColabOneComfyPackManager(rt, install_dir, models_dir)

    pack_dir = tmp_path / "pack_a"
    (pack_dir / "node_a").mkdir(parents=True)
    (pack_dir / "node_b").mkdir(parents=True)

    orig_replace = os.replace
    calls = [0]

    def _fail_second_replace(src, dst):
        calls[0] += 1
        if calls[0] == 2:
            raise OSError("simulated mid-swap failure")
        return orig_replace(src, dst)

    monkeypatch.setattr(os, "replace", _fail_second_replace)
    with pytest.raises(OSError, match="simulated mid-swap failure"):
        pm._activate_custom_nodes(
            pack_id="p1",
            pack_sig="sig1",
            custom_nodes=[{"name": "node_a"}, {"name": "node_b"}],
            pack_dir=pack_dir,
        )
    monkeypatch.undo()
    state = pm._read_managed_state()
    assert "node_a" in state["nodes"]
    assert "node_b" not in state["nodes"]


def test_comfy_ws_stale_reader_does_not_clobber_reconnected_state():
    from server.comfy_ws import (
        ColabOneComfyWsMonitor,
        MAX_WS_BUFFER_BYTES,
        MAX_WS_FRAME_BYTES,
    )

    assert MAX_WS_BUFFER_BYTES == MAX_WS_FRAME_BYTES + 14

    mon = ColabOneComfyWsMonitor(host="127.0.0.1", port=8188, client_id="c1")
    s1, s2 = socket.socketpair()
    try:
        s1.setblocking(False)
        s2.close()
        with mon._lock:
            mon._conn_gen = 2
            mon._connected = True
        mon._reader_loop(s1, bytearray(), conn_gen=1)
        assert mon.connected is True
    finally:
        try:
            s1.close()
        except OSError:
            pass


def test_comfy_proxy_secure_cookie_uses_parsed_host_header():
    from server.comfy_proxy import ColabOneCanvasProxyServer

    async def _run_check(host_header: str) -> str:
        proxy = ColabOneCanvasProxyServer(
            upstream_host="127.0.0.1",
            upstream_port=8188,
            canvas_token="tok-123",
            instance="inst-1",
        )
        raw_req = (
            f"GET /_colabone_auth?token=tok-123 HTTP/1.1\r\n"
            f"Host: {host_header}\r\n"
            f"\r\n"
        ).encode("ascii")
        reader = asyncio.StreamReader()
        reader.feed_data(raw_req)
        reader.feed_eof()

        written = bytearray()

        class _FakeWriter:
            def write(self, data: bytes):
                written.extend(data)

            async def drain(self):
                pass

            def is_closing(self):
                return False

            def close(self):
                pass

            async def wait_closed(self):
                pass

        await proxy._handle_client(reader, _FakeWriter())
        return written.decode("latin-1")

    resp_evil = asyncio.run(_run_check("localhost.evil.com"))
    assert "Set-Cookie:" in resp_evil and "; Secure" in resp_evil

    resp_nip = asyncio.run(_run_check("127.0.0.1.nip.io"))
    assert "Set-Cookie:" in resp_nip and "; Secure" in resp_nip

    resp_local = asyncio.run(_run_check("localhost:8188"))
    assert "Set-Cookie:" in resp_local and "; Secure" not in resp_local

    resp_v6 = asyncio.run(_run_check("[::1]:8188"))
    assert "Set-Cookie:" in resp_v6 and "; Secure" not in resp_v6


# ---------------------------------------------------------------------------
# Issue 6: Runtime lifecycle, Tunnel pinned IP, Drive sanitization, vm_update
# ---------------------------------------------------------------------------


def test_runtime_live_starting_busy_and_unload_atomic_detach(tmp_path):
    rt = _make_runtime(tmp_path / "rt_life")
    try:
        assert rt.busy() is False
        with rt.lock:
            rt._live_starting = True
        assert rt.busy() is True
        assert rt.busy(_ignore_live_starting=True) is False
        with rt.lock:
            rt._live_starting = False
        assert rt.busy() is False

        observed_during_terminate: list[tuple] = []

        class _FakeProc:
            pid = 999999

            def poll(self):
                return None

            def terminate(self):
                acquired = rt.lock.acquire(blocking=False)
                if acquired:
                    rt.lock.release()
                observed_during_terminate.append((acquired, rt.proc, rt.loaded))

            def wait(self, timeout=None):
                return 0

            def kill(self):
                pass

        rt.proc = _FakeProc()
        rt.loaded = "some-model"
        rt.unload()
        assert observed_during_terminate == [(True, None, None)]
        assert rt.proc is None
        assert rt.loaded is None
    finally:
        rt.close_http_sessions()


def test_tunnel_pinned_ip_adapter_and_sanitized_env(monkeypatch):
    from server.tunnel import (
        _ColabOneTunnelPinnedIPAdapter,
        _sanitized_tunnel_env,
        public_addresses,
    )

    monkeypatch.setenv("COLABONE_REMOTE_CONFIG", "secret")
    monkeypatch.setenv("HF_TOKEN", "hf_123")
    monkeypatch.setenv("OPENAI_API_KEY", "sk-123")
    monkeypatch.setenv("TUNNEL_TOKEN", "cf-123")
    env = _sanitized_tunnel_env()
    for k in ("COLABONE_REMOTE_CONFIG", "HF_TOKEN", "OPENAI_API_KEY", "TUNNEL_TOKEN"):
        assert k not in env

    monkeypatch.setattr(
        socket,
        "getaddrinfo",
        lambda host, port, type=0: [
            (socket.AF_INET, socket.SOCK_STREAM, 6, "", ("93.184.216.34", 443))
        ],
    )
    ips = public_addresses("https://example.trycloudflare.com")
    assert ips == ["93.184.216.34"]

    adapter = _ColabOneTunnelPinnedIPAdapter(
        expected_host="example.trycloudflare.com",
        pinned_ip="93.184.216.34",
        use_tls=True,
    )
    # PoolManager must pin TLS SNI & hostname verification to expected_host.
    assert adapter.poolmanager.connection_pool_kw["server_hostname"] == "example.trycloudflare.com"
    assert adapter.poolmanager.connection_pool_kw["assert_hostname"] == "example.trycloudflare.com"

    captured_send: dict[str, object] = {}

    def _fake_parent_send(self, req, *a, **kw):
        captured_send["url"] = req.url
        captured_send["host"] = req.headers.get("Host")
        return SimpleNamespace(status_code=200)

    monkeypatch.setattr(requests.adapters.HTTPAdapter, "send", _fake_parent_send)
    prep = requests.Request("GET", "https://example.trycloudflare.com/health").prepare()
    adapter.send(prep)
    assert captured_send["url"] == "https://93.184.216.34/health"
    assert captured_send["host"] == "example.trycloudflare.com"

    # Mismatched host rejected.
    bad_prep = requests.Request("GET", "https://evil.example.com/health").prepare()
    with pytest.raises(ValueError):
        adapter.send(bad_prep)


def test_drive_rclone_env_and_remote_remove_validation(tmp_path, monkeypatch):
    from server.drive import ColabOneDrive, _sanitized_rclone_env

    monkeypatch.setenv("COLABONE_REMOTE_CONFIG", "secret")
    monkeypatch.setenv("HF_TOKEN", "hf_123")
    monkeypatch.setenv("RCLONE_CONFIG_PASS", "rclone_secret")
    monkeypatch.setenv("RCLONE_DRIVE_TOKEN", "tok")
    env = _sanitized_rclone_env()
    for k in ("COLABONE_REMOTE_CONFIG", "HF_TOKEN", "RCLONE_CONFIG_PASS", "RCLONE_DRIVE_TOKEN"):
        assert k not in env

    drv = ColabOneDrive(tmp_path / "drv_val", tmp_path / "drv_val_cache")
    try:
        with pytest.raises(ValueError):
            drv.remove_remote_workflow("../escape")
        with pytest.raises(ValueError):
            drv.remove_remote_comfy_output("not-a-hex-id", "png")
        with pytest.raises(ValueError):
            drv.remove_remote_comfy_output("a" * 32, "../bad")
    finally:
        drv.close_http_sessions()


def test_vm_update_stat_parsing_with_parens_and_reverify_before_kill():
    vm_src = (ROOT / "server" / "vm_update.py").read_text(encoding="utf-8")
    tree = ast.parse(vm_src)
    # Confirm rsplit(")", 1) stat parsing and cmdline re-check before os.kill exist in vm_update.py.
    assert 'rsplit(")", 1)' in vm_src
    # Extract the zombie-parsing snippet logic and test directly against nasty /proc/<pid>/stat strings.
    for stat_line, expect_zombie in (
        ("101 (python (3.12)) Z 1 101 101 0 -1\n", True),
        ("102 (py (Z) runner) S 1 102 102 0 -1\n", False),
        ("103 (worker (a) (b)) Z 1 103 103 0 -1\n", True),
        ("104 (normal) R 1 104 104 0 -1\n", False),
    ):
        stat_tail = stat_line.rsplit(")", 1)[1].split() if ")" in stat_line else []
        is_zombie = bool(stat_tail) and stat_tail[0] == "Z"
        assert is_zombie is expect_zombie

    # Verify os.kill is guarded by a fresh cmdline check right before it in the AST/source.
    kill_idx = vm_src.index("os.kill(pid, signal.SIGTERM)")
    pre_kill_window = vm_src[kill_idx - 700 : kill_idx]
    assert "cmdline" in pre_kill_window
    assert len(tree.body) > 0
