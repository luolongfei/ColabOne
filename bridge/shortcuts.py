"""User-level Start Menu and Desktop shortcut management (no elevation required)."""

from __future__ import annotations

import ctypes
import os
import subprocess
import sys
import threading
from pathlib import Path

COLABONE_APP_NAME = "ColabOne"
COLABONE_APP_DESCRIPTION = "ColabOne — 远端 GPU 工作空间"
COLABONE_APP_DESCRIPTIONS = {
    "zh-CN": COLABONE_APP_DESCRIPTION,
    "en-US": "ColabOne — Remote GPU Workspace",
    "zh-TW": "ColabOne — 遠端 GPU 工作區",
    "zh-HK": "ColabOne — 遠端 GPU 工作空間",
    "ja-JP": "ColabOne — リモート GPU ワークスペース",
    "ko-KR": "ColabOne — 원격 GPU 워크스페이스",
}
_CREATE_NO_WINDOW = 0x08000000
_win_desktop_lock = threading.Lock()
_cached_win_desktop: Path | None = None


def colabone_app_description(locale: str = "zh-CN") -> str:
    return COLABONE_APP_DESCRIPTIONS.get(locale, COLABONE_APP_DESCRIPTION)


def _win_known_desktop_path() -> Path | None:
    if sys.platform != "win32":
        return None
    try:
        from ctypes import wintypes

        class _GUID(ctypes.Structure):
            _fields_ = [
                ("Data1", wintypes.DWORD),
                ("Data2", wintypes.WORD),
                ("Data3", wintypes.WORD),
                ("Data4", ctypes.c_ubyte * 8),
            ]

        # FOLDERID_Desktop = {B4BFCC3A-DB2C-424C-B029-7FE99A87C641}
        folder_id = _GUID(
            0xB4BFCC3A,
            0xDB2C,
            0x424C,
            (ctypes.c_ubyte * 8)(0xB0, 0x29, 0x7F, 0xE9, 0x9A, 0x87, 0xC6, 0x41),
        )
        shell32 = ctypes.windll.shell32
        ole32 = ctypes.windll.ole32
        ptr = ctypes.c_wchar_p()
        hr = shell32.SHGetKnownFolderPath(
            ctypes.byref(folder_id), 0, None, ctypes.byref(ptr)
        )
        try:
            if hr == 0 and ptr.value:
                return Path(ptr.value)
        finally:
            if ptr.value:
                ole32.CoTaskMemFree(ptr)
    except Exception:
        pass
    return None


def _win_desktop_dir(query_shell: bool = False) -> Path:
    global _cached_win_desktop
    with _win_desktop_lock:
        if _cached_win_desktop is not None:
            return _cached_win_desktop
    known = _win_known_desktop_path()
    if known is not None:
        with _win_desktop_lock:
            _cached_win_desktop = known
        return known
    if query_shell and sys.platform == "win32":
        try:
            r = subprocess.run(
                [
                    "powershell.exe",
                    "-NoProfile",
                    "-NonInteractive",
                    "-Command",
                    "[Console]::OutputEncoding=[System.Text.Encoding]::UTF8; [Environment]::GetFolderPath('Desktop')",
                ],
                capture_output=True,
                text=True,
                stdin=subprocess.DEVNULL,
                timeout=8,
                creationflags=_CREATE_NO_WINDOW,
            )
            found = r.stdout.strip()
            if r.returncode == 0 and found:
                resolved = Path(found)
                with _win_desktop_lock:
                    _cached_win_desktop = resolved
                return resolved
        except Exception:
            pass
    onedrive = os.environ.get("OneDrive") or os.environ.get("OneDriveConsumer") or ""
    if onedrive and (Path(onedrive) / "Desktop").is_dir():
        return Path(onedrive) / "Desktop"
    profile = os.environ.get("USERPROFILE") or str(Path.home())
    return Path(profile) / "Desktop"


def shortcut_paths(
    sandbox_dir: Path | None = None,
    platform_name: str | None = None,
    query_shell: bool = False,
) -> tuple[Path, Path]:
    plat = platform_name or sys.platform
    if sandbox_dir is not None:
        ext = ".lnk" if plat == "win32" else (".app" if plat == "darwin" else ".desktop")
        return (
            sandbox_dir / "StartMenu" / f"{COLABONE_APP_NAME}{ext}",
            sandbox_dir / "Desktop" / f"{COLABONE_APP_NAME}{ext}",
        )
    if plat == "win32":
        appdata = os.environ.get("APPDATA") or str(
            Path.home() / "AppData" / "Roaming"
        )
        start_menu = (
            Path(appdata)
            / "Microsoft"
            / "Windows"
            / "Start Menu"
            / "Programs"
            / f"{COLABONE_APP_NAME}.lnk"
        )
        desktop = _win_desktop_dir(query_shell=query_shell) / f"{COLABONE_APP_NAME}.lnk"
        return start_menu, desktop
    if plat == "darwin":
        return (
            Path.home() / "Applications" / f"{COLABONE_APP_NAME}.app",
            Path.home() / "Desktop" / f"{COLABONE_APP_NAME}.app",
        )
    return (
        Path.home() / ".local" / "share" / "applications" / "colabone.desktop",
        Path.home() / "Desktop" / f"{COLABONE_APP_NAME}.desktop",
    )


def _exists_or_link(path: Path) -> bool:
    return path.exists() or path.is_symlink()


def shortcut_status(
    sandbox_dir: Path | None = None, platform_name: str | None = None
) -> dict:
    start_path, desktop_path = shortcut_paths(
        sandbox_dir=sandbox_dir, platform_name=platform_name, query_shell=False
    )
    return {
        "start_menu": _exists_or_link(start_path),
        "desktop": _exists_or_link(desktop_path),
    }


def _resolve_target_binary(root: Path, plat: str) -> Path:
    if plat == "win32":
        candidates = [
            root / f"{COLABONE_APP_NAME}.exe",
            Path(sys.executable).resolve().parents[1] / f"{COLABONE_APP_NAME}.exe",
        ]
        for cand in candidates:
            if cand.is_file():
                return cand
        return root / f"{COLABONE_APP_NAME}.exe"
    if plat == "darwin":
        candidates = [
            root / f"{COLABONE_APP_NAME}.app",
            root.parent.parent if root.parent.parent.suffix == ".app" else root / f"{COLABONE_APP_NAME}.app",
        ]
        for cand in candidates:
            if cand.is_dir():
                return cand
        return root / f"{COLABONE_APP_NAME}.app"
    candidates = [root / COLABONE_APP_NAME, root / "colabone"]
    for cand in candidates:
        if cand.is_file():
            return cand
    return root / COLABONE_APP_NAME


def _write_windows_lnk(lnk_path: Path, exe_path: Path, locale: str = "zh-CN"):
    lnk_path.parent.mkdir(parents=True, exist_ok=True)
    env = dict(
        os.environ,
        _CO_LNK=str(lnk_path),
        _CO_EXE=str(exe_path),
        _CO_WORKDIR=str(exe_path.parent),
        _CO_DESC=colabone_app_description(locale),
    )
    ps_script = (
        "$ws = New-Object -ComObject WScript.Shell; "
        "$sc = $ws.CreateShortcut($env:_CO_LNK); "
        "$sc.TargetPath = $env:_CO_EXE; "
        "$sc.WorkingDirectory = $env:_CO_WORKDIR; "
        "$sc.IconLocation = ($env:_CO_EXE + ',0'); "
        "$sc.Description = $env:_CO_DESC; "
        "$sc.Save()"
    )
    r = subprocess.run(
        ["powershell.exe", "-NoProfile", "-NonInteractive", "-Command", ps_script],
        capture_output=True,
        text=True,
        env=env,
        stdin=subprocess.DEVNULL,
        timeout=15,
        creationflags=_CREATE_NO_WINDOW if sys.platform == "win32" else 0,
    )
    if r.returncode != 0 or not lnk_path.is_file():
        raise RuntimeError("创建快捷方式失败，请检查系统权限后重试")


def manage_shortcuts(
    root: Path,
    target: str = "start_menu",
    remove: bool = False,
    sandbox_dir: Path | None = None,
    platform_name: str | None = None,
    locale: str = "zh-CN",
) -> dict:
    if target not in ("start_menu", "desktop", "both"):
        raise ValueError("快捷方式目标不合法")
    plat = platform_name or sys.platform
    start_path, desktop_path = shortcut_paths(
        sandbox_dir=sandbox_dir,
        platform_name=plat,
        query_shell=(sandbox_dir is None and not remove),
    )
    selected: list[Path] = []
    if target in ("start_menu", "both"):
        selected.append(start_path)
    if target in ("desktop", "both"):
        selected.append(desktop_path)

    if remove:
        for p in selected:
            try:
                if p.is_symlink() or p.is_file():
                    p.unlink()
            except OSError as exc:
                raise RuntimeError("移除快捷方式失败：" + str(exc)) from exc
        return shortcut_status(sandbox_dir=sandbox_dir, platform_name=plat)

    binary = _resolve_target_binary(root, plat)
    if sandbox_dir is not None:
        for p in selected:
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_text(f"target={binary}\nworkdir={binary.parent}\n", encoding="utf-8")
        return shortcut_status(sandbox_dir=sandbox_dir, platform_name=plat)

    if plat == "win32":
        if not binary.is_file():
            raise RuntimeError("未在当前目录找到 ColabOne.exe，请在完整便携包目录中操作")
        for p in selected:
            _write_windows_lnk(p, binary, locale=locale)
    elif plat == "darwin":
        if not binary.is_dir():
            raise RuntimeError("未找到 ColabOne.app 应用包")
        for p in selected:
            p.parent.mkdir(parents=True, exist_ok=True)
            if p.is_symlink():
                p.unlink()
            elif p.exists():
                continue
            p.symlink_to(binary)
    else:
        icon_path = root / "icon.png"
        if not icon_path.is_file():
            icon_path = root / "console" / "resources" / "icon.png"
        desktop_entry = (
            "[Desktop Entry]\n"
            "Type=Application\n"
            f"Name={COLABONE_APP_NAME}\n"
            f"Comment={colabone_app_description(locale)}\n"
            f'Exec="{binary}"\n'
            f'Path="{binary.parent}"\n'
            f"Icon={icon_path}\n"
            "Terminal=false\n"
            "Categories=Development;Utility;\n"
        )
        for p in selected:
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_text(desktop_entry, encoding="utf-8")
            p.chmod(0o755)

    return shortcut_status(sandbox_dir=sandbox_dir, platform_name=plat)
