"""Unified real-time visual driver adapters for Live Studio.

Provides concrete adapters for:
- SoulX-FlashHead-1.3B Lite (`flashhead_lite`): single-photo instant portrait streaming
- MuseTalk 1.5 (`musetalk_bank`): 1080p action-bank lip-sync with ring-buffer,
  crossfade frame blending, and latent cache management
- PersonaLive (`personalive_dance`): experimental single-photo driving-video pose/dance
- Synthetic offline preview (`synthetic`): deterministic RGB frame generator with
  crossfade transitions for 100% offline unit tests and dry runs
"""

from __future__ import annotations

import array
import hashlib
import io
import json
import math
import os
import shutil
import subprocess
import sys
import threading
import time
from abc import ABC, abstractmethod
from collections import deque
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from PIL import Image, ImageDraw, ImageOps

from common.files import (
    COLABONE_SENSITIVE_SUBPROCESS_ENV_KEYS,
    colabone_sanitized_subprocess_env,
)
from common.live_studio import COLABONE_LIVE_CLIP_EXTENSIONS, validate_colabone_live_id

_SENSITIVE_VISUAL_ENV_KEYS = COLABONE_SENSITIVE_SUBPROCESS_ENV_KEYS


def _sanitized_visual_subprocess_env() -> dict[str, str]:
    return colabone_sanitized_subprocess_env()


def _fit_rgb_image(img: Image.Image, width: int, height: int) -> bytes:
    """Aspect-preserving center-crop + resize into RGB bytes."""
    resampling = (
        Image.Resampling.LANCZOS
        if hasattr(Image, "Resampling")
        else getattr(Image, "LANCZOS", Image.BICUBIC)
    )
    rgb = img.convert("RGB")
    if rgb.size == (width, height):
        return rgb.tobytes()
    fitted = ImageOps.fit(rgb, (width, height), method=resampling, centering=(0.5, 0.5))
    return fitted.tobytes()


def _decode_video_frames_rgb(
    video_path: Path,
    width: int,
    height: int,
    *,
    max_frames: int = 300,
) -> list[bytes]:
    """Decode real video clip frames into RGB byte buffers via cv2 or ffmpeg when available.

    Returns an empty list cleanly if the clip cannot be decoded or neither backend
    is available (e.g. placeholder bytes in offline unit tests).
    """
    raw_str = os.fspath(video_path)
    if any(ch in raw_str for ch in ("\r", "\n", "\x00")):
        return []
    if video_path.is_symlink() or not video_path.is_file():
        return []
    try:
        resolved_path = video_path.resolve(strict=True)
    except OSError:
        return []
    resolved_str = os.fspath(resolved_path)
    if (
        resolved_path.is_symlink()
        or not resolved_path.is_file()
        or any(ch in resolved_str for ch in ("\r", "\n", "\x00"))
        or resolved_str.startswith("-")
    ):
        return []
    frame_size = int(width) * int(height) * 3
    if frame_size <= 0 or max_frames <= 0:
        return []

    # 1. Try OpenCV first when installed.
    try:
        import cv2  # type: ignore[import-not-found]
    except Exception:
        cv2 = None

    if cv2 is not None:
        cap = None
        try:
            cap = cv2.VideoCapture(resolved_str)
            if cap is not None and cap.isOpened():
                decoded: list[bytes] = []
                while len(decoded) < max_frames:
                    ok, frame_bgr = cap.read()
                    if not ok or frame_bgr is None:
                        break
                    frame_rgb = cv2.cvtColor(frame_bgr, cv2.COLOR_BGR2RGB)
                    pil_img = Image.fromarray(frame_rgb)
                    decoded.append(_fit_rgb_image(pil_img, width, height))
                if decoded:
                    return decoded
        except Exception:
            pass
        finally:
            if cap is not None:
                try:
                    cap.release()
                except Exception:
                    pass

    # 2. Fallback to ffmpeg pipe when installed on PATH.
    ffmpeg_bin = shutil.which("ffmpeg")
    if not ffmpeg_bin:
        return []
    vf = (
        f"scale={int(width)}:{int(height)}:force_original_aspect_ratio=increase,"
        f"crop={int(width)}:{int(height)}"
    )
    cmd = [
        ffmpeg_bin,
        "-nostdin",
        "-hide_banner",
        "-loglevel",
        "error",
        "-protocol_whitelist",
        "file,pipe",
        "-i",
        resolved_str,
        "-vf",
        vf,
        "-frames:v",
        str(int(max_frames)),
        "-f",
        "rawvideo",
        "-pix_fmt",
        "rgb24",
        "pipe:1",
    ]
    try:
        proc = subprocess.run(
            cmd,
            capture_output=True,
            timeout=15.0,
            check=False,
            stdin=subprocess.DEVNULL,
            env=_sanitized_visual_subprocess_env(),
        )
        raw = proc.stdout or b""
        if proc.returncode != 0 or len(raw) < frame_size:
            return []
        count = min(max_frames, len(raw) // frame_size)
        return [raw[i * frame_size : (i + 1) * frame_size] for i in range(count)]
    except Exception:
        return []


@dataclass(frozen=True)
class ColabOneVisualFrame:
    """Single decoded RGB video frame emitted by a visual engine."""

    width: int
    height: int
    rgb_bytes: bytes
    pts_ms: int
    epoch: int
    action_id: str
    engine_id: str
    is_crossfading: bool = False


def parse_colabone_resolution(resolution: str) -> tuple[int, int]:
    """Parse '<width>x<height>' into an integer (width, height) tuple."""
    parts = str(resolution or "720x1280").lower().strip().split("x", 1)
    if len(parts) != 2 or not parts[0].isdigit() or not parts[1].isdigit():
        raise ValueError(f"非法分辨率格式: {resolution!r}")
    w, h = int(parts[0]), int(parts[1])
    if not (64 <= w <= 2560 and 64 <= h <= 2560):
        raise ValueError(f"分辨率超出允许范围: {resolution!r}")
    return w, h


def compute_pcm16_rms(pcm_bytes: bytes) -> float:
    """Compute normalized RMS amplitude in [0.0, 1.0] for mono Int16 PCM."""
    if not pcm_bytes or len(pcm_bytes) < 2:
        return 0.0
    usable = len(pcm_bytes) - (len(pcm_bytes) % 2)
    samples = array.array("h")
    samples.frombytes(pcm_bytes[:usable])
    if sys.byteorder != "little":
        samples.byteswap()
    if not samples:
        return 0.0
    acc = sum(float(s) * float(s) for s in samples)
    rms = math.sqrt(acc / len(samples)) / 32768.0
    return max(0.0, min(1.0, rms))


def _finite_float(val: Any, default: float) -> float:
    try:
        out = float(val)
    except (TypeError, ValueError):
        return default
    return out if math.isfinite(out) else default


def blend_rgb_frames(
    frame_a: bytes, frame_b: bytes, width: int, height: int, alpha: float
) -> bytes:
    """Linearly crossfade two equal-sized RGB byte buffers using Pillow."""
    clamped = max(0.0, min(1.0, _finite_float(alpha, 0.0)))
    expected = width * height * 3
    if len(frame_a) != expected or len(frame_b) != expected:
        raise ValueError("混合帧字节长度与分辨率不匹配")
    if clamped <= 0.0:
        return bytes(frame_a)
    if clamped >= 1.0:
        return bytes(frame_b)
    img_a = Image.frombytes("RGB", (width, height), frame_a)
    img_b = Image.frombytes("RGB", (width, height), frame_b)
    return Image.blend(img_a, img_b, clamped).tobytes()


class ColabOneLiveCameraPoseTracker:
    """Real-time lightweight operator motion/pose extractor from mobile camera JPEGs.

    Tracks horizontal sway (dx), vertical nod (dy), subtle head/shoulder roll tilt,
    lean zoom scale, and motion energy with EMA smoothing and neutral auto-decay.
    Works 100% offline with Pillow (and automatically accelerates via NumPy if present).
    """

    def __init__(self, *, sample_size: int = 48, smooth_alpha: float = 0.35) -> None:
        self._lock = threading.RLock()
        self._update_lock = threading.Lock()
        self._size = max(16, int(sample_size))
        self._alpha = max(0.05, min(0.95, _finite_float(smooth_alpha, 0.35)))
        self._prev_gray: bytes | None = None
        self._base_cx: float | None = None
        self._base_cy: float | None = None
        self._base_tilt: float | None = None
        self._gen = 0
        self._dx = 0.0
        self._dy = 0.0
        self._roll_deg = 0.0
        self._scale = 1.0
        self._energy = 0.0
        self._updated_at = 0.0
        # Precompute center-salience weights and coordinate tables once.
        n = self._size
        self._center_weights = [
            max(
                0.15,
                1.0
                - ((((x + 0.5) / n - 0.5) ** 2) + (((y + 0.5) / n - 0.5) ** 2)) * 1.4,
            )
            for y in range(n)
            for x in range(n)
        ]
        try:
            import numpy as _np

            yy, xx = _np.mgrid[0:n, 0:n]
            nx = (xx + 0.5) / float(n) - 0.5
            ny = (yy + 0.5) / float(n) - 0.5
            self._np = _np
            self._np_cw = _np.maximum(0.15, 1.0 - (nx * nx + ny * ny) * 1.4).astype(_np.float32)
            self._np_xx = xx.astype(_np.float32)
            self._np_yy = yy.astype(_np.float32)
        except Exception:
            self._np = None
            self._np_cw = None
            self._np_xx = None
            self._np_yy = None

    def reset(self) -> None:
        with self._lock:
            self._gen += 1
            self._prev_gray = None
            self._base_cx = None
            self._base_cy = None
            self._base_tilt = None
            self._dx = 0.0
            self._dy = 0.0
            self._roll_deg = 0.0
            self._scale = 1.0
            self._energy = 0.0
            self._updated_at = 0.0

    def update_from_image(self, img: Image.Image) -> dict[str, float]:
        with self._update_lock:
            return self._update_from_image_serialized(img)

    def _update_from_image_serialized(self, img: Image.Image) -> dict[str, float]:
        resampling = (
            Image.Resampling.BILINEAR
            if hasattr(Image, "Resampling")
            else getattr(Image, "BILINEAR", 2)
        )
        small = img.convert("L").resize((self._size, self._size), resampling)
        gray = small.tobytes()
        n = self._size
        half = n // 2

        with self._lock:
            gen_before = self._gen
            prev = self._prev_gray

        if self._np is not None and self._np_cw is not None:
            np = self._np
            arr = np.frombuffer(gray, dtype=np.uint8).reshape((n, n)).astype(np.float32)
            w = (arr / 255.0 + 0.08) * self._np_cw
            total_w = float(w.sum())
            cx = float((w * self._np_xx).sum() / total_w) / n if total_w > 0 else 0.5
            cy = float((w * self._np_yy).sum() / total_w) / n if total_w > 0 else 0.5
            upper_w = float(w[:half, :].sum())
            lower_w = float(w[half:, :].sum())
            upper_cx = float((w[:half, :] * self._np_xx[:half, :]).sum() / upper_w) if upper_w > 0 else n * 0.5
            lower_cx = float((w[half:, :] * self._np_xx[half:, :]).sum() / lower_w) if lower_w > 0 else n * 0.5
            tilt = (upper_cx - lower_cx) / n
            diff_sum = 0.0
            if prev is not None:
                prev_arr = np.frombuffer(prev, dtype=np.uint8).reshape((n, n)).astype(np.float32)
                diff = np.abs(arr - prev_arr)
                diff_sum = float(diff.sum())
                mask = diff >= 14.0
                diff_w = float(diff[mask].sum())
                if diff_w > n * 18.0:
                    mcx = float((diff[mask] * self._np_xx[mask]).sum() / diff_w) / n
                    mcy = float((diff[mask] * self._np_yy[mask]).sum() / diff_w) / n
                    cx = 0.6 * cx + 0.4 * mcx
                    cy = 0.6 * cy + 0.4 * mcy
        else:
            total_w = 0.0
            sum_x = 0.0
            sum_y = 0.0
            upper_w = 0.0
            upper_x = 0.0
            lower_w = 0.0
            lower_x = 0.0
            diff_sum = 0.0
            diff_w = 0.0
            diff_x = 0.0
            diff_y = 0.0
            cweights = self._center_weights
            for y in range(n):
                row_off = y * n
                is_upper = y < half
                for x in range(n):
                    idx = row_off + x
                    val = gray[idx]
                    w = (val / 255.0 + 0.08) * cweights[idx]
                    total_w += w
                    sum_x += x * w
                    sum_y += y * w
                    if is_upper:
                        upper_w += w
                        upper_x += x * w
                    else:
                        lower_w += w
                        lower_x += x * w
                    if prev is not None:
                        d = abs(val - prev[idx])
                        diff_sum += d
                        if d >= 14:
                            diff_w += d
                            diff_x += x * d
                            diff_y += y * d
            cx = (sum_x / total_w) / n if total_w > 0 else 0.5
            cy = (sum_y / total_w) / n if total_w > 0 else 0.5
            tilt = (
                ((upper_x / upper_w) - (lower_x / lower_w)) / n
                if (upper_w > 0 and lower_w > 0)
                else 0.0
            )
            if diff_w > n * 18.0:
                mcx = (diff_x / diff_w) / n
                mcy = (diff_y / diff_w) / n
                cx = 0.6 * cx + 0.4 * mcx
                cy = 0.6 * cy + 0.4 * mcy

        raw_energy = min(1.0, (diff_sum / max(1.0, float(n * n) * 28.0)))
        now = time.monotonic()

        with self._lock:
            if self._gen != gen_before:
                # reset() occurred while decoding off-lock; discard stale frame.
                return self.current_pose(now=now)
            self._prev_gray = gray
            if self._base_cx is None or self._base_cy is None or self._base_tilt is None:
                self._base_cx = cx
                self._base_cy = cy
                self._base_tilt = tilt
            else:
                # Slow neutral baseline drift prevents long-term lighting/offset creep
                self._base_cx = self._base_cx * 0.985 + cx * 0.015
                self._base_cy = self._base_cy * 0.985 + cy * 0.015
                self._base_tilt = self._base_tilt * 0.985 + tilt * 0.015

            raw_dx = max(-1.0, min(1.0, (cx - self._base_cx) * 6.5))
            raw_dy = max(-1.0, min(1.0, (cy - self._base_cy) * 6.5))
            tilt_delta = tilt - self._base_tilt
            raw_roll = max(-9.0, min(9.0, tilt_delta * 42.0 + raw_dx * 3.5))
            raw_scale = max(0.96, min(1.06, 1.0 + raw_energy * 0.045 - raw_dy * 0.02))

            a = self._alpha
            self._dx = self._dx * (1.0 - a) + raw_dx * a
            self._dy = self._dy * (1.0 - a) + raw_dy * a
            self._roll_deg = self._roll_deg * (1.0 - a) + raw_roll * a
            self._scale = self._scale * (1.0 - a) + raw_scale * a
            self._energy = self._energy * 0.55 + raw_energy * 0.45
            self._updated_at = now
            return {
                "dx": round(self._dx, 4),
                "dy": round(self._dy, 4),
                "roll_deg": round(self._roll_deg, 3),
                "scale": round(self._scale, 4),
                "energy": round(self._energy, 4),
                "updated_at": now,
            }

    def current_pose(self, now: float | None = None) -> dict[str, float]:
        ts = time.monotonic() if now is None else _finite_float(now, time.monotonic())
        with self._lock:
            if self._updated_at <= 0.0:
                return {
                    "dx": 0.0,
                    "dy": 0.0,
                    "roll_deg": 0.0,
                    "scale": 1.0,
                    "energy": 0.0,
                    "updated_at": 0.0,
                    "decayed": 1.0,
                }
            age = max(0.0, ts - self._updated_at)
            decay = 1.0 if age <= 1.2 else max(0.0, 1.0 - (age - 1.2) / 1.5)
            return {
                "dx": round(self._dx * decay, 4),
                "dy": round(self._dy * decay, 4),
                "roll_deg": round(self._roll_deg * decay, 3),
                "scale": round(1.0 + (self._scale - 1.0) * decay, 4),
                "energy": round(self._energy * decay, 4),
                "updated_at": self._updated_at,
                "decayed": 1.0,
            }


def apply_live_camera_motion(
    rgb_bytes: bytes, width: int, height: int, pose: dict[str, float]
) -> bytes:
    """Apply smooth edge-safe affine head/body motion transfer onto an RGB frame."""
    updated_at = _finite_float(pose.get("updated_at", 0.0) or 0.0, 0.0)
    already_decayed = bool(pose.get("decayed"))
    decay = 1.0
    if updated_at > 0.0 and not already_decayed:
        age = max(0.0, time.monotonic() - updated_at)
        decay = 1.0 if age <= 1.2 else max(0.0, 1.0 - (age - 1.2) / 1.5)
    dx = max(-1.0, min(1.0, _finite_float(pose.get("dx", 0.0) or 0.0, 0.0))) * decay
    dy = max(-1.0, min(1.0, _finite_float(pose.get("dy", 0.0) or 0.0, 0.0))) * decay
    roll_deg = max(-10.0, min(10.0, _finite_float(pose.get("roll_deg", 0.0) or 0.0, 0.0))) * decay
    raw_scale = max(0.95, min(1.08, _finite_float(pose.get("scale", 1.0) or 1.0, 1.0)))
    scale = 1.0 + (raw_scale - 1.0) * decay
    if (
        abs(dx) < 0.005
        and abs(dy) < 0.005
        and abs(roll_deg) < 0.05
        and abs(scale - 1.0) < 0.003
    ):
        return rgb_bytes

    expected = width * height * 3
    if len(rgb_bytes) != expected or width <= 0 or height <= 0:
        return rgb_bytes

    rad = math.radians(roll_deg)
    abs_cos = abs(math.cos(rad))
    abs_sin = abs(math.sin(rad))
    cx = width * 0.5
    cy = height * 0.5
    shift_x = dx * width * 0.032
    shift_y = dy * height * 0.028

    # Exact aspect-aware cross-coupled border-free overscan: guarantees every output
    # corner maps strictly inside [0, width] x [0, height] even with combined shift + roll.
    min_sx = (abs_cos * (cx + abs(shift_x)) + abs_sin * (cy + abs(shift_y))) / cx
    min_sy = (abs_sin * (cx + abs(shift_x)) + abs_cos * (cy + abs(shift_y))) / cy
    eff_scale = max(1.0, scale, min_sx, min_sy)

    inv_s = 1.0 / eff_scale
    cos_r = math.cos(rad) * inv_s
    sin_r = math.sin(rad) * inv_s

    a = cos_r
    b = sin_r
    c = cx - a * (cx + shift_x) - b * (cy + shift_y)
    d = -sin_r
    e = cos_r
    f = cy - d * (cx + shift_x) - e * (cy + shift_y)

    resampling = (
        Image.Resampling.BILINEAR
        if hasattr(Image, "Resampling")
        else getattr(Image, "BILINEAR", 2)
    )
    img = Image.frombytes("RGB", (width, height), rgb_bytes)
    warped = img.transform(
        (width, height),
        Image.Transform.AFFINE if hasattr(Image, "Transform") else Image.AFFINE,
        (a, b, c, d, e, f),
        resample=resampling,
    )
    return warped.tobytes()


def _deterministic_action_color(action_id: str) -> tuple[int, int, int]:
    digest = hashlib.sha256(action_id.encode("utf-8")).digest()
    return (
        40 + (digest[0] % 140),
        40 + (digest[1] % 140),
        50 + (digest[2] % 150),
    )


def _render_synthetic_rgb(
    width: int,
    height: int,
    *,
    action_id: str,
    frame_index: int,
    mouth_open: float,
    state: str,
) -> bytes:
    bg = _deterministic_action_color(action_id)
    img = Image.new("RGB", (width, height), color=bg)
    draw = ImageDraw.Draw(img)

    cx, cy = width // 2, height // 2
    head_r = max(12, min(width, height) // 5)
    sway = int(round(math.sin(frame_index * 0.25) * max(2, head_r // 8)))
    # Silhouette head circle
    draw.ellipse(
        (cx - head_r + sway, cy - head_r, cx + head_r + sway, cy + head_r),
        fill=(235, 205, 185),
        outline=(80, 60, 55),
        width=2,
    )
    # Eyes
    eye_y = cy - head_r // 4
    eye_dx = max(4, head_r // 3)
    eye_r = max(2, head_r // 10)
    for sign in (-1, 1):
        ex = cx + sway + sign * eye_dx
        draw.ellipse((ex - eye_r, eye_y - eye_r, ex + eye_r, eye_y + eye_r), fill=(35, 35, 45))
    # Mouth aperture driven by speech energy
    mouth_y = cy + head_r // 3
    mouth_w = max(4, head_r // 3)
    mouth_h = max(2, int(round(2 + min(1.0, max(0.0, mouth_open)) * (head_r // 2))))
    draw.ellipse(
        (
            cx + sway - mouth_w,
            mouth_y - mouth_h // 2,
            cx + sway + mouth_w,
            mouth_y + mouth_h // 2,
        ),
        fill=(185, 65, 75) if state == "SPEAKING" else (150, 85, 85),
    )
    return img.tobytes()


class ColabOneActionClipRingBuffer:
    """Ring-buffer managing loop and oneshot frame sequences for action-bank playback."""

    def __init__(self, max_frames: int = 300) -> None:
        self.max_frames = max(4, int(max_frames))
        self._clips: dict[str, dict[str, Any]] = {}

    def register_clip(
        self,
        action_id: str,
        frames: list[bytes],
        *,
        loop: bool = True,
        clip_path: str = "",
    ) -> None:
        aid = validate_colabone_live_id(action_id, "动作 ID")
        ring = deque(frames[: self.max_frames], maxlen=self.max_frames)
        self._clips[aid] = {
            "frames": list(ring),
            "loop": bool(loop),
            "clip_path": clip_path,
            "cursor": 0,
        }

    def has_clip(self, action_id: str) -> bool:
        return action_id in self._clips and bool(self._clips[action_id]["frames"])

    def reset_cursor(self, action_id: str) -> None:
        if action_id in self._clips:
            self._clips[action_id]["cursor"] = 0

    def set_loop(self, action_id: str, loop: bool) -> None:
        if action_id in self._clips:
            self._clips[action_id]["loop"] = bool(loop)

    def next_frame(self, action_id: str) -> tuple[bytes | None, bool]:
        """Return (rgb_bytes, completed_oneshot) for the requested action clip."""
        entry = self._clips.get(action_id)
        if not entry or not entry["frames"]:
            return None, False
        frames: list[bytes] = entry["frames"]
        idx = int(entry["cursor"])
        frame = frames[idx % len(frames)]
        if entry["loop"]:
            entry["cursor"] = (idx + 1) % len(frames)
            return frame, False
        if idx + 1 >= len(frames):
            entry["cursor"] = len(frames) - 1
            return frame, True
        entry["cursor"] = idx + 1
        return frame, False

    def list_action_ids(self) -> list[str]:
        return sorted(self._clips.keys())


class ColabOneVisualEngineBase(ABC):
    """Unified visual driver interface for Live Studio engines."""

    engine_id: str = "base"

    def __init__(
        self,
        *,
        width: int = 720,
        height: int = 1280,
        fps: int = 25,
        default_action: str = "idle_loop",
    ) -> None:
        self.width = int(width)
        self.height = int(height)
        self.fps = int(fps)
        self.default_action = default_action
        self._action_id = default_action
        self._action_loop = True
        self._frame_index = 0
        self._loaded = False
        self._disposed = False
        self._lock = threading.RLock()

        # Crossfade & live camera motion state
        self._last_rgb: bytes | None = None
        self._crossfade_from_rgb: bytes | None = None
        self._crossfade_step = 0
        self._crossfade_total = 0
        self._live_motion_pose: dict[str, float] | None = None

    @property
    def is_loaded(self) -> bool:
        return self._loaded

    @property
    def is_disposed(self) -> bool:
        return self._disposed

    def _ensure_not_disposed(self) -> None:
        if self._disposed:
            raise RuntimeError(f"视觉引擎 {self.engine_id} 已卸载释放，无法继续使用")

    @property
    def current_action(self) -> str:
        with self._lock:
            return self._action_id

    @abstractmethod
    def load(self) -> None:
        """Load engine resources / warm up caches."""

    @abstractmethod
    def unload(self) -> None:
        """Unload engine resources and release VRAM."""

    def set_action(
        self,
        action_id: str,
        *,
        loop: bool = True,
        clip_path: str = "",
        crossfade_frames: int = 8,
    ) -> dict[str, Any]:
        """Switch active visual action with optional smooth crossfade transition."""
        aid = validate_colabone_live_id(action_id, "动作 ID")
        with self._lock:
            self._ensure_not_disposed()
            if crossfade_frames <= 0:
                self._crossfade_from_rgb = None
                self._crossfade_step = 0
                self._crossfade_total = 0
            elif aid != self._action_id and self._last_rgb is not None:
                self._crossfade_from_rgb = self._last_rgb
                self._crossfade_step = 0
                self._crossfade_total = int(crossfade_frames)
            self._action_id = aid
            self._action_loop = bool(loop)
            self._on_action_changed(aid, loop=bool(loop), clip_path=clip_path)
            return {
                "action_id": self._action_id,
                "loop": self._action_loop,
                "crossfade_frames": self._crossfade_total,
            }

    def _on_action_changed(
        self, action_id: str, *, loop: bool, clip_path: str
    ) -> None:
        _ = (action_id, loop, clip_path)

    def _after_frame_rendered(self, rendered_rgb: bytes) -> None:
        """Hook invoked after a frame is finalized and crossfaded (under self._lock)."""
        _ = rendered_rgb

    def _apply_crossfade(self, raw_rgb: bytes) -> tuple[bytes, bool]:
        if self._crossfade_from_rgb is not None and self._crossfade_total > 0:
            self._crossfade_step += 1
            alpha = self._crossfade_step / float(self._crossfade_total)
            blended = blend_rgb_frames(
                self._crossfade_from_rgb, raw_rgb, self.width, self.height, alpha
            )
            if self._crossfade_step >= self._crossfade_total:
                self._crossfade_from_rgb = None
                self._crossfade_step = 0
                self._crossfade_total = 0
            self._last_rgb = blended
            return blended, True
        self._last_rgb = raw_rgb
        return raw_rgb, False

    @abstractmethod
    def _produce_raw_rgb(
        self, audio_pcm_24k: bytes, *, state: str
    ) -> bytes:
        """Produce a raw RGB frame of length width * height * 3."""

    def set_live_motion_pose(self, pose: dict[str, float] | None) -> None:
        """Set or clear live camera-driven motion pose vector (dx, dy, roll_deg, scale)."""
        with self._lock:
            if self._disposed:
                self._live_motion_pose = None
                return
            self._live_motion_pose = dict(pose) if isinstance(pose, dict) else None

    def render_frame(
        self,
        audio_pcm_24k: bytes = b"",
        *,
        epoch: int = 0,
        pts_ms: int = 0,
        state: str = "IDLE",
    ) -> ColabOneVisualFrame:
        with self._lock:
            self._ensure_not_disposed()
            if not self._loaded:
                self.load()
            frame_action_id = self._action_id
            raw_rgb = self._produce_raw_rgb(audio_pcm_24k, state=state)
            rgb, is_crossfading = self._apply_crossfade(raw_rgb)
            self._frame_index += 1
            self._after_frame_rendered(rgb)
            out_rgb = rgb
            if (
                self._live_motion_pose is not None
                and not (self.engine_id == "personalive_dance" and state == "PERFORMING")
            ):
                out_rgb = apply_live_camera_motion(
                    rgb, self.width, self.height, self._live_motion_pose
                )
            return ColabOneVisualFrame(
                width=self.width,
                height=self.height,
                rgb_bytes=out_rgb,
                pts_ms=int(pts_ms),
                epoch=int(epoch),
                action_id=frame_action_id,
                engine_id=self.engine_id,
                is_crossfading=is_crossfading,
            )

    def status(self) -> dict[str, Any]:
        with self._lock:
            return {
                "engine_id": self.engine_id,
                "loaded": self._loaded,
                "disposed": self._disposed,
                "width": self.width,
                "height": self.height,
                "fps": self.fps,
                "action_id": self._action_id,
                "frame_index": self._frame_index,
                "crossfading": self._crossfade_from_rgb is not None,
            }


class ColabOneSyntheticVisualEngine(ColabOneVisualEngineBase):
    """Offline deterministic RGB visual engine with smooth action crossfades."""

    engine_id = "synthetic"

    def __init__(
        self,
        *,
        width: int = 160,
        height: int = 120,
        fps: int = 25,
        default_action: str = "idle_loop",
    ) -> None:
        super().__init__(
            width=width, height=height, fps=fps, default_action=default_action
        )

    def load(self) -> None:
        with self._lock:
            self._ensure_not_disposed()
            self._loaded = True

    def unload(self) -> None:
        with self._lock:
            self._disposed = True
            self._loaded = False
            self._live_motion_pose = None
            self._last_rgb = None
            self._crossfade_from_rgb = None
            self._crossfade_step = 0
            self._crossfade_total = 0

    def _produce_raw_rgb(self, audio_pcm_24k: bytes, *, state: str) -> bytes:
        rms = compute_pcm16_rms(audio_pcm_24k)
        mouth_open = min(1.0, rms * 4.0) if audio_pcm_24k else (
            0.35 if state == "SPEAKING" else 0.0
        )
        return _render_synthetic_rgb(
            self.width,
            self.height,
            action_id=self._action_id,
            frame_index=self._frame_index,
            mouth_open=mouth_open,
            state=state,
        )


class ColabOneFlashHeadVisualEngine(ColabOneVisualEngineBase):
    """SoulX-FlashHead-1.3B Lite single-photo instant streaming portrait adapter."""

    engine_id = "flashhead_lite"

    def __init__(
        self,
        *,
        width: int = 720,
        height: int = 1280,
        fps: int = 25,
        portrait_path: Path | str | None = None,
        venv_python: Path | str | None = None,
        model_dir: Path | str | None = None,
        allow_fallback: bool = True,
    ) -> None:
        super().__init__(width=width, height=height, fps=fps)
        self.portrait_path = Path(portrait_path) if portrait_path else None
        self.venv_python = Path(venv_python) if venv_python else None
        self.model_dir = Path(model_dir) if model_dir else None
        self.allow_fallback = bool(allow_fallback)
        self._portrait_rgb: bytes | None = None
        self._audio_window: deque[bytes] = deque(maxlen=25)

    def load(self) -> None:
        with self._lock:
            self._ensure_not_disposed()
            if self._loaded:
                return
            if self.portrait_path is not None:
                if self.portrait_path.is_symlink():
                    raise ValueError("FlashHead 人像参考图片不能是符号链接")
                if self.portrait_path.is_file():
                    with Image.open(self.portrait_path) as src:
                        norm = ImageOps.exif_transpose(src)
                        self._portrait_rgb = _fit_rgb_image(norm, self.width, self.height)
                elif not self.allow_fallback:
                    raise RuntimeError("FlashHead 视觉引擎缺少有效的人像参考图片")
            elif not self.allow_fallback:
                raise RuntimeError("FlashHead 视觉引擎缺少有效的人像参考图片")
            self._loaded = True

    def unload(self) -> None:
        with self._lock:
            self._disposed = True
            self._loaded = False
            self._live_motion_pose = None
            self._portrait_rgb = None
            self._audio_window.clear()
            self._last_rgb = None
            self._crossfade_from_rgb = None
            self._crossfade_step = 0
            self._crossfade_total = 0

    def build_worker_command(self) -> list[str]:
        py = str(self.venv_python) if self.venv_python else sys.executable
        return [
            py,
            "-m",
            "flashhead.streaming_worker",
            "--model-dir",
            str(self.model_dir or ""),
            "--portrait",
            str(self.portrait_path or ""),
            "--fps",
            str(self.fps),
            "--width",
            str(self.width),
            "--height",
            str(self.height),
        ]

    def _produce_raw_rgb(self, audio_pcm_24k: bytes, *, state: str) -> bytes:
        if audio_pcm_24k:
            self._audio_window.append(audio_pcm_24k)
        if self._portrait_rgb is not None:
            return self._portrait_rgb
        rms = compute_pcm16_rms(audio_pcm_24k)
        return _render_synthetic_rgb(
            self.width,
            self.height,
            action_id=self._action_id,
            frame_index=self._frame_index,
            mouth_open=min(1.0, rms * 4.0),
            state=state,
        )


class ColabOneMuseTalkVisualEngine(ColabOneVisualEngineBase):
    """MuseTalk 1.5 action-bank lip-sync visual driver with ring-buffer & latent cache."""

    engine_id = "musetalk_bank"

    def __init__(
        self,
        *,
        width: int = 720,
        height: int = 1280,
        fps: int = 25,
        avatar_dir: Path | str | None = None,
        venv_python: Path | str | None = None,
        model_dir: Path | str | None = None,
        default_crossfade_frames: int = 8,
    ) -> None:
        super().__init__(width=width, height=height, fps=fps)
        self.avatar_dir = Path(avatar_dir) if avatar_dir else None
        self.venv_python = Path(venv_python) if venv_python else None
        self.model_dir = Path(model_dir) if model_dir else None
        self.default_crossfade_frames = max(0, int(default_crossfade_frames))
        self.ring_buffer = ColabOneActionClipRingBuffer()
        self._pending_return_to_default = False

    def latent_cache_dir(self) -> Path:
        if self.avatar_dir is None:
            raise ValueError("MuseTalk 视觉引擎未配置 avatar_dir，无法定位 latent 缓存目录")
        cache_dir = self.avatar_dir / ".musetalk_cache"
        if cache_dir.is_symlink():
            raise ValueError("MuseTalk latent 缓存目录不能是符号链接")
        return cache_dir

    def latent_cache_path(self, action_id: str) -> Path:
        aid = validate_colabone_live_id(action_id, "动作 ID")
        cache_dir = self.latent_cache_dir()
        target = cache_dir / f"{aid}.latents.pt"
        if target.is_symlink():
            raise ValueError("MuseTalk latent 缓存文件不能是符号链接")
        resolved_base = cache_dir.resolve()
        resolved_target = target.resolve()
        if not resolved_target.is_relative_to(resolved_base):
            raise ValueError("MuseTalk latent 缓存路径越界")
        return target

    def has_latent_cache(self, action_id: str) -> bool:
        try:
            path = self.latent_cache_path(action_id)
        except ValueError:
            return False
        return path.is_file() and not path.is_symlink()

    def register_action_frames(
        self, action_id: str, frames: list[bytes], *, loop: bool = True, clip_path: str = ""
    ) -> None:
        expected = self.width * self.height * 3
        for idx, frame in enumerate(frames):
            if len(frame) != expected:
                raise ValueError(
                    f"动作 {action_id} 第 {idx} 帧字节大小不匹配（期望 {expected}，实际 {len(frame)}）"
                )
        with self._lock:
            self._ensure_not_disposed()
            self.ring_buffer.register_clip(action_id, frames, loop=loop, clip_path=clip_path)

    def _try_load_clip_into_ring(
        self, action_id: str, clip_file: Path, *, loop: bool, clip_rel: str = ""
    ) -> bool:
        if self.avatar_dir is not None:
            if clip_file.is_symlink():
                return False
            try:
                if not clip_file.resolve().is_relative_to(self.avatar_dir.resolve()):
                    return False
            except OSError:
                return False
        frames = _decode_video_frames_rgb(
            clip_file, self.width, self.height, max_frames=self.ring_buffer.max_frames
        )
        if not frames:
            return False
        self.ring_buffer.register_clip(
            action_id, frames, loop=loop, clip_path=clip_rel or str(clip_file)
        )
        return True

    def _preload_avatar_clips_locked(self) -> None:
        if self.avatar_dir is None or self.avatar_dir.is_symlink() or not self.avatar_dir.is_dir():
            return
        loaded_ids: set[str] = set()
        manifest_path = self.avatar_dir / "manifest.json"
        if manifest_path.is_file() and not manifest_path.is_symlink():
            try:
                manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
                actions = manifest.get("actions") if isinstance(manifest, dict) else None
                if isinstance(actions, list):
                    for act in actions:
                        if not isinstance(act, dict):
                            continue
                        raw_id = str(act.get("id") or "").strip()
                        clip_rel = str(act.get("clip_path") or "").strip()
                        if not raw_id or not clip_rel:
                            continue
                        try:
                            aid = validate_colabone_live_id(raw_id, "动作 ID")
                        except ValueError:
                            continue
                        if self.ring_buffer.has_clip(aid):
                            loaded_ids.add(aid)
                            continue
                        cand = self.avatar_dir / clip_rel
                        kind = str(act.get("kind") or "loop").strip().lower()
                        if self._try_load_clip_into_ring(
                            aid, cand, loop=(kind == "loop"), clip_rel=clip_rel
                        ):
                            loaded_ids.add(aid)
            except Exception:
                pass

        clips_dir = self.avatar_dir / "clips"
        if clips_dir.is_dir() and not clips_dir.is_symlink():
            for entry in sorted(clips_dir.iterdir(), key=lambda p: p.name):
                if entry.is_symlink() or not entry.is_file():
                    continue
                ext = entry.suffix.lower().lstrip(".")
                if ext not in COLABONE_LIVE_CLIP_EXTENSIONS:
                    continue
                try:
                    aid = validate_colabone_live_id(entry.stem, "动作 ID")
                except ValueError:
                    continue
                if aid in loaded_ids or self.ring_buffer.has_clip(aid):
                    continue
                is_loop = aid.endswith("_loop") or aid.startswith("talk_")
                self._try_load_clip_into_ring(
                    aid, entry, loop=is_loop, clip_rel=f"clips/{entry.name}"
                )

    def _on_action_changed(
        self, action_id: str, *, loop: bool, clip_path: str
    ) -> None:
        self._pending_return_to_default = False
        if clip_path and not self.ring_buffer.has_clip(action_id):
            cand = Path(clip_path)
            if not cand.is_absolute() and self.avatar_dir is not None:
                cand = self.avatar_dir / clip_path
            if cand.is_file() and not cand.is_symlink():
                self._try_load_clip_into_ring(
                    action_id, cand, loop=loop, clip_rel=clip_path
                )
        if self.ring_buffer.has_clip(action_id):
            self.ring_buffer.set_loop(action_id, loop)
            self.ring_buffer.reset_cursor(action_id)

    def load(self) -> None:
        with self._lock:
            self._ensure_not_disposed()
            if self._loaded:
                return
            if self.avatar_dir is not None and self.avatar_dir.is_dir():
                cache_dir = self.latent_cache_dir()
                cache_dir.mkdir(parents=True, exist_ok=True)
                self._preload_avatar_clips_locked()
            self._loaded = True

    def unload(self) -> None:
        with self._lock:
            self._disposed = True
            self._loaded = False
            self._live_motion_pose = None
            self._last_rgb = None
            self._crossfade_from_rgb = None
            self._crossfade_step = 0
            self._crossfade_total = 0
            self._pending_return_to_default = False
            self.ring_buffer._clips.clear()

    def _after_frame_rendered(self, rendered_rgb: bytes) -> None:
        if not self._pending_return_to_default:
            return
        self._pending_return_to_default = False
        if self.default_crossfade_frames > 0:
            self._crossfade_from_rgb = rendered_rgb
            self._crossfade_step = 0
            self._crossfade_total = self.default_crossfade_frames
        self._action_id = self.default_action
        self._action_loop = True
        self.ring_buffer.reset_cursor(self.default_action)

    def _produce_raw_rgb(self, audio_pcm_24k: bytes, *, state: str) -> bytes:
        frame, completed_oneshot = self.ring_buffer.next_frame(self._action_id)
        if completed_oneshot and self._action_id != self.default_action:
            # Display the final oneshot frame intact on this tick, then transition
            # back to default_action (with crossfade queued for the next frame).
            self._pending_return_to_default = True
        if frame is not None:
            return frame
        rms = compute_pcm16_rms(audio_pcm_24k)
        return _render_synthetic_rgb(
            self.width,
            self.height,
            action_id=self._action_id,
            frame_index=self._frame_index,
            mouth_open=min(1.0, rms * 4.0),
            state=state,
        )


class ColabOnePersonaLiveVisualEngine(ColabOneVisualEngineBase):
    """Experimental PersonaLive single-photo driving-video pose/dance visual driver."""

    engine_id = "personalive_dance"

    def __init__(
        self,
        *,
        width: int = 720,
        height: int = 1280,
        fps: int = 25,
        portrait_path: Path | str | None = None,
        cache_dir: Path | str | None = None,
        venv_python: Path | str | None = None,
        model_dir: Path | str | None = None,
    ) -> None:
        super().__init__(width=width, height=height, fps=fps, default_action="dance_01")
        self.portrait_path = Path(portrait_path) if portrait_path else None
        self.cache_dir = Path(cache_dir) if cache_dir else None
        self.venv_python = Path(venv_python) if venv_python else None
        self.model_dir = Path(model_dir) if model_dir else None
        self._driving_clip_path: Path | None = None
        self._driving_frames: list[bytes] = []
        self._driving_cursor = 0
        self._driving_duration_seconds = 0.0
        self._driving_completed = False
        self._hot_swapped_out = False

    def tensorrt_cache_path(self, resolution: str | None = None) -> Path:
        if self.cache_dir is None:
            raise ValueError("PersonaLive 未配置 cache_dir，无法定位 TensorRT 缓存路径")
        res = resolution or f"{self.width}x{self.height}"
        w, h = parse_colabone_resolution(res)
        trt_dir = self.cache_dir / "trt"
        if trt_dir.is_symlink():
            raise ValueError("TensorRT 缓存目录不能是符号链接")
        target = trt_dir / f"personalive_{w}x{h}_fp16.plan"
        if target.is_symlink():
            raise ValueError("TensorRT 缓存文件不能是符号链接")
        if not target.resolve().is_relative_to(trt_dir.resolve()):
            raise ValueError("TensorRT 缓存路径越界")
        return target

    def load_driving_clip(
        self,
        clip_path: Path | str,
        *,
        allowed_root: Path | None = None,
        duration_seconds: float = 0.0,
    ) -> dict[str, Any]:
        """Validate and attach a driving motion clip for pose-driven dance playback."""
        with self._lock:
            self._ensure_not_disposed()
        path = Path(clip_path)
        if path.is_symlink():
            raise ValueError("驱动动作视频不能是符号链接")
        if not path.is_file():
            raise FileNotFoundError(f"驱动动作视频不存在: {path}")
        resolved = path.resolve()
        if allowed_root is not None and not resolved.is_relative_to(allowed_root.resolve()):
            raise ValueError("驱动动作视频路径越界")
        size = resolved.stat().st_size
        decoded_frames = _decode_video_frames_rgb(resolved, self.width, self.height)
        with self._lock:
            self._ensure_not_disposed()
            self._driving_clip_path = resolved
            self._driving_frames = decoded_frames
            self._driving_cursor = 0
            self._driving_duration_seconds = max(0.0, _finite_float(duration_seconds or 0.0, 0.0))
            self._driving_completed = False
        return {
            "path": str(resolved),
            "size": size,
            "fps": self.fps,
            "resolution": f"{self.width}x{self.height}",
            "decoded_frames": len(decoded_frames),
        }

    def set_driving_frames(self, frames: list[bytes]) -> None:
        expected = self.width * self.height * 3
        for idx, frame in enumerate(frames):
            if len(frame) != expected:
                raise ValueError(f"驱动帧 {idx} 尺寸不匹配")
        with self._lock:
            self._ensure_not_disposed()
            self._driving_frames = list(frames)
            self._driving_cursor = 0
            self._driving_completed = False

    def _on_action_changed(
        self, action_id: str, *, loop: bool, clip_path: str
    ) -> None:
        _ = (action_id, loop, clip_path)
        self._driving_cursor = 0
        self._driving_completed = False

    def consume_driving_completed(self) -> bool:
        with self._lock:
            done = self._driving_completed
            self._driving_completed = False
            return done

    def _release_runtime_resources_locked(self) -> None:
        self._loaded = False
        self._live_motion_pose = None
        self._last_rgb = None
        self._crossfade_from_rgb = None
        self._crossfade_step = 0
        self._crossfade_total = 0
        self._driving_frames.clear()
        self._driving_cursor = 0
        self._driving_completed = False

    def load(self) -> None:
        with self._lock:
            self._ensure_not_disposed()
            if self.cache_dir is not None:
                trt_dir = self.cache_dir / "trt"
                if trt_dir.is_symlink():
                    raise ValueError("TensorRT 缓存目录不能是符号链接")
                trt_dir.mkdir(parents=True, exist_ok=True)
            clip_to_decode = (
                self._driving_clip_path
                if (self._driving_clip_path is not None and not self._driving_frames)
                else None
            )
            w, h = self.width, self.height

        decoded_frames: list[bytes] = []
        if clip_to_decode is not None:
            if not clip_to_decode.is_symlink() and clip_to_decode.is_file():
                decoded_frames = _decode_video_frames_rgb(clip_to_decode, w, h)

        with self._lock:
            self._ensure_not_disposed()
            if (
                clip_to_decode is not None
                and self._driving_clip_path == clip_to_decode
                and not self._driving_frames
                and decoded_frames
            ):
                self._driving_frames = decoded_frames
                self._driving_cursor = 0
                self._driving_completed = False
            self._loaded = True
            self._hot_swapped_out = False

    def unload(self) -> None:
        with self._lock:
            self._disposed = True
            self._release_runtime_resources_locked()

    def hot_swap_unload(self) -> None:
        """Temporarily unload PersonaLive weights during hot-swap back to primary visual engine."""
        with self._lock:
            self._release_runtime_resources_locked()
            self._hot_swapped_out = True

    def hot_swap_reload(self) -> None:
        """Reload PersonaLive weights on demand for dance performance."""
        self.load()

    def _produce_raw_rgb(self, audio_pcm_24k: bytes, *, state: str) -> bytes:
        if self._driving_frames:
            n_frames = len(self._driving_frames)
            idx = self._driving_cursor % n_frames
            frame = self._driving_frames[idx]
            if self._action_loop:
                self._driving_cursor = (idx + 1) % n_frames
            else:
                if idx + 1 >= n_frames:
                    self._driving_cursor = n_frames - 1
                    self._driving_completed = True
                else:
                    self._driving_cursor = idx + 1
            return frame
        if not self._action_loop:
            total_frames = max(
                2,
                int(round(self._driving_duration_seconds * self.fps))
                if self._driving_duration_seconds > 0.0
                else max(2, self.fps),
            )
            self._driving_cursor += 1
            if self._driving_cursor >= total_frames:
                self._driving_completed = True
        rms = compute_pcm16_rms(audio_pcm_24k)
        return _render_synthetic_rgb(
            self.width,
            self.height,
            action_id=self._action_id,
            frame_index=self._frame_index,
            mouth_open=min(1.0, rms * 3.0),
            state=state,
        )

    def status(self) -> dict[str, Any]:
        info = super().status()
        with self._lock:
            info["experimental"] = True
            info["commercial_ok"] = False
            info["driving_clip"] = str(self._driving_clip_path) if self._driving_clip_path else ""
            info["hot_swapped_out"] = self._hot_swapped_out
        return info


def create_colabone_visual_engine(
    engine_id: str,
    *,
    synthetic: bool = False,
    resolution: str = "720x1280",
    fps: int = 25,
    **kwargs: Any,
) -> ColabOneVisualEngineBase:
    """Factory instantiating the requested visual engine adapter."""
    width, height = parse_colabone_resolution(resolution)
    if synthetic:
        return ColabOneSyntheticVisualEngine(width=width, height=height, fps=fps)
    if engine_id == "flashhead_lite":
        return ColabOneFlashHeadVisualEngine(width=width, height=height, fps=fps, **kwargs)
    if engine_id == "musetalk_bank":
        return ColabOneMuseTalkVisualEngine(width=width, height=height, fps=fps, **kwargs)
    if engine_id == "personalive_dance":
        return ColabOnePersonaLiveVisualEngine(width=width, height=height, fps=fps, **kwargs)
    raise ValueError(f"不支持的视觉引擎: {engine_id!r}")
