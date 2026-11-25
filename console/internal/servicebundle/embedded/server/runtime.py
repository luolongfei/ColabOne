"""Remote-only model cache, task supervisor and llama.cpp engine.
CUDA deployment adapted from colab-gpu-llm-service (MIT; see third_party).
"""

from __future__ import annotations

import errno
import hashlib
import inspect
import json
import os
import re
import shutil
import subprocess
import tarfile
import threading
import time
import uuid
import copy
from functools import wraps
from pathlib import Path
from urllib.parse import quote

import requests

from common.comfyui import (
    ComfyTtsPreviewRequest,
    WorkflowOutputRecord,
    WorkflowRunRequest,
    WorkflowTemplate,
    validate_colabone_hf_token,
)
from common.files import atomic_json, colabone_service_revision
from common.location import infer_location_from_endpoint, probe_vm_location
from common.models import (
    ModelSpec,
    resolve_colabone_api_model_id,
    validate_colabone_api_model_id,
)
from server.drive import ColabOneDrive
from server.telemetry import ColabOneResourceSampler
from server.download import (
    ColabOneTransfer,
    parallel_file,
    cached_bytes,
    metadata_path,
    ColabOneRangeUnsupportedError,
    CHUNK,
)


_UNSET = object()

_BEARER_SECRET_RE = re.compile(r"(?i)\bBearer\s+[^\s'\"`,;}\]]+")
_HF_TOKEN_RE = re.compile(r"\bhf_[A-Za-z0-9_-]{4,}\b")
_COLABONE_API_KEY_RE = re.compile(r"\bco_[A-Za-z0-9_-]{20,}")
_JWT_TOKEN_RE = re.compile(r"eyJ[A-Za-z0-9_+/=-]{20,}(?:\.[A-Za-z0-9_+/=-]+)*")
_GOOGLE_ACCESS_TOKEN_RE = re.compile(r"\bya29\.[A-Za-z0-9._-]+")
_GOOGLE_REFRESH_TOKEN_RE = re.compile(r"(?<![A-Za-z0-9])1//[A-Za-z0-9._-]+")
_GOOGLE_CLIENT_SECRET_RE = re.compile(r"\bGOCSPX-[A-Za-z0-9._-]+")
_HF_NON_SECRET_IDENTIFIERS = frozenset(
    {
        "hf_token",
        "hf_hub",
        "hf_home",
        "hf_cache",
        "hf_transfer",
        "hf_endpoint",
        "hf_xet",
    }
)


def _validate_hf_token(token: str) -> str:
    if token is None or token == "":
        return ""
    if not isinstance(token, str):
        raise ValueError("hf_token 格式不合法")
    return validate_colabone_hf_token(token)


def _scrub_hf_match(match: re.Match[str]) -> str:
    tok = match.group(0)
    if tok.lower() in _HF_NON_SECRET_IDENTIFIERS:
        return tok
    return "***"


def _scrub_secret_text(text: str, *secrets: str) -> str:
    result = str(text or "")
    if not result:
        return ""
    candidates: list[str] = []
    for secret in secrets:
        if not isinstance(secret, str):
            continue
        stripped = secret.strip()
        if not stripped:
            continue
        for variant in (secret, stripped, repr(secret)[1:-1], repr(stripped)[1:-1]):
            if variant and variant not in candidates:
                candidates.append(variant)
    for cand in sorted(candidates, key=len, reverse=True):
        if cand in result:
            result = result.replace(cand, "***")
    result = _BEARER_SECRET_RE.sub("Bearer ***", result)
    result = _HF_TOKEN_RE.sub(_scrub_hf_match, result)
    result = _COLABONE_API_KEY_RE.sub("***", result)
    result = _JWT_TOKEN_RE.sub("***", result)
    result = _GOOGLE_ACCESS_TOKEN_RE.sub("***", result)
    result = _GOOGLE_REFRESH_TOKEN_RE.sub("***", result)
    result = _GOOGLE_CLIENT_SECRET_RE.sub("***", result)
    return result


class _TrackedRLock:
    """Reentrant lock that tracks whether the current thread holds it."""

    def __init__(self) -> None:
        self._lock = threading.RLock()
        self._local = threading.local()

    def acquire(self, blocking: bool = True, timeout: float = -1) -> bool:
        ok = (
            self._lock.acquire(blocking, timeout)
            if timeout != -1
            else self._lock.acquire(blocking)
        )
        if ok:
            self._local.depth = getattr(self._local, "depth", 0) + 1
        return ok

    def release(self) -> None:
        self._local.depth = max(0, getattr(self._local, "depth", 0) - 1)
        self._lock.release()

    def held_by_current_thread(self) -> bool:
        return getattr(self._local, "depth", 0) > 0

    def __enter__(self):
        self.acquire()
        return self

    def __exit__(self, exc_type, exc, tb):
        self.release()
        return False


def synchronized(fn):
    @wraps(fn)
    def wrapped(self, *args, **kwargs):
        with self.lock:
            return fn(self, *args, **kwargs)

    return wrapped


class ColabOneConflictError(RuntimeError):
    pass


class ColabOneCancelledError(RuntimeError):
    pass


class ColabOneTask:
    def __init__(self, kind, model_id=""):
        self.id = uuid.uuid4().hex
        self.kind, self.model_id = kind, model_id
        self.state, self.progress, self.error = "running", 0, ""
        self.message = ""
        self.cancel = threading.Event()
        self.cancel_hook = None
        self.events = []
        self.lock = threading.RLock()
        self.created_at = time.time()
        self._started_monotonic = time.monotonic()
        self.finished_at: float | None = None
        self.elapsed_seconds: float | None = None
        self.transfer = None
        self.logs = []
        self.log_seq = 0
        self.outputs = []

    def note(self, message, level="info"):
        with self.lock:
            self.log_seq += 1
            self.logs.append(
                {
                    "seq": self.log_seq,
                    "time": time.time(),
                    "message": message,
                    "level": level,
                }
            )
            self.logs = self.logs[-100:]

    def emit(self, event, data):
        with self.lock:
            self.events.append(
                {"seq": len(self.events) + 1, "event": event, "data": data}
            )

    def update(
        self,
        progress=_UNSET,
        message=_UNSET,
        transfer=_UNSET,
        *,
        kind=_UNSET,
        monotonic_progress: bool = False,
    ):
        with self.lock:
            if progress is not _UNSET and progress is not None:
                if not monotonic_progress or progress > self.progress:
                    self.progress = progress
            if message is not _UNSET and message is not None:
                self.message = message
            if transfer is not _UNSET:
                self.transfer = transfer
            if kind is not _UNSET and kind is not None:
                self.kind = kind

    def request_cancel(self):
        self.cancel.set()
        with self.lock:
            hook = self.cancel_hook
        if hook:
            try:
                hook()
            except Exception:
                pass  # The generation loop may have just finished.

    def check(self):
        if self.cancel.is_set():
            raise ColabOneCancelledError("操作已取消")

    def snapshot(self):
        with self.lock:
            value = {
                k: getattr(self, k)
                for k in (
                    "id",
                    "kind",
                    "model_id",
                    "state",
                    "progress",
                    "error",
                    "message",
                    "created_at",
                )
            }
            value["elapsed_seconds"] = (
                self.elapsed_seconds
                if self.elapsed_seconds is not None
                else round(max(0.0, time.monotonic() - self._started_monotonic), 3)
            )
            if self.finished_at is not None:
                value["finished_at"] = self.finished_at
            value["logs"] = list(self.logs)
            transfer = self.transfer
        if transfer:
            value["download"] = transfer.snapshot()
        return value


class ColabOneRuntime:
    def __init__(self, root: Path, catalog: Path, http=None, drive=None):
        self.root, self.catalog = root, catalog
        self.root.mkdir(parents=True, exist_ok=True)
        self.models_dir = root / "models"
        self.models_dir.mkdir(exist_ok=True)
        self.lock = _TrackedRLock()
        self._engine_lifecycle_lock = threading.RLock()
        self._comfy_lifecycle_lock = threading.Lock()
        self.sync_lock = threading.Lock()
        self.release_lock = threading.Lock()
        self.quiescing = False
        self.auto_releasing = False
        self.release_prepared = False
        self.http = http or requests.Session()
        self.http.trust_env = False
        self.drive = drive or ColabOneDrive(root, catalog, http=self.http)
        self._dirty_seq: dict[str, int] = {}
        self._seq_counter = 0
        self.drive_restored = False
        self.service_revision = colabone_service_revision(
            Path(__file__).resolve().parents[1]
        )
        self.runtime_id = os.environ.get("COLABONE_RUNTIME_ID", uuid.uuid4().hex)
        self.location = infer_location_from_endpoint(self.runtime_id)
        if (root / "location.json").is_file():
            try:
                cached_loc = json.loads(
                    (root / "location.json").read_text(encoding="utf-8")
                )
                if isinstance(cached_loc, dict) and cached_loc.get("country"):
                    self.location = cached_loc
            except (OSError, ValueError):
                pass
        self.models = {
            m.id: m
            for m in map(
                ModelSpec.model_validate,
                json.loads((catalog / "presets.json").read_text(encoding="utf-8")),
            )
        }
        self._preset_api_ids = {mid: m.api_id for mid, m in self.models.items()}
        self.tasks: dict[str, ColabOneTask] = {}
        self.loaded = None
        self.active_options = {}
        self.proc = None
        self.chat_transport = None
        self.telemetry = ColabOneResourceSampler()
        self._telemetry_thread: threading.Thread | None = None
        self.last_active = time.time()
        self.idle_minutes = 30
        self.idle_countdown_seconds = 60
        self.activity_until = 0.0
        self.deadline = 0.0
        self.stopping = threading.Event()
        from server.public_api import ColabOnePublicGateway
        from server.comfyui import ColabOneComfyManager

        self.public_api = ColabOnePublicGateway(self)
        self.comfy = ColabOneComfyManager(self)
        if (root / "models.json").exists():
            try:
                raw_models = json.loads(
                    (root / "models.json").read_text(encoding="utf-8")
                )
            except (OSError, ValueError):
                raw_models = []
            if isinstance(raw_models, list):
                for value in raw_models:
                    try:
                        model = ModelSpec.model_validate(value)
                    except ValueError:
                        continue
                    if (
                        not model.api_id
                        and model.id in self._preset_api_ids
                        and self._preset_api_ids[model.id]
                    ):
                        model = model.model_copy(
                            update={"api_id": self._preset_api_ids[model.id]}
                        )
                    self.models[model.id] = model
                self._mark_dirty("models")

    def _mark_dirty(self, key: str):
        with self.lock:
            self._seq_counter += 1
            self._dirty_seq[key] = self._seq_counter

    def _refresh_comfy_queue_if_needed(self) -> None:
        held_fn = getattr(self.lock, "held_by_current_thread", None)
        if callable(held_fn) and held_fn():
            return
        if self.comfy.is_running():
            try:
                self.comfy.queue_counts()
            except Exception:
                pass

    def busy(self, *, _use_cached_queue: bool = False):
        held_fn = getattr(self.lock, "held_by_current_thread", None)
        already_locked = bool(held_fn()) if callable(held_fn) else False
        with self.lock:
            tasks = list(self.tasks.values())
        if any(t.state == "running" for t in tasks):
            return True
        if self.comfy.is_running():
            try:
                if _use_cached_queue or already_locked:
                    cached_fn = getattr(self.comfy, "queue_counts_cached", None)
                    counts = (
                        cached_fn()
                        if callable(cached_fn)
                        else self.comfy.queue_counts()
                    )
                else:
                    counts = self.comfy.queue_counts()
                if sum(counts) > 0:
                    return True
            except Exception:
                pass
        return False

    @synchronized
    def touch(self, hold_seconds=0):
        if self.quiescing:
            raise ColabOneConflictError("实例正在释放，无法延长闲置时间")
        self.last_active = time.time()
        self.activity_until = max(
            self.activity_until, self.last_active + hold_seconds
        )
        return self.idle_status(_use_cached_queue=True)

    def idle_status(self, now=None, *, _use_cached_queue: bool = False):
        now = time.time() if now is None else now
        active = self.busy(_use_cached_queue=_use_cached_queue) or now < self.activity_until
        elapsed = (
            0 if active else max(0, now - max(self.last_active, self.activity_until))
        )
        total = self.idle_minutes * 60
        warn_window = min(self.idle_countdown_seconds, total)
        remaining = max(0, total - elapsed)
        return {
            "phase": "active"
            if active
            else "countdown"
            if remaining <= warn_window
            else "watching",
            "remaining_seconds": remaining,
            "countdown_seconds": warn_window,
            "idle_seconds": elapsed,
            "server_time": now,
        }

    def claim_auto_release(self, now=None):
        self._refresh_comfy_queue_if_needed()
        with self.lock:
            now = time.time() if now is None else now
            if self.auto_releasing:
                return True
            if self.quiescing:
                return False
            budget_due = bool(self.deadline > 0 and now >= self.deadline)
            idle = self.idle_status(now)
            if budget_due or (
                idle["phase"] == "countdown" and idle["remaining_seconds"] <= 0
            ):
                self.quiescing = True
                self.auto_releasing = True
                return True
            return False

    def status(self):
        with self.lock:
            if self.proc and self.proc.poll() is not None:
                self.proc, self.loaded = None, None
                self.active_options = {}
            telemetry_alive = (
                self._telemetry_thread is not None
                and self._telemetry_thread.is_alive()
            )
            loaded = self.loaded
            active_options = self.active_options.copy()
            location = copy.deepcopy(self.location)
            quiescing = self.quiescing
            idle_minutes = self.idle_minutes
            deadline = self.deadline
            dirty_count = len(self._dirty_seq)
            tasks = list(self.tasks.values())
        comfy_status = self.comfy.status()
        comfy_running = bool(comfy_status.get("running"))
        idle = self.idle_status(_use_cached_queue=True)
        cached = self.telemetry.latest(max_age=2.5) if telemetry_alive else None
        if cached is not None:
            resources = cached
            gpu = cached["gpu"]
        else:
            gpu = self.gpu_info()
            resources = self.telemetry.sample(self.root, gpu_override=gpu)
        return {
            "protocol": 7,
            "service_revision": self.service_revision,
            "api_model_aliases": True,
            "public_api": self.public_api.snapshot(),
            "capabilities": [
                "public_api",
                "comfyui",
                "comfyui_hf_token",
                "comfyui_drive_export",
                "comfyui_tts_preview",
                "comfyui_longcat_native",
            ],
            "comfyui": comfy_status,
            "active_engine": "llama"
            if loaded
            else ("comfyui" if comfy_running else None),
            "idle": idle,
            "id": self.runtime_id,
            "state": "releasing" if quiescing else "ready",
            "gpu": gpu,
            "resources": resources,
            "location": location,
            "loaded_model": loaded,
            "active_options": active_options,
            "disk_free": resources["disk"]["free_bytes"],
            "idle_remaining": idle["remaining_seconds"],
            "idle_minutes": idle_minutes,
            "tasks": [t.snapshot() for t in tasks],
            "drive": self.drive.status(dirty_count),
            "deadline": deadline,
        }

    def detect_location(self) -> dict | None:
        detected = probe_vm_location(self.http)
        if not detected:
            return self.location
        with self.lock:
            existing_zone = (self.location or {}).get("zone") or ""
            existing_country = (self.location or {}).get("country") or ""
            if (
                existing_zone
                and not detected.get("zone")
                and existing_country == detected.get("country")
            ):
                detected["zone"] = existing_zone
            self.location = detected
            try:
                atomic_json(self.root / "location.json", detected)
            except OSError:
                pass
            return copy.deepcopy(self.location)

    @staticmethod
    def gpu_info():
        return ColabOneResourceSampler.gpu_info()

    def installed(self, model):
        directory = self.models_dir / model.id
        try:
            receipt = json.loads(
                (directory / "verified.json").read_text(encoding="utf-8")
            )
            return all(
                receipt.get(f.path) == f.sha256
                and (directory / f.path).stat().st_size == f.size
                for f in model.files
            )
        except (OSError, ValueError):
            return False

    def _partial_cached_bytes(self, model) -> int:
        directory = self.models_dir / model.id
        if not directory.exists():
            return 0
        total = 0
        for f in model.files:
            target = directory / f.path
            try:
                if target.exists() and target.stat().st_size == f.size:
                    total += f.size
                    continue
            except OSError:
                pass
            partial = target.with_name(target.name + ".part")
            try:
                total += min(f.size, cached_bytes(partial, f.size, f.sha256))
            except OSError:
                pass
        return total

    @synchronized
    def list_models(self):
        out = []
        for m in self.models.values():
            size = sum(f.size for f in m.files)
            downloaded = self.installed(m)
            out.append(
                m.model_dump()
                | {
                    "api_id": resolve_colabone_api_model_id(m),
                    "downloaded": downloaded,
                    "loaded": self.loaded == m.id,
                    "size": size,
                    "cached_bytes": size if downloaded else self._partial_cached_bytes(m),
                }
            )
        return out

    def add_model(self, spec):
        m = ModelSpec.model_validate(spec)
        with self.lock:
            existing = self.models.get(m.id)
            if existing and existing != m:
                if (
                    existing.files != m.files
                    or getattr(existing, "kind", "chat") != getattr(m, "kind", "chat")
                ):
                    raise ColabOneConflictError("同名模型配置不同，请使用新模型 ID")
            if not m.api_id and existing and existing.api_id:
                m = m.model_copy(update={"api_id": existing.api_id})
            self.models[m.id] = m
            atomic_json(
                self.root / "models.json",
                [x.model_dump() for x in self.models.values()],
            )
            self._mark_dirty("models")
        return m.model_dump() | {"api_id": resolve_colabone_api_model_id(m)}

    def update_model_api_id(self, model_id: str, api_id: str):
        clean_api_id = validate_colabone_api_model_id(api_id, allow_empty=True)
        with self.lock:
            existing = self.models.get(model_id)
            if existing is None:
                raise KeyError(model_id)
            target_api_id = (
                clean_api_id
                if clean_api_id
                else self._preset_api_ids.get(model_id, "")
            )
            updated = existing.model_copy(update={"api_id": target_api_id})
            self.models[model_id] = updated
            atomic_json(
                self.root / "models.json",
                [x.model_dump() for x in self.models.values()],
            )
            self._mark_dirty("models")
        return updated.model_dump() | {"api_id": resolve_colabone_api_model_id(updated)}

    def submit(self, kind, model_id, fn):
        self._refresh_comfy_queue_if_needed()
        with self.lock:
            if self.quiescing or self.stopping.is_set():
                raise ColabOneConflictError("实例正在准备释放")
            if self.busy():
                raise ColabOneConflictError("请等待当前任务完成，或先停止它")
            # Evict oldest finished tasks once history exceeds 50 entries.
            finished_ids = [
                tid
                for tid, existing in self.tasks.items()
                if existing.state != "running"
            ]
            while len(finished_ids) >= 50:
                self.tasks.pop(finished_ids.pop(0), None)
            t = ColabOneTask(kind, model_id)
            self.tasks[t.id] = t
            self.last_active = time.time()

        def run():
            final_state = "failed"
            final_progress = None
            final_error = ""
            try:
                fn(t)
                t.check()
                final_state = "completed"
                final_progress = 1
            except ColabOneCancelledError:
                final_state = "cancelled"
            except Exception as exc:
                final_state = "failed"
                final_error = _scrub_secret_text(str(exc) or type(exc).__name__)[:500]
            finally:
                with self.lock:
                    self.last_active = time.time()
                with t.lock:
                    transfer = t.transfer
                transfer_snap = None
                if transfer is not None:
                    transfer_lock = getattr(transfer, "lock", None)
                    if transfer_lock is not None:
                        with transfer_lock:
                            transfer.phase = final_state
                    else:
                        transfer.phase = final_state
                    transfer_snap = transfer.snapshot()
                with t.lock:
                    t.state = final_state
                    t.finished_at = time.time()
                    t.elapsed_seconds = round(
                        max(0.0, time.monotonic() - t._started_monotonic), 3
                    )
                    if final_progress is not None:
                        t.progress = final_progress
                    if final_error:
                        t.error = final_error
                    if transfer is not None:
                        t.note(
                            {
                                "completed": "下载完成，全部文件 SHA-256 校验通过",
                                "cancelled": "下载已取消，保留完成的分块以便续传",
                                "failed": "下载失败：" + t.error,
                            }.get(t.state, t.state)
                        )
                    done_payload = {
                        k: getattr(t, k)
                        for k in (
                            "id",
                            "kind",
                            "model_id",
                            "state",
                            "progress",
                            "error",
                            "message",
                            "created_at",
                            "finished_at",
                            "elapsed_seconds",
                        )
                    }
                    done_payload["logs"] = list(t.logs)
                    if transfer_snap is not None:
                        done_payload["download"] = transfer_snap
                    t.emit("done", done_payload)

        threading.Thread(target=run, daemon=True).start()
        return t.snapshot()

    def download(self, model_id, token="", threads=16, load=False, options=None):
        if threads not in (16, 32):
            raise ValueError("下载线程数请选择 16 或 32")
        clean_token = _validate_hf_token(token)
        self._refresh_comfy_queue_if_needed()
        with self.lock:
            model = self.models[model_id]
            load_spec = model
            if load and options:
                if model.kind != "chat":
                    raise ValueError("上下文参数仅用于聊天模型")
                load_spec = ModelSpec.model_validate(model.model_dump() | options)
            already = self.installed(model)
            initial_kind = "load" if (load and already) else "download"

            def pipeline(t):
                try:
                    if not self.installed(model):
                        self._download(t, model, clean_token, threads)
                        if load and t.transfer:
                            transfer_lock = getattr(t.transfer, "lock", None)
                            if transfer_lock is not None:
                                with transfer_lock:
                                    t.transfer.phase = "completed"
                            else:
                                t.transfer.phase = "completed"
                            t.note("下载完成，全部文件 SHA-256 校验通过")
                            t.update(transfer=None)
                    if load:
                        t.check()
                        t.update(
                            kind="load",
                            progress=0.95,
                            message="正在将模型装载到远端 GPU 显存",
                        )
                        self._load(t, load_spec)
                except ColabOneCancelledError:
                    raise
                except Exception as exc:
                    scrubbed = _scrub_secret_text(
                        str(exc) or type(exc).__name__, token, clean_token
                    )
                    if scrubbed != str(exc):
                        raise RuntimeError(scrubbed) from None
                    raise

            return self.submit(initial_kind, model_id, pipeline)

    def _download(self, task, model, token, threads=16):
        clean_token = _validate_hf_token(token)
        directory = self.models_dir / model.id
        directory.mkdir(exist_ok=True)
        total = sum(f.size for f in model.files)
        transfer = ColabOneTransfer(total, threads)
        transfer.count = len(model.files)
        task.update(transfer=transfer)
        task.note(
            f"准备下载 {model.name}：{len(model.files)} 个文件，共 {total / 1024**3:.2f} GiB，{threads} 线程，目标为远端 GPU 磁盘"
        )
        remaining = max(0, total - self._partial_cached_bytes(model))
        free = shutil.disk_usage(directory).free
        task.note(
            f"远端可用磁盘 {free / 1024**3:.1f} GiB，本次预计新增 {remaining / 1024**3:.1f} GiB"
        )
        if free < remaining + 512 * 1024**2:
            raise RuntimeError("远端磁盘空间不足，请删除其他已下载模型")
        receipt, done = {}, 0
        for index, file in enumerate(model.files, 1):
            task.check()
            target = directory / file.path
            target.parent.mkdir(parents=True, exist_ok=True)
            partial = target.with_name(target.name + ".part")
            stats = task.transfer
            stats.file, stats.index = file.path, index
            if (
                target.exists()
                and target.stat().st_size == file.size
                and self.sha256(target, task) == file.sha256
            ):
                receipt[file.path] = file.sha256
                done += file.size
                stats.downloaded = done
                stats.resumed += file.size
                task.note(
                    f"文件 {index}/{len(model.files)} 已存在且校验通过，跳过：{file.path}"
                )
                continue
            # An invalid existing final file must not exempt this transfer from disk checks.
            if target.exists():
                target.unlink()
                task.note("发现未通过校验的文件，重新下载", "warning")
            required = max(0, file.size - cached_bytes(partial, file.size, file.sha256))
            if shutil.disk_usage(directory).free < required + 512 * 1024**2:
                raise RuntimeError("远端磁盘空间不足，请删除其他已下载模型")
            task.update(message=file.path)
            task.note(
                f"文件 {index}/{len(model.files)} · {file.path} · {file.size / 1024**3:.2f} GiB"
            )
            headers = {"Authorization": f"Bearer {clean_token}"} if clean_token else {}
            url = f"https://huggingface.co/{file.repo}/resolve/{file.revision}/{quote(file.path)}"
            if file.size > CHUNK:
                resumed_before = stats.resumed
                try:
                    parallel_file(
                        url,
                        headers,
                        partial,
                        file.size,
                        file.sha256,
                        task,
                        done,
                        threads,
                        get=None
                        if isinstance(self.http, requests.Session)
                        else self.http.get,
                    )
                except ColabOneRangeUnsupportedError:
                    task.note("下载源忽略范围请求，自动切换为单连接下载", "warning")
                    partial.unlink(missing_ok=True)
                    metadata_path(partial).unlink(missing_ok=True)
                    stats.resumed = resumed_before
                    self._download_single(url, headers, partial, file, task, done)
            else:
                self._download_single(url, headers, partial, file, task, done)
            stats.phase = "verifying"
            task.update(message="校验 SHA-256 · " + file.path)
            task.note(f"文件 {index}/{len(model.files)} 传输完成，正在校验 SHA-256")
            if (
                partial.stat().st_size != file.size
                or self.sha256(partial, task) != file.sha256
            ):
                partial.unlink(missing_ok=True)
                metadata_path(partial).unlink(missing_ok=True)
                raise RuntimeError("文件校验失败，请重新下载")
            os.replace(partial, target)
            metadata_path(partial).unlink(missing_ok=True)
            receipt[file.path] = file.sha256
            done += file.size
            stats.downloaded = done
            task.note(f"文件 {index}/{len(model.files)} 校验通过，已保存到远端")
        elapsed = max(time.monotonic() - task.transfer.started, 0.1)
        task.note(
            f"全部文件已验证：{total / 1024**3:.2f} GiB，用时 {elapsed:.1f} 秒，平均传输 {task.transfer.received / elapsed / 1024**2:.1f} MiB/s；复用 {task.transfer.resumed / 1024**2:.1f} MiB"
        )
        atomic_json(directory / "verified.json", receipt)

    def _download_single(self, url, headers, partial, file, task, done):
        if partial.is_symlink():
            raise ValueError("下载临时文件不能是符号链接")
        offset = partial.stat().st_size if partial.exists() else 0
        if offset > file.size:
            partial.unlink()
            offset = 0
        stats = task.transfer
        stats.downloaded = done + offset
        stats.resumed += offset
        stats.phase = "downloading"
        if offset == file.size:
            return
        max_attempts = 3
        active_lock = threading.Lock()
        active_response = [None]

        def _cancel_active_response():
            with active_lock:
                resp = active_response[0]
            if resp is not None:
                try:
                    resp.close()
                except Exception:
                    pass

        task_lock = getattr(task, "lock", None)
        if task_lock is not None:
            with task_lock:
                prev_cancel_hook = getattr(task, "cancel_hook", None)
                task.cancel_hook = _cancel_active_response
        else:
            prev_cancel_hook = getattr(task, "cancel_hook", None)
            task.cancel_hook = _cancel_active_response
        try:
            for attempt in range(max_attempts):
                task.check()
                if partial.is_symlink():
                    raise ValueError("下载临时文件不能是符号链接")
                req_headers = dict(headers)
                req_headers["Accept-Encoding"] = "identity"
                if offset:
                    req_headers["Range"] = f"bytes={offset}-"
                stats.active = 1
                try:
                    with self.http.get(
                        url, headers=req_headers, stream=True, timeout=(10, 30)
                    ) as response:
                        with active_lock:
                            active_response[0] = response
                        if task.cancel.is_set():
                            _cancel_active_response()
                            task.check()
                        if response.status_code == 416 and offset > 0:
                            stats.resumed = max(0, stats.resumed - offset)
                            partial.unlink(missing_ok=True)
                            offset = 0
                            stats.downloaded = done
                            continue
                        response.raise_for_status()
                        if offset and response.status_code != 206:
                            stats.resumed = max(0, stats.resumed - offset)
                            offset = 0
                        if response.status_code == 206:
                            cr = response.headers.get("Content-Range", "").strip()
                            if not cr.startswith(f"bytes {offset}-") or (
                                "/" in cr and not cr.endswith(f"/{file.size}")
                            ):
                                raise RuntimeError("下载范围响应不匹配，请重试")
                        if partial.is_symlink():
                            raise ValueError("下载临时文件不能是符号链接")
                        with partial.open("ab" if offset else "wb") as stream:
                            for block in response.iter_content(1024**2):
                                task.check()
                                if not block:
                                    continue
                                if offset + len(block) > file.size:
                                    raise RuntimeError("远端文件大于清单声明的大小")
                                stream.write(block)
                                offset += len(block)
                                stats.wire(len(block))
                                stats.downloaded = done + offset
                                task.update(progress=(done + offset) / stats.total)
                                now = time.monotonic()
                                if now - stats.last_report >= 5:
                                    stats.last_report = now
                                    view = stats.snapshot()
                                    task.note(
                                        f"进度 {100 * stats.downloaded / stats.total:.1f}% · {stats.downloaded / 1024**3:.2f}/{stats.total / 1024**3:.2f} GiB · {view['speed_bps'] / 1024**2:.1f} MiB/s · 单连接"
                                    )
                    return
                except requests.HTTPError as exc:
                    task.check()
                    status_code = getattr(
                        getattr(exc, "response", None), "status_code", None
                    )
                    if (
                        status_code not in (429, 500, 502, 503, 504)
                        or attempt + 1 >= max_attempts
                    ):
                        if status_code in (401, 403):
                            raise RuntimeError(
                                f"单连接下载鉴权失败（HTTP {status_code}），请在设置中检查 Hugging Face Token"
                            ) from None
                        raise RuntimeError(
                            f"单连接下载失败（HTTP {status_code or type(exc).__name__}）"
                        ) from None
                    task.note(
                        f"单连接下载重试 {attempt + 1}/{max_attempts - 1}（HTTP {status_code}）",
                        "warning",
                    )
                    if task.cancel.wait(2**attempt):
                        task.check()
                except requests.RequestException as exc:
                    task.check()
                    if (
                        isinstance(
                            exc,
                            (
                                requests.exceptions.InvalidHeader,
                                requests.exceptions.InvalidURL,
                            ),
                        )
                        or attempt + 1 >= max_attempts
                    ):
                        raise RuntimeError(
                            f"单连接下载失败（{type(exc).__name__}）"
                        ) from None
                    task.note(
                        f"单连接下载重试 {attempt + 1}/{max_attempts - 1}（{type(exc).__name__}）",
                        "warning",
                    )
                    if task.cancel.wait(2**attempt):
                        task.check()
                except Exception:
                    if task.cancel.is_set():
                        task.check()
                    raise
                finally:
                    with active_lock:
                        active_response[0] = None
                    stats.active = 0
        finally:
            if task_lock is not None:
                with task_lock:
                    if getattr(task, "cancel_hook", None) is _cancel_active_response:
                        task.cancel_hook = prev_cancel_hook
            elif getattr(task, "cancel_hook", None) is _cancel_active_response:
                task.cancel_hook = prev_cancel_hook

    @staticmethod
    def sha256(path, task=None):
        h = hashlib.sha256()
        with path.open("rb") as f:
            while chunk := f.read(8 * 1024**2):
                if task:
                    task.check()
                h.update(chunk)
        return h.hexdigest()

    def prepare_engine(self, task):
        config = json.loads((self.catalog / "engine.json").read_text(encoding="utf-8"))
        root = self.root / "engine" / config["tag"]
        binaries = (
            list(root.rglob("llama-server")) if (root / ".complete").exists() else []
        )
        if binaries:
            task.note("复用远端已安装的 CUDA 推理引擎")
            return binaries[0]
        root.mkdir(parents=True, exist_ok=True)
        task.note("首次加载，准备远端 CUDA 推理引擎 " + config["tag"])
        for i, asset in enumerate(config["assets"]):
            task.note(f"下载引擎组件 {i + 1}/{len(config['assets'])}")
            task.update(message="安装远端 CUDA 推理引擎")
            target = root / f"asset-{i}.tar.gz"
            try:
                with self.http.get(asset["url"], stream=True, timeout=(15, 60)) as r:
                    r.raise_for_status()
                    with target.open("wb") as out:
                        for chunk in r.iter_content(1024**2):
                            task.check()
                            out.write(chunk)
                if not asset["sha256"] or self.sha256(target, task) != asset["sha256"]:
                    raise RuntimeError("推理引擎 SHA-256 校验失败")
                task.note(f"引擎组件 {i + 1} 校验通过，正在解压")
                with tarfile.open(target) as archive:
                    archive.extractall(root, filter="data")
            finally:
                target.unlink(missing_ok=True)
        binaries = list(root.rglob("llama-server"))
        if not binaries:
            raise RuntimeError("推理引擎包中没有 llama-server")
        (root / ".complete").touch()
        task.note("远端 CUDA 推理引擎安装完成")
        return binaries[0]

    def load(self, model_id, options=None):
        self._refresh_comfy_queue_if_needed()
        with self.lock:
            m = self.models[model_id]
            if options:
                if m.kind != "chat":
                    raise ValueError("上下文参数仅用于聊天模型")
                m = ModelSpec.model_validate(m.model_dump() | options)
            if not self.installed(m):
                raise ColabOneConflictError("请先下载模型到当前远端实例")
            return self.submit("load", model_id, lambda t: self._load(t, m))

    @staticmethod
    def _is_benign_engine_log_line(line: str) -> bool:
        low = line.lower()
        return (
            "llama.cpp/pull/25655" in low
            or "no api key is set and cors allows all origins" in low
        )

    @classmethod
    def _summarize_engine_error(
        cls, log_text: str, returncode: int | None = None
    ) -> str:
        lines = [
            ln.strip()
            for ln in (log_text or "").splitlines()
            if ln.strip() and not cls._is_benign_engine_log_line(ln)
        ]
        key_markers = (
            " E ",
            "error:",
            "failed to",
            "cudamalloc failed",
            "cuda error",
            "out of memory",
            "common_fit_params:",
            "does not support",
            "does not divide",
            "unknown model architecture",
            "not a gguf",
            "invalid magic",
            "missing tensor",
            "ggml_backend",
            "ggml_assert",
            "terminate called",
            "what():",
            "assertion",
            "segmentation fault",
            "aborted",
        )
        important: list[str] = []
        for ln in lines:
            low = ln.lower()
            if any(marker in ln or marker in low for marker in key_markers):
                if not important or important[-1] != ln:
                    important.append(ln)
        selected = important[-4:] if important else lines[-4:]
        summary = " | ".join(selected)
        if len(summary) > 420:
            summary = summary[:417].rstrip() + "..."
        if returncode in (-9, 137):
            oom_hint = "推理进程被系统终止（可能因系统内存不足 OOM）"
            return f"{oom_hint}：{summary}" if summary else oom_hint
        return summary

    @staticmethod
    def _is_fatal_non_retryable_load_error(log_text: str) -> bool:
        low = (log_text or "").lower()
        if any(
            tok in low
            for tok in (
                "out of memory",
                "cudamalloc",
                "common_fit_params",
                "cache type",
                "flash_attn",
                "does not support different k",
                "does not divide",
            )
        ):
            return False
        return any(
            tok in low
            for tok in (
                "unknown model architecture",
                "not a gguf file",
                "invalid magic",
                "gguf_init_from_file: failed to open",
                "failed to open gguf",
                "missing tensor",
            )
        )

    @staticmethod
    def _plan_context_length(args: list[str], default_ctx: int) -> int:
        try:
            idx = args.index("--ctx-size")
            return int(args[idx + 1])
        except (ValueError, IndexError):
            return default_ctx

    def host_memory_mib(self) -> int:
        info = ColabOneResourceSampler.memory_info()
        avail = int(info.get("available_bytes") or 0)
        return avail // (1024 * 1024)

    def _build_llama_load_plans(
        self, engine: Path, m: ModelSpec, model_path: Path, gpu: dict
    ) -> tuple[bool, list[tuple[str, list[str]]]]:
        """Build ordered scenario-based llama-server launch plans.

        Verified against llama.cpp (b12032) `common/fit.cpp`:
        - `common_fit_params()` aborts immediately if `n_gpu_layers != -1`
          (`auto`) OR if `tensor_buft_overrides` is non-empty (`-ot`,
          `--n-cpu-moe`, `--cpu-moe`).
        - Large PLE tensors (`per_layer_token_embd` > 4 GiB) are handled
          natively by `--lazy-mode auto` (CPU mmap lazy read) before tensor
          overrides run, while `--fit on` natively balances MoE experts and
          dense layers across VRAM and Host RAM.
        - MLA architectures (DeepSeek / GLM-5 / Kimi) reject asymmetric K/V
          cache types (`type_k != type_v`).
        - Hybrid recurrent/SWA models (e.g. Qwen3.8-Flash-Next Gated DeltaNet)
          benefit from finer context checkpoints (`--checkpoint-min-step` scaled
          to cover the full context within 32 checkpoints) and larger host RAM
          prompt cache (`--cache-ram 16384` only when available RAM headroom
          *after* CPU-offloaded weights is >= 48 GiB). High-VRAM hybrid MoE runs
          also use `-ub 1024` on Plan 1 to halve CPU-resident expert scans.
        """
        model_bytes = sum(f.size for f in m.files)
        model_mib = model_bytes / (1024 * 1024)
        gpu_total_mib = int(gpu.get("total_mib") or 0)
        gpu_free_mib = max(0, gpu_total_mib - int(gpu.get("used_mib") or 0))
        host_avail_mib = self.host_memory_mib()
        needs_hybrid = bool(m.cpu_moe_layers) or (
            model_mib > 0 and (model_mib * 1.08 + 1024) > gpu_free_mib
        )
        identity_text = " ".join(
            [
                m.id,
                m.name,
                m.description,
                *(f.repo for f in m.files),
                *(f.path for f in m.files),
            ]
        ).lower()
        is_mla_arch = any(
            tok in identity_text
            for tok in ("deepseek", "glm-5", "glm5", "kimi", "mla")
        )
        is_ple_hybrid = needs_hybrid and any(
            tok in identity_text
            for tok in (
                "flash-next",
                "flash_next",
                "deepseek-v4",
                "deepseek_v4",
                "deepseek4",
                "gemma-4",
                "gemma4",
            )
        )
        # Exclude random hf-<16-hex> custom import IDs from architecture token matching so
        # hex substrings like "a3b" or "a6b" never falsely classify dense models as MoE.
        arch_parts = [
            m.name,
            m.description,
            *(f.repo for f in m.files),
            *(f.path for f in m.files),
        ]
        if not m.id.lower().startswith("hf-"):
            arch_parts.insert(0, m.id)
        arch_text = " ".join(arch_parts).lower()
        is_moe_arch = (
            bool(m.cpu_moe_layers)
            or bool(re.search(r"(?:^|[^a-z0-9])(moe|a\d+b)(?:[^a-z0-9]|$)", arch_text))
            or any(
                tok in arch_text
                for tok in (
                    "flash-next",
                    "flash_next",
                    "qwen3-next",
                    "qwen3_next",
                    "qwen3.8",
                    "mixtral",
                    "deepseek-v3",
                    "deepseek_v3",
                    "deepseek-v4",
                    "deepseek_v4",
                    "deepseek4",
                    "glm-4.5",
                    "glm-5",
                    "glm5",
                    "kimi-k2",
                    "kimi_k2",
                )
            )
        )
        # Reserve headroom for CUDA 12.8 / cuBLAS / Blackwell runtime workspace.
        if gpu_total_mib >= 75000:
            primary_margin_mib, wide_margin_mib = 2048, 4096
        elif gpu_total_mib >= 36000:
            primary_margin_mib, wide_margin_mib = 1536, 2560
        else:
            primary_margin_mib, wide_margin_mib = 1024, 1536

        # Size host RAM prompt cache strictly from headroom remaining AFTER estimated
        # CPU-offloaded model weights so lazy cache growth never triggers mid-session OOM.
        est_cpu_model_mib = max(0, int(model_mib - gpu_free_mib * 0.90))
        ram_headroom_mib = (
            host_avail_mib - est_cpu_model_mib if host_avail_mib > 0 else 0
        )
        if ram_headroom_mib >= 48 * 1024:
            tuned_cache_ram_mib: int | None = 16384
        elif needs_hybrid and host_avail_mib > 0 and ram_headroom_mib < 16 * 1024:
            tuned_cache_ram_mib = 4096
        else:
            tuned_cache_ram_mib = None

        # Keep checkpoints fine enough to capture static system+tool prefixes while covering
        # the full context window within llama-server's default 32 checkpoints; avoid extra
        # checkpoint churn when host RAM headroom is tight.
        def compute_checkpoint_step(ctx: int) -> int | None:
            if ctx < 8192:
                return None
            if host_avail_mib > 0 and ram_headroom_mib < 16 * 1024:
                return None
            return max(2048, ctx // 32)

        # Halve CPU MoE expert weight scans during prefill on 80-96 GiB hybrid MoE runs
        # only (dense models get minimal PP gain from -ub 1024 while paying extra VRAM scratch
        # that would cause --fit on to spill extra layers to CPU).
        primary_ubatch_size = (
            1024
            if (
                needs_hybrid
                and is_moe_arch
                and gpu_total_mib >= 75000
                and m.context_length >= 8192
            )
            else None
        )

        def make_fit_cmd(
            *,
            ctx_size: int = m.context_length,
            kv_k: str | None = "q8_0",
            kv_v: str | None = "q8_0",
            flash_attn: str | None = "on",
            kv_unified: bool = True,
            fit_target_mib: int | None = primary_margin_mib,
            batch_size: int | None = None,
            ubatch_size: int | None = None,
            cache_ram_mib: int | None = tuned_cache_ram_mib,
            use_checkpoint_tuning: bool = True,
        ) -> list[str]:
            cmd = [
                str(engine),
                "--host",
                "127.0.0.1",
                "--port",
                "8081",
                "--model",
                str(model_path),
                "--alias",
                m.id,
                "--ctx-size",
                str(ctx_size),
                "--n-gpu-layers",
                "auto",
                "--fit",
                "on",
            ]
            if fit_target_mib is not None:
                cmd.extend(["--fit-target", str(fit_target_mib)])
            if flash_attn:
                cmd.extend(["--flash-attn", flash_attn])
            if kv_k and kv_v:
                cmd.extend(["--cache-type-k", kv_k, "--cache-type-v", kv_v])
            if kv_unified:
                cmd.append("--kv-unified")
            if batch_size and ubatch_size:
                cmd.extend(
                    ["--batch-size", str(batch_size), "--ubatch-size", str(ubatch_size)]
                )
            if cache_ram_mib:
                cmd.extend(["--cache-ram", str(cache_ram_mib)])
            cms = compute_checkpoint_step(ctx_size) if use_checkpoint_tuning else None
            if cms:
                cmd.extend(["--checkpoint-min-step", str(cms)])
            cmd.extend(["--parallel", "1", "--jinja"])
            return cmd

        plans: list[tuple[str, list[str]]] = []

        # Scenario 1: Primary optimal auto-fit (100% GPU residency when fitting in
        # VRAM; native MoE/dense VRAM+RAM fit + native lazy-mmap PLE when oversized).
        if is_ple_hybrid:
            primary_label = "GPU 显存 + 内存 PLE 协同装配（--fit on · KV q8_0）"
        elif needs_hybrid:
            primary_label = "GPU 显存 + 系统内存智能混合装配（--fit on · KV q8_0）"
        else:
            primary_label = "全量 GPU 驻留（FlashAttn · --fit on · KV q8_0）"
        plans.append(
            (
                primary_label,
                make_fit_cmd(
                    kv_k="q8_0",
                    kv_v="q8_0",
                    batch_size=2048 if primary_ubatch_size else None,
                    ubatch_size=primary_ubatch_size,
                ),
            )
        )

        # Scenario 2: Tight-VRAM KV compression + wider CUDA workspace margin.
        # Skip asymmetric KV on MLA models where llama.cpp requires type_k == type_v.
        if not is_mla_arch:
            plans.append(
                (
                    "GPU + 内存智能混合装配（--fit on · 非对称 KV q4_0/q8_0）",
                    make_fit_cmd(
                        kv_k="q4_0",
                        kv_v="q8_0",
                        fit_target_mib=wide_margin_mib,
                    ),
                )
            )
        plans.append(
            (
                "GPU + 内存智能混合装配（--fit on · 紧凑 KV q4_0）",
                make_fit_cmd(
                    kv_k="q4_0",
                    kv_v="q4_0",
                    fit_target_mib=wide_margin_mib,
                ),
            )
        )

        # Scenario 3: Architecture-compatible native f16 KV + auto FlashAttn with
        # --fit on (full-context compatibility plan omitting optional cache/checkpoint tuning).
        plans.append(
            (
                "GPU + 内存标准 KV 兼容装配（--fit on · KV f16）",
                make_fit_cmd(
                    kv_k=None,
                    kv_v=None,
                    flash_attn="auto",
                    fit_target_mib=wide_margin_mib,
                    cache_ram_mib=None,
                    use_checkpoint_tuning=False,
                ),
            )
        )

        # Scenario 4: Adaptive 4K context downscale when user requested >4K context
        # on constrained hardware where full-context KV still exceeds free VRAM.
        if m.context_length > 4096:
            plans.append(
                (
                    "GPU + 内存保守上下文装配（--fit on · 4K · KV q4_0）",
                    make_fit_cmd(
                        ctx_size=4096,
                        kv_k="q4_0",
                        kv_v="q4_0",
                        fit_target_mib=wide_margin_mib,
                        cache_ram_mib=None,
                        use_checkpoint_tuning=False,
                    ),
                )
            )

        # Scenario 5: Preset manual MoE expert CPU offload fallback (explicit
        # --fit off so --n-cpu-moe never conflicts with common_fit_params).
        if m.cpu_moe_layers:
            manual_moe_cmd = [
                str(engine),
                "--host",
                "127.0.0.1",
                "--port",
                "8081",
                "--model",
                str(model_path),
                "--alias",
                m.id,
                "--ctx-size",
                str(m.context_length),
                "--n-gpu-layers",
                "all",
                "--fit",
                "off",
                "--flash-attn",
                "auto",
                "--n-cpu-moe",
                str(m.cpu_moe_layers),
            ]
            if tuned_cache_ram_mib:
                manual_moe_cmd.extend(["--cache-ram", str(tuned_cache_ram_mib)])
            manual_cms = compute_checkpoint_step(m.context_length)
            if manual_cms:
                manual_moe_cmd.extend(["--checkpoint-min-step", str(manual_cms)])
            manual_moe_cmd.extend(["--parallel", "1", "--jinja"])
            plans.append(
                (
                    f"GPU + 内存 MoE 手动卸载回退（--fit off · {m.cpu_moe_layers} 层专家内存卸载）",
                    manual_moe_cmd,
                )
            )

        # Scenario 6: Minimal compatibility mode (omits KV quant / FA / unified-KV /
        # cache tuning while retaining --n-gpu-layers auto + --fit on and bounded 4K ctx
        # so even models rejecting optional tuning flags can load).
        compat_ctx = min(m.context_length, 4096)
        plans.append(
            (
                "兼容模式装载",
                make_fit_cmd(
                    ctx_size=compat_ctx,
                    kv_k=None,
                    kv_v=None,
                    flash_attn=None,
                    kv_unified=False,
                    fit_target_mib=None,
                    cache_ram_mib=None,
                    use_checkpoint_tuning=False,
                ),
            )
        )
        return needs_hybrid, plans

    def _load(self, task, m):
        if self.comfy.is_running():
            task.note("正在停止 ComfyUI 服务以释放显存")
        self.comfy.stop_server()
        if self.loaded or self.proc:
            prev_id = self.loaded
            if prev_id == m.id:
                task.note(f"正在重新装载 {m.name}，先卸载当前已装载模型以释放显存")
            elif prev_id:
                prev_model = self.models.get(prev_id)
                prev_label = prev_model.name if prev_model else prev_id
                task.note(f"正在卸载当前模型 {prev_label} 以释放显存")
            self._stop_engine()
        task.note(f"准备加载 {m.name}，上下文 {m.context_length} tokens")
        gpu = self.gpu_info()
        if not gpu["total_mib"]:
            raise RuntimeError("未检测到可用远端 GPU")
        if gpu["total_mib"] < m.min_vram_mib:
            raise RuntimeError(f"模型需要至少 {m.min_vram_mib} MiB 显存")
        model_gib = sum(f.size for f in m.files) / (1024**3)
        task.note(
            f"远端设备 {gpu.get('name', 'GPU')}，显存 {gpu['total_mib'] / 1024:.1f} GiB"
            + (f"，模型权重 {model_gib:.1f} GiB" if model_gib >= 0.5 else "")
        )
        engine = self.prepare_engine(task)
        task.check()
        file = next(f for f in m.files if f.role == "model")
        model_path = self.models_dir / m.id / file.path
        env = dict(os.environ)
        engine_base = self.root / "engine"
        try:
            rel = engine.resolve().relative_to(engine_base.resolve())
            lib_search_root = (
                engine_base / rel.parts[0] if len(rel.parts) >= 2 else engine.parent
            )
        except ValueError:
            lib_search_root = engine.parent
        lib_dirs = (
            {str(p.parent) for p in lib_search_root.rglob("*.so*")}
            if lib_search_root.is_dir()
            else set()
        )
        ld_parts = sorted(lib_dirs)
        if env.get("LD_LIBRARY_PATH"):
            ld_parts.append(env["LD_LIBRARY_PATH"])
        if ld_parts:
            env["LD_LIBRARY_PATH"] = ":".join(ld_parts)
        needs_hybrid, plans = self._build_llama_load_plans(engine, m, model_path, gpu)
        if needs_hybrid:
            task.note(
                "模型体积大于当前显存，已自动启用 GPU 显存 + 系统内存混合装载（--fit on），溢出权重将自动分配至内存"
            )
        log = self.root / "engine.log"
        last_error = "模型加载失败"
        for attempt_idx, (plan_label, args) in enumerate(plans):
            task.check()
            task.note(f"启动 llama.cpp：{plan_label}")
            effective_ctx = self._plan_context_length(args, m.context_length)
            with log.open("wb") as stream:
                proc = self._start_engine(
                    task,
                    args,
                    env=env,
                    stdout=stream,
                    stderr=subprocess.STDOUT,
                    stdin=subprocess.DEVNULL,
                )
            raw_log = ""
            attempt_detail = ""

            def _read_engine_log() -> str:
                try:
                    return log.read_text(errors="replace")[-32768:]
                except OSError:
                    return ""

            try:
                load_started = time.monotonic()
                load_last_note = load_started
                deadline = load_started + 600
                exited_early = False
                while time.monotonic() < deadline:
                    task.check()
                    if proc.poll() is not None:
                        exited_early = True
                        raw_log = _read_engine_log()
                        attempt_detail = self._summarize_engine_error(
                            raw_log, proc.returncode
                        )
                        last_error = (
                            f"模型加载失败（推理进程已退出，退出码 {proc.returncode}）"
                            + (f"：{attempt_detail}" if attempt_detail else "")
                        )
                        break
                    try:
                        if (
                            self.http.get(
                                "http://127.0.0.1:8081/health", timeout=2
                            ).status_code
                            == 200
                        ):
                            task.note(
                                f"模型权重已就绪（{plan_label}，耗时 {time.monotonic() - load_started:.1f} 秒）"
                            )
                            warmup_fatal = self._warmup_llama(task, proc, m.id)
                            if proc.poll() is not None:
                                exited_early = True
                                raw_log = _read_engine_log()
                                attempt_detail = self._summarize_engine_error(
                                    raw_log, proc.returncode
                                )
                                last_error = (
                                    f"模型预热失败（推理进程已退出，退出码 {proc.returncode}）"
                                    + (f"：{attempt_detail}" if attempt_detail else "")
                                )
                                break
                            if warmup_fatal:
                                exited_early = True
                                raw_log = _read_engine_log()
                                attempt_detail = (
                                    self._summarize_engine_error(raw_log)
                                    or warmup_fatal
                                )
                                last_error = f"模型预热失败：{attempt_detail}"
                                break
                            if effective_ctx != m.context_length:
                                task.note(
                                    f"当前显存紧张，上下文已自动调整为 {effective_ctx} tokens 以完成装载",
                                    "warning",
                                )
                            self._engine_ready(
                                task,
                                proc,
                                m.id,
                                {
                                    "context_length": effective_ctx,
                                    "temperature": m.temperature,
                                },
                            )
                            task.note("模型加载成功，远端推理服务已就绪")
                            return
                    except requests.RequestException:
                        pass
                    now = time.monotonic()
                    if now - load_last_note >= 10:
                        load_last_note = now
                        task.note(
                            f"正在载入模型权重（已耗时 {int(now - load_started)} 秒）"
                        )
                    task.update(
                        message="混合装载到显存与内存" if needs_hybrid else "加载到远端显存"
                    )
                    if task.cancel.wait(1):
                        task.check()
                if not exited_early:
                    raise RuntimeError("模型加载超过 10 分钟")
            except BaseException:
                self._stop_engine()
                raise
            self._stop_engine()
            if self._is_fatal_non_retryable_load_error(raw_log):
                break
            if attempt_idx + 1 < len(plans):
                if attempt_detail:
                    task.note(
                        f"方案「{plan_label}」未启动成功（{attempt_detail}），自动尝试下一降级装载方案…",
                        "warning",
                    )
                else:
                    task.note(
                        f"方案「{plan_label}」未启动成功，自动尝试下一降级装载方案…",
                        "warning",
                    )
        raise RuntimeError(last_error)

    def _warmup_llama(self, task, proc, model_id: str) -> str:
        """Run a 1-token warmup so first-call CUDA PTX JIT finishes during load instead of timing out the first API request.

        Returns a non-empty fatal reason if warmup hit a GPU/memory runtime error
        while the process stayed alive so `_load()` can retry a safer fallback plan.
        """
        use_dedicated_session = isinstance(self.http, requests.Session)
        post = None if use_dedicated_session else getattr(self.http, "post", None)
        if not use_dedicated_session and not callable(post):
            return ""
        warmup_started = time.monotonic()
        task.update(message="正在预热 GPU 推理内核")
        task.note("正在预热远端 CUDA 推理内核，避免首次 API 调用超时")
        done = threading.Event()
        outcome: dict[str, object] = {"status": None, "body": "", "exc": None}

        def _fire():
            session = None
            try:
                if use_dedicated_session:
                    session = requests.Session()
                    session.trust_env = False
                    resp = session.post(
                        "http://127.0.0.1:8081/v1/chat/completions",
                        json={
                            "model": model_id,
                            "messages": [{"role": "user", "content": "."}],
                            "max_tokens": 1,
                            "stream": False,
                        },
                        timeout=(5, 180),
                    )
                else:
                    resp = post(
                        "http://127.0.0.1:8081/v1/chat/completions",
                        json={
                            "model": model_id,
                            "messages": [{"role": "user", "content": "."}],
                            "max_tokens": 1,
                            "stream": False,
                        },
                        timeout=(5, 180),
                    )
                status = getattr(resp, "status_code", None)
                if isinstance(status, int):
                    outcome["status"] = status
                text = getattr(resp, "text", "")
                if isinstance(text, str):
                    outcome["body"] = text[:500]
            except Exception as exc:
                outcome["exc"] = exc
            finally:
                if session is not None:
                    try:
                        session.close()
                    except Exception:
                        pass
                done.set()

        threading.Thread(target=_fire, daemon=True).start()
        end = time.monotonic() + 185
        while not done.is_set() and time.monotonic() < end:
            task.check()
            if proc.poll() is not None:
                return ""
            done.wait(0.25)
        if not done.is_set():
            if proc.poll() is None:
                task.note("CUDA 推理内核预热超时，推理服务将继续就绪", "warning")
            return ""
        if outcome["exc"] is not None:
            # Give a crashing engine a brief moment to terminate so proc.poll() catches it.
            deadline = time.monotonic() + 0.25
            while proc.poll() is None and time.monotonic() < deadline:
                time.sleep(0.05)
            return ""
        status = outcome["status"]
        body = str(outcome["body"] or "")
        if isinstance(status, int) and status >= 500:
            low = body.lower()
            if any(
                tok in low
                for tok in ("out of memory", "cudamalloc", "cuda error", "failed to alloc")
            ):
                return body.strip()[:300] or f"HTTP {status}"
        if proc.poll() is None:
            task.note(
                f"CUDA 推理内核预热完成（耗时 {time.monotonic() - warmup_started:.1f} 秒）"
            )
        return ""

    def _start_engine(self, task, args, **kwargs):
        with self._engine_lifecycle_lock:
            with self.lock:
                task.check()
                if self.quiescing or self.stopping.is_set():
                    raise ColabOneCancelledError("实例正在停止，已取消模型加载")
            self._stop_engine()
            with self.lock:
                task.check()
                if self.quiescing or self.stopping.is_set():
                    raise ColabOneCancelledError("实例正在停止，已取消模型加载")
                self.proc = subprocess.Popen(args, **kwargs)
                return self.proc

    @synchronized
    def _engine_ready(self, task, proc, model_id, options=None):
        task.check()
        if self.quiescing or self.stopping.is_set():
            raise ColabOneCancelledError("实例正在停止，已取消模型加载")
        if self.proc is not proc or proc.poll() is not None:
            raise RuntimeError("推理进程已退出，模型未加载成功，请查看运行日志")
        self.loaded, self.active_options = model_id, options or {}

    def _stop_engine(self):
        with self._engine_lifecycle_lock:
            with self.lock:
                proc = self.proc
                self.proc, self.loaded = None, None
                self.active_options = {}
            if proc:
                if proc.poll() is None:
                    proc.terminate()
                try:
                    proc.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    try:
                        proc.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        pass

    def unload(self):
        self._refresh_comfy_queue_if_needed()
        with self._engine_lifecycle_lock:
            with self.lock:
                if self.busy():
                    raise ColabOneConflictError("请先停止当前任务")
                comfy_running = self.comfy.is_running()
            self._stop_engine()
        if comfy_running:
            self.comfy.free_memory(unload_models=True)
        with self.lock:
            self.last_active = time.time()

    def start_comfyui(self):
        with self._comfy_lifecycle_lock:
            self._refresh_comfy_queue_if_needed()
            with self.lock:
                return self.submit(
                    "comfyui_start", "", lambda t: self.comfy.start_server(t)
                )

    def stop_comfyui(self):
        def _is_task_running(task) -> bool:
            t_lock = getattr(task, "lock", None)
            if t_lock is not None:
                with t_lock:
                    return task.state == "running"
            return task.state == "running"

        with self._comfy_lifecycle_lock:
            with self.lock:
                comfy_tasks = [
                    t
                    for t in self.tasks.values()
                    if _is_task_running(t)
                    and t.kind in ("comfyui_start", "comfyui_run", "comfyui_tts_preview")
                ]
            for t in comfy_tasks:
                try:
                    t.request_cancel()
                except Exception:
                    pass
            self.comfy.stop_server()
            deadline = time.monotonic() + 10
            while any(_is_task_running(t) for t in comfy_tasks):
                if time.monotonic() >= deadline:
                    break
                time.sleep(0.05)
            if comfy_tasks and self.comfy.is_running():
                self.comfy.stop_server()
            if any(_is_task_running(t) for t in comfy_tasks):
                raise ColabOneConflictError("ComfyUI 任务仍在停止，请稍后重试")
            with self.lock:
                self.last_active = time.time()

    def free_comfyui(self):
        with self._comfy_lifecycle_lock:
            self._refresh_comfy_queue_if_needed()
            with self.lock:
                if self.quiescing or self.stopping.is_set():
                    raise ColabOneConflictError("实例正在准备释放")
                if self.busy():
                    raise ColabOneConflictError("请先等待当前任务完成，或先停止它")
            result = self.comfy.free_memory(unload_models=True)
            with self.lock:
                self.last_active = time.time()
            return result if result is not None else {"ok": True}

    def run_comfyui_workflow(self, request_data: dict | WorkflowRunRequest):
        raw_token_secret = (
            request_data.get("hf_token", "")
            if isinstance(request_data, dict)
            and isinstance(request_data.get("hf_token"), str)
            else ""
        )
        try:
            req = (
                request_data
                if isinstance(request_data, WorkflowRunRequest)
                else WorkflowRunRequest.model_validate(request_data)
            )
        except Exception as exc:
            raw_msg = str(exc) or type(exc).__name__
            scrubbed = _scrub_secret_text(raw_msg, raw_token_secret)
            if scrubbed != raw_msg:
                raise ValueError(scrubbed) from None
            raise
        with self._comfy_lifecycle_lock:
            self._refresh_comfy_queue_if_needed()
            with self.lock:
                if hasattr(self.comfy.store, "get_workflow"):
                    if self.comfy.store.get_workflow(req.workflow_id) is None:
                        raise KeyError("工作流不存在")
                else:
                    if not any(
                        w.get("id") == req.workflow_id
                        for w in self.comfy.store.list_workflows()
                    ):
                        raise KeyError("工作流不存在")
                token_secret = getattr(req, "hf_token", "") or ""

                def _execute(task):
                    try:
                        return self.comfy.run_workflow(task, req)
                    except ColabOneCancelledError:
                        raise
                    except Exception as exc:
                        raw_msg = str(exc) or type(exc).__name__
                        scrubbed = _scrub_secret_text(
                            raw_msg, raw_token_secret, token_secret
                        )
                        if scrubbed != raw_msg:
                            raise RuntimeError(scrubbed) from None
                        raise

                return self.submit(
                    "comfyui_run",
                    req.workflow_id,
                    _execute,
                )

    def preview_comfyui_tts(
        self, request_data: dict | ComfyTtsPreviewRequest
    ) -> dict:
        with self.lock:
            if self.quiescing or self.stopping.is_set():
                raise ColabOneConflictError("实例正在准备释放")

        raw_token_secret = (
            request_data.get("hf_token", "")
            if isinstance(request_data, dict)
            and isinstance(request_data.get("hf_token"), str)
            else ""
        )
        token_secret = ""
        try:
            req = (
                request_data
                if isinstance(request_data, ComfyTtsPreviewRequest)
                else ComfyTtsPreviewRequest.model_validate(request_data)
            )
            token_secret = getattr(req, "hf_token", "") or ""
            resolved = self.comfy._resolve_preview_inputs(req)
        except Exception as exc:
            raw_msg = str(exc) or type(exc).__name__
            scrubbed = _scrub_secret_text(raw_msg, raw_token_secret, token_secret)
            if scrubbed != raw_msg:
                raise ValueError(scrubbed) from None
            raise

        def _finalize_cache_hit(cached_item: dict) -> dict:
            with self.lock:
                if self.quiescing or self.stopping.is_set():
                    raise ColabOneConflictError("实例正在准备释放")
                self.touch()
            if int(cached_item.get("size") or 0) > 512 * 1024:
                trimmed = dict(cached_item)
                trimmed.pop("data", None)
                return trimmed
            return cached_item

        def _lookup_cached_preview(preview_id: str) -> dict | None:
            getter = self.comfy.tts.get_cached_preview
            accepts_max_inline = False
            try:
                sig = inspect.signature(getter)
                param = sig.parameters.get("max_inline_bytes")
                accepts_max_inline = (
                    param is not None
                    and param.kind
                    in (
                        inspect.Parameter.POSITIONAL_OR_KEYWORD,
                        inspect.Parameter.KEYWORD_ONLY,
                    )
                ) or any(
                    p.kind == inspect.Parameter.VAR_KEYWORD
                    for p in sig.parameters.values()
                )
            except (TypeError, ValueError):
                accepts_max_inline = False
            if accepts_max_inline:
                return getter(preview_id, max_inline_bytes=512 * 1024)
            return getter(preview_id)

        cached = _lookup_cached_preview(resolved["preview_id"])
        if cached is not None:
            return _finalize_cache_hit(cached)

        with self._comfy_lifecycle_lock:
            self._refresh_comfy_queue_if_needed()
            cached = _lookup_cached_preview(resolved["preview_id"])
            if cached is not None:
                return _finalize_cache_hit(cached)
            with self.lock:
                if self.quiescing or self.stopping.is_set():
                    raise ColabOneConflictError("实例正在准备释放")
                if self.busy():
                    raise ColabOneConflictError("请等待当前任务完成，或先停止它")

                def _execute(task):
                    try:
                        return self.comfy.run_tts_preview(task, req)
                    except ColabOneCancelledError:
                        raise
                    except Exception as exc:
                        raw_msg = str(exc) or type(exc).__name__
                        scrubbed = _scrub_secret_text(
                            raw_msg, raw_token_secret, token_secret
                        )
                        if scrubbed != raw_msg:
                            raise RuntimeError(scrubbed) from None
                        raise

                task = self.submit(
                    "comfyui_tts_preview",
                    resolved["engine_id"],
                    _execute,
                )

        return {
            "cached": False,
            "preview_id": resolved["preview_id"],
            "engine_id": resolved["engine_id"],
            "voice_preset": resolved["effective_voice_preset"],
            "sample_rate": resolved["sample_rate"],
            "text": resolved["resolved_text"],
            "task": task,
        }

    def delete(self, model_id):
        self._refresh_comfy_queue_if_needed()
        with self.lock:
            if self.busy() or self.loaded == model_id:
                raise ColabOneConflictError("请先停止任务并卸载此模型")
            # model_id comes from the validated catalog; callers cannot choose a path.
            m = self.models[model_id]
            shutil.rmtree(self.models_dir / m.id, ignore_errors=True)

    def connect_drive(self) -> dict:
        with self.sync_lock:
            is_drivefs = getattr(self.drive, "is_colab_drivefs_mounted", lambda: False)()
            if (
                self.drive.available()
                and self.drive_restored
                and (not is_drivefs or self.drive.mode == "colab_drivefs")
            ):
                if self._dirty_seq:
                    self._sync_to_drive()
                return self.drive.status(len(self._dirty_seq))
            self.drive.connect()
            restored = self._restore_from_drive()
            if restored and self._dirty_seq:
                self._sync_to_drive()
            return self.drive.status(len(self._dirty_seq))

    def sync_drive(self) -> dict:
        with self.sync_lock:
            if not self.drive.available():
                if self.drive.configured():
                    self.drive.connect()
                else:
                    raise RuntimeError("当前实例尚未配置 Google 云盘凭据")
            if not self.drive_restored:
                if not self._restore_from_drive():
                    return {"ok": False, **self.drive.status(len(self._dirty_seq))}
            ok = self._sync_to_drive()
            return {"ok": ok, **self.drive.status(len(self._dirty_seq))}

    def export_comfy_output_to_drive(self, output_id: str) -> dict:
        if not isinstance(output_id, str) or not re.fullmatch(r"[a-f0-9]{32}", output_id):
            raise ValueError("产物编号不合法")
        self.touch(hold_seconds=210)
        with self.comfy.store.lock:
            item = self.comfy.store.outputs[output_id]
            ext = item["extension"]
            filename = item["filename"]
            sha256 = item["sha256"]
            size = int(item["size"])
            src_path = self.comfy.store.outputs_dir / f"{output_id}.{ext}"
            if src_path.is_symlink():
                raise ValueError("作品不能是符号链接")
            if not src_path.is_file():
                raise KeyError(output_id)

        if not self.drive.available():
            if self.drive.configured():
                self.connect_drive()
        if not self.drive.available():
            raise ValueError("当前实例尚未连接 Google 云盘，请先完成云盘授权与挂载")

        try:
            result = self.drive.export_output_file(
                src_path,
                filename=filename,
                output_id=output_id,
                extension=ext,
                expected_sha256=sha256,
                expected_size=size,
            )
        except FileNotFoundError:
            raise KeyError(output_id) from None
        self.touch()
        return {"id": output_id, **result}

    def _restore_from_drive(self) -> bool:
        stage_dir = self.root / ".drive-pull"
        shutil.rmtree(stage_dir, ignore_errors=True)
        try:
            if not self.drive.pull_workspace(stage_dir):
                return False
            pulled_models = []
            if (stage_dir / "models.json").is_file():
                try:
                    raw_pulled = json.loads(
                        (stage_dir / "models.json").read_text(encoding="utf-8")
                    )
                except (OSError, ValueError):
                    raw_pulled = []
                if isinstance(raw_pulled, list):
                    for raw_model in raw_pulled:
                        try:
                            pulled_models.append(ModelSpec.model_validate(raw_model))
                        except ValueError:
                            continue
            with self.lock:
                local_changed = False
                for m in pulled_models:
                    if m.id not in self.models:
                        self.models[m.id] = m
                        local_changed = True
                    elif (
                        m.api_id
                        and m.files == self.models[m.id].files
                        and getattr(m, "kind", "chat")
                        == getattr(self.models[m.id], "kind", "chat")
                        and self.models[m.id].api_id != m.api_id
                    ):
                        self.models[m.id] = self.models[m.id].model_copy(
                            update={"api_id": m.api_id}
                        )
                        local_changed = True
                if local_changed:
                    atomic_json(
                        self.root / "models.json",
                        [x.model_dump() for x in self.models.values()],
                    )
            with self.comfy.store.lock:
                workflows_stage = stage_dir / "workflows"
                if workflows_stage.is_dir():
                    dst_workflows = self.root / "workflows"
                    if not dst_workflows.is_symlink():
                        dst_workflows.mkdir(parents=True, exist_ok=True)
                        for wf_path in workflows_stage.glob("*.json"):
                            try:
                                if wf_path.is_symlink() or not wf_path.is_file():
                                    continue
                                raw_wf = json.loads(wf_path.read_text(encoding="utf-8"))
                                if not isinstance(raw_wf, dict):
                                    continue
                                raw_wf["source"] = "custom"
                                remote_data = WorkflowTemplate.model_validate(
                                    raw_wf
                                ).model_dump()
                                if (
                                    remote_data["id"] != wf_path.stem
                                    or remote_data["id"] in self.comfy.store.builtins
                                ):
                                    continue
                                dst_wf = dst_workflows / wf_path.name
                                if dst_wf.is_symlink():
                                    continue
                                if dst_wf.is_file():
                                    local_data = json.loads(
                                        dst_wf.read_text(encoding="utf-8")
                                    )
                                    if local_data.get(
                                        "updated_at", 0
                                    ) >= remote_data.get("updated_at", 0):
                                        continue
                                atomic_json(dst_wf, remote_data)
                            except (OSError, ValueError, KeyError):
                                continue
                outputs_stage = stage_dir / "comfyui-outputs"
                if outputs_stage.is_dir():
                    dst_outputs = self.root / "comfyui-outputs"
                    if not dst_outputs.is_symlink():
                        dst_outputs.mkdir(parents=True, exist_ok=True)
                        for meta_path in outputs_stage.glob("*.json"):
                            try:
                                if meta_path.is_symlink() or not meta_path.is_file():
                                    continue
                                rec = WorkflowOutputRecord.model_validate_json(
                                    meta_path.read_text(encoding="utf-8")
                                ).model_dump()
                                ident = rec["id"]
                                ext = rec["extension"]
                                asset_src = outputs_stage / f"{ident}.{ext}"
                                if (
                                    ident != meta_path.stem
                                    or not asset_src.is_file()
                                    or asset_src.is_symlink()
                                    or asset_src.stat().st_size != rec["size"]
                                ):
                                    continue
                                dst_asset = dst_outputs / asset_src.name
                                dst_meta = dst_outputs / meta_path.name
                                if not dst_asset.exists() and not dst_asset.is_symlink():
                                    try:
                                        os.replace(asset_src, dst_asset)
                                    except OSError as exc:
                                        if exc.errno == errno.EXDEV:
                                            shutil.copy2(asset_src, dst_asset)
                                        else:
                                            raise
                                if not dst_meta.exists() and not dst_meta.is_symlink():
                                    atomic_json(dst_meta, rec)
                            except (OSError, ValueError, KeyError):
                                continue
                self.comfy.store.reload_from_disk()
            self.drive_restored = True
            return True
        finally:
            shutil.rmtree(stage_dir, ignore_errors=True)

    def _sync_to_drive(self) -> bool:
        if not self.drive.available():
            return False
        with self.lock:
            snapshot = dict(self._dirty_seq)
        if not snapshot:
            return True
        if not self.drive.push_workspace():
            return False
        with self.lock:
            for k, seq in snapshot.items():
                if self._dirty_seq.get(k) == seq:
                    self._dirty_seq.pop(k, None)
        return True

    def prepare_release(self, *, auto: bool = False):
        with self.release_lock:
            with self.lock:
                if self.release_prepared:
                    return {"ok": True}
                self.quiescing = True
                tasks = list(self.tasks.values())

            def _stop_comfy_serialized():
                with self._comfy_lifecycle_lock:
                    self.comfy.stop_server()

            if auto:
                for step in (
                    self.public_api.stop,
                    lambda: [
                        task.request_cancel()
                        for task in tasks
                        if task.state == "running"
                    ],
                    _stop_comfy_serialized,
                ):
                    try:
                        step()
                    except Exception:
                        pass
                deadline = time.monotonic() + 15
                while any(t.state == "running" for t in tasks):
                    if time.monotonic() >= deadline:
                        break
                    time.sleep(0.1)
                for step in (_stop_comfy_serialized, self._stop_engine):
                    try:
                        step()
                    except Exception:
                        pass
                with self.sync_lock:
                    try:
                        if self.drive.available():
                            if not self.drive_restored:
                                self._restore_from_drive()
                            if self.drive_restored and self._dirty_seq:
                                self._sync_to_drive()
                    except Exception:
                        pass
                    finally:
                        try:
                            self.drive.unmount()
                        except Exception:
                            pass
                with self.lock:
                    self.release_prepared = True
                return {"ok": True}
            try:
                self.public_api.stop()
                for task in tasks:
                    if task.state == "running":
                        task.request_cancel()
                _stop_comfy_serialized()
                deadline = time.monotonic() + 15
                while any(t.state == "running" for t in tasks):
                    if time.monotonic() >= deadline:
                        raise RuntimeError("任务仍在停止，请稍后重试释放")
                    time.sleep(0.1)
                _stop_comfy_serialized()
                self._stop_engine()
                with self.sync_lock:
                    if self.drive.available():
                        if not self.drive_restored and not self._restore_from_drive():
                            raise RuntimeError(
                                "云盘历史数据恢复未完成，为避免覆盖请重试或勾选强制释放"
                            )
                        if self._dirty_seq and not self._sync_to_drive():
                            raise RuntimeError(
                                "云盘同步尚未完成，请重试或勾选强制释放"
                            )
                        self.drive.unmount()
                with self.lock:
                    self.release_prepared = True
                return {"ok": True}
            except Exception:
                with self.lock:
                    if not self.auto_releasing:
                        self.quiescing = False
                raise

    def start_background(self):
        # Prime baseline CPU and disk I/O counters immediately so the first
        # status snapshot reports live rates instead of 0%.
        try:
            self.telemetry.sample(self.root, gpu_override=self.gpu_info())
        except Exception:
            pass

        if isinstance(self.stopping, threading.Event) and (
            self._telemetry_thread is None or not self._telemetry_thread.is_alive()
        ):
            def telemetry_loop():
                while not self.stopping.wait(1.0):
                    try:
                        self.telemetry.sample(self.root, gpu_override=self.gpu_info())
                    except Exception:
                        pass

            t = threading.Thread(target=telemetry_loop, daemon=True)
            t.start()
            self._telemetry_thread = t

        def loop():
            # Attach immediately only if Colab DriveFS is already mounted on disk
            # (instant local check). Avoid racing with the desktop's explicit
            # POST /api/v1/drive/connect on sync_lock during initial boot.
            if (
                getattr(self.drive, "is_colab_drivefs_mounted", lambda: False)()
                and not self.drive.available()
            ):
                try:
                    self.connect_drive()
                except Exception:
                    pass
            while not self.stopping.wait(5):
                if not isinstance(self.stopping, threading.Event):
                    try:
                        self.telemetry.sample(self.root, gpu_override=self.gpu_info())
                    except Exception:
                        pass
                try:
                    if not self.quiescing and self.drive.available() and self._dirty_seq:
                        with self.sync_lock:
                            if not self.drive_restored:
                                self._restore_from_drive()
                            if self.drive_restored:
                                self._sync_to_drive()
                except Exception:
                    pass
                if self.busy():
                    self.last_active = time.time()
                if self.claim_auto_release():
                    if not self.release_prepared:
                        try:
                            self.prepare_release(auto=True)
                        except Exception:
                            try:
                                with self.sync_lock:
                                    self.drive.unmount()
                            except Exception:
                                pass
                    self.quiescing = True
                    addr = os.environ.get("TBE_RUNTIME_ADDR", "")
                    if addr:
                        try:
                            self.http.post(
                                f"http://{addr}/unassign", data=b"", timeout=10
                            ).raise_for_status()
                            return
                        except requests.RequestException:
                            pass

        threading.Thread(target=loop, daemon=True).start()
