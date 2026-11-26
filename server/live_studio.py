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
import io
import json
import os
import re
import secrets
import shutil
import socket
import threading
import time
from pathlib import Path, PurePosixPath
from typing import Any, Callable
from urllib.parse import urlparse, urlunparse

import requests
from requests.adapters import HTTPAdapter
from PIL import Image, ImageOps

from common.files import atomic_json
from common.live_studio import (
    COLABONE_LIVE_BGM_EXTENSIONS,
    COLABONE_LIVE_CLIP_EXTENSIONS,
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


def _task_check(task: Any) -> None:
    if task is not None and callable(getattr(task, "check", None)):
        task.check()


def _task_update(task: Any, progress: float, message: str) -> None:
    if task is not None and callable(getattr(task, "update", None)):
        task.update(progress=progress, message=message)


def _task_note(task: Any, message: str, level: str = "info") -> None:
    if task is not None and callable(getattr(task, "note", None)):
        task.note(message, level=level)


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

        self._tokens: dict[str, float] = {}
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
        self._last_portrait_input_id: str = ""
        self._last_hf_token: str = ""

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

    def _is_component_cached(self, comp_id: str) -> bool:
        cache_dir = self.root / "cache" / "live-studio" / comp_id
        if cache_dir.is_symlink() or not cache_dir.is_dir():
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
                    if has_ready_marker or has_verified_weight:
                        continue
                    fp = base / fname
                    if fp.is_symlink() or not fp.is_file():
                        continue
                    if fp.suffix.lower() in _CACHE_WEIGHT_SUFFIXES:
                        try:
                            if fp.stat().st_size > 0:
                                has_verified_weight = True
                        except OSError:
                            continue
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
            if not self._is_component_cached(comp_id):
                total += self._catalog_item_disk_mib(section, comp_id)
        return total + _MIN_FREE_DISK_HEADROOM_MIB

    # ------------------------------------------------------------------
    # Ephemeral WebSocket session tokens
    # ------------------------------------------------------------------

    def _prune_expired_tokens_locked(self, now: float) -> None:
        expired = [tok for tok, exp in self._tokens.items() if exp <= now]
        for tok in expired:
            self._tokens.pop(tok, None)

    def issue_session_token(
        self,
        canvas_base_url: str = "",
        *,
        ttl_seconds: float = _DEFAULT_TOKEN_TTL_S,
        allow_loopback_fallback: bool = False,
    ) -> dict[str, Any]:
        now = time.time()
        ttl = max(5.0, min(300.0, float(ttl_seconds)))
        token = secrets.token_urlsafe(32)
        expires_at = now + ttl

        with self.lock:
            self._prune_expired_tokens_locked(now)
            self._tokens[token] = expires_at

        base = (canvas_base_url or "").strip().rstrip("/")
        canvas_ready = False
        ws_url = ""
        if base:
            parsed = urlparse(base)
            scheme = "wss" if parsed.scheme == "https" else "ws"
            if parsed.netloc:
                ws_url = f"{scheme}://{parsed.netloc}{self.ws_path}"
                canvas_ready = True
            elif allow_loopback_fallback:
                ws_url = f"ws://127.0.0.1:{self.port}{self.ws_path}"
        elif allow_loopback_fallback:
            ws_url = f"ws://127.0.0.1:{self.port}{self.ws_path}"

        return {
            "token": token,
            "expires_at": expires_at,
            "ws_path": self.ws_path,
            "ws_url": ws_url,
            "canvas_ready": canvas_ready,
        }

    def verify_session_token(self, token: str, *, consume: bool = True) -> bool:
        if not isinstance(token, str) or not token:
            return False
        now = time.time()
        with self.lock:
            self._prune_expired_tokens_locked(now)
            matched_key: str | None = None
            for candidate, exp in list(self._tokens.items()):
                if hmac.compare_digest(candidate, token) and exp > now:
                    matched_key = candidate
            if matched_key is None:
                return False
            if consume:
                self._tokens.pop(matched_key, None)
            return True

    # ------------------------------------------------------------------
    # Server lifecycle (start / stop / status)
    # ------------------------------------------------------------------

    def _resolve_engine_venv_python(self, engine_id: str) -> Path | None:
        eid = validate_colabone_live_id(engine_id, "引擎 ID")
        candidate_roots: list[Path] = [
            self.root / "live-envs" / eid / ".venv",
            self.root / "tts-envs" / eid / ".venv",
            self.root / "engines" / eid / ".venv",
        ]
        comfy = getattr(self.runtime, "comfy", None) if self.runtime is not None else None
        tts_mgr = getattr(comfy, "tts", None) if comfy is not None else None
        engines_dir = getattr(tts_mgr, "engines_dir", None) if tts_mgr is not None else None
        if engines_dir is not None:
            candidate_roots.append(Path(engines_dir) / eid / ".venv")

        for venv_dir in candidate_roots:
            if venv_dir.is_symlink() or not venv_dir.is_dir():
                continue
            for rel in ("bin/python", "bin/python3", "Scripts/python.exe"):
                cand = venv_dir / rel
                if cand.is_symlink() or not cand.is_file():
                    continue
                return cand
        return None

    def _resolve_component_model_dir(self, section: str, comp_id: str) -> Path | None:
        cid = validate_colabone_live_id(comp_id, "组件 ID")
        if self._is_component_cached(cid):
            return self.root / "cache" / "live-studio" / cid

        # Also check shared Hugging Face cache snapshots under tts-envs/_hf_cache
        repo_candidates: list[str] = []
        for entry in self.catalog_dict.get(section, []):
            if isinstance(entry, dict) and entry.get("id") == cid:
                for k in ("aux_repo", "custom_voice_repo", "repo"):
                    r = str(entry.get(k) or "").strip()
                    if r and r not in repo_candidates:
                        repo_candidates.append(r)

        hf_roots: list[Path] = [self.root / "tts-envs" / "_hf_cache"]
        comfy = getattr(self.runtime, "comfy", None) if self.runtime is not None else None
        tts_mgr = getattr(comfy, "tts", None) if comfy is not None else None
        engines_dir = getattr(tts_mgr, "engines_dir", None) if tts_mgr is not None else None
        if engines_dir is not None:
            hf_roots.append(Path(engines_dir) / "_hf_cache")

        for hf_root in hf_roots:
            if hf_root.is_symlink() or not hf_root.is_dir():
                continue
            for repo in repo_candidates:
                snaps = hf_root / ("models--" + repo.replace("/", "--")) / "snapshots"
                if snaps.is_symlink() or not snaps.is_dir():
                    continue
                try:
                    for snap in sorted(snaps.iterdir(), key=lambda p: p.name, reverse=True):
                        if snap.is_dir() and not snap.is_symlink():
                            return snap
                except OSError:
                    continue
        return None

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
        self._last_hf_token = ""
        self._last_portrait_input_id = ""
        self._last_gpu_info = None
        self._last_loaded_vram_mib = 0
        self._last_synthetic = False
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
    ) -> dict[str, Any]:
        req = (
            config
            if isinstance(config, ColabOneLiveStartRequest)
            else ColabOneLiveStartRequest.model_validate(config)
        )
        session_cfg = req.to_session_config()
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

        required_disk_mib = (
            max(0, int(min_free_disk_mib))
            if min_free_disk_mib is not None
            else self._required_disk_mib(session_cfg, synthetic=synthetic)
        )
        free_disk_mib = self._free_disk_mib()
        if free_disk_mib < required_disk_mib:
            raise ValueError(
                f"磁盘空间不足（剩余 {free_disk_mib} MiB，至少需要 {required_disk_mib} MiB）"
            )

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

        # Serialize concurrent start()/stop() via _lifecycle_cond WITHOUT holding
        # _lifecycle_lock across runner.stop() / runner.start().
        with self._lifecycle_cond:
            while self._lifecycle_busy:
                self._lifecycle_cond.wait()
            self._lifecycle_busy = True
            with self.lock:
                self._stopping = True
                self._generation += 1
                prev_runner, prev_app = self._detach_session_locked()

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
                tts = self._build_tts_engine(
                    req.tts_engine,
                    synthetic=synthetic,
                    hf_token=req.hf_token,
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

                if server_factory is not None:
                    runner = server_factory(app, host="127.0.0.1", port=self.port)
                else:
                    runner = ColabOneLiveServerRunner(app, host="127.0.0.1", port=self.port)
                runner.start()

                with self.lock:
                    if self._stopping or self._generation != start_gen:
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
                        self._last_portrait_input_id = req.portrait_input_id
                        self._last_hf_token = req.hf_token
                        aborted = False
                if aborted:
                    self._teardown_detached_session(runner, app)
                    raise RuntimeError("实时直播会话在启动过程中被停止")
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
                    str(getattr(resp, "text", "") or "")[:240], req.api_key
                )
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
                str(getattr(chat_resp, "text", "") or "")[:240], req.api_key
            )
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
                elif "path" in result:
                    src_path = Path(result["path"])
                    if src_path.is_symlink() or not src_path.is_file():
                        raise ValueError(f"生成的动作文件不存在或为符号链接: {action_id}")
                    cand_ext = src_path.suffix.lower().lstrip(".")
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
