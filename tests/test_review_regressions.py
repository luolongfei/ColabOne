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

    controller.command("runtime.upgrade", {"force": True, "restore": False})

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
    assert not any(path.endswith("/load") or path == "public-api/start" for _, path in calls)
    messages = [entry["message"] for entry in controller.activity.snapshot()]
    assert "更新前正在关闭对外 API 服务" in messages
    assert "更新前正在取消运行中的远端任务" in messages
    assert "更新前正在停止 ComfyUI 服务" in messages
    assert "更新前正在卸载当前显存模型（保留磁盘缓存）" in messages


def _valid_cf_token() -> str:
    import base64
    import json

    raw = json.dumps({"a": "account-1", "t": "tunnel-1", "s": "secret-1"}).encode("utf-8")
    return base64.urlsafe_b64encode(raw).decode("ascii").rstrip("=")


def test_upgrade_service_force_auto_restores_model_specs_and_public_api(tmp_path):
    vault = MemoryVault()
    token = _valid_cf_token()
    api_key = "co_" + ("A" * 44)
    vault.set(
        "public_api",
        {
            "mode": "named",
            "domain": "https://api.example.com",
            "tunnel_token": token,
            "api_key": api_key,
            "legacy_extra": "ignored",
        },
    )
    controller = ColabOneController(
        Path(__file__).parents[1],
        vault=vault,
        config_dir=tmp_path,
        background=False,
    )
    calls: list[tuple[str, str, object]] = []
    state = {
        "public_api": {"enabled": True, "state": "ready"},
        "tasks": [],
        "comfyui": {"running": False},
        "loaded_model": "qwen-flash",
        "active_options": {"context_length": 131072, "temperature": 0.7},
        "active_engine": "llama",
    }

    def fake_remote(method, path, payload=None, endpoint=None):
        calls.append((method, path, payload))
        if method == "GET" and path == "status":
            return {
                "public_api": dict(state["public_api"]),
                "tasks": list(state["tasks"]),
                "comfyui": dict(state["comfyui"]),
                "loaded_model": state["loaded_model"],
                "active_options": dict(state["active_options"]),
                "active_engine": state["active_engine"],
            }
        if method == "POST" and path == "public-api/stop":
            state["public_api"] = {"enabled": False, "state": "stopped"}
            return {"ok": True}
        if method == "POST" and path == "unload":
            state["loaded_model"] = ""
            state["active_options"] = {}
            state["active_engine"] = None
            return {"ok": True}
        if method == "POST" and path == "models/qwen-flash/load":
            return {"id": "b" * 32, "kind": "load", "state": "running"}
        if method == "POST" and path == "public-api/start":
            return {"enabled": True, "state": "starting"}
        raise AssertionError(f"Unexpected remote call: {method} {path}")

    controller.remote = fake_remote
    controller.provider = Mock()
    controller.provider.upgrade_service = Mock(
        side_effect=lambda **_: calls.append(("PROVIDER", "upgrade_service", None))
    )
    controller.refresh_remote = Mock(
        side_effect=lambda **_: calls.append(("LOCAL", "refresh_remote", None))
    )
    controller.configure_remote = Mock(
        side_effect=lambda **_: calls.append(("LOCAL", "configure_remote", None))
    )
    controller.async_operation = lambda _title, fn: (fn(), {"ok": True})[1]

    controller.command("runtime.upgrade", {"force": True})

    upgrade_idx = next(
        i for i, c in enumerate(calls) if c[:2] == ("PROVIDER", "upgrade_service")
    )
    load_calls = [
        (i, c[2]) for i, c in enumerate(calls) if c[:2] == ("POST", "models/qwen-flash/load")
    ]
    api_calls = [
        (i, c[2]) for i, c in enumerate(calls) if c[:2] == ("POST", "public-api/start")
    ]
    assert len(load_calls) == 1
    assert load_calls[0][0] > upgrade_idx
    assert load_calls[0][1] == {"context_length": 131072, "temperature": 0.7}

    assert len(api_calls) == 1
    assert api_calls[0][0] > upgrade_idx
    assert api_calls[0][1] == {
        "mode": "named",
        "domain": "https://api.example.com",
        "tunnel_token": token,
        "api_key": api_key,
    }
    messages = [entry["message"] for entry in controller.activity.snapshot()]
    assert any("128K · 温度 0.7" in m for m in messages)
    assert any("自动重新开启对外 API 服务与公网隧道" in m for m in messages)


def test_upgrade_service_force_auto_restores_comfyui_and_canvas(tmp_path):
    vault = MemoryVault()
    token = _valid_cf_token()
    vault.set(
        "comfyui_canvas",
        {
            "mode": "named",
            "domain": "https://canvas.example.com",
            "tunnel_token": token,
        },
    )
    controller = ColabOneController(
        Path(__file__).parents[1],
        vault=vault,
        config_dir=tmp_path,
        background=False,
    )
    calls: list[tuple[str, str, object]] = []
    comfy_running = True
    canvas_enabled = True

    def fake_remote(method, path, payload=None, endpoint=None):
        nonlocal comfy_running, canvas_enabled
        calls.append((method, path, payload))
        if method == "GET" and path == "status":
            return {
                "public_api": {"enabled": False, "state": "stopped"},
                "tasks": [],
                "comfyui": {
                    "running": comfy_running,
                    "state": "running" if comfy_running else "stopped",
                    "canvas": {
                        "enabled": canvas_enabled,
                        "state": "ready" if canvas_enabled else "stopped",
                    },
                },
                "loaded_model": "",
                "active_engine": "comfyui" if comfy_running else None,
            }
        if method == "POST" and path == "comfyui/stop":
            comfy_running = False
            canvas_enabled = False
            return {"ok": True}
        if method == "POST" and path == "comfyui/start":
            comfy_running = True
            return {"id": "c" * 32, "kind": "comfy_start", "state": "completed"}
        if method == "POST" and path == "comfyui/canvas/start":
            canvas_enabled = True
            return {"enabled": True, "state": "starting"}
        raise AssertionError(f"Unexpected remote call: {method} {path}")

    controller.remote = fake_remote
    controller.provider = Mock()
    controller.provider.upgrade_service = Mock(
        side_effect=lambda **_: calls.append(("PROVIDER", "upgrade_service", None))
    )
    controller.refresh_remote = Mock()
    controller.configure_remote = Mock()
    controller.async_operation = lambda _title, fn: (fn(), {"ok": True})[1]

    controller.command("runtime.upgrade", {"force": True})

    upgrade_idx = next(
        i for i, c in enumerate(calls) if c[:2] == ("PROVIDER", "upgrade_service")
    )
    comfy_start_idx = next(
        i for i, c in enumerate(calls) if c[:2] == ("POST", "comfyui/start")
    )
    canvas_calls = [
        (i, c[2]) for i, c in enumerate(calls) if c[:2] == ("POST", "comfyui/canvas/start")
    ]
    assert comfy_start_idx > upgrade_idx
    assert len(canvas_calls) == 1
    assert canvas_calls[0][0] > comfy_start_idx
    assert canvas_calls[0][1] == {
        "mode": "named",
        "domain": "https://canvas.example.com",
        "tunnel_token": token,
    }


def test_upgrade_service_force_restore_handles_missing_vault_and_partial_errors_gracefully(
    tmp_path,
):
    controller = ColabOneController(
        Path(__file__).parents[1],
        vault=MemoryVault(),  # No saved public_api credentials
        config_dir=tmp_path,
        background=False,
    )
    load_attempts: list[object] = []

    def fake_remote(method, path, payload=None, endpoint=None):
        if method == "GET" and path == "status":
            return {
                "public_api": {"enabled": True, "state": "ready"},
                "tasks": [],
                "comfyui": {"running": False},
                "loaded_model": "qwen-flash",
                "active_options": {"context_length": 65536},
                "active_engine": "llama",
            }
        if method == "POST" and path in ("public-api/stop", "unload"):
            return {"ok": True}
        if method == "POST" and path == "models/qwen-flash/load":
            load_attempts.append(payload)
            raise RuntimeError("transient gpu error")
        raise AssertionError(f"Unexpected remote call: {method} {path}")

    controller.remote = fake_remote
    controller.provider = Mock()
    controller.provider.upgrade_service = Mock()
    controller.refresh_remote = Mock()
    controller.configure_remote = Mock()

    # Upgrade itself must still succeed even if model restore fails and vault lacks API config.
    controller.upgrade_service(force=True)

    assert load_attempts == [{"context_length": 65536}]
    messages = [entry["message"] for entry in controller.activity.snapshot()]
    assert any("自动重新装载模型" in m and "失败" in m for m in messages)
    assert any("缺少完整固定域名或隧道配置" in m for m in messages)


def test_restore_comfy_canvas_watcher_stops_cleanly_on_user_stop_existing_canvas_or_llm_switch(
    tmp_path,
):
    controller = ColabOneController(
        Path(__file__).parents[1],
        vault=MemoryVault(),
        config_dir=tmp_path,
        background=False,
    )
    canvas_payload = {
        "mode": "named",
        "domain": "https://canvas.example.com",
        "tunnel_token": _valid_cf_token(),
    }
    started_canvas: list[dict] = []

    # Case 1: comfy_start task completed, then user manually stopped ComfyUI before watcher polled.
    def stopped_after_complete(method, path, payload=None, endpoint=None):
        if method == "GET" and path == "status":
            return {
                "loaded_model": "",
                "active_engine": None,
                "comfyui": {"running": False, "state": "stopped", "canvas": {"enabled": False}},
                "tasks": [{"id": "c" * 32, "kind": "comfy_start", "state": "completed"}],
            }
        if method == "POST" and path == "comfyui/canvas/start":
            started_canvas.append(payload)
            return {"ok": True}
        raise AssertionError(f"Unexpected call: {method} {path}")

    controller.remote = stopped_after_complete
    controller._restore_comfy_canvas_when_ready(
        canvas_payload, task_id="c" * 32, timeout_seconds=5.0
    )
    assert started_canvas == []

    # Case 2: user already manually enabled canvas while ComfyUI is running.
    def already_enabled(method, path, payload=None, endpoint=None):
        if method == "GET" and path == "status":
            return {
                "loaded_model": "",
                "active_engine": "comfyui",
                "comfyui": {
                    "running": True,
                    "state": "running",
                    "canvas": {"enabled": True, "state": "ready"},
                },
                "tasks": [],
            }
        if method == "POST" and path == "comfyui/canvas/start":
            started_canvas.append(payload)
            return {"ok": True}
        raise AssertionError(f"Unexpected call: {method} {path}")

    controller.remote = already_enabled
    controller._restore_comfy_canvas_when_ready(canvas_payload, timeout_seconds=5.0)
    assert started_canvas == []

    # Case 3: user switched to loading an LLM while waiting.
    def switched_to_llm(method, path, payload=None, endpoint=None):
        if method == "GET" and path == "status":
            return {
                "loaded_model": "qwen-flash",
                "active_engine": "llama",
                "comfyui": {"running": False, "state": "stopped"},
                "tasks": [],
            }
        if method == "POST" and path == "comfyui/canvas/start":
            started_canvas.append(payload)
            return {"ok": True}
        raise AssertionError(f"Unexpected call: {method} {path}")

    controller.remote = switched_to_llm
    controller._restore_comfy_canvas_when_ready(canvas_payload, timeout_seconds=5.0)
    assert started_canvas == []


def test_upgrade_service_pins_target_endpoint_when_active_instance_switches_mid_upgrade(
    tmp_path,
):
    vault = MemoryVault()
    vault.set("runtime_session_ids", ["ep-1", "ep-2"])
    vault.set("runtime_session:ep-1", {"endpoint": "ep-1"})
    vault.set("runtime_session:ep-2", {"endpoint": "ep-2"})
    vault.set("active_endpoint", "ep-1")
    controller = ColabOneController(
        Path(__file__).parents[1],
        vault=vault,
        config_dir=tmp_path,
        background=False,
    )
    seen_endpoints: list[tuple[str, str | None]] = []

    def fake_remote(method, path, payload=None, endpoint=None):
        seen_endpoints.append((f"{method} {path}", endpoint))
        if method == "GET" and path == "status":
            return {
                "public_api": {"enabled": False, "state": "stopped"},
                "tasks": [],
                "comfyui": {"running": False},
                "loaded_model": "qwen-flash",
                "active_options": {"context_length": 65536, "temperature": 0.4},
                "active_engine": "llama",
            }
        return {"ok": True}

    def fake_upgrade(endpoint=None, on_log=None):
        seen_endpoints.append(("PROVIDER upgrade_service", endpoint))
        # Simulate user clicking a different GPU instance tab while upgrade is in progress.
        vault.set("active_endpoint", "ep-2")

    controller.remote = fake_remote
    controller.provider = Mock()
    controller.provider.upgrade_service = fake_upgrade
    controller.refresh_remote = Mock(
        side_effect=lambda endpoint=None: seen_endpoints.append(("refresh_remote", endpoint))
    )
    controller.configure_remote = Mock(
        side_effect=lambda endpoint=None: seen_endpoints.append(("configure_remote", endpoint))
    )

    controller.upgrade_service(force=True)

    assert ("PROVIDER upgrade_service", "ep-1") in seen_endpoints
    assert ("POST models/qwen-flash/load", "ep-1") in seen_endpoints
    assert all(ep == "ep-1" for _, ep in seen_endpoints)


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
