"""Standalone remote GPU worker for diffusion-based Video Super-Resolution (VSR).

Supports Stage 3 native-flagship upscaling inside an isolated ``vsr_venv``
environment AFTER Stage 2 (LongCat-Video-Avatar 1.6) has exited so this process
owns the full GPU VRAM (e.g. 96 GB Blackwell) exclusively:

- ``seedvr2_7b_sharp``: ByteDance SeedVR2 7B Sharp FP16 via
  ``numz/ComfyUI-SeedVR2_VideoUpscaler`` (pinned commit ``4490bd1f``)
- ``seedvr2_7b``: ByteDance SeedVR2 7B FP16 via
  ``numz/ComfyUI-SeedVR2_VideoUpscaler`` (pinned commit ``4490bd1f``)
- ``flashvsr_plus``: OpenImagingLab FlashVSR+ (FlashVSR-v1.1) streaming
  diffusion VSR via ``OpenImagingLab/FlashVSR_Plus`` (pinned commit ``b515ffc6``)

All planning, model-file resolution, CLI command construction, FFmpeg lossless
cover-crop command construction, and progress helpers are pure Python stdlib
and importable cleanly without ``torch``, ``cv2``, or ``numpy`` installed.
"""

from __future__ import annotations

import argparse
from collections import deque
import gc
import json
import math
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import threading
import time
from typing import Any

__all__ = [
    "COLABONE_PROGRESS_PREFIX",
    "COLABONE_VSR_COLOR_CORRECTION_MODES",
    "COLABONE_VSR_ENGINE_FILES",
    "COLABONE_VSR_HIGH_VRAM_THRESHOLD_MIB",
    "COLABONE_VSR_LOW_VRAM_THRESHOLD_MIB",
    "ColabOneProcessGuard",
    "ColabOneSeedVR2ProgressTracker",
    "align_seedvr2_batch_size",
    "atomic_write_json",
    "build_cover_crop_lossless_cmd",
    "build_flashvsr_scale_factor",
    "build_seedvr2_cli_command",
    "compute_flashvsr_chunk_plan",
    "compute_flashvsr_cover_dimensions",
    "compute_seedvr2_chunk_plan",
    "compute_seedvr2_streaming_chunk_size",
    "compute_upscale_short_side",
    "discover_flashvsr_entrypoint",
    "emit_colabone_progress",
    "parse_cli_help_flags",
    "parse_vsr_progress_line",
    "resolve_vsr_model_files",
    "run_diffusion_vsr",
]

COLABONE_PROGRESS_PREFIX = "COLABONE_PROGRESS:"

# Required weight files per diffusion VSR engine.
# Note: FlashVSR+ uses TinyDecoder (TCDecoder.ckpt) so Wan2.1_VAE.pth is optional.
COLABONE_VSR_ENGINE_FILES: dict[str, tuple[str, ...]] = {
    "seedvr2_7b_sharp": (
        "seedvr2_ema_7b_sharp_fp16.safetensors",
        "ema_vae_fp16.safetensors",
    ),
    "seedvr2_7b": (
        "seedvr2_ema_7b_fp16.safetensors",
        "ema_vae_fp16.safetensors",
    ),
    "flashvsr_plus": (
        "diffusion_pytorch_model_streaming_dmd.safetensors",
        "LQ_proj_in.ckpt",
        "TCDecoder.ckpt",
    ),
}

COLABONE_VSR_SEEDVR2_ENGINES: frozenset[str] = frozenset(
    {"seedvr2_7b_sharp", "seedvr2_7b"}
)
COLABONE_VSR_COLOR_CORRECTION_MODES: tuple[str, ...] = ("lab", "wavelet", "none")

# VRAM thresholds (MiB):
# - >= 65536 MiB (64+ GB, e.g. 80 GB A100 / 96 GB RTX PRO 6000): full FP16/BF16,
#   zero block swap, zero VAE tiling for maximum speed and spatial coherence.
# - 0 < vram_mib <= 49152 MiB (<= 48 GB, e.g. L40S / A6000): enable conservative
#   block swap + tiled VAE + CPU offload for SeedVR2 so 48 GB and smaller GPUs
#   complete safely without CUDA OOM.
COLABONE_VSR_HIGH_VRAM_THRESHOLD_MIB: int = 65536
COLABONE_VSR_LOW_VRAM_THRESHOLD_MIB: int = 49152
_SEEDVR2_LOW_VRAM_BLOCKS_TO_SWAP: int = 16
_SEEDVR2_STREAMING_FRAME_THRESHOLD: int = 264
_SEEDVR2_MAX_CHUNK_SIZE: int = 264

_FLASHVSR_SINGLE_PASS_THRESHOLD: int = 140
_FLASHVSR_CHUNK_FRAMES: int = 120
_FLASHVSR_CHUNK_OVERLAP: int = 8

# Verified CLI flags from pinned numz/ComfyUI-SeedVR2_VideoUpscaler @ 4490bd1f.
# Using this static verified map avoids spawning `inference_cli.py --help`,
# which cold-imports torch and diffusers for 15-30 seconds on every run.
_SEEDVR2_PINNED_4490BD1F_FLAGS: dict[str, bool] = {
    "--output": True,
    "--output_format": True,
    "--video_backend": True,
    "--10bit": False,
    "--model_dir": True,
    "--dit_model": True,
    "--resolution": True,
    "--max_resolution": True,
    "--batch_size": True,
    "--uniform_batch_size": False,
    "--seed": True,
    "--skip_first_frames": True,
    "--load_cap": True,
    "--chunk_size": True,
    "--prepend_frames": True,
    "--temporal_overlap": True,
    "--color_correction": True,
    "--input_noise_scale": True,
    "--latent_noise_scale": True,
    "--cuda_device": True,
    "--dit_offload_device": True,
    "--vae_offload_device": True,
    "--tensor_offload_device": True,
    "--blocks_to_swap": True,
    "--swap_io_components": False,
    "--vae_encode_tiled": False,
    "--vae_encode_tile_size": True,
    "--vae_encode_tile_overlap": True,
    "--vae_decode_tiled": False,
    "--vae_decode_tile_size": True,
    "--vae_decode_tile_overlap": True,
    "--tile_debug": True,
    "--attention_mode": True,
    "--cache_dit": False,
    "--cache_vae": False,
    "--debug": False,
}

# FFV1 lossless intra-only codec args using bgr0 (universally supported lossless
# 8-bit RGB pixel format across FFmpeg versions).
_COLABONE_FFV1_CODEC_ARGS: tuple[str, ...] = (
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
)

_COLABONE_FFMPEG_PROTOCOL_ARGS: tuple[str, ...] = (
    "-protocol_whitelist",
    "file,pipe",
)

_SENSITIVE_SUBPROCESS_ENV_KEYS: frozenset[str] = frozenset(
    {
        "HF_TOKEN",
        "HUGGING_FACE_HUB_TOKEN",
        "HUGGINGFACE_HUB_TOKEN",
        "HF_API_TOKEN",
    }
)
_FFMPEG_FPS_MODE_CACHE_LOCK = threading.Lock()
_FFMPEG_FPS_MODE_CACHE: dict[str, str] = {}

_HELP_FLAG_RE = re.compile(
    r"(--[a-zA-Z0-9][a-zA-Z0-9_-]*)"
    r"(?:(?:=|\s)(<[^>]+>|\{[^}]+\}|[A-Z][A-Z0-9_]{1,}))?"
)
_PROGRESS_RATIO_RE = re.compile(
    r"(?:batch|chunk|segment|frame|frames|step|steps)\s*[:#]?\s*(\d+)\s*/\s*(\d+)",
    re.IGNORECASE,
)
_PROGRESS_BAR_RATIO_RE = re.compile(r"\b(\d+)\s*/\s*(\d+)\s*\[")
_PROGRESS_PCT_RE = re.compile(r"\b(\d{1,3}(?:\.\d+)?)\s*%")
_SEEDVR2_CHUNK_RE = re.compile(
    r"\bchunk\s*[:#]?\s*(\d+)\s*/\s*(\d+)",
    re.IGNORECASE,
)
_SEEDVR2_PHASE_NUM_RE = re.compile(r"\bphase\s*([1-4])\b", re.IGNORECASE)
_NATURAL_SORT_RE = re.compile(r"(\d+)")
_VIDEO_SUFFIXES: frozenset[str] = frozenset({".mkv", ".mp4", ".mov", ".avi", ".webm"})

# SeedVR2 4-phase progress windows within a single chunk or full run.
_SEEDVR2_PHASE_WINDOWS: dict[int, tuple[float, float]] = {
    1: (0.00, 0.10),  # Phase 1: VAE encoding
    2: (0.10, 0.78),  # Phase 2: DiT upscaling
    3: (0.78, 0.92),  # Phase 3: VAE decoding
    4: (0.92, 1.00),  # Phase 4: Post-processing
}


class _ColabOneWorkerTerminated(BaseException):
    """Raised when the worker receives SIGTERM or SIGINT for graceful teardown."""

    def __init__(self, signum: int) -> None:
        self.signum = int(signum)
        super().__init__(f"Diffusion VSR worker terminated by signal {self.signum}")


class ColabOneProcessGuard:
    """Tracks child subprocesses and temporary directories inside ``work_dir``."""

    def __init__(self, base_dir: str | Path | None = None) -> None:
        self._base_dir: Path | None = (
            _validate_safe_path(base_dir, "work_dir") if base_dir is not None else None
        )
        self._children: list[subprocess.Popen[Any]] = []
        self._temp_dirs: list[Path] = []
        self.terminated_signal: int | None = None
        self._tearing_down: bool = False

    @property
    def base_dir(self) -> Path | None:
        return self._base_dir

    def register_child(self, proc: subprocess.Popen[Any]) -> None:
        self._children.append(proc)

    def unregister_child(self, proc: subprocess.Popen[Any]) -> None:
        try:
            self._children.remove(proc)
        except ValueError:
            pass

    def create_temp_dir(self, prefix: str = "colabone_vsr_") -> Path:
        if self._base_dir is not None:
            self._base_dir.mkdir(parents=True, exist_ok=True)
            path = Path(tempfile.mkdtemp(prefix=prefix, dir=str(self._base_dir)))
        else:
            path = Path(tempfile.mkdtemp(prefix=prefix))
        self._temp_dirs.append(path)
        return path

    def terminate_child(
        self,
        proc: subprocess.Popen[Any],
        grace_seconds: float = 1.5,
    ) -> None:
        if proc.poll() is not None:
            return
        try:
            proc.terminate()
        except OSError:
            pass
        deadline = time.monotonic() + max(0.1, float(grace_seconds))
        while proc.poll() is None:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                break
            try:
                proc.wait(timeout=min(0.25, remaining))
                return
            except subprocess.TimeoutExpired:
                continue
            except OSError:
                break
        if proc.poll() is None:
            try:
                proc.kill()
            except OSError:
                pass
            try:
                proc.wait(timeout=2.0)
            except Exception:
                pass

    def terminate_children(self, grace_seconds: float = 1.5) -> None:
        for proc in list(self._children):
            if proc.poll() is not None:
                continue
            try:
                proc.terminate()
            except OSError:
                pass

        deadline = time.monotonic() + max(0.1, float(grace_seconds))
        for proc in list(self._children):
            if proc.poll() is not None:
                continue
            remaining = max(0.05, deadline - time.monotonic())
            try:
                proc.wait(timeout=remaining)
            except subprocess.TimeoutExpired:
                try:
                    proc.kill()
                except OSError:
                    pass
                try:
                    proc.wait(timeout=2.0)
                except Exception:
                    pass
        self._children.clear()

    def cleanup_temp_dirs(self) -> None:
        for temp_dir in reversed(self._temp_dirs):
            try:
                if temp_dir.exists():
                    shutil.rmtree(temp_dir, ignore_errors=True)
            except OSError:
                pass
        self._temp_dirs.clear()

    def cleanup_all(self, grace_seconds: float = 1.5) -> None:
        self._tearing_down = True
        self.terminate_children(grace_seconds=grace_seconds)
        _release_cuda_memory()
        self.cleanup_temp_dirs()

    def handle_signal(self, signum: int, _frame: Any) -> None:
        # Never perform blocking waits or subprocess cleanup inside a signal
        # handler; record the signal and unwind into normal ``finally:`` cleanup.
        # While already tearing down, suppress re-entrant raises so cleanup_all()
        # completes child termination and temp-dir removal reliably.
        self.terminated_signal = int(signum)
        if self._tearing_down:
            return
        raise _ColabOneWorkerTerminated(signum)


def _validate_safe_path(path: Any, label: str = "path") -> Path:
    if isinstance(path, bool) or not isinstance(path, (str, os.PathLike)):
        raise ValueError(f"{label} must be a path string, got {type(path).__name__}")
    raw = os.fspath(path)
    if not isinstance(raw, str) or not raw.strip():
        raise ValueError(f"{label} must not be empty")
    if any(ch in raw for ch in ("\r", "\n", "\x00")):
        raise ValueError(f"{label} must not contain control characters")
    return Path(raw)


def _reject_symlink(path: Path, label: str) -> None:
    if path.is_symlink():
        raise ValueError(f"{label} must not be a symlink: {path}")


def _parse_strict_int(value: Any, label: str) -> int:
    if isinstance(value, bool):
        raise ValueError(f"{label} must be an integer, got bool")
    if isinstance(value, float):
        raise ValueError(f"{label} must be an integer, got float {value!r}")
    try:
        return int(value)
    except (TypeError, ValueError) as exc:
        raise ValueError(f"{label} must be an integer, got {value!r}") from exc


def _validate_positive_int(value: Any, label: str) -> int:
    iv = _parse_strict_int(value, label)
    if iv <= 0:
        raise ValueError(f"{label} must be > 0, got {iv}")
    return iv


def _validate_non_negative_int(value: Any, label: str) -> int:
    iv = _parse_strict_int(value, label)
    if iv < 0:
        raise ValueError(f"{label} must be >= 0, got {iv}")
    return iv


def _require_cfg_value(cfg: dict[str, Any], key: str) -> Any:
    if key not in cfg or cfg[key] is None:
        raise ValueError(f"Missing required config field: {key}")
    return cfg[key]


def _sanitized_subprocess_env(extra: dict[str, str] | None = None) -> dict[str, str]:
    env = {
        k: v
        for k, v in os.environ.items()
        if k not in _SENSITIVE_SUBPROCESS_ENV_KEYS
    }
    if extra:
        env.update(extra)
    return env


def _natural_sort_key(path: Path) -> list[Any]:
    return [
        int(part) if part.isdigit() else part.lower()
        for part in _NATURAL_SORT_RE.split(path.as_posix())
    ]


def _read_stderr_tail(log_path: Path, max_bytes: int = 4096) -> str:
    try:
        if not log_path.is_file():
            return ""
        size = log_path.stat().st_size
        with log_path.open("rb") as fh:
            if size > max_bytes:
                fh.seek(size - max_bytes)
            data = fh.read(max_bytes)
        return data.decode("utf-8", errors="replace").strip()
    except OSError:
        return ""


def _release_cuda_memory() -> None:
    gc.collect()
    torch_mod = sys.modules.get("torch")
    if torch_mod is None:
        return
    cuda = getattr(torch_mod, "cuda", None)
    if cuda is None:
        return
    try:
        if callable(getattr(cuda, "is_available", None)) and cuda.is_available():
            if callable(getattr(cuda, "synchronize", None)):
                cuda.synchronize()
            if callable(getattr(cuda, "empty_cache", None)):
                cuda.empty_cache()
            if callable(getattr(cuda, "ipc_collect", None)):
                cuda.ipc_collect()
    except Exception:
        pass


def _detect_gpu_vram_mib() -> int:
    """Lazily query total CUDA VRAM in MiB via torch if available."""
    try:
        import torch  # type: ignore[import-not-found]
    except Exception:
        return 0
    try:
        if not torch.cuda.is_available():
            return 0
        props = torch.cuda.get_device_properties(0)
        total_bytes = int(getattr(props, "total_memory", 0) or 0)
        return total_bytes // (1024 * 1024)
    except Exception:
        return 0


def atomic_write_json(path: str | Path, payload: dict[str, Any]) -> None:
    """Atomically write JSON metadata with fsync + os.replace."""
    target = _validate_safe_path(path, "output_manifest_path")
    _reject_symlink(target, "output_manifest_path")
    target.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp_name = tempfile.mkstemp(
        prefix=".colabone-vsr-",
        suffix=".tmp",
        dir=str(target.parent),
    )
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(payload, stream, ensure_ascii=False, indent=2)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(tmp_name, target)
    finally:
        if os.path.exists(tmp_name):
            try:
                os.unlink(tmp_name)
            except OSError:
                pass


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
    """Emit a single-line ``COLABONE_PROGRESS:<json>`` record to stdout."""
    _ensure_utf8_stdio()
    clamped = round(max(0.0, min(1.0, float(progress))), 4)
    payload: dict[str, Any] = {
        "stage": str(stage),
        "progress": clamped,
        "message": str(message),
    }
    if extra:
        payload.update(extra)
    line = json.dumps(payload, ensure_ascii=False, separators=(",", ":"))
    try:
        sys.stdout.write(f"{COLABONE_PROGRESS_PREFIX}{line}\n")
        sys.stdout.flush()
    except UnicodeEncodeError:
        ascii_line = json.dumps(payload, ensure_ascii=True, separators=(",", ":"))
        sys.stdout.write(f"{COLABONE_PROGRESS_PREFIX}{ascii_line}\n")
        sys.stdout.flush()


def resolve_vsr_model_files(engine: str, weights_dir: str | Path) -> dict[str, Path]:
    """Validate diffusion VSR engine name and return expected weight file paths."""
    engine_key = str(engine or "").strip().lower()
    if engine_key not in COLABONE_VSR_ENGINE_FILES:
        supported = ", ".join(sorted(COLABONE_VSR_ENGINE_FILES))
        raise ValueError(
            f"Unsupported diffusion VSR engine: {engine!r}. Supported: {supported}"
        )
    base_dir = _validate_safe_path(weights_dir, "weights_dir")
    return {
        filename: base_dir / filename
        for filename in COLABONE_VSR_ENGINE_FILES[engine_key]
    }


def compute_upscale_short_side(
    src_w: int,
    src_h: int,
    target_w: int,
    target_h: int,
) -> int:
    """Compute minimum multiple-of-16 short side so aspect-preserving scaling covers target.

    Uses exact integer ceiling division to avoid floating-point overshoot:
    - ``1280x768`` to ``1920x1080``: scale = max(1920/1280, 1080/768) = 1.5,
      short side = ceil(768 * 1.5) = 1152 (already a multiple of 16).
    - ``1280x768`` to ``2560x1440``: scale = 2.0, short side = 1536.
    """
    sw = _validate_positive_int(src_w, "src_w")
    sh = _validate_positive_int(src_h, "src_h")
    tw = _validate_positive_int(target_w, "target_w")
    th = _validate_positive_int(target_h, "target_h")

    src_short = min(sw, sh)
    needed_by_w = (src_short * tw + sw - 1) // sw
    needed_by_h = (src_short * th + sh - 1) // sh
    needed_short = max(needed_by_w, needed_by_h)
    return max(16, ((needed_short + 15) // 16) * 16)


def compute_flashvsr_cover_dimensions(
    src_w: int,
    src_h: int,
    target_w: int,
    target_h: int,
) -> tuple[int, int, int, int]:
    """Compute aspect-preserving cover size and 128-aligned padded size for FlashVSR+.

    Returns ``(cover_w, cover_h, pad_w, pad_h)`` where ``(cover_w, cover_h)``
    preserves ``src_w:src_h`` while covering ``(target_w, target_h)``, and
    ``(pad_w, pad_h)`` rounds each side up to an exact multiple of 128.
    """
    sw = _validate_positive_int(src_w, "src_w")
    sh = _validate_positive_int(src_h, "src_h")
    tw = _validate_positive_int(target_w, "target_w")
    th = _validate_positive_int(target_h, "target_h")

    if tw * sh >= th * sw:
        cover_w = tw
        cover_h = max(th, (sh * tw + sw - 1) // sw)
    else:
        cover_h = th
        cover_w = max(tw, (sw * th + sh - 1) // sh)

    pad_w = max(128, ((cover_w + 127) // 128) * 128)
    pad_h = max(128, ((cover_h + 127) // 128) * 128)
    return (cover_w, cover_h, pad_w, pad_h)


def compute_flashvsr_chunk_plan(
    exact_frames: int,
    *,
    chunk_frames: int = _FLASHVSR_CHUNK_FRAMES,
    overlap: int = _FLASHVSR_CHUNK_OVERLAP,
    single_pass_threshold: int = _FLASHVSR_SINGLE_PASS_THRESHOLD,
) -> list[dict[str, int]]:
    """Build overlapped chunk schedule for FlashVSR+ host-RAM streaming.

    Clips with ``exact_frames <= single_pass_threshold`` run in a single pass.
    Longer clips split into temporally overlapped chunks so host RAM stays
    bounded regardless of duration while ``[keep_start, keep_end)`` forms an
    exact contiguous partition of ``[0, exact_frames)``.
    """
    total = _validate_non_negative_int(exact_frames, "exact_frames")
    if total == 0:
        return []
    chunk_len = _validate_positive_int(chunk_frames, "chunk_frames")
    ov = _validate_non_negative_int(overlap, "overlap")
    threshold = _validate_positive_int(single_pass_threshold, "single_pass_threshold")

    if total <= threshold or total <= chunk_len:
        return [
            {
                "start": 0,
                "end": total,
                "keep_start": 0,
                "keep_end": total,
            }
        ]

    eff_overlap = min(ov, chunk_len - 1)
    stride = chunk_len - eff_overlap
    raw_ranges: list[tuple[int, int]] = []
    start = 0
    while start < total:
        end = min(start + chunk_len, total)
        raw_ranges.append((start, end))
        if end >= total:
            break
        start += stride

    plan: list[dict[str, int]] = []
    num_chunks = len(raw_ranges)
    for idx, (c_start, c_end) in enumerate(raw_ranges):
        if idx == 0:
            keep_start = 0
        else:
            prev_end = raw_ranges[idx - 1][1]
            keep_start = (c_start + prev_end) // 2

        if idx == num_chunks - 1:
            keep_end = total
        else:
            next_start = raw_ranges[idx + 1][0]
            keep_end = (next_start + c_end) // 2

        plan.append(
            {
                "start": c_start,
                "end": c_end,
                "keep_start": keep_start,
                "keep_end": keep_end,
            }
        )
    return plan


def align_seedvr2_batch_size(batch_size: int, total_frames: int) -> int:
    """Enforce a valid SeedVR2 temporal ``4k+1`` batch size in ``[5, max(5, total_frames)]``."""
    requested = _parse_strict_int(batch_size, "batch_size")
    cap = max(5, _parse_strict_int(total_frames, "total_frames"))
    clamped = max(5, min(requested, cap))
    return ((clamped - 1) // 4) * 4 + 1


def compute_seedvr2_streaming_chunk_size(
    exact_frames: int,
    aligned_batch: int,
) -> int:
    """Return a memory-safe SeedVR2 ``--chunk_size`` for long videos, or 0 for single pass."""
    total = _validate_non_negative_int(exact_frames, "exact_frames")
    batch = _validate_positive_int(aligned_batch, "aligned_batch")
    if total <= _SEEDVR2_STREAMING_FRAME_THRESHOLD:
        return 0
    return min(_SEEDVR2_MAX_CHUNK_SIZE, max(batch, batch * 8))


def compute_seedvr2_chunk_plan(
    total_frames: int,
    batch_size: int = 33,
    overlap: int = 4,
) -> list[dict[str, int]]:
    """Build deterministic overlapping chunk schedule covering ``[0, total_frames)``.

    Each chunk dictionary contains ``start``, ``end``, ``keep_start``, ``keep_end``
    in half-open global frame coordinates so ``[keep_start, keep_end)`` forms an
    exact contiguous partition of ``[0, total_frames)``.
    """
    total = _parse_strict_int(total_frames, "total_frames")
    if total <= 0:
        return []

    aligned_batch = align_seedvr2_batch_size(batch_size, total)
    if total <= aligned_batch:
        return [
            {
                "start": 0,
                "end": total,
                "keep_start": 0,
                "keep_end": total,
            }
        ]

    eff_overlap = max(0, min(_parse_strict_int(overlap, "overlap"), aligned_batch - 1))
    stride = aligned_batch - eff_overlap

    raw_ranges: list[tuple[int, int]] = []
    start = 0
    while start < total:
        end = min(start + aligned_batch, total)
        raw_ranges.append((start, end))
        if end >= total:
            break
        start += stride

    plan: list[dict[str, int]] = []
    num_chunks = len(raw_ranges)
    for idx, (c_start, c_end) in enumerate(raw_ranges):
        if idx == 0:
            keep_start = 0
        else:
            prev_end = raw_ranges[idx - 1][1]
            keep_start = (c_start + prev_end) // 2

        if idx == num_chunks - 1:
            keep_end = total
        else:
            next_start = raw_ranges[idx + 1][0]
            keep_end = (next_start + c_end) // 2

        plan.append(
            {
                "start": c_start,
                "end": c_end,
                "keep_start": keep_start,
                "keep_end": keep_end,
            }
        )
    return plan


def parse_cli_help_flags(help_text: str) -> dict[str, bool]:
    """Parse ``--help`` output into ``{flag: takes_value}`` for graceful CLI adaptation."""
    flags: dict[str, bool] = {}
    for match in _HELP_FLAG_RE.finditer(str(help_text or "")):
        flag = match.group(1)
        metavar = match.group(2)
        flags[flag] = flags.get(flag, False) or bool(metavar)
    return flags


def parse_vsr_progress_line(line: str) -> float | None:
    """Extract a normalized ``[0.0, 1.0]`` progress fraction from a worker log line."""
    text = str(line or "").strip()
    if not text:
        return None
    for pattern in (_PROGRESS_RATIO_RE, _PROGRESS_BAR_RATIO_RE):
        match = pattern.search(text)
        if match:
            current = int(match.group(1))
            total = int(match.group(2))
            if total > 0 and 0 <= current <= total:
                return max(0.0, min(1.0, current / total))
    pct_match = _PROGRESS_PCT_RE.search(text)
    if pct_match:
        pct = float(pct_match.group(1))
        if 0.0 <= pct <= 100.0:
            return max(0.0, min(1.0, pct / 100.0))
    return None


class ColabOneSeedVR2ProgressTracker:
    """Phase-aware SeedVR2 progress parser across Phases 1-4 and streaming chunks.

    Phase weights within each chunk / run:
    - Phase 1 (Encoding): 0.00 - 0.10
    - Phase 2 (Upscaling): 0.10 - 0.78
    - Phase 3 (Decoding): 0.78 - 0.92
    - Phase 4 (Post-processing): 0.92 - 1.00
    """

    def __init__(self, total_chunks: int = 1) -> None:
        self.total_chunks = max(1, int(total_chunks))
        self.current_chunk_idx = 0
        self.current_phase = 0
        self.max_progress = 0.0

    def update(self, line: str) -> float | None:
        text = str(line or "").strip()
        if not text:
            return None
        lower = text.lower()

        chunk_match = _SEEDVR2_CHUNK_RE.search(text)
        if chunk_match:
            c_cur = int(chunk_match.group(1))
            c_tot = int(chunk_match.group(2))
            if c_tot > 0 and 1 <= c_cur <= c_tot:
                self.total_chunks = c_tot
                self.current_chunk_idx = c_cur - 1
                self.current_phase = 1
                return self._record(self.current_chunk_idx / self.total_chunks)

        detected_phase: int | None = None
        phase_match = _SEEDVR2_PHASE_NUM_RE.search(text)
        if phase_match:
            detected_phase = int(phase_match.group(1))
        elif "vae encoding" in lower or "encoding batch" in lower:
            detected_phase = 1
        elif "dit upscaling" in lower or "upscaling batch" in lower:
            detected_phase = 2
        elif "vae decoding" in lower or "decoding batch" in lower:
            detected_phase = 3
        elif "post-process" in lower or "postprocess" in lower:
            detected_phase = 4

        if detected_phase is not None:
            self.current_phase = detected_phase

        if self.current_phase == 0:
            return None

        ratio = parse_vsr_progress_line(text)
        if detected_phase is not None and ratio is None:
            ratio = 1.0 if "complete" in lower else 0.0

        if ratio is None:
            return None

        if self.current_phase in _SEEDVR2_PHASE_WINDOWS:
            p_start, p_end = _SEEDVR2_PHASE_WINDOWS[self.current_phase]
            chunk_progress = p_start + ratio * (p_end - p_start)
        else:
            chunk_progress = ratio

        overall = (self.current_chunk_idx + chunk_progress) / self.total_chunks
        return self._record(overall)

    def _record(self, value: float) -> float:
        clamped = max(0.0, min(1.0, float(value)))
        if clamped > self.max_progress:
            self.max_progress = clamped
        return self.max_progress


def _pick_supported_flag(
    supported: dict[str, bool] | None,
    candidates: tuple[str, ...],
) -> tuple[str | None, bool]:
    if supported is None:
        return (candidates[0], True)
    for flag in candidates:
        if flag in supported:
            return (flag, supported[flag])
    return (None, False)


def build_seedvr2_cli_command(
    python_bin: str,
    repo_dir: str | Path,
    weights_dir: str | Path,
    input_path: str | Path,
    output_path: str | Path,
    *,
    engine: str,
    resolution_short_side: int,
    batch_size: int,
    temporal_overlap: int,
    color_correction: str,
    seed: int,
    vram_mib: int,
    exact_frames: int = 0,
    chunk_size: int = 0,
    supported_flags: dict[str, bool] | set[str] | frozenset[str] | None = None,
) -> list[str]:
    """Build exact CLI command for ``numz/ComfyUI-SeedVR2_VideoUpscaler/inference_cli.py``.

    - Uses the verified pinned ``4490bd1f`` flag set by default without running
      ``inference_cli.py --help``.
    - Uses resolved absolute paths so leading-dash relative paths cannot be
      misinterpreted as CLI options.
    - ``vram_mib > 49152`` (or ``0`` when unknown): zero block swap and zero VAE tiling.
    - ``0 < vram_mib <= 49152``: enables ``--blocks_to_swap 16 --vae_encode_tiled
      --vae_decode_tiled --dit_offload_device cpu --vae_offload_device cpu``.
    - ``exact_frames > 0``: appends ``--load_cap <exact_frames>``.
    - ``chunk_size > 0``: appends ``--chunk_size <chunk_size> --cache_dit
      --cache_vae --dit_offload_device cpu --vae_offload_device cpu`` without
      duplicating offload flags.
    """
    engine_key = str(engine or "").strip().lower()
    if engine_key not in COLABONE_VSR_SEEDVR2_ENGINES:
        raise ValueError(
            f"build_seedvr2_cli_command requires a SeedVR2 engine, got {engine!r}"
        )

    color_mode = str(color_correction or "lab").strip().lower()
    if color_mode not in COLABONE_VSR_COLOR_CORRECTION_MODES:
        raise ValueError(
            f"Unsupported color_correction: {color_correction!r}. "
            f"Expected one of: {', '.join(COLABONE_VSR_COLOR_CORRECTION_MODES)}"
        )

    py_raw = (
        python_bin
        if python_bin is not None and str(python_bin).strip()
        else sys.executable
    )
    py_bin = str(_validate_safe_path(py_raw, "python_bin"))
    repo = _validate_safe_path(repo_dir, "repo_dir")
    weights = _validate_safe_path(weights_dir, "weights_dir")
    inp = _validate_safe_path(input_path, "input_path")
    out = _validate_safe_path(output_path, "output_path")
    short_side = _validate_positive_int(resolution_short_side, "resolution_short_side")
    batch_val = _validate_positive_int(batch_size, "batch_size")
    overlap_val = _validate_non_negative_int(temporal_overlap, "temporal_overlap")
    seed_val = _validate_non_negative_int(seed, "seed")
    vram = _validate_non_negative_int(vram_mib if vram_mib is not None else 0, "vram_mib")
    frames_cap = _validate_non_negative_int(exact_frames, "exact_frames")
    chunk_val = _validate_non_negative_int(chunk_size, "chunk_size")

    dit_model = COLABONE_VSR_ENGINE_FILES[engine_key][0]
    batch_cap = frames_cap if frames_cap > 0 else max(batch_val, 5)
    if chunk_val > 0:
        batch_cap = min(batch_cap, chunk_val)
    aligned_batch = align_seedvr2_batch_size(batch_val, batch_cap)
    if chunk_val > 0 and aligned_batch > chunk_val:
        aligned_batch = ((chunk_val - 1) // 4) * 4 + 1
    overlap_cap = (
        min(aligned_batch, chunk_val) - 1 if chunk_val > 0 else aligned_batch - 1
    )
    clamped_overlap = max(0, min(overlap_val, overlap_cap))

    if supported_flags is None:
        flag_map: dict[str, bool] = _SEEDVR2_PINNED_4490BD1F_FLAGS
    elif isinstance(supported_flags, dict):
        flag_map = supported_flags if supported_flags else _SEEDVR2_PINNED_4490BD1F_FLAGS
    else:
        flag_map = (
            {str(f): True for f in supported_flags}
            if supported_flags
            else _SEEDVR2_PINNED_4490BD1F_FLAGS
        )

    script_resolved = str((repo / "inference_cli.py").resolve())
    inp_resolved = str(inp.resolve())
    weights_resolved = str(weights.resolve())
    out_resolved = str(out.resolve())

    cmd: list[str] = [py_bin, script_resolved]

    if "--input" in flag_map and "--input_video" not in flag_map:
        cmd.extend(["--input", inp_resolved])
    else:
        cmd.append(inp_resolved)

    dit_flag, _ = _pick_supported_flag(flag_map, ("--dit_model", "--model"))
    if dit_flag:
        cmd.extend([dit_flag, dit_model])

    model_dir_flag, _ = _pick_supported_flag(
        flag_map, ("--model_dir", "--models_dir", "--checkpoint_dir")
    )
    if model_dir_flag:
        cmd.extend([model_dir_flag, weights_resolved])

    res_flag, _ = _pick_supported_flag(flag_map, ("--resolution", "--res"))
    if res_flag:
        cmd.extend([res_flag, str(short_side)])

    batch_flag, _ = _pick_supported_flag(flag_map, ("--batch_size",))
    if batch_flag:
        cmd.extend([batch_flag, str(aligned_batch)])

    overlap_flag, _ = _pick_supported_flag(
        flag_map, ("--temporal_overlap", "--overlap")
    )
    if overlap_flag:
        cmd.extend([overlap_flag, str(clamped_overlap)])

    color_flag, color_takes_val = _pick_supported_flag(
        flag_map, ("--color_correction",)
    )
    if color_flag:
        if color_takes_val:
            cmd.extend([color_flag, color_mode])
        elif color_mode != "none":
            cmd.append(color_flag)

    seed_flag, _ = _pick_supported_flag(flag_map, ("--seed",))
    if seed_flag:
        cmd.extend([seed_flag, str(seed_val)])

    out_flag, _ = _pick_supported_flag(flag_map, ("--output", "--output_path"))
    if out_flag:
        cmd.extend([out_flag, out_resolved])

    fmt_flag, _ = _pick_supported_flag(flag_map, ("--output_format", "--format"))
    if fmt_flag:
        cmd.extend([fmt_flag, "png"])

    if frames_cap > 0:
        load_cap_flag, _ = _pick_supported_flag(flag_map, ("--load_cap",))
        if load_cap_flag:
            cmd.extend([load_cap_flag, str(frames_cap)])

    need_dit_offload = False
    need_vae_offload = False

    if chunk_val > 0:
        chunk_flag, _ = _pick_supported_flag(flag_map, ("--chunk_size",))
        if chunk_flag:
            cmd.extend([chunk_flag, str(chunk_val)])
        cache_dit_flag, _ = _pick_supported_flag(flag_map, ("--cache_dit",))
        if cache_dit_flag:
            cmd.append(cache_dit_flag)
        cache_vae_flag, _ = _pick_supported_flag(flag_map, ("--cache_vae",))
        if cache_vae_flag:
            cmd.append(cache_vae_flag)
        need_dit_offload = True
        need_vae_offload = True

    if 0 < vram <= COLABONE_VSR_LOW_VRAM_THRESHOLD_MIB:
        swap_flag, _ = _pick_supported_flag(flag_map, ("--blocks_to_swap",))
        if swap_flag:
            cmd.extend([swap_flag, str(_SEEDVR2_LOW_VRAM_BLOCKS_TO_SWAP)])

        enc_tile_flag, _ = _pick_supported_flag(flag_map, ("--vae_encode_tiled",))
        if enc_tile_flag:
            cmd.append(enc_tile_flag)

        dec_tile_flag, _ = _pick_supported_flag(flag_map, ("--vae_decode_tiled",))
        if dec_tile_flag:
            cmd.append(dec_tile_flag)

        need_dit_offload = True
        need_vae_offload = True

    if need_dit_offload:
        dit_offload_flag, _ = _pick_supported_flag(flag_map, ("--dit_offload_device",))
        if dit_offload_flag:
            cmd.extend([dit_offload_flag, "cpu"])

    if need_vae_offload:
        vae_offload_flag, _ = _pick_supported_flag(flag_map, ("--vae_offload_device",))
        if vae_offload_flag:
            cmd.extend([vae_offload_flag, "cpu"])

    return cmd


def build_flashvsr_scale_factor(
    src_w: int,
    src_h: int,
    target_w: int,
    target_h: int,
) -> int:
    """Compute integer FlashVSR+ upscale factor (2 when 2x covers target, else 4)."""
    sw = _validate_positive_int(src_w, "src_w")
    sh = _validate_positive_int(src_h, "src_h")
    tw = _validate_positive_int(target_w, "target_w")
    th = _validate_positive_int(target_h, "target_h")

    if sw * 2 >= tw and sh * 2 >= th:
        return 2
    return 4


def _build_cover_crop_vf(target_w: int, target_h: int) -> str:
    w = _validate_positive_int(target_w, "target_w")
    h = _validate_positive_int(target_h, "target_h")
    return (
        f"setsar=1,scale={w}:{h}:force_original_aspect_ratio=increase:flags=lanczos,"
        f"crop={w}:{h}"
    )


def _resolve_ffmpeg_fps_mode_flag(ffmpeg_bin: str) -> str:
    """Return ``-fps_mode`` on FFmpeg >= 5.1, falling back to ``-vsync`` on FFmpeg 4.x."""
    key = str(ffmpeg_bin or "").strip() or "ffmpeg"
    with _FFMPEG_FPS_MODE_CACHE_LOCK:
        cached = _FFMPEG_FPS_MODE_CACHE.get(key)
    if cached is not None:
        return cached

    chosen = "-vsync"
    try:
        res = subprocess.run(
            [key, "-hide_banner", "-h"],
            capture_output=True,
            text=True,
            timeout=5,
            check=False,
            stdin=subprocess.DEVNULL,
            env=_sanitized_subprocess_env(),
        )
        help_text = (res.stdout or "") + "\n" + (res.stderr or "")
        if "-fps_mode" in help_text:
            chosen = "-fps_mode"
    except Exception:
        chosen = "-vsync"

    with _FFMPEG_FPS_MODE_CACHE_LOCK:
        _FFMPEG_FPS_MODE_CACHE[key] = chosen
    return chosen


def build_cover_crop_lossless_cmd(
    ffmpeg_bin: str,
    input_spec: str | Path,
    output_path: str | Path,
    *,
    target_w: int,
    target_h: int,
    fps: int,
    exact_frames: int,
    is_png_sequence: bool = False,
    fps_mode_flag: str = "-fps_mode",
) -> list[str]:
    """Build FFmpeg command for Lanczos cover-scale + center-crop into lossless FFV1 MKV."""
    ff_raw = (
        ffmpeg_bin
        if ffmpeg_bin is not None and str(ffmpeg_bin).strip()
        else "ffmpeg"
    )
    ff = str(_validate_safe_path(ff_raw, "ffmpeg_bin"))
    inp = str(_validate_safe_path(input_spec, "input_spec"))
    out = str(_validate_safe_path(output_path, "output_path"))
    fps_val = _validate_positive_int(fps, "fps")
    frames_val = _validate_positive_int(exact_frames, "exact_frames")
    mode_flag = str(fps_mode_flag or "-fps_mode").strip()
    if mode_flag not in ("-fps_mode", "-vsync"):
        raise ValueError(
            f"fps_mode_flag must be '-fps_mode' or '-vsync', got {fps_mode_flag!r}"
        )
    vf = _build_cover_crop_vf(target_w, target_h)

    cmd: list[str] = [
        ff,
        "-y",
        "-hide_banner",
        "-loglevel",
        "error",
        *_COLABONE_FFMPEG_PROTOCOL_ARGS,
    ]
    if is_png_sequence:
        cmd.extend(["-framerate", str(fps_val)])
        inp_name = Path(inp).name
        is_glob = ("%" not in inp_name) and any(
            ch in inp_name for ch in ("*", "?", "[")
        )
        if is_glob:
            cmd.extend(["-pattern_type", "glob"])
        else:
            cmd.extend(["-start_number", "1"])
        cmd.extend(["-i", inp])
        cmd.extend(
            [
                "-an",
                "-vf",
                vf,
                "-r",
                str(fps_val),
                "-frames:v",
                str(frames_val),
                *_COLABONE_FFV1_CODEC_ARGS,
                out,
            ]
        )
    else:
        vf_video = f"{vf},setpts=N/({fps_val}*TB)"
        cmd.extend(["-i", inp])
        cmd.extend(
            [
                "-an",
                "-vf",
                vf_video,
                mode_flag,
                "passthrough",
                "-frames:v",
                str(frames_val),
                *_COLABONE_FFV1_CODEC_ARGS,
                out,
            ]
        )
    return cmd


def _resolve_ffprobe_bin(ffmpeg_bin: str) -> str:
    ff_raw = (
        ffmpeg_bin
        if ffmpeg_bin is not None and str(ffmpeg_bin).strip()
        else "ffmpeg"
    )
    ff_path = _validate_safe_path(ff_raw, "ffmpeg_bin")
    if ff_path.parent != Path("."):
        sibling_name = ff_path.name.replace("ffmpeg", "ffprobe")
        sibling = ff_path.with_name(sibling_name)
        if sibling.is_file():
            return str(sibling)
    return "ffprobe"


def _probe_video_metadata(ffmpeg_bin: str, video_path: Path) -> dict[str, int]:
    """Probe video dimensions and frame count using ffprobe."""
    ffprobe_bin = _resolve_ffprobe_bin(ffmpeg_bin)
    cmd = [
        ffprobe_bin,
        "-v",
        "error",
        *_COLABONE_FFMPEG_PROTOCOL_ARGS,
        "-count_packets",
        "-select_streams",
        "v:0",
        "-show_entries",
        "stream=width,height,nb_read_packets,nb_frames",
        "-of",
        "json",
        str(video_path),
    ]
    try:
        res = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            timeout=60,
            check=False,
            stdin=subprocess.DEVNULL,
            env=_sanitized_subprocess_env(),
        )
    except Exception:
        return {}
    if res.returncode != 0 or not res.stdout.strip():
        return {}
    try:
        parsed = json.loads(res.stdout)
        streams = parsed.get("streams") or []
        if not streams:
            return {}
        stream = streams[0]
        result: dict[str, int] = {}
        for key in ("width", "height"):
            val = stream.get(key)
            if val is not None and str(val).isdigit():
                result[key] = int(val)
        for frame_key in ("nb_read_packets", "nb_frames"):
            val = stream.get(frame_key)
            if val is not None and str(val).isdigit() and int(val) > 0:
                result["frames"] = int(val)
                break
        return result
    except Exception:
        return {}


def _run_monitored_subprocess(
    cmd: list[str],
    *,
    cwd: Path | None,
    guard: ColabOneProcessGuard,
    engine: str,
    stage: str,
    progress_start: float,
    progress_end: float,
    progress_message: str,
    progress_tracker: ColabOneSeedVR2ProgressTracker | None = None,
    env_extra: dict[str, str] | None = None,
) -> list[str]:
    """Run a child process in the worker's process group and stream progress."""
    env = _sanitized_subprocess_env(
        {
            "PYTHONUNBUFFERED": "1",
            "PYTORCH_CUDA_ALLOC_CONF": "expandable_segments:True",
            **(env_extra or {}),
        }
    )
    proc = subprocess.Popen(
        cmd,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        stdin=subprocess.DEVNULL,
        cwd=str(cwd) if cwd is not None else None,
        env=env,
    )
    guard.register_child(proc)
    tail_lines: deque[str] = deque(maxlen=80)
    last_emit_ts = 0.0
    last_ratio = -1.0

    try:
        assert proc.stdout is not None
        stdout_fd = proc.stdout.fileno()
        buf = bytearray()
        while True:
            chunk = os.read(stdout_fd, 4096)
            if not chunk:
                break
            for byte in chunk:
                if byte in (10, 13):  # LF or CR
                    if buf:
                        line = buf.decode("utf-8", errors="replace").strip()
                        buf.clear()
                        if line:
                            tail_lines.append(line)
                            ratio = (
                                progress_tracker.update(line)
                                if progress_tracker is not None
                                else parse_vsr_progress_line(line)
                            )
                            now = time.monotonic()
                            if ratio is not None and (
                                ratio - last_ratio >= 0.02
                                or now - last_emit_ts >= 0.75
                            ):
                                mapped = progress_start + ratio * (
                                    progress_end - progress_start
                                )
                                emit_colabone_progress(
                                    stage,
                                    mapped,
                                    progress_message,
                                    engine=engine,
                                )
                                last_ratio = ratio
                                last_emit_ts = now
                else:
                    buf.append(byte)
        if buf:
            line = buf.decode("utf-8", errors="replace").strip()
            if line:
                tail_lines.append(line)

        rc = proc.wait()
        if rc != 0:
            tail_preview = "\n".join(list(tail_lines)[-20:])
            raise RuntimeError(
                f"{engine} subprocess exited with code {rc}.\n"
                f"Command: {' '.join(cmd)}\n"
                f"Output tail:\n{tail_preview}"
            )
        return list(tail_lines)
    finally:
        if proc.stdout is not None:
            try:
                proc.stdout.close()
            except Exception:
                pass
        if proc.poll() is None:
            guard.terminate_child(proc, grace_seconds=1.5)
        guard.unregister_child(proc)


def _run_ffmpeg_command(
    cmd: list[str],
    guard: ColabOneProcessGuard,
) -> None:
    """Execute FFmpeg synchronously under the process guard (same process group)."""
    proc = subprocess.Popen(
        cmd,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
        stdin=subprocess.DEVNULL,
        env=_sanitized_subprocess_env(),
    )
    guard.register_child(proc)
    try:
        _stdout, stderr = proc.communicate()
        if proc.returncode != 0:
            err_text = (stderr or b"").decode("utf-8", errors="replace").strip()
            raise RuntimeError(
                f"FFmpeg exited with code {proc.returncode}: {err_text[-1200:]}"
            )
    finally:
        if proc.stderr is not None:
            try:
                proc.stderr.close()
            except Exception:
                pass
        if proc.poll() is None:
            guard.terminate_child(proc, grace_seconds=1.5)
        guard.unregister_child(proc)


def _is_contained_regular_file(path: Path, root_resolved: Path) -> bool:
    if path.is_symlink() or not path.is_file():
        return False
    try:
        return path.resolve().is_relative_to(root_resolved)
    except (OSError, ValueError):
        return False


def _collect_png_sequence_or_video(
    output_dir: Path,
    seq_dir: Path,
    exact_frames: int,
) -> tuple[Path, bool]:
    """Locate SeedVR2 PNG sequence (or fallback video) and stage contiguous PNG links."""
    resolved_root = output_dir.resolve()
    png_files = sorted(
        (
            p
            for p in output_dir.rglob("*.png")
            if _is_contained_regular_file(p, resolved_root)
        ),
        key=_natural_sort_key,
    )
    if png_files:
        min_required_frames = (
            exact_frames
            if exact_frames <= 16
            else max(1, exact_frames - min(8, exact_frames // 10))
        )
        if len(png_files) < min_required_frames:
            raise RuntimeError(
                f"SeedVR2 produced only {len(png_files)} PNG frame(s), "
                f"expected at least {min_required_frames} (target {exact_frames})"
            )
        seq_dir.mkdir(parents=True, exist_ok=True)
        selected = list(png_files[:exact_frames])
        while len(selected) < exact_frames:
            selected.append(selected[-1])
        for idx, src in enumerate(selected, start=1):
            dst = seq_dir / f"frame_{idx:06d}.png"
            try:
                os.symlink(src.resolve(), dst)
            except OSError:
                shutil.copy2(src, dst)
        return (seq_dir / "frame_%06d.png", True)

    video_candidates = sorted(
        (
            p
            for p in output_dir.rglob("*")
            if _is_contained_regular_file(p, resolved_root)
            and p.suffix.lower() in _VIDEO_SUFFIXES
        ),
        key=lambda p: p.stat().st_mtime,
        reverse=True,
    )
    if not video_candidates:
        parent_resolved = output_dir.parent.resolve()
        for suffix in (".mp4", ".mkv", ".mov"):
            sibling = output_dir.with_suffix(suffix)
            if _is_contained_regular_file(sibling, parent_resolved):
                video_candidates.append(sibling)
                break
    if video_candidates:
        return (video_candidates[0], False)

    raise RuntimeError(
        f"SeedVR2 completed but produced neither PNG frames nor video under {output_dir}"
    )


def _prepare_seedvr2_input_video(
    input_video_path: Path,
    *,
    exact_frames: int,
    fps: int,
    ffmpeg_bin: str,
    tmp_dir: Path,
    guard: ColabOneProcessGuard,
) -> Path:
    """Ensure SeedVR2's OpenCV reader sees exact ``CAP_PROP_FRAME_COUNT``.

    Probes ``input_video_path`` with ``cv2.VideoCapture`` (or falls back
    proactively for ``.mkv`` when ``cv2`` is unavailable in this interpreter).
    If OpenCV cannot open/read the first frame or reports fewer than
    ``exact_frames`` frames, losslessly transcodes into a temporary ``.mp4``
    inside ``tmp_dir`` (contained in ``work_dir``).
    """
    needs_transcode = False
    try:
        import cv2  # type: ignore[import-not-found]
    except ImportError:
        needs_transcode = input_video_path.suffix.lower() == ".mkv"
    else:
        cap = cv2.VideoCapture(str(input_video_path))
        try:
            if not cap.isOpened():
                needs_transcode = True
            else:
                raw_count = cap.get(cv2.CAP_PROP_FRAME_COUNT)
                reported_count = (
                    int(raw_count)
                    if isinstance(raw_count, (int, float))
                    and math.isfinite(raw_count)
                    and raw_count > 0
                    else 0
                )
                ok, frame = cap.read()
                if not ok or frame is None or reported_count < exact_frames:
                    needs_transcode = True
        finally:
            cap.release()

    if not needs_transcode:
        return input_video_path

    fps_val = _validate_positive_int(fps, "fps")
    fps_mode_flag = _resolve_ffmpeg_fps_mode_flag(ffmpeg_bin)
    compat_mp4 = tmp_dir / "seedvr2_input_lossless.mp4"
    _reject_symlink(compat_mp4, "seedvr2_input_lossless")
    transcode_cmd = [
        ffmpeg_bin,
        "-y",
        "-hide_banner",
        "-loglevel",
        "error",
        *_COLABONE_FFMPEG_PROTOCOL_ARGS,
        "-i",
        str(input_video_path),
        "-an",
        "-vf",
        f"setpts=N/({fps_val}*TB)",
        fps_mode_flag,
        "passthrough",
        "-frames:v",
        str(exact_frames),
        "-c:v",
        "libx264rgb",
        "-crf",
        "0",
        "-preset",
        "ultrafast",
        "-pix_fmt",
        "rgb24",
        str(compat_mp4),
    ]
    _run_ffmpeg_command(transcode_cmd, guard)
    if not compat_mp4.is_file() or compat_mp4.stat().st_size <= 0:
        raise RuntimeError(
            f"Failed to create lossless OpenCV-compatible MP4 input: {compat_mp4}"
        )
    return compat_mp4


def _run_seedvr2_engine(
    *,
    engine: str,
    python_bin: str,
    repo_dir: Path,
    weights_dir: Path,
    input_video_path: Path,
    output_video_path: Path,
    source_width: int,
    source_height: int,
    target_width: int,
    target_height: int,
    fps: int,
    exact_frames: int,
    seed: int,
    batch_size: int,
    temporal_overlap: int,
    color_correction: str,
    vram_mib: int,
    ffmpeg_bin: str,
    guard: ColabOneProcessGuard,
) -> dict[str, Any]:
    inference_cli = repo_dir / "inference_cli.py"
    if not inference_cli.is_file():
        raise FileNotFoundError(
            f"SeedVR2 CLI script not found at {inference_cli}"
        )

    resolution_short_side = compute_upscale_short_side(
        source_width, source_height, target_width, target_height
    )
    aligned_batch = align_seedvr2_batch_size(batch_size, exact_frames)
    chunk_plan = compute_seedvr2_chunk_plan(
        exact_frames, aligned_batch, temporal_overlap
    )
    streaming_chunk_size = compute_seedvr2_streaming_chunk_size(
        exact_frames, aligned_batch
    )
    expected_stream_chunks = (
        (exact_frames + streaming_chunk_size - 1) // streaming_chunk_size
        if streaming_chunk_size > 0
        else 1
    )

    emit_colabone_progress(
        "prepare",
        0.05,
        (
            f"正在启动 {engine} 超分（短边 {resolution_short_side}px，"
            f"批次 {aligned_batch}，共 {len(chunk_plan)} 批）"
        ),
        engine=engine,
        resolution_short_side=resolution_short_side,
        batch_size=aligned_batch,
        chunks=len(chunk_plan),
        vram_mib=vram_mib,
    )

    tmp_dir = guard.create_temp_dir(prefix="colabone_seedvr2_")
    png_out_dir = tmp_dir / "png_frames"
    png_out_dir.mkdir(parents=True, exist_ok=True)
    seq_dir = tmp_dir / "seq"

    seedvr2_input_path = _prepare_seedvr2_input_video(
        input_video_path,
        exact_frames=exact_frames,
        fps=fps,
        ffmpeg_bin=ffmpeg_bin,
        tmp_dir=tmp_dir,
        guard=guard,
    )

    cli_cmd = build_seedvr2_cli_command(
        python_bin,
        repo_dir,
        weights_dir,
        seedvr2_input_path,
        png_out_dir,
        engine=engine,
        resolution_short_side=resolution_short_side,
        batch_size=aligned_batch,
        temporal_overlap=temporal_overlap,
        color_correction=color_correction,
        seed=seed,
        vram_mib=vram_mib,
        exact_frames=exact_frames,
        chunk_size=streaming_chunk_size,
    )

    upscale_msg = f"正在执行 SeedVR2 时空扩散超分（{engine}）"
    emit_colabone_progress(
        "upscale",
        0.10,
        upscale_msg,
        engine=engine,
    )
    tracker = ColabOneSeedVR2ProgressTracker(total_chunks=expected_stream_chunks)
    _run_monitored_subprocess(
        cli_cmd,
        cwd=repo_dir,
        guard=guard,
        engine=engine,
        stage="upscale",
        progress_start=0.10,
        progress_end=0.86,
        progress_message=upscale_msg,
        progress_tracker=tracker,
    )

    emit_colabone_progress(
        "encode",
        0.88,
        "正在将 SeedVR2 超分帧封装为无损 FFV1 中间视频",
        engine=engine,
    )
    input_spec, is_png_seq = _collect_png_sequence_or_video(
        png_out_dir, seq_dir, exact_frames
    )

    temp_output = output_video_path.with_name(
        f".{output_video_path.name}.{os.getpid()}.tmp.mkv"
    )
    _reject_symlink(temp_output, "temp_output")
    fps_mode_flag = _resolve_ffmpeg_fps_mode_flag(ffmpeg_bin)
    try:
        ffmpeg_cmd = build_cover_crop_lossless_cmd(
            ffmpeg_bin,
            input_spec,
            temp_output,
            target_w=target_width,
            target_h=target_height,
            fps=fps,
            exact_frames=exact_frames,
            is_png_sequence=is_png_seq,
            fps_mode_flag=fps_mode_flag,
        )
        _run_ffmpeg_command(ffmpeg_cmd, guard)
        os.replace(temp_output, output_video_path)
    finally:
        if temp_output.exists():
            try:
                temp_output.unlink()
            except OSError:
                pass

    return {
        "resolution_short_side": resolution_short_side,
        "batch_size": aligned_batch,
        "chunks": len(chunk_plan),
        "streaming_chunk_size": streaming_chunk_size,
    }


def discover_flashvsr_entrypoint(repo_dir: str | Path) -> tuple[str, Path | None]:
    """Auto-discover ``OpenImagingLab/FlashVSR_Plus`` Python inference pipeline."""
    root = _validate_safe_path(repo_dir, "repo_dir")
    pipeline_path = root / "inference" / "pipeline.py"
    if (
        pipeline_path.is_file()
        or (root / "flashvsr_plus").is_dir()
        or (root / "inference" / "__init__.py").is_file()
    ):
        return ("pipeline", pipeline_path)
    return ("pipeline", None)


def _locate_flashvsr_prompt_tensor(repo_dir: Path, weights_dir: Path) -> Path:
    primary = repo_dir / "prompt_tensor" / "posi_prompt.pth"
    if primary.is_file() and primary.stat().st_size > 0:
        return primary
    fallback = weights_dir / "posi_prompt.pth"
    if fallback.is_file() and fallback.stat().st_size > 0:
        return fallback
    raise FileNotFoundError(
        f"FlashVSR+ prompt tensor not found at {primary}"
    )


def _read_exact_bytes(stream: Any, num_bytes: int) -> bytes:
    buf = bytearray()
    while len(buf) < num_bytes:
        chunk = stream.read(num_bytes - len(buf))
        if not chunk:
            break
        buf.extend(chunk)
    return bytes(buf)


class _ColabOneRgb24FrameReader:
    """Streams decoded uint8 RGB24 frames sequentially from FFmpeg with bounded RAM."""

    def __init__(
        self,
        ffmpeg_bin: str,
        input_video_path: Path,
        width: int,
        height: int,
        exact_frames: int,
        tmp_dir: Path,
        guard: ColabOneProcessGuard,
    ) -> None:
        self._width = _validate_positive_int(width, "width")
        self._height = _validate_positive_int(height, "height")
        self._exact_frames = _validate_positive_int(exact_frames, "exact_frames")
        self._frame_bytes = self._width * self._height * 3
        self._guard = guard
        self._stderr_path = tmp_dir / f"ffdecode_{os.getpid()}.stderr.log"
        self._stderr_fh: Any = None
        self._proc: subprocess.Popen[Any] | None = None
        self._closed = False
        self._eof = False

        cmd = [
            ffmpeg_bin,
            "-hide_banner",
            "-loglevel",
            "error",
            *_COLABONE_FFMPEG_PROTOCOL_ARGS,
            "-i",
            str(input_video_path),
            "-an",
            "-vf",
            f"setsar=1,scale={self._width}:{self._height}:flags=lanczos",
            "-frames:v",
            str(self._exact_frames),
            "-f",
            "rawvideo",
            "-pix_fmt",
            "rgb24",
            "pipe:1",
        ]
        self._stderr_fh = self._stderr_path.open("wb")
        try:
            self._proc = subprocess.Popen(
                cmd,
                stdout=subprocess.PIPE,
                stderr=self._stderr_fh,
                stdin=subprocess.DEVNULL,
                env=_sanitized_subprocess_env(),
            )
            self._guard.register_child(self._proc)
        except BaseException:
            if self._proc is not None and self._proc.poll() is None:
                self._guard.terminate_child(self._proc, grace_seconds=1.5)
                self._guard.unregister_child(self._proc)
            self._close_stderr()
            raise

    def _close_stderr(self) -> None:
        if self._stderr_fh is not None:
            try:
                self._stderr_fh.close()
            except Exception:
                pass
            self._stderr_fh = None

    def read_frames(self, count: int) -> list[Any]:
        import numpy as np  # type: ignore[import-not-found]

        if self._closed or self._proc is None or self._proc.stdout is None:
            raise RuntimeError("Frame reader is closed")
        if count <= 0 or self._eof:
            return []
        frames: list[Any] = []
        for _ in range(count):
            raw = _read_exact_bytes(self._proc.stdout, self._frame_bytes)
            if len(raw) < self._frame_bytes:
                self._eof = True
                break
            arr = (
                np.frombuffer(raw, dtype=np.uint8)
                .reshape((self._height, self._width, 3))
                .copy()
            )
            frames.append(arr)
        return frames

    def close(self, *, check_returncode: bool = True) -> None:
        if self._closed:
            return
        self._closed = True
        proc = self._proc
        self._proc = None
        try:
            if proc is not None:
                if proc.stdout is not None:
                    try:
                        proc.stdout.close()
                    except Exception:
                        pass
                if not check_returncode and proc.poll() is None:
                    try:
                        proc.terminate()
                    except OSError:
                        pass
                try:
                    rc = proc.wait(timeout=15.0)
                except subprocess.TimeoutExpired:
                    try:
                        proc.kill()
                    except OSError:
                        pass
                    rc = proc.wait(timeout=5.0)
                self._close_stderr()
                if check_returncode and rc != 0:
                    err_tail = _read_stderr_tail(self._stderr_path)
                    raise RuntimeError(
                        f"FFmpeg decode failed (code {rc}): {err_tail[-800:]}"
                    )
        finally:
            self._close_stderr()
            if proc is not None:
                if proc.poll() is None:
                    self._guard.terminate_child(proc, grace_seconds=1.5)
                self._guard.unregister_child(proc)
            try:
                self._stderr_path.unlink(missing_ok=True)
            except OSError:
                pass


class _ColabOneFfv1CoverCropWriter:
    """Streams uint8 RGB24 ``(T, H, W, 3)`` chunks directly into lossless FFV1 MKV.

    Redirects FFmpeg stderr to a temporary log file inside ``work_dir`` so
    stderr buffers can never deadlock against stdin writes.
    """

    def __init__(
        self,
        ffmpeg_bin: str,
        output_video_path: Path,
        *,
        in_w: int,
        in_h: int,
        target_w: int,
        target_h: int,
        fps: int,
        exact_frames: int,
        tmp_dir: Path,
        guard: ColabOneProcessGuard,
    ) -> None:
        self._output_video_path = output_video_path
        self._in_w = _validate_positive_int(in_w, "in_w")
        self._in_h = _validate_positive_int(in_h, "in_h")
        self._exact_frames = _validate_positive_int(exact_frames, "exact_frames")
        self._guard = guard
        self._frames_written = 0
        self._last_frame_bytes: bytes | None = None
        self._closed = False
        self._committed = False

        self._temp_output = output_video_path.with_name(
            f".{output_video_path.name}.{os.getpid()}.tmp.mkv"
        )
        _reject_symlink(self._temp_output, "temp_output")
        self._stderr_path = tmp_dir / f"ffencode_{os.getpid()}.stderr.log"
        self._stderr_fh: Any = None
        self._proc: subprocess.Popen[Any] | None = None

        vf = _build_cover_crop_vf(target_w, target_h)
        cmd = [
            ffmpeg_bin,
            "-y",
            "-hide_banner",
            "-loglevel",
            "error",
            *_COLABONE_FFMPEG_PROTOCOL_ARGS,
            "-f",
            "rawvideo",
            "-pix_fmt",
            "rgb24",
            "-s",
            f"{self._in_w}x{self._in_h}",
            "-r",
            str(_validate_positive_int(fps, "fps")),
            "-i",
            "pipe:0",
            "-an",
            "-vf",
            vf,
            "-r",
            str(fps),
            "-frames:v",
            str(self._exact_frames),
            *_COLABONE_FFV1_CODEC_ARGS,
            str(self._temp_output),
        ]

        self._stderr_fh = self._stderr_path.open("wb")
        try:
            self._proc = subprocess.Popen(
                cmd,
                stdin=subprocess.PIPE,
                stdout=subprocess.DEVNULL,
                stderr=self._stderr_fh,
                env=_sanitized_subprocess_env(),
            )
            self._guard.register_child(self._proc)
        except BaseException:
            if self._proc is not None and self._proc.poll() is None:
                self._guard.terminate_child(self._proc, grace_seconds=1.5)
                self._guard.unregister_child(self._proc)
            self._close_stderr()
            raise

    def _close_stderr(self) -> None:
        if self._stderr_fh is not None:
            try:
                self._stderr_fh.close()
            except Exception:
                pass
            self._stderr_fh = None

    def _write_raw_frame(self, frame_bytes: bytes) -> None:
        if self._proc is None or self._proc.stdin is None:
            raise RuntimeError("FFV1 writer stdin is closed")
        try:
            self._proc.stdin.write(frame_bytes)
        except (BrokenPipeError, OSError) as exc:
            rc = self._wait_for_exit_on_error()
            err_tail = _read_stderr_tail(self._stderr_path)
            raise RuntimeError(
                f"FFmpeg FFV1 pipe write failed (code {rc}, {type(exc).__name__}): "
                f"{err_tail[-800:]}"
            ) from exc

    def _wait_for_exit_on_error(self) -> int | None:
        proc = self._proc
        if proc is None:
            return None
        if proc.stdin is not None:
            try:
                proc.stdin.close()
            except Exception:
                pass
        try:
            rc = proc.wait(timeout=5.0)
        except subprocess.TimeoutExpired:
            try:
                proc.kill()
            except OSError:
                pass
            rc = proc.wait(timeout=2.0)
        self._close_stderr()
        return rc

    def write_frames(self, frames_uint8: Any) -> None:
        import numpy as np  # type: ignore[import-not-found]

        if self._closed:
            raise RuntimeError("FFV1 writer is already closed")
        arr = np.asarray(frames_uint8, dtype=np.uint8)
        if arr.ndim != 4 or arr.shape[3] != 3:
            raise RuntimeError(
                f"Expected FlashVSR+ frame chunk shape (T, H, W, 3), got {tuple(arr.shape)}"
            )
        if arr.shape[1] != self._in_h or arr.shape[2] != self._in_w:
            raise RuntimeError(
                f"Frame chunk spatial shape {arr.shape[2]}x{arr.shape[1]} "
                f"does not match writer {self._in_w}x{self._in_h}"
            )

        remaining = self._exact_frames - self._frames_written
        if remaining <= 0 or arr.shape[0] <= 0:
            return

        count = min(int(arr.shape[0]), remaining)
        for idx in range(count):
            frame_bytes = np.ascontiguousarray(arr[idx]).tobytes()
            self._write_raw_frame(frame_bytes)
            self._last_frame_bytes = frame_bytes
            self._frames_written += 1

    def finalize(self) -> None:
        if self._closed:
            return
        if self._frames_written <= 0 or self._last_frame_bytes is None:
            self.abort()
            raise RuntimeError("FlashVSR+ produced zero output frames")
        while self._frames_written < self._exact_frames:
            self._write_raw_frame(self._last_frame_bytes)
            self._frames_written += 1

        self._closed = True
        proc = self._proc
        self._proc = None
        try:
            if proc is not None:
                if proc.stdin is not None:
                    try:
                        proc.stdin.close()
                    except Exception:
                        pass
                rc = proc.wait()
                self._close_stderr()
                if rc != 0:
                    err_tail = _read_stderr_tail(self._stderr_path)
                    raise RuntimeError(
                        f"FFmpeg FFV1 encode failed (code {rc}): {err_tail[-800:]}"
                    )
            os.replace(self._temp_output, self._output_video_path)
            self._committed = True
        finally:
            self._close_stderr()
            if proc is not None:
                if proc.poll() is None:
                    if proc.stdin is not None:
                        try:
                            proc.stdin.close()
                        except Exception:
                            pass
                    self._guard.terminate_child(proc, grace_seconds=1.5)
                self._guard.unregister_child(proc)
            if not self._committed and self._temp_output.exists():
                try:
                    self._temp_output.unlink()
                except OSError:
                    pass
            try:
                self._stderr_path.unlink(missing_ok=True)
            except OSError:
                pass

    def abort(self) -> None:
        if self._closed:
            return
        self._closed = True
        proc = self._proc
        self._proc = None
        try:
            if proc is not None:
                if proc.stdin is not None:
                    try:
                        proc.stdin.close()
                    except Exception:
                        pass
                if proc.poll() is None:
                    self._guard.terminate_child(proc, grace_seconds=2.0)
        finally:
            self._close_stderr()
            if proc is not None:
                if proc.poll() is None:
                    self._guard.terminate_child(proc, grace_seconds=1.5)
                self._guard.unregister_child(proc)
            if self._temp_output.exists():
                try:
                    self._temp_output.unlink()
                except OSError:
                    pass
            try:
                self._stderr_path.unlink(missing_ok=True)
            except OSError:
                pass


def _encode_rgb24_frames_cover_crop_lossless(
    ffmpeg_bin: str,
    frames_uint8: Any,
    output_video_path: Path,
    *,
    target_w: int,
    target_h: int,
    fps: int,
    exact_frames: int,
    tmp_dir: Path,
    guard: ColabOneProcessGuard,
) -> None:
    """Encode an in-memory uint8 ``(T, H, W, 3)`` array into lossless FFV1 MKV."""
    import numpy as np  # type: ignore[import-not-found]

    arr = np.asarray(frames_uint8, dtype=np.uint8)
    if arr.ndim != 4 or arr.shape[3] != 3:
        raise RuntimeError(
            f"Expected FlashVSR+ output shape (T, H, W, 3), got {tuple(arr.shape)}"
        )
    if arr.shape[0] <= 0:
        raise RuntimeError("FlashVSR+ produced zero output frames")
    writer = _ColabOneFfv1CoverCropWriter(
        ffmpeg_bin,
        output_video_path,
        in_w=int(arr.shape[2]),
        in_h=int(arr.shape[1]),
        target_w=target_w,
        target_h=target_h,
        fps=fps,
        exact_frames=exact_frames,
        tmp_dir=tmp_dir,
        guard=guard,
    )
    try:
        writer.write_frames(arr)
        writer.finalize()
    except BaseException:
        writer.abort()
        raise


def _crossfade_overlap_frames(prev_tail: Any, next_head: Any) -> Any:
    """Linearly crossfade ``(K, H, W, 3)`` uint8 overlap frames with ``w = (k + 1) / (K + 1)``."""
    import numpy as np  # type: ignore[import-not-found]

    prev_arr = np.asarray(prev_tail, dtype=np.uint8)
    next_arr = np.asarray(next_head, dtype=np.uint8)
    overlap = min(int(prev_arr.shape[0]), int(next_arr.shape[0]))
    if overlap <= 0:
        return next_arr[:0]

    blended_frames: list[Any] = []
    denom = float(overlap + 1)
    for k in range(overlap):
        w = float(k + 1) / denom
        inv_w = 1.0 - w
        mixed = (
            prev_arr[k].astype(np.float32) * inv_w
            + next_arr[k].astype(np.float32) * w
        )
        blended_frames.append(
            np.clip(np.rint(mixed), 0.0, 255.0).astype(np.uint8)
        )
    return np.stack(blended_frames, axis=0)


def _normalize_flashvsr_tensor_output(
    video_tensor: Any,
    *,
    expected_frames: int,
    cover_h: int,
    cover_w: int,
) -> Any:
    """Deterministically normalize FlashVSR+ ``[1, 3, F, pad_h, pad_w]`` in ``[-1, 1]``.

    Crops back to ``[:, :, :expected_frames, :cover_h, :cover_w]`` before
    converting to ``float32``, permutes to ``[F, H, W, 3]``, and unconditionally
    maps ``[-1.0, 1.0]`` to ``uint8 [0, 255]`` in-place without range-guessing
    heuristics or 3-frame channel-dimension ambiguity.
    """
    import torch  # type: ignore[import-not-found]

    if video_tensor is None or not hasattr(video_tensor, "detach"):
        raise RuntimeError(
            "FlashVSR+ returned missing or non-tensor result.video output"
        )
    vid = video_tensor.detach()
    if vid.ndim == 4 and vid.shape[0] == 3:
        vid = vid.unsqueeze(0)
    if vid.ndim != 5 or vid.shape[0] != 1 or vid.shape[1] != 3:
        raise RuntimeError(
            f"Expected FlashVSR+ result.video shape [1, 3, F, H, W], "
            f"got {tuple(vid.shape)}"
        )
    if vid.shape[2] <= 0 or vid.shape[3] < cover_h or vid.shape[4] < cover_w:
        raise RuntimeError(
            f"FlashVSR+ result.video spatial/temporal size {tuple(vid.shape)} "
            f"is smaller than expected cover size {cover_w}x{cover_h}"
        )

    cropped = vid[:, :, :expected_frames, :cover_h, :cover_w].cpu()
    t = cropped[0].permute(1, 2, 3, 0)  # [F, cover_h, cover_w, 3]
    del vid, cropped
    if (
        hasattr(t, "float")
        and hasattr(t, "clamp_")
        and hasattr(t, "add_")
        and hasattr(t, "mul_")
        and hasattr(t, "round_")
    ):
        t = (
            t.float()
            .clamp_(-1.0, 1.0)
            .add_(1.0)
            .mul_(127.5)
            .round_()
            .clamp_(0.0, 255.0)
            .to(torch.uint8)
        )
    else:
        t = (
            ((t.to(dtype=torch.float32).clamp(-1.0, 1.0) + 1.0) * 127.5)
            .round()
            .clamp(0.0, 255.0)
            .to(torch.uint8)
        )
    return t.contiguous().numpy()


def _run_flashvsr_python_pipeline(
    *,
    repo_dir: Path,
    weights_dir: Path,
    model_files: dict[str, Path],
    input_video_path: Path,
    output_video_path: Path,
    source_width: int,
    source_height: int,
    target_width: int,
    target_height: int,
    fps: int,
    exact_frames: int,
    seed: int,
    color_correction: str,
    vram_mib: int,
    ffmpeg_bin: str,
    guard: ColabOneProcessGuard,
) -> dict[str, Any]:
    """Run ``OpenImagingLab/FlashVSR_Plus`` streaming pipeline directly in-process."""
    import numpy as np  # type: ignore[import-not-found]
    import torch  # type: ignore[import-not-found]
    import torch.nn.functional as F  # type: ignore[import-not-found]

    search_paths = [str(repo_dir)]
    attn_op_dir = repo_dir / "operators" / "flashvsr_attn"
    if attn_op_dir.is_dir():
        search_paths.append(str(attn_op_dir))
    for path_str in reversed(search_paths):
        if os.path.isdir(path_str) and path_str not in sys.path:
            sys.path.insert(0, path_str)

    try:
        from inference import (  # type: ignore[import-not-found]
            InferenceRequest,
            ModelPaths,
            build_inference_pipeline,
            stream_config_for_mode,
        )
    except ImportError as exc:
        raise ImportError(
            f"Unable to import OpenImagingLab/FlashVSR_Plus inference API "
            f"from {repo_dir}: {exc}"
        ) from exc

    prompt_path = _locate_flashvsr_prompt_tensor(repo_dir, weights_dir)
    cover_w, cover_h, pad_w, pad_h = compute_flashvsr_cover_dimensions(
        source_width, source_height, target_width, target_height
    )
    chunk_plan = compute_flashvsr_chunk_plan(exact_frames)
    total_chunks = len(chunk_plan)

    emit_colabone_progress(
        "load_weights",
        0.10,
        f"正在加载 FlashVSR+ v1.1 流式扩散模型（{cover_w}×{cover_h}）",
        engine="flashvsr_plus",
        cover_width=cover_w,
        cover_height=cover_h,
        pad_width=pad_w,
        pad_height=pad_h,
        chunks=total_chunks,
        vram_mib=vram_mib,
    )

    device = "cuda:0" if torch.cuda.is_available() else "cpu"
    decode_every: int | str = (
        8 if (0 < vram_mib < COLABONE_VSR_LOW_VRAM_THRESHOLD_MIB) else "auto"
    )
    stream_cfg = stream_config_for_mode(
        "v1",
        device=device,
        seed=seed,
        decode_every=decode_every,
        color_correction=(color_correction != "none"),
    )
    dit_path = model_files["diffusion_pytorch_model_streaming_dmd.safetensors"]
    lq_proj_path = model_files["LQ_proj_in.ckpt"]
    tcdecoder_path = model_files["TCDecoder.ckpt"]
    model_paths = ModelPaths(
        dit=str(dit_path),
        lq_projection=str(lq_proj_path),
        context=str(prompt_path),
        tiny_decoder=str(tcdecoder_path),
    )

    pipeline = build_inference_pipeline(stream_cfg, model_paths)

    tmp_dir = guard.create_temp_dir(prefix="colabone_flashvsr_")
    reader: _ColabOneRgb24FrameReader | None = None
    writer: _ColabOneFfv1CoverCropWriter | None = None
    reader_ok = False
    try:
        reader = _ColabOneRgb24FrameReader(
            ffmpeg_bin,
            input_video_path,
            source_width,
            source_height,
            exact_frames,
            tmp_dir,
            guard,
        )
        writer = _ColabOneFfv1CoverCropWriter(
            ffmpeg_bin,
            output_video_path,
            in_w=cover_w,
            in_h=cover_h,
            target_w=target_width,
            target_h=target_height,
            fps=fps,
            exact_frames=exact_frames,
            tmp_dir=tmp_dir,
            guard=guard,
        )

        # Rolling raw input buffer holding only frames belonging to the current
        # chunk window so host RAM stays bounded on long (1-10 min) clips.
        window_frames: list[Any] = []
        window_start = 0
        last_input_frame: Any = None
        prev_tail_frames: Any = None

        for idx, chunk in enumerate(chunk_plan):
            chunk_idx = idx + 1
            c_start = chunk["start"]
            c_end = chunk["end"]
            chunk_len = c_end - c_start

            # Drop frames strictly before c_start from the rolling buffer.
            if c_start > window_start:
                drop = c_start - window_start
                window_frames = window_frames[drop:]
                window_start = c_start

            need_new = chunk_len - len(window_frames)
            if need_new > 0:
                newly_read = reader.read_frames(need_new)
                if newly_read:
                    last_input_frame = newly_read[-1]
                    window_frames.extend(newly_read)
                while len(window_frames) < chunk_len:
                    if last_input_frame is None:
                        raise RuntimeError(
                            f"Decoded zero RGB24 frames from {input_video_path}"
                        )
                    window_frames.append(last_input_frame)

            chunk_np = np.stack(window_frames[:chunk_len], axis=0)

            # Build [1, 3, T, pad_h, pad_w] strictly on CPU in float32 [-1.0, 1.0].
            # FlashVSR_Plus StreamRunner transfers 4-frame clips to CUDA on demand.
            tensor_tchw = (
                torch.from_numpy(np.ascontiguousarray(chunk_np))
                .permute(0, 3, 1, 2)
                .to(dtype=torch.float32)
                .div_(127.5)
                .sub_(1.0)
            )
            if cover_h != source_height or cover_w != source_width:
                tensor_tchw = F.interpolate(
                    tensor_tchw,
                    size=(cover_h, cover_w),
                    mode="bicubic",
                    align_corners=False,
                ).clamp_(-1.0, 1.0)
            if pad_h > cover_h or pad_w > cover_w:
                tensor_tchw = F.pad(
                    tensor_tchw,
                    (0, pad_w - cover_w, 0, pad_h - cover_h),
                    mode="replicate",
                )
            lq_video = tensor_tchw.permute(1, 0, 2, 3).unsqueeze(0).contiguous()
            del chunk_np, tensor_tchw

            chunk_progress = 0.20 + 0.65 * ((chunk_idx - 1) / max(1, total_chunks))
            emit_colabone_progress(
                "upscale",
                chunk_progress,
                f"正在执行 FlashVSR+ v1.1 流式扩散超分（第 {chunk_idx}/{total_chunks} 段）",
                engine="flashvsr_plus",
                chunk_index=chunk_idx,
                total_chunks=total_chunks,
            )

            request = InferenceRequest(
                lq_video=lq_video,
                output_path=None,
                fps=float(fps),
            )
            with torch.inference_mode():
                result = pipeline.infer(request)

            out_u8 = _normalize_flashvsr_tensor_output(
                getattr(result, "video", None),
                expected_frames=chunk_len,
                cover_h=cover_h,
                cover_w=cover_w,
            )
            if out_u8.shape[0] < chunk_len:
                pad_tail = np.repeat(
                    out_u8[-1:], chunk_len - out_u8.shape[0], axis=0
                )
                out_u8 = np.concatenate([out_u8, pad_tail], axis=0)

            prev_overlap = (
                int(prev_tail_frames.shape[0])
                if prev_tail_frames is not None
                else 0
            )
            if prev_tail_frames is not None and prev_overlap > 0:
                blended = _crossfade_overlap_frames(
                    prev_tail_frames, out_u8[:prev_overlap]
                )
                writer.write_frames(blended)
                del blended
                prev_tail_frames = None

            if idx + 1 < total_chunks:
                next_start = chunk_plan[idx + 1]["start"]
                next_overlap = max(
                    0, min(c_end - next_start, chunk_len - prev_overlap)
                )
            else:
                next_overlap = 0

            body_end = chunk_len - next_overlap
            if body_end > prev_overlap:
                writer.write_frames(out_u8[prev_overlap:body_end])

            if next_overlap > 0:
                prev_tail_frames = out_u8[body_end:].copy()

            del lq_video, request, result, out_u8
            if torch.cuda.is_available():
                torch.cuda.empty_cache()

        prev_tail_frames = None
        reader_ok = True
        reader.close(check_returncode=True)

        emit_colabone_progress(
            "encode",
            0.90,
            "正在将 FlashVSR+ 超分帧封装为无损 FFV1 中间视频",
            engine="flashvsr_plus",
        )
        writer.finalize()
    except BaseException:
        try:
            if reader is not None:
                reader.close(check_returncode=False)
        finally:
            if writer is not None:
                writer.abort()
        raise
    finally:
        if not reader_ok and reader is not None:
            reader.close(check_returncode=False)

    return {
        "cover_width": cover_w,
        "cover_height": cover_h,
        "pad_width": pad_w,
        "pad_height": pad_h,
        "chunks": total_chunks,
        "execution_mode": "pipeline",
    }


def _run_flashvsr_engine(
    *,
    repo_dir: Path,
    weights_dir: Path,
    model_files: dict[str, Path],
    input_video_path: Path,
    output_video_path: Path,
    source_width: int,
    source_height: int,
    target_width: int,
    target_height: int,
    fps: int,
    exact_frames: int,
    seed: int,
    color_correction: str,
    vram_mib: int,
    ffmpeg_bin: str,
    guard: ColabOneProcessGuard,
) -> dict[str, Any]:
    _mode, discovered_path = discover_flashvsr_entrypoint(repo_dir)
    if discovered_path is None:
        raise FileNotFoundError(
            f"FlashVSR+ inference pipeline not found under {repo_dir} "
            f"(expected {repo_dir / 'inference' / 'pipeline.py'})"
        )
    scale = build_flashvsr_scale_factor(
        source_width, source_height, target_width, target_height
    )
    meta = _run_flashvsr_python_pipeline(
        repo_dir=repo_dir,
        weights_dir=weights_dir,
        model_files=model_files,
        input_video_path=input_video_path,
        output_video_path=output_video_path,
        source_width=source_width,
        source_height=source_height,
        target_width=target_width,
        target_height=target_height,
        fps=fps,
        exact_frames=exact_frames,
        seed=seed,
        color_correction=color_correction,
        vram_mib=vram_mib,
        ffmpeg_bin=ffmpeg_bin,
        guard=guard,
    )
    return {
        "scale_factor": scale,
        **meta,
    }


def run_diffusion_vsr(cfg: dict[str, Any]) -> dict[str, Any]:
    """Execute Stage 3 diffusion VSR upscaling and write an atomic completion manifest."""
    if not isinstance(cfg, dict):
        raise ValueError("Worker config must be a dict")

    started = time.monotonic()

    engine = str(cfg.get("engine") or "").strip().lower()
    repo_dir = _validate_safe_path(_require_cfg_value(cfg, "repo_dir"), "repo_dir")
    weights_dir = _validate_safe_path(
        _require_cfg_value(cfg, "weights_dir"), "weights_dir"
    )
    input_video_path = _validate_safe_path(
        _require_cfg_value(cfg, "input_video_path"), "input_video_path"
    )
    output_video_path = _validate_safe_path(
        _require_cfg_value(cfg, "output_video_path"), "output_video_path"
    )
    output_manifest_path = _validate_safe_path(
        _require_cfg_value(cfg, "output_manifest_path"), "output_manifest_path"
    )

    _reject_symlink(output_video_path, "output_video_path")
    _reject_symlink(output_manifest_path, "output_manifest_path")

    raw_work_dir = cfg.get("work_dir")
    if raw_work_dir is not None and str(raw_work_dir).strip():
        work_dir = _validate_safe_path(raw_work_dir, "work_dir")
    else:
        work_dir = output_video_path.parent
    _reject_symlink(work_dir, "work_dir")

    model_files = resolve_vsr_model_files(engine, weights_dir)

    source_width = _validate_positive_int(
        _require_cfg_value(cfg, "source_width"), "source_width"
    )
    source_height = _validate_positive_int(
        _require_cfg_value(cfg, "source_height"), "source_height"
    )
    target_width = _validate_positive_int(
        _require_cfg_value(cfg, "target_width"), "target_width"
    )
    target_height = _validate_positive_int(
        _require_cfg_value(cfg, "target_height"), "target_height"
    )
    for dim_name, dim_val in (
        ("source_width", source_width),
        ("source_height", source_height),
        ("target_width", target_width),
        ("target_height", target_height),
    ):
        if dim_val < 16 or dim_val % 2 != 0:
            raise ValueError(
                f"{dim_name} must be an even integer >= 16, got {dim_val}"
            )

    raw_fps = cfg.get("fps")
    fps = 25 if raw_fps is None else _validate_positive_int(raw_fps, "fps")

    raw_seed = cfg.get("seed")
    seed = 42 if raw_seed is None else _validate_non_negative_int(raw_seed, "seed")

    raw_batch = cfg.get("batch_size")
    batch_size = (
        33 if raw_batch is None else _validate_positive_int(raw_batch, "batch_size")
    )

    raw_overlap = cfg.get("temporal_overlap")
    temporal_overlap = (
        4
        if raw_overlap is None
        else _validate_non_negative_int(raw_overlap, "temporal_overlap")
    )

    raw_exact_frames = cfg.get("exact_frames")
    exact_frames_raw = (
        0
        if raw_exact_frames is None
        else _validate_non_negative_int(raw_exact_frames, "exact_frames")
    )

    raw_vram = cfg.get("vram_mib")
    vram_mib = (
        0
        if raw_vram is None
        else _validate_non_negative_int(raw_vram, "vram_mib")
    )

    color_correction = str(cfg.get("color_correction") or "lab").strip().lower()
    if color_correction not in COLABONE_VSR_COLOR_CORRECTION_MODES:
        raise ValueError(
            f"Unsupported color_correction: {color_correction!r}. "
            f"Expected one of: {', '.join(COLABONE_VSR_COLOR_CORRECTION_MODES)}"
        )

    raw_ffmpeg = cfg.get("ffmpeg_bin")
    ffmpeg_bin = str(
        _validate_safe_path(
            raw_ffmpeg
            if raw_ffmpeg is not None and str(raw_ffmpeg).strip()
            else "ffmpeg",
            "ffmpeg_bin",
        )
    )
    raw_python = cfg.get("python_bin")
    python_bin = str(
        _validate_safe_path(
            raw_python
            if raw_python is not None and str(raw_python).strip()
            else sys.executable,
            "python_bin",
        )
    )

    if not input_video_path.is_file():
        raise FileNotFoundError(f"Input video not found: {input_video_path}")
    if not repo_dir.is_dir():
        raise FileNotFoundError(f"VSR repository directory not found: {repo_dir}")
    if not weights_dir.is_dir():
        raise FileNotFoundError(f"VSR weights directory not found: {weights_dir}")

    missing_weights = [
        name
        for name, path in model_files.items()
        if not path.is_file() or path.stat().st_size <= 0
    ]
    if missing_weights:
        raise FileNotFoundError(
            f"Missing or empty {engine} weight file(s) under {weights_dir}: "
            + ", ".join(missing_weights)
        )

    if exact_frames_raw > 0:
        exact_frames = exact_frames_raw
    else:
        probed_in = _probe_video_metadata(ffmpeg_bin, input_video_path)
        probed_frames = int(probed_in.get("frames") or 0)
        if probed_frames <= 0:
            raise ValueError(
                "exact_frames must be > 0 or determinable from input_video_path"
            )
        exact_frames = probed_frames

    if vram_mib <= 0:
        vram_mib = _detect_gpu_vram_mib()

    work_dir.mkdir(parents=True, exist_ok=True)
    output_video_path.parent.mkdir(parents=True, exist_ok=True)
    output_manifest_path.parent.mkdir(parents=True, exist_ok=True)

    guard = ColabOneProcessGuard(base_dir=work_dir)
    prev_sigterm = None
    prev_sigint = None
    can_install_signals = hasattr(signal, "SIGTERM")
    if can_install_signals:
        try:
            prev_sigterm = signal.signal(signal.SIGTERM, guard.handle_signal)
            prev_sigint = signal.signal(signal.SIGINT, guard.handle_signal)
        except ValueError:
            # Running in a non-main thread (e.g. unit tests); skip signal registration.
            can_install_signals = False

    try:
        if engine in COLABONE_VSR_SEEDVR2_ENGINES:
            extra_meta = _run_seedvr2_engine(
                engine=engine,
                python_bin=python_bin,
                repo_dir=repo_dir,
                weights_dir=weights_dir,
                input_video_path=input_video_path,
                output_video_path=output_video_path,
                source_width=source_width,
                source_height=source_height,
                target_width=target_width,
                target_height=target_height,
                fps=fps,
                exact_frames=exact_frames,
                seed=seed,
                batch_size=batch_size,
                temporal_overlap=temporal_overlap,
                color_correction=color_correction,
                vram_mib=vram_mib,
                ffmpeg_bin=ffmpeg_bin,
                guard=guard,
            )
        else:
            extra_meta = _run_flashvsr_engine(
                repo_dir=repo_dir,
                weights_dir=weights_dir,
                model_files=model_files,
                input_video_path=input_video_path,
                output_video_path=output_video_path,
                source_width=source_width,
                source_height=source_height,
                target_width=target_width,
                target_height=target_height,
                fps=fps,
                exact_frames=exact_frames,
                seed=seed,
                color_correction=color_correction,
                vram_mib=vram_mib,
                ffmpeg_bin=ffmpeg_bin,
                guard=guard,
            )

        _reject_symlink(output_video_path, "output_video_path")
        if not output_video_path.is_file() or output_video_path.stat().st_size <= 0:
            raise RuntimeError(
                f"Expected lossless output video missing or empty: {output_video_path}"
            )

        probed_out = _probe_video_metadata(ffmpeg_bin, output_video_path)
        if not probed_out:
            raise RuntimeError(
                f"Failed to verify lossless output video metadata via ffprobe: "
                f"{output_video_path}"
            )
        probed_w = int(probed_out.get("width") or 0)
        probed_h = int(probed_out.get("height") or 0)
        probed_frames = int(probed_out.get("frames") or 0)
        if probed_w != target_width or probed_h != target_height:
            raise RuntimeError(
                f"Output video dimensions mismatch: expected "
                f"{target_width}x{target_height}, got {probed_w}x{probed_h}"
            )
        if probed_frames != exact_frames:
            raise RuntimeError(
                f"Output video frame count mismatch: expected {exact_frames}, "
                f"got {probed_frames}"
            )
        frames_written = probed_frames
        elapsed_seconds = round(time.monotonic() - started, 3)

        manifest: dict[str, Any] = {
            "ok": True,
            "engine": engine,
            "output_video_path": str(output_video_path),
            "width": target_width,
            "height": target_height,
            "target_width": target_width,
            "target_height": target_height,
            "fps": fps,
            "frames_written": frames_written,
            "elapsed_seconds": elapsed_seconds,
            "source_width": source_width,
            "source_height": source_height,
            "vram_mib": vram_mib,
            "color_correction": color_correction,
            "seed": seed,
            "file_size_bytes": int(output_video_path.stat().st_size),
            **extra_meta,
        }
        atomic_write_json(output_manifest_path, manifest)
        emit_colabone_progress(
            "complete",
            1.0,
            f"已完成 {engine} 视频扩散超分（共 {frames_written} 帧，耗时 {elapsed_seconds:.1f} 秒）",
            engine=engine,
            frames_written=frames_written,
            elapsed_seconds=elapsed_seconds,
        )
        return manifest
    finally:
        guard._tearing_down = True
        try:
            guard.cleanup_all(grace_seconds=1.5)
        finally:
            if can_install_signals:
                try:
                    if prev_sigterm is not None:
                        signal.signal(signal.SIGTERM, prev_sigterm)
                    if prev_sigint is not None:
                        signal.signal(signal.SIGINT, prev_sigint)
                except ValueError:
                    pass


def main(argv: list[str] | None = None) -> int:
    _ensure_utf8_stdio()
    parser = argparse.ArgumentParser(
        description="ColabOne standalone diffusion VSR worker (SeedVR2 7B / FlashVSR+)"
    )
    parser.add_argument(
        "--config",
        required=True,
        help="Path to JSON configuration file",
    )
    args = parser.parse_args(argv)

    config_path = _validate_safe_path(args.config, "config")
    cfg = json.loads(config_path.read_text(encoding="utf-8"))
    if not isinstance(cfg, dict):
        raise ValueError("Worker config JSON must be an object")

    manifest_path_raw = cfg.get("output_manifest_path")
    engine = str(cfg.get("engine") or "").strip().lower()
    started = time.monotonic()

    try:
        run_diffusion_vsr(cfg)
        return 0
    except _ColabOneWorkerTerminated as exc:
        if manifest_path_raw:
            try:
                atomic_write_json(
                    manifest_path_raw,
                    {
                        "ok": False,
                        "engine": engine,
                        "error": str(exc),
                        "terminated_signal": exc.signum,
                        "elapsed_seconds": round(time.monotonic() - started, 3),
                    },
                )
            except Exception:
                pass
        return 128 + exc.signum
    except Exception as exc:
        if manifest_path_raw:
            try:
                atomic_write_json(
                    manifest_path_raw,
                    {
                        "ok": False,
                        "engine": engine,
                        "error": f"{type(exc).__name__}: {exc}",
                        "elapsed_seconds": round(time.monotonic() - started, 3),
                    },
                )
            except Exception:
                pass
        sys.stderr.write(f"ColabOne diffusion VSR worker failed: {exc}\n")
        sys.stderr.flush()
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
