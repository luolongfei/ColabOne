# Runs in the authenticated Colab kernel. Preserve data and the original budget guard.
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
    cfg_path = Path("/content/colabone-boot.json")
    cfg = json.loads(cfg_path.read_text())
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    req = urllib.request.Request(
        "http://127.0.0.1:7860/api/v1/status",
        headers={"Authorization": "Bearer " + cfg["token"]},
    )
    with opener.open(req, timeout=10) as response:
        status = json.load(response)
    if any(t["state"] == "running" for t in status.get("tasks", [])) or status.get(
        "loaded_model"
    ):
        raise RuntimeError("请等待任务结束并卸载模型后再更新远端服务")
    if cfg["deadline"] - time.time() < 90:
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
    stage = Path("/content/colabone-update")
    shutil.rmtree(stage, ignore_errors=True)
    stage.mkdir()
    with zipfile.ZipFile("/content/colabone-update.zip") as archive:
        archive.extractall(stage)
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
            str(stage / "colabone/server/requirements.txt"),
        ],
        check=True,
        timeout=120,
    )
    if cfg["deadline"] - time.time() < 60:
        raise RuntimeError("预算保护即将释放实例，未替换运行中的服务")
    backup = Path("/content/colabone-previous")
    shutil.rmtree(backup, ignore_errors=True)
    pid = processes[0]
    os.kill(pid, signal.SIGTERM)
    for _ in range(100):
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
    Path("/content/colabone").rename(backup)
    (stage / "colabone").rename("/content/colabone")
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
    for _ in range(60):
        if proc.poll() is not None:
            break
        try:
            with opener.open(req, timeout=1) as response:
                if json.load(response).get("protocol", 0) >= 3:
                    print("COLABONE_UPDATED=1", flush=True)
                    return
        except Exception:
            time.sleep(0.5)
    if proc.poll() is None:
        proc.terminate()
        proc.wait(timeout=10)
    shutil.rmtree("/content/colabone")
    backup.rename("/content/colabone")
    launch()
    raise RuntimeError("新服务未就绪，已恢复旧服务；模型文件与预算保持不变")


colabone_update()
