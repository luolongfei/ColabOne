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


def test_rclone_quota_project_directive_and_error_formatting(tmp_path, monkeypatch):
    from server.drive import format_rclone_error, parse_rclone_quota_project

    vault = MemoryVault()
    vault.set(
        "colab",
        {
            "token": "access-tok",
            "refresh_token": "refresh-tok",
            "client_id": "764086051850-6qr4p.apps.googleusercontent.com",
            "client_secret": "csecret",
            "expiry": "2099-01-01T00:00:00Z",
            "scopes": [
                "https://www.googleapis.com/auth/cloud-platform",
                "https://www.googleapis.com/auth/drive.file",
            ],
        },
    )
    vault.set("cloud_project", "colabone-drive-abcdef1234")
    auth = ColabOneAuth(vault)
    assert auth.requires_drive_quota_project() is True
    monkeypatch.setattr(
        "bridge.auth.Request",
        lambda *_, **__: (_ for _ in ()).throw(AssertionError("no live network")),
    )
    conf = auth.build_rclone_conf()
    assert "# colabone_quota_project = colabone-drive-abcdef1234" in conf

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


def test_ensure_drive_quota_project_handles_tos_and_existing_projects(monkeypatch):
    vault = MemoryVault()
    vault.set(
        "colab",
        {
            "token": "access-tok",
            "refresh_token": "refresh-tok",
            "client_id": "764086051850-6qr4p.apps.googleusercontent.com",
            "client_secret": "csecret",
            "expiry": "2099-01-01T00:00:00Z",
            "scopes": [
                "https://www.googleapis.com/auth/cloud-platform",
                "https://www.googleapis.com/auth/drive.file",
            ],
        },
    )
    vault.set("google_profile", {"sub": "user-123", "email": "user@example.com"})
    auth = ColabOneAuth(vault)

    # 1) Account with 0 GCP projects & ToS not accepted -> returns gcp_tos_required cleanly.
    fake_session = Mock()
    fake_session.get.side_effect = [
        SimpleNamespace(status_code=200, json=lambda: {}),
        SimpleNamespace(
            status_code=200,
            json=lambda: {
                "done": True,
                "error": {
                    "code": 9,
                    "message": "Callers must accept Terms of Service",
                },
            },
        ),
    ]
    fake_session.post.return_value = SimpleNamespace(
        status_code=200, json=lambda: {"name": "operations/create_project.1"}
    )
    monkeypatch.setattr("bridge.auth.AuthorizedSession", lambda _c: fake_session)
    monkeypatch.setattr("bridge.auth.time.sleep", lambda _: None)

    pid, reason = auth.ensure_drive_quota_project()
    assert pid == ""
    assert reason == "gcp_tos_required"
    # Second call hits negative cache without extra HTTP calls.
    pid2, reason2 = auth.ensure_drive_quota_project()
    assert (pid2, reason2) == ("", "gcp_tos_required")
    assert fake_session.post.call_count == 1

    # 2) Existing active project with drive.googleapis.com enabled -> reused read-only.
    auth.invalidate_drive_quota_project()
    fake_session2 = Mock()
    fake_session2.get.side_effect = [
        SimpleNamespace(
            status_code=200,
            json=lambda: {
                "projects": [
                    {"projectId": "my-existing-proj", "lifecycleState": "ACTIVE"}
                ]
            },
        ),
        SimpleNamespace(status_code=200, json=lambda: {"state": "ENABLED"}),
    ]
    monkeypatch.setattr("bridge.auth.AuthorizedSession", lambda _c: fake_session2)
    pid3, reason3 = auth.ensure_drive_quota_project()
    assert pid3 == "my-existing-proj"
    assert reason3 == "reused_existing_project"
    assert vault.get("cloud_project") == "my-existing-proj"


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


def test_colab_drive_mount_credentials_propagation_and_cancel():
    import threading
    from bridge.drive_mount import ColabOneDriveMount

    consent_uri = "https://accounts.google.com/o/oauth2/v2/auth?client_id=947318989803"
    client = SimpleNamespace(
        colab_domain="https://colab.research.google.com",
        session=Mock(),
    )
    get_counter = [0]

    def fake_get(_url, params=None, headers=None, timeout=15):
        get_counter[0] += 1
        assert headers and headers.get("X-Colab-Client-Agent") == "colab-cli"
        assert headers.get("Accept") == "application/json"
        return SimpleNamespace(
            status_code=200,
            text=")]}'\n" + json.dumps({"token": f"xsrf-tok-{get_counter[0]}"}),
        )

    post_calls = []

    def fake_post(_url, params=None, headers=None, files=None, timeout=15):
        post_calls.append((dict(params or {}), dict(headers or {})))
        n = len(post_calls)
        if n == 1:
            # Initial dryrun=true returns consent URL.
            assert params["dryrun"] == "true"
            return SimpleNamespace(
                status_code=200,
                text=")]}'\n"
                + json.dumps(
                    {"success": False, "unauthorized_redirect_uri": consent_uri}
                ),
            )
        if n == 2:
            # First dryrun=false poll while user is still consenting returns HTTP 400 HTML
            # (must NOT abort or crash JSON parser).
            assert params["dryrun"] == "false"
            return SimpleNamespace(
                status_code=400,
                text="<!DOCTYPE html><title>Error 400 (Bad Request)</title>",
            )
        # Subsequent dryrun=false poll succeeds once browser consent finishes!
        assert params["dryrun"] == "false"
        return SimpleNamespace(
            status_code=200, text=")]}'\n" + json.dumps({"success": True})
        )

    client.session.get.side_effect = fake_get
    client.session.post.side_effect = fake_post

    seen_urls = []
    sent_replies = []
    done = threading.Event()
    wsclient = SimpleNamespace(
        session=SimpleNamespace(msg=lambda kind, content: {"kind": kind, "content": content}),
        stdin_channel=SimpleNamespace(
            send=lambda msg: (sent_replies.append(msg), done.set())
        ),
    )

    helper = ColabOneDriveMount(
        client,
        "gpu-t4-test",
        on_auth_url=seen_urls.append,
        poll_interval=0.05,
    )
    handled = helper.hook(
        {
            "header": {"msg_id": "hdr-1"},
            "metadata": {"colab_msg_id": "cm-1"},
            "content": {"request": {"authType": "dfs_ephemeral"}},
        },
        wsclient,
    )
    assert handled is True
    assert done.wait(3.0)
    assert seen_urls == [consent_uri, ""]
    # Each POST must receive a distinct fresh XSRF token!
    used_tokens = [hdrs["x-goog-colab-token"] for _, hdrs in post_calls]
    assert used_tokens == ["xsrf-tok-1", "xsrf-tok-2", "xsrf-tok-3"]
    assert sent_replies[0]["content"]["value"] == {
        "type": "colab_reply",
        "colab_msg_id": "cm-1",
    }

    # Explicit confirm_event wakes immediately, surfaces actionable hint if Google still
    # rejects (e.g. unchecked granular consent or wrong browser account), then completes.
    confirm_client = SimpleNamespace(
        colab_domain="https://colab.research.google.com",
        session=Mock(),
    )
    confirm_client.session.get.return_value = SimpleNamespace(
        status_code=200, text='{"token":"fresh-tok"}'
    )
    confirm_evt = threading.Event()
    probe_evt = threading.Event()
    hints = []
    confirm_step = [0]

    def confirm_post(_url, params=None, headers=None, files=None, timeout=15):
        confirm_step[0] += 1
        if confirm_step[0] == 1:
            return SimpleNamespace(
                status_code=200,
                text=json.dumps(
                    {"success": False, "unauthorized_redirect_uri": consent_uri}
                ),
            )
        if confirm_step[0] == 2:
            return SimpleNamespace(status_code=400, text="<html>400</html>")
        return SimpleNamespace(status_code=200, text=json.dumps({"success": True}))

    confirm_client.session.post.side_effect = confirm_post

    def on_url_trigger(u):
        if u:
            confirm_evt.set()

    def on_hint_trigger(h):
        hints.append(h)
        if h:
            probe_evt.set()

    helper_confirm = ColabOneDriveMount(
        confirm_client,
        "gpu-t4-test",
        on_auth_url=on_url_trigger,
        confirm_event=confirm_evt,
        probe_event=probe_evt,
        on_hint=on_hint_trigger,
        account_email="alice@example.com",
        poll_interval=30.0,  # Long poll interval proves events wake up immediately.
    )
    assert helper_confirm.propagate() is True
    assert any("alice@example.com" in h and "勾选全部云盘权限" in h for h in hints)
    assert hints[-1] == ""

    # Already-authorized fast path propagates immediately with fresh dryrun=false XSRF token.
    fast_client = SimpleNamespace(
        colab_domain="https://colab.research.google.com",
        session=Mock(),
    )
    fast_get_dryruns = []
    fast_post_dryruns = []

    def fast_get(_url, params=None, headers=None, timeout=15):
        fast_get_dryruns.append(params["dryrun"])
        return SimpleNamespace(
            status_code=200, text=json.dumps({"token": f"tok-{len(fast_get_dryruns)}"})
        )

    def fast_post(_url, params=None, headers=None, files=None, timeout=15):
        fast_post_dryruns.append((params["dryrun"], headers["x-goog-colab-token"]))
        return SimpleNamespace(status_code=200, text=json.dumps({"success": True}))

    fast_client.session.get.side_effect = fast_get
    fast_client.session.post.side_effect = fast_post
    fast_urls = []
    assert (
        ColabOneDriveMount(
            fast_client, "gpu-t4-test", on_auth_url=fast_urls.append
        ).propagate()
        is True
    )
    assert fast_urls == []
    assert fast_get_dryruns == ["true", "false"]
    assert fast_post_dryruns == [("true", "tok-1"), ("false", "tok-2")]

    # Untrusted redirect URLs must be rejected immediately.
    bad_client = SimpleNamespace(
        colab_domain="https://colab.research.google.com",
        session=Mock(),
    )
    bad_client.session.get.return_value = SimpleNamespace(
        status_code=200, text='{"token":"t"}'
    )
    bad_client.session.post.return_value = SimpleNamespace(
        status_code=200,
        text='{"success":false,"unauthorized_redirect_uri":"https://evil.example/steal"}',
    )
    with pytest.raises(RuntimeError, match="未受信任"):
        ColabOneDriveMount(bad_client, "gpu-t4-test").dryrun_check()

    # Cancellation sends an error colab_reply immediately.
    cancel_evt = threading.Event()
    cancel_evt.set()
    err_replies = []
    err_done = threading.Event()
    wsclient2 = SimpleNamespace(
        session=SimpleNamespace(msg=lambda kind, content: {"kind": kind, "content": content}),
        stdin_channel=SimpleNamespace(
            send=lambda msg: (err_replies.append(msg), err_done.set())
        ),
    )
    helper2 = ColabOneDriveMount(
        client, "gpu-t4-test", cancel_event=cancel_evt, poll_interval=0.05
    )
    helper2.hook(
        {
            "metadata": {"colab_msg_id": "cm-2"},
            "content": {"request": {"authType": "dfs_ephemeral"}},
        },
        wsclient2,
    )
    assert err_done.wait(3.0)
    assert "取消" in err_replies[0]["content"]["value"]["error"]


def test_controller_hybrid_drive_falls_back_to_colab_drivefs_when_no_quota_project(
    tmp_path,
):
    c = ColabOneController(
        Path(__file__).resolve().parents[1],
        vault=MemoryVault(),
        config_dir=tmp_path,
        background=False,
    )
    c.auth = Mock()
    c.auth.has_drive_scope.return_value = True
    c.auth.requires_drive_quota_project.return_value = True
    c.auth.ensure_drive_quota_project.return_value = ("", "gcp_tos_required")
    c.auth.build_rclone_conf.return_value = ""
    c.provider = Mock()
    c.provider.account.return_value = {"logged_in": True, "assignments": 1, "rate": 1.5}
    c.provider.check_colab_drivefs_auth.return_value = (False, "https://accounts.google.com/o/oauth2/v2/auth")
    c.provider.mount_colab_drivefs.return_value = {
        "ok": True,
        "mount_path": "/content/drive/MyDrive",
        "mode": "colab_drivefs",
    }
    c.remote = Mock(
        return_value={
            "connected": True,
            "mode": "colab_drivefs",
            "remote_folder": COLABONE_DRIVE_FOLDER,
        }
    )
    c.refresh_remote = Mock()

    # 1) GPU start does not stall on browser consent when ToS is not accepted.
    c.start()
    c.provider.mount_colab_drivefs.assert_not_called()
    assert "rclone_conf" not in c.provider.start.call_args.kwargs

    # 2) Explicit connect_drive falls back to official Colab DriveFS and attaches workspace.
    info = c.connect_drive()
    c.provider.mount_colab_drivefs.assert_called_once()
    assert info["mode"] == "colab_drivefs"
    assert c.snapshot()["auth_url"] == ""

    # 3) drive.confirm / drive.probe / drive.cancel work non-blockingly; voluntary cancel produces no error toast.
    c.drive_confirm.clear()
    c.drive_probe.clear()
    assert c.command("drive.confirm", {}) == {"ok": True}
    assert c.drive_confirm.is_set()
    assert c.command("drive.probe", {}) == {"ok": True}
    assert c.drive_probe.is_set()
    c.update(auth_url="https://accounts.google.com/o/oauth2/v2/auth", drive_auth_hint="hint")
    assert c.command("drive.cancel", {}) == {"ok": True}
    assert c.drive_cancel.is_set()
    assert c.snapshot()["auth_url"] == ""
    assert c.snapshot()["drive_auth_hint"] == ""

    def cancelled_mount(**kwargs):
        kwargs["cancel_event"].set()
        raise RuntimeError("已取消 Google 云盘挂载")

    c.provider.mount_colab_drivefs.side_effect = cancelled_mount
    c.command("drive.connect", {})
    import time as _t

    deadline = _t.monotonic() + 3.0
    while c.snapshot()["operation"] is not None and _t.monotonic() < deadline:
        _t.sleep(0.02)
    assert c.snapshot()["operation"] is None
    assert c.snapshot()["error"] == ""


def test_provider_rpc_retries_idempotent_on_transient_disconnect_only(monkeypatch):
    from datetime import datetime, timedelta, timezone
    from bridge.provider import ColabOneProvider

    vault = MemoryVault()
    p = ColabOneProvider(ColabOneAuth(vault), vault, Path(__file__).resolve().parents[1])
    vault.set(
        "runtime_session",
        {
            "name": "colabone",
            "endpoint": "ep1",
            "token": "tok-valid",
            "url": "https://runtime.invalid",
            "token_expires_at": (datetime.now(timezone.utc) + timedelta(minutes=50)).isoformat(),
        },
    )

    calls = []

    def fake_execute(_session, code, timeout=75):
        calls.append((code, timeout))
        if len(calls) == 1:
            raise RuntimeError("Connection was lost.")
        return [{"text": 'COLABONE_RPC={"ok":true,"value":{"connected":true}}\n'}]

    monkeypatch.setattr(p, "execute", fake_execute)
    monkeypatch.setattr(p, "close_kernel_channel", Mock())

    # Idempotent drive/connect retries once cleanly after transient disconnect.
    res = p.rpc("POST", "drive/connect", timeout=180)
    assert res == {"connected": True}
    assert len(calls) == 2
    assert calls[0][1] == 180

    # Non-idempotent mutation must NOT retry after transient disconnect.
    calls.clear()
    monkeypatch.setattr(
        p, "execute", Mock(side_effect=RuntimeError("Connection was lost."))
    )
    with pytest.raises(RuntimeError, match="Connection was lost"):
        p.rpc("POST", "models/m1/download")
    assert p.execute.call_count == 1


def test_ensure_drive_quota_project_retries_409_and_enable_propagation(monkeypatch):
    vault = MemoryVault()
    vault.set(
        "colab",
        {
            "token": "access-tok",
            "refresh_token": "refresh-tok",
            "client_id": "764086051850-6qr4p.apps.googleusercontent.com",
            "client_secret": "csecret",
            "expiry": "2099-01-01T00:00:00Z",
            "scopes": [
                "https://www.googleapis.com/auth/cloud-platform",
                "https://www.googleapis.com/auth/drive.file",
            ],
        },
    )
    vault.set("google_profile", {"sub": "user-409", "email": "user409@example.com"})
    auth = ColabOneAuth(vault)

    assert auth.drive_quota_status() == {
        "needs_quota": True,
        "ready": False,
        "project_id": "",
        "reason": "",
    }

    fake_session = Mock()
    fake_session.get.side_effect = [
        SimpleNamespace(status_code=200, json=lambda: {}),
        SimpleNamespace(status_code=200, json=lambda: {"done": True}),
    ]
    # First create returns 409 ALREADY_EXISTS -> retries deterministic v2 ID.
    # First :enable returns 404 (propagation lag) -> retries and succeeds.
    fake_session.post.side_effect = [
        SimpleNamespace(status_code=409, text="ALREADY_EXISTS", json=lambda: {}),
        SimpleNamespace(
            status_code=200, text="", json=lambda: {"name": "operations/create.v2"}
        ),
        SimpleNamespace(status_code=404, text="not found", json=lambda: {}),
        SimpleNamespace(status_code=200, text="", json=lambda: {"done": True}),
    ]
    monkeypatch.setattr("bridge.auth.AuthorizedSession", lambda _c: fake_session)
    monkeypatch.setattr("bridge.auth.time.sleep", lambda _: None)

    pid, reason = auth.ensure_drive_quota_project(force_refresh=True)
    assert pid.startswith("colabone-drive-")
    assert reason == "created_colabone_project"
    assert vault.get("cloud_project") == pid
    assert auth.drive_quota_status() == {
        "needs_quota": True,
        "ready": True,
        "project_id": pid,
        "reason": "ready",
    }


def test_connect_drive_bypasses_negative_cache_and_setup_headless_drive(
    tmp_path, monkeypatch
):
    import time as _t

    vault = MemoryVault()
    vault.set(
        "colab",
        {
            "token": "valid-tok",
            "refresh_token": "refresh-tok",
            "client_id": "764086051850-6qr4p.apps.googleusercontent.com",
            "client_secret": "csec",
            "expiry": "2099-01-01T00:00:00Z",
            "scopes": [
                "https://www.googleapis.com/auth/cloud-platform",
                "https://www.googleapis.com/auth/drive.file",
            ],
        },
    )
    c = ColabOneController(
        Path(__file__).resolve().parents[1],
        vault=vault,
        config_dir=tmp_path,
        background=False,
    )
    c.auth.credentials = Mock(
        return_value=SimpleNamespace(
            to_json=lambda: json.dumps(vault.get("colab")),
            token="valid-tok",
            refresh_token="refresh-tok",
        )
    )

    seen_force = []

    def fake_ensure(on_log=None, force_refresh=False):
        seen_force.append(force_refresh)
        if len(seen_force) == 1:
            with c.auth.lock:
                c.auth._quota_probe_cache = (_t.monotonic(), "", "gcp_tos_required")
            return "", "gcp_tos_required"
        vault.set("cloud_project", "colabone-drive-ready12345")
        with c.auth.lock:
            c.auth._quota_probe_cache = (
                _t.monotonic(),
                "colabone-drive-ready12345",
                "created_colabone_project",
            )
        return "colabone-drive-ready12345", "created_colabone_project"

    monkeypatch.setattr(c.auth, "ensure_drive_quota_project", fake_ensure)

    # 1) drive.setup_headless surfaces actionable ToS error and updates setup reason.
    c.command("drive.setup_headless", {})
    deadline = _t.monotonic() + 3.0
    while c.snapshot()["operation"] is not None and _t.monotonic() < deadline:
        _t.sleep(0.02)
    snap1 = c.snapshot()
    assert seen_force == [True]
    assert snap1["setup"]["drive_headless"] is False
    assert snap1["setup"]["drive_quota_reason"] == "gcp_tos_required"
    assert "Google Cloud" in snap1["error"]

    # 2) Explicit connect_drive passes force_refresh=True when cloud_project is not cached,
    #    immediately picking up newly accepted ToS without waiting for negative cache TTL.
    c.provider = Mock()
    c.remote = Mock(
        return_value={
            "connected": True,
            "mode": "mount",
            "remote_folder": COLABONE_DRIVE_FOLDER,
        }
    )
    c.refresh_remote = Mock()

    info = c.connect_drive()
    assert seen_force == [True, True]
    assert info["mode"] == "mount"
    assert c.snapshot()["setup"]["drive_headless"] is True
    assert c.snapshot()["setup"]["drive_quota_reason"] == "ready"

    # 3) Once cloud_project is cached in vault, subsequent connect_drive does not force re-probe.
    c.connect_drive()
    assert seen_force == [True, True, False]
