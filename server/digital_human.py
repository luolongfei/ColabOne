"""Remote end-to-end long-video digital human pipeline runner for ComfyUI.

Orchestrates remote TTS synthesis or uploaded audio normalization, silence-valley
segmentation, portrait anchor preparation, multi-segment tail-frame relay rendering
through ComfyUI (with optional in-graph AI 2x super-resolution), and final
concatenation + 48 kHz master audio muxing.

Runs exclusively on the remote GPU runtime; all planning and audio/image helpers
are pure-Python and 100% offline unit-testable.
"""

from __future__ import annotations

import array
import copy
import glob
import inspect
import math
import operator
import os
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import threading
import time
import wave
from contextlib import nullcontext
from pathlib import Path, PurePosixPath
from typing import TYPE_CHECKING, Any, Callable

import requests

from common.comfyui import (
    MAX_OUTPUT_BYTES,
    WorkflowRunRequest,
    apply_workflow_overrides,
)

if TYPE_CHECKING:
    from server.tts_engines import ColabOneRemoteTtsRunner

_RES_SPEC_RE = re.compile(r"^\s*(\d+)\s*[xX×*]\s*(\d+)\s*$")

# Standard broadcast 1080p dimensions (1080 is divisible by 8, not 16) are
# preserved verbatim so Full HD target output is never distorted to 1072/1088.
_EXACT_STANDARD_RESOLUTIONS = frozenset({(1920, 1080), (1080, 1920)})

_VIDEO_EXTENSIONS = frozenset({".mp4", ".webm", ".mkv", ".mov", ".gif"})
_NON_VIDEO_EXTENSIONS = frozenset(
    {
        ".png",
        ".jpg",
        ".jpeg",
        ".webp",
        ".bmp",
        ".tiff",
        ".wav",
        ".mp3",
        ".flac",
        ".ogg",
        ".m4a",
        ".json",
        ".txt",
    }
)
_UPSCALE_MODEL_EXTENSIONS = frozenset({".safetensors", ".pth", ".pt", ".ckpt", ".bin"})

DEFAULT_AI_UPSCALE_MODEL = "2xNomosUni_span_multijpg.safetensors"

# WanVideoLongCatAvatarExtendEmbeds enforces num_frames <= 256 in ComfyUI;
# 4 * 63 + 1 = 253 is the largest valid 4k+1 Wan temporal frame count <= 256.
LONGCAT_MAX_FRAMES = 253
LONGCAT_MAX_SEGMENT_SECONDS = 9.5
# Cap AI 2x upscale segments to <= 20s (<= 501 frames @ 25fps) so ImageUpscaleWithModel
# peak host RAM stays safely inside Colab's 51–83 GB budget without OOM.
AI_UPSCALE_MAX_SEGMENT_SECONDS = 20.0
_STALE_WORK_DIR_MAX_AGE_SECONDS = 24 * 3600
_SENSITIVE_SUBPROCESS_ENV_KEYS = frozenset({"HF_TOKEN", "HUGGING_FACE_HUB_TOKEN"})


def _sanitize_task_tag(raw_tag: Any, *, allow_empty: bool = False) -> str:
    """Restrict task tags used in filenames and glob prefixes to [a-zA-Z0-9_-]."""
    cleaned = re.sub(r"[^a-zA-Z0-9_-]", "", str(raw_tag or ""))[:64]
    if cleaned:
        return cleaned
    return "" if allow_empty else "task"


def _validate_safe_path(path: Path | str, label: str = "路径") -> Path:
    """Reject CR, LF, and NUL control characters in filesystem paths passed to ffmpeg."""
    raw = os.fspath(path)
    if any(ch in raw for ch in ("\r", "\n", "\x00")):
        raise ValueError(f"{label}不能包含换行符或空字符")
    return Path(raw)


def _sanitized_subprocess_env() -> dict[str, str]:
    """Return a subprocess environment with Hugging Face tokens stripped."""
    return {
        k: v
        for k, v in os.environ.items()
        if k not in _SENSITIVE_SUBPROCESS_ENV_KEYS
    }


def _safe_contained_file(base_dir_raw: Path, *rel_parts: str) -> Path | None:
    """Verify a regular file strictly inside base_dir_raw with zero symlinks on the way.

    Checks is_symlink() on base_dir_raw, every intermediate component, and the leaf
    BEFORE calling .resolve(), then asserts base_dir in resolved.parents. Returns the
    unresolved Path so callers unlink/read the exact entry and never follow a symlink.
    """
    try:
        if base_dir_raw.is_symlink() or not base_dir_raw.is_dir():
            return None
        non_empty = [str(p) for p in rel_parts if p is not None and str(p) != ""]
        if not non_empty:
            return None
        normalized_parts: list[str] = []
        for part in non_empty:
            if "\x00" in part or "\r" in part or "\n" in part:
                return None
            normalized_parts.append(part.replace("\\", "/"))
        rel = Path(*normalized_parts)
        if rel.is_absolute() or not rel.parts or any(p == ".." for p in rel.parts):
            return None

        cur_raw = base_dir_raw
        for part in rel.parts:
            cur_raw = cur_raw / part
            if cur_raw.is_symlink():
                return None

        if not cur_raw.is_file():
            return None

        base_dir = base_dir_raw.resolve()
        resolved = cur_raw.resolve()
        if base_dir not in resolved.parents:
            return None
        if resolved != base_dir.joinpath(*rel.parts):
            return None
        return cur_raw
    except (OSError, ValueError):
        return None


def _ensure_even_dimension(value: int, minimum: int = 16, maximum: int = 4096) -> int:
    clamped = max(minimum, min(maximum, int(value)))
    even = (clamped // 2) * 2
    return max(minimum, even)


def _clamp_multiple_of_16(value: int, minimum: int = 256, maximum: int = 4096) -> int:
    clamped = max(minimum, min(maximum, int(value)))
    snapped = int(round(clamped / 16.0)) * 16
    return max(minimum, min(maximum, snapped))


def parse_target_resolution(
    spec: str, default: tuple[int, int] = (1280, 720)
) -> tuple[int, int]:
    """Parse a resolution string like '1280x720' or '1080x1920'.

    Preserves exact standard 1080p dimensions, clamps all other parsed
    dimensions to multiples of 16 within 256..4096, and guarantees even
    positive dimensions even for the default fallback.
    """
    fallback = (
        _ensure_even_dimension(int(default[0])),
        _ensure_even_dimension(int(default[1])),
    )
    if not isinstance(spec, str) or not spec.strip():
        return fallback
    match = _RES_SPEC_RE.fullmatch(spec)
    if not match:
        return fallback
    raw_w, raw_h = int(match.group(1)), int(match.group(2))
    if raw_w <= 0 or raw_h <= 0:
        return fallback
    if (raw_w, raw_h) in _EXACT_STANDARD_RESOLUTIONS:
        return (raw_w, raw_h)
    return (_clamp_multiple_of_16(raw_w), _clamp_multiple_of_16(raw_h))


def compute_base_dimensions(
    video_backend: str, target_w: int, target_h: int
) -> tuple[int, int]:
    """Compute native diffusion base (width, height) for a video backend."""
    portrait = int(target_h) > int(target_w)
    backend = str(video_backend or "").strip().lower()
    if backend == "longcat_avatar":
        if max(int(target_w), int(target_h)) >= 1280:
            return (768, 1280) if portrait else (1280, 768)
        return (480, 832) if portrait else (832, 480)
    # "infinitetalk" (Wan2.1 480p sweet spot) and fallback "custom".
    return (480, 832) if portrait else (832, 480)


def align_wan_frames(frame_count: int, minimum: int = 17) -> int:
    """Align frame count upward to Wan 4k+1 temporal latent stride."""
    n = max(int(minimum), int(frame_count))
    rem = (n - 1) % 4
    if rem:
        n += 4 - rem
    return n


def _decode_pcm_window_energy(
    raw_bytes: bytes, sampwidth: int, nchannels: int = 1
) -> tuple[float, int]:
    """Return DC-removed (sum_of_squares, sample_count) for a chunk of raw PCM frames."""
    if not raw_bytes:
        return (0.0, 0)
    samples: Any
    if sampwidth == 2:
        samples = array.array("h")
        samples.frombytes(raw_bytes)
        if sys.byteorder != "little":
            samples.byteswap()
    elif sampwidth == 1:
        samples_u8 = array.array("B")
        samples_u8.frombytes(raw_bytes)
        samples = [s - 128 for s in samples_u8]
    elif sampwidth == 3:
        count = len(raw_bytes) // 3
        samples = [
            int.from_bytes(raw_bytes[idx : idx + 3], "little", signed=True)
            for idx in range(0, count * 3, 3)
        ]
    elif sampwidth == 4:
        samples = array.array("i")
        samples.frombytes(raw_bytes)
        if sys.byteorder != "little":
            samples.byteswap()
    else:
        raise ValueError(f"不支持的 PCM 采样位深：{sampwidth} 字节")

    count = len(samples)
    if count <= 0:
        return (0.0, 0)

    # Subtract per-window DC offset using exact integer arithmetic so recordings
    # with microphone DC bias still detect interior silence valleys cleanly.
    ch_count = max(1, int(nchannels))
    if ch_count == 1 or count < ch_count:
        total = sum(samples)
        sum_sq = sum(map(operator.mul, samples, samples))
        ac_energy = max(0.0, (count * sum_sq - total * total) / count)
        return (ac_energy, count)

    ac_energy = 0.0
    for ch in range(ch_count):
        ch_samples = samples[ch::ch_count]
        n_ch = len(ch_samples)
        if n_ch <= 0:
            continue
        total = sum(ch_samples)
        sum_sq = sum(map(operator.mul, ch_samples, ch_samples))
        ac_energy += max(0.0, (n_ch * sum_sq - total * total) / n_ch)
    return (ac_energy, count)


def _pcm_full_scale(sampwidth: int) -> float:
    if sampwidth == 1:
        return 128.0
    if sampwidth == 2:
        return 32768.0
    if sampwidth == 3:
        return 8388608.0
    if sampwidth == 4:
        return 2147483648.0
    raise ValueError(f"不支持的 PCM 采样位深：{sampwidth} 字节")


def detect_pcm_silence_valleys(
    wav_path: Path,
    min_silence_sec: float = 0.18,
    threshold_db: float = -36.0,
) -> list[float]:
    """Scan a PCM WAV file with 20 ms DC-removed RMS hops and return silence valley midpoints.

    Only interior silence runs bounded by non-silent audio on both sides are
    returned, preventing leading or trailing silence from being treated as a
    segment cut valley.
    """
    path = Path(wav_path)
    min_sil = max(0.02, float(min_silence_sec))
    threshold_amp = 10.0 ** (float(threshold_db) / 20.0)

    with wave.open(str(path), "rb") as wf:
        nchannels = wf.getnchannels()
        sampwidth = wf.getsampwidth()
        framerate = wf.getframerate()
        nframes = wf.getnframes()
        if nchannels <= 0 or framerate <= 0 or nframes <= 0:
            return []
        full_scale = _pcm_full_scale(sampwidth)
        hop_frames = max(1, int(round(framerate * 0.02)))
        frame_bytes = nchannels * sampwidth
        # Read 5-second super-chunks to bound peak memory on long audio tracks.
        batch_hops = 250
        batch_frames = hop_frames * batch_hops

        silent_flags: list[bool] = []
        frames_read = 0
        while frames_read < nframes:
            to_read = min(batch_frames, nframes - frames_read)
            buf = wf.readframes(to_read)
            if not buf:
                break
            actual_frames = len(buf) // frame_bytes
            if actual_frames <= 0:
                break
            frames_read += actual_frames
            offset = 0
            for _ in range(0, actual_frames, hop_frames):
                end_off = min(actual_frames * frame_bytes, offset + hop_frames * frame_bytes)
                chunk = buf[offset:end_off]
                offset = end_off
                energy, count = _decode_pcm_window_energy(
                    chunk, sampwidth, nchannels=nchannels
                )
                if count <= 0:
                    continue
                rms_norm = math.sqrt(energy / count) / full_scale
                silent_flags.append(rms_norm <= threshold_amp)

    total_windows = len(silent_flags)
    if total_windows < 3:
        return []

    valleys: list[float] = []
    idx = 0
    while idx < total_windows:
        if not silent_flags[idx]:
            idx += 1
            continue
        run_start = idx
        while idx < total_windows and silent_flags[idx]:
            idx += 1
        run_end = idx
        # Exclude leading and trailing silence so only interior valleys remain.
        if run_start == 0 or run_end == total_windows:
            continue
        start_sec = (run_start * hop_frames) / framerate
        end_sec = min(nframes, run_end * hop_frames) / framerate
        if (end_sec - start_sec) >= min_sil - 1e-6:
            valleys.append(round((start_sec + end_sec) / 2.0, 3))

    return valleys


def _extract_valid_timestamps(raw: Any, duration: float) -> list[float]:
    if not isinstance(raw, (list, tuple)):
        return []
    out: list[float] = []
    for item in raw:
        val: Any = item
        if isinstance(item, dict):
            for key in ("mid", "midpoint", "time", "timestamp"):
                if key in item:
                    val = item[key]
                    break
        if isinstance(val, (int, float)) and not isinstance(val, bool):
            fval = float(val)
            if math.isfinite(fval) and 0.0 < fval < duration:
                out.append(fval)
    return sorted(set(out))


def plan_audio_segments(
    duration_sec: float,
    max_segment_sec: float = 45.0,
    manifest: dict[str, Any] | None = None,
    silence_valleys: list[float] | None = None,
) -> list[tuple[float, float]]:
    """Plan contiguous (start, end) audio segments covering [0.0, duration_sec].

    Prefers natural silence valleys from `manifest["silence_valleys"]`, then
    `silence_valleys`, falling back to clean `max_segment_sec` cuts while
    avoiding tiny (< 2 s) trailing slivers.
    """
    duration = float(duration_sec)
    if not math.isfinite(duration) or duration <= 0.0:
        return []
    max_seg = float(max_segment_sec)
    if not math.isfinite(max_seg) or max_seg <= 0.0:
        raise ValueError("max_segment_sec 必须大于 0")

    rounded_total = round(duration, 3)
    if rounded_total <= 0.0:
        return []
    if duration <= max_seg + 1e-6:
        return [(0.0, rounded_total)]

    manifest_raw = (
        manifest.get("silence_valleys") if isinstance(manifest, dict) else None
    )
    primary_valleys = _extract_valid_timestamps(manifest_raw, duration)
    secondary_valleys = _extract_valid_timestamps(silence_valleys, duration)

    min_span = max(8.0, max_seg * 0.45) if max_seg >= 10.0 else max_seg * 0.45
    min_tail = min(2.0, max_seg * 0.25)

    segments: list[tuple[float, float]] = []
    start = 0.0
    while True:
        remaining = duration - start
        if remaining <= max_seg + 1e-6:
            end_ts = rounded_total
            if end_ts > start + 1e-6:
                segments.append((round(start, 3), end_ts))
            break

        hi = start + max_seg
        # Keep the remaining tail >= min_tail whenever possible so we never
        # emit a tiny < 2 s trailing segment while staying <= max_seg.
        if duration - hi < min_tail:
            if remaining >= 2.0 * min_tail:
                safe_hi = max(start + min_tail, duration - min_tail)
            else:
                safe_hi = start + remaining / 2.0
        else:
            safe_hi = hi

        lo = start + min_span
        if lo > safe_hi:
            lo = max(start + min_tail, safe_hi * 0.5 + start * 0.5)

        chosen: float | None = None
        for candidates in (primary_valleys, secondary_valleys):
            in_range = [t for t in candidates if lo - 1e-6 <= t <= safe_hi + 1e-6]
            if in_range:
                chosen = in_range[-1]
                break

        cut = chosen if chosen is not None else safe_hi
        cut = round(min(safe_hi, max(start + 0.1, cut)), 3)
        if cut <= start:
            cut = round(min(duration, start + max_seg), 3)
        segments.append((round(start, 3), cut))
        start = cut
        if start >= rounded_total - 1e-6:
            break

    return segments


def prepare_anchor_image(
    src_path: Path, dst_path: Path, width: int, height: int
) -> Path:
    """Normalize EXIF orientation and fit portrait anchor image to (width, height)."""
    from PIL import Image, ImageOps

    src = Path(src_path)
    dst = Path(dst_path)
    target_w = max(16, int(width))
    target_h = max(16, int(height))
    dst.parent.mkdir(parents=True, exist_ok=True)

    resampling = getattr(Image, "Resampling", Image).LANCZOS
    with Image.open(src) as img:
        img.load()
        oriented = ImageOps.exif_transpose(img)
        if oriented.mode != "RGB":
            oriented = oriented.convert("RGB")
        fitted = ImageOps.fit(
            oriented,
            (target_w, target_h),
            method=resampling,
            centering=(0.5, 0.35),
        )
        fitted.save(dst, format="PNG")
    return dst


def inject_ai_upscale_nodes(
    workflow: dict[str, Any],
    upscale_model_name: str,
    video_combine_node_id: str = "11",
    target_w: int = 1280,
    target_h: int = 720,
    base_w: int | None = None,
    base_h: int | None = None,
) -> dict[str, Any]:
    """Deep-copy workflow and inject AI 2x super-resolution directly before VHS_VideoCombine.

    Wires UpscaleModelLoader -> ImageUpscaleWithModel -> VHS_VideoCombine directly
    without an in-graph PIL ImageScale node, eliminating the extra multi-GB float32
    frame-batch copy and double-Lanczos resampling (Stage 4 FFmpeg performs the single
    final scale/crop to exact target dimensions). Calling this helper repeatedly
    updates the loader model in-place without creating graph cycles, and cleanly
    bypasses any legacy colabone_upscale_fit node.
    """
    _ = (target_w, target_h, base_w, base_h)
    result = copy.deepcopy(workflow)
    model_name = str(upscale_model_name or "").strip()
    if not model_name or not isinstance(result, dict):
        return result

    combine_node: dict[str, Any] | None = None
    preferred = result.get(video_combine_node_id)
    if (
        isinstance(preferred, dict)
        and preferred.get("class_type") == "VHS_VideoCombine"
        and isinstance(preferred.get("inputs"), dict)
        and "images" in preferred["inputs"]
    ):
        combine_node = preferred
    else:
        for node in result.values():
            if (
                isinstance(node, dict)
                and node.get("class_type") == "VHS_VideoCombine"
                and isinstance(node.get("inputs"), dict)
                and "images" in node["inputs"]
            ):
                combine_node = node
                break

    if combine_node is None:
        return result

    orig_images_link = copy.deepcopy(combine_node["inputs"]["images"])
    if orig_images_link is None:
        return result

    injected_ids = frozenset(
        {"colabone_upscale_loader", "colabone_upscale_apply", "colabone_upscale_fit"}
    )

    def _is_injected_link(link: Any) -> bool:
        return (
            isinstance(link, list)
            and len(link) >= 1
            and str(link[0]) in injected_ids
        )

    if _is_injected_link(orig_images_link):
        existing_apply = result.get("colabone_upscale_apply")
        existing_fit = result.get("colabone_upscale_fit")
        apply_upstream = (
            copy.deepcopy(existing_apply["inputs"].get("image"))
            if isinstance(existing_apply, dict)
            and isinstance(existing_apply.get("inputs"), dict)
            else None
        )
        fit_upstream = (
            copy.deepcopy(existing_fit["inputs"].get("image"))
            if isinstance(existing_fit, dict)
            and isinstance(existing_fit.get("inputs"), dict)
            else None
        )

        resolved_upstream: Any = None
        if apply_upstream is not None and not _is_injected_link(apply_upstream):
            resolved_upstream = apply_upstream
        elif fit_upstream is not None and not _is_injected_link(fit_upstream):
            resolved_upstream = fit_upstream

        if resolved_upstream is None:
            if isinstance(result.get("colabone_upscale_loader"), dict) and isinstance(
                result["colabone_upscale_loader"].get("inputs"), dict
            ):
                result["colabone_upscale_loader"]["inputs"]["model_name"] = model_name
            else:
                result["colabone_upscale_loader"] = {
                    "class_type": "UpscaleModelLoader",
                    "_meta": {"title": "AI 超分模型加载"},
                    "inputs": {"model_name": model_name},
                }
            if isinstance(existing_apply, dict) and isinstance(
                existing_apply.get("inputs"), dict
            ):
                existing_apply["inputs"]["upscale_model"] = [
                    "colabone_upscale_loader",
                    0,
                ]
                combine_node["inputs"]["images"] = ["colabone_upscale_apply", 0]
            result.pop("colabone_upscale_fit", None)
            return result

        orig_images_link = resolved_upstream

    result["colabone_upscale_loader"] = {
        "class_type": "UpscaleModelLoader",
        "_meta": {"title": "AI 超分模型加载"},
        "inputs": {"model_name": model_name},
    }
    result["colabone_upscale_apply"] = {
        "class_type": "ImageUpscaleWithModel",
        "_meta": {"title": "AI 2x 细节超分"},
        "inputs": {
            "upscale_model": ["colabone_upscale_loader", 0],
            "image": orig_images_link,
        },
    }
    result.pop("colabone_upscale_fit", None)
    combine_node["inputs"]["images"] = ["colabone_upscale_apply", 0]
    return result


def build_concat_filter_script(
    segment_exact_frames: list[int],
    *,
    out_w: int,
    out_h: int,
    needs_resize_or_crop: bool,
    crop_only: bool = False,
) -> str:
    """Build an FFmpeg -filter_complex_script for frame-exact decoded concatenation.

    - Segment 0 trims decoded frames [0, exact_frames).
    - Segment i > 0 trims decoded frames [1, exact_frames + 1), dropping frame 0
      which overlaps the previous segment's relay tail frame.
    - Concatenates decoded streams and applies final scale/crop/setsar=1 once.
    """
    n_segs = len(segment_exact_frames)
    if n_segs <= 0:
        raise ValueError("没有可拼接的视频分段")

    chains: list[str] = []
    for idx, seg_frames in enumerate(segment_exact_frames):
        count = max(1, int(seg_frames))
        if idx == 0:
            start_f = 0
            end_f = count
        else:
            start_f = 1
            end_f = count + 1
        chains.append(
            f"[{idx}:v]trim=start_frame={start_f}:end_frame={end_f},setpts=PTS-STARTPTS[v{idx}]"
        )

    if n_segs > 1:
        inputs_spec = "".join(f"[v{i}]" for i in range(n_segs))
        chains.append(f"{inputs_spec}concat=n={n_segs}:v=1:a=0[vcat]")
        tail_in = "[vcat]"
    else:
        tail_in = "[v0]"

    if needs_resize_or_crop:
        if crop_only:
            post_filter = f"crop={int(out_w)}:{int(out_h)},setsar=1"
        else:
            post_filter = (
                f"scale={int(out_w)}:{int(out_h)}:"
                f"force_original_aspect_ratio=increase:flags=lanczos,"
                f"crop={int(out_w)}:{int(out_h)},setsar=1"
            )
        chains.append(f"{tail_in}{post_filter}[vout]")
    else:
        chains.append(f"{tail_in}setsar=1[vout]")

    return ";\n".join(chains) + "\n"


def _is_readable_pcm_wav(path: Path) -> bool:
    try:
        with wave.open(str(path), "rb") as wf:
            return (
                wf.getcomptype() == "NONE"
                and wf.getsampwidth() in (1, 2, 3, 4)
                and wf.getframerate() > 0
                and wf.getnframes() > 0
            )
    except (OSError, wave.Error, EOFError):
        return False


def _wav_duration_seconds(wav_path: Path) -> float:
    with wave.open(str(wav_path), "rb") as wf:
        rate = wf.getframerate()
        frames = wf.getnframes()
        return (frames / rate) if rate > 0 else 0.0


def slice_wav_segment(
    src_wav: Path, dst_wav: Path, start_sec: float, end_sec: float
) -> Path:
    """Losslessly slice a PCM WAV segment [start_sec, end_sec] using stdlib wave."""
    src = Path(src_wav)
    dst = Path(dst_wav)
    dst.parent.mkdir(parents=True, exist_ok=True)

    with wave.open(str(src), "rb") as reader:
        nchannels = reader.getnchannels()
        sampwidth = reader.getsampwidth()
        framerate = reader.getframerate()
        nframes = reader.getnframes()
        if framerate <= 0 or nframes <= 0:
            raise ValueError("音频文件为空，无法切片")

        start_frame = max(0, min(nframes, int(round(max(0.0, start_sec) * framerate))))
        end_frame = max(
            start_frame, min(nframes, int(round(max(0.0, end_sec) * framerate)))
        )
        if end_frame <= start_frame:
            start_frame = max(0, min(start_frame, nframes - 1))
            end_frame = min(nframes, start_frame + 1)

        reader.setpos(start_frame)
        remaining = end_frame - start_frame
        with wave.open(str(dst), "wb") as writer:
            writer.setnchannels(nchannels)
            writer.setsampwidth(sampwidth)
            writer.setframerate(framerate)
            chunk_frames = 65536
            while remaining > 0:
                to_read = min(chunk_frames, remaining)
                data = reader.readframes(to_read)
                if not data:
                    break
                writer.writeframes(data)
                remaining -= to_read
    return dst


def _parse_float_override(raw: Any, default: float, label: str) -> float:
    """Safely parse a numeric override, rejecting booleans, non-finite, and malformed values."""
    if raw is None:
        return float(default)
    if isinstance(raw, bool):
        raise ValueError(f"{label} 参数格式无效：不能为布尔值")
    if isinstance(raw, str):
        text = raw.strip()
        if not text:
            return float(default)
        try:
            val = float(text)
        except ValueError as exc:
            raise ValueError(f"{label} 参数格式无效：{raw!r}") from exc
    elif isinstance(raw, (int, float)):
        val = float(raw)
    else:
        raise ValueError(f"{label} 参数格式无效：{raw!r}")
    if not math.isfinite(val):
        raise ValueError(f"{label} 必须为有限数值")
    return val


def _update_task(
    task: Any,
    *,
    progress: float | None = None,
    message: str | None = None,
    append_output: dict[str, Any] | None = None,
) -> None:
    """Update task progress/message/outputs atomically under task.lock when available."""
    task_lock = getattr(task, "lock", None)
    with task_lock if task_lock is not None else nullcontext():
        if progress is not None:
            task.progress = progress
        if message is not None:
            task.message = message
        if (
            append_output is not None
            and hasattr(task, "outputs")
            and isinstance(task.outputs, list)
        ):
            task.outputs.append(append_output)


def _prune_stale_work_dirs(
    work_parent: Path,
    max_age_seconds: float = _STALE_WORK_DIR_MAX_AGE_SECONDS,
) -> None:
    """Best-effort cleanup of stale comfyui-work/dh-* directories older than max_age_seconds."""
    try:
        if work_parent.is_symlink() or not work_parent.is_dir():
            return
        cutoff = time.time() - max(0.0, float(max_age_seconds))
        for entry in work_parent.iterdir():
            try:
                if (
                    entry.name.startswith("dh-")
                    and not entry.is_symlink()
                    and entry.is_dir()
                    and entry.stat().st_mtime < cutoff
                ):
                    shutil.rmtree(entry, ignore_errors=True)
            except OSError:
                continue
    except OSError:
        return


def _resolve_media_node_key(
    configured_key: str,
    fallback_key: str,
    inputs_by_key: dict[str, tuple[str, Path]],
    workflow: dict[str, Any] | None,
    expected_class_type: str,
    expected_input: str,
) -> str:
    if configured_key:
        return configured_key
    if fallback_key in inputs_by_key:
        return fallback_key
    if isinstance(workflow, dict):
        node_id, inp_name = fallback_key.split(".", 1)
        node = workflow.get(node_id)
        if (
            isinstance(node, dict)
            and isinstance(node.get("inputs"), dict)
            and inp_name in node["inputs"]
        ):
            return fallback_key
        for nid, node_obj in workflow.items():
            if (
                isinstance(node_obj, dict)
                and node_obj.get("class_type") == expected_class_type
                and isinstance(node_obj.get("inputs"), dict)
                and expected_input in node_obj["inputs"]
            ):
                return f"{nid}.{expected_input}"
    return ""


def _response_ok(resp: Any) -> bool:
    code = getattr(resp, "status_code", None)
    if isinstance(code, int):
        return 200 <= code < 300
    return bool(getattr(resp, "ok", True))


def _terminate_process_group(proc: subprocess.Popen, timeout: float = 5.0) -> None:
    """Terminate proc and always kill any lingering process-group members, even if leader exited."""
    pid = getattr(proc, "pid", None)
    has_killpg = isinstance(pid, int) and pid > 0 and hasattr(os, "killpg")
    leader_alive = proc.poll() is None

    used_pg = False
    if has_killpg:
        try:
            os.killpg(pid, signal.SIGTERM)
            used_pg = True
        except (ProcessLookupError, PermissionError, OSError):
            pass

    if leader_alive and not used_pg:
        try:
            proc.terminate()
        except OSError:
            pass

    if leader_alive:
        try:
            proc.wait(timeout=timeout)
            leader_alive = False
        except subprocess.TimeoutExpired:
            pass

    if has_killpg:
        if used_pg and not leader_alive:
            try:
                os.killpg(pid, 0)
                time.sleep(0.05)
            except (ProcessLookupError, PermissionError, OSError):
                return
        try:
            os.killpg(pid, signal.SIGKILL)
        except (ProcessLookupError, PermissionError, OSError):
            pass
    elif leader_alive:
        try:
            proc.kill()
        except OSError:
            pass

    if proc.poll() is None:
        try:
            proc.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            pass


def _run_ffmpeg_command(
    args: list[str],
    task,
    log: Path,
    timeout: int = 1200,
    cwd: Path | str | None = None,
    stopping: threading.Event | None = None,
) -> None:
    """Cancelable process-group subprocess runner for ffmpeg commands."""
    log.parent.mkdir(parents=True, exist_ok=True)
    with log.open("wb") as out:
        proc = subprocess.Popen(
            args,
            cwd=cwd,
            env=_sanitized_subprocess_env(),
            stdout=out,
            stderr=subprocess.STDOUT,
            stdin=subprocess.DEVNULL,
            start_new_session=True,
        )

    task_lock = getattr(task, "lock", None)
    has_cancel_hook = hasattr(task, "cancel_hook")
    prev_hook = None
    cancel_hook = None
    if has_cancel_hook:
        with task_lock if task_lock is not None else nullcontext():
            prev_hook = task.cancel_hook

            def _cancel_ffmpeg() -> None:
                _terminate_process_group(proc, timeout=2.0)
                if callable(prev_hook):
                    try:
                        prev_hook()
                    except Exception:
                        pass

            cancel_hook = _cancel_ffmpeg
            task.cancel_hook = cancel_hook

    deadline = time.monotonic() + timeout
    try:
        while proc.poll() is None:
            task.check()
            if isinstance(stopping, threading.Event) and stopping.is_set():
                from server.runtime import ColabOneCancelledError

                raise ColabOneCancelledError("实例正在停止，已取消数字人音视频处理")
            if time.monotonic() > deadline:
                raise RuntimeError("音视频处理超时，请查看运行日志")
            time.sleep(0.2)
        if proc.returncode:
            task.check()
            tail = ""
            if log.is_file():
                tail = log.read_text(encoding="utf-8", errors="replace")[-600:]
            raise RuntimeError(f"音视频处理失败（退出码 {proc.returncode}）：{tail}")
    finally:
        if has_cancel_hook:
            with task_lock if task_lock is not None else nullcontext():
                if task.cancel_hook is cancel_hook:
                    task.cancel_hook = prev_hook
        _terminate_process_group(proc)


def _node_default_value(workflow: dict[str, Any], field_key: str) -> Any:
    if not field_key or "." not in field_key:
        return None
    node_id, input_name = field_key.split(".", 1)
    node = workflow.get(node_id)
    if not isinstance(node, dict):
        return None
    inputs = node.get("inputs")
    if not isinstance(inputs, dict):
        return None
    return inputs.get(input_name)


def _find_upscale_model_name(
    wf: dict[str, Any], models_dir: Path | None = None
) -> str:
    models = wf.get("models")
    if isinstance(models, (list, tuple)):
        for model in models:
            if not isinstance(model, dict):
                continue
            if model.get("target_dir") == "upscale_models":
                raw_path = model.get("path") or model.get("filename") or ""
                if raw_path:
                    return PurePosixPath(str(raw_path)).name
    if models_dir is not None:
        upscale_dir = Path(models_dir) / "upscale_models"
        if upscale_dir.is_dir() and not upscale_dir.is_symlink():
            default_path = upscale_dir / DEFAULT_AI_UPSCALE_MODEL
            if default_path.is_file() and not default_path.is_symlink():
                return DEFAULT_AI_UPSCALE_MODEL
            try:
                for entry in sorted(upscale_dir.iterdir(), key=lambda p: p.name):
                    if (
                        entry.is_file()
                        and not entry.is_symlink()
                        and not entry.name.startswith(".")
                        and entry.suffix.lower() in _UPSCALE_MODEL_EXTENSIONS
                    ):
                        return entry.name
            except OSError:
                pass
    return ""


def _parse_single_prompt_result(
    result: Any,
) -> tuple[str, list[Any]]:
    """Normalize return shapes from ColabOneComfyManager._execute_single_prompt."""
    if result is None:
        return ("", [])
    if isinstance(result, (bytes, bytearray, Path)):
        return ("", [result])
    if isinstance(result, tuple):
        prompt_id = ""
        items: list[Any] = []
        for elem in result:
            if isinstance(elem, str) and not prompt_id:
                prompt_id = elem
            elif isinstance(elem, list) and not items:
                items = elem
            elif isinstance(elem, dict) and not items:
                items = [elem]
        return (prompt_id, items)
    if isinstance(result, dict):
        prompt_id = str(result.get("prompt_id") or "")
        for key in ("outputs", "items", "files"):
            val = result.get(key)
            if isinstance(val, list):
                return (prompt_id, val)
        if any(k in result for k in ("filename", "raw", "bytes", "content", "data", "path")):
            return (prompt_id, [result])
        return (prompt_id, [])
    if isinstance(result, list):
        prompt_id = ""
        for elem in result:
            if isinstance(elem, dict) and elem.get("prompt_id"):
                prompt_id = str(elem["prompt_id"])
                break
        return (prompt_id, result)
    return ("", [])


def _pick_video_output_item(items: list[Any]) -> Any:
    """Pick the highest-priority valid video output item; reject non-video items."""
    if not items:
        return None

    best_item: Any = None
    best_prio = 999

    for item in items:
        prio: int | None = None
        if isinstance(item, dict):
            fname = str(item.get("filename") or "").strip().lower()
            ext = PurePosixPath(fname).suffix if fname else ""
            if ext == ".mp4":
                prio = 0
            elif ext in _VIDEO_EXTENSIONS:
                prio = 1
            elif ext in _NON_VIDEO_EXTENSIONS:
                prio = None
            elif (
                item.get("kind") == "video"
                or item.get("media_key") in ("videos", "gifs")
                or str(item.get("mime") or "").lower().startswith("video/")
            ):
                prio = 2
        elif isinstance(item, Path):
            ext = item.suffix.lower()
            if ext == ".mp4":
                prio = 0
            elif ext in _VIDEO_EXTENSIONS:
                prio = 1
        elif isinstance(item, (bytes, bytearray)):
            raw = bytes(item)
            if raw and not raw.startswith(
                (b"\x89PNG\r\n\x1a\n", b"\xff\xd8\xff", b"RIFF")
            ):
                prio = 3

        if prio is not None and prio < best_prio:
            best_prio = prio
            best_item = item

    return best_item


def _read_wav_mono_np(path: Path, np_mod: Any) -> tuple[Any, int]:
    """Read a WAV file directly into a 1-D NumPy float64 ndarray without Python float lists."""
    wav_path = Path(path)
    if not wav_path.is_file():
        raise FileNotFoundError(f"WAV 文件不存在：{wav_path}")

    try:
        with wave.open(str(wav_path), "rb") as wf:
            nchannels = wf.getnchannels()
            sampwidth = wf.getsampwidth()
            sample_rate = wf.getframerate()
            nframes = wf.getnframes()
            raw = wf.readframes(nframes)
    except wave.Error:
        from server.tts_engines import read_wav_mono_float

        samples, sr = read_wav_mono_float(wav_path)
        return np_mod.asarray(samples, dtype=np_mod.float64).reshape(-1), int(sr)

    if sample_rate <= 0:
        raise ValueError(f"WAV 采样率无效：{sample_rate}")
    if nframes == 0 or not raw or nchannels <= 0:
        return np_mod.empty(0, dtype=np_mod.float64), int(sample_rate)
    if sampwidth not in (1, 2, 3, 4):
        raise ValueError(f"不支持的 PCM 位宽：{sampwidth} 字节")

    frame_bytes = sampwidth * nchannels
    usable = len(raw) - (len(raw) % frame_bytes)
    if usable <= 0:
        return np_mod.empty(0, dtype=np_mod.float64), int(sample_rate)
    payload = memoryview(raw)[:usable]

    if sampwidth == 1:
        arr = (
            np_mod.frombuffer(payload, dtype=np_mod.uint8).astype(np_mod.float64) - 128.0
        ) / 128.0
    elif sampwidth == 2:
        arr = np_mod.frombuffer(payload, dtype="<i2").astype(np_mod.float64) / 32768.0
    elif sampwidth == 3:
        b = np_mod.frombuffer(payload, dtype=np_mod.uint8).reshape(-1, 3)
        vals = (
            b[:, 0].astype(np_mod.int32)
            | (b[:, 1].astype(np_mod.int32) << 8)
            | (b[:, 2].astype(np_mod.int32) << 16)
        )
        vals = np_mod.where(vals >= 0x800000, vals - 0x1000000, vals)
        arr = vals.astype(np_mod.float64) / 8388608.0
    else:
        arr = (
            np_mod.frombuffer(payload, dtype="<i4").astype(np_mod.float64) / 2147483648.0
        )

    if nchannels > 1:
        arr = arr.reshape(-1, nchannels).mean(axis=1)
    return arr.reshape(-1), int(sample_rate)


class ColabOneDigitalHumanRunner:
    """End-to-end long-video digital human pipeline runner for remote ComfyUI."""

    def __init__(
        self,
        manager,
        tts_runner: ColabOneRemoteTtsRunner | None,
        run_cmd_fn: Callable[..., Any] | None = None,
    ):
        self.manager = manager
        self.tts_runner = tts_runner
        self._run_cmd_fn = run_cmd_fn
        runtime_root = getattr(getattr(self.manager, "runtime", None), "root", None)
        if isinstance(runtime_root, (str, os.PathLike)):
            raw_root = os.fspath(runtime_root)
            if not any(ch in raw_root for ch in ("\r", "\n", "\x00")):
                _prune_stale_work_dirs(Path(raw_root) / "comfyui-work")

    def _ffmpeg_binary(self) -> str:
        found = shutil.which("ffmpeg")
        if found:
            return found
        if self._run_cmd_fn is not None:
            return "ffmpeg"
        raise RuntimeError("远端缺少 ffmpeg，无法执行数字人音视频处理")

    def _invoke_run_cmd(
        self,
        args: list[str],
        task,
        log: Path,
        timeout: int = 1200,
        cwd: Path | str | None = None,
    ) -> None:
        stopping = getattr(getattr(self.manager, "runtime", None), "stopping", None)
        if self._run_cmd_fn is None:
            _run_ffmpeg_command(
                args, task, log, timeout=timeout, cwd=cwd, stopping=stopping
            )
            return

        fn = self._run_cmd_fn
        try:
            sig = inspect.signature(fn)
            params = list(sig.parameters.values())
            has_var_pos = any(p.kind == inspect.Parameter.VAR_POSITIONAL for p in params)
            has_var_kw = any(p.kind == inspect.Parameter.VAR_KEYWORD for p in params)
            pos_params = [
                p
                for p in params
                if p.kind
                in (
                    inspect.Parameter.POSITIONAL_ONLY,
                    inspect.Parameter.POSITIONAL_OR_KEYWORD,
                )
            ]
            named_params = {p.name for p in params}
        except (TypeError, ValueError):
            has_var_pos, has_var_kw, pos_params, named_params = True, True, [], set()

        call_args: list[Any]
        if has_var_pos or len(pos_params) >= 3:
            call_args = [args, task, log]
        elif len(pos_params) == 2:
            call_args = [args, task]
        else:
            call_args = [args]

        kwargs: dict[str, Any] = {}
        optional_kw = {
            "timeout": timeout,
            "cwd": cwd,
            "stopping": stopping,
        }
        if len(call_args) < 2 and "task" in named_params:
            kwargs["task"] = task
        if len(call_args) < 3 and "log" in named_params:
            kwargs["log"] = log
        for k, v in optional_kw.items():
            if has_var_kw or k in named_params:
                kwargs[k] = v

        res = fn(*call_args, **kwargs)
        rc = getattr(res, "returncode", 0)
        if isinstance(rc, int) and rc != 0:
            raise RuntimeError(f"音视频处理命令失败（退出码 {rc}）")

    def _upload_to_comfy(
        self, task, filename: str, file_path: Path, is_image: bool
    ) -> str:
        task.check()
        upload_helper = getattr(self.manager, "_upload_input_file", None)
        if callable(upload_helper):
            return str(upload_helper(filename, file_path))
        raw_bytes = file_path.read_bytes()
        http = getattr(getattr(self.manager, "runtime", None), "http", None)
        base_url = getattr(self.manager, "base_url", "")
        if http is not None and base_url:
            try:
                resp = http.post(
                    base_url + "/upload/image",
                    files={"image": (filename, raw_bytes)},
                    data={"overwrite": "true"},
                    timeout=60,
                )
                if _response_ok(resp):
                    body = resp.json() or {}
                    subfolder = str(body.get("subfolder") or "").strip("/")
                    name = str(body.get("name") or filename)
                    return f"{subfolder}/{name}" if subfolder else name
                if not is_image and callable(
                    getattr(self.manager, "_stage_input_file", None)
                ):
                    return self.manager._stage_input_file(filename, raw_bytes)
                resp.raise_for_status()
            except requests.RequestException:
                if not is_image and callable(
                    getattr(self.manager, "_stage_input_file", None)
                ):
                    return self.manager._stage_input_file(filename, raw_bytes)
                raise
        if callable(getattr(self.manager, "_stage_input_file", None)):
            return self.manager._stage_input_file(filename, raw_bytes)
        return filename

    def _download_output_item_bytes(self, task, item: Any) -> bytes:
        if isinstance(item, (bytes, bytearray)):
            return bytes(item)
        if isinstance(item, Path):
            if item.is_symlink() or not item.is_file():
                raise RuntimeError("分段输出文件路径无效或为符号链接")
            if item.stat().st_size > MAX_OUTPUT_BYTES:
                raise ValueError("分段生成结果超过最大体积限制")
            return item.read_bytes()
        if (
            isinstance(item, tuple)
            and len(item) == 2
            and isinstance(item[1], (bytes, bytearray))
        ):
            return bytes(item[1])
        if not isinstance(item, dict):
            raise RuntimeError("ComfyUI 返回了无法识别的分段视频结果格式")

        for key in ("raw", "bytes", "content", "data"):
            val = item.get(key)
            if isinstance(val, (bytes, bytearray)) and val:
                return bytes(val)

        if item.get("path"):
            p = Path(item["path"])
            if p.is_symlink():
                raise RuntimeError("分段输出文件不能是符号链接")
            if p.is_file():
                io_out_dir_check = getattr(self.manager, "io_out_dir", None)
                if isinstance(io_out_dir_check, (str, os.PathLike)):
                    base_out_raw = Path(io_out_dir_check)
                    try:
                        rel_out = p.relative_to(base_out_raw)
                    except ValueError:
                        rel_out = None
                    if rel_out is not None:
                        safe_p = _safe_contained_file(base_out_raw, *rel_out.parts)
                        if safe_p is None:
                            raise RuntimeError("分段输出文件路径安全校验失败")
                        p = safe_p
                if p.stat().st_size > MAX_OUTPUT_BYTES:
                    raise ValueError("分段生成结果超过最大体积限制")
                return p.read_bytes()

        fname = str(item.get("filename") or "")
        if not fname:
            raise RuntimeError("ComfyUI 分段输出缺少文件名")
        subfolder = str(item.get("subfolder") or "")
        file_type = str(item.get("type") or "output")

        http = getattr(getattr(self.manager, "runtime", None), "http", None)
        base_url = getattr(self.manager, "base_url", "")
        if http is not None and base_url:
            view_resp = http.get(
                base_url + "/view",
                params={
                    "filename": fname,
                    "subfolder": subfolder,
                    "type": file_type,
                },
                stream=True,
                timeout=(10, 120),
            )
            try:
                view_resp.raise_for_status()
                if isinstance(view_resp, requests.Response):
                    buf = bytearray()
                    for chunk in view_resp.iter_content(256 * 1024):
                        task.check()
                        if not chunk:
                            continue
                        buf.extend(chunk)
                        if len(buf) > MAX_OUTPUT_BYTES:
                            raise ValueError("分段生成结果超过最大体积限制")
                    return bytes(buf)
                return bytes(view_resp.content)
            finally:
                close_fn = getattr(view_resp, "close", None)
                if callable(close_fn):
                    close_fn()

        io_out_dir = getattr(self.manager, "io_out_dir", None)
        if isinstance(io_out_dir, (str, os.PathLike)):
            safe_candidate = _safe_contained_file(Path(io_out_dir), subfolder, fname)
            if safe_candidate is not None:
                if safe_candidate.stat().st_size > MAX_OUTPUT_BYTES:
                    raise ValueError("分段生成结果超过最大体积限制")
                return safe_candidate.read_bytes()

        raise RuntimeError(f"无法从远端 ComfyUI 获取分段输出文件：{fname}")

    def _materialize_output_item_to_path(
        self,
        task,
        item: Any,
        dst_path: Path,
        *,
        work_dir: Path,
        segment_label: str,
    ) -> Path:
        """Stream, move, or copy a segment video output item to dst_path without loading into RAM."""
        dst = Path(dst_path)
        if dst.is_symlink():
            raise ValueError("分段视频缓存文件不能是符号链接")
        dst.parent.mkdir(parents=True, exist_ok=True)
        work_dir_resolved = Path(work_dir).resolve()

        def _validate_video_file(file_path: Path) -> None:
            if file_path.is_symlink() or not file_path.is_file():
                raise RuntimeError(f"{segment_label} 视频文件无效或为符号链接")
            size = file_path.stat().st_size
            if size <= 0:
                raise RuntimeError(f"{segment_label} 下载的视频文件为空")
            if size > MAX_OUTPUT_BYTES:
                raise ValueError("分段生成结果超过最大体积限制")
            with file_path.open("rb") as fh:
                header = fh.read(8)
            if header.startswith(b"\x89PNG\r\n\x1a\n"):
                raise RuntimeError(
                    f"{segment_label} 返回了图片而非视频文件，请检查视频输出节点格式"
                )

        def _transfer_from_file(src_path: Path) -> Path:
            _validate_video_file(src_path)
            src_resolved = src_path.resolve()
            dst_resolved = dst.resolve()
            if src_resolved == dst_resolved:
                return dst
            if work_dir_resolved in src_resolved.parents:
                os.replace(src_path, dst)
            else:
                shutil.copyfile(src_path, dst)
            return dst

        if isinstance(item, Path):
            if item.is_symlink() or not item.is_file():
                raise RuntimeError(f"{segment_label} 返回的分段视频路径无效或为符号链接")
            return _transfer_from_file(item)

        if isinstance(item, dict) and item.get("path"):
            raw_p = Path(item["path"])
            if raw_p.is_symlink():
                raise RuntimeError(f"{segment_label} 返回的分段视频路径不能是符号链接")
            if raw_p.is_file():
                try:
                    rel_work = raw_p.relative_to(work_dir)
                except ValueError:
                    rel_work = None
                if rel_work is not None:
                    safe_p = _safe_contained_file(work_dir, *rel_work.parts)
                    if safe_p is None:
                        raise RuntimeError(f"{segment_label} 分段视频路径安全校验失败")
                    return _transfer_from_file(safe_p)
                io_out_dir_check = getattr(self.manager, "io_out_dir", None)
                if isinstance(io_out_dir_check, (str, os.PathLike)):
                    base_out_raw = Path(io_out_dir_check)
                    try:
                        rel_out = raw_p.relative_to(base_out_raw)
                    except ValueError:
                        rel_out = None
                    if rel_out is not None:
                        safe_out_p = _safe_contained_file(base_out_raw, *rel_out.parts)
                        if safe_out_p is None:
                            raise RuntimeError(f"{segment_label} 分段视频路径安全校验失败")
                        return _transfer_from_file(safe_out_p)
                return _transfer_from_file(raw_p)

        if isinstance(item, dict) and not any(
            isinstance(item.get(k), (bytes, bytearray)) and item.get(k)
            for k in ("raw", "bytes", "content", "data")
        ):
            fname = str(item.get("filename") or "")
            subfolder = str(item.get("subfolder") or "")
            file_type = str(item.get("type") or "output")
            if fname:
                http = getattr(getattr(self.manager, "runtime", None), "http", None)
                base_url = getattr(self.manager, "base_url", "")
                if http is not None and base_url:
                    view_resp = http.get(
                        base_url + "/view",
                        params={
                            "filename": fname,
                            "subfolder": subfolder,
                            "type": file_type,
                        },
                        stream=True,
                        timeout=(10, 120),
                    )
                    total = 0
                    try:
                        view_resp.raise_for_status()
                        with dst.open("wb") as out:
                            if isinstance(view_resp, requests.Response):
                                for chunk in view_resp.iter_content(256 * 1024):
                                    task.check()
                                    if not chunk:
                                        continue
                                    total += len(chunk)
                                    if total > MAX_OUTPUT_BYTES:
                                        raise ValueError("分段生成结果超过最大体积限制")
                                    out.write(chunk)
                            else:
                                raw_bytes = bytes(view_resp.content)
                                if len(raw_bytes) > MAX_OUTPUT_BYTES:
                                    raise ValueError("分段生成结果超过最大体积限制")
                                out.write(raw_bytes)
                        _validate_video_file(dst)
                        return dst
                    except BaseException:
                        dst.unlink(missing_ok=True)
                        raise
                    finally:
                        close_fn = getattr(view_resp, "close", None)
                        if callable(close_fn):
                            close_fn()

                io_out_dir = getattr(self.manager, "io_out_dir", None)
                if isinstance(io_out_dir, (str, os.PathLike)):
                    safe_candidate = _safe_contained_file(
                        Path(io_out_dir), subfolder, fname
                    )
                    if safe_candidate is not None:
                        return _transfer_from_file(safe_candidate)

        seg_bytes = self._download_output_item_bytes(task, item)
        if not seg_bytes:
            raise RuntimeError(f"{segment_label} 下载的视频文件为空")
        if len(seg_bytes) > MAX_OUTPUT_BYTES:
            raise ValueError("分段生成结果超过最大体积限制")
        if seg_bytes.startswith(b"\x89PNG\r\n\x1a\n"):
            raise RuntimeError(
                f"{segment_label} 返回了图片而非视频文件，请检查视频输出节点格式"
            )
        dst.write_bytes(seg_bytes)
        return dst

    def _cleanup_intermediate_io_files(
        self,
        task_tag: str,
        io_in_names: set[str],
        io_out_entries: set[tuple[str, str]],
    ) -> None:
        safe_tag = _sanitize_task_tag(task_tag, allow_empty=True)
        glob_pattern = f"{glob.escape(f'dh_{safe_tag}_seg')}*" if safe_tag else ""

        io_in_dir = getattr(self.manager, "io_in_dir", None)
        if isinstance(io_in_dir, (str, os.PathLike)):
            try:
                base_in_raw = Path(io_in_dir)
                if not base_in_raw.is_symlink() and base_in_raw.is_dir():
                    for rel_name in io_in_names:
                        if not rel_name:
                            continue
                        safe_target = _safe_contained_file(base_in_raw, rel_name)
                        if safe_target is not None:
                            try:
                                safe_target.unlink(missing_ok=True)
                            except OSError:
                                continue
                    if glob_pattern:
                        for matched in base_in_raw.glob(glob_pattern):
                            try:
                                if matched.is_symlink() or not matched.is_file():
                                    continue
                                rel_parts = matched.relative_to(base_in_raw).parts
                                safe_target = _safe_contained_file(
                                    base_in_raw, *rel_parts
                                )
                                if safe_target is not None:
                                    safe_target.unlink(missing_ok=True)
                            except (OSError, ValueError):
                                continue
            except OSError:
                pass

        io_out_dir = getattr(self.manager, "io_out_dir", None)
        if isinstance(io_out_dir, (str, os.PathLike)):
            try:
                base_out_raw = Path(io_out_dir)
                if not base_out_raw.is_symlink() and base_out_raw.is_dir():
                    for subfolder, fname in io_out_entries:
                        if not fname:
                            continue
                        safe_target = _safe_contained_file(
                            base_out_raw, subfolder, fname
                        )
                        if safe_target is not None:
                            try:
                                safe_target.unlink(missing_ok=True)
                            except OSError:
                                continue
                    if glob_pattern:
                        for matched in base_out_raw.glob(glob_pattern):
                            try:
                                if matched.is_symlink() or not matched.is_file():
                                    continue
                                rel_parts = matched.relative_to(base_out_raw).parts
                                safe_target = _safe_contained_file(
                                    base_out_raw, *rel_parts
                                )
                                if safe_target is not None:
                                    safe_target.unlink(missing_ok=True)
                            except (OSError, ValueError):
                                continue
            except OSError:
                pass

    def _prepare_uploaded_audio(
        self, task, audio_src_path: Path, work_dir: Path
    ) -> tuple[Path, float, list[float]]:
        """Uniformly decode/resample uploaded audio to 48 kHz mono PCM16 WAV and normalize to -23 LUFS."""
        from server.tts_engines import (
            _normalize_audio_loudness_np,
            _resample_audio_np,
            normalize_audio_loudness,
            read_wav_mono_float,
            resample_audio_linear,
            write_wav_pcm16,
        )

        audio_src_path = _validate_safe_path(audio_src_path, "输入音频路径")
        work_dir = _validate_safe_path(work_dir, "工作目录")
        work_dir.mkdir(parents=True, exist_ok=True)
        master_wav = work_dir / "master_audio.wav"
        needs_ffmpeg = True
        if _is_readable_pcm_wav(audio_src_path):
            with wave.open(str(audio_src_path), "rb") as wf:
                if (
                    wf.getnchannels() == 1
                    and wf.getframerate() == 48000
                    and wf.getsampwidth() == 2
                ):
                    needs_ffmpeg = False

        source_for_norm = audio_src_path
        if needs_ffmpeg:
            ffmpeg_bin = self._ffmpeg_binary()
            decoded_wav = work_dir / "uploaded_decoded.wav"
            self._invoke_run_cmd(
                [
                    ffmpeg_bin,
                    "-y",
                    "-v",
                    "error",
                    "-protocol_whitelist",
                    "file,pipe",
                    "-i",
                    str(audio_src_path),
                    "-ac",
                    "1",
                    "-ar",
                    "48000",
                    "-c:a",
                    "pcm_s16le",
                    str(decoded_wav),
                ],
                task,
                work_dir / "ffmpeg_audio_decode.log",
                timeout=300,
            )
            if decoded_wav.is_file() and decoded_wav.stat().st_size > 0:
                source_for_norm = decoded_wav
            elif self._run_cmd_fn is not None and _is_readable_pcm_wav(audio_src_path):
                # Offline test spy recorded ffmpeg decode without writing decoded_wav.
                source_for_norm = audio_src_path
            else:
                raise RuntimeError("音频格式转换失败：ffmpeg 未产出 WAV 文件")

        try:
            import numpy as np  # type: ignore[import-not-found]
        except ImportError:
            np = None

        silence_floor_rms = 10.0 ** (-70.0 / 20.0)
        if np is not None:
            samples_np, src_sr = _read_wav_mono_np(source_for_norm, np)
            if samples_np.size == 0 or src_sr <= 0:
                raise ValueError("输入音频文件为空")
            if src_sr != 48000:
                samples_np = _resample_audio_np(samples_np, src_sr, 48000)
            rms = float(np.sqrt(np.dot(samples_np, samples_np) / samples_np.size))
            if rms > silence_floor_rms:
                normalized_np = _normalize_audio_loudness_np(
                    samples_np, 48000, target_lufs=-23.0
                )
            else:
                normalized_np = samples_np
            write_wav_pcm16(master_wav, normalized_np, 48000)
            duration = int(normalized_np.size) / 48000.0
        else:
            samples, src_sr = read_wav_mono_float(source_for_norm)
            if not samples or src_sr <= 0:
                raise ValueError("输入音频文件为空")
            if src_sr != 48000:
                samples = resample_audio_linear(samples, src_sr, 48000)
            # Normalize loudness consistently via tts_engines.normalize_audio_loudness (-23 LUFS),
            # skipping silence/noise-floor tracks (<= -70 dBFS RMS) so silence is never boosted.
            rms = math.sqrt(sum(v * v for v in samples) / len(samples))
            if rms > silence_floor_rms:
                normalized = normalize_audio_loudness(samples, 48000, target_lufs=-23.0)
            else:
                normalized = samples
            write_wav_pcm16(master_wav, normalized, 48000)
            duration = len(normalized) / 48000.0

        valleys = (
            detect_pcm_silence_valleys(master_wav)
            if _is_readable_pcm_wav(master_wav)
            else []
        )
        return (master_wav, duration, valleys)

    def _extract_tail_frame(
        self,
        task,
        prev_seg_mp4: Path,
        tail_frame_path: Path,
        tail_offset: float,
        base_w: int,
        base_h: int,
        fallback_anchor: Path,
        work_dir: Path,
        seg_idx: int,
        exact_frames: int | None = None,
        output_fps: int = 25,
    ) -> Path:
        from server.runtime import ColabOneCancelledError

        prev_seg_mp4 = _validate_safe_path(prev_seg_mp4, "分段视频路径")
        tail_frame_path = _validate_safe_path(tail_frame_path, "尾帧输出路径")
        ffmpeg_bin = self._ffmpeg_binary()
        vf = (
            f"scale={base_w}:{base_h}:"
            f"force_original_aspect_ratio=increase:flags=lanczos,"
            f"crop={base_w}:{base_h},setsar=1"
        )
        log_path = work_dir / f"ffmpeg_tail_{seg_idx:03d}.log"

        attempts: list[list[str]] = []
        if exact_frames is not None and exact_frames > 0 and output_fps > 0:
            half_frame = 0.5 / float(output_fps)
            cut_ts = max(0.0, (exact_frames - 1) / float(output_fps) - half_frame)
            attempts.append(
                [
                    ffmpeg_bin,
                    "-y",
                    "-protocol_whitelist",
                    "file,pipe",
                    "-ss",
                    f"{cut_ts:.6f}",
                    "-i",
                    str(prev_seg_mp4),
                    "-frames:v",
                    "1",
                    "-vf",
                    vf,
                    "-update",
                    "1",
                    str(tail_frame_path),
                ]
            )

        offset_str = f"-{max(0.01, float(tail_offset)):.3f}"
        attempts.append(
            [
                ffmpeg_bin,
                "-y",
                "-protocol_whitelist",
                "file,pipe",
                "-sseof",
                offset_str,
                "-i",
                str(prev_seg_mp4),
                "-frames:v",
                "1",
                "-vf",
                vf,
                "-update",
                "1",
                str(tail_frame_path),
            ]
        )

        # Final retry: decode through the end of a very short clip and keep the TRUE
        # last frame via -update 1 (never -frames:v 1, which would extract frame 0).
        retry_cmd = [
            ffmpeg_bin,
            "-y",
            "-protocol_whitelist",
            "file,pipe",
            "-i",
            str(prev_seg_mp4),
            "-vf",
            vf,
            "-update",
            "1",
            str(tail_frame_path),
        ]

        last_err: Exception | None = None
        for cmd in attempts:
            task.check()
            tail_frame_path.unlink(missing_ok=True)
            try:
                self._invoke_run_cmd(cmd, task, log_path, timeout=120)
            except ColabOneCancelledError:
                raise
            except RuntimeError as exc:
                task.check()
                last_err = exc
                continue
            if tail_frame_path.is_file() and tail_frame_path.stat().st_size > 0:
                return tail_frame_path
            if self._run_cmd_fn is not None:
                # Offline test spy recorded the ffmpeg tail-frame command without
                # materializing an image file on disk.
                return fallback_anchor

        task.check()
        tail_frame_path.unlink(missing_ok=True)
        try:
            self._invoke_run_cmd(retry_cmd, task, log_path, timeout=120)
        except ColabOneCancelledError:
            raise
        except RuntimeError as exc:
            task.check()
            last_err = exc

        if tail_frame_path.is_file() and tail_frame_path.stat().st_size > 0:
            return tail_frame_path
        if self._run_cmd_fn is not None and last_err is None:
            return fallback_anchor
        detail = f"：{last_err}" if last_err else ""
        raise RuntimeError(f"无法从第 {seg_idx} 段视频提取尾帧用于长视频衔接{detail}")

    def run(
        self,
        task,
        wf: dict[str, Any],
        req: WorkflowRunRequest | Any,
        resolved_inputs: list[tuple[str, str, Path]],
    ) -> list[dict[str, Any]]:
        """Execute the full 4-stage remote digital human pipeline."""
        started = time.monotonic()
        task.check()

        pipeline = wf.get("pipeline")
        if not isinstance(pipeline, dict):
            raise ValueError("工作流缺少 digital_human 流水线配置")
        if not isinstance(wf.get("workflow"), dict) or not wf["workflow"]:
            raise ValueError("工作流缺少有效节点配置")

        overrides: dict[str, Any] = dict(getattr(req, "overrides", None) or {})
        # Never log hf_token.
        hf_token = str(getattr(req, "hf_token", "") or "")
        inputs_by_key: dict[str, tuple[str, Path]] = {
            field_key: (fname, Path(fpath))
            for field_key, fname, fpath in (resolved_inputs or [])
        }

        video_backend = str(pipeline.get("video_backend") or "custom").strip().lower()
        default_tts_engine = str(pipeline.get("default_tts_engine") or "voxcpm2").strip()
        supported_tts_engines = list(pipeline.get("supported_tts_engines") or [])
        default_max_seg = _parse_float_override(
            pipeline.get("max_segment_seconds"), 45.0, "模板单段最大时长"
        )
        tail_offset = max(
            0.0,
            min(
                1.0,
                _parse_float_override(
                    pipeline.get("tail_frame_offset_seconds"),
                    0.08,
                    "尾帧偏移时长",
                ),
            ),
        )
        output_fps = max(1, min(60, int(pipeline.get("output_fps") or 25)))
        default_target_w = int(pipeline.get("target_width") or 1280)
        default_target_h = int(pipeline.get("target_height") or 720)
        default_upscale_mode = str(pipeline.get("upscale_mode") or "none")

        audio_node_key = str(pipeline.get("audio_node_key") or "").strip()
        image_node_key = str(pipeline.get("image_node_key") or "").strip()
        prompt_node_key = str(pipeline.get("prompt_node_key") or "").strip()
        frames_node_key = str(pipeline.get("frames_node_key") or "").strip()
        width_node_key = str(pipeline.get("width_node_key") or "").strip()
        height_node_key = str(pipeline.get("height_node_key") or "").strip()
        fps_node_key = str(pipeline.get("fps_node_key") or "").strip()
        steps_node_key = str(pipeline.get("steps_node_key") or "").strip()
        seed_node_key = str(pipeline.get("seed_node_key") or "").strip()

        # ------------------------------------------------------------------
        # Upfront Input & Preflight Validation (BEFORE unloading LLM / TTS)
        # ------------------------------------------------------------------
        effective_image_key = _resolve_media_node_key(
            image_node_key,
            "1.image",
            inputs_by_key,
            wf.get("workflow"),
            "LoadImage",
            "image",
        )
        effective_audio_key = _resolve_media_node_key(
            audio_node_key,
            "2.audio",
            inputs_by_key,
            wf.get("workflow"),
            "LoadAudio",
            "audio",
        )

        anchor_entry = inputs_by_key.get(effective_image_key)
        if anchor_entry is None or not anchor_entry[1].is_file():
            raise ValueError("请先上传主播参考人像图片")
        _, anchor_src_path = anchor_entry

        audio_source = str(
            overrides.get("pipeline.audio_source") or "tts"
        ).strip().lower()
        if audio_source not in ("tts", "audio"):
            raise ValueError(f"不支持的音频来源模式：{audio_source}")

        res_spec = overrides.get("pipeline.target_resolution")
        target_w, target_h = parse_target_resolution(
            str(res_spec) if res_spec else "",
            default=(default_target_w, default_target_h),
        )
        base_w, base_h = compute_base_dimensions(video_backend, target_w, target_h)

        upscale_mode = str(
            overrides.get("pipeline.upscale_mode") or default_upscale_mode or "none"
        ).strip().lower()
        if upscale_mode not in ("none", "ai_2x", "lanczos"):
            upscale_mode = "none"

        # H1: When upscale_mode == "none" and native (base_w, base_h) already covers
        # (target_w, target_h) (e.g. LongCat 1280x768 -> 1280x720 or 768x1280 -> 720x1280),
        # crop cleanly to exact (target_w, target_h) without upscaling. Only preserve native
        # (base_w, base_h) when native is smaller than target.
        if upscale_mode == "none":
            if base_w >= target_w and base_h >= target_h:
                out_w, out_h = target_w, target_h
            else:
                out_w, out_h = base_w, base_h
        else:
            out_w, out_h = target_w, target_h

        max_segment_sec = _parse_float_override(
            overrides.get("pipeline.max_segment_seconds"),
            default_max_seg,
            "单段最大时长",
        )
        if max_segment_sec <= 0.0:
            raise ValueError("单段最大时长必须大于 0")
        if video_backend == "longcat_avatar":
            longcat_cap = min(
                LONGCAT_MAX_SEGMENT_SECONDS,
                max(3.0, (LONGCAT_MAX_FRAMES - 8) / float(output_fps)),
            )
            max_segment_sec = max(3.0, min(longcat_cap, max_segment_sec))
        else:
            max_segment_sec = max(5.0, min(300.0, max_segment_sec))
        if upscale_mode == "ai_2x":
            ai_cap = min(
                AI_UPSCALE_MAX_SEGMENT_SECONDS,
                max(5.0, 500.0 / float(output_fps)),
            )
            max_segment_sec = min(max_segment_sec, ai_cap)

        # Validate mode-specific inputs before unloading any model or running TTS.
        script_text = ""
        tts_engine = ""
        ref_audio_path: Path | None = None
        ref_text = ""
        voice_prompt = ""
        emotion_ref_path: Path | None = None
        emotion_prompt = ""
        pause_seconds = 0.28
        audio_src_path: Path | None = None

        if audio_source == "tts":
            script_text = str(overrides.get("pipeline.script_text") or "").strip()
            if not script_text:
                raise ValueError(
                    "请先输入数字人播报文案，或切换为上传成品音频模式"
                )
            tts_engine = str(
                overrides.get("pipeline.tts_engine")
                or default_tts_engine
                or "voxcpm2"
            ).strip()
            if supported_tts_engines and tts_engine not in supported_tts_engines:
                raise ValueError(f"当前模板不支持所选语音引擎：{tts_engine}")
            if self.tts_runner is None:
                raise RuntimeError("远端 TTS 配音引擎尚未初始化")
            get_engine_fn = getattr(self.tts_runner, "get_engine", None)
            if callable(get_engine_fn):
                get_engine_fn(tts_engine)

            voice_ref_entry = inputs_by_key.get("pipeline.voice_ref_audio")
            ref_audio_path = voice_ref_entry[1] if voice_ref_entry else None
            if tts_engine == "indextts2" and (
                ref_audio_path is None or not ref_audio_path.is_file()
            ):
                raise ValueError("IndexTTS2 需要上传音色参考音频才能进行语音合成")
            if ref_audio_path is not None and not ref_audio_path.is_file():
                raise ValueError("音色参考音频文件不存在")

            emotion_ref_entry = inputs_by_key.get("pipeline.emotion_ref_audio")
            emotion_ref_path = emotion_ref_entry[1] if emotion_ref_entry else None
            if emotion_ref_path is not None and not emotion_ref_path.is_file():
                raise ValueError("情绪参考音频文件不存在")

            ref_text = str(overrides.get("pipeline.voice_ref_text") or "").strip()
            voice_prompt = str(overrides.get("pipeline.voice_prompt") or "").strip()
            emotion_prompt = str(
                overrides.get("pipeline.emotion_prompt") or ""
            ).strip()
            pause_seconds = _parse_float_override(
                overrides.get("pipeline.pause_seconds"),
                0.28,
                "句间自然停顿",
            )
            pause_seconds = max(0.0, min(1.5, pause_seconds))
        else:
            audio_entry = inputs_by_key.get(effective_audio_key)
            if audio_entry is None or not audio_entry[1].is_file():
                raise ValueError("请先上传驱动音频文件")
            _, audio_src_path = audio_entry

        # Determine base seed once so segments advance deterministically (seed + idx).
        base_seed = 42
        if seed_node_key:
            raw_seed = overrides.get(seed_node_key)
            if raw_seed is None:
                raw_seed = _node_default_value(wf["workflow"], seed_node_key)
            if raw_seed is not None and not isinstance(raw_seed, bool):
                try:
                    if isinstance(raw_seed, float) and not math.isfinite(raw_seed):
                        raise ValueError("non-finite seed")
                    base_seed = int(raw_seed)
                except (TypeError, ValueError):
                    base_seed = 42
        base_seed = max(0, base_seed)

        # Verify ffmpeg availability and GPU/disk preflight before touching GPU state or TTS.
        ffmpeg_bin = self._ffmpeg_binary()
        packs = getattr(self.manager, "packs", None)
        if packs is not None and callable(getattr(packs, "check_preflight", None)):
            packs.check_preflight(wf)

        runtime_root_raw = getattr(
            getattr(self.manager, "runtime", None), "root", tempfile.gettempdir()
        )
        runtime_root = _validate_safe_path(
            Path(runtime_root_raw)
            if isinstance(runtime_root_raw, (str, os.PathLike))
            else Path(tempfile.gettempdir()),
            "运行目录",
        )
        work_parent = runtime_root / "comfyui-work"
        work_parent.mkdir(parents=True, exist_ok=True)
        _prune_stale_work_dirs(work_parent)
        safe_task_tag = _sanitize_task_tag(str(getattr(task, "id", "task"))[:8])
        work_dir = _validate_safe_path(
            Path(
                tempfile.mkdtemp(
                    prefix=f"dh-{safe_task_tag}-", dir=str(work_parent)
                )
            ),
            "工作目录",
        )

        temp_io_in_files: set[str] = set()
        temp_io_out_files: set[tuple[str, str]] = set()

        try:
            task.check()
            # ------------------------------------------------------------------
            # Stage 1 — Audio Preparation (0%..18% progress)
            # ------------------------------------------------------------------
            _update_task(task, progress=0.02)
            manifest: dict[str, Any] | None = None
            valleys: list[float] = []

            if audio_source == "tts":
                assert self.tts_runner is not None
                # Unload any active LLM and free ComfyUI VRAM before TTS GPU pass.
                runtime = getattr(self.manager, "runtime", None)
                if getattr(runtime, "loaded", None) and callable(
                    getattr(runtime, "_stop_engine", None)
                ):
                    task.note("正在卸载当前对话模型以释放显存")
                    runtime._stop_engine()

                if callable(getattr(self.manager, "is_running", None)) and self.manager.is_running():
                    free_msg = "正在释放 ComfyUI 显存以启动远端配音引擎"
                    _update_task(task, message=free_msg)
                    task.note(free_msg)
                    try:
                        self.manager.free_memory(unload_models=True)
                    except Exception as exc:
                        task.note(
                            f"释放 ComfyUI 显存时出现非致命提示：{exc}",
                            level="warning",
                        )

                task.check()
                tts_dir = work_dir / "tts"
                tts_dir.mkdir(parents=True, exist_ok=True)
                tts_msg = f"正在使用远端 TTS 引擎（{tts_engine}）合成高保真配音"
                _update_task(task, progress=0.05, message=tts_msg)
                task.note(tts_msg)

                tts_result = self.tts_runner.synthesize(
                    task,
                    engine_id=tts_engine,
                    script_text=script_text,
                    work_dir=tts_dir,
                    ref_audio_path=ref_audio_path,
                    ref_text=ref_text,
                    voice_prompt=voice_prompt,
                    emotion_ref_path=emotion_ref_path,
                    emotion_prompt=emotion_prompt,
                    pause_seconds=pause_seconds,
                    hf_token=hf_token,
                )
                master_audio_path = Path(tts_result["audio_path"])
                raw_manifest = tts_result.get("manifest")
                manifest = raw_manifest if isinstance(raw_manifest, dict) else None
                duration = float(tts_result.get("duration") or 0.0)
                if duration <= 0.0 and _is_readable_pcm_wav(master_audio_path):
                    duration = _wav_duration_seconds(master_audio_path)

                # Prefer exact pause midpoints recorded in manifest["silence_valleys"]
                # directly when non-empty; only scan PCM WAV when manifest valleys are absent.
                manifest_valleys = _extract_valid_timestamps(
                    manifest.get("silence_valleys")
                    if isinstance(manifest, dict)
                    else None,
                    duration,
                )
                if manifest_valleys:
                    valleys = manifest_valleys
                elif _is_readable_pcm_wav(master_audio_path):
                    valleys = detect_pcm_silence_valleys(master_audio_path)
                _update_task(task, progress=0.18)
                task.note(f"远端配音完成，音频总时长 {duration:.1f} 秒")
            else:
                assert audio_src_path is not None
                audio_msg = "正在预处理上传音频（48 kHz / -23 LUFS）"
                _update_task(task, progress=0.06, message=audio_msg)
                task.note(audio_msg)
                master_audio_path, duration, valleys = self._prepare_uploaded_audio(
                    task, audio_src_path, work_dir
                )
                _update_task(task, progress=0.18)
                task.note(
                    f"音频预处理完成，总时长 {duration:.1f} 秒，检测到 {len(valleys)} 处自然换气静音点"
                )

            if duration <= 0.0:
                raise ValueError("驱动音频时长为 0，无法生成数字人视频")
            master_audio_path = _validate_safe_path(master_audio_path, "主音频路径")

            # ------------------------------------------------------------------
            # Stage 2 — Pack & Anchor Preparation (18%..32% progress)
            # ------------------------------------------------------------------
            task.check()
            anchor_msg = f"正在预处理主播参考图（{base_w}×{base_h}）"
            _update_task(task, progress=0.20, message=anchor_msg)
            task.note(anchor_msg)
            anchor_prepared_path = work_dir / "anchor_prepared.png"
            prepare_anchor_image(anchor_src_path, anchor_prepared_path, base_w, base_h)

            task.check()
            wf_name = str(wf.get("name") or wf.get("id") or "数字人工作流")
            env_msg = f"正在准备远端视频环境与模型权重：{wf_name}"
            _update_task(task, progress=0.22, message=env_msg)
            task.note(env_msg)
            ensure_fn = getattr(self.manager, "ensure_template_ready", None)
            if callable(ensure_fn):
                ensure_kwargs: dict[str, Any] = {"hf_token": hf_token}
                try:
                    ensure_sig = inspect.signature(ensure_fn)
                    if "progress_range" in ensure_sig.parameters or any(
                        p.kind == inspect.Parameter.VAR_KEYWORD
                        for p in ensure_sig.parameters.values()
                    ):
                        ensure_kwargs["progress_range"] = (0.22, 0.30)
                except (TypeError, ValueError):
                    ensure_kwargs["progress_range"] = (0.22, 0.30)
                ensure_fn(task, wf, **ensure_kwargs)
            elif not self.manager.is_running():
                self.manager.start_server(task)

            task.check()
            _update_task(task, progress=0.30)
            segments = plan_audio_segments(
                duration,
                max_segment_sec=max_segment_sec,
                manifest=manifest,
                silence_valleys=valleys,
            )
            if not segments:
                raise ValueError("音频时长不足，无法规划视频分段")

            models_dir = getattr(self.manager, "models_dir", None)
            upscale_model_name = _find_upscale_model_name(
                wf,
                models_dir=Path(models_dir)
                if isinstance(models_dir, (str, os.PathLike))
                else None,
            )
            if upscale_mode == "ai_2x" and not upscale_model_name:
                task.note(
                    "当前模板未声明 AI 超分模型且本地未检测到超分权重，将自动回退为 Lanczos 高清缩放",
                    level="warning",
                )

            # Compute exact cumulative integer frame boundaries per segment so
            # sum(exact_frames) matches round(duration * output_fps) with zero cumulative drift.
            total_segs = len(segments)
            total_target_frames = max(1, int(round(duration * output_fps)))
            segment_plan: list[tuple[float, float, int, int, int]] = []
            prev_end_frame = 0
            for idx, (seg_start, seg_end) in enumerate(segments):
                start_frame = (
                    prev_end_frame
                    if idx > 0
                    else max(0, int(round(seg_start * output_fps)))
                )
                if idx == total_segs - 1:
                    end_frame = total_target_frames
                else:
                    end_frame = int(round(seg_end * output_fps))
                if end_frame <= start_frame:
                    end_frame = start_frame + 1
                exact_frames = end_frame - start_frame
                prev_end_frame = end_frame
                frame_start_sec = start_frame / float(output_fps)
                frame_end_sec = (
                    duration
                    if idx == total_segs - 1
                    else (end_frame / float(output_fps))
                )
                segment_plan.append(
                    (frame_start_sec, frame_end_sec, start_frame, end_frame, exact_frames)
                )

            _update_task(task, progress=0.32)
            task.note(
                f"分段规划完成：共 {total_segs} 段（总时长 {duration:.1f} 秒，"
                f"基础渲染 {base_w}×{base_h}，目标输出 {out_w}×{out_h} @ {output_fps}fps）"
            )

            # Pre-upload any extra non-pipeline inputs beyond anchor image & driving audio.
            extra_media_map: dict[str, str] = {}
            for field_key, fname, fpath in resolved_inputs or []:
                if field_key.startswith("pipeline.") or field_key in (
                    effective_image_key,
                    effective_audio_key,
                ):
                    continue
                ext = Path(fpath).suffix.lstrip(".").lower()
                is_img = ext in ("png", "jpg", "jpeg", "webp")
                extra_media_map[field_key] = self._upload_to_comfy(
                    task, fname, Path(fpath), is_image=is_img
                )

            # ------------------------------------------------------------------
            # Stage 3 — Multi-Segment Relay Rendering (32%..90% progress)
            # ------------------------------------------------------------------
            seg_span = (0.90 - 0.32) / total_segs
            segment_mp4s: list[Path] = []
            segment_exact_frames: list[int] = []
            last_prompt_id = ""
            any_ai_upscaled = False

            for idx, (
                seg_start,
                seg_end,
                _start_frame,
                _end_frame,
                exact_frames,
            ) in enumerate(segment_plan):
                task.check()
                seg_lo = 0.32 + idx * seg_span
                seg_hi = 0.32 + (idx + 1) * seg_span
                segment_label = f"第 {idx + 1}/{total_segs} 段"

                # Slice segment audio cleanly with stdlib wave (lossless & offline-safe).
                # For idx > 0, start 1 frame earlier so frame 0 overlaps the relay anchor
                # and frames [1, exact_frames + 1) align frame-exact with master_audio_path.
                seg_audio_start = (
                    max(0.0, seg_start - 1.0 / float(output_fps))
                    if idx > 0
                    else seg_start
                )
                seg_audio_path = work_dir / f"seg_{idx:03d}.wav"
                if _is_readable_pcm_wav(master_audio_path):
                    slice_wav_segment(
                        master_audio_path, seg_audio_path, seg_audio_start, seg_end
                    )
                else:
                    self._invoke_run_cmd(
                        [
                            ffmpeg_bin,
                            "-y",
                            "-protocol_whitelist",
                            "file,pipe",
                            "-ss",
                            f"{seg_audio_start:.6f}",
                            "-to",
                            f"{seg_end:.6f}",
                            "-i",
                            str(master_audio_path),
                            "-ar",
                            "48000",
                            "-c:a",
                            "pcm_s16le",
                            str(seg_audio_path),
                        ],
                        task,
                        work_dir / f"ffmpeg_slice_{idx:03d}.log",
                        timeout=120,
                    )
                    if not seg_audio_path.is_file():
                        shutil.copyfile(master_audio_path, seg_audio_path)

                # Select segment anchor image: prepared portrait for seg 0,
                # tail frame extracted right at the previous segment's last kept frame for seg > 0.
                if idx == 0:
                    seg_image_path = anchor_prepared_path
                else:
                    prev_end_frame_exclusive = segment_exact_frames[-1] + (
                        1 if (idx - 1) > 0 else 0
                    )
                    tail_frame_path = work_dir / f"relay_{idx:03d}.png"
                    seg_image_path = self._extract_tail_frame(
                        task=task,
                        prev_seg_mp4=segment_mp4s[-1],
                        tail_frame_path=tail_frame_path,
                        tail_offset=tail_offset,
                        base_w=base_w,
                        base_h=base_h,
                        fallback_anchor=anchor_prepared_path,
                        work_dir=work_dir,
                        seg_idx=idx,
                        exact_frames=prev_end_frame_exclusive,
                        output_fps=output_fps,
                    )

                # Upload segment image and audio to ComfyUI and track for finally cleanup.
                img_upload_name = f"dh_{safe_task_tag}_seg{idx:03d}_img.png"
                aud_upload_name = f"dh_{safe_task_tag}_seg{idx:03d}_aud.wav"
                uploaded_img = self._upload_to_comfy(
                    task,
                    img_upload_name,
                    seg_image_path,
                    is_image=True,
                )
                temp_io_in_files.add(img_upload_name)
                if uploaded_img:
                    temp_io_in_files.add(uploaded_img)
                uploaded_aud = self._upload_to_comfy(
                    task,
                    aud_upload_name,
                    seg_audio_path,
                    is_image=False,
                )
                temp_io_in_files.add(aud_upload_name)
                if uploaded_aud:
                    temp_io_in_files.add(uploaded_aud)

                # Build per-segment overrides and Wan 4k+1 aligned frame count.
                # For idx > 0, request +1 overlap frame (exact_frames + 1) so frame 0 absorbs
                # the relay tail frame and Stage 4 trims [1, exact_frames + 1) cleanly.
                seg_overrides: dict[str, Any] = {
                    k: v for k, v in overrides.items() if not k.startswith("pipeline.")
                }
                raw_frames = exact_frames + (1 if idx > 0 else 0)
                num_frames = align_wan_frames(raw_frames, minimum=17)
                if video_backend == "longcat_avatar":
                    num_frames = min(num_frames, LONGCAT_MAX_FRAMES)

                if width_node_key:
                    seg_overrides[width_node_key] = int(base_w)
                if height_node_key:
                    seg_overrides[height_node_key] = int(base_h)
                if fps_node_key:
                    default_fps_val = _node_default_value(wf["workflow"], fps_node_key)
                    seg_overrides[fps_node_key] = (
                        float(output_fps)
                        if isinstance(default_fps_val, float)
                        else int(output_fps)
                    )
                if frames_node_key:
                    seg_overrides[frames_node_key] = int(num_frames)
                if steps_node_key and steps_node_key in overrides:
                    raw_steps = overrides[steps_node_key]
                    if not isinstance(raw_steps, bool):
                        try:
                            if isinstance(raw_steps, float) and not math.isfinite(
                                raw_steps
                            ):
                                raise ValueError("non-finite steps")
                            seg_overrides[steps_node_key] = int(raw_steps)
                        except (TypeError, ValueError):
                            pass
                if seed_node_key:
                    seg_overrides[seed_node_key] = int(base_seed + idx)
                if prompt_node_key and prompt_node_key in overrides:
                    seg_overrides[prompt_node_key] = str(overrides[prompt_node_key])

                media_map = dict(extra_media_map)
                if effective_image_key:
                    media_map[effective_image_key] = uploaded_img
                if effective_audio_key:
                    media_map[effective_audio_key] = uploaded_aud

                prompt_payload = apply_workflow_overrides(
                    wf["workflow"], seg_overrides, media_map
                )
                # Master audio is already normalized to -23 LUFS in Stage 1; disable
                # per-segment loudness normalization so levels stay uniform across cuts
                # and short (<0.4s) clips never crash pyloudnorm.
                node_9 = prompt_payload.get("9")
                if (
                    isinstance(node_9, dict)
                    and isinstance(node_9.get("inputs"), dict)
                    and "normalize_loudness" in node_9["inputs"]
                ):
                    node_9["inputs"]["normalize_loudness"] = False
                for node in prompt_payload.values():
                    if not isinstance(node, dict) or not isinstance(
                        node.get("inputs"), dict
                    ):
                        continue
                    cls = node.get("class_type")
                    if (
                        cls in ("MultiTalkWav2VecEmbeds", "LongCatAvatarWhisperEmbeds")
                        and "normalize_loudness" in node["inputs"]
                    ):
                        node["inputs"]["normalize_loudness"] = False
                    elif cls == "VHS_VideoCombine":
                        vhs_inputs = node["inputs"]
                        if isinstance(
                            vhs_inputs.get("frame_rate"), (int, float)
                        ) and not isinstance(vhs_inputs.get("frame_rate"), bool):
                            vhs_inputs["frame_rate"] = int(output_fps)
                        vhs_inputs["filename_prefix"] = (
                            f"dh_{safe_task_tag}_seg{idx:03d}"
                        )
                        vhs_inputs["save_metadata"] = False
                        vhs_inputs.pop("audio", None)

                # Inject in-graph AI 2x super-resolution (ImageUpscaleWithModel -> VHS_VideoCombine).
                seg_ai_upscaled = False
                if upscale_mode == "ai_2x" and upscale_model_name:
                    prompt_payload = inject_ai_upscale_nodes(
                        prompt_payload,
                        upscale_model_name=upscale_model_name,
                        target_w=out_w,
                        target_h=out_h,
                        base_w=base_w,
                        base_h=base_h,
                    )
                    seg_ai_upscaled = "colabone_upscale_apply" in prompt_payload
                any_ai_upscaled = any_ai_upscaled or seg_ai_upscaled

                seg_msg = (
                    f"{wf_name} · 正在渲染 {segment_label}"
                    f"（{seg_start:.1f}s–{seg_end:.1f}s，{num_frames} 帧）"
                )
                _update_task(task, progress=round(seg_lo, 4), message=seg_msg)
                task.note(seg_msg)

                exec_fn = self.manager._execute_single_prompt
                exec_kwargs: dict[str, Any] = {
                    "progress_range": (seg_lo, seg_hi),
                    "segment_label": segment_label,
                }
                try:
                    exec_sig = inspect.signature(exec_fn)
                    has_var_kw = any(
                        p.kind == inspect.Parameter.VAR_KEYWORD
                        for p in exec_sig.parameters.values()
                    )
                    if has_var_kw or "save_to_store" in exec_sig.parameters:
                        exec_kwargs["save_to_store"] = False
                    if has_var_kw or "output_dir" in exec_sig.parameters:
                        exec_kwargs["output_dir"] = work_dir
                except (TypeError, ValueError):
                    exec_kwargs["save_to_store"] = False

                exec_result = exec_fn(task, wf, prompt_payload, **exec_kwargs)
                seg_prompt_id, output_items = _parse_single_prompt_result(exec_result)
                if seg_prompt_id:
                    last_prompt_id = seg_prompt_id

                for item in output_items:
                    if (
                        isinstance(item, dict)
                        and item.get("filename")
                        and str(item.get("type") or "output") == "output"
                    ):
                        temp_io_out_files.add(
                            (str(item.get("subfolder") or ""), str(item["filename"]))
                        )

                video_item = _pick_video_output_item(output_items)
                if video_item is None:
                    raise RuntimeError(
                        f"{segment_label} 执行完成但未产出有效视频文件"
                    )

                seg_mp4_path = work_dir / f"seg_{idx:03d}.mp4"
                self._materialize_output_item_to_path(
                    task,
                    video_item,
                    seg_mp4_path,
                    work_dir=work_dir,
                    segment_label=segment_label,
                )
                # Remove any unselected extra files streamed into work_dir for this segment.
                for item in output_items:
                    if item is video_item or not isinstance(item, dict) or not item.get("path"):
                        continue
                    extra_raw = Path(item["path"])
                    try:
                        rel_extra = extra_raw.relative_to(work_dir)
                    except ValueError:
                        continue
                    safe_extra = _safe_contained_file(work_dir, *rel_extra.parts)
                    if safe_extra is not None and safe_extra.resolve() != seg_mp4_path.resolve():
                        safe_extra.unlink(missing_ok=True)

                segment_mp4s.append(seg_mp4_path)
                segment_exact_frames.append(exact_frames)
                _update_task(task, progress=round(seg_hi, 4))

            # ------------------------------------------------------------------
            # Stage 4 — Final Frame-Exact Concat, Scale/Crop & 48kHz Audio Muxing (90%..100%)
            # ------------------------------------------------------------------
            task.check()
            stage4_msg = "正在进行最终视频拼接、分辨率精调与 48 kHz 高保真音频合成"
            _update_task(task, progress=0.91, message=stage4_msg)
            task.note(stage4_msg)

            final_mp4_path = work_dir / "final_output.mp4"
            filter_script_path = work_dir / "filter_complex.txt"

            if any_ai_upscaled:
                needs_resize_or_crop = True
                crop_only = False
            else:
                needs_resize_or_crop = (base_w != out_w) or (base_h != out_h)
                crop_only = (
                    needs_resize_or_crop
                    and base_w >= out_w
                    and base_h >= out_h
                    and (base_w == out_w or base_h == out_h)
                    and bool(width_node_key and height_node_key)
                )

            filter_script_text = build_concat_filter_script(
                segment_exact_frames,
                out_w=out_w,
                out_h=out_h,
                needs_resize_or_crop=needs_resize_or_crop,
                crop_only=crop_only,
            )
            filter_script_path.write_text(filter_script_text, encoding="utf-8")

            input_args: list[str] = []
            for seg_path in segment_mp4s:
                input_args.extend(
                    ["-protocol_whitelist", "file,pipe", "-i", seg_path.name]
                )
            input_args.extend(
                ["-protocol_whitelist", "file,pipe", "-i", str(master_audio_path)]
            )

            final_cmd = [
                ffmpeg_bin,
                "-y",
                *input_args,
                "-filter_complex_script",
                str(filter_script_path),
                "-map",
                "[vout]",
                "-map",
                f"{len(segment_mp4s)}:a:0",
                "-c:v",
                "libx264",
                "-preset",
                "medium",
                "-crf",
                "18",
                "-pix_fmt",
                "yuv420p",
                "-r",
                str(output_fps),
                "-c:a",
                "aac",
                "-b:a",
                "192k",
                "-ar",
                "48000",
                "-movflags",
                "+faststart",
                "-shortest",
                str(final_mp4_path),
            ]
            self._invoke_run_cmd(
                final_cmd,
                task,
                work_dir / "ffmpeg_final.log",
                timeout=1800,
                cwd=work_dir,
            )

            if final_mp4_path.is_file() and final_mp4_path.stat().st_size > 0:
                output_src_path = final_mp4_path
            elif (
                self._run_cmd_fn is not None
                and segment_mp4s[0].is_file()
                and segment_mp4s[0].stat().st_size > 0
            ):
                # Offline test fallback when mocked run_cmd_fn only records commands.
                output_src_path = segment_mp4s[0]
            else:
                raise RuntimeError("最终视频合成失败：ffmpeg 未产出有效 MP4 文件")

            task.check()
            _update_task(task, progress=0.97)
            elapsed = max(0.0, time.monotonic() - started)
            out_filename = f"{wf['id']}_{out_w}x{out_h}_{int(time.time())}.mp4"
            save_file_fn = getattr(self.manager.store, "save_output_file", None)
            if callable(save_file_fn):
                record = save_file_fn(
                    output_src_path,
                    workflow_id=wf["id"],
                    workflow_name=wf_name,
                    filename=out_filename,
                    elapsed=elapsed,
                    prompt_id=last_prompt_id,
                )
            else:
                record = self.manager.store.save_output(
                    raw=output_src_path.read_bytes(),
                    workflow_id=wf["id"],
                    workflow_name=wf_name,
                    filename=out_filename,
                    elapsed=elapsed,
                    prompt_id=last_prompt_id,
                )
            done_msg = (
                f"{wf_name} · 数字人长视频生成完成"
                f"（共 {total_segs} 段，{duration:.1f} 秒，{out_w}×{out_h}）"
            )
            _update_task(task, progress=1, message=done_msg, append_output=record)
            task.note(done_msg)
            return [record]
        finally:
            shutil.rmtree(work_dir, ignore_errors=True)
            self._cleanup_intermediate_io_files(
                safe_task_tag, temp_io_in_files, temp_io_out_files
            )
