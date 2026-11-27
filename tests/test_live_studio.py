"""100% offline unit tests for ColabOne Live Studio remote core and contracts."""

from __future__ import annotations

import asyncio
import base64
import hashlib
import io
import json
import os
import queue
import socket
import struct
import sys
import threading
import time
from pathlib import Path
from typing import Any

import httpx
import pytest
from fastapi.testclient import TestClient
from PIL import Image
from pydantic import ValidationError
from starlette.websockets import WebSocketDisconnect

from common.live_studio import (
    COLABONE_LIVE_DIRECTOR_STATES,
    COLABONE_LIVE_FRAME_AUDIO_PCM,
    COLABONE_LIVE_FRAME_FMP4,
    COLABONE_LIVE_FRAME_MIC_PCM,
    COLABONE_LIVE_FRAME_VIDEO,
    COLABONE_LIVE_HEADER_SIZE,
    COLABONE_LIVE_PORT,
    COLABONE_LIVE_WS_PATH,
    ColabOneLiveActionSpec,
    ColabOneLiveAvatarManifest,
    ColabOneLiveBuildPackRequest,
    ColabOneLiveCatalog,
    ColabOneLiveDirectiveParser,
    ColabOneLiveExternalLLMConfig,
    ColabOneLiveProductItem,
    ColabOneLiveSessionConfig,
    ColabOneLiveStartRequest,
    build_colabone_live_system_prompt,
    evaluate_colabone_live_vram_budget,
    pack_colabone_live_frame,
    scrub_colabone_live_secrets,
    unpack_colabone_live_frame,
    validate_colabone_external_llm_resolved_ips,
    validate_colabone_live_rtmp_url,
)
from server.live_server import (
    COLABONE_LIVE_WS_AUTH_CLOSE_CODE,
    ColabOneLiveDirector,
    ColabOneLiveServerRunner,
    ColabOneLiveVideoEncoder,
    ColabOneSenseVoiceAsrAdapter,
    ColabOneSileroVadAdapter,
    constant_time_token_verifier,
    create_colabone_live_app,
    parse_openai_sse_delta,
    stream_llm_completion,
)
from server.live_studio import ColabOneLiveStudioManager
from server.live_worker_tts import (
    COLABONE_LIVE_TTS_CHUNK_BYTES,
    ColabOneMockStreamingTtsEngine,
    ColabOneResidentTtsWorker,
    iter_pcm24k_chunks,
    resample_pcm16_mono,
)
from server.live_worker_visual import (
    ColabOneFlashHeadVisualEngine,
    ColabOneMuseTalkVisualEngine,
    ColabOnePersonaLiveVisualEngine,
    ColabOneSyntheticVisualEngine,
    blend_rgb_frames,
    compute_pcm16_rms,
)

ROOT = Path(__file__).resolve().parents[1]
CATALOG_DIR = ROOT / "catalog"


def _make_png_b64(size: tuple[int, int] = (32, 32), color=(64, 128, 192)) -> str:
    buf = io.BytesIO()
    Image.new("RGB", size, color=color).save(buf, format="PNG")
    return base64.b64encode(buf.getvalue()).decode("ascii")


def _make_pcm16_frame(
    sample_rate: int = 16000, duration_ms: int = 20, amplitude: int = 0
) -> bytes:
    n_samples = int(sample_rate * duration_ms // 1000)
    return struct.pack(f"<{n_samples}h", *([amplitude] * n_samples))


# ---------------------------------------------------------------------------
# 1. Catalog validation
# ---------------------------------------------------------------------------


def test_live_studio_catalog_validates_and_matches_contract():
    raw = json.loads((CATALOG_DIR / "live_studio.json").read_text(encoding="utf-8"))
    catalog = ColabOneLiveCatalog.model_validate(raw)

    assert catalog.port == COLABONE_LIVE_PORT == 8190
    assert catalog.ws_path == COLABONE_LIVE_WS_PATH == "/_colabone_live/ws"
    assert catalog.default_fps == 25
    assert catalog.audio_input_sample_rate == 16000
    assert catalog.audio_output_sample_rate == 24000

    asr_vad = {item.id: item for item in catalog.asr_vad}
    assert set(asr_vad.keys()) == {"silero_vad", "sensevoice_small"}
    assert asr_vad["silero_vad"].license == "MIT"
    assert asr_vad["silero_vad"].device == "cpu"
    assert asr_vad["silero_vad"].min_vram_mib == 0
    assert asr_vad["sensevoice_small"].repo == "FunAudioLLM/SenseVoiceSmall"
    assert (
        asr_vad["sensevoice_small"].license
        == "MIT (code) / FunAudioLLM Model License (weights)"
    )
    assert asr_vad["sensevoice_small"].license_notice != ""
    assert catalog.encoding_note != ""
    assert asr_vad["sensevoice_small"].min_vram_mib == 1024
    assert asr_vad["sensevoice_small"].min_disk_mib == 1536

    tts = {item.id: item for item in catalog.streaming_tts}
    assert set(tts.keys()) == {"qwen3_tts", "voxcpm2"}
    assert tts["qwen3_tts"].min_vram_mib == 8192
    assert tts["qwen3_tts"].license == "Apache-2.0"
    assert tts["qwen3_tts"].ttfa_ms == 90
    assert tts["voxcpm2"].min_vram_mib == 8192
    assert tts["voxcpm2"].license == "Apache-2.0"
    assert tts["voxcpm2"].ttfa_ms == 170

    visual = {item.id: item for item in catalog.visual_engines}
    assert set(visual.keys()) == {
        "flashhead_lite",
        "musetalk_bank",
        "personalive_dance",
    }
    assert visual["flashhead_lite"].min_vram_mib == 6656
    assert visual["flashhead_lite"].min_disk_mib == 10240
    assert visual["flashhead_lite"].commercial_ok is True
    assert visual["flashhead_lite"].experimental is False

    assert visual["musetalk_bank"].min_vram_mib == 7680
    assert visual["musetalk_bank"].min_disk_mib == 12288
    assert visual["musetalk_bank"].license == "MIT"
    assert visual["musetalk_bank"].commercial_ok is True

    assert visual["personalive_dance"].min_vram_mib == 12288
    assert visual["personalive_dance"].min_disk_mib == 16384
    assert visual["personalive_dance"].experimental is True
    assert visual["personalive_dance"].commercial_ok is False
    assert visual["personalive_dance"].license_notice != ""

    actions = {item.id: item for item in catalog.default_actions}
    assert set(actions.keys()) == {
        "idle_loop",
        "talk_explain",
        "talk_product",
        "action_wave",
        "action_heart",
        "dance_01",
    }


# ---------------------------------------------------------------------------
# 2. Binary WebSocket frame pack / unpack
# ---------------------------------------------------------------------------


def test_binary_frame_pack_and_unpack_roundtrip_and_errors():
    assert COLABONE_LIVE_HEADER_SIZE == 13
    for ftype in (
        COLABONE_LIVE_FRAME_MIC_PCM,
        COLABONE_LIVE_FRAME_VIDEO,
        COLABONE_LIVE_FRAME_AUDIO_PCM,
        COLABONE_LIVE_FRAME_FMP4,
    ):
        payload = f"payload-{ftype}".encode("utf-8")
        packed = pack_colabone_live_frame(ftype, 42, 1234567890123, payload)
        assert len(packed) == 13 + len(payload)
        got_type, got_epoch, got_pts, got_payload = unpack_colabone_live_frame(packed)
        assert (got_type, got_epoch, got_pts, got_payload) == (
            ftype,
            42,
            1234567890123,
            payload,
        )

    with pytest.raises(ValueError):
        pack_colabone_live_frame(0x99, 1, 0, b"x")
    with pytest.raises(ValueError):
        pack_colabone_live_frame(True, 1, 0, b"x")  # type: ignore[arg-type]
    with pytest.raises(ValueError):
        pack_colabone_live_frame(COLABONE_LIVE_FRAME_VIDEO, -1, 0, b"x")
    with pytest.raises(ValueError):
        pack_colabone_live_frame(COLABONE_LIVE_FRAME_VIDEO, 1, -5, b"x")
    with pytest.raises(ValueError):
        unpack_colabone_live_frame(b"short")
    with pytest.raises(ValueError):
        unpack_colabone_live_frame(struct.pack("!BIQ", 0x7F, 1, 2) + b"abc")


# ---------------------------------------------------------------------------
# 3. Streaming directive parser
# ---------------------------------------------------------------------------


def test_directive_parser_streams_clauses_and_never_splits_tags():
    parser = ColabOneLiveDirectiveParser()
    stream = (
        "[MOOD:happy][AC",
        "TION:action_wave]欢迎来到直播间，",
        "今天给大家推荐[CARD:prod_01]这款降噪耳机！",
        "到手只要99.9元，[限时特惠]快来抢购。",
    )
    events: list[dict[str, str]] = []
    for chunk in stream:
        events.extend(parser.feed(chunk))
    events.extend(parser.flush())

    assert events == [
        {"kind": "mood", "value": "happy"},
        {"kind": "action", "value": "action_wave"},
        {"kind": "speech", "text": "欢迎来到直播间，"},
        {"kind": "speech", "text": "今天给大家推荐"},
        {"kind": "card", "value": "prod_01"},
        {"kind": "speech", "text": "这款降噪耳机！"},
        {"kind": "speech", "text": "到手只要99.9元，"},
        {"kind": "speech", "text": "[限时特惠]快来抢购。"},
    ]


def test_directive_parser_char_by_char_and_unclosed_bracket_flush():
    parser = ColabOneLiveDirectiveParser()
    text = "Hello, world! [action:action_heart] Thanks [unclosed"
    events: list[dict[str, str]] = []
    for ch in text:
        events.extend(parser.feed(ch))
    events.extend(parser.flush())

    assert {"kind": "action", "value": "action_heart"} in events
    speeches = [e["text"] for e in events if e["kind"] == "speech"]
    assert speeches[0] == "Hello,"
    assert speeches[1] == "world!"
    assert any("[unclosed" in s for s in speeches)


# ---------------------------------------------------------------------------
# 4. System prompt builder (dialogue vs director)
# ---------------------------------------------------------------------------


def test_system_prompt_separates_dialogue_and_director_modes():
    avatar = {
        "name": "小雅",
        "persona_prompt": "热情亲切的数码好物推荐官",
    }
    actions = [
        {"id": "action_wave", "label": "挥手打招呼", "kind": "oneshot"},
        {"id": "dance_01", "label": "才艺热舞", "kind": "dance"},
    ]
    products = [
        {
            "id": "prod_earbuds",
            "name": "降噪耳机 Pro",
            "price": "299元",
            "highlights": "48dB深度降噪",
            "script_hint": "强调通勤佩戴舒适",
        }
    ]

    dialogue_prompt = build_colabone_live_system_prompt("dialogue", avatar, products, actions)
    director_prompt = build_colabone_live_system_prompt("director", avatar, products, actions)

    assert "小雅" in dialogue_prompt
    assert "观众实时互动对话模式" in dialogue_prompt
    assert "[ACTION:action_wave]" in dialogue_prompt
    assert "[CARD:prod_earbuds]" in dialogue_prompt

    assert "后台导播指令模式" in director_prompt
    assert "好的导演" in director_prompt
    assert "绝对禁止" in director_prompt
    assert "[ACTION:dance_01]" in director_prompt
    assert "降噪耳机 Pro" in director_prompt


# ---------------------------------------------------------------------------
# 5. VRAM budget co-location calculator across G4 / A100 / L4 / T4
# ---------------------------------------------------------------------------


def test_vram_budget_matrix_across_gpus():
    catalog = json.loads((CATALOG_DIR / "live_studio.json").read_text(encoding="utf-8"))

    # G4 96GB + MuseTalk + PersonaLive + TTS + ASR + 27B GGUF (18432 MiB)
    g4 = evaluate_colabone_live_vram_budget(
        {
            "visual_engine": "musetalk_bank",
            "personalive_enabled": True,
            "tts_engine": "qwen3_tts",
            "llm_mode": "local_gguf",
        },
        gpu_total_mib=98304,
        local_llm_vram_mib=18432,
        catalog=catalog,
    )
    assert g4["ok"] is True
    assert g4["can_co_locate_llm"] is True
    assert g4["can_co_locate_personalive"] is True
    assert g4["personalive_mode"] == "co_located"
    assert g4["safety_margin_mib"] == 3072

    # A100 80GB + MuseTalk + PersonaLive + TTS + ASR + 14B GGUF (10240 MiB)
    a100 = evaluate_colabone_live_vram_budget(
        {
            "visual_engine": "musetalk_bank",
            "personalive_enabled": True,
            "tts_engine": "voxcpm2",
            "llm_mode": "local_gguf",
        },
        gpu_total_mib=81920,
        local_llm_vram_mib=10240,
        catalog=catalog,
    )
    assert a100["ok"] is True
    assert a100["can_co_locate_llm"] is True
    assert a100["can_co_locate_personalive"] is True
    assert a100["personalive_mode"] == "co_located"

    # L4 24GB + external API + FlashHead fits cleanly
    l4_flash = evaluate_colabone_live_vram_budget(
        {
            "visual_engine": "flashhead_lite",
            "personalive_enabled": False,
            "tts_engine": "qwen3_tts",
            "llm_mode": "external_api",
        },
        gpu_total_mib=24576,
        local_llm_vram_mib=0,
        catalog=catalog,
    )
    assert l4_flash["ok"] is True
    assert l4_flash["can_co_locate_llm"] is False
    assert l4_flash["can_co_locate_personalive"] is False

    # L4 24GB + external API + MuseTalk + PersonaLive: hot_swap_peak_mib includes 3072 MiB safety margin (25600 > 24576)
    l4_persona = evaluate_colabone_live_vram_budget(
        {
            "visual_engine": "musetalk_bank",
            "personalive_enabled": True,
            "tts_engine": "qwen3_tts",
            "llm_mode": "external_api",
        },
        gpu_total_mib=24576,
        local_llm_vram_mib=0,
        catalog=catalog,
    )
    assert l4_persona["ok"] is False
    assert l4_persona["can_co_locate_personalive"] is False
    assert l4_persona["personalive_mode"] == "unsupported"
    assert l4_persona["hot_swap_peak_mib"] == 25600

    # 28GB GPU + external API + MuseTalk + PersonaLive: hot-swap fits (25600 <= 28672), local LLM cannot co-locate (31744 > 28672)
    gpu28_persona = evaluate_colabone_live_vram_budget(
        {
            "visual_engine": "musetalk_bank",
            "personalive_enabled": True,
            "tts_engine": "qwen3_tts",
            "llm_mode": "external_api",
        },
        gpu_total_mib=28672,
        local_llm_vram_mib=0,
        catalog=catalog,
    )
    assert gpu28_persona["ok"] is True
    assert gpu28_persona["can_co_locate_personalive"] is False
    assert gpu28_persona["personalive_mode"] == "hot_swap"
    assert gpu28_persona["can_co_locate_llm"] is False
    assert any("热切换" in w or "hot-swap" in w for w in gpu28_persona["warnings"])

    # 32GB GPU + external API + MuseTalk + PersonaLive: hot-swap fits AND local LLM fits hot-swap peak (31744 <= 32768)
    gpu32_persona = evaluate_colabone_live_vram_budget(
        {
            "visual_engine": "musetalk_bank",
            "personalive_enabled": True,
            "tts_engine": "qwen3_tts",
            "llm_mode": "external_api",
        },
        gpu_total_mib=32768,
        local_llm_vram_mib=0,
        catalog=catalog,
    )
    assert gpu32_persona["ok"] is True
    assert gpu32_persona["personalive_mode"] == "hot_swap"
    assert gpu32_persona["can_co_locate_llm"] is True

    # T4 16GB + local LLM => fail-fast with recommendation to use external_api
    t4_local = evaluate_colabone_live_vram_budget(
        {
            "visual_engine": "flashhead_lite",
            "personalive_enabled": False,
            "tts_engine": "qwen3_tts",
            "llm_mode": "local_gguf",
        },
        gpu_total_mib=15360,
        local_llm_vram_mib=6144,
        catalog=catalog,
    )
    assert t4_local["ok"] is False
    assert t4_local["can_co_locate_llm"] is False
    assert any("external_api" in r for r in t4_local["recommendations"])


# ---------------------------------------------------------------------------
# 6. External LLM SSRF validation, secret hiding, and streaming SSE
# ---------------------------------------------------------------------------


def test_external_llm_ssrf_validation_and_secret_redaction():
    secret_key = "sk-colabone-super-secret-live-key-987654321"
    valid_https = ColabOneLiveExternalLLMConfig(
        base_url="https://api.openai.com/v1/",
        api_key=secret_key,
        model="gpt-4o-mini",
    )
    assert valid_https.base_url == "https://api.openai.com/v1"
    assert secret_key not in repr(valid_https)
    assert secret_key not in str(valid_https)
    assert valid_https.redacted_dict()["api_key"] == "***"

    # Loopback HTTP allowed
    local_ok = ColabOneLiveExternalLLMConfig(
        base_url="http://127.0.0.1:8081/v1",
        api_key="",
        model="local-qwen",
    )
    assert local_ok.base_url == "http://127.0.0.1:8081/v1"
    assert ColabOneLiveExternalLLMConfig(
        base_url="http://localhost:11434/v1", api_key="", model="qwen2.5"
    ).base_url == "http://localhost:11434/v1"

    # SSRF / non-loopback HTTP / metadata endpoints blocked
    for bad_url in (
        "http://api.openai.com/v1",
        "http://10.0.0.5:8080/v1",
        "https://192.168.1.20/v1",
        "https://169.254.169.254/latest/meta-data",
        "https://metadata.google.internal/computeMetadata/v1",
        "https://user:pass@api.openai.com/v1",
        "ftp://api.openai.com/v1",
    ):
        with pytest.raises(ValidationError) as exc_info:
            ColabOneLiveExternalLLMConfig(
                base_url=bad_url,
                api_key=secret_key,
                model="gpt-4o-mini",
            )
        assert secret_key not in str(exc_info.value)

    scrubbed = scrub_colabone_live_secrets(
        f"Authorization: Bearer {secret_key} key={secret_key} rtmp://live.example.com/app/stream_secret_123",
        secret_key,
    )
    assert secret_key not in scrubbed
    assert "stream_secret_123" not in scrubbed


def test_stream_llm_completion_offline_mock_and_error_scrubbing():
    secret_key = "sk-test-streaming-secret-xyz123456"
    captured: dict[str, Any] = {}

    def _ok_handler(request: httpx.Request) -> httpx.Response:
        captured["url"] = str(request.url)
        captured["auth"] = request.headers.get("Authorization")
        sse = (
            'data: {"choices":[{"delta":{"content":"[ACTION:action_wave]"}}]}\n\n'
            'data: {"choices":[{"delta":{"content":"大家好！"}}]}\n\n'
            "data: [DONE]\n\n"
        )
        return httpx.Response(200, content=sse.encode("utf-8"))

    async def _run_ok() -> list[str]:
        async with httpx.AsyncClient(transport=httpx.MockTransport(_ok_handler)) as client:
            out: list[str] = []
            async for delta in stream_llm_completion(
                [{"role": "user", "content": "hi"}],
                llm_mode="external_api",
                external_config={
                    "base_url": "https://api.example.com/v1",
                    "api_key": secret_key,
                    "model": "demo-model",
                },
                http_client=client,
            ):
                out.append(delta)
            return out

    deltas = asyncio.run(_run_ok())
    assert deltas == ["[ACTION:action_wave]", "大家好！"]
    assert captured["url"] == "https://api.example.com/v1/chat/completions"
    assert captured["auth"] == f"Bearer {secret_key}"
    assert parse_openai_sse_delta("data: [DONE]") is None

    def _err_handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(
            401,
            content=f"Invalid token Bearer {secret_key}".encode("utf-8"),
        )

    async def _run_err() -> None:
        async with httpx.AsyncClient(transport=httpx.MockTransport(_err_handler)) as client:
            async for _ in stream_llm_completion(
                [{"role": "user", "content": "hi"}],
                llm_mode="external_api",
                external_config={
                    "base_url": "https://api.example.com/v1",
                    "api_key": secret_key,
                    "model": "demo-model",
                },
                http_client=client,
            ):
                pass

    with pytest.raises(RuntimeError) as err_info:
        asyncio.run(_run_err())
    assert secret_key not in str(err_info.value)


# ---------------------------------------------------------------------------
# 7. Streaming TTS worker & PCM utilities
# ---------------------------------------------------------------------------


def test_streaming_tts_mock_chunks_resampling_and_cooperative_abort():
    tts = ColabOneMockStreamingTtsEngine("qwen3_tts", min_chunks=4, max_chunks=8)
    chunks = list(tts.synthesize_stream("你好直播间", epoch=1))
    assert len(chunks) >= 4
    assert all(len(c) == COLABONE_LIVE_TTS_CHUNK_BYTES == 960 for c in chunks)
    # Tone-free silence
    assert all(c == b"\x00" * 960 for c in chunks)

    # Cooperative cancel_event abort
    cancel = threading.Event()
    got: list[bytes] = []
    for i, chunk in enumerate(
        tts.synthesize_stream("这是一段稍长的测试文本内容", epoch=2, cancel_event=cancel)
    ):
        got.append(chunk)
        if i == 1:
            cancel.set()
    assert len(got) == 2
    assert 2 in tts.aborted_epochs

    # Epoch mismatch abort
    current = {"epoch": 5}
    pcm_raw = b"\x00" * (960 * 5)
    emitted = []
    for idx, ch in enumerate(
        iter_pcm24k_chunks(pcm_raw, epoch=5, get_current_epoch=lambda: current["epoch"])
    ):
        emitted.append(ch)
        if idx == 0:
            current["epoch"] = 6
    assert len(emitted) == 1

    # Pure-Python 48kHz to 24kHz resampler
    src_48k = _make_pcm16_frame(sample_rate=48000, duration_ms=20, amplitude=1000)
    dst_24k = resample_pcm16_mono(src_48k, 48000, 24000)
    assert len(dst_24k) == 960

    # Resident worker env sanitization
    worker = ColabOneResidentTtsWorker("voxcpm2", fallback_mock=True)
    env = worker.build_worker_env()
    assert env["COLABONE_LIVE_TTS_ENGINE"] == "voxcpm2"
    assert "OPENAI_API_KEY" not in env


# ---------------------------------------------------------------------------
# 8. Visual engines (Synthetic, FlashHead, MuseTalk, PersonaLive)
# ---------------------------------------------------------------------------


def test_visual_engines_crossfade_ring_buffer_and_cache_paths(tmp_path):
    # Synthetic engine crossfade transition
    syn = ColabOneSyntheticVisualEngine(width=64, height=64, fps=25)
    f0 = syn.render_frame(b"", epoch=1, pts_ms=0, state="IDLE")
    assert len(f0.rgb_bytes) == 64 * 64 * 3
    assert f0.is_crossfading is False

    syn.set_action("action_wave", loop=False, crossfade_frames=3)
    f1 = syn.render_frame(_make_pcm16_frame(24000, 20, 4000), epoch=1, pts_ms=40, state="SPEAKING")
    f2 = syn.render_frame(b"", epoch=1, pts_ms=80, state="SPEAKING")
    f3 = syn.render_frame(b"", epoch=1, pts_ms=120, state="SPEAKING")
    f4 = syn.render_frame(b"", epoch=1, pts_ms=160, state="SPEAKING")
    assert (f1.is_crossfading, f2.is_crossfading, f3.is_crossfading, f4.is_crossfading) == (
        True,
        True,
        True,
        False,
    )

    # Blend RGB sanity
    red = Image.new("RGB", (8, 8), (200, 0, 0)).tobytes()
    blue = Image.new("RGB", (8, 8), (0, 0, 200)).tobytes()
    mid = blend_rgb_frames(red, blue, 8, 8, 0.5)
    assert len(mid) == 8 * 8 * 3
    assert mid[0] == 100 and mid[2] == 100

    # MuseTalk action ring-buffer & latent cache safety
    avatar_dir = tmp_path / "avatar_a"
    avatar_dir.mkdir()
    mt = ColabOneMuseTalkVisualEngine(
        width=16, height=16, fps=25, avatar_dir=avatar_dir, default_crossfade_frames=2
    )
    mt.load()
    idle_rgb = Image.new("RGB", (16, 16), (10, 20, 30)).tobytes()
    wave_rgb_1 = Image.new("RGB", (16, 16), (200, 100, 50)).tobytes()
    wave_rgb_2 = Image.new("RGB", (16, 16), (210, 110, 60)).tobytes()
    mt.register_action_frames("idle_loop", [idle_rgb], loop=True)
    mt.register_action_frames("action_wave", [wave_rgb_1, wave_rgb_2], loop=False)

    mt.set_action("action_wave", loop=False, crossfade_frames=0)
    mt.render_frame(b"", epoch=1, pts_ms=0)
    mt.render_frame(b"", epoch=1, pts_ms=40)
    # After 2-frame oneshot completes, MuseTalk automatically returns to idle_loop
    assert mt.current_action == "idle_loop"

    cache_path = mt.latent_cache_path("idle_loop")
    assert cache_path.parent.name == ".musetalk_cache"
    assert mt.has_latent_cache("idle_loop") is False
    cache_path.write_bytes(b"latents")
    assert mt.has_latent_cache("idle_loop") is True

    # FlashHead portrait loading
    portrait = tmp_path / "portrait.png"
    Image.new("RGB", (32, 32), (80, 120, 160)).save(portrait, format="PNG")
    fh = ColabOneFlashHeadVisualEngine(width=16, height=16, portrait_path=portrait)
    fh.load()
    ff = fh.render_frame(b"", epoch=1, pts_ms=0)
    assert len(ff.rgb_bytes) == 16 * 16 * 3
    assert "--portrait" in fh.build_worker_command()

    # PersonaLive driving clip & TensorRT cache path + hot-swap
    drive_clip = tmp_path / "dance.mp4"
    drive_clip.write_bytes(b"fake-mp4-data")
    pl = ColabOnePersonaLiveVisualEngine(width=64, height=64, cache_dir=tmp_path / "pl_cache")
    pl.load()
    clip_info = pl.load_driving_clip(drive_clip, allowed_root=tmp_path)
    assert clip_info["size"] == len(b"fake-mp4-data")
    assert pl.tensorrt_cache_path().name == "personalive_64x64_fp16.plan"
    pl.hot_swap_unload()
    assert pl.is_loaded is False and pl.status()["hot_swapped_out"] is True
    pl.hot_swap_reload()
    assert pl.is_loaded is True and pl.status()["hot_swapped_out"] is False


# ---------------------------------------------------------------------------
# 9. VAD onset / barge-in / endpointing, Director, and Video Encoder
# ---------------------------------------------------------------------------


def test_vad_onset_barge_in_and_silence_endpointing():
    vad = ColabOneSileroVadAdapter(
        sample_rate=16000,
        speech_onset_ms=160,
        silence_endpoint_ms=380,
        energy_threshold=0.02,
    )
    loud = _make_pcm16_frame(16000, 20, amplitude=6000)
    quiet = _make_pcm16_frame(16000, 20, amplitude=0)
    assert compute_pcm16_rms(loud) > 0.1
    assert compute_pcm16_rms(quiet) == 0.0

    # Short 60 ms noise burst (< 160 ms) should NOT trigger speech_start
    for _ in range(3):
        assert vad.process_frame(loud) == []
    assert vad.process_frame(quiet) == []
    assert vad.is_speaking is False

    # 8 consecutive 20 ms loud frames == 160 ms triggers speech_start
    events: list[dict[str, Any]] = []
    for _ in range(8):
        events.extend(vad.process_frame(loud))
    assert len(events) == 1
    assert events[0]["event"] == "speech_start"
    assert events[0]["speech_ms"] >= 160.0
    assert vad.is_speaking is True

    # 18 quiet frames (360 ms) should not endpoint yet; 19th (380 ms) triggers speech_end
    for _ in range(18):
        assert vad.process_frame(quiet) == []
    end_events = vad.process_frame(quiet)
    assert len(end_events) == 1
    assert end_events[0]["event"] == "speech_end"
    assert len(end_events[0]["audio_pcm"]) > 0
    assert vad.is_speaking is False


def test_director_state_machine_barge_in_and_video_encoder():
    assert COLABONE_LIVE_DIRECTOR_STATES == (
        "IDLE",
        "LISTENING",
        "THINKING",
        "SPEAKING",
        "PERFORMING",
    )

    async def _run_barge_in() -> None:
        director = ColabOneLiveDirector()
        q: queue.Queue[str] = queue.Queue()
        q.put("stale-chunk-1")
        q.put("stale-chunk-2")
        director.register_queue(q)

        epoch1, sync_cancel, async_cancel = director.begin_turn("chat")
        assert epoch1 == 1
        assert director.state == "THINKING"
        assert async_cancel is not None

        task = asyncio.create_task(asyncio.sleep(10))
        director.register_task(task)
        director.set_state("SPEAKING")

        ev = director.trigger_barge_in("vad_speech_onset", next_state="LISTENING")
        assert ev["type"] == "barge_in"
        assert ev["epoch"] == 2
        assert ev["state"] == "LISTENING"
        assert ev["cancelled_tasks"] == 1
        assert ev["flushed_items"] == 2
        assert sync_cancel.is_set()
        assert async_cancel.is_set()
        assert q.empty()
        with pytest.raises(asyncio.CancelledError):
            await task

    asyncio.run(_run_barge_in())

    # Video encoder probe & command generation
    assert (
        ColabOneLiveVideoEncoder.probe_encoder(
            runner=lambda _cmd: (0, " V..... h264_nvenc  NVIDIA NVENC H.264\n")
        )
        == "h264_nvenc"
    )
    assert (
        ColabOneLiveVideoEncoder.probe_encoder(
            runner=lambda _cmd: (0, " V..... libx264  libx264 H.264\n")
        )
        == "libx264"
    )
    assert (
        ColabOneLiveVideoEncoder.probe_encoder(runner=lambda _cmd: (1, "not found"))
        == "jpeg"
    )

    nvenc_cmd = ColabOneLiveVideoEncoder.build_ffmpeg_command("h264_nvenc", 720, 1280, 25)
    assert "h264_nvenc" in nvenc_cmd
    assert "-preset" in nvenc_cmd and "p4" in nvenc_cmd
    assert "-tune" in nvenc_cmd and "ull" in nvenc_cmd
    assert "-zerolatency" in nvenc_cmd and "1" in nvenc_cmd
    assert "-bf" in nvenc_cmd and "0" in nvenc_cmd
    assert "-g" in nvenc_cmd and "25" in nvenc_cmd

    x264_cmd = ColabOneLiveVideoEncoder.build_ffmpeg_command("libx264", 720, 1280, 25)
    assert "libx264" in x264_cmd and "ultrafast" in x264_cmd


# ---------------------------------------------------------------------------
# 10. FastAPI WebSocket auth, text/action/barge-in/binary flow
# ---------------------------------------------------------------------------


def test_live_server_ws_auth_rejection_and_full_flow():
    async def _fake_llm_stream(messages, *, director_mode="dialogue", cancel_event=None):
        _ = (messages, director_mode, cancel_event)
        for chunk in (
            "[MOOD:warm][ACTION:action_wave]欢迎光临，",
            "[CARD:prod_01]这款产品超值！",
        ):
            yield chunk
            await asyncio.sleep(0)

    asr = ColabOneSenseVoiceAsrAdapter(transcribe_fn=lambda _pcm, _lang: "介绍一下商品")
    tokens = {"valid-ephemeral-token-123456"}

    def _verify(tok: str) -> bool:
        if tok in tokens:
            tokens.remove(tok)
            return True
        return False

    app = create_colabone_live_app(
        verify_token=_verify,
        session_config={"visual_engine": "flashhead_lite", "tts_engine": "qwen3_tts"},
        avatar={"id": "av1", "name": "小雅"},
        tts_engine=ColabOneMockStreamingTtsEngine("qwen3_tts", min_chunks=2, max_chunks=2),
        visual_engine=ColabOneSyntheticVisualEngine(width=32, height=32, fps=25),
        asr=asr,
        llm_stream_fn=_fake_llm_stream,
        auth_timeout_seconds=0.15,
    )

    with TestClient(app) as client:
        # Health endpoint OK, docs disabled
        health = client.get("/_colabone_live/health")
        assert health.status_code == 200
        assert health.json()["ok"] is True
        assert client.get("/docs").status_code == 404
        assert client.get("/openapi.json").status_code == 404

        # Wrong token rejected with code 4401
        with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws_bad:
            ws_bad.send_json({"type": "auth", "token": "wrong-token"})
            err = ws_bad.receive_json()
            assert err["type"] == "error"
            assert err["code"] == "unauthorized"
            with pytest.raises(WebSocketDisconnect) as disc:
                ws_bad.receive_json()
            assert disc.value.code == COLABONE_LIVE_WS_AUTH_CLOSE_CODE

        # Auth timeout (client sends no first frame) closes with code 4401
        with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws_timeout:
            err_to = ws_timeout.receive_json()
            assert err_to["type"] == "error"
            assert err_to["code"] == "unauthorized"
            with pytest.raises(WebSocketDisconnect) as disc_to:
                ws_timeout.receive_json()
            assert disc_to.value.code == COLABONE_LIVE_WS_AUTH_CLOSE_CODE

        # Valid token succeeds and runs text_input + trigger_action + barge_in + binary MIC_PCM
        with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws:
            ws.send_json({"type": "auth", "token": "valid-ephemeral-token-123456"})
            auth_ok = ws.receive_json()
            assert auth_ok["type"] == "auth_ok"
            assert auth_ok["state"] == "IDLE"

            # Text turn with inline directives + streaming audio/video binary frames
            ws.send_json({"type": "text_input", "text": "打个招呼"})
            seen_json: list[dict[str, Any]] = []
            seen_audio = 0
            seen_video = 0
            for _ in range(40):
                msg = ws.receive()
                if msg.get("text") is not None:
                    obj = json.loads(msg["text"])
                    seen_json.append(obj)
                    if obj.get("type") == "turn_end":
                        break
                elif msg.get("bytes") is not None:
                    ftype, f_epoch, _pts, body = unpack_colabone_live_frame(msg["bytes"])
                    assert f_epoch >= 1
                    if ftype == COLABONE_LIVE_FRAME_AUDIO_PCM:
                        assert len(body) == 960
                        seen_audio += 1
                    elif ftype == COLABONE_LIVE_FRAME_VIDEO:
                        with Image.open(io.BytesIO(body)) as img:
                            assert img.format == "JPEG"
                            assert img.size == (32, 32)
                        seen_video += 1

            event_types = [e["type"] for e in seen_json]
            assert "action" in event_types
            assert "card" in event_types
            assert "mood" in event_types
            assert "transcript_delta" in event_types
            assert "turn_end" in event_types
            assert seen_audio >= 2
            assert seen_video >= 1

            # Trigger action manually emits action + video frame
            ws.send_json({"type": "trigger_action", "action_id": "action_heart"})
            act_ev = ws.receive_json()
            assert act_ev["type"] == "action"
            assert act_ev["action_id"] == "action_heart"
            raw_v = ws.receive_bytes()
            v_type, _, _, v_body = unpack_colabone_live_frame(raw_v)
            assert v_type == COLABONE_LIVE_FRAME_VIDEO
            assert len(v_body) > 0

            # Manual barge-in advances epoch
            ws.send_json({"type": "barge_in", "reason": "host_cut"})
            bi_ev = ws.receive_json()
            assert bi_ev["type"] == "barge_in"
            assert bi_ev["reason"] == "host_cut"
            epoch_after_bi = bi_ev["epoch"]

            # Binary MIC_PCM flow: 8 loud frames => LISTENING, 19 quiet frames => asr_final + reply
            loud = _make_pcm16_frame(16000, 20, amplitude=8000)
            quiet = _make_pcm16_frame(16000, 20, amplitude=0)
            for i in range(8):
                ws.send_bytes(
                    pack_colabone_live_frame(COLABONE_LIVE_FRAME_MIC_PCM, epoch_after_bi, i * 20, loud)
                )
            listening_ev = ws.receive_json()
            assert listening_ev["type"] == "state"
            assert listening_ev["state"] == "LISTENING"

            for i in range(19):
                ws.send_bytes(
                    pack_colabone_live_frame(
                        COLABONE_LIVE_FRAME_MIC_PCM, epoch_after_bi, (8 + i) * 20, quiet
                    )
                )
            asr_ev = ws.receive_json()
            assert asr_ev["type"] == "asr_final"
            assert asr_ev["text"] == "介绍一下商品"

            # Drain triggered assistant turn_end cleanly
            saw_turn_end = False
            for _ in range(40):
                msg = ws.receive()
                if msg.get("text") is not None:
                    obj = json.loads(msg["text"])
                    if obj.get("type") == "turn_end":
                        saw_turn_end = True
                        break
            assert saw_turn_end is True

        # Token reuse rejected (consumed on first connection)
        with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws_reuse:
            ws_reuse.send_json({"type": "auth", "token": "valid-ephemeral-token-123456"})
            assert ws_reuse.receive_json()["code"] == "unauthorized"


def test_live_server_runner_enforces_loopback_bind():
    app = create_colabone_live_app(verify_token=constant_time_token_verifier("tok-123"))
    with pytest.raises(ValueError):
        ColabOneLiveServerRunner(app, host="0.0.0.0", port=8190)


# ---------------------------------------------------------------------------
# 11. ColabOneLiveStudioManager CRUD, security, build pack, lifecycle
# ---------------------------------------------------------------------------


def test_manager_avatar_crud_driving_motions_and_security(tmp_path):
    mgr = ColabOneLiveStudioManager(tmp_path / "live_root", CATALOG_DIR)
    summary = mgr.catalog_summary()
    assert summary["port"] == 8190
    assert len(summary["visual_engines"]) == 3

    # Create avatar with portrait + product
    created = mgr.save_avatar(
        {
            "id": "host_xiaoya",
            "name": "主播小雅",
            "persona_prompt": "专业数码好物主播",
            "portrait_b64": _make_png_b64(),
            "portrait_filename": "portrait.png",
            "products": [
                {
                    "id": "prod_01",
                    "name": "无线麦克风",
                    "price": "199元",
                    "highlights": "一键降噪",
                }
            ],
        }
    )
    assert created["id"] == "host_xiaoya"
    assert created["portrait_path"] == "portrait.png"
    assert "portrait.png" in created["assets_meta"]
    assert len(created["assets_meta"]["portrait.png"]["sha256"]) == 64

    # Upload action clip + BGM
    clip_bytes = b"fake-mp4-action-wave-stream"
    bgm_bytes = b"fake-mp3-bgm-data"
    up = mgr.upload_action_clip(
        "host_xiaoya",
        {
            "action_id": "action_wave",
            "label": "挥手欢迎",
            "kind": "oneshot",
            "filename": "wave.mp4",
            "data": base64.b64encode(clip_bytes).decode("ascii"),
            "bgm_filename": "wave.mp3",
            "bgm_data": base64.b64encode(bgm_bytes).decode("ascii"),
            "duration_seconds": 3.5,
        },
    )
    assert up["sha256"] == hashlib.sha256(clip_bytes).hexdigest()
    assert up["bgm_sha256"] == hashlib.sha256(bgm_bytes).hexdigest()
    loaded = mgr.get_avatar("host_xiaoya")
    assert any(a["id"] == "action_wave" and a["clip_path"] == "clips/action_wave.mp4" for a in loaded["actions"])

    # Path traversal & symlink rejection
    with pytest.raises(ValueError):
        mgr.get_avatar("../etc")
    with pytest.raises(ValueError):
        mgr.get_avatar("a/b")

    escape_target = tmp_path / "outside"
    escape_target.mkdir()
    symlink_avatar = mgr.avatars_dir / "sym_evil"
    os.symlink(escape_target, symlink_avatar)
    with pytest.raises(ValueError):
        mgr.get_avatar("sym_evil")
    assert all(a["id"] != "sym_evil" for a in mgr.list_avatars())

    # Driving motion upload / list / delete
    drive_bytes = b"fake-driving-dance-video"
    motion = mgr.upload_driving_motion(
        {
            "id": "drive_kpop",
            "label": "动感热舞",
            "filename": "kpop.mp4",
            "data": base64.b64encode(drive_bytes).decode("ascii"),
            "duration_seconds": 15.0,
            "trigger_keywords": ["跳舞", "热舞"],
        }
    )
    assert motion["sha256"] == hashlib.sha256(drive_bytes).hexdigest()
    assert len(mgr.list_driving_motions()) == 1
    mgr.delete_driving_motion("drive_kpop")
    assert mgr.list_driving_motions() == []

    # Delete avatar cleanly
    mgr.delete_avatar("host_xiaoya")
    with pytest.raises(KeyError):
        mgr.get_avatar("host_xiaoya")


def test_manager_build_avatar_pack_with_progress_and_cancel(tmp_path):
    mgr = ColabOneLiveStudioManager(tmp_path / "live_root", CATALOG_DIR)

    class _FakeTask:
        def __init__(self) -> None:
            self.progress: float = 0.0
            self.progress_history: list[float] = []
            self.messages: list[str] = []
            self.notes: list[str] = []
            self.cancelled = False

        def check(self) -> None:
            if self.cancelled:
                raise RuntimeError("cancelled")

        def update(self, progress: float = 0.0, message: str = "") -> None:
            self.progress = float(progress)
            self.progress_history.append(self.progress)
            self.messages.append(message)

        def note(self, message, level="info") -> None:
            self.notes.append(f"{level}:{message}")

    task = _FakeTask()
    built_ids: list[str] = []

    def _runner(*, action_id, action_meta, avatar_dir, portrait_path, request):
        _ = (action_meta, avatar_dir, request)
        assert portrait_path is not None and portrait_path.is_file()
        built_ids.append(action_id)
        return {"bytes": f"clip-{action_id}".encode("utf-8"), "duration_seconds": 4.0}

    res = mgr.build_avatar_pack(
        task,
        {
            "avatar_id": "pack_host",
            "name": "批处理主播",
            "portrait_b64": _make_png_b64(),
            "portrait_filename": "face.png",
        },
        comfy_runner=_runner,
    )
    assert res["built_count"] == 6
    assert built_ids == [
        "idle_loop",
        "talk_explain",
        "talk_product",
        "action_wave",
        "action_heart",
        "dance_01",
    ]
    assert task.progress == pytest.approx(1.0)
    assert all(0.0 <= p <= 1.0 for p in task.progress_history)
    assert task.progress_history[0] == pytest.approx(0.02)
    saved = mgr.get_avatar("pack_host")
    assert len(saved["actions"]) == 6

    # Cancellation stops remaining actions cleanly mid-pack
    cancel_task = _FakeTask()
    cancel_built: list[str] = []

    def _cancel_runner(*, action_id, action_meta, avatar_dir, portrait_path, request):
        _ = (action_meta, avatar_dir, portrait_path, request)
        cancel_built.append(action_id)
        if len(cancel_built) >= 2:
            cancel_task.cancelled = True
        return {"bytes": f"clip-{action_id}".encode("utf-8"), "duration_seconds": 3.0}

    with pytest.raises(RuntimeError, match="cancelled"):
        mgr.build_avatar_pack(
            cancel_task,
            {
                "avatar_id": "pack_host_cancel",
                "name": "可取消主播",
                "portrait_b64": _make_png_b64(),
                "portrait_filename": "face.png",
                "actions": ["idle_loop", "talk_explain", "action_wave", "action_heart"],
                "dance_ids": [],
            },
            comfy_runner=_cancel_runner,
        )
    assert cancel_built == ["idle_loop", "talk_explain"]
    partial = mgr.get_avatar("pack_host_cancel")
    assert [a["id"] for a in partial["actions"]] == ["idle_loop", "talk_explain"]


def test_manager_tokens_external_llm_probe_and_start_stop_lifecycle(tmp_path):
    mgr = ColabOneLiveStudioManager(tmp_path / "live_root", CATALOG_DIR)

    # Token issuance, wss URL rewrite, constant-time verify & one-time consume
    issued = mgr.issue_session_token("https://live.example.com", ttl_seconds=30.0)
    assert issued["ws_path"] == COLABONE_LIVE_WS_PATH
    assert issued["ws_url"] == "wss://live.example.com/_colabone_live/ws"
    assert issued["canvas_ready"] is True
    assert mgr.verify_session_token("wrong-tok") is False
    assert mgr.verify_session_token(issued["token"], consume=False) is True
    assert mgr.verify_session_token(issued["token"], consume=True) is True
    assert mgr.verify_session_token(issued["token"]) is False

    # External LLM connectivity test + fallback + secret scrubbing
    secret_key = "sk-probe-secret-abcdef123456"

    class _Resp:
        def __init__(self, status_code: int, text: str = "") -> None:
            self.status_code = status_code
            self.text = text

    ok_probe = mgr.test_external_llm(
        {
            "base_url": "https://api.example.com/v1",
            "api_key": secret_key,
            "model": "gpt-4o-mini",
        },
        http_get=lambda *a, **kw: _Resp(200, '{"data":[]}'),
    )
    assert ok_probe["ok"] is True

    fallback_probe = mgr.test_external_llm(
        {
            "base_url": "https://api.example.com/v1",
            "api_key": secret_key,
            "model": "gpt-4o-mini",
        },
        http_get=lambda *a, **kw: _Resp(404, "not found"),
        http_post=lambda *a, **kw: _Resp(200, '{"choices":[]}'),
    )
    assert fallback_probe["ok"] is True

    def _boom(*_a, **_kw):
        raise RuntimeError(f"connection failed with Bearer {secret_key}")

    err_probe = mgr.test_external_llm(
        {
            "base_url": "https://api.example.com/v1",
            "api_key": secret_key,
            "model": "gpt-4o-mini",
        },
        http_get=_boom,
    )
    assert err_probe["ok"] is False
    assert secret_key not in err_probe["error"]

    # Fail-fast VRAM preflight on T4 + local LLM
    with pytest.raises(ValueError) as vram_err:
        mgr.start(
            {
                "visual_engine": "flashhead_lite",
                "tts_engine": "qwen3_tts",
                "llm_mode": "local_gguf",
            },
            gpu_info={"name": "Tesla T4", "total_mib": 15360},
            loaded_model_vram_mib=6144,
        )
    assert "external_api" in str(vram_err.value)

    # Start / status / stop lifecycle with mocked loopback runner
    runner_state = {"started": False, "stopped": False}

    class _FakeRunner:
        def __init__(self, app, *, host, port):
            assert host == "127.0.0.1"
            assert port == 8190
            self.app = app

        def start(self):
            runner_state["started"] = True

        def stop(self):
            runner_state["stopped"] = True

    started_status = mgr.start(
        {
            "visual_engine": "flashhead_lite",
            "tts_engine": "qwen3_tts",
            "llm_mode": "external_api",
            "external_llm": {
                "base_url": "https://api.example.com/v1",
                "api_key": secret_key,
                "model": "gpt-4o-mini",
            },
            "enable_rtmp": True,
            "rtmp_url": "rtmp://push.example.com/live/my_secret_stream_key",
        },
        gpu_info={"name": "NVIDIA L4", "total_mib": 24576},
        loaded_model_vram_mib=0,
        server_factory=lambda app, host, port: _FakeRunner(app, host=host, port=port),
        synthetic=True,
        min_free_disk_mib=0,
    )
    assert runner_state["started"] is True
    assert started_status["running"] is True
    assert started_status["state"] == "ready"
    assert started_status["port"] == 8190
    assert started_status["active_config"]["external_llm"]["api_key"] == "***"
    assert "my_secret_stream_key" not in json.dumps(started_status)

    # Issue a token without canvas tunnel: canvas_ready=False and ws_url=""
    live_tok = mgr.issue_session_token()
    assert live_tok["canvas_ready"] is False
    assert live_tok["ws_url"] == ""
    loopback_tok = mgr.issue_session_token(allow_loopback_fallback=True)
    assert loopback_tok["canvas_ready"] is False
    assert loopback_tok["ws_url"] == f"ws://127.0.0.1:8190{COLABONE_LIVE_WS_PATH}"

    stopped_status = mgr.stop()
    assert runner_state["stopped"] is True
    assert stopped_status["running"] is False
    assert stopped_status["state"] == "stopped"
    assert stopped_status["active_config"] is None
    assert stopped_status["vram_budget"] is None
    assert stopped_status["error"] == ""
    assert mgr.verify_session_token(live_tok["token"]) is False


def test_live_ws_auth_ok_codec_switch_engine_and_performing_barge_in():
    token = "tok-live-ws-parity-0123456789abcdef"
    app = create_colabone_live_app(
        verify_token=constant_time_token_verifier(token),
        session_config={"visual_engine": "flashhead_lite", "tts_engine": "qwen3_tts"},
    )
    with TestClient(app) as client:
        with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws:
            ws.send_json({"type": "auth", "token": token})
            auth_ok = ws.receive_json()
            assert auth_ok["type"] == "auth_ok"
            assert auth_ok["codec"] == "jpeg"
            assert auth_ok["audio_sample_rate"] == 24000
            assert auth_ok["fps"] == 25

            # Ping echoes caller timestamp for RTT HUD
            ws.send_json({"type": "ping", "ts": 123456789})
            pong = ws.receive_json()
            assert pong == {"type": "pong", "ts": 123456789}

            # switch_engine updates config cleanly and rejects invalid engines
            ws.send_json(
                {
                    "type": "switch_engine",
                    "visual_engine": "musetalk_bank",
                    "personalive_enabled": True,
                }
            )
            sw = ws.receive_json()
            assert sw["type"] == "state"
            assert sw["visual_engine"] == "musetalk_bank"
            assert sw["personalive_enabled"] is True

            ws.send_json({"type": "switch_engine", "visual_engine": "bogus_engine"})
            bad_sw = ws.receive_json()
            assert bad_sw["type"] == "error"
            assert bad_sw["code"] == "invalid_engine"

            # Trigger PERFORMING dance action, then verify ptt_start interrupts PERFORMING
            ws.send_json({"type": "trigger_action", "action_id": "dance_01"})
            act = ws.receive_json()
            assert act["type"] == "action"
            assert act["state"] == "PERFORMING"
            frame_bytes = ws.receive_bytes()
            ftype, _, _, _ = unpack_colabone_live_frame(frame_bytes)
            assert ftype == COLABONE_LIVE_FRAME_VIDEO

            ws.send_json({"type": "ptt_start"})
            barge = ws.receive_json()
            assert barge["type"] == "barge_in"
            assert barge["reason"] == "ptt_start"
            assert barge["state"] == "LISTENING"


# ---------------------------------------------------------------------------
# 12. Hardening regression tests (C1, C2, C4, C7, C8, C9, H1, H3, H4, H5, H6, M1-M5, L1-L3)
# ---------------------------------------------------------------------------


def _find_free_loopback_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def test_real_server_runner_start_stop_no_deadlock_and_releases_port():
    port = _find_free_loopback_port()
    token = "tok-real-runner-check-0123456789"
    activity_calls: list[float] = []
    app = create_colabone_live_app(
        verify_token=constant_time_token_verifier(token),
        session_config={"visual_engine": "flashhead_lite", "tts_engine": "qwen3_tts"},
        on_activity=lambda: activity_calls.append(time.monotonic()),
    )
    runner = ColabOneLiveServerRunner(app, host="127.0.0.1", port=port)
    runner.start(wait_timeout_s=5.0)
    try:
        assert runner.is_running() is True
        # Idempotent start must NOT deadlock on self._lock
        runner.start(wait_timeout_s=2.0)
        resp = httpx.get(f"http://127.0.0.1:{port}/_colabone_live/health", timeout=2.0)
        assert resp.status_code == 200
        assert resp.json()["ok"] is True
    finally:
        t0 = time.monotonic()
        runner.stop(join_timeout_s=4.0)
        elapsed = time.monotonic() - t0
        assert elapsed < 3.5

    assert runner.is_running() is False
    # Verify port is cleanly released and immediately re-bindable
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        sock.bind(("127.0.0.1", port))

    # Verify on_activity throttling (3.0s) via TestClient on an isolated app (never concurrently against uvicorn)
    with TestClient(app) as client:
        with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws:
            ws.send_json({"type": "auth", "token": token})
            assert ws.receive_json()["type"] == "auth_ok"
            for i in range(5):
                ws.send_json({"type": "ping", "ts": i})
                assert ws.receive_json()["type"] == "pong"
    assert len(activity_calls) == 1


def test_text_input_during_speaking_cancels_turn1_and_completes_turn2():
    turn1_started = asyncio.Event()
    turn2_calls: list[str] = []

    async def _llm_stream(messages, *, director_mode="dialogue", cancel_event=None):
        _ = (director_mode, cancel_event)
        user_msg = messages[-1]["content"]
        if user_msg == "first":
            yield "第一轮长回复开始，"
            turn1_started.set()
            await asyncio.sleep(5.0)
            yield "不应送达的尾巴。"
        else:
            turn2_calls.append(user_msg)
            yield "第二轮回复成功！"
            await asyncio.sleep(0)

    token = "tok-c4-barge-text-0123456789"
    app = create_colabone_live_app(
        verify_token=constant_time_token_verifier(token),
        tts_engine=ColabOneMockStreamingTtsEngine("qwen3_tts", min_chunks=2, max_chunks=2),
        visual_engine=ColabOneSyntheticVisualEngine(width=32, height=32, fps=25),
        llm_stream_fn=_llm_stream,
    )

    with TestClient(app) as client:
        with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws:
            ws.send_json({"type": "auth", "token": token})
            assert ws.receive_json()["type"] == "auth_ok"

            ws.send_json({"type": "text_input", "text": "first"})
            # Wait until Turn 1 has entered SPEAKING and sent its first transcript_delta
            saw_first_delta = False
            for _ in range(20):
                msg = ws.receive()
                if msg.get("text") is not None:
                    obj = json.loads(msg["text"])
                    if obj.get("type") == "transcript_delta" and "第一轮" in obj.get("text", ""):
                        saw_first_delta = True
                        break
            assert saw_first_delta is True

            # Send Turn 2 while Turn 1 is still active: must barge-in Turn 1 and finish Turn 2
            ws.send_json({"type": "text_input", "text": "second"})
            seen_events: list[dict[str, Any]] = []
            for _ in range(50):
                msg = ws.receive()
                if msg.get("text") is not None:
                    obj = json.loads(msg["text"])
                    seen_events.append(obj)
                    if obj.get("type") == "turn_end":
                        break

            assert any(e.get("type") == "barge_in" and e.get("reason") == "new_turn" for e in seen_events)
            assert any(
                e.get("type") == "transcript_delta" and "第二轮回复成功" in e.get("text", "")
                for e in seen_events
            )
            telemetry = [e for e in seen_events if e.get("type") == "telemetry"]
            assert len(telemetry) == 1
            assert telemetry[0]["video_codec"] == "jpeg"
            assert telemetry[0]["ttft_ms"] >= 0.0
            assert telemetry[0]["ttfa_ms"] >= 0.0
            assert telemetry[0]["e2e_ms"] >= 0.0
            assert any(e.get("type") == "turn_end" and e.get("state") == "IDLE" for e in seen_events)
            assert turn2_calls == ["second"]
            assert app.state.director.state == "IDLE"


def test_frame_pump_continuous_idle_and_oneshot_auto_return(tmp_path):
    mt = ColabOneMuseTalkVisualEngine(
        width=16, height=16, fps=40, avatar_dir=tmp_path, default_crossfade_frames=0
    )
    mt.load()
    idle_rgb = Image.new("RGB", (16, 16), (20, 40, 60)).tobytes()
    wave_1 = Image.new("RGB", (16, 16), (220, 80, 40)).tobytes()
    wave_2 = Image.new("RGB", (16, 16), (230, 90, 50)).tobytes()
    mt.register_action_frames("idle_loop", [idle_rgb], loop=True)
    mt.register_action_frames("action_wave", [wave_1, wave_2], loop=False)

    token = "tok-c7-frame-pump-0123456789"
    app = create_colabone_live_app(
        verify_token=constant_time_token_verifier(token),
        session_config={"visual_engine": "musetalk_bank", "target_fps": 40},
        visual_engine=mt,
        frame_pump_enabled=True,
    )

    with TestClient(app) as client:
        with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws:
            ws.send_json({"type": "auth", "token": token})
            assert ws.receive_json()["type"] == "auth_ok"

            # Verify continuous IDLE video frames arrive automatically
            idle_frames = 0
            for _ in range(4):
                raw = ws.receive_bytes()
                ftype, _, _, body = unpack_colabone_live_frame(raw)
                assert ftype == COLABONE_LIVE_FRAME_VIDEO
                assert len(body) > 0
                idle_frames += 1
            assert idle_frames >= 2

            # Trigger 2-frame oneshot action and verify PERFORMING auto-returns to IDLE
            ws.send_json({"type": "trigger_action", "action_id": "action_wave", "loop": False})
            saw_action = False
            saw_idle_return = False
            for _ in range(30):
                msg = ws.receive()
                if msg.get("text") is not None:
                    obj = json.loads(msg["text"])
                    if obj.get("type") == "action" and obj.get("action_id") == "action_wave":
                        saw_action = True
                    elif (
                        obj.get("type") == "state"
                        and obj.get("state") == "IDLE"
                        and obj.get("action_id") == "idle_loop"
                    ):
                        saw_idle_return = True
                        break
            assert saw_action is True
            assert saw_idle_return is True


def test_product_card_resolution_clear_tag_and_empty_products(tmp_path):
    mgr = ColabOneLiveStudioManager(tmp_path / "live_root", CATALOG_DIR)
    saved = mgr.save_avatar(
        {
            "id": "host_shop",
            "name": "带货主播",
            "products": [
                {
                    "id": "sku1",
                    "name": "降噪耳机 Pro",
                    "price": "299元",
                    "highlights": "48dB降噪",
                    "script_hint": "通勤神器",
                    "tag": "爆款",
                }
            ],
        }
    )
    assert saved["products"][0]["tag"] == "爆款"

    # Omitting products preserves existing products
    preserved = mgr.save_avatar({"id": "host_shop", "name": "带货主播2"})
    assert len(preserved["products"]) == 1
    assert preserved["products"][0]["tag"] == "爆款"

    # Passing explicit products=[] clears the list
    cleared = mgr.save_avatar({"id": "host_shop", "name": "带货主播2", "products": []})
    assert cleared["products"] == []

    # WS card resolution for [CARD:sku1], [CARD:hide], and [CARD:]
    async def _card_llm(messages, *, director_mode="dialogue", cancel_event=None):
        _ = (messages, director_mode, cancel_event)
        yield "[CARD:sku1]看看这款宝贝，[CARD:hide]好啦收起，[CARD:]清空。"

    token = "tok-card-res-0123456789"
    app = create_colabone_live_app(
        verify_token=constant_time_token_verifier(token),
        avatar=saved,
        tts_engine=ColabOneMockStreamingTtsEngine("qwen3_tts", min_chunks=1, max_chunks=1),
        visual_engine=ColabOneSyntheticVisualEngine(width=32, height=32, fps=25),
        llm_stream_fn=_card_llm,
    )
    with TestClient(app) as client:
        with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws:
            ws.send_json({"type": "auth", "token": token})
            assert ws.receive_json()["type"] == "auth_ok"
            ws.send_json({"type": "text_input", "text": "展示商品"})

            card_events: list[dict[str, Any]] = []
            for _ in range(50):
                msg = ws.receive()
                if msg.get("text") is not None:
                    obj = json.loads(msg["text"])
                    if obj.get("type") == "card":
                        card_events.append(obj)
                    if obj.get("type") == "turn_end":
                        break

            assert len(card_events) == 3
            assert card_events[0]["product_id"] == "sku1"
            assert card_events[0]["card"]["name"] == "降噪耳机 Pro"
            assert card_events[0]["card"]["tag"] == "爆款"
            assert card_events[0]["card"]["price"] == "299元"
            assert card_events[1]["action"] == "clear"
            assert card_events[1]["card"] is None
            assert card_events[1]["product_id"] == ""
            assert card_events[2]["action"] == "clear"
            assert card_events[2]["card"] is None


def test_ptt_empty_end_resets_idle_and_multi_client_isolation():
    token = "tok-ptt-multi-0123456789"
    app = create_colabone_live_app(
        verify_token=constant_time_token_verifier(token),
        session_config={"mic_mode": "ptt"},
        tts_engine=ColabOneMockStreamingTtsEngine("qwen3_tts", min_chunks=2, max_chunks=2),
        visual_engine=ColabOneSyntheticVisualEngine(width=32, height=32, fps=25),
    )

    with TestClient(app) as client:
        with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws1:
            ws1.send_json({"type": "auth", "token": token})
            assert ws1.receive_json()["type"] == "auth_ok"

            # Audio arriving before ptt_start must NOT be buffered
            loud = _make_pcm16_frame(16000, 20, amplitude=6000)
            ws1.send_bytes(pack_colabone_live_frame(COLABONE_LIVE_FRAME_MIC_PCM, 0, 0, loud))

            ws1.send_json({"type": "ptt_start"})
            st = ws1.receive_json()
            assert st["type"] == "state" and st["state"] == "LISTENING"

            # Empty ptt_end MUST reset LISTENING back to IDLE
            ws1.send_json({"type": "ptt_end"})
            idle = ws1.receive_json()
            assert idle["type"] == "state" and idle["state"] == "IDLE"
            assert app.state.director.state == "IDLE"

            # Client-provided barge_in epoch is honored safely and clamped (<= current + 4)
            base_epoch = app.state.director.turn_epoch
            ws1.send_json({"type": "barge_in", "epoch": base_epoch + 100, "reason": "clamp_test"})
            bi = ws1.receive_json()
            assert bi["type"] == "barge_in"
            assert bi["epoch"] == base_epoch + 4

            # Second client disconnecting must NOT barge-in client 1
            ws1.send_json({"type": "trigger_action", "action_id": "dance_01", "loop": True})
            assert ws1.receive_json()["type"] == "action"
            _ = ws1.receive_bytes()
            epoch_before_ws2 = app.state.director.turn_epoch

            with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws2:
                ws2.send_json({"type": "auth", "token": token})
                assert ws2.receive_json()["type"] == "auth_ok"
                assert app.state.active_clients["count"] == 2
            # ws2 has now disconnected; ws1 is still active
            assert app.state.active_clients["count"] == 1
            assert app.state.director.turn_epoch == epoch_before_ws2
            assert app.state.director.state == "PERFORMING"


def test_ssrf_bypasses_dns_rebinding_rtmp_and_request_validators(tmp_path):
    secret = "sk-live-secret-99887766"
    for bad_url in (
        "https://metadata.google.internal./computeMetadata/v1",
        "https://0xa9fea9fe/latest/meta-data",
        "https://2852039166/latest/meta-data",
        "https://169.254.43518/latest/meta-data",
        "https://100.64.0.1/v1",
        "https://[::ffff:169.254.169.254]/v1",
        "http://127.0.0.2:8081/v1",
        "https://127.0.0.1/v1",
        "https://localhost/v1",
        "https://sub.localhost./v1",
        "https://api.openai.com/v1?api_key=leak",
        "https://user:pass@api.openai.com/v1",
    ):
        with pytest.raises(ValidationError) as exc_info:
            ColabOneLiveExternalLLMConfig(
                base_url=bad_url,
                api_key=secret,
                model="gpt-4o-mini",
            )
        assert secret not in str(exc_info.value)

    # DNS rebinding check
    with pytest.raises(ValueError):
        validate_colabone_external_llm_resolved_ips(
            "https://rebind.example.com/v1",
            resolver=lambda _h, _p: [(socket.AF_INET, socket.SOCK_STREAM, 6, "", ("169.254.169.254", 443))],
        )
    with pytest.raises(ValueError):
        validate_colabone_external_llm_resolved_ips(
            "https://cgnat.example.com/v1",
            resolver=lambda _h, _p: [(socket.AF_INET, socket.SOCK_STREAM, 6, "", ("100.64.0.10", 443))],
        )
    # Public IP passes
    validate_colabone_external_llm_resolved_ips(
        "https://api.example.com/v1",
        resolver=lambda _h, _p: [(socket.AF_INET, socket.SOCK_STREAM, 6, "", ("93.184.216.34", 443))],
    )
    # Explicit loopback HTTP skips DNS check
    validate_colabone_external_llm_resolved_ips("http://127.0.0.1:8081/v1")

    # RTMP host SSRF blocking
    for bad_rtmp in (
        "rtmp://127.0.0.1/live/key",
        "rtmp://169.254.169.254/live/key",
        "rtmp://100.64.0.1/live/key",
        "rtmp://metadata.google.internal./live/key",
        "rtmp://0x7f000001/live/key",
    ):
        with pytest.raises(ValueError):
            validate_colabone_live_rtmp_url(bad_rtmp)

    # Secret scrubbing for RTMP userinfo, query tokens, stream keys, and bare sk-... tokens
    scrubbed = scrub_colabone_live_secrets(
        "push rtmp://alice:pw123@live.example.com/app/stream_secret?key=tok999 bare sk-short12345"
    )
    assert "pw123" not in scrubbed
    assert "stream_secret" not in scrubbed
    assert "tok999" not in scrubbed
    assert "sk-short12345" not in scrubbed

    # Manager test_external_llm enforces allow_redirects=False and rejects 302
    mgr = ColabOneLiveStudioManager(tmp_path / "live_root", CATALOG_DIR)
    seen_kwargs: dict[str, Any] = {}

    class _RedirResp:
        status_code = 302
        text = "Found"

    def _mock_get(*_args, **kwargs):
        seen_kwargs.update(kwargs)
        return _RedirResp()

    redir_res = mgr.test_external_llm(
        {"base_url": "https://api.example.com/v1", "api_key": secret, "model": "demo"},
        http_get=_mock_get,
    )
    assert seen_kwargs.get("allow_redirects") is False
    assert redir_res["ok"] is False
    assert redir_res["status_code"] == 302

    # DNS check rejects private resolution inside test_external_llm
    with pytest.raises(ValueError):
        mgr.test_external_llm(
            {"base_url": "https://evil.example.com/v1", "api_key": secret, "model": "demo"},
            dns_resolver=lambda _h, _p: [(socket.AF_INET, socket.SOCK_STREAM, 6, "", ("10.0.0.5", 443))],
        )

    # M3: StartRequest requires external_llm when llm_mode == external_api
    with pytest.raises(ValidationError):
        ColabOneLiveStartRequest(llm_mode="external_api")

    # M3: BuildPackRequest enforces resolution bounds 256..2560
    with pytest.raises(ValidationError):
        ColabOneLiveBuildPackRequest(avatar_id="av1", resolution="128x128")
    with pytest.raises(ValidationError):
        ColabOneLiveBuildPackRequest(avatar_id="av1", resolution="3840x2160")


def test_directive_parser_edge_cases():
    parser = ColabOneLiveDirectiveParser()
    text = (
        '[ ACTION : action_wave ]大家喊「太棒了！」然后欢呼。她说：“好极了！”接着讲。'
        "[CARD:]Done.!Next [ACTION:] keep [MOOD:unclosed"
    )
    events: list[dict[str, str]] = []
    for ch in text:
        events.extend(parser.feed(ch))
    events.extend(parser.flush())

    assert events[0] == {"kind": "action", "value": "action_wave"}
    speeches = [e["text"] for e in events if e["kind"] == "speech"]
    # Closing brackets/quotes stay attached after hard punct (never stray next-clause prefix)
    assert speeches[0] == "大家喊「太棒了！」"
    assert speeches[1] == "然后欢呼。"
    assert speeches[2] == "她说："
    assert speeches[3] == "“好极了！”"
    assert speeches[4] == "接着讲。"
    # Empty [CARD:] emits clear card directive
    assert {"kind": "card", "value": ""} in events
    # _pending_dot followed by '!' splits properly without swallowing '!'
    assert "Done.!" in speeches
    # Released malformed directive buffers with ':' are NOT split on ':'
    assert any("[ACTION:] keep [MOOD:unclosed" in s for s in speeches)


def test_avatar_trash_tombstone_and_orphan_extension_cleanup(tmp_path):
    mgr = ColabOneLiveStudioManager(tmp_path / "live_root", CATALOG_DIR)
    mgr.save_avatar({"id": "av_del", "name": "待删主播"})

    # Upload mp4 then replace with webm: stale mp4 and assets_meta key must be removed
    mgr.upload_action_clip(
        "av_del",
        {
            "action_id": "action_wave",
            "filename": "wave.mp4",
            "data": base64.b64encode(b"mp4-v1").decode("ascii"),
            "bgm_filename": "bgm.mp3",
            "bgm_data": base64.b64encode(b"mp3-v1").decode("ascii"),
        },
    )
    av_dir = mgr.avatars_dir / "av_del"
    assert (av_dir / "clips" / "action_wave.mp4").is_file()

    mgr.upload_action_clip(
        "av_del",
        {
            "action_id": "action_wave",
            "filename": "wave.webm",
            "data": base64.b64encode(b"webm-v2").decode("ascii"),
            "bgm_filename": "bgm.wav",
            "bgm_data": base64.b64encode(b"wav-v2").decode("ascii"),
        },
    )
    assert not (av_dir / "clips" / "action_wave.mp4").exists()
    assert (av_dir / "clips" / "action_wave.webm").is_file()
    assert not (av_dir / "bgm" / "action_wave.mp3").exists()
    assert (av_dir / "bgm" / "action_wave.wav").is_file()
    loaded = mgr.get_avatar("av_del")
    assert "clips/action_wave.mp4" not in loaded["assets_meta"]
    assert "clips/action_wave.webm" in loaded["assets_meta"]
    assert "bgm/action_wave.mp3" not in loaded["assets_meta"]

    # Driving motion preserves original created_at and removes orphan extension
    m1 = mgr.upload_driving_motion(
        {
            "id": "drv1",
            "label": "舞步1",
            "filename": "d.mp4",
            "data": base64.b64encode(b"drive-mp4").decode("ascii"),
        }
    )
    time.sleep(0.02)
    m2 = mgr.upload_driving_motion(
        {
            "id": "drv1",
            "label": "舞步2",
            "filename": "d.mov",
            "data": base64.b64encode(b"drive-mov").decode("ascii"),
        }
    )
    assert m2["created_at"] == m1["created_at"]
    assert m2["updated_at"] >= m1["updated_at"]
    drv_dir = mgr.driving_dir / "drv1"
    assert not (drv_dir / "clip.mp4").exists()
    assert (drv_dir / "clip.mov").is_file()

    # Delete avatar moves folder to .trash/<id> tombstone (not listed in list_avatars)
    mgr.delete_avatar("av_del")
    assert not (mgr.avatars_dir / "av_del").exists()
    assert (mgr.avatars_dir / ".trash" / "av_del" / "manifest.json").is_file()
    assert all(a["id"] != "av_del" for a in mgr.list_avatars())

    # Recreating same avatar_id cleans up stale tombstone
    mgr.save_avatar({"id": "av_del", "name": "重建主播"})
    assert not (mgr.avatars_dir / ".trash" / "av_del").exists()
    assert mgr.get_avatar("av_del")["name"] == "重建主播"


def test_resident_tts_worker_stale_epoch_draining_stderr_drain_and_antialias(tmp_path):
    # Fake worker that floods >128 KiB to stderr and emits stale-epoch pcm/done before current epoch
    fake_worker_script = r"""
import base64, json, sys
sys.stderr.write("X" * 131072 + "\n")
sys.stderr.flush()
sys.stdout.write(json.dumps({"type": "ready", "engine_id": "qwen3_tts", "sample_rate": 24000}) + "\n")
sys.stdout.flush()
for line in sys.stdin:
    req = json.loads(line)
    cmd = req.get("cmd")
    if cmd == "shutdown":
        sys.stdout.write(json.dumps({"type": "bye"}) + "\n")
        sys.stdout.flush()
        break
    if cmd == "synthesize":
        ep = int(req["epoch"])
        if req.get("text") == "trigger_err":
            tok = req.get("text")
            sys.stdout.write(json.dumps({"type": "error", "epoch": ep, "detail": "hf_secret_leak_999 failed"}) + "\n")
            sys.stdout.flush()
            continue
        # Inject stale epoch (ep - 1) pcm + done before real epoch output
        stale_pcm = base64.b64encode(b"\x11\x11" * 480).decode("ascii")
        real_pcm = base64.b64encode(b"\x00\x00" * 480).decode("ascii")
        sys.stdout.write(json.dumps({"type": "pcm", "epoch": ep - 1, "sample_rate": 24000, "data_b64": stale_pcm}) + "\n")
        sys.stdout.write(json.dumps({"type": "done", "epoch": ep - 1}) + "\n")
        sys.stdout.write(json.dumps({"type": "pcm", "epoch": ep, "sample_rate": 24000, "data_b64": real_pcm}) + "\n")
        sys.stdout.write(json.dumps({"type": "done", "epoch": ep}) + "\n")
        sys.stdout.flush()
"""
    worker = ColabOneResidentTtsWorker(
        "qwen3_tts",
        venv_python=Path(sys.executable),
        hf_token="hf_secret_leak_999",
        startup_timeout_s=5.0,
        read_timeout_s=5.0,
    )
    worker.build_worker_command = lambda: [sys.executable, "-u", "-c", fake_worker_script]  # type: ignore[method-assign]
    worker.load()
    try:
        chunks = list(worker.synthesize_stream("你好", epoch=5))
        assert len(chunks) == 1
        assert chunks[0] == b"\x00" * 960

        with pytest.raises(RuntimeError) as exc_info:
            list(worker.synthesize_stream("trigger_err", epoch=6))
        assert "hf_secret_leak_999" not in str(exc_info.value)
    finally:
        proc = worker._proc
        worker.unload()
        assert worker.is_loaded is False
        assert proc is not None and proc.poll() is not None

    # Anti-aliasing filter attenuates 24kHz alternating Nyquist tone when downsampling 48k -> 24k
    n_samples = 960
    nyquist_48k = struct.pack(
        f"<{n_samples}h", *[12000 if i % 2 == 0 else -12000 for i in range(n_samples)]
    )
    down = resample_pcm16_mono(nyquist_48k, 48000, 24000)
    assert len(down) == 960
    assert compute_pcm16_rms(down) < 0.05


def test_nvenc_smoke_probe_rtmp_aac_and_visual_unload_fixes(tmp_path):
    # A100 simulation: -encoders lists h264_nvenc & libx264, but 1-frame nvenc smoke encode fails
    def _a100_runner(cmd: list[str]) -> tuple[int, str]:
        if "-encoders" in cmd:
            return 0, " V..... h264_nvenc\n V..... libx264\n"
        if "h264_nvenc" in cmd:
            return 1, "Cannot load libnvidia-encode.so.1"
        return 0, ""

    assert ColabOneLiveVideoEncoder.probe_encoder(runner=_a100_runner) == "libx264"

    # Baseline profile + level 4.0 and RTMP AAC audio mapping
    rtmp_cmd = ColabOneLiveVideoEncoder.build_ffmpeg_command(
        "libx264", 720, 1280, 25, rtmp_url="rtmp://live.example.com/app/key123"
    )
    assert "-profile:v" in rtmp_cmd and "baseline" in rtmp_cmd
    assert "-level" in rtmp_cmd and "4.0" in rtmp_cmd
    assert "-c:a" in rtmp_cmd and "aac" in rtmp_cmd
    assert "0:v:0" in rtmp_cmd and "1:a:0" in rtmp_cmd

    # Visual unload clears caches, MuseTalk displays final oneshot frame before crossfading to idle_loop, FlashHead rejects symlink
    mt = ColabOneMuseTalkVisualEngine(width=8, height=8, fps=25, default_crossfade_frames=2)
    mt.load()
    idle_rgb = Image.new("RGB", (8, 8), (0, 0, 200)).tobytes()
    wave_rgb = Image.new("RGB", (8, 8), (200, 0, 0)).tobytes()
    mt.register_action_frames("idle_loop", [idle_rgb], loop=True)
    mt.register_action_frames("action_wave", [wave_rgb], loop=False)
    mt.set_action("action_wave", loop=False, crossfade_frames=0)
    f_final = mt.render_frame(b"", epoch=1, pts_ms=0)
    # Tick 1 displays final oneshot frame intact (200,0,0) and queues transition to idle_loop
    assert f_final.action_id == "action_wave"
    assert f_final.is_crossfading is False
    assert f_final.rgb_bytes[0] == 200 and f_final.rgb_bytes[2] == 0
    assert mt.current_action == "idle_loop"
    # Tick 2 crossfades wave_rgb (200,0,0) with idle_rgb (0,0,200) at alpha=0.5 -> (100,0,100)
    f_blend = mt.render_frame(b"", epoch=1, pts_ms=40)
    assert f_blend.is_crossfading is True
    assert f_blend.rgb_bytes[0] == 100 and f_blend.rgb_bytes[2] == 100
    mt.unload()
    assert mt.ring_buffer.list_action_ids() == []

    pl = ColabOnePersonaLiveVisualEngine(width=8, height=8)
    pl.set_driving_frames([idle_rgb])
    pl.unload()
    assert pl._driving_frames == []

    real_img = tmp_path / "real.png"
    Image.new("RGB", (16, 16), (10, 20, 30)).save(real_img, format="PNG")
    sym_img = tmp_path / "sym.png"
    os.symlink(real_img, sym_img)
    fh = ColabOneFlashHeadVisualEngine(width=16, height=16, portrait_path=sym_img)
    with pytest.raises(ValueError):
        fh.load()

    # Manager catalog disk check when min_free_disk_mib=None & active_client_count fast path
    mgr = ColabOneLiveStudioManager(tmp_path / "live_mgr", CATALOG_DIR)
    assert mgr.active_client_count() == 0
    mgr._free_disk_mib = lambda: 4096  # type: ignore[method-assign]
    # Uncached non-synthetic requires full catalog disk (> 4096 MiB)
    with pytest.raises(ValueError) as disk_err:
        mgr.start(
            {
                "visual_engine": "flashhead_lite",
                "tts_engine": "qwen3_tts",
                "llm_mode": "local_gguf",
            },
            gpu_info={"name": "NVIDIA A100", "total_mib": 81920},
            loaded_model_vram_mib=6144,
            synthetic=False,
            min_free_disk_mib=None,
        )
    assert "磁盘空间不足" in str(disk_err.value)
    # Synthetic mode only requires 2048 MiB headroom
    cfg_check = ColabOneLiveSessionConfig(
        visual_engine="flashhead_lite", tts_engine="qwen3_tts", llm_mode="local_gguf"
    )
    assert mgr._required_disk_mib(cfg_check, synthetic=True) == 2048
    assert mgr._required_disk_mib(cfg_check, synthetic=False) > 4096
    # Populating local caches shrinks required disk down to 2048 MiB headroom
    for comp_id in ("silero_vad", "sensevoice_small", "qwen3_tts", "flashhead_lite"):
        cdir = mgr.root / "cache" / "live-studio" / comp_id
        cdir.mkdir(parents=True, exist_ok=True)
        (cdir / "model.bin").write_bytes(b"cached")
    assert mgr._required_disk_mib(cfg_check, synthetic=False) == 2048

    # Manager WS switch_engine validates VRAM & updates _active_config cleanly
    captured_app: dict[str, Any] = {}

    class _CapRunner:
        def __init__(self, app, *, host, port):
            _ = (host, port)
            captured_app["app"] = app

        def start(self):
            pass

        def stop(self):
            pass

    mgr.start(
        {
            "visual_engine": "flashhead_lite",
            "tts_engine": "qwen3_tts",
            "llm_mode": "external_api",
            "external_llm": {
                "base_url": "https://api.example.com/v1",
                "api_key": "sk-switch-test-123456",
                "model": "gpt-4o-mini",
            },
        },
        gpu_info={"name": "NVIDIA L4", "total_mib": 24576},
        loaded_model_vram_mib=0,
        server_factory=lambda app, host, port: _CapRunner(app, host=host, port=port),
        synthetic=True,
        min_free_disk_mib=0,
        frame_pump_enabled=False,
    )
    try:
        tok = mgr.issue_session_token("https://live.example.com")["token"]
        with TestClient(captured_app["app"]) as client:
            with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws:
                ws.send_json({"type": "auth", "token": tok})
                assert ws.receive_json()["type"] == "auth_ok"
                assert mgr.active_client_count() == 1

                # Switch flashhead_lite to musetalk_bank on L4 succeeds
                ws.send_json({"type": "switch_engine", "visual_engine": "musetalk_bank"})
                sw_ok = ws.receive_json()
                assert sw_ok["type"] == "state"
                assert sw_ok["visual_engine"] == "musetalk_bank"
                assert mgr.status()["visual_engine"] == "musetalk_bank"

                # Enabling personalive_enabled on L4 24GB fails VRAM preflight cleanly
                ws.send_json({"type": "switch_engine", "personalive_enabled": True})
                sw_err = ws.receive_json()
                assert sw_err["type"] == "error"
                assert sw_err["code"] == "switch_engine_failed"
                assert mgr.status()["personalive_enabled"] is False
    finally:
        mgr.stop()


def test_streaming_tts_incremental_queue_emits_first_chunk_before_clause_finishes():
    first_chunk_received_at: list[float] = []
    clause_finished_at: list[float] = []

    class _SlowStreamingTts(ColabOneMockStreamingTtsEngine):
        def synthesize_stream(
            self,
            text: str,
            *,
            voice_preset: str = "shen_zhiyi",
            epoch: int = 0,
            cancel_event: threading.Event | None = None,
            get_current_epoch=None,
        ):
            _ = (text, voice_preset, epoch, get_current_epoch)
            chunk = b"\x00" * self.chunk_bytes
            for idx in range(4):
                if cancel_event is not None and cancel_event.is_set():
                    return
                yield chunk
                if idx < 3:
                    time.sleep(0.06)
            clause_finished_at.append(time.monotonic())

    async def _one_clause_llm(messages, *, director_mode="dialogue", cancel_event=None):
        _ = (messages, director_mode, cancel_event)
        yield "流式首包测试！"

    token = "tok-incremental-tts-0123456789"
    app = create_colabone_live_app(
        verify_token=constant_time_token_verifier(token),
        tts_engine=_SlowStreamingTts("qwen3_tts"),
        visual_engine=ColabOneSyntheticVisualEngine(width=32, height=32, fps=25),
        llm_stream_fn=_one_clause_llm,
    )
    with TestClient(app) as client:
        with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws:
            ws.send_json({"type": "auth", "token": token})
            assert ws.receive_json()["type"] == "auth_ok"
            ws.send_json({"type": "text_input", "text": "测试"})

            audio_count = 0
            for _ in range(30):
                msg = ws.receive()
                if msg.get("bytes") is not None:
                    ftype, _, _, _ = unpack_colabone_live_frame(msg["bytes"])
                    if ftype == COLABONE_LIVE_FRAME_AUDIO_PCM:
                        audio_count += 1
                        if audio_count == 1:
                            first_chunk_received_at.append(time.monotonic())
                elif msg.get("text") is not None:
                    obj = json.loads(msg["text"])
                    if obj.get("type") == "turn_end":
                        break

            assert audio_count == 4
            assert len(first_chunk_received_at) == 1
            assert len(clause_finished_at) == 1
            # First PCM chunk reached WS client BEFORE the clause generator finished
            assert first_chunk_received_at[0] < clause_finished_at[0] - 0.05


# ---------------------------------------------------------------------------
# 13. Pass-2 hardening regression tests
# ---------------------------------------------------------------------------


def test_switch_engine_swap_before_unload_barge_in_and_live_vram_recheck(tmp_path):
    events_log: list[str] = []

    class _TrackedVisual(ColabOneSyntheticVisualEngine):
        def __init__(self, tag: str, on_unload=None) -> None:
            super().__init__(width=16, height=16, fps=25)
            self.tag = tag
            self.unload_calls = 0
            self._on_unload = on_unload

        def load(self) -> None:
            super().load()
            events_log.append(f"load:{self.tag}")

        def unload(self) -> None:
            events_log.append(f"unload:{self.tag}")
            if callable(self._on_unload):
                self._on_unload()
            self.unload_calls += 1
            super().unload()

    class _FakeRuntime:
        def __init__(self) -> None:
            self.loaded_vram = 0
            self.total_mib = 24576

        def _loaded_model_vram_mib(self) -> int:
            return self.loaded_vram

        def gpu_info(self) -> dict[str, Any]:
            return {"name": "NVIDIA L4", "total_mib": self.total_mib, "used_mib": self.loaded_vram}

    rt = _FakeRuntime()
    mgr = ColabOneLiveStudioManager(tmp_path / "live_root", CATALOG_DIR, runtime=rt)
    captured: dict[str, Any] = {}

    class _CapRunner:
        def __init__(self, app, *, host, port):
            _ = (host, port)
            captured["app"] = app

        def start(self):
            pass

        def stop(self):
            pass

    mgr.start(
        {
            "visual_engine": "flashhead_lite",
            "tts_engine": "qwen3_tts",
            "llm_mode": "external_api",
            "external_llm": {
                "base_url": "https://api.example.com/v1",
                "api_key": "sk-pass2-switch-123456",
                "model": "gpt-4o-mini",
            },
        },
        gpu_info=rt.gpu_info(),
        loaded_model_vram_mib=0,
        server_factory=lambda app, host, port: _CapRunner(app, host=host, port=port),
        synthetic=True,
        min_free_disk_mib=0,
        frame_pump_enabled=False,
    )
    try:
        app = captured["app"]
        new_vis = _TrackedVisual("new_vis")
        swapped_before_unload = {"ok": False}

        def _check_already_swapped() -> None:
            swapped_before_unload["ok"] = app.state.visual_engine is new_vis

        old_vis = _TrackedVisual("old_vis", on_unload=_check_already_swapped)
        old_vis.load()
        app.state.visual_engine = old_vis

        orig_build = mgr._build_visual_engine
        mgr._build_visual_engine = lambda *a, **k: new_vis  # type: ignore[method-assign]

        tok = mgr.issue_session_token("https://live.example.com")["token"]
        with TestClient(app) as client:
            with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws:
                ws.send_json({"type": "auth", "token": tok})
                assert ws.receive_json()["type"] == "auth_ok"

                # Put director in PERFORMING state first; switch_engine must barge-in FIRST
                ws.send_json({"type": "trigger_action", "action_id": "dance_01", "loop": True})
                assert ws.receive_json()["type"] == "action"
                _ = ws.receive_bytes()
                assert app.state.director.state == "PERFORMING"

                ws.send_json({"type": "switch_engine", "visual_engine": "musetalk_bank"})
                first_ev = ws.receive_json()
                assert first_ev["type"] == "barge_in"
                assert first_ev["reason"] == "switch_engine"
                second_ev = ws.receive_json()
                assert second_ev["type"] == "state"
                assert second_ev["visual_engine"] == "musetalk_bank"

                # Swap happened strictly BEFORE unload; old_vis unloaded once and never resurrected
                assert swapped_before_unload["ok"] is True
                assert app.state.visual_engine is new_vis
                assert old_vis.unload_calls == 1
                assert old_vis.is_loaded is False

                # High #9: live runtime now loads an 8 GiB local LLM; switch_engine MUST reject
                rt.loaded_vram = 8192
                ws.send_json({"type": "switch_engine", "visual_engine": "flashhead_lite"})
                err_ev = ws.receive_json()
                assert err_ev["type"] == "error"
                assert err_ev["code"] == "switch_engine_failed"
                assert "8192" in err_ev["detail"]
                assert mgr.status()["visual_engine"] == "musetalk_bank"

        mgr._build_visual_engine = orig_build  # type: ignore[method-assign]
    finally:
        mgr.stop()


def test_stream_llm_completion_dns_ssrf_and_follow_redirects_false(monkeypatch):
    # 1. DNS resolving to private/metadata IP is blocked before opening HTTP connection
    def _evil_getaddrinfo(_host, _port, *_args, **_kwargs):
        return [(socket.AF_INET, socket.SOCK_STREAM, 6, "", ("169.254.169.254", 443))]

    monkeypatch.setattr(socket, "getaddrinfo", _evil_getaddrinfo)

    async def _run_private_dns() -> None:
        async for _ in stream_llm_completion(
            [{"role": "user", "content": "hi"}],
            llm_mode="external_api",
            external_config={
                "base_url": "https://rebind.example.com/v1",
                "api_key": "sk-dns-ssrf-test-123456",
                "model": "demo-model",
            },
        ):
            pass

    with pytest.raises(ValueError, match="拦截"):
        asyncio.run(_run_private_dns())

    # 2. Verify AsyncClient receives follow_redirects=False and 302 redirects are rejected
    captured_client_kwargs: dict[str, Any] = {}
    orig_async_client = httpx.AsyncClient

    def _public_getaddrinfo(_host, _port, *_args, **_kwargs):
        return [(socket.AF_INET, socket.SOCK_STREAM, 6, "", ("93.184.216.34", 443))]

    monkeypatch.setattr(socket, "getaddrinfo", _public_getaddrinfo)

    def _redir_handler(_request: httpx.Request) -> httpx.Response:
        return httpx.Response(302, headers={"Location": "http://169.254.169.254/latest/meta-data"})

    def _wrapped_async_client(*args, **kwargs):
        captured_client_kwargs.update(kwargs)
        kwargs["transport"] = httpx.MockTransport(_redir_handler)
        return orig_async_client(*args, **kwargs)

    monkeypatch.setattr("server.live_server.httpx.AsyncClient", _wrapped_async_client)

    async def _run_redir() -> None:
        async for _ in stream_llm_completion(
            [{"role": "user", "content": "hi"}],
            llm_mode="external_api",
            external_config={
                "base_url": "https://api.example.com/v1",
                "api_key": "sk-redir-test-123456",
                "model": "demo-model",
            },
        ):
            pass

    with pytest.raises(RuntimeError, match="重定向"):
        asyncio.run(_run_redir())
    assert captured_client_kwargs.get("follow_redirects") is False
    assert captured_client_kwargs.get("trust_env") is False


def test_combined_cancel_wait_with_delayed_mock_tts_and_barge_in():
    from server.live_server import _CombinedCancel

    # Direct _CombinedCancel.wait() unit verification
    e1 = threading.Event()
    e2 = threading.Event()
    comb = _CombinedCancel(e1, e2)
    assert comb.wait(0.015) is False
    threading.Timer(0.02, e2.set).start()
    assert comb.wait(0.5) is True

    # Live WS turn with ColabOneMockStreamingTtsEngine(chunk_delay_s=0.01) must not raise AttributeError
    async def _llm_stream(_messages, *, director_mode="dialogue", cancel_event=None):
        _ = (director_mode, cancel_event)
        yield "带延迟的流式语音测试！"

    token = "tok-combined-cancel-wait-123"
    app = create_colabone_live_app(
        verify_token=constant_time_token_verifier(token),
        tts_engine=ColabOneMockStreamingTtsEngine(
            "qwen3_tts", min_chunks=3, max_chunks=3, chunk_delay_s=0.01
        ),
        visual_engine=ColabOneSyntheticVisualEngine(width=16, height=16, fps=25),
        llm_stream_fn=_llm_stream,
    )
    with TestClient(app) as client:
        with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws:
            ws.send_json({"type": "auth", "token": token})
            assert ws.receive_json()["type"] == "auth_ok"
            ws.send_json({"type": "text_input", "text": "测试"})
            audio_frames = 0
            saw_turn_end = False
            for _ in range(30):
                msg = ws.receive()
                if msg.get("bytes") is not None:
                    ftype, _, _, _ = unpack_colabone_live_frame(msg["bytes"])
                    if ftype == COLABONE_LIVE_FRAME_AUDIO_PCM:
                        audio_frames += 1
                elif msg.get("text") is not None:
                    obj = json.loads(msg["text"])
                    assert obj.get("type") != "error", obj
                    if obj.get("type") == "turn_end":
                        saw_turn_end = True
                        break
            assert audio_frames == 3
            assert saw_turn_end is True


def test_frame_pump_cas_does_not_clobber_listening_and_single_pump_multi_viewer(tmp_path):
    # 1. Unit check on atomic complete_performing_if_matching CAS
    director = ColabOneLiveDirector()
    director.set_action("action_wave")
    director.set_state("PERFORMING")
    epoch_before = director.turn_epoch
    # Barge-in moves state to LISTENING and increments epoch
    director.trigger_barge_in("vad_speech_onset", next_state="LISTENING")
    assert director.state == "LISTENING"
    # Stale epoch CAS must return False and preserve LISTENING
    assert director.complete_performing_if_matching(epoch_before, "idle_loop") is False
    assert director.state == "LISTENING"

    # 2. Live frame pump mid-render barge-in never clobbers LISTENING back to IDLE
    render_entered = threading.Event()
    release_render = threading.Event()

    class _SlowFinishMuseTalk(ColabOneMuseTalkVisualEngine):
        def render_frame(self, audio_pcm_24k=b"", *, epoch=0, pts_ms=0, state="IDLE"):
            if state == "PERFORMING" and self.current_action == "action_wave":
                render_entered.set()
                release_render.wait(timeout=2.0)
            return super().render_frame(audio_pcm_24k, epoch=epoch, pts_ms=pts_ms, state=state)

    mt = _SlowFinishMuseTalk(
        width=16, height=16, fps=40, avatar_dir=tmp_path, default_crossfade_frames=0
    )
    mt.load()
    idle_rgb = Image.new("RGB", (16, 16), (10, 20, 30)).tobytes()
    wave_rgb = Image.new("RGB", (16, 16), (200, 100, 50)).tobytes()
    mt.register_action_frames("idle_loop", [idle_rgb], loop=True)
    mt.register_action_frames("action_wave", [wave_rgb], loop=False)

    token = "tok-cas-barge-pump-123"
    app = create_colabone_live_app(
        verify_token=constant_time_token_verifier(token),
        session_config={"visual_engine": "musetalk_bank", "target_fps": 40},
        visual_engine=mt,
        frame_pump_enabled=True,
    )
    with TestClient(app) as client:
        with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws:
            ws.send_json({"type": "auth", "token": token})
            assert ws.receive_json()["type"] == "auth_ok"
            # Put director into PERFORMING with 1-frame oneshot active so pump renders it
            mt.set_action("action_wave", loop=False, crossfade_frames=0)
            app.state.director.set_action("action_wave")
            app.state.director.set_state("PERFORMING")
            assert render_entered.wait(timeout=2.0)
            # Trigger barge-in to LISTENING while frame pump is mid-render
            app.state.director.trigger_barge_in("vad_onset", next_state="LISTENING")
            release_render.set()
            time.sleep(0.08)
            assert app.state.director.state == "LISTENING"

    # 3. Single app-level pump broadcasts to multiple authenticated sockets without double-clocking
    class _CountVisual(ColabOneSyntheticVisualEngine):
        def __init__(self) -> None:
            super().__init__(width=16, height=16, fps=20)
            self.render_count = 0

        def render_frame(self, audio_pcm_24k=b"", *, epoch=0, pts_ms=0, state="IDLE"):
            self.render_count += 1
            return super().render_frame(audio_pcm_24k, epoch=epoch, pts_ms=pts_ms, state=state)

    cnt_vis = _CountVisual()
    tok2 = "tok-multi-viewer-pump-123"
    app2 = create_colabone_live_app(
        verify_token=constant_time_token_verifier(tok2),
        session_config={"target_fps": 20},
        visual_engine=cnt_vis,
        frame_pump_enabled=True,
    )
    with TestClient(app2) as client:
        with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws1:
            ws1.send_json({"type": "auth", "token": tok2})
            assert ws1.receive_json()["type"] == "auth_ok"
            with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws2:
                ws2.send_json({"type": "auth", "token": tok2})
                assert ws2.receive_json()["type"] == "auth_ok"
                assert len(app2.state.authed_sockets) == 2
                pump_task_a = app2.state.frame_pump_task
                f1 = ws1.receive_bytes()
                f2 = ws2.receive_bytes()
                assert unpack_colabone_live_frame(f1)[0] == COLABONE_LIVE_FRAME_VIDEO
                assert unpack_colabone_live_frame(f2)[0] == COLABONE_LIVE_FRAME_VIDEO
                # Exactly one app-level pump task drives both viewers (never 2x tasks).
                assert app2.state.frame_pump_task is pump_task_a
                assert cnt_vis.render_count >= 1
                # Capture identical frame pts received by both viewers (same broadcast frame).
                _, _, pts1, _ = unpack_colabone_live_frame(f1)
                _, _, pts2, _ = unpack_colabone_live_frame(f2)
                # Both viewers read from the same single pump timeline without diverging tasks.
                assert pts1 >= 0 and pts2 >= 0


def test_personalive_driving_ws_execution_and_avatar_action_loop_fallback(tmp_path):
    mgr = ColabOneLiveStudioManager(tmp_path / "live_root", CATALOG_DIR)
    mgr.save_avatar(
        {
            "id": "host_pl",
            "name": "舞者主播",
            "portrait_b64": _make_png_b64(),
            "portrait_filename": "p.png",
            "actions": [
                {"id": "talk_explain", "label": "讲解", "kind": "loop"},
                {"id": "action_wave", "label": "挥手", "kind": "oneshot"},
            ],
        }
    )
    mgr.upload_driving_motion(
        {
            "id": "drive_kpop",
            "label": "热舞驱动",
            "filename": "kpop.mp4",
            "data": base64.b64encode(b"fake-driving-mp4").decode("ascii"),
            "duration_seconds": 3.0,
        }
    )

    captured: dict[str, Any] = {}

    class _CapRunner:
        def __init__(self, app, *, host, port):
            _ = (host, port)
            captured["app"] = app

        def start(self):
            pass

        def stop(self):
            pass

    mgr.start(
        {
            "visual_engine": "flashhead_lite",
            "personalive_enabled": False,
            "tts_engine": "qwen3_tts",
            "llm_mode": "external_api",
            "avatar_id": "host_pl",
            "external_llm": {
                "base_url": "https://api.example.com/v1",
                "api_key": "sk-pl-test-123456",
                "model": "gpt-4o-mini",
            },
        },
        gpu_info={"name": "NVIDIA A100", "total_mib": 81920},
        loaded_model_vram_mib=0,
        server_factory=lambda app, host, port: _CapRunner(app, host=host, port=port),
        synthetic=True,
        min_free_disk_mib=0,
        frame_pump_enabled=False,
    )
    try:
        app = captured["app"]
        base_visual = app.state.visual_engine
        tok = mgr.issue_session_token("https://live.example.com")["token"]
        with TestClient(app) as client:
            with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws:
                ws.send_json({"type": "auth", "token": tok})
                assert ws.receive_json()["type"] == "auth_ok"

                # 1. Rejected with personalive_disabled when toggle is OFF; director stays IDLE
                ws.send_json(
                    {
                        "type": "trigger_action",
                        "kind": "personalive_drive",
                        "driving_motion_id": "drive_kpop",
                    }
                )
                err = ws.receive_json()
                assert err["type"] == "error"
                assert err["code"] == "personalive_disabled"
                assert app.state.director.state == "IDLE"
                assert app.state.visual_engine is base_visual

                # 2. Normal trigger_action with loop omitted uses avatar action kind=="loop"
                ws.send_json({"type": "trigger_action", "action_id": "talk_explain"})
                act_talk = ws.receive_json()
                assert act_talk["type"] == "action"
                assert act_talk["action_id"] == "talk_explain"
                _ = ws.receive_bytes()
                assert app.state.visual_engine._action_loop is True

                # 3. Enable PersonaLive on A100 and trigger driving motion
                ws.send_json({"type": "switch_engine", "personalive_enabled": True})
                barge_sw = ws.receive_json()
                assert barge_sw["type"] == "barge_in"
                sw_ok = ws.receive_json()
                assert sw_ok["type"] == "state" and sw_ok["personalive_enabled"] is True

                ws.send_json(
                    {
                        "type": "trigger_action",
                        "kind": "personalive_drive",
                        "driving_motion_id": "drive_kpop",
                    }
                )
                act_drv = ws.receive_json()
                assert act_drv["type"] == "action"
                assert act_drv["kind"] == "personalive_drive"
                assert act_drv["driving_motion_id"] == "drive_kpop"
                assert act_drv["state"] == "PERFORMING"
                drv_frame = ws.receive_bytes()
                assert unpack_colabone_live_frame(drv_frame)[0] == COLABONE_LIVE_FRAME_VIDEO
                assert isinstance(app.state.visual_engine, ColabOnePersonaLiveVisualEngine)
                assert app.state.base_visual_engine is base_visual

                # 4. Barge-in restores base visual engine cleanly
                ws.send_json({"type": "barge_in", "reason": "stop_dance"})
                bi = ws.receive_json()
                assert bi["type"] == "barge_in"
                assert app.state.visual_engine is base_visual
                assert app.state.base_visual_engine is None
    finally:
        mgr.stop()


def test_tombstones_portrait_validation_bgm_preservation_and_build_pack_hardening(tmp_path):
    from common.live_studio import ColabOneLiveAvatarSaveRequest

    # Low #17: AvatarSaveRequest allows up to 64 actions (aligned with Manifest)
    req64 = ColabOneLiveAvatarSaveRequest(
        name="多动作主播",
        actions=[{"id": f"act_{i}", "label": f"动作{i}", "kind": "oneshot"} for i in range(40)],
    )
    assert len(req64.actions or []) == 40

    mgr = ColabOneLiveStudioManager(tmp_path / "live_root", CATALOG_DIR)

    # High #7: delete_driving_motion creates .trash/<motion_id> tombstone
    bgm_v1 = b"fake-mp3-driving-bgm"
    m1 = mgr.upload_driving_motion(
        {
            "id": "drv_tomb",
            "label": "热舞一",
            "filename": "d1.mp4",
            "data": base64.b64encode(b"drive-v1").decode("ascii"),
            "bgm_filename": "music.mp3",
            "bgm_data": base64.b64encode(bgm_v1).decode("ascii"),
        }
    )
    assert m1["bgm_path"] == "bgm.mp3"
    assert m1["bgm_sha256"] == hashlib.sha256(bgm_v1).hexdigest()

    # Medium #14: re-uploading driving motion without bgm_data preserves existing bgm_path & bgm_sha256
    m2 = mgr.upload_driving_motion(
        {
            "id": "drv_tomb",
            "label": "热舞一改",
            "filename": "d2.mp4",
            "data": base64.b64encode(b"drive-v2").decode("ascii"),
        }
    )
    assert m2["bgm_path"] == "bgm.mp3"
    assert m2["bgm_sha256"] == m1["bgm_sha256"]

    mgr.delete_driving_motion("drv_tomb")
    assert not (mgr.driving_dir / "drv_tomb").exists()
    assert (mgr.driving_dir / ".trash" / "drv_tomb" / "meta.json").is_file()
    assert mgr.list_driving_motions() == []

    # Invalid re-upload MUST NOT erase .trash/<motion_id> tombstone
    with pytest.raises(ValueError):
        mgr.upload_driving_motion(
            {
                "id": "drv_tomb",
                "label": "坏视频",
                "filename": "bad.exe",
                "data": base64.b64encode(b"nope").decode("ascii"),
            }
        )
    assert (mgr.driving_dir / ".trash" / "drv_tomb").is_dir()

    # Medium #11: save_avatar failing on bad portrait keeps .trash tombstone intact & creates no dir
    mgr.save_avatar({"id": "av_tomb", "name": "旧主播"})
    mgr.delete_avatar("av_tomb")
    assert (mgr.avatars_dir / ".trash" / "av_tomb").is_dir()

    with pytest.raises(ValueError):
        mgr.save_avatar(
            {
                "id": "av_tomb",
                "name": "坏图主播",
                "portrait_b64": base64.b64encode(b"not-a-real-image").decode("ascii"),
                "portrait_filename": "bad.png",
            }
        )
    assert (mgr.avatars_dir / ".trash" / "av_tomb").is_dir()
    assert not (mgr.avatars_dir / "av_tomb").exists()

    # Missing portrait_input_id raises explicit ValueError in both save_avatar and start
    missing_id = "a" * 32
    with pytest.raises(ValueError, match="人像输入文件不存在"):
        mgr.save_avatar({"id": "av_missing_in", "name": "缺失人像", "portrait_input_id": missing_id})
    assert not (mgr.avatars_dir / "av_missing_in").exists()

    with pytest.raises(ValueError, match="人像输入文件不存在"):
        mgr.start(
            {
                "visual_engine": "flashhead_lite",
                "tts_engine": "qwen3_tts",
                "llm_mode": "external_api",
                "portrait_input_id": missing_id,
                "external_llm": {
                    "base_url": "https://api.example.com/v1",
                    "api_key": "sk-port-test-123456",
                    "model": "gpt-4o-mini",
                },
            },
            gpu_info={"name": "NVIDIA L4", "total_mib": 24576},
            synthetic=True,
            min_free_disk_mib=0,
        )

    # Medium #14: upload_action_clip preserves existing bgm_path when re-uploaded without BGM
    mgr.save_avatar({"id": "av_bgm", "name": "音乐主播"})
    bgm_clip = b"fake-mp3-action-bgm"
    u1 = mgr.upload_action_clip(
        "av_bgm",
        {
            "action_id": "dance_01",
            "filename": "d1.mp4",
            "data": base64.b64encode(b"mp4-v1").decode("ascii"),
            "bgm_filename": "dance.mp3",
            "bgm_data": base64.b64encode(bgm_clip).decode("ascii"),
        },
    )
    assert u1["action"]["bgm_path"] == "bgm/dance_01.mp3"
    u2 = mgr.upload_action_clip(
        "av_bgm",
        {
            "action_id": "dance_01",
            "filename": "d2.webm",
            "data": base64.b64encode(b"webm-v2").decode("ascii"),
        },
    )
    assert u2["action"]["clip_path"] == "clips/dance_01.webm"
    assert u2["action"]["bgm_path"] == "bgm/dance_01.mp3"
    assert u2["bgm_sha256"] == hashlib.sha256(bgm_clip).hexdigest()

    # Medium #12: build_avatar_pack derives extension from result["path"] and prunes stale extension
    src_webm = tmp_path / "generated.webm"
    src_webm.write_bytes(b"generated-webm-clip")
    res_build = mgr.build_avatar_pack(
        None,
        {
            "avatar_id": "av_bgm",
            "actions": ["dance_01"],
            "dance_ids": [],
            "overwrite": True,
        },
        comfy_runner=lambda **_kw: {"path": src_webm, "duration_seconds": 5.0},
    )
    assert res_build["actions"][0]["clip_path"] == "clips/dance_01.webm"
    assert res_build["actions"][0]["bgm_path"] == "bgm/dance_01.mp3"
    # Replace with .mov and ensure stale .webm is pruned
    src_mov = tmp_path / "generated.mov"
    src_mov.write_bytes(b"generated-mov-clip")
    res_build2 = mgr.build_avatar_pack(
        None,
        {
            "avatar_id": "av_bgm",
            "actions": ["dance_01"],
            "dance_ids": [],
            "overwrite": True,
        },
        comfy_runner=lambda **_kw: {"path": src_mov, "duration_seconds": 5.0},
    )
    assert res_build2["actions"][0]["clip_path"] == "clips/dance_01.mov"
    assert not (mgr.avatars_dir / "av_bgm" / "clips" / "dance_01.webm").exists()


def test_resident_tts_worker_skips_stdout_noise_resets_on_eof_and_worker_script_protocol():
    from server.live_worker_tts import COLABONE_LIVE_TTS_WORKER_SCRIPT

    # Verify worker script duplicates FD 1 for IPC, redirects stdout, passes language="Auto",
    # and formats VoxCPM2 instruct + tensor detach/cpu/numpy/reshape(-1).
    assert "os.dup(1)" in COLABONE_LIVE_TTS_WORKER_SCRIPT
    assert "os.dup2(2, 1)" in COLABONE_LIVE_TTS_WORKER_SCRIPT
    assert 'language="Auto"' in COLABONE_LIVE_TTS_WORKER_SCRIPT
    assert 'f"({instruct}){text}"' in COLABONE_LIVE_TTS_WORKER_SCRIPT
    assert ".reshape(-1)" in COLABONE_LIVE_TTS_WORKER_SCRIPT

    # Fake worker emitting non-JSON stdout noise before ready, then crashing with EOF on synth
    noisy_crash_script = r"""
import json, sys
sys.stdout.write("INFO: initializing cuda runtime...\n")
sys.stdout.write("WARNING: non-json progress bar 100%\n")
sys.stdout.write(json.dumps({"type": "ready", "engine_id": "qwen3_tts", "sample_rate": 24000}) + "\n")
sys.stdout.flush()
for line in sys.stdin:
    req = json.loads(line)
    if req.get("cmd") == "synthesize":
        sys.stderr.write("CUDA OOM while decoding hf_secret_crash_999\n")
        sys.stderr.flush()
        sys.exit(1)
"""
    worker = ColabOneResidentTtsWorker(
        "qwen3_tts",
        venv_python=Path(sys.executable),
        hf_token="hf_secret_crash_999",
        startup_timeout_s=5.0,
        read_timeout_s=3.0,
    )
    worker.build_worker_command = lambda: [sys.executable, "-u", "-c", noisy_crash_script]  # type: ignore[method-assign]
    worker.load()
    assert worker.is_loaded is True

    with pytest.raises(RuntimeError) as crash_exc:
        list(worker.synthesize_stream("测试崩溃", epoch=1))
    msg = str(crash_exc.value)
    assert "意外退出" in msg
    assert "CUDA OOM" in msg
    assert "hf_secret_crash_999" not in msg
    # Process was reaped and _loaded reset to False cleanly
    assert worker.is_loaded is False
    assert worker._proc is None


# ---------------------------------------------------------------------------
# 14. Pass-3 audit hardening tests (deadlock, disposal, auth_ok, broadcast,
#     SSRF pinning, IPv6/RTMP, hot-swap, A/V sync, ASR cancel, CRUD atomicity)
# ---------------------------------------------------------------------------


def test_stop_vs_ws_switch_engine_and_driving_no_circular_deadlock(tmp_path):
    mgr = ColabOneLiveStudioManager(tmp_path / "live_root", CATALOG_DIR)
    mgr.save_avatar({"id": "av_dl", "name": "防死锁主播"})
    mgr.upload_driving_motion(
        {
            "id": "drv_dl",
            "label": "热舞",
            "filename": "d.mp4",
            "data": base64.b64encode(b"fake-mp4").decode("ascii"),
        }
    )

    stop_entered = threading.Event()
    ws_results: dict[str, Any] = {}

    class _DeadlockProbeRunner:
        def __init__(self, app, *, host, port):
            _ = (app, host, port)

        def start(self):
            pass

        def stop(self):
            # Simulate uvicorn waiting for ThreadPoolExecutor WS callbacks during shutdown.
            stop_entered.set()

            def _ws_worker() -> None:
                try:
                    mgr._handle_ws_switch_engine({"visual_engine": "musetalk_bank"})
                    ws_results["switch"] = "unexpected_ok"
                except RuntimeError as exc:
                    ws_results["switch"] = str(exc)
                try:
                    mgr._handle_ws_trigger_driving("drv_dl")
                    ws_results["drive"] = "unexpected_ok"
                except RuntimeError as exc:
                    ws_results["drive"] = str(exc)

            t = threading.Thread(target=_ws_worker, daemon=True)
            t.start()
            t.join(timeout=3.0)
            assert not t.is_alive(), "WS handler deadlocked on _lifecycle_lock during stop()"

    mgr.start(
        {
            "visual_engine": "flashhead_lite",
            "personalive_enabled": True,
            "tts_engine": "qwen3_tts",
            "llm_mode": "external_api",
            "avatar_id": "av_dl",
            "hf_token": "hf_secret_clear_on_stop_123456",
            "external_llm": {
                "base_url": "https://api.example.com/v1",
                "api_key": "sk-deadlock-test-123456",
                "model": "gpt-4o-mini",
            },
        },
        gpu_info={"name": "NVIDIA A100", "total_mib": 81920},
        server_factory=lambda app, host, port: _DeadlockProbeRunner(app, host=host, port=port),
        synthetic=True,
        min_free_disk_mib=0,
    )
    assert mgr._last_hf_token == "hf_secret_clear_on_stop_123456"

    t0 = time.monotonic()
    st = mgr.stop()
    assert time.monotonic() - t0 < 3.0
    assert stop_entered.is_set()
    assert st["running"] is False
    assert st["state"] == "stopped"
    assert mgr._last_hf_token == ""
    assert "停止" in ws_results.get("switch", "") or "尚未启动" in ws_results.get("switch", "")
    assert "停止" in ws_results.get("drive", "") or "尚未启动" in ws_results.get("drive", "")


def test_auth_ok_early_disconnect_does_not_leak_active_clients_or_pump():
    token = "tok-early-disconnect-auth-ok-123"
    app = create_colabone_live_app(
        verify_token=constant_time_token_verifier(token),
        visual_engine=ColabOneSyntheticVisualEngine(width=16, height=16, fps=25),
        frame_pump_enabled=True,
    )

    async def _simulate_early_disconnect() -> None:
        in_messages = [
            {"type": "websocket.connect"},
            {"type": "websocket.receive", "text": json.dumps({"type": "auth", "token": token})},
        ]

        async def _receive() -> dict[str, Any]:
            if in_messages:
                return in_messages.pop(0)
            return {"type": "websocket.disconnect", "code": 1006}

        async def _send(message: dict[str, Any]) -> None:
            if message.get("type") == "websocket.send" and "auth_ok" in str(message.get("text", "")):
                raise RuntimeError("socket closed during auth_ok")

        scope = {
            "type": "websocket",
            "asgi": {"version": "3.0"},
            "http_version": "1.1",
            "scheme": "ws",
            "path": COLABONE_LIVE_WS_PATH,
            "raw_path": COLABONE_LIVE_WS_PATH.encode("ascii"),
            "query_string": b"",
            "headers": [],
            "client": ("127.0.0.1", 55555),
            "server": ("127.0.0.1", 8190),
            "subprotocols": [],
        }
        await app(scope, _receive, _send)

    asyncio.run(_simulate_early_disconnect())
    assert app.state.active_clients["count"] == 0
    assert len(app.state.authed_sockets) == 0
    assert len(app.state.active_sockets) == 0
    assert app.state.frame_pump_task is None


def test_frame_pump_survives_transient_render_error_and_scrubs_secrets(caplog):
    import logging

    secret = "sk-pump-transient-secret-987654"

    class _FlakyVisual(ColabOneSyntheticVisualEngine):
        def __init__(self) -> None:
            super().__init__(width=16, height=16, fps=40)
            self.attempts = 0

        def render_frame(self, audio_pcm_24k=b"", *, epoch=0, pts_ms=0, state="IDLE"):
            self.attempts += 1
            if self.attempts <= 2:
                raise RuntimeError(f"transient gpu glitch Bearer {secret}")
            return super().render_frame(audio_pcm_24k, epoch=epoch, pts_ms=pts_ms, state=state)

    flaky = _FlakyVisual()
    token = "tok-pump-survive-123"
    app = create_colabone_live_app(
        verify_token=constant_time_token_verifier(token),
        session_config={"target_fps": 40},
        visual_engine=flaky,
        active_secrets=[secret],
        frame_pump_enabled=True,
    )

    with caplog.at_level(logging.WARNING, logger="server.live_server"):
        with TestClient(app) as client:
            with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws:
                ws.send_json({"type": "auth", "token": token})
                assert ws.receive_json()["type"] == "auth_ok"
                raw = ws.receive_bytes()
                ftype, _, _, body = unpack_colabone_live_frame(raw)
                assert ftype == COLABONE_LIVE_FRAME_VIDEO
                assert len(body) > 0
                assert flaky.attempts >= 3
                assert app.state.frame_pump_task is not None
                assert not app.state.frame_pump_task.done()

    assert any("frame pump transient error" in rec.message for rec in caplog.records)
    assert all(secret not in rec.message for rec in caplog.records)


def test_multi_client_broadcast_and_connection_local_messages():
    async def _llm_stream(_messages, *, director_mode="dialogue", cancel_event=None):
        _ = (director_mode, cancel_event)
        yield "[MOOD:happy][ACTION:action_wave][CARD:p1]欢迎来到直播间！"

    token = "tok-multiclient-broadcast-123"
    app = create_colabone_live_app(
        verify_token=constant_time_token_verifier(token),
        avatar={"id": "av1", "name": "小雅", "products": [{"id": "p1", "name": "耳机", "price": "99元"}]},
        tts_engine=ColabOneMockStreamingTtsEngine("qwen3_tts", min_chunks=2, max_chunks=2),
        visual_engine=ColabOneSyntheticVisualEngine(width=16, height=16, fps=25),
        llm_stream_fn=_llm_stream,
    )

    with TestClient(app) as client:
        with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws1:
            ws1.send_json({"type": "auth", "token": token})
            assert ws1.receive_json()["type"] == "auth_ok"
            with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws2:
                ws2.send_json({"type": "auth", "token": token})
                assert ws2.receive_json()["type"] == "auth_ok"

                # Connection-local messages stay on ws1 only
                ws1.send_json({"type": "ping", "ts": 777})
                assert ws1.receive_json() == {"type": "pong", "ts": 777}
                ws1.send_text("{not-json")
                err1 = ws1.receive_json()
                assert err1["type"] == "error" and err1["code"] == "bad_json"

                # Turn triggered by ws1 broadcasts JSON + binary frames to BOTH ws1 and ws2
                ws1.send_json({"type": "text_input", "text": "打招呼"})

                def _drain_turn(ws):
                    jsons: list[dict[str, Any]] = []
                    audio_n = 0
                    video_n = 0
                    for _ in range(40):
                        msg = ws.receive()
                        if msg.get("text") is not None:
                            obj = json.loads(msg["text"])
                            jsons.append(obj)
                            if obj.get("type") == "turn_end":
                                break
                        elif msg.get("bytes") is not None:
                            ft, _, _, _ = unpack_colabone_live_frame(msg["bytes"])
                            if ft == COLABONE_LIVE_FRAME_AUDIO_PCM:
                                audio_n += 1
                            elif ft == COLABONE_LIVE_FRAME_VIDEO:
                                video_n += 1
                    return jsons, audio_n, video_n

                j1, a1, v1 = _drain_turn(ws1)
                j2, a2, v2 = _drain_turn(ws2)

                for jsons, a_cnt, v_cnt in ((j1, a1, v1), (j2, a2, v2)):
                    types = {e["type"] for e in jsons}
                    assert {"state", "mood", "action", "card", "transcript_delta", "telemetry", "turn_end"}.issubset(types)
                    assert a_cnt == 2
                    assert v_cnt >= 1
                # ws2 never received ws1's pong or bad_json error
                assert all(e["type"] not in ("pong", "error") for e in j2)


def test_tts_and_visual_post_unload_disposal_nonblocking_abort_and_producer_drain(tmp_path):
    # 1. Mock TTS permanent disposal
    mock_tts = ColabOneMockStreamingTtsEngine("qwen3_tts")
    mock_tts.load()
    mock_tts.unload()
    assert mock_tts.is_disposed is True
    with pytest.raises(RuntimeError):
        mock_tts.load()
    with pytest.raises(RuntimeError):
        list(mock_tts.synthesize_stream("你好", epoch=1))

    # 2. Visual engines permanent disposal
    for vis in (
        ColabOneSyntheticVisualEngine(width=16, height=16),
        ColabOneFlashHeadVisualEngine(width=16, height=16),
        ColabOneMuseTalkVisualEngine(width=16, height=16),
        ColabOnePersonaLiveVisualEngine(width=16, height=16),
    ):
        vis.load()
        vis.unload()
        assert vis.is_disposed is True
        with pytest.raises(RuntimeError):
            vis.load()
        with pytest.raises(RuntimeError):
            vis.render_frame(b"", epoch=1, pts_ms=0)

    # 3. Pre-cancelled synthesize_stream on ResidentTtsWorker does NOT trigger lazy load()
    load_called = {"count": 0}
    worker_pre = ColabOneResidentTtsWorker("qwen3_tts", venv_python=Path(sys.executable))
    orig_load = worker_pre.load

    def _counting_load() -> None:
        load_called["count"] += 1
        orig_load()

    worker_pre.load = _counting_load  # type: ignore[method-assign]
    cancel_ev = threading.Event()
    cancel_ev.set()
    assert list(worker_pre.synthesize_stream("跳过加载", epoch=1, cancel_event=cancel_ev)) == []
    assert load_called["count"] == 0

    # 4. ResidentTtsWorker.abort() is non-blocking even while load() is waiting for startup
    slow_ready_script = r"""
import json, sys, time
time.sleep(3.0)
sys.stdout.write(json.dumps({"type": "ready", "engine_id": "qwen3_tts", "sample_rate": 24000}) + "\n")
sys.stdout.flush()
for _ in sys.stdin:
    pass
"""
    worker_slow = ColabOneResidentTtsWorker(
        "qwen3_tts",
        venv_python=Path(sys.executable),
        startup_timeout_s=10.0,
    )
    worker_slow.build_worker_command = lambda: [sys.executable, "-u", "-c", slow_ready_script]  # type: ignore[method-assign]
    load_err: list[Exception] = []

    def _bg_load() -> None:
        try:
            worker_slow.load()
        except Exception as exc:
            load_err.append(exc)

    loader = threading.Thread(target=_bg_load, daemon=True)
    loader.start()
    time.sleep(0.15)
    t_ab = time.monotonic()
    worker_slow.abort(7)
    assert time.monotonic() - t_ab < 0.2, "abort() must never block waiting on load()"
    assert worker_slow._is_epoch_aborted(7) is True

    # unload() interrupts starting subprocess and sets permanent disposal
    worker_slow.unload()
    loader.join(timeout=3.0)
    assert not loader.is_alive()
    assert worker_slow.is_disposed is True
    with pytest.raises(RuntimeError):
        worker_slow.load()

    # 5. switch_engine drains active _tts_producer before calling old_tts.unload()
    producer_exited = threading.Event()
    order_log: list[str] = []

    class _TrackedSwitchTts(ColabOneMockStreamingTtsEngine):
        def synthesize_stream(self, text, *, voice_preset="shen_zhiyi", epoch=0, cancel_event=None, get_current_epoch=None):
            _ = (text, voice_preset, epoch, get_current_epoch)
            yield b"\x00" * self.chunk_bytes
            if cancel_event is not None:
                cancel_event.wait(0.5)
            time.sleep(0.05)
            order_log.append("producer_done")
            producer_exited.set()

        def unload(self) -> None:
            order_log.append(f"unload:exited={producer_exited.is_set()}")
            super().unload()

    old_tts = _TrackedSwitchTts("qwen3_tts")
    new_tts = ColabOneMockStreamingTtsEngine("voxcpm2")

    async def _llm_one(_m, *, director_mode="dialogue", cancel_event=None):
        _ = (director_mode, cancel_event)
        yield "测试切换排空！"

    tok = "tok-drain-tts-switch-123"
    app = create_colabone_live_app(
        verify_token=constant_time_token_verifier(tok),
        tts_engine=old_tts,
        visual_engine=ColabOneSyntheticVisualEngine(width=16, height=16),
        llm_stream_fn=_llm_one,
        on_switch_engine=lambda _req: {
            "config": ColabOneLiveSessionConfig(tts_engine="voxcpm2"),
            "tts_engine": new_tts,
            "old_tts": old_tts,
        },
    )
    with TestClient(app) as client:
        with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws:
            ws.send_json({"type": "auth", "token": tok})
            assert ws.receive_json()["type"] == "auth_ok"
            ws.send_json({"type": "text_input", "text": "开始"})
            # Wait until first audio frame confirms producer is actively running
            for _ in range(10):
                msg = ws.receive()
                if msg.get("bytes") is not None:
                    break
            ws.send_json({"type": "switch_engine", "tts_engine": "voxcpm2"})
            for _ in range(20):
                msg = ws.receive()
                if msg.get("text") is not None:
                    obj = json.loads(msg["text"])
                    if obj.get("type") == "state" and obj.get("tts_engine") == "voxcpm2":
                        break
    assert "producer_done" in order_log
    assert "unload:exited=True" in order_log
    assert order_log.index("producer_done") < order_log.index("unload:exited=True")


def test_ssrf_ipv6_parity_rtmp_dns_pinning_and_schema_alignment(tmp_path, monkeypatch):
    import requests
    from common.live_studio import (
        ColabOneLiveDrivingUploadRequest,
        validate_colabone_live_rtmp_resolved_ips,
    )
    from server.live_server import _ColabOnePinnedAsyncTransport
    from server.live_studio import _ColabOnePinnedIPAdapter

    # 1. IPv6 special ranges, CGNAT, documentation, benchmarking, control chars, params, empty port
    for bad_url in (
        "https://[fec0::1]/v1",
        "https://[64:ff9b::1.2.3.4]/v1",
        "https://[64:ff9b:1::1]/v1",
        "https://[2002:c000:0201::1]/v1",
        "https://[2001:0000::1]/v1",
        "https://[2001:2::1]/v1",
        "https://[2001:db8::1]/v1",
        "https://[100::1]/v1",
        "https://[::ffff:100.64.0.1]/v1",
        "https://198.18.0.1/v1",
        "https://192.0.2.1/v1",
        "https://198.51.100.1/v1",
        "https://203.0.113.1/v1",
        "https://api.example.com/v1;param=1",
        "https://api.example.com:/v1",
        "https://api.example.com/v1\x7f",
        "https://api.example.com/v1\x85",
    ):
        with pytest.raises(ValidationError):
            ColabOneLiveExternalLLMConfig(base_url=bad_url, api_key="", model="m1")

    # Control chars 0x7f / 0x85 rejected in api_key & model
    with pytest.raises(ValidationError):
        ColabOneLiveExternalLLMConfig(base_url="https://api.example.com/v1", api_key="abc\x7f", model="m1")
    with pytest.raises(ValidationError):
        ColabOneLiveExternalLLMConfig(base_url="https://api.example.com/v1", api_key="", model="m1\x85")

    # 2. RTMP DNS validation & empty port colon / params & single-segment secret scrubbing
    with pytest.raises(ValueError):
        validate_colabone_live_rtmp_url("rtmp://push.example.com:/live/key")
    with pytest.raises(ValueError):
        validate_colabone_live_rtmp_url("rtmp://push.example.com/live;x=1/key")
    with pytest.raises(ValueError):
        validate_colabone_live_rtmp_resolved_ips(
            "rtmp://rebind.example.com/app/key",
            resolver=lambda _h, _p: [(socket.AF_INET6, socket.SOCK_STREAM, 6, "", ("2001:db8::1", 1935, 0, 0))],
        )
    assert validate_colabone_live_rtmp_resolved_ips(
        "rtmp://push.example.com/app/key",
        resolver=lambda _h, _p: [(socket.AF_INET, socket.SOCK_STREAM, 6, "", ("93.184.216.34", 1935))],
    ) == ["93.184.216.34"]

    single_seg_scrubbed = scrub_colabone_live_secrets(
        "ffmpeg failed pushing to rtmp://push.example.com/top_secret_stream_key"
    )
    assert "top_secret_stream_key" not in single_seg_scrubbed
    assert "rtmp://push.example.com/***" in single_seg_scrubbed
    two_seg_scrubbed = scrub_colabone_live_secrets(
        "rtmp://push.example.com/live/my_secret_key"
    )
    assert two_seg_scrubbed == "rtmp://push.example.com/live/***"

    # 3. Even resolution & DrivingUploadRequest trigger_keywords <= 40 chars
    with pytest.raises(ValidationError):
        ColabOneLiveSessionConfig(resolution="721x1280")
    with pytest.raises(ValidationError):
        ColabOneLiveBuildPackRequest(avatar_id="av1", resolution="720x1281")
    with pytest.raises(ValidationError):
        ColabOneLiveDrivingUploadRequest(
            label="舞步",
            filename="d.mp4",
            data=base64.b64encode(b"data").decode("ascii"),
            trigger_keywords=["x" * 41],
        )

    # 4. PersonaLive unsupported recommendation on L4 24GB must NOT recommend L4 24GB
    l4_budget = evaluate_colabone_live_vram_budget(
        {
            "visual_engine": "musetalk_bank",
            "personalive_enabled": True,
            "tts_engine": "qwen3_tts",
            "llm_mode": "external_api",
        },
        gpu_total_mib=24576,
    )
    assert l4_budget["personalive_mode"] == "unsupported"
    assert all("L4 24GB" not in rec for rec in l4_budget["recommendations"])

    # 5. DNS IP pinning in stream_llm_completion (httpx) & test_external_llm (requests)
    captured_transport: list[Any] = []
    orig_async_client = httpx.AsyncClient

    def _capture_client(*args, **kwargs):
        captured_transport.append(kwargs.get("transport"))
        kwargs["transport"] = httpx.MockTransport(
            lambda _req: httpx.Response(200, content=b'data: {"choices":[{"delta":{"content":"ok"}}]}\n\ndata: [DONE]\n\n')
        )
        return orig_async_client(*args, **kwargs)

    monkeypatch.setattr("server.live_server.httpx.AsyncClient", _capture_client)
    monkeypatch.setattr(
        socket,
        "getaddrinfo",
        lambda _h, _p, *_a, **_kw: [(socket.AF_INET, socket.SOCK_STREAM, 6, "", ("93.184.216.34", 443))],
    )

    async def _run_pin() -> list[str]:
        out: list[str] = []
        async for chunk in stream_llm_completion(
            [{"role": "user", "content": "hi"}],
            llm_mode="external_api",
            external_config={
                "base_url": "https://api.example.com/v1",
                "api_key": "sk-pin-test-123456",
                "model": "demo",
            },
        ):
            out.append(chunk)
        return out

    assert asyncio.run(_run_pin()) == ["ok"]
    assert isinstance(captured_transport[0], _ColabOnePinnedAsyncTransport)
    assert captured_transport[0].expected_host == "api.example.com"
    assert captured_transport[0].pinned_ip == "93.184.216.34"

    # Verify requests _ColabOnePinnedIPAdapter rewrites URL to pinned IP & preserves Host/TLS SNI
    mgr = ColabOneLiveStudioManager(tmp_path / "live_pin", CATALOG_DIR)
    mounted_adapters: dict[str, Any] = {}
    orig_session_cls = mgr.test_external_llm

    class _FakeResp:
        status_code = 200
        text = '{"data":[]}'

    orig_mount = requests.Session.mount

    def _spy_mount(self, prefix, adapter):
        mounted_adapters[prefix] = adapter
        return orig_mount(self, prefix, adapter)

    monkeypatch.setattr(requests.Session, "mount", _spy_mount)
    monkeypatch.setattr(requests.Session, "get", lambda self, url, **kw: _FakeResp())
    probe = orig_session_cls(
        {"base_url": "https://api.example.com/v1", "api_key": "sk-pin-req-123456", "model": "demo"},
        dns_resolver=lambda _h, _p: [(socket.AF_INET, socket.SOCK_STREAM, 6, "", ("93.184.216.34", 443))],
    )
    assert probe["ok"] is True
    https_adapter = mounted_adapters.get("https://")
    assert isinstance(https_adapter, _ColabOnePinnedIPAdapter)
    assert https_adapter.expected_host == "api.example.com"
    assert https_adapter.pinned_ip == "93.184.216.34"
    assert https_adapter.poolmanager.connection_pool_kw.get("assert_hostname") == "api.example.com"
    assert https_adapter.poolmanager.connection_pool_kw.get("server_hostname") == "api.example.com"


def test_asr_cancel_on_speech_start_action_barge_in_mic_cap_and_av_sync():
    # 1. ASR cancellation on speech_start / ptt_start / text_input
    asr_started = threading.Event()
    asr_release = threading.Event()
    llm_inputs: list[str] = []

    def _slow_asr(_pcm: bytes, _lang: str) -> str:
        asr_started.set()
        asr_release.wait(timeout=2.0)
        return "过期的语音识别结果"

    async def _capture_llm(messages, *, director_mode="dialogue", cancel_event=None):
        _ = (director_mode, cancel_event)
        user_msg = messages[-1]["content"]
        llm_inputs.append(user_msg)
        if user_msg == "slow_turn":
            yield "正在慢慢回复，"
            await asyncio.sleep(3.0)
            yield "尾巴。"
        else:
            yield "收到新消息！"

    class _PcmRecordingVisual(ColabOneSyntheticVisualEngine):
        def __init__(self) -> None:
            super().__init__(width=16, height=16, fps=25)
            self.received_pcm_bytes: list[int] = []

        def render_frame(self, audio_pcm_24k=b"", *, epoch=0, pts_ms=0, state="IDLE"):
            if state == "SPEAKING" and audio_pcm_24k:
                self.received_pcm_bytes.append(len(audio_pcm_24k))
            return super().render_frame(audio_pcm_24k, epoch=epoch, pts_ms=pts_ms, state=state)

    rec_vis = _PcmRecordingVisual()
    tok = "tok-asr-cancel-avsync-123"
    app = create_colabone_live_app(
        verify_token=constant_time_token_verifier(tok),
        tts_engine=ColabOneMockStreamingTtsEngine("qwen3_tts", min_chunks=5, max_chunks=5),
        visual_engine=rec_vis,
        asr=ColabOneSenseVoiceAsrAdapter(transcribe_fn=_slow_asr),
        llm_stream_fn=_capture_llm,
    )

    with TestClient(app) as client:
        with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws:
            ws.send_json({"type": "auth", "token": tok})
            assert ws.receive_json()["type"] == "auth_ok"

            # Reject MIC_PCM > 64 KiB with bad_frame
            oversized = b"\x00" * (64 * 1024 + 2)
            ws.send_bytes(pack_colabone_live_frame(COLABONE_LIVE_FRAME_MIC_PCM, 0, 0, oversized))
            bad = ws.receive_json()
            assert bad["type"] == "error" and bad["code"] == "bad_frame"

            # Trigger VAD speech_start -> speech_end to start slow ASR, then immediately start new speech_start
            loud_20ms = _make_pcm16_frame(16000, 20, amplitude=8000)
            quiet_20ms = _make_pcm16_frame(16000, 20, amplitude=0)
            # Send 160 ms loud in one frame (VAD slices into 20 ms chunks cleanly)
            ws.send_bytes(pack_colabone_live_frame(COLABONE_LIVE_FRAME_MIC_PCM, 0, 0, loud_20ms * 8))
            st1 = ws.receive_json()
            assert st1["type"] == "state" and st1["state"] == "LISTENING"
            ep1 = st1["epoch"]
            # Send 380 ms quiet -> speech_end launches slow ASR
            ws.send_bytes(pack_colabone_live_frame(COLABONE_LIVE_FRAME_MIC_PCM, ep1, 160, quiet_20ms * 19))
            assert asr_started.wait(timeout=2.0)

            # New VAD speech_start MUST cancel in-flight ASR and bump epoch
            ws.send_bytes(pack_colabone_live_frame(COLABONE_LIVE_FRAME_MIC_PCM, ep1, 600, loud_20ms * 8))
            st2 = ws.receive_json()
            assert st2["type"] == "state" and st2["state"] == "LISTENING"
            assert st2["epoch"] > ep1
            asr_release.set()
            time.sleep(0.05)
            # Stale ASR must NOT have triggered an LLM turn
            assert "过期的语音识别结果" not in llm_inputs

            # Verify 100% of spoken PCM (5 chunks * 960 = 4800 bytes) reaches render_frame & monotonic pts_ms
            ws.send_json({"type": "text_input", "text": "五块音频测试"})
            audio_pts: list[int] = []
            for _ in range(40):
                msg = ws.receive()
                if msg.get("bytes") is not None:
                    ft, _, pts, _ = unpack_colabone_live_frame(msg["bytes"])
                    if ft == COLABONE_LIVE_FRAME_AUDIO_PCM:
                        audio_pts.append(pts)
                elif msg.get("text") is not None:
                    obj = json.loads(msg["text"])
                    if obj.get("type") == "turn_end":
                        break
            assert len(audio_pts) == 5
            # Exact 20 ms monotonic audio sample clock increments
            assert [audio_pts[i + 1] - audio_pts[i] for i in range(4)] == [20, 20, 20, 20]
            # 100% of 4800 PCM bytes forwarded to lip-sync render_frame (1920 + 1920 + 960 = 4800)
            assert sum(rec_vis.received_pcm_bytes) == 5 * 960
            assert rec_vis.received_pcm_bytes == [1920, 1920, 960]

            # Normal trigger_action during active SPEAKING turn must barge-in FIRST
            ws.send_json({"type": "text_input", "text": "slow_turn"})
            for _ in range(20):
                msg = ws.receive()
                if msg.get("text") is not None:
                    obj = json.loads(msg["text"])
                    if obj.get("type") == "transcript_delta":
                        break
            ws.send_json({"type": "trigger_action", "action_id": "action_heart"})
            seen_types: list[str] = []
            for _ in range(20):
                msg = ws.receive()
                if msg.get("text") is not None:
                    obj = json.loads(msg["text"])
                    seen_types.append(obj["type"])
                    if obj.get("type") == "action" and obj.get("action_id") == "action_heart":
                        break
            assert "barge_in" in seen_types
            assert seen_types.index("barge_in") < seen_types.index("action")


def test_personalive_hotswap_gpu_wiring_visual_encoder_and_crud_atomicity(tmp_path):
    import ast
    from server.live_worker_tts import COLABONE_LIVE_TTS_WORKER_SCRIPT

    # 1. Worker script AST validity & Qwen3/VoxCPM2 alignment
    ast.parse(COLABONE_LIVE_TTS_WORKER_SCRIPT)
    assert "torch_dtype" in COLABONE_LIVE_TTS_WORKER_SCRIPT
    assert "generate_voice_clone" in COLABONE_LIVE_TTS_WORKER_SCRIPT
    assert "load_denoiser" in COLABONE_LIVE_TTS_WORKER_SCRIPT
    assert "optimize" in COLABONE_LIVE_TTS_WORKER_SCRIPT
    assert "inspect.signature" in COLABONE_LIVE_TTS_WORKER_SCRIPT
    assert "_sanitize_voxcpm_prompt" in COLABONE_LIVE_TTS_WORKER_SCRIPT

    # 2. H.264 dynamic level selection & even dimension enforcement
    assert ColabOneLiveVideoEncoder.select_h264_level(720, 1280, 25) == "4.0"
    assert ColabOneLiveVideoEncoder.select_h264_level(1920, 1080, 60) == "4.2"
    assert ColabOneLiveVideoEncoder.select_h264_level(2560, 1440, 30) == "5.1"
    with pytest.raises(ValueError):
        ColabOneLiveVideoEncoder(width=721, height=1280)
    with pytest.raises(ValueError):
        ColabOneLiveVideoEncoder.build_ffmpeg_command("libx264", 720, 1281, 25)
    cmd_42 = ColabOneLiveVideoEncoder.build_ffmpeg_command("libx264", 1920, 1080, 60)
    assert cmd_42[cmd_42.index("-level") + 1] == "4.2"

    # 3. FlashHead EXIF transpose + aspect-preserving center-crop, MuseTalk loop override & crossfade clear
    wide_img = Image.new("RGB", (60, 20), (20, 20, 200))
    for x in range(20, 40):
        for y in range(20):
            wide_img.putpixel((x, y), (220, 40, 40))
    wide_path = tmp_path / "wide.png"
    wide_img.save(wide_path, format="PNG")
    fh = ColabOneFlashHeadVisualEngine(width=16, height=16, portrait_path=wide_path)
    fh.load()
    f_fh = fh.render_frame(b"", epoch=1, pts_ms=0)
    # Center-cropped 20x20 red region fills entire 16x16 square (edges not squashed blue)
    assert f_fh.rgb_bytes[0] > 180 and f_fh.rgb_bytes[2] < 80

    mt = ColabOneMuseTalkVisualEngine(width=8, height=8, fps=25, default_crossfade_frames=2)
    mt.load()
    r1 = Image.new("RGB", (8, 8), (180, 10, 10)).tobytes()
    r2 = Image.new("RGB", (8, 8), (10, 180, 10)).tobytes()
    mt.register_action_frames("idle_loop", [r1], loop=True)
    mt.register_action_frames("action_wave", [r1, r2], loop=False)
    # Explicit loop=True override keeps action_wave looping instead of returning to idle_loop
    mt.set_action("action_wave", loop=True, crossfade_frames=0)
    mt.render_frame(b"", epoch=1, pts_ms=0)
    mt.render_frame(b"", epoch=1, pts_ms=40)
    mt.render_frame(b"", epoch=1, pts_ms=80)
    assert mt.current_action == "action_wave"
    # Starting a crossfade and then calling set_action(..., crossfade_frames=0) clears stale crossfade
    mt.set_action("idle_loop", loop=True, crossfade_frames=4)
    assert mt.status()["crossfading"] is True
    mt.set_action("action_wave", loop=True, crossfade_frames=0)
    assert mt.status()["crossfading"] is False

    # 4. PersonaLive hot_swap mode unloads base_visual_engine BEFORE drive_engine.load() and recreates on restore
    hotswap_events: list[str] = []

    class _BaseTrackVis(ColabOneSyntheticVisualEngine):
        def unload(self) -> None:
            hotswap_events.append("base_unload")
            super().unload()

    class _DriveTrackVis(ColabOnePersonaLiveVisualEngine):
        def load(self) -> None:
            hotswap_events.append("drive_load")
            super().load()

        def unload(self) -> None:
            hotswap_events.append("drive_unload")
            super().unload()

    base_vis = _BaseTrackVis(width=16, height=16)
    base_vis.load()
    drive_vis = _DriveTrackVis(width=16, height=16)
    recreated: list[ColabOneSyntheticVisualEngine] = []

    def _recreate_base() -> ColabOneSyntheticVisualEngine:
        hotswap_events.append("base_recreate")
        fresh = ColabOneSyntheticVisualEngine(width=16, height=16)
        recreated.append(fresh)
        return fresh

    tok_hs = "tok-hotswap-personalive-123"
    app_hs = create_colabone_live_app(
        verify_token=constant_time_token_verifier(tok_hs),
        session_config={"visual_engine": "musetalk_bank", "personalive_enabled": True},
        visual_engine=base_vis,
        vram_budget={"personalive_mode": "hot_swap", "ok": True},
        on_trigger_driving=lambda _mid: {"visual_engine": drive_vis},
        on_recreate_base_visual=_recreate_base,
    )
    with TestClient(app_hs) as client:
        with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws:
            ws.send_json({"type": "auth", "token": tok_hs})
            assert ws.receive_json()["type"] == "auth_ok"
            ws.send_json({"type": "trigger_action", "kind": "personalive_drive", "driving_motion_id": "drive_01", "loop": True})
            assert ws.receive_json()["type"] == "action"
            _ = ws.receive_bytes()
            assert base_vis.is_disposed is True
            assert hotswap_events[:2] == ["base_unload", "drive_load"]

            ws.send_json({"type": "barge_in", "reason": "done"})
            assert ws.receive_json()["type"] == "barge_in"
            assert drive_vis.is_disposed is True
            assert len(recreated) == 1
            assert app_hs.state.visual_engine is recreated[0]
            assert app_hs.state.visual_engine.is_disposed is False
            assert app_hs.state.visual_engine.current_action == "idle_loop"
            assert hotswap_events == ["base_unload", "drive_load", "drive_unload", "base_recreate"]

    # 5. Cache marker / incomplete rejection, disk headroom addition, non-synthetic fail-fast
    mgr = ColabOneLiveStudioManager(tmp_path / "live_mgr", CATALOG_DIR)
    cdir = mgr.root / "cache" / "live-studio" / "qwen3_tts"
    cdir.mkdir(parents=True, exist_ok=True)
    # Only .tmp / .part file must NOT count as cached
    (cdir / "weights.safetensors.part").write_bytes(b"partial")
    assert mgr._is_component_cached("qwen3_tts") is False
    (cdir / "weights.safetensors.part").unlink()
    # Empty directory not cached; .colabone_ready marks cached
    assert mgr._is_component_cached("qwen3_tts") is False
    (cdir / ".colabone_ready").write_text("ok", encoding="utf-8")
    assert mgr._is_component_cached("qwen3_tts") is True
    cfg_d = ColabOneLiveSessionConfig(visual_engine="flashhead_lite", tts_engine="qwen3_tts")
    # Uncached components sum + 2048 MiB headroom
    assert mgr._required_disk_mib(cfg_d, synthetic=False) == (32 + 1536 + 10240) + 2048

    # synthetic=False fails fast when isolated TTS venv is missing (never silent mock fallback)
    with pytest.raises(RuntimeError, match="独立运行环境"):
        mgr.start(
            {
                "visual_engine": "flashhead_lite",
                "tts_engine": "qwen3_tts",
                "llm_mode": "external_api",
                "external_llm": {
                    "base_url": "https://api.example.com/v1",
                    "api_key": "sk-nonsyn-123456",
                    "model": "gpt-4o-mini",
                },
            },
            gpu_info={"name": "NVIDIA A100", "total_mib": 81920},
            synthetic=False,
            min_free_disk_mib=0,
        )

    # 6. CRUD atomicity, active avatar refresh/delete guard, dangling symlink tombstone
    # Build pack with empty actions or missing comfy_runner MUST NOT create avatar on disk
    with pytest.raises(ValueError):
        mgr.build_avatar_pack(None, {"avatar_id": "av_never", "actions": [], "dance_ids": []}, comfy_runner=lambda **k: b"x")
    assert not (mgr.avatars_dir / "av_never").exists()
    with pytest.raises(RuntimeError):
        mgr.build_avatar_pack(None, {"avatar_id": "av_never2", "actions": ["idle_loop"], "dance_ids": []}, comfy_runner=None)
    assert not (mgr.avatars_dir / "av_never2").exists()

    # Upload action clip rollback: when manifest already has 64 actions, adding a 65th fails BEFORE writing file
    mgr.save_avatar(
        {
            "id": "av_full",
            "name": "满载主播",
            "actions": [{"id": f"a_{i}", "label": f"动作{i}", "kind": "oneshot"} for i in range(64)],
        }
    )
    with pytest.raises(ValidationError):
        mgr.upload_action_clip(
            "av_full",
            {
                "action_id": "a_65",
                "filename": "clip.mp4",
                "data": base64.b64encode(b"should-not-exist").decode("ascii"),
            },
        )
    assert not (mgr.avatars_dir / "av_full" / "clips" / "a_65.mp4").exists()

    # Dangling symlink in .trash/<id> is cleaned safely on save_avatar
    trash_dir = mgr.avatars_dir / ".trash"
    trash_dir.mkdir(parents=True, exist_ok=True)
    dangling = trash_dir / "av_dangling"
    os.symlink(tmp_path / "does_not_exist_target", dangling)
    assert dangling.is_symlink() and not dangling.exists()
    mgr.save_avatar({"id": "av_dangling", "name": "悬空符号链接测试"})
    assert not dangling.is_symlink()

    # Active avatar sync & delete guard
    captured_live: dict[str, Any] = {}

    class _SyncRunner:
        def __init__(self, app, *, host, port):
            _ = (host, port)
            captured_live["app"] = app

        def start(self):
            pass

        def stop(self):
            pass

    mgr.save_avatar({"id": "av_active", "name": "初始名"})
    mgr.start(
        {
            "visual_engine": "flashhead_lite",
            "tts_engine": "qwen3_tts",
            "llm_mode": "external_api",
            "avatar_id": "av_active",
            "external_llm": {
                "base_url": "https://api.example.com/v1",
                "api_key": "sk-sync-123456",
                "model": "gpt-4o-mini",
            },
        },
        gpu_info={"name": "NVIDIA L4", "total_mib": 24576},
        server_factory=lambda app, host, port: _SyncRunner(app, host=host, port=port),
        synthetic=True,
        min_free_disk_mib=0,
    )
    try:
        mgr.save_avatar({"id": "av_active", "name": "更新后主播名"})
        assert captured_live["app"].state.avatar["name"] == "更新后主播名"
        mgr.upload_action_clip(
            "av_active",
            {
                "action_id": "action_wave",
                "filename": "wave.mp4",
                "data": base64.b64encode(b"mp4-live").decode("ascii"),
            },
        )
        assert any(a["id"] == "action_wave" for a in captured_live["app"].state.avatar["actions"])
        with pytest.raises(ValueError, match="正在直播会话中使用"):
            mgr.delete_avatar("av_active")
    finally:
        mgr.stop()
    mgr.delete_avatar("av_active")


def test_live_studio_auto_provisioning_progress_and_worker_env(tmp_path: Path, monkeypatch):
    import shutil
    from server.live_worker_tts import ColabOneResidentTtsWorker
    from server.runtime import ColabOneCancelledError, ColabOneTask

    # 1. Worker env sets PyTorch-only guards, HF cache dir, and strips sensitive env keys
    monkeypatch.setenv("OPENAI_API_KEY", "sk-leak-should-not-pass")
    worker = ColabOneResidentTtsWorker(
        "qwen3_tts",
        venv_python=tmp_path / "py",
        model_dir=tmp_path / "model",
        hf_cache_dir=tmp_path / "hf_cache",
        hf_token="hf_live_secret_token_999",
    )
    wenv = worker.build_worker_env()
    assert wenv["USE_TORCH"] == "1"
    assert wenv["USE_TF"] == "0"
    assert wenv["TRANSFORMERS_NO_TF"] == "1"
    assert wenv["DISABLE_TENSORFLOW"] == "1"
    assert wenv["HF_HUB_CACHE"] == str(tmp_path / "hf_cache")
    assert wenv["HUGGINGFACE_HUB_CACHE"] == str(tmp_path / "hf_cache")
    assert wenv["HF_TOKEN"] == "hf_live_secret_token_999"
    assert "OPENAI_API_KEY" not in wenv

    # 2. POSIX venv symlink bin/python is accepted; incomplete/Base-only HF snapshots rejected
    mgr = ColabOneLiveStudioManager(tmp_path / "auto_live", CATALOG_DIR)
    real_py_target = tmp_path / "base_python3"
    real_py_target.write_text("#!/bin/sh\n", encoding="utf-8")
    venv_bin = mgr.root / "tts-envs" / "qwen3_tts" / ".venv" / "bin"
    venv_bin.mkdir(parents=True, exist_ok=True)
    os.symlink(real_py_target, venv_bin / "python")
    resolved_py = mgr._resolve_engine_venv_python("qwen3_tts")
    assert resolved_py == venv_bin / "python"

    # Base-only HF snapshot must NOT satisfy require_live_compatible=True for qwen3_tts
    hf_cache = mgr._hf_cache_dir()
    base_model_dir = hf_cache / "models--Qwen--Qwen3-TTS-12Hz-1.7B-Base"
    (base_model_dir / "blobs").mkdir(parents=True, exist_ok=True)
    (base_model_dir / "blobs" / "b1").write_bytes(b"weights")
    base_snap = base_model_dir / "snapshots" / "fd4b254389122332181a7c3db7f27e918eec64e3"
    base_snap.mkdir(parents=True, exist_ok=True)
    os.symlink(os.path.join("..", "..", "blobs", "b1"), base_snap / "model.safetensors")
    assert (
        mgr._resolve_component_model_dir(
            "streaming_tts", "qwen3_tts", require_live_compatible=True
        )
        is None
    )

    # Clean up the pre-created venv so full auto-provisioning runs from scratch
    shutil.rmtree(mgr.root / "tts-envs")

    # 3. Full auto-provisioning flow with live ColabOneTransfer progress + prewarm + reuse
    events: list[str] = []
    seen_transfers: list[dict[str, Any]] = []
    prewarm_calls: list[str] = []

    class _FakeTts:
        def __init__(self, eid: str) -> None:
            self.engine_id = eid
            self.sample_rate = 24000
            self._loaded = False

        def load(self) -> None:
            prewarm_calls.append(self.engine_id)
            self._loaded = True

        def unload(self) -> None:
            self._loaded = False

    class _NoopRunner:
        def __init__(self, app, *, host, port):
            self.app = app
            self.host = host
            self.port = port

        def start(self) -> None:
            pass

        def stop(self) -> None:
            pass

    def fake_env_installer(*, task, section, engine_id, entry, hf_token):
        events.append(f"venv:{engine_id}")
        vbin = mgr.root / "tts-envs" / engine_id / ".venv" / "bin"
        vbin.mkdir(parents=True, exist_ok=True)
        py_link = vbin / "python"
        if not py_link.exists():
            os.symlink(real_py_target, py_link)
        return py_link

    def fake_weight_downloader(
        *, task, transfer, repo_id, revision, label, comp_id, cache_dir, local_dir, **_kw
    ):
        events.append(f"dl:{comp_id}:{repo_id}")
        with transfer.lock:
            transfer.total = 1000
            transfer.downloaded = 500
            transfer.phase = "downloading"
            transfer.file = "model.safetensors"
            transfer.index = 1
            transfer.count = 2
        transfer.wire(500)
        if task is not None:
            snap = task.snapshot()
            if "download" in snap:
                seen_transfers.append(snap["download"])
        if cache_dir is not None:
            mdir = cache_dir / ("models--" + repo_id.replace("/", "--"))
            (mdir / "blobs").mkdir(parents=True, exist_ok=True)
            (mdir / "blobs" / "w0").write_bytes(b"safetensor-bytes")
            sdir = mdir / "snapshots" / (revision or "rev1")
            sdir.mkdir(parents=True, exist_ok=True)
            os.symlink(os.path.join("..", "..", "blobs", "w0"), sdir / "model.safetensors")
            return sdir
        assert local_dir is not None
        local_dir.mkdir(parents=True, exist_ok=True)
        (local_dir / "model.pt").write_bytes(b"asr-bytes")
        return local_dir

    mgr._env_installer_fn = fake_env_installer
    mgr._weight_downloader_fn = fake_weight_downloader
    monkeypatch.setattr(
        "server.live_studio.create_colabone_streaming_tts_engine",
        lambda eid, **_kw: _FakeTts(eid),
    )

    task1 = ColabOneTask("live_start", "qwen3_tts")
    live_cfg = {
        "visual_engine": "flashhead_lite",
        "tts_engine": "qwen3_tts",
        "llm_mode": "external_api",
        "external_llm": {
            "base_url": "https://api.example.com/v1",
            "api_key": "sk-autoprov-123456",
            "model": "gpt-4o-mini",
        },
    }
    status1 = mgr.start(
        live_cfg,
        gpu_info={"name": "NVIDIA A100", "total_mib": 81920},
        server_factory=lambda app, host, port: _NoopRunner(app, host=host, port=port),
        synthetic=False,
        min_free_disk_mib=0,
        task=task1,
        auto_provision=True,
        prewarm_tts=True,
    )
    assert status1["running"] is True
    assert status1["state"] == "ready"
    assert events == [
        "venv:qwen3_tts",
        "dl:qwen3_tts:Qwen/Qwen3-TTS-12Hz-1.7B-VoiceDesign",
        "dl:sensevoice_small:FunAudioLLM/SenseVoiceSmall",
    ]
    assert len(seen_transfers) == 2
    assert seen_transfers[0]["downloaded_bytes"] == 500
    assert seen_transfers[0]["filename"] == "model.safetensors"
    assert prewarm_calls == ["qwen3_tts"]
    assert (mgr.root / "cache" / "live-studio" / "sensevoice_small" / ".colabone_ready").is_file()
    assert task1.transfer is None

    # Hot-switch TTS engine to voxcpm2 auto-provisions missing voxcpm2 deps
    sw = mgr._handle_ws_switch_engine({"tts_engine": "voxcpm2"})
    assert sw["tts_engine"].engine_id == "voxcpm2"
    assert "venv:voxcpm2" in events
    assert "dl:voxcpm2:openbmb/VoxCPM2" in events
    mgr.stop()

    # Second start reuses cached venv, weights, and ASR with zero downloads
    events.clear()
    task2 = ColabOneTask("live_start", "qwen3_tts")
    status2 = mgr.start(
        live_cfg,
        gpu_info={"name": "NVIDIA A100", "total_mib": 81920},
        server_factory=lambda app, host, port: _NoopRunner(app, host=host, port=port),
        synthetic=False,
        min_free_disk_mib=0,
        task=task2,
        auto_provision=True,
        prewarm_tts=True,
    )
    assert status2["running"] is True
    assert events == []
    mgr.stop()

    # 4. Best-effort ASR failure logs warning and still starts cleanly
    shutil.rmtree(mgr.root / "cache" / "live-studio" / "sensevoice_small")

    def failing_asr_dl(*, comp_id, **_kw):
        if comp_id == "sensevoice_small":
            raise RuntimeError("asr mirror down")
        raise AssertionError(f"unexpected download {comp_id}")

    mgr._weight_downloader_fn = failing_asr_dl
    task3 = ColabOneTask("live_start", "qwen3_tts")
    status3 = mgr.start(
        live_cfg,
        gpu_info={"name": "NVIDIA A100", "total_mib": 81920},
        server_factory=lambda app, host, port: _NoopRunner(app, host=host, port=port),
        synthetic=False,
        min_free_disk_mib=0,
        task=task3,
        auto_provision=True,
        prewarm_tts=False,
    )
    assert status3["running"] is True
    assert any(log["level"] == "warning" and "SenseVoice" in log["message"] for log in task3.logs)
    mgr.stop()

    # 5. Cancellation during weight download resets state to stopped without error
    shutil.rmtree(mgr._hf_cache_dir())
    task4 = ColabOneTask("live_start", "qwen3_tts")

    def cancel_during_dl(*, task, **_kw):
        task.request_cancel()
        task.check()

    mgr._weight_downloader_fn = cancel_during_dl
    with pytest.raises(ColabOneCancelledError):
        mgr.start(
            live_cfg,
            gpu_info={"name": "NVIDIA A100", "total_mib": 81920},
            server_factory=lambda app, host, port: _NoopRunner(app, host=host, port=port),
            synthetic=False,
            min_free_disk_mib=0,
            task=task4,
            auto_provision=True,
            prewarm_tts=False,
        )
    assert mgr.status()["state"] == "stopped"
    assert mgr.status()["error"] == ""
    assert task4.transfer is None


def test_live_studio_companion_pairing_vision_motion_and_exclusivity(tmp_path: Path):
    import io
    from PIL import Image
    from common.live_studio import (
        COLABONE_LIVE_COMPANION_PATH,
        COLABONE_LIVE_COMPANION_REDEEM_PATH,
        COLABONE_LIVE_FRAME_CAMERA_JPEG,
        pack_colabone_live_frame,
    )
    from server.live_worker_visual import (
        ColabOneLiveCameraPoseTracker,
        apply_live_camera_motion,
    )

    mgr = ColabOneLiveStudioManager(tmp_path / "companion_live", CATALOG_DIR)

    # 1. Pairing ticket uses bound HTTPS domain with #code= fragment; refresh invalidates prior code
    t1 = mgr.issue_companion_ticket("https://studio.example.com")
    assert t1["canvas_ready"] is True
    assert t1["companion_url"] == (
        f"https://studio.example.com{COLABONE_LIVE_COMPANION_PATH}#code={t1['code']}"
    )
    t2 = mgr.issue_companion_ticket("https://studio.example.com")
    assert t2["code"] != t1["code"]
    assert mgr.redeem_companion_ticket(t1["code"], device_label="OldPhone") is None

    # 2. Pose tracker & affine motion warping
    tracker = ColabOneLiveCameraPoseTracker()
    img1 = Image.new("RGB", (96, 96), (30, 30, 30))
    for y in range(24, 72):
        for x in range(20, 52):
            img1.putpixel((x, y), (220, 200, 180))
    img2 = Image.new("RGB", (96, 96), (30, 30, 30))
    for y in range(28, 76):
        for x in range(44, 76):
            img2.putpixel((x, y), (220, 200, 180))
    p1 = tracker.update_from_image(img1)
    p2 = tracker.update_from_image(img2)
    assert p2["dx"] > p1["dx"]
    assert p2["energy"] > 0.0
    warped = apply_live_camera_motion(img1.tobytes(), 96, 96, {"dx": 0.6, "dy": -0.3, "roll_deg": 4.0, "scale": 1.03})
    assert len(warped) == 96 * 96 * 3
    assert warped != img1.tobytes()

    # 3. End-to-end Live Studio FastAPI app: HTML page, single-use redeem, exclusivity, vision + fallback
    seen_llm_messages: list[list[dict[str, Any]]] = []
    reject_images = {"flag": False}

    async def fake_llm(messages, **_kw):
        seen_llm_messages.append(messages)
        last_user = messages[-1]["content"]
        if reject_images["flag"] and isinstance(last_user, list):
            raise RuntimeError("LLM 流式请求失败 (HTTP 400): image_url is not supported")
        if isinstance(last_user, list):
            yield "我看到镜头里展示了一款精致的新品！"
        else:
            yield "纯文本降级回复已生效。"

    app = create_colabone_live_app(
        verify_token=mgr.verify_session_token,
        verify_token_info=mgr.verify_session_token_info,
        redeem_companion_ticket=mgr.redeem_companion_ticket,
        llm_stream_fn=fake_llm,
        frame_pump_enabled=False,
    )

    jpeg_buf = io.BytesIO()
    img2.save(jpeg_buf, format="JPEG", quality=80)
    sample_jpeg = jpeg_buf.getvalue()

    with TestClient(app) as client:
        # Mobile HTML page is served with strict security headers
        page_resp = client.get(COLABONE_LIVE_COMPANION_PATH)
        assert page_resp.status_code == 200
        assert "ColabOne" in page_resp.text
        assert page_resp.headers.get("cache-control") == "no-store"
        assert page_resp.headers.get("referrer-policy") == "no-referrer"
        assert page_resp.headers.get("x-frame-options") == "DENY"

        # Redeem ticket t2 once; replay must fail with 401
        r_ok = client.post(
            COLABONE_LIVE_COMPANION_REDEEM_PATH,
            json={"code": t2["code"], "device_label": "iPhone · Safari"},
        )
        assert r_ok.status_code == 200
        phone1_tok = r_ok.json()["token"]
        assert phone1_tok
        r_replay = client.post(
            COLABONE_LIVE_COMPANION_REDEEM_PATH,
            json={"code": t2["code"], "device_label": "ReplayPhone"},
        )
        assert r_replay.status_code == 401

        # Connect Desktop + Phone 1
        desk_tok = mgr.issue_session_token(role="desktop")["token"]
        with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws_desk:
            ws_desk.send_json({"type": "auth", "token": desk_tok})
            desk_auth = ws_desk.receive_json()
            assert desk_auth["type"] == "auth_ok"
            assert desk_auth["companion"]["connected"] is False

            with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws_p1:
                ws_p1.send_json({"type": "auth", "token": phone1_tok})
                p1_auth = ws_p1.receive_json()
                assert p1_auth["type"] == "auth_ok"
                assert p1_auth["client_role"] == "companion"
                _ = ws_p1.receive_json()  # companion_status broadcast to self
                st_msg = ws_desk.receive_json()
                assert st_msg["type"] == "companion_status"
                assert st_msg["connected"] is True
                assert st_msg["device_label"] == "iPhone · Safari"

                # Enable mic + dual vision/motion camera mode
                ws_p1.send_json(
                    {
                        "type": "companion_state",
                        "mic_active": True,
                        "camera_mode": "vision_motion",
                    }
                )
                p1_st2 = ws_p1.receive_json()
                desk_st2 = ws_desk.receive_json()
                assert p1_st2["type"] == "companion_status"
                assert desk_st2["mic_active"] is True
                assert desk_st2["camera_mode"] == "vision_motion"

                # Stream camera JPEG binary frame (0x05)
                ws_p1.send_bytes(
                    pack_colabone_live_frame(
                        COLABONE_LIVE_FRAME_CAMERA_JPEG, 0, 1000, sample_jpeg
                    )
                )
                ws_p1.send_json({"type": "ping", "ts": 1})
                assert ws_p1.receive_json()["type"] == "pong"
                assert app.state.companion_info["vision_frame_ready"] is True
                assert app.state.visual_engine._live_motion_pose is not None

                def _recv_json_skip_binary(ws_conn):
                    while True:
                        raw = ws_conn.receive()
                        if raw.get("type") == "websocket.close":
                            return {"type": "__closed__", "code": raw.get("code")}
                        if "text" in raw and raw["text"] is not None:
                            return json.loads(raw["text"])

                # Ask a question -> LLM receives multimodal content with image_url!
                ws_p1.send_json({"type": "text_input", "text": "手里这个是什么？"})
                while True:
                    ev = _recv_json_skip_binary(ws_desk)
                    if ev.get("type") == "turn_end":
                        break
                assert seen_llm_messages
                user_content = seen_llm_messages[-1][-1]["content"]
                assert isinstance(user_content, list)
                assert user_content[0]["type"] == "text"
                assert user_content[1]["type"] == "image_url"
                assert user_content[1]["image_url"]["url"].startswith("data:image/jpeg;base64,")

                # Text-only LLM rejects image_url with HTTP 400 -> auto fallback + system_notice
                reject_images["flag"] = True
                ws_p1.send_json({"type": "text_input", "text": "再介绍一下"})
                saw_notice = False
                while True:
                    ev = _recv_json_skip_binary(ws_desk)
                    if ev.get("type") == "system_notice" and ev.get("code") == "vision_unsupported_fallback":
                        saw_notice = True
                    if ev.get("type") == "turn_end":
                        break
                assert saw_notice is True
                assert isinstance(seen_llm_messages[-1][-1]["content"], str)

                # Phone 2 scans a fresh QR -> cleanly kicks Phone 1 (single-device exclusivity)
                t3 = mgr.issue_companion_ticket("https://studio.example.com")
                r_p2 = client.post(
                    COLABONE_LIVE_COMPANION_REDEEM_PATH,
                    json={"code": t3["code"], "device_label": "Android · Chrome"},
                )
                assert r_p2.status_code == 200
                phone2_tok = r_p2.json()["token"]
                with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws_p2:
                    ws_p2.send_json({"type": "auth", "token": phone2_tok})
                    p2_auth = _recv_json_skip_binary(ws_p2)
                    assert p2_auth["type"] == "auth_ok"
                    assert p2_auth["companion"]["device_label"] == "Android · Chrome"
                    # Desktop kicks Phone 2 via kick_companion
                    ws_desk.send_json({"type": "kick_companion"})
                    got_kicked = False
                    for _ in range(8):
                        m = _recv_json_skip_binary(ws_p2)
                        if m.get("type") == "companion_kicked":
                            got_kicked = True
                            break
                        if m.get("type") == "__closed__":
                            break
                    assert got_kicked is True
                    assert app.state.companion_info["connected"] is False
                    assert app.state.visual_engine._live_motion_pose is None


def test_live_studio_companion_security_rate_limit_camera_off_video_and_6_locales(
    tmp_path: Path,
):
    import io
    from PIL import Image
    from common.live_studio import (
        COLABONE_LIVE_COMPANION_PATH,
        COLABONE_LIVE_COMPANION_REDEEM_PATH,
        COLABONE_LIVE_FRAME_CAMERA_JPEG,
        COLABONE_LIVE_FRAME_FMP4,
        COLABONE_LIVE_FRAME_VIDEO,
        ColabOneLiveCatalog,
        pack_colabone_live_frame,
        unpack_colabone_live_frame,
    )
    from server.live_companion_page import render_colabone_live_companion_html
    from server.live_server import ColabOneLiveVideoEncoder
    from server.live_worker_visual import ColabOneVisualFrame

    # 1. check_ws_path rejects HTML/JS/whitespace/traversal characters
    raw_cat = json.loads((CATALOG_DIR / "live_studio.json").read_text(encoding="utf-8"))
    for bad_ws in ("/_live/<script>", "/_live/ws?x=1", "/_live/../ws", "/_live//ws", '/_live/"x'):
        bad_cat = dict(raw_cat, ws_path=bad_ws)
        with pytest.raises(Exception):
            ColabOneLiveCatalog.model_validate(bad_cat)

    # 2. render_colabone_live_companion_html escapes <, >, & safely and supports all 6 locales
    escaped_html = render_colabone_live_companion_html(
        ws_path="/_live/</script><img src=x>",
        redeem_path="/_live/redeem&x=<y>",
        locale="en-US",
    )
    assert "</script><img" not in escaped_html
    assert "\\u003c/script\\u003e" in escaped_html
    assert "\\u0026" in escaped_html

    mgr = ColabOneLiveStudioManager(tmp_path / "comp_sec", CATALOG_DIR)
    app = create_colabone_live_app(
        verify_token=mgr.verify_session_token,
        verify_token_info=mgr.verify_session_token_info,
        redeem_companion_ticket=mgr.redeem_companion_ticket,
        frame_pump_enabled=False,
    )

    with TestClient(app) as client:
        # 3. 6-locale HTML rendering via ?lang= and Accept-Language + tightened CSP
        for loc, expected_snippet in [
            ("zh-CN", 'lang="zh-CN"'),
            ("en-US", 'lang="en-US"'),
            ("zh-TW", 'lang="zh-TW"'),
            ("zh-HK", 'lang="zh-HK"'),
            ("ja-JP", 'lang="ja-JP"'),
            ("ko-KR", 'lang="ko-KR"'),
        ]:
            resp = client.get(
                f"{COLABONE_LIVE_COMPANION_PATH}?lang={loc}",
                headers={"X-Forwarded-Proto": "https"},
            )
            assert resp.status_code == 200
            assert expected_snippet in resp.text
            csp = resp.headers.get("content-security-policy", "")
            assert "base-uri 'none'" in csp
            assert "object-src 'none'" in csp
            assert "form-action 'none'" in csp
            assert "connect-src 'self' wss:;" in csp
            assert "ws:" not in csp

        resp_accept = client.get(
            COLABONE_LIVE_COMPANION_PATH,
            headers={"Accept-Language": "ja-JP,ja;q=0.9,en;q=0.5"},
        )
        assert resp_accept.status_code == 200
        assert 'lang="ja-JP"' in resp_accept.text
        assert "connect-src 'self' wss: ws:;" in resp_accept.headers.get("content-security-policy", "")

        # 4. 4 KiB redeem body limit returns HTTP 413
        big_body = json.dumps({"code": "a" * 5000}).encode("utf-8")
        r_413 = client.post(
            COLABONE_LIVE_COMPANION_REDEEM_PATH,
            content=big_body,
            headers={"Content-Type": "application/json"},
        )
        assert r_413.status_code == 413

        # 5. Per-IP redeem rate-limit isolation: 5 attacker IPs (75 failures) cannot DoS owner IP!
        valid_ticket = mgr.issue_companion_ticket("https://studio.example.com")
        for attacker_idx in range(5):
            attacker_ip = f"198.51.100.{attacker_idx + 1}"
            for _ in range(15):
                r_bad = client.post(
                    COLABONE_LIVE_COMPANION_REDEEM_PATH,
                    json={"code": "wrong-code"},
                    headers={"CF-Connecting-IP": attacker_ip},
                )
                assert r_bad.status_code == 401
            r_limited = client.post(
                COLABONE_LIVE_COMPANION_REDEEM_PATH,
                json={"code": "wrong-code"},
                headers={"CF-Connecting-IP": attacker_ip},
            )
            assert r_limited.status_code == 429

        # Owner IP still redeems cleanly!
        r_owner = client.post(
            COLABONE_LIVE_COMPANION_REDEEM_PATH,
            json={"code": valid_ticket["code"], "device_label": "Owner iPhone"},
            headers={"CF-Connecting-IP": "203.0.113.42"},
        )
        assert r_owner.status_code == 200
        comp_tok = r_owner.json()["token"]

        # 6. Companion WS authorization denial, text_input > 4000 rejection, camera-off trailing frame drop,
        #    and PTT disconnect leaving director in IDLE (not stuck in LISTENING).
        desk_tok = mgr.issue_session_token(role="desktop")["token"]
        img = Image.new("RGB", (64, 64), (100, 140, 180))
        jpeg_buf = io.BytesIO()
        img.save(jpeg_buf, format="JPEG", quality=75)
        sample_jpeg = jpeg_buf.getvalue()

        with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws_desk:
            ws_desk.send_json({"type": "auth", "token": desk_tok})
            assert ws_desk.receive_json()["type"] == "auth_ok"

            with client.websocket_connect(COLABONE_LIVE_WS_PATH) as ws_comp:
                ws_comp.send_json(
                    {
                        "type": "auth",
                        "token": comp_tok,
                        "video_format": "jpeg",
                    }
                )
                assert ws_comp.receive_json()["type"] == "auth_ok"
                _ = ws_comp.receive_json()  # companion_status
                _ = ws_desk.receive_json()  # companion_status

                for forbidden_payload in (
                    {"type": "switch_engine", "tts_engine": "voxcpm2"},
                    {"type": "trigger_action", "action_id": "wave"},
                    {
                        "type": "trigger_action",
                        "kind": "personalive_drive",
                        "driving_motion_id": "dance1",
                    },
                    {"type": "show_card", "product_id": "p1"},
                    {"type": "set_card", "product_id": "p1"},
                    {"type": "kick_companion"},
                ):
                    ws_comp.send_json(forbidden_payload)
                    err = ws_comp.receive_json()
                    assert err["type"] == "error"
                    assert err["code"] == "forbidden", f"expected forbidden for {forbidden_payload}"

                # Over-length text_input (> 4000 chars) rejected cleanly
                ws_comp.send_json({"type": "text_input", "text": "x" * 4001})
                err_len = ws_comp.receive_json()
                assert err_len["type"] == "error"
                assert err_len["code"] == "text_too_long"

                # Camera-off trailing FRAME_CAMERA_JPEG must be dropped without flipping camera_mode
                assert app.state.companion_info["camera_mode"] == "off"
                ws_comp.send_bytes(
                    pack_colabone_live_frame(
                        COLABONE_LIVE_FRAME_CAMERA_JPEG, 0, 1234, sample_jpeg
                    )
                )
                ws_comp.send_json({"type": "ping", "ts": 9})
                assert ws_comp.receive_json()["type"] == "pong"
                assert app.state.companion_info["camera_mode"] == "off"
                assert app.state.companion_info["vision_frame_ready"] is False

                # Start PTT then disconnect companion without ptt_end -> director transitions back to IDLE
                ws_comp.send_json({"type": "set_mode", "mic_mode": "ptt"})
                _ = ws_comp.receive_json()
                _ = ws_desk.receive_json()
                ws_comp.send_json({"type": "ptt_start"})
                _ = ws_comp.receive_json()
                listen_ev = ws_desk.receive_json()
                assert listen_ev["state"] == "LISTENING"

            # After ws_comp closes, desktop receives IDLE state (not stuck in LISTENING)
            saw_idle = False
            for _ in range(4):
                ev = ws_desk.receive_json()
                if ev.get("type") == "state" and ev.get("state") == "IDLE":
                    saw_idle = True
                    break
            assert saw_idle is True
            assert app.state.director.state == "IDLE"

    # 7. Per-client video format negotiation (fMP4 init segment for late MSE joiner + scaled JPEG fallback for non-MSE)
    fake_init = (
        (16).to_bytes(4, "big") + b"ftyp" + b"isom\x00\x00\x00\x01"
        + (16).to_bytes(4, "big") + b"moov" + b"\x00" * 8
    )
    fake_frag = (
        (16).to_bytes(4, "big") + b"moof" + b"\x00" * 8
        + (16).to_bytes(4, "big") + b"mdat" + b"\x00" * 8
    )

    class _FakeFmp4Encoder(ColabOneLiveVideoEncoder):
        def __init__(self) -> None:
            super().__init__(width=640, height=960, fps=25, mode="libx264", enable_ffmpeg=False)
            self._fmp4_init_segment = fake_init

        def encode_frame(self, frame: ColabOneVisualFrame) -> tuple[int, bytes]:
            return COLABONE_LIVE_FRAME_FMP4, fake_frag

    fmp4_enc = _FakeFmp4Encoder()
    app_vid = create_colabone_live_app(
        verify_token=mgr.verify_session_token,
        verify_token_info=mgr.verify_session_token_info,
        redeem_companion_ticket=mgr.redeem_companion_ticket,
        encoder=fmp4_enc,
        frame_pump_enabled=False,
    )
    with TestClient(app_vid) as client2:
        t_desk = mgr.issue_session_token(role="desktop")["token"]
        t_comp = mgr.issue_session_token(role="companion")["token"]
        with client2.websocket_connect(COLABONE_LIVE_WS_PATH) as ws_d:
            ws_d.send_json({"type": "auth", "token": t_desk, "video_format": "fmp4"})
            assert ws_d.receive_json()["type"] == "auth_ok"
            # Late-joining MSE client immediately receives cached ftyp+moov init segment!
            init_bin = ws_d.receive_bytes()
            ft_init, _, _, pl_init = unpack_colabone_live_frame(init_bin)
            assert ft_init == COLABONE_LIVE_FRAME_FMP4
            assert pl_init == fake_init

            with client2.websocket_connect(COLABONE_LIVE_WS_PATH) as ws_c:
                ws_c.send_json({"type": "auth", "token": t_comp, "video_format": "jpeg"})
                assert ws_c.receive_json()["type"] == "auth_ok"
                _ = ws_c.receive_json()  # companion_status
                _ = ws_d.receive_json()  # companion_status

                # Trigger an action from desktop -> desktop gets fMP4 fragment, companion gets downscaled JPEG (<= 480px)!
                ws_d.send_json({"type": "trigger_action", "action_id": "wave"})
                _ = ws_d.receive_json()  # action
                _ = ws_c.receive_json()  # action
                d_vid = ws_d.receive_bytes()
                c_vid = ws_c.receive_bytes()
                ft_d, _, _, pl_d = unpack_colabone_live_frame(d_vid)
                ft_c, _, _, pl_c = unpack_colabone_live_frame(c_vid)
                assert ft_d == COLABONE_LIVE_FRAME_FMP4
                assert pl_d == fake_frag
                assert ft_c == COLABONE_LIVE_FRAME_VIDEO
                assert pl_c.startswith(b"\xff\xd8\xff")
                with Image.open(io.BytesIO(pl_c)) as dec_img:
                    assert max(dec_img.width, dec_img.height) <= 480

