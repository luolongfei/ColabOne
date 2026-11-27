"""Remote Live Studio lifecycle manager, Avatar Action Pack store, and token authority.

Manages:
- Catalog loading & summary (`catalog/live_studio.json`)
- Persistent Avatar Action Packs under `<root>/live-avatars/<avatar_id>/`
- Driving Motion clips under `<root>/live-driving-motions/<motion_id>/`
- Fail-fast VRAM & disk preflight checks
- Ephemeral WebSocket session tokens (constant-time verification + TTL)
- Local `:8190` Live Studio server start/stop/status lifecycle
- External OpenAI-compatible LLM connectivity testing with strict secret scrubbing
- Offline single-photo Avatar Action Pack batch building with task progress
"""

from __future__ import annotations

import base64
import hashlib
import hmac
import inspect
import io
import json
import math
import os
import queue
import re
import secrets
import shutil
import signal
import socket
import subprocess
import sys
import threading
import time
from pathlib import Path, PurePosixPath
from typing import Any, Callable, Sequence
from urllib.parse import urlparse, urlunparse

import requests
from requests.adapters import HTTPAdapter
from PIL import Image, ImageOps

from common.files import (
    COLABONE_SENSITIVE_SUBPROCESS_ENV_KEYS,
    atomic_json,
    colabone_sanitized_subprocess_env,
)
from common.live_studio import (
    COLABONE_LIVE_BGM_EXTENSIONS,
    COLABONE_LIVE_CLIP_EXTENSIONS,
    COLABONE_LIVE_COMPANION_PATH,
    COLABONE_LIVE_IMAGE_EXTENSIONS,
    COLABONE_LIVE_MAX_BGM_BYTES,
    COLABONE_LIVE_MAX_CLIP_BYTES,
    COLABONE_LIVE_MAX_PORTRAIT_BYTES,
    COLABONE_LIVE_PORT,
    COLABONE_LIVE_WS_PATH,
    ColabOneLiveActionSpec,
    ColabOneLiveAvatarManifest,
    ColabOneLiveAvatarSaveRequest,
    ColabOneLiveBuildPackRequest,
    ColabOneLiveCatalog,
    ColabOneLiveClipUploadRequest,
    ColabOneLiveDrivingUploadRequest,
    ColabOneLiveExternalLLMTestRequest,
    ColabOneLiveSessionConfig,
    ColabOneLiveStartRequest,
    evaluate_colabone_live_vram_budget,
    scrub_colabone_live_secrets,
    validate_colabone_external_llm_resolved_ips,
    validate_colabone_live_id,
    validate_colabone_live_rtmp_resolved_ips,
)
from server.download import ColabOneTransfer
from server.live_server import (
    ColabOneLiveServerRunner,
    ColabOneLiveVideoEncoder,
    ColabOneSenseVoiceAsrAdapter,
    create_colabone_live_app,
)
from server.live_worker_tts import create_colabone_streaming_tts_engine
from server.live_worker_visual import (
    ColabOnePersonaLiveVisualEngine,
    create_colabone_visual_engine,
    parse_colabone_resolution,
)

_MAX_IMAGE_PIXELS = 16_777_216
_DEFAULT_TOKEN_TTL_S = 60.0
_MIN_FREE_DISK_HEADROOM_MIB = 2048
_CACHE_WEIGHT_SUFFIXES = frozenset(
    {".safetensors", ".bin", ".pt", ".pth", ".onnx", ".ckpt", ".gguf", ".plan"}
)
_INCOMPLETE_CACHE_SUFFIXES = (".tmp", ".part", ".incomplete")
_HF_DL_PROGRESS_PREFIX = "COLABONE_LIVE_DL:"
_HF_TRANSFER_ACTIVE_MARKER = "COLABONE_HF_TRANSFER_ACTIVE=1"
_LIVE_PROVISION_SENSITIVE_ENV_KEYS = COLABONE_SENSITIVE_SUBPROCESS_ENV_KEYS
_LIVE_SETUP_STRIP_ENV_KEYS = frozenset(
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
_LIVE_TORCH_SKIP_PACKAGES = frozenset(
    {
        "torch",
        "torchvision",
        "torchaudio",
        "xformers",
        "triton",
        "flash-attn",
        "sageattention",
        "sageattn3",
    }
)
_LIVE_FALLBACK_WEIGHT_BYTES = {
    "qwen3_tts": 4 * 1024**3,
    "voxcpm2": 4 * 1024**3,
    "sensevoice_small": 900 * 1024**2,
}

# Executed inside an isolated remote venv to download Hugging Face repos with
# structured progress JSON lines for ColabOneTransfer telemetry.
_COLABONE_LIVE_HF_DOWNLOAD_SCRIPT = r"""
import json
import os
import sys
import threading
import time

PREFIX = "COLABONE_LIVE_DL:"
_incompat = any(
    k.lower() in (
        "http_proxy",
        "https_proxy",
        "all_proxy",
        "requests_ca_bundle",
        "ssl_cert_file",
        "curl_ca_bundle",
    )
    and str(v or "").strip()
    for k, v in os.environ.items()
)
try:
    import hf_transfer  # noqa: F401
    _has_hf_transfer = True
except Exception:
    _has_hf_transfer = False

if not _has_hf_transfer:
    os.environ["HF_HUB_ENABLE_HF_TRANSFER"] = "0"
elif _incompat:
    os.environ.setdefault("HF_HUB_ENABLE_HF_TRANSFER", "0")
else:
    os.environ.setdefault("HF_HUB_ENABLE_HF_TRANSFER", "1")

_raw_hft = str(os.environ.get("HF_HUB_ENABLE_HF_TRANSFER") or "").strip().lower()
if _has_hf_transfer and _raw_hft in ("1", "true", "yes", "on"):
    os.environ["HF_HUB_ENABLE_HF_TRANSFER"] = "1"
else:
    os.environ["HF_HUB_ENABLE_HF_TRANSFER"] = "0"

try:
    from huggingface_hub import HfApi, snapshot_download
except ImportError:
    sys.stderr.write("缺少 huggingface_hub，无法下载模型权重\n")
    sys.exit(2)

if _has_hf_transfer and os.environ.get("HF_HUB_ENABLE_HF_TRANSFER") == "1":
    sys.stderr.write("COLABONE_HF_TRANSFER_ACTIVE=1\n")
    sys.stderr.flush()

cfg = json.loads(sys.argv[1])
repo_id = cfg["repo_id"]
revision = cfg.get("revision") or None
cache_dir = cfg.get("cache_dir") or None
local_dir = cfg.get("local_dir") or None
allow_patterns = cfg.get("allow_patterns") or None
token = os.environ.get("HF_TOKEN") or None

_emit_lock = threading.Lock()


def _emit(payload):
    with _emit_lock:
        sys.stdout.write(PREFIX + json.dumps(payload, ensure_ascii=False) + "\n")
        sys.stdout.flush()


plan_total = 0
plan_files = 0
try:
    info = HfApi().repo_info(
        repo_id=repo_id,
        revision=revision,
        files_metadata=True,
        token=token,
    )
    siblings = getattr(info, "siblings", None) or []
    for sib in siblings:
        sz = getattr(sib, "size", None)
        if isinstance(sz, int) and sz > 0:
            plan_total += sz
            plan_files += 1
        elif getattr(sib, "rfilename", None):
            plan_files += 1
except Exception:
    pass

_emit({"event": "plan", "total_bytes": plan_total, "file_count": plan_files})

_state_lock = threading.Lock()
_state = {
    "bytes_done": 0,
    "bytes_total": plan_total,
    "files_done": 0,
    "files_total": plan_files,
    "filename": "",
    "last_emit": 0.0,
}


def _emit_progress(force=False):
    now = time.monotonic()
    with _state_lock:
        if not force and (now - _state["last_emit"]) < 0.25:
            return
        _state["last_emit"] = now
        snap = {
            "event": "progress",
            "downloaded_bytes": int(_state["bytes_done"]),
            "total_bytes": int(_state["bytes_total"]),
            "file_index": int(_state["files_done"]),
            "file_count": int(_state["files_total"]),
            "filename": str(_state["filename"]),
        }
    _emit(snap)


class _ColabOneProgressBar:
    _lock = threading.RLock()

    def __init__(self, *args, **kwargs):
        iterable = kwargs.get("iterable")
        if iterable is None and args:
            iterable = args[0]
        self._iterable = iterable
        self.total = int(kwargs.get("total") or 0)
        self.n = int(kwargs.get("initial") or 0)
        self.desc = str(kwargs.get("desc") or "").strip()
        self.unit = str(kwargs.get("unit") or "").strip()
        self._is_file_counter = (
            self.desc.lower().startswith("fetching")
            or (self.unit and self.unit.lower() not in ("b", "ib", "byte", "bytes"))
        )
        with _state_lock:
            if self._is_file_counter:
                if self.total > _state["files_total"]:
                    _state["files_total"] = self.total
            else:
                if self.desc:
                    _state["filename"] = self.desc.split("/")[-1][:120]
                if self.n > 0:
                    _state["bytes_done"] += self.n
                if plan_total <= 0 and self.total > 0:
                    _state["bytes_total"] += self.total
        _emit_progress(force=True)

    @classmethod
    def get_lock(cls):
        return cls._lock

    @classmethod
    def set_lock(cls, lock):
        cls._lock = lock

    def __iter__(self):
        if self._iterable is None:
            return iter(())
        for item in self._iterable:
            yield item
            self.update(1)

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, tb):
        self.close()
        return False

    def update(self, n=1):
        try:
            delta = int(n or 0)
        except (TypeError, ValueError):
            delta = 0
        if delta <= 0:
            return
        self.n += delta
        with _state_lock:
            if self._is_file_counter:
                _state["files_done"] = max(_state["files_done"], self.n)
                if self.total > _state["files_total"]:
                    _state["files_total"] = self.total
            else:
                _state["bytes_done"] += delta
                if self.desc:
                    _state["filename"] = self.desc.split("/")[-1][:120]
                if _state["bytes_done"] > _state["bytes_total"]:
                    _state["bytes_total"] = _state["bytes_done"]
        _emit_progress(force=False)

    def set_description(self, desc=None, refresh=True):
        if desc is not None:
            self.desc = str(desc).strip()
            if not self._is_file_counter and self.desc:
                with _state_lock:
                    _state["filename"] = self.desc.split("/")[-1][:120]

    def set_postfix(self, *args, **kwargs):
        pass

    def refresh(self, *args, **kwargs):
        pass

    def close(self):
        _emit_progress(force=True)


dl_kwargs = {
    "repo_id": repo_id,
    "token": token,
    "tqdm_class": _ColabOneProgressBar,
}
if revision:
    dl_kwargs["revision"] = revision
if cache_dir:
    dl_kwargs["cache_dir"] = cache_dir
if local_dir:
    dl_kwargs["local_dir"] = local_dir
if allow_patterns:
    dl_kwargs["allow_patterns"] = allow_patterns

try:
    result_path = snapshot_download(**dl_kwargs)
except TypeError:
    dl_kwargs.pop("tqdm_class", None)
    result_path = snapshot_download(**dl_kwargs)

_emit_progress(force=True)
_emit({"event": "done", "path": str(result_path)})
"""


class _ColabOnePinnedIPAdapter(HTTPAdapter):
    """Requests HTTPAdapter pinning TCP dials to a validated IP while preserving SNI and Host."""

    def __init__(
        self,
        *,
        expected_host: str,
        pinned_ip: str,
        use_tls: bool = True,
        **kwargs: Any,
    ) -> None:
        self.expected_host = expected_host.strip().lower().rstrip(".")
        self.pinned_ip = pinned_ip.strip()
        self.use_tls = bool(use_tls)
        super().__init__(**kwargs)

    def init_poolmanager(self, connections: int, maxsize: int, block: bool = False, **pool_kwargs: Any) -> Any:
        if self.use_tls:
            pool_kwargs = dict(pool_kwargs)
            pool_kwargs["assert_hostname"] = self.expected_host
            pool_kwargs["server_hostname"] = self.expected_host
        return super().init_poolmanager(connections, maxsize, block=block, **pool_kwargs)

    def send(self, request: Any, *args: Any, **kwargs: Any) -> Any:
        parsed = urlparse(request.url)
        req_host = (parsed.hostname or "").strip().lower().rstrip(".")
        if req_host != self.expected_host:
            raise ValueError("外部 LLM 请求主机与已校验解析结果不一致")
        scheme = (parsed.scheme or "").lower()
        default_port = 443 if scheme == "https" else 80
        port = parsed.port
        host_header = (
            f"{req_host}:{port}"
            if port is not None and port != default_port
            else req_host
        )
        ip_literal = f"[{self.pinned_ip}]" if ":" in self.pinned_ip else self.pinned_ip
        pinned_netloc = f"{ip_literal}:{port}" if port is not None else ip_literal
        pinned_url = urlunparse(parsed._replace(netloc=pinned_netloc))
        request.url = pinned_url
        request.headers["Host"] = host_header
        return super().send(request, *args, **kwargs)


def _ensure_safe_dir(path: Path, label: str) -> None:
    if path.is_symlink():
        raise ValueError(f"{label} 不能是符号链接")
    path.mkdir(parents=True, exist_ok=True)


def _ensure_contained_path(base: Path, target: Path, label: str) -> Path:
    if base.is_symlink():
        raise ValueError(f"{label} 根目录不能是符号链接")
    if target.is_symlink():
        raise ValueError(f"{label} 不能是符号链接")
    for parent in target.parents:
        if parent == base:
            break
        if parent.is_symlink():
            raise ValueError(f"{label} 路径中不允许符号链接")
    resolved_base = base.resolve()
    resolved_target = target.resolve()
    if not resolved_target.is_relative_to(resolved_base):
        raise ValueError(f"{label} 路径越界")
    return resolved_target


def _safe_extension(filename: str, allowed: frozenset[str], label: str) -> str:
    if not isinstance(filename, str) or not filename.strip():
        raise ValueError(f"{label} 文件名不能为空")
    cleaned = filename.replace("\\", "/").replace("\x00", "")
    name = PurePosixPath(cleaned).name
    if name in ("", ".", "..") or ".." in name:
        raise ValueError(f"{label} 文件名不合法")
    ext = PurePosixPath(name).suffix.lower().lstrip(".")
    if ext not in allowed:
        allowed_str = ", ".join(sorted(allowed))
        raise ValueError(f"{label} 仅支持格式: {allowed_str}")
    return ext


def _decode_base64_payload(encoded: str, max_bytes: int, label: str) -> bytes:
    if not isinstance(encoded, str) or not encoded.strip():
        raise ValueError(f"{label} 数据为空")
    payload = encoded.strip()
    if payload.startswith("data:"):
        if "," not in payload:
            raise ValueError(f"{label} Base64 数据头不合法")
        _, payload = payload.split(",", 1)
    if len(payload) > int(max_bytes * 1.5) + 4096:
        raise ValueError(f"{label} 超过大小上限")
    try:
        raw = base64.b64decode(re.sub(r"\s+", "", payload), validate=True)
    except Exception as exc:
        raise ValueError(f"{label} Base64 编码无效") from exc
    if not raw:
        raise ValueError(f"{label} 数据为空")
    if len(raw) > max_bytes:
        raise ValueError(f"{label} 超过大小上限")
    return raw


def _normalize_portrait_png(raw: bytes) -> bytes:
    try:
        with Image.open(io.BytesIO(raw)) as img:
            if img.format not in ("PNG", "JPEG", "WEBP"):
                raise ValueError("人像仅支持 PNG、JPEG 或 WebP 格式")
            if img.width * img.height > _MAX_IMAGE_PIXELS:
                raise ValueError("人像图片像素过高（上限 1600 万像素）")
            img.load()
            norm = ImageOps.exif_transpose(img)
            if norm.mode not in ("RGB", "RGBA"):
                norm = norm.convert("RGB")
            out = io.BytesIO()
            norm.save(out, format="PNG")
            return out.getvalue()
    except ValueError:
        raise
    except Exception as exc:
        raise ValueError("请提供有效的人像图片文件") from exc


def _atomic_write_bytes(path: Path, data: bytes) -> str:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.parent / f".{path.name}.{secrets.token_hex(6)}.tmp"
    try:
        with open(tmp, "wb") as fh:
            fh.write(data)
            fh.flush()
            os.fsync(fh.fileno())
        os.replace(tmp, path)
    finally:
        if tmp.exists():
            try:
                tmp.unlink()
            except OSError:
                pass
    return hashlib.sha256(data).hexdigest()


def _safe_rmtree(directory: Path, root: Path) -> None:
    _ensure_contained_path(root, directory, "目标目录")
    for dirpath, dirnames, filenames in os.walk(directory, topdown=False, followlinks=False):
        base = Path(dirpath)
        for fname in filenames:
            fp = base / fname
            if fp.is_symlink() or fp.is_file():
                fp.unlink()
        for dname in dirnames:
            dp = base / dname
            if dp.is_symlink():
                dp.unlink()
            elif dp.is_dir():
                dp.rmdir()
    directory.rmdir()


def _is_cancelled_error(exc: BaseException) -> bool:
    return getattr(type(exc), "__name__", "") == "ColabOneCancelledError"


def _task_check(task: Any, stopping: threading.Event | None = None) -> None:
    if task is not None and callable(getattr(task, "check", None)):
        task.check()
    if stopping is not None and stopping.is_set():
        from server.runtime import ColabOneCancelledError

        raise ColabOneCancelledError("实例正在停止，操作已取消")


def _task_update(
    task: Any,
    progress: float | None = None,
    message: str | None = None,
    *,
    monotonic_progress: bool = False,
) -> None:
    if task is None or not callable(getattr(task, "update", None)):
        return
    kwargs: dict[str, Any] = {}
    if progress is not None:
        kwargs["progress"] = progress
    if message is not None:
        kwargs["message"] = message
    if not kwargs:
        return
    try:
        sig = inspect.signature(task.update)
        if "monotonic_progress" in sig.parameters:
            kwargs["monotonic_progress"] = monotonic_progress
    except (TypeError, ValueError):
        pass
    task.update(**kwargs)


def _task_set_transfer(task: Any, transfer: Any) -> None:
    if task is None or not callable(getattr(task, "update", None)):
        return
    try:
        sig = inspect.signature(task.update)
        if "transfer" in sig.parameters or any(
            p.kind == inspect.Parameter.VAR_KEYWORD for p in sig.parameters.values()
        ):
            task.update(transfer=transfer)
            return
    except (TypeError, ValueError):
        pass
    if hasattr(task, "transfer"):
        task.transfer = transfer


def _task_note(task: Any, message: str, level: str = "info") -> None:
    if task is not None and callable(getattr(task, "note", None)):
        task.note(message, level=level)


def _sanitized_live_provision_env(extra: dict[str, str] | None = None) -> dict[str, str]:
    base_extra: dict[str, str] = {
        "PYTHONUNBUFFERED": "1",
        "PYTHONUTF8": "1",
        "PYTHONIOENCODING": "utf-8",
        "HF_HUB_DISABLE_TELEMETRY": "1",
    }
    if extra:
        base_extra.update(extra)
    return colabone_sanitized_subprocess_env(
        base_extra,
        strip_keys=_LIVE_SETUP_STRIP_ENV_KEYS,
        allow_extra_sensitive=False,
    )


def _has_hf_transfer_incompatible_env(env: dict[str, str]) -> bool:
    for k, v in env.items():
        if k.lower() in _HF_TRANSFER_INCOMPATIBLE_ENV_KEYS and str(v or "").strip():
            return True
    return False


def _is_non_retryable_hf_download_error(text: str) -> bool:
    lower = str(text or "").lower()
    if any(
        needle in lower
        for needle in (
            "401 client error",
            "403 client error",
            "404 client error",
            "401 unauthorized",
            "403 forbidden",
            "404 not found",
            "gated repo",
            "gatedrepoerror",
            "repositorynotfounderror",
            "revisionnotfounderror",
            "entrynotfounderror",
            "invalid credentials",
            "access to model",
            "no space left on device",
            "errno 28",
            "enospc",
        )
    ):
        return True
    return False


def _summarize_tail_lines(lines: Sequence[str], max_chars: int = 300) -> str:
    """Join recent stderr/output lines from the tail backwards so final exception lines survive."""
    kept: list[str] = []
    total = 0
    for raw in reversed(lines):
        piece = str(raw or "").strip()
        if not piece:
            continue
        extra = len(piece) + (3 if kept else 0)
        if kept and total + extra > max_chars:
            break
        kept.append(piece)
        total += extra
    summary = " | ".join(reversed(kept))
    if len(summary) > max_chars:
        summary = "..." + summary[-(max_chars - 3):]
    return summary


def _pgid_has_non_zombie_member_linux(pgid: int) -> bool | None:
    """Return True/False if /proc can verify live (non-Z/X) members in `pgid`, else None."""
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


def _pgid_alive(pgid: int) -> bool:
    """Return True if any non-zombie process still exists in POSIX process group `pgid`."""
    try:
        os.killpg(pgid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    except OSError:
        return False
    linux_live = _pgid_has_non_zombie_member_linux(pgid)
    if linux_live is not None:
        return linux_live
    return True


def _terminate_live_process_group(proc: subprocess.Popen[Any], timeout: float = 5.0) -> None:
    """Terminate a spawned subprocess and its process-group orphans safely."""
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
        if pid_recycled or not _pgid_alive(pid):
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
            if not _pgid_alive(pid) or time.monotonic() >= deadline:
                break
            time.sleep(0.05)
        if _pgid_alive(pid):
            try:
                os.killpg(pid, signal.SIGKILL)
            except (ProcessLookupError, PermissionError, OSError):
                pass
            kill_deadline = time.monotonic() + max(0.5, min(2.0, float(timeout)))
            while time.monotonic() < kill_deadline:
                proc.poll()
                if not _pgid_alive(pid):
                    break
                time.sleep(0.05)
    elif proc.poll() is None:
        try:
            proc.kill()
        except OSError:
            pass

    if proc.poll() is None:
        try:
            proc.wait(timeout=max(0.5, min(2.0, float(timeout))))
        except subprocess.TimeoutExpired:
            pass

    if proc.poll() is not None and (not can_killpg or not _pgid_alive(pid)):
        try:
            setattr(proc, "_colabone_pgrp_cleaned", True)
        except (AttributeError, TypeError):
            pass


def _measure_dir_bytes(path: Path) -> int:
    """Sum regular file sizes under path without following symlinks."""
    if path.is_symlink() or not path.is_dir():
        return 0
    total = 0
    try:
        for dirpath, _dirnames, filenames in os.walk(path, followlinks=False):
            base = Path(dirpath)
            for fname in filenames:
                fp = base / fname
                try:
                    if not fp.is_symlink():
                        total += fp.stat().st_size
                except OSError:
                    continue
    except OSError:
        return total
    return total


def _snapshot_dir_has_verified_weights(snap_dir: Path, model_dir_resolved: Path) -> bool:
    """Verify an HF cache snapshot contains intact weight files without partial markers."""
    if snap_dir.is_symlink() or not snap_dir.is_dir():
        return False
    index_files: list[tuple[Path, Path]] = []
    has_weight_file = False
    has_ready_marker = False
    try:
        for dirpath, _dirnames, filenames in os.walk(snap_dir, followlinks=False):
            base = Path(dirpath)
            for fname in filenames:
                lower = fname.lower()
                if any(lower.endswith(sfx) for sfx in _INCOMPLETE_CACHE_SUFFIXES):
                    return False
                fpath = base / fname
                if fname == ".colabone_ready" and not fpath.is_symlink() and fpath.is_file():
                    has_ready_marker = True
                    continue
                if lower.endswith(".index.json"):
                    index_files.append((base, fpath))
                    continue
                if fpath.suffix.lower() not in _CACHE_WEIGHT_SUFFIXES:
                    continue
                # Validate EVERY weight file in the snapshot; reject broken/escaping/0-byte weights.
                try:
                    resolved = fpath.resolve(strict=True)
                    resolved.relative_to(model_dir_resolved)
                    if not resolved.is_file() or resolved.stat().st_size <= 0:
                        return False
                    has_weight_file = True
                except (OSError, ValueError):
                    return False
    except OSError:
        return False

    if index_files:
        for base, idx_path in index_files:
            try:
                idx_resolved = idx_path.resolve(strict=True)
                idx_resolved.relative_to(model_dir_resolved)
                if not idx_resolved.is_file() or idx_resolved.stat().st_size <= 0:
                    return False
                idx_data = json.loads(idx_resolved.read_text(encoding="utf-8"))
                weight_map = idx_data.get("weight_map") if isinstance(idx_data, dict) else None
                if not isinstance(weight_map, dict) or not weight_map:
                    return False
                shards = {str(v) for v in weight_map.values() if isinstance(v, str) and v}
                if not shards:
                    return False
                for shard_rel in shards:
                    if "\\" in shard_rel or shard_rel.startswith("/"):
                        return False
                    rel_parts = PurePosixPath(shard_rel).parts
                    if not rel_parts or any(part in ("", ".", "..") for part in rel_parts):
                        return False
                    shard_path = base.joinpath(*rel_parts)
                    shard_resolved = shard_path.resolve(strict=True)
                    shard_resolved.relative_to(model_dir_resolved)
                    if not shard_resolved.is_file() or shard_resolved.stat().st_size <= 0:
                        return False
                has_weight_file = True
            except (OSError, ValueError, TypeError):
                return False

    return has_ready_marker or has_weight_file


class _ColabOneTaskStageProxy:
    """Forwards task cancellation/notes while scaling progress updates into a sub-range."""

    def __init__(
        self,
        task: Any,
        progress_range: tuple[float, float],
        *,
        stopping: threading.Event | None = None,
    ) -> None:
        self._task = task
        self._lo = max(0.0, min(1.0, float(progress_range[0])))
        self._hi = max(self._lo, min(1.0, float(progress_range[1])))
        self._stopping = stopping
        self.lock = getattr(task, "lock", None) or threading.RLock()
        self.cancel = getattr(task, "cancel", threading.Event())
        self._last_progress = self._lo
        self._last_message = ""

    @property
    def progress(self) -> float:
        return self._last_progress

    @progress.setter
    def progress(self, value: Any) -> None:
        self.update(progress=value)

    @property
    def message(self) -> str:
        return self._last_message

    @message.setter
    def message(self, value: Any) -> None:
        self.update(message=value)

    @property
    def cancel_hook(self) -> Any:
        return getattr(self._task, "cancel_hook", None)

    @cancel_hook.setter
    def cancel_hook(self, value: Any) -> None:
        if self._task is not None and hasattr(self._task, "cancel_hook"):
            self._task.cancel_hook = value

    def check(self) -> None:
        _task_check(self._task, self._stopping)

    def note(self, message: str, level: str = "info") -> None:
        _task_note(self._task, message, level=level)

    def update(
        self,
        progress: Any = None,
        message: Any = None,
        **_kwargs: Any,
    ) -> None:
        mapped: float | None = None
        msg_val = str(message) if isinstance(message, str) and message else None
        with self.lock:
            if isinstance(progress, (int, float)) and not isinstance(progress, bool):
                raw_p = float(progress)
                if math.isfinite(raw_p):
                    frac = max(0.0, min(1.0, raw_p))
                    cand = self._lo + (self._hi - self._lo) * frac
                    mapped = max(self._last_progress, cand)
                    self._last_progress = mapped
            elif msg_val:
                cand_msg: float | None = None
                if "虚拟环境" in msg_val:
                    cand_msg = self._lo + (self._hi - self._lo) * 0.20
                elif "源码" in msg_val:
                    cand_msg = self._lo + (self._hi - self._lo) * 0.40
                elif "安装" in msg_val:
                    cand_msg = self._lo + (self._hi - self._lo) * 0.55
                if cand_msg is not None:
                    mapped = max(self._last_progress, cand_msg)
                    self._last_progress = mapped
            if msg_val is not None:
                self._last_message = msg_val
        _task_update(
            self._task,
            progress=mapped,
            message=msg_val,
            monotonic_progress=True,
        )


class ColabOneLiveStudioManager:
    """Controller for remote Live Studio sessions, avatar packs, and WS auth tokens."""

    def __init__(
        self,
        root: Path | str,
        catalog_dir: Path | str,
        runtime: Any = None,
    ) -> None:
        self.root = Path(root)
        self.catalog_dir = Path(catalog_dir)
        self.runtime = runtime
        self.lock = threading.RLock()
        self._lifecycle_lock = threading.Lock()
        self._lifecycle_cond = threading.Condition(self._lifecycle_lock)
        self._lifecycle_busy = False
        self._stopping = False
        self._generation = 0
        self._active_switch_tasks: set[Any] = set()
        self._env_locks_guard = threading.Lock()
        self._env_locks: dict[str, threading.Lock] = {}

        _ensure_safe_dir(self.root, "Live Studio 根目录")
        self.avatars_dir = self.root / "live-avatars"
        _ensure_safe_dir(self.avatars_dir, "主播动作包目录")
        self.driving_dir = self.root / "live-driving-motions"
        _ensure_safe_dir(self.driving_dir, "驱动动作目录")

        catalog_path = self.catalog_dir / "live_studio.json"
        if not catalog_path.is_file():
            fallback_path = Path(__file__).resolve().parents[1] / "catalog" / "live_studio.json"
            if fallback_path.is_file():
                catalog_path = fallback_path
        raw_catalog = json.loads(catalog_path.read_text(encoding="utf-8"))
        self.catalog = ColabOneLiveCatalog.model_validate(raw_catalog)
        self.catalog_dict = self.catalog.model_dump()
        self.port = self.catalog.port
        self.ws_path = self.catalog.ws_path

        self._tokens: dict[str, dict[str, Any]] = {}
        self._companion_tickets: dict[str, dict[str, Any]] = {}
        self._companion_redeem_failures: list[float] = []
        self._companion_redeem_failures_by_ip: dict[str, list[float]] = {}
        self._runner: Any = None
        self._app: Any = None
        self._running = False
        self._state = "stopped"
        self._started_at: float | None = None
        self._active_config: dict[str, Any] | None = None
        self._active_session_cfg: ColabOneLiveSessionConfig | None = None
        self._vram_budget: dict[str, Any] | None = None
        self._error = ""
        self._last_gpu_info: dict[str, Any] | None = None
        self._last_loaded_vram_mib: int = 0
        self._last_synthetic: bool = False
        self._last_auto_provision: bool = False
        self._last_portrait_input_id: str = ""
        self._last_hf_token: str = ""

        # Optional test hooks for offline dependency auto-provisioning tests
        self._env_installer_fn: Callable[..., Path | None] | None = None
        self._weight_downloader_fn: Callable[..., Path | None] | None = None
        self._auto_provision_asr: bool = True

    def mark_starting(self) -> None:
        with self.lock:
            if not self._running:
                self._state = "starting"
                self._error = ""

    def mark_stopped_clean(self) -> None:
        with self.lock:
            if not self._running:
                self._state = "stopped"
                self._error = ""

    def mark_start_error(self, error: str) -> None:
        with self.lock:
            if not self._running:
                self._state = "error"
                self._error = str(error or "")

    # ------------------------------------------------------------------
    # Catalog summary
    # ------------------------------------------------------------------

    def catalog_summary(self) -> dict[str, Any]:
        return json.loads(json.dumps(self.catalog_dict))

    def _default_action_meta(self, action_id: str) -> dict[str, Any]:
        for item in self.catalog_dict.get("default_actions", []):
            if item.get("id") == action_id:
                return dict(item)
        kind = "dance" if action_id.startswith("dance") else (
            "loop" if action_id.endswith("_loop") or action_id.startswith("talk_") else "oneshot"
        )
        return {
            "id": action_id,
            "label": action_id,
            "kind": kind,
            "default_duration_seconds": 4.0,
            "trigger_keywords": [],
        }

    # ------------------------------------------------------------------
    # Avatar Action Packs CRUD & clip upload
    # ------------------------------------------------------------------

    def _avatar_dir(self, avatar_id: str) -> Path:
        aid = validate_colabone_live_id(avatar_id, "主播包 ID")
        target = self.avatars_dir / aid
        _ensure_contained_path(self.avatars_dir, target, "主播包目录")
        return target

    def _manifest_path(self, avatar_dir: Path) -> Path:
        path = avatar_dir / "manifest.json"
        _ensure_contained_path(avatar_dir, path, "主播清单文件")
        return path

    def _assets_meta_path(self, avatar_dir: Path) -> Path:
        path = avatar_dir / "assets_meta.json"
        _ensure_contained_path(avatar_dir, path, "主播素材校验文件")
        return path

    def _read_assets_meta(self, avatar_dir: Path) -> dict[str, dict[str, Any]]:
        path = self._assets_meta_path(avatar_dir)
        if not path.is_file():
            return {}
        try:
            data = json.loads(path.read_text(encoding="utf-8"))
            return data if isinstance(data, dict) else {}
        except (OSError, ValueError):
            return {}

    def _write_assets_meta(self, avatar_dir: Path, meta: dict[str, dict[str, Any]]) -> None:
        path = self._assets_meta_path(avatar_dir)
        atomic_json(path, meta)

    def _load_manifest_at(self, avatar_dir: Path) -> dict[str, Any]:
        manifest_path = self._manifest_path(avatar_dir)
        if not manifest_path.is_file():
            raise KeyError(avatar_dir.name)
        raw = json.loads(manifest_path.read_text(encoding="utf-8"))
        manifest = ColabOneLiveAvatarManifest.model_validate(raw).model_dump()
        if manifest["id"] != avatar_dir.name:
            raise ValueError("主播包清单 ID 与目录名不一致")
        return manifest

    def list_avatars(self) -> list[dict[str, Any]]:
        with self.lock:
            result: list[dict[str, Any]] = []
            if not self.avatars_dir.is_dir():
                return result
            for entry in sorted(self.avatars_dir.iterdir(), key=lambda p: p.name):
                if entry.name.startswith(".") or entry.is_symlink() or not entry.is_dir():
                    continue
                try:
                    validate_colabone_live_id(entry.name, "主播包 ID")
                    manifest = self._load_manifest_at(entry)
                    manifest["assets_meta"] = self._read_assets_meta(entry)
                    result.append(manifest)
                except (OSError, ValueError, KeyError):
                    continue
            result.sort(key=lambda item: float(item.get("updated_at") or 0.0), reverse=True)
            return result

    def get_avatar(self, avatar_id: str) -> dict[str, Any]:
        with self.lock:
            avatar_dir = self._avatar_dir(avatar_id)
            if not avatar_dir.is_dir():
                raise KeyError(avatar_id)
            manifest = self._load_manifest_at(avatar_dir)
            manifest["assets_meta"] = self._read_assets_meta(avatar_dir)
            return manifest

    def _resolve_portrait_input_path(self, portrait_input_id: str) -> Path | None:
        if not portrait_input_id or self.runtime is None:
            return None
        comfy = getattr(self.runtime, "comfy", None)
        store = getattr(comfy, "store", None) if comfy is not None else None
        inputs_dir = getattr(store, "inputs_dir", None) if store is not None else None
        if inputs_dir is None:
            return None
        base = Path(inputs_dir)
        for ext in sorted(COLABONE_LIVE_IMAGE_EXTENSIONS):
            candidate = base / f"{portrait_input_id}.{ext}"
            if candidate.is_symlink():
                raise ValueError("人像输入文件不能是符号链接")
            if candidate.is_file():
                _ensure_contained_path(base, candidate, "人像输入文件")
                return candidate
        return None

    def _resolve_portrait_input(self, portrait_input_id: str) -> bytes | None:
        candidate = self._resolve_portrait_input_path(portrait_input_id)
        if candidate is None:
            return None
        return candidate.read_bytes()

    def _resolve_session_portrait_path(
        self, avatar_data: dict[str, Any] | None, portrait_input_id: str
    ) -> Path | None:
        if avatar_data and avatar_data.get("portrait_path"):
            av_dir = self._avatar_dir(avatar_data["id"])
            candidate = av_dir / str(avatar_data["portrait_path"])
            if candidate.is_symlink():
                raise ValueError("主播人像文件不能是符号链接")
            _ensure_contained_path(av_dir, candidate, "主播人像文件")
            if candidate.is_file():
                return candidate
        if portrait_input_id:
            return self._resolve_portrait_input_path(portrait_input_id)
        return None

    def save_avatar(
        self, req: dict[str, Any] | ColabOneLiveAvatarSaveRequest
    ) -> dict[str, Any]:
        parsed = (
            req
            if isinstance(req, ColabOneLiveAvatarSaveRequest)
            else ColabOneLiveAvatarSaveRequest.model_validate(req)
        )
        # Validate and normalize portrait inputs BEFORE touching .trash or creating dirs.
        png_bytes: bytes | None = None
        if parsed.portrait_b64:
            if parsed.portrait_filename:
                _safe_extension(
                    parsed.portrait_filename,
                    COLABONE_LIVE_IMAGE_EXTENSIONS,
                    "人像图片",
                )
            portrait_raw = _decode_base64_payload(
                parsed.portrait_b64, COLABONE_LIVE_MAX_PORTRAIT_BYTES, "人像图片"
            )
            png_bytes = _normalize_portrait_png(portrait_raw)
        elif parsed.portrait_input_id:
            portrait_raw = self._resolve_portrait_input(parsed.portrait_input_id)
            if portrait_raw is None:
                raise ValueError("人像输入文件不存在或已失效，请重新上传")
            png_bytes = _normalize_portrait_png(portrait_raw)

        now = time.time()
        with self.lock:
            avatar_id = parsed.id or f"avatar_{secrets.token_hex(6)}"
            avatar_dir = self._avatar_dir(avatar_id)
            manifest_path = self._manifest_path(avatar_dir)
            existing: dict[str, Any] | None = None
            if avatar_dir.is_dir() and manifest_path.is_file():
                existing = self._load_manifest_at(avatar_dir)

            portrait_rel = "portrait.png" if png_bytes is not None else (
                existing["portrait_path"] if existing else ""
            )
            actions = (
                [a.model_dump() for a in parsed.actions]
                if parsed.actions is not None
                else (list(existing.get("actions", [])) if existing else [])
            )
            products = (
                [p.model_dump() for p in parsed.products]
                if parsed.products is not None
                else (list(existing.get("products", [])) if existing else [])
            )

            manifest = ColabOneLiveAvatarManifest.model_validate(
                {
                    "id": avatar_id,
                    "name": parsed.name,
                    "persona_prompt": parsed.persona_prompt,
                    "portrait_path": portrait_rel,
                    "default_tts_engine": parsed.default_tts_engine,
                    "default_voice_preset": parsed.default_voice_preset,
                    "actions": actions,
                    "products": products,
                    "created_at": existing["created_at"] if existing else now,
                    "updated_at": now,
                }
            ).model_dump()

            # All validation passed: safe to remove any prior local tombstone and write.
            trash_dir = self.avatars_dir / ".trash"
            tombstone = trash_dir / avatar_id
            if not trash_dir.is_symlink() and (tombstone.is_symlink() or tombstone.exists()):
                if tombstone.is_symlink() or tombstone.is_file():
                    tombstone.unlink()
                elif tombstone.is_dir():
                    _safe_rmtree(tombstone, trash_dir)

            _ensure_safe_dir(avatar_dir, "主播包目录")
            _ensure_safe_dir(avatar_dir / "clips", "动作片段目录")
            _ensure_safe_dir(avatar_dir / "bgm", "背景音乐目录")

            assets_meta = self._read_assets_meta(avatar_dir)
            if png_bytes is not None:
                portrait_file = avatar_dir / "portrait.png"
                _ensure_contained_path(avatar_dir, portrait_file, "人像文件")
                digest = _atomic_write_bytes(portrait_file, png_bytes)
                assets_meta["portrait.png"] = {
                    "sha256": digest,
                    "size": len(png_bytes),
                    "updated_at": now,
                }

            atomic_json(manifest_path, manifest)
            self._write_assets_meta(avatar_dir, assets_meta)
            self._refresh_active_app_avatar_locked(avatar_id, manifest)
            manifest["assets_meta"] = assets_meta
            return manifest

    def _refresh_active_app_avatar_locked(
        self, avatar_id: str, manifest: dict[str, Any]
    ) -> None:
        if (
            self._app is not None
            and self._active_session_cfg is not None
            and self._active_session_cfg.avatar_id == avatar_id
        ):
            app_state = getattr(self._app, "state", None)
            if app_state is not None:
                app_state.avatar = dict(manifest)

    def delete_avatar(self, avatar_id: str) -> None:
        with self.lock:
            avatar_dir = self._avatar_dir(avatar_id)
            if not avatar_dir.is_dir():
                raise KeyError(avatar_id)
            if (
                self._running
                and self._active_session_cfg is not None
                and self._active_session_cfg.avatar_id == avatar_dir.name
            ):
                raise ValueError("该主播包正在直播会话中使用，请先停止直播或切换主播后再删除")
            trash_dir = self.avatars_dir / ".trash"
            _ensure_safe_dir(trash_dir, "主播包回收目录")
            tombstone = trash_dir / avatar_dir.name
            _ensure_contained_path(trash_dir, tombstone, "主播包回收目标")
            if tombstone.is_symlink() or tombstone.is_file():
                tombstone.unlink()
            elif tombstone.is_dir():
                _safe_rmtree(tombstone, trash_dir)
            os.replace(avatar_dir, tombstone)

    def upload_action_clip(
        self,
        avatar_id: str,
        req: dict[str, Any] | ColabOneLiveClipUploadRequest,
    ) -> dict[str, Any]:
        parsed = (
            req
            if isinstance(req, ColabOneLiveClipUploadRequest)
            else ColabOneLiveClipUploadRequest.model_validate(req)
        )
        clip_ext = _safe_extension(parsed.filename, COLABONE_LIVE_CLIP_EXTENSIONS, "动作视频")
        clip_bytes = _decode_base64_payload(
            parsed.data, COLABONE_LIVE_MAX_CLIP_BYTES, "动作视频"
        )
        bgm_ext = ""
        bgm_bytes: bytes | None = None
        if parsed.bgm_data:
            bgm_ext = _safe_extension(
                parsed.bgm_filename or f"{parsed.action_id}.mp3",
                COLABONE_LIVE_BGM_EXTENSIONS,
                "背景音乐",
            )
            bgm_bytes = _decode_base64_payload(
                parsed.bgm_data, COLABONE_LIVE_MAX_BGM_BYTES, "背景音乐"
            )

        now = time.time()
        with self.lock:
            avatar_dir = self._avatar_dir(avatar_id)
            if not avatar_dir.is_dir():
                raise KeyError(avatar_id)
            manifest = self._load_manifest_at(avatar_dir)
            assets_meta = self._read_assets_meta(avatar_dir)

            prev_action = next(
                (
                    a
                    for a in manifest.get("actions", [])
                    if isinstance(a, dict) and a.get("id") == parsed.action_id
                ),
                None,
            )

            clip_rel = f"clips/{parsed.action_id}.{clip_ext}"
            clip_target = avatar_dir / clip_rel
            _ensure_contained_path(avatar_dir, clip_target, "动作视频文件")

            bgm_rel = ""
            bgm_sha = ""
            bgm_target: Path | None = None
            if bgm_bytes is not None:
                bgm_rel = f"bgm/{parsed.action_id}.{bgm_ext}"
                bgm_target = avatar_dir / bgm_rel
                _ensure_contained_path(avatar_dir, bgm_target, "背景音乐文件")
            elif prev_action is not None:
                prev_bgm_rel = str(prev_action.get("bgm_path") or "").strip()
                if prev_bgm_rel:
                    prev_bgm_path = avatar_dir / prev_bgm_rel
                    if not prev_bgm_path.is_symlink() and prev_bgm_path.is_file():
                        _ensure_contained_path(avatar_dir, prev_bgm_path, "背景音乐文件")
                        bgm_rel = prev_bgm_rel
                        bgm_meta = assets_meta.get(prev_bgm_rel)
                        if isinstance(bgm_meta, dict):
                            bgm_sha = str(bgm_meta.get("sha256") or "")

            # Validate action spec & full manifest BEFORE writing or unlinking any files on disk.
            default_meta = self._default_action_meta(parsed.action_id)
            action_spec = ColabOneLiveActionSpec.model_validate(
                {
                    "id": parsed.action_id,
                    "label": parsed.label or default_meta.get("label") or parsed.action_id,
                    "kind": parsed.kind,
                    "clip_path": clip_rel,
                    "bgm_path": bgm_rel,
                    "duration_seconds": parsed.duration_seconds,
                    "trigger_keywords": parsed.trigger_keywords
                    or list(default_meta.get("trigger_keywords") or []),
                }
            ).model_dump()

            updated_actions = [
                a for a in manifest.get("actions", []) if a.get("id") != parsed.action_id
            ]
            updated_actions.append(action_spec)
            candidate_manifest = dict(manifest)
            candidate_manifest["actions"] = updated_actions
            candidate_manifest["updated_at"] = now
            validated = ColabOneLiveAvatarManifest.model_validate(
                candidate_manifest
            ).model_dump()

            # All schema/path validations succeeded: write media, commit manifest, then prune stale files.
            _ensure_safe_dir(avatar_dir / "clips", "动作片段目录")
            clip_sha = _atomic_write_bytes(clip_target, clip_bytes)
            assets_meta[clip_rel] = {
                "sha256": clip_sha,
                "size": len(clip_bytes),
                "updated_at": now,
            }

            if bgm_bytes is not None and bgm_target is not None:
                _ensure_safe_dir(avatar_dir / "bgm", "背景音乐目录")
                bgm_sha = _atomic_write_bytes(bgm_target, bgm_bytes)
                assets_meta[bgm_rel] = {
                    "sha256": bgm_sha,
                    "size": len(bgm_bytes),
                    "updated_at": now,
                }

            for old_ext in COLABONE_LIVE_CLIP_EXTENSIONS:
                if old_ext == clip_ext:
                    continue
                stale_rel = f"clips/{parsed.action_id}.{old_ext}"
                assets_meta.pop(stale_rel, None)
            if bgm_bytes is not None:
                for old_ext in COLABONE_LIVE_BGM_EXTENSIONS:
                    if old_ext == bgm_ext:
                        continue
                    stale_bgm_rel = f"bgm/{parsed.action_id}.{old_ext}"
                    assets_meta.pop(stale_bgm_rel, None)

            atomic_json(self._manifest_path(avatar_dir), validated)
            self._write_assets_meta(avatar_dir, assets_meta)
            self._refresh_active_app_avatar_locked(avatar_id, validated)

            for old_ext in COLABONE_LIVE_CLIP_EXTENSIONS:
                if old_ext == clip_ext:
                    continue
                stale_path = avatar_dir / f"clips/{parsed.action_id}.{old_ext}"
                if stale_path.is_file() and not stale_path.is_symlink():
                    try:
                        stale_path.unlink()
                    except OSError:
                        pass
            if bgm_bytes is not None:
                for old_ext in COLABONE_LIVE_BGM_EXTENSIONS:
                    if old_ext == bgm_ext:
                        continue
                    stale_bgm_path = avatar_dir / f"bgm/{parsed.action_id}.{old_ext}"
                    if stale_bgm_path.is_file() and not stale_bgm_path.is_symlink():
                        try:
                            stale_bgm_path.unlink()
                        except OSError:
                            pass

            return {
                "avatar_id": avatar_id,
                "action": action_spec,
                "sha256": clip_sha,
                "size": len(clip_bytes),
                "bgm_sha256": bgm_sha,
            }

    # ------------------------------------------------------------------
    # Driving motion uploads (for PersonaLive dance)
    # ------------------------------------------------------------------

    def upload_avatar_clip(
        self,
        avatar_id: str,
        payload: dict[str, Any] | ColabOneLiveClipUploadRequest,
    ) -> dict[str, Any]:
        """Alias for upload_action_clip matching runtime/route naming."""
        return self.upload_action_clip(avatar_id, payload)

    def _driving_item_dir(self, motion_id: str) -> Path:
        mid = validate_colabone_live_id(motion_id, "驱动动作 ID")
        target = self.driving_dir / mid
        _ensure_contained_path(self.driving_dir, target, "驱动动作目录")
        return target

    def list_driving_motions(self) -> list[dict[str, Any]]:
        with self.lock:
            items: list[dict[str, Any]] = []
            if not self.driving_dir.is_dir():
                return items
            for entry in sorted(self.driving_dir.iterdir(), key=lambda p: p.name):
                if entry.name.startswith(".") or entry.is_symlink() or not entry.is_dir():
                    continue
                meta_path = entry / "meta.json"
                if meta_path.is_symlink() or not meta_path.is_file():
                    continue
                try:
                    validate_colabone_live_id(entry.name, "驱动动作 ID")
                    meta = json.loads(meta_path.read_text(encoding="utf-8"))
                    if isinstance(meta, dict) and meta.get("id") == entry.name:
                        items.append(meta)
                except (OSError, ValueError):
                    continue
            items.sort(key=lambda item: float(item.get("updated_at") or 0.0), reverse=True)
            return items

    def upload_driving_motion(
        self, req: dict[str, Any] | ColabOneLiveDrivingUploadRequest
    ) -> dict[str, Any]:
        parsed = (
            req
            if isinstance(req, ColabOneLiveDrivingUploadRequest)
            else ColabOneLiveDrivingUploadRequest.model_validate(req)
        )
        clip_ext = _safe_extension(parsed.filename, COLABONE_LIVE_CLIP_EXTENSIONS, "驱动视频")
        clip_bytes = _decode_base64_payload(
            parsed.data, COLABONE_LIVE_MAX_CLIP_BYTES, "驱动视频"
        )
        bgm_ext = ""
        bgm_bytes: bytes | None = None
        if parsed.bgm_data:
            bgm_ext = _safe_extension(
                parsed.bgm_filename or "bgm.mp3",
                COLABONE_LIVE_BGM_EXTENSIONS,
                "驱动背景音乐",
            )
            bgm_bytes = _decode_base64_payload(
                parsed.bgm_data, COLABONE_LIVE_MAX_BGM_BYTES, "驱动背景音乐"
            )

        now = time.time()
        with self.lock:
            motion_id = parsed.id or f"drive_{secrets.token_hex(6)}"
            item_dir = self._driving_item_dir(motion_id)

            # Clear prior tombstone ONLY AFTER all input validation succeeded.
            trash_dir = self.driving_dir / ".trash"
            tombstone = trash_dir / motion_id
            if not trash_dir.is_symlink() and (tombstone.is_symlink() or tombstone.exists()):
                if tombstone.is_symlink() or tombstone.is_file():
                    tombstone.unlink()
                elif tombstone.is_dir():
                    _safe_rmtree(tombstone, trash_dir)

            _ensure_safe_dir(item_dir, "驱动动作目录")

            meta_path = item_dir / "meta.json"
            _ensure_contained_path(item_dir, meta_path, "驱动动作元数据")
            prev_meta: dict[str, Any] = {}
            existing_created_at = now
            if meta_path.is_file() and not meta_path.is_symlink():
                try:
                    loaded_meta = json.loads(meta_path.read_text(encoding="utf-8"))
                    if isinstance(loaded_meta, dict):
                        prev_meta = loaded_meta
                        if prev_meta.get("created_at"):
                            existing_created_at = float(prev_meta["created_at"])
                except (OSError, ValueError, TypeError):
                    pass

            clip_rel = f"clip.{clip_ext}"
            clip_target = item_dir / clip_rel
            _ensure_contained_path(item_dir, clip_target, "驱动视频文件")
            clip_sha = _atomic_write_bytes(clip_target, clip_bytes)
            for old_ext in COLABONE_LIVE_CLIP_EXTENSIONS:
                if old_ext == clip_ext:
                    continue
                stale_clip = item_dir / f"clip.{old_ext}"
                if stale_clip.is_file() and not stale_clip.is_symlink():
                    try:
                        stale_clip.unlink()
                    except OSError:
                        pass

            bgm_rel = ""
            bgm_sha = ""
            if bgm_bytes is not None:
                bgm_rel = f"bgm.{bgm_ext}"
                bgm_target = item_dir / bgm_rel
                _ensure_contained_path(item_dir, bgm_target, "驱动背景音乐文件")
                bgm_sha = _atomic_write_bytes(bgm_target, bgm_bytes)
                for old_ext in COLABONE_LIVE_BGM_EXTENSIONS:
                    if old_ext == bgm_ext:
                        continue
                    stale_bgm = item_dir / f"bgm.{old_ext}"
                    if stale_bgm.is_file() and not stale_bgm.is_symlink():
                        try:
                            stale_bgm.unlink()
                        except OSError:
                            pass
            else:
                prev_bgm_rel = str(prev_meta.get("bgm_path") or "").strip()
                if prev_bgm_rel:
                    prev_bgm_path = item_dir / prev_bgm_rel
                    if (
                        not prev_bgm_path.is_symlink()
                        and prev_bgm_path.is_file()
                    ):
                        _ensure_contained_path(item_dir, prev_bgm_path, "驱动背景音乐文件")
                        bgm_rel = prev_bgm_rel
                        bgm_sha = str(prev_meta.get("bgm_sha256") or "")

            meta = {
                "id": motion_id,
                "label": parsed.label,
                "clip_path": clip_rel,
                "sha256": clip_sha,
                "size": len(clip_bytes),
                "bgm_path": bgm_rel,
                "bgm_sha256": bgm_sha,
                "duration_seconds": parsed.duration_seconds,
                "trigger_keywords": list(parsed.trigger_keywords),
                "created_at": existing_created_at,
                "updated_at": now,
            }
            atomic_json(meta_path, meta)
            return meta

    def delete_driving_motion(self, motion_id: str) -> None:
        with self.lock:
            item_dir = self._driving_item_dir(motion_id)
            if not item_dir.is_dir():
                raise KeyError(motion_id)
            trash_dir = self.driving_dir / ".trash"
            _ensure_safe_dir(trash_dir, "驱动动作回收目录")
            tombstone = trash_dir / item_dir.name
            _ensure_contained_path(trash_dir, tombstone, "驱动动作回收目标")
            if tombstone.is_symlink() or tombstone.is_file():
                tombstone.unlink()
            elif tombstone.is_dir():
                _safe_rmtree(tombstone, trash_dir)
            os.replace(item_dir, tombstone)

    # ------------------------------------------------------------------
    # VRAM & disk preflight
    # ------------------------------------------------------------------

    def check_vram(
        self,
        config: dict[str, Any] | ColabOneLiveSessionConfig,
        gpu_info: dict[str, Any] | None = None,
        loaded_model_vram_mib: int = 0,
    ) -> dict[str, Any]:
        info = gpu_info
        if info is None and self.runtime is not None and callable(
            getattr(self.runtime, "gpu_info", None)
        ):
            info = self.runtime.gpu_info()
        gpu_total_mib = int((info or {}).get("total_mib", 0) or 0)
        return evaluate_colabone_live_vram_budget(
            config,
            gpu_total_mib=gpu_total_mib,
            local_llm_vram_mib=loaded_model_vram_mib,
            catalog=self.catalog_dict,
        )

    def _free_disk_mib(self) -> int:
        usage = shutil.disk_usage(self.root)
        return int(usage.free // (1024 * 1024))

    def _catalog_item_disk_mib(self, section: str, item_id: str) -> int:
        for entry in self.catalog_dict.get(section, []):
            if isinstance(entry, dict) and entry.get("id") == item_id:
                return max(0, int(entry.get("min_disk_mib", 0) or 0))
        return 0

    def _env_lock_for(self, engine_id: str) -> threading.Lock:
        with self._env_locks_guard:
            lock = self._env_locks.get(engine_id)
            if lock is None:
                lock = threading.Lock()
                self._env_locks[engine_id] = lock
            return lock

    def _is_component_cached(self, comp_id: str) -> bool:
        cache_dir = self.root / "cache" / "live-studio" / comp_id
        if cache_dir.is_symlink() or not cache_dir.is_dir():
            return False
        downloading_marker = cache_dir / ".downloading"
        if downloading_marker.is_symlink() or downloading_marker.exists():
            return False
        ready_marker = cache_dir / ".colabone_ready"
        has_ready_marker = ready_marker.is_file() and not ready_marker.is_symlink()
        has_verified_weight = False
        try:
            for dirpath, _dirnames, filenames in os.walk(cache_dir, followlinks=False):
                base = Path(dirpath)
                for fname in filenames:
                    lower = fname.lower()
                    if any(lower.endswith(sfx) for sfx in _INCOMPLETE_CACHE_SUFFIXES):
                        return False
                    fp = base / fname
                    if fp.suffix.lower() not in _CACHE_WEIGHT_SUFFIXES:
                        continue
                    if fp.is_symlink() or not fp.is_file():
                        return False
                    try:
                        if fp.stat().st_size <= 0:
                            return False
                        has_verified_weight = True
                    except OSError:
                        return False
        except OSError:
            return False
        return has_ready_marker or has_verified_weight

    def _required_disk_mib(
        self, cfg: ColabOneLiveSessionConfig, *, synthetic: bool = False
    ) -> int:
        if synthetic:
            return _MIN_FREE_DISK_HEADROOM_MIB
        components: list[tuple[str, str]] = [
            ("asr_vad", "silero_vad"),
            ("asr_vad", "sensevoice_small"),
            ("streaming_tts", cfg.tts_engine),
            ("visual_engines", cfg.visual_engine),
        ]
        if cfg.personalive_enabled:
            components.append(("visual_engines", "personalive_dance"))
        total = 0
        for section, comp_id in components:
            cached = self._is_component_cached(comp_id)
            if not cached and section == "streaming_tts":
                cached = (
                    self._resolve_component_model_dir(
                        section, comp_id, require_live_compatible=True
                    )
                    is not None
                )
            if not cached:
                total += self._catalog_item_disk_mib(section, comp_id)
        return total + _MIN_FREE_DISK_HEADROOM_MIB

    def _required_provision_disk_mib(
        self,
        cfg: ColabOneLiveSessionConfig,
        *,
        synthetic: bool = False,
        include_best_effort_asr: bool = True,
    ) -> int:
        """Disk budget strictly for components that will actually be downloaded/installed."""
        if synthetic:
            return _MIN_FREE_DISK_HEADROOM_MIB
        total = 0
        if (
            self._resolve_component_model_dir(
                "streaming_tts", cfg.tts_engine, require_live_compatible=True
            )
            is None
        ):
            total += self._catalog_item_disk_mib("streaming_tts", cfg.tts_engine)
        if (
            include_best_effort_asr
            and self._auto_provision_asr
            and self._resolve_component_model_dir("asr_vad", "sensevoice_small") is None
        ):
            total += self._catalog_item_disk_mib("asr_vad", "sensevoice_small")
        return total + _MIN_FREE_DISK_HEADROOM_MIB

    # ------------------------------------------------------------------
    # Ephemeral WebSocket session tokens & mobile companion pairing
    # ------------------------------------------------------------------

    @staticmethod
    def _parse_safe_canvas_origin(canvas_base_url: str) -> tuple[str, str] | None:
        """Validate canvas_base_url and return (http_scheme, host_port) or None."""
        raw = str(canvas_base_url or "").strip().rstrip("/")
        if not raw or any(ch.isspace() or ord(ch) < 32 or ord(ch) == 127 for ch in raw):
            return None
        parsed = urlparse(raw)
        scheme = (parsed.scheme or "").lower()
        if scheme not in ("http", "https"):
            return None
        if parsed.username is not None or parsed.password is not None:
            return None
        host = (parsed.hostname or "").strip()
        if not host:
            return None
        try:
            port = parsed.port
        except ValueError:
            return None
        host_fmt = f"[{host}]" if ":" in host and not host.startswith("[") else host
        default_port = 443 if scheme == "https" else 80
        netloc = (
            f"{host_fmt}:{port}"
            if port is not None and port != default_port
            else host_fmt
        )
        return scheme, netloc

    def _prune_expired_tokens_locked(self, now: float) -> None:
        expired = [
            tok
            for tok, info in self._tokens.items()
            if float(info.get("expires_at", 0.0) if isinstance(info, dict) else info) <= now
        ]
        for tok in expired:
            self._tokens.pop(tok, None)

    def _prune_expired_tickets_locked(self, now: float) -> None:
        expired = [
            code
            for code, info in self._companion_tickets.items()
            if float(info.get("expires_at", 0.0)) <= now
        ]
        for code in expired:
            self._companion_tickets.pop(code, None)
        cutoff = now - 60.0
        self._companion_redeem_failures = [
            ts for ts in self._companion_redeem_failures if ts > cutoff
        ]
        stale_ips: list[str] = []
        for ip_key, stamps in self._companion_redeem_failures_by_ip.items():
            kept = [ts for ts in stamps if ts > cutoff]
            if kept:
                self._companion_redeem_failures_by_ip[ip_key] = kept
            else:
                stale_ips.append(ip_key)
        for ip_key in stale_ips:
            self._companion_redeem_failures_by_ip.pop(ip_key, None)

    def issue_session_token(
        self,
        canvas_base_url: str = "",
        *,
        ttl_seconds: float = _DEFAULT_TOKEN_TTL_S,
        allow_loopback_fallback: bool = False,
        role: str = "desktop",
        device_label: str = "",
    ) -> dict[str, Any]:
        now = time.time()
        ttl = max(5.0, min(300.0, float(ttl_seconds)))
        token = secrets.token_urlsafe(32)
        expires_at = now + ttl
        clean_role = "companion" if str(role).strip().lower() == "companion" else "desktop"
        clean_label = re.sub(r"[\r\n\t\x00-\x1f\x7f]+", " ", str(device_label or "")).strip()[:64]

        with self.lock:
            self._prune_expired_tokens_locked(now)
            self._tokens[token] = {
                "expires_at": expires_at,
                "role": clean_role,
                "device_label": clean_label,
            }

        origin_parts = self._parse_safe_canvas_origin(canvas_base_url)
        canvas_ready = False
        ws_url = ""
        if origin_parts is not None:
            http_scheme, safe_netloc = origin_parts
            ws_scheme = "wss" if http_scheme == "https" else "ws"
            ws_url = f"{ws_scheme}://{safe_netloc}{self.ws_path}"
            canvas_ready = True
        elif allow_loopback_fallback:
            ws_url = f"ws://127.0.0.1:{self.port}{self.ws_path}"

        return {
            "token": token,
            "expires_at": expires_at,
            "ws_path": self.ws_path,
            "ws_url": ws_url,
            "canvas_ready": canvas_ready,
            "role": clean_role,
        }

    def verify_session_token_info(
        self, token: str, *, consume: bool = True
    ) -> dict[str, Any] | None:
        if not isinstance(token, str) or not token:
            return None
        token_bytes = token.encode("utf-8")
        now = time.time()
        with self.lock:
            self._prune_expired_tokens_locked(now)
            matched_key: str | None = None
            matched_info: dict[str, Any] | None = None
            for candidate, info in list(self._tokens.items()):
                exp = float(info.get("expires_at", 0.0) if isinstance(info, dict) else info)
                if hmac.compare_digest(candidate.encode("utf-8"), token_bytes) and exp > now:
                    matched_key = candidate
                    matched_info = (
                        dict(info)
                        if isinstance(info, dict)
                        else {"expires_at": exp, "role": "desktop", "device_label": ""}
                    )
            if matched_key is None or matched_info is None:
                return None
            if consume:
                self._tokens.pop(matched_key, None)
            return matched_info

    def verify_session_token(self, token: str, *, consume: bool = True) -> bool:
        return self.verify_session_token_info(token, consume=consume) is not None

    def issue_companion_ticket(
        self,
        canvas_base_url: str = "",
        *,
        ttl_seconds: float = 600.0,
        allow_loopback_fallback: bool = False,
    ) -> dict[str, Any]:
        """Issue a single-use pairing code & HTTPS QR URL for mobile companion input."""
        now = time.time()
        ttl = max(30.0, min(1800.0, float(ttl_seconds)))
        code = secrets.token_urlsafe(24)
        expires_at = now + ttl

        with self.lock:
            self._prune_expired_tickets_locked(now)
            # Single active pairing ticket at a time so refreshing QR invalidates prior codes.
            self._companion_tickets.clear()
            self._companion_tickets[code] = {
                "created_at": now,
                "expires_at": expires_at,
            }

        origin_parts = self._parse_safe_canvas_origin(canvas_base_url)
        canvas_ready = False
        companion_url = ""
        public_origin = ""
        if origin_parts is not None:
            http_scheme, safe_netloc = origin_parts
            public_origin = f"{http_scheme}://{safe_netloc}"
            companion_url = (
                f"{public_origin}{COLABONE_LIVE_COMPANION_PATH}#code={code}"
            )
            canvas_ready = True
        elif allow_loopback_fallback:
            public_origin = f"http://127.0.0.1:{self.port}"
            companion_url = (
                f"{public_origin}{COLABONE_LIVE_COMPANION_PATH}#code={code}"
            )

        return {
            "code": code,
            "expires_at": expires_at,
            "ttl_seconds": ttl,
            "companion_path": COLABONE_LIVE_COMPANION_PATH,
            "companion_url": companion_url,
            "public_origin": public_origin,
            "canvas_ready": canvas_ready,
            "companion": self.companion_status(),
        }

    def redeem_companion_ticket(
        self, code: str, *, device_label: str = "", client_ip: str = ""
    ) -> dict[str, Any] | None:
        """Atomically redeem a one-time pairing code for a scoped companion WS token."""
        raw_code = str(code or "").strip() if isinstance(code, str) else ""
        raw_code_bytes = raw_code.encode("utf-8")
        ip_key = str(client_ip or "").strip()[:64] or "unknown"
        safe_label = (
            re.sub(r"[\r\n\t\x00-\x1f\x7f]+", " ", str(device_label or "")).strip()[:64]
            if isinstance(device_label, str)
            else ""
        ) or "远端移动设备"
        now = time.time()
        with self.lock:
            self._prune_expired_tickets_locked(now)
            if self._stopping:
                return None
            ip_failures = self._companion_redeem_failures_by_ip.get(ip_key, [])
            if len(ip_failures) >= 15:
                raise RuntimeError("rate_limited")
            matched_key: str | None = None
            if raw_code:
                for candidate, info in list(self._companion_tickets.items()):
                    if (
                        hmac.compare_digest(candidate.encode("utf-8"), raw_code_bytes)
                        and float(info.get("expires_at", 0.0)) > now
                    ):
                        matched_key = candidate
            if matched_key is None:
                # Partition failures strictly per client IP with bounded LRU (512 IPs)
                # so attacker traffic from other IPs cannot DoS the owner's pairing.
                existing_stamps = self._companion_redeem_failures_by_ip.pop(ip_key, [])
                if len(self._companion_redeem_failures_by_ip) >= 512:
                    oldest_ip = next(iter(self._companion_redeem_failures_by_ip))
                    self._companion_redeem_failures_by_ip.pop(oldest_ip, None)
                existing_stamps.append(now)
                self._companion_redeem_failures_by_ip[ip_key] = existing_stamps
                return None
            # Single-use: consume ticket AND mint companion WS token atomically under self.lock
            # so revoke_companion() / stop() can never race between consumption and token insert.
            self._companion_tickets.pop(matched_key, None)
            tok = self.issue_session_token(
                ttl_seconds=60.0,
                role="companion",
                device_label=safe_label,
            )
        return {
            "token": tok["token"],
            "expires_at": tok["expires_at"],
            "ws_path": self.ws_path,
            "role": "companion",
            "device_label": safe_label,
        }

    def companion_status(self) -> dict[str, Any]:
        with self.lock:
            app = self._app
        default = {
            "connected": False,
            "device_label": "",
            "mic_active": False,
            "camera_mode": "off",
            "camera_fps": 0.0,
            "motion_energy": 0.0,
            "vision_frame_ready": False,
            "connected_at": None,
        }
        if app is None:
            return default
        app_state = getattr(app, "state", None)
        info = getattr(app_state, "companion_info", None)
        info_lock = getattr(app_state, "companion_info_lock", None)
        if isinstance(info, dict):
            if info_lock is not None:
                with info_lock:
                    snap = dict(info)
            else:
                snap = dict(info)
            return {
                "connected": bool(snap.get("connected", False)),
                "device_label": str(snap.get("device_label") or ""),
                "mic_active": bool(snap.get("mic_active", False)),
                "camera_mode": str(snap.get("camera_mode") or "off"),
                "camera_fps": round(float(snap.get("camera_fps") or 0.0), 1),
                "motion_energy": round(float(snap.get("motion_energy") or 0.0), 3),
                "vision_frame_ready": bool(snap.get("vision_frame_ready", False)),
                "connected_at": snap.get("connected_at"),
            }
        return default

    def revoke_companion(self) -> dict[str, Any]:
        """Invalidate all pending companion pairing codes/tokens and kick active companion."""
        with self.lock:
            self._companion_tickets.clear()
            for tok, info in list(self._tokens.items()):
                if isinstance(info, dict) and info.get("role") == "companion":
                    self._tokens.pop(tok, None)
            app = self._app
        if app is not None:
            app_state = getattr(app, "state", None)
            kick_fn = getattr(app_state, "kick_companion_sync", None)
            if callable(kick_fn):
                try:
                    kick_fn("revoked_by_desktop")
                except Exception:
                    pass
        return self.companion_status()

    # ------------------------------------------------------------------
    # Server lifecycle (start / stop / status)
    # ------------------------------------------------------------------

    def _stopping_event(self) -> threading.Event | None:
        ev = getattr(self.runtime, "stopping", None) if self.runtime is not None else None
        return ev if isinstance(ev, threading.Event) else None

    def _catalog_entry(self, section: str, comp_id: str) -> dict[str, Any] | None:
        for entry in self.catalog_dict.get(section, []):
            if isinstance(entry, dict) and entry.get("id") == comp_id:
                return entry
        return None

    def _hf_cache_dir(self) -> Path:
        comfy = getattr(self.runtime, "comfy", None) if self.runtime is not None else None
        tts_mgr = getattr(comfy, "tts", None) if comfy is not None else None
        engines_dir = getattr(tts_mgr, "engines_dir", None) if tts_mgr is not None else None
        if engines_dir is not None:
            return Path(engines_dir) / "_hf_cache"
        return self.root / "tts-envs" / "_hf_cache"

    def _resolve_engine_venv_python(self, engine_id: str) -> Path | None:
        eid = validate_colabone_live_id(engine_id, "引擎 ID")
        candidate_roots: list[Path] = []
        comfy = getattr(self.runtime, "comfy", None) if self.runtime is not None else None
        tts_mgr = getattr(comfy, "tts", None) if comfy is not None else None
        engines_dir = getattr(tts_mgr, "engines_dir", None) if tts_mgr is not None else None
        if engines_dir is not None:
            candidate_roots.append(Path(engines_dir) / eid / ".venv")
        candidate_roots.extend(
            [
                self.root / "tts-envs" / eid / ".venv",
                self.root / "live-envs" / eid / ".venv",
                self.root / "engines" / eid / ".venv",
            ]
        )

        seen: set[str] = set()
        live_venv_dir = self.root / "live-envs" / eid / ".venv"
        for venv_dir in candidate_roots:
            key = str(venv_dir)
            if key in seen:
                continue
            seen.add(key)
            if venv_dir.is_symlink() or not venv_dir.is_dir():
                continue
            if venv_dir == live_venv_dir:
                inst_marker = venv_dir.parent / ".installing"
                ready_marker = venv_dir.parent / ".ready.json"
                if inst_marker.is_symlink() or inst_marker.exists():
                    continue
                if ready_marker.is_symlink():
                    continue
                if ready_marker.is_file():
                    try:
                        parsed_ready = json.loads(ready_marker.read_text(encoding="utf-8"))
                        if not isinstance(parsed_ready, dict) or parsed_ready.get("engine_id") != eid:
                            continue
                    except (OSError, ValueError):
                        continue
            for rel in ("bin/python", "bin/python3", "Scripts/python.exe"):
                cand = venv_dir / rel
                # On POSIX/Colab, `python -m venv` creates bin/python as a symlink
                # pointing at the base interpreter; verify parent dir is real and
                # target resolves to an existing regular file.
                if cand.parent.is_symlink() or not cand.parent.is_dir():
                    continue
                try:
                    if cand.is_file():
                        return cand
                except OSError:
                    continue
        return None

    def _resolve_component_model_dir(
        self,
        section: str,
        comp_id: str,
        *,
        require_live_compatible: bool = False,
    ) -> Path | None:
        cid = validate_colabone_live_id(comp_id, "组件 ID")
        if self._is_component_cached(cid):
            return self.root / "cache" / "live-studio" / cid

        # Also check shared Hugging Face cache snapshots under tts-envs/_hf_cache.
        # For qwen3_tts in live mode, VoiceDesign/CustomVoice repos are required
        # because built-in live presets rely on text instructions without ref_audio.
        repo_candidates: list[tuple[str, str]] = []
        entry = self._catalog_entry(section, cid)
        if entry is not None:
            if section == "streaming_tts" and cid == "qwen3_tts" and require_live_compatible:
                key_pairs = (
                    ("aux_repo", "aux_revision"),
                    ("custom_voice_repo", "custom_voice_revision"),
                )
            else:
                key_pairs = (
                    ("aux_repo", "aux_revision"),
                    ("custom_voice_repo", "custom_voice_revision"),
                    ("repo", "revision"),
                )
            for rk, revk in key_pairs:
                r = str(entry.get(rk) or "").strip()
                rev = str(entry.get(revk) or "").strip()
                if r and not any(prev[0] == r for prev in repo_candidates):
                    repo_candidates.append((r, rev))

        hf_roots: list[Path] = []
        comfy = getattr(self.runtime, "comfy", None) if self.runtime is not None else None
        tts_mgr = getattr(comfy, "tts", None) if comfy is not None else None
        engines_dir = getattr(tts_mgr, "engines_dir", None) if tts_mgr is not None else None
        if engines_dir is not None:
            hf_roots.append(Path(engines_dir) / "_hf_cache")
        default_hf = self.root / "tts-envs" / "_hf_cache"
        if default_hf not in hf_roots:
            hf_roots.append(default_hf)

        for hf_root in hf_roots:
            if hf_root.is_symlink() or not hf_root.is_dir():
                continue
            for repo, rev in repo_candidates:
                model_dir = hf_root / ("models--" + repo.replace("/", "--"))
                if model_dir.is_symlink() or not model_dir.is_dir():
                    continue
                blobs_dir = model_dir / "blobs"
                if blobs_dir.is_symlink():
                    continue
                if blobs_dir.is_dir():
                    try:
                        if any(
                            b.name.endswith(".incomplete") for b in blobs_dir.iterdir()
                        ):
                            continue
                    except OSError:
                        continue
                snaps = model_dir / "snapshots"
                if snaps.is_symlink() or not snaps.is_dir():
                    continue
                try:
                    model_dir_resolved = model_dir.resolve(strict=True)
                except OSError:
                    continue
                if rev and "/" not in rev and "\\" not in rev and ".." not in rev:
                    for cand_rev in (rev, rev.lower()) if rev != rev.lower() else (rev,):
                        pinned = snaps / cand_rev
                        if _snapshot_dir_has_verified_weights(pinned, model_dir_resolved):
                            return pinned
                    # If rev is a pinned 40-char hex commit SHA, never silently fall back to
                    # an unrelated snapshot commit.
                    if re.fullmatch(r"[0-9a-fA-F]{40}", rev):
                        continue
                    ref_file = model_dir / "refs" / rev
                    if ref_file.is_file() and not ref_file.is_symlink():
                        try:
                            ref_sha = ref_file.read_text(encoding="utf-8").strip()
                            if (
                                ref_sha
                                and "/" not in ref_sha
                                and "\\" not in ref_sha
                                and ".." not in ref_sha
                            ):
                                ref_snap = snaps / ref_sha
                                if _snapshot_dir_has_verified_weights(
                                    ref_snap, model_dir_resolved
                                ):
                                    return ref_snap
                        except OSError:
                            pass
                try:
                    for snap in sorted(
                        snaps.iterdir(), key=lambda p: p.name, reverse=True
                    ):
                        if _snapshot_dir_has_verified_weights(snap, model_dir_resolved):
                            return snap
                except OSError:
                    continue
        return None

    def _select_tts_download_target(self, tts_engine: str) -> tuple[str, str, str]:
        """Return (repo_id, revision, display_name) for a streaming TTS engine."""
        eid = validate_colabone_live_id(tts_engine, "流式 TTS 引擎 ID")
        entry = self._catalog_entry("streaming_tts", eid) or {}
        name = str(entry.get("name") or eid)
        if eid == "qwen3_tts":
            for rk, revk in (
                ("aux_repo", "aux_revision"),
                ("custom_voice_repo", "custom_voice_revision"),
                ("repo", "revision"),
            ):
                repo = str(entry.get(rk) or "").strip()
                if repo:
                    return repo, str(entry.get(revk) or "").strip(), name
        repo = str(entry.get("repo") or "").strip()
        rev = str(entry.get("revision") or "").strip()
        if not repo:
            raise RuntimeError(f"流式 TTS 引擎 {eid} 缺少模型仓库配置，无法自动下载")
        return repo, rev, name

    def _run_provision_command(
        self,
        cmd: Sequence[str],
        task: Any,
        *,
        env: dict[str, str],
        timeout: float = 1200.0,
        secret_to_scrub: str = "",
        stage_label: str = "",
    ) -> None:
        stopping = self._stopping_event()
        _task_check(task, stopping)
        proc = subprocess.Popen(
            list(cmd),
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            encoding="utf-8",
            errors="replace",
            env=env,
            start_new_session=True,
        )
        out_q: queue.Queue[str | None] = queue.Queue()
        tail_lines: list[str] = []

        def _drain() -> None:
            stream = proc.stdout
            try:
                if stream is not None:
                    for line in stream:
                        out_q.put(line)
            except Exception:
                pass
            finally:
                out_q.put(None)

        reader = threading.Thread(target=_drain, daemon=True)
        reader.start()

        term_lock = threading.Lock()

        def _safe_term() -> None:
            with term_lock:
                _terminate_live_process_group(proc, timeout=3.0)

        prev_hook = None
        installed_hook = None
        if task is not None and hasattr(task, "lock") and hasattr(task, "cancel_hook"):
            with task.lock:
                prev_hook = task.cancel_hook

                def _chained_cancel() -> None:
                    _safe_term()
                    if callable(prev_hook):
                        try:
                            prev_hook()
                        except Exception:
                            pass

                installed_hook = _chained_cancel
                task.cancel_hook = installed_hook
        deadline = time.monotonic() + max(10.0, float(timeout))
        eof = False
        exited_at: float | None = None
        try:
            while True:
                _task_check(task, stopping)
                now = time.monotonic()
                if now > deadline:
                    _safe_term()
                    raise RuntimeError(f"{stage_label or '依赖安装'}超时（>{int(timeout)}s）")
                if proc.poll() is not None:
                    if exited_at is None:
                        exited_at = now
                    elif eof or (now - exited_at) >= 2.0:
                        break
                try:
                    line = out_q.get(timeout=0.1)
                except queue.Empty:
                    if proc.poll() is not None and (
                        eof or (exited_at is not None and (time.monotonic() - exited_at) >= 2.0)
                    ):
                        break
                    continue
                if line is None:
                    eof = True
                    if proc.poll() is not None:
                        break
                    continue
                cleaned = scrub_colabone_live_secrets(line.strip(), secret_to_scrub)
                if cleaned:
                    tail_lines.append(cleaned)
                    if len(tail_lines) > 24:
                        tail_lines = tail_lines[-24:]
            rc = proc.wait(timeout=10.0)
            _safe_term()
            _task_check(task, stopping)
            if rc != 0:
                detail = _summarize_tail_lines(tail_lines[-8:], max_chars=300)
                suffix = f"：{detail}" if detail else ""
                raise RuntimeError(
                    scrub_colabone_live_secrets(
                        f"{stage_label or '依赖安装'}失败（退出码 {rc}）{suffix}",
                        secret_to_scrub,
                    )
                )
        except BaseException:
            _safe_term()
            raise
        finally:
            if (
                installed_hook is not None
                and task is not None
                and hasattr(task, "lock")
                and hasattr(task, "cancel_hook")
            ):
                with task.lock:
                    if task.cancel_hook is installed_hook:
                        task.cancel_hook = prev_hook
            reader.join(timeout=1.0)
            if proc.stdout is not None:
                try:
                    proc.stdout.close()
                except Exception:
                    pass

    def _ensure_engine_venv(
        self,
        task: Any,
        section: str,
        engine_id: str,
        *,
        hf_token: str = "",
        progress_range: tuple[float, float] = (0.08, 0.42),
    ) -> Path:
        eid = validate_colabone_live_id(engine_id, "引擎 ID")
        entry = self._catalog_entry(section, eid) or {}
        engine_name = str(entry.get("name") or eid)
        stopping = self._stopping_event()
        _task_check(task, stopping)

        if self._env_installer_fn is not None:
            existing = self._resolve_engine_venv_python(eid)
            if existing is not None:
                _task_note(task, f"复用已就绪的流式 TTS 运行环境：{engine_name}")
                _task_update(task, progress=progress_range[1], monotonic_progress=True)
                return existing
            _task_update(
                task,
                progress=progress_range[0],
                message=f"正在安装流式语音引擎运行环境：{engine_name}...",
                monotonic_progress=True,
            )
            _task_note(task, f"自动安装缺失的流式语音引擎环境：{engine_name}")
            installed = self._env_installer_fn(
                task=task,
                section=section,
                engine_id=eid,
                entry=entry,
                hf_token=hf_token,
            )
            _task_check(task, stopping)
            resolved = Path(installed) if installed is not None else self._resolve_engine_venv_python(eid)
            if resolved is None:
                raise RuntimeError(
                    f"流式 TTS 引擎 {eid} 的独立运行环境安装完成后未找到 Python 可执行文件"
                )
            _task_update(task, progress=progress_range[1], monotonic_progress=True)
            return resolved

        # Prefer sharing ComfyUI TTS Manager's canonical engine spec & venv so
        # `.ready.json` fingerprints match 100% and both panels reuse one venv.
        comfy = getattr(self.runtime, "comfy", None) if self.runtime is not None else None
        tts_mgr = getattr(comfy, "tts", None) if comfy is not None else None
        if section == "streaming_tts" and tts_mgr is not None and callable(
            getattr(tts_mgr, "ensure_engine_env", None)
        ):
            canonical_engines = getattr(tts_mgr, "engines_by_id", None)
            canonical_spec = (
                canonical_engines.get(eid)
                if isinstance(canonical_engines, dict)
                else None
            )
            if isinstance(canonical_spec, dict):
                _task_update(
                    task,
                    progress=progress_range[0],
                    message=f"正在检查并准备流式语音运行环境：{engine_name}...",
                    monotonic_progress=True,
                )
                proxy = _ColabOneTaskStageProxy(
                    task, progress_range, stopping=stopping
                )
                tts_mgr.ensure_engine_env(proxy, canonical_spec, hf_token=hf_token)
                _task_check(task, stopping)
                venv_py = self._resolve_engine_venv_python(eid)
                if venv_py is not None:
                    _task_update(task, progress=progress_range[1], monotonic_progress=True)
                    return venv_py
                raise RuntimeError(
                    f"流式 TTS 引擎 {eid} 的独立运行环境安装后不可用"
                )

        # Standalone fallback (when runtime.comfy.tts is not attached)
        env_dir = self.root / "live-envs" / eid
        _ensure_safe_dir(env_dir, "Live Studio 引擎环境目录")
        venv_dir = env_dir / ".venv"
        if venv_dir.is_symlink():
            raise ValueError("Live Studio 引擎虚拟环境目录不能是符号链接")
        live_ready_marker = env_dir / ".ready.json"
        live_installing_marker = env_dir / ".installing"
        if live_ready_marker.is_symlink() or live_installing_marker.is_symlink():
            raise ValueError("Live Studio 引擎状态标记不能是符号链接")
        pkgs = [
            str(p).strip()
            for p in (entry.get("pip_packages") or [])
            if str(p).strip()
            and re.split(r"[<>=!~;\[\s]", str(p).strip(), maxsplit=1)[0]
            .lower()
            .replace("_", "-")
            not in _LIVE_TORCH_SKIP_PACKAGES
        ]
        if (
            venv_dir.is_dir()
            and live_installing_marker.exists()
            and not live_ready_marker.is_file()
        ):
            shutil.rmtree(venv_dir, ignore_errors=True)

        existing = self._resolve_engine_venv_python(eid)
        if existing is not None:
            is_live_env_venv = str(existing).startswith(str(venv_dir))
            live_ready_matches = True
            if is_live_env_venv and live_ready_marker.is_file():
                try:
                    ready_data = json.loads(live_ready_marker.read_text(encoding="utf-8"))
                    if (
                        not isinstance(ready_data, dict)
                        or ready_data.get("engine_id") != eid
                        or list(ready_data.get("packages") or []) != pkgs
                    ):
                        live_ready_matches = False
                except (OSError, ValueError):
                    live_ready_matches = False
            if (
                not is_live_env_venv
                or (live_ready_matches and (live_ready_marker.is_file() or not live_installing_marker.exists()))
            ):
                _task_note(task, f"复用已就绪的流式 TTS 运行环境：{engine_name}")
                _task_update(task, progress=progress_range[1], monotonic_progress=True)
                return existing

        sub_env = _sanitized_live_provision_env()
        _task_update(
            task,
            progress=progress_range[0],
            message=f"正在创建流式语音隔离虚拟环境：{engine_name}...",
            monotonic_progress=True,
        )
        _task_note(task, f"正在创建隔离虚拟环境：{engine_name}")
        if venv_dir.exists():
            shutil.rmtree(venv_dir, ignore_errors=True)
        try:
            live_ready_marker.unlink(missing_ok=True)
            live_installing_marker.write_text(str(time.time()), encoding="utf-8")
            self._run_provision_command(
                [
                    sys.executable,
                    "-m",
                    "venv",
                    "--without-pip",
                    "--system-site-packages",
                    str(venv_dir),
                ],
                task,
                env=sub_env,
                timeout=300.0,
                secret_to_scrub=hf_token,
                stage_label=f"{engine_name} 虚拟环境创建",
            )
            try:
                from server.tts_engines import _ensure_parent_venv_pth

                _ensure_parent_venv_pth(venv_dir)
            except Exception:
                pass
            venv_py = None
            for rel in ("bin/python", "bin/python3", "Scripts/python.exe"):
                cand = venv_dir / rel
                if not cand.parent.is_symlink() and cand.parent.is_dir() and cand.is_file():
                    venv_py = cand
                    break
            if venv_py is None:
                raise RuntimeError(f"流式 TTS 引擎 {eid} 虚拟环境创建不完整")
            if pkgs:
                mid_pct = progress_range[0] + (progress_range[1] - progress_range[0]) * 0.45
                _task_update(
                    task,
                    progress=mid_pct,
                    message=f"正在安装流式语音引擎依赖：{engine_name}（共 {len(pkgs)} 个包）...",
                    monotonic_progress=True,
                )
                _task_note(task, f"正在安装 {engine_name} 依赖包：{', '.join(pkgs)}")
                try:
                    self._run_provision_command(
                        [str(venv_py), "-m", "pip", "--version"],
                        task,
                        env=sub_env,
                        timeout=60.0,
                        secret_to_scrub=hf_token,
                        stage_label=f"{engine_name} pip 检查",
                    )
                except RuntimeError as pip_exc:
                    if _is_cancelled_error(pip_exc):
                        raise
                    self._run_provision_command(
                        [str(venv_py), "-m", "ensurepip", "--upgrade"],
                        task,
                        env=sub_env,
                        timeout=180.0,
                        secret_to_scrub=hf_token,
                        stage_label=f"{engine_name} ensurepip",
                    )
                constraint_args: list[str] = []
                constraints_path = env_dir / ".torch-constraints.txt"
                if not constraints_path.is_symlink():
                    try:
                        from server.tts_engines import _collect_torch_constraints

                        pins = _collect_torch_constraints()
                        if pins:
                            constraints_path.write_text("\n".join(pins) + "\n", encoding="utf-8")
                            constraint_args = ["-c", str(constraints_path)]
                    except Exception:
                        pass
                self._run_provision_command(
                    [
                        str(venv_py),
                        "-m",
                        "pip",
                        "install",
                        "--no-input",
                        "--disable-pip-version-check",
                        *constraint_args,
                        *pkgs,
                    ],
                    task,
                    env=sub_env,
                    timeout=1200.0,
                    secret_to_scrub=hf_token,
                    stage_label=f"{engine_name} 依赖安装",
                )
            atomic_json(
                live_ready_marker,
                {"engine_id": eid, "ready_at": time.time(), "packages": pkgs},
            )
            live_installing_marker.unlink(missing_ok=True)
        except BaseException:
            shutil.rmtree(venv_dir, ignore_errors=True)
            raise

        _task_update(task, progress=progress_range[1], monotonic_progress=True)
        _task_note(task, f"流式语音运行环境安装完成：{engine_name}")
        return venv_py

    def _download_hf_repo_with_progress(
        self,
        task: Any,
        *,
        repo_id: str,
        revision: str = "",
        label: str,
        comp_id: str,
        cache_dir: Path | None = None,
        local_dir: Path | None = None,
        venv_python: Path | None = None,
        hf_token: str = "",
        progress_range: tuple[float, float] = (0.42, 0.80),
        timeout: float = 3600.0,
    ) -> Path:
        stopping = self._stopping_event()
        _task_check(task, stopping)
        lo, hi = progress_range
        token_clean = str(hf_token or "").strip()
        rev_clean = str(revision or "").strip()

        if cache_dir is not None:
            _ensure_safe_dir(cache_dir, f"{label} 缓存目录")
        if local_dir is not None:
            _ensure_safe_dir(local_dir, f"{label} 模型目录")

        msg = f"正在下载模型权重：{label}（{repo_id}）..."
        _task_update(task, progress=lo, message=msg, monotonic_progress=True)
        _task_note(task, msg)

        if self._weight_downloader_fn is not None:
            est_total = _LIVE_FALLBACK_WEIGHT_BYTES.get(comp_id, 2 * 1024**3)
            transfer = ColabOneTransfer(est_total, threads=4, progress_range=progress_range)
            _task_set_transfer(task, transfer)
            try:
                out = self._weight_downloader_fn(
                    task=task,
                    transfer=transfer,
                    repo_id=repo_id,
                    revision=rev_clean,
                    label=label,
                    comp_id=comp_id,
                    cache_dir=cache_dir,
                    local_dir=local_dir,
                    venv_python=venv_python,
                    hf_token=token_clean,
                    progress_range=progress_range,
                )
                _task_check(task, stopping)
                _task_update(task, progress=hi, monotonic_progress=True)
                return Path(out) if out is not None else (local_dir or cache_dir or self.root)
            finally:
                _task_set_transfer(task, None)

        dl_python = Path(venv_python) if venv_python is not None else Path(sys.executable)
        payload_json = json.dumps(
            {
                "repo_id": repo_id,
                "revision": rev_clean,
                "cache_dir": str(cache_dir) if cache_dir is not None else "",
                "local_dir": str(local_dir) if local_dir is not None else "",
            },
            ensure_ascii=False,
        )

        watch_dir = (
            local_dir
            if local_dir is not None
            else (
                cache_dir / ("models--" + repo_id.replace("/", "--")) / "blobs"
                if cache_dir is not None
                else None
            )
        )

        def _run_download_attempt(*, force_disable_hf_transfer: bool) -> tuple[Path | None, bool, str]:
            dl_env = _sanitized_live_provision_env()
            if force_disable_hf_transfer:
                dl_env["HF_HUB_ENABLE_HF_TRANSFER"] = "0"
            elif (
                _has_hf_transfer_incompatible_env(dl_env)
                and "HF_HUB_ENABLE_HF_TRANSFER" not in os.environ
            ):
                dl_env["HF_HUB_ENABLE_HF_TRANSFER"] = "0"
            if token_clean:
                dl_env["HF_TOKEN"] = token_clean

            est_total = _LIVE_FALLBACK_WEIGHT_BYTES.get(comp_id, 2 * 1024**3)
            transfer = ColabOneTransfer(est_total, threads=4, progress_range=progress_range)
            _task_set_transfer(task, transfer)

            initial_disk_bytes = _measure_dir_bytes(watch_dir) if watch_dir is not None else 0
            last_reported_bytes = 0
            last_disk_poll = time.monotonic()
            hf_transfer_active = False
            result_path_str = ""
            stderr_tail: list[str] = []

            proc = subprocess.Popen(
                [str(dl_python), "-u", "-c", _COLABONE_LIVE_HF_DOWNLOAD_SCRIPT, payload_json],
                stdin=subprocess.DEVNULL,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                encoding="utf-8",
                errors="replace",
                env=dl_env,
                start_new_session=True,
            )
            out_q: queue.Queue[tuple[str, str | None]] = queue.Queue()

            def _drain_stream(stream: Any, tag: str) -> None:
                try:
                    if stream is not None:
                        for raw in stream:
                            out_q.put((tag, raw))
                except Exception:
                    pass
                finally:
                    out_q.put((tag, None))

            t_out = threading.Thread(
                target=_drain_stream, args=(proc.stdout, "out"), daemon=True
            )
            t_err = threading.Thread(
                target=_drain_stream, args=(proc.stderr, "err"), daemon=True
            )
            t_out.start()
            t_err.start()

            term_lock = threading.Lock()

            def _safe_dl_term() -> None:
                with term_lock:
                    _terminate_live_process_group(proc, timeout=3.0)

            prev_hook = None
            installed_hook = None
            if task is not None and hasattr(task, "lock") and hasattr(task, "cancel_hook"):
                with task.lock:
                    prev_hook = task.cancel_hook

                    def _chained_dl_cancel() -> None:
                        _safe_dl_term()
                        if callable(prev_hook):
                            try:
                                prev_hook()
                            except Exception:
                                pass

                    installed_hook = _chained_dl_cancel
                    task.cancel_hook = installed_hook

            deadline = time.monotonic() + max(30.0, float(timeout))
            out_eof = False
            err_eof = False
            exited_at: float | None = None
            received_exact_plan = False

            def _apply_progress_bytes(
                downloaded: int,
                total_bytes: int | None = None,
                *,
                filename: str | None = None,
                file_index: int | None = None,
                file_count: int | None = None,
            ) -> None:
                nonlocal last_reported_bytes
                with transfer.lock:
                    if total_bytes is not None and total_bytes > 0:
                        if received_exact_plan:
                            transfer.total = max(transfer.total, total_bytes)
                        else:
                            # Never lower transfer.total below est_total from a single small file
                            # when HfApi().repo_info() failed to provide an exact repo plan.
                            transfer.total = max(transfer.total, total_bytes)
                    eff_total = max(1, int(transfer.total or 1))
                    clamped_done = max(last_reported_bytes, min(int(downloaded), eff_total))
                    delta = clamped_done - last_reported_bytes
                    last_reported_bytes = clamped_done
                    transfer.downloaded = clamped_done
                    transfer.phase = "downloading"
                    transfer.active = 1
                    if filename:
                        transfer.file = filename
                    if file_index is not None and file_index >= 0:
                        transfer.index = file_index
                    if file_count is not None and file_count > 0:
                        transfer.count = file_count
                if delta > 0:
                    transfer.wire(delta)
                frac = min(1.0, last_reported_bytes / max(1, int(transfer.total or 1)))
                _task_update(
                    task,
                    progress=lo + (hi - lo) * frac,
                    monotonic_progress=True,
                )

            try:
                while True:
                    _task_check(task, stopping)
                    now = time.monotonic()
                    if now > deadline:
                        _safe_dl_term()
                        raise RuntimeError(f"下载 {label} 模型权重超时（>{int(timeout)}s）")
                    if proc.poll() is not None:
                        if exited_at is None:
                            exited_at = now
                        elif (out_eof and err_eof) or (now - exited_at) >= 2.0:
                            break

                    if watch_dir is not None and (now - last_disk_poll) >= 0.8:
                        last_disk_poll = now
                        disk_delta = max(0, _measure_dir_bytes(watch_dir) - initial_disk_bytes)
                        if disk_delta > last_reported_bytes:
                            _apply_progress_bytes(disk_delta)

                    try:
                        tag, raw_line = out_q.get(timeout=0.1)
                    except queue.Empty:
                        if proc.poll() is not None and (
                            (out_eof and err_eof)
                            or (exited_at is not None and (time.monotonic() - exited_at) >= 2.0)
                        ):
                            break
                        continue

                    if raw_line is None:
                        if tag == "out":
                            out_eof = True
                        else:
                            err_eof = True
                        if proc.poll() is not None and out_eof and err_eof:
                            break
                        continue

                    if tag == "err":
                        cleaned_err = scrub_colabone_live_secrets(raw_line.strip(), token_clean)
                        if cleaned_err == _HF_TRANSFER_ACTIVE_MARKER:
                            hf_transfer_active = True
                            continue
                        if cleaned_err:
                            stderr_tail.append(cleaned_err)
                            if len(stderr_tail) > 24:
                                stderr_tail = stderr_tail[-24:]
                        continue

                    line_str = raw_line.strip()
                    if not line_str.startswith(_HF_DL_PROGRESS_PREFIX):
                        continue
                    try:
                        evt = json.loads(line_str[len(_HF_DL_PROGRESS_PREFIX) :])
                    except ValueError:
                        continue
                    if not isinstance(evt, dict):
                        continue
                    kind = evt.get("event")
                    if kind == "plan":
                        p_total = int(evt.get("total_bytes") or 0)
                        p_files = int(evt.get("file_count") or 0)
                        with transfer.lock:
                            if p_total > 0:
                                received_exact_plan = True
                                transfer.total = max(last_reported_bytes, p_total)
                            if p_files > 0:
                                transfer.count = p_files
                    elif kind == "progress":
                        p_done = int(evt.get("downloaded_bytes") or 0)
                        p_total = int(evt.get("total_bytes") or 0)
                        p_idx = int(evt.get("file_index") or 0)
                        p_cnt = int(evt.get("file_count") or 0)
                        p_file = str(evt.get("filename") or "")
                        _apply_progress_bytes(
                            p_done,
                            p_total if p_total > 0 else None,
                            filename=p_file or None,
                            file_index=p_idx,
                            file_count=p_cnt if p_cnt > 0 else None,
                        )
                    elif kind == "done":
                        result_path_str = str(evt.get("path") or "").strip()

                rc = proc.wait(timeout=10.0)
                _safe_dl_term()
                _task_check(task, stopping)
                err_full = "\n".join(stderr_tail)
                err_summary = _summarize_tail_lines(stderr_tail[-8:], max_chars=320)
                if rc != 0:
                    return None, hf_transfer_active, err_summary or f"退出码 {rc}", err_full
                with transfer.lock:
                    transfer.downloaded = max(transfer.downloaded, transfer.total)
                    transfer.phase = "done"
                _task_update(task, progress=hi, monotonic_progress=True)
                return (
                    Path(result_path_str) if result_path_str else (local_dir or cache_dir),
                    hf_transfer_active,
                    "",
                    "",
                )
            except BaseException:
                _safe_dl_term()
                raise
            finally:
                _task_set_transfer(task, None)
                if (
                    installed_hook is not None
                    and task is not None
                    and hasattr(task, "lock")
                    and hasattr(task, "cancel_hook")
                ):
                    with task.lock:
                        if task.cancel_hook is installed_hook:
                            task.cancel_hook = prev_hook
                t_out.join(timeout=1.0)
                t_err.join(timeout=1.0)
                for s in (proc.stdout, proc.stderr):
                    if s is not None:
                        try:
                            s.close()
                        except Exception:
                            pass

        out_path, used_hft, err_text, err_full = _run_download_attempt(
            force_disable_hf_transfer=False
        )
        if out_path is not None:
            _task_note(task, f"模型权重下载完成：{label}（{repo_id}）")
            return out_path

        if used_hft and not _is_non_retryable_hf_download_error(err_full or err_text):
            _task_check(task, stopping)
            _task_note(
                task,
                f"{label} 加速下载失败，自动关闭 hf_transfer 并回退标准下载重试一次",
                level="warning",
            )
            out_path, _used2, err_text2, _err_full2 = _run_download_attempt(
                force_disable_hf_transfer=True
            )
            if out_path is not None:
                _task_note(task, f"模型权重下载完成：{label}（{repo_id}）")
                return out_path
            err_text = err_text2 or err_text

        raise RuntimeError(
            scrub_colabone_live_secrets(
                f"下载 {label} 模型权重（{repo_id}）失败：{err_text}",
                token_clean,
            )
        )

    def _ensure_tts_ready(
        self,
        task: Any,
        tts_engine: str,
        *,
        hf_token: str = "",
        venv_range: tuple[float, float] = (0.08, 0.42),
        weight_range: tuple[float, float] = (0.42, 0.80),
    ) -> tuple[Path, Path]:
        eid = validate_colabone_live_id(tts_engine, "流式 TTS 引擎 ID")
        with self._env_lock_for(eid):
            repo_id, revision, engine_name = self._select_tts_download_target(eid)
            venv_py = self._ensure_engine_venv(
                task,
                "streaming_tts",
                eid,
                hf_token=hf_token,
                progress_range=venv_range,
            )
            model_dir = self._resolve_component_model_dir(
                "streaming_tts", eid, require_live_compatible=True
            )
            if model_dir is not None:
                _task_note(task, f"复用已缓存的流式语音模型权重：{engine_name}")
                _task_update(task, progress=weight_range[1], monotonic_progress=True)
                return venv_py, model_dir

            self._download_hf_repo_with_progress(
                task,
                repo_id=repo_id,
                revision=revision,
                label=engine_name,
                comp_id=eid,
                cache_dir=self._hf_cache_dir(),
                venv_python=venv_py,
                hf_token=hf_token,
                progress_range=weight_range,
            )
            resolved = self._resolve_component_model_dir(
                "streaming_tts", eid, require_live_compatible=True
            )
            if resolved is not None:
                return venv_py, resolved
            raise RuntimeError(
                f"流式 TTS 引擎 {eid} 的模型权重下载完成后未通过完整性校验"
            )

    def ensure_session_dependencies(
        self,
        task: Any,
        config: dict[str, Any] | ColabOneLiveStartRequest,
        *,
        synthetic: bool = False,
        min_free_disk_mib: int | None = None,
    ) -> dict[str, Any]:
        """Auto-install missing streaming TTS venv/weights and best-effort ASR weights."""
        req = (
            config
            if isinstance(config, ColabOneLiveStartRequest)
            else ColabOneLiveStartRequest.model_validate(config)
        )
        if synthetic:
            return {"tts_engine": req.tts_engine, "synthetic": True}

        stopping = self._stopping_event()
        _task_check(task, stopping)
        session_cfg = req.to_session_config()
        required_disk_mib = (
            max(0, int(min_free_disk_mib))
            if min_free_disk_mib is not None
            else self._required_provision_disk_mib(
                session_cfg, synthetic=False, include_best_effort_asr=False
            )
        )
        free_disk_mib = self._free_disk_mib()
        if free_disk_mib < required_disk_mib:
            raise ValueError(
                f"磁盘空间不足（剩余 {free_disk_mib} MiB，至少需要 {required_disk_mib} MiB）"
            )

        venv_py, tts_model_dir = self._ensure_tts_ready(
            task,
            req.tts_engine,
            hf_token=req.hf_token,
            venv_range=(0.08, 0.42),
            weight_range=(0.42, 0.80),
        )

        # Best-effort SenseVoice-Small ASR weights (non-fatal if unavailable or low disk)
        asr_dir: Path | None = self._resolve_component_model_dir(
            "asr_vad", "sensevoice_small"
        )
        if asr_dir is None and self._auto_provision_asr:
            asr_entry = self._catalog_entry("asr_vad", "sensevoice_small") or {}
            asr_repo = str(asr_entry.get("repo") or "").strip()
            asr_rev = str(asr_entry.get("revision") or "").strip()
            asr_name = str(asr_entry.get("name") or "SenseVoice-Small")
            asr_need_mib = (
                self._catalog_item_disk_mib("asr_vad", "sensevoice_small")
                + _MIN_FREE_DISK_HEADROOM_MIB
            )
            if asr_repo and self._free_disk_mib() >= asr_need_mib:
                target_asr_dir = self.root / "cache" / "live-studio" / "sensevoice_small"
                with self._env_lock_for("sensevoice_small"):
                    asr_dl_marker = target_asr_dir / ".downloading"
                    try:
                        _ensure_safe_dir(target_asr_dir, f"{asr_name} 模型目录")
                        if asr_dl_marker.is_symlink():
                            raise ValueError("SenseVoice 下载标记不能是符号链接")
                        asr_dl_marker.write_text(str(time.time()), encoding="utf-8")
                        self._download_hf_repo_with_progress(
                            task,
                            repo_id=asr_repo,
                            revision=asr_rev,
                            label=f"{asr_name} 语音识别",
                            comp_id="sensevoice_small",
                            local_dir=target_asr_dir,
                            venv_python=venv_py,
                            hf_token=req.hf_token,
                            progress_range=(0.80, 0.88),
                            timeout=1800.0,
                        )
                        atomic_json(
                            target_asr_dir / ".colabone_ready",
                            {
                                "repo": asr_repo,
                                "revision": asr_rev,
                                "ready_at": time.time(),
                            },
                        )
                        asr_dl_marker.unlink(missing_ok=True)
                        asr_dir = target_asr_dir
                    except Exception as asr_exc:
                        if _is_cancelled_error(asr_exc):
                            raise
                        err_clean = scrub_colabone_live_secrets(
                            str(asr_exc) or type(asr_exc).__name__, req.hf_token
                        )
                        _task_note(
                            task,
                            f"SenseVoice 语音识别权重下载失败（已跳过，不影响开播）：{err_clean}",
                            level="warning",
                        )
            elif asr_repo:
                _task_note(
                    task,
                    f"剩余磁盘空间不足以缓存 {asr_name} 语音识别权重（已跳过，不影响开播）",
                    level="warning",
                )
        _task_update(task, progress=0.88, monotonic_progress=True)
        return {
            "tts_engine": req.tts_engine,
            "tts_venv_python": str(venv_py),
            "tts_model_dir": str(tts_model_dir),
            "asr_model_dir": str(asr_dir) if asr_dir is not None else "",
        }

    def _build_tts_engine(
        self,
        tts_engine: str,
        *,
        synthetic: bool,
        hf_token: str,
    ) -> Any:
        if synthetic:
            return create_colabone_streaming_tts_engine(tts_engine, mock=True)
        venv_py = self._resolve_engine_venv_python(tts_engine)
        model_dir = self._resolve_component_model_dir(
            "streaming_tts", tts_engine, require_live_compatible=True
        )
        if model_dir is None:
            model_dir = self._resolve_component_model_dir("streaming_tts", tts_engine)
        if venv_py is None:
            raise RuntimeError(
                f"流式 TTS 引擎 {tts_engine} 的独立运行环境尚未安装就绪，无法启动实时语音合成"
            )
        if model_dir is None:
            raise RuntimeError(
                f"流式 TTS 引擎 {tts_engine} 的模型权重尚未缓存就绪，无法启动实时语音合成"
            )
        return create_colabone_streaming_tts_engine(
            tts_engine,
            mock=False,
            venv_python=venv_py,
            model_dir=model_dir,
            hf_cache_dir=self._hf_cache_dir(),
            hf_token=hf_token,
            fallback_mock=False,
        )

    def _build_visual_engine(
        self,
        visual_engine: str,
        *,
        synthetic: bool,
        resolution: str,
        fps: int,
        portrait_path: Path | None,
        avatar_dir: Path | None,
    ) -> Any:
        kwargs: dict[str, Any] = {}
        if not synthetic:
            venv_py = self._resolve_engine_venv_python(visual_engine)
            model_dir = self._resolve_component_model_dir("visual_engines", visual_engine)
            if venv_py is not None:
                kwargs["venv_python"] = venv_py
            if model_dir is not None:
                kwargs["model_dir"] = model_dir
            if visual_engine == "flashhead_lite" and portrait_path is not None:
                kwargs["portrait_path"] = portrait_path
            elif visual_engine == "musetalk_bank" and avatar_dir is not None:
                kwargs["avatar_dir"] = avatar_dir
            elif visual_engine == "personalive_dance":
                if portrait_path is not None:
                    kwargs["portrait_path"] = portrait_path
                kwargs["cache_dir"] = self.root / "live-cache"
        return create_colabone_visual_engine(
            visual_engine,
            synthetic=synthetic,
            resolution=resolution,
            fps=fps,
            **kwargs,
        )

    def _build_video_encoder(
        self,
        *,
        width: int,
        height: int,
        fps: int,
        synthetic: bool,
        enable_rtmp: bool,
        rtmp_url: str,
    ) -> ColabOneLiveVideoEncoder:
        eff_rtmp = rtmp_url if enable_rtmp else ""
        if synthetic:
            return ColabOneLiveVideoEncoder(
                width=width,
                height=height,
                fps=fps,
                mode="jpeg",
                rtmp_url=eff_rtmp,
                enable_ffmpeg=False,
            )
        probed_mode = ColabOneLiveVideoEncoder.probe_encoder()
        if eff_rtmp:
            if probed_mode not in ("h264_nvenc", "libx264"):
                raise RuntimeError("启用 RTMP 推流需要可用的 ffmpeg 与 H.264 编码器")
            validate_colabone_live_rtmp_resolved_ips(eff_rtmp)
        return ColabOneLiveVideoEncoder(
            width=width,
            height=height,
            fps=fps,
            mode=probed_mode,
            rtmp_url=eff_rtmp,
            enable_ffmpeg=probed_mode in ("h264_nvenc", "libx264"),
        )

    def _detach_session_locked(self) -> tuple[Any, Any]:
        """Detach active runner/app and clear cached session secrets under self.lock."""
        runner = self._runner
        app = self._app
        self._runner = None
        self._app = None
        self._running = False
        self._state = "stopping" if (runner is not None or app is not None) else "stopped"
        self._started_at = None
        self._active_config = None
        self._active_session_cfg = None
        self._vram_budget = None
        self._error = ""
        self._tokens.clear()
        self._companion_tickets.clear()
        self._last_hf_token = ""
        self._last_portrait_input_id = ""
        self._last_gpu_info = None
        self._last_loaded_vram_mib = 0
        self._last_synthetic = False
        self._last_auto_provision = False
        return runner, app

    @staticmethod
    def _teardown_detached_session(runner: Any, app: Any) -> None:
        """Stop detached uvicorn runner and unload engines OUTSIDE all manager locks."""
        if runner is not None:
            try:
                runner.stop()
            except Exception:
                pass

        if app is not None:
            state = getattr(app, "state", None)
            seen_engines: list[Any] = []
            for attr in ("tts_engine", "visual_engine", "base_visual_engine"):
                eng = getattr(state, attr, None)
                if eng is not None and not any(eng is prev for prev in seen_engines):
                    seen_engines.append(eng)
            for eng in seen_engines:
                if callable(getattr(eng, "unload", None)):
                    try:
                        eng.unload()
                    except Exception:
                        pass
            seen_encoders: list[Any] = []
            for attr in ("encoder", "base_encoder"):
                enc = getattr(state, attr, None)
                if enc is not None and not any(enc is prev for prev in seen_encoders):
                    seen_encoders.append(enc)
            for enc in seen_encoders:
                if callable(getattr(enc, "close", None)):
                    try:
                        enc.close()
                    except Exception:
                        pass

    def _recreate_current_base_visual(self) -> Any:
        with self.lock:
            if self._stopping or not self._running or self._active_session_cfg is None:
                raise RuntimeError("实时直播会话已停止")
            cur_cfg = self._active_session_cfg
            synthetic = self._last_synthetic
            portrait_input_id = self._last_portrait_input_id
        avatar_data: dict[str, Any] | None = None
        avatar_dir: Path | None = None
        if cur_cfg.avatar_id:
            try:
                avatar_data = self.get_avatar(cur_cfg.avatar_id)
                avatar_dir = self._avatar_dir(cur_cfg.avatar_id)
            except KeyError:
                avatar_data = None
                avatar_dir = None
        portrait_path = self._resolve_session_portrait_path(avatar_data, portrait_input_id)
        return self._build_visual_engine(
            cur_cfg.visual_engine,
            synthetic=synthetic,
            resolution=cur_cfg.resolution,
            fps=cur_cfg.target_fps,
            portrait_path=portrait_path,
            avatar_dir=avatar_dir,
        )

    def _handle_ws_switch_engine(self, switch_req: dict[str, Any]) -> dict[str, Any]:
        # NEVER acquire self._lifecycle_lock here: this runs inside uvicorn's ThreadPoolExecutor,
        # and stop() joins the uvicorn thread. Use self.lock + _stopping + _generation instead.
        with self.lock:
            if self._stopping or not self._running or self._active_session_cfg is None or self._app is None:
                raise RuntimeError("实时直播会话正在停止或尚未启动")
            gen = self._generation
            cur_cfg = self._active_session_cfg
            app = self._app
            gpu_info = self._last_gpu_info
            loaded_vram_mib = self._last_loaded_vram_mib
            synthetic = self._last_synthetic
            portrait_input_id = self._last_portrait_input_id
            hf_token = self._last_hf_token

        updates: dict[str, Any] = {}
        for key in ("visual_engine", "personalive_enabled", "tts_engine", "avatar_id"):
            if key in switch_req:
                updates[key] = switch_req[key]
        candidate_cfg = cur_cfg.model_copy(update=updates)

        # Read live loaded LLM VRAM & GPU info from runtime when available.
        actual_loaded_vram_mib = int(loaded_vram_mib or 0)
        live_gpu_info = gpu_info
        if self.runtime is not None:
            if callable(getattr(self.runtime, "_loaded_model_vram_mib", None)):
                actual_loaded_vram_mib = int(
                    self.runtime._loaded_model_vram_mib() or 0
                )
            if callable(getattr(self.runtime, "gpu_info", None)):
                rt_gpu = self.runtime.gpu_info()
                if isinstance(rt_gpu, dict) and rt_gpu:
                    live_gpu_info = rt_gpu

        budget = self.check_vram(
            candidate_cfg,
            gpu_info=live_gpu_info,
            loaded_model_vram_mib=actual_loaded_vram_mib,
        )
        if not budget["ok"] or (
            actual_loaded_vram_mib > 0 and not budget.get("can_co_locate_llm", False)
        ):
            rec = "；".join(budget.get("recommendations") or [])
            if not budget["ok"]:
                msg = f"切换引擎显存预检未通过（需 {budget['total_required_mib']} MiB，可用 {budget['available_mib']} MiB）"
            else:
                msg = (
                    f"当前远端已加载本地 LLM（约 {actual_loaded_vram_mib} MiB），"
                    f"显存不足以与目标直播配置同驻（可用 {budget['available_mib']} MiB）"
                )
            if rec:
                msg = f"{msg}。建议：{rec}"
            raise ValueError(msg)

        new_avatar: dict[str, Any] | None = None
        avatar_dir: Path | None = None
        if candidate_cfg.avatar_id:
            new_avatar = self.get_avatar(candidate_cfg.avatar_id)
            avatar_dir = self._avatar_dir(candidate_cfg.avatar_id)
        portrait_path = self._resolve_session_portrait_path(
            new_avatar, portrait_input_id
        )

        # Construct lazy (unloaded) replacement objects WITHOUT unloading old engines yet.
        # live_server.py performs the atomic swap on the event loop first, then unloads old engines.
        result: dict[str, Any] = {
            "config": candidate_cfg,
            "vram_budget": budget,
            "visual_engine": None,
            "tts_engine": None,
            "encoder": None,
            "avatar": new_avatar,
            "old_visual": None,
            "old_tts": None,
        }

        new_visual: Any = None
        new_tts: Any = None
        new_encoder: Any = None
        try:
            old_visual = getattr(app.state, "visual_engine", None)
            if (
                candidate_cfg.visual_engine != cur_cfg.visual_engine
                or candidate_cfg.avatar_id != cur_cfg.avatar_id
            ):
                new_visual = self._build_visual_engine(
                    candidate_cfg.visual_engine,
                    synthetic=synthetic,
                    resolution=candidate_cfg.resolution,
                    fps=candidate_cfg.target_fps,
                    portrait_path=portrait_path,
                    avatar_dir=avatar_dir,
                )
                cur_encoder = getattr(app.state, "encoder", None)
                enc_mode = getattr(cur_encoder, "mode", "jpeg") or "jpeg"
                new_encoder = ColabOneLiveVideoEncoder(
                    width=new_visual.width,
                    height=new_visual.height,
                    fps=candidate_cfg.target_fps,
                    mode=enc_mode,
                    rtmp_url=(
                        candidate_cfg.rtmp_url if candidate_cfg.enable_rtmp else ""
                    ),
                    enable_ffmpeg=getattr(cur_encoder, "enable_ffmpeg", False),
                )
                result["visual_engine"] = new_visual
                result["encoder"] = new_encoder
                result["old_visual"] = old_visual

            old_tts = getattr(app.state, "tts_engine", None)
            if candidate_cfg.tts_engine != cur_cfg.tts_engine:
                with self.lock:
                    allow_auto_prov = bool(self._last_auto_provision)
                if not synthetic and allow_auto_prov:
                    comfy_ref = getattr(self.runtime, "comfy", None) if self.runtime is not None else None
                    tts_mgr_ref = getattr(comfy_ref, "tts", None) if comfy_ref is not None else None
                    engines_dir_ref = getattr(tts_mgr_ref, "engines_dir", None) if tts_mgr_ref is not None else None
                    venv_py_cand = self._resolve_engine_venv_python(candidate_cfg.tts_engine)
                    need_venv = venv_py_cand is None
                    if not need_venv:
                        if engines_dir_ref is not None:
                            tts_ready_file = Path(engines_dir_ref) / candidate_cfg.tts_engine / ".ready.json"
                            if tts_ready_file.is_symlink() or not tts_ready_file.is_file():
                                need_venv = True
                        else:
                            live_env_dir = self.root / "live-envs" / candidate_cfg.tts_engine
                            try:
                                in_live_env = venv_py_cand.resolve().is_relative_to(live_env_dir.resolve())
                            except OSError:
                                in_live_env = False
                            if in_live_env:
                                live_ready_file = live_env_dir / ".ready.json"
                                if live_ready_file.is_symlink() or not live_ready_file.is_file():
                                    need_venv = True
                    need_weights = (
                        self._resolve_component_model_dir(
                            "streaming_tts",
                            candidate_cfg.tts_engine,
                            require_live_compatible=True,
                        )
                        is None
                    )
                    if need_venv or need_weights:
                        required_disk_mib = (
                            self._catalog_item_disk_mib(
                                "streaming_tts", candidate_cfg.tts_engine
                            )
                            if need_weights
                            else 0
                        ) + _MIN_FREE_DISK_HEADROOM_MIB
                        free_disk_mib = self._free_disk_mib()
                        if free_disk_mib < required_disk_mib:
                            raise ValueError(
                                f"磁盘空间不足（剩余 {free_disk_mib} MiB，至少需要 {required_disk_mib} MiB）"
                            )

                        studio_ref = self

                        class _ColabOneSwitchTaskAdapter:
                            def __init__(self) -> None:
                                self.lock = threading.RLock()
                                self.cancel_hook: Callable[[], None] | None = None
                                self.transfer: Any = None

                            def check(self) -> None:
                                with studio_ref.lock:
                                    aborted = (
                                        studio_ref._stopping
                                        or not studio_ref._running
                                        or studio_ref._generation != gen
                                    )
                                if aborted:
                                    from server.runtime import ColabOneCancelledError

                                    raise ColabOneCancelledError(
                                        "实时直播会话正在停止，切换引擎已取消"
                                    )

                            def snapshot(self) -> dict[str, Any]:
                                return {}

                            def trigger_cancel(self) -> None:
                                with self.lock:
                                    hook = self.cancel_hook
                                if callable(hook):
                                    try:
                                        hook()
                                    except Exception:
                                        pass

                        switch_task = _ColabOneSwitchTaskAdapter()
                        with self.lock:
                            if self._stopping or not self._running or self._generation != gen:
                                raise RuntimeError("实时直播会话正在停止，切换引擎已取消")
                            self._active_switch_tasks.add(switch_task)
                        try:
                            self._ensure_tts_ready(
                                switch_task,
                                candidate_cfg.tts_engine,
                                hf_token=hf_token,
                            )
                        finally:
                            with self.lock:
                                self._active_switch_tasks.discard(switch_task)
                        with self.lock:
                            if self._stopping or not self._running or self._generation != gen:
                                raise RuntimeError("实时直播会话正在停止，切换引擎已取消")
                new_tts = self._build_tts_engine(
                    candidate_cfg.tts_engine,
                    synthetic=synthetic,
                    hf_token=hf_token,
                )
                result["tts_engine"] = new_tts
                result["old_tts"] = old_tts

            with self.lock:
                if self._stopping or not self._running or self._generation != gen:
                    raise RuntimeError("实时直播会话正在停止，切换引擎已取消")
                self._active_session_cfg = candidate_cfg
                self._vram_budget = budget
                if isinstance(live_gpu_info, dict):
                    self._last_gpu_info = dict(live_gpu_info)
                self._last_loaded_vram_mib = actual_loaded_vram_mib
                if isinstance(self._active_config, dict):
                    self._active_config["visual_engine"] = candidate_cfg.visual_engine
                    self._active_config["personalive_enabled"] = (
                        candidate_cfg.personalive_enabled
                    )
                    self._active_config["tts_engine"] = candidate_cfg.tts_engine
                    self._active_config["avatar_id"] = candidate_cfg.avatar_id
            return result
        except Exception:
            for eng in (new_visual, new_tts):
                if eng is not None and callable(getattr(eng, "unload", None)):
                    try:
                        eng.unload()
                    except Exception:
                        pass
            if isinstance(new_encoder, ColabOneLiveVideoEncoder):
                try:
                    new_encoder.close()
                except Exception:
                    pass
            raise

    def _handle_ws_trigger_driving(self, motion_id: str) -> dict[str, Any]:
        mid = validate_colabone_live_id(motion_id, "驱动动作 ID")
        with self.lock:
            if self._stopping or not self._running or self._active_session_cfg is None or self._app is None:
                raise RuntimeError("实时直播会话正在停止或尚未启动")
            gen = self._generation
            cur_cfg = self._active_session_cfg
            app = self._app
            portrait_input_id = self._last_portrait_input_id
            synthetic = self._last_synthetic

        active_cfg = getattr(getattr(app, "state", None), "config", cur_cfg) or cur_cfg
        if not bool(getattr(active_cfg, "personalive_enabled", False)):
            raise ValueError("请先开启 PersonaLive 实时姿态驱动开关")

        with self.lock:
            if self._stopping or self._generation != gen:
                raise RuntimeError("实时直播会话正在停止")
            item_dir = self._driving_item_dir(mid)
            if not item_dir.is_dir():
                raise KeyError(mid)
            meta_path = item_dir / "meta.json"
            _ensure_contained_path(item_dir, meta_path, "驱动动作元数据")
            meta: dict[str, Any] = {}
            if meta_path.is_file() and not meta_path.is_symlink():
                try:
                    loaded = json.loads(meta_path.read_text(encoding="utf-8"))
                    if isinstance(loaded, dict):
                        meta = loaded
                except (OSError, ValueError):
                    meta = {}

            clip_path: Path | None = None
            meta_clip_rel = str(meta.get("clip_path") or "").strip()
            if meta_clip_rel:
                cand = item_dir / meta_clip_rel
                _ensure_contained_path(item_dir, cand, "驱动视频文件")
                if cand.is_file() and not cand.is_symlink():
                    clip_path = cand
            if clip_path is None:
                for ext in sorted(COLABONE_LIVE_CLIP_EXTENSIONS):
                    cand = item_dir / f"clip.{ext}"
                    if cand.is_symlink():
                        raise ValueError("驱动视频文件不能是符号链接")
                    if cand.is_file():
                        _ensure_contained_path(item_dir, cand, "驱动视频文件")
                        clip_path = cand
                        break
            if clip_path is None:
                raise KeyError(mid)

        avatar_data: dict[str, Any] | None = None
        if active_cfg.avatar_id:
            try:
                avatar_data = self.get_avatar(active_cfg.avatar_id)
            except KeyError:
                avatar_data = None
        portrait_path = self._resolve_session_portrait_path(
            avatar_data, portrait_input_id
        )
        width, height = parse_colabone_resolution(active_cfg.resolution)
        extra_kw: dict[str, Any] = {}
        if not synthetic:
            venv_py = self._resolve_engine_venv_python("personalive_dance")
            model_dir = self._resolve_component_model_dir("visual_engines", "personalive_dance")
            if venv_py is not None:
                extra_kw["venv_python"] = venv_py
            if model_dir is not None:
                extra_kw["model_dir"] = model_dir
        pl_engine = ColabOnePersonaLiveVisualEngine(
            width=width,
            height=height,
            fps=active_cfg.target_fps,
            portrait_path=portrait_path,
            cache_dir=self.root / "live-cache",
            **extra_kw,
        )
        pl_engine.load_driving_clip(
            clip_path,
            allowed_root=self.driving_dir,
            duration_seconds=float(meta.get("duration_seconds") or 0.0),
        )
        with self.lock:
            if self._stopping or not self._running or self._generation != gen:
                try:
                    pl_engine.unload()
                except Exception:
                    pass
                raise RuntimeError("实时直播会话正在停止")
        return {
            "motion_id": mid,
            "label": str(meta.get("label") or mid),
            "visual_engine": pl_engine,
            "clip_path": str(clip_path),
            "bgm_path": str(meta.get("bgm_path") or ""),
            "duration_seconds": float(meta.get("duration_seconds") or 0.0),
        }

    def start(
        self,
        config: dict[str, Any] | ColabOneLiveStartRequest,
        *,
        gpu_info: dict[str, Any] | None = None,
        loaded_model_vram_mib: int = 0,
        on_activity: Callable[[], None] | None = None,
        server_factory: Callable[..., Any] | None = None,
        synthetic: bool = False,
        min_free_disk_mib: int | None = None,
        frame_pump_enabled: bool | None = None,
        task: Any = None,
        auto_provision: bool = False,
        prewarm_tts: bool = False,
    ) -> dict[str, Any]:
        req = (
            config
            if isinstance(config, ColabOneLiveStartRequest)
            else ColabOneLiveStartRequest.model_validate(config)
        )
        session_cfg = req.to_session_config()
        stopping = self._stopping_event()
        _task_check(task, stopping)
        budget = self.check_vram(
            session_cfg,
            gpu_info=gpu_info,
            loaded_model_vram_mib=loaded_model_vram_mib,
        )
        if not budget["ok"]:
            rec = "；".join(budget.get("recommendations") or [])
            msg = f"显存预检未通过（需 {budget['total_required_mib']} MiB，可用 {budget['available_mib']} MiB）"
            if rec:
                msg = f"{msg}。建议：{rec}"
            raise ValueError(msg)

        if req.portrait_input_id and self._resolve_portrait_input_path(req.portrait_input_id) is None:
            raise ValueError("人像输入文件不存在或已失效，请重新上传")

        avatar_data: dict[str, Any] | None = None
        avatar_dir: Path | None = None
        if req.avatar_id:
            avatar_data = self.get_avatar(req.avatar_id)
            avatar_dir = self._avatar_dir(req.avatar_id)
        portrait_path = self._resolve_session_portrait_path(
            avatar_data, req.portrait_input_id
        )

        # Auto-provision missing dependencies BEFORE acquiring _lifecycle_busy so stop()
        # or cancellation can never deadlock waiting on long downloads.
        with self.lock:
            entry_gen = self._generation
        started_marking = bool(auto_provision and not synthetic)
        if started_marking:
            self.mark_starting()
        try:
            if started_marking:
                self.ensure_session_dependencies(
                    task,
                    req,
                    synthetic=False,
                    min_free_disk_mib=min_free_disk_mib,
                )

            required_disk_mib = (
                max(0, int(min_free_disk_mib))
                if min_free_disk_mib is not None
                else (
                    # Provisioning already ran (or prewarm-only after provisioning);
                    # never fail startup because best-effort SenseVoice ASR was skipped.
                    self._required_provision_disk_mib(
                        session_cfg,
                        synthetic=synthetic,
                        include_best_effort_asr=False,
                    )
                    if (auto_provision or prewarm_tts)
                    else self._required_disk_mib(session_cfg, synthetic=synthetic)
                )
            )
            free_disk_mib = self._free_disk_mib()
            if free_disk_mib < required_disk_mib:
                raise ValueError(
                    f"磁盘空间不足（剩余 {free_disk_mib} MiB，至少需要 {required_disk_mib} MiB）"
                )

            _task_check(task, stopping)
            with self.lock:
                if self._generation != entry_gen:
                    raise _cancelled_error_cls()("操作已取消")
        except Exception as prov_exc:
            if started_marking:
                if _is_cancelled_error(prov_exc):
                    self.mark_stopped_clean()
                    raise
                secret_list = [req.hf_token, req.rtmp_url]
                if req.external_llm is not None:
                    secret_list.append(req.external_llm.api_key)
                scrubbed = scrub_colabone_live_secrets(str(prov_exc), *secret_list)
                self.mark_start_error(scrubbed)
                if isinstance(prov_exc, ValueError):
                    raise ValueError(scrubbed) from None
                raise RuntimeError(scrubbed) from None
            raise

        # Serialize concurrent start()/stop() via _lifecycle_cond WITHOUT holding
        # _lifecycle_lock across runner.stop() / runner.start().
        with self._lifecycle_cond:
            while self._lifecycle_busy:
                self._lifecycle_cond.wait()
            with self.lock:
                if self._generation != entry_gen:
                    raise _cancelled_error_cls()("操作已取消")
                self._lifecycle_busy = True
                self._stopping = True
                self._generation += 1
                active_switches_before_restart = list(self._active_switch_tasks)
                prev_runner, prev_app = self._detach_session_locked()

        for sw_task in active_switches_before_restart:
            try:
                sw_task.trigger_cancel()
            except Exception:
                pass

        try:
            if prev_runner is not None or prev_app is not None:
                self._teardown_detached_session(prev_runner, prev_app)

            with self.lock:
                self._stopping = False
                self._generation += 1
                start_gen = self._generation
                self._state = "starting"
                self._error = ""

            tts: Any = None
            visual: Any = None
            encoder: Any = None
            runner: Any = None
            try:
                _task_check(task, stopping)
                tts = self._build_tts_engine(
                    req.tts_engine,
                    synthetic=synthetic,
                    hf_token=req.hf_token,
                )
                if prewarm_tts and not synthetic and callable(getattr(tts, "load", None)):
                    tts_entry = self._catalog_entry("streaming_tts", req.tts_engine) or {}
                    tts_name = str(tts_entry.get("name") or req.tts_engine)
                    _task_update(
                        task,
                        progress=0.90,
                        message=f"正在预热流式语音模型到显存：{tts_name}...",
                        monotonic_progress=True,
                    )
                    _task_note(task, f"正在预热流式语音模型：{tts_name}")
                    prev_hook = None
                    installed_hook = None
                    if task is not None and hasattr(task, "lock") and hasattr(task, "cancel_hook"):
                        def _tts_cancel_hook() -> None:
                            try:
                                tts.unload()
                            except Exception:
                                pass
                            if callable(prev_hook):
                                try:
                                    prev_hook()
                                except Exception:
                                    pass

                        with task.lock:
                            prev_hook = task.cancel_hook
                            installed_hook = _tts_cancel_hook
                            task.cancel_hook = installed_hook
                    try:
                        # Re-check cancellation AFTER installing cancel_hook so a cancel
                        # between line 3411 and hook install cannot freeze for 120s.
                        _task_check(task, stopping)
                        tts.load()
                    except Exception:
                        _task_check(task, stopping)
                        raise
                    finally:
                        if task is not None and hasattr(task, "lock") and hasattr(task, "cancel_hook"):
                            with task.lock:
                                if task.cancel_hook is installed_hook:
                                    task.cancel_hook = prev_hook
                    _task_check(task, stopping)

                _task_update(
                    task,
                    progress=0.95,
                    message="正在启动实时演播服务...",
                    monotonic_progress=True,
                )
                visual = self._build_visual_engine(
                    req.visual_engine,
                    synthetic=synthetic,
                    resolution=req.resolution,
                    fps=req.target_fps,
                    portrait_path=portrait_path,
                    avatar_dir=avatar_dir,
                )
                encoder = self._build_video_encoder(
                    width=visual.width,
                    height=visual.height,
                    fps=req.target_fps,
                    synthetic=synthetic,
                    enable_rtmp=req.enable_rtmp,
                    rtmp_url=req.rtmp_url,
                )
                asr_adapter: ColabOneSenseVoiceAsrAdapter | None = None
                if not synthetic:
                    asr_dir = self._resolve_component_model_dir(
                        "asr_vad", "sensevoice_small"
                    )
                    asr_adapter = ColabOneSenseVoiceAsrAdapter(
                        model_dir=str(asr_dir) if asr_dir is not None else ""
                    )

                secret_list = [req.hf_token, req.rtmp_url]
                if req.external_llm is not None:
                    secret_list.append(req.external_llm.api_key)

                eff_frame_pump = (
                    (not synthetic)
                    if frame_pump_enabled is None
                    else bool(frame_pump_enabled)
                )
                app = create_colabone_live_app(
                    verify_token=self.verify_session_token,
                    verify_token_info=self.verify_session_token_info,
                    redeem_companion_ticket=self.redeem_companion_ticket,
                    session_config=session_cfg,
                    avatar=avatar_data,
                    external_llm=req.external_llm,
                    tts_engine=tts,
                    visual_engine=visual,
                    encoder=encoder,
                    asr=asr_adapter,
                    on_activity=on_activity,
                    on_switch_engine=self._handle_ws_switch_engine,
                    on_trigger_driving=self._handle_ws_trigger_driving,
                    on_recreate_base_visual=self._recreate_current_base_visual,
                    vram_budget=budget,
                    active_secrets=secret_list,
                    frame_pump_enabled=eff_frame_pump,
                    ws_path=self.ws_path,
                )

                _task_check(task, stopping)
                if server_factory is not None:
                    runner = server_factory(app, host="127.0.0.1", port=self.port)
                else:
                    runner = ColabOneLiveServerRunner(app, host="127.0.0.1", port=self.port)
                runner.start()

                cancelled_during_start = False
                try:
                    _task_check(task, stopping)
                except Exception as chk_exc:
                    if _is_cancelled_error(chk_exc):
                        cancelled_during_start = True
                    else:
                        raise

                with self.lock:
                    if (
                        cancelled_during_start
                        or self._stopping
                        or self._generation != start_gen
                    ):
                        aborted = True
                    else:
                        self._app = app
                        self._runner = runner
                        self._running = True
                        self._state = "ready"
                        self._started_at = time.time()
                        self._active_config = req.redacted_summary()
                        self._active_session_cfg = session_cfg
                        self._vram_budget = budget
                        self._error = ""
                        self._last_gpu_info = dict(gpu_info) if isinstance(gpu_info, dict) else None
                        self._last_loaded_vram_mib = int(loaded_model_vram_mib or 0)
                        self._last_synthetic = bool(synthetic)
                        self._last_auto_provision = bool(auto_provision or prewarm_tts)
                        self._last_portrait_input_id = req.portrait_input_id
                        self._last_hf_token = req.hf_token
                        aborted = False
                if aborted:
                    self._teardown_detached_session(runner, app)
                    runner = None
                    from server.runtime import ColabOneCancelledError

                    raise ColabOneCancelledError("实时直播会话在启动过程中被停止")
            except Exception as exc:
                if runner is not None:
                    try:
                        runner.stop()
                    except Exception:
                        pass
                for eng in (tts, visual):
                    if eng is not None and callable(getattr(eng, "unload", None)):
                        try:
                            eng.unload()
                        except Exception:
                            pass
                if isinstance(encoder, ColabOneLiveVideoEncoder):
                    try:
                        encoder.close()
                    except Exception:
                        pass
                if _is_cancelled_error(exc):
                    with self.lock:
                        self._app = None
                        self._runner = None
                        self._running = False
                        self._state = "stopped"
                        self._started_at = None
                        self._active_config = None
                        self._active_session_cfg = None
                        self._vram_budget = None
                        self._last_hf_token = ""
                        self._last_portrait_input_id = ""
                        self._error = ""
                    raise
                secret_list = [req.hf_token, req.rtmp_url]
                if req.external_llm is not None:
                    secret_list.append(req.external_llm.api_key)
                scrubbed_err = scrub_colabone_live_secrets(str(exc), *secret_list)
                with self.lock:
                    self._app = None
                    self._runner = None
                    self._running = False
                    self._state = "error"
                    self._started_at = None
                    self._active_config = None
                    self._active_session_cfg = None
                    self._vram_budget = None
                    self._last_hf_token = ""
                    self._last_portrait_input_id = ""
                    self._error = scrubbed_err
                raise RuntimeError(scrubbed_err) from None
        finally:
            with self._lifecycle_cond:
                self._lifecycle_busy = False
                self._lifecycle_cond.notify_all()

        return self.status()

    def stop(self) -> dict[str, Any]:
        # Immediately flag stopping & bump generation under self.lock so any active WS
        # ThreadPoolExecutor callback fails fast before we even wait for lifecycle_cond.
        with self.lock:
            self._stopping = True
            self._generation += 1
            active_switches = list(self._active_switch_tasks)
        for sw_task in active_switches:
            try:
                sw_task.trigger_cancel()
            except Exception:
                pass

        with self._lifecycle_cond:
            while self._lifecycle_busy:
                self._lifecycle_cond.wait()
            self._lifecycle_busy = True
            with self.lock:
                self._stopping = True
                self._generation += 1
                runner, app = self._detach_session_locked()

        try:
            self._teardown_detached_session(runner, app)
        finally:
            with self.lock:
                self._state = "stopped"
                self._stopping = False
            with self._lifecycle_cond:
                self._lifecycle_busy = False
                self._lifecycle_cond.notify_all()
        return self.status()

    def is_running(self) -> bool:
        with self.lock:
            return bool(self._running)

    def active_client_count(self) -> int:
        """Fast-path active WS client count with minimal locking and zero disk I/O."""
        with self.lock:
            app = self._app
        if app is None:
            return 0
        app_state = getattr(app, "state", None)
        clients_box = getattr(app_state, "active_clients", None)
        if isinstance(clients_box, dict):
            return max(0, int(clients_box.get("count", 0) or 0))
        return 0

    def status(self) -> dict[str, Any]:
        with self.lock:
            director_state = "IDLE"
            turn_epoch = 0
            active_clients = 0
            if self._app is not None:
                app_state = getattr(self._app, "state", None)
                director = getattr(app_state, "director", None)
                if director is not None:
                    director_state = director.state
                    turn_epoch = director.turn_epoch
                clients_box = getattr(app_state, "active_clients", None)
                if isinstance(clients_box, dict):
                    active_clients = max(0, int(clients_box.get("count", 0) or 0))
            cfg_copy = (
                json.loads(json.dumps(self._active_config))
                if self._active_config is not None
                else None
            )
            cfg_dict = cfg_copy or {}
            return {
                "running": self._running,
                "state": self._state,
                "port": self.port,
                "ws_path": self.ws_path,
                "director_state": director_state,
                "turn_epoch": turn_epoch,
                "active_clients": active_clients,
                "visual_engine": cfg_dict.get("visual_engine", "flashhead_lite"),
                "personalive_enabled": bool(cfg_dict.get("personalive_enabled", False)),
                "tts_engine": cfg_dict.get("tts_engine", "qwen3_tts"),
                "llm_mode": cfg_dict.get("llm_mode", "local_gguf"),
                "director_mode": cfg_dict.get("director_mode", "dialogue"),
                "mic_mode": cfg_dict.get("mic_mode", "vad"),
                "avatar_id": cfg_dict.get("avatar_id", ""),
                "active_config": cfg_copy,
                "vram_budget": (
                    json.loads(json.dumps(self._vram_budget))
                    if self._vram_budget is not None
                    else None
                ),
                "started_at": self._started_at,
                "avatar_count": len(self.list_avatars()),
                "driving_motion_count": len(self.list_driving_motions()),
                "companion": self.companion_status(),
                "error": self._error,
            }

    # ------------------------------------------------------------------
    # External LLM connectivity probe (with strict secret scrubbing)
    # ------------------------------------------------------------------

    def test_external_llm(
        self,
        config: dict[str, Any] | ColabOneLiveExternalLLMTestRequest,
        *,
        http_get: Callable[..., Any] | None = None,
        http_post: Callable[..., Any] | None = None,
        dns_resolver: Callable[..., Any] | None = None,
    ) -> dict[str, Any]:
        req = (
            config
            if isinstance(config, ColabOneLiveExternalLLMTestRequest)
            else ColabOneLiveExternalLLMTestRequest.model_validate(config)
        )
        pinned_ips: list[str] = []
        if dns_resolver is not None or (http_get is None and http_post is None):
            pinned_ips = validate_colabone_external_llm_resolved_ips(
                req.base_url,
                resolver=dns_resolver or socket.getaddrinfo,
            )

        headers: dict[str, str] = {"Accept": "application/json"}
        if req.api_key:
            headers["Authorization"] = f"Bearer {req.api_key}"

        models_url = f"{req.base_url.rstrip('/')}/models"
        chat_url = f"{req.base_url.rstrip('/')}/chat/completions"
        started = time.monotonic()

        session: requests.Session | None = None
        if http_get is None or http_post is None:
            session = requests.Session()
            session.trust_env = False
            if pinned_ips:
                parsed_base = urlparse(req.base_url)
                orig_host = (parsed_base.hostname or "").strip().lower().rstrip(".")
                if orig_host:
                    session.mount(
                        "https://",
                        _ColabOnePinnedIPAdapter(
                            expected_host=orig_host,
                            pinned_ip=pinned_ips[0],
                            use_tls=True,
                        ),
                    )
                    session.mount(
                        "http://",
                        _ColabOnePinnedIPAdapter(
                            expected_host=orig_host,
                            pinned_ip=pinned_ips[0],
                            use_tls=False,
                        ),
                    )
        get_fn = http_get or (session.get if session is not None else requests.get)
        post_fn = http_post or (session.post if session is not None else requests.post)

        try:
            resp = get_fn(
                models_url,
                headers=headers,
                timeout=req.timeout_seconds,
                allow_redirects=False,
            )
            latency_ms = round((time.monotonic() - started) * 1000.0, 1)
            code = int(getattr(resp, "status_code", 0) or 0)
            if 200 <= code < 300:
                return {
                    "ok": True,
                    "status_code": code,
                    "latency_ms": latency_ms,
                    "model": req.model,
                    "endpoint": models_url,
                    "detail": "外部 LLM 接口连通正常",
                }
            if 300 <= code < 400:
                return {
                    "ok": False,
                    "status_code": code,
                    "latency_ms": latency_ms,
                    "model": req.model,
                    "endpoint": models_url,
                    "error": f"外部 LLM 接口返回重定向 ({code})，出于安全策略已拒绝跟随跳转",
                }
            if code not in (404, 405):
                text = scrub_colabone_live_secrets(
                    str(getattr(resp, "text", "") or "")[:4096], req.api_key
                )[:240]
                return {
                    "ok": False,
                    "status_code": code,
                    "latency_ms": latency_ms,
                    "model": req.model,
                    "endpoint": models_url,
                    "error": f"接口返回状态码 {code}: {text}".strip(),
                }

            # Fallback tiny chat completion probe when /models is disabled
            chat_resp = post_fn(
                chat_url,
                headers={**headers, "Content-Type": "application/json"},
                json={
                    "model": req.model,
                    "messages": [{"role": "user", "content": "ping"}],
                    "max_tokens": 1,
                    "stream": False,
                },
                timeout=req.timeout_seconds,
                allow_redirects=False,
            )
            latency_ms = round((time.monotonic() - started) * 1000.0, 1)
            chat_code = int(getattr(chat_resp, "status_code", 0) or 0)
            if 200 <= chat_code < 300:
                return {
                    "ok": True,
                    "status_code": chat_code,
                    "latency_ms": latency_ms,
                    "model": req.model,
                    "endpoint": chat_url,
                    "detail": "外部 LLM 对话探针连通正常",
                }
            if 300 <= chat_code < 400:
                return {
                    "ok": False,
                    "status_code": chat_code,
                    "latency_ms": latency_ms,
                    "model": req.model,
                    "endpoint": chat_url,
                    "error": f"外部 LLM 对话探针返回重定向 ({chat_code})，出于安全策略已拒绝跟随跳转",
                }
            err_text = scrub_colabone_live_secrets(
                str(getattr(chat_resp, "text", "") or "")[:4096], req.api_key
            )[:240]
            return {
                "ok": False,
                "status_code": chat_code,
                "latency_ms": latency_ms,
                "model": req.model,
                "endpoint": chat_url,
                "error": f"对话探针失败 ({chat_code}): {err_text}".strip(),
            }
        except Exception as exc:
            latency_ms = round((time.monotonic() - started) * 1000.0, 1)
            return {
                "ok": False,
                "status_code": 0,
                "latency_ms": latency_ms,
                "model": req.model,
                "endpoint": req.base_url,
                "error": scrub_colabone_live_secrets(str(exc), req.api_key),
            }
        finally:
            if session is not None:
                try:
                    session.close()
                except Exception:
                    pass

    # ------------------------------------------------------------------
    # Single-photo offline Avatar Action Pack batch builder
    # ------------------------------------------------------------------

    def build_avatar_pack(
        self,
        task: Any,
        req: dict[str, Any] | ColabOneLiveBuildPackRequest,
        *,
        comfy_runner: Callable[..., Any] | None = None,
    ) -> dict[str, Any]:
        """Batch-generate default & dance action clips into live-avatars/<avatar_id>/."""
        parsed = (
            req
            if isinstance(req, ColabOneLiveBuildPackRequest)
            else ColabOneLiveBuildPackRequest.model_validate(req)
        )
        _task_check(task)

        # Validate action targets and comfy_runner BEFORE mutating avatar state on disk.
        target_ids: list[str] = []
        for aid in [*parsed.actions, *parsed.dance_ids]:
            if aid not in target_ids:
                target_ids.append(aid)
        if not target_ids:
            raise ValueError("至少需要选择一个待生成动作片段")
        if comfy_runner is None:
            raise RuntimeError("未提供 ComfyUI 批处理执行器，无法离线构建主播动作包")

        _task_update(task, 0.02, "正在校验主播动作包与人像素材...")

        avatar_dir = self._avatar_dir(parsed.avatar_id)
        if not avatar_dir.is_dir() or not self._manifest_path(avatar_dir).is_file():
            self.save_avatar(
                {
                    "id": parsed.avatar_id,
                    "name": parsed.name or parsed.avatar_id,
                    "portrait_input_id": parsed.portrait_input_id,
                    "portrait_b64": parsed.portrait_b64,
                    "portrait_filename": parsed.portrait_filename,
                }
            )
        elif parsed.portrait_b64 or parsed.portrait_input_id:
            # Omit actions/products so save_avatar preserves concurrent updates atomically.
            existing = self.get_avatar(parsed.avatar_id)
            self.save_avatar(
                {
                    "id": parsed.avatar_id,
                    "name": parsed.name or existing["name"],
                    "persona_prompt": existing.get("persona_prompt", ""),
                    "portrait_input_id": parsed.portrait_input_id,
                    "portrait_b64": parsed.portrait_b64,
                    "portrait_filename": parsed.portrait_filename,
                    "default_tts_engine": existing.get("default_tts_engine", "qwen3_tts"),
                    "default_voice_preset": existing.get("default_voice_preset", "shen_zhiyi"),
                }
            )

        manifest = self.get_avatar(parsed.avatar_id)
        portrait_rel = manifest.get("portrait_path", "")
        portrait_path = (avatar_dir / portrait_rel) if portrait_rel else None
        if portrait_path is not None:
            _ensure_contained_path(avatar_dir, portrait_path, "人像文件")

        built_actions: list[dict[str, Any]] = []
        total = len(target_ids)
        for idx, action_id in enumerate(target_ids):
            _task_check(task)
            meta = self._default_action_meta(action_id)
            pct = round(0.05 + (idx / max(1, total)) * 0.90, 4)
            _task_update(
                task,
                pct,
                f"正在生成动作片段 ({idx + 1}/{total}): {meta.get('label', action_id)}",
            )
            _task_note(task, f"开始生成动作 {action_id}")

            with self.lock:
                if not avatar_dir.is_dir() or not self._manifest_path(avatar_dir).is_file():
                    raise KeyError(parsed.avatar_id)
                cur_manifest_pre = self._load_manifest_at(avatar_dir)
                existing_spec = next(
                    (
                        dict(a)
                        for a in cur_manifest_pre.get("actions", [])
                        if isinstance(a, dict) and a.get("id") == action_id
                    ),
                    None,
                )
                if (
                    not parsed.overwrite
                    and existing_spec
                    and existing_spec.get("clip_path")
                    and (avatar_dir / existing_spec["clip_path"]).is_file()
                ):
                    built_actions.append(existing_spec)
                    continue

            result = comfy_runner(
                action_id=action_id,
                action_meta=meta,
                avatar_dir=avatar_dir,
                portrait_path=portrait_path,
                request=parsed,
            )

            clip_ext = "mp4"
            if isinstance(result, (bytes, bytearray)):
                raw_bytes = bytes(result)
                duration = float(meta.get("default_duration_seconds") or 4.0)
            elif isinstance(result, (str, Path)):
                src_path = Path(result)
                if src_path.is_symlink() or not src_path.is_file():
                    raise ValueError(f"生成的动作文件不存在或为符号链接: {action_id}")
                cand_ext = src_path.suffix.lower().lstrip(".")
                if cand_ext in COLABONE_LIVE_CLIP_EXTENSIONS:
                    clip_ext = cand_ext
                raw_bytes = src_path.read_bytes()
                duration = float(meta.get("default_duration_seconds") or 4.0)
            elif isinstance(result, dict):
                if "bytes" in result and isinstance(result["bytes"], (bytes, bytearray)):
                    raw_bytes = bytes(result["bytes"])
                    cand_ext = str(result.get("extension") or "").strip().lower().lstrip(".")
                    if cand_ext in COLABONE_LIVE_CLIP_EXTENSIONS:
                        clip_ext = cand_ext
                elif "path" in result:
                    src_path = Path(result["path"])
                    if src_path.is_symlink() or not src_path.is_file():
                        raise ValueError(f"生成的动作文件不存在或为符号链接: {action_id}")
                    cand_ext = (
                        src_path.suffix.lower().lstrip(".")
                        or str(result.get("extension") or "").strip().lower().lstrip(".")
                    )
                    if cand_ext in COLABONE_LIVE_CLIP_EXTENSIONS:
                        clip_ext = cand_ext
                    raw_bytes = src_path.read_bytes()
                else:
                    raise ValueError(f"动作构建器返回结果缺少 bytes 或 path: {action_id}")
                duration = float(
                    result.get("duration_seconds")
                    or meta.get("default_duration_seconds")
                    or 4.0
                )
            else:
                raise ValueError(f"动作构建器返回了不支持的结果类型: {type(result).__name__}")

            now = time.time()
            with self.lock:
                if not avatar_dir.is_dir() or not self._manifest_path(avatar_dir).is_file():
                    raise KeyError(parsed.avatar_id)
                cur_manifest = self._load_manifest_at(avatar_dir)
                cur_assets_meta = self._read_assets_meta(avatar_dir)
                cur_actions = {
                    a["id"]: dict(a)
                    for a in cur_manifest.get("actions", [])
                    if isinstance(a, dict) and a.get("id")
                }
                cur_existing_spec = cur_actions.get(action_id)

                clip_rel = f"clips/{action_id}.{clip_ext}"
                clip_target = avatar_dir / clip_rel
                _ensure_contained_path(avatar_dir, clip_target, "生成动作片段")

                # Validate action spec & manifest BEFORE writing/unlinking files on disk.
                spec = ColabOneLiveActionSpec.model_validate(
                    {
                        "id": action_id,
                        "label": meta.get("label") or action_id,
                        "kind": meta.get("kind") or "oneshot",
                        "clip_path": clip_rel,
                        "bgm_path": cur_existing_spec.get("bgm_path", "")
                        if cur_existing_spec
                        else "",
                        "duration_seconds": duration,
                        "trigger_keywords": list(meta.get("trigger_keywords") or []),
                    }
                ).model_dump()
                cur_actions[action_id] = spec
                candidate_manifest = dict(cur_manifest)
                candidate_manifest["actions"] = list(cur_actions.values())
                candidate_manifest["updated_at"] = now
                validated = ColabOneLiveAvatarManifest.model_validate(
                    candidate_manifest
                ).model_dump()

                clips_dir = avatar_dir / "clips"
                _ensure_safe_dir(clips_dir, "动作片段目录")
                clip_sha = _atomic_write_bytes(clip_target, raw_bytes)
                clip_size = len(raw_bytes)
                for old_ext in COLABONE_LIVE_CLIP_EXTENSIONS:
                    if old_ext == clip_ext:
                        continue
                    cur_assets_meta.pop(f"clips/{action_id}.{old_ext}", None)
                cur_assets_meta[clip_rel] = {
                    "sha256": clip_sha,
                    "size": clip_size,
                    "updated_at": now,
                }
                atomic_json(self._manifest_path(avatar_dir), validated)
                self._write_assets_meta(avatar_dir, cur_assets_meta)
                self._refresh_active_app_avatar_locked(parsed.avatar_id, validated)

                for old_ext in COLABONE_LIVE_CLIP_EXTENSIONS:
                    if old_ext == clip_ext:
                        continue
                    stale_path = avatar_dir / f"clips/{action_id}.{old_ext}"
                    if stale_path.is_file() and not stale_path.is_symlink():
                        try:
                            stale_path.unlink()
                        except OSError:
                            pass

                built_actions.append(spec)

        _task_update(task, 1.0, f"主播动作包构建完成（共 {len(built_actions)} 个动作）")
        _task_note(task, f"主播动作包 {parsed.avatar_id} 构建完成")
        final_manifest = self.get_avatar(parsed.avatar_id)
        return {
            "avatar_id": parsed.avatar_id,
            "built_count": len(built_actions),
            "actions": built_actions,
            "avatar": final_manifest,
        }
