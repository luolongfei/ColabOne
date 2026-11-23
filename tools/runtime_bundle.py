#!/usr/bin/env python3
"""Helpers for ColabOne's pure Go single-binary install and staging layout."""

from __future__ import annotations

import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

LEGACY_STAGE_DIRS = (
    "runtime",
    "site-packages",
    "bridge",
    "server",
    "common",
    "catalog",
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
        except OSError:
            pass


def desktop_folder() -> Path:
    """The real Desktop, which OneDrive or a policy may have moved off %USERPROFILE%."""
    if sys.platform != "win32":
        return Path.home() / "Desktop"
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


def app_binary_exists(path: Path) -> bool:
    return path.is_dir() if path.suffix == ".app" else path.is_file()


def app_executable(app_path: Path) -> Path:
    """Return the native executable path for a macOS .app bundle or standalone binary."""
    if app_path.suffix == ".app":
        return app_path / "Contents" / "MacOS" / app_path.stem
    return app_path


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
        elif sys.platform == "win32":
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
        else:
            if root:
                sandbox = Path(root).resolve()
                self.local_appdata = sandbox / ".local" / "share"
                self.target_install = sandbox / "Programs" / "ColabOne"
                self.target_config = self.local_appdata / "ColabOne"
                self.start_menu = self.local_appdata / "applications"
                self.desktop = sandbox / "Desktop"
            else:
                base = (os.environ.get("LOCALAPPDATA") or "").strip() or str(
                    Path.home() / ".local" / "share"
                )
                self.local_appdata = Path(base)
                self.target_install = self.local_appdata / "Programs" / "ColabOne"
                self.target_config = self.local_appdata / "ColabOne"
                self.start_menu = Path.home() / ".local" / "share" / "applications"
                self.desktop = Path.home() / "Desktop"
            self.target_exe = self.target_install / "colabone"

        self.console_src = self.repo / "console"
        self.cache_dir = Path(cache_dir) if cache_dir else self.target_config / "cache"


def find_go() -> str | None:
    found = shutil.which("go")
    if found:
        return found
    if sys.platform == "darwin":
        candidates = [Path("/opt/homebrew/bin/go"), Path("/usr/local/bin/go")]
    elif sys.platform == "win32":
        candidates = [
            Path(os.environ.get("LOCALAPPDATA", ""))
            / "Programs"
            / "Go"
            / "bin"
            / "go.exe"
        ]
    else:
        candidates = [
            Path.home() / ".local" / "bin" / "go",
            Path("/usr/local/go/bin/go"),
        ]
    for local in candidates:
        if local.is_file():
            go_dir = str(local.parent)
            if go_dir not in os.environ.get("PATH", "").split(os.pathsep):
                os.environ["PATH"] = go_dir + os.pathsep + os.environ.get("PATH", "")
            return str(local)
    return None


def go_env() -> dict[str, str]:
    env = dict(os.environ)
    env.setdefault("GOTOOLCHAIN", "auto")
    return env
