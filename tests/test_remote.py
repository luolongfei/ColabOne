import hashlib
import json
import threading
import time
from unittest.mock import Mock
import pytest
from fastapi.testclient import TestClient
from common.files import atomic_json
from common.models import ModelSpec
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


def test_oversized_model_uses_hybrid_fit_and_falls_back_on_early_exit(
    runtime, monkeypatch
):
    oversized = ModelSpec(
        id="qwen38-flash-next-big",
        name="Qwen3.8-Flash-Next (111 GiB)",
        description="UD-Q4_K_XL",
        min_vram_mib=0,
        files=[
            {
                "repo": "huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
                "revision": "a" * 40,
                "path": "Qwen3.8-Flash-Next-UD-Q4_K_XL.gguf",
                "size": 111 * 1024**3,
                "sha256": hashlib.sha256(b"x").hexdigest(),
                "role": "model",
            }
        ],
    )
    runtime.add_model(oversized.model_dump())
    # Mock installed() so we don't allocate a 111 GiB test file on disk
    monkeypatch.setattr(runtime, "installed", lambda m: m.id == oversized.id)
    monkeypatch.setattr(
        runtime,
        "gpu_info",
        lambda: {"name": "NVIDIA RTX PRO 6000", "total_mib": 98304, "used_mib": 512},
    )
    monkeypatch.setattr(
        runtime, "prepare_engine", lambda _t: runtime.root / "llama-server"
    )
    launched_cmds = []

    def fake_popen(args, **kwargs):
        launched_cmds.append(list(args))
        proc = Mock()
        # First attempt simulates early OOM exit; second attempt succeeds with asymmetric KV fallback
        proc.poll.return_value = 1 if len(launched_cmds) == 1 else None
        return proc

    monkeypatch.setattr("server.runtime.subprocess.Popen", fake_popen)
    runtime.http = Mock()
    runtime.http.get.return_value = Response()

    task = wait_task(runtime, runtime.load(oversized.id))
    assert task.state == "completed"
    assert runtime.loaded == oversized.id
    assert len(launched_cmds) == 2
    # Attempt 1: --fit on MUST pair with --n-gpu-layers auto and NO -ot / --n-cpu-moe
    # (llama.cpp common_fit_params aborts if n_gpu_layers != auto or overrides are set).
    assert "--fit" in launched_cmds[0] and launched_cmds[0][launched_cmds[0].index("--fit") + 1] == "on"
    assert launched_cmds[0][launched_cmds[0].index("--n-gpu-layers") + 1] == "auto"
    assert launched_cmds[0][launched_cmds[0].index("--fit-target") + 1] == "2048"
    assert "--flash-attn" in launched_cmds[0]
    assert "-ot" not in launched_cmds[0]
    assert "--n-cpu-moe" not in launched_cmds[0]
    assert launched_cmds[0][launched_cmds[0].index("--cache-type-k") + 1] == "q8_0"
    assert launched_cmds[0][launched_cmds[0].index("--cache-type-v") + 1] == "q8_0"
    # Attempt 2 fallback: asymmetric KV q4_0 / q8_0 with --fit on, auto ngl, wider margin
    assert launched_cmds[1][launched_cmds[1].index("--n-gpu-layers") + 1] == "auto"
    assert launched_cmds[1][launched_cmds[1].index("--fit-target") + 1] == "4096"
    assert "-ot" not in launched_cmds[1]
    assert launched_cmds[1][launched_cmds[1].index("--cache-type-k") + 1] == "q4_0"
    assert launched_cmds[1][launched_cmds[1].index("--cache-type-v") + 1] == "q8_0"
    assert any("混合装载" in entry["message"] for entry in task.logs)
    runtime.unload()


def test_mla_and_moe_preset_scenarios_avoid_fit_conflicts_and_asymmetric_kv(
    runtime, monkeypatch
):
    glm_moe = ModelSpec(
        id="glm53-flash-test",
        name="GLM-5.3-Flash (320B-A18B)",
        description="320B-A18B MoE / Q4",
        min_vram_mib=0,
        context_length=16384,
        cpu_moe_layers=19,
        files=[
            {
                "repo": "GCSA-AiLab/GLM-5.3-Flash-Uncensored-RCO-GSQ-GGUF",
                "revision": "b" * 40,
                "path": "Q4/GLM-5.3-Flash.gguf",
                "size": 137 * 1024**3,
                "sha256": hashlib.sha256(b"glm").hexdigest(),
                "role": "model",
            }
        ],
    )
    gpu = {"name": "NVIDIA RTX PRO 6000", "total_mib": 98304, "used_mib": 512}
    needs_hybrid, plans = runtime._build_llama_load_plans(
        runtime.root / "llama-server",
        glm_moe,
        runtime.models_dir / glm_moe.id / glm_moe.files[0].path,
        gpu,
    )
    assert needs_hybrid is True
    # Every --fit on plan MUST keep --n-gpu-layers auto and NO tensor overrides.
    for label, cmd in plans:
        fit_val = cmd[cmd.index("--fit") + 1]
        ngl_val = cmd[cmd.index("--n-gpu-layers") + 1]
        if fit_val == "on":
            assert ngl_val == "auto", f"plan {label} must use --n-gpu-layers auto"
            assert "-ot" not in cmd, f"plan {label} must not pass -ot with --fit on"
            assert "--n-cpu-moe" not in cmd, f"plan {label} must not pass --n-cpu-moe with --fit on"
        else:
            # Explicit manual MoE fallback uses --fit off + --n-gpu-layers all + --n-cpu-moe
            assert ngl_val == "all"
            assert cmd[cmd.index("--n-cpu-moe") + 1] == "19"
        # MLA architecture must NEVER receive asymmetric K/V cache types.
        if "--cache-type-k" in cmd and "--cache-type-v" in cmd:
            assert cmd[cmd.index("--cache-type-k") + 1] == cmd[cmd.index("--cache-type-v") + 1]
    # Verify scenario ladder includes f16 compatible fit, 4K context downscale, manual MoE, and minimal compat.
    labels = [label for label, _ in plans]
    assert any("KV f16" in lb for lb in labels)
    assert any("4K" in lb for lb in labels)
    assert any("手动卸载回退" in lb for lb in labels)
    assert labels[-1] == "兼容模式装载"


def test_adaptive_context_downscale_updates_active_options(runtime, monkeypatch):
    oversized = ModelSpec(
        id="deepseek-v41-big",
        name="DeepSeek-V4.1-Flash",
        description="Q8_0",
        min_vram_mib=0,
        context_length=32768,
        files=[
            {
                "repo": "huihui-ai/Huihui-DeepSeek-V4.1-Flash-1017-abliterated-GGUF",
                "revision": "c" * 40,
                "path": "DeepSeek-V4.1-Flash-Q8_0.gguf",
                "size": 160 * 1024**3,
                "sha256": hashlib.sha256(b"ds").hexdigest(),
                "role": "model",
            }
        ],
    )
    runtime.add_model(oversized.model_dump())
    monkeypatch.setattr(runtime, "installed", lambda m: m.id == oversized.id)
    monkeypatch.setattr(
        runtime,
        "gpu_info",
        lambda: {"name": "NVIDIA L4", "total_mib": 24576, "used_mib": 256},
    )
    monkeypatch.setattr(
        runtime, "prepare_engine", lambda _t: runtime.root / "llama-server"
    )
    launched_cmds = []

    def fake_popen(args, stdout=None, **kwargs):
        launched_cmds.append(list(args))
        ctx = int(args[args.index("--ctx-size") + 1])
        proc = Mock()
        if ctx > 4096:
            if stdout is not None:
                stdout.write(
                    b"0.01.000.000 E ggml_backend_cuda_buffer_type_alloc_buffer: cudaMalloc failed: out of memory\n"
                )
                stdout.flush()
            proc.returncode = 1
            proc.poll.return_value = 1
        else:
            proc.returncode = None
            proc.poll.return_value = None
        return proc

    monkeypatch.setattr("server.runtime.subprocess.Popen", fake_popen)
    runtime.http = Mock()
    runtime.http.get.return_value = Response()

    task = wait_task(runtime, runtime.load(oversized.id))
    assert task.state == "completed"
    assert runtime.loaded == oversized.id
    assert runtime.active_options["context_length"] == 4096
    assert any("上下文已自动调整为 4096 tokens" in entry["message"] for entry in task.logs)
    runtime.unload()


def test_fatal_non_retryable_gguf_error_fast_fails_with_clean_summary(
    runtime, spec, monkeypatch
):
    install(runtime, spec)
    monkeypatch.setattr(
        runtime, "gpu_info", lambda: {"name": "Tesla T4", "total_mib": 15360, "used_mib": 128}
    )
    monkeypatch.setattr(
        runtime, "prepare_engine", lambda _t: runtime.root / "llama-server"
    )
    attempts = []

    def fake_popen(args, stdout=None, **kwargs):
        attempts.append(list(args))
        if stdout is not None:
            stdout.write(
                b"0.00.001.000 W security: no API key is set and CORS allows all origins (see https://github.com/ggml-org/llama.cpp/pull/25655)\n"
                b"0.00.093.020 I srv load_model: loading model 'bad.gguf'\n"
                b"0.00.120.000 E llama_model_load: error loading model: unknown model architecture: 'future_arch'\n"
            )
            stdout.flush()
        proc = Mock()
        proc.returncode = 1
        proc.poll.return_value = 1
        return proc

    monkeypatch.setattr("server.runtime.subprocess.Popen", fake_popen)
    task = wait_task(runtime, runtime.load(spec.id))
    assert task.state == "failed"
    assert len(attempts) == 1
    assert "pull/25655" not in task.error
    assert "unknown model architecture" in task.error
    # Verify SIGKILL OOM helper produces explicit host RAM OOM hint.
    oom_msg = runtime._summarize_engine_error("0.01 I loading weights\n", returncode=-9)
    assert "系统内存不足 OOM" in oom_msg


def test_service_revision_reported_by_remote_status(runtime):
    from pathlib import Path
    from common.files import colabone_service_revision

    st = runtime.status()
    expected = colabone_service_revision(Path(__file__).resolve().parents[1])
    assert expected and len(expected) == 16
    assert st["service_revision"] == expected


def test_warmup_http_500_oom_triggers_fallback_plan_and_assert_summary(
    runtime, spec, monkeypatch
):
    install(runtime, spec)
    monkeypatch.setattr(
        runtime,
        "gpu_info",
        lambda: {"name": "NVIDIA A100 40GB", "total_mib": 40960, "used_mib": 256},
    )
    monkeypatch.setattr(
        runtime, "prepare_engine", lambda _t: runtime.root / "llama-server"
    )
    launched_cmds = []

    def fake_popen(args, **kwargs):
        launched_cmds.append(list(args))
        proc = Mock()
        proc.poll.return_value = None
        proc.returncode = None
        return proc

    monkeypatch.setattr("server.runtime.subprocess.Popen", fake_popen)

    class WarmupResp:
        def __init__(self, status_code, text=""):
            self.status_code = status_code
            self.text = text

    post_calls = {"n": 0}

    def fake_post(*a, **k):
        post_calls["n"] += 1
        if post_calls["n"] == 1:
            return WarmupResp(500, '{"error":"cudaMalloc failed: out of memory"}')
        return WarmupResp(200, '{"choices":[{"message":{"content":"."}}]}')

    runtime.http = Mock()
    runtime.http.get.return_value = Response()
    runtime.http.post.side_effect = fake_post

    task = wait_task(runtime, runtime.load(spec.id, {"context_length": 65536}))
    assert task.state == "completed"
    assert len(launched_cmds) == 2
    # A100 40G tier uses 1536 primary and 2560 wide margins.
    assert launched_cmds[0][launched_cmds[0].index("--fit-target") + 1] == "1536"
    assert launched_cmds[1][launched_cmds[1].index("--fit-target") + 1] == "2560"
    # Verify non-hybrid large-context model still gets 4K compat_ctx in Scenario 6.
    _, plans = runtime._build_llama_load_plans(
        runtime.root / "llama-server",
        spec.model_copy(update={"context_length": 65536}),
        runtime.models_dir / spec.id / spec.files[0].path,
        {"name": "NVIDIA A100 40GB", "total_mib": 40960, "used_mib": 256},
    )
    compat_label, compat_cmd = plans[-1]
    assert compat_label == "兼容模式装载"
    assert compat_cmd[compat_cmd.index("--ctx-size") + 1] == "4096"
    # Verify C++ exception / GGML_ASSERT lines are extracted by _summarize_engine_error.
    assert_summary = runtime._summarize_engine_error(
        "0.00 I loading\nterminate called after throwing an instance of 'std::runtime_error'\n  what(): GGML_ASSERT(a == b) failed\n",
        returncode=-6,
    )
    assert "GGML_ASSERT" in assert_summary
    runtime.unload()


def test_reloading_same_model_stops_previous_engine_before_measuring_vram(
    runtime, spec, monkeypatch
):
    model = ModelSpec(
        id="same-reload-model",
        name="Same Reload 32B",
        description="Q8_0",
        min_vram_mib=0,
        files=[
            {
                "repo": "org/Same-32B-GGUF",
                "revision": "a" * 40,
                "path": "same-32b.gguf",
                "size": 32 * 1024**3,
                "sha256": hashlib.sha256(b"same").hexdigest(),
                "role": "model",
            }
        ],
    )
    runtime.add_model(model.model_dump())
    monkeypatch.setattr(runtime, "installed", lambda m: m.id == model.id)
    monkeypatch.setattr(
        runtime, "prepare_engine", lambda _t: runtime.root / "llama-server"
    )
    # Simulate an already-running engine occupying 36 GiB VRAM until stopped
    vram_used = {"mib": 36864}
    orig_stop = runtime._stop_engine

    def tracked_stop():
        orig_stop()
        vram_used["mib"] = 512

    monkeypatch.setattr(runtime, "_stop_engine", tracked_stop)
    monkeypatch.setattr(
        runtime,
        "gpu_info",
        lambda: {
            "name": "NVIDIA A100 40GB",
            "total_mib": 40960,
            "used_mib": vram_used["mib"],
        },
    )
    prev_proc = Mock()
    prev_proc.poll.return_value = None
    runtime.proc = prev_proc
    runtime.loaded = model.id

    launched_cmds = []

    def fake_popen(args, **kwargs):
        launched_cmds.append(list(args))
        proc = Mock()
        proc.poll.return_value = None
        return proc

    monkeypatch.setattr("server.runtime.subprocess.Popen", fake_popen)
    runtime.http = Mock()
    runtime.http.get.return_value = Response()

    task = wait_task(
        runtime, runtime.load(model.id, {"context_length": 16384, "temperature": 0.4})
    )
    assert task.state == "completed"
    assert runtime.loaded == model.id
    assert runtime.active_options["context_length"] == 16384
    # Because old engine was unloaded BEFORE gpu_info(), 32 GiB fits cleanly on 40 GiB without false hybrid warning
    assert not any("混合装载" in entry["message"] for entry in task.logs)
    runtime.unload()


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


def test_touch_preserves_longer_hold_and_connect_drive_gates_sync_on_restore(runtime):
    runtime.touch(180)
    held_until = runtime.activity_until
    runtime.touch(0)
    assert runtime.activity_until >= held_until

    runtime._mark_dirty("models")
    runtime.drive.available = lambda: True
    runtime.drive.connect = lambda: {}
    runtime.drive.pull_workspace = lambda _dest=None: False
    push_called = Mock(return_value=True)
    runtime.drive.push_workspace = push_called
    runtime.connect_drive()
    assert runtime.drive_restored is False
    push_called.assert_not_called()


def test_warmup_crash_falls_back_to_next_llama_plan(runtime, spec, monkeypatch):
    monkeypatch.setattr(runtime, "gpu_info", lambda: {"name": "T4", "total_mib": 16384, "used_mib": 0})
    monkeypatch.setattr(runtime, "prepare_engine", lambda _t: runtime.root / "llama-server")
    procs = []

    def fake_popen(args, **kwargs):
        proc = Mock()
        idx = len(procs)
        proc.returncode = None
        proc.poll.return_value = None
        procs.append((idx, proc))
        return proc

    monkeypatch.setattr("server.runtime.subprocess.Popen", fake_popen)
    runtime.http = Mock()
    runtime.http.get.return_value = Mock(status_code=200)

    def fake_warmup(_task, proc, _model_id):
        # Crash first plan during CUDA kernel warmup; succeed on second fallback plan.
        if len(procs) == 1:
            proc.returncode = 137
            proc.poll.return_value = 137

    monkeypatch.setattr(runtime, "_warmup_llama", fake_warmup)
    runtime._load(ColabOneTask("load", spec.id), spec)
    assert len(procs) == 2
    assert runtime.loaded == spec.id


def test_list_models_reports_cached_bytes_for_partial_and_sharded_models(runtime):
    from server.download import CHUNK, metadata_path

    shard1_data = b"a" * 1024
    shard2_size = CHUNK + 512
    shard2_digest = "b" * 64
    sharded = ModelSpec.model_validate(
        {
            "id": "sharded-cache-test",
            "name": "Sharded Cache Test",
            "description": "test",
            "min_vram_mib": 1000,
            "files": [
                {
                    "repo": "org/repo",
                    "revision": "a" * 40,
                    "path": "sub/part-1.gguf",
                    "size": len(shard1_data),
                    "sha256": hashlib.sha256(shard1_data).hexdigest(),
                    "role": "model",
                },
                {
                    "repo": "org/repo",
                    "revision": "a" * 40,
                    "path": "sub/part-2.gguf",
                    "size": shard2_size,
                    "sha256": shard2_digest,
                    "role": "shard",
                },
            ],
        }
    )
    runtime.add_model(sharded.model_dump())

    def entry():
        return next(m for m in runtime.list_models() if m["id"] == sharded.id)

    # Untouched model has 0 cached bytes
    assert entry()["cached_bytes"] == 0
    assert entry()["downloaded"] is False

    # Shard 1 finalized + shard 2 sparse .part with only chunk 0 completed
    directory = runtime.models_dir / sharded.id / "sub"
    directory.mkdir(parents=True)
    (directory / "part-1.gguf").write_bytes(shard1_data)
    part2 = directory / "part-2.gguf.part"
    with part2.open("wb") as handle:
        handle.truncate(shard2_size)
    metadata_path(part2).write_text(
        json.dumps(
            {
                "size": shard2_size,
                "sha256": shard2_digest,
                "chunk": CHUNK,
                "completed": [0],
            }
        ),
        encoding="utf-8",
    )
    # Must count shard1 + completed chunk 0, NOT sparse file length (shard2_size)
    assert entry()["cached_bytes"] == len(shard1_data) + CHUNK
    assert entry()["downloaded"] is False

    # Fully installed model reports full size
    part2.unlink()
    metadata_path(part2).unlink()
    with (directory / "part-2.gguf").open("wb") as handle:
        handle.truncate(shard2_size)
    atomic_json(
        runtime.models_dir / sharded.id / "verified.json",
        {
            "sub/part-1.gguf": hashlib.sha256(shard1_data).hexdigest(),
            "sub/part-2.gguf": shard2_digest,
        },
    )
    assert entry()["downloaded"] is True
    assert entry()["cached_bytes"] == len(shard1_data) + shard2_size

    # Deleting remote files resets cached_bytes to 0
    runtime.delete(sharded.id)
    assert entry()["downloaded"] is False
    assert entry()["cached_bytes"] == 0


def test_llama_load_plans_tune_ubatch_cache_ram_and_checkpoints_safely(
    runtime, monkeypatch
):
    qwen_big = ModelSpec(
        id="qwen38-flash-next-q8",
        name="Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
        description="Q8_0 hybrid PLE + MoE",
        min_vram_mib=0,
        context_length=32768,
        files=[
            {
                "repo": "huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
                "revision": "c" * 40,
                "path": "unsloth-Q8_0/Qwen3.8-Flash-Next-Q8_0-00001-of-00006.gguf",
                "size": 175 * 1024**3,
                "sha256": hashlib.sha256(b"q8").hexdigest(),
                "role": "model",
            }
        ],
    )
    gpu_96g = {"name": "NVIDIA RTX PRO 6000 Blackwell", "total_mib": 97894, "used_mib": 512}

    # High-RAM (180 GiB) + High-VRAM (96 GiB) hybrid MoE run:
    # - est_cpu_model ~= 89 GiB -> headroom ~= 91 GiB >= 48 GiB -> --cache-ram 16384.
    # - Plan 1 raises ubatch to 1024 (halving CPU MoE scans) and sets --checkpoint-min-step 2048.
    # - Plan 2 fallback drops --ubatch-size back to default 512 while retaining checkpoints.
    # - Plan 3 (full-context KV f16 compat) & Plan 6 omit optional cache/checkpoint/ubatch tuning.
    monkeypatch.setattr(runtime, "host_memory_mib", lambda: 180 * 1024)
    needs_hybrid, plans_hi = runtime._build_llama_load_plans(
        runtime.root / "llama-server",
        qwen_big,
        runtime.models_dir / qwen_big.id / qwen_big.files[0].path,
        gpu_96g,
    )
    assert needs_hybrid is True
    plan1_cmd = plans_hi[0][1]
    assert plan1_cmd[plan1_cmd.index("--batch-size") + 1] == "2048"
    assert plan1_cmd[plan1_cmd.index("--ubatch-size") + 1] == "1024"
    assert plan1_cmd[plan1_cmd.index("--cache-ram") + 1] == "16384"
    assert plan1_cmd[plan1_cmd.index("--checkpoint-min-step") + 1] == "2048"

    plan2_cmd = plans_hi[1][1]
    assert "--ubatch-size" not in plan2_cmd
    assert plan2_cmd[plan2_cmd.index("--cache-ram") + 1] == "16384"
    assert plan2_cmd[plan2_cmd.index("--checkpoint-min-step") + 1] == "2048"

    f16_compat_cmd = next(cmd for lbl, cmd in plans_hi if "KV f16" in lbl)
    assert "--ubatch-size" not in f16_compat_cmd
    assert "--cache-ram" not in f16_compat_cmd
    assert "--checkpoint-min-step" not in f16_compat_cmd

    compat_cmd = plans_hi[-1][1]
    assert "--ubatch-size" not in compat_cmd
    assert "--cache-ram" not in compat_cmd
    assert "--checkpoint-min-step" not in compat_cmd

    # 128K context scales --checkpoint-min-step to 4096 so 32 checkpoints cover the full 128K span.
    qwen_128k = qwen_big.model_copy(update={"context_length": 131072})
    _, plans_128k = runtime._build_llama_load_plans(
        runtime.root / "llama-server",
        qwen_128k,
        runtime.models_dir / qwen_big.id / qwen_big.files[0].path,
        gpu_96g,
    )
    assert plans_128k[0][1][plans_128k[0][1].index("--checkpoint-min-step") + 1] == "4096"

    # Dense oversized model on 96 GiB GPU — even with a random hf-<hex> ID containing "a3b"
    # or from the dense Gemma-4 / Qwen3.5 families — must NOT enable --ubatch-size 1024.
    for dense_id, dense_name, dense_path in [
        ("hf-82b42fca3b001234", "Llama-120B-Dense-Q8", "Llama-120B-Dense-Q8_0.gguf"),
        ("hf-1122334455667788", "Gemma-4-31B-Dense-Q8", "gemma-4-31b-it-Q8_0.gguf"),
        ("hf-9988776655443322", "Qwen3.5-72B-Dense-Q8", "Qwen3.5-72B-Q8_0.gguf"),
    ]:
        dense_big = ModelSpec(
            id=dense_id,
            name=dense_name,
            description="Dense Q8_0 model",
            min_vram_mib=0,
            context_length=32768,
            files=[
                {
                    "repo": f"org/{dense_name}-GGUF",
                    "revision": "d" * 40,
                    "path": dense_path,
                    "size": 120 * 1024**3,
                    "sha256": hashlib.sha256(dense_id.encode()).hexdigest(),
                    "role": "model",
                }
            ],
        )
        _, plans_dense = runtime._build_llama_load_plans(
            runtime.root / "llama-server",
            dense_big,
            runtime.models_dir / dense_big.id / dense_big.files[0].path,
            gpu_96g,
        )
        assert "--ubatch-size" not in plans_dense[0][1], f"{dense_name} should not get MoE ubatch"

    # Explicit active-parameter MoE naming (e.g. A4B / A10B / A17B) DOES enable --ubatch-size 1024.
    gemma_moe = ModelSpec(
        id="hf-deadbeef00112233",
        name="Qwen3.5-122B-A10B-Q8",
        description="122B-A10B MoE",
        min_vram_mib=0,
        context_length=32768,
        files=[
            {
                "repo": "org/Qwen3.5-122B-A10B-GGUF",
                "revision": "e" * 40,
                "path": "Qwen3.5-122B-A10B-Q8_0.gguf",
                "size": 125 * 1024**3,
                "sha256": hashlib.sha256(b"a10b").hexdigest(),
                "role": "model",
            }
        ],
    )
    _, plans_a10b = runtime._build_llama_load_plans(
        runtime.root / "llama-server",
        gemma_moe,
        runtime.models_dir / gemma_moe.id / gemma_moe.files[0].path,
        gpu_96g,
    )
    assert plans_a10b[0][1][plans_a10b[0][1].index("--ubatch-size") + 1] == "1024"

    # Tight RAM headroom after subtracting CPU-offloaded weights (128 GiB RAM, 40 GiB A100, 175 GiB model ->
    # est_cpu ~= 139 GiB -> negative headroom): must cap --cache-ram 4096 and omit --checkpoint-min-step.
    monkeypatch.setattr(runtime, "host_memory_mib", lambda: 128 * 1024)
    gpu_40g = {"name": "NVIDIA A100-SXM4-40GB", "total_mib": 40960, "used_mib": 256}
    _, plans_tight_ram = runtime._build_llama_load_plans(
        runtime.root / "llama-server",
        qwen_big,
        runtime.models_dir / qwen_big.id / qwen_big.files[0].path,
        gpu_40g,
    )
    assert plans_tight_ram[0][1][plans_tight_ram[0][1].index("--cache-ram") + 1] == "4096"
    assert "--checkpoint-min-step" not in plans_tight_ram[0][1]
    assert "--ubatch-size" not in plans_tight_ram[0][1]
