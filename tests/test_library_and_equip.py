import hashlib
import json
import time
from pathlib import Path
from unittest.mock import Mock, call

import pytest
from fastapi.testclient import TestClient

from bridge.controller import ColabOneController
from common.files import atomic_json
from common.models import ModelSpec, parse_hf_input, resolve_import
from conftest import MemoryVault, wait_task
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


def wait_controller_idle(controller: ColabOneController, timeout: float = 3.0):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if not controller.operation_lock.locked() and controller.snapshot()["operation"] is None:
            return
        time.sleep(0.01)
    raise AssertionError("controller async operation did not finish in time")


def make_controller(config_dir: Path, monkeypatch, session=None) -> tuple[ColabOneController, Mock]:
    hf_session = session or make_hf_session()
    monkeypatch.setattr(
        "bridge.controller.resolve_import",
        lambda repo, filename, revision="main", token="", custom_name="": resolve_import(
            repo,
            filename,
            revision=revision,
            token=token,
            session=hf_session,
            custom_name=custom_name,
        ),
    )
    c = ColabOneController(ROOT, vault=MemoryVault(), config_dir=config_dir, background=False)
    c.remote = Mock()
    return c, hf_session


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
# 2. Offline local library persistence in Controller (library.json)
# ---------------------------------------------------------------------------


def test_import_model_while_stopped_persists_and_reloads(tmp_path, monkeypatch):
    cfg_dir = tmp_path / "config"
    c, hf_session = make_controller(cfg_dir, monkeypatch)

    assert c.snapshot()["runtime"]["state"] == "stopped"
    spec = c.import_model(
        {
            "repo": "https://huggingface.co/owner/repo/resolve/main/qwen-q4_k_m.gguf",
            "name": "Saved Offline Qwen",
        }
    )

    c.remote.assert_not_called()
    hf_session.get.assert_called_once()

    library_path = cfg_dir / "library.json"
    assert library_path.is_file()
    saved_lib = json.loads(library_path.read_text(encoding="utf-8"))
    assert spec.id in saved_lib["common_ids"]
    assert [m["id"] for m in saved_lib["custom_models"]] == [spec.id]

    snap = c.snapshot()
    entry = next(m for m in snap["models"] if m["id"] == spec.id)
    assert entry["name"] == "Saved Offline Qwen"
    assert entry["repo"] == "owner/repo"
    assert entry["common"] is True
    assert entry["custom"] is True
    assert entry["downloaded"] is False
    assert entry["loaded"] is False

    # Survives instantiating a brand-new Controller pointing at the same config_dir
    c_reloaded = ColabOneController(ROOT, vault=MemoryVault(), config_dir=cfg_dir, background=False)
    snap_reloaded = c_reloaded.snapshot()
    reloaded_entry = next(m for m in snap_reloaded["models"] if m["id"] == spec.id)
    assert reloaded_entry["name"] == "Saved Offline Qwen"
    assert reloaded_entry["repo"] == "owner/repo"
    assert reloaded_entry["common"] is True
    assert reloaded_entry["custom"] is True


def test_default_common_repos_seeded_once_editable_and_migrated_from_legacy(tmp_path, monkeypatch):
    expected_defaults = [
        "huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
        "huihui-ai/Huihui-DeepSeek-V4.1-Flash-1017-abliterated-GGUF",
        "JonathanColetti/Qwen3.8-27B-Uncensored",
    ]
    cfg_dir = tmp_path / "fresh"
    c, _ = make_controller(cfg_dir, monkeypatch)
    assert [r["repo"] for r in c.snapshot()["common_repos"]] == expected_defaults
    assert c.snapshot()["settings"]["download_threads"] == 16

    # User can manually remove a default common repo and removal persists across reload
    assert c.command("common_repo.remove", {"repo": expected_defaults[1]}) == {"ok": True}
    c_reloaded = ColabOneController(ROOT, vault=MemoryVault(), config_dir=cfg_dir, background=False)
    assert [r["repo"] for r in c_reloaded.snapshot()["common_repos"]] == [
        expected_defaults[0],
        expected_defaults[2],
    ]

    # Legacy library.json (starred_ids + bookmarks) and legacy settings.json (download_threads=8) migrate cleanly
    legacy_dir = tmp_path / "legacy"
    legacy_dir.mkdir()
    (legacy_dir / "library.json").write_text(
        json.dumps(
            {
                "starred_ids": ["qwen25-05b", "gemma4-31b"],
                "bookmarks": [{"repo": "org/legacy-repo", "name": "Legacy Repo"}],
            }
        ),
        encoding="utf-8",
    )
    (legacy_dir / "settings.json").write_text(
        json.dumps({"download_threads": 8}),
        encoding="utf-8",
    )
    c_legacy = ColabOneController(ROOT, vault=MemoryVault(), config_dir=legacy_dir, background=False)
    snap = c_legacy.snapshot()
    assert snap["settings"]["download_threads"] == 16
    assert all(m["id"] != "qwen25-05b" for m in snap["models"])
    assert "qwen25-05b" not in c_legacy.common_ids
    assert next(m for m in snap["models"] if m["id"] == "gemma4-31b")["common"] is True
    repos = [r["repo"] for r in snap["common_repos"]]
    assert repos == ["org/legacy-repo", *expected_defaults]
    with pytest.raises(ValueError, match="16 或 32"):
        c_legacy.command("settings.save", {"download_threads": 8})
    assert c_legacy.command("settings.save", {"download_threads": 32}) == {"ok": True}
    assert c_legacy.snapshot()["settings"]["download_threads"] == 32


def test_toggle_common_on_preset_and_custom_models(tmp_path, monkeypatch):
    cfg_dir = tmp_path / "config"
    c, _ = make_controller(cfg_dir, monkeypatch)

    preset_id = c.presets()[0]["id"]
    preset_before = next(m for m in c.snapshot()["models"] if m["id"] == preset_id)
    assert preset_before["common"] is False
    assert preset_before["custom"] is False

    # Mark preset as common via command("model.common")
    assert c.command("model.common", {"id": preset_id}) == {"ok": True, "common": True}
    preset_common = next(m for m in c.snapshot()["models"] if m["id"] == preset_id)
    assert preset_common["common"] is True
    assert preset_common["custom"] is False
    assert preset_id not in c.custom_models

    # Toggle preset back off
    assert c.command("model.common", {"id": preset_id}) == {"ok": True, "common": False}
    assert next(m for m in c.snapshot()["models"] if m["id"] == preset_id)["common"] is False

    # Custom model common toggling and persistence
    spec = c.import_model({"repo": "owner/repo", "filename": "qwen-q4_k_m.gguf"})
    assert next(m for m in c.snapshot()["models"] if m["id"] == spec.id)["common"] is True

    assert c.command("model.common", {"id": spec.id, "common": False}) == {
        "ok": True,
        "common": False,
    }
    non_common_custom = next(m for m in c.snapshot()["models"] if m["id"] == spec.id)
    assert non_common_custom["common"] is False
    assert non_common_custom["custom"] is True

    assert c.set_common_model(spec.id, common=True) == {"ok": True, "common": True}
    saved_lib = json.loads((cfg_dir / "library.json").read_text(encoding="utf-8"))
    assert spec.id in saved_lib["common_ids"]

    with pytest.raises(ValueError):
        c.set_common_model("../invalid")


def test_common_repos_save_toggle_url_parsing_and_remove(tmp_path, monkeypatch):
    cfg_dir = tmp_path / "config"
    c, _ = make_controller(cfg_dir, monkeypatch)
    initial_count = len(c.snapshot()["common_repos"])

    res = c.command(
        "common_repo.save",
        {
            "repo": "https://huggingface.co/Qwen/Qwen2.5-7B-GGUF/blob/main/qwen2.5-7b-q4_k_m.gguf",
            "note": "7B daily driver",
            "downloads": 1234,
            "likes": 56,
        },
    )
    assert res == {"ok": True, "common": True, "repo": "Qwen/Qwen2.5-7B-GGUF"}
    common_repos = c.snapshot()["common_repos"]
    assert len(common_repos) == initial_count + 1
    assert common_repos[0]["repo"] == "Qwen/Qwen2.5-7B-GGUF"
    assert common_repos[0]["filename"] == "qwen2.5-7b-q4_k_m.gguf"
    assert common_repos[0]["note"] == "7B daily driver"
    assert common_repos[0]["downloads"] == 1234
    assert common_repos[0]["likes"] == 56

    # toggle=True removes an already-saved common repo (case-insensitive URL input)
    toggled_off = c.command(
        "common_repo.save",
        {"repo": "https://huggingface.co/qwen/qwen2.5-7b-gguf", "toggle": True},
    )
    assert toggled_off == {"ok": True, "common": False, "repo": "qwen/qwen2.5-7b-gguf"}
    assert all(r["repo"] != "Qwen/Qwen2.5-7B-GGUF" for r in c.snapshot()["common_repos"])

    # toggle=True when absent adds it back
    toggled_on = c.command(
        "common_repo.save",
        {"repo": "org/another-gguf", "name": "Another GGUF", "toggle": True},
    )
    assert toggled_on == {"ok": True, "common": True, "repo": "org/another-gguf"}

    # Survives reload before explicit removal
    c2 = ColabOneController(ROOT, vault=MemoryVault(), config_dir=cfg_dir, background=False)
    assert c2.snapshot()["common_repos"][0]["repo"] == "org/another-gguf"

    assert c2.command("common_repo.remove", {"repo": "ORG/ANOTHER-GGUF"}) == {"ok": True}
    assert all(r["repo"] != "org/another-gguf" for r in c2.snapshot()["common_repos"])
    saved_repos = json.loads((cfg_dir / "library.json").read_text(encoding="utf-8"))["common_repos"]
    assert all(r["repo"] != "org/another-gguf" for r in saved_repos)

    with pytest.raises(ValueError):
        c.save_common_repo({"repo": "https://evil.example/a/b"})


def test_remove_local_model_guards_downloaded_or_loaded_and_removes_cleanly(tmp_path, monkeypatch):
    cfg_dir = tmp_path / "config"
    c, _ = make_controller(cfg_dir, monkeypatch)
    spec = c.import_model({"repo": "owner/repo", "filename": "qwen-q4_k_m.gguf"})

    # Reject removal while downloaded on remote
    c.data["models"] = [
        spec.model_dump() | {"downloaded": True, "loaded": False, "size": 4096}
    ]
    with pytest.raises(RuntimeError, match="远端"):
        c.command("model.remove_local", {"id": spec.id})
    assert spec.id in c.custom_models

    # Reject removal while loaded on remote
    c.data["models"] = [
        spec.model_dump() | {"downloaded": False, "loaded": True, "size": 4096}
    ]
    with pytest.raises(RuntimeError, match="远端"):
        c.command("model.remove_local", {"id": spec.id})
    assert spec.id in c.custom_models

    # Succeeds when neither downloaded nor loaded
    c.data["models"] = [
        spec.model_dump() | {"downloaded": False, "loaded": False, "size": 4096}
    ]
    assert c.command("model.remove_local", {"id": spec.id}) == {"ok": True}
    assert spec.id not in c.custom_models
    assert spec.id not in c.common_ids
    assert all(m["id"] != spec.id for m in c.snapshot()["models"])

    saved_lib = json.loads((cfg_dir / "library.json").read_text(encoding="utf-8"))
    assert saved_lib["custom_models"] == []
    assert spec.id not in saved_lib["common_ids"]


def test_hub_install_without_download_saves_locally_without_remote_rpc(tmp_path, monkeypatch):
    cfg_dir = tmp_path / "config"
    c, hf_session = make_controller(cfg_dir, monkeypatch)

    result = c.command(
        "hub.install",
        {
            "repo": "https://huggingface.co/owner/repo/blob/main/qwen-q4_k_m.gguf",
            "name": "Hub Saved Only",
            "download": False,
        },
    )
    assert result == {"ok": True}
    wait_controller_idle(c)

    snap = c.snapshot()
    assert snap["error"] == ""
    c.remote.assert_not_called()
    hf_session.get.assert_called_once()

    custom_entries = [m for m in snap["models"] if m["name"] == "Hub Saved Only"]
    assert len(custom_entries) == 1
    assert custom_entries[0]["repo"] == "owner/repo"
    assert custom_entries[0]["common"] is True
    assert custom_entries[0]["custom"] is True
    assert len(json.loads((cfg_dir / "library.json").read_text(encoding="utf-8"))["custom_models"]) == 1


def test_ensure_remote_model_syncs_spec_before_download_or_load(tmp_path, monkeypatch):
    cfg_dir = tmp_path / "config"
    c, _ = make_controller(cfg_dir, monkeypatch)
    c.vault.set("hf", "hf_tok")

    # Locally save custom model while stopped
    spec = c.import_model({"repo": "owner/repo", "filename": "qwen-q4_k_m.gguf"})
    dumped = spec.model_dump()

    # Simulate a newly connected GPU whose remote models list only has built-in presets
    c.data["runtime"] = {"state": "ready", "id": "ep-fresh"}
    c.data["models"] = c.presets()
    c.remote = Mock(return_value={"id": "task-1", "state": "running"})

    c.command(
        "model.download",
        {
            "id": spec.id,
            "load": True,
            "context_length": 16384,
            "temperature": 0.3,
        },
    )
    assert c.remote.call_args_list == [
        call("POST", "models", dumped),
        call(
            "POST",
            f"models/{spec.id}/download",
            {
                "hf_token": "hf_tok",
                "threads": 16,
                "load": True,
                "context_length": 16384,
                "temperature": 0.3,
            },
        ),
    ]

    # Reset and test model.load when remote still lacks the custom model in self.data["models"]
    c.remote.reset_mock()
    c.command(
        "model.load",
        {"id": spec.id, "context_length": 16384, "temperature": 0.3},
    )
    assert c.remote.call_args_list == [
        call("POST", "models", dumped),
        call(
            "POST",
            f"models/{spec.id}/load",
            {"context_length": 16384, "temperature": 0.3},
        ),
    ]

    # Once remote snapshot includes the custom model, _ensure_remote_model skips POST models
    c.data["models"] = c.presets() + [dumped | {"downloaded": True, "loaded": False, "size": 4096}]
    c.remote.reset_mock()
    c.command("model.load", {"id": spec.id, "context_length": 16384})
    assert c.remote.call_args_list == [
        call("POST", f"models/{spec.id}/load", {"context_length": 16384})
    ]


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


def test_desktop_shortcuts_create_and_remove_in_isolated_sandbox(tmp_path):
    ctl = ColabOneController(ROOT, vault=MemoryVault(), config_dir=tmp_path, background=False)
    snap0 = ctl.snapshot()
    assert snap0["setup"]["start_menu_shortcut"] is False
    assert snap0["setup"]["desktop_shortcut"] is False

    res1 = ctl.command("desktop.shortcuts", {"target": "start_menu"})
    assert res1 == {"ok": True, "start_menu": True, "desktop": False}
    assert ctl.snapshot()["setup"]["start_menu_shortcut"] is True

    res2 = ctl.command("desktop.shortcuts", {"target": "desktop"})
    assert res2 == {"ok": True, "start_menu": True, "desktop": True}
    assert ctl.snapshot()["setup"]["desktop_shortcut"] is True

    res3 = ctl.command("desktop.shortcuts", {"target": "both", "remove": True})
    assert res3 == {"ok": True, "start_menu": False, "desktop": False}
    assert ctl.snapshot()["setup"]["start_menu_shortcut"] is False

    with pytest.raises(ValueError, match="不合法"):
        ctl.command("desktop.shortcuts", {"target": "taskbar-hack"})

