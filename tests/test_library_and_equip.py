import hashlib
import json
import time
from pathlib import Path
from unittest.mock import Mock, call

import pytest
from fastapi.testclient import TestClient

from common.files import atomic_json
from common.models import ModelSpec, parse_hf_input, resolve_import
from conftest import wait_task
from server.app import create_colabone_app

ROOT = Path(__file__).resolve().parents[1]
TOKEN = "test-control-token-at-least-32-chars"
AUTH = {"Authorization": "Bearer " + TOKEN}


class StreamResponse:
    def __init__(self, body=b"12345678", status=200, headers=None):
        self.body = body
        self.status_code = status
        self.headers = headers or {}

    def __enter__(self):
        return self

    def __exit__(self, *args):
        pass

    def raise_for_status(self):
        pass

    def iter_content(self, *args):
        yield self.body

    def close(self):
        pass


def make_hf_session(
    filename="qwen-q4_k_m.gguf",
    sha="c" * 40,
    digest="d" * 64,
    size=4096,
):
    session = Mock()
    resp = Mock()
    resp.raise_for_status.return_value = None
    resp.json.return_value = {
        "sha": sha,
        "siblings": [
            {
                "rfilename": filename,
                "lfs": {"sha256": digest, "size": size},
            }
        ],
    }
    session.get.return_value = resp
    return session


def install_weights(runtime, model: ModelSpec, payload: bytes = b"12345678"):
    directory = runtime.models_dir / model.id
    directory.mkdir(parents=True, exist_ok=True)
    receipt = {}
    for f in model.files:
        target = directory / f.path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(payload)
        receipt[f.path] = f.sha256
    atomic_json(directory / "verified.json", receipt)


# ---------------------------------------------------------------------------
# 1. parse_hf_input and resolve_import (offline mocked session)
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    "raw,filename,revision,expected",
    [
        ("owner/repo", "", "main", ("owner/repo", "", "main")),
        (
            "  owner/repo  ",
            " qwen-q4_k_m.gguf ",
            "v1.0",
            ("owner/repo", "qwen-q4_k_m.gguf", "v1.0"),
        ),
        ("https://huggingface.co/owner/repo", "", "main", ("owner/repo", "", "main")),
        ("https://www.huggingface.co/owner/repo/", "", "main", ("owner/repo", "", "main")),
        ("https://huggingface.co/owner/repo/tree/dev", "", "main", ("owner/repo", "", "dev")),
        (
            "https://huggingface.co/owner/repo/tree/dev",
            "",
            "explicit-branch",
            ("owner/repo", "", "explicit-branch"),
        ),
        (
            "https://huggingface.co/owner/repo/blob/main/qwen-q4_k_m.gguf",
            "",
            "main",
            ("owner/repo", "qwen-q4_k_m.gguf", "main"),
        ),
        (
            "https://huggingface.co/owner/repo/resolve/main/qwen-q4_k_m.gguf",
            "",
            "main",
            ("owner/repo", "qwen-q4_k_m.gguf", "main"),
        ),
        (
            "https://huggingface.co/owner/repo/resolve/dev/sub/qwen-q4_k_m.gguf",
            "",
            "main",
            ("owner/repo", "sub/qwen-q4_k_m.gguf", "dev"),
        ),
        (
            "https://huggingface.co/owner/repo/blob/main/README.md",
            "",
            "main",
            ("owner/repo", "", "main"),
        ),
    ],
)
def test_parse_hf_input_variants(raw, filename, revision, expected):
    assert parse_hf_input(raw, filename, revision) == expected


@pytest.mark.parametrize(
    "repo_input,filename_input,expected_rev,custom_name,expected_name",
    [
        ("owner/repo", "qwen-q4_k_m.gguf", "main", "", "repo"),
        ("https://huggingface.co/owner/repo", "qwen-q4_k_m.gguf", "main", "", "repo"),
        ("https://huggingface.co/owner/repo/tree/dev", "qwen-q4_k_m.gguf", "dev", "", "repo"),
        ("https://huggingface.co/owner/repo/blob/main/qwen-q4_k_m.gguf", "", "main", "", "repo"),
        (
            "https://huggingface.co/owner/repo/resolve/main/qwen-q4_k_m.gguf",
            "",
            "main",
            "My Custom Qwen",
            "My Custom Qwen",
        ),
    ],
)
def test_resolve_import_url_variants_and_custom_name(
    repo_input, filename_input, expected_rev, custom_name, expected_name
):
    session = make_hf_session(filename="qwen-q4_k_m.gguf", sha="e" * 40, digest="f" * 64, size=8192)
    spec = resolve_import(
        repo_input,
        filename_input,
        session=session,
        token="hf_dummy",
        custom_name=custom_name,
    )
    assert spec.name == expected_name
    assert spec.id.startswith("hf-")
    assert len(spec.files) == 1
    assert spec.files[0].repo == "owner/repo"
    assert spec.files[0].revision == "e" * 40
    assert spec.files[0].path == "qwen-q4_k_m.gguf"
    assert spec.files[0].size == 8192
    assert spec.files[0].sha256 == "f" * 64
    session.get.assert_called_once_with(
        f"https://huggingface.co/api/models/owner/repo/revision/{expected_rev}",
        params={"blobs": "true"},
        headers={"Authorization": "Bearer hf_dummy"},
        timeout=30,
    )


@pytest.mark.parametrize(
    "bad_repo,filename",
    [
        ("https://evil.example/a/b", "qwen-q4_k_m.gguf"),
        ("https://evil.example/owner/repo/blob/main/qwen-q4_k_m.gguf", ""),
        ("https://huggingface.co.evil.example/owner/repo", "qwen-q4_k_m.gguf"),
        ("ftp://huggingface.co/owner/repo", "qwen-q4_k_m.gguf"),
        ("just-one-segment", "qwen-q4_k_m.gguf"),
        ("https://huggingface.co/owner/repo", ""),
    ],
)
def test_resolve_import_rejects_non_hf_urls_and_missing_file(bad_repo, filename):
    session = make_hf_session()
    with pytest.raises(ValueError):
        resolve_import(bad_repo, filename, session=session)
    session.get.assert_not_called()


# ---------------------------------------------------------------------------
# 3. Remote atomic download(..., load=True, options=...) & model switching
# ---------------------------------------------------------------------------


def test_remote_atomic_download_and_equip_with_options(runtime, spec, monkeypatch):
    def fake_get(url, **kwargs):
        if "huggingface.co" in url:
            return StreamResponse(b"12345678", status=200)
        if url == "http://127.0.0.1:8081/health":
            return StreamResponse(b"ok", status=200)
        raise AssertionError(f"unexpected URL: {url}")

    runtime.http = Mock()
    runtime.http.get.side_effect = fake_get
    monkeypatch.setattr(
        runtime,
        "gpu_info",
        lambda: {"name": "NVIDIA T4", "total_mib": 15360, "used_mib": 512},
    )
    monkeypatch.setattr(
        runtime, "prepare_engine", lambda _task: runtime.root / "llama-server"
    )
    proc = Mock()
    proc.poll.return_value = None
    popen_calls = []

    def fake_popen(args, **kwargs):
        popen_calls.append(list(args))
        return proc

    monkeypatch.setattr("server.runtime.subprocess.Popen", fake_popen)

    load_invocations = []
    real_load = runtime._load

    def spy_load(task, load_spec):
        load_invocations.append(
            {
                "task_kind": task.kind,
                "installed": runtime.installed(spec),
                "transfer_cleared": task.transfer is None,
                "context_length": load_spec.context_length,
                "temperature": load_spec.temperature,
            }
        )
        return real_load(task, load_spec)

    monkeypatch.setattr(runtime, "_load", spy_load)

    app = create_colabone_app(runtime, TOKEN)
    with TestClient(app) as client:
        resp = client.post(
            f"/api/v1/models/{spec.id}/download",
            headers=AUTH,
            json={"load": True, "context_length": 16384, "temperature": 0.3},
        )
        assert resp.status_code == 200
        task = wait_task(runtime, resp.json())

        assert task.state == "completed"
        assert task.kind == "load"
        assert runtime.installed(spec)
        assert (runtime.models_dir / spec.id / "model.gguf").read_bytes() == b"12345678"
        assert any("SHA-256 校验通过" in entry["message"] for entry in task.logs)

        assert load_invocations == [
            {
                "task_kind": "load",
                "installed": True,
                "transfer_cleared": True,
                "context_length": 16384,
                "temperature": 0.3,
            }
        ]
        assert len(popen_calls) == 1
        ctx_idx = popen_calls[0].index("--ctx-size")
        assert popen_calls[0][ctx_idx + 1] == "16384"

        assert runtime.loaded == spec.id
        assert runtime.active_options == {"context_length": 16384, "temperature": 0.3}

        # Base catalog manifest keeps its default values
        assert runtime.models[spec.id].context_length == 8192
        assert runtime.models[spec.id].temperature == 0.7


def test_remote_atomic_download_hash_failure_aborts_before_load(runtime, spec, monkeypatch):
    runtime.http = Mock()
    runtime.http.get.return_value = StreamResponse(b"corrupt!", status=200)
    spy_load = Mock()
    monkeypatch.setattr(runtime, "_load", spy_load)

    with TestClient(create_colabone_app(runtime, TOKEN)) as client:
        resp = client.post(
            f"/api/v1/models/{spec.id}/download",
            headers=AUTH,
            json={"load": True, "context_length": 16384, "temperature": 0.3},
        )
        assert resp.status_code == 200
        task = wait_task(runtime, resp.json())

    assert task.state == "failed"
    assert "校验" in task.error
    assert not runtime.installed(spec)
    spy_load.assert_not_called()


def test_load_stops_existing_engine_first_before_loading_new_model(runtime, spec, monkeypatch):
    install_weights(runtime, spec)
    second = ModelSpec(
        id="second-model",
        name="Second Model",
        files=[
            {
                "repo": "org/second",
                "revision": "b" * 40,
                "path": "second.gguf",
                "size": 8,
                "sha256": hashlib.sha256(b"12345678").hexdigest(),
                "role": "model",
            }
        ],
    )
    runtime.add_model(second.model_dump())
    install_weights(runtime, second)

    old_proc = Mock()
    old_proc.poll.return_value = None
    runtime.proc = old_proc
    runtime.loaded = spec.id
    runtime.active_options = {"context_length": 4096, "temperature": 0.9}

    events = []
    real_stop_engine = runtime._stop_engine

    def tracked_stop_engine():
        events.append(("stop_engine", runtime.loaded))
        return real_stop_engine()

    def tracked_gpu_info():
        # Old engine must already be stopped before VRAM is inspected for the new model
        assert runtime.loaded is None
        assert runtime.proc is None
        events.append(("gpu_info", None))
        return {"name": "NVIDIA T4", "total_mib": 15360, "used_mib": 400}

    new_proc = Mock()
    new_proc.poll.return_value = None

    def tracked_popen(args, **kwargs):
        assert runtime.proc is None
        events.append(("popen", second.id))
        return new_proc

    monkeypatch.setattr(runtime, "_stop_engine", tracked_stop_engine)
    monkeypatch.setattr(runtime, "gpu_info", tracked_gpu_info)
    monkeypatch.setattr(
        runtime, "prepare_engine", lambda _task: runtime.root / "llama-server"
    )
    monkeypatch.setattr("server.runtime.subprocess.Popen", tracked_popen)
    runtime.http = Mock()
    runtime.http.get.return_value = StreamResponse(b"ok", status=200)

    task = wait_task(
        runtime,
        runtime.load("second-model", options={"context_length": 16384, "temperature": 0.3}),
    )

    assert task.state == "completed"
    old_proc.terminate.assert_called_once()
    old_proc.wait.assert_called()
    assert events[0] == ("stop_engine", spec.id)
    assert ("gpu_info", None) in events
    assert ("popen", "second-model") in events
    assert events.index(("stop_engine", spec.id)) < events.index(("gpu_info", None)) < events.index(("popen", "second-model"))
    assert any("正在卸载当前模型" in entry["message"] for entry in task.logs)
    assert runtime.loaded == "second-model"
    assert runtime.proc is new_proc
    assert runtime.active_options == {"context_length": 16384, "temperature": 0.3}

