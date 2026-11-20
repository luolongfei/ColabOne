"""Headless Google Drive mount and persistence via rclone + OAuth refresh_token.

Uses the saved `drive.file` OAuth token (`[colabone_gdrive]`) to create, mount and
synchronize the app-owned `ColabOne_Drive` folder with zero per-instance browser prompts.
"""

from __future__ import annotations

import hashlib
import json
import os
import platform
import shutil
import subprocess
import threading
import time
import zipfile
from pathlib import Path

import requests

COLABONE_DRIVE_REMOTE = "colabone_gdrive"
COLABONE_DRIVE_FOLDER = "ColabOne_Drive"
COLABONE_DRIVE_SPEC = f"{COLABONE_DRIVE_REMOTE}:{COLABONE_DRIVE_FOLDER}"
DEFAULT_MOUNT_ROOT = Path("/content/drive/MyDrive")


def stage_rclone_config(stage_path: Path, data_root: Path) -> Path | None:
    """Atomically install staged rclone config with 0600 permissions and remove stage."""
    if not stage_path.exists():
        return None
    target = data_root / ".rclone.conf"
    data_root.mkdir(parents=True, exist_ok=True)
    tmp = data_root / ".rclone.conf.tmp"
    old_umask = os.umask(0o077)
    try:
        raw = stage_path.read_bytes()
        tmp.write_bytes(raw)
        tmp.chmod(0o600)
        tmp.replace(target)
        # Also mirror into ~/.config/rclone/rclone.conf inside the remote container
        # when running in Colab (/content), so standard rclone invocations work too.
        if str(stage_path).startswith("/content/"):
            user_rc_dir = Path.home() / ".config" / "rclone"
            user_rc_dir.mkdir(parents=True, mode=0o700, exist_ok=True)
            user_tmp = user_rc_dir / ".rclone.conf.tmp"
            user_dst = user_rc_dir / "rclone.conf"
            user_tmp.write_bytes(raw)
            user_tmp.chmod(0o600)
            user_tmp.replace(user_dst)
    finally:
        os.umask(old_umask)
        tmp.unlink(missing_ok=True)
        stage_path.unlink(missing_ok=True)
    return target


def ensure_rclone_binary(root: Path, catalog: Path, http=None) -> Path:
    """Return system rclone or download and SHA-256-verify the pinned release."""
    existing = shutil.which("rclone")
    if existing:
        return Path(existing)
    manifest = json.loads((catalog / "rclone.json").read_text(encoding="utf-8"))
    tag = manifest["tag"]
    machine = platform.machine().lower()
    arch = {
        "x86_64": "amd64",
        "amd64": "amd64",
        "aarch64": "arm64",
        "arm64": "arm64",
    }.get(machine)
    if platform.system() != "Linux" or not arch:
        raise RuntimeError("rclone 自动安装仅支持远端 Linux 实例")
    expected = manifest["sha256"][arch]
    bin_dir = root / "bin"
    bin_dir.mkdir(parents=True, exist_ok=True)
    binary = bin_dir / f"rclone-{tag}"
    if binary.is_file():
        return binary
    archive_name = f"rclone-{tag}-linux-{arch}.zip"
    part = bin_dir / (archive_name + ".part")
    tmp_bin = bin_dir / f".rclone-{tag}.tmp"
    urls = (
        f"https://github.com/rclone/rclone/releases/download/{tag}/{archive_name}",
        f"https://downloads.rclone.org/{tag}/{archive_name}",
    )
    own_session = http is None
    session = http or requests.Session()
    if own_session:
        session.trust_env = False
    try:
        downloaded = False
        for url in urls:
            try:
                with session.get(url, stream=True, timeout=(10, 30)) as resp:
                    resp.raise_for_status()
                    digest, size, started = hashlib.sha256(), 0, time.monotonic()
                    with part.open("wb") as out:
                        for chunk in resp.iter_content(1024 * 1024):
                            if time.monotonic() - started > 180:
                                raise RuntimeError("rclone 下载超时")
                            size += len(chunk)
                            if size > 120 * 1024**2:
                                raise RuntimeError("rclone 安装包大小异常")
                            digest.update(chunk)
                            out.write(chunk)
                if digest.hexdigest().lower() != expected.lower():
                    raise RuntimeError("rclone SHA-256 校验失败")
                downloaded = True
                break
            except Exception:
                part.unlink(missing_ok=True)
        if not downloaded:
            raise RuntimeError("无法下载并通过校验 rclone 组件")
        with zipfile.ZipFile(part, "r") as zf:
            member = next(
                (
                    m
                    for m in zf.namelist()
                    if m.endswith("/rclone") and ".." not in m.split("/")
                ),
                None,
            )
            if not member:
                raise RuntimeError("rclone 压缩包结构异常")
            with zf.open(member, "r") as src, tmp_bin.open("wb") as dst:
                shutil.copyfileobj(src, dst)
        tmp_bin.chmod(0o700)
        tmp_bin.replace(binary)
        return binary
    finally:
        part.unlink(missing_ok=True)
        tmp_bin.unlink(missing_ok=True)
        if own_session:
            session.close()


class ColabOneDrive:
    """Manages headless rclone FUSE mount and direct rclone cloud persistence."""

    def __init__(
        self,
        root: Path,
        catalog: Path,
        mount_root: Path = DEFAULT_MOUNT_ROOT,
        http=None,
    ):
        self.root = Path(root)
        self.catalog = Path(catalog)
        self.mount_root = Path(mount_root)
        self.config_path = self.root / ".rclone.conf"
        self.http = http
        self.lock = threading.RLock()
        self.mode = "disabled"  # "disabled" | "mount" | "rclone_sync"
        self.error = ""
        self.mount_proc: subprocess.Popen | None = None
        self._space_cache = {"ts": 0.0, "total_bytes": None, "free_bytes": None}

    def configured(self) -> bool:
        try:
            return (
                self.config_path.is_file()
                and f"[{COLABONE_DRIVE_REMOTE}]" in self.config_path.read_text(encoding="utf-8")
            )
        except OSError:
            return False

    def is_fuse_mounted(self) -> bool:
        try:
            if not self.mount_root.exists():
                return False
            if os.path.ismount(self.mount_root):
                return True
            parent = self.mount_root.parent
            return (
                parent.exists()
                and os.stat(self.mount_root).st_dev != os.stat(parent).st_dev
            )
        except OSError:
            return False

    def available(self) -> bool:
        if self.mode == "mount":
            return self.is_fuse_mounted() or self.configured()
        return self.mode == "rclone_sync" and self.configured()

    def _rclone_cmd(self, binary: Path, *args: str) -> list[str]:
        return [str(binary), "--config", str(self.config_path), *args]

    def _recover_stale_fuse(self):
        try:
            if self.mount_root.exists():
                os.listdir(self.mount_root)
        except OSError:
            for cmd in (
                ["fusermount3", "-uz", str(self.mount_root)],
                ["fusermount", "-uz", str(self.mount_root)],
                ["umount", "-l", str(self.mount_root)],
            ):
                if shutil.which(cmd[0]):
                    subprocess.run(
                        cmd,
                        check=False,
                        stdin=subprocess.DEVNULL,
                        stdout=subprocess.DEVNULL,
                        stderr=subprocess.DEVNULL,
                    )

    def connect(self) -> dict:
        with self.lock:
            stage_rclone_config(Path("/content/.colabone_rclone.conf"), self.root)
            if not self.configured():
                self.mode = "disabled"
                raise RuntimeError("未检测到云盘凭据配置；请确认已登录 Google 账户")
            binary = ensure_rclone_binary(self.root, self.catalog, self.http)
            self._recover_stale_fuse()
            mkdir_res = subprocess.run(
                self._rclone_cmd(binary, "mkdir", COLABONE_DRIVE_SPEC),
                check=False,
                timeout=25,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.PIPE,
                text=True,
            )
            if mkdir_res.returncode != 0:
                detail = (mkdir_res.stderr or "").strip().splitlines()[-1:] or ["授权或网络异常"]
                self.mode = "disabled"
                self.error = "Google 云盘连接失败：" + detail[0][:160]
                raise RuntimeError(self.error)

            if self.is_fuse_mounted():
                self.mode = "mount"
                self.error = ""
                return self.status()

            can_fuse = Path("/dev/fuse").exists() and bool(
                shutil.which("fusermount3") or shutil.which("fusermount")
            )
            if can_fuse:
                self.mount_root.mkdir(parents=True, exist_ok=True)
                self.mount_proc = subprocess.Popen(
                    self._rclone_cmd(
                        binary,
                        "mount",
                        COLABONE_DRIVE_SPEC,
                        str(self.mount_root),
                        "--vfs-cache-mode",
                        "writes",
                        "--vfs-write-back",
                        "3s",
                        "--dir-cache-time",
                        "15s",
                        "--drive-chunk-size",
                        "64M",
                        "--buffer-size",
                        "32M",
                        "--allow-non-empty",
                    ),
                    stdin=subprocess.DEVNULL,
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                    start_new_session=True,
                )
                for _ in range(16):
                    if self.is_fuse_mounted():
                        self.mode = "mount"
                        self.error = ""
                        return self.status()
                    if self.mount_proc.poll() is not None:
                        break
                    time.sleep(0.4)
                # Clean up empty unmounted directory so local storage is never mistaken for Drive.
                try:
                    if (
                        self.mount_root.is_dir()
                        and not self.is_fuse_mounted()
                        and not any(self.mount_root.iterdir())
                    ):
                        self.mount_root.rmdir()
                except OSError:
                    pass

            # Fallback: direct rclone cloud sync works even in containers without /dev/fuse.
            self.mode = "rclone_sync"
            self.error = ""
            return self.status()

    def pull_workspace(self, dest_dir: Path | None = None) -> bool:
        """Pull persisted models.json, workflows, and ComfyUI outputs into dest_dir."""
        with self.lock:
            if not self.available():
                return False
            target_dir = Path(dest_dir) if dest_dir is not None else self.root
            target_dir.mkdir(parents=True, exist_ok=True)
            binary = ensure_rclone_binary(self.root, self.catalog, self.http)
            res = subprocess.run(
                self._rclone_cmd(
                    binary,
                    "copy",
                    f"{COLABONE_DRIVE_SPEC}/workspace",
                    str(target_dir),
                    "--transfers",
                    "8",
                    "--checkers",
                    "8",
                    "--include",
                    "/models.json",
                    "--include",
                    "/workflows/**",
                    "--include",
                    "/comfyui-outputs/**",
                    "--exclude",
                    "**/*.part*",
                    "--exclude",
                    "**/*.tmp*",
                    "--exclude",
                    "**/.trash/**",
                ),
                check=False,
                timeout=120,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.PIPE,
                text=True,
            )
            if res.returncode != 0:
                self.error = "从 Google 云盘恢复历史数据未完成，稍后自动重试"
                return False
            self.error = ""
            return True

    def push_workspace(self) -> bool:
        """Push local models.json, workflows, and ComfyUI outputs directly to ColabOne_Drive."""
        with self.lock:
            if not self.available():
                return False
            binary = ensure_rclone_binary(self.root, self.catalog, self.http)
            res = subprocess.run(
                self._rclone_cmd(
                    binary,
                    "copy",
                    str(self.root),
                    f"{COLABONE_DRIVE_SPEC}/workspace",
                    "--transfers",
                    "8",
                    "--checkers",
                    "8",
                    "--drive-chunk-size",
                    "64M",
                    "--include",
                    "/models.json",
                    "--include",
                    "/workflows/**",
                    "--include",
                    "/comfyui-outputs/**",
                    "--exclude",
                    "**/*.part*",
                    "--exclude",
                    "**/*.tmp*",
                    "--exclude",
                    "**/.trash/**",
                ),
                check=False,
                timeout=180,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.PIPE,
                text=True,
            )
            if res.returncode != 0:
                self.error = "Google 云盘同步失败，远端本地副本仍完整保留"
                return False
            self.error = ""
            return True

    def remove_remote_workflow(self, workflow_id: str):
        with self.lock:
            if not self.available():
                return
            try:
                binary = ensure_rclone_binary(self.root, self.catalog, self.http)
                subprocess.run(
                    self._rclone_cmd(
                        binary,
                        "moveto",
                        f"{COLABONE_DRIVE_SPEC}/workspace/workflows/{workflow_id}.json",
                        f"{COLABONE_DRIVE_SPEC}/workspace/workflows/.trash/{workflow_id}.json",
                    ),
                    check=False,
                    timeout=20,
                    stdin=subprocess.DEVNULL,
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                )
            except Exception:
                pass

    def remove_remote_comfy_output(self, identity: str, extension: str):
        with self.lock:
            if not self.available():
                return
            try:
                binary = ensure_rclone_binary(self.root, self.catalog, self.http)
                for ext in dict.fromkeys((extension, "json")):
                    subprocess.run(
                        self._rclone_cmd(
                            binary,
                            "moveto",
                            f"{COLABONE_DRIVE_SPEC}/workspace/comfyui-outputs/{identity}.{ext}",
                            f"{COLABONE_DRIVE_SPEC}/workspace/comfyui-outputs/.trash/{identity}.{ext}",
                        ),
                        check=False,
                        timeout=20,
                        stdin=subprocess.DEVNULL,
                        stdout=subprocess.DEVNULL,
                        stderr=subprocess.DEVNULL,
                    )
            except Exception:
                pass

    def unmount(self):
        with self.lock:
            if self.mount_proc and self.mount_proc.poll() is None:
                self.mount_proc.terminate()
                try:
                    self.mount_proc.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    self.mount_proc.kill()
            self.mount_proc = None
            if self.is_fuse_mounted():
                for cmd in (
                    ["fusermount3", "-uz", str(self.mount_root)],
                    ["fusermount", "-uz", str(self.mount_root)],
                    ["umount", "-l", str(self.mount_root)],
                ):
                    if shutil.which(cmd[0]):
                        subprocess.run(
                            cmd,
                            check=False,
                            stdin=subprocess.DEVNULL,
                            stdout=subprocess.DEVNULL,
                            stderr=subprocess.DEVNULL,
                        )

    def _refresh_space(self):
        now = time.time()
        if now - self._space_cache["ts"] < 30:
            return
        if self.mode == "mount" and self.is_fuse_mounted():
            try:
                usage = shutil.disk_usage(self.mount_root)
                self._space_cache = {
                    "ts": now,
                    "total_bytes": usage.total,
                    "free_bytes": usage.free,
                }
                return
            except OSError:
                pass
        if self.available():
            try:
                binary = ensure_rclone_binary(self.root, self.catalog, self.http)
                res = subprocess.run(
                    self._rclone_cmd(binary, "about", f"{COLABONE_DRIVE_REMOTE}:", "--json"),
                    check=False,
                    timeout=8,
                    stdin=subprocess.DEVNULL,
                    capture_output=True,
                    text=True,
                )
                if res.returncode == 0 and res.stdout:
                    info = json.loads(res.stdout)
                    self._space_cache = {
                        "ts": now,
                        "total_bytes": info.get("total"),
                        "free_bytes": info.get("free"),
                    }
            except Exception:
                pass

    def status(self, pending: int = 0) -> dict:
        connected = self.available()
        if connected:
            self._refresh_space()
        return {
            "connected": connected,
            "configured": self.configured(),
            "mode": self.mode if connected else "disabled",
            "mount_path": str(self.mount_root)
            if self.mode == "mount" and self.is_fuse_mounted()
            else "",
            "remote_folder": COLABONE_DRIVE_FOLDER,
            "pending": pending,
            "error": self.error,
            "total_bytes": self._space_cache["total_bytes"] if connected else None,
            "free_bytes": self._space_cache["free_bytes"] if connected else None,
        }
