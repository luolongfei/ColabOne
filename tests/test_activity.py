from pathlib import Path
from bridge.activity import Activity
from bridge.controller import Controller
from conftest import MemoryVault


def test_activity_is_bounded_and_redacts_credentials():
    log = Activity(limit=2)
    log.add("discarded")
    log.add(
        "https://example.com/?code=sensitive Bearer bearer-secret hf_secret_value 4/secret-authorization-code-long"
    )
    log.add("refresh_token=refresh-secret")
    items = log.snapshot()
    assert [item["id"] for item in items] == [2, 3]
    text = str(items)
    for secret in (
        "sensitive",
        "bearer-secret",
        "hf_secret_value",
        "secret-authorization",
        "refresh-secret",
    ):
        assert secret not in text
    log.clear()
    assert log.snapshot() == []


def test_activity_records_progress_and_errors_without_duplicate_polls(tmp_path):
    c = Controller(
        Path(__file__).resolve().parents[1],
        vault=MemoryVault(),
        config_dir=tmp_path,
        background=False,
    )
    c.update(operation={"title": "检查远端云盘授权", "running": True})
    c.update(operation={"title": "检查远端云盘授权", "running": True})
    task = {
        "id": "task",
        "kind": "download",
        "state": "running",
        "progress": 0.21,
        "message": "model.gguf",
    }
    c.update(runtime={"state": "ready", "tasks": [task]})
    c.update(runtime={"state": "ready", "tasks": [task | {"progress": 0.22}]})
    c.update(
        runtime={
            "state": "ready",
            "tasks": [task | {"state": "failed", "error": "下载校验失败"}],
        }
    )
    items = c.snapshot()["logs"]
    assert sum(item["message"] == "检查远端云盘授权" for item in items) == 1
    assert (
        sum("20%" in item["message"] and item["level"] == "info" for item in items) == 1
    )
    assert items[-1]["level"] == "error"
    assert "校验失败" in items[-1]["message"]
