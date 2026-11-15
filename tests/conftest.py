import hashlib
import json
import time
import pytest
from common.models import ModelSpec
from server.runtime import Runtime


class MemoryVault:
    def __init__(self):
        self.values = {}

    def get(self, name, default=None):
        return self.values.get(name, default)

    def set(self, name, value):
        self.values[name] = value

    def delete(self, name):
        self.values.pop(name, None)


class MemoryDrive:
    def __init__(self):
        self.token = "test"
        self.data = {}
        self.fail = False

    def put(self, name, data):
        if self.fail:
            raise OSError("offline")
        self.data[name] = json.loads(json.dumps(data))

    def get(self, name, default=None):
        return self.data.get(name, default)

    def conversations(self):
        return [v for k, v in self.data.items() if k.startswith("conversation-")]

    def available(self):
        return True

    def flush(self):
        if self.fail:
            raise OSError("flush failed")


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
    return Runtime(tmp_path / "remote", catalog, drive=MemoryDrive())


def wait_task(runtime, result, timeout=3):
    task = runtime.tasks[result["id"]]
    deadline = time.monotonic() + timeout
    while task.state == "running" and time.monotonic() < deadline:
        time.sleep(0.01)
    assert task.state != "running", "task did not finish"
    return task
