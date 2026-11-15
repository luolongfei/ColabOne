"""Remote-only model cache, task supervisor and llama.cpp engine.
CUDA deployment adapted from colab-gpu-llm-service (MIT; see third_party).
"""

from __future__ import annotations

import asyncio
import hashlib
import httpx
import json
import os
import shutil
import subprocess
import tarfile
import threading
import time
import uuid
import re
import copy
from functools import wraps
from pathlib import Path
from urllib.parse import quote

import requests

from common.files import atomic_json
from common.models import ModelSpec
from common.storage import DriveStore
from server.download import (
    Transfer,
    parallel_file,
    cached_bytes,
    metadata_path,
    RangeUnsupported,
    CHUNK,
)


def synchronized(fn):
    @wraps(fn)
    def wrapped(self, *args, **kwargs):
        with self.lock:
            return fn(self, *args, **kwargs)

    return wrapped


class Conflict(RuntimeError):
    pass


class Cancelled(RuntimeError):
    pass


class Task:
    def __init__(self, kind, model_id=""):
        self.id = uuid.uuid4().hex
        self.kind, self.model_id = kind, model_id
        self.state, self.progress, self.error = "running", 0, ""
        self.message = ""
        self.cancel = threading.Event()
        self.cancel_hook = None
        self.events = []
        self.lock = threading.Lock()
        self.created_at = time.time()
        self.transfer = None
        self.logs = []
        self.log_seq = 0

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

    def request_cancel(self):
        self.cancel.set()
        if self.cancel_hook:
            try:
                self.cancel_hook()
            except RuntimeError:
                pass  # The generation loop may have just finished.

    def check(self):
        if self.cancel.is_set():
            raise Cancelled("操作已取消")

    def snapshot(self):
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
        with self.lock:
            value["logs"] = list(self.logs)
        if self.transfer:
            value["download"] = self.transfer.snapshot()
        return value


class Runtime:
    def __init__(self, root: Path, catalog: Path, drive=None, http=None):
        self.root, self.catalog = root, catalog
        self.root.mkdir(parents=True, exist_ok=True)
        self.models_dir = root / "models"
        self.models_dir.mkdir(exist_ok=True)
        self.lock = threading.RLock()
        self.sync_lock = threading.Lock()
        self.quiescing = False
        self.auto_releasing = False
        self.http = http or requests.Session()
        self.http.trust_env = False
        self.drive = drive or DriveStore()
        self.runtime_id = os.environ.get("COLABONE_RUNTIME_ID", uuid.uuid4().hex)
        self.models = {
            m.id: m
            for m in map(
                ModelSpec.model_validate,
                json.loads((catalog / "presets.json").read_text(encoding="utf-8")),
            )
        }
        self.tasks: dict[str, Task] = {}
        self.conversations = {}
        self.dirty = set()
        self.sync_error = ""
        self.loaded = None
        self.proc = None
        self.chat_transport = None
        self.last_active = time.time()
        self.idle_minutes = 3
        self.idle_countdown_seconds = 60
        self.activity_until = 0.0
        self.deadline = 0.0
        self.stopping = threading.Event()
        for path in root.glob("conversation-*.json"):
            data = json.loads(path.read_text(encoding="utf-8"))
            # A restart cannot continue a lost generation; preserve its partial answer.
            for message in data.get("messages", []):
                if message.get("status") == "streaming":
                    message["status"] = "interrupted"
            self.conversations[data["id"]] = data
            self.dirty.add(data["id"])
        if (root / "models.json").exists():
            for value in json.loads((root / "models.json").read_text(encoding="utf-8")):
                model = ModelSpec.model_validate(value)
                self.models[model.id] = model
            self.dirty.add("models")

    def busy(self):
        return any(t.state == "running" for t in self.tasks.values())

    @synchronized
    def touch(self, hold_seconds=0):
        if self.quiescing:
            raise Conflict("实例正在释放，无法延长闲置时间")
        self.last_active = time.time()
        self.activity_until = self.last_active + hold_seconds
        return self.idle_status()

    def idle_status(self, now=None):
        now = time.time() if now is None else now
        active = self.busy() or now < self.activity_until
        elapsed = (
            0 if active else max(0, now - max(self.last_active, self.activity_until))
        )
        grace = self.idle_minutes * 60
        return {
            "phase": "active"
            if active
            else "countdown"
            if elapsed >= grace
            else "watching",
            "remaining_seconds": max(0, grace + self.idle_countdown_seconds - elapsed),
            "countdown_seconds": self.idle_countdown_seconds,
            "idle_seconds": elapsed,
            "server_time": now,
        }

    @synchronized
    def claim_auto_release(self, now=None):
        now = time.time() if now is None else now
        if self.auto_releasing:
            return True
        if self.quiescing:
            return False
        budget_due = bool(self.deadline and now >= self.deadline)
        idle = self.idle_status(now)
        if budget_due or (
            idle["phase"] == "countdown" and idle["remaining_seconds"] <= 0
        ):
            self.quiescing = True
            self.auto_releasing = True
            return True
        return False

    @synchronized
    def status(self):
        if self.proc and self.proc.poll() is not None:
            self.proc, self.loaded = None, None
        idle = self.idle_status()
        return {
            "protocol": 2,
            "idle": idle,
            "id": self.runtime_id,
            "state": "releasing" if self.quiescing else "ready",
            "gpu": self.gpu_info(),
            "loaded_model": self.loaded,
            "disk_free": shutil.disk_usage(self.root).free,
            "idle_remaining": idle["remaining_seconds"],
            "idle_minutes": self.idle_minutes,
            "tasks": [t.snapshot() for t in self.tasks.values()],
            "sync": {
                "pending": len(self.dirty),
                "error": self.sync_error,
                "connected": self.drive.available(),
            },
            "deadline": self.deadline,
        }

    @staticmethod
    def gpu_info():
        try:
            r = subprocess.run(
                [
                    "nvidia-smi",
                    "--query-gpu=name,memory.total,memory.used",
                    "--format=csv,noheader,nounits",
                ],
                capture_output=True,
                text=True,
                timeout=3,
                check=True,
            )
            name, total, used = r.stdout.splitlines()[0].split(",")
            return {
                "name": name.strip(),
                "total_mib": int(total),
                "used_mib": int(used),
            }
        except (OSError, ValueError, subprocess.SubprocessError):
            return {"name": "GPU 信息不可用", "total_mib": 0, "used_mib": 0}

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

    @synchronized
    def list_models(self):
        return [
            m.model_dump()
            | {
                "downloaded": self.installed(m),
                "loaded": self.loaded == m.id,
                "size": sum(f.size for f in m.files),
            }
            for m in self.models.values()
        ]

    def add_model(self, spec):
        m = ModelSpec.model_validate(spec)
        with self.lock:
            if m.id in self.models and self.models[m.id] != m:
                raise Conflict("同名模型配置不同，请使用新模型 ID")
            self.models[m.id] = m
            atomic_json(
                self.root / "models.json",
                [x.model_dump() for x in self.models.values()],
            )
            self.dirty.add("models")
        return m.model_dump()

    @synchronized
    def list_conversations(self):
        return copy.deepcopy(
            sorted(
                self.conversations.values(), key=lambda c: c["updated_at"], reverse=True
            )
        )

    def submit(self, kind, model_id, fn):
        with self.lock:
            if self.quiescing:
                raise Conflict("实例正在准备释放")
            if self.busy():
                raise Conflict("请等待当前任务完成，或先停止它")
            t = Task(kind, model_id)
            self.tasks[t.id] = t
            self.last_active = time.time()

        def run():
            try:
                fn(t)
                t.check()
                t.state, t.progress = "completed", 1
            except Cancelled:
                t.state = "cancelled"
            except Exception as exc:
                t.state, t.error = "failed", (str(exc) or type(exc).__name__)[:500]
            finally:
                self.last_active = time.time()
                if t.transfer:
                    t.transfer.phase = t.state
                    t.note(
                        {
                            "completed": "下载完成，全部文件 SHA-256 校验通过",
                            "cancelled": "下载已取消，保留完成的分块以便续传",
                            "failed": "下载失败：" + t.error,
                        }.get(t.state, t.state)
                    )
                t.emit("done", t.snapshot())

        threading.Thread(target=run, daemon=True).start()
        return t.snapshot()

    @synchronized
    def download(self, model_id, token="", threads=8):
        if threads not in (8, 32):
            raise ValueError("下载线程数请选择 8 或 32")
        model = self.models[model_id]
        return self.submit(
            "download", model_id, lambda t: self._download(t, model, token, threads)
        )

    def _download(self, task, model, token, threads=8):
        directory = self.models_dir / model.id
        directory.mkdir(exist_ok=True)
        total = sum(f.size for f in model.files)
        task.transfer = Transfer(total, threads)
        task.transfer.count = len(model.files)
        task.note(
            f"准备下载 {model.name}：{len(model.files)} 个文件，共 {total / 1024**3:.2f} GiB，{threads} 线程，目标为远端 GPU 磁盘"
        )
        remaining = sum(
            max(
                0,
                f.size - cached_bytes(directory / (f.path + ".part"), f.size, f.sha256),
            )
            for f in model.files
            if not (directory / f.path).exists()
        )
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
            task.message = file.path
            task.note(
                f"文件 {index}/{len(model.files)} · {file.path} · {file.size / 1024**3:.2f} GiB"
            )
            headers = {"Authorization": f"Bearer {token}"} if token else {}
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
                except RangeUnsupported:
                    task.note("下载源忽略范围请求，自动切换为单连接下载", "warning")
                    partial.unlink(missing_ok=True)
                    metadata_path(partial).unlink(missing_ok=True)
                    stats.resumed = resumed_before
                    self._download_single(url, headers, partial, file, task, done)
            else:
                self._download_single(url, headers, partial, file, task, done)
            stats.phase = "verifying"
            task.message = "校验 SHA-256 · " + file.path
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
        headers = dict(headers)
        if offset:
            headers["Range"] = f"bytes={offset}-"
        stats.active = 1
        try:
            with self.http.get(
                url, headers=headers, stream=True, timeout=(10, 10)
            ) as response:
                response.raise_for_status()
                if offset and response.status_code != 206:
                    stats.resumed -= offset
                    offset = 0
                if response.status_code == 206 and not response.headers.get(
                    "Content-Range", ""
                ).startswith(f"bytes {offset}-"):
                    raise RuntimeError("下载范围响应不匹配，请重试")
                with partial.open("ab" if offset else "wb") as stream:
                    for block in response.iter_content(1024**2):
                        task.check()
                        if offset + len(block) > file.size:
                            raise RuntimeError("远端文件大于清单声明的大小")
                        stream.write(block)
                        offset += len(block)
                        stats.wire(len(block))
                        stats.downloaded = done + offset
                        task.progress = (done + offset) / stats.total
        finally:
            stats.active = 0

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
            task.message = "安装远端 CUDA 推理引擎"
            target = root / f"asset-{i}.tar.gz"
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
            target.unlink()
        binaries = list(root.rglob("llama-server"))
        if not binaries:
            raise RuntimeError("推理引擎包中没有 llama-server")
        (root / ".complete").touch()
        return binaries[0]

    @synchronized
    def load(self, model_id):
        m = self.models[model_id]
        if not self.installed(m):
            raise Conflict("请先下载模型到当前远端实例")
        return self.submit("load", model_id, lambda t: self._load(t, m))

    def _load(self, task, m):
        task.note(f"准备加载 {m.name}，上下文 {m.context_length} tokens")
        gpu = self.gpu_info()
        if not gpu["total_mib"]:
            raise RuntimeError("未检测到可用远端 GPU")
        if gpu["total_mib"] < m.min_vram_mib:
            raise RuntimeError(f"模型需要至少 {m.min_vram_mib} MiB 显存")
        task.note(
            f"远端设备 {gpu.get('name', 'GPU')}，显存 {gpu['total_mib'] / 1024:.1f} GiB"
        )
        engine = self.prepare_engine(task)
        task.check()
        self._stop_engine()
        file = next(f for f in m.files if f.role == "model")
        env = dict(os.environ)
        lib_dirs = {str(p.parent) for p in engine.parent.parent.rglob("*.so*")}
        env["LD_LIBRARY_PATH"] = (
            ":".join(sorted(lib_dirs)) + ":" + env.get("LD_LIBRARY_PATH", "")
        )
        args = [
            str(engine),
            "--host",
            "127.0.0.1",
            "--port",
            "8081",
            "--model",
            str(self.models_dir / m.id / file.path),
            "--alias",
            m.id,
            "--ctx-size",
            str(m.context_length),
            "--n-gpu-layers",
            "99",
            "--parallel",
            "1",
            "--jinja",
        ]
        if m.cpu_moe_layers:
            args.extend(["--n-cpu-moe", str(m.cpu_moe_layers)])
        log = self.root / "engine.log"
        task.note("启动 llama.cpp，将模型加载到远端显存")
        with log.open("wb") as stream:
            self.proc = subprocess.Popen(
                args,
                env=env,
                stdout=stream,
                stderr=subprocess.STDOUT,
                stdin=subprocess.DEVNULL,
            )
        try:
            deadline = time.monotonic() + 600
            while time.monotonic() < deadline:
                task.check()
                if self.proc.poll() is not None:
                    raise RuntimeError(
                        "模型加载失败：" + log.read_text(errors="replace")[-1200:]
                    )
                try:
                    if (
                        self.http.get(
                            "http://127.0.0.1:8081/health", timeout=2
                        ).status_code
                        == 200
                    ):
                        self.loaded = m.id
                        task.note("模型加载成功，远端推理服务已就绪")
                        return
                except requests.RequestException:
                    pass
                task.message = "加载到远端显存"
                time.sleep(1)
            raise RuntimeError("模型加载超过 10 分钟")
        except BaseException:
            self._stop_engine()
            raise

    def _stop_engine(self):
        if self.proc:
            self.proc.terminate()
            try:
                self.proc.wait(timeout=15)
            except subprocess.TimeoutExpired:
                self.proc.kill()
                self.proc.wait(timeout=5)
        self.proc, self.loaded = None, None

    def unload(self):
        with self.lock:
            if self.busy():
                raise Conflict("请先停止当前任务")
            self._stop_engine()
            self.last_active = time.time()

    def delete(self, model_id):
        with self.lock:
            if self.busy() or self.loaded == model_id:
                raise Conflict("请先停止任务并卸载此模型")
            # model_id comes from the validated catalog; callers cannot choose a path.
            m = self.models[model_id]
            shutil.rmtree(self.models_dir / m.id, ignore_errors=True)

    @synchronized
    def save_conversation(self, convo):
        if not re.fullmatch(r"[a-zA-Z0-9_-]{1,80}", convo["id"]):
            raise ValueError("会话 ID 不合法")
        convo["updated_at"] = time.time()
        self.conversations[convo["id"]] = copy.deepcopy(convo)
        atomic_json(self.root / f"conversation-{convo['id']}.json", convo)
        self.dirty.add(convo["id"])

    @synchronized
    def chat(self, conversation_id, text):
        if not self.loaded:
            raise Conflict("请先加载模型")
        if not text.strip() or len(text) > 100000:
            raise ValueError("消息为空或过长")
        if conversation_id and conversation_id not in self.conversations:
            raise KeyError("会话不存在")
        convo = self.conversations.get(conversation_id) or {
            "id": uuid.uuid4().hex,
            "title": text[:40],
            "messages": [],
        }
        model_id = self.loaded

        def generate(task):
            convo["messages"].append(
                {"role": "user", "content": text, "status": "complete"}
            )
            answer = {
                "role": "assistant",
                "content": "",
                "status": "streaming",
                "model": model_id,
            }
            payload = {
                "model": model_id,
                "messages": [
                    {"role": x["role"], "content": x["content"]}
                    for x in convo["messages"]
                ],
                "stream": True,
                "temperature": self.models[model_id].temperature,
            }
            convo["messages"].append(answer)
            self.save_conversation(convo)
            task.emit("conversation", {"id": convo["id"]})

            async def consume():
                loop = asyncio.get_running_loop()
                current = asyncio.current_task()
                task.cancel_hook = lambda: loop.call_soon_threadsafe(current.cancel)
                try:
                    task.check()
                    timeout = httpx.Timeout(connect=10, read=300, write=30, pool=10)
                    async with httpx.AsyncClient(
                        trust_env=False, timeout=timeout, transport=self.chat_transport
                    ) as client:
                        async with client.stream(
                            "POST",
                            "http://127.0.0.1:8081/v1/chat/completions",
                            json=payload,
                        ) as response:
                            response.raise_for_status()
                            checkpoint = time.monotonic()
                            finished = False
                            async for line in response.aiter_lines():
                                task.check()
                                if not line.startswith("data:"):
                                    continue
                                value = line[5:].strip()
                                if value == "[DONE]":
                                    finished = True
                                    break
                                data = json.loads(value)
                                if "error" in data:
                                    raise RuntimeError(str(data["error"]))
                                choice = (data.get("choices") or [{}])[0]
                                if choice.get("finish_reason"):
                                    finished = True
                                delta = choice.get("delta", {})
                                content = delta.get("content") or ""
                                answer["content"] += content
                                # Thinking-capable engines may stream reasoning separately.
                                reasoning = delta.get("reasoning_content") or ""
                                if reasoning:
                                    answer["reasoning"] = (
                                        answer.get("reasoning", "") + reasoning
                                    )
                                task.emit(
                                    "delta",
                                    {"content": content, "reasoning": reasoning},
                                )
                                if time.monotonic() - checkpoint > 3:
                                    self.save_conversation(convo)
                                    checkpoint = time.monotonic()
                            if not finished:
                                raise RuntimeError("生成连接提前断开；已保留收到的内容")
                            task.check()
                            answer["status"] = "complete"
                except asyncio.CancelledError:
                    raise Cancelled("生成已停止") from None
                finally:
                    task.cancel_hook = None

            try:
                asyncio.run(consume())
            except BaseException:
                answer["status"] = (
                    "cancelled" if task.cancel.is_set() else "interrupted"
                )
                raise
            finally:
                self.save_conversation(convo)

        task = self.submit("chat", model_id, generate)
        return task | {"conversation_id": convo["id"]}

    def sync(self):
        with self.sync_lock:
            return self._sync()

    def _sync(self):
        # Do not hold the runtime lock during network IO. Serialize snapshots before sending.
        with self.lock:
            keys = list(self.dirty)
        for key in keys:
            try:
                with self.lock:
                    data = (
                        [m.model_dump() for m in self.models.values()]
                        if key == "models"
                        else self.conversations[key]
                    )
                    encoded = json.dumps(data, ensure_ascii=False)
                self.drive.put(
                    "models.json" if key == "models" else f"conversation-{key}.json",
                    json.loads(encoded),
                )
                with self.lock:
                    current = (
                        [m.model_dump() for m in self.models.values()]
                        if key == "models"
                        else self.conversations[key]
                    )
                    if json.dumps(current, ensure_ascii=False) == encoded:
                        self.dirty.discard(key)
                self.sync_error = ""
            except Exception:
                self.sync_error = (
                    "Google Drive 同步失败；远端暂存仍在，请检查授权或网络"
                )
                return False
        return True

    def prepare_release(self):
        with self.lock:
            self.quiescing = True
            tasks = list(self.tasks.values())
        try:
            for task in tasks:
                if task.state == "running":
                    task.request_cancel()
            deadline = time.monotonic() + 15
            while any(t.state == "running" for t in tasks):
                if time.monotonic() >= deadline:
                    raise RuntimeError("任务仍在停止，请稍后重试释放")
                time.sleep(0.1)
            with self.sync_lock:
                if not self._sync() or self.dirty:
                    raise RuntimeError("会话尚未同步，请连接云盘，或导出后选择仍然释放")
                self.drive.flush()
            return {"ok": True}
        except Exception:
            self.quiescing = False
            raise

    def start_background(self):
        def loop():
            while not self.stopping.wait(5):
                if not self.quiescing and self.drive.available():
                    self.sync()
                if self.busy():
                    self.last_active = time.time()
                if self.claim_auto_release():
                    try:
                        self.prepare_release()
                    except Exception:
                        pass  # Budget/idle release must still proceed when Drive is offline.
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
