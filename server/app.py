"""Authenticated remote API. Never run this on the desktop for normal operation."""

from __future__ import annotations
import hmac
import json
import os
import time
from pathlib import Path
from typing import Literal
from contextlib import asynccontextmanager

from fastapi import FastAPI, Depends, Header, HTTPException, Query
from fastapi.responses import StreamingResponse, JSONResponse
from pydantic import BaseModel, Field

from common.models import ModelSpec
from common.creative import CreativeRequest
from common.public_api import PublicConfig
from server.runtime import Runtime, Conflict


class Download(BaseModel):
    hf_token: str = ""
    threads: Literal[8, 32] = 8


class Turn(BaseModel):
    conversation_id: str = ""
    text: str = Field(min_length=1, max_length=100000)


class Configure(BaseModel):
    idle_minutes: int = Field(default=3, ge=1, le=120)
    idle_countdown_seconds: int = Field(default=60, ge=10, le=300)
    deadline: float = Field(default=0, ge=0)


class Activity(BaseModel):
    hold_seconds: int = Field(default=0, ge=0, le=330)


class Reference(BaseModel):
    data: str = Field(max_length=12 * 1024**2)


class LoadOptions(BaseModel):
    context_length: int | None = Field(default=None, ge=512, le=65536)
    temperature: float | None = Field(default=None, ge=0, le=2)


def create_app(runtime, token, background=False):
    if not token or len(token) < 24:
        raise ValueError("Remote API requires a strong control token")

    @asynccontextmanager
    async def lifespan(_app):
        if background:
            runtime.start_background()
        yield
        runtime.stopping.set()
        try:
            runtime.public_api.shutdown()
        finally:
            runtime._stop_engine()

    app = FastAPI(
        title="ColabOne",
        docs_url=None,
        redoc_url=None,
        openapi_url=None,
        lifespan=lifespan,
    )

    def authorize(authorization: str = Header(default="")):
        if not hmac.compare_digest(authorization, "Bearer " + token):
            raise HTTPException(401, "Unauthorized")

    @app.exception_handler(Conflict)
    async def conflict(_, exc):
        return JSONResponse(status_code=409, content={"detail": str(exc)})

    @app.exception_handler(KeyError)
    async def missing(_, exc):
        return JSONResponse(status_code=404, content={"detail": "资源不存在"})

    @app.exception_handler(ValueError)
    async def invalid(_, exc):
        return JSONResponse(status_code=400, content={"detail": str(exc)})

    @app.get("/health")
    def health():
        return {"ok": True, "version": "0.1.0"}

    auth = [Depends(authorize)]

    @app.get("/api/v1/status", dependencies=auth)
    def status():
        return runtime.status()

    @app.get("/api/v1/snapshot", dependencies=auth)
    def snapshot():
        return {
            "runtime": runtime.status(),
            "models": runtime.list_models(),
            "conversations": runtime.list_conversations(),
            "creations": runtime.creative.listing(),
        }

    @app.post("/api/v1/public-api/start", dependencies=auth)
    def public_start(body: PublicConfig):
        return runtime.public_api.start(body.model_dump())

    @app.post("/api/v1/public-api/stop", dependencies=auth)
    def public_stop():
        return runtime.public_api.stop()

    @app.post("/api/v1/configure", dependencies=auth)
    def configure(body: Configure):
        runtime.idle_minutes = body.idle_minutes
        runtime.idle_countdown_seconds = body.idle_countdown_seconds
        runtime.deadline = body.deadline
        return {"ok": True}

    @app.post("/api/v1/activity", dependencies=auth)
    def activity(body: Activity):
        return runtime.touch(body.hold_seconds)

    @app.post("/api/v1/restore", dependencies=auth)
    def restore():
        if runtime.busy():
            raise Conflict("请等待当前任务结束")
        for spec in runtime.drive.get("models.json", []):
            runtime.add_model(spec)
        for convo in runtime.drive.conversations():
            existing = runtime.conversations.get(convo["id"])
            if not existing or existing["updated_at"] < convo["updated_at"]:
                runtime.save_conversation(convo)
        if hasattr(runtime.drive, "media_list"):
            runtime.creative.restore()
        return {"ok": True}

    @app.get("/api/v1/models", dependencies=auth)
    def models():
        return runtime.list_models()

    @app.post("/api/v1/models", dependencies=auth)
    def add(body: ModelSpec):
        return runtime.add_model(body.model_dump())

    @app.post("/api/v1/models/{model_id}/download", dependencies=auth)
    def download(model_id: str, body: Download):
        return runtime.download(model_id, body.hf_token, body.threads)

    @app.post("/api/v1/models/{model_id}/load", dependencies=auth)
    def load(model_id: str, body: LoadOptions = LoadOptions()):
        return runtime.load(model_id, body.model_dump(exclude_none=True))

    @app.post("/api/v1/creative/references", dependencies=auth)
    def reference(body: Reference):
        runtime.touch()
        return runtime.creative.reference(body.data)

    @app.post("/api/v1/creative/generate", dependencies=auth)
    def generate(body: CreativeRequest):
        return runtime.generate(body.model_dump())

    @app.get("/api/v1/creations/{identity}/asset", dependencies=auth)
    def asset(
        identity: str,
        offset: int = Query(default=0, ge=0),
        length: int = Query(default=512 * 1024, ge=1, le=512 * 1024),
    ):
        runtime.touch()
        return runtime.creative.asset(identity, offset, length)

    @app.delete("/api/v1/creations/{identity}", dependencies=auth)
    def remove_creation(identity: str):
        runtime.touch()
        runtime.creative.delete(identity)
        return {"ok": True}

    @app.post("/api/v1/unload", dependencies=auth)
    def unload():
        runtime.unload()
        return {"ok": True}

    @app.delete("/api/v1/models/{model_id}", dependencies=auth)
    def delete(model_id: str):
        runtime.delete(model_id)
        return {"ok": True}

    @app.get("/api/v1/conversations", dependencies=auth)
    def conversations():
        return runtime.list_conversations()

    @app.post("/api/v1/turns", dependencies=auth)
    def turn(body: Turn):
        return runtime.chat(body.conversation_id, body.text)

    @app.get("/api/v1/tasks/{task_id}/poll", dependencies=auth)
    def poll(task_id: str, after: int = 0):
        task = runtime.tasks[task_id]
        with task.lock:
            return {"events": task.events[max(0, after) :], "state": task.state}

    @app.get("/api/v1/tasks/{task_id}/events", dependencies=auth)
    def events(task_id: str, after: int = 0):
        task = runtime.tasks[task_id]

        def stream():
            cursor = max(0, after)
            while True:
                with task.lock:
                    batch = task.events[cursor:]
                for event in batch:
                    cursor = event["seq"]
                    yield f"id: {cursor}\nevent: {event['event']}\ndata: {json.dumps(event['data'], ensure_ascii=False)}\n\n"
                if task.state != "running" and cursor >= len(task.events):
                    break
                yield ": heartbeat\n\n"
                time.sleep(0.15)

        return StreamingResponse(
            stream(),
            media_type="text/event-stream",
            headers={"Cache-Control": "no-cache"},
        )

    @app.delete("/api/v1/tasks/{task_id}", dependencies=auth)
    def cancel(task_id: str):
        runtime.tasks[task_id].request_cancel()
        return {"ok": True}

    @app.post("/api/v1/sync", dependencies=auth)
    def sync():
        return {
            "ok": runtime.sync(),
            "pending": len(runtime.dirty),
            "error": runtime.sync_error,
        }

    @app.post("/api/v1/prepare-release", dependencies=auth)
    def prepare_release():
        try:
            return runtime.prepare_release()
        except Exception as exc:
            raise HTTPException(409, str(exc)[:300]) from None

    return app


if __name__ == "__main__":
    import uvicorn

    config = json.loads(
        Path(os.environ["COLABONE_REMOTE_CONFIG"]).read_text(encoding="utf-8")
    )
    runtime = Runtime(
        Path(config["root"]), Path(__file__).resolve().parents[1] / "catalog"
    )
    runtime.deadline = config.get("deadline", 0)
    uvicorn.run(
        create_app(runtime, config["token"], background=True),
        host="127.0.0.1",
        port=7860,
        access_log=False,
    )
