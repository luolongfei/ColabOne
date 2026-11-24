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
        ("digital-human-longcat", "longcat_avatar"),
    ):
        tmpl = templates_by_id[tid]
        assert tmpl.source == "builtin"
        assert tmpl.pipeline is not None
        assert tmpl.pipeline.kind == "digital_human"
        assert tmpl.pipeline.video_backend == expected_backend
        assert tmpl.min_vram_mib > 0
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

    # 2. ComfyTtsEngineSpec
    base_engine = {
        "id": "demo_tts",
        "name": "Demo TTS",
        "repo": "org/demo-tts",
        "revision": "a" * 40,
    }
    assert ComfyTtsEngineSpec.model_validate(base_engine).commercial_ok is True
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
    for bad_pipe in (
        {"kind": "unknown_pipeline"},
        {"video_backend": "unsupported_backend"},
        {"upscale_mode": "ai_8x"},
        {"max_segment_seconds": 4.0},
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
                match="IndexTTS2 需要上传音色参考音频才能进行语音合成",
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

    # LongCat-Avatar: native 720p+ (1280x768 / 768x1280), smaller (<720p) falls back to 480p
    assert compute_base_dimensions("longcat_avatar", 1280, 720) == (1280, 768)
    assert compute_base_dimensions("longcat_avatar", 1920, 1080) == (1280, 768)
    assert compute_base_dimensions("longcat_avatar", 720, 1280) == (768, 1280)
    assert compute_base_dimensions("longcat_avatar", 1080, 1920) == (768, 1280)
    assert compute_base_dimensions("longcat_avatar", 832, 480) == (832, 480)
    assert compute_base_dimensions("longcat_avatar", 480, 832) == (480, 832)


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

    # Verify LongCat-Avatar 1.5 + Round-2 catalog contract.
    assert longcat.workflow["8"]["class_type"] == "WhisperModelLoader"
    assert (
        longcat.workflow["8"]["inputs"]["model"]
        == "whisper_large_v3_encoder_fp16.safetensors"
    )
    assert longcat.workflow["9"]["class_type"] == "LongCatAvatarWhisperEmbeds"
    assert longcat.workflow["9"]["inputs"]["fps"] == 25.0
    assert longcat.workflow["9"]["inputs"]["normalize_loudness"] is False
    assert longcat.workflow["12"]["inputs"]["merge_loras"] is True
    assert longcat.workflow["16"]["inputs"]["enable_vae_tiling"] is True
    model_targets = {
        f"{m.target_dir}/{PurePosixPath(m.path).name}" for m in longcat.models
    }
    assert "audio_encoders/whisper_large_v3_encoder_fp16.safetensors" in model_targets
    assert longcat.pipeline is not None
    assert longcat.pipeline.video_backend == "longcat_avatar"
    assert longcat.pipeline.output_fps == 25
    assert longcat.pipeline.upscale_mode == "lanczos"
    longcat_upscale_field = next(
        f for f in longcat.fields if f.key == "pipeline.upscale_mode"
    )
    assert longcat_upscale_field.default == "lanczos"
    assert len(longcat.node_patches) == 1
    assert longcat.node_patches[0].node_name == "ComfyUI-WanVideoWrapper"
    assert longcat.node_patches[0].kind == "wav2vec2_hidden_states_hook"
    assert compute_pack_content_signature(
        longcat.model_dump()
    ) == compute_pack_content_signature(infinitetalk.model_dump())
    assert infinitetalk.workflow["9"]["inputs"]["normalize_loudness"] is False
    assert infinitetalk.pipeline is not None
    assert infinitetalk.pipeline.max_segment_seconds == 20.0
    assert tts_by_id["fish_s2_pro"].min_vram_mib == 20480
    assert tts_by_id["indextts2"].sample_rate == 22050
    assert "CC-BY-4.0" in longcat.license
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
    outputs = runner.run(
        task,
        longcat.model_dump(),
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
    assert len(engines) == 4
    by_id = {e["id"]: e for e in engines}
    assert set(by_id.keys()) == {"voxcpm2", "qwen3_tts", "fish_s2_pro", "indextts2"}
    assert by_id["voxcpm2"]["commercial_ok"] is True
    assert by_id["voxcpm2"]["sample_rate"] == 48000
    assert by_id["qwen3_tts"]["commercial_ok"] is True
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


