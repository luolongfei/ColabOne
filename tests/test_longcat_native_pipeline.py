"""Comprehensive 100% offline unit & integration test suite for the 96 GB
LongCat-Video-Avatar 1.6 Native Flagship + SeedVR2 7B / FlashVSR+ pipeline.

Covers:
1. Real `catalog/comfyui.json` validation for `digital-human-longcat`
   (`longcat_avatar_native`) and `digital-human-infinitetalk` P0 tuning.
2. `server/avatar_worker_longcat16.py` pure-Python planning, vocal separation,
   sampling resolution, attention backend selection, FFV1 command, progress
   reporting, and speaker normalization helpers.
3. `server/vsr_worker_diffusion.py` pure-Python model file resolution, short-side
   target math, SeedVR2 4k+1 batch alignment & chunk planning, VRAM-tiered CLI
   command generation, FlashVSR+ scale factor, and lossless cover-crop command.
4. `server/avatar_engines.py` allow-pattern selectors, progress line parser,
   VRAM/disk preflight with cached weight credits, mocked engine setup, and
   live offline subprocess streaming progress translation + cancellation.
5. `server/digital_human.py` & `server/comfyui.py` 2K resolution parsing, native
   720p base dimensions, preset matrix resolution, full 4-stage mocked E2E
   `ColabOneDigitalHumanRunner.run(...)` without ComfyUI, and
   `ColabOneComfyManager.ensure_template_ready` delegation.

All tests run strictly offline with zero live network, Hugging Face, Google,
or GPU access.
"""

from __future__ import annotations

import io
import json
import math
import os
import stat
import struct
import sys
import threading
import time
import wave
from collections import namedtuple
from pathlib import Path
from typing import Any
from unittest.mock import Mock

import pytest
from PIL import Image

from common.comfyui import ComfyCatalog, WorkflowRunRequest
from common.files import atomic_json
from server.avatar_engines import (
    COLABONE_FLASHVSR_COMMIT,
    COLABONE_FLASHVSR_HF_REPO,
    COLABONE_FLASHVSR_HF_REV,
    COLABONE_LONGCAT16_AVATAR_HF_REPO,
    COLABONE_LONGCAT16_AVATAR_HF_REV,
    COLABONE_LONGCAT16_BASE_HF_REPO,
    COLABONE_LONGCAT16_BASE_HF_REV,
    COLABONE_LONGCAT16_COMMIT,
    COLABONE_SEEDVR2_COMMIT,
    COLABONE_SEEDVR2_HF_REPO,
    COLABONE_SEEDVR2_HF_REV,
    ColabOneAvatarEngineManager,
    longcat16_avatar_allow_patterns,
    longcat16_base_allow_patterns,
    parse_colabone_progress_line,
    vsr_engine_allow_patterns,
)
from server.avatar_worker_longcat16 import (
    COLABONE_PROGRESS_PREFIX,
    build_lossless_ffv1_writer_cmd,
    emit_colabone_progress,
    normalize_longcat16_speakers,
    plan_longcat16_segments,
    resolve_longcat16_attention_flags,
    resolve_longcat16_sampling_params,
    should_run_vocal_separation,
)
from server.digital_human import (
    COLABONE_LONGCAT_NATIVE_DEFAULT_NEGATIVE_PROMPT,
    COLABONE_LONGCAT_NATIVE_FRAMING_PROMPTS,
    ColabOneDigitalHumanRunner,
    compute_base_dimensions,
    parse_target_resolution,
    prepare_anchor_image,
    resolve_longcat_native_preset,
)
from server.runtime import ColabOneCancelledError, ColabOneTask
from server.vsr_worker_diffusion import (
    COLABONE_VSR_ENGINE_FILES,
    align_seedvr2_batch_size,
    build_cover_crop_lossless_cmd,
    build_flashvsr_scale_factor,
    build_seedvr2_cli_command,
    compute_seedvr2_chunk_plan,
    compute_seedvr2_streaming_chunk_size,
    compute_upscale_short_side,
    resolve_vsr_model_files,
)

ROOT = Path(__file__).resolve().parents[1]
_DiskUsage = namedtuple("DiskUsage", ["total", "used", "free"])


def _make_png_bytes(color: str = "navy", size: tuple[int, int] = (96, 96)) -> bytes:
    buf = io.BytesIO()
    Image.new("RGB", size, color).save(buf, format="PNG")
    return buf.getvalue()


def _write_pcm16_wav(
    path: Path,
    duration_sec: float = 4.0,
    sample_rate: int = 48000,
    freq_hz: float = 440.0,
) -> Path:
    path.parent.mkdir(parents=True, exist_ok=True)
    one_sec_samples = [
        int(round(12000.0 * math.sin(2.0 * math.pi * freq_hz * i / sample_rate)))
        for i in range(sample_rate)
    ]
    one_sec_bytes = struct.pack(f"<{len(one_sec_samples)}h", *one_sec_samples)
    total_frames = int(round(duration_sec * sample_rate))
    full_secs, rem = divmod(total_frames, sample_rate)
    pcm = (one_sec_bytes * full_secs) + one_sec_bytes[: rem * 2]
    with wave.open(str(path), "wb") as wf:
        wf.setnchannels(1)
        wf.setsampwidth(2)
        wf.setframerate(sample_rate)
        wf.writeframes(pcm)
    return path


def _load_real_comfy_catalog() -> ComfyCatalog:
    raw_text = (ROOT / "catalog" / "comfyui.json").read_text(encoding="utf-8")
    return ComfyCatalog.model_validate_json(raw_text)


def _create_python_wrapper(wrapper_path: Path, target_script: Path) -> Path:
    """Create a cross-platform executable wrapper invoking target_script with sys.executable."""
    wrapper_path.parent.mkdir(parents=True, exist_ok=True)
    if sys.platform == "win32":
        cmd_path = wrapper_path.with_suffix(".cmd")
        cmd_path.write_text(
            f'@echo off\r\n"{sys.executable}" "{target_script}" %*\r\n',
            encoding="utf-8",
        )
        return cmd_path
    wrapper_path.write_text(
        f'#!/bin/sh\nexec "{sys.executable}" "{target_script}" "$@"\n',
        encoding="utf-8",
    )
    wrapper_path.chmod(wrapper_path.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    return wrapper_path


# ---------------------------------------------------------------------------
# 1. Real catalog/comfyui.json validation
# ---------------------------------------------------------------------------


def test_real_catalog_longcat_native_flagship_and_infinitetalk_p0_contract():
    catalog = _load_real_comfy_catalog()
    templates_by_id = {t.id: t for t in catalog.templates}
    assert "digital-human-longcat" in templates_by_id
    assert "digital-human-infinitetalk" in templates_by_id

    longcat = templates_by_id["digital-human-longcat"]
    assert longcat.source == "builtin"
    assert longcat.pipeline is not None
    assert longcat.pipeline.kind == "digital_human"
    assert longcat.pipeline.video_backend == "longcat_avatar_native"
    assert longcat.commercial_ok is False
    assert longcat.license == "MIT / Apache-2.0"
    assert longcat.license_notice.strip() != ""
    assert "非商用" in longcat.license_notice
    assert longcat.min_vram_mib == 49152
    assert longcat.min_disk_mib == 79872
    assert longcat.custom_nodes == []
    assert longcat.models == []
    assert longcat.node_patches == []
    assert longcat.pip_packages == []
    assert longcat.node_count == 2
    assert set(longcat.workflow.keys()) == {"1", "2"}
    assert longcat.workflow["1"]["class_type"] == "LoadImage"
    assert longcat.workflow["2"]["class_type"] == "LoadAudio"
    assert longcat.pipeline.output_fps == 25
    assert longcat.pipeline.target_width == 1920
    assert longcat.pipeline.target_height == 1080
    assert longcat.pipeline.upscale_mode == "auto"

    fields_by_key = {f.key: f for f in longcat.fields}

    # Verify all 4 beginner-friendly basic controls.
    quality = fields_by_key["pipeline.quality_preset"]
    assert quality.group == "basic"
    assert quality.default == "cinema_flagship"
    assert quality.options == ["cinema_flagship", "balanced_hd", "longform_fast"]

    framing = fields_by_key["pipeline.framing_style"]
    assert framing.group == "basic"
    assert framing.default == "half_body_gestures"
    assert framing.options == [
        "half_body_gestures",
        "closeup_presenter",
        "full_body_dynamic",
    ]

    upscale = fields_by_key["pipeline.upscale_mode"]
    assert upscale.group == "basic"
    assert upscale.default == "auto"
    assert upscale.options == [
        "auto",
        "seedvr2_7b_sharp",
        "seedvr2_7b",
        "flashvsr_plus",
        "none",
    ]

    res = fields_by_key["pipeline.target_resolution"]
    assert res.group == "basic"
    assert res.default == "1920x1080"
    assert res.options == [
        "1920x1080",
        "1080x1920",
        "2560x1440",
        "1440x2560",
        "1280x720",
        "720x1280",
    ]

    # Verify collapsible advanced fields are grouped under "advanced".
    expected_advanced_keys = (
        "pipeline.sampling_mode",
        "pipeline.audio_guidance_scale",
        "pipeline.text_guidance_scale",
        "pipeline.num_inference_steps",
        "pipeline.mix_steps",
        "pipeline.mix_alpha",
        "pipeline.vocal_separation",
        "pipeline.ref_img_index",
        "pipeline.seed",
        "pipeline.crf",
        "pipeline.positive_prompt",
        "pipeline.negative_prompt",
        "pipeline.voice_ref_text",
        "pipeline.voice_prompt",
        "pipeline.emotion_ref_audio",
        "pipeline.emotion_prompt",
        "pipeline.pause_seconds",
    )
    for adv_key in expected_advanced_keys:
        assert adv_key in fields_by_key, f"missing advanced field {adv_key}"
        assert fields_by_key[adv_key].group == "advanced"

    assert fields_by_key["pipeline.sampling_mode"].options == [
        "auto",
        "non_distill_cfg",
        "distill_lora_mix",
    ]
    assert fields_by_key["pipeline.crf"].default == 16
    assert fields_by_key["pipeline.crf"].min_value == 12
    assert fields_by_key["pipeline.crf"].max_value == 24

    # Verify digital-human-infinitetalk P0 tuning remains intact.
    infinitetalk = templates_by_id["digital-human-infinitetalk"]
    assert infinitetalk.pipeline is not None
    assert infinitetalk.pipeline.video_backend == "infinitetalk"
    assert infinitetalk.workflow["14"]["inputs"]["colormatch"] == "disabled"
    assert infinitetalk.workflow["15"]["inputs"]["steps"] == 8
    assert infinitetalk.workflow["15"]["inputs"]["shift"] == pytest.approx(8.0)
    it_fields = {f.key: f for f in infinitetalk.fields}
    assert it_fields["15.steps"].default == 8
    assert "推荐 8 步" in it_fields["15.steps"].description


# ---------------------------------------------------------------------------
# 2. server/avatar_worker_longcat16.py pure helpers
# ---------------------------------------------------------------------------


def test_longcat16_plan_segments_edge_cases():
    # 1 frame
    seg_1 = plan_longcat16_segments(1)
    assert len(seg_1) == 1
    assert seg_1[0]["start_frame"] == 0
    assert seg_1[0]["end_frame"] == 93
    assert seg_1[0]["overlap_frames"] == 0
    assert seg_1[0]["drop_prefix_frames"] == 0
    assert seg_1[0]["emit_frames"] == 1
    assert seg_1[0]["cumulative_emitted_frames"] == 1

    # Exact single window: 93 frames
    seg_93 = plan_longcat16_segments(93)
    assert len(seg_93) == 1
    assert seg_93[0]["emit_frames"] == 93
    assert seg_93[0]["cumulative_generated_frames"] == 93
    assert seg_93[0]["cumulative_emitted_frames"] == 93

    # Just over one window: 94 frames (2 segments with 13-frame overlap)
    seg_94 = plan_longcat16_segments(94)
    assert len(seg_94) == 2
    assert seg_94[0]["start_frame"] == 0 and seg_94[0]["end_frame"] == 93
    assert seg_94[0]["emit_frames"] == 93
    assert seg_94[1]["start_frame"] == 80 and seg_94[1]["end_frame"] == 173
    assert seg_94[1]["overlap_frames"] == 13
    assert seg_94[1]["drop_prefix_frames"] == 13
    assert seg_94[1]["emit_frames"] == 1
    assert seg_94[1]["cumulative_emitted_frames"] == 94

    # 250 frames (10s @ 25fps): 93 + 80 + 77 = 250
    seg_250 = plan_longcat16_segments(250)
    assert len(seg_250) == 3
    assert [s["emit_frames"] for s in seg_250] == [93, 80, 77]
    assert [s["start_frame"] for s in seg_250] == [0, 80, 160]
    assert seg_250[-1]["cumulative_emitted_frames"] == 250

    # 1500 frames (60s @ 25fps): 1 + ceil((1500 - 93) / 80) = 19 segments
    seg_1500 = plan_longcat16_segments(1500)
    assert len(seg_1500) == 19
    assert sum(s["emit_frames"] for s in seg_1500) == 1500
    assert seg_1500[-1]["cumulative_emitted_frames"] == 1500
    assert seg_1500[-1]["cumulative_generated_frames"] >= 1500
    for idx, seg in enumerate(seg_1500):
        assert seg["segment_index"] == idx
        assert seg["num_frames"] == 93
        if idx == 0:
            assert seg["overlap_frames"] == 0
            assert seg["drop_prefix_frames"] == 0
        else:
            assert seg["overlap_frames"] == 13
            assert seg["drop_prefix_frames"] == 13
            assert seg["start_frame"] == seg_1500[idx - 1]["end_frame"] - 13

    # Negative / invalid inputs rejected cleanly.
    for bad_args in ((0, 93, 13), (-5, 93, 13), (True, 93, 13), (100, 13, 13), (100, 93, 0)):
        with pytest.raises(ValueError):
            plan_longcat16_segments(*bad_args)


def test_longcat16_vocal_separation_and_sampling_params():
    # Vocal separation modes.
    assert should_run_vocal_separation("auto", is_tts_audio=True) is False
    assert should_run_vocal_separation("auto", is_tts_audio=False) is True
    assert should_run_vocal_separation("always", is_tts_audio=True) is True
    assert should_run_vocal_separation("always", is_tts_audio=False) is True
    assert should_run_vocal_separation("never", is_tts_audio=True) is False
    assert should_run_vocal_separation("never", is_tts_audio=False) is False
    with pytest.raises(ValueError):
        should_run_vocal_separation("sometimes", is_tts_audio=False)

    # Non-distill full BF16 CFG defaults & clamping (use_loudness_norm ALWAYS False).
    cfg_default = resolve_longcat16_sampling_params({"use_loudness_norm": True})
    assert cfg_default == {
        "sampling_mode": "non_distill_cfg",
        "use_distill": False,
        "num_inference_steps": 50,
        "text_guidance_scale": 4.0,
        "audio_guidance_scale": 4.0,
        "mix_steps": 0,
        "mix_alpha": 0.0,
        "enhance_hf": True,
        "use_kv_cache": True,
        "use_loudness_norm": False,
    }

    cfg_clamped = resolve_longcat16_sampling_params(
        {
            "sampling_mode": "non_distill_cfg",
            "num_inference_steps": 999,
            "text_guidance_scale": 99.0,
            "audio_guidance_scale": 0.1,
            "use_loudness_norm": True,
        }
    )
    assert cfg_clamped["num_inference_steps"] == 60
    assert cfg_clamped["text_guidance_scale"] == pytest.approx(7.0)
    assert cfg_clamped["audio_guidance_scale"] == pytest.approx(1.0)
    assert cfg_clamped["use_loudness_norm"] is False

    # Distill LoRA mix defaults, KV-cache rule, and clamping.
    distill_default = resolve_longcat16_sampling_params(
        {"sampling_mode": "distill_lora_mix", "use_loudness_norm": True}
    )
    assert distill_default == {
        "sampling_mode": "distill_lora_mix",
        "use_distill": True,
        "num_inference_steps": 8,
        "text_guidance_scale": 1.0,
        "audio_guidance_scale": 1.0,
        "mix_steps": 4,
        "mix_alpha": 1.0,
        "enhance_hf": False,
        "use_kv_cache": False,
        "use_loudness_norm": False,
    }

    distill_kv = resolve_longcat16_sampling_params(
        {
            "sampling_mode": "distill_lora_mix",
            "mix_steps": 0,
            "mix_alpha": 2.5,
            "num_inference_steps": 50,
            "audio_guidance_scale": 5.0,
        }
    )
    assert distill_kv["num_inference_steps"] == 8
    assert distill_kv["audio_guidance_scale"] == pytest.approx(1.0)
    assert distill_kv["mix_steps"] == 0
    assert distill_kv["mix_alpha"] == pytest.approx(1.0)
    assert distill_kv["use_kv_cache"] is True
    assert distill_kv["use_loudness_norm"] is False

    with pytest.raises(ValueError):
        resolve_longcat16_sampling_params({"sampling_mode": "invalid_mode"})


def test_longcat16_attention_flags_and_rejects_quantized_sageattention():
    # Hopper (sm_90): prefers FA3, then FA2, then SDPA.
    assert resolve_longcat16_attention_flags(
        "auto", major=9, minor=0, has_fa3=True, has_fa2=True
    ) == {"enable_flashattn3": True, "enable_flashattn2": False}
    assert resolve_longcat16_attention_flags(
        "auto", major=9, minor=0, has_fa3=False, has_fa2=True
    ) == {"enable_flashattn3": False, "enable_flashattn2": True}
    assert resolve_longcat16_attention_flags(
        "auto", major=9, minor=0, has_fa3=False, has_fa2=False
    ) == {"enable_flashattn3": False, "enable_flashattn2": False}

    # Blackwell (sm_120 / sm_100) and Ada (sm_89): prefer FA2, never FA3 in auto.
    for arch in ((12, 0), (10, 0), (8, 9), (8, 0)):
        assert resolve_longcat16_attention_flags(
            "auto", major=arch[0], minor=arch[1], has_fa3=True, has_fa2=True
        ) == {"enable_flashattn3": False, "enable_flashattn2": True}
        assert resolve_longcat16_attention_flags(
            "auto", major=arch[0], minor=arch[1], has_fa3=True, has_fa2=False
        ) == {"enable_flashattn3": False, "enable_flashattn2": False}

    # Explicit backends and architecture guards:
    # - FA3 is strictly Hopper-only (major in (0, 9)): non-Hopper GPUs (sm_80+/sm_100/sm_120)
    #   degrade cleanly to FA2 when available, else SDPA.
    # - FA2 requires Ampere+ (major == 0 or major >= 8): Turing (sm_75) and Volta (sm_70)
    #   always force SDPA even when has_fa3/has_fa2 are True.
    assert resolve_longcat16_attention_flags(
        "flash_attn_3", major=9, minor=0, has_fa3=True, has_fa2=True
    ) == {"enable_flashattn3": True, "enable_flashattn2": False}
    assert resolve_longcat16_attention_flags(
        "flash_attn_3", major=0, minor=0, has_fa3=True, has_fa2=True
    ) == {"enable_flashattn3": True, "enable_flashattn2": False}
    for non_hopper in ((12, 0), (10, 0), (8, 9), (8, 0)):
        assert resolve_longcat16_attention_flags(
            "flash_attn_3",
            major=non_hopper[0],
            minor=non_hopper[1],
            has_fa3=True,
            has_fa2=True,
        ) == {"enable_flashattn3": False, "enable_flashattn2": True}
        assert resolve_longcat16_attention_flags(
            "flash_attn_3",
            major=non_hopper[0],
            minor=non_hopper[1],
            has_fa3=True,
            has_fa2=False,
        ) == {"enable_flashattn3": False, "enable_flashattn2": False}
    for legacy_arch in ((7, 5), (7, 0)):
        for mode in ("auto", "flash_attn_3", "flash_attn_2", "sdpa"):
            assert resolve_longcat16_attention_flags(
                mode,
                major=legacy_arch[0],
                minor=legacy_arch[1],
                has_fa3=True,
                has_fa2=True,
            ) == {"enable_flashattn3": False, "enable_flashattn2": False}
    assert resolve_longcat16_attention_flags(
        "flash_attn_3", major=9, has_fa3=False, has_fa2=True
    ) == {"enable_flashattn3": False, "enable_flashattn2": True}
    assert resolve_longcat16_attention_flags(
        "flash_attn_2", major=9, has_fa3=True, has_fa2=False
    ) == {"enable_flashattn3": False, "enable_flashattn2": False}
    assert resolve_longcat16_attention_flags(
        "sdpa", major=9, has_fa3=True, has_fa2=True
    ) == {"enable_flashattn3": False, "enable_flashattn2": False}

    # Quantized SageAttention and unsupported backends are strictly rejected.
    for forbidden in ("sageattn", "sageattn_3", "sageattention", "xformers", "fp8_attn"):
        with pytest.raises(ValueError):
            resolve_longcat16_attention_flags(forbidden, major=12, has_fa2=True)


def test_longcat16_ffv1_cmd_progress_and_speakers(capsys, tmp_path):
    out_mkv = tmp_path / "stage2_720p.mkv"
    cmd = build_lossless_ffv1_writer_cmd("/usr/bin/ffmpeg", 1280, 768, 25, out_mkv)
    assert cmd[0] == "/usr/bin/ffmpeg"
    assert "-protocol_whitelist" in cmd and cmd[cmd.index("-protocol_whitelist") + 1] == "file,pipe"
    assert "-f" in cmd and cmd[cmd.index("-f") + 1] == "rawvideo"
    pix_fmt_values = [cmd[idx + 1] for idx, tok in enumerate(cmd) if tok == "-pix_fmt"]
    assert pix_fmt_values == ["rgb24", "bgr0"]
    assert "-s" in cmd and cmd[cmd.index("-s") + 1] == "1280x768"
    assert "-r" in cmd and cmd[cmd.index("-r") + 1] == "25"
    assert "-c:v" in cmd and cmd[cmd.index("-c:v") + 1] == "ffv1"
    assert "-level" in cmd and cmd[cmd.index("-level") + 1] == "3"
    assert "-g" in cmd and cmd[cmd.index("-g") + 1] == "1"
    assert "-slicecrc" in cmd and cmd[cmd.index("-slicecrc") + 1] == "1"
    assert "-an" in cmd
    assert cmd[-1] == str(out_mkv)

    with pytest.raises(ValueError):
        build_lossless_ffv1_writer_cmd("ffmpeg", 0, 768, 25, out_mkv)
    with pytest.raises(ValueError):
        build_lossless_ffv1_writer_cmd("ffmpeg", 1280, 768, 25, "bad\npath.mkv")

    # Progress emission format & clamping.
    emit_colabone_progress("render", 1.5, "Segment 2/3", segment=2, total_segments=3)
    captured = capsys.readouterr().out.strip()
    assert captured.startswith(COLABONE_PROGRESS_PREFIX)
    parsed = parse_colabone_progress_line(captured)
    assert parsed == {
        "stage": "render",
        "progress": 1.0,
        "message": "Segment 2/3",
        "segment": 2,
        "total_segments": 3,
    }

    # Single speaker fallback from audio_path.
    primary_a, speakers_a = normalize_longcat16_speakers({"audio_path": "/tmp/a.wav"})
    assert primary_a == "/tmp/a.wav"
    assert speakers_a == [{"id": "speaker_1", "audio_path": "/tmp/a.wav"}]

    # Future multi-speaker list preserved and primary audio resolved from speakers[0].
    primary_multi, speakers_multi = normalize_longcat16_speakers(
        {
            "speakers": [
                {"id": "host_a", "audio_path": "/tmp/host_a.wav"},
                {"id": "", "audio_path": "/tmp/guest_b.wav"},
            ]
        }
    )
    assert primary_multi == "/tmp/host_a.wav"
    assert speakers_multi == [
        {"id": "host_a", "audio_path": "/tmp/host_a.wav"},
        {"id": "speaker_2", "audio_path": "/tmp/guest_b.wav"},
    ]


# ---------------------------------------------------------------------------
# 3. server/vsr_worker_diffusion.py pure helpers
# ---------------------------------------------------------------------------


def test_vsr_engine_files_resolution_and_dimensions(tmp_path):
    assert COLABONE_VSR_ENGINE_FILES["seedvr2_7b_sharp"] == (
        "seedvr2_ema_7b_sharp_fp16.safetensors",
        "ema_vae_fp16.safetensors",
    )
    assert COLABONE_VSR_ENGINE_FILES["seedvr2_7b"] == (
        "seedvr2_ema_7b_fp16.safetensors",
        "ema_vae_fp16.safetensors",
    )
    assert COLABONE_VSR_ENGINE_FILES["flashvsr_plus"] == (
        "diffusion_pytorch_model_streaming_dmd.safetensors",
        "LQ_proj_in.ckpt",
        "TCDecoder.ckpt",
    )
    assert "Wan2.1_VAE.pth" not in COLABONE_VSR_ENGINE_FILES["flashvsr_plus"]

    weights = tmp_path / "weights"
    sharp_files = resolve_vsr_model_files("seedvr2_7b_sharp", weights)
    assert sharp_files == {
        "seedvr2_ema_7b_sharp_fp16.safetensors": weights / "seedvr2_ema_7b_sharp_fp16.safetensors",
        "ema_vae_fp16.safetensors": weights / "ema_vae_fp16.safetensors",
    }
    flash_files = resolve_vsr_model_files("FLASHVSR_PLUS", weights)
    assert len(flash_files) == 3
    assert "Wan2.1_VAE.pth" not in flash_files
    assert flash_files["TCDecoder.ckpt"] == weights / "TCDecoder.ckpt"

    with pytest.raises(ValueError):
        resolve_vsr_model_files("realesrgan_4x", weights)

    # Short-side cover-scale targets (multiple of 16).
    assert compute_upscale_short_side(1280, 768, 1920, 1080) == 1152
    assert compute_upscale_short_side(1280, 768, 2560, 1440) == 1536
    assert compute_upscale_short_side(768, 1280, 1080, 1920) == 1152
    assert compute_upscale_short_side(768, 1280, 1440, 2560) == 1536
    non_aligned = compute_upscale_short_side(1280, 768, 1500, 900)
    assert non_aligned % 16 == 0 and non_aligned >= 900

    with pytest.raises(ValueError):
        compute_upscale_short_side(0, 768, 1920, 1080)

    # FlashVSR+ integer scale factor (2 when 2x covers target, else 4).
    assert build_flashvsr_scale_factor(1280, 768, 1920, 1080) == 2
    assert build_flashvsr_scale_factor(1280, 768, 2560, 1440) == 2
    assert build_flashvsr_scale_factor(768, 1280, 1080, 1920) == 2
    assert build_flashvsr_scale_factor(1280, 768, 3840, 2160) == 4
    assert build_flashvsr_scale_factor(768, 1280, 2160, 3840) == 4


def test_vsr_seedvr2_batch_alignment_chunk_plan_cli_and_cover_crop(tmp_path):
    # Temporal 4k+1 batch alignment in [5, max(5, total_frames)].
    assert align_seedvr2_batch_size(33, 100) == 33
    assert align_seedvr2_batch_size(32, 100) == 29
    assert align_seedvr2_batch_size(33, 20) == 17
    assert align_seedvr2_batch_size(2, 100) == 5
    assert align_seedvr2_batch_size(12, 3) == 5

    # Chunk plan invariants.
    assert compute_seedvr2_chunk_plan(0) == []
    single = compute_seedvr2_chunk_plan(25, batch_size=33, overlap=4)
    assert single == [{"start": 0, "end": 25, "keep_start": 0, "keep_end": 25}]

    multi = compute_seedvr2_chunk_plan(100, batch_size=33, overlap=4)
    assert len(multi) >= 4
    assert multi[0]["keep_start"] == 0
    assert multi[-1]["keep_end"] == 100
    for idx, chunk in enumerate(multi):
        assert chunk["start"] <= chunk["keep_start"] < chunk["keep_end"] <= chunk["end"]
        if idx > 0:
            assert chunk["keep_start"] == multi[idx - 1]["keep_end"]
            assert chunk["start"] < multi[idx - 1]["end"]  # overlapping windows

    # SeedVR2 CLI command: 96 GB GPU uses resolved paths, --load_cap, zero block swap, zero VAE tiling.
    repo = tmp_path / "seedvr2_repo"
    weights = tmp_path / "seedvr2_weights"
    in_mkv = tmp_path / "stage2.mkv"
    out_dir = tmp_path / "png_out"
    cmd_96g = build_seedvr2_cli_command(
        "/opt/seedvr2_venv/bin/python",
        repo,
        weights,
        in_mkv,
        out_dir,
        engine="seedvr2_7b_sharp",
        resolution_short_side=1152,
        batch_size=33,
        temporal_overlap=4,
        color_correction="lab",
        seed=42,
        vram_mib=98304,
        exact_frames=100,
    )
    assert cmd_96g[:3] == [
        str(Path("/opt/seedvr2_venv/bin/python")),
        str((repo / "inference_cli.py").resolve()),
        str(in_mkv.resolve()),
    ]
    assert cmd_96g[cmd_96g.index("--model_dir") + 1] == str(weights.resolve())
    assert cmd_96g[cmd_96g.index("--output") + 1] == str(out_dir.resolve())
    assert "--dit_model" in cmd_96g
    assert cmd_96g[cmd_96g.index("--dit_model") + 1] == "seedvr2_ema_7b_sharp_fp16.safetensors"
    assert cmd_96g[cmd_96g.index("--resolution") + 1] == "1152"
    assert cmd_96g[cmd_96g.index("--batch_size") + 1] == "33"
    assert cmd_96g[cmd_96g.index("--temporal_overlap") + 1] == "4"
    assert cmd_96g[cmd_96g.index("--color_correction") + 1] == "lab"
    assert cmd_96g[cmd_96g.index("--output_format") + 1] == "png"
    assert cmd_96g[cmd_96g.index("--load_cap") + 1] == "100"
    assert "--blocks_to_swap" not in cmd_96g
    assert "--vae_encode_tiled" not in cmd_96g
    assert "--vae_decode_tiled" not in cmd_96g
    assert "--dit_offload_device" not in cmd_96g
    assert "--vae_offload_device" not in cmd_96g
    assert "--chunk_size" not in cmd_96g

    # Low VRAM (< 48 GB) enables block swap + tiled VAE + CPU offload.
    cmd_24g = build_seedvr2_cli_command(
        "/opt/seedvr2_venv/bin/python",
        repo,
        weights,
        in_mkv,
        out_dir,
        engine="seedvr2_7b",
        resolution_short_side=1152,
        batch_size=17,
        temporal_overlap=4,
        color_correction="wavelet",
        seed=7,
        vram_mib=24576,
    )
    assert cmd_24g[cmd_24g.index("--dit_model") + 1] == "seedvr2_ema_7b_fp16.safetensors"
    assert cmd_24g[cmd_24g.index("--blocks_to_swap") + 1] == "16"
    assert "--vae_encode_tiled" in cmd_24g
    assert "--vae_decode_tiled" in cmd_24g
    assert cmd_24g[cmd_24g.index("--dit_offload_device") + 1] == "cpu"
    assert cmd_24g[cmd_24g.index("--vae_offload_device") + 1] == "cpu"

    # Long videos (> 264 frames) enable streaming chunking with cache + single CPU offload flags.
    assert compute_seedvr2_streaming_chunk_size(264, 33) == 0
    assert compute_seedvr2_streaming_chunk_size(265, 33) == 264
    assert compute_seedvr2_streaming_chunk_size(600, 33) == 264
    cmd_chunked_low_vram = build_seedvr2_cli_command(
        "/opt/seedvr2_venv/bin/python",
        repo,
        weights,
        in_mkv,
        out_dir,
        engine="seedvr2_7b_sharp",
        resolution_short_side=1152,
        batch_size=33,
        temporal_overlap=4,
        color_correction="lab",
        seed=42,
        vram_mib=24576,
        exact_frames=400,
        chunk_size=compute_seedvr2_streaming_chunk_size(400, 33),
    )
    assert cmd_chunked_low_vram[cmd_chunked_low_vram.index("--load_cap") + 1] == "400"
    assert cmd_chunked_low_vram[cmd_chunked_low_vram.index("--chunk_size") + 1] == "264"
    assert "--cache_dit" in cmd_chunked_low_vram
    assert "--cache_vae" in cmd_chunked_low_vram
    assert cmd_chunked_low_vram.count("--dit_offload_device") == 1
    assert cmd_chunked_low_vram.count("--vae_offload_device") == 1
    assert cmd_chunked_low_vram[cmd_chunked_low_vram.index("--dit_offload_device") + 1] == "cpu"
    assert cmd_chunked_low_vram[cmd_chunked_low_vram.index("--vae_offload_device") + 1] == "cpu"

    # Boundary 48 GB (<= 49152 MiB, e.g. L40S / A6000) enables conservative block swap + tiled VAE + CPU offload.
    cmd_48g = build_seedvr2_cli_command(
        "/opt/seedvr2_venv/bin/python",
        repo,
        weights,
        in_mkv,
        out_dir,
        engine="seedvr2_7b_sharp",
        resolution_short_side=1152,
        batch_size=33,
        temporal_overlap=4,
        color_correction="lab",
        seed=42,
        vram_mib=49152,
    )
    assert cmd_48g[cmd_48g.index("--blocks_to_swap") + 1] == "16"
    assert "--vae_encode_tiled" in cmd_48g
    assert "--vae_decode_tiled" in cmd_48g
    assert cmd_48g[cmd_48g.index("--dit_offload_device") + 1] == "cpu"
    assert cmd_48g[cmd_48g.index("--vae_offload_device") + 1] == "cpu"

    # Above 48 GB (> 49152 MiB, e.g. 64 GB / 96 GB GPUs) stays completely swap-free.
    for high_vram in (65536, 98304):
        cmd_hi = build_seedvr2_cli_command(
            "/opt/seedvr2_venv/bin/python",
            repo,
            weights,
            in_mkv,
            out_dir,
            engine="seedvr2_7b_sharp",
            resolution_short_side=1152,
            batch_size=33,
            temporal_overlap=4,
            color_correction="lab",
            seed=42,
            vram_mib=high_vram,
        )
        assert "--blocks_to_swap" not in cmd_hi
        assert "--vae_encode_tiled" not in cmd_hi
        assert "--vae_decode_tiled" not in cmd_hi
        assert "--dit_offload_device" not in cmd_hi
        assert "--vae_offload_device" not in cmd_hi

    with pytest.raises(ValueError):
        build_seedvr2_cli_command(
            "/opt/seedvr2_venv/bin/python",
            repo,
            weights,
            in_mkv,
            out_dir,
            engine="flashvsr_plus",
            resolution_short_side=1152,
            batch_size=33,
            temporal_overlap=4,
            color_correction="lab",
            seed=42,
            vram_mib=98304,
        )

    # Lossless cover-crop command (PNG sequence & video input):
    # -protocol_whitelist file,pipe, setsar=1 BEFORE scale, and -pix_fmt bgr0.
    out_mkv = tmp_path / "stage3_upscaled.mkv"
    png_cmd = build_cover_crop_lossless_cmd(
        "/usr/bin/ffmpeg",
        tmp_path / "seq" / "frame_%06d.png",
        out_mkv,
        target_w=1920,
        target_h=1080,
        fps=25,
        exact_frames=100,
        is_png_sequence=True,
    )
    assert "-protocol_whitelist" in png_cmd and png_cmd[png_cmd.index("-protocol_whitelist") + 1] == "file,pipe"
    assert "-framerate" in png_cmd and png_cmd[png_cmd.index("-framerate") + 1] == "25"
    assert "-frames:v" in png_cmd and png_cmd[png_cmd.index("-frames:v") + 1] == "100"
    assert (
        png_cmd[png_cmd.index("-vf") + 1]
        == "setsar=1,scale=1920:1080:force_original_aspect_ratio=increase:flags=lanczos,crop=1920:1080"
    )
    assert "-c:v" in png_cmd and png_cmd[png_cmd.index("-c:v") + 1] == "ffv1"
    assert "-pix_fmt" in png_cmd and png_cmd[png_cmd.index("-pix_fmt") + 1] == "bgr0"
    assert "-an" in png_cmd

    glob_cmd = build_cover_crop_lossless_cmd(
        "/usr/bin/ffmpeg",
        str(tmp_path / "seq" / "*.png"),
        out_mkv,
        target_w=2560,
        target_h=1440,
        fps=25,
        exact_frames=50,
        is_png_sequence=True,
    )
    assert "-pattern_type" in glob_cmd and glob_cmd[glob_cmd.index("-pattern_type") + 1] == "glob"

    vid_cmd = build_cover_crop_lossless_cmd(
        "/usr/bin/ffmpeg",
        in_mkv,
        out_mkv,
        target_w=1920,
        target_h=1080,
        fps=25,
        exact_frames=50,
        is_png_sequence=False,
    )
    assert "-protocol_whitelist" in vid_cmd and vid_cmd[vid_cmd.index("-protocol_whitelist") + 1] == "file,pipe"
    assert (
        vid_cmd[vid_cmd.index("-vf") + 1]
        == "setsar=1,scale=1920:1080:force_original_aspect_ratio=increase:flags=lanczos,crop=1920:1080,setpts=N/(25*TB)"
    )
    assert "-pix_fmt" in vid_cmd and vid_cmd[vid_cmd.index("-pix_fmt") + 1] == "bgr0"


# ---------------------------------------------------------------------------
# 4. server/avatar_engines.py
# ---------------------------------------------------------------------------


def test_avatar_engines_allow_patterns_and_progress_parser():
    # Avatar allow patterns: pure BF16, NEVER base_model_int8/**, exact Whisper files,
    # loraA/loraB ONLY for distill_lora_mix, conditional vocal_separator.
    p_cfg_auto = longcat16_avatar_allow_patterns("non_distill_cfg", "auto")
    assert "base_model/**" in p_cfg_auto
    assert "whisper-large-v3/model.safetensors" in p_cfg_auto
    assert "whisper-large-v3/*.json" in p_cfg_auto
    assert "whisper-large-v3/*.txt" in p_cfg_auto
    assert "whisper-large-v3/**" not in p_cfg_auto
    assert "scheduler/**" in p_cfg_auto
    assert "vocal_separator/**" in p_cfg_auto
    assert "loraA/**" not in p_cfg_auto
    assert "loraB/**" not in p_cfg_auto
    assert not any("int8" in pat.lower() for pat in p_cfg_auto)

    p_auto_mode = longcat16_avatar_allow_patterns("auto", "auto")
    assert p_auto_mode == p_cfg_auto
    assert "loraA/**" not in p_auto_mode
    assert "loraB/**" not in p_auto_mode

    p_distill = longcat16_avatar_allow_patterns("distill_lora_mix", "always")
    assert "loraA/**" in p_distill
    assert "loraB/**" in p_distill
    assert "vocal_separator/**" in p_distill
    assert not any("int8" in pat.lower() for pat in p_distill)

    p_no_vocal = longcat16_avatar_allow_patterns("non_distill_cfg", "never")
    assert "vocal_separator/**" not in p_no_vocal
    assert "loraA/**" not in p_no_vocal

    # Base allow patterns: tokenizer/text_encoder/vae only, NEVER dit/**.
    p_base = longcat16_base_allow_patterns()
    assert "tokenizer/**" in p_base
    assert "text_encoder/**" in p_base
    assert "vae/**" in p_base
    assert not any("dit" in pat.lower() for pat in p_base)

    # VSR allow patterns: exact lazy files per engine, no Wan2.1_VAE.pth on flashvsr_plus.
    repo_s, rev_s, pats_s = vsr_engine_allow_patterns("seedvr2_7b_sharp")
    assert repo_s == COLABONE_SEEDVR2_HF_REPO
    assert rev_s == COLABONE_SEEDVR2_HF_REV
    assert pats_s == ["seedvr2_ema_7b_sharp_fp16.safetensors", "ema_vae_fp16.safetensors"]

    repo_std, rev_std, pats_std = vsr_engine_allow_patterns("seedvr2_7b")
    assert repo_std == COLABONE_SEEDVR2_HF_REPO
    assert rev_std == COLABONE_SEEDVR2_HF_REV
    assert pats_std == ["seedvr2_ema_7b_fp16.safetensors", "ema_vae_fp16.safetensors"]

    repo_f, rev_f, pats_f = vsr_engine_allow_patterns("flashvsr_plus")
    assert repo_f == COLABONE_FLASHVSR_HF_REPO
    assert rev_f == COLABONE_FLASHVSR_HF_REV
    assert "diffusion_pytorch_model_streaming_dmd.safetensors" in pats_f
    assert "LQ_proj_in.ckpt" in pats_f
    assert "TCDecoder.ckpt" in pats_f
    assert "Wan2.1_VAE.pth" not in pats_f

    with pytest.raises(ValueError):
        vsr_engine_allow_patterns("unknown_vsr")

    # COLABONE_PROGRESS parser.
    assert parse_colabone_progress_line(
        'COLABONE_PROGRESS:{"stage":"render","progress":0.5,"message":"halfway"}'
    ) == {"stage": "render", "progress": 0.5, "message": "halfway"}
    assert parse_colabone_progress_line(
        '[worker] COLABONE_PROGRESS:{"stage":"upscale","progress":0.75}'
    ) == {"stage": "upscale", "progress": 0.75}
    assert parse_colabone_progress_line("COLABONE_PROGRESS:not-json") is None
    assert parse_colabone_progress_line("COLABONE_PROGRESS:[1,2,3]") is None
    assert parse_colabone_progress_line("ordinary log line") is None
    assert parse_colabone_progress_line("") is None


def _populate_concrete_weight_files(
    weights_dir: Path,
    *,
    repo_id: str,
    revision: str,
    patterns: list[str],
    concrete_relpaths: list[str],
) -> None:
    """Create concrete non-empty weight files and a valid .colabone-weights.json marker."""
    weights_dir.mkdir(parents=True, exist_ok=True)
    for rel in concrete_relpaths:
        target = weights_dir / rel
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(b"concrete-cached-weight-bytes")
    atomic_json(
        weights_dir / ".colabone-weights.json",
        {
            "repo_id": repo_id,
            "revision": revision,
            "downloaded_patterns": patterns,
        },
    )


def test_avatar_engine_manager_preflight_vram_disk_and_cached_weights(tmp_path, monkeypatch):
    gpu_state = {"name": "NVIDIA RTX PRO 6000 Blackwell", "total_mib": 98304, "used_mib": 1024}
    mgr = ColabOneAvatarEngineManager(
        tmp_path / "remote",
        gpu_info_fn=lambda: dict(gpu_state),
    )

    # 1. No GPU fails closed.
    gpu_state["total_mib"] = 0
    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(200 * 1024**3, 10 * 1024**3, 190 * 1024**3),
    )
    with pytest.raises(RuntimeError, match="未检测到可用远端 GPU"):
        mgr.check_preflight()

    # 2. Insufficient VRAM (24 GiB < 48 GiB) rejected.
    gpu_state["total_mib"] = 24576
    with pytest.raises(RuntimeError, match="显存不足"):
        mgr.check_preflight(min_vram_mib=49152, min_disk_mib=79872)

    # 3. 96 GB GPU + insufficient cold disk (30 GiB < 78 GiB) rejected.
    gpu_state["total_mib"] = 98304
    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(100 * 1024**3, 70 * 1024**3, 30 * 1024**3),
    )
    with pytest.raises(RuntimeError, match="远端磁盘空间不足"):
        mgr.check_preflight(min_vram_mib=49152, min_disk_mib=79872)

    # 4. Cached weights markers + concrete payload files pass with 10 GiB free (> 6 GiB headroom).
    _populate_concrete_weight_files(
        mgr.weights_dir / "longcat16_avatar",
        repo_id=COLABONE_LONGCAT16_AVATAR_HF_REPO,
        revision=COLABONE_LONGCAT16_AVATAR_HF_REV,
        patterns=longcat16_avatar_allow_patterns("non_distill_cfg", "auto"),
        concrete_relpaths=[
            "base_model/diffusion_pytorch_model.safetensors",
            "whisper-large-v3/model.safetensors",
            "whisper-large-v3/config.json",
            "scheduler/scheduler_config.json",
            "vocal_separator/Kim_Vocal_2.onnx",
        ],
    )
    _populate_concrete_weight_files(
        mgr.weights_dir / "longcat16_base",
        repo_id=COLABONE_LONGCAT16_BASE_HF_REPO,
        revision=COLABONE_LONGCAT16_BASE_HF_REV,
        patterns=longcat16_base_allow_patterns(),
        concrete_relpaths=[
            "tokenizer/tokenizer_config.json",
            "text_encoder/model.safetensors",
            "vae/diffusion_pytorch_model.safetensors",
        ],
    )
    _populate_concrete_weight_files(
        mgr.weights_dir / "seedvr2",
        repo_id=COLABONE_SEEDVR2_HF_REPO,
        revision=COLABONE_SEEDVR2_HF_REV,
        patterns=vsr_engine_allow_patterns("seedvr2_7b_sharp")[2],
        concrete_relpaths=[
            "seedvr2_ema_7b_sharp_fp16.safetensors",
            "ema_vae_fp16.safetensors",
        ],
    )

    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(100 * 1024**3, 90 * 1024**3, 10 * 1024**3),
    )
    info_96g = mgr.check_preflight(
        min_vram_mib=49152, min_disk_mib=79872, upscale_mode="seedvr2_7b_sharp"
    )
    assert info_96g["is_high_vram_96g"] is True
    assert info_96g["all_weights_cached"] is True
    assert info_96g["required_disk_mib"] == 6144
    assert info_96g["total_mib"] == 98304

    # 5. Cached weights still enforce 6 GiB workspace headroom.
    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(100 * 1024**3, 98 * 1024**3, 2 * 1024**3),
    )
    with pytest.raises(RuntimeError, match="工作区磁盘空间不足"):
        mgr.check_preflight(
            min_vram_mib=49152, min_disk_mib=79872, upscale_mode="seedvr2_7b_sharp"
        )

    # 6. 48 GB GPU passes preflight with is_high_vram_96g == False.
    gpu_state["total_mib"] = 49152
    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(100 * 1024**3, 90 * 1024**3, 10 * 1024**3),
    )
    info_48g = mgr.check_preflight(
        min_vram_mib=49152, min_disk_mib=79872, upscale_mode="seedvr2_7b_sharp"
    )
    assert info_48g["is_high_vram_96g"] is False


def test_avatar_engine_manager_mocked_ensure_longcat16_and_vsr_ready(tmp_path):
    setup_cmds: list[list[str]] = []
    hf_downloads: list[dict[str, Any]] = []

    def fake_run_cmd(args, **kwargs):
        cmd = [str(a) for a in args]
        setup_cmds.append(cmd)
        if cmd[0] == "git" and "init" in cmd:
            target = Path(cmd[-1])
            target.mkdir(parents=True, exist_ok=True)
            (target / ".git").mkdir(exist_ok=True)
        elif "-m" in cmd and "venv" in cmd:
            venv_dir = Path(cmd[-1])
            (venv_dir / "bin").mkdir(parents=True, exist_ok=True)
            (venv_dir / "pyvenv.cfg").write_text("include-system-site-packages = true\n", encoding="utf-8")
            (venv_dir / "bin" / "python").write_text("#!/bin/sh\n", encoding="utf-8")

    def fake_download(**kwargs):
        hf_downloads.append(dict(kwargs))
        local_dir = Path(kwargs["local_dir"])
        local_dir.mkdir(parents=True, exist_ok=True)
        (local_dir / "model.safetensors").write_bytes(b"weights")

    mgr = ColabOneAvatarEngineManager(
        tmp_path / "remote",
        run_cmd_fn=fake_run_cmd,
        download_fn=fake_download,
        gpu_info_fn=lambda: {"name": "Blackwell", "total_mib": 98304, "used_mib": 0},
    )
    task = ColabOneTask("comfyui_run", "native-ready")

    # First ensure_longcat16_ready (non_distill_cfg): clones pinned commit, builds venv, downloads avatar + base.
    paths = mgr.ensure_longcat16_ready(
        task,
        hf_token="hf_secret_123",
        sampling_mode="non_distill_cfg",
        vocal_separation="auto",
        progress_range=(0.18, 0.30),
    )
    assert paths["repo_dir"] == mgr.repos_dir / "LongCat-Video"
    assert paths["avatar_dir"] == mgr.weights_dir / "longcat16_avatar"
    assert paths["base_dir"] == mgr.weights_dir / "longcat16_base"
    assert paths["compile_cache_dir"].is_dir()
    assert any(COLABONE_LONGCAT16_COMMIT in cmd for cmd in setup_cmds)
    assert any("--system-site-packages" in cmd for cmd in setup_cmds)
    assert len(hf_downloads) == 2
    assert hf_downloads[0]["repo_id"] == COLABONE_LONGCAT16_AVATAR_HF_REPO
    assert hf_downloads[0]["revision"] == COLABONE_LONGCAT16_AVATAR_HF_REV
    assert "loraA/**" not in hf_downloads[0]["allow_patterns"]
    assert hf_downloads[1]["repo_id"] == COLABONE_LONGCAT16_BASE_HF_REPO
    assert hf_downloads[1]["revision"] == COLABONE_LONGCAT16_BASE_HF_REV
    assert not any("dit" in p for p in hf_downloads[1]["allow_patterns"])
    assert task.progress == pytest.approx(0.30)

    # Repeat call with same options: 100% cache hit, zero extra cmds or downloads.
    prev_cmds = len(setup_cmds)
    prev_dls = len(hf_downloads)
    mgr.ensure_longcat16_ready(
        task, sampling_mode="non_distill_cfg", vocal_separation="auto"
    )
    assert len(setup_cmds) == prev_cmds
    assert len(hf_downloads) == prev_dls

    # Upgrading to distill_lora_mix lazily fetches loraA/loraB without re-downloading base weights.
    mgr.ensure_longcat16_ready(
        task, sampling_mode="distill_lora_mix", vocal_separation="auto"
    )
    assert len(hf_downloads) == prev_dls + 1
    assert "loraA/**" in hf_downloads[-1]["allow_patterns"]
    assert "loraB/**" in hf_downloads[-1]["allow_patterns"]

    # ensure_vsr_ready for SeedVR2 7B Sharp and FlashVSR+ uses dedicated isolated venvs.
    setup_cmds.clear()
    hf_downloads.clear()
    seed_paths = mgr.ensure_vsr_ready(
        task, engine="seedvr2_7b_sharp", progress_range=(0.72, 0.78)
    )
    assert seed_paths["repo_dir"] == mgr.repos_dir / "ComfyUI-SeedVR2_VideoUpscaler"
    assert seed_paths["weights_dir"] == mgr.weights_dir / "seedvr2"
    assert seed_paths["python"].is_relative_to(mgr.seedvr2_venv_dir)
    assert (mgr.root_dir / ".seedvr2_venv.ready.json").is_file()
    assert any(COLABONE_SEEDVR2_COMMIT in cmd for cmd in setup_cmds)
    assert len(hf_downloads) == 1
    assert hf_downloads[0]["allow_patterns"] == [
        "seedvr2_ema_7b_sharp_fp16.safetensors",
        "ema_vae_fp16.safetensors",
    ]
    assert task.progress == pytest.approx(0.78)

    flash_paths = mgr.ensure_vsr_ready(
        task, engine="flashvsr_plus", progress_range=(0.78, 0.80)
    )
    assert flash_paths["repo_dir"] == mgr.repos_dir / "FlashVSR_Plus"
    assert flash_paths["weights_dir"] == mgr.weights_dir / "flashvsr_plus"
    assert flash_paths["python"].is_relative_to(mgr.flashvsr_venv_dir)
    assert flash_paths["python"] != seed_paths["python"]
    assert (mgr.root_dir / ".flashvsr_venv.ready.json").is_file()
    assert any(COLABONE_FLASHVSR_COMMIT in cmd for cmd in setup_cmds)
    assert hf_downloads[-1]["repo_id"] == COLABONE_FLASHVSR_HF_REPO
    assert hf_downloads[-1]["revision"] == COLABONE_FLASHVSR_HF_REV
    assert "Wan2.1_VAE.pth" not in hf_downloads[-1]["allow_patterns"]

    with pytest.raises(ValueError):
        mgr.ensure_vsr_ready(task, engine="not_a_vsr_engine")


def test_avatar_engine_manager_live_subprocess_progress_and_cancellation(tmp_path):
    """Exercise REAL subprocess streaming, COLABONE_PROGRESS translation, manifest
    verification, and live task cancellation completely offline using tiny stub scripts."""
    mgr = ColabOneAvatarEngineManager(
        tmp_path / "remote",
        gpu_info_fn=lambda: {"name": "Blackwell", "total_mib": 98304, "used_mib": 0},
    )

    # Stub worker that reads --config, emits COLABONE_PROGRESS lines, writes output MKV + manifest.
    stub_py = tmp_path / "stub_worker.py"
    stub_py.write_text(
        "import json, sys\n"
        "from pathlib import Path\n"
        "cfg_path = Path(sys.argv[sys.argv.index('--config') + 1])\n"
        "cfg = json.loads(cfg_path.read_text(encoding='utf-8'))\n"
        "print('COLABONE_PROGRESS:' + json.dumps({'stage': 'init', 'progress': 0.25, 'message': 'step-25'}), flush=True)\n"
        "print('COLABONE_PROGRESS:' + json.dumps({'stage': 'render', 'progress': 0.75, 'message': 'step-75'}), flush=True)\n"
        "out_vid = Path(cfg['output_video_path'])\n"
        "out_vid.parent.mkdir(parents=True, exist_ok=True)\n"
        "out_vid.write_bytes(b'MKV-LOSSLESS-FAKE-BYTES')\n"
        "manifest = {'ok': True, 'output_video_path': str(out_vid), 'width': cfg.get('width', 1280), 'height': cfg.get('height', 768)}\n"
        "Path(cfg['output_manifest_path']).write_text(json.dumps(manifest), encoding='utf-8')\n",
        encoding="utf-8",
    )
    fake_python = _create_python_wrapper(tmp_path / "bin" / "fake_python", stub_py)

    # 1. Stage 2 run_longcat16 streaming progress + manifest validation.
    work_dir = tmp_path / "work_s2"
    s2_out = work_dir / "stage2_720p.mkv"
    task_s2 = ColabOneTask("comfyui_run", "stream-s2")
    manifest_s2 = mgr.run_longcat16(
        task_s2,
        config={
            "output_video_path": str(s2_out),
            "width": 1280,
            "height": 768,
            "exact_frames": 93,
        },
        runtime_paths={
            "python": fake_python,
            "repo_dir": tmp_path / "repo_lc",
            "avatar_dir": tmp_path / "w_avatar",
            "base_dir": tmp_path / "w_base",
            "compile_cache_dir": tmp_path / "inductor_cache",
        },
        work_dir=work_dir,
        progress_range=(0.30, 0.72),
    )
    assert manifest_s2["ok"] is True
    assert Path(manifest_s2["output_video_path"]) == s2_out
    assert s2_out.is_file()
    assert task_s2.progress == pytest.approx(0.72)
    logged_s2 = [entry["message"] for entry in task_s2.logs]
    assert "step-25" in logged_s2 and "step-75" in logged_s2

    # 2. Stage 3 run_diffusion_vsr streaming progress + manifest validation.
    work_s3 = tmp_path / "work_s3"
    s3_out = work_s3 / "stage3_upscaled.mkv"
    task_s3 = ColabOneTask("comfyui_run", "stream-s3")
    manifest_s3 = mgr.run_diffusion_vsr(
        task_s3,
        config={
            "engine": "seedvr2_7b_sharp",
            "input_video_path": str(s2_out),
            "output_video_path": str(s3_out),
            "target_width": 1920,
            "target_height": 1080,
        },
        runtime_paths={
            "python": fake_python,
            "repo_dir": tmp_path / "repo_vsr",
            "weights_dir": tmp_path / "w_vsr",
        },
        work_dir=work_s3,
        progress_range=(0.78, 0.94),
    )
    assert manifest_s3["ok"] is True
    assert Path(manifest_s3["output_video_path"]) == s3_out
    assert task_s3.progress == pytest.approx(0.94)
    logged_s3 = [entry["message"] for entry in task_s3.logs]
    assert "step-25" in logged_s3 and "step-75" in logged_s3

    # 3. Live cancellation terminates the streaming worker process cleanly.
    slow_py = tmp_path / "slow_worker.py"
    slow_py.write_text(
        "import json, time\n"
        "print('COLABONE_PROGRESS:' + json.dumps({'stage': 'render', 'progress': 0.1, 'message': 'started-slow'}), flush=True)\n"
        "time.sleep(30)\n",
        encoding="utf-8",
    )
    slow_python = _create_python_wrapper(tmp_path / "bin" / "slow_python", slow_py)
    task_cancel = ColabOneTask("comfyui_run", "stream-cancel")

    def _trigger_cancel() -> None:
        deadline = time.monotonic() + 5.0
        while time.monotonic() < deadline:
            if any(entry.get("message") == "started-slow" for entry in task_cancel.logs):
                break
            time.sleep(0.05)
        task_cancel.request_cancel()

    canceller = threading.Thread(target=_trigger_cancel, daemon=True)
    canceller.start()
    work_cancel = tmp_path / "work_cancel"
    started_at = time.monotonic()
    with pytest.raises(ColabOneCancelledError):
        mgr.run_longcat16(
            task_cancel,
            config={"output_video_path": str(work_cancel / "cancel.mkv")},
            runtime_paths={"python": slow_python},
            work_dir=work_cancel,
            progress_range=(0.30, 0.72),
            timeout=30,
        )
    canceller.join(timeout=5.0)
    assert time.monotonic() - started_at < 8.0


# ---------------------------------------------------------------------------
# 5. server/digital_human.py & server/comfyui.py
# ---------------------------------------------------------------------------


def test_parse_target_resolution_2k_and_compute_base_dimensions_native():
    assert parse_target_resolution("2560x1440") == (2560, 1440)
    assert parse_target_resolution("1440x2560") == (1440, 2560)
    assert parse_target_resolution("1920x1080") == (1920, 1080)
    assert parse_target_resolution("1080x1920") == (1080, 1920)
    assert parse_target_resolution("1280x720") == (1280, 720)
    assert parse_target_resolution("720x1280") == (720, 1280)

    assert compute_base_dimensions("longcat_avatar_native", 1920, 1080) == (1280, 768)
    assert compute_base_dimensions("longcat_avatar_native", 2560, 1440) == (1280, 768)
    assert compute_base_dimensions("longcat_avatar_native", 1280, 720) == (1280, 768)
    assert compute_base_dimensions("longcat_avatar_native", 1080, 1920) == (768, 1280)
    assert compute_base_dimensions("longcat_avatar_native", 1440, 2560) == (768, 1280)
    assert compute_base_dimensions("longcat_avatar_native", 720, 1280) == (768, 1280)
    # Near-square targets (aspect ratio within [0.85, 1.18]) map to the (960, 960) bucket.
    assert compute_base_dimensions("longcat_avatar_native", 1024, 1024) == (960, 960)
    assert compute_base_dimensions("longcat_avatar_native", 1080, 1080) == (960, 960)
    assert compute_base_dimensions("longcat_avatar_native", 1000, 900) == (960, 960)
    assert compute_base_dimensions("longcat_avatar_native", 900, 1000) == (960, 960)


def test_resolve_longcat_native_preset_matrix():
    # 1. All 3 quality presets.
    p_cinema = resolve_longcat_native_preset(
        {"pipeline.quality_preset": "cinema_flagship"}, target_w=1920, target_h=1080
    )
    assert p_cinema["sampling_mode"] == "non_distill_cfg"
    assert p_cinema["upscale_engine"] == "seedvr2_7b_sharp"
    assert p_cinema["num_inference_steps"] == 50
    assert p_cinema["audio_guidance_scale"] == pytest.approx(4.0)
    assert p_cinema["text_guidance_scale"] == pytest.approx(4.0)
    assert p_cinema["mix_steps"] == 0
    assert p_cinema["base_width"] == 1280 and p_cinema["base_height"] == 768
    assert p_cinema["resolution"] == "720p"
    assert p_cinema["crf"] == 16

    p_balanced = resolve_longcat_native_preset(
        {"pipeline.quality_preset": "balanced_hd"}, target_w=1080, target_h=1920
    )
    assert p_balanced["sampling_mode"] == "distill_lora_mix"
    assert p_balanced["upscale_engine"] == "seedvr2_7b"
    assert p_balanced["num_inference_steps"] == 8
    assert p_balanced["audio_guidance_scale"] == pytest.approx(1.0)
    assert p_balanced["text_guidance_scale"] == pytest.approx(1.0)
    assert p_balanced["mix_steps"] == 4
    assert p_balanced["mix_alpha"] == pytest.approx(1.0)
    assert p_balanced["base_width"] == 768 and p_balanced["base_height"] == 1280

    p_fast = resolve_longcat_native_preset(
        {"pipeline.quality_preset": "longform_fast"}, target_w=2560, target_h=1440
    )
    assert p_fast["sampling_mode"] == "distill_lora_mix"
    assert p_fast["upscale_engine"] == "flashvsr_plus"
    assert p_fast["num_inference_steps"] == 8
    assert p_fast["mix_steps"] == 4

    # 2. All 3 framing styles map to their distinct canonical prompts.
    seen_prompts: set[str] = set()
    for style in ("half_body_gestures", "closeup_presenter", "full_body_dynamic"):
        res = resolve_longcat_native_preset(
            {"pipeline.framing_style": style}, target_w=1920, target_h=1080
        )
        assert res["framing_style"] == style
        assert res["positive_prompt"] == COLABONE_LONGCAT_NATIVE_FRAMING_PROMPTS[style]
        assert res["negative_prompt"] == COLABONE_LONGCAT_NATIVE_DEFAULT_NEGATIVE_PROMPT
        seen_prompts.add(res["positive_prompt"])
    assert len(seen_prompts) == 3

    # 3. Explicit upscale, sampling, CRF clamping, and custom prompt overrides.
    custom = resolve_longcat_native_preset(
        {
            "pipeline.quality_preset": "cinema_flagship",
            "pipeline.upscale_mode": "flashvsr_plus",
            "pipeline.sampling_mode": "distill_lora_mix",
            "pipeline.mix_steps": 6,
            "pipeline.mix_alpha": 0.75,
            "pipeline.crf": 5,
            "pipeline.ref_img_index": 50,
            "pipeline.positive_prompt": "Custom studio host speaking calmly",
            "pipeline.negative_prompt": "custom negative",
        },
        target_w=1920,
        target_h=1080,
    )
    assert custom["upscale_mode"] == "flashvsr_plus"
    assert custom["upscale_engine"] == "flashvsr_plus"
    assert custom["sampling_mode"] == "distill_lora_mix"
    assert custom["num_inference_steps"] == 8
    assert custom["mix_steps"] == 6
    assert custom["mix_alpha"] == pytest.approx(0.75)
    assert custom["crf"] == 12  # clamped up from 5 to 12
    assert custom["ref_img_index"] == 30  # clamped down from 50 to 30
    assert custom["positive_prompt"] == "Custom studio host speaking calmly"
    assert custom["negative_prompt"] == "custom negative"

    clamped_hi = resolve_longcat_native_preset(
        {
            "pipeline.quality_preset": "longform_fast",
            "pipeline.sampling_mode": "non_distill_cfg",
            "pipeline.upscale_mode": "none",
            "pipeline.num_inference_steps": 35,
            "pipeline.audio_guidance_scale": 4.5,
            "pipeline.crf": 30,
        },
        target_w=1280,
        target_h=720,
    )
    assert clamped_hi["upscale_engine"] == "none"
    assert clamped_hi["sampling_mode"] == "non_distill_cfg"
    assert clamped_hi["num_inference_steps"] == 35
    assert clamped_hi["audio_guidance_scale"] == pytest.approx(4.5)
    assert clamped_hi["crf"] == 24  # clamped down from 30 to 24

    # 4. upscale_mode="auto" at 720p / 960x960 skips Stage 3 VSR completely.
    for tw, th in ((1280, 720), (720, 1280), (960, 960)):
        auto_720 = resolve_longcat_native_preset(
            {"pipeline.quality_preset": "cinema_flagship", "pipeline.upscale_mode": "auto"},
            target_w=tw,
            target_h=th,
        )
        assert auto_720["upscale_mode"] == "auto"
        assert auto_720["upscale_engine"] == "none"
        assert (auto_720["target_width"], auto_720["target_height"]) == (tw, th)

    # 5. upscale_mode="none" with larger target keeps native 720p without Lanczos upscaling.
    for req_dims, expected_dims in (
        ((1920, 1080), (1280, 720)),
        ((2560, 1440), (1280, 720)),
        ((1080, 1920), (720, 1280)),
        ((1440, 2560), (720, 1280)),
        ((1200, 1200), (960, 960)),
    ):
        no_up = resolve_longcat_native_preset(
            {"pipeline.quality_preset": "cinema_flagship", "pipeline.upscale_mode": "none"},
            target_w=req_dims[0],
            target_h=req_dims[1],
        )
        assert no_up["upscale_mode"] == "none"
        assert no_up["upscale_engine"] == "none"
        assert (no_up["target_width"], no_up["target_height"]) == expected_dims


def test_longcat_native_runner_e2e_seedvr2_flashvsr_and_skip_vsr(runtime, tmp_path, monkeypatch):
    catalog = _load_real_comfy_catalog()
    longcat_wf = next(t for t in catalog.templates if t.id == "digital-human-longcat").model_dump()

    portrait = tmp_path / "anchor.png"
    portrait.write_bytes(_make_png_bytes("teal", size=(128, 128)))
    uploaded_wav = _write_pcm16_wav(tmp_path / "uploaded_4s.wav", duration_sec=4.0, sample_rate=48000)

    # Guardrail: ComfyUI MUST NEVER be started during longcat_avatar_native runs.
    monkeypatch.setattr(
        runtime.comfy,
        "start_server",
        lambda *a, **k: pytest.fail("ComfyUI start_server must NEVER be called for longcat_avatar_native"),
    )
    monkeypatch.setattr(
        runtime.comfy,
        "prepare",
        lambda *a, **k: pytest.fail("ComfyUI prepare must NEVER be called for longcat_avatar_native"),
    )

    # ------------------------------------------------------------------
    # Scenario A: cinema_flagship + TTS + SeedVR2 7B Sharp + running ComfyUI stopped
    # ------------------------------------------------------------------
    comfy_stopped = {"count": 0}
    comfy_state = {"running": True}
    monkeypatch.setattr(runtime.comfy, "is_running", lambda: comfy_state["running"])

    def fake_stop_server():
        comfy_stopped["count"] += 1
        comfy_state["running"] = False

    monkeypatch.setattr(runtime.comfy, "stop_server", fake_stop_server)
    monkeypatch.setattr(runtime.comfy, "free_memory", lambda unload_models=True: {"ok": True})

    tts_calls: list[dict[str, Any]] = []
    fake_tts = Mock()
    fake_tts.synth_fn = lambda *a, **k: None
    fake_tts.get_engine.return_value = {"id": "fish_drama3", "sample_rate": 44100}
    fake_tts.validate_tts_synthesis_inputs.return_value = {
        "engine": {"id": "fish_drama3", "sample_rate": 44100},
        "effective_voice_preset": "shen_zhiyi",
    }

    def fake_tts_synthesize(task, **kwargs):
        tts_calls.append(dict(kwargs))
        tts_wav = Path(kwargs["work_dir"]) / "tts_master.wav"
        _write_pcm16_wav(tts_wav, duration_sec=4.0, sample_rate=48000)
        return {
            "audio_path": str(tts_wav),
            "duration": 4.0,
            "sample_rate": 48000,
            "manifest": {"sample_rate": 48000, "silence_valleys": []},
        }

    fake_tts.synthesize.side_effect = fake_tts_synthesize

    events: list[str] = []
    captured_s2_cfg: list[dict[str, Any]] = []
    captured_s3_cfg: list[dict[str, Any]] = []
    created_mkvs: list[Path] = []
    ffmpeg_cmds: list[list[str]] = []

    class _FakeAvatarEngines:
        def check_preflight(self, **kwargs):
            events.append("preflight")
            return {
                "name": "NVIDIA RTX PRO 6000 Blackwell",
                "total_mib": 98304,
                "is_high_vram_96g": True,
                "all_weights_cached": True,
                "upscale_mode": kwargs.get("upscale_mode"),
            }

        def ensure_longcat16_ready(self, task, **kwargs):
            events.append(f"ensure_longcat16:{kwargs.get('sampling_mode')}")
            return {
                "python": tmp_path / "lc_python",
                "repo_dir": tmp_path / "lc_repo",
                "avatar_dir": tmp_path / "lc_avatar",
                "base_dir": tmp_path / "lc_base",
                "compile_cache_dir": tmp_path / "lc_cache",
            }

        def run_longcat16(self, task, *, config, runtime_paths, work_dir, progress_range):
            events.append("run_longcat16")
            assert comfy_state["running"] is False, "ComfyUI must be stopped before Stage 2"
            captured_s2_cfg.append(dict(config))
            out_p = Path(config["output_video_path"])
            out_p.write_bytes(b"STAGE2-LOSSLESS-FFV1-MKV")
            created_mkvs.append(out_p)
            return {
                "ok": True,
                "output_video_path": str(out_p),
                "width": config["width"],
                "height": config["height"],
            }

        def ensure_vsr_ready(self, task, *, engine, hf_token="", progress_range=(0.72, 0.78)):
            events.append(f"ensure_vsr:{engine}")
            return {
                "python": tmp_path / "vsr_python",
                "repo_dir": tmp_path / "vsr_repo",
                "weights_dir": tmp_path / "vsr_weights",
            }

        def run_diffusion_vsr(self, task, *, config, runtime_paths, work_dir, progress_range):
            events.append(f"run_vsr:{config['engine']}")
            captured_s3_cfg.append(dict(config))
            out_p = Path(config["output_video_path"])
            out_p.write_bytes(b"STAGE3-LOSSLESS-VSR-MKV")
            created_mkvs.append(out_p)
            return {
                "ok": True,
                "output_video_path": str(out_p),
                "width": config["target_width"],
                "height": config["target_height"],
            }

    def fake_ffmpeg(args, task=None, log=None, **kwargs):
        cmd = [str(x) for x in args]
        ffmpeg_cmds.append(cmd)
        out = Path(cmd[-1])
        out.parent.mkdir(parents=True, exist_ok=True)
        out.write_bytes(b"\x00\x00\x00\x18ftypmp42colabone-native-master")
        return ""

    runner = ColabOneDigitalHumanRunner(
        runtime.comfy,
        fake_tts,
        run_cmd_fn=fake_ffmpeg,
        avatar_engines=_FakeAvatarEngines(),
    )

    task_a = ColabOneTask("comfyui_run", "native-e2e-a")
    req_a = WorkflowRunRequest(
        workflow_id="digital-human-longcat",
        overrides={
            "pipeline.audio_source": "tts",
            "pipeline.tts_engine": "fish_drama3",
            "pipeline.script_text": "大家好，这是 96GB 电影级旗舰原生管线测试。",
            "pipeline.voice_preset": "shen_zhiyi",
            "pipeline.quality_preset": "cinema_flagship",
            "pipeline.framing_style": "half_body_gestures",
            "pipeline.upscale_mode": "auto",
            "pipeline.target_resolution": "1920x1080",
        },
        inputs={"1.image": "a" * 32},
    )
    outputs_a = runner.run(task_a, longcat_wf, req_a, [("1.image", "anchor.png", portrait)])

    assert len(outputs_a) == 1
    assert outputs_a[0]["extension"] == "mp4"
    assert outputs_a[0]["workflow_id"] == "digital-human-longcat"
    assert outputs_a[0]["id"] in runtime.comfy.store.outputs
    assert comfy_stopped["count"] == 1
    assert len(tts_calls) == 1
    assert events == [
        "preflight",
        "ensure_longcat16:non_distill_cfg",
        "run_longcat16",
        "ensure_vsr:seedvr2_7b_sharp",
        "run_vsr:seedvr2_7b_sharp",
    ]
    assert captured_s2_cfg[0]["resolution"] == "720p"
    assert captured_s2_cfg[0]["width"] == 1280 and captured_s2_cfg[0]["height"] == 768
    assert captured_s2_cfg[0]["is_tts_audio"] is True
    assert captured_s2_cfg[0]["sampling_mode"] == "non_distill_cfg"
    assert captured_s2_cfg[0]["num_inference_steps"] == 50
    assert captured_s2_cfg[0]["audio_guidance_scale"] == pytest.approx(4.0)
    assert captured_s2_cfg[0]["speakers"] == [
        {"id": "person1", "audio_path": captured_s2_cfg[0]["audio_path"]}
    ]
    assert captured_s3_cfg[0]["engine"] == "seedvr2_7b_sharp"
    assert captured_s3_cfg[0]["target_width"] == 1920
    assert captured_s3_cfg[0]["target_height"] == 1080
    assert captured_s3_cfg[0]["vram_mib"] == 98304

    # Verify Stage 4 single-pass FFmpeg master encode arguments (BT.709 matrix + VUI flags).
    assert len(ffmpeg_cmds) == 1
    ff_a = ffmpeg_cmds[0]
    assert ff_a[ff_a.index("-vf") + 1] == "scale=out_color_matrix=bt709:out_range=tv,setsar=1"
    assert ff_a[ff_a.index("-colorspace") + 1] == "bt709"
    assert ff_a[ff_a.index("-color_primaries") + 1] == "bt709"
    assert ff_a[ff_a.index("-color_trc") + 1] == "bt709"
    assert ff_a[ff_a.index("-color_range") + 1] == "tv"
    assert ff_a[ff_a.index("-c:v") + 1] == "libx264"
    assert ff_a[ff_a.index("-crf") + 1] == "16"
    assert ff_a[ff_a.index("-c:a") + 1] == "aac"
    assert ff_a[ff_a.index("-b:a") + 1] == "256k"
    assert "+faststart" in ff_a
    assert runtime.comfy.status()["active_pack"] == "digital-human-longcat"
    # Verify all intermediate lossless MKVs were cleaned up.
    assert len(created_mkvs) == 2
    assert all(not p.exists() for p in created_mkvs)

    # ------------------------------------------------------------------
    # Scenario B: longform_fast + uploaded audio + FlashVSR+ 2K + custom CRF 14
    # ------------------------------------------------------------------
    events.clear()
    captured_s2_cfg.clear()
    captured_s3_cfg.clear()
    created_mkvs.clear()
    ffmpeg_cmds.clear()

    task_b = ColabOneTask("comfyui_run", "native-e2e-b")
    req_b = WorkflowRunRequest(
        workflow_id="digital-human-longcat",
        overrides={
            "pipeline.audio_source": "audio",
            "pipeline.quality_preset": "longform_fast",
            "pipeline.framing_style": "full_body_dynamic",
            "pipeline.upscale_mode": "auto",
            "pipeline.target_resolution": "2560x1440",
            "pipeline.crf": 14,
        },
        inputs={"1.image": "a" * 32, "2.audio": "b" * 32},
    )
    outputs_b = runner.run(
        task_b,
        longcat_wf,
        req_b,
        [("1.image", "anchor.png", portrait), ("2.audio", "uploaded_4s.wav", uploaded_wav)],
    )
    assert len(outputs_b) == 1
    assert events == [
        "preflight",
        "ensure_longcat16:distill_lora_mix",
        "run_longcat16",
        "ensure_vsr:flashvsr_plus",
        "run_vsr:flashvsr_plus",
    ]
    assert captured_s2_cfg[0]["is_tts_audio"] is False
    assert captured_s2_cfg[0]["sampling_mode"] == "distill_lora_mix"
    assert captured_s2_cfg[0]["num_inference_steps"] == 8
    assert captured_s2_cfg[0]["mix_steps"] == 4
    assert captured_s3_cfg[0]["engine"] == "flashvsr_plus"
    assert captured_s3_cfg[0]["target_width"] == 2560
    assert captured_s3_cfg[0]["target_height"] == 1440
    assert ffmpeg_cmds[-1][ffmpeg_cmds[-1].index("-crf") + 1] == "14"
    assert all(not p.exists() for p in created_mkvs)

    # ------------------------------------------------------------------
    # Scenario C: upscale_mode="none" skips Stage 3 entirely and crops 1280x768 to 1280x720
    # ------------------------------------------------------------------
    events.clear()
    captured_s2_cfg.clear()
    captured_s3_cfg.clear()
    created_mkvs.clear()
    ffmpeg_cmds.clear()

    task_c = ColabOneTask("comfyui_run", "native-e2e-c")
    req_c = WorkflowRunRequest(
        workflow_id="digital-human-longcat",
        overrides={
            "pipeline.audio_source": "audio",
            "pipeline.quality_preset": "cinema_flagship",
            "pipeline.upscale_mode": "none",
            "pipeline.target_resolution": "1280x720",
        },
        inputs={"1.image": "a" * 32, "2.audio": "b" * 32},
    )
    outputs_c = runner.run(
        task_c,
        longcat_wf,
        req_c,
        [("1.image", "anchor.png", portrait), ("2.audio", "uploaded_4s.wav", uploaded_wav)],
    )
    assert len(outputs_c) == 1
    assert events == [
        "preflight",
        "ensure_longcat16:non_distill_cfg",
        "run_longcat16",
    ]
    assert captured_s3_cfg == []
    ff_c = ffmpeg_cmds[-1]
    assert (
        "-vf" in ff_c
        and ff_c[ff_c.index("-vf") + 1]
        == "crop=1280:720,scale=out_color_matrix=bt709:out_range=tv,setsar=1"
    )
    assert ff_c[ff_c.index("-colorspace") + 1] == "bt709"
    assert ff_c[ff_c.index("-color_primaries") + 1] == "bt709"
    assert ff_c[ff_c.index("-color_trc") + 1] == "bt709"
    assert ff_c[ff_c.index("-color_range") + 1] == "tv"
    assert len(created_mkvs) == 1
    assert not created_mkvs[0].exists()


def test_comfy_manager_ensure_template_ready_delegates_longcat_native(runtime, monkeypatch):
    catalog = _load_real_comfy_catalog()
    longcat_wf = next(t for t in catalog.templates if t.id == "digital-human-longcat").model_dump()

    monkeypatch.setattr(
        runtime.comfy,
        "start_server",
        lambda *a, **k: pytest.fail("ensure_template_ready must NOT start ComfyUI for longcat_avatar_native"),
    )
    monkeypatch.setattr(
        runtime.comfy,
        "prepare",
        lambda *a, **k: pytest.fail("ensure_template_ready must NOT prepare ComfyUI for longcat_avatar_native"),
    )
    monkeypatch.setattr(
        runtime.comfy.packs,
        "ensure_pack",
        lambda *a, **k: pytest.fail("ensure_template_ready must NOT invoke packs for longcat_avatar_native"),
    )

    calls: dict[str, Any] = {}
    fake_engines = Mock()
    fake_engines.check_preflight.side_effect = lambda **kw: calls.__setitem__("preflight", kw)
    fake_engines.ensure_longcat16_ready.side_effect = lambda task, **kw: calls.__setitem__("ensure", kw)
    fake_engines.ensure_vsr_ready.side_effect = lambda task, **kw: calls.__setitem__("ensure_vsr", kw)

    runtime.comfy.avatar_engines = fake_engines
    runtime.comfy.digital_human._avatar_engines = None

    task = ColabOneTask("comfyui_run", "ensure-native")
    runtime.comfy.ensure_template_ready(
        task,
        longcat_wf,
        hf_token="hf_delegate_token",
        progress_range=(0.20, 0.30),
    )

    assert calls["preflight"] == {
        "min_vram_mib": 49152,
        "min_disk_mib": 79872,
        "upscale_mode": "seedvr2_7b_sharp",
    }
    assert calls["ensure"] == {
        "hf_token": "hf_delegate_token",
        "sampling_mode": "non_distill_cfg",
        "vocal_separation": "auto",
        "progress_range": (pytest.approx(0.20), pytest.approx(0.272)),
    }
    assert calls["ensure_vsr"] == {
        "engine": "seedvr2_7b_sharp",
        "hf_token": "hf_delegate_token",
        "progress_range": (pytest.approx(0.272), pytest.approx(0.30)),
    }
    assert runtime.comfy.status()["active_pack"] == "digital-human-longcat"


# ---------------------------------------------------------------------------
# 6. Real cross-module wiring & adversarial edge-case integration tests
# ---------------------------------------------------------------------------


def _write_stereo_pcm16_wav(
    path: Path,
    duration_sec: float = 4.0,
    sample_rate: int = 48000,
    left_amp: int = 4000,
    right_amp: int = 18000,
) -> Path:
    path.parent.mkdir(parents=True, exist_ok=True)
    one_sec = bytearray()
    for i in range(sample_rate):
        angle = 2.0 * math.pi * 440.0 * i / sample_rate
        l_val = int(round(left_amp * math.sin(angle)))
        r_val = int(round(right_amp * math.cos(angle)))
        one_sec.extend(struct.pack("<hh", l_val, r_val))
    total_frames = int(round(duration_sec * sample_rate))
    full_secs, rem = divmod(total_frames, sample_rate)
    pcm = (bytes(one_sec) * full_secs) + bytes(one_sec[: rem * 4])
    with wave.open(str(path), "wb") as wf:
        wf.setnchannels(2)
        wf.setsampwidth(2)
        wf.setframerate(sample_rate)
        wf.writeframes(pcm)
    return path


def _install_stub_ffmpeg_and_ffprobe(bin_dir: Path, audit_log: Path) -> tuple[Path, Path]:
    """Install lightweight pure-Python executable ffmpeg and sibling ffprobe stubs."""
    bin_dir.mkdir(parents=True, exist_ok=True)

    ffmpeg_script = bin_dir / "_stub_ffmpeg.py"
    ffmpeg_script.write_text(
        f"""import json, os, re, struct, sys, time, wave
from pathlib import Path

AUDIT = Path({str(audit_log)!r})
args = sys.argv[1:]
AUDIT.parent.mkdir(parents=True, exist_ok=True)

def _append_audit(payload):
    line = (json.dumps(payload, ensure_ascii=False) + "\\n").encode("utf-8")
    lock_path = AUDIT.with_name(AUDIT.name + ".lock")
    with lock_path.open("a+b") as lock_fh:
        if os.name == "nt":
            import msvcrt
            while True:
                try:
                    lock_fh.seek(0)
                    msvcrt.locking(lock_fh.fileno(), msvcrt.LK_NBLCK, 1)
                    break
                except OSError:
                    time.sleep(0.01)
            try:
                with AUDIT.open("ab") as fh:
                    fh.seek(0, os.SEEK_END)
                    fh.write(line)
                    fh.flush()
            finally:
                lock_fh.seek(0)
                msvcrt.locking(lock_fh.fileno(), msvcrt.LK_UNLCK, 1)
        else:
            import fcntl
            fcntl.flock(lock_fh.fileno(), fcntl.LOCK_EX)
            try:
                with AUDIT.open("ab") as fh:
                    fh.seek(0, os.SEEK_END)
                    fh.write(line)
                    fh.flush()
            finally:
                fcntl.flock(lock_fh.fileno(), fcntl.LOCK_UN)

_append_audit({{"tool": "ffmpeg", "argv": args}})

if "-h" in args:
    sys.stdout.write("ffmpeg stub\\n  -fps_mode mode\\n")
    sys.exit(0)

out_arg = args[-1] if args else ""

def _arg_after(flag, default=""):
    for i, tok in enumerate(args):
        if tok == flag and i + 1 < len(args):
            return args[i + 1]
    return default

if out_arg == "pipe:1":
    fmt = _arg_after("-f")
    if fmt == "f32le":
        sr = int(_arg_after("-ar", "16000") or "16000")
        count = sr * 5
        sys.stdout.buffer.write(struct.pack("<f", 0.1) * count)
        sys.stdout.buffer.flush()
        sys.exit(0)
    if fmt == "rawvideo":
        vf = _arg_after("-vf")
        m_scale = re.search(r"scale=(\\d+):(\\d+)", vf)
        w, h = (int(m_scale.group(1)), int(m_scale.group(2))) if m_scale else (80, 64)
        nframes = int(_arg_after("-frames:v", "10") or "10")
        frame = bytes([180, 140, 100]) * (w * h)
        for _ in range(nframes):
            sys.stdout.buffer.write(frame)
        sys.stdout.buffer.flush()
        sys.exit(0)
    sys.exit(0)

out_path = Path(out_arg)
out_path.parent.mkdir(parents=True, exist_ok=True)

in_arg = _arg_after("-i")
vf = _arg_after("-vf")
m_crop = re.search(r"crop=(\\d+):(\\d+)", vf)

if in_arg in ("-", "pipe:0"):
    raw = sys.stdin.buffer.read()
    size_str = _arg_after("-s", "80x64")
    w_s, h_s = size_str.split("x", 1)
    in_w, in_h = int(w_s), int(h_s)
    frame_bytes = max(1, in_w * in_h * 3)
    frames = len(raw) // frame_bytes
    cap = _arg_after("-frames:v")
    if cap and cap.isdigit():
        frames = min(frames, int(cap))
    out_w, out_h = (int(m_crop.group(1)), int(m_crop.group(2))) if m_crop else (in_w, in_h)
    meta = {{"w": out_w, "h": out_h, "frames": frames}}
    out_path.write_bytes(b"COLABONE_FAKE_MKV:" + json.dumps(meta).encode("utf-8"))
    sys.exit(0)

if out_path.suffix.lower() == ".mkv":
    out_w, out_h = (int(m_crop.group(1)), int(m_crop.group(2))) if m_crop else (1920, 1080)
    nframes = int(_arg_after("-frames:v", "10") or "10")
    meta = {{"w": out_w, "h": out_h, "frames": nframes}}
    out_path.write_bytes(b"COLABONE_FAKE_MKV:" + json.dumps(meta).encode("utf-8"))
    sys.exit(0)

if out_path.suffix.lower() == ".wav":
    ch = int(_arg_after("-ac", "2") or "2")
    sr = int(_arg_after("-ar", "48000") or "48000")
    with wave.open(str(out_path), "wb") as wf:
        wf.setnchannels(ch)
        wf.setsampwidth(2)
        wf.setframerate(sr)
        wf.writeframes(b"\\x10\\x00" * (sr * ch))
    sys.exit(0)

out_path.write_bytes(b"\\x00\\x00\\x00\\x18ftypmp42colabone-stub-mp4")
sys.exit(0)
""",
        encoding="utf-8",
    )

    ffprobe_script = bin_dir / "_stub_ffprobe.py"
    ffprobe_script.write_text(
        f"""import json, os, sys, time
from pathlib import Path

AUDIT = Path({str(audit_log)!r})
args = sys.argv[1:]
AUDIT.parent.mkdir(parents=True, exist_ok=True)

def _append_audit(payload):
    line = (json.dumps(payload, ensure_ascii=False) + "\\n").encode("utf-8")
    lock_path = AUDIT.with_name(AUDIT.name + ".lock")
    with lock_path.open("a+b") as lock_fh:
        if os.name == "nt":
            import msvcrt
            while True:
                try:
                    lock_fh.seek(0)
                    msvcrt.locking(lock_fh.fileno(), msvcrt.LK_NBLCK, 1)
                    break
                except OSError:
                    time.sleep(0.01)
            try:
                with AUDIT.open("ab") as fh:
                    fh.seek(0, os.SEEK_END)
                    fh.write(line)
                    fh.flush()
            finally:
                lock_fh.seek(0)
                msvcrt.locking(lock_fh.fileno(), msvcrt.LK_UNLCK, 1)
        else:
            import fcntl
            fcntl.flock(lock_fh.fileno(), fcntl.LOCK_EX)
            try:
                with AUDIT.open("ab") as fh:
                    fh.seek(0, os.SEEK_END)
                    fh.write(line)
                    fh.flush()
            finally:
                fcntl.flock(lock_fh.fileno(), fcntl.LOCK_UN)

_append_audit({{"tool": "ffprobe", "argv": args}})

target = Path(args[-1])
raw = target.read_bytes()
prefix = b"COLABONE_FAKE_MKV:"
if not raw.startswith(prefix):
    sys.stderr.write("not a stub mkv\\n")
    sys.exit(1)
meta = json.loads(raw[len(prefix):].decode("utf-8"))
payload = {{
    "streams": [
        {{
            "width": int(meta["w"]),
            "height": int(meta["h"]),
            "nb_read_packets": str(int(meta["frames"])),
            "nb_frames": str(int(meta["frames"])),
        }}
    ]
}}
sys.stdout.write(json.dumps(payload))
sys.stdout.flush()
""",
        encoding="utf-8",
    )

    ffmpeg_bin = _create_python_wrapper(bin_dir / "ffmpeg", ffmpeg_script)
    ffprobe_bin = _create_python_wrapper(bin_dir / "ffprobe", ffprobe_script)
    return ffmpeg_bin, ffprobe_bin


def _install_fake_ml_site_and_repos(
    base_dir: Path,
    trace_file: Path,
    *,
    ai2v_height: int = 64,
    ai2v_width: int = 80,
) -> dict[str, Path]:
    """Create lightweight stdlib-only fake numpy/torch/longcat_video/FlashVSR_Plus/SeedVR2 stubs."""
    fake_site = base_dir / "fake_site"
    fake_site.mkdir(parents=True, exist_ok=True)

    # 1. Fake numpy with C-speed bytes backing + __array_interface__ for Pillow.
    (fake_site / "numpy.py").write_text(
        """import math, random as _random

float32 = "float32"
uint8 = "uint8"
int32 = "int32"

class _RandomMod:
    def seed(self, s):
        _random.seed(int(s))

random = _RandomMod()

def _prod(shape):
    n = 1
    for d in shape:
        n *= int(d)
    return n

class ndarray:
    def __init__(self, buf, shape, dtype=uint8):
        self._buf = buf
        self.shape = tuple(int(d) for d in shape)
        self.dtype = dtype

    @property
    def ndim(self):
        return len(self.shape)

    @property
    def size(self):
        return _prod(self.shape)

    def __len__(self):
        return self.shape[0] if self.shape else 0

    def _item_size(self):
        return 4 if self.dtype == float32 else 1

    def reshape(self, *new_shape):
        if len(new_shape) == 1 and isinstance(new_shape[0], (tuple, list)):
            new_shape = tuple(new_shape[0])
        shape = list(int(d) for d in new_shape)
        if -1 in shape:
            idx = shape.index(-1)
            known = 1
            for i, d in enumerate(shape):
                if i != idx:
                    known *= d
            shape[idx] = self.size // max(1, known)
        return ndarray(self._buf, tuple(shape), self.dtype)

    def copy(self):
        return ndarray(bytes(self._buf), self.shape, self.dtype)

    def astype(self, dtype, copy=True):
        if dtype == self.dtype and not copy:
            return self
        return ndarray(bytes(self._buf), self.shape, dtype)

    def tobytes(self):
        return bytes(self._buf)

    @property
    def __array_interface__(self):
        strides = None
        if len(self.shape) == 3:
            strides = (self.shape[1] * self.shape[2], self.shape[2], 1)
        return {
            "shape": self.shape,
            "typestr": "|u1" if self.dtype == uint8 else "<f4",
            "strides": strides,
            "data": self._buf,
            "version": 3,
        }

    def __getitem__(self, key):
        stride = _prod(self.shape[1:]) * self._item_size()
        n0 = self.shape[0]
        if isinstance(key, int):
            idx = key if key >= 0 else n0 + key
            start = idx * stride
            return ndarray(self._buf[start : start + stride], self.shape[1:], self.dtype)
        if isinstance(key, slice):
            s_idx, e_idx, step = key.indices(n0)
            if step == 1:
                sub = self._buf[s_idx * stride : e_idx * stride]
                return ndarray(sub, (max(0, e_idx - s_idx), *self.shape[1:]), self.dtype)
            parts = [self._buf[i * stride : (i + 1) * stride] for i in range(s_idx, e_idx, step)]
            return ndarray(b"".join(parts), (len(parts), *self.shape[1:]), self.dtype)
        raise TypeError(f"Unsupported slice {key!r}")

    def __iter__(self):
        for i in range(self.shape[0]):
            yield self[i]

    def all(self):
        return True

def frombuffer(buf, dtype=uint8):
    raw = bytes(buf)
    item = 4 if dtype == float32 else 1
    return ndarray(raw, (len(raw) // item,), dtype=dtype)

def zeros(shape, dtype=float32):
    if isinstance(shape, int):
        shape = (shape,)
    item = 4 if dtype == float32 else 1
    return ndarray(b"\\x00" * (_prod(shape) * item), shape, dtype=dtype)

def concatenate(arrays, axis=0):
    first = arrays[0]
    joined = b"".join(a._buf for a in arrays)
    total0 = sum(a.shape[0] for a in arrays)
    return ndarray(joined, (total0, *first.shape[1:]), dtype=first.dtype)

def stack(arrays, axis=0):
    first = arrays[0]
    joined = b"".join(a._buf for a in arrays)
    return ndarray(joined, (len(arrays), *first.shape), dtype=first.dtype)

def repeat(arr, repeats, axis=0):
    return ndarray(arr._buf * int(repeats), (arr.shape[0] * int(repeats), *arr.shape[1:]), dtype=arr.dtype)

def asarray(obj, dtype=None):
    if isinstance(obj, ndarray):
        if dtype is not None and dtype != obj.dtype:
            return obj.astype(dtype, copy=False)
        return obj
    if isinstance(obj, (bytes, bytearray, memoryview)):
        raw = bytes(obj)
        dt = dtype or uint8
        item = 4 if dt == float32 else 1
        return ndarray(raw, (len(raw) // item,), dtype=dt)
    if isinstance(obj, list):
        return zeros((len(obj),), dtype=dtype or float32)
    return zeros((1,), dtype=dtype or float32)

def ascontiguousarray(obj):
    arr = asarray(obj)
    if arr.ndim == 3 and arr.dtype == uint8:
        return memoryview(arr._buf)
    return arr

def isfinite(arr):
    return arr

def clip(arr, lo, hi):
    return arr
""",
        encoding="utf-8",
    )

    # 2. Fake torch + torch.distributed + torch.nn.functional
    torch_dir = fake_site / "torch"
    (torch_dir / "nn").mkdir(parents=True, exist_ok=True)
    (torch_dir / "__init__.py").write_text(
        """import contextlib, os
import numpy as np

bfloat16 = "bfloat16"
float32 = "float32"
uint8 = "uint8"
int32 = "int32"

class device:
    def __init__(self, spec="cuda:0"):
        self._spec = str(spec)
        self.type = self._spec.split(":", 1)[0]

    def __str__(self):
        return self._spec

class Generator:
    def __init__(self, device=None):
        self.device = device
        self._state = 0

    def manual_seed(self, s):
        self._state = int(s)
        return self

    def get_state(self):
        return self._state

    def set_state(self, st):
        self._state = st

class FakeTensor:
    def __init__(self, shape, dtype=float32, fill=0.0):
        self.shape = tuple(int(d) for d in shape)
        self.dtype = dtype
        self.fill = float(fill)

    @property
    def ndim(self):
        return len(self.shape)

    def detach(self):
        return self

    def cpu(self):
        return self

    def to(self, *args, **kwargs):
        dt = kwargs.get("dtype", self.dtype)
        return FakeTensor(self.shape, dtype=dt, fill=self.fill)

    def contiguous(self):
        return self

    def clone(self):
        return FakeTensor(self.shape, dtype=self.dtype, fill=self.fill)

    def unsqueeze(self, dim):
        d = dim if dim >= 0 else len(self.shape) + 1 + dim
        s = list(self.shape)
        s.insert(d, 1)
        return FakeTensor(tuple(s), dtype=self.dtype, fill=self.fill)

    def permute(self, *dims):
        return FakeTensor(tuple(self.shape[d] for d in dims), dtype=self.dtype, fill=self.fill)

    def div_(self, val):
        self.fill = self.fill / float(val)
        return self

    def sub_(self, val):
        self.fill = self.fill - float(val)
        return self

    def clamp_(self, lo, hi):
        self.fill = max(float(lo), min(float(hi), self.fill))
        return self

    def clamp(self, lo, hi):
        return FakeTensor(self.shape, dtype=self.dtype, fill=max(float(lo), min(float(hi), self.fill)))

    def round(self):
        return FakeTensor(self.shape, dtype=self.dtype, fill=round(self.fill))

    def __add__(self, other):
        if isinstance(other, FakeTensor):
            s = self.shape if len(self.shape) >= len(other.shape) else other.shape
            return FakeTensor(s, dtype=self.dtype, fill=self.fill + other.fill)
        return FakeTensor(self.shape, dtype=self.dtype, fill=self.fill + float(other))

    def __mul__(self, other):
        return FakeTensor(self.shape, dtype=self.dtype, fill=self.fill * float(other))

    def __sub__(self, other):
        return FakeTensor(self.shape, dtype=self.dtype, fill=self.fill - float(other))

    def __getitem__(self, key):
        if not isinstance(key, tuple):
            key = (key,)
        out_shape = []
        axis = 0
        for item in key:
            if item is None:
                out_shape.append(1)
            elif item is Ellipsis:
                break
            elif isinstance(item, int):
                axis += 1
            elif isinstance(item, slice):
                s_i, e_i, _ = item.indices(self.shape[axis])
                out_shape.append(max(0, e_i - s_i))
                axis += 1
            elif isinstance(item, FakeTensor):
                out_shape.extend(item.shape)
                axis += 1
        if axis < len(self.shape):
            out_shape.extend(self.shape[axis:])
        return FakeTensor(tuple(out_shape), dtype=self.dtype, fill=self.fill)

    def any(self):
        return False

    def numpy(self):
        n = 1
        for d in self.shape:
            n *= int(d)
        byte_val = max(0, min(255, int(round(self.fill))))
        return np.ndarray(bytes([byte_val]) * n, self.shape, dtype=np.uint8)

def from_numpy(arr):
    val = float(arr._buf[0]) if getattr(arr, "_buf", b"") else 128.0
    return FakeTensor(arr.shape, dtype=float32, fill=val)

def arange(*args):
    if len(args) == 1:
        n = int(args[0])
    elif len(args) == 2:
        n = max(0, int(args[1]) - int(args[0]))
    else:
        n = max(0, (int(args[1]) - int(args[0]) + int(args[2]) - 1) // max(1, int(args[2])))
    return FakeTensor((n,), dtype=int32, fill=0.0)

def clamp(t, min=0, max=1):
    return t

def isnan(t):
    return FakeTensor(t.shape, dtype=int32, fill=0.0)

def manual_seed(s):
    return None

def compile(fn, **kwargs):
    return fn

@contextlib.contextmanager
def inference_mode():
    yield

@contextlib.contextmanager
def no_grad():
    yield

class _Cuda:
    def is_available(self):
        return True
    def set_device(self, idx):
        return None
    def reset_peak_memory_stats(self, dev=None):
        return None
    def get_device_capability(self, dev=None):
        raw = os.environ.get("COLABONE_TEST_GPU_CC", "12.0")
        maj, mn = raw.split(".", 1)
        return (int(maj), int(mn))
    def max_memory_allocated(self, dev=None):
        return 2048 * 1024 * 1024
    def empty_cache(self):
        return None
    def ipc_collect(self):
        return None
    def synchronize(self, dev=None):
        return None
    def manual_seed_all(self, s):
        return None

class _DynamoConfig:
    cache_size_limit = 64
    accumulated_cache_size_limit = 256

class _Dynamo:
    config = _DynamoConfig()
    def reset(self):
        return None

class _Cudnn:
    benchmark = False

class _Backends:
    cudnn = _Cudnn()

cuda = _Cuda()
_dynamo = _Dynamo()
backends = _Backends()
from . import nn as nn
""",
        encoding="utf-8",
    )
    (torch_dir / "distributed.py").write_text(
        """_INIT = False

def is_initialized():
    return _INIT

def is_nccl_available():
    return False

def init_process_group(**kwargs):
    global _INIT
    _INIT = True

def get_rank():
    return 0

def get_world_size():
    return 1

def destroy_process_group():
    global _INIT
    _INIT = False
""",
        encoding="utf-8",
    )
    (torch_dir / "nn" / "__init__.py").write_text("from . import functional\n", encoding="utf-8")
    (torch_dir / "nn" / "functional.py").write_text(
        """from torch import FakeTensor

def scaled_dot_product_attention(q, k, v, **kwargs):
    return q

def interpolate(tensor, size=None, **kwargs):
    h, w = int(size[0]), int(size[1])
    t, c = tensor.shape[0], tensor.shape[1]
    return FakeTensor((t, c, h, w), dtype=tensor.dtype, fill=tensor.fill)

def pad(tensor, pad_spec, **kwargs):
    left, right, top, bottom = [int(x) for x in pad_spec]
    t, c, h, w = tensor.shape
    return FakeTensor((t, c, h + top + bottom, w + left + right), dtype=tensor.dtype, fill=tensor.fill)
""",
        encoding="utf-8",
    )

    (fake_site / "einops.py").write_text("def rearrange(x, *a, **k):\n    return x\n", encoding="utf-8")

    # 3. Fake diffusers.utils & transformers & audio_separator
    (fake_site / "diffusers").mkdir(exist_ok=True)
    (fake_site / "diffusers" / "__init__.py").write_text("", encoding="utf-8")
    (fake_site / "diffusers" / "utils.py").write_text(
        "from PIL import Image\ndef load_image(path):\n    return Image.open(path).convert('RGB')\n",
        encoding="utf-8",
    )

    (fake_site / "transformers.py").write_text(
        f"""import json
from pathlib import Path

TRACE = Path({str(trace_file)!r})

def _rec(event, **extra):
    TRACE.parent.mkdir(parents=True, exist_ok=True)
    with TRACE.open("a", encoding="utf-8") as fh:
        fh.write(json.dumps({{"event": event, **extra}}) + "\\n")

class AutoTokenizer:
    @classmethod
    def from_pretrained(cls, *a, **k):
        return cls()

class UMT5EncoderModel:
    def __init__(self):
        self.device = "cuda:0"

    @classmethod
    def from_pretrained(cls, *a, **k):
        _rec("umt5_load")
        return cls()

    def to(self, dev):
        self.device = str(dev)
        _rec("umt5_to", device=self.device)
        return self
""",
        encoding="utf-8",
    )

    (fake_site / "audio_separator").mkdir(exist_ok=True)
    (fake_site / "audio_separator" / "__init__.py").write_text("", encoding="utf-8")
    (fake_site / "audio_separator" / "separator.py").write_text(
        f"""import json
from pathlib import Path

TRACE = Path({str(trace_file)!r})

def _rec(event, **extra):
    TRACE.parent.mkdir(parents=True, exist_ok=True)
    with TRACE.open("a", encoding="utf-8") as fh:
        fh.write(json.dumps({{"event": event, **extra}}) + "\\n")

class Separator:
    def __init__(self, **kwargs):
        self.onnx_execution_provider = ["CUDAExecutionProvider"]
        self._model_instance = object()
        _rec("vocal_sep_init")

    @property
    def model_instance(self):
        return self._model_instance

    @model_instance.setter
    def model_instance(self, val):
        self._model_instance = val
        if val is None:
            _rec("vocal_sep_model_cleared")

    def load_model(self, name):
        _rec("vocal_sep_load_model", model=name)
""",
        encoding="utf-8",
    )

    # 4. Fake LongCat-Video repository package (longcat_video)
    longcat_repo = base_dir / "repo_longcat"
    lc_pkg = longcat_repo / "longcat_video"
    for sub in (
        lc_pkg / "audio_process",
        lc_pkg / "context_parallel",
        lc_pkg / "modules" / "avatar",
    ):
        sub.mkdir(parents=True, exist_ok=True)
        (sub / "__init__.py").write_text("", encoding="utf-8")
    (lc_pkg / "__init__.py").write_text("", encoding="utf-8")
    (lc_pkg / "modules" / "__init__.py").write_text("", encoding="utf-8")

    (lc_pkg / "audio_process" / "__init__.py").write_text(
        f"""import json
from pathlib import Path

TRACE = Path({str(trace_file)!r})

def _rec(event, **extra):
    TRACE.parent.mkdir(parents=True, exist_ok=True)
    with TRACE.open("a", encoding="utf-8") as fh:
        fh.write(json.dumps({{"event": event, **extra}}) + "\\n")

class _AudioEncoder:
    def to(self, dev):
        return self

def get_audio_encoder(model_dir, model_type):
    _rec("whisper_load", model_type=model_type)
    return _AudioEncoder()

def get_audio_feature_extractor(model_dir, model_type):
    return object()
""",
        encoding="utf-8",
    )
    (lc_pkg / "audio_process" / "vocal_separator.py").write_text(
        f"""import json
import numpy as np
from pathlib import Path

TRACE = Path({str(trace_file)!r})

def separate_vocals(separator, audio_path, duration=4.0):
    TRACE.parent.mkdir(parents=True, exist_ok=True)
    with TRACE.open("a", encoding="utf-8") as fh:
        fh.write(json.dumps({{"event": "vocal_sep_separate"}}) + "\\n")
    return np.zeros((16000 * 5,), dtype=np.float32), 16000
""",
        encoding="utf-8",
    )
    (lc_pkg / "context_parallel" / "context_parallel_util.py").write_text(
        """def init_context_parallel(**kwargs):
    return None

def get_cp_size():
    return 1

def get_optimal_split(cp_size):
    return (1, 1)
""",
        encoding="utf-8",
    )
    (lc_pkg / "modules" / "autoencoder_kl_wan.py").write_text(
        f"""import json
from pathlib import Path

TRACE = Path({str(trace_file)!r})

class AutoencoderKLWan:
    def __init__(self):
        self.encoder = lambda x: x
        self.decoder = lambda x: x

    @classmethod
    def from_pretrained(cls, *a, **k):
        TRACE.parent.mkdir(parents=True, exist_ok=True)
        with TRACE.open("a", encoding="utf-8") as fh:
            fh.write(json.dumps({{"event": "vae_load"}}) + "\\n")
        return cls()

    def to(self, dev):
        return self

    def clear_cache(self):
        return None
""",
        encoding="utf-8",
    )
    (lc_pkg / "modules" / "scheduling_flow_match_euler_discrete.py").write_text(
        """class FlowMatchEulerDiscreteScheduler:
    @classmethod
    def from_pretrained(cls, *a, **k):
        return cls()
""",
        encoding="utf-8",
    )
    (lc_pkg / "modules" / "avatar" / "longcat_video_dit_avatar.py").write_text(
        f"""import json
from pathlib import Path

TRACE = Path({str(trace_file)!r})

class _SubAttn:
    def __init__(self):
        self.enable_flashattn3 = False
        self.enable_flashattn2 = False
        self.enable_xformers = False
    def _process_attn(self, q, k, v, shape):
        return q
    def _process_cross_attn(self, *a, **k):
        return a[0]

class _Block:
    def __init__(self):
        self.attn = _SubAttn()
        self.cross_attn = _SubAttn()
        self.audio_cross_attn = _SubAttn()

class LongCatVideoAvatarTransformer3DModel:
    def __init__(self):
        self.blocks = [_Block()]

    @classmethod
    def from_pretrained(cls, *a, **k):
        TRACE.parent.mkdir(parents=True, exist_ok=True)
        with TRACE.open("a", encoding="utf-8") as fh:
            fh.write(json.dumps({{"event": "dit_load", "fa3": k.get("enable_flashattn3"), "fa2": k.get("enable_flashattn2")}}) + "\\n")
        return cls()

    def to(self, dev):
        return self

    def load_lora(self, path, key, **kwargs):
        with TRACE.open("a", encoding="utf-8") as fh:
            fh.write(json.dumps({{"event": "load_lora", "key": key}}) + "\\n")

    def enable_lora_mix(self, k1, k2, alpha, steps):
        with TRACE.open("a", encoding="utf-8") as fh:
            fh.write(json.dumps({{"event": "enable_lora_mix", "steps": steps, "alpha": alpha}}) + "\\n")

    def merge_lora(self, path, **kwargs):
        return None
""",
        encoding="utf-8",
    )
    (lc_pkg / "pipeline_longcat_video_avatar.py").write_text(
        f"""import json
import numpy as np
import torch
from pathlib import Path

TRACE = Path({str(trace_file)!r})
H = {int(ai2v_height)}
W = {int(ai2v_width)}

def _rec(event, **extra):
    TRACE.parent.mkdir(parents=True, exist_ok=True)
    with TRACE.open("a", encoding="utf-8") as fh:
        fh.write(json.dumps({{"event": event, **extra}}) + "\\n")

class LongCatVideoAvatarPipeline:
    def __init__(self, **kwargs):
        self.tokenizer = kwargs.get("tokenizer")
        self.text_encoder = kwargs.get("text_encoder")
        self.vae = kwargs.get("vae")
        self.scheduler = kwargs.get("scheduler")
        self.dit = kwargs.get("dit")
        self.audio_encoder = kwargs.get("audio_encoder")
        self.audio_feature_extractor = kwargs.get("audio_feature_extractor")
        self.model_type = kwargs.get("model_type")

    def to(self, dev):
        return self

    def get_audio_embedding(self, speech_array, **kwargs):
        assert kwargs.get("use_loudness_norm") is False
        _rec("get_audio_embedding", use_loudness_norm=kwargs.get("use_loudness_norm"))
        return torch.FakeTensor((300, 5, 768), dtype=torch.float32, fill=0.0)

    def generate_ai2v(self, **kwargs):
        num_frames = int(kwargs["num_frames"])
        _rec("generate_ai2v", whisper_released=(self.audio_encoder is None), num_frames=num_frames)
        arr = np.ndarray(bytes([120]) * (num_frames * H * W * 3), (num_frames, H, W, 3), dtype=np.uint8)
        lat = torch.FakeTensor((1, 16, 24, H // 8, W // 8), dtype=torch.bfloat16, fill=0.0)
        return (arr, lat)

    def generate_avc(self, **kwargs):
        num_frames = int(kwargs["num_frames"])
        _rec(
            "generate_avc",
            umt5_device=getattr(self.text_encoder, "device", ""),
            num_frames=num_frames,
            cond_len=len(kwargs.get("video") or []),
        )
        arr = np.ndarray(bytes([160]) * (num_frames * H * W * 3), (num_frames, H, W, 3), dtype=np.uint8)
        lat = torch.FakeTensor((1, 16, 24, H // 8, W // 8), dtype=torch.bfloat16, fill=0.0)
        return (arr, lat)

    def _clear_cache(self):
        _rec("pipe_clear_cache")
""",
        encoding="utf-8",
    )

    # 5. Fake FlashVSR_Plus repository package (inference)
    flashvsr_repo = base_dir / "repo_flashvsr"
    (flashvsr_repo / "inference").mkdir(parents=True, exist_ok=True)
    (flashvsr_repo / "prompt_tensor").mkdir(parents=True, exist_ok=True)
    (flashvsr_repo / "prompt_tensor" / "posi_prompt.pth").write_bytes(b"prompt-tensor")
    (flashvsr_repo / "inference" / "pipeline.py").write_text("# marker\n", encoding="utf-8")
    (flashvsr_repo / "inference" / "__init__.py").write_text(
        f"""import json
from dataclasses import dataclass
from pathlib import Path
from types import SimpleNamespace
import torch

TRACE = Path({str(trace_file)!r})

@dataclass
class ModelPaths:
    dit: str
    lq_projection: str
    context: str
    tiny_decoder: str

@dataclass
class InferenceRequest:
    lq_video: object
    output_path: object
    fps: float

def stream_config_for_mode(mode, **kwargs):
    return {{"mode": mode, **kwargs}}

class _FakeFlashPipeline:
    def infer(self, req):
        shape = tuple(req.lq_video.shape)
        assert len(shape) == 5 and shape[0] == 1 and shape[1] == 3
        TRACE.parent.mkdir(parents=True, exist_ok=True)
        with TRACE.open("a", encoding="utf-8") as fh:
            fh.write(json.dumps({{"event": "flashvsr_infer", "shape": list(shape)}}) + "\\n")
        return SimpleNamespace(video=torch.FakeTensor(shape, dtype=torch.float32, fill=0.5))

def build_inference_pipeline(stream_cfg, model_paths):
    TRACE.parent.mkdir(parents=True, exist_ok=True)
    with TRACE.open("a", encoding="utf-8") as fh:
        fh.write(json.dumps({{"event": "flashvsr_build", "decode_every": stream_cfg.get("decode_every")}}) + "\\n")
    return _FakeFlashPipeline()
""",
        encoding="utf-8",
    )

    # 6. Fake SeedVR2 repository CLI (inference_cli.py)
    seedvr2_repo = base_dir / "repo_seedvr2"
    seedvr2_repo.mkdir(parents=True, exist_ok=True)
    (seedvr2_repo / "inference_cli.py").write_text(
        """import sys
from pathlib import Path

args = sys.argv[1:]
def _val(flag, default=""):
    for i, a in enumerate(args):
        if a == flag and i + 1 < len(args):
            return args[i + 1]
    return default

out_dir = Path(_val("--output"))
out_dir.mkdir(parents=True, exist_ok=True)
frames = int(_val("--load_cap", "10") or "10")
print("Phase 1: VAE encoding batch 1/1", flush=True)
print("Phase 2: DiT upscaling batch 1/2", flush=True)
print("Phase 2: DiT upscaling batch 2/2", flush=True)
print("Phase 3: VAE decoding batch 1/1", flush=True)
print("Phase 4: Post-processing complete", flush=True)
tiny_png = (
    b"\\x89PNG\\r\\n\\x1a\\n\\x00\\x00\\x00\\rIHDR\\x00\\x00\\x00\\x01\\x00\\x00\\x00\\x01"
    b"\\x08\\x02\\x00\\x00\\x00\\x90wS\\xde\\x00\\x00\\x00\\x0cIDATx\\x9cc\\xf8\\xcf\\xc0"
    b"\\x00\\x00\\x03\\x01\\x01\\x00\\x18\\xdd\\x8d\\xb0\\x00\\x00\\x00\\x00IEND\\xaeB`\\x82"
)
for i in range(1, frames + 1):
    (out_dir / f"frame_{i:06d}.png").write_bytes(tiny_png)
""",
        encoding="utf-8",
    )

    return {
        "fake_site": fake_site,
        "longcat_repo": longcat_repo,
        "flashvsr_repo": flashvsr_repo,
        "seedvr2_repo": seedvr2_repo,
    }


def test_real_cross_module_wiring_runner_manager_workers_flashvsr_and_vram_order(
    runtime, tmp_path, monkeypatch
):
    """End-to-end real cross-module wiring test:
    ColabOneDigitalHumanRunner + real ColabOneAvatarEngineManager spawning REAL
    server/avatar_worker_longcat16.py and REAL server/vsr_worker_diffusion.py
    subprocesses with stubbed ML packages and stubbed ffmpeg/ffprobe."""
    audit_log = tmp_path / "ffmpeg_audit.jsonl"
    trace_file = tmp_path / "worker_trace.jsonl"
    ffmpeg_bin, _ffprobe_bin = _install_stub_ffmpeg_and_ffprobe(tmp_path / "bin", audit_log)
    stubs = _install_fake_ml_site_and_repos(
        tmp_path / "stubs", trace_file, ai2v_height=64, ai2v_width=80
    )

    # _build_worker_env securely strips ambient PYTHONPATH; inject stubs["fake_site"]
    # via a cross-platform executable Python wrapper acting as the isolated venv interpreter.
    if sys.platform == "win32":
        fake_site_python = tmp_path / "bin" / "fake_venv_python.cmd"
        fake_site_python.parent.mkdir(parents=True, exist_ok=True)
        fake_site_python.write_text(
            f'@echo off\r\n'
            f'if defined PYTHONPATH (\r\n'
            f'  set "PYTHONPATH={stubs["fake_site"]};%PYTHONPATH%"\r\n'
            f') else (\r\n'
            f'  set "PYTHONPATH={stubs["fake_site"]}"\r\n'
            f')\r\n'
            f'"{sys.executable}" %*\r\n',
            encoding="utf-8",
        )
    else:
        fake_site_python = tmp_path / "bin" / "fake_venv_python"
        fake_site_python.parent.mkdir(parents=True, exist_ok=True)
        fake_site_python.write_text(
            f'#!/bin/sh\nexport PYTHONPATH="{stubs["fake_site"]}${{PYTHONPATH:+:$PYTHONPATH}}"\n'
            f'exec "{sys.executable}" "$@"\n',
            encoding="utf-8",
        )
        fake_site_python.chmod(
            fake_site_python.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH
        )

    # Create concrete weights directories for LongCat 1.6 (with LoRAs + Kim_Vocal_2) and FlashVSR+.
    mgr = ColabOneAvatarEngineManager(
        runtime.root,
        gpu_info_fn=lambda: {
            "name": "NVIDIA RTX PRO 6000 Blackwell",
            "total_mib": 98304,
            "used_mib": 1024,
        },
    )
    avatar_dir = mgr.weights_dir / "longcat16_avatar"
    base_dir = mgr.weights_dir / "longcat16_base"
    flash_dir = mgr.weights_dir / "flashvsr_plus"
    _populate_concrete_weight_files(
        avatar_dir,
        repo_id=COLABONE_LONGCAT16_AVATAR_HF_REPO,
        revision=COLABONE_LONGCAT16_AVATAR_HF_REV,
        patterns=longcat16_avatar_allow_patterns("distill_lora_mix", "auto"),
        concrete_relpaths=[
            "base_model/model.safetensors",
            "whisper-large-v3/model.safetensors",
            "whisper-large-v3/config.json",
            "scheduler/scheduler_config.json",
            "vocal_separator/Kim_Vocal_2.onnx",
            "loraA/lora.safetensors",
            "loraB/lora.safetensors",
        ],
    )
    _populate_concrete_weight_files(
        base_dir,
        repo_id=COLABONE_LONGCAT16_BASE_HF_REPO,
        revision=COLABONE_LONGCAT16_BASE_HF_REV,
        patterns=longcat16_base_allow_patterns(),
        concrete_relpaths=[
            "tokenizer/tokenizer_config.json",
            "text_encoder/model.safetensors",
            "vae/model.safetensors",
        ],
    )
    _populate_concrete_weight_files(
        flash_dir,
        repo_id=COLABONE_FLASHVSR_HF_REPO,
        revision=COLABONE_FLASHVSR_HF_REV,
        patterns=vsr_engine_allow_patterns("flashvsr_plus")[2],
        concrete_relpaths=[
            "diffusion_pytorch_model_streaming_dmd.safetensors",
            "LQ_proj_in.ckpt",
            "TCDecoder.ckpt",
        ],
    )

    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(200 * 1024**3, 10 * 1024**3, 190 * 1024**3),
    )

    # Only stub setup returns (real run_longcat16 & run_diffusion_vsr spawn actual worker scripts).
    monkeypatch.setattr(
        mgr,
        "ensure_longcat16_ready",
        lambda task, **kw: {
            "python": fake_site_python,
            "repo_dir": stubs["longcat_repo"],
            "avatar_dir": avatar_dir,
            "base_dir": base_dir,
            "compile_cache_dir": mgr._resolve_compile_cache_dir(),
        },
    )
    monkeypatch.setattr(
        mgr,
        "ensure_vsr_ready",
        lambda task, **kw: {
            "python": fake_site_python,
            "repo_dir": stubs["flashvsr_repo"],
            "weights_dir": flash_dir,
        },
    )

    monkeypatch.setattr(
        runtime.comfy,
        "start_server",
        lambda *a, **k: pytest.fail("ComfyUI must NEVER start in native mode"),
    )

    runner = ColabOneDigitalHumanRunner(
        runtime.comfy,
        None,
        run_cmd_fn=None,  # Stage 4 executes real subprocess ffmpeg stub!
        avatar_engines=mgr,
    )
    monkeypatch.setattr(runner, "_ffmpeg_binary", lambda: str(ffmpeg_bin))

    catalog = _load_real_comfy_catalog()
    longcat_wf = next(t for t in catalog.templates if t.id == "digital-human-longcat").model_dump()
    portrait = tmp_path / "host.png"
    portrait.write_bytes(_make_png_bytes("navy", size=(96, 96)))
    # 4.0s @ 25fps = 100 frames => 2 LongCat segments (93 + 7 frames) exercising AI2V + AVC!
    audio_wav = _write_stereo_pcm16_wav(tmp_path / "stereo_4s.wav", duration_sec=4.0, sample_rate=48000)

    task = ColabOneTask("comfyui_run", "real-wire-e2e")
    req = WorkflowRunRequest(
        workflow_id="digital-human-longcat",
        overrides={
            "pipeline.audio_source": "audio",
            "pipeline.quality_preset": "longform_fast",
            "pipeline.upscale_mode": "flashvsr_plus",
            "pipeline.target_resolution": "320x256",
            "pipeline.vocal_separation": "auto",
        },
        inputs={"1.image": "a" * 32, "2.audio": "b" * 32},
    )
    outputs = runner.run(
        task,
        longcat_wf,
        req,
        [("1.image", "host.png", portrait), ("2.audio", "stereo_4s.wav", audio_wav)],
    )

    assert len(outputs) == 1
    assert outputs[0]["extension"] == "mp4"
    assert outputs[0]["id"] in runtime.comfy.store.outputs
    assert task.progress == pytest.approx(1.0)
    assert runtime.comfy.status()["active_pack"] == "digital-human-longcat"

    # Verify real worker progress lines flowed through ColabOneAvatarEngineManager into task.logs.
    log_msgs = [entry["message"] for entry in task.logs]
    assert any("LongCat-Video-Avatar 1.6 渲染完成（共 100 帧" in m for m in log_msgs)
    assert any("已完成 flashvsr_plus 视频扩散超分（共 100 帧" in m for m in log_msgs)

    # Verify strict VRAM cleanup ordering from real avatar_worker_longcat16.py execution:
    # 1. Kim_Vocal_2 runs and clears model_instance BEFORE CUDA models (UMT5/VAE/DiT/Whisper) load.
    # 2. Whisper is released (pipe.audio_encoder is None) BEFORE seg 0 (generate_ai2v).
    # 3. UMT5 is offloaded to CPU AFTER seg 0 and BEFORE seg 1 (generate_avc).
    # 4. pipe._clear_cache() is called after AVC segment 1.
    trace = [json.loads(line) for line in trace_file.read_text(encoding="utf-8").splitlines() if line.strip()]
    event_names = [item["event"] for item in trace]
    assert event_names.index("vocal_sep_load_model") < event_names.index("vocal_sep_model_cleared")
    assert event_names.index("vocal_sep_model_cleared") < event_names.index("umt5_load")
    assert event_names.index("vocal_sep_model_cleared") < event_names.index("dit_load")
    assert event_names.index("whisper_load") < event_names.index("get_audio_embedding")
    ai2v_evt = next(item for item in trace if item["event"] == "generate_ai2v")
    assert ai2v_evt["whisper_released"] is True
    avc_evt = next(item for item in trace if item["event"] == "generate_avc")
    assert avc_evt["umt5_device"] == "cpu"
    assert avc_evt["cond_len"] == 13
    avc_idx = event_names.index("generate_avc")
    assert event_names.index("generate_ai2v") < event_names.index("umt5_to") < avc_idx
    clear_indices = [idx for idx, name in enumerate(event_names) if name == "pipe_clear_cache"]
    assert any(idx > avc_idx for idx in clear_indices), (
        f"Expected pipe._clear_cache() after generate_avc (idx={avc_idx}), got {event_names}"
    )
    assert len(clear_indices) >= 2
    assert any(item["event"] == "flashvsr_infer" for item in trace)

    # Verify Stage 4 FFmpeg invocation used BT.709 matrix + VUI flags and work_dir was cleaned up.
    ff_records = [json.loads(line) for line in audit_log.read_text(encoding="utf-8").splitlines() if line.strip()]
    stage4_cmds = [r["argv"] for r in ff_records if r["tool"] == "ffmpeg" and r["argv"] and r["argv"][-1].endswith("final_output.mp4")]
    assert len(stage4_cmds) == 1
    s4 = stage4_cmds[0]
    assert "out_color_matrix=bt709:out_range=tv" in s4[s4.index("-vf") + 1]
    assert s4[s4.index("-colorspace") + 1] == "bt709"
    assert s4[s4.index("-color_primaries") + 1] == "bt709"
    assert s4[s4.index("-color_trc") + 1] == "bt709"
    assert s4[s4.index("-color_range") + 1] == "tv"
    # No leftover work_dir / intermediate MKVs.
    work_parent = runtime.root / "comfyui-work"
    assert list(work_parent.glob("dh-*")) == []


def test_real_cross_module_wiring_seedvr2_cli_worker_and_progress_tracker(tmp_path, monkeypatch):
    """Exercise real ColabOneAvatarEngineManager.run_diffusion_vsr invoking REAL
    server/vsr_worker_diffusion.py with SeedVR2 CLI, Phase 1-4 progress parsing,
    cover-crop FFV1 encode, and ffprobe verification."""
    audit_log = tmp_path / "seedvr2_ffmpeg.jsonl"
    trace_file = tmp_path / "seedvr2_trace.jsonl"
    ffmpeg_bin, _ = _install_stub_ffmpeg_and_ffprobe(tmp_path / "bin", audit_log)
    stubs = _install_fake_ml_site_and_repos(tmp_path / "stubs", trace_file)

    weights_dir = tmp_path / "seedvr2_weights"
    _populate_concrete_weight_files(
        weights_dir,
        repo_id=COLABONE_SEEDVR2_HF_REPO,
        revision=COLABONE_SEEDVR2_HF_REV,
        patterns=vsr_engine_allow_patterns("seedvr2_7b_sharp")[2],
        concrete_relpaths=[
            "seedvr2_ema_7b_sharp_fp16.safetensors",
            "ema_vae_fp16.safetensors",
        ],
    )

    work_dir = tmp_path / "work_seedvr2"
    work_dir.mkdir(parents=True, exist_ok=True)
    in_mkv = work_dir / "stage2_720p.mkv"
    in_mkv.write_bytes(b"COLABONE_FAKE_MKV:" + json.dumps({"w": 1280, "h": 768, "frames": 12}).encode("utf-8"))
    out_mkv = work_dir / "stage3_upscaled.mkv"

    mgr = ColabOneAvatarEngineManager(
        tmp_path / "remote",
        gpu_info_fn=lambda: {"name": "Blackwell", "total_mib": 98304, "used_mib": 0},
    )
    task = ColabOneTask("comfyui_run", "real-seedvr2")
    manifest = mgr.run_diffusion_vsr(
        task,
        config={
            "engine": "seedvr2_7b_sharp",
            "work_dir": str(work_dir),
            "input_video_path": str(in_mkv),
            "output_video_path": str(out_mkv),
            "source_width": 1280,
            "source_height": 768,
            "target_width": 1920,
            "target_height": 1080,
            "fps": 25,
            "exact_frames": 12,
            "vram_mib": 98304,
            "ffmpeg_bin": str(ffmpeg_bin),
        },
        runtime_paths={
            "python": Path(sys.executable),
            "repo_dir": stubs["seedvr2_repo"],
            "weights_dir": weights_dir,
        },
        work_dir=work_dir,
        progress_range=(0.78, 0.94),
    )

    assert manifest["ok"] is True
    assert manifest["engine"] == "seedvr2_7b_sharp"
    assert manifest["target_width"] == 1920
    assert manifest["target_height"] == 1080
    assert manifest["frames_written"] == 12
    assert manifest["resolution_short_side"] == 1152
    assert out_mkv.is_file() and out_mkv.stat().st_size > 0
    assert task.progress == pytest.approx(0.94)
    assert any("seedvr2_7b_sharp" in entry["message"] for entry in task.logs)


def test_adversarial_manifest_and_output_path_containment_rejected(tmp_path):
    """Malformed, missing, non-dict, symlink, and uncontained manifest/video paths fail closed."""
    from server.digital_human import _resolve_contained_worker_file

    mgr = ColabOneAvatarEngineManager(
        tmp_path / "remote",
        gpu_info_fn=lambda: {"name": "Blackwell", "total_mib": 98304, "used_mib": 0},
    )
    work_dir = tmp_path / "work"
    work_dir.mkdir(parents=True, exist_ok=True)
    outside_dir = tmp_path / "outside"
    outside_dir.mkdir(parents=True, exist_ok=True)
    outside_vid = outside_dir / "escape.mkv"
    outside_vid.write_bytes(b"outside-video")
    outside_manifest = outside_dir / "escape.json"
    outside_manifest.write_text(
        json.dumps({"ok": True, "output_video_path": str(outside_vid)}),
        encoding="utf-8",
    )

    # 1. Missing manifest file.
    with pytest.raises(RuntimeError, match="未生成有效输出清单"):
        mgr._load_and_validate_manifest(work_dir / "missing.json", stage_label="Stage2", work_dir=work_dir)

    # 2. Empty manifest file.
    empty_m = work_dir / "empty.json"
    empty_m.write_text("", encoding="utf-8")
    with pytest.raises(RuntimeError, match="未生成有效输出清单"):
        mgr._load_and_validate_manifest(empty_m, stage_label="Stage2", work_dir=work_dir)

    # 3. Malformed JSON manifest.
    bad_json = work_dir / "bad.json"
    bad_json.write_text("{not-json", encoding="utf-8")
    with pytest.raises(RuntimeError, match="解析失败"):
        mgr._load_and_validate_manifest(bad_json, stage_label="Stage2", work_dir=work_dir)

    # 4. Non-dict / empty-dict manifests.
    for idx, payload in enumerate(("[]", '"hello"', "123", "{}")):
        p = work_dir / f"nondict_{idx}.json"
        p.write_text(payload, encoding="utf-8")
        with pytest.raises(RuntimeError, match="格式无效或为空"):
            mgr._load_and_validate_manifest(p, stage_label="Stage2", work_dir=work_dir)

    # 5. Error manifest (ok=False) & missing output_video_path.
    err_m = work_dir / "err.json"
    err_m.write_text(json.dumps({"ok": False, "error": "OOM"}), encoding="utf-8")
    with pytest.raises(RuntimeError, match="OOM"):
        mgr._load_and_validate_manifest(err_m, stage_label="Stage2", work_dir=work_dir)

    no_vid_m = work_dir / "novid.json"
    no_vid_m.write_text(json.dumps({"ok": True, "width": 1280}), encoding="utf-8")
    with pytest.raises(RuntimeError, match="output_video_path"):
        mgr._load_and_validate_manifest(no_vid_m, stage_label="Stage2", work_dir=work_dir)

    # 6. Uncontained manifest path (outside work_dir or traversal).
    with pytest.raises(ValueError, match="必须位于工作目录内"):
        mgr._load_and_validate_manifest(outside_manifest, stage_label="Stage2", work_dir=work_dir)
    with pytest.raises(ValueError, match="必须位于工作目录内"):
        mgr._load_and_validate_manifest(work_dir / ".." / "outside" / "escape.json", stage_label="Stage2", work_dir=work_dir)

    # 7. Symlink manifest path rejected.
    sym_manifest = work_dir / "sym_manifest.json"
    sym_manifest.symlink_to(outside_manifest)
    with pytest.raises(ValueError, match="符号链接"):
        mgr._load_and_validate_manifest(sym_manifest, stage_label="Stage2", work_dir=work_dir)

    # 8. Manifest pointing to uncontained or symlinked or empty output video rejected.
    uncontained_vid_m = work_dir / "uncontained_vid.json"
    uncontained_vid_m.write_text(
        json.dumps({"ok": True, "output_video_path": str(outside_vid)}),
        encoding="utf-8",
    )
    with pytest.raises(ValueError, match="必须位于工作目录内"):
        mgr._load_and_validate_manifest(uncontained_vid_m, stage_label="Stage2", work_dir=work_dir)

    sym_vid = work_dir / "sym_out.mkv"
    sym_vid.symlink_to(outside_vid)
    sym_vid_m = work_dir / "sym_vid.json"
    sym_vid_m.write_text(
        json.dumps({"ok": True, "output_video_path": str(sym_vid)}),
        encoding="utf-8",
    )
    with pytest.raises(ValueError, match="符号链接"):
        mgr._load_and_validate_manifest(sym_vid_m, stage_label="Stage2", work_dir=work_dir)

    empty_vid = work_dir / "empty.mkv"
    empty_vid.write_bytes(b"")
    empty_vid_m = work_dir / "empty_vid.json"
    empty_vid_m.write_text(
        json.dumps({"ok": True, "output_video_path": str(empty_vid)}),
        encoding="utf-8",
    )
    with pytest.raises(RuntimeError, match="不存在或为空"):
        mgr._load_and_validate_manifest(empty_vid_m, stage_label="Stage2", work_dir=work_dir)

    # 9. run_longcat16 / run_diffusion_vsr reject uncontained/symlink output paths upfront.
    task = ColabOneTask("comfyui_run", "bad-paths")
    with pytest.raises(ValueError, match="必须位于工作目录内"):
        mgr.run_longcat16(
            task,
            config={"output_video_path": str(outside_vid)},
            runtime_paths={"python": Path(sys.executable)},
            work_dir=work_dir,
        )
    with pytest.raises(ValueError, match="符号链接"):
        mgr.run_diffusion_vsr(
            task,
            config={"output_video_path": str(sym_vid)},
            runtime_paths={"python": Path(sys.executable)},
            work_dir=work_dir,
        )

    # 10. digital_human._resolve_contained_worker_file rejects symlink, outside, and 0-byte files.
    with pytest.raises(RuntimeError, match="符号链接"):
        _resolve_contained_worker_file(work_dir, sym_vid, label="Stage2", missing_message="missing")
    with pytest.raises(RuntimeError, match="超出工作目录"):
        _resolve_contained_worker_file(work_dir, outside_vid, label="Stage2", missing_message="missing")
    with pytest.raises(RuntimeError, match="missing"):
        _resolve_contained_worker_file(work_dir, empty_vid, label="Stage2", missing_message="missing")


def test_adversarial_missing_concrete_weights_detected_even_when_dirs_exist(tmp_path, monkeypatch):
    """Empty weight dirs, .tmp/.part/0-byte placeholders, or a single missing file are caught."""
    from server.vsr_worker_diffusion import run_diffusion_vsr

    mgr = ColabOneAvatarEngineManager(
        tmp_path / "remote",
        gpu_info_fn=lambda: {"name": "Blackwell", "total_mib": 98304, "used_mib": 0},
    )

    # Create valid markers and directories, but ONLY empty subdirs / .tmp / dummy non-matching files.
    avatar_dir = mgr.weights_dir / "longcat16_avatar"
    for sub in ("base_model", "whisper-large-v3", "scheduler", "vocal_separator"):
        (avatar_dir / sub).mkdir(parents=True, exist_ok=True)
    (avatar_dir / "base_model" / "config.json").write_text("{}", encoding="utf-8")  # no weight ext!
    (avatar_dir / "base_model" / "model.safetensors.part").write_bytes(b"partial")
    (avatar_dir / "whisper-large-v3" / "model.safetensors").write_bytes(b"")  # 0-byte!
    atomic_json(
        avatar_dir / ".colabone-weights.json",
        {
            "repo_id": COLABONE_LONGCAT16_AVATAR_HF_REPO,
            "revision": COLABONE_LONGCAT16_AVATAR_HF_REV,
            "downloaded_patterns": longcat16_avatar_allow_patterns("non_distill_cfg", "auto"),
        },
    )

    assert not mgr._weights_marker_valid(
        avatar_dir,
        repo_id=COLABONE_LONGCAT16_AVATAR_HF_REPO,
        revision=COLABONE_LONGCAT16_AVATAR_HF_REV,
        required_patterns=longcat16_avatar_allow_patterns("non_distill_cfg", "auto"),
    )

    # Even with all other concrete files populated, removing ONE required file invalidates cache.
    _populate_concrete_weight_files(
        avatar_dir,
        repo_id=COLABONE_LONGCAT16_AVATAR_HF_REPO,
        revision=COLABONE_LONGCAT16_AVATAR_HF_REV,
        patterns=longcat16_avatar_allow_patterns("non_distill_cfg", "auto"),
        concrete_relpaths=[
            "base_model/model.safetensors",
            "whisper-large-v3/model.safetensors",
            "whisper-large-v3/config.json",
            "scheduler/scheduler_config.json",
            "vocal_separator/Kim_Vocal_2.onnx",
        ],
    )
    _populate_concrete_weight_files(
        mgr.weights_dir / "longcat16_base",
        repo_id=COLABONE_LONGCAT16_BASE_HF_REPO,
        revision=COLABONE_LONGCAT16_BASE_HF_REV,
        patterns=longcat16_base_allow_patterns(),
        concrete_relpaths=[
            "tokenizer/tokenizer.json",
            "text_encoder/model.safetensors",
            "vae/model.safetensors",
        ],
    )
    _populate_concrete_weight_files(
        mgr.weights_dir / "seedvr2",
        repo_id=COLABONE_SEEDVR2_HF_REPO,
        revision=COLABONE_SEEDVR2_HF_REV,
        patterns=vsr_engine_allow_patterns("seedvr2_7b_sharp")[2],
        concrete_relpaths=[
            "seedvr2_ema_7b_sharp_fp16.safetensors",
            "ema_vae_fp16.safetensors",
        ],
    )
    # Delete just ema_vae_fp16.safetensors while leaving dir and marker intact.
    (mgr.weights_dir / "seedvr2" / "ema_vae_fp16.safetensors").unlink()
    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(100 * 1024**3, 90 * 1024**3, 10 * 1024**3),
    )
    with pytest.raises(RuntimeError, match="远端磁盘空间不足"):
        mgr.check_preflight(
            min_vram_mib=49152, min_disk_mib=79872, upscale_mode="seedvr2_7b_sharp"
        )

    # run_diffusion_vsr also rejects missing or 0-byte concrete weights even when weights_dir exists.
    repo_dir = tmp_path / "repo_s3"
    repo_dir.mkdir()
    in_vid = tmp_path / "in.mkv"
    in_vid.write_bytes(b"video")
    (mgr.weights_dir / "seedvr2" / "ema_vae_fp16.safetensors").write_bytes(b"")  # 0-byte
    with pytest.raises(FileNotFoundError, match="ema_vae_fp16.safetensors"):
        run_diffusion_vsr(
            {
                "engine": "seedvr2_7b_sharp",
                "repo_dir": str(repo_dir),
                "weights_dir": str(mgr.weights_dir / "seedvr2"),
                "input_video_path": str(in_vid),
                "output_video_path": str(tmp_path / "out.mkv"),
                "output_manifest_path": str(tmp_path / "out.json"),
                "source_width": 1280,
                "source_height": 768,
                "target_width": 1920,
                "target_height": 1080,
                "exact_frames": 10,
            }
        )


def test_ffv1_pipe_writer_broken_pipe_flush_error_and_tmp_mkv_cleanup(tmp_path, monkeypatch):
    """_ColabOneFfv1PipeWriter handles BrokenPipeError / flush OSError and cleans .tmp.mkv atomically."""
    import importlib.util
    from server.avatar_worker_longcat16 import _ColabOneFfv1PipeWriter

    # Load isolated numpy stub without polluting global sys.modules across tests.
    stubs = _install_fake_ml_site_and_repos(tmp_path / "stubs", tmp_path / "t.jsonl")
    spec = importlib.util.spec_from_file_location("_colabone_test_fake_np", stubs["fake_site"] / "numpy.py")
    assert spec is not None and spec.loader is not None
    fake_np = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(fake_np)
    monkeypatch.setitem(sys.modules, "numpy", fake_np)

    # 1. Stub ffmpeg that writes partial .tmp.mkv and exits immediately (broken pipe on stdin write/flush).
    broken_script = tmp_path / "broken_ffmpeg.py"
    broken_script.write_text(
        "import sys\n"
        "from pathlib import Path\n"
        "Path(sys.argv[-1]).write_bytes(b'partial-tmp-mkv')\n"
        "sys.stderr.write('simulated ffv1 encoder crash\\n')\n"
        "sys.stderr.flush()\n"
        "sys.exit(1)\n",
        encoding="utf-8",
    )
    broken_ffmpeg = _create_python_wrapper(tmp_path / "bin" / "broken_ffmpeg", broken_script)
    out_broken = tmp_path / "out_broken.mkv"

    writer = _ColabOneFfv1PipeWriter(str(broken_ffmpeg), 64, 64, 25, out_broken)
    tmp_path_created = writer._tmp_output_path
    # Wait for child to exit and close stdin so write/flush hits BrokenPipeError / OSError.
    assert writer._proc is not None
    writer._proc.wait(timeout=5.0)
    big_frames = fake_np.ndarray(bytes([42]) * (8 * 64 * 64 * 3), (8, 64, 64, 3), dtype=fake_np.uint8)
    with pytest.raises(RuntimeError, match="FFmpeg FFV1 pipe failed"):
        writer.write_frames(big_frames)
    writer.abort()
    assert not tmp_path_created.exists()
    assert not out_broken.exists()

    # 2. Stub ffmpeg that consumes stdin, creates .tmp.mkv, then exits non-zero on close().
    fail_close_script = tmp_path / "fail_close_ffmpeg.py"
    fail_close_script.write_text(
        "import sys\n"
        "from pathlib import Path\n"
        "sys.stdin.buffer.read()\n"
        "Path(sys.argv[-1]).write_bytes(b'corrupt-tmp-mkv')\n"
        "sys.stderr.write('mux trailer failed\\n')\n"
        "sys.exit(2)\n",
        encoding="utf-8",
    )
    fail_close_ffmpeg = _create_python_wrapper(tmp_path / "bin" / "fail_close_ffmpeg", fail_close_script)
    out_fail_close = tmp_path / "out_fail_close.mkv"
    writer2 = _ColabOneFfv1PipeWriter(str(fail_close_ffmpeg), 16, 16, 25, out_fail_close)
    tmp2 = writer2._tmp_output_path
    small_frames = fake_np.ndarray(bytes([10]) * (2 * 16 * 16 * 3), (2, 16, 16, 3), dtype=fake_np.uint8)
    writer2.write_frames(small_frames)
    with pytest.raises(RuntimeError, match="exited with code 2"):
        writer2.close()
    assert not tmp2.exists()
    assert not out_fail_close.exists()

    # 3. Clean close() atomically promotes .tmp.mkv to output_path with zero leftover .tmp.mkv.
    ok_script = tmp_path / "ok_ffmpeg.py"
    ok_script.write_text(
        "import sys\n"
        "from pathlib import Path\n"
        "data = sys.stdin.buffer.read()\n"
        "Path(sys.argv[-1]).write_bytes(b'OKMKV:' + str(len(data)).encode())\n",
        encoding="utf-8",
    )
    ok_ffmpeg = _create_python_wrapper(tmp_path / "bin" / "ok_ffmpeg", ok_script)
    out_ok = tmp_path / "out_ok.mkv"
    writer3 = _ColabOneFfv1PipeWriter(str(ok_ffmpeg), 16, 16, 25, out_ok)
    tmp3 = writer3._tmp_output_path
    writer3.write_frames(small_frames)
    writer3.close()
    assert not tmp3.exists()
    assert out_ok.is_file()
    assert out_ok.read_bytes() == f"OKMKV:{2 * 16 * 16 * 3}".encode()


def test_adversarial_invalid_dimensions_fps_frames_and_numeric_overrides(tmp_path):
    """Strict rejection of invalid dimensions, fps != 25, non-4k+1 frames, bool/NaN/inf/negative."""
    from server.avatar_worker_longcat16 import run_longcat16_generation
    from server.vsr_worker_diffusion import run_diffusion_vsr

    base_lc_cfg = {
        "longcat_repo_dir": str(tmp_path / "repo"),
        "avatar_checkpoint_dir": str(tmp_path / "avatar"),
        "base_video_dir": str(tmp_path / "base"),
        "image_path": str(tmp_path / "img.png"),
        "audio_path": str(tmp_path / "aud.wav"),
        "output_video_path": str(tmp_path / "out.mkv"),
        "output_manifest_path": str(tmp_path / "out.json"),
        "width": 1280,
        "height": 768,
        "fps": 25,
        "exact_frames": 93,
        "num_frames": 93,
        "num_cond_frames": 13,
    }

    # LongCat 1.6 worker validation rejects < 16 or odd dims, fps != 25, non-4k+1 frames, bool/NaN/inf/negatives.
    for bad_patch in (
        {"width": 15},
        {"height": 8},
        {"width": 1281},
        {"height": 769},
        {"width": True},
        {"fps": 24},
        {"fps": 30},
        {"fps": True},
        {"num_frames": 92},
        {"num_frames": 94},
        {"num_cond_frames": 12},
        {"num_cond_frames": 14},
        {"exact_frames": 0},
        {"exact_frames": -5},
        {"exact_frames": True},
        {"seed": -1},
        {"seed": False},
        {"ref_img_index": -1},
        {"mask_frame_range": -1},
        {"lora_multiplier": float("nan")},
        {"lora_multiplier": float("inf")},
        {"lora_multiplier": True},
    ):
        with pytest.raises(ValueError):
            run_longcat16_generation(base_lc_cfg | bad_patch)

    # resolve_longcat16_sampling_params rejects bool/NaN/inf numeric values.
    for bad_sampling in (
        {"sampling_mode": "non_distill_cfg", "num_inference_steps": True},
        {"sampling_mode": "non_distill_cfg", "text_guidance_scale": float("nan")},
        {"sampling_mode": "non_distill_cfg", "audio_guidance_scale": float("inf")},
        {"sampling_mode": "non_distill_cfg", "audio_guidance_scale": False},
        {"sampling_mode": "distill_lora_mix", "mix_steps": True},
        {"sampling_mode": "distill_lora_mix", "mix_alpha": float("-inf")},
    ):
        with pytest.raises(ValueError):
            resolve_longcat16_sampling_params(bad_sampling)

    # Odd target dimensions are snapped to even >= 16 by parse_target_resolution / resolve_longcat_native_preset.
    odd_w, odd_h = parse_target_resolution("1281x721")
    assert odd_w % 2 == 0 and odd_h % 2 == 0 and odd_w >= 16 and odd_h >= 16
    preset_odd = resolve_longcat_native_preset({}, target_w=1921, target_h=1081)
    assert preset_odd["target_width"] % 2 == 0 and preset_odd["target_height"] % 2 == 0
    assert preset_odd["target_width"] >= 16 and preset_odd["target_height"] >= 16

    # resolve_longcat_native_preset strictly rejects bool / NaN / inf / malformed numeric overrides.
    for bad_override in (
        {"pipeline.num_inference_steps": True},
        {"pipeline.audio_guidance_scale": float("nan")},
        {"pipeline.text_guidance_scale": float("inf")},
        {"pipeline.text_guidance_scale": False},
        {"pipeline.crf": True},
        {"pipeline.crf": "not-a-num"},
        {"pipeline.seed": float("inf")},
        {"pipeline.ref_img_index": True},
        {"pipeline.sampling_mode": "distill_lora_mix", "pipeline.mix_steps": True},
        {"pipeline.sampling_mode": "distill_lora_mix", "pipeline.mix_alpha": float("nan")},
    ):
        with pytest.raises(ValueError):
            resolve_longcat_native_preset(bad_override, target_w=1920, target_h=1080)

    # VSR worker rejects bool, float, zero, and negative ints strictly.
    vsr_repo_dir = tmp_path / "vsr_repo"
    vsr_repo_dir.mkdir(parents=True, exist_ok=True)
    vsr_weights_dir = tmp_path / "vsr_w"
    _populate_concrete_weight_files(
        vsr_weights_dir,
        repo_id=COLABONE_SEEDVR2_HF_REPO,
        revision=COLABONE_SEEDVR2_HF_REV,
        patterns=vsr_engine_allow_patterns("seedvr2_7b_sharp")[2],
        concrete_relpaths=[
            "seedvr2_ema_7b_sharp_fp16.safetensors",
            "ema_vae_fp16.safetensors",
        ],
    )
    vsr_in_mkv = tmp_path / "in.mkv"
    vsr_in_mkv.write_bytes(b"fake-mkv")
    base_vsr_cfg = {
        "engine": "seedvr2_7b_sharp",
        "repo_dir": str(vsr_repo_dir),
        "weights_dir": str(vsr_weights_dir),
        "input_video_path": str(vsr_in_mkv),
        "output_video_path": str(tmp_path / "out.mkv"),
        "output_manifest_path": str(tmp_path / "out.json"),
        "source_width": 1280,
        "source_height": 768,
        "target_width": 1920,
        "target_height": 1080,
    }
    for bad_vsr in (
        {"source_width": 0},
        {"source_width": 14},
        {"source_width": 1281},
        {"source_height": -768},
        {"target_width": True},
        {"target_width": 1921},
        {"target_height": 1080.5},
        {"target_height": 12},
        {"fps": 0},
        {"seed": -1},
        {"batch_size": False},
        {"temporal_overlap": -2},
        {"exact_frames": -1},
        {"vram_mib": -100},
    ):
        with pytest.raises(ValueError):
            run_diffusion_vsr(base_vsr_cfg | bad_vsr)


def test_hf_token_scrubbing_validation_errors_and_regex_across_modules(runtime, tmp_path):
    """Raw hf_token is NEVER leaked across runtime, comfyui, FastAPI app, tts_engines, or avatar_engines."""
    from fastapi.testclient import TestClient

    from server.app import create_colabone_app
    from server.tts_engines import _HF_TOKEN_RE, _scrub_secret

    bad_ctrl_token = "hf_SuperSecretControlToken9876543210\n"
    raw_token_body = "hf_SuperSecretControlToken9876543210"
    long_alnum_token = "hf_AbCdEf1234567890GhIjKlMnOpQrStUvWx"

    # 1. server/runtime.py run_comfyui_workflow & preview_comfyui_tts scrub ValidationError.
    with pytest.raises(ValueError) as exc_rt_wf:
        runtime.run_comfyui_workflow(
            {"workflow_id": "digital-human-longcat", "hf_token": bad_ctrl_token}
        )
    assert raw_token_body not in str(exc_rt_wf.value)

    with pytest.raises(ValueError) as exc_rt_tts:
        runtime.preview_comfyui_tts(
            {"engine_id": "voxcpm2", "hf_token": bad_ctrl_token}
        )
    assert raw_token_body not in str(exc_rt_tts.value)

    # 2. server/comfyui.py run_workflow & run_tts_preview scrub ValidationError.
    task = ColabOneTask("comfyui_run", "scrub-comfy")
    with pytest.raises(ValueError) as exc_cf_wf:
        runtime.comfy.run_workflow(
            task, {"workflow_id": "digital-human-longcat", "hf_token": bad_ctrl_token}
        )
    assert raw_token_body not in str(exc_cf_wf.value)

    with pytest.raises(ValueError) as exc_cf_tts:
        runtime.comfy.run_tts_preview(
            task, {"engine_id": "voxcpm2", "hf_token": bad_ctrl_token}
        )
    assert raw_token_body not in str(exc_cf_tts.value)

    # 3. server/app.py RequestValidationError handler never reflects raw hf_token in HTTP 422.
    api_token = "c" * 32
    app = create_colabone_app(runtime, api_token, background=False)
    client = TestClient(app)
    headers = {"Authorization": f"Bearer {api_token}"}

    for endpoint, body in (
        ("/api/v1/comfyui/run", {"workflow_id": "digital-human-longcat", "hf_token": bad_ctrl_token}),
        ("/api/v1/comfyui/tts-preview", {"engine_id": "voxcpm2", "hf_token": bad_ctrl_token}),
        ("/api/v1/models/demo/download", {"hf_token": bad_ctrl_token}),
    ):
        resp = client.post(endpoint, json=body, headers=headers)
        assert resp.status_code == 422
        assert raw_token_body not in resp.text
        assert resp.json() == {"detail": "请求参数格式不合法"}

    # 4. server/tts_engines.py _HF_TOKEN_RE and _scrub_secret scrub tokens without clobbering benign names.
    assert _HF_TOKEN_RE.search(long_alnum_token) is not None
    scrubbed_text = _scrub_secret(
        f"Download failed with {long_alnum_token} and Bearer SecretBearer123456 at hf_download_longcat16_avatar.log",
        "",
    )
    assert long_alnum_token not in scrubbed_text
    assert "SecretBearer123456" not in scrubbed_text
    assert "hf_download_longcat16_avatar.log" in scrubbed_text

    # 5. server/avatar_engines.py scrubs hf_token in HF download failures, streaming worker logs, and config JSON.
    def failing_download(**kwargs):
        raise RuntimeError(f"401 Unauthorized for token {long_alnum_token} / {raw_token_body}")

    mgr = ColabOneAvatarEngineManager(
        tmp_path / "remote",
        download_fn=failing_download,
        gpu_info_fn=lambda: {"name": "Blackwell", "total_mib": 98304, "used_mib": 0},
    )
    with pytest.raises(RuntimeError) as exc_dl:
        mgr._ensure_hf_weights(
            task,
            repo_id=COLABONE_SEEDVR2_HF_REPO,
            revision=COLABONE_SEEDVR2_HF_REV,
            allow_patterns=["ema_vae_fp16.safetensors"],
            local_dir=mgr.weights_dir / "seedvr2",
            hf_token=raw_token_body,
            label="SeedVR2",
        )
    assert raw_token_body not in str(exc_dl.value)
    assert long_alnum_token not in str(exc_dl.value)

    # Streaming worker stdout/stderr and config JSON never retain hf_token.
    leak_worker = tmp_path / "leak_worker.py"
    leak_worker.write_text(
        f"import os, sys\n"
        f"assert 'HF_TOKEN' not in os.environ\n"
        f"print('leaked token {long_alnum_token}', flush=True)\n"
        f"sys.exit(1)\n",
        encoding="utf-8",
    )
    leak_python = _create_python_wrapper(tmp_path / "bin" / "leak_python", leak_worker)
    work_leak = tmp_path / "work_leak"
    mgr_real = ColabOneAvatarEngineManager(
        tmp_path / "remote2",
        gpu_info_fn=lambda: {"name": "Blackwell", "total_mib": 98304, "used_mib": 0},
    )
    with pytest.raises(RuntimeError) as exc_stream:
        mgr_real.run_longcat16(
            task,
            config={
                "output_video_path": str(work_leak / "out.mkv"),
                "hf_token": long_alnum_token,
            },
            runtime_paths={"python": leak_python},
            work_dir=work_leak,
        )
    assert long_alnum_token not in str(exc_stream.value)
    assert long_alnum_token not in (work_leak / "stage2_longcat16.log").read_text(encoding="utf-8")
    written_cfg = json.loads((work_leak / "stage2_longcat16_config.json").read_text(encoding="utf-8"))
    assert "hf_token" not in written_cfg


def test_drivefs_and_fuse_detection_forces_compile_cache_onto_local_nvme(tmp_path):
    """Both colab_drivefs and FUSE mount route weights to Drive while forcing compile cache onto local NVMe."""
    # Case 1: Google Colab native DriveFS (mode="colab_drivefs").
    drivefs_ws = tmp_path / "drivefs" / "MyDrive" / "ColabOne" / "workspace"
    drivefs_ws.mkdir(parents=True, exist_ok=True)
    fake_drivefs = Mock()
    fake_drivefs.mode = "colab_drivefs"
    fake_drivefs.is_colab_drivefs_mounted.return_value = True
    fake_drivefs._drivefs_workspace_dir.return_value = drivefs_ws
    for attr in ("persistent_cache_root", "persistent_cache_dir", "avatar_cache_dir", "cache_root", "cache_dir"):
        setattr(fake_drivefs, attr, None)

    mgr_df = ColabOneAvatarEngineManager(
        tmp_path / "local_nvme_df",
        drive=fake_drivefs,
        download_fn=lambda **kw: None,
        gpu_info_fn=lambda: {"name": "Blackwell", "total_mib": 98304, "used_mib": 0},
    )
    drive_root_df = mgr_df._resolve_drive_cache_root()
    assert drive_root_df == drivefs_ws / "avatar_native"
    # Weights resolve onto DriveFS, whereas compile cache stays on local NVMe.
    weights_path_df = mgr_df._resolve_weights_dir(
        "seedvr2",
        repo_id=COLABONE_SEEDVR2_HF_REPO,
        revision=COLABONE_SEEDVR2_HF_REV,
    )
    assert weights_path_df.is_relative_to(drive_root_df)
    compile_dir_df = mgr_df._resolve_compile_cache_dir()
    assert compile_dir_df == mgr_df.inductor_cache_dir
    assert not compile_dir_df.resolve().is_relative_to(drive_root_df.resolve())

    # Even if caller passes a compile_cache_dir inside DriveFS, _build_worker_env forces local NVMe!
    env_df = mgr_df._build_worker_env({"compile_cache_dir": drive_root_df / "inductor_cache"})
    assert Path(env_df["TORCHINDUCTOR_CACHE_DIR"]) == mgr_df.inductor_cache_dir
    assert Path(env_df["TRITON_CACHE_DIR"]) == mgr_df.inductor_cache_dir / "triton"

    # Case 2: FUSE mount (mode="mount").
    fuse_mount = tmp_path / "fuse_mount"
    fuse_mount.mkdir(parents=True, exist_ok=True)
    fake_fuse = Mock()
    fake_fuse.mode = "mount"
    fake_fuse.is_fuse_mounted.return_value = True
    fake_fuse.mount_root = fuse_mount
    for attr in ("persistent_cache_root", "persistent_cache_dir", "avatar_cache_dir", "cache_root", "cache_dir"):
        setattr(fake_fuse, attr, None)

    mgr_fuse = ColabOneAvatarEngineManager(
        tmp_path / "local_nvme_fuse",
        drive=fake_fuse,
        download_fn=lambda **kw: None,
        gpu_info_fn=lambda: {"name": "Blackwell", "total_mib": 98304, "used_mib": 0},
    )
    drive_root_fuse = mgr_fuse._resolve_drive_cache_root()
    assert drive_root_fuse == fuse_mount / "workspace" / "avatar_native"
    env_fuse = mgr_fuse._build_worker_env({"compile_cache_dir": drive_root_fuse / "inductor_cache"})
    assert Path(env_fuse["TORCHINDUCTOR_CACHE_DIR"]) == mgr_fuse.inductor_cache_dir
    assert Path(env_fuse["TRITON_CACHE_DIR"]) == mgr_fuse.inductor_cache_dir / "triton"


def test_flashvsr_short_clip_and_bright_frame_normalization_no_overflow(monkeypatch):
    """_normalize_flashvsr_tensor_output handles 3-frame clips and bright [-1, 1] values
    deterministically without 3-frame channel ambiguity or uint8 overflow/wraparound."""
    import types
    from server.vsr_worker_diffusion import _normalize_flashvsr_tensor_output

    class _ExactTensor:
        def __init__(self, shape, data, dtype="float32"):
            self.shape = tuple(shape)
            self.data = list(data)
            self.dtype = dtype

        @property
        def ndim(self):
            return len(self.shape)

        def detach(self):
            return self

        def cpu(self):
            return self

        def to(self, *args, **kwargs):
            dt = kwargs.get("dtype", args[0] if args else self.dtype)
            if dt == "uint8":
                return _ExactTensor(self.shape, [int(v) & 0xFF if 0 <= int(v) <= 255 else int(v) for v in self.data], dtype="uint8")
            return _ExactTensor(self.shape, [float(v) for v in self.data], dtype=dt)

        def unsqueeze(self, dim):
            s = list(self.shape)
            s.insert(dim, 1)
            return _ExactTensor(tuple(s), self.data, self.dtype)

        def _strides(self, shape):
            st = [1] * len(shape)
            for i in range(len(shape) - 2, -1, -1):
                st[i] = st[i + 1] * shape[i + 1]
            return st

        def __getitem__(self, key):
            if not isinstance(key, tuple):
                key = (key,)
            st = self._strides(self.shape)
            ranges = []
            out_shape = []
            for axis, s in enumerate(self.shape):
                k = key[axis] if axis < len(key) else slice(None)
                if isinstance(k, int):
                    ranges.append([k if k >= 0 else s + k])
                else:
                    ranges.append(list(range(*k.indices(s))))
                    out_shape.append(len(ranges[-1]))
            out = []
            def _walk(ax, off):
                if ax == len(ranges):
                    out.append(self.data[off])
                    return
                for idx in ranges[ax]:
                    _walk(ax + 1, off + idx * st[ax])
            _walk(0, 0)
            return _ExactTensor(tuple(out_shape), out, self.dtype)

        def permute(self, *dims):
            old_st = self._strides(self.shape)
            new_shape = tuple(self.shape[d] for d in dims)
            out = []
            coords = [0] * len(new_shape)
            def _walk(ax):
                if ax == len(new_shape):
                    off = 0
                    for new_ax, orig_ax in enumerate(dims):
                        off += coords[new_ax] * old_st[orig_ax]
                    out.append(self.data[off])
                    return
                for i in range(new_shape[ax]):
                    coords[ax] = i
                    _walk(ax + 1)
            _walk(0)
            return _ExactTensor(new_shape, out, self.dtype)

        def clamp(self, lo, hi):
            return _ExactTensor(self.shape, [max(lo, min(hi, v)) for v in self.data], self.dtype)

        def __add__(self, val):
            return _ExactTensor(self.shape, [v + val for v in self.data], self.dtype)

        def __mul__(self, val):
            return _ExactTensor(self.shape, [v * val for v in self.data], self.dtype)

        def round(self):
            return _ExactTensor(self.shape, [round(v) for v in self.data], self.dtype)

        def contiguous(self):
            return self

        def numpy(self):
            return self

    fake_torch = types.ModuleType("torch")
    fake_torch.float32 = "float32"  # type: ignore[attr-defined]
    fake_torch.uint8 = "uint8"  # type: ignore[attr-defined]
    monkeypatch.setitem(sys.modules, "torch", fake_torch)

    # 3 frames (F=3 matching C=3!), padded 4x4 cropped to 2x2, ALL bright positive values + overshoot > 1.0.
    # Layout [1, C=3, F=3, H=4, W=4]: per-channel test values across 3 frames:
    # Frame 0: R=0.50 (191), G=0.85 (236), B=1.00 (255)
    # Frame 1: R=1.40 (clamped->255), G=0.00 (128), B=-1.00 (0)
    # Frame 2: R=-1.50 (clamped->0), G=0.99 (254), B=1.05 (clamped->255)
    frame_rgb = [
        (0.50, 0.85, 1.00),
        (1.40, 0.00, -1.00),
        (-1.50, 0.99, 1.05),
    ]
    data_5d: list[float] = []
    for c in range(3):
        for f in range(3):
            val = frame_rgb[f][c]
            for _ in range(4 * 4):
                data_5d.append(val)

    vid_5d = _ExactTensor((1, 3, 3, 4, 4), data_5d)
    out = _normalize_flashvsr_tensor_output(vid_5d, expected_frames=3, cover_h=2, cover_w=2)
    assert out.shape == (3, 2, 2, 3)
    assert out.dtype == "uint8"
    assert all(0 <= px <= 255 for px in out.data)
    # Verify every frame's RGB triple matches exact [-1, 1] -> [0, 255] without wraparound!
    expected_rgb = [(191, 236, 255), (255, 128, 0), (0, 254, 255)]
    for f_idx, exp_triple in enumerate(expected_rgb):
        pixel_offset = f_idx * (2 * 2 * 3)
        assert tuple(out.data[pixel_offset : pixel_offset + 3]) == exp_triple

    # 4D input [C=3, F=3, H=4, W=4] also normalizes cleanly to (3, 2, 2, 3).
    vid_4d = _ExactTensor((3, 3, 4, 4), data_5d)
    out_4d = _normalize_flashvsr_tensor_output(vid_4d, expected_frames=3, cover_h=2, cover_w=2)
    assert out_4d.shape == (3, 2, 2, 3)
    assert out_4d.data == out.data

    # Invalid shape or undersized spatial dimensions rejected cleanly.
    with pytest.raises(RuntimeError, match="Expected FlashVSR\\+ result\\.video shape"):
        _normalize_flashvsr_tensor_output(_ExactTensor((1, 4, 3, 4, 4), [0.0] * 192), expected_frames=3, cover_h=2, cover_w=2)
    with pytest.raises(RuntimeError, match="smaller than expected cover size"):
        _normalize_flashvsr_tensor_output(vid_5d, expected_frames=3, cover_h=8, cover_w=8)


def test_stage4_oversized_mp4_triggers_automatic_reencode_fallback(runtime, tmp_path):
    """When Stage 4 master MP4 exceeds MAX_OUTPUT_BYTES (512 MiB), runner re-encodes once with target bitrate."""
    from common.comfyui import MAX_OUTPUT_BYTES

    catalog = _load_real_comfy_catalog()
    longcat_wf = next(t for t in catalog.templates if t.id == "digital-human-longcat").model_dump()
    portrait = tmp_path / "anchor.png"
    portrait.write_bytes(_make_png_bytes("olive", size=(96, 96)))
    audio_wav = _write_pcm16_wav(tmp_path / "voice_4s.wav", duration_sec=4.0, sample_rate=48000)

    class _MinimalNativeEngines:
        def check_preflight(self, **kw):
            return {"name": "Blackwell", "total_mib": 98304, "is_high_vram_96g": True, "all_weights_cached": True}

        def ensure_longcat16_ready(self, task, **kw):
            return {"python": tmp_path / "py"}

        def run_longcat16(self, task, *, config, runtime_paths, work_dir, progress_range):
            out_p = Path(config["output_video_path"])
            out_p.write_bytes(b"STAGE2-MKV")
            return {"ok": True, "output_video_path": str(out_p), "width": 1280, "height": 768}

    ff_calls: list[list[str]] = []

    def fake_ffmpeg(args, task=None, log=None, **kwargs):
        cmd = [str(x) for x in args]
        ff_calls.append(cmd)
        out = Path(cmd[-1])
        out.parent.mkdir(parents=True, exist_ok=True)
        if len(ff_calls) == 1:
            # First pass creates a sparse file > 512 MiB (zero real disk blocks on Linux).
            with out.open("wb") as fh:
                fh.truncate(MAX_OUTPUT_BYTES + 2 * 1024 * 1024)
        else:
            # Re-encode pass produces a compact valid MP4 within the 512 MiB budget.
            out.write_bytes(b"\x00\x00\x00\x18ftypmp42reencoded-compact")
        return ""

    runner = ColabOneDigitalHumanRunner(
        runtime.comfy,
        None,
        run_cmd_fn=fake_ffmpeg,
        avatar_engines=_MinimalNativeEngines(),
    )
    task = ColabOneTask("comfyui_run", "reencode-512m")
    req = WorkflowRunRequest(
        workflow_id="digital-human-longcat",
        overrides={
            "pipeline.audio_source": "audio",
            "pipeline.upscale_mode": "none",
            "pipeline.target_resolution": "1280x720",
            "pipeline.crf": 14,
        },
        inputs={"1.image": "a" * 32, "2.audio": "b" * 32},
    )
    outputs = runner.run(
        task,
        longcat_wf,
        req,
        [("1.image", "anchor.png", portrait), ("2.audio", "voice_4s.wav", audio_wav)],
    )

    assert len(outputs) == 1
    assert outputs[0]["size"] <= MAX_OUTPUT_BYTES
    assert len(ff_calls) == 2
    first_cmd, second_cmd = ff_calls
    assert "-crf" in first_cmd and first_cmd[first_cmd.index("-crf") + 1] == "14"
    assert "-crf" not in second_cmd
    assert "-b:v" in second_cmd and second_cmd[second_cmd.index("-b:v") + 1].endswith("k")
    assert "-maxrate" in second_cmd and "-bufsize" in second_cmd
    assert second_cmd[second_cmd.index("-colorspace") + 1] == "bt709"
    assert any("512 MiB" in entry["message"] and "重编码" in entry["message"] for entry in task.logs)


def test_native_mode_stops_comfyui_before_tts_never_starts_and_updates_active_pack(
    runtime, tmp_path, monkeypatch
):
    """Native mode stops running ComfyUI BEFORE Stage 1 TTS, never starts ComfyUI,
    and updates status()['active_pack'] via mark_native_pack_ready()."""
    catalog = _load_real_comfy_catalog()
    longcat_wf = next(t for t in catalog.templates if t.id == "digital-human-longcat").model_dump()
    portrait = tmp_path / "anchor.png"
    portrait.write_bytes(_make_png_bytes("maroon", size=(96, 96)))

    timeline: list[str] = []
    comfy_state = {"running": True}

    monkeypatch.setattr(runtime.comfy, "is_running", lambda: comfy_state["running"])
    monkeypatch.setattr(
        runtime.comfy,
        "start_server",
        lambda *a, **k: pytest.fail("ComfyUI start_server must NEVER run for longcat_avatar_native"),
    )
    monkeypatch.setattr(
        runtime.comfy,
        "prepare",
        lambda *a, **k: pytest.fail("ComfyUI prepare must NEVER run for longcat_avatar_native"),
    )

    def fake_stop_server():
        timeline.append("stop_comfyui")
        comfy_state["running"] = False

    monkeypatch.setattr(runtime.comfy, "stop_server", fake_stop_server)

    fake_tts = Mock()
    fake_tts.synth_fn = lambda *a, **k: None
    fake_tts.get_engine.return_value = {"id": "fish_drama3", "sample_rate": 44100}
    fake_tts.validate_tts_synthesis_inputs.return_value = {
        "engine": {"id": "fish_drama3", "sample_rate": 44100},
        "effective_voice_preset": "shen_zhiyi",
    }

    def fake_synthesize(task, **kwargs):
        assert comfy_state["running"] is False, "ComfyUI must be stopped BEFORE Stage 1 TTS!"
        timeline.append("tts_synthesize")
        out_wav = Path(kwargs["work_dir"]) / "tts_out.wav"
        _write_pcm16_wav(out_wav, duration_sec=2.0, sample_rate=48000)
        return {"audio_path": str(out_wav), "duration": 2.0, "sample_rate": 48000, "manifest": {}}

    fake_tts.synthesize.side_effect = fake_synthesize

    class _OrderEngines:
        def check_preflight(self, **kw):
            timeline.append("preflight")
            return {"name": "Blackwell", "total_mib": 98304, "is_high_vram_96g": True, "all_weights_cached": True}

        def ensure_longcat16_ready(self, task, **kw):
            timeline.append("ensure_longcat16")
            return {"python": tmp_path / "py"}

        def run_longcat16(self, task, *, config, runtime_paths, work_dir, progress_range):
            timeline.append("run_longcat16")
            out_p = Path(config["output_video_path"])
            out_p.write_bytes(b"MKV")
            return {"ok": True, "output_video_path": str(out_p), "width": 1280, "height": 768}

    def fake_ffmpeg(args, **kw):
        Path(args[-1]).write_bytes(b"\x00\x00\x00\x18ftypmp42ok")
        return ""

    assert runtime.comfy.status()["active_pack"] == ""
    runner = ColabOneDigitalHumanRunner(
        runtime.comfy,
        fake_tts,
        run_cmd_fn=fake_ffmpeg,
        avatar_engines=_OrderEngines(),
    )
    task = ColabOneTask("comfyui_run", "order-check")
    req = WorkflowRunRequest(
        workflow_id="digital-human-longcat",
        overrides={
            "pipeline.audio_source": "tts",
            "pipeline.tts_engine": "fish_drama3",
            "pipeline.script_text": "先释放 ComfyUI 显存再运行 TTS 与原生数字人。",
            "pipeline.voice_preset": "shen_zhiyi",
            "pipeline.upscale_mode": "auto",
            "pipeline.target_resolution": "1280x720",
        },
        inputs={"1.image": "a" * 32},
    )
    outputs = runner.run(task, longcat_wf, req, [("1.image", "anchor.png", portrait)])
    assert len(outputs) == 1
    assert timeline == ["preflight", "stop_comfyui", "tts_synthesize", "ensure_longcat16", "run_longcat16"]
    assert runtime.comfy.status()["active_pack"] == "digital-human-longcat"

    # Direct mark_native_pack_ready check.
    runtime.comfy.mark_native_pack_ready("custom-native-pack")
    assert runtime.comfy.status()["active_pack"] == "custom-native-pack"


def test_native_uploaded_audio_preserves_stereo_and_rgba_portrait_alpha_composited(
    runtime, tmp_path
):
    """Uploaded audio on longcat_avatar_native preserves stereo & skips -23 LUFS downmix
    with -vn -sn -dn -map 0:a:0, while RGBA portraits are alpha-composited onto studio backdrop."""
    ff_cmds: list[list[str]] = []

    def fake_ffmpeg(args, **kw):
        cmd = [str(x) for x in args]
        ff_cmds.append(cmd)
        out = Path(cmd[-1])
        _write_stereo_pcm16_wav(out, duration_sec=2.0, sample_rate=48000)
        return ""

    runner = ColabOneDigitalHumanRunner(runtime.comfy, None, run_cmd_fn=fake_ffmpeg)
    task = ColabOneTask("comfyui_run", "stereo-audio")

    # 1. 48 kHz 16-bit stereo PCM WAV on native backend: direct passthrough, stereo & dynamics intact.
    stereo_48k = _write_stereo_pcm16_wav(
        tmp_path / "stereo_48k.wav",
        duration_sec=2.0,
        sample_rate=48000,
        left_amp=2500,
        right_amp=14000,
    )
    work_a = tmp_path / "work_a"
    master_a, dur_a, valleys_a = runner._prepare_uploaded_audio(
        task,
        stereo_48k,
        work_a,
        preserve_stereo_dynamics=True,
        detect_silence=False,
    )
    assert ff_cmds == []
    assert dur_a == pytest.approx(2.0, abs=0.01)
    assert valleys_a == []
    assert master_a.read_bytes() == stereo_48k.read_bytes()
    with wave.open(str(master_a), "rb") as wf:
        assert wf.getnchannels() == 2
        assert wf.getframerate() == 48000

    # 2. 44.1 kHz stereo WAV on native backend: FFmpeg resamples to 48 kHz keeping -ac 2 and -vn -sn -dn -map 0:a:0.
    stereo_44k = _write_stereo_pcm16_wav(tmp_path / "stereo_44k.wav", duration_sec=2.0, sample_rate=44100)
    work_b = tmp_path / "work_b"
    runner._prepare_uploaded_audio(
        task,
        stereo_44k,
        work_b,
        preserve_stereo_dynamics=True,
        detect_silence=False,
    )
    assert len(ff_cmds) == 1
    cmd_44k = ff_cmds[0]
    assert "-vn" in cmd_44k and "-sn" in cmd_44k and "-dn" in cmd_44k
    assert cmd_44k[cmd_44k.index("-map") + 1] == "0:a:0"
    assert cmd_44k[cmd_44k.index("-ac") + 1] == "2"
    assert cmd_44k[cmd_44k.index("-ar") + 1] == "48000"

    # 3. Non-WAV compressed upload (.mp3) on native backend: preserves mono|stereo via aformat + -vn -sn -dn -map 0:a:0.
    ff_cmds.clear()
    fake_mp3 = tmp_path / "track.mp3"
    fake_mp3.write_bytes(b"ID3fake-mp3-payload")
    work_c = tmp_path / "work_c"
    runner._prepare_uploaded_audio(
        task,
        fake_mp3,
        work_c,
        preserve_stereo_dynamics=True,
        detect_silence=False,
    )
    assert len(ff_cmds) == 1
    cmd_mp3 = ff_cmds[0]
    assert "-vn" in cmd_mp3 and "-sn" in cmd_mp3 and "-dn" in cmd_mp3
    assert cmd_mp3[cmd_mp3.index("-map") + 1] == "0:a:0"
    assert "-ac" not in cmd_mp3
    assert cmd_mp3[cmd_mp3.index("-af") + 1] == "aformat=channel_layouts=mono|stereo"

    # 4. RGBA portrait with transparent background is cleanly alpha-composited onto (245, 245, 245) RGB.
    rgba_src = tmp_path / "portrait_rgba.png"
    rgba_img = Image.new("RGBA", (128, 128), (0, 0, 0, 0))
    # Paint an opaque blue square in the center and a semi-transparent black strip on the bottom.
    for y in range(40, 88):
        for x in range(40, 88):
            rgba_img.putpixel((x, y), (20, 80, 200, 255))
    for y in range(104, 128):
        for x in range(0, 128):
            rgba_img.putpixel((x, y), (0, 0, 0, 128))
    rgba_img.save(rgba_src, format="PNG")

    dst_rgb = tmp_path / "anchor_prepared.png"
    prepare_anchor_image(rgba_src, dst_rgb, 128, 128)
    with Image.open(dst_rgb) as out_img:
        assert out_img.mode == "RGB"
        assert out_img.size == (128, 128)
        # Fully transparent corner composited onto (245, 245, 245), NOT black (0, 0, 0).
        assert out_img.getpixel((4, 4)) == (245, 245, 245)
        # Opaque center preserved accurately.
        assert out_img.getpixel((64, 64)) == (20, 80, 200)
        # Semi-transparent black strip blended smoothly onto light gray backdrop (~122).
        r_mid, g_mid, b_mid = out_img.getpixel((64, 116))
        assert 115 <= r_mid <= 130 and 115 <= g_mid <= 130 and 115 <= b_mid <= 130


# ---------------------------------------------------------------------------
# 7. Round-2 comprehensive regression tests (workers, managers, orchestration,
#    ComfyUI, runtime, security, and schema invariants)
# ---------------------------------------------------------------------------


def _build_inprocess_longcat_stubs(
    tmp_path: Path,
    monkeypatch,
    *,
    trace: list[dict[str, Any]],
    fail_ai2v_once_with_compile_err: bool = False,
    fail_avc_once_with_compile_err: bool = False,
    ai2v_return_frames: int | None = None,
    height: int = 32,
    width: int = 32,
) -> dict[str, Any]:
    """Install lightweight in-process stub modules for avatar_worker_longcat16.run_longcat16_generation."""
    import types

    def _rec(event: str, **extra: Any) -> None:
        trace.append({"event": event, **extra})

    # Minimal numpy stub with uint8 / float32 ndarray support.
    np_mod = types.ModuleType("numpy")
    np_mod.float32 = "float32"  # type: ignore[attr-defined]
    np_mod.uint8 = "uint8"  # type: ignore[attr-defined]
    np_mod.int32 = "int32"  # type: ignore[attr-defined]
    np_mod.random = types.SimpleNamespace(seed=lambda s: None)  # type: ignore[attr-defined]

    def _prod(shape: tuple[int, ...]) -> int:
        n = 1
        for d in shape:
            n *= int(d)
        return n

    class _NdArray:
        def __init__(self, buf: bytes, shape: tuple[int, ...], dtype: str = "uint8"):
            self._buf = buf
            self.shape = tuple(int(d) for d in shape)
            self.dtype = dtype

        @property
        def ndim(self) -> int:
            return len(self.shape)

        @property
        def size(self) -> int:
            return _prod(self.shape)

        def __len__(self) -> int:
            return self.shape[0] if self.shape else 0

        def _item_size(self) -> int:
            return 4 if self.dtype == "float32" else 1

        def reshape(self, *new_shape: Any) -> "_NdArray":
            if len(new_shape) == 1 and isinstance(new_shape[0], (tuple, list)):
                new_shape = tuple(new_shape[0])
            sh = [int(d) for d in new_shape]
            if -1 in sh:
                idx = sh.index(-1)
                known = 1
                for i, d in enumerate(sh):
                    if i != idx:
                        known *= d
                sh[idx] = self.size // max(1, known)
            return _NdArray(self._buf, tuple(sh), self.dtype)

        def copy(self) -> "_NdArray":
            return _NdArray(bytes(self._buf), self.shape, self.dtype)

        def astype(self, dtype: str, copy: bool = True) -> "_NdArray":
            return _NdArray(bytes(self._buf), self.shape, dtype)

        def tobytes(self) -> bytes:
            return bytes(self._buf)

        @property
        def __array_interface__(self) -> dict[str, Any]:
            strides = None
            if len(self.shape) == 3:
                strides = (self.shape[1] * self.shape[2], self.shape[2], 1)
            return {
                "shape": self.shape,
                "typestr": "|u1" if self.dtype == "uint8" else "<f4",
                "strides": strides,
                "data": self._buf,
                "version": 3,
            }

        def __getitem__(self, key: Any) -> "_NdArray":
            stride = _prod(self.shape[1:]) * self._item_size()
            n0 = self.shape[0]
            if isinstance(key, int):
                idx = key if key >= 0 else n0 + key
                return _NdArray(
                    self._buf[idx * stride : (idx + 1) * stride],
                    self.shape[1:],
                    self.dtype,
                )
            if isinstance(key, slice):
                s_i, e_i, step = key.indices(n0)
                if step == 1:
                    return _NdArray(
                        self._buf[s_i * stride : e_i * stride],
                        (max(0, e_i - s_i), *self.shape[1:]),
                        self.dtype,
                    )
            raise TypeError(f"Unsupported key {key!r}")

        def __iter__(self):
            for i in range(self.shape[0]):
                yield self[i]

        def all(self) -> bool:
            return True

    np_mod.ndarray = _NdArray  # type: ignore[attr-defined]
    np_mod.frombuffer = lambda buf, dtype="uint8": _NdArray(  # type: ignore[attr-defined]
        bytes(buf), (len(buf) // (4 if dtype == "float32" else 1),), dtype
    )
    np_mod.zeros = lambda shape, dtype="float32": _NdArray(  # type: ignore[attr-defined]
        b"\x00" * (_prod((shape,) if isinstance(shape, int) else tuple(shape)) * (4 if dtype == "float32" else 1)),
        (shape,) if isinstance(shape, int) else tuple(shape),
        dtype,
    )
    np_mod.concatenate = lambda arrays, axis=0: _NdArray(  # type: ignore[attr-defined]
        b"".join(a._buf for a in arrays),
        (sum(a.shape[0] for a in arrays), *arrays[0].shape[1:]),
        arrays[0].dtype,
    )
    np_mod.asarray = lambda obj, dtype=None: (  # type: ignore[attr-defined]
        obj
        if isinstance(obj, _NdArray)
        else _NdArray(bytes(obj), (len(obj),), dtype or "uint8")
    )
    np_mod.ascontiguousarray = lambda obj: (  # type: ignore[attr-defined]
        memoryview(obj._buf) if isinstance(obj, _NdArray) and obj.ndim == 3 else obj
    )
    np_mod.isfinite = lambda arr: arr  # type: ignore[attr-defined]
    np_mod.clip = lambda arr, lo, hi: arr  # type: ignore[attr-defined]

    # Minimal torch + torch.distributed + torch.nn.functional stub.
    torch_mod = types.ModuleType("torch")
    torch_mod.bfloat16 = "bfloat16"  # type: ignore[attr-defined]
    torch_mod.float32 = "float32"  # type: ignore[attr-defined]
    torch_mod.uint8 = "uint8"  # type: ignore[attr-defined]
    torch_mod.int32 = "int32"  # type: ignore[attr-defined]

    class _Dev:
        def __init__(self, spec: str = "cuda:0"):
            self._spec = str(spec)
            self.type = self._spec.split(":", 1)[0]

        def __str__(self) -> str:
            return self._spec

    class _Gen:
        def __init__(self, device: Any = None):
            self.device = device
            self._state = 0

        def manual_seed(self, s: int) -> "_Gen":
            self._state = int(s)
            return self

        def get_state(self) -> int:
            return self._state

        def set_state(self, st: int) -> None:
            self._state = st
            _rec("generator_set_state", state=st)

    class _Tensor:
        def __init__(self, shape: tuple[int, ...], dtype: str = "float32"):
            self.shape = tuple(int(d) for d in shape)
            self.dtype = dtype

        def cpu(self) -> "_Tensor":
            return self

        def to(self, *args: Any, **kwargs: Any) -> "_Tensor":
            return self

        def clone(self) -> "_Tensor":
            return _Tensor(self.shape, self.dtype)

        def unsqueeze(self, dim: int) -> "_Tensor":
            s = list(self.shape)
            s.insert(dim if dim >= 0 else len(s) + 1 + dim, 1)
            return _Tensor(tuple(s), self.dtype)

        def __add__(self, other: Any) -> "_Tensor":
            return self

        def __sub__(self, other: Any) -> "_Tensor":
            return self

        def __getitem__(self, key: Any) -> "_Tensor":
            return self

        def all(self) -> bool:
            return True

        def any(self) -> bool:
            return False

    class _CompiledMarker:
        def __init__(self, orig: Any):
            self.orig = orig
            self.is_compiled = True

    def _fake_compile(obj: Any, **kwargs: Any) -> Any:
        _rec("torch_compile", target=type(obj).__name__)
        return _CompiledMarker(obj)

    def _dynamo_reset() -> None:
        _rec("dynamo_reset")

    torch_mod.device = _Dev  # type: ignore[attr-defined]
    torch_mod.Generator = _Gen  # type: ignore[attr-defined]
    torch_mod.arange = lambda *a: _Tensor((int(a[1] - a[0]) if len(a) >= 2 else int(a[0]),))  # type: ignore[attr-defined]
    torch_mod.clamp = lambda t, **k: t  # type: ignore[attr-defined]
    torch_mod.isfinite = lambda t: _Tensor(t.shape)  # type: ignore[attr-defined]
    torch_mod.isnan = lambda t: _Tensor(t.shape)  # type: ignore[attr-defined]
    torch_mod.manual_seed = lambda s: None  # type: ignore[attr-defined]
    torch_mod.compile = _fake_compile  # type: ignore[attr-defined]
    torch_mod._dynamo = types.SimpleNamespace(  # type: ignore[attr-defined]
        config=types.SimpleNamespace(cache_size_limit=64, accumulated_cache_size_limit=256),
        reset=_dynamo_reset,
    )
    torch_mod.backends = types.SimpleNamespace(  # type: ignore[attr-defined]
        cudnn=types.SimpleNamespace(benchmark=False)
    )
    torch_mod.cuda = types.SimpleNamespace(  # type: ignore[attr-defined]
        is_available=lambda: True,
        set_device=lambda idx: None,
        reset_peak_memory_stats=lambda dev=None: None,
        get_device_capability=lambda dev=None: (12, 0),
        max_memory_allocated=lambda dev=None: 1024 * 1024 * 1024,
        empty_cache=lambda: None,
        ipc_collect=lambda: None,
        synchronize=lambda dev=None: None,
        manual_seed_all=lambda s: None,
    )

    dist_mod = types.ModuleType("torch.distributed")
    pg_state = {"init": False}
    dist_mod.is_initialized = lambda: pg_state["init"]  # type: ignore[attr-defined]
    dist_mod.is_nccl_available = lambda: False  # type: ignore[attr-defined]

    def _init_pg(**kwargs: Any) -> None:
        pg_state["init"] = True

    def _destroy_pg() -> None:
        pg_state["init"] = False

    dist_mod.init_process_group = _init_pg  # type: ignore[attr-defined]
    dist_mod.destroy_process_group = _destroy_pg  # type: ignore[attr-defined]
    dist_mod.get_rank = lambda: 0  # type: ignore[attr-defined]
    dist_mod.get_world_size = lambda: 1  # type: ignore[attr-defined]
    torch_mod.distributed = dist_mod  # type: ignore[attr-defined]

    nn_mod = types.ModuleType("torch.nn")
    f_mod = types.ModuleType("torch.nn.functional")
    f_mod.scaled_dot_product_attention = lambda q, k, v, **kw: q  # type: ignore[attr-defined]
    nn_mod.functional = f_mod  # type: ignore[attr-defined]
    torch_mod.nn = nn_mod  # type: ignore[attr-defined]

    einops_mod = types.ModuleType("einops")
    einops_mod.rearrange = lambda x, *a, **k: x  # type: ignore[attr-defined]

    diffusers_mod = types.ModuleType("diffusers")
    diffusers_utils_mod = types.ModuleType("diffusers.utils")
    diffusers_utils_mod.load_image = lambda p: Image.open(p).convert("RGB")  # type: ignore[attr-defined]
    diffusers_mod.utils = diffusers_utils_mod  # type: ignore[attr-defined]

    transformers_mod = types.ModuleType("transformers")

    class _Tokenizer:
        @classmethod
        def from_pretrained(cls, *a: Any, **k: Any) -> "_Tokenizer":
            return cls()

    class _UMT5:
        def __init__(self) -> None:
            self.device = "cpu"

        @classmethod
        def from_pretrained(cls, *a: Any, **k: Any) -> "_UMT5":
            _rec("umt5_load")
            return cls()

        def to(self, dev: Any) -> "_UMT5":
            self.device = str(dev)
            _rec("umt5_to", device=self.device)
            return self

    transformers_mod.AutoTokenizer = _Tokenizer  # type: ignore[attr-defined]
    transformers_mod.UMT5EncoderModel = _UMT5  # type: ignore[attr-defined]

    # LongCat package hierarchy
    lc_root = types.ModuleType("longcat_video")
    lc_audio = types.ModuleType("longcat_video.audio_process")
    lc_cp = types.ModuleType("longcat_video.context_parallel")
    lc_cp_util = types.ModuleType("longcat_video.context_parallel.context_parallel_util")
    lc_mods = types.ModuleType("longcat_video.modules")
    lc_vae_mod = types.ModuleType("longcat_video.modules.autoencoder_kl_wan")
    lc_sched_mod = types.ModuleType("longcat_video.modules.scheduling_flow_match_euler_discrete")
    lc_avatar_pkg = types.ModuleType("longcat_video.modules.avatar")
    lc_dit_mod = types.ModuleType("longcat_video.modules.avatar.longcat_video_dit_avatar")
    lc_pipe_mod = types.ModuleType("longcat_video.pipeline_longcat_video_avatar")

    class _WhisperEnc:
        def to(self, dev: Any) -> "_WhisperEnc":
            _rec("whisper_to", device=str(dev))
            return self

    lc_audio.get_audio_encoder = lambda d, m: (_rec("whisper_load"), _WhisperEnc())[1]  # type: ignore[attr-defined]
    lc_audio.get_audio_feature_extractor = lambda d, m: object()  # type: ignore[attr-defined]
    lc_cp_util.init_context_parallel = lambda **k: None  # type: ignore[attr-defined]
    lc_cp_util.get_cp_size = lambda: 1  # type: ignore[attr-defined]
    lc_cp_util.get_optimal_split = lambda cp: (1, 1)  # type: ignore[attr-defined]
    lc_cp.context_parallel_util = lc_cp_util  # type: ignore[attr-defined]

    class _EagerSub:
        pass

    class _VAE:
        def __init__(self) -> None:
            self.encoder = _EagerSub()
            self.decoder = _EagerSub()

        @classmethod
        def from_pretrained(cls, *a: Any, **k: Any) -> "_VAE":
            _rec("vae_load")
            return cls()

        def to(self, dev: Any) -> "_VAE":
            return self

        def clear_cache(self) -> None:
            _rec("vae_clear_cache")

    lc_vae_mod.AutoencoderKLWan = _VAE  # type: ignore[attr-defined]

    class _Sched:
        @classmethod
        def from_pretrained(cls, *a: Any, **k: Any) -> "_Sched":
            return cls()

    lc_sched_mod.FlowMatchEulerDiscreteScheduler = _Sched  # type: ignore[attr-defined]

    class _SubAttn:
        def __init__(self) -> None:
            self.enable_flashattn3 = False
            self.enable_flashattn2 = False
            self.enable_xformers = False

        def _process_attn(self, q: Any, k: Any, v: Any, shape: Any) -> Any:
            return q

        def _process_cross_attn(self, *a: Any, **k: Any) -> Any:
            return a[0]

    class _Block:
        def __init__(self) -> None:
            self.attn = _SubAttn()
            self.cross_attn = _SubAttn()
            self.audio_cross_attn = _SubAttn()

    holder: dict[str, Any] = {}

    class _DiT:
        def __init__(self) -> None:
            self.blocks = [_Block()]
            self.device = "cpu"

        @classmethod
        def from_pretrained(cls, *a: Any, **k: Any) -> "_DiT":
            _rec("dit_load")
            inst = cls()
            holder["dit"] = inst
            return inst

        def to(self, dev: Any) -> "_DiT":
            self.device = str(dev)
            pipe_obj = holder.get("pipe")
            _rec(
                "dit_to",
                device=self.device,
                whisper_already_released=(
                    pipe_obj is not None and pipe_obj.audio_encoder is None
                ),
            )
            return self

    lc_dit_mod.LongCatVideoAvatarTransformer3DModel = _DiT  # type: ignore[attr-defined]

    call_counts = {"ai2v": 0, "avc": 0}

    class _Pipe:
        def __init__(self, **kwargs: Any) -> None:
            self.tokenizer = kwargs.get("tokenizer")
            self.text_encoder = kwargs.get("text_encoder")
            self.vae = kwargs.get("vae")
            self.scheduler = kwargs.get("scheduler")
            self.dit = kwargs.get("dit")
            self.audio_encoder = kwargs.get("audio_encoder")
            self.audio_feature_extractor = kwargs.get("audio_feature_extractor")
            self.model_type = kwargs.get("model_type")
            holder["pipe"] = self

        def to(self, dev: Any) -> "_Pipe":
            _rec(
                "pipe_to",
                device=str(dev),
                whisper_already_released=(self.audio_encoder is None),
            )
            return self

        def get_audio_embedding(self, speech_array: Any, **kwargs: Any) -> _Tensor:
            _rec(
                "get_audio_embedding",
                dit_device=getattr(self.dit, "device", ""),
            )
            return _Tensor((300, 5, 768), "float32")

        def generate_ai2v(self, **kwargs: Any) -> tuple[_NdArray, _Tensor]:
            call_counts["ai2v"] += 1
            exc_type = sys.exc_info()[0]
            blocks_compiled = any(
                isinstance(b, _CompiledMarker) for b in self.dit.blocks
            )
            vae_compiled = isinstance(self.vae.encoder, _CompiledMarker) or isinstance(
                self.vae.decoder, _CompiledMarker
            )
            _rec(
                "generate_ai2v",
                attempt=call_counts["ai2v"],
                active_exc_is_none=(exc_type is None),
                blocks_compiled=blocks_compiled,
                vae_compiled=vae_compiled,
            )
            if fail_ai2v_once_with_compile_err and call_counts["ai2v"] == 1:
                raise RuntimeError("torch._inductor triton kernel compilation failed")
            nf = (
                int(ai2v_return_frames)
                if ai2v_return_frames is not None
                else int(kwargs["num_frames"])
            )
            arr = _NdArray(bytes([100]) * (nf * height * width * 3), (nf, height, width, 3), "uint8")
            lat = _Tensor((1, 16, 24, height // 8, width // 8), "bfloat16")
            return (arr, lat)

        def generate_avc(self, **kwargs: Any) -> tuple[_NdArray, _Tensor]:
            call_counts["avc"] += 1
            exc_type = sys.exc_info()[0]
            blocks_compiled = any(
                isinstance(b, _CompiledMarker) for b in self.dit.blocks
            )
            vae_compiled = isinstance(self.vae.encoder, _CompiledMarker) or isinstance(
                self.vae.decoder, _CompiledMarker
            )
            _rec(
                "generate_avc",
                attempt=call_counts["avc"],
                active_exc_is_none=(exc_type is None),
                blocks_compiled=blocks_compiled,
                vae_compiled=vae_compiled,
            )
            if fail_avc_once_with_compile_err and call_counts["avc"] == 1:
                raise RuntimeError("torch._dynamo BackendCompilerFailed in triton")
            nf = int(kwargs["num_frames"])
            arr = _NdArray(bytes([140]) * (nf * height * width * 3), (nf, height, width, 3), "uint8")
            lat = _Tensor((1, 16, 24, height // 8, width // 8), "bfloat16")
            return (arr, lat)

        def _clear_cache(self) -> None:
            _rec("pipe_clear_cache")

    lc_pipe_mod.LongCatVideoAvatarPipeline = _Pipe  # type: ignore[attr-defined]

    stub_modules = {
        "numpy": np_mod,
        "torch": torch_mod,
        "torch.distributed": dist_mod,
        "torch.nn": nn_mod,
        "torch.nn.functional": f_mod,
        "einops": einops_mod,
        "diffusers": diffusers_mod,
        "diffusers.utils": diffusers_utils_mod,
        "transformers": transformers_mod,
        "longcat_video": lc_root,
        "longcat_video.audio_process": lc_audio,
        "longcat_video.context_parallel": lc_cp,
        "longcat_video.context_parallel.context_parallel_util": lc_cp_util,
        "longcat_video.modules": lc_mods,
        "longcat_video.modules.autoencoder_kl_wan": lc_vae_mod,
        "longcat_video.modules.scheduling_flow_match_euler_discrete": lc_sched_mod,
        "longcat_video.modules.avatar": lc_avatar_pkg,
        "longcat_video.modules.avatar.longcat_video_dit_avatar": lc_dit_mod,
        "longcat_video.pipeline_longcat_video_avatar": lc_pipe_mod,
    }
    for mod_name, mod_obj in stub_modules.items():
        monkeypatch.setitem(sys.modules, mod_name, mod_obj)

    # Create required directories & sample inputs.
    repo_dir = tmp_path / "longcat_repo"
    avatar_dir = tmp_path / "avatar_ckpt"
    base_dir = tmp_path / "base_ckpt"
    (avatar_dir / "whisper-large-v3").mkdir(parents=True, exist_ok=True)
    repo_dir.mkdir(parents=True, exist_ok=True)
    base_dir.mkdir(parents=True, exist_ok=True)
    img_path = tmp_path / "anchor.png"
    img_path.write_bytes(_make_png_bytes("navy", size=(width, height)))
    wav_path = _write_pcm16_wav(tmp_path / "speech.wav", duration_sec=4.0, sample_rate=16000)

    return {
        "repo_dir": repo_dir,
        "avatar_dir": avatar_dir,
        "base_dir": base_dir,
        "image_path": img_path,
        "audio_path": wav_path,
        "holder": holder,
    }


def test_r2_longcat16_flash_attn_probe_gating_head_dim_128_and_cpu_device(monkeypatch):
    """FA3/FA2 architecture & attention_backend probe gating, head_dim=128 probe shape,
    and explicit CPU device returning False."""
    import builtins
    import contextlib
    import types
    from server.avatar_worker_longcat16 import (
        _probe_flash_attn_availability,
        _smoke_test_flash_attn_module,
    )

    probe_log: list[tuple[str, tuple[int, ...]]] = []
    sync_calls: list[Any] = []

    class _FakeCudaTensor:
        def __init__(self, shape: tuple[int, ...], dtype: Any, device: Any):
            self.shape = tuple(shape)
            self.dtype = dtype
            self.device = device

    fake_torch = types.ModuleType("torch")
    fake_torch.bfloat16 = "bfloat16"  # type: ignore[attr-defined]
    fake_torch.int32 = "int32"  # type: ignore[attr-defined]
    fake_torch.no_grad = contextlib.nullcontext  # type: ignore[attr-defined]
    fake_torch.zeros = lambda shape, dtype=None, device=None: _FakeCudaTensor(shape, dtype, device)  # type: ignore[attr-defined]
    fake_torch.tensor = lambda data, dtype=None, device=None: _FakeCudaTensor((len(data),), dtype, device)  # type: ignore[attr-defined]
    fake_torch.cuda = types.SimpleNamespace(  # type: ignore[attr-defined]
        is_available=lambda: True,
        synchronize=lambda dev=None: sync_calls.append(dev),
    )
    monkeypatch.setitem(sys.modules, "torch", fake_torch)

    fa3_state = {"fail": False}
    fa3_mod = types.ModuleType("flash_attn_interface")

    def _fa3_func(q: Any, k: Any, v: Any) -> Any:
        probe_log.append(("fa3_func", q.shape))
        if fa3_state["fail"]:
            raise RuntimeError("FA3 kernel unsupported")
        return q

    def _fa3_varlen(q: Any, k: Any, v: Any, cu_q: Any, cu_k: Any, mq: int, mk: int) -> Any:
        probe_log.append(("fa3_varlen", q.shape))
        return q

    fa3_mod.flash_attn_func = _fa3_func  # type: ignore[attr-defined]
    fa3_mod.flash_attn_varlen_func = _fa3_varlen  # type: ignore[attr-defined]

    fa2_mod = types.ModuleType("flash_attn")
    fa2_mod.flash_attn_func = lambda q, k, v: (probe_log.append(("fa2_func", q.shape)), q)[1]  # type: ignore[attr-defined]
    fa2_mod.flash_attn_varlen_func = lambda q, k, v, cu_q, cu_k, mq, mk: (  # type: ignore[attr-defined]
        probe_log.append(("fa2_varlen", q.shape)),
        q,
    )[1]

    monkeypatch.setitem(sys.modules, "flash_attn_interface", fa3_mod)
    monkeypatch.setitem(sys.modules, "flash_attn", fa2_mod)

    # 1. Explicit CPU device (string or object with .type='cpu') MUST return False without invoking kernels.
    probe_log.clear()
    assert _smoke_test_flash_attn_module(fa2_mod, "cpu") is False
    assert _smoke_test_flash_attn_module(fa2_mod, types.SimpleNamespace(type="cpu")) is False
    assert probe_log == []

    # 2. CUDA device probes with exact head_dim=128 matching LongCat 1.6 DiT (4096 / 32 = 128).
    cuda_dev = types.SimpleNamespace(type="cuda")
    assert _smoke_test_flash_attn_module(fa2_mod, cuda_dev) is True
    assert probe_log == [("fa2_func", (1, 1, 1, 128)), ("fa2_varlen", (1, 1, 128))]
    assert sync_calls == [cuda_dev]

    # 3. backend="sdpa" skips probing both FA3 and FA2 entirely.
    imported_names: list[str] = []
    real_import = builtins.__import__

    def _spy_import(name: str, *args: Any, **kwargs: Any) -> Any:
        if name in ("flash_attn_interface", "flash_attn"):
            imported_names.append(name)
        return real_import(name, *args, **kwargs)

    monkeypatch.setattr(builtins, "__import__", _spy_import)

    probe_log.clear()
    imported_names.clear()
    assert _probe_flash_attn_availability(cuda_dev, major=9, backend="sdpa") == (False, False)
    assert imported_names == [] and probe_log == []

    # 4. Blackwell (major=12) + auto skips FA3 completely and probes FA2 only.
    probe_log.clear()
    imported_names.clear()
    assert _probe_flash_attn_availability(cuda_dev, major=12, backend="auto") == (False, True)
    assert "flash_attn_interface" not in imported_names
    assert "flash_attn" in imported_names
    assert all(tag.startswith("fa2_") for tag, _ in probe_log)

    # 5. Turing/Volta (major=7) skips both FA3 and FA2.
    probe_log.clear()
    imported_names.clear()
    assert _probe_flash_attn_availability(cuda_dev, major=7, backend="auto") == (False, False)
    assert imported_names == [] and probe_log == []

    # 6. Hopper (major=9) + auto probes FA3 first and short-circuits FA2 on success.
    probe_log.clear()
    imported_names.clear()
    assert _probe_flash_attn_availability(cuda_dev, major=9, backend="auto") == (True, False)
    assert imported_names == ["flash_attn_interface"]
    assert probe_log == [("fa3_func", (1, 1, 1, 128)), ("fa3_varlen", (1, 1, 128))]

    # 7. Hopper (major=9) + auto falls back to probing FA2 if FA3 kernel probe fails.
    fa3_state["fail"] = True
    probe_log.clear()
    imported_names.clear()
    assert _probe_flash_attn_availability(cuda_dev, major=9, backend="auto") == (False, True)
    assert imported_names == ["flash_attn_interface", "flash_attn"]

    # 8. Explicit backend="flash_attn_2" skips FA3 even on Hopper (major=9).
    fa3_state["fail"] = False
    probe_log.clear()
    imported_names.clear()
    assert _probe_flash_attn_availability(cuda_dev, major=9, backend="flash_attn_2") == (False, True)
    assert imported_names == ["flash_attn"]


def test_r2_longcat16_compile_fallback_outside_except_whisper_order_and_inductor_env(
    tmp_path, monkeypatch
):
    """LongCat 1.6 worker:
    - Whisper get_audio_embedding executes BEFORE dit.to(device) and pipe.to(device).
    - Segment 0 (AI2V) and Segment 1 (AVC) compile-fallback retries execute OUTSIDE
      `except Exception:` (sys.exc_info()[0] is None) after restoring eager DiT/VAE.
    - Manager TORCHINDUCTOR_CACHE_DIR / TRITON_CACHE_DIR env wins over cfg['compile_cache_dir']."""
    from server import avatar_worker_longcat16 as lc_worker

    # Stub FFV1 pipe writer so no external ffmpeg is needed.
    writer_events: list[str] = []

    class _StubWriter:
        def __init__(self, ffmpeg_bin, width, height, fps, output_path):
            self.output_path = Path(output_path)
            self.frames_written = 0

        def write_frames(self, frames):
            self.frames_written += len(frames)
            return len(frames)

        def close(self):
            writer_events.append("close")
            self.output_path.write_bytes(b"FAKE-FFV1-MKV")

        def abort(self):
            writer_events.append("abort")

    monkeypatch.setattr(lc_worker, "_ColabOneFfv1PipeWriter", _StubWriter)
    monkeypatch.setattr(
        lc_worker,
        "_load_audio_mono_16k",
        lambda audio_path, **kw: (sys.modules["numpy"].zeros((16000 * 5,), "float32"), 16000),
    )

    # Case 1: Segment 0 compile fallback outside except + Whisper order + env precedence.
    trace1: list[dict[str, Any]] = []
    env1 = _build_inprocess_longcat_stubs(
        tmp_path / "case1",
        monkeypatch,
        trace=trace1,
        fail_ai2v_once_with_compile_err=True,
    )
    mgr_inductor = tmp_path / "mgr_nvme_inductor"
    mgr_triton = tmp_path / "mgr_nvme_triton"
    cfg_cache_ignored = tmp_path / "cfg_should_not_win"
    monkeypatch.setenv("TORCHINDUCTOR_CACHE_DIR", str(mgr_inductor))
    monkeypatch.setenv("TRITON_CACHE_DIR", str(mgr_triton))

    out_mkv_1 = tmp_path / "case1" / "out.mkv"
    out_manifest_1 = tmp_path / "case1" / "manifest.json"
    manifest1 = lc_worker.run_longcat16_generation(
        {
            "longcat_repo_dir": str(env1["repo_dir"]),
            "avatar_checkpoint_dir": str(env1["avatar_dir"]),
            "base_video_dir": str(env1["base_dir"]),
            "image_path": str(env1["image_path"]),
            "audio_path": str(env1["audio_path"]),
            "output_video_path": str(out_mkv_1),
            "output_manifest_path": str(out_manifest_1),
            "exact_frames": 20,
            "num_frames": 21,
            "num_cond_frames": 5,
            "width": 32,
            "height": 32,
            "fps": 25,
            "vocal_separation": "never",
            "attention_backend": "sdpa",
            "compile_dit": True,
            "compile_vae": True,
            "compile_cache_dir": str(cfg_cache_ignored),
        }
    )
    assert manifest1["ok"] is True
    assert manifest1["frames_written"] == 20
    assert writer_events == ["close"]
    assert os.environ["TORCHINDUCTOR_CACHE_DIR"] == str(mgr_inductor)
    assert os.environ["TRITON_CACHE_DIR"] == str(mgr_triton)
    assert not cfg_cache_ignored.exists()

    # Verify Whisper get_audio_embedding ran BEFORE dit.to(device) and pipe.to(device).
    names1 = [e["event"] for e in trace1]
    assert names1.index("get_audio_embedding") < names1.index("dit_to") < names1.index("pipe_to")
    emb_evt = next(e for e in trace1 if e["event"] == "get_audio_embedding")
    assert emb_evt["dit_device"] == "cpu"
    dit_to_evt = next(e for e in trace1 if e["event"] == "dit_to")
    pipe_to_evt = next(e for e in trace1 if e["event"] == "pipe_to")
    assert dit_to_evt["whisper_already_released"] is True
    assert pipe_to_evt["whisper_already_released"] is True

    # Verify Segment 0 compile retry ran OUTSIDE except (sys.exc_info()[0] is None) in eager mode.
    ai2v_evts = [e for e in trace1 if e["event"] == "generate_ai2v"]
    assert len(ai2v_evts) == 2
    assert ai2v_evts[0]["blocks_compiled"] is True and ai2v_evts[0]["vae_compiled"] is True
    assert ai2v_evts[1]["active_exc_is_none"] is True
    assert ai2v_evts[1]["blocks_compiled"] is False and ai2v_evts[1]["vae_compiled"] is False
    assert "dynamo_reset" in names1

    # Case 2: Segment 1 (AVC) compile fallback also executes outside except with generator state restored,
    # and unset TORCHINDUCTOR_CACHE_DIR falls back to cfg['compile_cache_dir'].
    trace2: list[dict[str, Any]] = []
    writer_events.clear()
    env2 = _build_inprocess_longcat_stubs(
        tmp_path / "case2",
        monkeypatch,
        trace=trace2,
        fail_avc_once_with_compile_err=True,
    )
    monkeypatch.delenv("TORCHINDUCTOR_CACHE_DIR", raising=False)
    monkeypatch.delenv("TRITON_CACHE_DIR", raising=False)
    cfg_cache_used = tmp_path / "case2_cfg_cache"
    manifest2 = lc_worker.run_longcat16_generation(
        {
            "longcat_repo_dir": str(env2["repo_dir"]),
            "avatar_checkpoint_dir": str(env2["avatar_dir"]),
            "base_video_dir": str(env2["base_dir"]),
            "image_path": str(env2["image_path"]),
            "audio_path": str(env2["audio_path"]),
            "output_video_path": str(tmp_path / "case2" / "out.mkv"),
            "output_manifest_path": str(tmp_path / "case2" / "manifest.json"),
            "exact_frames": 25,  # > num_frames=21 -> 2 segments (AI2V + AVC)
            "num_frames": 21,
            "num_cond_frames": 5,
            "width": 32,
            "height": 32,
            "fps": 25,
            "vocal_separation": "never",
            "attention_backend": "sdpa",
            "compile_dit": True,
            "compile_vae": True,
            "compile_cache_dir": str(cfg_cache_used),
        }
    )
    assert manifest2["ok"] is True
    assert manifest2["frames_written"] == 25
    assert os.environ["TORCHINDUCTOR_CACHE_DIR"] == str(cfg_cache_used)
    assert os.environ["TRITON_CACHE_DIR"] == str(cfg_cache_used / "triton")
    assert cfg_cache_used.is_dir()
    avc_evts = [e for e in trace2 if e["event"] == "generate_avc"]
    assert len(avc_evts) == 2
    assert avc_evts[0]["blocks_compiled"] is True
    assert avc_evts[1]["active_exc_is_none"] is True
    assert avc_evts[1]["blocks_compiled"] is False and avc_evts[1]["vae_compiled"] is False
    assert any(e["event"] == "generator_set_state" for e in trace2)


def test_r2_longcat16_frame_count_mismatch_aborts_before_publishing_mkv(
    tmp_path, monkeypatch
):
    """If written frames != exact_frames, worker raises BEFORE ffmpeg_writer.close() publishes MKV."""
    from server import avatar_worker_longcat16 as lc_worker

    writer_calls: list[str] = []

    class _AbortSpyWriter:
        def __init__(self, ffmpeg_bin, width, height, fps, output_path):
            self.output_path = Path(output_path)
            self.frames_written = 0

        def write_frames(self, frames):
            self.frames_written += len(frames)
            return len(frames)

        def close(self):
            writer_calls.append("close")
            self.output_path.write_bytes(b"MUST-NOT-PUBLISH")

        def abort(self):
            writer_calls.append("abort")

    monkeypatch.setattr(lc_worker, "_ColabOneFfv1PipeWriter", _AbortSpyWriter)
    monkeypatch.setattr(
        lc_worker,
        "_load_audio_mono_16k",
        lambda audio_path, **kw: (sys.modules["numpy"].zeros((16000 * 3,), "float32"), 16000),
    )

    trace: list[dict[str, Any]] = []
    env = _build_inprocess_longcat_stubs(
        tmp_path,
        monkeypatch,
        trace=trace,
        ai2v_return_frames=6,  # Short output vs exact_frames=12
    )
    out_mkv = tmp_path / "truncated.mkv"
    out_manifest = tmp_path / "manifest.json"
    with pytest.raises(RuntimeError, match="Frame count mismatch: wrote 6 frames, expected 12"):
        lc_worker.run_longcat16_generation(
            {
                "longcat_repo_dir": str(env["repo_dir"]),
                "avatar_checkpoint_dir": str(env["avatar_dir"]),
                "base_video_dir": str(env["base_dir"]),
                "image_path": str(env["image_path"]),
                "audio_path": str(env["audio_path"]),
                "output_video_path": str(out_mkv),
                "output_manifest_path": str(out_manifest),
                "exact_frames": 12,
                "num_frames": 21,
                "num_cond_frames": 5,
                "width": 32,
                "height": 32,
                "fps": 25,
                "vocal_separation": "never",
                "attention_backend": "sdpa",
                "compile_dit": False,
                "compile_vae": False,
            }
        )

    assert "close" not in writer_calls
    assert writer_calls == ["abort"]
    assert not out_mkv.exists()
    assert not out_manifest.exists()


def test_r2_vsr_sigterm_reaps_live_child_and_tearing_down_suppresses_reentrant_signal(
    tmp_path,
):
    """Real SIGTERM on vsr_worker_diffusion.py terminates & reaps live SeedVR2 child BEFORE exit,
    and ColabOneProcessGuard._tearing_down suppresses re-entrant signal raises during cleanup_all()."""
    import signal
    import subprocess
    from server.vsr_worker_diffusion import (
        ColabOneProcessGuard,
        _ColabOneWorkerTerminated,
    )

    # Part A: Guard _tearing_down re-entrant signal suppression.
    guard = ColabOneProcessGuard(base_dir=tmp_path / "guard_work")
    tdir = guard.create_temp_dir(prefix="reentrant_")
    assert tdir.is_dir()

    # Before teardown, handle_signal raises _ColabOneWorkerTerminated.
    with pytest.raises(_ColabOneWorkerTerminated) as exc_info:
        guard.handle_signal(signal.SIGTERM, None)
    assert exc_info.value.signum == int(signal.SIGTERM)

    # During cleanup_all(), re-entrant signal is suppressed so temp dir cleanup completes.
    reentrant_called = {"ok": False}
    orig_term = guard.terminate_children

    def _term_with_reentrant(grace_seconds: float = 1.5) -> None:
        orig_term(grace_seconds=grace_seconds)
        # Must NOT raise while _tearing_down is True.
        guard.handle_signal(signal.SIGINT, None)
        reentrant_called["ok"] = True

    guard.terminate_children = _term_with_reentrant  # type: ignore[method-assign]
    guard.cleanup_all(grace_seconds=0.5)
    assert reentrant_called["ok"] is True
    assert guard.terminated_signal == int(signal.SIGINT)
    assert not tdir.exists()

    # Part B: Real POSIX subprocess SIGTERM reaps live SeedVR2 child before worker exits.
    # (Windows TerminateProcess terminates immediately without running Python SIGTERM handlers.)
    if os.name != "posix":
        return

    work_dir = tmp_path / "vsr_sigterm_work"
    work_dir.mkdir(parents=True, exist_ok=True)
    repo_dir = tmp_path / "seedvr2_hang_repo"
    repo_dir.mkdir(parents=True, exist_ok=True)
    weights_dir = tmp_path / "seedvr2_weights"
    weights_dir.mkdir(parents=True, exist_ok=True)
    (weights_dir / "seedvr2_ema_7b_sharp_fp16.safetensors").write_bytes(b"dit")
    (weights_dir / "ema_vae_fp16.safetensors").write_bytes(b"vae")

    in_mp4 = work_dir / "input.mp4"
    in_mp4.write_bytes(b"fake-mp4-no-transcode")
    child_pid_file = work_dir / "child_pid.txt"
    (repo_dir / "inference_cli.py").write_text(
        f"""import os, time
from pathlib import Path
Path({str(child_pid_file)!r}).write_text(str(os.getpid()), encoding="utf-8")
print("Phase 1: VAE encoding batch 1/1", flush=True)
time.sleep(30)
""",
        encoding="utf-8",
    )

    out_mkv = work_dir / "out.mkv"
    out_manifest = work_dir / "manifest.json"
    cfg_path = work_dir / "cfg.json"
    cfg_path.write_text(
        json.dumps(
            {
                "engine": "seedvr2_7b_sharp",
                "repo_dir": str(repo_dir),
                "weights_dir": str(weights_dir),
                "work_dir": str(work_dir),
                "input_video_path": str(in_mp4),
                "output_video_path": str(out_mkv),
                "output_manifest_path": str(out_manifest),
                "source_width": 640,
                "source_height": 480,
                "target_width": 1280,
                "target_height": 720,
                "fps": 25,
                "exact_frames": 10,
                "vram_mib": 98304,
                "python_bin": sys.executable,
            }
        ),
        encoding="utf-8",
    )

    worker_script = ROOT / "server" / "vsr_worker_diffusion.py"
    proc = subprocess.Popen(
        [sys.executable, str(worker_script), "--config", str(cfg_path)],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        stdin=subprocess.DEVNULL,
        start_new_session=True,
    )
    try:
        deadline = time.monotonic() + 10.0
        while not child_pid_file.is_file() and time.monotonic() < deadline:
            assert proc.poll() is None, f"VSR worker exited early: {proc.stderr.read().decode()}"
            time.sleep(0.05)
        assert child_pid_file.is_file(), "Child inference_cli.py did not start in time"
        child_pid = int(child_pid_file.read_text(encoding="utf-8").strip())
        # Confirm child is alive before SIGTERM.
        os.kill(child_pid, 0)

        proc.send_signal(signal.SIGTERM)
        rc = proc.wait(timeout=10.0)
        assert rc == 128 + int(signal.SIGTERM)

        # Child MUST already be terminated & reaped before worker exits.
        with pytest.raises(ProcessLookupError):
            os.kill(child_pid, 0)

        # Temp subdirs cleaned and error manifest written.
        assert list(work_dir.glob("colabone_vsr_*")) == []
        assert out_manifest.is_file()
        manifest = json.loads(out_manifest.read_text(encoding="utf-8"))
        assert manifest["ok"] is False
        assert manifest["terminated_signal"] == int(signal.SIGTERM)
    finally:
        if proc.poll() is None:
            proc.kill()
            proc.wait(timeout=3.0)


def test_r2_vsr_seedvr2_pre_phase1_tqdm_and_ffmpeg4_vsync_fallback(tmp_path, monkeypatch):
    """Pre-Phase-1 tqdm 100% / 1/1 [ lines must NOT lock ColabOneSeedVR2ProgressTracker,
    and FFmpeg 4.x falls back cleanly to `-vsync passthrough`."""
    from server import vsr_worker_diffusion as vsr_mod
    from server.vsr_worker_diffusion import (
        ColabOneProcessGuard,
        ColabOneSeedVR2ProgressTracker,
        _prepare_seedvr2_input_video,
        _resolve_ffmpeg_fps_mode_flag,
    )

    # 1. Pre-Phase-1 tqdm lines ignored cleanly.
    tracker = ColabOneSeedVR2ProgressTracker(total_chunks=1)
    for noisy in (
        "Loading weights: 100%|##########| 1/1 [00:02<00:00, 2.00s/it]",
        "Fetching metadata 100%",
        "1/1 [00:00<00:00, 50.00it/s]",
    ):
        assert tracker.update(noisy) is None
        assert tracker.max_progress == 0.0

    # Once Phase 1 begins, progress advances smoothly and is NOT pinned at 1.0.
    p1 = tracker.update("Phase 1: VAE encoding batch 1/2")
    assert p1 == pytest.approx(0.05)
    p2 = tracker.update("Phase 2: DiT upscaling batch 1/2")
    assert p2 == pytest.approx(0.44)
    p3 = tracker.update("Phase 3: VAE decoding batch 1/1")
    assert p3 == pytest.approx(0.92)
    p4 = tracker.update("Phase 4: Post-processing complete")
    assert p4 == pytest.approx(1.0)

    # 2. FFmpeg 4.x `-vsync passthrough` fallback vs FFmpeg 5.1+ `-fps_mode passthrough`.
    ff4_script = tmp_path / "ff4_stub.py"
    ff4_script.write_text(
        'import sys\nprint("ffmpeg version 4.4.2\\n  -vsync   video sync method")\n',
        encoding="utf-8",
    )
    ff4_bin = _create_python_wrapper(tmp_path / "bin4" / "ffmpeg", ff4_script)

    ff6_script = tmp_path / "ff6_stub.py"
    ff6_script.write_text(
        'import sys\nprint("ffmpeg version 6.1\\n  -fps_mode   set framerate mode")\n',
        encoding="utf-8",
    )
    ff6_bin = _create_python_wrapper(tmp_path / "bin6" / "ffmpeg", ff6_script)

    assert _resolve_ffmpeg_fps_mode_flag(str(ff4_bin)) == "-vsync"
    assert _resolve_ffmpeg_fps_mode_flag(str(ff6_bin)) == "-fps_mode"

    cmd_ff4 = build_cover_crop_lossless_cmd(
        str(ff4_bin),
        tmp_path / "in.mp4",
        tmp_path / "out.mkv",
        target_w=1920,
        target_h=1080,
        fps=25,
        exact_frames=50,
        is_png_sequence=False,
        fps_mode_flag="-vsync",
    )
    assert "-fps_mode" not in cmd_ff4
    assert cmd_ff4[cmd_ff4.index("-vsync") + 1] == "passthrough"

    # Also verify _prepare_seedvr2_input_video uses `-vsync passthrough` for FFmpeg 4.x.
    captured_cmds: list[list[str]] = []

    def _fake_run_ffmpeg(cmd: list[str], guard: ColabOneProcessGuard) -> None:
        captured_cmds.append(list(cmd))
        Path(cmd[-1]).write_bytes(b"compat-mp4")

    monkeypatch.setattr(vsr_mod, "_run_ffmpeg_command", _fake_run_ffmpeg)
    src_mkv = tmp_path / "stage2.mkv"
    src_mkv.write_bytes(b"fake-mkv")
    guard = ColabOneProcessGuard(base_dir=tmp_path)
    out_compat = _prepare_seedvr2_input_video(
        src_mkv,
        exact_frames=25,
        fps=25,
        ffmpeg_bin=str(ff4_bin),
        tmp_dir=tmp_path,
        guard=guard,
    )
    assert out_compat.name == "seedvr2_input_lossless.mp4"
    assert len(captured_cmds) == 1
    assert "-fps_mode" not in captured_cmds[0]
    assert captured_cmds[0][captured_cmds[0].index("-vsync") + 1] == "passthrough"


def test_r2_vsr_flashvsr_linear_crossfade_exact_frames_and_crop_before_float32(
    tmp_path, monkeypatch
):
    """FlashVSR+:
    - `_crossfade_overlap_frames` computes exact linear ramp w=(k+1)/(K+1).
    - Multi-chunk `_run_flashvsr_python_pipeline` crossfades overlap frames and writes exact_frames.
    - `_normalize_flashvsr_tensor_output` crops padded [1, 3, T, pad_h, pad_w] BEFORE float32 cast."""
    import contextlib
    import types
    from server import vsr_worker_diffusion as vsr_mod
    from server.vsr_worker_diffusion import (
        ColabOneProcessGuard,
        _crossfade_overlap_frames,
        _normalize_flashvsr_tensor_output,
        _run_flashvsr_python_pipeline,
    )

    # 1. Pure-Python arithmetic numpy stub to test _crossfade_overlap_frames numerically.
    class _MathArr:
        def __init__(self, shape: tuple[int, ...], vals: list[float], dtype: str = "uint8"):
            self.shape = tuple(shape)
            self.vals = list(vals)
            self.dtype = dtype

        def astype(self, dt: str) -> "_MathArr":
            if dt == "uint8":
                return _MathArr(self.shape, [int(v) for v in self.vals], "uint8")
            return _MathArr(self.shape, [float(v) for v in self.vals], "float32")

        def copy(self) -> "_MathArr":
            return _MathArr(self.shape, list(self.vals), self.dtype)

        def __mul__(self, scalar: float) -> "_MathArr":
            return _MathArr(self.shape, [v * float(scalar) for v in self.vals], self.dtype)

        def __add__(self, other: "_MathArr") -> "_MathArr":
            return _MathArr(
                self.shape, [a + b for a, b in zip(self.vals, other.vals)], self.dtype
            )

        def __getitem__(self, key: Any) -> "_MathArr":
            stride = 1
            for d in self.shape[1:]:
                stride *= d
            n0 = self.shape[0]
            if isinstance(key, int):
                idx = key if key >= 0 else n0 + key
                return _MathArr(
                    self.shape[1:], self.vals[idx * stride : (idx + 1) * stride], self.dtype
                )
            if isinstance(key, slice):
                s_i, e_i, _ = key.indices(n0)
                return _MathArr(
                    (max(0, e_i - s_i), *self.shape[1:]),
                    self.vals[s_i * stride : e_i * stride],
                    self.dtype,
                )
            raise TypeError(key)

    math_np = types.ModuleType("numpy")
    math_np.uint8 = "uint8"  # type: ignore[attr-defined]
    math_np.float32 = "float32"  # type: ignore[attr-defined]
    math_np.asarray = lambda obj, dtype=None: (  # type: ignore[attr-defined]
        obj.astype(dtype) if (isinstance(obj, _MathArr) and dtype) else obj
    )
    math_np.ascontiguousarray = lambda obj: obj  # type: ignore[attr-defined]
    math_np.rint = lambda arr: _MathArr(arr.shape, [round(v) for v in arr.vals], arr.dtype)  # type: ignore[attr-defined]
    math_np.clip = lambda arr, lo, hi: _MathArr(  # type: ignore[attr-defined]
        arr.shape, [max(lo, min(hi, v)) for v in arr.vals], arr.dtype
    )
    math_np.stack = lambda arrs, axis=0: _MathArr(  # type: ignore[attr-defined]
        (len(arrs), *arrs[0].shape),
        [v for a in arrs for v in a.vals],
        arrs[0].dtype,
    )
    math_np.repeat = lambda arr, reps, axis=0: _MathArr(  # type: ignore[attr-defined]
        (arr.shape[0] * reps, *arr.shape[1:]),
        arr.vals * reps,
        arr.dtype,
    )
    math_np.concatenate = lambda arrs, axis=0: _MathArr(  # type: ignore[attr-defined]
        (sum(a.shape[0] for a in arrs), *arrs[0].shape[1:]),
        [v for a in arrs for v in a.vals],
        arrs[0].dtype,
    )
    monkeypatch.setitem(sys.modules, "numpy", math_np)

    # K = 3 frames (1x1 RGB): prev = 0, next = 255 -> weights 1/4, 2/4, 3/4 -> [64, 128, 191].
    prev_tail = _MathArr((3, 1, 1, 3), [0] * 9, "uint8")
    next_head = _MathArr((3, 1, 1, 3), [255] * 9, "uint8")
    blended = _crossfade_overlap_frames(prev_tail, next_head)
    assert blended.shape == (3, 1, 1, 3)
    assert blended.dtype == "uint8"
    assert [blended[k].vals[0] for k in range(3)] == [64, 128, 191]

    # 2. Crop-before-float32 ordering spy in _normalize_flashvsr_tensor_output.
    ops_order: list[tuple[str, tuple[int, ...]]] = []

    class _SpyTensor:
        def __init__(self, shape: tuple[int, ...], dtype: str = "bfloat16"):
            self.shape = tuple(shape)
            self.dtype = dtype

        @property
        def ndim(self) -> int:
            return len(self.shape)

        def detach(self) -> "_SpyTensor":
            return self

        def cpu(self) -> "_SpyTensor":
            ops_order.append(("cpu", self.shape))
            return self

        def __getitem__(self, key: Any) -> "_SpyTensor":
            if not isinstance(key, tuple):
                key = (key,)
            out_shape: list[int] = []
            for ax, s in enumerate(self.shape):
                k = key[ax] if ax < len(key) else slice(None)
                if isinstance(k, int):
                    continue
                s_i, e_i, _ = k.indices(s)
                out_shape.append(max(0, e_i - s_i))
            res = _SpyTensor(tuple(out_shape), self.dtype)
            ops_order.append(("slice", res.shape))
            return res

        def permute(self, *dims: int) -> "_SpyTensor":
            return _SpyTensor(tuple(self.shape[d] for d in dims), self.dtype)

        def float(self) -> "_SpyTensor":
            ops_order.append(("float", self.shape))
            return _SpyTensor(self.shape, "float32")

        def clamp_(self, lo: float, hi: float) -> "_SpyTensor":
            return self

        def add_(self, v: float) -> "_SpyTensor":
            return self

        def mul_(self, v: float) -> "_SpyTensor":
            return self

        def round_(self) -> "_SpyTensor":
            return self

        def to(self, *args: Any, **kwargs: Any) -> "_SpyTensor":
            dt = kwargs.get("dtype", args[0] if args else self.dtype)
            ops_order.append((f"to_{dt}", self.shape))
            return _SpyTensor(self.shape, str(dt))

        def contiguous(self) -> "_SpyTensor":
            return self

        def numpy(self) -> _MathArr:
            n = 1
            for d in self.shape:
                n *= d
            return _MathArr(self.shape, [128] * n, "uint8")

    fake_torch = types.ModuleType("torch")
    fake_torch.float32 = "float32"  # type: ignore[attr-defined]
    fake_torch.uint8 = "uint8"  # type: ignore[attr-defined]
    fake_torch.cuda = types.SimpleNamespace(is_available=lambda: False, empty_cache=lambda: None)  # type: ignore[attr-defined]
    fake_torch.inference_mode = contextlib.nullcontext  # type: ignore[attr-defined]
    fake_torch.from_numpy = lambda arr: _SpyTensor(arr.shape, "uint8")  # type: ignore[attr-defined]
    # Add div_/sub_ on _SpyTensor for pipeline input build path.
    _SpyTensor.div_ = lambda self, v: self  # type: ignore[attr-defined]
    _SpyTensor.sub_ = lambda self, v: self  # type: ignore[attr-defined]
    _SpyTensor.unsqueeze = lambda self, d: _SpyTensor((1, *self.shape), self.dtype)  # type: ignore[attr-defined]

    nn_mod = types.ModuleType("torch.nn")
    f_mod = types.ModuleType("torch.nn.functional")
    f_mod.interpolate = lambda t, size=None, **kw: _SpyTensor((t.shape[0], t.shape[1], size[0], size[1]), "float32")  # type: ignore[attr-defined]
    f_mod.pad = lambda t, pad_spec, **kw: _SpyTensor(  # type: ignore[attr-defined]
        (t.shape[0], t.shape[1], t.shape[2] + pad_spec[2] + pad_spec[3], t.shape[3] + pad_spec[0] + pad_spec[1]),
        "float32",
    )
    nn_mod.functional = f_mod  # type: ignore[attr-defined]
    fake_torch.nn = nn_mod  # type: ignore[attr-defined]
    monkeypatch.setitem(sys.modules, "torch", fake_torch)
    monkeypatch.setitem(sys.modules, "torch.nn", nn_mod)
    monkeypatch.setitem(sys.modules, "torch.nn.functional", f_mod)

    # Padded [1, 3, 12, 256, 256] -> crop to [1, 3, 10, 128, 128] BEFORE calling .float()!
    padded = _SpyTensor((1, 3, 12, 256, 256), dtype="bfloat16")
    out_arr = _normalize_flashvsr_tensor_output(padded, expected_frames=10, cover_h=128, cover_w=128)
    assert out_arr.shape == (10, 128, 128, 3)
    float_entries = [item for item in ops_order if item[0] in ("float", "to_float32")]
    assert len(float_entries) == 1
    assert float_entries[0][1] == (10, 128, 128, 3)  # Cast happened ONLY on cropped tensor!
    assert ops_order.index(("slice", (1, 3, 10, 128, 128))) < ops_order.index(float_entries[0])

    # 3. Multi-chunk _run_flashvsr_python_pipeline (160 frames > 140 single-pass threshold)
    #    invokes linear overlap crossfade and writes EXACTLY 160 frames.
    written_batches: list[int] = []
    crossfade_calls: list[int] = []
    orig_crossfade = vsr_mod._crossfade_overlap_frames

    def _spy_crossfade(prev_t: Any, next_h: Any) -> Any:
        crossfade_calls.append(int(prev_t.shape[0]))
        return orig_crossfade(prev_t, next_h)

    monkeypatch.setattr(vsr_mod, "_crossfade_overlap_frames", _spy_crossfade)

    class _FakeReader:
        def __init__(self, *args: Any, **kwargs: Any):
            self.remaining = 160

        def read_frames(self, n: int) -> list[_MathArr]:
            cnt = min(n, self.remaining)
            self.remaining -= cnt
            return [_MathArr((16, 16, 3), [50] * (16 * 16 * 3), "uint8") for _ in range(cnt)]

        def close(self, check_returncode: bool = True) -> None:
            return None

    class _FakeWriter:
        def __init__(self, *args: Any, **kwargs: Any):
            self.finalized = False

        def write_frames(self, arr: Any) -> None:
            written_batches.append(int(arr.shape[0]))

        def finalize(self) -> None:
            self.finalized = True

        def abort(self) -> None:
            return None

    monkeypatch.setattr(vsr_mod, "_ColabOneRgb24FrameReader", _FakeReader)
    monkeypatch.setattr(vsr_mod, "_ColabOneFfv1CoverCropWriter", _FakeWriter)

    fake_inf = types.ModuleType("inference")
    fake_inf.ModelPaths = lambda **kw: types.SimpleNamespace(**kw)  # type: ignore[attr-defined]
    fake_inf.InferenceRequest = lambda **kw: types.SimpleNamespace(**kw)  # type: ignore[attr-defined]
    fake_inf.stream_config_for_mode = lambda mode, **kw: {"mode": mode, **kw}  # type: ignore[attr-defined]
    fake_inf.build_inference_pipeline = lambda cfg, paths: types.SimpleNamespace(  # type: ignore[attr-defined]
        infer=lambda req: types.SimpleNamespace(
            video=_SpyTensor(req.lq_video.shape, dtype="bfloat16")
        )
    )
    monkeypatch.setitem(sys.modules, "inference", fake_inf)

    repo_dir = tmp_path / "flash_repo"
    (repo_dir / "prompt_tensor").mkdir(parents=True, exist_ok=True)
    (repo_dir / "prompt_tensor" / "posi_prompt.pth").write_bytes(b"p")
    weights_dir = tmp_path / "flash_weights"
    weights_dir.mkdir(parents=True, exist_ok=True)
    model_files = {
        k: weights_dir / k
        for k in (
            "diffusion_pytorch_model_streaming_dmd.safetensors",
            "LQ_proj_in.ckpt",
            "TCDecoder.ckpt",
        )
    }
    for p in model_files.values():
        p.write_bytes(b"w")

    meta = _run_flashvsr_python_pipeline(
        repo_dir=repo_dir,
        weights_dir=weights_dir,
        model_files=model_files,
        input_video_path=tmp_path / "in.mkv",
        output_video_path=tmp_path / "out.mkv",
        source_width=16,
        source_height=16,
        target_width=16,
        target_height=16,
        fps=25,
        exact_frames=160,
        seed=42,
        color_correction="lab",
        vram_mib=98304,
        ffmpeg_bin="ffmpeg",
        guard=ColabOneProcessGuard(base_dir=tmp_path),
    )
    assert meta["chunks"] == 2
    assert crossfade_calls == [8]
    assert sum(written_batches) == 160


@pytest.mark.skipif(
    os.name != "posix",
    reason="POSIX process-group cleanup (os.killpg) applies to Linux Colab runtime",
)
def test_r2_avatar_manager_unconditional_pgrp_cleanup_and_2s_pipe_drain_deadline(tmp_path):
    """_run_streaming_worker enforces 2s pipe-drain deadline when an orphan child holds stdout
    and unconditionally runs _terminate_process_group in finally to reap any orphan children."""
    mgr = ColabOneAvatarEngineManager(tmp_path / "remote")
    work_dir = tmp_path / "work"
    work_dir.mkdir(parents=True, exist_ok=True)

    # Case 1: Worker spawns an orphan grandchild inheriting stdout (sleep 30) and exits 0 immediately.
    orphan_pid_file = work_dir / "orphan_stdout.pid"
    script_holding_stdout = (
        "import os, subprocess, sys\n"
        "from pathlib import Path\n"
        "child = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(30)'])\n"
        f"Path({str(orphan_pid_file)!r}).write_text(str(child.pid), encoding='utf-8')\n"
        "print('COLABONE_PROGRESS:{\"progress\":0.5,\"message\":\"half\"}', flush=True)\n"
        "os._exit(0)\n"
    )
    task1 = ColabOneTask("comfyui_run", "orphan-pipe-drain")
    t0 = time.monotonic()
    mgr._run_streaming_worker(
        [sys.executable, "-c", script_holding_stdout],
        task1,
        work_dir=work_dir,
        log_path=work_dir / "orphan1.log",
        env=dict(os.environ),
        progress_range=(0.30, 0.70),
        timeout=25,
        stage_label="Orphan Pipe Test",
    )
    elapsed = time.monotonic() - t0
    assert elapsed < 10.0, f"Expected ~2s pipe-drain deadline, took {elapsed:.1f}s"
    orphan_pid = int(orphan_pid_file.read_text(encoding="utf-8").strip())
    # Give OS a tiny moment if needed; process group cleanup must have terminated orphan_pid.
    deadline = time.monotonic() + 2.0
    alive = True
    while time.monotonic() < deadline:
        try:
            os.kill(orphan_pid, 0)
        except ProcessLookupError:
            alive = False
            break
        time.sleep(0.05)
    assert alive is False, f"Orphan grandchild PID {orphan_pid} survived process-group cleanup!"

    # Case 2: Worker spawns a silent background grandchild (not holding stdout) and exits 0 cleanly.
    silent_pid_file = work_dir / "orphan_silent.pid"
    script_silent_orphan = (
        "import os, subprocess, sys\n"
        "from pathlib import Path\n"
        "child = subprocess.Popen(\n"
        "    [sys.executable, '-c', 'import time; time.sleep(30)'],\n"
        "    stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,\n"
        ")\n"
        f"Path({str(silent_pid_file)!r}).write_text(str(child.pid), encoding='utf-8')\n"
        "print('COLABONE_PROGRESS:{\"progress\":1.0,\"message\":\"done\"}', flush=True)\n"
    )
    task2 = ColabOneTask("comfyui_run", "orphan-silent")
    mgr._run_streaming_worker(
        [sys.executable, "-c", script_silent_orphan],
        task2,
        work_dir=work_dir,
        log_path=work_dir / "orphan2.log",
        env=dict(os.environ),
        progress_range=(0.30, 0.70),
        timeout=15,
        stage_label="Silent Orphan Test",
    )
    silent_pid = int(silent_pid_file.read_text(encoding="utf-8").strip())
    deadline = time.monotonic() + 2.0
    alive = True
    while time.monotonic() < deadline:
        try:
            os.kill(silent_pid, 0)
        except ProcessLookupError:
            alive = False
            break
        time.sleep(0.05)
    assert alive is False, f"Silent grandchild PID {silent_pid} survived unconditional pgrp cleanup!"


def test_r2_avatar_manager_shard_index_incomplete_cache_drivefs_delta_and_compile_cache_cfg(
    tmp_path, monkeypatch
):
    """Avatar engine manager:
    - Sharded `*.safetensors.index.json` missing/empty/traversal shards fail closed.
    - `.incomplete` under `.cache/huggingface/download/` fails closed.
    - DriveFS partial-cache delta routing keeps `cinema_50` -> `balanced_8` and
      `seedvr2_7b_sharp` -> `seedvr2_7b` on DriveFS without ENOSPC.
    - `run_longcat16` writes sanitized local NVMe `compile_cache_dir` into config JSON."""
    from server.avatar_engines import _weights_files_satisfy_patterns

    # 1. Sharded index missing-shard detection & .cache/huggingface/download/*.incomplete fail closed.
    wdir = tmp_path / "sharded_weights"
    (wdir / "base_model").mkdir(parents=True, exist_ok=True)
    idx_path = wdir / "base_model" / "model.safetensors.index.json"
    idx_path.write_text(
        json.dumps(
            {
                "metadata": {},
                "weight_map": {
                    "layer.0": "model-00001-of-00002.safetensors",
                    "layer.1": "model-00002-of-00002.safetensors",
                },
            }
        ),
        encoding="utf-8",
    )
    (wdir / "base_model" / "model-00001-of-00002.safetensors").write_bytes(b"shard1")
    # Missing shard 2 -> False.
    assert _weights_files_satisfy_patterns(wdir, ["base_model/**"]) is False

    # Empty shard 2 -> False.
    shard2 = wdir / "base_model" / "model-00002-of-00002.safetensors"
    shard2.write_bytes(b"")
    assert _weights_files_satisfy_patterns(wdir, ["base_model/**"]) is False

    # Non-empty shard 2 -> True.
    shard2.write_bytes(b"shard2")
    assert _weights_files_satisfy_patterns(wdir, ["base_model/**"]) is True

    # Dropping a .incomplete file under .cache/huggingface/download/ fails closed!
    dl_cache = wdir / ".cache" / "huggingface" / "download"
    dl_cache.mkdir(parents=True, exist_ok=True)
    inc = dl_cache / "model-00002-of-00002.safetensors.incomplete"
    inc.write_bytes(b"partial")
    assert _weights_files_satisfy_patterns(wdir, ["base_model/**"]) is False
    inc.unlink()
    assert _weights_files_satisfy_patterns(wdir, ["base_model/**"]) is True

    # Traversal entry in shard index fails closed.
    idx_path.write_text(
        json.dumps({"weight_map": {"x": "../escape.safetensors"}}),
        encoding="utf-8",
    )
    assert _weights_files_satisfy_patterns(wdir, ["base_model/**"]) is False

    # 2. DriveFS partial-cache delta routing (cinema_50 -> balanced_8 and seedvr2_7b_sharp -> seedvr2_7b).
    drive_ws = tmp_path / "gdrive" / "ColabOne_Drive" / "workspace"
    drive_ws.mkdir(parents=True, exist_ok=True)
    fake_drive = Mock()
    fake_drive.mode = "colab_drivefs"
    fake_drive.is_colab_drivefs_mounted.return_value = True
    fake_drive._drivefs_workspace_dir.return_value = drive_ws
    fake_drive.mount_root = tmp_path / "gdrive"
    for attr in ("persistent_cache_root", "persistent_cache_dir", "avatar_cache_dir", "cache_root", "cache_dir"):
        setattr(fake_drive, attr, None)

    local_root = tmp_path / "local_nvme"
    mgr = ColabOneAvatarEngineManager(
        local_root,
        drive=fake_drive,
        gpu_info_fn=lambda: {"name": "Blackwell", "total_mib": 98304, "used_mib": 0},
    )
    drive_weights = drive_ws / "avatar_native" / "weights"

    # Populate valid cinema_50 (non_distill_cfg) avatar + base + seedvr2_7b_sharp on DriveFS.
    _populate_concrete_weight_files(
        drive_weights / "longcat16_avatar",
        repo_id=COLABONE_LONGCAT16_AVATAR_HF_REPO,
        revision=COLABONE_LONGCAT16_AVATAR_HF_REV,
        patterns=longcat16_avatar_allow_patterns("non_distill_cfg", "auto"),
        concrete_relpaths=[
            "base_model/model.safetensors",
            "whisper-large-v3/model.safetensors",
            "whisper-large-v3/config.json",
            "scheduler/scheduler_config.json",
            "vocal_separator/Kim_Vocal_2.onnx",
        ],
    )
    _populate_concrete_weight_files(
        drive_weights / "longcat16_base",
        repo_id=COLABONE_LONGCAT16_BASE_HF_REPO,
        revision=COLABONE_LONGCAT16_BASE_HF_REV,
        patterns=longcat16_base_allow_patterns(),
        concrete_relpaths=[
            "tokenizer/tokenizer_config.json",
            "text_encoder/model.safetensors",
            "vae/model.safetensors",
        ],
    )
    _populate_concrete_weight_files(
        drive_weights / "seedvr2",
        repo_id=COLABONE_SEEDVR2_HF_REPO,
        revision=COLABONE_SEEDVR2_HF_REV,
        patterns=vsr_engine_allow_patterns("seedvr2_7b_sharp")[2],
        concrete_relpaths=[
            "seedvr2_ema_7b_sharp_fp16.safetensors",
            "ema_vae_fp16.safetensors",
        ],
    )

    # Mock distinct filesystems: DriveFS has 10 GiB free (fits 5 GiB LoRA delta, NOT 39 GiB full),
    # local NVMe has 10 GiB free (fits 6 GiB headroom, NOT 39 GiB fallback).
    disk_budget = {"drive_gib": 10, "local_gib": 10}

    def _mock_disk_usage(path: Path) -> Any:
        if Path(path).resolve().is_relative_to((tmp_path / "gdrive").resolve()):
            free = int(disk_budget["drive_gib"] * 1024**3)
            return _DiskUsage(2000 * 1024**3, 2000 * 1024**3 - free, free)
        free = int(disk_budget["local_gib"] * 1024**3)
        return _DiskUsage(100 * 1024**3, 100 * 1024**3 - free, free)

    monkeypatch.setattr("shutil.disk_usage", _mock_disk_usage)

    # Case A: cinema_50 -> balanced_8 (non_distill_cfg -> distill_lora_mix) stays on DriveFS!
    pf_lora = mgr.check_preflight(
        min_vram_mib=49152,
        min_disk_mib=79872,
        upscale_mode="seedvr2_7b_sharp",
        sampling_mode="distill_lora_mix",
        vocal_separation="auto",
    )
    assert pf_lora["drive_required_disk_mib"] == 5000
    assert pf_lora["required_disk_mib"] == 6144
    resolved_avatar = mgr._resolve_weights_dir(
        "longcat16_avatar",
        repo_id=COLABONE_LONGCAT16_AVATAR_HF_REPO,
        revision=COLABONE_LONGCAT16_AVATAR_HF_REV,
        required_patterns=longcat16_avatar_allow_patterns("distill_lora_mix", "auto"),
        min_free_bytes=39000 * 1024 * 1024,
    )
    assert resolved_avatar == drive_weights / "longcat16_avatar"

    # Case B: seedvr2_7b_sharp -> seedvr2_7b (16000 MiB DiT delta < 16600 MiB cold size, VAE cached).
    disk_budget["drive_gib"] = 16.0  # 16384 MiB >= 16000 MiB delta, < 16600 MiB cold!
    disk_budget["local_gib"] = 8.0   # Enough for 6 GiB headroom, NOT 16 GiB local fallback!
    pf_vsr = mgr.check_preflight(
        min_vram_mib=49152,
        min_disk_mib=79872,
        upscale_mode="seedvr2_7b",
        sampling_mode="non_distill_cfg",
        vocal_separation="auto",
    )
    assert pf_vsr["drive_required_disk_mib"] == 16000
    assert pf_vsr["required_disk_mib"] == 6144
    resolved_seedvr2 = mgr._resolve_weights_dir(
        "seedvr2",
        repo_id=COLABONE_SEEDVR2_HF_REPO,
        revision=COLABONE_SEEDVR2_HF_REV,
        required_patterns=vsr_engine_allow_patterns("seedvr2_7b")[2],
        min_free_bytes=16600 * 1024 * 1024,
    )
    assert resolved_seedvr2 == drive_weights / "seedvr2"

    # 3. run_longcat16 writes sanitized local NVMe compile_cache_dir into JSON config!
    captured_env: dict[str, str] = {}
    work_dir = tmp_path / "stage2_work"
    work_dir.mkdir(parents=True, exist_ok=True)
    out_vid = work_dir / "stage2.mkv"

    def _fake_worker(cmd: list[str], *, env: dict[str, str] | None = None, **kw: Any) -> str:
        captured_env.update(env or {})
        out_vid.write_bytes(b"MKV")
        cfg_file = Path(cmd[cmd.index("--config") + 1])
        written = json.loads(cfg_file.read_text(encoding="utf-8"))
        Path(written["output_manifest_path"]).write_text(
            json.dumps({"ok": True, "output_video_path": str(out_vid)}),
            encoding="utf-8",
        )
        return ""

    mgr_mock = ColabOneAvatarEngineManager(
        local_root,
        drive=fake_drive,
        run_cmd_fn=_fake_worker,
    )
    drive_compile_bad = drive_ws / "avatar_native" / "inductor_cache"
    task = ColabOneTask("comfyui_run", "sanitize-compile-cfg")
    mgr_mock.run_longcat16(
        task,
        config={
            "output_video_path": str(out_vid),
            "compile_cache_dir": str(drive_compile_bad),
            "exact_frames": 25,
        },
        runtime_paths={
            "python": Path(sys.executable),
            "repo_dir": tmp_path / "repo",
            "avatar_dir": drive_weights / "longcat16_avatar",
            "base_dir": drive_weights / "longcat16_base",
            "compile_cache_dir": drive_compile_bad,
        },
        work_dir=work_dir,
    )
    cfg_on_disk = json.loads((work_dir / "stage2_longcat16_config.json").read_text(encoding="utf-8"))
    assert Path(cfg_on_disk["compile_cache_dir"]) == mgr_mock.inductor_cache_dir
    assert Path(captured_env["TORCHINDUCTOR_CACHE_DIR"]) == mgr_mock.inductor_cache_dir
    assert Path(captured_env["TRITON_CACHE_DIR"]) == mgr_mock.inductor_cache_dir / "triton"


def test_r2_tts_engines_cached_ids_voxcpm2_bootstrap_disk_and_hf_token_regex(
    tmp_path, monkeypatch
):
    """tts_engines.py:
    - cached_engine_ids() detects verified local caches and feeds comfy.status()['cached_tts_engines'].
    - check_disk includes 6144 MiB uncached voxcpm2 preset bootstrap budget for ref-audio preset engines.
    - _HF_TOKEN_RE preserves `hf_download_longcat16_avatar.log` while redacting real tokens."""
    from server.runtime import ColabOneRuntime
    from server.tts_engines import (
        ColabOneRemoteTtsRunner,
        _HF_TOKEN_RE,
        _VOXCPM2_BOOTSTRAP_DISK_MIB,
        _scrub_secret,
    )

    assert _VOXCPM2_BOOTSTRAP_DISK_MIB == 6144

    # 1. _HF_TOKEN_RE preserves benign hf_download_* / hf_cache_* filenames while scrubbing tokens.
    benign = "Log saved at /tmp/hf_download_longcat16_avatar.log and /root/hf_cache_dir/hf_hub_v1"
    assert _HF_TOKEN_RE.search(benign) is None
    assert _scrub_secret(benign, "") == benign
    secret_text = "Authorization failed for hf_AbCdEfGhIjKlMnOpQrStUvWx1234 in hf_download_longcat16_avatar.log"
    scrubbed = _scrub_secret(secret_text, "")
    assert "hf_AbCdEfGhIjKlMnOpQrStUvWx1234" not in scrubbed
    assert "hf_download_longcat16_avatar.log" in scrubbed

    # 2. cached_engine_ids() + comfy.status()["cached_tts_engines"] using real catalog.
    real_rt = ColabOneRuntime(tmp_path / "real_rt", ROOT / "catalog")
    tts = real_rt.comfy.tts
    assert tts.cached_engine_ids() == []
    assert real_rt.comfy.status()["cached_tts_engines"] == []

    moss_spec = tts.get_engine("moss_tts_v1_5")
    hf_cache = tts.engines_dir / "_hf_cache"
    main_snap = hf_cache / ("models--" + moss_spec["repo"].replace("/", "--")) / "snapshots" / moss_spec["revision"]
    main_snap.mkdir(parents=True, exist_ok=True)
    (main_snap / "model.safetensors").write_bytes(b"moss-main")
    # Missing aux_repo -> NOT cached yet!
    assert tts.cached_engine_ids() == []

    aux_snap = hf_cache / ("models--" + moss_spec["aux_repo"].replace("/", "--")) / "snapshots" / moss_spec["aux_revision"]
    aux_snap.mkdir(parents=True, exist_ok=True)
    (aux_snap / "tokenizer.safetensors").write_bytes(b"moss-aux")
    assert tts.cached_engine_ids() == ["moss_tts_v1_5"]
    assert real_rt.comfy.status()["cached_tts_engines"] == ["moss_tts_v1_5"]

    # 3. check_disk 6144 MiB uncached voxcpm2 preset bootstrap budget.
    engine_spec = {
        "id": "moss_tts_v1_5",
        "name": "MOSS-TTS",
        "repo": "OpenMOSS-Team/MOSS-TTS-Test",
        "revision": "a" * 40,
        "min_disk_mib": 10000,
    }
    tts_isolated = ColabOneRemoteTtsRunner(real_rt, {"moss_tts_v1_5": engine_spec})
    # Free disk = 10000 + 4096 MiB (< 10000 + 6144 MiB): fails for built-in voice preset!
    free_mib = {"val": 10000 + 4096}
    monkeypatch.setattr(
        "shutil.disk_usage",
        lambda _p: _DiskUsage(100 * 1024**3, 0, free_mib["val"] * 1024 * 1024),
    )
    with pytest.raises(RuntimeError, match="VoxCPM2"):
        tts_isolated.check_disk(engine_spec, voice_preset="shen_zhiyi")

    # Providing ref_audio_path skips bootstrap budget -> passes at 14096 MiB!
    ref_wav = _write_pcm16_wav(tmp_path / "ref.wav", duration_sec=1.0, sample_rate=16000)
    tts_isolated.check_disk(engine_spec, ref_audio_path=ref_wav, voice_preset="shen_zhiyi")

    # Increasing free disk to 10000 + 6144 MiB passes for built-in voice preset!
    free_mib["val"] = 10000 + 6144
    tts_isolated.check_disk(engine_spec, voice_preset="shen_zhiyi")


def test_r2_stage4_vbv_ceiling_audio_stepdown_second_pass_and_mkv_retention(
    runtime, tmp_path, monkeypatch
):
    """Stage 4 >512 MiB auto-reencode:
    - Retains Stage 2/3 lossless MKV across pass 1 and pass 2 until MP4 is verified <= 512 MiB.
    - Runs 2nd stricter pass if pass 1 still exceeds 512 MiB.
    - Steps down audio bitrate (256 -> 192 -> 128 -> 96 kbps) and respects physical VBV ceiling.
    - Fails cleanly when duration makes video bitrate < 80 kbps or 2nd pass still > 512 MiB."""
    from common.comfyui import MAX_OUTPUT_BYTES

    catalog = _load_real_comfy_catalog()
    longcat_wf = next(t for t in catalog.templates if t.id == "digital-human-longcat").model_dump()
    portrait = tmp_path / "anchor.png"
    portrait.write_bytes(_make_png_bytes("navy", size=(96, 96)))
    audio_wav = _write_pcm16_wav(tmp_path / "voice.wav", duration_sec=4.0, sample_rate=48000)

    def _make_runner(ff_handler):
        state = {"stage2_mkv": None}

        class _MinimalEngines:
            def check_preflight(self, **kw):
                return {"name": "Blackwell", "total_mib": 98304, "is_high_vram_96g": True, "all_weights_cached": True}

            def ensure_longcat16_ready(self, task, **kw):
                return {"python": tmp_path / "py"}

            def run_longcat16(self, task, *, config, runtime_paths, work_dir, progress_range):
                out_p = Path(config["output_video_path"])
                out_p.write_bytes(b"LOSSLESS-STAGE2-MKV")
                state["stage2_mkv"] = out_p
                return {"ok": True, "output_video_path": str(out_p), "width": 1280, "height": 768}

        runner = ColabOneDigitalHumanRunner(
            runtime.comfy,
            None,
            run_cmd_fn=lambda args, **kw: ff_handler(args, state),
            avatar_engines=_MinimalEngines(),
        )
        return runner, state

    req = WorkflowRunRequest(
        workflow_id="digital-human-longcat",
        overrides={
            "pipeline.audio_source": "audio",
            "pipeline.upscale_mode": "none",
            "pipeline.target_resolution": "1280x720",
            "pipeline.crf": 14,
        },
        inputs={"1.image": "a" * 32, "2.audio": "b" * 32},
    )

    # Case 1: 2nd pass succeeds, MKV retained through pass 1 & pass 2, unlinked after <= 512 MiB.
    calls_c1: list[list[str]] = []
    mkv_present_at_pass: list[bool] = []

    def _ff_two_pass(args, state):
        cmd = [str(x) for x in args]
        calls_c1.append(cmd)
        mkv_present_at_pass.append(bool(state["stage2_mkv"] and state["stage2_mkv"].is_file()))
        out = Path(cmd[-1])
        if len(calls_c1) in (1, 2):
            with out.open("wb") as fh:
                fh.truncate(MAX_OUTPUT_BYTES + 4 * 1024 * 1024)
        else:
            out.write_bytes(b"\x00\x00\x00\x18ftypmp42pass2-ok")
        return ""

    runner1, _ = _make_runner(_ff_two_pass)
    outputs = runner1.run(
        ColabOneTask("comfyui_run", "stage4-2pass"),
        longcat_wf,
        req,
        [("1.image", "anchor.png", portrait), ("2.audio", "voice.wav", audio_wav)],
    )
    assert len(outputs) == 1 and outputs[0]["size"] <= MAX_OUTPUT_BYTES
    assert len(calls_c1) == 3
    assert mkv_present_at_pass == [True, True, True]
    pass2_cmd = calls_c1[2]
    assert pass2_cmd[pass2_cmd.index("-b:a") + 1] == "128k"
    assert pass2_cmd[pass2_cmd.index("-b:v") + 1] == pass2_cmd[pass2_cmd.index("-maxrate") + 1]
    assert pass2_cmd[pass2_cmd.index("-b:v") + 1] == pass2_cmd[pass2_cmd.index("-bufsize") + 1]

    # Case 2: Long duration (6000s) triggers audio step-down (256 -> 192 -> 128k) and physical VBV ceiling.
    calls_c2: list[list[str]] = []

    def _ff_long_dur(args, state):
        cmd = [str(x) for x in args]
        calls_c2.append(cmd)
        out = Path(cmd[-1])
        if len(calls_c2) == 1:
            with out.open("wb") as fh:
                fh.truncate(MAX_OUTPUT_BYTES + 1024 * 1024)
        else:
            out.write_bytes(b"\x00\x00\x00\x18ftypmp42stepdown")
        return ""

    runner2, _ = _make_runner(_ff_long_dur)
    monkeypatch.setattr(
        runner2,
        "_prepare_uploaded_audio",
        lambda task, src, work_dir, **kw: (audio_wav, 6000.0, []),
    )
    runner2.run(
        ColabOneTask("comfyui_run", "stage4-stepdown"),
        longcat_wf,
        req,
        [("1.image", "anchor.png", portrait), ("2.audio", "voice.wav", audio_wav)],
    )
    assert len(calls_c2) == 2
    reenc = calls_c2[1]
    assert reenc[reenc.index("-b:a") + 1] == "128k"
    audio_bytes_6000 = int(math.ceil((128 * 1000 / 8.0) * 6000.0))
    hard_ceiling = int(((MAX_OUTPUT_BYTES * 0.96 - audio_bytes_6000) * 8) / 6000.0 / 1000)
    maxrate_val = int(reenc[reenc.index("-maxrate") + 1].rstrip("k"))
    bv_val = int(reenc[reenc.index("-b:v") + 1].rstrip("k"))
    assert 80 <= bv_val <= maxrate_val <= hard_ceiling

    # Case 3: Impossibly long duration (40000s -> hard_ceiling < 80 kbps) fails fast without re-encoding.
    calls_c3: list[list[str]] = []

    def _ff_huge_dur(args, state):
        cmd = [str(x) for x in args]
        calls_c3.append(cmd)
        with Path(cmd[-1]).open("wb") as fh:
            fh.truncate(MAX_OUTPUT_BYTES + 1024 * 1024)
        return ""

    runner3, _ = _make_runner(_ff_huge_dur)
    monkeypatch.setattr(
        runner3,
        "_prepare_uploaded_audio",
        lambda task, src, work_dir, **kw: (audio_wav, 40000.0, []),
    )
    with pytest.raises(RuntimeError, match="无法在 512 MiB 上限内保证可用画质"):
        runner3.run(
            ColabOneTask("comfyui_run", "stage4-too-long"),
            longcat_wf,
            req,
            [("1.image", "anchor.png", portrait), ("2.audio", "voice.wav", audio_wav)],
        )
    assert len(calls_c3) == 1

    # Case 4: Still > 512 MiB after 2nd pass raises clear RuntimeError.
    calls_c4: list[list[str]] = []

    def _ff_always_big(args, state):
        cmd = [str(x) for x in args]
        calls_c4.append(cmd)
        with Path(cmd[-1]).open("wb") as fh:
            fh.truncate(MAX_OUTPUT_BYTES + 2 * 1024 * 1024)
        return ""

    runner4, _ = _make_runner(_ff_always_big)
    with pytest.raises(RuntimeError, match="二次自动重编码.*512 MiB"):
        runner4.run(
            ColabOneTask("comfyui_run", "stage4-still-big"),
            longcat_wf,
            req,
            [("1.image", "anchor.png", portrait), ("2.audio", "voice.wav", audio_wav)],
        )
    assert len(calls_c4) == 3


def test_r2_comfy_active_pack_switching_and_clearing_on_stop_and_start(runtime, monkeypatch):
    """status()['active_pack'] switches cleanly from InfiniteTalk to native LongCat 1.6
    and clears _native_active_pack_id on stop_server() and start_server()."""
    comfy = runtime.comfy
    comfy.packs.active_pack_id = "digital-human-infinitetalk"
    assert comfy.status()["active_pack"] == "digital-human-infinitetalk"

    # Switching to native LongCat 1.6 takes precedence in status()["active_pack"].
    comfy.mark_native_pack_ready("digital-human-longcat")
    assert comfy.status()["active_pack"] == "digital-human-longcat"

    # stop_server() clears _native_active_pack_id.
    comfy.stop_server()
    assert comfy._native_active_pack_id == ""
    assert comfy.status()["active_pack"] == "digital-human-infinitetalk"

    # Mark native active again, then simulate healthy running ComfyUI start_server() clearing it.
    comfy.mark_native_pack_ready("digital-human-longcat")
    assert comfy.status()["active_pack"] == "digital-human-longcat"

    fake_proc = Mock()
    fake_proc.poll.return_value = None
    fake_proc.pid = None
    comfy.proc = fake_proc
    comfy.state = "ready"
    resp_ok = Mock(status_code=200)
    monkeypatch.setattr(runtime, "http", Mock(get=Mock(return_value=resp_ok)))

    comfy.start_server(ColabOneTask("comfyui_start", "clear-native-pack"))
    assert comfy._native_active_pack_id == ""
    assert comfy.status()["active_pack"] == "digital-human-infinitetalk"


def test_r2_image_16bit_trns_wav_truncation_and_contained_worker_file(runtime, tmp_path):
    """Covers:
    - 16-bit grayscale PNG (I;16) + tRNS preserves mid-gray tones (NOT solid white) and composites tRNS.
    - Truncated & header-only 48 kHz stereo WAV rejected by _is_readable_pcm_wav.
    - _resolve_contained_worker_file accepts relative path inside work_dir and avoids '路径路径'."""
    import zlib
    from server.digital_human import (
        _is_readable_pcm_wav,
        _resolve_contained_worker_file,
    )

    # 1. 16-bit grayscale PNG (I;16) + tRNS chunk.
    im16 = Image.new("I;16", (64, 64), 32768)  # mid-gray (32768 / 256 = 128)
    for y in range(0, 16):
        for x in range(0, 16):
            im16.putpixel((x, y), 1000)  # transparent via tRNS=1000
    for y in range(48, 64):
        for x in range(48, 64):
            im16.putpixel((x, y), 8192)  # dark gray (8192 / 256 = 32)
    buf = io.BytesIO()
    im16.save(buf, format="PNG")
    raw_png = buf.getvalue()
    idat_pos = raw_png.index(b"IDAT") - 4
    trns_payload = struct.pack(">H", 1000)
    trns_chunk = (
        struct.pack(">I", len(trns_payload))
        + b"tRNS"
        + trns_payload
        + struct.pack(">I", zlib.crc32(b"tRNS" + trns_payload) & 0xFFFFFFFF)
    )
    src_16 = tmp_path / "gray16_trns.png"
    src_16.write_bytes(raw_png[:idat_pos] + trns_chunk + raw_png[idat_pos:])

    dst_rgb = tmp_path / "gray16_prepared.png"
    prepare_anchor_image(src_16, dst_rgb, 64, 64)
    with Image.open(dst_rgb) as out_img:
        assert out_img.mode == "RGB"
        # Mid-gray must stay ~128 (NOT solid white 255).
        r_mid, g_mid, b_mid = out_img.getpixel((32, 32))
        assert 122 <= r_mid <= 134 and r_mid == g_mid == b_mid
        # Dark corner must stay ~32.
        r_dark, g_dark, b_dark = out_img.getpixel((56, 56))
        assert 26 <= r_dark <= 38 and r_dark == g_dark == b_dark
        # tRNS pixel composited onto (245, 245, 245) studio backdrop.
        assert out_img.getpixel((4, 4)) == (245, 245, 245)

    # 2. Truncated / header-only 48 kHz stereo WAV rejected by _is_readable_pcm_wav.
    valid_wav = _write_stereo_pcm16_wav(tmp_path / "valid_48k.wav", duration_sec=1.0, sample_rate=48000)
    assert _is_readable_pcm_wav(valid_wav) is True
    raw_wav = valid_wav.read_bytes()
    header_only = tmp_path / "header_only_48k.wav"
    header_only.write_bytes(raw_wav[:44])
    truncated = tmp_path / "truncated_48k.wav"
    truncated.write_bytes(raw_wav[:2048])
    assert _is_readable_pcm_wav(header_only) is False
    assert _is_readable_pcm_wav(truncated) is False

    # _prepare_uploaded_audio must NOT pass truncated 48k stereo WAV through untouched (calls ffmpeg).
    ff_called: list[list[str]] = []

    def _fake_ff(args, **kw):
        cmd = [str(x) for x in args]
        ff_called.append(cmd)
        _write_stereo_pcm16_wav(Path(cmd[-1]), duration_sec=1.0, sample_rate=48000)
        return ""

    runner = ColabOneDigitalHumanRunner(runtime.comfy, None, run_cmd_fn=_fake_ff)
    runner._prepare_uploaded_audio(
        ColabOneTask("comfyui_run", "trunc-wav"),
        truncated,
        tmp_path / "work_trunc",
        preserve_stereo_dynamics=True,
        detect_silence=False,
    )
    assert len(ff_called) == 1

    # 3. _resolve_contained_worker_file relative path & no duplicated "路径路径".
    wdir = tmp_path / "worker_containment"
    sub = wdir / "sub"
    sub.mkdir(parents=True, exist_ok=True)
    target = sub / "stage2.mkv"
    target.write_bytes(b"ok-mkv")

    resolved = _resolve_contained_worker_file(
        wdir,
        Path("sub/stage2.mkv"),
        label="Stage 2 输出视频路径",
        missing_message="missing",
    )
    assert resolved == target.resolve()

    with pytest.raises(RuntimeError) as exc_escape:
        _resolve_contained_worker_file(
            wdir,
            Path("../escape.mkv"),
            label="Stage 2 输出视频路径",
            missing_message="missing",
        )
    assert "路径路径" not in str(exc_escape.value)
    assert "Stage 2 输出视频路径" in str(exc_escape.value)

    sym = wdir / "sym.mkv"
    sym.symlink_to(target)
    with pytest.raises(RuntimeError) as exc_sym:
        _resolve_contained_worker_file(
            wdir,
            Path("sym.mkv"),
            label="Stage 2 输出视频路径",
            missing_message="missing",
        )
    assert "路径路径" not in str(exc_sym.value)


def test_r2_security_hide_input_in_errors_oauth_redaction_ssrf_and_sse_stopping(
    runtime, monkeypatch
):
    """Security & runtime fixes:
    - Pydantic hide_input_in_errors=True across all 5 secret-bearing models (zero tail leak on >512-char tokens).
    - drive.py:format_rclone_error OAuth redaction.
    - tunnel.py:public_addresses rejecting ::ffff:100.64.0.1 and 64:ff9b::127.0.0.1.
    - Async SSE /api/v1/tasks/{id}/events exiting cleanly on runtime.stopping.set()."""
    import asyncio
    import socket
    from pydantic import ValidationError
    from common.comfyui import ComfyCanvasConfig, ComfyTtsPreviewRequest
    from common.public_api import ColabOnePublicConfig
    from server.app import ColabOneDownloadRequest, create_colabone_app
    from server.drive import format_rclone_error
    from server.tunnel import public_addresses

    # 1. All 5 secret-bearing Pydantic models hide input in errors (zero head/tail leak on >512 chars).
    head_marker = "HEADSECRET98765"
    tail_marker = "TAILSECRET54321"
    long_hf = f"hf_{head_marker}" + ("A" * 540) + tail_marker
    long_tunnel = f"eyJ{head_marker}" + ("B" * 8050) + tail_marker
    long_api_key = f"co_{head_marker}" + ("C" * 540) + tail_marker

    cases = [
        (WorkflowRunRequest, {"workflow_id": "wf-1", "hf_token": long_hf}),
        (ComfyTtsPreviewRequest, {"engine_id": "voxcpm2", "hf_token": long_hf}),
        (ColabOneDownloadRequest, {"hf_token": long_hf}),
        (ComfyCanvasConfig, {"domain": "https://canvas.example.com", "tunnel_token": long_tunnel}),
        (
            ColabOnePublicConfig,
            {
                "domain": "https://api.example.com",
                "tunnel_token": long_tunnel,
                "api_key": long_api_key,
            },
        ),
    ]
    for model_cls, payload in cases:
        assert model_cls.model_config.get("hide_input_in_errors") is True, (
            f"{model_cls.__name__} must set hide_input_in_errors=True"
        )
        with pytest.raises(ValidationError) as exc_info:
            model_cls.model_validate(payload)
        err_blob = (
            str(exc_info.value)
            + "\n"
            + repr(exc_info.value)
            + "\n"
            + json.dumps(exc_info.value.errors(include_input=False), default=str)
        )
        assert "input_value=" not in str(exc_info.value)
        assert head_marker not in err_blob, f"{model_cls.__name__} leaked secret head!"
        assert tail_marker not in err_blob, f"{model_cls.__name__} leaked secret tail!"

    # 2. drive.py:format_rclone_error OAuth redaction.
    oauth_stderrs = [
        "2026/05/01 12:00:00 ERROR : HTTP 401 Authorization: Bearer ya29.a0SecretBearerTokenXYZ123",
        'CRITICAL: failed with access_token="ya29.quotedAccessSecret999" and client_secret="GOCSPX-QuotedSecret888"',
        "Failed to refresh token: refresh_token=1//0gPlainRefreshSecret777 client_secret=GOCSPX-PlainSecret666",
        "ERROR : raw leak ya29.BareToken555 1//0gBareRefresh444 GOCSPX-BareClient333",
    ]
    for raw_err in oauth_stderrs:
        formatted = format_rclone_error(raw_err)
        assert "***" in formatted
        for leaked in (
            "a0SecretBearerTokenXYZ123",
            "quotedAccessSecret999",
            "QuotedSecret888",
            "0gPlainRefreshSecret777",
            "PlainSecret666",
            "BareToken555",
            "0gBareRefresh444",
            "BareClient333",
        ):
            assert leaked not in formatted

    # 3. tunnel.py:public_addresses rejects IPv4-mapped CGNAT, NAT64, 6to4 private, Teredo.
    for bad_ip in (
        "::ffff:100.64.0.1",
        "64:ff9b::127.0.0.1",
        "64:ff9b:1::10.0.0.1",
        "2002:7f00:0001::1",
        "2001:0000:4136:e378:8000:63bf:3fff:fdd2",
    ):
        monkeypatch.setattr(
            socket,
            "getaddrinfo",
            lambda host, port, type=0, _ip=bad_ip: [(socket.AF_INET6, socket.SOCK_STREAM, 6, "", (_ip, 443, 0, 0))],
        )
        with pytest.raises(ValueError, match="公网地址"):
            public_addresses("https://api.example.com")

    monkeypatch.setattr(
        socket,
        "getaddrinfo",
        lambda host, port, type=0: [(socket.AF_INET, socket.SOCK_STREAM, 6, "", ("93.184.216.34", 443))],
    )
    public_addresses("https://api.example.com")

    # 4. Async SSE /api/v1/tasks/{id}/events exits cleanly when runtime.stopping.set() fires.
    task = ColabOneTask("comfyui_run", "sse-stop-test")
    task.emit("log", {"message": "running"})
    runtime.tasks[task.id] = task
    token = "colabone-sse-stopping-test-token-0123456789"
    app = create_colabone_app(runtime, token, background=False)

    async def _consume_sse_until_stopping() -> list[str]:
        received: list[str] = []
        status_holder = {"status": 0}
        never_disconnect = asyncio.Event()
        scope = {
            "type": "http",
            "asgi": {"version": "3.0"},
            "http_version": "1.1",
            "method": "GET",
            "scheme": "http",
            "path": f"/api/v1/tasks/{task.id}/events",
            "raw_path": f"/api/v1/tasks/{task.id}/events".encode(),
            "query_string": b"",
            "headers": [(b"authorization", f"Bearer {token}".encode())],
            "client": ("127.0.0.1", 12345),
            "server": ("127.0.0.1", 7860),
        }

        async def _receive():
            # Never disconnect the client; exit MUST come strictly from runtime.stopping.set().
            await never_disconnect.wait()
            return {"type": "http.disconnect"}

        async def _send(message: dict[str, Any]) -> None:
            if message["type"] == "http.response.start":
                status_holder["status"] = int(message["status"])
            elif message["type"] == "http.response.body":
                body = message.get("body", b"")
                if body:
                    text = body.decode("utf-8", errors="replace")
                    received.append(text)
                    if "running" in text and not runtime.stopping.is_set():
                        runtime.stopping.set()

        await app(scope, _receive, _send)
        assert status_holder["status"] == 200
        return received

    t0 = time.monotonic()
    chunks = asyncio.run(asyncio.wait_for(_consume_sse_until_stopping(), timeout=3.0))
    elapsed = time.monotonic() - t0
    assert elapsed < 2.0, f"SSE stream hung on runtime.stopping.set(): {elapsed:.2f}s"
    assert any("running" in c for c in chunks)


def test_r2_schema_validators_and_catalog_vram_commercial_invariants():
    """common/comfyui.py new validators + catalog VRAM & commercial alignment invariants."""
    from pydantic import ValidationError
    from common.comfyui import (
        ComfyTtsEngineSpec,
        WorkflowField,
        WorkflowTemplate,
        apply_workflow_overrides,
    )

    # 1. ComfyTtsEngineSpec.source_commit required when source_repo is set.
    base_tts = {
        "id": "custom_tts",
        "name": "Custom TTS",
        "repo": "org/tts-model",
        "revision": "a" * 40,
        "source_repo": "https://github.com/org/tts-code",
    }
    with pytest.raises(ValidationError, match="source_commit"):
        ComfyTtsEngineSpec.model_validate(base_tts)
    with pytest.raises(ValidationError):
        ComfyTtsEngineSpec.model_validate({**base_tts, "source_commit": "not-40-hex"})
    valid_tts = ComfyTtsEngineSpec.model_validate({**base_tts, "source_commit": "b" * 40})
    assert valid_tts.source_commit == "b" * 40

    # 2. WorkflowField.visible_when cap (>16), value type, and 160-char cap.
    base_field = {
        "key": "pipeline.mode",
        "label": "Mode",
        "node_id": "pipeline",
        "input_name": "mode",
        "field_type": "text",
    }
    with pytest.raises(ValidationError, match="16"):
        WorkflowField.model_validate(
            {**base_field, "visible_when": {f"pipeline.k{i}": "v" for i in range(17)}}
        )
    with pytest.raises(ValidationError):
        WorkflowField.model_validate({**base_field, "visible_when": {"pipeline.a": 123}})
    with pytest.raises(ValidationError, match="160"):
        WorkflowField.model_validate({**base_field, "visible_when": {"pipeline.a": "x" * 161}})

    # 3. WorkflowTemplate rejects visible_when referencing undeclared field.
    sample_wf = {"1": {"class_type": "LoadImage", "inputs": {"image": "a.png"}}}
    with pytest.raises(ValidationError, match="未声明的字段"):
        WorkflowTemplate.model_validate(
            {
                "id": "wf-vis-check",
                "name": "Vis Check",
                "workflow": sample_wf,
                "fields": [
                    {
                        "key": "pipeline.child",
                        "label": "Child",
                        "node_id": "pipeline",
                        "input_name": "child",
                        "field_type": "text",
                        "visible_when": {"pipeline.does_not_exist": "on"},
                    }
                ],
            }
        )

    # 4. WorkflowField.default strict type validation for boolean, integer, number.
    for bad_field in (
        {**base_field, "field_type": "boolean", "default": "true"},
        {**base_field, "field_type": "boolean", "default": 1},
        {**base_field, "field_type": "integer", "default": True},
        {**base_field, "field_type": "integer", "default": 1.5},
        {**base_field, "field_type": "integer", "default": "not-an-int"},
        {**base_field, "field_type": "number", "default": True},
        {**base_field, "field_type": "number", "default": "not-a-num"},
    ):
        with pytest.raises(ValidationError):
            WorkflowField.model_validate(bad_field)

    assert WorkflowField.model_validate({**base_field, "field_type": "boolean", "default": False}).default is False
    assert WorkflowField.model_validate({**base_field, "field_type": "integer", "default": 16}).default == 16
    assert WorkflowField.model_validate({**base_field, "field_type": "number", "default": 3.5}).default == 3.5

    # 5. apply_workflow_overrides rejects bool override on non-bool input, allows bool on bool.
    wf = {"6": {"class_type": "Sampler", "inputs": {"steps": 20, "enabled": True}}}
    with pytest.raises(ValueError, match="不接受布尔值覆盖"):
        apply_workflow_overrides(wf, overrides={"6.steps": True})
    updated = apply_workflow_overrides(wf, overrides={"6.enabled": False, "6.steps": 12})
    assert updated["6"]["inputs"]["enabled"] is False
    assert updated["6"]["inputs"]["steps"] == 12

    # 6. Real catalog invariant: default_tts_engine.min_vram_mib <= template.min_vram_mib
    #    and commercial_ok alignment across every digital-human template.
    catalog = _load_real_comfy_catalog()
    engines_by_id = {e.id: e for e in catalog.tts_engines}
    templates_by_id = {t.id: t for t in catalog.templates}

    for tmpl in catalog.templates:
        if tmpl.pipeline is None or not tmpl.pipeline.default_tts_engine:
            continue
        default_eng = engines_by_id[tmpl.pipeline.default_tts_engine]
        assert default_eng.min_vram_mib <= tmpl.min_vram_mib, (
            f"{tmpl.id}: default TTS {default_eng.id} requires {default_eng.min_vram_mib} MiB "
            f"> template {tmpl.min_vram_mib} MiB"
        )
        if tmpl.commercial_ok:
            assert default_eng.commercial_ok is True, (
                f"Commercial template {tmpl.id} must not default to non-commercial TTS {default_eng.id}"
            )

    inf = templates_by_id["digital-human-infinitetalk"]
    assert inf.commercial_ok is True
    assert inf.license == "Apache-2.0 / CC-BY-4.0 / GPL-3.0"
    assert inf.pipeline is not None and inf.pipeline.default_tts_engine == "moss_tts_v1_5"
    lc = templates_by_id["digital-human-longcat"]
    assert lc.pipeline is not None and lc.pipeline.default_tts_engine == "fish_drama3"


