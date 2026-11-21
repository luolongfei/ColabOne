"""Google Drive mount and workspace persistence for ColabOne remote runtimes.

Supports two complementary modes storing data in the identical Drive layout
(`My Drive / ColabOne_Drive / workspace`):
1. `colab_drivefs`: Official Google Colab native DriveFS mounted at `/content/drive`
   (`/content/drive/MyDrive`), requiring zero GCP project or rclone download.
2. `mount` / `rclone_sync`: Headless `rclone` using the saved `drive.file` OAuth
   refresh_token (and optional `X-Goog-User-Project` quota project header when
   using Google's public ADC OAuth client).
"""

from __future__ import annotations

import hashlib
import json
import os
import platform
import re
import shutil
import subprocess
import threading
import time
import uuid
import zipfile
from pathlib import Path

import requests

COLABONE_DRIVE_REMOTE = "colabone_gdrive"
COLABONE_DRIVE_FOLDER = "ColabOne_Drive"
COLABONE_DRIVE_SPEC = f"{COLABONE_DRIVE_REMOTE}:{COLABONE_DRIVE_FOLDER}"
DEFAULT_MOUNT_ROOT = Path("/content/drive/MyDrive")
_QUOTA_PROJECT_LINE_RE = re.compile(
    r"^\s*#\s*colabone_quota_project\s*=\s*([a-z][a-z0-9-]{4,28}[a-z0-9])\s*$"
)


def parse_rclone_quota_project(config_path: Path) -> str:
    """Extract optional `# colabone_quota_project = <id>` directive from rclone config."""
    try:
        if not config_path.is_file():
            return ""
        for line in config_path.read_text(encoding="utf-8", errors="replace").splitlines()[:16]:
            match = _QUOTA_PROJECT_LINE_RE.match(line)
            if match:
                return match.group(1)
    except OSError:
        return ""
    return ""


def format_rclone_error(stderr: str) -> str:
    """Extract an actionable error summary from multi-line rclone/Google API stderr."""
    raw = (stderr or "").strip()
    if not raw:
        return "Google 云盘连接失败：授权或网络异常"
    lower = raw.lower()
    if (
        "accessnotconfigured" in lower
        or "service_disabled" in lower
        or "requires a quota project" in lower
    ):
        return "Google Drive API 需要有效的 GCP 配额项目（当前公共凭据未配置配额项目）"
    if "invalid_grant" in lower or "token has been expired or revoked" in lower:
        return "Google 云盘授权已过期，请在设置中重新登录"
    lines = [ln.strip() for ln in raw.splitlines() if ln.strip()]
    chosen = ""
    for ln in lines:
        if "CRITICAL:" in ln or "ERROR :" in ln or "Failed to " in ln:
            chosen = ln
            break
    if not chosen:
        chosen = next((ln for ln in reversed(lines) if len(ln) > 4 and not ln.startswith((",", "]", "}", "{"))), lines[-1])
    chosen = re.sub(r"^\d{4}/\d{2}/\d{2}\s+\d{2}:\d{2}:\d{2}\s+", "", chosen)
    chosen = re.sub(r"^(?:CRITICAL|ERROR)\s*:\s*", "", chosen)
    return "Google 云盘连接失败：" + chosen[:180]


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


def _is_temp_name(name: str) -> bool:
    return ".part" in name or ".tmp" in name or name.startswith(".")


_SMALL_COMPARE_BYTES = 256 * 1024


def _copy_file_if_newer(src: Path, dst: Path) -> None:
    if src.is_symlink() or not src.is_file() or dst.is_symlink():
        return
    dst.parent.mkdir(parents=True, exist_ok=True)
    src_stat = src.stat()
    if dst.is_file():
        try:
            dst_stat = dst.stat()
            if dst_stat.st_size == src_stat.st_size:
                if src_stat.st_size <= _SMALL_COMPARE_BYTES:
                    if src.read_bytes() == dst.read_bytes():
                        return
                elif dst_stat.st_mtime >= src_stat.st_mtime:
                    return
        except OSError:
            pass
    tmp = dst.parent / f".co-sync-{uuid.uuid4().hex[:8]}.tmp"
    try:
        shutil.copy2(src, tmp)
        tmp.replace(dst)
    except OSError:
        tmp.unlink(missing_ok=True)
        shutil.copy2(src, dst)


class ColabOneDrive:
    """Manages native Colab DriveFS and headless rclone Google Drive persistence."""

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
        self.mode = "disabled"  # "disabled" | "colab_drivefs" | "mount" | "rclone_sync"
        self.error = ""
        self.mount_proc: subprocess.Popen | None = None
        self._space_cache = {"ts": 0.0, "total_bytes": None, "free_bytes": None}

    def _has_rclone_config(self) -> bool:
        try:
            return (
                self.config_path.is_file()
                and f"[{COLABONE_DRIVE_REMOTE}]" in self.config_path.read_text(encoding="utf-8")
            )
        except OSError:
            return False

    def configured(self) -> bool:
        return self._has_rclone_config() or self.is_colab_drivefs_mounted()

    @staticmethod
    def _proc_mount_fstype(target: Path) -> str:
        try:
            resolved = str(target)
            for line in Path("/proc/mounts").read_text(encoding="utf-8", errors="replace").splitlines():
                parts = line.split()
                if len(parts) >= 3 and parts[1] == resolved:
                    return parts[2]
        except OSError:
            pass
        return ""

    def _active_mydrive_dir(self) -> Path:
        if self.mount_root.is_dir():
            return self.mount_root
        alt = self.mount_root.parent / "My Drive"
        if alt.is_dir():
            return alt
        return self.mount_root

    def _drivefs_workspace_dir(self) -> Path:
        return self._active_mydrive_dir() / COLABONE_DRIVE_FOLDER / "workspace"

    def is_colab_drivefs_mounted(self) -> bool:
        """Return True if official Google Colab DriveFS is mounted at mount_root.parent."""
        drivefs_root = self.mount_root.parent
        try:
            if not drivefs_root.exists():
                return False
            fstype = self._proc_mount_fstype(drivefs_root)
            is_mount = fstype.startswith("fuse.drive") or (
                os.path.ismount(drivefs_root) and not os.path.ismount(self.mount_root)
            )
            if not is_mount:
                return False
            mydrive = self._active_mydrive_dir()
            if not mydrive.is_dir():
                return False
            # Allow DriveFS-managed virtual symlinks only if they stay inside drivefs_root.
            try:
                mydrive.resolve().relative_to(drivefs_root.resolve())
            except ValueError:
                return False
            return True
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
        if self.mode == "colab_drivefs":
            return self.is_colab_drivefs_mounted()
        if self.mode == "mount":
            return self.is_fuse_mounted() or self._has_rclone_config()
        return self.mode == "rclone_sync" and self._has_rclone_config()

    def _rclone_cmd(self, binary: Path, *args: str) -> list[str]:
        cmd = [str(binary), "--config", str(self.config_path)]
        quota_project = parse_rclone_quota_project(self.config_path)
        if quota_project:
            cmd.extend(["--header", f"X-Goog-User-Project: {quota_project}"])
        cmd.extend(args)
        return cmd

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
            # 1) Prefer already-mounted official Google Colab DriveFS (zero rclone/GCP needed).
            if self.is_colab_drivefs_mounted():
                ws = self._drivefs_workspace_dir()
                try:
                    ws.mkdir(parents=True, exist_ok=True)
                    os.listdir(ws)
                except OSError as exc:
                    self.mode = "disabled"
                    self.error = f"Google 云盘目录不可读写：{exc}"
                    raise RuntimeError(self.error) from None
                self.mode = "colab_drivefs"
                self.error = ""
                return self.status()

            # 2) Otherwise use headless rclone OAuth config.
            stage_rclone_config(Path("/content/.colabone_rclone.conf"), self.root)
            if not self._has_rclone_config():
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
                self.mode = "disabled"
                self.error = format_rclone_error(mkdir_res.stderr or "")
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
                log_path = self.root / "rclone-mount.log"
                with log_path.open("wb") as log_out:
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
                        stdout=log_out,
                        stderr=log_out,
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

    def _pull_drivefs_workspace(self, target_dir: Path) -> bool:
        ws = self._drivefs_workspace_dir()
        try:
            ws.mkdir(parents=True, exist_ok=True)
            models_src = ws / "models.json"
            if models_src.is_file() and not models_src.is_symlink():
                _copy_file_if_newer(models_src, target_dir / "models.json")
            for folder in ("workflows", "comfyui-outputs"):
                src_dir = ws / folder
                if not src_dir.is_dir() or src_dir.is_symlink():
                    continue
                dst_dir = target_dir / folder
                dst_dir.mkdir(parents=True, exist_ok=True)
                for item in src_dir.iterdir():
                    if _is_temp_name(item.name) or item.is_dir() or item.is_symlink():
                        continue
                    _copy_file_if_newer(item, dst_dir / item.name)
            self.error = ""
            return True
        except OSError:
            self.error = "从 Google 云盘恢复历史数据未完成，稍后自动重试"
            return False

    def _push_drivefs_workspace(self) -> bool:
        ws = self._drivefs_workspace_dir()
        try:
            ws.mkdir(parents=True, exist_ok=True)
            models_src = self.root / "models.json"
            if models_src.is_file() and not models_src.is_symlink():
                _copy_file_if_newer(models_src, ws / "models.json")
            for folder in ("workflows", "comfyui-outputs"):
                src_dir = self.root / folder
                if not src_dir.is_dir() or src_dir.is_symlink():
                    continue
                dst_dir = ws / folder
                dst_dir.mkdir(parents=True, exist_ok=True)
                for item in src_dir.iterdir():
                    if _is_temp_name(item.name) or item.is_dir() or item.is_symlink():
                        continue
                    _copy_file_if_newer(item, dst_dir / item.name)
            self.error = ""
            return True
        except OSError:
            self.error = "Google 云盘同步失败，远端本地副本仍完整保留"
            return False

    def pull_workspace(self, dest_dir: Path | None = None) -> bool:
        """Pull persisted models.json, workflows, and ComfyUI outputs into dest_dir."""
        with self.lock:
            if not self.available():
                return False
            target_dir = Path(dest_dir) if dest_dir is not None else self.root
            target_dir.mkdir(parents=True, exist_ok=True)
            if self.mode == "colab_drivefs":
                return self._pull_drivefs_workspace(target_dir)
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
            if self.mode == "colab_drivefs":
                return self._push_drivefs_workspace()
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
                if self.mode == "colab_drivefs":
                    ws = self._drivefs_workspace_dir() / "workflows"
                    src = ws / f"{workflow_id}.json"
                    if src.is_file() and not src.is_symlink():
                        trash = ws / ".trash"
                        trash.mkdir(parents=True, exist_ok=True)
                        src.replace(trash / src.name)
                    return
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
                if self.mode == "colab_drivefs":
                    ws = self._drivefs_workspace_dir() / "comfyui-outputs"
                    trash = ws / ".trash"
                    for ext in dict.fromkeys((extension, "json")):
                        src = ws / f"{identity}.{ext}"
                        if src.is_file() and not src.is_symlink():
                            trash.mkdir(parents=True, exist_ok=True)
                            src.replace(trash / src.name)
                    return
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
            if self.mode == "colab_drivefs":
                try:
                    from google.colab import drive as colab_drive

                    colab_drive.flush_and_unmount(timeout_ms=30000)
                except Exception:
                    pass
                self.mode = "disabled"
                return
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
        if (
            (self.mode == "colab_drivefs" and self.is_colab_drivefs_mounted())
            or (self.mode == "mount" and self.is_fuse_mounted())
        ):
            try:
                usage = shutil.disk_usage(self._active_mydrive_dir())
                self._space_cache = {
                    "ts": now,
                    "total_bytes": usage.total,
                    "free_bytes": usage.free,
                }
                return
            except OSError:
                pass
        if self.available() and self.mode != "colab_drivefs":
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
        mount_path = ""
        if connected and self.mode == "colab_drivefs" and self.is_colab_drivefs_mounted():
            mount_path = str(self._active_mydrive_dir())
        elif connected and self.mode == "mount" and self.is_fuse_mounted():
            mount_path = str(self.mount_root)
        return {
            "connected": connected,
            "configured": self.configured(),
            "mode": self.mode if connected else "disabled",
            "mount_path": mount_path,
            "remote_folder": COLABONE_DRIVE_FOLDER,
            "pending": pending,
            "error": self.error,
            "total_bytes": self._space_cache["total_bytes"] if connected else None,
            "free_bytes": self._space_cache["free_bytes"] if connected else None,
        }
