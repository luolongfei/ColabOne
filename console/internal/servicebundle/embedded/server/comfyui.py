"""Remote ComfyUI process manager, workflow runner, and immutable output storage.
Runs exclusively on the remote GPU runtime; the desktop never runs local inference.
"""

from __future__ import annotations

import base64
import copy
import hashlib
import importlib.util
import inspect
import io
import json
import os
import re
import shutil
import signal
import subprocess
import sys
import threading
import time
import uuid
from contextlib import nullcontext
from functools import wraps
from pathlib import Path, PurePosixPath
from typing import Any

import requests

from common.comfyui import (
    ALLOWED_INPUT_EXTENSIONS,
    ALLOWED_OUTPUT_MIME,
    MAX_AUDIO_INPUT_BYTES,
    MAX_IMAGE_INPUT_BYTES,
    MAX_OUTPUT_BYTES,
    ComfyCatalog,
    ComfyTtsPreviewRequest,
    WorkflowImportRequest,
    WorkflowOutputRecord,
    WorkflowRunRequest,
    WorkflowTemplate,
    apply_workflow_overrides,
    format_colabone_duration_zh,
)
from common.files import atomic_json
from server.comfy_proxy import ColabOneComfyCanvasGateway

MAX_TOTAL_INPUT_BYTES = 512 * 1024 * 1024  # 512 MiB
MAX_IMAGE_PIXELS = 16_777_216  # 16M pixels (4096 x 4096)
IMAGE_INPUT_EXTENSIONS = frozenset({"png", "jpg", "jpeg", "webp"})
AUDIO_INPUT_EXTENSIONS = frozenset(ALLOWED_INPUT_EXTENSIONS - IMAGE_INPUT_EXTENSIONS)
TORCH_SKIP_PACKAGES = frozenset({"torch", "torchvision", "torchaudio", "xformers"})
COMFY_MODEL_SUBDIRS = (
    "checkpoints",
    "diffusion_models",
    "loras",
    "vae",
    "text_encoders",
    "clip_vision",
    "wav2vec2",
    "controlnet",
    "upscale_models",
    "audio_encoders",
)

_WORKFLOW_ID_RE = re.compile(r"^[a-zA-Z0-9_-]{1,80}$")
_ASSET_ID_RE = re.compile(r"^[a-f0-9]{32}$")
_PROMPT_ID_RE = re.compile(r"^[A-Za-z0-9_.-]{1,120}$")
_STRIPPED_SUBPROCESS_ENV_KEYS = frozenset({"HF_TOKEN", "HUGGING_FACE_HUB_TOKEN"})


def _sanitized_subprocess_env() -> dict[str, str]:
    return {
        k: v
        for k, v in os.environ.items()
        if k not in _STRIPPED_SUBPROCESS_ENV_KEYS
    }


def store_locked(fn):
    @wraps(fn)
    def call(self, *args, **kwargs):
        with self.lock:
            return fn(self, *args, **kwargs)

    return call


def _response_ok(resp) -> bool:
    code = getattr(resp, "status_code", None)
    if isinstance(code, int):
        return 200 <= code < 300
    return bool(getattr(resp, "ok", True))


def _ensure_dir(path: Path, label: str) -> None:
    if path.is_symlink():
        sep = " " if label and label[-1].isascii() else ""
        raise ValueError(f"{label}{sep}不能是符号链接")
    path.mkdir(parents=True, exist_ok=True)


def _normalize_image_bytes(raw: bytes) -> bytes:
    from PIL import Image, ImageOps

    try:
        with Image.open(io.BytesIO(raw)) as src:
            if src.format not in ("PNG", "JPEG", "WEBP"):
                raise ValueError("仅支持 PNG、JPEG 或 WebP 图片")
            if src.width * src.height > MAX_IMAGE_PIXELS:
                raise ValueError("输入图片最多 1600 万像素")
            src.load()
            normalized = ImageOps.exif_transpose(src)
            has_alpha = (
                "A" in normalized.getbands()
                or normalized.mode in ("RGBA", "LA", "PA")
                or "transparency" in normalized.info
            )
            target_mode = "RGBA" if has_alpha else "RGB"
            if normalized.mode != target_mode:
                normalized = normalized.convert(target_mode)
            out = io.BytesIO()
            normalized.save(out, format="PNG")
            return out.getvalue()
    except ValueError:
        raise
    except Exception as exc:
        raise ValueError("请使用有效的 PNG、JPEG 或 WebP 图片文件") from exc


def _decode_input_b64(encoded_b64: str, max_bytes: int) -> bytes:
    if not isinstance(encoded_b64, str) or not encoded_b64.strip():
        raise ValueError("输入文件内容为空")
    payload = encoded_b64.strip()
    if payload.startswith("data:"):
        if "," not in payload:
            raise ValueError("输入文件编码格式无效")
        _, payload = payload.split(",", 1)
    if len(payload) > int(max_bytes * 1.5) + 4096:
        limit_mb = max_bytes // (1024 * 1024)
        raise ValueError(f"输入文件超过大小限制（最大 {limit_mb} MB）")
    try:
        raw = base64.b64decode(re.sub(r"\s+", "", payload), validate=True)
    except Exception as exc:
        raise ValueError("输入文件 Base64 编码无效") from exc
    if not raw:
        raise ValueError("输入文件内容为空")
    if len(raw) > max_bytes:
        limit_mb = max_bytes // (1024 * 1024)
        raise ValueError(f"输入文件超过大小限制（最大 {limit_mb} MB）")
    return raw


def _safe_basename(filename: str) -> str:
    if not isinstance(filename, str):
        return ""
    cleaned = filename.replace("\\", "/").replace("\x00", "")
    name = PurePosixPath(cleaned).name
    if name in ("", ".", ".."):
        return ""
    return name


class ColabOneComfyWorkflowStore:
    def __init__(
        self,
        root: Path,
        builtin_templates: list[WorkflowTemplate] | None = None,
        on_workflow_change=None,
        on_workflow_delete=None,
        on_output_change=None,
        on_output_delete=None,
    ):
        self.root = Path(root)
        self.on_workflow_change = on_workflow_change
        self.on_workflow_delete = on_workflow_delete
        self.on_output_change = on_output_change
        self.on_output_delete = on_output_delete
        self.lock = threading.RLock()

        self.workflows_dir = self.root / "workflows"
        _ensure_dir(self.workflows_dir, "工作流目录")
        self.outputs_dir = self.root / "comfyui-outputs"
        _ensure_dir(self.outputs_dir, "作品目录")
        self.inputs_dir = self.root / "comfyui-inputs"
        _ensure_dir(self.inputs_dir, "输入文件目录")

        self.builtins: dict[str, dict] = {}
        for tmpl in builtin_templates or []:
            raw = tmpl.model_dump() if isinstance(tmpl, WorkflowTemplate) else dict(tmpl)
            raw["source"] = "builtin"
            validated = WorkflowTemplate.model_validate(raw).model_dump()
            self.builtins[validated["id"]] = validated

        self.custom_workflows: dict[str, dict] = {}
        self.outputs: dict[str, dict] = {}
        self.inputs_index: dict[str, dict] = {}
        self.reload_from_disk()

    @store_locked
    def reload_from_disk(self) -> None:
        workflows: dict[str, dict] = {}
        for path in self.workflows_dir.glob("*.json"):
            try:
                if path.is_symlink() or not path.is_file():
                    continue
                data = json.loads(path.read_text(encoding="utf-8"))
                if not isinstance(data, dict):
                    continue
                data["source"] = "custom"
                item = WorkflowTemplate.model_validate(data).model_dump()
                if item["id"] == path.stem and item["id"] not in self.builtins:
                    workflows[item["id"]] = item
            except (OSError, ValueError, KeyError):
                continue
        self.custom_workflows = workflows

        outputs: dict[str, dict] = {}
        for path in self.outputs_dir.glob("*.json"):
            try:
                if path.is_symlink() or not path.is_file():
                    continue
                item = WorkflowOutputRecord.model_validate_json(
                    path.read_text(encoding="utf-8")
                ).model_dump()
                asset_path = self.outputs_dir / f"{item['id']}.{item['extension']}"
                if (
                    item["id"] == path.stem
                    and asset_path.is_file()
                    and not asset_path.is_symlink()
                ):
                    outputs[item["id"]] = item
            except (OSError, ValueError, KeyError):
                continue
        self.outputs = outputs

        inputs_index: dict[str, dict] = {}
        for path in self.inputs_dir.iterdir():
            try:
                if path.is_symlink() or not path.is_file():
                    continue
                ext = path.suffix.lstrip(".").lower()
                if not _ASSET_ID_RE.fullmatch(path.stem) or ext not in ALLOWED_INPUT_EXTENSIONS:
                    continue
                inputs_index[path.stem] = {
                    "id": path.stem,
                    "filename": path.name,
                    "path": path,
                    "size": path.stat().st_size,
                }
            except OSError:
                continue
        self.inputs_index = inputs_index

    @store_locked
    def list_workflows(self) -> list[dict]:
        ordered_custom = sorted(
            self.custom_workflows.values(),
            key=lambda x: x.get("updated_at", 0.0),
            reverse=True,
        )
        return copy.deepcopy(list(self.builtins.values()) + ordered_custom)

    @store_locked
    def get_workflow(self, workflow_id: str) -> dict:
        if workflow_id in self.custom_workflows:
            return copy.deepcopy(self.custom_workflows[workflow_id])
        if workflow_id in self.builtins:
            return copy.deepcopy(self.builtins[workflow_id])
        raise KeyError(workflow_id)

    def save_workflow(self, req: WorkflowImportRequest | dict) -> dict:
        validated = WorkflowImportRequest.model_validate(
            req.model_dump() if isinstance(req, WorkflowImportRequest) else req
        )
        with self.lock:
            wf_id = validated.id or f"wf-{uuid.uuid4().hex[:12]}"
            if wf_id in self.builtins:
                raise ValueError("不能覆盖内置工作流模板")
            if not _WORKFLOW_ID_RE.fullmatch(wf_id):
                raise ValueError("工作流编号不合法")
            now = time.time()
            existing = self.custom_workflows.get(wf_id)
            created_at = existing["created_at"] if existing else now
            item = WorkflowTemplate(
                id=wf_id,
                name=validated.name,
                description=validated.description,
                source="custom",
                created_at=created_at,
                updated_at=now,
                workflow=validated.workflow,
            ).model_dump()
            target = self.workflows_dir / f"{item['id']}.json"
            if target.is_symlink():
                raise ValueError("工作流文件不能是符号链接")
            atomic_json(target, item)
            self.custom_workflows[item["id"]] = item
        if self.on_workflow_change:
            self.on_workflow_change(item["id"])
        return copy.deepcopy(item)

    def delete_workflow(self, workflow_id: str) -> None:
        with self.lock:
            if workflow_id in self.builtins:
                raise ValueError("内置工作流模板不可删除")
            if not isinstance(workflow_id, str) or not _WORKFLOW_ID_RE.fullmatch(
                workflow_id
            ):
                raise ValueError("工作流编号不合法")
            if workflow_id not in self.custom_workflows:
                raise KeyError(workflow_id)
            trash = self.workflows_dir / ".trash"
            _ensure_dir(trash, "回收目录")
            path = self.workflows_dir / f"{workflow_id}.json"
            if path.is_symlink():
                raise ValueError("工作流文件不能是符号链接")
            if path.exists():
                path.replace(trash / path.name)
            del self.custom_workflows[workflow_id]
        if self.on_workflow_delete:
            self.on_workflow_delete(workflow_id)

    def save_input(self, filename: str, encoded_b64: str) -> dict:
        safe_name = _safe_basename(filename)
        ext = PurePosixPath(safe_name).suffix.lstrip(".").lower() if safe_name else ""
        if not ext or ext not in ALLOWED_INPUT_EXTENSIONS:
            raise ValueError(f"不支持的输入文件格式：{ext or '未知'}")
        is_image = ext in IMAGE_INPUT_EXTENSIONS
        max_bytes = MAX_IMAGE_INPUT_BYTES if is_image else MAX_AUDIO_INPUT_BYTES
        raw = _decode_input_b64(encoded_b64, max_bytes)
        if is_image:
            data = _normalize_image_bytes(raw)
            stored_ext = "png"
        else:
            data = raw
            stored_ext = ext
        with self.lock:
            used = sum(
                p.stat().st_size
                for p in self.inputs_dir.iterdir()
                if p.is_file() and not p.is_symlink()
            )
            if used + len(data) > MAX_TOTAL_INPUT_BYTES:
                raise ValueError(
                    "本次实例的输入素材已达 512 MB 上限，请复用已有素材或重新启动实例"
                )
            identity = uuid.uuid4().hex
            stored_name = f"{identity}.{stored_ext}"
            path = self.inputs_dir / stored_name
            if path.is_symlink():
                raise ValueError("输入文件不能是符号链接")
            path.write_bytes(data)
            self.inputs_index[identity] = {
                "id": identity,
                "filename": stored_name,
                "path": path,
                "size": len(data),
            }
            return {"id": identity, "filename": stored_name, "size": len(data)}

    @store_locked
    def input_file(self, identity: str) -> tuple[str, Path]:
        if not isinstance(identity, str) or not _ASSET_ID_RE.fullmatch(identity):
            raise ValueError("输入文件编号不合法")
        entry = self.inputs_index.get(identity)
        if entry is not None:
            path = Path(entry["path"])
            if (
                path.is_file()
                and not path.is_symlink()
                and path.parent == self.inputs_dir
            ):
                return str(entry["filename"]), path
            self.inputs_index.pop(identity, None)
        for ext in sorted(ALLOWED_INPUT_EXTENSIONS):
            candidate = self.inputs_dir / f"{identity}.{ext}"
            if candidate.is_file() and not candidate.is_symlink():
                self.inputs_index[identity] = {
                    "id": identity,
                    "filename": candidate.name,
                    "path": candidate,
                    "size": candidate.stat().st_size,
                }
                return candidate.name, candidate
        raise ValueError("输入文件已失效，请重新上传")

    @store_locked
    def list_outputs(self) -> list[dict]:
        return copy.deepcopy(
            sorted(self.outputs.values(), key=lambda x: x["created_at"], reverse=True)
        )

    def save_output(
        self,
        raw: bytes,
        workflow_id: str,
        workflow_name: str,
        filename: str,
        elapsed: float,
        prompt_id: str = "",
    ) -> dict:
        if not isinstance(raw, (bytes, bytearray)) or not raw:
            raise ValueError("生成结果为空")
        data = bytes(raw)
        if len(data) > MAX_OUTPUT_BYTES:
            raise ValueError("生成结果超过最大体积限制")
        safe_name = _safe_basename(filename)
        if ".." in safe_name:
            safe_name = safe_name.replace("..", "_")
        ext = PurePosixPath(safe_name).suffix.lstrip(".").lower() if safe_name else ""
        if not ext or ext not in ALLOWED_OUTPUT_MIME:
            raise ValueError(f"不支持的输出文件格式：{ext or '未知'}")
        kind, mime = ALLOWED_OUTPUT_MIME[ext]
        if ext == "png" and not data.startswith(b"\x89PNG\r\n\x1a\n"):
            raise ValueError("引擎返回的 PNG 图片格式不正确")
        if len(safe_name) > 255:
            stem = PurePosixPath(safe_name).stem[: 254 - len(ext)]
            safe_name = f"{stem or 'output'}.{ext}"

        identity = uuid.uuid4().hex
        record = WorkflowOutputRecord.model_validate(
            {
                "id": identity,
                "workflow_id": workflow_id,
                "workflow_name": (workflow_name or workflow_id)[:160],
                "kind": kind,
                "filename": safe_name,
                "extension": ext,
                "mime": mime,
                "size": len(data),
                "sha256": hashlib.sha256(data).hexdigest(),
                "created_at": time.time(),
                "elapsed": max(0.0, float(elapsed)),
                "prompt_id": (prompt_id or "")[:120],
            }
        ).model_dump()

        with self.lock:
            asset_path = self.outputs_dir / f"{identity}.{ext}"
            meta_path = self.outputs_dir / f"{identity}.json"
            if asset_path.is_symlink() or meta_path.is_symlink():
                raise ValueError("作品文件不能是符号链接")
            tmp_asset = self.outputs_dir / f".{identity}.{ext}.{uuid.uuid4().hex[:8]}.tmp"
            try:
                with tmp_asset.open("wb") as stream:
                    stream.write(data)
                    stream.flush()
                    os.fsync(stream.fileno())
                os.replace(tmp_asset, asset_path)
            finally:
                tmp_asset.unlink(missing_ok=True)
            atomic_json(meta_path, record)
            self.outputs[identity] = record
        if self.on_output_change:
            self.on_output_change(identity)
        return copy.deepcopy(record)

    def save_output_file(
        self,
        src_path: Path,
        workflow_id: str,
        workflow_name: str,
        filename: str,
        mime_type: str | None = None,
        elapsed: float = 0.0,
        prompt_id: str = "",
        extension: str | None = None,
        kind: str | None = None,
    ) -> dict:
        """Stream an output file from disk into immutable storage without loading it into RAM."""
        if isinstance(mime_type, (int, float)) and not isinstance(mime_type, bool):
            # Support positional (src_path, workflow_id, workflow_name, filename, elapsed, prompt_id)
            if isinstance(elapsed, str) and not prompt_id:
                prompt_id = elapsed
            elapsed = float(mime_type)
            mime_type = None

        src = Path(src_path)
        if src.is_symlink() or not src.is_file():
            raise ValueError("生成结果文件不存在")
        stat_size = src.stat().st_size
        if stat_size <= 0:
            raise ValueError("生成结果为空")
        if stat_size > MAX_OUTPUT_BYTES:
            raise ValueError("生成结果超过最大体积限制")

        safe_name = _safe_basename(filename)
        if ".." in safe_name:
            safe_name = safe_name.replace("..", "_")
        ext = (
            str(extension).lstrip(".").lower()
            if extension
            else (PurePosixPath(safe_name).suffix.lstrip(".").lower() if safe_name else "")
        )
        if not ext or ext not in ALLOWED_OUTPUT_MIME:
            raise ValueError(f"不支持的输出文件格式：{ext or '未知'}")
        expected_kind, expected_mime = ALLOWED_OUTPUT_MIME[ext]
        final_kind = str(kind).strip() if kind else expected_kind
        final_mime = str(mime_type).strip() if mime_type else expected_mime
        if final_kind != expected_kind or final_mime != expected_mime:
            raise ValueError(f"输出文件格式与类型不匹配：{ext}")
        if len(safe_name) > 255:
            stem = PurePosixPath(safe_name).stem[: 254 - len(ext)]
            safe_name = f"{stem or 'output'}.{ext}"

        identity = uuid.uuid4().hex
        tmp_asset = self.outputs_dir / f".{identity}.{ext}.{uuid.uuid4().hex[:8]}.tmp"
        hasher = hashlib.sha256()
        total_bytes = 0
        header_buf = bytearray()
        chunk_size = 1024 * 1024  # 1 MiB streaming chunks

        try:
            with src.open("rb") as reader, tmp_asset.open("wb") as writer:
                while True:
                    chunk = reader.read(chunk_size)
                    if not chunk:
                        break
                    total_bytes += len(chunk)
                    if total_bytes > MAX_OUTPUT_BYTES:
                        raise ValueError("生成结果超过最大体积限制")
                    if ext == "png" and len(header_buf) < 8:
                        need = 8 - len(header_buf)
                        header_buf.extend(chunk[:need])
                        if len(header_buf) >= 8 and not bytes(header_buf).startswith(
                            b"\x89PNG\r\n\x1a\n"
                        ):
                            raise ValueError("引擎返回的 PNG 图片格式不正确")
                    hasher.update(chunk)
                    writer.write(chunk)
                if total_bytes <= 0:
                    raise ValueError("生成结果为空")
                if ext == "png" and not bytes(header_buf).startswith(b"\x89PNG\r\n\x1a\n"):
                    raise ValueError("引擎返回的 PNG 图片格式不正确")
                writer.flush()
                os.fsync(writer.fileno())

            record = WorkflowOutputRecord.model_validate(
                {
                    "id": identity,
                    "workflow_id": workflow_id,
                    "workflow_name": (workflow_name or workflow_id)[:160],
                    "kind": final_kind,
                    "filename": safe_name,
                    "extension": ext,
                    "mime": final_mime,
                    "size": total_bytes,
                    "sha256": hasher.hexdigest(),
                    "created_at": time.time(),
                    "elapsed": max(0.0, float(elapsed)),
                    "prompt_id": (prompt_id or "")[:120],
                }
            ).model_dump()

            with self.lock:
                asset_path = self.outputs_dir / f"{identity}.{ext}"
                meta_path = self.outputs_dir / f"{identity}.json"
                if asset_path.is_symlink() or meta_path.is_symlink():
                    raise ValueError("作品文件不能是符号链接")
                os.replace(tmp_asset, asset_path)
                atomic_json(meta_path, record)
                self.outputs[identity] = record
        finally:
            tmp_asset.unlink(missing_ok=True)

        if self.on_output_change:
            self.on_output_change(identity)
        return copy.deepcopy(record)

    @store_locked
    def asset(
        self, identity: str, offset: int = 0, length: int = 512 * 1024
    ) -> dict:
        item = self.outputs[identity]
        size = item["size"]
        if (
            offset < 0
            or (offset >= size if size > 0 else offset > 0)
            or not 1 <= length <= 512 * 1024
        ):
            raise ValueError("作品读取范围不合法")
        path = self.outputs_dir / f"{identity}.{item['extension']}"
        if path.is_symlink():
            raise ValueError("作品不能是符号链接")
        if not path.is_file():
            raise KeyError(identity)
        with path.open("rb") as stream:
            stream.seek(offset)
            data = stream.read(length)
        return {
            "data": base64.b64encode(data).decode(),
            "offset": offset,
            "size": item["size"],
            "sha256": item["sha256"],
            "mime": item["mime"],
            "extension": item["extension"],
            "filename": item["filename"],
        }

    def delete_output(self, identity: str) -> None:
        with self.lock:
            item = self.outputs[identity]
            ext = item["extension"]
            trash = self.outputs_dir / ".trash"
            _ensure_dir(trash, "回收目录")
            for suffix in (ext, "json"):
                path = self.outputs_dir / f"{identity}.{suffix}"
                if path.is_symlink():
                    raise ValueError("作品文件不能是符号链接")
                if path.exists():
                    path.replace(trash / path.name)
            del self.outputs[identity]
        if self.on_output_delete:
            self.on_output_delete(identity, ext)


def _yaml_quote(value: str) -> str:
    if "\n" in value or "\r" in value:
        raise ValueError("路径不能包含换行符")
    return "'" + value.replace("'", "''") + "'"


def write_extra_model_paths(
    yaml_path: Path, comfy_models_dir: Path, shared_models_dir: Path
) -> Path:
    yaml_path = Path(yaml_path)
    comfy_models_dir = Path(comfy_models_dir)
    shared_models_dir = Path(shared_models_dir)

    _ensure_dir(comfy_models_dir, "ComfyUI 模型目录")
    for subdir in COMFY_MODEL_SUBDIRS:
        _ensure_dir(comfy_models_dir / subdir, f"ComfyUI 模型子目录 {subdir}")
    _ensure_dir(shared_models_dir, "共享模型目录")
    if yaml_path.is_symlink():
        raise ValueError("模型路径配置文件不能是符号链接")
    yaml_path.parent.mkdir(parents=True, exist_ok=True)

    lines = [
        "colabone_comfy:",
        f"  base_path: {_yaml_quote(str(comfy_models_dir))}",
        "  is_default: true",
    ]
    for subdir in COMFY_MODEL_SUBDIRS:
        lines.append(f"  {subdir}: {subdir}")
    lines.extend(
        [
            "",
            "colabone_shared:",
            f"  base_path: {_yaml_quote(str(shared_models_dir))}",
        ]
    )
    for subdir in COMFY_MODEL_SUBDIRS:
        lines.append(f"  {subdir}: .")
    lines.append("")

    yaml_path.write_text("\n".join(lines), encoding="utf-8")
    return yaml_path


def _pg_has_members(pgid: int) -> bool:
    """Return True if any process still exists in POSIX process group `pgid`."""
    try:
        os.killpg(pgid, 0)
        return True
    except ProcessLookupError:
        return False
    except (PermissionError, OSError):
        return False


def _terminate_process_group(proc: subprocess.Popen, timeout: float = 5.0) -> None:
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

    if not leader_alive and not can_killpg:
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

    # Even after the leader process exits, wait for surviving process-group
    # members to terminate cleanly after SIGTERM, then SIGKILL stragglers.
    if used_pg:
        while _pg_has_members(pid) and time.monotonic() < deadline:
            time.sleep(0.05)
        if _pg_has_members(pid):
            try:
                os.killpg(pid, signal.SIGKILL)
            except (ProcessLookupError, PermissionError, OSError):
                pass
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


def _run_command(
    args, task, log: Path, timeout: int = 1200, cwd=None, stopping=None
) -> None:
    """Cancelable process group; subprocesses cannot outlive their task."""
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
    end, noted = time.monotonic() + timeout, 0.0
    try:
        while proc.poll() is None:
            task.check()
            if isinstance(stopping, threading.Event) and stopping.is_set():
                from server.runtime import ColabOneCancelledError

                raise ColabOneCancelledError("实例正在停止，已取消 ComfyUI 准备")
            if time.monotonic() > end:
                raise RuntimeError("远端 ComfyUI 准备超时，请查看运行日志")
            if time.monotonic() - noted > 5:
                noted = time.monotonic()
                tail = log.read_text(encoding="utf-8", errors="replace")[
                    -1000:
                ].splitlines()
                if tail:
                    task.note(tail[-1][:250])
            time.sleep(0.25)
        if proc.returncode:
            raise RuntimeError(
                "远端 ComfyUI 准备失败："
                + log.read_text(encoding="utf-8", errors="replace")[-600:]
            )
    finally:
        _terminate_process_group(proc)


def _load_catalog(catalog_dir: Path | None) -> ComfyCatalog:
    if catalog_dir is not None:
        path = Path(catalog_dir) / "comfyui.json"
        if path.is_file():
            return ComfyCatalog.model_validate_json(path.read_text(encoding="utf-8"))
    return ComfyCatalog(
        repo="https://github.com/Comfy-Org/ComfyUI.git",
        tag="v0.46.0",
        commit="a277b5bf213e8a44fa34817b7770b54f86ef4cea",
        port=8188,
        proxy_port=8189,
        templates=[],
    )


def _format_comfy_errors(body: dict[str, Any]) -> str:
    parts: list[str] = []
    err = body.get("error")
    if isinstance(err, dict):
        msg = err.get("message") or err.get("type") or ""
        details = err.get("details") or ""
        if msg and details:
            parts.append(f"{msg}: {details}")
        elif msg or details:
            parts.append(str(msg or details))
    elif err:
        parts.append(str(err))

    node_errors = body.get("node_errors")
    if isinstance(node_errors, dict) and node_errors:
        for node_id, info in node_errors.items():
            if not isinstance(info, dict):
                parts.append(f"节点 {node_id}: {info}")
                continue
            cls = info.get("class_type") or ""
            label = f"节点 {node_id}({cls})" if cls else f"节点 {node_id}"
            errs = info.get("errors") or []
            if isinstance(errs, list) and errs:
                for item in errs:
                    if isinstance(item, dict):
                        msg = item.get("message") or item.get("type") or ""
                        details = item.get("details") or ""
                        parts.append(f"{label}: {msg}" + (f" ({details})" if details else ""))
                    else:
                        parts.append(f"{label}: {item}")
            else:
                parts.append(f"{label}: {info}")
    return "；".join(parts)[:600]


def _extract_history_error(status_info: dict[str, Any]) -> str:
    messages = status_info.get("messages") or []
    for msg in reversed(messages):
        if (
            isinstance(msg, (list, tuple))
            and len(msg) >= 2
            and msg[0] == "execution_error"
            and isinstance(msg[1], dict)
        ):
            info = msg[1]
            node_id = info.get("node_id") or ""
            node_type = info.get("node_type") or ""
            exc_msg = (
                info.get("exception_message")
                or info.get("message")
                or info.get("exception_type")
                or "未知执行错误"
            )
            prefix = (
                f"节点 {node_id}({node_type}): "
                if node_id and node_type
                else (f"节点 {node_id}: " if node_id else "")
            )
            return f"{prefix}{exc_msg}"[:600]
        if (
            isinstance(msg, (list, tuple))
            and len(msg) >= 1
            and msg[0] == "execution_interrupted"
        ):
            info = msg[1] if len(msg) >= 2 and isinstance(msg[1], dict) else {}
            node_id = info.get("node_id") or ""
            node_type = info.get("node_type") or ""
            if node_id and node_type:
                return f"节点 {node_id}({node_type}): 工作流执行已中断"[:600]
            if node_id:
                return f"节点 {node_id}: 工作流执行已中断"[:600]
            return "工作流执行已中断"
    for key in ("error", "message", "detail"):
        if status_info.get(key):
            return str(status_info[key])[:600]
    if status_info.get("completed") is False:
        return "工作流执行未完成或已中断"
    return "工作流执行出错"


from server import comfy_packs
from server.comfy_packs import (
    ColabOneComfyPackManager,
    _filter_requirements,
    _req_package_name,
)
from server.comfy_ws import ColabOneComfyWsMonitor
from server.digital_human import ColabOneDigitalHumanRunner
from server.tts_engines import ColabOneRemoteTtsRunner


class ColabOneComfyManager:
    def __init__(self, runtime):
        self.runtime = runtime
        self.catalog = _load_catalog(getattr(runtime, "catalog", None))
        self.lock = threading.RLock()
        self.install_dir = runtime.root / "comfyui"
        self.models_dir = runtime.root / "comfyui-models"
        self.io_in_dir = runtime.root / "comfyui-io-in"
        self.io_out_dir = runtime.root / "comfyui-io-out"
        self.port = self.catalog.port
        self.proxy_port = self.catalog.proxy_port
        self.base_url = f"http://127.0.0.1:{self.port}"
        self.proc: subprocess.Popen | None = None
        self.state = "stopped"
        self.error = ""
        self._queue_cache: tuple[int, int] = (0, 0)
        self._queue_checked_at: float = -10.0
        self._loaded_pack_signature: str = ""
        self._history_poll_interval: float = 0.5

        self.store = ColabOneComfyWorkflowStore(
            runtime.root,
            builtin_templates=self.catalog.templates,
            on_workflow_change=lambda wid: runtime._mark_dirty("workflow:" + wid),
            on_workflow_delete=lambda wid: runtime.drive.remove_remote_workflow(wid),
            on_output_change=lambda oid: runtime._mark_dirty("comfy_output:" + oid),
            on_output_delete=lambda oid, ext: runtime.drive.remove_remote_comfy_output(
                oid, ext
            ),
        )
        self.canvas = ColabOneComfyCanvasGateway(
            runtime, upstream_port=self.port, proxy_port=self.proxy_port
        )
        self.packs = ColabOneComfyPackManager(
            runtime, self.install_dir, self.models_dir
        )
        engines_by_id = {
            (e["id"] if isinstance(e, dict) else e.id): (
                dict(e) if isinstance(e, dict) else e.model_dump()
            )
            for e in (getattr(self.catalog, "tts_engines", None) or [])
        }
        self.tts = ColabOneRemoteTtsRunner(runtime, engines_by_id)
        self.digital_human = ColabOneDigitalHumanRunner(self, self.tts)

    def is_installed(self) -> bool:
        marker = self.install_dir / ".complete"
        if (
            self.install_dir.is_symlink()
            or not (self.install_dir / "main.py").is_file()
            or (self.install_dir / "main.py").is_symlink()
            or marker.is_symlink()
            or not marker.is_file()
        ):
            return False
        try:
            raw = marker.read_text(encoding="utf-8").strip()
        except OSError:
            return False
        if not raw:
            # Backwards compatible with legacy empty .complete marker files and tests.
            return True
        try:
            meta = json.loads(raw)
        except ValueError:
            return True
        if isinstance(meta, dict):
            recorded_commit = str(meta.get("commit") or "").strip().lower()
            expected_commit = str(self.catalog.commit or "").strip().lower()
            if recorded_commit and expected_commit and recorded_commit != expected_commit:
                return False
        return True

    def is_running(self) -> bool:
        with self.lock:
            return (
                self.proc is not None
                and self.proc.poll() is None
                and self.state == "ready"
            )

    def queue_counts(self) -> tuple[int, int]:
        if not self.is_running():
            with self.lock:
                self._queue_cache = (0, 0)
                self._queue_checked_at = time.monotonic()
            return (0, 0)
        try:
            resp = self.runtime.http.get(self.base_url + "/queue", timeout=1.5)
            if not _response_ok(resp):
                return (0, 0)
            data = resp.json() or {}
            running = data.get("queue_running") or []
            pending = data.get("queue_pending") or []
            counts = (len(running), len(pending))
            with self.lock:
                self._queue_cache = counts
                self._queue_checked_at = time.monotonic()
            return counts
        except Exception:
            return (0, 0)

    def queue_counts_cached(self) -> tuple[int, int]:
        """Return the last cached ComfyUI queue counts without blocking on HTTP."""
        with self.lock:
            if (
                self.proc is None
                or self.proc.poll() is not None
                or self.state != "ready"
            ):
                return (0, 0)
            return self._queue_cache

    def status(self) -> dict:
        with self.lock:
            if self.proc is not None and self.proc.poll() is not None:
                code = self.proc.returncode
                self.proc = None
                if self.state == "ready":
                    self.state = "error" if code else "stopped"
                    if code:
                        self.error = f"ComfyUI 进程异常退出（退出码 {code}）"
                self._queue_cache = (0, 0)
                self._queue_checked_at = -10.0
                self._loaded_pack_signature = ""
            running = self.is_running()
            state = self.state
            error = self.error
            last_checked = self._queue_checked_at
            cached = self._queue_cache

        if running:
            now = time.monotonic()
            if now - last_checked >= 2.0:
                counts = self.queue_counts()
                with self.lock:
                    self._queue_cache = counts
                    self._queue_checked_at = now
                q_run, q_pend = counts
            else:
                q_run, q_pend = cached
        else:
            q_run, q_pend = 0, 0

        return {
            "installed": self.is_installed(),
            "running": running,
            "state": state,
            "port": self.port,
            "tag": self.catalog.tag,
            "queue_running": q_run,
            "queue_pending": q_pend,
            "error": error,
            "active_pack": self.packs.active_pack_id,
            "canvas": self.canvas.snapshot(),
        }

    def list_tts_engines(self) -> list[dict]:
        return self.tts.list_engines()

    def prepare(self, task) -> Path:
        if self.is_installed():
            task.note("复用远端已安装的 ComfyUI " + self.catalog.tag)
            return self.install_dir
        if self.install_dir.is_symlink():
            raise ValueError("ComfyUI 目录不能是符号链接")
        if not shutil.which("git"):
            raise RuntimeError("此远端缺少 Git，无法安装 ComfyUI")
        stopping = getattr(self.runtime, "stopping", None)
        git_dir = self.install_dir / ".git"
        if git_dir.exists() and not git_dir.is_dir():
            shutil.rmtree(self.install_dir, ignore_errors=True)
        self.install_dir.mkdir(parents=True, exist_ok=True)
        log = self.runtime.root / "comfyui-install.log"
        task.message = "首次安装远端 ComfyUI " + self.catalog.tag
        task.note(task.message)
        if git_dir.is_dir():
            # Clean stale git lockfiles left by a previously interrupted install.
            for lock_file in git_dir.rglob("*.lock"):
                try:
                    lock_file.unlink(missing_ok=True)
                except OSError:
                    pass
            try:
                _run_command(
                    [
                        "git",
                        "-C",
                        str(self.install_dir),
                        "remote",
                        "set-url",
                        "origin",
                        self.catalog.repo,
                    ],
                    task,
                    log,
                    stopping=stopping,
                )
            except RuntimeError:
                _run_command(
                    [
                        "git",
                        "-C",
                        str(self.install_dir),
                        "remote",
                        "add",
                        "origin",
                        self.catalog.repo,
                    ],
                    task,
                    log,
                    stopping=stopping,
                )
        else:
            task.note("正在初始化远端 ComfyUI 仓库目录")
            _run_command(
                ["git", "init", str(self.install_dir)], task, log, stopping=stopping
            )
            _run_command(
                [
                    "git",
                    "-C",
                    str(self.install_dir),
                    "remote",
                    "add",
                    "origin",
                    self.catalog.repo,
                ],
                task,
                log,
                stopping=stopping,
            )
        task.note("正在拉取 ComfyUI 指定版本源码")
        _run_command(
            [
                "git",
                "-C",
                str(self.install_dir),
                "fetch",
                "--depth",
                "1",
                "origin",
                self.catalog.commit,
            ],
            task,
            log,
            stopping=stopping,
        )
        task.note("源码拉取完成，正在检出目标版本")
        _run_command(
            [
                "git",
                "-C",
                str(self.install_dir),
                "checkout",
                "--force",
                "--detach",
                self.catalog.commit,
            ],
            task,
            log,
            stopping=stopping,
        )

        req_file = self.install_dir / "requirements.txt"
        if req_file.is_file() and not req_file.is_symlink():
            has_torch = importlib.util.find_spec("torch") is not None
            filtered_req = self.install_dir / ".colabone-requirements.txt"
            if _filter_requirements(req_file, filtered_req, skip_torch=has_torch):
                task.message = "安装 ComfyUI Python 依赖"
                task.note(
                    "正在安装 ComfyUI 依赖包"
                    + ("（保留现有 CUDA PyTorch）" if has_torch else "")
                )
                pip_cmd = [
                    sys.executable,
                    "-m",
                    "pip",
                    "install",
                    "--no-input",
                    "--disable-pip-version-check",
                ]
                constraints_path = comfy_packs._write_torch_constraints(
                    self.install_dir
                )
                if constraints_path is not None:
                    pip_cmd.extend(["-c", str(constraints_path)])
                pip_cmd.extend(["-r", str(filtered_req)])
                _run_command(
                    pip_cmd,
                    task,
                    log,
                    stopping=stopping,
                )

        if not (self.install_dir / "main.py").is_file():
            raise RuntimeError("ComfyUI 安装不完整：缺少 main.py")
        complete_marker = self.install_dir / ".complete"
        if complete_marker.is_symlink():
            raise ValueError("ComfyUI 完成标记不能是符号链接")
        atomic_json(
            complete_marker,
            {"commit": self.catalog.commit, "tag": self.catalog.tag},
        )
        task.note("远端 ComfyUI " + self.catalog.tag + " 安装完成")
        return self.install_dir

    def start_server(self, task) -> None:
        if self.is_running():
            try:
                resp = self.runtime.http.get(
                    self.base_url + "/system_stats", timeout=2
                )
                if _response_ok(resp):
                    return
            except (requests.RequestException, OSError):
                pass
            self.stop_server()
        elif self.proc is not None:
            self.stop_server()

        task.check()
        if getattr(self.runtime, "quiescing", False) or (
            isinstance(getattr(self.runtime, "stopping", None), threading.Event)
            and self.runtime.stopping.is_set()
        ):
            from server.runtime import ColabOneCancelledError

            raise ColabOneCancelledError("实例正在停止，已取消启动 ComfyUI")

        if getattr(self.runtime, "loaded", None):
            task.note("正在卸载当前对话模型以释放显存")
        self.runtime._stop_engine()

        with self.lock:
            self.state = "starting"
            self.error = ""

        proc: subprocess.Popen | None = None
        try:
            self.prepare(task)
            task.check()
            _ensure_dir(self.io_in_dir, "ComfyUI 输入目录")
            _ensure_dir(self.io_out_dir, "ComfyUI 输出目录")
            yaml_path = write_extra_model_paths(
                self.runtime.root / "extra_model_paths.yaml",
                self.models_dir,
                self.runtime.models_dir,
            )

            gpu = self.runtime.gpu_info() or {}
            args = [
                sys.executable,
                str(self.install_dir / "main.py"),
                "--listen",
                "127.0.0.1",
                "--port",
                str(self.port),
                "--disable-auto-launch",
                "--extra-model-paths-config",
                str(yaml_path),
                "--input-directory",
                str(self.io_in_dir),
                "--output-directory",
                str(self.io_out_dir),
            ]
            if (gpu.get("total_mib") or 0) >= 40000:
                args.append("--disable-mmap")

            log = self.runtime.root / "comfyui.log"
            task.message = "正在启动远端 ComfyUI 引擎"
            task.note(f"已生成模型路径配置，正在启动远端 ComfyUI（端口 {self.port}）")
            launch_pack_sig = str(getattr(self.packs, "active_pack_signature", "") or "")
            cwd = str(self.install_dir) if self.install_dir.is_dir() else None
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
            with self.lock:
                if (
                    task.cancel.is_set()
                    or getattr(self.runtime, "quiescing", False)
                    or (
                        isinstance(getattr(self.runtime, "stopping", None), threading.Event)
                        and self.runtime.stopping.is_set()
                    )
                ):
                    from server.runtime import ColabOneCancelledError

                    raise ColabOneCancelledError("实例正在停止，已取消启动 ComfyUI")
                self.proc = proc

            start_t = time.monotonic()
            last_wait_note = start_t
            deadline = start_t + 180
            while time.monotonic() < deadline:
                task.check()
                if getattr(self.runtime, "quiescing", False) or (
                    isinstance(getattr(self.runtime, "stopping", None), threading.Event)
                    and self.runtime.stopping.is_set()
                ):
                    from server.runtime import ColabOneCancelledError

                    raise ColabOneCancelledError("实例正在停止，已取消启动 ComfyUI")
                if proc.poll() is not None:
                    tail = ""
                    if log.is_file():
                        tail = log.read_text(encoding="utf-8", errors="replace")[-800:]
                    raise RuntimeError(
                        "ComfyUI 启动失败：" + (tail or f"退出码 {proc.returncode}")
                    )
                try:
                    resp = self.runtime.http.get(
                        self.base_url + "/system_stats", timeout=2
                    )
                    if _response_ok(resp):
                        with self.lock:
                            task.check()
                            if (
                                self.proc is not proc
                                or getattr(self.runtime, "quiescing", False)
                                or (
                                    isinstance(
                                        getattr(self.runtime, "stopping", None),
                                        threading.Event,
                                    )
                                    and self.runtime.stopping.is_set()
                                )
                            ):
                                from server.runtime import ColabOneCancelledError

                                raise ColabOneCancelledError("实例正在停止，已取消启动 ComfyUI")
                            self.state = "ready"
                            self.error = ""
                            self._queue_cache = (0, 0)
                            self._queue_checked_at = -10.0
                            self._loaded_pack_signature = launch_pack_sig
                        task.note(
                            f"远端 ComfyUI 引擎已就绪（耗时 {time.monotonic() - start_t:.1f} 秒）"
                        )
                        return
                except (requests.RequestException, OSError):
                    pass
                now = time.monotonic()
                if now - last_wait_note >= 10:
                    last_wait_note = now
                    task.note(
                        f"正在等待 ComfyUI 完成节点初始化（已耗时 {int(now - start_t)} 秒）"
                    )
                time.sleep(0.5)
            raise RuntimeError("ComfyUI 启动超时（超过 180 秒），请查看运行日志")
        except BaseException as exc:
            if proc is not None:
                _terminate_process_group(proc, timeout=5.0)
            from server.runtime import ColabOneCancelledError

            is_cancelled = isinstance(exc, ColabOneCancelledError) or task.cancel.is_set()
            with self.lock:
                if self.proc is proc:
                    self.proc = None
                if is_cancelled:
                    self.state = "stopped"
                    self.error = ""
                else:
                    self.state = "error"
                    self.error = (str(exc) or type(exc).__name__)[:500]
                self._queue_cache = (0, 0)
                self._queue_checked_at = -10.0
                self._loaded_pack_signature = ""
            raise

    def stop_server(self) -> None:
        try:
            self.canvas.stop()
        finally:
            with self.lock:
                proc = self.proc
            try:
                if proc is not None:
                    _terminate_process_group(proc, timeout=5.0)
            finally:
                with self.lock:
                    self.proc = None
                    self.state = "stopped"
                    self.error = ""
                    self._queue_cache = (0, 0)
                    self._queue_checked_at = -10.0
                    self._loaded_pack_signature = ""

    def free_memory(self, unload_models: bool = True) -> dict:
        if not self.is_running():
            return {"ok": True, "running": False}
        resp = self.runtime.http.post(
            self.base_url + "/free",
            json={"unload_models": bool(unload_models), "free_memory": True},
            timeout=10,
        )
        resp.raise_for_status()
        return {"ok": True, "running": True}

    def start_canvas(self, config: dict) -> dict:
        if not self.is_running():
            raise RuntimeError("请先启动 ComfyUI 引擎后再开启原生画布")
        return self.canvas.start(config)

    def stop_canvas(self) -> dict:
        return self.canvas.stop()

    def canvas_url(self) -> dict:
        return self.canvas.auth_url()

    def _stage_input_file(self, filename: str, raw_bytes: bytes) -> str:
        _ensure_dir(self.io_in_dir, "ComfyUI 输入目录")
        safe_name = _safe_basename(filename)
        if not safe_name:
            raise ValueError("输入文件名不合法")
        dest = self.io_in_dir / safe_name
        if dest.is_symlink():
            raise ValueError("输入文件不能是符号链接")
        dest.write_bytes(raw_bytes)
        return safe_name

    def _cancel_prompt(self, prompt_id: str) -> None:
        if not prompt_id:
            return
        try:
            self.runtime.http.post(
                self.base_url + "/queue",
                json={"delete": [prompt_id]},
                timeout=3,
            )
        except Exception:
            pass
        try:
            resp = self.runtime.http.post(
                self.base_url + "/interrupt",
                json={"prompt_id": prompt_id},
                timeout=3,
            )
            if not _response_ok(resp):
                self.runtime.http.post(self.base_url + "/interrupt", timeout=3)
        except Exception:
            pass

    def ensure_template_ready(
        self,
        task,
        wf: dict,
        hf_token: str = "",
        progress_range: tuple[float, float] | None = None,
    ) -> None:
        task.check()
        has_pack_requirements = bool(
            wf.get("custom_nodes")
            or wf.get("pip_packages")
            or wf.get("models")
            or wf.get("node_patches")
            or (wf.get("min_vram_mib") or 0) > 0
            or (wf.get("min_disk_mib") or 0) > 0
        )
        if has_pack_requirements:
            self.packs.check_preflight(wf)
            if not self.is_running():
                self.prepare(task)
                task.check()
            ensure_kwargs: dict[str, Any] = {"hf_token": hf_token}
            if progress_range is not None:
                try:
                    sig = inspect.signature(self.packs.ensure_pack)
                    if "progress_range" in sig.parameters or any(
                        p.kind == inspect.Parameter.VAR_KEYWORD
                        for p in sig.parameters.values()
                    ):
                        ensure_kwargs["progress_range"] = progress_range
                except (TypeError, ValueError):
                    ensure_kwargs["progress_range"] = progress_range
            needs_restart = self.packs.ensure_pack(task, wf, **ensure_kwargs)
            current_pack_sig = str(
                getattr(self.packs, "active_pack_signature", "") or ""
            )
            with self.lock:
                loaded_pack_sig = self._loaded_pack_signature
            if self.is_running() and (
                needs_restart or loaded_pack_sig != current_pack_sig
            ):
                task.note("自定义节点包已变更，正在重启远端 ComfyUI 引擎以加载新节点")
                self.stop_server()
        if not self.is_running():
            self.start_server(task)
        task.check()

    def _fetch_output_to_path(
        self,
        task,
        filename: str,
        dest_path: Path,
        *,
        subfolder: str = "",
        file_type: str = "output",
    ) -> Path:
        """Stream a ComfyUI `/view` artifact directly to disk without buffering it in RAM."""
        dest = Path(dest_path)
        if dest.is_symlink():
            raise ValueError("输出缓存文件不能是符号链接")
        dest.parent.mkdir(parents=True, exist_ok=True)
        view_resp = self.runtime.http.get(
            self.base_url + "/view",
            params={
                "filename": filename,
                "subfolder": subfolder,
                "type": file_type,
            },
            stream=True,
            timeout=(10, 60),
        )
        total = 0
        try:
            view_resp.raise_for_status()
            is_streamable_requests_resp = (
                isinstance(view_resp, requests.Response)
                and getattr(view_resp, "raw", None) is not None
                and not getattr(view_resp, "_content_consumed", False)
            )
            is_streamable_stub = False
            if not isinstance(view_resp, requests.Response):
                from unittest.mock import NonCallableMock

                is_streamable_stub = (
                    not isinstance(view_resp, NonCallableMock)
                    and callable(getattr(view_resp, "iter_content", None))
                    and not isinstance(
                        getattr(view_resp, "content", None), (bytes, bytearray)
                    )
                )
            with dest.open("wb") as out:
                if is_streamable_requests_resp or is_streamable_stub:
                    for chunk in view_resp.iter_content(256 * 1024):
                        task.check()
                        if not chunk:
                            continue
                        total += len(chunk)
                        if total > MAX_OUTPUT_BYTES:
                            raise ValueError("生成结果超过最大体积限制")
                        out.write(chunk)
                else:
                    raw_bytes = bytes(view_resp.content)
                    if len(raw_bytes) > MAX_OUTPUT_BYTES:
                        raise ValueError("生成结果超过最大体积限制")
                    out.write(raw_bytes)
            return dest
        except BaseException:
            dest.unlink(missing_ok=True)
            raise
        finally:
            close_fn = getattr(view_resp, "close", None)
            if callable(close_fn):
                close_fn()

    def _upload_input_file(self, fname: str, fpath: Path) -> str:
        raw_bytes = fpath.read_bytes()
        ext = fpath.suffix.lstrip(".").lower()
        uploaded_name = ""
        try:
            resp = self.runtime.http.post(
                self.base_url + "/upload/image",
                files={"image": (fname, raw_bytes)},
                data={"overwrite": "true"},
                timeout=30,
            )
            if _response_ok(resp):
                body = resp.json() or {}
                subfolder = str(body.get("subfolder") or "").strip("/")
                name = str(body.get("name") or fname)
                uploaded_name = f"{subfolder}/{name}" if subfolder else name
            elif ext not in IMAGE_INPUT_EXTENSIONS:
                uploaded_name = self._stage_input_file(fname, raw_bytes)
            else:
                resp.raise_for_status()
        except requests.RequestException:
            if ext not in IMAGE_INPUT_EXTENSIONS:
                uploaded_name = self._stage_input_file(fname, raw_bytes)
            else:
                raise
        return uploaded_name

    def _execute_single_prompt(
        self,
        task,
        wf: dict,
        prompt_payload: dict,
        *,
        progress_range: tuple[float, float] = (0.08, 0.96),
        segment_label: str = "",
        save_to_store: bool = True,
        output_dir: Path | None = None,
    ) -> list[dict]:
        from server.runtime import ColabOneCancelledError

        task.check()
        lo, hi = progress_range
        lo = max(0.0, min(1.0, float(lo)))
        hi = max(lo, min(1.0, float(hi)))
        span = hi - lo
        sampler_hi = lo + span * 0.92
        prefix = f"{segment_label} · " if segment_label else ""
        task_lock = getattr(task, "lock", None)

        node_titles: dict[str, str] = {}
        for node_id, node in (prompt_payload or {}).items():
            if not isinstance(node, dict):
                continue
            meta = node.get("_meta")
            raw_title = meta.get("title") if isinstance(meta, dict) else None
            if isinstance(raw_title, str) and raw_title.strip():
                node_titles[str(node_id)] = raw_title.strip()
            elif (
                isinstance(node.get("class_type"), str)
                and node["class_type"].strip()
            ):
                node_titles[str(node_id)] = node["class_type"].strip()

        client_id = (
            f"colabone-{task.id}-{uuid.uuid4().hex[:8]}"
            if (segment_label or not save_to_store)
            else f"colabone-{task.id}"
        )

        ws_lock = threading.Lock()
        ws_wakeup = threading.Event()
        ws_state: dict[str, Any] = {
            "active": False,
            "last_node": "",
            "last_note_at": 0.0,
            "done": False,
            "error": "",
        }
        cancel_state: dict[str, Any] = {
            "prompt_id": "",
            "finished": False,
            "interrupted": False,
        }

        phase_labels = {
            "starting": "正在启动执行",
            "cached": "复用已缓存节点",
            "executing": "远端 GPU 执行中",
            "executed": "节点已完成",
            "sampling": "采样中",
        }

        def _on_ws_progress(event: dict) -> None:
            if not isinstance(event, dict):
                return
            node_title = str(
                event.get("node_title") or event.get("node_id") or ""
            ).strip()
            step = event.get("step")
            max_steps = event.get("max_steps")
            fraction = event.get("fraction")
            phase = str(event.get("phase") or "").strip()

            if (
                isinstance(step, int)
                and not isinstance(step, bool)
                and isinstance(max_steps, int)
                and not isinstance(max_steps, bool)
                and (max_steps > 1 or (phase == "sampling" and max_steps > 0))
            ):
                step_part = f" · 采样 {step}/{max_steps}"
            else:
                step_part = ""

            if node_title:
                msg = f"{prefix}{wf['name']} · {node_title}{step_part}"
            elif phase:
                label = phase_labels.get(phase, phase)
                msg = f"{prefix}{wf['name']} · {label}{step_part}"
            else:
                msg = f"{prefix}{wf['name']} · 远端 GPU 执行中{step_part}"

            now = time.monotonic()
            should_note = False
            with ws_lock:
                ws_state["active"] = True
                node_key = f"{node_title}:{phase}"
                if node_title and node_key != ws_state["last_node"]:
                    ws_state["last_node"] = node_key
                    ws_state["last_note_at"] = now
                    should_note = True
                elif now - ws_state["last_note_at"] >= 5.0:
                    ws_state["last_note_at"] = now
                    should_note = True

            with task_lock if task_lock is not None else nullcontext():
                task.message = msg
                if isinstance(fraction, (int, float)) and not isinstance(
                    fraction, bool
                ):
                    clamped = max(0.0, min(1.0, float(fraction)))
                    scaled = lo + span * 0.92 * clamped
                    if scaled > task.progress:
                        task.progress = min(sampler_hi, scaled)
            if should_note:
                task.note(msg)

        def _on_ws_error(err: Any = None) -> None:
            with ws_lock:
                ws_state["error"] = str(err) if err else "工作流执行出错"
            ws_wakeup.set()

        def _on_ws_done(_event: Any = None) -> None:
            with ws_lock:
                ws_state["done"] = True
            ws_wakeup.set()

        ws_monitor = None
        has_cancel_hook_attr = hasattr(task, "cancel_hook")
        prev_cancel_hook = None
        if has_cancel_hook_attr:
            with task_lock if task_lock is not None else nullcontext():
                prev_cancel_hook = task.cancel_hook

        def _cancel_hook() -> None:
            ws_wakeup.set()
            if callable(prev_cancel_hook):
                try:
                    prev_cancel_hook()
                except Exception:
                    pass
            with ws_lock:
                pid = cancel_state["prompt_id"]
                if pid and not cancel_state["finished"] and not cancel_state["interrupted"]:
                    cancel_state["interrupted"] = True
                else:
                    pid = ""
            if pid:
                self._cancel_prompt(pid)
            if ws_monitor is not None:
                try:
                    ws_monitor.stop()
                except Exception:
                    pass

        if has_cancel_hook_attr:
            with task_lock if task_lock is not None else nullcontext():
                task.cancel_hook = _cancel_hook

        try:
            task.check()
            try:
                ws_monitor = ColabOneComfyWsMonitor(
                    "127.0.0.1",
                    self.port,
                    client_id,
                    node_titles=node_titles,
                    on_progress=_on_ws_progress,
                    on_error=_on_ws_error,
                    on_done=_on_ws_done,
                )
                if task.cancel.is_set():
                    task.check()
                ws_monitor.start()
                if task.cancel.is_set():
                    try:
                        ws_monitor.stop()
                    except Exception:
                        pass
                    task.check()
            except ColabOneCancelledError:
                raise
            except Exception:
                ws_monitor = None

            submit_msg = f"{prefix}提交工作流：{wf['name']}"
            with task_lock if task_lock is not None else nullcontext():
                task.message = submit_msg
            task.note(submit_msg)
            task.check()
            submit_resp = self.runtime.http.post(
                self.base_url + "/prompt",
                json={"prompt": prompt_payload, "client_id": client_id},
                timeout=30,
            )
            try:
                submit_body = submit_resp.json() or {}
            except ValueError:
                submit_body = {}
            if not _response_ok(submit_resp):
                detail = _format_comfy_errors(submit_body)
                if detail:
                    raise RuntimeError(f"ComfyUI 拒绝了工作流：{detail}")
                submit_resp.raise_for_status()
            node_errors = submit_body.get("node_errors")
            if isinstance(node_errors, dict) and node_errors:
                detail = _format_comfy_errors(submit_body)
                raise RuntimeError(f"工作流节点校验失败：{detail}")
            prompt_id = str(submit_body.get("prompt_id") or "")
            if not prompt_id or not _PROMPT_ID_RE.fullmatch(prompt_id):
                detail = _format_comfy_errors(submit_body)
                raise RuntimeError(
                    f"ComfyUI 未返回有效任务编号：{detail or '未知错误'}"
                )

            with ws_lock:
                cancel_state["prompt_id"] = prompt_id

            if ws_monitor is not None:
                set_pid = getattr(ws_monitor, "set_prompt_id", None)
                if callable(set_pid):
                    try:
                        set_pid(prompt_id)
                    except Exception:
                        pass

            started = time.monotonic()
            last_phase: str | None = None
            last_note = 0.0
            hist_errors = 0
            missing_polls = 0
            prompt_finished = False
            task.note(f"{prefix}工作流已入队：{wf['name']}（任务 {prompt_id[:16]}）")

            try:
                while True:
                    task.check()
                    with self.lock:
                        proc = self.proc
                    if proc is not None and proc.poll() is not None:
                        raise RuntimeError("ComfyUI 进程在执行期间意外退出")

                    try:
                        hist_resp = self.runtime.http.get(
                            f"{self.base_url}/history/{prompt_id}", timeout=15
                        )
                        hist_resp.raise_for_status()
                        hist_data = hist_resp.json() or {}
                        hist_errors = 0
                    except (requests.RequestException, OSError, ValueError) as exc:
                        hist_errors += 1
                        if hist_errors >= 5:
                            raise RuntimeError(
                                f"查询 ComfyUI 任务进度连续失败：{exc}"
                            ) from None
                        time.sleep(1.0)
                        continue

                    if prompt_id in hist_data:
                        prompt_finished = True
                        with ws_lock:
                            cancel_state["finished"] = True
                        entry = hist_data[prompt_id] or {}
                        status_info = entry.get("status") or {}
                        status_str = str(status_info.get("status_str") or "").strip().lower()
                        has_interrupted_msg = any(
                            isinstance(msg, (list, tuple))
                            and len(msg) >= 1
                            and msg[0] == "execution_interrupted"
                            for msg in (status_info.get("messages") or [])
                        )
                        if (
                            status_str == "error"
                            or status_info.get("completed") is False
                            or has_interrupted_msg
                        ):
                            raise RuntimeError(
                                "ComfyUI 执行失败："
                                + _extract_history_error(status_info)
                            )

                        output_items: list[tuple[str, dict[str, Any]]] = []
                        seen: set[tuple[str, str, str]] = set()
                        for node_out in (entry.get("outputs") or {}).values():
                            if not isinstance(node_out, dict):
                                continue
                            for media_key in ("images", "gifs", "videos", "audio"):
                                items = node_out.get(media_key)
                                if not isinstance(items, list):
                                    continue
                                for item in items:
                                    if not isinstance(item, dict) or not item.get(
                                        "filename"
                                    ):
                                        continue
                                    key = (
                                        str(item["filename"]),
                                        str(item.get("subfolder") or ""),
                                        str(item.get("type") or "output"),
                                    )
                                    if key in seen:
                                        continue
                                    seen.add(key)
                                    output_items.append((media_key, item))

                        elapsed = time.monotonic() - started
                        total_items = len(output_items)
                        if total_items and save_to_store:
                            task.note(
                                f"{prefix}工作流推理完成，正在保存 {total_items} 个输出作品"
                            )
                        raw_results: list[dict[str, Any]] = []
                        for idx, (media_key, item) in enumerate(output_items, 1):
                            task.check()
                            fname = str(item["filename"])
                            subfolder = str(item.get("subfolder") or "")
                            file_type = str(item.get("type") or "output")
                            safe_fname = _safe_basename(fname) or f"output_{idx:03d}.bin"
                            if save_to_store:
                                item_msg = (
                                    f"{prefix}保存作品 {idx}/{total_items} · "
                                    f"{safe_fname}"
                                )
                            else:
                                item_msg = (
                                    f"{prefix}获取输出 {idx}/{total_items} · "
                                    f"{safe_fname}"
                                )
                            with task_lock if task_lock is not None else nullcontext():
                                task.message = item_msg
                            if save_to_store:
                                tmp_dl = (
                                    self.store.outputs_dir
                                    / f".dl-{uuid.uuid4().hex[:12]}.tmp"
                                )
                                try:
                                    self._fetch_output_to_path(
                                        task,
                                        fname,
                                        tmp_dl,
                                        subfolder=subfolder,
                                        file_type=file_type,
                                    )
                                    record = self.store.save_output_file(
                                        src_path=tmp_dl,
                                        workflow_id=wf["id"],
                                        workflow_name=wf["name"],
                                        filename=fname,
                                        elapsed=elapsed,
                                        prompt_id=prompt_id,
                                    )
                                    with (
                                        task_lock
                                        if task_lock is not None
                                        else nullcontext()
                                    ):
                                        task.outputs.append(record)
                                finally:
                                    tmp_dl.unlink(missing_ok=True)
                            elif output_dir is not None:
                                dl_dest = (
                                    Path(output_dir)
                                    / f"{prompt_id[:16]}_{idx:03d}_{safe_fname}"
                                )
                                if Path(output_dir).resolve() not in dl_dest.resolve().parents:
                                    raise ValueError("输出文件路径不合法")
                                self._fetch_output_to_path(
                                    task,
                                    fname,
                                    dl_dest,
                                    subfolder=subfolder,
                                    file_type=file_type,
                                )
                                raw_results.append(
                                    {
                                        "filename": fname,
                                        "path": str(dl_dest),
                                        "media_key": media_key,
                                        "prompt_id": prompt_id,
                                        "subfolder": subfolder,
                                        "type": file_type,
                                    }
                                )
                            else:
                                tmp_dl = (
                                    self.store.outputs_dir
                                    / f".seg-{uuid.uuid4().hex[:12]}.tmp"
                                )
                                try:
                                    self._fetch_output_to_path(
                                        task,
                                        fname,
                                        tmp_dl,
                                        subfolder=subfolder,
                                        file_type=file_type,
                                    )
                                    raw_output = tmp_dl.read_bytes()
                                finally:
                                    tmp_dl.unlink(missing_ok=True)
                                raw_results.append(
                                    {
                                        "filename": fname,
                                        "bytes": raw_output,
                                        "media_key": media_key,
                                        "prompt_id": prompt_id,
                                        "subfolder": subfolder,
                                        "type": file_type,
                                    }
                                )
                            if total_items > 0:
                                fetch_prog = lo + span * (
                                    0.92 + 0.08 * (idx / total_items)
                                )
                                with task_lock if task_lock is not None else nullcontext():
                                    if fetch_prog > task.progress:
                                        task.progress = min(hi, fetch_prog)

                        if save_to_store:
                            with task_lock if task_lock is not None else nullcontext():
                                saved_outputs = list(task.outputs)
                                task.progress = 1
                            elapsed_text = format_colabone_duration_zh(
                                elapsed, subsecond=True
                            )
                            if not output_items:
                                task.note(
                                    f"工作流执行完成，未产生可保存的输出文件（耗时 {elapsed_text}）"
                                )
                            else:
                                task.note(
                                    f"工作流执行完成，已保存 {len(saved_outputs)} 个作品到远端 GPU（耗时 {elapsed_text}）"
                                )
                            return saved_outputs
                        with task_lock if task_lock is not None else nullcontext():
                            if hi > task.progress:
                                task.progress = hi
                        return raw_results

                    elapsed = time.monotonic() - started
                    if elapsed > 3600:
                        with ws_lock:
                            cancel_state["interrupted"] = True
                        self._cancel_prompt(prompt_id)
                        raise RuntimeError("工作流执行超过 60 分钟，已取消任务")

                    phase = "running"
                    pending_pos = 0
                    queue_checked = False
                    in_queue = False
                    try:
                        q_resp = self.runtime.http.get(
                            self.base_url + "/queue", timeout=5
                        )
                        if _response_ok(q_resp):
                            queue_checked = True
                            q_data = q_resp.json() or {}
                            q_running = q_data.get("queue_running") or []
                            q_pending = q_data.get("queue_pending") or []
                            in_running = any(
                                isinstance(q_entry, (list, tuple))
                                and len(q_entry) >= 2
                                and str(q_entry[1]) == prompt_id
                                for q_entry in q_running
                            )
                            if in_running:
                                in_queue = True
                            else:
                                for pos, q_entry in enumerate(q_pending, 1):
                                    if (
                                        isinstance(q_entry, (list, tuple))
                                        and len(q_entry) >= 2
                                        and str(q_entry[1]) == prompt_id
                                    ):
                                        phase = "queued"
                                        pending_pos = pos
                                        in_queue = True
                                        break
                    except (requests.RequestException, OSError, ValueError):
                        pass

                    if queue_checked:
                        if in_queue:
                            missing_polls = 0
                        else:
                            missing_polls += 1
                            if missing_polls >= 10:
                                # Final check against /history in case the prompt just finished.
                                try:
                                    final_hist = self.runtime.http.get(
                                        f"{self.base_url}/history/{prompt_id}",
                                        timeout=10,
                                    )
                                    if _response_ok(final_hist) and prompt_id in (
                                        final_hist.json() or {}
                                    ):
                                        missing_polls = 0
                                        continue
                                except (requests.RequestException, OSError, ValueError):
                                    pass
                                with ws_lock:
                                    ws_err = ws_state["error"]
                                if ws_err:
                                    raise RuntimeError("ComfyUI 执行失败：" + ws_err)
                                raise RuntimeError(
                                    "ComfyUI 任务已从执行队列消失且未生成结果，可能已在画布中被取消"
                                )

                    ws_is_connected = bool(
                        ws_monitor is not None and getattr(ws_monitor, "connected", False)
                    )
                    with ws_lock:
                        ws_active = ws_is_connected and ws_state["active"]

                    if phase == "queued":
                        elapsed_label = format_colabone_duration_zh(elapsed)
                        queued_msg = (
                            f"{prefix}{wf['name']} · 排队中（第 {pending_pos} 位）· 已用 {elapsed_label}"
                        )
                        with task_lock if task_lock is not None else nullcontext():
                            task.message = queued_msg
                            queued_progress = lo if segment_label else 0.05
                            if queued_progress > task.progress:
                                task.progress = queued_progress
                        if phase != last_phase or time.monotonic() - last_note >= 5:
                            task.note(queued_msg)
                            last_phase, last_note = phase, time.monotonic()
                    elif not ws_active:
                        elapsed_label = format_colabone_duration_zh(elapsed)
                        run_msg = (
                            f"{prefix}{wf['name']} · 远端 GPU 执行中 · 已用 {elapsed_label}"
                        )
                        heuristic = lo + span * 0.92 * (
                            1.0 - 1.0 / (1.0 + elapsed / 30.0)
                        )
                        with task_lock if task_lock is not None else nullcontext():
                            task.message = run_msg
                            if heuristic > task.progress:
                                task.progress = min(sampler_hi, heuristic)
                        if phase != last_phase or time.monotonic() - last_note >= 5:
                            task.note(run_msg)
                            last_phase, last_note = phase, time.monotonic()

                    if not task.cancel.is_set():
                        if self._history_poll_interval > 0:
                            ws_wakeup.wait(self._history_poll_interval)
                        ws_wakeup.clear()
            except ColabOneCancelledError:
                with ws_lock:
                    should_cancel = (
                        not prompt_finished
                        and not cancel_state["finished"]
                        and not cancel_state["interrupted"]
                    )
                    if should_cancel:
                        cancel_state["interrupted"] = True
                if should_cancel:
                    self._cancel_prompt(prompt_id)
                raise
            except BaseException:
                with ws_lock:
                    should_cancel = (
                        not prompt_finished
                        and not cancel_state["finished"]
                        and not cancel_state["interrupted"]
                    )
                    if should_cancel:
                        cancel_state["interrupted"] = True
                if should_cancel:
                    self._cancel_prompt(prompt_id)
                if task.cancel.is_set():
                    raise ColabOneCancelledError("操作已取消") from None
                raise
        finally:
            if has_cancel_hook_attr:
                with task_lock if task_lock is not None else nullcontext():
                    task.cancel_hook = prev_cancel_hook
            if ws_monitor is not None:
                try:
                    ws_monitor.stop()
                except Exception:
                    pass
            with self.lock:
                self._queue_cache = (0, 0)
                self._queue_checked_at = -10.0
            if self.is_running():
                try:
                    self.queue_counts()
                except Exception:
                    pass

    def run_workflow(self, task, request: WorkflowRunRequest) -> list[dict]:
        req = (
            request
            if isinstance(request, WorkflowRunRequest)
            else WorkflowRunRequest.model_validate(request)
        )
        wf = self.store.get_workflow(req.workflow_id)
        resolved_inputs: list[tuple[str, str, Path]] = []
        for field_key, input_id in (req.inputs or {}).items():
            fname, fpath = self.store.input_file(input_id)
            resolved_inputs.append((field_key, fname, fpath))

        if (wf.get("pipeline") or {}).get("kind") == "digital_human":
            return self.digital_human.run(task, wf, req, resolved_inputs)

        self.ensure_template_ready(
            task, wf, hf_token=getattr(req, "hf_token", "") or ""
        )
        task.check()

        media_map: dict[str, str] = {}
        if resolved_inputs:
            task.note(f"正在向 ComfyUI 上传 {len(resolved_inputs)} 个输入素材")
        for field_key, fname, fpath in resolved_inputs:
            task.check()
            media_map[field_key] = self._upload_input_file(fname, fpath)

        prompt_payload = apply_workflow_overrides(
            wf["workflow"], req.overrides, media_map
        )
        return self._execute_single_prompt(
            task, wf, prompt_payload, save_to_store=True
        )

    def _resolve_preview_audio_input(
        self, asset_id: str, label: str
    ) -> tuple[Path | None, str]:
        ident = str(asset_id or "").strip()
        if not ident:
            return None, ""
        if not _ASSET_ID_RE.fullmatch(ident):
            raise ValueError(f"{label}编号不合法")

        path: Path | None = None
        resolve_fn = getattr(self.store, "resolve_input_path", None)
        if callable(resolve_fn):
            resolved_raw = resolve_fn(ident)
            path = Path(resolved_raw) if resolved_raw is not None else None
        elif callable(getattr(self.store, "input_file", None)):
            _, path = self.store.input_file(ident)
        else:
            store_lock = getattr(self.store, "lock", None)
            inputs_dir = getattr(self.store, "inputs_dir", None)
            with store_lock if store_lock is not None else nullcontext():
                entry = (getattr(self.store, "inputs_index", None) or {}).get(ident)
                if isinstance(entry, dict) and entry.get("path"):
                    candidate = Path(entry["path"])
                    if inputs_dir is None or candidate.parent == Path(inputs_dir):
                        path = candidate

        if path is None or path.is_symlink() or not path.is_file():
            raise ValueError(f"{label}文件不存在或已失效，请重新上传")
        ext = path.suffix.lstrip(".").lower()
        if not ext or ext not in AUDIO_INPUT_EXTENSIONS:
            raise ValueError(f"{label}必须为音频文件")

        hasher = hashlib.sha256()
        try:
            with path.open("rb") as stream:
                while chunk := stream.read(1024 * 1024):
                    hasher.update(chunk)
        except OSError:
            raise ValueError(f"{label}文件不存在或已失效，请重新上传") from None
        return path, hasher.hexdigest()

    def _resolve_preview_inputs(self, req: ComfyTtsPreviewRequest | dict) -> dict:
        validated = (
            req
            if isinstance(req, ComfyTtsPreviewRequest)
            else ComfyTtsPreviewRequest.model_validate(req)
        )
        engine_id = str(validated.engine_id or "").strip()
        if not engine_id or engine_id not in self.tts.engines_by_id:
            raise ValueError(
                f"未知的远端 TTS 引擎：{engine_id or validated.engine_id!r}"
            )

        voice_preset = str(validated.voice_preset or "").strip()
        preview_text = str(validated.preview_text or "").strip()
        voice_prompt = str(validated.voice_prompt or "").strip()
        emotion_prompt = str(validated.emotion_prompt or "").strip()
        ref_audio_input_id = str(validated.ref_audio_input_id or "").strip()
        ref_text = str(validated.ref_text or "").strip()
        emotion_ref_input_id = str(validated.emotion_ref_input_id or "").strip()

        ref_audio_path, ref_audio_sha256 = self._resolve_preview_audio_input(
            ref_audio_input_id, "音色参考音频"
        )
        emotion_ref_path, emotion_ref_sha256 = self._resolve_preview_audio_input(
            emotion_ref_input_id, "情绪参考音频"
        )

        preview_id, resolved_text = self.tts.compute_preview_id(
            engine_id,
            voice_preset=voice_preset,
            preview_text=preview_text,
            voice_prompt=voice_prompt,
            emotion_prompt=emotion_prompt,
            ref_audio_sha256=ref_audio_sha256,
            ref_text=ref_text,
            emotion_ref_sha256=emotion_ref_sha256,
        )
        validation = self.tts.validate_tts_synthesis_inputs(
            engine_id=engine_id,
            script_text=resolved_text,
            voice_preset=voice_preset,
            ref_audio_path=ref_audio_path,
            ref_text=ref_text,
            voice_prompt=voice_prompt,
            emotion_prompt=emotion_prompt,
            emotion_ref_path=emotion_ref_path,
        )
        engine_spec = validation["engine"]
        engine_name = str(engine_spec.get("name") or engine_id)
        sample_rate = max(48000, int(engine_spec.get("sample_rate") or 48000))
        effective_voice_preset = str(validation["effective_voice_preset"] or "")
        return {
            "preview_id": preview_id,
            "resolved_text": resolved_text,
            "engine_id": engine_id,
            "engine_name": engine_name,
            "sample_rate": sample_rate,
            "voice_preset": voice_preset,
            "effective_voice_preset": effective_voice_preset,
            "preview_text": preview_text,
            "voice_prompt": voice_prompt,
            "emotion_prompt": emotion_prompt,
            "ref_audio_input_id": ref_audio_input_id,
            "ref_audio_path": ref_audio_path,
            "ref_audio_sha256": ref_audio_sha256,
            "ref_text": ref_text,
            "emotion_ref_input_id": emotion_ref_input_id,
            "emotion_ref_path": emotion_ref_path,
            "emotion_ref_sha256": emotion_ref_sha256,
        }

    def get_tts_preview(
        self, preview_id: str, offset: int = 0, length: int = 0
    ) -> dict:
        if not isinstance(preview_id, str) or not _ASSET_ID_RE.fullmatch(preview_id):
            raise ValueError("试听音频编号不合法")
        if (
            isinstance(offset, bool)
            or isinstance(length, bool)
            or not isinstance(offset, int)
            or not isinstance(length, int)
            or offset < 0
            or length < 0
            or length > 512 * 1024
        ):
            raise ValueError("试听音频读取范围不合法")
        if offset > 0 or length > 0:
            preview = self.tts.get_cached_preview_chunk(
                preview_id, offset=offset, length=length or 512 * 1024
            )
        else:
            preview = self.tts.get_cached_preview(preview_id)
        if preview is None:
            raise KeyError(preview_id)
        return preview

    def run_tts_preview(self, task, req: ComfyTtsPreviewRequest | dict) -> dict:
        from server.runtime import ColabOneCancelledError, _scrub_secret_text

        check_fn = getattr(task, "check", None)
        if callable(check_fn):
            check_fn()

        raw_token_secret = (
            req.get("hf_token", "")
            if isinstance(req, dict) and isinstance(req.get("hf_token"), str)
            else ""
        )
        hf_token = ""
        try:
            req_model = (
                req
                if isinstance(req, ComfyTtsPreviewRequest)
                else ComfyTtsPreviewRequest.model_validate(req)
            )
            hf_token = str(getattr(req_model, "hf_token", "") or "").strip()
            resolved = self._resolve_preview_inputs(req_model)
        except Exception as exc:
            raw_msg = str(exc) or type(exc).__name__
            scrubbed = _scrub_secret_text(raw_msg, raw_token_secret, hf_token)
            if scrubbed != raw_msg:
                raise ValueError(scrubbed) from None
            raise

        if callable(check_fn):
            check_fn()

        task_lock = getattr(task, "lock", None)
        note_fn = getattr(task, "note", None)

        def _note(message: str, level: str = "info") -> None:
            if not callable(note_fn):
                return
            if level == "info":
                note_fn(message)
            else:
                try:
                    note_fn(message, level=level)
                except TypeError:
                    note_fn(message)

        cached = self.tts.get_cached_preview(resolved["preview_id"])
        if cached is not None:
            with task_lock if task_lock is not None else nullcontext():
                task.progress = 1.0
                task.message = "音色试听音频已就绪"
            duration = (
                cached.get("duration_seconds")
                if isinstance(cached, dict)
                and cached.get("duration_seconds") is not None
                else (cached.get("duration") if isinstance(cached, dict) else None)
            )
            if (
                isinstance(duration, (int, float))
                and not isinstance(duration, bool)
                and duration > 0
            ):
                _note(
                    f"复用已缓存的音色试听音频（时长 {float(duration):.1f} 秒）"
                )
            else:
                _note("复用已缓存的音色试听音频")
            return cached

        engine_spec = self.tts.get_engine(resolved["engine_id"])
        allow_unavailable = self.tts.synth_fn is not None
        self.tts.check_vram(engine_spec, allow_unavailable=allow_unavailable)
        self.tts.check_disk(engine_spec, allow_unavailable=allow_unavailable)

        if callable(check_fn):
            check_fn()

        if getattr(self.runtime, "loaded", None) and callable(
            getattr(self.runtime, "_stop_engine", None)
        ):
            _note("正在卸载当前对话模型以释放显存")
            self.runtime._stop_engine()
            if callable(check_fn):
                check_fn()

        if self.is_running():
            free_msg = "正在释放 ComfyUI 显存以启动音色试听"
            with task_lock if task_lock is not None else nullcontext():
                task.message = free_msg
            _note(free_msg)
            try:
                self.free_memory(unload_models=True)
            except Exception as exc:
                scrubbed_free = _scrub_secret_text(
                    str(exc) or type(exc).__name__, raw_token_secret, hf_token
                )
                _note(
                    f"释放 ComfyUI 显存时出现非致命提示：{scrubbed_free}",
                    level="warning",
                )
            if callable(check_fn):
                check_fn()

        engine_display = resolved.get("engine_name") or resolved["engine_id"]
        synth_msg = f"正在合成音色试听音频（{engine_display}）"
        with task_lock if task_lock is not None else nullcontext():
            if getattr(task, "progress", 0) < 0.05:
                task.progress = 0.05
            task.message = synth_msg
        _note(synth_msg)

        try:
            preview = self.tts.synthesize_preview(
                task,
                engine_id=resolved["engine_id"],
                voice_preset=resolved["voice_preset"],
                preview_text=resolved["resolved_text"] or resolved["preview_text"],
                voice_prompt=resolved["voice_prompt"],
                emotion_prompt=resolved["emotion_prompt"],
                ref_audio_path=resolved["ref_audio_path"],
                ref_text=resolved["ref_text"],
                emotion_ref_path=resolved["emotion_ref_path"],
                hf_token=hf_token,
                preview_id=resolved["preview_id"],
            )
        except ColabOneCancelledError:
            raise
        except Exception as exc:
            raw_msg = str(exc) or type(exc).__name__
            scrubbed = _scrub_secret_text(raw_msg, raw_token_secret, hf_token)
            if scrubbed != raw_msg:
                if isinstance(exc, ValueError):
                    raise ValueError(scrubbed) from None
                raise RuntimeError(scrubbed) from None
            raise

        if callable(check_fn):
            check_fn()

        with task_lock if task_lock is not None else nullcontext():
            task.progress = 1.0
            task.message = "音色试听音频已就绪"

        duration = (
            preview.get("duration_seconds")
            if isinstance(preview, dict) and preview.get("duration_seconds") is not None
            else (preview.get("duration") if isinstance(preview, dict) else None)
        )
        if (
            isinstance(duration, (int, float))
            and not isinstance(duration, bool)
            and duration > 0
        ):
            _note(f"音色试听音频已就绪（时长 {float(duration):.1f} 秒）")
        else:
            _note("音色试听音频已就绪")

        return preview
