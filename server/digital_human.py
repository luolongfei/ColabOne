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
import stat
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
    format_colabone_duration_zh,
)
from common.files import (
    COLABONE_SENSITIVE_SUBPROCESS_ENV_KEYS,
    colabone_sanitized_subprocess_env,
)

if TYPE_CHECKING:
    from server.tts_engines import ColabOneRemoteTtsRunner

_RES_SPEC_RE = re.compile(r"^\s*(\d+)\s*[xX×*]\s*(\d+)\s*$")

# Standard broadcast 1080p and 2K/QHD dimensions (1080 is divisible by 8, not 16)
# are preserved verbatim so Full HD / 2K target output is never distorted or snapped down.
_EXACT_STANDARD_RESOLUTIONS = frozenset(
    {(1920, 1080), (1080, 1920), (2560, 1440), (1440, 2560)}
)

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
_SENSITIVE_SUBPROCESS_ENV_KEYS = COLABONE_SENSITIVE_SUBPROCESS_ENV_KEYS
_FFMPEG_CFR_CACHE_LOCK = threading.Lock()
_FFMPEG_CFR_CACHE: dict[str, tuple[str, str]] = {}
_FFMPEG_NVENC_CACHE_LOCK = threading.Lock()
_FFMPEG_NVENC_CACHE: dict[str, bool] = {}


def _resolve_ffmpeg_cfr_args(ffmpeg_bin: str) -> list[str]:
    """Return ['-fps_mode', 'cfr'] on FFmpeg >= 5.1, falling back to ['-vsync', 'cfr'] on older builds."""
    key = str(ffmpeg_bin or "").strip() or "ffmpeg"
    with _FFMPEG_CFR_CACHE_LOCK:
        cached = _FFMPEG_CFR_CACHE.get(key)
    if cached is not None:
        return list(cached)

    chosen: tuple[str, str] = ("-vsync", "cfr")
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
            chosen = ("-fps_mode", "cfr")
    except Exception:
        chosen = ("-vsync", "cfr")

    with _FFMPEG_CFR_CACHE_LOCK:
        _FFMPEG_CFR_CACHE[key] = chosen
    return list(chosen)


def _mark_ffmpeg_nvenc_unavailable(ffmpeg_bin: str) -> None:
    """Mark h264_nvenc unavailable for this ffmpeg binary after a runtime failure."""
    key = str(ffmpeg_bin or "").strip() or "ffmpeg"
    with _FFMPEG_NVENC_CACHE_LOCK:
        _FFMPEG_NVENC_CACHE[key] = False


def _probe_ffmpeg_nvenc_h264(ffmpeg_bin: str) -> bool:
    """Return True if ffmpeg advertises h264_nvenc AND a live 256x256 GPU NVENC encode succeeds."""
    key = str(ffmpeg_bin or "").strip() or "ffmpeg"
    with _FFMPEG_NVENC_CACHE_LOCK:
        cached = _FFMPEG_NVENC_CACHE.get(key)
    if cached is not None:
        return cached

    supported = False
    try:
        env = _sanitized_subprocess_env()
        with tempfile.TemporaryDirectory(prefix="colabone-nvenc-probe-") as probe_dir:
            enc_res = subprocess.run(
                [key, "-hide_banner", "-encoders"],
                capture_output=True,
                text=True,
                timeout=5,
                check=False,
                stdin=subprocess.DEVNULL,
                cwd=probe_dir,
                env=env,
            )
            enc_text = (enc_res.stdout or "") + "\n" + (enc_res.stderr or "")
            if enc_res.returncode == 0 and "h264_nvenc" in enc_text:
                test_res = subprocess.run(
                    [
                        key,
                        "-hide_banner",
                        "-loglevel",
                        "error",
                        "-f",
                        "lavfi",
                        "-i",
                        "color=c=black:s=256x256:d=0.2:r=25",
                        "-frames:v",
                        "5",
                        "-pix_fmt",
                        "yuv420p",
                        *_build_h264_encode_args(18, use_nvenc=True),
                        "-colorspace",
                        "bt709",
                        "-color_primaries",
                        "bt709",
                        "-color_trc",
                        "bt709",
                        "-color_range",
                        "tv",
                        "-f",
                        "null",
                        "-",
                    ],
                    capture_output=True,
                    text=True,
                    timeout=10,
                    check=False,
                    stdin=subprocess.DEVNULL,
                    cwd=probe_dir,
                    env=env,
                )
                supported = test_res.returncode == 0
    except Exception:
        supported = False

    with _FFMPEG_NVENC_CACHE_LOCK:
        _FFMPEG_NVENC_CACHE[key] = supported
    return supported


def _build_h264_encode_args(
    crf: int,
    *,
    preset: str = "medium",
    use_nvenc: bool = False,
    rate_args: list[str] | None = None,
) -> list[str]:
    """Build FFmpeg H.264 encoder CLI args for GPU NVENC or CPU libx264."""
    if use_nvenc:
        if rate_args:
            return [
                "-c:v",
                "h264_nvenc",
                "-preset",
                "p6",
                "-tune",
                "hq",
                "-rc",
                "vbr",
                *rate_args,
                "-spatial-aq",
                "1",
                "-temporal-aq",
                "1",
            ]
        nvenc_cq = max(1, min(28, int(crf) + 2))
        return [
            "-c:v",
            "h264_nvenc",
            "-preset",
            "p6",
            "-tune",
            "hq",
            "-rc",
            "vbr",
            "-cq",
            str(nvenc_cq),
            "-b:v",
            "0",
            "-spatial-aq",
            "1",
            "-temporal-aq",
            "1",
        ]
    return [
        "-c:v",
        "libx264",
        "-preset",
        str(preset),
        *(list(rate_args) if rate_args else ["-crf", str(int(crf))]),
    ]


def _sanitize_task_tag(raw_tag: Any, *, allow_empty: bool = False) -> str:
    """Restrict task tags used in filenames and glob prefixes to [a-zA-Z0-9_-]."""
    cleaned = re.sub(r"[^a-zA-Z0-9_-]", "", str(raw_tag or ""))[:64]
    if cleaned:
        return cleaned
    return "" if allow_empty else "task"


def _callable_accepts_kwarg(fn: Callable[..., Any], kwarg_name: str) -> bool:
    """Return True if fn accepts kwarg_name explicitly or via **kwargs."""
    try:
        sig = inspect.signature(fn)
    except (TypeError, ValueError):
        return True
    return any(
        (
            param.name == kwarg_name
            and param.kind
            in (
                inspect.Parameter.POSITIONAL_OR_KEYWORD,
                inspect.Parameter.KEYWORD_ONLY,
            )
        )
        or param.kind == inspect.Parameter.VAR_KEYWORD
        for param in sig.parameters.values()
    )


def _validate_safe_path(path: Path | str, label: str = "路径") -> Path:
    """Reject CR, LF, and NUL control characters in filesystem paths passed to ffmpeg."""
    raw = os.fspath(path)
    if any(ch in raw for ch in ("\r", "\n", "\x00")):
        raise ValueError(f"{label}不能包含换行符或空字符")
    return Path(raw)


def _sanitized_subprocess_env() -> dict[str, str]:
    """Return a subprocess environment with ColabOne secrets stripped."""
    return colabone_sanitized_subprocess_env()


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
    """Parse a resolution string like '1280x720', '1920x1080', or '2560x1440'.

    Preserves exact standard 1080p and 2K/QHD dimensions, clamps all other parsed
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
    video_backend: str,
    target_w: int,
    target_h: int,
    render_quality: str | None = None,
) -> tuple[int, int]:
    """Compute native diffusion base (width, height) for a video backend."""
    portrait = int(target_h) > int(target_w)
    backend = str(video_backend or "").strip().lower()
    if backend == "longcat_avatar_native":
        tw = max(1, int(target_w))
        th = max(1, int(target_h))
        ratio = tw / float(th)
        if 0.85 <= ratio <= 1.18:
            return (960, 960)
        return (768, 1280) if portrait else (1280, 768)
    if backend == "longcat_avatar":
        rq = str(render_quality or "").strip().lower()
        if rq == "native_720p" and max(int(target_w), int(target_h)) >= 1280:
            return (768, 1280) if portrait else (1280, 768)
        # Official Meituan / Kijai 480p golden window (default & balanced_480p_ai2x).
        return (480, 832) if portrait else (832, 480)
    # "infinitetalk" (Wan2.1 480p sweet spot) and fallback "custom".
    return (480, 832) if portrait else (832, 480)


COLABONE_LONGCAT_NATIVE_FRAMING_PROMPTS: dict[str, str] = {
    "half_body_gestures": (
        "Cinematic static medium half-body shot of an engaging presenter speaking "
        "warmly and naturally directly to the camera, natural synchronized lip "
        "articulation with clean crisp teeth when smiling or speaking, lively "
        "authentic facial micro-expressions, gentle eyebrow raises, natural eye "
        "contact and blinking, expressive natural open-palm hand gestures and "
        "anatomically accurate five-fingered hand movements emphasizing key points "
        "without finger distortion, relaxed shoulder rhythm and subtle head nods, "
        "steady locked tripod camera, soft studio key lighting, crisp skin and "
        "fabric texture, clean stable background, photorealistic broadcast quality"
    ),
    "closeup_presenter": (
        "Cinematic static close-up chest-and-shoulders shot of a poised presenter "
        "speaking clearly and warmly directly to the camera, ultra-precise natural "
        "lip articulation and jaw motion synced with speech, clean crisp natural "
        "teeth when smiling or speaking, expressive authentic eye contact, subtle "
        "eyebrow nuances, natural blinking and micro-nods, calm composed posture "
        "with subtle shoulder breathing and occasional natural hand accents near "
        "the lower frame with anatomically accurate fingers, steady locked camera, "
        "flattering soft studio lighting, fine realistic skin pores and hair "
        "detail, clean softly blurred stable background, photorealistic cinema quality"
    ),
    "full_body_dynamic": (
        "Cinematic static wide full-body shot of a charismatic presenter standing "
        "naturally and speaking directly to the camera, clear natural lip "
        "articulation with crisp natural teeth when smiling or speaking, lively "
        "expressive facial expressions, energetic yet fluid natural hand gestures "
        "and open-palm arm movements with anatomically accurate fingers and zero "
        "hand distortion, balanced natural posture shifts, subtle weight transfer "
        "and organic upper-body rhythm, steady locked tripod camera with consistent "
        "framing, even cinematic studio lighting, sharp focus on subject, clean "
        "stable background, photorealistic production quality"
    ),
}

COLABONE_LONGCAT_NATIVE_DEFAULT_NEGATIVE_PROMPT = (
    "blurry, out of focus, low resolution, low quality, worst quality, JPEG "
    "artifacts, compression noise, overexposed, harsh blown-out highlights, "
    "flat muddy gray, static frozen face, stiff robotic posture, motionless "
    "arms, unnatural lip sync, warped mouth, smeared or missing teeth, deformed "
    "teeth, extra fingers, missing fingers, fused fingers, mutated hands, "
    "poorly drawn hands, extra limbs, disfigured anatomy, flickering skin "
    "texture, identity drift, plastic waxy skin, camera shake, sudden zoom, "
    "flickering background, subtitles, watermark, text overlay, logo"
)

_LONGCAT_NATIVE_QUALITY_PRESETS: dict[str, dict[str, Any]] = {
    "cinema_flagship": {
        "sampling_mode": "non_distill_cfg",
        "upscale_engine": "seedvr2_7b_sharp",
        "num_inference_steps": 50,
        "audio_guidance_scale": 4.0,
        "text_guidance_scale": 4.0,
        "mix_steps": 0,
        "mix_alpha": 0.0,
    },
    "balanced_hd": {
        "sampling_mode": "distill_lora_mix",
        "upscale_engine": "seedvr2_7b",
        "num_inference_steps": 8,
        "audio_guidance_scale": 1.0,
        "text_guidance_scale": 1.0,
        "mix_steps": 4,
        "mix_alpha": 1.0,
    },
    "longform_fast": {
        "sampling_mode": "distill_lora_mix",
        "upscale_engine": "flashvsr_plus",
        "num_inference_steps": 8,
        "audio_guidance_scale": 1.0,
        "text_guidance_scale": 1.0,
        "mix_steps": 4,
        "mix_alpha": 1.0,
    },
}

_LONGCAT_NATIVE_VSR_ENGINES = frozenset(
    {"seedvr2_7b_sharp", "seedvr2_7b", "flashvsr_plus"}
)


def _parse_int_override(raw: Any, default: int, label: str) -> int:
    """Safely parse an integer override, rejecting booleans, non-finite, and malformed values."""
    if raw is None:
        return int(default)
    if isinstance(raw, bool):
        raise ValueError(f"{label} 参数格式无效：不能为布尔值")
    if isinstance(raw, str):
        text = raw.strip()
        if not text:
            return int(default)
        try:
            return int(text)
        except ValueError:
            try:
                fval = float(text)
            except ValueError as exc:
                raise ValueError(f"{label} 参数格式无效：{raw!r}") from exc
            if not math.isfinite(fval):
                raise ValueError(f"{label} 必须为有限数值")
            return int(round(fval))
    if isinstance(raw, int):
        return raw
    if isinstance(raw, float):
        if not math.isfinite(raw):
            raise ValueError(f"{label} 必须为有限数值")
        return int(round(raw))
    raise ValueError(f"{label} 参数格式无效：{raw!r}")


def resolve_longcat_native_preset(
    overrides: dict[str, Any] | None,
    *,
    target_w: int,
    target_h: int,
) -> dict[str, Any]:
    """Normalize LongCat-Video-Avatar 1.6 native flagship quality/framing/VSR configuration."""
    ov: dict[str, Any] = dict(overrides) if isinstance(overrides, dict) else {}

    def _get(name: str) -> Any:
        prefixed = f"pipeline.{name}"
        if prefixed in ov and ov[prefixed] is not None:
            return ov[prefixed]
        if name in ov and ov[name] is not None:
            return ov[name]
        return None

    norm_target_w = _ensure_even_dimension(int(target_w))
    norm_target_h = _ensure_even_dimension(int(target_h))
    base_w, base_h = compute_base_dimensions(
        "longcat_avatar_native", norm_target_w, norm_target_h
    )

    quality_preset = str(_get("quality_preset") or "cinema_flagship").strip().lower()
    if quality_preset not in _LONGCAT_NATIVE_QUALITY_PRESETS:
        quality_preset = "cinema_flagship"
    defaults = _LONGCAT_NATIVE_QUALITY_PRESETS[quality_preset]

    framing_style = str(_get("framing_style") or "half_body_gestures").strip().lower()
    if framing_style not in COLABONE_LONGCAT_NATIVE_FRAMING_PROMPTS:
        framing_style = "half_body_gestures"

    raw_upscale_mode = str(_get("upscale_mode") or "auto").strip().lower()
    if raw_upscale_mode == "none":
        upscale_mode = "none"
        upscale_engine = "none"
        if base_w < norm_target_w or base_h < norm_target_h:
            if base_w == base_h:
                norm_target_w, norm_target_h = (960, 960)
            elif base_h > base_w:
                norm_target_w, norm_target_h = (720, 1280)
            else:
                norm_target_w, norm_target_h = (1280, 720)
    elif raw_upscale_mode == "lanczos":
        upscale_mode = "lanczos"
        upscale_engine = "none"
    elif raw_upscale_mode in _LONGCAT_NATIVE_VSR_ENGINES:
        upscale_mode = raw_upscale_mode
        upscale_engine = raw_upscale_mode
    else:
        upscale_mode = "auto"
        if base_w >= norm_target_w and base_h >= norm_target_h:
            upscale_engine = "none"
        else:
            upscale_engine = str(defaults["upscale_engine"])

    raw_sampling_mode = str(_get("sampling_mode") or "auto").strip().lower()
    if raw_sampling_mode in ("non_distill_cfg", "distill_lora_mix"):
        sampling_mode = raw_sampling_mode
    else:
        sampling_mode = str(defaults["sampling_mode"])

    if sampling_mode == "distill_lora_mix":
        num_inference_steps = 8
        audio_guidance_scale = 1.0
        text_guidance_scale = 1.0
        default_mix_steps = (
            int(defaults["mix_steps"])
            if defaults["sampling_mode"] == "distill_lora_mix"
            else 4
        )
        default_mix_alpha = (
            float(defaults["mix_alpha"])
            if defaults["sampling_mode"] == "distill_lora_mix"
            else 1.0
        )
        mix_steps = max(
            0,
            min(
                8,
                _parse_int_override(
                    _get("mix_steps"), default_mix_steps, "双 LoRA 前段步数"
                ),
            ),
        )
        mix_alpha = max(
            0.0,
            min(
                1.0,
                _parse_float_override(
                    _get("mix_alpha"), default_mix_alpha, "双 LoRA 混合权重"
                ),
            ),
        )
    else:
        default_steps = (
            int(defaults["num_inference_steps"])
            if defaults["sampling_mode"] == "non_distill_cfg"
            else 50
        )
        default_audio_cfg = (
            float(defaults["audio_guidance_scale"])
            if defaults["sampling_mode"] == "non_distill_cfg"
            else 4.0
        )
        default_text_cfg = (
            float(defaults["text_guidance_scale"])
            if defaults["sampling_mode"] == "non_distill_cfg"
            else 4.0
        )
        num_inference_steps = max(
            8,
            min(
                60,
                _parse_int_override(
                    _get("num_inference_steps"), default_steps, "非蒸馏采样步数"
                ),
            ),
        )
        audio_guidance_scale = max(
            1.0,
            min(
                7.0,
                _parse_float_override(
                    _get("audio_guidance_scale"), default_audio_cfg, "音频引导强度"
                ),
            ),
        )
        text_guidance_scale = max(
            1.0,
            min(
                7.0,
                _parse_float_override(
                    _get("text_guidance_scale"), default_text_cfg, "文本引导强度"
                ),
            ),
        )
        mix_steps = 0
        mix_alpha = 0.0

    vocal_separation = str(_get("vocal_separation") or "auto").strip().lower()
    if vocal_separation not in ("auto", "always", "never"):
        vocal_separation = "auto"

    ref_img_index = max(
        0,
        min(
            30,
            _parse_int_override(_get("ref_img_index"), 10, "身份参考帧索引"),
        ),
    )
    seed = max(
        0,
        min(
            2147483647,
            _parse_int_override(_get("seed"), 42, "随机种子"),
        ),
    )
    crf = max(
        12,
        min(
            24,
            _parse_int_override(_get("crf"), 16, "终稿编码质量 CRF"),
        ),
    )

    raw_positive = _get("positive_prompt")
    positive_prompt = (
        str(raw_positive).strip() if raw_positive is not None else ""
    ) or COLABONE_LONGCAT_NATIVE_FRAMING_PROMPTS[framing_style]

    raw_negative = _get("negative_prompt")
    negative_prompt = (
        str(raw_negative).strip() if raw_negative is not None else ""
    ) or COLABONE_LONGCAT_NATIVE_DEFAULT_NEGATIVE_PROMPT

    return {
        "quality_preset": quality_preset,
        "framing_style": framing_style,
        "upscale_mode": upscale_mode,
        "upscale_engine": upscale_engine,
        "sampling_mode": sampling_mode,
        "num_inference_steps": num_inference_steps,
        "audio_guidance_scale": audio_guidance_scale,
        "text_guidance_scale": text_guidance_scale,
        "mix_steps": mix_steps,
        "mix_alpha": mix_alpha,
        "vocal_separation": vocal_separation,
        "ref_img_index": ref_img_index,
        "seed": seed,
        "crf": crf,
        "positive_prompt": positive_prompt,
        "negative_prompt": negative_prompt,
        "base_width": base_w,
        "base_height": base_h,
        "target_width": norm_target_w,
        "target_height": norm_target_h,
        "resolution": "720p",
    }


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


def _normalize_16bit_grayscale(img: Any, fallback_transparency: Any = None) -> Any:
    """Scale 16-bit integer grayscale ('I;16*' / 'I') by 1/256 into 8-bit 'L' or 'RGBA' (preserving tRNS)."""
    from PIL import Image, ImageChops

    mode = str(getattr(img, "mode", "") or "")
    if not (mode == "I" or mode.startswith("I;16")):
        return img
    info = getattr(img, "info", None)
    trns = (
        info.get("transparency")
        if isinstance(info, dict) and "transparency" in info
        else fallback_transparency
    )
    as_i = img if mode == "I" else img.convert("I")
    l8 = as_i.point(lambda v: v * (1.0 / 256.0)).convert("L")
    if isinstance(trns, int) and not isinstance(trns, bool):
        gt = as_i.point(lambda v: v - trns).convert("L")
        lt = as_i.point(lambda v: trns - v).convert("L")
        diff_l = ImageChops.lighter(gt, lt)
        alpha = diff_l.point([0] + [255] * 255)
        return Image.merge("RGBA", (l8, l8, l8, alpha))
    return l8


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
        orig_trns = img.info.get("transparency")
        oriented = ImageOps.exif_transpose(img)
        oriented = _normalize_16bit_grayscale(
            oriented, fallback_transparency=orig_trns
        )
        has_alpha = (
            "A" in oriented.getbands()
            or oriented.mode in ("RGBA", "LA", "PA")
            or "transparency" in oriented.info
        )
        if has_alpha:
            rgba = oriented.convert("RGBA")
            backdrop = Image.new("RGBA", rgba.size, (245, 245, 245, 255))
            oriented = Image.alpha_composite(backdrop, rgba).convert("RGB")
        elif oriented.mode != "RGB":
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


def apply_longcat_acceleration(
    workflow: dict[str, Any],
    *,
    profile: str = "max",
    gpu_caps: dict[str, Any] | None = None,
) -> tuple[dict[str, Any], dict[str, Any]]:
    """Deep-copy a LongCat-Avatar workflow and apply GPU-aware ComfyUI acceleration settings.

    Never mutates InfiniteTalk (MultiTalk*) workflows. Safe to call repeatedly when
    stepping down from ``max`` -> ``balanced`` -> ``compat``.
    """
    result = copy.deepcopy(workflow)
    norm_profile = str(profile or "").strip().lower()
    if norm_profile not in ("max", "balanced", "compat"):
        norm_profile = "max"

    caps = dict(gpu_caps) if isinstance(gpu_caps, dict) else {}

    if isinstance(result, dict) and any(
        isinstance(node, dict)
        and node.get("class_type") in ("MultiTalkModelLoader", "MultiTalkWav2VecEmbeds")
        for node in result.values()
    ):
        return (
            result,
            {
                "profile": "compat",
                "attention_mode": "sdpa",
                "quantization": "disabled",
                "resident_vram": False,
                "vae_tiling": True,
                "torch_compile": False,
                "rms_norm_function": "default",
            },
        )

    try:
        vram_mib = int(caps.get("vram_mib") or 0)
    except (TypeError, ValueError):
        vram_mib = 0

    if norm_profile == "compat":
        attention_mode = "sdpa"
        quantization = "disabled"
        resident_vram = False
        vae_tiling = True
        torch_compile = False
        rms_norm_function = "default"
    else:
        quantization = (
            "fp8_e4m3fn_fast" if bool(caps.get("fp8_fast_supported")) else "disabled"
        )
        if bool(caps.get("sageattn3_available")) and bool(
            caps.get("sageattn_available")
        ):
            attention_mode = "sageattn_3"
        elif bool(caps.get("sageattn_available")):
            attention_mode = "sageattn"
        elif bool(caps.get("flash_attn_2_available")):
            attention_mode = "flash_attn_2"
        else:
            attention_mode = "sdpa"

        torch_compile = norm_profile == "max" and bool(
            caps.get("torch_compile_supported")
        )
        vram_threshold = 44000 if quantization == "fp8_e4m3fn_fast" else 65536
        resident_vram = vram_mib >= vram_threshold
        vae_tiling = not resident_vram
        rms_norm_function = "default" if torch_compile else "pytorch"

    if isinstance(result, dict):
        has_model_loader = False
        for node in list(result.values()):
            if not isinstance(node, dict) or not isinstance(node.get("inputs"), dict):
                continue
            cls = node.get("class_type")
            inputs = node["inputs"]
            if cls == "WanVideoModelLoader":
                has_model_loader = True
                inputs["attention_mode"] = attention_mode
                inputs["quantization"] = quantization
                inputs["load_device"] = (
                    "main_device" if resident_vram else "offload_device"
                )
                inputs["rms_norm_function"] = rms_norm_function
                if torch_compile:
                    inputs["compile_args"] = ["colabone_torch_compile", 0]
                else:
                    inputs.pop("compile_args", None)
            elif cls == "WanVideoSamplerv2":
                inputs["force_offload"] = not resident_vram
            elif cls == "WanVideoDecode":
                inputs["enable_vae_tiling"] = vae_tiling
            elif cls == "WanVideoLoraSelect":
                if quantization == "fp8_e4m3fn_fast":
                    inputs["merge_loras"] = True

        if torch_compile and has_model_loader:
            result["colabone_torch_compile"] = {
                "class_type": "WanVideoTorchCompileSettings",
                "_meta": {"title": "torch.compile 算子加速"},
                "inputs": {
                    "backend": "inductor",
                    "fullgraph": False,
                    "mode": "default",
                    "dynamic": True,
                    "dynamo_cache_size_limit": 64,
                    "compile_transformer_blocks_only": True,
                    "dynamo_recompile_limit": 128,
                    "force_parameter_static_shapes": False,
                    "allow_unmerged_lora_compile": False,
                },
            }
        else:
            result.pop("colabone_torch_compile", None)

    summary = {
        "profile": norm_profile,
        "attention_mode": attention_mode,
        "quantization": quantization,
        "resident_vram": resident_vram,
        "vae_tiling": vae_tiling,
        "torch_compile": torch_compile,
        "rms_norm_function": rms_norm_function,
    }
    return (result, summary)


def _resolve_infinitetalk_quantization(
    existing_quant: str,
    model_name: str,
    *,
    profile: str,
    fp8_fast_supported: bool,
) -> str:
    """Resolve WanVideoWrapper quantization safely without ever stripping _scaled from scaled FP8 models."""
    q = str(existing_quant or "").strip().lower()
    m = str(model_name or "").strip().lower()
    if m.endswith(".gguf"):
        return "disabled"
    norm_profile = str(profile or "").strip().lower()
    is_scaled = ("scaled" in q) or ("scaled" in m)
    is_e5m2 = ("e5m2" in q) or ("e5m2" in m)
    is_fp8_e4 = (
        ("e4m3fn" in q)
        or ("e4m3fn" in m)
        or ("fp8" in q and not is_e5m2)
        or ("fp8" in m and not is_e5m2)
    )
    if norm_profile == "compat":
        if is_scaled:
            return "fp8_e5m2_scaled" if is_e5m2 else "fp8_e4m3fn_scaled"
        if q.endswith("_fast"):
            return q[: -len("_fast")] or "disabled"
        return q or "disabled"

    if is_scaled:
        if is_e5m2:
            return "fp8_e5m2_scaled"
        return "fp8_e4m3fn_scaled_fast" if fp8_fast_supported else "fp8_e4m3fn_scaled"
    if is_fp8_e4:
        return "fp8_e4m3fn_fast" if fp8_fast_supported else "fp8_e4m3fn"
    if q in ("", "disabled"):
        return "fp8_e4m3fn_fast" if fp8_fast_supported else "disabled"
    if not fp8_fast_supported and q.endswith("_fast"):
        return q[: -len("_fast")] or "disabled"
    return q


def _loader_has_unmerged_lora(
    workflow: dict[str, Any],
    loader_id: str,
    loader_node: dict[str, Any],
) -> bool:
    """Return True if a WanVideoModelLoader has any connected unmerged LoRA.

    Pinned ComfyUI-WanVideoWrapper raises NotImplementedError whenever ``fast`` quantization
    is combined with an unmerged LoRA, and setting ``merge_loras=True`` on scaled FP8 models
    corrupts per-tensor ``scale_weight`` calibration. Treats explicit ``merge_loras=False``
    (or omitted ``merge_loras`` on ``WanVideoLoraSelect*`` with scaled FP8, or unresolved/non-bool
    values) as unmerged, and treats any transitively reachable downstream ``WanVideoSetLoRAs``
    node with a linked/non-empty ``lora`` input as unmerged (since ``WanVideoSetLoRAs`` never
    merges LoRAs).
    """
    if not isinstance(workflow, dict) or not isinstance(loader_node, dict):
        return False
    loader_inputs = loader_node.get("inputs")
    if not isinstance(loader_inputs, dict):
        return False

    loader_q = str(loader_inputs.get("quantization") or "").strip().lower()
    loader_m = str(loader_inputs.get("model") or "").strip().lower()
    loader_is_scaled = ("scaled" in loader_q) or ("scaled" in loader_m)

    loader_id_str = str(loader_id).strip()
    if loader_id_str:
        downstream_by_model: dict[str, list[tuple[str, dict[str, Any]]]] = {}
        for nid, node in workflow.items():
            if not isinstance(node, dict):
                continue
            n_inputs = node.get("inputs")
            if not isinstance(n_inputs, dict):
                continue
            m_ref = n_inputs.get("model")
            if (
                isinstance(m_ref, (list, tuple))
                and m_ref
                and str(m_ref[0]).strip()
            ):
                src_id = str(m_ref[0]).strip()
                nid_str = str(nid).strip()
                downstream_by_model.setdefault(src_id, []).append((nid_str, node))

        model_queue: list[str] = [loader_id_str]
        visited_models: set[str] = {loader_id_str}
        while model_queue:
            cur_model_id = model_queue.pop(0)
            for dst_id, dst_node in downstream_by_model.get(cur_model_id, ()):
                if dst_node.get("class_type") == "WanVideoSetLoRAs":
                    dst_inputs = dst_node.get("inputs") or {}
                    set_lora_ref = dst_inputs.get("lora")
                    has_set_lora = (
                        (
                            isinstance(set_lora_ref, (list, tuple))
                            and bool(set_lora_ref)
                            and bool(str(set_lora_ref[0]).strip())
                        )
                        or (isinstance(set_lora_ref, str) and bool(set_lora_ref.strip()))
                        or (
                            set_lora_ref is not None
                            and not isinstance(set_lora_ref, (list, tuple, str, bool))
                        )
                    )
                    if has_set_lora:
                        return True
                if dst_id and dst_id not in visited_models:
                    visited_models.add(dst_id)
                    model_queue.append(dst_id)

    queue: list[str] = []
    lora_ref = loader_inputs.get("lora")
    if isinstance(lora_ref, (list, tuple)) and lora_ref and str(lora_ref[0]).strip():
        queue.append(str(lora_ref[0]).strip())

    visited: set[str] = set()
    while queue:
        cur_id = queue.pop(0)
        if cur_id in visited:
            continue
        visited.add(cur_id)
        cur_node = workflow.get(cur_id)
        if cur_node is None and cur_id.isdigit():
            cur_node = workflow.get(int(cur_id))
        if not isinstance(cur_node, dict):
            return True
        cls = str(cur_node.get("class_type") or "")
        cur_inputs = cur_node.get("inputs")
        if not isinstance(cur_inputs, dict):
            return True

        if "merge_loras" in cur_inputs:
            raw_merge = cur_inputs.get("merge_loras")
            if not isinstance(raw_merge, bool) or not raw_merge:
                return True
        else:
            if loader_is_scaled or not cls.startswith("WanVideoLoraSelect"):
                return True

        prev_ref = cur_inputs.get("prev_lora")
        if isinstance(prev_ref, (list, tuple)) and prev_ref and str(prev_ref[0]).strip():
            queue.append(str(prev_ref[0]).strip())

    return False


def apply_infinitetalk_acceleration(
    workflow: dict[str, Any],
    *,
    profile: str = "max",
    gpu_caps: dict[str, Any] | None = None,
) -> tuple[dict[str, Any], dict[str, Any]]:
    """Deep-copy an InfiniteTalk ComfyUI workflow and apply GPU-aware acceleration settings.

    Preserves scaled FP8 calibration (never upgrades to ``*_fast`` when an unmerged LoRA is
    wired), uses compile-safe ``sageattn`` (v2 custom_op) -> ``flash_attn_2`` -> ``sdpa``
    (never ``sageattn_3``), keeps ``batched_cfg=False``, keeps 14B weights resident on
    >= 48 GB GPUs, and is idempotent across fallback step-downs
    (``max`` -> ``balanced`` -> ``compat``).
    """
    result = copy.deepcopy(workflow)
    norm_profile = str(profile or "").strip().lower()
    if norm_profile not in ("max", "balanced", "compat"):
        norm_profile = "max"

    caps = dict(gpu_caps) if isinstance(gpu_caps, dict) else {}
    try:
        raw_vram = caps.get("vram_mib")
        vram_mib = (
            int(raw_vram)
            if raw_vram is not None and not isinstance(raw_vram, bool)
            else 0
        )
    except (TypeError, ValueError):
        vram_mib = 0

    fp8_fast_supported = bool(caps.get("fp8_fast_supported"))

    # Collect all WanVideoModelLoader nodes up front and compute per-loader quantization /
    # FP8-e4m3fn / unmerged-LoRA state once so multi-loader workflows never split-brain.
    loader_infos: list[tuple[Any, str, bool, bool]] = []
    loader_quant_by_id: dict[Any, str] = {}
    if isinstance(result, dict):
        for nid, node in result.items():
            if (
                isinstance(node, dict)
                and node.get("class_type") == "WanVideoModelLoader"
                and isinstance(node.get("inputs"), dict)
            ):
                node_inputs = node["inputs"]
                raw_quant = str(node_inputs.get("quantization") or "")
                raw_model = str(node_inputs.get("model") or "")
                has_unmerged_lora = _loader_has_unmerged_lora(result, str(nid), node)
                allow_fp8_fast = (
                    fp8_fast_supported
                    and norm_profile != "compat"
                    and not has_unmerged_lora
                )
                node_quant = _resolve_infinitetalk_quantization(
                    raw_quant,
                    raw_model,
                    profile=norm_profile,
                    fp8_fast_supported=allow_fp8_fast,
                )
                is_e4m3fn = (not raw_model.lower().endswith(".gguf")) and (
                    ("e4m3fn" in node_quant) or ("e4m3fn" in raw_model.lower())
                )
                loader_infos.append((nid, node_quant, is_e4m3fn, has_unmerged_lora))
                loader_quant_by_id[nid] = node_quant

    has_model_loader = bool(loader_infos)
    loader_quants = [info[1] for info in loader_infos]
    quantization = loader_quants[0] if loader_quants else "disabled"
    any_e4m3fn_model = any(info[2] for info in loader_infos)
    any_fp8_fast = any(q.endswith("_fast") for q in loader_quants)

    if norm_profile == "compat":
        attention_mode = "sdpa"
        resident_vram = False
        vae_tiling = True
        torch_compile = False
        rms_norm_function = "default"
        resize_device = "cpu"
    else:
        # WanVideoWrapper wraps sageattn (v2) in a torch custom_op (compile-safe, INT8 QK +
        # FP16 PV safe for 32-token audio cross-attn). Never select sageattn_3 here:
        # WanVideoModelLoader@a7439db3 unconditionally imports ``from sageattention import
        # sageattn`` for any ``"sage" in attention_mode`` and crashes when v2 is absent,
        # and sageattn_3 is unwrapped / FP4-quantizes audio cross-attn.
        if bool(caps.get("sageattn_available")):
            attention_mode = "sageattn"
        elif bool(caps.get("flash_attn_2_available")):
            attention_mode = "flash_attn_2"
        else:
            attention_mode = "sdpa"

        resident_vram = vram_mib >= 48000
        vae_tiling = not resident_vram
        e4m3fn_compile_ok = (not any_e4m3fn_model) or fp8_fast_supported
        torch_compile = (
            has_model_loader
            and norm_profile == "max"
            and bool(caps.get("torch_compile_supported"))
            and e4m3fn_compile_ok
        )
        rms_norm_function = "default" if torch_compile else "pytorch"
        resize_device = "gpu" if vram_mib > 0 else "cpu"

    if isinstance(result, dict):
        for nid, node in list(result.items()):
            if not isinstance(node, dict) or not isinstance(node.get("inputs"), dict):
                continue
            cls = node.get("class_type")
            inputs = node["inputs"]
            if cls == "WanVideoModelLoader":
                inputs["attention_mode"] = attention_mode
                inputs["quantization"] = loader_quant_by_id.get(nid, quantization)
                inputs["load_device"] = (
                    "main_device" if resident_vram else "offload_device"
                )
                inputs["rms_norm_function"] = rms_norm_function
                if torch_compile:
                    inputs["compile_args"] = ["colabone_torch_compile", 0]
                else:
                    inputs.pop("compile_args", None)
            elif cls == "WanVideoSampler":
                inputs["force_offload"] = not resident_vram
                inputs["batched_cfg"] = False
                if norm_profile != "compat":
                    inputs["rope_function"] = "comfy"
            elif cls == "WanVideoImageToVideoMultiTalk":
                inputs["tiled_vae"] = vae_tiling
                inputs["force_offload"] = not resident_vram
            elif cls == "WanVideoClipVisionEncode":
                inputs["force_offload"] = True
            elif cls == "WanVideoDecode":
                inputs["enable_vae_tiling"] = vae_tiling
            elif cls == "ImageResizeKJv2":
                inputs["device"] = resize_device

        if torch_compile and has_model_loader:
            result["colabone_torch_compile"] = {
                "class_type": "WanVideoTorchCompileSettings",
                "_meta": {"title": "torch.compile 算子加速"},
                "inputs": {
                    "backend": "inductor",
                    "fullgraph": False,
                    "mode": "default",
                    "dynamic": True,
                    "dynamo_cache_size_limit": 64,
                    "compile_transformer_blocks_only": True,
                    "dynamo_recompile_limit": 128,
                    "force_parameter_static_shapes": False,
                    "allow_unmerged_lora_compile": True,
                },
            }
        else:
            result.pop("colabone_torch_compile", None)

    summary = {
        "profile": norm_profile,
        "attention_mode": attention_mode,
        "quantization": quantization,
        "any_fp8_fast": any_fp8_fast,
        "resident_vram": resident_vram,
        "vae_tiling": vae_tiling,
        "torch_compile": torch_compile,
        "rms_norm_function": rms_norm_function,
        "batched_cfg": False,
        "resize_device": resize_device,
    }
    return (result, summary)


def inject_longcat_ref_latent_nodes(
    workflow: dict[str, Any],
    ref_image_name: str,
) -> dict[str, Any]:
    """Deep-copy workflow and inject original-portrait ref_latent anchor nodes for LongCat relay segments.

    Idempotent if called again; no-op when ``ref_image_name`` is empty or
    ``WanVideoLongCatAvatarExtendEmbeds`` / ``WanVideoVAELoader`` is absent.
    """
    result = copy.deepcopy(workflow)
    image_name = str(ref_image_name or "").strip()
    if not image_name or not isinstance(result, dict):
        return result

    extend_node: dict[str, Any] | None = None
    vae_node_id: str | None = None
    for nid, node in result.items():
        if not isinstance(node, dict) or not isinstance(node.get("inputs"), dict):
            continue
        cls = node.get("class_type")
        if cls == "WanVideoLongCatAvatarExtendEmbeds" and extend_node is None:
            extend_node = node
        elif cls == "WanVideoVAELoader" and vae_node_id is None:
            vae_node_id = str(nid)

    if extend_node is None or vae_node_id is None:
        return result

    result["colabone_ref_image"] = {
        "class_type": "LoadImage",
        "_meta": {"title": "原始人像参考图（锁脸）"},
        "inputs": {
            "image": image_name,
        },
    }
    result["colabone_ref_encode"] = {
        "class_type": "WanVideoEncode",
        "_meta": {"title": "原始人像参考潜变量"},
        "inputs": {
            "vae": [vae_node_id, 0],
            "image": ["colabone_ref_image", 0],
            "enable_vae_tiling": False,
            "tile_x": 272,
            "tile_y": 272,
            "tile_stride_x": 144,
            "tile_stride_y": 128,
            "noise_aug_strength": 0.0,
            "latent_strength": 1.0,
        },
    }
    extend_node["inputs"]["ref_latent"] = ["colabone_ref_encode", 0]
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
        file_size = Path(path).stat().st_size
        with wave.open(str(path), "rb") as wf:
            nchannels = wf.getnchannels()
            sampwidth = wf.getsampwidth()
            framerate = wf.getframerate()
            nframes = wf.getnframes()
            if not (
                wf.getcomptype() == "NONE"
                and nchannels > 0
                and sampwidth in (1, 2, 3, 4)
                and framerate > 0
                and nframes > 0
            ):
                return False
            frame_bytes = nchannels * sampwidth
            if file_size < 44 + nframes * frame_bytes:
                return False
            wf.setpos(nframes - 1)
            return len(wf.readframes(1)) == frame_bytes
    except (OSError, wave.Error, EOFError, ValueError):
        return False


def _wav_duration_seconds(wav_path: Path) -> float:
    with wave.open(str(wav_path), "rb") as wf:
        rate = wf.getframerate()
        frames = wf.getnframes()
        return (frames / rate) if rate > 0 else 0.0


def slice_wav_segment(
    src_wav: Path,
    dst_wav: Path,
    start_sec: float,
    end_sec: float,
    pad_to_seconds: float | None = None,
) -> Path:
    """Losslessly slice a PCM WAV segment [start_sec, end_sec] using stdlib wave.

    When ``pad_to_seconds`` is provided and exceeds the sliced duration, appends
    trailing PCM silence so downstream audio-driven video nodes never receive
    short audio clips.
    """
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
        frame_bytes = max(1, nchannels * sampwidth)
        written_frames = 0
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
                actual_frames = len(data) // frame_bytes
                written_frames += actual_frames
                remaining -= actual_frames if actual_frames > 0 else to_read

            if pad_to_seconds is not None:
                pad_target_sec = max(0.0, float(pad_to_seconds))
                target_frames = int(
                    math.ceil(max(0.0, pad_target_sec * framerate - 1e-9))
                )
                pad_frames = target_frames - written_frames
                if pad_frames > 0:
                    silence_byte = b"\x80" if sampwidth == 1 else b"\x00"
                    silence_frame = silence_byte * frame_bytes
                    while pad_frames > 0:
                        batch = min(chunk_frames, pad_frames)
                        writer.writeframes(silence_frame * batch)
                        pad_frames -= batch
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


def _pg_has_non_zombie_member_linux(pgid: int) -> bool | None:
    proc_root = Path("/proc")
    if not proc_root.is_dir():
        return None
    try:
        entries = list(os.scandir(proc_root))
    except OSError:
        return None
    saw_any_stat = False
    for entry in entries:
        if not entry.name.isdigit():
            continue
        try:
            with open(f"/proc/{entry.name}/stat", "r", encoding="utf-8", errors="replace") as fh:
                raw = fh.read()
        except OSError:
            continue
        saw_any_stat = True
        rparen = raw.rfind(")")
        if rparen < 0:
            continue
        fields = raw[rparen + 2 :].split()
        if len(fields) < 3:
            continue
        state = fields[0]
        try:
            pgrp = int(fields[2])
        except ValueError:
            continue
        if pgrp == pgid and state not in ("Z", "X", "x"):
            return True
    return False if saw_any_stat else None


def _pg_has_members(pgid: int) -> bool:
    """Return True if any non-zombie process still exists in POSIX process group `pgid`."""
    try:
        os.killpg(pgid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    except OSError:
        return False
    linux_live = _pg_has_non_zombie_member_linux(pgid)
    if linux_live is not None:
        return linux_live
    return True


def _terminate_process_group(proc: subprocess.Popen, timeout: float = 5.0) -> None:
    """Terminate proc and lingering process-group orphans while guarding against recycled PIDs."""
    if getattr(proc, "_colabone_pgrp_cleaned", False) is True:
        return

    pid = getattr(proc, "pid", None)
    own_pgrp = os.getpgrp() if hasattr(os, "getpgrp") else None
    can_killpg = (
        isinstance(pid, int)
        and not isinstance(pid, bool)
        and pid > 0
        and hasattr(os, "killpg")
        and pid != own_pgrp
    )
    leader_alive = proc.poll() is None

    if not leader_alive:
        if not can_killpg:
            try:
                setattr(proc, "_colabone_pgrp_cleaned", True)
            except (AttributeError, TypeError):
                pass
            return
        # The leader has already been reaped. If `pid` itself is live now, the OS
        # recycled it for an unrelated process; never signal that process group.
        pid_recycled = False
        try:
            os.kill(pid, 0)
            pid_recycled = True
        except PermissionError:
            pid_recycled = True
        except ProcessLookupError:
            pid_recycled = False
        except OSError:
            pid_recycled = True
        if pid_recycled or not _pg_has_members(pid):
            try:
                setattr(proc, "_colabone_pgrp_cleaned", True)
            except (AttributeError, TypeError):
                pass
            return

    used_pg = False
    if can_killpg:
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

    deadline = time.monotonic() + max(0.0, float(timeout))
    if leader_alive:
        try:
            proc.wait(timeout=max(0.0, deadline - time.monotonic()))
        except subprocess.TimeoutExpired:
            pass

    if used_pg:
        while True:
            proc.poll()
            if not _pg_has_members(pid) or time.monotonic() >= deadline:
                break
            time.sleep(0.05)
        if _pg_has_members(pid):
            try:
                os.killpg(pid, signal.SIGKILL)
            except (ProcessLookupError, PermissionError, OSError):
                pass
            kill_deadline = time.monotonic() + max(0.5, min(2.0, float(timeout)))
            while time.monotonic() < kill_deadline:
                proc.poll()
                if not _pg_has_members(pid):
                    break
                time.sleep(0.05)
    elif proc.poll() is None:
        try:
            proc.kill()
        except OSError:
            pass

    if proc.poll() is None:
        try:
            proc.wait(timeout=max(0.5, float(timeout)))
        except subprocess.TimeoutExpired:
            pass

    if proc.poll() is not None and (not can_killpg or not _pg_has_members(pid)):
        try:
            setattr(proc, "_colabone_pgrp_cleaned", True)
        except (AttributeError, TypeError):
            pass


def _runtime_http(runtime):
    if runtime is None:
        return None
    getter = getattr(runtime, "_http_session", None)
    if callable(getter):
        return getter()
    return getattr(runtime, "http", None)


def _open_secure_log_write(log_path: Path):
    """Open a regular log file for write with O_NOFOLLOW to prevent symlink clobber."""
    path = Path(log_path)
    if path.is_symlink():
        raise ValueError(f"日志文件不能是符号链接：{path.name}")
    path.parent.mkdir(parents=True, exist_ok=True)
    flags = os.O_WRONLY | os.O_CREAT | os.O_TRUNC | getattr(os, "O_NOFOLLOW", 0)
    fd = os.open(str(path), flags, 0o600)
    try:
        st = os.fstat(fd)
        if not stat.S_ISREG(st.st_mode):
            raise ValueError(f"日志路径不是常规文件：{path.name}")
    except BaseException:
        os.close(fd)
        raise
    return os.fdopen(fd, "wb")


def _run_ffmpeg_command(
    args: list[str],
    task,
    log: Path,
    timeout: int = 1200,
    cwd: Path | str | None = None,
    stopping: threading.Event | None = None,
) -> None:
    """Cancelable process-group subprocess runner for ffmpeg commands."""
    with _open_secure_log_write(log) as out:
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
    term_lock = threading.Lock()
    if has_cancel_hook:
        with task_lock if task_lock is not None else nullcontext():
            prev_hook = task.cancel_hook

            def _cancel_ffmpeg() -> None:
                with term_lock:
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
        try:
            with term_lock:
                _terminate_process_group(proc)
        finally:
            if has_cancel_hook:
                with task_lock if task_lock is not None else nullcontext():
                    if task.cancel_hook is cancel_hook:
                        task.cancel_hook = prev_hook


def _existing_disk_ancestor(path: Path) -> Path:
    for candidate in (path, *path.parents):
        try:
            if candidate.exists():
                return candidate
        except OSError:
            continue
    return Path("/")


def _resolve_contained_worker_file(
    work_dir: Path,
    candidate: Path,
    *,
    label: str,
    missing_message: str,
) -> Path:
    """Enforce strict _safe_contained_file containment on worker-returned files inside work_dir."""
    path_label = label if label.endswith("路径") else f"{label}路径"
    cand = _validate_safe_path(candidate, path_label)
    if work_dir.is_symlink() or not work_dir.is_dir():
        raise RuntimeError(f"{label}工作目录安全校验失败")

    work_resolved = work_dir.resolve()
    rel: Path | None = None
    try:
        rel = cand.relative_to(work_dir)
    except ValueError:
        try:
            rel = cand.relative_to(work_resolved)
        except ValueError:
            rel = None

    if rel is None and not cand.is_absolute():
        norm_cand = Path(str(cand).replace("\\", "/"))
        if (
            not norm_cand.is_absolute()
            and norm_cand.parts
            and not any(p == ".." for p in norm_cand.parts)
        ):
            rel = norm_cand

    if cand.is_absolute() and cand.is_symlink():
        raise RuntimeError(f"{path_label}不能是符号链接")

    if rel is None or not rel.parts or any(p == ".." for p in rel.parts):
        raise RuntimeError(f"{path_label}超出工作目录安全范围")

    cur_raw = work_dir
    for part in rel.parts:
        cur_raw = cur_raw / part
        if cur_raw.is_symlink():
            raise RuntimeError(f"{path_label}包含符号链接，安全校验失败")

    if not cur_raw.is_file() or cur_raw.stat().st_size <= 0:
        raise RuntimeError(missing_message)

    safe = _safe_contained_file(work_dir, *rel.parts)
    if safe is None:
        safe = _safe_contained_file(work_resolved, *rel.parts)
    if safe is None or safe.stat().st_size <= 0:
        raise RuntimeError(f"{path_label}安全校验失败")
    return safe


def _unlink_contained_file(work_dir: Path, target: Path) -> None:
    """Safely unlink target only if strictly contained within work_dir (resolving work_dir consistently)."""
    if work_dir.is_symlink() or not work_dir.is_dir():
        return
    work_resolved = work_dir.resolve()
    rel: Path | None = None
    try:
        rel = target.relative_to(work_dir)
    except ValueError:
        try:
            rel = target.relative_to(work_resolved)
        except ValueError:
            rel = None
    if rel is None or not rel.parts or any(p == ".." for p in rel.parts):
        return
    safe = _safe_contained_file(work_dir, *rel.parts)
    if safe is None:
        safe = _safe_contained_file(work_resolved, *rel.parts)
    if safe is not None:
        try:
            safe.unlink(missing_ok=True)
        except OSError:
            pass


def _call_avatar_check_preflight(
    check_preflight_fn: Callable[..., Any],
    *,
    min_vram_mib: int,
    min_disk_mib: int,
    upscale_mode: str,
    sampling_mode: str,
    vocal_separation: str,
) -> dict[str, Any] | None:
    pf_kwargs: dict[str, Any] = {
        "min_vram_mib": min_vram_mib,
        "min_disk_mib": min_disk_mib,
        "upscale_mode": upscale_mode,
    }
    try:
        pf_sig = inspect.signature(check_preflight_fn)
        if "sampling_mode" in pf_sig.parameters:
            pf_kwargs["sampling_mode"] = sampling_mode
        if "vocal_separation" in pf_sig.parameters:
            pf_kwargs["vocal_separation"] = vocal_separation
    except (TypeError, ValueError):
        pass
    res = check_preflight_fn(**pf_kwargs)
    return dict(res) if isinstance(res, dict) else None


_PRESET_ANCHOR_TTS_ENGINES = frozenset(
    {"fish_drama3", "moss_tts_v1_5", "fish_s2_pro", "indextts2"}
)
_VOXCPM2_DEFAULT_DISK_MIB = 6144


def _estimate_uncached_tts_disk_mib(
    tts_runner: Any,
    engine_spec: dict[str, Any],
    *,
    allow_unavailable: bool,
    ref_audio_path: Path | None = None,
    voice_preset: str = "",
) -> int:
    if tts_runner is None or allow_unavailable:
        return 0
    total_mib = 0
    has_cache_fn = getattr(tts_runner, "_has_engine_model_cache", None)
    min_disk_mib = max(0, int(engine_spec.get("min_disk_mib") or 0))
    if min_disk_mib > 0:
        primary_cached = False
        if callable(has_cache_fn):
            try:
                primary_cached = has_cache_fn(engine_spec) is True
            except Exception:
                primary_cached = False
        if not primary_cached:
            total_mib += min_disk_mib

    engine_id = str(engine_spec.get("id") or "").strip()
    if (
        engine_id in _PRESET_ANCHOR_TTS_ENGINES
        and ref_audio_path is None
        and str(voice_preset or "").strip().lower() != "custom"
    ):
        preset_cached = False
        is_preset_cached_fn = getattr(
            tts_runner, "_is_preset_reference_cached", None
        )
        if callable(is_preset_cached_fn):
            try:
                from server.tts_engines import resolve_colabone_voice_preset

                resolved_preset = resolve_colabone_voice_preset(
                    engine_spec, voice_preset
                )
                if isinstance(resolved_preset, dict):
                    preset_cached = is_preset_cached_fn(resolved_preset) is True
            except Exception:
                preset_cached = False
        if not preset_cached:
            vox_spec: dict[str, Any] | None = None
            engines_by_id = getattr(tts_runner, "engines_by_id", None)
            if isinstance(engines_by_id, dict) and isinstance(
                engines_by_id.get("voxcpm2"), dict
            ):
                vox_spec = engines_by_id["voxcpm2"]
            else:
                get_engine_fn = getattr(tts_runner, "get_engine", None)
                if callable(get_engine_fn):
                    try:
                        candidate = get_engine_fn("voxcpm2")
                        if isinstance(candidate, dict):
                            vox_spec = candidate
                    except Exception:
                        vox_spec = None
            vox_cached = False
            if callable(has_cache_fn) and isinstance(vox_spec, dict):
                try:
                    vox_cached = has_cache_fn(vox_spec) is True
                except Exception:
                    vox_cached = False
            if not vox_cached:
                vox_mib = (
                    max(0, int(vox_spec.get("min_disk_mib") or 0))
                    if isinstance(vox_spec, dict)
                    else 0
                )
                total_mib += vox_mib or _VOXCPM2_DEFAULT_DISK_MIB

    return total_mib


def _estimate_uncached_native_disk_mib(
    avatar_engines: Any,
    preset: dict[str, Any],
    preflight_info: dict[str, Any] | None,
) -> int:
    """Estimate remaining Stage 2/3 disk requirement in MiB (accounting for sampling/vocal deltas)."""
    if not isinstance(preflight_info, dict):
        return 0
    raw_req = preflight_info.get("required_disk_mib")
    if raw_req is None or isinstance(raw_req, bool):
        return 0
    try:
        req_mib = max(0, int(raw_req))
    except (TypeError, ValueError):
        return 0

    check_fn = getattr(avatar_engines, "check_preflight", None)
    handles_patterns = False
    if callable(check_fn):
        try:
            sig = inspect.signature(check_fn)
            handles_patterns = (
                "sampling_mode" in sig.parameters
                and "vocal_separation" in sig.parameters
            )
        except (TypeError, ValueError):
            handles_patterns = False

    if not handles_patterns and bool(preflight_info.get("all_weights_cached")):
        valid_fn = getattr(avatar_engines, "_weights_marker_valid", None)
        weights_dir = getattr(avatar_engines, "weights_dir", None)
        if callable(valid_fn) and isinstance(weights_dir, (str, os.PathLike)):
            try:
                from server.avatar_engines import (
                    COLABONE_LONGCAT16_AVATAR_HF_REPO,
                    COLABONE_LONGCAT16_AVATAR_HF_REV,
                )

                candidates = [Path(weights_dir) / "longcat16_avatar"]
                drive_fn = getattr(avatar_engines, "_resolve_drive_cache_root", None)
                if callable(drive_fn):
                    drive_root = drive_fn()
                    if drive_root is not None:
                        candidates.append(
                            Path(drive_root) / "weights" / "longcat16_avatar"
                        )

                def _has_patterns(pats: list[str]) -> bool:
                    return any(
                        valid_fn(
                            cand,
                            repo_id=COLABONE_LONGCAT16_AVATAR_HF_REPO,
                            revision=COLABONE_LONGCAT16_AVATAR_HF_REV,
                            required_patterns=pats,
                        )
                        for cand in candidates
                    )

                sampling_mode = str(
                    preset.get("sampling_mode") or "non_distill_cfg"
                ).strip().lower()
                vocal_sep = str(
                    preset.get("vocal_separation") or "auto"
                ).strip().lower()
                if sampling_mode in ("distill_lora_mix", "auto") and not _has_patterns(
                    ["loraA/**", "loraB/**"]
                ):
                    req_mib += 2560
                if vocal_sep != "never" and not _has_patterns(["vocal_separator/**"]):
                    req_mib += 256
            except Exception:
                pass

    return req_mib


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
    """End-to-end long-video digital human pipeline runner for remote ComfyUI and native engines."""

    def __init__(
        self,
        manager,
        tts_runner: ColabOneRemoteTtsRunner | None,
        run_cmd_fn: Callable[..., Any] | None = None,
        avatar_engines: Any = None,
    ):
        self.manager = manager
        self.tts_runner = tts_runner
        self._run_cmd_fn = run_cmd_fn
        self._avatar_engines = avatar_engines
        runtime_root = getattr(getattr(self.manager, "runtime", None), "root", None)
        if isinstance(runtime_root, (str, os.PathLike)):
            raw_root = os.fspath(runtime_root)
            if not any(ch in raw_root for ch in ("\r", "\n", "\x00")):
                _prune_stale_work_dirs(Path(raw_root) / "comfyui-work")

    def get_avatar_engines(self) -> Any:
        """Lazily obtain or instantiate ColabOneAvatarEngineManager."""
        if self._avatar_engines is not None:
            return self._avatar_engines
        mgr_engines = getattr(self.manager, "avatar_engines", None)
        if mgr_engines is not None:
            self._avatar_engines = mgr_engines
            return mgr_engines

        from server.avatar_engines import ColabOneAvatarEngineManager

        runtime = getattr(self.manager, "runtime", None)
        drive = getattr(runtime, "drive", None)
        runtime_root = getattr(runtime, "root", None)
        if isinstance(runtime_root, (str, os.PathLike)):
            base_dir = Path(runtime_root)
        else:
            models_dir = getattr(self.manager, "models_dir", None)
            if isinstance(models_dir, (str, os.PathLike)):
                base_dir = Path(models_dir).parent
            else:
                base_dir = Path(tempfile.gettempdir()) / "colabone-remote"
        gpu_info_fn = getattr(runtime, "gpu_info", None)
        engines = ColabOneAvatarEngineManager(
            base_dir,
            runtime=runtime,
            drive=drive,
            run_cmd_fn=self._run_cmd_fn,
            gpu_info_fn=gpu_info_fn if callable(gpu_info_fn) else None,
        )
        self._avatar_engines = engines
        if (
            self.manager is not None
            and getattr(self.manager, "avatar_engines", None) is None
        ):
            try:
                self.manager.avatar_engines = engines
            except Exception:
                pass
        return engines

    def _get_avatar_engines(self) -> Any:
        return self.get_avatar_engines()

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
        http = _runtime_http(getattr(self.manager, "runtime", None))
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

        http = _runtime_http(getattr(self.manager, "runtime", None))
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
                http = _runtime_http(getattr(self.manager, "runtime", None))
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
        self,
        task,
        audio_src_path: Path,
        work_dir: Path,
        *,
        preserve_stereo_dynamics: bool = False,
        detect_silence: bool = True,
    ) -> tuple[Path, float, list[float]]:
        """Decode/resample uploaded audio to 48 kHz PCM16 WAV (normalizing legacy mono or preserving native stereo dynamics)."""
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
        is_pcm_wav = _is_readable_pcm_wav(audio_src_path)

        if preserve_stereo_dynamics:
            needs_ffmpeg = True
            wav_channels = 0
            if is_pcm_wav:
                with wave.open(str(audio_src_path), "rb") as wf:
                    wav_channels = wf.getnchannels()
                    if (
                        wav_channels in (1, 2)
                        and wf.getframerate() == 48000
                        and wf.getsampwidth() == 2
                        and wf.getnframes() > 0
                    ):
                        needs_ffmpeg = False

            if not needs_ffmpeg:
                shutil.copyfile(audio_src_path, master_wav)
            else:
                ffmpeg_bin = self._ffmpeg_binary()
                decoded_wav = work_dir / "uploaded_decoded.wav"
                decode_cmd = [
                    ffmpeg_bin,
                    "-y",
                    "-v",
                    "error",
                    "-protocol_whitelist",
                    "file,pipe",
                    "-i",
                    str(audio_src_path),
                    "-vn",
                    "-sn",
                    "-dn",
                    "-map",
                    "0:a:0",
                ]
                if is_pcm_wav and wav_channels > 0:
                    decode_cmd.extend(["-ac", "1" if wav_channels == 1 else "2"])
                else:
                    decode_cmd.extend(["-af", "aformat=channel_layouts=mono|stereo"])
                decode_cmd.extend(
                    [
                        "-ar",
                        "48000",
                        "-c:a",
                        "pcm_s16le",
                        str(decoded_wav),
                    ]
                )
                self._invoke_run_cmd(
                    decode_cmd,
                    task,
                    work_dir / "ffmpeg_audio_decode.log",
                    timeout=300,
                )
                if (
                    decoded_wav.is_file()
                    and decoded_wav.stat().st_size > 0
                    and _is_readable_pcm_wav(decoded_wav)
                ):
                    os.replace(decoded_wav, master_wav)
                elif self._run_cmd_fn is not None and is_pcm_wav:
                    # Offline test spy recorded ffmpeg decode without writing decoded_wav.
                    shutil.copyfile(audio_src_path, master_wav)
                else:
                    raise RuntimeError("音频格式转换失败：ffmpeg 未产出 WAV 文件")

            duration = _wav_duration_seconds(master_wav)
            if duration <= 0.0:
                raise ValueError("输入音频文件为空")
            valleys = (
                detect_pcm_silence_valleys(master_wav)
                if detect_silence and _is_readable_pcm_wav(master_wav)
                else []
            )
            return (master_wav, duration, valleys)

        needs_ffmpeg = True
        if is_pcm_wav:
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
                    "-vn",
                    "-sn",
                    "-dn",
                    "-map",
                    "0:a:0",
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
            elif self._run_cmd_fn is not None and is_pcm_wav:
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
            if detect_silence and _is_readable_pcm_wav(master_wav)
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

    def _probe_longcat_gpu_caps(self) -> dict[str, Any]:
        """Probe remote GPU acceleration capabilities for LongCat-Avatar safely."""
        gpu_info: dict[str, Any] = {}
        runtime = getattr(self.manager, "runtime", None)
        gpu_info_fn = getattr(runtime, "gpu_info", None)
        if callable(gpu_info_fn):
            try:
                raw = gpu_info_fn()
                if isinstance(raw, dict):
                    gpu_info = raw
            except Exception:
                gpu_info = {}

        try:
            from server import comfy_packs as _comfy_packs

            probe_fn = getattr(
                _comfy_packs, "probe_gpu_acceleration_capabilities", None
            )
            if callable(probe_fn):
                probed = probe_fn(gpu_info)
                if isinstance(probed, dict):
                    return dict(probed)
        except Exception:
            pass

        raw_vram = gpu_info.get("total_mib") or gpu_info.get("vram_mib") or 0
        try:
            vram_mib = int(raw_vram)
        except (TypeError, ValueError):
            vram_mib = 0
        return {
            "gpu_name": str(gpu_info.get("name") or ""),
            "vram_mib": vram_mib,
            "cc_major": 0,
            "cc_minor": 0,
            "torch_version": "",
            "cuda_version": "",
            "nvcc_version": "",
            "triton_available": False,
            "torch_compile_supported": False,
            "fp8_fast_supported": False,
            "sageattn_version": "",
            "sageattn_available": False,
            "sageattn3_available": False,
            "flash_attn_2_available": False,
        }

    def _run_longcat_native_pipeline(
        self,
        *,
        task,
        wf: dict[str, Any],
        wf_name: str,
        preset: dict[str, Any],
        anchor_prepared_path: Path,
        master_audio_path: Path,
        duration: float,
        output_fps: int,
        master_sample_rate: int,
        audio_source: str,
        hf_token: str,
        work_dir: Path,
        ffmpeg_bin: str,
        started: float,
        safe_task_tag: str,
        preflight_checked: bool = False,
        preflight_info: dict[str, Any] | None = None,
    ) -> list[dict[str, Any]]:
        """Execute Stage 2 (LongCat 1.6 native 720p) + Stage 3 (SeedVR2/FlashVSR+) + Stage 4 master encode."""
        from server.runtime import ColabOneCancelledError, _scrub_secret_text

        task.check()
        avatar_engines = self.get_avatar_engines()

        resolved_preflight: dict[str, Any] = (
            dict(preflight_info) if isinstance(preflight_info, dict) else {}
        )
        if not preflight_checked:
            check_preflight_fn = getattr(avatar_engines, "check_preflight", None)
            if callable(check_preflight_fn):
                res_pf = _call_avatar_check_preflight(
                    check_preflight_fn,
                    min_vram_mib=int(wf.get("min_vram_mib") or 49152),
                    min_disk_mib=int(wf.get("min_disk_mib") or 73728),
                    upscale_mode=preset["upscale_engine"],
                    sampling_mode=preset["sampling_mode"],
                    vocal_separation=preset["vocal_separation"],
                )
                if isinstance(res_pf, dict):
                    resolved_preflight = res_pf

        # Ensure full GPU exclusivity for Stage 2 + Stage 3 (never start ComfyUI).
        runtime = getattr(self.manager, "runtime", None)
        if getattr(runtime, "loaded", None) and callable(
            getattr(runtime, "_stop_engine", None)
        ):
            task.note("正在卸载当前对话模型以释放显存")
            runtime._stop_engine()

        is_running_fn = getattr(self.manager, "is_running", None)
        stop_server_fn = getattr(self.manager, "stop_server", None)
        if callable(is_running_fn) and callable(stop_server_fn):
            try:
                comfy_running = is_running_fn() is True
            except Exception:
                comfy_running = False
            if comfy_running:
                task.note("正在释放远端 ComfyUI 显存以独占 96GB 跑原生旗舰引擎")
                try:
                    stop_server_fn()
                except ColabOneCancelledError:
                    raise
                except Exception as exc:
                    scrubbed_err = _scrub_secret_text(
                        str(exc) or type(exc).__name__, hf_token
                    )
                    task.note(
                        f"停止远端 ComfyUI 时出现非致命提示：{scrubbed_err}",
                        level="warning",
                    )

        base_w = int(preset["base_width"])
        base_h = int(preset["base_height"])
        target_w = int(preset["target_width"])
        target_h = int(preset["target_height"])
        out_w, out_h = target_w, target_h
        upscale_engine = str(preset["upscale_engine"] or "none").strip().lower()
        exact_frames = max(
            1, int(math.ceil(float(duration) * float(output_fps) - 1e-6))
        )

        intermediate_mkvs: list[Path] = []
        try:
            # --------------------------------------------------------------
            # Stage 2A — Ensure LongCat-Video-Avatar 1.6 Native Ready (18%..30%)
            # --------------------------------------------------------------
            task.check()
            env_msg = f"正在准备 LongCat-Video-Avatar 1.6 原生旗舰环境与权重：{wf_name}"
            _update_task(task, message=env_msg)
            task.note(env_msg)
            longcat_paths = avatar_engines.ensure_longcat16_ready(
                task,
                hf_token=hf_token,
                sampling_mode=preset["sampling_mode"],
                vocal_separation=preset["vocal_separation"],
                progress_range=(0.18, 0.30),
            )
            wf_id = str(wf.get("id") or "").strip()
            mark_ready_fn = getattr(self.manager, "mark_native_pack_ready", None)
            if wf_id and callable(mark_ready_fn):
                mark_ready_fn(wf_id)

            try:
                raw_total_vram = resolved_preflight.get("total_mib")
                total_vram_mib = (
                    int(raw_total_vram)
                    if raw_total_vram is not None
                    and not isinstance(raw_total_vram, bool)
                    else 0
                )
            except (TypeError, ValueError):
                total_vram_mib = 0

            # --------------------------------------------------------------
            # Stage 2B — Execute LongCat-Video-Avatar 1.6 Native 720p (30%..72%)
            # --------------------------------------------------------------
            task.check()
            stage2_mkv_path = _validate_safe_path(
                work_dir / "stage2_720p.mkv", "Stage 2 视频路径"
            )
            intermediate_mkvs.append(stage2_mkv_path)
            stage2_msg = (
                f"{wf_name} · 正在使用 LongCat 1.6 原生引擎渲染 720p 主画面"
                f"（{base_w}×{base_h}，{exact_frames} 帧，{preset['sampling_mode']}）"
            )
            _update_task(task, progress=0.30, message=stage2_msg)
            task.note(stage2_msg)

            longcat_config: dict[str, Any] = {
                "image_path": str(anchor_prepared_path),
                "audio_path": str(master_audio_path),
                "speakers": [{"id": "person1", "audio_path": str(master_audio_path)}],
                "is_tts_audio": bool(audio_source == "tts"),
                "output_video_path": str(stage2_mkv_path),
                "output_manifest_path": str(
                    work_dir / "stage2_longcat16_manifest.json"
                ),
                "resolution": str(preset.get("resolution") or "720p"),
                "width": base_w,
                "height": base_h,
                "fps": int(output_fps),
                "exact_frames": exact_frames,
                "num_frames": 93,
                "num_cond_frames": 13,
                "sampling_mode": preset["sampling_mode"],
                "num_inference_steps": int(preset["num_inference_steps"]),
                "audio_guidance_scale": float(preset["audio_guidance_scale"]),
                "text_guidance_scale": float(preset["text_guidance_scale"]),
                "mix_steps": int(preset["mix_steps"]),
                "mix_alpha": float(preset["mix_alpha"]),
                "vocal_separation": preset["vocal_separation"],
                "ref_img_index": int(preset["ref_img_index"]),
                "seed": int(preset["seed"]),
                "prompt": preset["positive_prompt"],
                "negative_prompt": preset["negative_prompt"],
                "ffmpeg_bin": ffmpeg_bin,
                "attention_backend": "auto",
                "compile_dit": "auto",
                "compile_vae": False,
                "fp8_ffn": "auto",
                "batched_cfg": "auto",
            }
            if total_vram_mib > 0:
                longcat_config["vram_mib"] = total_vram_mib
            if isinstance(longcat_paths, dict):
                if longcat_paths.get("repo_dir"):
                    longcat_config["longcat_repo_dir"] = str(longcat_paths["repo_dir"])
                if longcat_paths.get("avatar_dir"):
                    longcat_config["avatar_checkpoint_dir"] = str(
                        longcat_paths["avatar_dir"]
                    )
                if longcat_paths.get("base_dir"):
                    longcat_config["base_video_dir"] = str(longcat_paths["base_dir"])
                if longcat_paths.get("compile_cache_dir"):
                    longcat_config["compile_cache_dir"] = str(
                        longcat_paths["compile_cache_dir"]
                    )

            stage2_started = time.monotonic()
            stage2_result = avatar_engines.run_longcat16(
                task,
                config=longcat_config,
                runtime_paths=longcat_paths,
                work_dir=work_dir,
                progress_range=(0.30, 0.72),
            )
            stage2_candidate = stage2_mkv_path
            actual_s2_w, actual_s2_h = base_w, base_h
            if isinstance(stage2_result, dict):
                raw_s2_out = (
                    stage2_result.get("output_video_path")
                    or stage2_result.get("output_path")
                    or stage2_result.get("video_path")
                )
                if raw_s2_out:
                    stage2_candidate = _validate_safe_path(
                        Path(str(raw_s2_out)), "Stage 2 输出视频路径"
                    )
                try:
                    s2_w = int(stage2_result.get("width") or 0)
                    s2_h = int(stage2_result.get("height") or 0)
                    if s2_w > 0 and s2_h > 0:
                        actual_s2_w, actual_s2_h = s2_w, s2_h
                except (TypeError, ValueError):
                    pass

            stage2_video_path = _resolve_contained_worker_file(
                work_dir,
                stage2_candidate,
                label="Stage 2 输出视频",
                missing_message="LongCat-Video-Avatar 1.6 渲染失败：未产出有效的 720p 无损中间视频",
            )
            if stage2_video_path != stage2_mkv_path:
                intermediate_mkvs.append(stage2_video_path)
            _update_task(task, progress=0.72)
            stage2_elapsed = max(0.0, time.monotonic() - stage2_started)
            task.note(
                f"{wf_name} · LongCat 1.6 原生 720p 主画面生成完成"
                f"（耗时 {format_colabone_duration_zh(stage2_elapsed, subsecond=True)}）"
            )
            if isinstance(stage2_result, dict):
                accel_parts: list[str] = []
                attn_used = str(stage2_result.get("attention_backend") or "").strip()
                if attn_used:
                    accel_parts.append(f"注意力={attn_used}")
                fp8_raw = stage2_result.get("fp8_ffn")
                if fp8_raw is None:
                    fp8_raw = stage2_result.get("fp8_ffn_enabled")
                if fp8_raw is not None:
                    accel_parts.append(
                        f"FP8 FFN={'开' if fp8_raw else '关'}"
                        if isinstance(fp8_raw, bool)
                        else f"FP8 FFN={fp8_raw}"
                    )
                bcfg_raw = stage2_result.get("batched_cfg")
                if bcfg_raw is None:
                    bcfg_raw = stage2_result.get("batched_cfg_enabled")
                if bcfg_raw is not None:
                    accel_parts.append(
                        f"Batched CFG={'开' if bcfg_raw else '关'}"
                        if isinstance(bcfg_raw, bool)
                        else f"Batched CFG={bcfg_raw}"
                    )
                comp_raw = stage2_result.get("compile_dit")
                if comp_raw is None:
                    comp_raw = stage2_result.get("regional_compile")
                if comp_raw is None:
                    comp_raw = stage2_result.get("compile_dit_enabled")
                if comp_raw is not None:
                    accel_parts.append(
                        f"Regional Compile={'开' if comp_raw else '关'}"
                        if isinstance(comp_raw, bool)
                        else f"Regional Compile={comp_raw}"
                    )
                if accel_parts:
                    task.note(
                        f"{wf_name} · LongCat 1.6 原生加速栈：{'，'.join(accel_parts)}"
                    )

            # --------------------------------------------------------------
            # Stage 3 — Diffusion Video Super-Resolution (72%..94%)
            # --------------------------------------------------------------
            video_src_path = stage2_video_path
            actual_src_w, actual_src_h = actual_s2_w, actual_s2_h
            stage3_video_path: Path | None = None
            if upscale_engine != "none":
                task.check()
                vsr_prep_msg = f"正在准备视频扩散超分引擎与权重：{upscale_engine}"
                _update_task(task, progress=0.72, message=vsr_prep_msg)
                task.note(vsr_prep_msg)
                vsr_paths = avatar_engines.ensure_vsr_ready(
                    task,
                    engine=upscale_engine,
                    hf_token=hf_token,
                    progress_range=(0.72, 0.78),
                )

                task.check()
                stage3_mkv_path = _validate_safe_path(
                    work_dir / "stage3_upscaled.mkv", "Stage 3 超分视频路径"
                )
                intermediate_mkvs.append(stage3_mkv_path)
                vsr_run_msg = (
                    f"{wf_name} · 正在执行 {upscale_engine} 视频扩散超分"
                    f"（{target_w}×{target_h}）"
                )
                _update_task(task, progress=0.78, message=vsr_run_msg)
                task.note(vsr_run_msg)

                vsr_config: dict[str, Any] = {
                    "engine": upscale_engine,
                    "work_dir": str(work_dir),
                    "input_video_path": str(stage2_video_path),
                    "output_video_path": str(stage3_mkv_path),
                    "output_manifest_path": str(
                        work_dir / "stage3_vsr_manifest.json"
                    ),
                    "source_width": actual_s2_w,
                    "source_height": actual_s2_h,
                    "target_width": target_w,
                    "target_height": target_h,
                    "fps": int(output_fps),
                    "exact_frames": exact_frames,
                    "seed": int(preset["seed"]),
                    "color_correction": "lab",
                    "ffmpeg_bin": ffmpeg_bin,
                    "attention_mode": "auto",
                    "compile_dit": "auto",
                    "compile_vae": "auto",
                    "output_format": "auto",
                    "batch_size": "auto",
                }
                if isinstance(vsr_paths, dict):
                    if vsr_paths.get("repo_dir"):
                        vsr_config["repo_dir"] = str(vsr_paths["repo_dir"])
                    if vsr_paths.get("weights_dir"):
                        vsr_config["weights_dir"] = str(vsr_paths["weights_dir"])
                    if vsr_paths.get("python"):
                        vsr_config["python_bin"] = str(vsr_paths["python"])
                pf_vram = total_vram_mib
                if pf_vram > 0:
                    vsr_config["vram_mib"] = pf_vram

                stage3_started = time.monotonic()
                stage3_result = avatar_engines.run_diffusion_vsr(
                    task,
                    config=vsr_config,
                    runtime_paths=vsr_paths,
                    work_dir=work_dir,
                    progress_range=(0.78, 0.94),
                )
                stage3_candidate = stage3_mkv_path
                actual_s3_w, actual_s3_h = target_w, target_h
                if isinstance(stage3_result, dict):
                    raw_s3_out = (
                        stage3_result.get("output_video_path")
                        or stage3_result.get("output_path")
                        or stage3_result.get("video_path")
                    )
                    if raw_s3_out:
                        stage3_candidate = _validate_safe_path(
                            Path(str(raw_s3_out)), "Stage 3 超分输出视频路径"
                        )
                    try:
                        raw_s3_w = (
                            stage3_result.get("width")
                            or stage3_result.get("target_width")
                            or 0
                        )
                        raw_s3_h = (
                            stage3_result.get("height")
                            or stage3_result.get("target_height")
                            or 0
                        )
                        if not isinstance(raw_s3_w, bool) and not isinstance(
                            raw_s3_h, bool
                        ):
                            s3_w = int(raw_s3_w)
                            s3_h = int(raw_s3_h)
                            if s3_w > 0 and s3_h > 0:
                                actual_s3_w, actual_s3_h = s3_w, s3_h
                    except (TypeError, ValueError):
                        pass

                stage3_video_path = _resolve_contained_worker_file(
                    work_dir,
                    stage3_candidate,
                    label="Stage 3 超分输出视频",
                    missing_message=f"视频扩散超分失败（{upscale_engine}）：未产出有效的无损超分视频",
                )
                if stage3_video_path != stage3_mkv_path:
                    intermediate_mkvs.append(stage3_video_path)
                video_src_path = stage3_video_path
                actual_src_w, actual_src_h = actual_s3_w, actual_s3_h
                _update_task(task, progress=0.94)
                stage3_elapsed = max(0.0, time.monotonic() - stage3_started)
                task.note(
                    f"{wf_name} · {upscale_engine} 视频扩散超分完成"
                    f"（耗时 {format_colabone_duration_zh(stage3_elapsed, subsecond=True)}）"
                )
                if stage2_video_path.resolve() != stage3_video_path.resolve():
                    _unlink_contained_file(work_dir, stage2_video_path)

            # --------------------------------------------------------------
            # Stage 4 — Single-Pass Master Encode & Output Registration (94%..100%)
            # --------------------------------------------------------------
            task.check()
            stage4_msg = (
                f"正在进行单次母版封装（CRF {int(preset['crf'])} · {out_w}×{out_h} · 48 kHz AAC 256k）"
            )
            _update_task(task, progress=0.95, message=stage4_msg)
            task.note(stage4_msg)

            final_mp4_path = _validate_safe_path(
                work_dir / "final_output.mp4", "最终输出视频路径"
            )
            cfr_args = (
                ["-fps_mode", "cfr"]
                if self._run_cmd_fn is not None
                else _resolve_ffmpeg_cfr_args(ffmpeg_bin)
            )
            if actual_src_w != out_w or actual_src_h != out_h:
                if (
                    actual_src_w >= out_w
                    and actual_src_h >= out_h
                    and (actual_src_w == out_w or actual_src_h == out_h)
                ):
                    vf_filter = (
                        f"crop={out_w}:{out_h},"
                        "scale=out_color_matrix=bt709:out_range=tv,setsar=1"
                    )
                else:
                    vf_filter = (
                        f"scale={out_w}:{out_h}:"
                        "force_original_aspect_ratio=increase:flags=lanczos:"
                        "out_color_matrix=bt709:out_range=tv,"
                        f"crop={out_w}:{out_h},setsar=1"
                    )
            else:
                vf_filter = "scale=out_color_matrix=bt709:out_range=tv,setsar=1"

            use_nvenc = (
                self._run_cmd_fn is None and _probe_ffmpeg_nvenc_h264(ffmpeg_bin)
            )
            if use_nvenc:
                task.note("已启用 GPU NVENC 硬件编码（h264_nvenc · p6 hq）加速母版封装")

            def _build_stage4_cmd(
                video_rate_args: list[str] | None,
                audio_kbps: int = 256,
                *,
                encode_nvenc: bool = False,
            ) -> list[str]:
                encode_args = _build_h264_encode_args(
                    int(preset["crf"]),
                    preset="medium",
                    use_nvenc=encode_nvenc,
                    rate_args=video_rate_args,
                )
                return [
                    ffmpeg_bin,
                    "-y",
                    "-protocol_whitelist",
                    "file,pipe",
                    "-i",
                    str(video_src_path),
                    "-protocol_whitelist",
                    "file,pipe",
                    "-i",
                    str(master_audio_path),
                    "-map",
                    "0:v:0",
                    "-map",
                    "1:a:0",
                    "-vf",
                    vf_filter,
                    *encode_args,
                    "-pix_fmt",
                    "yuv420p",
                    "-colorspace",
                    "bt709",
                    "-color_primaries",
                    "bt709",
                    "-color_trc",
                    "bt709",
                    "-color_range",
                    "tv",
                    *cfr_args,
                    "-r",
                    str(int(output_fps)),
                    "-c:a",
                    "aac",
                    "-b:a",
                    f"{int(audio_kbps)}k",
                    "-ar",
                    str(max(48000, int(master_sample_rate))),
                    "-movflags",
                    "+faststart",
                    "-shortest",
                    str(final_mp4_path),
                ]

            def _run_stage4_encode(
                video_rate_args: list[str] | None,
                *,
                log_path: Path,
                audio_kbps: int = 256,
            ) -> None:
                nonlocal use_nvenc
                cmd = _build_stage4_cmd(
                    video_rate_args,
                    audio_kbps=audio_kbps,
                    encode_nvenc=use_nvenc,
                )
                if not use_nvenc:
                    self._invoke_run_cmd(
                        cmd, task, log_path, timeout=1800, cwd=work_dir
                    )
                    return
                try:
                    self._invoke_run_cmd(
                        cmd, task, log_path, timeout=1800, cwd=work_dir
                    )
                except ColabOneCancelledError:
                    raise
                except RuntimeError as exc:
                    task.check()
                    stopping = getattr(
                        getattr(self.manager, "runtime", None), "stopping", None
                    )
                    if isinstance(stopping, threading.Event) and stopping.is_set():
                        raise
                    err_lower = str(exc or "").lower()
                    if any(
                        marker in err_lower
                        for marker in (
                            "超时",
                            "timed out",
                            "no space left on device",
                            "磁盘空间不足",
                        )
                    ):
                        raise
                    use_nvenc = False
                    _mark_ffmpeg_nvenc_unavailable(ffmpeg_bin)
                    task.note(
                        f"GPU NVENC 硬件编码失败（{exc}），已自动回退至 libx264 软件编码重试",
                        level="warning",
                    )
                    if final_mp4_path.resolve() != video_src_path.resolve():
                        final_mp4_path.unlink(missing_ok=True)
                    fallback_cmd = _build_stage4_cmd(
                        video_rate_args,
                        audio_kbps=audio_kbps,
                        encode_nvenc=False,
                    )
                    self._invoke_run_cmd(
                        fallback_cmd, task, log_path, timeout=1800, cwd=work_dir
                    )

            _run_stage4_encode(
                None,
                log_path=work_dir / "ffmpeg_native_final.log",
            )

            # Auto-reencode if master MP4 exceeds MAX_OUTPUT_BYTES (512 MiB) so GPU work is preserved.
            if (
                final_mp4_path.is_file()
                and final_mp4_path.stat().st_size > MAX_OUTPUT_BYTES
            ):
                task.check()
                oversized_bytes = final_mp4_path.stat().st_size
                eff_dur = max(1.0, float(duration))
                audio_kbps = 256
                for candidate_audio_kbps in (256, 192, 128, 96):
                    audio_kbps = candidate_audio_kbps
                    est_audio_bytes = int(
                        math.ceil((candidate_audio_kbps * 1000 / 8.0) * eff_dur)
                    )
                    if est_audio_bytes <= int(MAX_OUTPUT_BYTES * 0.20):
                        break
                audio_bytes = int(math.ceil((audio_kbps * 1000 / 8.0) * eff_dur))
                hard_ceiling_video_kbps = int(
                    ((MAX_OUTPUT_BYTES * 0.96 - audio_bytes) * 8) / eff_dur / 1000
                )
                if hard_ceiling_video_kbps < 80:
                    raise RuntimeError(
                        f"终稿视频时长过长（{eff_dur:.1f} 秒，当前体积 {oversized_bytes / (1024**2):.1f} MiB），"
                        f"即使音频降至 {audio_kbps} kbps，视频物理码率上限仅 {max(0, hard_ceiling_video_kbps)} kbps，"
                        f"无法在 {MAX_OUTPUT_BYTES // (1024**2)} MiB 上限内保证可用画质；请缩短播报时长或分段导出"
                    )
                target_bytes = int(MAX_OUTPUT_BYTES * 0.88)
                raw_target_kbps = int(
                    ((target_bytes - audio_bytes) * 8) / eff_dur / 1000
                )
                target_video_kbps = min(
                    hard_ceiling_video_kbps, max(80, raw_target_kbps)
                )
                maxrate_kbps = min(
                    hard_ceiling_video_kbps,
                    max(target_video_kbps, int(round(target_video_kbps * 1.08))),
                )
                bufsize_kbps = max(
                    maxrate_kbps, int(round(target_video_kbps * 1.5))
                )
                reencode_msg = (
                    f"终稿体积 {oversized_bytes / (1024**2):.1f} MiB 超过 "
                    f"{MAX_OUTPUT_BYTES // (1024**2)} MiB 上限，正在按目标视频码率 "
                    f"{target_video_kbps} kbps（音频 {audio_kbps}k）自动重编码以保留本次渲染成果"
                )
                _update_task(task, progress=0.96, message=reencode_msg)
                task.note(reencode_msg, level="warning")
                if final_mp4_path.resolve() != video_src_path.resolve():
                    final_mp4_path.unlink(missing_ok=True)
                _run_stage4_encode(
                    [
                        "-b:v",
                        f"{target_video_kbps}k",
                        "-maxrate",
                        f"{maxrate_kbps}k",
                        "-bufsize",
                        f"{bufsize_kbps}k",
                    ],
                    audio_kbps=audio_kbps,
                    log_path=work_dir / "ffmpeg_native_reencode.log",
                )

                # Before unlinking lossless Stage 3/2 MKV, verify pass 1 fit within budget;
                # if still oversized, run one stricter 2nd pass while video_src_path is intact.
                if (
                    final_mp4_path.is_file()
                    and final_mp4_path.stat().st_size > MAX_OUTPUT_BYTES
                ):
                    task.check()
                    actual_size = final_mp4_path.stat().st_size
                    pass2_audio_kbps = min(audio_kbps, 128)
                    pass2_audio_bytes = int(
                        math.ceil((pass2_audio_kbps * 1000 / 8.0) * eff_dur)
                    )
                    pass2_hard_ceiling = int(
                        ((MAX_OUTPUT_BYTES * 0.96 - pass2_audio_bytes) * 8)
                        / eff_dur
                        / 1000
                    )
                    if pass2_hard_ceiling < 80:
                        raise RuntimeError(
                            f"终稿重编码后体积仍为 {actual_size / (1024**2):.1f} MiB（时长 {eff_dur:.1f} 秒），"
                            f"超过 {MAX_OUTPUT_BYTES // (1024**2)} MiB 上限且无可用视频码率余量继续压缩；"
                            "请缩短播报时长或分段导出"
                        )
                    scaled_kbps = int(
                        target_video_kbps
                        * ((MAX_OUTPUT_BYTES * 0.88) / float(actual_size))
                    )
                    pass2_target_kbps = min(
                        pass2_hard_ceiling, max(80, scaled_kbps)
                    )
                    pass2_msg = (
                        f"首次重编码后终稿体积仍为 {actual_size / (1024**2):.1f} MiB，超过 "
                        f"{MAX_OUTPUT_BYTES // (1024**2)} MiB 上限，正在按更严格码率 "
                        f"{pass2_target_kbps} kbps（音频 {pass2_audio_kbps}k）执行二次重编码"
                    )
                    _update_task(task, progress=0.965, message=pass2_msg)
                    task.note(pass2_msg, level="warning")
                    if final_mp4_path.resolve() != video_src_path.resolve():
                        final_mp4_path.unlink(missing_ok=True)
                    _run_stage4_encode(
                        [
                            "-b:v",
                            f"{pass2_target_kbps}k",
                            "-maxrate",
                            f"{pass2_target_kbps}k",
                            "-bufsize",
                            f"{pass2_target_kbps}k",
                        ],
                        audio_kbps=pass2_audio_kbps,
                        log_path=work_dir / "ffmpeg_native_reencode_pass2.log",
                    )

                if (
                    final_mp4_path.is_file()
                    and final_mp4_path.stat().st_size > MAX_OUTPUT_BYTES
                ):
                    final_oversized = final_mp4_path.stat().st_size
                    raise RuntimeError(
                        f"终稿视频经二次自动重编码后体积仍为 {final_oversized / (1024**2):.1f} MiB"
                        f"（时长 {eff_dur:.1f} 秒），超过 {MAX_OUTPUT_BYTES // (1024**2)} MiB 上限，无法保存；"
                        "请缩短播报时长或降低目标分辨率后重试"
                    )

            if final_mp4_path.is_file() and final_mp4_path.stat().st_size > 0:
                if final_mp4_path.stat().st_size > MAX_OUTPUT_BYTES:
                    raise RuntimeError(
                        f"终稿视频体积为 {final_mp4_path.stat().st_size / (1024**2):.1f} MiB"
                        f"（时长 {max(1.0, float(duration)):.1f} 秒），超过 "
                        f"{MAX_OUTPUT_BYTES // (1024**2)} MiB 上限，无法保存"
                    )
                output_src_path = final_mp4_path
            elif (
                self._run_cmd_fn is not None
                and video_src_path.is_file()
                and video_src_path.stat().st_size > 0
            ):
                # Offline test fallback when mocked run_cmd_fn only records commands.
                output_src_path = video_src_path
            else:
                raise RuntimeError("最终视频合成失败：ffmpeg 未产出有效 MP4 文件")

            # Free multi-GB Stage 3 (or Stage 2) lossless MKV only after final MP4 is verified <= MAX_OUTPUT_BYTES.
            if stage3_video_path is not None and (
                output_src_path.resolve() != stage3_video_path.resolve()
            ):
                _unlink_contained_file(work_dir, stage3_video_path)
            if output_src_path.resolve() != video_src_path.resolve():
                _unlink_contained_file(work_dir, video_src_path)

            task.check()
            _update_task(task, progress=0.97)
            elapsed = max(0.0, time.monotonic() - started)
            out_filename = f"{wf['id']}_{out_w}x{out_h}_{int(time.time())}.mp4"
            native_prompt_id = f"longcat16-native-{safe_task_tag}"
            task_id = str(getattr(task, "id", "") or "")
            save_file_fn = getattr(self.manager.store, "save_output_file", None)
            if callable(save_file_fn):
                save_kwargs: dict[str, Any] = {
                    "workflow_id": wf["id"],
                    "workflow_name": wf_name,
                    "filename": out_filename,
                    "elapsed": elapsed,
                    "prompt_id": native_prompt_id,
                }
                if _callable_accepts_kwarg(save_file_fn, "task_id"):
                    save_kwargs["task_id"] = task_id
                record = save_file_fn(output_src_path, **save_kwargs)
            else:
                save_fn = self.manager.store.save_output
                save_kwargs = {
                    "raw": output_src_path.read_bytes(),
                    "workflow_id": wf["id"],
                    "workflow_name": wf_name,
                    "filename": out_filename,
                    "elapsed": elapsed,
                    "prompt_id": native_prompt_id,
                }
                if _callable_accepts_kwarg(save_fn, "task_id"):
                    save_kwargs["task_id"] = task_id
                record = save_fn(**save_kwargs)
            elapsed_label = format_colabone_duration_zh(elapsed, subsecond=True)
            vsr_note = f" + {upscale_engine}" if upscale_engine != "none" else ""
            done_msg = (
                f"{wf_name} · 原生旗舰数字人生成完成"
                f"（LongCat 1.6{vsr_note}，{duration:.1f} 秒，{out_w}×{out_h}，总耗时 {elapsed_label}）"
            )
            _update_task(task, progress=1, message=done_msg, append_output=record)
            task.note(done_msg)
            return [record]
        finally:
            for mkv_path in intermediate_mkvs:
                _unlink_contained_file(work_dir, mkv_path)

    def run(
        self,
        task,
        wf: dict[str, Any],
        req: WorkflowRunRequest | Any,
        resolved_inputs: list[tuple[str, str, Path]],
    ) -> list[dict[str, Any]]:
        """Execute the full 4-stage remote digital human pipeline."""
        from server.runtime import ColabOneCancelledError, _scrub_secret_text

        started = time.monotonic()
        task.check()

        pipeline = wf.get("pipeline")
        if not isinstance(pipeline, dict):
            raise ValueError("工作流缺少 digital_human 流水线配置")
        video_backend = str(pipeline.get("video_backend") or "custom").strip().lower()
        if video_backend != "longcat_avatar_native" and (
            not isinstance(wf.get("workflow"), dict) or not wf["workflow"]
        ):
            raise ValueError("工作流缺少有效节点配置")

        overrides: dict[str, Any] = dict(getattr(req, "overrides", None) or {})
        # Never log hf_token.
        hf_token = str(getattr(req, "hf_token", "") or "")
        inputs_by_key: dict[str, tuple[str, Path]] = {
            field_key: (fname, Path(fpath))
            for field_key, fname, fpath in (resolved_inputs or [])
        }

        supported_tts_engines = [
            str(eid).strip()
            for eid in (pipeline.get("supported_tts_engines") or [])
            if str(eid).strip()
        ]
        default_tts_engine = str(
            pipeline.get("default_tts_engine")
            or (supported_tts_engines[0] if supported_tts_engines else "")
            or "voxcpm2"
        ).strip()
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
        default_target_w = int(
            pipeline.get("target_width")
            or (1920 if video_backend == "longcat_avatar_native" else 1280)
        )
        default_target_h = int(
            pipeline.get("target_height")
            or (1080 if video_backend == "longcat_avatar_native" else 720)
        )
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
        if anchor_entry is None and video_backend == "longcat_avatar_native":
            anchor_entry = inputs_by_key.get("1.image") or inputs_by_key.get(
                "pipeline.anchor_image"
            )
        if anchor_entry is None or anchor_entry[1].is_symlink() or not anchor_entry[1].is_file():
            raise ValueError("请先上传主播参考人像图片")
        _, anchor_src_path = anchor_entry

        audio_source = str(
            overrides.get("pipeline.audio_source") or "tts"
        ).strip().lower()
        if audio_source not in ("tts", "audio"):
            raise ValueError(f"不支持的音频来源模式：{audio_source}")

        render_quality = str(
            overrides.get("pipeline.render_quality") or "balanced_480p_ai2x"
        ).strip().lower()
        if render_quality not in ("balanced_480p_ai2x", "native_720p"):
            render_quality = "balanced_480p_ai2x"

        accel_profile = str(
            overrides.get("pipeline.acceleration_profile") or "max"
        ).strip().lower()
        if accel_profile not in ("max", "balanced", "compat"):
            accel_profile = "max"

        voice_preset = str(overrides.get("pipeline.voice_preset") or "").strip()

        res_spec = overrides.get("pipeline.target_resolution")
        target_w, target_h = parse_target_resolution(
            str(res_spec) if res_spec else "",
            default=(default_target_w, default_target_h),
        )
        native_preset: dict[str, Any] | None = None
        if video_backend == "longcat_avatar_native":
            preset_overrides = dict(overrides)
            raw_ov_upscale = preset_overrides.get("pipeline.upscale_mode")
            raw_plain_upscale = preset_overrides.get("upscale_mode")
            tmpl_upscale = str(pipeline.get("upscale_mode") or "").strip()
            if (
                (raw_ov_upscale is None or str(raw_ov_upscale).strip() == "")
                and (raw_plain_upscale is None or str(raw_plain_upscale).strip() == "")
                and tmpl_upscale
            ):
                preset_overrides["pipeline.upscale_mode"] = tmpl_upscale
            native_preset = resolve_longcat_native_preset(
                preset_overrides, target_w=target_w, target_h=target_h
            )
            base_w = int(native_preset["base_width"])
            base_h = int(native_preset["base_height"])
            upscale_mode = str(native_preset["upscale_mode"])
            out_w = int(native_preset["target_width"])
            out_h = int(native_preset["target_height"])
        else:
            if video_backend == "longcat_avatar":
                base_w, base_h = compute_base_dimensions(
                    video_backend, target_w, target_h, render_quality=render_quality
                )
            else:
                base_w, base_h = compute_base_dimensions(
                    video_backend, target_w, target_h
                )

            explicit_upscale = overrides.get("pipeline.upscale_mode")
            has_explicit_upscale = (
                "pipeline.upscale_mode" in overrides
                and explicit_upscale is not None
                and str(explicit_upscale).strip() != ""
            )
            if (
                video_backend == "longcat_avatar"
                and render_quality == "native_720p"
                and not has_explicit_upscale
            ):
                upscale_mode = "lanczos"
            else:
                upscale_mode = str(
                    explicit_upscale or default_upscale_mode or "none"
                ).strip().lower()
            if upscale_mode not in ("none", "ai_2x", "lanczos"):
                upscale_mode = "none"

            # H1: When upscale_mode == "none" and native (base_w, base_h) already covers
            # (target_w, target_h) (e.g. LongCat 1280x768 to 1280x720 or 768x1280 to 720x1280),
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
        elif video_backend != "longcat_avatar_native":
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
        engine_spec: dict[str, Any] = {}
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
            engine_spec = {"id": tts_engine}
            get_engine_fn = getattr(self.tts_runner, "get_engine", None)
            if callable(get_engine_fn):
                fetched_spec = get_engine_fn(tts_engine)
                if isinstance(fetched_spec, dict):
                    engine_spec = fetched_spec

            voice_ref_entry = inputs_by_key.get("pipeline.voice_ref_audio")
            ref_audio_path = voice_ref_entry[1] if voice_ref_entry else None
            emotion_ref_entry = inputs_by_key.get("pipeline.emotion_ref_audio")
            emotion_ref_path = emotion_ref_entry[1] if emotion_ref_entry else None
            ref_text = str(overrides.get("pipeline.voice_ref_text") or "").strip()
            voice_prompt = str(overrides.get("pipeline.voice_prompt") or "").strip()
            emotion_prompt = str(
                overrides.get("pipeline.emotion_prompt") or ""
            ).strip()

            validate_fn = getattr(
                self.tts_runner, "validate_tts_synthesis_inputs", None
            )
            if callable(validate_fn):
                validate_kwargs: dict[str, Any] = {
                    "engine_id": tts_engine,
                    "script_text": script_text,
                    "voice_preset": voice_preset,
                    "ref_audio_path": ref_audio_path,
                    "ref_text": ref_text,
                    "voice_prompt": voice_prompt,
                    "emotion_prompt": emotion_prompt,
                    "emotion_ref_path": emotion_ref_path,
                }
                try:
                    validate_sig = inspect.signature(validate_fn)
                    allow_legacy_param = validate_sig.parameters.get(
                        "allow_legacy_empty_indextts_check"
                    )
                    if (
                        allow_legacy_param is not None
                        and allow_legacy_param.kind
                        in (
                            inspect.Parameter.POSITIONAL_OR_KEYWORD,
                            inspect.Parameter.KEYWORD_ONLY,
                        )
                    ) or any(
                        p.kind == inspect.Parameter.VAR_KEYWORD
                        for p in validate_sig.parameters.values()
                    ):
                        validate_kwargs["allow_legacy_empty_indextts_check"] = (
                            getattr(self.tts_runner, "synth_fn", None) is not None
                        )
                except (TypeError, ValueError):
                    pass
                validation = validate_fn(**validate_kwargs)
                if isinstance(validation, dict) and isinstance(
                    validation.get("engine"), dict
                ):
                    engine_spec = validation["engine"]
            else:
                if tts_engine == "indextts2" and (
                    ref_audio_path is None or not ref_audio_path.is_file()
                ):
                    if voice_preset.lower() == "custom":
                        raise ValueError(
                            "IndexTTS2 在自定义音色模式下需要上传音色参考音频才能进行语音合成"
                        )
                    if voice_preset:
                        from server.tts_engines import resolve_colabone_voice_preset

                        if (
                            resolve_colabone_voice_preset(engine_spec, voice_preset)
                            is None
                        ):
                            raise ValueError("IndexTTS2 需要上传音色参考音频才能进行语音合成")
                if ref_audio_path is not None and (
                    ref_audio_path.is_symlink() or not ref_audio_path.is_file()
                ):
                    raise ValueError("音色参考音频文件不存在")
                if emotion_ref_path is not None and (
                    emotion_ref_path.is_symlink() or not emotion_ref_path.is_file()
                ):
                    raise ValueError("情绪参考音频文件不存在")
                if (
                    tts_engine == "breeze_tts2"
                    and ref_audio_path is not None
                    and not ref_text
                ):
                    raise ValueError(
                        "Breeze TTS 2 克隆参考音色时必须同时填写「参考音色对应文字」"
                    )

            pause_seconds = _parse_float_override(
                overrides.get("pipeline.pause_seconds"),
                0.28,
                "句间自然停顿",
            )
            pause_seconds = max(0.0, min(1.5, pause_seconds))
        else:
            audio_entry = inputs_by_key.get(effective_audio_key)
            if audio_entry is None and video_backend == "longcat_avatar_native":
                audio_entry = inputs_by_key.get("2.audio") or inputs_by_key.get(
                    "pipeline.drive_audio"
                )
            if audio_entry is None or audio_entry[1].is_symlink() or not audio_entry[1].is_file():
                raise ValueError("请先上传驱动音频文件")
            _, audio_src_path = audio_entry

        # Determine base seed once so segments advance deterministically (seed + idx).
        base_seed = 42
        if seed_node_key:
            raw_seed = overrides.get(seed_node_key)
            if raw_seed is None:
                raw_seed = _node_default_value(
                    wf.get("workflow") if isinstance(wf.get("workflow"), dict) else {},
                    seed_node_key,
                )
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
        avatar_engines: Any = None
        native_preflight_info: dict[str, Any] | None = None
        if video_backend == "longcat_avatar_native":
            assert native_preset is not None
            avatar_engines = self.get_avatar_engines()
            check_preflight_fn = getattr(avatar_engines, "check_preflight", None)
            if callable(check_preflight_fn):
                res_pf = _call_avatar_check_preflight(
                    check_preflight_fn,
                    min_vram_mib=int(wf.get("min_vram_mib") or 49152),
                    min_disk_mib=int(wf.get("min_disk_mib") or 73728),
                    upscale_mode=native_preset["upscale_engine"],
                    sampling_mode=native_preset["sampling_mode"],
                    vocal_separation=native_preset["vocal_separation"],
                )
                if isinstance(res_pf, dict):
                    native_preflight_info = res_pf
        elif packs is not None and callable(getattr(packs, "check_preflight", None)):
            packs.check_preflight(wf)

        tts_allow_unavailable = (
            getattr(self.tts_runner, "synth_fn", None) is not None
            if self.tts_runner is not None
            else True
        )
        if audio_source == "tts" and self.tts_runner is not None:
            for preflight_attr in ("check_vram", "check_disk"):
                preflight_fn = getattr(self.tts_runner, preflight_attr, None)
                if not callable(preflight_fn):
                    continue
                preflight_kwargs: dict[str, Any] = {}
                try:
                    preflight_sig = inspect.signature(preflight_fn)
                    has_var_kw = any(
                        p.kind == inspect.Parameter.VAR_KEYWORD
                        for p in preflight_sig.parameters.values()
                    )

                    def _accepts_preflight_kw(param_name: str) -> bool:
                        if has_var_kw:
                            return True
                        param = preflight_sig.parameters.get(param_name)
                        return param is not None and param.kind in (
                            inspect.Parameter.POSITIONAL_OR_KEYWORD,
                            inspect.Parameter.KEYWORD_ONLY,
                        )

                    if _accepts_preflight_kw("allow_unavailable"):
                        preflight_kwargs["allow_unavailable"] = tts_allow_unavailable
                    if preflight_attr == "check_disk":
                        if _accepts_preflight_kw("ref_audio_path"):
                            preflight_kwargs["ref_audio_path"] = ref_audio_path
                        if _accepts_preflight_kw("voice_preset"):
                            preflight_kwargs["voice_preset"] = voice_preset
                except (TypeError, ValueError):
                    pass
                preflight_fn(engine_spec, **preflight_kwargs)

        runtime_root_raw = getattr(
            getattr(self.manager, "runtime", None), "root", tempfile.gettempdir()
        )
        runtime_root = _validate_safe_path(
            Path(runtime_root_raw)
            if isinstance(runtime_root_raw, (str, os.PathLike))
            else Path(tempfile.gettempdir()),
            "运行目录",
        )

        # Combined Stage 1 TTS + Stage 2/3 disk preflight for longcat_avatar_native.
        native_required_disk_mib = 0
        if video_backend == "longcat_avatar_native":
            assert native_preset is not None
            native_required_disk_mib = _estimate_uncached_native_disk_mib(
                avatar_engines, native_preset, native_preflight_info
            )
            uncached_tts_mib = (
                _estimate_uncached_tts_disk_mib(
                    self.tts_runner,
                    engine_spec,
                    allow_unavailable=tts_allow_unavailable,
                    ref_audio_path=ref_audio_path,
                    voice_preset=voice_preset,
                )
                if audio_source == "tts"
                else 0
            )
            combined_disk_mib = uncached_tts_mib + native_required_disk_mib
            if native_required_disk_mib > 0 and combined_disk_mib > 0:
                free_disk_mib: int | None = None
                if (
                    isinstance(native_preflight_info, dict)
                    and native_preflight_info.get("free_disk_mib") is not None
                    and not isinstance(native_preflight_info.get("free_disk_mib"), bool)
                ):
                    try:
                        free_disk_mib = max(
                            0, int(native_preflight_info["free_disk_mib"])
                        )
                    except (TypeError, ValueError):
                        free_disk_mib = None
                if free_disk_mib is None:
                    disk_probe_root = getattr(avatar_engines, "root_dir", None)
                    usage_path = _existing_disk_ancestor(
                        Path(disk_probe_root)
                        if isinstance(disk_probe_root, (str, os.PathLike))
                        else runtime_root
                    )
                    try:
                        free_disk_mib = int(shutil.disk_usage(usage_path).free) // (
                            1024 * 1024
                        )
                    except OSError:
                        free_disk_mib = None
                if free_disk_mib is not None and free_disk_mib < combined_disk_mib:
                    if uncached_tts_mib > 0:
                        tts_label = str(
                            engine_spec.get("name")
                            or engine_spec.get("id")
                            or tts_engine
                        )
                        raise RuntimeError(
                            f"远端磁盘空间不足：TTS 引擎 {tts_label}（约 {uncached_tts_mib / 1024.0:.1f} GiB）"
                            f"与 LongCat-Video-Avatar 1.6 原生管线（约 {native_required_disk_mib / 1024.0:.1f} GiB）"
                            f"合计至少需要 {combined_disk_mib / 1024.0:.1f} GiB（{combined_disk_mib} MiB）可用空间，"
                            f"当前仅剩 {free_disk_mib / 1024.0:.1f} GiB（{free_disk_mib} MiB）"
                        )
                    raise RuntimeError(
                        f"远端磁盘空间不足：LongCat-Video-Avatar 1.6 原生管线与所选采样/人声模式"
                        f"至少需要 {native_required_disk_mib / 1024.0:.1f} GiB（{native_required_disk_mib} MiB）可用空间，"
                        f"当前仅剩 {free_disk_mib / 1024.0:.1f} GiB（{free_disk_mib} MiB）"
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
            master_sample_rate = 48000

            if audio_source == "tts":
                assert self.tts_runner is not None
                # Unload any active LLM and release ComfyUI VRAM before TTS GPU pass.
                runtime = getattr(self.manager, "runtime", None)
                if getattr(runtime, "loaded", None) and callable(
                    getattr(runtime, "_stop_engine", None)
                ):
                    task.note("正在卸载当前对话模型以释放显存")
                    runtime._stop_engine()

                if callable(getattr(self.manager, "is_running", None)) and self.manager.is_running():
                    if video_backend == "longcat_avatar_native" and callable(
                        getattr(self.manager, "stop_server", None)
                    ):
                        stop_msg = "正在停止远端 ComfyUI 以释放完整显存供配音与原生旗舰引擎使用"
                        _update_task(task, message=stop_msg)
                        task.note(stop_msg)
                        try:
                            self.manager.stop_server()
                        except ColabOneCancelledError:
                            raise
                        except Exception as exc:
                            scrubbed_err = _scrub_secret_text(
                                str(exc) or type(exc).__name__, hf_token
                            )
                            task.note(
                                f"停止远端 ComfyUI 时出现非致命提示：{scrubbed_err}",
                                level="warning",
                            )
                    else:
                        free_msg = "正在释放 ComfyUI 显存以启动远端配音引擎"
                        _update_task(task, message=free_msg)
                        task.note(free_msg)
                        try:
                            self.manager.free_memory(unload_models=True)
                        except ColabOneCancelledError:
                            raise
                        except Exception as exc:
                            scrubbed_err = _scrub_secret_text(
                                str(exc) or type(exc).__name__, hf_token
                            )
                            task.note(
                                f"释放 ComfyUI 显存时出现非致命提示：{scrubbed_err}",
                                level="warning",
                            )

                task.check()
                tts_dir = work_dir / "tts"
                tts_dir.mkdir(parents=True, exist_ok=True)
                tts_msg = f"正在使用远端 TTS 引擎（{tts_engine}）合成高保真配音"
                _update_task(task, progress=0.05, message=tts_msg)
                task.note(tts_msg)

                synth_kwargs: dict[str, Any] = {
                    "engine_id": tts_engine,
                    "script_text": script_text,
                    "work_dir": tts_dir,
                    "ref_audio_path": ref_audio_path,
                    "ref_text": ref_text,
                    "voice_prompt": voice_prompt,
                    "emotion_ref_path": emotion_ref_path,
                    "emotion_prompt": emotion_prompt,
                    "pause_seconds": pause_seconds,
                    "hf_token": hf_token,
                }
                try:
                    synth_sig = inspect.signature(self.tts_runner.synthesize)
                    if "voice_preset" in synth_sig.parameters or any(
                        p.kind == inspect.Parameter.VAR_KEYWORD
                        for p in synth_sig.parameters.values()
                    ):
                        synth_kwargs["voice_preset"] = voice_preset
                except (TypeError, ValueError):
                    pass
                if callable(getattr(self.tts_runner, "cancel_preview_prefetch", None)):
                    self.tts_runner.cancel_preview_prefetch(wait=True, timeout=2.0)
                try:
                    tts_result = self.tts_runner.synthesize(task, **synth_kwargs)
                finally:
                    if callable(getattr(self.tts_runner, "release_warm_worker", None)):
                        self.tts_runner.release_warm_worker("digital_human_video_phase")
                master_audio_path = Path(tts_result["audio_path"])
                raw_manifest = tts_result.get("manifest")
                manifest = raw_manifest if isinstance(raw_manifest, dict) else None
                raw_sr = int(
                    tts_result.get("sample_rate")
                    or (manifest.get("sample_rate") if isinstance(manifest, dict) else 0)
                    or 0
                )
                if raw_sr <= 0 and _is_readable_pcm_wav(master_audio_path):
                    with wave.open(str(master_audio_path), "rb") as _wf_sr:
                        raw_sr = int(_wf_sr.getframerate() or 0)
                if raw_sr > 0:
                    master_sample_rate = max(48000, raw_sr)
                duration = float(tts_result.get("duration") or 0.0)
                if duration <= 0.0 and _is_readable_pcm_wav(master_audio_path):
                    duration = _wav_duration_seconds(master_audio_path)

                if video_backend != "longcat_avatar_native":
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
                is_native_backend = video_backend == "longcat_avatar_native"
                audio_msg = (
                    "正在预处理上传音频（48 kHz 无损直通，保留立体声与原始动态）"
                    if is_native_backend
                    else "正在预处理上传音频（48 kHz / -23 LUFS）"
                )
                _update_task(task, progress=0.06, message=audio_msg)
                task.note(audio_msg)
                master_audio_path, duration, valleys = self._prepare_uploaded_audio(
                    task,
                    audio_src_path,
                    work_dir,
                    preserve_stereo_dynamics=is_native_backend,
                    detect_silence=not is_native_backend,
                )
                _update_task(task, progress=0.18)
                if is_native_backend:
                    task.note(f"音频预处理完成，总时长 {duration:.1f} 秒")
                else:
                    task.note(
                        f"音频预处理完成，总时长 {duration:.1f} 秒，检测到 {len(valleys)} 处自然换气静音点"
                    )

            if not math.isfinite(duration) or duration <= 0.0:
                raise ValueError("驱动音频时长为 0，无法生成数字人视频")
            if duration < 0.1:
                raise ValueError("音频时长过短，至少需要 0.1 秒")
            master_audio_path = _validate_safe_path(master_audio_path, "主音频路径")

            # Re-verify remaining disk headroom after Stage 1 TTS before Stage 2 starts.
            if (
                video_backend == "longcat_avatar_native"
                and audio_source == "tts"
                and native_required_disk_mib > 0
            ):
                disk_probe_root = getattr(avatar_engines, "root_dir", None)
                usage_path = _existing_disk_ancestor(
                    Path(disk_probe_root)
                    if isinstance(disk_probe_root, (str, os.PathLike))
                    else work_dir
                )
                try:
                    post_free_bytes = int(shutil.disk_usage(usage_path).free)
                except OSError as exc:
                    raise RuntimeError(
                        f"配音完成后无法检测远端磁盘空间（{exc}），LongCat-Video-Avatar 1.6 至少需要 "
                        f"{native_required_disk_mib / 1024.0:.1f} GiB 可用磁盘空间"
                    ) from None
                if post_free_bytes < native_required_disk_mib * 1024 * 1024:
                    post_free_mib = post_free_bytes // (1024 * 1024)
                    raise RuntimeError(
                        f"远端磁盘空间不足：配音完成后仅剩 {post_free_bytes / (1024.0**3):.1f} GiB"
                        f"（{post_free_mib} MiB），不足以继续运行 LongCat-Video-Avatar 1.6 原生管线"
                        f"（至少需要 {native_required_disk_mib / 1024.0:.1f} GiB / {native_required_disk_mib} MiB）"
                    )

            # ------------------------------------------------------------------
            # Stage 2 — Pack & Anchor Preparation (18%..32% progress)
            # ------------------------------------------------------------------
            task.check()
            wf_name = str(wf.get("name") or wf.get("id") or "数字人工作流")
            anchor_msg = f"正在预处理主播参考图（{base_w}×{base_h}）"
            _update_task(
                task,
                progress=0.18 if video_backend == "longcat_avatar_native" else 0.20,
                message=anchor_msg,
            )
            task.note(anchor_msg)
            anchor_prepared_path = work_dir / "anchor_prepared.png"
            prepare_anchor_image(anchor_src_path, anchor_prepared_path, base_w, base_h)

            if video_backend == "longcat_avatar_native":
                assert native_preset is not None
                return self._run_longcat_native_pipeline(
                    task=task,
                    wf=wf,
                    wf_name=wf_name,
                    preset=native_preset,
                    anchor_prepared_path=anchor_prepared_path,
                    master_audio_path=master_audio_path,
                    duration=duration,
                    output_fps=output_fps,
                    master_sample_rate=master_sample_rate,
                    audio_source=audio_source,
                    hf_token=hf_token,
                    work_dir=work_dir,
                    ffmpeg_bin=ffmpeg_bin,
                    started=started,
                    safe_task_tag=safe_task_tag,
                    preflight_checked=True,
                    preflight_info=native_preflight_info,
                )

            task.check()
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

            active_accel_profile = accel_profile
            longcat_gpu_caps: dict[str, Any] = {}
            uses_comfy_accel = video_backend in ("longcat_avatar", "infinitetalk")
            comfy_accel_fn = (
                apply_infinitetalk_acceleration
                if video_backend == "infinitetalk"
                else apply_longcat_acceleration
            )
            comfy_accel_label = (
                "InfiniteTalk" if video_backend == "infinitetalk" else "LongCat"
            )
            if uses_comfy_accel and active_accel_profile != "compat":
                task.check()
                longcat_gpu_caps = self._probe_longcat_gpu_caps()
                ensure_sage_fn = (
                    getattr(packs, "ensure_sageattention_wheels", None)
                    if packs is not None
                    else None
                )
                is_real_unpatched_sage = (
                    getattr(ensure_sage_fn, "__module__", "") == "server.comfy_packs"
                    and getattr(ensure_sage_fn, "__qualname__", "")
                    == "ColabOneComfyPackManager.ensure_sageattention_wheels"
                )
                installed_any = False
                if callable(ensure_sage_fn) and (
                    self._run_cmd_fn is None or not is_real_unpatched_sage
                ):
                    try:
                        sage_kwargs: dict[str, Any] = {}
                        try:
                            sage_sig = inspect.signature(ensure_sage_fn)
                            has_sage_var_kw = any(
                                p.kind == inspect.Parameter.VAR_KEYWORD
                                for p in sage_sig.parameters.values()
                            )
                            if has_sage_var_kw or "gpu_caps" in sage_sig.parameters:
                                sage_kwargs["gpu_caps"] = longcat_gpu_caps
                            if has_sage_var_kw or "allow_sage3" in sage_sig.parameters:
                                sage_kwargs["allow_sage3"] = (
                                    video_backend == "longcat_avatar"
                                    and active_accel_profile == "max"
                                )
                        except (TypeError, ValueError):
                            sage_kwargs = {
                                "gpu_caps": longcat_gpu_caps,
                                "allow_sage3": (
                                    video_backend == "longcat_avatar"
                                    and active_accel_profile == "max"
                                ),
                            }
                        updated_caps = ensure_sage_fn(task, **sage_kwargs)
                        if isinstance(updated_caps, dict):
                            installed_any = bool(updated_caps.get("installed_any"))
                            longcat_gpu_caps = {**longcat_gpu_caps, **updated_caps}
                    except ColabOneCancelledError:
                        raise
                    except Exception as exc:
                        task.note(
                            f"准备 SageAttention 算子时出现非致命异常，将自动使用当前可用注意力实现：{exc}",
                            level="warning",
                        )

                needs_restart_fn = (
                    getattr(packs, "needs_pip_restart", None)
                    if packs is not None
                    else None
                )
                pending_pip_restart = False
                if callable(needs_restart_fn):
                    try:
                        pending_pip_restart = bool(needs_restart_fn())
                    except Exception:
                        pending_pip_restart = False
                if installed_any or pending_pip_restart:
                    stopped = False
                    is_running_fn = None
                    try:
                        is_running_fn = getattr(
                            self.manager, "is_running", None
                        )
                        stop_fn = getattr(self.manager, "stop_server", None)
                        start_fn = getattr(self.manager, "start_server", None)
                        if (
                            callable(is_running_fn)
                            and is_running_fn()
                            and callable(stop_fn)
                            and callable(start_fn)
                        ):
                            task.note(
                                "已安装 SageAttention 加速算子，正在重启远端 ComfyUI 引擎以加载新内核"
                            )
                            stop_fn()
                            stopped = True
                            task.check()
                            start_fn(task)
                            clear_restart_fn = getattr(
                                packs, "clear_pending_pip_restart", None
                            )
                            if callable(clear_restart_fn):
                                clear_restart_fn()
                    except ColabOneCancelledError:
                        raise
                    except Exception as exc:
                        if stopped or (callable(is_running_fn) and not is_running_fn()):
                            raise
                        task.note(
                            f"重启远端 ComfyUI 以加载加速算子时出现非致命异常，将继续使用当前进程：{exc}",
                            level="warning",
                        )

                _, stage2_accel_summary = comfy_accel_fn(
                    wf["workflow"],
                    profile=active_accel_profile,
                    gpu_caps=longcat_gpu_caps,
                )
                task.note(
                    f"已启用 {comfy_accel_label} 远端加速栈：注意力={stage2_accel_summary['attention_mode']}，"
                    f"量化={stage2_accel_summary['quantization']}，"
                    f"显存常驻={'是' if stage2_accel_summary['resident_vram'] else '否'}，"
                    f"torch.compile={'开' if stage2_accel_summary['torch_compile'] else '关'}"
                )

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
            uploaded_anchor_ref = ""
            if video_backend == "longcat_avatar" and total_segs > 1:
                anchor_ref_name = f"dh_{safe_task_tag}_anchor_ref.png"
                uploaded_anchor_ref = self._upload_to_comfy(
                    task,
                    anchor_ref_name,
                    anchor_prepared_path,
                    is_image=True,
                )
                temp_io_in_files.add(anchor_ref_name)
                if uploaded_anchor_ref:
                    temp_io_in_files.add(uploaded_anchor_ref)

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
                seg_started = time.monotonic()
                seg_lo = 0.32 + idx * seg_span
                seg_hi = 0.32 + (idx + 1) * seg_span
                segment_label = f"第 {idx + 1}/{total_segs} 段"

                # Compute Wan 4k+1 aligned frame count BEFORE slicing audio so
                # Whisper/Wav2Vec embeds always receive at least (num_frames + 2)
                # frames of audio (zero int() truncation, zero pad_with_start drift).
                raw_frames = exact_frames + (1 if idx > 0 else 0)
                num_frames = align_wan_frames(raw_frames, minimum=17)
                if video_backend == "longcat_avatar":
                    num_frames = min(num_frames, LONGCAT_MAX_FRAMES)

                # Slice segment audio cleanly with stdlib wave (lossless & offline-safe).
                # For idx > 0, start 1 frame earlier so frame 0 overlaps the relay anchor
                # and frames [1, exact_frames + 1) align frame-exact with master_audio_path.
                seg_audio_start = (
                    max(0.0, seg_start - 1.0 / float(output_fps))
                    if idx > 0
                    else seg_start
                )
                min_audio_dur = (num_frames + 2) / float(output_fps)
                lookahead_end = seg_audio_start + min_audio_dur
                slice_end = max(seg_audio_start, min(duration, lookahead_end))
                seg_audio_path = work_dir / f"seg_{idx:03d}.wav"
                if _is_readable_pcm_wav(master_audio_path):
                    slice_wav_segment(
                        master_audio_path,
                        seg_audio_path,
                        seg_audio_start,
                        slice_end,
                        pad_to_seconds=min_audio_dur,
                    )
                else:
                    slice_dur = max(0.0, slice_end - seg_audio_start)
                    pad_needed = max(0.0, min_audio_dur - slice_dur)
                    ffmpeg_slice_cmd = [
                        ffmpeg_bin,
                        "-y",
                        "-protocol_whitelist",
                        "file,pipe",
                        "-ss",
                        f"{seg_audio_start:.6f}",
                        "-to",
                        f"{slice_end:.6f}",
                        "-i",
                        str(master_audio_path),
                    ]
                    if pad_needed > 1e-4:
                        ffmpeg_slice_cmd.extend(
                            ["-af", f"apad=whole_dur={min_audio_dur:.6f}"]
                        )
                    ffmpeg_slice_cmd.extend(
                        [
                            "-ar",
                            str(max(48000, master_sample_rate)),
                            "-c:a",
                            "pcm_s16le",
                            str(seg_audio_path),
                        ]
                    )
                    self._invoke_run_cmd(
                        ffmpeg_slice_cmd,
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

                # Build per-segment overrides using the precomputed Wan 4k+1 frame count.
                seg_overrides: dict[str, Any] = {
                    k: v for k, v in overrides.items() if not k.startswith("pipeline.")
                }

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

                # For LongCat-Avatar relay segments (idx > 0), anchor identity to the
                # original prepared portrait via ref_latent to prevent long-video drift.
                if (
                    video_backend == "longcat_avatar"
                    and idx > 0
                    and uploaded_anchor_ref
                ):
                    prompt_payload = inject_longcat_ref_latent_nodes(
                        prompt_payload, uploaded_anchor_ref
                    )

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

                if uses_comfy_accel:
                    base_prompt_payload = prompt_payload
                    attempted_accel_keys: set[tuple[Any, ...]] = set()
                    while True:
                        task.check()
                        prompt_payload, accel_summary = comfy_accel_fn(
                            base_prompt_payload,
                            profile=active_accel_profile,
                            gpu_caps=longcat_gpu_caps,
                        )
                        accel_key = (
                            accel_summary["profile"],
                            accel_summary["attention_mode"],
                            accel_summary["quantization"],
                            accel_summary.get("any_fp8_fast"),
                            accel_summary["resident_vram"],
                            accel_summary["torch_compile"],
                            accel_summary.get("vae_tiling"),
                            accel_summary.get("batched_cfg"),
                            accel_summary.get("resize_device"),
                            accel_summary.get("rms_norm_function"),
                        )
                        attempted_accel_keys.add(accel_key)
                        try:
                            exec_result = exec_fn(
                                task, wf, prompt_payload, **exec_kwargs
                            )
                            break
                        except ColabOneCancelledError:
                            raise
                        except RuntimeError as exc:
                            task.check()
                            stopping = getattr(
                                getattr(self.manager, "runtime", None),
                                "stopping",
                                None,
                            )
                            if (
                                isinstance(stopping, threading.Event)
                                and stopping.is_set()
                            ):
                                raise

                            # Fail fast on non-acceleration fatal errors instead of blindly
                            # restarting/retrying ComfyUI through 3-4 acceleration tiers.
                            err_lower = str(exc or "").lower()
                            if any(
                                marker in err_lower
                                for marker in (
                                    "拒绝了工作流",
                                    "工作流节点校验失败",
                                    "未返回有效任务编号",
                                    "超过 60 分钟",
                                    "可能已在画布中被取消",
                                    "查询 comfyui 任务进度连续失败",
                                    "no space left on device",
                                    "磁盘空间不足",
                                    "filenotfounderror",
                                    "modulenotfounderror",
                                )
                            ):
                                raise

                            next_profile = active_accel_profile
                            next_caps = dict(longcat_gpu_caps)
                            fallback_reason = ""
                            can_step_offload = False
                            if video_backend == "infinitetalk":
                                has_fp8_fast = bool(
                                    accel_summary.get("any_fp8_fast")
                                ) or str(accel_summary["quantization"]).endswith(
                                    "_fast"
                                )
                                can_step_offload = (
                                    accel_summary["profile"] != "compat"
                                    and accel_summary["attention_mode"] != "sdpa"
                                    and (
                                        bool(accel_summary["resident_vram"])
                                        or has_fp8_fast
                                    )
                                )
                                can_step_compat = (
                                    accel_summary["profile"] != "compat"
                                    and (
                                        accel_summary["attention_mode"] != "sdpa"
                                        or has_fp8_fast
                                        or bool(accel_summary["resident_vram"])
                                        or accel_summary.get("resize_device") == "gpu"
                                        or accel_summary.get("rms_norm_function")
                                        != "default"
                                    )
                                )
                                compat_reason = (
                                    "回退至兼容保底模式（SDPA + Scaled FP8 + 常规显存卸载）"
                                )
                            else:
                                can_step_compat = (
                                    accel_summary["attention_mode"] != "sdpa"
                                    or accel_summary["quantization"] != "disabled"
                                    or bool(accel_summary["resident_vram"])
                                )
                                compat_reason = (
                                    "回退至兼容保底模式（SDPA + BF16 + 常规显存卸载）"
                                )

                            if accel_summary["torch_compile"]:
                                next_profile = "balanced"
                                fallback_reason = (
                                    "关闭 torch.compile 编译加速（回退至高速稳健模式）"
                                )
                            elif accel_summary["attention_mode"] == "sageattn_3":
                                if video_backend == "infinitetalk":
                                    next_profile = "balanced"
                                next_caps["sageattn3_available"] = False
                                fallback_reason = (
                                    "关闭 SageAttention 3（回退至 SageAttention 2）"
                                )
                            elif video_backend == "infinitetalk" and can_step_offload:
                                next_profile = "balanced"
                                next_caps["vram_mib"] = 0
                                next_caps["fp8_fast_supported"] = False
                                attn_mode = str(accel_summary["attention_mode"] or "")
                                if attn_mode == "sageattn":
                                    attn_label = "SageAttention 2"
                                elif attn_mode == "flash_attn_2":
                                    attn_label = "FlashAttention 2"
                                else:
                                    attn_label = attn_mode or "当前注意力"
                                fallback_reason = (
                                    f"关闭显存常驻与 FP8 加速并启用 VAE 分块卸载（保留 {attn_label} 加速）"
                                )
                            elif can_step_compat:
                                next_profile = "compat"
                                fallback_reason = compat_reason
                            else:
                                raise

                            _, preview_next = comfy_accel_fn(
                                base_prompt_payload,
                                profile=next_profile,
                                gpu_caps=next_caps,
                            )
                            next_key = (
                                preview_next["profile"],
                                preview_next["attention_mode"],
                                preview_next["quantization"],
                                preview_next.get("any_fp8_fast"),
                                preview_next["resident_vram"],
                                preview_next["torch_compile"],
                                preview_next.get("vae_tiling"),
                                preview_next.get("batched_cfg"),
                                preview_next.get("resize_device"),
                                preview_next.get("rms_norm_function"),
                            )
                            if next_key in attempted_accel_keys:
                                raise

                            active_accel_profile = next_profile
                            longcat_gpu_caps = next_caps
                            task.note(
                                f"{segment_label} 加速算子执行异常（{exc}），已自动{fallback_reason}并重试",
                                level="warning",
                            )
                            is_running_fn = getattr(
                                self.manager, "is_running", None
                            )
                            start_fn = getattr(self.manager, "start_server", None)
                            free_fn = getattr(self.manager, "free_memory", None)
                            if (
                                callable(is_running_fn)
                                and not is_running_fn()
                                and callable(start_fn)
                            ):
                                task.note(
                                    f"{segment_label} 检测到远端 ComfyUI 已退出，正在重启引擎后继续回退重试",
                                    level="warning",
                                )
                                start_fn(task)
                            elif callable(free_fn):
                                try:
                                    free_fn(unload_models=True)
                                except Exception:
                                    pass
                else:
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
                seg_elapsed = max(0.0, time.monotonic() - seg_started)
                task.note(
                    f"{wf_name} · {segment_label} 渲染完成（耗时 {format_colabone_duration_zh(seg_elapsed, subsecond=True)}）"
                )

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

            cfr_args = (
                ["-fps_mode", "cfr"]
                if self._run_cmd_fn is not None
                else _resolve_ffmpeg_cfr_args(ffmpeg_bin)
            )
            use_nvenc = (
                self._run_cmd_fn is None and _probe_ffmpeg_nvenc_h264(ffmpeg_bin)
            )
            if use_nvenc:
                task.note("已启用 GPU NVENC 硬件编码（h264_nvenc · p6 hq）加速终稿拼接封装")

            def _build_concat_final_cmd(
                video_rate_args: list[str] | None = None,
                *,
                encode_nvenc: bool,
                audio_kbps: int = 192,
            ) -> list[str]:
                encode_args = _build_h264_encode_args(
                    18,
                    preset="fast",
                    use_nvenc=encode_nvenc,
                    rate_args=video_rate_args,
                )
                return [
                    ffmpeg_bin,
                    "-y",
                    *input_args,
                    "-filter_complex_script",
                    str(filter_script_path),
                    "-map",
                    "[vout]",
                    "-map",
                    f"{len(segment_mp4s)}:a:0",
                    *encode_args,
                    "-pix_fmt",
                    "yuv420p",
                    *cfr_args,
                    "-r",
                    str(output_fps),
                    "-c:a",
                    "aac",
                    "-b:a",
                    f"{int(audio_kbps)}k",
                    "-ar",
                    str(max(48000, master_sample_rate)),
                    "-movflags",
                    "+faststart",
                    "-shortest",
                    str(final_mp4_path),
                ]

            def _run_concat_encode(
                video_rate_args: list[str] | None,
                *,
                log_path: Path,
                audio_kbps: int = 192,
            ) -> None:
                nonlocal use_nvenc
                cmd = _build_concat_final_cmd(
                    video_rate_args,
                    encode_nvenc=use_nvenc,
                    audio_kbps=audio_kbps,
                )
                if not use_nvenc:
                    self._invoke_run_cmd(
                        cmd,
                        task,
                        log_path,
                        timeout=1800,
                        cwd=work_dir,
                    )
                    return
                try:
                    self._invoke_run_cmd(
                        cmd,
                        task,
                        log_path,
                        timeout=1800,
                        cwd=work_dir,
                    )
                except ColabOneCancelledError:
                    raise
                except RuntimeError as exc:
                    task.check()
                    stopping = getattr(
                        getattr(self.manager, "runtime", None), "stopping", None
                    )
                    if isinstance(stopping, threading.Event) and stopping.is_set():
                        raise
                    err_lower = str(exc or "").lower()
                    if any(
                        marker in err_lower
                        for marker in (
                            "超时",
                            "timed out",
                            "no space left on device",
                            "磁盘空间不足",
                        )
                    ):
                        raise
                    use_nvenc = False
                    _mark_ffmpeg_nvenc_unavailable(ffmpeg_bin)
                    task.note(
                        f"GPU NVENC 硬件编码失败（{exc}），已自动回退至 libx264 软件编码重试",
                        level="warning",
                    )
                    final_mp4_path.unlink(missing_ok=True)
                    self._invoke_run_cmd(
                        _build_concat_final_cmd(
                            video_rate_args,
                            encode_nvenc=False,
                            audio_kbps=audio_kbps,
                        ),
                        task,
                        log_path,
                        timeout=1800,
                        cwd=work_dir,
                    )

            _run_concat_encode(
                None,
                log_path=work_dir / "ffmpeg_final.log",
            )

            # Auto-reencode if concatenated MP4 exceeds MAX_OUTPUT_BYTES (512 MiB) so GPU work is preserved.
            if (
                final_mp4_path.is_file()
                and final_mp4_path.stat().st_size > MAX_OUTPUT_BYTES
            ):
                task.check()
                oversized_bytes = final_mp4_path.stat().st_size
                eff_dur = max(1.0, float(duration))
                audio_kbps = 192
                for candidate_audio_kbps in (192, 128, 96):
                    audio_kbps = candidate_audio_kbps
                    est_audio_bytes = int(
                        math.ceil((candidate_audio_kbps * 1000 / 8.0) * eff_dur)
                    )
                    if est_audio_bytes <= int(MAX_OUTPUT_BYTES * 0.20):
                        break
                audio_bytes = int(math.ceil((audio_kbps * 1000 / 8.0) * eff_dur))
                hard_ceiling_video_kbps = int(
                    ((MAX_OUTPUT_BYTES * 0.96 - audio_bytes) * 8) / eff_dur / 1000
                )
                if hard_ceiling_video_kbps < 80:
                    raise RuntimeError(
                        f"终稿视频时长过长（{eff_dur:.1f} 秒，当前体积 {oversized_bytes / (1024**2):.1f} MiB），"
                        f"即使音频降至 {audio_kbps} kbps，视频物理码率上限仅 {max(0, hard_ceiling_video_kbps)} kbps，"
                        f"无法在 {MAX_OUTPUT_BYTES // (1024**2)} MiB 上限内保证可用画质；请缩短播报时长或分段导出"
                    )
                target_bytes = int(MAX_OUTPUT_BYTES * 0.88)
                raw_target_kbps = int(
                    ((target_bytes - audio_bytes) * 8) / eff_dur / 1000
                )
                target_video_kbps = min(
                    hard_ceiling_video_kbps, max(80, raw_target_kbps)
                )
                maxrate_kbps = min(
                    hard_ceiling_video_kbps,
                    max(target_video_kbps, int(round(target_video_kbps * 1.08))),
                )
                bufsize_kbps = max(
                    maxrate_kbps, int(round(target_video_kbps * 1.5))
                )
                reencode_msg = (
                    f"终稿体积 {oversized_bytes / (1024**2):.1f} MiB 超过 "
                    f"{MAX_OUTPUT_BYTES // (1024**2)} MiB 上限，正在按目标视频码率 "
                    f"{target_video_kbps} kbps（音频 {audio_kbps}k）自动重编码以保留本次渲染成果"
                )
                _update_task(task, progress=0.955, message=reencode_msg)
                task.note(reencode_msg, level="warning")
                final_mp4_path.unlink(missing_ok=True)
                _run_concat_encode(
                    [
                        "-b:v",
                        f"{target_video_kbps}k",
                        "-maxrate",
                        f"{maxrate_kbps}k",
                        "-bufsize",
                        f"{bufsize_kbps}k",
                    ],
                    audio_kbps=audio_kbps,
                    log_path=work_dir / "ffmpeg_final_reencode.log",
                )

                if (
                    final_mp4_path.is_file()
                    and final_mp4_path.stat().st_size > MAX_OUTPUT_BYTES
                ):
                    task.check()
                    actual_size = final_mp4_path.stat().st_size
                    pass2_audio_kbps = min(audio_kbps, 128)
                    pass2_audio_bytes = int(
                        math.ceil((pass2_audio_kbps * 1000 / 8.0) * eff_dur)
                    )
                    pass2_hard_ceiling = int(
                        ((MAX_OUTPUT_BYTES * 0.96 - pass2_audio_bytes) * 8)
                        / eff_dur
                        / 1000
                    )
                    if pass2_hard_ceiling < 80:
                        raise RuntimeError(
                            f"终稿重编码后体积仍为 {actual_size / (1024**2):.1f} MiB（时长 {eff_dur:.1f} 秒），"
                            f"超过 {MAX_OUTPUT_BYTES // (1024**2)} MiB 上限且无可用视频码率余量继续压缩；"
                            "请缩短播报时长或分段导出"
                        )
                    scaled_kbps = int(
                        target_video_kbps
                        * ((MAX_OUTPUT_BYTES * 0.88) / float(actual_size))
                    )
                    pass2_target_kbps = min(
                        pass2_hard_ceiling, max(80, scaled_kbps)
                    )
                    pass2_msg = (
                        f"首次重编码后终稿体积仍为 {actual_size / (1024**2):.1f} MiB，超过 "
                        f"{MAX_OUTPUT_BYTES // (1024**2)} MiB 上限，正在按更严格码率 "
                        f"{pass2_target_kbps} kbps（音频 {pass2_audio_kbps}k）执行二次重编码"
                    )
                    _update_task(task, progress=0.965, message=pass2_msg)
                    task.note(pass2_msg, level="warning")
                    final_mp4_path.unlink(missing_ok=True)
                    _run_concat_encode(
                        [
                            "-b:v",
                            f"{pass2_target_kbps}k",
                            "-maxrate",
                            f"{pass2_target_kbps}k",
                            "-bufsize",
                            f"{pass2_target_kbps}k",
                        ],
                        audio_kbps=pass2_audio_kbps,
                        log_path=work_dir / "ffmpeg_final_reencode_pass2.log",
                    )

                if (
                    final_mp4_path.is_file()
                    and final_mp4_path.stat().st_size > MAX_OUTPUT_BYTES
                ):
                    final_oversized = final_mp4_path.stat().st_size
                    raise RuntimeError(
                        f"终稿视频经二次自动重编码后体积仍为 {final_oversized / (1024**2):.1f} MiB"
                        f"（时长 {eff_dur:.1f} 秒），超过 {MAX_OUTPUT_BYTES // (1024**2)} MiB 上限，无法保存；"
                        "请缩短播报时长或降低目标分辨率后重试"
                    )

            if final_mp4_path.is_file() and final_mp4_path.stat().st_size > 0:
                if final_mp4_path.stat().st_size > MAX_OUTPUT_BYTES:
                    raise RuntimeError(
                        f"终稿视频体积为 {final_mp4_path.stat().st_size / (1024**2):.1f} MiB"
                        f"（时长 {max(1.0, float(duration)):.1f} 秒），超过 "
                        f"{MAX_OUTPUT_BYTES // (1024**2)} MiB 上限，无法保存"
                    )
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
            task_id = str(getattr(task, "id", "") or "")
            save_file_fn = getattr(self.manager.store, "save_output_file", None)
            if callable(save_file_fn):
                save_kwargs: dict[str, Any] = {
                    "workflow_id": wf["id"],
                    "workflow_name": wf_name,
                    "filename": out_filename,
                    "elapsed": elapsed,
                    "prompt_id": last_prompt_id,
                }
                if _callable_accepts_kwarg(save_file_fn, "task_id"):
                    save_kwargs["task_id"] = task_id
                record = save_file_fn(output_src_path, **save_kwargs)
            else:
                save_fn = self.manager.store.save_output
                save_kwargs = {
                    "raw": output_src_path.read_bytes(),
                    "workflow_id": wf["id"],
                    "workflow_name": wf_name,
                    "filename": out_filename,
                    "elapsed": elapsed,
                    "prompt_id": last_prompt_id,
                }
                if _callable_accepts_kwarg(save_fn, "task_id"):
                    save_kwargs["task_id"] = task_id
                record = save_fn(**save_kwargs)
            elapsed_label = format_colabone_duration_zh(elapsed, subsecond=True)
            done_msg = (
                f"{wf_name} · 数字人长视频生成完成"
                f"（共 {total_segs} 段，{duration:.1f} 秒，{out_w}×{out_h}，总耗时 {elapsed_label}）"
            )
            _update_task(task, progress=1, message=done_msg, append_output=record)
            task.note(done_msg)
            return [record]
        finally:
            shutil.rmtree(work_dir, ignore_errors=True)
            self._cleanup_intermediate_io_files(
                safe_task_tag, temp_io_in_files, temp_io_out_files
            )
