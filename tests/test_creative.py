"""Creative workloads are tested with a fake engine and temporary remote storage."""

import base64
import hashlib
import io
import json
from pathlib import Path
from unittest.mock import Mock

import pytest
from PIL import Image
from fastapi.testclient import TestClient
from pydantic import ValidationError

from common.creative import CreativeRequest
from common.models import ModelSpec
from common.storage import DriveStore
from server.app import create_app
from server.creative import CreativeStore, generate, image_bytes
from server.runtime import Cancelled, Conflict, Task
from conftest import wait_task


def png():
    data = io.BytesIO()
    Image.new("RGB", (32, 32), "blue").save(data, format="PNG")
    return base64.b64encode(data.getvalue()).decode()


def creative_model(runtime, kind="image"):
    catalog = json.loads(
        (Path(__file__).parents[1] / "catalog/creative.json").read_text(
            encoding="utf-8"
        )
    )
    m = ModelSpec.model_validate(next(m for m in catalog if m["kind"] == kind))
    runtime.models[m.id] = m
    runtime.loaded = m.id
    return m


def request(runtime, kind="image", **kwargs):
    m = creative_model(runtime, kind)
    defaults = dict(
        model_id=m.id, kind=kind, prompt="蓝色的海边", width=512, height=512, seed=42
    )
    return CreativeRequest(**(defaults | kwargs))


def response(body):
    r = Mock()
    r.json.return_value = body
    return r


def test_catalog_has_complete_verified_component_sets():
    models = json.loads(
        (Path(__file__).parents[1] / "catalog/creative.json").read_text(
            encoding="utf-8"
        )
    )
    for value in models:
        model = ModelSpec.model_validate(value)
        assert len(model.files) == 3
        assert all(f.sha256 and len(f.revision) == 40 for f in model.files)
        with pytest.raises(ValidationError):
            ModelSpec.model_validate(value | {"files": value["files"][:2]})
        with pytest.raises(ValidationError):
            ModelSpec.model_validate(value | {"kind": "chat"})


@pytest.mark.parametrize(
    "values",
    [
        {"width": 513},
        {"prompt": "   "},
        {"kind": "video", "count": 2},
        {"kind": "video", "frames": 32},
        {"kind": "video", "width": 2048},
        {"reference_id": "../secret"},
        {"strength": float("nan")},
    ],
)
def test_generation_rejects_invalid_limits(values):
    with pytest.raises(ValidationError):
        CreativeRequest(**(dict(model_id="x", prompt="sea") | values))


def test_reference_normalizes_and_rejects_non_image():
    assert image_bytes("data:image/png;base64," + png()).startswith(b"\x89PNG")
    with pytest.raises(ValueError):
        image_bytes(base64.b64encode(b"not an image").decode())
    bmp = io.BytesIO()
    Image.new("RGB", (2, 2)).save(bmp, format="BMP")
    with pytest.raises(ValueError):
        image_bytes(base64.b64encode(bmp.getvalue()).decode())


def test_store_chunks_cloud_restore_and_trash(runtime, tmp_path):
    mount = tmp_path / "mount"
    (mount / "MyDrive").mkdir(parents=True)
    drive = DriveStore(mount, mounted=lambda: True, flush=lambda: None)
    store = CreativeStore(runtime.root, drive)
    item = store.save(png(), request(runtime), "Z Image", 42, 1.5)
    first = store.asset(item["id"], 0, 15)
    second = store.asset(item["id"], 15)
    raw = base64.b64decode(first["data"]) + base64.b64decode(second["data"])
    assert hashlib.sha256(raw).hexdigest() == item["sha256"]
    assert (
        CreativeStore(runtime.root, drive).listing()[0]["params"]["prompt"]
        == "蓝色的海边"
    )
    store.sync()
    assert store.listing()[0]["synced"] and drive.pending_flush
    new_root = tmp_path / "new-vm"
    new_root.mkdir()
    restored = CreativeStore(new_root, drive)
    restored.restore()
    assert restored.asset(item["id"])["sha256"] == item["sha256"]
    assert not (restored.root / (item["id"] + ".png")).exists()
    restored.delete(item["id"])
    assert not restored.listing() and not drive.media_list()
    assert (
        mount / "MyDrive/ColabOne/creations/.trash" / (item["id"] + ".png")
    ).exists()


def test_invalid_metadata_and_symlink_never_read(runtime):
    store = runtime.creative
    identity = "a" * 32
    (store.root / (identity + ".json")).write_text(
        json.dumps({"id": identity, "extension": "../../../secret"})
    )
    assert CreativeStore(runtime.root, runtime.drive).listing() == []
    with pytest.raises(ValueError):
        store.reference_data("../secret")


def test_completed_image_batch_uses_seed_and_waits_for_real_completion(
    runtime, monkeypatch
):
    body = request(runtime, count=2)
    runtime.http = Mock()
    runtime.http.post.return_value = response({"id": "job_test"})
    runtime.http.get.side_effect = [
        response(
            {
                "status": "generating",
                "progress": {"stage": "sampling", "completed": 9, "total": 9},
            }
        ),
        response({"status": "completed", "result": {"images": [{"b64_json": png()}]}}),
        response({"status": "completed", "result": {"images": [{"b64_json": png()}]}}),
    ]
    monkeypatch.setattr("server.creative.time.sleep", lambda _: None)
    task = wait_task(runtime, runtime.generate(body.model_dump()))
    assert task.state == "completed"
    assert {item["params"]["seed"] for item in runtime.creative.listing()} == {42, 43}
    payload = runtime.http.post.call_args.kwargs["json"]
    assert payload["sample_params"]["guidance"]["txt_cfg"] == 1
    assert payload["width"] == 512 and payload["seed"] == 43
    assert runtime.idle_status()["phase"] != "active"


def test_video_payload_and_container(runtime):
    body = request(runtime, "video", width=832, height=480, steps=20, frames=33)
    runtime.http = Mock()
    runtime.http.post.return_value = response({"id": "video_job"})
    raw = b"\x1a\x45\xdf\xa3test webm"
    runtime.http.get.return_value = response(
        {
            "status": "completed",
            "result": {"b64_json": base64.b64encode(raw).decode(), "frame_count": 33},
        }
    )
    task = Task("video", body.model_id)
    generate(runtime, task, body)
    url = runtime.http.post.call_args.args[0]
    data = runtime.http.post.call_args.kwargs["json"]
    assert url.endswith("/vid_gen") and "batch_count" not in data
    assert data["video_frames"] == 33 and data["output_format"] == "webm"
    assert runtime.creative.listing()[0]["mime"] == "video/webm"


def test_cancel_stops_engine_before_task_becomes_idle(runtime):
    body = request(runtime)
    task = Task("image", body.model_id)
    runtime.http = Mock()
    runtime.http.post.return_value = response({"id": "cancel_job"})

    def cancel(*a, **kw):
        task.request_cancel()
        return response({"status": "generating"})

    runtime.http.get.side_effect = cancel
    runtime.proc = Mock()
    runtime.proc.poll.return_value = None
    process = runtime.proc
    with pytest.raises(Cancelled):
        generate(runtime, task, body)
    process.terminate.assert_called_once()
    process.wait.assert_called_once()
    assert runtime.loaded is None
    assert runtime.http.post.call_args.args[0].endswith("/cancel")
    assert not runtime.creative.listing()


def test_failed_engine_keeps_error_and_stops_gpu(runtime):
    body = request(runtime)
    runtime.http = Mock()
    runtime.http.post.return_value = response({"id": "failed_job"})
    runtime.http.get.return_value = response(
        {"status": "failed", "error": {"message": "CUDA out of memory"}}
    )
    task = wait_task(runtime, runtime.generate(body.model_dump()))
    assert task.state == "failed" and "CUDA out of memory" in task.error
    assert runtime.loaded is None and not runtime.creative.listing()


def test_chat_and_creative_models_cannot_cross_modes(runtime):
    body = request(runtime)
    with pytest.raises(Conflict):
        runtime.chat("", "hello")
    with pytest.raises(Conflict):
        runtime.generate(body.model_dump() | {"kind": "video"})


def test_run_options_apply_to_copy_without_mutating_manifest(
    runtime, spec, monkeypatch
):
    monkeypatch.setattr(runtime, "installed", lambda _: True)
    captured = []
    monkeypatch.setattr(runtime, "_load", lambda task, model: captured.append(model))
    task = wait_task(
        runtime, runtime.load(spec.id, {"context_length": 16384, "temperature": 0.3})
    )
    assert task.state == "completed"
    assert captured[0].context_length == 16384 and captured[0].temperature == 0.3
    assert runtime.models[spec.id] == spec


def test_creative_api_requires_auth_and_validates_chunk_ranges(runtime):
    token = "creative-test-token-at-least-32-chars"
    auth = {"Authorization": "Bearer " + token}
    item = runtime.creative.save(png(), request(runtime), "Image", 42, 1)
    with TestClient(create_app(runtime, token)) as client:
        for path in ["creative/references", "creative/generate"]:
            assert client.post("/api/v1/" + path, json={}).status_code == 401
        path = "/api/v1/creations/" + item["id"] + "/asset"
        assert client.get(path).status_code == 401
        assert client.get(path + "?offset=-1", headers=auth).status_code == 422
        assert client.get(path + "?length=9999999", headers=auth).status_code == 422
        assert client.get(path, headers=auth).json()["sha256"] == item["sha256"]
        assert client.delete("/api/v1/creations/" + item["id"]).status_code == 401


def test_lost_submission_reply_stops_possible_orphan_job(runtime):
    import requests

    body = request(runtime)
    runtime.http = Mock()
    runtime.http.post.side_effect = requests.Timeout("reply lost")
    runtime.proc = Mock()
    process = runtime.proc
    task = wait_task(runtime, runtime.generate(body.model_dump()))
    assert task.state == "failed"
    process.terminate.assert_called_once()
    assert runtime.loaded is None and not runtime.creative.listing()


def test_load_error_fails_immediately_and_stops_process(runtime, monkeypatch):
    from server.creative import load

    model = creative_model(runtime)
    runtime.loaded = None
    monkeypatch.setattr(
        "server.creative.prepare_engine", lambda *_: runtime.root / "sd-server"
    )
    process = Mock()
    process.poll.return_value = None
    monkeypatch.setattr("server.creative.subprocess.Popen", lambda *a, **k: process)
    runtime.http = Mock()
    runtime.http.get.return_value = response(
        {"state": "error", "error": "invalid weights"}
    )
    with pytest.raises(RuntimeError, match="invalid weights"):
        load(runtime, Task("load"), model)
    assert runtime.loaded is None
    process.terminate.assert_called_once()
