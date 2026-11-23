"""Regressions found in the lifecycle review; all transports/processes are offline."""

from datetime import datetime, timedelta, timezone
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock

import pytest

from bridge.controller import ColabOneController
from conftest import MemoryVault
from server.runtime import ColabOneTask
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
    controller = ColabOneController(
        Path(__file__).parents[1],
        vault=MemoryVault(),
        config_dir=tmp_path,
        background=False,
    )
    store_session(controller.vault)
    controller.vault.set("remote", {"endpoint": "owned", "deadline": 0})
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


def test_exit_during_health_check_never_marks_model_loaded(
    runtime, spec, monkeypatch
):
    model = spec
    runtime.loaded = None
    monkeypatch.setattr(runtime, "gpu_info", lambda: {"total_mib": 100000})
    monkeypatch.setattr(runtime, "prepare_engine", lambda _: runtime.root / "engine")
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
        runtime._load(ColabOneTask("load", model.id), model)
    assert runtime.loaded is None


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
        runtime._load(ColabOneTask("load", spec.id), spec)
    spawn.assert_not_called()


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


def test_upgrade_service_force_stops_active_services_tasks_and_model_before_upgrading(
    tmp_path,
):
    controller = ColabOneController(
        Path(__file__).parents[1],
        vault=MemoryVault(),
        config_dir=tmp_path,
        background=False,
    )
    calls = []
    task_id = "a" * 32
    state = {
        "public_api": {"enabled": True, "state": "ready"},
        "tasks": [{"id": task_id, "state": "running", "kind": "public_responses"}],
        "comfyui": {"running": True},
        "loaded_model": "qwen-flash",
        "active_engine": "llama",
    }

    def fake_remote(method, path, payload=None, endpoint=None):
        calls.append((method, path))
        if method == "GET" and path == "status":
            return {
                "public_api": dict(state["public_api"]),
                "tasks": [dict(t) for t in state["tasks"]],
                "comfyui": dict(state["comfyui"]),
                "loaded_model": state["loaded_model"],
                "active_engine": state["active_engine"],
            }
        if method == "POST" and path == "public-api/stop":
            state["public_api"] = {"enabled": False, "state": "stopped"}
            return {"ok": True}
        if method == "DELETE" and path == f"tasks/{task_id}":
            state["tasks"] = [{"id": task_id, "state": "cancelled"}]
            return {"ok": True}
        if method == "POST" and path == "comfyui/stop":
            state["comfyui"] = {"running": False}
            return {"ok": True}
        if method == "POST" and path == "unload":
            state["loaded_model"] = ""
            state["active_engine"] = None
            return {"ok": True}
        raise AssertionError(f"Unexpected remote call: {method} {path}")

    controller.remote = fake_remote
    controller.provider = Mock()
    controller.provider.upgrade_service = Mock(
        side_effect=lambda **_: calls.append(("PROVIDER", "upgrade_service"))
    )
    controller.refresh_remote = Mock(
        side_effect=lambda: calls.append(("LOCAL", "refresh_remote"))
    )
    controller.configure_remote = Mock(
        side_effect=lambda: calls.append(("LOCAL", "configure_remote"))
    )
    controller.async_operation = lambda _title, fn: (fn(), {"ok": True})[1]

    controller.command("runtime.upgrade", {"force": True})

    assert ("POST", "public-api/stop") in calls
    assert ("DELETE", f"tasks/{task_id}") in calls
    assert ("POST", "comfyui/stop") in calls
    assert ("POST", "unload") in calls
    upgrade_idx = calls.index(("PROVIDER", "upgrade_service"))
    assert calls.index(("POST", "public-api/stop")) < upgrade_idx
    assert calls.index(("DELETE", f"tasks/{task_id}")) < upgrade_idx
    assert calls.index(("POST", "comfyui/stop")) < upgrade_idx
    assert calls.index(("POST", "unload")) < upgrade_idx
    assert calls.index(("LOCAL", "refresh_remote")) > upgrade_idx
    assert calls.index(("LOCAL", "configure_remote")) > upgrade_idx
    messages = [entry["message"] for entry in controller.activity.snapshot()]
    assert "更新前正在关闭对外 API 服务" in messages
    assert "更新前正在取消运行中的远端任务" in messages
    assert "更新前正在停止 ComfyUI 服务" in messages
    assert "更新前正在卸载当前显存模型（保留磁盘缓存）" in messages


def test_upgrade_service_default_skips_force_teardown(tmp_path):
    controller = ColabOneController(
        Path(__file__).parents[1],
        vault=MemoryVault(),
        config_dir=tmp_path,
        background=False,
    )
    remote = Mock(side_effect=AssertionError("force=False must not call teardown RPCs"))
    controller.remote = remote
    controller.provider = Mock()
    controller.provider.upgrade_service = Mock()
    controller.refresh_remote = Mock()
    controller.configure_remote = Mock()
    controller.async_operation = lambda _title, fn: (fn(), {"ok": True})[1]

    controller.command("runtime.upgrade", {})

    remote.assert_not_called()
    controller.provider.upgrade_service.assert_called_once()
    controller.refresh_remote.assert_called_once()
    controller.configure_remote.assert_called_once()


def test_upgrade_service_force_stops_starting_comfy_canvas_and_refreshes_on_failure(
    tmp_path,
):
    controller = ColabOneController(
        Path(__file__).parents[1],
        vault=MemoryVault(),
        config_dir=tmp_path,
        background=False,
    )
    calls = []

    def fake_remote(method, path, payload=None, endpoint=None):
        calls.append((method, path))
        if method == "GET" and path == "status":
            return {
                "public_api": {"enabled": False, "state": "stopped"},
                "tasks": [],
                "comfyui": {
                    "running": False,
                    "state": "starting",
                    "canvas": {"enabled": True, "state": "ready"},
                },
                "loaded_model": "",
                "active_engine": None,
            }
        if method == "POST" and path == "comfyui/stop":
            return {"ok": True}
        raise AssertionError(f"Unexpected remote call: {method} {path}")

    controller.remote = fake_remote
    controller.provider = Mock()
    controller.provider.upgrade_service = Mock(side_effect=RuntimeError("upgrade failed"))
    controller.refresh_remote = Mock()
    controller.configure_remote = Mock()

    with pytest.raises(RuntimeError, match="upgrade failed"):
        controller.upgrade_service(force=True)

    assert ("POST", "comfyui/stop") in calls
    controller.refresh_remote.assert_called_once()
    controller.configure_remote.assert_not_called()


def test_upgrade_service_force_refreshes_remote_when_teardown_fails_midway(tmp_path):
    controller = ColabOneController(
        Path(__file__).parents[1],
        vault=MemoryVault(),
        config_dir=tmp_path,
        background=False,
    )

    def fake_remote(method, path, payload=None, endpoint=None):
        if method == "GET" and path == "status":
            return {
                "public_api": {"enabled": True, "state": "ready"},
                "tasks": [],
                "comfyui": {"running": False},
                "loaded_model": "qwen-flash",
                "active_engine": "llama",
            }
        if method == "POST" and path == "public-api/stop":
            return {"ok": True}
        if method == "POST" and path == "unload":
            raise RuntimeError("unload failed")
        raise AssertionError(f"Unexpected remote call: {method} {path}")

    controller.remote = fake_remote
    controller.provider = Mock()
    controller.provider.upgrade_service = Mock()
    controller.refresh_remote = Mock()

    with pytest.raises(RuntimeError, match="unload failed"):
        controller.upgrade_service(force=True)

    controller.provider.upgrade_service.assert_not_called()
    controller.refresh_remote.assert_called_once()


def test_upgrade_service_force_cancels_newly_spawned_task_during_wait(tmp_path):
    controller = ColabOneController(
        Path(__file__).parents[1],
        vault=MemoryVault(),
        config_dir=tmp_path,
        background=False,
    )
    tid1 = "1" * 32
    tid2 = "2" * 32
    deleted: list[str] = []
    status_calls = 0

    def fake_remote(method, path, payload=None, endpoint=None):
        nonlocal status_calls
        if method == "GET" and path == "status":
            status_calls += 1
            if status_calls == 1:
                tasks = [{"id": tid1, "state": "running"}]
            elif status_calls == 2:
                # Task 1 finished and chained into task 2 while waiting.
                tasks = [{"id": tid1, "state": "cancelled"}, {"id": tid2, "state": "running"}]
            else:
                tasks = [{"id": tid1, "state": "cancelled"}, {"id": tid2, "state": "cancelled"}]
            return {
                "public_api": {"enabled": False, "state": "stopped"},
                "tasks": tasks,
                "comfyui": {"running": False},
                "loaded_model": "",
                "active_engine": None,
            }
        if method == "DELETE" and path.startswith("tasks/"):
            deleted.append(path.split("/", 1)[1])
            return {"ok": True}
        raise AssertionError(f"Unexpected remote call: {method} {path}")

    controller.remote = fake_remote
    controller.provider = Mock()
    controller.provider.upgrade_service = Mock()
    controller.refresh_remote = Mock()
    controller.configure_remote = Mock()

    controller.upgrade_service(force=True)

    assert deleted == [tid1, tid2]
    controller.provider.upgrade_service.assert_called_once()
