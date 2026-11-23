import json
from pathlib import Path
from bridge.activity import ColabOneActivityLog
from bridge.controller import ColabOneController
from conftest import MemoryVault


def test_activity_is_bounded_and_redacts_credentials():
    log = ColabOneActivityLog(limit=2)
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
    c = ColabOneController(
        Path(__file__).resolve().parents[1],
        vault=MemoryVault(),
        config_dir=tmp_path,
        background=False,
    )
    c.update(operation={"title": "部署远端管理服务", "running": True})
    c.update(operation={"title": "部署远端管理服务", "running": True})
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
    assert sum(item["message"] == "部署远端管理服务" for item in items) == 1
    assert (
        sum("20%" in item["message"] and item["level"] == "info" for item in items) == 1
    )
    assert items[-1]["level"] == "error"
    assert "校验失败" in items[-1]["message"]


def test_transfer_logs_arrive_without_progress_bucket_change_and_are_deduplicated(
    tmp_path,
):
    c = ColabOneController(
        Path(__file__).resolve().parents[1],
        vault=MemoryVault(),
        config_dir=tmp_path,
        background=False,
    )
    task = {
        "id": "task",
        "kind": "download",
        "state": "running",
        "progress": 0.21,
        "message": "model.gguf",
        "logs": [{"seq": 1, "message": "8 个连接 · 120 MiB/s", "level": "info"}],
    }
    c.update(runtime={"state": "ready", "tasks": [task]})
    task["logs"].append({"seq": 2, "message": "校验 SHA-256", "level": "info"})
    c.update(runtime={"state": "ready", "tasks": [task]})
    c.update(runtime={"state": "ready", "tasks": [task]})
    messages = [x["message"] for x in c.snapshot()["logs"]]
    assert messages.count("8 个连接 · 120 MiB/s") == 1
    assert messages.count("校验 SHA-256") == 1


def test_step_by_step_logs_for_startup_boot_stream_release_and_settings(
    tmp_path, monkeypatch
):
    import json
    from types import SimpleNamespace
    from unittest.mock import Mock
    from colab_cli.state import SessionState

    root = Path(__file__).resolve().parents[1]
    vault = MemoryVault()
    vault.set("colab", {"token": "tok"})
    c = ColabOneController(root, vault=vault, config_dir=tmp_path, background=False)
    c.auth.credentials = Mock(return_value=SimpleNamespace(token="tok"))
    c.auth.build_rclone_conf = Mock(return_value="[colabone_gdrive]\ntype = drive\n")

    acct_payload = {
        "logged_in": True,
        "email": "dev@example.com",
        "balance": 123.45,
        "rate": 10.05,
        "assignments": 1,
        "instances": [],
    }
    monkeypatch.setattr(c.provider, "account", lambda: acct_payload)

    def fake_allocate(gpu, on_log=None):
        if callable(on_log):
            on_log(f"向 Google Colab 请求分配 {gpu} GPU（默认规格）", "GPU", "info")
        sess = SessionState(
            name="colabone",
            endpoint="gpu-g4-s-kkb-usw1b1-step",
            token="tok-step",
            url="https://step.invalid",
            variant="GPU",
            accelerator=gpu,
            machine_shape="STANDARD",
        )
        c.provider.save_session(sess, activate=True)
        return sess

    monkeypatch.setattr(c.provider, "allocate", fake_allocate)
    monkeypatch.setattr(c.provider, "ready", lambda _s, on_log=None: (
        on_log("远端 Jupyter 内核已响应（耗时 1.2 秒）", "GPU", "info")
        if callable(on_log)
        else None
    ))
    monkeypatch.setattr("bridge.provider.ContentsClient", Mock(return_value=Mock()))

    boot_lines = [
        "\nCOLABONE_LOG="
        + json.dumps(
            {"message": "远端引导脚本已启动，正在读取启动配置", "source": "远端"},
            ensure_ascii=False,
        )
        + "\n",
        "\nCOLABONE_LOG="
        + json.dumps(
            {
                "message": "正在检查并安装远端服务 Python 依赖",
                "source": "远端",
                "stage": "安装远端服务依赖",
            },
            ensure_ascii=False,
        )
        + "\n",
        "\nCOLABONE_LOG="
        + json.dumps(
            {"message": "远端管理服务健康检查通过（耗时 1.5 秒）", "source": "远端"},
            ensure_ascii=False,
        )
        + "\n",
    ]

    def fake_execute(session, code, timeout=300, hook=None, output_hook=None):
        if output_hook is not None:
            # Split first log line across chunks to verify line buffering.
            output_hook({"output_type": "stream", "name": "stdout", "text": boot_lines[0][:18]})
            output_hook({"output_type": "stream", "name": "stdout", "text": boot_lines[0][18:]})
            output_hook({"output_type": "stream", "name": "stdout", "text": boot_lines[1]})
            output_hook({"output_type": "stream", "name": "stdout", "text": boot_lines[2]})
        return [{"output_type": "stream", "name": "stdout", "text": "".join(boot_lines) + "\nCOLABONE_READY=1\n"}]

    monkeypatch.setattr(c.provider, "execute", fake_execute)

    def fake_remote(method, path, body=None, endpoint=None):
        if path == "snapshot":
            return {
                "runtime": {
                    "id": "gpu-g4-s-kkb-usw1b1-step",
                    "state": "ready",
                    "protocol": 7,
                    "tasks": [],
                    "drive": {"connected": True, "mode": "mount"},
                },
                "models": c.presets(),
                "workflows": [],
                "comfy_outputs": [],
            }
        if path == "drive/connect":
            return {"connected": True, "mode": "mount", "remote_folder": "ColabOne_Drive"}
        return {"ok": True}

    c.remote = Mock(side_effect=fake_remote)
    c.start(gpu="G4")

    msgs = [item["message"] for item in c.snapshot()["logs"]]
    for expected in (
        "校验 Google 登录凭据",
        "已准备 Google 云盘自动挂载凭据",
        "查询 Google Colab 账号余额与当前运行实例",
        "正在分配 G4",
        "向 Google Colab 请求分配 G4 GPU（默认规格）",
        "已分配实例 gpu-g4-s-kkb-usw1b1-step",
        "等待远端内核",
        "远端 Jupyter 内核已响应",
        "部署远端管理服务",
        "已打包远端管理服务",
        "正在上传管理服务程序包 (colabone.zip)",
        "远端引导脚本已启动，正在读取启动配置",
        "安装远端服务依赖",
        "正在检查并安装远端服务 Python 依赖",
        "远端管理服务健康检查通过",
        "正在拉取远端实例硬件与模型快照",
        "远端 GPU 已连接",
        "正在同步空闲自动释放策略",
        "已自动挂载 Google 云盘",
        "计费同步完成",
    ):
        assert any(expected in m for m in msgs), f"missing expected log: {expected}"

    # Verify settings save logs cleanly without leaking hf_token, while settings.hf_token reveals it on demand
    assert c.command("settings.hf_token", {}) == {"token": ""}
    c.save_settings({"gpu": "G4", "idle_minutes": 45, "hf_token": "  hf_super_secret_token  "})
    assert c.command("settings.hf_token", {}) == {"token": "hf_super_secret_token"}
    snap = c.snapshot()
    assert "hf_super_secret_token" not in json.dumps(snap)
    settings_msgs = [item["message"] for item in snap["logs"]]
    assert any("已保存设置" in m and "45 分钟" in m and "Hugging Face Token" in m for m in settings_msgs)
    assert all("hf_super_secret_token" not in m for m in settings_msgs)

    # Verify release logs each step
    client = Mock()
    client.list_assignments.side_effect = [
        [SimpleNamespace(endpoint="gpu-g4-s-kkb-usw1b1-step")],
        [],
    ]
    c.provider.client_factory = lambda: client
    c.release()
    rel_msgs = [item["message"] for item in c.snapshot()["logs"]]
    for expected in (
        "正在通知远端停止任务、卸载模型并同步云盘",
        "远端收尾与云盘同步已完成",
        "向 Google Colab 发送释放请求：gpu-g4-s-kkb-usw1b1-step",
        "已确认实例停止并清理本机会话：gpu-g4-s-kkb-usw1b1-step",
    ):
        assert any(expected in m for m in rel_msgs), f"missing expected release log: {expected}"


def test_activity_persists_redacted_lines_and_rotates_on_disk(tmp_path):
    from bridge.activity import safe_message

    assert (
        safe_message(
            "Failed to issue request POST https://colab.research.google.com/tun/m/assign?nbh=abc: Service Unavailable"
        )
        == "Failed to issue request POST [链接已隐藏]: Service Unavailable"
    )

    log_file = tmp_path / "activity.log"
    log = ColabOneActivityLog(limit=10, log_path=log_file, max_bytes=16 * 1024)
    log.add(
        "Bearer super-secret-token https://example.com/?code=mycode hf_secret_abc",
        "GPU",
        "error",
    )
    assert log_file.is_file()
    content = log_file.read_text(encoding="utf-8")
    assert "[ERROR] [GPU]" in content
    assert "[链接已隐藏]" in content
    for leaked in ("super-secret-token", "mycode", "hf_secret_abc", "https://example.com"):
        assert leaked not in content

    # Trigger rotation above max_bytes (16 KiB)
    for i in range(120):
        log.add(f"entry-{i:03d} " + ("x" * 180), "GPU", "info")
    backup = tmp_path / "activity.log.1"
    assert backup.is_file()
    assert log_file.stat().st_size < 18 * 1024

    # Controller mirrors activity logs into config_dir/activity.log
    ctrl_dir = tmp_path / "ctrl"
    c = ColabOneController(
        Path(__file__).resolve().parents[1],
        vault=MemoryVault(),
        config_dir=ctrl_dir,
        background=False,
    )
    assert (ctrl_dir / "activity.log").is_file()
    assert "客户端已启动" in (ctrl_dir / "activity.log").read_text(encoding="utf-8")
