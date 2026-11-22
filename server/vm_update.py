# Runs in the authenticated Colab kernel. Preserve data and the original budget guard.
import hashlib
import json
import os
import shutil
import signal
import subprocess
import sys
import time
import urllib.request
import zipfile
from pathlib import Path


def colabone_update():
    def emit_log(message, source="远端", level="info"):
        payload = {"message": str(message), "source": source, "level": level}
        print("\nCOLABONE_LOG=" + json.dumps(payload, ensure_ascii=False), flush=True)

    emit_log("远端更新脚本已启动，正在校验当前服务状态")
    cfg_path = Path("/content/colabone-boot.json")
    cfg = json.loads(cfg_path.read_text())
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    req = urllib.request.Request(
        "http://127.0.0.1:7860/api/v1/status",
        headers={"Authorization": "Bearer " + cfg["token"]},
    )
    with opener.open(req, timeout=10) as response:
        status = json.load(response)
    if (
        any(t["state"] == "running" for t in status.get("tasks", []))
        or status.get("loaded_model")
        or status.get("active_engine")
    ):
        raise RuntimeError("请等待任务结束并卸载模型或停止 ComfyUI 后再更新远端服务")
    if cfg.get("deadline", 0) > 0 and cfg["deadline"] - time.time() < 90:
        raise RuntimeError("预算保护即将释放实例，新服务会在下次启动时生效")
    processes = []
    for item in Path("/proc").glob("[0-9]*/cmdline"):
        try:
            args = item.read_bytes().split(b"\0")
            if b"-m" in args and b"server.app" in args:
                env = (item.parent / "environ").read_bytes().split(b"\0")
                if b"COLABONE_REMOTE_CONFIG=/content/colabone-boot.json" in env:
                    processes.append(int(item.parent.name))
        except (OSError, ProcessLookupError):
            pass
    if len(processes) != 1:
        raise RuntimeError("无法确认远端管理服务进程，未执行更新")
    emit_log("已确认当前空闲并定位旧服务进程，正在解压更新包")
    stage = Path("/content/colabone-update")
    shutil.rmtree(stage, ignore_errors=True)
    stage.mkdir()
    with zipfile.ZipFile("/content/colabone-update.zip") as archive:
        archive.extractall(stage)
    staged_root = stage / "colabone"
    staged_reqs = staged_root / "server/requirements.txt"
    if not staged_root.is_dir() or not staged_reqs.is_file():
        raise RuntimeError("更新包结构无效，未执行更新")
    rev_hasher = hashlib.sha256()
    rev_count = 0
    for folder in ("server", "common", "catalog"):
        folder_path = staged_root / folder
        if not folder_path.is_dir():
            continue
        for item in sorted(folder_path.glob("*"), key=lambda p: p.name):
            if item.is_file() and item.suffix in (".py", ".json", ".txt"):
                rev_hasher.update(f"{folder}/{item.name}".encode("utf-8"))
                rev_hasher.update(b"\0")
                rev_hasher.update(item.read_bytes())
                rev_hasher.update(b"\0")
                rev_count += 1
    expected_rev = rev_hasher.hexdigest()[:16] if rev_count else ""
    current_reqs = Path("/content/colabone/server/requirements.txt")
    reqs_unchanged = False
    try:
        reqs_unchanged = (
            current_reqs.is_file()
            and current_reqs.read_bytes() == staged_reqs.read_bytes()
        )
    except OSError:
        reqs_unchanged = False
    if reqs_unchanged:
        emit_log("依赖清单未变化，跳过重复安装，正在平滑停止旧管理服务")
    else:
        emit_log("更新包解压完成，正在校验并安装新版服务依赖")
        # Install locked server dependencies before stopping the working service.
        subprocess.run(
            [
                sys.executable,
                "-m",
                "pip",
                "install",
                "--disable-pip-version-check",
                "--require-hashes",
                "-r",
                str(staged_reqs),
            ],
            check=True,
            timeout=120,
        )
        emit_log("新版服务依赖已就绪，正在平滑停止旧管理服务")
    if cfg.get("deadline", 0) > 0 and cfg["deadline"] - time.time() < 60:
        raise RuntimeError("预算保护即将释放实例，未替换运行中的服务")
    backup = Path("/content/colabone-previous")
    shutil.rmtree(backup, ignore_errors=True)
    pid = processes[0]
    os.kill(pid, signal.SIGTERM)
    for _ in range(450):
        if not Path(f"/proc/{pid}").exists():
            break
        # A parent may not reap immediately; zombies no longer hold the port.
        try:
            if Path(f"/proc/{pid}/stat").read_text().split()[2] == "Z":
                break
        except OSError:
            break
        time.sleep(0.1)
    else:
        raise RuntimeError("旧服务未退出，未替换代码，请稍后重试")
    emit_log("旧服务已退出，正在切换新版代码并重启管理服务")
    Path("/content/colabone").rename(backup)
    try:
        staged_root.rename("/content/colabone")
    except BaseException:
        if not Path("/content/colabone").exists() and backup.exists():
            backup.rename("/content/colabone")
        raise
    env = dict(
        os.environ,
        PYTHONPATH="/content/colabone",
        COLABONE_REMOTE_CONFIG=str(cfg_path),
        COLABONE_RUNTIME_ID=cfg["runtime_id"],
        PYTHONUNBUFFERED="1",
    )

    def launch():
        with open("/content/colabone-server.log", "a") as log:
            return subprocess.Popen(
                [sys.executable, "-m", "server.app"],
                cwd="/content/colabone",
                env=env,
                stdin=subprocess.DEVNULL,
                stdout=log,
                stderr=log,
                start_new_session=True,
            )

    proc = launch()
    emit_log("新版服务已拉起，等待健康检查通过")
    for _ in range(60):
        if proc.poll() is not None:
            break
        try:
            with opener.open(req, timeout=1) as response:
                data = json.load(response)
                if data.get("protocol", 0) >= 7 and (
                    not expected_rev or data.get("service_revision") == expected_rev
                ):
                    emit_log("新版远端管理服务健康检查通过")
                    print("COLABONE_UPDATED=1", flush=True)
                    return
        except Exception:
            pass
        time.sleep(0.5)
    emit_log("新版服务未能在超时前就绪，正在回滚至旧版服务", level="warning")
    server_log = Path("/content/colabone-server.log")
    tail = ""
    try:
        if server_log.is_file():
            tail = server_log.read_text(encoding="utf-8", errors="replace")[-400:].strip()
    except OSError:
        pass
    if proc.poll() is None:
        proc.terminate()
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill()
            try:
                proc.wait(timeout=3)
            except subprocess.TimeoutExpired:
                pass
    shutil.rmtree("/content/colabone", ignore_errors=True)
    backup.rename("/content/colabone")
    rollback_proc = launch()
    rollback_ok = False
    for _ in range(30):
        if rollback_proc.poll() is not None:
            break
        try:
            opener.open("http://127.0.0.1:7860/health", timeout=1).close()
            rollback_ok = True
            break
        except Exception:
            time.sleep(0.5)
    base_msg = (
        "新服务未就绪，已恢复旧服务；模型文件与预算保持不变"
        if rollback_ok
        else "新服务未就绪且回滚后健康检查未通过，请重新连接或释放实例"
    )
    raise RuntimeError(base_msg + (f"（日志：{tail}）" if tail else ""))


colabone_update()
