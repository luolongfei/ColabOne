"""Authenticated inference gateway. Runs on the GPU, separate from its control API."""

import asyncio
import base64
import hmac
import json
import queue
import secrets
import threading
import time
from collections import deque
from typing import Literal

import httpx
from fastapi import FastAPI, Request, HTTPException, Depends, Header
from fastapi.exceptions import RequestValidationError
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse, StreamingResponse, Response
from pydantic import BaseModel, ConfigDict, Field

from common.creative import CreativeRequest
from common.public_api import PublicConfig
from server.tunnel import Tunnel


class ChatMessage(BaseModel):
    model_config = ConfigDict(extra="forbid")
    role: Literal["system", "user", "assistant"]
    content: str = Field(max_length=100000)


class ChatRequest(BaseModel):
    model_config = ConfigDict(extra="forbid", allow_inf_nan=False)
    model: str = Field(min_length=1, max_length=80)
    messages: list[ChatMessage] = Field(min_length=1, max_length=256)
    stream: bool = False
    temperature: float = Field(default=0.7, ge=0, le=2)
    top_p: float = Field(default=1, gt=0, le=1)
    max_tokens: int = Field(default=2048, ge=1, le=32768)
    seed: int | None = None
    stop: str | list[str] | None = None


class ImageRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")
    model: str = Field(min_length=1, max_length=80)
    prompt: str = Field(min_length=1, max_length=12000)
    n: int = Field(default=1, ge=1, le=4)
    size: str = Field(default="1024x1024", pattern=r"^\d{3,4}x\d{3,4}$")
    response_format: Literal["b64_json"] = "b64_json"


class PublicCreative(CreativeRequest):
    # External callers cannot refer to the desktop user's private reference images.
    reference_id: Literal[""] = ""


class Gateway:
    def __init__(self, runtime):
        self.runtime = runtime
        self.enabled, self.key = False, ""
        self.phase, self.mode, self.url, self.error = "stopped", "quick", "", ""
        self.instance = secrets.token_hex(16)
        self.checked_at = 0
        self.logs, self.log_seq = deque(maxlen=100), 0
        self.jobs = {}
        self.completed, self.failed, self.total = 0, 0, 0
        self.last_request = 0
        self.lock = threading.RLock()
        self.control_lock = threading.Lock()
        self.tunnel = Tunnel(self)
        self.server = None
        self.server_thread = None

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

    def snapshot(self):
        with self.lock:
            jobs = list(self.jobs.values())
            return {
                "state": self.phase,
                "enabled": self.enabled,
                "mode": self.mode,
                "url": self.url if self.enabled else "",
                "error": self.error,
                "streaming": self.mode == "named",
                "checked_at": self.checked_at,
                "requests": self.total,
                "active": sum(j["task"].state == "running" for j in jobs),
                "completed": self.completed
                + sum(j["task"].state == "completed" for j in jobs),
                "failed": self.failed
                + sum(j["task"].state in ("failed", "cancelled") for j in jobs),
                "last_request": self.last_request,
                "logs": list(self.logs),
            }

    def serve(self, port=7861):
        if self.server_thread and self.server_thread.is_alive():
            return
        import socket
        import uvicorn

        # Bind before starting the thread: a port collision must not be mistaken for readiness.
        sock = socket.socket()
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        sock.bind(("127.0.0.1", port))
        self.port = sock.getsockname()[1]
        sock.listen(128)
        config = uvicorn.Config(
            create_public_app(self),
            host="127.0.0.1",
            port=7861,
            access_log=False,
            log_level="warning",
            limit_concurrency=32,
            timeout_graceful_shutdown=5,
        )
        self.server = uvicorn.Server(config)
        self.server_thread = threading.Thread(
            target=self.server.run, kwargs={"sockets": [sock]}, daemon=True
        )
        self.server_thread.start()
        deadline = time.monotonic() + 5
        while not self.server.started:
            if not self.server_thread.is_alive() or time.monotonic() > deadline:
                self.server.should_exit = True
                sock.close()
                raise RuntimeError("远端 API 网关未能启动")
            time.sleep(0.05)

    def start(self, value):
        config = PublicConfig.model_validate(value)
        with self.control_lock:
            if self.enabled or (self.tunnel.thread and self.tunnel.thread.is_alive()):
                raise ValueError("请先关闭当前 API 服务，再更换配置")
            with self.runtime.lock:
                if self.runtime.quiescing:
                    raise ValueError("实例正在释放")
                self.runtime.touch(180)
            self.serve()
            self.key, self.mode = config.api_key, config.mode
            self.enabled, self.phase, self.url, self.error = True, "starting", "", ""
            self.note(
                "正在开启远端 API · "
                + ("临时地址" if config.mode == "quick" else "固定域名")
            )
            self.tunnel.start(config)
        return self.snapshot()

    def cancel_jobs(self):
        with self.lock:
            for job in self.jobs.values():
                if job["task"].state == "running":
                    job["task"].request_cancel()

    def stop(self):
        with self.control_lock:
            self.enabled, self.url, self.phase = False, "", "stopping"
            self.cancel_jobs()
            self.tunnel.stop()
            self.phase, self.error, self.key = "stopped", "", ""
            self.note("对外 API 已关闭，远端模型仍可在客户端使用")
        return self.snapshot()

    def shutdown(self):
        self.stop()
        if self.server:
            self.server.should_exit = True
        if self.server_thread:
            self.server_thread.join(timeout=7)

    def require_model(self, identity, kind):
        from server.runtime import Conflict

        if not self.enabled or self.runtime.quiescing:
            raise Conflict("API 服务已关闭或实例正在释放")
        if self.runtime.loaded != identity or self.runtime.models.get(identity) is None:
            raise Conflict("请先在 ColabOne 加载请求的模型；API 不会自动下载或切换模型")
        if self.runtime.models[identity].kind != kind:
            raise ValueError("当前模型类型与接口不匹配")

    def register(self, result, slot):
        # Called under the runtime lock, making enable/model/task ownership atomic.
        with self.lock:
            while len(self.jobs) >= 100:
                old = next(
                    (k for k, v in self.jobs.items() if v["task"].state != "running"),
                    None,
                )
                if old is None:
                    raise RuntimeError("API 任务已满")
                task = self.jobs.pop(old)["task"]
                self.completed += task.state == "completed"
                self.failed += task.state in ("failed", "cancelled")
                self.runtime.tasks.pop(old, None)
            slot["task"] = self.runtime.tasks[result["id"]]
            self.jobs[result["id"]] = slot
            self.total += 1
            self.last_request = time.time()
            self.note(
                f"API 请求 #{self.total} · {slot['kind']} · 模型 {slot['task'].model_id}"
            )
        return result["id"]

    def chat(self, body):
        slot = {"kind": "chat", "result": None, "queue": queue.Queue(maxsize=256)}
        payload = body.model_dump(exclude_none=True)

        def run(task):
            async def consume():
                loop, current = asyncio.get_running_loop(), asyncio.current_task()
                task.cancel_hook = lambda: loop.call_soon_threadsafe(current.cancel)
                try:
                    task.check()
                    async with asyncio.timeout(600 if body.stream else 85):
                        async with httpx.AsyncClient(
                            trust_env=False,
                            transport=self.runtime.chat_transport,
                            timeout=httpx.Timeout(60, connect=10),
                        ) as http:
                            async with http.stream(
                                "POST",
                                "http://127.0.0.1:8081/v1/chat/completions",
                                json=payload,
                            ) as response:
                                if response.status_code != 200:
                                    raise RuntimeError(
                                        f"推理引擎返回 HTTP {response.status_code}"
                                    )
                                if body.stream:
                                    finished = False
                                    async for line in response.aiter_lines():
                                        task.check()
                                        if len(line) > 1024 * 1024:
                                            raise RuntimeError("推理响应超过大小限制")
                                        if not line.startswith("data:"):
                                            continue
                                        if line[5:].strip() == "[DONE]":
                                            finished = True
                                        while True:
                                            try:
                                                slot["queue"].put_nowait(
                                                    (line + "\n\n").encode()
                                                )
                                                break
                                            except queue.Full:
                                                task.check()
                                                await asyncio.sleep(0.02)
                                    if not finished:
                                        raise RuntimeError("推理连接提前中断")
                                else:
                                    data = bytearray()
                                    async for chunk in response.aiter_bytes():
                                        task.check()
                                        data.extend(chunk)
                                        if len(data) > 4 * 1024**2:
                                            raise RuntimeError("推理响应超过 4 MB 限制")
                                    slot["result"] = json.loads(data)
                    task.note("外部 API 聊天生成完成")
                except asyncio.CancelledError:
                    from server.runtime import Cancelled

                    raise Cancelled("API 请求已取消") from None
                except (TimeoutError, httpx.TimeoutException):
                    raise RuntimeError(
                        "推理超时；请减少 max_tokens，或使用固定域名流式调用"
                    ) from None
                except httpx.HTTPError:
                    raise RuntimeError("无法连接远端推理引擎") from None
                finally:
                    task.cancel_hook = None

            asyncio.run(consume())

        with self.runtime.lock:
            self.require_model(body.model, "chat")
            result = self.runtime.submit("public_chat", body.model, run)
            return self.register(result, slot)

    def creative(self, body):
        with self.runtime.lock:
            self.require_model(body.model_id, body.kind)
            result = self.runtime.generate(body.model_dump())
            return self.register(result, {"kind": body.kind})

    def job(self, identity):
        with self.lock:
            if identity not in self.jobs:
                raise HTTPException(404, "任务不存在或已过期")
            slot = self.jobs[identity]
            task = slot["task"]
            return {
                "id": identity,
                "object": "colabone.job",
                "kind": slot["kind"],
                "model": task.model_id,
                "status": task.state,
                "progress": task.progress,
                "error": task.error or None,
                "created_at": task.created_at,
                "outputs": [
                    {
                        "id": item["id"],
                        "mime": item["mime"],
                        "size": item["size"],
                        "content_path": f"/v1/jobs/{identity}/outputs/{item['id']}",
                    }
                    for item in task.outputs
                ],
            }

    def content(self, identity, output):
        with self.lock:
            if identity not in self.jobs or not any(
                x["id"] == output for x in self.jobs[identity]["task"].outputs
            ):
                raise HTTPException(404, "结果不存在")
        # CreativeStore already validates identity, metadata, symlinks and content hashes.
        data, offset, mime = bytearray(), 0, ""
        while True:
            chunk = self.runtime.creative.asset(output, offset, 512 * 1024)
            data.extend(base64.b64decode(chunk["data"]))
            mime = chunk["mime"]
            if not chunk["data"] and offset < chunk["size"]:
                raise HTTPException(502, "作品文件不完整")
            offset = len(data)
            if offset >= chunk["size"]:
                break
        return bytes(data), mime


class BodyLimit:
    def __init__(self, app):
        self.app = app

    async def __call__(self, scope, receive, send):
        if scope["type"] != "http":
            return await self.app(scope, receive, send)
        body = bytearray()
        while True:
            message = await receive()
            if message["type"] == "http.disconnect":
                return
            body.extend(message.get("body", b""))
            if len(body) > 1024 * 1024:
                return await JSONResponse(
                    {"error": {"message": "请求体最大 1 MB"}}, status_code=413
                )(scope, receive, send)
            if not message.get("more_body"):
                break
        sent = False

        async def replay():
            nonlocal sent
            if not sent:
                sent = True
                return {"type": "http.request", "body": bytes(body), "more_body": False}
            return await receive()

        return await self.app(scope, replay, send)


def create_public_app(g):
    from server.runtime import Conflict

    app = FastAPI(docs_url=None, redoc_url=None, openapi_url=None)
    app.add_middleware(BodyLimit)
    app.add_middleware(
        CORSMiddleware,
        allow_origins=["*"],
        allow_methods=["GET", "POST", "DELETE"],
        allow_headers=["Authorization", "Content-Type"],
        allow_credentials=False,
    )

    async def authorize(authorization: str = Header(default="")):
        if not g.enabled or not g.key:
            raise HTTPException(503, "API 服务未开启")
        if not hmac.compare_digest(
            authorization.encode(), ("Bearer " + g.key).encode()
        ):
            raise HTTPException(401, "无效的 API Key")

    auth = [Depends(authorize)]

    @app.exception_handler(HTTPException)
    async def http_error(_, exc):
        return JSONResponse(
            {
                "error": {
                    "message": str(exc.detail),
                    "type": "api_error",
                    "code": exc.status_code,
                }
            },
            status_code=exc.status_code,
        )

    @app.exception_handler(Conflict)
    async def conflict(_, exc):
        return JSONResponse(
            {"error": {"message": str(exc), "type": "model_busy"}}, status_code=409
        )

    @app.exception_handler(ValueError)
    @app.exception_handler(RequestValidationError)
    async def invalid(_, exc):
        # Do not echo request bodies, prompts or secrets in validation errors.
        return JSONResponse(
            {
                "error": {
                    "message": "请求参数不受支持，请查看 ColabOne 的 API 接入说明",
                    "type": "invalid_request_error",
                }
            },
            status_code=400,
        )

    @app.exception_handler(KeyError)
    async def missing(_, exc):
        return JSONResponse(
            {"error": {"message": "结果不存在或已删除"}}, status_code=404
        )

    @app.get("/health")
    def health():
        return {"service": "ColabOne inference", "instance": g.instance}

    @app.get("/v1/models", dependencies=auth)
    def models():
        with g.runtime.lock:
            identity = g.runtime.loaded
            model = g.runtime.models.get(identity)
            return {
                "object": "list",
                "data": [
                    {
                        "id": identity,
                        "object": "model",
                        "created": 0,
                        "owned_by": "colabone",
                        "kind": model.kind,
                    }
                ]
                if model
                else [],
            }

    async def wait(identity, request):
        task = g.jobs[identity]["task"]
        try:
            async with asyncio.timeout(90):
                while task.state == "running":
                    if await request.is_disconnected():
                        raise asyncio.CancelledError()
                    await asyncio.sleep(0.1)
        except (TimeoutError, asyncio.CancelledError):
            task.request_cancel()
            raise HTTPException(
                504, "等待超时或调用已断开；画图和视频请使用 /v1/jobs 异步接口"
            ) from None
        if task.state != "completed":
            raise HTTPException(502, task.error or "任务已取消")

    @app.post("/v1/chat/completions", dependencies=auth)
    async def chat(body: ChatRequest, request: Request):
        if body.stream and g.mode != "named":
            raise HTTPException(
                400, "临时 Quick Tunnel 不支持 SSE，请设置 stream=false 或使用固定域名"
            )
        identity = g.chat(body)
        slot = g.jobs[identity]
        if not body.stream:
            await wait(identity, request)
            return slot["result"]

        async def stream():
            task = slot["task"]
            try:
                while task.state == "running" or not slot["queue"].empty():
                    try:
                        yield slot["queue"].get_nowait()
                    except queue.Empty:
                        await asyncio.sleep(0.05)
                if task.state != "completed":
                    yield (
                        "data: "
                        + json.dumps(
                            {"error": {"message": task.error or "API 请求已取消"}},
                            ensure_ascii=False,
                        )
                        + "\n\n"
                    ).encode()
            finally:
                if task.state == "running":
                    task.request_cancel()

        return StreamingResponse(
            stream(),
            media_type="text/event-stream",
            headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
        )

    @app.post("/v1/images/generations", dependencies=auth)
    async def image(body: ImageRequest, request: Request):
        width, height = map(int, body.size.split("x"))
        identity = g.creative(
            PublicCreative(
                model_id=body.model,
                prompt=body.prompt,
                width=width,
                height=height,
                count=body.n,
            )
        )
        await wait(identity, request)
        outputs = g.job(identity)["outputs"]
        return {
            "created": int(time.time()),
            "data": [
                {
                    "b64_json": base64.b64encode(
                        g.content(identity, out["id"])[0]
                    ).decode()
                }
                for out in outputs
            ],
        }

    @app.post("/v1/jobs", dependencies=auth, status_code=202)
    def create_job(body: PublicCreative):
        return g.job(g.creative(body))

    @app.get("/v1/jobs/{identity}", dependencies=auth)
    def job(identity: str):
        return g.job(identity)

    @app.delete("/v1/jobs/{identity}", dependencies=auth)
    def cancel(identity: str):
        g.job(identity)
        g.jobs[identity]["task"].request_cancel()
        return {"ok": True}

    @app.get("/v1/jobs/{identity}/outputs/{output}", dependencies=auth)
    def content(identity: str, output: str):
        data, mime = g.content(identity, output)
        return Response(data, media_type=mime, headers={"Cache-Control": "no-store"})

    return app
