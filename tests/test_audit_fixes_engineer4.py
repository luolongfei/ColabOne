"""Offline regression tests for Fix Engineer 4 (Auditors 8, 9, and 10)."""

from __future__ import annotations

import ast
import asyncio
import io
import json
import os
import queue
import shutil
import socket
import stat
import subprocess
import sys
import tarfile
import threading
import time
import zipfile
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock

import httpx
import pytest
import requests

ROOT = Path(__file__).resolve().parents[1]


# ---------------------------------------------------------------------------
# 1. Unified Subprocess Env Sanitizer & Comfy Proxy / Packs Hardening
# ---------------------------------------------------------------------------


def test_unified_subprocess_env_sanitizer_across_modules(monkeypatch, tmp_path):
    from common.files import colabone_sanitized_subprocess_env
    from server import (
        comfy_packs,
        comfyui,
        digital_human,
        drive,
        live_server,
        live_worker_tts,
        live_worker_visual,
        runtime as runtime_mod,
        tts_engines,
        tunnel,
    )

    secrets = {
        "COLABONE_REMOTE_CONFIG": "/content/secret.json",
        "HF_TOKEN": "hf_secret_1",
        "HUGGING_FACE_HUB_TOKEN": "hf_secret_2",
        "HUGGINGFACE_HUB_TOKEN": "hf_secret_3",
        "HF_API_TOKEN": "hf_secret_4",
        "OPENAI_API_KEY": "sk-secret-5",
        "TUNNEL_TOKEN": "tun-secret-6",
        "TUNNEL_ORIGIN_CERT": "/tmp/cert.pem",
        "RCLONE_CONFIG_PASS": "rclone-pass-7",
        "RCLONE_CONFIG_GDRIVE_TOKEN": "rclone-tok-8",
        "COLABONE_BENIGN_TEST_VAR": "keep-me",
    }
    for k, v in secrets.items():
        monkeypatch.setenv(k, v)

    env_samples = {
        "common": colabone_sanitized_subprocess_env(),
        "runtime": runtime_mod._sanitized_subprocess_env(),
        "comfyui": comfyui._sanitized_subprocess_env(),
        "comfy_packs": comfy_packs._sanitized_subprocess_env(),
        "tts_engines": tts_engines._sanitized_tts_env(),
        "digital_human": digital_human._sanitized_subprocess_env(),
        "live_visual": live_worker_visual._sanitized_visual_subprocess_env(),
        "live_server": live_server._sanitized_live_subprocess_env(),
        "tunnel": tunnel._sanitized_tunnel_env(),
        "drive": drive._sanitized_rclone_env(),
        "live_tts_worker": live_worker_tts.ColabOneResidentTtsWorker(
            "qwen3_tts", model_dir=tmp_path
        ).build_worker_env(),
    }

    stripped_keys = [k for k in secrets if k != "COLABONE_BENIGN_TEST_VAR"]
    for label, env in env_samples.items():
        for k in stripped_keys:
            assert k not in env, f"{label} leaked sensitive env var {k}"
        assert env.get("COLABONE_BENIGN_TEST_VAR") == "keep-me", f"{label} dropped benign var"


def test_comfy_packs_invoke_run_cmd_compat_strips_pip_vars_when_env_none(monkeypatch, tmp_path):
    from server import comfy_packs

    monkeypatch.setenv("PIP_USER", "1")
    monkeypatch.setenv("PIP_TARGET", "/tmp/evil")
    monkeypatch.setenv("PYTHONPATH", "/tmp/evil_py")
    monkeypatch.setenv("RCLONE_CONFIG_PASS", "secret-rclone")
    monkeypatch.setenv("TUNNEL_METRICS", "secret-tunnel")

    captured: dict[str, object] = {}

    def _fake_popen(args, **kwargs):
        captured["env"] = dict(kwargs.get("env") or {})
        proc = Mock()
        proc.poll.return_value = 0
        proc.returncode = 0
        proc.pid = 0
        return proc

    monkeypatch.setattr(comfy_packs.subprocess, "Popen", _fake_popen)
    task = SimpleNamespace(check=lambda: None, cancel_hook=None)
    comfy_packs._invoke_run_cmd_compat(
        ["echo", "ok"],
        task,
        tmp_path / "cmd.log",
        timeout=10,
        env=None,
        run_cmd_fn=None,
    )
    env = captured["env"]
    for bad in ("PIP_USER", "PIP_TARGET", "PYTHONPATH", "RCLONE_CONFIG_PASS", "TUNNEL_METRICS"):
        assert bad not in env


def test_comfy_proxy_tunnel_hardening_and_stdout_close(monkeypatch, tmp_path):
    from server import comfy_proxy

    monkeypatch.setenv("HF_TOKEN", "hf_leak")
    monkeypatch.setenv("RCLONE_CONFIG_PASS", "rclone_leak")
    monkeypatch.setenv("TUNNEL_CUSTOM_SECRET", "tun_leak")
    monkeypatch.setenv("TUNNEL_TOKEN", "tun_env_leak")

    captured: dict[str, object] = {}
    closed_streams: list[str] = []

    class _FakeStream:
        def __init__(self, name: str):
            self._name = name

        def __iter__(self):
            return iter(())

        def close(self):
            closed_streams.append(self._name)

    class _FakeProc:
        def __init__(self):
            self.pid = 999999
            self.stdout = _FakeStream("stdout")
            self.stderr = _FakeStream("stderr")
            self._polled = False

        def poll(self):
            return 0 if self._polled else None

        def terminate(self):
            self._polled = True

        def wait(self, timeout=None):
            self._polled = True
            return 0

        def kill(self):
            self._polled = True

    fake_proc = _FakeProc()

    def _fake_popen(args, **kwargs):
        captured["kwargs"] = kwargs
        # Trigger cancel immediately after Popen so _run_tunnel enters cleanup/terminate.
        proxy._tunnel_cancel.set()
        return fake_proc

    monkeypatch.setattr(comfy_proxy.subprocess, "Popen", _fake_popen)
    monkeypatch.setattr(
        comfy_proxy, "_call_ensure_binary", lambda *a, **kw: Path("/usr/bin/cloudflared")
    )
    fake_rt = SimpleNamespace(
        root=tmp_path,
        catalog=ROOT / "catalog",
        note=lambda *a, **k: None,
        lock=threading.RLock(),
    )
    proxy = comfy_proxy.ColabOneComfyCanvasGateway(runtime=fake_rt)
    proxy._run_tunnel(
        SimpleNamespace(domain="https://canvas.example.com", tunnel_token="tok_1234567890"),
        proxy_port=8189,
    )
    kw = captured["kwargs"]
    assert kw.get("stdin") == subprocess.DEVNULL
    if os.name == "posix":
        assert kw.get("start_new_session") is True
    env = kw.get("env") or {}
    for bad in ("HF_TOKEN", "RCLONE_CONFIG_PASS", "TUNNEL_CUSTOM_SECRET", "TUNNEL_TOKEN"):
        assert bad not in env
    assert "stdout" in closed_streams


# ---------------------------------------------------------------------------
# 2. Tunnel IPv4-first AI_ADDRCONFIG, Multi-IP Probe Failover & Symlink Checks
# ---------------------------------------------------------------------------


def test_tunnel_public_addresses_ipv4_first_and_probe_failover(monkeypatch, tmp_path):
    from server import tunnel

    seen_flags: list[int] = []

    def _fake_getaddrinfo(host, port, type=0, flags=0):
        seen_flags.append(flags)
        return [
            (socket.AF_INET6, socket.SOCK_STREAM, 6, "", ("2606:4700::1", 443, 0, 0)),
            (socket.AF_INET, socket.SOCK_STREAM, 6, "", ("104.21.1.2", 443)),
            (socket.AF_INET, socket.SOCK_STREAM, 6, "", ("104.21.1.3", 443)),
        ]

    monkeypatch.setattr(tunnel.socket, "getaddrinfo", _fake_getaddrinfo)
    addrs = tunnel.public_addresses("https://example.com")
    assert seen_flags == [getattr(socket, "AI_ADDRCONFIG", 0)]
    assert addrs == ["104.21.1.2", "104.21.1.3", "2606:4700::1"]

    # Fallback when AI_ADDRCONFIG fails with gaierror.
    calls = {"n": 0}

    def _flaky_getaddrinfo(host, port, type=0, flags=0):
        calls["n"] += 1
        if flags:
            raise socket.gaierror("AI_ADDRCONFIG failed")
        return [(socket.AF_INET, socket.SOCK_STREAM, 6, "", ("104.21.1.9", 443))]

    monkeypatch.setattr(tunnel.socket, "getaddrinfo", _flaky_getaddrinfo)
    assert tunnel.public_addresses("https://example.com") == ["104.21.1.9"]
    assert calls["n"] == 2

    # Multi-IP failover in probe().
    attempted_ips: list[str] = []

    class _FakeResp:
        def __init__(self, status_code: int, body: bytes):
            self.status_code = status_code
            self._body = body

        def __enter__(self):
            return self

        def __exit__(self, *args):
            return False

        def iter_content(self, chunk_size=16384):
            yield self._body

    class _FakeSession:
        def __init__(self):
            self.pinned_ip = ""

        def __enter__(self):
            return self

        def __exit__(self, *args):
            return False

        def mount(self, prefix, adapter):
            self.pinned_ip = adapter.pinned_ip

        def get(self, url, headers=None, timeout=None, allow_redirects=False, stream=False):
            if url.endswith("/health"):
                attempted_ips.append(self.pinned_ip)
                if self.pinned_ip == "104.21.1.2":
                    raise requests.ConnectionError("unreachable first edge")
                return _FakeResp(
                    200,
                    json.dumps({"service": "ColabOne inference", "instance": "inst-1"}).encode("utf-8"),
                )
            if headers and headers.get("Authorization") == "Bearer test-key-123":
                return _FakeResp(200, json.dumps({"object": "list", "data": []}).encode("utf-8"))
            return _FakeResp(401, b"{}")

    monkeypatch.setattr(
        tunnel, "public_addresses", lambda origin: ["104.21.1.2", "104.21.1.3"]
    )
    monkeypatch.setattr(tunnel.requests, "Session", _FakeSession)

    fake_gw = SimpleNamespace(
        instance="inst-1",
        key="test-key-123",
        lock=threading.RLock(),
        runtime=SimpleNamespace(root=tmp_path, catalog=ROOT / "catalog"),
        note=lambda *a, **k: None,
    )
    t = tunnel.ColabOneTunnel(fake_gw)
    assert t.probe("https://api.example.com", timeout_seconds=5) is True
    assert attempted_ips == ["104.21.1.2", "104.21.1.3"]

    # ensure_binary symlink guards on bin/ and cloudflared target.
    monkeypatch.setattr(tunnel.platform, "system", lambda: "Linux")
    monkeypatch.setattr(tunnel.platform, "machine", lambda: "x86_64")
    sym_root = tmp_path / "sym_root"
    sym_root.mkdir()
    outside = tmp_path / "outside"
    outside.mkdir()
    (sym_root / "bin").symlink_to(outside, target_is_directory=True)
    with pytest.raises(ValueError, match="符号链接"):
        tunnel.ensure_binary(sym_root, ROOT / "catalog", threading.Event(), lambda *a: None)


# ---------------------------------------------------------------------------
# 3. Runtime: _comfy_clip_runner, Auto-Release Reset, Sync Trash, engine.log
# ---------------------------------------------------------------------------


def test_runtime_comfy_clip_runner_dotted_fields_and_no_store_pollution(tmp_path, monkeypatch):
    import base64
    from PIL import Image

    from server.runtime import ColabOneRuntime

    rt = ColabOneRuntime(root=tmp_path / "rt", catalog=ROOT / "catalog")
    try:
        wf_id = "live-avatar-motion"
        rt.comfy.store.save_workflow(
            {
                "id": wf_id,
                "name": "Clip Test",
                "workflow": {
                    "10": {"class_type": "LoadImage", "inputs": {"image": "old.png"}},
                    "11": {"class_type": "CLIPTextEncode", "inputs": {"text": "old"}},
                    "12": {
                        "class_type": "EmptyLatentImage",
                        "inputs": {"width": 512, "height": 512, "batch_size": 1},
                    },
                    "13": {
                        "class_type": "CreateVideo",
                        "inputs": {"fps": 16},
                    },
                },
            }
        )

        captured_prompt: dict[str, object] = {}
        captured_kwargs: dict[str, object] = {}

        def _fake_exec(task, wf, prompt, **kwargs):
            captured_prompt["prompt"] = prompt
            captured_kwargs.update(kwargs)
            out_dir = Path(kwargs["output_dir"])
            out_dir.mkdir(parents=True, exist_ok=True)
            vid = out_dir / "clip.mp4"
            vid.write_bytes(b"\x00\x00\x00\x18ftypmp42fake")
            return [{"path": str(vid), "filename": "clip.mp4", "kind": "video"}]

        monkeypatch.setattr(rt.comfy, "_execute_single_prompt", _fake_exec)
        monkeypatch.setattr(
            rt.comfy,
            "_upload_input_file",
            lambda fname, fpath: f"uploaded_{fname}",
        )
        monkeypatch.setattr(rt.comfy, "is_running", lambda: True)
        monkeypatch.setattr(rt.comfy, "start_server", lambda *a, **kw: None)

        buf = io.BytesIO()
        Image.new("RGB", (32, 32), (80, 120, 200)).save(buf, format="PNG")
        portrait_b64 = base64.b64encode(buf.getvalue()).decode("ascii")

        snap = rt.build_live_avatar_pack(
            {
                "avatar_id": "av_clip_test",
                "name": "ClipTest",
                "portrait_b64": portrait_b64,
                "portrait_filename": "p.png",
                "actions": ["idle_loop"],
                "dance_ids": [],
                "fps": 25,
                "resolution": "720x1280",
            }
        )
        task = rt.tasks[snap["id"]]
        deadline = time.monotonic() + 5.0
        while task.state == "running" and time.monotonic() < deadline:
            time.sleep(0.02)
        assert task.state == "completed", task.error
        assert captured_kwargs["save_to_store"] is False
        prompt = captured_prompt["prompt"]
        assert prompt["10"]["inputs"]["image"].startswith("uploaded_")
        assert prompt["11"]["inputs"]["text"] != "old"
        assert prompt["12"]["inputs"]["width"] == 720
        assert prompt["12"]["inputs"]["height"] == 1280
        assert prompt["13"]["inputs"]["fps"] == 25
        assert list(rt.comfy.store.inputs_dir.glob("*")) == []
        assert rt.comfy.store.list_outputs() == []
    finally:
        rt.stopping.set()
        rt.close_http_sessions()


def test_runtime_auto_release_resets_on_late_cancellation_and_sync_trash(tmp_path, monkeypatch):
    from server.runtime import ColabOneCancelledError, ColabOneRuntime

    rt = ColabOneRuntime(root=tmp_path / "rt", catalog=ROOT / "catalog")
    try:
        entered = threading.Event()
        release_wait = threading.Event()

        def _body(task):
            with rt.lock:
                rt._pending_workflow_auto_release = {
                    "task_id": task.id,
                    "workflow_id": "wf-1",
                    "workflow_name": "WF",
                }
                rt._workflow_auto_release = {
                    "enabled": True,
                    "state": "armed",
                    "task_id": task.id,
                    "message": "armed",
                }
            entered.set()
            release_wait.wait(timeout=3.0)
            raise ColabOneCancelledError("cancelled late")

        snap = rt.submit("comfyui_run", "wf-1", _body)
        assert entered.wait(timeout=3.0)
        release_wait.set()
        task = rt.tasks[snap["id"]]
        deadline = time.monotonic() + 3.0
        while task.state == "running" and time.monotonic() < deadline:
            time.sleep(0.02)
        assert task.state == "cancelled"
        assert rt._pending_workflow_auto_release is False
        assert rt._workflow_auto_release["enabled"] is False
        assert rt._workflow_auto_release["state"] == "skipped"

        # Sync Drive trash acquires sync_lock, records reconciled tombstones, and workflow save forgets it.
        lock_states: list[bool] = []

        def _fake_remove_wf(wid):
            lock_states.append(rt.sync_lock.locked())
            return True

        monkeypatch.setattr(rt.drive, "remove_remote_workflow", _fake_remove_wf)
        rt.comfy.store.save_workflow(
            {
                "id": "wf-del-1",
                "name": "Delete Test",
                "workflow": {"1": {"class_type": "CLIPTextEncode", "inputs": {"text": "hi"}}},
            }
        )
        rt.comfy.store.delete_workflow("wf-del-1")
        assert lock_states == [True]
        assert any(
            entry[0] == "workflow" and entry[1] == "wf-del-1"
            for entry in rt._reconciled_tombstones
        )

        rt.comfy.store.save_workflow(
            {
                "id": "wf-del-1",
                "name": "Re-created Test",
                "workflow": {"1": {"class_type": "CLIPTextEncode", "inputs": {"text": "hi2"}}},
            }
        )
        assert not any(
            entry[0] == "workflow" and entry[1] == "wf-del-1"
            for entry in rt._reconciled_tombstones
        )

        # _load refuses symlink engine.log.
        victim = tmp_path / "victim.log"
        victim.write_text("do-not-clobber", encoding="utf-8")
        (rt.root / "engine.log").symlink_to(victim)
        spec = next(iter(rt.models.values()))
        for f in spec.files:
            p = rt.models_dir / spec.id / f.path
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_bytes(b"x")
        monkeypatch.setattr(rt, "installed", lambda s: True)
        monkeypatch.setattr(rt, "gpu_info", lambda: {"name": "T4", "total_mib": 16384, "used_mib": 0})
        monkeypatch.setattr(rt, "prepare_engine", lambda t: rt.root / "llama-server")
        monkeypatch.setattr(rt.comfy, "stop_server", lambda: None)
        with pytest.raises(ValueError, match="符号链接"):
            rt._load(
                SimpleNamespace(check=lambda: None, update=lambda **kw: None, note=lambda *a, **k: None),
                spec,
            )
        assert victim.read_text(encoding="utf-8") == "do-not-clobber"
    finally:
        rt.stopping.set()
        rt.close_http_sessions()


# ---------------------------------------------------------------------------
# 4. Drive: Trash ENOENT & Symlinks, Export Lock, Private File Hardening
# ---------------------------------------------------------------------------


def test_drive_trash_enoent_symlinks_export_lock_and_private_bytes(tmp_path, monkeypatch):
    from server.drive import ColabOneDrive, _write_private_bytes

    secret_target = tmp_path / "secret.bin"
    secret_target.write_bytes(b"old")
    sym = tmp_path / "sym.conf"
    sym.symlink_to(secret_target)
    with pytest.raises(ValueError, match="符号链接"):
        _write_private_bytes(sym, b"new")
    assert secret_target.read_bytes() == b"old"

    priv = tmp_path / "priv.conf"
    _write_private_bytes(priv, b"token=abc")
    if os.name == "posix":
        assert stat.S_IMODE(priv.stat().st_mode) == 0o600

    mount = tmp_path / "MyDrive"
    ws = mount / "ColabOne_Drive" / "workspace"
    (ws / "workflows").mkdir(parents=True)
    wf_file = ws / "workflows" / "wf1.json"
    wf_file.write_text("{}", encoding="utf-8")

    drv = ColabOneDrive(
        root=tmp_path / "state",
        catalog=ROOT / "catalog",
        mount_root=mount,
    )
    drv.mode = "colab_drivefs"
    drv.connected = True
    monkeypatch.setattr(drv, "available", lambda: True)

    orig_replace = Path.replace

    def _enoent_replace(self, target):
        if self == wf_file:
            raise FileNotFoundError("vanished concurrently")
        return orig_replace(self, target)

    monkeypatch.setattr(Path, "replace", _enoent_replace)
    assert drv.remove_remote_workflow("wf1") is True

    bad_mount = tmp_path / "BadDrive"
    bad_mount.mkdir()
    outside = tmp_path / "outside_drive"
    outside.mkdir()
    (bad_mount / "ColabOne_Drive").symlink_to(outside, target_is_directory=True)
    drv.mount_root = bad_mount
    with pytest.raises(ValueError, match="符号链接"):
        drv.remove_remote_workflow("wf1")

    drv.mount_root = mount
    monkeypatch.setattr(drv, "is_colab_drivefs_mounted", lambda: True)
    src_a = tmp_path / "a.png"
    src_b = tmp_path / "b.png"
    src_a.write_bytes(b"CONTENT-A")
    src_b.write_bytes(b"CONTENT-B")

    results: list[dict] = []
    errors: list[Exception] = []
    barrier = threading.Barrier(2)

    def _worker(src_path: Path, oid: str):
        try:
            barrier.wait(timeout=3.0)
            res = drv.export_output_file(
                src_path,
                filename="01_正面站姿.png",
                output_id=oid,
                extension="png",
            )
            results.append(res)
        except Exception as exc:
            errors.append(exc)

    t1 = threading.Thread(target=_worker, args=(src_a, "out-a"))
    t2 = threading.Thread(target=_worker, args=(src_b, "out-b"))
    t1.start()
    t2.start()
    t1.join(timeout=5.0)
    t2.join(timeout=5.0)
    assert errors == []
    assert len(results) == 2
    out_files = [
        p for p in (mount / "ColabOne_Drive" / "outputs").iterdir() if p.is_file()
    ]
    assert len(out_files) == 2
    assert {p.read_bytes() for p in out_files} == {b"CONTENT-A", b"CONTENT-B"}


# ---------------------------------------------------------------------------
# 5. VM Update Hardening (Auditor 8 M8)
# ---------------------------------------------------------------------------


def _load_colabone_update_fn(content_root: Path):
    """Load colabone_update() with /content redirected to a temporary directory."""
    vm_update_path = ROOT / "server" / "vm_update.py"
    source = vm_update_path.read_text(encoding="utf-8")
    patched = source.replace("/content", content_root.as_posix())
    tree = ast.parse(patched, filename=str(vm_update_path))
    if tree.body and isinstance(tree.body[-1], ast.Expr):
        tree.body.pop()
    ns: dict[str, object] = {"__name__": "server.vm_update_test_load"}
    exec(compile(tree, str(vm_update_path), "exec"), ns)
    return ns["colabone_update"], source


def test_vm_update_hardening_guards_and_rollback(tmp_path, monkeypatch):
    content_dir = tmp_path / "content"
    content_dir.mkdir()
    colabone_update, src_text = _load_colabone_update_fn(content_dir)
    assert "_has_server_app_cmdline" in src_text
    assert "_read_proc_state_and_starttime" in src_text
    assert 'status_obj.get("state") == "releasing"' in src_text
    assert "time.monotonic()" in src_text
    assert "stat.S_ISREG" in src_text
    assert "launch_err or tail" in src_text

    # Prepare boot config, current service, and valid zip update bundle.
    (content_dir / "colabone-boot.json").write_text(
        json.dumps({"token": "tok123", "deadline": 0, "runtime_id": "rt-test-1"}),
        encoding="utf-8",
    )
    service_dir = content_dir / "colabone"
    (service_dir / "server").mkdir(parents=True)
    (service_dir / "server" / "app.py").write_text("# old app\n", encoding="utf-8")
    (service_dir / "server" / "requirements.txt").write_text("# same reqs\n", encoding="utf-8")

    bundle = content_dir / "colabone-update.zip"
    with zipfile.ZipFile(bundle, "w") as zf:
        zf.writestr("colabone/server/app.py", "# new app\n")
        zf.writestr("colabone/server/requirements.txt", "# same reqs\n")

    class _FakeResp:
        def __init__(self, payload: dict):
            self.status = 200
            self._data = json.dumps(payload).encode("utf-8")

        def __enter__(self):
            return self

        def __exit__(self, *a):
            return False

        def read(self):
            return self._data

        def close(self):
            pass

    # Case 1: Abort when /api/v1/status reports state == "releasing".
    class _ReleasingOpener:
        def open(self, req, timeout=None):
            return _FakeResp({"state": "releasing", "tasks": []})

    monkeypatch.setattr("urllib.request.build_opener", lambda *a, **kw: _ReleasingOpener())
    with pytest.raises(RuntimeError, match="正在释放"):
        colabone_update()
    assert (service_dir / "server" / "app.py").read_text(encoding="utf-8") == "# old app\n"

    # Case 2: Rollback when launch() raises OSError (using a fake /proc tree for PID/starttime checks).
    fake_proc = tmp_path / "fake_proc"
    fake_pid_dir = fake_proc / "424242"
    fake_pid_dir.mkdir(parents=True)
    (fake_pid_dir / "cmdline").write_bytes(b"python3\x00-m\x00server.app\x00")
    (fake_pid_dir / "environ").write_bytes(
        f"COLABONE_REMOTE_CONFIG={content_dir.as_posix()}/colabone-boot.json\x00".encode("utf-8")
    )
    # /proc/<pid>/stat with parens inside comm and starttime=98765 at field 22 (index 19 after comm).
    stat_fields = ["S"] + ["0"] * 18 + ["98765"] + ["0"] * 5
    (fake_pid_dir / "stat").write_text(
        f"424242 (python (srv)) {' '.join(stat_fields)}\n", encoding="utf-8"
    )
    # Also add a bogus process with non-consecutive -m and server.app that must be ignored.
    bogus_dir = fake_proc / "424243"
    bogus_dir.mkdir(parents=True)
    (bogus_dir / "cmdline").write_bytes(b"python3\x00-m\x00other\x00server.app\x00")
    (bogus_dir / "environ").write_bytes(
        f"COLABONE_REMOTE_CONFIG={content_dir.as_posix()}/colabone-boot.json\x00".encode("utf-8")
    )
    (bogus_dir / "stat").write_text(
        f"424243 (other) {' '.join(stat_fields)}\n", encoding="utf-8"
    )

    killed_pids: list[int] = []

    def _fake_kill(pid: int, sig: int):
        killed_pids.append(pid)
        # Simulate clean exit after SIGTERM by removing the fake stat file.
        if pid == 424242:
            (fake_pid_dir / "stat").unlink(missing_ok=True)

    seen_timeouts: list[object] = []

    class _ReadyOpener:
        def open(self, req, timeout=None):
            url = req.full_url if hasattr(req, "full_url") else str(req)
            seen_timeouts.append((url, timeout))
            if url.endswith("/api/v1/status"):
                return _FakeResp({"state": "ready", "tasks": []})
            return _FakeResp({"ok": True})

    launch_attempts = {"n": 0}

    def _flaky_popen(*a, **kw):
        launch_attempts["n"] += 1
        if launch_attempts["n"] == 1:
            raise OSError("exec failed")
        proc = Mock(pid=12345)
        proc.poll.return_value = None
        return proc

    vm_src = (ROOT / "server" / "vm_update.py").read_text(encoding="utf-8")
    vm_patched = vm_src.replace("/content", content_dir.as_posix()).replace(
        "/proc", fake_proc.as_posix()
    )
    vm_tree = ast.parse(vm_patched, filename="vm_update_test.py")
    if vm_tree.body and isinstance(vm_tree.body[-1], ast.Expr):
        vm_tree.body.pop()
    vm_ns: dict[str, object] = {"__name__": "server.vm_update_test_load2"}
    exec(compile(vm_tree, "vm_update_test.py", "exec"), vm_ns)
    colabone_update_proc = vm_ns["colabone_update"]

    monkeypatch.setattr("urllib.request.build_opener", lambda *a, **kw: _ReadyOpener())
    monkeypatch.setattr("subprocess.Popen", _flaky_popen)
    monkeypatch.setattr("os.kill", _fake_kill)

    with pytest.raises(RuntimeError, match="已恢复旧服务"):
        colabone_update_proc()
    assert killed_pids == [424242]
    assert launch_attempts["n"] == 2
    assert (service_dir / "server" / "app.py").read_text(encoding="utf-8") == "# old app\n"
    assert any(url.endswith("/health") and t == 5 for url, t in seen_timeouts)


# ---------------------------------------------------------------------------
# 6. OpenAI Compat: Compaction Cumulative Cap & Linear-Time Merge (Auditor 9 H1)
# ---------------------------------------------------------------------------


def test_openai_compat_compaction_cumulative_cap_and_linear_merge():
    from server.openai_compat import (
        MAX_COMPACTION_DECOMPRESSED_BYTES,
        MAX_TOTAL_COMPACTION_DECOMPRESSED_BYTES,
        encode_compaction_content,
        normalize_responses_request,
        response_items_to_chat_messages,
    )

    # Each item stays below single-item cap, while total exceeds cumulative 1 MiB cap.
    per_item_len = min(300 * 1024, MAX_COMPACTION_DECOMPRESSED_BYTES - 1024)
    chunk_text = "A" * per_item_len
    enc = encode_compaction_content(chunk_text)
    count = (MAX_TOTAL_COMPACTION_DECOMPRESSED_BYTES // per_item_len) + 2
    items = [{"type": "compaction", "encrypted_content": enc} for _ in range(count)]

    with pytest.raises(ValueError, match="总大小"):
        response_items_to_chat_messages(items)

    with pytest.raises(ValueError, match="总大小"):
        normalize_responses_request({"model": "auto", "input": items}, {})

    # Merging consecutive same-role assistant compaction summaries works cleanly in linear time.
    small_enc = encode_compaction_content("summary-part")
    many_comp = [{"role": "user", "content": "start"}] + [
        {"type": "compaction", "encrypted_content": small_enc} for _ in range(50)
    ]
    merged, _ = response_items_to_chat_messages(many_comp)
    assert len(merged) == 2
    assert merged[1]["role"] == "assistant"
    assert merged[1]["content"].count("summary-part") == 50


# ---------------------------------------------------------------------------
# 7. Public API: BodyLimit Hardening, Stop vs Active Handler, Generic Errors
# ---------------------------------------------------------------------------


def test_public_api_body_limit_health_options_and_timeout():
    from server.public_api import ColabOneBodyLimit

    gw = SimpleNamespace(lock=threading.Lock(), enabled=True, key="secret-key-123")

    async def _ok_app(scope, receive, send):
        await send({"type": "http.response.start", "status": 200, "headers": []})
        await send({"type": "http.response.body", "body": b"ok"})

    mw = ColabOneBodyLimit(_ok_app, gateway=gw, max_bytes=1024)

    async def _run_req(method: str, path: str, *, headers=(), chunks=(b"",), slow=False):
        sent: list[dict] = []
        idx = {"i": 0}

        async def _recv():
            if slow:
                await asyncio.sleep(0.5)
                return {"type": "http.request", "body": b"x", "more_body": True}
            i = idx["i"]
            idx["i"] += 1
            if i < len(chunks):
                return {
                    "type": "http.request",
                    "body": chunks[i],
                    "more_body": i + 1 < len(chunks),
                }
            return {"type": "http.disconnect"}

        async def _send(msg):
            sent.append(msg)

        scope = {
            "type": "http",
            "method": method,
            "path": path,
            "headers": [(k.lower().encode("latin-1"), v.encode("latin-1")) for k, v in headers],
        }
        await mw(scope, _recv, _send)
        return sent[0]["status"]

    assert asyncio.run(_run_req("GET", "/health")) == 200
    assert asyncio.run(_run_req("POST", "/health")) == 401
    assert (
        asyncio.run(
            _run_req(
                "POST",
                "/health",
                headers=[("Authorization", "Bearer secret-key-123"), ("Content-Length", "4")],
                chunks=(b"body",),
            )
        )
        == 400
    )
    assert (
        asyncio.run(
            _run_req(
                "OPTIONS",
                "/v1/chat/completions",
                headers=[("Content-Length", "2")],
                chunks=(b"{}",),
            )
        )
        == 400
    )
    mw._BODY_READ_TIMEOUT_SECONDS = 0.05
    assert (
        asyncio.run(
            _run_req(
                "POST",
                "/v1/chat/completions",
                headers=[("Authorization", "Bearer secret-key-123")],
                slow=True,
            )
        )
        == 408
    )


def test_public_api_stop_preserves_active_handler_payload_and_scrubs_errors(tmp_path):
    from fastapi.testclient import TestClient

    from server.public_api import ColabOnePublicGateway, create_colabone_public_app
    from server.runtime import ColabOneRuntime

    rt = ColabOneRuntime(root=tmp_path / "rt", catalog=ROOT / "catalog")
    try:
        spec = next(
            m for m in rt.models.values() if getattr(m, "kind", "chat") == "chat"
        )
        rt.loaded = spec.id
        gw = ColabOnePublicGateway(rt)
        gw.enabled = True
        gw.key = "sk-topsecret-token-999999"

        fake_task = SimpleNamespace(
            lock=threading.Lock(), state="completed", error="", request_cancel=lambda: None
        )
        slot = {
            "task": fake_task,
            "result": {"ok": True},
            "queue": queue.Queue(),
            "fail_event": None,
            "active_handlers": 1,
        }
        gw.jobs["job-active"] = slot
        gw._active_http_handlers = 1
        gw.tunnel = SimpleNamespace(stop=lambda: None, thread=None)
        gw.stop()
        assert slot["result"] == {"ok": True}
        assert "sk-topsecret-token-999999" in gw._retired_keys
        scrubbed = gw._scrub_error_text("boom sk-topsecret-token-999999 leaked", "推理失败")
        assert "sk-topsecret-token-999999" not in scrubbed

        gw.enabled = True
        gw.key = "sk-live-key-123456"

        def _boom_transport(_req):
            raise ValueError("internal-secret-detail sk-live-key-123456")

        rt.chat_transport = httpx.MockTransport(_boom_transport)
        with TestClient(create_colabone_public_app(gw)) as client:
            resp = client.post(
                "/v1/chat/completions",
                json={"model": spec.id, "messages": [{"role": "user", "content": "hi"}], "stream": False},
                headers={"Authorization": "Bearer sk-live-key-123456"},
            )
            assert resp.status_code == 502
            body = resp.json()
            assert body["error"]["message"] == "推理失败"
            assert "internal-secret-detail" not in resp.text
            assert "sk-live-key-123456" not in resp.text
    finally:
        rt.stopping.set()
        rt.close_http_sessions()


# ---------------------------------------------------------------------------
# 8. TTS Engines: Warm Worker Non-Blocking Close & Reaped PID Guard
# ---------------------------------------------------------------------------


def test_tts_warm_worker_close_non_blocking_and_reaped_pid_guard(monkeypatch, tmp_path):
    from server import tts_engines

    # 1. _terminate_process_group never calls os.killpg when proc is already reaped & PID recycled.
    killpg_calls: list[tuple[int, int]] = []
    monkeypatch.setattr(
        tts_engines.os,
        "killpg",
        lambda pgid, sig: killpg_calls.append((pgid, sig)),
        raising=False,
    )
    monkeypatch.setattr(tts_engines.os, "kill", lambda pid, sig: None)
    reaped = SimpleNamespace(pid=424242, poll=lambda: 0)
    tts_engines._terminate_process_group(reaped, timeout=0.1)
    assert killpg_calls == []

    # 2. ColabOneWarmTtsWorker.close() does not deadlock when _stdin_lock is held.
    worker = tts_engines.ColabOneWarmTtsWorker(
        engine_id="qwen3_tts",
        worker_python=sys.executable,
        worker_script="/tmp/nonexistent.py",
        work_dir=tmp_path,
        env={},
    )
    closed_fds: list[str] = []

    class _Stream:
        def __init__(self, name: str):
            self.name = name
            self.closed = False

        def close(self):
            self.closed = True
            closed_fds.append(self.name)

    fake_proc = SimpleNamespace(
        pid=999998,
        stdin=_Stream("stdin"),
        stdout=_Stream("stdout"),
        stderr=_Stream("stderr"),
        poll=lambda: None,
    )
    terminated: list[object] = []
    monkeypatch.setattr(
        tts_engines, "_terminate_process_group", lambda p, timeout=3.0: terminated.append(p)
    )

    worker.proc = fake_proc
    lock_held = threading.Event()
    release_lock = threading.Event()

    def _hold_stdin_lock():
        with worker._stdin_lock:
            lock_held.set()
            release_lock.wait(timeout=5.0)

    holder = threading.Thread(target=_hold_stdin_lock)
    holder.start()
    assert lock_held.wait(timeout=2.0)
    try:
        t0 = time.monotonic()
        worker.close()
        elapsed = time.monotonic() - t0
    finally:
        release_lock.set()
        holder.join(timeout=2.0)
    assert elapsed < 2.5
    assert terminated == [fake_proc]
    assert "stdin" in closed_fds and "stdout" in closed_fds


# ---------------------------------------------------------------------------
# 9. Live Server: Video Encoder Non-Deadlock Close, fMP4 Safety, FFmpeg Whitelist,
#    LLM Caps, ptt_end State Guard
# ---------------------------------------------------------------------------


def test_live_server_encoder_close_fmp4_no_jpeg_fallback_and_ffmpeg_cmd():
    from server import live_server
    from server.live_worker_visual import ColabOneVisualFrame

    cmd = live_server.ColabOneLiveVideoEncoder.build_ffmpeg_command(
        "libx264",
        320,
        240,
        rtmp_url="rtmp://live.example.com/app/key",
        audio_input="/tmp/audio.pcm",
    )
    wls = [cmd[i + 1] for i, tok in enumerate(cmd) if tok == "-protocol_whitelist"]
    assert wls == ["pipe", "file,pipe", "rtmp,rtmps,tcp,tls,crypto"]

    for bad_audio in ("-evil", "http://169.254.169.254/latest", "pipe:0\nbad"):
        with pytest.raises(ValueError):
            live_server.ColabOneLiveVideoEncoder.build_ffmpeg_command(
                "libx264",
                320,
                240,
                rtmp_url="rtmp://live.example.com/app/key",
                audio_input=bad_audio,
            )

    enc = live_server.ColabOneLiveVideoEncoder(
        width=16, height=16, fps=25, enable_ffmpeg=True
    )
    enc.mode = "libx264"
    write_started = threading.Event()
    unblock_write = threading.Event()

    class _BlockingStdin:
        def write(self, data):
            write_started.set()
            unblock_write.wait(timeout=5.0)
            return len(data)

        def flush(self):
            pass

        def close(self):
            unblock_write.set()

    class _FakeEncProc:
        def __init__(self):
            self.stdin = _BlockingStdin()
            self.stdout = io.BytesIO()
            self.stderr = io.BytesIO()
            self.terminated = False

        def poll(self):
            return 0 if self.terminated else None

        def terminate(self):
            self.terminated = True
            unblock_write.set()

        def wait(self, timeout=None):
            self.terminated = True
            return 0

        def kill(self):
            self.terminated = True
            unblock_write.set()

    fake_proc = _FakeEncProc()
    enc._proc = fake_proc
    enc._stdout_queue = queue.Queue()

    frame = ColabOneVisualFrame(
        width=16,
        height=16,
        rgb_bytes=bytes(16 * 16 * 3),
        pts_ms=0,
        epoch=1,
        action_id="idle_loop",
        engine_id="synthetic",
    )

    unblock_write.set()
    ft, payload = enc.encode_frame(frame)
    assert ft == live_server.COLABONE_LIVE_FRAME_FMP4
    assert payload == b""

    unblock_write.clear()
    enc_thread = threading.Thread(target=enc.encode_frame, args=(frame,))
    enc_thread.start()
    assert write_started.wait(timeout=2.0)

    t0 = time.monotonic()
    enc.close()
    close_elapsed = time.monotonic() - t0
    enc_thread.join(timeout=2.0)
    assert close_elapsed < 2.5
    assert fake_proc.terminated is True


def test_live_server_llm_caps_and_director_ptt_end():
    from server.live_server import ColabOneLiveDirector, stream_llm_completion

    director = ColabOneLiveDirector()
    director.set_state("SPEAKING")
    changed, _ = director.end_listening_if_matching()
    assert changed is False
    assert director.state == "SPEAKING"

    director.set_state("LISTENING")
    changed2, _ = director.end_listening_if_matching()
    assert changed2 is True
    assert director.state == "IDLE"

    huge_err = b"E" * (128 * 1024)
    err_transport = httpx.MockTransport(lambda _r: httpx.Response(500, content=huge_err))

    async def _run_err():
        async with httpx.AsyncClient(transport=err_transport) as client:
            async for _ in stream_llm_completion(
                [{"role": "user", "content": "hi"}],
                llm_mode="local_gguf",
                local_base_url="http://127.0.0.1:8081/v1",
                local_model="test",
                http_client=client,
            ):
                pass

    with pytest.raises(RuntimeError) as err_exc:
        asyncio.run(_run_err())
    assert len(str(err_exc.value)) < 1000

    huge_sse = b"data: " + (b"x" * (1100 * 1024)) + b"\n"
    sse_transport = httpx.MockTransport(lambda _r: httpx.Response(200, content=huge_sse))

    async def _run_sse():
        async with httpx.AsyncClient(transport=sse_transport) as client:
            async for _ in stream_llm_completion(
                [{"role": "user", "content": "hi"}],
                llm_mode="local_gguf",
                local_base_url="http://127.0.0.1:8081/v1",
                local_model="test",
                http_client=client,
            ):
                pass

    with pytest.raises(RuntimeError, match="1 MiB"):
        asyncio.run(_run_sse())


# ---------------------------------------------------------------------------
# 10. Live Worker TTS & Visual Hardening (Auditor 9 H7, M9, L4)
# ---------------------------------------------------------------------------


def test_live_worker_tts_cleanup_non_blocking_and_starting_state_join(tmp_path):
    from server.live_worker_tts import ColabOneResidentTtsWorker

    worker = ColabOneResidentTtsWorker("qwen3_tts", model_dir=tmp_path)
    joined_flags: list[str] = []

    class _FakeThread:
        def __init__(self, name: str):
            self.name = name

        def join(self, timeout=None):
            joined_flags.append(self.name)

    class _FakeProc:
        def __init__(self):
            self.stdin = io.BytesIO()
            self.stdout = io.BytesIO()
            self.stderr = io.BytesIO()
            self.terminated = False

        def poll(self):
            return 0 if self.terminated else None

        def terminate(self):
            self.terminated = True

        def wait(self, timeout=None):
            self.terminated = True
            return 0

        def kill(self):
            self.terminated = True

    proc = _FakeProc()
    t_out = _FakeThread("out")
    t_err = _FakeThread("err")
    worker._starting_state = (proc, t_out, t_err)

    worker._stdin_lock.acquire()
    try:
        t0 = time.monotonic()
        worker.unload()
        elapsed = time.monotonic() - t0
    finally:
        worker._stdin_lock.release()
    assert elapsed < 2.5
    assert proc.terminated is True
    assert set(joined_flags) == {"out", "err"}


def test_live_worker_visual_disposed_set_action_driving_reload_and_ffmpeg_checks(
    tmp_path, monkeypatch
):
    from server import live_worker_visual

    eng = live_worker_visual.ColabOneSyntheticVisualEngine()
    eng.unload()
    with pytest.raises(RuntimeError, match="释放"):
        eng.set_action("wave")

    real_vid = tmp_path / "real.mp4"
    real_vid.write_bytes(b"fake")
    sym_vid = tmp_path / "sym.mp4"
    sym_vid.symlink_to(real_vid)
    assert live_worker_visual._decode_video_frames_rgb(sym_vid, 16, 16) == []
    assert live_worker_visual._decode_video_frames_rgb(Path("/tmp/bad\nclip.mp4"), 16, 16) == []

    p_eng = live_worker_visual.ColabOnePersonaLiveVisualEngine(width=16, height=16)
    decode_calls: list[Path] = []

    def _fake_decode(path, w, h, max_frames=300):
        decode_calls.append(Path(path))
        return [bytes(w * h * 3)]

    monkeypatch.setattr(live_worker_visual, "_decode_video_frames_rgb", _fake_decode)
    p_eng.load()
    p_eng.load_driving_clip(real_vid)
    assert len(decode_calls) == 1
    p_eng.hot_swap_unload()
    p_eng.hot_swap_reload()
    assert len(decode_calls) == 2


# ---------------------------------------------------------------------------
# 11. ComfyUI WS Monitor Generation & Stop-Event Isolation (Auditor 9 H5)
# ---------------------------------------------------------------------------


def test_comfy_ws_per_connection_stop_event_and_generation_filtering():
    from server.comfy_ws import ColabOneComfyWsMonitor

    progress_events: list[dict] = []
    mon = ColabOneComfyWsMonitor(
        "127.0.0.1",
        8188,
        "cid-1",
        prompt_id="p-1",
        on_progress=lambda ev: progress_events.append(ev),
    )
    mon._conn_gen = 2
    payload = json.dumps(
        {"type": "progress", "data": {"prompt_id": "p-1", "value": 1, "max": 10}}
    ).encode("utf-8")

    mon._handle_message(0x1, payload, conn_gen=1)
    assert progress_events == []

    mon._handle_message(0x1, payload, conn_gen=2)
    assert len(progress_events) == 1


# ---------------------------------------------------------------------------
# 12. Comfy Packs & Digital Human Hardening (Auditor 9 M8, M10, L3)
# ---------------------------------------------------------------------------


def test_comfy_packs_sageattn_cancel_while_locked_and_wheel_guards(tmp_path):
    from server import comfy_packs
    from server.runtime import ColabOneCancelledError

    held = threading.Event()
    release = threading.Event()

    def _holder():
        with comfy_packs._SAGEATTN_WHEEL_LOCK:
            held.set()
            release.wait(timeout=5.0)

    holder_t = threading.Thread(target=_holder)
    holder_t.start()
    assert held.wait(timeout=2.0)

    cancelled = threading.Event()
    cancel_timer = threading.Timer(0.15, cancelled.set)
    cancel_timer.start()

    def _check():
        if cancelled.is_set():
            raise ColabOneCancelledError("cancelled waiting for wheel lock")

    fake_task = SimpleNamespace(check=_check, update=lambda **kw: None, note=lambda *a, **k: None)
    caps = {
        "has_cuda": True,
        "cc_major": 8,
        "cc_minor": 9,
        "torch_version": "2.6.0+cu124",
        "cuda_version": "12.4",
        "sageattn_available": False,
        "sageattn3_available": False,
    }
    try:
        t0 = time.monotonic()
        with pytest.raises(ColabOneCancelledError):
            comfy_packs.ensure_sageattention_wheels_shared(
                task=fake_task,
                base_root=tmp_path / "cache",
                gpu_caps=caps,
            )
        assert time.monotonic() - t0 < 2.5
    finally:
        cancel_timer.cancel()
        release.set()
        holder_t.join(timeout=2.0)

    src_whl = tmp_path / "sageattention-2.1.1-cp312-cp312-linux_x86_64.whl"
    with zipfile.ZipFile(src_whl, "w") as zf:
        zf.writestr("sageattention-2.1.1.dist-info/METADATA", "Name: sageattention\nVersion: 2.1.1\n")
        zf.writestr("sageattention-2.1.1.dist-info/WHEEL", "Wheel-Version: 1.0\n")
    dst_whl = tmp_path / "dst" / src_whl.name
    copied = comfy_packs._copy_wheel_atomic(src_whl, dst_whl)
    assert copied == dst_whl and dst_whl.is_file()

    sym_dst = tmp_path / "dst" / "sym.whl"
    sym_dst.symlink_to(src_whl)
    with pytest.raises(ValueError, match="符号链接"):
        comfy_packs._copy_wheel_atomic(src_whl, sym_dst)

    bad_whl = tmp_path / "sageattention-2.1.1-bad.whl"
    bad_whl.write_bytes(b"not a zip archive")
    drive_mount = tmp_path / "drive"
    (drive_mount / "ColabOne_Drive" / "workspace").mkdir(parents=True)
    fake_drive = SimpleNamespace(
        mode="colab_drivefs",
        is_colab_drivefs_mounted=lambda: True,
        _drivefs_workspace_dir=lambda: drive_mount / "ColabOne_Drive" / "workspace",
    )
    assert (
        comfy_packs._try_mirror_wheel_to_drive(
            fake_drive, cache_key="sm89", wheel_path=src_whl
        )
        is True
    )
    assert (
        comfy_packs._try_mirror_wheel_to_drive(
            fake_drive, cache_key="sm89", wheel_path=bad_whl
        )
        is False
    )


def test_digital_human_reaped_pid_reuse_guard(monkeypatch):
    from server import digital_human

    killpg_calls: list[tuple[int, int]] = []
    monkeypatch.setattr(
        digital_human.os,
        "killpg",
        lambda pgid, sig: killpg_calls.append((pgid, sig)),
        raising=False,
    )
    monkeypatch.setattr(digital_human.os, "kill", lambda pid, sig: None)
    recycled_proc = SimpleNamespace(pid=333333, poll=lambda: 0)
    digital_human._terminate_process_group(recycled_proc, timeout=0.1)
    assert killpg_calls == []


# ---------------------------------------------------------------------------
# 13. Dev & Build Tools Hardening (Auditor 10 H3, H4, M11, M12)
# ---------------------------------------------------------------------------


def test_sync_service_bundle_uses_common_constants_and_atomic_rollback(tmp_path, monkeypatch):
    from common.files import (
        COLABONE_SERVICE_BUNDLE_FOLDERS,
        COLABONE_SERVICE_BUNDLE_SUFFIXES,
    )
    from tools import sync_service_bundle as ssb

    assert ssb.FOLDERS == COLABONE_SERVICE_BUNDLE_FOLDERS
    assert ssb.SUFFIXES == frozenset(COLABONE_SERVICE_BUNDLE_SUFFIXES)

    fake_root = tmp_path / "repo"
    for folder in COLABONE_SERVICE_BUNDLE_FOLDERS:
        (fake_root / folder).mkdir(parents=True)
    (fake_root / "server" / "app.py").write_text("print('v1')\n", encoding="utf-8")
    (fake_root / "common" / "files.py").write_text("X = 1\n", encoding="utf-8")
    (fake_root / "catalog" / "models.json").write_text("{}\n", encoding="utf-8")

    embed_dir = tmp_path / "embedded"
    assert ssb.sync_service_bundle(root=fake_root, embed_dir=embed_dir) == 3
    assert ssb.check_service_bundle(root=fake_root, embed_dir=embed_dir) == 3

    (fake_root / "server" / "app.py").write_text("print('v2')\n", encoding="utf-8")
    orig_rename = Path.rename
    calls = {"n": 0}

    def _fail_second_rename(self, target):
        calls["n"] += 1
        if calls["n"] == 2:
            raise OSError("simulated swap failure")
        return orig_rename(self, target)

    monkeypatch.setattr(Path, "rename", _fail_second_rename)
    with pytest.raises(OSError, match="simulated swap failure"):
        ssb.sync_service_bundle(root=fake_root, embed_dir=embed_dir)
    assert (embed_dir / "server" / "app.py").read_text(encoding="utf-8") == "print('v1')\n"


def test_release_update_manifest_validation_and_partial_headings(tmp_path):
    from tools.release_update_manifest import (
        ColabOneUpdateManifestError,
        colabone_prepare_update_manifests,
        colabone_render_github_release_body,
    )

    zh_only = "### 更新内容\r- 仅中文更新"
    rendered = colabone_render_github_release_body(zh_only, version=" v0.1.25 ")
    assert "仅中文更新" in rendered
    assert "\r" not in rendered
    assert "ColabOne-0.1.25-windows-x64-setup.exe" in rendered

    en_only = "### Highlights\r- English only change"
    rendered_en = colabone_render_github_release_body(en_only, version="0.1.25")
    assert "English only change" in rendered_en

    partial_zh = "### 更新内容\n\n## 下载安装\n- 自定义中文链接"
    rendered_partial_zh = colabone_render_github_release_body(partial_zh, version="0.1.25")
    assert rendered_partial_zh.count("## 下载安装") == 1
    assert rendered_partial_zh.count("## Downloads") == 1
    assert "自定义中文链接" in rendered_partial_zh

    partial_en = "### Highlights\n\n## Downloads\n- Custom English link"
    rendered_partial_en = colabone_render_github_release_body(partial_en, version="0.1.25")
    assert rendered_partial_en.count("## 下载安装") == 1
    assert rendered_partial_en.count("## Downloads") == 1
    assert "Custom English link" in rendered_partial_en

    dist_dir = tmp_path / "dist"
    dist_dir.mkdir()
    # 1-byte file: in Python `True == 1`, so rejecting `size: True` proves strict bool rejection.
    (dist_dir / "ColabOne.exe").write_bytes(b"x")
    base_manifest = {
        "version": "0.1.25",
        "notes": "### 更新内容\n- test",
        "signature": "sig-placeholder-ed25519",
        "url": "https://github.com/org/repo/releases/download/v0.1.25/ColabOne.exe",
        "sha256": "a" * 64,
        "size": True,
    }
    in_m = dist_dir / "update-windows-amd64.json"
    in_m.write_text(json.dumps(base_manifest), encoding="utf-8")
    with pytest.raises(ColabOneUpdateManifestError, match="size mismatch"):
        colabone_prepare_update_manifests(
            dist_dir=dist_dir,
            version="0.1.25",
            notes="### 更新内容\n- test",
            targets=("windows-amd64",),
        )

    base_manifest["size"] = 1
    for bad_url in (
        "https://github.com/org/repo/releases/download/v0.1.25/ColabOne.exe?token=abc",
        "https://github.com/org/repo/releases/download/v0.1.25/ColabOne.exe?",
        "https://github.com/org/repo/releases/download/v0.1.25/ColabOne.exe#",
        "https://user:pass@github.com/org/repo/releases/download/v0.1.25/ColabOne.exe",
        "https://github.com/org/repo/releases/download/v0.1.25/ColabOne.exe/",
    ):
        base_manifest["url"] = bad_url
        in_m.write_text(json.dumps(base_manifest), encoding="utf-8")
        with pytest.raises(ColabOneUpdateManifestError):
            colabone_prepare_update_manifests(
                dist_dir=dist_dir,
                version="0.1.25",
                notes="### 更新内容\n- test",
                targets=("windows-amd64",),
            )

    # Atomic multi-target validation: if target #2 fails, target #1 must remain untouched.
    base_manifest["url"] = "https://github.com/org/repo/releases/download/v0.1.25/ColabOne.exe"
    base_manifest["notes"] = "original-notes"
    in_m.write_text(json.dumps(base_manifest), encoding="utf-8")
    bad_m = dist_dir / "update-darwin-arm64.json"
    bad_m.write_text(json.dumps({**base_manifest, "version": "9.9.9"}), encoding="utf-8")
    with pytest.raises(ColabOneUpdateManifestError, match="version"):
        colabone_prepare_update_manifests(
            dist_dir=dist_dir,
            version=" v0.1.25 ",
            notes="updated-notes",
            targets=("windows-amd64", "darwin-arm64"),
        )
    assert json.loads(in_m.read_text(encoding="utf-8"))["notes"] == "original-notes"


def test_install_py_and_run_win_dev_hardening(tmp_path, monkeypatch):
    from tools import install as install_tool
    from tools import sync_service_bundle as ssb

    monkeypatch.setattr(install_tool.platform, "machine", lambda: "sparc64")
    with pytest.raises(RuntimeError, match="Unsupported host CPU architecture"):
        install_tool._host_goarch()

    # WOW64 on win32: 32-bit Python reports x86, should inspect PROCESSOR_ARCHITEW6432.
    with monkeypatch.context() as mp:
        mp.setattr(install_tool.sys, "platform", "win32")
        mp.setattr(install_tool.platform, "machine", lambda: "x86")
        mp.setenv("PROCESSOR_ARCHITEW6432", "AMD64")
        assert install_tool._host_goarch() == "amd64"

    src = tmp_path / "src.txt"
    src.write_text("hello", encoding="utf-8")
    dst = tmp_path / "dst.txt"
    install_tool.replace_file(src, dst)
    assert dst.read_text(encoding="utf-8") == "hello"

    sym_dst = tmp_path / "sym.txt"
    sym_dst.symlink_to(dst)
    with pytest.raises(ValueError, match="symlink"):
        install_tool.replace_file(src, sym_dst)

    # sync_service_bundle flags stray .pyc outside __pycache__ and symlinks.
    fake_repo = tmp_path / "sb_repo"
    for folder in ("server", "common", "catalog"):
        (fake_repo / folder).mkdir(parents=True)
    (fake_repo / "server" / "a.py").write_text("x = 1\n", encoding="utf-8")
    emb = tmp_path / "sb_emb"
    assert ssb.sync_service_bundle(root=fake_repo, embed_dir=emb) == 1
    stray_pyc = emb / "server" / "stray.pyc"
    stray_pyc.write_bytes(b"pyc")
    with pytest.raises(RuntimeError, match="extra=server/stray.pyc"):
        ssb.check_service_bundle(root=fake_repo, embed_dir=emb)
    stray_pyc.unlink()
    sym_file = emb / "server" / "link.py"
    sym_file.symlink_to(emb / "server" / "a.py")
    with pytest.raises(RuntimeError, match="symlinks=server/link.py"):
        ssb.check_service_bundle(root=fake_repo, embed_dir=emb)

    # Empty source_files refused cleanly.
    empty_repo = tmp_path / "empty_repo"
    empty_repo.mkdir()
    with pytest.raises(RuntimeError, match="No remote service bundle"):
        ssb.sync_service_bundle(root=empty_repo, embed_dir=tmp_path / "empty_emb")

    script_path = ROOT / "work" / "run-win-dev.sh"
    if script_path.is_file():
        script_text = script_path.read_text(encoding="utf-8")
        assert "-EncodedCommand" in script_text
        assert "ie4uinit.exe" in script_text
        assert "-Wait" not in script_text
        assert "shutil.copyfile" in script_text
        assert 'GetFolderPath("LocalApplicationData")' in script_text
        assert "dst_tp.is_symlink()" in script_text
        if os.name != "nt" and shutil.which("bash"):
            res = subprocess.run(
                ["bash", "-n", str(script_path)],
                stdin=subprocess.DEVNULL,
                capture_output=True,
                text=True,
            )
            assert res.returncode == 0, res.stderr

    mjs_path = ROOT / "frontend" / "scripts" / "sync-embedded-dist.mjs"
    mjs_text = mjs_path.read_text(encoding="utf-8")
    assert "fileURLToPath" in mjs_text
    assert "mkdtemp" in mjs_text
    assert "rename(" in mjs_text
    if shutil.which("node"):
        res_node = subprocess.run(
            ["node", "--check", str(mjs_path)],
            stdin=subprocess.DEVNULL,
            capture_output=True,
            text=True,
        )
        assert res_node.returncode == 0, res_node.stderr


# ---------------------------------------------------------------------------
# 14. Round-2 Regression Tests (Auditors 8, 9, 10 Deep Audit Findings)
# ---------------------------------------------------------------------------


def test_runtime_bounded_sync_lock_delete_recreate_guard_and_clip_ext_cleanup(
    tmp_path, monkeypatch
):
    import base64
    from PIL import Image

    from server.runtime import ColabOneRuntime

    rt = ColabOneRuntime(root=tmp_path / "rt", catalog=ROOT / "catalog")
    try:
        # 1. Bounded sync_lock acquire(timeout=2.0) skips remote call when contended.
        class _ContendedLock:
            def __init__(self):
                self.timeouts: list[object] = []

            def acquire(self, blocking=True, timeout=-1):
                self.timeouts.append(timeout)
                return False

            def release(self):
                raise AssertionError("must not release unacquired lock")

        contended = _ContendedLock()
        orig_lock = rt.sync_lock
        rt.sync_lock = contended
        called: list[str] = []
        monkeypatch.setattr(rt.drive, "remove_remote_workflow", lambda wid: called.append(wid) or True)
        monkeypatch.setattr(
            rt.drive, "remove_remote_comfy_output", lambda oid, ext: called.append(oid) or True
        )
        rt._on_workflow_deleted("wf-contended")
        rt._on_comfy_output_deleted("0" * 32, "png")
        assert contended.timeouts == [2.0, 2.0]
        assert called == []

        # 2. Recreate guard: if live workflow exists again when sync_lock is acquired,
        #    do NOT trash remote workflow on Drive.
        rt.sync_lock = orig_lock
        wf_dir = rt.root / "workflows"
        (wf_dir / ".trash").mkdir(parents=True, exist_ok=True)
        (wf_dir / ".trash" / "wf-recreated.json").write_text("{}", encoding="utf-8")
        (wf_dir / "wf-recreated.json").write_text('{"id":"wf-recreated"}', encoding="utf-8")
        rt._on_workflow_deleted("wf-recreated")
        assert called == []

        # 3. _comfy_clip_runner returns extension="webm", live_studio saves .webm, and cleans io_in/io_out.
        wf_id = "live-avatar-motion"
        rt.comfy.store.save_workflow(
            {
                "id": wf_id,
                "name": "Clip Webm Test",
                "workflow": {
                    "10": {"class_type": "LoadImage", "inputs": {"image": "old.png"}},
                },
            }
        )
        rt.comfy.io_in_dir.mkdir(parents=True, exist_ok=True)
        rt.comfy.io_out_dir.mkdir(parents=True, exist_ok=True)
        staged_io_in = rt.comfy.io_in_dir / "uploaded_portrait.png"
        staged_io_out = rt.comfy.io_out_dir / "gen_clip.webm"

        def _fake_upload(fname, fpath):
            staged_io_in.write_bytes(b"img")
            return staged_io_in.name

        def _fake_exec(task, wf, prompt, **kwargs):
            out_dir = Path(kwargs["output_dir"])
            out_dir.mkdir(parents=True, exist_ok=True)
            vid = out_dir / "gen_clip.webm"
            vid.write_bytes(b"\x1a\x45\xdf\xa3fakewebm")
            staged_io_out.write_bytes(b"\x1a\x45\xdf\xa3fakewebm")
            return [{"path": str(vid), "filename": "gen_clip.webm", "type": "output"}]

        monkeypatch.setattr(rt.comfy, "_upload_input_file", _fake_upload)
        monkeypatch.setattr(rt.comfy, "_execute_single_prompt", _fake_exec)
        monkeypatch.setattr(rt.comfy, "is_running", lambda: True)
        monkeypatch.setattr(rt.comfy, "start_server", lambda *a, **kw: None)

        buf = io.BytesIO()
        Image.new("RGB", (16, 16), (10, 20, 30)).save(buf, format="PNG")
        snap = rt.build_live_avatar_pack(
            {
                "avatar_id": "av_webm_test",
                "name": "WebmTest",
                "portrait_b64": base64.b64encode(buf.getvalue()).decode("ascii"),
                "portrait_filename": "p.png",
                "actions": ["idle_loop"],
                "dance_ids": [],
            }
        )
        task = rt.tasks[snap["id"]]
        deadline = time.monotonic() + 5.0
        while task.state == "running" and time.monotonic() < deadline:
            time.sleep(0.02)
        assert task.state == "completed", task.error
        av = rt.live.get_avatar("av_webm_test")
        assert any(a.get("clip_path") == "clips/idle_loop.webm" for a in av["actions"])
        assert not staged_io_in.exists()
        assert not staged_io_out.exists()
    finally:
        rt.stopping.set()
        rt.close_http_sessions()


def test_vm_update_symlink_log_launch_err_precedence_and_rename_fail_restart(
    tmp_path, monkeypatch
):
    content_dir = tmp_path / "content"
    content_dir.mkdir()
    (content_dir / "colabone-boot.json").write_text(
        json.dumps({"token": "tok123", "deadline": 0, "runtime_id": "rt-test-2"}),
        encoding="utf-8",
    )
    service_dir = content_dir / "colabone"
    (service_dir / "server").mkdir(parents=True)
    (service_dir / "server" / "app.py").write_text("# old\n", encoding="utf-8")
    (service_dir / "server" / "requirements.txt").write_text("# same\n", encoding="utf-8")

    bundle = content_dir / "colabone-update.zip"
    with zipfile.ZipFile(bundle, "w") as zf:
        zf.writestr("colabone/server/app.py", "# new\n")
        zf.writestr("colabone/server/requirements.txt", "# same\n")

    fake_proc = tmp_path / "fake_proc"
    fake_pid_dir = fake_proc / "515151"
    fake_pid_dir.mkdir(parents=True)
    (fake_pid_dir / "cmdline").write_bytes(b"python3\x00-m\x00server.app\x00")
    (fake_pid_dir / "environ").write_bytes(
        f"COLABONE_REMOTE_CONFIG={content_dir.as_posix()}/colabone-boot.json\x00".encode("utf-8")
    )
    stat_fields = ["S"] + ["0"] * 18 + ["12345"] + ["0"] * 5
    (fake_pid_dir / "stat").write_text(
        f"515151 (python) {' '.join(stat_fields)}\n", encoding="utf-8"
    )

    secret_file = tmp_path / "secret.txt"
    secret_file.write_text("TOP_SECRET_MUST_NOT_LEAK", encoding="utf-8")
    (content_dir / "colabone-server.log").symlink_to(secret_file)

    class _FakeResp:
        def __init__(self, payload: dict):
            self._data = json.dumps(payload).encode("utf-8")

        def __enter__(self):
            return self

        def __exit__(self, *a):
            return False

        def read(self):
            return self._data

        def close(self):
            pass

    class _Opener:
        def open(self, req, timeout=None):
            url = req.full_url if hasattr(req, "full_url") else str(req)
            if url.endswith("/api/v1/status"):
                return _FakeResp({"state": "ready", "tasks": []})
            return _FakeResp({"ok": True})

    vm_src = (ROOT / "server" / "vm_update.py").read_text(encoding="utf-8")
    vm_patched = vm_src.replace("/content", content_dir.as_posix()).replace(
        "/proc", fake_proc.as_posix()
    )
    vm_tree = ast.parse(vm_patched, filename="vm_update_test2.py")
    if vm_tree.body and isinstance(vm_tree.body[-1], ast.Expr):
        vm_tree.body.pop()
    vm_ns: dict[str, object] = {"__name__": "server.vm_update_test_load3"}
    exec(compile(vm_tree, "vm_update_test2.py", "exec"), vm_ns)
    colabone_update_fn = vm_ns["colabone_update"]

    monkeypatch.setattr("urllib.request.build_opener", lambda *a, **kw: _Opener())
    monkeypatch.setattr(
        "os.kill",
        lambda pid, sig: (fake_pid_dir / "stat").unlink(missing_ok=True),
    )

    with pytest.raises(RuntimeError) as exc_info:
        colabone_update_fn()
    msg = str(exc_info.value)
    assert "TOP_SECRET_MUST_NOT_LEAK" not in msg
    assert "refusing symlink log file" in msg

    # 2. If staged_root.rename("/content/colabone") fails, restore backup AND restart previous service.
    (content_dir / "colabone-server.log").unlink()
    (fake_pid_dir / "stat").write_text(
        f"515151 (python) {' '.join(stat_fields)}\n", encoding="utf-8"
    )
    orig_rename = Path.rename
    restarted_cmds: list[list[str]] = []

    def _fail_staged_rename(self: Path, target):
        if self.name == "colabone" and self.parent.name == "colabone-update":
            raise OSError("simulated EXDEV on staged_root.rename")
        return orig_rename(self, target)

    class _FakeRestartProc:
        pid = 616161

        def poll(self):
            return None

    monkeypatch.setattr(Path, "rename", _fail_staged_rename)
    monkeypatch.setattr(
        "subprocess.Popen",
        lambda cmd, **kw: restarted_cmds.append(list(cmd)) or _FakeRestartProc(),
    )
    with pytest.raises(OSError, match="simulated EXDEV"):
        colabone_update_fn()
    assert (service_dir / "server" / "app.py").read_text(encoding="utf-8") == "# old\n"
    assert restarted_cmds and restarted_cmds[0][-2:] == ["-m", "server.app"]


def test_tbe_runtime_addr_live_provision_env_and_drive_umount_env(tmp_path, monkeypatch):
    from common.files import colabone_sanitized_subprocess_env
    from server import drive, live_studio

    monkeypatch.setenv("TBE_RUNTIME_ADDR", "127.0.0.1:9999")
    monkeypatch.setenv("RCLONE_CONFIG_SECRET", "rclone-secret")
    monkeypatch.setenv("HF_TOKEN", "hf-ambient-secret")
    monkeypatch.setenv("PIP_USER", "1")
    monkeypatch.setenv("COLABONE_BENIGN_VAR", "ok")

    common_env = colabone_sanitized_subprocess_env()
    assert "TBE_RUNTIME_ADDR" not in common_env

    live_env = live_studio._sanitized_live_provision_env({"HF_TOKEN": "should-be-blocked"})
    assert "TBE_RUNTIME_ADDR" not in live_env
    assert "RCLONE_CONFIG_SECRET" not in live_env
    assert "HF_TOKEN" not in live_env
    assert "PIP_USER" not in live_env
    assert live_env.get("COLABONE_BENIGN_VAR") == "ok"

    # Drive unmount / stale FUSE recovery passes sanitized env to fusermount/umount.
    seen_envs: list[dict[str, str] | None] = []
    monkeypatch.setattr(drive.shutil, "which", lambda cmd: "/bin/umount" if cmd == "umount" else None)
    monkeypatch.setattr(
        drive.subprocess,
        "run",
        lambda cmd, **kw: seen_envs.append(kw.get("env")) or SimpleNamespace(returncode=0),
    )
    drv = drive.ColabOneDrive(root=tmp_path / "st", catalog=ROOT / "catalog", mount_root=tmp_path / "mnt")
    monkeypatch.setattr(drv, "is_fuse_mounted", lambda: True)
    drv.mode = "mount"
    drv.unmount()
    assert len(seen_envs) == 1 and isinstance(seen_envs[0], dict)
    assert "TBE_RUNTIME_ADDR" not in seen_envs[0]
    assert "RCLONE_CONFIG_SECRET" not in seen_envs[0]


def test_public_api_streaming_early_disconnect_and_retired_keys_pruning(tmp_path):
    from server.public_api import (
        ColabOnePublicGateway,
        _ColabOneGuardedStreamingResponse,
    )
    from server.runtime import ColabOneRuntime

    # 1. _ColabOneGuardedStreamingResponse calls on_close even when ASGI fails before body iteration.
    closed: list[bool] = []
    started = False

    async def _never_started_gen():
        nonlocal started
        started = True
        yield b"data: hi\n\n"

    resp = _ColabOneGuardedStreamingResponse(
        _never_started_gen(),
        on_close=lambda: closed.append(True),
        media_type="text/event-stream",
    )

    async def _disconnect_receive():
        return {"type": "http.disconnect"}

    async def _failing_send(_msg):
        raise RuntimeError("client disconnected before first body chunk")

    with pytest.raises(RuntimeError, match="client disconnected"):
        asyncio.run(resp({"type": "http"}, _disconnect_receive, _failing_send))
    assert started is False
    assert closed == [True]

    # 2. _finalize_request prunes _retired_keys even while task.state is still "running".
    rt = ColabOneRuntime(root=tmp_path / "rt", catalog=ROOT / "catalog")
    try:
        gw = ColabOnePublicGateway(rt)
        gw._retired_keys.append("sk-old-retired-key")
        metric, acc = gw._init_slot_metric("chat", "test-model", False)
        fake_task = SimpleNamespace(lock=threading.Lock(), state="running", error="")
        slot = {
            "task": fake_task,
            "metric": metric,
            "acc": acc,
            "active_handlers": 0,
        }
        gw.jobs["job-1"] = slot
        gw._active_http_handlers = 0
        gw._finalize_request(slot, state="completed")
        assert gw._retired_keys == []
    finally:
        rt.stopping.set()
        rt.close_http_sessions()


def test_openai_compat_blank_invalid_compaction_cap_and_compact_sync_400(tmp_path):
    import base64
    import zlib
    from fastapi.testclient import TestClient

    from server.openai_compat import (
        COLABONE_COMPACTION_PREFIX,
        MAX_COMPACTION_DECOMPRESSED_BYTES,
        MAX_TOTAL_COMPACTION_DECOMPRESSED_BYTES,
        encode_compaction_content,
        normalize_compact_request,
        render_items_for_compaction,
        response_items_to_chat_messages,
    )
    from server.public_api import ColabOnePublicGateway, create_colabone_public_app
    from server.runtime import ColabOneRuntime

    def _raw_compaction_blob(raw_bytes: bytes) -> str:
        comp = zlib.compress(raw_bytes, level=6)
        return COLABONE_COMPACTION_PREFIX + base64.urlsafe_b64encode(comp).decode("ascii").rstrip("=")

    # Blank summary (whitespace only) and invalid JSON still count toward cumulative cap.
    per_item_len = min(200 * 1024, MAX_COMPACTION_DECOMPRESSED_BYTES - 1024)
    blank_blob = _raw_compaction_blob(
        json.dumps({"v": 1, "summary": " " * per_item_len}).encode("utf-8")
    )
    invalid_blob = _raw_compaction_blob(b"{" + b"x" * per_item_len)
    count = (MAX_TOTAL_COMPACTION_DECOMPRESSED_BYTES // per_item_len) + 2

    blank_items = [{"type": "compaction", "encrypted_content": blank_blob} for _ in range(count)]
    invalid_items = [{"type": "compaction", "encrypted_content": invalid_blob} for _ in range(count)]

    for items in (blank_items, invalid_items):
        with pytest.raises(ValueError, match="总大小"):
            response_items_to_chat_messages(items)
        with pytest.raises(ValueError, match="总大小"):
            render_items_for_compaction(items)
        with pytest.raises(ValueError, match="总大小"):
            normalize_compact_request({"model": "auto", "input": items}, {})

    # Fallback summary charges additively on top of decompressed invalid encrypted_content.
    valid_enc = encode_compaction_content("A" * per_item_len)
    oversized_items = [{"type": "compaction", "encrypted_content": valid_enc} for _ in range(count)]
    rt = ColabOneRuntime(root=tmp_path / "rt", catalog=ROOT / "catalog")
    try:
        spec = next(m for m in rt.models.values() if getattr(m, "kind", "chat") == "chat")
        rt.loaded = spec.id
        gw = ColabOnePublicGateway(rt)
        gw.enabled = True
        gw.key = "sk-compact-400-test-key"
        with TestClient(create_colabone_public_app(gw)) as client:
            resp = client.post(
                "/v1/responses/compact",
                json={"model": spec.id, "input": oversized_items},
                headers={"Authorization": "Bearer sk-compact-400-test-key"},
            )
            assert resp.status_code == 400
    finally:
        rt.stopping.set()
        rt.close_http_sessions()


def test_comfy_ws_stop_none_sock_preserves_new_socket_and_set_prompt_id_gen_guard():
    from server.comfy_ws import ColabOneComfyWsMonitor

    closed_socks: list[str] = []

    class _FakeSock:
        def __init__(self, name: str):
            self.name = name

        def close(self):
            closed_socks.append(self.name)

    mon = ColabOneComfyWsMonitor("127.0.0.1", 8188, "cid-stop")
    new_sock = _FakeSock("new-conn")
    # Simulate stop() capturing sock=None while a concurrent start() installs new_sock before cleanup.
    orig_close = mon._close_socket

    def _spy_close(target_sock=None):
        orig_close(target_sock)

    mon._sock = None
    mon._thread = None
    mon.stop()
    mon._sock = new_sock
    # Explicitly verify stop() does NOT call _close_socket(None) to wipe new_sock.
    class _RacyThread:
        ident = 999999

        def join(self, timeout=None):
            # Concurrent start() publishes a brand new socket while old stop() is joining.
            mon._sock = new_sock

    mon._sock = None
    mon._thread = _RacyThread()
    mon.stop()
    assert mon._sock is new_sock
    assert closed_socks == []

    # set_prompt_id stops firing actions when _conn_gen changes mid-replay.
    fired: list[int] = []

    def _on_prog(ev):
        fired.append(ev["step"])
        mon2._conn_gen += 1

    mon2 = ColabOneComfyWsMonitor("127.0.0.1", 8188, "cid-gen", on_progress=_on_prog)
    mon2._pending_buffer.append(
        json.dumps({"type": "progress", "data": {"prompt_id": "p1", "value": 1, "max": 5}})
    )
    mon2._pending_buffer.append(
        json.dumps({"type": "progress", "data": {"prompt_id": "p1", "value": 2, "max": 5}})
    )
    mon2.set_prompt_id("p1")
    assert fired == [1]


def test_live_studio_pgrp_tts_cancel_disk_preflight_and_integrity(tmp_path, monkeypatch):
    from server import live_studio
    from server.runtime import ColabOneCancelledError, ColabOneTask

    # 1. _terminate_live_process_group guards recycled PID & bool PID.
    killpg_calls: list[tuple[int, int]] = []
    monkeypatch.setattr(
        live_studio.os,
        "killpg",
        lambda pgid, sig: killpg_calls.append((pgid, sig)),
        raising=False,
    )
    monkeypatch.setattr(live_studio.os, "kill", lambda pid, sig: None)
    live_studio._terminate_live_process_group(SimpleNamespace(pid=777777, poll=lambda: 0), timeout=0.1)
    live_studio._terminate_live_process_group(SimpleNamespace(pid=True, poll=lambda: None, terminate=lambda: None, wait=lambda timeout=None: 0, kill=lambda: None), timeout=0.1)
    assert killpg_calls == []

    # 2. _ensure_tts_ready rejects unverified weights even when download dir exists.
    mgr = live_studio.ColabOneLiveStudioManager(tmp_path / "live", ROOT / "catalog")
    fake_venv_py = tmp_path / "venv" / "python"
    fake_venv_py.parent.mkdir(parents=True)
    fake_venv_py.write_text("#!/bin/sh\n", encoding="utf-8")
    unverified_dir = tmp_path / "unverified"
    unverified_dir.mkdir()
    monkeypatch.setattr(mgr, "_ensure_engine_venv", lambda *a, **kw: fake_venv_py)
    monkeypatch.setattr(mgr, "_resolve_component_model_dir", lambda *a, **kw: None)
    monkeypatch.setattr(mgr, "_download_hf_repo_with_progress", lambda *a, **kw: unverified_dir)
    with pytest.raises(RuntimeError, match="完整性校验"):
        mgr._ensure_tts_ready(None, "qwen3_tts")

    # 3. start(..., auto_provision=True) disk failure clears "starting" state.
    monkeypatch.setattr(mgr, "ensure_session_dependencies", lambda *a, **kw: {"ok": True})
    monkeypatch.setattr(mgr, "_free_disk_mib", lambda: 100)
    with pytest.raises(ValueError, match="磁盘空间不足"):
        mgr.start(
            {"visual_engine": "flashhead_lite", "tts_engine": "qwen3_tts", "llm_mode": "local_gguf"},
            gpu_info={"name": "NVIDIA A100", "total_mib": 81920},
            synthetic=False,
            auto_provision=True,
            min_free_disk_mib=1024,
        )
    assert mgr.status()["state"] == "error"

    # 4. tts.load() cancellation transitions cleanly to "stopped" / ColabOneCancelledError.
    class _CancellingTts:
        def load(self):
            task_cancel.request_cancel()
            raise RuntimeError("worker killed during load")

        def unload(self):
            pass

    task_cancel = ColabOneTask("live_start", "qwen3_tts")
    monkeypatch.setattr(mgr, "_free_disk_mib", lambda: 999999)
    monkeypatch.setattr(mgr, "_build_tts_engine", lambda *a, **kw: _CancellingTts())
    with pytest.raises(ColabOneCancelledError):
        mgr.start(
            {"visual_engine": "flashhead_lite", "tts_engine": "qwen3_tts", "llm_mode": "local_gguf"},
            gpu_info={"name": "NVIDIA A100", "total_mib": 81920},
            synthetic=False,
            auto_provision=False,
            prewarm_tts=True,
            min_free_disk_mib=0,
            task=task_cancel,
        )
    assert mgr.status()["state"] == "stopped"

    # 5. _handle_ws_switch_engine enforces disk preflight and wires cancel adapter into stop().
    from common.live_studio import ColabOneLiveSessionConfig

    mgr._running = True
    mgr._stopping = False
    mgr._active_session_cfg = ColabOneLiveSessionConfig(
        visual_engine="flashhead_lite", tts_engine="qwen3_tts"
    )
    mgr._app = SimpleNamespace(
        state=SimpleNamespace(visual_engine=None, tts_engine=None, encoder=None)
    )
    mgr._last_auto_provision = True
    mgr._last_synthetic = False
    mgr._last_gpu_info = {"name": "NVIDIA A100", "total_mib": 81920}
    monkeypatch.setattr(mgr, "_resolve_engine_venv_python", lambda _eid: None)
    monkeypatch.setattr(mgr, "_free_disk_mib", lambda: 64)
    with pytest.raises(ValueError, match="磁盘空间不足"):
        mgr._handle_ws_switch_engine({"tts_engine": "voxcpm2"})

    hook_fired: list[bool] = []
    monkeypatch.setattr(mgr, "_free_disk_mib", lambda: 999999)

    def _cancellable_ensure_tts(sw_task, eid, **kw):
        assert sw_task is not None and hasattr(sw_task, "lock") and hasattr(sw_task, "cancel_hook")
        with sw_task.lock:
            sw_task.cancel_hook = lambda: hook_fired.append(True)
        mgr.stop()
        sw_task.check()

    monkeypatch.setattr(mgr, "_ensure_tts_ready", _cancellable_ensure_tts)
    with pytest.raises(ColabOneCancelledError):
        mgr._handle_ws_switch_engine({"tts_engine": "voxcpm2"})
    assert hook_fired == [True]


def test_comfy_proxy_any_import_and_live_worker_tts_closed_stdin(tmp_path):
    from server import comfy_proxy
    from server.live_worker_tts import ColabOneResidentTtsWorker

    assert hasattr(comfy_proxy, "Any")

    worker = ColabOneResidentTtsWorker("qwen3_tts", model_dir=tmp_path)
    closed_stdin = io.StringIO()
    closed_stdin.close()
    fake_proc = SimpleNamespace(
        stdin=closed_stdin,
        stdout=io.StringIO(),
        stderr=io.StringIO(),
        poll=lambda: None,
        terminate=lambda: None,
        wait=lambda timeout=None: 0,
        kill=lambda: None,
    )
    worker._proc = fake_proc
    worker._stdout_queue = queue.Queue()
    worker._stderr_lines = []
    worker._loaded = True
    with pytest.raises(RuntimeError, match="流式 TTS 写入任务失败"):
        list(worker.synthesize_stream("hello", epoch=1))


def test_live_server_zero_byte_trigger_action_and_personalive_cancel_rollback():
    src = (ROOT / "server" / "live_server.py").read_text(encoding="utf-8")
    assert "async with _get_video_encode_lock():" in src
    assert "if encoded:\n                await _broadcast_video_frame(" in src
    assert "if not encoded:\n                return" in src
    assert "if not fmp4_frame and not jpeg_frame:\n            return" in src
    assert "except asyncio.CancelledError:\n                                    await _rollback_drive_load()\n                                    raise" in src

    dh_src = (ROOT / "server" / "digital_human.py").read_text(encoding="utf-8")
    assert "if stopped or (callable(is_running_fn) and not is_running_fn()):" in dh_src


# ---------------------------------------------------------------------------
# 15. Round-3 Regression Tests (Auditors 8, 9, 10 Remaining Findings)
# ---------------------------------------------------------------------------


def test_round3_build_bundle_release_and_contract_parser_hardening(tmp_path):
    import stat as stat_mod

    from tools import install as install_tool
    from tools import release_update_manifest as rum
    from tools import sync_service_bundle as ssb
    from tests.test_go_python_contracts import (
        _extract_frontend_colabone_commands,
        _extract_go_switch_case_strings,
    )

    # 1. sync-embedded-dist.mjs stages strictly outside console/frontend with Windows retries.
    mjs_text = (ROOT / "frontend" / "scripts" / "sync-embedded-dist.mjs").read_text(encoding="utf-8")
    assert "path.dirname(targetRootDir)" in mjs_text
    assert ".frontend-dist-stage-" in mjs_text
    assert "maxRetries: 5" in mjs_text
    assert "renameWithRetry" in mjs_text

    # 2. run-win-dev.sh (local untracked dev helper) chmods staged exe, sweeps .ColabOne.exe.new.*, checks dst_tp.is_dir(), and unlinks backup.
    win_sh_path = ROOT / "work" / "run-win-dev.sh"
    if win_sh_path.exists():
        win_sh = win_sh_path.read_text(encoding="utf-8")
        assert "os.chmod(staged_exe, 0o755)" in win_sh
        assert ".ColabOne.exe.new.*" in win_sh
        assert "dst_tp.is_dir()" in win_sh
        assert "backup.unlink()" in win_sh

    # 3. tools/install.py & sync_service_bundle.py handle read-only parent dirs without chmodding symlink targets.
    ro_tree = tmp_path / "ro_tree"
    ro_sub = ro_tree / "sub"
    ro_sub.mkdir(parents=True)
    ro_file = ro_sub / "a.txt"
    ro_file.write_text("ro", encoding="utf-8")
    external_secret = tmp_path / "ext_readonly.txt"
    external_secret.write_text("keep-mode", encoding="utf-8")
    os.chmod(external_secret, 0o444)
    (ro_sub / "sym_ext").symlink_to(external_secret)
    os.chmod(ro_file, 0o444)
    os.chmod(ro_sub, 0o555)
    try:
        install_tool._rmtree_best_effort(ro_tree)
        assert not ro_tree.exists()
        assert not (external_secret.stat().st_mode & stat_mod.S_IWRITE)
        if os.name == "posix":
            assert stat_mod.S_IMODE(external_secret.stat().st_mode) == 0o444
    finally:
        if external_secret.exists():
            os.chmod(external_secret, 0o644)

    # 4. tools/release_update_manifest.py rejects malformed URLs, colons in filenames, and validates --repo before mutating manifests.
    with pytest.raises(rum.ColabOneUpdateManifestError):
        rum._asset_name_from_url("https://[::1/bad.tar.gz", "test.url")
    with pytest.raises(rum.ColabOneUpdateManifestError):
        rum._asset_name_from_url("https://github.com/org/repo/releases/download/v1/foo:bar.tar.gz", "test.url")

    dist_dir = tmp_path / "rel_dist"
    dist_dir.mkdir()
    archive = dist_dir / "app-0.1.9.tar.gz"
    archive.write_bytes(b"archive-bytes")
    manifest_path = dist_dir / "update-windows-amd64.json"
    orig_manifest = {
        "version": "0.1.9",
        "url": "https://github.com/luolongfei/ColabOne/releases/download/v0.1.9/app-0.1.9.tar.gz",
        "size": len(b"archive-bytes"),
        "signature": "sig123",
    }
    manifest_path.write_text(json.dumps(orig_manifest), encoding="utf-8")
    notes_file = tmp_path / "notes.md"
    notes_file.write_text("新版说明\n\n---\n\nEnglish notes", encoding="utf-8")
    rc = rum.main(
        [
            "--dist",
            str(dist_dir),
            "--version",
            "0.1.9",
            "--notes-file",
            str(notes_file),
            "--release-body-output",
            str(tmp_path / "body.md"),
            "--repo",
            "not a valid repo!!!",
            "--targets",
            "windows-amd64",
        ]
    )
    assert rc == 1
    assert "notes" not in json.loads(manifest_path.read_text(encoding="utf-8"))

    # 5. Contract test parsers skip comments, strings, raw strings, runes, and second switches.
    ts_sample = """
    // colabOneCommand("comment.line.fake")
    /* colabOneCommand("comment.block.fake") */
    const s = 'colabOneCommand("string.single.fake")';
    const d = "colabOneCommand('string.double.fake')";
    const t = `colabOneCommand("template.fake")`;
    colabOneCommand<Record<string, () => void>>("real.cmd.one");
    colabOneCommand('real.cmd.two');
    """
    assert _extract_frontend_colabone_commands(ts_sample) == {"real.cmd.one", "real.cmd.two"}

    go_sample = """
    func demo(action string) {
        switch x := `{`; action {
        case "first.cmd":
            _ = '}'
        case "second.cmd":
        }
        switch other {
        case "second.switch.ignored":
        }
    }
    """
    assert _extract_go_switch_case_strings(go_sample, "func demo(") == {
        "first.cmd",
        "second.cmd",
    }

    # 6. vm_update.py caps rollback log read at 8192 bytes.
    vm_src = (ROOT / "server" / "vm_update.py").read_text(encoding="utf-8")
    assert "lf.read(8192)" in vm_src

    # 7. comfy_packs._try_mirror_wheel_to_drive rejects non-wheel zips lacking .dist-info/WHEEL.
    from server import comfy_packs

    bad_whl = tmp_path / "fake-1.0-py3-none-any.whl"
    with zipfile.ZipFile(bad_whl, "w") as zf:
        zf.writestr("random.txt", "not a wheel")
    fake_drive = SimpleNamespace(
        available=lambda: True,
        mode="mount",
        mount_root=tmp_path / "drv_mnt",
        folder="ColabOne",
    )
    assert (
        comfy_packs._try_mirror_wheel_to_drive(
            fake_drive, cache_key="k1", wheel_path=bad_whl
        )
        is False
    )
    assert ssb is not None


def test_round3_runtime_clip_cleanup_preserves_preexisting_and_delete_live_whitespace(
    tmp_path, monkeypatch
):
    import base64
    from PIL import Image
    from server.runtime import ColabOneRuntime

    rt = ColabOneRuntime(root=tmp_path / "rt3", catalog=ROOT / "catalog")
    try:
        # 1. delete_live_avatar normalizes leading/trailing whitespace
        #    and symlinked .trash directory is refused.
        rt.live.save_avatar({"id": "av_ws", "name": "WS"})
        trash_calls: list[str] = []
        monkeypatch.setattr(rt.drive, "trash_live_avatar", lambda aid: trash_calls.append(aid) or True)
        rt.delete_live_avatar("  av_ws  ")
        assert trash_calls == ["av_ws"]

        wf_dir = rt.root / "workflows"
        wf_dir.mkdir(parents=True, exist_ok=True)
        real_trash = tmp_path / "ext_trash"
        real_trash.mkdir()
        (real_trash / "wf_sym.json").write_text("{}", encoding="utf-8")
        (wf_dir / ".trash").symlink_to(real_trash, target_is_directory=True)
        wf_calls: list[str] = []
        monkeypatch.setattr(rt.drive, "remove_remote_workflow", lambda wid: wf_calls.append(wid) or True)
        rt._on_workflow_deleted("wf_sym")
        assert wf_calls == []

        # 2. _comfy_clip_runner preserves pre-existing untouched io_out_dir files, skips traversal/symlink outputs,
        #    and falls back to path/extension when filename lacks a clip suffix.
        rt.comfy.store.save_workflow(
            {
                "id": "live-avatar-motion",
                "name": "Clip Guard Test",
                "workflow": {
                    "10": {"class_type": "LoadImage", "inputs": {"image": "old.png"}},
                },
            }
        )
        rt.comfy.io_in_dir.mkdir(parents=True, exist_ok=True)
        rt.comfy.io_out_dir.mkdir(parents=True, exist_ok=True)
        preexisting = rt.comfy.io_out_dir / "keep_me.mp4"
        preexisting.write_bytes(b"preexisting-user-video")
        outside_file = tmp_path / "outside.mp4"
        outside_file.write_bytes(b"outside")
        sym_sub = rt.comfy.io_out_dir / "sym_sub"
        sym_sub.symlink_to(tmp_path, target_is_directory=True)
        newly_created = rt.comfy.io_out_dir / "sub_ok" / "new_clip.mov"

        def _fake_upload(fname, fpath):
            p = rt.comfy.io_in_dir / "up.png"
            p.write_bytes(b"img")
            return p.name

        def _fake_exec(task, wf, prompt, **kwargs):
            out_dir = Path(kwargs["output_dir"])
            out_dir.mkdir(parents=True, exist_ok=True)
            vid = out_dir / "clip_result.mov"
            vid.write_bytes(b"\x00\x00\x00\x18ftypqt  fakemov")
            newly_created.parent.mkdir(parents=True, exist_ok=True)
            newly_created.write_bytes(b"temp-comfy-copy")
            return [
                {"filename": "keep_me.mp4", "subfolder": "", "type": "output"},
                {"filename": "../outside.mp4", "subfolder": "", "type": "output"},
                {"filename": "outside.mp4", "subfolder": "sym_sub", "type": "output"},
                {
                    "path": str(vid),
                    "filename": "new_clip.mov",
                    "subfolder": "sub_ok",
                    "type": "output",
                },
            ]

        monkeypatch.setattr(rt.comfy, "_upload_input_file", _fake_upload)
        monkeypatch.setattr(rt.comfy, "_execute_single_prompt", _fake_exec)
        monkeypatch.setattr(rt.comfy, "is_running", lambda: True)
        monkeypatch.setattr(rt.comfy, "start_server", lambda *a, **kw: None)

        buf = io.BytesIO()
        Image.new("RGB", (16, 16), (10, 20, 30)).save(buf, format="PNG")
        snap = rt.build_live_avatar_pack(
            {
                "avatar_id": "av_guard_test",
                "name": "GuardTest",
                "portrait_b64": base64.b64encode(buf.getvalue()).decode("ascii"),
                "portrait_filename": "p.png",
                "actions": ["idle_loop"],
                "dance_ids": [],
            }
        )
        task = rt.tasks[snap["id"]]
        deadline = time.monotonic() + 5.0
        while task.state == "running" and time.monotonic() < deadline:
            time.sleep(0.02)
        assert task.state == "completed", task.error
        assert preexisting.exists()
        assert outside_file.exists()
        assert not newly_created.exists()
    finally:
        rt.stopping.set()
        rt.close_http_sessions()



