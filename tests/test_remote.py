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
            ("live/vram-check", {}),
            ("live/start", {}),
            ("live/stop", {}),
            ("live/session-token", {}),
            ("live/companion-ticket", {}),
            ("live/companion-disconnect", {}),
            ("live/llm-test", {}),
            ("live/avatars", {}),
            ("live/avatars/av-1/clips", {}),
            ("live/avatars/build-pack", {}),
            ("live/driving-motions", {}),
            ("prepare-release", {}),
        ]:
            assert client.post("/api/v1/" + path, json=body).status_code == 401
        assert client.delete("/api/v1/models/test-model").status_code == 401
        assert client.delete("/api/v1/comfyui/workflows/wf1").status_code == 401
        assert client.delete("/api/v1/comfyui/outputs/" + "a" * 32).status_code == 401
        assert client.delete("/api/v1/live/avatars/av-1").status_code == 401
        assert client.delete("/api/v1/live/driving-motions/dm-1").status_code == 401
        assert client.get("/api/v1/status").status_code == 401
        assert client.get("/api/v1/comfyui/canvas/url").status_code == 401
        assert client.get("/api/v1/live/status").status_code == 401
        assert client.get("/api/v1/live/catalog").status_code == 401
        assert client.get("/api/v1/live/avatars").status_code == 401
        assert client.get("/api/v1/live/driving-motions").status_code == 401
        assert client.get("/health").json() == {"ok": True, "version": "0.1.0"}
    assert not runtime.tasks


def test_status_and_snapshot_report_protocol_7_and_comfyui_capabilities(
    runtime, monkeypatch
):
    monkeypatch.setattr(runtime, "gpu_info", lambda: {"total_mib": 15360, "used_mib": 0})
    with TestClient(create_colabone_app(runtime, TOKEN)) as client:
        st = client.get("/api/v1/status", headers=AUTH).json()
        assert st["protocol"] == 7
        assert st["capabilities"] == [
            "public_api",
            "comfyui",
            "comfyui_hf_token",
            "comfyui_drive_export",
            "comfyui_tts_preview",
            "comfyui_longcat_native",
            "comfyui_workflow_auto_release",
            "comfyui_live_studio",
            "comfyui_fashion_tryon",
            "comfyui_share_guard",
            "idle_release_modes",
        ]
        assert "comfyui" in st
        assert "live_studio" in st
        assert st["active_engine"] is None
        snap = client.get("/api/v1/snapshot", headers=AUTH).json()
        assert set(snap) == {
            "runtime",
            "models",
            "workflows",
            "comfy_outputs",
            "tts_engines",
            "live_avatars",
            "live_driving_motions",
            "live_catalog",
        }
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


def test_canvas_proxy_forwards_live_ws_without_cookie_and_protects_live_http():
    import socket
    from server.comfy_proxy import (
        COLABONE_COMFY_COOKIE,
        ColabOneCanvasProxyServer,
        ColabOneComfyCanvasGateway,
    )

    class _SimpleUpstream:
        def __init__(self, tag: str):
            self.tag = tag
            self.sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            self.sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            self.sock.bind(("127.0.0.1", 0))
            self.port = self.sock.getsockname()[1]
            self.sock.listen(16)
            self.sock.settimeout(0.2)
            self.stop_event = threading.Event()
            self.requests: list[dict] = []
            self.thread = threading.Thread(target=self._serve, daemon=True)
            self.thread.start()

        def _serve(self):
            while not self.stop_event.is_set():
                try:
                    conn, _ = self.sock.accept()
                except socket.timeout:
                    continue
                except OSError:
                    break
                threading.Thread(target=self._handle, args=(conn,), daemon=True).start()

        def _handle(self, conn: socket.socket):
            with conn:
                conn.settimeout(3.0)
                buf = bytearray()
                while b"\r\n\r\n" not in buf:
                    chunk = conn.recv(4096)
                    if not chunk:
                        return
                    buf.extend(chunk)
                sep = buf.index(b"\r\n\r\n")
                head = bytes(buf[:sep]).decode("latin-1")
                leftover = bytes(buf[sep + 4 :])
                lines = head.split("\r\n")
                headers: dict[str, str] = {}
                for line in lines[1:]:
                    k, _, v = line.partition(":")
                    headers[k.strip().lower()] = v.strip()
                self.requests.append({"line": lines[0], "headers": headers})
                if headers.get("upgrade", "").lower() == "websocket":
                    conn.sendall(
                        b"HTTP/1.1 101 Switching Protocols\r\n"
                        b"Upgrade: websocket\r\n"
                        b"Connection: Upgrade\r\n\r\n"
                    )
                    data = leftover or conn.recv(4096)
                    if data:
                        conn.sendall(f"{self.tag}:".encode("utf-8") + data)
                    return
                body = json.dumps(
                    {"tag": self.tag, "host": headers.get("host", "")}
                ).encode("utf-8")
                conn.sendall(
                    (
                        "HTTP/1.1 200 OK\r\n"
                        "Content-Type: application/json\r\n"
                        f"Content-Length: {len(body)}\r\n"
                        "Connection: close\r\n\r\n"
                    ).encode("latin-1")
                    + body
                )

        def close(self):
            self.stop_event.set()
            try:
                self.sock.close()
            except OSError:
                pass
            self.thread.join(timeout=3.0)

    def _raw_http(port: int, payload: bytes) -> tuple[int, bytes]:
        with socket.create_connection(("127.0.0.1", port), timeout=5.0) as sock:
            sock.sendall(payload)
            sock.shutdown(socket.SHUT_WR)
            raw = bytearray()
            while True:
                chunk = sock.recv(4096)
                if not chunk:
                    break
                raw.extend(chunk)
        head_b, _, body = bytes(raw).partition(b"\r\n\r\n")
        status = int(head_b.decode("latin-1").split("\r\n")[0].split(" ")[1])
        return status, body

    comfy_up = _SimpleUpstream("comfy")
    live_up = _SimpleUpstream("live")
    touches: list[float] = []
    proxy = ColabOneCanvasProxyServer(
        upstream_host="127.0.0.1",
        upstream_port=comfy_up.port,
        listen_host="127.0.0.1",
        listen_port=0,
        instance="inst-live-proxy",
        canvas_token="live-canvas-tok",
        session_cookie="live-sess-cookie",
        on_activity=lambda: touches.append(time.monotonic()),
        live_upstream_port=live_up.port,
    )
    proxy.start()
    try:
        # 1. /_colabone_live/ws WebSocket upgrade bypasses ComfyUI cookie, does NOT invoke
        #    proxy on_activity (live_server touches only after first-frame auth), and
        #    routes to live_upstream_port with accurate Host header.
        with socket.create_connection(("127.0.0.1", proxy.port), timeout=5.0) as ws:
            ws.sendall(
                (
                    "GET /_colabone_live/ws HTTP/1.1\r\n"
                    "Host: example.com\r\n"
                    "Upgrade: websocket\r\n"
                    "Connection: Upgrade\r\n"
                    "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"
                    "Sec-WebSocket-Version: 13\r\n"
                    f"Cookie: {COLABONE_COMFY_COOKIE}=stale-cookie\r\n\r\n"
                ).encode("latin-1")
            )
            resp_buf = bytearray()
            while b"\r\n\r\n" not in resp_buf:
                chunk = ws.recv(4096)
                assert chunk
                resp_buf.extend(chunk)
            assert resp_buf.startswith(b"HTTP/1.1 101")
            ws.sendall(b"hello-live")
            assert ws.recv(4096) == b"live:hello-live"
        assert touches == []
        assert comfy_up.requests == []
        assert len(live_up.requests) == 1
        assert live_up.requests[0]["headers"]["host"] == f"127.0.0.1:{live_up.port}"
        assert COLABONE_COMFY_COOKIE not in live_up.requests[0]["headers"].get("cookie", "")

        # 2. Incomplete RFC 6455 upgrade headers and non-upgrade requests to /_colabone_live/* require auth.
        for bad_req in (
            b"GET /_colabone_live/ws HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n",
            b"GET /_colabone_live/ws HTTP/1.1\r\nHost: 127.0.0.1\r\nUpgrade: websocket\r\n\r\n",
            b"GET /_colabone_live/ws HTTP/1.1\r\nHost: 127.0.0.1\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n",
            b"GET /_colabone_live/ws HTTP/1.1\r\nHost: 127.0.0.1\r\nUpgrade: websocket\r\nConnection: keep-alive\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n",
            b"POST /_colabone_live/ws HTTP/1.1\r\nHost: 127.0.0.1\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n",
            b"GET /_colabone_live/health HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n",
        ):
            st_unauth, _ = _raw_http(proxy.port, bad_req)
            assert st_unauth == 401
        assert touches == []
        assert len(live_up.requests) == 1

        # 3. Authorized /_colabone_live/health forwards to live_upstream_port with Host updated.
        st_health_ok, body_ok = _raw_http(
            proxy.port,
            (
                "GET /_colabone_live/health HTTP/1.1\r\n"
                "Host: example.com\r\n"
                "Authorization: Bearer live-canvas-tok\r\n\r\n"
            ).encode("latin-1"),
        )
        assert st_health_ok == 200
        assert json.loads(body_ok) == {
            "tag": "live",
            "host": f"127.0.0.1:{live_up.port}",
        }

        # 4. When Live Studio upstream is stopped, authorized /_colabone_live/health returns 502 cleanly.
        live_up.close()
        st_down, body_down = _raw_http(
            proxy.port,
            (
                "GET /_colabone_live/health HTTP/1.1\r\n"
                "Host: example.com\r\n"
                "Authorization: Bearer live-canvas-tok\r\n\r\n"
            ).encode("latin-1"),
        )
        assert st_down == 502
        assert "Live Studio" in json.loads(body_down)["error"]
    finally:
        proxy.stop()
        comfy_up.close()
        live_up.close()

    # 5. ColabOneComfyCanvasGateway.public_origin() returns origin only when enabled & ready.
    gw = ColabOneComfyCanvasGateway(None, upstream_port=8188, proxy_port=8189, live_upstream_port=8190)
    assert gw.live_upstream_port == 8190
    assert gw.public_origin() == ""
    gw.enabled = True
    gw.phase = "starting"
    gw.url = "https://canvas.example.com"
    assert gw.public_origin() == ""
    gw.phase = "ready"
    assert gw.public_origin() == "https://canvas.example.com"


def test_drive_live_avatars_filter_sync_and_safe_trash(tmp_path):
    from server.drive import ColabOneDrive, COLABONE_DRIVEFS_FOLDER

    assert "+ /live-avatars/**" in ColabOneDrive._WORKSPACE_FILTER_ARGS

    root = tmp_path / "remote"
    catalog = tmp_path / "catalog"
    mydrive = tmp_path / "drive" / "MyDrive"
    root.mkdir(parents=True)
    catalog.mkdir(parents=True)
    mydrive.mkdir(parents=True)

    drive = ColabOneDrive(root, catalog, mount_root=mydrive)
    drive.mode = "colab_drivefs"
    drive.is_colab_drivefs_mounted = lambda: True

    # Create local nested live-avatar folder + temp file + .trash entry, then push.
    avatar_dir = root / "live-avatars" / "av_host_1"
    (avatar_dir / "clips").mkdir(parents=True)
    (avatar_dir / "avatar.json").write_text('{"id":"av_host_1"}', encoding="utf-8")
    (avatar_dir / "clips" / "idle.mp4").write_bytes(b"mp4-idle-bytes")
    (avatar_dir / "clips" / "partial.mp4.tmp").write_bytes(b"skip-me")
    (root / "live-avatars" / ".trash" / "deleted_old").mkdir(parents=True)
    (root / "live-avatars" / ".trash" / "deleted_old" / "avatar.json").write_text("{}", encoding="utf-8")

    assert drive.push_workspace() is True
    ws_avatars = mydrive / COLABONE_DRIVEFS_FOLDER / "workspace" / "live-avatars"
    assert (ws_avatars / "av_host_1" / "avatar.json").is_file()
    assert (ws_avatars / "av_host_1" / "clips" / "idle.mp4").read_bytes() == b"mp4-idle-bytes"
    assert not (ws_avatars / "av_host_1" / "clips" / "partial.mp4.tmp").exists()
    assert not (ws_avatars / ".trash" / "deleted_old").exists()

    # Pull into a fresh stage directory restores nested files cleanly.
    pull_dest = tmp_path / "pulled"
    assert drive.pull_workspace(pull_dest) is True
    assert (pull_dest / "live-avatars" / "av_host_1" / "clips" / "idle.mp4").read_bytes() == b"mp4-idle-bytes"

    # Safe trash moves avatar directory into live-avatars/.trash/<avatar_id>.
    drive.trash_live_avatar("av_host_1")
    assert not (ws_avatars / "av_host_1").exists()
    assert (ws_avatars / ".trash" / "av_host_1" / "clips" / "idle.mp4").read_bytes() == b"mp4-idle-bytes"

    # Reject path traversal and symlinks strictly.
    for bad_id in ("", "..", "../escape", "a/b", ".trash", "av.with.dot"):
        with pytest.raises(ValueError):
            drive.trash_live_avatar(bad_id)

    link_target = tmp_path / "outside"
    link_target.mkdir()
    (ws_avatars / "sym_avatar").symlink_to(link_target)
    with pytest.raises(ValueError):
        drive.trash_live_avatar("sym_avatar")


def test_load_reads_active_config_outside_lock_and_respects_musetalk_personalive(
    runtime, spec, monkeypatch
):
    model = spec.model_copy(update={"id": "mid-llm-4g", "min_vram_mib": 4096})
    runtime.add_model(model.model_dump())
    install(runtime, model)

    lock_held_during_probes: list[tuple[str, bool]] = []
    seen_cfgs: list[dict] = []

    monkeypatch.setattr(runtime.live, "is_running", lambda: True)

    def fake_live_status():
        lock_held_during_probes.append(("live.status", runtime.lock.held_by_current_thread()))
        return {
            "running": True,
            "active_clients": 1,
            "active_config": {
                "visual_engine": "musetalk_bank",
                "personalive_enabled": True,
                "tts_engine": "qwen3_tts",
                "llm_mode": "local_gguf",
            },
            # Deliberately place a misleading "config" key to prove load() uses "active_config".
            "config": {
                "visual_engine": "flashhead_lite",
                "personalive_enabled": False,
                "tts_engine": "qwen3_tts",
                "llm_mode": "local_gguf",
            },
        }

    def fake_gpu_info():
        lock_held_during_probes.append(("gpu_info", runtime.lock.held_by_current_thread()))
        return {"name": "NVIDIA L4", "total_mib": 24576, "used_mib": 0}

    orig_check_vram = runtime.live.check_vram

    def spy_check_vram(cfg, gpu_info=None, loaded_model_vram_mib=0):
        seen_cfgs.append(dict(cfg))
        return orig_check_vram(cfg, gpu_info, loaded_model_vram_mib=loaded_model_vram_mib)

    monkeypatch.setattr(runtime.live, "status", fake_live_status)
    monkeypatch.setattr(runtime, "gpu_info", fake_gpu_info)
    monkeypatch.setattr(runtime.live, "check_vram", spy_check_vram)

    # On 24 GiB GPU with 4 GiB LLM: flashhead_lite fits (24064 <= 24576),
    # whereas musetalk_bank + personalive_enabled=True does NOT fit (25088 > 24576).
    with pytest.raises(ColabOneConflictError, match="实时直播正在推流"):
        runtime.load(model.id)

    assert len(seen_cfgs) == 1
    assert seen_cfgs[0]["visual_engine"] == "musetalk_bank"
    assert seen_cfgs[0]["personalive_enabled"] is True
    assert lock_held_during_probes == [("live.status", False), ("gpu_info", False)]

    # Verify _load() also reads active_config (not config) and stops idle live studio if 0 clients.
    stopped = {"called": False}

    def fake_idle_status():
        st = fake_live_status()
        st["active_clients"] = 0
        return st

    monkeypatch.setattr(runtime.live, "status", fake_idle_status)
    monkeypatch.setattr(runtime.live, "stop", lambda: stopped.__setitem__("called", True) or {})
    monkeypatch.setattr(runtime, "prepare_engine", lambda _t: runtime.root / "llama-server")
    proc = Mock()
    proc.poll.return_value = None
    monkeypatch.setattr("server.runtime.subprocess.Popen", lambda *a, **k: proc)
    runtime.http = Mock()
    runtime.http.get.return_value = Response()

    task = wait_task(runtime, runtime.load(model.id))
    assert task.state == "completed"
    assert stopped["called"] is True
    assert seen_cfgs[-1]["visual_engine"] == "musetalk_bank"
    runtime.unload()


def test_live_lifecycle_hardening_stop_comfy_tts_preview_vram_secrets_and_driving_routes(
    runtime, spec, monkeypatch
):
    import base64
    from server.runtime import _scrub_secret_text

    # 1. _scrub_secret_text redacts RTMP userinfo, RTMP stream keys, query secrets, and sk-... tokens.
    raw = (
        "push failed for rtmp://alice:s3cr3t@live.example.com/app/stream_key_999 "
        "and rtmps://a.rtmp.youtube.com/live2/xxxx-yyyy-zzzz "
        "with key sk-proj-1234567890abcdef"
    )
    scrubbed = _scrub_secret_text(raw)
    assert "s3cr3t" not in scrubbed
    assert "stream_key_999" not in scrubbed
    assert "xxxx-yyyy-zzzz" not in scrubbed
    assert "sk-proj-1234567890abcdef" not in scrubbed
    assert "rtmp://***@live.example.com/app/***" in scrubbed
    assert "rtmps://a.rtmp.youtube.com/live2/***" in scrubbed
    extracted = runtime._extract_live_secrets(
        {"rtmp_url": "rtmps://live.example.com/app/secret_stream_key"}
    )
    assert "rtmps://live.example.com/app/secret_stream_key" in extracted

    # 2. live_check_vram passes actual loaded LLM VRAM even in external_api mode.
    big_llm = spec.model_copy(update={"id": "loaded-8g", "min_vram_mib": 8192})
    runtime.add_model(big_llm.model_dump())
    runtime.loaded = big_llm.id
    monkeypatch.setattr(
        runtime, "gpu_info", lambda: {"name": "NVIDIA L4", "total_mib": 24576, "used_mib": 8192}
    )
    budget = runtime.live_check_vram(
        {
            "visual_engine": "flashhead_lite",
            "tts_engine": "qwen3_tts",
            "llm_mode": "external_api",
        }
    )
    assert budget["ok"] is True
    assert any("8192" in w for w in budget["warnings"])
    runtime.loaded = None

    # 3. stop_comfyui() preserves shared canvas tunnel when Live Studio is running.
    comfy_proc = Mock()
    comfy_proc.poll.return_value = None
    comfy_proc.pid = None
    comfy_proc.terminate.side_effect = lambda: setattr(comfy_proc.poll, "return_value", 0)
    runtime.comfy.proc = comfy_proc
    runtime.comfy.state = "ready"
    canvas_stop_spy = Mock()
    monkeypatch.setattr(runtime.comfy.canvas, "stop", canvas_stop_spy)
    monkeypatch.setattr(runtime.live, "is_running", lambda: True)
    runtime.stop_comfyui()
    assert runtime.comfy.state == "stopped"
    assert runtime.comfy.proc is None
    comfy_proc.terminate.assert_called_once()
    canvas_stop_spy.assert_not_called()

    # 4. preview_comfyui_tts() rejects with 409 when Live Studio has active clients.
    monkeypatch.setattr(runtime.live, "active_client_count", lambda: 1, raising=False)
    monkeypatch.setattr(runtime.comfy.tts, "get_cached_preview", lambda *a, **k: None)
    monkeypatch.setattr(
        runtime.comfy,
        "_resolve_preview_inputs",
        lambda _req: {
            "preview_id": "prev_test_001",
            "engine_id": "qwen3_tts",
            "effective_voice_preset": "shen_zhiyi",
            "sample_rate": 24000,
            "resolved_text": "你好世界",
        },
    )
    with pytest.raises(ColabOneConflictError, match="实时演播正在推流"):
        runtime.preview_comfyui_tts(
            {"engine_id": "qwen3_tts", "voice_preset": "shen_zhiyi", "preview_text": "你好世界"}
        )
    with pytest.raises(ColabOneConflictError, match="实时演播正在推流"):
        runtime.prefetch_comfyui_tts_previews(
            {"engine_id": "qwen3_tts", "voice_presets": ["shen_zhiyi"]}
        )
    with pytest.raises(ColabOneConflictError, match="实时演播正在推流"):
        runtime.start_comfyui()

    # Reset active clients and verify start_live_studio busy check + fail-fast VRAM error formatting.
    monkeypatch.setattr(runtime.live, "active_client_count", lambda: 0, raising=False)
    monkeypatch.setattr(runtime.live, "is_running", lambda: False)
    blocker = ColabOneTask("download", "m1")
    runtime.tasks[blocker.id] = blocker
    for mode in ("local_gguf", "external_api"):
        with pytest.raises(ColabOneConflictError):
            runtime.start_live_studio({"llm_mode": mode})
    blocker.state = "completed"

    monkeypatch.setattr(
        runtime, "gpu_info", lambda: {"name": "Tesla T4", "total_mib": 8192, "used_mib": 0}
    )
    with pytest.raises(ValueError) as vram_exc:
        runtime.start_live_studio({"visual_engine": "musetalk_bank", "llm_mode": "local_gguf"})
    err_str = str(vram_exc.value)
    assert "8192" in err_str and "建议" in err_str

    # 5. build_live_avatar_pack passes a callable adapter (never TypeError on ColabOneComfyManager).
    import io
    from PIL import Image

    buf = io.BytesIO()
    Image.new("RGB", (32, 32), (80, 120, 200)).save(buf, format="PNG")
    portrait_b64 = base64.b64encode(buf.getvalue()).decode("ascii")
    pack_task = wait_task(
        runtime,
        runtime.build_live_avatar_pack(
            {
                "avatar_id": "av_pack_test",
                "name": "测试主播",
                "portrait_b64": portrait_b64,
                "portrait_filename": "p.png",
                "actions": ["idle_loop"],
                "dance_ids": [],
            }
        ),
    )
    assert pack_task.state == "failed"
    assert "not callable" not in pack_task.error
    assert "工作流" in pack_task.error

    # 6. Auto-release skips releasing when Live Studio becomes active during grace window.
    runtime._workflow_auto_release_grace_seconds = 0.0
    runtime._pending_workflow_auto_release = True
    runtime._workflow_auto_release["enabled"] = True
    runtime._workflow_auto_release["state"] = "releasing"
    runtime._workflow_auto_release["task_id"] = "wf_done_1"
    monkeypatch.setattr(runtime.live, "active_client_count", lambda: 1, raising=False)
    runtime._execute_pending_workflow_auto_release("wf_done_1")
    assert runtime.quiescing is False
    assert runtime._workflow_auto_release["state"] == "skipped"
    monkeypatch.setattr(runtime.live, "active_client_count", lambda: 0, raising=False)

    # 7. GET/POST/DELETE /api/v1/live/driving-motions + snapshot live_driving_motions + canvas/start standalone.
    trashed_motions: list[str] = []
    monkeypatch.setattr(
        runtime.drive, "trash_live_driving_motion", lambda mid: trashed_motions.append(mid)
    )
    canvas_start_spy = Mock(return_value={"state": "starting", "enabled": True})
    monkeypatch.setattr(runtime.comfy.canvas, "start", canvas_start_spy)

    with TestClient(create_colabone_app(runtime, TOKEN)) as client:
        # Canvas can start even when neither ComfyUI nor Live Studio is running yet.
        cv = client.post(
            "/api/v1/comfyui/canvas/start",
            headers=AUTH,
            json={
                "domain": "https://canvas.example.com",
                "tunnel_token": "eyJhIjoiMSIsInQiOiIyIiwicyI6IjMifQ==",
            },
        )
        assert cv.status_code == 200, cv.text
        canvas_start_spy.assert_called_once()

        up = client.post(
            "/api/v1/live/driving-motions",
            headers=AUTH,
            json={
                "id": "dance_demo_1",
                "label": "热舞一",
                "filename": "dance.mp4",
                "data": base64.b64encode(b"fake-mp4-data").decode("ascii"),
            },
        )
        assert up.status_code == 200, up.text
        assert "live-driving-motions" in runtime._dirty_seq

        listed = client.get("/api/v1/live/driving-motions", headers=AUTH)
        assert listed.status_code == 200
        assert [m["id"] for m in listed.json()] == ["dance_demo_1"]

        snap = client.get("/api/v1/snapshot", headers=AUTH).json()
        assert [m["id"] for m in snap["live_driving_motions"]] == ["dance_demo_1"]

        deleted = client.delete("/api/v1/live/driving-motions/dance_demo_1", headers=AUTH)
        assert deleted.status_code == 200
        assert trashed_motions == ["dance_demo_1"]
        assert client.get("/api/v1/live/driving-motions", headers=AUTH).json() == []


def test_live_runtime_pass2_fps_forwarding_canvas_rollback_and_build_pack_cancel(
    runtime, monkeypatch
):
    import base64
    import io
    from PIL import Image

    # 1. Medium #16: start_live_studio rolls back auto-started canvas tunnel if live.start() fails
    canvas_events: list[str] = []
    runtime.comfy.canvas.enabled = False
    monkeypatch.setattr(
        runtime.comfy.canvas,
        "start",
        lambda _cfg: (
            setattr(runtime.comfy.canvas, "enabled", True),
            canvas_events.append("start"),
        ),
    )
    monkeypatch.setattr(
        runtime.comfy.canvas,
        "stop",
        lambda: (
            setattr(runtime.comfy.canvas, "enabled", False),
            canvas_events.append("stop"),
        ),
    )
    monkeypatch.setattr(
        runtime,
        "gpu_info",
        lambda: {"name": "NVIDIA A100", "total_mib": 81920, "used_mib": 0},
    )
    monkeypatch.setattr(
        runtime.live,
        "ensure_session_dependencies",
        lambda *_a, **_kw: {"tts_engine": "qwen3_tts"},
    )
    monkeypatch.setattr(
        runtime.live,
        "start",
        lambda *_a, **_kw: (_ for _ in ()).throw(RuntimeError("boom-on-start")),
    )

    fail_task = wait_task(
        runtime,
        runtime.start_live_studio(
            {
                "visual_engine": "flashhead_lite",
                "tts_engine": "qwen3_tts",
                "llm_mode": "external_api",
                "external_llm": {
                    "base_url": "https://api.example.com/v1",
                    "api_key": "sk-rollback-test-123456",
                    "model": "gpt-4o-mini",
                },
                "canvas_config": {
                    "domain": "https://canvas.example.com",
                    "tunnel_token": "eyJhIjoiMSIsInQiOiIyIiwicyI6IjMifQ==",
                },
            }
        ),
    )
    assert fail_task.kind == "live_start"
    assert fail_task.state == "failed"
    assert "boom-on-start" in fail_task.error
    assert canvas_events == ["start", "stop"]
    assert runtime.comfy.canvas.enabled is False

    # 2. Medium #13: _comfy_clip_runner respects request.fps (not hardcoded 25)
    seen_fps: list[int] = []
    monkeypatch.setattr(
        runtime.comfy,
        "generate_live_action_clip",
        lambda **kw: (
            seen_fps.append(int(kw["fps"])),
            {"bytes": b"fake-clip-bytes", "duration_seconds": 3.0},
        )[1],
        raising=False,
    )
    buf = io.BytesIO()
    Image.new("RGB", (32, 32), (80, 120, 200)).save(buf, format="PNG")
    portrait_b64 = base64.b64encode(buf.getvalue()).decode("ascii")
    pack_task = wait_task(
        runtime,
        runtime.build_live_avatar_pack(
            {
                "avatar_id": "av_fps_30",
                "name": "高帧率主播",
                "portrait_b64": portrait_b64,
                "portrait_filename": "p.png",
                "actions": ["idle_loop"],
                "dance_ids": [],
                "fps": 30,
            }
        ),
    )
    assert pack_task.state == "completed", pack_task.error
    assert seen_fps == [30]
    assert pack_task.progress == pytest.approx(1.0)

    # 3. Medium #15: stop_comfyui cancels running live_build_pack tasks too
    build_task = ColabOneTask("live_build_pack", "av_fps_30")
    orig_cancel = build_task.request_cancel
    cancel_called = {"flag": False}

    def _cancel_and_finish():
        cancel_called["flag"] = True
        orig_cancel()
        build_task.state = "cancelled"

    build_task.request_cancel = _cancel_and_finish  # type: ignore[method-assign]
    runtime.tasks[build_task.id] = build_task
    monkeypatch.setattr(runtime.comfy, "stop_server", lambda: None)
    monkeypatch.setattr(runtime.live, "is_running", lambda: False)
    runtime.stop_comfyui()
    assert cancel_called["flag"] is True


def test_app_auth_non_ascii_returns_401_and_rejects_nonfinite_floats(runtime):
    app = create_colabone_app(runtime, TOKEN)
    with TestClient(app) as client:
        # Non-ASCII Authorization header bytes (decoded by ASGI as non-ASCII str) must return 401, not 500.
        res = client.get(
            "/api/v1/status", headers={b"authorization": b"Bearer \xe9\xe8"}
        )
        assert res.status_code == 401

        # NaN / Infinity floats must be rejected by Pydantic ConfigDict(allow_inf_nan=False).
        for path, body in (
            ("/api/v1/configure", '{"deadline": NaN}'),
            ("/api/v1/configure", '{"deadline": Infinity}'),
            ("/api/v1/models/test-model/load", '{"temperature": NaN}'),
            ("/api/v1/models/test-model/download", '{"temperature": Infinity}'),
        ):
            bad = client.post(
                path,
                content=body,
                headers={**AUTH, "Content-Type": "application/json"},
            )
            assert bad.status_code == 422


def test_runtime_http_thread_local_sessions_and_close(runtime):
    import requests

    assert isinstance(runtime.http, requests.Session)
    sessions = []

    def worker():
        s1 = runtime._http_session()
        s2 = runtime._http_session()
        assert s1 is s2
        assert s1.trust_env is False
        assert tuple(s1.cookies.get_policy().allowed_domains()) == ()
        sessions.append(s1)

    t1 = threading.Thread(target=worker)
    t2 = threading.Thread(target=worker)
    t1.start()
    t2.start()
    t1.join(timeout=5)
    t2.join(timeout=5)
    assert len(sessions) == 2 and sessions[0] is not sessions[1]

    closed = []
    for s in sessions:
        orig_close = s.close
        s.close = lambda orig=orig_close, sess=s: (closed.append(sess), orig())
    runtime.close_http_sessions()
    assert len(closed) == 2

    mock_http = Mock()
    runtime.http = mock_http
    assert runtime._http_session() is mock_http


def test_download_single_subrange_continuation_fsync_short_stream_and_cancel_abort(
    runtime, spec, monkeypatch
):
    from server import runtime as runtime_mod

    # 1. Partial 206 sub-ranges loop and complete with fsync.
    fsync_calls = []
    real_fsync = runtime_mod.os.fsync
    monkeypatch.setattr(
        runtime_mod.os,
        "fsync",
        lambda fd: (fsync_calls.append(fd), real_fsync(fd))[1],
    )
    responses = iter(
        [
            Response(b"1234", 206, {"Content-Range": "bytes 0-3/8"}),
            Response(b"5678", 206, {"Content-Range": "bytes 4-7/8"}),
        ]
    )
    runtime.http = Mock()
    runtime.http.get.side_effect = lambda *a, **k: next(responses)
    task = wait_task(runtime, runtime.download(spec.id))
    assert task.state == "completed", task.error
    assert runtime.installed(spec)
    assert len(fsync_calls) >= 2
    assert runtime.http.get.call_count == 2
    assert runtime.http.get.call_args_list[1].kwargs["headers"]["Range"] == "bytes=4-"

    # 2. Permanently short stream hits retry cap, fails, and preserves .part file.
    runtime.delete(spec.id)
    runtime.http = Mock()
    runtime.http.get.side_effect = lambda *a, **k: Response(b"1234", 200)
    short_task = wait_task(runtime, runtime.download(spec.id))
    assert short_task.state == "failed"
    part = runtime.models_dir / spec.id / "model.gguf.part"
    assert part.is_file() and part.read_bytes() == b"1234"
    part.unlink()

    # 3. Cancel hook invokes _abort_response in both _download_single and prepare_engine.
    aborted = []
    monkeypatch.setattr(runtime_mod, "_abort_response", lambda resp: aborted.append(resp))

    entered = threading.Event()
    unblock = threading.Event()

    class BlockingResp(Response):
        def iter_content(self, *args):
            entered.set()
            unblock.wait(timeout=3)
            yield b"1234"

        def close(self):
            unblock.set()

    runtime.http = Mock()
    runtime.http.get.return_value = BlockingResp()
    snap = runtime.download(spec.id)
    assert entered.wait(timeout=3)
    runtime.tasks[snap["id"]].request_cancel()
    cancelled = wait_task(runtime, snap)
    assert cancelled.state == "cancelled"
    assert len(aborted) >= 1

    aborted.clear()
    entered.clear()
    unblock.clear()
    (runtime.catalog / "engine.json").write_text(
        json.dumps(
            {
                "tag": "cancel-test",
                "assets": [{"url": "https://example.invalid/e.tar.gz", "sha256": "a" * 64}],
            }
        ),
        encoding="utf-8",
    )
    runtime.http = Mock()
    runtime.http.get.return_value = BlockingResp()
    eng_task = ColabOneTask("load", spec.id)

    def _run_prepare():
        try:
            runtime.prepare_engine(eng_task)
        except Exception:
            pass

    th = threading.Thread(target=_run_prepare)
    th.start()
    assert entered.wait(timeout=3)
    eng_task.request_cancel()
    th.join(timeout=3)
    assert len(aborted) >= 1


def test_runtime_task_ordering_sanitized_env_locks_delete_toctou_and_api_id_override(
    runtime, spec, monkeypatch
):
    # 1. Task error/finished_at/elapsed_seconds are visible as soon as state != "running".
    t_snap = runtime.submit("test", spec.id, lambda _t: (_ for _ in ()).throw(RuntimeError("boom")))
    task = runtime.tasks[t_snap["id"]]
    deadline = time.monotonic() + 3
    while time.monotonic() < deadline:
        with task.lock:
            if task.state != "running":
                assert task.state == "failed"
                assert "boom" in task.error
                assert task.finished_at is not None
                assert task.elapsed_seconds is not None
                break
        time.sleep(0.005)
    else:
        pytest.fail("task did not finish")

    # 2. _load strips secret env vars and holds _comfy_lifecycle_lock around comfy.stop_server().
    install(runtime, spec)
    monkeypatch.setenv("COLABONE_REMOTE_CONFIG", "/content/secret.json")
    monkeypatch.setenv("HF_TOKEN", "hf_secret123456")
    monkeypatch.setenv("OPENAI_API_KEY", "sk-secret123456")
    monkeypatch.setenv("TUNNEL_TOKEN", "tun-secret")
    monkeypatch.setattr(
        runtime, "gpu_info", lambda: {"name": "Tesla T4", "total_mib": 16384, "used_mib": 0}
    )
    monkeypatch.setattr(
        runtime, "prepare_engine", lambda _t: runtime.root / "llama-server"
    )
    comfy_lock_held = []

    def spy_stop_server():
        comfy_lock_held.append(runtime._comfy_lifecycle_lock.locked())

    monkeypatch.setattr(runtime.comfy, "stop_server", spy_stop_server)
    captured_env = []

    def fake_popen(args, env=None, **kwargs):
        captured_env.append(dict(env or {}))
        proc = Mock()
        proc.poll.return_value = None
        return proc

    monkeypatch.setattr("server.runtime.subprocess.Popen", fake_popen)
    runtime.http = Mock()
    runtime.http.get.return_value = Response()
    load_task = wait_task(runtime, runtime.load(spec.id))
    assert load_task.state == "completed", load_task.error
    assert comfy_lock_held == [True]
    for secret_key in ("COLABONE_REMOTE_CONFIG", "HF_TOKEN", "OPENAI_API_KEY", "TUNNEL_TOKEN"):
        assert secret_key not in captured_env[0]
    runtime.unload()

    # 3. delete() runs shutil.rmtree() OUTSIDE runtime.lock after atomic rename.
    import shutil as _shutil

    rmtree_lock_states = []
    real_rmtree = _shutil.rmtree

    def spy_rmtree(path, *args, **kwargs):
        rmtree_lock_states.append(runtime.lock.held_by_current_thread())
        return real_rmtree(path, *args, **kwargs)

    monkeypatch.setattr("server.runtime.shutil.rmtree", spy_rmtree)
    runtime.delete(spec.id)
    assert rmtree_lock_states == [False]
    assert not runtime.installed(spec)

    # 4. _restore_from_drive preserves locally modified api_id overrides.
    runtime.update_model_api_id(spec.id, "local-custom-alias")

    def fake_pull(stage_dir):
        stage_dir.mkdir(parents=True, exist_ok=True)
        remote_spec = spec.model_copy(update={"api_id": "stale-drive-alias"})
        (stage_dir / "models.json").write_text(
            json.dumps([remote_spec.model_dump()]), encoding="utf-8"
        )
        return True

    monkeypatch.setattr(runtime.drive, "pull_workspace", fake_pull)
    assert runtime._restore_from_drive() is True
    assert runtime.models[spec.id].api_id == "local-custom-alias"

    # 5. run_comfyui_workflow does not wipe active _workflow_auto_release when Drive is unavailable.
    wf = runtime.comfy.store.save_workflow(
        {
            "id": "wf-auto-rel-guard",
            "name": "守卫测试",
            "workflow": {"1": {"class_type": "CLIPTextEncode", "inputs": {"text": "ok"}}},
        }
    )
    runtime._workflow_auto_release.update(
        {"enabled": True, "state": "armed", "task_id": "prev-tid", "message": "keep"}
    )
    monkeypatch.setattr(runtime.drive, "available", lambda: False)
    with pytest.raises(ValueError, match="尚未连接 Google 云盘"):
        runtime.run_comfyui_workflow(
            {"workflow_id": wf["id"], "auto_release_after_sync": True}
        )
    assert runtime._workflow_auto_release["enabled"] is True
    assert runtime._workflow_auto_release["state"] == "armed"
    assert runtime._workflow_auto_release["task_id"] == "prev-tid"

    # 6. start_live_studio recomputes loaded_vram_mib / need_unload_llm inside _live_lifecycle_lock.
    runtime.loaded = None
    runtime.proc = None
    stop_engine_calls = []
    monkeypatch.setattr(runtime, "_stop_engine", lambda: stop_engine_calls.append(1))
    monkeypatch.setattr(
        runtime,
        "gpu_info",
        lambda: {"name": "NVIDIA L4", "total_mib": 24576, "used_mib": 0},
    )
    # Simulate an LLM appearing between preflight and acquiring _live_lifecycle_lock.
    orig_cancel_warm = runtime.comfy.tts.cancel_prefetch_and_release_warm_worker

    def inject_loaded_llm(*args, **kwargs):
        runtime.loaded = spec.id
        runtime.models[spec.id] = spec.model_copy(update={"min_vram_mib": 14000})
        return orig_cancel_warm(*args, **kwargs)

    monkeypatch.setattr(
        runtime.comfy.tts,
        "cancel_prefetch_and_release_warm_worker",
        inject_loaded_llm,
    )
    seen_start_vram = []
    monkeypatch.setattr(
        runtime.live,
        "ensure_session_dependencies",
        lambda *_a, **_kw: {"tts_engine": "qwen3_tts"},
    )
    monkeypatch.setattr(
        runtime.live,
        "start",
        lambda payload, **kw: (seen_start_vram.append(kw["loaded_model_vram_mib"]), {"running": True})[1],
    )
    live_task = wait_task(
        runtime,
        runtime.start_live_studio(
            {
                "visual_engine": "flashhead_lite",
                "tts_engine": "qwen3_tts",
                "llm_mode": "external_api",
                "external_llm": {
                    "base_url": "https://api.example.com/v1",
                    "api_key": "sk-recompute-123456",
                    "model": "gpt-4o-mini",
                },
            }
        ),
    )
    assert live_task.kind == "live_start"
    assert live_task.state == "completed", live_task.error
    assert stop_engine_calls == [1]
    assert seen_start_vram == [0]

    # 7. Async live_start cancellation via stop_live_studio and prepare_release never deadlocks.
    started_prov = threading.Event()
    unblock_prov = threading.Event()

    def slow_provision(task, _payload):
        started_prov.set()
        while not unblock_prov.is_set():
            task.check()
            time.sleep(0.02)
        return {"tts_engine": "qwen3_tts"}

    monkeypatch.setattr(runtime.live, "ensure_session_dependencies", slow_provision)
    snap_task = runtime.start_live_studio(
        {
            "visual_engine": "flashhead_lite",
            "tts_engine": "qwen3_tts",
            "llm_mode": "external_api",
            "external_llm": {
                "base_url": "https://api.example.com/v1",
                "api_key": "sk-cancel-test-123456",
                "model": "gpt-4o-mini",
            },
        }
    )
    assert snap_task["kind"] == "live_start"
    assert started_prov.wait(timeout=3.0)
    runtime.stop_live_studio()
    cancelled_task = wait_task(runtime, snap_task)
    assert cancelled_task.state == "cancelled"
    assert runtime.live.status()["state"] == "stopped"
    assert runtime.live.status()["error"] == ""

    started_prov.clear()
    snap_task2 = runtime.start_live_studio(
        {
            "visual_engine": "flashhead_lite",
            "tts_engine": "qwen3_tts",
            "llm_mode": "external_api",
            "external_llm": {
                "base_url": "https://api.example.com/v1",
                "api_key": "sk-release-test-123456",
                "model": "gpt-4o-mini",
            },
        }
    )
    assert started_prov.wait(timeout=3.0)
    rel_res = runtime.prepare_release(auto=True)
    assert rel_res["ok"] is True
    cancelled_task2 = wait_task(runtime, snap_task2)
    assert cancelled_task2.state == "cancelled"


def test_vm_boot_vm_update_and_hub_hardening():
    import ast
    from pathlib import Path
    from common.hub import model_files, _fetch_models_page

    repo_root = Path(__file__).resolve().parents[1]

    # 1. vm_boot.py enforces --require-hashes, start_new_session + killpg on pip timeout,
    #    and sleeps outside the except block in the health readiness loop.
    boot_src = (repo_root / "server" / "vm_boot.py").read_text(encoding="utf-8")
    assert '"--require-hashes"' in boot_src
    assert "start_new_session=True" in boot_src
    assert "os.killpg(pip_proc.pid" in boot_src
    boot_ast = ast.parse(boot_src)
    boot_fn = next(n for n in boot_ast.body if isinstance(n, ast.FunctionDef) and n.name == "colabone_boot")
    health_loops = [
        n
        for n in ast.walk(boot_fn)
        if isinstance(n, ast.For)
        and isinstance(n.iter, ast.Call)
        and getattr(n.iter.func, "id", "") == "range"
        and n.iter.args
        and getattr(n.iter.args[0], "value", None) == 60
    ]
    assert len(health_loops) == 1
    assert any(
        isinstance(stmt, ast.Expr)
        and isinstance(stmt.value, ast.Call)
        and getattr(stmt.value.func, "attr", "") == "sleep"
        for stmt in health_loops[0].body
    )

    # 2. vm_update.py re-reads cfg_path and re-checks /api/v1/status right before SIGTERM,
    #    and opens colabone-server.log with O_NOFOLLOW and 0o600.
    upd_src = (repo_root / "server" / "vm_update.py").read_text(encoding="utf-8")
    sigterm_idx = upd_src.index("os.kill(pid, signal.SIGTERM)")
    pre_kill = upd_src[:sigterm_idx]
    assert pre_kill.count("cfg_path.read_text(") >= 2
    assert pre_kill.count("/api/v1/status") >= 2
    assert "O_NOFOLLOW" in upd_src
    assert "os.fchmod(log_fd, 0o600)" in upd_src

    # 3. common/hub.py rejects dot segments & non-strings in model_files and _fetch_models_page.
    for bad_repo in ("../etc", "org/..", "./model", "org/.", "../..", None, 123, ""):
        with pytest.raises(ValueError):
            model_files(bad_repo, session=Mock())

    fake_resp = Mock(status_code=200, links={})
    fake_resp.json.return_value = [
        "not-a-dict",
        None,
        {"id": 123},
        {"id": "../evil"},
        {"id": "org/.."},
        {"id": "./local"},
        {"id": "valid-org/valid-model", "downloads": 10},
    ]
    fake_session = Mock()
    fake_session.get.return_value = fake_resp
    models, _ = _fetch_models_page("", "downloads", "all", "", 10, "", "", fake_session)
    assert [m["repo"] for m in models] == ["valid-org/valid-model"]
