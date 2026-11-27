from __future__ import annotations
import hashlib
import json
import os
import tempfile
from pathlib import Path

COLABONE_SERVICE_BUNDLE_FOLDERS = ("server", "common", "catalog")
COLABONE_SERVICE_BUNDLE_SUFFIXES = (".py", ".json", ".txt")

COLABONE_SENSITIVE_SUBPROCESS_ENV_KEYS = frozenset(
    {
        "COLABONE_REMOTE_CONFIG",
        "HF_TOKEN",
        "HUGGING_FACE_HUB_TOKEN",
        "HUGGINGFACE_HUB_TOKEN",
        "HF_API_TOKEN",
        "OPENAI_API_KEY",
        "TUNNEL_TOKEN",
        "TBE_RUNTIME_ADDR",
    }
)
COLABONE_SENSITIVE_SUBPROCESS_ENV_PREFIXES = ("TUNNEL_", "RCLONE_")


def _is_sensitive_subprocess_env_key(
    key: str,
    extra_strip: frozenset[str] | set[str] | tuple[str, ...] = (),
    extra_prefixes: tuple[str, ...] = (),
) -> bool:
    upper_key = key.upper()
    if upper_key in COLABONE_SENSITIVE_SUBPROCESS_ENV_KEYS or upper_key in extra_strip:
        return True
    if any(upper_key.startswith(prefix) for prefix in COLABONE_SENSITIVE_SUBPROCESS_ENV_PREFIXES):
        return True
    if extra_prefixes and any(upper_key.startswith(prefix) for prefix in extra_prefixes):
        return True
    return False


def colabone_sanitized_subprocess_env(
    extra: dict[str, str] | None = None,
    *,
    strip_keys: frozenset[str] | set[str] | tuple[str, ...] = (),
    strip_prefixes: tuple[str, ...] | list[str] | set[str] = (),
    allow_extra_sensitive: bool = False,
) -> dict[str, str]:
    """Return a copy of os.environ with all ColabOne secrets and tool overrides stripped."""
    extra_strip = frozenset(str(k).upper() for k in strip_keys) if strip_keys else frozenset()
    norm_prefixes = tuple(str(p).upper() for p in strip_prefixes if p)
    env = {
        k: v
        for k, v in os.environ.items()
        if not _is_sensitive_subprocess_env_key(k, extra_strip, norm_prefixes)
    }
    if extra:
        for k, v in extra.items():
            if v is None:
                continue
            key_str = str(k)
            if not allow_extra_sensitive and _is_sensitive_subprocess_env_key(
                key_str, extra_strip, norm_prefixes
            ):
                continue
            env[key_str] = str(v)
    return env


def colabone_service_revision(root: Path) -> str:
    """Compute a deterministic short SHA-256 over the packaged remote service files."""
    base = Path(root)
    base_resolved = base.resolve()
    hasher = hashlib.sha256()
    count = 0
    for folder in COLABONE_SERVICE_BUNDLE_FOLDERS:
        folder_path = base / folder
        if folder_path.is_symlink() or not folder_path.is_dir():
            continue
        for path in sorted(folder_path.glob("*"), key=lambda p: p.name):
            if path.is_symlink() or not path.is_file() or path.suffix not in COLABONE_SERVICE_BUNDLE_SUFFIXES:
                continue
            try:
                path.resolve(strict=True).relative_to(base_resolved)
            except (OSError, ValueError):
                continue
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
