import json
import threading
import time
from unittest.mock import Mock
import pytest
from fastapi.testclient import TestClient
from common.files import atomic_json
from server.app import create_colabone_app
from server.runtime import ColabOneTask, ColabOneConflictError
from conftest import wait_task

TOKEN = "test-control-token-at-least-32-chars"
AUTH = {"Authorization": "Bearer " + TOKEN}


class Response:
    def __init__(self, body=b"12345678", status=200, headers=None):
        self.body, self.status_code, self.headers = body, status, headers or {}

    def __enter__(self):
        return self

    def __exit__(self, *args):
        pass

    def raise_for_status(self):
        pass

    def iter_content(self, *args):
        yield self.body

    def iter_lines(self, **kwargs):
        yield from self.body

    def close(self):
        pass


def install(runtime, spec):
    directory = runtime.models_dir / spec.id
    directory.mkdir(exist_ok=True)
    (directory / "model.gguf").write_bytes(b"12345678")
    atomic_json(directory / "verified.json", {"model.gguf": spec.files[0].sha256})


def test_all_mutations_require_token(runtime):
    app = create_colabone_app(runtime, TOKEN)
    with TestClient(app) as client:
        for path, body in [
            ("models/test-model/download", {}),
            ("models/test-model/load", {}),
            ("unload", {}),
            ("configure", {}),
            ("drive/connect", {}),
            ("drive/sync", {}),
            ("comfyui/start", {}),
            ("comfyui/stop", {}),
            ("comfyui/free", {}),
            ("comfyui/canvas/start", {}),
            ("comfyui/canvas/stop", {}),
            ("comfyui/workflows", {}),
            ("comfyui/inputs", {}),
            ("comfyui/run", {}),
            ("prepare-release", {}),
        ]:
            assert client.post("/api/v1/" + path, json=body).status_code == 401
        assert client.delete("/api/v1/models/test-model").status_code == 401
        assert client.delete("/api/v1/comfyui/workflows/wf1").status_code == 401
        assert client.delete("/api/v1/comfyui/outputs/" + "a" * 32).status_code == 401
        assert client.get("/api/v1/status").status_code == 401
        assert client.get("/api/v1/comfyui/canvas/url").status_code == 401
        assert client.get("/health").json() == {"ok": True, "version": "0.1.0"}
    assert not runtime.tasks


def test_status_and_snapshot_report_protocol_7_and_comfyui_capabilities(
    runtime, monkeypatch
):
    monkeypatch.setattr(runtime, "gpu_info", lambda: {"total_mib": 15360, "used_mib": 0})
    with TestClient(create_colabone_app(runtime, TOKEN)) as client:
        st = client.get("/api/v1/status", headers=AUTH).json()
        assert st["protocol"] == 7
        assert st["capabilities"] == ["public_api", "comfyui"]
        assert "comfyui" in st
        assert st["active_engine"] is None
        snap = client.get("/api/v1/snapshot", headers=AUTH).json()
        assert set(snap) == {"runtime", "models", "workflows", "comfy_outputs"}
        assert "conversations" not in snap and "creations" not in snap
        assert client.post("/api/v1/turns", json={"text": "hi"}, headers=AUTH).status_code == 404
        assert client.get("/api/v1/conversations", headers=AUTH).status_code == 404


def test_download_is_remote_and_does_not_load(runtime, spec):
    runtime.http = Mock()
    runtime.http.get.return_value = Response()
    task = wait_task(runtime, runtime.download(spec.id))
    assert task.state == "completed"
    assert runtime.installed(spec)
    assert runtime.loaded is None
    assert (runtime.models_dir / spec.id / "model.gguf").read_bytes() == b"12345678"


def test_range_resume(runtime, spec):
    directory = runtime.models_dir / spec.id
    directory.mkdir()
    (directory / "model.gguf.part").write_bytes(b"1234")
    runtime.http = Mock()
    runtime.http.get.return_value = Response(
        b"5678", 206, {"Content-Range": "bytes 4-7/8"}
    )
    task = wait_task(runtime, runtime.download(spec.id))
    assert task.state == "completed"
    assert runtime.http.get.call_args.kwargs["headers"]["Range"] == "bytes=4-"
    assert runtime.installed(spec)


def test_ignored_range_restarts_cleanly(runtime, spec):
    directory = runtime.models_dir / spec.id
    directory.mkdir()
    (directory / "model.gguf.part").write_bytes(b"1234")
    runtime.http = Mock()
    runtime.http.get.return_value = Response()
    assert wait_task(runtime, runtime.download(spec.id)).state == "completed"
    assert (directory / "model.gguf").read_bytes() == b"12345678"


def test_wrong_hash_never_becomes_installed(runtime, spec):
    runtime.http = Mock()
    runtime.http.get.return_value = Response(b"12345679")
    task = wait_task(runtime, runtime.download(spec.id))
    assert task.state == "failed" and "校验" in task.error
    assert not runtime.installed(spec)
    assert not (runtime.models_dir / spec.id / "model.gguf.part").exists()


def test_failed_download_preserves_other_models(runtime, spec):
    install(runtime, spec)
    second = spec.model_copy(update={"id": "second"})
    runtime.add_model(second.model_dump())
    runtime.http = Mock()
    runtime.http.get.side_effect = OSError("offline")
    assert wait_task(runtime, runtime.download("second")).state == "failed"
    assert runtime.installed(spec)


def test_low_disk_prevents_download(runtime, spec, monkeypatch):
    monkeypatch.setattr(
        "server.runtime.shutil.disk_usage", lambda _: type("Disk", (), {"free": 0})()
    )
    runtime.http = Mock()
    task = wait_task(runtime, runtime.download(spec.id))
    assert task.state == "failed" and "磁盘" in task.error
    runtime.http.get.assert_not_called()


def test_load_requires_download(runtime):
    with pytest.raises(ColabOneConflictError):
        runtime.load("test-model")


def test_low_vram_leaves_weights_intact(runtime, spec, monkeypatch):
    install(runtime, spec)
    runtime.models[spec.id] = spec.model_copy(update={"min_vram_mib": 9999})
    monkeypatch.setattr(runtime, "gpu_info", lambda: {"total_mib": 1000, "used_mib": 0})
    task = wait_task(runtime, runtime.load(spec.id))
    assert task.state == "failed" and "显存" in task.error
    assert runtime.installed(spec)


def test_switch_preserves_previous_model(runtime, spec, monkeypatch):
    install(runtime, spec)
    second = spec.model_copy(update={"id": "second"})
    runtime.add_model(second.model_dump())
    install(runtime, second)
    runtime.loaded = spec.id
    monkeypatch.setattr(
        runtime, "gpu_info", lambda: {"total_mib": 20000, "used_mib": 2000}
    )
    monkeypatch.setattr(
        runtime, "prepare_engine", lambda _t: runtime.root / "llama-server"
    )
    proc = Mock()
    proc.poll.return_value = None
    monkeypatch.setattr("server.runtime.subprocess.Popen", lambda *a, **k: proc)
    runtime.http = Mock()
    runtime.http.get.return_value = Response()
    assert wait_task(runtime, runtime.load("second")).state == "completed"
    assert runtime.loaded == "second"
    assert runtime.installed(spec) and runtime.installed(second)
    runtime.unload()
    assert runtime.installed(spec) and runtime.installed(second)


def test_busy_guards_switch_delete_and_unload(runtime, spec):
    install(runtime, spec)
    task = ColabOneTask("public_chat")
    runtime.tasks[task.id] = task
    for fn in (
        lambda: runtime.load(spec.id),
        lambda: runtime.delete(spec.id),
        runtime.unload,
    ):
        with pytest.raises(ColabOneConflictError):
            fn()


def test_cancel_cooperates(runtime):
    gate = threading.Event()

    def operation(task):
        gate.set()
        while True:
            task.check()
            time.sleep(0.01)

    result = runtime.submit("download", "test-model", operation)
    gate.wait(1)
    with TestClient(create_colabone_app(runtime, TOKEN)) as client:
        assert (
            client.delete("/api/v1/tasks/" + result["id"], headers=AUTH).status_code
            == 200
        )
    assert wait_task(runtime, result).state == "cancelled"


def test_events_reconnect_does_not_replay_previous_events(runtime):
    task = ColabOneTask("comfyui_run")
    runtime.tasks[task.id] = task
    task.emit("progress", {"step": 1})
    task.emit("progress", {"step": 2})
    task.state = "completed"
    task.emit("done", task.snapshot())
    with TestClient(create_colabone_app(runtime, TOKEN)) as client:
        text = client.get(
            "/api/v1/tasks/" + task.id + "/events?after=1", headers=AUTH
        ).text
    assert '"step": 1' not in text and '"step": 2' in text and "event: done" in text


def test_runtime_restart_reloads_saved_models_and_recomputes_installed(
    runtime, spec
):
    from server.runtime import ColabOneRuntime

    second = spec.model_copy(update={"id": "saved-custom", "name": "Saved Custom"})
    runtime.add_model(second.model_dump())
    assert (runtime.root / "models.json").exists()
    restarted = ColabOneRuntime(runtime.root, runtime.catalog)
    assert "saved-custom" in restarted.models
    assert restarted.models["saved-custom"].name == "Saved Custom"
    assert not restarted.installed(spec)
    assert restarted.loaded is None


def test_health_poll_does_not_reset_idle(runtime, monkeypatch):
    runtime.last_active = time.time() - 60
    monkeypatch.setattr(runtime, "gpu_info", lambda: {})
    before = runtime.last_active
    with TestClient(create_colabone_app(runtime, TOKEN)) as client:
        client.get("/api/v1/status", headers=AUTH)
    assert runtime.last_active == before


def test_release_cancels_tasks_and_blocks_new_work(runtime, spec):
    assert runtime.prepare_release()["ok"]
    with pytest.raises(ColabOneConflictError, match="释放"):
        runtime.download(spec.id)


def test_poll_cursor_replays_only_unseen_events(runtime):
    task = ColabOneTask("comfyui_run")
    runtime.tasks[task.id] = task
    task.emit("delta", {"content": "a"})
    task.emit("delta", {"content": "b"})
    task.state = "completed"
    with TestClient(create_colabone_app(runtime, TOKEN)) as client:
        result = client.get(
            f"/api/v1/tasks/{task.id}/poll?after=1", headers=AUTH
        ).json()
    assert result["state"] == "completed"
    assert [e["data"]["content"] for e in result["events"]] == ["b"]
