"""Comprehensive 100% offline pytest suite for ComfyUI workflow packs, WS progress,
multi-engine remote TTS, and long-video digital human pipelines.

All tests run strictly offline (zero live internet, zero real git/pip/ffmpeg/GPU).
"""

from __future__ import annotations

import ast
import base64
import copy
import hashlib
import inspect
import io
import json
import math
import re
import socket
import struct
import threading
import time
import wave
from collections import namedtuple
from pathlib import Path, PurePosixPath
from unittest.mock import Mock

import pytest
from PIL import Image
from pydantic import ValidationError

from common.comfyui import (
    ComfyCatalog,
    ComfyNodePatch,
    ComfyPipelineSpec,
    ComfyTtsEngineSpec,
    ComfyTtsVoicePreset,
    WorkflowField,
    WorkflowRunRequest,
)
from common.files import atomic_json
from server.runtime import ColabOneTask

ROOT = Path(__file__).resolve().parents[1]
_HEX_40_RE = re.compile(r"^[a-fA-F0-9]{40}$")
_HEX_64_RE = re.compile(r"^[a-f0-9]{64}$")
_WS_GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
_DiskUsage = namedtuple("DiskUsage", ["total", "used", "free"])


# ---------------------------------------------------------------------------
# Pure-stdlib offline test helpers (zero numpy/scipy/soundfile dependency)
# ---------------------------------------------------------------------------


def make_png_bytes(color: str = "blue", size: tuple[int, int] = (64, 64)) -> bytes:
    buf = io.BytesIO()
    Image.new("RGB", size, color).save(buf, format="PNG")
    return buf.getvalue()


def make_sine_samples(
    duration_sec: float,
    freq_hz: float = 440.0,
    amp: float = 0.5,
    sample_rate: int = 16000,
) -> list[float]:
    count = int(round(duration_sec * sample_rate))
    omega = 2.0 * math.pi * freq_hz / sample_rate
    return [amp * math.sin(omega * i) for i in range(count)]


def make_silence_samples(duration_sec: float, sample_rate: int = 16000) -> list[float]:
    return [0.0] * int(round(duration_sec * sample_rate))


def write_pcm16_wav(
    path: Path, samples: list[float], sample_rate: int = 16000
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


def read_pcm16_wav(path: Path) -> tuple[int, int, int, list[int]]:
    with wave.open(str(path), "rb") as wf:
        channels = wf.getnchannels()
        sampwidth = wf.getsampwidth()
        sr = wf.getframerate()
        raw = wf.readframes(wf.getnframes())
    count = len(raw) // 2
    samples = list(struct.unpack(f"<{count}h", raw)) if count else []
    return channels, sampwidth, sr, samples


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


def sample_digital_human_workflow() -> dict:
    return {
        "1": {
            "class_type": "LoadImage",
            "_meta": {"title": "主播参考图"},
            "inputs": {"image": "portrait.png", "upload": "image"},
        },
        "2": {
            "class_type": "LoadAudio",
            "_meta": {"title": "驱动语音"},
            "inputs": {"audio": "voice.wav", "audioUI": ""},
        },
        "5": {
            "class_type": "WanVideoTextEncode",
            "_meta": {"title": "画面提示词"},
            "inputs": {
                "positive_prompt": "natural studio portrait speaking clearly",
                "negative_prompt": "blurry, distorted",
            },
        },
        "7": {
            "class_type": "WanVideoSampler",
            "_meta": {"title": "视频采样器"},
            "inputs": {
                "steps": 6,
                "seed": 42,
                "width": 832,
                "height": 480,
                "frame_window_size": 81,
                "fps": 25,
                "image": ["1", 0],
                "audio": ["2", 0],
                "text_embeds": ["5", 0],
            },
        },
        "10": {
            "class_type": "WanVideoDecode",
            "_meta": {"title": "VAE 解码"},
            "inputs": {"samples": ["7", 0]},
        },
        "11": {
            "class_type": "VHS_VideoCombine",
            "_meta": {"title": "合成输出视频"},
            "inputs": {
                "images": ["10", 0],
                "audio": ["2", 0],
                "frame_rate": 25,
                "format": "video/h264-mp4",
                "filename_prefix": "colabone_dh",
            },
        },
    }


def sample_tts_engines_by_id() -> dict[str, dict]:
    return {
        "voxcpm2": {
            "id": "voxcpm2",
            "name": "VoxCPM2",
            "description": "48kHz Apache-2.0 voice cloning and design",
            "repo": "openbmb/VoxCPM2",
            "revision": "7936080b41163874f38610f46f438e2c0757f9fd",
            "aux_repo": "",
            "aux_revision": "",
            "license": "Apache-2.0",
            "commercial_ok": True,
            "license_notice": "",
            "min_vram_mib": 8192,
            "sample_rate": 48000,
            "supports_voice_clone": True,
            "supports_voice_design": True,
            "supports_emotion_ref": False,
            "supports_inline_tags": False,
            "pip_packages": ["voxcpm", "soundfile", "pyloudnorm", "huggingface-hub"],
            "source_repo": "https://github.com/OpenBMB/VoxCPM.git",
            "source_commit": "300e4260b509066151d623dc9c0aa922cf82232b",
        },
        "qwen3_tts": {
            "id": "qwen3_tts",
            "name": "Qwen3-TTS 1.7B",
            "description": "Apache-2.0 multilingual TTS with voice design",
            "repo": "Qwen/Qwen3-TTS-12Hz-1.7B-Base",
            "revision": "fd4b254389122332181a7c3db7f27e918eec64e3",
            "aux_repo": "Qwen/Qwen3-TTS-12Hz-1.7B-VoiceDesign",
            "aux_revision": "5ecdb67327fd37bb2e042aab12ff7391903235d3",
            "license": "Apache-2.0",
            "commercial_ok": True,
            "license_notice": "",
            "min_vram_mib": 8192,
            "sample_rate": 24000,
            "supports_voice_clone": True,
            "supports_voice_design": True,
            "supports_emotion_ref": False,
            "supports_inline_tags": False,
            "pip_packages": ["qwen-tts", "soundfile", "pyloudnorm", "huggingface-hub"],
            "source_repo": "https://github.com/QwenLM/Qwen3-TTS.git",
            "source_commit": "022e286b98fbec7e1e916cb940cdf532cd9f488e",
        },
        "fish_s2_pro": {
            "id": "fish_s2_pro",
            "name": "Fish Audio S2 Pro",
            "description": "Expressive TTS with inline emotion tags",
            "repo": "fishaudio/s2-pro",
            "revision": "1de9996b6be38b745688de084d87a5633f714e4e",
            "aux_repo": "",
            "aux_revision": "",
            "license": "Fish Audio Research License",
            "commercial_ok": False,
            "license_notice": "仅限个人学习与非商业研究使用；如需商业使用须另行向 Fish Audio 获取商业授权。",
            "min_vram_mib": 12288,
            "sample_rate": 44100,
            "supports_voice_clone": True,
            "supports_voice_design": False,
            "supports_emotion_ref": False,
            "supports_inline_tags": True,
            "pip_packages": ["fish-speech", "soundfile", "pyloudnorm", "huggingface-hub"],
            "source_repo": "https://github.com/fishaudio/fish-speech.git",
            "source_commit": "0f8a12e8a651062fdee9c33a894bdccd3a8513f1",
        },
        "indextts2": {
            "id": "indextts2",
            "name": "IndexTTS-2",
            "description": "Disentangled emotion reference TTS",
            "repo": "IndexTeam/IndexTTS-2",
            "revision": "740dcaff396282ffb241903d150ac011cd4b1ede",
            "aux_repo": "",
            "aux_revision": "",
            "license": "IndexTTS Research / Non-Commercial",
            "commercial_ok": False,
            "license_notice": "仅限个人学习与非商业研究使用；商用前请核查 IndexTeam 上游权重许可协议。",
            "min_vram_mib": 10240,
            "sample_rate": 24000,
            "supports_voice_clone": True,
            "supports_voice_design": False,
            "supports_emotion_ref": True,
            "supports_inline_tags": False,
            "pip_packages": ["soundfile", "pyloudnorm", "huggingface-hub", "omegaconf", "einops"],
            "source_repo": "https://github.com/index-tts/index-tts.git",
            "source_commit": "d9e41aac89fd00b3d71497fddb287b7f24613712",
        },
    }


def _make_server_ws_frame(payload: bytes, opcode: int = 0x1, fin: bool = True) -> bytes:
    first = (0x80 if fin else 0x00) | (opcode & 0x0F)
    length = len(payload)
    if length < 126:
        header = bytes([first, length])
    elif length < 65536:
        header = struct.pack("!BBH", first, 126, length)
    else:
        header = struct.pack("!BBQ", first, 127, length)
    return header + payload


# ---------------------------------------------------------------------------
# 1. Schema & Real Catalog Validation
# ---------------------------------------------------------------------------


def test_real_catalog_validates_digital_human_templates_and_tts_engines():
    raw_text = (ROOT / "catalog" / "comfyui.json").read_text(encoding="utf-8")
    catalog = ComfyCatalog.model_validate_json(raw_text)

    assert catalog.port == 8188
    assert catalog.proxy_port == 8189
    assert _HEX_40_RE.fullmatch(catalog.commit)

    templates_by_id = {t.id: t for t in catalog.templates}
    assert "digital-human-infinitetalk" in templates_by_id
    assert "digital-human-longcat" in templates_by_id

    for tid, expected_backend in (
        ("digital-human-infinitetalk", "infinitetalk"),
        ("digital-human-longcat", "longcat_avatar_native"),
    ):
        tmpl = templates_by_id[tid]
        assert tmpl.source == "builtin"
        assert tmpl.pipeline is not None
        assert tmpl.pipeline.kind == "digital_human"
        assert tmpl.pipeline.video_backend == expected_backend
        assert tmpl.min_vram_mib > 0
        if expected_backend == "longcat_avatar_native":
            assert tmpl.commercial_ok is False
            assert tmpl.license_notice.strip() != ""
            assert tmpl.custom_nodes == []
            assert tmpl.models == []
            assert tmpl.node_patches == []
            assert tmpl.node_count == 2
            assert set(tmpl.workflow.keys()) == {"1", "2"}
        else:
            assert tmpl.custom_nodes, f"{tid} must pin custom_nodes"
            for node in tmpl.custom_nodes:
                assert _HEX_40_RE.fullmatch(node.commit), f"unpinned commit on {node.name}"
            assert tmpl.models, f"{tid} must pin models"
            for model in tmpl.models:
                assert _HEX_40_RE.fullmatch(model.revision), f"unpinned revision on {model.path}"
                assert _HEX_64_RE.fullmatch(model.sha256), f"invalid sha256 on {model.path}"
                assert model.size > 0
        assert tmpl.fields, f"{tid} must define user-facing fields"
        field_keys = {f.key for f in tmpl.fields}
        for required_key in (
            "1.image",
            "2.audio",
            "pipeline.audio_source",
            "pipeline.tts_engine",
            "pipeline.script_text",
            "pipeline.voice_preset",
            "pipeline.target_resolution",
            "pipeline.upscale_mode",
        ):
            assert required_key in field_keys, f"{tid} missing field {required_key}"
        for patch in tmpl.node_patches:
            assert patch.kind == "wav2vec2_hidden_states_hook"

    engines_by_id = {e.id: e for e in catalog.tts_engines}
    assert {"voxcpm2", "qwen3_tts", "fish_s2_pro", "indextts2"}.issubset(engines_by_id)

    for commercial_id in ("voxcpm2", "qwen3_tts"):
        eng = engines_by_id[commercial_id]
        assert eng.commercial_ok is True
        assert _HEX_40_RE.fullmatch(eng.revision)

    assert engines_by_id["qwen3_tts"].aux_repo
    assert _HEX_40_RE.fullmatch(engines_by_id["qwen3_tts"].aux_revision)

    for non_commercial_id in ("fish_s2_pro", "indextts2"):
        eng = engines_by_id[non_commercial_id]
        assert eng.commercial_ok is False
        assert eng.license_notice.strip() != ""
        assert _HEX_40_RE.fullmatch(eng.revision)

    assert engines_by_id["fish_s2_pro"].supports_inline_tags is True
    assert engines_by_id["indextts2"].supports_emotion_ref is True


def test_schema_negative_validation_for_new_models():
    # 1. ComfyNodePatch
    valid_patch = ComfyNodePatch(
        node_name="ComfyUI-WanVideoWrapper", kind="wav2vec2_hidden_states_hook"
    )
    assert valid_patch.node_name == "ComfyUI-WanVideoWrapper"
    for bad_patch in (
        {"node_name": "ComfyUI-WanVideoWrapper", "kind": "arbitrary_exec"},
        {"node_name": "../escape", "kind": "wav2vec2_hidden_states_hook"},
        {"node_name": "sub/dir", "kind": "wav2vec2_hidden_states_hook"},
        {"node_name": "   ", "kind": "wav2vec2_hidden_states_hook"},
        {"node_name": "bad:name", "kind": "wav2vec2_hidden_states_hook"},
        {
            "node_name": "ComfyUI-WanVideoWrapper",
            "kind": "wav2vec2_hidden_states_hook",
            "extra": True,
        },
    ):
        with pytest.raises(ValidationError):
            ComfyNodePatch.model_validate(bad_patch)

    # 2. ComfyTtsVoicePreset & ComfyTtsEngineSpec
    valid_preset = ComfyTtsVoicePreset(
        id="shen_zhiyi",
        name="知性女主播 · 沈知意",
        gender="female",
        style="知性主播",
        description="清澈温润的专业女主播",
        prompt="clear warm female host",
        seed=202601,
    )
    assert valid_preset.id == "shen_zhiyi"
    assert valid_preset.gender == "female"
    assert valid_preset.seed == 202601
    default_preset = ComfyTtsVoicePreset(id="minimal_voice", name="Minimal Voice")
    assert default_preset.gender == "female"
    assert default_preset.seed == 42
    for bad_preset in (
        {"id": "", "name": "Empty ID"},
        {"id": "../evil", "name": "Traversal ID"},
        {"id": "bad id spaces", "name": "Spaces ID"},
        {"id": "a" * 65, "name": "Too Long ID"},
        {"id": "ok_id", "name": ""},
        {"id": "ok_id", "name": "n" * 81},
        {"id": "ok_id", "name": "Bad Gender", "gender": "robot"},
        {"id": "ok_id", "name": "Neg Seed", "seed": -1},
        {"id": "ok_id", "name": "Huge Seed", "seed": 2147483648},
        {"id": "ok_id", "name": "Long Style", "style": "s" * 81},
        {"id": "ok_id", "name": "Long Desc", "description": "d" * 241},
        {"id": "ok_id", "name": "Long Prompt", "prompt": "p" * 601},
        {"id": "ok_id", "name": "Extra Key", "unexpected": True},
    ):
        with pytest.raises(ValidationError):
            ComfyTtsVoicePreset.model_validate(bad_preset)

    base_engine = {
        "id": "demo_tts",
        "name": "Demo TTS",
        "repo": "org/demo-tts",
        "revision": "a" * 40,
    }
    parsed_base_engine = ComfyTtsEngineSpec.model_validate(base_engine)
    assert parsed_base_engine.commercial_ok is True
    assert parsed_base_engine.voice_presets == []
    with_presets = ComfyTtsEngineSpec.model_validate(
        base_engine | {"voice_presets": [valid_preset.model_dump()]}
    )
    assert len(with_presets.voice_presets) == 1
    assert with_presets.voice_presets[0].id == "shen_zhiyi"
    for bad_update in (
        {"id": "../evil"},
        {"id": "bad id with spaces"},
        {"name": "   "},
        {"license": "   "},
        {"repo": "not-a-valid-slug"},
        {"revision": "main"},
        {"revision": "z" * 40},
        {"aux_repo": "org/aux-only"},  # missing aux_revision
        {"aux_repo": "org/aux", "aux_revision": "short"},
        {"sample_rate": 4000},
        {"sample_rate": 192000},
        {"min_vram_mib": -1},
        {"pip_packages": ["--index-url https://evil.invalid"]},
        {"pip_packages": ["git+https://github.com/evil/pkg.git"]},
        {"source_commit": "not-a-sha"},
        {"voice_presets": [{"id": "../bad", "name": "Bad"}]},
        {
            "voice_presets": [
                {"id": f"voice_{idx}", "name": f"Voice {idx}"} for idx in range(25)
            ]
        },
        {"unexpected": 1},
    ):
        with pytest.raises(ValidationError):
            ComfyTtsEngineSpec.model_validate(base_engine | bad_update)

    # Duplicate tts_engine IDs rejected by ComfyCatalog
    with pytest.raises(ValidationError):
        ComfyCatalog.model_validate(
            {
                "repo": "https://github.com/Comfy-Org/ComfyUI.git",
                "tag": "v0.46.0",
                "commit": "a" * 40,
                "tts_engines": [base_engine, base_engine],
            }
        )

    # 3. ComfyPipelineSpec
    assert ComfyPipelineSpec().kind == "comfy_standard"
    assert ComfyPipelineSpec(max_segment_seconds=3.0).max_segment_seconds == pytest.approx(3.0)
    assert ComfyPipelineSpec(max_segment_seconds=3.7).max_segment_seconds == pytest.approx(3.7)
    for bad_pipe in (
        {"kind": "unknown_pipeline"},
        {"video_backend": "unsupported_backend"},
        {"upscale_mode": "ai_8x"},
        {"max_segment_seconds": 2.9},
        {"max_segment_seconds": 301.0},
        {"tail_frame_offset_seconds": -0.01},
        {"tail_frame_offset_seconds": 1.5},
        {"output_fps": 0},
        {"output_fps": 120},
        {"target_width": 32},
        {"target_height": 8192},
        {"audio_node_key": "missing_dot"},
        {"image_node_key": "../bad.image"},
        {"supported_tts_engines": ["../evil"]},
        {"extra_key": "nope"},
    ):
        with pytest.raises(ValidationError):
            ComfyPipelineSpec.model_validate(bad_pipe)

    # 4. WorkflowField.visible_when
    good_field = WorkflowField(
        key="pipeline.script_text",
        label="Script",
        node_id="pipeline",
        input_name="script_text",
        field_type="textarea",
        visible_when={"pipeline.audio_source": "tts", "1.enabled": True},
    )
    assert good_field.visible_when == {"pipeline.audio_source": "tts", "1.enabled": True}
    for bad_vw in (
        {"nodot": "tts"},
        {"../evil.audio_source": "tts"},
        {"pipeline.audio_source": ["tts"]},
    ):
        with pytest.raises(ValidationError):
            WorkflowField(
                key="pipeline.script_text",
                label="Script",
                node_id="pipeline",
                input_name="script_text",
                field_type="textarea",
                visible_when=bad_vw,
            )

    # 5. WorkflowRunRequest.hf_token
    default_req = WorkflowRunRequest(workflow_id="digital-human-infinitetalk")
    assert default_req.hf_token == ""
    token_req = WorkflowRunRequest(
        workflow_id="digital-human-infinitetalk",
        hf_token="hf_super_secret_token_value",
    )
    assert token_req.hf_token == "hf_super_secret_token_value"
    assert "hf_super_secret_token_value" not in repr(token_req)
    with pytest.raises(ValidationError):
        WorkflowRunRequest(
            workflow_id="digital-human-infinitetalk",
            hf_token="x" * 513,
        )


# ---------------------------------------------------------------------------
# 2. Workflow Pack Manager (server/comfy_packs.py)
# ---------------------------------------------------------------------------


def _make_mock_runtime(tmp_path: Path, total_mib: int = 81920):
    rt = Mock()
    rt.root = tmp_path / "remote"
    rt.root.mkdir(parents=True, exist_ok=True)
    rt.models_dir = rt.root / "models"
    rt.models_dir.mkdir(parents=True, exist_ok=True)
    rt.stopping = threading.Event()
    rt.gpu_info = Mock(
        return_value={"name": "NVIDIA A100-SXM4-80GB", "total_mib": total_mib, "used_mib": 1024}
    )
    return rt


def _make_pack_template(
    tid: str,
    *,
    min_vram_mib: int = 24000,
    min_disk_mib: int = 0,
    nodes: list[dict] | None = None,
    models: list[dict] | None = None,
    patches: list[dict] | None = None,
    pip_packages: list[str] | None = None,
) -> dict:
    return {
        "id": tid,
        "name": f"Pack {tid}",
        "source": "builtin",
        "min_vram_mib": min_vram_mib,
        "min_disk_mib": min_disk_mib,
        "custom_nodes": nodes or [],
        "node_patches": patches or [],
        "pip_packages": pip_packages or [],
        "models": models or [],
        "workflow": sample_digital_human_workflow(),
    }


def _invoke_mock_download_writer(payload_map: dict[str, bytes], calls: list[dict]):
    """Flexible mock download_fn compatible with per-model or URL-based signatures."""

    def _download(*args, **kwargs):
        calls.append({"args": args, "kwargs": kwargs})
        target_path: Path | None = None
        key_hint = ""

        # Extract destination Path and model identifier from positional or keyword args.
        for k in ("dest", "target", "target_path", "partial", "partial_path", "out_path"):
            if k in kwargs and isinstance(kwargs[k], (Path, str)):
                target_path = Path(kwargs[k])
                break
        if "model" in kwargs and isinstance(kwargs["model"], dict):
            key_hint = PurePosixPath(str(kwargs["model"].get("path", ""))).name
        elif "model_ref" in kwargs and isinstance(kwargs["model_ref"], dict):
            key_hint = PurePosixPath(str(kwargs["model_ref"].get("path", ""))).name

        for a in args:
            if isinstance(a, dict) and "path" in a and not key_hint:
                key_hint = PurePosixPath(str(a["path"])).name
            elif isinstance(a, Path) and target_path is None:
                target_path = a
            elif isinstance(a, str) and not key_hint and "/" in a:
                key_hint = PurePosixPath(a.split("?", 1)[0]).name

        assert target_path is not None, f"download_fn received no Path destination: {args} {kwargs}"
        if not key_hint:
            key_hint = target_path.name.removesuffix(".part")

        data = payload_map.get(key_hint)
        if data is None:
            data = payload_map.get(target_path.name.removesuffix(".part"))
        if data is None and len(payload_map) == 1:
            data = next(iter(payload_map.values()))
        assert data is not None, f"No mock payload configured for {key_hint!r}"

        target_path.parent.mkdir(parents=True, exist_ok=True)
        target_path.write_bytes(data)
        return target_path

    return _download


def test_pack_manager_preflight_vram_disk_and_cached_models(tmp_path, monkeypatch):
    from server.comfy_packs import ColabOneComfyPackManager

    rt = _make_mock_runtime(tmp_path, total_mib=15360)
    install_dir = rt.root / "comfyui"
    models_dir = rt.root / "comfyui-models"
    install_dir.mkdir(parents=True, exist_ok=True)
    models_dir.mkdir(parents=True, exist_ok=True)

    mgr = ColabOneComfyPackManager(rt, install_dir, models_dir)

    model_bytes = b"M" * (4 * 1024 * 1024)  # 4 MiB synthetic model
    model_sha = hashlib.sha256(model_bytes).hexdigest()
    model_ref = {
        "repo": "Kijai/WanVideo_comfy",
        "revision": "a" * 40,
        "path": "I2V/demo_weight.safetensors",
        "target_dir": "diffusion_models",
        "size": len(model_bytes),
        "sha256": model_sha,
    }
    wf = _make_pack_template(
        "wf-preflight",
        min_vram_mib=24000,
        min_disk_mib=1024,
        models=[model_ref],
    )

    # 1. Insufficient VRAM rejected with clear error.
    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(200 * 1024**3, 10 * 1024**3, 190 * 1024**3),
    )
    with pytest.raises(RuntimeError):
        mgr.check_preflight(wf)

    # 2. Sufficient VRAM, but insufficient disk space rejected.
    rt.gpu_info.return_value = {"name": "NVIDIA A100", "total_mib": 81920, "used_mib": 1000}
    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(10 * 1024**3, 9 * 1024**3, 256 * 1024**2),  # 256 MiB free
    )
    with pytest.raises(RuntimeError):
        mgr.check_preflight(wf)

    # 3. Adequate VRAM and disk passes.
    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(200 * 1024**3, 20 * 1024**3, 180 * 1024**3),
    )
    mgr.check_preflight(wf)

    # 4. Cached models reduce remaining bytes needed.
    assert mgr.models_total_bytes([model_ref]) == len(model_bytes)
    assert mgr.models_cached_bytes([model_ref]) == 0

    target = mgr.model_target_path(model_ref)
    assert target == models_dir / "diffusion_models" / "demo_weight.safetensors"
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_bytes(model_bytes)
    atomic_json(target.parent / "verified.json", {target.name: model_sha})

    assert mgr.models_cached_bytes([model_ref]) == len(model_bytes)

    # With a large model already cached on disk, disk space below total model size
    # (but above remaining uncached bytes + safety headroom) must pass preflight.
    huge_model = {
        "repo": "Kijai/WanVideo_comfy",
        "revision": "b" * 40,
        "path": "I2V/huge_cached.safetensors",
        "target_dir": "diffusion_models",
        "size": 30 * 1024**3,  # 30 GiB nominal size
        "sha256": "c" * 64,
    }
    wf_cached = _make_pack_template(
        "wf-cached-big",
        min_vram_mib=24000,
        min_disk_mib=0,
        models=[huge_model],
    )
    monkeypatch.setattr(mgr, "models_cached_bytes", lambda _ms: 30 * 1024**3)
    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(50 * 1024**3, 40 * 1024**3, 10 * 1024**3),  # 10 GiB free < 30 GiB total
    )
    mgr.check_preflight(wf_cached)


def test_pack_manager_isolated_checkout_symlink_swap_and_preserves_user_nodes(
    tmp_path, monkeypatch
):
    from server.comfy_packs import ColabOneComfyPackManager

    rt = _make_mock_runtime(tmp_path, total_mib=81920)
    install_dir = rt.root / "comfyui"
    models_dir = rt.root / "comfyui-models"
    custom_nodes_dir = install_dir / "custom_nodes"
    custom_nodes_dir.mkdir(parents=True, exist_ok=True)
    models_dir.mkdir(parents=True, exist_ok=True)

    # User-placed custom node (real directory, NOT managed by packs).
    user_node_dir = custom_nodes_dir / "UserOwnCustomNode"
    user_node_dir.mkdir()
    user_marker = user_node_dir / "user_code.py"
    user_marker.write_text("# user preserved\n", encoding="utf-8")

    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(200 * 1024**3, 10 * 1024**3, 190 * 1024**3),
    )

    recorded_cmds: list[list[str]] = []

    def fake_run_cmd(*args, **kwargs):
        cmd = [str(x) for x in args[0]]
        recorded_cmds.append(cmd)
        if "git" in Path(cmd[0]).name:
            if "init" in cmd:
                idx = cmd.index("init")
                target = Path(cmd[idx + 1])
                target.mkdir(parents=True, exist_ok=True)
                (target / ".git").mkdir(exist_ok=True)
            elif "clone" in cmd:
                target = Path(cmd[-1])
                target.mkdir(parents=True, exist_ok=True)
                (target / ".git").mkdir(exist_ok=True)
                (target / "__init__.py").write_text("# cloned\n", encoding="utf-8")
            elif "-C" in cmd:
                target = Path(cmd[cmd.index("-C") + 1])
                target.mkdir(parents=True, exist_ok=True)
                (target / ".git").mkdir(exist_ok=True)
                if "checkout" in cmd:
                    (target / "__init__.py").write_text("# checked out\n", encoding="utf-8")
        return ""

    mgr = ColabOneComfyPackManager(
        rt, install_dir, models_dir, run_cmd_fn=fake_run_cmd
    )

    pack_a = _make_pack_template(
        "pack-alpha",
        min_vram_mib=16000,
        nodes=[
            {
                "name": "NodeAlphaOne",
                "repo": "https://github.com/example/NodeAlphaOne.git",
                "commit": "1" * 40,
                "install_requirements": True,
            },
            {
                "name": "NodeShared",
                "repo": "https://github.com/example/NodeShared.git",
                "commit": "2" * 40,
                "install_requirements": False,
            },
        ],
    )
    pack_b = _make_pack_template(
        "pack-beta",
        min_vram_mib=16000,
        nodes=[
            {
                "name": "NodeBetaOnly",
                "repo": "https://github.com/example/NodeBetaOnly.git",
                "commit": "3" * 40,
                "install_requirements": False,
            },
        ],
    )

    task1 = ColabOneTask("comfyui_run", "pack-alpha")
    needs_restart_1 = mgr.ensure_pack(task1, pack_a)
    assert needs_restart_1 is True
    assert mgr.active_pack_id

    # Verify .pack-complete.json was written inside the isolated pack directory.
    complete_markers = list(rt.root.rglob(".pack-complete.json"))
    assert len(complete_markers) >= 1
    assert json.loads(complete_markers[0].read_text(encoding="utf-8"))

    # Active pack nodes are symlinks in comfyui/custom_nodes/.
    assert (custom_nodes_dir / "NodeAlphaOne").is_symlink()
    assert (custom_nodes_dir / "NodeShared").is_symlink()
    assert not (custom_nodes_dir / "NodeBetaOnly").exists()
    # User-placed node untouched.
    assert user_node_dir.is_dir() and not user_node_dir.is_symlink()
    assert user_marker.read_text(encoding="utf-8") == "# user preserved\n"

    # Second run of the SAME pack must NOT require ComfyUI restart.
    task2 = ColabOneTask("comfyui_run", "pack-alpha")
    needs_restart_2 = mgr.ensure_pack(task2, pack_a)
    assert needs_restart_2 is False

    # Switching to pack_b swaps symlinks cleanly and returns needs_restart=True.
    task3 = ColabOneTask("comfyui_run", "pack-beta")
    needs_restart_3 = mgr.ensure_pack(task3, pack_b)
    assert needs_restart_3 is True
    assert (custom_nodes_dir / "NodeBetaOnly").is_symlink()
    assert not (custom_nodes_dir / "NodeAlphaOne").exists()
    assert not (custom_nodes_dir / "NodeShared").exists()
    # User-placed node is STILL completely untouched!
    assert user_node_dir.is_dir() and not user_node_dir.is_symlink()
    assert user_marker.read_text(encoding="utf-8") == "# user preserved\n"


def test_pack_manager_apply_node_patches_wav2vec2_idempotent(tmp_path):
    from server.comfy_packs import ColabOneComfyPackManager

    rt = _make_mock_runtime(tmp_path)
    mgr = ColabOneComfyPackManager(rt, rt.root / "comfyui", rt.root / "comfyui-models")

    pack_dir = tmp_path / "pack_fixture"
    nodes_py = pack_dir / "ComfyUI-WanVideoWrapper" / "multitalk" / "nodes.py"
    nodes_py.parent.mkdir(parents=True, exist_ok=True)

    # Realistic WanVideoWrapper multitalk/nodes.py snippet calling wav2vec2 with output_hidden_states.
    original_src = (
        "import torch\n\n"
        "class MultiTalkWav2VecEmbeds:\n"
        "    def process(self, wav2vec_model, input_values):\n"
        "        embeddings = wav2vec_model(\n"
        "            input_values,\n"
        "            seq_len=100,\n"
        "            output_hidden_states=True,\n"
        "        )\n"
        "        return embeddings.hidden_states\n"
    )
    nodes_py.write_text(original_src, encoding="utf-8")

    patches = [
        {
            "node_name": "ComfyUI-WanVideoWrapper",
            "kind": "wav2vec2_hidden_states_hook",
        }
    ]
    task = ColabOneTask("comfyui_run", "digital-human-infinitetalk")

    applied_first = mgr.apply_node_patches(pack_dir, patches, task=task)
    patched_once = nodes_py.read_text(encoding="utf-8")
    assert isinstance(applied_first, list)
    assert patched_once != original_src
    # Patched file MUST remain syntactically valid Python.
    ast.parse(patched_once)

    # Applying the same patch a second time must be 100% idempotent (no duplicate injection).
    mgr.apply_node_patches(pack_dir, patches, task=task)
    patched_twice = nodes_py.read_text(encoding="utf-8")
    assert patched_twice == patched_once
    assert patched_twice.count("COLABONE_WAV2VEC2_HOOK_PATCH") == 1


def test_pack_manager_model_download_sha256_verify_cache_skip_and_mismatch_reject(
    tmp_path, monkeypatch
):
    from server.comfy_packs import ColabOneComfyPackManager

    rt = _make_mock_runtime(tmp_path, total_mib=81920)
    install_dir = rt.root / "comfyui"
    models_dir = rt.root / "comfyui-models"
    install_dir.mkdir(parents=True, exist_ok=True)
    models_dir.mkdir(parents=True, exist_ok=True)

    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(200 * 1024**3, 10 * 1024**3, 190 * 1024**3),
    )

    good_bytes = b"verified-safetensors-weights-payload-" * 32
    good_sha = hashlib.sha256(good_bytes).hexdigest()
    good_model = {
        "repo": "Phips/2xNomosUni_span_multijpg",
        "revision": "ed6111c4cabdaf42b75e6535600d5370cce9e4e2",
        "path": "2xNomosUni_span_multijpg.safetensors",
        "target_dir": "upscale_models",
        "size": len(good_bytes),
        "sha256": good_sha,
    }
    wf_good = _make_pack_template(
        "wf-model-good",
        min_vram_mib=8000,
        models=[good_model],
    )

    calls: list[dict] = []
    mgr = ColabOneComfyPackManager(
        rt,
        install_dir,
        models_dir,
        run_cmd_fn=lambda *a, **k: "",
        download_fn=_invoke_mock_download_writer(
            {"2xNomosUni_span_multijpg.safetensors": good_bytes}, calls
        ),
    )

    # First run downloads and writes verified.json.
    task1 = ColabOneTask("comfyui_run", "wf-model-good")
    mgr.ensure_pack(task1, wf_good, hf_token="hf_test_token")
    assert len(calls) == 1
    target_path = mgr.model_target_path(good_model)
    assert target_path.is_file()
    assert target_path.read_bytes() == good_bytes
    verified_json = target_path.parent / "verified.json"
    assert verified_json.is_file()
    verified_data = json.loads(verified_json.read_text(encoding="utf-8"))
    assert verified_data.get(target_path.name) == good_sha

    # Second run skips re-downloading already verified file.
    task2 = ColabOneTask("comfyui_run", "wf-model-good")
    mgr.ensure_pack(task2, wf_good)
    assert len(calls) == 1

    # SHA-256 mismatch must be rejected and NEVER recorded in verified.json.
    bad_expected_sha = hashlib.sha256(b"expected-clean-weights").hexdigest()
    corrupted_bytes = b"corrupted-tampered-bytes"
    bad_model = {
        "repo": "Kijai/wav2vec2_safetensors",
        "revision": "87847d3bc53702afda44078249e7c33e867827c4",
        "path": "bad_corrupt.safetensors",
        "target_dir": "wav2vec2",
        "size": len(corrupted_bytes),
        "sha256": bad_expected_sha,
    }
    wf_bad = _make_pack_template(
        "wf-model-bad",
        min_vram_mib=8000,
        models=[bad_model],
    )
    bad_calls: list[dict] = []
    mgr_bad = ColabOneComfyPackManager(
        rt,
        install_dir,
        models_dir,
        run_cmd_fn=lambda *a, **k: "",
        download_fn=_invoke_mock_download_writer(
            {"bad_corrupt.safetensors": corrupted_bytes}, bad_calls
        ),
    )
    task3 = ColabOneTask("comfyui_run", "wf-model-bad")
    with pytest.raises((RuntimeError, ValueError)):
        mgr_bad.ensure_pack(task3, wf_bad)
    bad_verified_path = models_dir / "wav2vec2" / "verified.json"
    if bad_verified_path.is_file():
        assert "bad_corrupt.safetensors" not in json.loads(
            bad_verified_path.read_text(encoding="utf-8")
        )


# ---------------------------------------------------------------------------
# 3. ComfyUI WebSocket Progress (server/comfy_ws.py)
# ---------------------------------------------------------------------------


def _unmask_rfc6455_client_frame(frame: bytes) -> tuple[int, bytes]:
    assert len(frame) >= 6
    b0, b1 = frame[0], frame[1]
    opcode = b0 & 0x0F
    masked = bool(b1 & 0x80)
    assert masked, "RFC 6455 client frames MUST be masked"
    length = b1 & 0x7F
    pos = 2
    if length == 126:
        length = struct.unpack("!H", frame[pos : pos + 2])[0]
        pos += 2
    elif length == 127:
        length = struct.unpack("!Q", frame[pos : pos + 8])[0]
        pos += 8
    mask_key = frame[pos : pos + 4]
    pos += 4
    masked_payload = frame[pos : pos + length]
    assert len(masked_payload) == length
    unmasked = bytes(b ^ mask_key[i % 4] for i, b in enumerate(masked_payload))
    return opcode, unmasked


def test_ws_rfc6455_frame_encode_and_decode_roundtrip():
    from server.comfy_ws import decode_ws_server_frame, encode_ws_client_frame

    short_payload = b'{"type":"status","data":{}}'
    medium_payload = ("A" * 300).encode("utf-8")  # 16-bit extended length (126..65535)

    for payload in (short_payload, medium_payload):
        encoded_client = encode_ws_client_frame(payload)
        opcode, unmasked = _unmask_rfc6455_client_frame(encoded_client)
        assert opcode == 0x1
        assert unmasked == payload

        # Roundtrip client masked frame through decoder as well.
        client_buf = bytearray(encoded_client)
        assert decode_ws_server_frame(client_buf) == (0x1, payload)
        assert len(client_buf) == 0

        # Incomplete server frame must return None without consuming bytes.
        server_frame = _make_server_ws_frame(payload, opcode=0x1)
        partial_buf = bytearray(server_frame[:-1])
        before_len = len(partial_buf)
        assert decode_ws_server_frame(partial_buf) is None
        assert len(partial_buf) == before_len

        # Complete server frame decodes (opcode, payload) and drains buffer in place.
        partial_buf.extend(server_frame[-1:])
        decoded = decode_ws_server_frame(partial_buf)
        assert decoded == (0x1, payload)
        assert len(partial_buf) == 0


def test_parse_comfy_ws_message_all_event_types():
    from server.comfy_ws import parse_comfy_ws_message

    prompt_id = "prompt-dh-001"
    node_titles = {"7": "视频采样器", "10": "VAE 解码"}

    # 1. execution_start
    ev_start = parse_comfy_ws_message(
        json.dumps({"type": "execution_start", "data": {"prompt_id": prompt_id}}),
        prompt_id,
        node_titles,
    )
    assert ev_start is not None
    assert ev_start.get("phase") or ev_start.get("type")

    # 2. executing with node title lookup
    ev_exec = parse_comfy_ws_message(
        json.dumps(
            {"type": "executing", "data": {"prompt_id": prompt_id, "node": "7"}}
        ),
        prompt_id,
        node_titles,
    )
    assert ev_exec is not None
    assert ev_exec.get("node_id") == "7"
    assert ev_exec.get("node_title") == "视频采样器"

    # 3. progress (step / max_steps / fraction)
    ev_prog = parse_comfy_ws_message(
        json.dumps(
            {
                "type": "progress",
                "data": {"prompt_id": prompt_id, "node": "7", "value": 3, "max": 6},
            }
        ),
        prompt_id,
        node_titles,
    )
    assert ev_prog is not None
    assert ev_prog.get("step") == 3
    assert ev_prog.get("max_steps") == 6
    assert ev_prog.get("fraction") == pytest.approx(0.5, abs=0.01)

    # 4. progress_state
    ev_pstate = parse_comfy_ws_message(
        json.dumps(
            {
                "type": "progress_state",
                "data": {
                    "prompt_id": prompt_id,
                    "nodes": {
                        "7": {
                            "value": 4,
                            "max": 8,
                            "state": "running",
                            "node_id": "7",
                        }
                    },
                },
            }
        ),
        prompt_id,
        node_titles,
    )
    assert ev_pstate is not None
    assert ev_pstate.get("step") == 4
    assert ev_pstate.get("max_steps") == 8
    assert ev_pstate.get("fraction") == pytest.approx(0.5, abs=0.01)

    # 5. execution_error
    ev_err = parse_comfy_ws_message(
        json.dumps(
            {
                "type": "execution_error",
                "data": {
                    "prompt_id": prompt_id,
                    "node_id": "7",
                    "node_type": "WanVideoSampler",
                    "exception_message": "CUDA OOM",
                },
            }
        ),
        prompt_id,
        node_titles,
    )
    assert ev_err is not None
    assert "error" in str(ev_err.get("phase", "")).lower() or ev_err.get("error")

    # 6. executing(node=None) marks completion
    ev_done = parse_comfy_ws_message(
        json.dumps(
            {"type": "executing", "data": {"prompt_id": prompt_id, "node": None}}
        ),
        prompt_id,
        node_titles,
    )
    assert ev_done is not None
    assert (
        ev_done.get("phase") == "done"
        or ev_done.get("done") is True
        or ev_done.get("node_id") in (None, "")
    )

    # 7. Unrelated prompt_id and invalid JSON return None cleanly
    assert (
        parse_comfy_ws_message(
            json.dumps(
                {"type": "executing", "data": {"prompt_id": "other-prompt", "node": "7"}}
            ),
            prompt_id,
            node_titles,
        )
        is None
    )
    assert parse_comfy_ws_message("{not valid json", prompt_id, node_titles) is None


class _MockLoopbackComfyWsServer:
    """Real localhost RFC 6455 WebSocket server for offline monitor tests."""

    def __init__(self, messages: list[dict]):
        self.messages = messages
        self.sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.sock.bind(("127.0.0.1", 0))
        self.port = self.sock.getsockname()[1]
        self.sock.listen(4)
        self.sock.settimeout(0.2)
        self.stop_event = threading.Event()
        self.handshakes: list[dict] = []
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
            head = bytes(buf[: buf.index(b"\r\n\r\n")]).decode("latin-1")
            lines = head.split("\r\n")
            headers: dict[str, str] = {}
            for line in lines[1:]:
                k, _, v = line.partition(":")
                headers[k.strip().lower()] = v.strip()
            ws_key = headers.get("sec-websocket-key", "")
            accept = base64.b64encode(
                hashlib.sha1((ws_key + _WS_GUID).encode("ascii")).digest()
            ).decode("ascii")
            self.handshakes.append({"request_line": lines[0], "headers": headers})
            conn.sendall(
                (
                    "HTTP/1.1 101 Switching Protocols\r\n"
                    "Upgrade: websocket\r\n"
                    "Connection: Upgrade\r\n"
                    f"Sec-WebSocket-Accept: {accept}\r\n\r\n"
                ).encode("latin-1")
            )
            for msg in self.messages:
                payload = json.dumps(msg).encode("utf-8")
                conn.sendall(_make_server_ws_frame(payload, opcode=0x1))
                time.sleep(0.01)
            # Send clean close frame (opcode 0x8).
            conn.sendall(_make_server_ws_frame(b"", opcode=0x8))

    def close(self):
        self.stop_event.set()
        try:
            self.sock.close()
        except OSError:
            pass
        self.thread.join(timeout=3.0)


def test_comfy_ws_monitor_loopback_live_and_unreachable_fallback():
    from server.comfy_ws import ColabOneComfyWsMonitor

    prompt_id = "prompt-live-ws-1"
    messages = [
        {"type": "execution_start", "data": {"prompt_id": prompt_id}},
        {"type": "executing", "data": {"prompt_id": prompt_id, "node": "7"}},
        {
            "type": "progress",
            "data": {"prompt_id": prompt_id, "node": "7", "value": 2, "max": 4},
        },
        {"type": "executing", "data": {"prompt_id": prompt_id, "node": None}},
    ]
    srv = _MockLoopbackComfyWsServer(messages)
    progress_events: list[dict] = []
    done_event = threading.Event()
    errors: list[str] = []

    try:
        kwargs = {
            "host": "127.0.0.1",
            "port": srv.port,
            "client_id": "colabone-test-ws",
            "node_titles": {"7": "视频采样器"},
            "on_progress": lambda ev: progress_events.append(ev),
            "on_error": lambda err: errors.append(str(err)),
            "on_done": lambda *_a, **_k: done_event.set(),
        }
        sig = inspect.signature(ColabOneComfyWsMonitor.__init__)
        if "prompt_id" in sig.parameters:
            kwargs["prompt_id"] = prompt_id
        monitor = ColabOneComfyWsMonitor(**kwargs)
        if hasattr(monitor, "set_prompt_id"):
            monitor.set_prompt_id(prompt_id)
        elif hasattr(monitor, "prompt_id") and not getattr(monitor, "prompt_id", None):
            monitor.prompt_id = prompt_id

        assert monitor.start() is True
        assert done_event.wait(timeout=3.0), f"on_done never fired; got {progress_events}"
        monitor.stop()

        assert len(srv.handshakes) == 1
        assert "clientId=colabone-test-ws" in srv.handshakes[0]["request_line"]
        assert errors == []
        assert any(
            ev.get("node_id") == "7" and ev.get("node_title") == "视频采样器"
            for ev in progress_events
        )
        assert any(
            ev.get("step") == 2 and ev.get("max_steps") == 4 for ev in progress_events
        )
    finally:
        srv.close()

    # Unreachable port returns False gracefully so caller can fall back to HTTP polling.
    tmp_sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    tmp_sock.bind(("127.0.0.1", 0))
    closed_port = tmp_sock.getsockname()[1]
    tmp_sock.close()

    unreachable = ColabOneComfyWsMonitor(
        host="127.0.0.1",
        port=closed_port,
        client_id="colabone-unreachable",
        connect_timeout=0.5,
    )
    assert unreachable.start() is False
    unreachable.stop()


# ---------------------------------------------------------------------------
# 4. Remote Multi-Engine TTS Runner (server/tts_engines.py)
# ---------------------------------------------------------------------------


def test_split_colabone_tts_script_preserves_inline_tags():
    from server.tts_engines import split_colabone_tts_script

    script = (
        "大家好，欢迎来到今天的直播间！今天我们要聊一聊如何用单张人像生成高清长视频。[laugh] "
        "首先我们来准备一段清晰自然的人声配音，确保每个句子停顿自然、吐字清楚。\n\n"
        "第二段内容更加精彩！[sigh] 即使文案长达几分钟，我们也会自动在静音谷切分并接力生成，完全不会跳帧。"
    )
    chunks = split_colabone_tts_script(script, max_chars=55)
    assert len(chunks) >= 3
    for chunk in chunks:
        assert chunk.strip() != ""
        # Inline bracketed tags must NEVER be split across chunk boundaries.
        assert chunk.count("[") == chunk.count("]"), f"Broken inline tag in chunk: {chunk!r}"
    joined = "".join(chunks)
    assert "[laugh]" in joined
    assert "[sigh]" in joined
    assert "欢迎来到今天的直播间" in joined
    assert "完全不会跳帧" in joined


def test_tts_trim_normalize_and_assemble_master_wav_and_manifest(tmp_path):
    from server.tts_engines import (
        assemble_colabone_tts_master,
        normalize_audio_loudness,
        trim_wav_silence,
    )

    sr = 24000
    # 0.5s leading silence + 0.6s sine speech + 0.5s trailing silence
    raw_samples = (
        make_silence_samples(0.5, sr)
        + make_sine_samples(0.6, freq_hz=440.0, amp=0.35, sample_rate=sr)
        + make_silence_samples(0.5, sr)
    )
    trimmed = trim_wav_silence(raw_samples, sr, threshold_db=-42.0, pad_ms=35.0)
    assert len(trimmed) < len(raw_samples)
    # Should keep ~0.6s speech + <= ~0.07s pads (well under original 1.6s).
    assert int(0.55 * sr) <= len(trimmed) <= int(0.85 * sr)

    # Loudness normalization boosts quiet signal without clipping.
    quiet = make_sine_samples(0.5, freq_hz=300.0, amp=0.02, sample_rate=sr)
    normed = normalize_audio_loudness(quiet, sr, target_lufs=-23.0)
    assert len(normed) == len(quiet)
    rms_before = math.sqrt(sum(x * x for x in quiet) / len(quiet))
    rms_after = math.sqrt(sum(float(x) * float(x) for x in normed) / len(normed))
    assert rms_after > rms_before
    assert max(abs(float(x)) for x in normed) <= 1.0001

    # Assemble 3 synthetic WAV segments into a 48kHz PCM16 master WAV + manifest.json.
    seg_wavs: list[tuple[str, Path]] = []
    for idx, text in enumerate(("第一句测试文案。", "第二句测试文案。", "第三句收尾文案。"), 1):
        seg_path = tmp_path / f"seg_{idx}.wav"
        seg_audio = (
            make_silence_samples(0.15, sr)
            + make_sine_samples(0.8, freq_hz=320.0 + idx * 60.0, amp=0.3, sample_rate=sr)
            + make_silence_samples(0.15, sr)
        )
        write_pcm16_wav(seg_path, seg_audio, sample_rate=sr)
        seg_wavs.append((text, seg_path))

    master_wav = tmp_path / "master.wav"
    manifest_path = tmp_path / "manifest.json"
    manifest = assemble_colabone_tts_master(
        seg_wavs,
        master_wav,
        manifest_path,
        pause_seconds=0.30,
        target_sr=48000,
        target_lufs=-23.0,
    )

    assert master_wav.is_file()
    assert manifest_path.is_file()
    channels, sampwidth, out_sr, pcm = read_pcm16_wav(master_wav)
    assert channels == 1
    assert sampwidth == 2
    assert out_sr == 48000
    wav_duration = len(pcm) / out_sr
    assert wav_duration > 2.0

    disk_manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    assert disk_manifest == manifest
    valleys = manifest.get("silence_valleys")
    assert isinstance(valleys, list) and len(valleys) >= 2
    assert all(0.0 < float(t) < wav_duration for t in valleys)
    manifest_dur = float(manifest.get("duration") or manifest.get("total_duration") or 0.0)
    assert manifest_dur == pytest.approx(wav_duration, abs=0.15)


def test_remote_tts_runner_all_four_engines_and_worker_script_ast(tmp_path):
    from server import tts_engines
    from server.tts_engines import ColabOneRemoteTtsRunner

    rt = _make_mock_runtime(tmp_path, total_mib=81920)
    engines = sample_tts_engines_by_id()

    def fake_synth(*args, **kwargs):
        """Flexible mock synth_fn that produces a valid WAV file for any segment or batch."""
        out_path: Path | None = None
        sr = 24000
        for k in ("out_wav", "out_path", "output_path", "output_wav", "wav_path"):
            if k in kwargs and kwargs[k] is not None:
                out_path = Path(kwargs[k])
                break
        if "engine" in kwargs and isinstance(kwargs["engine"], dict):
            sr = int(kwargs["engine"].get("sample_rate") or sr)
        for a in args:
            if isinstance(a, dict) and "sample_rate" in a:
                sr = int(a["sample_rate"])
            elif isinstance(a, Path) and out_path is None:
                out_path = a

        # Handle batch synth_fn(engine, segments, work_dir, ...) vs per-segment synth_fn(..., out_path)
        if out_path is not None and out_path.suffix.lower() == ".wav":
            samples = make_sine_samples(0.6, freq_hz=400.0, amp=0.35, sample_rate=sr)
            write_pcm16_wav(out_path, samples, sample_rate=sr)
            return out_path

        # If given a directory and list of segments, write one WAV per segment.
        work_dir = out_path or Path(kwargs.get("work_dir", tmp_path))
        segments = kwargs.get("segments")
        if segments is None:
            for a in args:
                if isinstance(a, list):
                    segments = a
                    break
        results: list[tuple[str, Path]] = []
        for idx, text in enumerate(segments or ["测试语音"], 1):
            wav_p = work_dir / f"mock_seg_{idx:03d}.wav"
            samples = make_sine_samples(0.6, freq_hz=350.0 + idx * 40.0, amp=0.35, sample_rate=sr)
            write_pcm16_wav(wav_p, samples, sample_rate=sr)
            results.append((str(text), wav_p))
        return results

    runner = ColabOneRemoteTtsRunner(rt, engines, synth_fn=fake_synth)
    listed = runner.list_engines()
    assert {e["id"] for e in listed} == {"voxcpm2", "qwen3_tts", "fish_s2_pro", "indextts2"}

    # Locate build_worker_script (either module-level helper or runner method).
    build_script_fn = getattr(tts_engines, "build_worker_script", None) or getattr(
        runner, "build_worker_script", None
    )
    assert callable(build_script_fn), "build_worker_script must exist in server.tts_engines"

    for engine_id in ("voxcpm2", "qwen3_tts", "fish_s2_pro", "indextts2"):
        spec = runner.get_engine(engine_id)
        assert spec["id"] == engine_id

        # Verify build_worker_script generates syntactically valid Python for every engine.
        sig = inspect.signature(build_script_fn)
        if len(sig.parameters) == 1:
            param_name = next(iter(sig.parameters))
            arg = spec if "spec" in param_name or "engine" == param_name else engine_id
            try:
                script_src = build_script_fn(arg)
            except TypeError:
                script_src = build_script_fn(engine_id if arg is spec else spec)
        else:
            script_src = build_script_fn(engine_id)
        assert isinstance(script_src, str) and len(script_src) > 40
        ast.parse(script_src)

        # Verify synthesize behavior for all 4 engines.
        task = ColabOneTask("comfyui_run", f"tts-{engine_id}")
        work_dir = tmp_path / f"tts_work_{engine_id}"
        ref_audio_path: Path | None = None
        if engine_id == "indextts2":
            # IndexTTS2 MUST reject missing reference audio before invoking synth_fn.
            with pytest.raises(
                ValueError,
                match=r"IndexTTS2.*需要上传音色参考音频才能进行语音合成",
            ):
                runner.synthesize(
                    task,
                    engine_id=engine_id,
                    script_text="你好，这是一段离线语音合成测试。第二句话用于检查静音谷切分。",
                    work_dir=work_dir,
                    pause_seconds=0.28,
                )
            ref_audio_path = write_pcm16_wav(
                tmp_path / "indextts2_ref_voice.wav",
                make_sine_samples(0.5, freq_hz=300.0, amp=0.35, sample_rate=24000),
                sample_rate=24000,
            )

        res = runner.synthesize(
            task,
            engine_id=engine_id,
            script_text="你好，这是一段离线语音合成测试。第二句话用于检查静音谷切分。",
            work_dir=work_dir,
            ref_audio_path=ref_audio_path,
            pause_seconds=0.28,
        )
        assert Path(res["audio_path"]).is_file()
        assert Path(res["manifest_path"]).is_file()
        assert res["engine_id"] == engine_id
        assert res["duration"] > 0
        assert res["sample_rate"] == 48000
        assert isinstance(res["manifest"], dict)

        # Non-commercial engines MUST log their license notice in task.logs.
        log_text = "\n".join(entry["message"] for entry in task.logs)
        if not spec["commercial_ok"]:
            assert (
                "非商业" in log_text
                or "非商用" in log_text
                or spec["license_notice"][:10] in log_text
            ), f"Expected non-commercial license notice in logs for {engine_id}, got: {log_text}"


# ---------------------------------------------------------------------------
# 5. Long-Video Digital Human Runner (server/digital_human.py)
# ---------------------------------------------------------------------------


def test_digital_human_resolution_and_base_dimensions():
    from server.digital_human import compute_base_dimensions, parse_target_resolution

    assert parse_target_resolution("1280x720") == (1280, 720)
    assert parse_target_resolution("720x1280") == (720, 1280)
    assert parse_target_resolution("1920x1080") == (1920, 1080)
    assert parse_target_resolution("1080x1920") == (1080, 1920)
    assert parse_target_resolution("invalid", default=(1280, 720)) == (1280, 720)

    # InfiniteTalk: landscape 832x480, portrait 480x832
    assert compute_base_dimensions("infinitetalk", 1280, 720) == (832, 480)
    assert compute_base_dimensions("infinitetalk", 1920, 1080) == (832, 480)
    assert compute_base_dimensions("infinitetalk", 720, 1280) == (480, 832)
    assert compute_base_dimensions("infinitetalk", 1080, 1920) == (480, 832)

    # LongCat-Avatar: defaults to official 480p (832x480 / 480x832) for fast diffusion + AI 2x upscale,
    # and switches to native 720p (1280x768 / 768x1280) when render_quality="native_720p" (with <720p falling back to 480p).
    assert compute_base_dimensions("longcat_avatar", 1280, 720) == (832, 480)
    assert compute_base_dimensions("longcat_avatar", 1920, 1080) == (832, 480)
    assert compute_base_dimensions("longcat_avatar", 720, 1280) == (480, 832)
    assert compute_base_dimensions("longcat_avatar", 1080, 1920) == (480, 832)
    assert compute_base_dimensions("longcat_avatar", 832, 480) == (832, 480)
    assert compute_base_dimensions("longcat_avatar", 480, 832) == (480, 832)

    assert compute_base_dimensions(
        "longcat_avatar", 1280, 720, render_quality="balanced_480p_ai2x"
    ) == (832, 480)
    assert compute_base_dimensions(
        "longcat_avatar", 720, 1280, render_quality="balanced_480p_ai2x"
    ) == (480, 832)

    assert compute_base_dimensions(
        "longcat_avatar", 1280, 720, render_quality="native_720p"
    ) == (1280, 768)
    assert compute_base_dimensions(
        "longcat_avatar", 1920, 1080, render_quality="native_720p"
    ) == (1280, 768)
    assert compute_base_dimensions(
        "longcat_avatar", 720, 1280, render_quality="native_720p"
    ) == (768, 1280)
    assert compute_base_dimensions(
        "longcat_avatar", 1080, 1920, render_quality="native_720p"
    ) == (768, 1280)
    assert compute_base_dimensions(
        "longcat_avatar", 832, 480, render_quality="native_720p"
    ) == (832, 480)
    assert compute_base_dimensions(
        "longcat_avatar", 480, 832, render_quality="native_720p"
    ) == (480, 832)


def test_detect_pcm_silence_valleys_and_plan_audio_segments(tmp_path):
    from server.digital_human import detect_pcm_silence_valleys, plan_audio_segments

    sr = 16000
    # Speech [0..1.4], silence [1.4..1.8] (midpoint ~1.6),
    # speech [1.8..3.3], silence [3.3..3.7] (midpoint ~3.5), speech [3.7..5.0]
    samples = (
        make_sine_samples(1.4, freq_hz=440.0, amp=0.5, sample_rate=sr)
        + make_silence_samples(0.4, sr)
        + make_sine_samples(1.5, freq_hz=520.0, amp=0.5, sample_rate=sr)
        + make_silence_samples(0.4, sr)
        + make_sine_samples(1.3, freq_hz=390.0, amp=0.5, sample_rate=sr)
    )
    wav_path = write_pcm16_wav(tmp_path / "valleys.wav", samples, sample_rate=sr)

    valleys = detect_pcm_silence_valleys(wav_path, min_silence_sec=0.18, threshold_db=-36.0)
    assert len(valleys) >= 2
    assert any(abs(v - 1.6) <= 0.15 for v in valleys), f"Missing valley near 1.6s: {valleys}"
    assert any(abs(v - 3.5) <= 0.15 for v in valleys), f"Missing valley near 3.5s: {valleys}"

    # 1. Audio <= 45s remains a single segment.
    single = plan_audio_segments(38.5, max_segment_sec=45.0)
    assert len(single) == 1
    assert single[0][0] == pytest.approx(0.0)
    assert single[0][1] == pytest.approx(38.5)

    # 2. Audio > 45s splits cleanly at manifest.json silence_valleys.
    manifest = {"silence_valleys": [18.0, 41.2, 65.5, 83.4, 101.0]}
    segs_manifest = plan_audio_segments(110.0, max_segment_sec=45.0, manifest=manifest)
    assert len(segs_manifest) == 3
    assert segs_manifest[0] == pytest.approx((0.0, 41.2), abs=0.05)
    assert segs_manifest[1] == pytest.approx((41.2, 83.4), abs=0.05)
    assert segs_manifest[2] == pytest.approx((83.4, 110.0), abs=0.05)
    for start, end in segs_manifest:
        assert 0.0 < end - start <= 45.0 + 1e-6

    # 3. Audio > 45s splits cleanly at raw detected silence_valleys when no manifest is given.
    raw_valleys = [22.0, 42.5, 79.0, 86.0]
    segs_raw = plan_audio_segments(98.0, max_segment_sec=45.0, silence_valleys=raw_valleys)
    assert len(segs_raw) == 3
    assert segs_raw[0][0] == pytest.approx(0.0)
    assert segs_raw[0][1] == pytest.approx(42.5, abs=0.05)
    assert segs_raw[1][0] == pytest.approx(42.5, abs=0.05)
    assert segs_raw[1][1] == pytest.approx(86.0, abs=0.05)
    assert segs_raw[2] == pytest.approx((86.0, 98.0), abs=0.05)


def test_inject_ai_upscale_nodes():
    from server.digital_human import inject_ai_upscale_nodes

    original = sample_digital_human_workflow()
    snapshot_before = json.dumps(original, sort_keys=True)

    updated = inject_ai_upscale_nodes(
        original,
        upscale_model_name="2xNomosUni_span_multijpg.safetensors",
        video_combine_node_id="11",
        target_w=1920,
        target_h=1080,
    )

    # Original workflow must not be mutated.
    assert json.dumps(original, sort_keys=True) == snapshot_before

    by_class = {
        node["class_type"]: (nid, node) for nid, node in updated.items()
    }
    assert "UpscaleModelLoader" in by_class
    assert "ImageUpscaleWithModel" in by_class
    assert "ImageScale" not in by_class
    assert "colabone_upscale_fit" not in updated

    loader_id, loader_node = by_class["UpscaleModelLoader"]
    up_id, up_node = by_class["ImageUpscaleWithModel"]

    assert loader_id == "colabone_upscale_loader"
    assert up_id == "colabone_upscale_apply"
    assert loader_node["inputs"]["model_name"] == "2xNomosUni_span_multijpg.safetensors"
    assert up_node["inputs"]["upscale_model"] == ["colabone_upscale_loader", 0]
    assert up_node["inputs"]["image"] == ["10", 0]
    assert updated["11"]["inputs"]["images"] == ["colabone_upscale_apply", 0]


def test_digital_human_runner_e2e_tts_multisegment_and_audio_mode(
    runtime, tmp_path, monkeypatch
):
    from server.digital_human import ColabOneDigitalHumanRunner
    from server.tts_engines import ColabOneRemoteTtsRunner

    monkeypatch.setattr(
        runtime,
        "gpu_info",
        lambda: {"name": "NVIDIA A100", "total_mib": 81920, "used_mib": 1024},
    )
    monkeypatch.setattr("server.comfyui.time.sleep", lambda _: None)
    monkeypatch.setattr("server.digital_human.time.sleep", lambda _: None, raising=False)

    # Mark ComfyUI server as already running with a mock process.
    comfy_proc = Mock()
    comfy_proc.poll.return_value = None
    comfy_proc.pid = None
    runtime.comfy.proc = comfy_proc
    runtime.comfy.state = "ready"

    # Mock pack manager so no real git/download runs in e2e test.
    if hasattr(runtime.comfy, "packs"):
        monkeypatch.setattr(runtime.comfy.packs, "check_preflight", lambda _wf: None)
        monkeypatch.setattr(runtime.comfy.packs, "ensure_pack", lambda *a, **k: False)

    # Mock HTTP interactions with ComfyUI (/upload/image, /prompt, /history, /view, /free, /queue).
    submitted_prompts: list[dict] = []
    uploaded_names: list[str] = []
    fake_segment_mp4 = b"\x00\x00\x00\x18ftypmp42fake-segment-video-bytes"

    def fake_post(url, **kwargs):
        if url.endswith("/upload/image"):
            files = kwargs.get("files") or {}
            fname = files["image"][0] if "image" in files else "uploaded.bin"
            uploaded_names.append(fname)
            return make_http_resp({"name": fname, "subfolder": ""})
        if url.endswith("/prompt"):
            idx = len(submitted_prompts) + 1
            submitted_prompts.append(copy.deepcopy(kwargs["json"]["prompt"]))
            return make_http_resp({"prompt_id": f"dh-prompt-{idx}"})
        if url.endswith("/free"):
            return make_http_resp({"ok": True})
        if url.endswith("/interrupt"):
            return make_http_resp({"ok": True})
        raise AssertionError(f"Unexpected POST {url}")

    def fake_get(url, **kwargs):
        if url.endswith("/system_stats"):
            return make_http_resp({"system": {"comfyui_version": "0.46.0"}})
        if url.endswith("/queue"):
            return make_http_resp({"queue_running": [], "queue_pending": []})
        if "/history/" in url:
            pid = url.rsplit("/", 1)[-1]
            return make_http_resp(
                {
                    pid: {
                        "status": {"status_str": "success"},
                        "outputs": {
                            "11": {
                                "gifs": [
                                    {
                                        "filename": f"{pid}_seg.mp4",
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
            return make_http_resp(content=fake_segment_mp4)
        raise AssertionError(f"Unexpected GET {url}")

    runtime.http = Mock()
    runtime.http.post.side_effect = fake_post
    runtime.http.get.side_effect = fake_get

    # Mock ffmpeg/ffprobe commands via run_cmd_fn: create valid output files on disk.
    ffmpeg_calls: list[list[str]] = []
    tail_png = make_png_bytes("red", size=(64, 64))

    def fake_ffmpeg_cmd(*args, **kwargs):
        cmd = [str(x) for x in args[0]]
        ffmpeg_calls.append(cmd)
        out_file = Path(cmd[-1])
        out_file.parent.mkdir(parents=True, exist_ok=True)
        suffix = out_file.suffix.lower()
        if suffix == ".wav":
            # Write a short valid PCM16 WAV slice.
            write_pcm16_wav(
                out_file, make_sine_samples(1.0, freq_hz=440.0, amp=0.3, sample_rate=16000), 16000
            )
        elif suffix in (".png", ".jpg", ".jpeg"):
            out_file.write_bytes(tail_png)
        elif suffix == ".mp4":
            out_file.write_bytes(b"\x00\x00\x00\x18ftypmp42final-muxed-digital-human-mp4")
        else:
            out_file.write_bytes(b"mock-output")
        return ""

    # Use real ColabOneRemoteTtsRunner with injected synth_fn producing >45s audio across 2 segments.
    synth_calls: list[list[str]] = []

    def fake_dh_synth(engine, segments, segments_dir):
        synth_calls.append(list(segments))
        sr = 48000
        # Build a fast 1-second 440Hz sine PCM16 block and repeat 26x per segment (>45s total).
        one_sec = b"".join(
            struct.pack("<h", int(10000 * math.sin(2.0 * math.pi * 440.0 * i / sr)))
            for i in range(sr)
        )
        per_seg_sec = max(26, int(math.ceil(52.0 / max(1, len(segments)))))
        results: list[tuple[str, Path]] = []
        for idx, seg_text in enumerate(segments):
            wav_path = Path(segments_dir) / f"dh_tts_seg_{idx:03d}.wav"
            with wave.open(str(wav_path), "wb") as wf_out:
                wf_out.setnchannels(1)
                wf_out.setsampwidth(2)
                wf_out.setframerate(sr)
                wf_out.writeframes(one_sec * per_seg_sec)
            results.append((seg_text, wav_path))
        return results

    tts_runner = ColabOneRemoteTtsRunner(
        runtime, sample_tts_engines_by_id(), synth_fn=fake_dh_synth
    )

    runner = ColabOneDigitalHumanRunner(
        runtime.comfy, tts_runner, run_cmd_fn=fake_ffmpeg_cmd
    )

    wf_template = {
        "id": "digital-human-infinitetalk",
        "name": "长视频数字人 · InfiniteTalk",
        "source": "builtin",
        "min_vram_mib": 24000,
        "pipeline": {
            "kind": "digital_human",
            "video_backend": "infinitetalk",
            "default_tts_engine": "voxcpm2",
            "supported_tts_engines": ["voxcpm2", "qwen3_tts", "fish_s2_pro", "indextts2"],
            "max_segment_seconds": 45.0,
            "tail_frame_offset_seconds": 0.08,
            "output_fps": 25,
            "target_width": 1280,
            "target_height": 720,
            "upscale_mode": "ai_2x",
            "audio_node_key": "2.audio",
            "image_node_key": "1.image",
            "prompt_node_key": "5.positive_prompt",
            "frames_node_key": "7.frame_window_size",
            "width_node_key": "7.width",
            "height_node_key": "7.height",
            "fps_node_key": "7.fps",
            "steps_node_key": "7.steps",
            "seed_node_key": "7.seed",
        },
        "models": [
            {
                "repo": "Phips/2xNomosUni_span_multijpg",
                "revision": "ed6111c4cabdaf42b75e6535600d5370cce9e4e2",
                "path": "2xNomosUni_span_multijpg.safetensors",
                "target_dir": "upscale_models",
                "size": 4461056,
                "sha256": "bee2a9c082f2b8f6e7f5db504b36593c24a1a959511f587114c399ca58b9c92c",
            }
        ],
        "workflow": sample_digital_human_workflow(),
    }

    portrait_path = tmp_path / "portrait.png"
    portrait_path.write_bytes(make_png_bytes("green", size=(64, 64)))

    # --- Case A: "tts" mode with >45s audio triggers multi-segment relay (>= 2 prompts) ---
    tts_req = WorkflowRunRequest(
        workflow_id="digital-human-infinitetalk",
        overrides={
            "pipeline.audio_source": "tts",
            "pipeline.tts_engine": "voxcpm2",
            "pipeline.script_text": (
                "大家好，这是第一段长视频数字人播报测试文案，用来验证远端配音自动分段与静音谷衔接。"
                "\n\n"
                "现在进入第二段播报内容，系统将在自然停顿处无缝接力尾帧，保持口型与画面连贯稳定。"
            ),
            "pipeline.target_resolution": "1280x720",
            "pipeline.upscale_mode": "ai_2x",
            "pipeline.max_segment_seconds": 45,
            "5.positive_prompt": "studio lighting, friendly speaker",
        },
        inputs={"1.image": "a" * 32},
    )
    task_tts = ColabOneTask("comfyui_run", "digital-human-infinitetalk")
    tts_outputs = runner.run(
        task_tts,
        wf_template,
        tts_req,
        [("1.image", "portrait.png", portrait_path)],
    )

    assert len(synth_calls) == 1
    assert len(synth_calls[0]) >= 2
    assert len(tts_outputs) >= 1
    final_tts = tts_outputs[-1]
    assert final_tts["extension"] == "mp4"
    assert final_tts["kind"] == "video"
    assert final_tts["mime"] == "video/mp4"
    assert final_tts["size"] > 0
    # 62s > 45s MUST have submitted at least 2 relayed segments to ComfyUI!
    assert len(submitted_prompts) >= 2
    assert any("UpscaleModelLoader" == n["class_type"] for n in submitted_prompts[0].values())
    assert any("ImageUpscaleWithModel" == n["class_type"] for n in submitted_prompts[0].values())
    assert not any("ImageScale" == n["class_type"] for n in submitted_prompts[0].values())
    assert "colabone_upscale_fit" not in submitted_prompts[0]
    assert submitted_prompts[0]["11"]["inputs"]["images"] == ["colabone_upscale_apply", 0]
    # Segment 2 must relay via extracted tail frame and advance seed deterministically.
    assert submitted_prompts[1]["1"]["inputs"]["image"] != submitted_prompts[0]["1"]["inputs"]["image"]
    assert submitted_prompts[1]["7"]["inputs"]["seed"] == submitted_prompts[0]["7"]["inputs"]["seed"] + 1
    assert len(ffmpeg_calls) >= 2
    assert "总耗时" in task_tts.message
    assert any("渲染完成（耗时" in entry["message"] for entry in task_tts.logs)

    # --- Case B: "audio" mode skips TTS synthesis and produces final MP4 ---
    submitted_prompts.clear()
    tts_called = {"called": False}

    def fail_if_tts_called(*args, **kwargs):
        tts_called["called"] = True
        raise AssertionError("TTS synthesize must NOT be called in audio mode")

    monkeypatch.setattr(tts_runner, "synthesize", fail_if_tts_called)

    audio_path = tmp_path / "uploaded_voice.wav"
    write_pcm16_wav(
        audio_path,
        make_sine_samples(4.0, freq_hz=330.0, amp=0.4, sample_rate=16000),
        sample_rate=16000,
    )
    audio_req = WorkflowRunRequest(
        workflow_id="digital-human-infinitetalk",
        overrides={
            "pipeline.audio_source": "audio",
            "pipeline.target_resolution": "720x1280",
            "pipeline.upscale_mode": "none",
            "5.positive_prompt": "portrait vertical shot",
        },
        inputs={"1.image": "a" * 32, "2.audio": "b" * 32},
    )
    task_audio = ColabOneTask("comfyui_run", "digital-human-infinitetalk")
    audio_outputs = runner.run(
        task_audio,
        wf_template,
        audio_req,
        [
            ("1.image", "portrait.png", portrait_path),
            ("2.audio", "uploaded_voice.wav", audio_path),
        ],
    )

    assert tts_called["called"] is False
    assert len(submitted_prompts) == 1
    assert not any("UpscaleModelLoader" == n["class_type"] for n in submitted_prompts[0].values())
    assert len(audio_outputs) >= 1
    final_audio = audio_outputs[-1]
    assert final_audio["extension"] == "mp4"
    assert final_audio["kind"] == "video"
    assert final_audio["size"] > 0


# ---------------------------------------------------------------------------
# 6. Audit Hardening Regression Tests
# ---------------------------------------------------------------------------


def test_longcat_avatar_15_catalog_and_pipeline_frame_cap(
    runtime, tmp_path, monkeypatch
):
    from server.comfy_packs import compute_pack_content_signature
    from server.digital_human import ColabOneDigitalHumanRunner, LONGCAT_MAX_FRAMES

    raw_text = (ROOT / "catalog" / "comfyui.json").read_text(encoding="utf-8")
    catalog = ComfyCatalog.model_validate_json(raw_text)
    longcat = next(t for t in catalog.templates if t.id == "digital-human-longcat")
    infinitetalk = next(
        t for t in catalog.templates if t.id == "digital-human-infinitetalk"
    )
    tts_by_id = {e.id: e for e in catalog.tts_engines}

    # Verify upgraded LongCat-Video-Avatar 1.6 native flagship catalog contract
    # alongside InfiniteTalk ComfyUI catalog contract.
    canonical_gesture_prompt = (
        "Static medium shot of an engaging presenter speaking warmly and naturally to the camera, "
        "making expressive, natural hand gestures and open-palm movements to emphasize points while talking, "
        "lively facial expressions, subtle eyebrow raises, natural head tilts, shoulder rhythm, "
        "natural blinking, clear lip articulation synced with speech, soft studio lighting, sharp focus, stable background"
    )
    assert infinitetalk.workflow["5"]["inputs"]["positive_prompt"] == canonical_gesture_prompt
    assert infinitetalk.workflow["14"]["inputs"]["colormatch"] == "disabled"
    assert infinitetalk.workflow["15"]["inputs"]["steps"] == 8
    assert infinitetalk.workflow["15"]["inputs"]["shift"] == pytest.approx(8.0)
    assert infinitetalk.commercial_ok is True
    assert infinitetalk.license == "Apache-2.0 / CC-BY-4.0 / GPL-3.0"
    assert infinitetalk.pipeline is not None
    assert infinitetalk.pipeline.default_tts_engine == "moss_tts_v1_5"

    assert longcat.commercial_ok is False
    assert longcat.license == "MIT / Apache-2.0"
    assert longcat.license_notice.strip() != ""
    assert longcat.min_vram_mib == 49152
    assert longcat.min_disk_mib == 79872
    assert longcat.custom_nodes == []
    assert longcat.models == []
    assert longcat.node_patches == []
    assert longcat.node_count == 2
    assert set(longcat.workflow.keys()) == {"1", "2"}
    assert longcat.workflow["1"]["class_type"] == "LoadImage"
    assert longcat.workflow["2"]["class_type"] == "LoadAudio"
    assert longcat.pipeline is not None
    assert longcat.pipeline.video_backend == "longcat_avatar_native"
    assert longcat.pipeline.default_tts_engine == "fish_drama3"
    assert longcat.pipeline.output_fps == 25
    assert longcat.pipeline.max_segment_seconds == pytest.approx(3.72)
    assert longcat.pipeline.upscale_mode == "auto"

    longcat_fields = {f.key: f for f in longcat.fields}
    infinitetalk_fields = {f.key: f for f in infinitetalk.fields}
    assert longcat_fields["pipeline.tts_engine"].default == "fish_drama3"
    assert infinitetalk_fields["pipeline.tts_engine"].default == "moss_tts_v1_5"
    assert longcat_fields["pipeline.quality_preset"].default == "cinema_flagship"
    assert longcat_fields["pipeline.framing_style"].default == "half_body_gestures"
    assert longcat_fields["pipeline.upscale_mode"].default == "auto"
    assert longcat_fields["pipeline.target_resolution"].default == "1920x1080"
    assert infinitetalk_fields["5.positive_prompt"].default == canonical_gesture_prompt
    assert infinitetalk_fields["15.steps"].default == 8
    assert "推荐 8 步" in infinitetalk_fields["15.steps"].description

    expected_female_voice_ids = [
        "shen_zhiyi",
        "lin_xiaoman",
        "su_wanqing",
        "gu_qinglan",
        "lu_mingwei",
    ]
    expected_male_voice_ids = [
        "chen_jingzhou",
        "lin_chenxu",
        "zhou_moyan",
        "gu_zhengyuan",
        "su_hechuan",
    ]
    expected_voice_ids = [*expected_female_voice_ids, *expected_male_voice_ids]
    for tmpl_fields in (longcat_fields, infinitetalk_fields):
        vp_field = tmpl_fields["pipeline.voice_preset"]
        assert vp_field.group == "basic"
        assert vp_field.default == "shen_zhiyi"
        assert vp_field.visible_when == {"pipeline.audio_source": "tts"}
        assert vp_field.options == [*expected_voice_ids, "custom"]

    for eid in (
        "fish_drama3",
        "moss_tts_v1_5",
        "breeze_tts2",
        "ming_3_tts",
        "voxcpm2",
        "qwen3_tts",
        "indextts2",
        "fish_s2_pro",
    ):
        eng = tts_by_id[eid]
        universal = [p for p in eng.voice_presets if not p.speaker_id]
        assert [p.id for p in universal] == expected_voice_ids
        assert [p.gender for p in universal[:5]] == ["female"] * 5
        assert [p.gender for p in universal[5:]] == ["male"] * 5
        assert [p.seed for p in universal] == list(range(202601, 202611))
        assert all(p.name.strip() != "" for p in eng.voice_presets)
        assert all(p.preview_text.strip() != "" for p in eng.voice_presets)
    qwen_official = [p for p in tts_by_id["qwen3_tts"].voice_presets if p.speaker_id]
    assert len(qwen_official) == 9

    assert len(infinitetalk.node_patches) == 1
    assert infinitetalk.node_patches[0].node_name == "ComfyUI-WanVideoWrapper"
    assert infinitetalk.node_patches[0].kind == "wav2vec2_hidden_states_hook"
    assert compute_pack_content_signature(infinitetalk.model_dump())
    assert infinitetalk.workflow["9"]["inputs"]["normalize_loudness"] is False
    assert infinitetalk.pipeline is not None
    assert infinitetalk.pipeline.max_segment_seconds == 20.0
    assert tts_by_id["fish_s2_pro"].min_vram_mib == 20480
    assert tts_by_id["indextts2"].sample_rate == 22050
    assert LONGCAT_MAX_FRAMES == 253

    # Verify digital_human.py enforces LongCat num_frames <= 253 even when caller
    # asks for a much longer max_segment_seconds on a 16-second audio clip.
    monkeypatch.setattr(
        runtime,
        "gpu_info",
        lambda: {"name": "NVIDIA A100", "total_mib": 81920, "used_mib": 1024},
    )
    monkeypatch.setattr("server.comfyui.time.sleep", lambda _: None)
    monkeypatch.setattr("server.digital_human.time.sleep", lambda _: None, raising=False)

    comfy_proc = Mock()
    comfy_proc.poll.return_value = None
    comfy_proc.pid = None
    runtime.comfy.proc = comfy_proc
    runtime.comfy.state = "ready"
    monkeypatch.setattr(runtime.comfy.packs, "check_preflight", lambda _wf: None)
    monkeypatch.setattr(runtime.comfy.packs, "ensure_pack", lambda *a, **k: False)
    if hasattr(runtime.comfy.packs, "ensure_sageattention_wheels"):
        monkeypatch.setattr(
            runtime.comfy.packs,
            "ensure_sageattention_wheels",
            lambda _task, *, gpu_caps=None, **_k: {
                **(gpu_caps or {}),
                "sageattn_available": False,
                "sageattn3_available": False,
                "installed_any": False,
            },
        )

    submitted_prompts: list[dict] = []
    fake_mp4 = b"\x00\x00\x00\x18ftypmp42longcat-seg"

    def fake_post(url, **kwargs):
        if url.endswith("/upload/image"):
            fname = (kwargs.get("files") or {}).get("image", ("uploaded.bin",))[0]
            return make_http_resp({"name": fname, "subfolder": ""})
        if url.endswith("/prompt"):
            idx = len(submitted_prompts) + 1
            submitted_prompts.append(copy.deepcopy(kwargs["json"]["prompt"]))
            return make_http_resp({"prompt_id": f"lc-prompt-{idx}"})
        if url.endswith("/free") or url.endswith("/interrupt"):
            return make_http_resp({"ok": True})
        raise AssertionError(f"Unexpected POST {url}")

    def fake_get(url, **kwargs):
        if url.endswith("/system_stats"):
            return make_http_resp({"system": {"comfyui_version": "0.46.0"}})
        if url.endswith("/queue"):
            return make_http_resp({"queue_running": [], "queue_pending": []})
        if "/history/" in url:
            pid = url.rsplit("/", 1)[-1]
            return make_http_resp(
                {
                    pid: {
                        "status": {"status_str": "success"},
                        "outputs": {
                            "11": {
                                "gifs": [
                                    {
                                        "filename": f"{pid}.mp4",
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
            return make_http_resp(content=fake_mp4)
        raise AssertionError(f"Unexpected GET {url}")

    runtime.http = Mock()
    runtime.http.post.side_effect = fake_post
    runtime.http.get.side_effect = fake_get

    tail_png = make_png_bytes("yellow", size=(64, 64))

    def fake_ffmpeg(*args, **kwargs):
        cmd = [str(x) for x in args[0]]
        out = Path(cmd[-1])
        out.parent.mkdir(parents=True, exist_ok=True)
        if out.suffix.lower() in (".png", ".jpg", ".jpeg"):
            out.write_bytes(tail_png)
        elif out.suffix.lower() == ".mp4":
            out.write_bytes(b"\x00\x00\x00\x18ftypmp42longcat-final")
        return ""

    # Write a 16-second 48kHz mono PCM16 WAV (fast block repeat).
    sr = 48000
    one_sec = b"".join(
        struct.pack("<h", int(10000 * math.sin(2.0 * math.pi * 440.0 * i / sr)))
        for i in range(sr)
    )
    long_wav = tmp_path / "longcat_16s.wav"
    with wave.open(str(long_wav), "wb") as wf_out:
        wf_out.setnchannels(1)
        wf_out.setsampwidth(2)
        wf_out.setframerate(sr)
        wf_out.writeframes(one_sec * 16)

    portrait = tmp_path / "lc_portrait.png"
    portrait.write_bytes(make_png_bytes("cyan", size=(64, 64)))

    runner = ColabOneDigitalHumanRunner(runtime.comfy, None, run_cmd_fn=fake_ffmpeg)
    req = WorkflowRunRequest(
        workflow_id="digital-human-longcat",
        overrides={
            "pipeline.audio_source": "audio",
            "pipeline.max_segment_seconds": 45,  # Must be capped by longcat_avatar
            "pipeline.target_resolution": "1280x720",
            "pipeline.upscale_mode": "none",
        },
        inputs={"1.image": "a" * 32, "2.audio": "b" * 32},
    )
    task = ColabOneTask("comfyui_run", "digital-human-longcat")
    legacy_longcat_template = _sample_longcat_template()
    outputs = runner.run(
        task,
        legacy_longcat_template,
        req,
        [("1.image", "lc_portrait.png", portrait), ("2.audio", "longcat_16s.wav", long_wav)],
    )
    assert len(outputs) == 1
    assert len(submitted_prompts) >= 2
    for prompt in submitted_prompts:
        num_frames = int(prompt["9"]["inputs"]["num_frames"])
        assert 17 <= num_frames <= 253
        assert (num_frames - 1) % 4 == 0
        assert prompt["9"]["inputs"]["fps"] == 25.0


def test_preflight_cached_models_subtracted_from_min_disk_and_cpu_only_fails_closed(
    tmp_path, monkeypatch
):
    from server.comfy_packs import ColabOneComfyPackManager

    rt = _make_mock_runtime(tmp_path, total_mib=0)
    install_dir = rt.root / "comfyui"
    models_dir = rt.root / "comfyui-models"
    install_dir.mkdir(parents=True, exist_ok=True)
    models_dir.mkdir(parents=True, exist_ok=True)
    mgr = ColabOneComfyPackManager(rt, install_dir, models_dir)

    big_model = {
        "repo": "Kijai/WanVideo_comfy",
        "revision": "a" * 40,
        "path": "LongCat/LongCat-Avatar-15_bf16.safetensors",
        "target_dir": "diffusion_models",
        "size": 37 * 1024**3,  # 37 GiB
        "sha256": "b" * 64,
    }
    wf = _make_pack_template(
        "wf-longcat-preflight",
        min_vram_mib=49152,
        min_disk_mib=55 * 1024,  # 55 GiB total required when uncached
        models=[big_model],
    )

    # H2: CPU-only runtime (total_mib == 0 or empty gpu_info) MUST fail closed.
    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(200 * 1024**3, 10 * 1024**3, 190 * 1024**3),
    )
    with pytest.raises(RuntimeError, match="未检测到可用 NVIDIA GPU"):
        mgr.check_preflight(wf)

    rt.gpu_info.return_value = {}
    with pytest.raises(RuntimeError, match="未检测到可用 NVIDIA GPU"):
        mgr.check_preflight(wf)

    # C2: With adequate GPU and 35 GiB free:
    # Run 1 (0 cached) requires 55 GiB -> FAILS.
    rt.gpu_info.return_value = {"name": "NVIDIA A100-SXM4-80GB", "total_mib": 81920, "used_mib": 1024}
    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(100 * 1024**3, 65 * 1024**3, 35 * 1024**3),  # 35 GiB free
    )
    monkeypatch.setattr(mgr, "models_cached_bytes", lambda _ms: 0)
    with pytest.raises(RuntimeError, match="远端磁盘空间不足"):
        mgr.check_preflight(wf)

    # Run 2 (37 GiB already cached): effective_min_disk = 55 - 37 = 18 GiB,
    # remaining_bytes = 0 -> required_disk = max(18 GiB, 4 GiB) = 18 GiB <= 35 GiB -> PASSES!
    monkeypatch.setattr(mgr, "models_cached_bytes", lambda _ms: 37 * 1024**3)
    mgr.check_preflight(wf)

    # Even with 37 GiB cached, dropping below effective 18 GiB headroom (e.g. 17 GiB free) still fails.
    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(100 * 1024**3, 83 * 1024**3, 17 * 1024**3),
    )
    with pytest.raises(RuntimeError, match="远端磁盘空间不足"):
        mgr.check_preflight(wf)


def test_pack_manager_downloads_models_before_activating_and_clears_task_transfer(
    tmp_path, monkeypatch
):
    from server.comfy_packs import ColabOneComfyPackManager

    rt = _make_mock_runtime(tmp_path, total_mib=81920)
    install_dir = rt.root / "comfyui"
    models_dir = rt.root / "comfyui-models"
    (install_dir / "custom_nodes").mkdir(parents=True, exist_ok=True)
    models_dir.mkdir(parents=True, exist_ok=True)

    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(200 * 1024**3, 10 * 1024**3, 190 * 1024**3),
    )

    def fake_git(*args, **kwargs):
        cmd = [str(x) for x in args[0]]
        if "git" in Path(cmd[0]).name:
            if "init" in cmd:
                target = Path(cmd[cmd.index("init") + 1])
                target.mkdir(parents=True, exist_ok=True)
                (target / ".git").mkdir(exist_ok=True)
            elif "-C" in cmd:
                target = Path(cmd[cmd.index("-C") + 1])
                target.mkdir(parents=True, exist_ok=True)
                (target / ".git").mkdir(exist_ok=True)
        return ""

    payload = b"valid-model-weight-bytes-" * 16
    sha = hashlib.sha256(payload).hexdigest()
    model_ref = {
        "repo": "Kijai/WanVideo_comfy",
        "revision": "a" * 40,
        "path": "order_test.safetensors",
        "target_dir": "diffusion_models",
        "size": len(payload),
        "sha256": sha,
    }
    wf = _make_pack_template(
        "wf-order-check",
        min_vram_mib=8000,
        nodes=[
            {
                "name": "OrderedNode",
                "repo": "https://github.com/example/OrderedNode.git",
                "commit": "1" * 40,
                "install_requirements": False,
            }
        ],
        models=[model_ref],
    )

    # 1. Verify _ensure_models failure prevents _activate_custom_nodes from running (C3).
    failing_activate_called = {"called": False}

    def failing_download(*args, **kwargs):
        raise RuntimeError("simulated network transfer failure")

    mgr_fail = ColabOneComfyPackManager(
        rt, install_dir, models_dir, run_cmd_fn=fake_git, download_fn=failing_download
    )
    orig_fail_activate = mgr_fail._activate_custom_nodes

    def spy_fail_activate(**kwargs):
        failing_activate_called["called"] = True
        return orig_fail_activate(**kwargs)

    monkeypatch.setattr(mgr_fail, "_activate_custom_nodes", spy_fail_activate)
    task_fail = ColabOneTask("comfyui_run", "wf-order-check")
    with pytest.raises(RuntimeError, match="simulated network transfer failure"):
        mgr_fail.ensure_pack(task_fail, wf)
    assert failing_activate_called["called"] is False
    assert mgr_fail.active_pack_id == ""
    assert not (install_dir / "custom_nodes" / "OrderedNode").exists()

    # 2. Verify _ensure_models runs BEFORE _activate_custom_nodes, and task.transfer is
    # active during download then cleared to None after _ensure_models finishes (C3 + H5).
    call_order: list[str] = []
    transfer_seen_during_download: list[bool] = []
    dl_calls: list[dict] = []
    base_writer = _invoke_mock_download_writer({"order_test.safetensors": payload}, dl_calls)

    def observing_download(*args, **kwargs):
        t = kwargs.get("task")
        if t is None:
            for a in args:
                if isinstance(a, ColabOneTask):
                    t = a
                    break
        transfer_seen_during_download.append(t is not None and t.transfer is not None)
        return base_writer(*args, **kwargs)

    mgr = ColabOneComfyPackManager(
        rt, install_dir, models_dir, run_cmd_fn=fake_git, download_fn=observing_download
    )
    orig_ensure_models = mgr._ensure_models
    orig_activate = mgr._activate_custom_nodes

    def wrapped_ensure_models(*args, **kwargs):
        call_order.append("ensure_models")
        res = orig_ensure_models(*args, **kwargs)
        # H5: task.transfer MUST already be None immediately upon _ensure_models return.
        assert args[0].transfer is None
        return res

    def wrapped_activate(*args, **kwargs):
        call_order.append("activate_custom_nodes")
        return orig_activate(*args, **kwargs)

    monkeypatch.setattr(mgr, "_ensure_models", wrapped_ensure_models)
    monkeypatch.setattr(mgr, "_activate_custom_nodes", wrapped_activate)

    task_ok = ColabOneTask("comfyui_run", "wf-order-check")
    changed = mgr.ensure_pack(task_ok, wf)
    assert changed is True
    assert call_order == ["ensure_models", "activate_custom_nodes"]
    assert transfer_seen_during_download == [True]
    assert task_ok.transfer is None
    assert (install_dir / "custom_nodes" / "OrderedNode").is_symlink()


def test_wav2vec2_ast_patcher_edge_cases(tmp_path):
    from server.comfy_packs import ColabOneComfyPackManager, _patch_wav2vec2_source

    class _DummyOut:
        def __init__(self, last_hidden_state):
            self.last_hidden_state = last_hidden_state
            self.hidden_states = None

    class _DummyLayer:
        def __init__(self, tag):
            self.tag = tag
            self._hooks = []

        def register_forward_hook(self, fn):
            self._hooks.append(fn)

            class _Handle:
                def remove(_s):
                    if fn in self._hooks:
                        self._hooks.remove(fn)

            return _Handle()

        def forward(self, x):
            out = f"{x}->{self.tag}"
            for fn in list(self._hooks):
                fn(self, (x,), out)
            return out

    class _DummyWav2Vec:
        def __init__(self):
            self.encoder = type("Enc", (), {"layers": [_DummyLayer("L0"), _DummyLayer("L1")]})()

        def __call__(self, x, output_hidden_states=False):
            h = x
            for layer in self.encoder.layers:
                h = layer.forward(h)
            return _DummyOut(h)

    def _exec_patched(src: str):
        ast.parse(src)
        ns: dict = {}
        exec(compile(src, "<patched>", "exec"), ns)
        out = ns["run"](_DummyWav2Vec(), "audio")
        assert out.hidden_states == ("audio", "audio->L0", "audio->L0->L1")

    # Case 1: UTF-8 BOM at start of file (direct helper + apply_node_patches from disk).
    bom_src = (
        "\ufeffimport math\n\n"
        "def run(wav2vec_model, x):\n"
        "    return wav2vec_model(x, output_hidden_states=True)\n"
    )
    patched_bom, changed = _patch_wav2vec2_source(bom_src)
    assert changed is True
    assert not patched_bom.startswith("\ufeff")
    assert "_colabone_wav2vec2_with_hidden_states(wav2vec_model, x, output_hidden_states=True)" in patched_bom
    _exec_patched(patched_bom)
    assert _patch_wav2vec2_source(patched_bom) == (patched_bom, False)

    rt = _make_mock_runtime(tmp_path)
    mgr = ColabOneComfyPackManager(rt, rt.root / "comfyui", rt.root / "comfyui-models")
    pack_dir = tmp_path / "bom_pack"
    nodes_py = pack_dir / "ComfyUI-WanVideoWrapper" / "multitalk" / "nodes.py"
    nodes_py.parent.mkdir(parents=True, exist_ok=True)
    nodes_py.write_bytes(bom_src.encode("utf-8"))
    applied = mgr.apply_node_patches(
        pack_dir,
        [{"node_name": "ComfyUI-WanVideoWrapper", "kind": "wav2vec2_hidden_states_hook"}],
    )
    assert applied == ["ComfyUI-WanVideoWrapper:wav2vec2_hidden_states_hook"]
    _exec_patched(nodes_py.read_text(encoding="utf-8"))

    # Case 2: Comment containing '(' between callee and '('.
    comment_paren_src = (
        "def run(wav2vec_model, x):\n"
        "    return (\n"
        "        wav2vec_model  # stray ( open paren in comment!\n"
        "        (x, output_hidden_states=True)\n"
        "    )\n"
    )
    patched_comment, changed_comment = _patch_wav2vec2_source(comment_paren_src)
    assert changed_comment is True
    assert "_colabone_wav2vec2_with_hidden_states(wav2vec_model, x, output_hidden_states=True)" in patched_comment
    _exec_patched(patched_comment)

    # Case 3: Parenthesized callee `(wav2vec_model)(...)` and compound `(None or wav2vec_model)(...)`.
    paren_callee_src = (
        "def run(wav2vec_model, x):\n"
        "    a = (wav2vec_model)(x, output_hidden_states=True)\n"
        "    b = (None or wav2vec_model)(x, output_hidden_states=True)\n"
        "    assert a.hidden_states == b.hidden_states\n"
        "    return b\n"
    )
    patched_paren, changed_paren = _patch_wav2vec2_source(paren_callee_src)
    assert changed_paren is True
    assert "_colabone_wav2vec2_with_hidden_states((wav2vec_model), x, output_hidden_states=True)" in patched_paren
    assert "_colabone_wav2vec2_with_hidden_states((None or wav2vec_model), x, output_hidden_states=True)" in patched_paren
    _exec_patched(patched_paren)

    # Case 4: PEP 263 coding header on line 2 (preceded by a comment line) MUST stay on line 2.
    pep263_src = (
        "# Copyright (c) 2026 ColabOne\n"
        "# -*- coding: utf-8 -*-\n"
        "def run(wav2vec_model, x):\n"
        "    return wav2vec_model(x, output_hidden_states=True)\n"
    )
    patched_pep, changed_pep = _patch_wav2vec2_source(pep263_src)
    assert changed_pep is True
    pep_lines = patched_pep.splitlines()
    assert pep_lines[0] == "# Copyright (c) 2026 ColabOne"
    assert pep_lines[1] == "# -*- coding: utf-8 -*-"
    assert patched_pep.index("# -*- coding: utf-8 -*-") < patched_pep.index(
        "# COLABONE_WAV2VEC2_HOOK_PATCH"
    )
    ast.parse(patched_pep.encode("utf-8"))
    _exec_patched(patched_pep)


def test_ws_monitor_pre_bind_cross_prompt_buffering_and_error_suppresses_done():
    from server.comfy_ws import ColabOneComfyWsMonitor

    # H8: Messages arriving BEFORE set_prompt_id() are buffered; on set_prompt_id(target),
    # foreign prompt messages and prompt-less progress frames are discarded while target
    # prompt messages replay in exact order.
    pre_bind_msgs = [
        {"type": "execution_start", "data": {"prompt_id": "foreign-prompt"}},
        {"type": "executing", "data": {"prompt_id": "foreign-prompt", "node": "99"}},
        {"type": "progress", "data": {"node": "99", "value": 1, "max": 10}},  # no prompt_id
        {"type": "execution_start", "data": {"prompt_id": "target-prompt"}},
        {"type": "executing", "data": {"prompt_id": "target-prompt", "node": "7"}},
        {
            "type": "progress",
            "data": {"prompt_id": "target-prompt", "node": "7", "value": 3, "max": 6},
        },
        {"type": "executing", "data": {"prompt_id": "foreign-prompt", "node": None}},
    ]
    srv1 = _MockLoopbackComfyWsServer(pre_bind_msgs)
    progress_events: list[dict] = []
    done_events: list[dict] = []
    errors: list[str] = []
    try:
        monitor = ColabOneComfyWsMonitor(
            host="127.0.0.1",
            port=srv1.port,
            client_id="colabone-h8",
            node_titles={"7": "视频采样器", "99": "其他任务节点"},
            on_progress=lambda ev: progress_events.append(ev),
            on_error=lambda err: errors.append(str(err)),
            on_done=lambda ev: done_events.append(ev),
        )
        assert monitor.start() is True
        # Wait for all pre-bind messages to arrive in the pending buffer.
        deadline = time.monotonic() + 3.0
        while time.monotonic() < deadline:
            with monitor._lock:
                if len(monitor._pending_buffer) >= len(pre_bind_msgs):
                    break
            time.sleep(0.01)
        assert progress_events == []
        assert done_events == []
        assert errors == []

        monitor.set_prompt_id("target-prompt")
        monitor.stop()

        assert errors == []
        assert done_events == []
        assert [ev["phase"] for ev in progress_events] == [
            "starting",
            "executing",
            "sampling",
        ]
        assert progress_events[1]["node_id"] == "7"
        assert progress_events[1]["node_title"] == "视频采样器"
        assert progress_events[2]["step"] == 3
        assert progress_events[2]["max_steps"] == 6
    finally:
        srv1.close()

    # M4: executing(node=None) and execution_success arriving AFTER execution_error
    # MUST NOT overwrite _last_event or fire on_done.
    err_then_done_msgs = [
        {"type": "execution_start", "data": {"prompt_id": "err-prompt"}},
        {"type": "executing", "data": {"prompt_id": "err-prompt", "node": "7"}},
        {
            "type": "execution_error",
            "data": {
                "prompt_id": "err-prompt",
                "node_id": "7",
                "node_type": "WanVideoSampler",
                "exception_message": "CUDA OOM",
            },
        },
        {"type": "executing", "data": {"prompt_id": "err-prompt", "node": None}},
        {"type": "execution_success", "data": {"prompt_id": "err-prompt"}},
    ]
    srv2 = _MockLoopbackComfyWsServer(err_then_done_msgs)
    err_progress: list[dict] = []
    err_done: list[dict] = []
    err_msgs: list[str] = []
    try:
        mon2 = ColabOneComfyWsMonitor(
            host="127.0.0.1",
            port=srv2.port,
            client_id="colabone-m4",
            prompt_id="err-prompt",
            node_titles={"7": "视频采样器"},
            on_progress=lambda ev: err_progress.append(ev),
            on_error=lambda msg: err_msgs.append(str(msg)),
            on_done=lambda ev: err_done.append(ev),
        )
        assert mon2.start() is True
        deadline = time.monotonic() + 3.0
        while mon2.connected and time.monotonic() < deadline:
            time.sleep(0.01)
        mon2.stop()

        assert len(err_msgs) == 1 and "CUDA OOM" in err_msgs[0]
        assert err_done == []
        assert mon2.last_event is not None
        assert mon2.last_event["phase"] == "error"
        assert "CUDA OOM" in str(mon2.last_event.get("error", ""))
        assert "CUDA OOM" in mon2.error_message
    finally:
        srv2.close()


def test_digital_human_concat_outpoint_upscale_aspect_cycles_native_none_and_preflight_fail_fast(
    runtime, tmp_path, monkeypatch
):
    from server.digital_human import (
        ColabOneDigitalHumanRunner,
        inject_ai_upscale_nodes,
    )
    from server.tts_engines import ColabOneRemoteTtsRunner

    # --- 1. C2/R2: inject_ai_upscale_nodes wires UpscaleModelLoader -> ImageUpscaleWithModel
    # directly into VHS_VideoCombine (no ImageScale/colabone_upscale_fit), prevents graph cycles
    # on re-injection, and cleanly strips legacy colabone_upscale_fit nodes ---
    base_wf = sample_digital_human_workflow()
    injected_once = inject_ai_upscale_nodes(
        base_wf,
        upscale_model_name="2xNomosUni_span_multijpg.safetensors",
        video_combine_node_id="11",
        target_w=1280,
        target_h=720,
        base_w=832,
        base_h=480,
    )
    assert "colabone_upscale_fit" not in injected_once
    assert not any(
        n.get("class_type") == "ImageScale" for n in injected_once.values()
    )
    assert (
        injected_once["colabone_upscale_loader"]["inputs"]["model_name"]
        == "2xNomosUni_span_multijpg.safetensors"
    )
    assert injected_once["colabone_upscale_apply"]["inputs"]["upscale_model"] == [
        "colabone_upscale_loader",
        0,
    ]
    assert injected_once["colabone_upscale_apply"]["inputs"]["image"] == ["10", 0]
    assert injected_once["11"]["inputs"]["images"] == ["colabone_upscale_apply", 0]

    # Re-injecting on the already-upscaled graph updates in place without creating a cycle.
    injected_twice = inject_ai_upscale_nodes(
        injected_once,
        upscale_model_name="other_2x.safetensors",
        video_combine_node_id="11",
        target_w=1920,
        target_h=1080,
        base_w=832,
        base_h=480,
    )
    assert "colabone_upscale_fit" not in injected_twice
    assert (
        injected_twice["colabone_upscale_loader"]["inputs"]["model_name"]
        == "other_2x.safetensors"
    )
    assert injected_twice["colabone_upscale_apply"]["inputs"]["image"] == ["10", 0]
    assert injected_twice["11"]["inputs"]["images"] == ["colabone_upscale_apply", 0]

    # Even if an existing graph had a legacy colabone_upscale_fit or cyclic link on
    # colabone_upscale_apply, re-injection updates parameters safely and removes fit.
    corrupted = copy.deepcopy(injected_once)
    corrupted["colabone_upscale_fit"] = {
        "class_type": "ImageScale",
        "inputs": {"image": ["colabone_upscale_apply", 0], "width": 1280, "height": 720},
    }
    corrupted["colabone_upscale_apply"]["inputs"]["image"] = ["colabone_upscale_fit", 0]
    corrupted["11"]["inputs"]["images"] = ["colabone_upscale_fit", 0]
    repaired = inject_ai_upscale_nodes(
        corrupted,
        upscale_model_name="safe_2x.safetensors",
        video_combine_node_id="11",
        target_w=1280,
        target_h=720,
        base_w=832,
        base_h=480,
    )
    assert "colabone_upscale_fit" not in repaired
    assert (
        repaired["colabone_upscale_loader"]["inputs"]["model_name"]
        == "safe_2x.safetensors"
    )
    assert repaired["11"]["inputs"]["images"] == ["colabone_upscale_apply", 0]

    # --- 2. C1/R2 + upscale_mode="none": decoded frame-exact -filter_complex_script concat
    # trims overlap frames cleanly, uses relative seg_000.mp4 + -protocol_whitelist file,pipe
    # (no -safe 0), and upscale_mode="none" preserves native (base_w, base_h) ---
    monkeypatch.setattr(
        runtime,
        "gpu_info",
        lambda: {"name": "NVIDIA A100", "total_mib": 81920, "used_mib": 1024},
    )
    monkeypatch.setattr("server.comfyui.time.sleep", lambda _: None)
    monkeypatch.setattr("server.digital_human.time.sleep", lambda _: None, raising=False)

    comfy_proc = Mock()
    comfy_proc.poll.return_value = None
    comfy_proc.pid = None
    runtime.comfy.proc = comfy_proc
    runtime.comfy.state = "ready"
    monkeypatch.setattr(runtime.comfy.packs, "check_preflight", lambda _wf: None)
    monkeypatch.setattr(runtime.comfy.packs, "ensure_pack", lambda *a, **k: False)

    submitted_prompts: list[dict] = []
    captured_filter_scripts: list[str] = []
    final_ffmpeg_cmds: list[list[str]] = []
    fake_seg_mp4 = b"\x00\x00\x00\x18ftypmp42seg-bytes"
    tail_png = make_png_bytes("magenta", size=(64, 64))

    def fake_post(url, **kwargs):
        if url.endswith("/upload/image"):
            fname = (kwargs.get("files") or {}).get("image", ("uploaded.bin",))[0]
            return make_http_resp({"name": fname, "subfolder": ""})
        if url.endswith("/prompt"):
            idx = len(submitted_prompts) + 1
            submitted_prompts.append(copy.deepcopy(kwargs["json"]["prompt"]))
            return make_http_resp({"prompt_id": f"c1-prompt-{idx}"})
        if url.endswith("/free") or url.endswith("/interrupt"):
            return make_http_resp({"ok": True})
        raise AssertionError(f"Unexpected POST {url}")

    def fake_get(url, **kwargs):
        if url.endswith("/system_stats"):
            return make_http_resp({"system": {"comfyui_version": "0.46.0"}})
        if url.endswith("/queue"):
            return make_http_resp({"queue_running": [], "queue_pending": []})
        if "/history/" in url:
            pid = url.rsplit("/", 1)[-1]
            return make_http_resp(
                {
                    pid: {
                        "status": {"status_str": "success"},
                        "outputs": {
                            "11": {
                                "gifs": [
                                    {
                                        "filename": f"{pid}.mp4",
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
            return make_http_resp(content=fake_seg_mp4)
        raise AssertionError(f"Unexpected GET {url}")

    runtime.http = Mock()
    runtime.http.post.side_effect = fake_post
    runtime.http.get.side_effect = fake_get

    def spy_ffmpeg(*args, **kwargs):
        cmd = [str(x) for x in args[0]]
        for idx, token in enumerate(cmd):
            if token == "-filter_complex_script" and idx + 1 < len(cmd):
                captured_filter_scripts.append(
                    Path(cmd[idx + 1]).read_text(encoding="utf-8")
                )
                final_ffmpeg_cmds.append(cmd)
        out = Path(cmd[-1])
        out.parent.mkdir(parents=True, exist_ok=True)
        if out.suffix.lower() in (".png", ".jpg", ".jpeg"):
            out.write_bytes(tail_png)
        elif out.suffix.lower() == ".mp4":
            out.write_bytes(b"\x00\x00\x00\x18ftypmp42final-mp4")
        return ""

    wf_template = {
        "id": "digital-human-infinitetalk",
        "name": "长视频数字人 · InfiniteTalk",
        "source": "builtin",
        "min_vram_mib": 40960,
        "pipeline": {
            "kind": "digital_human",
            "video_backend": "infinitetalk",
            "default_tts_engine": "voxcpm2",
            "supported_tts_engines": ["voxcpm2", "qwen3_tts", "fish_s2_pro", "indextts2"],
            "max_segment_seconds": 10.0,
            "tail_frame_offset_seconds": 0.08,
            "output_fps": 25,
            "target_width": 1280,
            "target_height": 720,
            "upscale_mode": "ai_2x",
            "audio_node_key": "2.audio",
            "image_node_key": "1.image",
            "prompt_node_key": "5.positive_prompt",
            "frames_node_key": "7.frame_window_size",
            "width_node_key": "7.width",
            "height_node_key": "7.height",
            "fps_node_key": "7.fps",
            "steps_node_key": "7.steps",
            "seed_node_key": "7.seed",
        },
        "workflow": sample_digital_human_workflow(),
    }

    # Build a 22-second 48kHz mono PCM16 WAV so max_segment_seconds=10 splits into 3 segments.
    sr = 48000
    one_sec = b"".join(
        struct.pack("<h", int(10000 * math.sin(2.0 * math.pi * 440.0 * i / sr)))
        for i in range(sr)
    )
    wav_22s = tmp_path / "voice_22s.wav"
    with wave.open(str(wav_22s), "wb") as wf_out:
        wf_out.setnchannels(1)
        wf_out.setsampwidth(2)
        wf_out.setframerate(sr)
        wf_out.writeframes(one_sec * 22)

    portrait = tmp_path / "portrait_native.png"
    portrait.write_bytes(make_png_bytes("green", size=(64, 64)))

    tts_runner = ColabOneRemoteTtsRunner(runtime, sample_tts_engines_by_id())
    runner = ColabOneDigitalHumanRunner(runtime.comfy, tts_runner, run_cmd_fn=spy_ffmpeg)

    req_none = WorkflowRunRequest(
        workflow_id="digital-human-infinitetalk",
        overrides={
            "pipeline.audio_source": "audio",
            "pipeline.target_resolution": "1280x720",
            "pipeline.upscale_mode": "none",
            "pipeline.max_segment_seconds": 10.0,
        },
        inputs={"1.image": "a" * 32, "2.audio": "b" * 32},
    )
    outputs = runner.run(
        ColabOneTask("comfyui_run", "digital-human-infinitetalk"),
        wf_template,
        req_none,
        [("1.image", "portrait_native.png", portrait), ("2.audio", "voice_22s.wav", wav_22s)],
    )
    assert len(outputs) == 1
    assert len(submitted_prompts) == 3
    assert len(captured_filter_scripts) == 1
    filter_text = captured_filter_scripts[0]
    trim_spans = [
        (int(m.group(1)), int(m.group(2)))
        for m in re.finditer(r"trim=start_frame=(\d+):end_frame=(\d+)", filter_text)
    ]
    assert len(trim_spans) == 3
    assert trim_spans[0][0] == 0
    assert trim_spans[1][0] == 1
    assert trim_spans[2][0] == 1
    # Total trimmed frames MUST equal exact duration (22.0s at 25fps -> 550 frames).
    assert sum(end_f - start_f for start_f, end_f in trim_spans) == 550
    assert "concat=n=3:v=1:a=0[vcat]" in filter_text
    assert "[vcat]setsar=1[vout]" in filter_text
    assert "scale=" not in filter_text

    final_cmd = final_ffmpeg_cmds[0]
    assert "-filter_complex_script" in final_cmd
    assert "-safe" not in final_cmd
    assert "-vf" not in final_cmd
    assert "seg_000.mp4" in final_cmd
    assert "seg_001.mp4" in final_cmd
    assert "seg_002.mp4" in final_cmd
    wl_pairs = [
        (final_cmd[i], final_cmd[i + 1])
        for i in range(len(final_cmd) - 1)
        if final_cmd[i] == "-protocol_whitelist"
    ]
    assert len(wl_pairs) == 4
    assert all(val == "file,pipe" for _, val in wl_pairs)
    assert "832x480" in outputs[0]["filename"]
    assert not any("UpscaleModelLoader" == n["class_type"] for n in submitted_prompts[0].values())

    # --- 3. H16: digital_human.run() fails fast on VRAM/disk preflight BEFORE
    # unloading active LLM or invoking TTS ---
    runtime.loaded = "active-llm-model"
    stop_engine_spy = Mock()
    free_memory_spy = Mock()
    tts_spy = Mock()
    monkeypatch.setattr(runtime, "_stop_engine", stop_engine_spy)
    monkeypatch.setattr(runtime.comfy, "free_memory", free_memory_spy)
    monkeypatch.setattr(tts_runner, "synthesize", tts_spy)

    tts_req = WorkflowRunRequest(
        workflow_id="digital-human-infinitetalk",
        overrides={
            "pipeline.audio_source": "tts",
            "pipeline.tts_engine": "voxcpm2",
            "pipeline.script_text": "你好，这是预检失败前绝不能触发卸载或 TTS 的测试。",
        },
        inputs={"1.image": "a" * 32},
    )

    for err_msg in (
        "当前远端 GPU 显存不足：此工作流至少需要 40 GB 显存",
        "远端磁盘空间不足：至少需要 45.0 GiB 可用空间",
    ):
        monkeypatch.setattr(
            runtime.comfy.packs,
            "check_preflight",
            Mock(side_effect=RuntimeError(err_msg)),
        )
        with pytest.raises(RuntimeError, match=err_msg[:8]):
            runner.run(
                ColabOneTask("comfyui_run", "digital-human-infinitetalk"),
                wf_template,
                tts_req,
                [("1.image", "portrait_native.png", portrait)],
            )
        assert stop_engine_spy.call_count == 0
        assert free_memory_spy.call_count == 0
        assert tts_spy.call_count == 0


def test_tts_script_year_url_near_silence_and_pyproject_filtering(
    tmp_path, monkeypatch
):
    from server import tts_engines
    from server.tts_engines import (
        _extract_pyproject_dependencies,
        normalize_audio_loudness,
        split_colabone_tts_script,
    )

    # C4: Splits cleanly after years like "2026." while keeping URLs and numbered list prefixes intact.
    year_chunks = split_colabone_tts_script(
        "The platform launched in 2026. It gained rapid adoption across creative studios.",
        max_chars=50,
    )
    assert year_chunks == [
        "The platform launched in 2026.",
        "It gained rapid adoption across creative studios.",
    ]

    numbered = split_colabone_tts_script(
        "1. First item stays together without splitting the prefix.",
        max_chars=120,
    )
    assert numbered == ["1. First item stays together without splitting the prefix."]

    url_chunks = split_colabone_tts_script(
        "Check https://example.com/search?q=test for complete docs. Then start rendering!",
        max_chars=65,
    )
    assert any("https://example.com/search?q=test" in c for c in url_chunks)
    assert len(url_chunks) == 2
    assert url_chunks[0].endswith("docs.")
    assert url_chunks[1] == "Then start rendering!"

    # M8: normalize_audio_loudness MUST NOT boost near-silence (rms < 1e-4) or pure silence.
    sr = 24000
    near_silence = make_sine_samples(0.6, freq_hz=440.0, amp=1e-5, sample_rate=sr)
    rms_in = math.sqrt(sum(x * x for x in near_silence) / len(near_silence))
    assert rms_in < 1e-4
    out_near = normalize_audio_loudness(near_silence, sr, target_lufs=-23.0)
    assert len(out_near) == len(near_silence)
    assert max(abs(float(x)) for x in out_near) == pytest.approx(
        max(abs(x) for x in near_silence), abs=1e-9
    )

    zeros = make_silence_samples(0.5, sample_rate=sr)
    out_zeros = normalize_audio_loudness(zeros, sr, target_lufs=-23.0)
    assert all(float(x) == 0.0 for x in out_zeros)

    # H14: pyproject.toml dependency extraction skips torch/pyaudio & unsafe flags
    # under both stdlib tomllib and the Python 3.10 fallback parser.
    pyproject = tmp_path / "pyproject.toml"
    pyproject.write_text(
        (
            "[project]\n"
            'name = "demo-tts"\n'
            "dependencies = [\n"
            '    "torch>=2.4.0",\n'
            '    "torchaudio>=2.4.0",\n'
            '    "torchvision==0.19.0",\n'
            '    "xformers",\n'
            '    "pyaudio>=0.2.14",\n'
            '    "PyAudio",\n'
            '    "soundfile>=0.12.1",\n'
            '    "einops",\n'
            '    "omegaconf>=2.3.0",\n'
            '    "https://download.pytorch.org/whl/torch-2.4.0-cp312-linux_x86_64.whl",\n'
            '    "--extra-index-url https://evil.invalid",\n'
            "]\n"
        ),
        encoding="utf-8",
    )
    expected_deps = ["soundfile>=0.12.1", "einops", "omegaconf>=2.3.0"]
    assert _extract_pyproject_dependencies(pyproject) == expected_deps

    monkeypatch.setattr(tts_engines, "tomllib", None)
    assert _extract_pyproject_dependencies(pyproject) == expected_deps


def test_fastapi_comfyui_tts_engines_route(tmp_path):
    from fastapi.testclient import TestClient

    from server.app import create_colabone_app
    from server.runtime import ColabOneRuntime

    rt = ColabOneRuntime(tmp_path / "remote", ROOT / "catalog")
    token = "tts-route-test-control-token-32ch"
    auth = {"Authorization": "Bearer " + token}
    client = TestClient(create_colabone_app(rt, token, background=False))

    # Rejects unauthenticated requests.
    assert client.get("/api/v1/comfyui/tts-engines").status_code == 401

    resp = client.get("/api/v1/comfyui/tts-engines", headers=auth)
    assert resp.status_code == 200
    engines = resp.json()
    assert isinstance(engines, list)
    assert len(engines) == 8
    by_id = {e["id"]: e for e in engines}
    assert set(by_id.keys()) == {
        "fish_drama3",
        "moss_tts_v1_5",
        "breeze_tts2",
        "ming_3_tts",
        "voxcpm2",
        "qwen3_tts",
        "fish_s2_pro",
        "indextts2",
    }
    assert by_id["fish_drama3"]["commercial_ok"] is False
    assert by_id["fish_drama3"]["min_vram_mib"] >= 46080
    assert by_id["fish_drama3"]["sample_rate"] == 44100
    assert by_id["moss_tts_v1_5"]["commercial_ok"] is True
    assert by_id["breeze_tts2"]["commercial_ok"] is False
    assert by_id["breeze_tts2"]["supports_voice_design"] is True
    assert by_id["ming_3_tts"]["commercial_ok"] is True
    assert by_id["ming_3_tts"]["sample_rate"] == 44100
    assert by_id["voxcpm2"]["commercial_ok"] is True
    assert by_id["voxcpm2"]["sample_rate"] == 48000
    assert by_id["qwen3_tts"]["commercial_ok"] is True
    assert by_id["qwen3_tts"]["supports_custom_speakers"] is True
    assert by_id["fish_s2_pro"]["commercial_ok"] is False
    assert by_id["fish_s2_pro"]["supports_inline_tags"] is True
    assert by_id["indextts2"]["commercial_ok"] is False
    assert by_id["indextts2"]["supports_emotion_ref"] is True

    snap = client.get("/api/v1/snapshot", headers=auth)
    assert snap.status_code == 200
    assert {e["id"] for e in snap.json()["tts_engines"]} == set(by_id.keys())


# ---------------------------------------------------------------------------
# 7. Round-2 Hardening Regression Tests
# ---------------------------------------------------------------------------


def test_r2_comfy_start_server_omits_highvram_and_prepare_runs_before_ensure_pack(
    runtime, monkeypatch
):
    from server import comfyui as comfy_mod

    # 1. start_server() must NEVER pass --highvram even on 80 GB GPUs.
    monkeypatch.setattr(
        runtime,
        "gpu_info",
        lambda: {"name": "NVIDIA A100-SXM4-80GB", "total_mib": 81920, "used_mib": 0},
    )
    runtime.comfy.install_dir.mkdir(parents=True, exist_ok=True)
    (runtime.comfy.install_dir / "main.py").write_text("# stub\n", encoding="utf-8")
    (runtime.comfy.install_dir / ".complete").write_text(
        json.dumps({"commit": runtime.comfy.catalog.commit, "tag": runtime.comfy.catalog.tag}),
        encoding="utf-8",
    )

    spawned_args: list[list[str]] = []

    def fake_popen(args, **kwargs):
        spawned_args.append([str(a) for a in args])
        proc = Mock()
        proc.poll.return_value = None
        proc.pid = None
        return proc

    monkeypatch.setattr(comfy_mod.subprocess, "Popen", fake_popen)
    monkeypatch.setattr(comfy_mod.time, "sleep", lambda _: None)
    runtime.http = Mock()
    runtime.http.get.return_value = make_http_resp({"system": {"comfyui_version": "0.46.0"}})

    try:
        runtime.comfy.start_server(ColabOneTask("comfyui_start"))
        assert len(spawned_args) == 1
        cmd = spawned_args[0]
        assert "--highvram" not in cmd
        assert "--disable-mmap" in cmd
    finally:
        runtime.comfy.stop_server()

    # 2. ensure_template_ready() MUST call prepare() BEFORE packs.ensure_pack().
    call_order: list[str] = []
    runtime.comfy.proc = None
    runtime.comfy.state = "stopped"

    monkeypatch.setattr(
        runtime.comfy.packs,
        "check_preflight",
        lambda _wf: call_order.append("preflight"),
    )
    monkeypatch.setattr(
        runtime.comfy,
        "prepare",
        lambda _task: (call_order.append("prepare"), runtime.comfy.install_dir)[1],
    )
    monkeypatch.setattr(
        runtime.comfy.packs,
        "ensure_pack",
        lambda *_a, **_k: (call_order.append("ensure_pack"), False)[1],
    )
    monkeypatch.setattr(
        runtime.comfy,
        "start_server",
        lambda _task: call_order.append("start_server"),
    )

    wf = {
        "id": "wf-pack-order",
        "custom_nodes": [
            {
                "name": "ComfyUI-OrderNode",
                "repo": "https://github.com/example/ComfyUI-OrderNode.git",
                "commit": "a" * 40,
            }
        ],
    }
    runtime.comfy.ensure_template_ready(ColabOneTask("comfyui_run", "wf-pack-order"), wf)
    assert call_order == ["preflight", "prepare", "ensure_pack", "start_server"]
    assert call_order.index("prepare") < call_order.index("ensure_pack")


def test_r2_comfy_ws_monitor_stop_in_callback_buffer_cap_and_legacy_progress():
    from server.comfy_ws import (
        ColabOneComfyWsMonitor,
        MAX_PENDING_BUFFER_BYTES,
        WS_OP_TEXT,
        parse_comfy_ws_message,
    )

    # 1. Calling stop() from inside a callback must NOT deadlock (< 0.5s) and must
    # emit zero post-stop callbacks even when multiple frames arrive in one TCP burst.
    prompt_id = "prompt-stop-cb"
    burst_msgs = [
        {"type": "execution_start", "data": {"prompt_id": prompt_id}},
        {"type": "executing", "data": {"prompt_id": prompt_id, "node": "7"}},
        {"type": "progress", "data": {"prompt_id": prompt_id, "node": "7", "value": 1, "max": 4}},
        {"type": "progress", "data": {"prompt_id": prompt_id, "node": "7", "value": 2, "max": 4}},
        {"type": "executing", "data": {"prompt_id": prompt_id, "node": None}},
    ]
    srv_sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    srv_sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    srv_sock.bind(("127.0.0.1", 0))
    srv_port = srv_sock.getsockname()[1]
    srv_sock.listen(2)
    srv_sock.settimeout(3.0)

    def _serve_burst():
        try:
            conn, _ = srv_sock.accept()
        except OSError:
            return
        with conn:
            conn.settimeout(3.0)
            buf = bytearray()
            while b"\r\n\r\n" not in buf:
                chunk = conn.recv(4096)
                if not chunk:
                    return
                buf.extend(chunk)
            head = bytes(buf[: buf.index(b"\r\n\r\n")]).decode("latin-1")
            ws_key = ""
            for line in head.split("\r\n")[1:]:
                k, _, v = line.partition(":")
                if k.strip().lower() == "sec-websocket-key":
                    ws_key = v.strip()
            accept = base64.b64encode(
                hashlib.sha1((ws_key + _WS_GUID).encode("ascii")).digest()
            ).decode("ascii")
            burst = bytearray(
                (
                    "HTTP/1.1 101 Switching Protocols\r\n"
                    "Upgrade: websocket\r\n"
                    "Connection: Upgrade\r\n"
                    f"Sec-WebSocket-Accept: {accept}\r\n\r\n"
                ).encode("latin-1")
            )
            for msg in burst_msgs:
                burst.extend(_make_server_ws_frame(json.dumps(msg).encode("utf-8"), opcode=0x1))
            try:
                conn.sendall(bytes(burst))
            except OSError:
                pass

    srv_thread = threading.Thread(target=_serve_burst, daemon=True)
    srv_thread.start()

    seen_progress: list[dict] = []
    seen_done: list[dict] = []
    seen_errors: list[str] = []
    stop_elapsed: list[float] = []
    cb_finished = threading.Event()
    monitor_holder: list[ColabOneComfyWsMonitor] = []

    def _on_progress(ev: dict) -> None:
        seen_progress.append(ev)
        if len(seen_progress) == 1:
            t0 = time.monotonic()
            monitor_holder[0].stop()
            stop_elapsed.append(time.monotonic() - t0)
            cb_finished.set()

    monitor = ColabOneComfyWsMonitor(
        "127.0.0.1",
        srv_port,
        "colabone-stop-cb",
        prompt_id=prompt_id,
        on_progress=_on_progress,
        on_error=lambda err: seen_errors.append(str(err)),
        on_done=lambda ev: seen_done.append(ev),
    )
    monitor_holder.append(monitor)
    try:
        assert monitor.start() is True
        assert cb_finished.wait(timeout=2.0), "First WS callback never fired"
        # Wait briefly to confirm zero post-stop callbacks leak from remaining frames.
        if monitor._thread is not None:
            monitor._thread.join(timeout=1.0)
        assert len(stop_elapsed) == 1
        assert stop_elapsed[0] < 0.5
        assert len(seen_progress) == 1
        assert seen_done == []
        assert seen_errors == []
    finally:
        monitor.stop()
        srv_sock.close()
        srv_thread.join(timeout=2.0)

    # 2. _pending_buffer enforces the 4 MiB byte budget (dropping >4 MiB single items
    # and evicting oldest buffered messages when cumulative size exceeds 4 MiB).
    assert MAX_PENDING_BUFFER_BYTES == 4 * 1024 * 1024
    buf_mon = ColabOneComfyWsMonitor("127.0.0.1", 8188, "colabone-buf-cap", prompt_id="")
    oversized = ("x" * (MAX_PENDING_BUFFER_BYTES + 128)).encode("utf-8")
    buf_mon._handle_message(WS_OP_TEXT, oversized)
    assert len(buf_mon._pending_buffer) == 0
    assert buf_mon._pending_buffer_bytes == 0

    one_mib_pad = "a" * (1024 * 1024)
    for idx in range(6):
        payload = json.dumps(
            {
                "type": "executing",
                "data": {"prompt_id": "p-buf", "node": str(idx), "pad": one_mib_pad},
            }
        ).encode("utf-8")
        buf_mon._handle_message(WS_OP_TEXT, payload)
        assert buf_mon._pending_buffer_bytes <= MAX_PENDING_BUFFER_BYTES

    assert 1 <= len(buf_mon._pending_buffer) <= 3
    assert buf_mon._pending_buffer_bytes <= MAX_PENDING_BUFFER_BYTES
    # Newest message must be retained after evicting older ones.
    assert '"node": "5"' in buf_mon._pending_buffer[-1]

    # 3. Legacy `progress` message with max <= 1 yields fraction is None (never pins 100%).
    ev_one = parse_comfy_ws_message(
        json.dumps(
            {"type": "progress", "data": {"prompt_id": "p1", "node": "3", "value": 1, "max": 1}}
        ),
        prompt_id="p1",
    )
    assert ev_one is not None
    assert ev_one["phase"] == "sampling"
    assert ev_one["step"] == 1
    assert ev_one["max_steps"] == 1
    assert ev_one["fraction"] is None

    ev_zero = parse_comfy_ws_message(
        json.dumps(
            {"type": "progress", "data": {"prompt_id": "p1", "node": "3", "value": 0, "max": 0}}
        ),
        prompt_id="p1",
    )
    assert ev_zero is not None
    assert ev_zero["fraction"] is None

    ev_multi = parse_comfy_ws_message(
        json.dumps(
            {"type": "progress", "data": {"prompt_id": "p1", "node": "3", "value": 3, "max": 6}}
        ),
        prompt_id="p1",
    )
    assert ev_multi is not None
    assert ev_multi["fraction"] == pytest.approx(0.5)


def test_r2_runtime_stale_queue_cache_and_hf_token_download_hardening(
    runtime, tmp_path
):
    from fastapi.testclient import TestClient
    import requests
    from types import SimpleNamespace

    from server.app import ColabOneDownloadRequest, create_colabone_app
    from server.download import ColabOneTransfer

    # 1. Stale _queue_cache=(1, 0) after a ComfyUI task finishes must NOT trigger
    # spurious HTTP 409 on free_comfyui() or submit() when live /queue is empty.
    comfy_proc = Mock()
    comfy_proc.poll.return_value = None
    comfy_proc.pid = None
    runtime.comfy.proc = comfy_proc
    runtime.comfy.state = "ready"

    runtime.http = Mock()
    runtime.http.get.side_effect = lambda url, **kw: (
        make_http_resp({"queue_running": [], "queue_pending": []})
        if url.endswith("/queue")
        else make_http_resp({"ok": True})
    )
    runtime.http.post.side_effect = lambda url, **kw: make_http_resp({"ok": True})

    token = "r2-stale-queue-test-control-token-32ch"
    auth = {"Authorization": "Bearer " + token}
    with TestClient(create_colabone_app(runtime, token, background=False)) as client:
        with runtime.comfy.lock:
            runtime.comfy._queue_cache = (1, 0)
            runtime.comfy._queue_checked_at = time.monotonic()

        free_resp = client.post("/api/v1/comfyui/free", headers=auth)
        assert free_resp.status_code == 200, free_resp.text
        assert free_resp.json()["ok"] is True

        # Re-plant stale cache and verify runtime.submit() also refreshes live /queue.
        with runtime.comfy.lock:
            runtime.comfy._queue_cache = (1, 0)
            runtime.comfy._queue_checked_at = time.monotonic()

        done_ev = threading.Event()
        snap = runtime.submit("noop", "", lambda _t: done_ev.set())
        assert done_ev.wait(timeout=2.0)
        deadline = time.monotonic() + 2.0
        while runtime.tasks[snap["id"]].state == "running" and time.monotonic() < deadline:
            time.sleep(0.01)
        assert runtime.tasks[snap["id"]].state == "completed"

    # 2. ColabOneDownloadRequest rejects CR/LF/whitespace/control chars in hf_token.
    for bad_token in (
        "hf_secret\r\nX-Evil: 1",
        "hf_secret\nline2",
        "hf_secret\rline2",
        "hf_secret with space",
        "hf_secret\x00nul",
    ):
        with pytest.raises(ValidationError):
            ColabOneDownloadRequest(hf_token=bad_token)
    assert ColabOneDownloadRequest(hf_token="  hf_valid_123  ").hf_token == "hf_valid_123"

    # 3. _download_single() sanitizes InvalidHeader without leaking the HF token and fails fast.
    secret = "hf_SUPER_SECRET_HEADER_LEAK_987654"
    runtime.http = Mock()
    runtime.http.get.side_effect = requests.exceptions.InvalidHeader(
        f"Invalid header value 'Bearer {secret}\\r\\nInjected: 1'"
    )
    dl_task = ColabOneTask("download", "test-model")
    dl_task.transfer = ColabOneTransfer(1024, 16)
    file_spec = SimpleNamespace(size=1024, path="model.gguf")
    partial = tmp_path / "model.gguf.part"

    with pytest.raises(RuntimeError) as exc_info:
        runtime._download_single(
            "https://huggingface.co/org/repo/resolve/main/model.gguf",
            {"Authorization": f"Bearer {secret}"},
            partial,
            file_spec,
            dl_task,
            0,
        )
    err_text = str(exc_info.value)
    assert "InvalidHeader" in err_text
    assert secret not in err_text
    assert "Bearer" not in err_text
    assert runtime.http.get.call_count == 1


def test_r2_common_comfyui_depth_cap_hf_token_and_validators():
    from common.comfyui import (
        ComfyNodeSpec,
        WorkflowTemplate,
        validate_comfy_api_workflow,
    )

    # 1. JSON nesting depth > 32 raises ValueError (and never RecursionError even at extreme depth).
    nested_40: object = {"leaf": 1}
    for _ in range(40):
        nested_40 = {"child": nested_40}
    with pytest.raises(ValueError, match="嵌套层级"):
        validate_comfy_api_workflow(
            {"1": {"class_type": "DeepNode", "inputs": {"payload": nested_40}}}
        )

    nested_deep: object = 1
    for _ in range(1500):
        nested_deep = [nested_deep]
    with pytest.raises(ValueError, match="嵌套层级"):
        validate_comfy_api_workflow(
            {"1": {"class_type": "VeryDeepNode", "inputs": {"payload": nested_deep}}}
        )

    # 2. WorkflowRunRequest rejects CR/LF/control chars/interior whitespace in hf_token.
    for bad_token in (
        "hf_tok\r\nX-Injected: 1",
        "hf_tok\nsecond",
        "hf_tok\rsecond",
        "hf_tok with space",
        "hf_tok\ttab",
        "hf_tok\x00nul",
    ):
        with pytest.raises(ValidationError):
            WorkflowRunRequest(workflow_id="wf_ok", hf_token=bad_token)
    assert (
        WorkflowRunRequest(workflow_id="wf_ok", hf_token="  hf_clean_abc  ").hf_token
        == "hf_clean_abc"
    )

    # 3. Leading-dot custom node names and pip CLI flags like `numpy --pre` are rejected.
    with pytest.raises(ValidationError):
        ComfyNodeSpec(
            name=".hidden-node",
            repo="https://github.com/example/node.git",
            commit="a" * 40,
        )
    with pytest.raises(ValidationError):
        ComfyNodePatch(node_name=".hidden-node", kind="wav2vec2_hidden_states_hook")

    base_wf = {"1": {"class_type": "LoadImage", "inputs": {"image": "a.png"}}}
    for bad_pip in ("numpy --pre", "numpy -U", "--extra-index-url https://evil.invalid"):
        with pytest.raises(ValidationError):
            WorkflowTemplate(
                id="wf_bad_pip",
                name="Bad Pip",
                source="builtin",
                workflow=base_wf,
                pip_packages=[bad_pip],
            )
        with pytest.raises(ValidationError):
            ComfyTtsEngineSpec(
                id="bad_tts",
                name="Bad TTS",
                repo="org/repo",
                revision="a" * 40,
                pip_packages=[bad_pip],
            )


def test_r2_tts_engines_venv_voxcpm2_stereo_silence_pip_scrub_and_int16(
    runtime, tmp_path, monkeypatch
):
    import os
    import subprocess
    import sys
    import types

    from server.tts_engines import (
        ColabOneRemoteTtsRunner,
        _SILENCE_FLOOR_RMS,
        _WORKER_COMMON_HEADER,
        _filter_pip_packages,
        _is_standard_mono_pcm16_wav,
        _run_tts_subprocess,
        _sanitize_requirement_spec,
        normalize_audio_loudness,
    )

    # 1. ensure_engine_env passes `venv --without-pip --system-site-packages`.
    recorded_cmds: list[list[str]] = []
    runner = ColabOneRemoteTtsRunner(
        runtime,
        sample_tts_engines_by_id(),
        run_cmd_fn=lambda args, **_kw: recorded_cmds.append([str(x) for x in args]),
    )
    task = ColabOneTask("comfyui_run", "tts-r2")
    env_dir = runner.ensure_engine_env(task, runner.get_engine("voxcpm2"))
    venv_cmds = [c for c in recorded_cmds if "venv" in c]
    assert len(venv_cmds) == 1
    assert venv_cmds[0][:6] == [
        sys.executable,
        "-m",
        "venv",
        "--without-pip",
        "--system-site-packages",
        str(env_dir / ".venv"),
    ]

    # 2. VoxCPM2 worker inspects `_generate` when `generate(*args, **kwargs)` has no named params.
    mock_pkg_dir = tmp_path / "mock_voxcpm_pkg"
    mock_pkg_dir.mkdir(parents=True, exist_ok=True)
    capture_json = tmp_path / "voxcpm_capture.json"
    (mock_pkg_dir / "huggingface_hub.py").write_text(
        "def snapshot_download(**kwargs):\n    return '.'\n",
        encoding="utf-8",
    )
    (mock_pkg_dir / "voxcpm.py").write_text(
        (
            "import json\n"
            "from pathlib import Path\n"
            "class VoxCPM:\n"
            "    sample_rate = 48000\n"
            "    @classmethod\n"
            "    def from_pretrained(cls, repo, **kwargs):\n"
            "        return cls()\n"
            "    def _generate(self, text, prompt_wav_path=None, prompt_text=None, "
            "reference_wav_path=None, cfg_value=2.0, inference_timesteps=10, normalize=False):\n"
            "        pass\n"
            "    def generate(self, *args, **kwargs):\n"
            f"        Path({str(capture_json)!r}).write_text(json.dumps(kwargs), encoding='utf-8')\n"
            "        return [0.1, -0.1] * 240\n"
        ),
        encoding="utf-8",
    )
    worker_py = tmp_path / "worker_voxcpm2.py"
    worker_py.write_text(runner.build_worker_script("voxcpm2"), encoding="utf-8")
    ref_wav = write_pcm16_wav(
        tmp_path / "ref_mono.wav",
        make_sine_samples(0.2, freq_hz=440.0, amp=0.3, sample_rate=24000),
        sample_rate=24000,
    )
    out_seg_wav = tmp_path / "vox_seg_0.wav"
    cfg_path = tmp_path / "vox_cfg.json"
    cfg_path.write_text(
        json.dumps(
            {
                "repo": "openbmb/VoxCPM2",
                "revision": "7936080b41163874f38610f46f438e2c0757f9fd",
                "cache_dir": str(tmp_path / "cache"),
                "sample_rate": 48000,
                "ref_audio_path": str(ref_wav),
                "ref_text": "",
                "voice_prompt": "warm tone",
                "segments": [{"index": 0, "text": "你好世界", "output_wav": str(out_seg_wav)}],
            }
        ),
        encoding="utf-8",
    )
    sub_env = dict(os.environ)
    sub_env["PYTHONPATH"] = str(mock_pkg_dir) + (
        os.pathsep + sub_env["PYTHONPATH"] if sub_env.get("PYTHONPATH") else ""
    )
    proc_res = subprocess.run(
        [sys.executable, str(worker_py), "--config", str(cfg_path)],
        env=sub_env,
        capture_output=True,
        text=True,
        timeout=15,
    )
    assert proc_res.returncode == 0, proc_res.stderr
    assert out_seg_wav.is_file()
    captured_kwargs = json.loads(capture_json.read_text(encoding="utf-8"))
    assert captured_kwargs["reference_wav_path"] == str(ref_wav)
    assert captured_kwargs["text"] == "(warm tone)你好世界"

    # 3. Stereo WAV is NOT standard mono PCM16 and triggers ffmpeg mono PCM16 transcoding.
    stereo_wav = tmp_path / "ref_stereo.wav"
    with wave.open(str(stereo_wav), "wb") as wf_st:
        wf_st.setnchannels(2)
        wf_st.setsampwidth(2)
        wf_st.setframerate(24000)
        wf_st.writeframes(struct.pack("<hh", 1000, -1000) * 100)
    assert _is_standard_mono_pcm16_wav(stereo_wav) is False
    assert _is_standard_mono_pcm16_wav(ref_wav) is True

    ffmpeg_cmds: list[list[str]] = []
    transcode_runner = ColabOneRemoteTtsRunner(
        runtime,
        sample_tts_engines_by_id(),
        run_cmd_fn=lambda args, **_kw: ffmpeg_cmds.append([str(x) for x in args]),
    )
    # Mono PCM16 WAV bypasses ffmpeg.
    assert (
        transcode_runner._prepare_ref_audio_wav(
            task, ref_wav, tmp_path / "work_mono", "voice", target_sr=24000
        )
        == ref_wav
    )
    assert ffmpeg_cmds == []
    # Stereo WAV triggers ffmpeg -> mono PCM16 transcode.
    dst_mono = transcode_runner._prepare_ref_audio_wav(
        task, stereo_wav, tmp_path / "work_stereo", "voice", target_sr=24000
    )
    assert dst_mono == tmp_path / "work_stereo" / "ref_voice.wav"
    assert len(ffmpeg_cmds) == 1
    cmd = ffmpeg_cmds[0]
    assert cmd[0] == "ffmpeg"
    assert cmd[cmd.index("-ac") + 1] == "1"
    assert cmd[cmd.index("-ar") + 1] == "24000"
    assert cmd[cmd.index("-c:a") + 1] == "pcm_s16le"
    assert cmd[cmd.index("-protocol_whitelist") + 1] == "file,pipe"

    # 4. -70 dBFS silence gate prevents boosting sub--70 dBFS noise floor (including pyloudnorm gate).
    assert _SILENCE_FLOOR_RMS == pytest.approx(10.0 ** (-70.0 / 20.0))
    sr = 24000
    # -72 dBFS RMS sine wave (~2.5e-4 RMS, above old 1e-4 threshold, below new -70 dBFS gate).
    amp_72db = (10.0 ** (-72.0 / 20.0)) * math.sqrt(2.0)
    quiet_72db = make_sine_samples(0.6, freq_hz=440.0, amp=amp_72db, sample_rate=sr)
    out_72db = normalize_audio_loudness(quiet_72db, sr, target_lufs=-23.0)
    assert max(abs(x) for x in out_72db) == pytest.approx(
        max(abs(x) for x in quiet_72db), abs=1e-12
    )

    # When pyloudnorm reports <= -70 LUFS on a signal above RMS gate, it must NOT fall through to +40 dB RMS gain.
    fake_pyln = types.ModuleType("pyloudnorm")

    class _FakeMeter:
        def __init__(self, rate):
            self.rate = rate

        def integrated_loudness(self, _data):
            return -75.0

    fake_pyln.Meter = _FakeMeter  # type: ignore[attr-defined]
    monkeypatch.setitem(sys.modules, "pyloudnorm", fake_pyln)
    mid_50db = make_sine_samples(0.6, freq_hz=440.0, amp=10.0 ** (-50.0 / 20.0), sample_rate=sr)
    out_gated = normalize_audio_loudness(mid_50db, sr, target_lufs=-23.0)
    assert max(abs(x) for x in out_gated) == pytest.approx(
        max(abs(x) for x in mid_50db), abs=1e-12
    )

    # 5. Pip newline/flag/URL rejection in _sanitize_requirement_spec & _filter_pip_packages.
    bad_specs = [
        "soundfile\n--extra-index-url https://evil.invalid",
        "einops\r\n--pre",
        "numpy --pre",
        "numpy -U",
        "--index-url https://evil.invalid",
        "pkg @ https://evil.invalid/pkg.whl",
        "git+https://github.com/org/repo.git",
        "torch>=2.4.0",
    ]
    for spec in bad_specs:
        assert _sanitize_requirement_spec(spec) == ""
    assert _filter_pip_packages([*bad_specs, "soundfile>=0.12.1", "einops"]) == [
        "soundfile>=0.12.1",
        "einops",
    ]

    # 6. Scrub-before-slice: secret straddling the 800-char log tail boundary is fully scrubbed.
    boundary_secret = "hf_BOUNDARY_SECRET_TOKEN_XYZ789"
    log_file = tmp_path / "scrub_boundary.log"
    with pytest.raises(RuntimeError) as sub_exc:
        _run_tts_subprocess(
            [
                sys.executable,
                "-c",
                f"import sys; sys.stdout.write('PRE ' + {boundary_secret!r} + 'A' * 790 + '\\n'); sys.exit(1)",
            ],
            task,
            log_file,
            timeout=15,
            secret_to_scrub=boundary_secret,
        )
    sub_err = str(sub_exc.value)
    assert boundary_secret not in sub_err
    assert "BOUNDARY_SECRET" not in sub_err
    assert "XYZ789" not in sub_err
    assert "***" in sub_err

    # 7. _to_mono_list scales int16 arrays by 32768.0 (and other int dtypes by iinfo.max) into [-1, 1].
    class _FakeIntType:
        pass

    class _FakeInt16(_FakeIntType):
        pass

    class _FakeInt32(_FakeIntType):
        pass

    class _FakeFloat32:
        pass

    class _FakeArray:
        def __init__(self, data, dtype):
            self._data = data
            self.dtype = dtype
            if isinstance(data, list) and data and isinstance(data[0], list):
                self.ndim = 2
                self.shape = (len(data), len(data[0]))
            elif isinstance(data, list):
                self.ndim = 1
                self.shape = (len(data),)
            else:
                self.ndim = 0
                self.shape = ()

        def astype(self, new_dtype):
            if self.ndim == 2:
                return _FakeArray([[float(v) for v in row] for row in self._data], new_dtype)
            return _FakeArray([float(v) for v in self._data], new_dtype)

        def __truediv__(self, scalar):
            if self.ndim == 2:
                return _FakeArray(
                    [[v / scalar for v in row] for row in self._data], self.dtype
                )
            return _FakeArray([v / scalar for v in self._data], self.dtype)

        def mean(self, axis=0):
            if axis == 0:
                cols = len(self._data[0])
                rows = len(self._data)
                return _FakeArray(
                    [sum(self._data[r][c] for r in range(rows)) / rows for c in range(cols)],
                    self.dtype,
                )
            return _FakeArray(
                [sum(row) / len(row) for row in self._data],
                self.dtype,
            )

        def tolist(self):
            return list(self._data)

    fake_np = types.ModuleType("numpy")
    fake_np.integer = _FakeIntType  # type: ignore[attr-defined]
    fake_np.int16 = _FakeInt16  # type: ignore[attr-defined]
    fake_np.int32 = _FakeInt32  # type: ignore[attr-defined]
    fake_np.float32 = _FakeFloat32  # type: ignore[attr-defined]
    fake_np.asarray = lambda x: x if isinstance(x, _FakeArray) else _FakeArray(list(x), _FakeFloat32)  # type: ignore[attr-defined]
    fake_np.issubdtype = lambda dt, base: issubclass(dt, base)  # type: ignore[attr-defined]
    fake_np.iinfo = lambda dt: types.SimpleNamespace(max=32767 if dt is _FakeInt16 else 2147483647)  # type: ignore[attr-defined]
    fake_np.nan_to_num = lambda arr, **_kw: arr  # type: ignore[attr-defined]

    worker_ns: dict = {}
    exec(_WORKER_COMMON_HEADER, worker_ns)
    _to_mono_list = worker_ns["_to_mono_list"]

    with monkeypatch.context() as mp:
        mp.setitem(sys.modules, "numpy", fake_np)
        scaled_i16 = _to_mono_list(_FakeArray([0, 16384, -32768, 32767], _FakeInt16))
        stereo_i16 = _to_mono_list(
            _FakeArray([[16384, -32768, 0, 16384], [16384, 0, 0, -16384]], _FakeInt16)
        )
        scaled_i32 = _to_mono_list(_FakeArray([0, 2147483647], _FakeInt32))
        scaled_f32 = _to_mono_list(_FakeArray([0.25, -0.5], _FakeFloat32))

    assert scaled_i16 == pytest.approx([0.0, 0.5, -1.0, 32767 / 32768.0], abs=1e-6)
    assert stereo_i16 == pytest.approx([0.5, -0.5, 0.0, 0.0], abs=1e-6)
    assert scaled_i32 == pytest.approx([0.0, 1.0], abs=1e-6)
    assert scaled_f32 == pytest.approx([0.25, -0.5], abs=1e-6)


# ---------------------------------------------------------------------------
# 8. Extreme Digital Human Optimization & Built-in Female Voice Tests
# ---------------------------------------------------------------------------


def _sample_longcat_workflow() -> dict:
    return {
        "1": {
            "class_type": "LoadImage",
            "_meta": {"title": "主播形象图"},
            "inputs": {"image": "portrait.png"},
        },
        "2": {
            "class_type": "LoadAudio",
            "_meta": {"title": "口播驱动音频"},
            "inputs": {"audio": "voice.wav"},
        },
        "3": {
            "class_type": "ImageResizeKJv2",
            "_meta": {"title": "缩放主播画面"},
            "inputs": {
                "image": ["1", 0],
                "width": 832,
                "height": 480,
                "upscale_method": "lanczos",
                "keep_proportion": "crop",
                "divisible_by": 16,
            },
        },
        "4": {
            "class_type": "WanVideoVAELoader",
            "_meta": {"title": "加载 Wan2.1 VAE"},
            "inputs": {
                "model_name": "Wan2_1_VAE_bf16.safetensors",
                "precision": "bf16",
                "use_cpu_cache": False,
            },
        },
        "5": {
            "class_type": "WanVideoTextEncodeCached",
            "_meta": {"title": "画面与口播风格提示词"},
            "inputs": {
                "model_name": "umt5-xxl-enc-fp8_e4m3fn.safetensors",
                "positive_prompt": "Static medium shot of an engaging presenter speaking warmly with expressive hand gestures",
                "negative_prompt": "static, frozen hands, blurry",
            },
        },
        "6": {
            "class_type": "WanVideoEncode",
            "_meta": {"title": "编码首帧参考潜空间"},
            "inputs": {
                "vae": ["4", 0],
                "image": ["3", 0],
                "enable_vae_tiling": False,
                "tile_x": 272,
                "tile_y": 272,
                "tile_stride_x": 144,
                "tile_stride_y": 128,
                "noise_aug_strength": 0.0,
                "latent_strength": 1.0,
            },
        },
        "8": {
            "class_type": "WhisperModelLoader",
            "_meta": {"title": "Whisper 音频编码器"},
            "inputs": {
                "model": "whisper_large_v3_encoder_fp16.safetensors",
                "base_precision": "fp16",
                "load_device": "main_device",
            },
        },
        "9": {
            "class_type": "LongCatAvatarWhisperEmbeds",
            "_meta": {"title": "LongCat Whisper 音频特征"},
            "inputs": {
                "whisper_model": ["8", 0],
                "audio_1": ["2", 0],
                "normalize_loudness": False,
                "num_frames": 93,
                "fps": 25.0,
                "audio_scale": 1.15,
                "audio_cfg_scale": 1.0,
                "multi_audio_type": "para",
            },
        },
        "10": {
            "class_type": "WanVideoLongCatAvatarExtendEmbeds",
            "_meta": {"title": "LongCat 音视频融合条件"},
            "inputs": {
                "prev_latents": ["6", 0],
                "audio_embeds": ["9", 0],
                "num_frames": ["9", 2],
                "overlap": 1,
                "frames_processed": 0,
                "if_not_enough_audio": "pad_with_start",
                "ref_frame_index": 10,
                "ref_mask_frame_range": 3,
            },
        },
        "11": {
            "class_type": "VHS_VideoCombine",
            "_meta": {"title": "合成有声 MP4 视频"},
            "inputs": {
                "images": ["16", 0],
                "audio": ["2", 0],
                "frame_rate": 25,
                "format": "video/h264-mp4",
                "filename_prefix": "ColabOne_LongCatAvatar",
            },
        },
        "12": {
            "class_type": "WanVideoLoraSelect",
            "_meta": {"title": "加载 LongCat DMD 蒸馏 LoRA"},
            "inputs": {
                "lora": "LongCat-Avatar-15_dmd_distill_lora_rank128_bf16.safetensors",
                "strength": 1.0,
                "low_mem_load": False,
                "merge_loras": True,
            },
        },
        "13": {
            "class_type": "WanVideoModelLoader",
            "_meta": {"title": "加载 LongCat-Avatar 1.5 主模型"},
            "inputs": {
                "model": "LongCat-Avatar-15_bf16.safetensors",
                "base_precision": "bf16",
                "quantization": "disabled",
                "load_device": "offload_device",
                "attention_mode": "sdpa",
                "lora": ["12", 0],
                "rms_norm_function": "default",
            },
        },
        "14": {
            "class_type": "WanVideoSchedulerv2",
            "_meta": {"title": "LongCat 蒸馏调度器"},
            "inputs": {
                "scheduler": "longcat_distill_euler",
                "steps": 8,
                "shift": 7.0,
                "start_step": 0,
                "end_step": -1,
                "enhance_hf": False,
            },
        },
        "15": {
            "class_type": "WanVideoSamplerv2",
            "_meta": {"title": "LongCat-Avatar 扩散采样器"},
            "inputs": {
                "model": ["13", 0],
                "image_embeds": ["10", 0],
                "text_embeds": ["5", 0],
                "scheduler": ["14", 0],
                "cfg": 1.0,
                "seed": 42,
                "force_offload": True,
                "add_noise_to_samples": False,
            },
        },
        "16": {
            "class_type": "WanVideoDecode",
            "_meta": {"title": "VAE 视频解码"},
            "inputs": {
                "vae": ["4", 0],
                "samples": ["15", 0],
                "enable_vae_tiling": True,
                "tile_x": 272,
                "tile_y": 272,
                "tile_stride_x": 144,
                "tile_stride_y": 128,
                "normalization": "default",
            },
        },
    }


def _sample_longcat_template() -> dict:
    return {
        "id": "digital-human-longcat",
        "name": "口播数字人 · 旗舰高清版",
        "source": "builtin",
        "min_vram_mib": 49152,
        "pipeline": {
            "kind": "digital_human",
            "video_backend": "longcat_avatar",
            "default_tts_engine": "voxcpm2",
            "supported_tts_engines": ["voxcpm2", "qwen3_tts", "fish_s2_pro", "indextts2"],
            "max_segment_seconds": 3.7,
            "tail_frame_offset_seconds": 0.08,
            "output_fps": 25,
            "target_width": 1280,
            "target_height": 720,
            "upscale_mode": "ai_2x",
            "audio_node_key": "2.audio",
            "image_node_key": "1.image",
            "prompt_node_key": "5.positive_prompt",
            "frames_node_key": "9.num_frames",
            "width_node_key": "3.width",
            "height_node_key": "3.height",
            "fps_node_key": "9.fps",
            "steps_node_key": "14.steps",
            "seed_node_key": "15.seed",
        },
        "models": [
            {
                "repo": "Phips/2xNomosUni_span_multijpg",
                "revision": "ed6111c4cabdaf42b75e6535600d5370cce9e4e2",
                "path": "2xNomosUni_span_multijpg.safetensors",
                "target_dir": "upscale_models",
                "size": 4461056,
                "sha256": "bee2a9c082f2b8f6e7f5db504b36593c24a1a959511f587114c399ca58b9c92c",
            }
        ],
        "workflow": _sample_longcat_workflow(),
    }


def test_opt_apply_longcat_acceleration_five_gpu_profiles_and_three_modes():
    from server.digital_human import apply_longcat_acceleration

    base_wf = _sample_longcat_workflow()
    snapshot_before = json.dumps(base_wf, sort_keys=True)

    # 1. Blackwell sm_120 96 GB (sage3 + sage2 + fp8_fast + torch.compile) across max / balanced / compat.
    blackwell_caps = {
        "gpu_name": "NVIDIA RTX PRO 6000 Blackwell",
        "vram_mib": 98304,
        "cc_major": 12,
        "cc_minor": 0,
        "fp8_fast_supported": True,
        "torch_compile_supported": True,
        "sageattn_available": True,
        "sageattn3_available": True,
        "flash_attn_2_available": False,
    }
    wf_bw_max, sum_bw_max = apply_longcat_acceleration(
        base_wf, profile="max", gpu_caps=blackwell_caps
    )
    assert json.dumps(base_wf, sort_keys=True) == snapshot_before
    assert sum_bw_max["profile"] == "max"
    assert sum_bw_max["attention_mode"] == "sageattn_3"
    assert sum_bw_max["quantization"] == "fp8_e4m3fn_fast"
    assert sum_bw_max["resident_vram"] is True
    assert sum_bw_max["vae_tiling"] is False
    assert sum_bw_max["torch_compile"] is True
    assert sum_bw_max["rms_norm_function"] == "default"
    assert wf_bw_max["13"]["inputs"]["attention_mode"] == "sageattn_3"
    assert wf_bw_max["13"]["inputs"]["quantization"] == "fp8_e4m3fn_fast"
    assert wf_bw_max["13"]["inputs"]["load_device"] == "main_device"
    assert wf_bw_max["13"]["inputs"]["rms_norm_function"] == "default"
    assert wf_bw_max["13"]["inputs"]["compile_args"] == ["colabone_torch_compile", 0]
    compile_node = wf_bw_max["colabone_torch_compile"]
    assert compile_node["class_type"] == "WanVideoTorchCompileSettings"
    assert compile_node["inputs"]["backend"] == "inductor"
    assert compile_node["inputs"]["fullgraph"] is False
    assert compile_node["inputs"]["compile_transformer_blocks_only"] is True
    assert wf_bw_max["15"]["inputs"]["force_offload"] is False
    assert wf_bw_max["16"]["inputs"]["enable_vae_tiling"] is False

    wf_bw_bal, sum_bw_bal = apply_longcat_acceleration(
        base_wf, profile="balanced", gpu_caps=blackwell_caps
    )
    assert sum_bw_bal["profile"] == "balanced"
    assert sum_bw_bal["attention_mode"] == "sageattn_3"
    assert sum_bw_bal["quantization"] == "fp8_e4m3fn_fast"
    assert sum_bw_bal["resident_vram"] is True
    assert sum_bw_bal["vae_tiling"] is False
    assert sum_bw_bal["torch_compile"] is False
    assert sum_bw_bal["rms_norm_function"] == "pytorch"
    assert "colabone_torch_compile" not in wf_bw_bal
    assert "compile_args" not in wf_bw_bal["13"]["inputs"]
    assert wf_bw_bal["13"]["inputs"]["rms_norm_function"] == "pytorch"

    wf_bw_compat, sum_bw_compat = apply_longcat_acceleration(
        base_wf, profile="compat", gpu_caps=blackwell_caps
    )
    assert sum_bw_compat["profile"] == "compat"
    assert sum_bw_compat["attention_mode"] == "sdpa"
    assert sum_bw_compat["quantization"] == "disabled"
    assert sum_bw_compat["resident_vram"] is False
    assert sum_bw_compat["vae_tiling"] is True
    assert sum_bw_compat["torch_compile"] is False
    assert sum_bw_compat["rms_norm_function"] == "default"
    assert wf_bw_compat["13"]["inputs"]["attention_mode"] == "sdpa"
    assert wf_bw_compat["13"]["inputs"]["quantization"] == "disabled"
    assert wf_bw_compat["13"]["inputs"]["load_device"] == "offload_device"
    assert wf_bw_compat["15"]["inputs"]["force_offload"] is True
    assert wf_bw_compat["16"]["inputs"]["enable_vae_tiling"] is True
    assert "colabone_torch_compile" not in wf_bw_compat

    # 2. Hopper sm_90 80 GB (H100): sageattn + fp8_e4m3fn_fast + resident + compile.
    hopper_caps = {
        "gpu_name": "NVIDIA H100 80GB HBM3",
        "vram_mib": 81920,
        "cc_major": 9,
        "cc_minor": 0,
        "fp8_fast_supported": True,
        "torch_compile_supported": True,
        "sageattn_available": True,
        "sageattn3_available": False,
        "flash_attn_2_available": True,
    }
    wf_h100, sum_h100 = apply_longcat_acceleration(
        base_wf, profile="max", gpu_caps=hopper_caps
    )
    assert sum_h100["attention_mode"] == "sageattn"
    assert sum_h100["quantization"] == "fp8_e4m3fn_fast"
    assert sum_h100["resident_vram"] is True
    assert sum_h100["vae_tiling"] is False
    assert sum_h100["torch_compile"] is True
    assert wf_h100["13"]["inputs"]["attention_mode"] == "sageattn"
    assert wf_h100["13"]["inputs"]["load_device"] == "main_device"

    # 3. Ada sm_89 24 GB (L4): fp8_fast + sageattn, but non-resident offload + tiled VAE.
    ada_caps = {
        "gpu_name": "NVIDIA L4",
        "vram_mib": 24576,
        "cc_major": 8,
        "cc_minor": 9,
        "fp8_fast_supported": True,
        "torch_compile_supported": True,
        "sageattn_available": True,
        "sageattn3_available": False,
        "flash_attn_2_available": False,
    }
    wf_ada, sum_ada = apply_longcat_acceleration(
        base_wf, profile="max", gpu_caps=ada_caps
    )
    assert sum_ada["attention_mode"] == "sageattn"
    assert sum_ada["quantization"] == "fp8_e4m3fn_fast"
    assert sum_ada["resident_vram"] is False
    assert sum_ada["vae_tiling"] is True
    assert sum_ada["torch_compile"] is True
    assert wf_ada["13"]["inputs"]["load_device"] == "offload_device"
    assert wf_ada["15"]["inputs"]["force_offload"] is True
    assert wf_ada["16"]["inputs"]["enable_vae_tiling"] is True

    # 4. Ampere sm_80 80 GB (A100): BF16 resident (no fp8_fast), while A100 40 GB offloads.
    ampere_80_caps = {
        "gpu_name": "NVIDIA A100-SXM4-80GB",
        "vram_mib": 81920,
        "cc_major": 8,
        "cc_minor": 0,
        "fp8_fast_supported": False,
        "torch_compile_supported": True,
        "sageattn_available": True,
        "sageattn3_available": False,
        "flash_attn_2_available": True,
    }
    wf_a100, sum_a100 = apply_longcat_acceleration(
        base_wf, profile="max", gpu_caps=ampere_80_caps
    )
    assert sum_a100["attention_mode"] == "sageattn"
    assert sum_a100["quantization"] == "disabled"
    assert sum_a100["resident_vram"] is True
    assert sum_a100["vae_tiling"] is False
    assert sum_a100["torch_compile"] is True
    assert wf_a100["13"]["inputs"]["quantization"] == "disabled"
    assert wf_a100["13"]["inputs"]["load_device"] == "main_device"
    assert wf_a100["15"]["inputs"]["force_offload"] is False

    _, sum_a100_40 = apply_longcat_acceleration(
        base_wf, profile="balanced", gpu_caps={**ampere_80_caps, "vram_mib": 40960}
    )
    assert sum_a100_40["quantization"] == "disabled"
    assert sum_a100_40["resident_vram"] is False
    assert sum_a100_40["vae_tiling"] is True
    assert sum_a100_40["torch_compile"] is False

    # 5. Turing sm_75 16 GB (T4): full fallback to sdpa + disabled + offload + no compile.
    turing_caps = {
        "gpu_name": "NVIDIA Tesla T4",
        "vram_mib": 15360,
        "cc_major": 7,
        "cc_minor": 5,
        "fp8_fast_supported": False,
        "torch_compile_supported": False,
        "sageattn_available": False,
        "sageattn3_available": False,
        "flash_attn_2_available": False,
    }
    wf_t4, sum_t4 = apply_longcat_acceleration(
        base_wf, profile="max", gpu_caps=turing_caps
    )
    assert sum_t4["attention_mode"] == "sdpa"
    assert sum_t4["quantization"] == "disabled"
    assert sum_t4["resident_vram"] is False
    assert sum_t4["vae_tiling"] is True
    assert sum_t4["torch_compile"] is False
    assert wf_t4["13"]["inputs"]["attention_mode"] == "sdpa"
    assert wf_t4["13"]["inputs"]["load_device"] == "offload_device"
    assert wf_t4["15"]["inputs"]["force_offload"] is True
    assert wf_t4["16"]["inputs"]["enable_vae_tiling"] is True
    assert "colabone_torch_compile" not in wf_t4

    # When SageAttention is absent but FlashAttention-2 is available, balanced/max falls back to flash_attn_2.
    _, sum_fa2 = apply_longcat_acceleration(
        base_wf,
        profile="balanced",
        gpu_caps={**hopper_caps, "sageattn_available": False, "flash_attn_2_available": True},
    )
    assert sum_fa2["attention_mode"] == "flash_attn_2"


def test_opt_probe_gpu_acceleration_capabilities_rejects_sageattn_v1_accepts_v2(
    monkeypatch,
):
    import importlib.metadata
    import importlib.util
    import shutil
    import subprocess
    import sys
    import types

    from server.comfy_packs import probe_gpu_acceleration_capabilities

    orig_find_spec = importlib.util.find_spec
    orig_meta_version = importlib.metadata.version
    orig_which = shutil.which
    orig_sub_run = subprocess.run
    orig_sub_check_output = subprocess.check_output

    def _run_probe(
        *,
        cc: tuple[int, int],
        vram_mib: int,
        torch_ver: str,
        cuda_ver: str,
        nvcc_ver: str,
        sage_ver: str | None,
        has_sage3: bool,
        has_triton: bool = True,
        has_flash: bool = False,
    ) -> dict:
        fake_torch = types.ModuleType("torch")
        fake_torch.__version__ = torch_ver  # type: ignore[attr-defined]
        fake_torch.version = types.SimpleNamespace(cuda=cuda_ver)  # type: ignore[attr-defined]
        fake_torch.cuda = types.SimpleNamespace(  # type: ignore[attr-defined]
            is_available=lambda: True,
            get_device_capability=lambda _idx=0: cc,
            get_device_name=lambda _idx=0: f"Mock GPU sm_{cc[0]}{cc[1]}",
            get_device_properties=lambda _idx=0: types.SimpleNamespace(
                name=f"Mock GPU sm_{cc[0]}{cc[1]}",
                major=cc[0],
                minor=cc[1],
                total_memory=vram_mib * 1024 * 1024,
            ),
        )

        present_specs = {
            "triton": object() if has_triton else None,
            "sageattention": object() if sage_ver is not None else None,
            "sageattn3": object() if has_sage3 else None,
            "flash_attn": object() if has_flash else None,
        }

        def fake_find_spec(name, package=None):
            if name in present_specs:
                return present_specs[name]
            return orig_find_spec(name, package)

        def fake_meta_version(dist_name):
            norm = dist_name.lower().replace("_", "-")
            if norm == "sageattention":
                if sage_ver is not None:
                    return sage_ver
                raise importlib.metadata.PackageNotFoundError(dist_name)
            if norm in ("sageattn3", "sageattention3-blackwell"):
                if has_sage3:
                    return "1.0.0"
                raise importlib.metadata.PackageNotFoundError(dist_name)
            if norm == "triton":
                if has_triton:
                    return "3.3.0"
                raise importlib.metadata.PackageNotFoundError(dist_name)
            if norm == "flash-attn":
                if has_flash:
                    return "2.7.4"
                raise importlib.metadata.PackageNotFoundError(dist_name)
            return orig_meta_version(dist_name)

        def fake_which(cmd, *a, **k):
            if cmd == "nvcc":
                return "/usr/local/cuda/bin/nvcc" if nvcc_ver else None
            return orig_which(cmd, *a, **k)

        nvcc_stdout = (
            f"nvcc: NVIDIA (R) Cuda compiler driver\n"
            f"Cuda compilation tools, release {nvcc_ver}, V{nvcc_ver}.93\n"
        )

        def fake_sub_run(cmd, *a, **k):
            if cmd and Path(str(cmd[0])).name == "nvcc":
                return subprocess.CompletedProcess(cmd, 0, stdout=nvcc_stdout, stderr="")
            return orig_sub_run(cmd, *a, **k)

        def fake_sub_check_output(cmd, *a, **k):
            if cmd and Path(str(cmd[0])).name == "nvcc":
                return nvcc_stdout if k.get("text") else nvcc_stdout.encode("utf-8")
            return orig_sub_check_output(cmd, *a, **k)

        with monkeypatch.context() as mp:
            mp.setitem(sys.modules, "torch", fake_torch)
            if sage_ver is not None:
                fake_sage = types.ModuleType("sageattention")
                fake_sage.__version__ = sage_ver  # type: ignore[attr-defined]
                mp.setitem(sys.modules, "sageattention", fake_sage)
            else:
                mp.delitem(sys.modules, "sageattention", raising=False)
            mp.setattr(importlib.util, "find_spec", fake_find_spec)
            mp.setattr(importlib.metadata, "version", fake_meta_version)
            mp.setattr(shutil, "which", fake_which)
            mp.setattr(subprocess, "run", fake_sub_run)
            mp.setattr(subprocess, "check_output", fake_sub_check_output)
            return probe_gpu_acceleration_capabilities(
                {"name": f"Mock GPU sm_{cc[0]}{cc[1]}", "total_mib": vram_mib}
            )

    expected_keys = {
        "gpu_name",
        "vram_mib",
        "cc_major",
        "cc_minor",
        "torch_version",
        "cuda_version",
        "nvcc_version",
        "triton_available",
        "torch_compile_supported",
        "fp8_fast_supported",
        "sageattn_version",
        "sageattn_available",
        "sageattn3_available",
        "flash_attn_2_available",
    }

    # 1. PyPI sageattention==1.0.6 MUST be rejected (sageattn_available is False).
    caps_v1 = _run_probe(
        cc=(12, 0),
        vram_mib=98304,
        torch_ver="2.9.0+cu128",
        cuda_ver="12.8",
        nvcc_ver="12.8",
        sage_ver="1.0.6",
        has_sage3=False,
    )
    assert expected_keys.issubset(caps_v1.keys())
    assert caps_v1["sageattn_available"] is False
    assert caps_v1["cc_major"] == 12
    assert caps_v1["cc_minor"] == 0
    assert caps_v1["fp8_fast_supported"] is True
    assert caps_v1["triton_available"] is True
    assert caps_v1["torch_compile_supported"] is True
    assert caps_v1["sageattn3_available"] is False

    # 2. Source-built sageattention==2.2.0 + sageattn3 on Blackwell sm_120 MUST be accepted.
    caps_v2 = _run_probe(
        cc=(12, 0),
        vram_mib=98304,
        torch_ver="2.9.0+cu128",
        cuda_ver="12.8",
        nvcc_ver="12.8",
        sage_ver="2.2.0",
        has_sage3=True,
    )
    assert caps_v2["sageattn_available"] is True
    assert caps_v2["sageattn_version"] == "2.2.0"
    assert caps_v2["sageattn3_available"] is True
    assert caps_v2["fp8_fast_supported"] is True
    assert caps_v2["torch_compile_supported"] is True

    # 3. sageattn3 MUST be False on non-Blackwell (Hopper sm_90) even if package spec exists.
    caps_h100 = _run_probe(
        cc=(9, 0),
        vram_mib=81920,
        torch_ver="2.9.0+cu128",
        cuda_ver="12.8",
        nvcc_ver="12.8",
        sage_ver="2.2.0",
        has_sage3=True,
        has_flash=True,
    )
    assert caps_h100["sageattn_available"] is True
    assert caps_h100["sageattn3_available"] is False
    assert caps_h100["flash_attn_2_available"] is True
    assert caps_h100["fp8_fast_supported"] is True

    # 4. Ampere sm_80: fp8_fast_supported is False, torch<2.7 disables torch_compile_supported.
    caps_a100_old_torch = _run_probe(
        cc=(8, 0),
        vram_mib=81920,
        torch_ver="2.6.0+cu124",
        cuda_ver="12.4",
        nvcc_ver="12.4",
        sage_ver="2.2.0",
        has_sage3=False,
    )
    assert caps_a100_old_torch["fp8_fast_supported"] is False
    assert caps_a100_old_torch["torch_compile_supported"] is False
    assert caps_a100_old_torch["sageattn_available"] is True


def test_opt_ensure_sageattention_wheels_cache_reuse_drive_build_and_graceful_degrade(
    tmp_path, monkeypatch
):
    import shutil
    import zipfile

    from server import comfy_packs
    from server.comfy_packs import (
        ColabOneComfyPackManager,
        SAGEATTN_COMMIT,
        SAGEATTN_REPO_URL,
    )

    assert SAGEATTN_REPO_URL == "https://github.com/thu-ml/SageAttention.git"
    assert _HEX_40_RE.fullmatch(SAGEATTN_COMMIT)

    rt = _make_mock_runtime(tmp_path, total_mib=81920)
    drive_ws = tmp_path / "drive_mount" / "ColabOne_Drive" / "workspace"
    drive_ws.mkdir(parents=True, exist_ok=True)
    mock_drive = Mock()
    mock_drive.mode = "colab_drivefs"
    mock_drive.mount_root = tmp_path / "drive_mount"
    mock_drive.available.return_value = True
    mock_drive.is_colab_drivefs_mounted.return_value = True
    mock_drive._drivefs_workspace_dir.return_value = drive_ws
    rt.drive = mock_drive

    install_dir = rt.root / "comfyui"
    models_dir = rt.root / "comfyui-models"
    install_dir.mkdir(parents=True, exist_ok=True)
    models_dir.mkdir(parents=True, exist_ok=True)
    mgr = ColabOneComfyPackManager(rt, install_dir, models_dir)

    orig_which = shutil.which
    nvcc_present = {"ok": True}
    monkeypatch.setattr(
        shutil,
        "which",
        lambda cmd, *a, **k: (
            ("/usr/local/cuda/bin/nvcc" if nvcc_present["ok"] else None)
            if cmd == "nvcc"
            else ("/usr/bin/git" if cmd == "git" else orig_which(cmd, *a, **k))
        ),
    )

    def _write_dummy_whl(dest_dir: Path, filename: str) -> Path:
        dest_dir.mkdir(parents=True, exist_ok=True)
        whl_path = dest_dir / filename
        with zipfile.ZipFile(whl_path, "w") as zf:
            zf.writestr("METADATA", "Metadata-Version: 2.1\nName: sageattention\nVersion: 2.2.0\n")
        return whl_path

    # 1. Graceful no-op on Turing (cc_major < 8) and when nvcc is missing.
    noop_cmds: list[list[str]] = []
    turing_caps = {
        "gpu_name": "NVIDIA Tesla T4",
        "vram_mib": 15360,
        "cc_major": 7,
        "cc_minor": 5,
        "torch_version": "2.9.0+cu128",
        "cuda_version": "12.8",
        "nvcc_version": "12.8",
        "sageattn_available": False,
        "sageattn3_available": False,
    }
    res_t4 = mgr.ensure_sageattention_wheels(
        ColabOneTask("comfyui_run", "t4-noop"),
        gpu_caps=turing_caps,
        run_cmd_fn=lambda *a, **k: noop_cmds.append([str(x) for x in a[0]]),
    )
    assert noop_cmds == []
    assert res_t4["sageattn_available"] is False

    nvcc_present["ok"] = False
    no_nvcc_caps = {
        **turing_caps,
        "gpu_name": "NVIDIA H100",
        "vram_mib": 81920,
        "cc_major": 9,
        "cc_minor": 0,
        "nvcc_version": "",
    }
    res_no_nvcc = mgr.ensure_sageattention_wheels(
        ColabOneTask("comfyui_run", "no-nvcc"),
        gpu_caps=no_nvcc_caps,
        run_cmd_fn=lambda *a, **k: noop_cmds.append([str(x) for x in a[0]]),
    )
    assert noop_cmds == []
    assert res_no_nvcc["sageattn_available"] is False
    nvcc_present["ok"] = True

    # 2. Cache miss -> builds wheel via mocked run_cmd_fn, installs, caches locally + mirrors to Drive.
    hopper_caps = {
        "gpu_name": "NVIDIA H100 80GB HBM3",
        "vram_mib": 81920,
        "cc_major": 9,
        "cc_minor": 0,
        "torch_version": "2.9.0+cu128",
        "cuda_version": "12.8",
        "nvcc_version": "12.8",
        "triton_available": True,
        "torch_compile_supported": True,
        "fp8_fast_supported": True,
        "sageattn_version": "",
        "sageattn_available": False,
        "sageattn3_available": False,
        "flash_attn_2_available": False,
    }
    recorded_cmds: list[tuple[list[str], dict | None]] = []
    install_state = {"sage2": False, "sage3": False}

    def fake_run_cmd(*args, **kwargs):
        cmd = [str(x) for x in args[0]]
        env = kwargs.get("env")
        recorded_cmds.append((cmd, dict(env) if isinstance(env, dict) else None))
        if "git" in Path(cmd[0]).name:
            if "init" in cmd:
                target = Path(cmd[cmd.index("init") + 1])
                target.mkdir(parents=True, exist_ok=True)
                (target / ".git").mkdir(exist_ok=True)
            elif "clone" in cmd:
                target = Path(cmd[-1])
                target.mkdir(parents=True, exist_ok=True)
                (target / ".git").mkdir(exist_ok=True)
                (target / "setup.py").write_text("# stub\n", encoding="utf-8")
                (target / "sageattention3_blackwell").mkdir(exist_ok=True)
                (target / "sageattention3_blackwell" / "setup.py").write_text(
                    "# stub\n", encoding="utf-8"
                )
            elif "-C" in cmd:
                target = Path(cmd[cmd.index("-C") + 1])
                target.mkdir(parents=True, exist_ok=True)
                (target / ".git").mkdir(exist_ok=True)
                (target / "setup.py").write_text("# stub\n", encoding="utf-8")
                (target / "sageattention3_blackwell").mkdir(exist_ok=True)
                (target / "sageattention3_blackwell" / "setup.py").write_text(
                    "# stub\n", encoding="utf-8"
                )
        elif "pip" in cmd and "wheel" in cmd:
            wdir = None
            for flag in ("--wheel-dir", "-w"):
                if flag in cmd:
                    wdir = Path(cmd[cmd.index(flag) + 1])
                    break
            assert wdir is not None, f"pip wheel missing --wheel-dir: {cmd}"
            is_sage3 = any("sageattention3_blackwell" in tok for tok in cmd)
            whl_name = (
                "sageattn3-1.0.0-cp312-cp312-linux_x86_64.whl"
                if is_sage3
                else "sageattention-2.2.0-cp312-cp312-linux_x86_64.whl"
            )
            _write_dummy_whl(wdir, whl_name)
        elif "pip" in cmd and "install" in cmd:
            if any("sageattn3" in tok or "sageattention3" in tok for tok in cmd):
                install_state["sage3"] = True
            else:
                install_state["sage2"] = True
        return ""

    monkeypatch.setattr(
        comfy_packs,
        "probe_gpu_acceleration_capabilities",
        lambda gpu_info=None: {
            **hopper_caps,
            "sageattn_version": "2.2.0" if install_state["sage2"] else "",
            "sageattn_available": install_state["sage2"],
            "sageattn3_available": install_state["sage3"],
        },
    )

    task_build = ColabOneTask("comfyui_run", "sage-build")
    res_build = mgr.ensure_sageattention_wheels(
        task_build,
        gpu_caps=hopper_caps,
        allow_sage3=False,
        run_cmd_fn=fake_run_cmd,
    )
    assert res_build["sageattn_available"] is True
    assert res_build.get("installed_any") is True

    wheel_builds = [
        (cmd, env) for cmd, env in recorded_cmds if "pip" in cmd and "wheel" in cmd
    ]
    assert len(wheel_builds) == 1
    build_cmd, build_env = wheel_builds[0]
    assert "--no-build-isolation" in build_cmd
    assert "--no-deps" in build_cmd
    assert build_env is not None
    assert build_env.get("TORCH_CUDA_ARCH_LIST") == "9.0"
    assert build_env.get("EXT_PARALLEL") == "4"
    assert build_env.get("NVCC_APPEND_FLAGS") == "--threads 8"
    assert int(build_env.get("MAX_JOBS", "0")) >= 1

    pip_installs = [cmd for cmd, _ in recorded_cmds if "pip" in cmd and "install" in cmd]
    assert len(pip_installs) == 1
    assert "--no-deps" in pip_installs[0]
    assert any(tok.endswith(".whl") for tok in pip_installs[0])

    local_whls = list((rt.root / "wheel-cache").rglob("*.whl"))
    drive_whls = list((drive_ws / "wheel-cache").rglob("*.whl"))
    assert len(local_whls) >= 1, "Expected wheel cached under runtime.root/wheel-cache"
    assert len(drive_whls) >= 1, "Expected wheel mirrored under Drive wheel-cache"

    # 3. Local cache hit reuses cached .whl with ZERO git/build commands.
    recorded_cmds.clear()
    install_state["sage2"] = False
    res_local_hit = mgr.ensure_sageattention_wheels(
        ColabOneTask("comfyui_run", "sage-local-hit"),
        gpu_caps=hopper_caps,
        allow_sage3=False,
        run_cmd_fn=fake_run_cmd,
    )
    assert res_local_hit["sageattn_available"] is True
    assert not any("git" in Path(cmd[0]).name for cmd, _ in recorded_cmds)
    assert not any("pip" in cmd and "wheel" in cmd for cmd, _ in recorded_cmds)
    assert any(
        "pip" in cmd and "install" in cmd and "--no-deps" in cmd
        for cmd, _ in recorded_cmds
    )

    # 4. Drive cache hit on fresh VM (local wheel-cache wiped, Drive wheel-cache intact).
    shutil.rmtree(rt.root / "wheel-cache")
    recorded_cmds.clear()
    install_state["sage2"] = False
    res_drive_hit = mgr.ensure_sageattention_wheels(
        ColabOneTask("comfyui_run", "sage-drive-hit"),
        gpu_caps=hopper_caps,
        allow_sage3=False,
        run_cmd_fn=fake_run_cmd,
    )
    assert res_drive_hit["sageattn_available"] is True
    assert not any("git" in Path(cmd[0]).name for cmd, _ in recorded_cmds)
    assert not any("pip" in cmd and "wheel" in cmd for cmd, _ in recorded_cmds)
    assert any(
        "pip" in cmd and "install" in cmd and "--no-deps" in cmd
        for cmd, _ in recorded_cmds
    )

    # 5. Build failure degrades gracefully (NEVER raises!) and logs a clear warning.
    rt_fail = _make_mock_runtime(tmp_path / "fail_vm", total_mib=81920)
    mgr_fail = ColabOneComfyPackManager(
        rt_fail, rt_fail.root / "comfyui", rt_fail.root / "comfyui-models"
    )
    install_state["sage2"] = False
    task_fail = ColabOneTask("comfyui_run", "sage-build-fail")

    def failing_run_cmd(*args, **kwargs):
        raise RuntimeError("nvcc fatal: unsupported compiler")

    res_fail = mgr_fail.ensure_sageattention_wheels(
        task_fail,
        gpu_caps=hopper_caps,
        allow_sage3=False,
        run_cmd_fn=failing_run_cmd,
    )
    assert res_fail["sageattn_available"] is False
    assert any(entry["level"] == "warning" for entry in task_fail.logs)


def test_opt_longcat_ref_latent_omitted_seg0_and_injected_segments_1_to_n(
    runtime, tmp_path, monkeypatch
):
    from server.digital_human import (
        ColabOneDigitalHumanRunner,
        inject_longcat_ref_latent_nodes,
    )

    # 1. Direct unit test of inject_longcat_ref_latent_nodes.
    base_wf = _sample_longcat_workflow()
    snapshot_before = json.dumps(base_wf, sort_keys=True)
    injected = inject_longcat_ref_latent_nodes(base_wf, "anchor_orig.png")
    assert json.dumps(base_wf, sort_keys=True) == snapshot_before
    assert injected["colabone_ref_image"]["class_type"] == "LoadImage"
    assert injected["colabone_ref_image"]["inputs"]["image"] == "anchor_orig.png"
    assert injected["colabone_ref_encode"]["class_type"] == "WanVideoEncode"
    assert injected["colabone_ref_encode"]["inputs"]["vae"] == ["4", 0]
    assert injected["colabone_ref_encode"]["inputs"]["image"] == ["colabone_ref_image", 0]
    assert injected["colabone_ref_encode"]["inputs"]["noise_aug_strength"] == 0.0
    assert injected["colabone_ref_encode"]["inputs"]["latent_strength"] == 1.0
    assert injected["10"]["inputs"]["ref_latent"] == ["colabone_ref_encode", 0]

    # 2. Full multi-segment LongCat runner test: omitted on segment 0, injected on segments 1..N.
    monkeypatch.setattr(
        runtime,
        "gpu_info",
        lambda: {"name": "NVIDIA A100-SXM4-80GB", "total_mib": 81920, "used_mib": 1024},
    )
    monkeypatch.setattr("server.comfyui.time.sleep", lambda _: None)
    monkeypatch.setattr("server.digital_human.time.sleep", lambda _: None, raising=False)

    comfy_proc = Mock()
    comfy_proc.poll.return_value = None
    comfy_proc.pid = None
    runtime.comfy.proc = comfy_proc
    runtime.comfy.state = "ready"
    monkeypatch.setattr(runtime.comfy.packs, "check_preflight", lambda _wf: None)
    monkeypatch.setattr(runtime.comfy.packs, "ensure_pack", lambda *a, **k: False)
    if hasattr(runtime.comfy.packs, "ensure_sageattention_wheels"):
        monkeypatch.setattr(
            runtime.comfy.packs,
            "ensure_sageattention_wheels",
            lambda _task, *, gpu_caps=None, **_k: {
                **(gpu_caps or {}),
                "sageattn_available": False,
                "sageattn3_available": False,
                "installed_any": False,
            },
        )

    submitted_prompts: list[dict] = []
    fake_mp4 = b"\x00\x00\x00\x18ftypmp42longcat-ref-seg"

    def fake_post(url, **kwargs):
        if url.endswith("/upload/image"):
            fname = (kwargs.get("files") or {}).get("image", ("uploaded.bin",))[0]
            return make_http_resp({"name": fname, "subfolder": ""})
        if url.endswith("/prompt"):
            idx = len(submitted_prompts) + 1
            submitted_prompts.append(copy.deepcopy(kwargs["json"]["prompt"]))
            return make_http_resp({"prompt_id": f"ref-prompt-{idx}"})
        if url.endswith("/free") or url.endswith("/interrupt"):
            return make_http_resp({"ok": True})
        raise AssertionError(f"Unexpected POST {url}")

    def fake_get(url, **kwargs):
        if url.endswith("/system_stats"):
            return make_http_resp({"system": {"comfyui_version": "0.46.0"}})
        if url.endswith("/queue"):
            return make_http_resp({"queue_running": [], "queue_pending": []})
        if "/history/" in url:
            pid = url.rsplit("/", 1)[-1]
            return make_http_resp(
                {
                    pid: {
                        "status": {"status_str": "success"},
                        "outputs": {
                            "11": {
                                "gifs": [
                                    {
                                        "filename": f"{pid}.mp4",
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
            return make_http_resp(content=fake_mp4)
        raise AssertionError(f"Unexpected GET {url}")

    runtime.http = Mock()
    runtime.http.post.side_effect = fake_post
    runtime.http.get.side_effect = fake_get

    tail_png = make_png_bytes("orange", size=(64, 64))

    def fake_ffmpeg(*args, **kwargs):
        cmd = [str(x) for x in args[0]]
        out = Path(cmd[-1])
        out.parent.mkdir(parents=True, exist_ok=True)
        if out.suffix.lower() in (".png", ".jpg", ".jpeg"):
            out.write_bytes(tail_png)
        elif out.suffix.lower() == ".mp4":
            out.write_bytes(b"\x00\x00\x00\x18ftypmp42longcat-ref-final")
        return ""

    sr = 48000
    one_sec = b"".join(
        struct.pack("<h", int(10000 * math.sin(2.0 * math.pi * 440.0 * i / sr)))
        for i in range(sr)
    )
    wav_9s = tmp_path / "longcat_9s.wav"
    with wave.open(str(wav_9s), "wb") as wf_out:
        wf_out.setnchannels(1)
        wf_out.setsampwidth(2)
        wf_out.setframerate(sr)
        wf_out.writeframes(one_sec * 9)

    portrait = tmp_path / "anchor.png"
    portrait.write_bytes(make_png_bytes("purple", size=(64, 64)))

    runner = ColabOneDigitalHumanRunner(runtime.comfy, None, run_cmd_fn=fake_ffmpeg)
    req = WorkflowRunRequest(
        workflow_id="digital-human-longcat",
        overrides={
            "pipeline.audio_source": "audio",
            "pipeline.max_segment_seconds": 3.7,
            "pipeline.target_resolution": "1280x720",
            "pipeline.upscale_mode": "ai_2x",
        },
        inputs={"1.image": "a" * 32, "2.audio": "b" * 32},
    )
    outputs = runner.run(
        ColabOneTask("comfyui_run", "digital-human-longcat"),
        _sample_longcat_template(),
        req,
        [("1.image", "anchor.png", portrait), ("2.audio", "longcat_9s.wav", wav_9s)],
    )
    assert len(outputs) == 1
    assert len(submitted_prompts) >= 3

    # Segment 0 MUST NOT inject ref_latent nodes.
    seg0 = submitted_prompts[0]
    assert "colabone_ref_image" not in seg0
    assert "colabone_ref_encode" not in seg0
    assert "ref_latent" not in seg0["10"]["inputs"]

    # Segments 1..N MUST inject ref_latent wired to the original anchor image (not the relay tail frame).
    ref_names_seen: set[str] = set()
    for seg_prompt in submitted_prompts[1:]:
        assert "colabone_ref_image" in seg_prompt
        assert "colabone_ref_encode" in seg_prompt
        assert seg_prompt["colabone_ref_image"]["class_type"] == "LoadImage"
        assert seg_prompt["colabone_ref_encode"]["class_type"] == "WanVideoEncode"
        assert seg_prompt["colabone_ref_encode"]["inputs"]["image"] == [
            "colabone_ref_image",
            0,
        ]
        assert seg_prompt["colabone_ref_encode"]["inputs"]["vae"] == ["4", 0]
        assert seg_prompt["10"]["inputs"]["ref_latent"] == ["colabone_ref_encode", 0]
        ref_name = seg_prompt["colabone_ref_image"]["inputs"]["image"]
        assert isinstance(ref_name, str) and ref_name != ""
        assert ref_name != seg_prompt["1"]["inputs"]["image"]
        ref_names_seen.add(ref_name)
    assert len(ref_names_seen) == 1


def test_opt_lipsync_lookahead_audio_slicing_and_stage4_ffmpeg_fast_cfr(
    runtime, tmp_path, monkeypatch
):
    from server.digital_human import ColabOneDigitalHumanRunner

    monkeypatch.setattr(
        runtime,
        "gpu_info",
        lambda: {"name": "NVIDIA A100-SXM4-80GB", "total_mib": 81920, "used_mib": 1024},
    )
    monkeypatch.setattr("server.comfyui.time.sleep", lambda _: None)
    monkeypatch.setattr("server.digital_human.time.sleep", lambda _: None, raising=False)

    comfy_proc = Mock()
    comfy_proc.poll.return_value = None
    comfy_proc.pid = None
    runtime.comfy.proc = comfy_proc
    runtime.comfy.state = "ready"
    monkeypatch.setattr(runtime.comfy.packs, "check_preflight", lambda _wf: None)
    monkeypatch.setattr(runtime.comfy.packs, "ensure_pack", lambda *a, **k: False)
    if hasattr(runtime.comfy.packs, "ensure_sageattention_wheels"):
        monkeypatch.setattr(
            runtime.comfy.packs,
            "ensure_sageattention_wheels",
            lambda _task, *, gpu_caps=None, **_k: {
                **(gpu_caps or {}),
                "sageattn_available": False,
                "sageattn3_available": False,
                "installed_any": False,
            },
        )

    submitted_prompts: list[dict] = []
    uploaded_wavs_by_name: dict[str, bytes] = {}
    final_ffmpeg_cmds: list[list[str]] = []
    fake_mp4 = b"\x00\x00\x00\x18ftypmp42lipsync-seg"

    def fake_post(url, **kwargs):
        if url.endswith("/upload/image"):
            file_tuple = (kwargs.get("files") or {}).get("image")
            fname = file_tuple[0] if file_tuple else "uploaded.bin"
            raw_bytes = file_tuple[1] if file_tuple and len(file_tuple) > 1 else b""
            if str(fname).lower().endswith(".wav"):
                uploaded_wavs_by_name[str(fname)] = bytes(raw_bytes)
            return make_http_resp({"name": fname, "subfolder": ""})
        if url.endswith("/prompt"):
            idx = len(submitted_prompts) + 1
            submitted_prompts.append(copy.deepcopy(kwargs["json"]["prompt"]))
            return make_http_resp({"prompt_id": f"ls-prompt-{idx}"})
        if url.endswith("/free") or url.endswith("/interrupt"):
            return make_http_resp({"ok": True})
        raise AssertionError(f"Unexpected POST {url}")

    def fake_get(url, **kwargs):
        if url.endswith("/system_stats"):
            return make_http_resp({"system": {"comfyui_version": "0.46.0"}})
        if url.endswith("/queue"):
            return make_http_resp({"queue_running": [], "queue_pending": []})
        if "/history/" in url:
            pid = url.rsplit("/", 1)[-1]
            return make_http_resp(
                {
                    pid: {
                        "status": {"status_str": "success"},
                        "outputs": {
                            "11": {
                                "gifs": [
                                    {
                                        "filename": f"{pid}.mp4",
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
            return make_http_resp(content=fake_mp4)
        raise AssertionError(f"Unexpected GET {url}")

    runtime.http = Mock()
    runtime.http.post.side_effect = fake_post
    runtime.http.get.side_effect = fake_get

    tail_png = make_png_bytes("teal", size=(64, 64))

    def spy_ffmpeg(*args, **kwargs):
        cmd = [str(x) for x in args[0]]
        if "-filter_complex_script" in cmd:
            final_ffmpeg_cmds.append(cmd)
        out = Path(cmd[-1])
        out.parent.mkdir(parents=True, exist_ok=True)
        if out.suffix.lower() in (".png", ".jpg", ".jpeg"):
            out.write_bytes(tail_png)
        elif out.suffix.lower() == ".mp4":
            out.write_bytes(b"\x00\x00\x00\x18ftypmp42lipsync-final")
        return ""

    # 8.2s audio @ 48kHz with max_segment_seconds=3.7 yields 3 segments, with final segment
    # requiring trailing silence padding so LongCatAvatarWhisperEmbeds receives >= (num_frames + 2) frames.
    sr = 48000
    wav_8_2s = tmp_path / "lipsync_8_2s.wav"
    total_samples = int(round(8.2 * sr))
    one_sec_pcm = b"".join(
        struct.pack("<h", int(12000 * math.sin(2.0 * math.pi * 440.0 * i / sr)))
        for i in range(sr)
    )
    full_pcm = (one_sec_pcm * 9)[: total_samples * 2]
    with wave.open(str(wav_8_2s), "wb") as wf_out:
        wf_out.setnchannels(1)
        wf_out.setsampwidth(2)
        wf_out.setframerate(sr)
        wf_out.writeframes(full_pcm)

    portrait = tmp_path / "ls_portrait.png"
    portrait.write_bytes(make_png_bytes("blue", size=(64, 64)))

    runner = ColabOneDigitalHumanRunner(runtime.comfy, None, run_cmd_fn=spy_ffmpeg)
    req = WorkflowRunRequest(
        workflow_id="digital-human-longcat",
        overrides={
            "pipeline.audio_source": "audio",
            "pipeline.max_segment_seconds": 3.7,
            "pipeline.target_resolution": "1280x720",
            "pipeline.upscale_mode": "ai_2x",
        },
        inputs={"1.image": "a" * 32, "2.audio": "b" * 32},
    )
    outputs = runner.run(
        ColabOneTask("comfyui_run", "digital-human-longcat"),
        _sample_longcat_template(),
        req,
        [("1.image", "ls_portrait.png", portrait), ("2.audio", "lipsync_8_2s.wav", wav_8_2s)],
    )
    assert len(outputs) == 1
    assert len(submitted_prompts) >= 3

    for idx, prompt in enumerate(submitted_prompts):
        num_frames = int(prompt["9"]["inputs"]["num_frames"])
        aud_name = prompt["2"]["inputs"]["audio"]
        assert aud_name in uploaded_wavs_by_name, f"Segment {idx} audio not uploaded"
        with wave.open(io.BytesIO(uploaded_wavs_by_name[aud_name]), "rb") as wf_in:
            wav_sr = wf_in.getframerate()
            wav_frames = wf_in.getnframes()
            wav_raw = wf_in.readframes(wav_frames)
        wav_dur = wav_frames / float(wav_sr)
        needed_dur = (num_frames + 2) / 25.0
        assert wav_dur >= needed_dur - 0.002, (
            f"Segment {idx} WAV duration {wav_dur:.4f}s < needed (num_frames+2)/fps={needed_dur:.4f}s"
        )
        # Final segment extends past master audio end -> trailing samples must be padded silence (0).
        if idx == len(submitted_prompts) - 1:
            tail_samples = struct.unpack(f"<{min(200, len(wav_raw) // 2)}h", wav_raw[-400:])
            assert all(s == 0 for s in tail_samples), (
                "Final segment lookahead pad should be clean trailing silence"
            )

    # Verify Stage 4 FFmpeg uses -preset fast, -fps_mode cfr, and -r 25.
    assert len(final_ffmpeg_cmds) == 1
    final_cmd = final_ffmpeg_cmds[0]
    assert "-preset" in final_cmd
    assert final_cmd[final_cmd.index("-preset") + 1] == "fast"
    assert "-fps_mode" in final_cmd
    assert final_cmd[final_cmd.index("-fps_mode") + 1] == "cfr"
    assert "-r" in final_cmd
    assert final_cmd[final_cmd.index("-r") + 1] == "25"


def test_opt_longcat_runtime_acceleration_fallback_cascade_recovers_cleanly(
    runtime, tmp_path, monkeypatch
):
    from server import comfy_packs, digital_human
    from server.digital_human import ColabOneDigitalHumanRunner
    from server.runtime import ColabOneCancelledError

    blackwell_caps = {
        "gpu_name": "NVIDIA RTX PRO 6000 Blackwell",
        "vram_mib": 98304,
        "cc_major": 12,
        "cc_minor": 0,
        "torch_version": "2.9.0+cu128",
        "cuda_version": "12.8",
        "nvcc_version": "12.8",
        "triton_available": True,
        "torch_compile_supported": True,
        "fp8_fast_supported": True,
        "sageattn_version": "2.2.0",
        "sageattn_available": True,
        "sageattn3_available": True,
        "flash_attn_2_available": False,
    }
    monkeypatch.setattr(
        runtime,
        "gpu_info",
        lambda: {"name": blackwell_caps["gpu_name"], "total_mib": 98304, "used_mib": 1024},
    )
    monkeypatch.setattr(
        comfy_packs, "probe_gpu_acceleration_capabilities", lambda _g=None: dict(blackwell_caps)
    )
    monkeypatch.setattr(
        digital_human,
        "probe_gpu_acceleration_capabilities",
        lambda _g=None: dict(blackwell_caps),
        raising=False,
    )
    monkeypatch.setattr("server.comfyui.time.sleep", lambda _: None)
    monkeypatch.setattr("server.digital_human.time.sleep", lambda _: None, raising=False)

    comfy_proc = Mock()
    comfy_proc.poll.return_value = None
    comfy_proc.pid = None
    runtime.comfy.proc = comfy_proc
    runtime.comfy.state = "ready"
    monkeypatch.setattr(runtime.comfy.packs, "check_preflight", lambda _wf: None)
    monkeypatch.setattr(runtime.comfy.packs, "ensure_pack", lambda *a, **k: False)
    if hasattr(runtime.comfy.packs, "ensure_sageattention_wheels"):
        monkeypatch.setattr(
            runtime.comfy.packs,
            "ensure_sageattention_wheels",
            lambda _task, *, gpu_caps=None, **_k: {
                **blackwell_caps,
                "installed_any": False,
            },
        )
    monkeypatch.setattr(
        runtime.comfy, "_upload_input_file", lambda fname, _path: str(fname)
    )

    def fake_ffmpeg(*args, **kwargs):
        cmd = [str(x) for x in args[0]]
        out = Path(cmd[-1])
        out.parent.mkdir(parents=True, exist_ok=True)
        if out.suffix.lower() == ".mp4":
            out.write_bytes(b"\x00\x00\x00\x18ftypmp42fallback-final")
        return ""

    wav_3s = write_pcm16_wav(
        tmp_path / "short_3s.wav",
        make_sine_samples(3.0, freq_hz=440.0, amp=0.35, sample_rate=48000),
        sample_rate=48000,
    )
    portrait = tmp_path / "fb_portrait.png"
    portrait.write_bytes(make_png_bytes("navy", size=(64, 64)))
    runner = ColabOneDigitalHumanRunner(runtime.comfy, None, run_cmd_fn=fake_ffmpeg)
    req = WorkflowRunRequest(
        workflow_id="digital-human-longcat",
        overrides={
            "pipeline.audio_source": "audio",
            "pipeline.max_segment_seconds": 3.7,
            "pipeline.target_resolution": "1280x720",
            "pipeline.upscale_mode": "ai_2x",
            "pipeline.acceleration_profile": "max",
        },
        inputs={"1.image": "a" * 32, "2.audio": "b" * 32},
    )

    # Case A: Fails under torch.compile -> steps down to balanced (compile off, keeping sageattn_3 + fp8_fast).
    attempts_a: list[dict] = []

    def exec_fail_compile(task, wf, prompt, **kwargs):
        loader = prompt["13"]["inputs"]
        has_compile = "compile_args" in loader or "colabone_torch_compile" in prompt
        attempts_a.append(
            {
                "compile": has_compile,
                "attention_mode": loader.get("attention_mode"),
                "quantization": loader.get("quantization"),
            }
        )
        if has_compile:
            raise RuntimeError("torch._inductor lowering failed on custom kernel")
        return ("fb-prompt-ok", [b"\x00\x00\x00\x18ftypmp42recovered-seg"])

    monkeypatch.setattr(runtime.comfy, "_execute_single_prompt", exec_fail_compile)
    task_a = ColabOneTask("comfyui_run", "digital-human-longcat")
    outputs_a = runner.run(
        task_a,
        _sample_longcat_template(),
        req,
        [("1.image", "fb_portrait.png", portrait), ("2.audio", "short_3s.wav", wav_3s)],
    )
    assert len(outputs_a) == 1
    assert len(attempts_a) == 2
    assert attempts_a[0] == {
        "compile": True,
        "attention_mode": "sageattn_3",
        "quantization": "fp8_e4m3fn_fast",
    }
    assert attempts_a[1] == {
        "compile": False,
        "attention_mode": "sageattn_3",
        "quantization": "fp8_e4m3fn_fast",
    }
    assert any(entry["level"] == "warning" for entry in task_a.logs)

    # Case B: Full cascade (compile -> sageattn_3 -> sageattn -> compat sdpa + disabled BF16).
    attempts_b: list[dict] = []

    def exec_full_cascade(task, wf, prompt, **kwargs):
        loader = prompt["13"]["inputs"]
        has_compile = "compile_args" in loader or "colabone_torch_compile" in prompt
        attn = loader.get("attention_mode")
        quant = loader.get("quantization")
        attempts_b.append(
            {"compile": has_compile, "attention_mode": attn, "quantization": quant}
        )
        if has_compile:
            raise RuntimeError("inductor compile failed")
        if attn == "sageattn_3":
            raise RuntimeError("sageattn3 FP4 kernel unsupported")
        if attn == "sageattn":
            raise RuntimeError("sageattn2 CUDA error")
        return ("fb-compat-ok", [b"\x00\x00\x00\x18ftypmp42compat-seg"])

    monkeypatch.setattr(runtime.comfy, "_execute_single_prompt", exec_full_cascade)
    task_b = ColabOneTask("comfyui_run", "digital-human-longcat")
    outputs_b = runner.run(
        task_b,
        _sample_longcat_template(),
        req,
        [("1.image", "fb_portrait.png", portrait), ("2.audio", "short_3s.wav", wav_3s)],
    )
    assert len(outputs_b) == 1
    assert attempts_b == [
        {"compile": True, "attention_mode": "sageattn_3", "quantization": "fp8_e4m3fn_fast"},
        {"compile": False, "attention_mode": "sageattn_3", "quantization": "fp8_e4m3fn_fast"},
        {"compile": False, "attention_mode": "sageattn", "quantization": "fp8_e4m3fn_fast"},
        {"compile": False, "attention_mode": "sdpa", "quantization": "disabled"},
    ]
    assert sum(1 for entry in task_b.logs if entry["level"] == "warning") >= 3

    # Case C: Cancellation MUST propagate immediately without stepping down or retrying.
    cancel_attempts = {"count": 0}

    def exec_cancelled(task, wf, prompt, **kwargs):
        cancel_attempts["count"] += 1
        raise ColabOneCancelledError("用户取消任务")

    monkeypatch.setattr(runtime.comfy, "_execute_single_prompt", exec_cancelled)
    with pytest.raises(ColabOneCancelledError):
        runner.run(
            ColabOneTask("comfyui_run", "digital-human-longcat"),
            _sample_longcat_template(),
            req,
            [("1.image", "fb_portrait.png", portrait), ("2.audio", "short_3s.wav", wav_3s)],
        )
    assert cancel_attempts["count"] == 1


def test_opt_builtin_named_female_voices_across_all_four_tts_engines(
    tmp_path, monkeypatch
):
    import os
    import subprocess
    import sys

    from server.tts_engines import (
        COLABONE_TTS_VOICE_PRESETS,
        COLABONE_VOICE_PRESET_ANCHOR_TEXT,
        ColabOneRemoteTtsRunner,
        DEFAULT_COLABONE_VOICE_PRESET,
        resolve_colabone_voice_preset,
    )

    # 1. Verify canonical 5 named female voices and resolver semantics.
    expected_voices = {
        "shen_zhiyi": 202601,
        "lin_xiaoman": 202602,
        "su_wanqing": 202603,
        "gu_qinglan": 202604,
        "lu_mingwei": 202605,
    }
    assert DEFAULT_COLABONE_VOICE_PRESET == "shen_zhiyi"
    assert "大家好，欢迎来到今天的节目" in COLABONE_VOICE_PRESET_ANCHOR_TEXT
    assert set(expected_voices.keys()).issubset(COLABONE_TTS_VOICE_PRESETS.keys())

    catalog = ComfyCatalog.model_validate_json(
        (ROOT / "catalog" / "comfyui.json").read_text(encoding="utf-8")
    )
    engines_by_id = {e.id: e.model_dump() for e in catalog.tts_engines}

    for eid in ("voxcpm2", "qwen3_tts", "fish_s2_pro", "indextts2"):
        eng = engines_by_id[eid]
        assert resolve_colabone_voice_preset(eng, "custom") is None
        default_resolved = resolve_colabone_voice_preset(eng, "")
        assert default_resolved is not None
        assert default_resolved["id"] == "shen_zhiyi"
        assert default_resolved["seed"] == 202601
        for vid, expected_seed in expected_voices.items():
            resolved = resolve_colabone_voice_preset(eng, vid)
            assert resolved is not None
            assert resolved["id"] == vid
            assert resolved["gender"] == "female"
            assert resolved["seed"] == expected_seed
            assert str(resolved.get("prompt") or "").strip() != ""

    rt = _make_mock_runtime(tmp_path, total_mib=81920)
    captured_configs: list[dict] = []

    def capture_synth(config: dict):
        captured_configs.append(copy.deepcopy(config))
        sr = int(config.get("sample_rate") or 24000)
        for seg in config.get("segments") or []:
            write_pcm16_wav(
                Path(seg["output_wav"]),
                make_sine_samples(0.4, freq_hz=380.0, amp=0.35, sample_rate=sr),
                sample_rate=sr,
            )
        return None

    runner = ColabOneRemoteTtsRunner(rt, engines_by_id, synth_fn=capture_synth)

    # 2. VoxCPM2 runner passes preset voice_prompt + voice_seed; worker uses cfg_value=2.0 + (prompt) + seed.
    runner.synthesize(
        ColabOneTask("comfyui_run", "vox-preset"),
        engine_id="voxcpm2",
        script_text="欢迎收看今天的精彩分享。",
        work_dir=tmp_path / "work_vox",
        voice_preset="lin_xiaoman",
    )
    vox_cfg = captured_configs[-1]
    assert vox_cfg["voice_seed"] == 202602
    assert "22-year-old" in vox_cfg["voice_prompt"]

    mock_pkg_dir = tmp_path / "mock_tts_pkgs"
    mock_pkg_dir.mkdir(parents=True, exist_ok=True)
    seed_log_json = tmp_path / "torch_seeds.json"
    vox_capture_json = tmp_path / "vox_worker_cap.json"
    qwen_capture_json = tmp_path / "qwen_worker_cap.json"

    (mock_pkg_dir / "torch.py").write_text(
        (
            "import json\n"
            "from pathlib import Path\n"
            "float32 = 'float32'\n"
            "float16 = 'float16'\n"
            "bfloat16 = 'bfloat16'\n"
            f"_SEED_LOG = Path({str(seed_log_json)!r})\n"
            "def _rec(kind, val):\n"
            "    cur = json.loads(_SEED_LOG.read_text('utf-8')) if _SEED_LOG.is_file() else []\n"
            "    cur.append([kind, int(val)])\n"
            "    _SEED_LOG.write_text(json.dumps(cur), encoding='utf-8')\n"
            "def manual_seed(val):\n"
            "    _rec('cpu', val)\n"
            "class _Cuda:\n"
            "    @staticmethod\n"
            "    def is_available(): return False\n"
            "    @staticmethod\n"
            "    def is_bf16_supported(): return False\n"
            "    @staticmethod\n"
            "    def manual_seed_all(val): _rec('cuda', val)\n"
            "    @staticmethod\n"
            "    def empty_cache(): pass\n"
            "cuda = _Cuda()\n"
        ),
        encoding="utf-8",
    )
    (mock_pkg_dir / "huggingface_hub.py").write_text(
        "def snapshot_download(**kwargs):\n    return '.'\n",
        encoding="utf-8",
    )
    (mock_pkg_dir / "voxcpm.py").write_text(
        (
            "import json\n"
            "from pathlib import Path\n"
            "class VoxCPM:\n"
            "    sample_rate = 48000\n"
            "    @classmethod\n"
            "    def from_pretrained(cls, repo, **kwargs):\n"
            "        return cls()\n"
            "    def generate(self, text, prompt_wav_path=None, prompt_text=None, "
            "reference_wav_path=None, cfg_value=2.0, inference_timesteps=18, normalize=True):\n"
            f"        Path({str(vox_capture_json)!r}).write_text(json.dumps({{"
            "'text': text, 'cfg_value': cfg_value, 'inference_timesteps': inference_timesteps"
            "}), encoding='utf-8')\n"
            "        return [0.1, -0.1] * 240\n"
        ),
        encoding="utf-8",
    )
    (mock_pkg_dir / "qwen_tts.py").write_text(
        (
            "import json\n"
            "from pathlib import Path\n"
            "class Qwen3TTSModel:\n"
            "    @classmethod\n"
            "    def from_pretrained(cls, local_dir, **kwargs):\n"
            "        return cls()\n"
            "    def generate_voice_design(self, text, language='Auto', instruct=''):\n"
            f"        Path({str(qwen_capture_json)!r}).write_text(json.dumps({{"
            "'text': text, 'language': language, 'instruct': instruct"
            "}), encoding='utf-8')\n"
            "        return [[0.1, -0.1] * 240], 24000\n"
        ),
        encoding="utf-8",
    )

    sub_env = dict(os.environ)
    sub_env["PYTHONPATH"] = str(mock_pkg_dir) + (
        os.pathsep + sub_env["PYTHONPATH"] if sub_env.get("PYTHONPATH") else ""
    )

    vox_worker_py = tmp_path / "opt_worker_voxcpm2.py"
    vox_worker_py.write_text(runner.build_worker_script("voxcpm2"), encoding="utf-8")
    vox_out_wav = tmp_path / "opt_vox_seg0.wav"
    vox_cfg_file = tmp_path / "opt_vox_cfg.json"
    vox_cfg_file.write_text(
        json.dumps(
            {
                "repo": "openbmb/VoxCPM2",
                "revision": "7936080b41163874f38610f46f438e2c0757f9fd",
                "cache_dir": str(tmp_path / "cache"),
                "sample_rate": 48000,
                "ref_audio_path": "",
                "ref_text": "",
                "voice_prompt": vox_cfg["voice_prompt"],
                "voice_seed": 202602,
                "segments": [{"index": 0, "text": "你好世界", "output_wav": str(vox_out_wav)}],
            }
        ),
        encoding="utf-8",
    )
    res_vox_proc = subprocess.run(
        [sys.executable, str(vox_worker_py), "--config", str(vox_cfg_file)],
        env=sub_env,
        capture_output=True,
        text=True,
        timeout=15,
    )
    assert res_vox_proc.returncode == 0, res_vox_proc.stderr
    vox_gen = json.loads(vox_capture_json.read_text(encoding="utf-8"))
    assert vox_gen["cfg_value"] == pytest.approx(2.0)
    assert vox_gen["inference_timesteps"] == 18
    assert vox_gen["text"] == f"({vox_cfg['voice_prompt']})你好世界"
    seeds_recorded = json.loads(seed_log_json.read_text(encoding="utf-8"))
    assert ["cpu", 202602] in seeds_recorded

    # VoxCPM2 with empty voice_prompt & no ref_audio MUST still apply default fallback (prompt) prefix.
    vox_fallback_cfg = tmp_path / "opt_vox_fallback_cfg.json"
    vox_fallback_cfg.write_text(
        json.dumps(
            {
                "repo": "openbmb/VoxCPM2",
                "revision": "7936080b41163874f38610f46f438e2c0757f9fd",
                "cache_dir": str(tmp_path / "cache"),
                "sample_rate": 48000,
                "ref_audio_path": "",
                "ref_text": "",
                "voice_prompt": "",
                "segments": [{"index": 0, "text": "兜底女声测试", "output_wav": str(vox_out_wav)}],
            }
        ),
        encoding="utf-8",
    )
    res_vox_fb = subprocess.run(
        [sys.executable, str(vox_worker_py), "--config", str(vox_fallback_cfg)],
        env=sub_env,
        capture_output=True,
        text=True,
        timeout=15,
    )
    assert res_vox_fb.returncode == 0, res_vox_fb.stderr
    vox_fb_gen = json.loads(vox_capture_json.read_text(encoding="utf-8"))
    assert vox_fb_gen["text"].startswith("(") and vox_fb_gen["text"].endswith(")兜底女声测试")

    # 3. Qwen3-TTS runner & worker use rich Chinese instruct + voice_seed.
    runner.synthesize(
        ColabOneTask("comfyui_run", "qwen-preset"),
        engine_id="qwen3_tts",
        script_text="夜深了，来听一段温暖治愈的故事。",
        work_dir=tmp_path / "work_qwen",
        voice_preset="su_wanqing",
    )
    qwen_cfg = captured_configs[-1]
    assert qwen_cfg["voice_seed"] == 202603
    assert "温柔治愈" in qwen_cfg["voice_prompt"]

    seed_log_json.unlink(missing_ok=True)
    qwen_worker_py = tmp_path / "opt_worker_qwen3.py"
    qwen_worker_py.write_text(runner.build_worker_script("qwen3_tts"), encoding="utf-8")
    qwen_out_wav = tmp_path / "opt_qwen_seg0.wav"
    qwen_cfg_file = tmp_path / "opt_qwen_cfg.json"
    qwen_cfg_file.write_text(
        json.dumps(
            {
                "repo": "Qwen/Qwen3-TTS-12Hz-1.7B-Base",
                "revision": "fd4b254389122332181a7c3db7f27e918eec64e3",
                "aux_repo": "Qwen/Qwen3-TTS-12Hz-1.7B-VoiceDesign",
                "aux_revision": "5ecdb67327fd37bb2e042aab12ff7391903235d3",
                "cache_dir": str(tmp_path / "cache"),
                "sample_rate": 24000,
                "ref_audio_path": "",
                "ref_text": "",
                "voice_prompt": qwen_cfg["voice_prompt"],
                "voice_seed": 202603,
                "segments": [{"index": 0, "text": "晚安好梦", "output_wav": str(qwen_out_wav)}],
            }
        ),
        encoding="utf-8",
    )
    res_qwen_proc = subprocess.run(
        [sys.executable, str(qwen_worker_py), "--config", str(qwen_cfg_file)],
        env=sub_env,
        capture_output=True,
        text=True,
        timeout=15,
    )
    assert res_qwen_proc.returncode == 0, res_qwen_proc.stderr
    qwen_gen = json.loads(qwen_capture_json.read_text(encoding="utf-8"))
    assert "温柔治愈" in qwen_gen["instruct"]
    qwen_seeds = json.loads(seed_log_json.read_text(encoding="utf-8"))
    assert ["cpu", 202603] in qwen_seeds

    # 4. Reference-driven engines (IndexTTS-2 & Fish S2 Pro): built-in preset anchor works cleanly,
    # user ref_audio_path takes priority, and IndexTTS-2 still rejects "custom" without ref_audio_path.
    presets_cache_dir = runner.engines_dir / "_voice_presets"
    presets_cache_dir.mkdir(parents=True, exist_ok=True)
    for preset_id in ("gu_qinglan", "lu_mingwei"):
        write_pcm16_wav(
            presets_cache_dir / f"{preset_id}.wav",
            make_sine_samples(0.6, freq_hz=330.0, amp=0.35, sample_rate=48000),
            sample_rate=48000,
        )
        (presets_cache_dir / f"{preset_id}.txt").write_text(
            COLABONE_VOICE_PRESET_ANCHOR_TEXT, encoding="utf-8"
        )
        preset_spec = resolve_colabone_voice_preset(engines_by_id["indextts2"], preset_id)
        assert preset_spec is not None
        _, _, _, expected_meta = runner._preset_anchor_fingerprint(preset_spec)
        (presets_cache_dir / f"{preset_id}.meta.json").write_text(
            json.dumps(expected_meta, ensure_ascii=False), encoding="utf-8"
        )

    res_idx_preset = runner.synthesize(
        ColabOneTask("comfyui_run", "indextts2-preset"),
        engine_id="indextts2",
        script_text="今天我们从三个关键指标分析季度增长。",
        work_dir=tmp_path / "work_indextts2_preset",
        voice_preset="gu_qinglan",
    )
    assert Path(res_idx_preset["audio_path"]).is_file()
    idx_cfg = captured_configs[-1]
    assert Path(idx_cfg["ref_audio_path"]).name == "gu_qinglan.wav"

    # IndexTTS-2 MUST still reject voice_preset="custom" when ref_audio_path is missing.
    with pytest.raises(ValueError, match=r"IndexTTS2.*需要上传音色参考音频"):
        runner.synthesize(
            ColabOneTask("comfyui_run", "indextts2-custom-missing"),
            engine_id="indextts2",
            script_text="缺少自定义音色参考音频应该报错。",
            work_dir=tmp_path / "work_indextts2_custom",
            voice_preset="custom",
        )

    # User-uploaded ref_audio_path overrides built-in voice_preset.
    user_ref_wav = write_pcm16_wav(
        tmp_path / "user_custom_voice.wav",
        make_sine_samples(0.5, freq_hz=280.0, amp=0.35, sample_rate=24000),
        sample_rate=24000,
    )
    runner.synthesize(
        ColabOneTask("comfyui_run", "indextts2-user-override"),
        engine_id="indextts2",
        script_text="优先克隆用户自己上传的参考音色。",
        work_dir=tmp_path / "work_indextts2_override",
        ref_audio_path=user_ref_wav,
        voice_preset="gu_qinglan",
    )
    assert Path(captured_configs[-1]["ref_audio_path"]) == user_ref_wav

    # Fish Speech S2 Pro resolves cached female preset anchor + anchor text cleanly.
    res_fish_preset = runner.synthesize(
        ColabOneTask("comfyui_run", "fish-preset"),
        engine_id="fish_s2_pro",
        script_text="以下是今天的晚间重点新闻播报。",
        work_dir=tmp_path / "work_fish_preset",
        voice_preset="lu_mingwei",
    )
    assert Path(res_fish_preset["audio_path"]).is_file()
    fish_cfg = captured_configs[-1]
    assert Path(fish_cfg["ref_audio_path"]).name == "lu_mingwei.wav"
    assert fish_cfg["ref_text"] == COLABONE_VOICE_PRESET_ANCHOR_TEXT


def test_opt_audit_regressions_nvidia_smi_probe_dynamic_compile_ffmpeg_cfr_and_preflight(
    tmp_path: Path, monkeypatch
):
    """Regression suite for audit fixes: zero-torch GPU probe, dynamic compile, FFmpeg 4.4 CFR fallback,
    SageAttention Blackwell gates + restart flag clearing, voice preset whitespace validation, and
    IndexTTS2 unknown-preset preflight rejection before VRAM unload."""
    import importlib.metadata
    import importlib.util
    import shutil
    import subprocess
    import sys
    from types import SimpleNamespace

    from pydantic import ValidationError

    from common.comfyui import ComfyTtsVoicePreset, WorkflowRunRequest
    from server import comfy_packs
    from server.comfy_packs import ColabOneComfyPackManager, probe_gpu_acceleration_capabilities
    from server.digital_human import (
        ColabOneDigitalHumanRunner,
        _FFMPEG_CFR_CACHE,
        _resolve_ffmpeg_cfr_args,
        apply_longcat_acceleration,
    )
    from server.runtime import ColabOneTask

    # 1. ComfyTtsVoicePreset strips and rejects whitespace-only names (Go/Python parity).
    with pytest.raises(ValidationError):
        ComfyTtsVoicePreset(id="ws_only", name="   ")
    trimmed = ComfyTtsVoicePreset(id="ok_id", name="  沈知意  ")
    assert trimmed.name == "沈知意"

    # 2. probe_gpu_acceleration_capabilities must NOT cold-import torch when absent from sys.modules;
    #    it probes nvidia-smi + importlib.metadata cleanly with zero CUDA context allocation.
    orig_find_spec = importlib.util.find_spec
    orig_meta_version = importlib.metadata.version
    orig_which = shutil.which
    orig_sub_run = subprocess.run

    def fake_which(cmd, *a, **k):
        if cmd == "nvidia-smi":
            return "/usr/bin/nvidia-smi"
        if cmd == "nvcc":
            return "/usr/local/cuda/bin/nvcc"
        return orig_which(cmd, *a, **k)

    def fake_run(cmd, *a, **k):
        exe = Path(str(cmd[0])).name if cmd else ""
        if exe == "nvidia-smi":
            return subprocess.CompletedProcess(
                cmd, 0, stdout="NVIDIA RTX PRO 6000 Blackwell, 98304, 12.0\n", stderr=""
            )
        if exe == "nvcc":
            return subprocess.CompletedProcess(
                cmd, 0, stdout="Cuda compilation tools, release 12.8, V12.8.93\n", stderr=""
            )
        return orig_sub_run(cmd, *a, **k)

    with monkeypatch.context() as mp:
        mp.delitem(sys.modules, "torch", raising=False)
        mp.setattr(shutil, "which", fake_which)
        mp.setattr(subprocess, "run", fake_run)
        mp.setattr(
            importlib.metadata,
            "version",
            lambda name: "2.8.0+cu128"
            if name.lower() == "torch"
            else ("2.2.0" if name.lower() == "sageattention" else orig_meta_version(name)),
        )
        mp.setattr(
            importlib.util,
            "find_spec",
            lambda name, package=None: object()
            if name in ("triton", "sageattention", "sageattn3")
            else orig_find_spec(name, package),
        )
        caps = probe_gpu_acceleration_capabilities({})
        assert "torch" not in sys.modules
        assert caps["gpu_name"] == "NVIDIA RTX PRO 6000 Blackwell"
        assert caps["vram_mib"] == 98304
        assert (caps["cc_major"], caps["cc_minor"]) == (12, 0)
        assert caps["torch_version"] == "2.8.0+cu128"
        assert caps["cuda_version"] == "12.8"
        assert caps["fp8_fast_supported"] is True
        assert caps["torch_compile_supported"] is True
        assert caps["sageattn_available"] is True
        assert caps["sageattn3_available"] is True

    # 3. apply_longcat_acceleration enables dynamic=True in WanVideoTorchCompileSettings
    #    so variable-length silence-valley segments compile once without per-segment recompilation.
    real_cat = ComfyCatalog.model_validate(
        json.loads((Path(__file__).resolve().parents[1] / "catalog" / "comfyui.json").read_text(encoding="utf-8"))
    )
    longcat_wf = next(t for t in real_cat.templates if t.id == "digital-human-longcat").model_dump()
    patched_wf, summary = apply_longcat_acceleration(
        _sample_longcat_workflow(),
        profile="max",
        gpu_caps={
            "vram_mib": 98304,
            "fp8_fast_supported": True,
            "torch_compile_supported": True,
            "sageattn_available": True,
            "sageattn3_available": True,
        },
    )
    assert summary["torch_compile"] is True
    assert patched_wf["colabone_torch_compile"]["inputs"]["dynamic"] is True

    # 4. _resolve_ffmpeg_cfr_args probes FFmpeg capability cleanly: -fps_mode on >=5.1, -vsync on 4.4.
    _FFMPEG_CFR_CACHE.clear()
    with monkeypatch.context() as mp:
        mp.setattr(
            subprocess,
            "run",
            lambda cmd, *a, **k: subprocess.CompletedProcess(
                cmd, 0, stdout="-fps_mode[:stream_spec]  set framerate mode", stderr=""
            ),
        )
        assert _resolve_ffmpeg_cfr_args("/fake/ffmpeg-6") == ["-fps_mode", "cfr"]
    with monkeypatch.context() as mp:
        mp.setattr(
            subprocess,
            "run",
            lambda cmd, *a, **k: subprocess.CompletedProcess(
                cmd, 0, stdout="-vsync parameter  video sync method", stderr=""
            ),
        )
        assert _resolve_ffmpeg_cfr_args("/fake/ffmpeg-4.4") == ["-vsync", "cfr"]
    _FFMPEG_CFR_CACHE.clear()

    # 5. ensure_sageattention_wheels skips Blackwell sage3 when sage2 failed OR when torch < 2.8,
    #    and clear_pending_pip_restart resets the restart marker cleanly.
    rt = SimpleNamespace(
        root=tmp_path / "rt_sage",
        gpu_info=lambda: {"name": "Blackwell", "total_mib": 98304},
        stopping=threading.Event(),
        drive=None,
    )
    rt.root.mkdir(parents=True, exist_ok=True)
    built_targets: list[str] = []

    def fake_build_cmd(args, task, log, **kwargs):
        if "wheel" in args:
            target = str(args[-1])
            built_targets.append(target)
            if "sageattention3_blackwell" not in target:
                raise RuntimeError("simulated sage2 build failure")

    packs = ColabOneComfyPackManager(
        rt,
        install_dir=tmp_path / "comfy",
        models_dir=tmp_path / "models",
        run_cmd_fn=fake_build_cmd,
    )
    res_fail_sage2 = packs.ensure_sageattention_wheels(
        ColabOneTask("comfyui_run", "sage2-fail"),
        gpu_caps={
            "cc_major": 12,
            "cc_minor": 0,
            "torch_version": "2.8.0+cu128",
            "cuda_version": "12.8",
            "nvcc_version": "12.8",
            "sageattn_available": False,
            "sageattn3_available": False,
        },
        allow_sage3=True,
    )
    assert res_fail_sage2["installed_any"] is False
    assert not any("sageattention3_blackwell" in t for t in built_targets)

    # Also skip sage3 source build when torch < 2.8 even if sage2 is already available.
    built_targets.clear()
    task_torch27 = ColabOneTask("comfyui_run", "sage3-torch27")
    res_torch27 = packs.ensure_sageattention_wheels(
        task_torch27,
        gpu_caps={
            "cc_major": 12,
            "cc_minor": 0,
            "torch_version": "2.7.1+cu128",
            "cuda_version": "12.8",
            "nvcc_version": "12.8",
            "sageattn_available": True,
            "sageattn3_available": False,
        },
        allow_sage3=True,
    )
    assert res_torch27["installed_any"] is False
    assert built_targets == []
    assert any("低于 2.8" in str(entry.get("message", "")) for entry in task_torch27.logs)

    packs._pending_pip_restart = True
    packs.clear_pending_pip_restart()
    assert packs._pending_pip_restart is False

    # 6. Digital human preflight rejects unknown voice_preset for indextts2 BEFORE unloading LLM/VRAM.
    unload_called = {"llm": False, "comfy": False}
    fake_rt = SimpleNamespace(
        root=tmp_path / "rt_preflight",
        loaded="qwen3",
        _stop_engine=lambda: unload_called.__setitem__("llm", True),
        stopping=threading.Event(),
        gpu_info=lambda: {"name": "Blackwell", "total_mib": 98304},
    )
    fake_mgr = SimpleNamespace(
        runtime=fake_rt,
        packs=None,
        is_running=lambda: True,
        free_memory=lambda unload_models=True: unload_called.__setitem__("comfy", True),
    )
    fake_tts = SimpleNamespace(
        get_engine=lambda eid: {"id": eid, "voice_presets": []},
        synthesize=lambda *a, **k: pytest.fail("synthesize must not run on invalid preflight"),
    )
    anchor_png = tmp_path / "anchor.png"
    anchor_png.write_bytes(b"\x89PNG\r\n\x1a\n" + b"\x00" * 32)
    runner = ColabOneDigitalHumanRunner(fake_mgr, fake_tts, run_cmd_fn=lambda *a, **k: None)
    with pytest.raises(ValueError, match="IndexTTS2 需要上传音色参考音频"):
        runner.run(
            ColabOneTask("comfyui_run", "bad-preset"),
            longcat_wf,
            WorkflowRunRequest(
                workflow_id="digital-human-longcat",
                overrides={
                    "pipeline.audio_source": "tts",
                    "pipeline.tts_engine": "indextts2",
                    "pipeline.voice_preset": "nonexistent_voice_id",
                    "pipeline.script_text": "测试预检拦截。",
                }
            ),
            [("1.image", "anchor.png", anchor_png)],
        )
    assert unload_called == {"llm": False, "comfy": False}

    # 7. Round-2 audit regressions: RAM-aware NVCC concurrency, sageattention3_blackwell wheel
    #    matching, worker script AST + random/numpy seeding, and VRAM cleanup before fallback retry.
    import os as _os

    from server.comfy_packs import _sageattention_build_env
    from server.tts_engines import ColabOneRemoteTtsRunner

    with monkeypatch.context() as mp:
        mp.setattr(comfy_packs, "_detect_total_ram_gib", lambda: 16.0)
        mp.setattr(_os, "cpu_count", lambda: 12)
        small_ram_env = _sageattention_build_env(8, 0)
        assert small_ram_env["TORCH_CUDA_ARCH_LIST"] == "8.0"
        assert small_ram_env["EXT_PARALLEL"] == "4"
        assert small_ram_env["NVCC_APPEND_FLAGS"] == "--threads 8"
        assert small_ram_env["MAX_JOBS"] == "4"

        mp.setattr(comfy_packs, "_detect_total_ram_gib", lambda: 51.0)
        mid_ram_env = _sageattention_build_env(8, 9)
        assert mid_ram_env["MAX_JOBS"] == "6"

        mp.setattr(comfy_packs, "_detect_total_ram_gib", lambda: 96.0)
        hi_ram_env = _sageattention_build_env(12, 0)
        assert hi_ram_env["TORCH_CUDA_ARCH_LIST"] == "12.0"
        assert hi_ram_env["MAX_JOBS"] == "12"

    # sageattention3_blackwell-*.whl matches cached wheel lookup on Blackwell.
    py_tag = f"{sys.version_info.major}{sys.version_info.minor}"
    bw_cache_dir = (
        rt.root
        / "wheel-cache"
        / comfy_packs._sanitize_cache_key(
            f"sageattention-{comfy_packs.SAGEATTN_COMMIT[:10]}-py{py_tag}-torch2.8.0-cu12.8-sm120"
        )
    )
    bw_cache_dir.mkdir(parents=True, exist_ok=True)
    bw_whl = bw_cache_dir / f"sageattention3_blackwell-1.0.0-cp{py_tag}-cp{py_tag}-linux_x86_64.whl"
    bw_whl.write_bytes(b"wheel-bytes")
    installed_wheels: list[str] = []
    packs_bw = ColabOneComfyPackManager(
        rt,
        install_dir=tmp_path / "comfy_bw",
        models_dir=tmp_path / "models_bw",
        run_cmd_fn=lambda args, task, log, **kw: installed_wheels.append(str(args[-1]))
        if "install" in args
        else None,
    )
    res_bw_whl = packs_bw.ensure_sageattention_wheels(
        ColabOneTask("comfyui_run", "bw-whl-name"),
        gpu_caps={
            "cc_major": 12,
            "cc_minor": 0,
            "torch_version": "2.8.0",
            "cuda_version": "12.8",
            "nvcc_version": "12.8",
            "sageattn_available": True,
            "sageattn3_available": False,
        },
        allow_sage3=True,
    )
    assert res_bw_whl["installed_any"] is True
    assert res_bw_whl["sageattn3_available"] is True
    assert any("sageattention3_blackwell-" in p for p in installed_wheels)

    # All 4 worker scripts remain syntactically valid AST and VoxCPM2/Qwen3 seed random+numpy+torch.
    tts_check = ColabOneRemoteTtsRunner(rt, {e.id: e.model_dump() for e in real_cat.tts_engines})
    for eid in ("voxcpm2", "qwen3_tts", "fish_s2_pro", "indextts2"):
        script = tts_check.build_worker_script(eid)
        ast.parse(script)
        if eid in ("voxcpm2", "qwen3_tts"):
            assert "_py_random.seed(voice_seed)" in script
            assert "_np.random.seed(voice_seed" in script
            assert "torch.manual_seed(voice_seed)" in script
        if eid == "voxcpm2":
            assert "_sanitize_voxcpm_prompt" in script


def test_comfyui_ws_execution_error_aborts_immediately_and_env_blocks_tf(
    runtime, tmp_path, monkeypatch
):
    """ComfyUI aborts immediately on WS execution_error (no 10-poll delay) and launches with PyTorch-only env."""
    import time

    from server import comfyui as comfyui_mod
    from server.comfyui import ColabOneComfyManager
    from server.runtime import ColabOneTask

    mgr = ColabOneComfyManager(runtime)
    mgr.state = "ready"

    class _FakeResp:
        def __init__(self, status_code: int, payload: Any):
            self.status_code = status_code
            self._payload = payload

        def json(self):
            return self._payload

        def raise_for_status(self):
            if self.status_code >= 400:
                raise RuntimeError(f"HTTP {self.status_code}")

    class _FakeHttp:
        def post(self, url, json=None, timeout=30):
            if url.endswith("/prompt"):
                return _FakeResp(200, {"prompt_id": "pid-err-fast-1"})
            return _FakeResp(200, {})

        def get(self, url, timeout=15):
            if "/history/" in url:
                return _FakeResp(200, {})
            if url.endswith("/queue"):
                return _FakeResp(200, {"queue_running": [], "queue_pending": []})
            return _FakeResp(200, {})

    runtime.http = _FakeHttp()

    class _ImmediateErrorWsMonitor:
        def __init__(self, host, port, client_id, *, on_error=None, **kw):
            self._on_error = on_error
            self.connected = True

        def start(self):
            return True

        def set_prompt_id(self, pid):
            if callable(self._on_error):
                self._on_error("WanVideoSampler: CUDA out of memory")

        def stop(self):
            self.connected = False

    monkeypatch.setattr(comfyui_mod, "ColabOneComfyWsMonitor", _ImmediateErrorWsMonitor)
    task = ColabOneTask("comfyui_run", "ws-fast-abort")
    wf = {"id": "wf-err", "name": "Fast Abort WF"}
    t0 = time.monotonic()
    with pytest.raises(RuntimeError, match="CUDA out of memory"):
        mgr._execute_single_prompt(
            task,
            wf,
            {"1": {"class_type": "WanVideoSampler", "inputs": {}}},
        )
    elapsed = time.monotonic() - t0
    assert elapsed < 3.0, f"Expected immediate WS error abort, took {elapsed:.2f}s"

    # Also verify start_server passes USE_TF=0 / USE_JAX=0 / TRANSFORMERS_NO_TF=1.
    captured_env: dict[str, str] = {}

    class _FakeProc:
        def poll(self):
            return None

    def _fake_popen(args, *, env=None, **kw):
        captured_env.update(env or {})
        return _FakeProc()

    mgr2 = ColabOneComfyManager(runtime)
    mgr2.install_dir.mkdir(parents=True, exist_ok=True)
    (mgr2.install_dir / "main.py").write_text("print('ok')\n", encoding="utf-8")
    monkeypatch.setattr(runtime, "gpu_info", lambda: {"total_mib": 16000})
    monkeypatch.setattr(comfyui_mod.subprocess, "Popen", _fake_popen)
    monkeypatch.setattr(mgr2, "prepare", lambda task: None)
    runtime.http = type(
        "_ReadyHttp",
        (),
        {"get": lambda self, url, timeout=2: _FakeResp(200, {"system": {}})},
    )()
    mgr2.start_server(ColabOneTask("comfyui_start", "env-check"))
    for k, expected in (
        ("USE_TORCH", "1"),
        ("USE_TF", "0"),
        ("USE_FLAX", "0"),
        ("USE_JAX", "0"),
        ("TRANSFORMERS_NO_TF", "1"),
        ("TRANSFORMERS_NO_FLAX", "1"),
        ("TRANSFORMERS_NO_JAX", "1"),
        ("DISABLE_TENSORFLOW", "1"),
    ):
        assert captured_env.get(k) == expected






