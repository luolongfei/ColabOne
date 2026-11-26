"""Remote native digital-human avatar & diffusion VSR engine manager.

Manages isolated virtual environments, pinned upstream Git checkouts, lazy
Hugging Face weight snapshots (with optional Google Drive persistent cache
backing), hardware/disk preflight checks, and cancelable streaming worker
subprocesses for:

- Stage 2: LongCat-Video-Avatar 1.6 (native pure BF16 flagship on 96 GB GPUs)
- Stage 3: SeedVR2 7B (sharp / standard) and FlashVSR+ v1.1 diffusion VSR

Runs exclusively on the remote GPU runtime; all allow-pattern selectors,
progress line parsers, and environment/preflight flows are 100% offline
unit-testable via injectable callbacks.
"""

from __future__ import annotations

import collections
import copy
import hashlib
import json
import math
import os
import queue
import re
import shutil
import site
import stat
import subprocess
import sys
import sysconfig
import tempfile
import threading
import time
from contextlib import nullcontext
from pathlib import Path, PurePosixPath
from typing import Any, Callable, Iterator, Sequence

from common.files import atomic_json
from server.tts_engines import (
    _call_with_compatible_kwargs,
    _collect_installed_torch_constraints,
    _extract_pyproject_dependencies,
    _extract_requirements_file_packages,
    _filter_pip_packages,
    _find_venv_python_on_disk,
    _merge_requirement_specs,
    _normalize_git_repo_url,
    _read_log_tail,
    _resolve_venv_python,
    _safe_task_note,
    _scrub_secret,
    _terminate_process_group,
    _write_torch_constraints_file,
)

# ---------------------------------------------------------------------------
# 1. Pinned upstream repositories, commits, and Hugging Face revisions
# ---------------------------------------------------------------------------

COLABONE_LONGCAT16_REPO = "https://github.com/meituan-longcat/LongCat-Video.git"
COLABONE_LONGCAT16_COMMIT = "58eb336edc5c8c63389838727e40b8c18434e54e"
COLABONE_LONGCAT16_AVATAR_HF_REPO = "meituan-longcat/LongCat-Video-Avatar-1.6"
COLABONE_LONGCAT16_AVATAR_HF_REV = "acfc628244201fc53fb6fe8a8903c5bac9bdf68f"
COLABONE_LONGCAT16_BASE_HF_REPO = "meituan-longcat/LongCat-Video"
COLABONE_LONGCAT16_BASE_HF_REV = "03b55529b1d1d4045f5fbe14d65c8c6e8116b278"

COLABONE_SEEDVR2_REPO = "https://github.com/numz/ComfyUI-SeedVR2_VideoUpscaler.git"
COLABONE_SEEDVR2_COMMIT = "4490bd1f482e026674543386bb2a4d176da245b9"
COLABONE_SEEDVR2_HF_REPO = "numz/SeedVR2_comfyUI"
COLABONE_SEEDVR2_HF_REV = "09ced71023636e9bc8cdf9cdecfb2625d1e691e8"

COLABONE_FLASHVSR_REPO = "https://github.com/OpenImagingLab/FlashVSR_Plus.git"
COLABONE_FLASHVSR_COMMIT = "b515ffc6dba57fdc9ad84c66cadd87d05f27590a"
COLABONE_FLASHVSR_HF_REPO = "JunhaoZhuang/FlashVSR-v1.1"
COLABONE_FLASHVSR_HF_REV = "2aa7e1f3ab50e743537d1eca243fb0bff442b336"

COLABONE_PROGRESS_PREFIX = "COLABONE_PROGRESS:"

COLABONE_SUPPORTED_VSR_ENGINES = frozenset(
    {"seedvr2_7b_sharp", "seedvr2_7b", "flashvsr_plus"}
)
SUPPORTED_VSR_ENGINES = COLABONE_SUPPORTED_VSR_ENGINES

_GIT_COMMIT_SHA_RE = re.compile(r"^[0-9a-fA-F]{40}$")
_SENSITIVE_ENV_KEYS = frozenset(
    {
        "HF_TOKEN",
        "HUGGING_FACE_HUB_TOKEN",
        "HUGGINGFACE_HUB_TOKEN",
        "HF_API_TOKEN",
    }
)
_SETUP_STRIP_ENV_KEYS = frozenset(
    {
        "PIP_USER",
        "PIP_TARGET",
        "PIP_PREFIX",
        "PIP_CONSTRAINT",
        "PIP_REQUIRE_VIRTUALENV",
        "PYTHONUSERBASE",
        "PYTHONPATH",
        "PYTHONNOUSERSITE",
    }
)
_SENSITIVE_CONFIG_KEYS = frozenset(
    {
        "hf_token",
        "hugging_face_hub_token",
        "huggingface_hub_token",
        "hf_api_token",
        "token",
        "authorization",
    }
)
_HF_TRANSFER_INCOMPATIBLE_ENV_KEYS = frozenset(
    {
        "http_proxy",
        "https_proxy",
        "all_proxy",
        "requests_ca_bundle",
        "ssl_cert_file",
        "curl_ca_bundle",
    }
)
_HF_TRANSFER_ACTIVE_MARKER = "COLABONE_HF_TRANSFER_ACTIVE=1"
_HF_NON_RETRYABLE_STATUS_RE = re.compile(
    r"(?:"
    r"(?:http(?:\s+error|\s+status)?|status(?:\s+code)?|client\s+error)\s*[:=(]?\s*(?:401|403|404)\b"
    r"|\b(?:401|403|404)\s+(?:client\s+error|unauthorized|forbidden|not\s+found)\b"
    r"|\b(?:401|403|404)\b[^\n]{0,24}\b(?:unauthorized|forbidden|not\s+found)\b"
    r")",
    re.IGNORECASE,
)
_HF_EXIT_CODE_2_RE = re.compile(r"退出码\s*2\b")
_HF_NON_RETRYABLE_MARKERS = (
    "缺少 huggingface_hub",
    "no module named 'huggingface_hub'",
    'no module named "huggingface_hub"',
    "执行超时",
    "timeoutexpired",
    "gatedrepoerror",
    "repositorynotfounderror",
    "revisionnotfounderror",
    "entrynotfounderror",
    "no space left on device",
    "enospc",
    "errno 28",
    "disk quota exceeded",
    "edquot",
    "errno 122",
    "cannot access gated repo",
    "invalid credentials",
)

_WORKSPACE_HEADROOM_MIB = 6144  # ~6 GiB workspace headroom on repeated cached runs
_HIGH_VRAM_96G_THRESHOLD_MIB = 80000

_LONGCAT16_AVATAR_CORE_MIB = 33744
_LONGCAT16_VOCAL_SEP_MIB = 256
_LONGCAT16_AVATAR_BASE_MIB = _LONGCAT16_AVATAR_CORE_MIB + _LONGCAT16_VOCAL_SEP_MIB
_LONGCAT16_LORA_MIB = 5000
_LONGCAT16_AVATAR_ESTIMATED_MIB = _LONGCAT16_AVATAR_BASE_MIB
_LONGCAT16_BASE_ESTIMATED_MIB = 22800
_SEEDVR2_DIT_MIB = 16000
_SEEDVR2_VAE_MIB = 600
_SEEDVR2_ESTIMATED_MIB = _SEEDVR2_DIT_MIB + _SEEDVR2_VAE_MIB
_FLASHVSR_ESTIMATED_MIB = 6300

_INCOMPLETE_ARTIFACT_SUFFIXES = (".incomplete", ".tmp", ".part")
_SHARD_INDEX_SUFFIXES = (".safetensors.index.json", ".bin.index.json")

_MAX_LOG_FILE_BYTES = 5 * 1024 * 1024  # 5 MiB truncation threshold
_MAX_STREAM_LINE_BYTES = 65536  # 64 KiB cap per streamed stdout/stderr line
_DEFAULT_WORKER_INACTIVITY_TIMEOUT_S = 900  # 15 min silence / no-progress watchdog
_TQDM_STEP_PROGRESS_RE = re.compile(r"(?:^|\|\s*)(\d{1,4})/(\d{1,4})\s*\[")
_TERMINAL_WORKER_ERROR_MARKERS = (
    "[colabone-longcat16] ERROR:",
    "ColabOne diffusion VSR worker failed:",
)
_WORKER_DIAG_ERROR_MARKERS = (
    "[colabone-longcat16] ERROR:",
    "ColabOne diffusion VSR worker failed:",
    "CUDA out of memory",
    "OutOfMemoryError:",
    "RuntimeError:",
    "AssertionError:",
    "AttributeError:",
    "ModuleNotFoundError:",
    "ImportError:",
    "FileNotFoundError:",
    "ValueError:",
)
_WORKER_DIAG_WARNING_MARKERS = (
    "[colabone-longcat16] WARNING:",
    "[colabone-vsr] WARNING:",
)

_AVATAR_WEIGHT_EXTS = frozenset(
    {".safetensors", ".bin", ".onnx", ".ckpt", ".pth", ".pt"}
)
_WEIGHT_BEARING_SUBDIRS = frozenset(
    {"base_model", "loraA", "loraB", "vocal_separator", "text_encoder", "vae"}
)
_OPTIONAL_METADATA_PATTERNS = frozenset({"config.json", "model_index.json"})

# Curated Colab Python 3.11/3.12 + PyTorch 2.6-2.9 + CUDA 12.x safe dependencies.
# Leaves sympy and numpy unpinned to match Colab's system PyTorch ABI; never scrapes
# LongCat's raw container-freeze requirements_avatar.txt.
_LONGCAT16_CORE_PACKAGES: tuple[str, ...] = (
    "diffusers>=0.32.0,<0.36",
    "transformers>=4.41.0,<5.0",
    "accelerate>=0.33.0",
    "safetensors>=0.4.3",
    "huggingface_hub>=0.34.0,<1.0",
    "einops>=0.8.0",
    "ftfy>=6.2.0",
    "regex",
    "sentencepiece",
    "protobuf>=4.25.1",
    "loguru",
    "tqdm",
    "pillow",
    "scipy",
    "soundfile",
    "librosa",
    "soxr",
    "pyloudnorm",
    "imageio",
    "imageio-ffmpeg",
    "psutil",
    "onnxruntime-gpu",
)
_LONGCAT16_OPTIONAL_PACKAGES: tuple[str, ...] = ("audio-separator",)
_LONGCAT16_EXTRA_PACKAGES: tuple[str, ...] = (
    _LONGCAT16_CORE_PACKAGES + _LONGCAT16_OPTIONAL_PACKAGES
)

_SEEDVR2_EXTRA_PACKAGES: tuple[str, ...] = (
    "einops",
    "omegaconf",
    "rotary-embedding-torch",
    "diffusers",
    "safetensors",
    "huggingface_hub",
    "pillow",
    "numpy",
    "tqdm",
)

_FLASHVSR_EXTRA_PACKAGES: tuple[str, ...] = (
    "einops",
    "safetensors",
    "huggingface_hub",
    "pillow",
    "numpy",
    "tqdm",
)

_EOF_SENTINEL = object()


# ---------------------------------------------------------------------------
# 2. Pure-Python weight allow-pattern & progress parsing helpers
# ---------------------------------------------------------------------------


def longcat16_avatar_allow_patterns(
    sampling_mode: str = "non_distill_cfg",
    vocal_separation: str = "auto",
) -> list[str]:
    """Return HF allow_patterns for LongCat-Video-Avatar 1.6 (pure BF16, never int8)."""
    mode = str(sampling_mode or "non_distill_cfg").strip().lower()
    if mode == "auto":
        mode = "non_distill_cfg"
    vocal = str(vocal_separation or "auto").strip().lower()
    patterns: list[str] = [
        "base_model/**",
        "whisper-large-v3/model.safetensors",
        "whisper-large-v3/*.json",
        "whisper-large-v3/*.txt",
        "scheduler/**",
        "config.json",
        "model_index.json",
    ]
    if mode == "distill_lora_mix":
        patterns.extend(["loraA/**", "loraB/**"])
    if vocal != "never":
        patterns.append("vocal_separator/**")
    return patterns


def longcat16_base_allow_patterns() -> list[str]:
    """Return strict HF allow_patterns for LongCat-Video base components (no dit/**)."""
    return [
        "tokenizer/**",
        "text_encoder/**",
        "vae/**",
        "config.json",
        "model_index.json",
    ]


def vsr_engine_allow_patterns(engine: str) -> tuple[str, str, list[str]]:
    """Return (hf_repo, hf_rev, allow_patterns) for a supported diffusion VSR engine."""
    key = str(engine or "").strip().lower()
    if key == "seedvr2_7b_sharp":
        return (
            COLABONE_SEEDVR2_HF_REPO,
            COLABONE_SEEDVR2_HF_REV,
            [
                "seedvr2_ema_7b_sharp_fp16.safetensors",
                "ema_vae_fp16.safetensors",
            ],
        )
    if key == "seedvr2_7b":
        return (
            COLABONE_SEEDVR2_HF_REPO,
            COLABONE_SEEDVR2_HF_REV,
            [
                "seedvr2_ema_7b_fp16.safetensors",
                "ema_vae_fp16.safetensors",
            ],
        )
    if key == "flashvsr_plus":
        return (
            COLABONE_FLASHVSR_HF_REPO,
            COLABONE_FLASHVSR_HF_REV,
            [
                "diffusion_pytorch_model_streaming_dmd.safetensors",
                "LQ_proj_in.ckpt",
                "TCDecoder.ckpt",
                "config.json",
                "model_index.json",
            ],
        )
    raise ValueError(
        f"不支持的远端扩散超分引擎：{engine!r}（仅支持 seedvr2_7b_sharp、seedvr2_7b、flashvsr_plus）"
    )


def parse_colabone_progress_line(line: str) -> dict[str, Any] | None:
    """Safely parse a `COLABONE_PROGRESS:{...}` line into a dict, or None if not a match."""
    if not isinstance(line, str) or not line:
        return None
    idx = line.find(COLABONE_PROGRESS_PREFIX)
    if idx < 0:
        return None
    raw_json = line[idx + len(COLABONE_PROGRESS_PREFIX) :].strip()
    if not raw_json or not raw_json.startswith("{"):
        return None
    try:
        parsed = json.loads(raw_json)
    except (ValueError, TypeError):
        return None
    if not isinstance(parsed, dict):
        return None
    return parsed


# ---------------------------------------------------------------------------
# 3. Internal filesystem, task, venv, and subprocess helpers
# ---------------------------------------------------------------------------


def _atomic_write_text(path: Path, text: str) -> None:
    if path.is_symlink():
        raise ValueError(f"目标文件不能是符号链接：{path.name}")
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp_name = tempfile.mkstemp(
        prefix=f".{path.name}.", suffix=".tmp", dir=str(path.parent)
    )
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as fh:
            fh.write(text)
            fh.flush()
            try:
                os.fsync(fh.fileno())
            except OSError:
                pass
        os.replace(tmp_name, path)
    finally:
        if os.path.exists(tmp_name):
            try:
                os.unlink(tmp_name)
            except OSError:
                pass


def _ensure_not_symlink(path: Path, label: str) -> None:
    if path.is_symlink():
        raise ValueError(f"{label}不能是符号链接：{path}")


def _validate_contained_path(path: Path, work_dir: Path, label: str) -> Path:
    """Validate `path` has no symlink components inside `work_dir` and resolves within it."""
    raw = os.fspath(path)
    if not raw or not str(raw).strip() or any(ch in str(raw) for ch in ("\r", "\n", "\x00")):
        raise ValueError(f"{label} 路径不合法：{path!r}")
    _ensure_not_symlink(work_dir, "工作目录")
    work_root = work_dir.resolve()
    candidate = path if path.is_absolute() else (work_root / path)

    # Check unresolved path components up to work_dir / work_root before resolving.
    check_cur = candidate
    for _ in range(128):
        if check_cur.is_symlink():
            raise ValueError(f"{label} 路径不能包含符号链接：{check_cur}")
        if check_cur == work_dir or check_cur == work_root:
            break
        parent = check_cur.parent
        if parent == check_cur:
            break
        check_cur = parent

    resolved = candidate.resolve()
    if not resolved.is_relative_to(work_root):
        raise ValueError(f"{label} 必须位于工作目录内：{path}")
    rel_parts = resolved.relative_to(work_root).parts
    if not rel_parts:
        raise ValueError(f"{label} 不能指向工作目录本身：{path}")

    cur = work_dir
    for part in rel_parts:
        cur = cur / part
        if cur.is_symlink():
            raise ValueError(f"{label} 路径不能包含符号链接：{cur}")
    return resolved


def _safe_nonneg_int(value: Any, default: int = 0) -> int:
    if isinstance(value, bool) or value is None:
        return default
    try:
        num = float(value)
    except (TypeError, ValueError):
        return default
    if not math.isfinite(num) or num < 0:
        return default
    return int(num)


def _json_safe(value: Any) -> Any:
    if isinstance(value, Path):
        return str(value)
    if isinstance(value, dict):
        return {str(k): _json_safe(v) for k, v in value.items()}
    if isinstance(value, (list, tuple)):
        return [_json_safe(v) for v in value]
    return value


def _strip_sensitive_config_keys(value: Any) -> Any:
    """Recursively strip token/auth secret keys before persisting worker config JSON."""
    if isinstance(value, dict):
        return {
            k: _strip_sensitive_config_keys(v)
            for k, v in value.items()
            if str(k).strip().lower() not in _SENSITIVE_CONFIG_KEYS
        }
    if isinstance(value, list):
        return [_strip_sensitive_config_keys(v) for v in value]
    return value


def _sanitized_env(extra: dict[str, str] | None = None) -> dict[str, str]:
    blocked = _SENSITIVE_ENV_KEYS | _SETUP_STRIP_ENV_KEYS
    env = {k: v for k, v in os.environ.items() if k not in blocked}
    if extra:
        env.update(extra)
    return env


def _summarize_error_tail(
    lines: Sequence[str],
    fallback_text: str = "",
    *,
    max_chars: int = 800,
) -> str:
    cleaned = [str(line).strip() for line in lines if str(line).strip()]
    if not cleaned and fallback_text:
        cleaned = [ln.strip() for ln in str(fallback_text).splitlines() if ln.strip()]
    if not cleaned:
        return ""
    budget = max(80, int(max_chars))
    kept: list[str] = []
    used = 0
    for line in reversed(cleaned):
        sep = 1 if kept else 0
        if kept and used + sep + len(line) > budget:
            break
        if not kept and len(line) > budget:
            return line[-budget:]
        kept.append(line)
        used += sep + len(line)
    kept.reverse()
    return "\n".join(kept)


def _normalize_opencv_package_spec(spec: str) -> str:
    """Normalize GUI opencv-python specs to headless variants to avoid libGL.so.1 errors."""
    cleaned = str(spec or "").strip()
    if not cleaned:
        return ""
    match = re.match(r"^([A-Za-z0-9_.-]+)(.*)$", cleaned)
    if not match:
        return cleaned
    raw_name, rest = match.group(1), match.group(2)
    norm_name = re.sub(r"[-_.]+", "-", raw_name).lower()
    if norm_name == "opencv-python":
        return f"opencv-python-headless{rest}"
    if norm_name == "opencv-contrib-python":
        return f"opencv-contrib-python-headless{rest}"
    return cleaned


def _collect_parent_venv_site_packages() -> list[str]:
    """Return site-packages directories of the host virtualenv when sys.prefix != sys.base_prefix."""
    try:
        prefix = Path(sys.prefix).resolve()
        base_prefix = Path(sys.base_prefix).resolve()
    except OSError:
        return []
    if prefix == base_prefix:
        return []

    base_site_set: set[Path] = set()
    try:
        for p in site.getsitepackages([sys.base_prefix]):
            try:
                base_site_set.add(Path(p).resolve())
            except OSError:
                continue
    except Exception:
        pass

    raw_dirs: list[str] = []
    try:
        raw_dirs.extend(site.getsitepackages([sys.prefix]))
    except Exception:
        pass
    try:
        paths = sysconfig.get_paths()
        for key in ("purelib", "platlib"):
            val = paths.get(key)
            if val:
                raw_dirs.append(val)
    except Exception:
        pass

    candidates: list[str] = []
    seen: set[Path] = set()
    for raw in raw_dirs:
        if not raw or any(ch in raw for ch in ("\r", "\n", "\x00")):
            continue
        p = Path(raw)
        try:
            if p.is_symlink() or not p.is_dir():
                continue
            rp = p.resolve()
        except OSError:
            continue
        if rp in seen or rp in base_site_set:
            continue
        if not rp.is_relative_to(prefix):
            continue
        seen.add(rp)
        candidates.append(str(rp))
    return candidates


def _find_venv_site_packages_dir(venv_dir: Path) -> Path | None:
    """Locate the site-packages directory inside `venv_dir`, if present on disk."""
    candidates: list[Path] = [
        venv_dir
        / "lib"
        / f"python{sys.version_info.major}.{sys.version_info.minor}"
        / "site-packages",
        venv_dir
        / "lib64"
        / f"python{sys.version_info.major}.{sys.version_info.minor}"
        / "site-packages",
        venv_dir / "Lib" / "site-packages",
    ]
    lib_dir = venv_dir / "lib"
    if lib_dir.is_dir() and not lib_dir.is_symlink():
        try:
            for child in sorted(lib_dir.iterdir()):
                if (
                    child.name.startswith("python")
                    and child.is_dir()
                    and not child.is_symlink()
                ):
                    candidates.append(child / "site-packages")
        except OSError:
            pass
    for cand in candidates:
        try:
            if not cand.is_symlink() and cand.is_dir():
                return cand
        except OSError:
            continue
    return None


def _ensure_parent_venv_pth(venv_dir: Path) -> Path | None:
    """Write a .pth file into child venv site-packages so parent-venv PyTorch stays visible."""
    parent_sites = _collect_parent_venv_site_packages()
    if not parent_sites:
        return None
    child_site = _find_venv_site_packages_dir(venv_dir)
    if child_site is None:
        return None
    pth_path = child_site / "_colabone_parent_venv.pth"
    _ensure_not_symlink(pth_path, "虚拟环境继承路径文件")
    content = "\n".join(parent_sites) + "\n"
    try:
        if pth_path.is_file() and pth_path.read_text(encoding="utf-8") == content:
            return pth_path
    except OSError:
        pass
    _atomic_write_text(pth_path, content)
    return pth_path


def _has_hf_transfer_incompatible_env(env: dict[str, str]) -> bool:
    for key, val in env.items():
        if key.lower() in _HF_TRANSFER_INCOMPATIBLE_ENV_KEYS and str(val or "").strip():
            return True
    return False


def _is_non_retryable_hf_download_error(err_text: str) -> bool:
    text = str(err_text or "")
    if not text:
        return False
    lower = text.lower()
    if any(marker in lower for marker in _HF_NON_RETRYABLE_MARKERS):
        return True
    if _HF_EXIT_CODE_2_RE.search(text):
        return True
    if _HF_NON_RETRYABLE_STATUS_RE.search(text):
        return True
    return False


def _read_log_attempt_head_tail(
    log_path: Path, start_offset: int, *, window_bytes: int = 65536
) -> tuple[str, str]:
    try:
        if log_path.is_symlink() or not log_path.is_file():
            return "", ""
        cur_size = log_path.stat().st_size
        pos = start_offset if 0 <= start_offset <= cur_size else 0
        if pos >= cur_size:
            return "", ""
        with log_path.open("rb") as fh:
            fh.seek(pos)
            head_raw = fh.read(window_bytes)
            if cur_size - pos > window_bytes:
                tail_pos = max(pos + window_bytes, cur_size - window_bytes)
                fh.seek(tail_pos)
                tail_raw = fh.read(cur_size - tail_pos)
            else:
                tail_raw = head_raw
        return (
            head_raw.decode("utf-8", errors="replace"),
            tail_raw.decode("utf-8", errors="replace"),
        )
    except OSError:
        return "", ""


def _find_all_venv_site_packages_dirs(venv_dir: Path) -> list[Path]:
    """Locate all non-symlink site-packages directories directly inside `venv_dir`."""
    if venv_dir.is_symlink() or not venv_dir.is_dir():
        return []
    try:
        venv_resolved = venv_dir.resolve()
    except OSError:
        return []
    candidates: list[Path] = []
    for lib_name in ("lib", "lib64"):
        lib_dir = venv_dir / lib_name
        try:
            if lib_dir.is_symlink() or not lib_dir.is_dir():
                continue
            for child in sorted(lib_dir.iterdir()):
                if (
                    child.name.startswith("python")
                    and not child.is_symlink()
                    and child.is_dir()
                ):
                    sp = child / "site-packages"
                    if not sp.is_symlink() and sp.is_dir():
                        candidates.append(sp)
        except OSError:
            continue
    win_lib = venv_dir / "Lib"
    try:
        if not win_lib.is_symlink() and win_lib.is_dir():
            win_sp = win_lib / "site-packages"
            if not win_sp.is_symlink() and win_sp.is_dir():
                candidates.append(win_sp)
    except OSError:
        pass
    result: list[Path] = []
    seen: set[Path] = set()
    for sp in candidates:
        try:
            rp = sp.resolve()
        except OSError:
            continue
        if rp in seen or not rp.is_relative_to(venv_resolved):
            continue
        seen.add(rp)
        result.append(sp)
    return result


def _parse_dist_info_version(info_path: Path) -> str:
    """Extract package Version from .dist-info/METADATA or .egg-info/PKG-INFO or filename."""
    meta_candidates: list[Path] = []
    if info_path.is_dir():
        meta_candidates.extend([info_path / "METADATA", info_path / "PKG-INFO"])
    elif info_path.is_file():
        meta_candidates.append(info_path)
    for meta in meta_candidates:
        try:
            if meta.is_symlink() or not meta.is_file():
                continue
            for line in meta.read_text(encoding="utf-8", errors="ignore").splitlines():
                if not line.strip():
                    break
                if line.lower().startswith("version:"):
                    val = line.split(":", 1)[1].strip()
                    if val:
                        return val
        except OSError:
            continue
    stem = info_path.name
    for suffix in (".dist-info", ".egg-info"):
        if stem.lower().endswith(suffix):
            stem = stem[: -len(suffix)]
            break
    parts = stem.split("-", 1)
    return parts[1].strip() if len(parts) == 2 else ""


def _prune_shadowing_sageattention(venv_dir: Path, caps: dict[str, Any]) -> None:
    """Remove shadowing PyPI sageattention (< 2.0) in child venv when host provides SageAttention 2+."""
    if not isinstance(caps, dict) or not bool(caps.get("sageattn_available")):
        return
    for site_dir in _find_all_venv_site_packages_dirs(venv_dir):
        try:
            site_resolved = site_dir.resolve()
            entries = list(site_dir.iterdir())
        except OSError:
            continue
        shadow_infos: list[Path] = []
        has_v2_plus = False
        for entry in entries:
            lower_name = entry.name.lower()
            if not (
                lower_name.startswith("sageattention-")
                and (
                    lower_name.endswith(".dist-info")
                    or lower_name.endswith(".egg-info")
                )
            ):
                continue
            if entry.is_symlink():
                continue
            ver_str = _parse_dist_info_version(entry)
            ver_match = re.match(r"^\s*v?(\d+)", ver_str)
            if not ver_match:
                continue
            if int(ver_match.group(1)) >= 2:
                has_v2_plus = True
            else:
                shadow_infos.append(entry)
        if not shadow_infos:
            continue
        for info_entry in shadow_infos:
            try:
                if info_entry.is_symlink():
                    continue
                if not info_entry.resolve().is_relative_to(site_resolved):
                    continue
                if info_entry.is_dir():
                    shutil.rmtree(info_entry, ignore_errors=True)
                elif info_entry.is_file():
                    info_entry.unlink(missing_ok=True)
            except OSError:
                continue
        if has_v2_plus:
            continue
        for entry in entries:
            if entry.is_symlink():
                continue
            lower_name = entry.name.lower()
            is_pkg_target = (
                lower_name == "sageattention"
                or lower_name == "sageattention.py"
                or (
                    lower_name.startswith("sageattention.")
                    and lower_name.endswith((".so", ".pyd"))
                )
            )
            if not is_pkg_target:
                continue
            try:
                if not entry.resolve().is_relative_to(site_resolved):
                    continue
                if entry.is_dir():
                    shutil.rmtree(entry, ignore_errors=True)
                elif entry.is_file():
                    entry.unlink(missing_ok=True)
            except OSError:
                continue


def _normalize_progress_range(progress_range: tuple[float, float]) -> tuple[float, float]:
    lo = float(progress_range[0])
    hi = float(progress_range[1])
    if not math.isfinite(lo):
        lo = 0.0
    if not math.isfinite(hi):
        hi = lo
    if hi < lo:
        lo, hi = hi, lo
    return max(0.0, min(1.0, lo)), max(0.0, min(1.0, hi))


def _interp_progress(progress_range: tuple[float, float], fraction: float) -> float:
    lo, hi = _normalize_progress_range(progress_range)
    try:
        val = float(fraction)
    except (TypeError, ValueError):
        val = 0.0
    if not math.isfinite(val):
        val = 0.0
    clamped = max(0.0, min(1.0, val))
    return round(lo + (hi - lo) * clamped, 4)


def _extract_progress_fraction(payload: dict[str, Any]) -> float | None:
    for key in ("progress", "fraction"):
        raw = payload.get(key)
        if raw is not None and not isinstance(raw, bool):
            try:
                val = float(raw)
            except (TypeError, ValueError):
                continue
            if not math.isfinite(val):
                continue
            return max(0.0, min(1.0, val))

    raw_pct = payload.get("percent")
    if raw_pct is not None and not isinstance(raw_pct, bool):
        try:
            pct = float(raw_pct)
            if math.isfinite(pct):
                return max(0.0, min(1.0, pct / 100.0))
        except (TypeError, ValueError):
            pass

    for cur_key, total_key in (("step", "total_steps"), ("current", "total")):
        cur = payload.get(cur_key)
        tot = payload.get(total_key)
        if (
            cur is not None
            and tot is not None
            and not isinstance(cur, bool)
            and not isinstance(tot, bool)
        ):
            try:
                cur_f = float(cur)
                tot_f = float(tot)
                if math.isfinite(cur_f) and math.isfinite(tot_f) and tot_f > 0:
                    return max(0.0, min(1.0, cur_f / tot_f))
            except (TypeError, ValueError):
                pass
    return None


def _update_task_state(
    task: Any,
    *,
    progress: float | None = None,
    message: str | None = None,
    monotonic: bool = True,
) -> None:
    if task is None:
        return
    update_fn = getattr(task, "update", None)
    if callable(update_fn):
        kwargs: dict[str, Any] = {}
        if progress is not None:
            kwargs["progress"] = progress
        if message:
            kwargs["message"] = message
        if not kwargs:
            return
        try:
            update_fn(**kwargs, monotonic_progress=monotonic)
            return
        except TypeError:
            try:
                update_fn(**kwargs)
                return
            except TypeError:
                pass

    lock = getattr(task, "lock", None)
    with lock if lock is not None else nullcontext():
        if progress is not None and hasattr(task, "progress"):
            cur = getattr(task, "progress", 0)
            if not monotonic or not isinstance(cur, (int, float)) or progress > cur:
                task.progress = progress
        if message and hasattr(task, "message"):
            task.message = message


def _cancelled_error_cls() -> type[BaseException]:
    try:
        from server.runtime import ColabOneCancelledError

        return ColabOneCancelledError
    except ImportError:
        return RuntimeError


def _is_cancelled_error(exc: BaseException) -> bool:
    cls = _cancelled_error_cls()
    if cls is not RuntimeError and isinstance(exc, cls):
        return True
    return type(exc).__name__ == "ColabOneCancelledError"


def _check_cancel(task: Any, stopping: threading.Event | None = None) -> None:
    check_fn = getattr(task, "check", None)
    if callable(check_fn):
        check_fn()
    if isinstance(stopping, threading.Event) and stopping.is_set():
        raise _cancelled_error_cls()("实例正在停止，已取消远端数字人任务")


def _open_secure_log_file(
    log_path: Path,
    *,
    append: bool = False,
    max_bytes: int = _MAX_LOG_FILE_BYTES,
) -> Any:
    """Open `log_path` with 0o600 permissions (no symlinks), truncating if > max_bytes."""
    _ensure_not_symlink(log_path.parent, "运行日志目录")
    _ensure_not_symlink(log_path, "运行日志文件")
    log_path.parent.mkdir(parents=True, exist_ok=True)

    truncate = not append
    if append:
        try:
            st = log_path.lstat()
            if stat.S_ISLNK(st.st_mode):
                raise ValueError(f"运行日志文件不能是符号链接：{log_path}")
            if st.st_size > max_bytes:
                truncate = True
        except FileNotFoundError:
            truncate = True

    flags = os.O_WRONLY | os.O_CREAT | (os.O_TRUNC if truncate else os.O_APPEND)
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    fd = os.open(str(log_path), flags, 0o600)
    try:
        try:
            os.fchmod(fd, 0o600)
        except (AttributeError, OSError):
            try:
                os.chmod(log_path, 0o600)
            except OSError:
                pass
        return os.fdopen(fd, "wb")
    except BaseException:
        try:
            os.close(fd)
        except OSError:
            pass
        raise


def _iter_pipe_lines(
    stream: Any, *, max_line_bytes: int = _MAX_STREAM_LINE_BYTES
) -> Iterator[str]:
    """Yield decoded text lines split on CR or LF with a hard per-line byte cap."""
    buf = bytearray()
    pending_lf = False
    while True:
        chunk = stream.read(4096)
        if not chunk:
            break
        if pending_lf:
            pending_lf = False
            if chunk.startswith(b"\n"):
                chunk = chunk[1:]
                if not chunk:
                    continue
        buf.extend(chunk)
        while buf:
            nl_idx = buf.find(b"\n")
            cr_idx = buf.find(b"\r")
            if nl_idx >= 0 and cr_idx >= 0:
                split_idx = min(nl_idx, cr_idx)
            elif nl_idx >= 0:
                split_idx = nl_idx
            elif cr_idx >= 0:
                split_idx = cr_idx
            else:
                split_idx = -1

            if split_idx < 0:
                if len(buf) >= max_line_bytes:
                    raw = bytes(buf[:max_line_bytes])
                    del buf[:max_line_bytes]
                    yield raw.decode("utf-8", errors="replace")
                    continue
                break

            if split_idx > max_line_bytes:
                raw = bytes(buf[:max_line_bytes])
                del buf[:max_line_bytes]
                yield raw.decode("utf-8", errors="replace")
                continue

            raw = bytes(buf[:split_idx])
            delim = buf[split_idx]
            consume_end = split_idx + 1
            if delim == 13:  # b"\r"
                if split_idx + 1 < len(buf):
                    if buf[split_idx + 1] == 10:  # b"\r\n"
                        consume_end = split_idx + 2
                else:
                    pending_lf = True
            del buf[:consume_end]
            yield raw.decode("utf-8", errors="replace")
    if buf:
        yield bytes(buf).decode("utf-8", errors="replace")


def _path_has_hidden_segment(rel_path: Path) -> bool:
    return any(part.startswith(".") for part in rel_path.parts)


def _is_incomplete_artifact_name(name: str) -> bool:
    return name.lower().endswith(_INCOMPLETE_ARTIFACT_SUFFIXES)


def _is_nonempty_regular_file(path: Path) -> bool:
    try:
        if path.is_symlink() or not path.is_file():
            return False
        name = path.name
        if name.startswith(".") or _is_incomplete_artifact_name(name):
            return False
        return path.stat().st_size > 0
    except OSError:
        return False


def _dir_has_incomplete_artifacts(directory: Path, *, recursive: bool = True) -> bool:
    """Return True if any *.incomplete, *.tmp, or *.part file exists in `directory`."""
    try:
        if directory.is_symlink():
            return True
        if not directory.is_dir():
            return False
        if not recursive:
            for entry in directory.iterdir():
                if entry.is_symlink():
                    continue
                if entry.is_file() and _is_incomplete_artifact_name(entry.name):
                    return True
            return False
        for dirpath, dirnames, filenames in os.walk(directory, followlinks=False):
            dp = Path(dirpath)
            dirnames[:] = [d for d in dirnames if not (dp / d).is_symlink()]
            for fname in filenames:
                if _is_incomplete_artifact_name(fname):
                    return True
    except OSError:
        return True
    return False


def _verify_shard_index_file(idx_dir: Path, idx_path: Path, root_resolved: Path) -> bool:
    """Parse a *.safetensors.index.json / *.bin.index.json and verify every referenced shard."""
    try:
        if idx_path.is_symlink() or not idx_path.is_file():
            return False
        if idx_path.stat().st_size <= 0:
            return False
        if not idx_path.resolve().is_relative_to(root_resolved):
            return False
        idx_data = json.loads(idx_path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return False
    if not isinstance(idx_data, dict):
        return False
    weight_map = idx_data.get("weight_map")
    if not isinstance(weight_map, dict) or not weight_map:
        return False
    for shard_name in set(weight_map.values()):
        if (
            not isinstance(shard_name, str)
            or not shard_name.strip()
            or "\\" in shard_name
            or "\x00" in shard_name
        ):
            return False
        shard_posix = PurePosixPath(shard_name)
        if shard_posix.is_absolute() or ".." in shard_posix.parts or not shard_posix.parts:
            return False
        if any(part.startswith(".") for part in shard_posix.parts):
            return False
        cur = idx_dir
        for part in shard_posix.parts:
            cur = cur / part
            try:
                if cur.is_symlink():
                    return False
            except OSError:
                return False
        if not _is_nonempty_regular_file(cur):
            return False
        try:
            if not cur.resolve().is_relative_to(root_resolved):
                return False
        except OSError:
            return False
    return True


def _subdir_has_valid_payload(
    subdir: Path,
    *,
    require_weight_ext: bool,
    root_resolved: Path | None = None,
) -> bool:
    """Walk the full subdir without early-return; reject temp artifacts and verify shard indices."""
    if subdir.is_symlink() or not subdir.is_dir():
        return False
    try:
        bound_root = root_resolved if root_resolved is not None else subdir.resolve()
        if not subdir.resolve().is_relative_to(bound_root):
            return False
    except OSError:
        return False

    has_payload = False
    index_files: list[tuple[Path, Path]] = []
    try:
        for dirpath, dirnames, filenames in os.walk(subdir, followlinks=False):
            dp = Path(dirpath)
            # Ignore HF metadata/cache folders like .cache or .huggingface while walking payload.
            dirnames[:] = [
                d
                for d in dirnames
                if not d.startswith(".") and not (dp / d).is_symlink()
            ]
            for fname in filenames:
                # Fail closed on any in-progress / partial download artifacts anywhere in subdir.
                if _is_incomplete_artifact_name(fname):
                    return False
                if fname.startswith("."):
                    continue
                fpath = dp / fname
                lower_name = fname.lower()
                if lower_name.endswith(_SHARD_INDEX_SUFFIXES):
                    index_files.append((dp, fpath))
                    continue
                if lower_name == "config.json":
                    if not _is_nonempty_regular_file(fpath):
                        return False
                    if not fpath.resolve().is_relative_to(bound_root):
                        return False
                if require_weight_ext and fpath.suffix.lower() in _AVATAR_WEIGHT_EXTS:
                    if not _is_nonempty_regular_file(fpath):
                        return False
                    if not fpath.resolve().is_relative_to(bound_root):
                        return False
                    has_payload = True
                    continue
                if not _is_nonempty_regular_file(fpath):
                    continue
                if not fpath.resolve().is_relative_to(bound_root):
                    continue
                if not require_weight_ext:
                    has_payload = True
    except OSError:
        return False

    if index_files:
        for idx_dir, idx_path in index_files:
            if not _verify_shard_index_file(idx_dir, idx_path, bound_root):
                return False
        has_payload = True

    return has_payload


def _collect_weight_file_sizes(weights_dir: Path) -> dict[str, int]:
    """Collect relative POSIX path -> exact byte size for all regular payload files in `weights_dir`."""
    if weights_dir.is_symlink() or not weights_dir.is_dir():
        return {}
    try:
        root_resolved = weights_dir.resolve()
    except OSError:
        return {}
    sizes: dict[str, int] = {}
    try:
        for dirpath, dirnames, filenames in os.walk(weights_dir, followlinks=False):
            dp = Path(dirpath)
            dirnames[:] = [
                d
                for d in dirnames
                if not d.startswith(".") and not (dp / d).is_symlink()
            ]
            for fname in filenames:
                if fname.startswith(".") or _is_incomplete_artifact_name(fname):
                    continue
                fpath = dp / fname
                if not _is_nonempty_regular_file(fpath):
                    continue
                try:
                    if not fpath.resolve().is_relative_to(root_resolved):
                        continue
                    rel = fpath.relative_to(weights_dir).as_posix()
                    sizes[rel] = int(fpath.stat().st_size)
                except (OSError, ValueError):
                    continue
    except OSError:
        return {}
    return dict(sorted(sizes.items()))


def _verify_recorded_file_sizes(weights_dir: Path, file_sizes: Any) -> bool:
    """Verify every recorded relative file exists as a non-symlink regular file with exact size."""
    if not isinstance(file_sizes, dict) or not file_sizes:
        return False
    if weights_dir.is_symlink() or not weights_dir.is_dir():
        return False
    try:
        root_resolved = weights_dir.resolve()
    except OSError:
        return False
    for rel_raw, expected_size in file_sizes.items():
        if (
            not isinstance(rel_raw, str)
            or not rel_raw.strip()
            or "\\" in rel_raw
            or "\x00" in rel_raw
        ):
            return False
        if (
            isinstance(expected_size, bool)
            or not isinstance(expected_size, int)
            or expected_size <= 0
        ):
            return False
        posix = PurePosixPath(rel_raw)
        if posix.is_absolute() or ".." in posix.parts or not posix.parts:
            return False
        if any(part.startswith(".") for part in posix.parts):
            return False
        cur = weights_dir
        for part in posix.parts:
            cur = cur / part
            try:
                if cur.is_symlink():
                    return False
            except OSError:
                return False
        if not _is_nonempty_regular_file(cur):
            return False
        try:
            if not cur.resolve().is_relative_to(root_resolved):
                return False
            if int(cur.stat().st_size) != expected_size:
                return False
        except OSError:
            return False
    return True


def _weights_files_satisfy_patterns(
    weights_dir: Path, required_patterns: Sequence[str]
) -> bool:
    """Validate concrete non-empty files matching `required_patterns` exist inside `weights_dir`."""
    if weights_dir.is_symlink() or not weights_dir.is_dir():
        return False
    try:
        root_resolved = weights_dir.resolve()
    except OSError:
        return False

    # Fail closed if HF Hub download staging cache or top-level dir contains incomplete artifacts.
    hf_dl_cache = weights_dir / ".cache" / "huggingface" / "download"
    try:
        if (
            (weights_dir / ".cache").is_symlink()
            or (weights_dir / ".cache" / "huggingface").is_symlink()
            or hf_dl_cache.is_symlink()
        ):
            return False
        if hf_dl_cache.is_dir() and _dir_has_incomplete_artifacts(hf_dl_cache, recursive=True):
            return False
    except OSError:
        return False
    if _dir_has_incomplete_artifacts(weights_dir, recursive=False):
        return False

    cleaned_patterns = [
        str(p).strip().replace("\\", "/") for p in required_patterns if str(p).strip()
    ]
    if not cleaned_patterns:
        return False

    checked_concrete = False
    for pat in cleaned_patterns:
        posix = PurePosixPath(pat)
        if posix.is_absolute() or ".." in posix.parts:
            return False
        if pat in _OPTIONAL_METADATA_PATTERNS or pat.endswith("/*.txt"):
            continue
        checked_concrete = True

        if pat.endswith("/**"):
            subdir_rel = pat[:-3].rstrip("/")
            if not subdir_rel:
                return False
            subdir_posix = PurePosixPath(subdir_rel)
            cur = weights_dir
            for part in subdir_posix.parts:
                cur = cur / part
                try:
                    if cur.is_symlink():
                        return False
                except OSError:
                    return False
            subdir = cur
            try:
                if subdir.is_symlink() or not subdir.is_dir():
                    return False
                if not subdir.resolve().is_relative_to(root_resolved):
                    return False
            except OSError:
                return False
            leaf_name = subdir_posix.name
            needs_weight = leaf_name in _WEIGHT_BEARING_SUBDIRS
            if not _subdir_has_valid_payload(
                subdir,
                require_weight_ext=needs_weight,
                root_resolved=root_resolved,
            ):
                return False
            continue

        if any(ch in pat for ch in ("*", "?", "[")):
            parent_rel = posix.parent
            parent_dir = weights_dir
            if str(parent_rel) not in ("", "."):
                for part in parent_rel.parts:
                    parent_dir = parent_dir / part
                    try:
                        if parent_dir.is_symlink():
                            return False
                    except OSError:
                        return False
                try:
                    if not parent_dir.is_dir() or not parent_dir.resolve().is_relative_to(root_resolved):
                        return False
                except OSError:
                    return False
                if _dir_has_incomplete_artifacts(parent_dir, recursive=True):
                    return False

            if pat == "whisper-large-v3/*.json":
                # Require at least one canonical Whisper JSON config (config.json or
                # preprocessor_config.json) and validate every present canonical JSON file.
                canonical_found = False
                for req_name in ("config.json", "preprocessor_config.json"):
                    cand = parent_dir / req_name
                    try:
                        if cand.is_symlink():
                            return False
                        if cand.exists():
                            if not _is_nonempty_regular_file(cand):
                                return False
                            if not cand.resolve().is_relative_to(root_resolved):
                                return False
                            canonical_found = True
                    except OSError:
                        return False
                if not canonical_found:
                    return False
                continue

            matched = False
            try:
                for cand in weights_dir.glob(pat):
                    if _is_incomplete_artifact_name(cand.name):
                        return False
                    if not _is_nonempty_regular_file(cand):
                        continue
                    try:
                        rel = cand.relative_to(weights_dir)
                        if _path_has_hidden_segment(rel):
                            continue
                        if not cand.resolve().is_relative_to(root_resolved):
                            continue
                    except (OSError, ValueError):
                        continue
                    matched = True
            except OSError:
                return False
            if not matched:
                return False
            continue

        # Exact concrete relative file path.
        cur = weights_dir
        for part in posix.parts:
            cur = cur / part
            try:
                if cur.is_symlink():
                    return False
            except OSError:
                return False
        if cur.parent != weights_dir and _dir_has_incomplete_artifacts(cur.parent, recursive=False):
            return False
        if not _is_nonempty_regular_file(cur):
            return False
        try:
            if not cur.resolve().is_relative_to(root_resolved):
                return False
        except OSError:
            return False

    if not checked_concrete:
        return _subdir_has_valid_payload(
            weights_dir,
            require_weight_ext=False,
            root_resolved=root_resolved,
        )
    return True


def _existing_ancestor(path: Path) -> Path:
    for candidate in (path, *path.parents):
        try:
            if candidate.exists():
                return candidate
        except OSError:
            continue
    return Path("/")


# ---------------------------------------------------------------------------
# 4. ColabOneAvatarEngineManager
# ---------------------------------------------------------------------------


class ColabOneAvatarEngineManager:
    """Manages native LongCat-Video-Avatar 1.6 and diffusion VSR remote engines."""

    def __init__(
        self,
        base_dir: Path | str,
        *,
        runtime: Any = None,
        drive: Any = None,
        run_cmd_fn: Callable[..., Any] | None = None,
        download_fn: Callable[..., Any] | None = None,
        gpu_info_fn: Callable[[], dict[str, Any]] | None = None,
        accel_wheels_fn: Callable[..., Any] | None = None,
    ) -> None:
        self.base_dir = Path(base_dir)
        self.root_dir = self.base_dir / "avatar_native"
        self.longcat16_venv_dir = self.root_dir / "longcat16_venv"
        self.seedvr2_venv_dir = self.root_dir / "seedvr2_venv"
        self.flashvsr_venv_dir = self.root_dir / "flashvsr_venv"
        self.repos_dir = self.root_dir / "repos"
        self.weights_dir = self.root_dir / "weights"
        self.inductor_cache_dir = self.root_dir / "inductor_cache"
        self.logs_dir = self.root_dir / "logs"

        self.runtime = runtime
        self.drive = drive if drive is not None else getattr(runtime, "drive", None)
        self._run_cmd_fn = run_cmd_fn
        self._download_fn = download_fn
        self._gpu_info_fn = gpu_info_fn
        self._accel_wheels_fn = accel_wheels_fn
        self._last_gpu_caps: dict[str, Any] = {}

        self._locks_guard = threading.Lock()
        self._env_locks: dict[str, threading.Lock] = {}

    def _get_env_lock(self, key: str) -> threading.Lock:
        with self._locks_guard:
            lock = self._env_locks.get(key)
            if lock is None:
                lock = threading.Lock()
                self._env_locks[key] = lock
            return lock

    def _stopping_event(self) -> threading.Event | None:
        stopping = getattr(self.runtime, "stopping", None)
        return stopping if isinstance(stopping, threading.Event) else None

    def _require_venv_python(self, venv_dir: Path, *, label: str) -> Path:
        found = _find_venv_python_on_disk(venv_dir)
        if found is not None:
            return found
        if self._run_cmd_fn is not None:
            return _resolve_venv_python(venv_dir)
        raise RuntimeError(
            f"{label} 隔离虚拟环境缺少 Python 可执行文件，拒绝回退到宿主环境：{venv_dir}"
        )

    # -- Drive persistent cache resolution ----------------------------------

    def _resolve_drive_cache_root(self) -> Path | None:
        drive = self.drive
        if drive is None:
            return None

        # Explicit persistent cache path attributes/callables (also convenient for tests).
        for attr in (
            "persistent_cache_root",
            "persistent_cache_dir",
            "avatar_cache_dir",
            "cache_root",
            "cache_dir",
        ):
            val = getattr(drive, attr, None)
            if callable(val):
                try:
                    val = val()
                except Exception:
                    val = None
            if isinstance(val, (str, os.PathLike)) and str(val).strip():
                cand = Path(val)
                try:
                    if cand.is_symlink():
                        continue
                    if cand.is_dir() or cand.parent.is_dir():
                        target = (
                            cand if cand.name == "avatar_native" else (cand / "avatar_native")
                        )
                        if not target.is_symlink() and (
                            not target.exists() or target.is_dir()
                        ):
                            return target
                except OSError:
                    continue

        # ColabOneDrive native Colab DriveFS mount (matches server/comfy_packs.py).
        mode = str(getattr(drive, "mode", "") or "")
        if mode == "colab_drivefs":
            is_drivefs_mounted = getattr(drive, "is_colab_drivefs_mounted", None)
            if callable(is_drivefs_mounted):
                try:
                    if not is_drivefs_mounted():
                        return None
                except Exception:
                    return None
            ws_fn = getattr(drive, "_drivefs_workspace_dir", None)
            if callable(ws_fn):
                try:
                    ws_dir = Path(ws_fn())
                    folder_dir = ws_dir.parent
                    mydrive_dir = folder_dir.parent
                    if (
                        not folder_dir.is_symlink()
                        and not ws_dir.is_symlink()
                        and (
                            ws_dir.is_dir()
                            or (
                                not ws_dir.exists()
                                and (
                                    folder_dir.is_dir()
                                    or (not folder_dir.exists() and mydrive_dir.is_dir())
                                )
                            )
                        )
                    ):
                        if not ws_dir.exists():
                            ws_dir.mkdir(parents=True, exist_ok=True)
                        if ws_dir.is_dir() and not ws_dir.is_symlink():
                            target = ws_dir / "avatar_native"
                            if not target.is_symlink() and (
                                not target.exists() or target.is_dir()
                            ):
                                return target
                except OSError:
                    return None

        # ColabOneDrive FUSE mount fallback.
        if mode == "mount":
            is_mounted_fn = getattr(drive, "is_fuse_mounted", None)
            mount_root = getattr(drive, "mount_root", None)
            if callable(is_mounted_fn) and isinstance(mount_root, (str, os.PathLike)):
                try:
                    mount_p = Path(mount_root)
                    if is_mounted_fn() and not mount_p.is_symlink() and mount_p.is_dir():
                        ws_dir = mount_p / "workspace"
                        if not ws_dir.is_symlink():
                            if not ws_dir.exists():
                                ws_dir.mkdir(parents=True, exist_ok=True)
                            if ws_dir.is_dir():
                                target = ws_dir / "avatar_native"
                                if not target.is_symlink() and (
                                    not target.exists() or target.is_dir()
                                ):
                                    return target
                except OSError:
                    return None

        return None

    def _weights_marker_valid(
        self,
        weights_dir: Path,
        *,
        repo_id: str,
        revision: str,
        required_patterns: Sequence[str] | None = None,
    ) -> bool:
        if weights_dir.is_symlink() or not weights_dir.is_dir():
            return False
        marker_path = weights_dir / ".colabone-weights.json"
        if marker_path.is_symlink() or not marker_path.is_file():
            return False
        try:
            data = json.loads(marker_path.read_text(encoding="utf-8"))
        except (OSError, ValueError):
            return False
        if not isinstance(data, dict):
            return False
        if data.get("repo_id") != repo_id or data.get("revision") != revision:
            return False
        cached_patterns = data.get("downloaded_patterns")
        if not isinstance(cached_patterns, list):
            return False
        cached_list = [str(p) for p in cached_patterns if str(p).strip()]
        if required_patterns is not None:
            req_list = [str(p) for p in required_patterns if str(p).strip()]
            if not set(req_list).issubset(set(cached_list)):
                return False
            patterns_to_verify = req_list
        else:
            patterns_to_verify = cached_list
        if self._download_fn is None:
            if not _weights_files_satisfy_patterns(weights_dir, patterns_to_verify):
                return False
            if "file_sizes" in data and not _verify_recorded_file_sizes(
                weights_dir, data.get("file_sizes")
            ):
                return False
        return True

    def _estimate_missing_component_mib(
        self,
        weights_dir: Path | None,
        *,
        subdir: str,
        repo_id: str,
        revision: str,
        required_patterns: Sequence[str] | None,
    ) -> int:
        """Compute exact remaining download size (MiB) for missing component groups in `weights_dir`."""
        req_list = [
            str(p).strip().replace("\\", "/")
            for p in (required_patterns or ())
            if str(p).strip()
        ]
        if not req_list:
            if subdir == "longcat16_avatar":
                return _LONGCAT16_AVATAR_BASE_MIB
            if subdir == "longcat16_base":
                return _LONGCAT16_BASE_ESTIMATED_MIB
            if subdir == "seedvr2":
                return _SEEDVR2_ESTIMATED_MIB
            if subdir == "flashvsr_plus":
                return _FLASHVSR_ESTIMATED_MIB
            return 0

        def _has_group(group_patterns: Sequence[str]) -> bool:
            if weights_dir is None or not group_patterns:
                return False
            return self._weights_marker_valid(
                weights_dir,
                repo_id=repo_id,
                revision=revision,
                required_patterns=group_patterns,
            )

        if _has_group(req_list):
            return 0

        if subdir == "longcat16_avatar":
            delta_mib = 0
            core_patterns = [
                p
                for p in req_list
                if p not in ("loraA/**", "loraB/**", "vocal_separator/**")
            ]
            if core_patterns and not _has_group(core_patterns):
                delta_mib += _LONGCAT16_AVATAR_CORE_MIB
            lora_a_mib = _LONGCAT16_LORA_MIB // 2
            lora_b_mib = _LONGCAT16_LORA_MIB - lora_a_mib
            if "loraA/**" in req_list and not _has_group(["loraA/**"]):
                delta_mib += lora_a_mib
            if "loraB/**" in req_list and not _has_group(["loraB/**"]):
                delta_mib += lora_b_mib
            if "vocal_separator/**" in req_list and not _has_group(["vocal_separator/**"]):
                delta_mib += _LONGCAT16_VOCAL_SEP_MIB
            return delta_mib

        if subdir == "longcat16_base":
            return _LONGCAT16_BASE_ESTIMATED_MIB

        if subdir == "seedvr2":
            delta_mib = 0
            for pat in req_list:
                if pat in _OPTIONAL_METADATA_PATTERNS:
                    continue
                if _has_group([pat]):
                    continue
                if pat == "ema_vae_fp16.safetensors":
                    delta_mib += _SEEDVR2_VAE_MIB
                else:
                    delta_mib += _SEEDVR2_DIT_MIB
            return delta_mib

        if subdir == "flashvsr_plus":
            return _FLASHVSR_ESTIMATED_MIB

        return 0

    def _resolve_weights_dir(
        self,
        subdir: str,
        *,
        repo_id: str,
        revision: str,
        required_patterns: Sequence[str] | None = None,
        min_free_bytes: int = 0,
    ) -> Path:
        _ensure_not_symlink(self.root_dir, "数字人引擎根目录")
        _ensure_not_symlink(self.weights_dir, "数字人权重目录")
        local_dir = self.weights_dir / subdir
        _ensure_not_symlink(local_dir, f"本地权重目录（{subdir}）")

        drive_root = self._resolve_drive_cache_root()
        if drive_root is not None:
            drive_weights_root = drive_root / "weights"
            drive_dir = drive_weights_root / subdir
            try:
                _ensure_not_symlink(drive_root, "Drive 缓存目录")
                _ensure_not_symlink(drive_weights_root, "Drive 权重目录")
                _ensure_not_symlink(drive_dir, f"Drive 权重目录（{subdir}）")
                # 1. Prefer Drive if all required patterns are already cached there.
                if self._weights_marker_valid(
                    drive_dir,
                    repo_id=repo_id,
                    revision=revision,
                    required_patterns=required_patterns,
                ):
                    return drive_dir
                # 2. If all required patterns are already cached locally, stay on fast local NVMe.
                if self._weights_marker_valid(
                    local_dir,
                    repo_id=repo_id,
                    revision=revision,
                    required_patterns=required_patterns,
                ):
                    return local_dir

                def _drive_has_space(needed_bytes: int) -> bool:
                    if self._download_fn is not None or needed_bytes <= 0:
                        return True
                    free_bytes = int(
                        shutil.disk_usage(_existing_ancestor(drive_root)).free
                    )
                    return free_bytes >= needed_bytes

                # 3. Prefer extending an existing valid partial cache on Drive in place using
                #    only the incremental delta byte budget (matching check_preflight 1:1).
                drive_has_partial = self._weights_marker_valid(
                    drive_dir,
                    repo_id=repo_id,
                    revision=revision,
                    required_patterns=None,
                )
                if drive_has_partial:
                    drive_delta_mib = self._estimate_missing_component_mib(
                        drive_dir,
                        subdir=subdir,
                        repo_id=repo_id,
                        revision=revision,
                        required_patterns=required_patterns,
                    )
                    drive_delta_bytes = (
                        drive_delta_mib * 1024 * 1024
                        if drive_delta_mib > 0
                        else min_free_bytes
                    )
                    if _drive_has_space(drive_delta_bytes):
                        drive_dir.mkdir(parents=True, exist_ok=True)
                        return drive_dir

                # 4. Otherwise prefer extending an existing valid partial cache on local NVMe in place.
                if self._weights_marker_valid(
                    local_dir,
                    repo_id=repo_id,
                    revision=revision,
                    required_patterns=None,
                ):
                    return local_dir

                # 5. Cold download: use Drive only if writable and has enough free space for full cold size.
                cold_mib = self._estimate_missing_component_mib(
                    None,
                    subdir=subdir,
                    repo_id=repo_id,
                    revision=revision,
                    required_patterns=required_patterns,
                )
                cold_bytes = (
                    cold_mib * 1024 * 1024
                    if (required_patterns is not None and cold_mib > 0)
                    else min_free_bytes
                )
                if not drive_has_partial and _drive_has_space(cold_bytes):
                    drive_dir.mkdir(parents=True, exist_ok=True)
                    return drive_dir
            except (OSError, ValueError):
                pass
        return local_dir

    def _resolve_compile_cache_dir(self) -> Path:
        """Always keep TorchInductor / Triton compile caches on fast local NVMe (never DriveFS)."""
        _ensure_not_symlink(self.root_dir, "数字人引擎根目录")
        _ensure_not_symlink(self.inductor_cache_dir, "编译缓存目录")
        self.inductor_cache_dir.mkdir(parents=True, exist_ok=True)
        return self.inductor_cache_dir

    def _sanitize_compile_cache_dir(self, candidate: Path | str | None) -> Path:
        """Redirect None or any DriveFS/FUSE path to fast local NVMe compile cache dir."""
        if candidate is None or not str(candidate).strip():
            return self._resolve_compile_cache_dir()

        compile_p = Path(candidate)
        local_default = self.inductor_cache_dir
        try:
            local_default_resolved = local_default.resolve()
        except OSError:
            local_default_resolved = local_default

        drive_roots: list[Path] = []
        drive_root = self._resolve_drive_cache_root()
        if drive_root is not None:
            drive_roots.append(drive_root)
        drive = self.drive
        if drive is not None:
            mount_root = getattr(drive, "mount_root", None)
            if isinstance(mount_root, (str, os.PathLike)) and str(mount_root).strip():
                mr = Path(mount_root)
                drive_roots.append(mr)
                mr_parent = mr.parent
                drive_mode = str(getattr(drive, "mode", "") or "")
                try:
                    parent_is_drivefs_mount = (
                        drive_mode == "colab_drivefs"
                        or (mr_parent != Path("/") and os.path.ismount(mr_parent))
                    )
                    if (
                        parent_is_drivefs_mount
                        and mr_parent != Path("/")
                        and not local_default_resolved.is_relative_to(mr_parent.resolve())
                    ):
                        drive_roots.append(mr_parent)
                except OSError:
                    pass
            ws_fn = getattr(drive, "_drivefs_workspace_dir", None)
            if callable(ws_fn):
                try:
                    ws_val = ws_fn()
                    if isinstance(ws_val, (str, os.PathLike)) and str(ws_val).strip():
                        ws_p = Path(ws_val)
                        for anc in (ws_p, ws_p.parent, ws_p.parent.parent):
                            if anc != Path("/") and not local_default_resolved.is_relative_to(
                                anc.resolve()
                            ):
                                drive_roots.append(anc)
                except Exception:
                    pass

        try:
            compile_resolved = compile_p.resolve()
        except OSError:
            return self._resolve_compile_cache_dir()

        for dr in drive_roots:
            try:
                if compile_resolved.is_relative_to(dr.resolve()):
                    return self._resolve_compile_cache_dir()
            except OSError:
                return self._resolve_compile_cache_dir()

        # Also catch any distinct DriveFS / FUSE mount device via st_dev comparison.
        try:
            local_dev = os.stat(_existing_ancestor(self.root_dir)).st_dev
        except OSError:
            local_dev = None
        if local_dev is not None and drive_roots:
            drive_devs: set[int] = set()
            for dr in drive_roots:
                try:
                    dr_dev = os.stat(_existing_ancestor(dr)).st_dev
                    if dr_dev != local_dev:
                        drive_devs.add(dr_dev)
                except OSError:
                    continue
            if drive_devs:
                try:
                    cand_dev = os.stat(_existing_ancestor(compile_p)).st_dev
                    if cand_dev in drive_devs:
                        return self._resolve_compile_cache_dir()
                except OSError:
                    return self._resolve_compile_cache_dir()

        _ensure_not_symlink(compile_p, "编译缓存目录")
        compile_p.mkdir(parents=True, exist_ok=True)
        return compile_p

    # -- Hardware & disk preflight ------------------------------------------

    def probe_gpu_info(self) -> dict[str, Any]:
        """Query GPU capabilities via injected callback, runtime, or telemetry sampler."""
        raw: dict[str, Any] = {}
        if callable(self._gpu_info_fn):
            raw = self._gpu_info_fn() or {}
        elif self.runtime is not None and callable(getattr(self.runtime, "gpu_info", None)):
            raw = self.runtime.gpu_info() or {}
        else:
            from server.telemetry import ColabOneResourceSampler

            raw = ColabOneResourceSampler.gpu_info() or {}

        if not isinstance(raw, dict):
            raw = {}
        name = str(raw.get("name") or "GPU 信息不可用").strip() or "GPU 信息不可用"
        total_mib = _safe_nonneg_int(raw.get("total_mib"), 0)
        used_mib = _safe_nonneg_int(raw.get("used_mib"), 0)
        if "free_mib" in raw and raw.get("free_mib") is not None:
            free_mib = _safe_nonneg_int(raw.get("free_mib"), 0)
        else:
            free_mib = max(0, total_mib - used_mib)
        return {
            "name": name,
            "total_mib": total_mib,
            "used_mib": used_mib,
            "free_mib": free_mib,
        }

    def _resolve_wheel_cache_base_root(self) -> Path:
        """Resolve the shared runtime root holding `wheel-cache/` (shared with ComfyUI)."""
        runtime_root = getattr(self.runtime, "root", None)
        if isinstance(runtime_root, (str, os.PathLike)) and str(runtime_root).strip():
            rt_path = Path(runtime_root)
            if rt_path.is_symlink():
                raise ValueError(f"远端运行根目录不能是符号链接：{rt_path}")
            return rt_path
        parent = self.root_dir.parent
        if parent.is_symlink():
            raise ValueError(f"加速包缓存根目录不能是符号链接：{parent}")
        if self.base_dir.is_symlink():
            raise ValueError(f"加速包缓存根目录不能是符号链接：{self.base_dir}")
        return parent

    def ensure_gpu_acceleration_wheels(
        self,
        task: Any,
        *,
        allow_sage3: bool = True,
        gpu_info: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        """Probe GPU capabilities and ensure shared SageAttention 2/3 wheels are installed.

        Reuses the same local NVMe and Google Drive wheel cache as ComfyUI via
        ``server.comfy_packs.ensure_sageattention_wheels_shared`` so wheels built
        once are inherited across ComfyUI, LongCat 1.6, SeedVR2, and FlashVSR+.
        Best-effort and never fatal (except task cancellation).
        """
        _check_cancel(task, self._stopping_event())
        if self._accel_wheels_fn is None and self._run_cmd_fn is not None:
            return {}
        from server import comfy_packs

        raw_gpu = dict(gpu_info) if isinstance(gpu_info, dict) else self.probe_gpu_info()
        caps = comfy_packs.probe_gpu_acceleration_capabilities(raw_gpu)
        self._last_gpu_caps = dict(caps)

        cc_major = _safe_nonneg_int(caps.get("cc_major"), 0)
        cc_minor = _safe_nonneg_int(caps.get("cc_minor"), 0)
        need_sage2 = not bool(caps.get("sageattn_available"))
        need_sage3 = bool(
            allow_sage3
            and comfy_packs.is_blackwell_cc(cc_major, cc_minor)
            and not bool(caps.get("sageattn3_available"))
        )
        if cc_major < 8 or (not need_sage2 and not need_sage3):
            return caps

        wheel_base_root = self._resolve_wheel_cache_base_root()
        try:
            if self._accel_wheels_fn is not None:
                updated = _call_with_compatible_kwargs(
                    self._accel_wheels_fn,
                    (task,),
                    {
                        "gpu_caps": dict(caps),
                        "allow_sage3": allow_sage3,
                        "base_root": wheel_base_root,
                        "runtime": self.runtime,
                        "drive": self.drive,
                        "stopping": self._stopping_event(),
                    },
                )
            else:
                updated = comfy_packs.ensure_sageattention_wheels_shared(
                    task,
                    base_root=wheel_base_root,
                    runtime=self.runtime,
                    drive=self.drive,
                    gpu_caps=caps,
                    allow_sage3=allow_sage3,
                    stopping=self._stopping_event(),
                )
            if isinstance(updated, dict):
                caps = {**caps, **updated}
        except Exception as exc:
            if _is_cancelled_error(exc):
                raise
            _safe_task_note(
                task,
                f"SageAttention 加速包准备跳过，将自动回退：{str(exc)[:300]}",
                level="warning",
            )

        self._last_gpu_caps = dict(caps)
        return caps

    def _is_component_cached(
        self,
        subdir: str,
        *,
        repo_id: str,
        revision: str,
        required_patterns: Sequence[str],
    ) -> bool:
        """Return True iff local NVMe or DriveFS holds a verified pattern-complete snapshot."""
        local_dir = self.weights_dir / subdir
        if not local_dir.is_symlink() and self._weights_marker_valid(
            local_dir,
            repo_id=repo_id,
            revision=revision,
            required_patterns=required_patterns,
        ):
            return True

        drive_root = self._resolve_drive_cache_root()
        if drive_root is not None:
            drive_weights = drive_root / "weights"
            drive_dir = drive_weights / subdir
            if (
                not drive_root.is_symlink()
                and not drive_weights.is_symlink()
                and not drive_dir.is_symlink()
                and self._weights_marker_valid(
                    drive_dir,
                    repo_id=repo_id,
                    revision=revision,
                    required_patterns=required_patterns,
                )
            ):
                return True
        return False

    def check_preflight(
        self,
        *,
        min_vram_mib: int = 49152,
        min_disk_mib: int = 73728,
        upscale_mode: str = "seedvr2_7b_sharp",
        sampling_mode: str = "non_distill_cfg",
        vocal_separation: str = "auto",
    ) -> dict[str, Any]:
        """Verify remote VRAM and per-filesystem disk headroom using verified weight patterns."""
        _ensure_not_symlink(self.root_dir, "数字人引擎根目录")
        _ensure_not_symlink(self.weights_dir, "数字人权重目录")

        gpu = self.probe_gpu_info()
        gpu_name = str(gpu.get("name") or "GPU 信息不可用")
        total_mib = int(gpu.get("total_mib") or 0)
        used_mib = int(gpu.get("used_mib") or 0)
        free_mib = int(gpu.get("free_mib") or 0)

        min_vram = max(0, int(min_vram_mib or 0))
        if min_vram > 0:
            if total_mib <= 0:
                raise RuntimeError(
                    f"未检测到可用远端 GPU，LongCat-Video-Avatar 1.6 原生管线至少需要 "
                    f"{min_vram / 1024.0:.0f} GiB（{min_vram} MiB）显存"
                )
            if total_mib < min_vram:
                raise RuntimeError(
                    f"远端 GPU 显存不足：当前 {gpu_name} 仅 {total_mib / 1024.0:.1f} GiB"
                    f"（{total_mib} MiB），LongCat-Video-Avatar 1.6 原生管线至少需要 "
                    f"{min_vram / 1024.0:.1f} GiB（{min_vram} MiB）显存"
                )

        norm_sampling = str(sampling_mode or "non_distill_cfg").strip().lower()
        if norm_sampling == "auto":
            norm_sampling = "non_distill_cfg"
        norm_vocal = str(vocal_separation or "auto").strip().lower()

        raw_upscale = str(upscale_mode or "").strip().lower()
        if raw_upscale == "auto":
            effective_upscale = "seedvr2_7b_sharp"
        elif raw_upscale in ("", "none"):
            effective_upscale = "none"
        elif raw_upscale in COLABONE_SUPPORTED_VSR_ENGINES:
            effective_upscale = raw_upscale
        else:
            raise ValueError(
                f"不支持的远端扩散超分引擎：{upscale_mode!r}（仅支持 seedvr2_7b_sharp、seedvr2_7b、flashvsr_plus、none、auto）"
            )

        # Build component specs matching _resolve_weights_dir 1:1 across variants & partial caches.
        avatar_patterns = longcat16_avatar_allow_patterns(
            sampling_mode=norm_sampling, vocal_separation=norm_vocal
        )
        base_patterns = longcat16_base_allow_patterns()
        component_specs: list[tuple[str, str, str, list[str]]] = [
            (
                "longcat16_avatar",
                COLABONE_LONGCAT16_AVATAR_HF_REPO,
                COLABONE_LONGCAT16_AVATAR_HF_REV,
                avatar_patterns,
            ),
            (
                "longcat16_base",
                COLABONE_LONGCAT16_BASE_HF_REPO,
                COLABONE_LONGCAT16_BASE_HF_REV,
                base_patterns,
            ),
        ]
        if effective_upscale in COLABONE_SUPPORTED_VSR_ENGINES:
            vsr_repo, vsr_rev, vsr_patterns = vsr_engine_allow_patterns(effective_upscale)
            vsr_subdir = (
                "flashvsr_plus" if effective_upscale == "flashvsr_plus" else "seedvr2"
            )
            component_specs.append((vsr_subdir, vsr_repo, vsr_rev, vsr_patterns))

        usage_path = _existing_ancestor(self.root_dir)
        min_disk = max(0, int(min_disk_mib or 0))
        workspace_headroom_mib = min(min_disk, _WORKSPACE_HEADROOM_MIB) if min_disk > 0 else 0

        # Resolve DriveFS availability once (matching _resolve_weights_dir symlink & distinct-FS rules).
        drive_root = self._resolve_drive_cache_root()
        drive_weights_root: Path | None = None
        drive_usable = False
        drive_free_mib: int | None = None
        drive_usage = None
        drive_usage_path: Path | None = None
        if drive_root is not None:
            try:
                _ensure_not_symlink(drive_root, "Drive 缓存目录")
                cand_dw = drive_root / "weights"
                _ensure_not_symlink(cand_dw, "Drive 权重目录")
                drive_weights_root = cand_dw
                drive_usage_path = _existing_ancestor(drive_root)
                drive_usage = shutil.disk_usage(drive_usage_path)
                drive_free_mib = int(drive_usage.free) // (1024 * 1024)
                drive_usable = True
            except (OSError, ValueError):
                drive_weights_root = None
                drive_free_mib = None
                drive_usage = None
                drive_usage_path = None
                drive_usable = False

        # Determine per-component cache hit vs partial-cache delta before evaluating disk usage.
        all_weights_cached = True
        component_plans: list[dict[str, Any]] = []
        for comp_subdir, comp_repo, comp_rev, comp_patterns in component_specs:
            local_dir = self.weights_dir / comp_subdir
            local_safe = not local_dir.is_symlink()
            drive_dir = (
                drive_weights_root / comp_subdir
                if drive_usable and drive_weights_root is not None
                else None
            )
            drive_safe = drive_dir is not None and not drive_dir.is_symlink()

            drive_full = drive_safe and self._weights_marker_valid(
                drive_dir,  # type: ignore[arg-type]
                repo_id=comp_repo,
                revision=comp_rev,
                required_patterns=comp_patterns,
            )
            local_full = (not drive_full) and local_safe and self._weights_marker_valid(
                local_dir,
                repo_id=comp_repo,
                revision=comp_rev,
                required_patterns=comp_patterns,
            )
            if drive_full or local_full:
                continue

            all_weights_cached = False
            drive_partial = drive_safe and self._weights_marker_valid(
                drive_dir,  # type: ignore[arg-type]
                repo_id=comp_repo,
                revision=comp_rev,
                required_patterns=None,
            )
            local_partial = local_safe and self._weights_marker_valid(
                local_dir,
                repo_id=comp_repo,
                revision=comp_rev,
                required_patterns=None,
            )
            drive_delta_mib = (
                self._estimate_missing_component_mib(
                    drive_dir,
                    subdir=comp_subdir,
                    repo_id=comp_repo,
                    revision=comp_rev,
                    required_patterns=comp_patterns,
                )
                if drive_partial
                else 0
            )
            local_delta_mib = (
                self._estimate_missing_component_mib(
                    local_dir,
                    subdir=comp_subdir,
                    repo_id=comp_repo,
                    revision=comp_rev,
                    required_patterns=comp_patterns,
                )
                if local_partial
                else 0
            )
            cold_mib = self._estimate_missing_component_mib(
                None,
                subdir=comp_subdir,
                repo_id=comp_repo,
                revision=comp_rev,
                required_patterns=comp_patterns,
            )
            component_plans.append(
                {
                    "subdir": comp_subdir,
                    "drive_partial": drive_partial,
                    "drive_delta_mib": drive_delta_mib,
                    "local_partial": local_partial,
                    "local_delta_mib": local_delta_mib,
                    "cold_mib": cold_mib,
                }
            )

        try:
            local_usage = shutil.disk_usage(usage_path)
            free_bytes = int(local_usage.free)
        except OSError as exc:
            if min_disk > 0:
                fallback_weights = sum(
                    p["drive_delta_mib"]
                    if p["drive_partial"]
                    else (p["local_delta_mib"] if p["local_partial"] else p["cold_mib"])
                    for p in component_plans
                )
                fallback_req = workspace_headroom_mib + fallback_weights
                raise RuntimeError(
                    f"无法检测远端磁盘可用空间（{exc}），LongCat-Video-Avatar 1.6 至少需要 "
                    f"{fallback_req / 1024.0:.1f} GiB 可用磁盘空间"
                ) from None
            local_usage = None
            free_bytes = 0

        free_disk_mib = free_bytes // (1024 * 1024)

        # Route uncached weight deltas across DriveFS (if mounted & distinct) vs local NVMe,
        # matching _resolve_weights_dir 1:1 (partial caches extended in place with delta budgets).
        drive_weights_needed_mib = 0
        local_weights_needed_mib = 0

        if min_disk == 0:
            effective_required_mib = 0
        elif all_weights_cached:
            effective_required_mib = workspace_headroom_mib
        else:
            drive_available_mib = drive_free_mib or 0
            drive_distinct_fs = False
            if drive_usable and drive_usage is not None and drive_usage_path is not None:
                try:
                    same_dev = os.stat(usage_path).st_dev == os.stat(drive_usage_path).st_dev
                except OSError:
                    same_dev = False
                same_usage = (
                    local_usage is not None
                    and int(local_usage.total) == int(drive_usage.total)
                )
                drive_distinct_fs = not (same_dev and same_usage)

            for plan in component_plans:
                if plan["drive_partial"]:
                    d_mib = int(plan["drive_delta_mib"])
                    if drive_distinct_fs:
                        if drive_available_mib >= d_mib:
                            drive_available_mib -= d_mib
                            drive_weights_needed_mib += d_mib
                            continue
                    else:
                        local_weights_needed_mib += d_mib
                        continue

                if plan["local_partial"]:
                    local_weights_needed_mib += int(plan["local_delta_mib"])
                    continue

                c_mib = int(plan["cold_mib"])
                if not plan["drive_partial"] and drive_distinct_fs and drive_available_mib >= c_mib:
                    drive_available_mib -= c_mib
                    drive_weights_needed_mib += c_mib
                else:
                    local_weights_needed_mib += c_mib

            effective_required_mib = workspace_headroom_mib + local_weights_needed_mib

        if effective_required_mib > 0 and free_bytes < effective_required_mib * 1024 * 1024:
            req_gib = effective_required_mib / 1024.0
            free_gib = free_bytes / (1024.0**3)
            if all_weights_cached or local_weights_needed_mib == 0:
                raise RuntimeError(
                    f"远端工作区磁盘空间不足：权重已缓存或将写入 Drive，但生成与临时转码仍需至少 "
                    f"{req_gib:.1f} GiB（{effective_required_mib} MiB）可用空间，"
                    f"当前仅剩 {free_gib:.1f} GiB（{free_disk_mib} MiB）"
                )
            raise RuntimeError(
                f"远端磁盘空间不足：LongCat-Video-Avatar 1.6 原生管线与超分模型至少需要 "
                f"{req_gib:.1f} GiB（{effective_required_mib} MiB）可用空间，"
                f"当前仅剩 {free_gib:.1f} GiB（{free_disk_mib} MiB）"
            )

        return {
            "name": gpu_name,
            "total_mib": total_mib,
            "used_mib": used_mib,
            "free_mib": free_mib,
            "is_high_vram_96g": total_mib >= _HIGH_VRAM_96G_THRESHOLD_MIB,
            "free_disk_mib": free_disk_mib,
            "required_disk_mib": effective_required_mib,
            "drive_free_disk_mib": drive_free_mib,
            "drive_required_disk_mib": drive_weights_needed_mib,
            "all_weights_cached": all_weights_cached,
            "upscale_mode": effective_upscale,
            "sampling_mode": norm_sampling,
            "vocal_separation": norm_vocal,
        }

    # -- Subprocess helpers (setup + streaming inference) -------------------

    def _invoke_setup_command(
        self,
        args: Sequence[str],
        task: Any,
        log_path: Path,
        *,
        timeout: int = 1800,
        cwd: Path | str | None = None,
        env: dict[str, str] | None = None,
        secret_to_scrub: str = "",
        append_log: bool = True,
    ) -> None:
        stopping = self._stopping_event()
        if self._run_cmd_fn is not None:
            _call_with_compatible_kwargs(
                self._run_cmd_fn,
                (list(args),),
                {
                    "task": task,
                    "log": log_path,
                    "timeout": timeout,
                    "cwd": cwd,
                    "env": env,
                    "stopping": stopping,
                },
            )
            return
        self._run_plain_subprocess(
            args,
            task,
            log_path,
            timeout=timeout,
            cwd=cwd,
            env=env,
            stopping=stopping,
            secret_to_scrub=secret_to_scrub,
            append_log=append_log,
            stage_label="远端数字人引擎环境准备",
        )

    @staticmethod
    def _run_plain_subprocess(
        args: Sequence[str],
        task: Any,
        log_path: Path,
        *,
        timeout: int = 1800,
        cwd: Path | str | None = None,
        env: dict[str, str] | None = None,
        stopping: threading.Event | None = None,
        secret_to_scrub: str = "",
        append_log: bool = False,
        stage_label: str = "远端数字人引擎",
    ) -> None:
        _check_cancel(task, stopping)
        log_fh = _open_secure_log_file(log_path, append=append_log)
        try:
            proc = subprocess.Popen(
                list(args),
                cwd=str(cwd) if cwd is not None else None,
                env=env,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                stdin=subprocess.DEVNULL,
                start_new_session=True,
                bufsize=0,
            )
        except BaseException:
            try:
                log_fh.close()
            except OSError:
                pass
            raise

        has_cancel_hook = task is not None and hasattr(task, "cancel_hook")
        task_lock = getattr(task, "lock", None) if has_cancel_hook else None
        prev_cancel_hook = None
        term_lock = threading.Lock()
        pgrp_cleaned = False
        tail_lock = threading.Lock()
        tail_lines: collections.deque[str] = collections.deque(maxlen=80)

        def _cleanup_pgrp() -> None:
            nonlocal pgrp_cleaned
            with term_lock:
                if not pgrp_cleaned:
                    pgrp_cleaned = True
                    _terminate_process_group(proc, timeout=4.0)

        def _cancel_proc() -> None:
            _cleanup_pgrp()
            if callable(prev_cancel_hook):
                try:
                    prev_cancel_hook()
                except Exception:
                    pass

        if has_cancel_hook:
            with task_lock if task_lock is not None else nullcontext():
                prev_cancel_hook = getattr(task, "cancel_hook", None)
                task.cancel_hook = _cancel_proc

        def _reader_loop() -> None:
            try:
                assert proc.stdout is not None
                with log_fh:
                    for raw_line in _iter_pipe_lines(proc.stdout):
                        scrubbed = _scrub_secret(raw_line, secret_to_scrub)
                        log_fh.write((scrubbed + "\n").encode("utf-8", errors="replace"))
                        log_fh.flush()
                        stripped = scrubbed.strip()
                        if stripped:
                            with tail_lock:
                                tail_lines.append(stripped)
            except OSError:
                pass
            finally:
                try:
                    if proc.stdout is not None:
                        proc.stdout.close()
                except OSError:
                    pass

        reader = threading.Thread(
            target=_reader_loop,
            name=f"colabone-avatar-cmd-{proc.pid}",
            daemon=True,
        )
        reader.start()

        deadline = time.monotonic() + max(1, int(timeout))
        last_noted = 0.0
        last_noted_line = ""
        try:
            while proc.poll() is None:
                _check_cancel(task, stopping)
                if time.monotonic() > deadline:
                    raise RuntimeError(f"{stage_label} 执行超时，请查看运行日志")
                if time.monotonic() - last_noted > 5.0:
                    last_noted = time.monotonic()
                    with tail_lock:
                        latest = tail_lines[-1] if tail_lines else ""
                    if (
                        latest
                        and latest != last_noted_line
                        and latest != _HF_TRANSFER_ACTIVE_MARKER
                    ):
                        last_noted_line = latest
                        _safe_task_note(task, latest[:250])
                time.sleep(0.2)

            reader.join(timeout=2.0)
            _check_cancel(task, stopping)
            if proc.returncode:
                with tail_lock:
                    captured = list(tail_lines)
                fallback = (
                    ""
                    if captured
                    else _scrub_secret(_read_log_tail(log_path, 4096), secret_to_scrub)
                )
                tail_text = _summarize_error_tail(captured, fallback, max_chars=800)
                raise RuntimeError(
                    f"{stage_label} 执行失败（退出码 {proc.returncode}）：{tail_text}"
                )
        finally:
            try:
                _cleanup_pgrp()
                reader.join(timeout=2.0)
            finally:
                if has_cancel_hook:
                    with task_lock if task_lock is not None else nullcontext():
                        if getattr(task, "cancel_hook", None) is _cancel_proc:
                            task.cancel_hook = prev_cancel_hook

    # -- Repo checkout, isolated venv, and HF weight downloads --------------

    @staticmethod
    def _verify_git_checkout_head(
        repo_dir: Path,
        expected_commit: str,
        *,
        expected_entries: Sequence[str] = (),
        env: dict[str, str] | None = None,
    ) -> bool:
        """Verify an existing Git checkout has intact HEAD matching `expected_commit`."""
        git_dir = repo_dir / ".git"
        if repo_dir.is_symlink() or git_dir.is_symlink() or not git_dir.is_dir():
            return False
        for rel in expected_entries:
            target = repo_dir / rel
            if target.is_symlink() or not target.exists():
                return False
        try:
            has_worktree_content = any(
                p.name not in (".git", ".colabone-checkout.json")
                for p in repo_dir.iterdir()
            )
        except OSError:
            return False
        if not has_worktree_content:
            return False

        try:
            res = subprocess.run(
                ["git", "-C", str(repo_dir), "rev-parse", "--verify", "HEAD"],
                capture_output=True,
                text=True,
                timeout=30,
                stdin=subprocess.DEVNULL,
                env=env,
                check=False,
            )
        except (OSError, subprocess.SubprocessError):
            return False
        return (
            res.returncode == 0
            and res.stdout.strip().lower() == expected_commit.strip().lower()
        )

    def _ensure_repo_checkout(
        self,
        task: Any,
        *,
        repo_url: str,
        commit: str,
        repo_name: str,
        log_path: Path,
        label: str,
        expected_entries: Sequence[str] = (),
    ) -> Path:
        if not _GIT_COMMIT_SHA_RE.fullmatch(str(commit or "").strip()):
            raise ValueError(
                f"{label} 源码提交哈希必须为完整 40 位十六进制 SHA：{commit!r}"
            )
        normalized_url = _normalize_git_repo_url(repo_url)

        _ensure_not_symlink(self.root_dir, "数字人引擎根目录")
        _ensure_not_symlink(self.repos_dir, "数字人源码目录")
        _ensure_not_symlink(self.logs_dir, "数字人运行日志目录")
        _ensure_not_symlink(log_path, f"{label} 日志文件")
        self.repos_dir.mkdir(parents=True, exist_ok=True)

        repo_dir = self.repos_dir / repo_name
        _ensure_not_symlink(repo_dir, f"{label} 源码目录")
        git_dir = repo_dir / ".git"
        _ensure_not_symlink(git_dir, f"{label} Git 元数据目录")
        marker_path = repo_dir / ".colabone-checkout.json"
        _ensure_not_symlink(marker_path, f"{label} 源码就绪标记")

        sub_env = _sanitized_env()

        if marker_path.is_file():
            try:
                cached = json.loads(marker_path.read_text(encoding="utf-8"))
                marker_ok = (
                    isinstance(cached, dict)
                    and cached.get("repo_url") == normalized_url
                    and cached.get("commit") == commit
                )
                if marker_ok:
                    if self._run_cmd_fn is not None:
                        _safe_task_note(
                            task, f"复用已检出的远端源码：{label}@{commit[:10]}"
                        )
                        return repo_dir
                    if self._verify_git_checkout_head(
                        repo_dir,
                        commit,
                        expected_entries=expected_entries,
                        env=sub_env,
                    ):
                        _safe_task_note(
                            task, f"复用已检出的远端源码：{label}@{commit[:10]}"
                        )
                        return repo_dir
            except (OSError, ValueError):
                pass
            try:
                marker_path.unlink(missing_ok=True)
            except OSError:
                pass

        _check_cancel(task, self._stopping_event())
        if self._run_cmd_fn is None and not shutil.which("git"):
            raise RuntimeError(f"远端缺少 Git，无法拉取 {label} 源码")

        def _checkout_once(*, fresh: bool) -> None:
            if fresh and repo_dir.exists():
                _ensure_not_symlink(repo_dir, f"{label} 源码目录")
                if repo_dir.is_dir():
                    shutil.rmtree(repo_dir, ignore_errors=True)
                else:
                    repo_dir.unlink(missing_ok=True)
            _ensure_not_symlink(repo_dir, f"{label} 源码目录")
            _ensure_not_symlink(git_dir, f"{label} Git 元数据目录")
            if git_dir.exists() and not git_dir.is_dir():
                shutil.rmtree(repo_dir, ignore_errors=True)
            repo_dir.mkdir(parents=True, exist_ok=True)

            if git_dir.is_dir():
                for lock_file in git_dir.rglob("*.lock"):
                    try:
                        if not lock_file.is_symlink():
                            lock_file.unlink(missing_ok=True)
                    except OSError:
                        pass
                try:
                    self._invoke_setup_command(
                        [
                            "git",
                            "-C",
                            str(repo_dir),
                            "remote",
                            "set-url",
                            "--",
                            "origin",
                            normalized_url,
                        ],
                        task,
                        log_path,
                        timeout=180,
                        env=sub_env,
                    )
                except RuntimeError as exc:
                    if _is_cancelled_error(exc):
                        raise
                    self._invoke_setup_command(
                        [
                            "git",
                            "-C",
                            str(repo_dir),
                            "remote",
                            "add",
                            "--",
                            "origin",
                            normalized_url,
                        ],
                        task,
                        log_path,
                        timeout=180,
                        env=sub_env,
                    )
            else:
                self._invoke_setup_command(
                    ["git", "init", "--", str(repo_dir)],
                    task,
                    log_path,
                    timeout=180,
                    env=sub_env,
                )
                self._invoke_setup_command(
                    [
                        "git",
                        "-C",
                        str(repo_dir),
                        "remote",
                        "add",
                        "--",
                        "origin",
                        normalized_url,
                    ],
                    task,
                    log_path,
                    timeout=180,
                    env=sub_env,
                )

            self._invoke_setup_command(
                [
                    "git",
                    "-C",
                    str(repo_dir),
                    "fetch",
                    "--depth",
                    "1",
                    "--",
                    "origin",
                    commit,
                ],
                task,
                log_path,
                timeout=600,
                env=sub_env,
            )
            self._invoke_setup_command(
                [
                    "git",
                    "-C",
                    str(repo_dir),
                    "checkout",
                    "--force",
                    "--detach",
                    commit,
                ],
                task,
                log_path,
                timeout=300,
                env=sub_env,
            )
            self._invoke_setup_command(
                ["git", "-C", str(repo_dir), "clean", "-ffdx"],
                task,
                log_path,
                timeout=300,
                env=sub_env,
            )
            if self._run_cmd_fn is None and not self._verify_git_checkout_head(
                repo_dir,
                commit,
                expected_entries=expected_entries,
                env=sub_env,
            ):
                raise RuntimeError(
                    f"{label} 源码检出校验失败，HEAD 与目标提交 {commit[:10]} 不一致或关键文件缺失"
                )

        msg = f"正在检出 {label} 固定版本源码：{commit[:10]}"
        _update_task_state(task, message=msg)
        _safe_task_note(task, msg)

        had_existing_dir = False
        try:
            had_existing_dir = repo_dir.exists() and (
                not repo_dir.is_dir() or any(repo_dir.iterdir())
            )
        except OSError:
            had_existing_dir = repo_dir.exists()
        try:
            _checkout_once(fresh=False)
        except Exception as exc:
            if _is_cancelled_error(exc) or not had_existing_dir:
                raise
            _check_cancel(task, self._stopping_event())
            _safe_task_note(
                task,
                f"{label} 现有 Git 仓库异常，正在清空并重新检出：{str(exc)[:240]}",
                level="warning",
            )
            _checkout_once(fresh=True)

        atomic_json(
            marker_path,
            {
                "repo_url": normalized_url,
                "commit": commit,
                "ready_at": time.time(),
            },
        )
        return repo_dir

    @staticmethod
    def _collect_repo_requirements(
        repo_dir: Path,
        *,
        req_filenames: Sequence[str],
        extra_packages: Sequence[str],
    ) -> list[str]:
        collected: list[str] = []
        for fname in req_filenames:
            req_path = repo_dir / fname
            if req_path.is_file() and not req_path.is_symlink():
                normalized = [
                    _normalize_opencv_package_spec(s)
                    for s in _extract_requirements_file_packages(req_path)
                ]
                collected = _merge_requirement_specs(
                    collected, _filter_pip_packages(normalized)
                )
        pyproject_path = repo_dir / "pyproject.toml"
        if pyproject_path.is_file() and not pyproject_path.is_symlink():
            normalized_py = [
                _normalize_opencv_package_spec(s)
                for s in _extract_pyproject_dependencies(pyproject_path)
            ]
            collected = _merge_requirement_specs(
                collected, _filter_pip_packages(normalized_py)
            )
        extra_normalized = [_normalize_opencv_package_spec(s) for s in extra_packages]
        extra_clean = _filter_pip_packages(extra_normalized)
        return _merge_requirement_specs(extra_clean, collected)

    def _ensure_venv_installed(
        self,
        task: Any,
        *,
        venv_dir: Path,
        marker_path: Path,
        spec_key: str,
        commit: str,
        packages: Sequence[str],
        optional_packages: Sequence[str] = (),
        log_path: Path,
        label: str,
        additive: bool = False,
    ) -> Path:
        _ensure_not_symlink(self.root_dir, "数字人引擎根目录")
        _ensure_not_symlink(venv_dir, f"{label} 虚拟环境目录")
        _ensure_not_symlink(marker_path, f"{label} 环境就绪标记")
        self.root_dir.mkdir(parents=True, exist_ok=True)

        torch_constraints = _collect_installed_torch_constraints()
        pkg_list = list(packages)
        opt_pkg_list = list(optional_packages)
        spec_payload = {
            "commit": commit,
            "packages": pkg_list,
            "optional_packages": opt_pkg_list,
            "torch_constraints": torch_constraints,
            "venv_mode": "system-site-packages",
        }
        spec_hash = hashlib.sha256(
            json.dumps(spec_payload, sort_keys=True, ensure_ascii=True).encode("utf-8")
        ).hexdigest()

        venv_present = (
            (
                (venv_dir / "pyvenv.cfg").is_file()
                and _find_venv_python_on_disk(venv_dir) is not None
            )
            or self._run_cmd_fn is not None
        )

        existing_marker: dict[str, Any] = {}
        marker_base_matches = False
        if marker_path.is_file():
            try:
                parsed = json.loads(marker_path.read_text(encoding="utf-8"))
                if isinstance(parsed, dict):
                    existing_marker = parsed
                    marker_base_matches = (
                        parsed.get("venv_mode") == "system-site-packages"
                        and parsed.get("torch_constraints") == torch_constraints
                        and venv_present
                    )
            except (OSError, ValueError):
                existing_marker = {}
                marker_base_matches = False

        installed_specs = (
            dict(existing_marker.get("installed_specs"))
            if marker_base_matches
            and isinstance(existing_marker.get("installed_specs"), dict)
            else {}
        )
        cur_spec = installed_specs.get(spec_key)
        if (
            marker_base_matches
            and isinstance(cur_spec, dict)
            and cur_spec.get("commit") == commit
            and cur_spec.get("spec_hash") == spec_hash
        ):
            _ensure_parent_venv_pth(venv_dir)
            venv_python = self._require_venv_python(venv_dir, label=label)
            _safe_task_note(task, f"复用已就绪的隔离环境：{label}")
            return venv_python

        _check_cancel(task, self._stopping_event())
        sub_env = _sanitized_env()

        # Rebuild venv cleanly if base constraints changed or non-additive mismatch.
        rebuild_venv = (not marker_base_matches) or (
            not additive and bool(existing_marker)
        )
        if rebuild_venv:
            installed_specs = {}
            if marker_path.is_file():
                try:
                    marker_path.unlink(missing_ok=True)
                except OSError:
                    pass
            if venv_dir.exists() and (self._run_cmd_fn is None or venv_dir.is_dir()):
                shutil.rmtree(venv_dir, ignore_errors=True)

        has_venv_files = (
            venv_present
            if marker_base_matches and not rebuild_venv
            else (
                (venv_dir / "pyvenv.cfg").is_file()
                and _find_venv_python_on_disk(venv_dir) is not None
            )
        )
        if not has_venv_files:
            if venv_dir.exists():
                shutil.rmtree(venv_dir, ignore_errors=True)
            msg = f"正在创建隔离虚拟环境：{label}（保留宿主 PyTorch）"
            _update_task_state(task, message=msg)
            _safe_task_note(task, msg)
            try:
                self._invoke_setup_command(
                    [
                        sys.executable,
                        "-m",
                        "venv",
                        "--without-pip",
                        "--system-site-packages",
                        "--",
                        str(venv_dir),
                    ],
                    task,
                    log_path,
                    timeout=300,
                    env=sub_env,
                )
                _ensure_parent_venv_pth(venv_dir)
                venv_python_disk = _find_venv_python_on_disk(venv_dir)
                if venv_python_disk is not None:
                    try:
                        self._invoke_setup_command(
                            [str(venv_python_disk), "-m", "pip", "--version"],
                            task,
                            log_path,
                            timeout=120,
                            env=sub_env,
                        )
                    except RuntimeError as exc:
                        if _is_cancelled_error(exc):
                            raise
                        self._invoke_setup_command(
                            [str(venv_python_disk), "-m", "ensurepip", "--upgrade"],
                            task,
                            log_path,
                            timeout=180,
                            env=sub_env,
                        )
                elif self._run_cmd_fn is None:
                    raise RuntimeError(
                        f"{label} 虚拟环境创建不完整，缺少 Python 可执行文件：{venv_dir}"
                    )
            except BaseException:
                shutil.rmtree(venv_dir, ignore_errors=True)
                raise
        else:
            _ensure_parent_venv_pth(venv_dir)

        venv_python = self._require_venv_python(venv_dir, label=label)
        constraints_path = _write_torch_constraints_file(
            self.root_dir / f".{venv_dir.name}-torch-constraints.txt",
            torch_constraints,
        )

        req_out_path: Path | None = None
        if pkg_list:
            req_out_path = self.root_dir / f".{venv_dir.name}-{spec_key}-requirements.txt"
            _atomic_write_text(req_out_path, "\n".join(pkg_list) + "\n")
            msg = f"正在安装 {label} 运行依赖（共 {len(pkg_list)} 个包，保留现有 PyTorch）"
            _update_task_state(task, message=msg)
            _safe_task_note(task, msg)
            self._invoke_setup_command(
                [
                    str(venv_python),
                    "-m",
                    "pip",
                    "install",
                    "--no-input",
                    "--disable-pip-version-check",
                    "-c",
                    str(constraints_path),
                    "-r",
                    str(req_out_path),
                ],
                task,
                log_path,
                timeout=1500,
                env=sub_env,
            )

        optional_installed = True
        if opt_pkg_list:
            opt_req_path = (
                self.root_dir / f".{venv_dir.name}-{spec_key}-optional-requirements.txt"
            )
            _atomic_write_text(opt_req_path, "\n".join(opt_pkg_list) + "\n")
            opt_cmd = [
                str(venv_python),
                "-m",
                "pip",
                "install",
                "--no-input",
                "--disable-pip-version-check",
                "-c",
                str(constraints_path),
            ]
            if req_out_path is not None:
                opt_cmd.extend(["-c", str(req_out_path)])
            opt_cmd.extend(["-r", str(opt_req_path)])
            try:
                self._invoke_setup_command(
                    opt_cmd,
                    task,
                    log_path,
                    timeout=900,
                    env=sub_env,
                )
            except Exception as exc:
                if _is_cancelled_error(exc):
                    raise
                optional_installed = False
                _safe_task_note(
                    task,
                    f"可选依赖（{', '.join(opt_pkg_list)}）安装失败，已跳过，{label} 将使用原始音频继续运行：{str(exc)[:300]}",
                    level="warning",
                )

        installed_specs[spec_key] = {
            "commit": commit,
            "spec_hash": spec_hash,
            "packages_count": len(pkg_list),
            "optional_installed": optional_installed,
            "ready_at": time.time(),
        }
        atomic_json(
            marker_path,
            {
                "venv_mode": "system-site-packages",
                "torch_constraints": torch_constraints,
                "installed_specs": installed_specs,
                "updated_at": time.time(),
            },
        )
        _safe_task_note(task, f"{label} 隔离环境已就绪")
        return venv_python

    def _ensure_hf_weights(
        self,
        task: Any,
        *,
        repo_id: str,
        revision: str,
        allow_patterns: Sequence[str],
        local_dir: Path,
        hf_token: str,
        label: str,
        venv_python: Path | None = None,
    ) -> Path:
        _ensure_not_symlink(self.logs_dir, "数字人运行日志目录")
        _ensure_not_symlink(local_dir, f"{label} 权重目录")
        patterns = list(dict.fromkeys(str(p) for p in allow_patterns if str(p).strip()))
        marker_path = local_dir / ".colabone-weights.json"
        _ensure_not_symlink(marker_path, f"{label} 权重标记")

        if self._weights_marker_valid(
            local_dir,
            repo_id=repo_id,
            revision=revision,
            required_patterns=patterns,
        ):
            _safe_task_note(task, f"复用已缓存的模型权重：{label}（{repo_id}）")
            return local_dir

        _check_cancel(task, self._stopping_event())
        local_dir.mkdir(parents=True, exist_ok=True)
        token_clean = str(hf_token or "").strip()

        msg = f"正在下载模型权重：{label}（{repo_id}@{revision[:10]}）"
        _update_task_state(task, message=msg)
        _safe_task_note(task, msg)

        try:
            if self._download_fn is not None:
                _call_with_compatible_kwargs(
                    self._download_fn,
                    (),
                    {
                        "repo_id": repo_id,
                        "revision": revision,
                        "allow_patterns": patterns,
                        "local_dir": str(local_dir),
                        "token": token_clean or None,
                    },
                )
            else:
                dl_python = (
                    Path(venv_python)
                    if venv_python is not None
                    else Path(sys.executable)
                )
                dl_env = _sanitized_env(
                    {
                        "PYTHONUNBUFFERED": "1",
                        "HF_HUB_DISABLE_TELEMETRY": "1",
                    }
                )
                incompat_proxy_or_ca = _has_hf_transfer_incompatible_env(dl_env)
                if incompat_proxy_or_ca and "HF_HUB_ENABLE_HF_TRANSFER" not in os.environ:
                    dl_env["HF_HUB_ENABLE_HF_TRANSFER"] = "0"
                if token_clean:
                    dl_env["HF_TOKEN"] = token_clean
                dl_script = (
                    "import json, os, sys\n"
                    "_incompat = any(\n"
                    "    k.lower() in ('http_proxy', 'https_proxy', 'all_proxy', 'requests_ca_bundle', 'ssl_cert_file', 'curl_ca_bundle')\n"
                    "    and str(v or '').strip()\n"
                    "    for k, v in os.environ.items()\n"
                    ")\n"
                    "try:\n"
                    "    import hf_transfer  # noqa: F401\n"
                    "    _has_hf_transfer = True\n"
                    "except Exception:\n"
                    "    _has_hf_transfer = False\n"
                    "if not _has_hf_transfer:\n"
                    "    os.environ['HF_HUB_ENABLE_HF_TRANSFER'] = '0'\n"
                    "elif _incompat:\n"
                    "    os.environ.setdefault('HF_HUB_ENABLE_HF_TRANSFER', '0')\n"
                    "else:\n"
                    "    os.environ.setdefault('HF_HUB_ENABLE_HF_TRANSFER', '1')\n"
                    "_raw_hft = str(os.environ.get('HF_HUB_ENABLE_HF_TRANSFER') or '').strip().lower()\n"
                    "if _has_hf_transfer and _raw_hft in ('1', 'true', 'yes', 'on'):\n"
                    "    os.environ['HF_HUB_ENABLE_HF_TRANSFER'] = '1'\n"
                    "else:\n"
                    "    os.environ['HF_HUB_ENABLE_HF_TRANSFER'] = '0'\n"
                    "try:\n"
                    "    from huggingface_hub import snapshot_download\n"
                    "except ImportError:\n"
                    "    sys.stderr.write('缺少 huggingface_hub，无法下载模型权重\\n')\n"
                    "    sys.exit(2)\n"
                    "if _has_hf_transfer and os.environ.get('HF_HUB_ENABLE_HF_TRANSFER') == '1':\n"
                    "    sys.stderr.write('COLABONE_HF_TRANSFER_ACTIVE=1\\n')\n"
                    "    sys.stderr.flush()\n"
                    "cfg = json.loads(sys.argv[1])\n"
                    "snapshot_download(\n"
                    "    repo_id=cfg['repo_id'],\n"
                    "    revision=cfg['revision'],\n"
                    "    allow_patterns=cfg['allow_patterns'],\n"
                    "    local_dir=cfg['local_dir'],\n"
                    "    token=os.environ.get('HF_TOKEN') or None,\n"
                    ")\n"
                )
                payload_json = json.dumps(
                    {
                        "repo_id": repo_id,
                        "revision": revision,
                        "allow_patterns": patterns,
                        "local_dir": str(local_dir),
                    },
                    ensure_ascii=False,
                )
                self.logs_dir.mkdir(parents=True, exist_ok=True)
                dl_log = self.logs_dir / f"hf_download_{local_dir.name}.log"
                dl_cmd = [str(dl_python), "-c", dl_script, payload_json]
                raw_hf_transfer_env = dl_env.get("HF_HUB_ENABLE_HF_TRANSFER")
                hf_transfer_disabled = (
                    "HF_HUB_ENABLE_HF_TRANSFER" in dl_env
                    and (
                        not str(raw_hf_transfer_env or "").strip()
                        or str(raw_hf_transfer_env or "").strip().lower()
                        in ("0", "false", "no", "off")
                    )
                )
                try:
                    dl_log_size = (
                        int(dl_log.stat().st_size)
                        if (not dl_log.is_symlink() and dl_log.is_file())
                        else 0
                    )
                    dl_log_offset = (
                        0 if dl_log_size > _MAX_LOG_FILE_BYTES else dl_log_size
                    )
                except OSError:
                    dl_log_offset = 0
                try:
                    self._run_plain_subprocess(
                        dl_cmd,
                        task,
                        dl_log,
                        timeout=3600,
                        env=dl_env,
                        stopping=self._stopping_event(),
                        secret_to_scrub=token_clean,
                        append_log=True,
                        stage_label=f"{label} 权重下载",
                    )
                except Exception as dl_exc:
                    if _is_cancelled_error(dl_exc) or hf_transfer_disabled:
                        raise
                    attempt_head, attempt_tail = _read_log_attempt_head_tail(
                        dl_log, dl_log_offset
                    )
                    attempt_had_hf_transfer = (
                        any(
                            ln.strip() == _HF_TRANSFER_ACTIVE_MARKER
                            for ln in attempt_head.splitlines()
                        )
                        or any(
                            ln.strip() == _HF_TRANSFER_ACTIVE_MARKER
                            for ln in attempt_tail.splitlines()
                        )
                        or (_HF_TRANSFER_ACTIVE_MARKER in str(dl_exc))
                    )
                    if (
                        not attempt_had_hf_transfer
                        or _is_non_retryable_hf_download_error(
                            f"{dl_exc}\n{attempt_tail}"
                        )
                    ):
                        raise
                    _check_cancel(task, self._stopping_event())
                    _safe_task_note(
                        task,
                        f"{label} 权重加速下载失败，自动关闭 hf_transfer 并回退标准下载重试一次",
                        level="warning",
                    )
                    retry_env = dict(dl_env)
                    retry_env["HF_HUB_ENABLE_HF_TRANSFER"] = "0"
                    self._run_plain_subprocess(
                        dl_cmd,
                        task,
                        dl_log,
                        timeout=3600,
                        env=retry_env,
                        stopping=self._stopping_event(),
                        secret_to_scrub=token_clean,
                        append_log=True,
                        stage_label=f"{label} 权重下载",
                    )
        except Exception as exc:
            if _is_cancelled_error(exc):
                raise
            scrubbed = _scrub_secret(str(exc), token_clean)
            raise RuntimeError(
                f"下载 {label} 模型权重失败（{repo_id}）：{scrubbed}"
            ) from None

        if self._download_fn is None and not _weights_files_satisfy_patterns(
            local_dir, patterns
        ):
            raise RuntimeError(
                f"{label} 模型权重下载不完整，缺少必需权重文件：{local_dir}"
            )

        prev_patterns: list[str] = []
        if marker_path.is_file():
            try:
                prev = json.loads(marker_path.read_text(encoding="utf-8"))
                if (
                    isinstance(prev, dict)
                    and prev.get("repo_id") == repo_id
                    and prev.get("revision") == revision
                    and isinstance(prev.get("downloaded_patterns"), list)
                ):
                    raw_prev = [
                        str(p) for p in prev["downloaded_patterns"] if str(p).strip()
                    ]
                    if self._download_fn is None:
                        prev_patterns = [
                            p
                            for p in raw_prev
                            if _weights_files_satisfy_patterns(local_dir, [p])
                        ]
                    else:
                        prev_patterns = raw_prev
            except (OSError, ValueError):
                prev_patterns = []

        merged_patterns = sorted(set(prev_patterns) | set(patterns))
        marker_payload: dict[str, Any] = {
            "repo_id": repo_id,
            "revision": revision,
            "downloaded_patterns": merged_patterns,
            "updated_at": time.time(),
        }
        if self._download_fn is None:
            marker_payload["file_sizes"] = _collect_weight_file_sizes(local_dir)
        atomic_json(marker_path, marker_payload)
        _safe_task_note(task, f"{label} 模型权重准备完成")
        return local_dir

    # -- Public engine provisioning methods ---------------------------------

    def ensure_longcat16_ready(
        self,
        task: Any,
        *,
        hf_token: str = "",
        sampling_mode: str = "non_distill_cfg",
        vocal_separation: str = "auto",
        progress_range: tuple[float, float] = (0.18, 0.30),
    ) -> dict[str, Path]:
        """Prepare pinned LongCat-Video repo, isolated venv, and pure BF16 Avatar 1.6 weights."""
        with self._get_env_lock("longcat16"):
            _check_cancel(task, self._stopping_event())
            _ensure_not_symlink(self.root_dir, "数字人引擎根目录")
            _ensure_not_symlink(self.logs_dir, "数字人运行日志目录")
            self.root_dir.mkdir(parents=True, exist_ok=True)
            self.logs_dir.mkdir(parents=True, exist_ok=True)
            setup_log = self.logs_dir / "setup_longcat16.log"

            _update_task_state(
                task,
                progress=_interp_progress(progress_range, 0.0),
                message="正在准备 LongCat-Video-Avatar 1.6 原生运行环境",
            )

            repo_dir = self._ensure_repo_checkout(
                task,
                repo_url=COLABONE_LONGCAT16_REPO,
                commit=COLABONE_LONGCAT16_COMMIT,
                repo_name="LongCat-Video",
                log_path=setup_log,
                label="LongCat-Video-Avatar 1.6",
                expected_entries=("longcat_video",),
            )
            _update_task_state(task, progress=_interp_progress(progress_range, 0.15))

            core_packages = _filter_pip_packages(_LONGCAT16_CORE_PACKAGES)
            optional_packages = _filter_pip_packages(_LONGCAT16_OPTIONAL_PACKAGES)
            python_path = self._ensure_venv_installed(
                task,
                venv_dir=self.longcat16_venv_dir,
                marker_path=self.root_dir / ".longcat16_venv.ready.json",
                spec_key="longcat16",
                commit=COLABONE_LONGCAT16_COMMIT,
                packages=core_packages,
                optional_packages=optional_packages,
                log_path=setup_log,
                label="LongCat-Video-Avatar 1.6",
                additive=False,
            )
            gpu_caps: dict[str, Any] = {}
            if self._run_cmd_fn is None or self._accel_wheels_fn is not None:
                gpu_caps = self.ensure_gpu_acceleration_wheels(task)
            if self._run_cmd_fn is None:
                _prune_shadowing_sageattention(self.longcat16_venv_dir, gpu_caps)
            _ensure_parent_venv_pth(self.longcat16_venv_dir)
            _update_task_state(task, progress=_interp_progress(progress_range, 0.40))

            norm_sampling = str(sampling_mode or "non_distill_cfg").strip().lower()
            if norm_sampling == "auto":
                norm_sampling = "non_distill_cfg"
            avatar_patterns = longcat16_avatar_allow_patterns(
                sampling_mode=norm_sampling,
                vocal_separation=vocal_separation,
            )
            avatar_est_mib = self._estimate_missing_component_mib(
                None,
                subdir="longcat16_avatar",
                repo_id=COLABONE_LONGCAT16_AVATAR_HF_REPO,
                revision=COLABONE_LONGCAT16_AVATAR_HF_REV,
                required_patterns=avatar_patterns,
            )
            avatar_dir = self._resolve_weights_dir(
                "longcat16_avatar",
                repo_id=COLABONE_LONGCAT16_AVATAR_HF_REPO,
                revision=COLABONE_LONGCAT16_AVATAR_HF_REV,
                required_patterns=avatar_patterns,
                min_free_bytes=avatar_est_mib * 1024 * 1024,
            )
            avatar_dir = self._ensure_hf_weights(
                task,
                repo_id=COLABONE_LONGCAT16_AVATAR_HF_REPO,
                revision=COLABONE_LONGCAT16_AVATAR_HF_REV,
                allow_patterns=avatar_patterns,
                local_dir=avatar_dir,
                hf_token=hf_token,
                label="LongCat-Video-Avatar 1.6",
                venv_python=python_path,
            )
            _update_task_state(task, progress=_interp_progress(progress_range, 0.78))

            base_patterns = longcat16_base_allow_patterns()
            base_dir = self._resolve_weights_dir(
                "longcat16_base",
                repo_id=COLABONE_LONGCAT16_BASE_HF_REPO,
                revision=COLABONE_LONGCAT16_BASE_HF_REV,
                required_patterns=base_patterns,
                min_free_bytes=_LONGCAT16_BASE_ESTIMATED_MIB * 1024 * 1024,
            )
            base_dir = self._ensure_hf_weights(
                task,
                repo_id=COLABONE_LONGCAT16_BASE_HF_REPO,
                revision=COLABONE_LONGCAT16_BASE_HF_REV,
                allow_patterns=base_patterns,
                local_dir=base_dir,
                hf_token=hf_token,
                label="LongCat-Video 基础编码器与 VAE",
                venv_python=python_path,
            )

            compile_cache_dir = self._resolve_compile_cache_dir()
            _update_task_state(
                task,
                progress=_interp_progress(progress_range, 1.0),
                message="LongCat-Video-Avatar 1.6 原生环境与权重已就绪",
            )
            return {
                "python": Path(python_path),
                "repo_dir": Path(repo_dir),
                "avatar_dir": Path(avatar_dir),
                "base_dir": Path(base_dir),
                "compile_cache_dir": Path(compile_cache_dir),
            }

    def ensure_vsr_ready(
        self,
        task: Any,
        *,
        engine: str,
        hf_token: str = "",
        progress_range: tuple[float, float] = (0.72, 0.78),
    ) -> dict[str, Path]:
        """Prepare pinned SeedVR2 7B or FlashVSR+ repo, dedicated isolated venv, and lazy weights."""
        norm_engine = str(engine or "").strip().lower()
        if norm_engine not in COLABONE_SUPPORTED_VSR_ENGINES:
            raise ValueError(
                f"不支持的远端扩散超分引擎：{engine!r}（仅支持 seedvr2_7b_sharp、seedvr2_7b、flashvsr_plus）"
            )

        hf_repo, hf_rev, allow_patterns = vsr_engine_allow_patterns(norm_engine)
        if norm_engine == "flashvsr_plus":
            repo_url = COLABONE_FLASHVSR_REPO
            commit = COLABONE_FLASHVSR_COMMIT
            repo_name = "FlashVSR_Plus"
            spec_key = "flashvsr_plus"
            lock_key = "vsr_flashvsr"
            venv_dir = self.flashvsr_venv_dir
            marker_path = self.root_dir / ".flashvsr_venv.ready.json"
            weights_subdir = "flashvsr_plus"
            extra_pkgs = _FLASHVSR_EXTRA_PACKAGES
            min_free_bytes = _FLASHVSR_ESTIMATED_MIB * 1024 * 1024
            expected_entries: tuple[str, ...] = (
                "inference/pipeline.py",
                "prompt_tensor/posi_prompt.pth",
            )
            label = "FlashVSR+ v1.1"
        else:
            repo_url = COLABONE_SEEDVR2_REPO
            commit = COLABONE_SEEDVR2_COMMIT
            repo_name = "ComfyUI-SeedVR2_VideoUpscaler"
            spec_key = "seedvr2"
            lock_key = "vsr_seedvr2"
            venv_dir = self.seedvr2_venv_dir
            marker_path = self.root_dir / ".seedvr2_venv.ready.json"
            weights_subdir = "seedvr2"
            extra_pkgs = _SEEDVR2_EXTRA_PACKAGES
            min_free_bytes = _SEEDVR2_ESTIMATED_MIB * 1024 * 1024
            expected_entries = ("inference_cli.py",)
            label = (
                "SeedVR2 7B Sharp"
                if norm_engine == "seedvr2_7b_sharp"
                else "SeedVR2 7B"
            )

        with self._get_env_lock(lock_key):
            _check_cancel(task, self._stopping_event())
            _ensure_not_symlink(self.root_dir, "数字人引擎根目录")
            _ensure_not_symlink(self.logs_dir, "数字人运行日志目录")
            self.root_dir.mkdir(parents=True, exist_ok=True)
            self.logs_dir.mkdir(parents=True, exist_ok=True)
            setup_log = self.logs_dir / f"setup_{spec_key}.log"

            _update_task_state(
                task,
                progress=_interp_progress(progress_range, 0.0),
                message=f"正在准备 {label} 扩散超分环境",
            )

            repo_dir = self._ensure_repo_checkout(
                task,
                repo_url=repo_url,
                commit=commit,
                repo_name=repo_name,
                log_path=setup_log,
                label=label,
                expected_entries=expected_entries,
            )
            _update_task_state(task, progress=_interp_progress(progress_range, 0.25))

            packages = self._collect_repo_requirements(
                repo_dir,
                req_filenames=("requirements.txt",),
                extra_packages=extra_pkgs,
            )
            python_path = self._ensure_venv_installed(
                task,
                venv_dir=venv_dir,
                marker_path=marker_path,
                spec_key=spec_key,
                commit=commit,
                packages=packages,
                log_path=setup_log,
                label=label,
                additive=False,
            )
            gpu_caps: dict[str, Any] = {}
            if self._run_cmd_fn is None or self._accel_wheels_fn is not None:
                gpu_caps = self.ensure_gpu_acceleration_wheels(task)
            if self._run_cmd_fn is None:
                _prune_shadowing_sageattention(venv_dir, gpu_caps)
            _ensure_parent_venv_pth(venv_dir)
            _update_task_state(task, progress=_interp_progress(progress_range, 0.55))

            weights_dir = self._resolve_weights_dir(
                weights_subdir,
                repo_id=hf_repo,
                revision=hf_rev,
                required_patterns=allow_patterns,
                min_free_bytes=min_free_bytes,
            )
            weights_dir = self._ensure_hf_weights(
                task,
                repo_id=hf_repo,
                revision=hf_rev,
                allow_patterns=allow_patterns,
                local_dir=weights_dir,
                hf_token=hf_token,
                label=label,
                venv_python=python_path,
            )

            _update_task_state(
                task,
                progress=_interp_progress(progress_range, 1.0),
                message=f"{label} 扩散超分环境与权重已就绪",
            )
            return {
                "python": Path(python_path),
                "repo_dir": Path(repo_dir),
                "weights_dir": Path(weights_dir),
            }

    # -- Streaming worker execution & manifest contract ---------------------

    def _handle_worker_output_line(
        self,
        raw_line: str,
        task: Any,
        *,
        progress_range: tuple[float, float],
        last_msg_holder: list[str],
        stage_label: str = "",
        progress_state: dict[str, Any] | None = None,
    ) -> bool:
        segments = raw_line.split("\r") if "\r" in raw_line else [raw_line]
        saw_progress = False
        now = time.monotonic()
        for seg in segments:
            payload = parse_colabone_progress_line(seg)
            if payload is not None:
                frac = _extract_progress_fraction(payload)
                mapped = (
                    _interp_progress(progress_range, frac) if frac is not None else None
                )
                raw_msg = (
                    payload.get("message")
                    or payload.get("detail")
                    or payload.get("status")
                    or ""
                )
                msg = str(raw_msg).strip() if raw_msg else ""
                stage_val = str(payload.get("stage") or "").strip().lower()
                step_val = payload.get("step")
                seg_val = payload.get("segment")
                tot_seg_val = payload.get("total_segments")
                if progress_state is not None:
                    sig = (
                        round(frac, 5) if frac is not None else None,
                        msg,
                        step_val if isinstance(step_val, int) and not isinstance(step_val, bool) else None,
                        seg_val if isinstance(seg_val, int) and not isinstance(seg_val, bool) else None,
                    )
                    if sig != progress_state.get("last_structured_sig"):
                        saw_progress = True
                        progress_state["last_structured_sig"] = sig
                    progress_state["seen_structured"] = True
                    progress_state["last_structured_at"] = now
                    if (
                        isinstance(step_val, int)
                        and not isinstance(step_val, bool)
                        and step_val > 0
                    ):
                        progress_state["seen_step_progress"] = True
                    if stage_val and stage_val != "generate":
                        progress_state.pop("segment", None)
                        progress_state.pop("total_segments", None)
                    elif (
                        isinstance(seg_val, int)
                        and not isinstance(seg_val, bool)
                        and isinstance(tot_seg_val, int)
                        and not isinstance(tot_seg_val, bool)
                        and 1 <= seg_val <= tot_seg_val
                    ):
                        progress_state["segment"] = seg_val
                        progress_state["total_segments"] = tot_seg_val
                else:
                    saw_progress = True
                if mapped is not None or msg:
                    _update_task_state(
                        task,
                        progress=mapped,
                        message=msg or None,
                        monotonic=True,
                    )
                if msg and msg != last_msg_holder[0]:
                    last_msg_holder[0] = msg
                    level = str(payload.get("level") or "info").strip().lower()
                    if level not in ("info", "warning", "error"):
                        level = "info"
                    _safe_task_note(task, msg, level=level)
                continue

            cleaned = seg.strip()
            if not cleaned:
                continue

            # Fallback tqdm denoising/inference step progress when structured step hook is absent.
            tqdm_match = _TQDM_STEP_PROGRESS_RE.search(cleaned)
            if tqdm_match is not None:
                try:
                    step_cur = int(tqdm_match.group(1))
                    step_tot = int(tqdm_match.group(2))
                except ValueError:
                    step_cur, step_tot = 0, 0
                if 1 <= step_cur <= step_tot <= 5000:
                    prev_step = (
                        progress_state.get("last_tqdm_step")
                        if progress_state is not None
                        else None
                    )
                    if prev_step != (step_cur, step_tot):
                        saw_progress = True
                    has_native_steps = bool(
                        progress_state is not None
                        and progress_state.get("seen_step_progress")
                    )
                    last_struct = (
                        float(progress_state.get("last_structured_at") or 0.0)
                        if progress_state is not None
                        else 0.0
                    )
                    last_tqdm_at = (
                        float(progress_state.get("last_tqdm_at") or 0.0)
                        if progress_state is not None
                        else 0.0
                    )
                    if (
                        not has_native_steps
                        and (now - last_struct) >= 5.0
                        and (prev_step != (step_cur, step_tot) or (now - last_tqdm_at) >= 2.0)
                    ):
                        if progress_state is not None:
                            progress_state["last_tqdm_step"] = (step_cur, step_tot)
                            progress_state["last_tqdm_at"] = now
                        mapped_tqdm: float | None = None
                        seg_cur = (
                            progress_state.get("segment")
                            if progress_state is not None
                            else None
                        )
                        seg_tot = (
                            progress_state.get("total_segments")
                            if progress_state is not None
                            else None
                        )
                        if (
                            isinstance(seg_cur, int)
                            and isinstance(seg_tot, int)
                            and 1 <= seg_cur <= seg_tot
                        ):
                            seg_frac = 0.30 + 0.65 * (
                                (float(seg_cur - 1) + 0.96 * (float(step_cur) / float(step_tot)))
                                / float(seg_tot)
                            )
                            mapped_tqdm = _interp_progress(progress_range, seg_frac)
                            step_msg = (
                                f"正在渲染第 {seg_cur}/{seg_tot} 段（第 {step_cur}/{step_tot} 步）"
                            )
                        else:
                            label = stage_label or "远端渲染"
                            step_msg = f"{label} · 推理进度 {step_cur}/{step_tot}"
                        _update_task_state(
                            task,
                            progress=mapped_tqdm,
                            message=step_msg,
                            monotonic=True,
                        )
                    continue

            # Detect worker terminal error banners or diagnostic warnings/errors so they surface live.
            if any(marker in cleaned for marker in _TERMINAL_WORKER_ERROR_MARKERS):
                if progress_state is not None and progress_state.get("terminal_error_at") is None:
                    progress_state["terminal_error_at"] = now
                    progress_state["terminal_error_msg"] = cleaned[:400]
                last_diag = (
                    str(progress_state.get("last_diag") or "")
                    if progress_state is not None
                    else ""
                )
                if cleaned != last_diag:
                    if progress_state is not None:
                        progress_state["last_diag"] = cleaned
                    _safe_task_note(task, cleaned[:300], level="error")
                continue

            is_err_diag = any(marker in cleaned for marker in _WORKER_DIAG_ERROR_MARKERS)
            is_warn_diag = (not is_err_diag) and any(
                marker in cleaned for marker in _WORKER_DIAG_WARNING_MARKERS
            )
            if is_err_diag or is_warn_diag:
                last_diag = (
                    str(progress_state.get("last_diag") or "")
                    if progress_state is not None
                    else ""
                )
                if cleaned != last_diag:
                    if progress_state is not None:
                        progress_state["last_diag"] = cleaned
                    _safe_task_note(
                        task,
                        cleaned[:300],
                        level="error" if is_err_diag else "warning",
                    )
        return saw_progress

    def _run_streaming_worker(
        self,
        cmd: Sequence[str],
        task: Any,
        *,
        work_dir: Path,
        log_path: Path,
        env: dict[str, str],
        progress_range: tuple[float, float],
        timeout: int,
        stage_label: str,
        inactivity_timeout: int | None = None,
    ) -> None:
        _ensure_not_symlink(log_path, f"{stage_label} 日志文件")
        stopping = self._stopping_event()
        last_msg_holder = [""]

        if self._run_cmd_fn is not None:
            _check_cancel(task, stopping)
            log_path.parent.mkdir(parents=True, exist_ok=True)
            result = _call_with_compatible_kwargs(
                self._run_cmd_fn,
                (list(cmd),),
                {
                    "task": task,
                    "log": log_path,
                    "timeout": timeout,
                    "cwd": work_dir,
                    "env": env,
                    "stopping": stopping,
                    "progress_range": progress_range,
                },
            )
            if isinstance(result, str):
                for line in result.splitlines():
                    self._handle_worker_output_line(
                        line,
                        task,
                        progress_range=progress_range,
                        last_msg_holder=last_msg_holder,
                    )
            elif isinstance(result, (list, tuple)):
                for item in result:
                    if isinstance(item, str):
                        self._handle_worker_output_line(
                            item,
                            task,
                            progress_range=progress_range,
                            last_msg_holder=last_msg_holder,
                        )
            elif log_path.is_file() and not log_path.is_symlink():
                try:
                    for line in log_path.read_text(
                        encoding="utf-8", errors="replace"
                    ).splitlines():
                        self._handle_worker_output_line(
                            line,
                            task,
                            progress_range=progress_range,
                            last_msg_holder=last_msg_holder,
                        )
                except OSError:
                    pass
            _check_cancel(task, stopping)
            return

        _check_cancel(task, stopping)
        log_fh = _open_secure_log_file(log_path, append=False)
        try:
            proc = subprocess.Popen(
                list(cmd),
                cwd=str(work_dir),
                env=env,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                stdin=subprocess.DEVNULL,
                start_new_session=True,
                bufsize=0,
            )
        except BaseException:
            try:
                log_fh.close()
            except OSError:
                pass
            raise

        has_cancel_hook = task is not None and hasattr(task, "cancel_hook")
        task_lock = getattr(task, "lock", None) if has_cancel_hook else None
        prev_cancel_hook = None
        term_lock = threading.Lock()
        pgrp_cleaned = False
        tail_lock = threading.Lock()

        def _cleanup_pgrp() -> None:
            nonlocal pgrp_cleaned
            with term_lock:
                if not pgrp_cleaned:
                    pgrp_cleaned = True
                    _terminate_process_group(proc, timeout=4.0)

        def _cancel_proc() -> None:
            _cleanup_pgrp()
            if callable(prev_cancel_hook):
                try:
                    prev_cancel_hook()
                except Exception:
                    pass

        if has_cancel_hook:
            with task_lock if task_lock is not None else nullcontext():
                prev_cancel_hook = getattr(task, "cancel_hook", None)
                task.cancel_hook = _cancel_proc

        line_queue: queue.Queue[Any] = queue.Queue()
        tail_lines: collections.deque[str] = collections.deque(maxlen=80)

        def _reader_loop() -> None:
            try:
                assert proc.stdout is not None
                with log_fh:
                    for raw_line in _iter_pipe_lines(proc.stdout):
                        decoded = _scrub_secret(raw_line, "")
                        log_fh.write((decoded + "\n").encode("utf-8", errors="replace"))
                        log_fh.flush()
                        stripped = decoded.rstrip("\r\n")
                        if stripped:
                            with tail_lock:
                                tail_lines.append(stripped)
                        line_queue.put(stripped)
            except OSError:
                pass
            finally:
                try:
                    if proc.stdout is not None:
                        proc.stdout.close()
                except OSError:
                    pass
                line_queue.put(_EOF_SENTINEL)

        reader = threading.Thread(
            target=_reader_loop,
            name=f"colabone-avatar-stream-{proc.pid}",
            daemon=True,
        )
        reader.start()

        effective_timeout = max(1, int(timeout))
        if inactivity_timeout is None:
            effective_inactivity = _DEFAULT_WORKER_INACTIVITY_TIMEOUT_S
        else:
            effective_inactivity = max(0, int(inactivity_timeout))

        now_start = time.monotonic()
        deadline = now_start + effective_timeout
        last_activity = now_start
        last_progress_activity = now_start
        progress_state: dict[str, Any] = {
            "seen_structured": False,
            "last_structured_at": 0.0,
            "last_tqdm_at": 0.0,
            "last_tqdm_step": (-1, -1),
            "last_diag": "",
            "terminal_error_at": None,
            "terminal_error_msg": "",
        }
        proc_exited_at: float | None = None
        pipe_drain_grace_s = 2.0
        pipe_drain_timed_out = False
        try:
            eof_seen = False
            while not eof_seen:
                _check_cancel(task, stopping)
                now = time.monotonic()
                if now > deadline:
                    raise RuntimeError(f"{stage_label} 执行超时，请查看运行日志")
                if effective_inactivity > 0:
                    if (now - last_activity) > effective_inactivity:
                        raise RuntimeError(
                            f"{stage_label} 长时间无输出与进度更新（超过 {effective_inactivity} 秒），进程疑似卡死，请查看运行日志"
                        )
                    if (
                        progress_state["seen_structured"]
                        and (now - last_progress_activity) > effective_inactivity
                    ):
                        raise RuntimeError(
                            f"{stage_label} 长时间无进度推进（超过 {effective_inactivity} 秒），进程疑似卡死，请查看运行日志"
                        )
                term_err_at = progress_state.get("terminal_error_at")
                if term_err_at is not None and (now - float(term_err_at)) >= 15.0:
                    err_detail = str(progress_state.get("terminal_error_msg") or "子进程退出挂起")
                    raise RuntimeError(
                        f"{stage_label} 执行失败且子进程清理超时：{err_detail}"
                    )
                if proc.poll() is not None:
                    if proc_exited_at is None:
                        proc_exited_at = now
                    elif (now - proc_exited_at) >= pipe_drain_grace_s:
                        pipe_drain_timed_out = True
                        break
                try:
                    item = line_queue.get(timeout=0.2)
                except queue.Empty:
                    if proc.poll() is not None:
                        if not reader.is_alive():
                            break
                        now_empty = time.monotonic()
                        if proc_exited_at is None:
                            proc_exited_at = now_empty
                        elif (now_empty - proc_exited_at) >= pipe_drain_grace_s:
                            pipe_drain_timed_out = True
                            break
                    continue

                if item is _EOF_SENTINEL:
                    break
                if item:
                    last_activity = time.monotonic()
                if self._handle_worker_output_line(
                    item,
                    task,
                    progress_range=progress_range,
                    last_msg_holder=last_msg_holder,
                    stage_label=stage_label,
                    progress_state=progress_state,
                ):
                    last_progress_activity = time.monotonic()
                drained = 0
                while drained < 64:
                    try:
                        more = line_queue.get_nowait()
                    except queue.Empty:
                        break
                    drained += 1
                    if more is _EOF_SENTINEL:
                        eof_seen = True
                        break
                    if more:
                        last_activity = time.monotonic()
                    if self._handle_worker_output_line(
                        more,
                        task,
                        progress_range=progress_range,
                        last_msg_holder=last_msg_holder,
                        stage_label=stage_label,
                        progress_state=progress_state,
                    ):
                        last_progress_activity = time.monotonic()

            while proc.poll() is None:
                _check_cancel(task, stopping)
                now = time.monotonic()
                if now > deadline:
                    raise RuntimeError(f"{stage_label} 进程退出超时，请查看运行日志")
                wait_slice = min(0.2, max(0.01, deadline - now))
                try:
                    proc.wait(timeout=wait_slice)
                except subprocess.TimeoutExpired:
                    continue

            if pipe_drain_timed_out:
                _cleanup_pgrp()
            reader.join(timeout=2.0)
            _check_cancel(task, stopping)
            if proc.returncode:
                with tail_lock:
                    captured = list(tail_lines)
                fallback = (
                    ""
                    if captured
                    else _scrub_secret(_read_log_tail(log_path, 4096), "")
                )
                tail_text = _summarize_error_tail(captured, fallback, max_chars=800)
                raise RuntimeError(
                    f"{stage_label} 执行失败（退出码 {proc.returncode}）：{tail_text}"
                )
        finally:
            try:
                _cleanup_pgrp()
                reader.join(timeout=2.0)
            finally:
                if has_cancel_hook:
                    with task_lock if task_lock is not None else nullcontext():
                        if getattr(task, "cancel_hook", None) is _cancel_proc:
                            task.cancel_hook = prev_cancel_hook

    def _load_and_validate_manifest(
        self,
        manifest_path: Path,
        *,
        stage_label: str,
        work_dir: Path | None = None,
    ) -> dict[str, Any]:
        effective_work_dir = (
            Path(work_dir) if work_dir is not None else manifest_path.parent
        )
        resolved_manifest = _validate_contained_path(
            manifest_path, effective_work_dir, f"{stage_label} 输出清单"
        )
        if not resolved_manifest.is_file() or resolved_manifest.stat().st_size <= 0:
            raise RuntimeError(
                f"{stage_label} 执行完成但未生成有效输出清单：{resolved_manifest}"
            )
        try:
            manifest = json.loads(resolved_manifest.read_text(encoding="utf-8"))
        except (OSError, ValueError) as exc:
            raise RuntimeError(f"{stage_label} 输出清单解析失败：{exc}") from None
        if not isinstance(manifest, dict) or not manifest:
            raise RuntimeError(f"{stage_label} 输出清单格式无效或为空")
        if manifest.get("ok") is False or (
            manifest.get("error") and manifest.get("ok") is not True
        ):
            raise RuntimeError(
                f"{stage_label} 返回错误：{manifest.get('error') or '未知错误'}"
            )

        found_video_path = False
        for path_key in ("output_video_path", "video_path", "output_path"):
            raw_val = manifest.get(path_key)
            if isinstance(raw_val, str) and raw_val.strip():
                found_video_path = True
                out_p = _validate_contained_path(
                    Path(raw_val.strip()),
                    effective_work_dir,
                    f"{stage_label} 输出视频",
                )
                if self._run_cmd_fn is None and (
                    not out_p.is_file() or out_p.stat().st_size <= 0
                ):
                    raise RuntimeError(
                        f"{stage_label} 声明的输出视频不存在或为空：{out_p}"
                    )
        if not found_video_path:
            raise RuntimeError(
                f"{stage_label} 输出清单缺少有效的输出视频路径（output_video_path）"
            )
        return manifest

    def _build_worker_env(self, runtime_paths: dict[str, Path]) -> dict[str, str]:
        extra_env: dict[str, str] = {
            "PYTHONUNBUFFERED": "1",
            "PYTHONUTF8": "1",
            "PYTHONIOENCODING": "utf-8",
            "USE_TORCH": "1",
            "USE_TF": "0",
            "USE_FLAX": "0",
            "USE_JAX": "0",
            "TRANSFORMERS_NO_TF": "1",
            "TRANSFORMERS_NO_FLAX": "1",
            "TRANSFORMERS_NO_JAX": "1",
            "DISABLE_TENSORFLOW": "1",
            "TF_CPP_MIN_LOG_LEVEL": "3",
        }
        if "PYTORCH_CUDA_ALLOC_CONF" not in os.environ:
            extra_env["PYTORCH_CUDA_ALLOC_CONF"] = "expandable_segments:True"
        for env_key, default_val in (
            ("CUDA_MODULE_LOADING", "LAZY"),
            ("TORCHINDUCTOR_FX_GRAPH_CACHE", "1"),
            ("TORCH_CUDNN_V8_API_ENABLED", "1"),
        ):
            if env_key not in os.environ:
                extra_env[env_key] = default_val

        compile_p = self._sanitize_compile_cache_dir(
            runtime_paths.get("compile_cache_dir")
        )
        triton_dir = compile_p / "triton"
        _ensure_not_symlink(triton_dir, "Triton 编译缓存目录")
        triton_dir.mkdir(parents=True, exist_ok=True)
        extra_env["TORCHINDUCTOR_CACHE_DIR"] = str(compile_p)
        extra_env["TRITON_CACHE_DIR"] = str(triton_dir)

        pythonpath_parts: list[str] = []
        repo_dir = runtime_paths.get("repo_dir")
        if repo_dir is not None:
            repo_p = Path(repo_dir)
            pythonpath_parts.append(str(repo_p))
            flashvsr_attn_dir = repo_p / "operators" / "flashvsr_attn"
            try:
                if (
                    flashvsr_attn_dir.is_dir()
                    and not flashvsr_attn_dir.is_symlink()
                ):
                    pythonpath_parts.append(str(flashvsr_attn_dir))
            except OSError:
                pass
        project_root = str(Path(__file__).resolve().parents[1])
        pythonpath_parts.append(project_root)
        extra_env["PYTHONPATH"] = os.pathsep.join(pythonpath_parts)
        return _sanitized_env(extra_env)

    # -- Public stage runners -----------------------------------------------

    def run_longcat16(
        self,
        task: Any,
        *,
        config: dict[str, Any],
        runtime_paths: dict[str, Path],
        work_dir: Path,
        progress_range: tuple[float, float] = (0.30, 0.72),
        timeout: int | str | None = None,
        inactivity_timeout: int | None = None,
    ) -> dict[str, Any]:
        """Run isolated Stage 2 LongCat-Video-Avatar 1.6 worker and validate its manifest."""
        work_dir_p = Path(work_dir)
        _ensure_not_symlink(work_dir_p, "Stage 2 工作目录")
        work_dir_p.mkdir(parents=True, exist_ok=True)

        worker_cfg = _strip_sensitive_config_keys(
            _json_safe(copy.deepcopy(dict(config or {})))
        )
        raw_manifest = str(worker_cfg.get("output_manifest_path") or "").strip()
        manifest_path = _validate_contained_path(
            Path(raw_manifest)
            if raw_manifest
            else (work_dir_p / "stage2_longcat16_manifest.json"),
            work_dir_p,
            "Stage 2 输出清单",
        )
        worker_cfg["output_manifest_path"] = str(manifest_path)

        for vid_key in ("output_video_path", "video_path", "output_path"):
            raw_vid = str(worker_cfg.get(vid_key) or "").strip()
            if raw_vid:
                validated_vid = _validate_contained_path(
                    Path(raw_vid), work_dir_p, "Stage 2 输出视频"
                )
                worker_cfg[vid_key] = str(validated_vid)

        for cfg_key, path_key in (
            ("longcat_repo_dir", "repo_dir"),
            ("avatar_checkpoint_dir", "avatar_dir"),
            ("base_video_dir", "base_dir"),
            ("compile_cache_dir", "compile_cache_dir"),
        ):
            if path_key in runtime_paths and not worker_cfg.get(cfg_key):
                worker_cfg[cfg_key] = str(Path(runtime_paths[path_key]))

        sanitized_compile_dir = self._sanitize_compile_cache_dir(
            worker_cfg.get("compile_cache_dir") or runtime_paths.get("compile_cache_dir")
        )
        worker_cfg["compile_cache_dir"] = str(sanitized_compile_dir)
        effective_runtime_paths = dict(runtime_paths)
        effective_runtime_paths["compile_cache_dir"] = sanitized_compile_dir

        exact_frames = _safe_nonneg_int(worker_cfg.get("exact_frames"), 0)
        if timeout is None or (
            isinstance(timeout, str) and timeout.strip().lower() == "auto"
        ):
            effective_timeout = max(7200, int(exact_frames * 1.2) + 1800)
        else:
            effective_timeout = max(1, int(timeout))

        cfg_path = work_dir_p / "stage2_longcat16_config.json"
        _ensure_not_symlink(cfg_path, "Stage 2 配置文件")
        atomic_json(cfg_path, worker_cfg)

        worker_script = Path(__file__).with_name("avatar_worker_longcat16.py")
        python_bin = Path(runtime_paths["python"])
        cmd = [str(python_bin), str(worker_script), "--config", str(cfg_path)]
        log_path = work_dir_p / "stage2_longcat16.log"
        worker_env = self._build_worker_env(effective_runtime_paths)

        _check_cancel(task, self._stopping_event())
        _update_task_state(
            task,
            progress=_interp_progress(progress_range, 0.0),
            message="正在启动 LongCat-Video-Avatar 1.6 原生渲染进程",
        )
        _safe_task_note(task, "正在启动 LongCat-Video-Avatar 1.6 原生渲染进程")

        self._run_streaming_worker(
            cmd,
            task,
            work_dir=work_dir_p,
            log_path=log_path,
            env=worker_env,
            progress_range=progress_range,
            timeout=effective_timeout,
            inactivity_timeout=inactivity_timeout,
            stage_label="LongCat-Video-Avatar 1.6 原生渲染",
        )
        manifest = self._load_and_validate_manifest(
            manifest_path,
            work_dir=work_dir_p,
            stage_label="LongCat-Video-Avatar 1.6 原生渲染",
        )
        _update_task_state(task, progress=_interp_progress(progress_range, 1.0))
        return manifest

    def run_diffusion_vsr(
        self,
        task: Any,
        *,
        config: dict[str, Any],
        runtime_paths: dict[str, Path],
        work_dir: Path,
        progress_range: tuple[float, float] = (0.78, 0.94),
        timeout: int | str | None = None,
        inactivity_timeout: int | None = None,
    ) -> dict[str, Any]:
        """Run isolated Stage 3 diffusion VSR worker (SeedVR2 7B / FlashVSR+) and validate manifest."""
        work_dir_p = Path(work_dir)
        _ensure_not_symlink(work_dir_p, "Stage 3 超分工作目录")
        work_dir_p.mkdir(parents=True, exist_ok=True)

        worker_cfg = _strip_sensitive_config_keys(
            _json_safe(copy.deepcopy(dict(config or {})))
        )
        raw_manifest = str(worker_cfg.get("output_manifest_path") or "").strip()
        manifest_path = _validate_contained_path(
            Path(raw_manifest)
            if raw_manifest
            else (work_dir_p / "stage3_vsr_manifest.json"),
            work_dir_p,
            "Stage 3 超分输出清单",
        )
        worker_cfg["output_manifest_path"] = str(manifest_path)

        for vid_key in ("output_video_path", "video_path", "output_path"):
            raw_vid = str(worker_cfg.get(vid_key) or "").strip()
            if raw_vid:
                validated_vid = _validate_contained_path(
                    Path(raw_vid), work_dir_p, "Stage 3 超分输出视频"
                )
                worker_cfg[vid_key] = str(validated_vid)

        for cfg_key, path_key in (
            ("repo_dir", "repo_dir"),
            ("weights_dir", "weights_dir"),
            ("python_bin", "python"),
        ):
            if path_key in runtime_paths and not worker_cfg.get(cfg_key):
                worker_cfg[cfg_key] = str(Path(runtime_paths[path_key]))

        exact_frames = _safe_nonneg_int(worker_cfg.get("exact_frames"), 0)
        if timeout is None or (
            isinstance(timeout, str) and timeout.strip().lower() == "auto"
        ):
            effective_timeout = max(5400, int(exact_frames * 0.8) + 1200)
        else:
            effective_timeout = max(1, int(timeout))

        cfg_path = work_dir_p / "stage3_vsr_config.json"
        _ensure_not_symlink(cfg_path, "Stage 3 超分配置文件")
        atomic_json(cfg_path, worker_cfg)

        worker_script = Path(__file__).with_name("vsr_worker_diffusion.py")
        python_bin = Path(runtime_paths["python"])
        cmd = [str(python_bin), str(worker_script), "--config", str(cfg_path)]
        log_path = work_dir_p / "stage3_vsr.log"
        worker_env = self._build_worker_env(runtime_paths)

        _check_cancel(task, self._stopping_event())
        _update_task_state(
            task,
            progress=_interp_progress(progress_range, 0.0),
            message="正在启动远端扩散视频超分进程",
        )
        _safe_task_note(task, "正在启动远端扩散视频超分进程")

        self._run_streaming_worker(
            cmd,
            task,
            work_dir=work_dir_p,
            log_path=log_path,
            env=worker_env,
            progress_range=progress_range,
            timeout=effective_timeout,
            inactivity_timeout=inactivity_timeout,
            stage_label="远端扩散视频超分",
        )
        manifest = self._load_and_validate_manifest(
            manifest_path,
            work_dir=work_dir_p,
            stage_label="远端扩散视频超分",
        )
        _update_task_state(task, progress=_interp_progress(progress_range, 1.0))
        return manifest
