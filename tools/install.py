#!/usr/bin/env python3
"""Reproducible developer check and portable pure Go install; never starts a GPU."""

from __future__ import annotations

import argparse
import os
import platform
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

if __package__:
    from . import runtime_bundle as bundle
    from .sync_service_bundle import check_service_bundle, sync_service_bundle
else:
    import runtime_bundle as bundle
    from sync_service_bundle import check_service_bundle, sync_service_bundle

ROOT = Path(__file__).resolve().parents[1]


def _clean_env():
    env = bundle.go_env()
    extra_bins = []
    local_bin = str(Path.home() / ".local/bin")
    if Path(local_bin).is_dir():
        extra_bins.append(local_bin)
    try:
        go_bin = bundle.find_go()
        if go_bin:
            go_dir = str(Path(go_bin).resolve().parent)
            if Path(go_dir).is_dir():
                extra_bins.append(go_dir)
    except Exception:
        pass
    parts = env.get("PATH", "").split(os.pathsep)
    for d in reversed(extra_bins):
        if d not in parts:
            parts.insert(0, d)
    env["PATH"] = os.pathsep.join(p for p in parts if p)
    sysroot_lib = str(Path.home() / ".local/sysroot/usr/lib/x86_64-linux-gnu")
    if Path(sysroot_lib).is_dir():
        existing = env.get("LD_LIBRARY_PATH", "")
        if sysroot_lib not in existing.split(os.pathsep):
            env["LD_LIBRARY_PATH"] = (
                sysroot_lib + (os.pathsep + existing if existing else "")
            )
    return env


def _host_goos() -> str:
    if sys.platform == "darwin":
        return "darwin"
    if sys.platform == "win32":
        return "windows"
    return "linux"


def _host_goarch() -> str:
    machine = platform.machine().lower()
    if machine in ("arm64", "aarch64"):
        return "arm64"
    if machine in ("amd64", "x86_64", "x64"):
        return "amd64"
    return "arm64" if sys.platform == "darwin" else "amd64"


def _verify_mygo_bindings_up_to_date() -> None:
    committed = ROOT / "frontend" / "src" / "colabOneBridge.ts"
    if not committed.is_file():
        raise RuntimeError(
            f"Missing generated bindings {committed}; run `cd console && go tool mygo generate`"
        )
    with tempfile.TemporaryDirectory(prefix="colabone-mygo-check-") as tmp:
        out_file = Path(tmp) / "colabOneBridge.ts"
        run(["go", "tool", "mygo", "generate", "-o", str(out_file)], ROOT / "console")
        if not out_file.is_file():
            raise RuntimeError(
                f"`go tool mygo generate -o {out_file}` did not produce output file"
            )
        if out_file.read_bytes() != committed.read_bytes():
            raise RuntimeError(
                "frontend/src/colabOneBridge.ts is out of date; run `cd console && go tool mygo generate`"
            )


def run(args, cwd=ROOT, timeout=300, **kwargs):
    kwargs.setdefault("stdin", subprocess.DEVNULL)
    kwargs.setdefault("env", _clean_env())
    kwargs.setdefault("timeout", timeout)
    subprocess.run(args, cwd=cwd, check=True, **kwargs)


def replace_tree(source, target, **kwargs):
    """Stage a complete owned code tree, replacing deleted resources as well.

    Merging app bundles leaves obsolete signed files and resources behind.
    Keep the previous tree until the staged replacement has been moved in place.
    User settings and credentials live outside these code trees.
    """
    target.parent.mkdir(parents=True, exist_ok=True)
    tmp = Path(tempfile.mkdtemp(prefix=".colabone-update-", dir=target.parent))
    staged, previous = tmp / "new", tmp / "previous"
    installed = False
    try:
        shutil.copytree(source, staged, symlinks=True, **kwargs)
        if target.exists() or target.is_symlink():
            target.rename(previous)
        try:
            staged.rename(target)
            installed = True
        except BaseException:
            if previous.exists() or previous.is_symlink():
                previous.rename(target)
            raise
    finally:
        # If rollback itself fails, retain the previous tree for recovery.
        if installed or not (previous.exists() or previous.is_symlink()):
            shutil.rmtree(tmp)


def replace_file(source: Path, target: Path) -> None:
    """Atomically stage and replace a single file in target.parent."""
    target.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp_name = tempfile.mkstemp(
        prefix=f".{target.name}.", suffix=".tmp", dir=target.parent
    )
    os.close(fd)
    tmp_path = Path(tmp_name)
    try:
        shutil.copy2(source, tmp_path)
        os.replace(tmp_path, target)
    except BaseException:
        tmp_path.unlink(missing_ok=True)
        raise


def _find_built_app(platform_dir: Path, goos: str | None = None) -> Path:
    target_os = goos or _host_goos()
    if target_os == "darwin":
        preferred = platform_dir / "ColabOne.app"
        if bundle.app_binary_exists(preferred):
            return preferred
        candidates = sorted(platform_dir.glob("*.app"))
    elif target_os == "windows":
        preferred = platform_dir / "ColabOne.exe"
        if bundle.app_binary_exists(preferred):
            return preferred
        candidates = [
            p
            for p in sorted(platform_dir.glob("*.exe"))
            if "-setup-" not in p.name.lower()
        ]
    else:
        for name in ("colabone", "ColabOne"):
            preferred = platform_dir / name
            if bundle.app_binary_exists(preferred):
                return preferred
        ignored_suffixes = (
            ".deb",
            ".rpm",
            ".gz",
            ".zip",
            ".zst",
            ".xz",
            ".sha256",
            ".txt",
            ".sig",
            ".json",
            ".delta",
        )
        entries = sorted(platform_dir.iterdir()) if platform_dir.is_dir() else []
        candidates = [
            p
            for p in entries
            if p.is_file()
            and p.name != "PKGBUILD"
            and "-setup-" not in p.name.lower()
            and not p.name.lower().endswith(ignored_suffixes)
        ]
    if not candidates:
        raise RuntimeError(f"Desktop build artifact not found in {platform_dir}")
    return candidates[0]


def _remove_legacy_stage_dirs(target: Path) -> None:
    for name in bundle.LEGACY_STAGE_DIRS:
        stale = target / name
        if stale.is_dir() and not stale.is_symlink():
            shutil.rmtree(stale)
        elif stale.exists() or stale.is_symlink():
            stale.unlink()


def check():
    run(
        [
            sys.executable,
            "-m",
            "compileall",
            "-q",
            "server",
            "common",
            "tools",
        ]
    )
    check_service_bundle(ROOT)
    _verify_mygo_bindings_up_to_date()
    run(["npm.cmd" if os.name == "nt" else "npm", "run", "build"], ROOT / "frontend")
    run(["go", "vet", "./..."], ROOT / "console")
    run(["go", "test", "./..."], ROOT / "console")
    run([sys.executable, "-m", "pytest", "-q", "tests"])
    run(["npm.cmd" if os.name == "nt" else "npm", "test"], ROOT / "frontend")
    run(["go", "run", ".", "--self-test"], ROOT / "console")


def install(root, check_only=False, build_installer=False):
    paths = bundle.ColabOneInstallPaths(root)
    if check_only:
        check()
        return paths.target_install
    sync_service_bundle(ROOT)
    goos = _host_goos()
    goarch = _host_goarch()
    build_cmd = [
        "go",
        "tool",
        "mygo",
        "build",
        "-platform",
        f"{goos}/{goarch}",
    ]
    if not build_installer:
        if goos == "darwin":
            build_cmd.append("-skip-dmg")
        elif goos == "windows":
            build_cmd.append("-skip-installer")
    run(build_cmd, ROOT / "console", timeout=600 if build_installer else 300)
    platform_dir = ROOT / "console/dist" / f"{goos}-{goarch}"
    app = _find_built_app(platform_dir, goos)
    target = paths.target_install
    target.mkdir(parents=True, exist_ok=True)
    _remove_legacy_stage_dirs(target)
    installed_app = target / app.name
    if app.is_dir():
        replace_tree(app, installed_app)
    else:
        replace_file(app, installed_app)
    mygo_files = platform_dir / ".mygo-files"
    if goos != "darwin" and mygo_files.is_file():
        shutil.copy2(mygo_files, target / ".mygo-files")
    replace_tree(
        ROOT / "third_party",
        target / "third_party",
        ignore=shutil.ignore_patterns("__pycache__", "*.pyc", "test_*", "*.in"),
    )
    for name in ("README.md", "LICENSE", "NOTICE"):
        shutil.copy2(ROOT / name, target / name)
    run(
        [str(bundle.app_executable(installed_app)), "--self-test"],
        cwd=target,
    )
    print("Portable installation:", target)
    return target


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", required=True, help="Isolated installation root")
    parser.add_argument("--check", action="store_true")
    parser.add_argument(
        "--installer",
        action="store_true",
        help="Build the native platform installer (.exe on Windows, .dmg on macOS)",
    )
    args = parser.parse_args()
    install(args.root, args.check, build_installer=args.installer)


if __name__ == "__main__":
    main()
