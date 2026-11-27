# Runs in the authenticated Colab kernel. Preserve data and the original budget guard.
import errno
import hashlib
import json
import os
import secrets
import shutil
import signal
import stat
import subprocess
import sys
import time
import urllib.request
import zipfile
from pathlib import Path


def _remove_path(path: Path) -> None:
    if path.is_symlink() or path.is_file():
        try:
            path.unlink(missing_ok=True)
        except OSError:
            pass
    elif path.is_dir():
        shutil.rmtree(path, ignore_errors=True)


def _move_tree(src: Path, dst: Path) -> None:
    try:
        src.rename(dst)
    except OSError as exc:
        if exc.errno == errno.EXDEV:
            shutil.move(str(src), str(dst))
        else:
            raise


def colabone_update():
    def emit_log(message, source="远端", level="info"):
        payload = {"message": str(message), "source": source, "level": level}
        print("\nCOLABONE_LOG=" + json.dumps(payload, ensure_ascii=False), flush=True)

    def _has_server_app_cmdline(args):
        clean_args = [part for part in args if part]
        return any(
            clean_args[i] == b"-m" and clean_args[i + 1] == b"server.app"
            for i in range(len(clean_args) - 1)
        )

    def _read_proc_state_and_starttime(pid):
        stat_text = Path(f"/proc/{pid}/stat").read_text(encoding="utf-8", errors="replace")
        after_comm = stat_text.rsplit(")", 1)[1].split()
        if len(after_comm) <= 19:
            raise OSError("unexpected /proc stat format")
        # Field 3 is process state (index 0 after comm); field 22 is starttime (index 19).
        return after_comm[0], after_comm[19]

    def _check_status_idle(status_obj):
        if status_obj.get("state") == "releasing":
            raise RuntimeError("实例正在释放，未执行更新")
        if (
            any(t.get("state") == "running" for t in status_obj.get("tasks", []))
            or status_obj.get("loaded_model")
            or status_obj.get("active_engine")
        ):
            raise RuntimeError("请等待任务结束并卸载模型或停止 ComfyUI 后再更新远端服务")

    emit_log("远端更新脚本已启动，正在校验当前服务状态")
    cfg_path = Path("/content/colabone-boot.json")
    cfg = json.loads(cfg_path.read_text(encoding="utf-8"))
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    req = urllib.request.Request(
        "http://127.0.0.1:7860/api/v1/status",
        headers={"Authorization": "Bearer " + cfg["token"]},
    )
    with opener.open(req, timeout=10) as response:
        status = json.load(response)
    _check_status_idle(status)
    if cfg.get("deadline", 0) > 0 and cfg["deadline"] - time.time() < 90:
        raise RuntimeError("远端自动释放保护即将释放实例，新服务会在下次启动时生效")
    processes = []
    for item in Path("/proc").glob("[0-9]*/cmdline"):
        try:
            args = item.read_bytes().split(b"\0")
            if _has_server_app_cmdline(args):
                env = (item.parent / "environ").read_bytes().split(b"\0")
                if b"COLABONE_REMOTE_CONFIG=/content/colabone-boot.json" in env:
                    pid_val = int(item.parent.name)
                    _, start_ticks = _read_proc_state_and_starttime(pid_val)
                    processes.append((pid_val, start_ticks))
        except (OSError, ProcessLookupError, ValueError, IndexError):
            pass
    if len(processes) != 1:
        raise RuntimeError("无法确认远端管理服务进程，未执行更新")
    emit_log("已确认当前空闲并定位旧服务进程，正在解压更新包")
    stage = Path("/content/colabone-update")
    _remove_path(stage)
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
        pip_env = {
            k: v
            for k, v in os.environ.items()
            if not k.upper().startswith(("PIP_", "HF_", "TUNNEL_", "RCLONE_"))
            and k.upper()
            not in (
                "COLABONE_REMOTE_CONFIG",
                "OPENAI_API_KEY",
                "HUGGING_FACE_HUB_TOKEN",
                "HUGGINGFACE_HUB_TOKEN",
                "TBE_RUNTIME_ADDR",
                "PYTHONUSERBASE",
                "PYTHONPATH",
                "PYTHONNOUSERSITE",
            )
        }
        pip_proc = subprocess.Popen(
            [
                sys.executable,
                "-m",
                "pip",
                "install",
                "--isolated",
                "--no-input",
                "--disable-pip-version-check",
                "--require-hashes",
                "-r",
                str(staged_reqs),
            ],
            stdin=subprocess.DEVNULL,
            env=pip_env,
            start_new_session=True,
        )
        try:
            pip_rc = pip_proc.wait(timeout=120)
        except BaseException:
            try:
                if pip_proc.poll() is None:
                    os.killpg(pip_proc.pid, signal.SIGTERM)
                    pip_proc.wait(timeout=3)
            except Exception:
                try:
                    os.killpg(pip_proc.pid, signal.SIGKILL)
                except Exception:
                    pass
                try:
                    pip_proc.wait(timeout=3)
                except Exception:
                    pass
            raise
        if pip_rc != 0:
            raise subprocess.CalledProcessError(pip_rc, pip_proc.args)
        emit_log("新版服务依赖已就绪，正在平滑停止旧管理服务")
    cfg = json.loads(cfg_path.read_text(encoding="utf-8"))
    if cfg.get("deadline", 0) > 0 and cfg["deadline"] - time.time() < 60:
        raise RuntimeError("远端自动释放保护即将释放实例，未替换运行中的服务")
    req = urllib.request.Request(
        "http://127.0.0.1:7860/api/v1/status",
        headers={"Authorization": "Bearer " + cfg["token"]},
    )
    with opener.open(req, timeout=10) as response:
        status = json.load(response)
    _check_status_idle(status)
    backup = Path("/content/colabone-previous")
    _remove_path(backup)
    pid, orig_starttime = processes[0]
    try:
        cur_args = Path(f"/proc/{pid}/cmdline").read_bytes().split(b"\0")
        cur_env = Path(f"/proc/{pid}/environ").read_bytes().split(b"\0")
        _, cur_starttime = _read_proc_state_and_starttime(pid)
        if not (
            _has_server_app_cmdline(cur_args)
            and b"COLABONE_REMOTE_CONFIG=/content/colabone-boot.json" in cur_env
            and cur_starttime == orig_starttime
        ):
            raise RuntimeError("远端管理服务进程已变化，未执行更新")
    except (OSError, ProcessLookupError, IndexError):
        raise RuntimeError("远端管理服务进程已退出或不可访问，未执行更新") from None
    os.kill(pid, signal.SIGTERM)
    for _ in range(450):
        if not Path(f"/proc/{pid}").exists():
            break
        # A parent may not reap immediately; zombies no longer hold the port.
        # If starttime changed, the original PID exited and was recycled.
        try:
            proc_state, loop_starttime = _read_proc_state_and_starttime(pid)
            if proc_state == "Z" or loop_starttime != orig_starttime:
                break
        except (OSError, IndexError):
            break
        time.sleep(0.1)
    else:
        raise RuntimeError("旧服务未退出，未替换代码，请稍后重试")
    emit_log("旧服务已退出，正在切换新版代码并重启管理服务")
    env = dict(
        os.environ,
        PYTHONPATH="/content/colabone",
        COLABONE_REMOTE_CONFIG=str(cfg_path),
        COLABONE_RUNTIME_ID=cfg["runtime_id"],
        PYTHONUNBUFFERED="1",
    )

    def launch():
        log_path = Path("/content/colabone-server.log")
        if log_path.is_symlink():
            raise OSError("refusing symlink log file")
        log_fd = os.open(
            str(log_path),
            os.O_WRONLY | os.O_CREAT | os.O_APPEND | getattr(os, "O_NOFOLLOW", 0),
            0o600,
        )
        try:
            if hasattr(os, "fchmod"):
                os.fchmod(log_fd, 0o600)
            log_file = os.fdopen(log_fd, "a", encoding="utf-8")
        except BaseException:
            os.close(log_fd)
            raise
        with log_file as log:
            return subprocess.Popen(
                [sys.executable, "-m", "server.app"],
                cwd="/content/colabone",
                env=env,
                stdin=subprocess.DEVNULL,
                stdout=log,
                stderr=log,
                start_new_session=True,
            )

    current_path = Path("/content/colabone")
    try:
        _move_tree(current_path, backup)
        _move_tree(staged_root, current_path)
    except BaseException:
        if not current_path.exists() and backup.exists():
            try:
                _move_tree(backup, current_path)
            except OSError:
                pass
        if current_path.exists():
            try:
                launch()
            except OSError:
                pass
        raise

    proc = None
    launch_err = ""
    try:
        proc = launch()
    except OSError as exc:
        launch_err = str(exc) or type(exc).__name__

    if proc is not None:
        emit_log("新版服务已拉起，等待健康检查通过")
        health_ok = False
        deadline = time.monotonic() + 30.0
        while True:
            if proc.poll() is not None:
                break
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                break
            try:
                if not health_ok:
                    opener.open(
                        "http://127.0.0.1:7860/health",
                        timeout=min(5.0, max(0.5, remaining)),
                    ).close()
                    health_ok = True
                status_remaining = deadline - time.monotonic()
                if status_remaining <= 0:
                    break
                with opener.open(
                    req, timeout=min(5.0, max(0.5, status_remaining))
                ) as response:
                    data = json.load(response)
                    if data.get("protocol", 0) >= 7 and (
                        not expected_rev or data.get("service_revision") == expected_rev
                    ):
                        emit_log("新版远端管理服务健康检查通过")
                        print("COLABONE_UPDATED=1", flush=True)
                        return
            except Exception:
                pass
            sleep_remaining = deadline - time.monotonic()
            if sleep_remaining <= 0:
                break
            time.sleep(min(0.5, sleep_remaining))

    emit_log("新版服务未能在超时前就绪，正在回滚至旧版服务", level="warning")
    server_log = Path("/content/colabone-server.log")
    tail = ""
    try:
        if not server_log.is_symlink():
            log_fd = os.open(
                str(server_log),
                os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0),
            )
            try:
                st = os.fstat(log_fd)
                if stat.S_ISREG(st.st_mode):
                    if st.st_size > 8192:
                        os.lseek(log_fd, -8192, os.SEEK_END)
                    with os.fdopen(log_fd, "rb") as lf:
                        log_fd = -1
                        raw_tail = lf.read(8192)
                    tail = raw_tail.decode("utf-8", errors="replace")[-400:].strip()
            finally:
                if log_fd >= 0:
                    os.close(log_fd)
    except OSError:
        pass
    if proc is not None:
        pid_to_kill = proc.pid
        own_pgrp = os.getpgrp() if hasattr(os, "getpgrp") else None
        can_killpg = (
            isinstance(pid_to_kill, int)
            and pid_to_kill > 0
            and hasattr(os, "killpg")
            and pid_to_kill != own_pgrp
        )
        leader_alive = proc.poll() is None
        if leader_alive or can_killpg:
            skip_pg = False
            if not leader_alive and can_killpg:
                try:
                    os.kill(pid_to_kill, 0)
                    skip_pg = True
                except ProcessLookupError:
                    skip_pg = False
                except OSError:
                    skip_pg = True
            if can_killpg and not skip_pg:
                try:
                    os.killpg(pid_to_kill, signal.SIGTERM)
                except OSError:
                    pass
            elif leader_alive:
                try:
                    proc.terminate()
                except OSError:
                    pass
            if leader_alive:
                try:
                    proc.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    if can_killpg:
                        try:
                            os.killpg(pid_to_kill, signal.SIGKILL)
                        except OSError:
                            pass
                    else:
                        try:
                            proc.kill()
                        except OSError:
                            pass
                    try:
                        proc.wait(timeout=3)
                    except subprocess.TimeoutExpired:
                        pass
            elif can_killpg and not skip_pg:
                kill_deadline = time.monotonic() + 2.0
                while time.monotonic() < kill_deadline:
                    try:
                        os.killpg(pid_to_kill, 0)
                    except ProcessLookupError:
                        break
                    except OSError:
                        break
                    time.sleep(0.05)
                try:
                    os.killpg(pid_to_kill, signal.SIGKILL)
                except OSError:
                    pass
    current_dir = Path("/content/colabone")
    failed_dir = Path(
        f"/content/colabone-failed-{os.getpid()}-{int(time.time())}-{secrets.token_hex(4)}"
    )
    if current_dir.exists() or current_dir.is_symlink():
        try:
            _move_tree(current_dir, failed_dir)
        except OSError:
            _remove_path(current_dir)
    _move_tree(backup, current_dir)
    if failed_dir.exists() or failed_dir.is_symlink():
        _remove_path(failed_dir)
    rollback_ok = False
    try:
        rollback_proc = launch()
    except OSError:
        rollback_proc = None
    if rollback_proc is not None:
        rollback_deadline = time.monotonic() + 15.0
        while True:
            if rollback_proc.poll() is not None:
                break
            remaining = rollback_deadline - time.monotonic()
            if remaining <= 0:
                break
            try:
                opener.open(
                    "http://127.0.0.1:7860/health",
                    timeout=min(5.0, max(0.5, remaining)),
                ).close()
                rollback_ok = True
                break
            except Exception:
                sleep_remaining = rollback_deadline - time.monotonic()
                if sleep_remaining <= 0:
                    break
                time.sleep(min(0.5, sleep_remaining))
    base_msg = (
        "新服务未就绪，已恢复旧服务；模型文件与运行配置保持不变"
        if rollback_ok
        else "新服务未就绪且回滚后健康检查未通过，请重新连接或释放实例"
    )
    detail_text = launch_err or tail
    raise RuntimeError(base_msg + (f"（日志：{detail_text}）" if detail_text else ""))


colabone_update()
