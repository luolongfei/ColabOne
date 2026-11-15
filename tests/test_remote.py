import json
import threading
import time
from unittest.mock import Mock
import pytest
from fastapi.testclient import TestClient
from common.files import atomic_json
from server.app import create_app
from server.runtime import Task, Conflict
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
    app = create_app(runtime, TOKEN)
    with TestClient(app) as client:
        for path, body in [
            ("models/test-model/download", {}),
            ("models/test-model/load", {}),
            ("unload", {}),
            ("turns", {"text": "hi"}),
            ("configure", {}),
            ("restore", {}),
            ("sync", {}),
        ]:
            assert client.post("/api/v1/" + path, json=body).status_code == 401
        assert client.delete("/api/v1/models/test-model").status_code == 401
        assert client.get("/api/v1/status").status_code == 401
        assert client.get("/health").json() == {"ok": True, "version": "0.1.0"}
    assert not runtime.tasks


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
    with pytest.raises(Conflict):
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
    task = Task("chat")
    runtime.tasks[task.id] = task
    for fn in (
        lambda: runtime.load(spec.id),
        lambda: runtime.delete(spec.id),
        runtime.unload,
    ):
        with pytest.raises(Conflict):
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
    with TestClient(create_app(runtime, TOKEN)) as client:
        assert (
            client.delete("/api/v1/tasks/" + result["id"], headers=AUTH).status_code
            == 200
        )
    assert wait_task(runtime, result).state == "cancelled"


def test_events_reconnect_does_not_replay_previous_tokens(runtime):
    task = Task("chat")
    runtime.tasks[task.id] = task
    task.emit("delta", {"content": "first"})
    task.emit("delta", {"content": "second"})
    task.state = "completed"
    task.emit("done", task.snapshot())
    with TestClient(create_app(runtime, TOKEN)) as client:
        text = client.get(
            "/api/v1/tasks/" + task.id + "/events?after=1", headers=AUTH
        ).text
    assert "first" not in text and "second" in text and "event: done" in text


def test_drive_failure_preserves_dirty_snapshot(runtime):
    convo = {"id": "a" * 32, "title": "test", "messages": []}
    runtime.save_conversation(convo)
    runtime.drive.fail = True
    assert not runtime.sync()
    assert convo["id"] in runtime.dirty
    assert (runtime.root / f"conversation-{convo['id']}.json").exists()
    runtime.drive.fail = False
    assert runtime.sync() and not runtime.dirty


def test_fresh_runtime_restores_history_but_not_installed_flags(
    runtime, spec, tmp_path
):
    from server.runtime import Runtime

    install(runtime, spec)
    convo = {
        "id": "a" * 32,
        "title": "saved",
        "messages": [{"role": "user", "content": "hello", "status": "complete"}],
    }
    runtime.save_conversation(convo)
    assert runtime.sync()
    new = Runtime(tmp_path / "second-runtime", runtime.catalog, drive=runtime.drive)
    with TestClient(create_app(new, TOKEN)) as client:
        assert client.post("/api/v1/restore", headers=AUTH).status_code == 200
    assert new.conversations[convo["id"]]["messages"][0]["content"] == "hello"
    assert not new.installed(spec)
    assert new.loaded is None


def test_health_poll_does_not_reset_idle(runtime, monkeypatch):
    runtime.last_active = time.time() - 60
    monkeypatch.setattr(runtime, "gpu_info", lambda: {})
    before = runtime.last_active
    with TestClient(create_app(runtime, TOKEN)) as client:
        client.get("/api/v1/status", headers=AUTH)
    assert runtime.last_active == before


def test_chat_stream_saves_and_finishes(runtime):
    import httpx

    runtime.loaded = "test-model"

    def handler(request):
        assert request.url.host == "127.0.0.1"
        body = json.loads(request.content)
        assert body["messages"] == [{"role": "user", "content": "hello"}]
        return httpx.Response(
            200,
            content='data: {"choices":[{"delta":{"content":"你好"}}]}\n\ndata: [DONE]\n\n'.encode(),
        )

    runtime.chat_transport = httpx.MockTransport(handler)
    result = runtime.chat("", "hello")
    task = wait_task(runtime, result)
    assert task.state == "completed"
    convo = runtime.conversations[result["conversation_id"]]
    assert convo["messages"][-1]["content"] == "你好"
    assert convo["messages"][-1]["status"] == "complete"
    assert runtime.sync()
    assert (
        runtime.drive.get(f"conversation-{convo['id']}.json")["messages"][-1]["content"]
        == "你好"
    )


def test_chat_disconnect_preserves_partial_and_does_not_retry(runtime):
    import httpx

    runtime.loaded = "test-model"
    handler = Mock(
        return_value=httpx.Response(
            200, content=b'data: {"choices":[{"delta":{"content":"partial"}}]}\n\n'
        )
    )
    runtime.chat_transport = httpx.MockTransport(handler)
    result = runtime.chat("", "hello")
    assert wait_task(runtime, result).state == "failed"
    convo = runtime.conversations[result["conversation_id"]]
    assert convo["messages"][-1]["content"] == "partial"
    assert convo["messages"][-1]["status"] == "interrupted"
    assert handler.call_count == 1


def test_cancel_interrupts_waiting_for_first_token(runtime):
    import asyncio
    import httpx

    runtime.loaded = "test-model"
    entered = threading.Event()
    closed = threading.Event()

    class SlowStream(httpx.AsyncByteStream):
        async def __aiter__(self):
            entered.set()
            await asyncio.sleep(60)
            yield b"data: [DONE]\n\n"

        async def aclose(self):
            closed.set()

    runtime.chat_transport = httpx.MockTransport(
        lambda _: httpx.Response(200, stream=SlowStream())
    )
    result = runtime.chat("", "hello")
    assert entered.wait(1)
    runtime.tasks[result["id"]].request_cancel()
    assert wait_task(runtime, result).state == "cancelled"
    assert closed.wait(1)
    assert (
        runtime.conversations[result["conversation_id"]]["messages"][-1]["status"]
        == "cancelled"
    )
