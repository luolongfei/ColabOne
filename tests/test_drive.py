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


def test_sanitize_drive_export_filename_and_drivefs_export_collision_handling(
    tmp_path, monkeypatch
):
    from server.drive import (
        COLABONE_DRIVE_OUTPUTS_SUBDIR,
        sanitize_drive_export_filename,
    )

    assert (
        sanitize_drive_export_filename(
            "../evil/path:name*?.mp4", "a1b2c3d4" + "0" * 24, "mp4"
        )
        == "path_name_.mp4"
    )
    assert (
        sanitize_drive_export_filename("...", "a1b2c3d4" + "0" * 24, "png")
        == "colabone-a1b2c3d4.png"
    )
    assert (
        sanitize_drive_export_filename("clip_final", "deadbeef" + "0" * 24, "MP4")
        == "clip_final.mp4"
    )
    assert (
        sanitize_drive_export_filename("CON.png", "deadbeef" + "0" * 24, "png")
        == "colabone-CON.png"
    )
    assert (
        sanitize_drive_export_filename("nul.mp4", "deadbeef" + "0" * 24, "mp4")
        == "colabone-nul.mp4"
    )
    with pytest.raises(ValueError, match="输出文件格式不合法"):
        sanitize_drive_export_filename("demo.mp4", "a" * 32, "../sh")

    drive_root = tmp_path / "drive"
    mydrive = drive_root / "MyDrive"
    mydrive.mkdir(parents=True)
    data_root = tmp_path / "colabone-data"
    data_root.mkdir()
    catalog = tmp_path / "catalog"
    catalog.mkdir()

    drive = ColabOneDrive(data_root, catalog, mount_root=mydrive)
    monkeypatch.setattr(drive, "is_colab_drivefs_mounted", lambda: True)
    drive.connect()

    src1 = data_root / "item1.mp4"
    src1.write_bytes(b"VIDEO-BYTES-ONE")
    sha1 = hashlib.sha256(b"VIDEO-BYTES-ONE").hexdigest()
    oid1 = "11112222" + "a" * 24

    res1 = drive.export_output_file(
        src1,
        filename="digital-human_720x1280.mp4",
        output_id=oid1,
        extension="mp4",
        expected_sha256=sha1,
        expected_size=src1.stat().st_size,
    )
    out_dir = mydrive / COLABONE_DRIVE_FOLDER / COLABONE_DRIVE_OUTPUTS_SUBDIR
    assert res1["ok"] is True
    assert res1["already_exists"] is False
    assert res1["filename"] == "digital-human_720x1280.mp4"
    assert res1["remote_folder"] == f"{COLABONE_DRIVE_FOLDER}/{COLABONE_DRIVE_OUTPUTS_SUBDIR}"
    assert res1["drive_path"] == (
        f"My Drive/{COLABONE_DRIVE_FOLDER}/{COLABONE_DRIVE_OUTPUTS_SUBDIR}/digital-human_720x1280.mp4"
    )
    assert (out_dir / "digital-human_720x1280.mp4").read_bytes() == b"VIDEO-BYTES-ONE"

    # Re-exporting identical content is idempotent.
    res1_again = drive.export_output_file(
        src1,
        filename="digital-human_720x1280.mp4",
        output_id=oid1,
        extension="mp4",
        expected_sha256=sha1,
        expected_size=src1.stat().st_size,
    )
    assert res1_again["already_exists"] is True
    assert res1_again["filename"] == "digital-human_720x1280.mp4"

    # Different artwork with the same filename must disambiguate rather than overwrite.
    src2 = data_root / "item2.mp4"
    src2.write_bytes(b"VIDEO-BYTES-DIFFERENT")
    sha2 = hashlib.sha256(b"VIDEO-BYTES-DIFFERENT").hexdigest()
    oid2 = "abcd5678" + "b" * 24
    res2 = drive.export_output_file(
        src2,
        filename="digital-human_720x1280.mp4",
        output_id=oid2,
        extension="mp4",
        expected_sha256=sha2,
        expected_size=src2.stat().st_size,
    )
    assert res2["already_exists"] is False
    assert res2["filename"] == "digital-human_720x1280-abcd5678.mp4"
    assert (out_dir / "digital-human_720x1280.mp4").read_bytes() == b"VIDEO-BYTES-ONE"
    assert (out_dir / "digital-human_720x1280-abcd5678.mp4").read_bytes() == b"VIDEO-BYTES-DIFFERENT"

    # Expected size mismatch must ignore stale expected_sha256 and recompute actual digest.
    res_stale = drive.export_output_file(
        src1,
        filename="digital-human_720x1280.mp4",
        output_id=oid1,
        extension="mp4",
        expected_sha256="0" * 64,
        expected_size=999999,
    )
    assert res_stale["already_exists"] is True

    # Failed temp-file write (e.g. ENOSPC) must NOT retry directly into dst or leave a partial file,
    # and must not pollute global drive.error.
    drive.error = ""
    from server import drive as drive_mod

    orig_stream = drive_mod._stream_write_file

    def fail_tmp_write(src, target):
        if target.name.startswith(".co-export-"):
            target.write_bytes(b"PARTIAL")
            raise OSError("No space left on device")
        return orig_stream(src, target)

    monkeypatch.setattr(drive_mod, "_stream_write_file", fail_tmp_write)
    with pytest.raises(RuntimeError, match="写入 Google 云盘失败"):
        drive.export_output_file(
            src1,
            filename="should_not_exist.mp4",
            output_id="99998888" + "e" * 24,
            extension="mp4",
        )
    assert not (out_dir / "should_not_exist.mp4").exists()
    assert not any(p.name.startswith(".co-export-") for p in out_dir.iterdir())
    assert drive.error == ""
    monkeypatch.setattr(drive_mod, "_stream_write_file", orig_stream)


def test_rclone_export_output_file_idempotency_and_collision(tmp_path, monkeypatch):
    catalog = tmp_path / "catalog"
    catalog.mkdir()
    root = tmp_path / "data"
    root.mkdir()
    (root / ".rclone.conf").write_text(
        f"[{COLABONE_DRIVE_REMOTE}]\ntype = drive\n", encoding="utf-8"
    )
    drive = ColabOneDrive(root, catalog, mount_root=root / "mnt")
    drive.mode = "rclone_sync"

    fake_bin = tmp_path / "rclone"
    monkeypatch.setattr("server.drive.ensure_rclone_binary", lambda *a, **k: fake_bin)

    src = root / "clip.mp4"
    payload = b"RCLONE-MP4-CONTENT"
    src.write_bytes(payload)
    sha = hashlib.sha256(payload).hexdigest()
    oid = "feed1234" + "c" * 24

    # 1. File does not exist yet -> uploads with rclone copyto.
    calls = []

    def fake_run_upload(cmd, **kwargs):
        calls.append(list(cmd))
        if "lsjson" in cmd:
            return SimpleNamespace(returncode=3, stdout="", stderr="not found")
        return SimpleNamespace(returncode=0, stdout="", stderr="")

    monkeypatch.setattr("server.drive.subprocess.run", fake_run_upload)
    res = drive.export_output_file(
        src,
        filename="avatar_out.mp4",
        output_id=oid,
        extension="mp4",
        expected_sha256=sha,
        expected_size=len(payload),
    )
    assert res["ok"] is True
    assert res["already_exists"] is False
    assert res["filename"] == "avatar_out.mp4"
    assert "--stat" in calls[0] and "--hash" in calls[0]
    assert (
        "copyto" in calls[1]
        and f"{COLABONE_DRIVE_SPEC}/outputs/avatar_out.mp4" in calls[1]
        and "--drive-chunk-size" in calls[1]
    )

    # 2. Identical file already exists (--stat dict response) -> skips upload cleanly.
    calls.clear()

    def fake_run_exists(cmd, **kwargs):
        calls.append(list(cmd))
        return SimpleNamespace(
            returncode=0,
            stdout=json.dumps(
                {
                    "Name": "avatar_out.mp4",
                    "Size": len(payload),
                    "IsDir": False,
                    "Hashes": {"SHA-256": sha},
                }
            ),
            stderr="",
        )

    monkeypatch.setattr("server.drive.subprocess.run", fake_run_exists)
    res_same = drive.export_output_file(
        src,
        filename="avatar_out.mp4",
        output_id=oid,
        extension="mp4",
        expected_sha256=sha,
        expected_size=len(payload),
    )
    assert res_same["already_exists"] is True
    assert len(calls) == 1 and "lsjson" in calls[0]

    # 3. Different file (or directory) with same filename -> disambiguates and uploads to -feed1234.mp4.
    calls.clear()

    def fake_run_collision(cmd, **kwargs):
        calls.append(list(cmd))
        if "lsjson" in cmd and cmd[-1].endswith("/avatar_out.mp4"):
            return SimpleNamespace(
                returncode=0,
                stdout=json.dumps(
                    {
                        "Name": "avatar_out.mp4",
                        "Size": -1,
                        "IsDir": True,
                    }
                ),
                stderr="",
            )
        if "lsjson" in cmd:
            return SimpleNamespace(returncode=3, stdout="", stderr="not found")
        return SimpleNamespace(returncode=0, stdout="", stderr="")

    monkeypatch.setattr("server.drive.subprocess.run", fake_run_collision)
    res_col = drive.export_output_file(
        src,
        filename="avatar_out.mp4",
        output_id=oid,
        extension="mp4",
        expected_sha256=sha,
        expected_size=len(payload),
    )
    assert res_col["already_exists"] is False
    assert res_col["filename"] == "avatar_out-feed1234.mp4"
    assert any(
        "copyto" in c and f"{COLABONE_DRIVE_SPEC}/outputs/avatar_out-feed1234.mp4" in c
        for c in calls
    )

    # 4. Non-not-found lsjson error (e.g., auth failure) must abort instead of blindly overwriting.
    calls.clear()

    def fake_run_lsjson_err(cmd, **kwargs):
        calls.append(list(cmd))
        return SimpleNamespace(
            returncode=1,
            stdout="",
            stderr="couldn't fetch token: invalid_grant",
        )

    monkeypatch.setattr("server.drive.subprocess.run", fake_run_lsjson_err)
    with pytest.raises(RuntimeError):
        drive.export_output_file(
            src,
            filename="avatar_out.mp4",
            output_id=oid,
            extension="mp4",
            expected_sha256=sha,
            expected_size=len(payload),
        )
    assert not any("copyto" in c for c in calls)
    assert drive.error == ""


def test_remote_api_export_comfy_output_to_drive_endpoint(tmp_path, spec, monkeypatch):
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
    mydrive = tmp_path / "drive" / "MyDrive"
    mydrive.mkdir(parents=True)
    vm_root = tmp_path / "vm"
    drive = ColabOneDrive(vm_root, catalog, mount_root=mydrive)
    drive_mounted = {"value": False}
    monkeypatch.setattr(drive, "is_colab_drivefs_mounted", lambda: drive_mounted["value"])

    rt = ColabOneRuntime(vm_root, catalog, drive=drive)
    output_rec = rt.comfy.store.save_output(
        raw=_sample_png_bytes(),
        workflow_id="wf-test",
        workflow_name="测试工作流",
        filename="portrait_result.png",
        elapsed=2.5,
        prompt_id="prompt-drive-1",
    )
    oid = output_rec["id"]
    token = "test-control-token-at-least-32-chars"
    headers = {"Authorization": "Bearer " + token}

    with TestClient(create_colabone_app(rt, token)) as client:
        # Unauthenticated request rejected.
        assert client.post(f"/api/v1/comfyui/outputs/{oid}/export-drive").status_code == 401
        # Invalid output ID rejected.
        assert (
            client.post(
                "/api/v1/comfyui/outputs/not-a-hex-id/export-drive", headers=headers
            ).status_code
            == 400
        )
        # Missing output returns 404.
        assert (
            client.post(
                f"/api/v1/comfyui/outputs/{'0' * 32}/export-drive", headers=headers
            ).status_code
            == 404
        )
        # Drive not mounted yet returns 400 with actionable message.
        unmounted = client.post(
            f"/api/v1/comfyui/outputs/{oid}/export-drive", headers=headers
        )
        assert unmounted.status_code == 400
        assert "尚未连接 Google 云盘" in unmounted.json()["detail"]

        # Once DriveFS is mounted, export succeeds and survives output deletion.
        drive_mounted["value"] = True
        rt.connect_drive()
        exported = client.post(
            f"/api/v1/comfyui/outputs/{oid}/export-drive", headers=headers
        )
        assert exported.status_code == 200
        body = exported.json()
        assert body["ok"] is True
        assert body["id"] == oid
        assert body["filename"] == "portrait_result.png"
        assert body["drive_path"] == "My Drive/ColabOne_Drive/outputs/portrait_result.png"
        saved_file = mydrive / COLABONE_DRIVE_FOLDER / "outputs" / "portrait_result.png"
        assert saved_file.is_file()

        # Deleting the ComfyUI output must NOT remove the permanent exported copy in outputs/.
        assert (
            client.delete(f"/api/v1/comfyui/outputs/{oid}", headers=headers).status_code
            == 200
        )
        assert saved_file.is_file()


def test_drive_live_driving_motions_cache_exclusions_trash_and_safe_stage_merge(
    tmp_path, spec, monkeypatch
):
    from server.drive import _LIVE_AVATAR_ID_RE

    # 1. Verify rclone filter rules include live-driving-motions and exclude rebuildable caches BEFORE includes.
    args = list(ColabOneDrive._WORKSPACE_FILTER_ARGS)
    assert "+ /live-avatars/**" in args
    assert "+ /live-driving-motions/**" in args
    assert "- **/.musetalk_cache/**" in args
    assert "- **/.trt_cache/**" in args
    assert args.index("- **/.musetalk_cache/**") < args.index("+ /live-avatars/**")
    assert args.index("- **/.trt_cache/**") < args.index("+ /live-avatars/**")
    assert args.index("+ /live-driving-motions/**") < args.index("- **")

    # 2. Verify aligned ID regex rejects leading hyphens/underscores and >64 chars.
    assert _LIVE_AVATAR_ID_RE.fullmatch("av_ok-1") is not None
    assert _LIVE_AVATAR_ID_RE.fullmatch("-leading-dash") is None
    assert _LIVE_AVATAR_ID_RE.fullmatch("_leading_under") is None
    assert _LIVE_AVATAR_ID_RE.fullmatch("a" * 65) is None

    # 3. Verify DriveFS syncs live-driving-motions, skips .musetalk_cache/.trt_cache, and safely trashes motions.
    mydrive = tmp_path / "drive" / "MyDrive"
    mydrive.mkdir(parents=True)
    data_root = tmp_path / "vm"
    data_root.mkdir()
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

    drive = ColabOneDrive(data_root, catalog, mount_root=mydrive)
    monkeypatch.setattr(drive, "is_colab_drivefs_mounted", lambda: True)
    drive.connect()

    motion_dir = data_root / "live-driving-motions" / "dance_01"
    motion_dir.mkdir(parents=True)
    (motion_dir / "clip.mp4").write_bytes(b"mp4-driving-bytes")
    (motion_dir / "meta.json").write_text(
        json.dumps(
            {
                "id": "dance_01",
                "label": "测试舞蹈",
                "clip_path": "clip.mp4",
                "bgm_path": "",
                "updated_at": 100.0,
            }
        ),
        encoding="utf-8",
    )
    av_cache = data_root / "live-avatars" / "av1" / ".musetalk_cache"
    av_cache.mkdir(parents=True)
    (av_cache / "huge.bin").write_bytes(b"cache-skip")
    (data_root / "live-avatars" / "av1" / "portrait.png").write_bytes(b"png")

    assert drive.push_workspace() is True
    ws = mydrive / COLABONE_DRIVE_FOLDER / "workspace"
    assert (ws / "live-driving-motions" / "dance_01" / "clip.mp4").read_bytes() == b"mp4-driving-bytes"
    assert not (ws / "live-avatars" / "av1" / ".musetalk_cache").exists()

    pull_dir = tmp_path / "pull"
    assert drive.pull_workspace(pull_dir) is True
    assert (pull_dir / "live-driving-motions" / "dance_01" / "clip.mp4").read_bytes() == b"mp4-driving-bytes"

    drive.trash_live_driving_motion("dance_01")
    assert not (ws / "live-driving-motions" / "dance_01").exists()
    assert (ws / "live-driving-motions" / ".trash" / "dance_01" / "clip.mp4").is_file()

    for bad_id in ("", "-bad", "_bad", "..", "../escape", "a/b", "a.b", "x" * 65):
        with pytest.raises(ValueError):
            drive.trash_live_driving_motion(bad_id)
        with pytest.raises(ValueError):
            drive.trash_live_avatar(bad_id)

    link_out = tmp_path / "outside_motion"
    link_out.mkdir()
    (ws / "live-driving-motions" / "sym_motion").symlink_to(link_out)
    with pytest.raises(ValueError):
        drive.trash_live_driving_motion("sym_motion")

    # 4. Verify runtime._merge_live_avatars_stage & _merge_live_driving_motions_stage:
    #    - skips locally trashed avatars/motions
    #    - validates manifest.json against ColabOneLiveAvatarManifest (rejects corrupt / mismatched / leading-hyphen)
    #    - merges assets_meta.json per-entry by updated_at
    rt = ColabOneRuntime(tmp_path / "vm_merge", catalog)
    local_av = rt.root / "live-avatars" / "av_merge"
    local_av.mkdir(parents=True)
    (local_av / "manifest.json").write_text(
        json.dumps(
            {
                "id": "av_merge",
                "name": "旧名称",
                "portrait_path": "portrait.png",
                "actions": [],
                "products": [],
                "created_at": 10.0,
                "updated_at": 50.0,
            }
        ),
        encoding="utf-8",
    )
    (local_av / "assets_meta.json").write_text(
        json.dumps(
            {
                "clips/local_new.mp4": {"sha256": "localnew", "size": 10, "updated_at": 300.0},
                "portrait.png": {"sha256": "oldsha", "size": 5, "updated_at": 50.0},
            }
        ),
        encoding="utf-8",
    )
    (rt.root / "live-avatars" / ".trash" / "av_deleted").mkdir(parents=True)

    stage = tmp_path / "stage_avatars"
    # Valid avatar with newer manifest and mixed assets_meta timestamps
    st_ok = stage / "av_merge"
    st_ok.mkdir(parents=True)
    (st_ok / "portrait.png").write_bytes(b"new-portrait")
    (st_ok / "manifest.json").write_text(
        json.dumps(
            {
                "id": "av_merge",
                "name": "新名称",
                "portrait_path": "portrait.png",
                "actions": [],
                "products": [],
                "created_at": 10.0,
                "updated_at": 200.0,
            }
        ),
        encoding="utf-8",
    )
    (st_ok / "assets_meta.json").write_text(
        json.dumps(
            {
                "clips/local_new.mp4": {"sha256": "stale", "size": 10, "updated_at": 100.0},
                "portrait.png": {"sha256": "newsha", "size": 12, "updated_at": 200.0},
                "clips/remote_only.mp4": {"sha256": "remsha", "size": 20, "updated_at": 150.0},
            }
        ),
        encoding="utf-8",
    )
    # Locally trashed avatar must NOT be resurrected
    st_del = stage / "av_deleted"
    st_del.mkdir(parents=True)
    (st_del / "manifest.json").write_text(
        json.dumps(
            {
                "id": "av_deleted",
                "name": "已删主播",
                "actions": [],
                "products": [],
                "updated_at": 999.0,
            }
        ),
        encoding="utf-8",
    )
    # Invalid manifest (mismatched id / schema violation) and leading-hyphen dir must be skipped
    st_bad = stage / "av_invalid"
    st_bad.mkdir(parents=True)
    (st_bad / "manifest.json").write_text(
        json.dumps({"id": "wrong_id", "name": "坏包", "extra_bad": 1}),
        encoding="utf-8",
    )
    st_dash = stage / "-bad-leading"
    st_dash.mkdir(parents=True)
    (st_dash / "manifest.json").write_text(
        json.dumps({"id": "-bad-leading", "name": "非法名"}),
        encoding="utf-8",
    )

    rt._merge_live_avatars_stage(stage)

    merged_manifest = json.loads((local_av / "manifest.json").read_text(encoding="utf-8"))
    assert merged_manifest["name"] == "新名称"
    merged_meta = json.loads((local_av / "assets_meta.json").read_text(encoding="utf-8"))
    assert merged_meta["clips/local_new.mp4"]["sha256"] == "localnew"
    assert merged_meta["portrait.png"]["sha256"] == "newsha"
    assert merged_meta["clips/remote_only.mp4"]["sha256"] == "remsha"
    assert not (rt.root / "live-avatars" / "av_deleted").exists()
    assert not (rt.root / "live-avatars" / "av_invalid").exists()
    assert not (rt.root / "live-avatars" / "-bad-leading").exists()

    # Driving motions stage merge
    (rt.root / "live-driving-motions" / ".trash" / "dm_trashed").mkdir(parents=True)
    m_stage = tmp_path / "stage_motions"
    for mid, valid_clip in (("dm_good", True), ("dm_trashed", True), ("dm_missing_clip", False)):
        d = m_stage / mid
        d.mkdir(parents=True)
        if valid_clip:
            (d / "clip.mp4").write_bytes(b"clip-data")
        (d / "meta.json").write_text(
            json.dumps(
                {
                    "id": mid,
                    "label": f"动作 {mid}",
                    "clip_path": "clip.mp4",
                    "bgm_path": "",
                    "updated_at": 100.0,
                }
            ),
            encoding="utf-8",
        )

    rt._merge_live_driving_motions_stage(m_stage)
    assert (rt.root / "live-driving-motions" / "dm_good" / "clip.mp4").read_bytes() == b"clip-data"
    assert not (rt.root / "live-driving-motions" / "dm_trashed").exists()
    assert not (rt.root / "live-driving-motions" / "dm_missing_clip").exists()


def test_drive_thread_local_http_session_cookie_policy_and_close(tmp_path):
    import requests

    root = tmp_path / "data"
    root.mkdir()
    catalog = tmp_path / "catalog"
    catalog.mkdir()
    drive = ColabOneDrive(root, catalog, http=requests.Session())

    seen = []

    def worker():
        s1 = drive._http_session()
        s2 = drive._http_session()
        assert s1 is s2
        assert isinstance(s1, requests.Session)
        assert s1.trust_env is False
        assert tuple(s1.cookies.get_policy().allowed_domains()) == ()
        seen.append(s1)

    import threading

    t1 = threading.Thread(target=worker)
    t2 = threading.Thread(target=worker)
    t1.start()
    t2.start()
    t1.join(timeout=5)
    t2.join(timeout=5)
    assert len(seen) == 2
    assert seen[0] is not seen[1]

    closed = []
    for s in seen:
        orig_close = s.close
        s.close = lambda orig=orig_close, sess=s: (closed.append(sess), orig())
    drive.close_http_sessions()
    assert len(closed) == 2

    mock_http = Mock()
    drive_mock = ColabOneDrive(root, catalog, http=mock_http)
    assert drive_mock._http_session() is mock_http


def test_drive_nonblocking_transfers_and_single_flight_space_cache(tmp_path, monkeypatch):
    import threading
    import time

    root = tmp_path / "data"
    root.mkdir()
    catalog = tmp_path / "catalog"
    catalog.mkdir()
    (root / ".rclone.conf").write_text(
        f"[{COLABONE_DRIVE_REMOTE}]\ntype = drive\n", encoding="utf-8"
    )
    drive = ColabOneDrive(root, catalog, mount_root=root / "mnt")
    drive.mode = "rclone_sync"

    fake_bin = tmp_path / "rclone"
    fake_bin.write_text("", encoding="utf-8")
    monkeypatch.setattr("server.drive.ensure_rclone_binary", lambda *a, **k: fake_bin)
    monkeypatch.setattr(drive, "_existing_rclone_binary", lambda: fake_bin)

    copy_entered = threading.Event()
    copy_release = threading.Event()
    about_started = threading.Event()
    about_release = threading.Event()
    about_calls = 0
    about_lock = threading.Lock()

    def fake_run(cmd, **kwargs):
        nonlocal about_calls
        if "copy" in cmd:
            copy_entered.set()
            assert copy_release.wait(timeout=5)
            return SimpleNamespace(returncode=0, stdout="", stderr="")
        if "about" in cmd:
            with about_lock:
                about_calls += 1
            about_started.set()
            assert about_release.wait(timeout=5)
            return SimpleNamespace(
                returncode=0,
                stdout=json.dumps({"total": 1000, "free": 600}),
                stderr="",
            )
        return SimpleNamespace(returncode=0, stdout="", stderr="")

    monkeypatch.setattr("server.drive.subprocess.run", fake_run)

    # 1. push_workspace does not hold drive.lock across blocking rclone copy.
    push_res = []
    t_push = threading.Thread(target=lambda: push_res.append(drive.push_workspace()))
    t_push.start()
    assert copy_entered.wait(timeout=3)
    assert drive.lock.acquire(blocking=False)
    drive.lock.release()
    assert drive.available() is True
    copy_release.set()
    t_push.join(timeout=3)
    assert push_res == [True]

    # 2. Concurrent status() calls trigger only one rclone about subprocess (single-flight).
    results = []
    t_a = threading.Thread(target=lambda: results.append(drive.status()))
    t_a.start()
    assert about_started.wait(timeout=3)

    t_b = threading.Thread(target=lambda: results.append(drive.status()))
    t_b.start()
    t_b.join(timeout=2)
    assert not t_b.is_alive()
    about_release.set()
    t_a.join(timeout=3)

    assert about_calls == 1
    assert len(results) == 2
    # Subsequent call within 30s hits cache without another subprocess.
    st = drive.status()
    assert st["total_bytes"] == 1000 and st["free_bytes"] == 600
    assert about_calls == 1


def test_copy_file_if_newer_fsyncs_write_fd_preserves_mtime_and_symlink_safe(
    tmp_path, monkeypatch
):
    from server import drive as drive_mod

    src = tmp_path / "src.bin"
    src.write_bytes(b"new-payload-data")
    target_mtime = 1700000123
    os.utime(src, (target_mtime, target_mtime))

    fsync_fds = []
    real_fsync = os.fsync

    def spy_fsync(fd):
        fsync_fds.append(fd)
        return real_fsync(fd)

    monkeypatch.setattr(drive_mod.os, "fsync", spy_fsync)

    dst = tmp_path / "dst.bin"
    drive_mod._copy_file_if_newer(src, dst)
    assert dst.read_bytes() == b"new-payload-data"
    assert int(dst.stat().st_mtime) == target_mtime
    assert len(fsync_fds) >= 1

    # Symlink introduced before rename fallback must NOT be overwritten.
    secret = tmp_path / "secret.txt"
    secret.write_text("untouched", encoding="utf-8")
    dst2 = tmp_path / "dst2.bin"

    orig_replace = Path.replace

    def swap_to_symlink_and_fail(self_path, other):
        if self_path.name.startswith(".co-sync-"):
            if other.exists():
                other.unlink()
            other.symlink_to(secret)
            raise OSError("rename blocked")
        return orig_replace(self_path, other)

    monkeypatch.setattr(Path, "replace", swap_to_symlink_and_fail)
    drive_mod._copy_file_if_newer(src, dst2)
    assert secret.read_text(encoding="utf-8") == "untouched"


def test_drive_tombstones_prevent_resurrection_and_replay_offline_deletions(
    tmp_path, spec, monkeypatch
):
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

    mydrive = tmp_path / "drive" / "MyDrive"
    mydrive.mkdir(parents=True)
    vm1_root = tmp_path / "vm1"
    drive1 = ColabOneDrive(vm1_root, catalog, mount_root=mydrive)
    mounted = {"v": True}
    monkeypatch.setattr(drive1, "is_colab_drivefs_mounted", lambda: mounted["v"])

    rt1 = ColabOneRuntime(vm1_root, catalog, drive=drive1)
    rt1.connect_drive()
    wf = rt1.comfy.store.save_workflow(
        {
            "id": "wf-offline-del",
            "name": "待删除工作流",
            "description": "tombstone test",
            "workflow": {"1": {"class_type": "CLIPTextEncode", "inputs": {"text": "hi"}}},
        }
    )
    out_rec = rt1.comfy.store.save_output(
        raw=_sample_png_bytes(),
        workflow_id=wf["id"],
        workflow_name=wf["name"],
        filename="img.png",
        elapsed=0.5,
        prompt_id="p1",
    )
    oid = out_rec["id"]
    # Seed avatar & driving motion and push everything to Drive.
    av_dir = vm1_root / "live-avatars" / "av_del_1"
    av_dir.mkdir(parents=True)
    (av_dir / "manifest.json").write_text(
        json.dumps(
            {
                "id": "av_del_1",
                "name": "待删主播",
                "actions": [],
                "products": [],
                "updated_at": 100.0,
            }
        ),
        encoding="utf-8",
    )
    dm_dir = vm1_root / "live-driving-motions" / "dm_del_1"
    dm_dir.mkdir(parents=True)
    (dm_dir / "clip.mp4").write_bytes(b"mp4-bytes")
    (dm_dir / "meta.json").write_text(
        json.dumps(
            {
                "id": "dm_del_1",
                "label": "待删动作",
                "clip_path": "clip.mp4",
                "bgm_path": "",
                "updated_at": 100.0,
            }
        ),
        encoding="utf-8",
    )
    rt1._mark_dirty("live-avatars")
    rt1._mark_dirty("live-driving-motions")
    assert rt1.sync_drive()["ok"] is True

    ws = mydrive / COLABONE_DRIVE_FOLDER / "workspace"
    assert (ws / "workflows" / "wf-offline-del.json").is_file()
    assert (ws / "comfyui-outputs" / f"{oid}.json").is_file()
    assert (ws / "live-avatars" / "av_del_1").is_dir()
    assert (ws / "live-driving-motions" / "dm_del_1").is_dir()

    # Drive goes offline; delete all four items locally.
    mounted["v"] = False
    rt1.drive.mode = "disabled"
    rt1.comfy.store.delete_workflow("wf-offline-del")
    rt1.comfy.store.delete_output(oid)
    rt1.delete_live_avatar("av_del_1")
    rt1.delete_live_driving_motion("dm_del_1")

    # Drive reconnects: _restore_from_drive must NOT resurrect any deleted item,
    # and _sync_to_drive must replay local tombstones onto Drive.
    mounted["v"] = True
    rt1.drive_restored = False
    rt1.connect_drive()

    assert not (vm1_root / "workflows" / "wf-offline-del.json").exists()
    assert not (vm1_root / "comfyui-outputs" / f"{oid}.json").exists()
    assert not (vm1_root / "live-avatars" / "av_del_1").exists()
    assert not (vm1_root / "live-driving-motions" / "dm_del_1").exists()

    assert not (ws / "workflows" / "wf-offline-del.json").exists()
    assert (ws / "workflows" / ".trash" / "wf-offline-del.json").is_file()
    assert not (ws / "comfyui-outputs" / f"{oid}.json").exists()
    assert (ws / "comfyui-outputs" / ".trash" / f"{oid}.json").is_file()
    assert not (ws / "live-avatars" / "av_del_1").exists()
    assert (ws / "live-avatars" / ".trash" / "av_del_1").is_dir()
    assert not (ws / "live-driving-motions" / "dm_del_1").exists()
    assert (ws / "live-driving-motions" / ".trash" / "dm_del_1").is_dir()

    # Fresh VM2 connecting to Drive sees none of the deleted items resurrected.
    vm2_root = tmp_path / "vm2"
    drive2 = ColabOneDrive(vm2_root, catalog, mount_root=mydrive)
    monkeypatch.setattr(drive2, "is_colab_drivefs_mounted", lambda: True)
    rt2 = ColabOneRuntime(vm2_root, catalog, drive=drive2)
    rt2.connect_drive()
    assert not (vm2_root / "workflows" / "wf-offline-del.json").exists()
    assert rt2.comfy.store.list_outputs() == []
    assert not (vm2_root / "live-avatars" / "av_del_1").exists()
    assert not (vm2_root / "live-driving-motions" / "dm_del_1").exists()
