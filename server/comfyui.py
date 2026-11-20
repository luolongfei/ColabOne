"""Remote ComfyUI process manager, workflow runner, and immutable output storage.
Runs exclusively on the remote GPU runtime; the desktop never runs local inference.
"""

from __future__ import annotations

import base64
import copy
import hashlib
import importlib.util
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
    WorkflowImportRequest,
    WorkflowOutputRecord,
    WorkflowRunRequest,
    WorkflowTemplate,
    apply_workflow_overrides,
)
from common.files import atomic_json
from server.comfy_proxy import ColabOneComfyCanvasGateway

MAX_TOTAL_INPUT_BYTES = 512 * 1024 * 1024  # 512 MiB
MAX_IMAGE_PIXELS = 16_777_216  # 16M pixels (4096 x 4096)
IMAGE_INPUT_EXTENSIONS = frozenset({"png", "jpg", "jpeg", "webp"})
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
        raise ValueError(f"{label}不能是符号链接")
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
            asset_path.write_bytes(data)
            atomic_json(meta_path, record)
            self.outputs[identity] = record
        if self.on_output_change:
            self.on_output_change(identity)
        return copy.deepcopy(record)

    @store_locked
    def asset(
        self, identity: str, offset: int = 0, length: int = 512 * 1024
    ) -> dict:
        item = self.outputs[identity]
        if offset < 0 or offset > item["size"] or not 1 <= length <= 512 * 1024:
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


def _terminate_process_group(proc: subprocess.Popen, timeout: float = 5.0) -> None:
    if proc.poll() is not None:
        return
    used_pg = False
    pid = getattr(proc, "pid", None)
    if isinstance(pid, int) and hasattr(os, "killpg"):
        try:
            os.killpg(pid, signal.SIGTERM)
            used_pg = True
        except (ProcessLookupError, PermissionError, OSError):
            pass
    if not used_pg:
        try:
            proc.terminate()
        except OSError:
            pass
    try:
        proc.wait(timeout=timeout)
        return
    except subprocess.TimeoutExpired:
        pass
    if used_pg and isinstance(pid, int) and hasattr(os, "killpg"):
        try:
            os.killpg(pid, signal.SIGKILL)
        except (ProcessLookupError, PermissionError, OSError):
            pass
    else:
        try:
            proc.kill()
        except OSError:
            pass
    try:
        proc.wait(timeout=timeout)
    except subprocess.TimeoutExpired:
        pass


def _run_command(args, task, log: Path, timeout: int = 1200, cwd=None) -> None:
    """Cancelable process group; subprocesses cannot outlive their task."""
    log.parent.mkdir(parents=True, exist_ok=True)
    with log.open("wb") as out:
        proc = subprocess.Popen(
            args,
            cwd=cwd,
            stdout=out,
            stderr=subprocess.STDOUT,
            stdin=subprocess.DEVNULL,
            start_new_session=True,
        )
    end, noted = time.monotonic() + timeout, 0.0
    try:
        while proc.poll() is None:
            task.check()
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
        if proc.poll() is None:
            _terminate_process_group(proc)


def _req_package_name(line: str) -> str:
    raw = line.split("#", 1)[0].strip()
    if not raw or raw.startswith("-"):
        return ""
    match = re.match(r"^([A-Za-z0-9_.-]+)", raw)
    if not match:
        return ""
    return re.sub(r"[-_.]+", "-", match.group(1)).lower()


def _filter_requirements(req_path: Path, out_path: Path, skip_torch: bool) -> bool:
    kept: list[str] = []
    for raw_line in req_path.read_text(encoding="utf-8").splitlines():
        stripped = raw_line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        if skip_torch and _req_package_name(stripped) in TORCH_SKIP_PACKAGES:
            continue
        kept.append(stripped)
    if not kept:
        return False
    out_path.write_text("\n".join(kept) + "\n", encoding="utf-8")
    return True


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
    for key in ("error", "message", "detail"):
        if status_info.get(key):
            return str(status_info[key])[:600]
    return "工作流执行出错"


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

    def is_installed(self) -> bool:
        return (
            not self.install_dir.is_symlink()
            and (self.install_dir / "main.py").is_file()
            and not (self.install_dir / "main.py").is_symlink()
            and (self.install_dir / ".complete").exists()
        )

    def is_running(self) -> bool:
        with self.lock:
            return (
                self.proc is not None
                and self.proc.poll() is None
                and self.state == "ready"
            )

    def queue_counts(self) -> tuple[int, int]:
        if not self.is_running():
            return (0, 0)
        try:
            resp = self.runtime.http.get(self.base_url + "/queue", timeout=1.5)
            if not _response_ok(resp):
                return (0, 0)
            data = resp.json() or {}
            running = data.get("queue_running") or []
            pending = data.get("queue_pending") or []
            return (len(running), len(pending))
        except Exception:
            return (0, 0)

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
            "canvas": self.canvas.snapshot(),
        }

    def prepare(self, task) -> Path:
        if self.is_installed():
            task.note("复用远端已安装的 ComfyUI " + self.catalog.tag)
            return self.install_dir
        if self.install_dir.is_symlink():
            raise ValueError("ComfyUI 目录不能是符号链接")
        if not shutil.which("git"):
            raise RuntimeError("此远端缺少 Git，无法安装 ComfyUI")
        self.install_dir.mkdir(parents=True, exist_ok=True)
        log = self.runtime.root / "comfyui-install.log"
        task.message = "首次安装远端 ComfyUI " + self.catalog.tag
        task.note(task.message)
        if not (self.install_dir / ".git").exists():
            task.note("正在初始化远端 ComfyUI 仓库目录")
            _run_command(["git", "init", str(self.install_dir)], task, log)
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
        )
        task.note("源码拉取完成，正在检出目标版本")
        _run_command(
            [
                "git",
                "-C",
                str(self.install_dir),
                "checkout",
                "--detach",
                self.catalog.commit,
            ],
            task,
            log,
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
                _run_command(
                    [
                        sys.executable,
                        "-m",
                        "pip",
                        "install",
                        "--no-input",
                        "--disable-pip-version-check",
                        "-r",
                        str(filtered_req),
                    ],
                    task,
                    log,
                )

        if not (self.install_dir / "main.py").is_file():
            raise RuntimeError("ComfyUI 安装不完整：缺少 main.py")
        (self.install_dir / ".complete").touch()
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
                args.append("--highvram")

            log = self.runtime.root / "comfyui.log"
            task.message = "正在启动远端 ComfyUI 引擎"
            task.note(f"已生成模型路径配置，正在启动远端 ComfyUI（端口 {self.port}）")
            cwd = str(self.install_dir) if self.install_dir.is_dir() else None
            with log.open("wb") as out:
                proc = subprocess.Popen(
                    args,
                    cwd=cwd,
                    stdout=out,
                    stderr=subprocess.STDOUT,
                    stdin=subprocess.DEVNULL,
                    start_new_session=True,
                )

            start_t = time.monotonic()
            last_wait_note = start_t
            deadline = start_t + 180
            while time.monotonic() < deadline:
                task.check()
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
                            self.proc = proc
                            self.state = "ready"
                            self.error = ""
                            self._queue_cache = (0, 0)
                            self._queue_checked_at = -10.0
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
                self.proc = None
                if is_cancelled:
                    self.state = "stopped"
                    self.error = ""
                else:
                    self.state = "error"
                    self.error = (str(exc) or type(exc).__name__)[:500]
                self._queue_cache = (0, 0)
                self._queue_checked_at = -10.0
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
        try:
            self.runtime.http.post(self.base_url + "/interrupt", timeout=3)
        except Exception:
            pass
        try:
            self.runtime.http.post(
                self.base_url + "/queue",
                json={"delete": [prompt_id]},
                timeout=3,
            )
        except Exception:
            pass

    def run_workflow(self, task, request: WorkflowRunRequest) -> list[dict]:
        from server.runtime import ColabOneCancelledError

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

        if not self.is_running():
            self.start_server(task)
        task.check()

        media_map: dict[str, str] = {}
        if resolved_inputs:
            task.note(f"正在向 ComfyUI 上传 {len(resolved_inputs)} 个输入素材")
        for field_key, fname, fpath in resolved_inputs:
            task.check()
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
            media_map[field_key] = uploaded_name

        prompt_payload = apply_workflow_overrides(
            wf["workflow"], req.overrides, media_map
        )

        task.message = f"提交工作流：{wf['name']}"
        task.note(task.message)
        submit_resp = self.runtime.http.post(
            self.base_url + "/prompt",
            json={"prompt": prompt_payload, "client_id": f"colabone-{task.id}"},
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
            raise RuntimeError(f"ComfyUI 未返回有效任务编号：{detail or '未知错误'}")

        started = time.monotonic()
        last_phase: str | None = None
        last_note = 0.0
        task.note(f"工作流已入队：{wf['name']}（任务 {prompt_id[:16]}）")

        try:
            while True:
                task.check()
                with self.lock:
                    proc = self.proc
                if proc is not None and proc.poll() is not None:
                    raise RuntimeError("ComfyUI 进程在执行期间意外退出")

                hist_resp = self.runtime.http.get(
                    f"{self.base_url}/history/{prompt_id}", timeout=15
                )
                hist_resp.raise_for_status()
                hist_data = hist_resp.json() or {}
                if prompt_id in hist_data:
                    entry = hist_data[prompt_id] or {}
                    status_info = entry.get("status") or {}
                    if status_info.get("status_str") == "error":
                        raise RuntimeError(
                            "ComfyUI 执行失败：" + _extract_history_error(status_info)
                        )

                    output_items: list[dict[str, Any]] = []
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
                                output_items.append(item)

                    elapsed = time.monotonic() - started
                    total_items = len(output_items)
                    if total_items:
                        task.note(f"工作流推理完成，正在保存 {total_items} 个输出作品")
                    for idx, item in enumerate(output_items, 1):
                        task.check()
                        fname = str(item["filename"])
                        subfolder = str(item.get("subfolder") or "")
                        file_type = str(item.get("type") or "output")
                        task.message = (
                            f"保存作品 {idx}/{total_items} · {_safe_basename(fname) or fname}"
                        )
                        view_resp = self.runtime.http.get(
                            self.base_url + "/view",
                            params={
                                "filename": fname,
                                "subfolder": subfolder,
                                "type": file_type,
                            },
                            timeout=60,
                        )
                        view_resp.raise_for_status()
                        record = self.store.save_output(
                            raw=view_resp.content,
                            workflow_id=wf["id"],
                            workflow_name=wf["name"],
                            filename=fname,
                            elapsed=elapsed,
                            prompt_id=prompt_id,
                        )
                        task.outputs.append(record)

                    if not output_items:
                        task.note("工作流执行完成，未产生可保存的输出文件")
                    else:
                        task.note(
                            f"工作流执行完成，已保存 {len(task.outputs)} 个作品到远端 GPU"
                        )
                    task.progress = 1
                    return task.outputs

                elapsed = time.monotonic() - started
                if elapsed > 3600:
                    self._cancel_prompt(prompt_id)
                    raise RuntimeError("工作流执行超过 60 分钟，已取消任务")

                phase = "running"
                pending_pos = 0
                try:
                    q_resp = self.runtime.http.get(
                        self.base_url + "/queue", timeout=5
                    )
                    if _response_ok(q_resp):
                        q_data = q_resp.json() or {}
                        q_running = q_data.get("queue_running") or []
                        q_pending = q_data.get("queue_pending") or []
                        in_running = any(
                            isinstance(q_entry, (list, tuple))
                            and len(q_entry) >= 2
                            and str(q_entry[1]) == prompt_id
                            for q_entry in q_running
                        )
                        if not in_running:
                            for pos, q_entry in enumerate(q_pending, 1):
                                if (
                                    isinstance(q_entry, (list, tuple))
                                    and len(q_entry) >= 2
                                    and str(q_entry[1]) == prompt_id
                                ):
                                    phase = "queued"
                                    pending_pos = pos
                                    break
                except (requests.RequestException, OSError):
                    pass

                if phase == "queued":
                    task.message = (
                        f"{wf['name']} · 排队中（第 {pending_pos} 位）· 已用 {int(elapsed)} 秒"
                    )
                    task.progress = 0.05
                else:
                    task.message = (
                        f"{wf['name']} · 远端 GPU 执行中 · 已用 {int(elapsed)} 秒"
                    )
                    task.progress = min(
                        0.92, 0.1 + 0.82 * (1.0 - 1.0 / (1.0 + elapsed / 30.0))
                    )

                if phase != last_phase or time.monotonic() - last_note >= 5:
                    task.note(task.message)
                    last_phase, last_note = phase, time.monotonic()

                time.sleep(0.5)
        except ColabOneCancelledError:
            self._cancel_prompt(prompt_id)
            raise
        except BaseException:
            if task.cancel.is_set():
                self._cancel_prompt(prompt_id)
                raise ColabOneCancelledError("操作已取消") from None
            raise
