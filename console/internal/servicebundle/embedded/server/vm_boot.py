# Executed inside the Colab kernel. It contains no secrets: those arrive via ContentsClient.
# The control service is loopback-only; the authenticated Colab kernel carries RPC.
import json
import os
import shutil
import subprocess
import sys
import threading
import time
import urllib.request
import zipfile
from pathlib import Path


def colabone_boot():
    print_lock = threading.Lock()

    def emit_log(message, source="远端", level="info", stage=None):
        payload = {"message": str(message), "source": source, "level": level}
        if stage:
            payload["stage"] = str(stage)
        line = "\nCOLABONE_LOG=" + json.dumps(payload, ensure_ascii=False)
        with print_lock:
            print(line, flush=True)

    emit_log("远端引导脚本已启动，正在读取启动配置")
    config_path = Path("/content/colabone-boot.json")
    config = json.loads(config_path.read_text(encoding="utf-8"))
    # Require the runtime release endpoint before dependency installation or networking.
    addr = os.environ.get("TBE_RUNTIME_ADDR", "")
    if not addr:
        raise RuntimeError(
            "Colab runtime release endpoint unavailable; refusing unprotected startup"
        )
    emit_log("已校验 Colab 运行时释放保护接口")
    if config.get("deadline", 0) > 0:
        guard = (
            "import json,time,urllib.request\n"
            "from pathlib import Path\n"
            "cfg_path=Path(%r)\n"
            "fallback_deadline=%r\n"
            "unassign_url=%r\n"
            "while True:\n"
            " dl=fallback_deadline\n"
            " try:\n"
            "  d=json.loads(cfg_path.read_text(encoding='utf-8')).get('deadline')\n"
            "  if d is not None: dl=float(d)\n"
            " except Exception:\n"
            "  pass\n"
            " if dl<=0:\n"
            "  time.sleep(30)\n"
            "  continue\n"
            " rem=(dl+40)-time.time()\n"
            " if rem<=0:\n"
            "  break\n"
            " time.sleep(min(30,rem))\n"
            "while True:\n"
            " try:\n"
            "  urllib.request.build_opener(urllib.request.ProxyHandler({})).open(urllib.request.Request(unassign_url,data=b'',method='POST'),timeout=15)\n"
            "  break\n"
            " except Exception: time.sleep(5)\n"
        ) % (str(config_path), float(config["deadline"]), "http://" + addr + "/unassign")
        subprocess.Popen(
            [sys.executable, "-c", guard],
            stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            start_new_session=True,
        )
        emit_log("已启动远端自动释放保护守护进程")
    emit_log("正在解压管理服务程序包")
    shutil.rmtree("/content/colabone", ignore_errors=True)
    with zipfile.ZipFile("/content/colabone.zip") as archive:
        archive.extractall("/content")
    boot_root = Path(config["root"])
    boot_root.mkdir(parents=True, exist_ok=True)
    emit_log("管理服务解压完成，工作目录已初始化")
    telemetry_stop = threading.Event()
    telemetry_thread = None
    try:
        if "/content/colabone" not in sys.path:
            sys.path.insert(0, "/content/colabone")
        from server.telemetry import ColabOneResourceSampler

        boot_sampler = ColabOneResourceSampler()

        def _emit_boot_telemetry():
            while not telemetry_stop.is_set():
                try:
                    sample = boot_sampler.sample(boot_root)
                    payload = "\nCOLABONE_TELEMETRY=" + json.dumps(
                        sample, ensure_ascii=False
                    )
                    with print_lock:
                        if telemetry_stop.is_set():
                            break
                        print(payload, flush=True)
                except Exception:
                    pass
                if telemetry_stop.wait(1.5):
                    break

        telemetry_thread = threading.Thread(target=_emit_boot_telemetry, daemon=True)
        telemetry_thread.start()
        emit_log("已启动远端硬件实时监控采样")
    except Exception:
        pass
    try:
        emit_log("正在检查并安装远端服务 Python 依赖", stage="安装远端服务依赖")
        pip_started = time.monotonic()
        pip_last_note = pip_started
        pip_log = Path("/content/colabone-pip.log")
        with pip_log.open("wb") as out:
            pip_proc = subprocess.Popen(
                [
                    sys.executable,
                    "-m",
                    "pip",
                    "install",
                    "--quiet",
                    "-r",
                    "/content/colabone/server/requirements.txt",
                ],
                stdin=subprocess.DEVNULL,
                stdout=out,
                stderr=subprocess.STDOUT,
            )
        while pip_proc.poll() is None:
            now = time.monotonic()
            if now - pip_started > 180:
                pip_proc.terminate()
                try:
                    pip_proc.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    pip_proc.kill()
                raise RuntimeError("远端服务依赖安装超时（超过 180 秒）")
            if now - pip_last_note >= 10:
                pip_last_note = now
                emit_log(f"仍在安装远端服务依赖（已耗时 {int(now - pip_started)} 秒）")
            time.sleep(0.5)
        if pip_proc.returncode != 0:
            tail = ""
            if pip_log.is_file():
                tail = pip_log.read_text(encoding="utf-8", errors="replace")[-600:].strip()
            raise RuntimeError(
                "远端服务依赖安装失败" + (f"：{tail}" if tail else "")
            )
        emit_log(f"远端服务依赖已就绪（耗时 {time.monotonic() - pip_started:.1f} 秒）")
        config_path.write_text(json.dumps(config))
        config_path.chmod(0o600)
        rc_stage = Path("/content/.colabone_rclone.conf")
        if rc_stage.exists():
            emit_log("正在部署 Google 云盘 rclone 凭据", source="云盘")
            if "/content/colabone" not in sys.path:
                sys.path.insert(0, "/content/colabone")
            from server.drive import stage_rclone_config

            stage_rclone_config(rc_stage, boot_root)
            emit_log("Google 云盘凭据已写入安全权限目录", source="云盘")
        env = dict(
            os.environ,
            PYTHONPATH="/content/colabone",
            COLABONE_REMOTE_CONFIG=str(config_path),
            COLABONE_RUNTIME_ID=config["runtime_id"],
            PYTHONUNBUFFERED="1",
        )
        emit_log("正在启动远端管理服务进程 (127.0.0.1:7860)", stage="启动远端管理服务")
        with open("/content/colabone-server.log", "w") as log:
            proc = subprocess.Popen(
                [sys.executable, "-m", "server.app"],
                cwd="/content/colabone",
                env=env,
                stdin=subprocess.DEVNULL,
                stdout=log,
                stderr=log,
                start_new_session=True,
            )
        emit_log("远端管理服务进程已拉起，等待健康检查通过")
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        server_log = Path("/content/colabone-server.log")

        def _server_log_tail():
            try:
                if server_log.is_file():
                    return server_log.read_text(encoding="utf-8", errors="replace")[-600:].strip()
            except OSError:
                pass
            return ""

        status_req = urllib.request.Request(
            "http://127.0.0.1:7860/api/v1/status",
            headers={"Authorization": "Bearer " + config["token"]},
        )
        health_started = time.monotonic()
        health_last_note = health_started
        for _ in range(60):
            if proc.poll() is not None:
                tail = _server_log_tail()
                raise RuntimeError(
                    "ColabOne service failed to start" + (f": {tail}" if tail else "")
                )
            try:
                with opener.open(status_req, timeout=2) as resp:
                    status_payload = json.load(resp)
                if status_payload.get("id") == config["runtime_id"]:
                    emit_log(
                        f"远端管理服务健康检查通过（耗时 {time.monotonic() - health_started:.1f} 秒）"
                    )
                    break
            except Exception:
                now = time.monotonic()
                if now - health_last_note >= 6:
                    health_last_note = now
                    emit_log(
                        f"正在等待远端管理服务就绪（已耗时 {int(now - health_started)} 秒）"
                    )
                time.sleep(0.5)
        else:
            if proc.poll() is None:
                proc.terminate()
                try:
                    proc.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    try:
                        proc.wait(timeout=3)
                    except subprocess.TimeoutExpired:
                        pass
            tail = _server_log_tail()
            raise RuntimeError(
                "ColabOne service readiness timeout" + (f": {tail}" if tail else "")
            )
    finally:
        with print_lock:
            telemetry_stop.set()
        if telemetry_thread is not None:
            telemetry_thread.join(timeout=4)
    with print_lock:
        print("\nCOLABONE_READY=1", flush=True)


colabone_boot()
