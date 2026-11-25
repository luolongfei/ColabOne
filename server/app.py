"""Authenticated remote API. Never run this on the desktop for normal operation."""

from __future__ import annotations
import asyncio
import hmac
import json
import os
import threading
import time
from pathlib import Path
from typing import Literal
from contextlib import asynccontextmanager

from fastapi import FastAPI, Depends, Header, HTTPException, Query, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import StreamingResponse, JSONResponse
from pydantic import BaseModel, ConfigDict, Field, field_validator

from common.comfyui import (
    ComfyCanvasConfig,
    ComfyTtsPreviewRequest,
    WorkflowImportRequest,
    WorkflowRunRequest,
    validate_colabone_hf_token,
)
from common.files import atomic_json
from common.models import ModelSpec
from common.public_api import ColabOnePublicConfig
from server.runtime import ColabOneRuntime, ColabOneConflictError, _scrub_secret_text


class ColabOneDownloadRequest(BaseModel):
    model_config = ConfigDict(hide_input_in_errors=True)
    hf_token: str = Field(default="", max_length=512, repr=False)
    threads: Literal[16, 32] = 16
    load: bool = False
    context_length: int | None = Field(default=None, ge=512, le=262144)
    temperature: float | None = Field(default=None, ge=0, le=2)

    @field_validator("hf_token")
    @classmethod
    def valid_hf_token(cls, value: str) -> str:
        return validate_colabone_hf_token(value)


class ColabOneConfigureRequest(BaseModel):
    idle_minutes: int = Field(default=30, ge=1, le=240)
    idle_countdown_seconds: int = Field(default=60, ge=10, le=300)
    deadline: float = Field(default=0, ge=0)


class ColabOneActivityRequest(BaseModel):
    hold_seconds: int = Field(default=0, ge=0, le=330)


class ColabOneLoadOptions(BaseModel):
    context_length: int | None = Field(default=None, ge=512, le=262144)
    temperature: float | None = Field(default=None, ge=0, le=2)


class ColabOneModelApiIdRequest(BaseModel):
    api_id: str = Field(default="", max_length=120)


class ColabOneComfyInputUpload(BaseModel):
    filename: str = Field(min_length=1, max_length=255)
    data: str = Field(max_length=98 * 1024**2)


def create_colabone_app(runtime, token, background=False):
    if not token or len(token) < 24:
        raise ValueError("Remote API requires a strong control token")

    @asynccontextmanager
    async def lifespan(_app):
        if background:
            threading.Thread(target=runtime.detect_location, daemon=True).start()
            runtime.start_background()
        yield
        with runtime.lock:
            runtime.stopping.set()
        try:
            def _is_task_running(t) -> bool:
                t_lock = getattr(t, "lock", None)
                if t_lock is not None:
                    with t_lock:
                        val = getattr(t, "state", getattr(t, "status", ""))
                else:
                    val = getattr(t, "state", getattr(t, "status", ""))
                return val == "running"

            with runtime.lock:
                running_tasks = [
                    t for t in runtime.tasks.values() if _is_task_running(t)
                ]
            for task in running_tasks:
                try:
                    await asyncio.to_thread(task.request_cancel)
                except Exception:
                    pass
            if running_tasks:
                deadline = time.monotonic() + 5.0
                while any(_is_task_running(t) for t in running_tasks):
                    if time.monotonic() >= deadline:
                        break
                    await asyncio.sleep(0.1)
        except Exception:
            pass
        try:
            await asyncio.to_thread(runtime.public_api.shutdown)
        finally:
            try:
                def _stop_comfy() -> None:
                    comfy_lock = getattr(runtime, "_comfy_lifecycle_lock", None)
                    if comfy_lock is not None:
                        with comfy_lock:
                            runtime.comfy.stop_server()
                    else:
                        runtime.comfy.stop_server()

                await asyncio.to_thread(_stop_comfy)
            finally:
                def _flush_drive_and_stop_engine() -> None:
                    try:
                        with runtime.sync_lock:
                            if (
                                runtime.drive.available()
                                and runtime.drive_restored
                                and runtime._dirty_seq
                            ):
                                try:
                                    runtime._sync_to_drive()
                                except Exception:
                                    pass
                            # Do not flush_and_unmount native Colab DriveFS during normal
                            # SIGTERM (e.g., hot-upgrade via vm_update.py); prepare_release()
                            # unmounts DriveFS right before VM termination.
                            if runtime.drive.mode != "colab_drivefs":
                                runtime.drive.unmount()
                    finally:
                        runtime._stop_engine()

                await asyncio.to_thread(_flush_drive_and_stop_engine)

    app = FastAPI(
        title="ColabOne",
        docs_url=None,
        redoc_url=None,
        openapi_url=None,
        lifespan=lifespan,
    )

    config_file_lock = threading.Lock()

    def authorize(authorization: str = Header(default="")):
        if not hmac.compare_digest(authorization, "Bearer " + token):
            raise HTTPException(401, "Unauthorized")

    @app.exception_handler(ColabOneConflictError)
    async def conflict(_, exc):
        return JSONResponse(status_code=409, content={"detail": _scrub_secret_text(str(exc))})

    @app.exception_handler(KeyError)
    async def missing(_, exc):
        return JSONResponse(status_code=404, content={"detail": "资源不存在"})

    @app.exception_handler(ValueError)
    async def invalid(_, exc):
        return JSONResponse(status_code=400, content={"detail": _scrub_secret_text(str(exc))})

    @app.exception_handler(RequestValidationError)
    async def request_invalid(_, _exc):
        return JSONResponse(status_code=422, content={"detail": "请求参数格式不合法"})

    @app.exception_handler(RuntimeError)
    async def runtime_error(_, exc):
        return JSONResponse(
            status_code=502, content={"detail": _scrub_secret_text(str(exc))[:300]}
        )

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
            "workflows": runtime.comfy.store.list_workflows(),
            "comfy_outputs": runtime.comfy.store.list_outputs(),
            "tts_engines": runtime.comfy.list_tts_engines(),
        }

    @app.post("/api/v1/public-api/start", dependencies=auth)
    def public_start(body: ColabOnePublicConfig):
        return runtime.public_api.start(body.model_dump())

    @app.post("/api/v1/public-api/stop", dependencies=auth)
    def public_stop():
        return runtime.public_api.stop()

    @app.post("/api/v1/configure", dependencies=auth)
    def configure(body: ColabOneConfigureRequest):
        with runtime.lock:
            runtime.idle_minutes = body.idle_minutes
            runtime.idle_countdown_seconds = body.idle_countdown_seconds
            runtime.deadline = body.deadline
        cfg_env = os.environ.get("COLABONE_REMOTE_CONFIG", "")
        if cfg_env:
            cfg_path = Path(cfg_env)
            with config_file_lock:
                with runtime.lock:
                    idle_minutes = runtime.idle_minutes
                    idle_countdown_seconds = runtime.idle_countdown_seconds
                    deadline = runtime.deadline
                try:
                    if not cfg_path.is_symlink() and cfg_path.is_file():
                        data = json.loads(cfg_path.read_text(encoding="utf-8"))
                        if isinstance(data, dict):
                            data["idle_minutes"] = idle_minutes
                            data["idle_countdown_seconds"] = idle_countdown_seconds
                            data["deadline"] = deadline
                            atomic_json(cfg_path, data)
                except (OSError, ValueError):
                    pass
        return {"ok": True}

    @app.post("/api/v1/activity", dependencies=auth)
    def activity(body: ColabOneActivityRequest):
        return runtime.touch(body.hold_seconds)

    @app.post("/api/v1/drive/connect", dependencies=auth)
    def drive_connect():
        return runtime.connect_drive()

    @app.post("/api/v1/drive/sync", dependencies=auth)
    def drive_sync():
        return runtime.sync_drive()

    @app.get("/api/v1/models", dependencies=auth)
    def models():
        return runtime.list_models()

    @app.post("/api/v1/models", dependencies=auth)
    def add(body: ModelSpec):
        return runtime.add_model(body.model_dump())

    @app.post("/api/v1/models/{model_id}/download", dependencies=auth)
    def download(model_id: str, body: ColabOneDownloadRequest):
        options = body.model_dump(
            include={"context_length", "temperature"}, exclude_none=True
        )
        return runtime.download(
            model_id,
            body.hf_token,
            body.threads,
            load=body.load,
            options=options or None,
        )

    @app.post("/api/v1/models/{model_id}/load", dependencies=auth)
    def load(model_id: str, body: ColabOneLoadOptions = ColabOneLoadOptions()):
        return runtime.load(model_id, body.model_dump(exclude_none=True))

    @app.post("/api/v1/models/{model_id}/api-id", dependencies=auth)
    def update_model_api_id(model_id: str, body: ColabOneModelApiIdRequest):
        return runtime.update_model_api_id(model_id, body.api_id)

    @app.post("/api/v1/unload", dependencies=auth)
    def unload():
        runtime.unload()
        return {"ok": True}

    @app.delete("/api/v1/models/{model_id}", dependencies=auth)
    def delete(model_id: str):
        runtime.delete(model_id)
        return {"ok": True}

    @app.post("/api/v1/comfyui/start", dependencies=auth)
    def comfyui_start():
        return runtime.start_comfyui()

    @app.post("/api/v1/comfyui/stop", dependencies=auth)
    def comfyui_stop():
        runtime.stop_comfyui()
        return {"ok": True}

    @app.post("/api/v1/comfyui/free", dependencies=auth)
    def comfyui_free():
        return runtime.free_comfyui()

    @app.post("/api/v1/comfyui/canvas/start", dependencies=auth)
    def comfyui_canvas_start(body: ComfyCanvasConfig):
        runtime.touch()
        return runtime.comfy.start_canvas(body.model_dump())

    @app.post("/api/v1/comfyui/canvas/stop", dependencies=auth)
    def comfyui_canvas_stop():
        return runtime.comfy.stop_canvas()

    @app.get("/api/v1/comfyui/canvas/url", dependencies=auth)
    def comfyui_canvas_url():
        runtime.touch()
        return runtime.comfy.canvas_url()

    @app.get("/api/v1/comfyui/tts-engines", dependencies=auth)
    def comfyui_tts_engines():
        return runtime.comfy.list_tts_engines()

    @app.post("/api/v1/comfyui/tts-preview", dependencies=auth)
    def comfyui_tts_preview(body: ComfyTtsPreviewRequest):
        return runtime.preview_comfyui_tts(body.model_dump())

    @app.get("/api/v1/comfyui/tts-previews/{preview_id}", dependencies=auth)
    def comfyui_tts_preview_get(
        preview_id: str,
        offset: int = Query(default=0, ge=0),
        length: int = Query(default=0, ge=0, le=512 * 1024),
    ):
        with runtime.lock:
            if runtime.quiescing or runtime.stopping.is_set():
                raise ColabOneConflictError("实例正在准备释放")
            runtime.touch()
        return runtime.comfy.get_tts_preview(preview_id, offset=offset, length=length)

    @app.get("/api/v1/comfyui/workflows", dependencies=auth)
    def comfyui_workflows():
        return runtime.comfy.store.list_workflows()

    @app.post("/api/v1/comfyui/workflows", dependencies=auth)
    def comfyui_save_workflow(body: WorkflowImportRequest):
        runtime.touch()
        return runtime.comfy.store.save_workflow(body)

    @app.delete("/api/v1/comfyui/workflows/{workflow_id}", dependencies=auth)
    def comfyui_delete_workflow(workflow_id: str):
        runtime.touch()
        runtime.comfy.store.delete_workflow(workflow_id)
        return {"ok": True}

    @app.post("/api/v1/comfyui/inputs", dependencies=auth)
    def comfyui_upload_input(body: ColabOneComfyInputUpload):
        runtime.touch()
        return runtime.comfy.store.save_input(body.filename, body.data)

    @app.post("/api/v1/comfyui/run", dependencies=auth)
    def comfyui_run(body: WorkflowRunRequest):
        return runtime.run_comfyui_workflow(body.model_dump())

    @app.get("/api/v1/comfyui/outputs/{output_id}/asset", dependencies=auth)
    def comfyui_output_asset(
        output_id: str,
        offset: int = Query(default=0, ge=0),
        length: int = Query(default=512 * 1024, ge=1, le=512 * 1024),
    ):
        runtime.touch()
        return runtime.comfy.store.asset(output_id, offset, length)

    @app.post("/api/v1/comfyui/outputs/{output_id}/export-drive", dependencies=auth)
    def comfyui_export_output_drive(output_id: str):
        return runtime.export_comfy_output_to_drive(output_id)

    @app.delete("/api/v1/comfyui/outputs/{output_id}", dependencies=auth)
    def comfyui_delete_output(output_id: str):
        runtime.touch()
        runtime.comfy.store.delete_output(output_id)
        return {"ok": True}

    @app.get("/api/v1/tasks/{task_id}/poll", dependencies=auth)
    def poll(task_id: str, after: int = 0):
        with runtime.lock:
            task = runtime.tasks.get(task_id)
        if task is None:
            raise KeyError(task_id)
        with task.lock:
            return {"events": task.events[max(0, after) :], "state": task.state}

    @app.get("/api/v1/tasks/{task_id}/events", dependencies=auth)
    async def events(task_id: str, request: Request, after: int = 0):
        with runtime.lock:
            task = runtime.tasks.get(task_id)
        if task is None:
            raise KeyError(task_id)

        async def stream():
            cursor = max(0, after)
            while True:
                if runtime.stopping.is_set() or await request.is_disconnected():
                    break
                with task.lock:
                    batch = task.events[cursor:]
                    total = len(task.events)
                    state = task.state
                for event in batch:
                    cursor = event["seq"]
                    yield f"id: {cursor}\nevent: {event['event']}\ndata: {json.dumps(event['data'], ensure_ascii=False)}\n\n"
                if state != "running" and cursor >= total:
                    break
                if runtime.stopping.is_set() or await request.is_disconnected():
                    break
                yield ": heartbeat\n\n"
                await asyncio.sleep(0.15)

        return StreamingResponse(
            stream(),
            media_type="text/event-stream",
            headers={"Cache-Control": "no-cache"},
        )

    @app.delete("/api/v1/tasks/{task_id}", dependencies=auth)
    def cancel(task_id: str):
        with runtime.lock:
            task = runtime.tasks.get(task_id)
        if task is None:
            raise KeyError(task_id)
        task.request_cancel()
        return {"ok": True}

    @app.post("/api/v1/prepare-release", dependencies=auth)
    def prepare_release():
        try:
            return runtime.prepare_release()
        except Exception as exc:
            raise HTTPException(409, _scrub_secret_text(str(exc))[:300]) from None

    return app


def create_colabone_uvicorn_server(
    runtime,
    app,
    *,
    host: str = "127.0.0.1",
    port: int = 7860,
    timeout_graceful_shutdown: int = 5,
):
    import uvicorn

    uvicorn_config = uvicorn.Config(
        app,
        host=host,
        port=port,
        access_log=False,
        timeout_graceful_shutdown=timeout_graceful_shutdown,
    )

    class ColabOneUvicornServer(uvicorn.Server):
        def handle_exit(self, sig, frame):
            stopping = getattr(runtime, "stopping", None)
            if stopping is not None and hasattr(stopping, "set"):
                stopping.set()
            return super().handle_exit(sig, frame)

    return ColabOneUvicornServer(uvicorn_config)


if __name__ == "__main__":
    config = json.loads(
        Path(os.environ["COLABONE_REMOTE_CONFIG"]).read_text(encoding="utf-8")
    )
    runtime = ColabOneRuntime(
        Path(config["root"]), Path(__file__).resolve().parents[1] / "catalog"
    )
    runtime.deadline = config.get("deadline", 0)
    if config.get("idle_minutes"):
        runtime.idle_minutes = int(config["idle_minutes"])
    if config.get("idle_countdown_seconds"):
        runtime.idle_countdown_seconds = int(config["idle_countdown_seconds"])
    app = create_colabone_app(runtime, config["token"], background=True)
    create_colabone_uvicorn_server(runtime, app).run()
