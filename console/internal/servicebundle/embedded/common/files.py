from __future__ import annotations
import hashlib
import json
import os
import tempfile
from pathlib import Path

COLABONE_SERVICE_BUNDLE_FOLDERS = ("server", "common", "catalog")
COLABONE_SERVICE_BUNDLE_SUFFIXES = (".py", ".json", ".txt")


def colabone_service_revision(root: Path) -> str:
    """Compute a deterministic short SHA-256 over the packaged remote service files."""
    base = Path(root)
    hasher = hashlib.sha256()
    count = 0
    for folder in COLABONE_SERVICE_BUNDLE_FOLDERS:
        folder_path = base / folder
        if not folder_path.is_dir():
            continue
        for path in sorted(folder_path.glob("*"), key=lambda p: p.name):
            if path.is_file() and path.suffix in COLABONE_SERVICE_BUNDLE_SUFFIXES:
                rel = f"{folder}/{path.name}"
                hasher.update(rel.encode("utf-8"))
                hasher.update(b"\0")
                hasher.update(path.read_bytes())
                hasher.update(b"\0")
                count += 1
    return hasher.hexdigest()[:16] if count else ""


def atomic_json(path: Path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temp = tempfile.mkstemp(prefix=".save-", suffix=".tmp", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(data, stream, ensure_ascii=False, indent=2)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temp, path)
    finally:
        if os.path.exists(temp):
            os.unlink(temp)
