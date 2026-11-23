#!/usr/bin/env python3
"""Sync or verify remote service files (server/, common/, catalog/) in console/internal/servicebundle/embedded/.

Keeps the pure Go desktop binary 100% self-contained without duplicating source ownership.
"""

from __future__ import annotations

import argparse
import shutil
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
EMBED_DIR = ROOT / "console" / "internal" / "servicebundle" / "embedded"
FOLDERS = ("server", "common", "catalog")
SUFFIXES = frozenset({".py", ".json", ".txt"})


def _collect_source_files(root: Path) -> dict[Path, Path]:
    files: dict[Path, Path] = {}
    for folder in FOLDERS:
        src_dir = root / folder
        if not src_dir.is_dir():
            continue
        for path in sorted(src_dir.glob("*"), key=lambda p: p.name):
            if path.is_file() and path.suffix in SUFFIXES:
                files[Path(folder) / path.name] = path
    return files


def sync_service_bundle(root: Path = ROOT, embed_dir: Path | None = None) -> int:
    target = embed_dir or (root / "console" / "internal" / "servicebundle" / "embedded")
    if target.exists():
        shutil.rmtree(target)
    target.mkdir(parents=True, exist_ok=True)
    source_files = _collect_source_files(root)
    for rel, src_path in source_files.items():
        dst_path = target / rel
        dst_path.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(src_path, dst_path)
    return len(source_files)


def check_service_bundle(root: Path = ROOT, embed_dir: Path | None = None) -> int:
    """Strictly verify embedded files match source files byte-for-byte without mutating disk."""
    target = embed_dir or (root / "console" / "internal" / "servicebundle" / "embedded")
    if not target.is_dir():
        raise RuntimeError(
            f"Embedded service bundle missing at {target}; run `python tools/sync_service_bundle.py`"
        )
    source_files = _collect_source_files(root)
    embedded_files: dict[Path, Path] = {}
    for path in sorted(target.rglob("*")):
        if path.is_file():
            embedded_files[path.relative_to(target)] = path

    missing = sorted(set(source_files) - set(embedded_files))
    extra = sorted(set(embedded_files) - set(source_files))
    mismatched = [
        rel
        for rel in sorted(set(source_files) & set(embedded_files))
        if source_files[rel].read_bytes() != embedded_files[rel].read_bytes()
    ]
    if missing or extra or mismatched:
        parts = []
        if missing:
            parts.append("missing=" + ", ".join(p.as_posix() for p in missing))
        if extra:
            parts.append("extra=" + ", ".join(p.as_posix() for p in extra))
        if mismatched:
            parts.append("mismatched=" + ", ".join(p.as_posix() for p in mismatched))
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
