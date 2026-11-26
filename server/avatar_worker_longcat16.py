"""Self-contained remote GPU worker for Meituan LongCat-Video-Avatar 1.6.

Runs on the remote Colab GPU runtime as a standalone subprocess:
    python server/avatar_worker_longcat16.py --config /path/to/config.json

All planning, sampling resolution, attention backend selection, FFV1 command
construction, and progress reporting helpers at module scope are pure-Python
stdlib-only so offline unit tests can import this module without PyTorch,
Transformers, Diffusers, NumPy, Pillow, or audio-separator installed.
"""

from __future__ import annotations

import argparse
import collections
import contextlib
import datetime
import gc
import importlib.metadata
import importlib.util
import inspect
import json
import math
import os
import queue
import random
import signal
import socket
import subprocess
import sys
import tempfile
import threading
import time
import traceback
import types
from pathlib import Path
from typing import Any

__all__ = [
    "COLABONE_LONGCAT16_DEFAULT_NEGATIVE_PROMPT",
    "COLABONE_LONGCAT16_FP8_FFN_BLOCKS",
    "COLABONE_PROGRESS_PREFIX",
    "ColabOneLongCat16Terminated",
    "build_lossless_ffv1_writer_cmd",
    "emit_colabone_progress",
    "main",
    "normalize_longcat16_speakers",
    "plan_longcat16_segments",
    "resolve_longcat16_attention_flags",
    "resolve_longcat16_sampling_params",
    "run_longcat16_generation",
    "should_run_vocal_separation",
]

COLABONE_PROGRESS_PREFIX = "COLABONE_PROGRESS:"

# Official calibrated 40-block FP8 FFN set from meituan-longcat/LongCat-Video
# (leaves blocks 0..5, 7, 8, attention, and final_layer in pure BF16).
COLABONE_LONGCAT16_FP8_FFN_BLOCKS: tuple[int, ...] = (
    6,
    9,
    10,
    11,
    12,
    13,
    14,
    15,
    16,
    17,
    18,
    19,
    20,
    21,
    22,
    23,
    24,
    25,
    26,
    27,
    28,
    29,
    30,
    31,
    32,
    33,
    34,
    35,
    36,
    37,
    38,
    39,
    40,
    41,
    42,
    43,
    44,
    45,
    46,
    47,
)

# Official negative prompt from meituan-longcat/LongCat-Video avatar single script.
COLABONE_LONGCAT16_DEFAULT_NEGATIVE_PROMPT = (
    "Close-up, Bright tones, overexposed, static, blurred details, subtitles, "
    "style, works, paintings, images, overall gray, worst quality, "
    "low quality, JPEG compression residue, ugly, incomplete, extra fingers, "
    "poorly drawn hands, poorly drawn faces, deformed, disfigured, misshapen "
    "limbs, fused fingers, still picture, messy background, three legs, many "
    "people in the background, walking backwards"
)

_VALID_SAMPLING_MODES = frozenset({"non_distill_cfg", "distill_lora_mix"})
_VALID_ATTENTION_BACKENDS = frozenset(
    {"auto", "sageattn_3", "sageattn_2", "flash_attn_3", "flash_attn_2", "sdpa"}
)
_VALID_VOCAL_SEPARATION_MODES = frozenset({"auto", "always", "never"})
_VALID_RESOLUTIONS = frozenset({"480p", "720p"})
_SENSITIVE_SUBPROCESS_ENV_KEYS = frozenset(
    {
        "HF_TOKEN",
        "HUGGING_FACE_HUB_TOKEN",
        "COLABONE_TOKEN",
        "COLABONE_API_KEY",
        "OPENAI_API_KEY",
        "ANTHROPIC_API_KEY",
        "GOOGLE_API_KEY",
        "GEMINI_API_KEY",
    }
)


class ColabOneLongCat16Terminated(BaseException):
    """Raised when the worker receives SIGTERM or SIGINT for graceful teardown."""

    def __init__(self, signum: int) -> None:
        self.signum = int(signum)
        super().__init__(f"Terminated by signal {self.signum}")


def _require_int(value: Any, name: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise ValueError(f"{name} must be an integer, got {value!r}")
    return value


def _parse_strict_int(value: Any, name: str, *, default: int | None = None) -> int:
    if value is None:
        if default is not None:
            return default
        raise ValueError(f"{name} is required")
    if isinstance(value, bool):
        raise ValueError(f"{name} must be an integer, got bool {value!r}")
    if isinstance(value, int):
        return value
    if isinstance(value, str):
        stripped = value.strip()
        if stripped and (
            stripped.isdigit()
            or (stripped[0] in ("+", "-") and stripped[1:].isdigit())
        ):
            return int(stripped)
    raise ValueError(f"{name} must be an integer, got {value!r}")


def _parse_strict_float(
    value: Any, name: str, *, default: float | None = None
) -> float:
    if value is None:
        if default is not None:
            return default
        raise ValueError(f"{name} is required")
    if isinstance(value, bool):
        raise ValueError(f"{name} must be a number, got bool {value!r}")
    if isinstance(value, (int, float)):
        out = float(value)
    elif isinstance(value, str):
        stripped = value.strip()
        if not stripped:
            raise ValueError(f"{name} must be a number, got {value!r}")
        try:
            out = float(stripped)
        except ValueError as exc:
            raise ValueError(f"{name} must be a number, got {value!r}") from exc
    else:
        raise ValueError(f"{name} must be a number, got {value!r}")
    if not math.isfinite(out):
        raise ValueError(f"{name} must be a finite number, got {value!r}")
    return out


def _validate_safe_arg(value: str | Path, label: str) -> str:
    raw = os.fspath(value)
    if not raw or not str(raw).strip():
        raise ValueError(f"{label} must not be empty")
    if any(ch in raw for ch in ("\r", "\n", "\x00")):
        raise ValueError(f"{label} must not contain CR, LF, or NUL characters")
    return raw


_SYSTEM_SYMLINK_DIRS = frozenset({Path("/tmp"), Path("/var"), Path("/var/tmp")})


def _ensure_not_symlink(value: str | Path, label: str) -> Path:
    safe = _validate_safe_arg(value, label)
    path = Path(safe)
    if path.is_symlink():
        raise ValueError(f"{label} must not be a symlink: {path}")
    parent = path.parent
    if (
        parent != path
        and parent not in _SYSTEM_SYMLINK_DIRS
        and parent.is_symlink()
    ):
        raise ValueError(f"{label} parent directory must not be a symlink: {parent}")
    return path


def _validate_ffmpeg_bin(value: str | Path, label: str = "ffmpeg_bin") -> str:
    raw = _validate_safe_arg(str(value or "ffmpeg").strip() or "ffmpeg", label)
    if raw.startswith("-"):
        raise ValueError(f"{label} must not start with '-': {raw!r}")
    return raw


def _normalize_ffmpeg_path_arg(value: str | Path, label: str) -> str:
    raw = _validate_safe_arg(value, label)
    if raw.startswith("-") or raw.lstrip().startswith("-"):
        raise ValueError(f"{label} must not start with '-': {raw!r}")
    if not os.path.isabs(raw):
        raw = os.path.abspath(raw)
    return raw


def _sanitized_subprocess_env(extra: dict[str, str] | None = None) -> dict[str, str]:
    env = {
        k: v
        for k, v in os.environ.items()
        if k not in _SENSITIVE_SUBPROCESS_ENV_KEYS
    }
    if extra:
        env.update(extra)
    return env


def _clamp_int(value: int, low: int, high: int) -> int:
    return max(low, min(high, value))


def _clamp_float(value: float, low: float, high: float) -> float:
    if math.isnan(value) or math.isinf(value):
        return low
    return max(low, min(high, value))


def plan_longcat16_segments(
    exact_frames: int,
    num_frames: int = 93,
    num_cond_frames: int = 13,
) -> list[dict[str, int]]:
    """Compute the exact multi-segment continuation plan for LongCat-Video-Avatar 1.6.

    Segment 0 covers [0, num_frames) with zero overlap (advancing num_frames).
    Each subsequent segment k >= 1 covers [start, start + num_frames) with
    num_cond_frames overlap against the previous segment (net advance of
    num_frames - num_cond_frames, i.e., 80 frames by default), stopping once
    cumulative generated coverage reaches or exceeds exact_frames.
    """
    exact_frames_int = _require_int(exact_frames, "exact_frames")
    num_frames_int = _require_int(num_frames, "num_frames")
    num_cond_int = _require_int(num_cond_frames, "num_cond_frames")

    if exact_frames_int < 1:
        raise ValueError(f"exact_frames must be >= 1, got {exact_frames_int}")
    if num_cond_int < 1:
        raise ValueError(f"num_cond_frames must be >= 1, got {num_cond_int}")
    if num_frames_int <= num_cond_int:
        raise ValueError(
            f"num_frames ({num_frames_int}) must be greater than "
            f"num_cond_frames ({num_cond_int})"
        )
    if (num_frames_int - 1) % 4 != 0:
        raise ValueError(
            f"num_frames must satisfy (num_frames - 1) % 4 == 0, got {num_frames_int}"
        )
    if (num_cond_int - 1) % 4 != 0:
        raise ValueError(
            "num_cond_frames must satisfy (num_cond_frames - 1) % 4 == 0, "
            f"got {num_cond_int}"
        )

    net_advance = num_frames_int - num_cond_int
    segments: list[dict[str, int]] = []
    segment_index = 0
    cumulative_generated = 0
    cumulative_emitted = 0

    while cumulative_generated < exact_frames_int:
        if segment_index == 0:
            start_frame = 0
            overlap_frames = 0
            drop_prefix = 0
            segment_new_capacity = num_frames_int
        else:
            start_frame = cumulative_generated - num_cond_int
            overlap_frames = num_cond_int
            drop_prefix = num_cond_int
            segment_new_capacity = net_advance

        end_frame = start_frame + num_frames_int
        cumulative_generated = end_frame
        remaining_needed = max(0, exact_frames_int - cumulative_emitted)
        emit_frames = min(segment_new_capacity, remaining_needed)
        cumulative_emitted += emit_frames

        segments.append(
            {
                "segment_index": segment_index,
                "start_frame": start_frame,
                "end_frame": end_frame,
                "num_frames": num_frames_int,
                "overlap_frames": overlap_frames,
                "drop_prefix_frames": drop_prefix,
                "emit_frames": emit_frames,
                "cumulative_generated_frames": cumulative_generated,
                "cumulative_emitted_frames": cumulative_emitted,
            }
        )
        segment_index += 1

    return segments


def should_run_vocal_separation(mode: str, is_tts_audio: bool) -> bool:
    """Decide whether to run Kim_Vocal_2 vocal separation on the input audio."""
    normalized = str(mode if mode is not None else "auto").strip().lower() or "auto"
    if normalized not in _VALID_VOCAL_SEPARATION_MODES:
        raise ValueError(
            f"Unsupported vocal_separation mode {mode!r}; "
            f"expected one of {sorted(_VALID_VOCAL_SEPARATION_MODES)}"
        )
    if normalized == "always":
        return True
    if normalized == "never":
        return False
    return not bool(is_tts_audio)


def resolve_longcat16_sampling_params(cfg: dict[str, Any]) -> dict[str, Any]:
    """Normalize LongCat-Video-Avatar 1.6 sampling parameters from worker config.

    Matches official Meituan LongCat-Video-Avatar 1.6 semantics:
    - use_loudness_norm is ALWAYS False for avatar-v1.6.
    - non_distill_cfg: full BF16 classifier-free guidance with enhance_hf=True,
      use_kv_cache=True, steps clamped to [8, 60], CFG clamped to [1.0, 7.0].
    - distill_lora_mix: 8-step LoRA distillation (DMD loraA + OPSD loraB),
      CFG fixed at 1.0, enhance_hf=False, use_kv_cache=(mix_steps == 0),
      mix_steps clamped to [0, 8], mix_alpha clamped to [0.0, 1.0].
    """
    if not isinstance(cfg, dict):
        raise ValueError("cfg must be a dict")

    raw_mode = cfg.get("sampling_mode")
    sampling_mode = (
        str(raw_mode).strip().lower()
        if raw_mode is not None and str(raw_mode).strip()
        else "non_distill_cfg"
    )
    if sampling_mode not in _VALID_SAMPLING_MODES:
        raise ValueError(
            f"Unsupported sampling_mode {raw_mode!r}; "
            f"expected one of {sorted(_VALID_SAMPLING_MODES)}"
        )

    if sampling_mode == "distill_lora_mix":
        mix_steps = _clamp_int(
            _parse_strict_int(cfg.get("mix_steps"), "mix_steps", default=4),
            0,
            8,
        )
        mix_alpha = _clamp_float(
            _parse_strict_float(cfg.get("mix_alpha"), "mix_alpha", default=1.0),
            0.0,
            1.0,
        )
        return {
            "sampling_mode": "distill_lora_mix",
            "use_distill": True,
            "num_inference_steps": 8,
            "text_guidance_scale": 1.0,
            "audio_guidance_scale": 1.0,
            "mix_steps": mix_steps,
            "mix_alpha": mix_alpha,
            "enhance_hf": False,
            "use_kv_cache": mix_steps == 0,
            "use_loudness_norm": False,
        }

    steps = _clamp_int(
        _parse_strict_int(
            cfg.get("num_inference_steps"), "num_inference_steps", default=50
        ),
        8,
        60,
    )
    text_cfg = _clamp_float(
        _parse_strict_float(
            cfg.get("text_guidance_scale"), "text_guidance_scale", default=4.0
        ),
        1.0,
        7.0,
    )
    audio_cfg = _clamp_float(
        _parse_strict_float(
            cfg.get("audio_guidance_scale"), "audio_guidance_scale", default=4.0
        ),
        1.0,
        7.0,
    )
    return {
        "sampling_mode": "non_distill_cfg",
        "use_distill": False,
        "num_inference_steps": steps,
        "text_guidance_scale": text_cfg,
        "audio_guidance_scale": audio_cfg,
        "mix_steps": 0,
        "mix_alpha": 0.0,
        "enhance_hf": True,
        "use_kv_cache": True,
        "use_loudness_norm": False,
    }


def _parse_tri_state_option(
    value: Any,
    name: str,
    *,
    default: str = "auto",
) -> str | bool:
    if value is None:
        return default
    if isinstance(value, bool):
        return value
    if isinstance(value, str):
        norm = value.strip().lower()
        if not norm or norm == "auto":
            return "auto"
        if norm in ("1", "true", "yes", "on"):
            return True
        if norm in ("0", "false", "no", "off"):
            return False
    raise ValueError(f"Unsupported {name} value {value!r}; expected 'auto', true, or false")


def _make_attention_flags(
    *,
    fa3: bool = False,
    fa2: bool = False,
    sage3: bool = False,
    sage2: bool = False,
) -> dict[str, bool]:
    return {
        "enable_flashattn3": bool(fa3),
        "enable_flashattn2": bool(fa2),
        "enable_sageattn3": bool(sage3),
        "enable_sageattn2": bool(sage2),
    }


def resolve_longcat16_attention_flags(
    backend: str,
    *,
    major: int = 0,
    minor: int = 0,
    has_fa3: bool = False,
    has_fa2: bool = False,
    has_sage3: bool = False,
    has_sage2: bool = False,
) -> dict[str, bool]:
    """Resolve LongCat DiT attention flags across SageAttention3/2, FlashAttention3/2, and SDPA.

    Architecture gating:
    - SageAttention3 (FP4 Blackwell): Blackwell only (major == 0 or major >= 10) and has_sage3.
    - SageAttention2 (INT8 QK + FP16 PV): Ampere+ (major == 0 or major >= 8) and has_sage2.
    - FlashAttention-3: Hopper only (major in (0, 9)) and has_fa3.
    - FlashAttention-2: Ampere+ (major == 0 or major >= 8) and has_fa2.
    - Legacy GPUs (0 < major < 8): always degrade to SDPA (all flags False).

    Auto priority:
    - Blackwell (major >= 10): sageattn_3, sageattn_2, flash_attn_2, sdpa.
    - Hopper (major == 9): sageattn_2, flash_attn_3, flash_attn_2, sdpa.
    - Ampere/Ada/unknown (major == 8 or major == 0): sageattn_2, flash_attn_2, sdpa.
    """
    major_int = _require_int(major, "major")
    _ = _require_int(minor, "minor")  # Reserved for architecture-specific checks.
    normalized = str(backend if backend is not None else "auto").strip().lower() or "auto"
    if normalized not in _VALID_ATTENTION_BACKENDS:
        raise ValueError(
            f"Unsupported attention_backend {backend!r}; "
            f"expected one of {sorted(_VALID_ATTENTION_BACKENDS)}"
        )

    sage3_allowed = (major_int == 0 or major_int >= 10) and bool(has_sage3)
    sage2_allowed = (major_int == 0 or major_int >= 8) and bool(has_sage2)
    fa3_allowed = (major_int in (0, 9)) and bool(has_fa3)
    fa2_allowed = (major_int == 0 or major_int >= 8) and bool(has_fa2)

    if normalized == "sdpa":
        return _make_attention_flags()

    if normalized == "sageattn_3":
        if sage3_allowed:
            return _make_attention_flags(sage3=True)
        if sage2_allowed:
            return _make_attention_flags(sage2=True)
        if fa3_allowed:
            return _make_attention_flags(fa3=True)
        if fa2_allowed:
            return _make_attention_flags(fa2=True)
        return _make_attention_flags()

    if normalized == "sageattn_2":
        if sage2_allowed:
            return _make_attention_flags(sage2=True)
        if fa3_allowed:
            return _make_attention_flags(fa3=True)
        if fa2_allowed:
            return _make_attention_flags(fa2=True)
        return _make_attention_flags()

    if normalized == "flash_attn_3":
        if fa3_allowed:
            return _make_attention_flags(fa3=True)
        if fa2_allowed:
            return _make_attention_flags(fa2=True)
        return _make_attention_flags()

    if normalized == "flash_attn_2":
        if fa2_allowed:
            return _make_attention_flags(fa2=True)
        return _make_attention_flags()

    # Auto selection per GPU architecture.
    if major_int >= 10:
        if sage3_allowed:
            return _make_attention_flags(sage3=True)
        if sage2_allowed:
            return _make_attention_flags(sage2=True)
        if fa2_allowed:
            return _make_attention_flags(fa2=True)
        return _make_attention_flags()

    if major_int == 9:
        if sage2_allowed:
            return _make_attention_flags(sage2=True)
        if fa3_allowed:
            return _make_attention_flags(fa3=True)
        if fa2_allowed:
            return _make_attention_flags(fa2=True)
        return _make_attention_flags()

    if sage2_allowed:
        return _make_attention_flags(sage2=True)
    if fa2_allowed:
        return _make_attention_flags(fa2=True)
    return _make_attention_flags()


def build_lossless_ffv1_writer_cmd(
    ffmpeg_bin: str,
    width: int,
    height: int,
    fps: int,
    output_path: str | Path,
) -> list[str]:
    """Build the FFmpeg command that encodes raw rgb24 stdin into lossless FFV1 MKV."""
    bin_path = _validate_ffmpeg_bin(ffmpeg_bin, "ffmpeg_bin")
    out_path = _normalize_ffmpeg_path_arg(output_path, "output_path")
    w = _require_int(width, "width")
    h = _require_int(height, "height")
    r = _require_int(fps, "fps")
    if w < 1 or h < 1:
        raise ValueError(f"width and height must be positive, got {w}x{h}")
    if r < 1:
        raise ValueError(f"fps must be positive, got {r}")

    return [
        bin_path,
        "-y",
        "-protocol_whitelist",
        "file,pipe,fd",
        "-f",
        "rawvideo",
        "-vcodec",
        "rawvideo",
        "-pix_fmt",
        "rgb24",
        "-s",
        f"{w}x{h}",
        "-r",
        str(r),
        "-i",
        "-",
        "-an",
        "-c:v",
        "ffv1",
        "-level",
        "3",
        "-g",
        "1",
        "-slicecrc",
        "1",
        "-pix_fmt",
        "bgr0",
        out_path,
    ]


def _ensure_utf8_stdio() -> None:
    """Reconfigure stdout/stderr to UTF-8 so Chinese progress JSON never fails on cp1252."""
    for stream in (sys.stdout, sys.stderr):
        reconf = getattr(stream, "reconfigure", None)
        if callable(reconf):
            try:
                reconf(encoding="utf-8", errors="replace")
            except Exception:
                pass


def emit_colabone_progress(
    stage: str,
    progress: float,
    message: str,
    **extra: Any,
) -> None:
    """Emit a flushed COLABONE_PROGRESS JSON event line to stdout."""
    _ensure_utf8_stdio()
    clamped = round(_clamp_float(float(progress), 0.0, 1.0), 4)
    payload: dict[str, Any] = dict(extra)
    payload["stage"] = str(stage)
    payload["progress"] = clamped
    payload["message"] = str(message)
    line = COLABONE_PROGRESS_PREFIX + json.dumps(
        payload,
        ensure_ascii=False,
        separators=(",", ":"),
    )
    try:
        print(line, flush=True)
    except UnicodeEncodeError:
        ascii_line = COLABONE_PROGRESS_PREFIX + json.dumps(
            payload,
            ensure_ascii=True,
            separators=(",", ":"),
        )
        print(ascii_line, flush=True)


def normalize_longcat16_speakers(
    cfg: dict[str, Any],
) -> tuple[str, list[dict[str, str]]]:
    """Resolve primary audio path and normalized speakers metadata list from config."""
    raw_audio_path = str(cfg.get("audio_path") or "").strip()
    raw_speakers = cfg.get("speakers")
    normalized: list[dict[str, str]] = []
    if isinstance(raw_speakers, list):
        for idx, item in enumerate(raw_speakers):
            if not isinstance(item, dict):
                continue
            speaker_id = str(item.get("id") or f"speaker_{idx + 1}").strip()
            if not speaker_id:
                speaker_id = f"speaker_{idx + 1}"
            speaker_audio = str(item.get("audio_path") or "").strip()
            normalized.append({"id": speaker_id, "audio_path": speaker_audio})

    primary_audio = raw_audio_path
    if not primary_audio and normalized:
        primary_audio = normalized[0]["audio_path"]

    if not normalized and primary_audio:
        normalized = [{"id": "speaker_1", "audio_path": primary_audio}]

    return primary_audio, normalized


def _write_json_atomic(path: Path | str, payload: dict[str, Any]) -> None:
    target = _ensure_not_symlink(path, "output_manifest_path")
    target.parent.mkdir(parents=True, exist_ok=True)
    serialized = json.dumps(payload, ensure_ascii=False, indent=2) + "\n"
    tmp_path: Path | None = None
    try:
        with tempfile.NamedTemporaryFile(
            mode="w",
            encoding="utf-8",
            dir=str(target.parent),
            prefix=f".{target.name}.",
            suffix=".tmp",
            delete=False,
        ) as handle:
            tmp_path = Path(handle.name)
            handle.write(serialized)
            handle.flush()
            os.fsync(handle.fileno())
        _ensure_not_symlink(target, "output_manifest_path")
        os.replace(tmp_path, target)
        tmp_path = None
    finally:
        if tmp_path is not None and tmp_path.exists():
            try:
                tmp_path.unlink()
            except OSError:
                pass


def _find_free_loopback_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def _is_cuda_device(device: Any) -> bool:
    if device is None:
        return False
    dev_type = getattr(device, "type", None)
    if dev_type is not None:
        return str(dev_type).lower() == "cuda"
    return str(device).strip().lower().startswith("cuda")


def _smoke_test_flash_attn_module(mod: Any, device: Any) -> bool:
    fn = getattr(mod, "flash_attn_func", None)
    varlen_fn = getattr(mod, "flash_attn_varlen_func", None)
    if not (callable(fn) and callable(varlen_fn)):
        return False
    if device is None:
        return True
    if not _is_cuda_device(device):
        return False
    try:
        import torch

        if not torch.cuda.is_available():
            return False
        with torch.no_grad():
            # Probe with head_dim=128 matching LongCat 1.6 DiT (hidden_size=4096, num_heads=32).
            q = torch.zeros((1, 1, 1, 128), dtype=torch.bfloat16, device=device)
            fn(q, q, q)
            q_varlen = torch.zeros((1, 1, 128), dtype=torch.bfloat16, device=device)
            cu_seqlens = torch.tensor([0, 1], dtype=torch.int32, device=device)
            varlen_fn(q_varlen, q_varlen, q_varlen, cu_seqlens, cu_seqlens, 1, 1)
            torch.cuda.synchronize(device)
        return True
    except Exception as exc:
        mod_name = getattr(mod, "__name__", str(mod))
        print(
            f"[colabone-longcat16] WARNING: {mod_name} CUDA kernel probe failed ({exc}); "
            "disabling this FlashAttention backend",
            file=sys.stderr,
            flush=True,
        )
        return False


def _tensor_all_finite(torch_mod: Any, tensor: Any) -> bool:
    isfinite_fn = getattr(torch_mod, "isfinite", None)
    if callable(isfinite_fn):
        mask = isfinite_fn(tensor)
        all_fn = getattr(mask, "all", None)
        if callable(all_fn):
            return bool(all_fn())
    t_isfinite = getattr(tensor, "isfinite", None)
    if callable(t_isfinite):
        mask = t_isfinite()
        all_fn = getattr(mask, "all", None)
        if callable(all_fn):
            return bool(all_fn())
    isnan_fn = getattr(torch_mod, "isnan", None)
    if callable(isnan_fn):
        mask = isnan_fn(tensor)
        any_fn = getattr(mask, "any", None)
        if callable(any_fn) and bool(any_fn()):
            return False
    isinf_fn = getattr(torch_mod, "isinf", None)
    if callable(isinf_fn):
        mask = isinf_fn(tensor)
        any_fn = getattr(mask, "any", None)
        if callable(any_fn) and bool(any_fn()):
            return False
    return True


def _parse_major_version(ver_str: str) -> int | None:
    cleaned = str(ver_str or "").strip().lstrip("vV")
    if not cleaned:
        return None
    head = cleaned.split(".", 1)[0]
    digits = "".join(ch for ch in head if ch.isdigit())
    return int(digits) if digits else None


def _smoke_test_sage3_module(mod: Any, device: Any) -> bool:
    fn = getattr(mod, "sageattn3_blackwell", None)
    if not callable(fn):
        return False
    if device is None:
        return True
    if not _is_cuda_device(device):
        return False
    try:
        import torch

        if not torch.cuda.is_available():
            return False
        with torch.no_grad():
            make_fn = getattr(torch, "randn", None)
            if not callable(make_fn):
                make_fn = torch.zeros
            # Probe with LongCat head_dim=128 and seq_len=256 so FP4 group_size=128 is exercised
            # for both per_block_mean=False (long 480p/720p sequences) and per_block_mean=True.
            q = make_fn((1, 4, 256, 128), dtype=torch.bfloat16, device=device)
            k = make_fn((1, 4, 256, 128), dtype=torch.bfloat16, device=device)
            v = make_fn((1, 4, 256, 128), dtype=torch.bfloat16, device=device)
            for pbm in (False, True):
                k_in = k.clone() if callable(getattr(k, "clone", None)) else k
                out = fn(q, k_in, v, is_causal=False, per_block_mean=pbm)
                if isinstance(out, tuple):
                    out = out[0]
                if not _tensor_all_finite(torch, out):
                    raise RuntimeError(
                        f"sageattn3_blackwell returned non-finite output (per_block_mean={pbm})"
                    )
            # Also probe an asymmetric non-128-multiple shape (mirrors AVC KV-cache continuation).
            q_asym = make_fn((1, 4, 300, 128), dtype=torch.bfloat16, device=device)
            k_asym = make_fn((1, 4, 360, 128), dtype=torch.bfloat16, device=device)
            v_asym = make_fn((1, 4, 360, 128), dtype=torch.bfloat16, device=device)
            k_asym_in = (
                k_asym.clone() if callable(getattr(k_asym, "clone", None)) else k_asym
            )
            out_asym = fn(
                q_asym,
                k_asym_in,
                v_asym,
                is_causal=False,
                per_block_mean=False,
            )
            if isinstance(out_asym, tuple):
                out_asym = out_asym[0]
            if not _tensor_all_finite(torch, out_asym):
                raise RuntimeError(
                    "sageattn3_blackwell returned non-finite output "
                    "(asymmetric non-128-multiple)"
                )
            torch.cuda.synchronize(device)
        return True
    except Exception as exc:
        print(
            f"[colabone-longcat16] WARNING: sageattn3 CUDA kernel probe failed ({exc}); "
            "disabling SageAttention3",
            file=sys.stderr,
            flush=True,
        )
        return False


def _smoke_test_sage2_module(mod: Any, device: Any) -> bool:
    fn = getattr(mod, "sageattn", None)
    if not callable(fn):
        return False
    ver_raw = getattr(mod, "__version__", None)
    if not ver_raw:
        try:
            ver_raw = importlib.metadata.version("sageattention")
        except Exception:
            ver_raw = None
    if ver_raw is not None:
        major_ver = _parse_major_version(str(ver_raw))
        if major_ver is not None and major_ver < 2:
            print(
                f"[colabone-longcat16] WARNING: sageattention version {ver_raw!r} < 2.0; "
                "disabling SageAttention2",
                file=sys.stderr,
                flush=True,
            )
            return False
    if device is None:
        return True
    if not _is_cuda_device(device):
        return False
    try:
        import torch

        if not torch.cuda.is_available():
            return False
        with torch.no_grad():
            make_fn = getattr(torch, "randn", None)
            if not callable(make_fn):
                make_fn = torch.zeros
            # Probe with LongCat head_dim=128 BF16 HND shape.
            q = make_fn((1, 4, 256, 128), dtype=torch.bfloat16, device=device)
            k = make_fn((1, 4, 256, 128), dtype=torch.bfloat16, device=device)
            v = make_fn((1, 4, 256, 128), dtype=torch.bfloat16, device=device)
            out = fn(
                q,
                k,
                v,
                tensor_layout="HND",
                is_causal=False,
                sm_scale=128**-0.5,
            )
            if isinstance(out, tuple):
                out = out[0]
            if not _tensor_all_finite(torch, out):
                raise RuntimeError("sageattn returned non-finite output")
            torch.cuda.synchronize(device)
        return True
    except Exception as exc:
        print(
            f"[colabone-longcat16] WARNING: sageattention v2 CUDA kernel probe failed ({exc}); "
            "disabling SageAttention2",
            file=sys.stderr,
            flush=True,
        )
        return False


def _probe_sage_attn_availability(
    device: Any = None,
    *,
    major: int = 0,
    backend: str = "auto",
) -> tuple[bool, bool]:
    major_int = _require_int(major, "major")
    normalized = str(backend if backend is not None else "auto").strip().lower() or "auto"
    if (
        normalized in ("sdpa", "flash_attn_3", "flash_attn_2")
        or normalized not in _VALID_ATTENTION_BACKENDS
    ):
        return False, False
    if 0 < major_int < 8:
        return False, False

    has_sage3 = False
    has_sage2 = False

    if (major_int >= 10 and normalized in ("auto", "sageattn_3")) or (
        major_int == 0 and normalized == "sageattn_3"
    ):
        try:
            import sageattn3  # type: ignore[import-not-found]

            has_sage3 = _smoke_test_sage3_module(sageattn3, device)
        except Exception:
            has_sage3 = False

    if (major_int == 0 or major_int >= 8) and normalized in (
        "auto",
        "sageattn_3",
        "sageattn_2",
    ):
        try:
            import sageattention  # type: ignore[import-not-found]

            has_sage2 = _smoke_test_sage2_module(sageattention, device)
        except Exception:
            has_sage2 = False

    return has_sage3, has_sage2


def _probe_flash_attn_availability(
    device: Any = None,
    *,
    major: int = 0,
    backend: str = "auto",
) -> tuple[bool, bool]:
    major_int = _require_int(major, "major")
    normalized = str(backend if backend is not None else "auto").strip().lower() or "auto"
    if normalized == "sdpa" or normalized not in _VALID_ATTENTION_BACKENDS:
        return False, False

    has_fa3 = False
    has_fa2 = False

    if major_int in (0, 9) and normalized in (
        "auto",
        "sageattn_3",
        "sageattn_2",
        "flash_attn_3",
    ):
        try:
            import flash_attn_interface  # type: ignore[import-not-found]

            has_fa3 = _smoke_test_flash_attn_module(flash_attn_interface, device)
        except Exception:
            has_fa3 = False

    if (
        (major_int == 0 or major_int >= 8)
        and normalized in ("auto", "sageattn_3", "sageattn_2", "flash_attn_3", "flash_attn_2")
        and not (major_int == 9 and has_fa3)
    ):
        try:
            import flash_attn  # type: ignore[import-not-found]

            has_fa2 = _smoke_test_flash_attn_module(flash_attn, device)
        except Exception:
            has_fa2 = False

    return has_fa3, has_fa2


def _seed_all_generators(torch_mod: Any, np_mod: Any, seed: int) -> None:
    random.seed(seed)
    np_mod.random.seed(seed % (2**32))
    torch_mod.manual_seed(seed)
    cuda_mod = getattr(torch_mod, "cuda", None)
    if cuda_mod is not None and callable(getattr(cuda_mod, "manual_seed_all", None)):
        try:
            cuda_mod.manual_seed_all(seed)
        except Exception:
            pass


def _torch_gc(torch_mod: Any) -> None:
    gc.collect()
    cuda_mod = getattr(torch_mod, "cuda", None)
    if cuda_mod is not None and cuda_mod.is_available():
        try:
            cuda_mod.empty_cache()
        except Exception:
            pass
        try:
            cuda_mod.ipc_collect()
        except Exception:
            pass


def _torch_gc_if_low_vram(
    torch_mod: Any,
    device: Any,
    *,
    min_free_bytes: int = 16 * 1024 * 1024 * 1024,
) -> bool:
    """Run full CUDA GC only when effective free VRAM falls below min_free_bytes (keeps allocator warm on 80GB/96GB)."""
    cuda_mod = getattr(torch_mod, "cuda", None)
    mem_get_info = getattr(cuda_mod, "mem_get_info", None) if cuda_mod is not None else None
    if callable(mem_get_info) and _is_cuda_device(device):
        try:
            free_bytes, _total_bytes = mem_get_info(device)
            reserved_bytes = 0
            allocated_bytes = 0
            mem_reserved_fn = getattr(cuda_mod, "memory_reserved", None)
            mem_allocated_fn = getattr(cuda_mod, "memory_allocated", None)
            if callable(mem_reserved_fn) and callable(mem_allocated_fn):
                try:
                    reserved_bytes = int(mem_reserved_fn(device))
                    allocated_bytes = int(mem_allocated_fn(device))
                except Exception:
                    reserved_bytes = 0
                    allocated_bytes = 0
            effective_free = int(free_bytes) + max(0, reserved_bytes - allocated_bytes)
            if effective_free >= int(min_free_bytes):
                return False
        except Exception:
            pass
    _torch_gc(torch_mod)
    return True


def _detect_available_ram_bytes() -> int:
    """Read Linux MemAvailable from /proc/meminfo in bytes; returns 0 on non-Linux or error."""
    try:
        with open("/proc/meminfo", "r", encoding="utf-8") as handle:
            for line in handle:
                if line.startswith("MemAvailable:"):
                    parts = line.split()
                    if len(parts) >= 2 and parts[1].isdigit():
                        return int(parts[1]) * 1024
    except Exception:
        return 0
    return 0


def _get_cuda_total_vram_mib(torch_mod: Any, device: Any) -> int:
    cuda_mod = getattr(torch_mod, "cuda", None)
    if cuda_mod is None or not _is_cuda_device(device):
        return 0
    get_props = getattr(cuda_mod, "get_device_properties", None)
    if callable(get_props):
        try:
            props = get_props(device)
            total_mem = int(getattr(props, "total_memory", 0) or 0)
            if total_mem > 0:
                return total_mem // (1024 * 1024)
        except Exception:
            pass
    mem_get_info = getattr(cuda_mod, "mem_get_info", None)
    if callable(mem_get_info):
        try:
            _free_bytes, total_bytes = mem_get_info(device)
            if int(total_bytes) > 0:
                return int(total_bytes) // (1024 * 1024)
        except Exception:
            pass
    return 0


def _is_triton_importable() -> bool:
    if "triton" in sys.modules:
        return sys.modules["triton"] is not None
    try:
        return importlib.util.find_spec("triton") is not None
    except Exception:
        return False


def _load_audio_mono_16k(
    audio_path: str,
    *,
    ffmpeg_bin: str = "ffmpeg",
    target_sr: int = 16000,
) -> tuple[Any, int]:
    """Load an audio file as a 1-D float32 numpy array at target_sr (16 kHz mono)."""
    import numpy as np

    try:
        import librosa  # type: ignore[import-not-found]

        waveform, sr = librosa.load(audio_path, sr=target_sr, mono=True)
        arr = np.asarray(waveform, dtype=np.float32).reshape(-1)
        if arr.size > 0:
            return arr, int(sr)
    except Exception as exc:
        print(
            f"[colabone-longcat16] WARNING: librosa.load failed ({exc}), "
            "falling back to ffmpeg f32le decoder",
            file=sys.stderr,
            flush=True,
        )

    safe_audio = _normalize_ffmpeg_path_arg(audio_path, "audio_path")
    safe_ffmpeg = _validate_ffmpeg_bin(ffmpeg_bin, "ffmpeg_bin")
    cmd = [
        safe_ffmpeg,
        "-nostdin",
        "-hide_banner",
        "-loglevel",
        "error",
        "-protocol_whitelist",
        "file,pipe,fd",
        "-i",
        safe_audio,
        "-ac",
        "1",
        "-ar",
        str(target_sr),
        "-f",
        "f32le",
        "pipe:1",
    ]
    proc = subprocess.run(
        cmd,
        capture_output=True,
        check=False,
        stdin=subprocess.DEVNULL,
        env=_sanitized_subprocess_env(),
    )
    if proc.returncode != 0:
        err = proc.stderr.decode("utf-8", errors="replace").strip()
        raise RuntimeError(
            f"Failed to decode audio {audio_path} via ffmpeg (rc={proc.returncode}): {err}"
        )
    arr = np.frombuffer(proc.stdout, dtype=np.float32).copy()
    if arr.size == 0:
        raise RuntimeError(f"Decoded audio is empty: {audio_path}")
    return arr, target_sr


def _resample_audio_if_needed(
    waveform: Any,
    src_sr: int,
    target_sr: int = 16000,
) -> tuple[Any, int]:
    import numpy as np

    arr = np.asarray(waveform, dtype=np.float32).reshape(-1)
    if int(src_sr) == int(target_sr) or arr.size == 0:
        return arr, int(target_sr)
    try:
        import librosa  # type: ignore[import-not-found]

        resampled = librosa.resample(arr, orig_sr=int(src_sr), target_sr=int(target_sr))
        return np.asarray(resampled, dtype=np.float32).reshape(-1), int(target_sr)
    except Exception:
        import scipy.signal as ss  # type: ignore[import-not-found]

        g = math.gcd(int(target_sr), int(src_sr))
        resampled = ss.resample_poly(arr, int(target_sr) // g, int(src_sr) // g)
        return np.asarray(resampled, dtype=np.float32).reshape(-1), int(target_sr)


def _select_fastest_sdpa_backend(
    torch_mod: Any,
    device: Any,
) -> tuple[Any, str]:
    """Benchmark lossless BF16 CUDA SDPA backends and return (SDPBackend | None, label).

    On Blackwell (sm_120) and Hopper (sm_90), PyTorch's cuDNN 9 FMHA backend can be
    substantially faster than CUTLASS mem-efficient SDPA when inputs are contiguous.
    Probes only mathematically equivalent fused BF16 backends (never MATH) in < 50 ms.
    """
    if not _is_cuda_device(device):
        return None, "default"
    cuda_mod = getattr(torch_mod, "cuda", None)
    if cuda_mod is None or not callable(getattr(cuda_mod, "is_available", None)):
        return None, "default"
    try:
        if not cuda_mod.is_available():
            return None, "default"
    except Exception:
        return None, "default"

    # Enable lossless fused CUDA SDPA backends and TF32 for auxiliary FP32 ops.
    backends_cuda = getattr(getattr(torch_mod, "backends", None), "cuda", None)
    backends_cudnn = getattr(getattr(torch_mod, "backends", None), "cudnn", None)
    for obj, attr, val in (
        (backends_cuda, "enable_cudnn_sdp", True),
        (backends_cuda, "enable_flash_sdp", True),
        (backends_cuda, "enable_mem_efficient_sdp", True),
    ):
        fn = getattr(obj, attr, None)
        if callable(fn):
            try:
                fn(val)
            except Exception:
                pass
    try:
        if backends_cuda is not None and hasattr(backends_cuda, "matmul"):
            backends_cuda.matmul.allow_tf32 = True
        if backends_cudnn is not None and hasattr(backends_cudnn, "allow_tf32"):
            backends_cudnn.allow_tf32 = True
    except Exception:
        pass

    nn_attn = getattr(getattr(torch_mod, "nn", None), "attention", None)
    sdpa_kernel_fn = getattr(nn_attn, "sdpa_kernel", None)
    sdp_backend_enum = getattr(nn_attn, "SDPBackend", None)
    f_mod = getattr(getattr(torch_mod, "nn", None), "functional", None)
    if not (
        callable(sdpa_kernel_fn)
        and sdp_backend_enum is not None
        and f_mod is not None
        and callable(getattr(f_mod, "scaled_dot_product_attention", None))
    ):
        return None, "default"

    candidates: list[tuple[str, Any]] = []
    for label, enum_attr in (
        ("cudnn", "CUDNN_ATTENTION"),
        ("flash", "FLASH_ATTENTION"),
        ("efficient", "EFFICIENT_ATTENTION"),
    ):
        member = getattr(sdp_backend_enum, enum_attr, None)
        if member is not None:
            candidates.append((label, member))
    if not candidates:
        return None, "default"

    results: list[tuple[float, str, Any]] = []
    timings_desc: list[str] = []
    try:
        with torch_mod.no_grad():
            # LongCat 1.6 DiT head shape: num_heads=32, head_dim=128, pure BF16.
            q = torch_mod.randn((1, 32, 4096, 128), dtype=torch_mod.bfloat16, device=device)
            k = torch_mod.randn((1, 32, 4096, 128), dtype=torch_mod.bfloat16, device=device)
            v = torch_mod.randn((1, 32, 4096, 128), dtype=torch_mod.bfloat16, device=device)
            scale = 128**-0.5
            for label, backend_member in candidates:
                try:
                    with sdpa_kernel_fn([backend_member]):
                        # 1 warmup + 3 timed passes.
                        f_mod.scaled_dot_product_attention(q, k, v, dropout_p=0.0, scale=scale)
                        cuda_mod.synchronize(device)
                        t0 = time.perf_counter()
                        for _ in range(3):
                            f_mod.scaled_dot_product_attention(
                                q, k, v, dropout_p=0.0, scale=scale
                            )
                        cuda_mod.synchronize(device)
                        ms = (time.perf_counter() - t0) * 1000.0 / 3.0
                    results.append((ms, label, backend_member))
                    timings_desc.append(f"{label}={ms:.2f}ms")
                except Exception:
                    timings_desc.append(f"{label}=unavailable")
            del q, k, v
    except Exception:
        return None, "default"

    if not results:
        return None, "default"

    results.sort(key=lambda item: item[0])
    best_ms, best_label, best_member = results[0]
    print(
        f"[colabone-longcat16] INFO: SDPA BF16 benchmark ({', '.join(timings_desc)}); "
        f"selected {best_label} ({best_ms:.2f}ms)",
        file=sys.stderr,
        flush=True,
    )
    return best_member, best_label


def _install_accelerated_attention(
    dit: Any,
    torch_mod: Any,
    *,
    use_sage3: bool = False,
    use_sage2: bool = False,
    preferred_sdpa_backend: Any = None,
) -> dict[str, Any]:
    """Install SageAttention3/2 + benchmarked BF16 SDPA patches onto LongCat DiT blocks.

    - 3D spatiotemporal self-attention (block.attn._process_attn, shape [B, H, S, D]):
      uses SageAttention3 (Blackwell FP4, cloning K so in-place mean-subtraction cannot
      corrupt LongCat's KV cache) for S >= 256 and D == 128, then SageAttention2 (HND
      zero-copy), then fastest benchmarked BF16 SDPA, with automatic once-only runtime
      fallback on kernel error or non-finite output.
    - Audio cross-attention (short 5..32 token windows) and text cross-attention (variable
      kv_seqlen) ALWAYS use lossless benchmarked BF16 SDPA rather than quantized attention.
    """
    from einops import rearrange  # type: ignore[import-not-found]

    f_mod = torch_mod.nn.functional
    nn_attn = getattr(getattr(torch_mod, "nn", None), "attention", None)
    sdpa_kernel_fn = (
        getattr(nn_attn, "sdpa_kernel", None)
        if preferred_sdpa_backend is not None
        else None
    )
    if not callable(sdpa_kernel_fn):
        sdpa_kernel_fn = None

    sdp_backend_enum = getattr(nn_attn, "SDPBackend", None)
    sdpa_supports_priority = False
    ordered_sdpa_backends: list[Any] = []
    if sdpa_kernel_fn is not None:
        try:
            sdpa_sig = inspect.signature(sdpa_kernel_fn)
            sdpa_params = sdpa_sig.parameters
            sdpa_supports_priority = "set_priority" in sdpa_params or any(
                p.kind == inspect.Parameter.VAR_KEYWORD for p in sdpa_params.values()
            )
        except (TypeError, ValueError):
            sdpa_supports_priority = False

        seen_backends: list[Any] = []
        for cand in (
            preferred_sdpa_backend,
            getattr(sdp_backend_enum, "FLASH_ATTENTION", None)
            if sdp_backend_enum is not None
            else None,
            getattr(sdp_backend_enum, "EFFICIENT_ATTENTION", None)
            if sdp_backend_enum is not None
            else None,
            getattr(sdp_backend_enum, "MATH", None)
            if sdp_backend_enum is not None
            else None,
        ):
            if cand is not None and cand not in seen_backends:
                seen_backends.append(cand)
        ordered_sdpa_backends = seen_backends

    math_backend = (
        getattr(sdp_backend_enum, "MATH", None) if sdp_backend_enum is not None else None
    )
    use_priority_sdpa = bool(
        sdpa_kernel_fn is not None
        and sdpa_supports_priority
        and ordered_sdpa_backends
        and math_backend is not None
        and math_backend in ordered_sdpa_backends
    )
    sdpa_priority_tls = threading.local()

    raw_compiler_disable = getattr(
        getattr(torch_mod, "compiler", None), "disable", None
    )
    if not callable(raw_compiler_disable):
        raw_compiler_disable = getattr(
            getattr(torch_mod, "_dynamo", None), "disable", None
        )

    def _compiler_disable(fn: Any) -> Any:
        if callable(raw_compiler_disable):
            try:
                return raw_compiler_disable(fn)
            except Exception:
                return fn
        return fn

    sage3_fn: Any = None
    if use_sage3:
        try:
            from sageattn3 import (  # type: ignore[import-not-found]
                sageattn3_blackwell,
            )

            if callable(sageattn3_blackwell):
                sage3_fn = sageattn3_blackwell
        except Exception:
            sage3_fn = None

    sage2_fn: Any = None
    if use_sage2:
        try:
            from sageattention import sageattn  # type: ignore[import-not-found]

            if callable(sageattn):
                sage2_fn = sageattn
        except Exception:
            sage2_fn = None

    attn_state: dict[str, Any] = {
        "use_sage3": sage3_fn is not None,
        "use_sage2": sage2_fn is not None,
        "sage3_verified": False,
        "sage3_verified_regimes": set(),
        "sage2_verified": False,
    }

    def _prepare_qkv(q_t: Any, k_t: Any, v_t: Any) -> tuple[Any, Any, Any]:
        v_dtype = v_t.dtype
        if q_t.dtype != v_dtype:
            q_t = q_t.to(v_dtype)
        if k_t.dtype != v_dtype:
            k_t = k_t.to(v_dtype)
        q_is_contig = getattr(q_t, "is_contiguous", None)
        if callable(q_is_contig) and not q_is_contig():
            q_t = q_t.contiguous()
        k_is_contig = getattr(k_t, "is_contiguous", None)
        if callable(k_is_contig) and not k_is_contig():
            k_t = k_t.contiguous()
        v_is_contig = getattr(v_t, "is_contiguous", None)
        if callable(v_is_contig) and not v_is_contig():
            v_t = v_t.contiguous()
        return q_t, k_t, v_t

    @_compiler_disable
    def _run_sdpa_legacy_single_backend(
        q_t: Any,
        k_t: Any,
        v_t: Any,
        scale: Any,
    ) -> Any:
        try:
            with sdpa_kernel_fn([preferred_sdpa_backend]):
                return f_mod.scaled_dot_product_attention(
                    q_t,
                    k_t,
                    v_t,
                    dropout_p=0.0,
                    scale=scale,
                )
        except RuntimeError:
            return f_mod.scaled_dot_product_attention(
                q_t,
                k_t,
                v_t,
                dropout_p=0.0,
                scale=scale,
            )

    @_compiler_disable
    def _run_sdpa_priority_fallback(
        q_t: Any,
        k_t: Any,
        v_t: Any,
        scale: Any,
    ) -> Any:
        with sdpa_kernel_fn(ordered_sdpa_backends, set_priority=True):
            return f_mod.scaled_dot_product_attention(
                q_t,
                k_t,
                v_t,
                dropout_p=0.0,
                scale=scale,
            )

    def _run_sdpa(
        q_t: Any,
        k_t: Any,
        v_t: Any,
        *,
        scale: Any,
        prepared: bool = False,
        is_self_attn: bool = False,
    ) -> Any:
        if not prepared:
            q_t, k_t, v_t = _prepare_qkv(q_t, k_t, v_t)
        if sdpa_kernel_fn is not None and _is_cuda_device(getattr(q_t, "device", None)):
            if use_priority_sdpa:
                if getattr(sdpa_priority_tls, "active", False):
                    return f_mod.scaled_dot_product_attention(
                        q_t,
                        k_t,
                        v_t,
                        dropout_p=0.0,
                        scale=scale,
                    )
                return _run_sdpa_priority_fallback(q_t, k_t, v_t, scale)
            if is_self_attn:
                return _run_sdpa_legacy_single_backend(q_t, k_t, v_t, scale)
        return f_mod.scaled_dot_product_attention(
            q_t,
            k_t,
            v_t,
            dropout_p=0.0,
            scale=scale,
        )

    def _sdpa_process_attn(self_attn: Any, q: Any, k: Any, v: Any, shape: Any) -> Any:
        _ = shape
        return _run_sdpa(q, k, v, scale=self_attn.scale, is_self_attn=True)

    @_compiler_disable
    def _try_sage_self_attn(q_t: Any, k_t: Any, v_t: Any, scale: Any) -> Any:
        if not _is_cuda_device(getattr(q_t, "device", None)):
            return None

        if (
            attn_state["use_sage3"]
            and callable(getattr(q_t, "size", None))
            and callable(getattr(k_t, "size", None))
            and q_t.size(2) >= 256
            and k_t.size(2) >= 256
            and q_t.size(-1) == 128
        ):
            try:
                # Cap per_block_mean delta_s at ~256 MB (64M fp32 elements) to avoid
                # multi-GB VRAM blowups and LayoutDS int32_t stride overflow on long sequences.
                delta_s_elems = (
                    int(q_t.size(0))
                    * int(q_t.size(1))
                    * ((int(q_t.size(2)) + 127) // 128)
                    * int(k_t.size(2))
                )
                use_per_block_mean = delta_s_elems <= 64_000_000
                # Clone K so sageattn3_blackwell's in-place mean-subtraction cannot mutate
                # LongCat's cached keys (k_full / k_cache).
                k_safe = k_t.clone()
                out = sage3_fn(
                    q_t,
                    k_safe,
                    v_t,
                    is_causal=False,
                    per_block_mean=use_per_block_mean,
                )
                if isinstance(out, tuple):
                    out = out[0]
                regime_key = (
                    bool(use_per_block_mean),
                    int(q_t.size(2)) == int(k_t.size(2)),
                )
                verified_regimes = attn_state["sage3_verified_regimes"]
                if regime_key not in verified_regimes:
                    if not _tensor_all_finite(torch_mod, out):
                        raise RuntimeError(
                            "sageattn3_blackwell produced non-finite output "
                            f"(per_block_mean={use_per_block_mean}, symmetric={regime_key[1]})"
                        )
                    verified_regimes.add(regime_key)
                    attn_state["sage3_verified"] = True
                return out
            except Exception as exc:
                attn_state["use_sage3"] = False
                print(
                    f"[colabone-longcat16] WARNING: sageattn3_blackwell runtime error ({exc}); "
                    "falling back to SageAttention2/SDPA",
                    file=sys.stderr,
                    flush=True,
                )

        if attn_state["use_sage2"]:
            try:
                out = sage2_fn(
                    q_t,
                    k_t,
                    v_t,
                    tensor_layout="HND",
                    is_causal=False,
                    sm_scale=scale,
                )
                if isinstance(out, tuple):
                    out = out[0]
                if not attn_state["sage2_verified"]:
                    if not _tensor_all_finite(torch_mod, out):
                        raise RuntimeError("sageattn v2 produced non-finite output")
                    attn_state["sage2_verified"] = True
                return out
            except Exception as exc:
                attn_state["use_sage2"] = False
                print(
                    f"[colabone-longcat16] WARNING: sageattention v2 runtime error ({exc}); "
                    "falling back to BF16 SDPA",
                    file=sys.stderr,
                    flush=True,
                )

        return None

    def _accel_process_attn(self_attn: Any, q: Any, k: Any, v: Any, shape: Any) -> Any:
        _ = shape
        q_t, k_t, v_t = _prepare_qkv(q, k, v)
        sage_out = _try_sage_self_attn(q_t, k_t, v_t, self_attn.scale)
        if sage_out is not None:
            return sage_out
        return _run_sdpa(
            q_t,
            k_t,
            v_t,
            scale=self_attn.scale,
            prepared=True,
            is_self_attn=True,
        )

    def _sdpa_audio_cross_attn(
        self_mod: Any,
        x: Any,
        cond: Any,
        frames_num: int | None = None,
        x_ref_attn_map: Any = None,
        human_num: int | None = None,
    ) -> Any:
        n_t = frames_num
        out_dtype = x.dtype
        x = rearrange(x, "B (N_t S) C -> (B N_t) S C", N_t=n_t)

        batch_nt, seq_len, hidden = x.shape
        q = self_mod.q_linear(x)
        q = q.view(batch_nt, seq_len, self_mod.num_heads, self_mod.head_dim).permute(
            (0, 2, 1, 3)
        )
        q = self_mod.q_norm(q)

        if x_ref_attn_map is not None:
            from longcat_video.modules.avatar.attention import (  # type: ignore[import-not-found]
                normalize_and_scale,
            )

            max_values = x_ref_attn_map.max(1).values[:, None, None]
            min_values = x_ref_attn_map.min(1).values[:, None, None]
            max_min_values = torch_mod.cat([max_values, min_values], dim=2)
            human1_max, human1_min = (
                max_min_values[0, :, 0].max(),
                max_min_values[0, :, 1].min(),
            )
            human2_max, human2_min = (
                max_min_values[1, :, 0].max(),
                max_min_values[1, :, 1].min(),
            )
            human1 = normalize_and_scale(
                x_ref_attn_map[0],
                (human1_min, human1_max),
                (self_mod.rope_h1[0], self_mod.rope_h1[1]),
            )
            human2 = normalize_and_scale(
                x_ref_attn_map[1],
                (human2_min, human2_max),
                (self_mod.rope_h2[0], self_mod.rope_h2[1]),
            )
            background_pos = (
                self_mod.rope_bak if x_ref_attn_map.shape[0] <= 3 else 100
            )
            back = torch_mod.full(
                (x_ref_attn_map.size(1),),
                background_pos,
                dtype=human1.dtype,
                device=human1.device,
            )
            max_indices = x_ref_attn_map.argmax(dim=0).clamp(max=2)
            normalized_map = torch_mod.stack([human1, human2, back], dim=1)
            normalized_pos = normalized_map[range(x_ref_attn_map.size(1)), max_indices]

            q = rearrange(q, "(B N_t) H S C -> B H (N_t S) C", N_t=n_t)
            q = self_mod.rope_1d(q, normalized_pos)
            q = rearrange(q, "B H (N_t S) C -> (B N_t) H S C", N_t=n_t)

        _, n_a, _ = cond.shape
        encoder_kv = self_mod.kv_linear(cond)
        encoder_kv = encoder_kv.view(
            batch_nt, n_a, 2, self_mod.num_heads, self_mod.head_dim
        ).permute((2, 0, 3, 1, 4))
        encoder_k, encoder_v = encoder_kv.unbind(0)
        encoder_k = self_mod.k_norm(encoder_k)

        if x_ref_attn_map is not None:
            per_frame = torch_mod.zeros(
                n_a, dtype=encoder_k.dtype, device=encoder_k.device
            )
            human1_pos = (self_mod.rope_h1[0] + self_mod.rope_h1[1]) / 2
            human2_pos = (self_mod.rope_h2[0] + self_mod.rope_h2[1]) / 2
            if human_num is not None and human_num > 2:
                background_pos = (
                    self_mod.rope_bak if x_ref_attn_map.shape[0] <= 3 else 100
                )
                tokens_per_human = per_frame.size(0) // human_num
                per_frame[:tokens_per_human] = human1_pos
                per_frame[tokens_per_human : 2 * tokens_per_human] = human2_pos
                per_frame[2 * tokens_per_human :] = background_pos
            else:
                per_frame[: per_frame.size(0) // 2] = human1_pos
                per_frame[per_frame.size(0) // 2 :] = human2_pos
            encoder_pos = torch_mod.concat([per_frame] * n_t, dim=0)
            encoder_k = rearrange(encoder_k, "(B N_t) H S C -> B H (N_t S) C", N_t=n_t)
            encoder_k = self_mod.rope_1d(encoder_k, encoder_pos)
            encoder_k = rearrange(encoder_k, "B H (N_t S) C -> (B N_t) H S C", N_t=n_t)

        attn_out = _run_sdpa(q, encoder_k, encoder_v, scale=self_mod.scale)
        attn_out = attn_out.transpose(1, 2).reshape(batch_nt, seq_len, hidden)
        attn_out = self_mod.proj_drop(self_mod.proj(attn_out))
        attn_out = rearrange(attn_out, "(B N_t) S C -> B (N_t S) C", N_t=n_t)
        return attn_out.to(out_dtype)

    kv_seqlen_cache_tls = threading.local()

    def _sdpa_multihead_cross_process(
        self_mod: Any,
        x: Any,
        cond: Any,
        kv_seqlen: Any,
    ) -> Any:
        out_dtype = x.dtype
        batch_size, seq_len, hidden = x.shape

        q_all = self_mod.q_linear(x).view(
            batch_size, seq_len, self_mod.num_heads, self_mod.head_dim
        )
        q_all = q_all.permute(0, 2, 1, 3)
        if getattr(self_mod, "q_norm", None) is not None:
            q_all = self_mod.q_norm(q_all)

        kv_raw = self_mod.kv_linear(cond)
        scale = getattr(self_mod, "scale", self_mod.head_dim**-0.5)

        if kv_seqlen is None:
            kv = kv_raw.view(
                batch_size, -1, 2, self_mod.num_heads, self_mod.head_dim
            ).permute(2, 0, 3, 1, 4)
            k_all, v_all = kv.unbind(0)
            if getattr(self_mod, "k_norm", None) is not None:
                k_all = self_mod.k_norm(k_all)
            out = _run_sdpa(q_all, k_all, v_all, scale=scale)
        else:
            dynamo_mod = getattr(torch_mod, "_dynamo", None)
            is_dynamo_compiling = bool(
                dynamo_mod is not None
                and callable(getattr(dynamo_mod, "is_compiling", None))
                and dynamo_mod.is_compiling()
            )
            seq_lens_cache = (
                None if is_dynamo_compiling else getattr(kv_seqlen_cache_tls, "cache", None)
            )
            cache_key = (id(kv_seqlen), batch_size) if seq_lens_cache is not None else None
            cached_entry = seq_lens_cache.get(cache_key) if cache_key is not None else None
            if cached_entry is not None and cached_entry[0] is kv_seqlen:
                seq_lens = cached_entry[1]
            else:
                raw_lens = kv_seqlen.tolist() if hasattr(kv_seqlen, "tolist") else kv_seqlen
                if isinstance(raw_lens, int) and not isinstance(raw_lens, bool):
                    seq_lens = [int(raw_lens)] * batch_size
                else:
                    seq_lens = [int(s) for s in raw_lens]
                if cache_key is not None:
                    seq_lens_cache[cache_key] = (kv_seqlen, seq_lens)
            if len(seq_lens) != batch_size:
                raise ValueError(
                    f"kv_seqlen length ({len(seq_lens)}) does not match batch size ({batch_size})"
                )
            if any(seq_k < 0 for seq_k in seq_lens):
                raise ValueError(
                    f"kv_seqlen values must be non-negative, got {seq_lens}"
                )

            total_kv_expected = sum(seq_lens)
            is_padded_batched = (
                kv_raw.ndim == 3
                and kv_raw.shape[0] == batch_size
                and batch_size > 1
                and kv_raw.shape[0] * kv_raw.shape[1] != total_kv_expected
            )

            outputs = []
            if is_padded_batched:
                kv = kv_raw.view(
                    batch_size,
                    kv_raw.shape[1],
                    2,
                    self_mod.num_heads,
                    self_mod.head_dim,
                ).permute(2, 0, 3, 1, 4)
                k_all, v_all = kv.unbind(0)
                if getattr(self_mod, "k_norm", None) is not None:
                    k_all = self_mod.k_norm(k_all)
                if q_all.dtype != v_all.dtype:
                    q_all = q_all.to(v_all.dtype)
                if k_all.dtype != v_all.dtype:
                    k_all = k_all.to(v_all.dtype)
                max_kv_len = int(k_all.shape[2])
                for b_idx, seq_k in enumerate(seq_lens):
                    if seq_k > max_kv_len:
                        raise ValueError(
                            f"kv_seqlen[{b_idx}] ({seq_k}) exceeds padded KV length ({max_kv_len})"
                        )
                    if seq_k == 0:
                        outputs.append(
                            torch_mod.zeros(
                                (1, self_mod.num_heads, seq_len, self_mod.head_dim),
                                dtype=v_all.dtype,
                                device=v_all.device,
                            )
                        )
                        continue
                    outputs.append(
                        _run_sdpa(
                            q_all[b_idx : b_idx + 1],
                            k_all[b_idx : b_idx + 1, :, :seq_k, :],
                            v_all[b_idx : b_idx + 1, :, :seq_k, :],
                            scale=scale,
                        )
                    )
            else:
                kv = kv_raw.reshape(
                    1, -1, 2, self_mod.num_heads, self_mod.head_dim
                ).permute(2, 0, 3, 1, 4)
                k_all, v_all = kv.unbind(0)
                if getattr(self_mod, "k_norm", None) is not None:
                    k_all = self_mod.k_norm(k_all)
                if q_all.dtype != v_all.dtype:
                    q_all = q_all.to(v_all.dtype)
                if k_all.dtype != v_all.dtype:
                    k_all = k_all.to(v_all.dtype)
                total_kv_avail = int(k_all.shape[2])
                offset = 0
                for b_idx, seq_k in enumerate(seq_lens):
                    if offset + seq_k > total_kv_avail:
                        raise ValueError(
                            f"kv_seqlen[{b_idx}] ({seq_k}) at offset {offset} exceeds "
                            f"packed KV length ({total_kv_avail})"
                        )
                    if seq_k == 0:
                        outputs.append(
                            torch_mod.zeros(
                                (1, self_mod.num_heads, seq_len, self_mod.head_dim),
                                dtype=v_all.dtype,
                                device=v_all.device,
                            )
                        )
                        continue
                    k_slice = k_all[:, :, offset : offset + seq_k, :]
                    v_slice = v_all[:, :, offset : offset + seq_k, :]
                    offset += seq_k
                    outputs.append(
                        _run_sdpa(
                            q_all[b_idx : b_idx + 1],
                            k_slice,
                            v_slice,
                            scale=scale,
                        )
                    )
            out = torch_mod.cat(outputs, dim=0)

        out = out.transpose(1, 2).reshape(batch_size, seq_len, hidden)
        out = self_mod.proj(out)
        proj_drop = getattr(self_mod, "proj_drop", None)
        if callable(proj_drop):
            out = proj_drop(out)
        return out.to(out_dtype)

    self_attn_fn = (
        _accel_process_attn
        if (attn_state["use_sage3"] or attn_state["use_sage2"])
        else _sdpa_process_attn
    )
    for block in dit.blocks:
        attn_mod = getattr(block, "attn", None)
        if attn_mod is not None and hasattr(attn_mod, "_process_attn"):
            attn_mod._process_attn = types.MethodType(self_attn_fn, attn_mod)
        audio_attn_mod = getattr(block, "audio_cross_attn", None)
        if audio_attn_mod is not None and hasattr(audio_attn_mod, "_process_cross_attn"):
            audio_attn_mod._process_cross_attn = types.MethodType(
                _sdpa_audio_cross_attn,
                audio_attn_mod,
            )
        cross_mod = getattr(block, "cross_attn", None)
        if cross_mod is not None and hasattr(cross_mod, "_process_cross_attn"):
            cross_mod._process_cross_attn = types.MethodType(
                _sdpa_multihead_cross_process,
                cross_mod,
            )

    orig_dit_forward = getattr(dit, "forward", None)
    if callable(orig_dit_forward) and not getattr(
        orig_dit_forward, "_colabone_kv_cache_wrapped", False
    ):

        def _wrapped_dit_forward(*args: Any, **kwargs: Any) -> Any:
            prev_cache = getattr(kv_seqlen_cache_tls, "cache", None)
            kv_seqlen_cache_tls.cache = {}
            prev_sdpa_active = bool(getattr(sdpa_priority_tls, "active", False))
            enter_sdpa_priority = bool(use_priority_sdpa and not prev_sdpa_active)
            try:
                if enter_sdpa_priority:
                    with sdpa_kernel_fn(ordered_sdpa_backends, set_priority=True):
                        sdpa_priority_tls.active = True
                        return orig_dit_forward(*args, **kwargs)
                return orig_dit_forward(*args, **kwargs)
            finally:
                if enter_sdpa_priority:
                    sdpa_priority_tls.active = prev_sdpa_active
                kv_seqlen_cache_tls.cache = prev_cache

        _wrapped_dit_forward._colabone_kv_cache_wrapped = True  # type: ignore[attr-defined]
        dit.forward = _wrapped_dit_forward

    return attn_state


def _install_sdpa_attention_fallback(
    dit: Any,
    torch_mod: Any,
    *,
    preferred_backend: Any = None,
) -> None:
    """Patch pure BF16 SDPA fallback onto LongCat attention modules when FA3/FA2/Sage are disabled."""
    _install_accelerated_attention(
        dit,
        torch_mod,
        use_sage3=False,
        use_sage2=False,
        preferred_sdpa_backend=preferred_backend,
    )


_FP8_LINEAR_CLASS_CACHE: dict[int, type[Any]] = {}


def _get_colabone_fp8_linear_class(torch_mod: Any) -> type[Any]:
    nn_mod = torch_mod.nn
    base_cls = nn_mod.Module
    cache_key = id(base_cls)
    cached = _FP8_LINEAR_CLASS_CACHE.get(cache_key)
    if cached is not None:
        return cached

    class ColabOneFp8Linear(base_cls):  # type: ignore[misc,valid-type]
        """Pure-PyTorch FP8 E4M3 Tensor Core linear layer backed by torch._scaled_mm."""

        def __init__(
            self,
            source_linear: Any,
            *,
            late_weight_fp32: Any = None,
        ) -> None:
            super().__init__()
            self.in_features = int(source_linear.in_features)
            self.out_features = int(source_linear.out_features)

            def _quantize_weight(w_fp32: Any) -> tuple[Any, Any]:
                w_scale = (
                    w_fp32.abs().amax().clamp(min=1e-12) / 448.0
                ).to(dtype=torch_mod.float32)
                w_q = (
                    (w_fp32 / w_scale)
                    .clamp(-448.0, 448.0)
                    .to(dtype=torch_mod.float8_e4m3fn)
                    .contiguous()
                )
                return w_q, w_scale

            with torch_mod.no_grad():
                w_fp32 = source_linear.weight.detach().to(dtype=torch_mod.float32)
                w_fp8, weight_scale = _quantize_weight(w_fp32)
                if late_weight_fp32 is not None:
                    w_fp8_other, weight_scale_other = _quantize_weight(
                        late_weight_fp32.detach().to(
                            device=w_fp32.device, dtype=torch_mod.float32
                        )
                    )
                else:
                    w_fp8_other, weight_scale_other = None, None
            # Store row-major contiguous (N, K) so .t() is always zero-copy column-major
            # (K, N) with stride (1, K), surviving .to(device) cleanly for cuBLASLt _scaled_mm.
            self.register_buffer("weight_fp8", w_fp8)
            self.register_buffer("weight_scale", weight_scale)
            if w_fp8_other is not None and weight_scale_other is not None:
                self.register_buffer("weight_fp8_other", w_fp8_other)
                self.register_buffer("weight_scale_other", weight_scale_other)
            else:
                self.weight_fp8_other = None
                self.weight_scale_other = None
            if source_linear.bias is not None:
                self.register_buffer(
                    "bias",
                    source_linear.bias.detach().clone().contiguous(),
                )
            else:
                self.bias = None

        def swap_lora_mix_weights(self) -> None:
            """Zero-drift in-place swap between early and late FP8 weights (torch.compile safe)."""
            if self.weight_fp8_other is None or self.weight_scale_other is None:
                return
            with torch_mod.no_grad():
                tmp_w = self.weight_fp8.clone()
                self.weight_fp8.copy_(self.weight_fp8_other)
                self.weight_fp8_other.copy_(tmp_w)
                tmp_s = self.weight_scale.clone()
                self.weight_scale.copy_(self.weight_scale_other)
                self.weight_scale_other.copy_(tmp_s)

        @property
        def weight_fp8_t(self) -> Any:
            return self.weight_fp8.t()

        def forward(self, x: Any) -> Any:
            orig_shape = x.shape
            out_dtype = x.dtype
            x_2d = x.reshape(-1, self.in_features)
            orig_m = x_2d.shape[0]
            pad_m = (16 - (orig_m % 16)) % 16
            if pad_m:
                x_2d = torch_mod.nn.functional.pad(x_2d, (0, 0, 0, pad_m))
            scale_a = (
                x_2d.abs().amax().to(dtype=torch_mod.float32).clamp(min=1e-12) / 448.0
            )
            if x_2d.dtype == torch_mod.bfloat16:
                inv_scale_a = (1.0 / scale_a).to(dtype=torch_mod.bfloat16)
                x_fp8 = (
                    (x_2d * inv_scale_a)
                    .clamp(-448.0, 448.0)
                    .to(dtype=torch_mod.float8_e4m3fn)
                )
            else:
                x_fp8 = (
                    (x_2d.to(dtype=torch_mod.float32) * (1.0 / scale_a))
                    .clamp(-448.0, 448.0)
                    .to(dtype=torch_mod.float8_e4m3fn)
                )
            if not x_fp8.is_contiguous():
                x_fp8 = x_fp8.contiguous()

            mm_out_dtype = (
                out_dtype
                if out_dtype in (torch_mod.bfloat16, torch_mod.float16)
                else torch_mod.bfloat16
            )
            bias_arg = None
            if self.bias is not None:
                bias_arg = (
                    self.bias
                    if self.bias.dtype == mm_out_dtype
                    else self.bias.to(dtype=mm_out_dtype)
                )

            res = torch_mod._scaled_mm(
                x_fp8,
                self.weight_fp8.t(),
                scale_a=scale_a,
                scale_b=self.weight_scale,
                bias=bias_arg,
                out_dtype=mm_out_dtype,
                use_fast_accum=True,
            )
            out = res[0] if isinstance(res, tuple) else res
            if pad_m:
                out = out[:orig_m]
            if out.dtype != out_dtype:
                out = out.to(dtype=out_dtype)
            return out.reshape(*orig_shape[:-1], self.out_features)

        def extra_repr(self) -> str:
            return (
                f"in_features={self.in_features}, out_features={self.out_features}, "
                f"bias={self.bias is not None}, dtype=float8_e4m3fn"
            )

    _FP8_LINEAR_CLASS_CACHE[cache_key] = ColabOneFp8Linear
    return ColabOneFp8Linear


def _benchmark_torch_fp8_scaled_mm(
    torch_mod: Any,
    device: Any,
    *,
    require_speedup: bool = True,
) -> bool:
    """Verify accuracy and end-to-end speedup of torch._scaled_mm FP8 E4M3 vs BF16 on CUDA."""
    if not _is_cuda_device(device):
        return False
    cuda_mod = getattr(torch_mod, "cuda", None)
    if cuda_mod is None or not callable(getattr(cuda_mod, "is_available", None)):
        return False
    if not (
        callable(getattr(torch_mod, "_scaled_mm", None))
        and hasattr(torch_mod, "float8_e4m3fn")
        and callable(getattr(torch_mod, "randn", None))
    ):
        return False
    f_mod = getattr(getattr(torch_mod, "nn", None), "functional", None)
    if f_mod is None or not callable(getattr(f_mod, "linear", None)):
        return False
    try:
        if not cuda_mod.is_available():
            return False
        with torch_mod.no_grad():
            x = torch_mod.randn((4096, 4096), dtype=torch_mod.bfloat16, device=device) * 0.5
            w = torch_mod.randn((10752, 4096), dtype=torch_mod.bfloat16, device=device) * 0.02
            scale_b = (
                w.to(dtype=torch_mod.float32).abs().amax().clamp(min=1e-12) / 448.0
            ).to(dtype=torch_mod.float32)
            w_fp8 = (
                (w.to(dtype=torch_mod.float32) / scale_b)
                .clamp(-448.0, 448.0)
                .to(dtype=torch_mod.float8_e4m3fn)
                .contiguous()
            )
            w_fp8_t = w_fp8.t()

            # BF16 reference: 1 warmup + 3 timed passes.
            ref = f_mod.linear(x, w)
            cuda_mod.synchronize(device)
            t0 = time.perf_counter()
            for _ in range(3):
                ref = f_mod.linear(x, w)
            cuda_mod.synchronize(device)
            bf16_ms = (time.perf_counter() - t0) * 1000.0 / 3.0

            def _fp8_step() -> Any:
                sa = x.abs().amax().to(dtype=torch_mod.float32).clamp(min=1e-12) / 448.0
                inv_sa = (1.0 / sa).to(dtype=torch_mod.bfloat16)
                x_q = (
                    (x * inv_sa)
                    .clamp(-448.0, 448.0)
                    .to(dtype=torch_mod.float8_e4m3fn)
                )
                res = torch_mod._scaled_mm(
                    x_q,
                    w_fp8_t,
                    scale_a=sa,
                    scale_b=scale_b,
                    out_dtype=torch_mod.bfloat16,
                    use_fast_accum=True,
                )
                return res[0] if isinstance(res, tuple) else res

            # FP8 E4M3: 1 warmup + 3 timed passes (includes dynamic activation quant).
            out_fp8 = _fp8_step()
            cuda_mod.synchronize(device)
            t1 = time.perf_counter()
            for _ in range(3):
                out_fp8 = _fp8_step()
            cuda_mod.synchronize(device)
            fp8_ms = (time.perf_counter() - t1) * 1000.0 / 3.0

            if not _tensor_all_finite(torch_mod, out_fp8):
                raise RuntimeError("torch._scaled_mm produced non-finite output")
            ref_f32 = ref.to(dtype=torch_mod.float32)
            out_f32 = out_fp8.to(dtype=torch_mod.float32)
            denom = float(ref_f32.norm().clamp(min=1e-6).item())
            rel_err = float((out_f32 - ref_f32).norm().item()) / denom
            del x, w, ref, ref_f32, out_fp8, out_f32, w_fp8, w_fp8_t

        speedup = bf16_ms / max(fp8_ms, 1e-6)
        if rel_err > 0.15:
            print(
                f"[colabone-longcat16] WARNING: FP8 FFN probe relative error too high "
                f"({rel_err:.4f}); keeping pure BF16 FFN",
                file=sys.stderr,
                flush=True,
            )
            return False
        if require_speedup and fp8_ms > bf16_ms * 0.95:
            print(
                f"[colabone-longcat16] INFO: FP8 FFN probe not faster than BF16 "
                f"(bf16={bf16_ms:.2f}ms, fp8={fp8_ms:.2f}ms, speedup={speedup:.2f}x); "
                "keeping pure BF16 FFN",
                file=sys.stderr,
                flush=True,
            )
            return False
        print(
            f"[colabone-longcat16] INFO: FP8 FFN probe passed "
            f"(bf16={bf16_ms:.2f}ms, fp8={fp8_ms:.2f}ms, speedup={speedup:.2f}x, "
            f"rel_err={rel_err:.4f})",
            file=sys.stderr,
            flush=True,
        )
        return True
    except Exception as exc:
        print(
            f"[colabone-longcat16] WARNING: FP8 FFN probe failed ({exc}); "
            "keeping pure BF16 FFN",
            file=sys.stderr,
            flush=True,
        )
        return False


def _enable_longcat16_fp8_ffn(
    dit: Any,
    torch_mod: Any,
    device: Any,
    *,
    major: int = 0,
    minor: int = 0,
    fp8_ffn_opt: str | bool = "auto",
    skip_module_names: set[str] | None = None,
    will_compile_dit: bool = False,
) -> bool:
    """Enable calibrated FP8 Tensor Core FFN on sm_89+ for blocks 6 and 9..47 (ffn.w1, ffn.w3)."""
    if fp8_ffn_opt is False:
        return False
    supports_fp8 = (int(major) > 8) or (int(major) == 8 and int(minor) >= 9)
    if not supports_fp8:
        if fp8_ffn_opt is True:
            print(
                f"[colabone-longcat16] INFO: GPU sm_{major}{minor} lacks FP8 Tensor Cores; "
                "keeping pure BF16 FFN",
                file=sys.stderr,
                flush=True,
            )
        return False

    lora_mix_cfg = getattr(dit, "lora_mix_config", None)
    if lora_mix_cfg and not (
        callable(getattr(lora_mix_cfg, "get", None))
        and lora_mix_cfg.get("fast_weight_swap")
    ):
        return False

    blocks = getattr(dit, "blocks", None)
    if not blocks or len(blocks) <= COLABONE_LONGCAT16_FP8_FFN_BLOCKS[-1]:
        return False

    nn_mod = getattr(torch_mod, "nn", None)
    linear_cls = getattr(nn_mod, "Linear", None)
    if not isinstance(linear_cls, type) or not isinstance(getattr(nn_mod, "Module", None), type):
        return False

    skip_set = set(skip_module_names or ())
    fast_swap_entries = getattr(dit, "_colabone_fast_lora_swap_entries", None)
    compute_late_w_fn = getattr(dit, "_colabone_compute_late_weight_fp32", None)
    can_fp8_dual_swap = isinstance(fast_swap_entries, dict) and callable(
        compute_late_w_fn
    )

    def _is_layer_eligible(block_id: int, attr: str, layer: Any) -> bool:
        mod_name = f"blocks.{block_id}.ffn.{attr}"
        if hasattr(layer, "org_forward"):
            return False
        if mod_name in skip_set and not (
            can_fp8_dual_swap and mod_name in fast_swap_entries
        ):
            return False
        if not isinstance(layer, linear_cls):
            return False
        in_f = int(getattr(layer, "in_features", 0) or 0)
        out_f = int(getattr(layer, "out_features", 0) or 0)
        if in_f <= 0 or out_f <= 0 or (in_f % 16 != 0) or (out_f % 16 != 0):
            return False
        weight = getattr(layer, "weight", None)
        return weight is not None and getattr(weight, "dtype", None) in (
            torch_mod.bfloat16,
            torch_mod.float16,
        )

    eligible_targets: list[tuple[int, str, str, Any]] = []
    has_dual_swap_targets = False
    for block_id in COLABONE_LONGCAT16_FP8_FFN_BLOCKS:
        ffn = getattr(blocks[block_id], "ffn", None)
        if ffn is None:
            continue
        for attr in ("w1", "w3"):
            layer = getattr(ffn, attr, None)
            if layer is not None and _is_layer_eligible(block_id, attr, layer):
                mod_name = f"blocks.{block_id}.ffn.{attr}"
                if can_fp8_dual_swap and mod_name in fast_swap_entries:
                    has_dual_swap_targets = True
                eligible_targets.append((block_id, attr, mod_name, layer))

    if not eligible_targets:
        return False

    # Prefer official Transformer Engine path if TE is installed, torch.compile is disabled,
    # and no dual-LoRA swap is attached. Smoke-test TE FP8 autocast on a dummy layer BEFORE
    # mutating dit so incompatible TE/CUDA builds cleanly fall through to ColabOneFp8Linear.
    if (
        not will_compile_dit
        and not has_dual_swap_targets
        and len(eligible_targets) == len(COLABONE_LONGCAT16_FP8_FFN_BLOCKS) * 2
    ):
        try:
            import transformer_engine.pytorch as _te_mod  # type: ignore[import-not-found]
            from longcat_video.modules.quantization import (  # type: ignore[import-not-found]
                enable_fp8_ffn as _upstream_enable_fp8_ffn,
            )

            autocast_cm = getattr(_te_mod, "fp8_autocast", None) or getattr(
                _te_mod, "autocast", None
            )
            if not callable(autocast_cm):
                raise RuntimeError(
                    "Transformer Engine module lacks callable fp8_autocast/autocast"
                )

            with torch_mod.no_grad():
                try:
                    probe_lin = _te_mod.Linear(
                        64,
                        64,
                        params_dtype=torch_mod.bfloat16,
                        device=device,
                    )
                except TypeError:
                    probe_lin = _te_mod.Linear(64, 64).to(
                        device=device, dtype=torch_mod.bfloat16
                    )
                probe_x = torch_mod.randn(
                    (64, 64), device=device, dtype=torch_mod.bfloat16
                )
                with autocast_cm(enabled=True):
                    probe_y = probe_lin(probe_x)
                if not _tensor_all_finite(torch_mod, probe_y):
                    raise RuntimeError(
                        "Transformer Engine FP8 probe produced non-finite output"
                    )
                cuda_mod = getattr(torch_mod, "cuda", None)
                sync_fn = (
                    getattr(cuda_mod, "synchronize", None)
                    if cuda_mod is not None
                    else None
                )
                if callable(sync_fn) and _is_cuda_device(device):
                    sync_fn(device)
                del probe_lin, probe_x, probe_y

            _upstream_enable_fp8_ffn(dit, block_ids=COLABONE_LONGCAT16_FP8_FFN_BLOCKS)
            print(
                f"[colabone-longcat16] INFO: enabled Transformer Engine FP8 FFN on "
                f"{len(COLABONE_LONGCAT16_FP8_FFN_BLOCKS)} calibrated blocks",
                file=sys.stderr,
                flush=True,
            )
            return True
        except Exception as exc:
            print(
                f"[colabone-longcat16] INFO: Transformer Engine FP8 FFN unavailable ({exc}); "
                "falling back to native PyTorch FP8 probe",
                file=sys.stderr,
                flush=True,
            )

    require_speedup = fp8_ffn_opt is not True
    try:
        fp8_probe_ok = _benchmark_torch_fp8_scaled_mm(
            torch_mod,
            device,
            require_speedup=require_speedup,
        )
    except TypeError:
        fp8_probe_ok = _benchmark_torch_fp8_scaled_mm(torch_mod, device)
    if not fp8_probe_ok:
        return False

    fp8_cls = _get_colabone_fp8_linear_class(torch_mod)
    replaced = 0
    dual_upgraded = 0
    for block_id, attr, mod_name, layer in eligible_targets:
        ffn = blocks[block_id].ffn
        late_w_fp32 = None
        is_dual = can_fp8_dual_swap and mod_name in fast_swap_entries
        if is_dual:
            layer_dev = getattr(getattr(layer, "weight", None), "device", None)
            try:
                late_w_fp32 = compute_late_w_fn(mod_name, target_device=layer_dev)
            except TypeError:
                late_w_fp32 = compute_late_w_fn(mod_name)
        fp8_layer = fp8_cls(layer, late_weight_fp32=late_w_fp32)
        setattr(ffn, attr, fp8_layer)
        if is_dual:
            fast_swap_entries[mod_name] = {
                "kind": "fp8_dual",
                "module": fp8_layer,
            }
            dual_upgraded += 1
        replaced += 1

    if replaced > 0:
        dual_note = (
            f" (including {dual_upgraded} dual-weight LoRA-mix FFN layers)"
            if dual_upgraded > 0
            else ""
        )
        print(
            f"[colabone-longcat16] INFO: enabled native PyTorch FP8 E4M3 FFN on "
            f"{replaced} linear layers across calibrated blocks{dual_note}",
            file=sys.stderr,
            flush=True,
        )
        return True
    return False


class _ColabOneLoRaSwapUnrecoverableError(RuntimeError):
    """Raised when Phase 2 in-place LoRA merge fails and rollback cannot restore base weights."""


def _install_fast_lora_mix_weight_swap(
    dit: Any,
    dmd_path: str,
    opsd_path: str,
    mix_alpha: float,
    mix_steps: int,
    torch_mod: Any,
    *,
    lora_network_dim: int = 128,
    lora_network_alpha: int = 64,
) -> set[str]:
    """Replace slow per-step Python LoRA hooks with zero-overhead static weight delta swaps.

    Pre-merges early weights W_early = W_0 + alpha * d_dmd + (1 - alpha) * d_opsd in-place and
    patches dit.set_lora_mix_step(step) to apply W_late - W_early ONCE at the mix_steps boundary.
    Compatible with torch.compile (weight storage pointers never change). Raises on any
    mismatch so callers cleanly fall back to upstream dit.load_lora + dit.enable_lora_mix.
    """
    from safetensors.torch import load_file  # type: ignore[import-not-found]

    alpha = float(mix_alpha)
    steps_boundary = int(mix_steps)
    if not 0.0 <= alpha <= 1.0:
        raise ValueError(f"mix_alpha must be in [0, 1], got {alpha}")

    dmd_sd = load_file(dmd_path, device="cpu")
    opsd_sd = load_file(opsd_path, device="cpu")
    default_alpha_scale = float(lora_network_alpha) / float(lora_network_dim)

    def _extract_entries(sd: dict[str, Any]) -> dict[str, dict[str, Any]]:
        entries: dict[str, dict[str, Any]] = {}
        for key, down_t in sd.items():
            if not key.endswith(".lora_down.weight"):
                continue
            lora_name = key[: -len(".lora_down.weight")]
            module_name = lora_name.replace("lora___lorahyphen___", "").replace(
                "___lorahyphen___", "."
            )
            up_key = f"{lora_name}.lora_up.weight"
            block_prefix = f"{lora_name}.lora_up.blocks."
            up_tensor: Any = None
            up_blocks: list[Any] | None = None
            if up_key in sd:
                up_tensor = sd[up_key]
            else:
                block_keys = [
                    k for k in sd if k.startswith(block_prefix) and k.endswith(".weight")
                ]
                if not block_keys:
                    raise KeyError(f"Missing lora_up weights for {lora_name}")
                up_blocks = []
                for idx in range(len(block_keys)):
                    bk = f"{block_prefix}{idx}.weight"
                    if bk not in sd:
                        raise KeyError(f"Missing parallel LoRA block {bk}")
                    up_blocks.append(sd[bk])
            alpha_key = f"{lora_name}.alpha_scale"
            if alpha_key in sd:
                a_scale = float(sd[alpha_key].detach().to(dtype=torch_mod.float32).item())
            else:
                a_scale = default_alpha_scale
            entries[module_name] = {
                "down": down_t,
                "up": up_tensor,
                "up_blocks": up_blocks,
                "alpha_scale": a_scale,
            }
        return entries

    dmd_entries = _extract_entries(dmd_sd)
    opsd_entries = _extract_entries(opsd_sd)
    all_names = sorted(set(dmd_entries.keys()) | set(opsd_entries.keys()))
    if not all_names:
        raise RuntimeError("No LoRA modules found in distillation mix checkpoints")

    def _resolve_module(name: str) -> Any:
        getter = getattr(dit, "_get_module_by_name", None)
        if callable(getter):
            return getter(name)
        mod = dit
        for part in name.split("."):
            mod = getattr(mod, part)
        return mod

    # Validate every target Linear module and tensor shape BEFORE mutating any weight.
    targets: list[tuple[str, Any]] = []
    total_dense_bytes = 0
    for name in all_names:
        mod = _resolve_module(name)
        if mod.__class__.__name__ != "Linear":
            raise TypeError(
                f"Fast LoRA swap target must be Linear, got {mod.__class__.__name__}: {name}"
            )
        weight = getattr(mod, "weight", None)
        if weight is None or getattr(weight, "ndim", 0) != 2:
            raise TypeError(f"Target module {name} lacks a 2D weight tensor")
        out_f, in_f = int(weight.shape[0]), int(weight.shape[1])
        for label, entry in (("dmd", dmd_entries.get(name)), ("opsd", opsd_entries.get(name))):
            if entry is None:
                continue
            if int(entry["down"].shape[1]) != in_f:
                raise ValueError(
                    f"{label} LoRA down in_features mismatch on {name}: "
                    f"{tuple(entry['down'].shape)} vs weight {tuple(weight.shape)}"
                )
            if entry["up_blocks"] is not None:
                up_out = sum(int(blk.shape[0]) for blk in entry["up_blocks"])
            else:
                up_out = int(entry["up"].shape[0])
            if up_out != out_f:
                raise ValueError(
                    f"{label} LoRA up out_features mismatch on {name}: "
                    f"{up_out} vs weight {tuple(weight.shape)}"
                )
        total_dense_bytes += int(weight.numel()) * int(weight.element_size())
        targets.append((name, mod))

    ref_device = targets[0][1].weight.device
    free_vram_bytes = 0
    cuda_mod = getattr(torch_mod, "cuda", None)
    mem_get_info = getattr(cuda_mod, "mem_get_info", None) if cuda_mod is not None else None
    if callable(mem_get_info) and _is_cuda_device(ref_device):
        try:
            free_vram_bytes = int(mem_get_info(ref_device)[0])
        except Exception:
            free_vram_bytes = 0

    avail_ram_bytes = _detect_available_ram_bytes()
    gib = 1024 * 1024 * 1024
    # Tiered storage for zero-drift dual-weight LoRA swap:
    # - dense_gpu: bit-exact late weights on GPU when VRAM headroom >= 30 GiB (e.g. G4 96GB)
    #   or compact offline models (<= 8 GiB with no CUDA telemetry).
    # - dense_cpu: bit-exact late weights in host RAM when >= 32 GiB RAM headroom remains
    #   (zero BF16 drift on 80 GB GPUs; ~7 GiB FFN entries are freed once FP8 FFN upgrades them).
    # - compact_lowrank: ~1.2 GiB low-rank GPU factors when both VRAM and host RAM are tight.
    if alpha <= 0.0:
        swap_mode = "static_only"
    elif (free_vram_bytes == 0 and total_dense_bytes <= 8 * gib) or (
        free_vram_bytes > 0 and (free_vram_bytes - total_dense_bytes) >= 30 * gib
    ):
        swap_mode = "dense_gpu"
    elif (
        _is_cuda_device(ref_device)
        and avail_ram_bytes > 0
        and (avail_ram_bytes - total_dense_bytes) >= 32 * gib
    ):
        swap_mode = "dense_cpu"
    else:
        swap_mode = "compact_lowrank"

    def _compute_delta_fp32(entry: dict[str, Any], dev: Any) -> Any:
        down = entry["down"].to(device=dev, dtype=torch_mod.float32)
        if entry["up_blocks"] is not None:
            down_chunks = torch_mod.chunk(down, len(entry["up_blocks"]), dim=0)
            delta = torch_mod.cat(
                [
                    torch_mod.mm(
                        blk.to(device=dev, dtype=torch_mod.float32),
                        down_chunks[idx],
                    )
                    for idx, blk in enumerate(entry["up_blocks"])
                ],
                dim=0,
            )
        else:
            delta = torch_mod.mm(
                entry["up"].to(device=dev, dtype=torch_mod.float32),
                down,
            )
        return delta * float(entry["alpha_scale"])

    def _entry_to_gpu_compact(entry: dict[str, Any] | None, dev: Any, dt: Any) -> dict[str, Any] | None:
        if entry is None:
            return None
        return {
            "down": entry["down"].to(device=dev, dtype=dt).contiguous(),
            "up": (
                entry["up"].to(device=dev, dtype=dt).contiguous()
                if entry["up"] is not None
                else None
            ),
            "up_blocks": (
                [blk.to(device=dev, dtype=dt).contiguous() for blk in entry["up_blocks"]]
                if entry["up_blocks"] is not None
                else None
            ),
            "alpha_scale": float(entry["alpha_scale"]),
        }

    swap_entries: dict[str, dict[str, Any]] = {}
    with torch_mod.no_grad():
        # Phase 1 (read-only on mod.weight): allocate all persistent swap buffers BEFORE
        # mutating any model weight so an OOM leaves dit completely untouched for fallback.
        try:
            if swap_mode != "static_only":
                for name, mod in targets:
                    orig_dtype = mod.weight.dtype
                    dev = mod.weight.device
                    dmd_entry = dmd_entries.get(name)
                    opsd_entry = opsd_entries.get(name)
                    if swap_mode in ("dense_gpu", "dense_cpu"):
                        d_opsd = (
                            _compute_delta_fp32(opsd_entry, dev)
                            if opsd_entry is not None
                            else None
                        )
                        if d_opsd is not None:
                            w_fp32 = mod.weight.data.to(dtype=torch_mod.float32)
                            w_late = (w_fp32 + d_opsd).to(dtype=orig_dtype)
                            del w_fp32, d_opsd
                        else:
                            w_late = mod.weight.data.clone()
                        if swap_mode == "dense_gpu":
                            swap_entries[name] = {
                                "kind": "dense",
                                "module": mod,
                                "other": w_late.contiguous(),
                            }
                        else:
                            w_late_cpu = w_late.to(device="cpu", copy=True).contiguous()
                            del w_late
                            swap_entries[name] = {
                                "kind": "dense_cpu",
                                "module": mod,
                                "other": w_late_cpu,
                            }
                    else:
                        swap_entries[name] = {
                            "kind": "lowrank",
                            "module": mod,
                            "dmd": _entry_to_gpu_compact(dmd_entry, dev, orig_dtype),
                            "opsd": _entry_to_gpu_compact(opsd_entry, dev, orig_dtype),
                        }
        except BaseException:
            swap_entries.clear()
            raise

        # Phase 2 (commit with rollback guard): merge W_early in-place with zero persistent
        # allocations; roll back already-mutated layers if anything fails unexpectedly.
        mutated_layers: list[
            tuple[str, Any, dict[str, Any] | None, dict[str, Any] | None]
        ] = []
        try:
            for name, mod in targets:
                orig_dtype = mod.weight.dtype
                dev = mod.weight.device
                dmd_entry = dmd_entries.get(name)
                opsd_entry = opsd_entries.get(name)
                d_dmd = (
                    _compute_delta_fp32(dmd_entry, dev)
                    if (dmd_entry is not None and alpha > 0.0)
                    else None
                )
                d_opsd = (
                    _compute_delta_fp32(opsd_entry, dev)
                    if (opsd_entry is not None and (1.0 - alpha) > 0.0)
                    else None
                )

                delta_early = None
                if d_dmd is not None:
                    delta_early = alpha * d_dmd
                if d_opsd is not None:
                    opsd_part = (1.0 - alpha) * d_opsd
                    delta_early = (
                        opsd_part if delta_early is None else (delta_early + opsd_part)
                    )
                if delta_early is not None:
                    w_fp32 = mod.weight.data.to(dtype=torch_mod.float32)
                    w_early = (w_fp32 + delta_early).to(dtype=orig_dtype)
                    mod.weight.data.copy_(w_early)
                    mutated_layers.append((name, mod, dmd_entry, opsd_entry))
                    del w_fp32, w_early
                del d_dmd, d_opsd, delta_early
        except BaseException as phase2_exc:
            if swap_mode in ("compact_lowrank", "lowrank", "static_only"):
                swap_entries.clear()
            elif swap_mode in ("dense_gpu", "dense_cpu"):
                mutated_names = {n for n, _, _, _ in mutated_layers}
                for stale_name in [
                    k for k in list(swap_entries.keys()) if k not in mutated_names
                ]:
                    swap_entries.pop(stale_name, None)
            if (
                cuda_mod is not None
                and callable(getattr(cuda_mod, "is_available", None))
                and cuda_mod.is_available()
            ):
                _torch_gc(torch_mod)

            def _restore_layer_on_device(
                mod: Any,
                entry: dict[str, Any] | None,
                dmd_entry: dict[str, Any] | None,
                opsd_entry: dict[str, Any] | None,
                compute_dev: Any,
            ) -> None:
                orig_dtype = mod.weight.dtype
                if entry is not None and entry["kind"] in ("dense", "dense_cpu"):
                    w_base_fp32 = entry["other"].to(
                        device=compute_dev, dtype=torch_mod.float32
                    )
                    if opsd_entry is not None:
                        w_base_fp32 = w_base_fp32 - _compute_delta_fp32(
                            opsd_entry, compute_dev
                        )
                    w_restored = w_base_fp32.to(dtype=orig_dtype)
                    mod.weight.data.copy_(w_restored)
                    del w_base_fp32, w_restored
                    return
                d_dmd_rb = (
                    _compute_delta_fp32(dmd_entry, compute_dev)
                    if (dmd_entry is not None and alpha > 0.0)
                    else None
                )
                d_opsd_rb = (
                    _compute_delta_fp32(opsd_entry, compute_dev)
                    if (opsd_entry is not None and (1.0 - alpha) > 0.0)
                    else None
                )
                delta_early_rb = None
                if d_dmd_rb is not None:
                    delta_early_rb = alpha * d_dmd_rb
                if d_opsd_rb is not None:
                    opsd_part_rb = (1.0 - alpha) * d_opsd_rb
                    delta_early_rb = (
                        opsd_part_rb
                        if delta_early_rb is None
                        else (delta_early_rb + opsd_part_rb)
                    )
                if delta_early_rb is not None:
                    w_cur_fp32 = mod.weight.data.to(
                        device=compute_dev, dtype=torch_mod.float32
                    )
                    w_restored = (w_cur_fp32 - delta_early_rb).to(dtype=orig_dtype)
                    mod.weight.data.copy_(w_restored)
                    del w_cur_fp32, w_restored
                del d_dmd_rb, d_opsd_rb, delta_early_rb

            rollback_failed = False
            failed_layers: list[str] = []
            for name, mod, dmd_entry, opsd_entry in reversed(mutated_layers):
                entry = swap_entries.pop(name, None)
                dev = mod.weight.device
                restored = False
                try:
                    _restore_layer_on_device(mod, entry, dmd_entry, opsd_entry, dev)
                    restored = True
                except Exception:
                    if str(dev).lower() != "cpu":
                        try:
                            if (
                                cuda_mod is not None
                                and callable(getattr(cuda_mod, "is_available", None))
                                and cuda_mod.is_available()
                            ):
                                _torch_gc(torch_mod)
                            _restore_layer_on_device(
                                mod, entry, dmd_entry, opsd_entry, "cpu"
                            )
                            restored = True
                        except Exception:
                            restored = False
                del entry
                if not restored:
                    rollback_failed = True
                    failed_layers.append(name)
            swap_entries.clear()
            if rollback_failed:
                raise _ColabOneLoRaSwapUnrecoverableError(
                    f"Fast LoRA swap failed during Phase 2 commit ({phase2_exc}) and "
                    f"could not restore {len(failed_layers)} mutated layer(s) "
                    f"(e.g. {', '.join(failed_layers[:3])})"
                ) from phase2_exc
            raise

    dmd_entries.clear()
    opsd_entries.clear()
    del dmd_sd, opsd_sd

    def _compute_late_weight_fp32(
        mod_name: str,
        target_device: Any = None,
    ) -> Any:
        entry = swap_entries.get(mod_name)
        if entry is None:
            return None
        with torch_mod.no_grad():
            if entry["kind"] == "dense_cpu":
                other_t = entry["other"].detach()
                if target_device is not None:
                    return other_t.to(device=target_device).to(dtype=torch_mod.float32)
                return other_t.to(dtype=torch_mod.float32)
            if entry["kind"] == "dense":
                other_t = entry["other"].detach()
                if target_device is not None:
                    return other_t.to(device=target_device, dtype=torch_mod.float32)
                return other_t.to(dtype=torch_mod.float32)
            if entry["kind"] == "lowrank":
                mod = entry["module"]
                dev = mod.weight.device
                w_early_fp32 = mod.weight.data.to(dtype=torch_mod.float32)
                d_late = (
                    _compute_delta_fp32(entry["opsd"], dev)
                    if entry["opsd"] is not None
                    else None
                )
                d_early_dmd = (
                    _compute_delta_fp32(entry["dmd"], dev)
                    if entry["dmd"] is not None
                    else None
                )
                if d_late is not None and d_early_dmd is not None:
                    return w_early_fp32 + alpha * (d_late - d_early_dmd)
                if d_late is not None:
                    return w_early_fp32 + alpha * d_late
                if d_early_dmd is not None:
                    return w_early_fp32 - alpha * d_early_dmd
                return w_early_fp32
        return None

    dit._colabone_fast_lora_swap_entries = swap_entries
    dit._colabone_compute_late_weight_fp32 = _compute_late_weight_fp32

    dit.lora_mix_config = {
        "dmd_key": "dmd",
        "opsd_key": "opsd",
        "mix_alpha": alpha,
        "mix_steps": steps_boundary,
        "fast_weight_swap": True,
    }
    dit._lora_mix_active = True

    def _fast_set_lora_mix_step(self_dit: Any, step_index: int) -> None:
        cfg_mix = getattr(self_dit, "lora_mix_config", None)
        if cfg_mix is None:
            return
        is_mixed = int(step_index) < int(cfg_mix["mix_steps"])
        if is_mixed == getattr(self_dit, "_lora_mix_active", None):
            return
        if not swap_entries:
            self_dit._lora_mix_active = is_mixed
            return
        with torch_mod.no_grad():
            for entry in swap_entries.values():
                kind = entry["kind"]
                if kind == "fp8_dual":
                    entry["module"].swap_lora_mix_weights()
                elif kind == "dense":
                    mod = entry["module"]
                    w_other = entry["other"]
                    tmp = mod.weight.data.clone()
                    mod.weight.data.copy_(w_other)
                    entry["other"] = tmp
                elif kind == "dense_cpu":
                    mod = entry["module"]
                    w_other_cpu = entry["other"]
                    tmp_cpu = mod.weight.data.to(device="cpu", copy=True).contiguous()
                    mod.weight.data.copy_(w_other_cpu, non_blocking=False)
                    entry["other"] = tmp_cpu
                elif kind == "lowrank":
                    mod = entry["module"]
                    orig_dtype = mod.weight.dtype
                    dev = mod.weight.device
                    d_late = (
                        _compute_delta_fp32(entry["opsd"], dev)
                        if entry["opsd"] is not None
                        else None
                    )
                    d_early_dmd = (
                        _compute_delta_fp32(entry["dmd"], dev)
                        if entry["dmd"] is not None
                        else None
                    )
                    if d_late is not None and d_early_dmd is not None:
                        diff_fp32 = alpha * (d_late - d_early_dmd)
                    elif d_late is not None:
                        diff_fp32 = alpha * d_late
                    elif d_early_dmd is not None:
                        diff_fp32 = -alpha * d_early_dmd
                    else:
                        continue
                    diff_cast = diff_fp32.to(dtype=orig_dtype)
                    if is_mixed:
                        mod.weight.data.sub_(diff_cast)
                    else:
                        mod.weight.data.add_(diff_cast)
        self_dit._lora_mix_active = is_mixed

    dit.set_lora_mix_step = types.MethodType(_fast_set_lora_mix_step, dit)
    print(
        f"[colabone-longcat16] INFO: enabled static dual-weight LoRA swap for "
        f"{len(targets)} layers (mode={swap_mode}, "
        f"mix_steps={steps_boundary}, mix_alpha={alpha})",
        file=sys.stderr,
        flush=True,
    )
    return set(swap_entries.keys())


def _install_batched_cfg_runner(
    pipe: Any,
    torch_mod: Any,
) -> dict[str, Any] | None:
    """Fuse LongCat 1.6 single-speaker dual-CFG passes (batch=2 + batch=1) into one batch=3 pass.

    Reads the 27 GiB DiT weights once per denoising step instead of twice (~30-40% faster
    with bit-identical CFG math). Only activates on real LongCatVideoAvatarPipeline instances,
    and automatically restores upstream generate_ai2v/generate_avc on CUDA OOM or runtime error.
    """
    required_attrs = (
        "generate_ai2v",
        "generate_avc",
        "encode_prompt",
        "get_timesteps_sigmas",
        "prepare_latents",
        "check_inputs",
        "get_condition_shape",
        "normalize_latents",
        "denormalize_latents",
        "_decode_video_latents",
        "_cache_clean_latents",
        "_get_kv_cache_dict",
        "scheduler",
        "video_processor",
        "vae",
        "dit",
    )
    for attr in required_attrs:
        if not hasattr(pipe, attr):
            return None
    if getattr(pipe, "video_processor", None) is None or getattr(pipe, "scheduler", None) is None:
        return None
    for callable_attr in (
        "generate_ai2v",
        "generate_avc",
        "encode_prompt",
        "get_timesteps_sigmas",
        "prepare_latents",
        "check_inputs",
        "get_condition_shape",
        "normalize_latents",
        "denormalize_latents",
        "_decode_video_latents",
        "_cache_clean_latents",
        "_get_kv_cache_dict",
    ):
        if not callable(getattr(pipe, callable_attr, None)):
            return None

    try:
        import numpy as np
        from longcat_video.context_parallel import (  # type: ignore[import-not-found]
            context_parallel_util,
        )
        from longcat_video.pipeline_longcat_video_avatar import (  # type: ignore[import-not-found]
            retrieve_latents,
        )
    except Exception:
        return None

    orig_generate_ai2v = pipe.generate_ai2v
    orig_generate_avc = pipe.generate_avc
    state: dict[str, Any] = {"active": True}

    def _restore_upstream() -> None:
        state["active"] = False
        pipe.generate_ai2v = orig_generate_ai2v
        pipe.generate_avc = orig_generate_avc

    def _make_progress_bar(total: int) -> Any:
        pipe_mod = sys.modules.get(getattr(type(pipe), "__module__", ""))
        mod_tqdm = getattr(pipe_mod, "tqdm", None) if pipe_mod is not None else None
        if callable(mod_tqdm):
            return mod_tqdm(total=total, desc="Denoising")
        pb_fn = getattr(pipe, "progress_bar", None)
        if callable(pb_fn):
            return pb_fn(total=total)

        class _NoopBar:
            def __enter__(self) -> "_NoopBar":
                return self

            def __exit__(self, *args: Any) -> bool:
                return False

            def update(self, n: int = 1) -> None:
                _ = n

        return _NoopBar()

    def _batched_generate_ai2v(
        image: Any,
        prompt: Any = None,
        negative_prompt: Any = None,
        resolution: str = "480p",
        num_frames: int = 93,
        num_inference_steps: int = 50,
        use_distill: bool = False,
        text_guidance_scale: float = 4.0,
        audio_guidance_scale: float = 4.0,
        num_videos_per_prompt: int | None = 1,
        generator: Any = None,
        latents: Any = None,
        output_type: str | None = "np",
        attention_kwargs: dict[str, Any] | None = None,
        max_sequence_length: int = 512,
        audio_emb: Any = None,
        ref_target_masks: Any = None,
        resize_mode: str | None = "crop",
    ) -> Any:
        call_kwargs = dict(
            image=image,
            prompt=prompt,
            negative_prompt=negative_prompt,
            resolution=resolution,
            num_frames=num_frames,
            num_inference_steps=num_inference_steps,
            use_distill=use_distill,
            text_guidance_scale=text_guidance_scale,
            audio_guidance_scale=audio_guidance_scale,
            num_videos_per_prompt=num_videos_per_prompt,
            generator=generator,
            latents=latents,
            output_type=output_type,
            attention_kwargs=attention_kwargs,
            max_sequence_length=max_sequence_length,
            audio_emb=audio_emb,
            ref_target_masks=ref_target_masks,
            resize_mode=resize_mode,
        )
        batch_size = 1 if isinstance(prompt, str) else (len(prompt) if prompt is not None else 0)
        do_cfg = float(text_guidance_scale) > 1.0 or float(audio_guidance_scale) > 1.0
        audio_emb_shape = getattr(audio_emb, "shape", None)
        audio_emb_b1 = bool(
            audio_emb is not None
            and audio_emb_shape is not None
            and len(audio_emb_shape) >= 1
            and int(audio_emb_shape[0]) == 1
        )
        if (
            not state["active"]
            or not do_cfg
            or not audio_emb_b1
            or ref_target_masks is not None
            or batch_size != 1
            or (num_videos_per_prompt or 1) != 1
            or output_type not in (None, "np", "both", "latent")
            or context_parallel_util.get_cp_size() > 1
        ):
            return orig_generate_ai2v(**call_kwargs)

        gen_state = (
            generator.get_state()
            if generator is not None and callable(getattr(generator, "get_state", None))
            else None
        )
        fallback_err: str | None = None
        try:
            with torch_mod.no_grad():
                scale_factor_spatial = pipe.vae_scale_factor_spatial * 2
                if pipe.dit.cp_split_hw is not None:
                    scale_factor_spatial *= max(pipe.dit.cp_split_hw)
                height, width = pipe.get_condition_shape(
                    image, resolution, scale_factor_spatial=scale_factor_spatial
                )
                pipe.check_inputs(
                    prompt, negative_prompt, height, width, scale_factor_spatial
                )
                if resize_mode not in ("default", "crop"):
                    raise ValueError(f"Unsupported resize_mode {resize_mode}")

                eff_frames = num_frames
                if eff_frames % pipe.vae_scale_factor_temporal != 1:
                    eff_frames = (
                        eff_frames // pipe.vae_scale_factor_temporal
                    ) * pipe.vae_scale_factor_temporal + 1
                eff_frames = max(eff_frames, 1)

                pipe._text_guidance_scale = text_guidance_scale
                pipe._audio_guidance_scale = audio_guidance_scale
                pipe._attention_kwargs = attention_kwargs
                pipe._current_timestep = None
                pipe._interrupt = False

                device = pipe.device
                dit_dtype = pipe.dit.dtype

                (
                    prompt_embeds,
                    prompt_attention_mask,
                    negative_prompt_embeds,
                    negative_prompt_attention_mask,
                ) = pipe.encode_prompt(
                    prompt=prompt,
                    negative_prompt=negative_prompt,
                    do_classifier_free_guidance=True,
                    num_videos_per_prompt=1,
                    max_sequence_length=max_sequence_length,
                    dtype=dit_dtype,
                    device=device,
                )

                # Batch=3 order: [neg_text + zero_audio, neg_text + audio, pos_text + audio]
                prompt_embeds_3 = torch_mod.cat(
                    [negative_prompt_embeds, negative_prompt_embeds, prompt_embeds], dim=0
                )
                prompt_mask_3 = torch_mod.cat(
                    [
                        negative_prompt_attention_mask,
                        negative_prompt_attention_mask,
                        prompt_attention_mask,
                    ],
                    dim=0,
                )
                audio_uncond = torch_mod.zeros_like(audio_emb)
                audio_embs_3 = torch_mod.cat([audio_uncond, audio_emb, audio_emb], dim=0)

                sigmas = pipe.get_timesteps_sigmas(
                    num_inference_steps, use_distill=use_distill
                )
                pipe.scheduler.set_timesteps(
                    num_inference_steps, sigmas=sigmas, device=device
                )
                timesteps = pipe.scheduler.timesteps

                img_tensor = pipe.video_processor.preprocess(
                    image, height=height, width=width, resize_mode=resize_mode
                )
                img_tensor = img_tensor.to(device=device, dtype=prompt_embeds_3.dtype)

                cur_latents = pipe.prepare_latents(
                    image=img_tensor,
                    batch_size=1,
                    num_channels_latents=pipe.dit.config.in_channels,
                    height=height,
                    width=width,
                    num_frames=eff_frames,
                    num_cond_frames=1,
                    dtype=torch_mod.float32,
                    device=device,
                    generator=generator,
                    latents=latents,
                )

                with _make_progress_bar(len(timesteps)) as progress_bar:
                    for i, t in enumerate(timesteps):
                        if pipe.interrupt:
                            continue
                        if getattr(pipe.dit, "lora_mix_config", None) is not None:
                            pipe.dit.set_lora_mix_step(i)
                        pipe._current_timestep = t

                        latent_model_input = torch_mod.cat(
                            [cur_latents, cur_latents, cur_latents], dim=0
                        ).to(dit_dtype)
                        timestep_3 = t.expand(latent_model_input.shape[0]).to(dit_dtype)
                        timestep_3 = timestep_3.unsqueeze(-1).repeat(
                            1, latent_model_input.shape[2]
                        )
                        timestep_3[:, :1] = 0

                        noise_pred_3 = pipe.dit(
                            hidden_states=latent_model_input,
                            timestep=timestep_3,
                            encoder_hidden_states=prompt_embeds_3,
                            encoder_attention_mask=prompt_mask_3,
                            num_cond_latents=1,
                            audio_embs=audio_embs_3,
                            ref_target_masks=None,
                        )
                        noise_pred_uncond, noise_pred_uncond_text, noise_pred_cond = (
                            noise_pred_3.chunk(3, dim=0)
                        )
                        noise_pred = (
                            noise_pred_uncond
                            + text_guidance_scale
                            * (noise_pred_cond - noise_pred_uncond_text)
                            + audio_guidance_scale
                            * (noise_pred_uncond_text - noise_pred_uncond)
                        )
                        noise_pred = -noise_pred
                        cur_latents[:, :, 1:] = pipe.scheduler.step(
                            noise_pred[:, :, 1:],
                            t,
                            cur_latents[:, :, 1:],
                            return_dict=False,
                        )[0]

                        if i == len(timesteps) - 1 or (i + 1) % pipe.scheduler.order == 0:
                            progress_bar.update()

                pipe._current_timestep = None
                if output_type == "latent":
                    return cur_latents
                latents_out = cur_latents.clone() if output_type == "both" else None
                dec_latents = pipe.denormalize_latents(cur_latents.to(pipe.vae.dtype))
                output_video = pipe._decode_video_latents(dec_latents)
                output_video = pipe.video_processor.postprocess_video(output_video)
                if output_type == "both":
                    return (output_video, latents_out)
                return output_video
        except Exception as exc:
            if _is_torch_compile_runtime_error(exc):
                raise
            fallback_err = str(exc)
            traceback.clear_frames(exc.__traceback__)

        print(
            f"[colabone-longcat16] WARNING: batched-3 CFG generate_ai2v failed ({fallback_err}); "
            "restoring upstream dual-pass CFG and retrying",
            file=sys.stderr,
            flush=True,
        )
        _restore_upstream()
        _clear_pipe_kv_cache(pipe)
        _clear_vae_cache(pipe)
        _torch_gc(torch_mod)
        if gen_state is not None and callable(getattr(generator, "set_state", None)):
            generator.set_state(gen_state)
        return orig_generate_ai2v(**call_kwargs)

    def _batched_generate_avc(
        video: Any,
        video_latent: Any,
        prompt: Any = None,
        negative_prompt: Any = None,
        height: int = 480,
        width: int = 832,
        num_frames: int = 93,
        num_cond_frames: int = 13,
        num_inference_steps: int = 50,
        use_distill: bool = False,
        text_guidance_scale: float = 4.0,
        audio_guidance_scale: float = 4.0,
        num_videos_per_prompt: int | None = 1,
        generator: Any = None,
        latents: Any = None,
        output_type: str | None = "np",
        attention_kwargs: dict[str, Any] | None = None,
        max_sequence_length: int = 512,
        use_kv_cache: bool = True,
        offload_kv_cache: bool = False,
        enhance_hf: bool = True,
        audio_emb: Any = None,
        ref_latent: Any = None,
        ref_img_index: int | None = None,
        mask_frame_range: int | None = None,
        ref_target_masks: Any = None,
        resize_mode: str | None = "crop",
    ) -> Any:
        call_kwargs = dict(
            video=video,
            video_latent=video_latent,
            prompt=prompt,
            negative_prompt=negative_prompt,
            height=height,
            width=width,
            num_frames=num_frames,
            num_cond_frames=num_cond_frames,
            num_inference_steps=num_inference_steps,
            use_distill=use_distill,
            text_guidance_scale=text_guidance_scale,
            audio_guidance_scale=audio_guidance_scale,
            num_videos_per_prompt=num_videos_per_prompt,
            generator=generator,
            latents=latents,
            output_type=output_type,
            attention_kwargs=attention_kwargs,
            max_sequence_length=max_sequence_length,
            use_kv_cache=use_kv_cache,
            offload_kv_cache=offload_kv_cache,
            enhance_hf=enhance_hf,
            audio_emb=audio_emb,
            ref_latent=ref_latent,
            ref_img_index=ref_img_index,
            mask_frame_range=mask_frame_range,
            ref_target_masks=ref_target_masks,
            resize_mode=resize_mode,
        )
        batch_size = 1 if isinstance(prompt, str) else (len(prompt) if prompt is not None else 0)
        do_cfg = float(text_guidance_scale) > 1.0 or float(audio_guidance_scale) > 1.0
        audio_emb_shape = getattr(audio_emb, "shape", None)
        audio_emb_b1 = bool(
            audio_emb is not None
            and audio_emb_shape is not None
            and len(audio_emb_shape) >= 1
            and int(audio_emb_shape[0]) == 1
        )
        if (
            not state["active"]
            or not do_cfg
            or not audio_emb_b1
            or ref_target_masks is not None
            or batch_size != 1
            or (num_videos_per_prompt or 1) != 1
            or output_type not in (None, "np", "both", "latent")
            or context_parallel_util.get_cp_size() > 1
        ):
            return orig_generate_avc(**call_kwargs)

        gen_state = (
            generator.get_state()
            if generator is not None and callable(getattr(generator, "get_state", None))
            else None
        )
        fallback_err: str | None = None
        try:
            with torch_mod.no_grad():
                if use_distill and enhance_hf:
                    raise ValueError("use_distill and enhance_hf cannot both be True")
                scale_factor_spatial = pipe.vae_scale_factor_spatial * 2
                if pipe.dit.cp_split_hw is not None:
                    scale_factor_spatial *= max(pipe.dit.cp_split_hw)
                pipe.check_inputs(
                    prompt, negative_prompt, height, width, scale_factor_spatial
                )
                if resize_mode not in ("default", "crop"):
                    raise ValueError(f"Unsupported resize_mode {resize_mode}")

                eff_frames = num_frames
                if eff_frames % pipe.vae_scale_factor_temporal != 1:
                    eff_frames = (
                        eff_frames // pipe.vae_scale_factor_temporal
                    ) * pipe.vae_scale_factor_temporal + 1
                eff_frames = max(eff_frames, 1)

                pipe._text_guidance_scale = text_guidance_scale
                pipe._audio_guidance_scale = audio_guidance_scale
                pipe._attention_kwargs = attention_kwargs
                pipe._current_timestep = None
                pipe._interrupt = False

                device = pipe.device
                dit_dtype = pipe.dit.dtype

                (
                    prompt_embeds,
                    prompt_attention_mask,
                    negative_prompt_embeds,
                    negative_prompt_attention_mask,
                ) = pipe.encode_prompt(
                    prompt=prompt,
                    negative_prompt=negative_prompt,
                    do_classifier_free_guidance=True,
                    num_videos_per_prompt=1,
                    max_sequence_length=max_sequence_length,
                    dtype=dit_dtype,
                    device=device,
                )

                # Batch=3 order: [neg_text + zero_audio, neg_text + audio, pos_text + audio]
                prompt_embeds_3 = torch_mod.cat(
                    [negative_prompt_embeds, negative_prompt_embeds, prompt_embeds], dim=0
                )
                prompt_mask_3 = torch_mod.cat(
                    [
                        negative_prompt_attention_mask,
                        negative_prompt_attention_mask,
                        prompt_attention_mask,
                    ],
                    dim=0,
                )
                audio_uncond = torch_mod.zeros_like(audio_emb)
                audio_embs_3 = torch_mod.cat([audio_uncond, audio_emb, audio_emb], dim=0)

                sigmas = pipe.get_timesteps_sigmas(
                    num_inference_steps, use_distill=use_distill
                )
                pipe.scheduler.set_timesteps(
                    num_inference_steps, sigmas=sigmas, device=device
                )
                timesteps = pipe.scheduler.timesteps

                if enhance_hf:
                    tail_uniform_start = 500
                    tail_uniform_end = 0
                    num_tail_uniform_steps = 10
                    timesteps_uniform_tail = list(
                        np.linspace(
                            tail_uniform_start,
                            tail_uniform_end,
                            num_tail_uniform_steps,
                            dtype=np.float32,
                            endpoint=(tail_uniform_end != 0),
                        )
                    )
                    timesteps_uniform_tail = [
                        torch_mod.tensor(t_val, device=device).unsqueeze(0)
                        for t_val in timesteps_uniform_tail
                    ]
                    filtered_timesteps = [
                        ts.unsqueeze(0) for ts in timesteps if ts > tail_uniform_start
                    ]
                    timesteps = torch_mod.cat(filtered_timesteps + timesteps_uniform_tail)
                    pipe.scheduler.timesteps = timesteps
                    pipe.scheduler.sigmas = torch_mod.cat(
                        [timesteps / 1000, torch_mod.zeros(1, device=timesteps.device)]
                    )

                ppv = pipe.video_processor.preprocess_video
                try:
                    vid_tensor = ppv(
                        video, height=height, width=width, resize_mode=resize_mode
                    )
                except TypeError:
                    vid_tensor = ppv(
                        pipe.video_processor,
                        video,
                        height=height,
                        width=width,
                        resize_mode=resize_mode,
                    )
                vid_tensor = vid_tensor.to(device=device, dtype=prompt_embeds_3.dtype)
                cond_videos = vid_tensor[:, :, -num_cond_frames:]
                cond_videos_latents = retrieve_latents(
                    pipe.vae.encode(cond_videos), generator, sample_mode="argmax"
                )
                cond_videos_latents = pipe.normalize_latents(cond_videos_latents)

                cur_latents = pipe.prepare_latents(
                    video=video_latent,
                    batch_size=1,
                    num_channels_latents=pipe.dit.config.in_channels,
                    height=height,
                    width=width,
                    num_frames=eff_frames,
                    num_cond_frames=num_cond_frames,
                    dtype=dit_dtype,
                    device=device,
                    generator=generator,
                    latents=latents,
                    need_encode=False,
                )

                num_cond_latents = (
                    1 + (num_cond_frames - 1) // pipe.vae_scale_factor_temporal
                )
                if not getattr(pipe, "use_vcond", False):
                    cur_latents[:, :, :num_cond_latents] = cond_videos_latents

                num_ref_latents = None
                if ref_latent is not None:
                    num_cond_latents += 1
                    num_ref_latents = 1
                    cur_latents = torch_mod.cat([ref_latent, cur_latents], dim=2)

                cond_latents = None
                if use_kv_cache:
                    cond_latents = cur_latents[:, :, :num_cond_latents]
                    pipe._cache_clean_latents(
                        cond_latents,
                        max_sequence_length,
                        offload_kv_cache=offload_kv_cache,
                        device=pipe.device,
                        dtype=dit_dtype,
                        audio_embs=audio_emb,
                        num_cond_latents=num_cond_latents,
                        num_ref_latents=num_ref_latents,
                        ref_img_index=ref_img_index,
                    )
                    kv_cache_dict = pipe._get_kv_cache_dict()
                    cur_latents = cur_latents[:, :, num_cond_latents:]
                else:
                    kv_cache_dict = None

                with _make_progress_bar(len(timesteps)) as progress_bar:
                    for i, t in enumerate(timesteps):
                        if pipe.interrupt:
                            continue
                        if getattr(pipe.dit, "lora_mix_config", None) is not None:
                            pipe.dit.set_lora_mix_step(i)
                        pipe._current_timestep = t

                        latent_model_input = torch_mod.cat(
                            [cur_latents, cur_latents, cur_latents], dim=0
                        ).to(dit_dtype)
                        timestep_3 = t.expand(latent_model_input.shape[0]).to(dit_dtype)
                        timestep_3 = timestep_3.unsqueeze(-1).repeat(
                            1, latent_model_input.shape[2]
                        )
                        if not use_kv_cache:
                            timestep_3[:, :num_cond_latents] = 0

                        noise_pred_3 = pipe.dit(
                            hidden_states=latent_model_input,
                            timestep=timestep_3,
                            encoder_hidden_states=prompt_embeds_3,
                            encoder_attention_mask=prompt_mask_3,
                            num_cond_latents=num_cond_latents,
                            kv_cache_dict=kv_cache_dict,
                            audio_embs=audio_embs_3,
                            num_ref_latents=num_ref_latents,
                            ref_img_index=ref_img_index,
                            mask_frame_range=mask_frame_range,
                            ref_target_masks=None,
                        )
                        noise_pred_uncond, noise_pred_uncond_text, noise_pred_cond = (
                            noise_pred_3.chunk(3, dim=0)
                        )
                        noise_pred = (
                            noise_pred_uncond
                            + text_guidance_scale
                            * (noise_pred_cond - noise_pred_uncond_text)
                            + audio_guidance_scale
                            * (noise_pred_uncond_text - noise_pred_uncond)
                        )
                        noise_pred = -noise_pred

                        if use_kv_cache:
                            cur_latents = pipe.scheduler.step(
                                noise_pred, t, cur_latents, return_dict=False
                            )[0]
                        else:
                            cur_latents[:, :, num_cond_latents:] = pipe.scheduler.step(
                                noise_pred[:, :, num_cond_latents:],
                                t,
                                cur_latents[:, :, num_cond_latents:],
                                return_dict=False,
                            )[0]

                        if i == len(timesteps) - 1 or (i + 1) % pipe.scheduler.order == 0:
                            progress_bar.update()

                if use_kv_cache and cond_latents is not None:
                    cur_latents = torch_mod.cat([cond_latents, cur_latents], dim=2)
                if ref_latent is not None and num_ref_latents is not None:
                    cur_latents = cur_latents[:, :, num_ref_latents:]
                    num_cond_latents -= 1
                cur_latents[:, :, :num_cond_latents] = cond_videos_latents

                pipe._current_timestep = None
                if output_type == "latent":
                    return cur_latents
                latents_out = cur_latents.clone() if output_type == "both" else None
                dec_latents = pipe.denormalize_latents(cur_latents.to(pipe.vae.dtype))
                output_video = pipe._decode_video_latents(dec_latents)
                output_video = pipe.video_processor.postprocess_video(output_video)
                if output_type == "both":
                    return (output_video, latents_out)
                return output_video
        except Exception as exc:
            if _is_torch_compile_runtime_error(exc):
                raise
            fallback_err = str(exc)
            traceback.clear_frames(exc.__traceback__)

        print(
            f"[colabone-longcat16] WARNING: batched-3 CFG generate_avc failed ({fallback_err}); "
            "restoring upstream dual-pass CFG and retrying",
            file=sys.stderr,
            flush=True,
        )
        _restore_upstream()
        _clear_pipe_kv_cache(pipe)
        _clear_vae_cache(pipe)
        _torch_gc(torch_mod)
        if gen_state is not None and callable(getattr(generator, "set_state", None)):
            generator.set_state(gen_state)
        return orig_generate_avc(**call_kwargs)

    pipe.generate_ai2v = _batched_generate_ai2v
    pipe.generate_avc = _batched_generate_avc
    print(
        "[colabone-longcat16] INFO: enabled bit-exact 3-way batched CFG fusion "
        "(batch=3 single DiT pass per step)",
        file=sys.stderr,
        flush=True,
    )
    return state


def _is_torch_compile_runtime_error(exc: BaseException) -> bool:
    chain: list[BaseException] = []
    visited: set[int] = set()
    pending: collections.deque[BaseException] = collections.deque([exc])
    while pending:
        cur = pending.popleft()
        if not isinstance(cur, BaseException):
            continue
        cur_id = id(cur)
        if cur_id in visited:
            continue
        visited.add(cur_id)
        chain.append(cur)
        for nxt in (
            getattr(cur, "__cause__", None),
            getattr(cur, "__context__", None),
            getattr(cur, "inner_exception", None),
        ):
            if isinstance(nxt, BaseException) and id(nxt) not in visited:
                pending.append(nxt)

    # First pass: OOM / CUDA device faults anywhere in the chain are NEVER compile errors.
    for item in chain:
        cls = type(item)
        cls_name = str(getattr(cls, "__name__", "") or "").lower()
        msg = str(item).lower()
        if (
            isinstance(item, MemoryError)
            or "outofmemory" in cls_name
            or any(
                token in msg
                for token in (
                    "out of memory",
                    "cuda error:",
                    "cudaerrormemoryallocation",
                    "illegal memory access",
                    "illegal instruction",
                )
            )
        ):
            return False

    stdlib_wrapper_basenames = (
        "subprocess.py",
        "os.py",
        "pathlib.py",
        "shutil.py",
        "posixpath.py",
    )
    stdlib_wrapper_suffixes = tuple(f"/{name}" for name in stdlib_wrapper_basenames)

    # Second pass: match Dynamo/Inductor/Triton/symbolic_shapes errors across the chain.
    for item in chain:
        cls = type(item)
        mod_name = str(getattr(cls, "__module__", "") or "").lower()
        cls_name = str(getattr(cls, "__name__", "") or "").lower()
        msg = str(item).lower()

        if (
            "dynamo" in mod_name
            or "inductor" in mod_name
            or "triton" in mod_name
            or "functorch" in mod_name
            or "symbolic_shapes" in mod_name
            or "backendcompilerfailed" in cls_name
            or "inductorerror" in cls_name
            or "guardondatadependentsymnode" in cls_name
            or "constraintviolationerror" in cls_name
            or "torch._dynamo" in msg
            or "torch._inductor" in msg
            or "triton" in msg
            or "ptxas" in msg
            or "nvrtc" in msg
            or "guardondatadependentsymnode" in msg
        ):
            return True

        try:
            frames = traceback.extract_tb(item.__traceback__)
        except Exception:
            frames = []

        trimmed_frames = list(frames)
        while trimmed_frames:
            tail_fn = (
                str(getattr(trimmed_frames[-1], "filename", "") or "")
                .replace("\\", "/")
                .lower()
            )
            if tail_fn in stdlib_wrapper_basenames or any(
                tail_fn.endswith(sfx) for sfx in stdlib_wrapper_suffixes
            ):
                trimmed_frames.pop()
            else:
                break

        if trimmed_frames:
            filename = (
                str(getattr(trimmed_frames[-1], "filename", "") or "")
                .replace("\\", "/")
                .lower()
            )
            if "torch/_dynamo/" in filename:
                if not (
                    filename.endswith("/eval_frame.py")
                    or filename == "eval_frame.py"
                    or filename.endswith("/runtime_wrappers.py")
                    or filename == "runtime_wrappers.py"
                ):
                    return True
            elif (
                "torch/_inductor/" in filename
                or "torch/_functorch/" in filename
                or "torch/_subclasses/" in filename
                or "torch/fx/experimental/symbolic_shapes.py" in filename
                or "/triton/" in filename
                or filename.startswith("triton/")
            ):
                return True

    return False


def _clear_pipe_kv_cache(pipe_obj: Any, *, flush_cuda_cache: bool = True) -> None:
    if pipe_obj is None:
        return
    if flush_cuda_cache:
        clear_fn = getattr(pipe_obj, "_clear_cache", None)
        if callable(clear_fn):
            try:
                clear_fn()
                return
            except Exception:
                pass
    if hasattr(pipe_obj, "kv_cache_dict"):
        try:
            pipe_obj.kv_cache_dict = None
        except Exception:
            pass
    dit_obj = getattr(pipe_obj, "dit", None)
    blocks = getattr(dit_obj, "blocks", None)
    if blocks is not None:
        for block in blocks:
            for owner in (block, getattr(block, "attn", None)):
                if owner is None:
                    continue
                for attr in ("kv_cache", "kv_cache_dict"):
                    if hasattr(owner, attr):
                        try:
                            setattr(owner, attr, None)
                        except Exception:
                            pass


def _clear_vae_cache(pipe_obj: Any) -> None:
    if pipe_obj is None:
        return
    vae_obj = getattr(pipe_obj, "vae", None)
    clear_fn = getattr(vae_obj, "clear_cache", None)
    if callable(clear_fn):
        try:
            clear_fn()
        except Exception:
            pass


def _buffer_nbytes(obj: Any) -> int | None:
    if obj is None:
        return None
    nbytes = getattr(obj, "nbytes", None)
    if isinstance(nbytes, int) and not isinstance(nbytes, bool):
        return nbytes
    if isinstance(obj, (bytes, bytearray, memoryview)):
        try:
            return int(len(obj))
        except Exception:
            return None
    numel_fn = getattr(obj, "numel", None)
    elem_size_fn = getattr(obj, "element_size", None)
    if callable(numel_fn) and callable(elem_size_fn):
        try:
            return int(numel_fn()) * int(elem_size_fn())
        except Exception:
            return None
    return None


class _ColabOneFfv1PipeWriter:
    """Async streaming lossless FFV1 MKV writer that bounds RAM and overlaps CPU encode with GPU."""

    _buffer_nbytes = staticmethod(_buffer_nbytes)

    def __init__(
        self,
        ffmpeg_bin: str,
        width: int,
        height: int,
        fps: int,
        output_path: str | Path,
    ) -> None:
        self.width = _require_int(width, "width")
        self.height = _require_int(height, "height")
        self.fps = _require_int(fps, "fps")
        safe_out = _normalize_ffmpeg_path_arg(output_path, "output_video_path")
        self.output_path = _ensure_not_symlink(safe_out, "output_video_path")
        self.output_path.parent.mkdir(parents=True, exist_ok=True)
        self._tmp_output_path = self.output_path.parent / (
            f".{self.output_path.name}.{os.getpid()}.{id(self)}.tmp.mkv"
        )
        _ensure_not_symlink(self._tmp_output_path, "temp_output_video_path")
        self.cmd = build_lossless_ffv1_writer_cmd(
            ffmpeg_bin=ffmpeg_bin,
            width=self.width,
            height=self.height,
            fps=self.fps,
            output_path=self._tmp_output_path,
        )
        self._stderr_lock = threading.Lock()
        self._stderr_lines: collections.deque[str] = collections.deque(maxlen=200)
        self._write_queue: queue.Queue[Any] = queue.Queue(maxsize=2)
        self._error_lock = threading.Lock()
        self._write_error: BaseException | None = None
        self._abort_event = threading.Event()
        self._pipe_frames_flushed = 0
        self.frames_written = 0
        self._closed = False
        self._stderr_thread: threading.Thread | None = None
        self._writer_thread: threading.Thread | None = None
        self._proc: subprocess.Popen[bytes] | None = subprocess.Popen(
            self.cmd,
            stdin=subprocess.PIPE,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.PIPE,
            env=_sanitized_subprocess_env(),
        )
        try:
            self._stderr_thread = threading.Thread(
                target=self._drain_stderr,
                name="colabone-ffv1-stderr",
                daemon=True,
            )
            self._stderr_thread.start()
            self._writer_thread = threading.Thread(
                target=self._writer_loop,
                name="colabone-ffv1-writer",
                daemon=True,
            )
            self._writer_thread.start()
        except BaseException:
            self.abort()
            raise

    def _stderr_tail(self, limit: int = 20) -> str:
        with self._stderr_lock:
            lines = list(self._stderr_lines)
        return "\n".join(lines[-limit:])

    def _drain_stderr(self) -> None:
        proc = self._proc
        if proc is None or proc.stderr is None:
            return
        try:
            for raw_line in iter(proc.stderr.readline, b""):
                text = raw_line.decode("utf-8", errors="replace").rstrip()
                if text:
                    with self._stderr_lock:
                        self._stderr_lines.append(text)
        except Exception:
            pass
        finally:
            try:
                proc.stderr.close()
            except Exception:
                pass

    def _record_write_error(self, err: BaseException) -> None:
        with self._error_lock:
            if self._write_error is None:
                self._write_error = err

    def _get_write_error(self) -> BaseException | None:
        with self._error_lock:
            return self._write_error

    def _check_pipe_healthy(self) -> None:
        err = self._get_write_error()
        if err is not None:
            raise err
        proc = self._proc
        if proc is not None and proc.poll() is not None:
            try:
                self._stderr_thread.join(timeout=0.5)
            except Exception:
                pass
            tail = self._stderr_tail(20)
            err = RuntimeError(
                f"FFmpeg FFV1 pipe failed after {self._pipe_frames_flushed} frames: {tail}"
            )
            self._record_write_error(err)
            raise err

    def _writer_loop(self) -> None:
        while True:
            item = self._write_queue.get()
            try:
                if item is None or self._abort_event.is_set():
                    break
                proc = self._proc
                if proc is None or proc.stdin is None:
                    self._record_write_error(
                        RuntimeError("FFV1 writer pipe is closed unexpectedly")
                    )
                    break
                import numpy as np

                stdin = proc.stdin
                try:
                    for frame_idx in range(item.shape[0]):
                        if self._abort_event.is_set():
                            break
                        frame = np.ascontiguousarray(item[frame_idx])
                        stdin.write(memoryview(frame).cast("B"))
                        self._pipe_frames_flushed += 1
                    if not self._abort_event.is_set():
                        stdin.flush()
                except (BrokenPipeError, OSError, ValueError, RuntimeError) as exc:
                    if self._abort_event.is_set():
                        break
                    if isinstance(exc, (BrokenPipeError, OSError)):
                        try:
                            self._stderr_thread.join(timeout=0.5)
                        except Exception:
                            pass
                        tail = self._stderr_tail(20)
                        err = RuntimeError(
                            f"FFmpeg FFV1 pipe failed after {self._pipe_frames_flushed} frames: {tail}"
                        )
                        err.__cause__ = exc
                        self._record_write_error(err)
                    else:
                        self._record_write_error(exc)
                    break
                except Exception as exc:
                    if self._abort_event.is_set():
                        break
                    self._record_write_error(exc)
                    break
            finally:
                self._write_queue.task_done()

    def write_frames(self, frames_uint8: Any) -> int:
        import numpy as np

        if self._closed or self._proc is None or self._proc.stdin is None:
            raise RuntimeError("FFV1 writer pipe is already closed")

        self._check_pipe_healthy()

        arr = np.asarray(frames_uint8)
        if arr.ndim != 4 or arr.shape[1:] != (self.height, self.width, 3):
            raise ValueError(
                f"Expected frame batch shape (N, {self.height}, {self.width}, 3), "
                f"got {tuple(arr.shape)}"
            )
        if arr.dtype != np.uint8:
            arr = arr.astype(np.uint8, copy=False)

        count = int(arr.shape[0])
        if count == 0:
            return 0

        # Detach slice views into larger segment buffers so the async queue does not
        # pin full segment arrays in host RAM.
        arr_base = getattr(arr, "base", None)
        arr_nbytes = self._buffer_nbytes(arr)
        base_nbytes = self._buffer_nbytes(arr_base) if arr_base is not None else None
        if (
            arr_base is not None
            and isinstance(arr_nbytes, int)
            and isinstance(base_nbytes, int)
            and arr_nbytes < base_nbytes
        ):
            copy_fn = getattr(arr, "copy", None)
            arr = np.ascontiguousarray(copy_fn() if callable(copy_fn) else arr)

        while True:
            self._check_pipe_healthy()
            if self._writer_thread is None or not self._writer_thread.is_alive():
                self._check_pipe_healthy()
                raise RuntimeError(
                    f"FFmpeg FFV1 pipe failed after {self._pipe_frames_flushed} frames: writer thread stopped"
                )
            try:
                self._write_queue.put(arr, timeout=0.2)
                break
            except queue.Full:
                continue

        self.frames_written += count
        return count

    def _signal_writer_stop(self, timeout_sec: float = 300.0) -> None:
        deadline = time.monotonic() + float(timeout_sec)
        while self._writer_thread.is_alive():
            if self._abort_event.is_set() or self._get_write_error() is not None:
                while True:
                    try:
                        self._write_queue.get_nowait()
                        self._write_queue.task_done()
                    except queue.Empty:
                        break
            remaining = deadline - time.monotonic()
            if remaining <= 0.0:
                break
            try:
                self._write_queue.put(None, timeout=min(0.1, remaining))
                break
            except queue.Full:
                continue
        if self._writer_thread.is_alive():
            join_timeout = max(0.0, deadline - time.monotonic())
            if join_timeout > 0.0:
                self._writer_thread.join(timeout=join_timeout)

    def close(self) -> None:
        if self._closed:
            return
        self._closed = True
        proc = self._proc
        if proc is None:
            return
        replaced = False
        try:
            self._signal_writer_stop(timeout_sec=300.0)
            write_err = self._get_write_error()
            if write_err is not None:
                raise write_err
            if self._writer_thread.is_alive():
                self.abort()
                raise RuntimeError(
                    f"FFmpeg FFV1 writer thread timed out after flushing "
                    f"{self._pipe_frames_flushed}/{self.frames_written} frames"
                )
            if self._pipe_frames_flushed != self.frames_written:
                self.abort()
                raise RuntimeError(
                    f"FFmpeg FFV1 pipe flushed {self._pipe_frames_flushed} frames, "
                    f"expected {self.frames_written}; refusing to publish truncated output"
                )
            if proc.stdin is not None:
                try:
                    proc.stdin.flush()
                except Exception:
                    pass
                try:
                    proc.stdin.close()
                except Exception:
                    pass
            returncode = proc.wait()
            self._stderr_thread.join(timeout=5.0)
            if returncode != 0:
                tail = self._stderr_tail(30)
                raise RuntimeError(
                    f"FFmpeg FFV1 writer exited with code {returncode}: {tail}"
                )
            _ensure_not_symlink(self.output_path, "output_video_path")
            os.replace(self._tmp_output_path, self.output_path)
            replaced = True
        finally:
            self._abort_event.set()
            while True:
                try:
                    self._write_queue.get_nowait()
                    self._write_queue.task_done()
                except queue.Empty:
                    break
            try:
                self._write_queue.put_nowait(None)
            except queue.Full:
                pass
            if proc.poll() is None:
                try:
                    proc.terminate()
                    proc.wait(timeout=3.0)
                except Exception:
                    try:
                        proc.kill()
                        proc.wait(timeout=3.0)
                    except Exception:
                        pass
            try:
                self._writer_thread.join(timeout=2.0)
            except Exception:
                pass
            if proc.stdin is not None:
                try:
                    proc.stdin.close()
                except Exception:
                    pass
            try:
                self._stderr_thread.join(timeout=1.0)
            except Exception:
                pass
            if not replaced:
                try:
                    self._tmp_output_path.unlink(missing_ok=True)
                except OSError:
                    pass
            self._proc = None

    def abort(self) -> None:
        self._closed = True
        self._abort_event.set()
        while True:
            try:
                self._write_queue.get_nowait()
                self._write_queue.task_done()
            except queue.Empty:
                break
        try:
            self._write_queue.put_nowait(None)
        except queue.Full:
            pass
        proc = self._proc
        try:
            if proc is not None:
                if proc.poll() is None:
                    try:
                        proc.terminate()
                        proc.wait(timeout=3.0)
                    except Exception:
                        try:
                            proc.kill()
                            proc.wait(timeout=3.0)
                        except Exception:
                            pass
                if self._writer_thread is not None:
                    try:
                        self._writer_thread.join(timeout=2.0)
                    except Exception:
                        pass
                if proc.stdin is not None:
                    try:
                        proc.stdin.close()
                    except Exception:
                        pass
                if self._stderr_thread is not None:
                    try:
                        self._stderr_thread.join(timeout=1.0)
                    except Exception:
                        pass
            try:
                self._tmp_output_path.unlink(missing_ok=True)
            except OSError:
                pass
        finally:
            self._proc = None


def _to_uint8_rgb_frames(output_video: Any) -> Any:
    import numpy as np

    # Fast path for PyTorch tensors (performs clamp + uint8 cast before host copy).
    if (
        callable(getattr(output_video, "detach", None))
        and callable(getattr(output_video, "cpu", None))
        and callable(getattr(output_video, "numpy", None))
    ):
        t = output_video.detach()
        if getattr(t, "ndim", 0) == 5:
            t = t[0]
        t_shape = getattr(t, "shape", None)
        if (
            getattr(t, "ndim", 0) == 4
            and t_shape is not None
            and len(t_shape) == 4
            and int(t_shape[1]) == 3
            and int(t_shape[-1]) != 3
            and callable(getattr(t, "permute", None))
        ):
            t = t.permute(0, 2, 3, 1)
        if str(getattr(t, "dtype", "")) == "torch.uint8":
            return t.contiguous().cpu().numpy()
        isfinite_fn = getattr(t, "isfinite", None)
        if callable(isfinite_fn) and not bool(isfinite_fn().all()):
            raise ValueError(
                "Generated video tensor contains NaN or Inf values; aborting frame conversion"
            )
        import torch

        return (
            t.clamp(0.0, 1.0)
            .mul(255.0)
            .to(dtype=torch.uint8)
            .contiguous()
            .cpu()
            .numpy()
        )

    arr = np.asarray(output_video)
    if arr.ndim == 5:
        arr = arr[0]
    if arr.dtype == np.uint8:
        return arr
    if not np.isfinite(arr).all():
        raise ValueError(
            "Generated video tensor contains NaN or Inf values; aborting frame conversion"
        )
    clipped = np.clip(arr, 0.0, 1.0)
    mul_fn = getattr(np, "multiply", None)
    np_dtype_cls = getattr(np, "dtype", None)
    if (
        callable(mul_fn)
        and isinstance(np_dtype_cls, type)
        and isinstance(getattr(clipped, "dtype", None), np_dtype_cls)
    ):
        mul_fn(clipped, 255.0, out=clipped)
        return clipped.astype(np.uint8, copy=False)
    return (clipped * 255.0).astype(np.uint8)


def run_longcat16_generation(cfg: dict[str, Any]) -> dict[str, Any]:
    """Execute full BF16 LongCat-Video-Avatar 1.6 generation and write FFV1 MKV + manifest."""
    if not isinstance(cfg, dict):
        raise ValueError("Worker config must be a JSON object")

    start_time = time.monotonic()

    # Validate core paths and parameters before importing heavy ML libraries.
    longcat_repo_dir = _validate_safe_arg(cfg.get("longcat_repo_dir", ""), "longcat_repo_dir")
    avatar_checkpoint_dir = _validate_safe_arg(
        cfg.get("avatar_checkpoint_dir", ""), "avatar_checkpoint_dir"
    )
    base_video_dir = _validate_safe_arg(cfg.get("base_video_dir", ""), "base_video_dir")
    image_path = _validate_safe_arg(cfg.get("image_path", ""), "image_path")
    output_video_path = _normalize_ffmpeg_path_arg(
        cfg.get("output_video_path", ""), "output_video_path"
    )
    _ensure_not_symlink(output_video_path, "output_video_path")
    output_manifest_path = str(
        _ensure_not_symlink(
            cfg.get("output_manifest_path", ""), "output_manifest_path"
        )
    )
    primary_audio_path, speakers = normalize_longcat16_speakers(cfg)
    primary_audio_path = _normalize_ffmpeg_path_arg(primary_audio_path, "audio_path")

    resolution = str(cfg.get("resolution") or "720p").strip().lower()
    if resolution not in _VALID_RESOLUTIONS:
        raise ValueError(
            f"Unsupported resolution {resolution!r}; expected one of {sorted(_VALID_RESOLUTIONS)}"
        )

    req_width = _parse_strict_int(cfg.get("width"), "width", default=1280)
    req_height = _parse_strict_int(cfg.get("height"), "height", default=768)
    fps = _parse_strict_int(cfg.get("fps"), "fps", default=25)
    exact_frames = _parse_strict_int(cfg.get("exact_frames"), "exact_frames")
    num_frames = _parse_strict_int(cfg.get("num_frames"), "num_frames", default=93)
    num_cond_frames = _parse_strict_int(
        cfg.get("num_cond_frames"), "num_cond_frames", default=13
    )
    ref_img_index = _parse_strict_int(
        cfg.get("ref_img_index"), "ref_img_index", default=10
    )
    mask_frame_range = _parse_strict_int(
        cfg.get("mask_frame_range"), "mask_frame_range", default=0
    )
    seed = _parse_strict_int(cfg.get("seed"), "seed", default=42)
    lora_multiplier = _parse_strict_float(
        cfg.get("lora_multiplier"), "lora_multiplier", default=1.0
    )

    if req_width < 16 or req_height < 16 or req_width % 2 != 0 or req_height % 2 != 0:
        raise ValueError(
            f"width and height must be even integers >= 16, got {req_width}x{req_height}"
        )
    if fps != 25:
        raise ValueError(
            f"LongCat-Video-Avatar 1.6 requires fps == 25 (Whisper 50Hz alignment), got {fps}"
        )
    if (num_frames - 1) % 4 != 0:
        raise ValueError(
            f"num_frames must satisfy (num_frames - 1) % 4 == 0, got {num_frames}"
        )
    if (num_cond_frames - 1) % 4 != 0:
        raise ValueError(
            f"num_cond_frames must satisfy (num_cond_frames - 1) % 4 == 0, got {num_cond_frames}"
        )
    if seed < 0:
        raise ValueError(f"seed must be >= 0, got {seed}")
    if ref_img_index < 0:
        raise ValueError(f"ref_img_index must be >= 0, got {ref_img_index}")
    if mask_frame_range < 0:
        raise ValueError(f"mask_frame_range must be >= 0, got {mask_frame_range}")

    prompt = str(cfg.get("prompt") or "")
    negative_prompt = str(
        cfg.get("negative_prompt")
        if cfg.get("negative_prompt") is not None
        and str(cfg.get("negative_prompt")).strip()
        else COLABONE_LONGCAT16_DEFAULT_NEGATIVE_PROMPT
    )
    vocal_sep_mode = str(cfg.get("vocal_separation") or "auto").strip().lower()
    is_tts_audio = bool(cfg.get("is_tts_audio", False))
    run_vocal_sep = should_run_vocal_separation(vocal_sep_mode, is_tts_audio)
    compile_dit_opt = _parse_tri_state_option(
        cfg.get("compile_dit"), "compile_dit", default="auto"
    )
    compile_vae_raw = cfg.get("compile_vae")
    compile_vae = (
        compile_vae_raw
        if isinstance(compile_vae_raw, bool)
        else (
            str(compile_vae_raw).strip().lower() in ("1", "true", "yes", "on")
            if compile_vae_raw is not None
            else False
        )
    )
    fp8_ffn_opt = _parse_tri_state_option(cfg.get("fp8_ffn"), "fp8_ffn", default="auto")
    batched_cfg_opt = _parse_tri_state_option(
        cfg.get("batched_cfg"), "batched_cfg", default="auto"
    )
    compile_cache_dir = str(cfg.get("compile_cache_dir") or "").strip()
    attention_backend = str(cfg.get("attention_backend") or "auto").strip().lower()
    if attention_backend not in _VALID_ATTENTION_BACKENDS:
        raise ValueError(
            f"Unsupported attention_backend {attention_backend!r}; "
            f"expected one of {sorted(_VALID_ATTENTION_BACKENDS)}"
        )
    ffmpeg_bin = _validate_ffmpeg_bin(cfg.get("ffmpeg_bin") or "ffmpeg", "ffmpeg_bin")

    sampling = resolve_longcat16_sampling_params(cfg)
    segment_plan = plan_longcat16_segments(
        exact_frames=exact_frames,
        num_frames=num_frames,
        num_cond_frames=num_cond_frames,
    )
    num_segments = len(segment_plan)

    if compile_cache_dir:
        compile_cache_path = _ensure_not_symlink(compile_cache_dir, "compile_cache_dir")
    else:
        compile_cache_path = None

    if not Path(longcat_repo_dir).is_dir():
        raise FileNotFoundError(f"longcat_repo_dir not found: {longcat_repo_dir}")
    if not Path(avatar_checkpoint_dir).is_dir():
        raise FileNotFoundError(
            f"avatar_checkpoint_dir not found: {avatar_checkpoint_dir}"
        )
    if not Path(base_video_dir).is_dir():
        raise FileNotFoundError(f"base_video_dir not found: {base_video_dir}")
    if not Path(image_path).is_file():
        raise FileNotFoundError(f"image_path not found: {image_path}")
    if not Path(primary_audio_path).is_file():
        raise FileNotFoundError(f"audio_path not found: {primary_audio_path}")

    # Set persistent TorchInductor & Triton cache directories BEFORE importing torch.
    # Use setdefault so the manager's DriveFS-isolated env vars take precedence when set.
    if compile_cache_path is not None:
        if not os.environ.get("TORCHINDUCTOR_CACHE_DIR", "").strip():
            compile_cache_path.mkdir(parents=True, exist_ok=True)
            os.environ.pop("TORCHINDUCTOR_CACHE_DIR", None)
        os.environ.setdefault("TORCHINDUCTOR_CACHE_DIR", str(compile_cache_path))
        triton_cache_path = compile_cache_path / "triton"
        if not os.environ.get("TRITON_CACHE_DIR", "").strip():
            _ensure_not_symlink(triton_cache_path, "triton_cache_dir")
            triton_cache_path.mkdir(parents=True, exist_ok=True)
            os.environ.pop("TRITON_CACHE_DIR", None)
        os.environ.setdefault("TRITON_CACHE_DIR", str(triton_cache_path))

    # Expose LongCat-Video repo on sys.path before importing longcat_video modules.
    if longcat_repo_dir not in sys.path:
        sys.path.insert(0, longcat_repo_dir)

    emit_colabone_progress(
        "init",
        0.02,
        f"正在初始化 LongCat-Video-Avatar 1.6（{sampling['sampling_mode']}，共 {num_segments} 段）",
        sampling_mode=sampling["sampling_mode"],
        total_segments=num_segments,
        exact_frames=exact_frames,
    )

    # Install SIGTERM/SIGINT handlers so ffmpeg child and CUDA memory tear down cleanly.
    can_install_signals = threading.current_thread() is threading.main_thread()
    prev_sigterm = signal.getsignal(signal.SIGTERM) if can_install_signals else None
    prev_sigint = signal.getsignal(signal.SIGINT) if can_install_signals else None
    terminated_flag = {"signum": 0}

    def _handle_termination(signum: int, _frame: Any) -> None:
        if terminated_flag["signum"] != 0:
            return
        terminated_flag["signum"] = int(signum)
        raise ColabOneLongCat16Terminated(signum)

    if can_install_signals:
        signal.signal(signal.SIGTERM, _handle_termination)
        signal.signal(signal.SIGINT, _handle_termination)

    ffmpeg_writer: _ColabOneFfv1PipeWriter | None = None
    vocal_temp_dir: tempfile.TemporaryDirectory[str] | None = None
    vocal_separator: Any = None
    initialized_pg = False
    torch_mod: Any = None
    dist_mod: Any = None
    pipe: Any = None
    dit: Any = None
    vae: Any = None
    text_encoder: Any = None
    audio_encoder: Any = None
    eager_dit_blocks: Any = None
    eager_vae_encoder: Any = None
    eager_vae_decoder: Any = None
    latent: Any = None
    ref_latent: Any = None
    full_audio_emb: Any = None
    audio_emb_0: Any = None
    audio_emb_seg: Any = None

    try:
        import numpy as np
        import PIL.Image
        import torch
        import torch.distributed as dist
        from diffusers.utils import load_image
        from transformers import AutoTokenizer, UMT5EncoderModel

        from longcat_video.audio_process import (  # type: ignore[import-not-found]
            get_audio_encoder,
            get_audio_feature_extractor,
        )
        from longcat_video.context_parallel import (  # type: ignore[import-not-found]
            context_parallel_util,
        )
        from longcat_video.modules.autoencoder_kl_wan import (  # type: ignore[import-not-found]
            AutoencoderKLWan,
        )
        from longcat_video.modules.avatar.longcat_video_dit_avatar import (  # type: ignore[import-not-found]
            LongCatVideoAvatarTransformer3DModel,
        )
        from longcat_video.modules.scheduling_flow_match_euler_discrete import (  # type: ignore[import-not-found]
            FlowMatchEulerDiscreteScheduler,
        )
        from longcat_video.pipeline_longcat_video_avatar import (  # type: ignore[import-not-found]
            LongCatVideoAvatarPipeline,
        )

        torch_mod = torch
        dist_mod = dist

        if not torch.cuda.is_available():
            raise RuntimeError("LongCat-Video-Avatar 1.6 requires a CUDA GPU")

        local_rank = 0
        torch.cuda.set_device(local_rank)
        device = torch.device(f"cuda:{local_rank}")

        # Always enable cuDNN autotuner and TF32 matmul acceleration on Ampere+ GPUs.
        try:
            backends_mod = getattr(torch, "backends", None)
            cudnn_be = getattr(backends_mod, "cudnn", None)
            cuda_be = getattr(backends_mod, "cuda", None)
            if cudnn_be is not None:
                if hasattr(cudnn_be, "benchmark"):
                    cudnn_be.benchmark = True
                if hasattr(cudnn_be, "allow_tf32"):
                    cudnn_be.allow_tf32 = True
            if cuda_be is not None and hasattr(cuda_be, "matmul"):
                cuda_be.matmul.allow_tf32 = True
            set_matmul_prec = getattr(torch, "set_float32_matmul_precision", None)
            if callable(set_matmul_prec):
                set_matmul_prec("high")
        except Exception:
            pass

        try:
            torch.cuda.reset_peak_memory_stats(device)
        except Exception:
            pass

        # Always initialize a private single-rank loopback process group without
        # trusting inherited RANK / WORLD_SIZE / MASTER_PORT environment variables.
        if not dist.is_initialized():
            pg_timeout = datetime.timedelta(seconds=120)
            preferred_backend = "nccl" if dist.is_nccl_available() else "gloo"
            port = _find_free_loopback_port()
            init_url = f"tcp://127.0.0.1:{port}"
            os.environ["MASTER_ADDR"] = "127.0.0.1"
            os.environ["MASTER_PORT"] = str(port)
            os.environ["RANK"] = "0"
            os.environ["WORLD_SIZE"] = "1"
            os.environ["LOCAL_RANK"] = str(local_rank)
            if preferred_backend == "gloo":
                os.environ.setdefault("GLOO_SOCKET_IFNAME", "lo")
            try:
                dist.init_process_group(
                    backend=preferred_backend,
                    init_method=init_url,
                    rank=0,
                    world_size=1,
                    timeout=pg_timeout,
                )
            except Exception as exc:
                if preferred_backend == "gloo":
                    raise
                print(
                    f"[colabone-longcat16] WARNING: single-GPU nccl init failed ({exc}), "
                    "retrying with gloo loopback group",
                    file=sys.stderr,
                    flush=True,
                )
                os.environ.setdefault("GLOO_SOCKET_IFNAME", "lo")
                port_retry = _find_free_loopback_port()
                os.environ["MASTER_PORT"] = str(port_retry)
                dist.init_process_group(
                    backend="gloo",
                    init_method=f"tcp://127.0.0.1:{port_retry}",
                    rank=0,
                    world_size=1,
                    timeout=pg_timeout,
                )
            initialized_pg = True

        global_rank = dist.get_rank()
        num_processes = dist.get_world_size()
        context_parallel_util.init_context_parallel(
            context_parallel_size=1,
            global_rank=global_rank,
            world_size=num_processes,
        )
        cp_size = context_parallel_util.get_cp_size()
        cp_split_hw = context_parallel_util.get_optimal_split(cp_size)

        # Compute full target audio window covering all segments + 1.0s safety tail.
        total_generated_frames = num_frames + max(0, num_segments - 1) * (
            num_frames - num_cond_frames
        )
        generate_duration = total_generated_frames / float(fps)
        audio_process_duration = generate_duration + 1.0

        # Run vocal separation BEFORE loading DiT/VAE/UMT5/Whisper onto CUDA so
        # ONNX Runtime never stacks on top of the diffusion weights in VRAM.
        emit_colabone_progress(
            "audio",
            0.06,
            f"正在准备音频特征（前置人声分离：{'开启' if run_vocal_sep else '关闭'}）",
        )

        speech_array = None
        sr = 16000
        vocal_separated = False

        if run_vocal_sep:
            vocal_model_path = os.path.join(
                avatar_checkpoint_dir, "vocal_separator", "Kim_Vocal_2.onnx"
            )
            if os.path.isfile(vocal_model_path):
                vocal_model_dir = os.path.dirname(vocal_model_path)
                prev_audio_sep_model_dir = os.environ.get("AUDIO_SEPARATOR_MODEL_DIR")
                try:
                    from audio_separator.separator import (  # type: ignore[import-not-found]
                        Separator,
                    )
                    from longcat_video.audio_process.vocal_separator import (  # type: ignore[import-not-found]
                        separate_vocals,
                    )

                    vocal_temp_dir = tempfile.TemporaryDirectory(
                        prefix="colabone_longcat16_vocal_"
                    )
                    vocal_out_dir = Path(vocal_temp_dir.name) / "vocals"
                    vocal_out_dir.mkdir(parents=True, exist_ok=True)
                    os.environ["AUDIO_SEPARATOR_MODEL_DIR"] = vocal_model_dir
                    vocal_separator = Separator(
                        output_dir=vocal_out_dir,
                        output_single_stem="vocals",
                        model_file_dir=vocal_model_dir,
                    )
                    if vocal_separator.onnx_execution_provider == [
                        "CUDAExecutionProvider"
                    ]:
                        vocal_separator.onnx_execution_provider = [
                            (
                                "CUDAExecutionProvider",
                                {
                                    "device_id": local_rank,
                                    "cudnn_conv_algo_search": "HEURISTIC",
                                },
                            )
                        ]
                    vocal_separator.load_model(os.path.basename(vocal_model_path))
                    sep_audio, sep_sr = separate_vocals(
                        vocal_separator,
                        primary_audio_path,
                        duration=audio_process_duration,
                    )
                    sep_arr = np.asarray(sep_audio, dtype=np.float32).reshape(-1)
                    if sep_arr.size > 0:
                        speech_array, sr = _resample_audio_if_needed(
                            sep_arr, int(sep_sr), 16000
                        )
                        vocal_separated = True
                    else:
                        print(
                            "[colabone-longcat16] WARNING: vocal separator returned empty audio, "
                            "falling back to raw speech WAV",
                            file=sys.stderr,
                            flush=True,
                        )
                except Exception as exc:
                    print(
                        f"[colabone-longcat16] WARNING: vocal separation failed ({exc}), "
                        "falling back to raw speech WAV",
                        file=sys.stderr,
                        flush=True,
                    )
                finally:
                    if prev_audio_sep_model_dir is None:
                        os.environ.pop("AUDIO_SEPARATOR_MODEL_DIR", None)
                    else:
                        os.environ["AUDIO_SEPARATOR_MODEL_DIR"] = prev_audio_sep_model_dir
                    if vocal_separator is not None:
                        try:
                            if hasattr(vocal_separator, "model_instance"):
                                vocal_separator.model_instance = None
                        except Exception:
                            pass
                        vocal_separator = None
                    if vocal_temp_dir is not None:
                        try:
                            vocal_temp_dir.cleanup()
                        except Exception:
                            pass
                        vocal_temp_dir = None
                    _torch_gc(torch)
            else:
                print(
                    f"[colabone-longcat16] WARNING: Kim_Vocal_2.onnx not found at "
                    f"{vocal_model_path}, falling back to raw speech WAV",
                    file=sys.stderr,
                    flush=True,
                )

        if speech_array is None:
            speech_array, sr = _load_audio_mono_16k(
                primary_audio_path,
                ffmpeg_bin=ffmpeg_bin,
                target_sr=16000,
            )

        # Truncate overlong audio before tail zero-padding so Whisper only processes
        # the window needed for all planned segments.
        max_audio_samples = math.ceil(audio_process_duration * sr)
        if len(speech_array) > max_audio_samples:
            speech_array = speech_array[:max_audio_samples]

        # Pad speech tail with zeros up to audio_process_duration so Whisper covers every segment.
        source_duration = len(speech_array) / float(sr)
        added_sample_nums = math.ceil((audio_process_duration - source_duration) * sr)
        if added_sample_nums > 0:
            speech_array = np.concatenate(
                [speech_array, np.zeros(added_sample_nums, dtype=np.float32)]
            )

        # Detect GPU compute capability and run architecture-gated SageAttention + FlashAttention probes.
        try:
            major, minor = torch.cuda.get_device_capability(device)
        except Exception:
            major, minor = 0, 0
        has_sage3, has_sage2 = _probe_sage_attn_availability(
            device=device,
            major=int(major),
            backend=attention_backend,
        )
        attn_flags = resolve_longcat16_attention_flags(
            attention_backend,
            major=int(major),
            minor=int(minor),
            has_sage3=has_sage3,
            has_sage2=has_sage2,
        )
        has_fa3 = False
        has_fa2 = False
        if not attn_flags["enable_sageattn3"] and not attn_flags["enable_sageattn2"]:
            has_fa3, has_fa2 = _probe_flash_attn_availability(
                device=device,
                major=int(major),
                backend=attention_backend,
            )
            attn_flags = resolve_longcat16_attention_flags(
                attention_backend,
                major=int(major),
                minor=int(minor),
                has_fa3=has_fa3,
                has_fa2=has_fa2,
                has_sage3=has_sage3,
                has_sage2=has_sage2,
            )

        if attn_flags["enable_sageattn3"]:
            resolved_attn_name = "sageattn_3"
        elif attn_flags["enable_sageattn2"]:
            resolved_attn_name = "sageattn_2"
        elif attn_flags["enable_flashattn3"]:
            resolved_attn_name = "flash_attn_3"
        elif attn_flags["enable_flashattn2"]:
            resolved_attn_name = "flash_attn_2"
        else:
            resolved_attn_name = "sdpa"

        emit_colabone_progress(
            "load_models",
            0.10,
            "正在加载 LongCat-Video-Avatar 1.6 模型权重",
            attention_backend=resolved_attn_name,
        )

        # Load pure BF16 base video components on CPU.
        # Keep text_encoder, vae, and dit on CPU while Whisper extracts audio features
        # so ~40 GiB of diffusion/text weights never stack with Whisper in VRAM.
        tokenizer = AutoTokenizer.from_pretrained(
            base_video_dir,
            subfolder="tokenizer",
        )
        text_encoder = UMT5EncoderModel.from_pretrained(
            base_video_dir,
            subfolder="text_encoder",
            torch_dtype=torch.bfloat16,
        )
        vae = AutoencoderKLWan.from_pretrained(
            base_video_dir,
            subfolder="vae",
            torch_dtype=torch.bfloat16,
        )
        scheduler = FlowMatchEulerDiscreteScheduler.from_pretrained(
            avatar_checkpoint_dir,
            subfolder="scheduler",
            torch_dtype=torch.bfloat16,
        )

        emit_colabone_progress(
            "load_models",
            0.15,
            "正在加载 BF16 Avatar 1.6 DiT 主模型",
        )

        # Load BF16 Avatar 1.6 DiT on CPU (never load base_model_int8).
        dit = LongCatVideoAvatarTransformer3DModel.from_pretrained(
            avatar_checkpoint_dir,
            subfolder="base_model",
            cp_split_hw=cp_split_hw,
            enable_flashattn3=attn_flags["enable_flashattn3"],
            enable_flashattn2=attn_flags["enable_flashattn2"],
            enable_xformers=False,
            torch_dtype=torch.bfloat16,
        )

        # Ensure every block enforces the resolved FlashAttention flags.
        for block in dit.blocks:
            for attn_submod in (
                getattr(block, "attn", None),
                getattr(block, "cross_attn", None),
                getattr(block, "audio_cross_attn", None),
            ):
                if attn_submod is None:
                    continue
                if hasattr(attn_submod, "enable_flashattn3"):
                    attn_submod.enable_flashattn3 = attn_flags["enable_flashattn3"]
                if hasattr(attn_submod, "enable_flashattn2"):
                    attn_submod.enable_flashattn2 = attn_flags["enable_flashattn2"]
                if hasattr(attn_submod, "enable_xformers"):
                    attn_submod.enable_xformers = False

        sdpa_backend_label = "none"
        accel_attn_state: dict[str, Any] | None = None
        if not attn_flags["enable_flashattn3"] and not attn_flags["enable_flashattn2"]:
            preferred_sdpa_backend, sdpa_backend_label = _select_fastest_sdpa_backend(
                torch, device
            )
            if attn_flags["enable_sageattn3"] or attn_flags["enable_sageattn2"]:
                accel_attn_state = _install_accelerated_attention(
                    dit,
                    torch,
                    use_sage3=attn_flags["enable_sageattn3"],
                    use_sage2=bool(attn_flags["enable_sageattn2"] or has_sage2),
                    preferred_sdpa_backend=preferred_sdpa_backend,
                )
            else:
                _install_sdpa_attention_fallback(
                    dit,
                    torch,
                    preferred_backend=preferred_sdpa_backend,
                )
            emit_colabone_progress(
                "load_models",
                0.18,
                f"已就绪 Avatar 1.6 DiT（注意力后端：{resolved_attn_name}/{sdpa_backend_label}）",
                attention_backend=resolved_attn_name,
                sdpa_backend=sdpa_backend_label,
            )

        # Whisper-Large-v3 audio encoder (Avatar v1.5/v1.6 architecture).
        # Run global audio embedding BEFORE moving DiT/VAE/UMT5 onto CUDA.
        model_type = "avatar-v1.5"
        audio_stride = 1
        audio_model_dir = os.path.join(avatar_checkpoint_dir, "whisper-large-v3")
        if not os.path.isdir(audio_model_dir):
            raise FileNotFoundError(
                f"whisper-large-v3 directory not found: {audio_model_dir}"
            )
        audio_encoder = get_audio_encoder(audio_model_dir, model_type).to(device)
        audio_feature_extractor = get_audio_feature_extractor(
            audio_model_dir, model_type
        )

        pipe = LongCatVideoAvatarPipeline(
            tokenizer=tokenizer,
            text_encoder=text_encoder,
            vae=vae,
            scheduler=scheduler,
            dit=dit,
            audio_encoder=audio_encoder,
            audio_feature_extractor=audio_feature_extractor,
            model_type=model_type,
        )

        emit_colabone_progress(
            "audio",
            0.20,
            "正在提取全局 Whisper-Large-v3 音频特征",
        )

        # Single-pass global audio embedding over the full audio (use_loudness_norm=False for v1.6).
        full_audio_emb = pipe.get_audio_embedding(
            speech_array,
            fps=fps * audio_stride,
            device=device,
            sample_rate=sr,
            model_type=model_type,
            use_loudness_norm=sampling["use_loudness_norm"],
        )
        isfinite_fn = getattr(torch, "isfinite", None)
        if callable(isfinite_fn):
            finite_mask = isfinite_fn(full_audio_emb)
            if callable(getattr(finite_mask, "all", None)):
                if not bool(finite_mask.all()):
                    raise ValueError(
                        "Broken Whisper audio embedding contains NaN or Inf values"
                    )
            elif bool(torch.isnan(full_audio_emb).any()):
                raise ValueError("Broken Whisper audio embedding contains NaN values")
        else:
            if bool(torch.isnan(full_audio_emb).any()):
                raise ValueError("Broken Whisper audio embedding contains NaN values")
            isinf_fn = getattr(torch, "isinf", None)
            if callable(isinf_fn) and bool(isinf_fn(full_audio_emb).any()):
                raise ValueError("Broken Whisper audio embedding contains Inf values")

        full_audio_emb = full_audio_emb.cpu()
        speech_array = None

        # Release Whisper-Large-v3 (~6.2 GiB FP32) immediately after global embedding extraction.
        pipe.audio_encoder = None
        audio_encoder = None
        _torch_gc(torch)

        # Move DiT onto CUDA, apply optional distillation LoRAs, FP8 FFN, and move pipeline components.
        dit = dit.to(device)
        pipe.dit = dit

        # Optional distillation LoRA mixing (DMD loraA + OPSD loraB) or static OPSD merge.
        fast_lora_swap_enabled = False
        dynamic_lora_modules: set[str] = set()
        if sampling["use_distill"]:
            if sampling["mix_steps"] > 0:
                dmd_lora_path = os.path.join(
                    avatar_checkpoint_dir, "loraA", "lora.safetensors"
                )
                opsd_lora_path = os.path.join(
                    avatar_checkpoint_dir, "loraB", "lora.safetensors"
                )
                missing_loras = [
                    p for p in (dmd_lora_path, opsd_lora_path) if not os.path.isfile(p)
                ]
                if missing_loras:
                    raise FileNotFoundError(
                        f"Distillation mixing LoRA missing: {missing_loras}"
                    )
                try:
                    dynamic_lora_modules = _install_fast_lora_mix_weight_swap(
                        dit,
                        dmd_lora_path,
                        opsd_lora_path,
                        sampling["mix_alpha"],
                        sampling["mix_steps"],
                        torch,
                        lora_network_dim=128,
                        lora_network_alpha=64,
                    )
                    fast_lora_swap_enabled = True
                except _ColabOneLoRaSwapUnrecoverableError:
                    raise
                except Exception as exc:
                    print(
                        f"[colabone-longcat16] INFO: static dual-weight LoRA swap unavailable "
                        f"({exc}); using upstream enable_lora_mix",
                        file=sys.stderr,
                        flush=True,
                    )
                    dit.load_lora(
                        dmd_lora_path,
                        "dmd",
                        multiplier=1.0,
                        lora_network_dim=128,
                        lora_network_alpha=64,
                    )
                    dit.load_lora(
                        opsd_lora_path,
                        "opsd",
                        multiplier=1.0,
                        lora_network_dim=128,
                        lora_network_alpha=64,
                    )
                    dit.enable_lora_mix(
                        "dmd",
                        "opsd",
                        sampling["mix_alpha"],
                        sampling["mix_steps"],
                    )
            else:
                opsd_lora_path = os.path.join(
                    avatar_checkpoint_dir, "loraB", "lora.safetensors"
                )
                if not os.path.isfile(opsd_lora_path):
                    raise FileNotFoundError(
                        f"Distillation LoRA not found: {opsd_lora_path}"
                    )
                dit.merge_lora(
                    opsd_lora_path,
                    lora_key="opsd",
                    multiplier=lora_multiplier,
                    lora_network_dim=128,
                    lora_network_alpha=64,
                )

        # Resolve Regional Compilation for DiT blocks BEFORE FP8 FFN setup so FP8
        # chooses compile-safe stateless ColabOneFp8Linear when DiT will be compiled.
        total_denoising_steps = num_segments * int(sampling["num_inference_steps"])
        has_torch_compile = callable(getattr(torch, "compile", None))
        if compile_dit_opt is True:
            compile_dit = True
        elif compile_dit_opt is False:
            compile_dit = False
        else:
            compile_dit = (
                has_torch_compile
                and _is_triton_importable()
                and int(major) >= 8
                and total_denoising_steps >= 16
            )

        # Calibrated FP8 Tensor Core FFN on sm_89+ (runs after static LoRA merge; skips dynamic LoRA targets).
        fp8_ffn_enabled = _enable_longcat16_fp8_ffn(
            dit,
            torch,
            device,
            major=int(major),
            minor=int(minor),
            fp8_ffn_opt=fp8_ffn_opt,
            skip_module_names=dynamic_lora_modules,
            will_compile_dit=bool(compile_dit and has_torch_compile),
        )

        pipe.to(device)

        # Always enable fused VAE padding and cache views regardless of torch.compile setting.
        for vae_opt_name in (
            "enable_encoder_fused_padding",
            "enable_encoder_cache_views",
            "enable_decoder_fused_padding",
            "enable_decoder_cache_views",
        ):
            opt_fn = getattr(pipe.vae, vae_opt_name, None)
            if callable(opt_fn):
                try:
                    opt_fn()
                except Exception:
                    pass

        # Bit-exact 3-way batched CFG fusion for non-distill CFG on >= 64 GiB GPUs (or explicit True).
        batched_cfg_state: dict[str, Any] | None = None
        if not sampling["use_distill"] and batched_cfg_opt is not False:
            total_vram_mib = _get_cuda_total_vram_mib(torch, device)
            if total_vram_mib <= 0 and cfg.get("vram_mib") is not None:
                total_vram_mib = _parse_strict_int(
                    cfg.get("vram_mib"), "vram_mib", default=0
                )
            want_batched_cfg = (
                True if batched_cfg_opt is True else (total_vram_mib >= 65536)
            )
            if want_batched_cfg:
                batched_cfg_state = _install_batched_cfg_runner(pipe, torch)

        # Keep eager references so any Dynamo/Inductor/Triton compile failure falls back cleanly.
        eager_dit_blocks = list(dit.blocks)
        eager_vae_encoder = pipe.vae.encoder
        eager_vae_decoder = pipe.vae.decoder
        compiled_any = False
        compile_dit_enabled = False

        def _restore_eager_compiled_modules() -> None:
            nonlocal compiled_any, compile_dit_enabled
            for block_idx, orig_block in enumerate(eager_dit_blocks):
                if hasattr(orig_block, "_compiled_call_impl"):
                    try:
                        orig_block._compiled_call_impl = None
                    except Exception:
                        pass
                dit.blocks[block_idx] = orig_block
            pipe.vae.encoder = eager_vae_encoder
            pipe.vae.decoder = eager_vae_decoder
            compiled_any = False
            compile_dit_enabled = False

        if compile_dit or compile_vae:
            emit_colabone_progress(
                "compile",
                0.25,
                "正在配置 torch.compile 区域算子编译",
            )
            if has_torch_compile:
                dynamo_cfg = getattr(getattr(torch, "_dynamo", None), "config", None)
                if dynamo_cfg is not None:
                    for cfg_attr, cfg_val in (
                        ("cache_size_limit", 128),
                        ("accumulated_cache_size_limit", 1024),
                        ("inline_inbuilt_nn_modules", True),
                    ):
                        if hasattr(dynamo_cfg, cfg_attr):
                            try:
                                setattr(dynamo_cfg, cfg_attr, cfg_val)
                            except Exception:
                                pass

        if compile_dit and has_torch_compile:
            try:
                regional_opts = {
                    "triton.cudagraphs": False,
                    "epilogue_fusion": False,
                }
                for block_idx, block in enumerate(dit.blocks):
                    block_compile_fn = getattr(block, "compile", None)
                    if callable(block_compile_fn):
                        block_compile_fn(
                            fullgraph=False,
                            dynamic=True,
                            options=regional_opts,
                        )
                    else:
                        dit.blocks[block_idx] = torch.compile(
                            block,
                            fullgraph=False,
                            dynamic=True,
                            options=regional_opts,
                        )
                compiled_any = True
                compile_dit_enabled = True
            except Exception as exc:
                for block_idx, orig_block in enumerate(eager_dit_blocks):
                    if hasattr(orig_block, "_compiled_call_impl"):
                        try:
                            orig_block._compiled_call_impl = None
                        except Exception:
                            pass
                    dit.blocks[block_idx] = orig_block
                compile_dit_enabled = False
                print(
                    f"[colabone-longcat16] WARNING: torch.compile on DiT blocks failed, "
                    f"falling back to eager mode: {exc}",
                    file=sys.stderr,
                    flush=True,
                )

        if compile_vae and has_torch_compile:
            try:
                pipe.vae.encoder = torch.compile(
                    pipe.vae.encoder,
                    fullgraph=False,
                    dynamic=False,
                    options={"triton.cudagraphs": False},
                )
                pipe.vae.decoder = torch.compile(
                    pipe.vae.decoder,
                    fullgraph=False,
                    dynamic=False,
                    options={"triton.cudagraphs": False},
                )
                compiled_any = True
            except Exception as exc:
                pipe.vae.encoder = eager_vae_encoder
                pipe.vae.decoder = eager_vae_decoder
                print(
                    f"[colabone-longcat16] WARNING: torch.compile on VAE failed, "
                    f"falling back to eager mode: {exc}",
                    file=sys.stderr,
                    flush=True,
                )

        # Seed deterministic RNG for segment generation.
        _seed_all_generators(torch, np, seed)
        generator = torch.Generator(device=device)
        generator.manual_seed(seed)

        # Helper to slice the 5-frame audio context window for any segment start index.
        window_offsets = torch.arange(2 * 2 + 1) - 2

        def _slice_audio_emb(start_frame_idx: int) -> Any:
            audio_start = start_frame_idx * audio_stride
            audio_end = audio_start + audio_stride * num_frames
            center_indices = (
                torch.arange(audio_start, audio_end, audio_stride).unsqueeze(1)
                + window_offsets.unsqueeze(0)
            )
            center_indices = torch.clamp(
                center_indices,
                min=0,
                max=int(full_audio_emb.shape[0]) - 1,
            )
            return full_audio_emb[center_indices][None, ...].to(device)

        # Segment 0: AI2V anchor generation.
        anchor_image = load_image(image_path)
        seg0 = segment_plan[0]
        seg_num = 1
        audio_emb_0 = _slice_audio_emb(seg0["start_frame"])

        emit_colabone_progress(
            "generate",
            0.30,
            (
                f"正在渲染第 {seg_num}/{num_segments} 段（首段 AI2V · 首次算子编译中，请稍候）"
                if compiled_any
                else f"正在渲染第 {seg_num}/{num_segments} 段（首段 AI2V）"
            ),
            segment=seg_num,
            total_segments=num_segments,
            frames_written=0,
            exact_frames=exact_frames,
        )

        def _run_segment_0() -> Any:
            with _hook_pipe_step_progress(
                pipe,
                seg_index=0,
                num_segments=num_segments,
                seg_label="首段 AI2V",
                total_steps_hint=int(sampling["num_inference_steps"]),
                frames_written=0,
                exact_frames=exact_frames,
            ):
                return pipe.generate_ai2v(
                    image=anchor_image,
                    prompt=prompt,
                    negative_prompt=negative_prompt,
                    resolution=resolution,
                    num_frames=num_frames,
                    num_inference_steps=sampling["num_inference_steps"],
                    text_guidance_scale=sampling["text_guidance_scale"],
                    audio_guidance_scale=sampling["audio_guidance_scale"],
                    output_type="both",
                    generator=generator,
                    audio_emb=audio_emb_0,
                    use_distill=sampling["use_distill"],
                )

        compile_retry_err: str | None = None
        output_tuple: Any = None
        try:
            output_tuple = _run_segment_0()
        except Exception as exc:
            if not (compiled_any and _is_torch_compile_runtime_error(exc)):
                raise
            compile_retry_err = str(exc)
            traceback.clear_frames(exc.__traceback__)

        if compile_retry_err is not None:
            print(
                f"[colabone-longcat16] WARNING: torch.compile runtime error during segment 1 "
                f"({compile_retry_err}); restoring eager DiT/VAE and retrying",
                file=sys.stderr,
                flush=True,
            )
            _restore_eager_compiled_modules()
            _clear_pipe_kv_cache(pipe)
            _clear_vae_cache(pipe)
            try:
                torch._dynamo.reset()
            except Exception:
                pass
            _torch_gc(torch)
            _seed_all_generators(torch, np, seed)
            generator = torch.Generator(device=device)
            generator.manual_seed(seed)
            output_tuple = _run_segment_0()

        output_0, latent = output_tuple
        video_array_0 = _to_uint8_rgb_frames(output_0)
        del output_0, output_tuple

        actual_height = int(video_array_0.shape[1])
        actual_width = int(video_array_0.shape[2])
        if actual_width != req_width or actual_height != req_height:
            print(
                f"[colabone-longcat16] INFO: AI2V resolved bucket {actual_width}x{actual_height} "
                f"(config requested {req_width}x{req_height})",
                file=sys.stderr,
                flush=True,
            )

        # Capture anchor reference latent (first latent frame) for all AVC continuations.
        ref_latent = latent[:, :, :1].clone()

        # Keep only the tail num_cond_frames as PIL images so host RAM stays bounded.
        current_video = [
            PIL.Image.fromarray(frame) for frame in video_array_0[-num_cond_frames:]
        ]

        # Open streaming lossless FFV1 MKV pipe and write segment 0 frames up to exact_frames.
        ffmpeg_writer = _ColabOneFfv1PipeWriter(
            ffmpeg_bin=ffmpeg_bin,
            width=actual_width,
            height=actual_height,
            fps=fps,
            output_path=output_video_path,
        )
        emit_slice_0 = video_array_0[: seg0["emit_frames"]]
        ffmpeg_writer.write_frames(emit_slice_0)
        audio_emb_0 = None
        del video_array_0, emit_slice_0

        # Offload UMT5-XXL (~11.4 GiB BF16) to CPU after Segment 0 finishes since
        # pipe._prompt_cache already caches constant prompt embeddings.
        if getattr(pipe, "text_encoder", None) is not None:
            pipe.text_encoder = pipe.text_encoder.to("cpu")
            text_encoder = pipe.text_encoder
        _clear_pipe_kv_cache(pipe)
        _torch_gc(torch)

        emit_colabone_progress(
            "generate",
            0.30 + 0.65 * (1.0 / float(num_segments)),
            f"已完成第 {seg_num}/{num_segments} 段（累计 {ffmpeg_writer.frames_written}/{exact_frames} 帧）",
            segment=seg_num,
            total_segments=num_segments,
            frames_written=ffmpeg_writer.frames_written,
            exact_frames=exact_frames,
        )

        # Segments 1..N-1: AVC long-video continuation with anchor ref_latent and streaming FFV1 output.
        for seg in segment_plan[1:]:
            seg_num = seg["segment_index"] + 1
            emit_colabone_progress(
                "generate",
                0.30 + 0.65 * (float(seg["segment_index"]) / float(num_segments)),
                f"正在渲染第 {seg_num}/{num_segments} 段（13 帧连续续写）",
                segment=seg_num,
                total_segments=num_segments,
                frames_written=ffmpeg_writer.frames_written,
                exact_frames=exact_frames,
            )

            audio_emb_seg = _slice_audio_emb(seg["start_frame"])

            def _run_segment_avc() -> Any:
                with _hook_pipe_step_progress(
                    pipe,
                    seg_index=int(seg["segment_index"]),
                    num_segments=num_segments,
                    seg_label="13 帧连续续写",
                    total_steps_hint=int(sampling["num_inference_steps"]),
                    frames_written=ffmpeg_writer.frames_written,
                    exact_frames=exact_frames,
                ):
                    return pipe.generate_avc(
                        video=current_video,
                        video_latent=latent,
                        prompt=prompt,
                        negative_prompt=negative_prompt,
                        height=actual_height,
                        width=actual_width,
                        num_frames=num_frames,
                        num_cond_frames=num_cond_frames,
                        num_inference_steps=sampling["num_inference_steps"],
                        text_guidance_scale=sampling["text_guidance_scale"],
                        audio_guidance_scale=sampling["audio_guidance_scale"],
                        generator=generator,
                        output_type="both",
                        use_kv_cache=sampling["use_kv_cache"],
                        offload_kv_cache=False,
                        enhance_hf=sampling["enhance_hf"],
                        audio_emb=audio_emb_seg,
                        ref_latent=ref_latent,
                        ref_img_index=ref_img_index,
                        mask_frame_range=mask_frame_range,
                        use_distill=sampling["use_distill"],
                    )

            seg_gen_state = generator.get_state()
            compile_retry_err = None
            output_tuple = None
            try:
                output_tuple = _run_segment_avc()
            except Exception as exc:
                if not (compiled_any and _is_torch_compile_runtime_error(exc)):
                    raise
                compile_retry_err = str(exc)
                traceback.clear_frames(exc.__traceback__)

            if compile_retry_err is not None:
                print(
                    f"[colabone-longcat16] WARNING: torch.compile runtime error during segment {seg_num} "
                    f"({compile_retry_err}); restoring eager DiT/VAE and retrying",
                    file=sys.stderr,
                    flush=True,
                )
                _restore_eager_compiled_modules()
                _clear_pipe_kv_cache(pipe)
                _clear_vae_cache(pipe)
                try:
                    torch._dynamo.reset()
                except Exception:
                    pass
                _torch_gc(torch)
                generator.set_state(seg_gen_state)
                output_tuple = _run_segment_avc()

            # Free LongCat KV cache (~14.1 GiB) immediately after every AVC segment
            # without forcing an unconditional torch.cuda.empty_cache().
            _clear_pipe_kv_cache(pipe, flush_cuda_cache=False)

            output_seg, latent = output_tuple
            video_array_seg = _to_uint8_rgb_frames(output_seg)
            audio_emb_seg = None
            del output_seg, output_tuple

            # Keep only the final num_cond_frames for the next AVC continuation.
            current_video = [
                PIL.Image.fromarray(frame) for frame in video_array_seg[-num_cond_frames:]
            ]

            # Drop overlap prefix frames and clip strictly at exact_frames.
            drop_prefix = seg["drop_prefix_frames"]
            remaining = max(0, exact_frames - ffmpeg_writer.frames_written)
            emit_count = min(seg["emit_frames"], remaining)
            if emit_count > 0:
                emit_slice = video_array_seg[drop_prefix : drop_prefix + emit_count]
                ffmpeg_writer.write_frames(emit_slice)
                del emit_slice

            del video_array_seg
            _torch_gc_if_low_vram(torch, device)

            emit_colabone_progress(
                "generate",
                0.30 + 0.65 * (float(seg_num) / float(num_segments)),
                f"已完成第 {seg_num}/{num_segments} 段（累计 {ffmpeg_writer.frames_written}/{exact_frames} 帧）",
                segment=seg_num,
                total_segments=num_segments,
                frames_written=ffmpeg_writer.frames_written,
                exact_frames=exact_frames,
            )

        frames_written = ffmpeg_writer.frames_written
        if frames_written != exact_frames:
            raise RuntimeError(
                f"Frame count mismatch: wrote {frames_written} frames, expected {exact_frames}"
            )

        emit_colabone_progress(
            "finalize",
            0.97,
            "正在封装无损 FFV1 中间视频并写入输出清单",
            frames_written=frames_written,
            exact_frames=exact_frames,
        )

        ffmpeg_writer.close()
        ffmpeg_writer = None

        peak_vram_mib = 0.0
        try:
            peak_bytes = int(torch.cuda.max_memory_allocated(device))
            peak_vram_mib = round(peak_bytes / (1024.0 * 1024.0), 2)
        except Exception:
            peak_vram_mib = 0.0

        elapsed_seconds = round(time.monotonic() - start_time, 3)

        effective_attn_name = resolved_attn_name
        if accel_attn_state is not None:
            if accel_attn_state.get("use_sage3"):
                effective_attn_name = "sageattn_3"
            elif accel_attn_state.get("use_sage2"):
                effective_attn_name = "sageattn_2"
            else:
                effective_attn_name = "sdpa"

        batched_cfg_enabled = bool(
            batched_cfg_state is not None and batched_cfg_state.get("active", False)
        )

        manifest: dict[str, Any] = {
            "ok": True,
            "output_video_path": str(output_video_path),
            "width": actual_width,
            "height": actual_height,
            "fps": fps,
            "frames_written": frames_written,
            "segments": num_segments,
            "segment_plan": segment_plan,
            "sampling_mode": sampling["sampling_mode"],
            "vocal_separated": vocal_separated,
            "speakers": speakers,
            "speakers_count": len(speakers),
            "attention_backend": effective_attn_name,
            "sdpa_backend": sdpa_backend_label,
            "fp8_ffn_enabled": bool(fp8_ffn_enabled),
            "batched_cfg_enabled": batched_cfg_enabled,
            "compile_dit_enabled": bool(compile_dit_enabled),
            "fast_lora_swap_enabled": bool(fast_lora_swap_enabled),
            "elapsed_seconds": elapsed_seconds,
            "peak_vram_mib": peak_vram_mib,
        }
        _write_json_atomic(output_manifest_path, manifest)

        emit_colabone_progress(
            "done",
            1.0,
            f"LongCat-Video-Avatar 1.6 渲染完成（共 {frames_written} 帧，耗时 {elapsed_seconds:.1f} 秒）",
            frames_written=frames_written,
            segments=num_segments,
            elapsed_seconds=elapsed_seconds,
            peak_vram_mib=peak_vram_mib,
        )
        return manifest

    finally:
        if ffmpeg_writer is not None:
            ffmpeg_writer.abort()
        if vocal_separator is not None:
            try:
                if hasattr(vocal_separator, "model_instance"):
                    vocal_separator.model_instance = None
            except Exception:
                pass
            vocal_separator = None
        if vocal_temp_dir is not None:
            try:
                vocal_temp_dir.cleanup()
            except Exception:
                pass
            vocal_temp_dir = None
        _clear_pipe_kv_cache(pipe)
        _clear_vae_cache(pipe)
        audio_emb_seg = None
        audio_emb_0 = None
        full_audio_emb = None
        ref_latent = None
        latent = None
        eager_dit_blocks = None
        eager_vae_encoder = None
        eager_vae_decoder = None
        audio_encoder = None
        text_encoder = None
        vae = None
        dit = None
        pipe = None
        if initialized_pg and dist_mod is not None:
            try:
                if dist_mod.is_initialized():
                    dist_mod.destroy_process_group()
            except Exception:
                pass
        if torch_mod is not None:
            _torch_gc(torch_mod)
        if can_install_signals:
            try:
                if prev_sigterm is not None:
                    signal.signal(signal.SIGTERM, prev_sigterm)
                if prev_sigint is not None:
                    signal.signal(signal.SIGINT, prev_sigint)
            except ValueError:
                pass


class _ColabOneStepProgressBarProxy:
    """Wrap a diffusers/tqdm progress bar to emit live per-step COLABONE_PROGRESS updates."""

    def __init__(self, inner: Any, *, total_steps: int, on_step: Any) -> None:
        self._inner = inner
        self._total_steps = max(1, int(total_steps))
        self._on_step = on_step
        self._current = 0

    def __enter__(self) -> "_ColabOneStepProgressBarProxy":
        enter_fn = getattr(self._inner, "__enter__", None)
        if callable(enter_fn):
            enter_fn()
        return self

    def __exit__(self, exc_type: Any, exc: Any, tb: Any) -> Any:
        exit_fn = getattr(self._inner, "__exit__", None)
        if callable(exit_fn):
            return exit_fn(exc_type, exc, tb)
        return False

    def __iter__(self) -> Any:
        for item in self._inner:
            yield item
            self._current += 1
            self._on_step(self._current, self._total_steps)

    def update(self, n: int = 1) -> Any:
        step_inc = max(1, int(n)) if isinstance(n, (int, float)) else 1
        self._current += step_inc
        self._on_step(self._current, self._total_steps)
        update_fn = getattr(self._inner, "update", None)
        if callable(update_fn):
            return update_fn(n)
        return None

    def __getattr__(self, name: str) -> Any:
        return getattr(self._inner, name)


@contextlib.contextmanager
def _hook_pipe_step_progress(
    pipe: Any,
    *,
    seg_index: int,
    num_segments: int,
    seg_label: str,
    total_steps_hint: int,
    frames_written: int,
    exact_frames: int,
) -> Any:
    orig_progress_bar = getattr(pipe, "progress_bar", None)
    has_pipe_bar = callable(orig_progress_bar)
    pipe_mod = sys.modules.get(getattr(type(pipe), "__module__", ""))
    orig_mod_tqdm = getattr(pipe_mod, "tqdm", None) if pipe_mod is not None else None
    has_mod_tqdm = (
        pipe_mod is not None
        and callable(orig_mod_tqdm)
        and getattr(pipe_mod, "__name__", "") not in ("builtins", "unittest.mock")
    )
    if not has_pipe_bar and not has_mod_tqdm:
        yield
        return

    had_instance_attr = "progress_bar" in getattr(pipe, "__dict__", {})
    seg_num = seg_index + 1
    last_emitted_step = 0
    last_step_ts = time.monotonic()

    def _on_step(step: int, total: int) -> None:
        nonlocal last_emitted_step, last_step_ts
        bounded_total = max(1, int(total))
        bounded_step = min(bounded_total, max(1, int(step)))
        if bounded_step <= last_emitted_step:
            return
        now_ts = time.monotonic()
        step_delta = max(0.0, now_ts - last_step_ts)
        step_count_delta = max(1, bounded_step - last_emitted_step)
        sec_per_step = round(step_delta / float(step_count_delta), 2)
        last_step_ts = now_ts
        last_emitted_step = bounded_step
        frac_in_seg = float(bounded_step) / float(bounded_total)
        overall = 0.30 + 0.65 * (
            (float(seg_index) + 0.96 * frac_in_seg) / float(max(1, num_segments))
        )
        emit_colabone_progress(
            "generate",
            overall,
            (
                f"正在渲染第 {seg_num}/{num_segments} 段"
                f"（{seg_label} · 第 {bounded_step}/{bounded_total} 步，{sec_per_step:.1f} 秒/步）"
            ),
            segment=seg_num,
            total_segments=num_segments,
            step=bounded_step,
            total_steps=bounded_total,
            step_seconds=sec_per_step,
            frames_written=frames_written,
            exact_frames=exact_frames,
        )

    def _resolve_total(iterable: Any, total: Any) -> int:
        resolved_total = max(1, int(total_steps_hint))
        if isinstance(total, int) and not isinstance(total, bool) and total > 0:
            return total
        if iterable is not None:
            try:
                ln = len(iterable)
                if ln > 0:
                    return ln
            except TypeError:
                pass
        return resolved_total

    def _wrapped_progress_bar(
        iterable: Any = None, total: int | None = None, **kwargs: Any
    ) -> Any:
        inner = orig_progress_bar(iterable=iterable, total=total, **kwargs)
        return _ColabOneStepProgressBarProxy(
            inner,
            total_steps=_resolve_total(iterable, total),
            on_step=_on_step,
        )

    def _wrapped_mod_tqdm(*args: Any, **kwargs: Any) -> Any:
        inner = orig_mod_tqdm(*args, **kwargs)
        desc = str(kwargs.get("desc") or "").strip().lower()
        iterable = args[0] if args else kwargs.get("iterable")
        total = kwargs.get("total")
        is_denoising = (
            "denois" in desc
            or (
                not desc
                and isinstance(total, int)
                and not isinstance(total, bool)
                and total == int(total_steps_hint)
            )
        )
        if not is_denoising:
            return inner
        return _ColabOneStepProgressBarProxy(
            inner,
            total_steps=_resolve_total(iterable, total),
            on_step=_on_step,
        )

    if has_pipe_bar:
        pipe.progress_bar = _wrapped_progress_bar
    if has_mod_tqdm:
        setattr(pipe_mod, "tqdm", _wrapped_mod_tqdm)
    try:
        yield
    finally:
        if has_mod_tqdm:
            try:
                setattr(pipe_mod, "tqdm", orig_mod_tqdm)
            except Exception:
                pass
        if has_pipe_bar:
            if had_instance_attr:
                pipe.progress_bar = orig_progress_bar
            else:
                try:
                    delattr(pipe, "progress_bar")
                except AttributeError:
                    pipe.progress_bar = orig_progress_bar


_NON_TORCH_BLOCKED_MODULES = frozenset(
    {"tensorflow", "tf_keras", "keras", "jax", "jaxlib", "flax"}
)


class _ColabOneNonTorchMetaPathFilter:
    _colabone_non_torch_filter = True

    def __init__(self, inner: Any) -> None:
        self._inner = inner

    def find_spec(self, fullname: str, path: Any = None, target: Any = None) -> Any:
        if str(fullname).partition(".")[0] in _NON_TORCH_BLOCKED_MODULES:
            return None
        find_fn = getattr(self._inner, "find_spec", None)
        if callable(find_fn):
            return find_fn(fullname, path, target)
        return None

    def __getattr__(self, name: str) -> Any:
        return getattr(self._inner, name)


def _disable_non_torch_backends() -> None:
    """Force PyTorch-only mode and hide Colab host TensorFlow/JAX/Flax without polluting sys.modules."""
    for env_key, env_val in (
        ("USE_TORCH", "1"),
        ("USE_TF", "0"),
        ("USE_FLAX", "0"),
        ("USE_JAX", "0"),
        ("TRANSFORMERS_NO_TF", "1"),
        ("TRANSFORMERS_NO_FLAX", "1"),
        ("TRANSFORMERS_NO_JAX", "1"),
        ("DISABLE_TENSORFLOW", "1"),
        ("TF_CPP_MIN_LOG_LEVEL", "3"),
    ):
        os.environ.setdefault(env_key, env_val)
    for mod_name in _NON_TORCH_BLOCKED_MODULES:
        if sys.modules.get(mod_name) is None:
            sys.modules.pop(mod_name, None)
    sys.meta_path[:] = [
        f
        if getattr(f, "_colabone_non_torch_filter", False)
        else _ColabOneNonTorchMetaPathFilter(f)
        for f in sys.meta_path
    ]


def _try_write_error_manifest(
    manifest_path_raw: Any,
    error_message: str,
    *,
    terminated_signal: int | None = None,
) -> None:
    if not manifest_path_raw:
        return
    try:
        target = _ensure_not_symlink(manifest_path_raw, "output_manifest_path")
        payload: dict[str, Any] = {
            "ok": False,
            "error": str(error_message),
        }
        if terminated_signal is not None:
            payload["terminated_signal"] = int(terminated_signal)
        _write_json_atomic(target, payload)
    except Exception:
        pass


def main(argv: list[str] | None = None) -> int:
    _ensure_utf8_stdio()
    _disable_non_torch_backends()
    parser = argparse.ArgumentParser(
        description="ColabOne Meituan LongCat-Video-Avatar 1.6 remote GPU worker"
    )
    parser.add_argument(
        "--config",
        required=True,
        help="Path to the worker JSON configuration file.",
    )
    args = parser.parse_args(argv)

    manifest_path_raw: Any = None
    try:
        safe_config = _validate_safe_arg(args.config, "config")
        config_path = Path(safe_config)
        if config_path.is_symlink():
            raise ValueError(f"config must not be a symlink: {config_path}")
        if not config_path.is_file():
            print(
                f"[colabone-longcat16] ERROR: config file not found: {config_path}",
                file=sys.stderr,
                flush=True,
            )
            return 2

        cfg = json.loads(config_path.read_text(encoding="utf-8"))
        if isinstance(cfg, dict):
            manifest_path_raw = cfg.get("output_manifest_path")
        run_longcat16_generation(cfg)
        return 0
    except ColabOneLongCat16Terminated as exc:
        _try_write_error_manifest(
            manifest_path_raw,
            str(exc),
            terminated_signal=exc.signum,
        )
        print(
            f"[colabone-longcat16] Terminated cleanly on signal {exc.signum}",
            file=sys.stderr,
            flush=True,
        )
        return 128 + exc.signum
    except Exception as exc:
        _try_write_error_manifest(manifest_path_raw, str(exc))
        print(
            f"[colabone-longcat16] ERROR: {exc}",
            file=sys.stderr,
            flush=True,
        )
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
