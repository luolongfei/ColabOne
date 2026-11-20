"""Offline tests for ColabOne headless rclone Google Drive mount & persistence."""

import hashlib
import io
import json
import zipfile
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock

import pytest
from fastapi.testclient import TestClient
from PIL import Image

from bridge.auth import ColabOneAuth
from bridge.controller import ColabOneController
from conftest import MemoryVault
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


def test_build_rclone_conf_uses_colabone_naming_and_drive_file_scope(monkeypatch):
    monkeypatch.setattr(
        "bridge.auth.Request",
        lambda *_, **__: (_ for _ in ()).throw(
            AssertionError("offline test must not call Google OAuth endpoint")
        ),
    )
    vault = MemoryVault()
    saved = {
        "token": "access-tok",
        "refresh_token": "refresh-tok",
        "client_id": "cid.apps.googleusercontent.com",
        "client_secret": "csecret",
        "expiry": "2099-01-01T00:00:00Z",
        "scopes": [
            "openid",
            "https://www.googleapis.com/auth/colaboratory",
            "https://www.googleapis.com/auth/drive.file",
        ],
    }
    vault.set("colab", saved)
    auth = ColabOneAuth(vault)
    assert auth.has_drive_scope() is True
    conf = auth.build_rclone_conf()
    assert f"[{COLABONE_DRIVE_REMOTE}]" in conf
    assert "scope = drive.file" in conf
    assert '"access_token":"access-tok"' in conf
    assert '"refresh_token":"refresh-tok"' in conf

    # Token lacking drive scope returns empty config rather than broken remote.
    vault.set("colab", saved | {"scopes": ["openid"]})
    assert auth.has_drive_scope() is False
    assert auth.build_rclone_conf() == ""


def test_stage_rclone_config_enforces_0600_and_removes_staging(tmp_path):
    stage = tmp_path / "staged.conf"
    stage.write_text(f"[{COLABONE_DRIVE_REMOTE}]\ntype = drive\n", encoding="utf-8")
    data_root = tmp_path / "colabone-data"
    dst = stage_rclone_config(stage, data_root)
    assert dst == data_root / ".rclone.conf"
    assert not stage.exists()
    assert (dst.stat().st_mode & 0o777) == 0o600
    assert f"[{COLABONE_DRIVE_REMOTE}]" in dst.read_text(encoding="utf-8")


def test_ensure_rclone_binary_verifies_sha256_and_extracts(tmp_path, monkeypatch):
    monkeypatch.setattr("server.drive.shutil.which", lambda _: None)
    monkeypatch.setattr("server.drive.platform.system", lambda: "Linux")
    monkeypatch.setattr("server.drive.platform.machine", lambda: "x86_64")

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
        assert "/models.json" in cmd
        assert "/workflows/**" in cmd
        assert "/comfyui-outputs/**" in cmd
        assert "/conversation-*.json" not in cmd
        assert "/creations/**" not in cmd
        assert "**/.trash/**" in cmd

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


def test_controller_starts_gpu_and_mounts_colabone_drive_without_browser_consent(tmp_path):
    c = ColabOneController(
        Path(__file__).resolve().parents[1],
        vault=MemoryVault(),
        config_dir=tmp_path,
        background=False,
    )
    c.vault.set(
        "colab",
        {
            "token": "valid-tok",
            "refresh_token": "refresh-tok",
            "client_id": "cid",
            "client_secret": "csec",
            "scopes": ["https://www.googleapis.com/auth/drive.file"],
        },
    )
    c.auth.credentials = Mock(
        return_value=SimpleNamespace(
            to_json=lambda: json.dumps(c.vault.get("colab")),
            token="valid-tok",
            refresh_token="refresh-tok",
        )
    )
    c.provider = Mock()
    c.provider.account.return_value = {"logged_in": True, "assignments": 1, "rate": 1.5}
    c.remote = Mock(
        side_effect=lambda method, path, body=None, endpoint=None: (
            {"connected": True, "mode": "mount", "remote_folder": COLABONE_DRIVE_FOLDER}
            if path == "drive/connect"
            else {"ok": True}
        )
    )
    c.refresh_remote = Mock()

    c.start()
    kwargs = c.provider.start.call_args.kwargs
    assert f"[{COLABONE_DRIVE_REMOTE}]" in kwargs["rclone_conf"]
    assert ("POST", "drive/connect") in [call.args[:2] for call in c.remote.call_args_list]
    assert c.snapshot()["auth_url"] == ""
