#!/usr/bin/env python3
"""
Installer for ColabOne.

Builds the console executable, prepares the bundled Python runtime and its packages
(downloaded and pinned, nothing is copied from another application), lays out the install
directory, manages shortcuts, and closes/restarts the console GUI.

Run it with a Python 3.10+ that has pip (python.org); that Python only runs this
script and pip. The installed app runs on its own runtime\\python.exe.

Usage:
  python tools/install.py                 # Full install / sync
  python tools/install.py --check         # Prepare runtime + dev-venv, run AST/go vet/go test/unit tests, stop
  python tools/install.py --dry-run       # Show actions without modifying files
  python tools/install.py --no-restart    # Do not close or launch the GUI
  python tools/install.py --root <dir>    # Redirect all locations under a sandbox root
  python tools/install.py --embed-zip <f> # Use a local copy of the pinned embeddable zip
"""

from __future__ import annotations

import hashlib
import inspect
import os
import platform
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.request
import zipfile
from pathlib import Path

# Bundled CPython 3.12.10 runtimes (pinned by URL and SHA-256):
# - Windows x64: python.org embeddable zip (MD5 fe8ef205f2e9c3ba44d0cf9954e1abd3)
# - macOS arm64: python-build-standalone 20250529 install_only_stripped
PYTHON_VERSION = "3.12.10"
PYTHON_TAG = "312"
PYTHON_EMBED_URL = f"https://www.python.org/ftp/python/{PYTHON_VERSION}/python-{PYTHON_VERSION}-embed-amd64.zip"
PYTHON_EMBED_SHA256 = "4acbed6dd1c744b0376e3b1cf57ce906f9dc9e95e68824584c8099a63025a3c3"
PYTHON_MACOS_ARM64_URL = (
    "https://github.com/astral-sh/python-build-standalone/releases/download/20250529/"
    f"cpython-{PYTHON_VERSION}%2B20250529-aarch64-apple-darwin-install_only_stripped.tar.gz"
)
PYTHON_MACOS_ARM64_SHA256 = (
    "2579c9ffcbb0a745f1de3d645612c7466269eb9a23316a54ece49d50c1c9122f"
)


def run_ps1(
    script_content: str, args: list[str] | None = None, env: dict | None = None
) -> subprocess.CompletedProcess:
    """Run a PowerShell script via pwsh -NoProfile -File (never powershell.exe)."""
    if shutil.which("pwsh") is None:
        raise SystemExit(
            "PowerShell 7 (pwsh) is required: winget install Microsoft.PowerShell"
        )
    with tempfile.NamedTemporaryFile(
        "w", suffix=".ps1", delete=False, encoding="utf-8"
    ) as tf:
        tf.write(script_content)
        tmp_name = tf.name
    try:
        cmd = ["pwsh", "-NoProfile", "-File", tmp_name] + (args or [])
        return subprocess.run(
            cmd, capture_output=True, text=True, env=env, stdin=subprocess.DEVNULL
        )
    finally:
        try:
            os.remove(tmp_name)
        except Exception:
            pass


def desktop_folder() -> Path:
    """The real Desktop, which OneDrive or a policy may have moved off %USERPROFILE%."""
    if shutil.which("pwsh") is None:
        raise SystemExit(
            "PowerShell 7 (pwsh) is required: winget install Microsoft.PowerShell"
        )
    r = subprocess.run(
        ["pwsh", "-NoProfile", "-Command", "[Environment]::GetFolderPath('Desktop')"],
        capture_output=True,
        text=True,
        stdin=subprocess.DEVNULL,
        timeout=30,
    )
    found = r.stdout.strip()
    return Path(found) if r.returncode == 0 and found else Path.home() / "Desktop"


def bundled_python(install_dir: Path) -> Path:
    return (
        install_dir
        / "runtime"
        / ("python.exe" if sys.platform == "win32" else "bin/python3")
    )


def app_binary_exists(path: Path) -> bool:
    return path.is_dir() if path.suffix == ".app" else path.is_file()


class ColabOneInstallPaths:
    def __init__(self, root: str | None = None, cache_dir: str | Path | None = None):
        self.repo = Path(__file__).resolve().parent.parent
        self.is_sandbox = root is not None
        if sys.platform == "darwin":
            if root:
                sandbox = Path(root).resolve()
                self.local_appdata = sandbox / "Library" / "Application Support"
                self.target_install = sandbox / "Programs" / "ColabOne"
                self.target_config = self.local_appdata / "ColabOne"
                self.start_menu = sandbox / "Applications"
                self.desktop = sandbox / "Desktop"
            else:
                base = os.environ.get("LOCALAPPDATA") or str(
                    Path.home() / "Library" / "Application Support"
                )
                self.local_appdata = Path(base)
                self.target_install = self.local_appdata / "Programs" / "ColabOne"
                self.target_config = self.local_appdata / "ColabOne"
                self.start_menu = Path.home() / "Applications"
                self.desktop = Path.home() / "Desktop"
            self.target_exe = self.target_install / "ColabOne.app"
        else:
            if root:
                sandbox = Path(root).resolve()
                self.local_appdata = sandbox / "AppData" / "Local"
                self.target_install = sandbox / "Programs" / "ColabOne"
                self.target_config = self.local_appdata / "ColabOne"
                self.start_menu = sandbox / "StartMenu"
                self.desktop = sandbox / "Desktop"
            else:
                base = os.environ.get("LOCALAPPDATA") or str(
                    Path.home() / "AppData" / "Local"
                )
                self.local_appdata = Path(base)
                self.target_install = self.local_appdata / "Programs" / "ColabOne"
                self.target_config = self.local_appdata / "ColabOne"
                self.start_menu = (
                    Path(os.environ.get("APPDATA", ""))
                    / "Microsoft"
                    / "Windows"
                    / "Start Menu"
                    / "Programs"
                )
                self.desktop = desktop_folder()
            self.target_exe = self.target_install / "ColabOne.exe"

        self.console_src = self.repo / "console"
        # Downloaded archives are kept here, so a second install does not download again.
        self.cache_dir = Path(cache_dir) if cache_dir else self.target_config / "cache"


def find_go() -> str | None:
    found = shutil.which("go")
    if found:
        return found
    candidates = [
        Path(os.environ.get("LOCALAPPDATA", "")) / "Programs" / "Go" / "bin" / "go.exe"
    ]
    if sys.platform == "darwin":
        candidates = [Path("/opt/homebrew/bin/go"), Path("/usr/local/bin/go")]
    for local in candidates:
        if local.is_file():
            go_dir = str(local.parent)
            if go_dir not in os.environ.get("PATH", ""):
                os.environ["PATH"] = go_dir + os.pathsep + os.environ.get("PATH", "")
            return str(local)
    return None


def go_env() -> dict[str, str]:
    env = dict(os.environ)
    env.setdefault("GOTOOLCHAIN", "auto")
    return env


def host_pythons(paths: ColabOneInstallPaths | None = None) -> list[list[str]]:
    """Command lines of Pythons on this machine that may have pip and venv, best first."""
    out: list[list[str]] = []
    if paths is not None:
        bundled = bundled_python(paths.target_install)
        if bundled.is_file():
            out.append([str(bundled)])
    if sys.version_info >= (3, 10):
        out.append([sys.executable])
    if sys.platform == "win32":
        if shutil.which("py"):
            out.append(["py", "-3"])
        if shutil.which("python"):
            out.append(["python"])
    else:
        for cand in (
            "/opt/homebrew/bin/python3",
            "/usr/local/bin/python3",
            shutil.which("python3") or "",
        ):
            if cand and Path(cand).is_file() and [cand] not in out:
                out.append([cand])
        if shutil.which("uv"):
            try:
                r = subprocess.run(
                    ["uv", "python", "find", ">=3.10"],
                    capture_output=True,
                    text=True,
                    stdin=subprocess.DEVNULL,
                    timeout=15,
                )
                found = r.stdout.strip()
                if (
                    r.returncode == 0
                    and found
                    and Path(found).is_file()
                    and [found] not in out
                ):
                    out.append([found])
            except (OSError, subprocess.TimeoutExpired):
                pass
    if [sys.executable] not in out:
        out.append([sys.executable])
    return out


def python_can(cmd: list[str], code: str) -> bool:
    try:
        r = subprocess.run(
            cmd + ["-c", code],
            capture_output=True,
            stdin=subprocess.DEVNULL,
            timeout=60,
        )
    except (OSError, subprocess.TimeoutExpired):
        return False
    return r.returncode == 0


def find_pip_python(paths: ColabOneInstallPaths | None = None) -> list[str] | None:
    candidates = host_pythons(paths)
    for cmd in candidates:
        if python_can(cmd, "import sys, pip; assert sys.version_info >= (3, 10)"):
            return cmd
    for cmd in candidates:
        if python_can(cmd, "import pip"):
            return cmd
    return None


def server_test_python(paths: ColabOneInstallPaths) -> str | None:
    """A Python with fastapi for ColabOne server tests. The Windows embeddable runtime has
    neither fastapi nor pip, so a dev venv is made once under the config dir from a Python
    with venv + ensurepip (COLABONE_DEV_PYTHON, else bundled/host Python 3.10+)."""
    venv = paths.target_config / "dev-venv"
    vpy = venv / ("Scripts/python.exe" if sys.platform == "win32" else "bin/python")
    reqs = paths.repo / "server" / "requirements-dev.txt"
    quiet = dict(stdin=subprocess.DEVNULL, timeout=600)
    if not vpy.is_file():
        base = os.environ.get("COLABONE_DEV_PYTHON")
        candidates = [[base]] if base else host_pythons(paths)
        cmd = next(
            (
                c
                for c in candidates
                if python_can(
                    c, "import sys, venv, ensurepip; assert sys.version_info >= (3, 10)"
                )
            ),
            None,
        )
        if not cmd:
            return None
        print(f"Creating the server-test venv at {venv} ...")
        if subprocess.run(cmd + ["-m", "venv", str(venv)], **quiet).returncode != 0:
            return None
    probe = subprocess.run(
        [str(vpy), "-c", "import fastapi, httpx"], capture_output=True, **quiet
    )
    if probe.returncode != 0:
        r = subprocess.run(
            [str(vpy), "-m", "pip", "install", "-q", "-r", str(reqs)], **quiet
        )
        if r.returncode != 0:
            return None
    return str(vpy)


def find_python(paths: ColabOneInstallPaths) -> str:
    """The bundled runtime when it exists (it is made first), else the running Python."""
    bundled = bundled_python(paths.target_install)
    return str(bundled) if bundled.is_file() else sys.executable


# ─── Runtime and packages (self-contained: downloaded, hash-pinned) ──────────────


def sha256_of(path: Path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def download_verified(url: str, sha256: str, dest: Path) -> None:
    """Download url to dest (via a .part file), keeping it only if its SHA-256 matches."""
    dest.parent.mkdir(parents=True, exist_ok=True)
    part = dest.with_name(dest.name + ".part")
    last_err: Exception | None = None
    for attempt in range(1, 4):
        try:
            print(f"runtime: downloading {url} (attempt {attempt}) ...")
            req = urllib.request.Request(
                url, headers={"User-Agent": "ColabOne-installer"}
            )
            with (
                urllib.request.urlopen(req, timeout=60) as resp,
                open(part, "wb") as out,
            ):
                shutil.copyfileobj(resp, out, 1 << 20)
            got = sha256_of(part)
            if got != sha256:
                raise RuntimeError(
                    f"SHA-256 mismatch for {url}: expected {sha256}, got {got}"
                )
            os.replace(part, dest)
            return
        except (
            Exception
        ) as e:  # network errors and hash mismatch both retry, then fail loudly
            last_err = e
            print(f"runtime: download failed: {e}")
            time.sleep(2 * attempt)
    try:
        part.unlink()
    except OSError:
        pass
    raise RuntimeError(
        f"could not download a verified {dest.name}: {last_err}\n"
        f"Download it yourself from {url} and pass it with --embed-zip."
    )


def safe_extract(zip_path: Path, dest: Path) -> None:
    root = dest.resolve()
    with zipfile.ZipFile(zip_path) as z:
        for name in z.namelist():
            target = (dest / name).resolve()
            if root != target and root not in target.parents:
                raise RuntimeError(f"unsafe path in {zip_path.name}: {name}")
        z.extractall(dest)


def safe_extract_tar(tar_path: Path, dest: Path) -> None:
    root = str(dest.resolve())
    prefix = root + os.sep
    with tarfile.open(tar_path, "r:gz") as t:
        for m in t.getmembers():
            if m.isdev():
                raise RuntimeError(f"unsafe device entry in {tar_path.name}: {m.name}")
            norm = os.path.normpath(os.path.join(root, m.name))
            if norm != root and not norm.startswith(prefix):
                raise RuntimeError(f"unsafe path in {tar_path.name}: {m.name}")
            if m.issym() or m.islnk():
                link_base = root if m.islnk() else os.path.dirname(norm)
                link_target = os.path.normpath(os.path.join(link_base, m.linkname))
                if link_target != root and not link_target.startswith(prefix):
                    raise RuntimeError(
                        f"unsafe link in {tar_path.name}: {m.name} -> {m.linkname}"
                    )
        if "filter" in inspect.signature(t.extractall).parameters:
            t.extractall(dest, filter="tar")
        else:
            t.extractall(dest)


def _runtime_spec() -> tuple[str, str, str]:
    """(url, sha256, local_filename) for the current platform's standalone CPython."""
    if sys.platform == "win32":
        return PYTHON_EMBED_URL, PYTHON_EMBED_SHA256, PYTHON_EMBED_URL.rsplit("/", 1)[1]
    if sys.platform == "darwin":
        if platform.machine() != "arm64":
            raise SystemExit(
                f"Unsupported macOS architecture '{platform.machine()}': ColabOne pins arm64 (Apple Silicon) wheels."
            )
        fname = urllib.request.unquote(PYTHON_MACOS_ARM64_URL.rsplit("/", 1)[1])
        return PYTHON_MACOS_ARM64_URL, PYTHON_MACOS_ARM64_SHA256, fname
    raise SystemExit(
        f"Unsupported platform '{sys.platform}': ColabOne supports Windows x64 and macOS arm64."
    )


def ensure_runtime(
    paths: ColabOneInstallPaths, dry_run: bool, embed_zip: str | None = None
) -> bool:
    """Make <install>/runtime from the pinned standalone CPython unless it is there already.
    Returns True when it had to be created."""
    runtime = paths.target_install / "runtime"
    if bundled_python(paths.target_install).is_file():
        return False
    url, expected_sha, archive_name = _runtime_spec()
    if dry_run:
        print(
            f"[dry-run] runtime: would download CPython {PYTHON_VERSION} standalone into {runtime}"
        )
        return True
    if embed_zip:
        archive = Path(embed_zip)
        got = sha256_of(archive)
        if got != expected_sha:
            raise RuntimeError(
                f"{archive} is not the pinned {archive_name}: SHA-256 {got}"
            )
    else:
        archive = paths.cache_dir / archive_name
        if not (archive.is_file() and sha256_of(archive) == expected_sha):
            download_verified(url, expected_sha, archive)
    paths.target_install.mkdir(parents=True, exist_ok=True)
    staging = paths.target_install / "runtime.new"
    if staging.exists():
        _force_rmtree(staging)
    staging.mkdir()
    if sys.platform == "win32":
        safe_extract(archive, staging)
        # The embeddable zip ships its ._pth with "import site" commented out. Keep the install
        # dir ("..") and site-packages on the path; site stays on so .pth files are processed.
        (staging / f"python{PYTHON_TAG}._pth").write_text(
            f"python{PYTHON_TAG}.zip\n.\n..\n..\\site-packages\nimport site\n",
            encoding="ascii",
        )
    else:
        safe_extract_tar(archive, staging)
        inner = staging / "python"
        if inner.is_dir():
            for child in list(inner.iterdir()):
                shutil.move(str(child), str(staging / child.name))
            inner.rmdir()
        # Relative .pth inside runtime/lib/python3.12/site-packages puts <install> and
        # <install>/site-packages on sys.path regardless of where the folder is moved.
        rt_sp = (
            staging
            / "lib"
            / f"python{PYTHON_TAG[0]}.{PYTHON_TAG[1:]}"
            / "site-packages"
        )
        rt_sp.mkdir(parents=True, exist_ok=True)
        (rt_sp / "colabone.pth").write_text(
            "../../../..\n../../../../site-packages\n", encoding="ascii"
        )
    if runtime.exists():
        _force_rmtree(runtime)  # an incomplete earlier attempt
    os.replace(staging, runtime)
    print(f"runtime: CPython {PYTHON_VERSION} standalone ready at {runtime}")
    return True


def norm_name(name: str) -> str:
    return re.sub(r"[-_.]+", "-", name).lower()


def locked_pins(reqs: Path) -> dict[str, str]:
    pins: dict[str, str] = {}
    for line in reqs.read_text(encoding="utf-8").splitlines():
        m = re.match(r"^([A-Za-z0-9][A-Za-z0-9._-]*)==([^\s\\;]+)", line)
        if m:
            marker_text = line.split(";", 1)[1].rstrip(" \\") if ";" in line else ""
            if marker_text:
                from packaging.markers import Marker, default_environment

                env = default_environment()
                env["python_version"], env["python_full_version"] = (
                    "3.12",
                    PYTHON_VERSION,
                )
                if not Marker(marker_text).evaluate(env):
                    continue
            pins[norm_name(m.group(1))] = m.group(2)
    return pins


def installed_pins(site_packages: Path) -> dict[str, str]:
    pins: dict[str, str] = {}
    if not site_packages.is_dir():
        return pins
    for d in site_packages.glob("*.dist-info"):
        m = re.match(r"^(.+?)-([^-]+)\.dist-info$", d.name)
        if m:
            pins[norm_name(m.group(1))] = m.group(2)
    return pins


def ensure_site_packages(
    paths: ColabOneInstallPaths, dry_run: bool, before_replace=None
) -> bool:
    """Make <install>\\site-packages match bridge/requirements.txt, installing with the
    pip of a host Python (cross-targeting CPython 3.12 / win_amd64, wheels only, every
    wheel checked against its pinned hash). Returns True when it had to install."""
    reqs = paths.repo / "bridge" / "requirements.txt"
    sp = paths.target_install / "site-packages"
    want = locked_pins(reqs)
    if not want:
        raise RuntimeError(f"{reqs} lists no pinned packages")
    if installed_pins(sp) == want and (sp / "colab_cli").is_dir():
        return False
    if dry_run:
        print(
            f"[dry-run] site-packages: would install {len(want)} pinned packages into {sp}"
        )
        return True
    pip_py = find_pip_python(paths)
    if pip_py is None:
        raise RuntimeError(
            "site-packages needs installing but no Python with pip was found. Run this script "
            "with a Python 3.10+ that has pip (e.g. 'python3 tools/install.py') or put one on PATH."
        )
    staging = paths.target_install / "site-packages.new"
    if staging.exists():
        _force_rmtree(staging)
    paths.target_install.mkdir(parents=True, exist_ok=True)
    if sys.platform == "win32":
        platform_args = ["--platform", "win_amd64"]
    elif sys.platform == "darwin" and platform.machine() == "arm64":
        platform_args = [
            "--platform",
            "macosx_12_0_arm64",
            "--platform",
            "macosx_11_0_arm64",
            "--platform",
            "macosx_10_15_universal2",
            "--platform",
            "macosx_10_13_universal2",
            "--platform",
            "macosx_10_9_universal2",
            "--implementation",
            "cp",
        ]
    else:
        raise SystemExit(
            f"Unsupported platform/arch: {sys.platform}/{platform.machine()}"
        )
    cmd = pip_py + [
        "-m",
        "pip",
        "install",
        "--disable-pip-version-check",
        "--no-compile",
        "--target",
        str(staging),
        *platform_args,
        "--python-version",
        f"{PYTHON_TAG[0]}.{PYTHON_TAG[1:]}",
        "--only-binary=:all:",
        "--no-deps",
        "--require-hashes",
        "-r",
        str(reqs),
    ]
    print(
        f"site-packages: installing {len(want)} pinned packages with {' '.join(pip_py)} -m pip ..."
    )
    r = subprocess.run(cmd, stdin=subprocess.DEVNULL)
    if r.returncode != 0:
        if staging.exists():
            _force_rmtree(staging)
        raise RuntimeError(
            "pip install of bridge/requirements.txt failed (see the output above)"
        )
    # pip --target also writes console-script launchers into bin\; the bridge uses none.
    for junk in ("bin", "__pycache__"):
        if (staging / junk).exists():
            _force_rmtree(staging / junk)
    got = installed_pins(staging)
    if got != want:
        _force_rmtree(staging)
        raise RuntimeError(
            f"pip installed {len(got)} packages, expected {len(want)} from the lock"
        )
    if before_replace:
        before_replace()
    if sp.exists():
        _force_rmtree(sp)
    os.replace(staging, sp)
    print(f"site-packages: {len(want)} packages installed at {sp}")
    return True


def _force_rmtree(path):
    shutil.rmtree(path)
