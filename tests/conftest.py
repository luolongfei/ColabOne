import hashlib
import json
import time
import pytest
from common.models import ModelSpec
from server.runtime import ColabOneRuntime


class MemoryVault:
    def __init__(self):
        self.values = {}

    def get(self, name, default=None):
        return self.values.get(name, default)

    def set(self, name, value):
        self.values[name] = value

    def delete(self, name):
        self.values.pop(name, None)


@pytest.fixture
def spec():
    return ModelSpec(
        id="test-model",
        name="Test model",
        files=[
            {
                "repo": "org/model",
                "revision": "a" * 40,
                "path": "model.gguf",
                "size": 8,
                "sha256": hashlib.sha256(b"12345678").hexdigest(),
                "role": "model",
            }
        ],
    )


@pytest.fixture
def runtime(tmp_path, spec):
    catalog = tmp_path / "catalog"
    catalog.mkdir()
    (catalog / "presets.json").write_text(json.dumps([spec.model_dump()]))
    (catalog / "engine.json").write_text('{"tag":"test","assets":[]}')
    (catalog / "comfyui.json").write_text(
        json.dumps(
            {
                "repo": "https://github.com/Comfy-Org/ComfyUI.git",
                "tag": "test",
                "commit": "a" * 40,
                "port": 8188,
                "proxy_port": 8189,
                "templates": [],
            }
        )
    )
    return ColabOneRuntime(tmp_path / "remote", catalog)


def wait_task(runtime, result, timeout=3):
    task = runtime.tasks[result["id"]]
    deadline = time.monotonic() + timeout
    while task.state == "running" and time.monotonic() < deadline:
        time.sleep(0.01)
    assert task.state != "running", "task did not finish"
    return task
