"""Offline tests for workflow post-sync auto-release and Drive export IDs."""

from __future__ import annotations

import json
import time
from pathlib import Path
from unittest.mock import Mock

import pytest
from fastapi.testclient import TestClient

from server.app import create_colabone_app
from server.drive import ColabOneDrive
from server.runtime import ColabOneRuntime

ROOT = Path(__file__).resolve().parents[1]
PNG_BYTES = (
    b"\x89PNG\r\n\x1a\n"
    b"\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde"
    b"\x00\x00\x00\x0cIDATx\x9cc```\x00\x00\x00\x04\x00\x01\xf6\x178U"
    b"\x00\x00\x00\x00IEND\xaeB`\x82"
)


class FakeDrive:
    def __init__(self, connected: bool = True):
        self.connected = connected
        self.mode = "mount" if connected else "disabled"
        self.error = ""
        self.export_calls: list[dict] = []
        self.push_calls = 0
        self.pull_calls = 0
        self.unmount_calls = 0
        self.fail_export = False
        self.fail_push = False

    def available(self) -> bool:
        return self.connected

    def configured(self) -> bool:
        return self.connected

    def is_colab_drivefs_mounted(self) -> bool:
        return False

    def connect(self) -> dict:
        if not self.connected:
            raise RuntimeError("未检测到云盘凭据配置")
        return self.status()

    def pull_workspace(self, dest_dir: Path | None = None) -> bool:
        self.pull_calls += 1
        return self.connected

    def push_workspace(self) -> bool:
        self.push_calls += 1
        if not self.connected or self.fail_push:
            self.error = "Google 云盘同步失败"
            return False
        self.error = ""
        return True

    def export_output_file(
        self,
        src_path: Path,
        *,
        filename: str,
        output_id: str,
        extension: str,
        expected_sha256: str = "",
        expected_size: int | None = None,
    ) -> dict:
        if not self.connected:
            raise ValueError("当前实例尚未连接 Google 云盘，请先完成云盘授权与挂载")
        if self.fail_export:
            raise RuntimeError("模拟云盘写入超时")
        self.export_calls.append(
            {
                "src_path": str(src_path),
                "filename": filename,
                "output_id": output_id,
                "extension": extension,
            }
        )
        return {
            "ok": True,
            "already_exists": False,
            "filename": filename,
            "remote_folder": "ColabOne_Drive/outputs",
            "drive_path": f"My Drive/ColabOne_Drive/outputs/{filename}",
            "drive_file_id": f"gdrive-id-{output_id[:8]}",
            "drive_web_view_link": f"https://drive.google.com/file/d/gdrive-id-{output_id[:8]}/view",
            "size": Path(src_path).stat().st_size,
            "mode": self.mode,
        }

    def unmount(self) -> None:
        self.unmount_calls += 1
        self.connected = False
        self.mode = "disabled"

    def status(self, pending: int = 0) -> dict:
        return {
            "connected": self.connected,
            "configured": self.connected,
            "mode": self.mode if self.connected else "disabled",
            "mount_path": "/content/drive/MyDrive" if self.connected else "",
            "remote_folder": "ColabOne_Drive",
            "pending": pending,
            "error": self.error,
            "total_bytes": 100 * 1024**3 if self.connected else None,
            "free_bytes": 50 * 1024**3 if self.connected else None,
        }


def _make_runtime(tmp_path: Path, drive: FakeDrive) -> ColabOneRuntime:
    http = Mock()
    resp = Mock()
    resp.raise_for_status = Mock()
    http.post.return_value = resp
    rt = ColabOneRuntime(tmp_path / "remote", ROOT / "catalog", http=http, drive=drive)
    rt.drive_restored = True
    rt._workflow_auto_release_grace_seconds = 0.0
    rt.gpu_info = lambda: {"name": "Tesla T4", "total_mib": 15360, "used_mib": 256}
    return rt


def _wait_until(predicate, timeout: float = 5.0) -> bool:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return True
        time.sleep(0.02)
    return predicate()


def test_auto_release_requires_connected_drive(tmp_path):
    drive = FakeDrive(connected=False)
    rt = _make_runtime(tmp_path, drive)
    token = "colabone-auto-release-test-token-0123456789"
    headers = {"Authorization": f"Bearer {token}"}
    app = create_colabone_app(rt, token, background=False)

    with TestClient(app) as client:
        resp = client.post(
            "/api/v1/comfyui/auto-release", json={"enabled": True}, headers=headers
        )
        assert resp.status_code == 400
        assert "尚未连接 Google 云盘" in resp.json()["detail"]

        # Disabling when already idle succeeds cleanly.
        off_resp = client.post(
            "/api/v1/comfyui/auto-release", json={"enabled": False}, headers=headers
        )
        assert off_resp.status_code == 200
        assert off_resp.json()["enabled"] is False
        assert off_resp.json()["state"] == "idle"


def test_auto_release_exports_syncs_and_unassigns_after_success(tmp_path, monkeypatch):
    monkeypatch.setenv("TBE_RUNTIME_ADDR", "127.0.0.1:9999")
    drive = FakeDrive(connected=True)
    rt = _make_runtime(tmp_path, drive)
    wf_id = rt.comfy.store.list_workflows()[0]["id"]

    def fake_run_workflow(task, req):
        rec = rt.comfy.store.save_output(
            raw=PNG_BYTES,
            workflow_id=req.workflow_id,
            workflow_name="Test Workflow",
            filename="night_render.png",
            elapsed=12.5,
            prompt_id="prompt-1",
        )
        with task.lock:
            task.outputs.append(rec)
        return [rec]

    monkeypatch.setattr(rt.comfy, "run_workflow", fake_run_workflow)

    snap = rt.run_comfyui_workflow(
        {"workflow_id": wf_id, "auto_release_after_sync": True}
    )
    assert snap["kind"] == "comfyui_run"

    assert _wait_until(lambda: rt.workflow_auto_release_status()["state"] == "released")
    ar = rt.workflow_auto_release_status()
    assert ar["enabled"] is False
    assert ar["error"] == ""
    assert len(ar["exported"]) == 1
    assert ar["exported"][0]["filename"] == "night_render.png"
    assert ar["exported"][0]["drive_file_id"].startswith("gdrive-id-")
    assert len(drive.export_calls) == 1
    assert drive.push_calls >= 1
    assert drive.unmount_calls == 1
    assert rt.release_prepared is True
    assert rt.auto_releasing is True
    rt.http.post.assert_called_once_with(
        "http://127.0.0.1:9999/unassign", data=b"", timeout=10
    )


def test_auto_release_can_be_armed_mid_run_and_disarmed_mid_run(tmp_path, monkeypatch):
    monkeypatch.setenv("TBE_RUNTIME_ADDR", "127.0.0.1:9999")
    drive = FakeDrive(connected=True)
    rt = _make_runtime(tmp_path, drive)
    wf_id = rt.comfy.store.list_workflows()[0]["id"]

    gate = []

    def fake_run_workflow(task, req):
        while not gate:
            time.sleep(0.01)
        rec = rt.comfy.store.save_output(
            raw=PNG_BYTES,
            workflow_id=req.workflow_id,
            workflow_name="Mid Run WF",
            filename="mid_run.png",
            elapsed=5.0,
            prompt_id="prompt-2",
        )
        with task.lock:
            task.outputs.append(rec)
        return [rec]

    monkeypatch.setattr(rt.comfy, "run_workflow", fake_run_workflow)

    # Start WITHOUT auto-release armed, then arm mid-run.
    snap = rt.run_comfyui_workflow({"workflow_id": wf_id})
    armed = rt.set_comfyui_auto_release(True)
    assert armed["enabled"] is True
    assert armed["state"] == "armed"
    assert armed["task_id"] == snap["id"]

    # Disarm mid-run before completion -> must NOT release!
    disarmed = rt.set_comfyui_auto_release(False)
    assert disarmed["enabled"] is False
    assert disarmed["state"] == "idle"

    gate.append(True)
    assert _wait_until(lambda: rt.tasks[snap["id"]].state == "completed")
    time.sleep(0.1)
    assert rt.release_prepared is False
    assert drive.unmount_calls == 0
    assert rt.http.post.call_count == 0


def test_auto_release_aborts_on_workflow_failure_or_sync_failure(tmp_path, monkeypatch):
    monkeypatch.setenv("TBE_RUNTIME_ADDR", "127.0.0.1:9999")
    drive = FakeDrive(connected=True)
    rt = _make_runtime(tmp_path, drive)
    wf_id = rt.comfy.store.list_workflows()[0]["id"]

    # 1) Workflow fails -> keep instance, do NOT release.
    def fail_workflow(task, req):
        raise RuntimeError("CUDA out of memory")

    monkeypatch.setattr(rt.comfy, "run_workflow", fail_workflow)
    snap1 = rt.run_comfyui_workflow(
        {"workflow_id": wf_id, "auto_release_after_sync": True}
    )
    assert _wait_until(lambda: rt.tasks[snap1["id"]].state == "failed")
    ar1 = rt.workflow_auto_release_status()
    assert ar1["enabled"] is False
    assert ar1["state"] == "skipped"
    assert rt.release_prepared is False
    assert drive.unmount_calls == 0

    # 2) Workflow succeeds but Drive export fails -> keep instance, report error!
    drive.fail_export = True

    def ok_workflow(task, req):
        rec = rt.comfy.store.save_output(
            raw=PNG_BYTES,
            workflow_id=req.workflow_id,
            workflow_name="Fail Export WF",
            filename="export_fail.png",
            elapsed=3.0,
            prompt_id="prompt-3",
        )
        with task.lock:
            task.outputs.append(rec)
        return [rec]

    monkeypatch.setattr(rt.comfy, "run_workflow", ok_workflow)
    snap2 = rt.run_comfyui_workflow(
        {"workflow_id": wf_id, "auto_release_after_sync": True}
    )
    assert _wait_until(lambda: rt.tasks[snap2["id"]].state == "completed")
    ar2 = rt.workflow_auto_release_status()
    assert ar2["enabled"] is False
    assert ar2["state"] == "failed"
    assert "模拟云盘写入超时" in ar2["error"]
    assert rt.release_prepared is False
    assert drive.unmount_calls == 0
    assert rt.http.post.call_count == 0

    # 3) Export succeeds but workspace sync fails -> keep instance!
    drive.fail_export = False
    drive.fail_push = True
    snap3 = rt.run_comfyui_workflow(
        {"workflow_id": wf_id, "auto_release_after_sync": True}
    )
    assert _wait_until(lambda: rt.tasks[snap3["id"]].state == "completed")
    ar3 = rt.workflow_auto_release_status()
    assert ar3["enabled"] is False
    assert ar3["state"] == "failed"
    assert "工作区同步到 Google 云盘失败" in ar3["error"]
    assert rt.release_prepared is False
    assert drive.unmount_calls == 0
    assert rt.http.post.call_count == 0


def test_rclone_export_returns_drive_file_id(tmp_path, monkeypatch):
    drive = ColabOneDrive(tmp_path / "root", ROOT / "catalog")
    drive.root.mkdir(parents=True, exist_ok=True)
    drive.config_path.write_text("[colabone_gdrive]\ntype = drive\n", encoding="utf-8")
    drive.mode = "rclone_sync"

    src = tmp_path / "sample.png"
    src.write_bytes(PNG_BYTES)

    calls = []

    def fake_run(cmd, **kwargs):
        calls.append(cmd)
        proc = Mock()
        if "lsjson" in cmd:
            if len([c for c in calls if "lsjson" in c]) == 1:
                proc.returncode = 3
                proc.stdout = ""
                proc.stderr = "directory not found"
            else:
                proc.returncode = 0
                proc.stdout = json.dumps(
                    {"Name": "sample.png", "Size": len(PNG_BYTES), "ID": "1AbCdEfGhIjKlMn"}
                )
                proc.stderr = ""
        else:
            proc.returncode = 0
            proc.stdout = ""
            proc.stderr = ""
        return proc

    monkeypatch.setattr("server.drive.ensure_rclone_binary", lambda *a, **kw: Path("/usr/bin/rclone"))
    monkeypatch.setattr("server.drive.subprocess.run", fake_run)

    res = drive.export_output_file(
        src,
        filename="sample.png",
        output_id="a" * 32,
        extension="png",
    )
    assert res["ok"] is True
    assert res["drive_file_id"] == "1AbCdEfGhIjKlMn"
    assert res["drive_web_view_link"] == "https://drive.google.com/file/d/1AbCdEfGhIjKlMn/view"


def test_auto_release_can_be_cancelled_during_grace_window_before_quiescing(tmp_path, monkeypatch):
    monkeypatch.setenv("TBE_RUNTIME_ADDR", "127.0.0.1:9999")
    drive = FakeDrive(connected=True)
    rt = _make_runtime(tmp_path, drive)
    rt._workflow_auto_release_grace_seconds = 1.5
    wf_id = rt.comfy.store.list_workflows()[0]["id"]

    def ok_workflow(task, req):
        rec = rt.comfy.store.save_output(
            raw=PNG_BYTES,
            workflow_id=req.workflow_id,
            workflow_name="Grace Cancel WF",
            filename="grace.png",
            elapsed=2.0,
            prompt_id="prompt-grace",
        )
        with task.lock:
            task.outputs.append(rec)
        return [rec]

    monkeypatch.setattr(rt.comfy, "run_workflow", ok_workflow)
    snap = rt.run_comfyui_workflow(
        {"workflow_id": wf_id, "auto_release_after_sync": True}
    )
    assert _wait_until(
        lambda: rt.workflow_auto_release_status()["state"] == "releasing"
        and rt.tasks[snap["id"]].state == "completed"
    )
    assert rt.quiescing is False

    # User cancels auto-release during the grace window before quiescing starts!
    cancelled = rt.set_comfyui_auto_release(False)
    assert cancelled["enabled"] is False
    assert cancelled["state"] == "idle"
    time.sleep(0.25)
    assert rt.quiescing is False
    assert rt.release_prepared is False
    assert drive.unmount_calls == 0
    assert rt.http.post.call_count == 0


def test_stale_failed_auto_release_cleared_on_next_workflow_run(tmp_path, monkeypatch):
    drive = FakeDrive(connected=True)
    drive.fail_export = True
    rt = _make_runtime(tmp_path, drive)
    wf_id = rt.comfy.store.list_workflows()[0]["id"]

    def ok_workflow(task, req):
        rec = rt.comfy.store.save_output(
            raw=PNG_BYTES,
            workflow_id=req.workflow_id,
            workflow_name="Next Run WF",
            filename="next.png",
            elapsed=1.5,
            prompt_id="prompt-next",
        )
        with task.lock:
            task.outputs.append(rec)
        return [rec]

    monkeypatch.setattr(rt.comfy, "run_workflow", ok_workflow)
    snap1 = rt.run_comfyui_workflow(
        {"workflow_id": wf_id, "auto_release_after_sync": True}
    )
    assert _wait_until(lambda: rt.tasks[snap1["id"]].state == "completed")
    assert rt.workflow_auto_release_status()["state"] == "failed"

    # Run a second workflow WITHOUT auto-release -> stale failed alert must reset cleanly!
    snap2 = rt.run_comfyui_workflow({"workflow_id": wf_id})
    assert _wait_until(lambda: rt.tasks[snap2["id"]].state == "completed")
    ar2 = rt.workflow_auto_release_status()
    assert ar2["state"] == "idle"
    assert ar2["error"] == ""


def test_task_cancel_during_drive_export_aborts_auto_release(tmp_path, monkeypatch):
    monkeypatch.setenv("TBE_RUNTIME_ADDR", "127.0.0.1:9999")
    drive = FakeDrive(connected=True)
    rt = _make_runtime(tmp_path, drive)
    wf_id = rt.comfy.store.list_workflows()[0]["id"]

    task_holder = []

    def ok_two_outputs(task, req):
        task_holder.append(task)
        r1 = rt.comfy.store.save_output(
            raw=PNG_BYTES,
            workflow_id=req.workflow_id,
            workflow_name="Two Out",
            filename="first.png",
            elapsed=1.0,
            prompt_id="p1",
        )
        r2 = rt.comfy.store.save_output(
            raw=PNG_BYTES,
            workflow_id=req.workflow_id,
            workflow_name="Two Out",
            filename="second.png",
            elapsed=1.0,
            prompt_id="p2",
        )
        with task.lock:
            task.outputs.extend([r1, r2])
        return [r1, r2]

    orig_export = drive.export_output_file

    def export_and_cancel_midway(*args, **kwargs):
        res = orig_export(*args, **kwargs)
        # Cancel the task after the first output finishes exporting.
        task_holder[0].request_cancel()
        return res

    monkeypatch.setattr(rt.comfy, "run_workflow", ok_two_outputs)
    monkeypatch.setattr(drive, "export_output_file", export_and_cancel_midway)

    snap = rt.run_comfyui_workflow(
        {"workflow_id": wf_id, "auto_release_after_sync": True}
    )
    assert _wait_until(lambda: rt.tasks[snap["id"]].state == "cancelled")
    ar = rt.workflow_auto_release_status()
    assert ar["enabled"] is False
    assert ar["state"] == "skipped"
    assert len(drive.export_calls) == 1
    assert rt.release_prepared is False
    assert drive.unmount_calls == 0
    assert rt.http.post.call_count == 0


def test_submit_conflict_rolls_back_armed_auto_release(tmp_path, monkeypatch):
    from server.runtime import ColabOneConflictError

    drive = FakeDrive(connected=True)
    rt = _make_runtime(tmp_path, drive)
    wf_id = rt.comfy.store.list_workflows()[0]["id"]

    gate = []

    def block_until_gate(t):
        while not gate:
            time.sleep(0.01)

    blocker = rt.submit("download", "busy-model", block_until_gate)
    assert _wait_until(lambda: rt.tasks[blocker["id"]].state == "running")
    try:
        with pytest.raises(ColabOneConflictError):
            rt.run_comfyui_workflow(
                {"workflow_id": wf_id, "auto_release_after_sync": True}
            )
        ar = rt.workflow_auto_release_status()
        assert ar["enabled"] is False
        assert ar["state"] == "idle"
    finally:
        gate.append(True)
        _wait_until(lambda: rt.tasks[blocker["id"]].state == "completed")


def test_unassign_failure_keeps_releasing_state_instead_of_false_released(tmp_path, monkeypatch):
    import requests

    monkeypatch.setenv("TBE_RUNTIME_ADDR", "127.0.0.1:9999")
    drive = FakeDrive(connected=True)
    rt = _make_runtime(tmp_path, drive)
    rt.http.post.side_effect = requests.ConnectionError("transient proxy outage")
    monkeypatch.setattr("server.runtime.time.sleep", lambda *_a, **_kw: None)
    wf_id = rt.comfy.store.list_workflows()[0]["id"]

    def ok_workflow(task, req):
        rec = rt.comfy.store.save_output(
            raw=PNG_BYTES,
            workflow_id=req.workflow_id,
            workflow_name="Retry Unassign WF",
            filename="retry.png",
            elapsed=1.0,
            prompt_id="prompt-retry",
        )
        with task.lock:
            task.outputs.append(rec)
        return [rec]

    monkeypatch.setattr(rt.comfy, "run_workflow", ok_workflow)
    rt.run_comfyui_workflow({"workflow_id": wf_id, "auto_release_after_sync": True})
    assert _wait_until(lambda: rt.http.post.call_count == 3)
    assert _wait_until(
        lambda: "重试释放" in str(rt.workflow_auto_release_status().get("message") or "")
    )
    ar = rt.workflow_auto_release_status()
    assert ar["state"] == "releasing"
    assert ar["enabled"] is False
