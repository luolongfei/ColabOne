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
import datetime
import gc
import json
import math
import os
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
_VALID_ATTENTION_BACKENDS = frozenset({"auto", "flash_attn_3", "flash_attn_2", "sdpa"})
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


def resolve_longcat16_attention_flags(
    backend: str,
    *,
    major: int = 0,
    minor: int = 0,
    has_fa3: bool = False,
    has_fa2: bool = False,
) -> dict[str, bool]:
    """Resolve LongCat DiT attention flags without ever enabling quantized SageAttention.

    - FlashAttention-3 is strictly Hopper-only (major in (0, 9)): never enabled on
      non-Hopper GPUs (Blackwell/Ada/Ampere/Turing/Volta), degrading to FA2 when
      major >= 8 and has_fa2, else SDPA.
    - FlashAttention-2 requires Ampere+ (major == 0 or major >= 8): never enabled on
      Turing (sm_75) or Volta (sm_70), degrading cleanly to SDPA.
    - In auto mode: Hopper (major == 9) prefers FA3 then FA2 then SDPA; Ampere/Ada/
      Blackwell (major >= 8 or major == 0) prefer FA2 then SDPA; older GPUs use SDPA.
    - Explicit sdpa always disables both FA3 and FA2.
    """
    major_int = _require_int(major, "major")
    _ = _require_int(minor, "minor")  # Reserved for architecture-specific checks.
    normalized = str(backend if backend is not None else "auto").strip().lower() or "auto"
    if normalized not in _VALID_ATTENTION_BACKENDS:
        raise ValueError(
            f"Unsupported attention_backend {backend!r}; "
            f"expected one of {sorted(_VALID_ATTENTION_BACKENDS)}"
        )

    fa3_allowed = (major_int in (0, 9)) and bool(has_fa3)
    fa2_allowed = (major_int == 0 or major_int >= 8) and bool(has_fa2)

    if normalized == "sdpa":
        return {"enable_flashattn3": False, "enable_flashattn2": False}

    if normalized == "flash_attn_3":
        if fa3_allowed:
            return {"enable_flashattn3": True, "enable_flashattn2": False}
        if fa2_allowed:
            return {"enable_flashattn3": False, "enable_flashattn2": True}
        return {"enable_flashattn3": False, "enable_flashattn2": False}

    if normalized == "flash_attn_2":
        if fa2_allowed:
            return {"enable_flashattn3": False, "enable_flashattn2": True}
        return {"enable_flashattn3": False, "enable_flashattn2": False}

    # Auto selection: Hopper (sm_90) prefers FA3; Blackwell/Ampere/Ada prefer FA2.
    if major_int == 9:
        if fa3_allowed:
            return {"enable_flashattn3": True, "enable_flashattn2": False}
        if fa2_allowed:
            return {"enable_flashattn3": False, "enable_flashattn2": True}
        return {"enable_flashattn3": False, "enable_flashattn2": False}

    if fa2_allowed:
        return {"enable_flashattn3": False, "enable_flashattn2": True}
    return {"enable_flashattn3": False, "enable_flashattn2": False}


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
        "file,pipe",
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


def emit_colabone_progress(
    stage: str,
    progress: float,
    message: str,
    **extra: Any,
) -> None:
    """Emit a flushed COLABONE_PROGRESS JSON event line to stdout."""
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
    print(line, flush=True)


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

    if major_int in (0, 9) and normalized in ("auto", "flash_attn_3"):
        try:
            import flash_attn_interface  # type: ignore[import-not-found]

            has_fa3 = _smoke_test_flash_attn_module(flash_attn_interface, device)
        except Exception:
            has_fa3 = False

    if (
        (major_int == 0 or major_int >= 8)
        and normalized in ("auto", "flash_attn_3", "flash_attn_2")
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
        "file,pipe",
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


def _install_sdpa_attention_fallback(dit: Any, torch_mod: Any) -> None:
    """Patch SDPA fallback onto LongCat attention modules when FA3/FA2 are both disabled.

    Upstream LongCat Attention._process_attn raises RuntimeError when FA3, FA2, and
    xFormers are all False, and SingleStreamAttention._process_cross_attn leaves the
    output unassigned. Installing PyTorch F.scaled_dot_product_attention guarantees
    pure BF16 SDPA execution without xFormers or quantized attention kernels.
    """
    from einops import rearrange  # type: ignore[import-not-found]

    f_mod = torch_mod.nn.functional

    def _sdpa_process_attn(self_attn: Any, q: Any, k: Any, v: Any, shape: Any) -> Any:
        _ = shape
        if q.dtype != v.dtype:
            q = q.to(v.dtype)
        if k.dtype != v.dtype:
            k = k.to(v.dtype)
        return f_mod.scaled_dot_product_attention(
            q,
            k,
            v,
            dropout_p=0.0,
            scale=self_attn.scale,
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

        if q.dtype != encoder_v.dtype:
            q = q.to(encoder_v.dtype)
        if encoder_k.dtype != encoder_v.dtype:
            encoder_k = encoder_k.to(encoder_v.dtype)
        attn_out = f_mod.scaled_dot_product_attention(
            q,
            encoder_k,
            encoder_v,
            dropout_p=0.0,
            scale=self_mod.scale,
        )
        attn_out = attn_out.transpose(1, 2).reshape(batch_nt, seq_len, hidden)
        attn_out = self_mod.proj_drop(self_mod.proj(attn_out))
        attn_out = rearrange(attn_out, "(B N_t) S C -> B (N_t S) C", N_t=n_t)
        return attn_out.to(out_dtype)

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
            if q_all.dtype != v_all.dtype:
                q_all = q_all.to(v_all.dtype)
            if k_all.dtype != v_all.dtype:
                k_all = k_all.to(v_all.dtype)
            out = f_mod.scaled_dot_product_attention(
                q_all,
                k_all,
                v_all,
                dropout_p=0.0,
                scale=scale,
            )
        else:
            raw_lens = kv_seqlen.tolist() if hasattr(kv_seqlen, "tolist") else kv_seqlen
            if isinstance(raw_lens, int) and not isinstance(raw_lens, bool):
                seq_lens = [int(raw_lens)] * batch_size
            else:
                seq_lens = [int(s) for s in raw_lens]
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
                        f_mod.scaled_dot_product_attention(
                            q_all[b_idx : b_idx + 1],
                            k_all[b_idx : b_idx + 1, :, :seq_k, :],
                            v_all[b_idx : b_idx + 1, :, :seq_k, :],
                            dropout_p=0.0,
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
                        f_mod.scaled_dot_product_attention(
                            q_all[b_idx : b_idx + 1],
                            k_slice,
                            v_slice,
                            dropout_p=0.0,
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

    for block in dit.blocks:
        attn_mod = getattr(block, "attn", None)
        if attn_mod is not None and hasattr(attn_mod, "_process_attn"):
            attn_mod._process_attn = types.MethodType(_sdpa_process_attn, attn_mod)
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


def _is_torch_compile_runtime_error(exc: BaseException) -> bool:
    cls = type(exc)
    mod_name = str(getattr(cls, "__module__", "") or "").lower()
    cls_name = str(getattr(cls, "__name__", "") or "").lower()
    msg = str(exc).lower()
    tb_parts: list[str] = []
    try:
        tb_parts.append(traceback.format_exc())
    except Exception:
        pass
    try:
        tb_parts.append(
            "".join(traceback.format_exception(type(exc), exc, exc.__traceback__))
        )
    except Exception:
        pass
    tb_text = "\n".join(tb_parts).replace("\\", "/").lower()
    return (
        "dynamo" in mod_name
        or "inductor" in mod_name
        or "triton" in mod_name
        or "functorch" in mod_name
        or "backendcompilerfailed" in cls_name
        or "inductorerror" in cls_name
        or "torch._dynamo" in msg
        or "torch._inductor" in msg
        or "triton" in msg
        or "ptxas" in msg
        or "nvrtc" in msg
        or "torch/_dynamo" in tb_text
        or "torch/_inductor" in tb_text
        or "torch/_functorch" in tb_text
        or "torch/_subclasses" in tb_text
        or "triton/" in tb_text
        or "ptxas" in tb_text
        or "nvrtc" in tb_text
    )


def _clear_pipe_kv_cache(pipe_obj: Any) -> None:
    if pipe_obj is None:
        return
    clear_fn = getattr(pipe_obj, "_clear_cache", None)
    if callable(clear_fn):
        try:
            clear_fn()
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


class _ColabOneFfv1PipeWriter:
    """Streaming lossless FFV1 MKV writer that bounds RAM by piping RGB24 frames."""

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
        self._stderr_lines: collections.deque[str] = collections.deque(maxlen=200)
        self._proc: subprocess.Popen[bytes] | None = subprocess.Popen(
            self.cmd,
            stdin=subprocess.PIPE,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.PIPE,
            env=_sanitized_subprocess_env(),
        )
        self._stderr_thread = threading.Thread(
            target=self._drain_stderr,
            name="colabone-ffv1-stderr",
            daemon=True,
        )
        self._stderr_thread.start()
        self.frames_written = 0
        self._closed = False

    def _drain_stderr(self) -> None:
        proc = self._proc
        if proc is None or proc.stderr is None:
            return
        try:
            for raw_line in iter(proc.stderr.readline, b""):
                text = raw_line.decode("utf-8", errors="replace").rstrip()
                if text:
                    self._stderr_lines.append(text)
        except Exception:
            pass
        finally:
            try:
                proc.stderr.close()
            except Exception:
                pass

    def write_frames(self, frames_uint8: Any) -> int:
        import numpy as np

        if self._closed or self._proc is None or self._proc.stdin is None:
            raise RuntimeError("FFV1 writer pipe is already closed")

        arr = np.asarray(frames_uint8)
        if arr.ndim != 4 or arr.shape[1:] != (self.height, self.width, 3):
            raise ValueError(
                f"Expected frame batch shape (N, {self.height}, {self.width}, 3), "
                f"got {tuple(arr.shape)}"
            )
        if arr.dtype != np.uint8:
            arr = arr.astype(np.uint8, copy=False)

        written = 0
        stdin = self._proc.stdin
        try:
            for frame_idx in range(arr.shape[0]):
                frame = np.ascontiguousarray(arr[frame_idx])
                stdin.write(memoryview(frame).cast("B"))
                written += 1
                self.frames_written += 1
            stdin.flush()
        except (BrokenPipeError, OSError) as exc:
            tail = "\n".join(list(self._stderr_lines)[-20:])
            raise RuntimeError(
                f"FFmpeg FFV1 pipe failed after {self.frames_written} frames: {tail}"
            ) from exc
        return written

    def close(self) -> None:
        if self._closed:
            return
        self._closed = True
        proc = self._proc
        if proc is None:
            return
        replaced = False
        try:
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
                tail = "\n".join(list(self._stderr_lines)[-30:])
                raise RuntimeError(
                    f"FFmpeg FFV1 writer exited with code {returncode}: {tail}"
                )
            _ensure_not_symlink(self.output_path, "output_video_path")
            os.replace(self._tmp_output_path, self.output_path)
            replaced = True
        finally:
            if proc.poll() is None:
                if proc.stdin is not None:
                    try:
                        proc.stdin.close()
                    except Exception:
                        pass
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
        proc = self._proc
        try:
            if proc is not None:
                if proc.stdin is not None:
                    try:
                        proc.stdin.close()
                    except Exception:
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

    arr = np.asarray(output_video)
    if arr.ndim == 5:
        arr = arr[0]
    if arr.dtype == np.uint8:
        return arr
    if not np.isfinite(arr).all():
        raise ValueError(
            "Generated video tensor contains NaN or Inf values; aborting frame conversion"
        )
    return (np.clip(arr, 0.0, 1.0) * 255.0).astype(np.uint8)


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
    compile_dit = bool(True if cfg.get("compile_dit") is None else cfg.get("compile_dit"))
    compile_vae = bool(True if cfg.get("compile_vae") is None else cfg.get("compile_vae"))
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

        # Detect GPU compute capability and run architecture-gated CUDA FlashAttention probes.
        try:
            major, minor = torch.cuda.get_device_capability(device)
        except Exception:
            major, minor = 0, 0
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
        )
        resolved_attn_name = (
            "flash_attn_3"
            if attn_flags["enable_flashattn3"]
            else ("flash_attn_2" if attn_flags["enable_flashattn2"] else "sdpa")
        )

        emit_colabone_progress(
            "load_models",
            0.10,
            "正在加载纯 BF16 LongCat-Video-Avatar 1.6 模型权重",
            attention_backend=resolved_attn_name,
        )

        # Load pure BF16 base video components on CPU (zero FP8/INT8 quantization).
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

        # Load pure BF16 Avatar 1.6 DiT on CPU (never load base_model_int8 or enable FP8).
        dit = LongCatVideoAvatarTransformer3DModel.from_pretrained(
            avatar_checkpoint_dir,
            subfolder="base_model",
            cp_split_hw=cp_split_hw,
            enable_flashattn3=attn_flags["enable_flashattn3"],
            enable_flashattn2=attn_flags["enable_flashattn2"],
            enable_xformers=False,
            torch_dtype=torch.bfloat16,
        )

        # Ensure every block enforces the resolved attention backend flags.
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

        if not attn_flags["enable_flashattn3"] and not attn_flags["enable_flashattn2"]:
            _install_sdpa_attention_fallback(dit, torch)

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

        # Move DiT onto CUDA, apply optional distillation LoRAs, and move pipeline components (VAE & UMT5).
        dit = dit.to(device)
        pipe.dit = dit

        # Optional distillation LoRA mixing (DMD loraA + OPSD loraB) or static OPSD merge.
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

        pipe.to(device)

        # Keep eager references so any Dynamo/Inductor/Triton compile failure falls back cleanly.
        eager_dit_blocks = list(dit.blocks)
        eager_vae_encoder = pipe.vae.encoder
        eager_vae_decoder = pipe.vae.decoder
        compiled_any = False

        if compile_dit or compile_vae:
            emit_colabone_progress(
                "compile",
                0.25,
                "正在配置 torch.compile 无损算子编译",
            )
            if callable(getattr(torch, "compile", None)):
                try:
                    torch._dynamo.config.cache_size_limit = 128
                    torch._dynamo.config.accumulated_cache_size_limit = 1024
                except Exception:
                    pass

        if compile_dit and callable(getattr(torch, "compile", None)):
            try:
                for block_idx, block in enumerate(dit.blocks):
                    dit.blocks[block_idx] = torch.compile(
                        block,
                        fullgraph=False,
                        dynamic=False,
                        options={
                            "triton.cudagraphs": False,
                            "epilogue_fusion": False,
                            "emulate_precision_casts": True,
                        },
                    )
                compiled_any = True
            except Exception as exc:
                for block_idx, orig_block in enumerate(eager_dit_blocks):
                    dit.blocks[block_idx] = orig_block
                print(
                    f"[colabone-longcat16] WARNING: torch.compile on DiT blocks failed, "
                    f"falling back to eager mode: {exc}",
                    file=sys.stderr,
                    flush=True,
                )

        if compile_vae and callable(getattr(torch, "compile", None)):
            try:
                torch.backends.cudnn.benchmark = True
                for vae_opt_name in (
                    "enable_encoder_fused_padding",
                    "enable_encoder_cache_views",
                    "enable_decoder_fused_padding",
                    "enable_decoder_cache_views",
                ):
                    opt_fn = getattr(pipe.vae, vae_opt_name, None)
                    if callable(opt_fn):
                        opt_fn()
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
            f"正在渲染第 {seg_num}/{num_segments} 段（首段 AI2V）",
            segment=seg_num,
            total_segments=num_segments,
            frames_written=0,
            exact_frames=exact_frames,
        )

        def _run_segment_0() -> Any:
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
            for block_idx, orig_block in enumerate(eager_dit_blocks):
                dit.blocks[block_idx] = orig_block
            pipe.vae.encoder = eager_vae_encoder
            pipe.vae.decoder = eager_vae_decoder
            compiled_any = False
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
                for block_idx, orig_block in enumerate(eager_dit_blocks):
                    dit.blocks[block_idx] = orig_block
                pipe.vae.encoder = eager_vae_encoder
                pipe.vae.decoder = eager_vae_decoder
                compiled_any = False
                _clear_pipe_kv_cache(pipe)
                _clear_vae_cache(pipe)
                try:
                    torch._dynamo.reset()
                except Exception:
                    pass
                _torch_gc(torch)
                generator.set_state(seg_gen_state)
                output_tuple = _run_segment_avc()

            # Free LongCat KV cache (~14.1 GiB) immediately after every AVC segment.
            _clear_pipe_kv_cache(pipe)

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
            _torch_gc(torch)

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
            "attention_backend": resolved_attn_name,
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
