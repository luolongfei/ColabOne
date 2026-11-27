#!/usr/bin/env python3
"""Sync or verify remote service files (server/, common/, catalog/) in console/internal/servicebundle/embedded/.

Keeps the pure Go desktop binary 100% self-contained without duplicating source ownership.
"""

from __future__ import annotations

import argparse
import os
import shutil
import stat
import sys
import tempfile
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from common.files import (  # noqa: E402
    COLABONE_SERVICE_BUNDLE_FOLDERS,
    COLABONE_SERVICE_BUNDLE_SUFFIXES,
)

EMBED_DIR = ROOT / "console" / "internal" / "servicebundle" / "embedded"
FOLDERS = COLABONE_SERVICE_BUNDLE_FOLDERS
SUFFIXES = frozenset(COLABONE_SERVICE_BUNDLE_SUFFIXES)


def _collect_source_files(root: Path) -> dict[Path, Path]:
    root_resolved = root.resolve()
    files: dict[Path, Path] = {}
    for folder in FOLDERS:
        src_dir = root / folder
        if src_dir.is_symlink():
            raise RuntimeError(f"Refusing symlinked source directory: {src_dir}")
        if not src_dir.is_dir():
            continue
        for path in sorted(src_dir.glob("*"), key=lambda p: p.name):
            if path.suffix not in SUFFIXES:
                continue
            if path.is_symlink():
                raise RuntimeError(f"Refusing symlinked source file: {path}")
            if not path.is_file():
                continue
            try:
                path.resolve(strict=True).relative_to(root_resolved)
            except (OSError, ValueError) as exc:
                raise RuntimeError(f"Source file escapes repository root: {path}") from exc
            files[Path(folder) / path.name] = path
    return files


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


def _rmtree_best_effort(path: Path) -> None:
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
            pass

    try:
        if sys.version_info >= (3, 12):
            shutil.rmtree(path, onexc=_clear_readonly)
        else:
            shutil.rmtree(path, onerror=_clear_readonly)
    except OSError:
        pass


def sync_service_bundle(root: Path = ROOT, embed_dir: Path | None = None) -> int:
    target = embed_dir or (root / "console" / "internal" / "servicebundle" / "embedded")
    target.parent.mkdir(parents=True, exist_ok=True)
    source_files = _collect_source_files(root)
    if not source_files:
        raise RuntimeError(
            f"No remote service bundle source files found under {root}; refusing to clear {target}"
        )

    # Stage the entire replacement tree into a sibling temp dir before touching target,
    # and roll back cleanly if directory swap fails.
    tmp_root = Path(
        tempfile.mkdtemp(prefix=f".{target.name}-stage-", dir=target.parent)
    )
    staged = tmp_root / "new"
    previous = tmp_root / "previous"
    staged.mkdir(parents=True, exist_ok=True)
    installed = False
    try:
        for rel, src_path in source_files.items():
            dst_path = staged / rel
            dst_path.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(src_path, dst_path)
            if dst_path.read_bytes() != src_path.read_bytes():
                raise RuntimeError(f"Staged bundle verification failed for {rel.as_posix()}")

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
        if installed or not (previous.exists() or previous.is_symlink()):
            _rmtree_best_effort(tmp_root)
    return len(source_files)


def check_service_bundle(root: Path = ROOT, embed_dir: Path | None = None) -> int:
    """Strictly verify embedded files match source files byte-for-byte without mutating disk."""
    target = embed_dir or (root / "console" / "internal" / "servicebundle" / "embedded")
    if target.is_symlink() or not target.is_dir():
        raise RuntimeError(
            f"Embedded service bundle missing at {target}; run `python tools/sync_service_bundle.py`"
        )
    source_files = _collect_source_files(root)
    if not source_files:
        raise RuntimeError(
            f"No remote service bundle source files found under {root}"
        )
    embedded_files: dict[Path, Path] = {}
    embedded_dirs: set[Path] = set()
    symlinks: list[Path] = []
    for path in sorted(target.rglob("*")):
        rel_path = path.relative_to(target)
        if path.is_symlink():
            symlinks.append(rel_path)
            continue
        if path.is_dir():
            embedded_dirs.add(rel_path)
            continue
        if path.is_file():
            embedded_files[rel_path] = path

    expected_dirs = {rel.parent for rel in source_files if rel.parent != Path(".")}
    extra_dirs = sorted(embedded_dirs - expected_dirs)
    missing = sorted(set(source_files) - set(embedded_files))
    extra = sorted(set(embedded_files) - set(source_files))
    mismatched = [
        rel
        for rel in sorted(set(source_files) & set(embedded_files))
        if source_files[rel].read_bytes() != embedded_files[rel].read_bytes()
    ]
    if missing or extra or extra_dirs or mismatched or symlinks:
        parts = []
        if missing:
            parts.append("missing=" + ", ".join(p.as_posix() for p in missing))
        if extra:
            parts.append("extra=" + ", ".join(p.as_posix() for p in extra))
        if extra_dirs:
            parts.append("extra_dirs=" + ", ".join(p.as_posix() for p in extra_dirs))
        if mismatched:
            parts.append("mismatched=" + ", ".join(p.as_posix() for p in mismatched))
        if symlinks:
            parts.append("symlinks=" + ", ".join(p.as_posix() for p in symlinks))
        raise RuntimeError(
            "Embedded service bundle is out of sync ("
            + "; ".join(parts)
            + "); run `python tools/sync_service_bundle.py`"
        )
    return len(source_files)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--check",
        action="store_true",
        help="Verify embedded bundle matches source files byte-for-byte without modifying files",
    )
    args = parser.parse_args()
    if args.check:
        try:
            count = check_service_bundle()
        except RuntimeError as exc:
            print(str(exc), file=sys.stderr)
            raise SystemExit(1) from exc
        print(f"Verified {count} embedded remote service bundle files in {EMBED_DIR}")
        return
    count = sync_service_bundle()
    print(f"Synced {count} remote service bundle files into {EMBED_DIR}")


if __name__ == "__main__":
    main()
