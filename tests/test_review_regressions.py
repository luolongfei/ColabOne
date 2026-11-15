"""Regressions found in the lifecycle review; all transports/processes are offline."""

import base64
import time
from datetime import datetime, timedelta, timezone
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock

import pytest
from fastapi import HTTPException

from bridge.controller import Controller
from conftest import MemoryVault
from server.runtime import Task
from conftest import wait_task
from test_creative import creative_model, png, request
from test_provider import make_provider, store_session


def test_rpc_refreshes_expired_proxy_before_sending_mutation(monkeypatch):
    provider, vault = make_provider()
    store_session(vault)
    old = provider.session()
    old.token_expires_at = datetime.now(timezone.utc) - timedelta(seconds=1)
    provider.save_session(old)
    info = SimpleNamespace(
        url="https://new-runtime.invalid",
        token="renewed-token",
        expires_at=lambda: datetime.now(timezone.utc) + timedelta(hours=1),
    )
    client = Mock()
    client.list_assignments.return_value = [
        SimpleNamespace(endpoint="owned", runtime_proxy_info=info)
    ]
    provider.client_factory = lambda: client
    contents = Mock()
    monkeypatch.setattr("bridge.provider.ContentsClient", contents)
    provider.execute = Mock(
        return_value=[{"text": 'COLABONE_RPC={"ok":true,"value":{}}'}]
    )
    provider.rpc("POST", "unload", {})
    assert contents.call_args.args[0].token == "renewed-token"
    assert provider.execute.call_args.args[0].token == "renewed-token"
    assert (
        provider.execute.call_count == 1
    )  # Never replay a potentially accepted mutation.


@pytest.mark.parametrize("released", [False, True])
def test_late_kernel_callbacks_cannot_restore_stale_credentials(monkeypatch, released):
    provider, vault = make_provider()
    store_session(vault)
    factory = Mock()
    monkeypatch.setattr("bridge.provider.ColabRuntime", factory)
    provider.execute(provider.session(), "pass")
    callback = factory.call_args.kwargs["on_kernel_started"]
    if released:
        provider.clear()
    else:
        renewed = provider.session()
        renewed.token = "renewed-token"
        provider.save_session(renewed)
    callback("kernel-id")
    if released:
        assert vault.get("runtime_session") is None
    else:
        assert vault.get("runtime_session")["token"] == "renewed-token"
        assert vault.get("runtime_session")["kernel_id"] == "kernel-id"


def test_monitor_renews_connection_even_when_configuration_fails(tmp_path):
    controller = Controller(
        Path(__file__).parents[1],
        vault=MemoryVault(),
        config_dir=tmp_path,
        background=False,
    )
    store_session(controller.vault)
    controller.vault.set(
        "remote", {"endpoint": "owned", "deadline": time.time() + 5000, "max_cu": 5}
    )
    controller.provider = Mock()
    controller.provider.account.return_value = {"balance": 100}
    controller.configure_remote = Mock(side_effect=RuntimeError("stale connection"))
    controller.stop = Mock()
    controller.stop.is_set.side_effect = [False, True]
    controller.monitor()
    controller.provider.heartbeat.assert_called_once()


def test_stop_engine_survives_exit_observed_by_status(runtime, monkeypatch):
    monkeypatch.setattr(runtime, "gpu_info", lambda: {})
    process = Mock()
    process.poll.return_value = None
    runtime.proc = process
    runtime.loaded = "test-model"

    def terminate():
        process.poll.return_value = 0
        runtime.status()

    process.terminate.side_effect = terminate
    runtime._stop_engine()
    process.wait.assert_called_once()
    assert runtime.proc is None and runtime.loaded is None


@pytest.mark.parametrize("kind", ["chat", "image"])
def test_exit_during_health_check_never_marks_model_loaded(
    runtime, spec, monkeypatch, kind
):
    model = spec if kind == "chat" else creative_model(runtime)
    runtime.loaded = None
    monkeypatch.setattr(runtime, "gpu_info", lambda: {"total_mib": 100000})
    monkeypatch.setattr(runtime, "prepare_engine", lambda _: runtime.root / "engine")
    monkeypatch.setattr(
        "server.creative.prepare_engine", lambda *_: runtime.root / "engine"
    )
    process = Mock()
    process.poll.return_value = None
    monkeypatch.setattr("server.runtime.subprocess.Popen", lambda *a, **k: process)
    runtime.http = Mock()

    def health(*args, **kwargs):
        process.poll.return_value = 1
        runtime.status()
        response = Mock(status_code=200, ok=True)
        response.json.return_value = {"state": "ready"}
        return response

    runtime.http.get.side_effect = health
    with pytest.raises(RuntimeError, match="退出|停止"):
        runtime._load(Task("load", model.id), model)
    assert runtime.loaded is None


@pytest.mark.parametrize("damage", ["changed", "truncated", "extended"])
def test_public_output_rejects_corruption(runtime, damage):
    body = request(runtime)
    item = runtime.creative.save(png(), body, "Image", 42, 1)
    task = Task("image", body.model_id)
    task.outputs = [item]
    runtime.public_api.jobs[task.id] = {"task": task}
    path = runtime.creative.root / (item["id"] + ".png")
    raw = base64.b64decode(png())
    corrupted = {
        "changed": b"x" + raw[1:],
        "truncated": raw[:-1],
        "extended": raw + b"x",
    }[damage]
    path.write_bytes(corrupted)
    with pytest.raises(HTTPException) as error:
        runtime.public_api.content(task.id, item["id"])
    assert error.value.status_code == 502


def test_first_sync_merges_drive_catalog_before_uploading_local_models(runtime, spec):
    local = spec.model_copy(update={"id": "local-import"})
    cloud = spec.model_copy(update={"id": "previous-instance"})
    runtime.drive.data["models.json"] = [cloud.model_dump()]
    runtime.add_model(local.model_dump())
    assert runtime.sync()
    assert {m["id"] for m in runtime.drive.get("models.json")} == {
        spec.id,
        local.id,
        cloud.id,
    }


def test_shutdown_during_engine_preparation_does_not_launch_orphan(
    runtime, spec, monkeypatch
):
    monkeypatch.setattr(runtime, "gpu_info", lambda: {"total_mib": 100000})

    def prepare(_):
        runtime.stopping.set()
        return runtime.root / "llama-server"

    monkeypatch.setattr(runtime, "prepare_engine", prepare)
    spawn = Mock()
    monkeypatch.setattr("server.runtime.subprocess.Popen", spawn)
    with pytest.raises(RuntimeError, match="停止"):
        runtime._load(Task("load", spec.id), spec)
    spawn.assert_not_called()


def test_chat_returns_stable_turn_position_and_preserves_previous_snapshot(runtime):
    import httpx

    runtime.loaded = "test-model"
    runtime.chat_transport = httpx.MockTransport(
        lambda _: httpx.Response(
            200,
            text='data: {"choices":[{"delta":{"content":"answer"}}]}\n\ndata: [DONE]\n\n',
        )
    )
    first = runtime.chat("", "first")
    wait_task(runtime, first)
    before = runtime.conversations[first["conversation_id"]]
    second = runtime.chat(first["conversation_id"], "second")
    wait_task(runtime, second)
    assert first["message_index"] == 0 and second["message_index"] == 2
    assert len(before["messages"]) == 2
    assert len(runtime.conversations[first["conversation_id"]]["messages"]) == 4


def test_proxy_refresh_in_flight_cannot_resurrect_released_session():
    provider, vault = make_provider()
    store_session(vault)
    client = Mock()

    def released():
        provider.clear("owned")
        return [SimpleNamespace(endpoint="owned", runtime_proxy_info=Mock())]

    client.list_assignments.side_effect = released
    provider.client_factory = lambda: client
    assert provider.session(refresh=True) is None
    assert vault.get("runtime_session") is None
