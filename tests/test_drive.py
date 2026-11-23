"""Offline tests for ColabOne headless rclone Google Drive mount & persistence."""

import hashlib
import io
import json
import os
import zipfile
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock

import pytest
from fastapi.testclient import TestClient
from PIL import Image

from server.app import create_colabone_app
from server.drive import (
    ColabOneDrive,
    COLABONE_DRIVE_FOLDER,
    COLABONE_DRIVE_REMOTE,
    COLABONE_DRIVE_SPEC,
    ensure_rclone_binary,
    stage_rclone_config,
)
from server.runtime import ColabOneRuntime


def test_stage_rclone_config_enforces_0600_and_removes_staging(tmp_path, monkeypatch):
    chmod_calls = []
    real_chmod = os.chmod

    def spy_chmod(path, mode, *args, **kwargs):
        chmod_calls.append((Path(path), mode))
        return real_chmod(path, mode, *args, **kwargs)

    monkeypatch.setattr(os, "chmod", spy_chmod)
    stage = tmp_path / "staged.conf"
    stage.write_text(f"[{COLABONE_DRIVE_REMOTE}]\ntype = drive\n", encoding="utf-8")
    data_root = tmp_path / "colabone-data"
    dst = stage_rclone_config(stage, data_root)
    assert dst == data_root / ".rclone.conf"
    assert not stage.exists()
    assert any(mode == 0o600 for _, mode in chmod_calls)
    if os.name != "nt":
        assert (dst.stat().st_mode & 0o777) == 0o600
    assert f"[{COLABONE_DRIVE_REMOTE}]" in dst.read_text(encoding="utf-8")


def test_ensure_rclone_binary_verifies_sha256_and_extracts(tmp_path, monkeypatch):
    monkeypatch.setattr("server.drive.shutil.which", lambda _: None)
    monkeypatch.setattr("server.drive.platform.system", lambda: "Linux")
    monkeypatch.setattr("server.drive.platform.machine", lambda: "x86_64")

    chmod_calls = []
    real_chmod = os.chmod

    def spy_chmod(path, mode, *args, **kwargs):
        chmod_calls.append((Path(path), mode))
        return real_chmod(path, mode, *args, **kwargs)

    monkeypatch.setattr(os, "chmod", spy_chmod)

    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as zf:
        zf.writestr("rclone-v1.76.0-linux-amd64/rclone", b"#!/bin/sh\necho ok\n")
    archive_bytes = buf.getvalue()
    sha = hashlib.sha256(archive_bytes).hexdigest()

    catalog = tmp_path / "catalog"
    catalog.mkdir()
    (catalog / "rclone.json").write_text(
        json.dumps({"tag": "v1.76.0", "sha256": {"amd64": sha, "arm64": sha}}),
        encoding="utf-8",
    )

    resp = Mock()
    resp.__enter__ = lambda self: self
    resp.__exit__ = lambda *_: False
    resp.raise_for_status = Mock()
    resp.iter_content = lambda _sz: [archive_bytes]
    http = Mock()
    http.get.return_value = resp

    binary = ensure_rclone_binary(tmp_path / "data", catalog, http=http)
    assert binary.is_file()
    assert any(mode == 0o700 for _, mode in chmod_calls)
    if os.name != "nt":
        assert (binary.stat().st_mode & 0o777) == 0o700

    # Wrong SHA-256 must be rejected.
    bad_catalog = tmp_path / "bad-catalog"
    bad_catalog.mkdir()
    (bad_catalog / "rclone.json").write_text(
        json.dumps({"tag": "v1.76.0", "sha256": {"amd64": "0" * 64, "arm64": "0" * 64}}),
        encoding="utf-8",
    )
    with pytest.raises(RuntimeError, match="无法下载并通过校验"):
        ensure_rclone_binary(tmp_path / "data-bad", bad_catalog, http=http)


class FakeCloudDrive(ColabOneDrive):
    """In-memory directory simulation of ColabOne_Drive/workspace for offline tests."""

    def __init__(self, root: Path, catalog: Path, cloud_dir: Path):
        super().__init__(root, catalog, mount_root=root / "mnt_drive")
        self.cloud_dir = cloud_dir
        self.cloud_dir.mkdir(parents=True, exist_ok=True)
        self.fail_push = False
        self.unmounted = False

    def configured(self) -> bool:
        return True

    def connect(self) -> dict:
        self.mode = "mount"
        self.error = ""
        return self.status()

    def available(self) -> bool:
        return self.mode != "disabled"

    def is_fuse_mounted(self) -> bool:
        return self.mode == "mount"

    def pull_workspace(self, dest_dir: Path | None = None) -> bool:
        import shutil

        target = Path(dest_dir) if dest_dir else self.root
        target.mkdir(parents=True, exist_ok=True)
        for item in self.cloud_dir.iterdir():
            if item.name == ".trash":
                continue
            dst = target / item.name
            if item.is_dir():
                shutil.copytree(item, dst, dirs_exist_ok=True)
            else:
                shutil.copy2(item, dst)
        return True

    def push_workspace(self) -> bool:
        import shutil

        if self.fail_push:
            self.error = "Google 云盘同步失败"
            return False
        for name in ("models.json",):
            src = self.root / name
            if src.is_file():
                shutil.copy2(src, self.cloud_dir / name)
        for folder in ("workflows", "comfyui-outputs"):
            src_dir = self.root / folder
            if src_dir.is_dir():
                dst_dir = self.cloud_dir / folder
                dst_dir.mkdir(exist_ok=True)
                for item in src_dir.iterdir():
                    if item.name == ".trash" or item.is_dir():
                        continue
                    shutil.copy2(item, dst_dir / item.name)
        self.error = ""
        return True

    def unmount(self):
        self.unmounted = True


def _sample_png_bytes() -> bytes:
    buf = io.BytesIO()
    Image.new("RGB", (32, 32), (30, 90, 200)).save(buf, format="PNG")
    return buf.getvalue()


def test_cross_instance_headless_restore_and_sync(tmp_path, spec):
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
    cloud = tmp_path / "ColabOne_Drive"

    # Instance 1 saves a custom model, a ComfyUI workflow, and a ComfyUI output, then releases cleanly.
    rt1 = ColabOneRuntime(
        tmp_path / "vm1", catalog, drive=FakeCloudDrive(tmp_path / "vm1", catalog, cloud)
    )
    rt1.connect_drive()
    imported = spec.model_copy(update={"id": "custom-gguf", "name": "Custom GGUF"})
    rt1.add_model(imported.model_dump())
    wf = rt1.comfy.store.save_workflow(
        {
            "id": "wf-cloud-sync",
            "name": "跨实例工作流",
            "description": "云盘持久化测试",
            "workflow": {
                "1": {
                    "class_type": "CLIPTextEncode",
                    "inputs": {"text": "你好 ColabOne ComfyUI"},
                }
            },
        }
    )
    output_rec = rt1.comfy.store.save_output(
        raw=_sample_png_bytes(),
        workflow_id=wf["id"],
        workflow_name=wf["name"],
        filename="result_0001.png",
        elapsed=1.2,
        prompt_id="prompt-1",
    )
    assert rt1.prepare_release()["ok"]
    assert rt1.drive.unmounted

    # Instance 2 starts on a fresh VM and automatically restores everything with zero browser prompts.
    token = "test-control-token-at-least-32-chars"
    rt2 = ColabOneRuntime(
        tmp_path / "vm2", catalog, drive=FakeCloudDrive(tmp_path / "vm2", catalog, cloud)
    )
    with TestClient(create_colabone_app(rt2, token)) as client:
        res = client.post(
            "/api/v1/drive/connect", headers={"Authorization": "Bearer " + token}
        )
        assert res.status_code == 200
        assert res.json()["connected"] is True
        assert res.json()["remote_folder"] == COLABONE_DRIVE_FOLDER

    assert "custom-gguf" in rt2.models
    restored_wf = rt2.comfy.store.get_workflow("wf-cloud-sync")
    assert restored_wf["name"] == "跨实例工作流"
    assert (
        restored_wf["workflow"]["1"]["inputs"]["text"] == "你好 ColabOne ComfyUI"
    )
    outputs = rt2.comfy.store.list_outputs()
    assert len(outputs) == 1 and outputs[0]["id"] == output_rec["id"]
    assert not rt2.installed(imported)


def test_rclone_workspace_sync_targets_workflows_and_comfy_outputs(tmp_path, monkeypatch):
    catalog = tmp_path / "catalog"
    catalog.mkdir()
    root = tmp_path / "data"
    root.mkdir()
    (root / ".rclone.conf").write_text(f"[{COLABONE_DRIVE_REMOTE}]\ntype = drive\n", encoding="utf-8")

    drive = ColabOneDrive(root, catalog, mount_root=root / "mnt")
    drive.mode = "rclone_sync"

    fake_bin = tmp_path / "rclone"
    monkeypatch.setattr("server.drive.ensure_rclone_binary", lambda *a, **k: fake_bin)
    calls = []

    def fake_run(cmd, **kwargs):
        calls.append(list(cmd))
        return SimpleNamespace(returncode=0, stdout="", stderr="")

    monkeypatch.setattr("server.drive.subprocess.run", fake_run)

    assert drive.pull_workspace(tmp_path / "pull") is True
    assert drive.push_workspace() is True
    drive.remove_remote_workflow("wf-demo")
    oid = "a" * 32
    drive.remove_remote_comfy_output(oid, "mp4")

    pull_cmd, push_cmd = calls[0], calls[1]
    for cmd in (pull_cmd, push_cmd):
        assert "+ /models.json" in cmd
        assert "+ /workflows/**" in cmd
        assert "+ /comfyui-outputs/**" in cmd
        assert "+ /conversation-*.json" not in cmd
        assert "+ /creations/**" not in cmd
        assert "- **/.trash/**" in cmd
        assert "- **/.save-*" in cmd
        assert "- **" in cmd
        assert cmd.index("- **/.trash/**") < cmd.index("+ /workflows/**")
        assert cmd.index("+ /comfyui-outputs/**") < cmd.index("- **")

    assert calls[2][-3:] == [
        "moveto",
        f"{COLABONE_DRIVE_SPEC}/workspace/workflows/wf-demo.json",
        f"{COLABONE_DRIVE_SPEC}/workspace/workflows/.trash/wf-demo.json",
    ]
    assert calls[3][-3:] == [
        "moveto",
        f"{COLABONE_DRIVE_SPEC}/workspace/comfyui-outputs/{oid}.mp4",
        f"{COLABONE_DRIVE_SPEC}/workspace/comfyui-outputs/.trash/{oid}.mp4",
    ]
    assert calls[4][-3:] == [
        "moveto",
        f"{COLABONE_DRIVE_SPEC}/workspace/comfyui-outputs/{oid}.json",
        f"{COLABONE_DRIVE_SPEC}/workspace/comfyui-outputs/.trash/{oid}.json",
    ]


def test_rclone_quota_project_directive_and_error_formatting(tmp_path):
    from server.drive import format_rclone_error, parse_rclone_quota_project

    conf = (
        "# colabone_quota_project = colabone-drive-abcdef1234\n"
        f"[{COLABONE_DRIVE_REMOTE}]\n"
        "type = drive\n"
        "scope = drive.file\n"
    )
    root = tmp_path / "data"
    root.mkdir()
    rc_path = root / ".rclone.conf"
    rc_path.write_text(conf, encoding="utf-8")
    assert parse_rclone_quota_project(rc_path) == "colabone-drive-abcdef1234"

    drive = ColabOneDrive(root, tmp_path / "catalog", mount_root=root / "mnt")
    cmd = drive._rclone_cmd(Path("/usr/bin/rclone"), "mkdir", COLABONE_DRIVE_SPEC)
    assert "--header" in cmd
    assert "X-Goog-User-Project: colabone-drive-abcdef1234" in cmd
    assert cmd[-2:] == ["mkdir", COLABONE_DRIVE_SPEC]

    multiline_403 = (
        "2026/11/21 06:57:54 CRITICAL: Failed to create file system: googleapi: Error 403: "
        "Your application is authenticating by using local Application Default Credentials. "
        "The drive.googleapis.com API requires a quota project.\n"
        '  "reason": "SERVICE_DISABLED"\n'
        ", accessNotConfigured\n"
    )
    formatted = format_rclone_error(multiline_403)
    assert "配额项目" in formatted
    assert formatted != "Google 云盘连接失败：, accessNotConfigured"


def test_colab_drivefs_mode_local_sync_and_trash(tmp_path, monkeypatch):
    drive_root = tmp_path / "drive"
    mydrive = drive_root / "MyDrive"
    mydrive.mkdir(parents=True)
    data_root = tmp_path / "colabone-data"
    data_root.mkdir()
    catalog = tmp_path / "catalog"
    catalog.mkdir()

    drive = ColabOneDrive(data_root, catalog, mount_root=mydrive)
    monkeypatch.setattr(drive, "is_colab_drivefs_mounted", lambda: True)

    info = drive.connect()
    assert info["connected"] is True
    assert info["mode"] == "colab_drivefs"
    assert info["mount_path"] == str(mydrive)

    # Seed local workspace files + ignored temp files and push to DriveFS.
    (data_root / "models.json").write_text('[{"id":"m1"}]', encoding="utf-8")
    (data_root / "workflows").mkdir()
    (data_root / "workflows" / "wf1.json").write_text('{"id":"wf1"}', encoding="utf-8")
    (data_root / "workflows" / "wf1.json.tmp").write_text("skip", encoding="utf-8")
    (data_root / "comfyui-outputs").mkdir()
    oid = "b" * 32
    (data_root / "comfyui-outputs" / f"{oid}.png").write_bytes(b"PNGDATA")
    (data_root / "comfyui-outputs" / f"{oid}.json").write_text('{"id":"b"}', encoding="utf-8")

    assert drive.push_workspace() is True
    ws = mydrive / COLABONE_DRIVE_FOLDER / "workspace"
    assert (ws / "models.json").is_file()
    assert (ws / "workflows" / "wf1.json").is_file()
    assert not (ws / "workflows" / "wf1.json.tmp").exists()
    assert (ws / "comfyui-outputs" / f"{oid}.png").read_bytes() == b"PNGDATA"

    # Pull into a fresh directory.
    pull_dir = tmp_path / "pull"
    assert drive.pull_workspace(pull_dir) is True
    assert (pull_dir / "models.json").read_text(encoding="utf-8") == '[{"id":"m1"}]'
    assert (pull_dir / "workflows" / "wf1.json").is_file()
    assert (pull_dir / "comfyui-outputs" / f"{oid}.png").read_bytes() == b"PNGDATA"

    # Same-size rapid edit even with FUSE mtime skew must still sync cleanly.
    import os as _os

    (data_root / "models.json").write_text('[{"id":"m2"}]', encoding="utf-8")
    _os.utime(ws / "models.json", (2000000000, 2000000000))
    assert drive.push_workspace() is True
    assert (ws / "models.json").read_text(encoding="utf-8") == '[{"id":"m2"}]'

    # Deletions move files safely into .trash/.
    drive.remove_remote_workflow("wf1")
    drive.remove_remote_comfy_output(oid, "png")
    assert not (ws / "workflows" / "wf1.json").exists()
    assert (ws / "workflows" / ".trash" / "wf1.json").is_file()
    assert (ws / "comfyui-outputs" / ".trash" / f"{oid}.png").is_file()
    assert (ws / "comfyui-outputs" / ".trash" / f"{oid}.json").is_file()
