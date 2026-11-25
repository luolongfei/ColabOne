"""Comprehensive 100% offline pytest suite for the 8 flagship TTS engines,
per-engine disk preflight / segmentation, and voice preview synthesis + caching.

All tests run strictly offline (zero live internet, zero real git/pip/ffmpeg/GPU).
"""

from __future__ import annotations

import ast
import base64
import hashlib
import io
import json
import math
import re
import struct
import sys
import threading
import types
import wave
from collections import namedtuple
from pathlib import Path
from typing import Any
from unittest.mock import Mock

import pytest
from fastapi.testclient import TestClient
from pydantic import ValidationError

from common.comfyui import (
    ComfyCatalog,
    ComfyTtsEngineSpec,
    ComfyTtsPreviewRequest,
    ComfyTtsVoicePreset,
)
from conftest import wait_task
from server.app import create_colabone_app
from server.runtime import ColabOneRuntime, ColabOneTask
from server.tts_engines import ColabOneRemoteTtsRunner

ROOT = Path(__file__).resolve().parents[1]
_HEX_32_RE = re.compile(r"^[a-f0-9]{32}$")
_HEX_40_RE = re.compile(r"^[a-fA-F0-9]{40}$")
_FORBIDDEN_RIGHT_ARROW_RE = re.compile(
    "["
    "\u2192\u219d\u21a0\u21a3\u21a6\u21aa\u21ac\u21b3"
    "\u21c0\u21c1\u21c9\u21d2\u21db\u21dd\u21e2\u21e5\u21e8"
    "\u21f6\u21f8\u21fb\u21fe"
    "\u2794\u2799-\u279e\u27a0-\u27a4\u27f6\u27f9\u27fc\u27fe\u27ff\u2b95"
    "]"
    "|&ra" "rr;|&#85" "94;|&#x21" "92;"
    "|\\bPhArrow" "Right\\b",
    re.IGNORECASE,
)
_DiskUsage = namedtuple("DiskUsage", ["total", "used", "free"])

EXPECTED_FLAGSHIP_ENGINE_IDS = (
    "fish_drama3",
    "moss_tts_v1_5",
    "breeze_tts2",
    "ming_3_tts",
    "voxcpm2",
    "qwen3_tts",
    "indextts2",
    "fish_s2_pro",
)
NON_COMMERCIAL_ENGINE_IDS = (
    "fish_drama3",
    "breeze_tts2",
    "indextts2",
    "fish_s2_pro",
)
COMMERCIAL_ENGINE_IDS = (
    "moss_tts_v1_5",
    "ming_3_tts",
    "voxcpm2",
    "qwen3_tts",
)
QWEN3_OFFICIAL_SPEAKERS = (
    "Vivian",
    "Serena",
    "Uncle_Fu",
    "Dylan",
    "Eric",
    "Ryan",
    "Aiden",
    "Ono_Anna",
    "Sohee",
)


# ---------------------------------------------------------------------------
# Pure-stdlib offline test helpers (zero numpy/scipy/soundfile dependency)
# ---------------------------------------------------------------------------


def make_sine_samples(
    duration_sec: float,
    freq_hz: float = 440.0,
    amp: float = 0.45,
    sample_rate: int = 24000,
) -> list[float]:
    count = int(round(duration_sec * sample_rate))
    omega = 2.0 * math.pi * freq_hz / sample_rate
    return [amp * math.sin(omega * i) for i in range(count)]


def write_pcm16_wav(
    path: Path, samples: list[float], sample_rate: int = 24000
) -> Path:
    path.parent.mkdir(parents=True, exist_ok=True)
    pcm = bytearray()
    for val in samples:
        clamped = max(-1.0, min(1.0, float(val)))
        pcm.extend(struct.pack("<h", int(round(clamped * 32767.0))))
    with wave.open(str(path), "wb") as wf:
        wf.setnchannels(1)
        wf.setsampwidth(2)
        wf.setframerate(sample_rate)
        wf.writeframes(bytes(pcm))
    return path


def _make_mock_runtime(tmp_path: Path, total_mib: int = 81920):
    rt = Mock()
    rt.root = tmp_path / "remote"
    rt.root.mkdir(parents=True, exist_ok=True)
    rt.models_dir = rt.root / "models"
    rt.models_dir.mkdir(parents=True, exist_ok=True)
    rt.stopping = threading.Event()
    rt.gpu_info = Mock(
        return_value={
            "name": "NVIDIA A100-SXM4-80GB",
            "total_mib": total_mib,
            "used_mib": 1024,
        }
    )
    return rt


def _make_offline_synth_fn(
    call_log: list[dict] | None = None,
    *,
    duration_sec: float = 0.5,
):
    """Offline mock synth_fn that writes real stdlib mono PCM16 WAV files."""

    def _synth(engine, segments, segments_dir, **kwargs):
        if call_log is not None:
            call_log.append(
                {
                    "engine_id": engine.get("id", "") if isinstance(engine, dict) else "",
                    "segments": list(segments),
                    "kwargs": dict(kwargs),
                }
            )
        sr = int(engine.get("sample_rate") or 24000) if isinstance(engine, dict) else 24000
        out_dir = Path(segments_dir)
        out_dir.mkdir(parents=True, exist_ok=True)
        results: list[tuple[str, Path]] = []
        for idx, text in enumerate(segments):
            wav_path = out_dir / f"offline_seg_{idx:03d}.wav"
            samples = make_sine_samples(
                duration_sec,
                freq_hz=360.0 + idx * 45.0,
                amp=0.35,
                sample_rate=sr,
            )
            write_pcm16_wav(wav_path, samples, sample_rate=sr)
            results.append((str(text), wav_path))
        return results

    return _synth


def sample_eight_tts_engines_by_id() -> dict[str, dict]:
    """Load the real 8-engine catalog metadata for offline runner tests."""
    raw_text = (ROOT / "catalog" / "comfyui.json").read_text(encoding="utf-8")
    catalog = ComfyCatalog.model_validate_json(raw_text)
    return {e.id: e.model_dump() for e in catalog.tts_engines}


# ---------------------------------------------------------------------------
# 1. Catalog & Schema Validation Tests
# ---------------------------------------------------------------------------


def test_real_catalog_eight_flagship_engines_and_presets():
    raw_text = (ROOT / "catalog" / "comfyui.json").read_text(encoding="utf-8")
    catalog = ComfyCatalog.model_validate_json(raw_text)

    assert catalog.port == 8188
    assert catalog.proxy_port == 8189
    assert _HEX_40_RE.fullmatch(catalog.commit)

    engines_by_id = {e.id: e for e in catalog.tts_engines}
    assert len(catalog.tts_engines) == len(EXPECTED_FLAGSHIP_ENGINE_IDS)
    assert set(engines_by_id.keys()) == set(EXPECTED_FLAGSHIP_ENGINE_IDS)

    for eid in EXPECTED_FLAGSHIP_ENGINE_IDS:
        eng = engines_by_id[eid]
        assert _HEX_40_RE.fullmatch(eng.revision), f"{eid}: unpinned revision"
        if eng.aux_repo:
            assert _HEX_40_RE.fullmatch(eng.aux_revision), f"{eid}: unpinned aux_revision"
        if eng.custom_voice_repo:
            assert _HEX_40_RE.fullmatch(
                eng.custom_voice_revision
            ), f"{eid}: unpinned custom_voice_revision"
        if eng.source_commit:
            assert _HEX_40_RE.fullmatch(eng.source_commit), f"{eid}: unpinned source_commit"

        assert eng.min_vram_mib > 0, f"{eid}: min_vram_mib must be positive"
        assert eng.min_disk_mib > 0, f"{eid}: min_disk_mib must be positive"
        assert 40 <= eng.max_segment_chars <= 1000, f"{eid}: invalid max_segment_chars"
        assert 8000 <= eng.sample_rate <= 96000, f"{eid}: invalid sample_rate"

        # Every engine must have >= 10 universal presets (5 female + 5 male),
        # each with non-empty preview_text and unique IDs.
        preset_ids = [p.id for p in eng.voice_presets]
        assert len(preset_ids) == len(set(preset_ids)), f"{eid}: duplicate preset IDs"
        for preset in eng.voice_presets:
            assert preset.preview_text.strip() != "", (
                f"{eid}/{preset.id}: preview_text must not be empty"
            )

        universal = [p for p in eng.voice_presets if not p.speaker_id]
        female_universal = [p for p in universal if p.gender == "female"]
        male_universal = [p for p in universal if p.gender == "male"]
        assert len(universal) >= 10, f"{eid}: expected >= 10 universal presets"
        assert len(female_universal) >= 5, f"{eid}: expected >= 5 female presets"
        assert len(male_universal) >= 5, f"{eid}: expected >= 5 male presets"

    # Truthful commercial / non-commercial flags and notices.
    for nc_id in NON_COMMERCIAL_ENGINE_IDS:
        eng = engines_by_id[nc_id]
        assert eng.commercial_ok is False, f"{nc_id} must be marked commercial_ok=False"
        assert eng.license_notice.strip() != "", f"{nc_id} must carry a license_notice"

    for com_id in COMMERCIAL_ENGINE_IDS:
        eng = engines_by_id[com_id]
        assert eng.commercial_ok is True, f"{com_id} must be marked commercial_ok=True"

    # Qwen3-TTS official CustomVoice metadata and 9 official speakers.
    qwen3 = engines_by_id["qwen3_tts"]
    assert qwen3.aux_repo != ""
    assert _HEX_40_RE.fullmatch(qwen3.aux_revision)
    assert qwen3.custom_voice_repo != ""
    assert _HEX_40_RE.fullmatch(qwen3.custom_voice_revision)
    assert qwen3.supports_custom_speakers is True

    official_presets = [p for p in qwen3.voice_presets if p.speaker_id]
    official_speakers = [p.speaker_id for p in official_presets]
    assert len(official_presets) == len(QWEN3_OFFICIAL_SPEAKERS)
    assert set(official_speakers) == set(QWEN3_OFFICIAL_SPEAKERS)
    for preset in official_presets:
        assert preset.preview_text.strip() != ""


def test_tts_voice_preset_engine_spec_and_preview_request_negative_validation():
    # 1. ComfyTtsVoicePreset positive + negative validation
    default_preset = ComfyTtsVoicePreset(id="voice_ok", name="Voice OK")
    assert default_preset.preview_text == ""
    assert default_preset.speaker_id == ""

    boundary_preset = ComfyTtsVoicePreset(
        id="qwen_ono_anna",
        name="Ono Anna",
        gender="female",
        preview_text="试" * 400,
        speaker_id="Ono_Anna",
        seed=202609,
    )
    assert len(boundary_preset.preview_text) == 400
    assert boundary_preset.speaker_id == "Ono_Anna"

    for bad_preset in (
        {"id": "ok_id", "name": "Too Long Preview", "preview_text": "试" * 401},
        {"id": "ok_id", "name": "Too Long ASCII Preview", "preview_text": "a" * 401},
        {"id": "ok_id", "name": "Bad Speaker Traversal", "speaker_id": "../evil"},
        {"id": "ok_id", "name": "Bad Speaker Spaces", "speaker_id": "Vivian Chen"},
        {"id": "ok_id", "name": "Bad Speaker Slash", "speaker_id": "Vivian/01"},
        {"id": "ok_id", "name": "Bad Speaker Punct", "speaker_id": "Vivian!"},
        {"id": "ok_id", "name": "Overlong Speaker", "speaker_id": "s" * 65},
        {"id": "ok_id", "name": "Extra Forbidden", "unknown_extra": True},
    ):
        with pytest.raises(ValidationError):
            ComfyTtsVoicePreset.model_validate(bad_preset)

    # 2. ComfyTtsEngineSpec positive + negative validation
    base_engine = {
        "id": "demo_tts",
        "name": "Demo TTS",
        "repo": "org/demo-tts",
        "revision": "a" * 40,
    }
    parsed_default = ComfyTtsEngineSpec.model_validate(base_engine)
    assert parsed_default.min_disk_mib == 0
    assert parsed_default.max_segment_chars == 120
    assert parsed_default.custom_voice_repo == ""
    assert parsed_default.custom_voice_revision == ""
    assert parsed_default.supports_custom_speakers is False

    parsed_extended = ComfyTtsEngineSpec.model_validate(
        base_engine
        | {
            "custom_voice_repo": "Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice",
            "custom_voice_revision": "b" * 40,
            "supports_custom_speakers": True,
            "min_disk_mib": 12288,
            "max_segment_chars": 320,
        }
    )
    assert parsed_extended.min_disk_mib == 12288
    assert parsed_extended.max_segment_chars == 320
    assert parsed_extended.supports_custom_speakers is True

    # Boundary max_segment_chars values (40 and 1000) accepted.
    assert ComfyTtsEngineSpec.model_validate(base_engine | {"max_segment_chars": 40}).max_segment_chars == 40
    assert ComfyTtsEngineSpec.model_validate(base_engine | {"max_segment_chars": 1000}).max_segment_chars == 1000

    for bad_engine_update in (
        {"custom_voice_repo": "Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice"},
        {
            "custom_voice_repo": "Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice",
            "custom_voice_revision": "",
        },
        {
            "custom_voice_repo": "Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice",
            "custom_voice_revision": "short-sha",
        },
        {
            "custom_voice_repo": "../evil",
            "custom_voice_revision": "b" * 40,
        },
        {"custom_voice_revision": "z" * 40},
        {"min_disk_mib": -1},
        {"max_segment_chars": 39},
        {"max_segment_chars": 0},
        {"max_segment_chars": -10},
        {"max_segment_chars": 1001},
        {"extra_unknown": 123},
    ):
        with pytest.raises(ValidationError):
            ComfyTtsEngineSpec.model_validate(base_engine | bad_engine_update)

    # 3. ComfyTtsPreviewRequest positive + negative validation
    min_req = ComfyTtsPreviewRequest(engine_id="voxcpm2")
    assert min_req.engine_id == "voxcpm2"
    assert min_req.voice_preset == ""
    assert min_req.preview_text == ""
    assert min_req.ref_audio_input_id == ""
    assert min_req.hf_token == ""

    secret = "hf_preview_secret_token_987654321"
    full_req = ComfyTtsPreviewRequest(
        engine_id="qwen3_tts",
        voice_preset="qwen_vivian",
        preview_text="你好，这是一段离线试听文案。",
        voice_prompt="warm articulate studio anchor",
        emotion_prompt="gentle smile",
        ref_audio_input_id="a" * 32,
        ref_text="参考音频文本",
        emotion_ref_input_id="b" * 32,
        hf_token=f"  {secret}  ",
    )
    assert full_req.hf_token == secret
    assert secret not in repr(full_req)

    for bad_preview in (
        {"engine_id": ""},
        {"engine_id": "../evil"},
        {"engine_id": "bad engine"},
        {"engine_id": "e" * 81},
        {"engine_id": "voxcpm2", "voice_preset": "../evil"},
        {"engine_id": "voxcpm2", "voice_preset": "v" * 65},
        {"engine_id": "voxcpm2", "ref_audio_input_id": "../secret"},
        {"engine_id": "voxcpm2", "ref_audio_input_id": "not-32-hex"},
        {"engine_id": "voxcpm2", "ref_audio_input_id": "A" * 32},
        {"engine_id": "voxcpm2", "ref_audio_input_id": "g" * 32},
        {"engine_id": "voxcpm2", "ref_audio_input_id": "a" * 31},
        {"engine_id": "voxcpm2", "ref_audio_input_id": "a" * 33},
        {"engine_id": "voxcpm2", "emotion_ref_input_id": "bad-asset-id"},
        {"engine_id": "voxcpm2", "hf_token": "hf_abc\rdef"},
        {"engine_id": "voxcpm2", "hf_token": "hf_abc\ndef"},
        {"engine_id": "voxcpm2", "hf_token": "hf_abc\tdef"},
        {"engine_id": "voxcpm2", "hf_token": "hf_abc\x00def"},
        {"engine_id": "voxcpm2", "hf_token": "hf_abc\x7fdef"},
        {"engine_id": "voxcpm2", "hf_token": "hf_abc def"},
        {"engine_id": "voxcpm2", "hf_token": "hf_abc\u3000def"},
        {"engine_id": "voxcpm2", "hf_token": "x" * 513},
        {"engine_id": "voxcpm2", "preview_text": "文" * 601},
        {"engine_id": "voxcpm2", "voice_prompt": "描" * 601},
        {"engine_id": "voxcpm2", "emotion_prompt": "情" * 241},
        {"engine_id": "voxcpm2", "ref_text": "参" * 601},
        {"engine_id": "voxcpm2", "unexpected_field": True},
    ):
        with pytest.raises(ValidationError):
            ComfyTtsPreviewRequest.model_validate(bad_preview)


# ---------------------------------------------------------------------------
# 2. Worker Script AST & Contract Tests for All 8 Engines
# ---------------------------------------------------------------------------


def test_worker_scripts_ast_and_contract_for_all_eight_engines(tmp_path):
    from server.tts_engines import ColabOneRemoteTtsRunner

    rt = _make_mock_runtime(tmp_path, total_mib=81920)
    runner = ColabOneRemoteTtsRunner(rt, sample_eight_tts_engines_by_id())

    scripts: dict[str, str] = {}
    for engine_id in EXPECTED_FLAGSHIP_ENGINE_IDS:
        script = runner.build_worker_script(engine_id)
        assert isinstance(script, str) and len(script) > 100
        # Every generated worker script MUST parse cleanly as Python AST.
        tree = ast.parse(script)
        assert any(
            isinstance(node, ast.FunctionDef) and node.name == "main"
            for node in tree.body
        ), f"{engine_id} worker must define top-level main()"
        # Zero right-arrow glyphs anywhere in generated worker code.
        assert not _FORBIDDEN_RIGHT_ARROW_RE.search(script), (
            f"{engine_id} worker script contains forbidden right-arrow glyph"
        )
        assert "_write_pcm16_wav" in script
        scripts[engine_id] = script

    # Reject unsupported engine IDs cleanly.
    with pytest.raises(ValueError):
        runner.build_worker_script("nonexistent_engine_xyz")

    # fish_drama3: must explicitly set compile=False and model_family="drama3".
    drama_script = scripts["fish_drama3"]
    assert re.search(
        r"""(?:compile\s*=\s*False|["']compile["']\s*:\s*False)""", drama_script
    ), "fish_drama3 worker must explicitly set compile=False"
    assert re.search(
        r"""(?:model_family\s*=\s*["']drama3["']|["']model_family["']\s*:\s*["']drama3["'])""",
        drama_script,
    ), 'fish_drama3 worker must explicitly set model_family="drama3"'
    assert "compile=True" not in drama_script

    # qwen3_tts: must handle generate_custom_voice, generate_voice_design, and generate_voice_clone.
    qwen_script = scripts["qwen3_tts"]
    assert "generate_custom_voice" in qwen_script
    assert "generate_voice_design" in qwen_script
    assert "generate_voice_clone" in qwen_script
    assert "custom_voice_repo" in qwen_script

    # indextts2: must try indextts.infer_v2_5 BEFORE indextts.infer_v2, and support lang + use_bf16.
    index_script = scripts["indextts2"]
    m_v25 = re.search(r"\bindextts\.infer_v2_5\b", index_script)
    m_v2 = re.search(r"\bindextts\.infer_v2\b", index_script)
    assert m_v25 is not None, "indextts2 worker must import indextts.infer_v2_5"
    assert m_v2 is not None, "indextts2 worker must keep indextts.infer_v2 fallback"
    assert m_v25.start() < m_v2.start(), (
        "indextts2 worker must import indextts.infer_v2_5 before indextts.infer_v2"
    )
    assert "use_bf16" in index_script
    assert re.search(r"""(?:["']lang["']|\blang\s*=)""", index_script), (
        "indextts2 worker must support lang parameter"
    )

    # moss_tts_v1_5: expected HuggingFace/MOSS-TTS API symbols.
    moss_script = scripts["moss_tts_v1_5"]
    assert "moss_tts_v1_5" in moss_script
    assert "snapshot_download" in moss_script
    assert "AutoModel" in moss_script
    assert "AutoProcessor" in moss_script
    assert "trust_remote_code" in moss_script

    # breeze_tts2: expected real Breeze runtime & streaming symbols.
    breeze_script = scripts["breeze_tts2"]
    assert "breeze_tts2" in breeze_script
    assert "snapshot_download" in breeze_script
    assert "torch" in breeze_script
    for sym in (
        "breeze_infer.runtime",
        "load_runtime",
        "breeze_infer.templates",
        "FastBreezeStreamingRuntime",
        "FastStreamingConfig",
    ):
        assert sym in breeze_script, f"breeze_tts2 worker missing expected symbol: {sym}"

    # ming_3_tts: expected Ming-3.0-tts-tiny native API symbols.
    ming_script = scripts["ming_3_tts"]
    assert "ming_3_tts" in ming_script
    assert "snapshot_download" in ming_script
    assert "torch" in ming_script
    assert "MingTinyTTS" in ming_script
    assert "ming_tiny" in ming_script


# ---------------------------------------------------------------------------
# 3. Disk Preflight & Per-Engine Segmentation Tests
# ---------------------------------------------------------------------------


def test_tts_runner_check_disk_preflight(tmp_path, monkeypatch):
    from server.tts_engines import ColabOneRemoteTtsRunner

    rt = _make_mock_runtime(tmp_path, total_mib=81920)
    engines = sample_eight_tts_engines_by_id()
    runner = ColabOneRemoteTtsRunner(rt, engines)

    drama_engine = runner.get_engine("fish_drama3")
    assert drama_engine["min_disk_mib"] >= 20480

    # Insufficient free disk space must raise RuntimeError.
    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(100 * 1024**3, 98 * 1024**3, 2 * 1024**3),
    )
    with pytest.raises(RuntimeError):
        runner.check_disk(drama_engine)

    # Adequate free disk space passes cleanly.
    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(200 * 1024**3, 50 * 1024**3, 150 * 1024**3),
    )
    runner.check_disk(drama_engine)

    # Engine with min_disk_mib=0 passes even when free space is small.
    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(10 * 1024**3, 9 * 1024**3, 512 * 1024**2),
    )
    runner.check_disk({"id": "zero_disk", "name": "Zero Disk", "min_disk_mib": 0})


def test_tts_runner_synthesize_respects_engine_max_segment_chars(tmp_path, monkeypatch):
    from server.tts_engines import ColabOneRemoteTtsRunner

    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(200 * 1024**3, 20 * 1024**3, 180 * 1024**3),
    )

    rt = _make_mock_runtime(tmp_path, total_mib=81920)
    engines = sample_eight_tts_engines_by_id()
    # Explicitly configure 120 default vs 320 for Drama 3 so the segmentation
    # contrast is directly verified against a ~220-character single sentence.
    engines["voxcpm2"]["max_segment_chars"] = 120
    engines["fish_drama3"]["max_segment_chars"] = 320

    call_log: list[dict] = []
    runner = ColabOneRemoteTtsRunner(
        rt,
        engines,
        synth_fn=_make_offline_synth_fn(call_log, duration_sec=0.4),
    )

    # Single continuous sentence (~220 CJK chars) with internal commas and one terminal period:
    # - Under max_segment_chars=120, it MUST be split on commas into >= 2 segments <= 120 chars.
    # - Under max_segment_chars=320, it stays intact as 1 segment.
    long_sentence = (
        "欢迎收看今天的深度技术解析节目，我们将在这一段连续完整的口播示范里详细介绍新一代语音大模型"
        "在长文本韵律建模方面的关键进展，包括上下文语境记忆连贯性、自然停顿与呼吸节奏控制、"
        "情绪起伏细腻过渡以及中英文专业术语混读时的顺滑表现，帮助内容创作者在单次合成中直接获得"
        "更加贴近真实演播室录音棚水准的自然听感体验。"
    )
    assert 150 < len(long_sentence) < 300

    # 1. Default 120-char engine (voxcpm2) splits into >= 2 segments each <= 120 chars.
    res_vox = runner.synthesize(
        ColabOneTask("comfyui_run", "seg-vox"),
        engine_id="voxcpm2",
        script_text=long_sentence,
        work_dir=tmp_path / "work_vox",
        voice_preset="shen_zhiyi",
    )
    assert Path(res_vox["audio_path"]).is_file()
    vox_calls = [c for c in call_log if c["engine_id"] == "voxcpm2"]
    assert len(vox_calls) >= 1
    vox_segs = vox_calls[-1]["segments"]
    assert len(vox_segs) >= 2
    assert all(len(seg) <= 120 for seg in vox_segs)

    # 2. Drama 3 (320-char budget) keeps the ~220-char sentence in a single segment.
    ref_wav = write_pcm16_wav(
        tmp_path / "drama_ref.wav",
        make_sine_samples(0.5, freq_hz=320.0, amp=0.35, sample_rate=24000),
        sample_rate=24000,
    )
    res_drama = runner.synthesize(
        ColabOneTask("comfyui_run", "seg-drama"),
        engine_id="fish_drama3",
        script_text=long_sentence,
        work_dir=tmp_path / "work_drama",
        ref_audio_path=ref_wav,
        ref_text="参考样本",
        voice_preset="custom",
    )
    assert Path(res_drama["audio_path"]).is_file()
    drama_calls = [c for c in call_log if c["engine_id"] == "fish_drama3"]
    assert len(drama_calls) == 1
    drama_segs = drama_calls[0]["segments"]
    assert len(drama_segs) == 1
    assert 120 < len(drama_segs[0]) <= 320


# ---------------------------------------------------------------------------
# 4. Voice Preview Synthesis, Caching & Symlink Protection Tests
# ---------------------------------------------------------------------------


def test_compute_preview_id_determinism_and_preset_fallback(tmp_path):
    from server.tts_engines import ColabOneRemoteTtsRunner

    rt = _make_mock_runtime(tmp_path, total_mib=81920)
    engines = sample_eight_tts_engines_by_id()
    runner = ColabOneRemoteTtsRunner(rt, engines)

    vox_presets = {p["id"]: p for p in engines["voxcpm2"]["voice_presets"]}
    expected_preview_text = vox_presets["shen_zhiyi"]["preview_text"].strip()
    assert expected_preview_text != ""

    # Empty preview_text falls back to the preset's preview_text and stays deterministic.
    pid_a, resolved_a = runner.compute_preview_id(
        "voxcpm2", voice_preset="shen_zhiyi", preview_text=""
    )
    pid_b, resolved_b = runner.compute_preview_id(
        "voxcpm2", voice_preset="shen_zhiyi", preview_text="   "
    )
    pid_explicit, resolved_explicit = runner.compute_preview_id(
        "voxcpm2", voice_preset="shen_zhiyi", preview_text=expected_preview_text
    )

    assert _HEX_32_RE.fullmatch(pid_a)
    assert resolved_a == expected_preview_text
    assert (pid_a, resolved_a) == (pid_b, resolved_b)
    assert (pid_a, resolved_a) == (pid_explicit, resolved_explicit)

    # Distinct engine, voice_preset, or text must produce distinct preview IDs.
    pid_other_engine, _ = runner.compute_preview_id(
        "qwen3_tts", voice_preset="shen_zhiyi", preview_text=expected_preview_text
    )
    pid_other_preset, _ = runner.compute_preview_id(
        "voxcpm2", voice_preset="lin_xiaoman", preview_text=expected_preview_text
    )
    pid_other_text, _ = runner.compute_preview_id(
        "voxcpm2", voice_preset="shen_zhiyi", preview_text="另一段自定义试听台词。"
    )
    assert len({pid_a, pid_other_engine, pid_other_preset, pid_other_text}) == 4


def test_synthesize_preview_caches_under_voice_previews_and_reuses_instantly(
    tmp_path, monkeypatch
):
    from server.tts_engines import ColabOneRemoteTtsRunner

    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(200 * 1024**3, 20 * 1024**3, 180 * 1024**3),
    )

    rt = _make_mock_runtime(tmp_path, total_mib=81920)
    call_log: list[dict] = []
    runner = ColabOneRemoteTtsRunner(
        rt,
        sample_eight_tts_engines_by_id(),
        synth_fn=_make_offline_synth_fn(call_log, duration_sec=0.45),
    )

    preview_id, resolved_text = runner.compute_preview_id(
        "voxcpm2", voice_preset="shen_zhiyi", preview_text=""
    )

    # First call synthesizes and persists _voice_previews/{preview_id}.wav + .json.
    first = runner.synthesize_preview(
        ColabOneTask("comfyui_tts_preview", "voxcpm2"),
        engine_id="voxcpm2",
        voice_preset="shen_zhiyi",
        preview_text=resolved_text,
        preview_id=preview_id,
    )
    assert len(call_log) == 1
    assert first["preview_id"] == preview_id

    previews_dir = runner.engines_dir / "_voice_previews"
    cached_wav = previews_dir / f"{preview_id}.wav"
    cached_json = previews_dir / f"{preview_id}.json"
    assert cached_wav.is_file() and not cached_wav.is_symlink()
    assert cached_json.is_file() and not cached_json.is_symlink()

    wav_bytes = cached_wav.read_bytes()
    assert wav_bytes.startswith(b"RIFF")
    expected_sha = hashlib.sha256(wav_bytes).hexdigest()
    meta = json.loads(cached_json.read_text(encoding="utf-8"))
    assert meta.get("sha256") == expected_sha

    with wave.open(io.BytesIO(wav_bytes), "rb") as wf:
        assert wf.getnchannels() == 1
        assert wf.getsampwidth() == 2
        assert wf.getnframes() > 0

    # Second call MUST hit cache immediately without re-invoking synth_fn.
    second = runner.synthesize_preview(
        ColabOneTask("comfyui_tts_preview", "voxcpm2"),
        engine_id="voxcpm2",
        voice_preset="shen_zhiyi",
        preview_text=resolved_text,
        preview_id=preview_id,
    )
    assert len(call_log) == 1
    assert second["preview_id"] == preview_id
    assert second.get("cached") is True
    assert second.get("sha256") == expected_sha
    assert base64.b64decode(second["data"]) == wav_bytes

    # get_cached_preview returns the same valid WAV payload.
    fetched = runner.get_cached_preview(preview_id)
    assert fetched is not None
    assert fetched["cached"] is True
    assert fetched["preview_id"] == preview_id
    assert fetched["mime"] == "audio/wav"
    assert fetched["sha256"] == expected_sha
    assert fetched["size"] == len(wav_bytes)
    assert base64.b64decode(fetched["data"]) == wav_bytes


def test_get_cached_preview_rejects_invalid_ids_and_symlinks(tmp_path):
    from server.tts_engines import ColabOneRemoteTtsRunner

    rt = _make_mock_runtime(tmp_path, total_mib=81920)
    runner = ColabOneRemoteTtsRunner(rt, sample_eight_tts_engines_by_id())

    # Invalid preview_id strings must raise ValueError.
    for bad_id in (
        "",
        "../evil",
        "sub/dir",
        "not-a-valid-hex-id",
        "A" * 32,
        "g" * 32,
        "a" * 31,
        "a" * 33,
    ):
        with pytest.raises(ValueError):
            runner.get_cached_preview(bad_id)

    # Valid format but uncached preview returns None.
    assert runner.get_cached_preview("0" * 32) is None

    # Create real target files outside _voice_previews for symlink tests.
    outside_wav = tmp_path / "outside.wav"
    write_pcm16_wav(outside_wav, make_sine_samples(0.2, sample_rate=24000), 24000)
    outside_json = tmp_path / "outside.json"
    outside_json.write_text(
        json.dumps(
            {
                "preview_id": "1" * 32,
                "engine_id": "voxcpm2",
                "voice_preset": "shen_zhiyi",
                "text": "hi",
                "mime": "audio/wav",
                "size": outside_wav.stat().st_size,
                "sha256": hashlib.sha256(outside_wav.read_bytes()).hexdigest(),
            }
        ),
        encoding="utf-8",
    )

    previews_dir = runner.engines_dir / "_voice_previews"
    previews_dir.mkdir(parents=True, exist_ok=True)

    # 1. Symlinked WAV file rejected.
    sym_wav_id = "1" * 32
    (previews_dir / f"{sym_wav_id}.wav").symlink_to(outside_wav)
    (previews_dir / f"{sym_wav_id}.json").write_text(
        outside_json.read_text(encoding="utf-8"), encoding="utf-8"
    )
    with pytest.raises(ValueError):
        runner.get_cached_preview(sym_wav_id)

    # 2. Symlinked metadata JSON rejected.
    sym_meta_id = "2" * 32
    write_pcm16_wav(
        previews_dir / f"{sym_meta_id}.wav",
        make_sine_samples(0.2, sample_rate=24000),
        24000,
    )
    (previews_dir / f"{sym_meta_id}.json").symlink_to(outside_json)
    with pytest.raises(ValueError):
        runner.get_cached_preview(sym_meta_id)

    # 3. Symlinked _voice_previews directory itself rejected.
    real_other_dir = tmp_path / "other_previews"
    real_other_dir.mkdir()
    runner2 = ColabOneRemoteTtsRunner(
        _make_mock_runtime(tmp_path / "rt2", total_mib=81920),
        sample_eight_tts_engines_by_id(),
    )
    runner2.engines_dir.mkdir(parents=True, exist_ok=True)
    (runner2.engines_dir / "_voice_previews").symlink_to(real_other_dir)
    with pytest.raises(ValueError):
        runner2.get_cached_preview("3" * 32)


# ---------------------------------------------------------------------------
# 5. FastAPI Voice Preview Endpoints & HF Token Scrubbing Tests
# ---------------------------------------------------------------------------


def test_fastapi_tts_preview_endpoints_auth_cache_and_hf_token_scrubbing(
    tmp_path, monkeypatch
):
    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(200 * 1024**3, 20 * 1024**3, 180 * 1024**3),
    )

    rt = ColabOneRuntime(tmp_path / "remote", ROOT / "catalog")
    monkeypatch.setattr(
        rt,
        "gpu_info",
        lambda: {"name": "NVIDIA A100-SXM4-80GB", "total_mib": 81920, "used_mib": 512},
    )

    synth_calls: list[dict] = []
    rt.comfy.tts.synth_fn = _make_offline_synth_fn(synth_calls, duration_sec=0.4)

    token = "tts-preview-api-control-token-32ch"
    auth = {"Authorization": "Bearer " + token}
    app = create_colabone_app(rt, token, background=False)

    with TestClient(app) as client:
        # 1. Auth enforcement: 401 without valid Bearer token.
        assert (
            client.post(
                "/api/v1/comfyui/tts-preview",
                json={"engine_id": "voxcpm2", "voice_preset": "shen_zhiyi"},
            ).status_code
            == 401
        )
        assert (
            client.post(
                "/api/v1/comfyui/tts-preview",
                json={"engine_id": "voxcpm2", "voice_preset": "shen_zhiyi"},
                headers={"Authorization": "Bearer wrong-token"},
            ).status_code
            == 401
        )
        assert client.get(f"/api/v1/comfyui/tts-previews/{'a' * 32}").status_code == 401

        # 2. Invalid payloads return 422.
        for bad_body in (
            {"engine_id": "../evil"},
            {"engine_id": "voxcpm2", "ref_audio_input_id": "not-32-hex"},
            {"engine_id": "voxcpm2", "hf_token": "hf_bad\ntoken"},
            {"engine_id": "voxcpm2", "unexpected_extra": True},
        ):
            bad_resp = client.post(
                "/api/v1/comfyui/tts-preview",
                json=bad_body,
                headers=auth,
            )
            assert bad_resp.status_code == 422

        # Unknown valid preview ID returns 404; malformed preview ID returns 400.
        assert (
            client.get(
                f"/api/v1/comfyui/tts-previews/{'0' * 32}", headers=auth
            ).status_code
            == 404
        )
        assert (
            client.get(
                "/api/v1/comfyui/tts-previews/invalid-preview-id", headers=auth
            ).status_code
            == 400
        )

        # 3. Cache miss POST spawns async task (cached: False).
        miss_resp = client.post(
            "/api/v1/comfyui/tts-preview",
            json={"engine_id": "voxcpm2", "voice_preset": "shen_zhiyi"},
            headers=auth,
        )
        assert miss_resp.status_code == 200
        miss_body = miss_resp.json()
        assert miss_body["cached"] is False
        preview_id = miss_body["preview_id"]
        assert _HEX_32_RE.fullmatch(preview_id)
        assert isinstance(miss_body.get("task"), dict)
        assert miss_body["task"]["kind"] == "comfyui_tts_preview"

        done_task = wait_task(rt, miss_body["task"])
        assert done_task.state == "completed"
        assert len(synth_calls) == 1

        # 4. GET /api/v1/comfyui/tts-previews/{preview_id} returns valid WAV base64 + sha256.
        get_resp = client.get(
            f"/api/v1/comfyui/tts-previews/{preview_id}",
            headers=auth,
        )
        assert get_resp.status_code == 200
        get_body = get_resp.json()
        assert get_body["preview_id"] == preview_id
        assert get_body["cached"] is True
        assert get_body["mime"] == "audio/wav"
        wav_bytes = base64.b64decode(get_body["data"])
        assert wav_bytes.startswith(b"RIFF")
        assert hashlib.sha256(wav_bytes).hexdigest() == get_body["sha256"]
        assert get_body["size"] == len(wav_bytes)
        with wave.open(io.BytesIO(wav_bytes), "rb") as wf:
            assert wf.getnchannels() == 1
            assert wf.getsampwidth() == 2
            assert wf.getnframes() > 0

        # 5. Subsequent POST with identical inputs returns instant cached: True without re-synthesizing.
        hit_resp = client.post(
            "/api/v1/comfyui/tts-preview",
            json={"engine_id": "voxcpm2", "voice_preset": "shen_zhiyi"},
            headers=auth,
        )
        assert hit_resp.status_code == 200
        hit_body = hit_resp.json()
        assert hit_body["cached"] is True
        assert hit_body["preview_id"] == preview_id
        assert hit_body["sha256"] == get_body["sha256"]
        assert base64.b64decode(hit_body["data"]) == wav_bytes
        assert len(synth_calls) == 1

        # 6. Secret hf_token must be scrubbed if synthesis raises an exception containing it.
        leaked_secret = "hf_super_secret_preview_token_leak_check_999"

        def _failing_synth(*args, **kwargs):
            raise RuntimeError(f"upstream rejected credential {leaked_secret} during download")

        rt.comfy.tts.synth_fn = _failing_synth
        fail_resp = client.post(
            "/api/v1/comfyui/tts-preview",
            json={
                "engine_id": "voxcpm2",
                "voice_preset": "lin_xiaoman",
                "preview_text": "触发异常脱敏校验的独立试听文本。",
                "hf_token": leaked_secret,
            },
            headers=auth,
        )
        assert fail_resp.status_code == 200
        fail_body = fail_resp.json()
        assert fail_body["cached"] is False
        assert leaked_secret not in json.dumps(fail_body, ensure_ascii=False)

        failed_task = wait_task(rt, fail_body["task"])
        assert failed_task.state == "failed"
        assert leaked_secret not in (failed_task.error or "")
        assert leaked_secret not in (failed_task.message or "")
        assert leaked_secret not in json.dumps(failed_task.logs, ensure_ascii=False)
        assert leaked_secret not in json.dumps(failed_task.events, ensure_ascii=False)


def test_breeze_worker_prepare_inputs_supplies_all_required_cfg_kwargs(tmp_path):
    rt = _make_mock_runtime(tmp_path)
    runner = ColabOneRemoteTtsRunner(rt, sample_eight_tts_engines_by_id())
    script = runner.build_worker_script("breeze_tts2")
    ns: dict[str, Any] = {}
    exec(compile(script, "worker_breeze_tts2.py", "exec"), ns)

    captured: dict[str, Any] = {}

    def _upstream_prepare_inputs(
        tokenizer,
        audio_tokenizer,
        model,
        requests,
        template,
        *,
        guidance_scale: float,
        guidance_scale_ref: float | None,
        guidance_scale_ins: float | None,
    ):
        captured["requests"] = requests
        captured["guidance_scale"] = guidance_scale
        captured["guidance_scale_ref"] = guidance_scale_ref
        captured["guidance_scale_ins"] = guidance_scale_ins
        return {"ok": True}

    # Non-CFG template (build_negative_segments=None) must use guidance_scale=1.0.
    non_cfg_tpl = types.SimpleNamespace(build_negative_segments=None)
    out = ns["_prepare_breeze_inputs"](
        _upstream_prepare_inputs,
        object(),
        object(),
        object(),
        {"id": "seg-0", "text": "你好", "speaker": "S0"},
        non_cfg_tpl,
    )
    assert out == {"ok": True}
    assert captured["guidance_scale"] == 1.0
    assert captured["guidance_scale_ref"] is None
    assert captured["guidance_scale_ins"] is None

    # CFG-capable template (tts_instruction / ref_edit_tata) must use guidance_scale=4.0.
    cfg_tpl = types.SimpleNamespace(build_negative_segments=lambda *a, **kw: [])
    out_cfg = ns["_prepare_breeze_inputs"](
        _upstream_prepare_inputs,
        object(),
        object(),
        object(),
        {"id": "seg-0", "text": "你好", "speaker": "S0", "instruction": "温柔女声"},
        cfg_tpl,
    )
    assert out_cfg == {"ok": True}
    assert captured["guidance_scale"] == 4.0
    assert captured["guidance_scale_ref"] is None
    assert captured["guidance_scale_ins"] is None

    # Verify Breeze worker wraps snapshot_download in Path() for upstream `ckpt_dir / "audio_tokenizer"`.
    assert "local_dir = Path(" in script
    assert "load_runtime(local_dir, **load_kwargs)" in script


def test_drama3_worker_prefers_load_codec_model_and_patches_torchaudio(tmp_path):
    rt = _make_mock_runtime(tmp_path)
    runner = ColabOneRemoteTtsRunner(rt, sample_eight_tts_engines_by_id())
    script = runner.build_worker_script("fish_drama3")
    assert "from fish_speech.models.text2semantic.inference import load_codec_model" in script
    assert "_patch_torchaudio_load_soundfile()" in script
    assert "load_codec_model(**codec_kwargs)" in script


def test_ensure_engine_env_ming_uses_requirements_tiny_and_deduplicates_pep503(tmp_path):
    engines_map = sample_eight_tts_engines_by_id()
    ming = engines_map["ming_3_tts"]

    rt = _make_mock_runtime(tmp_path)
    recorded_cmds: list[list[str]] = []

    def _fake_cmd(cmd, **kwargs):
        recorded_cmds.append(list(cmd))

    runner = ColabOneRemoteTtsRunner(rt, engines_map, run_cmd_fn=_fake_cmd)
    env_dir = runner.engines_dir / "ming_3_tts"
    source_dir = env_dir / "source"
    source_dir.mkdir(parents=True)
    # 0.5B incompatible requirements.txt must be ignored in favor of requirements-tiny.txt.
    (source_dir / "requirements.txt").write_text(
        "diffusers==0.33.0\nsoundfile==0.12.1\naccelerate==1.3.0\n",
        encoding="utf-8",
    )
    (source_dir / "requirements-tiny.txt").write_text(
        "transformers==4.46.3\n"
        "tokenizers==0.20.3\n"
        "accelerate==1.14.0\n"
        "soundfile==0.14.0\n"
        "x_transformers==2.7.4\n"
        "fla-core==0.4.2\n"
        "torchaudio==2.6.0\n",
        encoding="utf-8",
    )

    out_dir = runner.ensure_engine_env(ColabOneTask("comfyui_tts_preview", "ming_3_tts"), ming)
    assert out_dir == env_dir

    merged_req = (env_dir / ".colabone-requirements.txt").read_text(encoding="utf-8").splitlines()
    # Never includes 0.5B-only packages or torch/torchaudio.
    assert not any("diffusers==0.33.0" in line for line in merged_req)
    assert not any("soundfile==0.12.1" in line for line in merged_req)
    assert not any(line.startswith("torchaudio") for line in merged_req)
    # Upgrades unpinned catalog entries to requirements-tiny.txt pins without duplicates.
    assert "soundfile==0.14.0" in merged_req
    assert "accelerate==1.14.0" in merged_req
    assert "x_transformers==2.7.4" in merged_req
    assert "fla-core==0.4.2" in merged_req
    assert "transformers==4.46.3" in merged_req
    # Every normalized PEP-503 package name appears at most once.
    norm_names = [re.sub(r"[-_.]+", "-", re.match(r"^[A-Za-z0-9_.-]+", s).group(0)).lower() for s in merged_req]
    assert len(norm_names) == len(set(norm_names))

    marker = json.loads((env_dir / ".ready.json").read_text(encoding="utf-8"))
    assert marker["requirements_file"] == "requirements-tiny.txt"


def test_indextts2_worker_restores_persistent_cache_dirs_after_import(tmp_path):
    rt = _make_mock_runtime(tmp_path)
    runner = ColabOneRemoteTtsRunner(rt, sample_eight_tts_engines_by_id())
    script = runner.build_worker_script("indextts2")
    assert 'os.environ["HF_HUB_CACHE"] = cache_dir' in script
    assert 'os.environ["HUGGINGFACE_HUB_CACHE"] = cache_dir' in script
    assert 'os.environ["MODELSCOPE_CACHE"] = str(Path(cache_dir) / "modelscope")' in script
    assert "_patch_torchaudio_load_soundfile()" in script


def test_moss_catalog_runtime_deps_and_common_torchaudio_fallback(tmp_path, monkeypatch):
    engines_map = sample_eight_tts_engines_by_id()
    moss = engines_map["moss_tts_v1_5"]
    pip_pkgs = set(moss["pip_packages"])
    for expected in (
        "transformers==5.0.0",
        "tiktoken",
        "librosa",
        "scipy",
        "pyyaml",
        "orjson",
        "tqdm",
    ):
        assert expected in pip_pkgs, f"moss_tts_v1_5 missing required runtime dep: {expected}"

    rt = _make_mock_runtime(tmp_path)
    runner = ColabOneRemoteTtsRunner(rt, engines_map)
    for eng_id in ("moss_tts_v1_5", "breeze_tts2", "ming_3_tts", "fish_drama3", "indextts2"):
        script = runner.build_worker_script(eng_id)
        assert "_patch_torchaudio_load_soundfile()" in script

    # Exercise _patch_torchaudio_load_soundfile fallback with fake torchaudio/soundfile/torch.
    ns: dict[str, Any] = {}
    exec(compile(runner.build_worker_script("moss_tts_v1_5"), "worker_moss.py", "exec"), ns)

    class _FakeArray:
        def __init__(self, data):
            self.data = data

        @property
        def T(self):
            return _FakeArray([list(row) for row in zip(*self.data)])

    fake_ta = types.ModuleType("torchaudio")

    def _broken_load(*args, **kwargs):
        raise RuntimeError("torchcodec is not installed")

    fake_ta.load = _broken_load  # type: ignore[attr-defined]

    fake_np = types.ModuleType("numpy")
    fake_np.asarray = lambda x: _FakeArray(x)  # type: ignore[attr-defined]
    fake_np.ascontiguousarray = lambda x: x  # type: ignore[attr-defined]

    fake_sf = types.ModuleType("soundfile")
    fake_sf.read = lambda src, **kw: ([[0.25], [-0.25], [0.5]], 24000)  # type: ignore[attr-defined]

    fake_torch = types.ModuleType("torch")
    fake_torch.from_numpy = lambda arr: ("tensor", arr.data)  # type: ignore[attr-defined]

    monkeypatch.setitem(sys.modules, "torchaudio", fake_ta)
    monkeypatch.setitem(sys.modules, "numpy", fake_np)
    monkeypatch.setitem(sys.modules, "soundfile", fake_sf)
    monkeypatch.setitem(sys.modules, "torch", fake_torch)

    ns["_patch_torchaudio_load_soundfile"]()
    tensor, sr = fake_ta.load(str(tmp_path / "ref.wav"))
    assert sr == 24000
    assert tensor == ("tensor", [[0.25, -0.25, 0.5]])


def test_breeze_preview_rejects_missing_ref_text_upfront_and_logs_duration(
    tmp_path, monkeypatch
):
    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(200 * 1024**3, 20 * 1024**3, 180 * 1024**3),
    )

    rt = ColabOneRuntime(tmp_path / "remote", ROOT / "catalog")
    monkeypatch.setattr(
        rt,
        "gpu_info",
        lambda: {"name": "NVIDIA A100-SXM4-80GB", "total_mib": 81920, "used_mib": 512},
    )

    synth_calls: list[dict] = []
    rt.comfy.tts.synth_fn = _make_offline_synth_fn(synth_calls, duration_sec=0.6)

    # 1. compute_preview_id ignores voice_preset when ref_audio_sha256 is set.
    pid_a, _ = rt.comfy.tts.compute_preview_id(
        "fish_drama3",
        voice_preset="shen_zhiyi",
        preview_text="同一段克隆试听",
        ref_audio_sha256="a" * 64,
    )
    pid_b, _ = rt.comfy.tts.compute_preview_id(
        "fish_drama3",
        voice_preset="lin_xiaoman",
        preview_text="同一段克隆试听",
        ref_audio_sha256="a" * 64,
    )
    assert pid_a == pid_b

    # 2. Direct synthesize() rejects breeze_tts2 + ref_audio without ref_text upfront.
    ref_wav = tmp_path / "ref.wav"
    write_pcm16_wav(ref_wav, make_sine_samples(0.2, sample_rate=24000), sample_rate=24000)
    with pytest.raises(ValueError, match="参考音色对应文字"):
        rt.comfy.tts.synthesize(
            ColabOneTask("comfyui_tts_preview", "breeze_tts2"),
            engine_id="breeze_tts2",
            script_text="测试文本",
            work_dir=tmp_path / "w1",
            ref_audio_path=ref_wav,
            ref_text="",
        )
    assert len(synth_calls) == 0

    # 3. API rejects breeze_tts2 + ref_audio without ref_text with 400 before queuing a task.
    saved_input = rt.comfy.store.save_input(
        "ref.wav", base64.b64encode(ref_wav.read_bytes()).decode("ascii")
    )
    token = "breeze-upfront-validation-token-32ch"
    auth = {"Authorization": "Bearer " + token}
    app = create_colabone_app(rt, token, background=False)
    with TestClient(app) as client:
        resp = client.post(
            "/api/v1/comfyui/tts-preview",
            json={
                "engine_id": "breeze_tts2",
                "voice_preset": "custom",
                "preview_text": "你好",
                "ref_audio_input_id": saved_input["id"],
                "ref_text": "",
            },
            headers=auth,
        )
        assert resp.status_code == 400
        assert "参考音色对应文字" in resp.text
        assert len(synth_calls) == 0

        # 4. Successful preview logs exact duration from duration_seconds.
        ok_resp = client.post(
            "/api/v1/comfyui/tts-preview",
            json={"engine_id": "voxcpm2", "voice_preset": "shen_zhiyi"},
            headers=auth,
        )
        assert ok_resp.status_code == 200
        done_task = wait_task(rt, ok_resp.json()["task"])
        assert done_task.state == "completed"
        joined_logs = json.dumps(done_task.logs, ensure_ascii=False)
        assert "音色试听音频已就绪（时长 " in joined_logs


def test_server_voice_presets_match_catalog_verbatim():
    from server.tts_engines import COLABONE_TTS_VOICE_PRESETS

    for eng in sample_eight_tts_engines_by_id().values():
        for vp in eng.get("voice_presets", []):
            canonical = COLABONE_TTS_VOICE_PRESETS.get(vp["id"])
            assert canonical is not None, f"missing canonical preset {vp['id']}"
            for field in (
                "name",
                "gender",
                "style",
                "description",
                "preview_text",
                "speaker_id",
                "seed",
            ):
                assert vp.get(field) == canonical.get(field), (
                    f"{eng['id']}/{vp['id']}.{field} mismatch"
                )


def test_catalog_ordered_engines_defaults_and_schema_parity():
    raw_text = (ROOT / "catalog" / "comfyui.json").read_text(encoding="utf-8")
    catalog = ComfyCatalog.model_validate_json(raw_text)

    # Exact canonical engine order & moss_tts_v1_5 pinned aux_repo.
    assert [e.id for e in catalog.tts_engines] == list(EXPECTED_FLAGSHIP_ENGINE_IDS)
    engines_by_id = {e.id: e for e in catalog.tts_engines}
    assert engines_by_id["moss_tts_v1_5"].aux_repo == "OpenMOSS-Team/MOSS-Audio-Tokenizer"
    assert _HEX_40_RE.fullmatch(engines_by_id["moss_tts_v1_5"].aux_revision)
    assert engines_by_id["breeze_tts2"].min_vram_mib >= 12288
    assert engines_by_id["fish_drama3"].min_disk_mib >= 49152
    assert engines_by_id["moss_tts_v1_5"].min_disk_mib >= 28672

    # Breeze & Ming built-in presets must use Chinese instructions matching Chinese scripts.
    for eid in ("breeze_tts2", "ming_3_tts"):
        for vp in engines_by_id[eid].voice_presets:
            assert any("\u4e00" <= ch <= "\u9fff" for ch in vp.prompt), (
                f"{eid}/{vp.id} prompt should be Chinese"
            )

    # Both digital-human templates default to fish_drama3 and list all 8 engines in order.
    tpl_by_id = {tpl.id: tpl for tpl in catalog.templates}
    for tid in ("digital-human-infinitetalk", "digital-human-longcat"):
        tpl = tpl_by_id[tid]
        assert tpl.pipeline is not None
        assert tpl.pipeline.default_tts_engine == "fish_drama3"
        assert tpl.pipeline.supported_tts_engines == list(EXPECTED_FLAGSHIP_ENGINE_IDS)
        tts_field = next(f for f in tpl.fields if f.key == "pipeline.tts_engine")
        assert tts_field.default == "fish_drama3"
        assert tts_field.options == list(EXPECTED_FLAGSHIP_ENGINE_IDS)

    # Duplicate voice_presets[].id rejected in schema.
    dup_spec = engines_by_id["voxcpm2"].model_dump()
    dup_spec["voice_presets"] = [dup_spec["voice_presets"][0], dup_spec["voice_presets"][0]]
    with pytest.raises(ValidationError):
        ComfyTtsEngineSpec.model_validate(dup_spec)

    # hf_token rejects C1 control chars and non-ASCII spaces (matching Go parity).
    for bad_tok in ("hf_abc\x80def", "\u00a0hf_abc\u00a0", "\u3000hf_abc\u3000"):
        with pytest.raises(ValidationError):
            ComfyTtsPreviewRequest.model_validate({"engine_id": "voxcpm2", "hf_token": bad_tok})


def test_indextts2_pcm_scale_float32_not_clipped_and_use_qwen_emo_gated(tmp_path):
    rt = _make_mock_runtime(tmp_path)
    runner = ColabOneRemoteTtsRunner(rt, sample_eight_tts_engines_by_id())
    script = runner.build_worker_script("indextts2")
    assert '"use_qwen_emo": bool(emotion_prompt and not emotion_ref_path)' in script
    assert 'os.environ.setdefault("USE_MODELSCOPE", "false")' in script
    # Must NOT clobber upstream save_pcm_wav (which handles / 32767.0 before torchaudio.save).
    assert "_indextts_common.save_pcm_wav =" not in script

    # Even direct calls to _colabone_save_pcm_wav with PCM-scale float32 [-16384, +16384] must NOT clip!
    ns: dict[str, Any] = {}
    exec(compile(script, "worker_indextts2.py", "exec"), ns)
    out_wav = tmp_path / "indextts_out.wav"
    pcm_scale_float = [16384.0 * math.sin(2.0 * math.pi * 440.0 * i / 22050.0) for i in range(441)]
    ns["_colabone_save_pcm_wav"](str(out_wav), pcm_scale_float, 22050)
    with wave.open(str(out_wav), "rb") as wf:
        raw = wf.readframes(wf.getnframes())
    samples = struct.unpack(f"<{len(raw) // 2}h", raw)
    peak = max(abs(s) for s in samples) / 32767.0
    assert 0.45 <= peak <= 0.55, f"expected ~0.5 unclipped peak, got {peak}"


def test_ming_worker_protocol_and_moss_language_and_drama3_guards(tmp_path):
    rt = _make_mock_runtime(tmp_path)
    runner = ColabOneRemoteTtsRunner(rt, sample_eight_tts_engines_by_id())

    # 1. Ming-3.0-TTS: never passes prompt_text on dialect/emotion/freeform; exact 14 tags; multilingual before paralinguistic.
    ming_ns: dict[str, Any] = {}
    exec(compile(runner.build_worker_script("ming_3_tts"), "worker_ming.py", "exec"), ming_ns)
    select_ming = ming_ns["_select_ming_task"]
    has_tag = ming_ns["_has_ming_paralinguistic_tag"]

    for emo, expected_task in (("四川话", "dialect"), ("高兴", "emotion"), ("轻快热情地讲述", "freeform")):
        task_name, _ctrls, ref_kw = select_ming("测试句子", "/tmp/ref.wav", "参考文字", "", emo)
        assert task_name == expected_task
        assert "prompt_text" not in ref_kw
        assert ref_kw == {"prompt_wav": "/tmp/ref.wav"}

    task_seed, _, seed_ref = select_ming("你好世界", "/tmp/ref.wav", "参考文字", "", "")
    assert task_seed == "seed"
    assert seed_ref["prompt_text"] == "参考文字"

    assert has_tag("[Laughter] 哈哈") is True
    assert has_tag("[1] 任意标签 <note>") is False
    # Japanese text with [Sigh] must route to multilingual (NOT paralinguistic which only allows ZH/EN).
    task_ja, _, _ = select_ming("[Sigh] こんにちは", "/tmp/ref.wav", "参考文字", "", "")
    assert task_ja == "multilingual"

    # 2. MOSS-TTS v1.5: ambiguous scripts return None (no forced English!); pinned codec_path + <32GiB CPU tokenizer.
    moss_script = runner.build_worker_script("moss_tts_v1_5")
    moss_ns: dict[str, Any] = {}
    exec(compile(moss_script, "worker_moss.py", "exec"), moss_ns)
    detect_moss = moss_ns["_detect_moss_language"]
    assert detect_moss("Bonjour tout le monde") is None
    assert detect_moss("Hello world") is None
    assert detect_moss("你好世界") is None
    assert detect_moss("こんにちは") == "Japanese"
    assert detect_moss("안녕하세요") == "Korean"
    assert "codec_path=local_codec_dir" in moss_script
    assert "32 * 1024 * 1024 * 1024" in moss_script

    # 3. Drama 3: codec_checkpoint_path + mixed bracket emotion tag does not double-wrap.
    drama_script = runner.build_worker_script("fish_drama3")
    assert "codec_checkpoint_path" in drama_script
    drama_ns: dict[str, Any] = {}
    exec(compile(drama_script, "worker_drama3.py", "exec"), drama_ns)
    tagged = drama_ns["_apply_drama3_emotion_tag"]("你好", "[温柔] 慢速")
    assert "[[" not in tagged


def test_qwen3_custom_voice_preserves_emotion_and_cross_engine_allowlist(tmp_path):
    rt = _make_mock_runtime(tmp_path)
    synth_calls: list[dict] = []
    runner = ColabOneRemoteTtsRunner(
        rt,
        sample_eight_tts_engines_by_id(),
        synth_fn=_make_offline_synth_fn(synth_calls, duration_sec=0.3),
    )

    # Qwen3 CustomVoice speaker must NOT fill voice_prompt with static preset prompt, preserving emotion_prompt.
    runner.synthesize(
        ColabOneTask("comfyui_tts_preview", "qwen3_tts"),
        engine_id="qwen3_tts",
        script_text="今天心情非常激动。",
        work_dir=tmp_path / "qwen_cv",
        voice_preset="qwen_vivian",
        voice_prompt="",
        emotion_prompt="用非常激动的语气说",
    )
    assert len(synth_calls) == 1
    assert synth_calls[0]["kwargs"]["speaker_id"] == "Vivian"
    assert synth_calls[0]["kwargs"]["voice_prompt"] == ""
    assert synth_calls[0]["kwargs"]["emotion_prompt"] == "用非常激动的语气说"

    # Cross-engine Qwen-only preset MUST be rejected on fish_drama3.
    with pytest.raises(ValueError, match="不支持该内置音色"):
        runner.synthesize(
            ColabOneTask("comfyui_tts_preview", "fish_drama3"),
            engine_id="fish_drama3",
            script_text="测试跨引擎校验",
            work_dir=tmp_path / "drama_bad",
            voice_preset="qwen_vivian",
        )

    # Stale user ref_text MUST NOT leak into built-in preset anchor cloning.
    presets_dir = runner.engines_dir / "_voice_presets"
    presets_dir.mkdir(parents=True, exist_ok=True)
    anchor_wav = presets_dir / "shen_zhiyi.wav"
    write_pcm16_wav(anchor_wav, make_sine_samples(0.2, sample_rate=48000), sample_rate=48000)
    (presets_dir / "shen_zhiyi.txt").write_text("内置标准锚点文案\n", encoding="utf-8")
    from server.tts_engines import resolve_colabone_voice_preset

    preset_spec = resolve_colabone_voice_preset(runner.get_engine("fish_drama3"), "shen_zhiyi")
    assert preset_spec is not None
    _, _, _, expected_meta = runner._preset_anchor_fingerprint(preset_spec)
    (presets_dir / "shen_zhiyi.meta.json").write_text(
        json.dumps(expected_meta, ensure_ascii=False), encoding="utf-8"
    )
    synth_calls.clear()
    runner.synthesize(
        ColabOneTask("comfyui_tts_preview", "fish_drama3"),
        engine_id="fish_drama3",
        script_text="测试内置音色覆盖旧参考文字",
        work_dir=tmp_path / "drama_ok",
        voice_preset="shen_zhiyi",
        ref_text="完全无关的旧自定义参考文字",
    )
    assert len(synth_calls) == 1
    assert synth_calls[0]["engine_id"] == "fish_drama3"
    assert synth_calls[0]["kwargs"]["ref_text"] == "内置标准锚点文案"


def test_common_header_6arg_torchaudio_3d_stereo_and_atomic_angle_tags(tmp_path, monkeypatch):
    from server.tts_engines import split_colabone_tts_script

    # Angle-bracket tags (<|speaker:0|>, <字|PINYIN>, <laughter>) must never split mid-tag.
    for text, limit in (
        ("甲" * 117 + "<重|CHONG2>庆欢迎您", 120),
        ("甲" * 115 + "<laughter>哈哈大笑", 120),
        ("a" * 315 + "<|speaker:1|> hello world", 320),
    ):
        segs = split_colabone_tts_script(text, max_chars=limit)
        joined = "".join(segs)
        for token in ("<重|CHONG2>", "<laughter>", "<|speaker:1|>"):
            if token in text:
                assert any(token in seg for seg in segs), f"{token} was split across {segs!r}"

    # 6 positional args to patched torchaudio.load must NOT raise TypeError.
    rt = _make_mock_runtime(tmp_path)
    runner = ColabOneRemoteTtsRunner(rt, sample_eight_tts_engines_by_id())
    ns: dict[str, Any] = {}
    exec(compile(runner.build_worker_script("voxcpm2"), "worker_vox.py", "exec"), ns)

    received: list[tuple] = []
    fake_ta = types.ModuleType("torchaudio")

    def _orig_load(uri, frame_offset=0, num_frames=-1, normalize=True, channels_first=True, format=None):
        received.append((uri, frame_offset, num_frames, normalize, channels_first, format))
        return ("ok", 24000)

    fake_ta.load = _orig_load  # type: ignore[attr-defined]
    monkeypatch.setitem(sys.modules, "torchaudio", fake_ta)
    ns["_patch_torchaudio_load_soundfile"]()
    res = fake_ta.load("sample.wav", 0, -1, True, True, "wav")
    assert res == ("ok", 24000)
    assert received == [("sample.wav", 0, -1, True, True, "wav")]


def test_preview_chunked_get_corruption_rejection_and_upfront_guards(tmp_path, monkeypatch):
    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(200 * 1024**3, 20 * 1024**3, 180 * 1024**3),
    )
    rt = ColabOneRuntime(tmp_path / "remote", ROOT / "catalog")
    monkeypatch.setattr(
        rt,
        "gpu_info",
        lambda: {"name": "NVIDIA A100-SXM4-80GB", "total_mib": 81920, "used_mib": 512},
    )
    synth_calls: list[dict] = []
    rt.comfy.tts.synth_fn = _make_offline_synth_fn(synth_calls, duration_sec=0.5)

    # Empty snapshot dir must NOT bypass check_disk().
    empty_snap = (
        rt.comfy.tts.engines_dir
        / "_hf_cache"
        / "models--fishaudio--drama-3"
        / "snapshots"
        / rt.comfy.tts.get_engine("fish_drama3")["revision"]
    )
    empty_snap.mkdir(parents=True, exist_ok=True)
    assert rt.comfy.tts._has_engine_model_cache(rt.comfy.tts.get_engine("fish_drama3")) is False

    token = "chunked-preview-regression-token-32ch"
    auth = {"Authorization": "Bearer " + token}
    app = create_colabone_app(rt, token, background=False)
    ref_wav = tmp_path / "ref.wav"
    write_pcm16_wav(ref_wav, make_sine_samples(0.2, sample_rate=24000), sample_rate=24000)
    saved_audio = rt.comfy.store.save_input(
        "ref.wav", base64.b64encode(ref_wav.read_bytes()).decode("ascii")
    )

    with TestClient(app) as client:
        # Pure inline tag text, fish_drama3 / ming_3_tts missing ref_text, and clone-only custom without ref audio all fail with 400 upfront!
        for bad_payload in (
            {"engine_id": "fish_drama3", "voice_preset": "shen_zhiyi", "preview_text": "[laugh]"},
            {"engine_id": "fish_drama3", "voice_preset": "custom", "preview_text": "你好", "ref_audio_input_id": saved_audio["id"], "ref_text": ""},
            {"engine_id": "ming_3_tts", "voice_preset": "custom", "preview_text": "你好", "ref_audio_input_id": saved_audio["id"], "ref_text": ""},
            {"engine_id": "fish_drama3", "voice_preset": "custom", "preview_text": "你好"},
        ):
            r = client.post("/api/v1/comfyui/tts-preview", json=bad_payload, headers=auth)
            assert r.status_code == 400, f"expected 400 for {bad_payload}, got {r.status_code}: {r.text}"
        assert len(synth_calls) == 0

        # Synthesize a preview, then fetch in small chunks via offset & length and verify exact SHA-256 reassembly.
        miss = client.post(
            "/api/v1/comfyui/tts-preview",
            json={"engine_id": "voxcpm2", "voice_preset": "shen_zhiyi", "preview_text": "分段拉取回归测试。"},
            headers=auth,
        )
        assert miss.status_code == 200
        miss_body = miss.json()
        assert miss_body["sample_rate"] == 48000
        assert miss_body["voice_preset"] == "shen_zhiyi"
        pid = miss_body["preview_id"]
        done = wait_task(rt, miss_body["task"])
        assert done.state == "completed"

        assembled = bytearray()
        offset = 0
        expected_sha = ""
        expected_size = 0
        while True:
            chunk_resp = client.get(
                f"/api/v1/comfyui/tts-previews/{pid}",
                params={"offset": offset, "length": 4096},
                headers=auth,
            )
            assert chunk_resp.status_code == 200
            chunk_body = chunk_resp.json()
            raw_chunk = base64.b64decode(chunk_body["data"])
            assembled.extend(raw_chunk)
            expected_sha = chunk_body["sha256"]
            expected_size = chunk_body["size"]
            offset += len(raw_chunk)
            if not chunk_body["has_more"]:
                break
        assert len(assembled) == expected_size
        assert hashlib.sha256(bytes(assembled)).hexdigest() == expected_sha

        # Corrupt the cached WAV on disk: get_cached_preview detects SHA-256/size mismatch and returns 404.
        (rt.comfy.tts.previews_dir / f"{pid}.wav").write_bytes(b"RIFFcorrupted")
        corrupt_resp = client.get(f"/api/v1/comfyui/tts-previews/{pid}", headers=auth)
        assert corrupt_resp.status_code == 404


# ---------------------------------------------------------------------------
# 11. Round-2 Cross-Verification Regression Suite
# ---------------------------------------------------------------------------


def test_r2_worker_drama3_devices_dtype_vram_and_breeze_pre_ampere(tmp_path):
    """Verify Drama 3 plural `devices` binding + <48 GB FP8 guard, and Breeze pre-Ampere
    FP32 RoPE `inv_freq` + `text_encoder` preservation & upfront CUDA guard."""
    rt = _make_mock_runtime(tmp_path)
    runner = ColabOneRemoteTtsRunner(rt, sample_eight_tts_engines_by_id())
    drama_src = runner.build_worker_script("fish_drama3")
    breeze_src = runner.build_worker_script("breeze_tts2")
    drama_ns: dict[str, Any] = {}
    breeze_ns: dict[str, Any] = {}
    exec(compile(drama_src, "worker_drama3_r2.py", "exec"), drama_ns)
    exec(compile(breeze_src, "worker_breeze_r2.py", "exec"), breeze_ns)

    # 1. Drama 3: _normalize_drama3_devices preserves both backbone devices & tower device.
    codec_dev, all_devs = drama_ns["_normalize_drama3_devices"](
        (["cuda:0", "cuda:1"], "cuda:1"), "cuda:0"
    )
    assert codec_dev == "cuda:1"
    assert all_devs == ["cuda:0", "cuda:1", "cuda:1"]

    # Drama 3 script binds plural `devices` when calling resolve_drama3_dtype & checks <45056 MiB & hip.
    assert 'p.name == "devices"' in drama_src
    assert "fish_speech.models.text2semantic.fish_omni.generate" in drama_src
    assert "vram_mib < 45056" in drama_src
    assert '"hip"' in drama_src

    # 2. Breeze TTS 2: upfront CUDA guard, seed clamping, dict chunk guard, 3D stereo squeeze.
    assert "Breeze TTS 2 需要 NVIDIA CUDA GPU 才能运行" in breeze_src
    assert breeze_ns["_extract_breeze_raw_chunk"]({"audio": None}) is None
    assert breeze_ns["_extract_breeze_raw_chunk"]({"audio": [0.1, 0.2]}) == [0.1, 0.2]

    # 3D (1, 2, N) downmix in _chunk_to_mono_f32 (when numpy is present).
    try:
        import numpy as np
    except ImportError:
        np = None
    if np is not None:
        stereo_3d = np.array([[[0.2, 0.4, 0.6], [0.6, 0.8, 1.0]]], dtype=np.float32)
        mono = breeze_ns["_chunk_to_mono_f32"](stereo_3d)
        assert mono.ndim == 1 and mono.shape == (3,)
        assert np.allclose(mono, [0.4, 0.6, 0.8], atol=1e-5)

    # Mocked _cast_breeze_model_for_pre_ampere preserves *inv_freq in float32 & text_encoder in float32
    # while emitting float16 from text_encoder_proj hook.
    class _FakeTensor:
        def __init__(self, dtype: str, device: str = "cuda:0"):
            self.dtype = dtype
            self.device = device
            self.is_floating_point = lambda: self.dtype in ("fp32", "fp16", "bf16")

        def detach(self):
            return self

        def clone(self):
            return _FakeTensor(self.dtype, self.device)

        def to(self, *args, dtype=None, device=None, **kwargs):
            next_dtype = dtype or (args[0] if args and args[0] in ("fp32", "fp16", "bf16") else self.dtype)
            next_dev = device or self.device
            return _FakeTensor(next_dtype, next_dev)

    class _FakeModule:
        def __init__(self, name: str, buffers: dict[str, _FakeTensor] | None = None, children: dict[str, Any] | None = None):
            self.name = name
            self._buffers = dict(buffers or {})
            self._children = dict(children or {})
            self.dtype = "bf16"
            self.pre_hooks: list[Any] = []
            self.post_hooks: list[Any] = []
            for k, v in self._buffers.items():
                setattr(self, k, v)
            for k, v in self._children.items():
                setattr(self, k, v)

        def named_modules(self, prefix: str = ""):
            yield (prefix, self)
            for cname, child in self._children.items():
                sub = f"{prefix}.{cname}" if prefix else cname
                yield from child.named_modules(sub)

        def named_buffers(self, recurse: bool = True):
            yield from self._buffers.items()
            if recurse:
                for cname, child in self._children.items():
                    for bname, buf in child.named_buffers(recurse=True):
                        yield (f"{cname}.{bname}", buf)

        def named_children(self):
            yield from self._children.items()

        def register_buffer(self, name: str, tensor: _FakeTensor, persistent: bool = False):
            self._buffers[name] = tensor
            setattr(self, name, tensor)

        def register_forward_pre_hook(self, fn):
            self.pre_hooks.append(fn)

        def register_forward_hook(self, fn):
            self.post_hooks.append(fn)

        def to(self, *args, dtype=None, device=None, **kwargs):
            target = dtype or (args[0] if args else self.dtype)
            self.dtype = target
            for k, b in list(self._buffers.items()):
                nb = b.to(dtype=target, device=device)
                self._buffers[k] = nb
                setattr(self, k, nb)
            for child in self._children.values():
                child.to(dtype=target, device=device)
            return self

        def __call__(self, x):
            args = (x,)
            for h in self.pre_hooks:
                res = h(self, args)
                if res is not None:
                    args = res
            out = _FakeTensor(self.dtype, args[0].device)
            for h in self.post_hooks:
                res = h(self, args, out)
                if res is not None:
                    out = res
            return out

    fake_torch = types.SimpleNamespace(float32="fp32", float16="fp16", bfloat16="bf16", Tensor=_FakeTensor)
    rotary = _FakeModule("rotary", buffers={"inv_freq": _FakeTensor("fp32")})
    text_enc = _FakeModule("text_encoder", buffers={"full_attention_inv_freq": _FakeTensor("fp32")})
    text_proj = _FakeModule("text_encoder_proj")
    backbone = _FakeModule("backbone_model", children={"rotary_emb": rotary})
    fake_model = _FakeModule(
        "root",
        children={
            "text_encoder": text_enc,
            "text_encoder_proj": text_proj,
            "backbone_model": backbone,
        },
    )
    breeze_ns["_cast_breeze_model_for_pre_ampere"](fake_model, "cuda:0", fake_torch)
    assert backbone.dtype == "fp16"
    assert rotary.inv_freq.dtype == "fp32"
    assert text_enc.dtype == "fp32"
    assert text_enc.full_attention_inv_freq.dtype == "fp32"
    assert text_proj.dtype == "fp32"
    proj_out = text_proj(_FakeTensor("fp32"))
    assert proj_out.dtype == "fp16"


def test_r2_worker_moss_and_ming_language_dialect_anchor_and_talkertn(tmp_path):
    """Verify MOSS/Ming Kana punctuation & Greek math guards, Ming speaker anchor logic,
    dialect aliases, and TalkerTN _normalizer guard."""
    rt = _make_mock_runtime(tmp_path)
    runner = ColabOneRemoteTtsRunner(rt, sample_eight_tts_engines_by_id())
    moss_ns: dict[str, Any] = {}
    ming_ns: dict[str, Any] = {}
    exec(compile(runner.build_worker_script("moss_tts_v1_5"), "worker_moss_r2.py", "exec"), moss_ns)
    exec(compile(runner.build_worker_script("ming_3_tts"), "worker_ming_r2.py", "exec"), ming_ns)

    # 1. MOSS language detection: exclude Kana middle dot & isolated Greek math symbols.
    assert moss_ns["_detect_moss_language"]("阿尔伯特・爱因斯坦提出相对论") is None
    assert moss_ns["_detect_moss_language"]("这是一个 α 测试与 β 版本，π = 3.14，误差 5 μm，温差 ΔT。") is None
    assert moss_ns["_detect_moss_language"]("Use α and β parameters for ΔT estimation.") is None
    assert moss_ns["_detect_moss_language"]("Καλημέρα, πώς είστε σήμερα;") == "Greek"
    assert moss_ns["_detect_moss_language"]("東京へ行きます") == "Japanese"
    assert moss_ns["_detect_moss_language"]("안녕하세요 반갑습니다") == "Korean"

    # 2. Ming language & dialect detection.
    assert ming_ns["_detect_ming_language"]("阿尔伯特・爱因斯坦") == "Chinese"
    assert ming_ns["_detect_ming_language"]("こんにちは世界") == "Japanese"
    assert ming_ns["_detect_ming_dialect"]("广州话") == "广粤话"
    assert ming_ns["_detect_ming_dialect"]("speak in canton style") == "广粤话"

    # 3. Ming speaker anchor helper triggers for no-ref dialect/emotion/multilingual/paralinguistic
    # and long multi-segment voice_design, but NOT when ref audio is already provided or short single-seg voice_design.
    assert ming_ns["_needs_ming_reference_anchor"]([{"text": "今天吃火锅"}], "", "甜美女声", "四川话") is True
    assert ming_ns["_needs_ming_reference_anchor"]([{"text": "太开心了"}], "", "甜美女声", "高兴") is True
    assert ming_ns["_needs_ming_reference_anchor"]([{"text": "こんにちは、元気ですか"}], "", "甜美女声", "") is True
    assert ming_ns["_needs_ming_reference_anchor"]([{"text": "你好 [Laughter]"}], "", "甜美女声", "") is True
    assert ming_ns["_needs_ming_reference_anchor"]([{"text": "甲" * 60}, {"text": "乙" * 20}], "", "甜美女声", "") is True
    assert ming_ns["_needs_ming_reference_anchor"]([{"text": "简短的试听散文"}], "", "甜美女声", "") is False
    assert ming_ns["_needs_ming_reference_anchor"]([{"text": "今天吃火锅"}], "/tmp/ref.wav", "甜美女声", "四川话") is False

    # 4. Ming TalkerTN retry MUST NOT swallow post-TN FileNotFoundError when _normalizer is already initialized.
    class _FakeTtsPostTnError:
        def __init__(self):
            self.text_normalization = True
            self._normalizer = object()
            self.calls = 0

        def generate(self, **kwargs):
            self.calls += 1
            raise FileNotFoundError("missing campplus.onnx")

    fake_tts = _FakeTtsPostTnError()
    with pytest.raises(FileNotFoundError, match="campplus.onnx"):
        ming_ns["_run_ming_generate"](fake_tts, {"text": "你好"})
    assert fake_tts.calls == 1
    assert fake_tts.text_normalization is True

    # When _normalizer is None, TalkerTN failure disables text_normalization and retries once.
    class _FakeTtsTnInitError:
        def __init__(self):
            self.text_normalization = True
            self._normalizer = None
            self.calls = 0

        def generate(self, **kwargs):
            self.calls += 1
            if self.text_normalization:
                raise RuntimeError("OpenFst load failed")
            return types.SimpleNamespace(waveform=[0.1, 0.2], sample_rate=44100)

    fake_tts2 = _FakeTtsTnInitError()
    out = ming_ns["_run_ming_generate"](fake_tts2, {"text": "你好"})
    assert fake_tts2.calls == 2
    assert fake_tts2.text_normalization is False
    assert out.sample_rate == 44100


def test_r2_inline_workers_common_header_regexes_and_locks(tmp_path, monkeypatch):
    """Verify narrowed angle-bracket regexes, _safe_ta_load 1-arg wrapper + BytesIO seek,
    _to_mono_list edge cases, VoxCPM2 seed/fullwidth/emotion, Fish S2 Pro mixed brackets,
    IndexTTS2 language heuristic, and runner lock isolation."""
    import io
    import threading
    from server.tts_engines import (
        _has_speakable_text,
        _split_line_sentences,
        split_colabone_tts_script,
    )

    # 1. Narrowed angle-bracket regexes do NOT swallow prose comparisons.
    assert _has_speakable_text("< 100ms 响应 >") is True
    assert _has_speakable_text("<重|CHONG2>") is True
    assert _has_speakable_text("[laugh]") is False
    assert _has_speakable_text("<|speaker:0|>") is False
    assert _has_speakable_text("<laughter>") is False
    segs = split_colabone_tts_script("甲" * 110 + " < 10 and y > " + "a" * 115, max_chars=120)
    assert "< 10 and y >" in " ".join(segs)
    sents = _split_line_sentences("成本 < 100。收益 > 200。")
    assert len(sents) == 2

    # 2. _WORKER_COMMON_HEADER: 1-arg torchaudio.load wrapper & empty 2D / int64 _to_mono_list.
    rt = _make_mock_runtime(tmp_path)
    runner = ColabOneRemoteTtsRunner(rt, sample_eight_tts_engines_by_id())
    vox_src = runner.build_worker_script("voxcpm2")
    assert 'kwargs["seed"]' in vox_src
    assert "emotion_prompt" in vox_src
    assert "（）" in vox_src or "（" in vox_src

    ns: dict[str, Any] = {}
    exec(compile(vox_src, "worker_vox_r2.py", "exec"), ns)

    # Simplified 1-arg mock torchaudio.load works verbatim without 5-arg TypeError.
    fake_ta = types.ModuleType("torchaudio")
    seen_uris: list[Any] = []

    def _simple_load(uri):
        seen_uris.append(uri)
        return ("simple_ok", 24000)

    fake_ta.load = _simple_load  # type: ignore[attr-defined]
    monkeypatch.setitem(sys.modules, "torchaudio", fake_ta)
    ns["_patch_torchaudio_load_soundfile"]()
    assert fake_ta.load("foo.wav") == ("simple_ok", 24000)
    assert seen_uris == ["foo.wav"]

    # _to_mono_list handles Python list[int] without dividing by int64.max & empty 2D returns [].
    vals = ns["_to_mono_list"]([1, -1, 0])
    assert vals == [1.0, -1.0, 0.0]
    try:
        import numpy as np
    except ImportError:
        np = None
    if np is not None:
        assert ns["_to_mono_list"](np.zeros((1, 0), dtype=np.float32)) == []
        assert ns["_to_mono_list"](np.array([16384, -16384], dtype=np.int16)) == pytest.approx([0.5, -0.5], abs=1e-4)

    # 3. Fish S2 Pro mixed-bracket emotion tag & leading speaker tag preservation.
    fish_ns: dict[str, Any] = {}
    exec(compile(runner.build_worker_script("fish_s2_pro"), "worker_fish_r2.py", "exec"), fish_ns)
    assert fish_ns["_apply_emotion_tag"]("你好", "[温柔] 慢速") == "[温柔] [慢速] 你好"
    assert fish_ns["_apply_emotion_tag"]("<|speaker:0|> 你好", "温柔") == "<|speaker:0|> [温柔] 你好"

    # 4. IndexTTS2 language detection heuristic.
    idx_ns: dict[str, Any] = {}
    exec(compile(runner.build_worker_script("indextts2"), "worker_idx_r2.py", "exec"), idx_ns)
    assert idx_ns["_detect_indextts_lang"]("I went to the café for a résumé.") == "EN"
    assert idx_ns["_detect_indextts_lang"]("Está bien") == "ES"
    assert idx_ns["_detect_indextts_lang"]("¡Hola, gracias a todos!") == "ES"

    # 5. Lock isolation: holding per-engine env lock MUST NOT block preview cache reads!
    env_lock = runner._get_env_lock("fish_drama3")
    with env_lock:
        result_holder: list[Any] = []

        def _probe():
            result_holder.append(runner.get_cached_preview("a" * 32))

        t = threading.Thread(target=_probe)
        t.start()
        t.join(timeout=2.0)
        assert not t.is_alive(), "preview cache read blocked while env lock was held!"
        assert result_holder == [None]


def test_r2_hf_cache_sharded_index_custom_voice_lru_corrupt_meta_and_api_edges(tmp_path, monkeypatch):
    """Verify sharded *.index.json & custom_voice_repo cache checks, _prune_preview_cache_locked LRU,
    corrupt sample_rate/duration_seconds cache miss, 409 conflict/quiescing, >512 KiB cache hit,
    422/400 chunk bounds, digital_human upfront indextts2 parity, and subprocess finally safety."""
    import os

    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(200 * 1024**3, 20 * 1024**3, 180 * 1024**3),
    )
    rt = ColabOneRuntime(tmp_path / "remote", ROOT / "catalog")
    monkeypatch.setattr(
        rt,
        "gpu_info",
        lambda: {"name": "NVIDIA A100-SXM4-80GB", "total_mib": 81920, "used_mib": 512},
    )
    tts = rt.comfy.tts

    # 1. _has_engine_model_cache requires custom_voice_repo for qwen3_tts.
    qwen_spec = tts.get_engine("qwen3_tts")
    for repo_key, rev_key in (("repo", "revision"), ("aux_repo", "aux_revision")):
        repo_id = qwen_spec[repo_key]
        rev = qwen_spec[rev_key]
        snap = tts.engines_dir / "_hf_cache" / f"models--{repo_id.replace('/', '--')}" / "snapshots" / rev
        snap.mkdir(parents=True, exist_ok=True)
        (snap / "model.safetensors").write_bytes(b"weights")
    assert tts._has_engine_model_cache(qwen_spec) is False
    cv_snap = (
        tts.engines_dir
        / "_hf_cache"
        / f"models--{qwen_spec['custom_voice_repo'].replace('/', '--')}"
        / "snapshots"
        / qwen_spec["custom_voice_revision"]
    )
    cv_snap.mkdir(parents=True, exist_ok=True)
    (cv_snap / "model.safetensors").write_bytes(b"weights")
    assert tts._has_engine_model_cache(qwen_spec) is True

    # 2. Sharded *.index.json must verify ALL referenced shards exist and are non-empty.
    drama_spec = tts.get_engine("fish_drama3")
    d_snap = (
        tts.engines_dir
        / "_hf_cache"
        / f"models--{drama_spec['repo'].replace('/', '--')}"
        / "snapshots"
        / drama_spec["revision"]
    )
    d_snap.mkdir(parents=True, exist_ok=True)
    (d_snap / "model-00001-of-00002.safetensors").write_bytes(b"shard1")
    (d_snap / "model.safetensors.index.json").write_text(
        json.dumps(
            {
                "weight_map": {
                    "layer.0": "model-00001-of-00002.safetensors",
                    "layer.1": "model-00002-of-00002.safetensors",
                }
            }
        ),
        encoding="utf-8",
    )
    assert tts._has_engine_model_cache(drama_spec) is False
    (d_snap / "model-00002-of-00002.safetensors").write_bytes(b"shard2")
    assert tts._has_engine_model_cache(drama_spec) is True

    # 3. _prune_preview_cache_locked removes orphans, evicts oldest LRU, preserves keep_pid & hot entries.
    tts.previews_dir.mkdir(parents=True, exist_ok=True)
    (tts.previews_dir / f"{'0' * 32}.wav").write_bytes(b"orphan")
    (tts.previews_dir / f"{'1' * 32}.json").write_text("{}", encoding="utf-8")
    p_old = "a" * 32
    p_hot = "b" * 32
    p_keep = "c" * 32
    for idx, pid in enumerate((p_old, p_hot, p_keep)):
        raw = b"RIFF" + bytes([idx]) * 100
        wav_p = tts.previews_dir / f"{pid}.wav"
        meta_p = tts.previews_dir / f"{pid}.json"
        wav_p.write_bytes(raw)
        meta_p.write_text(
            json.dumps(
                {
                    "preview_id": pid,
                    "size": len(raw),
                    "sha256": hashlib.sha256(raw).hexdigest(),
                    "sample_rate": 48000,
                    "duration_seconds": 0.5,
                }
            ),
            encoding="utf-8",
        )
        ts = 1000.0 + idx * 100.0
        os.utime(wav_p, (ts, ts))
        os.utime(meta_p, (ts, ts))
    # Touch p_hot so it is newer than p_keep & p_old, then cap entries to 2 with keep_pid=p_keep.
    os.utime(tts.previews_dir / f"{p_hot}.wav", (5000.0, 5000.0))
    monkeypatch.setattr("server.tts_engines.MAX_PREVIEW_CACHE_ENTRIES", 2)
    with tts._preview_lock:
        tts._prune_preview_cache_locked(keep_pid=p_keep)
    assert not (tts.previews_dir / f"{'0' * 32}.wav").exists()
    assert not (tts.previews_dir / f"{'1' * 32}.json").exists()
    assert not (tts.previews_dir / f"{p_old}.wav").exists()
    assert (tts.previews_dir / f"{p_hot}.wav").is_file()
    assert (tts.previews_dir / f"{p_keep}.wav").is_file()

    # 4. Corrupt sample_rate / duration_seconds in preview .json returns None (not ValueError).
    corrupt_pid = "d" * 32
    raw_ok = b"RIFF" + b"\x00" * 64
    (tts.previews_dir / f"{corrupt_pid}.wav").write_bytes(raw_ok)
    (tts.previews_dir / f"{corrupt_pid}.json").write_text(
        json.dumps(
            {
                "preview_id": corrupt_pid,
                "size": len(raw_ok),
                "sha256": hashlib.sha256(raw_ok).hexdigest(),
                "sample_rate": "corrupt_sr",
                "duration_seconds": 1.0,
            }
        ),
        encoding="utf-8",
    )
    assert tts.get_cached_preview(corrupt_pid) is None
    assert tts.get_cached_preview_chunk(corrupt_pid, 0, 1024) is None

    # 5. Oversized (> 512 KiB) cache hit omits inline data, chunked GET returns 422/400/409 properly.
    synth_calls: list[dict] = []
    # ~6.0s at 48 kHz mono PCM16 = ~576 KiB (> 512 KiB).
    tts.synth_fn = _make_offline_synth_fn(synth_calls, duration_sec=6.0)
    token = "r2-preview-api-regression-token-32ch"
    auth = {"Authorization": "Bearer " + token}
    app = create_colabone_app(rt, token, background=False)
    with TestClient(app) as client:
        r_miss = client.post(
            "/api/v1/comfyui/tts-preview",
            json={"engine_id": "voxcpm2", "voice_preset": "shen_zhiyi", "preview_text": "大体积试听缓存测试。"},
            headers=auth,
        )
        assert r_miss.status_code == 200
        pid_big = r_miss.json()["preview_id"]
        done = wait_task(rt, r_miss.json()["task"])
        assert done.state == "completed"

        # Cache hit > 512 KiB strips inline `data` while keeping size & sha256.
        r_hit = client.post(
            "/api/v1/comfyui/tts-preview",
            json={"engine_id": "voxcpm2", "voice_preset": "shen_zhiyi", "preview_text": "大体积试听缓存测试。"},
            headers=auth,
        )
        assert r_hit.status_code == 200
        hit_body = r_hit.json()
        assert hit_body["cached"] is True
        assert "data" not in hit_body
        assert hit_body["size"] > 512 * 1024
        assert len(hit_body["sha256"]) == 64

        # Invalid query bounds -> 422; offset >= size -> 400 (and does not touch mtime).
        assert client.get(f"/api/v1/comfyui/tts-previews/{pid_big}", params={"offset": -1}, headers=auth).status_code == 422
        assert client.get(f"/api/v1/comfyui/tts-previews/{pid_big}", params={"length": 512 * 1024 + 1}, headers=auth).status_code == 422
        wav_big = tts.previews_dir / f"{pid_big}.wav"
        os.utime(wav_big, (2222.0, 2222.0))
        r_oob = client.get(
            f"/api/v1/comfyui/tts-previews/{pid_big}",
            params={"offset": hit_body["size"], "length": 4096},
            headers=auth,
        )
        assert r_oob.status_code == 400
        assert int(wav_big.stat().st_mtime) == 2222

        # bool offset/length rejected directly in get_tts_preview.
        with pytest.raises(ValueError, match="读取范围不合法"):
            rt.comfy.get_tts_preview(pid_big, offset=True, length=1024)

        # Quiescing returns 409 on both POST and GET.
        rt.quiescing = True
        try:
            assert client.post(
                "/api/v1/comfyui/tts-preview",
                json={"engine_id": "voxcpm2", "voice_preset": "shen_zhiyi", "preview_text": "释放中测试。"},
                headers=auth,
            ).status_code == 409
            assert client.get(f"/api/v1/comfyui/tts-previews/{pid_big}", headers=auth).status_code == 409
        finally:
            rt.quiescing = False

    # 6. digital_human upfront parity: indextts2 + omitted voice_preset + synth_fn fails BEFORE VRAM teardown.
    stop_calls: list[int] = []
    free_calls: list[int] = []
    monkeypatch.setattr(rt, "_stop_engine", lambda: stop_calls.append(1))
    monkeypatch.setattr(rt.comfy, "free_memory", lambda **kw: free_calls.append(1))
    from PIL import Image as _Image

    portrait_path = tmp_path / "portrait.png"
    _Image.new("RGB", (64, 64), (120, 140, 160)).save(portrait_path, format="PNG")
    cat = ComfyCatalog.model_validate_json((ROOT / "catalog" / "comfyui.json").read_text(encoding="utf-8"))
    dh_wf = next(w.model_dump() for w in cat.templates if w.id == "digital-human-infinitetalk")
    dh_img_key = dh_wf["pipeline"]["image_node_key"]
    dh_task = ColabOneTask("comfyui_run", "dh-indextts2-preflight")
    dh_req = types.SimpleNamespace(
        overrides={
            "pipeline.audio_source": "tts",
            "pipeline.tts_engine": "indextts2",
            "pipeline.script_text": "你好测试",
        },
        hf_token="",
    )
    with pytest.raises(ValueError, match="IndexTTS2"):
        rt.comfy.digital_human.run(
            dh_task,
            dh_wf,
            dh_req,
            [(dh_img_key, "portrait.png", portrait_path)],
        )
    assert stop_calls == []
    assert free_calls == []

    # 7. _run_tts_subprocess finally restores task.cancel_hook even if log dir becomes chmod 000 mid-run.
    from server.tts_engines import _run_tts_subprocess

    locked_dir = tmp_path / "locked_logs"
    locked_dir.mkdir()
    locked_log = locked_dir / "worker.log"
    locked_log.write_text("secret=hf_test_secret_token_99999\n", encoding="utf-8")
    try:
        sub_task = ColabOneTask("comfyui_tts_preview", "finally-hook")
        sentinel_hook = lambda: None
        sub_task.cancel_hook = sentinel_hook
        with pytest.raises(RuntimeError, match="boom-original"):
            def _boom_popen(*args, **kwargs):
                os.chmod(locked_dir, 0o000)
                raise RuntimeError("boom-original")

            monkeypatch.setattr("subprocess.Popen", _boom_popen)
            _run_tts_subprocess(
                ["echo", "hi"],
                sub_task,
                locked_log,
                timeout=5,
                secret_to_scrub="hf_test_secret_token_99999",
            )
        assert sub_task.cancel_hook is sentinel_hook
    finally:
        os.chmod(locked_dir, 0o755)



