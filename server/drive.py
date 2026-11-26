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
from http.cookiejar import DefaultCookiePolicy
from pathlib import Path, PurePosixPath
from urllib.parse import quote as _url_quote

import requests


def _create_http_session() -> requests.Session:
    session = requests.Session()
    session.trust_env = False
    session.cookies.set_policy(DefaultCookiePolicy(allowed_domains=[]))
    return session

COLABONE_DRIVE_REMOTE = "colabone_gdrive"
COLABONE_DRIVE_FOLDER = "ColabOne_Drive"
COLABONE_DRIVE_OUTPUTS_SUBDIR = "outputs"
COLABONE_DRIVE_SPEC = f"{COLABONE_DRIVE_REMOTE}:{COLABONE_DRIVE_FOLDER}"
DEFAULT_MOUNT_ROOT = Path("/content/drive/MyDrive")
_QUOTA_PROJECT_LINE_RE = re.compile(
    r"^\s*#\s*colabone_quota_project\s*=\s*([a-z][a-z0-9-]{4,28}[a-z0-9])\s*$"
)
_EXPORT_EXT_RE = re.compile(r"^[a-z0-9]{1,10}$")
_DRIVE_FILE_ID_RE = re.compile(r"^[A-Za-z0-9._-]{4,256}$")
_LIVE_AVATAR_ID_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$", re.ASCII)
_FORBIDDEN_EXPORT_CHARS_RE = re.compile(r'[<>:"/\\|?*\x00-\x1f\x7f]+')
_WINDOWS_RESERVED_STEMS = frozenset(
    {
        "CON",
        "PRN",
        "AUX",
        "NUL",
        *(f"COM{i}" for i in range(1, 10)),
        *(f"LPT{i}" for i in range(1, 10)),
    }
)
_EXPORT_STREAM_CHUNK_BYTES = 1024 * 1024


def sanitize_drive_export_filename(
    filename: str, output_id: str, extension: str
) -> str:
    """Return a cross-platform safe basename guaranteed to end with `.<extension>`."""
    ext = str(extension or "").lstrip(".").lower()
    if not _EXPORT_EXT_RE.fullmatch(ext):
        raise ValueError("输出文件格式不合法")
    raw = str(filename or "").replace("\\", "/").replace("\x00", "")
    base = PurePosixPath(raw).name if raw else ""
    base = _FORBIDDEN_EXPORT_CHARS_RE.sub("_", base).strip().strip(". ")
    while ".." in base:
        base = base.replace("..", "_")
    base = base.strip(". ")
    suffix = f".{ext}"
    if base.lower().endswith(suffix):
        stem = base[: -len(suffix)]
    else:
        stem = base
    stem = stem.strip(". ")[:180].rstrip(". ")
    if not stem or stem in (".", "..") or not stem.strip("_"):
        short_id = re.sub(r"[^a-zA-Z0-9]", "", str(output_id or ""))[:8]
        stem = f"colabone-{short_id}" if short_id else "colabone-output"
    elif stem.split(".", 1)[0].upper() in _WINDOWS_RESERVED_STEMS:
        stem = f"colabone-{stem}"[:180].rstrip(". ")
    return f"{stem}.{ext}"


def _hash_file(path: Path, algorithm: str = "sha256") -> str:
    hasher = hashlib.new(algorithm)
    with path.open("rb") as stream:
        while True:
            chunk = stream.read(_EXPORT_STREAM_CHUNK_BYTES)
            if not chunk:
                break
            hasher.update(chunk)
    return hasher.hexdigest().lower()


def _stream_write_file(src: Path, target: Path) -> None:
    if target.is_symlink():
        raise OSError("refusing symlink target")
    fd = os.open(
        target,
        os.O_WRONLY | os.O_CREAT | os.O_TRUNC | getattr(os, "O_NOFOLLOW", 0),
        0o666,
    )
    try:
        writer = os.fdopen(fd, "wb")
    except BaseException:
        os.close(fd)
        raise
    with src.open("rb") as reader, writer:
        while True:
            chunk = reader.read(_EXPORT_STREAM_CHUNK_BYTES)
            if not chunk:
                break
            writer.write(chunk)
        writer.flush()
        try:
            os.fsync(writer.fileno())
        except OSError:
            pass


def _copy_file_stream(src: Path, dst: Path) -> None:
    """Stream-copy src into dst with fsync, preferring atomic rename in dst.parent."""
    if src.is_symlink() or not src.is_file():
        raise FileNotFoundError(str(src))
    if dst.is_symlink():
        raise ValueError("云盘导出目标不能是符号链接")
    dst.parent.mkdir(parents=True, exist_ok=True)
    tmp = dst.parent / f".co-export-{uuid.uuid4().hex[:8]}.tmp"
    try:
        _stream_write_file(src, tmp)
    except OSError:
        try:
            tmp.unlink(missing_ok=True)
        except OSError:
            pass
        raise
    try:
        tmp.replace(dst)
    except OSError:
        try:
            tmp.unlink(missing_ok=True)
        except OSError:
            pass
        if dst.is_symlink():
            raise ValueError("云盘导出目标不能是符号链接") from None
        try:
            _stream_write_file(src, dst)
        except OSError:
            try:
                dst.unlink(missing_ok=True)
            except OSError:
                pass
            raise


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


_OAUTH_BEARER_RE = re.compile(r"(?i)\bBearer\s+[^\s'\"`,;}\]]+")
_OAUTH_ACCESS_TOKEN_RE = re.compile(r"\bya29\.[A-Za-z0-9._-]+")
_OAUTH_REFRESH_TOKEN_RE = re.compile(r"(?<![A-Za-z0-9])1//[A-Za-z0-9._-]+")
_OAUTH_CLIENT_SECRET_RE = re.compile(r"\bGOCSPX-[A-Za-z0-9._-]+")
_OAUTH_QUOTED_KV_RE = re.compile(
    r'(?i)(["\']?(?:client_secret|refresh_token|access_token)["\']?\s*[:=]\s*["\'])([^"\']+)(["\'])'
)
_OAUTH_PLAIN_KV_RE = re.compile(
    r"(?i)\b(client_secret|refresh_token|access_token)\s*=\s*([^\s&'\"`,;}\]]+)"
)


def _redact_oauth_secrets(text: str) -> str:
    result = str(text or "")
    if not result:
        return ""
    result = _OAUTH_BEARER_RE.sub("Bearer ***", result)
    result = _OAUTH_QUOTED_KV_RE.sub(r"\1***\3", result)
    result = _OAUTH_PLAIN_KV_RE.sub(r"\1=***", result)
    result = _OAUTH_ACCESS_TOKEN_RE.sub("***", result)
    result = _OAUTH_REFRESH_TOKEN_RE.sub("***", result)
    result = _OAUTH_CLIENT_SECRET_RE.sub("***", result)
    return result


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
    chosen = _redact_oauth_secrets(chosen)
    return "Google 云盘连接失败：" + chosen[:180]


_RCLONE_INSTALL_LOCK = threading.Lock()


def _write_private_bytes(path: Path, data: bytes) -> None:
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    try:
        with os.fdopen(fd, "wb") as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
    except BaseException:
        path.unlink(missing_ok=True)
        raise
    os.chmod(path, 0o600)


def stage_rclone_config(stage_path: Path, data_root: Path) -> Path | None:
    """Atomically install staged rclone config with 0600 permissions and remove stage."""
    if not stage_path.exists():
        return None
    target = data_root / ".rclone.conf"
    data_root.mkdir(parents=True, exist_ok=True)
    tmp = data_root / f".rclone.conf.{uuid.uuid4().hex[:8]}.tmp"
    user_tmp: Path | None = None
    try:
        raw = stage_path.read_bytes()
        _write_private_bytes(tmp, raw)
        tmp.replace(target)
        # Also mirror into ~/.config/rclone/rclone.conf inside the remote container
        # when running in Colab (/content), so standard rclone invocations work too.
        if str(stage_path).startswith("/content/"):
            user_rc_dir = Path.home() / ".config" / "rclone"
            user_rc_dir.mkdir(parents=True, mode=0o700, exist_ok=True)
            user_tmp = user_rc_dir / f".rclone.conf.{uuid.uuid4().hex[:8]}.tmp"
            user_dst = user_rc_dir / "rclone.conf"
            _write_private_bytes(user_tmp, raw)
            user_tmp.replace(user_dst)
    finally:
        tmp.unlink(missing_ok=True)
        if user_tmp is not None:
            user_tmp.unlink(missing_ok=True)
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
    with _RCLONE_INSTALL_LOCK:
        if binary.is_file():
            return binary
        archive_name = f"rclone-{tag}-linux-{arch}.zip"
        token = uuid.uuid4().hex[:8]
        part = bin_dir / f"{archive_name}.{token}.part"
        tmp_bin = bin_dir / f".rclone-{tag}.{token}.tmp"
        urls = (
            f"https://github.com/rclone/rclone/releases/download/{tag}/{archive_name}",
            f"https://downloads.rclone.org/{tag}/{archive_name}",
        )
        own_session = http is None
        session = http or _create_http_session()
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
        _stream_write_file(src, tmp)
        try:
            os.utime(tmp, ns=(src_stat.st_atime_ns, src_stat.st_mtime_ns))
        except OSError:
            pass
    except OSError:
        try:
            tmp.unlink(missing_ok=True)
        except OSError:
            pass
        raise
    try:
        if dst.is_symlink():
            tmp.unlink(missing_ok=True)
            return
        tmp.replace(dst)
    except OSError:
        try:
            tmp.unlink(missing_ok=True)
        except OSError:
            pass
        if dst.is_symlink():
            return
        try:
            _stream_write_file(src, dst)
            try:
                os.utime(dst, ns=(src_stat.st_atime_ns, src_stat.st_mtime_ns))
            except OSError:
                pass
        except OSError:
            try:
                dst.unlink(missing_ok=True)
            except OSError:
                pass
            raise


def _sync_drivefs_tree(
    src_dir: Path,
    dst_dir: Path,
    *,
    recursive: bool = False,
    max_depth: int = 6,
) -> None:
    if max_depth < 0 or not src_dir.is_dir() or src_dir.is_symlink():
        return
    if dst_dir.is_symlink():
        return
    dst_dir.mkdir(parents=True, exist_ok=True)
    for item in src_dir.iterdir():
        if _is_temp_name(item.name) or item.is_symlink():
            continue
        if item.is_dir():
            if recursive and max_depth > 0:
                _sync_drivefs_tree(
                    item,
                    dst_dir / item.name,
                    recursive=True,
                    max_depth=max_depth - 1,
                )
            continue
        _copy_file_if_newer(item, dst_dir / item.name)


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
        self._http_local = threading.local()
        self._http_sessions_lock = threading.Lock()
        self._http_sessions: list[requests.Session] = []
        self.lock = threading.RLock()
        self._mount_lock = threading.Lock()
        self.mode = "disabled"  # "disabled" | "colab_drivefs" | "mount" | "rclone_sync"
        self.error = ""
        self.mount_proc: subprocess.Popen | None = None
        self._space_cache = {"ts": 0.0, "total_bytes": None, "free_bytes": None}
        self._space_refreshing = False

    def _http_session(self):
        if self.http is not None and not isinstance(self.http, requests.Session):
            return self.http
        session = getattr(self._http_local, "session", None)
        if session is None:
            session = _create_http_session()
            self._http_local.session = session
            with self._http_sessions_lock:
                self._http_sessions.append(session)
        return session

    def close_http_sessions(self) -> None:
        with self._http_sessions_lock:
            sessions = list(self._http_sessions)
            self._http_sessions.clear()
        self._http_local = threading.local()
        for session in sessions:
            try:
                session.close()
            except Exception:
                pass

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

    def _drivefs_outputs_dir(self) -> Path:
        return (
            self._active_mydrive_dir()
            / COLABONE_DRIVE_FOLDER
            / COLABONE_DRIVE_OUTPUTS_SUBDIR
        )

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
        with self.lock:
            mode = self.mode
        if mode == "colab_drivefs":
            return self.is_colab_drivefs_mounted()
        if mode == "mount":
            return self.is_fuse_mounted() or self._has_rclone_config()
        return mode == "rclone_sync" and self._has_rclone_config()

    def _rclone_cmd(self, binary: Path, *args: str) -> list[str]:
        cmd = [str(binary), "--config", str(self.config_path)]
        quota_project = parse_rclone_quota_project(self.config_path)
        if quota_project:
            cmd.extend(["--header", f"X-Goog-User-Project: {quota_project}"])
        cmd.extend(args)
        return cmd

    def _terminate_mount_proc(self) -> None:
        with self.lock:
            proc = self.mount_proc
            self.mount_proc = None
        if proc is not None and proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
                try:
                    proc.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    pass

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
                    try:
                        subprocess.run(
                            cmd,
                            check=False,
                            timeout=10,
                            stdin=subprocess.DEVNULL,
                            stdout=subprocess.DEVNULL,
                            stderr=subprocess.DEVNULL,
                        )
                    except (subprocess.TimeoutExpired, OSError):
                        pass

    def connect(self) -> dict:
        staged_path = Path("/content/.colabone_rclone.conf")
        # 1) Prefer already-mounted official Google Colab DriveFS (zero rclone/GCP needed).
        if self.is_colab_drivefs_mounted():
            with self._mount_lock:
                if self.is_colab_drivefs_mounted():
                    staged_path.unlink(missing_ok=True)
                    self._terminate_mount_proc()
                    ws = self._drivefs_workspace_dir()
                    try:
                        ws.mkdir(parents=True, exist_ok=True)
                        os.listdir(ws)
                    except OSError as exc:
                        err = f"Google 云盘目录不可读写：{exc}"
                        with self.lock:
                            self.mode = "disabled"
                            self.error = err
                        raise RuntimeError(err) from None
                    with self.lock:
                        self.mode = "colab_drivefs"
                        self.error = ""
                    return self.status()

        # 2) Otherwise use headless rclone OAuth config.
        with self._mount_lock:
            stage_rclone_config(staged_path, self.root)
            has_cfg = self._has_rclone_config()
        if not has_cfg:
            with self.lock:
                self.mode = "disabled"
            raise RuntimeError("未检测到云盘凭据配置；请确认已登录 Google 账户")

        binary = ensure_rclone_binary(self.root, self.catalog, self._http_session())
        try:
            mkdir_res = subprocess.run(
                self._rclone_cmd(binary, "mkdir", COLABONE_DRIVE_SPEC),
                check=False,
                timeout=25,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.PIPE,
                text=True,
            )
        except (subprocess.TimeoutExpired, OSError):
            err = "Google 云盘连接超时，请稍后重试"
            with self.lock:
                self.mode = "disabled"
                self.error = err
            raise RuntimeError(err) from None
        if mkdir_res.returncode != 0:
            err = format_rclone_error(mkdir_res.stderr or "")
            with self.lock:
                self.mode = "disabled"
                self.error = err
            raise RuntimeError(err)

        with self._mount_lock:
            self._recover_stale_fuse()
            if self.is_fuse_mounted():
                with self.lock:
                    self.mode = "mount"
                    self.error = ""
            else:
                can_fuse = Path("/dev/fuse").exists() and bool(
                    shutil.which("fusermount3") or shutil.which("fusermount")
                )
                mounted = False
                if can_fuse:
                    self._terminate_mount_proc()
                    self.mount_root.mkdir(parents=True, exist_ok=True)
                    log_path = self.root / "rclone-mount.log"
                    log_fd = os.open(
                        log_path,
                        os.O_WRONLY | os.O_CREAT | os.O_TRUNC | getattr(os, "O_NOFOLLOW", 0),
                        0o600,
                    )
                    try:
                        if hasattr(os, "fchmod"):
                            os.fchmod(log_fd, 0o600)
                        log_stream = os.fdopen(log_fd, "wb")
                    except BaseException:
                        os.close(log_fd)
                        raise
                    with log_stream as log_out:
                        proc = subprocess.Popen(
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
                    with self.lock:
                        self.mount_proc = proc
                    for _ in range(16):
                        if self.is_fuse_mounted():
                            mounted = True
                            break
                        if proc.poll() is not None:
                            break
                        time.sleep(0.4)
                    if not mounted:
                        # Terminate lingering mount daemon before falling back to rclone_sync.
                        self._terminate_mount_proc()
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

                with self.lock:
                    self.mode = "mount" if mounted else "rclone_sync"
                    self.error = ""
        return self.status()

    def _pull_drivefs_workspace(self, target_dir: Path) -> bool:
        ws = self._drivefs_workspace_dir()
        try:
            ws.mkdir(parents=True, exist_ok=True)
            models_src = ws / "models.json"
            if models_src.is_file() and not models_src.is_symlink():
                _copy_file_if_newer(models_src, target_dir / "models.json")
            for folder, recursive in (
                ("workflows", False),
                ("comfyui-outputs", False),
                ("live-avatars", True),
                ("live-driving-motions", True),
            ):
                _sync_drivefs_tree(
                    ws / folder, target_dir / folder, recursive=recursive
                )
            with self.lock:
                self.error = ""
            return True
        except OSError:
            with self.lock:
                self.error = "从 Google 云盘恢复历史数据未完成，稍后自动重试"
            return False

    def _push_drivefs_workspace(self) -> bool:
        ws = self._drivefs_workspace_dir()
        try:
            ws.mkdir(parents=True, exist_ok=True)
            models_src = self.root / "models.json"
            if models_src.is_file() and not models_src.is_symlink():
                _copy_file_if_newer(models_src, ws / "models.json")
            for folder, recursive in (
                ("workflows", False),
                ("comfyui-outputs", False),
                ("live-avatars", True),
                ("live-driving-motions", True),
            ):
                _sync_drivefs_tree(
                    self.root / folder, ws / folder, recursive=recursive
                )
            with self.lock:
                self.error = ""
            return True
        except OSError:
            with self.lock:
                self.error = "Google 云盘同步失败，远端本地副本仍完整保留"
            return False

    _WORKSPACE_FILTER_ARGS = (
        "--filter",
        "- **/.trash/**",
        "--filter",
        "- **/.musetalk_cache/**",
        "--filter",
        "- **/.trt_cache/**",
        "--filter",
        "- **/.save-*",
        "--filter",
        "- **/*.part*",
        "--filter",
        "- **/*.tmp*",
        "--filter",
        "+ /models.json",
        "--filter",
        "+ /workflows/**",
        "--filter",
        "+ /comfyui-outputs/**",
        "--filter",
        "+ /live-avatars/**",
        "--filter",
        "+ /live-driving-motions/**",
        "--filter",
        "- **",
    )

    def pull_workspace(self, dest_dir: Path | None = None) -> bool:
        """Pull persisted models.json, workflows, ComfyUI outputs, live-avatars, and live-driving-motions into dest_dir."""
        if not self.available():
            return False
        with self.lock:
            mode = self.mode
        target_dir = Path(dest_dir) if dest_dir is not None else self.root
        target_dir.mkdir(parents=True, exist_ok=True)
        if mode == "colab_drivefs":
            return self._pull_drivefs_workspace(target_dir)
        try:
            binary = ensure_rclone_binary(self.root, self.catalog, self._http_session())
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
                    *self._WORKSPACE_FILTER_ARGS,
                ),
                check=False,
                timeout=120,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.PIPE,
                text=True,
            )
        except (subprocess.TimeoutExpired, OSError, RuntimeError):
            with self.lock:
                self.error = "从 Google 云盘恢复历史数据未完成，稍后自动重试"
            return False
        if res.returncode != 0:
            with self.lock:
                self.error = "从 Google 云盘恢复历史数据未完成，稍后自动重试"
            return False
        with self.lock:
            self.error = ""
        return True

    def push_workspace(self) -> bool:
        """Push local models.json, workflows, ComfyUI outputs, live-avatars, and live-driving-motions directly to ColabOne_Drive."""
        if not self.available():
            return False
        with self.lock:
            mode = self.mode
        if mode == "colab_drivefs":
            return self._push_drivefs_workspace()
        try:
            binary = ensure_rclone_binary(self.root, self.catalog, self._http_session())
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
                    *self._WORKSPACE_FILTER_ARGS,
                ),
                check=False,
                timeout=180,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.PIPE,
                text=True,
            )
        except (subprocess.TimeoutExpired, OSError, RuntimeError):
            with self.lock:
                self.error = "Google 云盘同步失败，远端本地副本仍完整保留"
            return False
        if res.returncode != 0:
            with self.lock:
                self.error = "Google 云盘同步失败，远端本地副本仍完整保留"
            return False
        with self.lock:
            self.error = ""
        return True

    @staticmethod
    def _rclone_trash_succeeded(res: subprocess.CompletedProcess) -> bool:
        if res.returncode == 0 or res.returncode in (3, 4):
            return True
        err_low = (res.stderr or "").lower()
        return "not found" in err_low or "doesn't exist" in err_low

    def remove_remote_workflow(self, workflow_id: str) -> bool:
        if not self.available():
            return False
        with self.lock:
            mode = self.mode
        try:
            if mode == "colab_drivefs":
                ws = self._drivefs_workspace_dir() / "workflows"
                src = ws / f"{workflow_id}.json"
                if src.is_file() and not src.is_symlink():
                    trash = ws / ".trash"
                    trash.mkdir(parents=True, exist_ok=True)
                    src.replace(trash / src.name)
                return True
            binary = ensure_rclone_binary(self.root, self.catalog, self._http_session())
            res = subprocess.run(
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
                stderr=subprocess.PIPE,
                text=True,
            )
            return self._rclone_trash_succeeded(res)
        except Exception:
            return False

    def remove_remote_comfy_output(self, identity: str, extension: str) -> bool:
        if not self.available():
            return False
        with self.lock:
            mode = self.mode
        try:
            if mode == "colab_drivefs":
                ws = self._drivefs_workspace_dir() / "comfyui-outputs"
                trash = ws / ".trash"
                for ext in dict.fromkeys((extension, "json")):
                    src = ws / f"{identity}.{ext}"
                    if src.is_file() and not src.is_symlink():
                        trash.mkdir(parents=True, exist_ok=True)
                        src.replace(trash / src.name)
                return True
            binary = ensure_rclone_binary(self.root, self.catalog, self._http_session())
            all_ok = True
            for ext in dict.fromkeys((extension, "json")):
                res = subprocess.run(
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
                    stderr=subprocess.PIPE,
                    text=True,
                )
                if not self._rclone_trash_succeeded(res):
                    all_ok = False
            return all_ok
        except Exception:
            return False

    def _trash_live_subdir(self, subdir: str, item_id: str, label: str) -> bool:
        if not isinstance(item_id, str) or not _LIVE_AVATAR_ID_RE.fullmatch(item_id):
            raise ValueError(f"{label}编号不合法")
        if not self.available():
            return False
        with self.lock:
            mode = self.mode
        try:
            if mode == "colab_drivefs":
                ws = self._drivefs_workspace_dir() / subdir
                if ws.is_symlink():
                    raise ValueError(f"云盘{label}目录不能是符号链接")
                if not ws.is_dir():
                    return True
                src = ws / item_id
                if src.is_symlink():
                    raise ValueError(f"云盘{label}路径不能是符号链接")
                if src.exists():
                    ws_resolved = ws.resolve()
                    if src.resolve().parent != ws_resolved:
                        raise ValueError(f"{label}路径越界")
                    trash = ws / ".trash"
                    if trash.is_symlink():
                        raise ValueError("云盘回收目录不能是符号链接")
                    trash.mkdir(parents=True, exist_ok=True)
                    dst = trash / item_id
                    if dst.is_symlink():
                        raise ValueError("云盘回收目标不能是符号链接")
                    if dst.exists():
                        if dst.is_dir():
                            shutil.rmtree(dst)
                        else:
                            dst.unlink()
                    src.replace(dst)
                return True
            binary = ensure_rclone_binary(self.root, self.catalog, self._http_session())
            res = subprocess.run(
                self._rclone_cmd(
                    binary,
                    "move",
                    "--delete-empty-src-dirs",
                    f"{COLABONE_DRIVE_SPEC}/workspace/{subdir}/{item_id}",
                    f"{COLABONE_DRIVE_SPEC}/workspace/{subdir}/.trash/{item_id}",
                ),
                check=False,
                timeout=30,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.PIPE,
                text=True,
            )
            return self._rclone_trash_succeeded(res)
        except ValueError:
            raise
        except Exception:
            return False

    def trash_live_avatar(self, avatar_id: str) -> bool:
        """Safely move a deleted live-avatar folder into live-avatars/.trash/<avatar_id>."""
        return self._trash_live_subdir("live-avatars", avatar_id, "数字人形象")

    def trash_live_driving_motion(self, motion_id: str) -> bool:
        """Safely move a deleted live-driving-motion folder into live-driving-motions/.trash/<motion_id>."""
        return self._trash_live_subdir("live-driving-motions", motion_id, "驱动动作")

    def _inspect_rclone_remote_file(
        self, binary: Path, remote_path: str
    ) -> tuple[str, dict | str | None]:
        """Inspect remote_path via rclone lsjson --stat --hash.

        Returns ("exists", entry_dict), ("missing", None), or ("error", message).
        """
        try:
            res = subprocess.run(
                self._rclone_cmd(binary, "lsjson", "--stat", "--hash", remote_path),
                check=False,
                timeout=20,
                stdin=subprocess.DEVNULL,
                capture_output=True,
                text=True,
            )
        except subprocess.TimeoutExpired:
            return ("error", "导出到 Google 云盘超时，请稍后重试")
        except OSError as exc:
            return ("error", f"导出到 Google 云盘失败：{exc}")
        if res.returncode != 0:
            err_low = (res.stderr or "").lower()
            if res.returncode in (3, 4) or "not found" in err_low:
                return ("missing", None)
            return ("error", format_rclone_error(res.stderr or ""))
        if not res.stdout or not res.stdout.strip():
            return ("missing", None)
        try:
            parsed = json.loads(res.stdout)
        except ValueError:
            return ("missing", None)
        if isinstance(parsed, dict):
            return ("exists", parsed)
        if isinstance(parsed, list):
            for entry in parsed:
                if isinstance(entry, dict):
                    return ("exists", entry)
        return ("missing", None)

    def export_output_file(
        self,
        src_path: Path,
        *,
        filename: str,
        output_id: str,
        extension: str,
        expected_sha256: str = "",
        expected_size: int | None = None,
    ) -> dict:
        """Export a ComfyUI output directly from the remote VM to Google Drive outputs/."""
        if not self.available():
            raise ValueError("当前实例尚未连接 Google 云盘，请先完成云盘授权与挂载")
        with self.lock:
            mode = self.mode
        src = Path(src_path)
        if src.is_symlink() or not src.is_file():
            raise FileNotFoundError(str(src))
        actual_size = src.stat().st_size
        if actual_size <= 0:
            raise ValueError("生成结果为空")
        clean_name = sanitize_drive_export_filename(filename, output_id, extension)
        ext = clean_name.rsplit(".", 1)[1]
        stem = clean_name[: -(len(ext) + 1)]
        short_id = re.sub(r"[^a-zA-Z0-9]", "", str(output_id or ""))[:8] or "copy"
        sha256_expected = (expected_sha256 or "").strip().lower()
        if expected_size is not None and actual_size != expected_size:
            sha256_expected = ""
        remote_folder = f"{COLABONE_DRIVE_FOLDER}/{COLABONE_DRIVE_OUTPUTS_SUBDIR}"

        def _ensure_src_sha256() -> str:
            nonlocal sha256_expected
            if not sha256_expected:
                sha256_expected = _hash_file(src, "sha256")
            return sha256_expected

        def _build_candidates() -> list[str]:
            base_stem = stem[:168].rstrip(". ") or "colabone-output"
            items = [clean_name]
            disambig = f"{base_stem}-{short_id}.{ext}"
            if disambig.lower() != clean_name.lower():
                items.append(disambig)
            return items

        if mode == "colab_drivefs":
            if not self.is_colab_drivefs_mounted():
                err = "Google 云盘连接已断开，请重新挂载后再试"
                with self.lock:
                    self.mode = "disabled"
                    self.error = err
                raise RuntimeError(err)
            folder_dir = self._active_mydrive_dir() / COLABONE_DRIVE_FOLDER
            out_dir = self._drivefs_outputs_dir()
            if folder_dir.is_symlink() or out_dir.is_symlink():
                raise ValueError("云盘导出目录不能是符号链接")
            try:
                out_dir.mkdir(parents=True, exist_ok=True)
            except OSError as exc:
                raise RuntimeError(f"Google 云盘导出目录不可读写：{exc}") from None

            target_dst: Path | None = None
            for cand in _build_candidates():
                dst = out_dir / cand
                if dst.is_symlink():
                    continue
                if dst.exists():
                    if dst.is_file():
                        try:
                            if dst.stat().st_size == actual_size and _hash_file(
                                dst, "sha256"
                            ) == _ensure_src_sha256():
                                with self.lock:
                                    self.error = ""
                                return {
                                    "ok": True,
                                    "already_exists": True,
                                    "filename": dst.name,
                                    "remote_folder": remote_folder,
                                    "drive_path": f"My Drive/{remote_folder}/{dst.name}",
                                    "size": actual_size,
                                    "mode": mode,
                                }
                        except OSError:
                            pass
                    continue
                target_dst = dst
                break

            if target_dst is None:
                base_stem = stem[:160].rstrip(". ") or "colabone-output"
                target_dst = (
                    out_dir / f"{base_stem}-{short_id}-{uuid.uuid4().hex[:4]}.{ext}"
                )

            try:
                _copy_file_stream(src, target_dst)
            except OSError as exc:
                raise RuntimeError(f"写入 Google 云盘失败：{exc}") from None

            with self.lock:
                self.error = ""
                self._space_cache = {**self._space_cache, "ts": 0.0}
            return {
                "ok": True,
                "already_exists": False,
                "filename": target_dst.name,
                "remote_folder": remote_folder,
                "drive_path": f"My Drive/{remote_folder}/{target_dst.name}",
                "size": actual_size,
                "mode": mode,
            }

        if not self._has_rclone_config():
            err = "未检测到云盘凭据配置，请重新挂载 Google 云盘"
            with self.lock:
                self.mode = "disabled"
                self.error = err
            raise RuntimeError(err)

        binary = ensure_rclone_binary(self.root, self.catalog, self._http_session())
        remote_dir = f"{COLABONE_DRIVE_SPEC}/{COLABONE_DRIVE_OUTPUTS_SUBDIR}"
        src_md5_cache: str | None = None

        def _entry_matches_src(entry: dict) -> bool:
            nonlocal src_md5_cache
            if entry.get("IsDir"):
                return False
            if entry.get("Size") != actual_size:
                return False
            raw_hashes = entry.get("Hashes")
            if not isinstance(raw_hashes, dict):
                return False
            hashes = {
                str(k).lower().replace("-", ""): str(v).strip().lower()
                for k, v in raw_hashes.items()
                if isinstance(v, str) and v.strip()
            }
            if "sha256" in hashes:
                return hashes["sha256"] == _ensure_src_sha256()
            if "md5" in hashes:
                if src_md5_cache is None:
                    src_md5_cache = _hash_file(src, "md5")
                return hashes["md5"] == src_md5_cache
            return False

        final_name: str | None = None
        for cand in _build_candidates():
            inspect_status, inspect_payload = self._inspect_rclone_remote_file(
                binary, f"{remote_dir}/{cand}"
            )
            if inspect_status == "error":
                raise RuntimeError(
                    str(inspect_payload or format_rclone_error(""))
                )
            if inspect_status == "missing" or not isinstance(inspect_payload, dict):
                final_name = cand
                break
            if _entry_matches_src(inspect_payload):
                with self.lock:
                    self.error = ""
                raw_id = inspect_payload.get("ID")
                cand_id = str(raw_id).strip() if isinstance(raw_id, str) else ""
                drive_file_id = (
                    cand_id if _DRIVE_FILE_ID_RE.fullmatch(cand_id) else ""
                )
                payload_out = {
                    "ok": True,
                    "already_exists": True,
                    "filename": cand,
                    "remote_folder": remote_folder,
                    "drive_path": f"My Drive/{remote_folder}/{cand}",
                    "size": actual_size,
                    "mode": mode,
                }
                if drive_file_id:
                    payload_out["drive_file_id"] = drive_file_id
                    payload_out["drive_web_view_link"] = (
                        f"https://drive.google.com/file/d/{_url_quote(drive_file_id, safe='')}/view"
                    )
                return payload_out

        if final_name is None:
            base_stem = stem[:160].rstrip(". ") or "colabone-output"
            final_name = f"{base_stem}-{short_id}-{uuid.uuid4().hex[:4]}.{ext}"

        try:
            res = subprocess.run(
                self._rclone_cmd(
                    binary,
                    "copyto",
                    str(src),
                    f"{remote_dir}/{final_name}",
                    "--drive-chunk-size",
                    "64M",
                ),
                check=False,
                timeout=210,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.PIPE,
                text=True,
            )
        except subprocess.TimeoutExpired:
            raise RuntimeError("导出到 Google 云盘超时，请稍后重试") from None
        except OSError as exc:
            raise RuntimeError(f"导出到 Google 云盘失败：{exc}") from None

        if res.returncode != 0:
            raise RuntimeError(format_rclone_error(res.stderr or ""))

        drive_file_id = ""
        try:
            post_status, post_payload = self._inspect_rclone_remote_file(
                binary, f"{remote_dir}/{final_name}"
            )
            if post_status == "exists" and isinstance(post_payload, dict):
                raw_id = post_payload.get("ID")
                if isinstance(raw_id, str):
                    cand_id = raw_id.strip()
                    if _DRIVE_FILE_ID_RE.fullmatch(cand_id):
                        drive_file_id = cand_id
        except Exception:
            drive_file_id = ""

        with self.lock:
            self.error = ""
            self._space_cache = {**self._space_cache, "ts": 0.0}
        payload_out = {
            "ok": True,
            "already_exists": False,
            "filename": final_name,
            "remote_folder": remote_folder,
            "drive_path": f"My Drive/{remote_folder}/{final_name}",
            "size": actual_size,
            "mode": mode,
        }
        if drive_file_id:
            payload_out["drive_file_id"] = drive_file_id
            payload_out["drive_web_view_link"] = (
                f"https://drive.google.com/file/d/{_url_quote(drive_file_id, safe='')}/view"
            )
        return payload_out

    def unmount(self):
        with self._mount_lock:
            with self.lock:
                mode = self.mode
            if mode == "colab_drivefs":
                try:
                    os.sync()
                except OSError:
                    pass
                try:
                    from google.colab import drive as colab_drive

                    colab_drive.flush_and_unmount(timeout_ms=30000)
                except Exception:
                    pass
                with self.lock:
                    self.mode = "disabled"
                return
            self._terminate_mount_proc()
            if self.is_fuse_mounted():
                for cmd in (
                    ["fusermount3", "-uz", str(self.mount_root)],
                    ["fusermount", "-uz", str(self.mount_root)],
                    ["umount", "-l", str(self.mount_root)],
                ):
                    if shutil.which(cmd[0]):
                        try:
                            subprocess.run(
                                cmd,
                                check=False,
                                timeout=10,
                                stdin=subprocess.DEVNULL,
                                stdout=subprocess.DEVNULL,
                                stderr=subprocess.DEVNULL,
                            )
                        except (subprocess.TimeoutExpired, OSError):
                            pass
            with self.lock:
                self.mode = "disabled"

    def _existing_rclone_binary(self) -> Path | None:
        existing = shutil.which("rclone")
        if existing:
            return Path(existing)
        try:
            manifest = json.loads((self.catalog / "rclone.json").read_text(encoding="utf-8"))
            tag = manifest.get("tag")
            if tag:
                candidate = self.root / "bin" / f"rclone-{tag}"
                if candidate.is_file():
                    return candidate
        except Exception:
            pass
        return None

    def _refresh_space(self):
        now = time.monotonic()
        with self.lock:
            cache_ts = float(self._space_cache.get("ts") or 0.0)
            if cache_ts > 0.0 and (now - cache_ts) < 30.0:
                return
            if self._space_refreshing:
                return
            self._space_refreshing = True
            mode = self.mode
        try:
            if (
                (mode == "colab_drivefs" and self.is_colab_drivefs_mounted())
                or (mode == "mount" and self.is_fuse_mounted())
            ):
                try:
                    usage = shutil.disk_usage(self._active_mydrive_dir())
                    done_ts = time.monotonic()
                    with self.lock:
                        self._space_cache = {
                            "ts": done_ts,
                            "total_bytes": usage.total,
                            "free_bytes": usage.free,
                        }
                    return
                except OSError:
                    done_ts = time.monotonic()
                    with self.lock:
                        self._space_cache = {
                            **self._space_cache,
                            "ts": done_ts,
                        }
                    return
            if self.available() and mode != "colab_drivefs":
                try:
                    binary = self._existing_rclone_binary()
                    if binary is None:
                        done_ts = time.monotonic()
                        with self.lock:
                            self._space_cache = {**self._space_cache, "ts": done_ts}
                        return
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
                        if isinstance(info, dict):
                            done_ts = time.monotonic()
                            with self.lock:
                                self._space_cache = {
                                    "ts": done_ts,
                                    "total_bytes": info.get("total"),
                                    "free_bytes": info.get("free"),
                                }
                            return
                except Exception:
                    pass
                done_ts = time.monotonic()
                with self.lock:
                    self._space_cache = {**self._space_cache, "ts": done_ts}
        finally:
            with self.lock:
                self._space_refreshing = False

    def status(self, pending: int = 0) -> dict:
        connected = self.available()
        if connected:
            self._refresh_space()
        with self.lock:
            mode = self.mode
            error = self.error
            total_bytes = self._space_cache["total_bytes"]
            free_bytes = self._space_cache["free_bytes"]
        if connected and mode == "disabled" and not self.available():
            connected = False
        mount_path = ""
        if connected and mode == "colab_drivefs" and self.is_colab_drivefs_mounted():
            mount_path = str(self._active_mydrive_dir())
        elif connected and mode == "mount" and self.is_fuse_mounted():
            mount_path = str(self.mount_root)
        return {
            "connected": connected,
            "configured": self.configured(),
            "mode": mode if connected else "disabled",
            "mount_path": mount_path,
            "remote_folder": COLABONE_DRIVE_FOLDER,
            "pending": pending,
            "error": error,
            "total_bytes": total_bytes if connected else None,
            "free_bytes": free_bytes if connected else None,
        }
