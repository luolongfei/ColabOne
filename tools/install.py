#!/usr/bin/env python3
"""Reproducible developer check and portable pure Go install; never starts a GPU."""

from __future__ import annotations

import argparse
import os
import platform
import shutil
import stat
import subprocess
import sys
import tempfile
import time
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
    machine = (platform.machine() or "").strip().lower()
    if sys.platform == "win32" and machine in ("", "x86", "i386", "i686"):
        wow_arch = (
            os.environ.get("PROCESSOR_ARCHITEW6432")
            or os.environ.get("PROCESSOR_ARCHITECTURE")
            or ""
        ).strip().lower()
        if wow_arch:
            machine = wow_arch
    if machine in ("arm64", "aarch64"):
        return "arm64"
    if machine in ("amd64", "x86_64", "x64"):
        return "amd64"
    if not machine:
        return "arm64" if sys.platform == "darwin" else "amd64"
    raise RuntimeError(f"Unsupported host CPU architecture: {machine!r}")


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
        gen_norm = out_file.read_bytes().replace(b"\r\n", b"\n").replace(b"\r", b"\n")
        com_norm = committed.read_bytes().replace(b"\r\n", b"\n").replace(b"\r", b"\n")
        if gen_norm != com_norm:
            raise RuntimeError(
                "frontend/src/colabOneBridge.ts is out of date; run `cd console && go tool mygo generate`"
            )


def run(args, cwd=ROOT, timeout=300, **kwargs):
    kwargs.setdefault("stdin", subprocess.DEVNULL)
    kwargs.setdefault("env", _clean_env())
    kwargs.setdefault("timeout", timeout)
    subprocess.run(args, cwd=cwd, check=True, **kwargs)


def _rename_with_retry(src: Path, dst: Path) -> None:
    """Rename a path with bounded Windows transient-lock retries."""
    max_attempts = 5 if os.name == "nt" else 1
    for attempt in range(max_attempts):
        try:
            src.rename(dst)
            return
        except OSError as exc:
            if (
                attempt + 1 >= max_attempts
                or os.name != "nt"
                or getattr(exc, "winerror", None) not in (5, 32, 33)
            ):
                raise
            time.sleep(0.05 * (attempt + 1))


def _replace_with_retry(src: Path, dst: Path) -> None:
    """Atomically replace a file with bounded Windows transient-lock retries."""
    max_attempts = 5 if os.name == "nt" else 1
    for attempt in range(max_attempts):
        try:
            os.replace(src, dst)
            return
        except OSError as exc:
            if (
                attempt + 1 >= max_attempts
                or os.name != "nt"
                or getattr(exc, "winerror", None) not in (5, 32, 33)
            ):
                raise
            time.sleep(0.05 * (attempt + 1))


def _rmtree_best_effort(path: Path, *, ignore_errors: bool = False) -> None:
    root_resolved = path.resolve() if path.exists() else path

    def _clear_readonly(func, target_path, _exc):
        try:
            parent_dir = os.path.dirname(target_path)
            if parent_dir and not os.path.islink(parent_dir):
                try:
                    if Path(parent_dir).resolve().is_relative_to(root_resolved):
                        parent_mode = os.stat(parent_dir).st_mode
                        os.chmod(
                            parent_dir,
                            stat.S_IMODE(parent_mode)
                            | stat.S_IWRITE
                            | stat.S_IREAD
                            | stat.S_IEXEC,
                        )
                except (OSError, ValueError):
                    pass
            if not os.path.islink(target_path):
                mode = stat.S_IWRITE | stat.S_IREAD
                if os.path.isdir(target_path):
                    mode |= stat.S_IEXEC
                try:
                    cur_mode = stat.S_IMODE(os.lstat(target_path).st_mode)
                    os.chmod(target_path, cur_mode | mode)
                except OSError:
                    os.chmod(target_path, mode)
            func(target_path)
        except OSError:
            if not ignore_errors:
                raise

    try:
        if sys.version_info >= (3, 12):
            shutil.rmtree(path, onexc=_clear_readonly)
        else:
            shutil.rmtree(path, onerror=_clear_readonly)
    except OSError:
        if not ignore_errors:
            raise


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
            _rename_with_retry(target, previous)
        try:
            _rename_with_retry(staged, target)
            installed = True
        except BaseException:
            if previous.exists() or previous.is_symlink():
                try:
                    _rename_with_retry(previous, target)
                except OSError:
                    pass
            raise
    finally:
        # If rollback itself fails, retain the previous tree for recovery.
        if installed or not (previous.exists() or previous.is_symlink()):
            _rmtree_best_effort(tmp, ignore_errors=True)


def replace_file(source: Path, target: Path) -> None:
    """Atomically stage and replace a single file in target.parent."""
    src_path = Path(source)
    if src_path.is_symlink() or not src_path.is_file():
        raise ValueError(f"Refusing to install non-regular source file: {source}")
    if target.is_symlink():
        raise ValueError(f"Refusing to overwrite symlink target: {target}")
    if target.is_dir():
        raise ValueError(f"Refusing to overwrite directory target: {target}")
    target.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp_name = tempfile.mkstemp(
        prefix=f".{target.name}.", suffix=".tmp", dir=target.parent
    )
    tmp_path = Path(tmp_name)
    try:
        try:
            dst_fh = os.fdopen(fd, "wb")
        except BaseException:
            os.close(fd)
            raise
        with dst_fh:
            with src_path.open("rb") as src_fh:
                shutil.copyfileobj(src_fh, dst_fh)
            dst_fh.flush()
            try:
                os.fsync(dst_fh.fileno())
            except OSError:
                pass
        shutil.copystat(src_path, tmp_path)
        if os.name == "nt":
            try:
                os.chmod(
                    tmp_path,
                    stat.S_IMODE(tmp_path.stat().st_mode) | stat.S_IWRITE,
                )
            except OSError:
                pass
            if target.exists() and not target.is_symlink():
                try:
                    os.chmod(
                        target,
                        stat.S_IMODE(target.stat().st_mode) | stat.S_IWRITE,
                    )
                except OSError:
                    pass
        _replace_with_retry(tmp_path, target)
    except BaseException:
        try:
            if tmp_path.exists() and not tmp_path.is_symlink():
                try:
                    os.chmod(
                        tmp_path,
                        stat.S_IMODE(tmp_path.stat().st_mode) | stat.S_IWRITE,
                    )
                except OSError:
                    pass
            tmp_path.unlink(missing_ok=True)
        except OSError:
            pass
        raise


def _find_built_app(platform_dir: Path, goos: str | None = None) -> Path:
    target_os = goos or _host_goos()
    if target_os == "darwin":
        preferred = platform_dir / "ColabOne.app"
        if not preferred.is_symlink() and bundle.app_binary_exists(preferred):
            return preferred
        candidates = [
            p for p in sorted(platform_dir.glob("*.app")) if not p.is_symlink()
        ]
    elif target_os == "windows":
        preferred = platform_dir / "ColabOne.exe"
        if not preferred.is_symlink() and bundle.app_binary_exists(preferred):
            return preferred
        candidates = [
            p
            for p in sorted(platform_dir.glob("*.exe"))
            if not p.is_symlink() and "-setup-" not in p.name.lower()
        ]
    else:
        for name in ("colabone", "ColabOne"):
            preferred = platform_dir / name
            if not preferred.is_symlink() and bundle.app_binary_exists(preferred):
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
            if not p.is_symlink()
            and p.is_file()
            and os.access(p, os.X_OK)
            and not p.name.startswith(".")
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
            _rmtree_best_effort(stale)
        elif stale.exists() or stale.is_symlink():
            stale.unlink()


def check(with_e2e: bool = False):
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
    if with_e2e:
        run(
            ["npx.cmd" if os.name == "nt" else "npx", "playwright", "test"],
            ROOT / "frontend",
            timeout=600,
        )


def install(root, check_only=False, build_installer=False, with_e2e=False):
    paths = bundle.ColabOneInstallPaths(root)
    if check_only:
        check(with_e2e=with_e2e)
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
    installed_mygo_files = target / ".mygo-files"
    if goos != "darwin" and mygo_files.is_file() and not mygo_files.is_symlink():
        replace_file(mygo_files, installed_mygo_files)
    elif installed_mygo_files.is_file() and not installed_mygo_files.is_symlink():
        installed_mygo_files.unlink(missing_ok=True)
    replace_tree(
        ROOT / "third_party",
        target / "third_party",
        ignore=shutil.ignore_patterns("__pycache__", "*.pyc", "test_*", "*.in"),
    )
    for name in ("README.md", "LICENSE", "NOTICE"):
        replace_file(ROOT / name, target / name)
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
        "--with-e2e",
        action="store_true",
        help="Also run frontend Playwright UI regression tests during --check",
    )
    parser.add_argument(
        "--installer",
        action="store_true",
        help="Build the native platform installer (.exe on Windows, .dmg on macOS)",
    )
    args = parser.parse_args()
    install(
        args.root,
        args.check,
        build_installer=args.installer,
        with_e2e=args.with_e2e,
    )


if __name__ == "__main__":
    main()
