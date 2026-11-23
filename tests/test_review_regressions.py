"""Regressions found in remote runtime lifecycle review; all processes are offline."""

from unittest.mock import Mock

import pytest

from server.runtime import ColabOneTask


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
