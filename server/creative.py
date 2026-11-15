"""Remote creative engine and immutable output storage.
Uses the pinned stable-diffusion.cpp native async API, never a local inference engine.
"""

from __future__ import annotations
import base64
import copy
import hashlib
import io
import os
import re
import secrets
import shutil
import signal
import subprocess
import time
import uuid
import threading
from functools import wraps

import requests
from common.files import atomic_json
from common.creative import CreativeRequest, CreationRecord

SD_COMMIT = "5dcfa79801213f23e27b3e83617350ebb8ea93aa"
SD_URL = "https://github.com/leejet/stable-diffusion.cpp.git"
ENGINE_URL = "http://127.0.0.1:8082/sdcpp/v1/"
MAX_ASSET = 64 * 1024**2


def image_bytes(encoded):
    from PIL import Image, ImageOps

    if len(encoded) > 12 * 1024**2:
        raise ValueError("参考图最大 8 MB")
    try:
        data = base64.b64decode(encoded.split(",")[-1], validate=True)
        if len(data) > 8 * 1024**2:
            raise ValueError("参考图最大 8 MB")
        with Image.open(io.BytesIO(data)) as src:
            if src.format not in ("PNG", "JPEG", "WEBP"):
                raise ValueError("Unsupported image format")
            if src.width * src.height > 16_777_216:
                raise ValueError("参考图最多 1600 万像素")
            src.load()
            normalized = ImageOps.exif_transpose(src).convert("RGB")
            out = io.BytesIO()
            normalized.save(out, format="PNG")
            return out.getvalue()
    except Exception as exc:
        raise ValueError("请使用有效的 PNG、JPEG 或 WebP 参考图（最大 8 MB）") from exc


def store_locked(fn):
    @wraps(fn)
    def call(self, *args, **kwargs):
        with self.lock:
            return fn(self, *args, **kwargs)

    return call


class CreativeStore:
    def __init__(self, root, drive):
        self.root, self.drive = root / "creations", drive
        self.lock = threading.RLock()
        if self.root.is_symlink():
            raise ValueError("作品目录不能是符号链接")
        self.root.mkdir(exist_ok=True)
        self.refs = root / "references"
        if self.refs.is_symlink():
            raise ValueError("参考图目录不能是符号链接")
        self.refs.mkdir(exist_ok=True)
        self.items = {}
        for path in self.root.glob("*.json"):
            try:
                if path.is_symlink():
                    continue
                item = CreationRecord.model_validate_json(path.read_text()).model_dump()
                if item["id"] == path.stem:
                    self.items[item["id"]] = item
            except (OSError, ValueError, KeyError):
                continue

    @store_locked
    def listing(self):
        return copy.deepcopy(
            sorted(self.items.values(), key=lambda x: x["created_at"], reverse=True)
        )

    @store_locked
    def reference(self, encoded):
        if (
            sum(p.stat().st_size for p in self.refs.glob("*.png") if not p.is_symlink())
            > 256 * 1024**2
        ):
            raise ValueError(
                "本次实例的参考图已达 256 MB，请复用已有参考图或重新启动实例"
            )
        data = image_bytes(encoded)
        identity = uuid.uuid4().hex
        (self.refs / (identity + ".png")).write_bytes(data)
        return {"id": identity}

    @store_locked
    def reference_data(self, identity):
        if not re.fullmatch(r"[a-f0-9]{32}", identity):
            raise ValueError("参考图编号不合法")
        path = self.refs / (identity + ".png")
        if not path.is_file() or path.is_symlink():
            raise ValueError("参考图已失效，请重新选择")
        return base64.b64encode(path.read_bytes()).decode()

    @store_locked
    def save(self, payload, request, model_name, seed, elapsed, frame_count=None):
        raw = base64.b64decode(payload, validate=True)
        if not raw or len(raw) > MAX_ASSET:
            raise ValueError("生成结果为空或超过 64 MB，请减少尺寸或视频长度")
        extension = "png" if request.kind == "image" else "webm"
        if request.kind == "image" and not raw.startswith(b"\x89PNG\r\n\x1a\n"):
            raise ValueError("引擎返回的图片格式不正确")
        if request.kind == "video" and not raw.startswith(b"\x1a\x45\xdf\xa3"):
            raise ValueError("引擎返回的视频格式不正确")
        identity = uuid.uuid4().hex
        path = self.root / (identity + "." + extension)
        path.write_bytes(raw)
        params = request.model_dump() | {"seed": seed, "count": 1}
        item = {
            "id": identity,
            "kind": request.kind,
            "model": model_name,
            "created_at": time.time(),
            "params": params,
            "size": len(raw),
            "sha256": hashlib.sha256(raw).hexdigest(),
            "extension": extension,
            "mime": "image/png" if extension == "png" else "video/webm",
            "elapsed": elapsed,
            "frame_count": frame_count,
            "synced": False,
        }
        atomic_json(self.root / (identity + ".json"), item)
        self.items[identity] = item
        return item

    @store_locked
    def asset(self, identity, offset=0, length=512 * 1024):
        item = self.items[identity]
        if offset < 0 or offset > item["size"] or not 1 <= length <= 512 * 1024:
            raise ValueError("作品读取范围不合法")
        path = self.root / (identity + "." + item["extension"])
        if not path.exists() and item.get("synced"):
            path = self.drive.media_file(identity + "." + item["extension"])
        if path.is_symlink():
            raise ValueError("作品不能是符号链接")
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
        }

    @store_locked
    def sync(self):
        for identity, item in list(self.items.items()):
            if item.get("synced"):
                continue
            path = self.root / (identity + "." + item["extension"])
            saved = item | {"synced": True}
            self.drive.put_media(path, saved)
            item["synced"] = True
            atomic_json(self.root / (identity + ".json"), item)

    @store_locked
    def restore(self):
        for raw in self.drive.media_list():
            item = CreationRecord.model_validate(raw).model_dump()
            identity = item["id"]
            if identity not in self.items:
                self.items[identity] = item
                atomic_json(self.root / (identity + ".json"), item)

    @store_locked
    def delete(self, identity):
        item = self.items[identity]
        if item.get("synced"):
            self.drive.trash_media(identity, item["extension"])
        trash = self.root / ".trash"
        if trash.is_symlink():
            raise ValueError("回收目录不能是符号链接")
        trash.mkdir(exist_ok=True)
        for ext in (item["extension"], "json"):
            path = self.root / (identity + "." + ext)
            if path.exists():
                path.replace(trash / path.name)
        del self.items[identity]


def run_command(args, task, log, timeout=1200, cwd=None):
    """Cancelable process group; build subprocesses cannot outlive their task."""
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
                raise RuntimeError("远端引擎准备超时，请查看运行日志")
            if time.monotonic() - noted > 5:
                noted = time.monotonic()
                tail = log.read_text(errors="replace")[-1000:].splitlines()
                if tail:
                    task.note(tail[-1][:250])
            time.sleep(0.25)
        if proc.returncode:
            raise RuntimeError(
                "远端引擎准备失败：" + log.read_text(errors="replace")[-600:]
            )
    finally:
        if proc.poll() is None:
            os.killpg(proc.pid, signal.SIGTERM)
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(proc.pid, signal.SIGKILL)
                proc.wait(timeout=5)


def prepare_engine(runtime, task):
    root = runtime.root / "creative-engine" / SD_COMMIT
    binary = root / "build/bin/sd-server"
    if binary.exists() and (root / ".complete").exists():
        task.note("复用远端已编译的 CUDA 画图 / 视频引擎")
        return binary
    if not shutil.which("nvcc") or not shutil.which("cmake") or not shutil.which("git"):
        raise RuntimeError(
            "此远端缺少 CUDA 编译工具、CMake 或 Git，请使用标准 Colab GPU 运行环境"
        )
    root.mkdir(parents=True, exist_ok=True)
    log = runtime.root / "creative-build.log"
    task.message = "首次准备远端创作引擎（编译 CUDA，可能需要数分钟）"
    task.note(task.message)
    if not (root / ".git").exists():
        run_command(["git", "init", str(root)], task, log)
        run_command(
            ["git", "-C", str(root), "remote", "add", "origin", SD_URL], task, log
        )
    run_command(
        ["git", "-C", str(root), "fetch", "--depth", "1", "origin", SD_COMMIT],
        task,
        log,
    )
    run_command(["git", "-C", str(root), "checkout", "--detach", SD_COMMIT], task, log)
    run_command(
        [
            "git",
            "-C",
            str(root),
            "submodule",
            "update",
            "--init",
            "--depth",
            "1",
            "ggml",
            "thirdparty/libwebp",
            "thirdparty/libwebm",
        ],
        task,
        log,
    )
    run_command(
        [
            "cmake",
            "-S",
            str(root),
            "-B",
            str(root / "build"),
            "-DCMAKE_BUILD_TYPE=Release",
            "-DSD_CUDA=ON",
            "-DSD_CUDA_CUTLASS_PATH:PATH=",
            "-DSD_SERVER_BUILD_FRONTEND=OFF",
        ],
        task,
        log,
    )
    run_command(
        [
            "cmake",
            "--build",
            str(root / "build"),
            "--target",
            "sd-server",
            "--parallel",
            "2",
        ],
        task,
        log,
    )
    if not binary.is_file():
        raise RuntimeError("编译未产生创作推理引擎")
    (root / ".complete").touch()
    return binary


def load(runtime, task, model):
    engine = prepare_engine(runtime, task)
    task.check()
    runtime._stop_engine()
    args = [
        str(engine),
        "--listen-ip",
        "127.0.0.1",
        "--listen-port",
        "8082",
        "--offload-to-cpu",
        "--flash-attn",
        "off,diffusion=on",
    ]
    options = {
        "model": "--diffusion-model",
        "vae": "--vae",
        "llm": "--llm",
        "t5xxl": "--t5xxl",
    }
    for file in model.files:
        args += [options[file.role], str(runtime.models_dir / model.id / file.path)]
    log = runtime.root / "creative-engine.log"
    task.message = "加载创作模型到远端 GPU"
    task.note(task.message + "：" + model.name)
    with log.open("wb") as out:
        runtime.proc = subprocess.Popen(
            args, stdout=out, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL
        )
    try:
        end = time.monotonic() + 600
        while time.monotonic() < end:
            task.check()
            if runtime.proc.poll() is not None:
                raise RuntimeError(
                    "创作模型加载失败：" + log.read_text(errors="replace")[-700:]
                )
            try:
                status = runtime.http.get(ENGINE_URL + "models/status", timeout=2)
                if status.ok and status.json().get("state") == "error":
                    raise RuntimeError(
                        "创作模型加载失败："
                        + str(status.json().get("error") or "未知引擎错误")[:500]
                    )
                if status.ok and status.json().get("state") == "ready":
                    runtime.loaded = model.id
                    task.note("远端创作模型已加载，可以开始生成")
                    return
            except requests.RequestException:
                pass
            time.sleep(1)
        raise RuntimeError("创作模型加载超过 10 分钟")
    except BaseException:
        runtime._stop_engine()
        raise


def generate(runtime, task, request: CreativeRequest):
    model = runtime.models[request.model_id]
    seed = secrets.randbelow(2147483647) if request.seed < 0 else request.seed
    for index in range(request.count):
        task.check()
        current_seed = (seed + index) % 2147483648
        payload = {
            "prompt": request.prompt,
            "negative_prompt": request.negative_prompt,
            "width": request.width,
            "height": request.height,
            "seed": current_seed,
            "strength": request.strength,
            "preview": "none",
            "vae_tiling_params": {"enabled": True},
            "sample_params": {
                "sample_method": "euler",
                "sample_steps": request.steps,
                "flow_shift": 5.0 if request.kind == "video" else 0.0,
                "guidance": {"txt_cfg": 5.0 if request.kind == "video" else 1.0},
            },
        }
        if request.reference_id:
            payload["init_image"] = runtime.creative.reference_data(
                request.reference_id
            )
        if request.kind == "video":
            payload.update(
                video_frames=request.frames, fps=request.fps, output_format="webm"
            )
        else:
            payload.update(batch_count=1, output_format="png")
        endpoint = "vid_gen" if request.kind == "video" else "img_gen"
        try:
            response = runtime.http.post(
                ENGINE_URL + endpoint, json=payload, timeout=30
            )
            response.raise_for_status()
            job_id = response.json()["id"]
            if not re.fullmatch(r"[A-Za-z0-9_-]{1,100}", job_id):
                raise RuntimeError("引擎任务编号无效")
        except BaseException:
            # Submission may have reached the engine even if the reply was lost.
            runtime._stop_engine()
            raise
        started, last_phase, last_note = time.monotonic(), None, 0
        task.note(
            f"开始生成 {index + 1}/{request.count} · {request.width}×{request.height} · {request.steps} 步 · 种子 {current_seed}"
        )
        try:
            while True:
                task.check()
                result = runtime.http.get(ENGINE_URL + "jobs/" + job_id, timeout=30)
                result.raise_for_status()
                job = result.json()
                progress = job.get("progress") or {}
                stage = progress.get("stage", "preparing")
                stages = {
                    "queued": "等待生成",
                    "waiting": "等待引擎",
                    "preparing": "准备生成",
                    "conditioning": "理解提示词",
                    "sampling": "采样",
                    "vae_encode": "处理参考图",
                    "vae_decode": "解码画面",
                    "encoding": "保存作品",
                }
                done, total = progress.get("completed"), progress.get("total")
                detail = f" · {done}/{total}" if total else ""
                task.message = f"{index + 1}/{request.count} · {stages.get(stage, stage)}{detail} · 已用 {int(time.monotonic() - started)} 秒"
                task.progress = (
                    index + (min((done or 0) / total, 0.95) if total else 0.02)
                ) / request.count
                if stage != last_phase or time.monotonic() - last_note >= 5:
                    task.note(task.message)
                    last_phase, last_note = stage, time.monotonic()
                if job["status"] == "completed":
                    data = job["result"]
                    encoded = (
                        data["b64_json"]
                        if request.kind == "video"
                        else data["images"][0]["b64_json"]
                    )
                    runtime.creative.save(
                        encoded,
                        request,
                        model.name,
                        current_seed,
                        time.monotonic() - started,
                        data.get("frame_count"),
                    )
                    task.note("作品已保存到远端；连接云盘时会自动备份")
                    break
                if job["status"] in ("failed", "cancelled"):
                    raise RuntimeError(
                        (job.get("error") or {}).get("message") or "生成已终止"
                    )
                if time.monotonic() - started > 3600:
                    raise RuntimeError("生成超过 60 分钟，已取消任务")
                time.sleep(0.5)
        except BaseException:
            try:
                runtime.http.post(ENGINE_URL + f"jobs/{job_id}/cancel", timeout=3)
            except requests.RequestException:
                pass
            finally:
                # Cancellation can be cooperative and delayed in CUDA. Stop the engine
                # before making another task eligible or reporting the GPU idle.
                runtime._stop_engine()
            raise
